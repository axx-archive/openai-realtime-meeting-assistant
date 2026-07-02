package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Room chat persists as transcript injection even while the recording toggle
// is off: typing is an explicit act, unlike ambient audio.
func TestRoomChatPersistsRegardlessOfRecordingToggle(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	app.setTranscriptRecording(false, "Tom")

	payload, ok := app.recordRoomChatMessage("Tom", "ship the release notes tonight")
	if !ok {
		t.Fatal("recordRoomChatMessage ok=false, want true with recording disabled")
	}
	if payload["name"] != "Tom" {
		t.Fatalf("payload name=%q, want Tom", payload["name"])
	}
	if payload["text"] != "ship the release notes tonight" {
		t.Fatalf("payload text=%q, want the typed message without a speaker prefix", payload["text"])
	}
	id, _ := payload["id"].(string)
	if !strings.HasPrefix(id, "chat-") {
		t.Fatalf("payload id=%q, want durable chat- prefix", id)
	}
	createdAt, _ := payload["createdAt"].(string)
	if _, err := time.Parse(time.RFC3339Nano, createdAt); err != nil {
		t.Fatalf("payload createdAt=%q is not RFC3339Nano: %v", createdAt, err)
	}

	entries := app.memory.snapshot(0)
	if len(entries) != 1 {
		t.Fatalf("memory entries=%d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Kind != meetingMemoryKindTranscript {
		t.Fatalf("entry kind=%q, want transcript", entry.Kind)
	}
	if entry.Metadata["source"] != transcriptSourceRoomChat {
		t.Fatalf("entry source=%q, want room_chat", entry.Metadata["source"])
	}
	if entry.Metadata["speaker"] != "Tom" {
		t.Fatalf("entry speaker=%q, want Tom", entry.Metadata["speaker"])
	}
	if entry.Text != "Tom: ship the release notes tonight" {
		t.Fatalf("entry text=%q, want speaker-prefixed transcript text", entry.Text)
	}
}

// Typed chat bypasses the transcriptLooksUseful filler filter that guards
// spoken transcripts — "ok" is deliberate when typed.
func TestRoomChatBypassesTranscriptUsefulnessFilter(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	if _, ok := app.recordRoomChatMessage("Tyler", "ok"); !ok {
		t.Fatal("short typed chat was dropped; transcriptLooksUseful must not filter room chat")
	}

	// The spoken-transcript path still filters the same text.
	if _, appended, err := app.memory.appendAttributedTranscript("event-filler", "item-filler", "Tyler", "dominant", "ok"); err != nil {
		t.Fatalf("appendAttributedTranscript: %v", err)
	} else if appended {
		t.Fatal("spoken filler transcript appended=true, want the usefulness filter to hold")
	}
}

func TestRoomChatRejectsEmptyAndTrimsOversizeText(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	if _, ok := app.recordRoomChatMessage("Tim", "   \n\t "); ok {
		t.Fatal("whitespace-only chat was persisted, want empty-reject")
	}
	if entries := app.memory.snapshot(0); len(entries) != 0 {
		t.Fatalf("memory entries=%d after empty message, want 0", len(entries))
	}

	oversize := strings.Repeat("é", maxRoomChatMessageRunes+250)
	payload, ok := app.recordRoomChatMessage("Tim", oversize)
	if !ok {
		t.Fatal("oversize chat rejected outright, want trim-and-persist")
	}
	text, _ := payload["text"].(string)
	if got := len([]rune(text)); got != maxRoomChatMessageRunes {
		t.Fatalf("trimmed text runes=%d, want %d", got, maxRoomChatMessageRunes)
	}
}

func TestRoomChatHistoryScopesToCurrentMeetingAndCapsEntries(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	for range 3 {
		if _, ok := app.recordRoomChatMessage("Tom", "note from the previous meeting"); !ok {
			t.Fatal("seed previous-meeting chat failed")
		}
	}
	// Non-chat transcripts must never leak into chat history.
	if _, appended, err := app.memory.appendAttributedTranscript("event-spoken", "item-spoken", "Tyler", "dominant", "Boot Barn spoken update."); err != nil || !appended {
		t.Fatalf("append spoken transcript: appended=%v err=%v", appended, err)
	}
	app.memory.rotateMeetingID()

	total := roomChatHistoryLimit + 5
	for index := range total {
		if _, ok := app.recordRoomChatMessage("Tyler", "current meeting message "+strings.Repeat("x", index+1)); !ok {
			t.Fatalf("seed current-meeting chat %d failed", index)
		}
	}

	history := app.roomChatHistory(roomChatHistoryLimit)
	if len(history) != roomChatHistoryLimit {
		t.Fatalf("history length=%d, want %d", len(history), roomChatHistoryLimit)
	}
	first, _ := history[0]["text"].(string)
	if !strings.HasPrefix(first, "current meeting message ") {
		t.Fatalf("history[0] text=%q, want only current-meeting chat", first)
	}
	last, _ := history[len(history)-1]["text"].(string)
	if last != "current meeting message "+strings.Repeat("x", total) {
		t.Fatalf("history tail=%q, want the newest message last (oldest-first order)", last)
	}
	for _, item := range history {
		if item["name"] != "Tyler" {
			t.Fatalf("history item name=%v, want Tyler", item["name"])
		}
	}
}

// Full websocket round trip: identity comes from the participant session
// (payload names are ignored), malformed payloads keep the connection alive,
// and the echo-on-broadcast payload carries the durable entry data.
func TestWebsocketRoomChatBroadcastsSessionIdentity(t *testing.T) {
	conn := newIsolatedNativeWebsocket(t, "tom@shareability.com")

	writeNativeWebsocketEvent(t, conn, "participant", map[string]any{})
	waitForKanbanEvent(t, conn, "access_granted", 5*time.Second)
	// Broadcasts only reach media-joined sockets; join media so the sender
	// receives its own echo-on-broadcast.
	writeNativeWebsocketEvent(t, conn, "media_ready", map[string]any{})

	// Malformed payload must not kill the connection (per-case continue).
	if err := conn.WriteJSON(websocketMessage{Event: "room_chat", Data: "{not-json"}); err != nil {
		t.Fatalf("send malformed room chat: %v", err)
	}
	// Empty text is rejected without a broadcast.
	writeNativeWebsocketEvent(t, conn, "room_chat", map[string]string{"text": "   "})
	// The name in the payload is attacker-controlled and must be ignored.
	writeNativeWebsocketEvent(t, conn, "room_chat", map[string]string{"name": "Mallory", "text": "ship it"})

	raw := waitForKanbanEvent(t, conn, "room_chat", 5*time.Second)
	var chat struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Text      string `json:"text"`
		CreatedAt string `json:"createdAt"`
	}
	if err := json.Unmarshal(raw, &chat); err != nil {
		t.Fatalf("decode room chat broadcast: %v", err)
	}
	if chat.Name != "Tom" {
		t.Fatalf("broadcast name=%q, want session identity Tom", chat.Name)
	}
	if chat.Text != "ship it" {
		t.Fatalf("broadcast text=%q, want ship it", chat.Text)
	}
	if !strings.HasPrefix(chat.ID, "chat-") {
		t.Fatalf("broadcast id=%q, want chat- prefix", chat.ID)
	}
	if _, err := time.Parse(time.RFC3339Nano, chat.CreatedAt); err != nil {
		t.Fatalf("broadcast createdAt=%q is not RFC3339Nano: %v", chat.CreatedAt, err)
	}

	entries := kanbanApp.memory.snapshot(0)
	if len(entries) != 1 {
		t.Fatalf("memory entries=%d, want exactly the one chat entry", len(entries))
	}
	if entries[0].Metadata["source"] != transcriptSourceRoomChat {
		t.Fatalf("persisted source=%q, want room_chat", entries[0].Metadata["source"])
	}
	if entries[0].Metadata["speaker"] != "Tom" {
		t.Fatalf("persisted speaker=%q, want Tom", entries[0].Metadata["speaker"])
	}
}

// A newly admitted participant receives the meeting's chat backlog as a
// direct room_chat_history send inside the accept block.
func TestWebsocketRoomChatHistoryReplayOnAdmission(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	conn := dialIsolatedWebsocket(t, server, "tyler@shareability.com")

	writeNativeWebsocketEvent(t, conn, "participant", map[string]any{})
	waitForKanbanEvent(t, conn, "access_granted", 5*time.Second)

	if _, ok := kanbanApp.recordRoomChatMessage("Tom", "hello from earlier"); !ok {
		t.Fatal("seed chat message failed")
	}
	if _, ok := kanbanApp.recordRoomChatMessage("Tim", "second message"); !ok {
		t.Fatal("seed second chat message failed")
	}

	// A fresh join (a second account) replays the backlog on admission.
	rejoined := dialIsolatedWebsocket(t, server, "tom@shareability.com")
	writeNativeWebsocketEvent(t, rejoined, "participant", map[string]any{})

	raw := waitForKanbanEvent(t, rejoined, "room_chat_history", 5*time.Second)
	var history []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Text      string `json:"text"`
		CreatedAt string `json:"createdAt"`
	}
	if err := json.Unmarshal(raw, &history); err != nil {
		t.Fatalf("decode room chat history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history length=%d, want 2", len(history))
	}
	if history[0].Name != "Tom" || history[0].Text != "hello from earlier" {
		t.Fatalf("history[0]=%+v, want Tom / hello from earlier", history[0])
	}
	if history[1].Name != "Tim" || history[1].Text != "second message" {
		t.Fatalf("history[1]=%+v, want Tim / second message", history[1])
	}
	for _, item := range history {
		if !strings.HasPrefix(item.ID, "chat-") {
			t.Fatalf("history id=%q, want chat- prefix", item.ID)
		}
		if _, err := time.Parse(time.RFC3339Nano, item.CreatedAt); err != nil {
			t.Fatalf("history createdAt=%q is not RFC3339Nano: %v", item.CreatedAt, err)
		}
	}
}

// Frontend wiring guard, following the repo's index.html grep-test pattern:
// the room chat panel, dock toggle with unread badge, and websocket cases
// must stay wired together.
func TestIndexRoomChatPanelWiring(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	for _, want := range []string{
		`id="roomChatPanel"`,
		`id="roomChatThread"`,
		`id="roomChatForm"`,
		`id="roomChatInput"`,
		`id="roomChatToggle"`,
		`id="roomChatUnread"`,
		`id="roomChatClose"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing room chat anchor %s", want)
		}
	}

	kanbanSwitch := functionBody(html, "function handleKanbanMessage(message)")
	if !strings.Contains(kanbanSwitch, "case 'room_chat':") {
		t.Fatal("handleKanbanMessage must route the room_chat event")
	}
	if !strings.Contains(kanbanSwitch, "case 'room_chat_history':") {
		t.Fatal("handleKanbanMessage must route the room_chat_history replay")
	}

	sendBody := functionBody(html, "function sendRoomChat(text)")
	if !strings.Contains(sendBody, "event: 'room_chat'") {
		t.Fatal("sendRoomChat must send the room_chat websocket event")
	}
	if strings.Contains(sendBody, "appendRoomChatMessage") {
		t.Fatal("sendRoomChat must not render optimistically; the sender renders from the broadcast echo")
	}

	nodeBody := functionBody(html, "function roomChatMessageNode(message)")
	if !strings.Contains(nodeBody, "scout-chat-msg--user") || !strings.Contains(nodeBody, "scout-chat-msg--peer") {
		t.Fatal("room chat bubbles must reuse the scout-chat bubble variants (own=user ink, peers=surface)")
	}
	if !strings.Contains(nodeBody, "scout-chat-meta") {
		t.Fatal("room chat messages must carry the author on the mono meta line")
	}

	if !strings.Contains(html, `class="chat-thread-item__unread room-chat-toggle__unread"`) {
		t.Fatal("dock unread badge must reuse the chat-thread-item__unread ink pill")
	}
}
