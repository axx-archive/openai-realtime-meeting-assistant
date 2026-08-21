package main

// document_editor.go owns the native Document Studio boundary. Markdown stays
// the canonical persisted source so existing renderers, exports, search, and
// company memory keep one source of truth. The browser may project it into
// editable blocks, but saves always return a complete bounded Markdown body.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

const documentStudioMaxBytes = 1 << 20

type documentStudioDocument struct {
	SchemaVersion int    `json:"schemaVersion"`
	Markdown      string `json:"markdown"`
}

type documentStudioArtifactView struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Type         string `json:"type"`
	Version      int    `json:"version"`
	UpdatedAt    string `json:"updatedAt,omitempty"`
	SavedToFiles bool   `json:"savedToFiles"`
}

func documentStudioView(entry meetingMemoryEntry) documentStudioArtifactView {
	return documentStudioArtifactView{
		ID: entry.ID, Title: strings.TrimSpace(entry.Metadata["title"]), Type: artifactType(entry),
		Version: artifactVersion(entry), UpdatedAt: strings.TrimSpace(entry.Metadata["updatedAt"]),
		SavedToFiles: strings.EqualFold(strings.TrimSpace(entry.Metadata["savedToFiles"]), "true"),
	}
}

func artifactIsDocumentStudioDocument(entry meetingMemoryEntry) bool {
	return strings.TrimSpace(entry.ID) != "" && artifactType(entry) == artifactTypeMarkdown
}

func validateDocumentStudioDocument(doc documentStudioDocument) error {
	if doc.SchemaVersion != 1 {
		return fmt.Errorf("document must use schemaVersion 1")
	}
	if len([]byte(doc.Markdown)) > documentStudioMaxBytes {
		return fmt.Errorf("document exceeds the 1MB editing bound")
	}
	if !utf8.ValidString(doc.Markdown) || strings.IndexByte(doc.Markdown, 0) >= 0 {
		return fmt.Errorf("document contains invalid text")
	}
	return nil
}

func documentEditorHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPatch {
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
		writeAuthError(w, http.StatusServiceUnavailable, "artifacts are unavailable")
		return
	}

	if r.Method == http.MethodGet {
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		artifact, ok := authorizedArtifactForActions(r.Context(), user, id, ACLReadContent)
		if !ok || !artifactIsDocumentStudioDocument(artifact) {
			writeAuthError(w, http.StatusNotFound, "document artifact not found")
			return
		}
		_, canWrite := authorizedArtifactForActions(r.Context(), user, id, ACLReadContent, ACLWrite)
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok": true, "artifact": documentStudioView(artifact),
			"document": documentStudioDocument{SchemaVersion: 1, Markdown: artifact.Text}, "canWrite": canWrite,
		})
		return
	}

	payload := struct {
		ArtifactID      string                 `json:"artifactId"`
		ExpectedVersion int                    `json:"expectedVersion"`
		Title           string                 `json:"title"`
		Document        documentStudioDocument `json:"document"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, documentStudioMaxBytes+64<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read document update")
		return
	}
	payload.ArtifactID = strings.TrimSpace(payload.ArtifactID)
	payload.Title = strings.TrimSpace(payload.Title)
	if len([]rune(payload.Title)) > 160 || validateDocumentStudioDocument(payload.Document) != nil {
		writeAuthError(w, http.StatusBadRequest, "document title or body is invalid")
		return
	}
	prior, ok := authorizedArtifactForActions(r.Context(), user, payload.ArtifactID, ACLReadContent, ACLWrite)
	if !ok || !artifactIsDocumentStudioDocument(prior) {
		writeAuthError(w, http.StatusNotFound, "document artifact not found")
		return
	}
	if payload.ExpectedVersion < 1 || artifactVersion(prior) != payload.ExpectedVersion {
		writeDocumentVersionConflict(w, prior)
		return
	}
	title := firstNonEmptyString(payload.Title, strings.TrimSpace(prior.Metadata["title"]), "Untitled document")
	header := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(prior))
	var updated meetingMemoryEntry
	var changed bool
	err := kanbanApp.withCurrentAgentThreadSource(scoutAgentThread{Artifact: prior}, func() error {
		var updateErr error
		updated, changed, updateErr = kanbanApp.memory.updateOSArtifactWithMetadataIfHeaderMatches(
			header, prior.ID, title, payload.Document.Markdown, user.Name,
			map[string]string{"type": artifactTypeMarkdown, "documentSchemaVersion": "1"},
		)
		return updateErr
	})
	if err != nil {
		writeDocumentVersionConflict(w, prior)
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok": true, "updated": changed, "artifact": documentStudioView(updated),
		"document": documentStudioDocument{SchemaVersion: 1, Markdown: updated.Text},
	})
}

func documentEditorCopyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil || kanbanApp == nil || kanbanApp.memory == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if !strideE10TenantSurfaceUseBound(r.Context(), StrideE10TenantSurfaceDrive) {
		err := withStrideE10TenantRequestUse(r, StrideE10TenantSurfaceDrive, func(ctx context.Context, _ *StrideE10TenantPrincipal) error {
			documentEditorCopyHandler(w, r.WithContext(ctx))
			return nil
		})
		if err != nil {
			writeStrideE10TenantHookError(w, err, "document copy is unavailable")
		}
		return
	}
	payload := struct {
		ArtifactID      string                 `json:"artifactId"`
		ExpectedVersion int                    `json:"expectedVersion"`
		Title           string                 `json:"title"`
		FileName        string                 `json:"fileName"`
		FolderID        string                 `json:"folderId"`
		Document        documentStudioDocument `json:"document"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, documentStudioMaxBytes+64<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read document copy")
		return
	}
	payload.ArtifactID = strings.TrimSpace(payload.ArtifactID)
	payload.Title = strings.TrimSpace(payload.Title)
	payload.FileName = strings.TrimSpace(payload.FileName)
	payload.FolderID = strings.TrimSpace(payload.FolderID)
	fileName, fileNameErr := normalizeAssistantFileName(payload.FileName)
	if payload.Title == "" || len([]rune(payload.Title)) > 160 || fileNameErr != nil || validateDocumentStudioDocument(payload.Document) != nil {
		writeAuthError(w, http.StatusBadRequest, "a valid document name and Files destination are required")
		return
	}
	prior, ok := authorizedArtifactForActions(r.Context(), user, payload.ArtifactID, ACLReadContent, ACLWrite)
	if !ok || !artifactIsDocumentStudioDocument(prior) {
		writeAuthError(w, http.StatusNotFound, "document artifact not found")
		return
	}
	if artifactVersion(prior) != payload.ExpectedVersion {
		writeDocumentVersionConflict(w, prior)
		return
	}
	if !fileFolderWritableFromContext(r.Context(), user, payload.FolderID) {
		writeAuthError(w, http.StatusNotFound, errFileFolderNotFound.Error())
		return
	}
	metadata := map[string]string{
		"title": payload.Title, "type": artifactTypeMarkdown, "source": "scout_thread",
		"status": artifactStatusComplete, "threadStatus": artifactStatusComplete,
		"copiedFromArtifactId": prior.ID, "copiedFromArtifactVersion": strconv.Itoa(artifactVersion(prior)),
		"documentSchemaVersion": "1", "tenantId": strings.TrimSpace(prior.Metadata["tenantId"]),
		"visibility": firstNonEmptyString(strings.TrimSpace(prior.Metadata["visibility"]), "organization"),
		"ownerEmail": normalizeAccountEmail(user.Email),
	}
	copyEntry, appended, err := kanbanApp.createOSArtifactWithMetadata("artifacts", payload.Title, payload.Document.Markdown, firstNonEmptyString(user.Name, user.Email), metadata)
	if err != nil || !appended {
		writeAuthError(w, http.StatusInternalServerError, "document copy could not be created")
		return
	}
	file, err := kanbanApp.saveDeliverableSnapshotToFilesNamed(copyEntry, payload.FolderID, fileName, firstNonEmptyString(user.Name, user.Email))
	if err != nil {
		writeAuthError(w, fileSaveErrorStatus(err), "document copy was created, but Files is unavailable")
		return
	}
	stored, _ := kanbanApp.osArtifactByID(copyEntry.ID)
	broadcastSignedInKanbanEvent("file", file)
	writeAuthJSON(w, http.StatusCreated, map[string]any{"ok": true, "artifact": documentStudioView(stored), "document": payload.Document, "file": file})
}

func writeDocumentVersionConflict(w http.ResponseWriter, artifact meetingMemoryEntry) {
	writeAuthJSON(w, http.StatusConflict, map[string]any{
		"error": "document revision changed; reload or save a copy", "artifactId": artifact.ID,
		"currentVersion": artifactVersion(artifact),
	})
}
