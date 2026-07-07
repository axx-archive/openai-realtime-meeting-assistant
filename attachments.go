package main

// Chat attachment ingestion (card 085) — the missing three seams between the
// composer, the content-addressed blob store (blobs.go), and Scout's
// Anthropic calls:
//
//  1. POST /assistant/attachments uploads one image/PDF binary into putBlob
//     and returns its ref, so message records carry refs instead of dropped
//     bytes (the frontend previously read only text-like files).
//  2. attachmentContentBlocks turns ref'd files into image/document content
//     blocks under the wave-5 request budgets, so the CURRENT turn's binaries
//     ride the model call (history keeps the bounded text placeholders).
//  3. deriveAttachmentText runs one bounded transcription pass whose output
//     lands in scoutChatFileAttachment.Text — the field every existing text
//     consumer (history folding, channel team replies, thread previews,
//     launch objectives) already reads, so downstream context is free.
//
// KEYLESS: uploads and chips still work (pure disk); the transcription pass
// and binary blocks are skipped, degrading to today's name-only behavior.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// attachmentUploadMaxBytes caps one composer upload at 25MB — generous
	// for decks and screenshots while staying far under the blob store's
	// 64MB ceiling and Anthropic's 32MB request cap after base64 expansion.
	attachmentUploadMaxBytes = 25 << 20

	// One PDF per message, ≤20MB decoded. This is the per-category ceiling;
	// the COMBINED image+PDF payload is separately bounded by
	// attachmentMaxRequestBytes so the two budgets can never sum past the
	// API's request cap.
	attachmentMaxPDFBlocks = 1
	attachmentMaxPDFBytes  = 20 << 20

	// attachmentMaxRequestBytes caps the combined decoded payload of every
	// image and document block in one message. base64 expands the whole body
	// ~1.33x, so 22MB decoded → ~29MB on the wire, leaving headroom under
	// Anthropic's 32MB request ceiling for the JSON envelope and text prompt.
	// Without this guard the independent 20MB image and 20MB PDF budgets could
	// sum to ~40MB decoded (~53MB base64) and the request would 413 opaquely.
	attachmentMaxRequestBytes = 22 << 20

	// The derived-text pass is bounded and best-effort: one sub-25s Sonnet
	// call whose failure never blocks the message commit.
	attachmentDeriveTimeout   = 25 * time.Second
	attachmentDeriveMaxTokens = 1200
)

// attachmentModelSafeMimes is the closed set of binary types the composer may
// upload and Scout's model calls may attach: the image types Anthropic's
// image blocks accept plus native PDF document blocks. Everything else keeps
// today's name-only chip path.
var attachmentModelSafeMimes = map[string]bool{
	"image/png":       true,
	"image/jpeg":      true,
	"image/webp":      true,
	"image/gif":       true,
	"application/pdf": true,
}

// attachmentUploadMime normalizes a Content-Type header down to its bare
// media type (parameters stripped, lowercased).
func attachmentUploadMime(header string) string {
	mime := strings.TrimSpace(header)
	if index := strings.Index(mime, ";"); index >= 0 {
		mime = mime[:index]
	}
	return strings.ToLower(strings.TrimSpace(mime))
}

// assistantAttachmentUploadHandler serves POST /assistant/attachments — the
// composer's binary upload door. Session-gated exactly like its
// /artifacts/blob neighbor (origin check, signed-in user); the raw body is
// the file, Content-Type declares the mime, and the response carries the
// content-addressed ref the message record will reference. Dedupe, mime
// pinning, and immutability all come free from putBlob.
func assistantAttachmentUploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if userFromRequest(r) == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	mime := attachmentUploadMime(r.Header.Get("Content-Type"))
	if !attachmentModelSafeMimes[mime] {
		writeAuthError(w, http.StatusUnsupportedMediaType, "attachments must be png, jpeg, webp, gif, or pdf")
		return
	}

	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, attachmentUploadMaxBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeAuthError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("attachment exceeds the %dMB cap", attachmentUploadMaxBytes>>20))
			return
		}
		writeAuthError(w, http.StatusBadRequest, "could not read attachment body")
		return
	}
	if len(data) == 0 {
		writeAuthError(w, http.StatusBadRequest, "attachment body is empty")
		return
	}

	ref, err := putBlob(data, mime)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The FIRST write pins the sidecar mime — a re-upload of known bytes
	// answers with the pinned value, exactly what the serve route will use.
	meta, err := blobStatForRef(ref)
	if err != nil {
		meta = blobMeta{Mime: mime, Size: int64(len(data))}
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"ref":  ref,
		"mime": meta.Mime,
		"size": meta.Size,
	})
}

// blobStatForRef is the cheap existence + mime check for a ref: one os.Stat
// on the data path plus the sidecar read — no full-file read or digest
// re-hash (getBlob does that when the bytes are actually needed).
func blobStatForRef(ref string) (blobMeta, error) {
	dataPath, metaPath, err := blobPaths(strings.TrimSpace(ref))
	if err != nil {
		return blobMeta{}, err
	}
	info, err := os.Stat(dataPath)
	if err != nil {
		return blobMeta{}, fmt.Errorf("blob not found")
	}
	meta := blobMeta{Mime: blobDefaultMime}
	if rawMeta, err := os.ReadFile(metaPath); err == nil {
		var parsed blobMeta
		if err := json.Unmarshal(rawMeta, &parsed); err == nil && strings.TrimSpace(parsed.Mime) != "" {
			meta = parsed
		}
	}
	meta.Size = info.Size()
	return meta, nil
}

// attachmentContentBlocks builds the model-facing content blocks for a
// message's ref'd binaries: image/* refs become base64 image blocks,
// application/pdf refs become document blocks. getBlob re-verifies each
// digest, so a corrupted blob degrades to no block — never wrong bytes. The
// wave-5 budgets are enforced here (≤12 images / ~20MB decoded, plus the
// 1-PDF/20MB document budget) alongside a combined ≤22MB decoded cap across
// both categories so the two budgets can't sum past the API's request
// ceiling; an over-budget or unreadable file silently keeps its text
// placeholder instead of failing the send.
func attachmentContentBlocks(files []scoutChatFileAttachment) []json.RawMessage {
	var blocks []json.RawMessage
	images, pdfs := 0, 0
	imageBytes, pdfBytes := 0, 0
	for _, file := range files {
		ref := strings.TrimSpace(file.Ref)
		if !validBlobRef(ref) {
			continue
		}
		data, meta, err := getBlob(ref)
		if err != nil {
			log.Warnf("Skipping unreadable chat attachment blob %s: %v", ref, err)
			continue
		}
		mime := strings.ToLower(strings.TrimSpace(meta.Mime))
		if !attachmentModelSafeMimes[mime] {
			continue
		}
		// Combined guard: base64 expands the sum of all blocks, not each
		// category in isolation, so admitting this file must not push the
		// total decoded payload past the shared request budget.
		if imageBytes+pdfBytes+len(data) > attachmentMaxRequestBytes {
			continue
		}
		if mime == "application/pdf" {
			if pdfs+1 > attachmentMaxPDFBlocks || pdfBytes+len(data) > attachmentMaxPDFBytes {
				continue
			}
			pdfs++
			pdfBytes += len(data)
			blocks = append(blocks, anthropicDocumentBlock(mime, data))
			continue
		}
		if images+1 > anthropicMaxRequestImages || imageBytes+len(data) > anthropicMaxRequestImageBytes {
			continue
		}
		images++
		imageBytes += len(data)
		blocks = append(blocks, anthropicImageBlock(mime, data))
	}
	return blocks
}

// attachmentDeriveInstructions is the transcription system prompt: the output
// persists into the thread record as shared team memory, so it must be the
// facts on the page, not commentary.
const attachmentDeriveInstructions = "You transcribe file attachments into a team's shared memory. " +
	"Extract the key facts, numbers, names, dates, and claims exactly as they appear. " +
	"Be concise and factual — no commentary, no advice. Stay under 700 words."

// deriveAttachmentText runs the bounded transcription pass over a message's
// ref'd binaries and stores the result into the first ref'd file's Text —
// the field scoutChatMessageModelText already folds into history, channel
// team replies, previews, and launch objectives, so every downstream text
// consumer inherits the attachment content with zero further plumbing.
// Best-effort: keyless deploys, timeouts, and refusals all leave files
// untouched and the send proceeds.
func deriveAttachmentText(ctx context.Context, files []scoutChatFileAttachment, blocks []json.RawMessage) []scoutChatFileAttachment {
	if len(blocks) == 0 {
		return files
	}
	target := -1
	for index, file := range files {
		if strings.TrimSpace(file.Ref) != "" && strings.TrimSpace(file.Text) == "" {
			target = index
			break
		}
	}
	if target < 0 {
		return files
	}
	apiKey := currentAnthropicAPIKey()
	if apiKey == "" {
		return files
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, attachmentDeriveTimeout)
	defer cancel()

	transcript, err := createAnthropicTextResponse(ctx, apiKey, anthropicTextRequest{
		Model:        chatModel(),
		Instructions: attachmentDeriveInstructions,
		Input:        "Transcribe the key facts, numbers, names, and claims in the attached files for the team's shared memory.",
		Effort:       "low",
		MaxTokens:    attachmentDeriveMaxTokens,
		Attachments:  blocks,
	})
	if err != nil {
		log.Warnf("Attachment transcription failed (message still sends): %v", err)
		return files
	}
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return files
	}
	if len(transcript) > scoutChatMaxFileTextBytes {
		transcript = transcript[:scoutChatMaxFileTextBytes]
		for !utf8.ValidString(transcript) && len(transcript) > 0 {
			transcript = transcript[:len(transcript)-1]
		}
		transcript = strings.TrimSpace(transcript) + "\n[truncated]"
	}
	files[target].Text = transcript
	return files
}
