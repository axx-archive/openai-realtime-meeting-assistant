package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strconv"
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

func postDocumentImageUpload(t *testing.T, cookies []*http.Cookie, artifactID string, version int, name, mime string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	formBody, formType := multipartDocumentImageBody(t, artifactID, version, name, mime, data)
	request := httptest.NewRequest(http.MethodPost, "/artifacts/document/images", formBody)
	request.Header.Set("Content-Type", formType)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	documentEditorImageUploadHandler(recorder, request)
	return recorder
}

func multipartDocumentImageBody(t *testing.T, artifactID string, version int, name, mime string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("artifactId", artifactID)
	_ = writer.WriteField("expectedVersion", strconv.Itoa(version))
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, name))
	header.Set("Content-Type", mime)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body, writer.FormDataContentType()
}

func TestDocumentStudioImageUploadBindsExactRevisionAndPDFAsset(t *testing.T) {
	cookies, artifact := setupDocumentEditorHTTPTest(t)
	response := postDocumentImageUpload(t, cookies, artifact.ID, artifactVersion(artifact), "field-notes.png", "image/png", tinyPNG(t))
	if response.Code != http.StatusCreated {
		t.Fatalf("image upload status=%d body=%s", response.Code, response.Body.String())
	}
	var uploaded struct {
		Artifact documentStudioArtifactView `json:"artifact"`
		Image    struct {
			Ref  string `json:"ref"`
			Mime string `json:"mime"`
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"image"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &uploaded); err != nil || uploaded.Artifact.Version <= artifactVersion(artifact) || !validBlobRef(uploaded.Image.Ref) || uploaded.Image.Mime != "image/png" || !strings.HasPrefix(uploaded.Image.URL, "/artifacts/blob?") {
		t.Fatalf("image upload response=%s", response.Body.String())
	}
	stored, ok := kanbanApp.osArtifactByID(artifact.ID)
	assets := artifactAssets(stored)
	if !ok || len(assets) != 1 || assets[0].Ref != uploaded.Image.Ref || assets[0].Kind != "image" || stored.Text != artifact.Text {
		t.Fatalf("image upload did not preserve body and bind one asset: %+v assets=%+v", stored, assets)
	}
	if html := newReportPrintRenderer(stored).imageHTML("Field notes", uploaded.Image.URL); !strings.Contains(html, "data:image/png;base64,") {
		t.Fatalf("attached document image did not become a local PDF image: %s", html)
	}
	stale := postDocumentImageUpload(t, cookies, artifact.ID, artifactVersion(artifact), "stale.png", "image/png", tinyPNG(t))
	afterStale, _ := kanbanApp.osArtifactByID(artifact.ID)
	if stale.Code != http.StatusConflict || len(artifactAssets(afterStale)) != 1 {
		t.Fatalf("stale image upload status=%d body=%s", stale.Code, stale.Body.String())
	}
	invalid := postDocumentImageUpload(t, cookies, artifact.ID, uploaded.Artifact.Version, "script.svg", "image/svg+xml", []byte(`<svg><script>alert(1)</script></svg>`))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("unsafe image upload status=%d body=%s", invalid.Code, invalid.Body.String())
	}
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
	if json.Unmarshal(get.Body.Bytes(), &loaded) != nil || !loaded.CanWrite || loaded.Document.Markdown != artifact.Text || loaded.Artifact.ContentDigest != artifactCapabilityDigest(artifact) {
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
	var saved struct {
		Receipt struct {
			Outcome      string `json:"outcome"`
			ContentSaved bool   `json:"contentSaved"`
			SavedToFiles bool   `json:"savedToFiles"`
		} `json:"receipt"`
	}
	if err := json.Unmarshal(patch.Body.Bytes(), &saved); err != nil || saved.Receipt.Outcome != "document_saved" || !saved.Receipt.ContentSaved || saved.Receipt.SavedToFiles {
		t.Fatalf("PATCH receipt=%s", patch.Body.String())
	}
	stale := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/document", string(body), cookies, documentEditorHandler)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "reload or save a copy") {
		t.Fatalf("stale PATCH status=%d body=%s", stale.Code, stale.Body.String())
	}
}

func TestDocumentStudioEmptyBodyRoundTripsAndCanBeRepopulated(t *testing.T) {
	cookies, artifact := setupDocumentEditorHTTPTest(t)
	emptyBody, _ := json.Marshal(map[string]any{
		"artifactId": artifact.ID, "expectedVersion": artifactVersion(artifact), "title": "Empty field notes",
		"document": map[string]any{"schemaVersion": 1, "markdown": ""},
	})
	empty := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/document", string(emptyBody), cookies, documentEditorHandler)
	if empty.Code != http.StatusOK {
		t.Fatalf("empty PATCH status=%d body=%s", empty.Code, empty.Body.String())
	}
	var emptied struct {
		Artifact documentStudioArtifactView `json:"artifact"`
		Document documentStudioDocument     `json:"document"`
	}
	if err := json.Unmarshal(empty.Body.Bytes(), &emptied); err != nil || emptied.Document.Markdown != "" || emptied.Artifact.Version <= artifactVersion(artifact) {
		t.Fatalf("empty PATCH response=%s", empty.Body.String())
	}
	stored, ok := kanbanApp.osArtifactByID(artifact.ID)
	if !ok || stored.Text != documentStudioEmptyBodySentinel || stored.Metadata[documentStudioEmptyMetadataKey] != "true" {
		t.Fatalf("empty stored artifact=%+v", stored)
	}
	get := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/document?id="+artifact.ID, "", cookies, documentEditorHandler)
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"markdown":""`) {
		t.Fatalf("empty GET status=%d body=%s", get.Code, get.Body.String())
	}
	emptyCopyBody, _ := json.Marshal(map[string]any{
		"artifactId": artifact.ID, "expectedVersion": emptied.Artifact.Version, "title": "Empty field notes copy",
		"fileName": "Empty field notes copy", "folderId": "",
		"document": map[string]any{"schemaVersion": 1, "markdown": ""},
	})
	emptyCopy := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/copies", string(emptyCopyBody), cookies, documentEditorCopyHandler)
	if emptyCopy.Code != http.StatusCreated || !strings.Contains(emptyCopy.Body.String(), `"markdown":""`) {
		t.Fatalf("empty copy status=%d body=%s", emptyCopy.Code, emptyCopy.Body.String())
	}
	repopulatedBody, _ := json.Marshal(map[string]any{
		"artifactId": artifact.ID, "expectedVersion": emptied.Artifact.Version, "title": "Repopulated field notes",
		"document": map[string]any{"schemaVersion": 1, "markdown": "# Back again"},
	})
	repopulated := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/document", string(repopulatedBody), cookies, documentEditorHandler)
	if repopulated.Code != http.StatusOK {
		t.Fatalf("repopulate PATCH status=%d body=%s", repopulated.Code, repopulated.Body.String())
	}
	stored, _ = kanbanApp.osArtifactByID(artifact.ID)
	if stored.Text != "# Back again" || stored.Metadata[documentStudioEmptyMetadataKey] != "false" {
		t.Fatalf("repopulated stored artifact=%+v", stored)
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

func TestDocumentStudioStaleDraftCanSaveCopyWithBranchReceipt(t *testing.T) {
	cookies, artifact := setupDocumentEditorHTTPTest(t)
	baseVersion := artifactVersion(artifact)
	updateBody, _ := json.Marshal(map[string]any{
		"artifactId": artifact.ID, "expectedVersion": baseVersion, "title": "Current source",
		"document": map[string]any{"schemaVersion": 1, "markdown": "# Current\n\nA teammate changed this."},
	})
	updatedResponse := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/document", string(updateBody), cookies, documentEditorHandler)
	if updatedResponse.Code != http.StatusOK {
		t.Fatalf("source update status=%d body=%s", updatedResponse.Code, updatedResponse.Body.String())
	}
	current, _ := kanbanApp.osArtifactByID(artifact.ID)
	if artifactVersion(current) <= baseVersion {
		t.Fatalf("current version=%d base=%d", artifactVersion(current), baseVersion)
	}
	copyBody, _ := json.Marshal(map[string]any{
		"artifactId": artifact.ID, "expectedVersion": baseVersion, "title": "Recovered local draft",
		"fileName": "Recovered local draft", "folderId": "",
		"document": map[string]any{"schemaVersion": 1, "markdown": "# Local draft\n\nKeep my unsaved work."},
	})
	response := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/copies", string(copyBody), cookies, documentEditorCopyHandler)
	if response.Code != http.StatusCreated {
		t.Fatalf("stale copy status=%d body=%s", response.Code, response.Body.String())
	}
	var copied struct {
		Artifact documentStudioArtifactView `json:"artifact"`
		Document documentStudioDocument     `json:"document"`
		Receipt  struct {
			BranchedFromVersion  int  `json:"branchedFromArtifactVersion"`
			SourceCurrentVersion int  `json:"sourceCurrentVersion"`
			StaleBranch          bool `json:"staleBranch"`
		} `json:"receipt"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &copied); err != nil || copied.Document.Markdown != "# Local draft\n\nKeep my unsaved work." || copied.Receipt.BranchedFromVersion != baseVersion || copied.Receipt.SourceCurrentVersion != artifactVersion(current) || !copied.Receipt.StaleBranch {
		t.Fatalf("stale copy response=%s", response.Body.String())
	}
	entry, ok := kanbanApp.osArtifactByID(copied.Artifact.ID)
	if !ok || entry.Metadata["copiedFromArtifactVersion"] != strconv.Itoa(baseVersion) || entry.Metadata["copiedFromCurrentArtifactVersion"] != strconv.Itoa(artifactVersion(current)) || entry.Metadata["copiedFromStaleRevision"] != "true" {
		t.Fatalf("stale copy metadata=%+v", entry.Metadata)
	}
	currentAfter, _ := kanbanApp.osArtifactByID(artifact.ID)
	if currentAfter.Text != current.Text || artifactVersion(currentAfter) != artifactVersion(current) {
		t.Fatalf("stale copy mutated source: before=%+v after=%+v", current, currentAfter)
	}
	futureBody, _ := json.Marshal(map[string]any{
		"artifactId": artifact.ID, "expectedVersion": artifactVersion(current) + 1, "title": "Impossible branch",
		"fileName": "Impossible branch", "folderId": "",
		"document": map[string]any{"schemaVersion": 1, "markdown": "future body"},
	})
	future := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/copies", string(futureBody), cookies, documentEditorCopyHandler)
	if future.Code != http.StatusConflict {
		t.Fatalf("future copy status=%d body=%s", future.Code, future.Body.String())
	}
}

func TestDocumentStudioCopyFailureReturnsRetryablePartialSuccess(t *testing.T) {
	cookies, artifact := setupDocumentEditorHTTPTest(t)
	folder, err := createFileFolder("Document copy race", "AJ")
	if err != nil {
		t.Fatal(err)
	}
	previousProbe := fileSaveAfterArtifactStampProbe
	fileSaveAfterArtifactStampProbe = func() {
		fileSaveAfterArtifactStampProbe = nil
		_ = sharedFileFolderStore().remove(folder.ID)
	}
	t.Cleanup(func() { fileSaveAfterArtifactStampProbe = previousProbe })
	body, _ := json.Marshal(map[string]any{
		"artifactId": artifact.ID, "expectedVersion": artifactVersion(artifact), "title": "Recoverable copy",
		"fileName": "Recoverable copy", "folderId": folder.ID,
		"document": map[string]any{"schemaVersion": 1, "markdown": "# Recoverable\n\nThe artifact must survive."},
	})
	response := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/copies", string(body), cookies, documentEditorCopyHandler)
	if response.Code != http.StatusNotFound {
		t.Fatalf("partial copy status=%d body=%s", response.Code, response.Body.String())
	}
	var partial struct {
		OK             bool                       `json:"ok"`
		PartialSuccess bool                       `json:"partialSuccess"`
		Artifact       documentStudioArtifactView `json:"artifact"`
		Document       documentStudioDocument     `json:"document"`
		Receipt        struct {
			Outcome      string `json:"outcome"`
			ArtifactID   string `json:"artifactId"`
			ContentSaved bool   `json:"contentSaved"`
			FilingDone   bool   `json:"filingCompleted"`
			SavedToFiles bool   `json:"savedToFiles"`
			Retryable    bool   `json:"retryable"`
			RetryURL     string `json:"retryUrl"`
		} `json:"receipt"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &partial); err != nil || partial.OK || !partial.PartialSuccess || partial.Artifact.ID == "" || partial.Document.Markdown != "# Recoverable\n\nThe artifact must survive." || partial.Receipt.Outcome != "copy_created_files_failed" || partial.Receipt.ArtifactID != partial.Artifact.ID || !partial.Receipt.ContentSaved || partial.Receipt.FilingDone || partial.Receipt.SavedToFiles || !partial.Receipt.Retryable || partial.Receipt.RetryURL != "/assistant/files/save" {
		t.Fatalf("partial copy response=%s", response.Body.String())
	}
	created, ok := kanbanApp.osArtifactByID(partial.Artifact.ID)
	if !ok || created.Text != partial.Document.Markdown || strings.EqualFold(created.Metadata["savedToFiles"], "true") {
		t.Fatalf("partial copy artifact=%+v", created)
	}
	retryBody, _ := json.Marshal(map[string]any{"artifactId": partial.Artifact.ID, "fileName": "Recoverable copy", "folderId": ""})
	retry := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/files/save", string(retryBody), cookies, assistantFileSaveHandler)
	if retry.Code != http.StatusOK {
		t.Fatalf("partial copy retry status=%d body=%s", retry.Code, retry.Body.String())
	}
}

func TestDocumentStudioOwnerPrivateACLAndCopyStayPrivate(t *testing.T) {
	ownerCookies, _ := setupDocumentEditorHTTPTest(t)
	nonOwnerCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	private, _, err := kanbanApp.createOSArtifactWithMetadata("artifacts", "Private notes", "# Private", "AJ", map[string]string{
		"type": artifactTypeMarkdown, "visibility": "private", "requestedBy": "aj@shareability.com", "ownerEmail": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerGET := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/document?id="+private.ID, "", ownerCookies, documentEditorHandler)
	if ownerGET.Code != http.StatusOK {
		t.Fatalf("owner GET status=%d body=%s", ownerGET.Code, ownerGET.Body.String())
	}
	nonOwnerGET := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/document?id="+private.ID, "", nonOwnerCookies, documentEditorHandler)
	if nonOwnerGET.Code != http.StatusNotFound {
		t.Fatalf("non-owner GET status=%d body=%s", nonOwnerGET.Code, nonOwnerGET.Body.String())
	}
	patchBody, _ := json.Marshal(map[string]any{
		"artifactId": private.ID, "expectedVersion": artifactVersion(private), "title": "Leaked edit",
		"document": map[string]any{"schemaVersion": 1, "markdown": "# Non-owner edit"},
	})
	deniedPatch := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/document", string(patchBody), nonOwnerCookies, documentEditorHandler)
	if deniedPatch.Code != http.StatusNotFound {
		t.Fatalf("non-owner PATCH status=%d body=%s", deniedPatch.Code, deniedPatch.Body.String())
	}
	copyBody, _ := json.Marshal(map[string]any{
		"artifactId": private.ID, "expectedVersion": artifactVersion(private), "title": "Private notes copy",
		"fileName": "Private notes copy", "folderId": "",
		"document": map[string]any{"schemaVersion": 1, "markdown": "# Still private"},
	})
	deniedCopy := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/copies", string(copyBody), nonOwnerCookies, documentEditorCopyHandler)
	if deniedCopy.Code != http.StatusNotFound {
		t.Fatalf("non-owner copy status=%d body=%s", deniedCopy.Code, deniedCopy.Body.String())
	}
	ownerCopy := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/copies", string(copyBody), ownerCookies, documentEditorCopyHandler)
	if ownerCopy.Code != http.StatusCreated {
		t.Fatalf("owner copy status=%d body=%s", ownerCopy.Code, ownerCopy.Body.String())
	}
	var copied struct {
		Artifact documentStudioArtifactView `json:"artifact"`
	}
	if err := json.Unmarshal(ownerCopy.Body.Bytes(), &copied); err != nil {
		t.Fatal(err)
	}
	copyEntry, _ := kanbanApp.osArtifactByID(copied.Artifact.ID)
	if copyEntry.Metadata["visibility"] != "private" || normalizeAccountEmail(copyEntry.Metadata["ownerEmail"]) != "aj@shareability.com" {
		t.Fatalf("private copy metadata=%+v", copyEntry.Metadata)
	}
	copyDeniedGET := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/document?id="+copied.Artifact.ID, "", nonOwnerCookies, documentEditorHandler)
	if copyDeniedGET.Code != http.StatusNotFound {
		t.Fatalf("non-owner copied GET status=%d body=%s", copyDeniedGET.Code, copyDeniedGET.Body.String())
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
