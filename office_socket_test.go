package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// sendOfficeHello registers the connection for office event delivery and
// waits for the grant, which guarantees the registry write has landed before
// the test broadcasts anything.
func sendOfficeHello(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()

	writeNativeWebsocketEvent(t, conn, "office", map[string]any{})
	raw := waitForKanbanEvent(t, conn, "office_granted", 5*time.Second)
	grant := map[string]any{}
	if err := json.Unmarshal(raw, &grant); err != nil {
		t.Fatalf("decode office grant: %v", err)
	}
	return grant
}

// TestWebsocketOfficeHelloDeliversSignedInStateWithoutRoomSeat proves the
// office hello grants signed-in event delivery — grant, board, undo, memory,
// meeting, room-chat history, notification backlog, and codex proposals — all
// without admitting the session into the room or taking a seat.
func TestWebsocketOfficeHelloDeliversSignedInStateWithoutRoomSeat(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	conn := dialIsolatedWebsocket(t, server, "aj@shareability.com")

	grant := sendOfficeHello(t, conn)
	if grant["email"] != "aj@shareability.com" {
		t.Fatalf("expected the session email in the office grant, got %v", grant["email"])
	}
	if name, _ := grant["name"].(string); name == "" {
		t.Fatalf("expected the account display name in the office grant, got %v", grant["name"])
	}

	// The replay set follows the grant in order; each direct send must land.
	for _, event := range []string{
		"board",
		"undo_available",
		"memory",
		"meeting",
		"room_chat_history",
		"notification_backlog",
		"codex_proposals",
	} {
		waitForKanbanEvent(t, conn, event, 5*time.Second)
	}

	// No room seat was taken: the office hello must not admit the session.
	snapshot := kanbanApp.roomSnapshot()
	if occupied, ok := snapshot["occupiedSeats"].(int); !ok || occupied != 0 {
		t.Fatalf("expected zero occupied seats after office hello, got %v", snapshot["occupiedSeats"])
	}
	if participants, ok := snapshot["participants"].([]string); !ok || len(participants) != 0 {
		t.Fatalf("expected no room participants after office hello, got %v", snapshot["participants"])
	}
	listLock.RLock()
	officeCount := len(officeConnections)
	activeCount := len(activeParticipantConnections)
	poolCount := len(peerConnections)
	listLock.RUnlock()
	if officeCount != 1 {
		t.Fatalf("expected one registered office connection, got %d", officeCount)
	}
	if activeCount != 0 || poolCount != 0 {
		t.Fatalf("office hello must not enter room pools: active=%d pool=%d", activeCount, poolCount)
	}
}

// TestOfficeSocketReceivesBroadcastAndTargetedNotifications proves the
// notification fan-out reaches office-only sockets: broadcast records reach
// every office socket, targeted records reach only the matching account.
func TestOfficeSocketReceivesBroadcastAndTargetedNotifications(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	ajConn := dialIsolatedWebsocket(t, server, "aj@shareability.com")
	timConn := dialIsolatedWebsocket(t, server, "tim@shareability.com")
	sendOfficeHello(t, ajConn)
	sendOfficeHello(t, timConn)

	if _, err := kanbanApp.createNotification("", notificationKindInfo, "office broadcast check", "room", "", "", false); err != nil {
		t.Fatalf("create broadcast notification: %v", err)
	}
	for name, conn := range map[string]*websocket.Conn{"aj": ajConn, "tim": timConn} {
		raw := waitForKanbanEvent(t, conn, "notification", 5*time.Second)
		if !strings.Contains(string(raw), "office broadcast check") {
			t.Fatalf("%s office socket got the wrong notification payload: %s", name, raw)
		}
	}

	// Targeted record: only AJ's office socket may receive it.
	if _, err := kanbanApp.createNotification("aj@shareability.com", notificationKindAlert, "aj-only office secret", "room", "", "", false); err != nil {
		t.Fatalf("create targeted notification: %v", err)
	}
	raw := waitForKanbanEvent(t, ajConn, "notification", 5*time.Second)
	if !strings.Contains(string(raw), "aj-only office secret") {
		t.Fatalf("aj office socket missed the targeted notification: %s", raw)
	}

	// A broadcast marker sent after the targeted record bounds the check:
	// tim's next notification must be the marker, never the secret.
	if _, err := kanbanApp.createNotification("", notificationKindInfo, "post-target marker", "room", "", "", false); err != nil {
		t.Fatalf("create marker notification: %v", err)
	}
	timRaw := waitForKanbanEvent(t, timConn, "notification", 5*time.Second)
	if strings.Contains(string(timRaw), "aj-only office secret") {
		t.Fatalf("targeted notification leaked to a non-recipient office socket: %s", timRaw)
	}
	if !strings.Contains(string(timRaw), "post-target marker") {
		t.Fatalf("tim office socket missed the broadcast marker: %s", timRaw)
	}
}

// TestOfficeSocketReceivesOfficeAndUnionFanoutButNotRoomBroadcasts pins the
// routing contract: office-only events (chat_thread) and union events
// (memory) reach an office socket, while the room fan-out
// (broadcastKanbanEvent — signaling companions, participant_track,
// memory_transcript) never does.
func TestOfficeSocketReceivesOfficeAndUnionFanoutButNotRoomBroadcasts(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	conn := dialIsolatedWebsocket(t, server, "tom@shareability.com")
	sendOfficeHello(t, conn)
	// Drain the office replay so ordered reads below start clean.
	waitForKanbanEvent(t, conn, "codex_proposals", 5*time.Second)

	// Room-only events must not reach the office socket. Send them first…
	broadcastKanbanEvent("participant_track", map[string]any{"name": "AJ", "kind": "video"})
	broadcastKanbanEvent("memory_transcript", map[string]any{"id": "transcript-1", "text": "room only line"})

	// …then a chat_thread over the office fan-out and a memory snapshot over
	// the union fan-out. Ordered delivery on one socket means anything the
	// room fan-out leaked would arrive before these markers.
	broadcastOfficeKanbanEvent("chat_thread", map[string]any{"id": "thread-1", "title": "channel", "visibility": "public"})
	broadcastSignedInKanbanEvent("memory", []map[string]any{{"id": "memory-1", "kind": "brain"}})

	sawEvents := []string{}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("read websocket while draining office events: %v", err)
		}
		if message.Event != "kanban" {
			continue
		}
		var inner struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal([]byte(message.Data), &inner); err != nil {
			t.Fatalf("decode kanban envelope: %v", err)
		}
		sawEvents = append(sawEvents, inner.Event)
		if inner.Event == "memory" {
			break
		}
	}

	for _, event := range sawEvents {
		if event == "participant_track" || event == "memory_transcript" {
			t.Fatalf("room-only event %q leaked to an office socket (saw %v)", event, sawEvents)
		}
	}
	foundChatThread := false
	for _, event := range sawEvents {
		if event == "chat_thread" {
			foundChatThread = true
		}
	}
	if !foundChatThread {
		t.Fatalf("office socket missed the chat_thread fan-out (saw %v)", sawEvents)
	}
}

// The room-audience meeting recap rides the signed-in union fan-out like every
// other room_chat writer: an office-only socket (no room seat) receives the
// recap line live instead of waiting for a room join's history replay.
func TestMeetingRecapRoomPostReachesOfficeSocket(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	conn := dialIsolatedWebsocket(t, server, "tom@shareability.com")
	sendOfficeHello(t, conn)
	// Drain the office replay so the read below starts clean.
	waitForKanbanEvent(t, conn, "codex_proposals", 5*time.Second)

	kanbanApp.mu.Lock()
	kanbanApp.apiKey = "test-key"
	kanbanApp.mu.Unlock()
	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		return "## Overview\nThe Boot Barn pilot is on track for Friday.", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })
	appendTestTranscript(t, kanbanApp, "recap-office-1", "Boot Barn pilot is on track.")

	if _, _, err := kanbanApp.meetingRecap(map[string]any{"audience": "room"}, ""); err != nil {
		t.Fatalf("meetingRecap: %v", err)
	}

	raw := waitForKanbanEvent(t, conn, "room_chat", 5*time.Second)
	if !strings.Contains(string(raw), "Meeting recap:") {
		t.Fatalf("office room_chat payload=%s, want the recap line", raw)
	}
}

// TestOfficeSocketUnregisteredOnClose proves the registry entry is reaped
// when the socket goes away, so dead office sockets never accumulate.
func TestOfficeSocketUnregisteredOnClose(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	conn := dialIsolatedWebsocket(t, server, "aj@shareability.com")
	sendOfficeHello(t, conn)

	listLock.RLock()
	before := len(officeConnections)
	listLock.RUnlock()
	if before != 1 {
		t.Fatalf("expected one office connection before close, got %d", before)
	}

	_ = conn.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		listLock.RLock()
		remaining := len(officeConnections)
		listLock.RUnlock()
		if remaining == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("office connection was not unregistered after the socket closed")
}

// TestPrivateChatThreadUpdatesReachOwnerOfficeSocketOnly pins the private
// thread live path: chat_thread broadcasts are public-only, and the 12s chat
// poll skips its fetch while the office socket is up, so a private thread's
// commit — and especially an agent-thread ref status flip — must ride the
// owner-targeted send to the owner's office socket, and never reach another
// signed-in user's socket.
func TestPrivateChatThreadUpdatesReachOwnerOfficeSocketOnly(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	timConn := dialIsolatedWebsocket(t, server, "tim@shareability.com")
	ajConn := dialIsolatedWebsocket(t, server, "aj@shareability.com")
	sendOfficeHello(t, timConn)
	sendOfficeHello(t, ajConn)
	// Drain the ordered replay so the reads below observe only new events.
	waitForKanbanEvent(t, timConn, "codex_proposals", 5*time.Second)
	waitForKanbanEvent(t, ajConn, "codex_proposals", 5*time.Second)

	thread, err := kanbanApp.createScoutChatThread("tim@shareability.com", "Tim", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	ref := scoutChatMessageRecord{
		ID:        "msg-ref-1",
		Kind:      "thread",
		Role:      "scout",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Thread:    &scoutChatThreadRef{ID: "agent-thread-1", Mode: "research", Query: "creator market", Status: "running"},
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages("tim@shareability.com", thread.ID, ref); err != nil {
		t.Fatalf("commit private ref message: %v", err)
	}
	raw := waitForKanbanEvent(t, timConn, "chat_thread", 5*time.Second)
	if !strings.Contains(string(raw), thread.ID) || !strings.Contains(string(raw), `"running"`) {
		t.Fatalf("owner office socket missed the private thread commit: %s", raw)
	}

	// The worker finishing flips the persisted ref; pre-fix this flip had NO
	// live path for private threads (public-only broadcast + gated poll), so
	// the run card froze at "running" until a reload.
	if err := kanbanApp.commitScoutChatThreadRefStatus(thread.ID, "tim@shareability.com", "agent-thread-1", "complete", "artifact-9"); err != nil {
		t.Fatalf("commit ref status flip: %v", err)
	}
	raw = waitForKanbanEvent(t, timConn, "chat_thread", 5*time.Second)
	if !strings.Contains(string(raw), `"complete"`) || !strings.Contains(string(raw), "artifact-9") {
		t.Fatalf("owner office socket missed the private ref status flip: %s", raw)
	}

	// Bound the negative check with a public marker: aj's next chat_thread
	// event must be the marker, never tim's private thread.
	broadcastOfficeKanbanEvent("chat_thread", map[string]any{"id": "marker-thread", "visibility": "public"})
	ajRaw := waitForKanbanEvent(t, ajConn, "chat_thread", 5*time.Second)
	if strings.Contains(string(ajRaw), thread.ID) {
		t.Fatalf("private thread update leaked to a non-owner office socket: %s", ajRaw)
	}
	if !strings.Contains(string(ajRaw), "marker-thread") {
		t.Fatalf("aj office socket missed the public marker: %s", ajRaw)
	}
}
