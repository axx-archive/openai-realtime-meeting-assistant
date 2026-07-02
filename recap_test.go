package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// meeting_recap forces one brain pass (minBatch=1 — a single unconsumed
// transcript is enough), posts the full recap to room chat, and returns the
// recap + headline for the voice model to speak.
func TestMeetingRecapForcesBrainPassAndPostsToRoomChat(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	originalResponder := createOpenAITextResponse
	brainCalls := 0
	createOpenAITextResponse = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		brainCalls++
		if !strings.Contains(request.Input, "Boot Barn pilot is on track") {
			t.Fatalf("brain input missing the unconsumed transcript: %s", request.Input)
		}
		return "## Overview\nThe Boot Barn pilot is on track for Friday.\n\n## Decisions\n- Ship it.", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	appendTestTranscript(t, app, "recap-transcript-1", "Boot Barn pilot is on track.")

	result, changed, err := app.applyToolCallArgs("meeting_recap", map[string]any{})
	if err != nil {
		t.Fatalf("meeting_recap: %v", err)
	}
	if changed {
		t.Fatal("meeting_recap must not report a board change")
	}
	if brainCalls != 1 {
		t.Fatalf("brain passes=%d, want exactly one forced pass with minBatch=1", brainCalls)
	}
	if result["ok"] != true || result["audience"] != "room" {
		t.Fatalf("result=%#v, want ok room recap", result)
	}
	recap := asString(result["recap"])
	if !strings.Contains(recap, "Boot Barn pilot is on track for Friday") {
		t.Fatalf("recap=%q, want the fresh brain write-up", recap)
	}
	if headline := asString(result["headline"]); headline != "The Boot Barn pilot is on track for Friday." {
		t.Fatalf("headline=%q, want the first Overview paragraph", headline)
	}

	// Room delivery rides the typed-chat transcript path.
	history := app.roomChatHistory(roomChatHistoryLimit)
	found := false
	for _, item := range history {
		if strings.Contains(asString(item["text"]), "Meeting recap:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("room chat history=%#v, want the posted recap", history)
	}
}

// A recap request with nothing new to consume falls back to the newest brain
// entry of the current meeting instead of failing.
func TestMeetingRecapReturnsExistingBrainEntryWhenNothingNew(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("no unconsumed transcripts: the forced pass must skip the model")
		return "", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	if _, _, err := app.memory.appendBrainWriteUp("brain-existing", "## Overview\nEarlier summary stands.", nil); err != nil {
		t.Fatalf("append brain entry: %v", err)
	}

	result, _, err := app.meetingRecap(map[string]any{"audience": "room"}, "")
	if err != nil {
		t.Fatalf("meetingRecap: %v", err)
	}
	if !strings.Contains(asString(result["recap"]), "Earlier summary stands") {
		t.Fatalf("recap=%v, want the existing brain entry", result["recap"])
	}
}

// No transcripts and no brain entries: the tool errors cleanly through the
// (result, changed, err) path.
func TestMeetingRecapErrorsWhenNothingCaptured(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	if _, _, err := app.meetingRecap(map[string]any{}, ""); err == nil || !strings.Contains(err.Error(), "nothing has been captured") {
		t.Fatalf("err=%v, want the nothing-captured error", err)
	}

	// Missing API key errors before any pass.
	keyless := newIsolatedKanbanBoardApp(t)
	if _, _, err := keyless.meetingRecap(map[string]any{}, ""); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("keyless err=%v, want an API key error", err)
	}
}

// Audience "me" (catch-me-up) writes a targeted notification and skips the
// room chat post; without a requester it falls back to the room.
func TestMeetingRecapAudienceMeTargetsRequesterBell(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		return "## Overview\nTim owns the vendor call.", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	appendTestTranscript(t, app, "recap-me-1", "Tim will take the vendor call.")

	result, _, err := app.applyPrivateRealtimeVoiceTool("tim@shareability.com", "catch_me_up", map[string]any{})
	if err != nil {
		t.Fatalf("catch_me_up: %v", err)
	}
	if result["audience"] != "me" {
		t.Fatalf("audience=%v, want me", result["audience"])
	}

	unread := app.unreadNotificationsFor("tim@shareability.com", notificationListLimit)
	if len(unread) != 1 || !strings.Contains(asString(unread[0]["text"]), "Tim owns the vendor call") {
		t.Fatalf("tim unread=%#v, want the recap headline notification", unread)
	}
	if other := app.unreadNotificationsFor("aj@shareability.com", notificationListLimit); len(other) != 0 {
		t.Fatalf("aj unread=%#v, want the catch-up targeted only", other)
	}
	for _, item := range app.roomChatHistory(roomChatHistoryLimit) {
		if strings.Contains(asString(item["text"]), "Meeting recap:") {
			t.Fatalf("audience me must not post to room chat: %#v", item)
		}
	}

	// Room dispatch of audience "me" has no requester: falls back to room.
	appendTestTranscript(t, app, "recap-me-2", "Another beat of the meeting.")
	fallback, _, err := app.applyToolCallArgs("meeting_recap", map[string]any{"audience": "me"})
	if err != nil {
		t.Fatalf("meeting_recap room fallback: %v", err)
	}
	if fallback["audience"] != "room" {
		t.Fatalf("fallback audience=%v, want room without a requester", fallback["audience"])
	}
}

// Contract: schema, both allowlists, both instruction builders, and the async
// dispatch list must all expose the recap tools.
func TestMeetingRecapToolContract(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	rawTools, err := jsonMarshalForTest(app.kanbanTools())
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	for _, want := range []string{`"name":"meeting_recap"`, `"name":"catch_me_up"`, `"name":"post_to_channel"`, `"name":"create_channel"`, `"name":"start_grill_session"`, `"name":"end_grill_session"`} {
		if !strings.Contains(rawTools, want) {
			t.Fatalf("tools JSON missing %s", want)
		}
	}

	for _, tool := range []string{"meeting_recap", "catch_me_up", "post_to_channel", "create_channel"} {
		if !privateRealtimeVoiceToolAllowed(tool) {
			t.Fatalf("private realtime voice must allow %s", tool)
		}
		found := false
		for _, schema := range app.privateRealtimeVoiceTools() {
			if asString(schema["name"]) == tool {
				found = true
			}
		}
		if !found {
			t.Fatalf("privateRealtimeVoiceTools must expose the %s schema", tool)
		}
	}

	roomInstructions := app.sessionInstructions()
	privateInstructions := app.privateRealtimeVoiceSessionInstructions()
	for _, want := range []string{"meeting_recap", "post_to_channel", "create_channel"} {
		if !strings.Contains(roomInstructions, want) {
			t.Fatalf("room instructions missing %s", want)
		}
		if !strings.Contains(privateInstructions, want) {
			t.Fatalf("private instructions missing %s", want)
		}
	}
	for _, want := range []string{"start_grill_session", "end_grill_session"} {
		if !strings.Contains(roomInstructions, want) {
			t.Fatalf("room instructions missing %s", want)
		}
		if strings.Contains(privateInstructions, want) {
			t.Fatalf("private instructions must not teach room-only %s", want)
		}
	}

	for _, tool := range []string{"meeting_recap", "catch_me_up", "end_grill_session"} {
		if !realtimeToolRunsAsync(tool) {
			t.Fatalf("%s blocks on model/report work and must run async", tool)
		}
	}
}

func jsonMarshalForTest(value any) (string, error) {
	raw, err := json.Marshal(value)
	return string(raw), err
}
