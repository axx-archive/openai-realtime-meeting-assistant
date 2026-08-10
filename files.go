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
	return assistantFileRecord{
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
	}
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
// own stamps (goalPlan on the parent, goalDeliverable on a flagged child); the
// status must be terminally good (complete/published — running scaffolds and
// error/needs_attention bodies never qualify); and UI-state-ish artifacts
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
	if strings.TrimSpace(metadata["source"]) != "scout_thread" &&
		strings.TrimSpace(metadata["source"]) != "chat_image" &&
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
	if strings.TrimSpace(metadata["source"]) == "chat_image" {
		assets := artifactAssets(entry)
		for index := range assets {
			if assets[index].Kind == "image" && validBlobRef(assets[index].Ref) {
				asset := assets[index]
				imageAsset = &asset
				break
			}
		}
		if imageAsset != nil {
			name = firstNonEmptyString(strings.TrimSpace(imageAsset.Name), name)
			deliverableMime = firstNonEmptyString(strings.TrimSpace(imageAsset.Mime), "image/png")
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
	if app == nil || app.memory == nil {
		return nil
	}
	rows := make([]assistantFileRecord, 0, 32)
	principal, canonical := strideE10TenantPrincipalFromContext(ctx)
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindFile, 0) {
		if canonical && strings.TrimSpace(entry.Metadata["tenantId"]) != principal.TenantID {
			continue
		}
		row := fileRecordFromEntry(entry)
		if canonical {
			row.CanDelete = strings.TrimSpace(entry.Metadata["uploaderPersonId"]) == principal.PersonID
			row.UploaderEmail = ""
		} else {
			row.CanDelete = viewer != nil && (isArtifactApprovalAdmin(viewer) || normalizeAccountEmail(row.UploaderEmail) == normalizeAccountEmail(viewer.Email))
		}
		rows = append(rows, row)
	}
	for _, entry := range app.authorizedFileDeliverableCandidates(ctx, viewer, ACLReadContent) {
		if row, ok := fileDeliverableRecord(entry); ok {
			_, row.CanDelete = authorizedArtifactForActions(ctx, viewer, entry.ID, ACLReadContent, ACLWrite)
			rows = append(rows, row)
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
		row, visible := fileDeliverableRecord(artifact)
		if !visible {
			return scoutChatFileAttachment{}, blobMeta{}, "", false
		}
		ref := ""
		for _, preferredKind := range []string{"pdf", "image", "export"} {
			for _, asset := range artifactAssets(artifact) {
				if strings.EqualFold(strings.TrimSpace(asset.Kind), preferredKind) && validBlobRef(asset.Ref) {
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
		row := fileRecordFromEntry(entry)
		ref := strings.TrimSpace(entry.Metadata["blobRef"])
		meta, err := blobStatForRef(ref)
		if err != nil {
			return scoutChatFileAttachment{}, blobMeta{}, "", false
		}
		revision, err := STRIDEContractDigest(struct {
			FileID string `json:"fileId"`
			Ref    string `json:"ref"`
			Mime   string `json:"mime"`
			Size   int64  `json:"size"`
		}{fileID, ref, strings.ToLower(strings.TrimSpace(meta.Mime)), meta.Size})
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
	// Direct uploads are the existing shared-company Drive contract.
	if _, found := app.memory.entryByKindAndID(meetingMemoryKindFile, fileID); found {
		return true
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
		assistantFileRename(w, r, user)
		return
	}

	rows := kanbanApp.assistantFilesForPrincipal(r.Context(), user)
	folders := []assistantFileFolderPayload{}
	if principal, canonical := strideE10TenantPrincipalFromContext(r.Context()); canonical {
		folders = decorateAssistantFileFoldersForTenant(rows, principal.TenantID)
	} else {
		folders = decorateAssistantFileFolders(rows)
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"files":   rows,
		"folders": folders,
	})
}

func assistantFileRename(w http.ResponseWriter, r *http.Request, user *userAccount) {
	payload := struct {
		ID     string `json:"id"`
		FileID string `json:"fileId"`
		Name   string `json:"name"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read rename request")
		return
	}
	fileID := firstNonEmptyString(strings.TrimSpace(payload.ID), strings.TrimSpace(payload.FileID))
	if fileID == "" {
		writeAuthError(w, http.StatusBadRequest, errFileFolderFileID.Error())
		return
	}
	name, err := normalizeAssistantFileName(payload.Name)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	row, err := kanbanApp.renameAssistantFileForUser(r.Context(), user, fileID, name)
	if err != nil {
		if errors.Is(err, errFileSaveNotFound) {
			writeAuthError(w, http.StatusNotFound, "file not found")
			return
		}
		log.Errorf("Rename Drive file %s failed: %v", fileID, err)
		writeAuthError(w, http.StatusInternalServerError, "could not rename the file")
		return
	}
	broadcastSignedInKanbanEvent("file", map[string]any{"kind": "renamed", "file": row})
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "file": row})
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
		var deleted bool
		_, deleted, err = app.memory.deleteEntryByID(fileID)
		if err == nil && !deleted {
			err = errFileSaveNotFound
		}
	}
	if err != nil {
		return "", err
	}
	// Folder assignments are projections. A persistence failure here can only
	// leave a harmless dangling id, so it must not resurrect the removed source.
	if err := moveFileToFolder(fileID, ""); err != nil {
		log.Errorf("Clear deleted Drive file folder assignment %s failed: %v", fileID, err)
	}
	broadcastSignedInKanbanEvent("file", map[string]any{"kind": "deleted", "fileId": fileID})
	return mode, nil
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
	thread.Messages[messageIndex].Files = append(thread.Messages[messageIndex].Files[:fileIndex], thread.Messages[messageIndex].Files[fileIndex+1:]...)
	thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := app.saveScoutChatThread(thread); err != nil {
		return err
	}
	if strings.TrimSpace(file.SourceID) != "" {
		if err := app.revokeAttachmentSource(file.SourceID); err != nil {
			log.Errorf("Revoke deleted Drive chat attachment source %s failed: %v", file.SourceID, err)
		}
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
	}
	if principal, canonical := strideE10TenantPrincipalFromContext(r.Context()); canonical {
		metadata["tenantId"] = principal.TenantID
		metadata["uploaderPersonId"] = principal.PersonID
	}
	if brainStatus == fileBrainStatusIngested {
		metadata["ingestedAt"] = now.Format(time.RFC3339Nano)
	}
	entry, _, err := kanbanApp.memory.appendEntry(meetingMemoryKindFile, fmt.Sprintf("file-%d", now.UnixNano()), entryText, metadata)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}

	row := fileRecordFromEntry(entry)
	if _, canonical := strideE10TenantPrincipalFromContext(r.Context()); canonical {
		row.UploaderEmail = ""
		row.CanDelete = true
	}
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
// points at the same immutable blob, so renaming or deleting it never mutates
// the source message.
func (app *kanbanBoardApp) saveChatAttachmentToFiles(user *userAccount, sourceFileID string, folderID string, fileName string) (assistantFileRecord, error) {
	return app.saveChatAttachmentToFilesBound(context.Background(), user, nil, sourceFileID, folderID, fileName)
}

func (app *kanbanBoardApp) saveChatAttachmentToFilesForPrincipal(ctx context.Context, user *userAccount, principal StrideE10TenantPrincipal, sourceFileID string, folderID string, fileName string) (assistantFileRecord, error) {
	return app.saveChatAttachmentToFilesBound(ctx, user, &principal, sourceFileID, folderID, fileName)
}

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
	}
	if principal != nil {
		metadata["tenantId"] = principal.TenantID
		metadata["uploaderPersonId"] = principal.PersonID
	}
	if brainStatus == fileBrainStatusIngested {
		metadata["ingestedAt"] = now.Format(time.RFC3339Nano)
	}
	entry, _, err := app.memory.appendEntry(meetingMemoryKindFile, entryID, entryText, metadata)
	if err != nil {
		return assistantFileRecord{}, err
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
		"ok":   true,
		"file": row,
	})
}
