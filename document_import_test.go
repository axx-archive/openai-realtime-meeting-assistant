package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

func seedDriveFile(t *testing.T, name string, mime string, data []byte, uploader string) meetingMemoryEntry {
	t.Helper()
	ref, err := putBlob(data, mime)
	if err != nil {
		t.Fatal(err)
	}
	entry, _, err := kanbanApp.memory.appendEntry(meetingMemoryKindFile, "file-"+name, "File "+name+" uploaded by "+uploader+".", map[string]string{
		"name": name, "blobRef": ref, "mime": mime, "size": strconv.Itoa(len(data)),
		"uploaderEmail": uploader, "uploaderName": uploader, "origin": "files", "brainStatus": fileBrainStatusStored,
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestDocumentStudioImportOpensDriveMarkdownAsEditableDocument(t *testing.T) {
	cookies, _ := setupDocumentEditorHTTPTest(t)
	body := "# Imported\r\n\r\nHello **world**.\r\n"
	file := seedDriveFile(t, "field_notes.md", "text/markdown", []byte(body), "aj@shareability.com")
	response := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/import", `{"fileId":"`+file.ID+`"}`, cookies, documentEditorImportHandler)
	if response.Code != http.StatusCreated {
		t.Fatalf("import status=%d body=%s", response.Code, response.Body.String())
	}
	var created struct {
		OK       bool                       `json:"ok"`
		ID       string                     `json:"id"`
		Artifact documentStudioArtifactView `json:"artifact"`
		Document documentStudioDocument     `json:"document"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	// The store trims trailing whitespace on every body (normalizeMemoryEntryText).
	if !created.OK || created.ID == "" || created.ID != created.Artifact.ID || created.Artifact.Title != "field notes" || created.Document.Markdown != "# Imported\n\nHello **world**." {
		t.Fatalf("import payload=%s", response.Body.String())
	}
	stored, found := kanbanApp.osArtifactByID(created.ID)
	if !found || stored.Metadata["source"] != studioBlankSourceDocument || stored.Metadata["importedFromFileId"] != file.ID || stored.Metadata["ownerEmail"] != "aj@shareability.com" {
		t.Fatalf("imported artifact metadata=%v", stored.Metadata)
	}
	if kind, ok := studioLegacyProjectCandidate(stored); !ok || kind != studioProjectKindDocument {
		t.Fatalf("imported document not classified as a document project: kind=%q ok=%v", kind, ok)
	}
	get := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/document?id="+created.ID, "", cookies, documentEditorHandler)
	var loaded struct {
		CanWrite bool                   `json:"canWrite"`
		Document documentStudioDocument `json:"document"`
	}
	if get.Code != http.StatusOK || json.Unmarshal(get.Body.Bytes(), &loaded) != nil || !loaded.CanWrite || loaded.Document.Markdown != created.Document.Markdown {
		t.Fatalf("imported document GET status=%d body=%s", get.Code, get.Body.String())
	}
	patchDocumentMarkdown(t, cookies, created.ID, created.Artifact.Version, "# Imported\n\nEdited after import.", 0)
	original, _ := kanbanApp.memory.entryByKindAndID(meetingMemoryKindFile, file.ID)
	if original.Metadata["blobRef"] != file.Metadata["blobRef"] {
		t.Fatal("import must not touch the source Drive file")
	}

	// Plain text with an explicit title works too.
	text := seedDriveFile(t, "notes.txt", "text/plain", []byte("just text\n"), "aj@shareability.com")
	titled := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/import", `{"fileId":"`+text.ID+`","title":"Renamed on import"}`, cookies, documentEditorImportHandler)
	created.Artifact = documentStudioArtifactView{}
	if titled.Code != http.StatusCreated || json.Unmarshal(titled.Body.Bytes(), &created) != nil || created.Artifact.Title != "Renamed on import" || created.Document.Markdown != "just text" {
		t.Fatalf("txt import status=%d body=%s", titled.Code, titled.Body.String())
	}
}

func TestDocumentStudioImportRejectsNonTextAndUnauthorizedFiles(t *testing.T) {
	cookies, _ := setupDocumentEditorHTTPTest(t)
	png := seedDriveFile(t, "photo.png", "image/png", tinyPNG(t), "aj@shareability.com")
	if response := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/import", `{"fileId":"`+png.ID+`"}`, cookies, documentEditorImportHandler); response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("png import status=%d body=%s want 415", response.Code, response.Body.String())
	}
	if response := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/import", `{"fileId":"file-does-not-exist"}`, cookies, documentEditorImportHandler); response.Code != http.StatusNotFound {
		t.Fatalf("missing file import status=%d body=%s want 404", response.Code, response.Body.String())
	}
	if response := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/import", `{}`, cookies, documentEditorImportHandler); response.Code != http.StatusBadRequest {
		t.Fatalf("empty import status=%d want 400", response.Code)
	}
	// A deliverable the caller cannot read is a file they cannot import.
	private, _, err := kanbanApp.createOSArtifactWithMetadata("artifacts", "Private notes", "# Private", "AJ", map[string]string{
		"type": artifactTypeMarkdown, "visibility": "private", "requestedBy": "aj@shareability.com", "ownerEmail": "aj@shareability.com", "savedToFiles": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	nonOwner := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	if response := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/import", `{"fileId":"`+private.ID+`"}`, nonOwner, documentEditorImportHandler); response.Code != http.StatusNotFound || len(response.Body.String()) > 200 {
		t.Fatalf("non-owner import status=%d body=%s want non-oracular 404", response.Code, response.Body.String())
	}
	if response := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/import", `{"fileId":"`+png.ID+`"}`, nil, documentEditorImportHandler); response.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out import status=%d want 401", response.Code)
	}
}
