package main

// Files surface (card 095) — the Google-Drive-like door over everything the
// team has uploaded. One list, two sources:
//
//  1. Direct uploads: POST /assistant/files/upload stores the bytes through
//     putBlob and appends a first-class kind=file memory entry whose Text is
//     the file's name + the 085 derived transcript, so a direct upload feeds
//     answer_memory_question exactly like a chat upload feeds thread context.
//  2. Chat attachments: GET /assistant/files adapts the scoutChatFileAttachment
//     records 085 already persists inside thread messages — no double-write,
//     and thread visibility (private vs public channel) keeps governing who
//     sees which files.
//
// Every row is decorated for the client with the session-gated blob download
// URL (/artifacts/blob, blobs.go) plus the honest feeds-the-brain badge:
// "ingested" when derived/extracted text rides model context, "stored" when
// only the bytes are durable (keyless deploys, non-model mimes).

import (
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

// assistantFilesForUser assembles the viewer's file list: every direct upload
// (team-wide, like a shared drive) plus the chat attachments the viewer may
// read — their own threads and public channels, the same visibility law
// scoutChatThreadsSnapshot already enforces. Newest first.
func (app *kanbanBoardApp) assistantFilesForUser(viewerEmail string) []assistantFileRecord {
	if app == nil || app.memory == nil {
		return nil
	}
	rows := make([]assistantFileRecord, 0, 32)
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindFile, 0) {
		rows = append(rows, fileRecordFromEntry(entry))
	}
	for _, thread := range app.scoutChatThreadsSnapshot(viewerEmail, true, 0) {
		rows = append(rows, fileRecordsFromThread(thread)...)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return fileRecordTime(rows[i]).After(fileRecordTime(rows[j]))
	})
	if len(rows) > assistantFilesListLimit {
		rows = rows[:assistantFilesListLimit]
	}
	return rows
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

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"files": kanbanApp.assistantFilesForUser(user.Email),
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
