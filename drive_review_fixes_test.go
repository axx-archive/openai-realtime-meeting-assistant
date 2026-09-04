package main

// drive_review_fixes_test.go pins the Drive/blob code-review fixes: D3 (deck
// scene refs survive the weekly sweep + fail-safe), D1 (document import
// inherits the source's reach), D2 (version chains re-head across trash /
// restore / purge), D5 (restore reads through the Drive seam), D6 (upload
// rolls back a failed chain stamp), D7 (PATCH validates every sub-update
// first), D10/D11 (one scan, one rewrite), M12 (store-level ref lookup),
// D8 (per-version share-link counts) and B4 (per-recipient "file" events).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func driveRowsNamed(rows []assistantFileRecord, name string) []assistantFileRecord {
	var out []assistantFileRecord
	for _, row := range rows {
		if row.Name == name {
			out = append(out, row)
		}
	}
	return out
}

func driveRowIDs(rows []assistantFileRecord) string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return strings.Join(ids, ",")
}

func driveFileMetadata(t *testing.T, id string) map[string]string {
	t.Helper()
	entry, ok := kanbanApp.memory.entryByKindAndID(meetingMemoryKindFile, id)
	if !ok {
		t.Fatalf("file row %s missing", id)
	}
	return entry.Metadata
}

// D3: the Deck Studio scene blob lives ONLY in deckSceneRef metadata and each
// superseded revision's scene only in the version journal's SceneRef. Both
// must survive two weekly sweeps; the orphan beside them must not.
func TestBlobSweepKeepsDeckSceneRefsAndVersionSceneRefs(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	sceneV1, err := putBlob([]byte(`{"schemaVersion":1,"slides":[{"id":"slide-1","elements":[]}]}`), "application/vnd.bonfire.deck+json")
	if err != nil {
		t.Fatal(err)
	}
	sceneV2, err := putBlob([]byte(`{"schemaVersion":1,"slides":[{"id":"slide-1","elements":[]},{"id":"slide-2","elements":[]}]}`), "application/vnd.bonfire.deck+json")
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := putBlob([]byte("abandoned raster beside the deck scenes"), "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	artifact, appended, err := app.createOSArtifactWithMetadata("artifacts", "Native deck", "<!doctype html><html><body>v1</body></html>", "AJ", map[string]string{
		"type": artifactTypeHTMLDeck, deckSceneRefMetadataKey: sceneV1, "visibility": "organization", "ownerEmail": "aj@shareability.com",
	})
	if err != nil || !appended {
		t.Fatalf("create deck artifact appended=%v err=%v", appended, err)
	}
	edited, changed, err := app.updateOSArtifactWithMetadata(artifact.ID, "Native deck", "<!doctype html><html><body>v2</body></html>", "AJ", map[string]string{deckSceneRefMetadataKey: sceneV2})
	if err != nil || !changed {
		t.Fatalf("edit deck artifact changed=%v err=%v", changed, err)
	}
	history := artifactVersionHistory(edited)
	if len(history) != 1 || history[0].SceneRef != sceneV1 || edited.Metadata[deckSceneRefMetadataKey] != sceneV2 {
		t.Fatalf("history=%+v current scene=%q, want v1 scene journaled and v2 scene current", history, edited.Metadata[deckSceneRefMetadataKey])
	}

	start := time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC)
	first, ran, err := app.runScheduledBlobSweep(start)
	if err != nil || !ran || first.Deleted != 0 {
		t.Fatalf("first run report=%+v ran=%v err=%v", first, ran, err)
	}
	second, ran, err := app.runScheduledBlobSweep(start.Add(blobSweepInterval))
	if err != nil || !ran {
		t.Fatalf("second run ran=%v err=%v", ran, err)
	}
	if second.Deleted != 1 || len(second.DeletedRefs) != 1 || second.DeletedRefs[0] != orphan {
		t.Fatalf("second run deleted=%v, want exactly the orphan %s", second.DeletedRefs, orphan)
	}
	for label, ref := range map[string]string{"current scene": sceneV2, "superseded scene": sceneV1, "version body": history[0].BodyBlobRef} {
		if _, _, err := getBlob(ref); err != nil {
			t.Fatalf("%s %s swept after two weekly runs: %v", label, ref, err)
		}
	}
	if _, _, err := getBlob(orphan); err == nil {
		t.Fatal("orphan survived two weekly runs")
	}
}

// D3 fail-safe: rows that CLAIM blob bytes exist but the walk produced no refs
// at all — a broken walk, not an empty workspace. Both the admin sweep and the
// weekly job must refuse rather than orphan-classify every blob.
func TestBlobSweepRefusesWhenReferenceWalkYieldsNoRefs(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	// 2026-09-02: the trigger is now a row whose ref-bearing metadata is
	// present but no longer decodes, not merely an artifact that holds no
	// blobs — that second shape is an ordinary chat-only workspace, and
	// counting it refused the chat-media retention walk every single day.
	if _, appended, err := app.memory.appendOSArtifact("artifact-undecodable-assets", "# Notes with no readable blobs", map[string]string{
		"title": "Notes", "mode": "research", artifactAssetsMetadataKey: "{not json at all",
	}); err != nil || !appended {
		t.Fatalf("append artifact claiming undecodable assets: appended=%v err=%v", appended, err)
	}
	orphan, err := putBlob([]byte("would be an orphan if the walk were trusted"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if deleted, err := sweepUnreferencedBlobs(app); err == nil || len(deleted) != 0 {
		t.Fatalf("zero-ref walk deleted=%v err=%v, want a refusal", deleted, err)
	}
	if _, ran, err := app.runScheduledBlobSweep(time.Now().UTC()); err == nil || !ran {
		t.Fatalf("weekly run ran=%v err=%v, want ran with a refusal", ran, err)
	}
	if _, _, err := getBlob(orphan); err != nil {
		t.Fatalf("refused sweep still deleted the blob: %v", err)
	}
	// A single real reference makes the walk trustworthy again.
	if _, _, err := app.memory.appendEntry(meetingMemoryKindFile, "file-anchor", "File anchor.txt uploaded.", map[string]string{"name": "anchor.txt", "blobRef": orphan, "uploaderEmail": "aj@shareability.com"}); err != nil {
		t.Fatal(err)
	}
	if deleted, err := sweepUnreferencedBlobs(app); err != nil || len(deleted) != 0 {
		t.Fatalf("referenced walk deleted=%v err=%v, want a clean no-op", deleted, err)
	}
}

// D1: an imported document carries its source's reach. A private or
// people-shared Drive file becomes a PRIVATE document owned by the importer;
// a company file stays organization-visible; provenance records the source.
func TestDocumentImportInheritsSourceFileAccess(t *testing.T) {
	ajCookies, _ := setupDocumentEditorHTTPTest(t)
	timCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	seed := func(id, name, uploader string, extra map[string]string) meetingMemoryEntry {
		t.Helper()
		body := "# " + name + "\n\nimported body\n"
		ref, err := putBlob([]byte(body), "text/markdown")
		if err != nil {
			t.Fatal(err)
		}
		metadata := map[string]string{
			"name": name, "blobRef": ref, "mime": "text/markdown", "size": strconv.Itoa(len(body)),
			"uploaderEmail": uploader, "uploaderName": uploader, "origin": "files", "brainStatus": fileBrainStatusStored,
		}
		for key, value := range extra {
			metadata[key] = value
		}
		entry, _, err := kanbanApp.memory.appendEntry(meetingMemoryKindFile, id, "File "+name+" uploaded by "+uploader+".", metadata)
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	importFile := func(cookies []*http.Cookie, fileID string) (string, int) {
		t.Helper()
		response := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/import", `{"fileId":"`+fileID+`"}`, cookies, documentEditorImportHandler)
		var created struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &created)
		return created.ID, response.Code
	}
	listedFor := func(cookies []*http.Cookie, artifactID string) bool {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/artifacts", nil)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		artifactsHandler(recorder, req)
		if recorder.Code != http.StatusOK {
			t.Fatalf("list artifacts status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var payload struct {
			Artifacts []meetingMemoryEntry `json:"artifacts"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		for _, artifact := range payload.Artifacts {
			if artifact.ID == artifactID {
				return true
			}
		}
		return false
	}
	documentStatus := func(cookies []*http.Cookie, artifactID string) int {
		t.Helper()
		return artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/document?id="+artifactID, "", cookies, documentEditorHandler).Code
	}

	private := seed("file-import-private", "runway_plan.md", "aj@shareability.com", map[string]string{"visibility": fileVisibilityPrivate})
	privateDoc, status := importFile(ajCookies, private.ID)
	if status != http.StatusCreated || privateDoc == "" {
		t.Fatalf("private import status=%d id=%q", status, privateDoc)
	}
	stored, _ := kanbanApp.osArtifactByID(privateDoc)
	if stored.Metadata["visibility"] != "private" || stored.Metadata["ownerEmail"] != "aj@shareability.com" ||
		stored.Metadata["importedFromVisibility"] != fileVisibilityPrivate || stored.Metadata["importedFromUploaderEmail"] != "aj@shareability.com" {
		t.Fatalf("private import metadata=%v, want a private document owned by the importer with provenance", stored.Metadata)
	}
	if documentStatus(ajCookies, privateDoc) != http.StatusOK {
		t.Fatal("importer cannot open the document they imported")
	}
	if documentStatus(timCookies, privateDoc) != http.StatusNotFound || listedFor(timCookies, privateDoc) {
		t.Fatal("private Drive file imported as an org-readable document")
	}

	// A people-shared file imported by its grantee: private to the grantee,
	// provenance names the uploader and grants.
	people := seed("file-import-people", "brief.md", "tim@shareability.com", map[string]string{"visibility": fileVisibilityPeople, "grants": "aj@shareability.com"})
	peopleDoc, status := importFile(ajCookies, people.ID)
	if status != http.StatusCreated {
		t.Fatalf("people import status=%d", status)
	}
	stored, _ = kanbanApp.osArtifactByID(peopleDoc)
	if stored.Metadata["visibility"] != "private" || stored.Metadata["ownerEmail"] != "aj@shareability.com" ||
		stored.Metadata["importedFromUploaderEmail"] != "tim@shareability.com" || stored.Metadata["importedFromGrants"] != "aj@shareability.com" {
		t.Fatalf("people import metadata=%v", stored.Metadata)
	}
	if listedFor(loginAs(t, "joel@shareability.com", "B0NFIRE!"), peopleDoc) {
		t.Fatal("people-shared Drive file imported as an org-readable document")
	}

	// A company file keeps organization reach.
	company := seed("file-import-company", "offsite.md", "aj@shareability.com", map[string]string{"visibility": fileVisibilityCompany})
	companyDoc, status := importFile(ajCookies, company.ID)
	if status != http.StatusCreated {
		t.Fatalf("company import status=%d", status)
	}
	stored, _ = kanbanApp.osArtifactByID(companyDoc)
	if stored.Metadata["visibility"] != "organization" || !listedFor(timCookies, companyDoc) || documentStatus(timCookies, companyDoc) != http.StatusOK {
		t.Fatalf("company import metadata=%v, want organization reach", stored.Metadata)
	}
}

// D2 (trash): trashing the head of a version chain re-heads the chain — the
// prior version returns to the live list un-superseded, the versions lane
// still shows the whole chain, and the next same-name upload continues the
// count past the trashed version.
func TestAssistantFileTrashedHeadReheadsPriorVersion(t *testing.T) {
	ajCookies, _ := setupDriveWaveTest(t)
	v1 := uploadDriveFileRow(t, ajCookies, "deck.pdf", "application/pdf", []byte("%PDF-1.7 one"), nil)
	v2 := uploadDriveFileRow(t, ajCookies, "deck.pdf", "application/pdf", []byte("%PDF-1.7 two"), nil)
	if v2.VersionOf != v1.ID || v2.Version != 2 {
		t.Fatalf("chain v2=%+v", v2)
	}
	if response := deleteDriveFileRequest(t, ajCookies, v2.ID); response.Code != http.StatusOK {
		t.Fatalf("trash v2 status=%d body=%s", response.Code, response.Body.String())
	}
	live := driveRowsNamed(listDriveFiles(t, ajCookies, "").Files, "deck.pdf")
	if len(live) != 1 || live[0].ID != v1.ID || live[0].Superseded {
		t.Fatalf("live rows after trashing the head=%+v, want exactly v1 un-superseded", live)
	}
	if metadata := driveFileMetadata(t, v1.ID); fileEntrySuperseded(metadata) || metadata["supersededBy"] != "" || fileEntryVersion(metadata) != 1 {
		t.Fatalf("v1 metadata after re-head=%v", metadata)
	}
	versions := listDriveFiles(t, ajCookies, "versionsOf="+url.QueryEscape(v1.ID)).Files
	if driveRowIDs(versions) != v2.ID+","+v1.ID || versions[0].DeletedAt == "" || versions[1].Superseded {
		t.Fatalf("versions after trash=%+v, want [v2 trashed, v1 live]", versions)
	}
	v3 := uploadDriveFileRow(t, ajCookies, "deck.pdf", "application/pdf", []byte("%PDF-1.7 three"), nil)
	if v3.VersionOf != v1.ID || v3.Version != 3 {
		t.Fatalf("upload after trash=%+v, want versionOf v1 at version 3 (never a second v2)", v3)
	}
	live = driveRowsNamed(listDriveFiles(t, ajCookies, "").Files, "deck.pdf")
	if len(live) != 1 || live[0].ID != v3.ID {
		t.Fatalf("live rows after v3=%+v, want exactly v3", live)
	}
	if metadata := driveFileMetadata(t, v1.ID); !fileEntrySuperseded(metadata) || metadata["supersededBy"] != v3.ID {
		t.Fatalf("v1 not re-superseded by v3: %v", metadata)
	}
	if chain := listDriveFiles(t, ajCookies, "versionsOf="+url.QueryEscape(v3.ID)).Files; driveRowIDs(chain) != strings.Join([]string{v3.ID, v2.ID, v1.ID}, ",") {
		t.Fatalf("chain from v3=%s, want v3,v2,v1", driveRowIDs(chain))
	}
}

// D2 (restore): restoring a trashed head re-supersedes the interim head; a
// restore that is no longer the newest version comes back superseded.
func TestAssistantFileRestoredHeadResupersedesPrior(t *testing.T) {
	ajCookies, _ := setupDriveWaveTest(t)
	v1 := uploadDriveFileRow(t, ajCookies, "plan.pdf", "application/pdf", []byte("%PDF-1.7 one"), nil)
	v2 := uploadDriveFileRow(t, ajCookies, "plan.pdf", "application/pdf", []byte("%PDF-1.7 two"), nil)
	restore := func(id string) *httptest.ResponseRecorder {
		t.Helper()
		return postDriveJSON(t, assistantFileRestoreHandler, "/assistant/files/restore", ajCookies, fmt.Sprintf(`{"id":%q}`, id))
	}
	if response := deleteDriveFileRequest(t, ajCookies, v2.ID); response.Code != http.StatusOK {
		t.Fatalf("trash v2 status=%d", response.Code)
	}
	if metadata := driveFileMetadata(t, v1.ID); fileEntrySuperseded(metadata) {
		t.Fatal("v1 still superseded while v2 is in the trash")
	}
	response := restore(v2.ID)
	if response.Code != http.StatusOK {
		t.Fatalf("restore v2 status=%d body=%s", response.Code, response.Body.String())
	}
	var restored struct {
		File assistantFileRecord `json:"file"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &restored); err != nil {
		t.Fatal(err)
	}
	if restored.File.ID != v2.ID || restored.File.Superseded || restored.File.DeletedAt != "" {
		t.Fatalf("restored row=%+v, want the live head", restored.File)
	}
	live := driveRowsNamed(listDriveFiles(t, ajCookies, "").Files, "plan.pdf")
	if len(live) != 1 || live[0].ID != v2.ID {
		t.Fatalf("live rows after restore=%+v, want exactly v2", live)
	}
	if metadata := driveFileMetadata(t, v1.ID); !fileEntrySuperseded(metadata) || metadata["supersededBy"] != v2.ID {
		t.Fatalf("v1 not re-superseded after restore: %v", metadata)
	}
	if chain := listDriveFiles(t, ajCookies, "versionsOf="+url.QueryEscape(v1.ID)).Files; driveRowIDs(chain) != v2.ID+","+v1.ID || chain[0].Superseded || !chain[1].Superseded {
		t.Fatalf("chain after restore=%+v", chain)
	}

	// Trash v2 again, upload v3 onto the re-headed v1, then restore v2: v3 is
	// newer, so v2 returns superseded and v3 stays the one live row.
	if response := deleteDriveFileRequest(t, ajCookies, v2.ID); response.Code != http.StatusOK {
		t.Fatalf("second trash v2 status=%d", response.Code)
	}
	v3 := uploadDriveFileRow(t, ajCookies, "plan.pdf", "application/pdf", []byte("%PDF-1.7 three"), nil)
	if v3.Version != 3 || v3.VersionOf != v1.ID {
		t.Fatalf("v3=%+v", v3)
	}
	if response := restore(v2.ID); response.Code != http.StatusOK {
		t.Fatalf("restore v2 behind v3 status=%d body=%s", response.Code, response.Body.String())
	}
	live = driveRowsNamed(listDriveFiles(t, ajCookies, "").Files, "plan.pdf")
	if len(live) != 1 || live[0].ID != v3.ID {
		t.Fatalf("live rows after restoring behind v3=%+v, want exactly v3", live)
	}
	if metadata := driveFileMetadata(t, v2.ID); !fileEntrySuperseded(metadata) || metadata["supersededBy"] != v3.ID || fileEntryTrashed(metadata) {
		t.Fatalf("v2 restored behind v3 metadata=%v, want superseded by v3", metadata)
	}
	if chain := listDriveFiles(t, ajCookies, "versionsOf="+url.QueryEscape(v2.ID)).Files; driveRowIDs(chain) != strings.Join([]string{v3.ID, v2.ID, v1.ID}, ",") {
		t.Fatalf("chain after restore behind v3=%s", driveRowIDs(chain))
	}
}

// D2 (purge): emptying the trash on a trashed head leaves the prior version
// live and un-superseded; purging a trashed MIDDLE version splices the chain
// so the versions lane still walks end to end.
func TestAssistantFilePurgedTrashedVersionsKeepChainLive(t *testing.T) {
	ajCookies, _ := setupDriveWaveTest(t)
	v1 := uploadDriveFileRow(t, ajCookies, "memo.txt", "text/plain", []byte("memo one"), nil)
	v2 := uploadDriveFileRow(t, ajCookies, "memo.txt", "text/plain", []byte("memo two"), nil)
	if response := deleteDriveFileRequest(t, ajCookies, v2.ID); response.Code != http.StatusOK {
		t.Fatalf("trash v2 status=%d", response.Code)
	}
	empty := postDriveJSON(t, assistantFileEmptyTrashHandler, "/assistant/files/trash/empty", ajCookies, `{}`)
	if empty.Code != http.StatusOK || !strings.Contains(empty.Body.String(), `"purged":1`) {
		t.Fatalf("empty trash status=%d body=%s", empty.Code, empty.Body.String())
	}
	if _, ok := kanbanApp.memory.entryByKindAndID(meetingMemoryKindFile, v2.ID); ok {
		t.Fatal("purged head still in memory")
	}
	live := driveRowsNamed(listDriveFiles(t, ajCookies, "").Files, "memo.txt")
	if len(live) != 1 || live[0].ID != v1.ID || live[0].Superseded {
		t.Fatalf("live rows after purging the head=%+v, want exactly v1 un-superseded", live)
	}
	if chain := listDriveFiles(t, ajCookies, "versionsOf="+url.QueryEscape(v1.ID)).Files; driveRowIDs(chain) != v1.ID {
		t.Fatalf("chain after purge=%s, want v1 alone", driveRowIDs(chain))
	}
	next := uploadDriveFileRow(t, ajCookies, "memo.txt", "text/plain", []byte("memo three"), nil)
	if next.VersionOf != v1.ID || next.Version != 2 {
		t.Fatalf("upload after purge=%+v, want to chain onto v1 as version 2", next)
	}
	if live := driveRowsNamed(listDriveFiles(t, ajCookies, "").Files, "memo.txt"); len(live) != 1 || live[0].ID != next.ID {
		t.Fatalf("live rows after re-upload=%+v", live)
	}

	// Middle-version purge: a three-deep chain whose trashed v2 ages out of
	// the trash keeps v3 linked to v1.
	p1 := uploadDriveFileRow(t, ajCookies, "plan.pdf", "application/pdf", []byte("%PDF-1.7 one"), nil)
	p2 := uploadDriveFileRow(t, ajCookies, "plan.pdf", "application/pdf", []byte("%PDF-1.7 two"), nil)
	p3 := uploadDriveFileRow(t, ajCookies, "plan.pdf", "application/pdf", []byte("%PDF-1.7 three"), nil)
	middle, _ := kanbanApp.memory.entryByKindAndID(meetingMemoryKindFile, p2.ID)
	if _, _, err := kanbanApp.memory.updateEntryWithMetadata(meetingMemoryKindFile, p2.ID, middle.Text, map[string]string{
		"deletedAt": time.Now().UTC().Add(-31 * 24 * time.Hour).Format(time.RFC3339Nano), "deletedBy": "aj@shareability.com", relevanceMetadataKey: relevanceExpired,
	}); err != nil {
		t.Fatal(err)
	}
	if purged := kanbanApp.sweepFileTrashOnce(time.Now().UTC()); purged != 1 {
		t.Fatalf("sweep purged %d, want the aged middle version only", purged)
	}
	if metadata := driveFileMetadata(t, p3.ID); metadata["versionOf"] != p1.ID || metadata["version"] != "3" {
		t.Fatalf("v3 after middle purge=%v, want spliced onto v1 at version 3", metadata)
	}
	if chain := listDriveFiles(t, ajCookies, "versionsOf="+url.QueryEscape(p1.ID)).Files; driveRowIDs(chain) != p3.ID+","+p1.ID {
		t.Fatalf("chain after middle purge=%s, want v3,v1", driveRowIDs(chain))
	}
	if live := driveRowsNamed(listDriveFiles(t, ajCookies, "").Files, "plan.pdf"); len(live) != 1 || live[0].ID != p3.ID {
		t.Fatalf("live rows after middle purge=%+v, want exactly v3", live)
	}
}

// D7: PATCH validates and authorizes every sub-update before applying any.
// The legacy admin may rename another member's upload but never widen or
// narrow its access; a body carrying both fails whole — nothing renamed.
func TestAssistantFilePatchValidatesEverySubUpdateBeforeApplying(t *testing.T) {
	ajCookies, joelCookies := setupDriveWaveTest(t)
	joels := uploadDriveFileRow(t, joelCookies, "joel-notes.txt", "text/plain", []byte("joel body"), nil)

	response := patchDriveFile(t, ajCookies, fmt.Sprintf(`{"id":%q,"name":"renamed-by-admin.txt","visibility":"private"}`, joels.ID))
	if response.Code != http.StatusForbidden {
		t.Fatalf("admin rename+access status=%d body=%s, want 403", response.Code, response.Body.String())
	}
	if metadata := driveFileMetadata(t, joels.ID); metadata["name"] != "joel-notes.txt" || metadata["visibility"] != fileVisibilityCompany {
		t.Fatalf("refused PATCH partially applied: %v", metadata)
	}
	// The admin's rename authority on its own is intact, so the refusal above
	// came from the access half, not from the rename.
	if response := patchDriveFile(t, ajCookies, fmt.Sprintf(`{"id":%q,"name":"renamed-by-admin.txt"}`, joels.ID)); response.Code != http.StatusOK {
		t.Fatalf("admin rename alone status=%d body=%s", response.Code, response.Body.String())
	}
	if metadata := driveFileMetadata(t, joels.ID); metadata["name"] != "renamed-by-admin.txt" {
		t.Fatalf("admin rename did not land: %v", metadata)
	}
	// An invalid grant with a rename: the name must not move either.
	if response := patchDriveFile(t, joelCookies, fmt.Sprintf(`{"id":%q,"name":"never.txt","grants":{"add":["nobody@example.com"]}}`, joels.ID)); response.Code != http.StatusBadRequest {
		t.Fatalf("rename+bad grant status=%d, want 400", response.Code)
	}
	if metadata := driveFileMetadata(t, joels.ID); metadata["name"] != "renamed-by-admin.txt" {
		t.Fatalf("rename landed beside a rejected grant: %v", metadata)
	}
	// The uploader composing rename + access lands both in ONE store rewrite.
	rewrites := 0
	previousProbe := memoryRewriteProbe
	memoryRewriteProbe = func() { rewrites++ }
	t.Cleanup(func() { memoryRewriteProbe = previousProbe })
	response = patchDriveFile(t, joelCookies, fmt.Sprintf(`{"id":%q,"name":"final.txt","visibility":"private"}`, joels.ID))
	if response.Code != http.StatusOK {
		t.Fatalf("uploader rename+access status=%d body=%s", response.Code, response.Body.String())
	}
	if rewrites != 1 {
		t.Fatalf("rename+access rewrote the store %d times, want exactly 1", rewrites)
	}
	if metadata := driveFileMetadata(t, joels.ID); metadata["name"] != "final.txt" || metadata["visibility"] != fileVisibilityPrivate {
		t.Fatalf("combined PATCH metadata=%v", metadata)
	}
	var payload struct {
		File assistantFileRecord `json:"file"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload.File.Name != "final.txt" || payload.File.Visibility != fileVisibilityPrivate {
		t.Fatalf("combined PATCH row=%+v err=%v", payload.File, err)
	}
}

// D5: restore reads through the same seam as every other Drive door — a
// trashed row is its uploader's alone, so the legacy admin restoring another
// member's upload (private or company) gets the non-enumerating 404 the
// trash list already gives.
func TestAssistantFileRestoreRequiresReadableRow(t *testing.T) {
	ajCookies, joelCookies := setupDriveWaveTest(t)
	private := uploadDriveFileRow(t, joelCookies, "joel-private.txt", "text/plain", []byte("joel private"), map[string]string{"visibility": "private"})
	company := uploadDriveFileRow(t, joelCookies, "joel-company.txt", "text/plain", []byte("joel company"), nil)
	for _, id := range []string{private.ID, company.ID} {
		if response := deleteDriveFileRequest(t, joelCookies, id); response.Code != http.StatusOK {
			t.Fatalf("trash %s status=%d", id, response.Code)
		}
	}
	for _, id := range []string{private.ID, company.ID} {
		if response := postDriveJSON(t, assistantFileRestoreHandler, "/assistant/files/restore", ajCookies, fmt.Sprintf(`{"id":%q}`, id)); response.Code != http.StatusNotFound {
			t.Fatalf("admin restore of %s status=%d body=%s, want 404", id, response.Code, response.Body.String())
		}
		if !fileEntryTrashed(driveFileMetadata(t, id)) {
			t.Fatalf("admin restore of %s changed the row", id)
		}
	}
	for _, id := range []string{private.ID, company.ID} {
		if response := postDriveJSON(t, assistantFileRestoreHandler, "/assistant/files/restore", joelCookies, fmt.Sprintf(`{"id":%q}`, id)); response.Code != http.StatusOK {
			t.Fatalf("uploader restore of %s status=%d body=%s", id, response.Code, response.Body.String())
		}
	}
}

// D6: when the prior row's superseded stamp cannot be written, the upload
// fails whole — the just-appended row is removed, the prior stays the single
// live head, and a retry chains normally.
func TestAssistantFileUploadRollsBackWhenVersionStampFails(t *testing.T) {
	ajCookies, _ := setupDriveWaveTest(t)
	v1 := uploadDriveFileRow(t, ajCookies, "chain.txt", "text/plain", []byte("chain one"), nil)

	// The upload appends its row without a rewrite; the first store rewrite
	// of the request IS the superseded stamp. Point the store at a path whose
	// parent is a regular file for that one rewrite so it fails, then restore
	// the real path so the rollback's rewrite can land.
	realPath := kanbanApp.memory.path
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	previousProbe := memoryRewriteProbe
	memoryRewriteProbe = func() {
		calls++
		if calls == 1 {
			kanbanApp.memory.path = filepath.Join(blocker, "memory.jsonl")
		} else {
			kanbanApp.memory.path = realPath
		}
	}
	t.Cleanup(func() {
		memoryRewriteProbe = previousProbe
		kanbanApp.memory.path = realPath
	})
	response := postFileUploadWithFields(t, ajCookies, "chain.txt", "text/plain", []byte("chain two"), nil)
	memoryRewriteProbe = previousProbe
	kanbanApp.memory.path = realPath
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("upload with a failing chain stamp status=%d body=%s, want 500", response.Code, response.Body.String())
	}
	if calls < 2 {
		t.Fatalf("rewrites during the failed upload=%d, want the failed stamp plus the rollback", calls)
	}
	rows := kanbanApp.memory.entriesOfKind(meetingMemoryKindFile, 0)
	if len(rows) != 1 || rows[0].ID != v1.ID || fileEntrySuperseded(rows[0].Metadata) {
		t.Fatalf("rows after rollback=%d (first=%s superseded=%v), want v1 alone and live", len(rows), rows[0].ID, fileEntrySuperseded(rows[0].Metadata))
	}
	if live := driveRowsNamed(listDriveFiles(t, ajCookies, "").Files, "chain.txt"); len(live) != 1 || live[0].ID != v1.ID {
		t.Fatalf("live rows after rollback=%+v", live)
	}
	v2 := uploadDriveFileRow(t, ajCookies, "chain.txt", "text/plain", []byte("chain two"), nil)
	if v2.VersionOf != v1.ID || v2.Version != 2 {
		t.Fatalf("retry after rollback=%+v, want a clean v2", v2)
	}
}

// M12: the store-level metadata lookup returns only the rows behind a ref, as
// clones, under the read lock — and the blob route resolves through it.
func TestDriveStoreEntriesOfKindByMetadataClones(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	shared, err := putBlob([]byte("bytes two rows share"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	other, err := putBlob([]byte("bytes one row holds"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	for id, ref := range map[string]string{"file-shared-a": shared, "file-shared-b": shared, "file-other": other} {
		if _, _, err := app.memory.appendEntry(meetingMemoryKindFile, id, "File "+id+" uploaded.", map[string]string{
			"name": id + ".txt", "blobRef": ref, "mime": "text/plain", "uploaderEmail": "aj@shareability.com", "visibility": fileVisibilityCompany,
		}); err != nil {
			t.Fatal(err)
		}
	}
	rows := app.memory.entriesOfKindByMetadata(meetingMemoryKindFile, "blobRef", shared)
	if len(rows) != 2 || rows[0].ID != "file-shared-a" || rows[1].ID != "file-shared-b" {
		ids := make([]string, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		t.Fatalf("lookup rows=%v, want the two rows behind the shared ref in store order", ids)
	}
	rows[0].Metadata["name"] = "mutated.txt"
	if stored, _ := app.memory.entryByKindAndID(meetingMemoryKindFile, "file-shared-a"); stored.Metadata["name"] != "file-shared-a.txt" {
		t.Fatal("lookup returned a live row, not a clone")
	}
	if rows := app.memory.entriesOfKindByMetadata(meetingMemoryKindFile, "blobRef", strings.Repeat("0", 64)); len(rows) != 0 {
		t.Fatalf("unknown ref matched %d rows", len(rows))
	}
	if rows := app.memory.entriesOfKindByMetadata(meetingMemoryKindOSArtifact, "blobRef", shared); len(rows) != 0 {
		t.Fatalf("kind filter ignored: %d rows", len(rows))
	}
	aj := accountStore().findUser("aj@shareability.com")
	if !blobAuthorized(context.Background(), aj, shared) || !blobAuthorized(context.Background(), aj, other) {
		t.Fatal("blob route no longer resolves Drive rows through the metadata lookup")
	}
}

// D10: emptying the trash walks the Drive once and removes every purged row
// in a single store rewrite.
func TestAssistantFileEmptyTrashRewritesStoreOnce(t *testing.T) {
	ajCookies, _ := setupDriveWaveTest(t)
	var ids []string
	for index := 0; index < 3; index++ {
		row := uploadDriveFileRow(t, ajCookies, fmt.Sprintf("purge-%d.bin", index), "application/octet-stream", []byte(strings.Repeat("p", 10+index)), nil)
		ids = append(ids, row.ID)
	}
	for _, id := range ids {
		if response := deleteDriveFileRequest(t, ajCookies, id); response.Code != http.StatusOK {
			t.Fatalf("trash %s status=%d", id, response.Code)
		}
	}
	rewrites := 0
	previousProbe := memoryRewriteProbe
	memoryRewriteProbe = func() { rewrites++ }
	t.Cleanup(func() { memoryRewriteProbe = previousProbe })
	empty := postDriveJSON(t, assistantFileEmptyTrashHandler, "/assistant/files/trash/empty", ajCookies, `{}`)
	memoryRewriteProbe = previousProbe
	if empty.Code != http.StatusOK {
		t.Fatalf("empty trash status=%d body=%s", empty.Code, empty.Body.String())
	}
	var receipt struct {
		Purged     int   `json:"purged"`
		FreedBytes int64 `json:"freedBytes"`
	}
	if err := json.Unmarshal(empty.Body.Bytes(), &receipt); err != nil || receipt.Purged != 3 || receipt.FreedBytes != 10+11+12 {
		t.Fatalf("receipt=%+v err=%v, want 3 purged freeing 33 bytes", receipt, err)
	}
	if rewrites != 1 {
		t.Fatalf("empty trash rewrote the store %d times, want exactly 1", rewrites)
	}
	for _, id := range ids {
		if _, ok := kanbanApp.memory.entryByKindAndID(meetingMemoryKindFile, id); ok {
			t.Fatalf("purged row %s still in memory", id)
		}
	}
}

// D8/D11: a file share link binds the exact blob it was minted on, so it keeps
// serving version 1's bytes after version 2 supersedes the row; the versions
// lane (one Drive walk) reports the live link count per version so the
// minter can see that.
func TestShareLinkVersionsLaneReportsLiveLinkCounts(t *testing.T) {
	ajCookies, _ := setupDriveWaveTest(t)
	v1 := uploadDriveFileRow(t, ajCookies, "pitch.txt", "text/plain", []byte("pitch version one"), nil)
	link := mintFileShareLinkForTest(t, v1.ID, ajCookies)
	v2 := uploadDriveFileRow(t, ajCookies, "pitch.txt", "text/plain", []byte("pitch version two"), nil)
	if v2.VersionOf != v1.ID {
		t.Fatalf("v2=%+v", v2)
	}
	versions := listDriveFiles(t, ajCookies, "versionsOf="+url.QueryEscape(v1.ID)).Files
	if driveRowIDs(versions) != v2.ID+","+v1.ID {
		t.Fatalf("versions=%s", driveRowIDs(versions))
	}
	if versions[0].ShareLinkCount != 0 || versions[1].ShareLinkCount != 1 || !versions[1].Superseded {
		t.Fatalf("versions=%+v, want v1 superseded with one live link and v2 with none", versions)
	}
	if _, visible := driveRowByID(listDriveFiles(t, ajCookies, "").Files, v1.ID); visible {
		t.Fatal("superseded v1 leaked into the live list")
	}
	opened := shareLinkRequest(t, http.MethodGet, fmt.Sprint(link["url"]), "", nil)
	if opened.Code != http.StatusOK || opened.Body.String() != "pitch version one" {
		t.Fatalf("link minted on v1 status=%d body=%q, want v1's bytes", opened.Code, opened.Body.String())
	}
	if _, plain := driveRowByID(listDriveFiles(t, ajCookies, "").Files, v2.ID); !plain {
		t.Fatal("live head missing from the default list")
	}
}

// B4: a "file" event carrying a decorated row is re-projected per recipient —
// the uploader's socket gets the row, another member's socket gets only a
// {kind, id} tombstone (no name, uploader or download URL) for a private
// upload, and everyone gets the row for a company upload.
func TestAssistantFileBroadcastProjectsRowPerRecipient(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	ajConn := dialIsolatedWebsocket(t, server, "aj@shareability.com")
	timConn := dialIsolatedWebsocket(t, server, "tim@shareability.com")
	sendOfficeHello(t, ajConn)
	sendOfficeHello(t, timConn)
	waitForKanbanEvent(t, ajConn, "codex_proposals", 5*time.Second)
	waitForKanbanEvent(t, timConn, "codex_proposals", 5*time.Second)

	ref, err := putBlob([]byte("private drive bytes"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	private, _, err := kanbanApp.memory.appendEntry(meetingMemoryKindFile, "file-private-broadcast", "File secret-plan.txt uploaded by AJ.", map[string]string{
		"name": "secret-plan.txt", "blobRef": ref, "mime": "text/plain", "size": "19", "uploaderEmail": "aj@shareability.com", "uploaderName": "AJ",
		"origin": "files", "brainStatus": fileBrainStatusStored, "visibility": fileVisibilityPrivate, "ownerEmail": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	broadcastSignedInKanbanEvent("file", fileRecordFromEntry(private))

	ajRaw := string(waitForKanbanEvent(t, ajConn, "file", 5*time.Second))
	if !strings.Contains(ajRaw, "secret-plan.txt") || !strings.Contains(ajRaw, "downloadUrl") {
		t.Fatalf("uploader socket payload=%s, want the full row", ajRaw)
	}
	timRaw := string(waitForKanbanEvent(t, timConn, "file", 5*time.Second))
	if strings.Contains(timRaw, "secret-plan.txt") || strings.Contains(timRaw, "downloadUrl") || strings.Contains(timRaw, "uploaderEmail") {
		t.Fatalf("non-reader socket payload=%s, want a bare tombstone", timRaw)
	}
	if !strings.Contains(timRaw, private.ID) {
		t.Fatalf("non-reader tombstone=%s, want the row id for the client refetch", timRaw)
	}

	company, _, err := kanbanApp.memory.appendEntry(meetingMemoryKindFile, "file-company-broadcast", "File offsite.txt uploaded by AJ.", map[string]string{
		"name": "offsite-notes.txt", "blobRef": ref, "mime": "text/plain", "size": "19", "uploaderEmail": "aj@shareability.com", "uploaderName": "AJ",
		"origin": "files", "brainStatus": fileBrainStatusStored, "visibility": fileVisibilityCompany, "ownerEmail": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	broadcastSignedInKanbanEvent("file", map[string]any{"kind": "renamed", "file": fileRecordFromEntry(company)})
	if raw := string(waitForKanbanEvent(t, timConn, "file", 5*time.Second)); !strings.Contains(raw, "offsite-notes.txt") || !strings.Contains(raw, `"kind":"renamed"`) {
		t.Fatalf("member socket company payload=%s, want the full renamed row", raw)
	}
	if raw := string(waitForKanbanEvent(t, ajConn, "file", 5*time.Second)); !strings.Contains(raw, "offsite-notes.txt") {
		t.Fatalf("uploader socket company payload=%s", raw)
	}
}
