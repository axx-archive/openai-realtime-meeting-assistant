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

// TestAssistantFileSaveAcceptsEveryStudioProjectKind pins Wave 3 D2: the
// explicit save door files a terminal Work result of every kind — agent-thread
// image, chat image render, sheet, research brief, generic pdf artifact, and
// the studio-native blank document/deck — and answers with the file id.
func TestAssistantFileSaveAcceptsEveryStudioProjectKind(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() { kanbanApp = previousApp; artifactObjectAuthorizer = previousAuthorizer })
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", filepath.Join(t.TempDir(), "file-folders.json"))

	owner := "aj@shareability.com"
	thread, err := kanbanApp.createScoutChatThread(owner, "AJ", "Save every kind", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := seedStudioProjectKindFixtures(t, kanbanApp, owner, thread)
	user := &userAccount{Email: owner, Name: "AJ"}
	blankDocument := studioBlankBaseMetadata(user, "Blank memo", studioBlankSourceDocument, "document")
	blankDocument["type"] = artifactTypeMarkdown
	if fixtures["document"], _, err = kanbanApp.createOSArtifactWithMetadata("research", "Blank memo", "# Blank memo\n\nTyped by hand.", "AJ", blankDocument); err != nil {
		t.Fatal(err)
	}
	blankDeck := studioBlankBaseMetadata(user, "Blank deck", studioBlankSourceDeck, "presentation")
	blankDeck["type"] = artifactTypeHTMLDeck
	blankDeck[deckSceneRefMetadataKey] = strings.Repeat("e", 64)
	if fixtures["presentation"], _, err = kanbanApp.createOSArtifactWithMetadata("artifacts", "Blank deck", "<!doctype html><html><body><main>Blank deck</main></body></html>", "AJ", blankDeck); err != nil {
		t.Fatal(err)
	}
	for kind, entry := range fixtures {
		if classified, ok := studioLegacyProjectCandidate(entry); !ok || classified == "" {
			t.Fatalf("%s fixture is not a Work result: %v", kind, entry.Metadata)
		}
	}

	cookies := loginAs(t, owner, "B0NFIRE!")
	for _, kind := range []string{"image", "chatImage", "sheet", "research", "artifact", "document", "presentation"} {
		entry := fixtures[kind]
		if fileRowVisible(kanbanApp, owner, entry.ID) {
			t.Fatalf("%s result was visible in Files before an explicit save", kind)
		}
		response := postFileSave(t, cookies, fmt.Sprintf(`{"artifactId":%q}`, entry.ID))
		if response.Code != http.StatusOK {
			t.Fatalf("%s save status=%d body=%s", kind, response.Code, response.Body.String())
		}
		var payload struct {
			OK     bool                `json:"ok"`
			FileID string              `json:"fileId"`
			File   assistantFileRecord `json:"file"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if !payload.OK || payload.FileID != entry.ID || payload.File.ID != entry.ID || payload.File.ArtifactID != entry.ID {
			t.Fatalf("%s save payload=%s", kind, response.Body.String())
		}
		if !fileRowVisible(kanbanApp, owner, entry.ID) {
			t.Fatalf("%s result is not visible in Files after saving", kind)
		}
		switch kind {
		case "image", "chatImage":
			if payload.File.Mime != "image/png" || payload.File.DownloadURL == "" {
				t.Fatalf("%s file row=%+v, want image bytes", kind, payload.File)
			}
		case "sheet":
			if payload.File.Mime != ventureWorkbookMime || payload.File.DownloadURL == "" {
				t.Fatalf("sheet file row=%+v, want workbook bytes", payload.File)
			}
		case "artifact":
			if payload.File.Mime != "application/pdf" || payload.File.DownloadURL == "" {
				t.Fatalf("artifact file row=%+v, want pdf bytes", payload.File)
			}
		case "presentation":
			if payload.File.Mime != "text/html" {
				t.Fatalf("presentation file row=%+v, want html", payload.File)
			}
		}
	}
}

// ---- Wave 5 Drive: per-file ACL, share, versions, star/trash, search, work
// refs, quota (D1/D2/D5/D6/D7/D8/D9). ----

func postFileUploadWithFields(t *testing.T, cookies []*http.Cookie, name string, contentType string, data []byte, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write field %s: %v", key, err)
		}
	}
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
	req := httptest.NewRequest(http.MethodPost, "/assistant/files/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantFileUploadHandler(recorder, req)
	return recorder
}

func uploadDriveFileRow(t *testing.T, cookies []*http.Cookie, name string, contentType string, data []byte, fields map[string]string) assistantFileRecord {
	t.Helper()
	recorder := postFileUploadWithFields(t, cookies, name, contentType, data, fields)
	if recorder.Code != http.StatusOK {
		t.Fatalf("upload %s status=%d body=%s", name, recorder.Code, recorder.Body.String())
	}
	var payload struct {
		File assistantFileRecord `json:"file"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode upload %s: %v", name, err)
	}
	return payload.File
}

type driveListPayload struct {
	OK    bool                  `json:"ok"`
	Files []assistantFileRecord `json:"files"`
	Scope string                `json:"scope"`
}

func listDriveFiles(t *testing.T, cookies []*http.Cookie, rawQuery string) driveListPayload {
	t.Helper()
	target := "/assistant/files"
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantFilesHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list %q status=%d body=%s", rawQuery, recorder.Code, recorder.Body.String())
	}
	var payload driveListPayload
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode list %q: %v", rawQuery, err)
	}
	return payload
}

func driveRowByID(rows []assistantFileRecord, id string) (assistantFileRecord, bool) {
	for _, row := range rows {
		if row.ID == id {
			return row, true
		}
	}
	return assistantFileRecord{}, false
}

func patchDriveFile(t *testing.T, cookies []*http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/assistant/files", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantFilesHandler(recorder, req)
	return recorder
}

func postDriveJSON(t *testing.T, handler http.HandlerFunc, path string, cookies []*http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, req)
	return recorder
}

func blobRefFromRow(t *testing.T, row assistantFileRecord) string {
	t.Helper()
	parsed, err := url.Parse(row.DownloadURL)
	if err != nil {
		t.Fatalf("parse downloadUrl %q: %v", row.DownloadURL, err)
	}
	return parsed.Query().Get("ref")
}

func setupDriveWaveTest(t *testing.T) ([]*http.Cookie, []*http.Cookie) {
	t.Helper()
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", filepath.Join(t.TempDir(), "file-folders.json"))
	return loginAs(t, "aj@shareability.com", "B0NFIRE!"), loginAs(t, "joel@shareability.com", "B0NFIRE!")
}

// D1: private uploads are the uploader's alone, people uploads reach only their
// grants, company uploads reach every member, legacy unstamped rows read as
// company, unknown values fail closed for everyone but the uploader, and the
// same policy gates the blob route and principal-scoped recall.
func TestAssistantFilePerFileACLScopesListsBlobsAndRecall(t *testing.T) {
	ajCookies, joelCookies := setupDriveWaveTest(t)
	aj := accountStore().findUser("aj@shareability.com")
	joel := accountStore().findUser("joel@shareability.com")

	private := uploadDriveFileRow(t, ajCookies, "private-plan.txt", "text/plain", []byte("private runway plan canary"), map[string]string{"visibility": "private"})
	company := uploadDriveFileRow(t, ajCookies, "company-notes.txt", "text/plain", []byte("company offsite canary"), nil)
	people := uploadDriveFileRow(t, ajCookies, "people-brief.txt", "text/plain", []byte("people brief canary"), map[string]string{"visibility": "people"})
	if private.Visibility != fileVisibilityPrivate || company.Visibility != fileVisibilityCompany || people.Visibility != fileVisibilityPeople {
		t.Fatalf("visibility private=%q company=%q people=%q", private.Visibility, company.Visibility, people.Visibility)
	}
	if !private.CanShare || !private.CanDelete {
		t.Fatalf("uploader row=%+v, want canShare+canDelete", private)
	}
	if bad := postFileUploadWithFields(t, ajCookies, "bad.txt", "text/plain", []byte("x"), map[string]string{"visibility": "everyone"}); bad.Code != http.StatusBadRequest {
		t.Fatalf("unknown visibility upload status=%d, want 400", bad.Code)
	}

	joelRows := listDriveFiles(t, joelCookies, "").Files
	if _, visible := driveRowByID(joelRows, private.ID); visible {
		t.Fatal("private upload leaked into another member's list")
	}
	if _, visible := driveRowByID(joelRows, people.ID); visible {
		t.Fatal("people upload without grants leaked into a non-grantee's list")
	}
	companyRow, visible := driveRowByID(joelRows, company.ID)
	if !visible || companyRow.CanShare || companyRow.CanDelete {
		t.Fatalf("company row for non-uploader=%+v visible=%v, want visible without share/delete", companyRow, visible)
	}
	ajRows := listDriveFiles(t, ajCookies, "").Files
	for _, id := range []string{private.ID, company.ID, people.ID} {
		if _, visible := driveRowByID(ajRows, id); !visible {
			t.Fatalf("uploader cannot see own upload %s", id)
		}
	}

	// Blob route: the same policy, per request.
	privateRef, peopleRef := blobRefFromRow(t, private), blobRefFromRow(t, people)
	if blobAuthorized(context.Background(), joel, privateRef) {
		t.Fatal("private blob authorized for a non-uploader")
	}
	if !blobAuthorized(context.Background(), aj, privateRef) {
		t.Fatal("private blob denied to its uploader")
	}
	if blobAuthorized(context.Background(), joel, peopleRef) {
		t.Fatal("people blob authorized before any grant")
	}
	// Recall: the principal-scoped store carries only readable rows.
	recallHas := func(viewer *userAccount, query string, id string) bool {
		for _, match := range kanbanApp.recallStoreForPrincipal(context.Background(), recallPrincipalForUser(viewer)).search(query, 10) {
			if match.Entry.ID == id {
				return true
			}
		}
		return false
	}
	if recallHas(joel, "private runway plan canary", private.ID) {
		t.Fatal("private upload reached another member's recall")
	}
	if !recallHas(aj, "private runway plan canary", private.ID) {
		t.Fatal("private upload missing from its uploader's recall")
	}
	if recallHas(joel, "people brief canary", people.ID) {
		t.Fatal("people upload reached a non-grantee's recall")
	}

	// Grant joel: the people row, its blob and its recall open for him only.
	grant := patchDriveFile(t, ajCookies, fmt.Sprintf(`{"id":%q,"grants":{"add":["Joel@Shareability.com"]}}`, people.ID))
	if grant.Code != http.StatusOK {
		t.Fatalf("grant status=%d body=%s", grant.Code, grant.Body.String())
	}
	joelRows = listDriveFiles(t, joelCookies, "").Files
	peopleRow, visible := driveRowByID(joelRows, people.ID)
	if !visible || len(peopleRow.Grants) != 1 || peopleRow.Grants[0] != "joel@shareability.com" {
		t.Fatalf("granted people row=%+v visible=%v", peopleRow, visible)
	}
	if !blobAuthorized(context.Background(), joel, peopleRef) {
		t.Fatal("people blob denied to a grantee")
	}
	if !recallHas(joel, "people brief canary", people.ID) {
		t.Fatal("granted people upload missing from the grantee's recall")
	}
	tim := accountStore().findUser("tim@shareability.com")
	if blobAuthorized(context.Background(), tim, peopleRef) {
		t.Fatal("people blob authorized for a member without a grant")
	}

	// Legacy rows (no visibility) read as company for everyone; an unknown
	// value fails closed for everyone but the uploader.
	legacy, _, err := kanbanApp.memory.appendEntry(meetingMemoryKindFile, "file-legacy-unstamped", "File legacy.txt uploaded by AJ. legacy canary", map[string]string{
		"name": "legacy.txt", "mime": "text/plain", "size": "13", "uploaderEmail": "aj@shareability.com", "uploaderName": "AJ", "origin": "files", "brainStatus": fileBrainStatusIngested,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := kanbanApp.memory.appendEntry(meetingMemoryKindFile, "file-weird-visibility", "File weird.txt uploaded by AJ.", map[string]string{
		"name": "weird.txt", "uploaderEmail": "aj@shareability.com", "origin": "files", "brainStatus": fileBrainStatusStored, "visibility": "everyone",
	}); err != nil {
		t.Fatal(err)
	}
	joelRows = listDriveFiles(t, joelCookies, "").Files
	if legacyRow, visible := driveRowByID(joelRows, legacy.ID); !visible || legacyRow.Visibility != fileVisibilityCompany {
		t.Fatalf("legacy unstamped row=%+v visible=%v, want company-visible", legacyRow, visible)
	}
	if _, visible := driveRowByID(joelRows, "file-weird-visibility"); visible {
		t.Fatal("unknown visibility value must fail closed for other members")
	}

	// Migration: stamps exactly the unstamped row, then is a no-op.
	if stamped := kanbanApp.migrateFileVisibilityDefaults(); stamped != 1 {
		t.Fatalf("first migration stamped %d, want 1", stamped)
	}
	if entry, ok := kanbanApp.memory.entryByKindAndID(meetingMemoryKindFile, legacy.ID); !ok || entry.Metadata["visibility"] != fileVisibilityCompany {
		t.Fatalf("legacy row after migration=%+v ok=%v", entry.Metadata, ok)
	}
	if stamped := kanbanApp.migrateFileVisibilityDefaults(); stamped != 0 {
		t.Fatalf("second migration stamped %d, want 0", stamped)
	}
	if entry, _ := kanbanApp.memory.entryByKindAndID(meetingMemoryKindFile, private.ID); entry.Metadata["visibility"] != fileVisibilityPrivate {
		t.Fatalf("migration rewrote a stamped row: %q", entry.Metadata["visibility"])
	}
	if _, visible := driveRowByID(listDriveFiles(t, joelCookies, "").Files, legacy.ID); !visible {
		t.Fatal("migrated legacy row must stay company-visible")
	}
}

// D2: PATCH {id, visibility?, grants?} is the uploader's alone (403 for a
// reader, 404 for a non-reader); grants must be registered accounts.
func TestAssistantFilePatchManagesAccessForUploaderOnly(t *testing.T) {
	ajCookies, joelCookies := setupDriveWaveTest(t)
	company := uploadDriveFileRow(t, ajCookies, "shared.txt", "text/plain", []byte("shared body"), nil)
	private := uploadDriveFileRow(t, ajCookies, "mine.txt", "text/plain", []byte("mine body"), map[string]string{"visibility": "private"})

	if response := patchDriveFile(t, joelCookies, fmt.Sprintf(`{"id":%q,"visibility":"private"}`, company.ID)); response.Code != http.StatusForbidden {
		t.Fatalf("reader manage-access status=%d body=%s, want 403", response.Code, response.Body.String())
	}
	if response := patchDriveFile(t, joelCookies, fmt.Sprintf(`{"id":%q,"visibility":"company"}`, private.ID)); response.Code != http.StatusNotFound {
		t.Fatalf("non-reader manage-access status=%d, want 404", response.Code)
	}
	if entry, _ := kanbanApp.memory.entryByKindAndID(meetingMemoryKindFile, company.ID); entry.Metadata["visibility"] != fileVisibilityCompany {
		t.Fatalf("forbidden PATCH changed visibility to %q", entry.Metadata["visibility"])
	}
	if response := patchDriveFile(t, ajCookies, fmt.Sprintf(`{"id":%q,"grants":{"add":["nobody@example.com"]}}`, company.ID)); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "not registered") {
		t.Fatalf("unregistered grant status=%d body=%s, want 400", response.Code, response.Body.String())
	}
	if response := patchDriveFile(t, ajCookies, fmt.Sprintf(`{"id":%q,"visibility":"friends"}`, company.ID)); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid visibility status=%d, want 400", response.Code)
	}

	response := patchDriveFile(t, ajCookies, fmt.Sprintf(`{"id":%q,"visibility":"people","grants":{"add":["joel@shareability.com","tim@shareability.com","aj@shareability.com"]}}`, company.ID))
	if response.Code != http.StatusOK {
		t.Fatalf("manage-access status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		OK   bool                `json:"ok"`
		File assistantFileRecord `json:"file"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || payload.File.ID != company.ID || payload.File.Visibility != fileVisibilityPeople || strings.Join(payload.File.Grants, ",") != "joel@shareability.com,tim@shareability.com" {
		t.Fatalf("manage-access row=%+v, want people with joel+tim (uploader implicit)", payload.File)
	}
	response = patchDriveFile(t, ajCookies, fmt.Sprintf(`{"id":%q,"grants":{"remove":["tim@shareability.com"]}}`, company.ID))
	if response.Code != http.StatusOK {
		t.Fatalf("remove grant status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Join(payload.File.Grants, ",") != "joel@shareability.com" {
		t.Fatalf("after remove grants=%v, want joel only", payload.File.Grants)
	}
	if _, visible := driveRowByID(listDriveFiles(t, loginAs(t, "tim@shareability.com", "B0NFIRE!"), "").Files, company.ID); visible {
		t.Fatal("removed grantee still sees the people row")
	}
	// A rename by a reader is still a 404 (unchanged write authority).
	if response := patchDriveFile(t, joelCookies, fmt.Sprintf(`{"id":%q,"name":"renamed.txt"}`, private.ID)); response.Code != http.StatusNotFound {
		t.Fatalf("reader rename status=%d, want 404", response.Code)
	}
}

// D5: a same-name re-upload into the same folder by the same uploader chains
// versionOf/version, the default list shows only the newest, and
// ?versionsOf= lists the chain newest first with older rows superseded.
func TestAssistantFileVersioningChainsSameNameUploads(t *testing.T) {
	ajCookies, joelCookies := setupDriveWaveTest(t)
	first := uploadDriveFileRow(t, ajCookies, "deck.pdf", "application/pdf", []byte("%PDF-1.7 v1"), map[string]string{"visibility": "private"})
	second := uploadDriveFileRow(t, ajCookies, "deck.pdf", "application/pdf", []byte("%PDF-1.7 v2"), nil)
	if second.VersionOf != first.ID || second.Version != 2 || first.Version != 1 || first.VersionOf != "" {
		t.Fatalf("chain first=%+v second=%+v", first, second)
	}
	if second.Visibility != fileVisibilityPrivate {
		t.Fatalf("new version visibility=%q, want inherited private", second.Visibility)
	}
	rows := listDriveFiles(t, ajCookies, "").Files
	if _, visible := driveRowByID(rows, first.ID); visible {
		t.Fatal("superseded version still in the default list")
	}
	newest, visible := driveRowByID(rows, second.ID)
	if !visible || newest.Superseded {
		t.Fatalf("newest version row=%+v visible=%v", newest, visible)
	}
	versions := listDriveFiles(t, ajCookies, "versionsOf="+url.QueryEscape(first.ID)).Files
	if len(versions) != 2 || versions[0].ID != second.ID || versions[1].ID != first.ID || !versions[1].Superseded || versions[0].Superseded {
		t.Fatalf("versions=%+v, want [v2, v1(superseded)]", versions)
	}
	if fromNewest := listDriveFiles(t, ajCookies, "versionsOf="+url.QueryEscape(second.ID)).Files; len(fromNewest) != 2 || fromNewest[0].ID != second.ID {
		t.Fatalf("versions from newest=%+v", fromNewest)
	}
	// A private chain is invisible to others, root and all.
	if others := listDriveFiles(t, joelCookies, "versionsOf="+url.QueryEscape(first.ID)).Files; len(others) != 0 {
		t.Fatalf("private version chain leaked: %+v", others)
	}
	// Another uploader's same name starts its own chain; a different folder does too.
	joelDeck := uploadDriveFileRow(t, joelCookies, "deck.pdf", "application/pdf", []byte("%PDF-1.7 joel"), nil)
	if joelDeck.VersionOf != "" || joelDeck.Version != 1 {
		t.Fatalf("other uploader chained onto a foreign file: %+v", joelDeck)
	}
	folder, err := createFileFolder("Decks", "aj@shareability.com")
	if err != nil {
		t.Fatal(err)
	}
	inFolder := uploadDriveFileRow(t, ajCookies, "deck.pdf", "application/pdf", []byte("%PDF-1.7 folder"), map[string]string{"folderId": folder.ID})
	if inFolder.VersionOf != "" || inFolder.Version != 1 || inFolder.FolderID != folder.ID {
		t.Fatalf("folder upload=%+v, want a fresh chain filed under %s", inFolder, folder.ID)
	}
	if bad := postFileUploadWithFields(t, ajCookies, "deck.pdf", "application/pdf", []byte("%PDF-1.7 x"), map[string]string{"folderId": "folder-missing"}); bad.Code != http.StatusNotFound {
		t.Fatalf("missing folder upload status=%d, want 404", bad.Code)
	}
}

// D6: per-viewer star; DELETE is a soft delete that hides the row from lists
// and recall but keeps the blob; restore returns it; the daily sweep purges
// only rows trashed longer than 30 days.
func TestAssistantFileStarTrashRestoreAndSweep(t *testing.T) {
	ajCookies, joelCookies := setupDriveWaveTest(t)
	aj := accountStore().findUser("aj@shareability.com")
	keep := uploadDriveFileRow(t, ajCookies, "keep.txt", "text/plain", []byte("keep canary body"), nil)
	trash := uploadDriveFileRow(t, ajCookies, "trash.txt", "text/plain", []byte("trash canary body"), nil)

	star := patchDriveFile(t, joelCookies, fmt.Sprintf(`{"id":%q,"starred":true}`, keep.ID))
	if star.Code != http.StatusOK || !strings.Contains(star.Body.String(), `"starred":true`) {
		t.Fatalf("star status=%d body=%s", star.Code, star.Body.String())
	}
	if row, _ := driveRowByID(listDriveFiles(t, joelCookies, "").Files, keep.ID); !row.Starred {
		t.Fatal("star missing from the starring viewer's list")
	}
	if row, _ := driveRowByID(listDriveFiles(t, ajCookies, "").Files, keep.ID); row.Starred {
		t.Fatal("one viewer's star leaked into another viewer's row")
	}

	if response := deleteDriveFileRequest(t, ajCookies, trash.ID); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"trashed"`) {
		t.Fatalf("trash status=%d body=%s", response.Code, response.Body.String())
	}
	if _, visible := driveRowByID(listDriveFiles(t, ajCookies, "").Files, trash.ID); visible {
		t.Fatal("trashed row still in the live list")
	}
	trashed := listDriveFiles(t, ajCookies, "scope=trash")
	trashedRow, visible := driveRowByID(trashed.Files, trash.ID)
	if trashed.Scope != "trash" || !visible || trashedRow.DeletedAt == "" {
		t.Fatalf("trash scope=%+v row=%+v visible=%v", trashed.Scope, trashedRow, visible)
	}
	if _, visible := driveRowByID(listDriveFiles(t, joelCookies, "scope=trash").Files, trash.ID); visible {
		t.Fatal("another member sees the uploader's trash")
	}
	rawRecallHas := func(id string) bool {
		for _, match := range kanbanApp.memory.search("trash canary body", 10) {
			if match.Entry.ID == id {
				return true
			}
		}
		return false
	}
	if rawRecallHas(trash.ID) {
		t.Fatal("trashed upload still in recall")
	}
	trashRef := blobRefFromRow(t, trash)
	if _, _, err := getBlob(trashRef); err != nil {
		t.Fatalf("trashed blob must be retained: %v", err)
	}
	if !blobAuthorized(context.Background(), aj, trashRef) {
		t.Fatal("uploader cannot open a trashed file's bytes")
	}
	if blobAuthorized(context.Background(), accountStore().findUser("joel@shareability.com"), trashRef) {
		t.Fatal("trashed company file bytes still served to other members")
	}
	if _, _, _, ok := kanbanApp.assistantFileAttachmentSource(context.Background(), aj, trash.ID); ok {
		t.Fatal("trashed row still resolves as an attachment source")
	}
	if response := deleteDriveFileRequest(t, ajCookies, trash.ID); response.Code != http.StatusNotFound {
		t.Fatalf("second delete status=%d, want 404", response.Code)
	}

	if response := postDriveJSON(t, assistantFileRestoreHandler, "/assistant/files/restore", joelCookies, fmt.Sprintf(`{"id":%q}`, trash.ID)); response.Code != http.StatusNotFound {
		t.Fatalf("other member restore status=%d, want 404", response.Code)
	}
	restore := postDriveJSON(t, assistantFileRestoreHandler, "/assistant/files/restore", ajCookies, fmt.Sprintf(`{"id":%q}`, trash.ID))
	if restore.Code != http.StatusOK {
		t.Fatalf("restore status=%d body=%s", restore.Code, restore.Body.String())
	}
	if restored, visible := driveRowByID(listDriveFiles(t, ajCookies, "").Files, trash.ID); !visible || restored.DeletedAt != "" {
		t.Fatalf("restored row=%+v visible=%v", restored, visible)
	}
	if !rawRecallHas(trash.ID) {
		t.Fatal("restored upload missing from recall")
	}
	if response := postDriveJSON(t, assistantFileRestoreHandler, "/assistant/files/restore", ajCookies, fmt.Sprintf(`{"id":%q}`, trash.ID)); response.Code != http.StatusConflict {
		t.Fatalf("restore of a live row status=%d, want 409", response.Code)
	}

	// Sweep: 31-day-old trash is purged, fresh trash and live rows survive.
	fresh := uploadDriveFileRow(t, ajCookies, "fresh-trash.txt", "text/plain", []byte("fresh trash body"), nil)
	for _, id := range []string{trash.ID, fresh.ID} {
		if response := deleteDriveFileRequest(t, ajCookies, id); response.Code != http.StatusOK {
			t.Fatalf("trash %s status=%d", id, response.Code)
		}
	}
	old, _ := kanbanApp.memory.entryByKindAndID(meetingMemoryKindFile, trash.ID)
	if _, _, err := kanbanApp.memory.updateEntryWithMetadata(meetingMemoryKindFile, trash.ID, old.Text, map[string]string{
		"deletedAt": time.Now().UTC().Add(-31 * 24 * time.Hour).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if purged := kanbanApp.sweepFileTrashOnce(time.Now().UTC()); purged != 1 {
		t.Fatalf("sweep purged %d, want 1", purged)
	}
	if _, ok := kanbanApp.memory.entryByKindAndID(meetingMemoryKindFile, trash.ID); ok {
		t.Fatal("31-day-old trash survived the sweep")
	}
	if _, ok := kanbanApp.memory.entryByKindAndID(meetingMemoryKindFile, fresh.ID); !ok {
		t.Fatal("fresh trash was purged early")
	}
	if _, visible := driveRowByID(listDriveFiles(t, ajCookies, "").Files, keep.ID); !visible {
		t.Fatal("live row lost during the sweep")
	}
	if purged := kanbanApp.sweepFileTrashOnce(time.Now().UTC()); purged != 0 {
		t.Fatalf("second sweep purged %d, want 0", purged)
	}
}

// D7: ?q= matches ingested body text through the principal-scoped memory
// search, so a body-only hit surfaces for an authorized viewer and never for
// an unauthorized one.
func TestAssistantFilesContentSearchIsACLScoped(t *testing.T) {
	ajCookies, joelCookies := setupDriveWaveTest(t)
	memo := uploadDriveFileRow(t, ajCookies, "q3-memo.txt", "text/plain", []byte("the runway extends through november"), map[string]string{"visibility": "private"})
	notes := uploadDriveFileRow(t, ajCookies, "team-notes.txt", "text/plain", []byte("offsite planning for november"), nil)
	uploadDriveFileRow(t, ajCookies, "unrelated.txt", "text/plain", []byte("nothing to see here"), nil)

	ajHits := listDriveFiles(t, ajCookies, "q=november").Files
	if _, hit := driveRowByID(ajHits, memo.ID); !hit {
		t.Fatalf("body-only match missing for the uploader: %+v", ajHits)
	}
	if _, hit := driveRowByID(ajHits, notes.ID); !hit || len(ajHits) != 2 {
		t.Fatalf("uploader search hits=%+v, want memo+notes", ajHits)
	}
	joelHits := listDriveFiles(t, joelCookies, "q=november").Files
	if _, hit := driveRowByID(joelHits, memo.ID); hit {
		t.Fatalf("private body text matched for an unauthorized viewer: %+v", joelHits)
	}
	if _, hit := driveRowByID(joelHits, notes.ID); !hit || len(joelHits) != 1 {
		t.Fatalf("company body match hits=%+v, want notes only", joelHits)
	}
	if byName := listDriveFiles(t, joelCookies, "q=unrelated").Files; len(byName) != 1 || byName[0].Name != "unrelated.txt" {
		t.Fatalf("name match hits=%+v", byName)
	}
	if byUploader := listDriveFiles(t, joelCookies, "q=aj@shareability").Files; len(byUploader) != 2 {
		t.Fatalf("uploader match hits=%d, want the two company rows", len(byUploader))
	}
}

// D9: usage reports the Drive's stored bytes against DRIVE_QUOTA_BYTES and an
// upload that would exceed it is refused with 413 before any bytes land.
func TestAssistantFileUsageAndQuota(t *testing.T) {
	ajCookies, _ := setupDriveWaveTest(t)
	t.Setenv("DRIVE_QUOTA_BYTES", "100")
	usageRequest := func() map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/assistant/files/usage", nil)
		for _, cookie := range ajCookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantFileUsageHandler(recorder, req)
		if recorder.Code != http.StatusOK {
			t.Fatalf("usage status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	if usage := usageRequest(); usage["bytesUsed"] != float64(0) || usage["fileCount"] != float64(0) || usage["quotaBytes"] != float64(100) {
		t.Fatalf("empty usage=%v", usage)
	}
	uploadDriveFileRow(t, ajCookies, "sixty.bin", "application/octet-stream", bytes.Repeat([]byte("a"), 60), nil)
	if usage := usageRequest(); usage["bytesUsed"] != float64(60) || usage["fileCount"] != float64(1) {
		t.Fatalf("usage after 60 bytes=%v", usage)
	}
	over := postFileUploadWithFields(t, ajCookies, "fifty.bin", "application/octet-stream", bytes.Repeat([]byte("b"), 50), nil)
	if over.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-quota status=%d body=%s, want 413", over.Code, over.Body.String())
	}
	var refusal map[string]any
	if err := json.Unmarshal(over.Body.Bytes(), &refusal); err != nil {
		t.Fatal(err)
	}
	if refusal["error"] == nil || refusal["bytesUsed"] != float64(60) || refusal["quotaBytes"] != float64(100) {
		t.Fatalf("refusal=%v, want error+bytesUsed+quotaBytes", refusal)
	}
	if usage := usageRequest(); usage["bytesUsed"] != float64(60) || usage["fileCount"] != float64(1) {
		t.Fatalf("refused upload changed usage: %v", usage)
	}
	uploadDriveFileRow(t, ajCookies, "forty.bin", "application/octet-stream", bytes.Repeat([]byte("c"), 40), nil)
	if usage := usageRequest(); usage["bytesUsed"] != float64(100) || usage["fileCount"] != float64(2) {
		t.Fatalf("usage at quota=%v", usage)
	}
}

// D8: client-declared Drive refs on a work request reach the launched goal
// only after re-authorizing for the requester; an unauthorized ref refuses
// the launch with a 403-class error and launches nothing.
func TestConversationWorkLaunchAcceptsAuthorizedDriveContextRefs(t *testing.T) {
	ajCookies, joelCookies := setupDriveWaveTest(t)
	app := kanbanApp
	// A configured worker is what lets a registry-tool launch mint its goal;
	// the async starter is stubbed so nothing actually runs.
	app.apiKey = "openai-router-test"
	aj := accountStore().findUser("aj@shareability.com")
	brief := uploadDriveFileRow(t, ajCookies, "brief.txt", "text/plain", []byte("launch brief body for the one pager"), nil)
	secret := uploadDriveFileRow(t, joelCookies, "secret.txt", "text/plain", []byte("joel private notes"), map[string]string{"visibility": "private"})

	var launches int
	previousStarter := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) { launches++ }
	t.Cleanup(func() { startGoalThreadAsync = previousStarter })

	launch := func(refs []workRequestContextRef, text string) (map[string]any, scoutChatThreadRecord, error) {
		thread, err := app.createScoutChatThread(aj.Email, aj.Name, "Drive refs "+text, scoutChatVisibilityPrivate)
		if err != nil {
			t.Fatal(err)
		}
		// A private launch binds to the journaled turn operation the chat door
		// mints; mirror that receipt here the way the crash-reconciliation test does.
		operation := conversationTurnOperation{ID: "conversation-drive-ref-" + sha256Hex([]byte(text))[:16], BodyDigest: sha256Hex([]byte("drive ref body " + text))}
		message := scoutChatMessageRecord{
			ID: "drive-ref-" + sha256Hex([]byte(text))[:12], Kind: "message", Role: "user", Text: text,
			AuthorName: aj.Name, AuthorEmail: aj.Email, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			SourceOperationID: operation.ID, SourceOperationDigest: operation.BodyDigest,
		}
		if _, err := app.commitScoutChatThreadMessages(aj.Email, thread.ID, message); err != nil {
			t.Fatal(err)
		}
		ctx := withWorkRequestContextRefs(withConversationTurnOperation(context.Background(), operation), refs)
		response, err := app.startConversationPrivateWork(ctx, aj, thread, message, conversationWorkDecision{
			Kind: conversationWorkRegistryTool, ToolID: "one_pager", Objective: text,
		}, "", proposalSourceChatRouter, func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
			return app.commitScoutChatThreadMessages(aj.Email, thread.ID, messages...)
		})
		return response, thread, err
	}

	response, _, err := launch([]workRequestContextRef{{Ref: assistantFileContextRef(brief.ID)}}, "Write the one pager from the brief")
	if err != nil {
		t.Fatalf("authorized launch err=%v", err)
	}
	launched, ok := response["agentThread"].(scoutAgentThread)
	if !ok || launched.ID == "" {
		t.Fatalf("launched=%#v", response["agentThread"])
	}
	refs := decodeAssistantContextRefs(launched.Artifact.Metadata["contextRefs"])
	found := false
	for _, ref := range refs {
		if ref == assistantFileContextRef(brief.ID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("launched goal contextRefs=%v, want %s", refs, assistantFileContextRef(brief.ID))
	}

	_, thread, err := launch([]workRequestContextRef{{Ref: assistantFileContextRef(secret.ID)}}, "Summarize the secret notes")
	if !errors.Is(err, errWorkRequestContextRefForbidden) || workRequestContextRefStatus(err) != http.StatusForbidden {
		t.Fatalf("unauthorized ref err=%v status=%d, want forbidden/403", err, workRequestContextRefStatus(err))
	}
	current, _, readErr := app.scoutChatThreadByID(aj.Email, thread.ID)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, message := range current.Messages {
		if message.Thread != nil {
			t.Fatalf("unauthorized ref still produced a work card: %+v", message)
		}
	}
	if _, _, err := launch([]workRequestContextRef{{Ref: assistantFileContextRef(brief.ID), SourceRevision: "stale-revision"}}, "Stale revision"); !errors.Is(err, errWorkRequestContextRefForbidden) {
		t.Fatalf("stale revision err=%v, want forbidden", err)
	}
	if _, _, err := launch([]workRequestContextRef{{Ref: "artifact|" + brief.ID}}, "Non drive ref"); !errors.Is(err, errWorkRequestContextRefForbidden) {
		t.Fatalf("non-Drive ref err=%v, want forbidden", err)
	}
	if _, _, err := launch([]workRequestContextRef{{Ref: assistantFileContextRef(secret.ID)}, {Ref: assistantFileContextRef(brief.ID)}}, "Mixed refs"); !errors.Is(err, errWorkRequestContextRefForbidden) {
		t.Fatalf("mixed refs err=%v, want forbidden (one refusal refuses all)", err)
	}
	if launches != 1 {
		t.Fatalf("launches=%d, want exactly the one authorized launch", launches)
	}
	if decoded, err := decodeWorkRequestContextRefs([]any{"file|abc", map[string]any{"ref": "file|def", "sourceId": "s1", "sourceRevision": "r1"}}); err != nil || len(decoded) != 2 || decoded[1].SourceID != "s1" {
		t.Fatalf("decode=%+v err=%v", decoded, err)
	}
	if _, err := decodeWorkRequestContextRefs("file|abc"); err == nil {
		t.Fatal("non-list contextRefs must be rejected")
	}
}

// D1 migration cost: N unstamped rows are stamped in ONE locked pass with a
// single JSONL rewrite, stamped rows are untouched, and a second run neither
// stamps nor rewrites.
func TestMigrateFileVisibilityDefaultsBatchesOneRewrite(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	app := kanbanApp

	const unstamped = 5
	ids := make([]string, 0, unstamped)
	for index := 0; index < unstamped; index++ {
		id := fmt.Sprintf("file-legacy-batch-%d", index)
		if _, _, err := app.memory.appendEntry(meetingMemoryKindFile, id, fmt.Sprintf("File legacy-%d.txt uploaded by AJ.", index), map[string]string{
			"name": fmt.Sprintf("legacy-%d.txt", index), "uploaderEmail": "aj@shareability.com", "origin": "files", "brainStatus": fileBrainStatusStored,
		}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if _, _, err := app.memory.appendEntry(meetingMemoryKindFile, "file-already-private", "File mine.txt uploaded by AJ.", map[string]string{
		"name": "mine.txt", "uploaderEmail": "aj@shareability.com", "origin": "files", "brainStatus": fileBrainStatusStored, "visibility": fileVisibilityPrivate,
	}); err != nil {
		t.Fatal(err)
	}

	rewrites := 0
	previousProbe := memoryRewriteProbe
	memoryRewriteProbe = func() { rewrites++ }
	t.Cleanup(func() { memoryRewriteProbe = previousProbe })

	if stamped := app.migrateFileVisibilityDefaults(); stamped != unstamped {
		t.Fatalf("first migration stamped %d, want %d", stamped, unstamped)
	}
	if rewrites != 1 {
		t.Fatalf("first migration rewrote the store %d times, want exactly 1", rewrites)
	}
	for _, id := range ids {
		if entry, ok := app.memory.entryByKindAndID(meetingMemoryKindFile, id); !ok || entry.Metadata["visibility"] != fileVisibilityCompany {
			t.Fatalf("row %s after migration=%v ok=%v, want company", id, entry.Metadata, ok)
		}
	}
	if entry, _ := app.memory.entryByKindAndID(meetingMemoryKindFile, "file-already-private"); entry.Metadata["visibility"] != fileVisibilityPrivate {
		t.Fatalf("stamped row rewritten to %q", entry.Metadata["visibility"])
	}
	if stamped := app.migrateFileVisibilityDefaults(); stamped != 0 || rewrites != 1 {
		t.Fatalf("second migration stamped %d rewrites=%d, want 0 and still 1", stamped, rewrites)
	}
	// The stamp is durable: a fresh store from the same file carries it.
	reloaded, err := newMeetingMemoryStore(app.memory.path)
	if err != nil {
		t.Fatal(err)
	}
	if entry, ok := reloaded.entryByKindAndID(meetingMemoryKindFile, ids[0]); !ok || entry.Metadata["visibility"] != fileVisibilityCompany {
		t.Fatalf("reloaded row=%v ok=%v, want the persisted company stamp", entry.Metadata, ok)
	}
}

// D8 door: POST /assistant/chat-threads/{id}/messages accepts contextRefs; an
// authorized Drive ref reaches the launched goal, an unauthorized one is a 403
// with nothing committed or launched, and a malformed field is a 400.
func TestScoutChatSendAcceptsDriveContextRefsAndRefusesUnauthorized(t *testing.T) {
	ajCookies, joelCookies := setupDriveWaveTest(t)
	kanbanApp.apiKey = "openai-router-test"
	t.Setenv("OPENAI_API_KEY", "openai-router-test")
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_route" {
			t.Fatalf("unexpected workflow %q", request.Workflow)
		}
		return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
			Outcome: string(conversationIntentStartPrivateWork), Route: "tool_run", ToolID: "deep_research", Objective: "Map the fintech landscape",
		}), nil
	})
	launches := 0
	previousRunner := startGoalThreadAsync
	startGoalThreadAsync = func(_ *kanbanBoardApp, _ string) { launches++ }
	t.Cleanup(func() { startGoalThreadAsync = previousRunner })

	brief := uploadDriveFileRow(t, ajCookies, "fintech-brief.txt", "text/plain", []byte("fintech landscape brief body"), nil)
	secret := uploadDriveFileRow(t, joelCookies, "joel-secret.txt", "text/plain", []byte("joel private notes"), map[string]string{"visibility": "private"})

	send := func(threadID string, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+threadID+"/messages", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range ajCookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantChatThreadHandler(recorder, req)
		return recorder
	}

	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Drive refs door", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	rec := send(thread.ID, fmt.Sprintf(`{"text":"map the fintech landscape","operationId":"drive-ref-door-operation-0001","contextRefs":[{"ref":%q}]}`, assistantFileContextRef(brief.ID)))
	if rec.Code != http.StatusOK {
		t.Fatalf("authorized send status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		AgentThread scoutAgentThread   `json:"agentThread"`
		Artifact    meetingMemoryEntry `json:"artifact"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.AgentThread.ID == "" {
		t.Fatalf("body=%s, want a launched agentThread", rec.Body.String())
	}
	found := false
	for _, ref := range decodeAssistantContextRefs(payload.Artifact.Metadata["contextRefs"]) {
		if ref == assistantFileContextRef(brief.ID) {
			found = true
		}
	}
	if !found || launches != 1 {
		t.Fatalf("launched contextRefs=%q launches=%d, want %s reached the goal exactly once", payload.Artifact.Metadata["contextRefs"], launches, assistantFileContextRef(brief.ID))
	}

	denied, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Drive refs denied", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	rec = send(denied.ID, fmt.Sprintf(`{"text":"summarize the secret notes","operationId":"drive-ref-door-operation-0002","contextRefs":[{"ref":%q}]}`, assistantFileContextRef(secret.ID)))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "not readable") {
		t.Fatalf("unauthorized send status=%d body=%s, want 403", rec.Code, rec.Body.String())
	}
	if launches != 1 {
		t.Fatalf("unauthorized ref launched work: launches=%d", launches)
	}
	current, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", denied.ID)
	if err != nil || len(current.Messages) != 0 {
		t.Fatalf("refused turn left messages behind: %+v err=%v", current.Messages, err)
	}
	if rec := send(denied.ID, `{"text":"hello","operationId":"drive-ref-door-operation-0003","contextRefs":"file|nope"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed contextRefs status=%d, want 400", rec.Code)
	}
}

// D5 reviewer finding: a trashed MIDDLE version must not split the chain.
// Edges bridge trashed nodes in both directions; the trashed row itself is
// projected only for its uploader.
func TestAssistantFileVersionsBridgeTrashedMiddleVersion(t *testing.T) {
	ajCookies, joelCookies := setupDriveWaveTest(t)
	v1 := uploadDriveFileRow(t, ajCookies, "plan.pdf", "application/pdf", []byte("%PDF-1.7 one"), map[string]string{"visibility": "people"})
	if response := patchDriveFile(t, ajCookies, fmt.Sprintf(`{"id":%q,"grants":{"add":["joel@shareability.com"]}}`, v1.ID)); response.Code != http.StatusOK {
		t.Fatalf("grant status=%d body=%s", response.Code, response.Body.String())
	}
	v2 := uploadDriveFileRow(t, ajCookies, "plan.pdf", "application/pdf", []byte("%PDF-1.7 two"), nil)
	v3 := uploadDriveFileRow(t, ajCookies, "plan.pdf", "application/pdf", []byte("%PDF-1.7 three"), nil)
	if v2.VersionOf != v1.ID || v3.VersionOf != v2.ID || v3.Version != 3 {
		t.Fatalf("chain v2=%+v v3=%+v", v2, v3)
	}
	// Trash the middle version directly (the live list only exposes the newest).
	middle, _ := kanbanApp.memory.entryByKindAndID(meetingMemoryKindFile, v2.ID)
	if _, _, err := kanbanApp.memory.updateEntryWithMetadata(meetingMemoryKindFile, v2.ID, middle.Text, map[string]string{
		"deletedAt": time.Now().UTC().Format(time.RFC3339Nano), "deletedBy": "aj@shareability.com", relevanceMetadataKey: relevanceExpired,
	}); err != nil {
		t.Fatal(err)
	}
	ids := func(rows []assistantFileRecord) string {
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, row.ID)
		}
		return strings.Join(out, ",")
	}
	for _, anchor := range []string{v1.ID, v3.ID} {
		uploaderChain := listDriveFiles(t, ajCookies, "versionsOf="+url.QueryEscape(anchor)).Files
		if ids(uploaderChain) != strings.Join([]string{v3.ID, v2.ID, v1.ID}, ",") || uploaderChain[1].DeletedAt == "" {
			t.Fatalf("uploader chain from %s=%s (deletedAt=%q), want v3,v2(trashed),v1", anchor, ids(uploaderChain), uploaderChain[1].DeletedAt)
		}
		granteeChain := listDriveFiles(t, joelCookies, "versionsOf="+url.QueryEscape(anchor)).Files
		if ids(granteeChain) != strings.Join([]string{v3.ID, v1.ID}, ",") {
			t.Fatalf("grantee chain from %s=%s, want v3,v1 bridging the trashed v2", anchor, ids(granteeChain))
		}
	}
	// The trashed anchor itself is the uploader's alone.
	if chain := listDriveFiles(t, joelCookies, "versionsOf="+url.QueryEscape(v2.ID)).Files; len(chain) != 0 {
		t.Fatalf("grantee walked from a trashed anchor: %s", ids(chain))
	}
	if chain := listDriveFiles(t, ajCookies, "versionsOf="+url.QueryEscape(v2.ID)).Files; ids(chain) != strings.Join([]string{v3.ID, v2.ID, v1.ID}, ",") {
		t.Fatalf("uploader chain from trashed anchor=%s", ids(chain))
	}
	live := listDriveFiles(t, joelCookies, "").Files
	if _, visible := driveRowByID(live, v3.ID); !visible {
		t.Fatal("newest version missing from the live list")
	}
	for _, id := range []string{v1.ID, v2.ID} {
		if _, visible := driveRowByID(live, id); visible {
			t.Fatalf("older version %s leaked into the live list", id)
		}
	}
}

// D6/D9 reviewer finding: POST /assistant/files/trash/empty hard-deletes the
// caller's own trashed rows now, reports purged + freedBytes, drops usage, and
// never touches another member's trash or any live row.
func TestAssistantFileEmptyTrashPurgesOnlyCallersRows(t *testing.T) {
	ajCookies, joelCookies := setupDriveWaveTest(t)
	keep := uploadDriveFileRow(t, ajCookies, "keep.bin", "application/octet-stream", bytes.Repeat([]byte("k"), 30), nil)
	first := uploadDriveFileRow(t, ajCookies, "one.bin", "application/octet-stream", bytes.Repeat([]byte("a"), 40), nil)
	second := uploadDriveFileRow(t, ajCookies, "two.bin", "application/octet-stream", bytes.Repeat([]byte("b"), 50), nil)
	joels := uploadDriveFileRow(t, joelCookies, "joel.bin", "application/octet-stream", bytes.Repeat([]byte("j"), 60), nil)
	for _, target := range []struct {
		cookies []*http.Cookie
		id      string
	}{{ajCookies, first.ID}, {ajCookies, second.ID}, {joelCookies, joels.ID}} {
		if response := deleteDriveFileRequest(t, target.cookies, target.id); response.Code != http.StatusOK {
			t.Fatalf("trash %s status=%d", target.id, response.Code)
		}
	}
	if usage := kanbanApp.driveUsageForPrincipal(context.Background()); usage.BytesUsed != 180 {
		t.Fatalf("usage before empty=%+v, want trash still counted (180)", usage)
	}
	empty := postDriveJSON(t, assistantFileEmptyTrashHandler, "/assistant/files/trash/empty", ajCookies, `{}`)
	if empty.Code != http.StatusOK {
		t.Fatalf("empty trash status=%d body=%s", empty.Code, empty.Body.String())
	}
	var receipt struct {
		OK         bool  `json:"ok"`
		Purged     int   `json:"purged"`
		FreedBytes int64 `json:"freedBytes"`
	}
	if err := json.Unmarshal(empty.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if !receipt.OK || receipt.Purged != 2 || receipt.FreedBytes != 90 {
		t.Fatalf("receipt=%+v, want purged 2 freeing 90 bytes", receipt)
	}
	for _, id := range []string{first.ID, second.ID} {
		if _, ok := kanbanApp.memory.entryByKindAndID(meetingMemoryKindFile, id); ok {
			t.Fatalf("emptied row %s still in memory", id)
		}
	}
	if _, ok := kanbanApp.memory.entryByKindAndID(meetingMemoryKindFile, joels.ID); !ok {
		t.Fatal("another member's trash was emptied")
	}
	if _, visible := driveRowByID(listDriveFiles(t, ajCookies, "").Files, keep.ID); !visible {
		t.Fatal("live row lost while emptying trash")
	}
	// keep.bin (30, live) + joel's trashed row (60, still retained) remain; only
	// keep.bin is a live row.
	if usage := kanbanApp.driveUsageForPrincipal(context.Background()); usage.BytesUsed != 90 || usage.FileCount != 1 {
		t.Fatalf("usage after empty=%+v, want 90 bytes / 1 live row", usage)
	}
	if trashed := listDriveFiles(t, ajCookies, "scope=trash").Files; len(trashed) != 0 {
		t.Fatalf("trash scope after empty=%+v, want empty", trashed)
	}
	if _, visible := driveRowByID(listDriveFiles(t, joelCookies, "scope=trash").Files, joels.ID); !visible {
		t.Fatal("another member's trashed row disappeared from their trash")
	}
	empty = postDriveJSON(t, assistantFileEmptyTrashHandler, "/assistant/files/trash/empty", ajCookies, `{}`)
	if err := json.Unmarshal(empty.Body.Bytes(), &receipt); err != nil || receipt.Purged != 0 || receipt.FreedBytes != 0 {
		t.Fatalf("second empty=%+v err=%v, want nothing to purge", receipt, err)
	}
}
