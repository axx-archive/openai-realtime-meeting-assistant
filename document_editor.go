package main

// document_editor.go owns the native Document Studio boundary. Markdown stays
// the canonical persisted source so existing renderers, exports, search, and
// company memory keep one source of truth. The browser may project it into
// editable blocks, but saves always return a complete bounded Markdown body.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	documentStudioMaxBytes      = 1 << 20
	documentStudioImageMaxBytes = 16 << 20
)

const (
	documentStudioEmptyMetadataKey = "documentEmpty"
	// The shared artifact store deliberately rejects blank bodies. Document
	// Studio represents an intentional empty document with an invisible body
	// plus an explicit marker, then projects it back to an empty Markdown string
	// at this boundary. The marker prevents a genuine zero-width character in a
	// legacy document from being mistaken for an empty document.
	documentStudioEmptyBodySentinel = "\u200b"
)

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

func documentStudioDocumentFromEntry(entry meetingMemoryEntry) documentStudioDocument {
	markdown := entry.Text
	if strings.EqualFold(strings.TrimSpace(entry.Metadata[documentStudioEmptyMetadataKey]), "true") && markdown == documentStudioEmptyBodySentinel {
		markdown = ""
	}
	return documentStudioDocument{SchemaVersion: 1, Markdown: markdown}
}

func documentStudioStoredBody(markdown string) (string, string) {
	if markdown == "" {
		return documentStudioEmptyBodySentinel, "true"
	}
	return markdown, "false"
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
		qualityState, canExport, stable := kanbanApp.authoredResultFinalExportState(artifact)
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok": true, "artifact": documentStudioView(artifact),
			"document": documentStudioDocumentFromEntry(artifact), "canWrite": canWrite,
			"qualityState": qualityState, "canExport": stable && canExport,
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
	storedBody, emptyMarker := documentStudioStoredBody(payload.Document.Markdown)
	var updated meetingMemoryEntry
	var changed bool
	err := kanbanApp.withCurrentAgentThreadSource(scoutAgentThread{Artifact: prior}, func() error {
		var updateErr error
		updated, changed, updateErr = kanbanApp.memory.updateOSArtifactWithMetadataIfHeaderMatches(
			header, prior.ID, title, storedBody, user.Name,
			map[string]string{"type": artifactTypeMarkdown, "documentSchemaVersion": "1", documentStudioEmptyMetadataKey: emptyMarker},
		)
		return updateErr
	})
	if err != nil {
		current, found := kanbanApp.osArtifactByID(prior.ID)
		if !found || !artifactAuthorizationHeaderEqual(header, resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(current))) {
			writeDocumentVersionConflict(w, current)
			return
		}
		writeAuthError(w, http.StatusInternalServerError, "document could not be saved")
		return
	}
	qualityState, canExport, stable := kanbanApp.authoredResultFinalExportState(updated)
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok": true, "updated": changed, "artifact": documentStudioView(updated),
		"document":     documentStudioDocumentFromEntry(updated),
		"qualityState": qualityState, "canExport": stable && canExport,
		"receipt": map[string]any{
			"outcome": "document_saved", "artifactId": updated.ID,
			"artifactVersion": artifactVersion(updated), "contentSaved": true,
			"savedToFiles": strings.EqualFold(strings.TrimSpace(updated.Metadata["savedToFiles"]), "true"),
		},
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
	prior, ok := authorizedArtifactForActions(r.Context(), user, payload.ArtifactID, ACLReadContent, ACLWrite, ACLCreateChild)
	if !ok || !artifactIsDocumentStudioDocument(prior) {
		writeAuthError(w, http.StatusNotFound, "document artifact not found")
		return
	}
	if payload.ExpectedVersion < 1 || payload.ExpectedVersion > artifactVersion(prior) {
		writeDocumentVersionConflict(w, prior)
		return
	}
	if !fileFolderWritableFromContext(r.Context(), user, payload.FolderID) {
		writeAuthError(w, http.StatusNotFound, errFileFolderNotFound.Error())
		return
	}
	actor := firstNonEmptyString(user.Name, user.Email)
	var copyEntry meetingMemoryEntry
	var file assistantFileRecord
	var fileErr error
	var internalCopyErr bool
	sourceEntry := prior
	staleBranch := false
	guardErr := kanbanApp.withFinalExportAdmissionOperation(prior, func(current meetingMemoryEntry) error {
		sourceEntry = current
		currentSourceVersion := artifactVersion(current)
		staleBranch = payload.ExpectedVersion != currentSourceVersion
		managed := strings.TrimSpace(firstNonEmptyString(current.Metadata["goalId"], current.Metadata["goalParentId"])) != ""
		if managed && (staleBranch || payload.Document.Markdown != documentStudioDocumentFromEntry(current).Markdown) {
			return fmt.Errorf("review the current authored document before saving an independent copy")
		}

		storedBody, emptyMarker := documentStudioStoredBody(payload.Document.Markdown)
		metadata := map[string]string{
			"title": payload.Title, "type": artifactTypeMarkdown, "source": "scout_thread",
			"status": artifactStatusComplete, "threadStatus": artifactStatusComplete,
			"copiedFromArtifactId": current.ID, "copiedFromArtifactVersion": strconv.Itoa(payload.ExpectedVersion),
			"copiedFromCurrentArtifactVersion": strconv.Itoa(currentSourceVersion),
			"documentSchemaVersion":            "1", "tenantId": strings.TrimSpace(current.Metadata["tenantId"]),
			documentStudioEmptyMetadataKey: emptyMarker,
			"visibility":                   firstNonEmptyString(strings.TrimSpace(current.Metadata["visibility"]), "organization"),
			"ownerEmail":                   normalizeAccountEmail(user.Email),
		}
		copyAssets := make([]artifactAsset, 0)
		for _, asset := range artifactAssets(current) {
			if artifactAssetIsEditableImage(asset) {
				copyAssets = append(copyAssets, asset)
			}
		}
		if len(copyAssets) > 0 {
			rawAssets, marshalErr := json.Marshal(copyAssets)
			if marshalErr != nil {
				internalCopyErr = true
				return marshalErr
			}
			metadata[artifactAssetsMetadataKey] = string(rawAssets)
		}
		if staleBranch {
			metadata["copiedFromStaleRevision"] = "true"
		}
		var appended bool
		var createErr error
		copyEntry, appended, createErr = kanbanApp.createOSArtifactWithMetadata("artifacts", payload.Title, storedBody, actor, metadata)
		if createErr != nil || !appended {
			internalCopyErr = true
			if createErr != nil {
				return createErr
			}
			return fmt.Errorf("document copy was not appended")
		}
		file, fileErr = kanbanApp.saveDeliverableSnapshotToFilesNamed(copyEntry, payload.FolderID, fileName, actor)
		return nil
	})
	if guardErr != nil {
		if copyEntry.ID != "" {
			rollbackAuthoredIndependentCopy(kanbanApp, copyEntry.ID)
		}
		if internalCopyErr {
			log.Errorf("Document copy create failed: %v", guardErr)
			writeAuthError(w, http.StatusInternalServerError, "document copy could not be created")
		} else {
			writeAuthError(w, http.StatusConflict, guardErr.Error())
		}
		return
	}
	if fileErr != nil {
		stored, _ := kanbanApp.osArtifactByID(copyEntry.ID)
		storedView := documentStudioView(stored)
		writeAuthJSON(w, fileSaveErrorStatus(fileErr), map[string]any{
			"ok": false, "partialSuccess": true,
			"error":    "document copy was created, but Files filing failed",
			"artifact": storedView, "document": documentStudioDocumentFromEntry(stored),
			"receipt": map[string]any{
				"outcome": "copy_created_files_failed", "artifactId": stored.ID,
				"artifactVersion": artifactVersion(stored), "contentSaved": true,
				"filingCompleted": false, "savedToFiles": storedView.SavedToFiles,
				"branchedFromArtifactVersion": payload.ExpectedVersion,
				"sourceCurrentVersion":        artifactVersion(sourceEntry), "staleBranch": staleBranch,
				"retryable": true, "retryUrl": "/assistant/files/save", "retryMethod": http.MethodPost,
				"fileName": fileName, "folderId": payload.FolderID,
			},
		})
		return
	}
	stored, _ := kanbanApp.osArtifactByID(copyEntry.ID)
	broadcastSignedInKanbanEvent("file", file)
	writeAuthJSON(w, http.StatusCreated, map[string]any{
		"ok": true, "artifact": documentStudioView(stored), "document": documentStudioDocumentFromEntry(stored), "file": file,
		"receipt": map[string]any{
			"outcome": "copy_created_and_filed", "artifactId": stored.ID,
			"artifactVersion": artifactVersion(stored), "contentSaved": true, "savedToFiles": true,
			"branchedFromArtifactVersion": payload.ExpectedVersion,
			"sourceCurrentVersion":        artifactVersion(sourceEntry), "staleBranch": staleBranch,
		},
	})
}

// documentEditorImageUploadHandler attaches one safe, content-addressed image
// to the exact writable document revision. The Markdown body remains dirty in
// the browser until its image token is saved, while the asset is already bound
// to this artifact so native preview and the non-fetching PDF renderer can use
// the same verified bytes.
func documentEditorImageUploadHandler(w http.ResponseWriter, r *http.Request) {
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
		writeAuthError(w, http.StatusServiceUnavailable, "artifacts are unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, documentStudioImageMaxBytes+(1<<20))
	if err := r.ParseMultipartForm(documentStudioImageMaxBytes); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read image upload")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	artifactID := strings.TrimSpace(r.FormValue("artifactId"))
	expectedVersion, err := strconv.Atoi(strings.TrimSpace(r.FormValue("expectedVersion")))
	if artifactID == "" || err != nil || expectedVersion < 1 {
		writeAuthError(w, http.StatusBadRequest, "artifactId and expectedVersion are required")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "image file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, documentStudioImageMaxBytes+1))
	if err != nil || len(data) == 0 || len(data) > documentStudioImageMaxBytes {
		writeAuthError(w, http.StatusBadRequest, "image file exceeds the 16MB limit")
		return
	}
	mime := http.DetectContentType(data)
	if !oneOf(mime, "image/png", "image/jpeg", "image/webp", "image/gif") {
		writeAuthError(w, http.StatusBadRequest, "image must be PNG, JPEG, WebP, or GIF")
		return
	}
	prior, ok := authorizedArtifactForActions(r.Context(), user, artifactID, ACLReadContent, ACLWrite)
	if !ok || !artifactIsDocumentStudioDocument(prior) {
		writeAuthError(w, http.StatusNotFound, "document artifact not found")
		return
	}
	if artifactVersion(prior) != expectedVersion {
		writeDocumentVersionConflict(w, prior)
		return
	}
	ref, err := putBlob(data, mime)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "image could not be stored")
		return
	}
	name := strings.TrimSpace(filepath.Base(header.Filename))
	if name == "" || name == "." || len([]rune(name)) > 200 {
		name = "document-image." + deckImageExtension(mime)
	}
	assetsRaw, err := deckAssetsMetadataWith(prior, artifactAsset{Ref: ref, Mime: mime, Name: name, Kind: "image"})
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "image could not be attached")
		return
	}
	headerOwner := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(prior))
	var updated meetingMemoryEntry
	err = kanbanApp.withCurrentAgentThreadSource(scoutAgentThread{Artifact: prior}, func() error {
		var updateErr error
		updated, _, updateErr = kanbanApp.memory.updateOSArtifactWithMetadataIfHeaderMatches(
			headerOwner, prior.ID, prior.Metadata["title"], prior.Text, user.Name,
			map[string]string{artifactAssetsMetadataKey: assetsRaw},
		)
		return updateErr
	})
	if err != nil {
		current, _ := kanbanApp.osArtifactByID(prior.ID)
		writeDocumentVersionConflict(w, current)
		return
	}
	writeAuthJSON(w, http.StatusCreated, map[string]any{
		"ok": true, "artifact": documentStudioView(updated),
		"image": map[string]any{
			"ref": ref, "mime": mime, "name": name,
			"url": fileBlobDownloadURL(ref, name),
		},
	})
}

func writeDocumentVersionConflict(w http.ResponseWriter, artifact meetingMemoryEntry) {
	writeAuthJSON(w, http.StatusConflict, map[string]any{
		"error": "document revision changed; reload or save a copy", "artifactId": artifact.ID,
		"currentVersion": artifactVersion(artifact),
	})
}
