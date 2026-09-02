package main

// Chat attachment ingestion (card 085), frontend half. Grep-style pins in the
// frontend_chat_mentions_test.go idiom: the composer uploads image/PDF
// binaries to POST /assistant/attachments and stamps ref+mime on the file
// payload (with client size caps and a name-only degrade), and the message
// renderer turns ref'd images into session-gated /artifacts/blob thumbnails
// and ref'd PDFs into new-tab viewer links.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForAttachments(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func TestIndexAttachmentUploadWiring(t *testing.T) {
	html := readIndexForAttachments(t)
	for _, want := range []string{
		// the composer's upload call + ref/mime stamping
		"fetch(`/assistant/attachments?threadId=${encodeURIComponent(destinationId)}`, {",
		"payload.ref = data.ref",
		"payload.mime = data.mime || type",
		"payload.sourceId = data.sourceId || ''",
		"payload.sourceRevision = data.sourceRevision || ''",
		// the exact model-safe allowlist, mirroring attachments.go
		"function scoutChatFileUploadable(type)",
		"['image/png', 'image/jpeg', 'image/webp', 'image/gif', 'application/pdf'].includes(type) || scoutChatVideoUploadable(type)",
		// hotfix gen 249: the phone/desktop video containers ride the same upload
		"function scoutChatVideoUploadable(type)",
		"['video/mp4', 'video/quicktime', 'video/webm', 'video/x-m4v'].includes(String(type || '').toLowerCase())",
		// client size caps: PDFs and videos 25MB (the server cap), images 8MB
		"type === 'application/pdf' || scoutChatVideoUploadable(type) ? 25 * 1024 * 1024 : 8 * 1024 * 1024",
		// a video's duration/frame size are probed locally and ride the payload
		"Object.assign(payload, await scoutChatProbeVideoFile(file))",
		"payload.width = Number(data.width)",
		// failure degrades to today's name-only chip, never a blocked send
		"upload failed — sending the name only",
		"is too large — sending the name only",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing attachment upload hook %q", want)
		}
	}

	// The upload branch lives inside scoutChatFilePayload, ahead of the
	// text-like read path it degrades to.
	start := strings.Index(html, "async function scoutChatFilePayload(file)")
	end := strings.Index(html, "function scoutChatFileLooksReadable(file)")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("cannot scope scoutChatFilePayload")
	}
	payloadBody := html[start:end]
	if !strings.Contains(payloadBody, "/assistant/attachments") {
		t.Fatal("scoutChatFilePayload must upload binaries to /assistant/attachments")
	}
	if !strings.Contains(payloadBody, "scoutChatFileLooksReadable(file) && file.size <= 256 * 1024") {
		t.Fatal("scoutChatFilePayload must keep the existing text-like read path")
	}
}

func TestIndexAttachmentRenderWiring(t *testing.T) {
	html := readIndexForAttachments(t)
	for _, want := range []string{
		// the session-gated blob URL builder
		"function scoutChatBlobUrl(ref, name)",
		"/artifacts/blob?ref=${encodeURIComponent(ref)}&name=${encodeURIComponent(name || 'file')}",
		// ref'd images render as inline thumbnails
		"scout-chat-file scout-chat-file--image",
		"openScoutChatImagePreview(blobHref, img.alt)",
		"scout-chat-image__expand",
		"img.className = 'scout-chat-file__thumb'",
		"img.loading = 'lazy'",
		// ref'd PDFs (and other ref'd non-images) open in a new tab
		"chip.classList.add('scout-chat-file--link')",
		// css for the thumbnail frame, beside the existing chip kit
		".scout-chat-file--image {",
		".scout-chat-file__thumb {",
		"--desktop-chat-image-preview-measure: 420px;",
		"max-height: 300px;",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing attachment render hook %q", want)
		}
	}

	// The ref branches live inside scoutChatFilesNode. Image previews stay in the
	// app's accessible lightbox; non-images that open a tab keep noopener.
	start := strings.Index(html, "function scoutChatFilesNode(files)")
	end := strings.Index(html, "function scoutChatFileMeta(file, { compact = false } = {})")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("cannot scope scoutChatFilesNode")
	}
	filesNode := html[start:end]
	for _, want := range []string{
		"mime.startsWith('image/')",
		"const frame = document.createElement('button')",
		"frame.setAttribute('aria-label', `Expand ${file.name",
		"chip.rel = 'noopener'",
	} {
		if !strings.Contains(filesNode, want) {
			t.Fatalf("scoutChatFilesNode missing %q", want)
		}
	}
}
