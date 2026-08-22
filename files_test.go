package main

// Files surface (card 095): the list + upload doors' auth gates, the direct
// upload roundtrip (bytes → putBlob, record → kind=file entry, keyless
// "stored" badge), the 085 ingestion seam firing exactly once with a key, the
// chat-attachment adapter's visibility law (private threads stay the owner's),
// newest-first ordering, the memory-timeline exclusion, control_app opening
// the surface, the GC sweep treating drive uploads as live refs, and the
// third source: terminal agent deliverables (artifact-stage rows) filing into
// the folder layer alongside uploads.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func multipartFileBody(t *testing.T, name string, contentType string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, name))
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("create multipart part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write multipart part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return body, writer.FormDataContentType()
}

func postFileUpload(t *testing.T, cookies []*http.Cookie, name string, contentType string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	body, formContentType := multipartFileBody(t, name, contentType, data)
	req := httptest.NewRequest(http.MethodPost, "/assistant/files/upload", body)
	req.Header.Set("Content-Type", formContentType)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantFileUploadHandler(recorder, req)
	return recorder
}

func deleteDriveFileRequest(t *testing.T, cookies []*http.Cookie, fileID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/assistant/files", strings.NewReader(fmt.Sprintf(`{"fileId":%q}`, fileID)))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantFilesHandler(recorder, req)
	return recorder
}

func TestAssistantFilesHandlersGates(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")

	// Method gates.
	recorder := httptest.NewRecorder()
	assistantFilesHandler(recorder, httptest.NewRequest(http.MethodPost, "/assistant/files", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("list POST status=%d, want 405", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	assistantFileUploadHandler(recorder, httptest.NewRequest(http.MethodGet, "/assistant/files/upload", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("upload GET status=%d, want 405", recorder.Code)
	}

	// Cross-origin gates.
	crossList := httptest.NewRequest(http.MethodGet, "/assistant/files", nil)
	crossList.Header.Set("Origin", "https://evil.example")
	recorder = httptest.NewRecorder()
	assistantFilesHandler(recorder, crossList)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin list status=%d, want 403", recorder.Code)
	}
	crossUpload := httptest.NewRequest(http.MethodPost, "/assistant/files/upload", bytes.NewReader([]byte("x")))
	crossUpload.Header.Set("Origin", "https://evil.example")
	recorder = httptest.NewRecorder()
	assistantFileUploadHandler(recorder, crossUpload)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin upload status=%d, want 403", recorder.Code)
	}

	// Session gates.
	recorder = httptest.NewRecorder()
	assistantFilesHandler(recorder, httptest.NewRequest(http.MethodGet, "/assistant/files", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out list status=%d, want 401", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	assistantFileUploadHandler(recorder, httptest.NewRequest(http.MethodPost, "/assistant/files/upload", bytes.NewReader([]byte("x"))))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out upload status=%d, want 401", recorder.Code)
	}
}

func TestAssistantFileUploadRoundtripAndList(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")

	// A missing file field is a 400, not a panic.
	req := httptest.NewRequest(http.MethodPost, "/assistant/files/upload", strings.NewReader("plain body"))
	req.Header.Set("Content-Type", "text/plain")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantFileUploadHandler(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("non-multipart upload status=%d, want 400", recorder.Code)
	}

	// Empty file rejects before putBlob.
	if recorder := postFileUpload(t, cookies, "empty.txt", "text/plain", nil); recorder.Code != http.StatusBadRequest {
		t.Fatalf("empty upload status=%d, want 400", recorder.Code)
	}

	// Happy path 1: declared mime wins; keyless deploys stay honest "stored".
	pdfBytes := []byte("%PDF-1.7 stationtenn deck bytes")
	recorder = postFileUpload(t, cookies, "stationtenn-deck.pdf", "application/pdf", pdfBytes)
	if recorder.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	var uploadPayload struct {
		OK   bool                `json:"ok"`
		File assistantFileRecord `json:"file"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &uploadPayload); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	row := uploadPayload.File
	if !uploadPayload.OK || row.Name != "stationtenn-deck.pdf" || row.Origin != "files" || row.Mime != "application/pdf" {
		t.Fatalf("upload row=%+v, want ok pdf row with origin files", row)
	}
	if row.BrainStatus != fileBrainStatusStored {
		t.Fatalf("keyless brainStatus=%q, want %q", row.BrainStatus, fileBrainStatusStored)
	}
	if row.UploaderEmail != "aj@shareability.com" || row.UploaderName == "" {
		t.Fatalf("uploader=%q/%q, want the session user stamped", row.UploaderName, row.UploaderEmail)
	}
	if !strings.HasPrefix(row.DownloadURL, "/artifacts/blob?ref=") || !row.Previewable {
		t.Fatalf("downloadUrl=%q previewable=%v, want the session-gated blob route with inline pdf preview", row.DownloadURL, row.Previewable)
	}

	// The bytes round-trip through the content-addressed store.
	parsed, err := url.Parse(row.DownloadURL)
	if err != nil {
		t.Fatalf("parse downloadUrl: %v", err)
	}
	ref := parsed.Query().Get("ref")
	stored, meta, err := getBlob(ref)
	if err != nil {
		t.Fatalf("getBlob after upload: %v", err)
	}
	if !bytes.Equal(stored, pdfBytes) || meta.Mime != "application/pdf" {
		t.Fatalf("stored=%q mime=%q, want the uploaded bytes with the pinned mime", stored, meta.Mime)
	}

	// Happy path 2: octet-stream declared → the extension names the mime; a
	// non-inline-safe type is downloadable but never previewable.
	recorder = postFileUpload(t, cookies, "notes.txt", "application/octet-stream", []byte("term sheet notes"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("txt upload status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	var textPayload struct {
		File assistantFileRecord `json:"file"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &textPayload); err != nil {
		t.Fatal(err)
	}
	if textPayload.File.BrainStatus != fileBrainStatusIngested {
		t.Fatalf("plain text brainStatus=%q, want immediate ingestion", textPayload.File.BrainStatus)
	}
	if matches := kanbanApp.memory.search("term sheet notes", 5); len(matches) == 0 {
		t.Fatal("plain text contents must enter recall without a model key")
	}

	// The list door: newest first, both uploads present.
	listReq := httptest.NewRequest(http.MethodGet, "/assistant/files", nil)
	for _, cookie := range cookies {
		listReq.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	assistantFilesHandler(recorder, listReq)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	var listPayload struct {
		OK    bool                  `json:"ok"`
		Files []assistantFileRecord `json:"files"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if !listPayload.OK || len(listPayload.Files) != 2 {
		t.Fatalf("list=%+v, want both uploads", listPayload)
	}
	if listPayload.Files[0].Name != "notes.txt" || listPayload.Files[1].Name != "stationtenn-deck.pdf" {
		t.Fatalf("list order=%q,%q, want newest first", listPayload.Files[0].Name, listPayload.Files[1].Name)
	}
	if listPayload.Files[0].Mime != "text/plain" || listPayload.Files[0].Previewable {
		t.Fatalf("txt row=%+v, want extension-derived text/plain with no inline preview", listPayload.Files[0])
	}

	// kind=file entries are searchable knowledge (name at minimum) but NEVER
	// memory-timeline noise.
	if matches := kanbanApp.memory.search("stationtenn", 5); len(matches) == 0 {
		t.Fatal("uploaded file name must be findable via memory search")
	}
	for _, entry := range visibleMeetingMemoryEntries(kanbanApp.memory.snapshot(0), 0) {
		if entry.Kind == meetingMemoryKindFile {
			t.Fatal("file entries must not render in the client memory timeline")
		}
	}
}

func TestAssistantFileDeleteRemovesUploadAndRecall(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", filepath.Join(t.TempDir(), "file-folders.json"))

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	upload := postFileUpload(t, cookies, "semantic-delete-canary.txt", "text/plain", []byte("delete me"))
	if upload.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", upload.Code, upload.Body.String())
	}
	var payload struct {
		File assistantFileRecord `json:"file"`
	}
	if err := json.Unmarshal(upload.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if matches := kanbanApp.memory.search("semantic delete canary", 5); len(matches) == 0 {
		t.Fatal("uploaded file must enter recall before delete")
	}
	if response := deleteDriveFileRequest(t, cookies, payload.File.ID); response.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
	if fileRowVisible(kanbanApp, "aj@shareability.com", payload.File.ID) {
		t.Fatal("deleted upload still visible in Drive")
	}
	if matches := kanbanApp.memory.search("semantic delete canary", 5); len(matches) != 0 {
		t.Fatalf("deleted upload still participates in recall: %+v", matches)
	}
	if response := deleteDriveFileRequest(t, cookies, payload.File.ID); response.Code != http.StatusNotFound {
		t.Fatalf("second delete status=%d, want 404", response.Code)
	}
}

func TestAssistantFileDeleteChatAttachmentAndUnsavesDeliverable(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", filepath.Join(t.TempDir(), "file-folders.json"))
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")

	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Source chat", "private")
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("aj@shareability.com")
	ref, err := putBlob(tinyPNG(t), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	file := grantTestPendingAttachment(t, kanbanApp, user, thread, ref)
	file.Name = "source.png"
	file.Mime = "image/png"
	file.Text = "chat recall canary"
	meta, _ := blobStatForRef(ref)
	const reservationID = "drive-delete-chat-file"
	if err := kanbanApp.reservePendingAttachmentUpload(user, thread, file, meta, reservationID); err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{
		ID: "message-files", Kind: "message", Role: "user", Text: "attached", AuthorName: "AJ", AuthorEmail: "aj@shareability.com",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Files: []scoutChatFileAttachment{file},
		attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(thread), attachmentReservationID: reservationID,
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages(user.Email, thread.ID, message); err != nil {
		t.Fatal(err)
	}
	chatID := thread.ID + ":message-files:0"
	if response := deleteDriveFileRequest(t, cookies, chatID); response.Code != http.StatusOK {
		t.Fatalf("delete chat file status=%d body=%s", response.Code, response.Body.String())
	}
	storedThread, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", thread.ID)
	if err != nil || len(storedThread.Messages) != 1 || len(storedThread.Messages[0].Files) != 0 {
		t.Fatalf("chat attachment survived delete: thread=%+v err=%v", storedThread, err)
	}

	report, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Saved report", "# durable artifact", "AJ", map[string]string{
		"source": "scout_thread", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kanbanApp.saveDeliverableToFiles(report.ID, "", "AJ"); err != nil {
		t.Fatal(err)
	}
	response := deleteDriveFileRequest(t, cookies, report.ID)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "removed_from_drive") {
		t.Fatalf("unsave status=%d body=%s", response.Code, response.Body.String())
	}
	if fileRowVisible(kanbanApp, "aj@shareability.com", report.ID) {
		t.Fatal("unsaved deliverable still visible in Drive")
	}
	if artifact, ok := kanbanApp.osArtifactByID(report.ID); !ok || artifact.Text != "# durable artifact" || artifact.Metadata["savedToFiles"] != "false" {
		t.Fatalf("unsave damaged source artifact: %+v ok=%v", artifact, ok)
	}
}

func TestApprovedPackagingStudioDeliverableCanBeSavedToFiles(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	deck, _, err := app.createOSArtifactWithMetadata("artifacts", "Approved studio deck", faithfulDeckHTML, "AJ", map[string]string{
		"type": artifactTypeHTMLDeck, "source": "packaging_studio_ship", "status": artifactStatusApproved,
		"goalId": "os-artifact-workflow-files-proof", "artifactContract": packagingStudioDeckContract,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !deliverableRecordQualifies(deck) {
		t.Fatal("approved Packaging Studio deck did not qualify for Files")
	}
	row, err := app.saveDeliverableToFilesNamed(deck.ID, "", "Approved studio deck", "AJ")
	if err != nil {
		t.Fatalf("save approved studio deck: %v", err)
	}
	if row.ID != deck.ID || row.Name != "Approved studio deck" {
		t.Fatalf("Files row=%+v", row)
	}
	draft := deck
	draft.ID = "draft-studio-deck"
	draft.Metadata = map[string]string{"source": "packaging_studio_ship", "status": artifactStatusDraft, "goalId": "goal", "artifactContract": packagingStudioDeckContract}
	if deliverableRecordQualifies(draft) {
		t.Fatal("unapproved Packaging Studio draft qualified for Files")
	}
}

func TestAssistantFileUploadOversizeRejected(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	recorder := postFileUpload(t, cookies, "raw-footage.mov", "video/quicktime", make([]byte, blobMaxBytes+1))
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize upload status=%d, want 413", recorder.Code)
	}
	if entries := kanbanApp.memory.entriesOfKind(meetingMemoryKindFile, 0); len(entries) != 0 {
		t.Fatalf("oversize upload persisted %d entries, want none", len(entries))
	}
}

func TestAssistantFileUploadCleansMultipartTempFiles(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	// Point multipart's spill directory at a private temp dir so we can prove
	// the handler leaves no leftover parts behind (os.TempDir reads TMPDIR).
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)

	multipartLeftovers := func() int {
		matches, err := filepath.Glob(filepath.Join(tmpDir, "multipart-*"))
		if err != nil {
			t.Fatalf("glob multipart temp files: %v", err)
		}
		return len(matches)
	}
	if n := multipartLeftovers(); n != 0 {
		t.Fatalf("temp dir already has %d multipart-* files before upload", n)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	// 9MB comfortably clears the 8MB in-memory threshold, forcing a temp-file
	// spill during ParseMultipartForm, while staying under the 64MB blob cap.
	recorder := postFileUpload(t, cookies, "big-deck.pdf", "application/pdf", make([]byte, 9<<20))
	if recorder.Code != http.StatusOK {
		t.Fatalf("large upload status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	if n := multipartLeftovers(); n != 0 {
		t.Fatalf("handler left %d multipart-* temp files behind, want 0", n)
	}
}

func TestAssistantFileUploadRunsIngestionSeamOnce(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = "sk-openai-test"
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	t.Setenv("OPENAI_API_KEY", "sk-openai-test")

	const transcription = "Deck claims: $2M ARR, 40% MoM growth, pilot with StationTenn."
	deriveCalls := 0
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		deriveCalls++
		if request.Instructions != attachmentDeriveInstructions {
			t.Fatalf("derive instructions=%q, want the 085 transcription prompt", request.Instructions)
		}
		if len(request.Attachments) != 1 {
			t.Fatalf("derive attachments=%d, want the uploaded binary block", len(request.Attachments))
		}
		return transcription, nil
	})

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	recorder := postFileUpload(t, cookies, "deck.png", "image/png", []byte("\x89PNG raster bytes"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	if deriveCalls != 1 {
		t.Fatalf("ingestion seam calls=%d, want exactly one", deriveCalls)
	}
	var payload struct {
		File assistantFileRecord `json:"file"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if payload.File.BrainStatus != fileBrainStatusIngested {
		t.Fatalf("brainStatus=%q, want %q after the derive pass", payload.File.BrainStatus, fileBrainStatusIngested)
	}

	entries := kanbanApp.memory.entriesOfKind(meetingMemoryKindFile, 0)
	if len(entries) != 1 {
		t.Fatalf("file entries=%d, want one", len(entries))
	}
	if !strings.Contains(entries[0].Text, "$2M ARR") {
		t.Fatalf("entry text=%q, want the derived transcript feeding memory search", entries[0].Text)
	}
	if entries[0].Metadata["ingestedAt"] == "" {
		t.Fatal("ingested entry must stamp ingestedAt")
	}

	// A non-model-safe type never touches the seam.
	recorder = postFileUpload(t, cookies, "notes.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", []byte("zip bytes"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("docx upload status=%d, want 200", recorder.Code)
	}
	if deriveCalls != 1 {
		t.Fatalf("ingestion seam calls=%d after docx upload, want still one", deriveCalls)
	}
}

func TestAssistantFilesRequireExplicitChatAttachmentSave(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)

	ref, err := putBlob([]byte("raster bytes"), "image/png")
	if err != nil {
		t.Fatalf("putBlob: %v", err)
	}

	private, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Deck check", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	aj := accountStore().findUser("aj@shareability.com")
	privateReservation := "files-private-reservation"
	privateFile := reserveTestAttachment(t, app, aj, private, scoutChatFileAttachment{Name: "deck.png", Kind: "png", Ref: ref, Text: "derived facts"}, privateReservation)
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", private.ID, scoutChatMessageRecord{
		ID:                            "msg-private-1",
		Kind:                          "message",
		Role:                          "user",
		Text:                          "look at this",
		CreatedAt:                     time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano),
		AuthorName:                    "AJ",
		AuthorEmail:                   "aj@shareability.com",
		Files:                         []scoutChatFileAttachment{privateFile},
		attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(private),
		attachmentReservationID:       privateReservation,
	}); err != nil {
		t.Fatalf("commit private message: %v", err)
	}

	channel, err := app.createScoutChatThread("tom@shareability.com", "Tom", "standup", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	publicRef, err := putBlob([]byte("%PDF-1.4\npublic fixture"), "application/pdf")
	if err != nil {
		t.Fatalf("put public PDF: %v", err)
	}
	tom := accountStore().findUser("tom@shareability.com")
	publicReservation := "files-public-reservation"
	publicFile := reserveTestAttachment(t, app, tom, channel, scoutChatFileAttachment{Name: "onesheet.pdf", Kind: "pdf", Ref: publicRef}, publicReservation)
	if _, err := app.commitScoutChatThreadMessages("tom@shareability.com", channel.ID, scoutChatMessageRecord{
		ID:          "msg-channel-1",
		Kind:        "message",
		Role:        "user",
		Text:        "sharing the onesheet",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		AuthorName:  "Tom",
		AuthorEmail: "tom@shareability.com",
		Files: []scoutChatFileAttachment{
			publicFile,
			// name-only pre-085 chip: no bytes, no text — never listed
			{Name: "ghost.key", Kind: "key", Size: 9},
		},
		attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(channel),
		attachmentReservationID:       publicReservation,
	}); err != nil {
		t.Fatalf("commit channel message: %v", err)
	}

	// Chat attachments remain in chat and do not clutter Drive for either an
	// owner or a teammate, even when the channel itself is readable.
	rows := app.assistantFilesForUser("aj@shareability.com")
	if len(rows) != 0 {
		t.Fatalf("owner rows=%+v, want no implicit chat attachments", rows)
	}
	teammateRows := app.assistantFilesForUser("tom@shareability.com")
	if len(teammateRows) != 0 {
		t.Fatalf("teammate rows=%+v, want no implicit channel attachments", teammateRows)
	}

	// A reader can explicitly promote the public-channel attachment, choose a
	// Drive name, and get a source-bound first-class file row.
	publicSourceID := fmt.Sprintf("%s:%s:%d", channel.ID, "msg-channel-1", 0)
	saved, err := app.saveChatAttachmentToFiles(aj, publicSourceID, "", "Country Golf report.pdf")
	if err != nil {
		t.Fatalf("save channel attachment: %v", err)
	}
	if saved.Name != "Country Golf report.pdf" || saved.Origin != "files" || saved.UploaderEmail != "aj@shareability.com" {
		t.Fatalf("saved row=%+v, want named AJ-owned Drive copy", saved)
	}
	rows = app.assistantFilesForUser("aj@shareability.com")
	if len(rows) != 1 || rows[0].ID != saved.ID {
		t.Fatalf("owner rows=%+v, want only the explicit Drive copy", rows)
	}
	if _, _, err := getBlob(publicRef); err != nil {
		t.Fatalf("shared blob missing after promotion: %v", err)
	}
	savedEntry, found := app.memory.entryByKindAndID(meetingMemoryKindFile, saved.ID)
	if !found || savedEntry.Metadata["sourceChatFileId"] != publicSourceID ||
		savedEntry.Metadata["sourceAttachmentId"] != publicFile.SourceID ||
		savedEntry.Metadata["sourceFileRevision"] != publicFile.SourceRevision {
		t.Fatalf("saved provenance=%+v, want exact committed chat source", savedEntry.Metadata)
	}

	// Readability remains the source authority: a teammate cannot promote AJ's
	// private attachment merely by guessing its virtual source id.
	privateSourceID := fmt.Sprintf("%s:%s:%d", private.ID, "msg-private-1", 0)
	if _, err := app.saveChatAttachmentToFiles(tom, privateSourceID, "", "stolen.png"); !errors.Is(err, errFileSaveSourceNotFound) {
		t.Fatalf("private save err=%v, want not found", err)
	}

	// Derived text does not bypass the explicit-save boundary either.
	notesReservation := "files-notes-reservation"
	notesFile := reserveTestAttachment(t, app, tom, channel, scoutChatFileAttachment{Name: "notes.pdf", Kind: "pdf", Ref: publicRef, Text: "channel derived facts"}, notesReservation)
	if _, err := app.commitScoutChatThreadMessages("tom@shareability.com", channel.ID, scoutChatMessageRecord{
		ID:                            "msg-channel-2",
		Kind:                          "message",
		Role:                          "user",
		Text:                          "notes",
		CreatedAt:                     time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano),
		AuthorName:                    "Tom",
		AuthorEmail:                   "tom@shareability.com",
		Files:                         []scoutChatFileAttachment{notesFile},
		attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(channel),
		attachmentReservationID:       notesReservation,
	}); err != nil {
		t.Fatalf("commit second channel message: %v", err)
	}
	notesFound := false
	for _, row := range app.assistantFilesForUser("tom@shareability.com") {
		if row.Name == "notes.pdf" {
			notesFound = true
		}
	}
	if notesFound {
		t.Fatal("a later channel attachment must also stay out of Drive until explicitly saved")
	}
}

func TestSaveChatAttachmentToFilesRejectsRevocationAfterInitialAuthorization(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	owner := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(owner.Email, owner.Name, "Promotion race", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := putBlob([]byte("promotion race bytes"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	reservationID := "files-promotion-race-reservation"
	file := reserveTestAttachment(t, app, owner, thread, scoutChatFileAttachment{Name: "race.pdf", Kind: "pdf", Ref: ref}, reservationID)
	const messageID = "files-promotion-race-message"
	if _, err := app.commitScoutChatThreadMessages(owner.Email, thread.ID, scoutChatMessageRecord{
		ID: messageID, Kind: "message", Role: "user", Text: "save this", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		AuthorName: owner.Name, AuthorEmail: owner.Email, Files: []scoutChatFileAttachment{file},
		attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(thread), attachmentReservationID: reservationID,
	}); err != nil {
		t.Fatal(err)
	}

	previousProbe := saveChatAttachmentToFilesAfterAuthorizationProbe
	t.Cleanup(func() { saveChatAttachmentToFilesAfterAuthorizationProbe = previousProbe })
	var revokeErr error
	probeCalls := 0
	saveChatAttachmentToFilesAfterAuthorizationProbe = func() {
		probeCalls++
		revokeErr = app.revokeAttachmentSource(file.SourceID)
		saveChatAttachmentToFilesAfterAuthorizationProbe = nil
	}
	before := len(app.memory.entriesOfKind(meetingMemoryKindFile, 0))
	_, err = app.saveChatAttachmentToFiles(owner, fmt.Sprintf("%s:%s:0", thread.ID, messageID), "", "race copy.pdf")
	if !errors.Is(err, errFileSaveSourceNotFound) {
		t.Fatalf("save err=%v, want source not found after revocation", err)
	}
	if revokeErr != nil || probeCalls != 1 {
		t.Fatalf("revocation err=%v probeCalls=%d", revokeErr, probeCalls)
	}
	if after := len(app.memory.entriesOfKind(meetingMemoryKindFile, 0)); after != before {
		t.Fatalf("Files rows=%d before/%d after; revoked promotion must append nothing", before, after)
	}
}

func TestPromotedChatFileReadsRemainSourceBoundWhileDirectUploadsDoNot(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	owner := accountStore().findUser("aj@shareability.com")
	cookies := loginAs(t, owner.Email, "B0NFIRE!")

	thread, err := kanbanApp.createScoutChatThread(owner.Email, owner.Name, "Promotion authority", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	promotedRef, err := putBlob([]byte("source-bound report"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	reservationID := "files-source-bound-reservation"
	source := reserveTestAttachment(t, kanbanApp, owner, thread, scoutChatFileAttachment{Name: "bound.pdf", Kind: "pdf", Ref: promotedRef, Text: "BOUND-SOURCE-CONTEXT"}, reservationID)
	const messageID = "files-source-bound-message"
	if _, err := kanbanApp.commitScoutChatThreadMessages(owner.Email, thread.ID, scoutChatMessageRecord{
		ID: messageID, Kind: "message", Role: "user", Text: "source", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		AuthorName: owner.Name, AuthorEmail: owner.Email, Files: []scoutChatFileAttachment{source},
		attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(thread), attachmentReservationID: reservationID,
	}); err != nil {
		t.Fatal(err)
	}
	sourceFileID := fmt.Sprintf("%s:%s:0", thread.ID, messageID)
	promoted, err := kanbanApp.saveChatAttachmentToFiles(owner, sourceFileID, "", "promoted.pdf")
	if err != nil {
		t.Fatal(err)
	}
	promotedEntry, found := kanbanApp.memory.entryByKindAndID(meetingMemoryKindFile, promoted.ID)
	if !found {
		t.Fatal("promoted Files row missing")
	}

	// Rows written before sourceAttachmentId shipped still bind to the exact
	// historical thread/message/index, ref, and source revision.
	legacyMetadata := make(map[string]string, len(promotedEntry.Metadata))
	for key, value := range promotedEntry.Metadata {
		if key != "sourceAttachmentId" {
			legacyMetadata[key] = value
		}
	}
	legacy, _, err := kanbanApp.memory.appendEntry(meetingMemoryKindFile, "file-legacy-promoted", promotedEntry.Text, legacyMetadata)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, allowed := kanbanApp.promotedChatFileSource(context.Background(), owner, legacy); !allowed {
		t.Fatal("historical promoted provenance was denied while its exact source remained current")
	}

	directRef, err := putBlob([]byte("independent direct upload"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	directMeta, err := blobStatForRef(directRef)
	if err != nil {
		t.Fatal(err)
	}
	direct, _, err := kanbanApp.memory.appendEntry(meetingMemoryKindFile, "file-direct-independent", "DIRECT-UPLOAD-CONTEXT", map[string]string{
		"name": "direct.pdf", "blobRef": directRef, "mime": directMeta.Mime, "size": strconv.FormatInt(directMeta.Size, 10),
		"uploaderEmail": owner.Email, "uploaderName": owner.Name, "origin": "files", "brainStatus": fileBrainStatusIngested,
	})
	if err != nil {
		t.Fatal(err)
	}
	embeddingIDs := map[string]bool{}
	for _, entry := range kanbanApp.memory.eligibleEmbeddingEntriesSnapshot() {
		embeddingIDs[entry.ID] = true
	}
	if embeddingIDs[promoted.ID] || embeddingIDs[legacy.ID] || !embeddingIDs[direct.ID] {
		t.Fatalf("embedding corpus ids=%v; promoted copies must stay request-authorized while direct uploads remain eligible", embeddingIDs)
	}

	requestBlob := func(ref string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/artifacts/blob?ref="+ref, nil)
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		artifactBlobHandler(recorder, request)
		return recorder
	}
	if got := requestBlob(promotedRef); got.Code != http.StatusOK {
		t.Fatalf("authorized promoted blob status=%d body=%s", got.Code, got.Body.String())
	}
	if _, _, _, ok := kanbanApp.assistantFileAttachmentSource(context.Background(), owner, promoted.ID); !ok {
		t.Fatal("authorized promoted file attachment source denied")
	}
	if _, ok := kanbanApp.recallStoreForPrincipal(context.Background(), recallPrincipalForUser(owner)).entryByID(promoted.ID); !ok {
		t.Fatal("authorized promoted derived text missing from recall")
	}

	if err := kanbanApp.revokeAttachmentSource(source.SourceID); err != nil {
		t.Fatal(err)
	}
	visible := map[string]bool{}
	for _, row := range kanbanApp.assistantFilesForUser(owner.Email) {
		visible[row.ID] = true
	}
	if visible[promoted.ID] || visible[legacy.ID] || !visible[direct.ID] {
		t.Fatalf("post-revocation visible rows=%v; promoted rows must disappear and direct upload must remain", visible)
	}
	if _, _, _, ok := kanbanApp.assistantFileAttachmentSource(context.Background(), owner, promoted.ID); ok {
		t.Fatal("revoked promoted file remained available to assistant attachment reads")
	}
	if _, _, _, allowed := kanbanApp.promotedChatFileSource(context.Background(), owner, legacy); allowed {
		t.Fatal("revoked source authorized a historical promoted row")
	}
	if _, ok := kanbanApp.recallStoreForPrincipal(context.Background(), recallPrincipalForUser(owner)).entryByID(promoted.ID); ok {
		t.Fatal("revoked promoted derived text reached assistant recall")
	}
	if got := requestBlob(promotedRef); got.Code != http.StatusNotFound {
		t.Fatalf("revoked promoted blob status=%d body=%s, want 404", got.Code, got.Body.String())
	}
	if got := requestBlob(directRef); got.Code != http.StatusOK || got.Body.String() != "independent direct upload" {
		t.Fatalf("direct upload status=%d body=%q, want unchanged independent read", got.Code, got.Body.String())
	}
}

func TestPromotedManagedPDFRevalidatesCurrentFinalExportAdmission(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	fixture := seedDocumentReportQualityFixture(t, documentReportMinimumJurySeats)
	fixture.fileJury(t, 9.4, documentReportMinimumJurySeats, "KEEP")
	fileAdmittedPublishedDocument(t, &fixture)
	if _, err := fixture.app.saveDeliverableToFiles(fixture.report.ID, "", "AJ"); err != nil {
		t.Fatalf("save admitted report to Files: %v", err)
	}
	fixture.report = mustArtifact(t, fixture.app, fixture.report.ID)
	previousApp := kanbanApp
	kanbanApp = fixture.app
	t.Cleanup(func() { kanbanApp = previousApp })
	owner := accountStore().findUser("aj@shareability.com")
	cookies := loginAs(t, owner.Email, "B0NFIRE!")
	thread, err := kanbanApp.createScoutChatThread(owner.Email, owner.Name, "Managed PDF promotion", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}

	recorder := postAttachmentFromFileForTest(t, cookies, thread.ID, fixture.report.ID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("attach admitted report status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	source := decodeAttachmentFromFileForTest(t, recorder)
	if source.Mime != "application/pdf" {
		t.Fatalf("attached source mime=%q, want admitted PDF", source.Mime)
	}
	const reservationID = "managed-pdf-promotion-reservation"
	cleaned, err := kanbanApp.sanitizeScoutChatFiles(context.Background(), owner, thread, []scoutChatFileAttachment{source}, reservationID)
	if err != nil || len(cleaned) != 1 {
		t.Fatalf("sanitize admitted report: files=%+v err=%v", cleaned, err)
	}
	const messageID = "managed-pdf-promotion-message"
	if _, err := kanbanApp.commitScoutChatThreadMessages(owner.Email, thread.ID, scoutChatMessageRecord{
		ID: messageID, Kind: "message", Role: "user", Text: "file this", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		AuthorName: owner.Name, AuthorEmail: owner.Email, Files: cleaned,
		attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(thread), attachmentReservationID: reservationID,
	}); err != nil {
		t.Fatal(err)
	}
	promoted, err := kanbanApp.saveChatAttachmentToFiles(owner, fmt.Sprintf("%s:%s:0", thread.ID, messageID), "", "admitted-report.pdf")
	if err != nil {
		t.Fatalf("promote admitted report: %v", err)
	}
	if _, _, _, ok := kanbanApp.assistantFileAttachmentSource(context.Background(), owner, promoted.ID); !ok {
		t.Fatal("freshly admitted managed PDF promotion was denied")
	}

	// Move only the jury evidence after promotion. The artifact and immutable PDF
	// remain present, but stable final-export admission is no longer current.
	juryStage := fixture.plan.subtaskByID(documentReportJuryStageID)
	if juryStage == nil || juryStage.ArtifactID == "" {
		t.Fatal("document jury stage missing")
	}
	juryRecord := mustArtifact(t, kanbanApp, juryStage.ArtifactID)
	jury := mustArtifact(t, kanbanApp, juryRecord.Metadata["documentJuryArtifactId"])
	if _, _, err := kanbanApp.updateOSArtifact(jury.ID, jury.Metadata["title"], jury.Text+"\nADMISSION DRIFT", owner.Name); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := kanbanApp.assistantFileAttachmentSource(context.Background(), owner, promoted.ID); ok {
		t.Fatal("promoted managed PDF survived stale final-export admission")
	}
	for _, row := range kanbanApp.assistantFilesForUser(owner.Email) {
		if row.ID == promoted.ID {
			t.Fatal("stale managed PDF promotion remained visible in Files")
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/artifacts/blob?ref="+source.Ref, nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	blobResponse := httptest.NewRecorder()
	artifactBlobHandler(blobResponse, request)
	if blobResponse.Code != http.StatusNotFound || blobResponse.Header().Get("ETag") != "" {
		t.Fatalf("stale promoted managed PDF status=%d headers=%v body=%s", blobResponse.Code, blobResponse.Header(), blobResponse.Body.String())
	}
}

func TestAssistantFilesListsDeliverablesAndFolders(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", filepath.Join(t.TempDir(), "file-folders.json"))

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")

	// card-110 contract: a finished research run files as a deliverable row only
	// once it has been explicitly saved (savedToFiles=true)...
	report, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Samsung ambient teardown", "# Samsung ambient teardown\n\nFindings ride here.", "AJ", map[string]string{
		"source":       "scout_thread",
		"status":       "complete",
		"threadStatus": "complete",
		"updatedBy":    "Scout",
		"savedToFiles": "true",
	})
	if err != nil {
		t.Fatalf("create report artifact: %v", err)
	}
	// ...an explicitly saved html_deck maps onto the deck mime...
	deck, _, err := kanbanApp.createOSArtifactWithMetadata("design", "StationTenn deck", "<!DOCTYPE html><html><body>deck</body></html>", "AJ", map[string]string{
		"source":       "scout_thread",
		"status":       "complete",
		"threadStatus": "complete",
		"type":         "html_deck",
		"savedToFiles": "true",
	})
	if err != nil {
		t.Fatalf("create deck artifact: %v", err)
	}
	// ...while a qualifying-but-never-saved deliverable, an error scaffold, and a
	// hand-saved draft all stay off the surface.
	unsaved, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Unsaved teardown", "# Unsaved\n\nNot filed yet.", "AJ", map[string]string{
		"source":       "scout_thread",
		"status":       "complete",
		"threadStatus": "complete",
	})
	if err != nil {
		t.Fatalf("create unsaved deliverable: %v", err)
	}
	failed, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Broken run", "Scaffold body.", "AJ", map[string]string{
		"source":       "scout_thread",
		"status":       "error",
		"threadStatus": "error",
		"savedToFiles": "true",
	})
	if err != nil {
		t.Fatalf("create error scaffold: %v", err)
	}
	draft, _, err := kanbanApp.createOSArtifactWithMetadata("artifacts", "Hand-saved note", "Just a note.", "AJ", nil)
	if err != nil {
		t.Fatalf("create draft artifact: %v", err)
	}

	// One direct upload rides alongside, then files into a fresh folder next
	// to the deck deliverable — folders take any row id.
	if recorder := postFileUpload(t, cookies, "pitch.pdf", "application/pdf", []byte("%PDF-1.7 bytes")); recorder.Code != http.StatusOK {
		t.Fatalf("upload status=%d, want 200", recorder.Code)
	}
	folder, err := createFileFolder("Diligence", "AJ")
	if err != nil {
		t.Fatalf("createFileFolder: %v", err)
	}
	uploadID := kanbanApp.memory.entriesOfKind(meetingMemoryKindFile, 0)[0].ID
	if err := moveFileToFolder(uploadID, folder.ID); err != nil {
		t.Fatalf("move upload: %v", err)
	}
	if err := moveFileToFolder(deck.ID, folder.ID); err != nil {
		t.Fatalf("move deliverable: %v", err)
	}
	// A dangling assignment (row id no source lists) is ignored at read time.
	if err := moveFileToFolder("file-long-gone", folder.ID); err != nil {
		t.Fatalf("move dangling: %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/assistant/files", nil)
	for _, cookie := range cookies {
		listReq.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantFilesHandler(recorder, listReq)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	var listPayload struct {
		OK      bool                         `json:"ok"`
		Files   []assistantFileRecord        `json:"files"`
		Folders []assistantFileFolderPayload `json:"folders"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if !listPayload.OK || len(listPayload.Files) != 3 {
		t.Fatalf("files=%+v, want the upload + two deliverables", listPayload.Files)
	}
	rowsByID := map[string]assistantFileRecord{}
	for _, row := range listPayload.Files {
		rowsByID[row.ID] = row
	}
	if _, present := rowsByID[failed.ID]; present {
		t.Fatal("error scaffold must never file as a deliverable, even stamped savedToFiles")
	}
	if _, present := rowsByID[unsaved.ID]; present {
		t.Fatal("a qualifying deliverable stays off the surface until explicitly saved (card-110)")
	}
	if _, present := rowsByID[draft.ID]; present {
		t.Fatal("a hand-saved draft has no deliverable provenance")
	}
	reportRow, present := rowsByID[report.ID]
	if !present {
		t.Fatalf("rows=%+v, want the terminal research report", listPayload.Files)
	}
	if reportRow.Origin != "deliverable" || reportRow.ArtifactID != report.ID || !reportRow.Previewable {
		t.Fatalf("report row=%+v, want a previewable deliverable pointing at its artifact", reportRow)
	}
	if reportRow.Name != "Samsung ambient teardown" || reportRow.Mime != "text/markdown" || reportRow.UploaderName != "Scout" {
		t.Fatalf("report row=%+v, want title name, markdown mime, updatedBy attribution", reportRow)
	}
	if reportRow.BrainStatus != fileBrainStatusIngested || reportRow.FolderID != "" {
		t.Fatalf("report row=%+v, want ingested badge at root", reportRow)
	}
	deckRow := rowsByID[deck.ID]
	if deckRow.Mime != "text/html" || deckRow.FolderID != folder.ID {
		t.Fatalf("deck row=%+v, want html_deck mime filed under the folder", deckRow)
	}
	uploadRow := rowsByID[uploadID]
	if uploadRow.FolderID != folder.ID || uploadRow.ArtifactID != "" {
		t.Fatalf("upload row=%+v, want folderId stamped and no artifact pointer", uploadRow)
	}

	// The folders payload counts only visible rows — the dangling assignment
	// does not inflate it.
	if len(listPayload.Folders) != 1 {
		t.Fatalf("folders=%+v, want the one folder", listPayload.Folders)
	}
	if chip := listPayload.Folders[0]; chip.ID != folder.ID || chip.Name != "Diligence" || chip.Count != 2 {
		t.Fatalf("folder chip=%+v, want id/name with count 2", chip)
	}
}

func TestControlAppOpensFilesSurface(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if normalized := normalizeOSControlTool("file"); normalized != "files" {
		t.Fatalf("normalizeOSControlTool(file)=%q, want files", normalized)
	}
	result, _, err := app.controlApp(map[string]any{"tool": "files", "also_open": []any{"memory"}})
	if err != nil {
		t.Fatalf("control_app: %v", err)
	}
	opened, _ := result["opened"].([]string)
	if strings.Join(opened, ",") != "files,memory" {
		t.Fatalf("opened=%v, want files then memory", opened)
	}
	actions, ok := result["actions"].([]osAssistantAction)
	if !ok || len(actions) == 0 {
		t.Fatalf("actions=%#v, want open_tool actions", result["actions"])
	}
	if actions[0].Type != "open_tool" || actions[0].Tool != "files" {
		t.Fatalf("first action=%+v, want open_tool files", actions[0])
	}
}

func TestSweepUnreferencedBlobsKeepsFileEntryRefs(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	keepRef, err := putBlob([]byte("drive upload bytes"), "application/pdf")
	if err != nil {
		t.Fatalf("putBlob keep: %v", err)
	}
	orphanRef, err := putBlob([]byte("orphan bytes"), "image/png")
	if err != nil {
		t.Fatalf("putBlob orphan: %v", err)
	}
	if _, _, err := app.memory.appendEntry(meetingMemoryKindFile, "file-keep", "File pitch.pdf uploaded by AJ.", map[string]string{
		"name":    "pitch.pdf",
		"blobRef": keepRef,
		"origin":  "files",
	}); err != nil {
		t.Fatalf("append file entry: %v", err)
	}

	deleted, err := sweepUnreferencedBlobs(app)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != orphanRef {
		t.Fatalf("deleted=%v, want only the orphan %s", deleted, orphanRef)
	}
	if _, _, err := getBlob(keepRef); err != nil {
		t.Fatalf("drive upload blob deleted by sweep: %v", err)
	}
}

// fileRowVisible reports whether a row id surfaces on a viewer's Files list.
func fileRowVisible(app *kanbanBoardApp, viewer string, id string) bool {
	for _, row := range app.assistantFilesForUser(viewer) {
		if row.ID == id {
			return true
		}
	}
	return false
}

func postFileSave(t *testing.T, cookies []*http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/assistant/files/save", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantFileSaveHandler(recorder, req)
	return recorder
}

// TestAssistantFileSaveHandler pins the explicit-save door (card-110): the same
// gate stack as the other files mutations, honest statuses for unknown /
// non-deliverable ids, and a happy path that stamps + files + surfaces the row.
func TestAssistantFileSaveHandler(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", filepath.Join(t.TempDir(), "file-folders.json"))

	// Method gate.
	rec := httptest.NewRecorder()
	assistantFileSaveHandler(rec, httptest.NewRequest(http.MethodGet, "/assistant/files/save", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("save GET status=%d, want 405", rec.Code)
	}
	// Cross-origin gate.
	cross := httptest.NewRequest(http.MethodPost, "/assistant/files/save", strings.NewReader(`{}`))
	cross.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	assistantFileSaveHandler(rec, cross)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin save status=%d, want 403", rec.Code)
	}
	// Session gate.
	rec = httptest.NewRecorder()
	assistantFileSaveHandler(rec, httptest.NewRequest(http.MethodPost, "/assistant/files/save", strings.NewReader(`{}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out save status=%d, want 401", rec.Code)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")

	report, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Samsung teardown", "# body", "AJ", map[string]string{
		"source":       "scout_thread",
		"status":       "complete",
		"threadStatus": "complete",
	})
	if err != nil {
		t.Fatalf("create report: %v", err)
	}
	if fileRowVisible(kanbanApp, "aj@shareability.com", report.ID) {
		t.Fatal("a deliverable must be invisible before it is saved")
	}

	// Missing artifactId → 400.
	if rec := postFileSave(t, cookies, `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("save empty status=%d, want 400", rec.Code)
	}
	// Unknown artifact → 404.
	if rec := postFileSave(t, cookies, `{"artifactId":"nope"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("save unknown status=%d, want 404", rec.Code)
	}
	// A hand-saved note (no deliverable provenance) → 400.
	note, _, err := kanbanApp.createOSArtifactWithMetadata("artifacts", "Note", "just a note", "AJ", nil)
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	if rec := postFileSave(t, cookies, fmt.Sprintf(`{"artifactId":%q}`, note.ID)); rec.Code != http.StatusBadRequest {
		t.Fatalf("save non-deliverable status=%d, want 400", rec.Code)
	}

	// A held canonical principal cannot file even an authorized artifact into
	// a guessed folder owned by another tenant.
	foreign, err := sharedFileFolderStore().createInParentForPrincipal("Foreign", "", StrideE10TenantPrincipal{TenantID: "tenant-foreign", PersonID: "person-foreign"})
	if err != nil {
		t.Fatalf("create foreign folder: %v", err)
	}
	crossTenant := httptest.NewRequest(http.MethodPost, "/assistant/files/save", strings.NewReader(fmt.Sprintf(`{"artifactId":%q,"folderId":%q}`, report.ID, foreign.ID)))
	for _, cookie := range cookies {
		crossTenant.AddCookie(cookie)
	}
	ctx := context.WithValue(crossTenant.Context(), strideE10TenantPrincipalContextKey{}, StrideE10TenantPrincipal{TenantID: "tenant-current", PersonID: "person-current"})
	ctx = context.WithValue(ctx, strideE10TenantSurfaceContextKey{}, StrideE10TenantSurfaceDrive)
	rec = httptest.NewRecorder()
	assistantFileSaveHandler(rec, crossTenant.WithContext(ctx))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant folder save status=%d body=%s, want 404", rec.Code, rec.Body.String())
	}
	if saved, _ := kanbanApp.osArtifactByID(report.ID); saved.Metadata["savedToFiles"] == "true" {
		t.Fatal("cross-tenant folder rejection stamped the artifact as saved")
	}

	// Happy path with a destination folder → 200, stamped + filed, now visible.
	folder, err := createFileFolder("Diligence", "AJ")
	if err != nil {
		t.Fatalf("createFileFolder: %v", err)
	}
	rec = postFileSave(t, cookies, fmt.Sprintf(`{"artifactId":%q,"folderId":%q}`, report.ID, folder.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("save happy status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var savePayload struct {
		OK   bool                `json:"ok"`
		File assistantFileRecord `json:"file"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &savePayload); err != nil {
		t.Fatalf("decode save response: %v", err)
	}
	if !savePayload.OK || savePayload.File.ArtifactID != report.ID || savePayload.File.FolderID != folder.ID {
		t.Fatalf("save payload=%+v, want the saved row filed under the folder", savePayload.File)
	}
	saved, ok := kanbanApp.osArtifactByID(report.ID)
	if !ok || !strings.EqualFold(saved.Metadata["savedToFiles"], "true") {
		t.Fatalf("report savedToFiles=%q, want true", saved.Metadata["savedToFiles"])
	}
	if strings.TrimSpace(saved.Metadata["savedToFilesBy"]) == "" || strings.TrimSpace(saved.Metadata["savedToFilesAt"]) == "" {
		t.Fatalf("save must stamp who/when, got by=%q at=%q", saved.Metadata["savedToFilesBy"], saved.Metadata["savedToFilesAt"])
	}
	if !fileRowVisible(kanbanApp, "aj@shareability.com", report.ID) {
		t.Fatal("a saved deliverable must surface on the Files list")
	}

	// The same HTTP door accepts an exact chat attachment source and returns an
	// independently named Drive row filed in the requested folder.
	owner := accountStore().findUser("aj@shareability.com")
	thread, err := kanbanApp.createScoutChatThread(owner.Email, owner.Name, "Launch assets", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create source thread: %v", err)
	}
	ref, err := putBlob([]byte("photo bytes"), "image/png")
	if err != nil {
		t.Fatalf("put source blob: %v", err)
	}
	reservation := "file-save-handler-reservation"
	file := reserveTestAttachment(t, kanbanApp, owner, thread, scoutChatFileAttachment{Name: "photo.png", Kind: "png", Ref: ref}, reservation)
	if _, err := kanbanApp.commitScoutChatThreadMessages(owner.Email, thread.ID, scoutChatMessageRecord{
		ID:                            "message-with-photo",
		Kind:                          "message",
		Role:                          "user",
		Text:                          "photo",
		CreatedAt:                     time.Now().UTC().Format(time.RFC3339Nano),
		AuthorName:                    owner.Name,
		AuthorEmail:                   owner.Email,
		Files:                         []scoutChatFileAttachment{file},
		attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(thread),
		attachmentReservationID:       reservation,
	}); err != nil {
		t.Fatalf("commit source attachment: %v", err)
	}
	sourceFileID := fmt.Sprintf("%s:%s:0", thread.ID, "message-with-photo")
	rec = postFileSave(t, cookies, fmt.Sprintf(`{"sourceFileId":%q,"fileName":"Board photo.png","folderId":%q}`, sourceFileID, folder.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("save attachment status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var attachmentPayload struct {
		File assistantFileRecord `json:"file"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &attachmentPayload); err != nil {
		t.Fatalf("decode attachment save: %v", err)
	}
	if attachmentPayload.File.Name != "Board photo.png" || attachmentPayload.File.Origin != "files" || attachmentPayload.File.FolderID != folder.ID {
		t.Fatalf("saved attachment=%+v, want named filed Drive copy", attachmentPayload.File)
	}
}

// TestGrandfatherSavedToFilesMigrationIdempotent pins the run-once backfill: the
// FIRST boot stamps pre-gate-qualifying deliverables while never touching
// scaffolds or an explicit unsave, records a persisted marker, and a SECOND boot
// is a no-op EVEN WITH a new qualifying unstamped deliverable added between boots
// (gate finding A: the marker, not an inference, gates the migration — a post-gate
// creation belongs to the explicit-save policy and must never be grandfathered).
func TestGrandfatherSavedToFilesMigrationIdempotent(t *testing.T) {
	previousApp := kanbanApp
	t.Cleanup(func() { kanbanApp = previousApp })

	// Seed legacy content into the store BEFORE the app boots: grandfather runs
	// inside newKanbanBoardApp, so the entries under test must already be on disk
	// for the boot-time migration to see (and stamp) them.
	dir := t.TempDir()
	memPath := filepath.Join(dir, "memory.jsonl")
	t.Setenv("MEETING_MEMORY_PATH", memPath)
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))

	seed, err := newMeetingMemoryStore(memPath)
	if err != nil {
		t.Fatalf("seed store: %v", err)
	}
	qualifying, _, err := seed.appendOSArtifact("legacy-qualifying", "# body", map[string]string{
		"source":       "scout_thread",
		"status":       "complete",
		"threadStatus": "complete",
	})
	if err != nil {
		t.Fatalf("seed qualifying: %v", err)
	}
	scaffold, _, err := seed.appendOSArtifact("legacy-scaffold", "scaffold", map[string]string{
		"source":       "scout_thread",
		"status":       "error",
		"threadStatus": "error",
	})
	if err != nil {
		t.Fatalf("seed scaffold: %v", err)
	}
	unsaved, _, err := seed.appendOSArtifact("legacy-unsaved", "# body", map[string]string{
		"source":       "scout_thread",
		"status":       "complete",
		"threadStatus": "complete",
		"savedToFiles": "false",
	})
	if err != nil {
		t.Fatalf("seed unsaved: %v", err)
	}

	// First boot: the constructor runs grandfatherSavedToFilesAtBoot exactly once.
	kanbanApp = newKanbanBoardApp()
	app := kanbanApp

	if got, _ := app.osArtifactByID(qualifying.ID); !strings.EqualFold(got.Metadata["savedToFiles"], "true") {
		t.Fatalf("qualifying deliverable savedToFiles=%q, want true", got.Metadata["savedToFiles"])
	}
	if s, _ := app.osArtifactByID(scaffold.ID); strings.TrimSpace(s.Metadata["savedToFiles"]) != "" {
		t.Fatalf("error scaffold must never be grandfathered, got %q", s.Metadata["savedToFiles"])
	}
	if u, _ := app.osArtifactByID(unsaved.ID); !strings.EqualFold(u.Metadata["savedToFiles"], "false") {
		t.Fatalf("an explicit unsave must be preserved, got %q", u.Metadata["savedToFiles"])
	}
	if _, ok := app.memory.entryByKindAndID(savedToFilesGrandfatherMarkerKind, savedToFilesGrandfatherMarkerID); !ok {
		t.Fatal("run-once marker must be recorded after the first boot")
	}

	// A NEW qualifying unstamped deliverable created AFTER the migration ran — a
	// post-gate creation the explicit-save policy owns.
	fresh, _, err := app.createOSArtifactWithMetadata("research", "Post-gate brief", "# body", "AJ", map[string]string{
		"source":       "scout_thread",
		"status":       "complete",
		"threadStatus": "complete",
	})
	if err != nil {
		t.Fatalf("create post-gate deliverable: %v", err)
	}

	// Second boot: the marker gates the migration, so the post-gate deliverable is
	// NOT grandfathered and the earlier decisions are unchanged.
	app.grandfatherSavedToFilesAtBoot()
	if got, _ := app.osArtifactByID(fresh.ID); strings.TrimSpace(got.Metadata["savedToFiles"]) != "" {
		t.Fatalf("post-gate deliverable must NOT be grandfathered on a second boot, got %q", got.Metadata["savedToFiles"])
	}
	if got, _ := app.osArtifactByID(qualifying.ID); !strings.EqualFold(got.Metadata["savedToFiles"], "true") {
		t.Fatalf("second boot changed a stamped deliverable: %q", got.Metadata["savedToFiles"])
	}
	if u, _ := app.osArtifactByID(unsaved.ID); !strings.EqualFold(u.Metadata["savedToFiles"], "false") {
		t.Fatalf("second boot resurrected an explicit unsave: %q", u.Metadata["savedToFiles"])
	}
	if markers := app.memory.entriesOfKind(savedToFilesGrandfatherMarkerKind, 0); len(markers) != 1 {
		t.Fatalf("exactly one run-once marker expected, got %d", len(markers))
	}
	// The marker is bookkeeping, not knowledge: it must stay out of the memory
	// snapshot / client timeline (relevance=expired → memoryEntryHiddenFromRecall).
	for _, entry := range app.memorySnapshot(0) {
		if entry.Kind == savedToFilesGrandfatherMarkerKind {
			t.Fatal("run-once marker leaked into the memory snapshot")
		}
	}
	// Mint-free note: the marker pre-stamps meetingId="none" so a boot-time
	// append never opens a phantom office sitting. Not asserted here — the seed
	// entries themselves carry live meeting ids that boot resumes; the property
	// is pinned by TestCompanyDigestIsLedgerStateViewPlusThinNarrative,
	// TestDayDigestTickEmitsReflectionForCompletedDayOnce, and
	// TestMultiRoomAcceptanceFlow, which boot clean stores and assert no idle mint.
}

// TestSaveToFilesToolFilesDeliverables pins the Scout seam: it matches INTEL
// deliverables by title fragment, stamps + files only the matches, and leaves
// unrelated deliverables untouched.
func TestSaveToFilesToolFilesDeliverables(t *testing.T) {
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	app := kanbanApp
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", filepath.Join(t.TempDir(), "file-folders.json"))

	report, _, err := app.createOSArtifactWithMetadata("research", "Samsung TV Plus teardown", "# body", "AJ", map[string]string{
		"source":       "scout_thread",
		"status":       "complete",
		"threadStatus": "complete",
	})
	if err != nil {
		t.Fatalf("create report: %v", err)
	}
	other, _, err := app.createOSArtifactWithMetadata("research", "Unrelated brief", "# body", "AJ", map[string]string{
		"source":       "scout_thread",
		"status":       "complete",
		"threadStatus": "complete",
	})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}

	result, changed, err := app.saveToFilesTool(map[string]any{
		"fileNames":  stringsToAny([]string{"samsung"}),
		"folderName": "Diligence",
	}, "aj@shareability.com")
	if err != nil {
		t.Fatalf("saveToFilesTool: %v", err)
	}
	if changed {
		t.Fatal("save_to_files is not a board mutation")
	}
	if saved, _ := result["saved"].([]string); len(saved) != 1 || !strings.Contains(saved[0], "Samsung") {
		t.Fatalf("saved=%v, want the Samsung report", result["saved"])
	}
	if created, _ := result["created"].(bool); !created {
		t.Fatal("folder Diligence should be created on demand")
	}

	if got, _ := app.osArtifactByID(report.ID); !strings.EqualFold(got.Metadata["savedToFiles"], "true") {
		t.Fatalf("report savedToFiles=%q, want true", got.Metadata["savedToFiles"])
	}
	if o, _ := app.osArtifactByID(other.ID); strings.TrimSpace(o.Metadata["savedToFiles"]) != "" {
		t.Fatalf("unrelated deliverable must be untouched, got %q", o.Metadata["savedToFiles"])
	}

	// folderId is decorated at list time (the HTTP handler step), so decorate
	// before reading it.
	rows := app.assistantFilesForUser("aj@shareability.com")
	decorateAssistantFileFolders(rows)
	var row assistantFileRecord
	found := false
	for _, r := range rows {
		if r.ID == report.ID {
			row = r
			found = true
		}
	}
	if !found {
		t.Fatal("a saved deliverable must surface on the Files list")
	}
	folders := listFileFolders()
	if len(folders) != 1 || row.FolderID != folders[0].ID {
		t.Fatalf("row folderId=%q, want the Diligence folder %+v", row.FolderID, folders)
	}
}
