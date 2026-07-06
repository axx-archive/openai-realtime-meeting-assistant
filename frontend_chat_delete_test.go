package main

// Card 073 — delete affordance for misplaced messages, client half. These
// grep-style pins hold: the shared arm-then-confirm delete control exists and
// only rides one's OWN bubbles, room chat deletes travel the websocket
// (room_chat_delete both ways), scout thread deletes travel the DELETE route
// plus the chat_thread deletedMessageId broadcast, and the control's CSS is
// hover/long-press revealed in the attribution voice (token-driven, both
// themes).

import (
	"os"
	"strings"
	"testing"
)

func readIndexForChatDelete(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func TestIndexChatMessageDeleteControl(t *testing.T) {
	html := readIndexForChatDelete(t)
	for _, want := range []string{
		// the shared control: arm-then-confirm, long-press reveal on touch
		"function attachChatMessageDeleteControl(item, onDelete)",
		"control.className = 'scout-chat-msg__delete'",
		"control.textContent = 'delete?'",
		"item.classList.add('show-delete')",
		// own-message gating on each surface (the server enforces the same)
		"if (own && message?.id) {",
		"if (kind === 'user' && message?.id) {",
		// the CSS: hidden until hover/focus/long-press, danger only when armed
		".scout-chat-msg:hover .scout-chat-msg__delete,",
		".scout-chat-msg.show-delete .scout-chat-msg__delete {",
		`.scout-chat-msg__delete[data-armed="1"]`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing chat delete control hook %q", want)
		}
	}
}

func TestIndexRoomChatDeleteWiring(t *testing.T) {
	html := readIndexForChatDelete(t)
	for _, want := range []string{
		// outbound: the delete rides the room socket, no optimistic drop
		"function sendRoomChatDelete(id) {",
		"event: 'room_chat_delete',",
		// inbound: every client drops the bubble on the broadcast
		"case 'room_chat_delete':",
		"handleRoomChatDeleteEvent(message.data)",
		"function handleRoomChatDeleteEvent(payload) {",
		"roomChatSeenIds.delete(id)",
		// bubbles are addressable by their durable id
		"item.dataset.messageId = String(message?.id || '')",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing room chat delete hook %q", want)
		}
	}
	// Own-detection prefers the server-stamped session email over the
	// mutable display name.
	ownBody := functionBody(html, "function roomChatMessageIsOwn(message)")
	if ownBody == "" {
		t.Fatal("could not extract roomChatMessageIsOwn body")
	}
	if !strings.Contains(ownBody, "message?.authorEmail") || !strings.Contains(ownBody, "authedUser?.email") {
		t.Fatal("roomChatMessageIsOwn must key on the server-stamped authorEmail before name matching")
	}
}

func TestIndexScoutThreadMessageDeleteWiring(t *testing.T) {
	html := readIndexForChatDelete(t)
	for _, want := range []string{
		// outbound: the session-identified DELETE route
		"async function deleteScoutChatMessage(messageId) {",
		"/messages/${encodeURIComponent(messageId)}`, { method: 'DELETE' })",
		// shared local removal, used by the sender and the broadcast alike
		"function removeScoutChatThreadMessage(threadId, messageId) {",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing scout thread delete hook %q", want)
		}
	}
	// The live path: a chat_thread event carrying deletedMessageId drops the
	// bubble in every other open tab.
	threadEvent := functionBody(html, "function handleChatThreadEvent(payload)")
	if threadEvent == "" {
		t.Fatal("could not extract handleChatThreadEvent body")
	}
	if !strings.Contains(threadEvent, "payload.deletedMessageId") || !strings.Contains(threadEvent, "removeScoutChatThreadMessage(id, String(payload.deletedMessageId))") {
		t.Fatal("handleChatThreadEvent must route deletedMessageId through removeScoutChatThreadMessage")
	}
}
