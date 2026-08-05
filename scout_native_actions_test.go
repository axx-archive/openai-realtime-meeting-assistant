package main

import (
	"context"
	"path/filepath"
	"testing"
)

func TestScoutRouterBuildsNativeActionInsteadOfWorkProposal(t *testing.T) {
	output := openAIScoutRouterOutput{
		Route:  "app_action",
		ToolID: "archive_channel",
		Fields: []openAIScoutRouterField{{Key: "channel", Value: "Bonfire Map"}},
	}
	verdict, err := scoutRouterVerdictFromOpenAI(output, "yeah remove it")
	if err != nil {
		t.Fatalf("native action verdict: %v", err)
	}
	if verdict == nil || verdict.action == nil {
		t.Fatalf("verdict=%#v, want native action", verdict)
	}
	if verdict.proposal != nil || verdict.choices != nil {
		t.Fatalf("native action must not mint work: %#v", verdict)
	}
	if verdict.action.ToolID != "archive_channel" || verdict.action.Fields["channel"] != "Bonfire Map" {
		t.Fatalf("action=%#v", verdict.action)
	}
}

func TestScoutRouterDropsFieldsOutsideTheSelectedNativeAction(t *testing.T) {
	action, err := scoutNativeActionFromRouter(openAIScoutRouterOutput{
		ToolID: "archive_channel",
		Fields: []openAIScoutRouterField{
			{Key: "channel", Value: "Bonfire Map"},
			{Key: "text", Value: "smuggled second action"},
		},
	})
	if err != nil {
		t.Fatalf("native action: %v", err)
	}
	if len(action.Fields) != 1 || action.Fields["channel"] != "Bonfire Map" {
		t.Fatalf("fields=%v, want only the selected action's fields", action.Fields)
	}
}

func TestScoutNativeActionArchivesAuthorizedChannelWithReceipt(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	channel, err := app.createScoutChatThread(user.Email, user.Name, "Bonfire Map", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	receipt, changed, err := app.executeScoutNativeAction(context.Background(), user, scoutNativeAction{
		ToolID: "archive_channel",
		Fields: map[string]string{"channel": "Bonfire Map"},
	})
	if err != nil {
		t.Fatalf("archive channel: %v", err)
	}
	if changed {
		t.Fatal("channel archive must not claim a Board mutation")
	}
	if receipt["ok"] != true || receipt["threadId"] != channel.ID || receipt["changed"] != true {
		t.Fatalf("receipt=%v", receipt)
	}
	archived, _, err := app.scoutChatThreadByID(user.Email, channel.ID)
	if err != nil || archived.ArchivedAt == "" {
		t.Fatalf("archived=%+v err=%v", archived, err)
	}
}

func TestScoutNativeActionDeletesFolderLabelButKeepsFilesAtRoot(t *testing.T) {
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", filepath.Join(t.TempDir(), "file-folders.json"))
	app := newIsolatedKanbanBoardApp(t)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	folder, err := createFileFolder("Bonfire Map", user.Email)
	if err != nil {
		t.Fatalf("create folder: %v", err)
	}
	if err := moveFileToFolder("file-1", folder.ID); err != nil {
		t.Fatalf("assign file: %v", err)
	}

	receipt, changed, err := app.executeScoutNativeAction(context.Background(), user, scoutNativeAction{
		ToolID: "delete_file_folder",
		Fields: map[string]string{"folder": "Bonfire Map"},
	})
	if err != nil {
		t.Fatalf("delete folder: %v", err)
	}
	if changed || receipt["ok"] != true || receipt["folderId"] != folder.ID {
		t.Fatalf("receipt=%v changed=%v", receipt, changed)
	}
	folders, assignments := sharedFileFolderStore().snapshot()
	if len(folders) != 0 {
		t.Fatalf("folders=%v, want label deleted", folders)
	}
	if assignments["file-1"] != "" {
		t.Fatalf("assignment=%q, want file returned to root", assignments["file-1"])
	}
}

func TestPrivateRealtimeIncludesRequesterBoundNativeControls(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	seen := map[string]bool{}
	for _, tool := range app.privateRealtimeVoiceTools() {
		seen[asString(tool["name"])] = true
	}
	for _, name := range []string{"archive_channel", "rename_channel", "create_file_folder", "rename_file_folder", "delete_file_folder", "delete_file"} {
		if !seen[name] || !privateRealtimeVoiceToolAllowed(name) {
			t.Fatalf("private Realtime missing %s", name)
		}
	}
	for _, tool := range app.realtimeRoomVoiceTools() {
		if seenName := asString(tool["name"]); seenName == "archive_channel" || seenName == "delete_file_folder" || seenName == "delete_file" {
			t.Fatalf("requester-bound tool %s leaked into shared room voice", seenName)
		}
	}
}
