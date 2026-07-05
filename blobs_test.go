package main

import (
	"bytes"
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
