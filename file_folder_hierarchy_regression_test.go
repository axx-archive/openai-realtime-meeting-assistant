package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
)

func TestFileFolderHierarchyScopesNamesReparentsAndCapsDepth(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "meeting-memory.jsonl"))
	store := newFileFolderStore(filepath.Join(dir, "hierarchy.json"))
	root, err := store.createInParent("Research", "", "aj@shareability.com")
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.createInParent("Research", root.ID, "aj@shareability.com")
	if err != nil {
		t.Fatalf("same name in child scope: %v", err)
	}
	grandchild, err := store.createInParent("Evidence", child.ID, "aj@shareability.com")
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentID != root.ID || grandchild.ParentID != child.ID {
		t.Fatalf("hierarchy root=%+v child=%+v grandchild=%+v", root, child, grandchild)
	}
	if _, err := store.createInParent("research", root.ID, "aj@shareability.com"); err != errFileFolderDuplicate {
		t.Fatalf("sibling duplicate err=%v, want %v", err, errFileFolderDuplicate)
	}
	if err := store.remove(child.ID); err != nil {
		t.Fatal(err)
	}
	folders, _ := store.snapshot()
	for _, folder := range folders {
		if folder.ID == grandchild.ID && folder.ParentID != root.ID {
			t.Fatalf("grandchild parent after middle delete=%q, want %q", folder.ParentID, root.ID)
		}
	}

	depthStore := newFileFolderStore(filepath.Join(t.TempDir(), "depth.json"))
	parentID := ""
	for depth := 1; depth <= fileFolderMaxDepth; depth++ {
		folder, err := depthStore.createInParent(fmt.Sprintf("Level %d", depth), parentID, "aj@shareability.com")
		if err != nil {
			t.Fatalf("create depth %d: %v", depth, err)
		}
		parentID = folder.ID
	}
	if _, err := depthStore.createInParent("Too Deep", parentID, "aj@shareability.com"); err != errFileFolderLimit {
		t.Fatalf("depth overflow err=%v, want %v", err, errFileFolderLimit)
	}
	if _, err := depthStore.createInParent("Missing Parent", "folder-missing", "aj@shareability.com"); err != errFileFolderNotFound {
		t.Fatalf("missing parent err=%v, want %v", err, errFileFolderNotFound)
	}
}

func TestAssistantFileFolderParentIDHonorsCreatorBoundary(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", filepath.Join(t.TempDir(), "folders.json"))
	ajCookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	timCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")

	rootResponse := postFolderJSON(t, http.MethodPost, "/assistant/files/folders", `{"name":"AJ Root"}`, ajCookies)
	if rootResponse.Code != http.StatusOK {
		t.Fatalf("root status=%d body=%s", rootResponse.Code, rootResponse.Body.String())
	}
	var rootPayload struct {
		Folder fileFolderRecord `json:"folder"`
	}
	if err := json.Unmarshal(rootResponse.Body.Bytes(), &rootPayload); err != nil {
		t.Fatal(err)
	}
	childResponse := postFolderJSON(t, http.MethodPost, "/assistant/files/folders", fmt.Sprintf(`{"name":"AJ Child","parentId":%q}`, rootPayload.Folder.ID), ajCookies)
	if childResponse.Code != http.StatusOK {
		t.Fatalf("child status=%d body=%s", childResponse.Code, childResponse.Body.String())
	}
	var childPayload struct {
		Folder fileFolderRecord `json:"folder"`
	}
	if err := json.Unmarshal(childResponse.Body.Bytes(), &childPayload); err != nil {
		t.Fatal(err)
	}
	if childPayload.Folder.ParentID != rootPayload.Folder.ID {
		t.Fatalf("child parentId=%q, want %q", childPayload.Folder.ParentID, rootPayload.Folder.ID)
	}
	denied := postFolderJSON(t, http.MethodPost, "/assistant/files/folders", fmt.Sprintf(`{"name":"Tim Child","parentId":%q}`, rootPayload.Folder.ID), timCookies)
	if denied.Code != http.StatusNotFound {
		t.Fatalf("cross-owner child status=%d body=%s, want 404", denied.Code, denied.Body.String())
	}
}
