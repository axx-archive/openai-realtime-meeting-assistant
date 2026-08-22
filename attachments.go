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
	"strconv"
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
	OriginFileID        string    `json:"originFileId,omitempty"`
	OriginRevision      string    `json:"originRevision,omitempty"`
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
	return app.grantPendingAttachmentUploadFromFile(user, destination, ref, meta, "", "")
}

func (app *kanbanBoardApp) grantPendingAttachmentUploadFromFile(user *userAccount, destination scoutChatThreadRecord, ref string, meta blobMeta, originFileID, originRevision string) (pendingAttachmentUploadGrant, error) {
	if app == nil || user == nil || !validBlobRef(ref) || strings.TrimSpace(destination.ID) == "" {
		return pendingAttachmentUploadGrant{}, fmt.Errorf("attachment source grant is unavailable")
	}
	email := normalizeAccountEmail(user.Email)
	mime := strings.ToLower(strings.TrimSpace(meta.Mime))
	originFileID = strings.TrimSpace(originFileID)
	originRevision = strings.TrimSpace(originRevision)
	if email == "" || !attachmentUploadSafeMimes[mime] || meta.Size < 1 || (originFileID == "") != (originRevision == "") {
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
		OriginFileID:        originFileID,
		OriginRevision:      originRevision,
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
	originGrant, originOK := app.pendingAttachmentUploads[sourceID]
	app.pendingAttachmentUploadsMu.Unlock()
	if originOK && originGrant.OriginFileID != "" {
		current, ok := app.assistantFileSourceRevisionForDestination(context.Background(), user, originGrant.OriginFileID, destination)
		if !ok || current != originGrant.OriginRevision {
			return fmt.Errorf("attachment source changed; attach the file again")
		}
	}
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
		grant.DestinationRevision == scoutChatAttachmentDestinationRevision(destination) &&
		grant.OriginFileID == originGrant.OriginFileID && grant.OriginRevision == originGrant.OriginRevision
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
	sourceID = strings.TrimSpace(sourceID)
	if store := currentHomeProjectStore(); store != nil {
		groups, err := store.activeProjectChatGroupsForAttachmentSource(context.Background(), sourceID)
		if err != nil {
			return err
		}
		for _, group := range groups {
			operationID := projectChatID("project_attachment_source_revoke", group.organizationID, group.groupID, sourceID)
			if err := store.invalidateProjectChatAttachmentSourceGroupForDrift(context.Background(), group.organizationID, group.groupID, operationID, sourceID); err != nil {
				return err
			}
		}
	}
	app.pendingAttachmentUploadsMu.Lock()
	defer app.pendingAttachmentUploadsMu.Unlock()
	grant, ok := app.pendingAttachmentUploads[sourceID]
	if !ok {
		return fmt.Errorf("attachment source is unavailable")
	}
	grant.State = attachmentSourceRevoked
	grant.ReservationID = ""
	grant.ReservedAt = time.Time{}
	app.pendingAttachmentUploads[grant.SourceID] = grant
	return app.persistAttachmentSourceStoreLocked()
}

func (app *kanbanBoardApp) revokeAttachmentSourcesForOrigin(originFileID string) error {
	originFileID = strings.TrimSpace(originFileID)
	if app == nil || originFileID == "" {
		return nil
	}
	app.pendingAttachmentUploadsMu.Lock()
	var sourceIDs []string
	for sourceID, grant := range app.pendingAttachmentUploads {
		if grant.OriginFileID == originFileID && grant.State != attachmentSourceRevoked {
			sourceIDs = append(sourceIDs, sourceID)
		}
	}
	app.pendingAttachmentUploadsMu.Unlock()
	for _, sourceID := range sourceIDs {
		if err := app.revokeAttachmentSource(sourceID); err != nil {
			return err
		}
	}
	return nil
}

func (app *kanbanBoardApp) attachmentSourceAuthorizedForRead(user *userAccount, destination scoutChatThreadRecord, file scoutChatFileAttachment, reservationID string) bool {
	if app == nil || user == nil {
		return false
	}
	app.pendingAttachmentUploadsMu.Lock()
	grant, ok := app.pendingAttachmentUploads[strings.TrimSpace(file.SourceID)]
	storeHealthy := app.attachmentSourceStoreErr == nil
	app.pendingAttachmentUploadsMu.Unlock()
	if !ok || !storeHealthy {
		return false
	}
	if grant.OriginFileID != "" {
		current, currentOK := app.assistantFileSourceRevisionForDestination(context.Background(), user, grant.OriginFileID, destination)
		if !currentOK || current != grant.OriginRevision {
			return false
		}
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
	"text/plain":      true,
	"text/markdown":   true,
}

var attachmentModelSafeMimes = map[string]bool{
	"image/png":       true,
	"image/jpeg":      true,
	"image/webp":      true,
	"application/pdf": true,
}

var openAIAttachmentModelSafeMimes = map[string]bool{
	"image/png":       true,
	"image/jpeg":      true,
	"image/webp":      true,
	"application/pdf": true,
	"text/plain":      true,
	"text/markdown":   true,
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

// assistantAttachmentFromFileHandler mints a fresh, single-use attachment
// authority for an already-authorized Drive row. It binds the immutable blob,
// the current source revision, and the exact destination audience. The source
// is revalidated when the message is reserved/read; deleting, unsaving, or
// changing its ACL therefore fails closed instead of leaving a bearer file ID.
func assistantAttachmentFromFileHandler(w http.ResponseWriter, r *http.Request) {
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
	if kanbanApp == nil || kanbanApp.memory == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "attachments are unavailable")
		return
	}
	payload := struct {
		ThreadID string `json:"threadId"`
		FileID   string `json:"fileId"`
	}{}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || ensureJSONEOF(decoder) != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read Drive attachment request")
		return
	}
	payload.ThreadID = strings.TrimSpace(payload.ThreadID)
	payload.FileID = strings.TrimSpace(payload.FileID)
	if payload.ThreadID == "" || payload.FileID == "" {
		writeAuthError(w, http.StatusBadRequest, "threadId and fileId are required")
		return
	}
	destination, _, err := kanbanApp.scoutChatThreadByID(user.Email, payload.ThreadID)
	if err != nil {
		writeAuthError(w, http.StatusNotFound, "chat thread not found")
		return
	}
	if destination.ArchivedAt != "" {
		writeAuthError(w, http.StatusConflict, "chat thread is archived")
		return
	}
	file, meta, originRevision, ok := kanbanApp.assistantFileAttachmentSourceForDestination(r.Context(), user, payload.FileID, destination)
	if !ok {
		writeAuthError(w, http.StatusNotFound, "file not found")
		return
	}
	if !attachmentUploadSafeMimes[strings.ToLower(strings.TrimSpace(meta.Mime))] || meta.Size < 1 || meta.Size > attachmentUploadMaxBytes {
		writeAuthError(w, http.StatusUnsupportedMediaType, "this Drive file type cannot be attached to chat yet")
		return
	}
	grant, err := kanbanApp.grantPendingAttachmentUploadFromFile(user, destination, file.Ref, meta, payload.FileID, originRevision)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not authorize the Drive attachment")
		return
	}
	file.SourceID = grant.SourceID
	file.SourceRevision = grant.SourceRevision
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "attachment": file})
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
	case "text/plain", "text/markdown":
		if len(data) == 0 || !utf8.Valid(data) || bytesContainsNUL(data) {
			return fmt.Errorf("invalid text attachment")
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
		if !openAIAttachmentModelSafeMimes[mime] || imageBytes+pdfBytes+len(data) > attachmentMaxRequestBytes {
			continue
		}
		dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		if mime == "application/pdf" || strings.HasPrefix(mime, "text/") {
			if pdfs+1 > attachmentMaxPDFBlocks || pdfBytes+len(data) > attachmentMaxPDFBytes {
				continue
			}
			filename := filepath.Base(strings.TrimSpace(file.Name))
			if filename == "." || filename == string(filepath.Separator) || filename == "" {
				filename = "attachment"
			}
			if mime == "application/pdf" && !strings.EqualFold(filepath.Ext(filename), ".pdf") {
				filename += ".pdf"
			} else if mime == "text/markdown" && filepath.Ext(filename) == "" {
				filename += ".md"
			} else if mime == "text/plain" && filepath.Ext(filename) == "" {
				filename += ".txt"
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

// followUpAttachmentSourceAuthorized accepts the two exact authority states a
// queued artifact follow-up can observe: its triggering chat message may still
// hold the request's reservation, or it may already have committed that same
// source into the same destination. The current authenticated human must own
// the source in either state. This is intentionally narrower than ordinary
// committed-chat reads: a teammate can read a file posted in a shared channel,
// but cannot smuggle somebody else's attachment into their own provider run.
func (app *kanbanBoardApp) followUpAttachmentSourceAuthorized(user *userAccount, destination scoutChatThreadRecord, file scoutChatFileAttachment, reservationID string) bool {
	if app == nil || user == nil || strings.TrimSpace(file.SourceID) == "" || strings.TrimSpace(file.Ref) == "" {
		return false
	}
	email := normalizeAccountEmail(user.Email)
	app.pendingAttachmentUploadsMu.Lock()
	grant, ok := app.pendingAttachmentUploads[strings.TrimSpace(file.SourceID)]
	storeHealthy := app.attachmentSourceStoreErr == nil
	app.pendingAttachmentUploadsMu.Unlock()
	if !storeHealthy || !ok || grant.OwnerEmail != email || grant.SourceRevision != strings.TrimSpace(file.SourceRevision) ||
		grant.Ref != strings.TrimSpace(file.Ref) || grant.DestinationID != strings.TrimSpace(destination.ID) ||
		grant.DestinationRevision != scoutChatAttachmentDestinationRevision(destination) {
		return false
	}
	switch grant.State {
	case attachmentSourceReserved:
		if grant.ReservationID != strings.TrimSpace(reservationID) || !grant.ExpiresAt.After(time.Now().UTC()) {
			return false
		}
	case attachmentSourceCommitted:
		if strings.TrimSpace(grant.CommittedMessageID) == "" ||
			!app.committedChatAttachmentAuthorized(email, destination.ID, grant.CommittedMessageID, file) {
			return false
		}
	default:
		return false
	}
	meta, err := blobStatForRef(grant.Ref)
	return err == nil && attachmentSourceRevision(grant.Ref, meta) == grant.SourceRevision &&
		strings.ToLower(strings.TrimSpace(meta.Mime)) == grant.Mime && meta.Size == grant.Size
}

func (app *kanbanBoardApp) followUpAttachmentSourcesAuthorized(user *userAccount, destination scoutChatThreadRecord, files []scoutChatFileAttachment, reservationID string) bool {
	for _, file := range files {
		if strings.TrimSpace(file.Ref) == "" {
			continue
		}
		if !app.followUpAttachmentSourceAuthorized(user, destination, file, reservationID) {
			return false
		}
	}
	return true
}

// followUpAttachmentContentBlocksAuthorized performs the same exact-source
// check before and after each blob read. The async follow-up worker calls this
// only after re-resolving the current requester and destination immediately at
// provider admission; no launch-time byte block is retained as authority.
func (app *kanbanBoardApp) followUpAttachmentContentBlocksAuthorized(user *userAccount, destination scoutChatThreadRecord, files []scoutChatFileAttachment, reservationID string) []json.RawMessage {
	return attachmentContentBlocksWithReader(files, func(file scoutChatFileAttachment) ([]byte, blobMeta, bool) {
		if !app.followUpAttachmentSourceAuthorized(user, destination, file, reservationID) {
			return nil, blobMeta{}, false
		}
		data, meta, err := getBlob(strings.TrimSpace(file.Ref))
		if attachmentBlobReadAfterProbe != nil {
			attachmentBlobReadAfterProbe(strings.TrimSpace(file.SourceID))
		}
		if err != nil || attachmentSourceRevision(strings.TrimSpace(file.Ref), meta) != strings.TrimSpace(file.SourceRevision) ||
			!app.followUpAttachmentSourceAuthorized(user, destination, file, reservationID) {
			return nil, blobMeta{}, false
		}
		return data, meta, true
	})
}

// followUpOpenAIAttachmentContentAuthorized is the Responses-native twin of
// followUpAttachmentContentBlocksAuthorized. It deliberately uses the
// follow-up source contract (reserved by, or already committed for, the same
// current human and destination) rather than the broader ordinary attachment
// read contract. The source is checked before and after every blob read so a
// revocation or audience change cannot turn queued bytes into a bearer grant.
func (app *kanbanBoardApp) followUpOpenAIAttachmentContentAuthorized(user *userAccount, destination scoutChatThreadRecord, files []scoutChatFileAttachment, reservationID string) []openAIInputContent {
	return openAIAttachmentContentWithReader(files, func(file scoutChatFileAttachment) ([]byte, blobMeta, bool) {
		if !app.followUpAttachmentSourceAuthorized(user, destination, file, reservationID) {
			return nil, blobMeta{}, false
		}
		data, meta, err := getBlob(strings.TrimSpace(file.Ref))
		if attachmentBlobReadAfterProbe != nil {
			attachmentBlobReadAfterProbe(strings.TrimSpace(file.SourceID))
		}
		if err != nil || attachmentSourceRevision(strings.TrimSpace(file.Ref), meta) != strings.TrimSpace(file.SourceRevision) ||
			!app.followUpAttachmentSourceAuthorized(user, destination, file, reservationID) {
			return nil, blobMeta{}, false
		}
		return data, meta, true
	})
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

	recordConversationProviderCall(ctx)
	transcript, err := createOpenAITextResponse(ctx, apiKey, openAITextRequest{
		Model:           scoutExtractionModel(),
		Seat:            seatAttachments,
		Workflow:        "attachment_extract",
		Instructions:    attachmentDeriveInstructions,
		Input:           "Transcribe the key facts, numbers, names, and claims in the attached files for the team's shared memory.",
		ReasoningEffort: scoutExtractionReasoningEffort(),
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
	if grant.OriginFileID != "" {
		viewer := accountStore().findUser(viewerEmail)
		if viewer == nil {
			return false
		}
		current, ok := app.assistantFileSourceRevisionForDestination(context.Background(), viewer, grant.OriginFileID, thread)
		if !ok || current != grant.OriginRevision {
			return false
		}
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
	content, _ := app.committedOpenAIAttachmentContentVerdict(viewerEmail, threadID, messageID, files)
	return content
}

func (app *kanbanBoardApp) committedOpenAIAttachmentContentVerdict(viewerEmail string, threadID string, messageID string,
	files []scoutChatFileAttachment) ([]openAIInputContent, bool) {
	authorized := true
	content := openAIAttachmentContentWithReader(files, func(file scoutChatFileAttachment) ([]byte, blobMeta, bool) {
		if !app.committedChatAttachmentAuthorized(viewerEmail, threadID, messageID, file) {
			authorized = false
			return nil, blobMeta{}, false
		}
		data, meta, err := getBlob(strings.TrimSpace(file.Ref))
		if attachmentBlobReadAfterProbe != nil {
			attachmentBlobReadAfterProbe(strings.TrimSpace(file.SourceID))
		}
		if err != nil || attachmentSourceRevision(strings.TrimSpace(file.Ref), meta) != strings.TrimSpace(file.SourceRevision) ||
			!app.committedChatAttachmentAuthorized(viewerEmail, threadID, messageID, file) {
			authorized = false
			return nil, blobMeta{}, false
		}
		return data, meta, true
	})
	return content, authorized && app.committedAttachmentsAuthorized(viewerEmail, threadID, messageID, files)
}

// openAIReplyMediaContentForTurn keeps Project-bound reply ancestry honest.
// Until parent media is represented as exact canonical evidence, only ordinary
// unlinked replies may forward those bytes to a provider.
func (app *kanbanBoardApp) openAIReplyMediaContentForTurn(projectSourceBound bool, viewerEmail, threadID, messageID string) []openAIInputContent {
	if projectSourceBound {
		return nil
	}
	return app.openAIReplyMediaContent(viewerEmail, threadID, messageID)
}

// openAIProjectReplyMediaContentVerdict admits only the exact v3 support
// parts already signed into the Project manifest and confirmed in PostgreSQL.
// A valid but provider-unsupported source yields no block with authorized=true;
// any source, blob, destination, origin, or parent snapshot drift fails closed.
func (app *kanbanBoardApp) openAIProjectReplyMediaContentVerdict(viewerEmail, threadID string,
	reply *projectChatManifestReply) ([]openAIInputContent, bool, string) {
	if app == nil || reply == nil || reply.ManifestVersion != projectChatSourceManifestV3 {
		return nil, reply != nil && reply.ManifestVersion == projectChatSourceManifestVersion, ""
	}
	thread, _, err := app.scoutChatThreadByID(viewerEmail, threadID)
	if err != nil || !projectChatReplyJournalMatchesThread(reply, thread) {
		return nil, false, ""
	}
	index := scoutChatMessageIndex(thread, reply.MessageID)
	if index < 0 {
		return nil, false, ""
	}
	parent := thread.Messages[index]
	if len(reply.Media) != len(parent.Files)+func() int {
		if parent.Image != nil && validBlobRef(parent.Image.Ref) {
			return 1
		}
		return 0
	}() {
		return nil, false, ""
	}
	content := make([]openAIInputContent, 0, len(reply.Media))
	for ordinal, media := range reply.Media {
		if media.Ordinal != ordinal {
			return nil, false, media.SourceID
		}
		switch media.Kind {
		case "file":
			if ordinal >= len(parent.Files) {
				return nil, false, media.SourceID
			}
			file := parent.Files[ordinal]
			if strings.TrimSpace(file.SourceID) != media.SourceID || strings.TrimSpace(file.SourceRevision) != media.SourceRevision ||
				strings.TrimSpace(file.Ref) != media.BlobRef || !app.committedChatAttachmentAuthorized(viewerEmail, threadID, parent.ID, file) {
				return nil, false, media.SourceID
			}
			blocks := openAIAttachmentContentWithReader([]scoutChatFileAttachment{file}, func(current scoutChatFileAttachment) ([]byte, blobMeta, bool) {
				data, meta, readErr := getBlob(current.Ref)
				if attachmentBlobReadAfterProbe != nil {
					attachmentBlobReadAfterProbe(current.SourceID)
				}
				if readErr != nil || current.Ref != media.BlobRef || attachmentSourceRevision(current.Ref, meta) != media.SourceRevision ||
					strings.ToLower(strings.TrimSpace(meta.Mime)) != media.Mime || meta.Size != media.Size ||
					!app.committedChatAttachmentAuthorized(viewerEmail, threadID, parent.ID, current) {
					return nil, blobMeta{}, false
				}
				return data, meta, true
			})
			content = append(content, blocks...)
		case "generated_image":
			if ordinal != len(parent.Files) || parent.Image == nil || parent.Image.Ref != media.BlobRef {
				return nil, false, media.SourceID
			}
			data, meta, readErr := getBlob(media.BlobRef)
			if readErr != nil || attachmentSourceRevision(media.BlobRef, meta) != media.SourceRevision ||
				strings.ToLower(strings.TrimSpace(meta.Mime)) != media.Mime || meta.Size != media.Size {
				return nil, false, media.SourceID
			}
			if len(data) <= attachmentMaxRequestBytes && oneOf(media.Mime, "image/png", "image/jpeg", "image/webp") {
				content = append(content, openAIInputContent{Type: "input_image", ImageURL: "data:" + media.Mime + ";base64," + base64.StdEncoding.EncodeToString(data)})
			}
		default:
			return nil, false, media.SourceID
		}
	}
	postRead, _, postErr := app.scoutChatThreadByID(viewerEmail, threadID)
	if postErr != nil || !projectChatReplyJournalMatchesThread(reply, postRead) {
		return nil, false, ""
	}
	return content, true, ""
}

func (app *kanbanBoardApp) projectChatReplyMediaManifestCurrent(viewerEmail string, thread scoutChatThreadRecord,
	reply *projectChatManifestReply) bool {
	if app == nil || reply == nil || reply.ManifestVersion != projectChatSourceManifestV3 ||
		!projectChatReplyJournalMatchesThread(reply, thread) {
		return false
	}
	index := scoutChatMessageIndex(thread, reply.MessageID)
	if index < 0 {
		return false
	}
	parent := thread.Messages[index]
	imageCount := 0
	if parent.Image != nil && validBlobRef(parent.Image.Ref) {
		imageCount = 1
	}
	if len(reply.Media) != len(parent.Files)+imageCount {
		return false
	}
	for ordinal, media := range reply.Media {
		if media.Ordinal != ordinal {
			return false
		}
		if media.Kind == "file" {
			if ordinal >= len(parent.Files) {
				return false
			}
			file := parent.Files[ordinal]
			if file.SourceID != media.SourceID || file.SourceRevision != media.SourceRevision || file.Ref != media.BlobRef ||
				!app.committedChatAttachmentAuthorized(viewerEmail, thread.ID, parent.ID, file) {
				return false
			}
			meta, err := blobStatForRef(media.BlobRef)
			if err != nil || attachmentSourceRevision(media.BlobRef, meta) != media.SourceRevision || meta.Size != media.Size ||
				strings.ToLower(strings.TrimSpace(meta.Mime)) != media.Mime {
				return false
			}
			continue
		}
		if media.Kind != "generated_image" || ordinal != len(parent.Files) || parent.Image == nil || parent.Image.Ref != media.BlobRef {
			return false
		}
		meta, err := blobStatForRef(media.BlobRef)
		if err != nil || attachmentSourceRevision(media.BlobRef, meta) != media.SourceRevision || meta.Size != media.Size ||
			strings.ToLower(strings.TrimSpace(meta.Mime)) != media.Mime {
			return false
		}
	}
	return true
}

// openAIReplyMediaContent carries the exact image/file from the message being
// replied to into the current multimodal answer. Reply snippets alone are not
// visual context. The thread is re-read through its viewer ACL, committed file
// grants are revalidated, and generated-image blobs are digest-checked before
// any bytes reach OpenAI.
func (app *kanbanBoardApp) openAIReplyMediaContent(viewerEmail, threadID, messageID string) []openAIInputContent {
	thread, _, err := app.scoutChatThreadByID(viewerEmail, threadID)
	if err != nil {
		return nil
	}
	index := scoutChatMessageIndex(thread, strings.TrimSpace(messageID))
	if index < 0 {
		return nil
	}
	message := thread.Messages[index]
	content := app.committedOpenAIAttachmentContent(viewerEmail, threadID, message.ID, message.Files)
	if message.Image == nil || !validBlobRef(message.Image.Ref) {
		return content
	}
	data, meta, err := getBlob(message.Image.Ref)
	if err != nil || len(data) > attachmentMaxRequestBytes {
		return content
	}
	mime := strings.ToLower(strings.TrimSpace(meta.Mime))
	if !oneOf(mime, "image/png", "image/jpeg", "image/webp") {
		return content
	}
	content = append(content, openAIInputContent{Type: "input_image", ImageURL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)})
	return content
}

func (app *kanbanBoardApp) committedAttachmentsAuthorized(viewerEmail string, threadID string, messageID string, files []scoutChatFileAttachment) bool {
	for _, file := range files {
		if strings.TrimSpace(file.Ref) != "" && !app.committedChatAttachmentAuthorized(viewerEmail, threadID, messageID, file) {
			return false
		}
	}
	return true
}

func (app *kanbanBoardApp) privateRiffInvalidPublicationRoots(thread scoutChatThreadRecord) map[string]bool {
	invalid := map[string]bool{}
	var authorized map[string]privateRiffMemorySource
	for _, message := range thread.Messages {
		publication := message.Publication
		if publication == nil || publication.Version != privateRiffConversationPublicationVersion || publication.RootMessageID == "" {
			continue
		}
		validManifest := true
		if len(publication.ContextSources) > 0 {
			manifestDigest, err := digestAny(publication.ContextSources)
			validManifest = err == nil && publication.ContextManifestDigest != "" && manifestDigest == publication.ContextManifestDigest
		} else if publication.ContextManifestDigest != "" {
			validManifest = false
		}
		if len(publication.ContextSources) > 0 && authorized == nil && app != nil {
			authorized = app.privateRiffAuthorizedContextSources(context.Background(), thread.ID)
		}
		if !validManifest || (len(publication.ContextSources) > 0 && (app == nil || !privateRiffContextSourcesMatchAuthorized(authorized, publication.ContextSources))) {
			invalid[publication.RootMessageID] = true
		}
	}
	return invalid
}

func redactPrivateRiffPublicationMessage(message scoutChatMessageRecord) scoutChatMessageRecord {
	message.Text = "This shared Private Riff is unavailable because context used by the conversation is no longer authorized for this channel."
	message.Sources = nil
	message.IntentOutcome = string(conversationIntentUnavailable)
	message.Activity = nil
	message.Thread = nil
	message.Work = nil
	message.Proposal = nil
	message.Choices = nil
	message.Manifest = nil
	message.Image = nil
	message.ImageGeneration = nil
	message.Reply = nil
	message.Files = nil
	if message.ReplyTo != nil {
		message.ReplyTo = &scoutChatReplyRef{MessageID: message.ReplyTo.MessageID}
	}
	return message
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
func (app *kanbanBoardApp) projectScoutChatThreadForViewer(viewerEmail string, thread scoutChatThreadRecord, contexts ...context.Context) scoutChatThreadRecord {
	return app.projectScoutChatThreadForViewerEpisode(viewerEmail, thread, "", contexts...)
}

func (app *kanbanBoardApp) projectScoutChatThreadForViewerEpisode(viewerEmail string, thread scoutChatThreadRecord, episodeID string, contexts ...context.Context) scoutChatThreadRecord {
	return app.projectScoutChatThreadForViewerEpisodeWithResults(viewerEmail, thread, episodeID, true, contexts...)
}

func (app *kanbanBoardApp) projectScoutChatThreadForViewerEpisodeWithResults(viewerEmail string, thread scoutChatThreadRecord, episodeID string, includeArtifactResults bool, contexts ...context.Context) scoutChatThreadRecord {
	projected := thread
	projectionContext := context.Background()
	if len(contexts) > 0 && contexts[0] != nil {
		projectionContext = contexts[0]
	}
	var resultIndex scoutChatResultProjectionIndex
	var resultViewer *userAccount
	if includeArtifactResults {
		resultIndex = app.scoutChatResultIndex()
		resultViewer = accountStore().findUser(normalizeAccountEmail(viewerEmail))
	}
	if thread.Riff != nil {
		projected.ConversationKind = "channel_riff"
	}
	meetingRecordCurrent := app == nil || app.meetingRecordConversationBindingCurrent(viewerEmail, thread)
	pendingDeletes := map[string]bool{}
	for _, operation := range thread.ProjectSourceMutationOperations {
		if operation.State == "pending" && operation.Kind == "delete" {
			pendingDeletes[operation.MessageID] = true
		}
	}
	projected.OpeningOperation = nil
	projected.VoiceSession = nil
	projected.MeetingRecord = nil
	projected.Riff = app.projectPrivateRiffBindingForEpisode(viewerEmail, thread, episodeID)
	projected.LegacyConversationOperations = nil
	projected.ModerationReceipts = nil
	projected.ProjectLinkOperations = nil
	projected.ProjectCorrectionOperations = nil
	projected.ProjectSourceMutationOperations = nil
	if thread.Riff != nil && !app.privateRiffSourceAccessible(viewerEmail, thread) {
		projected.Title = "Private Riff"
		projected.Preview = "Private channel context is no longer available"
		projected.Messages = nil
		return projected
	}
	if thread.MeetingRecord != nil && !meetingRecordCurrent {
		projected.Title = "Meeting Record conversation"
		projected.Preview = "The bound Meeting Record revision is unavailable"
	}
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
	// A v2 Riff publication is one authored conversation, even though it is
	// stored as a channel root plus replies. If any turn relied on broader
	// context that is no longer authorized for this destination, redact the
	// entire batch. Keeping only the root or neighboring replies could leak the
	// withdrawn context through references and would misrepresent the shared
	// conversation as complete.
	invalidRiffPublicationRoots := app.privateRiffInvalidPublicationRoots(thread)
	projected.Messages = make([]scoutChatMessageRecord, 0, len(thread.Messages))
	viewedEpisodeID := ""
	if projected.Riff != nil && privateRiffIsSpace(thread) {
		viewedEpisodeID = projected.Riff.ViewedEpisodeID
	}
	for _, message := range thread.Messages {
		if viewedEpisodeID != "" && message.RiffEpisodeID != viewedEpisodeID {
			continue
		}
		if pendingDeletes[message.ID] || (message.CausedByMessageID != "" && pendingDeletes[message.CausedByMessageID]) {
			continue
		}
		if !meetingRecordCurrent {
			role := strings.ToLower(strings.TrimSpace(message.Role))
			if role == "scout" || role == "assistant" {
				message.Text = "This Meeting Record answer is unavailable because its exact source revision is no longer authorized."
				message.Sources = nil
				message.IntentOutcome = string(conversationIntentUnavailable)
				message.Thread = nil
				message.Work = nil
				message.Proposal = nil
				message.Choices = nil
				message.Manifest = nil
				message.Image = nil
				message.ImageGeneration = nil
				message.Reply = nil
				message.Files = nil
			}
		}
		if message.Publication != nil && invalidRiffPublicationRoots[message.Publication.RootMessageID] {
			message = redactPrivateRiffPublicationMessage(message)
		}
		projected.Messages = append(projected.Messages, message)
	}
	for messageIndex := range projected.Messages {
		original := projected.Messages[messageIndex]
		if includeArtifactResults {
			app.projectScoutChatResultRef(projectionContext, resultViewer, &projected.Messages[messageIndex], resultIndex)
		}
		projected.Messages[messageIndex].SourceOperationID = ""
		projected.Messages[messageIndex].SourceOperationDigest = ""
		projected.Messages[messageIndex].RiffAuthority = nil
		if original.Publication != nil {
			publication := *original.Publication
			publication.OperationID = ""
			publication.RiffThreadID = ""
			publication.SourceMessageID = ""
			publication.SelectionDigest = ""
			publication.ContextManifestDigest = ""
			publication.ContextSources = nil
			projected.Messages[messageIndex].Publication = &publication
		}
		if original.Activity != nil {
			activity := *original.Activity
			activity.SourceMessageDigest = ""
			activity.SourceWindowDigest = ""
			activity.SourceAudienceDigest = ""
			activity.ContextManifestDigest = ""
			activity.ContextSources = nil
			projected.Messages[messageIndex].Activity = &activity
		}
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
		if original.ImageGeneration != nil {
			generation := *original.ImageGeneration
			generation.Prompt = ""
			generation.RequestedByEmail = ""
			generation.RequestedByName = ""
			generation.Phase = ""
			generation.PhaseGeneration = 0
			generation.ResultRef = ""
			generation.ResultMime = ""
			generation.ArtifactID = ""
			generation.FailureClass = ""
			generation.FailureText = ""
			projected.Messages[messageIndex].ImageGeneration = &generation
		}
		if original.Image != nil {
			image := *original.Image
			if artifact, ok := app.osArtifactByID(strings.TrimSpace(image.ArtifactID)); ok {
				image.SavedToFiles = strings.EqualFold(strings.TrimSpace(artifact.Metadata["savedToFiles"]), "true")
			}
			projected.Messages[messageIndex].Image = &image
		}
	}
	return projected
}

type scoutChatResultProjectionIndex struct {
	byID                      map[string]meetingMemoryEntry
	deckByGoal                map[string]meetingMemoryEntry
	acceptedDeckByGoal        map[string]meetingMemoryEntry
	acceptedDeckBindingByGoal map[string]scoutChatAcceptedDeckBinding
	documentByGoal            map[string]meetingMemoryEntry
	goalPlanByID              map[string]goalPlan
}

type scoutChatAcceptedDeckBinding struct {
	State   string
	Version int
	Digest  string
}

const (
	scoutChatResultApprovalExact  = "approved_exact"
	scoutChatResultApprovalEdited = "edited_after_approval"
	scoutChatResultApprovalLegacy = "legacy_approval_binding"
)

var scoutChatResultIndexProbe func()

func (app *kanbanBoardApp) scoutChatResultIndex() scoutChatResultProjectionIndex {
	if scoutChatResultIndexProbe != nil {
		scoutChatResultIndexProbe()
	}
	index := scoutChatResultProjectionIndex{
		byID:                      map[string]meetingMemoryEntry{},
		deckByGoal:                map[string]meetingMemoryEntry{},
		acceptedDeckByGoal:        map[string]meetingMemoryEntry{},
		acceptedDeckBindingByGoal: map[string]scoutChatAcceptedDeckBinding{},
		documentByGoal:            map[string]meetingMemoryEntry{},
		goalPlanByID:              map[string]goalPlan{},
	}
	if app == nil {
		return index
	}
	// One store snapshot per thread projection keeps a large channel O(A+M),
	// rather than scanning every artifact once for every work message.
	artifacts := app.osArtifactsSnapshot(0)
	deckCandidates := map[string][]meetingMemoryEntry{}
	documentCandidates := map[string][]meetingMemoryEntry{}
	for _, artifact := range artifacts {
		index.byID[artifact.ID] = artifact
		if artifactType(artifact) == artifactTypeHTMLDeck && artifactIsHTMLDocument(artifact) {
			goalID := ""
			if strings.TrimSpace(artifact.Metadata["source"]) == "packaging_studio_ship" &&
				strings.TrimSpace(artifact.Metadata["artifactContract"]) == packagingStudioDeckContract {
				goalID = strings.TrimSpace(artifact.Metadata["goalId"])
			} else if strings.TrimSpace(artifact.Metadata["source"]) == "scout_thread" {
				goalID = strings.TrimSpace(artifact.Metadata["goalParentId"])
			}
			if goalID != "" {
				deckCandidates[goalID] = append(deckCandidates[goalID], artifact)
			}
		}
		if artifactType(artifact) == artifactTypeMarkdown &&
			strings.TrimSpace(artifact.Metadata["source"]) == "scout_thread" &&
			strings.EqualFold(strings.TrimSpace(artifact.Metadata["goalDeliverable"]), "true") {
			if goalID := strings.TrimSpace(artifact.Metadata["goalParentId"]); goalID != "" {
				documentCandidates[goalID] = append(documentCandidates[goalID], artifact)
			}
		}
	}
	for _, goal := range artifacts {
		if strings.TrimSpace(goal.Metadata["mode"]) != "goal" {
			continue
		}
		acceptedID := strings.TrimSpace(goal.Metadata["acceptedResultArtifactId"])
		acceptedVersion, _ := strconv.Atoi(strings.TrimSpace(goal.Metadata["acceptedResultArtifactVersion"]))
		acceptedDigest := strings.TrimSpace(goal.Metadata["acceptedResultArtifactDigest"])
		var plan goalPlan
		if raw := strings.TrimSpace(goal.Metadata["goalPlan"]); raw != "" && json.Unmarshal([]byte(raw), &plan) == nil {
			index.goalPlanByID[goal.ID] = plan
			if strings.TrimSpace(plan.Report.AcceptedResultArtifactID) != "" {
				acceptedID = strings.TrimSpace(plan.Report.AcceptedResultArtifactID)
				acceptedVersion = plan.Report.AcceptedResultArtifactVersion
				acceptedDigest = strings.TrimSpace(plan.Report.AcceptedResultArtifactDigest)
			}
		}
		if accepted, ok := index.byID[acceptedID]; ok && scoutChatDeckBelongsToGoal(accepted, goal.ID) {
			index.acceptedDeckByGoal[goal.ID] = accepted
			binding := scoutChatAcceptedDeckBinding{State: scoutChatResultApprovalLegacy}
			if acceptedVersion > 0 && acceptedDigest != "" {
				binding.Version = acceptedVersion
				binding.Digest = acceptedDigest
				binding.State = scoutChatResultApprovalEdited
				if acceptedVersion == artifactVersion(accepted) && strings.EqualFold(acceptedDigest, artifactCapabilityDigest(accepted)) {
					binding.State = scoutChatResultApprovalExact
				}
			}
			index.acceptedDeckBindingByGoal[goal.ID] = binding
		} else {
			// Compatibility for goals approved before acceptedResultArtifactId was
			// introduced: freeze the latest eligible deck that existed when the ship
			// checkpoint resolved. A later retry cannot move that historical cutoff.
			checkpoint := plan.Checkpoint
			if checkpoint != nil && checkpoint.StageID == "ship_approval" && checkpoint.LastAction == processCheckpointActionProceed && strings.TrimSpace(checkpoint.ResolvedAt) != "" {
				if resolvedAt, err := time.Parse(time.RFC3339Nano, checkpoint.ResolvedAt); err == nil {
					for _, candidate := range deckCandidates[goal.ID] {
						prior, found := index.acceptedDeckByGoal[goal.ID]
						if !candidate.CreatedAt.After(resolvedAt) && (!found || candidate.CreatedAt.After(prior.CreatedAt)) {
							index.acceptedDeckByGoal[goal.ID] = candidate
							index.acceptedDeckBindingByGoal[goal.ID] = scoutChatAcceptedDeckBinding{State: scoutChatResultApprovalLegacy}
						}
					}
				}
			}
		}

		// A candidate deck remains private to the process while render/jury/gate
		// are running. The channel receives Edit/Present only after ship_compile
		// has durably named the exact reviewed candidate (or after a legacy goal
		// already reached verified terminal state).
		if plan.State == goalStateBlocked {
			salvageID := strings.TrimSpace(plan.Report.DeliverableArtifactID)
			if salvage, ok := index.byID[salvageID]; ok && scoutChatDeckBelongsToGoal(salvage, goal.ID) {
				index.deckByGoal[goal.ID] = salvage
			}
		} else if ready, ok := scoutChatReadyDeckForGoal(index.byID, goal.ID, plan); ok {
			index.deckByGoal[goal.ID] = ready
		}

		// Successful document/report goals expose only the server-marked
		// deliverable child. A blocked goal may expose only its exact salvaged
		// draft id. Intermediate research/dossier children never become the
		// editable result merely because they are newer or longer.
		documentID := ""
		if plan.State == goalStateBlocked {
			documentID = strings.TrimSpace(plan.Report.DeliverableArtifactID)
		} else if plan.State == goalStateVerified {
			if deliverable := plan.subtaskByID(goalDeliverableSubtaskID(&plan)); deliverable != nil {
				documentID = strings.TrimSpace(deliverable.ArtifactID)
			}
			if documentID == "" {
				for _, candidate := range documentCandidates[goal.ID] {
					if scoutChatDocumentBelongsToGoal(candidate, goal.ID, plan) {
						documentID = candidate.ID
					}
				}
			}
		}
		if document, ok := index.byID[documentID]; ok && scoutChatDocumentBelongsToGoal(document, goal.ID, plan) {
			index.documentByGoal[goal.ID] = document
		}
	}
	return index
}

func scoutChatReadyDeckForGoal(byID map[string]meetingMemoryEntry, goalID string, plan goalPlan) (meetingMemoryEntry, bool) {
	if plan.ProcessID != packagingStudioProcessID {
		return meetingMemoryEntry{}, false
	}
	ship := plan.subtaskByID("ship_compile")
	if ship == nil || ship.Status != subtaskComplete || strings.TrimSpace(ship.ArtifactID) == "" {
		return meetingMemoryEntry{}, false
	}
	record, ok := byID[strings.TrimSpace(ship.ArtifactID)]
	if !ok || strings.TrimSpace(record.Metadata["processStage"]) != "ship_compile" || strings.TrimSpace(record.Metadata["goalParentId"]) != goalID {
		return meetingMemoryEntry{}, false
	}
	deckID := strings.TrimSpace(record.Metadata["deckArtifactId"])
	deck, ok := byID[deckID]
	if !ok || !scoutChatDeckBelongsToGoal(deck, goalID) {
		return meetingMemoryEntry{}, false
	}
	return deck, true
}

func scoutChatDeckBelongsToGoal(deck meetingMemoryEntry, goalID string) bool {
	if artifactType(deck) != artifactTypeHTMLDeck || !artifactIsHTMLDocument(deck) {
		return false
	}
	shipForGoal := strings.TrimSpace(deck.Metadata["source"]) == "packaging_studio_ship" &&
		strings.TrimSpace(deck.Metadata["goalId"]) == goalID &&
		strings.TrimSpace(deck.Metadata["artifactContract"]) == packagingStudioDeckContract
	childForGoal := strings.TrimSpace(deck.Metadata["source"]) == "scout_thread" &&
		strings.TrimSpace(deck.Metadata["goalParentId"]) == goalID
	return shipForGoal || childForGoal
}

func scoutChatDocumentBelongsToGoal(document meetingMemoryEntry, goalID string, plan goalPlan) bool {
	expectedStageID := goalDeliverableSubtaskID(&plan)
	if expectedStageID == "" || strings.TrimSpace(document.Metadata["goalSubtaskId"]) != expectedStageID {
		return false
	}
	if processID := strings.TrimSpace(plan.ProcessID); processID != "" {
		expectedContract := strings.TrimSpace(plan.ResultOutputContract)
		if expectedContract == "" {
			// Backward-compatible migration only. New plans persist this binding
			// at instantiation so a later registry change cannot move the result.
			if definition, ok := processByID(processID); ok {
				if stage, found := definition.stageByID(expectedStageID); found {
					expectedContract = strings.TrimSpace(stage.OutputContract)
				}
			}
		}
		if expectedContract == "" || strings.TrimSpace(document.Metadata["outputContract"]) != expectedContract {
			return false
		}
	}
	return artifactType(document) == artifactTypeMarkdown &&
		strings.TrimSpace(document.Metadata["source"]) == "scout_thread" &&
		strings.TrimSpace(document.Metadata["goalParentId"]) == strings.TrimSpace(goalID) &&
		strings.EqualFold(strings.TrimSpace(document.Metadata["goalDeliverable"]), "true")
}

// scoutChatIndexedArtifactCurrent revalidates the parent/run revision selected
// by a reusable result index. Public terminal fanout shares that index across
// viewers, so a goal edit between recipients must not let a later payload use
// the earlier goal plan. The current header is body-free; version + content
// digest cover the goal plan and authored body used for result selection.
func (app *kanbanBoardApp) scoutChatIndexedArtifactCurrent(indexed meetingMemoryEntry) bool {
	if app == nil || app.memory == nil || strings.TrimSpace(indexed.ID) == "" {
		return false
	}
	current, found := app.memory.artifactAuthorizationHeaderByID(indexed.ID)
	if !found {
		return false
	}
	expected := artifactAuthorizationHeaderFromEntry(indexed)
	return current.ContentRevision == expected.ContentRevision &&
		strings.EqualFold(strings.TrimSpace(current.ContentDigest), strings.TrimSpace(expected.ContentDigest))
}

// projectScoutChatResultRef upgrades old and new work messages at the read
// boundary with the concrete presentation they produced. The binding is
// server-owned and conjunctive: a direct ref must itself be a deck, while a
// goal ref may resolve only a declared HTML deck filed by Packaging Studio or
// a later Scout artifact whose goalParentId is that exact goal. No title
// sniffing and no cross-goal artifact search is allowed.
func (app *kanbanBoardApp) projectScoutChatResultRef(ctx context.Context, viewer *userAccount, message *scoutChatMessageRecord, index scoutChatResultProjectionIndex) {
	if message == nil || message.Thread == nil {
		return
	}
	projectedRef := *message.Thread
	message.Thread = &projectedRef
	ref := message.Thread
	ref.ResultArtifactID = ""
	ref.ResultArtifactType = ""
	ref.ResultTitle = ""
	ref.ResultPreview = ""
	ref.ResultApprovalState = ""
	ref.ResultCanEdit = false
	artifact, found := index.byID[strings.TrimSpace(ref.ArtifactID)]
	if !found || !app.scoutChatIndexedArtifactCurrent(artifact) {
		return
	}
	result := artifact
	goalResult := strings.TrimSpace(artifact.Metadata["mode"]) == "goal"
	acceptedBinding := scoutChatAcceptedDeckBinding{}
	selectedAcceptedDeck := false
	if goalResult {
		deck, ok := index.acceptedDeckByGoal[artifact.ID]
		if ok {
			selectedAcceptedDeck = true
			acceptedBinding = index.acceptedDeckBindingByGoal[artifact.ID]
		}
		if !ok {
			deck, ok = index.deckByGoal[artifact.ID]
		}
		if ok {
			if !scoutChatDeckBelongsToGoal(deck, artifact.ID) {
				return
			}
			result = deck
		} else if document, found := index.documentByGoal[artifact.ID]; found {
			if !scoutChatDocumentBelongsToGoal(document, artifact.ID, index.goalPlanByID[artifact.ID]) {
				return
			}
			result = document
		} else {
			return
		}
	}
	currentResult, authorized := app.authorizedScoutChatResultArtifact(ctx, viewer, result.ID)
	if !authorized {
		return
	}
	result = currentResult
	ref.ResultCanEdit = artifactAuthorized(ctx, viewer, ACLWrite, result)
	if selectedAcceptedDeck && acceptedBinding.State == scoutChatResultApprovalExact &&
		(acceptedBinding.Version != artifactVersion(result) || !strings.EqualFold(acceptedBinding.Digest, artifactCapabilityDigest(result))) {
		acceptedBinding.State = scoutChatResultApprovalEdited
	}
	if goalResult {
		if artifactType(result) == artifactTypeHTMLDeck {
			if !scoutChatDeckBelongsToGoal(result, artifact.ID) {
				return
			}
		} else if !scoutChatDocumentBelongsToGoal(result, artifact.ID, index.goalPlanByID[artifact.ID]) {
			return
		}
	}
	if !goalResult && !scoutChatStandaloneTerminalResult(result, ref) {
		return
	}
	resultType := artifactType(result)
	if resultType == artifactTypeHTMLDeck && artifactIsHTMLDocument(result) {
		ref.ResultArtifactID = result.ID
		ref.ResultArtifactType = artifactTypeHTMLDeck
		ref.ResultTitle = firstNonEmptyString(strings.TrimSpace(result.Metadata["title"]), "Presentation")
		if selectedAcceptedDeck {
			ref.ResultApprovalState = acceptedBinding.State
		}
		return
	}
	if resultType != artifactTypeMarkdown || (strings.TrimSpace(artifact.Metadata["mode"]) != "goal" && !oneOf(strings.ToLower(strings.TrimSpace(result.Metadata["threadStatus"])), codexJobStatusComplete, artifactStatusApproved, artifactStatusPublished)) {
		return
	}
	ref.ResultArtifactID = result.ID
	ref.ResultArtifactType = artifactTypeMarkdown
	ref.ResultTitle = firstNonEmptyString(strings.TrimSpace(result.Metadata["title"]), "Document")
	ref.ResultPreview = truncateAgentThreadText(strings.TrimSpace(stripOpenAIWebCitationReceipt(result.Text)), 1200)
}

// authorizedScoutChatResultArtifact performs the authorization check against
// a body-free current header, then returns only the exact body revision whose
// header still matches under the store lock. This is the final serialization
// seam, so an earlier channel-wide index can never authorize a stale body.
func (app *kanbanBoardApp) authorizedScoutChatResultArtifact(ctx context.Context, viewer *userAccount, id string) (meetingMemoryEntry, bool) {
	if app == nil || app.memory == nil || viewer == nil || strings.TrimSpace(id) == "" {
		return meetingMemoryEntry{}, false
	}
	header, found := app.memory.artifactAuthorizationHeaderByID(id)
	if !found || !artifactHeaderAuthorized(ctx, viewer, ACLReadContent, header) {
		return meetingMemoryEntry{}, false
	}
	if artifactAuthorizationAfterCheckProbe != nil {
		artifactAuthorizationAfterCheckProbe()
	}
	return app.memory.artifactSnapshotIfHeaderMatches(id, header)
}

// scoutChatStandaloneTerminalResult distinguishes a user-facing, direct
// deliverable from process receipts and goal children. Internal stages may be
// complete Markdown too, but they belong only in the parent activity ledger.
func scoutChatStandaloneTerminalResult(result meetingMemoryEntry, ref *scoutChatThreadRef) bool {
	if ref == nil || strings.TrimSpace(result.Metadata["source"]) != "scout_thread" || strings.TrimSpace(result.Metadata["goalParentId"]) != "" {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(firstNonEmptyString(result.Metadata["threadStatus"], result.Metadata["status"])))
	return strings.TrimSpace(result.ID) == strings.TrimSpace(ref.ArtifactID) &&
		strings.TrimSpace(result.Metadata["threadId"]) == strings.TrimSpace(ref.ID) &&
		oneOf(status, codexJobStatusComplete, artifactStatusApproved, artifactStatusPublished)
}

// scoutChatThreadRefMayExposeResult is the event hot-path gate. Active work
// can never own a channel-facing result, so queued/running progress frames do
// not scan the artifact store. Terminal/review states still build the exact
// result index so completion appears immediately and a later ACL/revision
// change authoritatively clears stale Result* fields.
func scoutChatThreadRefMayExposeResult(ref *scoutChatThreadRef) bool {
	if ref == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(ref.Status)) {
	case codexJobStatusComplete, "completed", artifactStatusPublished, artifactStatusApproved, "verified",
		codexJobStatusApprovalRequired, "error", codexJobStatusFailed, "needs_attention":
		return true
	default:
		return false
	}
}

func clearScoutChatMessageResultRef(message *scoutChatMessageRecord) {
	if message == nil || message.Thread == nil {
		return
	}
	ref := *message.Thread
	ref.ResultArtifactID = ""
	ref.ResultArtifactType = ""
	ref.ResultTitle = ""
	ref.ResultPreview = ""
	ref.ResultApprovalState = ""
	ref.ResultCanEdit = false
	message.Thread = &ref
}

func (app *kanbanBoardApp) projectScoutChatMessageForViewer(viewerEmail string, thread scoutChatThreadRecord, message scoutChatMessageRecord, contexts ...context.Context) scoutChatMessageRecord {
	var resultIndex *scoutChatResultProjectionIndex
	if scoutChatThreadRefMayExposeResult(message.Thread) {
		index := app.scoutChatResultIndex()
		resultIndex = &index
	}
	return app.projectScoutChatMessageForViewerWithResultIndex(viewerEmail, thread, message, resultIndex, contexts...)
}

// projectScoutChatMessageForViewerWithResultIndex lets a terminal public
// event build the O(artifacts) index once, then apply per-viewer ACL checks
// without repeating that scan for every roster member.
func (app *kanbanBoardApp) projectScoutChatMessageForViewerWithResultIndex(viewerEmail string, thread scoutChatThreadRecord, message scoutChatMessageRecord, resultIndex *scoutChatResultProjectionIndex, contexts ...context.Context) scoutChatMessageRecord {
	// Publication revocation is batch-wide, but ordinary message events retain
	// the historical O(1-message) projection. Only a v2 publication scans its
	// sibling provenance receipts, and it computes one authorized context map.
	if message.Publication != nil && message.Publication.Version == privateRiffConversationPublicationVersion {
		invalid := app.privateRiffInvalidPublicationRoots(thread)
		if invalid[message.Publication.RootMessageID] {
			message = redactPrivateRiffPublicationMessage(message)
		}
		// These receipts are server-only and the one-message projection below
		// would otherwise repeat the already completed authorization scan.
		publication := *message.Publication
		publication.ContextManifestDigest = ""
		publication.ContextSources = nil
		message.Publication = &publication
	}
	// Attachment/publication projection remains O(1-message). Result hydration
	// is applied below from the optional prebuilt terminal index.
	projected := app.projectScoutChatThreadForViewerEpisodeWithResults(viewerEmail, scoutChatThreadRecord{
		ID: thread.ID, OwnerEmail: thread.OwnerEmail, Visibility: thread.Visibility, ConversationKind: thread.ConversationKind,
		Riff: thread.Riff, Messages: []scoutChatMessageRecord{message},
	}, message.RiffEpisodeID, false, contexts...)
	if len(projected.Messages) == 0 {
		return scoutChatMessageRecord{ID: message.ID, Kind: message.Kind, Role: message.Role, CreatedAt: message.CreatedAt,
			RiffEpisodeID: message.RiffEpisodeID, RiffCheckpointID: message.RiffCheckpointID}
	}
	result := projected.Messages[0]
	// A running retry must remove a previously enriched client result without
	// paying for a store scan. Terminal events start from the same resultless
	// baseline, then repopulate only an exact authorized current snapshot.
	clearScoutChatMessageResultRef(&result)
	if resultIndex != nil && result.Thread != nil {
		projectionContext := context.Background()
		if len(contexts) > 0 && contexts[0] != nil {
			projectionContext = contexts[0]
		}
		app.projectScoutChatResultRef(projectionContext, accountStore().findUser(normalizeAccountEmail(viewerEmail)), &result, *resultIndex)
	}
	return result
}

// projectScoutChatResponseForViewer applies the same attachment projection to
// every chat HTTP response shape. Keeping this at the handler boundary avoids
// relying on each mutator to remember that a source can be revoked between a
// successful commit and serialization back to the requester.
func (app *kanbanBoardApp) projectScoutChatResponseForViewer(viewerEmail string, threadID string, response map[string]any, contexts ...context.Context) map[string]any {
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
	projected["thread"] = app.projectScoutChatThreadForViewer(viewerEmail, thread, contexts...)
	for _, key := range []string{"message", "answer"} {
		if message, ok := response[key].(scoutChatMessageRecord); ok {
			projected[key] = app.projectScoutChatMessageForViewer(viewerEmail, thread, message, contexts...)
		}
	}
	return projected
}
