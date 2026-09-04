package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupIsolatedBlobStore points the blob store (which rides the
// meeting-memory directory) at a temp dir, without booting a full app.
func setupIsolatedBlobStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	return dir
}

// The store's core contract: put → get round-trips the exact bytes, the ref
// is the content digest (same bytes → same ref, no rewrite), the layout is
// sharded by the first two hex chars with a .meta sidecar, and the FIRST
// write pins the mime — a re-put with a different declared mime changes
// nothing.
func TestPutBlobGetBlobRoundTripDedupeAndMimePinning(t *testing.T) {
	dir := setupIsolatedBlobStore(t)

	deckBytes := []byte("%PDF-1.7 flattened deck bytes")
	ref, err := putBlob(deckBytes, "application/pdf")
	if err != nil {
		t.Fatalf("putBlob: %v", err)
	}
	if !validBlobRef(ref) {
		t.Fatalf("ref=%q, want a 64-char lowercase hex sha256", ref)
	}

	dataPath := filepath.Join(dir, "blobs", ref[:2], ref)
	if _, err := os.Stat(dataPath); err != nil {
		t.Fatalf("blob data file missing at sharded path: %v", err)
	}
	rawMeta, err := os.ReadFile(dataPath + blobMetaSuffix)
	if err != nil {
		t.Fatalf("blob meta sidecar missing: %v", err)
	}
	var meta blobMeta
	if err := json.Unmarshal(rawMeta, &meta); err != nil {
		t.Fatalf("decode meta sidecar: %v", err)
	}
	if meta.Mime != "application/pdf" || meta.Size != int64(len(deckBytes)) {
		t.Fatalf("meta=%+v, want mime application/pdf size %d", meta, len(deckBytes))
	}
	if _, err := time.Parse(time.RFC3339Nano, meta.CreatedAt); err != nil {
		t.Fatalf("meta createdAt=%q is not RFC3339Nano: %v", meta.CreatedAt, err)
	}

	got, gotMeta, err := getBlob(ref)
	if err != nil {
		t.Fatalf("getBlob: %v", err)
	}
	if !bytes.Equal(got, deckBytes) {
		t.Fatalf("getBlob bytes=%q, want the stored bytes verbatim", got)
	}
	if gotMeta.Mime != "application/pdf" || gotMeta.Size != int64(len(deckBytes)) {
		t.Fatalf("getBlob meta=%+v, want the sidecar values", gotMeta)
	}

	// Dedupe: same bytes with a DIFFERENT declared mime → same ref, and the
	// first write's mime stays pinned.
	dupRef, err := putBlob(deckBytes, "text/plain")
	if err != nil {
		t.Fatalf("dedupe putBlob: %v", err)
	}
	if dupRef != ref {
		t.Fatalf("dedupe ref=%q, want %q (same bytes must address the same blob)", dupRef, ref)
	}
	if _, pinned, err := getBlob(ref); err != nil || pinned.Mime != "application/pdf" {
		t.Fatalf("mime after re-put=%q err=%v, want the pinned application/pdf", pinned.Mime, err)
	}

	// Different bytes → different ref.
	otherRef, err := putBlob([]byte("a page raster"), "image/jpeg")
	if err != nil {
		t.Fatalf("second putBlob: %v", err)
	}
	if otherRef == ref {
		t.Fatalf("distinct bytes produced the same ref %q", ref)
	}
}

func TestPutBlobRepairsEveryIncompletePublicationCrashPoint(t *testing.T) {
	dir := setupIsolatedBlobStore(t)
	data := []byte("recoverable blob payload")
	digest := sha256.Sum256(data)
	ref := hex.EncodeToString(digest[:])
	dataPath := filepath.Join(dir, "blobs", ref[:2], ref)
	if err := os.MkdirAll(filepath.Dir(dataPath), 0o755); err != nil {
		t.Fatal(err)
	}

	// Crash after data publication but before sidecar: bytes are not served,
	// and retry repairs the publication marker.
	if err := os.WriteFile(dataPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := getBlob(ref); err == nil {
		t.Fatal("data-only blob was retrievable")
	}
	if got, err := putBlob(data, "application/pdf"); err != nil || got != ref {
		t.Fatalf("repair data-only put ref=%q err=%v", got, err)
	}

	// A malformed sidecar is likewise unpublished and deterministically
	// repaired without changing the content address.
	if err := os.WriteFile(dataPath+blobMetaSuffix, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := getBlob(ref); err == nil {
		t.Fatal("malformed-sidecar blob was retrievable")
	}
	if got, err := putBlob(data, "application/pdf"); err != nil || got != ref {
		t.Fatalf("repair malformed meta ref=%q err=%v", got, err)
	}

	// Corrupt bytes occupying the correct hash path are repaired from the
	// caller-supplied bytes before the sidecar is accepted.
	if err := os.WriteFile(dataPath, []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := putBlob(data, "application/pdf"); err != nil || got != ref {
		t.Fatalf("repair corrupt data ref=%q err=%v", got, err)
	}
	got, _, err := getBlob(ref)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("repaired blob bytes=%q err=%v", got, err)
	}
}

// The 64MB cap is inclusive: exactly-at-cap stores, one byte over and empty
// payloads are rejected before any disk write.
func TestPutBlobSizeCapAndEmpty(t *testing.T) {
	dir := setupIsolatedBlobStore(t)

	if _, err := putBlob(nil, "application/pdf"); err == nil {
		t.Fatal("putBlob(nil) succeeded, want an error for an empty blob")
	}
	if _, err := putBlob([]byte{}, "application/pdf"); err == nil {
		t.Fatal("putBlob(empty) succeeded, want an error for an empty blob")
	}

	oversized := make([]byte, blobMaxBytes+1)
	if _, err := putBlob(oversized, "application/pdf"); err == nil {
		t.Fatal("putBlob over the 64MB cap succeeded, want an error")
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "blobs")); err == nil && len(entries) != 0 {
		t.Fatalf("rejected put left %d entries in the store", len(entries))
	}

	atCap := make([]byte, blobMaxBytes)
	if _, err := putBlob(atCap, "application/pdf"); err != nil {
		t.Fatalf("putBlob at exactly the cap: %v", err)
	}
}

// getBlob rejects malformed refs before building any path, misses cleanly,
// and refuses to serve bytes that no longer match their digest.
func TestGetBlobRejectsInvalidMissingAndCorruptRefs(t *testing.T) {
	dir := setupIsolatedBlobStore(t)

	for _, ref := range []string{
		"",
		"abc",
		strings.Repeat("a", 63),
		strings.Repeat("A", 64), // uppercase hex is not a canonical ref
		strings.Repeat("z", 64),
		"../../etc/passwd",
	} {
		if _, _, err := getBlob(ref); err == nil {
			t.Fatalf("getBlob(%q) succeeded, want invalid-ref error", ref)
		}
	}

	if _, _, err := getBlob(strings.Repeat("0", 64)); err == nil {
		t.Fatal("getBlob of a never-stored ref succeeded, want not-found error")
	}

	// Corruption: flip the stored bytes on disk; the content-addressed read
	// must fail verification rather than serve wrong bytes.
	ref, err := putBlob([]byte("original deck bytes"), "application/pdf")
	if err != nil {
		t.Fatalf("putBlob: %v", err)
	}
	dataPath := filepath.Join(dir, "blobs", ref[:2], ref)
	if err := os.WriteFile(dataPath, []byte("tampered bytes"), 0o644); err != nil {
		t.Fatalf("corrupt blob file: %v", err)
	}
	if _, _, err := getBlob(ref); err == nil {
		t.Fatal("getBlob of a corrupted blob succeeded, want a verification error")
	}
}

// The assets metadata contract: artifactAssets round-trips what
// appendArtifactAsset wrote, re-attaching a ref replaces in place (idempotent
// re-exports never stack), the artifact body is untouched (metadata-only
// stamp), and garbage kinds/refs are rejected.
func TestArtifactAssetsAppendAndRead(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	artifact, _, err := app.createOSArtifact("design", "investor deck", "# Deck body", "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}
	if assets := artifactAssets(artifact); len(assets) != 0 {
		t.Fatalf("new artifact assets=%v, want none", assets)
	}

	pdfRef, err := putBlob([]byte("%PDF-1.7 export"), "application/pdf")
	if err != nil {
		t.Fatalf("putBlob pdf: %v", err)
	}
	imageRef, err := putBlob([]byte("jpeg page raster"), "image/jpeg")
	if err != nil {
		t.Fatalf("putBlob image: %v", err)
	}

	updated, err := app.appendArtifactAsset(artifact.ID, artifactAsset{Ref: pdfRef, Mime: "application/pdf", Name: "deck.pdf", Kind: "pdf"})
	if err != nil {
		t.Fatalf("appendArtifactAsset: %v", err)
	}
	if updated.Text != "# Deck body" {
		t.Fatalf("artifact text=%q after asset append, want the body untouched", updated.Text)
	}
	assets := artifactAssets(updated)
	if len(assets) != 1 || assets[0].Ref != pdfRef || assets[0].Mime != "application/pdf" || assets[0].Name != "deck.pdf" || assets[0].Kind != "pdf" {
		t.Fatalf("assets=%+v, want the appended pdf asset", assets)
	}

	updated, err = app.appendArtifactAsset(artifact.ID, artifactAsset{Ref: imageRef, Mime: "image/jpeg", Name: "page-01.jpg", Kind: "image"})
	if err != nil {
		t.Fatalf("append second asset: %v", err)
	}
	assets = artifactAssets(updated)
	if len(assets) != 2 || assets[0].Ref != pdfRef || assets[1].Ref != imageRef {
		t.Fatalf("assets=%+v, want [pdf, image] in append order", assets)
	}

	// Re-attaching the same ref replaces that entry in place.
	updated, err = app.appendArtifactAsset(artifact.ID, artifactAsset{Ref: pdfRef, Mime: "application/pdf", Name: "deck-v2.pdf", Kind: "export"})
	if err != nil {
		t.Fatalf("re-append same ref: %v", err)
	}
	assets = artifactAssets(updated)
	if len(assets) != 2 || assets[0].Name != "deck-v2.pdf" || assets[0].Kind != "export" || assets[1].Ref != imageRef {
		t.Fatalf("assets after re-append=%+v, want in-place replacement, no duplicate", assets)
	}

	if _, err := app.appendArtifactAsset(artifact.ID, artifactAsset{Ref: pdfRef, Kind: "weird"}); err == nil {
		t.Fatal("append with an unknown kind succeeded, want an error")
	}
	if _, err := app.appendArtifactAsset(artifact.ID, artifactAsset{Ref: "not-a-ref"}); err == nil {
		t.Fatal("append with an invalid ref succeeded, want an error")
	}
	if _, err := app.appendArtifactAsset("os-artifact-missing", artifactAsset{Ref: pdfRef}); err == nil {
		t.Fatal("append onto a missing artifact succeeded, want an error")
	}

	// Malformed assets JSON degrades to no assets, never a panic.
	if got := artifactAssets(meetingMemoryEntry{ID: "x", Metadata: map[string]string{"assets": "{not json"}}); got != nil {
		t.Fatalf("malformed assets metadata returned %v, want nil", got)
	}
}

// GC deletes exactly the orphans: a blob referenced by any artifact asset
// survives, its sidecar survives, and a sweep without a live artifact store
// refuses to run (sweeping blind would orphan-classify everything).
func TestSweepUnreferencedBlobsDeletesOnlyOrphans(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	keptRef, err := putBlob([]byte("referenced deck export"), "application/pdf")
	if err != nil {
		t.Fatalf("putBlob kept: %v", err)
	}
	orphanRef, err := putBlob([]byte("abandoned intermediate raster"), "image/jpeg")
	if err != nil {
		t.Fatalf("putBlob orphan: %v", err)
	}

	artifact, _, err := app.createOSArtifact("design", "investor deck", "# Deck", "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}
	if _, err := app.appendArtifactAsset(artifact.ID, artifactAsset{Ref: keptRef, Mime: "application/pdf", Kind: "pdf"}); err != nil {
		t.Fatalf("appendArtifactAsset: %v", err)
	}

	deleted, err := sweepUnreferencedBlobs(app)
	if err != nil {
		t.Fatalf("sweepUnreferencedBlobs: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != orphanRef {
		t.Fatalf("deleted=%v, want exactly the orphan %q", deleted, orphanRef)
	}
	if _, _, err := getBlob(orphanRef); err == nil {
		t.Fatal("orphan blob still readable after sweep")
	}
	orphanPath := filepath.Join(blobStoreDir(), orphanRef[:2], orphanRef)
	if _, err := os.Stat(orphanPath + blobMetaSuffix); !os.IsNotExist(err) {
		t.Fatalf("orphan meta sidecar still present after sweep: %v", err)
	}
	if _, _, err := getBlob(keptRef); err != nil {
		t.Fatalf("referenced blob unreadable after sweep: %v", err)
	}

	// Second sweep is a no-op.
	if deleted, err := sweepUnreferencedBlobs(app); err != nil || len(deleted) != 0 {
		t.Fatalf("second sweep deleted=%v err=%v, want a no-op", deleted, err)
	}

	// No live store → refuse, never delete.
	if _, err := sweepUnreferencedBlobs(nil); err == nil {
		t.Fatal("sweep without an artifact store succeeded, want an error")
	}
	if _, _, err := getBlob(keptRef); err != nil {
		t.Fatalf("blob store touched by the refused sweep: %v", err)
	}
}

func TestSweepUnreferencedBlobsKeepsCompletedChatImages(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	ref, err := putBlob([]byte("chat image without artifact asset fallback"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{
		ID: "chat-image-gc-reference", Kind: scoutChatMessageKindImage, Role: "scout", Text: "render",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Image:     &scoutChatImageRef{Ref: ref, Mime: "image/png", Prompt: "a retained render"},
	}
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", thread.ID, message); err != nil {
		t.Fatal(err)
	}
	deleted, err := sweepUnreferencedBlobs(app)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range deleted {
		if candidate == ref {
			t.Fatal("blob sweep deleted a completed chat image still referenced by its message")
		}
	}
}

// The version-body seam is wired IN PRODUCTION (blobs.go init), not just in
// tests: a body edit journals the superseded body as a recoverable blob, and
// the GC treats version-body refs as referenced — an edit history never
// evaporates on sweep.
func TestArtifactVersionBodySeamWiredAndSweepSafe(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if artifactVersionBlobStore == nil {
		t.Fatal("artifactVersionBlobStore is nil — blobs.go must wire the seam at init")
	}

	artifact, _, err := app.createOSArtifact("design", "investor deck", "# Deck body v1", "AJ")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}
	edited, _, err := app.memory.updateOSArtifact(artifact.ID, "", "# Deck body v2", "AJ")
	if err != nil {
		t.Fatalf("updateOSArtifact: %v", err)
	}
	history := artifactVersionHistory(edited)
	if len(history) != 1 || !validBlobRef(history[0].BodyBlobRef) {
		t.Fatalf("history=%+v, want one record with a real body blob ref", history)
	}
	prior, meta, err := getBlob(history[0].BodyBlobRef)
	if err != nil || string(prior) != "# Deck body v1" {
		t.Fatalf("version body blob err=%v body=%q, want the superseded v1 body", err, prior)
	}
	if !strings.HasPrefix(meta.Mime, "text/markdown") {
		t.Fatalf("version body mime=%q, want text/markdown", meta.Mime)
	}

	// GC must not orphan-classify the version body.
	deleted, err := sweepUnreferencedBlobs(app)
	if err != nil {
		t.Fatalf("sweepUnreferencedBlobs: %v", err)
	}
	for _, ref := range deleted {
		if ref == history[0].BodyBlobRef {
			t.Fatal("sweep deleted a version-body blob still referenced by the lineage journal")
		}
	}
	if _, _, err := getBlob(history[0].BodyBlobRef); err != nil {
		t.Fatalf("version body unreadable after sweep: %v", err)
	}
}

// The blob route's contract: session-gated like its /artifacts neighbors
// (401 signed out), 400 on a malformed ref, 404 on a miss, and the 200
// carries the pinned mime, ETag=ref, immutable caching, nosniff, and a
// sanitized Content-Disposition. Inline is allowlisted (pdf/images);
// script-capable types download as attachments. If-None-Match on the ref
// answers 304.
func TestArtifactBlobRouteAuthAndHeaders(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	pdfBytes := []byte("%PDF-1.7 flattened deck")
	pdfRef, err := putBlob(pdfBytes, "application/pdf")
	if err != nil {
		t.Fatalf("putBlob pdf: %v", err)
	}
	htmlRef, err := putBlob([]byte("<!doctype html><script>alert(1)</script>"), "text/html")
	if err != nil {
		t.Fatalf("putBlob html: %v", err)
	}
	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("design", "blob route fixture", "Authorized assets", "AJ", map[string]string{"visibility": "organization"})
	if err != nil {
		t.Fatalf("createOSArtifactWithMetadata: %v", err)
	}
	if _, err := kanbanApp.appendArtifactAsset(artifact.ID, artifactAsset{Ref: pdfRef, Mime: "application/pdf", Kind: "pdf"}); err != nil {
		t.Fatalf("append pdf asset: %v", err)
	}
	if _, err := kanbanApp.appendArtifactAsset(artifact.ID, artifactAsset{Ref: htmlRef, Mime: "text/html", Kind: "export"}); err != nil {
		t.Fatalf("append html asset: %v", err)
	}

	// Method gate.
	recorder := httptest.NewRecorder()
	artifactBlobHandler(recorder, httptest.NewRequest(http.MethodPost, "/artifacts/blob?ref="+pdfRef, nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d, want 405", recorder.Code)
	}

	// Session gate: no cookie → 401, same contract as artifactsHandler.
	recorder = httptest.NewRecorder()
	artifactBlobHandler(recorder, httptest.NewRequest(http.MethodGet, "/artifacts/blob?ref="+pdfRef, nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out status=%d, want 401", recorder.Code)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	get := func(target string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		recorder := httptest.NewRecorder()
		artifactBlobHandler(recorder, req)
		return recorder
	}

	if recorder := get("/artifacts/blob?ref=not-a-ref", nil); recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid-ref status=%d, want 400", recorder.Code)
	}
	if recorder := get("/artifacts/blob?ref="+strings.Repeat("0", 64), nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("missing-blob status=%d, want 404", recorder.Code)
	}

	recorder = get("/artifacts/blob?ref="+pdfRef+"&name=deck.pdf", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	if !bytes.Equal(recorder.Body.Bytes(), pdfBytes) {
		t.Fatalf("body=%q, want the blob bytes verbatim", recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/pdf" {
		t.Fatalf("Content-Type=%q, want the pinned application/pdf", got)
	}
	if got := recorder.Header().Get("ETag"); got != `"`+pdfRef+`"` {
		t.Fatalf("ETag=%q, want the quoted ref", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "private, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control=%q, want private immutable forever", got)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options=%q, want nosniff", got)
	}
	if got := recorder.Header().Get("Content-Disposition"); got != `inline; filename="deck.pdf"` {
		t.Fatalf("Content-Disposition=%q, want inline with the supplied name", got)
	}

	// The declared query mime never overrides the pinned sidecar mime, and a
	// hostile name is reduced to its sanitized base.
	recorder = get("/artifacts/blob?ref="+pdfRef+"&name=../../evil.pdf", nil)
	if got := recorder.Header().Get("Content-Disposition"); got != `inline; filename="evil.pdf"` {
		t.Fatalf("Content-Disposition=%q, want the path-stripped base name", got)
	}
	recorder = get("/artifacts/blob?ref="+pdfRef, nil)
	if got := recorder.Header().Get("Content-Disposition"); got != `inline; filename="`+pdfRef+`"` {
		t.Fatalf("Content-Disposition=%q, want the ref as fallback filename", got)
	}

	// Script-capable mime → attachment, never inline on the app origin.
	recorder = get("/artifacts/blob?ref="+htmlRef+"&name=deck.html", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("html blob status=%d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Disposition"); got != `attachment; filename="deck.html"` {
		t.Fatalf("html Content-Disposition=%q, want attachment", got)
	}

	// Conditional revalidation: If-None-Match on the ref → 304, no body.
	recorder = get("/artifacts/blob?ref="+pdfRef, map[string]string{"If-None-Match": `"` + pdfRef + `"`})
	if recorder.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match status=%d, want 304", recorder.Code)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("304 carried a %d-byte body", recorder.Body.Len())
	}
	if got := recorder.Header().Get("ETag"); got != `"`+pdfRef+`"` {
		t.Fatalf("304 ETag=%q, want the quoted ref", got)
	}
}

// Pagination is additive: a bare GET returns exactly today's shape (no
// hasMore key), ?limit= windows the newest N, and following nextBefore walks
// strictly older windows until hasMore=false. Bad cursors 404, bad limits 400.
func TestArtifactsPaginationWindow(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	ids := make([]string, 0, 5)
	for index := 0; index < 5; index++ {
		artifact, appended, err := kanbanApp.createOSArtifact("research", "brief", "# Brief body "+strings.Repeat("x", index+1), "AJ")
		if err != nil || !appended {
			t.Fatalf("createOSArtifact %d: appended=%v err=%v", index, appended, err)
		}
		ids = append(ids, artifact.ID)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	list := func(target string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		artifactsHandler(recorder, req)
		return recorder
	}
	type listPayload struct {
		OK         bool                 `json:"ok"`
		Artifacts  []meetingMemoryEntry `json:"artifacts"`
		HasMore    *bool                `json:"hasMore"`
		NextBefore string               `json:"nextBefore"`
	}
	decode := func(recorder *httptest.ResponseRecorder) listPayload {
		t.Helper()
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
		}
		var payload listPayload
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode list response: %v", err)
		}
		return payload
	}
	windowIDs := func(payload listPayload) []string {
		got := make([]string, 0, len(payload.Artifacts))
		for _, artifact := range payload.Artifacts {
			got = append(got, artifact.ID)
		}
		return got
	}
	equalIDs := func(got []string, want []string) bool {
		if len(got) != len(want) {
			return false
		}
		for index := range got {
			if got[index] != want[index] {
				return false
			}
		}
		return true
	}

	// Default: today's exact shape — every artifact, and NO pagination keys
	// so the existing UI's response handling is untouched.
	payload := decode(list("/artifacts"))
	if !equalIDs(windowIDs(payload), ids) {
		t.Fatalf("default window=%v, want all of %v", windowIDs(payload), ids)
	}
	if payload.HasMore != nil {
		t.Fatalf("default response carries hasMore=%v, want the key absent", *payload.HasMore)
	}

	// limit only: the newest two, oldest → newest inside the window.
	payload = decode(list("/artifacts?limit=2"))
	if !equalIDs(windowIDs(payload), ids[3:5]) {
		t.Fatalf("limit=2 window=%v, want %v", windowIDs(payload), ids[3:5])
	}
	if payload.HasMore == nil || !*payload.HasMore || payload.NextBefore != ids[3] {
		t.Fatalf("limit=2 hasMore=%v nextBefore=%q, want true/%q", payload.HasMore, payload.NextBefore, ids[3])
	}

	// Follow the cursor: strictly older than ids[3].
	payload = decode(list("/artifacts?before=" + ids[3] + "&limit=2"))
	if !equalIDs(windowIDs(payload), ids[1:3]) {
		t.Fatalf("cursor window=%v, want %v", windowIDs(payload), ids[1:3])
	}
	if payload.HasMore == nil || !*payload.HasMore || payload.NextBefore != ids[1] {
		t.Fatalf("cursor hasMore=%v nextBefore=%q, want true/%q", payload.HasMore, payload.NextBefore, ids[1])
	}

	// Final window: the oldest artifact, hasMore=false, no further cursor.
	payload = decode(list("/artifacts?before=" + ids[1] + "&limit=2"))
	if !equalIDs(windowIDs(payload), ids[0:1]) {
		t.Fatalf("final window=%v, want %v", windowIDs(payload), ids[0:1])
	}
	if payload.HasMore == nil || *payload.HasMore || payload.NextBefore != "" {
		t.Fatalf("final hasMore=%v nextBefore=%q, want false/empty", payload.HasMore, payload.NextBefore)
	}

	// Paging past the oldest artifact is an empty window, not an error.
	payload = decode(list("/artifacts?before=" + ids[0] + "&limit=2"))
	if len(payload.Artifacts) != 0 || payload.HasMore == nil || *payload.HasMore {
		t.Fatalf("past-the-end window=%v hasMore=%v, want empty/false", windowIDs(payload), payload.HasMore)
	}

	if recorder := list("/artifacts?before=os-artifact-unknown"); recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown cursor status=%d, want 404", recorder.Code)
	}
	for _, bad := range []string{"0", "-3", "abc"} {
		if recorder := list("/artifacts?limit=" + bad); recorder.Code != http.StatusBadRequest {
			t.Fatalf("limit=%s status=%d, want 400", bad, recorder.Code)
		}
	}
}

// The single-artifact window is additive too: ?id=<artifact-id> returns
// exactly that artifact in the same {artifacts: [...]} shape (404 when it
// does not exist). This is the goalcard's door to a goal parent buried under
// 100+ of its own stage children — outside the newest-100 default window —
// so a parked checkpoint can still render its choices.
func TestArtifactsFetchByID(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	artifact, appended, err := kanbanApp.createOSArtifact("research", "parked goal", "# Goal record", "AJ")
	if err != nil || !appended {
		t.Fatalf("createOSArtifact: appended=%v err=%v", appended, err)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	get := func(target string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		artifactsHandler(recorder, req)
		return recorder
	}

	recorder := get("/artifacts?id=" + artifact.ID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		OK        bool                 `json:"ok"`
		Artifacts []meetingMemoryEntry `json:"artifacts"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode by-id response: %v", err)
	}
	if !payload.OK || len(payload.Artifacts) != 1 || payload.Artifacts[0].ID != artifact.ID {
		t.Fatalf("by-id window=%+v, want exactly %q", payload.Artifacts, artifact.ID)
	}

	if recorder := get("/artifacts?id=os-artifact-unknown"); recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown id status=%d, want 404", recorder.Code)
	}
}

// Wave 5 D10: the admin action. Dry run reports counts and deletes nothing;
// the immediate form deletes every unreferenced blob; non-admins are 403 and
// the signed-out caller 401.
func TestBlobSweepAdminActionDryRunThenDelete(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	admin := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	member := loginAs(t, "tim@shareability.com", "B0NFIRE!")

	keptRef, err := putBlob([]byte("referenced drive upload"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.memory.appendEntry(meetingMemoryKindFile, "file-sweep-kept", "File kept.txt uploaded.", map[string]string{
		"name": "kept.txt", "blobRef": keptRef, "mime": "text/plain", "uploaderEmail": "tim@shareability.com",
	}); err != nil {
		t.Fatal(err)
	}
	orphanBody := []byte("abandoned intermediate raster for the admin action")
	orphanRef, err := putBlob(orphanBody, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	// a walker-gap blob: mentioned only under a metadata key the reference
	// walk does not collect — "sweep now" must not delete it either
	gapBody := []byte("scene bytes stored under a key the walker does not know")
	gapRef, err := putBlob(gapBody, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if _, appended, err := app.memory.appendOSArtifact("artifact-admin-gap", "# Deck\n\nBody.", map[string]string{
		"title": "Deck", "mode": "design", "futureSceneRef": gapRef,
	}); err != nil || !appended {
		t.Fatalf("append gap artifact: appended=%v err=%v", appended, err)
	}

	sweep := func(body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/assistant/admin/blobs/sweep", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		adminBlobSweepHandler(recorder, req)
		return recorder
	}
	if recorder := sweep(`{"dryRun":true}`, nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out sweep status=%d, want 401", recorder.Code)
	}
	if recorder := sweep(`{"dryRun":true}`, member); recorder.Code != http.StatusForbidden {
		t.Fatalf("member sweep status=%d, want 403", recorder.Code)
	}

	dry := sweep(`{"dryRun":true}`, admin)
	if dry.Code != http.StatusOK {
		t.Fatalf("dry run status=%d body=%s", dry.Code, dry.Body.String())
	}
	dryPayload := decodeJSON(t, dry)
	if dryPayload["dryRun"] != true || dryPayload["scanned"] != float64(3) || dryPayload["unreferenced"] != float64(2) || dryPayload["deleted"] != float64(0) || dryPayload["deletedBytes"] != float64(0) || dryPayload["protected"] != float64(1) {
		t.Fatalf("dry run payload=%v, want scanned=3 unreferenced=2 deleted=0 protected=1", dryPayload)
	}
	if _, _, err := getBlob(orphanRef); err != nil {
		t.Fatalf("dry run deleted the orphan: %v", err)
	}

	real := sweep(`{"dryRun":false}`, admin)
	if real.Code != http.StatusOK {
		t.Fatalf("sweep status=%d body=%s", real.Code, real.Body.String())
	}
	realPayload := decodeJSON(t, real)
	if realPayload["dryRun"] != false || realPayload["unreferenced"] != float64(2) || realPayload["deleted"] != float64(1) || realPayload["deletedBytes"] != float64(len(orphanBody)) || realPayload["protected"] != float64(1) || realPayload["forced"] != false {
		t.Fatalf("sweep payload=%v, want unreferenced=2 deleted=1 (orphan only) protected=1", realPayload)
	}
	if _, _, err := getBlob(orphanRef); err == nil {
		t.Fatal("orphan survived the immediate admin sweep")
	}
	if _, _, err := getBlob(keptRef); err != nil {
		t.Fatalf("referenced Drive blob deleted by the admin sweep: %v", err)
	}
	if _, _, err := getBlob(gapRef); err != nil {
		t.Fatalf("mentioned-but-uncollected blob deleted by the admin sweep: %v", err)
	}

	// force needs a reason; with one it is the only override
	if recorder := sweep(`{"dryRun":false,"force":true}`, admin); recorder.Code != http.StatusBadRequest {
		t.Fatalf("force without a reason status=%d, want 400", recorder.Code)
	}
	if _, _, err := getBlob(gapRef); err != nil {
		t.Fatalf("reasonless force deleted the protected blob: %v", err)
	}
	forced := sweep(`{"dryRun":false,"force":true,"reason":"walker extended in the next release; scene re-rendered"}`, admin)
	if forced.Code != http.StatusOK {
		t.Fatalf("forced sweep status=%d body=%s", forced.Code, forced.Body.String())
	}
	forcedPayload := decodeJSON(t, forced)
	if forcedPayload["deleted"] != float64(1) || forcedPayload["deletedBytes"] != float64(len(gapBody)) || forcedPayload["forced"] != true || forcedPayload["protected"] != float64(0) {
		t.Fatalf("forced payload=%v, want the protected blob deleted", forcedPayload)
	}
	if _, _, err := getBlob(gapRef); err == nil {
		t.Fatal("forced sweep left the protected blob")
	}
}

// Wave 5 D10: the weekly job. A referenced blob (live or trashed Drive row,
// live file share link) always survives; an unreferenced blob is deleted only
// on the second consecutive weekly run; a run inside the interval is a no-op;
// the first sighting is a dry run that deletes nothing.
func TestBlobScheduledSweepDeletesOnlyAfterTwoConsecutiveRuns(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	liveRef, _ := putBlob([]byte("live drive row"), "text/plain")
	trashedRef, _ := putBlob([]byte("trashed drive row awaiting purge"), "text/plain")
	linkedRef, _ := putBlob([]byte("bytes behind a live file share link"), "text/plain")
	orphanRef, _ := putBlob([]byte("first orphan"), "image/jpeg")
	for id, ref := range map[string]string{"file-live": liveRef, "file-trashed": trashedRef, "file-linked": linkedRef} {
		metadata := map[string]string{"name": id + ".txt", "blobRef": ref, "mime": "text/plain", "uploaderEmail": "tim@shareability.com"}
		if id == "file-trashed" {
			metadata["deletedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
		}
		if _, _, err := app.memory.appendEntry(meetingMemoryKindFile, id, "File uploaded.", metadata); err != nil {
			t.Fatal(err)
		}
	}
	// A live file share link keeps its bound blob even after its row is gone.
	if err := saveShareLinks([]shareLinkRecord{{
		ID: "share-link-file-gc", FileID: "file-linked", TenantID: canonicalArtifactTenantID(), ObjectType: shareLinkObjectTypeFile,
		ContentDigest: linkedRef, Action: "read_content", Status: shareLinkStatusActive, TokenHash: strings.Repeat("ab", 32),
		CreatedBy: "tim@shareability.com", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano),
	}}); err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC)
	report, ran, err := app.runScheduledBlobSweep(start)
	if err != nil || !ran {
		t.Fatalf("first run ran=%v err=%v", ran, err)
	}
	if report.Deleted != 0 || report.Unreferenced != 1 || report.Scanned != 4 {
		t.Fatalf("first run report=%+v, want scanned=4 unreferenced=1 deleted=0 (dry-run first sighting)", report)
	}
	if _, _, err := getBlob(orphanRef); err != nil {
		t.Fatalf("first sighting deleted the orphan: %v", err)
	}
	state, err := loadBlobSweepState()
	if err != nil || len(state.PendingRefs) != 1 || state.PendingRefs[0] != orphanRef {
		t.Fatalf("state after first run=%+v err=%v, want the orphan pending", state, err)
	}

	// Inside the weekly interval: nothing runs, nothing changes.
	if _, ran, err := app.runScheduledBlobSweep(start.Add(3 * 24 * time.Hour)); err != nil || ran {
		t.Fatalf("mid-interval run ran=%v err=%v, want a no-op", ran, err)
	}

	// A second orphan appears between runs: it is sighted, not deleted.
	secondOrphanRef, _ := putBlob([]byte("second orphan"), "image/jpeg")
	report, ran, err = app.runScheduledBlobSweep(start.Add(blobSweepInterval))
	if err != nil || !ran {
		t.Fatalf("second run ran=%v err=%v", ran, err)
	}
	if report.Deleted != 1 || len(report.DeletedRefs) != 1 || report.DeletedRefs[0] != orphanRef {
		t.Fatalf("second run report=%+v, want exactly the twice-sighted orphan deleted", report)
	}
	if _, _, err := getBlob(orphanRef); err == nil {
		t.Fatal("orphan survived its second consecutive sighting")
	}
	if _, _, err := getBlob(secondOrphanRef); err != nil {
		t.Fatalf("second orphan deleted on its first sighting: %v", err)
	}
	for name, ref := range map[string]string{"live": liveRef, "trashed": trashedRef, "linked": linkedRef} {
		if _, _, err := getBlob(ref); err != nil {
			t.Fatalf("referenced %s blob deleted by the weekly sweep: %v", name, err)
		}
	}
	state, _ = loadBlobSweepState()
	if len(state.PendingRefs) != 1 || state.PendingRefs[0] != secondOrphanRef || state.LastDeleted != 1 {
		t.Fatalf("state after second run=%+v, want only the second orphan pending", state)
	}

	// Third run: the second orphan goes; nothing else does.
	report, ran, err = app.runScheduledBlobSweep(start.Add(2 * blobSweepInterval))
	if err != nil || !ran || report.Deleted != 1 || report.DeletedRefs[0] != secondOrphanRef {
		t.Fatalf("third run ran=%v err=%v report=%+v, want the second orphan deleted", ran, err, report)
	}
	if _, ran, err := app.runScheduledBlobSweep(start.Add(2*blobSweepInterval + time.Hour)); err != nil || ran {
		t.Fatalf("post-run repeat ran=%v err=%v, want a no-op", ran, err)
	}
}

// Registered reference walkers (Wave 7 recordings register theirs at init)
// keep their refs alive across a sweep; putBlobWithCap honors a per-write
// ceiling above the store default.
func TestBlobReferenceWalkersAndPutBlobWithCap(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	walked, err := putBlob([]byte("bytes kept alive by a registered walker"), "video/webm")
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := putBlob([]byte("orphan beside the walked ref"), "video/webm")
	if err != nil {
		t.Fatal(err)
	}
	registerBlobReferenceWalker(func(app *kanbanBoardApp) []string { return []string{walked, "not-a-ref"} })
	t.Cleanup(func() {
		blobReferenceWalkersMu.Lock()
		blobReferenceWalkers = blobReferenceWalkers[:len(blobReferenceWalkers)-1]
		blobReferenceWalkersMu.Unlock()
	})
	deleted, err := sweepUnreferencedBlobs(app)
	if err != nil || len(deleted) != 1 || deleted[0] != orphan {
		t.Fatalf("deleted=%v err=%v, want only the orphan", deleted, err)
	}
	if _, _, err := getBlob(walked); err != nil {
		t.Fatalf("walker-referenced blob deleted: %v", err)
	}

	big := make([]byte, blobMaxBytes+1)
	if _, err := putBlob(big, "video/webm"); err == nil {
		t.Fatal("putBlob accepted a payload over the default cap")
	}
	if _, err := putBlobWithCap(big, "video/webm", blobMaxBytes+2); err != nil {
		t.Fatalf("putBlobWithCap rejected a payload under its own cap: %v", err)
	}
	if _, err := putBlobWithCap([]byte("x"), "video/webm", 0); err != nil {
		t.Fatalf("non-positive cap must fall back to the default: %v", err)
	}
	if !blobInlineSafeMimes["video/webm"] || !blobInlineSafeMimes["audio/webm"] {
		t.Fatal("webm media must be inline-safe for Meeting Record playback")
	}
}

// 2026-09-02: a blob referenced ONLY through a metadata key the reference
// walk does not know (the deck-scene class of bug) is flagged by the mention
// audit, reported by the founder route with a producer guess, and never
// deleted by the weekly job even on its second sighting.
func TestBlobSweepReportFlagsRefMentionedOnlyByUnknownMetadataKey(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	// a legitimately referenced Drive row keeps the walk's fail-safe (rows but
	// no refs) out of the picture
	keptRef, err := putBlob([]byte("referenced drive upload beside the mystery"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.memory.appendEntry(meetingMemoryKindFile, "file-report-kept", "File kept.txt uploaded.", map[string]string{
		"name": "kept.txt", "blobRef": keptRef, "mime": "text/plain", "uploaderEmail": "tim@shareability.com",
	}); err != nil {
		t.Fatal(err)
	}
	mysteryRef, err := putBlob([]byte("bytes a new producer stored under a key the walker does not know"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if _, appended, err := app.memory.appendOSArtifact("artifact-mystery-producer", "# Deck\n\nBody.", map[string]string{
		"title": "Deck", "mode": "design", "futureSceneRef": mysteryRef,
	}); err != nil || !appended {
		t.Fatalf("append artifact: appended=%v err=%v", appended, err)
	}
	orphanRef, err := putBlob([]byte("a genuine orphan nobody mentions"), "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}

	mentions, _ := blobRefMentions(app, blobRefSet([]string{mysteryRef, orphanRef}))
	if hits := mentions[mysteryRef]; len(hits) != 1 || hits[0].Kind != meetingMemoryKindOSArtifact || hits[0].ID != "artifact-mystery-producer" || hits[0].Key != "futureSceneRef" {
		t.Fatalf("mystery mentions=%+v, want the unknown metadata key flagged", hits)
	}
	if _, mentioned := mentions[orphanRef]; mentioned {
		t.Fatalf("orphan reported a mention: %+v", mentions[orphanRef])
	}
	if guess := blobProducerGuess(mentions[mysteryRef]); guess != "artifact metadata futureSceneRef" {
		t.Fatalf("producer guess=%q", guess)
	}

	// weekly job: first sighting (dry run) then second a week later — the
	// mentioned ref is protected, the true orphan is deleted.
	start := time.Date(2026, 9, 2, 3, 0, 0, 0, time.UTC)
	if _, ran, err := app.runScheduledBlobSweep(start); err != nil || !ran {
		t.Fatalf("first run ran=%v err=%v", ran, err)
	}
	state, err := loadBlobSweepState()
	if err != nil || len(state.PendingRefs) != 2 {
		t.Fatalf("state=%+v err=%v, want both refs pending after the first sighting", state, err)
	}
	report, ran, err := app.runScheduledBlobSweep(start.Add(blobSweepInterval))
	if err != nil || !ran {
		t.Fatalf("second run ran=%v err=%v", ran, err)
	}
	if report.Deleted != 1 || len(report.DeletedRefs) != 1 || report.DeletedRefs[0] != orphanRef {
		t.Fatalf("second run report=%+v, want only the unmentioned orphan deleted", report)
	}
	if _, _, err := getBlob(mysteryRef); err != nil {
		t.Fatalf("mentioned-but-uncollected blob was deleted: %v", err)
	}
	if _, _, err := getBlob(keptRef); err != nil {
		t.Fatalf("referenced blob was deleted: %v", err)
	}

	// founder route: pending refs with producer guesses; members and the
	// signed-out get nothing.
	get := func(query string, cookies []*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/assistant/admin/blobs/sweep?"+query, nil)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		adminBlobSweepHandler(recorder, req)
		return recorder
	}
	if recorder := get("pending=1", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out report status=%d, want 401", recorder.Code)
	}
	if recorder := get("pending=1", loginAs(t, "tim@shareability.com", "B0NFIRE!")); recorder.Code != http.StatusForbidden {
		t.Fatalf("member report status=%d, want 403", recorder.Code)
	}
	founder := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	if recorder := get("", founder); recorder.Code != http.StatusBadRequest {
		t.Fatalf("report without a mode status=%d, want 400", recorder.Code)
	}
	recorder := get("pending=1", founder)
	if recorder.Code != http.StatusOK {
		t.Fatalf("founder report status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		OK                      bool             `json:"ok"`
		PendingCount            int              `json:"pendingCount"`
		MentionedButUncollected int              `json:"mentionedButUncollected"`
		Refs                    []blobPendingRef `json:"refs"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if !payload.OK || payload.PendingCount != 1 || payload.MentionedButUncollected != 1 || len(payload.Refs) != 1 {
		t.Fatalf("report=%+v, want the protected ref still pending and flagged", payload)
	}
	row := payload.Refs[0]
	if row.Ref != mysteryRef || !row.Mentioned || row.Producer != "artifact metadata futureSceneRef" || row.Size == 0 || row.Mime != "image/png" || row.CreatedAt == "" {
		t.Fatalf("report row=%+v", row)
	}

	// read-only report mode: a fresh orphan is sighted, nothing deleted, the
	// two-sighting state untouched
	t.Setenv(blobSweepReportOnlyEnv, "1")
	secondOrphan, _ := putBlob([]byte("second orphan under report-only"), "image/jpeg")
	stateBefore, _ := os.ReadFile(blobSweepStatePath())
	report, ran, err = app.runScheduledBlobSweep(start.Add(3 * blobSweepInterval))
	if err != nil || !ran || report.Deleted != 0 || report.Unreferenced != 2 {
		t.Fatalf("report-only run ran=%v err=%v report=%+v, want a dry run over both refs", ran, err, report)
	}
	stateAfter, _ := os.ReadFile(blobSweepStatePath())
	if string(stateBefore) != string(stateAfter) {
		t.Fatal("report-only mode wrote the sweep state")
	}
	if _, _, err := getBlob(secondOrphan); err != nil {
		t.Fatalf("report-only mode deleted a blob: %v", err)
	}
	scan := get("scan=1", founder)
	if scan.Code != http.StatusOK || !strings.Contains(scan.Body.String(), secondOrphan) || !strings.Contains(scan.Body.String(), `"reportOnlyMode":true`) {
		t.Fatalf("scan report status=%d body=%s", scan.Code, scan.Body.String())
	}
}

func TestScanBlobRefTokens(t *testing.T) {
	ref := strings.Repeat("ab", 32)
	var found []string
	scanBlobRefTokens("see /artifacts/blob/"+ref+" and "+ref+ref+" and "+strings.ToUpper(ref)+" then "+ref, func(hit string) { found = append(found, hit) })
	if len(found) != 2 || found[0] != ref || found[1] != ref {
		t.Fatalf("found=%v, want exactly the two 64-hex runs (not the 128-run, not uppercase)", found)
	}
}
