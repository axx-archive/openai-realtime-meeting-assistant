package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestManagementAuthorizationCreatorMemberAndAdmin(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("BONFIRE_ROOMS_PATH", filepath.Join(t.TempDir(), "rooms.json"))
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", filepath.Join(t.TempDir(), "folders.json"))
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	creator := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	noncreator := loginAs(t, "tom@shareability.com", "B0NFIRE!")
	admin := loginAs(t, "aj@shareability.com", "B0NFIRE!")

	createdRoom := doRoomsRequest(t, roomsHandler, http.MethodPost, "/rooms", `{"name":"Creator room","guestAccess":true}`, creator)
	if createdRoom.Code != http.StatusOK {
		t.Fatalf("room create=%d %s", createdRoom.Code, createdRoom.Body.String())
	}
	var roomPayload struct {
		Room map[string]any `json:"room"`
	}
	if err := json.Unmarshal(createdRoom.Body.Bytes(), &roomPayload); err != nil {
		t.Fatal(err)
	}
	roomID := fmt.Sprint(roomPayload.Room["id"])
	if listed := doRoomsRequest(t, roomsHandler, http.MethodGet, "/rooms", "", noncreator); listed.Code != http.StatusOK || !json.Valid(listed.Body.Bytes()) {
		t.Fatalf("noncreator room list=%d %s", listed.Code, listed.Body.String())
	}
	if denied := doRoomsRequest(t, roomActionHandler, http.MethodPost, "/rooms/"+roomID+"/passcode", `{"passcode":"secret"}`, noncreator); denied.Code != http.StatusNotFound {
		t.Fatalf("noncreator room manage=%d %s", denied.Code, denied.Body.String())
	}
	if allowed := doRoomsRequest(t, roomActionHandler, http.MethodPost, "/rooms/"+roomID+"/passcode", `{"passcode":"secret"}`, creator); allowed.Code != http.StatusOK {
		t.Fatalf("creator room manage=%d %s", allowed.Code, allowed.Body.String())
	}
	if allowed := doRoomsRequest(t, roomActionHandler, http.MethodPost, "/rooms/"+roomID+"/archive", `{}`, admin); allowed.Code != http.StatusOK {
		t.Fatalf("admin room manage=%d %s", allowed.Code, allowed.Body.String())
	}

	createdPackage := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/packages", `{"name":"Creator package","thesis":"test"}`, creator, assistantPackagesHandler)
	if createdPackage.Code != http.StatusOK {
		t.Fatalf("package create=%d %s", createdPackage.Code, createdPackage.Body.String())
	}
	var packagePayload struct {
		Package map[string]any `json:"package"`
	}
	if err := json.Unmarshal(createdPackage.Body.Bytes(), &packagePayload); err != nil {
		t.Fatal(err)
	}
	packageID := fmt.Sprint(packagePayload.Package["id"])
	header, found := kanbanApp.packageAuthorizationHeaderByID(packageID)
	if !found || header.OwnerEmail != "tim@shareability.com" {
		t.Fatalf("package owner header=%+v found=%v", header, found)
	}
	updateBody := `{"action":"update","thesis":"updated"}`
	packageBodyReads := 0
	previousPackageProbe := venturePackageDecodeProbe
	venturePackageDecodeProbe = func(string) { packageBodyReads++ }
	if denied := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/packages/"+packageID+"/action", updateBody, noncreator, assistantPackageActionHandler); denied.Code != http.StatusNotFound {
		t.Fatalf("noncreator package mutate=%d %s", denied.Code, denied.Body.String())
	}
	venturePackageDecodeProbe = previousPackageProbe
	if packageBodyReads != 0 {
		t.Fatalf("unauthorized package probe decoded %d package bodies", packageBodyReads)
	}
	if denied := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/packages/"+packageID+"/action", `{`, noncreator, assistantPackageActionHandler); denied.Code != http.StatusNotFound {
		t.Fatalf("noncreator package authorization did not precede body decode: %d %s", denied.Code, denied.Body.String())
	}
	if allowed := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/packages/"+packageID+"/action", updateBody, creator, assistantPackageActionHandler); allowed.Code != http.StatusOK {
		t.Fatalf("creator package mutate=%d %s", allowed.Code, allowed.Body.String())
	}
	if allowed := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/packages/"+packageID+"/action", `{"action":"set_stage","stage":"design"}`, admin, assistantPackageActionHandler); allowed.Code != http.StatusOK {
		t.Fatalf("admin package mutate=%d %s", allowed.Code, allowed.Body.String())
	}
	private, _, err := kanbanApp.createOSArtifactWithMetadata("research", "private attach", "secret", "AJ", map[string]string{"visibility": "private", "requestedBy": "aj@shareability.com"})
	if err != nil {
		t.Fatal(err)
	}
	attachBody := fmt.Sprintf(`{"action":"attach","refType":"artifact","refId":%q}`, private.ID)
	if denied := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/packages/"+packageID+"/action", attachBody, creator, assistantPackageActionHandler); denied.Code != http.StatusNotFound {
		t.Fatalf("unauthorized artifact attach=%d %s", denied.Code, denied.Body.String())
	}
	kanbanApp.mu.Lock()
	kanbanApp.cards = append(kanbanApp.cards, kanbanCard{ID: "card-team-shared", Status: kanbanStatusBacklog, Title: "Shared card", Notes: "card body"})
	kanbanApp.mu.Unlock()
	decision, _, err := kanbanApp.memory.appendDecision("decision-team-shared", "Shared decision body", map[string]string{"status": decisionStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []struct {
		kind string
		id   string
	}{{packageRefTypeCard, "card-team-shared"}, {packageRefTypeDecision, decision.ID}} {
		body := fmt.Sprintf(`{"action":"attach","refType":%q,"refId":%q}`, ref.kind, ref.id)
		if allowed := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/packages/"+packageID+"/action", body, creator, assistantPackageActionHandler); allowed.Code != http.StatusOK {
			t.Fatalf("creator package %s attach=%d %s", ref.kind, allowed.Code, allowed.Body.String())
		}
	}
	if denied := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/packages/"+packageID+"/action", `{"action":"attach","refType":"decision","refId":"missing-decision"}`, creator, assistantPackageActionHandler); denied.Code != http.StatusNotFound {
		t.Fatalf("missing decision attach=%d %s", denied.Code, denied.Body.String())
	}
	publicChannel, err := kanbanApp.createScoutChatThread("tim@shareability.com", "Tim", "public package channel", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	privateChannel, err := kanbanApp.createScoutChatThread("tim@shareability.com", "Tim", "private package thread", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if allowed := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/packages/"+packageID+"/action", fmt.Sprintf(`{"action":"attach","refType":"channel","refId":%q}`, publicChannel.ID), creator, assistantPackageActionHandler); allowed.Code != http.StatusOK {
		t.Fatalf("public channel attach=%d %s", allowed.Code, allowed.Body.String())
	}
	if denied := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/packages/"+packageID+"/action", fmt.Sprintf(`{"action":"attach","refType":"channel","refId":%q}`, privateChannel.ID), creator, assistantPackageActionHandler); denied.Code != http.StatusNotFound {
		t.Fatalf("private channel attach=%d %s", denied.Code, denied.Body.String())
	}

	createdFolder := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/files/folders", `{"name":"Creator folder"}`, creator, assistantFileFoldersHandler)
	if createdFolder.Code != http.StatusOK {
		t.Fatalf("folder create=%d %s", createdFolder.Code, createdFolder.Body.String())
	}
	var folderPayload struct {
		Folder fileFolderRecord `json:"folder"`
	}
	if err := json.Unmarshal(createdFolder.Body.Bytes(), &folderPayload); err != nil {
		t.Fatal(err)
	}
	renameBody := fmt.Sprintf(`{"id":%q,"name":"Renamed"}`, folderPayload.Folder.ID)
	if denied := artifactAuthorizationRequest(t, http.MethodPatch, "/assistant/files/folders", renameBody, noncreator, assistantFileFoldersHandler); denied.Code != http.StatusNotFound {
		t.Fatalf("noncreator folder rename=%d %s", denied.Code, denied.Body.String())
	}
	if allowed := artifactAuthorizationRequest(t, http.MethodPatch, "/assistant/files/folders", renameBody, creator, assistantFileFoldersHandler); allowed.Code != http.StatusOK {
		t.Fatalf("creator folder rename=%d %s", allowed.Code, allowed.Body.String())
	}
	moveBody := fmt.Sprintf(`{"fileId":%q,"folderId":%q}`, private.ID, folderPayload.Folder.ID)
	if denied := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/files/move", moveBody, creator, assistantFileMoveHandler); denied.Code != http.StatusNotFound {
		t.Fatalf("unauthorized private file assignment=%d %s", denied.Code, denied.Body.String())
	}
	if allowed := artifactAuthorizationRequest(t, http.MethodDelete, "/assistant/files/folders?id="+folderPayload.Folder.ID, "", admin, assistantFileFoldersHandler); allowed.Code != http.StatusOK {
		t.Fatalf("admin folder delete=%d %s", allowed.Code, allowed.Body.String())
	}
}

func TestFileMoveAuthorizationUsesVisibleRowAndOwnership(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", filepath.Join(t.TempDir(), "folders.json"))
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	tim := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	tom := loginAs(t, "tom@shareability.com", "B0NFIRE!")

	createFolder := func(cookies []*http.Cookie, name string) fileFolderRecord {
		recorder := artifactAuthorizationRequest(t, http.MethodPost, "/assistant/files/folders", fmt.Sprintf(`{"name":%q}`, name), cookies, assistantFileFoldersHandler)
		if recorder.Code != http.StatusOK {
			t.Fatalf("create folder %s=%d %s", name, recorder.Code, recorder.Body.String())
		}
		var payload struct {
			Folder fileFolderRecord `json:"folder"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Folder
	}
	timFolder := createFolder(tim, "Tim files")
	tomFolder := createFolder(tom, "Tom files")
	direct, _, err := kanbanApp.memory.appendEntry(meetingMemoryKindFile, "file-tim-owned", "private upload body", map[string]string{
		"name": "tim.txt", "uploaderEmail": "tim@shareability.com", "origin": "files", "brainStatus": fileBrainStatusIngested,
	})
	if err != nil {
		t.Fatal(err)
	}
	move := func(cookies []*http.Cookie, fileID, folderID string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"fileId":%q,"folderId":%q}`, fileID, folderID)
		return artifactAuthorizationRequest(t, http.MethodPost, "/assistant/files/move", body, cookies, assistantFileMoveHandler)
	}
	if denied := move(tom, "file-does-not-exist", tomFolder.ID); denied.Code != http.StatusNotFound {
		t.Fatalf("nonexistent file move=%d %s", denied.Code, denied.Body.String())
	}
	if denied := move(tom, direct.ID, tomFolder.ID); denied.Code != http.StatusNotFound {
		t.Fatalf("direct upload nonowner move=%d %s", denied.Code, denied.Body.String())
	}
	if allowed := move(tim, direct.ID, timFolder.ID); allowed.Code != http.StatusOK {
		t.Fatalf("direct upload owner move=%d %s", allowed.Code, allowed.Body.String())
	}
	if denied := move(tom, direct.ID, ""); denied.Code != http.StatusNotFound {
		t.Fatalf("root move by neither row nor folder owner=%d %s", denied.Code, denied.Body.String())
	}
	if err := sharedFileFolderStore().assign(direct.ID, tomFolder.ID); err != nil {
		t.Fatal(err)
	}
	if allowed := move(tom, direct.ID, ""); allowed.Code != http.StatusOK {
		t.Fatalf("root move by current folder manager=%d %s", allowed.Code, allowed.Body.String())
	}

	privateThread, err := kanbanApp.createScoutChatThread("tim@shareability.com", "Tim", "private files", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	privateThread.Messages = append(privateThread.Messages, scoutChatMessageRecord{
		ID: "message-private-file", Kind: "message", AuthorEmail: "tim@shareability.com", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Files: []scoutChatFileAttachment{{Name: "secret.txt", Text: "secret attachment body"}},
	})
	if err := kanbanApp.saveScoutChatThread(privateThread); err != nil {
		t.Fatal(err)
	}
	privateFileID := privateThread.ID + ":message-private-file:0"
	privateThreadBodyReads := 0
	previousThreadProbe := fileMoveChatThreadBodyProbe
	fileMoveChatThreadBodyProbe = func(string) { privateThreadBodyReads++ }
	if denied := move(tom, privateFileID, tomFolder.ID); denied.Code != http.StatusNotFound {
		t.Fatalf("private attachment nonowner move=%d %s", denied.Code, denied.Body.String())
	}
	fileMoveChatThreadBodyProbe = previousThreadProbe
	if privateThreadBodyReads != 0 {
		t.Fatalf("private attachment denial decoded %d thread bodies", privateThreadBodyReads)
	}
}

func TestLegacyPackageOwnerMetadataProjection(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	legacy := venturePackageRecord{ID: "package-legacy-owner", Name: "Legacy", Stage: packageStages[0], CreatedBy: "Tim"}
	encoded, err := encodeVenturePackage(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, appended, err := kanbanApp.memory.appendVenturePackage(legacy.ID, encoded, map[string]string{"name": legacy.Name, "stage": legacy.Stage}); err != nil || !appended {
		t.Fatalf("append legacy package: appended=%v err=%v", appended, err)
	}
	if _, appended, err := kanbanApp.memory.appendVenturePackage("package-unresolved-owner", `{"id":"package-unresolved-owner","name":"Unknown","createdBy":"No Such Person"}`, map[string]string{"name": "Unknown", "stage": packageStages[0]}); err != nil || !appended {
		t.Fatalf("append unresolved package: appended=%v err=%v", appended, err)
	}
	kanbanApp.projectLegacyPackageOwnerMetadataAtBoot()
	if header, found := kanbanApp.packageAuthorizationHeaderByID(legacy.ID); !found || header.OwnerEmail != "tim@shareability.com" {
		t.Fatalf("legacy owner projection=%+v found=%v", header, found)
	}
	if _, found := kanbanApp.packageAuthorizationHeaderByID("package-unresolved-owner"); found {
		t.Fatal("unresolved legacy owner received an authorization header")
	}
}

func TestLegacyManagementNamesResolveUniquelyAndFailClosed(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", filepath.Join(t.TempDir(), "folders.json"))
	tim := accountStore().findUser("tim@shareability.com")
	if tim == nil {
		t.Fatal("Tim missing from roster")
	}
	legacyPackage := venturePackageRecord{CreatedBy: "Tim"}
	legacyFolder, err := sharedFileFolderStore().create("Legacy Tim folder", "Tim")
	if err != nil {
		t.Fatal(err)
	}
	if !packageManagedByUser(legacyPackage, tim) || !fileFolderManagedByUser(legacyFolder.ID, tim) {
		t.Fatal("unique current roster name did not resolve to Tim")
	}
	store := accountStore()
	store.mu.Lock()
	store.users[normalizeAccountEmail(tim.Email)].Name = "Timothy"
	store.mu.Unlock()
	if packageManagedByUser(legacyPackage, tim) || fileFolderManagedByUser(legacyFolder.ID, tim) {
		t.Fatal("renamed roster identity retained stale display-name ownership")
	}
	store.mu.Lock()
	store.users[normalizeAccountEmail(tim.Email)].Name = "Tim"
	store.users["tom@shareability.com"].Name = "Tim"
	store.mu.Unlock()
	if packageManagedByUser(legacyPackage, tim) || fileFolderManagedByUser(legacyFolder.ID, tim) {
		t.Fatal("ambiguous roster display name did not fail closed")
	}
}

func TestFileFolderPersistenceErrorIsOpaque(t *testing.T) {
	status, message := fileFolderPublicError(errors.New("persist /secret/company/path: disk failed"))
	if status != http.StatusServiceUnavailable || message != "file folders are unavailable" {
		t.Fatalf("persistence mapping=(%d,%q)", status, message)
	}
	status, message = packagePublicError(errors.New("rewrite /secret/company/packages.jsonl: disk failed"))
	if status != http.StatusServiceUnavailable || message != "packages are unavailable" {
		t.Fatalf("package persistence mapping=(%d,%q)", status, message)
	}
}

func TestManagementAuthorizationRegistryTruth(t *testing.T) {
	want := map[string]struct {
		actions   []ACLAction
		readsBody bool
	}{
		"http.room_action":              {[]ACLAction{ACLManage}, false},
		"http.assistant.package_action": {[]ACLAction{ACLReadContent, ACLWrite}, true},
		"http.assistant.file_folders":   {[]ACLAction{ACLCreateChild, ACLWrite, ACLDelete}, true},
		"http.assistant.file_move":      {[]ACLAction{ACLReadContent, ACLWrite}, true},
	}
	registered := map[string]AuthorizationSurface{}
	for _, surface := range AuthorizationSurfaces() {
		registered[surface.ID] = surface
	}
	for id, contract := range want {
		surface, ok := registered[id]
		if !ok {
			t.Fatalf("management surface %s missing", id)
		}
		if surface.Status != AuthorizationCanonicalEnforced || !surface.AuthorizeBeforeBodyRead || surface.ReadsBody != contract.readsBody || !equalACLActions(surface.RequiredActions, contract.actions) {
			t.Fatalf("management surface registry drift for %s: %+v want actions=%v readsBody=%v", id, surface, contract.actions, contract.readsBody)
		}
		if id == "http.assistant.package_action" && (!containsString(surface.ObjectFamilies, "chat_thread") || !containsString(surface.ObjectFamilies, "channel")) {
			t.Fatalf("package action registry omits channel resource families: %+v", surface.ObjectFamilies)
		}
	}
}
