package main

// Files-surface folders: the side-store's laws (name collapse + bounds,
// case-insensitive dup reject, the 100-folder cap, delete drops assignments,
// atomic persistence surviving a reload), the Scout-seam helpers, and the
// /assistant/files/folders + /assistant/files/move doors' gates and CRUD
// mapping (400/404/409, the "file" refresh broadcast contract is exercised
// implicitly).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFileFolderStoreCRUDAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file-folders.json")
	store := newFileFolderStore(path)

	// Name law: whitespace collapses, 1-60 runes after the collapse.
	folder, err := store.create("  Research   Decks  ", "AJ")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if folder.Name != "Research Decks" || !strings.HasPrefix(folder.ID, "folder-") || folder.CreatedBy != "AJ" || folder.CreatedAt == "" {
		t.Fatalf("folder=%+v, want collapsed name + folder- id + creator stamp", folder)
	}
	if _, err := store.create("   ", "AJ"); err != errFileFolderName {
		t.Fatalf("blank name err=%v, want %v", err, errFileFolderName)
	}
	if _, err := store.create(strings.Repeat("x", fileFolderNameMaxLen+1), "AJ"); err != errFileFolderName {
		t.Fatalf("over-long name err=%v, want %v", err, errFileFolderName)
	}

	// Dup reject is case-insensitive, on create AND rename.
	if _, err := store.create("research decks", "Tom"); err != errFileFolderDuplicate {
		t.Fatalf("dup create err=%v, want %v", err, errFileFolderDuplicate)
	}
	second, err := store.create("Contracts", "Tom")
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if _, err := store.rename(second.ID, "RESEARCH  DECKS"); err != errFileFolderDuplicate {
		t.Fatalf("dup rename err=%v, want %v", err, errFileFolderDuplicate)
	}
	renamed, err := store.rename(second.ID, "  Signed   Contracts ")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if renamed.Name != "Signed Contracts" {
		t.Fatalf("renamed=%+v, want the collapsed new name", renamed)
	}
	if _, err := store.rename("folder-missing", "Anything"); err != errFileFolderNotFound {
		t.Fatalf("rename missing err=%v, want %v", err, errFileFolderNotFound)
	}

	// Assignments take any row id; only the folder must exist.
	if err := store.assign("file-123", folder.ID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := store.assign("os-artifact-research-9", folder.ID); err != nil {
		t.Fatalf("assign artifact row: %v", err)
	}
	if err := store.assign("file-123", "folder-missing"); err != errFileFolderNotFound {
		t.Fatalf("assign to missing folder err=%v, want %v", err, errFileFolderNotFound)
	}
	if err := store.assign("", folder.ID); err != errFileFolderFileID {
		t.Fatalf("assign empty file id err=%v, want %v", err, errFileFolderFileID)
	}
	// "" folder = back to root.
	if err := store.assign("os-artifact-research-9", ""); err != nil {
		t.Fatalf("move to root: %v", err)
	}
	if _, assignments := store.snapshot(); assignments["os-artifact-research-9"] != "" || assignments["file-123"] != folder.ID {
		t.Fatalf("assignments=%v, want only file-123 filed", assignments)
	}

	// The store survives a reload byte-for-byte (tmp+rename persistence).
	reloaded := newFileFolderStore(path)
	folders, assignments := reloaded.snapshot()
	if len(folders) != 2 || folders[0].Name != "Research Decks" || folders[1].Name != "Signed Contracts" {
		t.Fatalf("reloaded folders=%+v, want both in creation order", folders)
	}
	if assignments["file-123"] != folder.ID {
		t.Fatalf("reloaded assignments=%v, want file-123 still filed", assignments)
	}

	// Deleting a folder drops its assignments — filed rows fall back to root.
	if err := reloaded.remove(folder.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := reloaded.remove(folder.ID); err != errFileFolderNotFound {
		t.Fatalf("double remove err=%v, want %v", err, errFileFolderNotFound)
	}
	folders, assignments = reloaded.snapshot()
	if len(folders) != 1 || len(assignments) != 0 {
		t.Fatalf("after delete folders=%+v assignments=%v, want one folder and no assignments", folders, assignments)
	}
}

func TestFileFolderStoreCap(t *testing.T) {
	store := newFileFolderStore(filepath.Join(t.TempDir(), "file-folders.json"))
	for i := 0; i < fileFolderMaxCount; i++ {
		if _, err := store.create(fmt.Sprintf("Folder %03d", i), "AJ"); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if _, err := store.create("One Too Many", "AJ"); err != errFileFolderLimit {
		t.Fatalf("cap err=%v, want %v", err, errFileFolderLimit)
	}
}

func TestFileFolderStoreRollsBackWhenPersistenceFails(t *testing.T) {
	root := t.TempDir()
	validPath := filepath.Join(root, "file-folders.json")
	store := newFileFolderStore(validPath)
	folder, err := store.create("Diligence", "AJ")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.assign("file-1", folder.ID); err != nil {
		t.Fatal(err)
	}
	beforeFolders, beforeAssignments := store.snapshot()

	blockedParent := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blockedParent, []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	store.path = filepath.Join(blockedParent, "file-folders.json")

	if _, err := store.create("Should Roll Back", "AJ"); err == nil {
		t.Fatal("create reported success when persistence failed")
	}
	if _, err := store.rename(folder.ID, "Renamed"); err == nil {
		t.Fatal("rename reported success when persistence failed")
	}
	if err := store.assign("file-2", folder.ID); err == nil {
		t.Fatal("assignment reported success when persistence failed")
	}
	if err := store.assign("file-1", ""); err == nil {
		t.Fatal("move-to-root reported success when persistence failed")
	}
	if err := store.remove(folder.ID); err == nil {
		t.Fatal("remove reported success when persistence failed")
	}

	afterFolders, afterAssignments := store.snapshot()
	if !reflect.DeepEqual(afterFolders, beforeFolders) || !reflect.DeepEqual(afterAssignments, beforeAssignments) {
		t.Fatalf("failed persistence mutated state: folders=%+v assignments=%v", afterFolders, afterAssignments)
	}
	reloaded := newFileFolderStore(validPath)
	reloadedFolders, reloadedAssignments := reloaded.snapshot()
	if !reflect.DeepEqual(reloadedFolders, beforeFolders) || !reflect.DeepEqual(reloadedAssignments, beforeAssignments) {
		t.Fatalf("failed persistence changed durable state: folders=%+v assignments=%v", reloadedFolders, reloadedAssignments)
	}
}

func TestFileFolderStoreReloadsPublishedGenerationAfterAmbiguousSync(t *testing.T) {
	t.Setenv("BONFIRE_CANONICAL_MODE", "shadow")
	path := filepath.Join(t.TempDir(), "file-folders.json")
	store := newFileFolderStore(path)
	previous := syncDirectoryForAtomicWrite
	syncDirectoryForAtomicWrite = func(string) error { return io.ErrClosedPipe }
	t.Cleanup(func() { syncDirectoryForAtomicWrite = previous })

	if _, err := store.create("Published But Unsynced", "aj@example.com"); !errors.Is(err, ErrDurableReplaceAmbiguous) {
		t.Fatalf("create err=%v, want ambiguous published generation", err)
	}
	syncDirectoryForAtomicWrite = previous
	folders, _ := store.snapshot()
	if len(folders) != 1 || folders[0].Name != "Published But Unsynced" {
		t.Fatalf("in-memory state did not reload visible generation: %+v", folders)
	}
	reloaded := newFileFolderStore(path)
	reloadedFolders, _ := reloaded.snapshot()
	if !reflect.DeepEqual(reloadedFolders, folders) {
		t.Fatalf("disk and memory diverged: memory=%+v disk=%+v", folders, reloadedFolders)
	}
	if _, err := store.create("Published But Unsynced", "aj@example.com"); !errors.Is(err, errFileFolderDuplicate) {
		t.Fatalf("retry duplicated ambiguous create: %v", err)
	}
}

func TestFileFolderStoreMalformedStateFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file-folders.json")
	malformed := []byte(`{"folders":[`)
	if err := os.WriteFile(path, malformed, 0o600); err != nil {
		t.Fatal(err)
	}
	store := newFileFolderStore(path)
	if _, err := store.create("Must Not Overwrite", "aj@example.com"); err == nil {
		t.Fatal("malformed store allowed create")
	}
	if err := store.assign("file-1", ""); err == nil {
		t.Fatal("malformed store allowed assignment")
	}
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, malformed) {
		t.Fatalf("malformed source was overwritten: raw=%q err=%v", raw, err)
	}
}

func TestFileFolderScoutSeamHelpers(t *testing.T) {
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", filepath.Join(t.TempDir(), "file-folders.json"))

	folder, err := createFileFolder("Diligence", "Scout")
	if err != nil {
		t.Fatalf("createFileFolder: %v", err)
	}
	if err := moveFileToFolder("file-42", folder.ID); err != nil {
		t.Fatalf("moveFileToFolder: %v", err)
	}
	folders := listFileFolders()
	if len(folders) != 1 || folders[0].ID != folder.ID || folders[0].Name != "Diligence" {
		t.Fatalf("listFileFolders=%+v, want the one created folder", folders)
	}
}

func postFolderJSON(t *testing.T, method string, path string, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	if strings.HasPrefix(path, "/assistant/files/move") {
		assistantFileMoveHandler(recorder, req)
	} else {
		assistantFileFoldersHandler(recorder, req)
	}
	return recorder
}

func TestAssistantFileFolderHandlersGates(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", filepath.Join(t.TempDir(), "file-folders.json"))

	// Method gates.
	recorder := httptest.NewRecorder()
	assistantFileFoldersHandler(recorder, httptest.NewRequest(http.MethodGet, "/assistant/files/folders", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("folders GET status=%d, want 405", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	assistantFileMoveHandler(recorder, httptest.NewRequest(http.MethodGet, "/assistant/files/move", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("move GET status=%d, want 405", recorder.Code)
	}

	// Cross-origin gates.
	crossFolders := httptest.NewRequest(http.MethodPost, "/assistant/files/folders", strings.NewReader(`{"name":"X"}`))
	crossFolders.Header.Set("Origin", "https://evil.example")
	recorder = httptest.NewRecorder()
	assistantFileFoldersHandler(recorder, crossFolders)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin folders status=%d, want 403", recorder.Code)
	}
	crossMove := httptest.NewRequest(http.MethodPost, "/assistant/files/move", strings.NewReader(`{"fileId":"f"}`))
	crossMove.Header.Set("Origin", "https://evil.example")
	recorder = httptest.NewRecorder()
	assistantFileMoveHandler(recorder, crossMove)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin move status=%d, want 403", recorder.Code)
	}

	// Session gates: every signed-out mutation is a 401.
	if recorder := postFolderJSON(t, http.MethodPost, "/assistant/files/folders", `{"name":"X"}`, nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out create status=%d, want 401", recorder.Code)
	}
	if recorder := postFolderJSON(t, http.MethodPatch, "/assistant/files/folders", `{"id":"folder-1","name":"X"}`, nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out rename status=%d, want 401", recorder.Code)
	}
	if recorder := postFolderJSON(t, http.MethodDelete, "/assistant/files/folders?id=folder-1", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out delete status=%d, want 401", recorder.Code)
	}
	if recorder := postFolderJSON(t, http.MethodPost, "/assistant/files/move", `{"fileId":"f","folderId":"folder-1"}`, nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out move status=%d, want 401", recorder.Code)
	}
}

func TestAssistantFileFolderHandlersCRUD(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", filepath.Join(t.TempDir(), "file-folders.json"))

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")

	// Create.
	recorder := postFolderJSON(t, http.MethodPost, "/assistant/files/folders", `{"name":" Board   Packets "}`, cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	var createPayload struct {
		OK     bool             `json:"ok"`
		Folder fileFolderRecord `json:"folder"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &createPayload); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	folder := createPayload.Folder
	if !createPayload.OK || folder.Name != "Board Packets" || !strings.HasPrefix(folder.ID, "folder-") {
		t.Fatalf("create payload=%+v, want ok + collapsed name", createPayload)
	}
	if folder.CreatedBy == "" {
		t.Fatal("created folder must stamp the session user")
	}

	// Dup name → 409; blank name → 400.
	if recorder := postFolderJSON(t, http.MethodPost, "/assistant/files/folders", `{"name":"board packets"}`, cookies); recorder.Code != http.StatusConflict {
		t.Fatalf("dup create status=%d, want 409", recorder.Code)
	}
	if recorder := postFolderJSON(t, http.MethodPost, "/assistant/files/folders", `{"name":"  "}`, cookies); recorder.Code != http.StatusBadRequest {
		t.Fatalf("blank create status=%d, want 400", recorder.Code)
	}

	// Rename.
	recorder = postFolderJSON(t, http.MethodPatch, "/assistant/files/folders", fmt.Sprintf(`{"id":%q,"name":"Investor Packets"}`, folder.ID), cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("rename status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	if recorder := postFolderJSON(t, http.MethodPatch, "/assistant/files/folders", `{"id":"folder-missing","name":"X"}`, cookies); recorder.Code != http.StatusNotFound {
		t.Fatalf("rename missing status=%d, want 404", recorder.Code)
	}

	// Move: file a row, unknown folder is a 404, empty fileId a 400.
	if _, _, err := kanbanApp.memory.appendEntry(meetingMemoryKindFile, "file-1", "File owned by AJ", map[string]string{
		"name": "file-1.txt", "uploaderEmail": "aj@shareability.com", "origin": "files", "brainStatus": fileBrainStatusIngested,
	}); err != nil {
		t.Fatal(err)
	}
	if recorder := postFolderJSON(t, http.MethodPost, "/assistant/files/move", fmt.Sprintf(`{"fileId":"file-1","folderId":%q}`, folder.ID), cookies); recorder.Code != http.StatusOK {
		t.Fatalf("move status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	if recorder := postFolderJSON(t, http.MethodPost, "/assistant/files/move", `{"fileId":"file-1","folderId":"folder-missing"}`, cookies); recorder.Code != http.StatusNotFound {
		t.Fatalf("move to missing folder status=%d, want 404", recorder.Code)
	}
	if recorder := postFolderJSON(t, http.MethodPost, "/assistant/files/move", `{"fileId":"","folderId":""}`, cookies); recorder.Code != http.StatusBadRequest {
		t.Fatalf("move empty fileId status=%d, want 400", recorder.Code)
	}

	// Delete drops the folder AND its assignments.
	if recorder := postFolderJSON(t, http.MethodDelete, "/assistant/files/folders?id="+folder.ID, "", cookies); recorder.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	if recorder := postFolderJSON(t, http.MethodDelete, "/assistant/files/folders?id="+folder.ID, "", cookies); recorder.Code != http.StatusNotFound {
		t.Fatalf("double delete status=%d, want 404", recorder.Code)
	}
	folders, assignments := sharedFileFolderStore().snapshot()
	if len(folders) != 0 || len(assignments) != 0 {
		t.Fatalf("after delete folders=%+v assignments=%v, want empty store", folders, assignments)
	}
}
