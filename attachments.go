package main

// Chat attachment ingestion (card 085) — the missing three seams between the
// composer, the content-addressed blob store (blobs.go), and model calls:
//
//  1. POST /assistant/attachments uploads one image/PDF binary into putBlob
//     and returns its ref, so message records carry refs instead of dropped
//     bytes (the frontend previously read only text-like files).
//  2. Provider adapters turn ref'd files into bounded image/document content
//     so the CURRENT turn's binaries ride the model call (history keeps the
//     bounded text placeholders). Scout's required path uses OpenAI Responses;
//     the legacy block adapter remains only for an explicit specialist
//     artifact follow-up.
//  3. deriveAttachmentText runs one bounded transcription pass whose output
//     lands in scoutChatFileAttachment.Text — the field every existing text
//     consumer (history folding, channel team replies, thread previews,
//     launch objectives) already reads, so downstream context is free.
//
// KEYLESS: uploads and chips still work (pure disk); the transcription pass
// and binary blocks are skipped, degrading to today's name-only behavior.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// attachmentUploadMaxBytes caps one composer upload at 25MB — generous
	// for decks and screenshots while staying far under the blob store's
	// 64MB ceiling and the provider request envelopes after base64 expansion.
	attachmentUploadMaxBytes = 25 << 20

	// One PDF per message, ≤20MB decoded. This is the per-category ceiling;
	// the COMBINED image+PDF payload is separately bounded by
	// attachmentMaxRequestBytes so the two budgets can never sum past the
	// API's request cap.
	attachmentMaxPDFBlocks = 1
	attachmentMaxPDFBytes  = 20 << 20

	// attachmentMaxRequestBytes caps the combined decoded payload of every
	// image and document block in one message. base64 expands the whole body
	// ~1.33x, so 22MB decoded → ~29MB on the wire, leaving headroom for the
	// JSON envelope and text prompt across supported provider adapters.
	// Without this guard the independent 20MB image and 20MB PDF budgets could
	// sum to ~40MB decoded (~53MB base64) and the request would 413 opaquely.
	attachmentMaxRequestBytes = 22 << 20

	// The Luna derived-text pass is bounded and best-effort: one sub-25s call
	// whose failure never blocks the message commit.
	attachmentDeriveTimeout   = 25 * time.Second
	attachmentDeriveMaxTokens = 1200

	// A composer upload is an ephemeral source object until a message commits it
	// into a destination with its own ACL. The grant is deliberately short: a
	// stalled browser can retry, while an abandoned upload cannot become a
	// long-lived bearer capability.
	pendingAttachmentUploadTTL = 30 * time.Minute
	attachmentReservationTTL   = 2 * time.Minute
)

const (
	attachmentSourcePending   = "pending"
	attachmentSourceReserved  = "reserved"
	attachmentSourceCommitted = "committed"
	attachmentSourceRevoked   = "revoked"
)

type pendingAttachmentUploadGrant struct {
	SourceID            string    `json:"sourceId"`
	SourceRevision      string    `json:"sourceRevision"`
	OwnerEmail          string    `json:"ownerEmail"`
	Ref                 string    `json:"ref"`
	Mime                string    `json:"mime"`
	Size                int64     `json:"size"`
	DestinationID       string    `json:"destinationId"`
	DestinationRevision string    `json:"destinationRevision"`
	State               string    `json:"state"`
	ReservationID       string    `json:"reservationId,omitempty"`
	ReservedAt          time.Time `json:"reservedAt,omitempty"`
	CommittedMessageID  string    `json:"committedMessageId,omitempty"`
	CreatedAt           time.Time `json:"createdAt"`
	ExpiresAt           time.Time `json:"expiresAt"`
}

type attachmentSourceStoreState struct {
	Version int                                     `json:"version"`
	Sources map[string]pendingAttachmentUploadGrant `json:"sources"`
}

var attachmentBlobReadAfterProbe func(string)
var attachmentSourceStoreWriter = writeAttachmentSourceStore

func attachmentSourceStorePath() string {
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "attachment-sources.json")
}

func writeAttachmentSourceStore(state attachmentSourceStoreState) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode attachment source authority: %w", err)
	}
	raw = append(raw, '\n')
	if err := writeFileAtomicallyDurable(attachmentSourceStorePath(), raw, 0o600); err != nil {
		return fmt.Errorf("persist attachment source authority: %w", err)
	}
	return nil
}

func (app *kanbanBoardApp) persistAttachmentSourceStoreLocked() error {
	if app.attachmentSourceStoreErr != nil {
		return app.attachmentSourceStoreErr
	}
	state := attachmentSourceStoreState{Version: 1, Sources: app.pendingAttachmentUploads}
	if err := attachmentSourceStoreWriter(state); err != nil {
		app.attachmentSourceStoreErr = err
		return err
	}
	return nil
}

func (app *kanbanBoardApp) attachmentSourceCommittedInChat(grant pendingAttachmentUploadGrant) (string, bool) {
	if app == nil || app.memory == nil {
		return "", false
	}
	for _, entry := range app.memory.snapshot(0) {
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok || strings.TrimSpace(thread.ID) != grant.DestinationID || scoutChatAttachmentDestinationRevision(thread) != grant.DestinationRevision {
			continue
		}
		for _, message := range thread.Messages {
			for _, file := range message.Files {
				if strings.TrimSpace(file.SourceID) == grant.SourceID && strings.TrimSpace(file.SourceRevision) == grant.SourceRevision && strings.TrimSpace(file.Ref) == grant.Ref {
					return message.ID, true
				}
			}
		}
	}
	return "", false
}

func (app *kanbanBoardApp) initializeAttachmentSourceStore() error {
	if app == nil {
		return fmt.Errorf("attachment source store is unavailable")
	}
	state := attachmentSourceStoreState{Version: 1, Sources: map[string]pendingAttachmentUploadGrant{}}
	raw, err := os.ReadFile(attachmentSourceStorePath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read attachment source authority: %w", err)
	}
	if err == nil {
		if decodeErr := json.Unmarshal(raw, &state); decodeErr != nil || state.Version != 1 || state.Sources == nil {
			return fmt.Errorf("decode attachment source authority")
		}
	}
	now := time.Now().UTC()
	changed := false
	for sourceID, grant := range state.Sources {
		if sourceID == "" || sourceID != grant.SourceID || !validBlobRef(grant.Ref) || grant.SourceRevision == "" {
			return fmt.Errorf("attachment source authority contains an invalid record")
		}
		switch grant.State {
		case attachmentSourcePending, attachmentSourceReserved, attachmentSourceCommitted, attachmentSourceRevoked:
		default:
			return fmt.Errorf("attachment source authority contains an invalid state")
		}
		if messageID, committed := app.attachmentSourceCommittedInChat(grant); committed {
			if grant.State != attachmentSourceCommitted || grant.CommittedMessageID != messageID || grant.ReservationID != "" {
				grant.State = attachmentSourceCommitted
				grant.CommittedMessageID = messageID
				grant.ReservationID = ""
				grant.ReservedAt = time.Time{}
				state.Sources[sourceID] = grant
				changed = true
			}
			continue
		}
		if grant.State == attachmentSourceReserved {
			// No in-process request survives a restart. A reservation with no exact
			// committed message is therefore safely recoverable by its same owner,
			// source revision, and destination binding.
			grant.State = attachmentSourcePending
			grant.ReservationID = ""
			grant.ReservedAt = time.Time{}
			state.Sources[sourceID] = grant
			changed = true
		}
		if !grant.ExpiresAt.After(now) && grant.State != attachmentSourceCommitted && grant.State != attachmentSourceRevoked {
			grant.State = attachmentSourceRevoked
			grant.ReservationID = ""
			grant.ReservedAt = time.Time{}
			state.Sources[sourceID] = grant
			changed = true
		}
	}
	app.pendingAttachmentUploads = state.Sources
	if changed {
		return attachmentSourceStoreWriter(state)
	}
	return nil
}

func attachmentSourceRevision(ref string, meta blobMeta) string {
	canonical := fmt.Sprintf("attachment-source/v1\x00%s\x00%s\x00%d", strings.TrimSpace(ref), strings.ToLower(strings.TrimSpace(meta.Mime)), meta.Size)
	digest := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("sha256:%x", digest[:])
}

func scoutChatAttachmentDestinationRevision(thread scoutChatThreadRecord) string {
	canonical := strings.Join([]string{
		"scout-chat-destination/v2",
		strings.TrimSpace(thread.ID),
		normalizeAccountEmail(thread.OwnerEmail),
		scoutChatThreadVisibility(thread),
		strings.Join(scoutChatThreadMemberEmails(thread), ","),
		strings.TrimSpace(thread.ArchivedAt),
	}, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("sha256:%x", digest[:])

}

func newPendingAttachmentSourceID() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "attachment-source-" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (app *kanbanBoardApp) grantPendingAttachmentUpload(user *userAccount, destination scoutChatThreadRecord, ref string, meta blobMeta) (pendingAttachmentUploadGrant, error) {
	if app == nil || user == nil || !validBlobRef(ref) || strings.TrimSpace(destination.ID) == "" {
		return pendingAttachmentUploadGrant{}, fmt.Errorf("attachment source grant is unavailable")
	}
	email := normalizeAccountEmail(user.Email)
	mime := strings.ToLower(strings.TrimSpace(meta.Mime))
	if email == "" || !attachmentUploadSafeMimes[mime] || meta.Size < 1 {
		return pendingAttachmentUploadGrant{}, fmt.Errorf("attachment source metadata is invalid")
	}
	if destination.ArchivedAt != "" {
		return pendingAttachmentUploadGrant{}, fmt.Errorf("chat thread is archived")
	}
	sourceID, err := newPendingAttachmentSourceID()
	if err != nil {
		return pendingAttachmentUploadGrant{}, fmt.Errorf("mint attachment source handle: %w", err)
	}
	now := time.Now().UTC()
	grant := pendingAttachmentUploadGrant{
		SourceID:            sourceID,
		SourceRevision:      attachmentSourceRevision(ref, meta),
		OwnerEmail:          email,
		Ref:                 strings.TrimSpace(ref),
		Mime:                mime,
		Size:                meta.Size,
		DestinationID:       strings.TrimSpace(destination.ID),
		DestinationRevision: scoutChatAttachmentDestinationRevision(destination),
		State:               attachmentSourcePending,
		CreatedAt:           now,
		ExpiresAt:           now.Add(pendingAttachmentUploadTTL),
	}
	app.pendingAttachmentUploadsMu.Lock()
	if app.pendingAttachmentUploads == nil {
		app.pendingAttachmentUploads = map[string]pendingAttachmentUploadGrant{}
	}
	for key, existing := range app.pendingAttachmentUploads {
		if !existing.ExpiresAt.After(now) && (existing.State == attachmentSourcePending || existing.State == attachmentSourceReserved) {
			existing.State = attachmentSourceRevoked
			existing.ReservationID = ""
			existing.ReservedAt = time.Time{}
			app.pendingAttachmentUploads[key] = existing
		}
	}
	app.pendingAttachmentUploads[sourceID] = grant
	if err := app.persistAttachmentSourceStoreLocked(); err != nil {
		delete(app.pendingAttachmentUploads, sourceID)
		app.pendingAttachmentUploadsMu.Unlock()
		return pendingAttachmentUploadGrant{}, fmt.Errorf("persist attachment source handle: %w", err)
	}
	app.pendingAttachmentUploadsMu.Unlock()
	return grant, nil
}

func (app *kanbanBoardApp) reservePendingAttachmentUpload(user *userAccount, destination scoutChatThreadRecord, file scoutChatFileAttachment, meta blobMeta, reservationID string) error {
	if app == nil || user == nil || !validBlobRef(file.Ref) {
		return fmt.Errorf("attachment is unavailable")
	}
	email := normalizeAccountEmail(user.Email)
	sourceID := strings.TrimSpace(file.SourceID)
	reservationID = strings.TrimSpace(reservationID)
	if email == "" || sourceID == "" || reservationID == "" {
		return fmt.Errorf("attachment source authorization is required")
	}
	now := time.Now().UTC()
	app.pendingAttachmentUploadsMu.Lock()
	defer app.pendingAttachmentUploadsMu.Unlock()
	if app.attachmentSourceStoreErr != nil {
		return fmt.Errorf("attachment source authority is unavailable")
	}
	grant, ok := app.pendingAttachmentUploads[sourceID]
	if ok && !grant.ExpiresAt.After(now) {
		grant.State = attachmentSourceRevoked
		grant.ReservationID = ""
		grant.ReservedAt = time.Time{}
		app.pendingAttachmentUploads[sourceID] = grant
		_ = app.persistAttachmentSourceStoreLocked()
		return fmt.Errorf("attachment source authorization expired")
	}
	if !ok {
		return fmt.Errorf("attachment source authorization is unavailable")
	}
	valid := grant.OwnerEmail == email &&
		grant.SourceID == sourceID &&
		grant.SourceRevision == strings.TrimSpace(file.SourceRevision) &&
		grant.SourceRevision == attachmentSourceRevision(file.Ref, meta) &&
		grant.Ref == strings.TrimSpace(file.Ref) &&
		grant.Mime == strings.ToLower(strings.TrimSpace(meta.Mime)) &&
		grant.Size == meta.Size &&
		grant.DestinationID == strings.TrimSpace(destination.ID) &&
		grant.DestinationRevision == scoutChatAttachmentDestinationRevision(destination)
	if !valid {
		return fmt.Errorf("attachment source authorization does not match this destination")
	}
	switch grant.State {
	case attachmentSourcePending:
		grant.State = attachmentSourceReserved
		grant.ReservationID = reservationID
		grant.ReservedAt = now
		app.pendingAttachmentUploads[sourceID] = grant
		if err := app.persistAttachmentSourceStoreLocked(); err != nil {
			return fmt.Errorf("reserve attachment source: %w", err)
		}
		return nil
	case attachmentSourceReserved:
		if grant.ReservationID == reservationID {
			return nil
		}
		if now.Sub(grant.ReservedAt) > attachmentReservationTTL {
			grant.ReservationID = reservationID
			grant.ReservedAt = now
			app.pendingAttachmentUploads[sourceID] = grant
			if err := app.persistAttachmentSourceStoreLocked(); err != nil {
				return fmt.Errorf("recover attachment source reservation: %w", err)
			}
			return nil
		}
		return fmt.Errorf("attachment source is already in use")
	default:
		return fmt.Errorf("attachment source authorization was already consumed")
	}
}

func (app *kanbanBoardApp) releaseAttachmentReservation(reservationID string) {
	reservationID = strings.TrimSpace(reservationID)
	if app == nil || reservationID == "" {
		return
	}
	app.pendingAttachmentUploadsMu.Lock()
	defer app.pendingAttachmentUploadsMu.Unlock()
	changed := false
	now := time.Now().UTC()
	for sourceID, grant := range app.pendingAttachmentUploads {
		if grant.State != attachmentSourceReserved || grant.ReservationID != reservationID {
			continue
		}
		if grant.ExpiresAt.After(now) {
			grant.State = attachmentSourcePending
		} else {
			grant.State = attachmentSourceRevoked
		}
		grant.ReservationID = ""
		grant.ReservedAt = time.Time{}
		app.pendingAttachmentUploads[sourceID] = grant
		changed = true
	}
	if changed {
		_ = app.persistAttachmentSourceStoreLocked()
	}
}

func (app *kanbanBoardApp) revokeAttachmentSource(sourceID string) error {
	if app == nil || strings.TrimSpace(sourceID) == "" {
		return fmt.Errorf("attachment source is unavailable")
	}
	app.pendingAttachmentUploadsMu.Lock()
	defer app.pendingAttachmentUploadsMu.Unlock()
	grant, ok := app.pendingAttachmentUploads[strings.TrimSpace(sourceID)]
	if !ok {
		return fmt.Errorf("attachment source is unavailable")
	}
	grant.State = attachmentSourceRevoked
	grant.ReservationID = ""
	grant.ReservedAt = time.Time{}
	app.pendingAttachmentUploads[grant.SourceID] = grant
	return app.persistAttachmentSourceStoreLocked()
}

func (app *kanbanBoardApp) attachmentSourceAuthorizedForRead(user *userAccount, destination scoutChatThreadRecord, file scoutChatFileAttachment, reservationID string) bool {
	if app == nil || user == nil {
		return false
	}
	app.pendingAttachmentUploadsMu.Lock()
	defer app.pendingAttachmentUploadsMu.Unlock()
	grant, ok := app.pendingAttachmentUploads[strings.TrimSpace(file.SourceID)]
	if !ok || app.attachmentSourceStoreErr != nil {
		return false
	}
	valid := grant.State == attachmentSourceReserved && grant.ReservationID == strings.TrimSpace(reservationID) &&
		grant.OwnerEmail == normalizeAccountEmail(user.Email) && grant.SourceRevision == strings.TrimSpace(file.SourceRevision) &&
		grant.Ref == strings.TrimSpace(file.Ref) && grant.DestinationID == strings.TrimSpace(destination.ID) &&
		grant.DestinationRevision == scoutChatAttachmentDestinationRevision(destination) && grant.ExpiresAt.After(time.Now().UTC())
	if !valid {
		return false
	}
	meta, err := blobStatForRef(grant.Ref)
	return err == nil && attachmentSourceRevision(grant.Ref, meta) == grant.SourceRevision &&
		strings.ToLower(strings.TrimSpace(meta.Mime)) == grant.Mime && meta.Size == grant.Size
}

func attachmentMessagesHaveSources(messages []scoutChatMessageRecord) bool {
	for _, message := range messages {
		for _, file := range message.Files {
			if strings.TrimSpace(file.SourceID) != "" || strings.TrimSpace(file.Ref) != "" {
				return true
			}
		}
	}
	return false
}

// validateAttachmentMessageSourcesLocked is the final authority check. The
// caller holds both the destination thread lock and pendingAttachmentUploadsMu,
// so source revocation and audience changes cannot interleave with the durable
// chat save.
func (app *kanbanBoardApp) validateAttachmentMessageSourcesLocked(viewerEmail string, destination scoutChatThreadRecord, messages []scoutChatMessageRecord) error {
	if app.attachmentSourceStoreErr != nil {
		return fmt.Errorf("attachment source authority is unavailable")
	}
	seenSourceIDs := map[string]struct{}{}
	for _, message := range messages {
		for _, file := range message.Files {
			if strings.TrimSpace(file.Ref) == "" && strings.TrimSpace(file.SourceID) == "" {
				continue
			}
			sourceID := strings.TrimSpace(file.SourceID)
			if sourceID == "" {
				return fmt.Errorf("chat attachment authorization changed; attach the file again")
			}
			if _, duplicate := seenSourceIDs[sourceID]; duplicate {
				return fmt.Errorf("the same attachment cannot be added twice")
			}
			seenSourceIDs[sourceID] = struct{}{}
			grant, ok := app.pendingAttachmentUploads[sourceID]
			if !ok || grant.State != attachmentSourceReserved || grant.ReservationID != strings.TrimSpace(message.attachmentReservationID) ||
				grant.OwnerEmail != normalizeAccountEmail(viewerEmail) || grant.SourceRevision != strings.TrimSpace(file.SourceRevision) ||
				grant.Ref != strings.TrimSpace(file.Ref) || grant.DestinationID != strings.TrimSpace(destination.ID) ||
				grant.DestinationRevision != scoutChatAttachmentDestinationRevision(destination) || !grant.ExpiresAt.After(time.Now().UTC()) {
				return fmt.Errorf("chat attachment authorization changed; attach the file again")
			}
			meta, err := blobStatForRef(grant.Ref)
			if err != nil || attachmentSourceRevision(grant.Ref, meta) != grant.SourceRevision || strings.ToLower(strings.TrimSpace(meta.Mime)) != grant.Mime || meta.Size != grant.Size {
				return fmt.Errorf("chat attachment source changed; attach the file again")
			}
		}
	}
	return nil
}

func (app *kanbanBoardApp) commitAttachmentMessageSourcesLocked(messages []scoutChatMessageRecord) error {
	changed := false
	for _, message := range messages {
		for _, file := range message.Files {
			sourceID := strings.TrimSpace(file.SourceID)
			if sourceID == "" {
				continue
			}
			grant := app.pendingAttachmentUploads[sourceID]
			grant.State = attachmentSourceCommitted
			grant.CommittedMessageID = message.ID
			grant.ReservationID = ""
			grant.ReservedAt = time.Time{}
			app.pendingAttachmentUploads[sourceID] = grant
			changed = true
		}
	}
	if changed {
		return app.persistAttachmentSourceStoreLocked()
	}
	return nil
}

// Composer storage and model forwarding are separate contracts. GIF remains
// a supported rendered chat attachment, but Responses accepts only
// non-animated GIF and this validator cannot yet prove that safely. GIFs are
// therefore stored/rendered while model context degrades to the name chip.
var attachmentUploadSafeMimes = map[string]bool{
	"image/png":       true,
	"image/jpeg":      true,
	"image/webp":      true,
	"image/gif":       true,
	"application/pdf": true,
}

var attachmentModelSafeMimes = map[string]bool{
	"image/png":       true,
	"image/jpeg":      true,
	"image/webp":      true,
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

// canonicalAttachmentUploadMime accepts the harmless aliases native document
// pickers use for otherwise-supported formats. Validation still happens
// against the bytes before anything is stored.
func canonicalAttachmentUploadMime(header string) string {
	switch mime := attachmentUploadMime(header); mime {
	case "image/jpg", "image/pjpeg":
		return "image/jpeg"
	case "application/x-pdf":
		return "application/pdf"
	default:
		return mime
	}
}

func detectedAttachmentUploadMime(data []byte) string {
	detected := canonicalAttachmentUploadMime(http.DetectContentType(data))
	if attachmentUploadSafeMimes[detected] && validateAttachmentBytes(detected, data) == nil {
		return detected
	}
	// Go's generic content sniffer has not recognized WebP in every supported
	// toolchain. The strict RIFF/WEBP validator remains the authority.
	if validateAttachmentBytes("image/webp", data) == nil {
		return "image/webp"
	}
	return ""
}

// resolveAttachmentUploadMime preserves strict matching for declared safe
// types while giving native pickers' absent/generic declarations a safe
// content-sniffed fallback. An actively conflicting safe declaration is never
// silently rewritten to another type.
func resolveAttachmentUploadMime(declared string, data []byte) (string, error) {
	normalized := canonicalAttachmentUploadMime(declared)
	if attachmentUploadSafeMimes[normalized] {
		if err := validateAttachmentBytes(normalized, data); err != nil {
			return "", err
		}
		return normalized, nil
	}
	switch normalized {
	case "", "application/octet-stream", "binary/octet-stream":
		if detected := detectedAttachmentUploadMime(data); detected != "" {
			return detected, nil
		}
	}
	return "", fmt.Errorf("unsupported attachment type")
}

// readAssistantAttachmentUpload accepts both the original raw-body contract
// and multipart/form-data with one `file` part. React Native can stream a
// picker URI through multipart without first materializing a JS Blob, while
// web/legacy callers remain compatible with the raw contract.
func readAssistantAttachmentUpload(w http.ResponseWriter, r *http.Request) ([]byte, string, int, string) {
	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	mediaType, _, parseErr := mime.ParseMediaType(contentType)
	if strings.HasPrefix(strings.ToLower(contentType), "multipart/") {
		if parseErr != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
			return nil, "", http.StatusBadRequest, "could not read attachment form"
		}
		// Allow bounded framing overhead, but cap the decoded part itself at the
		// exact same 25MB contract as a raw upload.
		r.Body = http.MaxBytesReader(w, r.Body, attachmentUploadMaxBytes+(1<<20))
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return nil, "", http.StatusRequestEntityTooLarge, fmt.Sprintf("attachment exceeds the %dMB cap", attachmentUploadMaxBytes>>20)
			}
			return nil, "", http.StatusBadRequest, "could not read attachment form"
		}
		defer r.MultipartForm.RemoveAll()
		part, header, err := r.FormFile("file")
		if err != nil {
			return nil, "", http.StatusBadRequest, "attachment form needs a file field"
		}
		defer part.Close()
		data, err := io.ReadAll(io.LimitReader(part, attachmentUploadMaxBytes+1))
		if err != nil {
			return nil, "", http.StatusBadRequest, "could not read attachment body"
		}
		if len(data) > attachmentUploadMaxBytes {
			return nil, "", http.StatusRequestEntityTooLarge, fmt.Sprintf("attachment exceeds the %dMB cap", attachmentUploadMaxBytes>>20)
		}
		return data, header.Header.Get("Content-Type"), 0, ""
	}

	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, attachmentUploadMaxBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, "", http.StatusRequestEntityTooLarge, fmt.Sprintf("attachment exceeds the %dMB cap", attachmentUploadMaxBytes>>20)
		}
		return nil, "", http.StatusBadRequest, "could not read attachment body"
	}
	return data, contentType, 0, ""
}

// assistantAttachmentUploadHandler serves POST /assistant/attachments — the
// composer's binary upload door. Session-gated exactly like its
// /artifacts/blob neighbor (origin check, signed-in user); callers may send a
// raw body or one multipart `file` part, and the response carries the
// content-addressed ref the message record will reference. Dedupe, MIME
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
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "attachments are unavailable")
		return
	}
	destinationID := strings.TrimSpace(r.URL.Query().Get("threadId"))
	if destinationID == "" {
		writeAuthError(w, http.StatusBadRequest, "attachment destination is required")
		return
	}
	destination, _, err := kanbanApp.scoutChatThreadByID(user.Email, destinationID)
	if err != nil {
		writeScoutChatThreadError(w, err)
		return
	}
	if destination.ArchivedAt != "" {
		writeAuthError(w, http.StatusConflict, "chat thread is archived")
		return
	}

	data, declaredMime, status, message := readAssistantAttachmentUpload(w, r)
	if status != 0 {
		writeAuthError(w, status, message)
		return
	}
	if len(data) == 0 {
		writeAuthError(w, http.StatusBadRequest, "attachment body is empty")
		return
	}
	mime, err := resolveAttachmentUploadMime(declaredMime, data)
	if err != nil {
		writeAuthError(w, http.StatusUnsupportedMediaType, "attachment contents do not match the selected file type")
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
	grant, err := kanbanApp.grantPendingAttachmentUpload(user, destination, ref, meta)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not authorize attachment upload")
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"ref":            ref,
		"mime":           meta.Mime,
		"size":           meta.Size,
		"sourceId":       grant.SourceID,
		"sourceRevision": grant.SourceRevision,
	})
}

// validateAttachmentBytes refuses type-confused and malformed files before
// they are persisted or forwarded to a model. DecodeConfig reads dimensions
// without expanding full raster pixel buffers, and the pixel ceiling blocks
// ordinary image decompression bombs while preserving phone photography.
func validateAttachmentBytes(mime string, data []byte) error {
	const maxAttachmentPixels = 64_000_000
	var (
		config image.Config
		err    error
	)
	switch mime {
	case "image/png":
		config, err = png.DecodeConfig(bytes.NewReader(data))
	case "image/jpeg":
		config, err = jpeg.DecodeConfig(bytes.NewReader(data))
	case "image/gif":
		config, err = gif.DecodeConfig(bytes.NewReader(data))
	case "image/webp":
		if len(data) < 16 || !bytes.Equal(data[:4], []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WEBP")) {
			return fmt.Errorf("invalid webp")
		}
		return nil
	case "application/pdf":
		if len(data) < 8 || !bytes.HasPrefix(data, []byte("%PDF-")) {
			return fmt.Errorf("invalid pdf")
		}
		return nil
	default:
		return fmt.Errorf("unsupported attachment type")
	}
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 12_000 || config.Height > 12_000 || int64(config.Width)*int64(config.Height) > maxAttachmentPixels {
		return fmt.Errorf("invalid or oversized image dimensions")
	}
	return nil
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
	return attachmentContentBlocksWithReader(files, func(file scoutChatFileAttachment) ([]byte, blobMeta, bool) {
		data, meta, err := getBlob(strings.TrimSpace(file.Ref))
		if err != nil {
			log.Warnf("Skipping unreadable chat attachment blob %s: %v", strings.TrimSpace(file.Ref), err)
			return nil, blobMeta{}, false
		}
		return data, meta, true
	})
}

func attachmentContentBlocksWithReader(files []scoutChatFileAttachment, read func(scoutChatFileAttachment) ([]byte, blobMeta, bool)) []json.RawMessage {
	var blocks []json.RawMessage
	images, pdfs := 0, 0
	imageBytes, pdfBytes := 0, 0
	for _, file := range files {
		ref := strings.TrimSpace(file.Ref)
		if !validBlobRef(ref) {
			continue
		}
		data, meta, ok := read(file)
		if !ok {
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

// openAIAttachmentContent is the OpenAI Responses counterpart to
// attachmentContentBlocks. Images use input_image data URLs; PDFs use
// input_file with an inline data URL and a stable filename. It deliberately
// reuses the existing conservative decoded-byte budgets.
func openAIAttachmentContent(files []scoutChatFileAttachment) []openAIInputContent {
	return openAIAttachmentContentWithReader(files, func(file scoutChatFileAttachment) ([]byte, blobMeta, bool) {
		data, meta, err := getBlob(strings.TrimSpace(file.Ref))
		if err != nil {
			log.Warnf("Skipping unreadable chat attachment blob %s: %v", strings.TrimSpace(file.Ref), err)
			return nil, blobMeta{}, false
		}
		return data, meta, true
	})
}

func openAIAttachmentContentWithReader(files []scoutChatFileAttachment, read func(scoutChatFileAttachment) ([]byte, blobMeta, bool)) []openAIInputContent {
	var content []openAIInputContent
	images, pdfs := 0, 0
	imageBytes, pdfBytes := 0, 0
	for _, file := range files {
		ref := strings.TrimSpace(file.Ref)
		if !validBlobRef(ref) {
			continue
		}
		data, meta, ok := read(file)
		if !ok {
			continue
		}
		mime := strings.ToLower(strings.TrimSpace(meta.Mime))
		if !attachmentModelSafeMimes[mime] || imageBytes+pdfBytes+len(data) > attachmentMaxRequestBytes {
			continue
		}
		dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		if mime == "application/pdf" {
			if pdfs+1 > attachmentMaxPDFBlocks || pdfBytes+len(data) > attachmentMaxPDFBytes {
				continue
			}
			filename := filepath.Base(strings.TrimSpace(file.Name))
			if filename == "." || filename == string(filepath.Separator) || !strings.EqualFold(filepath.Ext(filename), ".pdf") {
				filename = "attachment.pdf"
			}
			pdfs++
			pdfBytes += len(data)
			content = append(content, openAIInputContent{Type: "input_file", Filename: filename, FileData: dataURL})
			continue
		}
		if images+1 > anthropicMaxRequestImages || imageBytes+len(data) > anthropicMaxRequestImageBytes {
			continue
		}
		images++
		imageBytes += len(data)
		content = append(content, openAIInputContent{Type: "input_image", ImageURL: dataURL})
	}
	return content
}

func (app *kanbanBoardApp) attachmentSourcesAuthorizedForRead(user *userAccount, destination scoutChatThreadRecord, files []scoutChatFileAttachment, reservationID string) bool {
	for _, file := range files {
		if strings.TrimSpace(file.Ref) == "" {
			continue
		}
		if !app.attachmentSourceAuthorizedForRead(user, destination, file, reservationID) {
			return false
		}
	}
	return true
}

// attachmentContentBlocksAuthorized reauthorizes the exact durable source
// record immediately before and after every blob read. Bytes from a source
// revoked, expired, or rebound during I/O are discarded before they can reach
// a provider request.
func (app *kanbanBoardApp) attachmentContentBlocksAuthorized(user *userAccount, destination scoutChatThreadRecord, files []scoutChatFileAttachment, reservationID string) []json.RawMessage {
	return attachmentContentBlocksWithReader(files, func(file scoutChatFileAttachment) ([]byte, blobMeta, bool) {
		if !app.attachmentSourceAuthorizedForRead(user, destination, file, reservationID) {
			return nil, blobMeta{}, false
		}
		data, meta, err := getBlob(strings.TrimSpace(file.Ref))
		if attachmentBlobReadAfterProbe != nil {
			attachmentBlobReadAfterProbe(strings.TrimSpace(file.SourceID))
		}
		if err != nil || attachmentSourceRevision(strings.TrimSpace(file.Ref), meta) != strings.TrimSpace(file.SourceRevision) ||
			!app.attachmentSourceAuthorizedForRead(user, destination, file, reservationID) {
			return nil, blobMeta{}, false
		}
		return data, meta, true
	})
}

func (app *kanbanBoardApp) openAIAttachmentContentAuthorized(user *userAccount, destination scoutChatThreadRecord, files []scoutChatFileAttachment, reservationID string) []openAIInputContent {
	return openAIAttachmentContentWithReader(files, func(file scoutChatFileAttachment) ([]byte, blobMeta, bool) {
		if !app.attachmentSourceAuthorizedForRead(user, destination, file, reservationID) {
			return nil, blobMeta{}, false
		}
		data, meta, err := getBlob(strings.TrimSpace(file.Ref))
		if attachmentBlobReadAfterProbe != nil {
			attachmentBlobReadAfterProbe(strings.TrimSpace(file.SourceID))
		}
		if err != nil || attachmentSourceRevision(strings.TrimSpace(file.Ref), meta) != strings.TrimSpace(file.SourceRevision) ||
			!app.attachmentSourceAuthorizedForRead(user, destination, file, reservationID) {
			return nil, blobMeta{}, false
		}
		return data, meta, true
	})
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
func deriveAttachmentText(ctx context.Context, apiKey string, files []scoutChatFileAttachment, attachments []openAIInputContent) []scoutChatFileAttachment {
	if len(attachments) == 0 {
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
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return files
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, attachmentDeriveTimeout)
	defer cancel()

	transcript, err := createOpenAITextResponse(ctx, apiKey, openAITextRequest{
		Model:           scoutExtractionModel(),
		Seat:            seatAttachments,
		Workflow:        "attachment_extract",
		Instructions:    attachmentDeriveInstructions,
		Input:           "Transcribe the key facts, numbers, names, and claims in the attached files for the team's shared memory.",
		ReasoningEffort: "low",
		Verbosity:       "low",
		MaxOutputTokens: attachmentDeriveMaxTokens,
		Attachments:     attachments,
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

func (app *kanbanBoardApp) deriveAttachmentTextAuthorized(ctx context.Context, user *userAccount, destination scoutChatThreadRecord, files []scoutChatFileAttachment, reservationID string, attachments []openAIInputContent) []scoutChatFileAttachment {
	before := append([]scoutChatFileAttachment(nil), files...)
	// This is the last check before the model call. The second check happens
	// after its response and discards all derived text if authority changed
	// while external processing was in flight.
	if !app.attachmentSourcesAuthorizedForRead(user, destination, files, reservationID) {
		return before
	}
	derived := deriveAttachmentText(ctx, app.currentOpenAIAPIKey(), files, attachments)
	if !app.attachmentSourcesAuthorizedForRead(user, destination, files, reservationID) {
		return before
	}
	return derived
}

func (app *kanbanBoardApp) committedChatAttachmentAuthorized(viewerEmail string, threadID string, messageID string, file scoutChatFileAttachment) bool {
	if app == nil || strings.TrimSpace(file.SourceID) == "" || strings.TrimSpace(file.Ref) == "" {
		return false
	}
	app.pendingAttachmentUploadsMu.Lock()
	grant, ok := app.pendingAttachmentUploads[strings.TrimSpace(file.SourceID)]
	storeHealthy := app.attachmentSourceStoreErr == nil
	app.pendingAttachmentUploadsMu.Unlock()
	if !storeHealthy || !ok || grant.State != attachmentSourceCommitted || grant.CommittedMessageID != strings.TrimSpace(messageID) ||
		grant.SourceRevision != strings.TrimSpace(file.SourceRevision) || grant.Ref != strings.TrimSpace(file.Ref) || grant.DestinationID != strings.TrimSpace(threadID) {
		return false
	}
	meta, err := blobStatForRef(grant.Ref)
	if err != nil || attachmentSourceRevision(grant.Ref, meta) != grant.SourceRevision || strings.ToLower(strings.TrimSpace(meta.Mime)) != grant.Mime || meta.Size != grant.Size {
		return false
	}
	thread, _, err := app.scoutChatThreadByID(viewerEmail, threadID)
	if err != nil || scoutChatAttachmentDestinationRevision(thread) != grant.DestinationRevision {
		return false
	}
	index := scoutChatMessageIndex(thread, messageID)
	if index < 0 {
		return false
	}
	for _, current := range thread.Messages[index].Files {
		if strings.TrimSpace(current.SourceID) == grant.SourceID && strings.TrimSpace(current.SourceRevision) == grant.SourceRevision && strings.TrimSpace(current.Ref) == grant.Ref {
			return true
		}
	}
	return false
}

func (app *kanbanBoardApp) committedAttachmentContentBlocks(viewerEmail string, threadID string, messageID string, files []scoutChatFileAttachment) []json.RawMessage {
	return attachmentContentBlocksWithReader(files, func(file scoutChatFileAttachment) ([]byte, blobMeta, bool) {
		if !app.committedChatAttachmentAuthorized(viewerEmail, threadID, messageID, file) {
			return nil, blobMeta{}, false
		}
		data, meta, err := getBlob(strings.TrimSpace(file.Ref))
		if attachmentBlobReadAfterProbe != nil {
			attachmentBlobReadAfterProbe(strings.TrimSpace(file.SourceID))
		}
		if err != nil || attachmentSourceRevision(strings.TrimSpace(file.Ref), meta) != strings.TrimSpace(file.SourceRevision) ||
			!app.committedChatAttachmentAuthorized(viewerEmail, threadID, messageID, file) {
			return nil, blobMeta{}, false
		}
		return data, meta, true
	})
}

func (app *kanbanBoardApp) committedOpenAIAttachmentContent(viewerEmail string, threadID string, messageID string, files []scoutChatFileAttachment) []openAIInputContent {
	return openAIAttachmentContentWithReader(files, func(file scoutChatFileAttachment) ([]byte, blobMeta, bool) {
		if !app.committedChatAttachmentAuthorized(viewerEmail, threadID, messageID, file) {
			return nil, blobMeta{}, false
		}
		data, meta, err := getBlob(strings.TrimSpace(file.Ref))
		if attachmentBlobReadAfterProbe != nil {
			attachmentBlobReadAfterProbe(strings.TrimSpace(file.SourceID))
		}
		if err != nil || attachmentSourceRevision(strings.TrimSpace(file.Ref), meta) != strings.TrimSpace(file.SourceRevision) ||
			!app.committedChatAttachmentAuthorized(viewerEmail, threadID, messageID, file) {
			return nil, blobMeta{}, false
		}
		return data, meta, true
	})
}

func (app *kanbanBoardApp) committedAttachmentsAuthorized(viewerEmail string, threadID string, messageID string, files []scoutChatFileAttachment) bool {
	for _, file := range files {
		if strings.TrimSpace(file.Ref) != "" && !app.committedChatAttachmentAuthorized(viewerEmail, threadID, messageID, file) {
			return false
		}
	}
	return true
}

// projectScoutChatThreadForViewer is the sole read projection for persisted
// chat attachments. A persisted chat message is not itself authority to expose
// an attachment: every file must still resolve to its exact committed source
// grant, current blob revision, destination audience, and a healthy authority
// store. Anything else is omitted wholesale -- including its name, ref, MIME,
// size, derived text, and even the fact that an attachment existed.
//
// The returned thread is a copy. It is deliberately safe for HTTP responses,
// websocket events, Files/deposits views, and model-history construction, but
// must never be used for a durable write.
func (app *kanbanBoardApp) projectScoutChatThreadForViewer(viewerEmail string, thread scoutChatThreadRecord) scoutChatThreadRecord {
	projected := thread
	projected.OpeningOperation = nil
	// Direct coworker identity comes from the signed Product ledger so an old
	// chat record is upgraded on read without making the chat title authoritative.
	if app != nil {
		if agent, ok := app.strideRuntime.productAgentForDirectThread(thread.ID); ok {
			projected.AgentID = agent.ID
			projected.AgentName = agent.DisplayName
		}
	}
	if len(thread.Messages) == 0 {
		return projected
	}
	projected.Messages = append([]scoutChatMessageRecord(nil), thread.Messages...)
	for messageIndex := range projected.Messages {
		original := thread.Messages[messageIndex]
		if original.Reply != nil {
			reply := *original.Reply
			reply.LeaseID = ""
			reply.LeaseExpiresAt = ""
			projected.Messages[messageIndex].Reply = &reply
		}
		files := make([]scoutChatFileAttachment, 0, len(original.Files))
		for _, file := range original.Files {
			if app.committedChatAttachmentAuthorized(viewerEmail, thread.ID, original.ID, file) {
				files = append(files, file)
			}
		}
		// nil (rather than a zero-length allocated slice) preserves omitempty and
		// prevents a count/shape side channel in JSON clients.
		if len(files) == 0 {
			projected.Messages[messageIndex].Files = nil
		} else {
			projected.Messages[messageIndex].Files = files
		}
	}
	return projected
}

func (app *kanbanBoardApp) projectScoutChatMessageForViewer(viewerEmail string, thread scoutChatThreadRecord, message scoutChatMessageRecord) scoutChatMessageRecord {
	projected := app.projectScoutChatThreadForViewer(viewerEmail, scoutChatThreadRecord{ID: thread.ID, OwnerEmail: thread.OwnerEmail, Visibility: thread.Visibility, Messages: []scoutChatMessageRecord{message}})
	if len(projected.Messages) == 0 {
		return message
	}
	return projected.Messages[0]
}

// projectScoutChatResponseForViewer applies the same attachment projection to
// every chat HTTP response shape. Keeping this at the handler boundary avoids
// relying on each mutator to remember that a source can be revoked between a
// successful commit and serialization back to the requester.
func (app *kanbanBoardApp) projectScoutChatResponseForViewer(viewerEmail string, threadID string, response map[string]any) map[string]any {
	if response == nil {
		return nil
	}
	projected := make(map[string]any, len(response))
	for key, value := range response {
		projected[key] = value
	}
	thread, hasThread := response["thread"].(scoutChatThreadRecord)
	if !hasThread {
		thread, _, _ = app.scoutChatThreadByID(viewerEmail, threadID)
		hasThread = strings.TrimSpace(thread.ID) != ""
	}
	if !hasThread {
		return projected
	}
	projected["thread"] = app.projectScoutChatThreadForViewer(viewerEmail, thread)
	for _, key := range []string{"message", "answer"} {
		if message, ok := response[key].(scoutChatMessageRecord); ok {
			projected[key] = app.projectScoutChatMessageForViewer(viewerEmail, thread, message)
		}
	}
	return projected
}
