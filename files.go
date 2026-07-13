package main

// Files surface (card 095) — the Google-Drive-like door over everything the
// team has uploaded. One list, three sources:
//
//  1. Direct uploads: POST /assistant/files/upload stores the bytes through
//     putBlob and appends a first-class kind=file memory entry whose Text is
//     the file's name + the 085 derived transcript, so a direct upload feeds
//     answer_memory_question exactly like a chat upload feeds thread context.
//  2. Chat attachments: GET /assistant/files adapts the scoutChatFileAttachment
//     records 085 already persists inside thread messages — no double-write,
//     and thread visibility (private vs public channel) keeps governing who
//     sees which files.
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
)

var (
	errFileSaveArtifactID     = errors.New("artifactId is required")
	errFileSaveNotFound       = errors.New("artifact not found")
	errFileSaveNotDeliverable = errors.New("only a finished deliverable can be saved to Files")
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
func fileRecordsFromThread(thread scoutChatThreadRecord) []assistantFileRecord {
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
// grandfather migration stamps exactly the entries that pass this, and
// fileDeliverableRecord layers the savedToFiles gate on top.
func deliverableRecordQualifies(entry meetingMemoryEntry) bool {
	metadata := entry.Metadata
	if metadata == nil {
		return false
	}
	if strings.TrimSpace(metadata["source"]) != "scout_thread" &&
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

	name := firstNonEmptyString(strings.TrimSpace(metadata["title"]), strings.TrimSpace(metadata["threadQuery"]), "Deliverable")
	deliverableMime := "text/markdown"
	if artifactType(entry) == artifactTypeHTMLDeck {
		deliverableMime = "text/html"
	}
	createdAt := ""
	if !entry.CreatedAt.IsZero() {
		createdAt = entry.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	return assistantFileRecord{
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
	}, true
}

// assistantFilesForUser assembles the viewer's file list: every direct upload
// (team-wide, like a shared drive), the chat attachments the viewer may
// read — their own threads and public channels, the same visibility law
// scoutChatThreadsSnapshot already enforces — plus the finished agent
// deliverables. Newest first, capped after the merge.
func (app *kanbanBoardApp) assistantFilesForUser(viewerEmail string) []assistantFileRecord {
	return app.assistantFilesForPrincipal(context.Background(), &userAccount{Email: normalizeAccountEmail(viewerEmail)})
}

func (app *kanbanBoardApp) assistantFilesForPrincipal(ctx context.Context, viewer *userAccount) []assistantFileRecord {
	if app == nil || app.memory == nil {
		return nil
	}
	viewerEmail := ""
	if viewer != nil {
		viewerEmail = viewer.Email
	}
	rows := make([]assistantFileRecord, 0, 32)
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindFile, 0) {
		rows = append(rows, fileRecordFromEntry(entry))
	}
	for _, thread := range app.scoutChatThreadsSnapshot(viewerEmail, true, 0) {
		rows = append(rows, fileRecordsFromThread(thread)...)
	}
	for _, entry := range app.authorizedFileDeliverableCandidates(ctx, viewer, ACLReadContent) {
		if row, ok := fileDeliverableRecord(entry); ok {
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
	if r.Method != http.MethodGet {
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
		writeAuthError(w, http.StatusServiceUnavailable, "files are unavailable")
		return
	}

	rows := kanbanApp.assistantFilesForPrincipal(r.Context(), user)
	folders := decorateAssistantFileFolders(rows)
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"files":   rows,
		"folders": folders,
	})
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
	if currentAnthropicAPIKey() != "" && attachmentModelSafeMimes[meta.Mime] {
		blocks := attachmentContentBlocks(files)
		files = deriveAttachmentText(r.Context(), files, blocks)
	}
	transcript := strings.TrimSpace(files[0].Text)
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
	if brainStatus == fileBrainStatusIngested {
		metadata["ingestedAt"] = now.Format(time.RFC3339Nano)
	}
	entry, _, err := kanbanApp.memory.appendEntry(meetingMemoryKindFile, fmt.Sprintf("file-%d", now.UnixNano()), entryText, metadata)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}

	row := fileRecordFromEntry(entry)
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
	return app.saveDeliverableSnapshotToFiles(entry, folderID, actor)
}

func (app *kanbanBoardApp) saveDeliverableSnapshotToFiles(entry meetingMemoryEntry, folderID string, actor string) (assistantFileRecord, error) {
	artifactID := strings.TrimSpace(entry.ID)
	if artifactID == "" {
		return assistantFileRecord{}, errFileSaveArtifactID
	}
	if !deliverableRecordQualifies(entry) {
		return assistantFileRecord{}, errFileSaveNotDeliverable
	}
	folderID = strings.TrimSpace(folderID)
	if folderID != "" && !fileFolderExists(folderID) {
		return assistantFileRecord{}, errFileFolderNotFound
	}
	expectedHeader := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(entry))
	updated, matched, err := app.memory.updateOSArtifactMetadataIfHeaderMatches(expectedHeader, artifactID, map[string]string{
		"savedToFiles":   "true",
		"savedToFilesBy": strings.TrimSpace(actor),
		"savedToFilesAt": time.Now().UTC().Format(time.RFC3339Nano),
	})
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
				[]string{"savedToFiles", "savedToFilesBy", "savedToFilesAt"})
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
	case errors.Is(err, errFileSaveArtifactID), errors.Is(err, errFileSaveNotDeliverable):
		return http.StatusBadRequest
	case errors.Is(err, errFileSaveNotFound):
		return http.StatusNotFound
	case errors.Is(err, errFileFolderNotFound), errors.Is(err, errFileFolderDuplicate):
		return fileFolderErrorStatus(err)
	default:
		return http.StatusInternalServerError
	}
}

// assistantFileSaveHandler serves POST /assistant/files/save — the explicit
// save door (kanban-card-110). Same gate stack as assistantFileMoveHandler
// (method, origin, session, app, MaxBytesReader); body {artifactId, folderId?}.
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

	payload := struct {
		ArtifactID string `json:"artifactId"`
		FolderID   string `json:"folderId"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read save request")
		return
	}
	payload.ArtifactID = strings.TrimSpace(payload.ArtifactID)
	if payload.ArtifactID == "" {
		writeAuthError(w, http.StatusBadRequest, errFileSaveArtifactID.Error())
		return
	}
	artifact, ok := authorizedArtifactForActions(r.Context(), user, payload.ArtifactID, ACLReadContent, ACLWrite)
	if !ok {
		writeAuthError(w, http.StatusNotFound, "artifact not found")
		return
	}
	actor := firstNonEmptyString(strings.TrimSpace(user.Name), normalizeAccountEmail(user.Email))
	row, err := kanbanApp.saveDeliverableSnapshotToFiles(artifact, payload.FolderID, actor)
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
