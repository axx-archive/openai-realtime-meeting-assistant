package main

// Files-surface folders — the organization layer over the one shared list
// files.go assembles. A folder is a label, not a container: rows keep living
// in their source stores (kind=file entries, chat attachments, os_artifact
// deliverables) and a mutex-guarded JSON side-store at data/file-folders.json
// maps row id → folder id. Assignments may reference ANY row id — an upload
// entry, a chat-attachment id, an artifact id — and a dangling assignment
// (deleted folder, pruned row) is simply ignored at read time, so the store
// never needs to chase the sources.
//
// The path derives like sessionsFilePath does (next to the memory JSONL, env
// override for tests/ops) and persistence is atomic tmp+rename, the
// sessionStore.persistLocked law.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// fileFolderMaxCount caps the complete tree.
	fileFolderMaxCount = 100
	fileFolderMaxDepth = 5
	// fileFolderNameMaxLen bounds a folder name after whitespace collapse.
	fileFolderNameMaxLen = 60
)

var (
	errFileFolderName      = errors.New("folder name must be 1-60 characters")
	errFileFolderDuplicate = errors.New("a folder with that name already exists")
	errFileFolderNotFound  = errors.New("folder not found")
	errFileFolderLimit     = fmt.Errorf("folder limit reached (%d folders max)", fileFolderMaxCount)
	errFileFolderFileID    = errors.New("file id is required")
)

// fileFolderRecord is one stored folder, serialized to the client verbatim.
type fileFolderRecord struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	ParentID          string `json:"parentId,omitempty"`
	CreatedBy         string `json:"createdBy,omitempty"`
	TenantID          string `json:"tenantId,omitempty"`
	CreatedByPersonID string `json:"createdByPersonId,omitempty"`
	CreatedAt         string `json:"createdAt,omitempty"`
}

// fileFolderStoreState is the on-disk shape of data/file-folders.json.
type fileFolderStoreState struct {
	Folders             []fileFolderRecord `json:"folders"`
	Assignments         map[string]string  `json:"assignments"`
	AssignmentUpdatedAt map[string]string  `json:"assignmentUpdatedAt,omitempty"`
}

// fileFolderStore guards the folder list + row assignments behind one mutex,
// persisting every mutation atomically (tmp+rename, like sessionStore).
type fileFolderStore struct {
	mu          sync.Mutex
	path        string
	folders     []fileFolderRecord
	assignments map[string]string
	// assignmentUpdatedAt is recovery-only ABA evidence. Canonical projection
	// deliberately ignores it; legacy files without it remain readable.
	assignmentUpdatedAt map[string]string
	loadErr             error
}

func newFileFolderStore(path string) *fileFolderStore {
	store := &fileFolderStore{path: path, assignments: map[string]string{}, assignmentUpdatedAt: map[string]string{}}
	if err := recoverLegacyLifecycleTransactionsForPaths(CanonicalImportPaths{
		MeetingMemory: meetingMemoryPath(), Notifications: notificationsPath(), FileFolders: path,
	}); err != nil {
		store.loadErr = fmt.Errorf("recover legacy lifecycle transactions: %w", err)
		return store
	}
	if raw, err := os.ReadFile(path); err == nil {
		var state fileFolderStoreState
		if err := json.Unmarshal(raw, &state); err != nil {
			store.loadErr = fmt.Errorf("file-folder store is malformed")
		} else {
			recovered, changed, recoveryErr := recoverJournaledFileFolderDeletions(path, state)
			if recoveryErr != nil {
				store.loadErr = recoveryErr
				return store
			}
			state = recovered
			if changed {
				raw, encodeErr := json.MarshalIndent(state, "", "  ")
				if encodeErr != nil {
					store.loadErr = fmt.Errorf("encode file-folder lifecycle recovery: %w", encodeErr)
					return store
				}
				if persistErr := writeFileAtomicallyForCanonicalMode(path, raw, 0o600); persistErr != nil {
					store.loadErr = fmt.Errorf("persist file-folder lifecycle recovery: %w", persistErr)
					return store
				}
			}
			store.folders = state.Folders
			if state.Assignments != nil {
				store.assignments = state.Assignments
			}
			store.assignmentUpdatedAt = cloneFileFolderAssignments(state.AssignmentUpdatedAt)
			if !store.folderTreeValidLocked() {
				store.loadErr = fmt.Errorf("file-folder store is malformed")
			}
		}
	} else if !os.IsNotExist(err) {
		store.loadErr = fmt.Errorf("file-folder store is unavailable")
	}
	return store
}

func (s *fileFolderStore) reloadVisibleStateLocked() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		s.loadErr = fmt.Errorf("file-folder store is unavailable")
		return s.loadErr
	}
	var state fileFolderStoreState
	if err := json.Unmarshal(raw, &state); err != nil {
		s.loadErr = fmt.Errorf("file-folder store is malformed")
		return s.loadErr
	}
	s.folders = append([]fileFolderRecord(nil), state.Folders...)
	s.assignments = cloneFileFolderAssignments(state.Assignments)
	s.assignmentUpdatedAt = cloneFileFolderAssignments(state.AssignmentUpdatedAt)
	if !s.folderTreeValidLocked() {
		s.loadErr = fmt.Errorf("file-folder store is malformed")
		return s.loadErr
	}
	s.loadErr = nil
	return nil
}

func (s *fileFolderStore) persistLocked() error {
	raw, err := json.MarshalIndent(fileFolderStoreState{
		Folders:             s.folders,
		Assignments:         s.assignments,
		AssignmentUpdatedAt: s.assignmentUpdatedAt,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode file-folder store: %w", err)
	}
	if err := writeFileAtomicallyForCanonicalMode(s.path, raw, 0o600); err != nil {
		return fmt.Errorf("persist file-folder store: %w", err)
	}
	return nil
}

func cloneFileFolderAssignments(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for fileID, folderID := range source {
		clone[fileID] = folderID
	}
	return clone
}

// normalizeFileFolderName collapses whitespace runs and enforces the 1-60
// bound — the only name law; anything printable inside it is the team's call.
func normalizeFileFolderName(raw string) (string, error) {
	name := strings.Join(strings.Fields(raw), " ")
	if name == "" || len([]rune(name)) > fileFolderNameMaxLen {
		return "", errFileFolderName
	}
	return name, nil
}

func (s *fileFolderStore) folderIndexLocked(id string) int {
	for index := range s.folders {
		if s.folders[index].ID == id {
			return index
		}
	}
	return -1
}

func (s *fileFolderStore) nameTakenLocked(name string, excludeID string, parentID string) bool {
	for _, folder := range s.folders {
		if folder.ID != excludeID && folder.ParentID == parentID && strings.EqualFold(folder.Name, name) {
			return true
		}
	}
	return false
}

func (s *fileFolderStore) create(name string, createdBy string) (fileFolderRecord, error) {
	return s.createInParent(name, "", createdBy)
}

func (s *fileFolderStore) createInParent(name string, parentID string, createdBy string) (fileFolderRecord, error) {
	return s.createInParentBound(name, parentID, createdBy, "", "")
}

func (s *fileFolderStore) createInParentForPrincipal(name, parentID string, principal StrideE10TenantPrincipal) (fileFolderRecord, error) {
	if !strideIdentifier(principal.TenantID) || !strideIdentifier(principal.PersonID) {
		return fileFolderRecord{}, errFileFolderNotFound
	}
	return s.createInParentBound(name, parentID, "", principal.TenantID, principal.PersonID)
}

func (s *fileFolderStore) createInParentBound(name string, parentID string, createdBy string, tenantID string, personID string) (fileFolderRecord, error) {
	normalized, err := normalizeFileFolderName(name)
	if err != nil {
		return fileFolderRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return fileFolderRecord{}, s.loadErr
	}
	if len(s.folders) >= fileFolderMaxCount {
		return fileFolderRecord{}, errFileFolderLimit
	}
	parentID = strings.TrimSpace(parentID)
	if parentID != "" {
		parentIndex := s.folderIndexLocked(parentID)
		if parentIndex < 0 || tenantID != "" && s.folders[parentIndex].TenantID != tenantID {
			return fileFolderRecord{}, errFileFolderNotFound
		}
		if s.folderDepthLocked(parentID) >= fileFolderMaxDepth {
			return fileFolderRecord{}, errFileFolderLimit
		}
	}
	if s.nameTakenLocked(normalized, "", parentID) {
		return fileFolderRecord{}, errFileFolderDuplicate
	}
	folder := fileFolderRecord{
		ID:        fmt.Sprintf("folder-%d", time.Now().UnixNano()),
		Name:      normalized,
		ParentID:  parentID,
		CreatedBy: strings.TrimSpace(createdBy),
		TenantID:  strings.TrimSpace(tenantID), CreatedByPersonID: strings.TrimSpace(personID),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	s.folders = append(s.folders, folder)
	if err := s.persistLocked(); err != nil {
		if errors.Is(err, ErrDurableReplaceAmbiguous) {
			_ = s.reloadVisibleStateLocked()
			return fileFolderRecord{}, err
		}
		s.folders = s.folders[:len(s.folders)-1]
		return fileFolderRecord{}, err
	}
	return folder, nil
}

func (s *fileFolderStore) rename(id string, name string) (fileFolderRecord, error) {
	normalized, err := normalizeFileFolderName(name)
	if err != nil {
		return fileFolderRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return fileFolderRecord{}, s.loadErr
	}
	index := s.folderIndexLocked(strings.TrimSpace(id))
	if index < 0 {
		return fileFolderRecord{}, errFileFolderNotFound
	}
	if s.nameTakenLocked(normalized, s.folders[index].ID, s.folders[index].ParentID) {
		return fileFolderRecord{}, errFileFolderDuplicate
	}
	prior := s.folders[index].Name
	s.folders[index].Name = normalized
	if err := s.persistLocked(); err != nil {
		if errors.Is(err, ErrDurableReplaceAmbiguous) {
			_ = s.reloadVisibleStateLocked()
			return fileFolderRecord{}, err
		}
		s.folders[index].Name = prior
		return fileFolderRecord{}, err
	}
	return s.folders[index], nil
}

// remove deletes a folder and drops its assignments — the filed rows fall
// back to root, they are never deleted with the label.
func (s *fileFolderStore) remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return s.loadErr
	}
	index := s.folderIndexLocked(strings.TrimSpace(id))
	if index < 0 {
		return errFileFolderNotFound
	}
	priorFolders := append([]fileFolderRecord(nil), s.folders...)
	priorAssignments := cloneFileFolderAssignments(s.assignments)
	priorAssignmentUpdatedAt := cloneFileFolderAssignments(s.assignmentUpdatedAt)
	removedFolder := s.folders[index]
	folderID := removedFolder.ID
	parentID := removedFolder.ParentID
	objects := make([]CanonicalImportedObject, 0, 1+len(s.assignments))
	folderObject, err := canonicalFileFolderImportedObject(removedFolder)
	if err != nil {
		return err
	}
	objects = append(objects, folderObject)
	fileIDs := make([]string, 0)
	for fileID, assigned := range s.assignments {
		if assigned == folderID {
			fileIDs = append(fileIDs, fileID)
		}
	}
	sort.Strings(fileIDs)
	stamp := time.Now().UTC()
	stampedLegacyAssignments := false
	if s.assignmentUpdatedAt == nil {
		s.assignmentUpdatedAt = map[string]string{}
	}
	for _, fileID := range fileIDs {
		if strings.TrimSpace(s.assignmentUpdatedAt[fileID]) == "" {
			s.assignmentUpdatedAt[fileID] = stamp.Format(time.RFC3339Nano)
			stampedLegacyAssignments = true
		}
	}
	if stampedLegacyAssignments {
		if err := s.persistLocked(); err != nil {
			s.assignmentUpdatedAt = priorAssignmentUpdatedAt
			return fmt.Errorf("persist file-folder assignment generation: %w", err)
		}
		priorAssignmentUpdatedAt = cloneFileFolderAssignments(s.assignmentUpdatedAt)
	}
	for _, fileID := range fileIDs {
		assignment, err := canonicalFileAssignmentImportedObject(fileID, folderID)
		if err != nil {
			return err
		}
		objects = append(objects, assignment)
	}
	deletionAt := time.Now().UTC()
	if !deletionAt.After(stamp) {
		deletionAt = stamp.Add(time.Nanosecond)
	}
	records := canonicalLifecycleDeletionRecords(objects, deletionAt, "file_folder_deleted")
	if err := withCanonicalLifecycleJournalBatch(canonicalDeletedLifecycleJournalPath(), records, func() error {
		s.folders = append(s.folders[:index], s.folders[index+1:]...)
		for child := range s.folders {
			if s.folders[child].ParentID == folderID {
				s.folders[child].ParentID = parentID
			}
		}
		for fileID, assigned := range s.assignments {
			if assigned == folderID {
				delete(s.assignments, fileID)
				delete(s.assignmentUpdatedAt, fileID)
			}
		}
		if err := s.persistLocked(); err != nil {
			if errors.Is(err, ErrDurableReplaceAmbiguous) {
				_ = s.reloadVisibleStateLocked()
				return err
			}
			s.folders = priorFolders
			s.assignments = priorAssignments
			s.assignmentUpdatedAt = priorAssignmentUpdatedAt
			return err
		}
		return nil
	}); err != nil {
		return fmt.Errorf("journal file-folder deletion %s: %w", folderID, err)
	}
	return nil
}

func recoverJournaledFileFolderDeletions(path string, state fileFolderStoreState) (fileFolderStoreState, bool, error) {
	latest, err := latestCommittedCanonicalLifecycleRecords(canonicalDeletedLifecycleJournalPath(), "file_folder", "file_assignment")
	if err != nil {
		return fileFolderStoreState{}, false, fmt.Errorf("read file-folder lifecycle recovery: %w", err)
	}
	if len(latest) == 0 {
		return state, false, nil
	}
	changed := false
	removedParents := map[string]string{}
	keptFolders := make([]fileFolderRecord, 0, len(state.Folders))
	for _, folder := range state.Folders {
		journal, exists := latest["file_folder\x00"+folder.ID]
		if !exists {
			keptFolders = append(keptFolders, folder)
			continue
		}
		object, objectErr := canonicalFileFolderImportedObject(folder)
		if objectErr != nil {
			return fileFolderStoreState{}, false, objectErr
		}
		if !object.OccurredAt.IsZero() && object.OccurredAt.After(journal.At) {
			keptFolders = append(keptFolders, folder)
			continue
		}
		if object.StateDigest == journal.StateDigest {
			removedParents[folder.ID] = folder.ParentID
			changed = true
			continue
		}
		return fileFolderStoreState{}, false, fmt.Errorf("file-folder lifecycle recovery state mismatch for %s", folder.ID)
	}
	for index := range keptFolders {
		for {
			parent, removed := removedParents[keptFolders[index].ParentID]
			if !removed {
				break
			}
			keptFolders[index].ParentID = parent
		}
	}
	assignments := cloneFileFolderAssignments(state.Assignments)
	assignmentUpdatedAt := cloneFileFolderAssignments(state.AssignmentUpdatedAt)
	for fileID, folderID := range state.Assignments {
		object, objectErr := canonicalFileAssignmentImportedObject(fileID, folderID)
		if objectErr != nil {
			return fileFolderStoreState{}, false, objectErr
		}
		journal, exists := latest["file_assignment\x00"+object.ObjectID]
		_, folderWasDeleted := latest["file_folder\x00"+folderID]
		if folderWasDeleted && !exists {
			return fileFolderStoreState{}, false, fmt.Errorf("file-folder lifecycle recovery lacks assignment evidence for %s", object.ObjectID)
		}
		if !exists {
			continue
		}
		updatedAt, timeErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(assignmentUpdatedAt[fileID]))
		if timeErr != nil {
			return fileFolderStoreState{}, false, fmt.Errorf("file-assignment lifecycle recovery lacks generation for %s", object.ObjectID)
		}
		if updatedAt.After(journal.At) {
			continue
		}
		if journal.StateDigest != object.StateDigest {
			return fileFolderStoreState{}, false, fmt.Errorf("file-assignment lifecycle recovery state mismatch for %s", object.ObjectID)
		}
		delete(assignments, fileID)
		delete(assignmentUpdatedAt, fileID)
		changed = true
	}
	return fileFolderStoreState{Folders: keptFolders, Assignments: assignments, AssignmentUpdatedAt: assignmentUpdatedAt}, changed, nil
}

func (s *fileFolderStore) folderDepthLocked(id string) int {
	depth := 0
	seen := map[string]bool{}
	for id != "" {
		if seen[id] {
			return fileFolderMaxDepth + 1
		}
		seen[id] = true
		index := s.folderIndexLocked(id)
		if index < 0 {
			return fileFolderMaxDepth + 1
		}
		depth++
		id = s.folders[index].ParentID
	}
	return depth
}

func (s *fileFolderStore) folderTreeValidLocked() bool {
	ids := make(map[string]bool, len(s.folders))
	for _, folder := range s.folders {
		if strings.TrimSpace(folder.ID) == "" || ids[folder.ID] {
			return false
		}
		ids[folder.ID] = true
	}
	for index, folder := range s.folders {
		if folder.ParentID != "" && !ids[folder.ParentID] {
			return false
		}
		if s.folderDepthLocked(folder.ID) > fileFolderMaxDepth {
			return false
		}
		for prior := 0; prior < index; prior++ {
			if s.folders[prior].ParentID == folder.ParentID && strings.EqualFold(s.folders[prior].Name, folder.Name) {
				return false
			}
		}
	}
	return true
}

// assign files a row under a folder; an empty folderID moves it back to root.
// The row id is taken on faith (any source's id qualifies) — a stale one just
// becomes a dangling assignment the readers ignore.
func (s *fileFolderStore) assign(fileID string, folderID string) error {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return errFileFolderFileID
	}
	folderID = strings.TrimSpace(folderID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return s.loadErr
	}
	if folderID == "" {
		prior, ok := s.assignments[fileID]
		if !ok {
			return nil
		}
		delete(s.assignments, fileID)
		priorUpdatedAt, hadUpdatedAt := s.assignmentUpdatedAt[fileID]
		delete(s.assignmentUpdatedAt, fileID)
		if err := s.persistLocked(); err != nil {
			if errors.Is(err, ErrDurableReplaceAmbiguous) {
				_ = s.reloadVisibleStateLocked()
				return err
			}
			s.assignments[fileID] = prior
			if hadUpdatedAt {
				s.assignmentUpdatedAt[fileID] = priorUpdatedAt
			}
			return err
		}
		return nil
	}
	if s.folderIndexLocked(folderID) < 0 {
		return errFileFolderNotFound
	}
	if s.assignments == nil {
		s.assignments = map[string]string{}
	}
	prior, hadPrior := s.assignments[fileID]
	priorUpdatedAt, hadUpdatedAt := s.assignmentUpdatedAt[fileID]
	s.assignments[fileID] = folderID
	if s.assignmentUpdatedAt == nil {
		s.assignmentUpdatedAt = map[string]string{}
	}
	s.assignmentUpdatedAt[fileID] = time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.persistLocked(); err != nil {
		if errors.Is(err, ErrDurableReplaceAmbiguous) {
			_ = s.reloadVisibleStateLocked()
			return err
		}
		if hadPrior {
			s.assignments[fileID] = prior
		} else {
			delete(s.assignments, fileID)
		}
		if hadUpdatedAt {
			s.assignmentUpdatedAt[fileID] = priorUpdatedAt
		} else {
			delete(s.assignmentUpdatedAt, fileID)
		}
		return err
	}
	return nil
}

// list returns the folders in creation order (a copy — callers never see the
// guarded slice).
func (s *fileFolderStore) list() []fileFolderRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]fileFolderRecord(nil), s.folders...)
}

// snapshot returns copies of both halves for read-time decoration.
func (s *fileFolderStore) snapshot() ([]fileFolderRecord, map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	assignments := make(map[string]string, len(s.assignments))
	for fileID, folderID := range s.assignments {
		assignments[fileID] = folderID
	}
	return append([]fileFolderRecord(nil), s.folders...), assignments
}

var (
	fileFolderStoreMu    sync.Mutex
	fileFolderStoreCache = map[string]*fileFolderStore{}
)

// fileFoldersFilePath derives the side-store path the way sessionsFilePath
// does: next to the memory JSONL, with an env override.
func fileFoldersFilePath() string {
	if path := strings.TrimSpace(os.Getenv("BONFIRE_FILE_FOLDERS_PATH")); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "file-folders.json")
}

func sharedFileFolderStore() *fileFolderStore {
	path := fileFoldersFilePath()
	fileFolderStoreMu.Lock()
	defer fileFolderStoreMu.Unlock()
	if store, ok := fileFolderStoreCache[path]; ok {
		return store
	}
	store := newFileFolderStore(path)
	fileFolderStoreCache[path] = store
	return store
}

// listFileFolders / createFileFolder / moveFileToFolder are the clean seams a
// Scout organize tool calls — the same store, the same laws as the HTTP doors.
func listFileFolders() []fileFolderRecord {
	return sharedFileFolderStore().list()
}

func createFileFolder(name string, createdBy string) (fileFolderRecord, error) {
	return sharedFileFolderStore().create(name, createdBy)
}

func moveFileToFolder(fileID string, folderID string) error {
	return sharedFileFolderStore().assign(fileID, folderID)
}

// assistantFileFolderPayload is one folder chip on the Files surface: the
// stored record plus the count of visible rows filed under it.
type assistantFileFolderPayload struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parentId,omitempty"`
	Count    int    `json:"count"`
}

// decorateAssistantFileFolders stamps each visible row's folderId from the
// assignment map (dangling assignments are ignored — the row simply reads as
// root) and returns every folder with its visible-row count for the GET
// /assistant/files payload.
func decorateAssistantFileFolders(rows []assistantFileRecord) []assistantFileFolderPayload {
	return decorateAssistantFileFoldersForTenant(rows, "")
}

func decorateAssistantFileFoldersForTenant(rows []assistantFileRecord, tenantID string) []assistantFileFolderPayload {
	folders, assignments := sharedFileFolderStore().snapshot()
	counts := make(map[string]int, len(folders))
	for _, folder := range folders {
		if tenantID != "" && folder.TenantID != tenantID {
			continue
		}
		counts[folder.ID] = 0
	}
	for index := range rows {
		folderID := assignments[rows[index].ID]
		if _, ok := counts[folderID]; !ok {
			continue
		}
		rows[index].FolderID = folderID
		counts[folderID]++
	}
	payload := make([]assistantFileFolderPayload, 0, len(folders))
	for _, folder := range folders {
		if tenantID != "" && folder.TenantID != tenantID {
			continue
		}
		payload = append(payload, assistantFileFolderPayload{ID: folder.ID, Name: folder.Name, ParentID: folder.ParentID, Count: counts[folder.ID]})
	}
	return payload
}

// fileFolderErrorStatus maps store errors onto honest HTTP statuses.
func fileFolderErrorStatus(err error) int {
	switch {
	case errors.Is(err, errFileFolderNotFound):
		return http.StatusNotFound
	case errors.Is(err, errFileFolderDuplicate):
		return http.StatusConflict
	case errors.Is(err, errFileFolderName), errors.Is(err, errFileFolderLimit), errors.Is(err, errFileFolderFileID):
		return http.StatusBadRequest
	default:
		return http.StatusServiceUnavailable
	}
}

func fileFolderPublicError(err error) (int, string) {
	status := fileFolderErrorStatus(err)
	if status == http.StatusServiceUnavailable || status == http.StatusInternalServerError {
		log.Errorf("File-folder mutation failed: %v", err)
		return status, "file folders are unavailable"
	}
	return status, err.Error()
}

// assistantFileFoldersHandler serves /assistant/files/folders — POST creates,
// PATCH renames, DELETE (?id=…) removes. Gate pattern of assistantFilesHandler:
// method, origin, session. Every mutation fans out the "file" refresh event so
// open Files surfaces re-pull.
func assistantFileFoldersHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost, http.MethodPatch, http.MethodDelete:
	default:
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
	if !strideE10TenantSurfaceUseBound(r.Context(), StrideE10TenantSurfaceDrive) {
		err := withStrideE10TenantRequestUse(r, StrideE10TenantSurfaceDrive, func(ctx context.Context, _ *StrideE10TenantPrincipal) error {
			assistantFileFoldersHandler(w, r.WithContext(ctx))
			return nil
		})
		if err != nil {
			writeStrideE10TenantHookError(w, err, "file folders are unavailable")
		}
		return
	}
	principal, canonical := strideE10TenantPrincipalFromContext(r.Context())

	store := sharedFileFolderStore()
	switch r.Method {
	case http.MethodPost:
		payload := struct {
			Name     string `json:"name"`
			ParentID string `json:"parentId,omitempty"`
		}{}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
			writeAuthError(w, http.StatusBadRequest, "could not read folder request")
			return
		}
		createdBy := normalizeAccountEmail(user.Email)
		parentAllowed := fileFolderManagedByUser(payload.ParentID, user)
		if canonical {
			parentAllowed = fileFolderManagedByPrincipal(payload.ParentID, principal)
		}
		if payload.ParentID != "" && !parentAllowed {
			writeAuthError(w, http.StatusNotFound, "folder not found")
			return
		}
		var folder fileFolderRecord
		var err error
		if canonical {
			folder, err = store.createInParentForPrincipal(payload.Name, payload.ParentID, principal)
		} else {
			folder, err = store.createInParent(payload.Name, payload.ParentID, createdBy)
		}
		if err != nil {
			status, message := fileFolderPublicError(err)
			writeAuthError(w, status, message)
			return
		}
		broadcastSignedInKanbanEvent("file", map[string]any{"kind": "folders"})
		folderPayload := any(folder)
		if canonical {
			folderPayload = assistantFileFolderPayload{ID: folder.ID, Name: folder.Name, ParentID: folder.ParentID}
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "folder": folderPayload})
	case http.MethodPatch:
		payload := struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}{}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
			writeAuthError(w, http.StatusBadRequest, "could not read folder request")
			return
		}
		managed := fileFolderManagedByUser(payload.ID, user)
		if canonical {
			managed = fileFolderManagedByPrincipal(payload.ID, principal)
		}
		if !managed {
			writeAuthError(w, http.StatusNotFound, "folder not found")
			return
		}
		folder, err := store.rename(payload.ID, payload.Name)
		if err != nil {
			status, message := fileFolderPublicError(err)
			writeAuthError(w, status, message)
			return
		}
		broadcastSignedInKanbanEvent("file", map[string]any{"kind": "folders"})
		folderPayload := any(folder)
		if canonical {
			folderPayload = assistantFileFolderPayload{ID: folder.ID, Name: folder.Name, ParentID: folder.ParentID}
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "folder": folderPayload})
	case http.MethodDelete:
		managed := fileFolderManagedByUser(r.URL.Query().Get("id"), user)
		if canonical {
			managed = fileFolderManagedByPrincipal(r.URL.Query().Get("id"), principal)
		}
		if !managed {
			writeAuthError(w, http.StatusNotFound, "folder not found")
			return
		}
		if err := store.remove(r.URL.Query().Get("id")); err != nil {
			status, message := fileFolderPublicError(err)
			writeAuthError(w, status, message)
			return
		}
		broadcastSignedInKanbanEvent("file", map[string]any{"kind": "folders"})
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// assistantFileMoveHandler serves POST /assistant/files/move — file a row
// under a folder, or back to root with an empty folderId. Same gates as the
// folder door.
func assistantFileMoveHandler(w http.ResponseWriter, r *http.Request) {
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
	if !strideE10TenantSurfaceUseBound(r.Context(), StrideE10TenantSurfaceDrive) {
		err := withStrideE10TenantRequestUse(r, StrideE10TenantSurfaceDrive, func(ctx context.Context, _ *StrideE10TenantPrincipal) error {
			assistantFileMoveHandler(w, r.WithContext(ctx))
			return nil
		})
		if err != nil {
			writeStrideE10TenantHookError(w, err, "files are unavailable")
		}
		return
	}
	principal, canonical := strideE10TenantPrincipalFromContext(r.Context())

	payload := struct {
		FileID   string `json:"fileId"`
		FolderID string `json:"folderId"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read move request")
		return
	}
	payload.FileID = strings.TrimSpace(payload.FileID)
	payload.FolderID = strings.TrimSpace(payload.FolderID)
	if payload.FileID == "" {
		writeAuthError(w, http.StatusBadRequest, errFileFolderFileID.Error())
		return
	}
	row, rowWritable := authorizedFileRowForMove(r.Context(), user, payload.FileID)
	if row.ID == "" {
		writeAuthError(w, http.StatusNotFound, "file not found")
		return
	}
	if payload.FolderID != "" {
		folderAllowed := fileFolderManagedByUser(payload.FolderID, user)
		if canonical {
			folderAllowed = fileFolderManagedByPrincipal(payload.FolderID, principal)
		}
		if !rowWritable || !folderAllowed {
			writeAuthError(w, http.StatusNotFound, "file not found")
			return
		}
	} else if !rowWritable {
		if row.ArtifactID != "" {
			writeAuthError(w, http.StatusNotFound, "file not found")
			return
		}
		_, assignments := sharedFileFolderStore().snapshot()
		currentFolderID := assignments[payload.FileID]
		if currentFolderID == "" || !fileFolderManagedByUser(currentFolderID, user) {
			writeAuthError(w, http.StatusNotFound, "file not found")
			return
		}
	}
	if err := sharedFileFolderStore().assign(payload.FileID, payload.FolderID); err != nil {
		status, message := fileFolderPublicError(err)
		writeAuthError(w, status, message)
		return
	}
	broadcastSignedInKanbanEvent("file", map[string]any{"kind": "folders"})
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func fileFolderManagedByUser(folderID string, user *userAccount) bool {
	if user == nil {
		return false
	}
	if isArtifactApprovalAdmin(user) {
		return true
	}
	for _, folder := range listFileFolders() {
		if folder.ID == strings.TrimSpace(folderID) {
			creator := strings.TrimSpace(folder.CreatedBy)
			if canonicalAuthenticatedPrincipal(creator) {
				return normalizeAccountEmail(creator) == normalizeAccountEmail(user.Email)
			}
			creatorEmail, ok := canonicalEmailForUniqueRosterName(creator)
			return ok && creatorEmail == normalizeAccountEmail(user.Email)
		}
	}
	return false
}

func fileFolderManagedByPrincipal(folderID string, principal StrideE10TenantPrincipal) bool {
	if !strideIdentifier(principal.TenantID) || !strideIdentifier(principal.PersonID) {
		return false
	}
	for _, folder := range listFileFolders() {
		if folder.ID == strings.TrimSpace(folderID) {
			return folder.TenantID == principal.TenantID && folder.CreatedByPersonID == principal.PersonID
		}
	}
	return false
}

// authorizedFileRowForMove resolves through the exact Files visibility seam.
// Direct uploads are legacy team-readable but uploader-write; chat files are
// uploader-write after their thread visibility gate; deliverables additionally
// require exact canonical artifact read+write authorization.
var fileMoveChatThreadBodyProbe func(string)

func authorizedFileRowForMove(ctx context.Context, user *userAccount, fileID string) (assistantFileRecord, bool) {
	if user == nil || kanbanApp == nil || strings.TrimSpace(fileID) == "" {
		return assistantFileRecord{}, false
	}
	fileID = strings.TrimSpace(fileID)
	principal, canonical := strideE10TenantPrincipalFromContext(ctx)

	// Artifacts have their own exact header/revision authorizer. The body is
	// only projected into a Files row after both read and write are allowed.
	if kanbanApp.memory != nil {
		if _, found := kanbanApp.memory.artifactAuthorizationHeaderByID(fileID); found {
			artifact, ok := authorizedArtifactForActions(ctx, user, fileID, ACLReadContent, ACLWrite)
			if !ok {
				return assistantFileRecord{}, false
			}
			row, visible := fileDeliverableRecord(artifact)
			return row, visible
		}
	}

	// Direct files are classified from metadata only. Their company-readable,
	// uploader-write legacy policy never needs Entry.Text.
	if kanbanApp.memory != nil {
		kanbanApp.memory.mu.Lock()
		for index := len(kanbanApp.memory.entries) - 1; index >= 0; index-- {
			entry := &kanbanApp.memory.entries[index]
			if entry.Kind != meetingMemoryKindFile || entry.ID != fileID {
				continue
			}
			if canonical && strings.TrimSpace(entry.Metadata["tenantId"]) != principal.TenantID {
				kanbanApp.memory.mu.Unlock()
				return assistantFileRecord{}, false
			}
			metadata := make(map[string]string, len(entry.Metadata))
			for key, value := range entry.Metadata {
				metadata[key] = value
			}
			header := meetingMemoryEntry{ID: entry.ID, Kind: entry.Kind, CreatedAt: entry.CreatedAt, Metadata: metadata}
			kanbanApp.memory.mu.Unlock()
			row := fileRecordFromEntry(header)
			writable := false
			if canonical {
				writable = strings.TrimSpace(metadata["uploaderPersonId"]) == principal.PersonID
				row.UploaderEmail = ""
			} else {
				writable = isArtifactApprovalAdmin(user) || normalizeAccountEmail(row.UploaderEmail) != "" && normalizeAccountEmail(row.UploaderEmail) == normalizeAccountEmail(user.Email)
			}
			return row, writable
		}
		kanbanApp.memory.mu.Unlock()
	}

	// Attachment ids are <thread>:<message>:<index>. Parse the exact source,
	// authorize its owner/visibility metadata header, and only then decode that
	// one thread body.
	threadID, messageID, fileIndex, parsed := parseChatAttachmentFileID(fileID)
	if canonical {
		return assistantFileRecord{}, false
	}
	if !parsed || kanbanApp.memory == nil {
		return assistantFileRecord{}, false
	}
	var threadEntry meetingMemoryEntry
	kanbanApp.memory.mu.Lock()
	for index := len(kanbanApp.memory.entries) - 1; index >= 0; index-- {
		entry := &kanbanApp.memory.entries[index]
		if entry.Kind != meetingMemoryKindScoutChat || entry.ID != threadID {
			continue
		}
		if normalizeAccountEmail(entry.Metadata["ownerEmail"]) == "" || !scoutChatThreadMetadataAllowsViewer(entry.Metadata, user.Email) {
			kanbanApp.memory.mu.Unlock()
			return assistantFileRecord{}, false
		}
		threadEntry = cloneMemoryEntry(*entry)
		break
	}
	kanbanApp.memory.mu.Unlock()
	if threadEntry.ID == "" {
		return assistantFileRecord{}, false
	}
	if fileMoveChatThreadBodyProbe != nil {
		fileMoveChatThreadBodyProbe(threadEntry.ID)
	}
	thread, decoded := decodeScoutChatThreadEntry(threadEntry)
	if !decoded {
		return assistantFileRecord{}, false
	}
	sourceFound := false
	for _, message := range thread.Messages {
		if message.ID == messageID && fileIndex < len(message.Files) {
			file := message.Files[fileIndex]
			sourceFound = strings.TrimSpace(file.Ref) != "" || strings.TrimSpace(file.Text) != ""
			break
		}
	}
	if !sourceFound {
		return assistantFileRecord{}, false
	}
	for _, row := range kanbanApp.fileRecordsFromThread(user.Email, thread) {
		if row.ID != fileID {
			continue
		}
		writable := isArtifactApprovalAdmin(user) || normalizeAccountEmail(row.UploaderEmail) == normalizeAccountEmail(user.Email) || normalizeAccountEmail(thread.OwnerEmail) == normalizeAccountEmail(user.Email)
		return row, writable
	}
	return assistantFileRecord{}, false
}

func parseChatAttachmentFileID(fileID string) (string, string, int, bool) {
	last := strings.LastIndex(fileID, ":")
	if last <= 0 || last == len(fileID)-1 {
		return "", "", 0, false
	}
	fileIndex, err := strconv.Atoi(fileID[last+1:])
	if err != nil || fileIndex < 0 {
		return "", "", 0, false
	}
	prefix := fileID[:last]
	middle := strings.LastIndex(prefix, ":")
	if middle <= 0 || middle == len(prefix)-1 {
		return "", "", 0, false
	}
	return prefix[:middle], prefix[middle+1:], fileIndex, true
}
