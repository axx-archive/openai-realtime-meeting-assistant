package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

type studioVersionListPayload struct {
	OK             bool                `json:"ok"`
	ArtifactID     string              `json:"artifactId"`
	CurrentVersion int                 `json:"currentVersion"`
	Current        studioVersionView   `json:"current"`
	Versions       []studioVersionView `json:"versions"`
}

func patchDocumentMarkdown(t *testing.T, cookies []*http.Cookie, artifactID string, version int, markdown string, restoredFrom int) documentStudioArtifactView {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"artifactId": artifactID, "expectedVersion": version, "restoredFrom": restoredFrom,
		"document": map[string]any{"schemaVersion": 1, "markdown": markdown},
	})
	response := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/document", string(body), cookies, documentEditorHandler)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH v%d status=%d body=%s", version, response.Code, response.Body.String())
	}
	var saved struct {
		Artifact documentStudioArtifactView `json:"artifact"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	return saved.Artifact
}

func TestDocumentStudioVersionsListReopenAndRestore(t *testing.T) {
	cookies, artifact := setupDocumentEditorHTTPTest(t)
	bodies := []string{"# Field notes\n\nSecond body.", "# Field notes\n\nThird body.", "# Field notes\n\nFourth body."}
	version := artifactVersion(artifact)
	for _, markdown := range bodies {
		saved := patchDocumentMarkdown(t, cookies, artifact.ID, version, markdown, 0)
		if saved.Version != version+1 {
			t.Fatalf("PATCH minted version %d, want %d", saved.Version, version+1)
		}
		version = saved.Version
	}

	list := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/document/versions?id="+artifact.ID, "", cookies, documentEditorVersionsHandler)
	if list.Code != http.StatusOK {
		t.Fatalf("versions status=%d body=%s", list.Code, list.Body.String())
	}
	var payload studioVersionListPayload
	if err := json.Unmarshal(list.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || payload.ArtifactID != artifact.ID || payload.CurrentVersion != 4 || !payload.Current.Current || payload.Current.Version != 4 {
		t.Fatalf("versions envelope=%s", list.Body.String())
	}
	if len(payload.Versions) != 3 {
		t.Fatalf("three PATCHes should journal three superseded versions, got %d: %s", len(payload.Versions), list.Body.String())
	}
	for index, want := range []int{3, 2, 1} {
		row := payload.Versions[index]
		if row.Version != want || !row.Recoverable || row.Size < 1 || row.Digest == "" || row.At == "" || row.Source != "edit" || row.Current {
			t.Fatalf("versions[%d]=%+v want newest-first recoverable v%d", index, row, want)
		}
	}

	second := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/document?id="+artifact.ID+"&version=2", "", cookies, documentEditorHandler)
	if second.Code != http.StatusOK {
		t.Fatalf("version=2 status=%d body=%s", second.Code, second.Body.String())
	}
	var reopened struct {
		Document documentStudioDocument `json:"document"`
		Version  studioVersionView      `json:"version"`
		ReadOnly bool                   `json:"readOnly"`
		CanWrite bool                   `json:"canWrite"`
		Artifact documentStudioArtifactView
	}
	if err := json.Unmarshal(second.Body.Bytes(), &reopened); err != nil {
		t.Fatal(err)
	}
	if reopened.Document.Markdown != bodies[0] || reopened.Version.Version != 2 || !reopened.ReadOnly || reopened.CanWrite {
		t.Fatalf("version=2 payload=%s", second.Body.String())
	}
	first := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/document?id="+artifact.ID+"&version=1", "", cookies, documentEditorHandler)
	if first.Code != http.StatusOK || !json.Valid(first.Body.Bytes()) {
		t.Fatalf("version=1 status=%d body=%s", first.Code, first.Body.String())
	}
	var original struct {
		Document documentStudioDocument `json:"document"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &original)
	if original.Document.Markdown != artifact.Text {
		t.Fatalf("version=1 body=%q want the original %q", original.Document.Markdown, artifact.Text)
	}
	current := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/document?id="+artifact.ID+"&version=4", "", cookies, documentEditorHandler)
	if current.Code != http.StatusOK {
		t.Fatalf("version=current status=%d body=%s", current.Code, current.Body.String())
	}
	for _, unknown := range []string{"9", "0", "abc"} {
		response := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/document?id="+artifact.ID+"&version="+unknown, "", cookies, documentEditorHandler)
		if response.Code != http.StatusNotFound && response.Code != http.StatusBadRequest {
			t.Fatalf("version=%s status=%d body=%s, want 404/400", unknown, response.Code, response.Body.String())
		}
		if unknown == "9" && response.Code != http.StatusNotFound {
			t.Fatalf("unknown version status=%d want 404", response.Code)
		}
	}

	// Restore = PATCH the old body as a new version, stamped with its origin.
	restored := patchDocumentMarkdown(t, cookies, artifact.ID, 4, bodies[0], 2)
	if restored.Version != 5 {
		t.Fatalf("restore minted version %d, want 5", restored.Version)
	}
	stored, _ := kanbanApp.osArtifactByID(artifact.ID)
	if stored.Metadata[artifactRestoredFromMetadataKey] != "2" || stored.Text != bodies[0] {
		t.Fatalf("restore did not stamp restoredFrom: metadata=%v text=%q", stored.Metadata, stored.Text)
	}
	list = artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/document/versions?id="+artifact.ID, "", cookies, documentEditorVersionsHandler)
	payload = studioVersionListPayload{}
	if err := json.Unmarshal(list.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Current.Source != "restore" || payload.Current.RestoredFrom != 2 || len(payload.Versions) != 4 {
		t.Fatalf("post-restore envelope=%s", list.Body.String())
	}
	// A plain save clears the stamp; the superseded restore keeps it in the journal.
	plain := patchDocumentMarkdown(t, cookies, artifact.ID, 5, "# Field notes\n\nSixth body.", 0)
	list = artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/document/versions?id="+artifact.ID, "", cookies, documentEditorVersionsHandler)
	payload = studioVersionListPayload{}
	_ = json.Unmarshal(list.Body.Bytes(), &payload)
	if plain.Version != 6 || payload.Current.Source != "edit" || payload.Current.RestoredFrom != 0 || payload.Versions[0].Version != 5 || payload.Versions[0].Source != "restore" || payload.Versions[0].RestoredFrom != 2 {
		t.Fatalf("journal lost the restore origin: %s", list.Body.String())
	}

	// restoredFrom must name a real prior version.
	badBody, _ := json.Marshal(map[string]any{
		"artifactId": artifact.ID, "expectedVersion": 6, "restoredFrom": 9,
		"document": map[string]any{"schemaVersion": 1, "markdown": "# nope"},
	})
	bad := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/document", string(badBody), cookies, documentEditorHandler)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("restoredFrom=9 on v6 status=%d body=%s want 400", bad.Code, bad.Body.String())
	}
}

func TestDocumentStudioVersionsHonorPrivateACL(t *testing.T) {
	ownerCookies, _ := setupDocumentEditorHTTPTest(t)
	nonOwnerCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	private, _, err := kanbanApp.createOSArtifactWithMetadata("artifacts", "Private notes", "# Private", "AJ", map[string]string{
		"type": artifactTypeMarkdown, "visibility": "private", "requestedBy": "aj@shareability.com", "ownerEmail": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	patchDocumentMarkdown(t, ownerCookies, private.ID, 1, "# Private v2", 0)
	probes := []struct {
		target  string
		handler http.HandlerFunc
	}{
		{"/artifacts/document/versions?id=" + private.ID, documentEditorVersionsHandler},
		{"/artifacts/document?id=" + private.ID + "&version=1", documentEditorHandler},
	}
	for _, probe := range probes {
		target, handler := probe.target, probe.handler
		owner := artifactAuthorizationRequest(t, http.MethodGet, target, "", ownerCookies, handler)
		if owner.Code != http.StatusOK {
			t.Fatalf("owner %s status=%d body=%s", target, owner.Code, owner.Body.String())
		}
		denied := artifactAuthorizationRequest(t, http.MethodGet, target, "", nonOwnerCookies, handler)
		if denied.Code != http.StatusNotFound {
			t.Fatalf("non-owner %s status=%d body=%s want 404", target, denied.Code, denied.Body.String())
		}
		anonymous := artifactAuthorizationRequest(t, http.MethodGet, target, "", nil, handler)
		if anonymous.Code != http.StatusUnauthorized {
			t.Fatalf("signed-out %s status=%d want 401", target, anonymous.Code)
		}
	}
}

func TestDeckStudioVersionsReopenPriorScene(t *testing.T) {
	cookies, artifact := setupDeckEditorHTTPTest(t, LegacyCompatibleObjectAuthorizer{})
	get := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/deck?id="+artifact.ID, "", cookies, deckEditorHandler)
	var loaded struct {
		Deck deckDocument `json:"deck"`
	}
	if get.Code != http.StatusOK || json.Unmarshal(get.Body.Bytes(), &loaded) != nil {
		t.Fatalf("GET status=%d body=%s", get.Code, get.Body.String())
	}
	version := artifactVersion(artifact)
	texts := []string{"Saved once", "Saved twice"}
	for _, text := range texts {
		loaded.Deck.Slides[0].Elements[0].Text = text
		body, _ := json.Marshal(map[string]any{"artifactId": artifact.ID, "expectedVersion": version, "deck": loaded.Deck})
		patch := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/deck", string(body), cookies, deckEditorHandler)
		var saved struct {
			Artifact deckArtifactView `json:"artifact"`
		}
		if patch.Code != http.StatusOK || json.Unmarshal(patch.Body.Bytes(), &saved) != nil || saved.Artifact.Version != version+1 {
			t.Fatalf("PATCH status=%d body=%s", patch.Code, patch.Body.String())
		}
		version = saved.Artifact.Version
	}
	list := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/deck/versions?id="+artifact.ID, "", cookies, deckEditorVersionsHandler)
	var payload studioVersionListPayload
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &payload) != nil || len(payload.Versions) != 2 || payload.CurrentVersion != 3 || payload.Versions[0].Version != 2 {
		t.Fatalf("deck versions status=%d body=%s", list.Code, list.Body.String())
	}
	stored, _ := kanbanApp.osArtifactByID(artifact.ID)
	records := artifactVersionHistory(stored)
	if len(records) != 2 || !validBlobRef(records[1].SceneRef) || records[0].SceneRef != "" {
		t.Fatalf("journal should carry the native scene ref for v2 only: %+v", records)
	}

	second := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/deck?id="+artifact.ID+"&version=2", "", cookies, deckEditorHandler)
	var reopened struct {
		Deck          deckDocument      `json:"deck"`
		Version       studioVersionView `json:"version"`
		ImportQuality string            `json:"importQuality"`
		ReadOnly      bool              `json:"readOnly"`
		CanWrite      bool              `json:"canWrite"`
	}
	if second.Code != http.StatusOK || json.Unmarshal(second.Body.Bytes(), &reopened) != nil {
		t.Fatalf("version=2 status=%d body=%s", second.Code, second.Body.String())
	}
	if reopened.Deck.Slides[0].Elements[0].Text != texts[0] || reopened.ImportQuality != "native" || reopened.Version.Version != 2 || !reopened.ReadOnly || reopened.CanWrite || reopened.Deck.Theme.ID != "graphite" {
		t.Fatalf("version=2 payload=%s", second.Body.String())
	}
	// v1 predates any native scene: it reopens from the journaled HTML body,
	// honestly labeled through importQuality.
	first := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/deck?id="+artifact.ID+"&version=1", "", cookies, deckEditorHandler)
	reopened.Deck = deckDocument{}
	if first.Code != http.StatusOK || json.Unmarshal(first.Body.Bytes(), &reopened) != nil || reopened.Deck.Slides[0].Elements[0].Text != "Like a Farmer" || reopened.ImportQuality != "faithful" {
		t.Fatalf("version=1 status=%d body=%s", first.Code, first.Body.String())
	}
	unknown := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/deck?id="+artifact.ID+"&version=7", "", cookies, deckEditorHandler)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown deck version status=%d body=%s", unknown.Code, unknown.Body.String())
	}

	// Restore stamps the deck revision too.
	loaded.Deck.Slides[0].Elements[0].Text = texts[0]
	body, _ := json.Marshal(map[string]any{"artifactId": artifact.ID, "expectedVersion": version, "restoredFrom": 2, "deck": loaded.Deck})
	restore := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/deck", string(body), cookies, deckEditorHandler)
	if restore.Code != http.StatusOK {
		t.Fatalf("deck restore status=%d body=%s", restore.Code, restore.Body.String())
	}
	stored, _ = kanbanApp.osArtifactByID(artifact.ID)
	if stored.Metadata[artifactRestoredFromMetadataKey] != strconv.Itoa(2) {
		t.Fatalf("deck restore did not stamp restoredFrom: %v", stored.Metadata)
	}
}
