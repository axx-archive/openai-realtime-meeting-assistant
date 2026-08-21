package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func setupDocumentEditorHTTPTest(t *testing.T) ([]*http.Cookie, meetingMemoryEntry) {
	t.Helper()
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() {
		kanbanApp = previousApp
		artifactObjectAuthorizer = previousAuthorizer
	})
	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("artifacts", "Field notes", "# Field notes\n\nOriginal paragraph.", "AJ", map[string]string{
		"type": artifactTypeMarkdown, "visibility": "organization", "requestedBy": "aj@shareability.com",
	})
	if err != nil {
		t.Fatalf("create document artifact: %v", err)
	}
	return loginAs(t, "aj@shareability.com", "B0NFIRE!"), artifact
}

func TestDocumentStudioGETPatchAndConflict(t *testing.T) {
	cookies, artifact := setupDocumentEditorHTTPTest(t)
	get := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/document?id="+artifact.ID, "", cookies, documentEditorHandler)
	if get.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", get.Code, get.Body.String())
	}
	var loaded struct {
		Artifact documentStudioArtifactView `json:"artifact"`
		Document documentStudioDocument     `json:"document"`
		CanWrite bool                       `json:"canWrite"`
	}
	if json.Unmarshal(get.Body.Bytes(), &loaded) != nil || !loaded.CanWrite || loaded.Document.Markdown != artifact.Text {
		t.Fatalf("GET response=%s", get.Body.String())
	}
	body, _ := json.Marshal(map[string]any{
		"artifactId": artifact.ID, "expectedVersion": loaded.Artifact.Version, "title": "Field notes revised",
		"document": map[string]any{"schemaVersion": 1, "markdown": "# Field notes\n\nA sharper paragraph."},
	})
	patch := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/document", string(body), cookies, documentEditorHandler)
	if patch.Code != http.StatusOK || !strings.Contains(patch.Body.String(), "A sharper paragraph") {
		t.Fatalf("PATCH status=%d body=%s", patch.Code, patch.Body.String())
	}
	stale := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/document", string(body), cookies, documentEditorHandler)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "reload or save a copy") {
		t.Fatalf("stale PATCH status=%d body=%s", stale.Code, stale.Body.String())
	}
}

func TestDocumentStudioSaveCopyCreatesIndependentFile(t *testing.T) {
	cookies, artifact := setupDocumentEditorHTTPTest(t)
	body, _ := json.Marshal(map[string]any{
		"artifactId": artifact.ID, "expectedVersion": artifactVersion(artifact), "title": "Field notes copy",
		"fileName": "Field notes copy", "folderId": "",
		"document": map[string]any{"schemaVersion": 1, "markdown": "# Copy\n\nIndependent body."},
	})
	response := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/copies", string(body), cookies, documentEditorCopyHandler)
	if response.Code != http.StatusCreated {
		t.Fatalf("copy status=%d body=%s", response.Code, response.Body.String())
	}
	var copied struct {
		Artifact documentStudioArtifactView `json:"artifact"`
		File     assistantFileRecord        `json:"file"`
	}
	if json.Unmarshal(response.Body.Bytes(), &copied) != nil || copied.Artifact.ID == artifact.ID || copied.File.ID != copied.Artifact.ID || !copied.Artifact.SavedToFiles {
		t.Fatalf("copy response=%s", response.Body.String())
	}
	original, _ := kanbanApp.osArtifactByID(artifact.ID)
	if strings.Contains(original.Text, "Independent body") || original.Metadata["savedToFiles"] == "true" {
		t.Fatalf("copy mutated original: %+v", original)
	}
}

func TestDocumentStudioRejectsBinaryArtifactsAndInvalidText(t *testing.T) {
	cookies, artifact := setupDocumentEditorHTTPTest(t)
	binary := artifact
	binary.ID = "document-studio-pdf"
	binary.Metadata = cloneStringMap(artifact.Metadata)
	binary.Metadata["type"] = artifactTypePDF
	kanbanApp.memory.mu.Lock()
	kanbanApp.memory.entries = append(kanbanApp.memory.entries, binary)
	kanbanApp.memory.mu.Unlock()
	get := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/document?id="+binary.ID, "", cookies, documentEditorHandler)
	if get.Code != http.StatusNotFound {
		t.Fatalf("binary GET status=%d body=%s", get.Code, get.Body.String())
	}
	body, _ := json.Marshal(map[string]any{
		"artifactId": artifact.ID, "expectedVersion": artifactVersion(artifact), "title": "Invalid",
		"document": map[string]any{"schemaVersion": 1, "markdown": "unsafe\u0000text"},
	})
	patch := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/document", string(body), cookies, documentEditorHandler)
	if patch.Code != http.StatusBadRequest {
		t.Fatalf("invalid text PATCH status=%d body=%s", patch.Code, patch.Body.String())
	}
}
