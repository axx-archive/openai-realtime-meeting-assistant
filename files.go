package main

// Files surface (card 095) — the Google-Drive-like door over durable files the
// team has deliberately filed. One list, two persisted sources plus an
// explicit chat-promotion seam:
//
//  1. Direct uploads: POST /assistant/files/upload stores the bytes through
//     putBlob and appends a first-class kind=file memory entry whose Text is
//     the file's name + the 085 derived transcript, so a direct upload feeds
//     answer_memory_question exactly like a chat upload feeds thread context.
//  2. Chat attachments stay in chat by default. POST /assistant/files/save can
//     explicitly copy one readable attachment into a first-class kind=file
//     entry, preserving the source message while avoiding Drive clutter.
//  3. Agent deliverables: terminal, good-status os_artifact work products
//     (research reports, decks, goal outputs) adapt into rows that open in the
//     artifact stage via ArtifactID — no bytes to download, the artifact IS
//     the file.
//
// Every row is decorated for the client with the session-gated blob download
// URL (/artifacts/blob, blobs.go) plus the honest feeds-the-brain badge:
// "ingested" when derived/extracted text rides model context, "stored" when
// only the bytes are durable (keyless deploys, non-model mimes). Rows organize
// into the flat folder layer of file_folders.go (folderId + the folders list
// on the GET payload).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	errFileSaveArtifactID     = errors.New("artifactId is required")
	errFileSaveSourceID       = errors.New("sourceFileId is required")
	errFileSaveNotFound       = errors.New("artifact not found")
	errFileSaveSourceNotFound = errors.New("attachment not found")
	errFileSaveNotDeliverable = errors.New("only a finished deliverable can be saved to Files")
	errAssistantFileName      = errors.New("file name is required")
)

var fileSaveAfterArtifactStampProbe func()

const (
	// meetingMemoryKindFile is one uploaded file per entry. Like kind
	// decision it is deliberately NOT a UI-state kind: entry.Text carries the
	// file name + derived transcript so store.search grounds Scout's answers
	// on uploaded material ("feeds the brain" is literal). It is still
	// excluded from the client memory timeline via visibleMeetingMemoryEntries
	// — the Files surface is its render home.
	meetingMemoryKindFile = "file"

	fileBrainStatusIngested = "ingested"
	fileBrainStatusStored   = "stored"
	// fileBrainStatusThread is the honest middle badge for a PRIVATE chat
	// attachment: Scout read its derived text, but that text rides only the
	// owning 1:1 thread's context and never enters company-wide recall — so it
	// is neither company "ingested" nor bytes-only "stored".
	fileBrainStatusThread = "thread"

	// assistantFilesListLimit caps the list response; the newest uploads win.
	assistantFilesListLimit = 400

	// assistantFileNameMaxLen keeps pathological filenames out of the store.
	assistantFileNameMaxLen = 160
)

// Per-file ACL, versions, trash and quota (STRIDE v2 Wave 5 D1/D2/D5/D6/D9).
// The policy lives on the kind=file metadata row and is read through
// authorizeFileEntry, the Files-surface analogue of the artifact
// ObjectAuthorizer: handlers authorize the body-free metadata header with an
// ACLAction before any Text or bytes are projected.
const (
	fileVisibilityPrivate = "private"
	fileVisibilityCompany = "company"
	fileVisibilityPeople  = "people"

	// fileTrashRetention is how long a trashed upload stays restorable before
	// the daily sweep hard-deletes its row (blob GC is a separate, later sweep).
	fileTrashRetention      = 30 * 24 * time.Hour
	fileTrashSweepInterval  = 24 * time.Hour
	fileTrashSweepBootDelay = 10 * time.Minute

	driveQuotaBytesEnv     = "DRIVE_QUOTA_BYTES"
	driveQuotaBytesDefault = int64(20) << 30
)

var (
	errFileAccessForbidden   = errors.New("only the uploader can change who this file is shared with")
	errFileGrantUnregistered = errors.New("one or more people are not registered accounts")
	errFileVisibilityInvalid = errors.New("visibility must be private, company, or people")
	errFileNotTrashed        = errors.New("file is not in the trash")
	errFileQuotaExceeded     = errors.New("drive quota exceeded")
)

// normalizeFileVisibility maps the stored value onto the closed vocabulary.
// Empty is the legacy (pre-ACL) row and reads as company — exactly today's
// every-member-sees-every-upload contract. The recall store's own
// organization/private synonyms (recallEntryScopeAllowed) map onto the same
// two buckets so a row stamped by that vocabulary reads identically in both
// lanes; any other value fails closed until it acquires an explicit policy.
func normalizeFileVisibility(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", fileVisibilityCompany, "organization", "org", "team", "public", "shared":
		return fileVisibilityCompany, true
	case fileVisibilityPrivate, "owner":
		return fileVisibilityPrivate, true
	case fileVisibilityPeople:
		return fileVisibilityPeople, true
	default:
		return "", false
	}
}

func fileEntryVisibility(metadata map[string]string) (string, bool) {
	return normalizeFileVisibility(metadata["visibility"])
}

// splitFileEmailList parses a comma-separated email list into normalized,
// deduplicated, sorted emails (the grants/starredBy encoding).
func splitFileEmailList(raw string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 4)
	for _, part := range strings.Split(raw, ",") {
		email := normalizeAccountEmail(part)
		if email == "" {
			continue
		}
		if _, dup := seen[email]; dup {
			continue
		}
		seen[email] = struct{}{}
		out = append(out, email)
	}
	sort.Strings(out)
	return out
}

func fileGrantEmails(metadata map[string]string) []string {
	return splitFileEmailList(metadata["grants"])
}

func fileEntryTrashed(metadata map[string]string) bool {
	return strings.TrimSpace(metadata["deletedAt"]) != ""
}

func fileEntrySuperseded(metadata map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(metadata["superseded"]), "true")
}

func fileEntryVersion(metadata map[string]string) int {
	version, err := strconv.Atoi(strings.TrimSpace(metadata["version"]))
	if err != nil || version < 1 {
		return 1
	}
	return version
}

func fileEntryStarredBy(metadata map[string]string, email string) bool {
	email = normalizeAccountEmail(email)
	if email == "" {
		return false
	}
	for _, starred := range splitFileEmailList(metadata["starredBy"]) {
		if starred == email {
			return true
		}
	}
	return false
}

// fileGrantsAllowEmail reports whether email is the uploader or a listed
// grant. It is the people-visibility predicate shared with recall scoping.
func fileGrantsAllowEmail(metadata map[string]string, email string) bool {
	email = normalizeAccountEmail(email)
	if email == "" {
		return false
	}
	if email == normalizeAccountEmail(metadata["uploaderEmail"]) {
		return true
	}
	for _, grant := range fileGrantEmails(metadata) {
		if grant == email {
			return true
		}
	}
	return false
}

// fileEntryReadableByEmail is the pure metadata read policy for the legacy
// email principal: company → any signed-in member, private → uploader,
// people → uploader or grant. Unknown visibility fails closed.
func fileEntryReadableByEmail(metadata map[string]string, email string) bool {
	visibility, ok := fileEntryVisibility(metadata)
	if !ok {
		return false
	}
	email = normalizeAccountEmail(email)
	switch visibility {
	case fileVisibilityCompany:
		return email != ""
	case fileVisibilityPrivate:
		return email != "" && email == normalizeAccountEmail(metadata["uploaderEmail"])
	case fileVisibilityPeople:
		return fileGrantsAllowEmail(metadata, email)
	}
	return false
}

// fileEntryUploadedByViewer is the canonical-aware uploader check: the held
// person id under a bound tenant principal, the session email otherwise.
func fileEntryUploadedByViewer(ctx context.Context, viewer *userAccount, metadata map[string]string) bool {
	if viewer == nil {
		return false
	}
	if principal, canonical := strideE10TenantPrincipalFromContext(ctx); canonical {
		uploader := strings.TrimSpace(metadata["uploaderPersonId"])
		return uploader != "" && uploader == principal.PersonID
	}
	email := normalizeAccountEmail(viewer.Email)
	return email != "" && email == normalizeAccountEmail(metadata["uploaderEmail"])
}

// authorizeFileEntry is the per-file object authorizer. Read actions follow
// the visibility/grants policy; write and delete stay with the uploader (plus
// the approval admin in legacy mode, matching folders/rename today); share
// (manage access) is the uploader's alone. Never widens: a nil viewer, a
// tenant mismatch, or an unknown visibility is a denial.
func authorizeFileEntry(ctx context.Context, viewer *userAccount, action ACLAction, entry meetingMemoryEntry) bool {
	if viewer == nil || entry.Kind != meetingMemoryKindFile {
		return false
	}
	metadata := entry.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}
	principal, canonical := strideE10TenantPrincipalFromContext(ctx)
	if canonical && strings.TrimSpace(metadata["tenantId"]) != principal.TenantID {
		return false
	}
	uploader := fileEntryUploadedByViewer(ctx, viewer, metadata)
	switch action {
	case ACLReadMetadata, ACLReadContent:
		if uploader {
			return true
		}
		visibility, ok := fileEntryVisibility(metadata)
		if !ok {
			return false
		}
		switch visibility {
		case fileVisibilityCompany:
			return normalizeAccountEmail(viewer.Email) != ""
		case fileVisibilityPeople:
			return fileGrantsAllowEmail(metadata, viewer.Email)
		default:
			return false
		}
	case ACLWrite, ACLDelete:
		if uploader {
			return true
		}
		return !canonical && isArtifactApprovalAdmin(viewer)
	case ACLShare, ACLManage:
		return uploader
	default:
		return false
	}
}

// fileEntryReadableByViewer composes the per-file read ACL with the trash
// state and, for chat-promoted rows, the exact committed source
// reauthorization. A service principal (nil viewer, e.g. shared-room Scout)
// keeps today's contract: live company uploads only.
func (app *kanbanBoardApp) fileEntryReadableByViewer(ctx context.Context, viewer *userAccount, entry meetingMemoryEntry) bool {
	if app == nil || entry.Kind != meetingMemoryKindFile {
		return false
	}
	_, promoted, valid := promotedChatFileBindingFromEntry(entry)
	if viewer == nil {
		visibility, ok := fileEntryVisibility(entry.Metadata)
		return ok && visibility == fileVisibilityCompany && !fileEntryTrashed(entry.Metadata) && !promoted
	}
	if fileEntryTrashed(entry.Metadata) && !fileEntryUploadedByViewer(ctx, viewer, entry.Metadata) {
		return false
	}
	if !authorizeFileEntry(ctx, viewer, ACLReadContent, entry) {
		return false
	}
	if promoted {
		if !valid {
			return false
		}
		if _, _, _, authorized := app.promotedChatFileSource(ctx, viewer, entry); !authorized {
			return false
		}
	}
	return true
}

// assistantFileEntryForViewer resolves one live kind=file row through the
// full read discipline (tenant, trash, promoted source, per-file ACL). Share
// links and other object handlers read Drive rows through this seam only.
func (app *kanbanBoardApp) assistantFileEntryForViewer(ctx context.Context, viewer *userAccount, fileID string) (meetingMemoryEntry, bool) {
	if app == nil || app.memory == nil || viewer == nil {
		return meetingMemoryEntry{}, false
	}
	entry, found := app.memory.entryByKindAndID(meetingMemoryKindFile, strings.TrimSpace(fileID))
	if !found || fileEntryTrashed(entry.Metadata) || !app.fileEntryReadableByViewer(ctx, viewer, entry) {
		return meetingMemoryEntry{}, false
	}
	return entry, true
}

// assistantFileRecord is one row of the Files surface, decorated for the
// client the way decorateArchiveDownloadURLForClient decorates archives.
type assistantFileRecord struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Mime              string `json:"mime,omitempty"`
	Size              int64  `json:"size,omitempty"`
	UploaderName      string `json:"uploaderName,omitempty"`
	UploaderEmail     string `json:"uploaderEmail,omitempty"`
	CreatedAt         string `json:"createdAt,omitempty"`
	Origin            string `json:"origin"`
	OriginThreadID    string `json:"originThreadId,omitempty"`
	OriginThreadTitle string `json:"originThreadTitle,omitempty"`
	BrainStatus       string `json:"brainStatus"`
	DownloadURL       string `json:"downloadUrl,omitempty"`
	Previewable       bool   `json:"previewable,omitempty"`
	// ArtifactID points a deliverable row at its os_artifact so the client
	// opens it in the artifact stage instead of downloading bytes.
	ArtifactID string `json:"artifactId,omitempty"`
	// FolderID files the row under a Files-surface folder (file_folders.go);
	// empty means root.
	FolderID string `json:"folderId,omitempty"`
	// CanDelete is computed from the source's write authority. The client only
	// offers a destructive control when the current principal may remove this
	// Drive projection (or the underlying uploaded/chat file).
	CanDelete bool `json:"canDelete,omitempty"`
	// CanShare is true only for the uploader: visibility and grants are the
	// uploader's alone to change (not even the approval admin may widen them).
	CanShare bool `json:"canShare,omitempty"`
	// Visibility is the per-file ACL of a direct upload: private (uploader
	// only), company (every signed-in member — the legacy default), or people
	// (uploader + Grants). Grants lists the granted account emails.
	Visibility string   `json:"visibility,omitempty"`
	Grants     []string `json:"grants,omitempty"`
	// Starred is per-viewer (metadata starredBy), never shared row state.
	Starred bool `json:"starred,omitempty"`
	// DeletedAt marks a trashed row (only rendered under scope=trash).
	DeletedAt string `json:"deletedAt,omitempty"`
	// VersionOf/Version/Superseded describe a same-name re-upload chain: the
	// newest row is the only one in the default list; older rows carry
	// superseded and are reachable through GET /assistant/files?versionsOf=.
	VersionOf  string `json:"versionOf,omitempty"`
	Version    int    `json:"version,omitempty"`
	Superseded bool   `json:"superseded,omitempty"`
	// ShareLinkCount is the number of LIVE file share links bound to this
	// exact row (versions lane only). A link binds the blob it was minted on,
	// so a superseded version can still be serving a link the Drive list no
	// longer shows — this is how the client can say so.
	ShareLinkCount int `json:"shareLinkCount,omitempty"`
}

// fileBlobDownloadURL builds the session-gated content-addressed download
// route (artifactBlobHandler) for a stored ref.
func fileBlobDownloadURL(ref string, name string) string {
	ref = strings.TrimSpace(ref)
	if !validBlobRef(ref) {
		return ""
	}
	if strings.TrimSpace(name) == "" {
		name = "file"
	}
	return "/artifacts/blob?ref=" + url.QueryEscape(ref) + "&name=" + url.QueryEscape(name)
}

// assistantFileUploadName normalizes a client filename down to a bounded bare
// base name; degenerate names fall back to "file".
func assistantFileUploadName(raw string) string {
	name := filepath.Base(strings.TrimSpace(raw))
	name = strings.Map(func(char rune) rune {
		if char < 0x20 || char == 0x7f {
			return -1
		}
		return char
	}, name)
	if name == "" || name == "." || name == ".." || name == "/" || name == "\\" {
		return "file"
	}
	if runes := []rune(name); len(runes) > assistantFileNameMaxLen {
		name = string(runes[:assistantFileNameMaxLen])
	}
	return name
}

// assistantFileUploadMimeFor resolves the stored mime: the part's declared
// Content-Type first, the filename extension second, octet-stream last. The
// serve route's inline allowlist (blobInlineSafeMimes) — not this value —
// decides render-vs-download, so a lying client can never earn inline HTML.
func assistantFileUploadMimeFor(declared string, name string) string {
	resolved := attachmentUploadMime(declared)
	if resolved == "" || resolved == blobDefaultMime {
		if byExt := attachmentUploadMime(mime.TypeByExtension(filepath.Ext(name))); byExt != "" {
			resolved = byExt
		}
	}
	if resolved == "" {
		resolved = blobDefaultMime
	}
	return resolved
}

// directFilePlainText makes ordinary text uploads immediately recallable
// without spending a model call. The Drive accepts more formats than chat, so
// unsupported binaries still remain safely stored with a pending index dot.
func directFilePlainText(data []byte, fileMime string) string {
	fileMime = strings.ToLower(strings.TrimSpace(fileMime))
	if !strings.HasPrefix(fileMime, "text/") && fileMime != "application/json" && fileMime != "application/xml" {
		return ""
	}
	if len(data) == 0 || !utf8.Valid(data) || bytesContainsNUL(data) {
		return ""
	}
	text := strings.TrimSpace(string(data))
	if len(text) > scoutChatMaxFileTextBytes {
		text = text[:scoutChatMaxFileTextBytes]
		for !utf8.ValidString(text) && len(text) > 0 {
			text = text[:len(text)-1]
		}
		text = strings.TrimSpace(text) + "\n[truncated]"
	}
	return text
}

func bytesContainsNUL(data []byte) bool {
	for _, value := range data {
		if value == 0 {
			return true
		}
	}
	return false
}

// fileRecordFromEntry adapts a kind=file memory entry (direct upload) into
// the client row shape.
func fileRecordFromEntry(entry meetingMemoryEntry) assistantFileRecord {
	metadata := entry.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}
	name := firstNonEmptyString(strings.TrimSpace(metadata["name"]), "file")
	fileMime := strings.TrimSpace(metadata["mime"])
	size, _ := strconv.ParseInt(strings.TrimSpace(metadata["size"]), 10, 64)
	brainStatus := firstNonEmptyString(strings.TrimSpace(metadata["brainStatus"]), fileBrainStatusStored)
	createdAt := ""
	if !entry.CreatedAt.IsZero() {
		createdAt = entry.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	row := assistantFileRecord{
		ID:            entry.ID,
		Name:          name,
		Mime:          fileMime,
		Size:          size,
		UploaderName:  strings.TrimSpace(metadata["uploaderName"]),
		UploaderEmail: strings.TrimSpace(metadata["uploaderEmail"]),
		CreatedAt:     createdAt,
		Origin:        "files",
		BrainStatus:   brainStatus,
		DownloadURL:   fileBlobDownloadURL(metadata["blobRef"], name),
		Previewable:   blobInlineSafeMimes[fileMime],
		DeletedAt:     strings.TrimSpace(metadata["deletedAt"]),
		VersionOf:     strings.TrimSpace(metadata["versionOf"]),
		Version:       fileEntryVersion(metadata),
		Superseded:    fileEntrySuperseded(metadata),
	}
	if visibility, ok := fileEntryVisibility(metadata); ok {
		row.Visibility = visibility
		if visibility == fileVisibilityPeople {
			row.Grants = fileGrantEmails(metadata)
		}
	}
	return row
}

// decorateFileRowForViewer projects a kind=file entry for one viewer: the
// per-viewer star, the write/share affordances, and the canonical-mode email
// redaction the list has always applied.
func (app *kanbanBoardApp) decorateFileRowForViewer(ctx context.Context, viewer *userAccount, entry meetingMemoryEntry) assistantFileRecord {
	row := fileRecordFromEntry(entry)
	if viewer != nil {
		row.Starred = fileEntryStarredBy(entry.Metadata, viewer.Email)
		row.CanDelete = authorizeFileEntry(ctx, viewer, ACLDelete, entry)
		row.CanShare = authorizeFileEntry(ctx, viewer, ACLShare, entry)
	}
	if _, canonical := strideE10TenantPrincipalFromContext(ctx); canonical {
		row.UploaderEmail = ""
	}
	return row
}

/* ---------- "file" socket event projection (review B4) ---------- */

// fileBroadcastRow extracts the decorated Drive row a "file" kanban event
// carries, when it carries one: the bare row (upload, save, studio copy) or
// {kind, file: row} (rename, restore). Events without a row ({kind:"deleted",
// fileId}, {kind:"folders"}, …) return false and fan out unchanged.
func fileBroadcastRow(data any) (assistantFileRecord, string, bool) {
	switch payload := data.(type) {
	case assistantFileRecord:
		return payload, "", payload.ID != ""
	case *assistantFileRecord:
		if payload == nil {
			return assistantFileRecord{}, "", false
		}
		return *payload, "", payload.ID != ""
	case map[string]any:
		kind, _ := payload["kind"].(string)
		switch row := payload["file"].(type) {
		case assistantFileRecord:
			return row, kind, row.ID != ""
		case *assistantFileRecord:
			if row != nil {
				return *row, kind, row.ID != ""
			}
		}
	}
	return assistantFileRecord{}, "", false
}

// fileBroadcastRowReadableByViewer decides whether one signed-in recipient
// may see the row a "file" event carries, through the exact seam each origin's
// list uses: the per-file ACL for a direct upload (private/people rows reach
// only their uploader/grantees), the artifact ACL for a saved deliverable,
// thread visibility for a chat attachment. Unknown origins fail closed.
func (app *kanbanBoardApp) fileBroadcastRowReadableByViewer(ctx context.Context, viewer *userAccount, row assistantFileRecord) bool {
	if app == nil || app.memory == nil || viewer == nil || strings.TrimSpace(row.ID) == "" {
		return false
	}
	switch row.Origin {
	case "files":
		entry, found := app.memory.entryByKindAndID(meetingMemoryKindFile, row.ID)
		return found && app.fileEntryReadableByViewer(ctx, viewer, entry)
	case "deliverable":
		_, ok := app.authorizedArtifactForActions(ctx, viewer, firstNonEmptyString(row.ArtifactID, row.ID), ACLReadContent)
		return ok
	case "chat":
		threadID, _, _, parsed := parseChatAttachmentFileID(row.ID)
		if !parsed {
			return false
		}
		_, _, err := app.scoutChatThreadByID(viewer.Email, threadID)
		return err == nil
	}
	return false
}

// fileBroadcastTombstone is what a non-reader receives instead of the row: the
// event kind and the id only — enough for the client's refetch-on-any-file-
// event, carrying no name, uploader or download URL.
func fileBroadcastTombstone(kind string, row assistantFileRecord) map[string]any {
	return map[string]any{"kind": firstNonEmptyString(strings.TrimSpace(kind), "changed"), "id": row.ID, "fileId": row.ID}
}

// promotedChatFileBinding distinguishes an explicitly promoted chat file from
// a true Files upload. Promoted rows own their Files name/folder lifecycle, but
// their bytes and derived text remain bound to the exact committed chat
// attachment that supplied them. Older promoted rows predate
// sourceAttachmentId; their exact thread/message/index, ref, and revision are
// still a complete source binding.
type promotedChatFileBinding struct {
	SourceFileID       string
	SourceThreadID     string
	SourceMessageID    string
	SourceFileIndex    int
	SourceRevision     string
	SourceAttachmentID string
	BlobRef            string
}

var promotedChatFileAuthorizationMetadataKeys = []string{
	"sourceChatFileId", "sourceThreadId", "sourceMessageId", "sourceFileRevision", "sourceAttachmentId",
	"blobRef", "mime", "size", "tenantId", "visibility", "ownerEmail",
}

func promotedChatFileAuthorizationHeader(entry meetingMemoryEntry) meetingMemoryEntry {
	metadata := make(map[string]string, len(promotedChatFileAuthorizationMetadataKeys))
	for _, key := range promotedChatFileAuthorizationMetadataKeys {
		if value := entry.Metadata[key]; value != "" {
			metadata[key] = value
		}
	}
	return meetingMemoryEntry{ID: entry.ID, Kind: entry.Kind, CreatedAt: entry.CreatedAt, Metadata: metadata}
}

func promotedChatFileAuthorizationHeaderEqual(left meetingMemoryEntry, right meetingMemoryEntry) bool {
	if left.ID != right.ID || left.Kind != right.Kind {
		return false
	}
	for _, key := range promotedChatFileAuthorizationMetadataKeys {
		if left.Metadata[key] != right.Metadata[key] {
			return false
		}
	}
	return true
}

func promotedChatFileBindingFromEntry(entry meetingMemoryEntry) (promotedChatFileBinding, bool, bool) {
	metadata := entry.Metadata
	if metadata == nil {
		return promotedChatFileBinding{}, false, true
	}
	keys := []string{"sourceChatFileId", "sourceThreadId", "sourceMessageId", "sourceFileRevision", "sourceAttachmentId"}
	promoted := false
	for _, key := range keys {
		if strings.TrimSpace(metadata[key]) != "" {
			promoted = true
			break
		}
	}
	if !promoted {
		return promotedChatFileBinding{}, false, true
	}

	sourceFileID := strings.TrimSpace(metadata["sourceChatFileId"])
	threadID, messageID, fileIndex, parsed := parseChatAttachmentFileID(sourceFileID)
	binding := promotedChatFileBinding{
		SourceFileID:       sourceFileID,
		SourceThreadID:     strings.TrimSpace(metadata["sourceThreadId"]),
		SourceMessageID:    strings.TrimSpace(metadata["sourceMessageId"]),
		SourceFileIndex:    fileIndex,
		SourceRevision:     strings.TrimSpace(metadata["sourceFileRevision"]),
		SourceAttachmentID: strings.TrimSpace(metadata["sourceAttachmentId"]),
		BlobRef:            strings.TrimSpace(metadata["blobRef"]),
	}
	valid := parsed && binding.SourceThreadID == threadID && binding.SourceMessageID == messageID &&
		binding.SourceRevision != "" && validBlobRef(binding.BlobRef)
	return binding, true, valid
}

// promotedChatFileSource reauthorizes a promoted Files row against the exact
// current chat message and the committed source-grant store. The committed
// source check also follows an attachment's OriginFileID, so a managed
// artifact-backed PDF/PPTX must still satisfy its current revision,
// publication admission, audience, and export ACL before this promoted row can
// expose it. Direct uploads never enter this path.
func (app *kanbanBoardApp) promotedChatFileSource(ctx context.Context, viewer *userAccount, entry meetingMemoryEntry) (scoutChatThreadRecord, scoutChatFileAttachment, promotedChatFileBinding, bool) {
	binding, promoted, valid := promotedChatFileBindingFromEntry(entry)
	if app == nil || app.memory == nil || viewer == nil || !promoted || !valid {
		return scoutChatThreadRecord{}, scoutChatFileAttachment{}, binding, false
	}

	var thread scoutChatThreadRecord
	principal, canonical := strideE10TenantPrincipalFromContext(ctx)
	if canonical {
		threadEntry, found := app.memory.entryByKindAndID(meetingMemoryKindScoutChat, binding.SourceThreadID)
		if !found || !scoutChatThreadMetadataAllowsPrincipal(threadEntry.Metadata, principal) {
			return scoutChatThreadRecord{}, scoutChatFileAttachment{}, binding, false
		}
		var decoded bool
		thread, decoded = decodeScoutChatThreadEntry(threadEntry)
		if !decoded {
			return scoutChatThreadRecord{}, scoutChatFileAttachment{}, binding, false
		}
	} else {
		var err error
		thread, _, err = app.scoutChatThreadByID(viewer.Email, binding.SourceThreadID)
		if err != nil {
			return scoutChatThreadRecord{}, scoutChatFileAttachment{}, binding, false
		}
	}

	messageIndex := scoutChatMessageIndex(thread, binding.SourceMessageID)
	if messageIndex < 0 || binding.SourceFileIndex >= len(thread.Messages[messageIndex].Files) {
		return scoutChatThreadRecord{}, scoutChatFileAttachment{}, binding, false
	}
	source := thread.Messages[messageIndex].Files[binding.SourceFileIndex]
	if strings.TrimSpace(source.Ref) != binding.BlobRef || strings.TrimSpace(source.SourceRevision) != binding.SourceRevision ||
		(binding.SourceAttachmentID != "" && strings.TrimSpace(source.SourceID) != binding.SourceAttachmentID) {
		return scoutChatThreadRecord{}, scoutChatFileAttachment{}, binding, false
	}
	if storedMime := strings.ToLower(strings.TrimSpace(entry.Metadata["mime"])); storedMime != "" && storedMime != strings.ToLower(strings.TrimSpace(source.Mime)) {
		return scoutChatThreadRecord{}, scoutChatFileAttachment{}, binding, false
	}
	if storedSize := strings.TrimSpace(entry.Metadata["size"]); storedSize != "" {
		size, err := strconv.ParseInt(storedSize, 10, 64)
		if err != nil || size != source.Size {
			return scoutChatThreadRecord{}, scoutChatFileAttachment{}, binding, false
		}
	}

	authorized := app.committedChatAttachmentAuthorized(viewer.Email, binding.SourceThreadID, binding.SourceMessageID, source)
	if canonical {
		authorized = app.committedChatAttachmentAuthorizedForPrincipal(ctx, principal, thread, binding.SourceMessageID, source)
	}
	if !authorized {
		return scoutChatThreadRecord{}, scoutChatFileAttachment{}, binding, false
	}
	return thread, source, binding, true
}

// fileRecordsFromThread adapts one chat thread's persisted attachments (085's
// scoutChatFileAttachment records) into rows. Only files with durable bytes
// (Ref) or ingested text qualify — a pre-085 name-only chip has nothing to
// list. Derived/extracted Text riding model context IS the brain, so it sets
// the badge.
func (app *kanbanBoardApp) fileRecordsFromThread(viewerEmail string, thread scoutChatThreadRecord) []assistantFileRecord {
	thread = app.projectScoutChatThreadForViewer(viewerEmail, thread)
	var rows []assistantFileRecord
	// A public-channel attachment's derived text is company-visible recall; a
	// private thread's text stays scoped to that 1:1, so its badge is honest
	// about never reaching company recall (card-103 folded fix).
	threadPrivate := scoutChatThreadVisibility(thread) == scoutChatVisibilityPrivate
	for _, message := range thread.Messages {
		for index, file := range message.Files {
			ref := strings.TrimSpace(file.Ref)
			hasText := strings.TrimSpace(file.Text) != ""
			if ref == "" && !hasText {
				continue
			}
			name := firstNonEmptyString(strings.TrimSpace(file.Name), "file")
			brainStatus := fileBrainStatusStored
			if hasText {
				brainStatus = fileBrainStatusIngested
				if threadPrivate {
					brainStatus = fileBrainStatusThread
				}
			}
			fileMime := strings.TrimSpace(file.Mime)
			rows = append(rows, assistantFileRecord{
				ID:                fmt.Sprintf("%s:%s:%d", thread.ID, message.ID, index),
				Name:              name,
				Mime:              fileMime,
				Size:              file.Size,
				UploaderName:      strings.TrimSpace(message.AuthorName),
				UploaderEmail:     strings.TrimSpace(message.AuthorEmail),
				CreatedAt:         strings.TrimSpace(message.CreatedAt),
				Origin:            "chat",
				OriginThreadID:    thread.ID,
				OriginThreadTitle: strings.TrimSpace(thread.Title),
				BrainStatus:       brainStatus,
				DownloadURL:       fileBlobDownloadURL(ref, name),
				Previewable:       blobInlineSafeMimes[fileMime],
			})
		}
	}
	return rows
}

// deliverableRecordQualifies reports whether an os_artifact entry is a real,
// terminal, non-UI-state deliverable — the provenance/status/kind checks that
// PREDATE the explicit-save gate. Provenance must be an agent-thread run
// (source scout_thread — including goal writer children) or the goal engine's
// own stamps (goalPlan on the parent, goalDeliverable on a flagged child), or
// an explicitly approved Packaging Studio ship contract; the status must be
// terminally good (complete/published, or approved for a studio ship — running
// scaffolds and error/needs_attention bodies never qualify); and UI-state-ish artifacts
// (taste profiles, the house-style doc, quarantined entries) stay out. The
// chat_image renders are also terminal deliverables: they carry source
// chat_image and an image asset that the Files row can download/preview. The
// grandfather migration stamps exactly the entries that pass this, and
// fileDeliverableRecord layers the savedToFiles gate on top.
func deliverableRecordQualifies(entry meetingMemoryEntry) bool {
	metadata := entry.Metadata
	if metadata == nil {
		return false
	}
	source := strings.TrimSpace(metadata["source"])
	studioShip := source == "packaging_studio_ship" && strings.TrimSpace(metadata["goalId"]) != "" && strings.TrimSpace(metadata["artifactContract"]) != ""
	// Studio-native blank creates (Document/Deck Studio) are terminal Work
	// results with a server-minted identity; they save the same way a finished
	// thread deliverable does.
	studioNative := oneOf(source, studioBlankSourceDocument, studioBlankSourceDeck) && strings.TrimSpace(metadata["threadId"]) == ""
	if source != "scout_thread" &&
		source != "chat_image" && !studioShip && !studioNative &&
		strings.TrimSpace(metadata["goalPlan"]) == "" &&
		!strings.EqualFold(strings.TrimSpace(metadata["goalDeliverable"]), "true") {
		return false
	}
	if strings.TrimSpace(metadata[tasteProfileArtifactTypeKey]) != "" {
		return false
	}
	if memoryEntryHiddenFromRecall(entry) {
		return false
	}
	switch agentThreadStatusValue(entry) {
	case artifactStatusComplete, artifactStatusPublished:
		return true
	case artifactStatusApproved:
		return studioShip
	default:
		return false
	}
}

// fileDeliverableRecord adapts a finished agent work product (os_artifact)
// into a Files row. Only real deliverables qualify (deliverableRecordQualifies)
// AND only once explicitly saved (the savedToFiles gate below). The row carries
// ArtifactID so the client opens the artifact stage instead of downloading
// bytes.
func fileDeliverableRecord(entry meetingMemoryEntry) (assistantFileRecord, bool) {
	if !deliverableRecordQualifies(entry) {
		return assistantFileRecord{}, false
	}
	metadata := entry.Metadata
	// Explicit-save gate (kanban-card-110): a qualifying deliverable is only a
	// Files-surface row once a user (or Scout on the user's behalf, via
	// /assistant/files/save or save_to_files) has explicitly saved it. Existing
	// prod content is preserved by grandfatherSavedToFilesAtBoot, which stamps
	// every entry that passed the PRE-gate rules once at startup.
	if !strings.EqualFold(strings.TrimSpace(metadata["savedToFiles"]), "true") {
		return assistantFileRecord{}, false
	}

	name := firstNonEmptyString(strings.TrimSpace(metadata["driveFileName"]), strings.TrimSpace(metadata["title"]), strings.TrimSpace(metadata["threadQuery"]), "Deliverable")
	deliverableMime := "text/markdown"
	if artifactType(entry) == artifactTypeHTMLDeck {
		deliverableMime = "text/html"
	}
	var workbookAsset *artifactAsset
	if artifactType(entry) == artifactTypeWorkbook {
		assets := artifactAssets(entry)
		for index := range assets {
			asset := assets[index]
			if asset.Kind == "export" && strings.EqualFold(strings.TrimSpace(asset.Mime), ventureWorkbookMime) && validBlobRef(asset.Ref) {
				workbookAsset = &asset
				break
			}
		}
		if workbookAsset == nil {
			return assistantFileRecord{}, false
		}
		name = firstNonEmptyString(strings.TrimSpace(metadata["driveFileName"]), strings.TrimSpace(workbookAsset.Name), name)
		deliverableMime = ventureWorkbookMime
	}
	var imageAsset *artifactAsset
	if strings.TrimSpace(metadata["source"]) == "chat_image" || artifactType(entry) == artifactTypeImage {
		assets := artifactAssets(entry)
		for index := range assets {
			if assets[index].Kind == "image" && validBlobRef(assets[index].Ref) {
				asset := assets[index]
				imageAsset = &asset
				break
			}
		}
		if imageAsset != nil {
			name = firstNonEmptyString(strings.TrimSpace(metadata["driveFileName"]), strings.TrimSpace(imageAsset.Name), name)
			deliverableMime = firstNonEmptyString(strings.TrimSpace(imageAsset.Mime), "image/png")
		}
	}
	// Generic file results (pdf/bundle/file) hand over their exact bytes the
	// way a workbook does: the first pdf/export asset, else the first asset.
	var fileAsset *artifactAsset
	if oneOf(artifactType(entry), artifactTypePDF, artifactTypeBundle, artifactTypeFile) {
		assets := artifactAssets(entry)
		for _, preferred := range []bool{true, false} {
			for index := range assets {
				if !validBlobRef(assets[index].Ref) || artifactAssetIsPageImage(assets[index]) {
					continue
				}
				if preferred && !oneOf(assets[index].Kind, "pdf", "export") {
					continue
				}
				asset := assets[index]
				fileAsset = &asset
				break
			}
			if fileAsset != nil {
				break
			}
		}
		if fileAsset != nil {
			name = firstNonEmptyString(strings.TrimSpace(metadata["driveFileName"]), strings.TrimSpace(fileAsset.Name), name)
			deliverableMime = firstNonEmptyString(strings.TrimSpace(fileAsset.Mime), "application/octet-stream")
		}
	}
	createdAt := ""
	if !entry.CreatedAt.IsZero() {
		createdAt = entry.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	row := assistantFileRecord{
		ID:           entry.ID,
		ArtifactID:   entry.ID,
		Name:         name,
		Mime:         deliverableMime,
		UploaderName: firstNonEmptyString(strings.TrimSpace(metadata["updatedBy"]), strings.TrimSpace(metadata["createdBy"])),
		CreatedAt:    createdAt,
		Origin:       "deliverable",
		// The artifact body IS meeting memory — deliverables always feed the
		// brain.
		BrainStatus: fileBrainStatusIngested,
		Previewable: true,
	}
	if imageAsset != nil {
		row.DownloadURL = fileBlobDownloadURL(imageAsset.Ref, name)
		row.Previewable = blobInlineSafeMimes[deliverableMime]
	}
	if workbookAsset != nil {
		row.DownloadURL = fileBlobDownloadURL(workbookAsset.Ref, name)
		row.Previewable = false
	}
	if fileAsset != nil {
		row.DownloadURL = fileBlobDownloadURL(fileAsset.Ref, name)
		row.Previewable = blobInlineSafeMimes[deliverableMime]
	}
	return row, true
}

// assistantFilesForUser assembles the viewer's file list: every direct upload
// (including chat attachments explicitly promoted to Drive) plus the finished
// agent deliverables. Ordinary chat attachments remain in their conversation
// and never appear here implicitly. Newest first, capped after the merge.
func (app *kanbanBoardApp) assistantFilesForUser(viewerEmail string) []assistantFileRecord {
	return app.assistantFilesForPrincipal(context.Background(), &userAccount{Email: normalizeAccountEmail(viewerEmail)})
}

func (app *kanbanBoardApp) assistantFilesForPrincipal(ctx context.Context, viewer *userAccount) []assistantFileRecord {
	return app.assistantFileRowsForPrincipal(ctx, viewer, assistantFileListScope{})
}

// assistantTrashedFilesForPrincipal lists the caller's own trashed uploads
// (plus, in legacy mode, the approval admin's view of every trashed row).
func (app *kanbanBoardApp) assistantTrashedFilesForPrincipal(ctx context.Context, viewer *userAccount) []assistantFileRecord {
	return app.assistantFileRowsForPrincipal(ctx, viewer, assistantFileListScope{trash: true})
}

// assistantFileListScope selects which kind=file rows a list projects: the
// live default (not trashed, newest of each version chain), the trash, or the
// live set including superseded versions (the versionsOf lane).
type assistantFileListScope struct {
	trash             bool
	includeSuperseded bool
	// includeTrashed projects live AND trashed rows together (the versions
	// walk); a trashed row still surfaces only to whoever may read it there —
	// its uploader — because fileEntryReadableByViewer enforces that.
	includeTrashed bool
}

func (app *kanbanBoardApp) assistantFileRowsForPrincipal(ctx context.Context, viewer *userAccount, scope assistantFileListScope) []assistantFileRecord {
	if app == nil || app.memory == nil {
		return nil
	}
	rows := make([]assistantFileRecord, 0, 32)
	principal, canonical := strideE10TenantPrincipalFromContext(ctx)
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindFile, 0) {
		if canonical && strings.TrimSpace(entry.Metadata["tenantId"]) != principal.TenantID {
			continue
		}
		if !scope.includeTrashed && fileEntryTrashed(entry.Metadata) != scope.trash {
			continue
		}
		if !scope.includeSuperseded && fileEntrySuperseded(entry.Metadata) {
			continue
		}
		// The trash is the owner's: only whoever may delete the row sees it there.
		if scope.trash && !authorizeFileEntry(ctx, viewer, ACLDelete, entry) {
			continue
		}
		if !app.fileEntryReadableByViewer(ctx, viewer, entry) {
			continue
		}
		rows = append(rows, app.decorateFileRowForViewer(ctx, viewer, entry))
	}
	if !scope.trash {
		for _, entry := range app.authorizedFileDeliverableCandidates(ctx, viewer, ACLReadContent) {
			if row, ok := fileDeliverableRecord(entry); ok {
				_, row.CanDelete = authorizedArtifactForActions(ctx, viewer, entry.ID, ACLReadContent, ACLWrite)
				rows = append(rows, row)
			}
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return fileRecordTime(rows[i]).After(fileRecordTime(rows[j]))
	})
	if len(rows) > assistantFilesListLimit {
		rows = rows[:assistantFilesListLimit]
	}
	return rows
}

// assistantFileVersionsForPrincipal returns the whole same-name version chain
// that contains fileID, newest first, restricted to rows the viewer may read.
// Chain EDGES come from every same-tenant row's body-free versionOf id —
// trashed or not — so a trashed middle version still bridges its neighbours in
// both directions; the trashed row itself is projected only for its uploader.
// An anchor the viewer cannot read yields nothing (no chain oracle).
func (app *kanbanBoardApp) assistantFileVersionsForPrincipal(ctx context.Context, viewer *userAccount, fileID string) []assistantFileRecord {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" || app == nil || app.memory == nil {
		return nil
	}
	// One walk of the Drive: chain edges from every same-tenant row, the
	// viewer's projection only for the rows they may read.
	principal, canonical := strideE10TenantPrincipalFromContext(ctx)
	visible := map[string]assistantFileRecord{}
	parents := map[string]string{}
	children := map[string][]string{}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindFile, 0) {
		if canonical && strings.TrimSpace(entry.Metadata["tenantId"]) != principal.TenantID {
			continue
		}
		if parent := strings.TrimSpace(entry.Metadata["versionOf"]); parent != "" {
			parents[entry.ID] = parent
			children[parent] = append(children[parent], entry.ID)
		}
		if app.fileEntryReadableByViewer(ctx, viewer, entry) {
			visible[entry.ID] = app.decorateFileRowForViewer(ctx, viewer, entry)
		}
	}
	if _, ok := visible[fileID]; !ok {
		return nil
	}
	root := fileID
	visited := map[string]bool{root: true}
	for {
		parent, found := parents[root]
		if !found || parent == "" || visited[parent] {
			break
		}
		visited[parent] = true
		root = parent
	}
	chain := make([]assistantFileRecord, 0, 4)
	collected := map[string]bool{root: true}
	if row, ok := visible[root]; ok {
		chain = append(chain, row)
	}
	for frontier := []string{root}; len(frontier) > 0; {
		var next []string
		for _, id := range frontier {
			for _, child := range children[id] {
				if collected[child] {
					continue
				}
				collected[child] = true
				if row, ok := visible[child]; ok {
					chain = append(chain, row)
				}
				next = append(next, child)
			}
		}
		frontier = next
	}
	sort.SliceStable(chain, func(i, j int) bool {
		if chain[i].Version != chain[j].Version {
			return chain[i].Version > chain[j].Version
		}
		return fileRecordTime(chain[i]).After(fileRecordTime(chain[j]))
	})
	// A file share link binds the exact blob it was minted on (share_links.go
	// mintFileShareLink), so a superseded version hidden from the Drive list
	// can still be serving one. Surface the live count per version so the
	// client can say so. One side-store read; never an error for the lane.
	if links, err := loadShareLinks(); err == nil && len(links) > 0 {
		now := time.Now().UTC()
		counts := make(map[string]int, len(chain))
		for _, link := range links {
			if link.ObjectType == shareLinkObjectTypeFile && shareLinkLive(link, now) {
				counts[strings.TrimSpace(link.FileID)]++
			}
		}
		for index := range chain {
			chain[index].ShareLinkCount = counts[chain[index].ID]
		}
	}
	return chain
}

/* ---------- version chain re-heading (Drive review D2) ---------- */

// fileVersionChainRows returns every kind=file row connected to anchorID
// through versionOf edges, trashed or not (a trashed middle version still
// bridges its neighbours), keyed by id. Edges are explicit ids, so a chain
// never crosses tenants by construction.
func (app *kanbanBoardApp) fileVersionChainRows(anchorID string) map[string]meetingMemoryEntry {
	anchorID = strings.TrimSpace(anchorID)
	if app == nil || app.memory == nil || anchorID == "" {
		return nil
	}
	byID := map[string]meetingMemoryEntry{}
	neighbours := map[string][]string{}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindFile, 0) {
		byID[entry.ID] = entry
		if parent := strings.TrimSpace(entry.Metadata["versionOf"]); parent != "" {
			neighbours[entry.ID] = append(neighbours[entry.ID], parent)
			neighbours[parent] = append(neighbours[parent], entry.ID)
		}
	}
	if _, ok := byID[anchorID]; !ok {
		return nil
	}
	component := map[string]meetingMemoryEntry{}
	for frontier := []string{anchorID}; len(frontier) > 0; {
		var next []string
		for _, id := range frontier {
			if _, seen := component[id]; seen {
				continue
			}
			entry, ok := byID[id]
			if !ok {
				continue
			}
			component[id] = entry
			next = append(next, neighbours[id]...)
		}
		frontier = next
	}
	return component
}

// fileVersionChainMaxVersion is the highest version number anywhere in the
// chain containing prior — trashed and superseded rows included — so a fresh
// upload always continues the count instead of reusing a trashed version's
// number.
func (app *kanbanBoardApp) fileVersionChainMaxVersion(prior meetingMemoryEntry) int {
	maxVersion := fileEntryVersion(prior.Metadata)
	for _, entry := range app.fileVersionChainRows(prior.ID) {
		if version := fileEntryVersion(entry.Metadata); version > maxVersion {
			maxVersion = version
		}
	}
	return maxVersion
}

// reconcileFileVersionChain re-heads the chain containing fileID after a
// trash, restore or purge. Among the chain's untrashed rows the highest
// version (newest on a tie) is the live head — its superseded/supersededBy
// stamps cleared so it returns to the default list and to
// priorFileVersionForUpload — and every other untrashed row is stamped
// superseded by that head. Trashed rows keep their stamps untouched (a
// restore reconciles again); versionOf/version never change. Only rows whose
// stamps actually differ are rewritten.
func (app *kanbanBoardApp) reconcileFileVersionChain(fileID string) error {
	component := app.fileVersionChainRows(fileID)
	if len(component) == 0 {
		return nil
	}
	var head meetingMemoryEntry
	hasHead := false
	for _, entry := range component {
		if fileEntryTrashed(entry.Metadata) {
			continue
		}
		if !hasHead || fileEntryVersion(entry.Metadata) > fileEntryVersion(head.Metadata) ||
			(fileEntryVersion(entry.Metadata) == fileEntryVersion(head.Metadata) && entry.CreatedAt.After(head.CreatedAt)) {
			head = entry
			hasHead = true
		}
	}
	if !hasHead {
		return nil
	}
	for _, entry := range component {
		if fileEntryTrashed(entry.Metadata) {
			continue
		}
		want := map[string]string{"superseded": "", "supersededBy": ""}
		if entry.ID != head.ID {
			want = map[string]string{"superseded": "true", "supersededBy": head.ID}
		}
		if strings.TrimSpace(entry.Metadata["superseded"]) == want["superseded"] && strings.TrimSpace(entry.Metadata["supersededBy"]) == want["supersededBy"] {
			continue
		}
		if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindFile, entry.ID, entry.Text, want); err != nil {
			return fmt.Errorf("re-head Drive version chain at %s: %w", entry.ID, err)
		}
	}
	return nil
}

// priorFileVersionForUpload finds the live row a fresh upload supersedes: the
// same name, in the same folder, by the same uploader (same tenant). Chat-
// promoted rows are source-bound and never chain onto a direct upload.
func (app *kanbanBoardApp) priorFileVersionForUpload(ctx context.Context, viewer *userAccount, name string, folderID string) (meetingMemoryEntry, bool) {
	if app == nil || app.memory == nil || viewer == nil || strings.TrimSpace(name) == "" {
		return meetingMemoryEntry{}, false
	}
	folderID = strings.TrimSpace(folderID)
	_, assignments := sharedFileFolderStore().snapshot()
	principal, canonical := strideE10TenantPrincipalFromContext(ctx)
	var newest meetingMemoryEntry
	found := false
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindFile, 0) {
		if canonical && strings.TrimSpace(entry.Metadata["tenantId"]) != principal.TenantID {
			continue
		}
		if strings.TrimSpace(entry.Metadata["name"]) != name || fileEntryTrashed(entry.Metadata) || fileEntrySuperseded(entry.Metadata) {
			continue
		}
		if !fileEntryUploadedByViewer(ctx, viewer, entry.Metadata) {
			continue
		}
		if _, promoted, _ := promotedChatFileBindingFromEntry(entry); promoted {
			continue
		}
		if strings.TrimSpace(assignments[entry.ID]) != folderID {
			continue
		}
		if !found || entry.CreatedAt.After(newest.CreatedAt) {
			newest = entry
			found = true
		}
	}
	return newest, found
}

// searchAssistantFilesForPrincipal narrows an already ACL-scoped row list to
// the rows whose name or uploader contains the query, OR whose ingested body
// text matches through the same principal-scoped memory search Scout's file
// context uses (recallStoreForPrincipal → search). A row the viewer cannot
// read is never in the input list, so body matches can never leak a name.
func (app *kanbanBoardApp) searchAssistantFilesForPrincipal(ctx context.Context, viewer *userAccount, rows []assistantFileRecord, query string) []assistantFileRecord {
	query = strings.TrimSpace(query)
	if query == "" || app == nil || app.memory == nil {
		return rows
	}
	needle := strings.ToLower(query)
	matched := map[string]bool{}
	if viewer != nil {
		scoped := app.recallStoreForPrincipal(ctx, recallPrincipalForUser(viewer))
		for _, match := range scoped.search(query, assistantFilesListLimit) {
			if match.Entry.Kind == meetingMemoryKindFile || match.Entry.Kind == meetingMemoryKindOSArtifact {
				matched[match.Entry.ID] = true
			}
		}
	}
	out := make([]assistantFileRecord, 0, len(rows))
	for _, row := range rows {
		if strings.Contains(strings.ToLower(row.Name), needle) ||
			strings.Contains(strings.ToLower(row.UploaderName), needle) ||
			strings.Contains(strings.ToLower(row.UploaderEmail), needle) ||
			matched[row.ID] || (row.ArtifactID != "" && matched[row.ArtifactID]) {
			out = append(out, row)
		}
	}
	return out
}

// assistantFileAttachmentSource resolves one Drive row through its native ACL
// and returns only a content-addressed attachment handle. The client never
// receives a document body from this seam. Deliverables without a rendered
// binary are materialized as immutable Markdown blobs so an exact report can
// be attached and summarized while its PDF is still rendering.
func (app *kanbanBoardApp) assistantFileAttachmentSource(ctx context.Context, viewer *userAccount, fileID string) (scoutChatFileAttachment, blobMeta, string, bool) {
	if app == nil || app.memory == nil || viewer == nil {
		return scoutChatFileAttachment{}, blobMeta{}, "", false
	}
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return scoutChatFileAttachment{}, blobMeta{}, "", false
	}

	if _, found := app.memory.artifactAuthorizationHeaderByID(fileID); found {
		artifact, allowed := authorizedArtifactForActions(ctx, viewer, fileID, ACLReadContent)
		if !allowed {
			return scoutChatFileAttachment{}, blobMeta{}, "", false
		}
		if strings.TrimSpace(firstNonEmptyString(artifact.Metadata["goalId"], artifact.Metadata["goalParentId"])) != "" &&
			!app.authoredResultPublicationReady(artifact) {
			// A saved working draft remains openable in Files and editable in its
			// Studio, but it is not silently flattened into an unlabeled chat
			// attachment. Review is the explicit publication boundary.
			return scoutChatFileAttachment{}, blobMeta{}, "", false
		}
		row, visible := fileDeliverableRecord(artifact)
		if !visible {
			return scoutChatFileAttachment{}, blobMeta{}, "", false
		}
		finalExportChecked := false
		finalExportAllowed := false
		allowFinalExportAsset := func() bool {
			if finalExportChecked {
				return finalExportAllowed
			}
			finalExportChecked = true
			exact, allowed := authorizedArtifactForActions(ctx, viewer, fileID, ACLReadContent, ACLExport)
			if !allowed || !artifactAuthorizationHeaderEqual(
				resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact)),
				resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(exact)),
			) {
				return false
			}
			finalExportAllowed = app.authoredResultPublicationReady(exact)
			return finalExportAllowed
		}
		ref := ""
		for _, preferredKind := range []string{"pdf", "image", "export"} {
			for _, asset := range artifactAssets(artifact) {
				if strings.EqualFold(strings.TrimSpace(asset.Kind), preferredKind) && validBlobRef(asset.Ref) &&
					(!artifactAssetRefRequiresFinalExportAdmission(asset) || allowFinalExportAsset()) {
					ref = asset.Ref
					if strings.TrimSpace(asset.Name) != "" && preferredKind != "pdf" {
						row.Name = strings.TrimSpace(asset.Name)
					}
					break
				}
			}
			if ref != "" {
				break
			}
		}
		if ref == "" {
			if strings.TrimSpace(artifact.Text) == "" {
				return scoutChatFileAttachment{}, blobMeta{}, "", false
			}
			var err error
			ref, err = putBlob([]byte(artifact.Text), "text/markdown")
			if err != nil {
				return scoutChatFileAttachment{}, blobMeta{}, "", false
			}
		}
		meta, err := blobStatForRef(ref)
		if err != nil {
			return scoutChatFileAttachment{}, blobMeta{}, "", false
		}
		header := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact))
		revision, err := STRIDEContractDigest(struct {
			FileID   string                      `json:"fileId"`
			Artifact ArtifactAuthorizationHeader `json:"artifact"`
			Ref      string                      `json:"ref"`
			Mime     string                      `json:"mime"`
			Size     int64                       `json:"size"`
		}{fileID, header, ref, strings.ToLower(strings.TrimSpace(meta.Mime)), meta.Size})
		if err != nil {
			return scoutChatFileAttachment{}, blobMeta{}, "", false
		}
		return assistantFileAttachment(row.Name, ref, meta), meta, revision, true
	}

	if entry, found := app.memory.entryByKindAndID(meetingMemoryKindFile, fileID); found {
		// Per-file ACL first (D1): a trashed row or one outside the viewer's
		// visibility/grants never yields an attachment handle.
		if fileEntryTrashed(entry.Metadata) || !app.fileEntryReadableByViewer(ctx, viewer, entry) {
			return scoutChatFileAttachment{}, blobMeta{}, "", false
		}
		row := fileRecordFromEntry(entry)
		ref := strings.TrimSpace(entry.Metadata["blobRef"])
		var promotedThread scoutChatThreadRecord
		var promotedSource scoutChatFileAttachment
		var promotedBinding promotedChatFileBinding
		if _, promoted, valid := promotedChatFileBindingFromEntry(entry); promoted {
			if !valid {
				return scoutChatFileAttachment{}, blobMeta{}, "", false
			}
			var authorized bool
			promotedThread, promotedSource, promotedBinding, authorized = app.promotedChatFileSource(ctx, viewer, entry)
			if !authorized {
				return scoutChatFileAttachment{}, blobMeta{}, "", false
			}
		}
		meta, err := blobStatForRef(ref)
		if err != nil {
			return scoutChatFileAttachment{}, blobMeta{}, "", false
		}
		var revision string
		if promotedBinding.SourceFileID != "" {
			revision, err = STRIDEContractDigest(struct {
				FileID              string `json:"fileId"`
				Ref                 string `json:"ref"`
				Mime                string `json:"mime"`
				Size                int64  `json:"size"`
				SourceFileID        string `json:"sourceFileId"`
				SourceID            string `json:"sourceId"`
				SourceRevision      string `json:"sourceRevision"`
				DestinationRevision string `json:"destinationRevision"`
			}{fileID, ref, strings.ToLower(strings.TrimSpace(meta.Mime)), meta.Size, promotedBinding.SourceFileID, strings.TrimSpace(promotedSource.SourceID), promotedBinding.SourceRevision, scoutChatAttachmentDestinationRevision(promotedThread)})
		} else {
			revision, err = STRIDEContractDigest(struct {
				FileID string `json:"fileId"`
				Ref    string `json:"ref"`
				Mime   string `json:"mime"`
				Size   int64  `json:"size"`
			}{fileID, ref, strings.ToLower(strings.TrimSpace(meta.Mime)), meta.Size})
		}
		if err != nil {
			return scoutChatFileAttachment{}, blobMeta{}, "", false
		}
		return assistantFileAttachment(row.Name, ref, meta), meta, revision, true
	}

	threadID, messageID, fileIndex, parsed := parseChatAttachmentFileID(fileID)
	if !parsed {
		return scoutChatFileAttachment{}, blobMeta{}, "", false
	}
	thread, _, err := app.scoutChatThreadByID(viewer.Email, threadID)
	if err != nil {
		return scoutChatFileAttachment{}, blobMeta{}, "", false
	}
	for _, message := range thread.Messages {
		if message.ID != messageID || fileIndex >= len(message.Files) {
			continue
		}
		source := message.Files[fileIndex]
		if !app.committedChatAttachmentAuthorized(viewer.Email, thread.ID, message.ID, source) {
			return scoutChatFileAttachment{}, blobMeta{}, "", false
		}
		ref := strings.TrimSpace(source.Ref)
		if ref == "" && strings.TrimSpace(source.Text) != "" {
			ref, err = putBlob([]byte(source.Text), "text/plain")
			if err != nil {
				return scoutChatFileAttachment{}, blobMeta{}, "", false
			}
		}
		meta, statErr := blobStatForRef(ref)
		if statErr != nil {
			return scoutChatFileAttachment{}, blobMeta{}, "", false
		}
		revision, digestErr := STRIDEContractDigest(struct {
			FileID              string `json:"fileId"`
			Ref                 string `json:"ref"`
			Mime                string `json:"mime"`
			Size                int64  `json:"size"`
			SourceID            string `json:"sourceId,omitempty"`
			SourceRevision      string `json:"sourceRevision,omitempty"`
			DestinationRevision string `json:"destinationRevision"`
		}{fileID, ref, strings.ToLower(strings.TrimSpace(meta.Mime)), meta.Size, strings.TrimSpace(source.SourceID), strings.TrimSpace(source.SourceRevision), scoutChatAttachmentDestinationRevision(thread)})
		if digestErr != nil {
			return scoutChatFileAttachment{}, blobMeta{}, "", false
		}
		return assistantFileAttachment(firstNonEmptyString(source.Name, "file"), ref, meta), meta, revision, true
	}
	return scoutChatFileAttachment{}, blobMeta{}, "", false
}

func assistantFileAttachment(name, ref string, meta blobMeta) scoutChatFileAttachment {
	name = firstNonEmptyString(strings.TrimSpace(name), "file")
	return scoutChatFileAttachment{
		Name: name,
		Kind: strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), "."),
		Size: meta.Size,
		Ref:  strings.TrimSpace(ref),
		Mime: strings.ToLower(strings.TrimSpace(meta.Mime)),
	}
}

func (app *kanbanBoardApp) assistantFileSourceRevision(ctx context.Context, viewer *userAccount, fileID string) (string, bool) {
	_, _, revision, ok := app.assistantFileAttachmentSource(ctx, viewer, fileID)
	return revision, ok
}

// assistantFileSourceAllowsDestination is the audience-intersection gate for
// Drive-to-chat attachments. Organization-visible sources may be narrowed into
// any thread. A private source may only stay inside a private thread owned by
// the same person; it can never be widened into a company channel merely
// because its owner can post there.
func (app *kanbanBoardApp) assistantFileSourceAllowsDestination(ctx context.Context, viewer *userAccount, fileID string, destination scoutChatThreadRecord) bool {
	if app == nil || app.memory == nil || viewer == nil {
		return false
	}
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return false
	}
	destinationPrivate := scoutChatThreadVisibility(destination) == scoutChatVisibilityPrivate
	destinationOwner := normalizeAccountEmail(destination.OwnerEmail)
	viewerEmail := normalizeAccountEmail(viewer.Email)

	if header, found := app.memory.artifactAuthorizationHeaderByID(fileID); found {
		header = resolveArtifactHeaderOwner(header)
		if !artifactHeaderAuthorized(ctx, viewer, ACLReadContent, header) {
			return false
		}
		if legacyArtifactHeaderOrganizationVisible(header) {
			return true
		}
		return destinationPrivate && destinationOwner == viewerEmail && normalizeAccountEmail(header.OwnerEmail) == viewerEmail
	}
	// Direct uploads narrow by their per-file visibility: company rows may go
	// anywhere, a private row only into its uploader's own private thread, a
	// people row only where every destination reader holds a grant. A promoted
	// chat row additionally preserves its source audience and cannot widen a
	// private/project attachment merely because it now has a Files name.
	if entry, found := app.memory.entryByKindAndID(meetingMemoryKindFile, fileID); found {
		if fileEntryTrashed(entry.Metadata) || !app.fileEntryReadableByViewer(ctx, viewer, entry) {
			return false
		}
		if binding, promoted, _ := promotedChatFileBindingFromEntry(entry); promoted {
			if !app.assistantFileSourceAllowsDestination(ctx, viewer, binding.SourceFileID, destination) {
				return false
			}
		}
		return fileEntryAudienceAllowsDestination(entry.Metadata, viewerEmail, destination)
	}

	threadID, _, _, parsed := parseChatAttachmentFileID(fileID)
	if !parsed {
		return false
	}
	source, _, err := app.scoutChatThreadByID(viewer.Email, threadID)
	if err != nil {
		return false
	}
	if scoutChatThreadIsOrganizationPublic(source) {
		return true
	}
	if scoutChatThreadVisibility(source) == scoutChatVisibilityPrivate {
		return destinationPrivate && destinationOwner == viewerEmail && normalizeAccountEmail(source.OwnerEmail) == viewerEmail
	}
	// A project channel is public only to its explicit member set. The
	// destination audience must be a subset of that set; a company-wide
	// channel can never receive a project-restricted source.
	sourceMembers := map[string]struct{}{}
	for _, member := range scoutChatThreadMemberEmails(source) {
		sourceMembers[member] = struct{}{}
	}
	if destinationPrivate {
		_, allowed := sourceMembers[destinationOwner]
		return allowed
	}
	destinationMembers := scoutChatThreadMemberEmails(destination)
	if len(destinationMembers) == 0 {
		return false
	}
	for _, member := range destinationMembers {
		if _, allowed := sourceMembers[member]; !allowed {
			return false
		}
	}
	return true
}

// fileEntryAudienceAllowsDestination is the per-file audience intersection
// for Drive-to-chat attachments. Unknown visibility fails closed.
func fileEntryAudienceAllowsDestination(metadata map[string]string, viewerEmail string, destination scoutChatThreadRecord) bool {
	visibility, ok := fileEntryVisibility(metadata)
	if !ok {
		return false
	}
	viewerEmail = normalizeAccountEmail(viewerEmail)
	destinationPrivate := scoutChatThreadVisibility(destination) == scoutChatVisibilityPrivate
	destinationOwner := normalizeAccountEmail(destination.OwnerEmail)
	switch visibility {
	case fileVisibilityCompany:
		return true
	case fileVisibilityPrivate:
		return destinationPrivate && destinationOwner == viewerEmail && normalizeAccountEmail(metadata["uploaderEmail"]) == viewerEmail
	case fileVisibilityPeople:
		if destinationPrivate {
			return fileGrantsAllowEmail(metadata, destinationOwner)
		}
		if scoutChatThreadIsOrganizationPublic(destination) {
			return false
		}
		members := scoutChatThreadMemberEmails(destination)
		if len(members) == 0 {
			return false
		}
		for _, member := range members {
			if !fileGrantsAllowEmail(metadata, member) {
				return false
			}
		}
		return true
	}
	return false
}

func (app *kanbanBoardApp) assistantFileAttachmentSourceForDestination(ctx context.Context, viewer *userAccount, fileID string, destination scoutChatThreadRecord) (scoutChatFileAttachment, blobMeta, string, bool) {
	if !app.assistantFileSourceAllowsDestination(ctx, viewer, fileID, destination) {
		return scoutChatFileAttachment{}, blobMeta{}, "", false
	}
	return app.assistantFileAttachmentSource(ctx, viewer, fileID)
}

func (app *kanbanBoardApp) assistantFileSourceRevisionForDestination(ctx context.Context, viewer *userAccount, fileID string, destination scoutChatThreadRecord) (string, bool) {
	_, _, revision, ok := app.assistantFileAttachmentSourceForDestination(ctx, viewer, fileID, destination)
	return revision, ok
}

// authorizedFileDeliverableCandidates collects IDs only, then obtains each
// exact artifact snapshot through the body-free authorization seam. No title,
// metadata, or Text is copied before the viewer is authorized.
func (app *kanbanBoardApp) authorizedFileDeliverableCandidates(ctx context.Context, viewer *userAccount, actions ...ACLAction) []meetingMemoryEntry {
	if app == nil || app.memory == nil || viewer == nil {
		return nil
	}
	app.memory.mu.Lock()
	ids := make([]string, 0)
	for _, stored := range app.memory.entries {
		if stored.Kind != meetingMemoryKindOSArtifact {
			continue
		}
		ids = append(ids, stored.ID)
	}
	app.memory.mu.Unlock()
	allowed := make([]meetingMemoryEntry, 0, len(ids))
	for _, id := range ids {
		candidate, ok := authorizedArtifactForActions(ctx, viewer, id, actions...)
		if ok {
			allowed = append(allowed, candidate)
		}
	}
	return allowed
}

func fileRecordTime(row assistantFileRecord) time.Time {
	if parsed, err := time.Parse(time.RFC3339Nano, row.CreatedAt); err == nil {
		return parsed
	}
	if parsed, err := time.Parse(time.RFC3339, row.CreatedAt); err == nil {
		return parsed
	}
	return time.Time{}
}

// savedToFilesGrandfatherMarkerKind / savedToFilesGrandfatherMarkerID identify
// the run-once marker for grandfatherSavedToFilesAtBoot: a boot looks it up by
// (kind, id) and skips the migration when present. The id's v1 suffix versions
// the migration — a future re-grandfather uses a new id. The marker is a
// persisted-but-hidden bookkeeping record: it is stamped relevance=expired so
// the single memoryEntryHiddenFromRecall gate keeps it out of Scout's recall,
// the model context, the memory snapshot, AND the client timeline in one move
// (adding a bespoke kind to each of those filter lists would reach well past
// this file). expired is NOT quarantined and carries no expiresAt, so the sole
// hard-delete sweep (sweepExpiredQuarantine, quarantined-only) never reaps it —
// the marker rides the memory volume for the life of the store.
const (
	savedToFilesGrandfatherMarkerKind = "migration_marker"
	savedToFilesGrandfatherMarkerID   = "migration-saved-to-files-grandfather-v1"
)

// grandfatherSavedToFilesAtBoot is a run-ONCE startup backfill (kanban-card-110):
// the explicit-save gate would otherwise disappear every deliverable already
// living on the Files surface, so on the FIRST boot after the gate ships we stamp
// savedToFiles=true on each os_artifact that qualified under the PRE-gate rules
// (deliverableRecordQualifies). A persisted marker (gate finding A) makes this
// exactly-once per store: without it the migration re-stamps savedToFiles=true on
// EVERY redeploy, silently resurrecting deliverables the team deliberately left
// unsaved after the gate. A second boot is a no-op EVEN IF new qualifying
// unstamped deliverables now exist — those are post-gate creations the
// explicit-save policy owns.
func (app *kanbanBoardApp) grandfatherSavedToFilesAtBoot() {
	if app == nil || app.memory == nil {
		return
	}
	if _, done := app.memory.entryByKindAndID(savedToFilesGrandfatherMarkerKind, savedToFilesGrandfatherMarkerID); done {
		return
	}

	// Read pass: collect every pre-gate-qualifying deliverable that carries no
	// savedToFiles decision yet. A prior stamp (either "true" or a user's explicit
	// "false" unsave) is a decision already made — never resurrect an unsaved one.
	targetIDs := make([]string, 0)
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0) {
		if strings.TrimSpace(entry.Metadata["savedToFiles"]) != "" {
			continue
		}
		if !deliverableRecordQualifies(entry) {
			continue
		}
		targetIDs = append(targetIDs, entry.ID)
	}

	// Single batched stamp+rewrite (gate finding B): one lock, one fsync'd JSONL
	// rewrite for all N deliverables rather than N full re-encodes at boot.
	stamped, err := app.memory.updateOSArtifactsMetadataBatch(targetIDs, map[string]string{"savedToFiles": "true"})
	if err != nil {
		// Leave the marker unwritten so the next boot retries the backfill.
		log.Errorf("grandfather savedToFiles batch stamp failed: %v", err)
		return
	}

	// Record the marker LAST: a crash between the stamp and the marker re-runs the
	// migration next boot, which is idempotent for the already-stamped set —
	// strictly safer than recording first and skipping the backfill entirely.
	// meetingId is pre-stamped ("none"): appendEntry lazily MINTS the office
	// meeting id when that field is empty (appendEntryForMeeting), and a
	// boot-time marker must never open a phantom office sitting.
	if _, _, err := app.memory.appendEntry(
		savedToFilesGrandfatherMarkerKind,
		savedToFilesGrandfatherMarkerID,
		fmt.Sprintf("Files savedToFiles grandfather migration ran; stamped %d deliverable(s).", stamped),
		map[string]string{"migration": savedToFilesGrandfatherMarkerID, relevanceMetadataKey: relevanceExpired, "meetingId": "none"},
	); err != nil {
		log.Errorf("grandfather savedToFiles marker append failed: %v", err)
		return
	}

	if stamped > 0 {
		log.Infof("Files grandfather migration stamped %d existing deliverable(s) savedToFiles=true", stamped)
	}
}

// assistantFilesHandler serves GET /assistant/files — the Files surface list.
// Gate pattern of assistantMemoryHandler: method, origin, session, app.
func assistantFilesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodDelete && r.Method != http.MethodPatch {
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
		writeAuthError(w, http.StatusServiceUnavailable, "files are unavailable")
		return
	}
	if !strideE10TenantSurfaceUseBound(r.Context(), StrideE10TenantSurfaceDrive) {
		err := withStrideE10TenantRequestUse(r, StrideE10TenantSurfaceDrive, func(ctx context.Context, _ *StrideE10TenantPrincipal) error {
			assistantFilesHandler(w, r.WithContext(ctx))
			return nil
		})
		if err != nil {
			writeStrideE10TenantHookError(w, err, "files are unavailable")
		}
		return
	}
	if r.Method == http.MethodDelete {
		assistantFileDelete(w, r, user)
		return
	}
	if r.Method == http.MethodPatch {
		assistantFilePatch(w, r, user)
		return
	}

	query := r.URL.Query()
	// ?versionsOf=<id>: the same-name re-upload chain, newest first (D5).
	if versionsOf := strings.TrimSpace(query.Get("versionsOf")); versionsOf != "" {
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok":         true,
			"versionsOf": versionsOf,
			"files":      kanbanApp.assistantFileVersionsForPrincipal(r.Context(), user, versionsOf),
		})
		return
	}
	var rows []assistantFileRecord
	scope := strings.ToLower(strings.TrimSpace(query.Get("scope")))
	switch scope {
	case "trash":
		// The caller's own trashed uploads (D6).
		rows = kanbanApp.assistantTrashedFilesForPrincipal(r.Context(), user)
	case "", "live":
		scope = ""
		rows = kanbanApp.assistantFilesForPrincipal(r.Context(), user)
	default:
		writeAuthError(w, http.StatusBadRequest, "unknown files scope")
		return
	}
	// ?q=: name/uploader/ingested-text search, ACL-scoped (D7).
	searchQuery := strings.TrimSpace(query.Get("q"))
	if searchQuery != "" {
		rows = kanbanApp.searchAssistantFilesForPrincipal(r.Context(), user, rows, searchQuery)
	}
	folders := []assistantFileFolderPayload{}
	if principal, canonical := strideE10TenantPrincipalFromContext(r.Context()); canonical {
		folders = decorateAssistantFileFoldersForTenant(rows, principal.TenantID)
	} else {
		folders = decorateAssistantFileFolders(rows)
	}
	payload := map[string]any{
		"ok":      true,
		"files":   rows,
		"folders": folders,
	}
	if scope != "" {
		payload["scope"] = scope
	}
	if searchQuery != "" {
		payload["q"] = searchQuery
	}
	writeAuthJSON(w, http.StatusOK, payload)
}

// assistantFilePatch serves PATCH /assistant/files:
//
//	{id, name?}                                   rename (uploader/admin)
//	{id, visibility?, grants?: {add?[], remove?[]}} manage access (uploader ONLY → 403 otherwise)
//	{id, starred?}                                per-viewer star (any reader)
//
// Fields compose in one call; the response carries the row as the caller now
// sees it. A body with none of them keeps the historical rename error.
func assistantFilePatch(w http.ResponseWriter, r *http.Request, user *userAccount) {
	payload := struct {
		ID         string  `json:"id"`
		FileID     string  `json:"fileId"`
		Name       *string `json:"name"`
		Visibility *string `json:"visibility"`
		Grants     *struct {
			Add    []string `json:"add"`
			Remove []string `json:"remove"`
		} `json:"grants"`
		Starred *bool `json:"starred"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read file update request")
		return
	}
	fileID := firstNonEmptyString(strings.TrimSpace(payload.ID), strings.TrimSpace(payload.FileID))
	if fileID == "" {
		writeAuthError(w, http.StatusBadRequest, errFileFolderFileID.Error())
		return
	}
	if payload.Name == nil && payload.Visibility == nil && payload.Grants == nil && payload.Starred == nil {
		writeAuthError(w, http.StatusBadRequest, errAssistantFileName.Error())
		return
	}
	ctx := r.Context()

	// Phase 1 — validate and authorize EVERY sub-update before applying any.
	// A legacy admin may rename someone else's upload but never widen its
	// access; a body naming both must fail as a whole, with nothing renamed
	// and nothing broadcast, instead of renaming first and 403ing second.
	name := ""
	if payload.Name != nil {
		normalized, err := normalizeAssistantFileName(*payload.Name)
		if err != nil {
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		name = normalized
		if row, writable := authorizedFileRowForMove(ctx, user, fileID); row.ID == "" || !writable {
			writeAuthError(w, http.StatusNotFound, "file not found")
			return
		}
	}
	var access *assistantFileAccessUpdate
	if payload.Visibility != nil || payload.Grants != nil {
		var add, remove []string
		if payload.Grants != nil {
			add, remove = payload.Grants.Add, payload.Grants.Remove
		}
		prepared, err := kanbanApp.prepareAssistantFileAccessUpdate(ctx, user, fileID, payload.Visibility, add, remove)
		if err != nil {
			status, message := fileAccessErrorStatus(err)
			if status == http.StatusInternalServerError {
				log.Errorf("Update Drive file access %s failed: %v", fileID, err)
			}
			writeAuthError(w, status, message)
			return
		}
		access = &prepared
	}
	if payload.Starred != nil {
		if _, ok := kanbanApp.assistantFileEntryForViewer(ctx, user, fileID); !ok {
			writeAuthError(w, http.StatusNotFound, "file not found")
			return
		}
	}

	// Phase 2 — apply. A rename composed with an access change on a direct
	// upload (the only row kind manage-access resolves) rides the SAME
	// metadata rewrite, so the two can never land half-way.
	var row assistantFileRecord
	if payload.Name != nil && access != nil {
		access.updates["name"] = name
		updated, err := kanbanApp.applyAssistantFileAccessUpdate(ctx, user, *access)
		if err != nil {
			log.Errorf("Update Drive file %s failed: %v", fileID, err)
			writeAuthError(w, http.StatusInternalServerError, "could not update the file")
			return
		}
		row = updated
		broadcastSignedInKanbanEvent("file", map[string]any{"kind": "renamed", "file": row})
		broadcastSignedInKanbanEvent("file", map[string]any{"kind": "access", "fileId": fileID})
	} else {
		if payload.Name != nil {
			updated, err := kanbanApp.renameAssistantFileForUser(ctx, user, fileID, name)
			if err != nil {
				if errors.Is(err, errFileSaveNotFound) {
					writeAuthError(w, http.StatusNotFound, "file not found")
					return
				}
				log.Errorf("Rename Drive file %s failed: %v", fileID, err)
				writeAuthError(w, http.StatusInternalServerError, "could not rename the file")
				return
			}
			row = updated
			broadcastSignedInKanbanEvent("file", map[string]any{"kind": "renamed", "file": row})
		}
		if access != nil {
			updated, err := kanbanApp.applyAssistantFileAccessUpdate(ctx, user, *access)
			if err != nil {
				log.Errorf("Update Drive file access %s failed: %v", fileID, err)
				writeAuthError(w, http.StatusInternalServerError, "could not update the file")
				return
			}
			row = updated
			broadcastSignedInKanbanEvent("file", map[string]any{"kind": "access", "fileId": fileID})
		}
	}
	if payload.Starred != nil {
		updated, err := kanbanApp.setAssistantFileStarredForUser(ctx, user, fileID, *payload.Starred)
		if err != nil {
			if errors.Is(err, errFileSaveNotFound) {
				writeAuthError(w, http.StatusNotFound, "file not found")
				return
			}
			log.Errorf("Star Drive file %s failed: %v", fileID, err)
			writeAuthError(w, http.StatusInternalServerError, "could not update the file")
			return
		}
		row = updated
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "file": row})
}

// fileAccessErrorStatus maps manage-access errors onto honest statuses. A
// missing row and a row the caller may not even read collapse into 404; a
// readable row whose access the caller may not change is an explicit 403.
func fileAccessErrorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, errFileSaveNotFound):
		return http.StatusNotFound, "file not found"
	case errors.Is(err, errFileAccessForbidden):
		return http.StatusForbidden, errFileAccessForbidden.Error()
	case errors.Is(err, errFileVisibilityInvalid), errors.Is(err, errFileGrantUnregistered):
		return http.StatusBadRequest, err.Error()
	default:
		return http.StatusInternalServerError, "could not update the file"
	}
}

// updateAssistantFileAccessForUser changes a direct upload's visibility and/or
// grants. Uploader only — the approval admin may delete a row but never widen
// who reads it. Grants must be registered, enabled accounts (checked one by
// one, never enumerated back to the caller); the uploader is implicit.
func (app *kanbanBoardApp) updateAssistantFileAccessForUser(ctx context.Context, user *userAccount, fileID string, visibility *string, add []string, remove []string) (assistantFileRecord, error) {
	prepared, err := app.prepareAssistantFileAccessUpdate(ctx, user, fileID, visibility, add, remove)
	if err != nil {
		return assistantFileRecord{}, err
	}
	return app.applyAssistantFileAccessUpdate(ctx, user, prepared)
}

// assistantFileAccessUpdate is a fully validated, fully authorized manage-
// access change that has not been written yet — the PATCH door validates
// every sub-update first and only then applies (nothing half-lands).
type assistantFileAccessUpdate struct {
	entry   meetingMemoryEntry
	updates map[string]string
}

// prepareAssistantFileAccessUpdate resolves the row, checks the caller's
// share authority, normalizes the visibility and grants, and returns the
// metadata rewrite to apply. It writes nothing.
func (app *kanbanBoardApp) prepareAssistantFileAccessUpdate(ctx context.Context, user *userAccount, fileID string, visibility *string, add []string, remove []string) (assistantFileAccessUpdate, error) {
	if app == nil || app.memory == nil || user == nil {
		return assistantFileAccessUpdate{}, errFileSaveNotFound
	}
	entry, found := app.memory.entryByKindAndID(meetingMemoryKindFile, strings.TrimSpace(fileID))
	if !found || fileEntryTrashed(entry.Metadata) || !app.fileEntryReadableByViewer(ctx, user, entry) {
		return assistantFileAccessUpdate{}, errFileSaveNotFound
	}
	if !authorizeFileEntry(ctx, user, ACLShare, entry) {
		return assistantFileAccessUpdate{}, errFileAccessForbidden
	}
	updates := map[string]string{"ownerEmail": normalizeAccountEmail(entry.Metadata["uploaderEmail"])}
	if visibility != nil {
		normalized, ok := normalizeFileVisibility(*visibility)
		if !ok || strings.TrimSpace(*visibility) == "" {
			return assistantFileAccessUpdate{}, errFileVisibilityInvalid
		}
		updates["visibility"] = normalized
	} else if current, ok := fileEntryVisibility(entry.Metadata); ok {
		// Stamp the read-time default explicitly once access is being managed.
		updates["visibility"] = current
	}
	uploader := normalizeAccountEmail(entry.Metadata["uploaderEmail"])
	grants := map[string]struct{}{}
	for _, existing := range fileGrantEmails(entry.Metadata) {
		grants[existing] = struct{}{}
	}
	for _, raw := range add {
		email := normalizeAccountEmail(raw)
		if email == "" || email == uploader {
			continue
		}
		if accountStore().findUser(email) == nil || accountIsDisabled(email) {
			return assistantFileAccessUpdate{}, errFileGrantUnregistered
		}
		grants[email] = struct{}{}
	}
	for _, raw := range remove {
		delete(grants, normalizeAccountEmail(raw))
	}
	merged := make([]string, 0, len(grants))
	for email := range grants {
		merged = append(merged, email)
	}
	sort.Strings(merged)
	updates["grants"] = strings.Join(merged, ",")
	return assistantFileAccessUpdate{entry: entry, updates: updates}, nil
}

// applyAssistantFileAccessUpdate lands a prepared change in one metadata
// rewrite and returns the row as the caller now sees it.
func (app *kanbanBoardApp) applyAssistantFileAccessUpdate(ctx context.Context, user *userAccount, prepared assistantFileAccessUpdate) (assistantFileRecord, error) {
	if app == nil || app.memory == nil || user == nil || prepared.entry.ID == "" {
		return assistantFileRecord{}, errFileSaveNotFound
	}
	updated, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindFile, prepared.entry.ID, prepared.entry.Text, prepared.updates)
	if err != nil {
		return assistantFileRecord{}, err
	}
	if err := app.publishDriveFileSourceEpisode(updated); err != nil && !errors.Is(err, ErrSourceEpisodeUnavailable) {
		log.Errorf("SourceEpisode Drive access publication unavailable: %v", err)
	}
	row := app.decorateFileRowForViewer(ctx, user, updated)
	_, assignments := sharedFileFolderStore().snapshot()
	row.FolderID = assignments[prepared.entry.ID]
	return row, nil
}

// setAssistantFileStarredForUser toggles the caller's own star on a readable
// row (metadata starredBy is a per-viewer list, never shared row state).
func (app *kanbanBoardApp) setAssistantFileStarredForUser(ctx context.Context, user *userAccount, fileID string, starred bool) (assistantFileRecord, error) {
	entry, ok := app.assistantFileEntryForViewer(ctx, user, fileID)
	if !ok {
		return assistantFileRecord{}, errFileSaveNotFound
	}
	email := normalizeAccountEmail(user.Email)
	starredBy := make([]string, 0, 4)
	for _, existing := range splitFileEmailList(entry.Metadata["starredBy"]) {
		if existing != email {
			starredBy = append(starredBy, existing)
		}
	}
	if starred && email != "" {
		starredBy = append(starredBy, email)
	}
	sort.Strings(starredBy)
	updated, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindFile, entry.ID, entry.Text, map[string]string{"starredBy": strings.Join(starredBy, ",")})
	if err != nil {
		return assistantFileRecord{}, err
	}
	row := app.decorateFileRowForViewer(ctx, user, updated)
	_, assignments := sharedFileFolderStore().snapshot()
	row.FolderID = assignments[entry.ID]
	return row, nil
}

// restoreAssistantFileForUser lifts a trashed upload back into the live list.
// The row must be readable by the caller through the same seam every other
// Drive door uses (a trashed row reads only for its uploader) AND deletable —
// so the legacy approval admin, who may purge but cannot read another
// member's private upload, gets the same non-enumerating 404 the trash list
// and restore-by-others already give. The blob never left. Restoring a row
// re-heads its version chain: if it is the newest untrashed version it
// becomes the live head again and the interim head is re-superseded.
func (app *kanbanBoardApp) restoreAssistantFileForUser(ctx context.Context, user *userAccount, fileID string) (assistantFileRecord, error) {
	if app == nil || app.memory == nil || user == nil {
		return assistantFileRecord{}, errFileSaveNotFound
	}
	entry, found := app.memory.entryByKindAndID(meetingMemoryKindFile, strings.TrimSpace(fileID))
	if !found || !app.fileEntryReadableByViewer(ctx, user, entry) || !authorizeFileEntry(ctx, user, ACLDelete, entry) {
		return assistantFileRecord{}, errFileSaveNotFound
	}
	if !fileEntryTrashed(entry.Metadata) {
		return assistantFileRecord{}, errFileNotTrashed
	}
	updated, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindFile, entry.ID, entry.Text, map[string]string{
		"deletedAt": "", "deletedBy": "", relevanceMetadataKey: relevanceActive,
	})
	if err != nil {
		return assistantFileRecord{}, err
	}
	if err := app.reconcileFileVersionChain(entry.ID); err != nil {
		log.Errorf("Drive version chain re-head after restore of %s failed: %v", entry.ID, err)
	} else if current, ok := app.memory.entryByKindAndID(meetingMemoryKindFile, entry.ID); ok {
		updated = current
	}
	if err := app.publishDriveFileSourceEpisode(updated); err != nil && !errors.Is(err, ErrSourceEpisodeUnavailable) {
		log.Errorf("SourceEpisode Drive restore publication unavailable: %v", err)
	}
	row := app.decorateFileRowForViewer(ctx, user, updated)
	_, assignments := sharedFileFolderStore().snapshot()
	row.FolderID = assignments[entry.ID]
	broadcastSignedInKanbanEvent("file", map[string]any{"kind": "restored", "file": row})
	return row, nil
}

func normalizeAssistantFileName(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errAssistantFileName
	}
	name := assistantFileUploadName(raw)
	if strings.TrimSpace(name) == "" {
		return "", errAssistantFileName
	}
	return name, nil
}

func (app *kanbanBoardApp) renameAssistantFileForUser(ctx context.Context, user *userAccount, fileID string, name string) (assistantFileRecord, error) {
	row, writable := authorizedFileRowForMove(ctx, user, fileID)
	if row.ID == "" || !writable {
		return assistantFileRecord{}, errFileSaveNotFound
	}
	switch row.Origin {
	case "deliverable":
		artifact, ok := authorizedArtifactForActions(ctx, user, row.ArtifactID, ACLReadContent, ACLWrite)
		if !ok {
			return assistantFileRecord{}, errFileSaveNotFound
		}
		header := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact))
		updated, matched, err := app.memory.updateOSArtifactMetadataIfHeaderMatches(header, artifact.ID, map[string]string{"driveFileName": name})
		if err != nil || !matched {
			if err != nil {
				return assistantFileRecord{}, err
			}
			return assistantFileRecord{}, errFileSaveNotFound
		}
		row, _ = fileDeliverableRecord(updated)
	case "chat":
		threadID, messageID, fileIndex, ok := parseChatAttachmentFileID(fileID)
		if !ok {
			return assistantFileRecord{}, errFileSaveNotFound
		}
		thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
		if err != nil {
			return assistantFileRecord{}, errFileSaveNotFound
		}
		messageIndex := -1
		for index := range thread.Messages {
			if thread.Messages[index].ID == messageID {
				messageIndex = index
				break
			}
		}
		if messageIndex < 0 || fileIndex < 0 || fileIndex >= len(thread.Messages[messageIndex].Files) {
			return assistantFileRecord{}, errFileSaveNotFound
		}
		thread.Messages[messageIndex].Files[fileIndex].Name = name
		thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := app.saveScoutChatThread(thread); err != nil {
			return assistantFileRecord{}, err
		}
		rows := app.fileRecordsFromThread(user.Email, thread)
		for _, candidate := range rows {
			if candidate.ID == fileID {
				row = candidate
				break
			}
		}
	default:
		entry, ok := app.memory.entryByID(fileID)
		if !ok || entry.Kind != meetingMemoryKindFile {
			return assistantFileRecord{}, errFileSaveNotFound
		}
		updated, changed, err := app.memory.updateEntryWithMetadata(meetingMemoryKindFile, entry.ID, entry.Text, map[string]string{"name": name})
		if err != nil {
			return assistantFileRecord{}, err
		}
		if !changed && strings.TrimSpace(entry.Metadata["name"]) != name {
			return assistantFileRecord{}, errFileSaveNotFound
		}
		if err := app.publishDriveFileSourceEpisode(updated); err != nil && !errors.Is(err, ErrSourceEpisodeUnavailable) {
			log.Errorf("SourceEpisode Drive rename publication unavailable: %v", err)
		}
		row = fileRecordFromEntry(updated)
	}
	_, assignments := sharedFileFolderStore().snapshot()
	row.FolderID = assignments[fileID]
	row.CanDelete = true
	return row, nil
}

// assistantFileDelete removes one row through its source-of-truth seam. A
// direct upload is deleted from memory (and therefore semantic recall); a chat
// attachment is removed from its source message; a saved deliverable is only
// removed from Drive because the underlying artifact remains first-class in
// Artifacts. The response names that distinction so the UI never over-promises.
func assistantFileDelete(w http.ResponseWriter, r *http.Request, user *userAccount) {
	payload := struct {
		FileID string `json:"fileId"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read delete request")
		return
	}
	payload.FileID = strings.TrimSpace(payload.FileID)
	if payload.FileID == "" {
		writeAuthError(w, http.StatusBadRequest, errFileFolderFileID.Error())
		return
	}
	mode, err := kanbanApp.deleteAssistantFileForUser(r.Context(), user, payload.FileID)
	if err != nil {
		if errors.Is(err, errFileSaveNotFound) || strings.Contains(err.Error(), "not found") {
			writeAuthError(w, http.StatusNotFound, "file not found")
			return
		}
		log.Errorf("Delete Drive file %s failed: %v", payload.FileID, err)
		writeAuthError(w, http.StatusInternalServerError, "could not delete the file")
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": mode})
}

// deleteAssistantFileForUser is the source-aware Drive deletion domain seam
// shared by the HTTP control and Scout native actions. It reauthorizes the
// stable file id at execution time and reports whether the underlying source
// was deleted or only its Drive projection was removed.
func (app *kanbanBoardApp) deleteAssistantFileForUser(ctx context.Context, user *userAccount, fileID string) (string, error) {
	row, writable := authorizedFileRowForMove(ctx, user, fileID)
	if row.ID == "" || !writable {
		return "", errFileSaveNotFound
	}

	mode := "deleted"
	var err error
	switch row.Origin {
	case "deliverable":
		if err := app.revokeAttachmentSourcesForOrigin(fileID); err != nil {
			return "", err
		}
		artifact, ok := authorizedArtifactForActions(ctx, user, row.ArtifactID, ACLReadContent, ACLWrite)
		if !ok {
			return "", errFileSaveNotFound
		}
		header := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact))
		_, _, err = app.memory.updateOSArtifactMetadataIfHeaderMatches(header, artifact.ID, map[string]string{
			"savedToFiles": "false",
		})
		mode = "removed_from_drive"
	case "chat":
		err = app.deleteChatAttachmentFromDrive(user, fileID)
	default:
		// Soft delete (D6): the row is stamped deletedAt and hidden from every
		// list and from recall (relevance=expired rides the single
		// memoryEntryHiddenFromRecall gate); the blob and folder assignment are
		// retained so restore is exact. Attachments derived from this file stop
		// resolving while it is trashed because assistantFileAttachmentSource
		// refuses trashed rows; the daily sweep revokes them for good at purge.
		entry, ok := app.memory.entryByKindAndID(meetingMemoryKindFile, fileID)
		if !ok || fileEntryTrashed(entry.Metadata) {
			err = errFileSaveNotFound
			break
		}
		now := time.Now().UTC()
		var trashed meetingMemoryEntry
		trashed, _, err = app.memory.updateEntryWithMetadata(meetingMemoryKindFile, fileID, entry.Text, map[string]string{
			"deletedAt": now.Format(time.RFC3339Nano), "deletedBy": normalizeAccountEmail(user.Email), relevanceMetadataKey: relevanceExpired,
		})
		if err == nil {
			if publishErr := app.tombstoneDriveFileSourceEpisode(trashed, now); publishErr != nil && !errors.Is(publishErr, ErrSourceEpisodeUnavailable) {
				log.Errorf("SourceEpisode Drive deletion publication unavailable: %v", publishErr)
			}
			// Trashing the head of a version chain must not make the whole
			// chain vanish: the newest untrashed prior version becomes the
			// live head again (D2).
			if chainErr := app.reconcileFileVersionChain(fileID); chainErr != nil {
				log.Errorf("Drive version chain re-head after trashing %s failed: %v", fileID, chainErr)
			}
		}
		mode = "trashed"
	}
	if err != nil {
		return "", err
	}
	// Folder assignments are projections. A persistence failure here can only
	// leave a harmless dangling id, so it must not resurrect the removed source.
	// A trashed upload keeps its assignment so restore returns it to its folder.
	if mode != "trashed" {
		if err := moveFileToFolder(fileID, ""); err != nil {
			log.Errorf("Clear deleted Drive file folder assignment %s failed: %v", fileID, err)
		}
	}
	broadcastSignedInKanbanEvent("file", map[string]any{"kind": "deleted", "fileId": fileID, "mode": mode})
	return mode, nil
}

// sweepFileTrashOnce hard-deletes every upload trashed longer than
// fileTrashRetention: attachment grants that originated from it are revoked,
// the row leaves memory, and its folder assignment is cleared. It then hands
// off to the weekly blob GC so orphaned bytes are reclaimed behind the purge.
// Keyless-safe: no provider call anywhere on this path.
func (app *kanbanBoardApp) sweepFileTrashOnce(now time.Time) int {
	if app == nil || app.memory == nil {
		return 0
	}
	var expired []meetingMemoryEntry
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindFile, 0) {
		deletedAt := parseRFC3339OrZero(entry.Metadata["deletedAt"])
		if deletedAt.IsZero() || now.Sub(deletedAt) < fileTrashRetention {
			continue
		}
		expired = append(expired, entry)
	}
	purged := len(app.purgeTrashedFileEntries(expired))
	if purged > 0 {
		log.Infof("Drive trash sweep purged %d upload(s) trashed longer than %s", purged, fileTrashRetention)
	}
	if _, _, err := app.runScheduledBlobSweep(now); err != nil {
		log.Errorf("Scheduled blob sweep after Drive trash purge failed: %v", err)
	}
	return purged
}

// purgeTrashedFileEntry hard-deletes one trashed upload (the batch form below
// is the shared path; this is its single-row convenience).
func (app *kanbanBoardApp) purgeTrashedFileEntry(entry meetingMemoryEntry) bool {
	return len(app.purgeTrashedFileEntries([]meetingMemoryEntry{entry})) == 1
}

// purgeTrashedFileEntries is the one hard-delete path for trashed uploads,
// shared by the daily sweep and the self-service empty-trash door: revoke the
// attachment grants that originated from each row, splice each row out of its
// version chain (its children re-point at its parent so a purged middle
// version never severs the chain), remove every row in ONE store rewrite,
// then clear the folder assignments. Never touches a live (untrashed) row; a
// row whose grant revocation fails is left for the next pass. Returns the
// rows actually removed.
func (app *kanbanBoardApp) purgeTrashedFileEntries(entries []meetingMemoryEntry) []meetingMemoryEntry {
	if app == nil || app.memory == nil || len(entries) == 0 {
		return nil
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Kind != meetingMemoryKindFile || !fileEntryTrashed(entry.Metadata) {
			continue
		}
		if err := app.revokeAttachmentSourcesForOrigin(entry.ID); err != nil {
			log.Errorf("Drive trash purge could not revoke attachment sources for %s: %v", entry.ID, err)
			continue
		}
		ids = append(ids, entry.ID)
	}
	if len(ids) == 0 {
		return nil
	}
	purging := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		purging[id] = struct{}{}
	}
	// Splice: a child of a purged row inherits the purged row's parent (walking
	// past any parent that is itself being purged). versionOf edges are what
	// bridge a chain across trashed versions, so they must survive the purge.
	byID := map[string]meetingMemoryEntry{}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindFile, 0) {
		byID[entry.ID] = entry
	}
	for _, entry := range byID {
		if _, gone := purging[entry.ID]; gone {
			continue
		}
		parent := strings.TrimSpace(entry.Metadata["versionOf"])
		if _, parentGone := purging[parent]; parent == "" || !parentGone {
			continue
		}
		visited := map[string]bool{}
		for parent != "" && !visited[parent] {
			visited[parent] = true
			if _, gone := purging[parent]; !gone {
				break
			}
			parent = strings.TrimSpace(byID[parent].Metadata["versionOf"])
		}
		if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindFile, entry.ID, entry.Text, map[string]string{"versionOf": parent}); err != nil {
			log.Errorf("Drive trash purge could not splice version chain at %s: %v", entry.ID, err)
		}
	}
	removed, err := app.memory.deleteEntriesByIDJournaled(ids, func(current meetingMemoryEntry) bool {
		return current.Kind == meetingMemoryKindFile && fileEntryTrashed(current.Metadata)
	})
	if err != nil {
		log.Errorf("Drive trash purge could not remove %d row(s): %v", len(ids), err)
		return nil
	}
	for _, entry := range removed {
		if err := moveFileToFolder(entry.ID, ""); err != nil {
			log.Errorf("Clear purged Drive file folder assignment %s failed: %v", entry.ID, err)
		}
	}
	return removed
}

// emptyAssistantFileTrashForUser hard-deletes the caller's OWN trashed
// uploads now (the 30-day sweep is the default; this is the self-service
// escape hatch for quota). Returns the purged count and the bytes the Drive's
// usage actually dropped by (a blob still referenced elsewhere frees nothing).
// One Drive scan does both jobs: it selects the caller's trashed rows and
// records, per content-addressed blob, the bytes it holds and how many rows
// hold it — so the freed bytes are exactly driveUsageForPrincipal's delta
// without walking the Drive twice more.
func (app *kanbanBoardApp) emptyAssistantFileTrashForUser(ctx context.Context, user *userAccount) (int, int64) {
	if app == nil || app.memory == nil || user == nil {
		return 0, 0
	}
	principal, canonical := strideE10TenantPrincipalFromContext(ctx)
	type blobHolders struct {
		size int64
		rows int
	}
	holders := map[string]*blobHolders{}
	usageKey := func(entry meetingMemoryEntry) string {
		key := strings.TrimSpace(entry.Metadata["blobRef"])
		if !validBlobRef(key) {
			key = "entry:" + entry.ID
		}
		return key
	}
	var targets []meetingMemoryEntry
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindFile, 0) {
		if canonical && strings.TrimSpace(entry.Metadata["tenantId"]) != principal.TenantID {
			continue
		}
		key := usageKey(entry)
		holder := holders[key]
		if holder == nil {
			holder = &blobHolders{}
			if size, err := strconv.ParseInt(strings.TrimSpace(entry.Metadata["size"]), 10, 64); err == nil && size > 0 {
				holder.size = size
			}
			holders[key] = holder
		}
		holder.rows++
		if fileEntryTrashed(entry.Metadata) && fileEntryUploadedByViewer(ctx, user, entry.Metadata) {
			targets = append(targets, entry)
		}
	}
	removed := app.purgeTrashedFileEntries(targets)
	purgedPerKey := map[string]int{}
	for _, entry := range removed {
		purgedPerKey[usageKey(entry)]++
	}
	var freed int64
	for key, count := range purgedPerKey {
		if holder := holders[key]; holder != nil && holder.rows == count {
			freed += holder.size
		}
	}
	if len(removed) > 0 {
		broadcastSignedInKanbanEvent("file", map[string]any{"kind": "trash_emptied", "purged": len(removed)})
	}
	return len(removed), freed
}

// assistantFileEmptyTrashHandler serves POST /assistant/files/trash/empty →
// {ok, purged, freedBytes}; caller's own trashed rows only.
func assistantFileEmptyTrashHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := assistantFilesGate(w, r, http.MethodPost, assistantFileEmptyTrashHandler)
	if !ok {
		return
	}
	purged, freed := kanbanApp.emptyAssistantFileTrashForUser(r.Context(), user)
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "purged": purged, "freedBytes": freed})
}

// startFileTrashSweeper runs sweepFileTrashOnce daily (first pass shortly
// after boot so a frequently redeployed droplet still purges on schedule).
// Same boot-once, keyless, no-app-lock shape as the liveness sweeper.
func (app *kanbanBoardApp) startFileTrashSweeper() {
	if app == nil || app.memory == nil {
		return
	}
	go func() {
		timer := time.NewTimer(fileTrashSweepBootDelay)
		<-timer.C
		app.sweepFileTrashOnce(time.Now().UTC())
		ticker := time.NewTicker(fileTrashSweepInterval)
		defer ticker.Stop()
		for range ticker.C {
			app.sweepFileTrashOnce(time.Now().UTC())
		}
	}()
}

// migrateFileVisibilityDefaults is the boot-time stamping migration for D1:
// every kind=file row with no visibility is stamped company — the read-time
// default it already receives, now made explicit on disk so later policy
// changes to the default can never silently re-scope legacy uploads. Rows
// already stamped are untouched, so a second run writes nothing; the stamped
// count is the only marker it needs.
func (app *kanbanBoardApp) migrateFileVisibilityDefaults() int {
	if app == nil || app.memory == nil {
		return 0
	}
	// Read pass collects ids only; the stamp is ONE locked pass with a single
	// fsync'd rewrite (an 88 MB store must not be rewritten once per row).
	targetIDs := make([]string, 0)
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindFile, 0) {
		if strings.TrimSpace(entry.Metadata["visibility"]) != "" {
			continue
		}
		targetIDs = append(targetIDs, entry.ID)
	}
	if len(targetIDs) == 0 {
		log.Infof("Drive visibility migration stamped 0 unlabeled file row(s) company")
		return 0
	}
	stamped, err := app.memory.updateEntriesWithMetadata(meetingMemoryKindFile, targetIDs, map[string]string{"visibility": fileVisibilityCompany})
	if err != nil {
		// Nothing was written; the next boot retries the same idempotent stamp.
		log.Errorf("Drive visibility migration batch stamp failed: %v", err)
		return 0
	}
	log.Infof("Drive visibility migration stamped %d unlabeled file row(s) company", stamped)
	return stamped
}

// driveUsage is the GET /assistant/files/usage payload (D9).
type driveUsage struct {
	BytesUsed  int64 `json:"bytesUsed"`
	FileCount  int   `json:"fileCount"`
	QuotaBytes int64 `json:"quotaBytes"`
}

// driveQuotaBytes reads DRIVE_QUOTA_BYTES (default 20 GiB).
func driveQuotaBytes() int64 {
	if raw := strings.TrimSpace(os.Getenv(driveQuotaBytesEnv)); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			return parsed
		}
	}
	return driveQuotaBytesDefault
}

// driveUsageForPrincipal sums the Drive's stored bytes (tenant-scoped under a
// bound principal). Blobs are content-addressed, so the same bytes stored
// under two rows count once; trashed rows still count because their blob is
// retained until the purge. fileCount is the live (non-trashed) row count.
func (app *kanbanBoardApp) driveUsageForPrincipal(ctx context.Context) driveUsage {
	usage := driveUsage{QuotaBytes: driveQuotaBytes()}
	if app == nil || app.memory == nil {
		return usage
	}
	principal, canonical := strideE10TenantPrincipalFromContext(ctx)
	counted := map[string]struct{}{}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindFile, 0) {
		if canonical && strings.TrimSpace(entry.Metadata["tenantId"]) != principal.TenantID {
			continue
		}
		if !fileEntryTrashed(entry.Metadata) {
			usage.FileCount++
		}
		key := strings.TrimSpace(entry.Metadata["blobRef"])
		if !validBlobRef(key) {
			key = "entry:" + entry.ID
		}
		if _, dup := counted[key]; dup {
			continue
		}
		counted[key] = struct{}{}
		if size, err := strconv.ParseInt(strings.TrimSpace(entry.Metadata["size"]), 10, 64); err == nil && size > 0 {
			usage.BytesUsed += size
		}
	}
	return usage
}

// assistantFilesGate is the shared gate stack of the Files doors (method,
// origin, session, app, tenant binding). It returns the signed-in user only
// when the caller may proceed on this exact request; on a tenant rebind it
// re-enters handler with the bound context and the caller must return.
func assistantFilesGate(w http.ResponseWriter, r *http.Request, method string, handler http.HandlerFunc) (*userAccount, bool) {
	if r.Method != method {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return nil, false
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return nil, false
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return nil, false
	}
	if kanbanApp == nil || kanbanApp.memory == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "files are unavailable")
		return nil, false
	}
	if !strideE10TenantSurfaceUseBound(r.Context(), StrideE10TenantSurfaceDrive) {
		err := withStrideE10TenantRequestUse(r, StrideE10TenantSurfaceDrive, func(ctx context.Context, _ *StrideE10TenantPrincipal) error {
			handler(w, r.WithContext(ctx))
			return nil
		})
		if err != nil {
			writeStrideE10TenantHookError(w, err, "files are unavailable")
		}
		return nil, false
	}
	return user, true
}

// assistantFileUsageHandler serves GET /assistant/files/usage (D9).
func assistantFileUsageHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := assistantFilesGate(w, r, http.MethodGet, assistantFileUsageHandler); !ok {
		return
	}
	usage := kanbanApp.driveUsageForPrincipal(r.Context())
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok": true, "bytesUsed": usage.BytesUsed, "fileCount": usage.FileCount, "quotaBytes": usage.QuotaBytes,
	})
}

// assistantFileRestoreHandler serves POST /assistant/files/restore {id} (D6).
func assistantFileRestoreHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := assistantFilesGate(w, r, http.MethodPost, assistantFileRestoreHandler)
	if !ok {
		return
	}
	payload := struct {
		ID     string `json:"id"`
		FileID string `json:"fileId"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read restore request")
		return
	}
	fileID := firstNonEmptyString(strings.TrimSpace(payload.ID), strings.TrimSpace(payload.FileID))
	if fileID == "" {
		writeAuthError(w, http.StatusBadRequest, errFileFolderFileID.Error())
		return
	}
	row, err := kanbanApp.restoreAssistantFileForUser(r.Context(), user, fileID)
	if err != nil {
		switch {
		case errors.Is(err, errFileSaveNotFound):
			writeAuthError(w, http.StatusNotFound, "file not found")
		case errors.Is(err, errFileNotTrashed):
			writeAuthError(w, http.StatusConflict, errFileNotTrashed.Error())
		default:
			log.Errorf("Restore Drive file %s failed: %v", fileID, err)
			writeAuthError(w, http.StatusInternalServerError, "could not restore the file")
		}
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "file": row})
}

func (app *kanbanBoardApp) deleteChatAttachmentFromDrive(user *userAccount, fileID string) error {
	threadID, messageID, fileIndex, ok := parseChatAttachmentFileID(fileID)
	if !ok || app == nil || app.memory == nil || user == nil {
		return errFileSaveNotFound
	}
	thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
	if err != nil {
		return errFileSaveNotFound
	}
	messageIndex := -1
	for index := range thread.Messages {
		if thread.Messages[index].ID == messageID {
			messageIndex = index
			break
		}
	}
	if messageIndex < 0 || fileIndex >= len(thread.Messages[messageIndex].Files) {
		return errFileSaveNotFound
	}
	file := thread.Messages[messageIndex].Files[fileIndex]
	actor := normalizeAccountEmail(user.Email)
	if !isArtifactApprovalAdmin(user) && actor != normalizeAccountEmail(thread.OwnerEmail) && actor != normalizeAccountEmail(thread.Messages[messageIndex].AuthorEmail) {
		return errFileSaveNotFound
	}
	if strings.TrimSpace(file.Ref) == "" && strings.TrimSpace(file.Text) == "" {
		return errFileSaveNotFound
	}
	// A Project-linked attachment is canonical source evidence. Revoke its
	// source (and synchronously terminalize every owning source group) before
	// changing the legacy message. A canonical failure therefore has zero
	// legacy effect; a later legacy save failure remains fail closed because
	// the committed grant can no longer authorize the stale file projection.
	if strings.TrimSpace(file.SourceID) != "" {
		if err := app.revokeAttachmentSource(file.SourceID); err != nil {
			return err
		}
	}
	thread.Messages[messageIndex].Files = append(thread.Messages[messageIndex].Files[:fileIndex], thread.Messages[messageIndex].Files[fileIndex+1:]...)
	thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := app.saveScoutChatThread(thread); err != nil {
		return err
	}
	return nil
}

// assistantFileUploadHandler serves POST /assistant/files/upload — the Files
// surface's direct-upload door. multipart/form-data with one "file" part, any
// type, capped at the blob store's 64MB ceiling. The bytes land in putBlob,
// the record lands as a kind=file memory entry, and — key permitting — the
// 085 ingestion seam (attachmentContentBlocks + deriveAttachmentText) runs
// once, synchronously (the same request-path law as chat sends), so the
// response already carries the honest brain badge.
func assistantFileUploadHandler(w http.ResponseWriter, r *http.Request) {
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
		writeAuthError(w, http.StatusServiceUnavailable, "files are unavailable")
		return
	}
	if !strideE10TenantSurfaceUseBound(r.Context(), StrideE10TenantSurfaceDrive) {
		err := withStrideE10TenantRequestUse(r, StrideE10TenantSurfaceDrive, func(ctx context.Context, _ *StrideE10TenantPrincipal) error {
			assistantFileUploadHandler(w, r.WithContext(ctx))
			return nil
		})
		if err != nil {
			writeStrideE10TenantHookError(w, err, "files are unavailable")
		}
		return
	}

	// 1MB of multipart framing headroom over the blob cap; putBlob re-checks
	// the decoded payload against blobMaxBytes exactly.
	r.Body = http.MaxBytesReader(w, r.Body, blobMaxBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeAuthError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file exceeds the %dMB cap", blobMaxBytes>>20))
			return
		}
		writeAuthError(w, http.StatusBadRequest, "could not read upload form")
		return
	}
	// ParseMultipartForm spills parts over the 8MB in-memory threshold to a
	// $TMPDIR temp file that is NOT auto-removed on return; RemoveAll frees them
	// so >8MB uploads don't accumulate and exhaust /tmp on the long-lived VPS.
	defer func() {
		if r.MultipartForm != nil {
			r.MultipartForm.RemoveAll()
		}
	}()
	part, header, err := r.FormFile("file")
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "upload form needs a file field")
		return
	}
	defer part.Close()
	data, err := io.ReadAll(part)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read upload body")
		return
	}
	if len(data) == 0 {
		writeAuthError(w, http.StatusBadRequest, "uploaded file is empty")
		return
	}
	if len(data) > blobMaxBytes {
		writeAuthError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file exceeds the %dMB cap", blobMaxBytes>>20))
		return
	}

	name := assistantFileUploadName(header.Filename)
	// D1: optional visibility (default company); D5: optional folderId so a
	// same-name re-upload chains inside its folder; D9: the quota is checked
	// before any bytes land in the blob store.
	requestedVisibility := strings.TrimSpace(r.FormValue("visibility"))
	visibility, visibilityOK := normalizeFileVisibility(requestedVisibility)
	if !visibilityOK {
		writeAuthError(w, http.StatusBadRequest, errFileVisibilityInvalid.Error())
		return
	}
	folderID := strings.TrimSpace(r.FormValue("folderId"))
	if !fileFolderWritableFromContext(r.Context(), user, folderID) {
		writeAuthError(w, fileFolderErrorStatus(errFileFolderNotFound), errFileFolderNotFound.Error())
		return
	}
	if usage := kanbanApp.driveUsageForPrincipal(r.Context()); usage.BytesUsed+int64(len(data)) > usage.QuotaBytes {
		writeAuthJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
			"error": errFileQuotaExceeded.Error(), "bytesUsed": usage.BytesUsed, "quotaBytes": usage.QuotaBytes,
		})
		return
	}
	uploadMime := assistantFileUploadMimeFor(header.Header.Get("Content-Type"), name)
	ref, err := putBlob(data, uploadMime)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The FIRST write pins the sidecar mime; a re-upload of known bytes
	// answers with the pinned value, exactly what the serve route uses.
	meta, err := blobStatForRef(ref)
	if err != nil {
		meta = blobMeta{Mime: uploadMime, Size: int64(len(data))}
	}

	// 085 ingestion seam, exactly once, direct-upload edition: model-safe
	// binaries get the bounded transcription pass; keyless deploys and other
	// types stay honest "stored".
	files := []scoutChatFileAttachment{{
		Name: name,
		Kind: strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), "."),
		Size: meta.Size,
		Ref:  ref,
		Mime: meta.Mime,
	}}
	transcript := directFilePlainText(data, meta.Mime)
	if transcript == "" && kanbanApp.currentOpenAIAPIKey() != "" && attachmentModelSafeMimes[meta.Mime] {
		attachments := openAIAttachmentContent(files)
		files = deriveAttachmentText(r.Context(), kanbanApp.currentOpenAIAPIKey(), files, attachments)
		transcript = strings.TrimSpace(files[0].Text)
	}
	brainStatus := fileBrainStatusStored
	if transcript != "" {
		brainStatus = fileBrainStatusIngested
	}

	now := time.Now().UTC()
	uploaderName := firstNonEmptyString(strings.TrimSpace(user.Name), normalizeAccountEmail(user.Email))
	entryText := fmt.Sprintf("File %s uploaded by %s.", name, uploaderName)
	if transcript != "" {
		entryText += " " + transcript
	}
	metadata := map[string]string{
		"name":          name,
		"blobRef":       ref,
		"mime":          meta.Mime,
		"size":          strconv.FormatInt(meta.Size, 10),
		"uploaderEmail": normalizeAccountEmail(user.Email),
		"uploaderName":  uploaderName,
		"origin":        "files",
		"brainStatus":   brainStatus,
		"visibility":    visibility,
		// ownerEmail mirrors the uploader so recall scoping (private/people)
		// and the office-event owner routing see the same principal.
		"ownerEmail": normalizeAccountEmail(user.Email),
	}
	if principal, canonical := strideE10TenantPrincipalFromContext(r.Context()); canonical {
		metadata["tenantId"] = principal.TenantID
		metadata["uploaderPersonId"] = principal.PersonID
	}
	if brainStatus == fileBrainStatusIngested {
		metadata["ingestedAt"] = now.Format(time.RFC3339Nano)
	}
	// D5 versioning: a same-name upload into the same folder by the same
	// uploader chains onto the live prior row and inherits its access/stars
	// unless this upload names its own visibility.
	prior, hasPrior := kanbanApp.priorFileVersionForUpload(r.Context(), user, name, folderID)
	if hasPrior {
		metadata["versionOf"] = prior.ID
		// Continue the chain's count past trashed/superseded versions too: a
		// re-headed v1 (its v2 in the trash) gets v3 next, never a second v2.
		metadata["version"] = strconv.Itoa(kanbanApp.fileVersionChainMaxVersion(prior) + 1)
		if requestedVisibility == "" {
			if inherited, ok := fileEntryVisibility(prior.Metadata); ok {
				metadata["visibility"] = inherited
			}
		}
		for _, key := range []string{"grants", "starredBy"} {
			if value := strings.TrimSpace(prior.Metadata[key]); value != "" {
				metadata[key] = value
			}
		}
	}
	entry, _, err := kanbanApp.memory.appendEntry(meetingMemoryKindFile, fmt.Sprintf("file-%d", now.UnixNano()), entryText, metadata)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if folderID != "" {
		if err := moveFileToFolder(entry.ID, folderID); err != nil {
			// The folder may disappear after validation. Remove the unannounced
			// row so the failed upload cannot leave a surprise root entry.
			if _, deleted, deleteErr := kanbanApp.memory.deleteEntryByID(entry.ID); deleteErr != nil || !deleted {
				log.Errorf("Upload folder assignment rollback for %s failed: %v", entry.ID, deleteErr)
			}
			status, message := fileFolderPublicError(err)
			writeAuthError(w, status, message)
			return
		}
	}
	if hasPrior {
		// The row and its prior are two store rewrites (no multi-entry
		// transaction exists). Append first so a failed stamp can never leave
		// the prior pointing at a row that does not exist; if the stamp then
		// fails, remove the just-appended row (and its folder assignment) and
		// fail the upload rather than leave two live heads in one chain.
		if _, _, err := kanbanApp.memory.updateEntryWithMetadata(meetingMemoryKindFile, prior.ID, prior.Text, map[string]string{"superseded": "true", "supersededBy": entry.ID}); err != nil {
			log.Errorf("Drive version chain stamp %s -> %s failed: %v", prior.ID, entry.ID, err)
			if folderID != "" {
				if moveErr := moveFileToFolder(entry.ID, ""); moveErr != nil {
					log.Errorf("Upload rollback folder assignment clear for %s failed: %v", entry.ID, moveErr)
				}
			}
			if _, deleted, deleteErr := kanbanApp.memory.deleteEntryByID(entry.ID); deleteErr != nil || !deleted {
				log.Errorf("Upload rollback after version chain stamp failure for %s failed: %v", entry.ID, deleteErr)
			}
			writeAuthError(w, http.StatusInternalServerError, "could not record the new file version")
			return
		}
	}
	if err := kanbanApp.publishDriveFileSourceEpisode(entry); err != nil && !errors.Is(err, ErrSourceEpisodeUnavailable) {
		log.Errorf("SourceEpisode Drive upload publication unavailable: %v", err)
	}

	row := kanbanApp.decorateFileRowForViewer(r.Context(), user, entry)
	row.FolderID = folderID
	broadcastSignedInKanbanEvent("file", row)
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"file": row,
	})
}

// saveDeliverableToFiles is the explicit-save choke point (kanban-card-110):
// it stamps a qualifying deliverable savedToFiles=true (plus who/when) so it
// surfaces on the Files list, optionally filing it under a folder in the same
// call. Both the HTTP door (/assistant/files/save) and Scout's save_to_files
// tool route through it. The gate is the full deliverable qualification —
// which subsumes the UI-state exclusion — so a successful save always surfaces
// the row instead of silently stamping a never-visible entry.
func (app *kanbanBoardApp) saveDeliverableToFiles(artifactID string, folderID string, actor string) (assistantFileRecord, error) {
	return app.saveDeliverableToFilesNamed(artifactID, folderID, "", actor)
}

// saveChatAttachmentToFiles explicitly promotes one readable chat attachment
// into Drive. The new kind=file entry owns its Drive name/folder lifecycle but
// remains source-bound: renaming or filing it never mutates the source message,
// while source revocation immediately removes its read authority.
func (app *kanbanBoardApp) saveChatAttachmentToFiles(user *userAccount, sourceFileID string, folderID string, fileName string) (assistantFileRecord, error) {
	return app.saveChatAttachmentToFilesBound(context.Background(), user, nil, sourceFileID, folderID, fileName)
}

func (app *kanbanBoardApp) saveChatAttachmentToFilesForPrincipal(ctx context.Context, user *userAccount, principal StrideE10TenantPrincipal, sourceFileID string, folderID string, fileName string) (assistantFileRecord, error) {
	return app.saveChatAttachmentToFilesBound(ctx, user, &principal, sourceFileID, folderID, fileName)
}

// saveChatAttachmentToFilesAfterAuthorizationProbe is test-only timing
// instrumentation for proving the source is checked again before a Files row
// becomes durable.
var saveChatAttachmentToFilesAfterAuthorizationProbe func()

func (app *kanbanBoardApp) saveChatAttachmentToFilesBound(ctx context.Context, user *userAccount, principal *StrideE10TenantPrincipal, sourceFileID string, folderID string, fileName string) (assistantFileRecord, error) {
	if app == nil || app.memory == nil || user == nil {
		return assistantFileRecord{}, fmt.Errorf("files are unavailable")
	}
	sourceFileID = strings.TrimSpace(sourceFileID)
	threadID, messageID, fileIndex, parsed := parseChatAttachmentFileID(sourceFileID)
	if !parsed {
		return assistantFileRecord{}, errFileSaveSourceID
	}

	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()

	// Resolve the thread metadata ACL before decoding its body, then authorize the
	// exact attachment through the current source-grant/blob authority. Saving is
	// a copy operation, so readable public-channel attachments may be promoted by
	// a teammate even when that teammate cannot mutate the message.
	var threadEntry meetingMemoryEntry
	app.memory.mu.Lock()
	for index := len(app.memory.entries) - 1; index >= 0; index-- {
		entry := &app.memory.entries[index]
		if entry.Kind != meetingMemoryKindScoutChat || entry.ID != threadID {
			continue
		}
		allowed := normalizeAccountEmail(entry.Metadata["ownerEmail"]) != "" && scoutChatThreadMetadataAllowsViewer(entry.Metadata, user.Email)
		if principal != nil {
			allowed = scoutChatThreadMetadataAllowsPrincipal(entry.Metadata, *principal)
		}
		if !allowed {
			app.memory.mu.Unlock()
			return assistantFileRecord{}, errFileSaveSourceNotFound
		}
		threadEntry = cloneMemoryEntry(*entry)
		break
	}
	app.memory.mu.Unlock()
	if threadEntry.ID == "" {
		return assistantFileRecord{}, errFileSaveSourceNotFound
	}
	thread, decoded := decodeScoutChatThreadEntry(threadEntry)
	if !decoded {
		return assistantFileRecord{}, errFileSaveSourceNotFound
	}
	var source scoutChatFileAttachment
	found := false
	for _, message := range thread.Messages {
		if message.ID != messageID || fileIndex >= len(message.Files) {
			continue
		}
		source = message.Files[fileIndex]
		found = strings.TrimSpace(source.Ref) != "" || strings.TrimSpace(source.Text) != ""
		break
	}
	if !found {
		return assistantFileRecord{}, errFileSaveSourceNotFound
	}
	authorizedSource := app.committedChatAttachmentAuthorized(user.Email, threadID, messageID, source)
	if principal != nil {
		authorizedSource = app.committedChatAttachmentAuthorizedForPrincipal(ctx, *principal, thread, messageID, source)
	}
	if !authorizedSource {
		return assistantFileRecord{}, errFileSaveSourceNotFound
	}

	name := firstNonEmptyString(strings.TrimSpace(fileName), strings.TrimSpace(source.Name), "file")
	name, err := normalizeAssistantFileName(name)
	if err != nil {
		return assistantFileRecord{}, err
	}
	folderID = strings.TrimSpace(folderID)
	folderAllowed := folderID == "" || fileFolderExists(folderID)
	if principal != nil {
		folderAllowed = folderID == "" || fileFolderManagedByPrincipal(folderID, *principal)
	}
	if !folderAllowed {
		return assistantFileRecord{}, errFileFolderNotFound
	}

	now := time.Now().UTC()
	entryID := fmt.Sprintf("file-%d", now.UnixNano())
	actorEmail := normalizeAccountEmail(user.Email)
	actorName := firstNonEmptyString(strings.TrimSpace(user.Name), actorEmail)
	brainStatus := fileBrainStatusStored
	entryText := fmt.Sprintf("File %s saved to Drive by %s.", name, actorName)
	if transcript := strings.TrimSpace(source.Text); transcript != "" {
		brainStatus = fileBrainStatusIngested
		entryText += " " + transcript
	}
	metadata := map[string]string{
		"name":               name,
		"blobRef":            strings.TrimSpace(source.Ref),
		"mime":               strings.TrimSpace(source.Mime),
		"size":               strconv.FormatInt(source.Size, 10),
		"uploaderEmail":      actorEmail,
		"uploaderName":       actorName,
		"origin":             "files",
		"brainStatus":        brainStatus,
		"sourceChatFileId":   sourceFileID,
		"sourceThreadId":     threadID,
		"sourceMessageId":    messageID,
		"sourceFileRevision": strings.TrimSpace(source.SourceRevision),
		"sourceAttachmentId": strings.TrimSpace(source.SourceID),
		// A promoted row adds no restriction beyond its source audience; the
		// source binding above is what keeps a private attachment private.
		"visibility": fileVisibilityCompany,
		"ownerEmail": actorEmail,
	}
	if principal != nil {
		metadata["tenantId"] = principal.TenantID
		metadata["uploaderPersonId"] = principal.PersonID
		ctx = context.WithValue(ctx, strideE10TenantPrincipalContextKey{}, *principal)
	}
	if brainStatus == fileBrainStatusIngested {
		metadata["ingestedAt"] = now.Format(time.RFC3339Nano)
	}
	prospective := meetingMemoryEntry{ID: entryID, Kind: meetingMemoryKindFile, CreatedAt: now, Metadata: metadata}
	if _, _, _, authorized := app.promotedChatFileSource(ctx, user, prospective); !authorized {
		return assistantFileRecord{}, errFileSaveSourceNotFound
	}
	if saveChatAttachmentToFilesAfterAuthorizationProbe != nil {
		saveChatAttachmentToFilesAfterAuthorizationProbe()
	}
	entry, _, err := app.memory.appendEntry(meetingMemoryKindFile, entryID, entryText, metadata)
	if err != nil {
		return assistantFileRecord{}, err
	}
	if _, _, _, authorized := app.promotedChatFileSource(ctx, user, entry); !authorized {
		if _, deleted, deleteErr := app.memory.deleteEntryByID(entryID); deleteErr != nil || !deleted {
			return assistantFileRecord{}, fmt.Errorf("save chat attachment source changed (rollback failed: %v)", deleteErr)
		}
		return assistantFileRecord{}, errFileSaveSourceNotFound
	}
	if folderID != "" {
		if err := moveFileToFolder(entryID, folderID); err != nil {
			// The folder may disappear after validation. Remove the unannounced
			// Drive copy so the failed operation cannot leave a surprise root row.
			if _, deleted, deleteErr := app.memory.deleteEntryByID(entryID); deleteErr != nil || !deleted {
				return assistantFileRecord{}, fmt.Errorf("assign saved chat attachment: %w (rollback failed: %v)", err, deleteErr)
			}
			return assistantFileRecord{}, err
		}
	}
	if err := app.publishDriveFileSourceEpisode(entry); err != nil && !errors.Is(err, ErrSourceEpisodeUnavailable) {
		log.Errorf("SourceEpisode promoted Drive publication unavailable: %v", err)
	}
	row := fileRecordFromEntry(entry)
	if principal != nil {
		row.UploaderEmail = ""
		row.CanDelete = true
	}
	row.FolderID = folderID
	return row, nil
}

func scoutChatThreadMetadataAllowsPrincipal(metadata map[string]string, principal StrideE10TenantPrincipal) bool {
	if metadata == nil || metadata["tenantId"] != principal.TenantID || !strideIdentifier(principal.PersonID) {
		return false
	}
	visibility := normalizeScoutChatVisibility(metadata["visibility"])
	owner := strings.TrimSpace(metadata["ownerPersonId"])
	switch visibility {
	case scoutChatVisibilityPrivate:
		return owner == principal.PersonID
	case scoutChatVisibilityPublic:
		return true
	default:
		for _, member := range strings.FieldsFunc(metadata["memberPersonIds"], func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
			if member == principal.PersonID {
				return true
			}
		}
		return false
	}
}

func (app *kanbanBoardApp) committedChatAttachmentAuthorizedForPrincipal(_ context.Context, principal StrideE10TenantPrincipal, thread scoutChatThreadRecord, messageID string, file scoutChatFileAttachment) bool {
	if app == nil || !strideIdentifier(principal.PersonID) || strings.TrimSpace(file.SourceID) == "" || strings.TrimSpace(file.Ref) == "" {
		return false
	}
	app.pendingAttachmentUploadsMu.Lock()
	grant, ok := app.pendingAttachmentUploads[strings.TrimSpace(file.SourceID)]
	storeHealthy := app.attachmentSourceStoreErr == nil
	app.pendingAttachmentUploadsMu.Unlock()
	if !storeHealthy || !ok || strings.TrimSpace(grant.OriginFileID) != "" || grant.State != attachmentSourceCommitted || grant.CommittedMessageID != strings.TrimSpace(messageID) ||
		grant.SourceRevision != strings.TrimSpace(file.SourceRevision) || grant.Ref != strings.TrimSpace(file.Ref) || grant.DestinationID != strings.TrimSpace(thread.ID) ||
		grant.DestinationRevision != scoutChatAttachmentDestinationRevision(thread) {
		return false
	}
	meta, err := blobStatForRef(grant.Ref)
	return err == nil && attachmentSourceRevision(grant.Ref, meta) == grant.SourceRevision && strings.ToLower(strings.TrimSpace(meta.Mime)) == grant.Mime && meta.Size == grant.Size
}

func (app *kanbanBoardApp) saveDeliverableToFilesNamed(artifactID string, folderID string, fileName string, actor string) (assistantFileRecord, error) {
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return assistantFileRecord{}, errFileSaveArtifactID
	}
	if app == nil || app.memory == nil {
		return assistantFileRecord{}, fmt.Errorf("files are unavailable")
	}
	entry, ok := app.osArtifactByID(artifactID)
	if !ok {
		return assistantFileRecord{}, errFileSaveNotFound
	}
	return app.saveDeliverableSnapshotToFilesNamed(entry, folderID, fileName, actor)
}

func (app *kanbanBoardApp) saveDeliverableSnapshotToFiles(entry meetingMemoryEntry, folderID string, actor string) (assistantFileRecord, error) {
	return app.saveDeliverableSnapshotToFilesNamed(entry, folderID, "", actor)
}

func (app *kanbanBoardApp) saveDeliverableSnapshotToFilesNamed(entry meetingMemoryEntry, folderID string, fileName string, actor string) (assistantFileRecord, error) {
	if app == nil || app.memory == nil {
		return assistantFileRecord{}, fmt.Errorf("files are unavailable")
	}
	artifactID := strings.TrimSpace(entry.ID)
	if artifactID == "" {
		return assistantFileRecord{}, errFileSaveArtifactID
	}
	lock := app.scoutChatThreadLock("artifact-files-" + artifactID)
	lock.Lock()
	defer lock.Unlock()
	if !deliverableRecordQualifies(entry) {
		return assistantFileRecord{}, errFileSaveNotDeliverable
	}
	folderID = strings.TrimSpace(folderID)
	if folderID != "" && !fileFolderExists(folderID) {
		return assistantFileRecord{}, errFileFolderNotFound
	}
	expectedHeader := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(entry))
	metadataUpdates := map[string]string{
		"savedToFiles":   "true",
		"savedToFilesBy": strings.TrimSpace(actor),
		"savedToFilesAt": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if strings.TrimSpace(fileName) != "" {
		normalizedName, nameErr := normalizeAssistantFileName(fileName)
		if nameErr != nil {
			return assistantFileRecord{}, nameErr
		}
		metadataUpdates["driveFileName"] = normalizedName
	}
	updated, matched, err := app.memory.updateOSArtifactMetadataIfHeaderMatches(expectedHeader, artifactID, metadataUpdates)
	if err != nil {
		if !matched {
			return assistantFileRecord{}, errFileSaveNotFound
		}
		return assistantFileRecord{}, err
	}
	if !matched {
		return assistantFileRecord{}, errFileSaveNotFound
	}
	if fileSaveAfterArtifactStampProbe != nil {
		fileSaveAfterArtifactStampProbe()
	}
	if folderID != "" {
		if err := moveFileToFolder(artifactID, folderID); err != nil {
			// A folder can disappear between validation and assignment. Restore
			// the exact prior artifact metadata conditionally before returning.
			updatedHeader := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(updated))
			_, _, _ = app.memory.restoreOSArtifactMetadataIfHeaderMatches(updatedHeader, artifactID, entry.Metadata,
				[]string{"savedToFiles", "savedToFilesBy", "savedToFilesAt", "driveFileName"})
			return assistantFileRecord{}, err
		}
	}
	row, _ := fileDeliverableRecord(updated)
	if folderID != "" {
		row.FolderID = folderID
	}
	return row, nil
}

func fileFolderExists(folderID string) bool {
	folderID = strings.TrimSpace(folderID)
	if folderID == "" {
		return true
	}
	for _, folder := range listFileFolders() {
		if folder.ID == folderID {
			return true
		}
	}
	return false
}

// fileFolderWritableFromContext prevents an otherwise-authorized Files write
// from naming a folder outside the held tenant/person authority. Legacy mode
// keeps the existing per-user ownership rule; an empty id means Files root.
func fileFolderWritableFromContext(ctx context.Context, user *userAccount, folderID string) bool {
	folderID = strings.TrimSpace(folderID)
	if folderID == "" {
		return true
	}
	if principal, canonical := strideE10TenantPrincipalFromContext(ctx); canonical {
		return fileFolderManagedByPrincipal(folderID, principal)
	}
	for _, folder := range listFileFolders() {
		if folder.ID == folderID && strings.TrimSpace(folder.TenantID) != "" {
			return false
		}
	}
	return fileFolderManagedByUser(folderID, user)
}

// fileSaveErrorStatus maps saveDeliverableToFiles errors onto honest statuses.
func fileSaveErrorStatus(err error) int {
	switch {
	case errors.Is(err, errFileSaveArtifactID), errors.Is(err, errFileSaveSourceID), errors.Is(err, errFileSaveNotDeliverable), errors.Is(err, errAssistantFileName):
		return http.StatusBadRequest
	case errors.Is(err, errFileSaveNotFound), errors.Is(err, errFileSaveSourceNotFound):
		return http.StatusNotFound
	case errors.Is(err, errFileFolderNotFound), errors.Is(err, errFileFolderDuplicate):
		return fileFolderErrorStatus(err)
	default:
		return http.StatusInternalServerError
	}
}

// assistantFileSaveHandler serves POST /assistant/files/save — the explicit
// save door (kanban-card-110). Same gate stack as assistantFileMoveHandler
// (method, origin, session, app, MaxBytesReader). It accepts either a terminal
// {artifactId} or a readable chat {sourceFileId}, plus fileName/folderId.
func assistantFileSaveHandler(w http.ResponseWriter, r *http.Request) {
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
		writeAuthError(w, http.StatusServiceUnavailable, "files are unavailable")
		return
	}
	if !strideE10TenantSurfaceUseBound(r.Context(), StrideE10TenantSurfaceDrive) {
		err := withStrideE10TenantRequestUse(r, StrideE10TenantSurfaceDrive, func(ctx context.Context, _ *StrideE10TenantPrincipal) error {
			assistantFileSaveHandler(w, r.WithContext(ctx))
			return nil
		})
		if err != nil {
			writeStrideE10TenantHookError(w, err, "files are unavailable")
		}
		return
	}

	payload := struct {
		ArtifactID   string `json:"artifactId"`
		SourceFileID string `json:"sourceFileId"`
		FolderID     string `json:"folderId"`
		FileName     string `json:"fileName"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read save request")
		return
	}
	payload.ArtifactID = strings.TrimSpace(payload.ArtifactID)
	payload.SourceFileID = strings.TrimSpace(payload.SourceFileID)
	if payload.ArtifactID == "" && payload.SourceFileID == "" {
		writeAuthError(w, http.StatusBadRequest, errFileSaveArtifactID.Error())
		return
	}
	var row assistantFileRecord
	var err error
	if payload.SourceFileID != "" {
		if principal, canonical := strideE10TenantPrincipalFromContext(r.Context()); canonical {
			row, err = kanbanApp.saveChatAttachmentToFilesForPrincipal(r.Context(), user, principal, payload.SourceFileID, payload.FolderID, payload.FileName)
		} else {
			row, err = kanbanApp.saveChatAttachmentToFiles(user, payload.SourceFileID, payload.FolderID, payload.FileName)
		}
	} else {
		artifact, ok := authorizedArtifactForActions(r.Context(), user, payload.ArtifactID, ACLReadContent, ACLWrite)
		if !ok {
			writeAuthError(w, http.StatusNotFound, "artifact not found")
			return
		}
		if !fileFolderWritableFromContext(r.Context(), user, payload.FolderID) {
			writeAuthError(w, fileFolderErrorStatus(errFileFolderNotFound), errFileFolderNotFound.Error())
			return
		}
		actor := firstNonEmptyString(strings.TrimSpace(user.Name), normalizeAccountEmail(user.Email))
		row, err = kanbanApp.saveDeliverableSnapshotToFilesNamed(artifact, payload.FolderID, payload.FileName, actor)
	}
	if err != nil {
		status := fileSaveErrorStatus(err)
		message := err.Error()
		if status == http.StatusServiceUnavailable || status == http.StatusInternalServerError {
			log.Errorf("Save deliverable to Files failed: %v", err)
			message = "files are unavailable"
		}
		writeAuthError(w, status, message)
		return
	}
	broadcastSignedInKanbanEvent("file", row)
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"fileId": row.ID,
		"file":   row,
	})
}
