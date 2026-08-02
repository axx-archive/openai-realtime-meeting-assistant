package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestOfficeChatTypingBroadcastUsesSessionIdentityAndPublicThreadsOnly(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	avatar := "data:image/png;base64,aGVsbG8="
	if _, err := accountStore().updateProfile("tim@shareability.com", "Tim Profile", avatar); err != nil {
		t.Fatalf("update typing profile: %v", err)
	}
	expectedName := participantNameForAccount(accountStore().findUser("tim@shareability.com"))

	timConn := dialIsolatedWebsocket(t, server, "tim@shareability.com")
	ajConn := dialIsolatedWebsocket(t, server, "aj@shareability.com")
	caitlynConn := dialIsolatedWebsocket(t, server, "caitlyn@shareability.com")
	sendOfficeHello(t, timConn)
	sendOfficeHello(t, ajConn)
	sendOfficeHello(t, caitlynConn)
	waitForKanbanEvent(t, timConn, "codex_proposals", 5*time.Second)
	waitForKanbanEvent(t, ajConn, "codex_proposals", 5*time.Second)
	waitForKanbanEvent(t, caitlynConn, "codex_proposals", 5*time.Second)

	publicThread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "team typing", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create public channel: %v", err)
	}
	writeNativeWebsocketEvent(t, timConn, "chat_typing", map[string]any{
		"threadId":      publicThread.ID,
		"typing":        true,
		"email":         "mallory@example.com",
		"name":          "Mallory",
		"avatarDataURL": "data:image/png;base64,ZXZpbA==",
	})
	raw := waitForKanbanEvent(t, ajConn, "chat_typing", 5*time.Second)
	_ = waitForKanbanEvent(t, caitlynConn, "chat_typing", 5*time.Second)
	payload := map[string]any{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode typing event: %v", err)
	}
	if payload["threadId"] != publicThread.ID || payload["typing"] != true || payload["email"] != "tim@shareability.com" || payload["name"] != expectedName || payload["avatarDataURL"] != avatar {
		t.Fatalf("typing payload did not use the authenticated profile: %s", raw)
	}

	project, _, err := kanbanApp.ensureScoutChatThread("stride-typing-project", "aj@shareability.com", "AJ", "Dog Perfect", scoutChatVisibilityPublic, []string{"tim@shareability.com"})
	if err != nil {
		t.Fatalf("create member project: %v", err)
	}
	writeNativeWebsocketEvent(t, timConn, "chat_typing", map[string]any{"threadId": project.ID, "typing": true})
	raw = waitForKanbanEvent(t, ajConn, "chat_typing", 5*time.Second)
	payload = map[string]any{}
	if err := json.Unmarshal(raw, &payload); err != nil || payload["threadId"] != project.ID {
		t.Fatalf("project member missed typing event: raw=%s err=%v", raw, err)
	}
	// Bound the nonmember negative without poisoning the websocket read with a
	// deadline: the next visible event must be the explicit organization marker.
	broadcastSignedInKanbanEvent("chat_typing", map[string]any{"threadId": "project-typing-marker", "typing": false})
	raw = waitForKanbanEvent(t, caitlynConn, "chat_typing", 5*time.Second)
	_ = waitForKanbanEvent(t, ajConn, "chat_typing", 5*time.Second)
	payload = map[string]any{}
	if err := json.Unmarshal(raw, &payload); err != nil || payload["threadId"] != "project-typing-marker" {
		t.Fatalf("member-scoped project typing leaked before marker: raw=%s err=%v", raw, err)
	}

	privateThread, err := kanbanApp.createScoutChatThread("tim@shareability.com", "Tim", "private typing", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	writeNativeWebsocketEvent(t, timConn, "chat_typing", map[string]any{"threadId": privateThread.ID, "typing": true})
	// A marker on the same event type bounds the negative assertion without a
	// read timeout (gorilla websocket connections cannot safely resume reads
	// after a deadline fires).
	broadcastSignedInKanbanEvent("chat_typing", map[string]any{"threadId": "typing-marker", "typing": false})
	raw = waitForKanbanEvent(t, ajConn, "chat_typing", 5*time.Second)
	if string(raw) == "" || string(raw) == "null" {
		t.Fatal("missing typing marker")
	}
	payload = map[string]any{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode typing marker: %v", err)
	}
	if payload["threadId"] != "typing-marker" {
		t.Fatalf("private-thread typing leaked before marker: %s", raw)
	}
}

func TestScoutChatTypingPayloadRejectsArchivedPublicThread(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("MEETING_MEMORY_PATH", t.TempDir()+"/memory.jsonl")
	app := newKanbanBoardApp()
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "archived typing", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create public channel: %v", err)
	}
	thread, err = app.setScoutChatThreadArchived("aj@shareability.com", thread.ID, true)
	if err != nil {
		t.Fatalf("archive channel: %v", err)
	}
	user := accountStore().findUser("aj@shareability.com")
	if _, err := scoutChatTypingEventPayload(app, user, thread.ID, true); err == nil {
		t.Fatal("archived public channel accepted typing presence")
	}
}
