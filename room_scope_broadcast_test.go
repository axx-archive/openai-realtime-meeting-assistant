package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNamedRoomAudienceEventDoesNotReachOfficeSocket(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	conn := dialIsolatedWebsocket(t, server, "tom@shareability.com")
	sendOfficeHello(t, conn)
	waitForKanbanEvent(t, conn, "codex_proposals", 5*time.Second)

	broadcastRoomAudienceKanbanEvent("room-private-a", "room_chat", map[string]any{
		"id": "private-message", "roomId": "room-private-a", "text": "must not escape",
	})
	broadcastOfficeKanbanEvent("scope_test_done", map[string]any{"ok": true})

	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		var outer websocketMessage
		if err := conn.ReadJSON(&outer); err != nil {
			t.Fatalf("read office socket: %v", err)
		}
		if outer.Event != "kanban" {
			continue
		}
		var inner struct {
			Event string `json:"event"`
			Data  any    `json:"data"`
		}
		if err := json.Unmarshal([]byte(outer.Data), &inner); err != nil {
			t.Fatal(err)
		}
		if inner.Event == "room_chat" {
			t.Fatalf("named-room payload leaked to office socket: %#v", inner.Data)
		}
		if inner.Event == "scope_test_done" {
			return
		}
	}
}

func TestNamedRoomAssistantMediaEventDoesNotReachOfficeSocket(t *testing.T) {
	server := newIsolatedWebsocketServer(t)
	conn := dialIsolatedWebsocket(t, server, "tom@shareability.com")
	sendOfficeHello(t, conn)
	waitForKanbanEvent(t, conn, "codex_proposals", 5*time.Second)

	broadcastRoomAssistantTelemetry("room-private-a", "signal", "private publisher connected", map[string]any{
		"participant": "Private Person",
		"trackId":     "private-track",
	})
	broadcastOfficeKanbanEvent("scope_test_done", map[string]any{"ok": true})

	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		var outer websocketMessage
		if err := conn.ReadJSON(&outer); err != nil {
			t.Fatalf("read office socket: %v", err)
		}
		if outer.Event != "kanban" {
			continue
		}
		var inner struct {
			Event string `json:"event"`
			Data  any    `json:"data"`
		}
		if err := json.Unmarshal([]byte(outer.Data), &inner); err != nil {
			t.Fatal(err)
		}
		data, _ := inner.Data.(map[string]any)
		if inner.Event == "assistant_event" && data["trackId"] == "private-track" {
			t.Fatalf("named-room media identity leaked to office socket: %#v", inner.Data)
		}
		if inner.Event == "scope_test_done" {
			return
		}
	}
}
