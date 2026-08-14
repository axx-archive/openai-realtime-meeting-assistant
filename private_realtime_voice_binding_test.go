package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrivateRealtimeVoiceSessionBindingIsRestartSafeAndOwnerIsolated(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)

	first, created, err := app.ensurePrivateRealtimeVoiceConversation("AJ@Shareability.com", "AJ", "voice-session-restart-1")
	if err != nil || !created {
		t.Fatalf("first=%+v created=%v err=%v", first, created, err)
	}
	if first.Title != "Live Voice with Scout" || scoutChatThreadVisibility(first) != scoutChatVisibilityPrivate || first.VoiceSession == nil {
		t.Fatalf("first binding=%+v", first)
	}
	if first.VoiceSession.SessionDigest == "voice-session-restart-1" || !isHexDigest(first.VoiceSession.SessionDigest) {
		t.Fatalf("raw or invalid session digest persisted: %+v", first.VoiceSession)
	}

	retry, created, err := app.ensurePrivateRealtimeVoiceConversation("aj@shareability.com", "AJ", "voice-session-restart-1")
	if err != nil || created || retry.ID != first.ID {
		t.Fatalf("retry=%+v created=%v err=%v", retry, created, err)
	}
	different, created, err := app.ensurePrivateRealtimeVoiceConversation("aj@shareability.com", "AJ", "voice-session-restart-2")
	if err != nil || !created || different.ID == first.ID {
		t.Fatalf("different=%+v created=%v err=%v", different, created, err)
	}
	otherOwner, created, err := app.ensurePrivateRealtimeVoiceConversation("tim@shareability.com", "Tim", "voice-session-restart-1")
	if err != nil || !created || otherOwner.ID == first.ID || normalizeAccountEmail(otherOwner.OwnerEmail) != "tim@shareability.com" {
		t.Fatalf("other owner=%+v created=%v err=%v", otherOwner, created, err)
	}

	restarted := newKanbanBoardApp()
	reloaded, created, err := restarted.ensurePrivateRealtimeVoiceConversation("aj@shareability.com", "AJ", "voice-session-restart-1")
	if err != nil || created || reloaded.ID != first.ID {
		t.Fatalf("restart=%+v created=%v err=%v", reloaded, created, err)
	}
	projected := restarted.projectScoutChatThreadForViewer("aj@shareability.com", reloaded)
	if projected.VoiceSession != nil {
		t.Fatalf("server-only voice binding leaked to viewer: %+v", projected.VoiceSession)
	}
	if _, err := restarted.privateRealtimeVoiceConversation("tim@shareability.com", "voice-session-restart-1", first.ID); err == nil {
		t.Fatal("another owner resolved AJ's voice thread")
	}
}

func TestPrivateRealtimeVoiceSessionRejectsArchivedAndMismatchedThread(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	thread, _, err := app.ensurePrivateRealtimeVoiceConversation("aj@shareability.com", "AJ", "voice-session-fence")
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := app.ensurePrivateRealtimeVoiceConversation("aj@shareability.com", "AJ", "voice-session-other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.privateRealtimeVoiceConversation("aj@shareability.com", "voice-session-fence", other.ID); err == nil {
		t.Fatal("session accepted another voice thread")
	}
	if _, err := app.setScoutChatThreadArchived("aj@shareability.com", thread.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := app.privateRealtimeVoiceConversation("aj@shareability.com", "voice-session-fence", thread.ID); err == nil || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("archived resolution err=%v", err)
	}
	if _, _, err := app.ensurePrivateRealtimeVoiceConversation("aj@shareability.com", "AJ", "voice-session-fence"); err == nil || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("archived offer retry err=%v", err)
	}
}

func TestPrivateRealtimeVoiceSessionRoutesEveryTurnToExactBoundThread(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-router-test"
	bound, _, err := app.ensurePrivateRealtimeVoiceConversation("aj@shareability.com", "AJ", "voice-session-route")
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Unrelated recent chat", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		switch request.Workflow {
		case "scout_route":
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Outcome: string(conversationIntentConversationalReply)}), nil
		case "scout_chat":
			return "Bound voice answer.", nil
		default:
			return "", fmt.Errorf("unexpected workflow %q", request.Workflow)
		}
	})

	first, changed, err := app.applyPrivateRealtimeVoiceSessionModelTool(context.Background(), "aj@shareability.com", "voice-session-route", bound.ID, "voice-call-1", "route_conversation_turn", map[string]any{"utterance": "Keep this in my voice chat"})
	if err != nil || changed || first["thread_id"] != bound.ID || first["voice_session_id"] != "voice-session-route" {
		t.Fatalf("first=%v changed=%v err=%v", first, changed, err)
	}
	replay, changed, err := app.applyPrivateRealtimeVoiceSessionModelTool(context.Background(), "aj@shareability.com", "voice-session-route", bound.ID, "voice-call-1", "route_conversation_turn", map[string]any{"utterance": "Keep this in my voice chat"})
	if err != nil || changed || replay["thread_id"] != bound.ID || calls != 2 {
		t.Fatalf("replay=%v changed=%v calls=%d err=%v", replay, changed, calls, err)
	}
	if _, _, err := app.applyPrivateRealtimeVoiceSessionModelTool(context.Background(), "aj@shareability.com", "voice-session-route", bound.ID, "voice-call-1", "route_conversation_turn", map[string]any{"utterance": "Changed words"}); err == nil {
		t.Fatal("call id reuse with changed words was accepted")
	}
	savedBound, _, _ := app.scoutChatThreadByID("aj@shareability.com", bound.ID)
	savedUnrelated, _, _ := app.scoutChatThreadByID("aj@shareability.com", unrelated.ID)
	if len(savedBound.Messages) != 2 || len(savedUnrelated.Messages) != 0 {
		t.Fatalf("bound messages=%d unrelated messages=%d", len(savedBound.Messages), len(savedUnrelated.Messages))
	}
}

func TestAssistantRealtimeToolRequiresAndEchoesExactVoiceBinding(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = "openai-router-test"
	t.Cleanup(func() { kanbanApp = previousApp })
	voiceSessionID := "voice-http-exact"
	thread, _, err := kanbanApp.ensurePrivateRealtimeVoiceConversation("aj@shareability.com", "AJ", voiceSessionID)
	if err != nil {
		t.Fatal(err)
	}
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow == "scout_route" {
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Outcome: string(conversationIntentConversationalReply)}), nil
		}
		return "Exact HTTP binding.", nil
	})
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/assistant/realtime-tool", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantRealtimeToolHandler(recorder, req)
		return recorder
	}
	missing := post(`{"callId":"voice-http-call","name":"route_conversation_turn","arguments":{"utterance":"Hello"}}`)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing binding status=%d body=%s", missing.Code, missing.Body.String())
	}
	mismatch := post(fmt.Sprintf(`{"voiceSessionId":%q,"threadId":%q,"callId":"voice-http-call","name":"route_conversation_turn","arguments":{"utterance":"Hello"}}`, voiceSessionID, "scout-voice-mismatch"))
	if mismatch.Code != http.StatusOK || !strings.Contains(mismatch.Body.String(), "do not match") {
		t.Fatalf("mismatch status=%d body=%s", mismatch.Code, mismatch.Body.String())
	}
	response := post(fmt.Sprintf(`{"voiceSessionId":%q,"threadId":%q,"callId":"voice-http-call","name":"route_conversation_turn","arguments":{"utterance":"Hello"}}`, voiceSessionID, thread.ID))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		OK             bool   `json:"ok"`
		VoiceSessionID string `json:"voiceSessionId"`
		ThreadID       string `json:"threadId"`
		Result         struct {
			VoiceSessionID string `json:"voice_session_id"`
			ThreadID       string `json:"thread_id"`
			Message        string `json:"message"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || payload.VoiceSessionID != voiceSessionID || payload.ThreadID != thread.ID || payload.Result.VoiceSessionID != voiceSessionID || payload.Result.ThreadID != thread.ID || payload.Result.Message != "Exact HTTP binding." {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestAssistantRealtimeToolRoutesTodaysMeetingsThroughAuthorizedBriefing(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	voiceSessionID := "voice-http-todays-meetings"
	thread, _, err := kanbanApp.ensurePrivateRealtimeVoiceConversation("aj@shareability.com", "AJ", voiceSessionID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(meetingTimeLocation())
	spanStart := time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, now.Location())
	spanEnd := spanStart.Add(45 * time.Minute)
	digest := `{"meetingId":"voice-http-meeting-today","title":"Launch review","day":"` + spanStart.Format("2006-01-02") + `",` +
		`"decisions":[{"d":"Keep the Friday launch with a rollback owner assigned","status":"decided","importance":5}]}`
	upsertBriefingTestDigest(t, kanbanApp, "voice-http-meeting-today", digest, spanStart.Format("2006-01-02"), spanStart.UTC().Format(time.RFC3339), spanEnd.UTC().Format(time.RFC3339))

	providerCalls := 0
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		providerCalls++
		t.Fatalf("authorized meeting briefing reached generic provider workflow %q", request.Workflow)
		return "", nil
	})
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	post := func() *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"voiceSessionId":%q,"threadId":%q,"callId":"voice-http-meeting-call","name":"route_conversation_turn","arguments":{"utterance":"Catch me up on today's meetings. What was important?"}}`, voiceSessionID, thread.ID)
		req := httptest.NewRequest(http.MethodPost, "/assistant/realtime-tool", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantRealtimeToolHandler(recorder, req)
		return recorder
	}
	for attempt := 0; attempt < 2; attempt++ {
		response := post()
		if response.Code != http.StatusOK {
			t.Fatalf("attempt %d status=%d body=%s", attempt, response.Code, response.Body.String())
		}
		var payload struct {
			OK     bool `json:"ok"`
			Result struct {
				Outcome string `json:"outcome"`
				Message string `json:"message"`
			} `json:"result"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if !payload.OK || payload.Result.Outcome != string(conversationIntentConversationalReply) ||
			!strings.Contains(payload.Result.Message, "Keep the Friday launch") ||
			!strings.Contains(payload.Result.Message, "Launch review") {
			t.Fatalf("attempt %d payload=%+v", attempt, payload)
		}
	}
	if providerCalls != 0 {
		t.Fatalf("generic provider calls=%d, want 0", providerCalls)
	}
}
