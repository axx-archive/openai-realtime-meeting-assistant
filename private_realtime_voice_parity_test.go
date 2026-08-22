package main

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPrivateRealtimeConversationResultPreservesServerMintedActions(t *testing.T) {
	actions := []map[string]any{{"type": "open_tool", "tool": "chat"}}
	result := privateRealtimeConversationResult(map[string]any{
		"intentOutcome": string(conversationIntentConversationalReply),
		"answer": scoutChatMessageRecord{
			Text: "Opening the authorized destination.", IntentOutcome: string(conversationIntentConversationalReply),
		},
		"actions": actions,
	}, "voice-thread")
	got, ok := result["actions"].([]map[string]any)
	if !ok || len(got) != 1 || got[0]["tool"] != "chat" {
		t.Fatalf("voice result dropped or rewrote typed-router actions: %#v", result)
	}
}

func TestPrivateRealtimeVoiceSessionForbidsParallelToolCalls(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	session := app.privateRealtimeVoiceSessionConfig("gpt-realtime-2.1")
	if session["tool_choice"] != "required" || session["parallel_tool_calls"] != false {
		t.Fatalf("private Realtime route must be required and singular: %#v", session)
	}
}

func TestPrivateRealtimeVoiceRoutesEveryAuthoredWorkFamilyThroughTypedScout(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerOpenAIText)
	t.Setenv("OPENAI_API_KEY", "openai-private-voice-parity")
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-private-voice-parity"
	voiceSessionID := "voice-authored-work-parity"
	thread, _, err := app.ensurePrivateRealtimeVoiceConversation("aj@shareability.com", "AJ", voiceSessionID)
	if err != nil {
		t.Fatal(err)
	}

	previousGoalStarter := startGoalThreadAsync
	previousAgentStarter := startAgentThreadAsync
	previousImageStarter := startScoutChatImageAsyncWithPending
	var goalStarts atomic.Int32
	var agentStarts atomic.Int32
	var imageStarts atomic.Int32
	startGoalThreadAsync = func(*kanbanBoardApp, string) { goalStarts.Add(1) }
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) { agentStarts.Add(1) }
	startScoutChatImageAsyncWithPending = func(*kanbanBoardApp, string, string, string, string, string) { imageStarts.Add(1) }
	t.Cleanup(func() {
		startGoalThreadAsync = previousGoalStarter
		startAgentThreadAsync = previousAgentStarter
		startScoutChatImageAsyncWithPending = previousImageStarter
	})

	var routerCalls atomic.Int32
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_route" {
			t.Fatalf("authored work reached unexpected provider workflow %q", request.Workflow)
		}
		routerCalls.Add(1)
		switch {
		case strings.Contains(request.Input, "research the creator landscape"):
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
				Outcome: string(conversationIntentStartPrivateWork), Route: "workstream", Mode: "research",
				Objective: "Research the creator landscape and return sourced findings",
			}), nil
		case strings.Contains(request.Input, "editorial image"):
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
				Outcome: string(conversationIntentStartPrivateWork), Route: "image",
				Objective: "A rights-safe editorial image of a western creator field team",
				Prompt:    "A rights-safe editorial image of a western creator field team",
			}), nil
		default:
			t.Fatalf("deterministic deck/document route unexpectedly reached model router:\n%s", request.Input)
			return "", nil
		}
	})

	tests := []struct {
		name       string
		utterance  string
		callID     string
		wantWorkID bool
	}{
		{name: "deck", utterance: "Build a six-slide presentation for the western creator opportunity", callID: "voice-work-deck", wantWorkID: true},
		{name: "document", utterance: "Write a market report about the western creator opportunity", callID: "voice-work-document", wantWorkID: true},
		{name: "image", utterance: "Generate an editorial image of a western creator field team", callID: "voice-work-image"},
		{name: "general research", utterance: "Please research the creator landscape and return sourced findings", callID: "voice-work-research", wantWorkID: true},
	}
	for _, test := range tests {
		result, changed, routeErr := app.applyPrivateRealtimeVoiceSessionModelTool(
			context.Background(), "aj@shareability.com", voiceSessionID, thread.ID, test.callID,
			"route_conversation_turn", map[string]any{"utterance": test.utterance},
		)
		if routeErr != nil || changed || result["outcome"] != string(conversationIntentStartPrivateWork) {
			t.Fatalf("%s result=%#v changed=%t err=%v", test.name, result, changed, routeErr)
		}
		if test.wantWorkID && strings.TrimSpace(asString(result["work_id"])) == "" {
			t.Fatalf("%s did not return the durable typed-Scout work receipt: %#v", test.name, result)
		}
	}
	if goalStarts.Load() != 2 || agentStarts.Load() != 1 || imageStarts.Load() != 1 || routerCalls.Load() != 2 {
		t.Fatalf("starts goal=%d agent=%d image=%d router=%d, want 2/1/1/2", goalStarts.Load(), agentStarts.Load(), imageStarts.Load(), routerCalls.Load())
	}

	// Provider/call replay is the exactly-once boundary across reconnects.
	result, changed, err := app.applyPrivateRealtimeVoiceSessionModelTool(
		context.Background(), "aj@shareability.com", voiceSessionID, thread.ID, "voice-work-deck",
		"route_conversation_turn", map[string]any{"utterance": "Build a six-slide presentation for the western creator opportunity"},
	)
	if err != nil || changed || strings.TrimSpace(asString(result["work_id"])) == "" || goalStarts.Load() != 2 {
		t.Fatalf("reconnect replay duplicated work: result=%#v changed=%t goals=%d err=%v", result, changed, goalStarts.Load(), err)
	}
}

func TestPrivateRealtimeVoiceBrainQuestionUsesCurrentAuthorizedCompanyContext(t *testing.T) {
	fixture := newSTRIDECoworkerTestFixture(t)
	fixture.app.apiKey = "openai-private-voice-brain"
	channel, created, err := fixture.app.ensureScoutChatThread(
		"voice-brain-current-channel", fixture.user.Email, fixture.user.Name,
		"dogcenter voice", scoutChatVisibilityPublic, nil,
	)
	if err != nil || !created {
		t.Fatalf("create company channel: created=%t err=%v", created, err)
	}
	const sourceText = "The verified Campfire Relay proof target is 120 attributable creator posts."
	if _, err := fixture.app.commitScoutChatThreadMessages(fixture.user.Email, channel.ID, scoutChatMessageRecord{
		ID: "voice-brain-current-source", Kind: "message", Role: "user", Text: sourceText,
		AuthorName: "Dr. May", AuthorEmail: "tom@shareability.com", CreatedAt: time.Date(2026, 8, 21, 16, 35, 0, 0, time.UTC).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	voiceSessionID := "voice-brain-parity"
	voiceThread, _, err := fixture.app.ensurePrivateRealtimeVoiceConversation(fixture.user.Email, fixture.user.Name, voiceSessionID)
	if err != nil {
		t.Fatal(err)
	}
	var answerCalls atomic.Int32
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		switch request.Workflow {
		case "scout_route":
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Outcome: string(conversationIntentConversationalReply)}), nil
		case "scout_chat":
			answerCalls.Add(1)
			if !strings.Contains(request.Input, sourceText) {
				t.Fatalf("private voice Brain answer missed current authorized company context:\n%s", request.Input)
			}
			return "Dr. May recorded Campfire Relay with a proof target of 120 attributable creator posts.", nil
		default:
			t.Fatalf("unexpected Brain workflow %q", request.Workflow)
			return "", nil
		}
	})
	result, changed, err := fixture.app.applyPrivateRealtimeVoiceSessionModelTool(
		context.Background(), fixture.user.Email, voiceSessionID, voiceThread.ID, "voice-brain-call",
		"route_conversation_turn", map[string]any{"utterance": "What verified Campfire Relay proof target did Dr. May record in dogcenter voice?"},
	)
	if err != nil || changed || result["outcome"] != string(conversationIntentConversationalReply) ||
		!strings.Contains(asString(result["message"]), "Campfire Relay") || answerCalls.Load() != 1 {
		t.Fatalf("Brain result=%#v changed=%t answerCalls=%d err=%v", result, changed, answerCalls.Load(), err)
	}
}

func TestPrivateRealtimeVoiceOpenChatNavigatesWithoutRebindingOrPublishing(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "voice-chat-navigation"
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user unavailable")
	}
	target, err := app.createScoutChatThread(user.Email, user.Name, "Like A Farmer", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	voiceSessionID := "voice-navigation-parity"
	voiceThread, _, err := app.ensurePrivateRealtimeVoiceConversation(user.Email, user.Name, voiceSessionID)
	if err != nil {
		t.Fatal(err)
	}
	targetBefore := len(target.Messages)
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_route" {
			t.Fatalf("voice navigation reached unexpected workflow %q", request.Workflow)
		}
		return openChatThreadRouteJSON(t, target.Title, scoutChatVisibilityPublic), nil
	})
	result, changed, err := app.applyPrivateRealtimeVoiceSessionModelTool(
		context.Background(), user.Email, voiceSessionID, voiceThread.ID, "voice-open-like-a-farmer",
		"route_conversation_turn", map[string]any{"utterance": "Open the Like A Farmer channel"},
	)
	if err != nil || changed || result["outcome"] != string(conversationIntentStartPrivateWork) || result["thread_id"] != voiceThread.ID {
		t.Fatalf("voice navigation result=%#v changed=%t err=%v", result, changed, err)
	}
	actions, ok := result["actions"].([]map[string]any)
	if !ok || len(actions) != 1 || actions[0]["type"] != "open_chat_thread" || actions[0]["threadId"] != target.ID ||
		result["voice_context_policy"] != "private_scout_thread" || result["voice_context_thread_id"] != voiceThread.ID {
		t.Fatalf("voice navigation action=%#v", result["actions"])
	}
	if _, leaked := actions[0]["voiceContext"]; leaked {
		t.Fatalf("shared navigation action carried a false voice-only claim: %#v", actions[0])
	}
	if asString(result["message"]) != "Opening #Like A Farmer. Nothing was posted there." {
		t.Fatalf("voice navigation did not preserve the neutral typed-router receipt: %#v", result)
	}
	targetAfter, _, err := app.scoutChatThreadByID(user.Email, target.ID)
	if err != nil || len(targetAfter.Messages) != targetBefore {
		t.Fatalf("voice navigation published ambient speech: before=%d after=%d err=%v", targetBefore, len(targetAfter.Messages), err)
	}
	bound, err := app.privateRealtimeVoiceConversation(user.Email, voiceSessionID, voiceThread.ID)
	if err != nil || bound.ID != voiceThread.ID || bound.ID == target.ID {
		t.Fatalf("voice binding was rebound to destination: bound=%+v err=%v", bound, err)
	}
}

func TestPrivateRealtimeVoiceOpensOwnerPrivateChatWithoutRebindingOrPublishing(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "voice-private-chat-navigation"
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user unavailable")
	}
	target, err := app.createScoutChatThread(user.Email, user.Name, "Western creator lab", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	voiceSessionID := "voice-private-navigation-parity"
	voiceThread, _, err := app.ensurePrivateRealtimeVoiceConversation(user.Email, user.Name, voiceSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if target.ID == voiceThread.ID {
		t.Fatal("test target unexpectedly reused the bound voice conversation")
	}
	targetBefore := len(target.Messages)
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_route" {
			t.Fatalf("voice private navigation reached unexpected workflow %q", request.Workflow)
		}
		return openChatThreadRouteJSON(t, target.Title, scoutChatVisibilityPrivate), nil
	})
	result, changed, err := app.applyPrivateRealtimeVoiceSessionModelTool(
		context.Background(), user.Email, voiceSessionID, voiceThread.ID, "voice-open-private-western-creator-lab",
		"route_conversation_turn", map[string]any{"utterance": "Open my private Western creator lab chat"},
	)
	if err != nil || changed || result["outcome"] != string(conversationIntentStartPrivateWork) || result["thread_id"] != voiceThread.ID {
		t.Fatalf("voice private navigation result=%#v changed=%t err=%v", result, changed, err)
	}
	actions, ok := result["actions"].([]map[string]any)
	if !ok || len(actions) != 1 || actions[0]["type"] != "open_chat_thread" || actions[0]["threadId"] != target.ID ||
		actions[0]["visibility"] != scoutChatVisibilityPrivate || result["voice_context_policy"] != "private_scout_thread" ||
		result["voice_context_thread_id"] != voiceThread.ID {
		t.Fatalf("voice private navigation action=%#v", result)
	}
	if asString(result["message"]) != "Opening Western creator lab. Nothing was posted there." {
		t.Fatalf("voice private navigation did not preserve the typed-router receipt: %#v", result)
	}
	targetAfter, _, err := app.scoutChatThreadByID(user.Email, target.ID)
	if err != nil || len(targetAfter.Messages) != targetBefore {
		t.Fatalf("voice private navigation published ambient speech: before=%d after=%d err=%v", targetBefore, len(targetAfter.Messages), err)
	}
	bound, err := app.privateRealtimeVoiceConversation(user.Email, voiceSessionID, voiceThread.ID)
	if err != nil || bound.ID != voiceThread.ID || bound.ID == target.ID {
		t.Fatalf("voice binding was rebound to owner-private destination: bound=%+v err=%v", bound, err)
	}
}
