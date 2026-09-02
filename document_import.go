package main

// document_import.go opens an uploaded Markdown / plain-text Drive file in
// Document Studio. The file is read through the SAME authorization seam chat
// attachments use (assistantFileAttachmentSource: artifact ACL for
// deliverables, tenant/promotion checks for uploads), its bytes become the
// body of a brand-new document artifact minted by the blank-create path, and
// the original file is left untouched.

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

var documentImportTextMimes = map[string]bool{
	"text/markdown":   true,
	"text/x-markdown": true,
	"text/plain":      true,
}

// documentImportTitle derives the new document's title from the file name:
// extension dropped, whitespace collapsed, bounded like every studio title.
func documentImportTitle(fileName string) string {
	name := strings.TrimSpace(filepath.Base(strings.ReplaceAll(fileName, "\\", "/")))
	if extension := filepath.Ext(name); extension != "" && len(extension) <= 12 {
		name = strings.TrimSuffix(name, extension)
	}
	name = strings.Join(strings.Fields(strings.NewReplacer("_", " ").Replace(name)), " ")
	if runes := []rune(name); len(runes) > 160 {
		name = strings.TrimSpace(string(runes[:160]))
	}
	return firstNonEmptyString(name, "Imported document")
}

// documentImportAccessMetadata carries the SOURCE's reach onto the imported
// document instead of the blank-create default ("organization"). Artifacts
// know two reaches — private (owner only) and organization — so a Drive
// upload that is private or shared with people lands as a PRIVATE document
// owned by the importer (the one principal proven able to read the source),
// with the source's uploader, visibility and grants recorded as provenance; a
// company upload stays organization-visible. A saved deliverable carries its
// own artifact visibility; a chat attachment follows its thread (public
// channel → organization, anything else → private). Fails closed: an
// unresolvable source yields a private document.
func (app *kanbanBoardApp) documentImportAccessMetadata(ctx context.Context, user *userAccount, fileID string) map[string]string {
	owner := ""
	if user != nil {
		owner = normalizeAccountEmail(user.Email)
	}
	private := map[string]string{"visibility": "private", "ownerEmail": owner}
	if app == nil || app.memory == nil || user == nil {
		return private
	}
	fileID = strings.TrimSpace(fileID)
	if entry, found := app.memory.entryByKindAndID(meetingMemoryKindFile, fileID); found {
		out := map[string]string{
			"ownerEmail":                owner,
			"importedFromUploaderEmail": normalizeAccountEmail(entry.Metadata["uploaderEmail"]),
		}
		visibility, ok := fileEntryVisibility(entry.Metadata)
		if !ok {
			visibility = "unknown"
		}
		out["importedFromVisibility"] = visibility
		if grants := strings.Join(fileGrantEmails(entry.Metadata), ","); grants != "" {
			out["importedFromGrants"] = grants
		}
		if visibility == fileVisibilityCompany {
			out["visibility"] = "organization"
		} else {
			out["visibility"] = "private"
		}
		return out
	}
	if artifact, ok := app.authorizedArtifactForActions(ctx, user, fileID, ACLReadContent); ok {
		if legacyArtifactIsPrivate(artifact) {
			return private
		}
		return map[string]string{"visibility": "organization", "ownerEmail": owner}
	}
	if threadID, _, _, parsed := parseChatAttachmentFileID(fileID); parsed {
		if thread, _, err := app.scoutChatThreadByID(user.Email, threadID); err == nil && scoutChatThreadIsOrganizationPublic(thread) {
			return map[string]string{"visibility": "organization", "ownerEmail": owner}
		}
	}
	return private
}

// documentEditorImportHandler POST /artifacts/document/import {fileId} →
// 201 {ok, id, artifact, document}. 404 for a file the caller cannot read
// (non-oracular, like every artifact route), 415 for a non-text file, 413
// above the 1MB editing bound.
func documentEditorImportHandler(w http.ResponseWriter, r *http.Request) {
	user := studioBlankCreateUser(w, r)
	if user == nil {
		return
	}
	if !strideE10TenantSurfaceUseBound(r.Context(), StrideE10TenantSurfaceDrive) {
		err := withStrideE10TenantRequestUse(r, StrideE10TenantSurfaceDrive, func(ctx context.Context, _ *StrideE10TenantPrincipal) error {
			documentEditorImportHandler(w, r.WithContext(ctx))
			return nil
		})
		if err != nil {
			writeStrideE10TenantHookError(w, err, "document import is unavailable")
		}
		return
	}
	payload := struct {
		FileID string `json:"fileId"`
		Title  string `json:"title"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read the import request")
		return
	}
	payload.FileID = strings.TrimSpace(payload.FileID)
	payload.Title = strings.Join(strings.Fields(strings.TrimSpace(payload.Title)), " ")
	if payload.FileID == "" || len([]rune(payload.Title)) > 160 {
		writeAuthError(w, http.StatusBadRequest, "fileId is required")
		return
	}
	file, meta, _, ok := kanbanApp.assistantFileAttachmentSource(r.Context(), user, payload.FileID)
	if !ok || !validBlobRef(file.Ref) {
		writeAuthError(w, http.StatusNotFound, "file not found")
		return
	}
	fileMime := strings.ToLower(strings.TrimSpace(strings.Split(meta.Mime, ";")[0]))
	if !documentImportTextMimes[fileMime] {
		writeAuthError(w, http.StatusUnsupportedMediaType, "only Markdown (.md) and plain-text (.txt) files can open in Document Studio")
		return
	}
	if meta.Size < 1 || meta.Size > documentStudioMaxBytes {
		writeAuthError(w, http.StatusRequestEntityTooLarge, "the file exceeds the 1MB document editing bound")
		return
	}
	data, _, err := getBlob(file.Ref)
	if err != nil {
		writeAuthError(w, http.StatusNotFound, "file not found")
		return
	}
	if len(data) == 0 || len(data) > documentStudioMaxBytes || !utf8.Valid(data) || bytesContainsNUL(data) {
		writeAuthError(w, http.StatusUnsupportedMediaType, "the file is not UTF-8 text")
		return
	}
	markdown := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\r", "\n")
	markdown = strings.TrimPrefix(markdown, "\uFEFF")
	if err := validateDocumentStudioDocument(documentStudioDocument{SchemaVersion: 1, Markdown: markdown}); err != nil {
		writeAuthError(w, http.StatusUnsupportedMediaType, err.Error())
		return
	}
	title := firstNonEmptyString(payload.Title, documentImportTitle(file.Name))
	extra := map[string]string{
		"importedFromFileId": payload.FileID, "importedFromFileName": strings.TrimSpace(file.Name), "importedFromBlobRef": file.Ref,
	}
	for key, value := range kanbanApp.documentImportAccessMetadata(r.Context(), user, payload.FileID) {
		extra[key] = value
	}
	entry, err := createDocumentStudioArtifact(user, title, markdown, extra)
	if err != nil {
		log.Errorf("Document import create failed: %v", err)
		writeAuthError(w, http.StatusInternalServerError, "the document could not be created")
		return
	}
	writeAuthJSON(w, http.StatusCreated, map[string]any{
		"ok": true, "id": entry.ID, "artifact": documentStudioView(entry),
		"document": documentStudioDocumentFromEntry(entry),
	})
}
