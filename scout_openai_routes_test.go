package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func openAIScoutRouteJSON(t *testing.T, route openAIScoutRouterOutput) string {
	t.Helper()
	raw, err := json.Marshal(route)
	if err != nil {
		t.Fatalf("marshal Scout route: %v", err)
	}
	return string(raw)
}

func TestScoutCoreRouteModelsAreExplicitOpenAISeats(t *testing.T) {
	t.Setenv("OPENAI_SCOUT_ROUTER_MODEL", "")
	t.Setenv("OPENAI_SCOUT_CHAT_MODEL", "")
	t.Setenv("OPENAI_SCOUT_EXTRACTION_MODEL", "")
	if got := scoutRouterModel(); got != "gpt-5.6-luna" {
		t.Fatalf("router model=%q, want Luna", got)
	}
	if got := scoutChatModel(); got != "gpt-5.6-terra" {
		t.Fatalf("chat model=%q, want Terra", got)
	}
	if got := scoutExtractionModel(); got != "gpt-5.6-luna" {
		t.Fatalf("extraction model=%q, want Luna", got)
	}
}

func TestScoutCoreIgnoresInstalledAnthropicKey(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	t.Setenv("ANTHROPIC_API_KEY", "installed-but-unavailable")
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-core-key"

	swapAnthropicTextResponder(t, func(context.Context, string, anthropicTextRequest) (string, error) {
		t.Fatal("Anthropic text responder must not receive core Scout traffic")
		return "", nil
	})
	swapAnthropicMessagesResponder(t, func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		t.Fatal("Anthropic Messages responder must not receive core Scout traffic")
		return anthropicMessagesResponse{}, nil
	})

	var workflows []string
	swapOpenAITextResponder(t, func(_ context.Context, apiKey string, request openAITextRequest) (string, error) {
		if apiKey != "openai-core-key" {
			t.Fatalf("apiKey=%q, want OpenAI core key", apiKey)
		}
		workflows = append(workflows, request.Workflow)
		switch request.Workflow {
		case "scout_route":
			if request.Model != defaultScoutRouterModel || request.JSONSchema == nil || request.Seat != seatRouter || request.ReasoningEffort != scoutRouterReasoningEffort() {
				t.Fatalf("router request=%+v, want Luna/medium route seat", request)
			}
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Outcome: string(conversationIntentConversationalReply)}), nil
		case "scout_chat":
			if request.Model != defaultScoutChatModel || request.Seat != seatChat || request.ReasoningEffort != scoutReasoningEffort() {
				t.Fatalf("chat request=%+v, want Terra/high chat seat", request)
			}
			return "OpenAI core answer", nil
		default:
			t.Fatalf("unexpected OpenAI workflow %q", request.Workflow)
			return "", nil
		}
	})

	if verdict := app.routeScoutChatTurn(context.Background(), "what did we decide?", nil); verdict != nil {
		t.Fatalf("inline verdict=%#v, want nil", verdict)
	}
	answer, err := app.answerAssistantQueryWithModel(context.Background(), "aj@shareability.com", "what did we decide?", nil, nil, nil)
	if err != nil || answer != "OpenAI core answer" {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	if strings.Join(workflows, ",") != "scout_route,scout_chat" {
		t.Fatalf("workflows=%v, want router then chat", workflows)
	}
	if state := capabilityState(capabilityTypedScoutRouter); state.LastSuccess.IsZero() || state.LastError != "" {
		t.Fatalf("router capability=%+v, want accepted OpenAI output", state)
	}
	if state := capabilityState(capabilityTypedScoutAnswer); state.LastSuccess.IsZero() || state.LastError != "" {
		t.Fatalf("answer capability=%+v, want accepted OpenAI output", state)
	}
}

func TestScoutRouterStrictOutputBuildsProposalAndRejectsInvalidOutput(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-core-key"
	responses := []struct {
		text string
		err  error
	}{
		{text: openAIScoutRouteJSON(t, openAIScoutRouterOutput{Route: "tool_run", ToolID: "comps_precedent", Objective: "price the rodeo doc"})},
		{text: `{"route":"tool_run","tool_id":"invented"}`},
		{err: errors.New("provider unavailable")},
	}
	swapOpenAITextResponder(t, func(context.Context, string, openAITextRequest) (string, error) {
		response := responses[0]
		responses = responses[1:]
		return response.text, response.err
	})

	verdict := app.routeScoutChatTurn(context.Background(), "pull comps for the rodeo doc", nil)
	if verdict == nil || verdict.proposal == nil || verdict.proposal.ToolID != "comps_precedent" {
		t.Fatalf("proposal verdict=%#v", verdict)
	}
	if verdict = app.routeScoutChatTurn(context.Background(), "do the thing", nil); verdict != nil {
		t.Fatalf("invalid strict output verdict=%#v, want degraded inline", verdict)
	}
	if verdict = app.routeScoutChatTurn(context.Background(), "do the next thing", nil); verdict != nil {
		t.Fatalf("provider failure verdict=%#v, want degraded inline", verdict)
	}
}

func TestScoutImageAnalysisRoutesInlineWithoutWorkstream(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-core-key"
	calls := 0
	swapOpenAITextResponder(t, func(context.Context, string, openAITextRequest) (string, error) {
		calls++
		return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Route: "workstream", Mode: "research", Objective: "wrong"}), nil
	})

	if verdict := app.routeScoutChatTurn(context.Background(), "Can you analyze this image and reverse engineer the likely prompt?", nil); verdict != nil {
		t.Fatalf("image analysis verdict=%#v, want ordinary inline answer", verdict)
	}
	if calls != 0 {
		t.Fatalf("router calls=%d, want deterministic inline route", calls)
	}
	if scoutChatRequestIsFileWork("analyze this image and reverse engineer the prompt") {
		t.Fatal("ordinary source analysis must not mint durable work")
	}
	if !scoutChatRequestIsFileWork("analyze this image and produce a report with recommendations") {
		t.Fatal("explicit report/recommendation must remain durable work")
	}
}

func TestPrivateRealtimeMilestoneHealthContract(t *testing.T) {
	setupAuthTestEnv(t)
	resetCapabilityRuntimeForTest(t)
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	post := func(milestone string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/assistant/realtime/milestone", strings.NewReader(`{"milestone":`+"\""+milestone+"\"}"))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantRealtimeMilestoneHandler(recorder, req)
		return recorder
	}
	if recorder := post("data_channel_open"); recorder.Code != http.StatusOK {
		t.Fatalf("data-channel milestone status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	state := capabilityState(capabilityPrivateVoice)
	if state.LastMilestone != "data_channel_open" || state.MilestoneSource != "client" || !state.LastSuccess.IsZero() {
		t.Fatalf("intermediate capability=%+v, want milestone without accepted-output success", state)
	}
	if recorder := post("first_audio"); recorder.Code != http.StatusOK {
		t.Fatalf("first-audio milestone status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	state = capabilityState(capabilityPrivateVoice)
	if state.LastMilestone != "first_audio" || state.MilestoneSource != "client" || !state.LastSuccess.IsZero() || state.LastError != "" {
		t.Fatalf("client-attested audio must remain diagnostic-only: %+v", state)
	}
	if recorder := post("made_up"); recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown milestone status=%d, want 400", recorder.Code)
	}
}

func TestCapabilitiesExposeIndependentScoutAndSTTLanes(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("OPENAI_DICTATION_TRANSCRIPT_MODEL", "gpt-transcribe")
	now := time.Now().UTC()
	recordCapabilitySuccess(capabilityDictation, now)
	recordCapabilityFailure(capabilityTypedScoutRouter, now, errors.New("router failure"))

	snapshot, degraded := capabilitySnapshot(now)
	for _, key := range []string{"roomVoice", "privateVoice", "meetingSTT", "dictation", "typedScoutRouter", "typedScoutAnswer"} {
		if _, ok := snapshot[key].(map[string]any); !ok {
			t.Fatalf("snapshot missing %q: %v", key, snapshot)
		}
	}
	dictation := snapshot["dictation"].(map[string]any)
	if dictation["status"] != "healthy" || dictation["model"] != "gpt-transcribe" {
		t.Fatalf("dictation=%v", dictation)
	}
	router := snapshot["typedScoutRouter"].(map[string]any)
	if router["status"] != "degraded" || router["provider"] != providerOpenAI || router["model"] != defaultScoutRouterModel {
		t.Fatalf("router=%v", router)
	}
	if !containsString(degraded, "typedScoutRouter") {
		t.Fatalf("degraded=%v, want typedScoutRouter", degraded)
	}
}

func TestCapabilitiesDoNotCallAllocatedTranscriptionLaneConnected(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.transcriptLane = newMeetingTranscriptionLane(app, "test-openai-key", "gpt-transcribe")
	app.mu.Unlock()
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	recordCapabilitySuccess(capabilityMeetingSTT, time.Now().UTC())

	snapshot, degraded := capabilitySnapshot(time.Now().UTC())
	meeting := snapshot["meetingSTT"].(map[string]any)
	if meeting["connected"] != false || meeting["status"] != "degraded" {
		t.Fatalf("allocated but disconnected lane reported healthy: %v", meeting)
	}
	if !containsString(degraded, "meetingSTT") {
		t.Fatalf("degraded=%v, want meetingSTT", degraded)
	}
}
