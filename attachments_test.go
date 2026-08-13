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
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	pixel := image.NewRGBA(image.Rect(0, 0, 1, 1))
	pixel.Set(0, 0, color.RGBA{R: 255, G: 90, B: 60, A: 255})
	if err := png.Encode(&buffer, pixel); err != nil {
		t.Fatalf("encode PNG fixture: %v", err)
	}
	return buffer.Bytes()
}

func grantTestPendingAttachment(t *testing.T, app *kanbanBoardApp, user *userAccount, destination scoutChatThreadRecord, ref string) scoutChatFileAttachment {
	t.Helper()
	meta, err := blobStatForRef(ref)
	if err != nil {
		t.Fatalf("stat pending attachment: %v", err)
	}
	grant, err := app.grantPendingAttachmentUpload(user, destination, ref, meta)
	if err != nil {
		t.Fatalf("grant pending attachment: %v", err)
	}
	return scoutChatFileAttachment{Ref: ref, SourceID: grant.SourceID, SourceRevision: grant.SourceRevision}
}

func reserveTestAttachment(t *testing.T, app *kanbanBoardApp, user *userAccount, destination scoutChatThreadRecord, file scoutChatFileAttachment, reservationID string) scoutChatFileAttachment {
	t.Helper()
	meta, err := blobStatForRef(file.Ref)
	if err != nil {
		t.Fatalf("stat attachment source: %v", err)
	}
	grant, err := app.grantPendingAttachmentUpload(user, destination, file.Ref, meta)
	if err != nil {
		t.Fatalf("grant attachment source: %v", err)
	}
	file.SourceID = grant.SourceID
	file.SourceRevision = grant.SourceRevision
	file.Mime = meta.Mime
	file.Size = meta.Size
	if err := app.reservePendingAttachmentUpload(user, destination, file, meta, reservationID); err != nil {
		t.Fatalf("reserve attachment source: %v", err)
	}
	return file
}

func TestAssistantAttachmentUploadHandlerAuthMimeAndSize(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	owner := accountStore().findUser("aj@shareability.com")
	destination, err := kanbanApp.createScoutChatThread(owner.Email, owner.Name, "Attachment test", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create attachment destination: %v", err)
	}
	uploadURL := "/assistant/attachments?threadId=" + destination.ID

	pngBytes := tinyPNG(t)

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
		req := httptest.NewRequest(http.MethodPost, uploadURL, bytes.NewReader(body))
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

	// Explicit script-capable and unsupported types never enter the store.
	// Missing/generic declarations are handled later through byte validation
	// for native picker compatibility.
	for _, mime := range []string{"text/html", "image/svg+xml", "image/heic"} {
		if recorder := post(mime, pngBytes); recorder.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("mime %q status=%d, want 415", mime, recorder.Code)
		}
	}

	// Empty body rejects before putBlob.
	if recorder := post("image/png", nil); recorder.Code != http.StatusBadRequest {
		t.Fatalf("empty-body status=%d, want 400", recorder.Code)
	}

	// A declared safe type is not enough: malformed or type-confused bytes
	// never enter the content-addressed store.
	if recorder := post("image/png", []byte("\x89PNG\r\n\x1a\nfake image payload")); recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("malformed png status=%d, want 415", recorder.Code)
	}
	if recorder := post("application/pdf", []byte("<script>alert(1)</script>")); recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("type-confused pdf status=%d, want 415", recorder.Code)
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
		OK             bool   `json:"ok"`
		Ref            string `json:"ref"`
		Mime           string `json:"mime"`
		Size           int64  `json:"size"`
		SourceID       string `json:"sourceId"`
		SourceRevision string `json:"sourceRevision"`
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
	other := accountStore().findUser("tim@shareability.com")
	file := scoutChatFileAttachment{Ref: payload.Ref, SourceID: payload.SourceID, SourceRevision: payload.SourceRevision}
	if payload.SourceID == "" || payload.SourceRevision == "" || kanbanApp.reservePendingAttachmentUpload(owner, destination, file, meta, "upload-handler-test") != nil {
		t.Fatal("successful upload did not mint a destination-bound immutable source grant")
	}
	if kanbanApp.reservePendingAttachmentUpload(other, destination, file, meta, "upload-handler-replay") == nil {
		t.Fatal("consumed upload grant authorized a replay by another user")
	}
}

func TestAssistantAttachmentUploadHandlerSupportsNativeMultipartAndSafeMIMEFallbacks(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	owner := accountStore().findUser("aj@shareability.com")
	destination, err := kanbanApp.createScoutChatThread(owner.Email, owner.Name, "Native attachment test", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create attachment destination: %v", err)
	}
	uploadURL := "/assistant/attachments?threadId=" + destination.ID
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	pngBytes := tinyPNG(t)

	postMultipart := func(field, filename, declared string, data []byte) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		var part io.Writer
		var err error
		if declared == "" {
			part, err = writer.CreateFormFile(field, filename)
		} else {
			header := make(textproto.MIMEHeader)
			header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, filename))
			header.Set("Content-Type", declared)
			part, err = writer.CreatePart(header)
		}
		if err != nil {
			t.Fatalf("create multipart part: %v", err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatalf("write multipart part: %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("close multipart body: %v", err)
		}
		request := httptest.NewRequest(http.MethodPost, uploadURL, &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantAttachmentUploadHandler(recorder, request)
		return recorder
	}

	// Expo/React Native's reliable URI upload shape is multipart. A generic
	// part MIME is content-sniffed and returns the canonical stored MIME.
	recorder := postMultipart("file", "screenshot.png", "application/octet-stream", pngBytes)
	if recorder.Code != http.StatusOK {
		t.Fatalf("native multipart status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Ref  string `json:"ref"`
		Mime string `json:"mime"`
		Size int64  `json:"size"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode multipart response: %v", err)
	}
	if payload.Mime != "image/png" || payload.Size != int64(len(pngBytes)) || !validBlobRef(payload.Ref) {
		t.Fatalf("multipart response=%s", recorder.Body.String())
	}

	if recorder := postMultipart("wrong", "screenshot.png", "image/png", pngBytes); recorder.Code != http.StatusBadRequest {
		t.Fatalf("wrong field status=%d, want 400", recorder.Code)
	}
	if recorder := postMultipart("file", "screenshot.png", "image/jpeg", pngBytes); recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("conflicting declared MIME status=%d, want 415", recorder.Code)
	}

	// Native MIME aliases canonicalize, but only when the bytes match.
	request := httptest.NewRequest(http.MethodPost, uploadURL, bytes.NewReader(pngBytes))
	request.Header.Set("Content-Type", "application/octet-stream")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	assistantAttachmentUploadHandler(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"mime":"image/png"`) {
		t.Fatalf("raw generic status=%d body=%s, want sniffed PNG", recorder.Code, recorder.Body.String())
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

func TestOpenAIAttachmentContentWireShape(t *testing.T) {
	image := []byte("raster")
	pdf := []byte("%PDF-1.7")
	items := openAIAttachmentContentWithReader([]scoutChatFileAttachment{
		{Name: "shot.png", Ref: strings.Repeat("a", 64)},
		{Name: "../brief.pdf", Ref: strings.Repeat("b", 64)},
		{Name: "animated.gif", Ref: strings.Repeat("c", 64)},
	}, func(file scoutChatFileAttachment) ([]byte, blobMeta, bool) {
		if strings.HasSuffix(file.Name, ".pdf") {
			return pdf, blobMeta{Mime: "application/pdf"}, true
		}
		if strings.HasSuffix(file.Name, ".gif") {
			return []byte("GIF89a"), blobMeta{Mime: "image/gif"}, true
		}
		return image, blobMeta{Mime: "image/png"}, true
	})
	if len(items) != 2 {
		t.Fatalf("items=%d, want image and PDF", len(items))
	}
	if items[0].Type != "input_image" || items[0].ImageURL != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(image) {
		t.Fatalf("image item=%+v", items[0])
	}
	if items[1].Type != "input_file" || items[1].Filename != "brief.pdf" || items[1].FileData != "data:application/pdf;base64,"+base64.StdEncoding.EncodeToString(pdf) {
		t.Fatalf("PDF item=%+v", items[1])
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
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	thread, err := kanbanApp.createScoutChatThread(user.Email, user.Name, "Scout", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}

	pngRef, err := putBlob(tinyPNG(t), "image/png")
	if err != nil {
		t.Fatalf("putBlob png: %v", err)
	}
	grantedFile := grantTestPendingAttachment(t, kanbanApp, user, thread, pngRef)
	htmlRef, err := putBlob([]byte("<script>alert(1)</script>"), "text/html")
	if err != nil {
		t.Fatalf("putBlob html: %v", err)
	}

	cleaned, err := kanbanApp.sanitizeScoutChatFiles(context.Background(), user, thread, []scoutChatFileAttachment{
		// A valid ref keeps the store's pinned mime and NEVER client text —
		// a ref'd binary's text is the server-derived transcription only.
		{Name: "shot.png", Kind: "png", Size: 6, Ref: pngRef, Mime: "application/x-spoofed", Text: "attacker-claimed contents", SourceID: grantedFile.SourceID, SourceRevision: grantedFile.SourceRevision},
	}, "sanitize-valid")
	if err != nil {
		t.Fatalf("sanitize valid source: %v", err)
	}
	meta, err := blobStatForRef(pngRef)
	if err != nil {
		t.Fatalf("stat png: %v", err)
	}
	if cleaned[0].Ref != pngRef || cleaned[0].Mime != "image/png" || cleaned[0].Size != meta.Size || cleaned[0].Text != "" {
		t.Fatalf("ref'd file=%+v, want kept ref, blob-pinned metadata, stripped client text", cleaned[0])
	}
	duplicateFile := grantTestPendingAttachment(t, kanbanApp, user, thread, pngRef)
	if _, err := kanbanApp.sanitizeScoutChatFiles(context.Background(), user, thread, []scoutChatFileAttachment{duplicateFile, duplicateFile}, "sanitize-duplicate"); err == nil {
		t.Fatal("duplicate source id was accepted")
	}
	for label, candidate := range map[string]scoutChatFileAttachment{
		"malformed": {Name: "bogus.png", Ref: "zz"},
		"missing":   {Name: "gone.png", Ref: strings.Repeat("a", 64)},
		"unsafe":    {Name: "page.html", Ref: htmlRef},
	} {
		if _, err := kanbanApp.sanitizeScoutChatFiles(context.Background(), user, thread, []scoutChatFileAttachment{candidate}, "sanitize-"+label); err == nil {
			t.Fatalf("%s ref downgraded to a name-only chip; want fail closed", label)
		}
	}
	other := accountStore().findUser("tim@shareability.com")
	if _, err := kanbanApp.sanitizeScoutChatFiles(context.Background(), other, thread, []scoutChatFileAttachment{{Name: "guessed.png", Ref: pngRef}}, "sanitize-guessed"); err == nil {
		t.Fatal("cross-user guessed ref downgraded to an inert chip instead of failing closed")
	}
}

func TestAttachmentSourceHandleBindsRevisionDestinationAudienceExpiryAndOneCommit(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	privateA, err := kanbanApp.createScoutChatThread(user.Email, user.Name, "A", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create private A: %v", err)
	}
	privateB, err := kanbanApp.createScoutChatThread(user.Email, user.Name, "B", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create private B: %v", err)
	}
	ref, err := putBlob(tinyPNG(t), "image/png")
	if err != nil {
		t.Fatalf("put source blob: %v", err)
	}

	assertDenied := func(label string, destination scoutChatThreadRecord, file scoutChatFileAttachment) {
		t.Helper()
		file.Name = label + ".png"
		file.Text = "stale derived secret"
		if _, err := kanbanApp.sanitizeScoutChatFiles(context.Background(), user, destination, []scoutChatFileAttachment{file}, "denied-"+label); err == nil {
			t.Fatalf("%s source downgraded instead of failing closed", label)
		}
	}

	wrongRevision := grantTestPendingAttachment(t, kanbanApp, user, privateA, ref)
	wrongRevision.SourceRevision = "sha256:" + strings.Repeat("0", 64)
	assertDenied("wrong-revision", privateA, wrongRevision)

	wrongDestination := grantTestPendingAttachment(t, kanbanApp, user, privateA, ref)
	assertDenied("wrong-destination", privateB, wrongDestination)

	changedRecipients := grantTestPendingAttachment(t, kanbanApp, user, privateA, ref)
	mutatedAudience := privateA
	mutatedAudience.Visibility = scoutChatVisibilityPublic
	assertDenied("changed-audience", mutatedAudience, changedRecipients)

	expired := grantTestPendingAttachment(t, kanbanApp, user, privateA, ref)
	kanbanApp.pendingAttachmentUploadsMu.Lock()
	grant := kanbanApp.pendingAttachmentUploads[expired.SourceID]
	grant.ExpiresAt = time.Now().UTC().Add(-time.Second)
	kanbanApp.pendingAttachmentUploads[expired.SourceID] = grant
	kanbanApp.pendingAttachmentUploadsMu.Unlock()
	assertDenied("expired", privateA, expired)

	revoked := grantTestPendingAttachment(t, kanbanApp, user, privateA, ref)
	kanbanApp.pendingAttachmentUploadsMu.Lock()
	delete(kanbanApp.pendingAttachmentUploads, revoked.SourceID)
	kanbanApp.pendingAttachmentUploadsMu.Unlock()
	assertDenied("revoked", privateA, revoked)

	oneCommit := grantTestPendingAttachment(t, kanbanApp, user, privateA, ref)
	accepted, err := kanbanApp.sanitizeScoutChatFiles(context.Background(), user, privateA, []scoutChatFileAttachment{oneCommit}, "one-commit")
	if err != nil {
		t.Fatalf("reserve source: %v", err)
	}
	if len(accepted) != 1 || accepted[0].Ref != ref || accepted[0].Mime != "image/png" {
		t.Fatalf("first commit=%+v, want authorized source", accepted)
	}
	assertDenied("replay", privateA, oneCommit)
}

func TestAttachmentDestinationRevisionFenceRejectsAudienceRaceAtCommit(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	user := accountStore().findUser("aj@shareability.com")
	thread, err := kanbanApp.createScoutChatThread(user.Email, user.Name, "private", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	ref, err := putBlob(tinyPNG(t), "image/png")
	if err != nil {
		t.Fatalf("put source blob: %v", err)
	}
	file := grantTestPendingAttachment(t, kanbanApp, user, thread, ref)
	cleaned, err := kanbanApp.sanitizeScoutChatFiles(context.Background(), user, thread, []scoutChatFileAttachment{file}, "attachment-race-reservation")
	if err != nil {
		t.Fatalf("sanitize source: %v", err)
	}
	if len(cleaned) != 1 || cleaned[0].Ref != ref {
		t.Fatalf("preflight source=%+v, want authorized ref", cleaned)
	}
	message := scoutChatMessageRecord{
		ID:                            "attachment-race-message",
		Kind:                          "message",
		Role:                          "user",
		Text:                          "private source",
		CreatedAt:                     time.Now().UTC().Format(time.RFC3339Nano),
		AuthorEmail:                   user.Email,
		Files:                         cleaned,
		attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(thread),
		attachmentReservationID:       "attachment-race-reservation",
	}
	thread.Visibility = scoutChatVisibilityPublic
	if err := kanbanApp.saveScoutChatThread(thread); err != nil {
		t.Fatalf("mutate audience fixture: %v", err)
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages(user.Email, thread.ID, message); err == nil || !strings.Contains(err.Error(), "destination changed") {
		t.Fatalf("commit error=%v, want destination revision rejection", err)
	}
	saved, _, err := kanbanApp.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		t.Fatalf("reload thread: %v", err)
	}
	if len(saved.Messages) != 0 {
		t.Fatalf("audience-raced attachment committed: %+v", saved.Messages)
	}
}

func TestOpenAIReplyMediaContentCarriesAuthorizedGeneratedImage(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "private image", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	ref, err := putBlob(tinyPNG(t), "image/png")
	if err != nil {
		t.Fatalf("put image: %v", err)
	}
	thread.Messages = append(thread.Messages, scoutChatMessageRecord{
		ID: "generated-image-message", Kind: scoutChatMessageKindImage, Role: "scout",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Image: &scoutChatImageRef{Ref: ref, Mime: "image/png", Name: "concept.png"},
	})
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatalf("save thread: %v", err)
	}
	content := app.openAIReplyMediaContent("aj@shareability.com", thread.ID, "generated-image-message")
	if len(content) != 1 || content[0].Type != "input_image" || !strings.HasPrefix(content[0].ImageURL, "data:image/png;base64,") {
		t.Fatalf("reply media=%+v, want one inline image", content)
	}
	if linked := app.openAIReplyMediaContentForTurn(true, "aj@shareability.com", thread.ID, "generated-image-message"); len(linked) != 0 {
		t.Fatalf("Project-linked generated-image parent reached provider media: %+v", linked)
	}
	meta, err := blobStatForRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	generatedMedia := projectChatManifestReplyMedia{Ordinal: 0, Kind: "generated_image", SourceID: "generated-image-source",
		SourceRevision: attachmentSourceRevision(ref, meta), BlobRef: ref, BlobDigest: ref, Mime: "image/png", Size: meta.Size,
		DestinationRevision: scoutChatAttachmentDestinationRevision(thread)}
	generatedReply := &projectChatManifestReply{ManifestVersion: projectChatSourceManifestV3, MessageID: "generated-image-message",
		LegacyDigest: projectChatMessageSourceDigestV3(thread.Messages[0]), Media: []projectChatManifestReplyMedia{generatedMedia}}
	if governed, authorized, _ := app.openAIProjectReplyMediaContentVerdict("aj@shareability.com", thread.ID, generatedReply); !authorized || len(governed) != 1 {
		t.Fatalf("governed generated reply media authorized=%v content=%+v", authorized, governed)
	}
	if !app.projectChatReplyMediaManifestCurrent("aj@shareability.com", thread, generatedReply) {
		t.Fatal("current generated reply manifest rejected before canonical commit")
	}
	if oldReply := *generatedReply; func() bool {
		oldReply.ManifestVersion = projectChatSourceManifestVersion
		oldReply.Media = nil
		governed, authorized, _ := app.openAIProjectReplyMediaContentVerdict("aj@shareability.com", thread.ID, &oldReply)
		return authorized && len(governed) == 0
	}() == false {
		t.Fatal("released v2 reply did not preserve media withholding")
	}
	if ordinary := app.openAIReplyMediaContentForTurn(false, "aj@shareability.com", thread.ID, "generated-image-message"); len(ordinary) != 1 {
		t.Fatalf("ordinary generated-image reply lost media: %+v", ordinary)
	}
	if leaked := app.openAIReplyMediaContent("outsider@example.com", thread.ID, "generated-image-message"); len(leaked) != 0 {
		t.Fatalf("unauthorized reply media leaked: %+v", leaked)
	}

	user := accountStore().findUser("aj@shareability.com")
	granted := grantTestPendingAttachment(t, app, user, thread, ref)
	response, err := app.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "File parent", []scoutChatFileAttachment{{
		Name: "parent.png", Mime: "image/png", Ref: ref, SourceID: granted.SourceID, SourceRevision: granted.SourceRevision,
	}}, "")
	if err != nil {
		t.Fatalf("append file parent: %v", err)
	}
	fileParent, ok := response["message"].(scoutChatMessageRecord)
	if !ok || fileParent.ID == "" {
		t.Fatalf("file parent response=%T %+v", response["message"], response["message"])
	}
	if ordinary := app.openAIReplyMediaContentForTurn(false, user.Email, thread.ID, fileParent.ID); len(ordinary) != 1 {
		t.Fatalf("ordinary file reply lost media: %+v", ordinary)
	}
	if linked := app.openAIReplyMediaContentForTurn(true, user.Email, thread.ID, fileParent.ID); len(linked) != 0 {
		t.Fatalf("Project-linked file parent reached provider media: %+v", linked)
	}
	freshThread, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	fileIndex := scoutChatMessageIndex(freshThread, fileParent.ID)
	if fileIndex < 0 {
		t.Fatal("file parent missing")
	}
	fileMeta, err := blobStatForRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	fileMedia := projectChatManifestReplyMedia{Ordinal: 0, Kind: "file", SourceID: granted.SourceID, SourceRevision: granted.SourceRevision,
		BlobRef: ref, BlobDigest: ref, Mime: "image/png", Size: fileMeta.Size, DestinationRevision: scoutChatAttachmentDestinationRevision(freshThread)}
	fileReply := &projectChatManifestReply{ManifestVersion: projectChatSourceManifestV3, MessageID: fileParent.ID,
		AuthorEmail: fileParent.AuthorEmail, LegacyDigest: projectChatMessageSourceDigestV3(freshThread.Messages[fileIndex]),
		Media: []projectChatManifestReplyMedia{fileMedia}}
	if governed, authorized, _ := app.openAIProjectReplyMediaContentVerdict(user.Email, thread.ID, fileReply); !authorized || len(governed) != 1 {
		t.Fatalf("governed file reply media authorized=%v content=%+v", authorized, governed)
	}
	if !app.projectChatReplyMediaManifestCurrent(user.Email, freshThread, fileReply) {
		t.Fatal("current file reply manifest rejected before canonical commit")
	}
	fileReply.Media[0].SourceRevision = "sha256:stale"
	if governed, authorized, failedSource := app.openAIProjectReplyMediaContentVerdict(user.Email, thread.ID, fileReply); authorized || len(governed) != 0 || failedSource != granted.SourceID {
		t.Fatalf("stale governed reply media authorized=%v content=%+v", authorized, governed)
	}
}

func TestSanitizeScoutChatFilesDoesNotWidenPrivateArtifactIntoPublicChannel(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	private, _, err := kanbanApp.createOSArtifactWithMetadata("research", "private deck", "private", user.Name, map[string]string{
		"visibility":  scoutChatVisibilityPrivate,
		"requestedBy": user.Email,
	})
	if err != nil {
		t.Fatalf("create private artifact: %v", err)
	}
	ref, err := putBlob(tinyPNG(t), "image/png")
	if err != nil {
		t.Fatalf("put private blob: %v", err)
	}
	if _, err := kanbanApp.appendArtifactAsset(private.ID, artifactAsset{Ref: ref, Mime: "image/png", Kind: "image"}); err != nil {
		t.Fatalf("append private asset: %v", err)
	}
	publicThread, err := kanbanApp.createScoutChatThread(user.Email, user.Name, "team", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create public thread: %v", err)
	}
	if _, err := kanbanApp.sanitizeScoutChatFiles(context.Background(), user, publicThread, []scoutChatFileAttachment{{Name: "private.png", Ref: ref}}, "private-widen"); err == nil {
		t.Fatal("private artifact widened into public chat or downgraded to a name-only chip")
	}
}

// The full keyed send path: the derived-text pass transcribes the attachment
// into file.Text before the commit, and the Q&A turn carries the binary
// blocks so Scout actually sees the image.
func TestScoutChatAttachmentDerivedTextAndVisionQnA(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = "sk-openai-test"
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("OPENAI_API_KEY", "sk-openai-test")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")

	pngRef, err := putBlob([]byte("raster bytes"), "image/png")
	if err != nil {
		t.Fatalf("putBlob: %v", err)
	}

	swapAnthropicMessagesResponder(t, func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		t.Fatal("installed Anthropic key received core Scout attachment traffic")
		return anthropicMessagesResponse{}, nil
	})
	swapAnthropicTextResponder(t, func(context.Context, string, anthropicTextRequest) (string, error) {
		t.Fatal("installed Anthropic key received core Scout attachment traffic")
		return "", nil
	})

	const transcription = "Deck claims: $2M ARR, 40% MoM growth, pilot with StationTenn."
	var textRequests []openAITextRequest
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		textRequests = append(textRequests, request)
		switch request.Workflow {
		case "scout_route":
			return `{"tool":"","imagePrompt":"","workstreamTitle":"","workstreamInstructions":"","goalObjective":"","goalScope":[],"goalAcceptanceCriteria":[],"goalExclusions":[],"goalConstraints":[],"goalQuestions":[],"goalReasons":[],"choices":[]}`, nil
		case "attachment_extract":
			return transcription, nil
		case "scout_chat":
			return "It says ARR is $2M.", nil
		default:
			t.Fatalf("unexpected OpenAI workflow %q", request.Workflow)
			return "", nil
		}
	})

	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Deck check", "")
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	grantedFile := grantTestPendingAttachment(t, kanbanApp, user, thread, pngRef)

	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "what does this deck say?", []scoutChatFileAttachment{
		{Name: "deck.png", Kind: "png", Size: 12, Ref: pngRef, Text: "client junk that must be stripped", SourceID: grantedFile.SourceID, SourceRevision: grantedFile.SourceRevision},
	}, "")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	if len(textRequests) != 3 {
		t.Fatalf("text seam calls=%d, want route + derive + Q&A", len(textRequests))
	}
	derive := textRequests[0]
	qna := textRequests[0]
	for _, request := range textRequests {
		if request.Workflow == "attachment_extract" {
			derive = request
		}
		if request.Workflow == "scout_chat" {
			qna = request
		}
	}
	if len(derive.Attachments) != 1 || derive.Instructions != attachmentDeriveInstructions || derive.MaxOutputTokens != attachmentDeriveMaxTokens || derive.Model != defaultScoutExtractionModel {
		t.Fatalf("derive request=%+v, want one attachment block under the transcription budget", derive)
	}
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

func TestScoutChatAttachmentUnavailableReportsEveryProviderAttempt(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = "sk-openai-test"
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("OPENAI_API_KEY", "sk-openai-test")

	pngRef, err := putBlob([]byte("raster bytes"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		switch request.Workflow {
		case "attachment_extract":
			return "Source facts extracted.", nil
		case "scout_route":
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
				Outcome: string(conversationIntentUnavailable), Message: "That work is not admitted yet.",
			}), nil
		default:
			return "", fmt.Errorf("unexpected workflow %q", request.Workflow)
		}
	})
	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Unavailable attachment", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("aj@shareability.com")
	granted := grantTestPendingAttachment(t, kanbanApp, user, thread, pngRef)
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "Create the unavailable deliverable from this", []scoutChatFileAttachment{{
		Name: "source.png", Kind: "png", Size: 12, Ref: pngRef, SourceID: granted.SourceID, SourceRevision: granted.SourceRevision,
	}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || response["providerCalls"] != 2 || response["intentOutcome"] != string(conversationIntentUnavailable) {
		t.Fatalf("calls=%d response=%#v, want two truthful provider attempts and unavailable", calls, response)
	}
}

// An installed Anthropic key is never selected for the core attachment path.
func TestScoutChatAttachmentInstalledAnthropicKeyDoesNotReceiveCoreTraffic(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.mu.Lock()
	kanbanApp.apiKey = "test-openai-key"
	kanbanApp.mu.Unlock()
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-installed")

	pngRef, err := putBlob([]byte("raster bytes"), "image/png")
	if err != nil {
		t.Fatalf("putBlob: %v", err)
	}

	swapAnthropicTextResponder(t, func(context.Context, string, anthropicTextRequest) (string, error) {
		t.Fatal("Anthropic text seam must not be touched by core Scout attachments")
		return "", nil
	})
	swapAnthropicMessagesResponder(t, func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		t.Fatal("Anthropic router seam must not be touched by core Scout attachments")
		return anthropicMessagesResponse{}, nil
	})
	var gotInput string
	const transcription = "OpenAI extracted attachment facts."
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		switch request.Workflow {
		case "scout_route":
			return `{"tool":"","imagePrompt":"","workstreamTitle":"","workstreamInstructions":"","goalObjective":"","goalScope":[],"goalAcceptanceCriteria":[],"goalExclusions":[],"goalConstraints":[],"goalQuestions":[],"goalReasons":[],"choices":[]}`, nil
		case "attachment_extract":
			return transcription, nil
		case "scout_chat":
			gotInput = request.Input
			return "The attachment was read through OpenAI.", nil
		default:
			return "", fmt.Errorf("unexpected workflow %q", request.Workflow)
		}
	})

	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Deck check", "")
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	grantedFile := grantTestPendingAttachment(t, kanbanApp, user, thread, pngRef)

	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "what does this deck say?", []scoutChatFileAttachment{
		{Name: "deck.png", Kind: "png", Size: 12, Ref: pngRef, SourceID: grantedFile.SourceID, SourceRevision: grantedFile.SourceRevision},
	}, "")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}
	if !strings.Contains(gotInput, transcription) {
		t.Fatalf("OpenAI Q&A input=%q, want derived attachment text", gotInput)
	}
	saved, ok := response["thread"].(scoutChatThreadRecord)
	if !ok {
		t.Fatalf("response thread=%T, want scoutChatThreadRecord", response["thread"])
	}
	file := saved.Messages[0].Files[0]
	if file.Text != transcription {
		t.Fatalf("derived text=%q, want OpenAI extraction", file.Text)
	}
	if file.Ref != pngRef {
		t.Fatalf("ref=%q, want the ref preserved for the render path", file.Ref)
	}
}

func TestPublicHumanAttachmentCommitsBeforeDeferredDerivation(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = "sk-openai-test"
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("OPENAI_API_KEY", "sk-openai-test")

	ref, err := putBlob([]byte("public channel raster"), "image/png")
	if err != nil {
		t.Fatalf("putBlob: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Instructions != attachmentDeriveInstructions {
			t.Errorf("unexpected async request instructions=%q", request.Instructions)
		}
		close(started)
		<-release
		return "Launch note says September 8 with a $40,000 budget.", nil
	})

	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "team", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create public channel: %v", err)
	}
	user := accountStore().findUser("aj@shareability.com")
	grantedFile := grantTestPendingAttachment(t, kanbanApp, user, thread, ref)
	type appendResult struct {
		response map[string]any
		err      error
	}
	result := make(chan appendResult, 1)
	go func() {
		response, appendErr := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "latest launch note", []scoutChatFileAttachment{{Name: "launch.png", Ref: ref, SourceID: grantedFile.SourceID, SourceRevision: grantedFile.SourceRevision}}, "")
		result <- appendResult{response: response, err: appendErr}
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("deferred derivation did not start")
	}
	var appended appendResult
	select {
	case appended = <-result:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("public human message waited for attachment derivation")
	}
	if appended.err != nil {
		t.Fatalf("append message: %v", appended.err)
	}
	saved, ok := appended.response["thread"].(scoutChatThreadRecord)
	if !ok || len(saved.Messages) != 1 || len(saved.Messages[0].Files) != 1 {
		t.Fatalf("unexpected immediate response thread: %#v", appended.response["thread"])
	}
	if saved.Messages[0].Files[0].Text != "" {
		t.Fatalf("immediate commit already carried derived text %q", saved.Messages[0].Files[0].Text)
	}
	messageID := saved.Messages[0].ID
	close(release)
	released = true

	deadline := time.Now().Add(3 * time.Second)
	for {
		current, _, readErr := kanbanApp.scoutChatThreadByID(user.Email, thread.ID)
		if readErr != nil {
			t.Fatalf("read enriched thread: %v", readErr)
		}
		index := scoutChatMessageIndex(current, messageID)
		if index >= 0 && current.Messages[index].Files[0].Text == "Launch note says September 8 with a $40,000 budget." {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("deferred text did not persist onto message %s: %+v", messageID, current.Messages)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAttachmentDerivationDeferralExcludesScoutAndActionFlows(t *testing.T) {
	file := []scoutChatFileAttachment{{Name: "brief.pdf", Ref: strings.Repeat("a", 64)}}
	public := scoutChatThreadRecord{Visibility: scoutChatVisibilityPublic}
	private := scoutChatThreadRecord{}
	if !shouldDeferScoutChatAttachmentDerivation(public, "for the team", file, "", "") {
		t.Fatal("ordinary public human attachment should defer")
	}
	for name, deferred := range map[string]bool{
		"scout mention":        shouldDeferScoutChatAttachmentDerivation(public, "@Scout read this", file, "", ""),
		"artifact follow-up":   shouldDeferScoutChatAttachmentDerivation(public, "feedback", file, "artifact-1", ""),
		"tool launch":          shouldDeferScoutChatAttachmentDerivation(public, "run it", file, "", "research"),
		"private Scout thread": shouldDeferScoutChatAttachmentDerivation(private, "read this", file, "", ""),
		"brain intake":         shouldDeferScoutChatAttachmentDerivation(scoutChatThreadRecord{Visibility: scoutChatVisibilityPublic, Intake: brainIntakeKind}, "contribution", file, "", ""),
	} {
		if deferred {
			t.Fatalf("%s unexpectedly deferred attachment derivation", name)
		}
	}
}

func TestDeferredAttachmentEnrichmentDoesNotResurrectDeletedMessage(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "sk-openai-test"
	t.Setenv("OPENAI_API_KEY", "sk-openai-test")
	ref, err := putBlob([]byte("delete race raster"), "image/png")
	if err != nil {
		t.Fatalf("putBlob: %v", err)
	}
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "team", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	user := accountStore().findUser("aj@shareability.com")
	file := grantTestPendingAttachment(t, app, user, thread, ref)
	file.Name = "delete.png"
	file.Mime = "image/png"
	reservationID := "deferred-delete-reservation"
	meta, err := blobStatForRef(ref)
	if err != nil || app.reservePendingAttachmentUpload(user, thread, file, meta, reservationID) != nil {
		t.Fatalf("reserve deferred source: %v", err)
	}
	message := scoutChatMessageRecord{ID: "deferred-delete-message", Kind: "message", Role: "user", Text: "temporary", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: "aj@shareability.com", Files: []scoutChatFileAttachment{file}, attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(thread), attachmentReservationID: reservationID}
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", thread.ID, message); err != nil {
		t.Fatalf("commit message: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	swapOpenAITextResponder(t, func(context.Context, string, openAITextRequest) (string, error) {
		close(started)
		<-release
		return "stale derived text", nil
	})
	done := make(chan error, 1)
	go func() {
		done <- app.enrichScoutChatMessageAttachments(context.Background(), "aj@shareability.com", thread.ID, message.ID)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("enrichment did not start")
	}
	if _, err := app.deleteScoutChatThreadMessage("aj@shareability.com", thread.ID, message.ID); err != nil {
		t.Fatalf("delete message: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("enrichment after delete: %v", err)
	}
	current, _, err := app.scoutChatThreadByID("aj@shareability.com", thread.ID)
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if scoutChatMessageIndex(current, message.ID) >= 0 {
		t.Fatal("deferred enrichment resurrected a deleted message")
	}
}

func TestAttachmentSourceAuthoritySurvivesRestartAndReclaimsOrphanReservation(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "restart", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	ref, err := putBlob(tinyPNG(t), "image/png")
	if err != nil {
		t.Fatalf("put source: %v", err)
	}
	file := grantTestPendingAttachment(t, app, user, thread, ref)
	meta, err := blobStatForRef(ref)
	if err != nil {
		t.Fatalf("stat source: %v", err)
	}
	if err := app.reservePendingAttachmentUpload(user, thread, file, meta, "request-lost-on-restart"); err != nil {
		t.Fatalf("reserve before restart: %v", err)
	}

	restarted := newKanbanBoardApp()
	reloadedThread, _, err := restarted.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		t.Fatalf("reload destination after restart: %v", err)
	}
	if err := restarted.reservePendingAttachmentUpload(user, reloadedThread, file, meta, "request-after-restart"); err != nil {
		t.Fatalf("orphan reservation was not safely reclaimed after restart: %v", err)
	}
	message := scoutChatMessageRecord{
		ID:                            "restart-committed-message",
		Kind:                          "message",
		Role:                          "user",
		Text:                          "durable source",
		CreatedAt:                     time.Now().UTC().Format(time.RFC3339Nano),
		AuthorEmail:                   user.Email,
		Files:                         []scoutChatFileAttachment{file},
		attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(reloadedThread),
		attachmentReservationID:       "request-after-restart",
	}
	if _, err := restarted.commitScoutChatThreadMessages(user.Email, thread.ID, message); err != nil {
		t.Fatalf("commit after restart: %v", err)
	}

	restartedAgain := newKanbanBoardApp()
	if !restartedAgain.committedChatAttachmentAuthorized(user.Email, thread.ID, message.ID, file) {
		t.Fatal("committed source authority did not survive restart")
	}
	if err := restartedAgain.reservePendingAttachmentUpload(user, reloadedThread, file, meta, "replay-after-commit"); err == nil {
		t.Fatal("committed source authorized a replay after restart")
	}
}

func TestAttachmentCommitFailureReleasesReservationForRetry(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("ANTHROPIC_API_KEY", "")
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "save failure", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	ref, err := putBlob(tinyPNG(t), "image/png")
	if err != nil {
		t.Fatalf("put source: %v", err)
	}
	file := grantTestPendingAttachment(t, app, user, thread, ref)
	file.Name = "retry.png"
	originalMemoryPath := app.memory.path
	app.memory.path = t.TempDir() // rename onto a directory must fail.
	if _, err := app.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "first try", []scoutChatFileAttachment{file}, ""); err == nil {
		t.Fatal("save failure unexpectedly succeeded")
	}
	app.pendingAttachmentUploadsMu.Lock()
	stateAfterFailure := app.pendingAttachmentUploads[file.SourceID].State
	app.pendingAttachmentUploadsMu.Unlock()
	if stateAfterFailure != attachmentSourcePending {
		t.Fatalf("source state=%q after save failure, want pending for safe retry", stateAfterFailure)
	}
	app.memory.path = originalMemoryPath
	retryDone := make(chan error, 1)
	go func() {
		_, retryErr := app.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "retry", []scoutChatFileAttachment{file}, "")
		retryDone <- retryErr
	}()
	select {
	case err := <-retryDone:
		if err != nil {
			t.Fatalf("retry after save failure: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("successful attachment commit deadlocked during projection/delivery")
	}
	app.pendingAttachmentUploadsMu.Lock()
	stateAfterRetry := app.pendingAttachmentUploads[file.SourceID].State
	app.pendingAttachmentUploadsMu.Unlock()
	if stateAfterRetry != attachmentSourceCommitted {
		t.Fatalf("source state=%q after retry, want committed", stateAfterRetry)
	}
}

func TestAttachmentRestartReconcilesChatSavedBeforeAuthorityFinalization(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "ambiguous finalize", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	ref, err := putBlob(tinyPNG(t), "image/png")
	if err != nil {
		t.Fatalf("put source: %v", err)
	}
	file := grantTestPendingAttachment(t, app, user, thread, ref)
	meta, _ := blobStatForRef(ref)
	const reservationID = "ambiguous-finalize-reservation"
	if err := app.reservePendingAttachmentUpload(user, thread, file, meta, reservationID); err != nil {
		t.Fatalf("reserve source: %v", err)
	}
	previousWriter := attachmentSourceStoreWriter
	failOnce := true
	attachmentSourceStoreWriter = func(state attachmentSourceStoreState) error {
		if failOnce {
			failOnce = false
			return fmt.Errorf("synthetic authority fsync failure")
		}
		return previousWriter(state)
	}
	t.Cleanup(func() { attachmentSourceStoreWriter = previousWriter })
	message := scoutChatMessageRecord{ID: "ambiguous-finalize-message", Kind: "message", Role: "user", Text: "saved first", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorEmail: user.Email, Files: []scoutChatFileAttachment{file}, attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(thread), attachmentReservationID: reservationID}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, message); err == nil || !strings.Contains(err.Error(), "finalization is ambiguous") {
		t.Fatalf("commit err=%v, want explicit ambiguous finalization", err)
	}
	stored, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil || scoutChatMessageIndex(stored, message.ID) < 0 {
		t.Fatalf("chat message was not durably saved before authority failure: err=%v thread=%+v", err, stored)
	}

	attachmentSourceStoreWriter = previousWriter
	restarted := newKanbanBoardApp()
	if !restarted.committedChatAttachmentAuthorized(user.Email, thread.ID, message.ID, file) {
		t.Fatal("restart did not reconcile the reserved source from the exact committed chat message")
	}
}

func TestAttachmentEditPreservesCommittedSourceAndAddsNewAuthorizedSource(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("ANTHROPIC_API_KEY", "")
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "mixed edit", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	firstRef, err := putBlob(tinyPNG(t), "image/png")
	if err != nil {
		t.Fatalf("put first source: %v", err)
	}
	first := grantTestPendingAttachment(t, app, user, thread, firstRef)
	first.Name = "first.png"
	first.Mime = "image/png"
	firstMeta, _ := blobStatForRef(firstRef)
	if err := app.reservePendingAttachmentUpload(user, thread, first, firstMeta, "mixed-edit-first"); err != nil {
		t.Fatalf("reserve first source: %v", err)
	}
	message := scoutChatMessageRecord{ID: "mixed-edit-message", Kind: "message", Role: "user", Text: "first", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorEmail: user.Email, Files: []scoutChatFileAttachment{first}, attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(thread), attachmentReservationID: "mixed-edit-first"}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, message); err != nil {
		t.Fatalf("commit first source: %v", err)
	}
	duplicateText := "duplicate source"
	if _, _, err := app.editScoutChatThreadMessage(context.Background(), user, thread.ID, message.ID, &duplicateText, &[]scoutChatFileAttachment{first, first}); err == nil || !strings.Contains(err.Error(), "same attachment") {
		t.Fatalf("duplicate source edit err=%v, want duplicate attachment rejection", err)
	}

	secondBytes := tinyPNG(t)
	secondBytes = append(secondBytes, 0) // distinct immutable digest; still valid PNG.
	secondRef, err := putBlob(secondBytes, "image/png")
	if err != nil {
		t.Fatalf("put second source: %v", err)
	}
	second := grantTestPendingAttachment(t, app, user, thread, secondRef)
	second.Name = "second.png"
	second.Mime = "image/png"
	updatedText := "two files"
	type editResult struct {
		message scoutChatMessageRecord
		err     error
	}
	editDone := make(chan editResult, 1)
	go func() {
		_, editedMessage, editErr := app.editScoutChatThreadMessage(context.Background(), user, thread.ID, message.ID, &updatedText, &[]scoutChatFileAttachment{first, second})
		editDone <- editResult{message: editedMessage, err: editErr}
	}()
	var edited scoutChatMessageRecord
	select {
	case result := <-editDone:
		if result.err != nil {
			t.Fatalf("mixed attachment edit: %v", result.err)
		}
		edited = result.message
	case <-time.After(2 * time.Second):
		t.Fatal("successful attachment edit deadlocked during projection/delivery")
	}
	if len(edited.Files) != 2 || edited.Files[0].SourceID != first.SourceID || edited.Files[1].SourceID != second.SourceID {
		t.Fatalf("edited files=%+v, want committed first source plus newly authorized second source", edited.Files)
	}
	app.pendingAttachmentUploadsMu.Lock()
	firstState := app.pendingAttachmentUploads[first.SourceID]
	secondState := app.pendingAttachmentUploads[second.SourceID]
	app.pendingAttachmentUploadsMu.Unlock()
	if firstState.State != attachmentSourceCommitted || firstState.CommittedMessageID != message.ID || secondState.State != attachmentSourceCommitted || secondState.CommittedMessageID != message.ID {
		t.Fatalf("source states after mixed edit first=%+v second=%+v", firstState, secondState)
	}
}

func TestAttachmentBlobReadDropsBytesRevokedDuringRead(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "revoke read", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	ref, err := putBlob(tinyPNG(t), "image/png")
	if err != nil {
		t.Fatalf("put source: %v", err)
	}
	file := grantTestPendingAttachment(t, app, user, thread, ref)
	meta, _ := blobStatForRef(ref)
	const reservationID = "read-revocation"
	if err := app.reservePendingAttachmentUpload(user, thread, file, meta, reservationID); err != nil {
		t.Fatalf("reserve source: %v", err)
	}
	previousProbe := attachmentBlobReadAfterProbe
	attachmentBlobReadAfterProbe = func(sourceID string) {
		if sourceID == file.SourceID {
			_ = app.revokeAttachmentSource(sourceID)
		}
	}
	t.Cleanup(func() { attachmentBlobReadAfterProbe = previousProbe })
	if blocks := app.attachmentContentBlocksAuthorized(user, thread, []scoutChatFileAttachment{file}, reservationID); len(blocks) != 0 {
		t.Fatalf("revoked source produced %d model blocks", len(blocks))
	}
}

func TestCommittedOpenAIAttachmentVerdictSeparatesAuthorizationFromBlockAdmission(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "provider verdict", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name string
		data []byte
		mime string
	}{
		{name: "unsupported_gif", data: []byte("GIF89a"), mime: "image/gif"},
		{name: "over_budget_image", data: make([]byte, anthropicMaxRequestImageBytes+1), mime: "image/png"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ref, err := putBlob(testCase.data, testCase.mime)
			if err != nil {
				t.Fatal(err)
			}
			file := grantTestPendingAttachment(t, app, user, thread, ref)
			file.Name = testCase.name
			messageID := "provider-verdict-" + testCase.name
			meta, _ := blobStatForRef(ref)
			reservationID := messageID + "-reservation"
			if err := app.reservePendingAttachmentUpload(user, thread, file, meta, reservationID); err != nil {
				t.Fatal(err)
			}
			message := scoutChatMessageRecord{
				ID: messageID, Kind: "message", Role: "user", Text: "provider verdict",
				CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorEmail: user.Email,
				Files: []scoutChatFileAttachment{file}, attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(thread),
				attachmentReservationID: reservationID,
			}
			if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, message); err != nil {
				t.Fatal(err)
			}
			blocks, authorized := app.committedOpenAIAttachmentContentVerdict(user.Email, thread.ID, messageID,
				[]scoutChatFileAttachment{file})
			if !authorized || len(blocks) != 0 {
				t.Fatalf("authorized=%v blocks=%d, want valid source with intentional omission", authorized, len(blocks))
			}
		})
	}
	ref, err := putBlob(tinyPNG(t), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	file := grantTestPendingAttachment(t, app, user, thread, ref)
	messageID := "provider-verdict-revoked"
	meta, _ := blobStatForRef(ref)
	reservationID := messageID + "-reservation"
	if err := app.reservePendingAttachmentUpload(user, thread, file, meta, reservationID); err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{
		ID: messageID, Kind: "message", Role: "user", Text: "provider verdict",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorEmail: user.Email,
		Files: []scoutChatFileAttachment{file}, attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(thread),
		attachmentReservationID: reservationID,
	}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, message); err != nil {
		t.Fatal(err)
	}
	previousProbe := attachmentBlobReadAfterProbe
	attachmentBlobReadAfterProbe = func(sourceID string) {
		if sourceID == file.SourceID {
			_ = app.revokeAttachmentSource(sourceID)
		}
	}
	t.Cleanup(func() { attachmentBlobReadAfterProbe = previousProbe })
	blocks, authorized := app.committedOpenAIAttachmentContentVerdict(user.Email, thread.ID, messageID,
		[]scoutChatFileAttachment{file})
	if authorized || len(blocks) != 0 {
		t.Fatalf("revoked during read authorized=%v blocks=%d", authorized, len(blocks))
	}
}

func TestAttachmentDerivedTextIsDiscardedWhenRevokedDuringModelRead(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("OPENAI_API_KEY", "sk-openai-test")
	app.apiKey = "sk-openai-test"
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "revoke model", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	ref, err := putBlob(tinyPNG(t), "image/png")
	if err != nil {
		t.Fatalf("put source: %v", err)
	}
	file := grantTestPendingAttachment(t, app, user, thread, ref)
	file.Name = "private.png"
	meta, _ := blobStatForRef(ref)
	const reservationID = "model-revocation"
	if err := app.reservePendingAttachmentUpload(user, thread, file, meta, reservationID); err != nil {
		t.Fatalf("reserve source: %v", err)
	}
	attachments := app.openAIAttachmentContentAuthorized(user, thread, []scoutChatFileAttachment{file}, reservationID)
	if len(attachments) != 1 {
		t.Fatalf("authorized attachments=%d, want 1", len(attachments))
	}
	swapOpenAITextResponder(t, func(context.Context, string, openAITextRequest) (string, error) {
		if err := app.revokeAttachmentSource(file.SourceID); err != nil {
			t.Errorf("revoke source during provider call: %v", err)
		}
		return "confidential derived text", nil
	})
	derived := app.deriveAttachmentTextAuthorized(context.Background(), user, thread, []scoutChatFileAttachment{file}, reservationID, attachments)
	if len(derived) != 1 || strings.TrimSpace(derived[0].Text) != "" {
		t.Fatalf("revoked model output persisted into attachment: %+v", derived)
	}
}

func TestCommittedAttachmentProjectionFailsClosedAcrossReaderSurfaces(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	owner := accountStore().findUser("aj@shareability.com")
	viewer := accountStore().findUser("tim@shareability.com")
	if owner == nil || viewer == nil {
		t.Fatal("seed users missing")
	}
	thread, err := app.createScoutChatThread(owner.Email, owner.Name, "projection", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	ref, err := putBlob(tinyPNG(t), "image/png")
	if err != nil {
		t.Fatalf("put source: %v", err)
	}
	const reservationID = "projection-reservation"
	file := reserveTestAttachment(t, app, owner, thread, scoutChatFileAttachment{
		Name: "PRIVATE-ATTACHMENT-CANARY.png",
		Kind: "png",
		Ref:  ref,
	}, reservationID)
	file.Text = "PRIVATE-DERIVED-TEXT-CANARY"
	message := scoutChatMessageRecord{
		ID:                            "projection-message",
		Kind:                          "message",
		Role:                          "user",
		Text:                          "ordinary chat text",
		CreatedAt:                     time.Now().UTC().Format(time.RFC3339Nano),
		AuthorName:                    owner.Name,
		AuthorEmail:                   owner.Email,
		Files:                         []scoutChatFileAttachment{file},
		attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(thread),
		attachmentReservationID:       reservationID,
	}
	if _, err := app.commitScoutChatThreadMessages(owner.Email, thread.ID, message); err != nil {
		t.Fatalf("commit attachment: %v", err)
	}
	thread, _, err = app.scoutChatThreadByID(viewer.Email, thread.ID)
	if err != nil {
		t.Fatalf("load public thread: %v", err)
	}
	if got := app.projectScoutChatThreadForViewer(viewer.Email, thread); len(got.Messages) != 1 || len(got.Messages[0].Files) != 1 {
		t.Fatalf("healthy projection=%+v, want visible committed attachment", got)
	}

	assertNoAttachmentLeak := func(label string, value any) {
		t.Helper()
		body, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatalf("%s marshal: %v", label, marshalErr)
		}
		for _, secret := range []string{"PRIVATE-ATTACHMENT-CANARY", "PRIVATE-DERIVED-TEXT-CANARY", ref, "image/png"} {
			if strings.Contains(string(body), secret) {
				t.Fatalf("%s leaked %q: %s", label, secret, body)
			}
		}
	}
	assertClosed := func(label string) {
		t.Helper()
		projected := app.projectScoutChatThreadForViewer(viewer.Email, thread)
		if len(projected.Messages) != 1 || len(projected.Messages[0].Files) != 0 {
			t.Fatalf("%s thread files=%+v, want none", label, projected.Messages)
		}
		assertNoAttachmentLeak(label+" thread", projected)
		files := app.fileRecordsFromThread(viewer.Email, thread)
		if len(files) != 0 {
			t.Fatalf("%s Files rows=%+v, want none", label, files)
		}
		assertNoAttachmentLeak(label+" Files", files)
		deposits := threadDeposits(projected.Messages)
		if len(deposits.Files) != 0 {
			t.Fatalf("%s deposits=%+v, want no attachment deposit", label, deposits)
		}
		assertNoAttachmentLeak(label+" deposits", deposits)
		history := app.scoutChatHistoryForViewer(viewer.Email, thread)
		assertNoAttachmentLeak(label+" model history", history)
		payload := app.scoutChatThreadUpdatePayload(viewer.Email, thread, thread.Messages[0])
		assertNoAttachmentLeak(label+" event", payload)
	}

	app.pendingAttachmentUploadsMu.Lock()
	app.attachmentSourceStoreErr = fmt.Errorf("attachment authority test outage")
	app.pendingAttachmentUploadsMu.Unlock()
	assertClosed("unhealthy authority")
	app.pendingAttachmentUploadsMu.Lock()
	app.attachmentSourceStoreErr = nil
	app.pendingAttachmentUploadsMu.Unlock()
	if err := app.revokeAttachmentSource(file.SourceID); err != nil {
		t.Fatalf("revoke source: %v", err)
	}
	assertClosed("revoked authority")
}

func TestArtifactBlobDownloadReauthorizesAfterRead(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "download race", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	ref, err := putBlob(tinyPNG(t), "image/png")
	if err != nil {
		t.Fatalf("put source: %v", err)
	}
	file := grantTestPendingAttachment(t, app, user, thread, ref)
	meta, _ := blobStatForRef(ref)
	const reservationID = "download-race-reservation"
	if err := app.reservePendingAttachmentUpload(user, thread, file, meta, reservationID); err != nil {
		t.Fatalf("reserve source: %v", err)
	}
	message := scoutChatMessageRecord{ID: "download-race-message", Kind: "message", Role: "user", Text: "delete me", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorEmail: user.Email, Files: []scoutChatFileAttachment{file}, attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(thread), attachmentReservationID: reservationID}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, message); err != nil {
		t.Fatalf("commit attachment: %v", err)
	}
	previousProbe := artifactBlobAfterReadProbe
	artifactBlobAfterReadProbe = func(string) {
		_, _ = app.deleteScoutChatThreadMessage(user.Email, thread.ID, message.ID)
	}
	t.Cleanup(func() { artifactBlobAfterReadProbe = previousProbe })
	cookies := loginAs(t, user.Email, "B0NFIRE!")
	request := httptest.NewRequest(http.MethodGet, "/artifacts/blob?ref="+ref, nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	artifactBlobHandler(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("download status=%d body=%q, want 404 after authority was revoked during read", recorder.Code, recorder.Body.String())
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
	user := accountStore().findUser("aj@shareability.com")
	reservationID := "sweep-chat-attachment"
	chatFile := reserveTestAttachment(t, app, user, thread, scoutChatFileAttachment{Name: "deck.png", Ref: chatRef}, reservationID)
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", thread.ID, scoutChatMessageRecord{
		ID:                            "scout-chat-message-1",
		Kind:                          "message",
		Role:                          "user",
		Text:                          "the deck",
		Files:                         []scoutChatFileAttachment{chatFile},
		attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(thread),
		attachmentReservationID:       reservationID,
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
