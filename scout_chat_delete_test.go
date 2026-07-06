package main

// Card 073 — delete affordance for misplaced messages, server half. A user
// may remove THEIR OWN message from a scout thread (private or public
// channel) and from the room chat transcript; identity comes from the
// session, so nobody can delete someone else's words, and Scout's committed
// replies are not deletable at all.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedScoutChatUserMessage(t *testing.T, threadID string, viewerEmail string, id string, authorEmail string, text string) {
	t.Helper()
	_, err := kanbanApp.commitScoutChatThreadMessages(viewerEmail, threadID, scoutChatMessageRecord{
		ID:          id,
		Kind:        "message",
		Role:        "user",
		Text:        text,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		AuthorEmail: normalizeAccountEmail(authorEmail),
	})
	if err != nil {
		t.Fatalf("seed message %s: %v", id, err)
	}
}

func TestScoutChatThreadMessageDeleteOwnMessagesOnly(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "launch plan", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	seedScoutChatUserMessage(t, channel.ID, "aj@shareability.com", "msg-aj", "aj@shareability.com", "wrong channel, meant this for ops")
	seedScoutChatUserMessage(t, channel.ID, "tim@shareability.com", "msg-tim", "tim@shareability.com", "keeping this one")
	if _, err := kanbanApp.commitScoutChatThreadMessages("aj@shareability.com", channel.ID, scoutChatMessageRecord{
		ID:        "msg-scout",
		Kind:      "message",
		Role:      "scout",
		Text:      "noted",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("seed scout reply: %v", err)
	}

	// Authz: another signed-in user cannot delete aj's message.
	if _, err := kanbanApp.deleteScoutChatThreadMessage("tim@shareability.com", channel.ID, "msg-aj"); err == nil || !strings.Contains(err.Error(), "your own") {
		t.Fatalf("cross-user delete err=%v, want the own-messages refusal", err)
	}
	// Scout's committed reply is nobody's to delete.
	if _, err := kanbanApp.deleteScoutChatThreadMessage("aj@shareability.com", channel.ID, "msg-scout"); err == nil || !strings.Contains(err.Error(), "your own") {
		t.Fatalf("scout-reply delete err=%v, want the own-messages refusal", err)
	}
	if thread, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", channel.ID); err != nil || len(thread.Messages) != 3 {
		t.Fatalf("messages=%d err=%v after refused deletes, want all 3 intact", len(thread.Messages), err)
	}

	// The author removes their own message; the persisted record loses it and
	// the preview recomputes from what remains.
	thread, err := kanbanApp.deleteScoutChatThreadMessage("aj@shareability.com", channel.ID, "msg-aj")
	if err != nil {
		t.Fatalf("own delete: %v", err)
	}
	if len(thread.Messages) != 2 {
		t.Fatalf("messages=%d after delete, want 2", len(thread.Messages))
	}
	for _, message := range thread.Messages {
		if message.ID == "msg-aj" {
			t.Fatal("deleted message still present in the returned thread")
		}
	}
	if thread.Preview != "noted" {
		t.Fatalf("preview=%q, want it recomputed from the surviving newest text", thread.Preview)
	}
	persisted, _, err := kanbanApp.scoutChatThreadByID("tim@shareability.com", channel.ID)
	if err != nil {
		t.Fatalf("re-read channel: %v", err)
	}
	if len(persisted.Messages) != 2 {
		t.Fatalf("persisted messages=%d, want the deletion durable", len(persisted.Messages))
	}

	// A miss is a 404-shaped error, not a silent success.
	if _, err := kanbanApp.deleteScoutChatThreadMessage("aj@shareability.com", channel.ID, "msg-aj"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("re-delete err=%v, want not found", err)
	}
}

func TestScoutChatThreadMessageDeletePrivateThreadStaysOwnerOnly(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Private notes", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	// Pre-stamp message (no authorEmail): owner-only visibility already
	// proves authorship in a private thread, so the owner can still delete it.
	seedScoutChatUserMessage(t, private.ID, "aj@shareability.com", "msg-legacy", "", "posted before the stamp existed")

	if _, err := kanbanApp.deleteScoutChatThreadMessage("tim@shareability.com", private.ID, "msg-legacy"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("outsider delete err=%v, want the thread hidden entirely", err)
	}
	thread, err := kanbanApp.deleteScoutChatThreadMessage("aj@shareability.com", private.ID, "msg-legacy")
	if err != nil {
		t.Fatalf("owner delete of pre-stamp message: %v", err)
	}
	if len(thread.Messages) != 0 {
		t.Fatalf("messages=%d, want 0", len(thread.Messages))
	}
}

// A pre-stamp (authorEmail-less) user message in a PUBLIC channel has no
// provable author — nobody may delete it.
func TestScoutChatThreadMessageDeleteUnstampedChannelMessageRefused(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "launch plan", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	seedScoutChatUserMessage(t, channel.ID, "aj@shareability.com", "msg-unstamped", "", "who wrote this?")
	if _, err := kanbanApp.deleteScoutChatThreadMessage("aj@shareability.com", channel.ID, "msg-unstamped"); err == nil || !strings.Contains(err.Error(), "your own") {
		t.Fatalf("unstamped channel delete err=%v, want refusal", err)
	}
}

// The wire contract the delete control's fetch relies on: DELETE
// /assistant/chat-threads/{id}/messages/{messageId}, session-identified,
// 403 on someone else's message, 200 + the updated thread on your own.
func TestAssistantChatThreadMessageDeleteRoute(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "launch plan", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	seedScoutChatUserMessage(t, channel.ID, "aj@shareability.com", "msg-aj", "aj@shareability.com", "wrong channel")

	deleteAs := func(email string, messageID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, "/assistant/chat-threads/"+channel.ID+"/messages/"+messageID, nil)
		for _, cookie := range loginAs(t, email, "B0NFIRE!") {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantChatThreadHandler(recorder, req)
		return recorder
	}

	if recorder := deleteAs("tim@shareability.com", "msg-aj"); recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-user delete status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusForbidden)
	}
	recorder := deleteAs("aj@shareability.com", "msg-aj")
	if recorder.Code != http.StatusOK {
		t.Fatalf("own delete status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}
	var payload struct {
		OK     bool                  `json:"ok"`
		Thread scoutChatThreadRecord `json:"thread"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if !payload.OK || len(payload.Thread.Messages) != 0 {
		t.Fatalf("response=%s, want ok with the message gone", recorder.Body.String())
	}
	if recorder := deleteAs("aj@shareability.com", "msg-aj"); recorder.Code != http.StatusNotFound {
		t.Fatalf("re-delete status=%d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestRoomChatDeleteEnforcesAuthorshipFromSession(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	payload, ok := app.recordRoomChatMessageWithMetadata("Tom", "oops, wrong room", map[string]string{
		"authorEmail": "tom@shareability.com",
	})
	if !ok {
		t.Fatal("seed room chat message failed")
	}
	id, _ := payload["id"].(string)
	if authorEmail, _ := payload["authorEmail"].(string); authorEmail != "tom@shareability.com" {
		t.Fatalf("payload authorEmail=%q, want the session stamp on the wire", authorEmail)
	}

	// Someone else — even sharing the display name — cannot delete it.
	if _, ok := app.deleteRoomChatMessage(id, "tim@shareability.com", "Tom"); ok {
		t.Fatal("cross-user room chat delete succeeded, want authz refusal")
	}
	if history := app.roomChatHistory(roomChatHistoryLimit); len(history) != 1 {
		t.Fatalf("history=%d after refused delete, want the message intact", len(history))
	}

	// The author deletes it: gone from the persisted record and from history.
	deleted, ok := app.deleteRoomChatMessage(id, "TOM@shareability.com", "Tom")
	if !ok {
		t.Fatal("author room chat delete failed")
	}
	if deleted["id"] != id {
		t.Fatalf("delete payload id=%v, want %q", deleted["id"], id)
	}
	if history := app.roomChatHistory(roomChatHistoryLimit); len(history) != 0 {
		t.Fatalf("history=%d after delete, want 0", len(history))
	}
	if entries := app.memory.snapshot(0); len(entries) != 0 {
		t.Fatalf("memory entries=%d after delete, want the transcript entry hard-removed", len(entries))
	}
}

func TestRoomChatDeleteLegacyEntriesFallBackToSpeakerName(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	payload, ok := app.recordRoomChatMessage("Tyler", "pre-stamp message")
	if !ok {
		t.Fatal("seed legacy room chat message failed")
	}
	id, _ := payload["id"].(string)

	if _, ok := app.deleteRoomChatMessage(id, "tim@shareability.com", "Tim"); ok {
		t.Fatal("name-mismatched delete of a legacy entry succeeded, want refusal")
	}
	if _, ok := app.deleteRoomChatMessage(id, "tyler@shareability.com", "tyler"); !ok {
		t.Fatal("case-insensitive speaker-name fallback delete failed")
	}
}

func TestRoomChatDeleteOnlyTouchesRoomChatEntries(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	entry, appended, err := app.memory.appendAttributedTranscript("event-spoken", "item-spoken", "Tyler", "dominant", "Boot Barn spoken update.")
	if err != nil || !appended {
		t.Fatalf("append spoken transcript: appended=%v err=%v", appended, err)
	}
	if _, ok := app.deleteRoomChatMessage(entry.ID, "tyler@shareability.com", "Tyler"); ok {
		t.Fatal("spoken transcript deleted through the room chat path, want refusal")
	}
	if entries := app.memory.snapshot(0); len(entries) != 1 {
		t.Fatalf("memory entries=%d, want the spoken transcript untouched", len(entries))
	}
}
