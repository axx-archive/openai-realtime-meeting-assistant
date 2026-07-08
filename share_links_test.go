package main

// Wave 3 item 14 (+14b wiring) pins: share-link mint auth + the server-side
// approved/final status gate, token expiry/revocation, the public /a/<token>
// route serving each artifact type at full fidelity while logging the open,
// the PDF export trigger's 503-when-absent / enqueue-when-present split, and
// the render callback's auth + asset append.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shareLinkTestEnv mirrors dealRoomTestEnv: auth env + an isolated app as the
// global kanbanApp, returning admin + member cookies.
func shareLinkTestEnv(t *testing.T) (adminCookies, memberCookies []*http.Cookie) {
	t.Helper()
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	return loginAs(t, "aj@shareability.com", "B0NFIRE!"), loginAs(t, "tim@shareability.com", "B0NFIRE!")
}

func shareLinkRequest(t *testing.T, method, path, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	switch {
	case path == "/artifacts/share" || strings.HasPrefix(path, "/artifacts/share?"):
		artifactShareHandler(recorder, req)
	case strings.HasPrefix(path, "/a/"):
		shareLinkPublicHandler(recorder, req)
	case path == "/artifacts/export-pdf":
		artifactExportPDFHandler(recorder, req)
	default:
		t.Fatalf("unknown share link path %q", path)
	}
	return recorder
}

// seedShareArtifact creates an artifact with the given status and body.
func seedShareArtifact(t *testing.T, status string, body string, extra map[string]string) meetingMemoryEntry {
	t.Helper()
	metadata := map[string]string{"status": status, "title": "Aurora deck"}
	for key, value := range extra {
		metadata[key] = value
	}
	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Aurora", body, "AJ", metadata)
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	return artifact
}

func mintShareLinkForTest(t *testing.T, artifactID string, cookies []*http.Cookie) map[string]any {
	t.Helper()
	recorder := shareLinkRequest(t, http.MethodPost, "/artifacts/share", fmt.Sprintf(`{"artifactId":%q}`, artifactID), cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("mint status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	link, _ := decodeJSON(t, recorder)["link"].(map[string]any)
	if link == nil || strings.TrimSpace(fmt.Sprint(link["url"])) == "" {
		t.Fatalf("mint returned no link url: %s", recorder.Body.String())
	}
	return link
}

func shareOpenedSignalCount(t *testing.T, artifactID string) int {
	t.Helper()
	count := 0
	for _, entry := range kanbanApp.memory.entriesOfKind(meetingMemoryKindSignal, 0) {
		record, ok := decodeSignalEntry(entry)
		if ok && record.Event == signalEventShareOpened && record.ArtifactID == artifactID {
			count++
		}
	}
	return count
}

// Minting is session-gated AND status-gated server-side: signed out is 401,
// a draft artifact is a 400 with the status named, approved mints a live
// /a/ URL with the default expiry window.
func TestShareLinkMintAuthAndStatusGate(t *testing.T) {
	_, member := shareLinkTestEnv(t)
	draft := seedShareArtifact(t, "draft", "# Aurora\n\nnot ready", nil)
	approved := seedShareArtifact(t, "approved", "# Aurora\n\nready to ship", nil)

	if recorder := shareLinkRequest(t, http.MethodPost, "/artifacts/share", fmt.Sprintf(`{"artifactId":%q}`, approved.ID), nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out mint status=%d, want 401", recorder.Code)
	}

	recorder := shareLinkRequest(t, http.MethodPost, "/artifacts/share", fmt.Sprintf(`{"artifactId":%q}`, draft.ID), member)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("draft mint status=%d body=%s, want 400", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "approved") {
		t.Fatalf("draft mint error must name the gate, got %s", recorder.Body.String())
	}

	link := mintShareLinkForTest(t, approved.ID, member)
	if !strings.HasPrefix(fmt.Sprint(link["url"]), "/a/") {
		t.Fatalf("link url=%v, want an /a/ path", link["url"])
	}
	expires, err := time.Parse(time.RFC3339Nano, fmt.Sprint(link["expiresAt"]))
	if err != nil {
		t.Fatalf("expiresAt=%v is not RFC3339Nano: %v", link["expiresAt"], err)
	}
	if days := time.Until(expires).Hours() / 24; days < float64(shareLinkDefaultExpiryDays)-1 || days > float64(shareLinkDefaultExpiryDays)+1 {
		t.Fatalf("default expiry is %.1f days out, want ~%d", days, shareLinkDefaultExpiryDays)
	}

	// The system's REAL human-approval record mints too: the admin approve
	// stamp (humanApprovedAt) on landed (complete/published) work.
	humanApproved := seedShareArtifact(t, "complete", "# Aurora\n\nshipped cut", map[string]string{
		artifactHumanApprovedAtKey: time.Now().UTC().Format(time.RFC3339Nano),
		artifactHumanApprovedByKey: "AJ",
	})
	mintShareLinkForTest(t, humanApproved.ID, member)

	// The untracked "final" alias does NOT mint — nothing produces it, and it
	// must never bypass the human-approval requirement.
	final := seedShareArtifact(t, "final", "# Aurora\n\nfinal cut", nil)
	if recorder := shareLinkRequest(t, http.MethodPost, "/artifacts/share", fmt.Sprintf(`{"artifactId":%q}`, final.ID), member); recorder.Code != http.StatusBadRequest {
		t.Fatalf("'final' status mint status=%d, want 400", recorder.Code)
	}

	// The stamp alone is not enough either: approved-but-still-running
	// external work never leaks early.
	stillRunning := seedShareArtifact(t, "running", "# Aurora\n\nin flight", map[string]string{
		artifactHumanApprovedAtKey: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if recorder := shareLinkRequest(t, http.MethodPost, "/artifacts/share", fmt.Sprintf(`{"artifactId":%q}`, stillRunning.ID), member); recorder.Code != http.StatusBadRequest {
		t.Fatalf("human-approved running mint status=%d, want 400", recorder.Code)
	}

	// expiresDays clamps to the maximum window.
	over := shareLinkRequest(t, http.MethodPost, "/artifacts/share", fmt.Sprintf(`{"artifactId":%q,"expiresDays":100000}`, approved.ID), member)
	if over.Code != http.StatusOK {
		t.Fatalf("clamped mint status=%d body=%s", over.Code, over.Body.String())
	}
	overLink, _ := decodeJSON(t, over)["link"].(map[string]any)
	overExpires, err := time.Parse(time.RFC3339Nano, fmt.Sprint(overLink["expiresAt"]))
	if err != nil {
		t.Fatalf("clamped expiresAt parse: %v", err)
	}
	if time.Until(overExpires).Hours()/24 > float64(shareLinkMaxExpiryDays)+1 {
		t.Fatalf("expiresDays was not clamped to %d days", shareLinkMaxExpiryDays)
	}
}

// The public route serves each type at full fidelity with NO session, logs
// share_opened, and counts the open on the record. Markdown stays on the
// escaped renderer (an injected <script> never survives raw), an html_deck
// ships verbatim under the sandboxed render CSP, and a pdf-typed artifact
// streams its newest pdf asset inline from the blob store.
func TestShareLinkPublicServesEachTypeAndLogsOpen(t *testing.T) {
	_, member := shareLinkTestEnv(t)

	markdown := seedShareArtifact(t, "approved", "# Aurora\n\n<script>alert(1)</script>\n\n- strong deal", nil)
	markdownLink := mintShareLinkForTest(t, markdown.ID, member)
	recorder := shareLinkRequest(t, http.MethodGet, fmt.Sprint(markdownLink["url"]), "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("markdown share status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if page := recorder.Body.String(); strings.Contains(page, "<script>alert(1)</script>") || !strings.Contains(page, "&lt;script&gt;") {
		t.Fatal("markdown share must escape artifact-derived HTML")
	}
	if shareOpenedSignalCount(t, markdown.ID) != 1 {
		t.Fatal("markdown open did not record a share_opened signal")
	}

	deckBody := "<!doctype html><html><body><h1>Aurora</h1><script>presenterMode()</script></body></html>"
	deck := seedShareArtifact(t, "approved", deckBody, map[string]string{"type": "html_deck"})
	deckLink := mintShareLinkForTest(t, deck.ID, member)
	recorder = shareLinkRequest(t, http.MethodGet, fmt.Sprint(deckLink["url"]), "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("deck share status=%d", recorder.Code)
	}
	if recorder.Body.String() != deckBody {
		t.Fatal("deck share must serve the artifact body verbatim (full fidelity)")
	}
	if got := recorder.Header().Get("Content-Security-Policy"); got != artifactRenderCSP {
		t.Fatalf("deck share CSP=%q, want the sandboxed render policy", got)
	}

	pdfBytes := []byte("%PDF-1.7 flattened aurora deck")
	ref, err := putBlob(pdfBytes, "application/pdf")
	if err != nil {
		t.Fatalf("putBlob: %v", err)
	}
	pdfArtifact := seedShareArtifact(t, "approved", "flattened deck export", map[string]string{"type": "pdf"})
	if _, err := kanbanApp.appendArtifactAsset(pdfArtifact.ID, artifactAsset{Ref: ref, Mime: "application/pdf", Name: "aurora.pdf", Kind: "pdf"}); err != nil {
		t.Fatalf("append pdf asset: %v", err)
	}
	pdfLink := mintShareLinkForTest(t, pdfArtifact.ID, member)
	recorder = shareLinkRequest(t, http.MethodGet, fmt.Sprint(pdfLink["url"]), "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("pdf share status=%d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/pdf" {
		t.Fatalf("pdf share content-type=%q", got)
	}
	if recorder.Body.String() != string(pdfBytes) {
		t.Fatal("pdf share must stream the exact blob bytes")
	}

	// The open stamped the record's counter for the mint UI.
	records, err := loadShareLinks()
	if err != nil {
		t.Fatalf("loadShareLinks: %v", err)
	}
	for _, record := range records {
		if record.ArtifactID == markdown.ID && record.OpenCount != 1 {
			t.Fatalf("markdown link openCount=%d, want 1", record.OpenCount)
		}
	}
}

// Expiry and revocation both kill the public route with the same 404, a
// non-creator non-admin cannot revoke, and pulling the artifact's approval
// revokes every live link instantly (the route re-checks the status gate).
func TestShareLinkExpiryRevocationAndApprovalPull(t *testing.T) {
	admin, member := shareLinkTestEnv(t)
	artifact := seedShareArtifact(t, "approved", "# Aurora\n\nready", nil)

	// Unknown token is a 404 page, not an error leak.
	if recorder := shareLinkRequest(t, http.MethodGet, "/a/not-a-real-token", "", nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown token status=%d, want 404", recorder.Code)
	}

	// Expiry: age the minted record in place, then the route refuses.
	expiredLink := mintShareLinkForTest(t, artifact.ID, member)
	records, err := loadShareLinks()
	if err != nil {
		t.Fatalf("loadShareLinks: %v", err)
	}
	for index := range records {
		records[index].ExpiresAt = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	}
	if err := saveShareLinks(records); err != nil {
		t.Fatalf("saveShareLinks: %v", err)
	}
	if recorder := shareLinkRequest(t, http.MethodGet, fmt.Sprint(expiredLink["url"]), "", nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("expired token status=%d, want 404", recorder.Code)
	}

	// Revocation: a stranger cannot, the creator can, and the token dies.
	link := mintShareLinkForTest(t, artifact.ID, member)
	if recorder := shareLinkRequest(t, http.MethodDelete, "/artifacts/share", fmt.Sprintf(`{"id":%q}`, link["id"]), admin); recorder.Code != http.StatusOK {
		// admin may revoke anything — this asserts the ADMIN path works…
		t.Fatalf("admin revoke status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := shareLinkRequest(t, http.MethodGet, fmt.Sprint(link["url"]), "", nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("revoked token status=%d, want 404", recorder.Code)
	}

	// …and the stranger path is forbidden: a member cannot revoke the
	// admin's own link.
	adminLink := mintShareLinkForTest(t, artifact.ID, admin)
	if recorder := shareLinkRequest(t, http.MethodDelete, "/artifacts/share", fmt.Sprintf(`{"id":%q}`, adminLink["id"]), member); recorder.Code != http.StatusForbidden {
		t.Fatalf("foreign revoke status=%d, want 403", recorder.Code)
	}

	// Approval pull: flip the artifact back to draft — the still-active link
	// must stop serving without touching the share records.
	liveLink := mintShareLinkForTest(t, artifact.ID, member)
	if _, _, err := kanbanApp.memory.updateOSArtifactMetadata(artifact.ID, map[string]string{"status": "draft"}); err != nil {
		t.Fatalf("pull approval: %v", err)
	}
	if recorder := shareLinkRequest(t, http.MethodGet, fmt.Sprint(liveLink["url"]), "", nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("approval-pulled token status=%d, want 404", recorder.Code)
	}

	// The list still shows history, url only on live links.
	listRecorder := shareLinkRequest(t, http.MethodGet, "/artifacts/share?artifactId="+artifact.ID, "", member)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status=%d", listRecorder.Code)
	}
	links, _ := decodeJSON(t, listRecorder)["links"].([]any)
	if len(links) != 4 {
		t.Fatalf("list returned %d links, want 4", len(links))
	}
	for _, item := range links {
		entry, _ := item.(map[string]any)
		if entry["status"] == shareLinkStatusRevoked && entry["url"] != nil {
			t.Fatal("revoked links must not carry a url")
		}
	}
}

// setupRenderSidecarEnv isolates the render queue + heartbeat for one test.
func setupRenderSidecarEnv(t *testing.T) (queueDir string) {
	t.Helper()
	dir := t.TempDir()
	queueDir = filepath.Join(dir, "render-jobs")
	t.Setenv("BONFIRE_RENDER_QUEUE_PATH", queueDir)
	t.Setenv("BONFIRE_RENDER_HEARTBEAT_PATH", filepath.Join(dir, "render-runner-heartbeat.json"))
	return queueDir
}

// The export trigger degrades exactly like codex sidecar absence: no (or
// stale) heartbeat is a 503 with a clear operator message and NO queued job;
// a live heartbeat enqueues the export_pdf job and stamps renderJobId.
func TestArtifactExportPDFSidecarAbsentThenPresent(t *testing.T) {
	_, member := shareLinkTestEnv(t)
	queueDir := setupRenderSidecarEnv(t)
	deck := seedShareArtifact(t, "draft", "<!doctype html><html><body>deck</body></html>", map[string]string{"type": "html_deck"})

	if recorder := shareLinkRequest(t, http.MethodPost, "/artifacts/export-pdf", fmt.Sprintf(`{"artifactId":%q}`, deck.ID), nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out export status=%d, want 401", recorder.Code)
	}

	recorder := shareLinkRequest(t, http.MethodPost, "/artifacts/export-pdf", fmt.Sprintf(`{"artifactId":%q}`, deck.ID), member)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("sidecar-absent export status=%d body=%s, want 503", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "render sidecar not available") {
		t.Fatalf("503 must carry the clear operator message, got %s", recorder.Body.String())
	}
	if entries, err := os.ReadDir(queueDir); err == nil && len(entries) != 0 {
		t.Fatal("sidecar-absent export must not enqueue a job")
	}

	// A markdown artifact exports too: the server converts the body into the
	// branded BonfireOS print document (renderResearchReportPrintHTML —
	// report_print_test.go pins the document itself) and routes it down the
	// text-native paper path.
	if err := writeRenderRunnerHeartbeat("test-runner"); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}
	markdown := seedShareArtifact(t, "draft", "# not a deck", nil)
	if recorder := shareLinkRequest(t, http.MethodPost, "/artifacts/export-pdf", fmt.Sprintf(`{"artifactId":%q}`, markdown.ID), member); recorder.Code != http.StatusAccepted {
		t.Fatalf("markdown export status=%d body=%s, want 202 (branded paper print)", recorder.Code, recorder.Body.String())
	} else if entry, _ := kanbanApp.osArtifactByID(markdown.ID); entry.Metadata["renderKind"] != renderJobKindPaper {
		t.Fatalf("markdown artifact enqueued as %q, want paper (text-native path)", entry.Metadata["renderKind"])
	}

	// The flatten law is server-owned: a deck exported as "paper" would ship
	// the layered print, so a kind that disagrees with the artifact's own
	// declaration is a 400 — never a silent rewrite, never honored.
	if recorder := shareLinkRequest(t, http.MethodPost, "/artifacts/export-pdf", fmt.Sprintf(`{"artifactId":%q,"kind":"paper"}`, deck.ID), member); recorder.Code != http.StatusBadRequest {
		t.Fatalf("paper-kind export of a deck status=%d, want 400 (flatten law)", recorder.Code)
	}
	paperKit := seedShareArtifact(t, "draft", "<!doctype html><html><body>the talk</body></html>", map[string]string{"type": "html_deck", "paperKit": "true"})
	if recorder := shareLinkRequest(t, http.MethodPost, "/artifacts/export-pdf", fmt.Sprintf(`{"artifactId":%q}`, paperKit.ID), member); recorder.Code != http.StatusAccepted {
		t.Fatalf("paper-kit export status=%d, want 202", recorder.Code)
	} else if kit, _ := kanbanApp.osArtifactByID(paperKit.ID); kit.Metadata["renderKind"] != renderJobKindPaper {
		t.Fatalf("paper-kit artifact enqueued as %q, want paper (text-native path)", kit.Metadata["renderKind"])
	}

	recorder = shareLinkRequest(t, http.MethodPost, "/artifacts/export-pdf", fmt.Sprintf(`{"artifactId":%q,"kind":"deck"}`, deck.ID), member)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("sidecar-present export status=%d body=%s, want 202", recorder.Code, recorder.Body.String())
	}
	payload := decodeJSON(t, recorder)
	jobID := strings.TrimSpace(fmt.Sprint(payload["jobId"]))
	if jobID == "" {
		t.Fatal("export response carried no jobId")
	}
	if _, err := os.Stat(filepath.Join(queueDir, jobID+".json")); err != nil {
		t.Fatalf("queued job file missing: %v", err)
	}
	updated, _ := kanbanApp.osArtifactByID(deck.ID)
	if updated.Metadata["renderJobId"] != jobID || updated.Metadata["renderStatus"] != renderJobStatusQueued {
		t.Fatalf("artifact metadata=%v, want renderJobId=%s renderStatus=queued", updated.Metadata, jobID)
	}
}

func renderCallbackRequest(t *testing.T, token string, payload renderRunnerCallbackPayload) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode callback: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/internal/render/jobs/result", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	internalRenderRunnerResultHandler(recorder, req)
	return recorder
}

// The render callback is Bearer-gated (the codex callback twin); a complete
// deck job stores the flattened PDF as a blob, appends the {kind: pdf} asset,
// stamps render metadata, and records the pdf_exported signal. A mismatched
// job id conflicts, and an unflattened deck violates the flatten law.
func TestInternalRenderCallbackAuthAssetAppendAndFlattenLaw(t *testing.T) {
	shareLinkTestEnv(t)
	setupRenderSidecarEnv(t)
	t.Setenv("BONFIRE_RUNNER_TOKEN", "render-secret")
	deck := seedShareArtifact(t, "draft", "<!doctype html><html><body>deck</body></html>", map[string]string{"type": "html_deck"})
	if _, _, err := kanbanApp.memory.updateOSArtifactMetadata(deck.ID, map[string]string{"renderJobId": "render-job-1"}); err != nil {
		t.Fatalf("stamp job id: %v", err)
	}
	pdfBytes := []byte("%PDF-1.7 flattened deck")
	complete := renderRunnerCallbackPayload{
		JobID:      "render-job-1",
		ArtifactID: deck.ID,
		Kind:       renderJobKindDeck,
		Status:     renderJobStatusComplete,
		PDFBase64:  base64.StdEncoding.EncodeToString(pdfBytes),
		PageCount:  12,
		Flattened:  true,
	}

	if recorder := renderCallbackRequest(t, "", complete); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("tokenless callback status=%d, want 401", recorder.Code)
	}
	if recorder := renderCallbackRequest(t, "wrong-secret", complete); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-token callback status=%d, want 401", recorder.Code)
	}

	mismatched := complete
	mismatched.JobID = "render-job-999"
	if recorder := renderCallbackRequest(t, "render-secret", mismatched); recorder.Code != http.StatusConflict {
		t.Fatalf("mismatched job callback status=%d, want 409", recorder.Code)
	}

	// The identity check is mandatory, never best-effort: an empty job_id is
	// a 400, and a callback for an artifact with NO pending renderJobId stamp
	// is a 409 — a hostile runner-token holder can never pick its own target.
	jobless := complete
	jobless.JobID = ""
	if recorder := renderCallbackRequest(t, "render-secret", jobless); recorder.Code != http.StatusBadRequest {
		t.Fatalf("empty job_id callback status=%d, want 400", recorder.Code)
	}
	unstamped := seedShareArtifact(t, "draft", "<!doctype html><html><body>other deck</body></html>", map[string]string{"type": "html_deck"})
	hijack := complete
	hijack.ArtifactID = unstamped.ID
	if recorder := renderCallbackRequest(t, "render-secret", hijack); recorder.Code != http.StatusConflict {
		t.Fatalf("no-pending-stamp callback status=%d, want 409", recorder.Code)
	}
	if hijacked, _ := kanbanApp.osArtifactByID(unstamped.ID); len(artifactAssets(hijacked)) != 0 || hijacked.Metadata["renderStatus"] != "" {
		t.Fatalf("rejected callback still mutated the artifact: %v", hijacked.Metadata)
	}

	unflattened := complete
	unflattened.Flattened = false
	if recorder := renderCallbackRequest(t, "render-secret", unflattened); recorder.Code != http.StatusBadRequest {
		t.Fatalf("unflattened deck callback status=%d, want 400 (flatten law)", recorder.Code)
	}

	// A running callback (before completion) only stamps status metadata — no
	// asset, no signal.
	running := renderRunnerCallbackPayload{JobID: "render-job-1", ArtifactID: deck.ID, Kind: renderJobKindDeck, Status: renderJobStatusRunning}
	if recorder := renderCallbackRequest(t, "render-secret", running); recorder.Code != http.StatusOK {
		t.Fatalf("running callback status=%d", recorder.Code)
	}
	if progressed, _ := kanbanApp.osArtifactByID(deck.ID); progressed.Metadata["renderStatus"] != renderJobStatusRunning || len(artifactAssets(progressed)) != 0 {
		t.Fatalf("running callback want status-only stamp, got metadata=%v assets=%v", progressed.Metadata, artifactAssets(progressed))
	}

	recorder := renderCallbackRequest(t, "render-secret", complete)
	if recorder.Code != http.StatusOK {
		t.Fatalf("complete callback status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	ref := strings.TrimSpace(fmt.Sprint(decodeJSON(t, recorder)["ref"]))
	stored, meta, err := getBlob(ref)
	if err != nil || string(stored) != string(pdfBytes) || meta.Mime != "application/pdf" {
		t.Fatalf("blob round-trip ref=%s err=%v mime=%s", ref, err, meta.Mime)
	}

	updated, _ := kanbanApp.osArtifactByID(deck.ID)
	assets := artifactAssets(updated)
	if len(assets) != 1 || assets[0].Ref != ref || assets[0].Kind != "pdf" || assets[0].Mime != "application/pdf" {
		t.Fatalf("assets=%v, want one pdf asset with ref %s", assets, ref)
	}
	if updated.Metadata["renderStatus"] != renderJobStatusComplete || updated.Metadata["renderPageCount"] != "12" {
		t.Fatalf("render metadata=%v, want complete + pageCount 12", updated.Metadata)
	}

	exported := 0
	for _, entry := range kanbanApp.memory.entriesOfKind(meetingMemoryKindSignal, 0) {
		if record, ok := decodeSignalEntry(entry); ok && record.Event == signalEventPDFExported && record.ArtifactID == deck.ID {
			exported++
		}
	}
	if exported != 1 {
		t.Fatalf("pdf_exported signals=%d, want 1", exported)
	}

	// A completed job is spent: the success path cleared renderJobId, so ANY
	// replay — the same complete callback or a late running scribble — is a
	// 409 and mutates nothing.
	if recorder := renderCallbackRequest(t, "render-secret", complete); recorder.Code != http.StatusConflict {
		t.Fatalf("replayed complete callback status=%d, want 409", recorder.Code)
	}
	if recorder := renderCallbackRequest(t, "render-secret", running); recorder.Code != http.StatusConflict {
		t.Fatalf("post-completion running callback status=%d, want 409", recorder.Code)
	}
	updated, _ = kanbanApp.osArtifactByID(deck.ID)
	if updated.Metadata["renderStatus"] != renderJobStatusComplete || len(artifactAssets(updated)) != 1 {
		t.Fatalf("replay mutated the artifact: metadata=%v assets=%d", updated.Metadata, len(artifactAssets(updated)))
	}
}
