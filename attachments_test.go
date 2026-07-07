package main

// Chat attachment ingestion (card 085): the upload door's auth/mime/size
// contract, the ref → content-block builder and its request budgets, the
// document block's wire shape, attachment placement in the text request, the
// sanitize-side ref validation, the derived-text pass through the full send
// path, the keyless degrade, and the GC sweep's new chat-ref awareness.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// anthropicSourceBlockView decodes the wire shape shared by image and
// document blocks: {"type":..., "source":{"type":"base64","media_type":...}}.
type anthropicSourceBlockView struct {
	Type   string `json:"type"`
	Source struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source"`
}

func decodeAnthropicSourceBlock(t *testing.T, raw json.RawMessage) anthropicSourceBlockView {
	t.Helper()
	var view anthropicSourceBlockView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatalf("decode content block: %v", err)
	}
	return view
}

func TestAssistantAttachmentUploadHandlerAuthMimeAndSize(t *testing.T) {
	setupAuthTestEnv(t)
	setupIsolatedBlobStore(t)
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")

	pngBytes := []byte("\x89PNG\r\n\x1a\nfake image payload")

	// Method gate.
	recorder := httptest.NewRecorder()
	assistantAttachmentUploadHandler(recorder, httptest.NewRequest(http.MethodGet, "/assistant/attachments", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status=%d, want 405", recorder.Code)
	}

	// Cross-origin gate, before any auth or body read.
	crossOrigin := httptest.NewRequest(http.MethodPost, "/assistant/attachments", bytes.NewReader(pngBytes))
	crossOrigin.Header.Set("Origin", "https://evil.example")
	crossOrigin.Header.Set("Content-Type", "image/png")
	recorder = httptest.NewRecorder()
	assistantAttachmentUploadHandler(recorder, crossOrigin)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status=%d, want 403", recorder.Code)
	}

	// Session gate: no cookie → 401.
	unsigned := httptest.NewRequest(http.MethodPost, "/assistant/attachments", bytes.NewReader(pngBytes))
	unsigned.Header.Set("Content-Type", "image/png")
	recorder = httptest.NewRecorder()
	assistantAttachmentUploadHandler(recorder, unsigned)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out status=%d, want 401", recorder.Code)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	post := func(contentType string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/assistant/attachments", bytes.NewReader(body))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantAttachmentUploadHandler(recorder, req)
		return recorder
	}

	// Mime allowlist: script-capable and unknown types never enter the store.
	for _, mime := range []string{"", "text/html", "image/svg+xml", "application/octet-stream"} {
		if recorder := post(mime, pngBytes); recorder.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("mime %q status=%d, want 415", mime, recorder.Code)
		}
	}

	// Empty body rejects before putBlob.
	if recorder := post("image/png", nil); recorder.Code != http.StatusBadRequest {
		t.Fatalf("empty-body status=%d, want 400", recorder.Code)
	}

	// One byte over the 25MB cap → 413.
	if recorder := post("application/pdf", make([]byte, attachmentUploadMaxBytes+1)); recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize status=%d, want 413", recorder.Code)
	}

	// Happy path: parameters on the Content-Type are stripped, the response
	// carries the content-addressed ref, and the stored bytes round-trip.
	recorder = post("image/png; charset=binary", pngBytes)
	if recorder.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		OK   bool   `json:"ok"`
		Ref  string `json:"ref"`
		Mime string `json:"mime"`
		Size int64  `json:"size"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if !payload.OK || !validBlobRef(payload.Ref) || payload.Mime != "image/png" || payload.Size != int64(len(pngBytes)) {
		t.Fatalf("upload payload=%+v, want ok with a valid ref, image/png, size %d", payload, len(pngBytes))
	}
	stored, meta, err := getBlob(payload.Ref)
	if err != nil {
		t.Fatalf("getBlob after upload: %v", err)
	}
	if !bytes.Equal(stored, pngBytes) || meta.Mime != "image/png" {
		t.Fatalf("stored=%q mime=%q, want the uploaded bytes with the pinned mime", stored, meta.Mime)
	}
}

func TestAttachmentContentBlocksShapesAndBudgets(t *testing.T) {
	setupIsolatedBlobStore(t)

	pngBytes := []byte("png raster bytes")
	pngRef, err := putBlob(pngBytes, "image/png")
	if err != nil {
		t.Fatalf("putBlob png: %v", err)
	}
	pdfBytes := []byte("%PDF-1.7 attached deck")
	pdfRef, err := putBlob(pdfBytes, "application/pdf")
	if err != nil {
		t.Fatalf("putBlob pdf: %v", err)
	}
	textRef, err := putBlob([]byte("plain notes"), "text/plain")
	if err != nil {
		t.Fatalf("putBlob text: %v", err)
	}

	blocks := attachmentContentBlocks([]scoutChatFileAttachment{
		{Name: "shot.png", Ref: pngRef},
		{Name: "deck.pdf", Ref: pdfRef},
		{Name: "missing.png", Ref: strings.Repeat("0", 64)},
		{Name: "bogus.png", Ref: "not-a-ref"},
		{Name: "notes.txt", Ref: textRef}, // stored, but not a model-safe mime
		{Name: "plain.txt"},               // no ref at all
	})
	if len(blocks) != 2 {
		t.Fatalf("blocks=%d, want exactly the png image block and the pdf document block", len(blocks))
	}
	image := decodeAnthropicSourceBlock(t, blocks[0])
	if image.Type != "image" || image.Source.Type != "base64" || image.Source.MediaType != "image/png" {
		t.Fatalf("image block=%+v, want type=image base64 image/png", image)
	}
	if decoded, err := base64.StdEncoding.DecodeString(image.Source.Data); err != nil || !bytes.Equal(decoded, pngBytes) {
		t.Fatalf("image data did not round-trip: %v", err)
	}
	document := decodeAnthropicSourceBlock(t, blocks[1])
	if document.Type != "document" || document.Source.Type != "base64" || document.Source.MediaType != "application/pdf" {
		t.Fatalf("document block=%+v, want type=document base64 application/pdf", document)
	}
	if decoded, err := base64.StdEncoding.DecodeString(document.Source.Data); err != nil || !bytes.Equal(decoded, pdfBytes) {
		t.Fatalf("document data did not round-trip: %v", err)
	}
	// The API rejects base64 with embedded newlines.
	for index, raw := range blocks {
		if bytes.ContainsAny(raw, "\n") {
			t.Fatalf("block %d carries a newline in its JSON", index)
		}
	}

	// Image count budget: a 13-image message ships only the first 12 blocks.
	overStuffed := make([]scoutChatFileAttachment, 0, anthropicMaxRequestImages+1)
	for range anthropicMaxRequestImages + 1 {
		overStuffed = append(overStuffed, scoutChatFileAttachment{Name: "shot.png", Ref: pngRef})
	}
	if got := attachmentContentBlocks(overStuffed); len(got) != anthropicMaxRequestImages {
		t.Fatalf("image blocks=%d, want the %d-image cap enforced", len(got), anthropicMaxRequestImages)
	}

	// PDF count budget: one document block per message, the second degrades
	// to its text placeholder (no block, no error).
	if got := attachmentContentBlocks([]scoutChatFileAttachment{
		{Name: "a.pdf", Ref: pdfRef},
		{Name: "b.pdf", Ref: pdfRef},
	}); len(got) != 1 {
		t.Fatalf("pdf blocks=%d, want the 1-PDF cap enforced", len(got))
	}
}

// The COMBINED image+PDF payload is bounded so a message that fills both
// per-category budgets can't blow past Anthropic's 32MB request cap after
// base64 expansion. A 20MB PDF plus a smaller image sum over the combined
// ceiling, so only the PDF's block ships even though each file is within its
// own category budget.
func TestAttachmentContentBlocksCombinedRequestBudget(t *testing.T) {
	setupIsolatedBlobStore(t)

	// A PDF at the per-category cap.
	pdfBytes := make([]byte, attachmentMaxPDFBytes)
	pdfBytes[0], pdfBytes[1] = '%', 'P'
	pdfRef, err := putBlob(pdfBytes, "application/pdf")
	if err != nil {
		t.Fatalf("putBlob pdf: %v", err)
	}
	// An image that fits the image category on its own (well under 20MB) but
	// pushes the running total past attachmentMaxRequestBytes.
	imageBytes := make([]byte, attachmentMaxRequestBytes-attachmentMaxPDFBytes+(1<<20))
	imageBytes[0] = 0x89
	imageRef, err := putBlob(imageBytes, "image/png")
	if err != nil {
		t.Fatalf("putBlob png: %v", err)
	}

	blocks := attachmentContentBlocks([]scoutChatFileAttachment{
		{Name: "deck.pdf", Ref: pdfRef},
		{Name: "shot.png", Ref: imageRef},
	})
	if len(blocks) != 1 {
		t.Fatalf("blocks=%d, want only the PDF block once the combined budget is spent", len(blocks))
	}
	if got := decodeAnthropicSourceBlock(t, blocks[0]); got.Type != "document" {
		t.Fatalf("kept block type=%s, want the document block admitted before the combined cap tripped", got.Type)
	}
}

func TestAnthropicDocumentBlockWireShape(t *testing.T) {
	data := []byte("%PDF-1.7 tiny")
	block := decodeAnthropicSourceBlock(t, anthropicDocumentBlock(" application/pdf ", data))
	if block.Type != "document" || block.Source.Type != "base64" || block.Source.MediaType != "application/pdf" {
		t.Fatalf("block=%+v, want document/base64/application/pdf with trimmed media type", block)
	}
	decoded, err := base64.StdEncoding.DecodeString(block.Source.Data)
	if err != nil || !bytes.Equal(decoded, data) {
		t.Fatalf("document data did not round-trip: %v", err)
	}
	if strings.ContainsAny(block.Source.Data, "\r\n") {
		t.Fatal("document base64 must be newline-free")
	}
}

// Attachments land BEFORE the text block in the single user turn — the
// documented order for vision/document requests — and a request without
// attachments assembles exactly one text block as before.
func TestCreateAnthropicTextResponsePlacesAttachmentsBeforeText(t *testing.T) {
	t.Setenv("BONFIRE_CHAT_MODEL", "")
	imageBlock := anthropicImageBlock("image/png", []byte("raster"))
	documentBlock := anthropicDocumentBlock("application/pdf", []byte("%PDF-1.7"))

	var got anthropicMessagesRequest
	swapAnthropicMessagesResponder(t, func(_ context.Context, _ string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		got = request
		return anthropicMessagesResponse{
			StopReason: "end_turn",
			Content:    []json.RawMessage{mockAnthropicTextBlock("the deck says hello")},
		}, nil
	})

	if _, err := createAnthropicTextResponse(context.Background(), "sk-ant-test", anthropicTextRequest{
		Input:       "what does this deck say?",
		Attachments: []json.RawMessage{imageBlock, documentBlock},
	}); err != nil {
		t.Fatalf("createAnthropicTextResponse: %v", err)
	}

	if len(got.Messages) != 1 || len(got.Messages[0].Content) != 3 {
		t.Fatalf("messages=%+v, want one user turn with attachment+attachment+text", got.Messages)
	}
	first := decodeAnthropicSourceBlock(t, got.Messages[0].Content[0])
	second := decodeAnthropicSourceBlock(t, got.Messages[0].Content[1])
	if first.Type != "image" || second.Type != "document" {
		t.Fatalf("block order=%s,%s, want image,document ahead of the text", first.Type, second.Type)
	}
	last := decodeAnthropicBlock(got.Messages[0].Content[2])
	if last.Type != "text" || last.Text != "what does this deck say?" {
		t.Fatalf("final block=%+v, want the text input last", last)
	}
}

func TestSanitizeScoutChatFilesValidatesBlobRefs(t *testing.T) {
	setupIsolatedBlobStore(t)

	pngRef, err := putBlob([]byte("raster"), "image/png")
	if err != nil {
		t.Fatalf("putBlob png: %v", err)
	}
	htmlRef, err := putBlob([]byte("<script>alert(1)</script>"), "text/html")
	if err != nil {
		t.Fatalf("putBlob html: %v", err)
	}

	cleaned := sanitizeScoutChatFiles([]scoutChatFileAttachment{
		// A valid ref keeps the store's pinned mime and NEVER client text —
		// a ref'd binary's text is the server-derived transcription only.
		{Name: "shot.png", Kind: "png", Size: 6, Ref: pngRef, Mime: "application/x-spoofed", Text: "attacker-claimed contents"},
		// Malformed and missing refs drop to plain chips.
		{Name: "bogus.png", Ref: "zz"},
		{Name: "gone.png", Ref: strings.Repeat("a", 64)},
		// A stored blob outside the model-safe allowlist drops its ref too.
		{Name: "page.html", Ref: htmlRef},
	})
	if len(cleaned) != 4 {
		t.Fatalf("cleaned=%d files, want all four chips kept", len(cleaned))
	}
	if cleaned[0].Ref != pngRef || cleaned[0].Mime != "image/png" || cleaned[0].Text != "" {
		t.Fatalf("ref'd file=%+v, want kept ref, pinned image/png mime, stripped client text", cleaned[0])
	}
	for index := 1; index < 4; index++ {
		if cleaned[index].Ref != "" || cleaned[index].Mime != "" {
			t.Fatalf("file %d=%+v, want ref and mime dropped", index, cleaned[index])
		}
	}
	if cleaned[1].Name != "bogus.png" {
		t.Fatalf("name=%q, want the chip name preserved", cleaned[1].Name)
	}
}

// The full keyed send path: the derived-text pass transcribes the attachment
// into file.Text before the commit, and the Q&A turn carries the binary
// blocks so Scout actually sees the image.
func TestScoutChatAttachmentDerivedTextAndVisionQnA(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")

	pngRef, err := putBlob([]byte("raster bytes"), "image/png")
	if err != nil {
		t.Fatalf("putBlob: %v", err)
	}

	// The private-thread router turn rides the raw Messages seam — return no
	// tool_use so the turn falls through to plain Q&A.
	swapAnthropicMessagesResponder(t, func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		return anthropicMessagesResponse{StopReason: "end_turn"}, nil
	})

	const transcription = "Deck claims: $2M ARR, 40% MoM growth, pilot with StationTenn."
	var textRequests []anthropicTextRequest
	swapAnthropicTextResponder(t, func(_ context.Context, _ string, request anthropicTextRequest) (string, error) {
		textRequests = append(textRequests, request)
		if len(textRequests) == 1 {
			return transcription, nil
		}
		return "It says ARR is $2M.", nil
	})

	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Deck check", "")
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}

	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "what does this deck say?", []scoutChatFileAttachment{
		{Name: "deck.png", Kind: "png", Size: 12, Ref: pngRef, Text: "client junk that must be stripped"},
	}, "")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if len(textRequests) != 2 {
		t.Fatalf("text seam calls=%d, want derive + Q&A", len(textRequests))
	}
	derive := textRequests[0]
	if len(derive.Attachments) != 1 || derive.Instructions != attachmentDeriveInstructions || derive.MaxTokens != attachmentDeriveMaxTokens {
		t.Fatalf("derive request=%+v, want one attachment block under the transcription budget", derive)
	}
	qna := textRequests[1]
	if len(qna.Attachments) != 1 {
		t.Fatalf("Q&A request carries %d attachments, want the image block", len(qna.Attachments))
	}
	if !strings.Contains(qna.Input, transcription) {
		t.Fatalf("Q&A input=%q, want the derived transcription folded into the model query", qna.Input)
	}

	saved, ok := response["thread"].(scoutChatThreadRecord)
	if !ok {
		t.Fatalf("response thread=%T, want scoutChatThreadRecord", response["thread"])
	}
	if len(saved.Messages) == 0 || len(saved.Messages[0].Files) != 1 {
		t.Fatalf("saved thread=%+v, want the user message with one file", saved)
	}
	file := saved.Messages[0].Files[0]
	if file.Ref != pngRef || file.Mime != "image/png" {
		t.Fatalf("persisted file=%+v, want ref + pinned mime", file)
	}
	if file.Text != transcription {
		t.Fatalf("persisted file text=%q, want the derived transcription (client text stripped)", file.Text)
	}
}

// Keyless-Anthropic: no transcription runs, no blob is read for blocks, and
// the gpt-5.5 path answers from the name-only placeholder byte-for-byte.
func TestScoutChatAttachmentKeylessDegradesToNameOnly(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.mu.Lock()
	kanbanApp.apiKey = "test-openai-key"
	kanbanApp.mu.Unlock()
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("ANTHROPIC_API_KEY", "")

	pngRef, err := putBlob([]byte("raster bytes"), "image/png")
	if err != nil {
		t.Fatalf("putBlob: %v", err)
	}

	swapAnthropicTextResponder(t, func(context.Context, string, anthropicTextRequest) (string, error) {
		t.Fatal("Anthropic seam must not be touched keyless")
		return "", nil
	})
	var gotInput string
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		gotInput = request.Input
		return "I can only see the file name.", nil
	})

	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Deck check", "")
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}

	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "what does this deck say?", []scoutChatFileAttachment{
		{Name: "deck.png", Kind: "png", Size: 12, Ref: pngRef},
	}, "")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}
	if !strings.Contains(gotInput, "Attached file: deck.png") {
		t.Fatalf("keyless input=%q, want the name-only placeholder", gotInput)
	}
	saved, ok := response["thread"].(scoutChatThreadRecord)
	if !ok {
		t.Fatalf("response thread=%T, want scoutChatThreadRecord", response["thread"])
	}
	file := saved.Messages[0].Files[0]
	if file.Text != "" {
		t.Fatalf("keyless derived text=%q, want empty (no transcription without a key)", file.Text)
	}
	if file.Ref != pngRef {
		t.Fatalf("keyless ref=%q, want the ref preserved for the render path", file.Ref)
	}
}

// The admin GC sweep must treat chat-attachment refs as live: a thread's
// inline image survives while a true orphan is deleted.
func TestSweepUnreferencedBlobsKeepsChatAttachmentRefs(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)

	chatRef, err := putBlob([]byte("chat attachment raster"), "image/png")
	if err != nil {
		t.Fatalf("putBlob chat: %v", err)
	}
	orphanRef, err := putBlob([]byte("orphan bytes"), "image/png")
	if err != nil {
		t.Fatalf("putBlob orphan: %v", err)
	}

	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Deck check", "")
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", thread.ID, scoutChatMessageRecord{
		ID:   "scout-chat-message-1",
		Kind: "message",
		Role: "user",
		Text: "the deck",
		Files: []scoutChatFileAttachment{
			{Name: "deck.png", Ref: chatRef, Mime: "image/png"},
		},
	}); err != nil {
		t.Fatalf("commit message: %v", err)
	}

	deleted, err := sweepUnreferencedBlobs(app)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != orphanRef {
		t.Fatalf("deleted=%v, want only the orphan %s", deleted, orphanRef)
	}
	if _, _, err := getBlob(chatRef); err != nil {
		t.Fatalf("chat attachment blob was swept: %v", err)
	}
	dataPath := filepath.Join(blobStoreDir(), orphanRef[:2], orphanRef)
	if _, err := os.Stat(dataPath); !os.IsNotExist(err) {
		t.Fatalf("orphan blob still on disk at %s", dataPath)
	}
}
