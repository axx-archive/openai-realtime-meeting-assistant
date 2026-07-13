package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

type denyArtifactActionAuthorizer struct{ denied ACLAction }

func (authorizer denyArtifactActionAuthorizer) AuthorizeArtifactHeader(_ context.Context, _ *userAccount, action ACLAction, _ ArtifactAuthorizationHeader) bool {
	return action != authorizer.denied
}

// dealRoomTestEnv wires the auth env + an isolated app installed as the global
// kanbanApp (the handlers read the global), and returns admin + member cookies.
// admin is aj@shareability.com (isArtifactApprovalAdmin); member is not.
func dealRoomTestEnv(t *testing.T) (adminCookies, memberCookies []*http.Cookie) {
	t.Helper()
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	return loginAs(t, "aj@shareability.com", "B0NFIRE!"), loginAs(t, "tim@shareability.com", "B0NFIRE!")
}

func TestDealRoomCapabilityStoresOnlyHashAndRejectsEditedBoundGalleryArtifact(t *testing.T) {
	admin, member := dealRoomTestEnv(t)
	packageID, _ := seedPackageWithBinder(t, "# Bound cover\n\nImmutable at approval")
	deck := attachGalleryArtifact(t, packageID, "Bound gallery deck", "<!doctype html><html><body>approved gallery v1</body></html>", map[string]string{"type": "html_deck", "status": "approved"})
	url := approveDealRoomForTest(t, admin, member, packageID)
	rawToken := strings.TrimPrefix(url, "/deal-room/")
	stored, err := os.ReadFile(meetingMemoryPath())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte(rawToken)) || !bytes.Contains(stored, []byte(`tokenHash`)) {
		t.Fatalf("deal room persisted plaintext token or omitted hash")
	}
	page := dealRoomRequest(t, http.MethodGet, url, "", nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Bound gallery deck") {
		t.Fatalf("pre-edit page status=%d", page.Code)
	}
	renderHref := html.UnescapeString(regexp.MustCompile(`/artifacts/render\?id=[^"]+`).FindString(page.Body.String()))
	if _, _, err := kanbanApp.memory.updateOSArtifact(deck.ID, "", "<!doctype html><html><body>unapproved gallery v2</body></html>", "AJ"); err != nil {
		t.Fatal(err)
	}
	after := dealRoomRequest(t, http.MethodGet, url, "", nil)
	if after.Code != http.StatusOK || strings.Contains(after.Body.String(), "Bound gallery deck") || strings.Contains(after.Body.String(), "unapproved gallery v2") {
		t.Fatalf("edited gallery leaked under old capability: status=%d", after.Code)
	}
	render := httptest.NewRecorder()
	artifactRenderHandler(render, httptest.NewRequest(http.MethodGet, renderHref, nil))
	if render.Code != http.StatusNotFound || strings.Contains(render.Body.String(), "unapproved gallery v2") {
		t.Fatalf("old render capability served edit: status=%d body=%s", render.Code, render.Body.String())
	}
}

func TestLegacyPlaintextDealRoomCapabilityFailsClosed(t *testing.T) {
	dealRoomTestEnv(t)
	record := dealRoomRecord{ID: "legacy-room", PackageID: "package", ArtifactID: "artifact", Status: dealRoomStatusActive, Token: "legacy-room-secret"}
	if _, err := kanbanApp.persistDealRoom(record, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := kanbanApp.dealRoomByToken("legacy-room-secret"); ok {
		t.Fatal("legacy plaintext Deal Room token was accepted")
	}
}

func TestDealRoomPersistFailureDoesNotMutateGlobalArtifactApproval(t *testing.T) {
	admin, member := dealRoomTestEnv(t)
	packageID, artifactID := seedPackageWithBinder(t, "# Binder")
	request := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, packageID), member)
	id := fmt.Sprint(decodeJSON(t, request)["id"])
	kanbanApp.memory.path = t.TempDir() // rename target is a directory: deterministic persist failure
	resolve := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/resolve", fmt.Sprintf(`{"id":%q,"action":"approve"}`, id), admin)
	if resolve.Code == http.StatusOK {
		t.Fatalf("resolve unexpectedly succeeded: %s", resolve.Body.String())
	}
	artifact, _ := kanbanApp.osArtifactByID(artifactID)
	if artifactStatus(artifact) == artifactStatusApproved || artifact.Metadata[artifactHumanApprovedAtKey] != "" {
		t.Fatalf("failed Deal Room persist mutated approval: %+v", artifact.Metadata)
	}
}

func TestDealRoomApprovalReauthorizesApproveAction(t *testing.T) {
	admin, member := dealRoomTestEnv(t)
	packageID, _ := seedPackageWithBinder(t, "# Binder")
	request := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, packageID), member)
	id := fmt.Sprint(decodeJSON(t, request)["id"])
	prior := artifactObjectAuthorizer
	artifactObjectAuthorizer = denyArtifactActionAuthorizer{denied: ACLApprove}
	t.Cleanup(func() { artifactObjectAuthorizer = prior })
	resolve := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/resolve", fmt.Sprintf(`{"id":%q,"action":"approve"}`, id), admin)
	if resolve.Code == http.StatusOK {
		t.Fatalf("approve denial was ignored: %s", resolve.Body.String())
	}
	record, _ := kanbanApp.dealRoomByID(id)
	if record.Status != dealRoomStatusPending || record.TokenHash != "" {
		t.Fatalf("denied approval minted capability: %+v", record)
	}
}

func TestDealRoomGalleryAuthorizesBeforeBodyOrEligibilityRead(t *testing.T) {
	admin, member := dealRoomTestEnv(t)
	packageID, _ := seedPackageWithBinder(t, "# Binder")
	gallery := attachGalleryArtifact(t, packageID, "Private gallery", "secret gallery body", map[string]string{"status": artifactStatusApproved})
	request := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, packageID), member)
	id := fmt.Sprint(decodeJSON(t, request)["id"])
	priorAuthorizer, priorProbe := artifactObjectAuthorizer, artifactBodyReadProbe
	recorder := &shareRecordingAuthorizer{denyObject: gallery.ID, denyAction: ACLShare}
	readIDs := []string{}
	artifactObjectAuthorizer = recorder
	artifactBodyReadProbe = func(id string) { readIDs = append(readIDs, id) }
	t.Cleanup(func() { artifactObjectAuthorizer, artifactBodyReadProbe = priorAuthorizer, priorProbe })
	resolve := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/resolve", fmt.Sprintf(`{"id":%q,"action":"approve"}`, id), admin)
	if resolve.Code == http.StatusOK {
		t.Fatalf("gallery share denial ignored: %s", resolve.Body.String())
	}
	for _, readID := range readIDs {
		if readID == gallery.ID {
			t.Fatal("denied gallery body was read before authorization")
		}
	}
	record, _ := kanbanApp.dealRoomByID(id)
	if record.Status != dealRoomStatusPending || record.TokenHash != "" {
		t.Fatalf("denied gallery minted capability: %+v", record)
	}
}

func TestDealRoomPDFCapabilityNeverServesReplacementAsset(t *testing.T) {
	admin, member := dealRoomTestEnv(t)
	packageID, _ := seedPackageWithBinder(t, "# Cover")
	pdf := attachGalleryArtifact(t, packageID, "Bound PDF", "pdf", map[string]string{"type": "pdf", "status": artifactStatusDraft})
	refA, _ := putBlob([]byte("%PDF-A approved"), "application/pdf")
	if _, err := kanbanApp.appendArtifactAsset(pdf.ID, artifactAsset{Ref: refA, Mime: "application/pdf", Name: "a.pdf", Kind: "pdf"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := kanbanApp.memory.updateOSArtifactMetadata(pdf.ID, map[string]string{"status": artifactStatusApproved}); err != nil {
		t.Fatal(err)
	}
	url := approveDealRoomForTest(t, admin, member, packageID)
	refB, _ := putBlob([]byte("%PDF-B replacement secret"), "application/pdf")
	if _, err := kanbanApp.replaceArtifactAssetsOfKind(pdf.ID, "pdf", []artifactAsset{{Ref: refB, Mime: "application/pdf", Name: "b.pdf", Kind: "pdf"}}); err != nil {
		t.Fatal(err)
	}
	response := dealRoomRequest(t, http.MethodGet, url+"?artifact="+pdf.ID, "", nil)
	if response.Code != http.StatusNotFound || bytes.Contains(response.Body.Bytes(), []byte("replacement secret")) {
		t.Fatalf("old deal room served replacement PDF: status=%d body=%q", response.Code, response.Body.Bytes())
	}
}

func dealRoomRequest(t *testing.T, method, path, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	switch {
	case path == "/assistant/deal-room/request":
		assistantDealRoomRequestHandler(recorder, req)
	case path == "/assistant/deal-room/resolve":
		assistantDealRoomResolveHandler(recorder, req)
	case path == "/assistant/deal-room/revoke":
		assistantDealRoomRevokeHandler(recorder, req)
	case path == "/assistant/deal-room/list":
		assistantDealRoomListHandler(recorder, req)
	case strings.HasPrefix(path, "/deal-room/"):
		dealRoomPublicHandler(recorder, req)
	default:
		t.Fatalf("unknown deal room path %q", path)
	}
	return recorder
}

// seedPackageWithBinder creates a package and attaches a binder artifact whose
// body carries an injected <script> to prove the public renderer escapes it.
func seedPackageWithBinder(t *testing.T, body string) (packageID, artifactID string) {
	t.Helper()
	pkg, err := kanbanApp.createVenturePackage("Aurora", "an IP thesis", "AJ")
	if err != nil {
		t.Fatalf("create package: %v", err)
	}
	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Aurora binder", body, "AJ", map[string]string{
		"toolTemplate":     "package_assembly",
		"artifactContract": "package_binder_v1",
		"title":            "Aurora — assembled binder",
	})
	if err != nil {
		t.Fatalf("create binder artifact: %v", err)
	}
	if _, err := kanbanApp.attachToPackage(pkg.ID, "artifact", artifact.ID, "AJ"); err != nil {
		t.Fatalf("attach binder: %v", err)
	}
	return pkg.ID, artifact.ID
}

func decodeJSON(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode body %q: %v", recorder.Body.String(), err)
	}
	return payload
}

func TestDealRoomRequestLandsPending(t *testing.T) {
	_, member := dealRoomTestEnv(t)
	packageID, _ := seedPackageWithBinder(t, "# Aurora\n\nA strong deal.")

	recorder := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, packageID), member)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	payload := decodeJSON(t, recorder)
	if payload["status"] != dealRoomStatusPending {
		t.Fatalf("status=%v, want pending", payload["status"])
	}
	if strings.TrimSpace(fmt.Sprint(payload["id"])) == "" {
		t.Fatal("expected an id")
	}

	// Idempotent-ish: a second identical request returns the same pending room.
	second := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, packageID), member)
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	if decodeJSON(t, second)["id"] != payload["id"] {
		t.Fatal("second request must return the same pending room id")
	}
}

func TestDealRoomRequestWithoutBinderIs400(t *testing.T) {
	_, member := dealRoomTestEnv(t)
	pkg, err := kanbanApp.createVenturePackage("Empty", "no artifacts", "AJ")
	if err != nil {
		t.Fatalf("create package: %v", err)
	}

	recorder := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, pkg.ID), member)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(strings.ToLower(recorder.Body.String()), "binder") {
		t.Fatalf("expected a clear no-binder message, got %s", recorder.Body.String())
	}
}

// TestDealRoomRequestRejectsForeignArtifact proves the SECURITY membership gate:
// a client-supplied artifactId that exists but does NOT belong to the package is
// rejected, so an approved share can never publish an unrelated artifact behind
// a public token.
func TestDealRoomRequestRejectsForeignArtifact(t *testing.T) {
	_, member := dealRoomTestEnv(t)
	packageID, ownArtifactID := seedPackageWithBinder(t, "# Aurora\n\nA strong deal.")

	// A real artifact that exists but is attached to NO package (a private memo).
	foreign, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Secret memo", "# Confidential\n\nnot for sharing", "AJ", map[string]string{"title": "Secret memo"})
	if err != nil {
		t.Fatalf("create foreign artifact: %v", err)
	}

	// The foreign artifactId must be rejected as not part of the package.
	reject := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q,"artifactId":%q}`, packageID, foreign.ID), member)
	if reject.Code != http.StatusBadRequest {
		t.Fatalf("foreign artifact status=%d body=%s, want 400", reject.Code, reject.Body.String())
	}
	if !strings.Contains(strings.ToLower(reject.Body.String()), "not part of this package") {
		t.Fatalf("expected a membership-rejection message, got %s", reject.Body.String())
	}

	// The package's OWN artifactId is still accepted on the explicit path.
	accept := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q,"artifactId":%q}`, packageID, ownArtifactID), member)
	if accept.Code != http.StatusOK {
		t.Fatalf("own artifact status=%d body=%s, want 200", accept.Code, accept.Body.String())
	}
}

func TestDealRoomNonAdminResolveForbidden(t *testing.T) {
	_, member := dealRoomTestEnv(t)
	packageID, _ := seedPackageWithBinder(t, "# Aurora\n\nBody.")
	reqRec := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, packageID), member)
	id := fmt.Sprint(decodeJSON(t, reqRec)["id"])

	recorder := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/resolve", fmt.Sprintf(`{"id":%q,"action":"approve"}`, id), member)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s, want 403", recorder.Code, recorder.Body.String())
	}
}

func TestDealRoomApproveMintsTokenAndServesEscapedBinder(t *testing.T) {
	admin, member := dealRoomTestEnv(t)
	packageID, _ := seedPackageWithBinder(t, "# Aurora\n\nHello <script>alert('xss')</script> world\n\n- one item")
	reqRec := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, packageID), member)
	id := fmt.Sprint(decodeJSON(t, reqRec)["id"])

	// admin approve -> active + minted token
	resolveRec := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/resolve", fmt.Sprintf(`{"id":%q,"action":"approve"}`, id), admin)
	if resolveRec.Code != http.StatusOK {
		t.Fatalf("resolve status=%d body=%s", resolveRec.Code, resolveRec.Body.String())
	}
	room, ok := decodeJSON(t, resolveRec)["dealRoom"].(map[string]any)
	if !ok {
		t.Fatalf("no dealRoom payload: %s", resolveRec.Body.String())
	}
	if room["status"] != dealRoomStatusActive {
		t.Fatalf("status=%v, want active", room["status"])
	}
	if room["tokenHash"] != nil || room["snapshot"] != nil || room["boundArtifacts"] != nil {
		t.Fatalf("deal room payload exposed capability internals: %+v", room)
	}
	url, _ := room["url"].(string)
	if !strings.HasPrefix(url, "/deal-room/") || len(strings.TrimPrefix(url, "/deal-room/")) < 24 {
		t.Fatalf("url=%q, want a minted /deal-room/<token>", url)
	}

	// public GET serves the binder with the injected script escaped
	pageRec := dealRoomRequest(t, http.MethodGet, url, "", nil)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("public status=%d", pageRec.Code)
	}
	if ct := pageRec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type=%q", ct)
	}
	pageBody := pageRec.Body.String()
	if strings.Contains(pageBody, "<script>alert(") {
		t.Fatal("injected <script> must be escaped, not served raw")
	}
	if !strings.Contains(pageBody, "&lt;script&gt;") {
		t.Fatalf("expected escaped script marker in page: %s", pageBody)
	}
	if !strings.Contains(pageBody, "read-only") || !strings.Contains(pageBody, "Provenance") {
		t.Fatal("expected read-only provenance chrome")
	}
}

func TestDealRoomRevokeThen404(t *testing.T) {
	admin, member := dealRoomTestEnv(t)
	packageID, _ := seedPackageWithBinder(t, "# Aurora\n\nBody.")
	reqRec := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, packageID), member)
	id := fmt.Sprint(decodeJSON(t, reqRec)["id"])
	resolveRec := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/resolve", fmt.Sprintf(`{"id":%q,"action":"approve"}`, id), admin)
	room := decodeJSON(t, resolveRec)["dealRoom"].(map[string]any)
	url := room["url"].(string)

	// live before revoke
	if live := dealRoomRequest(t, http.MethodGet, url, "", nil); live.Code != http.StatusOK {
		t.Fatalf("expected 200 before revoke, got %d", live.Code)
	}

	if rev := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/revoke", fmt.Sprintf(`{"id":%q}`, id), admin); rev.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", rev.Code, rev.Body.String())
	}

	if after := dealRoomRequest(t, http.MethodGet, url, "", nil); after.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after revoke, got %d", after.Code)
	}
}

func TestDealRoomListScoping(t *testing.T) {
	admin, member := dealRoomTestEnv(t)
	packageID, _ := seedPackageWithBinder(t, "# Aurora\n\nBody.")
	// member's own request
	dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, packageID), member)

	// member sees their own room, no canApprove
	memberList := dealRoomRequest(t, http.MethodGet, "/assistant/deal-room/list", "", member)
	memberPayload := decodeJSON(t, memberList)
	if memberPayload["canApprove"] != false {
		t.Fatalf("member canApprove=%v, want false", memberPayload["canApprove"])
	}
	memberRooms, _ := memberPayload["rooms"].([]any)
	if len(memberRooms) != 1 {
		t.Fatalf("member rooms=%d, want 1", len(memberRooms))
	}

	// admin sees all rooms and canApprove true
	adminList := dealRoomRequest(t, http.MethodGet, "/assistant/deal-room/list", "", admin)
	adminPayload := decodeJSON(t, adminList)
	if adminPayload["canApprove"] != true {
		t.Fatalf("admin canApprove=%v, want true", adminPayload["canApprove"])
	}
	adminRooms, _ := adminPayload["rooms"].([]any)
	if len(adminRooms) != 1 {
		t.Fatalf("admin rooms=%d, want 1", len(adminRooms))
	}
}

// TestDealRoomListNoCrossUserLeak proves the existence-leak concern from the
// showcase is closed: a non-admin's /list payload contains ONLY their own
// requests — never another member's room, not even its id.
func TestDealRoomListNoCrossUserLeak(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	tim := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	caitlyn := loginAs(t, "caitlyn@shareability.com", "B0NFIRE!")

	packageID, _ := seedPackageWithBinder(t, "# Aurora\n\nBody.")

	// caitlyn opens a request; tim (a different non-admin) must never see it.
	caitRec := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, packageID), caitlyn)
	if caitRec.Code != http.StatusOK {
		t.Fatalf("caitlyn request status=%d body=%s", caitRec.Code, caitRec.Body.String())
	}
	caitRoomID := fmt.Sprint(decodeJSON(t, caitRec)["id"])

	timList := dealRoomRequest(t, http.MethodGet, "/assistant/deal-room/list", "", tim)
	timPayload := decodeJSON(t, timList)
	timRooms, _ := timPayload["rooms"].([]any)
	if len(timRooms) != 0 {
		t.Fatalf("tim sees %d rooms, want 0 (no cross-user leak)", len(timRooms))
	}
	// Belt-and-braces: caitlyn's room id must not appear anywhere in tim's body.
	if strings.Contains(timList.Body.String(), caitRoomID) {
		t.Fatalf("tim's /list leaked caitlyn's room id %q: %s", caitRoomID, timList.Body.String())
	}
}

/* ---------- gallery (spec §4, Wave 4 item 19) ---------- */

// approveDealRoomForTest requests + approves a room for the package and
// returns the public /deal-room/<token> url.
func approveDealRoomForTest(t *testing.T, admin, member []*http.Cookie, packageID string) string {
	t.Helper()
	reqRec := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/request", fmt.Sprintf(`{"packageId":%q}`, packageID), member)
	if reqRec.Code != http.StatusOK {
		t.Fatalf("request status=%d body=%s", reqRec.Code, reqRec.Body.String())
	}
	id := fmt.Sprint(decodeJSON(t, reqRec)["id"])
	resolveRec := dealRoomRequest(t, http.MethodPost, "/assistant/deal-room/resolve", fmt.Sprintf(`{"id":%q,"action":"approve"}`, id), admin)
	if resolveRec.Code != http.StatusOK {
		t.Fatalf("resolve status=%d body=%s", resolveRec.Code, resolveRec.Body.String())
	}
	room, _ := decodeJSON(t, resolveRec)["dealRoom"].(map[string]any)
	url, _ := room["url"].(string)
	if !strings.HasPrefix(url, "/deal-room/") {
		t.Fatalf("no minted url in %v", room)
	}
	return url
}

// attachGalleryArtifact creates an artifact with the given metadata and
// attaches it to the package.
func attachGalleryArtifact(t *testing.T, packageID, title, body string, extra map[string]string) meetingMemoryEntry {
	t.Helper()
	metadata := map[string]string{"title": title}
	for key, value := range extra {
		metadata[key] = value
	}
	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("research", title, body, "AJ", metadata)
	if err != nil {
		t.Fatalf("create gallery artifact %q: %v", title, err)
	}
	if _, err := kanbanApp.attachToPackage(packageID, "artifact", artifact.ID, "AJ"); err != nil {
		t.Fatalf("attach gallery artifact %q: %v", title, err)
	}
	return artifact
}

// seedGalleryPDF stores pdf bytes as a blob and stamps them as the artifact's
// pdf asset (the render-callback shape).
func seedGalleryPDF(t *testing.T, artifactID string, pdfBytes []byte) {
	t.Helper()
	before, _ := kanbanApp.osArtifactByID(artifactID)
	wasApproved := artifactShareEligible(before)
	ref, err := putBlob(pdfBytes, "application/pdf")
	if err != nil {
		t.Fatalf("putBlob: %v", err)
	}
	if _, err := kanbanApp.appendArtifactAsset(artifactID, artifactAsset{Ref: ref, Mime: "application/pdf", Name: "aurora.pdf", Kind: "pdf"}); err != nil {
		t.Fatalf("append pdf asset: %v", err)
	}
	if wasApproved {
		if _, _, err := kanbanApp.memory.updateOSArtifactMetadata(artifactID, map[string]string{"status": artifactStatusApproved}); err != nil {
			t.Fatal(err)
		}
	}
}

func dealRoomOpenSignalCount(t *testing.T, artifactID string) int {
	t.Helper()
	count := 0
	for _, entry := range kanbanApp.memory.entriesOfKind(meetingMemoryKindSignal, 0) {
		record, ok := decodeSignalEntry(entry)
		if ok && record.Event == signalEventDealRoomArtifactOpened && record.ArtifactID == artifactID {
			count++
		}
	}
	return count
}

// The gallery renders ONLY final/approved artifacts below the (still escaped,
// unchanged) binder cover: title + type badge + version + gateOutcome/rubric
// score, an html_deck linking to the sandboxed render route with a page-build
// render token that actually authorizes the render, and a pdf linking to the
// deal-room-scoped serve — never the session-gated blob route. Draft work
// never appears.
func TestDealRoomGalleryRendersOnlyApprovedArtifacts(t *testing.T) {
	admin, member := dealRoomTestEnv(t)
	packageID, _ := seedPackageWithBinder(t, "# Aurora\n\nHello <script>alert('xss')</script> world")

	deckBody := "<!doctype html><html><body><h1>Aurora deck</h1></body></html>"
	deck := attachGalleryArtifact(t, packageID, "Aurora deck", deckBody, map[string]string{
		"type":        "html_deck",
		"status":      "approved",
		"gateOutcome": "passed",
		"goalPlan":    `{"state":"done","subtasks":[{"id":"s1","review":{"verdict":"pass","score":9.2}}]}`,
	})
	attachGalleryArtifact(t, packageID, "Unfinished memo", "# not ready", map[string]string{"status": "draft"})
	pdfArtifact := attachGalleryArtifact(t, packageID, "Aurora diligence pdf", "flattened export", map[string]string{"type": "pdf", "status": "approved"})
	seedGalleryPDF(t, pdfArtifact.ID, []byte("%PDF-1.7 aurora flattened"))

	url := approveDealRoomForTest(t, admin, member, packageID)
	pageRec := dealRoomRequest(t, http.MethodGet, url, "", nil)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("page status=%d", pageRec.Code)
	}
	page := pageRec.Body.String()

	// The escaped cover is unchanged: injected script never survives raw.
	if strings.Contains(page, "<script>alert(") || !strings.Contains(page, "&lt;script&gt;") {
		t.Fatal("binder cover must stay on the escaped renderer")
	}

	// Approved entries with badge, version, and gate outcome + rubric score.
	if !strings.Contains(page, "Aurora deck") || !strings.Contains(page, "Aurora diligence pdf") {
		t.Fatalf("gallery is missing approved artifacts: %s", page)
	}
	if !strings.Contains(page, ">deck</span>") || !strings.Contains(page, ">pdf</span>") {
		t.Fatalf("gallery is missing type badges: %s", page)
	}
	if !strings.Contains(page, ">v1</span>") {
		t.Fatalf("gallery is missing the version stamp: %s", page)
	}
	if !strings.Contains(page, "passed · 9.2") {
		t.Fatalf("gallery is missing gateOutcome + rubric score: %s", page)
	}
	if strings.Contains(page, "Unfinished memo") {
		t.Fatal("a draft artifact must never appear in the gallery")
	}

	// The deck href is the sandboxed render route with a token minted at page
	// build — and that token authorizes the render for real.
	renderHref := regexp.MustCompile(`/artifacts/render\?id=[^"]+`).FindString(page)
	if renderHref == "" || !strings.Contains(renderHref, deck.ID) {
		t.Fatalf("no render link for the deck in page: %s", page)
	}
	renderReq := httptest.NewRequest(http.MethodGet, html.UnescapeString(renderHref), nil)
	renderRec := httptest.NewRecorder()
	artifactRenderHandler(renderRec, renderReq)
	if renderRec.Code != http.StatusOK || renderRec.Body.String() != deckBody {
		t.Fatalf("page-build render token did not authorize the deck: status=%d", renderRec.Code)
	}

	// The pdf href is the deal-room-scoped serve; the session-gated blob route
	// is never handed to visitors.
	if !strings.Contains(page, html.EscapeString(url+"?artifact="+pdfArtifact.ID)) {
		t.Fatalf("no deal-room-scoped pdf link in page: %s", page)
	}
	if strings.Contains(page, "/artifacts/blob") {
		t.Fatal("gallery must never link the session-gated blob route")
	}
}

// The pdf serve is scoped STRICTLY to the package the token grants: an
// approved pdf attached to a different package 404s, an unattached one 404s,
// a draft one attached to the right package 404s (approval re-checked per
// open), a bogus token 404s — and the package's own approved pdf streams the
// exact blob bytes.
func TestDealRoomGalleryPDFScopedToPackage(t *testing.T) {
	admin, member := dealRoomTestEnv(t)
	packageID, _ := seedPackageWithBinder(t, "# Aurora\n\nBody.")
	pdfBytes := []byte("%PDF-1.7 aurora flattened")

	ownPDF := attachGalleryArtifact(t, packageID, "Own pdf", "export", map[string]string{"type": "pdf", "status": "approved"})
	seedGalleryPDF(t, ownPDF.ID, pdfBytes)

	otherPkg, err := kanbanApp.createVenturePackage("Borealis", "another thesis", "AJ")
	if err != nil {
		t.Fatalf("create other package: %v", err)
	}
	foreignPDF := attachGalleryArtifact(t, otherPkg.ID, "Foreign pdf", "export", map[string]string{"type": "pdf", "status": "approved"})
	seedGalleryPDF(t, foreignPDF.ID, pdfBytes)

	unattachedPDF, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Unattached pdf", "export", "AJ", map[string]string{"title": "Unattached pdf", "type": "pdf", "status": "approved"})
	if err != nil {
		t.Fatalf("create unattached pdf: %v", err)
	}
	seedGalleryPDF(t, unattachedPDF.ID, pdfBytes)

	draftPDF := attachGalleryArtifact(t, packageID, "Draft pdf", "export", map[string]string{"type": "pdf", "status": "draft"})
	seedGalleryPDF(t, draftPDF.ID, pdfBytes)

	url := approveDealRoomForTest(t, admin, member, packageID)

	own := dealRoomRequest(t, http.MethodGet, url+"?artifact="+ownPDF.ID, "", nil)
	if own.Code != http.StatusOK {
		t.Fatalf("own pdf status=%d, want 200", own.Code)
	}
	if got := own.Header().Get("Content-Type"); got != "application/pdf" {
		t.Fatalf("own pdf content-type=%q", got)
	}
	if own.Body.String() != string(pdfBytes) {
		t.Fatal("own pdf must stream the exact blob bytes")
	}

	for name, artifactID := range map[string]string{
		"foreign-package pdf": foreignPDF.ID,
		"unattached pdf":      unattachedPDF.ID,
		"draft attached pdf":  draftPDF.ID,
	} {
		if recorder := dealRoomRequest(t, http.MethodGet, url+"?artifact="+artifactID, "", nil); recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d, want 404 (blob access must never widen beyond the package)", name, recorder.Code)
		}
	}

	// A bad token never reaches the serve, even for an in-scope artifact.
	if recorder := dealRoomRequest(t, http.MethodGet, "/deal-room/not-a-real-token?artifact="+ownPDF.ID, "", nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("bogus token pdf status=%d, want 404", recorder.Code)
	}
}

// Gallery opens record the §5 deal_room_artifact_opened signal, debounced to
// at most one per (room, artifact) per hour — a crawler on this public route
// must never grow the JSONL store per hit.
func TestDealRoomOpenSignalsDebounced(t *testing.T) {
	admin, member := dealRoomTestEnv(t)
	packageID, binderID := seedPackageWithBinder(t, "# Aurora\n\nBody.")
	pdfArtifact := attachGalleryArtifact(t, packageID, "Aurora diligence pdf", "flattened export", map[string]string{"type": "pdf", "status": "approved"})
	seedGalleryPDF(t, pdfArtifact.ID, []byte("%PDF-1.7 aurora flattened"))

	url := approveDealRoomForTest(t, admin, member, packageID)

	// Two page opens -> ONE signal against the binder cover.
	for range 2 {
		if recorder := dealRoomRequest(t, http.MethodGet, url, "", nil); recorder.Code != http.StatusOK {
			t.Fatalf("page open status=%d", recorder.Code)
		}
	}
	if count := dealRoomOpenSignalCount(t, binderID); count != 1 {
		t.Fatalf("binder open signals=%d, want 1 (debounced)", count)
	}

	// Two pdf opens -> ONE signal against the pdf artifact.
	for range 2 {
		if recorder := dealRoomRequest(t, http.MethodGet, url+"?artifact="+pdfArtifact.ID, "", nil); recorder.Code != http.StatusOK {
			t.Fatalf("pdf open status=%d", recorder.Code)
		}
	}
	if count := dealRoomOpenSignalCount(t, pdfArtifact.ID); count != 1 {
		t.Fatalf("pdf open signals=%d, want 1 (debounced)", count)
	}

	// The debounce stamps landed on the room record itself.
	var room dealRoomRecord
	for _, record := range kanbanApp.dealRoomsSnapshot() {
		if record.PackageID == packageID {
			room = record
			break
		}
	}
	if room.ArtifactOpens[binderID] == "" || room.ArtifactOpens[pdfArtifact.ID] == "" {
		t.Fatalf("artifactOpens stamps missing: %v", room.ArtifactOpens)
	}
}

func TestDealRoomUnknownToken404(t *testing.T) {
	dealRoomTestEnv(t)
	recorder := dealRoomRequest(t, http.MethodGet, "/deal-room/this-token-does-not-exist", "", nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", recorder.Code)
	}
	if ct := recorder.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type=%q, want html 404 page", ct)
	}
}
