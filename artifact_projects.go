package main

// artifact_projects.go — Wave 11 D8: project tags on deliverables. A project
// is a name, not a record: tagging an artifact stamps `project` on it and
// auto-files its saved Drive copy under the `Projects/<name>` folder
// (created on first use, the folder-by-name-or-create pattern of
// kanban.go's saveToFilesTool, nested under one "Projects" root). Retagging
// moves the Drive copy; untagging clears the tag and leaves the file where it
// is. A deliverable that has not been saved to Drive yet is saved on tagging
// (the founder's "tag a project which would auto-place it in that folder").
//
// The Drive half is fenced and degrades rather than failing: a PRIVATE
// deliverable never mints a folder (the folder tree is listed to every member
// by name, so the codename would leak), and a folder the tagger does not
// manage is never written to (the /assistant/files/save fence). Either way
// the tag itself is still stamped — a project is a name on the artifact.
//
// GET /assistant/projects lists every project the viewer can see with its
// deliverable count; the studio list filters on ?project=.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	artifactProjectMetadataKey         = "project"
	artifactProjectFolderIDMetadataKey = "projectFolderId"
	artifactProjectTaggedAtMetadataKey = "projectTaggedAt"
	artifactProjectTaggedByMetadataKey = "projectTaggedBy"
	artifactProjectsRootFolderName     = "Projects"
)

type artifactProjectView struct {
	Name     string `json:"name"`
	Count    int    `json:"count"`
	FolderID string `json:"folderId,omitempty"`
}

// topLevelFileFolderByName resolves a top-level folder by name.
func topLevelFileFolderByName(name string) (fileFolderRecord, bool) {
	for _, folder := range listFileFolders() {
		if strings.TrimSpace(folder.ParentID) == "" && strings.EqualFold(folder.Name, name) {
			return folder, true
		}
	}
	return fileFolderRecord{}, false
}

func childFileFolderByName(parentID string, name string) (fileFolderRecord, bool) {
	for _, folder := range listFileFolders() {
		if folder.ParentID == parentID && strings.EqualFold(folder.Name, name) {
			return folder, true
		}
	}
	return fileFolderRecord{}, false
}

// projectFolderCreateRaceProbe runs immediately before each folder create
// that can lose a first-use race. Tests use it to make that race
// deterministic; production leaves it nil.
var projectFolderCreateRaceProbe func(parentID string, name string)

func projectFolderCreateRace(parentID string, name string) {
	if projectFolderCreateRaceProbe != nil {
		projectFolderCreateRaceProbe(parentID, name)
	}
}

// projectsRootFolder resolves the top-level "Projects" folder, creating it
// only when create is true.
//
// The root is a shared NAMESPACE anchor, not a private container: exactly
// like the top level of Drive (where /assistant/files/folders lets any member
// create a folder), any member may add their own `Projects/<name>` under it.
// What the root deliberately does NOT confer is write access to somebody
// else's project folder — tagArtifactProject re-checks that per folder.
func projectsRootFolder(actor string, create bool) (fileFolderRecord, error) {
	if folder, ok := topLevelFileFolderByName(artifactProjectsRootFolderName); ok {
		return folder, nil
	}
	if !create {
		return fileFolderRecord{}, nil
	}
	projectFolderCreateRace("", artifactProjectsRootFolderName)
	folder, err := createFileFolder(artifactProjectsRootFolderName, actor)
	if err != nil {
		// Two taggers can race into the same first use. A duplicate name is
		// evidence the other one won, not a failure: re-resolve and use it.
		if errors.Is(err, errFileFolderDuplicate) {
			if existing, ok := topLevelFileFolderByName(artifactProjectsRootFolderName); ok {
				return existing, nil
			}
		}
		return fileFolderRecord{}, err
	}
	return folder, nil
}

// projectFolderByNameOrCreate resolves the `Projects/<name>` folder, creating
// the root and the child on first use ONLY when create is true (a private
// deliverable never mints one — see tagArtifactProject). The returned name is
// the stored (whitespace-collapsed) folder name so tags and folders never
// drift, and it is returned even when no folder exists, so a tag can still be
// stamped without a Drive folder behind it.
func projectFolderByNameOrCreate(name string, actor string, create bool) (fileFolderRecord, string, error) {
	normalized, err := normalizeFileFolderName(name)
	if err != nil {
		return fileFolderRecord{}, "", err
	}
	root, err := projectsRootFolder(actor, create)
	if err != nil {
		return fileFolderRecord{}, normalized, err
	}
	if strings.TrimSpace(root.ID) == "" {
		return fileFolderRecord{}, normalized, nil
	}
	if folder, ok := childFileFolderByName(root.ID, normalized); ok {
		return folder, folder.Name, nil
	}
	if !create {
		return fileFolderRecord{}, normalized, nil
	}
	projectFolderCreateRace(root.ID, normalized)
	folder, err := sharedFileFolderStore().createInParent(normalized, root.ID, actor)
	if err != nil {
		// Same race, one level down: the loser re-resolves the winner's
		// folder instead of failing the tag.
		if errors.Is(err, errFileFolderDuplicate) {
			if existing, ok := childFileFolderByName(root.ID, normalized); ok {
				return existing, existing.Name, nil
			}
		}
		return fileFolderRecord{}, normalized, err
	}
	return folder, folder.Name, nil
}

// projectFolderByName resolves an existing `Projects/<name>` folder.
func projectFolderByName(name string) (fileFolderRecord, bool) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return fileFolderRecord{}, false
	}
	root, ok := topLevelFileFolderByName(artifactProjectsRootFolderName)
	if !ok {
		return fileFolderRecord{}, false
	}
	return childFileFolderByName(root.ID, name)
}

// artifactProjectTaggable is the closed set the picker may tag: studio
// deliverables (presentation, document, research, and the other Work kinds)
// plus Story Studio outlines.
func artifactProjectTaggable(entry meetingMemoryEntry) bool {
	if packagingStoryOutlineArtifact(entry) {
		return true
	}
	_, _, _, ok := studioProjectClassification(entry)
	return ok
}

type artifactProjectTagResult struct {
	ArtifactID string `json:"artifactId"`
	Project    string `json:"project"`
	FolderID   string `json:"folderId,omitempty"`
	FileID     string `json:"fileId,omitempty"`
	Moved      bool   `json:"moved"`
	Saved      bool   `json:"saved"`
}

// tagArtifactProject applies the picker semantics: name → stamp + auto-file
// (creating the folder on first use, when this caller and this deliverable
// may have one); "" → clear the tag, leave the file.
func (app *kanbanBoardApp) tagArtifactProject(ctx context.Context, user *userAccount, artifact meetingMemoryEntry, project string) (artifactProjectTagResult, error) {
	result := artifactProjectTagResult{ArtifactID: artifact.ID}
	actor := firstNonEmptyString(strings.TrimSpace(user.Name), normalizeAccountEmail(user.Email))
	// A project folder records its creator the way POST /assistant/files/folders
	// does — the canonical account email — so fileFolderManagedByUser resolves
	// the creator exactly rather than through a roster-name lookup that a
	// duplicate or renamed display name can lose.
	folderCreator := firstNonEmptyString(normalizeAccountEmail(user.Email), actor)
	header := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact))
	project = strings.Join(strings.Fields(project), " ")
	if project == "" {
		updates := map[string]string{
			artifactProjectMetadataKey: "", artifactProjectFolderIDMetadataKey: "",
			artifactProjectTaggedAtMetadataKey: "", artifactProjectTaggedByMetadataKey: "",
		}
		if _, matched, err := app.memory.updateOSArtifactMetadataIfHeaderMatches(header, artifact.ID, updates); err != nil {
			return result, err
		} else if !matched {
			return result, errFileSaveNotFound
		}
		if fileRow, ok := fileDeliverableRecord(artifact); ok {
			result.FileID = fileRow.ID
		}
		return result, nil
	}
	// The Drive half of a project tag is fenced twice, because a Drive folder
	// is BOTH a globally listed name and a write target.
	//
	//  1. A private deliverable never MINTS `Projects/<name>`: the folder tree
	//     GET /assistant/files returns is not viewer-filtered, so creating one
	//     would publish the very codename listArtifactProjects, the studio
	//     `?project=` filter and this file's fence work to keep private.
	//  2. The folder a tag files into must be one this caller could have named
	//     on /assistant/files/save. Resolving `Projects/<name>` by name across
	//     all creators would otherwise let anyone drop their own Drive row
	//     into somebody else's project folder through this door.
	//
	// Neither fence fails the tag: a project is a name on the artifact, not a
	// Drive record. When the Drive folder is unavailable to this caller the
	// tag is still stamped and the deliverable's Drive copy simply stays where
	// it is — no folder id is stamped either, so nothing downstream (the
	// duplicate door, a later retag) inherits a folder this caller cannot use.
	folder, storedName, err := projectFolderByNameOrCreate(project, folderCreator, !legacyArtifactHeaderIsPrivate(header))
	if err != nil {
		return result, err
	}
	result.Project = storedName
	filed := strings.TrimSpace(folder.ID) != "" && fileFolderWritableFromContext(ctx, user, folder.ID)
	if filed {
		result.FolderID = folder.ID
	}
	updates := map[string]string{
		artifactProjectMetadataKey:         storedName,
		artifactProjectFolderIDMetadataKey: result.FolderID,
		artifactProjectTaggedAtMetadataKey: time.Now().UTC().Format(time.RFC3339Nano),
		artifactProjectTaggedByMetadataKey: normalizeAccountEmail(user.Email),
	}
	updated, matched, err := app.memory.updateOSArtifactMetadataIfHeaderMatches(header, artifact.ID, updates)
	if err != nil {
		return result, err
	}
	if !matched {
		return result, errFileSaveNotFound
	}
	// Auto-file: a deliverable's Drive row is keyed by its artifact id. Save
	// it first when it has never been saved, then assign the folder. Story
	// outlines are not Drive deliverables; the tag alone files them in the hub.
	if !filed || !deliverableRecordQualifies(updated) {
		return result, nil
	}
	if !strings.EqualFold(strings.TrimSpace(updated.Metadata["savedToFiles"]), "true") {
		if _, saveErr := app.saveDeliverableSnapshotToFilesNamed(updated, folder.ID, "", actor); saveErr != nil {
			return result, saveErr
		}
		result.Saved = true
		result.Moved = true
		result.FileID = updated.ID
		return result, nil
	}
	_, assignments := sharedFileFolderStore().snapshot()
	if assignments[updated.ID] != folder.ID {
		if moveErr := moveFileToFolder(updated.ID, folder.ID); moveErr != nil {
			return result, moveErr
		}
		result.Moved = true
	}
	result.FileID = updated.ID
	return result, nil
}

// fileFolderCreatedByUser reports whether the viewer created the folder (the
// first tagger of a project is its folder's creator).
func fileFolderCreatedByUser(folder fileFolderRecord, user *userAccount) bool {
	if user == nil {
		return false
	}
	creator := strings.TrimSpace(folder.CreatedBy)
	if creator == "" {
		return false
	}
	if canonicalAuthenticatedPrincipal(creator) {
		return normalizeAccountEmail(creator) == normalizeAccountEmail(user.Email)
	}
	if creatorEmail, ok := canonicalEmailForUniqueRosterName(creator); ok {
		return creatorEmail == normalizeAccountEmail(user.Email)
	}
	return strings.EqualFold(creator, strings.TrimSpace(user.Name))
}

// listArtifactProjects returns only the projects this viewer may know exist:
// a `Projects/<name>` folder the viewer created, or any project tag on at
// least one artifact the viewer can read. A project whose deliverables are
// all unreadable to the viewer never leaks its name (deal codenames).
func (app *kanbanBoardApp) listArtifactProjects(ctx context.Context, user *userAccount) []artifactProjectView {
	views := map[string]*artifactProjectView{}
	order := []string{}
	add := func(name string, folderID string) *artifactProjectView {
		key := strings.ToLower(name)
		if view, ok := views[key]; ok {
			if view.FolderID == "" {
				view.FolderID = folderID
			}
			return view
		}
		view := &artifactProjectView{Name: name, FolderID: folderID}
		views[key] = view
		order = append(order, key)
		return view
	}
	folders := listFileFolders()
	for _, root := range folders {
		if strings.TrimSpace(root.ParentID) != "" || !strings.EqualFold(root.Name, artifactProjectsRootFolderName) {
			continue
		}
		for _, folder := range folders {
			if folder.ParentID == root.ID && fileFolderCreatedByUser(folder, user) {
				add(folder.Name, folder.ID)
			}
		}
	}
	if app != nil && app.memory != nil && user != nil {
		for _, candidate := range app.memory.artifactListAuthorizationSnapshot() {
			name := strings.TrimSpace(candidate.Entry.Metadata[artifactProjectMetadataKey])
			if name == "" || !artifactHeaderAuthorized(ctx, user, ACLReadContent, candidate.Header) {
				continue
			}
			add(name, strings.TrimSpace(candidate.Entry.Metadata[artifactProjectFolderIDMetadataKey])).Count++
		}
	}
	result := make([]artifactProjectView, 0, len(order))
	for _, key := range order {
		result = append(result, *views[key])
	}
	sort.SliceStable(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) < strings.ToLower(result[right].Name)
	})
	return result
}

// artifactProjectHandler serves PATCH /artifacts/project {artifactId, project}.
func artifactProjectHandler(w http.ResponseWriter, r *http.Request) {
	// The member gate runs before the method check so every verb answers a
	// guest with a hard 401/403 (the route-walk allowlist contract).
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if r.Method != http.MethodPatch {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if kanbanApp == nil || kanbanApp.memory == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "artifacts are unavailable")
		return
	}
	payload := struct {
		ArtifactID string `json:"artifactId"`
		Project    string `json:"project"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read the project tag")
		return
	}
	payload.ArtifactID = strings.TrimSpace(payload.ArtifactID)
	project := strings.Join(strings.Fields(payload.Project), " ")
	if payload.ArtifactID == "" {
		writeAuthError(w, http.StatusBadRequest, "artifactId is required")
		return
	}
	if project != "" {
		if _, err := normalizeFileFolderName(project); err != nil {
			writeAuthError(w, http.StatusBadRequest, "project name must be 1-60 characters")
			return
		}
	}
	artifact, ok := authorizedArtifactForActions(r.Context(), user, payload.ArtifactID, ACLReadContent, ACLWrite)
	if !ok || !artifactProjectTaggable(artifact) {
		writeAuthError(w, http.StatusNotFound, "artifact not found")
		return
	}
	result, err := kanbanApp.tagArtifactProject(r.Context(), user, artifact, project)
	if err != nil {
		status := fileSaveErrorStatus(err)
		if status == http.StatusInternalServerError {
			log.Errorf("Project tag %s failed: %v", artifact.ID, err)
			writeAuthError(w, status, "the project tag could not be applied")
			return
		}
		writeAuthError(w, status, err.Error())
		return
	}
	broadcastSignedInKanbanEvent("file", map[string]any{"kind": "folders"})
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok": true, "artifactId": result.ArtifactID, "project": result.Project, "folderId": result.FolderID,
		"fileId": result.FileID, "moved": result.Moved, "saved": result.Saved,
	})
}

// assistantProjectsHandler serves GET /assistant/projects.
func assistantProjectsHandler(w http.ResponseWriter, r *http.Request) {
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if kanbanApp == nil || kanbanApp.memory == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "artifacts are unavailable")
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "projects": kanbanApp.listArtifactProjects(r.Context(), user)})
}

// artifactProjectVisibleToViewer is the `?project=` fence: a name outside
// the viewer's own project set is indistinguishable from a nonexistent one.
func (app *kanbanBoardApp) artifactProjectVisibleToViewer(ctx context.Context, user *userAccount, project string) bool {
	project = strings.Join(strings.Fields(project), " ")
	if project == "" {
		return true
	}
	for _, view := range app.listArtifactProjects(ctx, user) {
		if strings.EqualFold(view.Name, project) {
			return true
		}
	}
	return false
}
