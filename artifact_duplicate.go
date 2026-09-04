package main

// artifact_duplicate.go — Wave 11 D7: one Duplicate door for every
// deliverable kind. POST /artifacts/duplicate {artifactId, title?, fileName?,
// folderId?} wraps the existing copy paths — the native deck copy
// (createDeckEditorCopy) and the Document Studio markdown copy, which research
// reports share because they are Markdown bodies — so a duplicate keeps the
// exact same fence as its source (visibility, tenant, review-bound parent),
// carries the "Copy of …" title, and lands in Drive like a copy made from
// inside the editors. Rename stays on the studio PATCH.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const artifactDuplicateTitlePrefix = "Copy of "

func artifactDuplicateTitle(source meetingMemoryEntry, requested string) string {
	requested = strings.Join(strings.Fields(requested), " ")
	if requested != "" {
		return boundedStudioProjectTitle(requested)
	}
	base := strings.Join(strings.Fields(firstNonEmptyString(source.Metadata["studioTitle"], source.Metadata["title"], source.Metadata["threadQuery"], "Untitled")), " ")
	return boundedStudioProjectTitle(artifactDuplicateTitlePrefix + base)
}

// createMarkdownArtifactCopy is the document copy path lifted out of
// documentEditorCopyHandler so research reports and documents duplicate the
// same way: current body, editable image assets, copiedFrom lineage, the
// source's visibility/tenant fence, and the review-bound parent when the
// source is a goal deliverable. The research contract stamps ride along so a
// duplicated report keeps its branded export and its Work kind.
func createMarkdownArtifactCopy(user *userAccount, current meetingMemoryEntry, title string, sourceVersion int) (meetingMemoryEntry, error) {
	if user == nil || kanbanApp == nil || kanbanApp.memory == nil {
		return meetingMemoryEntry{}, fmt.Errorf("artifacts are unavailable")
	}
	currentSourceVersion := artifactVersion(current)
	parentID := strings.TrimSpace(firstNonEmptyString(current.Metadata["goalId"], current.Metadata["goalParentId"]))
	storedBody, emptyMarker := documentStudioStoredBody(documentStudioDocumentFromEntry(current).Markdown)
	metadata := map[string]string{
		"title": title, "type": artifactTypeMarkdown, "source": "scout_thread",
		"status": artifactStatusComplete, "threadStatus": artifactStatusComplete,
		"copiedFromArtifactId": current.ID, "copiedFromArtifactVersion": strconv.Itoa(sourceVersion),
		"copiedFromCurrentArtifactVersion": strconv.Itoa(currentSourceVersion),
		"documentSchemaVersion":            "1", "tenantId": strings.TrimSpace(current.Metadata["tenantId"]),
		documentStudioEmptyMetadataKey: emptyMarker,
		"visibility":                   firstNonEmptyString(strings.TrimSpace(current.Metadata["visibility"]), "organization"),
		"ownerEmail":                   normalizeAccountEmail(user.Email),
	}
	// The copy keeps the source's Work identity: source/mode/thread lineage
	// are exactly what studioLegacyProjectCandidate classifies on (a blank
	// document stays a document_studio document, a threaded report stays a
	// research row), and a research report keeps its contract stamps so the
	// copy still exports branded.
	for _, key := range []string{"source", "mode", "threadId"} {
		if value := strings.TrimSpace(current.Metadata[key]); value != "" {
			metadata[key] = value
		}
	}
	if strings.EqualFold(strings.TrimSpace(current.Metadata["mode"]), "research") && studioResearchReportArtifact(current.Metadata) {
		for _, key := range []string{"artifactContract", "outputContract", "artifactHeadings", "requestedBy", "model", "worker"} {
			if value := strings.TrimSpace(current.Metadata[key]); value != "" {
				metadata[key] = value
			}
		}
	}
	copyAssets := make([]artifactAsset, 0)
	for _, asset := range artifactAssets(current) {
		if artifactAssetIsEditableImage(asset) {
			copyAssets = append(copyAssets, asset)
		}
	}
	if len(copyAssets) > 0 {
		rawAssets, err := json.Marshal(copyAssets)
		if err != nil {
			return meetingMemoryEntry{}, err
		}
		metadata[artifactAssetsMetadataKey] = string(rawAssets)
	}
	if sourceVersion != currentSourceVersion {
		metadata["copiedFromStaleRevision"] = "true"
	}
	if parentID != "" {
		metadata["goalParentId"] = parentID
		metadata[authoredCopyReviewMetadataKey] = authoredCopyReviewPending
		metadata[authoredCopyAdmissionRootMetadataKey] = strings.TrimSpace(firstNonEmptyString(current.Metadata[authoredCopyAdmissionRootMetadataKey], current.ID))
	}
	actor := firstNonEmptyString(strings.TrimSpace(user.Name), normalizeAccountEmail(user.Email))
	copyEntry, appended, err := kanbanApp.createOSArtifactWithMetadata("artifacts", title, storedBody, actor, metadata)
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	if !appended || strings.TrimSpace(copyEntry.ID) == "" {
		return meetingMemoryEntry{}, fmt.Errorf("copy was not appended")
	}
	return copyEntry, nil
}

type artifactDuplicateView struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Type         string `json:"type"`
	Kind         string `json:"kind,omitempty"`
	Version      int    `json:"version"`
	SavedToFiles bool   `json:"savedToFiles"`
}

func artifactDuplicateViewFromEntry(entry meetingMemoryEntry) artifactDuplicateView {
	kind, _, _, _ := studioProjectClassification(entry)
	return artifactDuplicateView{
		ID: entry.ID, Title: strings.TrimSpace(entry.Metadata["title"]), Type: artifactType(entry), Kind: kind,
		Version: artifactVersion(entry), SavedToFiles: strings.EqualFold(strings.TrimSpace(entry.Metadata["savedToFiles"]), "true"),
	}
}

// artifactDuplicateHandler serves POST /artifacts/duplicate.
func artifactDuplicateHandler(w http.ResponseWriter, r *http.Request) {
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
	payload := struct {
		ArtifactID string `json:"artifactId"`
		Title      string `json:"title"`
		FileName   string `json:"fileName"`
		FolderID   string `json:"folderId"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read the duplicate request")
		return
	}
	payload.ArtifactID = strings.TrimSpace(payload.ArtifactID)
	payload.FolderID = strings.TrimSpace(payload.FolderID)
	if payload.ArtifactID == "" {
		writeAuthError(w, http.StatusBadRequest, "artifactId is required")
		return
	}
	if len([]rune(strings.TrimSpace(payload.Title))) > studioProjectTitleMaxRunes {
		writeAuthError(w, http.StatusBadRequest, "the title is too long")
		return
	}
	prior, ok := authorizedArtifactForActions(r.Context(), user, payload.ArtifactID, ACLReadContent, ACLWrite, ACLCreateChild)
	if !ok {
		writeAuthError(w, http.StatusNotFound, "artifact not found")
		return
	}
	isDeck := artifactIsDeckEditorDocument(prior)
	isMarkdown := !isDeck && artifactIsDocumentStudioDocument(prior)
	if !isDeck && !isMarkdown {
		writeAuthError(w, http.StatusNotFound, "only presentations, documents, and research reports can be duplicated")
		return
	}
	// A project tag is part of the fence a copy inherits: the duplicate files
	// into the same Projects/<name> folder unless the caller names another.
	// The substitution happens BEFORE the writability gate, never after it:
	// checking the caller's "" and then filing into the source's project
	// folder would let anyone who can read an organization-visible deliverable
	// write a row into a folder /assistant/files/save would refuse them.
	inheritedFolder := false
	if payload.FolderID == "" {
		if folderID := strings.TrimSpace(prior.Metadata[artifactProjectFolderIDMetadataKey]); folderID != "" && fileFolderExists(folderID) {
			payload.FolderID = folderID
			inheritedFolder = true
		}
	}
	if !fileFolderWritableFromContext(r.Context(), user, payload.FolderID) {
		// A folder the caller NAMED is a hard refusal; an inherited one is not
		// the caller's request, so the copy simply lands in the Drive root.
		if !inheritedFolder {
			writeAuthError(w, fileFolderErrorStatus(errFileFolderNotFound), errFileFolderNotFound.Error())
			return
		}
		payload.FolderID = ""
	}
	title := artifactDuplicateTitle(prior, payload.Title)
	fileName, err := normalizeAssistantFileName(firstNonEmptyString(strings.TrimSpace(payload.FileName), title))
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "a valid file name is required")
		return
	}
	actor := firstNonEmptyString(strings.TrimSpace(user.Name), normalizeAccountEmail(user.Email))
	var copyEntry meetingMemoryEntry
	var file assistantFileRecord
	var fileErr error
	var internalCopyErr bool
	guardErr := kanbanApp.withAuthoredCopySourceOperation(prior, func(current meetingMemoryEntry) error {
		sourceVersion := artifactVersion(current)
		var createErr error
		if isDeck {
			deck, _, quality, loadErr := loadDeckDocument(current)
			if loadErr != nil {
				return loadErr
			}
			if quality == "approximate" {
				return fmt.Errorf("legacy deck cannot be duplicated without losing unrecognized content")
			}
			reviewBound := strings.TrimSpace(firstNonEmptyString(current.Metadata["goalId"], current.Metadata["goalParentId"])) != ""
			copyEntry, createErr = createDeckEditorCopy(user, current, deck, title, sourceVersion, reviewBound)
		} else {
			copyEntry, createErr = createMarkdownArtifactCopy(user, current, title, sourceVersion)
		}
		if createErr != nil {
			internalCopyErr = true
			return createErr
		}
		if project := strings.TrimSpace(current.Metadata[artifactProjectMetadataKey]); project != "" {
			// The copy inherits the tag, but a stamped folder id is a standing
			// Drive write target: only one this caller may write rides along,
			// so the next duplicate cannot launder it either.
			projectFolderID := strings.TrimSpace(current.Metadata[artifactProjectFolderIDMetadataKey])
			if projectFolderID != "" && !fileFolderWritableFromContext(r.Context(), user, projectFolderID) {
				projectFolderID = ""
			}
			_, _, _ = kanbanApp.memory.updateOSArtifactMetadata(copyEntry.ID, map[string]string{
				artifactProjectMetadataKey:         project,
				artifactProjectFolderIDMetadataKey: projectFolderID,
			})
		}
		file, fileErr = kanbanApp.saveDeliverableSnapshotToFilesNamed(copyEntry, payload.FolderID, fileName, actor)
		return nil
	})
	if guardErr != nil {
		if copyEntry.ID != "" {
			rollbackAuthoredIndependentCopy(kanbanApp, copyEntry.ID)
		}
		if internalCopyErr {
			log.Errorf("Duplicate create failed: %v", guardErr)
			writeAuthError(w, http.StatusInternalServerError, "the duplicate could not be created")
		} else {
			writeAuthError(w, http.StatusConflict, guardErr.Error())
		}
		return
	}
	stored, _ := kanbanApp.osArtifactByID(copyEntry.ID)
	if fileErr != nil {
		if fileSaveErrorStatus(fileErr) == http.StatusInternalServerError {
			log.Errorf("Duplicate Files save failed: %v", fileErr)
		}
		writeAuthJSON(w, fileSaveErrorStatus(fileErr), map[string]any{
			"ok": false, "partialSuccess": true, "error": "the duplicate was created, but Files filing failed",
			"artifact": artifactDuplicateViewFromEntry(stored),
			"receipt": map[string]any{
				"outcome": "copy_created_files_failed", "artifactId": stored.ID, "artifactVersion": artifactVersion(stored),
				"contentSaved": true, "filingCompleted": false, "retryable": true, "retryUrl": "/assistant/files/save", "retryMethod": http.MethodPost,
				"fileName": fileName, "folderId": payload.FolderID,
			},
		})
		return
	}
	broadcastSignedInKanbanEvent("file", file)
	writeAuthJSON(w, http.StatusCreated, map[string]any{
		"ok": true, "artifact": artifactDuplicateViewFromEntry(stored), "file": file,
		"receipt": map[string]any{
			"outcome": "copy_created_and_filed", "artifactId": stored.ID, "artifactVersion": artifactVersion(stored),
			"contentSaved": true, "savedToFiles": true, "sourceArtifactId": prior.ID,
		},
	})
}
