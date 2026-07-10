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
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"path/filepath"
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
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")

	const transcription = "Deck claims: $2M ARR, 40% MoM growth, pilot with StationTenn."
	deriveCalls := 0
	swapAnthropicTextResponder(t, func(_ context.Context, _ string, request anthropicTextRequest) (string, error) {
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

func TestAssistantFilesListsChatAttachmentsWithVisibility(t *testing.T) {
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
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", private.ID, scoutChatMessageRecord{
		ID:          "msg-private-1",
		Kind:        "message",
		Role:        "user",
		Text:        "look at this",
		CreatedAt:   time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano),
		AuthorName:  "AJ",
		AuthorEmail: "aj@shareability.com",
		Files: []scoutChatFileAttachment{
			{Name: "deck.png", Kind: "png", Size: 12, Ref: ref, Mime: "image/png", Text: "derived facts"},
		},
	}); err != nil {
		t.Fatalf("commit private message: %v", err)
	}

	channel, err := app.createScoutChatThread("tom@shareability.com", "Tom", "standup", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if _, err := app.commitScoutChatThreadMessages("tom@shareability.com", channel.ID, scoutChatMessageRecord{
		ID:          "msg-channel-1",
		Kind:        "message",
		Role:        "user",
		Text:        "sharing the onesheet",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		AuthorName:  "Tom",
		AuthorEmail: "tom@shareability.com",
		Files: []scoutChatFileAttachment{
			{Name: "onesheet.pdf", Kind: "pdf", Size: 40, Ref: ref, Mime: "application/pdf"},
			// name-only pre-085 chip: no bytes, no text — never listed
			{Name: "ghost.key", Kind: "key", Size: 9},
		},
	}); err != nil {
		t.Fatalf("commit channel message: %v", err)
	}

	// The owner sees both; newest (channel) first.
	rows := app.assistantFilesForUser("aj@shareability.com")
	if len(rows) != 2 {
		t.Fatalf("owner rows=%d (%+v), want private + channel files", len(rows), rows)
	}
	if rows[0].Name != "onesheet.pdf" || rows[1].Name != "deck.png" {
		t.Fatalf("order=%q,%q, want newest first", rows[0].Name, rows[1].Name)
	}
	channelRow, privateRow := rows[0], rows[1]
	if channelRow.Origin != "chat" || channelRow.OriginThreadID != channel.ID || channelRow.OriginThreadTitle != "standup" {
		t.Fatalf("channel row=%+v, want chat origin with the thread chip data", channelRow)
	}
	if channelRow.BrainStatus != fileBrainStatusStored || !channelRow.Previewable {
		t.Fatalf("channel row=%+v, want stored (no text yet) + inline pdf preview", channelRow)
	}
	// card-103 folded fix: a PRIVATE thread attachment's derived text rides only
	// that 1:1's context, never company recall — so it earns the honest
	// thread-scoped badge, not the company-wide "ingested".
	if privateRow.BrainStatus != fileBrainStatusThread {
		t.Fatalf("private row=%+v, want thread-scoped badge (private text never enters company recall)", privateRow)
	}
	if !strings.HasPrefix(privateRow.DownloadURL, "/artifacts/blob?ref=") || privateRow.UploaderEmail != "aj@shareability.com" {
		t.Fatalf("private row=%+v, want blob download + uploader stamp", privateRow)
	}

	// A teammate sees ONLY the public channel's file — private threads stay
	// the owner's.
	teammateRows := app.assistantFilesForUser("tom@shareability.com")
	if len(teammateRows) != 1 || teammateRows[0].Name != "onesheet.pdf" {
		t.Fatalf("teammate rows=%+v, want only the channel file", teammateRows)
	}

	// The other side of the folded fix: a PUBLIC channel attachment WITH derived
	// text keeps the company-wide "ingested" badge — only private threads scope
	// down to "in this chat".
	if _, err := app.commitScoutChatThreadMessages("tom@shareability.com", channel.ID, scoutChatMessageRecord{
		ID:          "msg-channel-2",
		Kind:        "message",
		Role:        "user",
		Text:        "notes",
		CreatedAt:   time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano),
		AuthorName:  "Tom",
		AuthorEmail: "tom@shareability.com",
		Files: []scoutChatFileAttachment{
			{Name: "notes.pdf", Kind: "pdf", Size: 20, Ref: ref, Mime: "application/pdf", Text: "channel derived facts"},
		},
	}); err != nil {
		t.Fatalf("commit second channel message: %v", err)
	}
	notesFound := false
	notesStatus := ""
	for _, row := range app.assistantFilesForUser("tom@shareability.com") {
		if row.Name == "notes.pdf" {
			notesFound = true
			notesStatus = row.BrainStatus
		}
	}
	if !notesFound || notesStatus != fileBrainStatusIngested {
		t.Fatalf("public channel row with text: found=%v status=%q, want ingested (company recall)", notesFound, notesStatus)
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
