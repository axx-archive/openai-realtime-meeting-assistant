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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// fileFolderMaxCount caps the tree flat: a drive with more than 100
	// top-level folders is a filing failure, not a feature.
	fileFolderMaxCount = 100
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
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedBy string `json:"createdBy,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// fileFolderStoreState is the on-disk shape of data/file-folders.json.
type fileFolderStoreState struct {
	Folders     []fileFolderRecord `json:"folders"`
	Assignments map[string]string  `json:"assignments"`
}

// fileFolderStore guards the folder list + row assignments behind one mutex,
// persisting every mutation atomically (tmp+rename, like sessionStore).
type fileFolderStore struct {
	mu          sync.Mutex
	path        string
	folders     []fileFolderRecord
	assignments map[string]string
}

func newFileFolderStore(path string) *fileFolderStore {
	store := &fileFolderStore{path: path, assignments: map[string]string{}}
	if raw, err := os.ReadFile(path); err == nil {
		var state fileFolderStoreState
		if err := json.Unmarshal(raw, &state); err != nil {
			log.Errorf("Ignoring malformed file-folder store at %s: %v", path, err)
		} else {
			store.folders = state.Folders
			if state.Assignments != nil {
				store.assignments = state.Assignments
			}
		}
	}
	return store
}

func (s *fileFolderStore) persistLocked() {
	raw, err := json.MarshalIndent(fileFolderStoreState{
		Folders:     s.folders,
		Assignments: s.assignments,
	}, "", "  ")
	if err != nil {
		log.Errorf("Failed to encode file-folder store: %v", err)
		return
	}
	if err := writeFileAtomicallyForCanonicalMode(s.path, raw, 0o600); err != nil {
		log.Errorf("Failed to persist file-folder store: %v", err)
	}
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

func (s *fileFolderStore) nameTakenLocked(name string, excludeID string) bool {
	for _, folder := range s.folders {
		if folder.ID != excludeID && strings.EqualFold(folder.Name, name) {
			return true
		}
	}
	return false
}

func (s *fileFolderStore) create(name string, createdBy string) (fileFolderRecord, error) {
	normalized, err := normalizeFileFolderName(name)
	if err != nil {
		return fileFolderRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.folders) >= fileFolderMaxCount {
		return fileFolderRecord{}, errFileFolderLimit
	}
	if s.nameTakenLocked(normalized, "") {
		return fileFolderRecord{}, errFileFolderDuplicate
	}
	folder := fileFolderRecord{
		ID:        fmt.Sprintf("folder-%d", time.Now().UnixNano()),
		Name:      normalized,
		CreatedBy: strings.TrimSpace(createdBy),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	s.folders = append(s.folders, folder)
	s.persistLocked()
	return folder, nil
}

func (s *fileFolderStore) rename(id string, name string) (fileFolderRecord, error) {
	normalized, err := normalizeFileFolderName(name)
	if err != nil {
		return fileFolderRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.folderIndexLocked(strings.TrimSpace(id))
	if index < 0 {
		return fileFolderRecord{}, errFileFolderNotFound
	}
	if s.nameTakenLocked(normalized, s.folders[index].ID) {
		return fileFolderRecord{}, errFileFolderDuplicate
	}
	s.folders[index].Name = normalized
	s.persistLocked()
	return s.folders[index], nil
}

// remove deletes a folder and drops its assignments — the filed rows fall
// back to root, they are never deleted with the label.
func (s *fileFolderStore) remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.folderIndexLocked(strings.TrimSpace(id))
	if index < 0 {
		return errFileFolderNotFound
	}
	folderID := s.folders[index].ID
	s.folders = append(s.folders[:index], s.folders[index+1:]...)
	for fileID, assigned := range s.assignments {
		if assigned == folderID {
			delete(s.assignments, fileID)
		}
	}
	s.persistLocked()
	return nil
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
	if folderID == "" {
		if _, ok := s.assignments[fileID]; !ok {
			return nil
		}
		delete(s.assignments, fileID)
		s.persistLocked()
		return nil
	}
	if s.folderIndexLocked(folderID) < 0 {
		return errFileFolderNotFound
	}
	if s.assignments == nil {
		s.assignments = map[string]string{}
	}
	s.assignments[fileID] = folderID
	s.persistLocked()
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
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// decorateAssistantFileFolders stamps each visible row's folderId from the
// assignment map (dangling assignments are ignored — the row simply reads as
// root) and returns every folder with its visible-row count for the GET
// /assistant/files payload.
func decorateAssistantFileFolders(rows []assistantFileRecord) []assistantFileFolderPayload {
	folders, assignments := sharedFileFolderStore().snapshot()
	counts := make(map[string]int, len(folders))
	for _, folder := range folders {
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
		payload = append(payload, assistantFileFolderPayload{ID: folder.ID, Name: folder.Name, Count: counts[folder.ID]})
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
	default:
		return http.StatusBadRequest
	}
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

	store := sharedFileFolderStore()
	switch r.Method {
	case http.MethodPost:
		payload := struct {
			Name string `json:"name"`
		}{}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
			writeAuthError(w, http.StatusBadRequest, "could not read folder request")
			return
		}
		createdBy := firstNonEmptyString(strings.TrimSpace(user.Name), normalizeAccountEmail(user.Email))
		folder, err := store.create(payload.Name, createdBy)
		if err != nil {
			writeAuthError(w, fileFolderErrorStatus(err), err.Error())
			return
		}
		broadcastSignedInKanbanEvent("file", map[string]any{"kind": "folders"})
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "folder": folder})
	case http.MethodPatch:
		payload := struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}{}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
			writeAuthError(w, http.StatusBadRequest, "could not read folder request")
			return
		}
		folder, err := store.rename(payload.ID, payload.Name)
		if err != nil {
			writeAuthError(w, fileFolderErrorStatus(err), err.Error())
			return
		}
		broadcastSignedInKanbanEvent("file", map[string]any{"kind": "folders"})
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "folder": folder})
	case http.MethodDelete:
		if err := store.remove(r.URL.Query().Get("id")); err != nil {
			writeAuthError(w, fileFolderErrorStatus(err), err.Error())
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

	payload := struct {
		FileID   string `json:"fileId"`
		FolderID string `json:"folderId"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read move request")
		return
	}
	if err := sharedFileFolderStore().assign(payload.FileID, payload.FolderID); err != nil {
		writeAuthError(w, fileFolderErrorStatus(err), err.Error())
		return
	}
	broadcastSignedInKanbanEvent("file", map[string]any{"kind": "folders"})
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true})
}
