package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConversationIntentContractAcceptsExactlyFiveExclusiveShapes(t *testing.T) {
	valid := []conversationIntentDecision{
		{Outcome: conversationIntentConversationalReply},
		{Outcome: conversationIntentClarifyOnce, Question: "Who is this for?", Options: []scoutChatChoiceOption{{ID: "opt-1", Label: "Investors"}, {ID: "opt-2", Label: "The team"}}},
		{Outcome: conversationIntentStartPrivateWork, Work: &conversationWorkDecision{Kind: conversationWorkWorkstream, Mode: "research", Objective: "Research the market"}},
		{Outcome: conversationIntentApprovalRequired, Approval: &conversationApprovalDecision{EffectClass: "external_send", Summary: "Send this to the channel?", Work: &conversationWorkDecision{Kind: conversationWorkNativeAction, ToolID: "post_to_channel", Objective: "Post the update", Fields: map[string]string{"channel": "team", "text": "Update"}}}},
		{Outcome: conversationIntentUnavailable, Unavailable: &conversationUnavailableDecision{Code: "source_missing", Message: "Attach the source first."}},
	}
	for _, decision := range valid {
		if err := decision.validate(); err != nil {
			t.Errorf("valid outcome %q rejected: %v", decision.Outcome, err)
		}
	}

	invalid := []conversationIntentDecision{
		{Outcome: conversationIntentConversationalReply, Work: &conversationWorkDecision{Kind: conversationWorkGoal, Objective: "hidden work"}},
		{Outcome: conversationIntentClarifyOnce, Question: "Which?", Options: []scoutChatChoiceOption{{Label: "Deck", ToolID: "deck_outline"}, {Label: "Memo"}}},
		{Outcome: conversationIntentStartPrivateWork, Work: &conversationWorkDecision{Kind: conversationWorkGoal, Objective: "Deploy it", Authority: codexJobAuthorityExternalWrite}},
		{Outcome: conversationIntentApprovalRequired, Approval: &conversationApprovalDecision{EffectClass: "deletion", Summary: "Delete?"}},
		{Outcome: conversationIntentUnavailable},
	}
	for index, decision := range invalid {
		if err := decision.validate(); err == nil {
			t.Errorf("invalid decision %d (%q) was accepted", index, decision.Outcome)
		}
	}
}

func TestConversationIntentModelInputIsModalityBlind(t *testing.T) {
	modalities := []conversationTurnModality{
		conversationModalityTypedText,
		conversationModalityComposerDictation,
		conversationModalityPrivateRealtimeVoice,
		conversationModalityScoutChat,
	}
	var first string
	for _, modality := range modalities {
		got, err := conversationIntentModelText(conversationIntentTurn{Text: "Build a source-bound market brief", ReplyContext: "the Aurora note", AttachmentsContext: "aurora.pdf", Modality: modality})
		if err != nil {
			t.Fatalf("modality %q: %v", modality, err)
		}
		if first == "" {
			first = got
		} else if got != first {
			t.Fatalf("modality %q changed the classifier input\nfirst: %q\n got: %q", modality, first, got)
		}
	}
	typed, err := conversationIntentModelText(conversationIntentTurn{Text: "Research it", AddressedAgentID: "agent-colton", Modality: conversationModalityTypedText})
	if err != nil {
		t.Fatal(err)
	}
	direct, err := conversationIntentModelText(conversationIntentTurn{Text: "Research it", AddressedAgentID: "agent-colton", Modality: conversationModalityDirectAgentChat})
	if err != nil {
		t.Fatal(err)
	}
	if typed != direct || !strings.Contains(direct, "does not expand capability") {
		t.Fatalf("addressed-agent input drifted by modality: typed=%q direct=%q", typed, direct)
	}
}

func TestConversationIntentOpenAIOutputEnforcesOutcomeAndApprovalBoundary(t *testing.T) {
	start, err := scoutConversationIntentFromOpenAI(openAIScoutRouterOutput{
		Outcome: string(conversationIntentStartPrivateWork), Route: "tool_run", ToolID: "one_pager", Objective: "Write the Aurora one-pager",
	}, "write the one-pager")
	if err != nil || start.Outcome != conversationIntentStartPrivateWork || start.Work == nil || start.Work.ToolID != "one_pager" {
		t.Fatalf("start=%+v err=%v", start, err)
	}

	blockedDeletion, err := scoutConversationIntentFromOpenAI(openAIScoutRouterOutput{
		Outcome: string(conversationIntentStartPrivateWork), Route: "app_action", ToolID: "delete_ticket", Objective: "Delete the card",
		Fields: []openAIScoutRouterField{{Key: "title", Value: "Launch"}},
	}, "delete the Launch card")
	if err != nil || blockedDeletion.Outcome != conversationIntentUnavailable || blockedDeletion.Unavailable == nil || blockedDeletion.Unavailable.Code != "tool_unadmitted" {
		t.Fatalf("unadmitted deletion=%+v err=%v", blockedDeletion, err)
	}

	approval, err := scoutConversationIntentFromOpenAI(openAIScoutRouterOutput{
		Outcome: string(conversationIntentApprovalRequired), Route: "app_action", ToolID: "delete_ticket", Objective: "Delete the card",
		EffectClass: "deletion", Message: "Delete the Launch card?",
		Fields: []openAIScoutRouterField{{Key: "title", Value: "Launch"}},
	}, "delete the Launch card")
	if err != nil || approval.Outcome != conversationIntentUnavailable || approval.Unavailable == nil || approval.Unavailable.Code != "tool_unadmitted" {
		t.Fatalf("unadmitted approved action=%+v err=%v", approval, err)
	}

	governed, err := scoutConversationIntentFromOpenAI(openAIScoutRouterOutput{
		Outcome: string(conversationIntentStartPrivateWork), Route: "goal_run", Objective: "Push the reviewed change to GitHub",
		AuthorityHint: toolAuthorityWorkspaceWrite,
	}, "push the reviewed change")
	if err != nil || governed.Outcome != conversationIntentApprovalRequired || governed.Approval == nil || governed.Approval.EffectClass != "repository_mutation" {
		t.Fatalf("server-upgraded governed work=%+v err=%v", governed, err)
	}
}

func TestConversationIntentPrivateToolRequestStartsWithoutProposal(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-router-test"
	previousGoalStarter := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStarter })
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_route" {
			t.Fatalf("unexpected workflow %q", request.Workflow)
		}
		return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
			Outcome: string(conversationIntentStartPrivateWork), Route: "tool_run", ToolID: "one_pager", Objective: "Write the Aurora investor one-pager",
		}), nil
	})
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Aurora", "")
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("aj@shareability.com")
	ctx := withConversationTurnOperation(context.Background(), conversationTurnOperation{
		ID: "conversation-private-tool-0001", BodyDigest: sha256Hex([]byte("Write the Aurora investor one-pager")),
	})
	response, err := app.appendScoutChatThreadMessage(ctx, user, thread.ID, "Write the Aurora investor one-pager", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if response["intentOutcome"] != string(conversationIntentStartPrivateWork) || response["proposal"] != nil {
		t.Fatalf("response=%#v, want direct private work and no proposal", response)
	}
	launched, ok := response["agentThread"].(scoutAgentThread)
	if !ok || launched.Mode != "goal" || launched.Artifact.Metadata["toolTemplate"] != "one_pager" {
		t.Fatalf("launched=%#v, want one-pager goal", response["agentThread"])
	}
	saved := response["thread"].(scoutChatThreadRecord)
	if len(saved.Messages) != 2 || saved.Messages[1].Kind != "thread" || saved.Messages[1].IntentOutcome != string(conversationIntentStartPrivateWork) {
		t.Fatalf("messages=%#v", saved.Messages)
	}
}

func TestConversationIntentClarifiesOnceThenFailsClosed(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-router-test"
	calls := 0
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_route" {
			t.Fatalf("unexpected workflow %q", request.Workflow)
		}
		calls++
		return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
			Outcome: string(conversationIntentClarifyOnce), Question: "Who is this for?",
			Options: []openAIScoutRouterOption{{Label: "Investors", Reply: "Investors"}, {Label: "The team", Reply: "The team"}},
		}), nil
	})
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Draft", "")
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("aj@shareability.com")
	first, err := app.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "Draft it", nil, "")
	if err != nil || first["intentOutcome"] != string(conversationIntentClarifyOnce) {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := app.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "Investors", nil, "")
	if err != nil || second["intentOutcome"] != string(conversationIntentUnavailable) {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	if calls != 2 {
		t.Fatalf("router calls=%d, want two bounded classifications", calls)
	}
	saved := second["thread"].(scoutChatThreadRecord)
	clarifications := 0
	for _, message := range saved.Messages {
		if message.IntentOutcome == string(conversationIntentClarifyOnce) {
			clarifications++
		}
	}
	if clarifications != 1 {
		t.Fatalf("clarification messages=%d, want exactly one", clarifications)
	}
}

func TestConversationIntentHTTPClientToolTemplateIsIgnored(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = "openai-router-test"
	t.Cleanup(func() { kanbanApp = previousApp })
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		switch request.Workflow {
		case "scout_route":
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Outcome: string(conversationIntentConversationalReply)}), nil
		case "scout_chat":
			return "Let's talk through the assumptions.", nil
		default:
			t.Fatalf("unexpected workflow %q", request.Workflow)
			return "", nil
		}
	})
	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Conversation", "")
	if err != nil {
		t.Fatal(err)
	}
	body := `{"text":"Let's discuss the model","toolTemplate":"economics_waterfall","operationId":"client-selected-tool"}`
	req := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+thread.ID+"/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantChatThreadHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["clientToolTemplateIgnored"] != true || response["intentOutcome"] != string(conversationIntentConversationalReply) {
		t.Fatalf("response=%v", response)
	}
	if _, launched := response["agentThread"]; launched {
		t.Fatalf("client toolTemplate launched work: %v", response)
	}
}

func TestConversationIntentHTTPOperationLostResponseReplayConflictAndStrictEnvelope(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = "openai-router-test"
	previousGoalStarter := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() {
		kanbanApp = previousApp
		startGoalThreadAsync = previousGoalStarter
	})
	var routerCalls atomic.Int32
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_route" {
			t.Fatalf("unexpected workflow %q", request.Workflow)
		}
		routerCalls.Add(1)
		return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
			Outcome: string(conversationIntentStartPrivateWork), Route: "tool_run", ToolID: "one_pager", Objective: "Create the Aurora investor update",
		}), nil
	})
	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "HTTP operation", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+thread.ID+"/messages", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantChatThreadHandler(recorder, req)
		return recorder
	}
	body := `{"text":"Create a concise investor update for Aurora","operationId":"http-conversation-operation-0001"}`
	first := post(body)
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	replay := post(body)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	var replayPayload map[string]any
	if err := json.Unmarshal(replay.Body.Bytes(), &replayPayload); err != nil {
		t.Fatal(err)
	}
	if replayPayload["replayed"] != true || routerCalls.Load() != 1 {
		t.Fatalf("replay=%v router calls=%d", replayPayload, routerCalls.Load())
	}
	conflict := post(`{"text":"Create a different investor update","operationId":"http-conversation-operation-0001"}`)
	if conflict.Code != http.StatusConflict || routerCalls.Load() != 1 {
		t.Fatalf("conflict status=%d body=%s router calls=%d", conflict.Code, conflict.Body.String(), routerCalls.Load())
	}
	duplicate := post(`{"text":"Hello","operationId":"http-conversation-operation-0002","operationId":"http-conversation-operation-0003"}`)
	if duplicate.Code != http.StatusBadRequest || routerCalls.Load() != 1 {
		t.Fatalf("duplicate status=%d body=%s router calls=%d", duplicate.Code, duplicate.Body.String(), routerCalls.Load())
	}
	widened := post(`{"text":"Hello","operationId":"http-conversation-operation-0004","model":"gpt-5.6-sol"}`)
	if widened.Code != http.StatusBadRequest || routerCalls.Load() != 1 {
		t.Fatalf("widened status=%d body=%s router calls=%d", widened.Code, widened.Body.String(), routerCalls.Load())
	}
}

func TestPrivateRealtimeVoiceUsesSharedConversationContractAndReplays(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-router-test"
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Voice continuity", scoutChatVisibilityPrivate)
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
			return "The core assumption is distribution, not demand.", nil
		default:
			t.Fatalf("unexpected workflow %q", request.Workflow)
			return "", nil
		}
	})

	first, changed, err := app.applyPrivateRealtimeVoiceModelTool(context.Background(), "aj@shareability.com", "call_voice_1", "route_conversation_turn", map[string]any{"utterance": "What is the core assumption?"})
	if err != nil || changed {
		t.Fatalf("first=%v changed=%v err=%v", first, changed, err)
	}
	if first["outcome"] != string(conversationIntentConversationalReply) || first["message"] != "The core assumption is distribution, not demand." || first["thread_id"] != thread.ID {
		t.Fatalf("first=%v", first)
	}
	if calls != 2 {
		t.Fatalf("provider calls=%d, want router+answer", calls)
	}

	replay, changed, err := app.applyPrivateRealtimeVoiceModelTool(context.Background(), "aj@shareability.com", "call_voice_1", "route_conversation_turn", map[string]any{"utterance": "What is the core assumption?"})
	if err != nil || changed || replay["outcome"] != first["outcome"] || replay["message"] != first["message"] || calls != 2 {
		t.Fatalf("replay=%v changed=%v calls=%d err=%v", replay, changed, calls, err)
	}
	if _, _, err := app.applyPrivateRealtimeVoiceModelTool(context.Background(), "aj@shareability.com", "call_voice_1", "route_conversation_turn", map[string]any{"utterance": "Use different words"}); err == nil {
		t.Fatal("call id reuse with changed utterance must fail closed")
	}
	if calls != 2 {
		t.Fatalf("collision reached provider: calls=%d", calls)
	}

	saved, _, err := app.scoutChatThreadByID("aj@shareability.com", thread.ID)
	if err != nil || len(saved.Messages) != 2 {
		t.Fatalf("saved messages=%#v err=%v", saved.Messages, err)
	}
	if saved.Messages[0].SourceOperationID != "call_voice_1" || saved.Messages[0].SourceOperationDigest == "" || saved.Messages[1].IntentOutcome != string(conversationIntentConversationalReply) {
		t.Fatalf("durable replay binding missing: %#v", saved.Messages)
	}
	projected := app.projectScoutChatThreadForViewer("aj@shareability.com", saved)
	if projected.Messages[0].SourceOperationID != "" || projected.Messages[0].SourceOperationDigest != "" {
		t.Fatalf("server-only replay binding leaked to viewer: %#v", projected.Messages[0])
	}
}

func TestPrivateRealtimeVoiceRejectsDirectToolsAndStrictArgumentWidening(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	before := len(app.snapshotState().Cards)
	for _, name := range []string{"launch_agent_thread", "initiate_goal", "create_ticket", "delete_ticket", "post_to_channel", "create_artifact", "answer_memory_question"} {
		if _, _, err := app.applyPrivateRealtimeVoiceModelTool(context.Background(), "aj@shareability.com", "call_reject_"+name, name, map[string]any{}); err == nil {
			t.Errorf("direct model-selected tool %q was admitted", name)
		}
	}
	if got := len(app.snapshotState().Cards); got != before {
		t.Fatalf("rejected direct tools mutated board: before=%d after=%d", before, got)
	}
	if _, _, err := app.applyPrivateRealtimeVoiceModelTool(context.Background(), "aj@shareability.com", "call_extra", "route_conversation_turn", map[string]any{"utterance": "hello", "tool_id": "one_pager"}); err == nil {
		t.Fatal("widened route arguments were admitted")
	}
	if _, _, err := app.applyPrivateRealtimeVoiceModelTool(context.Background(), "aj@shareability.com", "call_wrong_type", "route_conversation_turn", map[string]any{"utterance": 7}); err == nil {
		t.Fatal("non-string utterance was admitted")
	}
}

func TestPrivateRealtimeVoicePrivateWorkStartsWithoutModelSelectingTool(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-router-test"
	previousGoalStarter := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStarter })
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Voice work", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_route" {
			t.Fatalf("unexpected workflow %q", request.Workflow)
		}
		return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
			Outcome: string(conversationIntentStartPrivateWork), Route: "tool_run", ToolID: "one_pager", Objective: "Write the Aurora investor one-pager",
		}), nil
	})
	result, changed, err := app.applyPrivateRealtimeVoiceModelTool(context.Background(), "aj@shareability.com", "call_voice_work_1", "route_conversation_turn", map[string]any{"utterance": "Write the Aurora investor one-pager"})
	if err != nil || changed {
		t.Fatalf("result=%v changed=%v err=%v", result, changed, err)
	}
	if result["outcome"] != string(conversationIntentStartPrivateWork) || result["thread_id"] != thread.ID || strings.TrimSpace(asString(result["work_id"])) == "" {
		t.Fatalf("result=%v", result)
	}
	saved, _, err := app.scoutChatThreadByID("aj@shareability.com", thread.ID)
	if err != nil || len(saved.Messages) != 2 || saved.Messages[1].Thread == nil || saved.Messages[1].Thread.ArtifactID == "" {
		t.Fatalf("saved work card=%#v err=%v", saved.Messages, err)
	}
}

func TestConversationIntentOperationReplaysPrivateWorkAndRejectsChangedBody(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-router-test"
	previousGoalStarter := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStarter })
	var routerCalls atomic.Int32
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_route" {
			t.Fatalf("unexpected workflow %q", request.Workflow)
		}
		routerCalls.Add(1)
		return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
			Outcome: string(conversationIntentStartPrivateWork), Route: "tool_run", ToolID: "one_pager", Objective: "Write the Aurora investor one-pager",
		}), nil
	})
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Operation replay", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("aj@shareability.com")
	operation := conversationTurnOperation{ID: "conversation-private-work-replay-0001", BodyDigest: sha256Hex([]byte("same canonical body"))}
	ctx := withConversationTurnOperation(context.Background(), operation)
	first, err := app.appendScoutChatThreadMessage(ctx, user, thread.ID, "Create a concise investor update for Aurora", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := app.appendScoutChatThreadMessage(ctx, user, thread.ID, "Create a concise investor update for Aurora", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if routerCalls.Load() != 1 || replay["replayed"] != true {
		t.Fatalf("router calls=%d replay=%#v", routerCalls.Load(), replay)
	}
	firstWork := first["agentThread"].(scoutAgentThread)
	replayWork := replay["agentThread"].(scoutAgentThread)
	if firstWork.ID != replayWork.ID {
		t.Fatalf("first=%#v replay=%#v", firstWork, replayWork)
	}
	changed := operation
	changed.BodyDigest = sha256Hex([]byte("changed canonical body"))
	_, err = app.appendScoutChatThreadMessage(withConversationTurnOperation(context.Background(), changed), user, thread.ID, "Different request", nil, "")
	if !errors.Is(err, ErrSTRIDEConversationConflict) {
		t.Fatalf("changed-body error=%v, want conversation conflict", err)
	}
	if routerCalls.Load() != 1 {
		t.Fatalf("changed-body retry reached provider: %d", routerCalls.Load())
	}
}

func TestConversationPresentationRequestShowsOnePremiumWorkCardNotInternalProcessCopy(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-router-test"
	previousGoalStarter := startGoalThreadAsync
	var starts atomic.Int32
	startGoalThreadAsync = func(*kanbanBoardApp, string) { starts.Add(1) }
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStarter })
	var routerCalls atomic.Int32
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_route" {
			t.Fatalf("unexpected workflow %q", request.Workflow)
		}
		routerCalls.Add(1)
		return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
			Outcome: string(conversationIntentStartPrivateWork), Route: "tool_run", ToolID: packagingStudioProcessID,
			Objective: "Create a polished 10-slide pitch deck for the STRIDE platform",
		}), nil
	})
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Presentation", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("aj@shareability.com")
	ctx := withConversationTurnOperation(context.Background(), conversationTurnOperation{
		ID: "presentation-conversation-0001", BodyDigest: sha256Hex([]byte("presentation request body")),
	})
	first, err := app.appendScoutChatThreadMessage(ctx, user, thread.ID, "Can you make a 10 slide deck pitching me this platform?", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := app.appendScoutChatThreadMessage(ctx, user, thread.ID, "Can you make a 10 slide deck pitching me this platform?", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if routerCalls.Load() != 1 || starts.Load() != 1 || replay["replayed"] != true {
		t.Fatalf("router=%d starts=%d replay=%#v", routerCalls.Load(), starts.Load(), replay)
	}
	saved := first["thread"].(scoutChatThreadRecord)
	if len(saved.Messages) != 2 {
		t.Fatalf("messages=%#v, want one user turn plus one work card", saved.Messages)
	}
	card := saved.Messages[1]
	if card.Kind != "thread" || card.IntentOutcome != string(conversationIntentStartPrivateWork) || card.Thread == nil || card.Text != "Presentation in progress" {
		t.Fatalf("presentation card=%#v", card)
	}
	serialized, _ := json.Marshal(saved.Messages)
	if strings.Contains(strings.ToLower(string(serialized)), "packaging studio") || strings.Contains(strings.ToLower(card.Text), "staged process") {
		t.Fatalf("internal presentation machinery leaked into chat: %s", serialized)
	}
}

func TestConversationIntentConcurrentDuplicateStartsOnePrivateWorkRun(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-router-test"
	previousGoalStarter := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStarter })
	var routerCalls atomic.Int32
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_route" {
			t.Fatalf("unexpected workflow %q", request.Workflow)
		}
		routerCalls.Add(1)
		return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
			Outcome: string(conversationIntentStartPrivateWork), Route: "tool_run", ToolID: "one_pager", Objective: "Write one durable investor one-pager",
		}), nil
	})
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Concurrent operation", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("aj@shareability.com")
	ctx := withConversationTurnOperation(context.Background(), conversationTurnOperation{
		ID: "conversation-concurrent-work-0001", BodyDigest: sha256Hex([]byte("concurrent canonical body")),
	})
	const callers = 12
	results := make(chan map[string]any, callers)
	errs := make(chan error, callers)
	var wait sync.WaitGroup
	for index := 0; index < callers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, callErr := app.appendScoutChatThreadMessage(ctx, user, thread.ID, "Create one durable investor update for Aurora", nil, "")
			results <- response
			errs <- callErr
		}()
	}
	wait.Wait()
	close(results)
	close(errs)
	for callErr := range errs {
		if callErr != nil {
			t.Fatalf("duplicate call: %v", callErr)
		}
	}
	workIDs := map[string]bool{}
	for response := range results {
		work, ok := response["agentThread"].(scoutAgentThread)
		if !ok || work.ID == "" {
			t.Fatalf("response missing work: %#v", response)
		}
		workIDs[work.ID] = true
	}
	if routerCalls.Load() != 1 || len(workIDs) != 1 {
		t.Fatalf("router calls=%d work ids=%v", routerCalls.Load(), workIDs)
	}
	saved, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil || len(saved.Messages) != 2 {
		t.Fatalf("messages=%#v err=%v", saved.Messages, err)
	}
}

func TestConversationIntentReconstructsWorkCardAfterPostLaunchCrash(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-router-test"
	previousGoalStarter := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStarter })
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Crash reconciliation", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("aj@shareability.com")
	operation := conversationTurnOperation{ID: "conversation-post-launch-crash-0001", BodyDigest: sha256Hex([]byte("post-launch canonical body"))}
	message := scoutChatMessageRecord{
		ID:   "scout-chat-message-" + sha256Hex([]byte("conversation-turn/v1\x00" + normalizeAccountEmail(user.Email) + "\x00" + thread.ID + "\x00" + operation.ID))[:24],
		Kind: "message", Role: "user", Text: "Write the crash-safe one-pager", AuthorName: user.Name, AuthorEmail: normalizeAccountEmail(user.Email),
		SourceOperationID: operation.ID, SourceOperationDigest: operation.BodyDigest,
	}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, message); err != nil {
		t.Fatal(err)
	}
	ctx := withConversationTurnOperation(context.Background(), operation)
	_, err = app.startConversationPrivateWork(ctx, user, thread, message, conversationWorkDecision{
		Kind: conversationWorkRegistryTool, ToolID: "one_pager", Objective: "Write the crash-safe one-pager",
	}, "", proposalSourceChatRouter, func(...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		return scoutChatThreadRecord{}, errors.New("simulated crash before work-card projection")
	})
	var pending *conversationWorkProjectionPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("launch error=%v, want projection-pending", err)
	}
	current, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil || len(current.Messages) != 1 {
		t.Fatalf("pre-reconcile messages=%#v err=%v", current.Messages, err)
	}
	replayed, found, err := app.replayConversationTurnInThread(context.Background(), user.Email, current, operation)
	if err != nil || !found || replayed["replayed"] != true {
		t.Fatalf("replayed=%#v found=%v err=%v", replayed, found, err)
	}
	work, ok := replayed["agentThread"].(scoutAgentThread)
	if !ok || work.ID == "" {
		t.Fatalf("reconstructed work=%#v", replayed["agentThread"])
	}
	artifact, exists := app.osArtifactByID(savedWorkArtifactID(replayed))
	if !exists || artifact.Metadata["operationId"] != operation.ID || artifact.Metadata["operationBodyDigest"] != operation.BodyDigest {
		t.Fatalf("reconstructed artifact=%#v exists=%v", artifact, exists)
	}
	saved := replayed["thread"].(scoutChatThreadRecord)
	if len(saved.Messages) != 2 || saved.Messages[1].Thread == nil || saved.Messages[1].Thread.ID != work.ID {
		t.Fatalf("reconstructed messages=%#v", saved.Messages)
	}
	again, found, err := app.replayConversationTurnInThread(context.Background(), user.Email, saved, operation)
	if err != nil || !found || again["agentThread"].(scoutAgentThread).ID != work.ID {
		t.Fatalf("second replay=%#v found=%v err=%v", again, found, err)
	}
}

func TestConversationApprovalAcceptedGoalReconcilesLostCardWithoutDuplicateRun(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-router-test"
	var starts atomic.Int32
	previousGoalStarter := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) { starts.Add(1) }
	previousProbe := conversationWorkBeforeCardCommitProbe
	var crashOnce atomic.Bool
	conversationWorkBeforeCardCommitProbe = func(scoutAgentThread) error {
		if crashOnce.CompareAndSwap(false, true) {
			return errors.New("simulated lost response before approved work card")
		}
		return nil
	}
	t.Cleanup(func() {
		startGoalThreadAsync = previousGoalStarter
		conversationWorkBeforeCardCommitProbe = previousProbe
	})
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_route" {
			t.Fatalf("unexpected workflow %q", request.Workflow)
		}
		return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
			Outcome: string(conversationIntentStartPrivateWork), Route: "goal_run",
			Objective: "Push the reviewed Aurora change to GitHub", AuthorityHint: toolAuthorityWorkspaceWrite,
		}), nil
	})
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Approved goal", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("aj@shareability.com")
	routed, err := app.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "Push the reviewed Aurora change to GitHub", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	proposal, ok := routed["proposal"].(*scoutRouterProposal)
	answer, answerOK := routed["answer"].(scoutChatMessageRecord)
	if !ok || !answerOK || proposal.EffectClass != "repository_mutation" || answer.IntentOutcome != string(conversationIntentApprovalRequired) {
		t.Fatalf("approval route=%#v", routed)
	}
	acceptedObjective := proposal.Objective
	_, err = app.resolveScoutChatProposal(context.Background(), user, thread.ID, scoutChatProposalAction{
		Action: "accepted", MessageID: answer.ID, Objective: "Deploy the reviewed Aurora change to production",
	})
	if err == nil || !strings.Contains(err.Error(), "approved request changed") || starts.Load() != 0 {
		t.Fatalf("effect-expanding edit err=%v starts=%d, want pre-claim rejection", err, starts.Load())
	}
	stillPending, pendingErr := app.pendingScoutChatProposal(thread.ID, user.Email, answer.ID)
	if pendingErr != nil || stillPending.Status != "" || stillPending.Objective != acceptedObjective {
		t.Fatalf("changed approval was claimed: pending=%#v err=%v", stillPending, pendingErr)
	}
	_, err = app.resolveScoutChatProposal(context.Background(), user, thread.ID, scoutChatProposalAction{
		Action: "accepted", MessageID: answer.ID, Objective: acceptedObjective,
	})
	var pending *conversationWorkProjectionPendingError
	if !errors.As(err, &pending) || starts.Load() != 1 {
		t.Fatalf("first acceptance err=%v starts=%d, want one durable run plus lost-card error", err, starts.Load())
	}

	reconciled, err := app.resolveScoutChatProposal(context.Background(), user, thread.ID, scoutChatProposalAction{
		Action: "accepted", MessageID: answer.ID, Objective: "client retry must not replace accepted bytes",
	})
	if err != nil || reconciled["reconciled"] != true || starts.Load() != 1 {
		t.Fatalf("reconciled=%#v err=%v starts=%d", reconciled, err, starts.Load())
	}
	work, ok := reconciled["agentThread"].(scoutAgentThread)
	if !ok || work.ID == "" || work.Query != acceptedObjective {
		t.Fatalf("reconciled work=%#v", reconciled["agentThread"])
	}
	metadata := work.Artifact.Metadata
	if metadata["approvedProposalId"] != answer.ID || metadata["approvedEffectClass"] != "repository_mutation" || metadata["operationId"] == "" || metadata["operationBodyDigest"] == "" {
		t.Fatalf("approved work metadata=%#v", metadata)
	}
	saved := reconciled["thread"].(scoutChatThreadRecord)
	workCards := 0
	for _, message := range saved.Messages {
		if message.CausedByMessageID == answer.ID && message.Thread != nil {
			workCards++
		}
		if message.ID == answer.ID && (message.Proposal == nil || message.Proposal.Objective != acceptedObjective || message.Proposal.Status != "accepted") {
			t.Fatalf("accepted proposal bytes were not persisted: %#v", message.Proposal)
		}
	}
	if workCards != 1 {
		t.Fatalf("approved work cards=%d, want one", workCards)
	}
}

func TestConversationApprovalCannotReviveUnadmittedNativeAction(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Held native action", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("aj@shareability.com")
	proposal := scoutRouterProposal{
		Kind: scoutRouterProposalKindNativeAction, IntentOutcome: string(conversationIntentApprovalRequired),
		EffectClass: "deletion", ToolID: "delete_ticket", Objective: "Delete the Aurora card",
		Fields: map[string]string{"title": "Aurora"}, Summary: "Delete the Aurora card?",
	}
	messageID := "native-action-unadmitted-proposal"
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, scoutChatMessageRecord{
		ID: messageID, Kind: scoutChatMessageKindProposal, Role: "scout", Proposal: &proposal,
		IntentOutcome: string(conversationIntentApprovalRequired), Text: proposal.Summary,
	}); err != nil {
		t.Fatal(err)
	}
	before := len(app.snapshotState().Cards)
	_, err = app.resolveScoutChatProposal(context.Background(), user, thread.ID, scoutChatProposalAction{Action: "accepted", MessageID: messageID})
	if err == nil || !strings.Contains(err.Error(), "individually admitted") {
		t.Fatalf("native approval err=%v", err)
	}
	if got := len(app.snapshotState().Cards); got != before {
		t.Fatalf("unadmitted native approval mutated board: before=%d after=%d", before, got)
	}
	pending, err := app.pendingScoutChatProposal(thread.ID, user.Email, messageID)
	if err != nil || pending.Status != "" {
		t.Fatalf("unadmitted native proposal was claimed: pending=%#v err=%v", pending, err)
	}
}

func TestConversationApprovalRejectsStaleEffectClassBeforeClaim(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Stale approval effect", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("aj@shareability.com")
	proposal := scoutRouterProposal{
		Kind:        scoutRouterProposalKindGoalRun,
		EffectClass: "external_send", Objective: "Push the reviewed change to GitHub",
		Authority: codexJobAuthorityExternalWrite, Summary: "Push the reviewed change?",
	}
	messageID := "stale-effect-approval-proposal"
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, scoutChatMessageRecord{
		ID: messageID, Kind: scoutChatMessageKindProposal, Role: "scout", Proposal: &proposal,
		IntentOutcome: string(conversationIntentApprovalRequired), Text: proposal.Summary,
	}); err != nil {
		t.Fatal(err)
	}
	_, err = app.resolveScoutChatProposal(context.Background(), user, thread.ID, scoutChatProposalAction{Action: "accepted", MessageID: messageID})
	if err == nil || !strings.Contains(err.Error(), "effect no longer matches") {
		t.Fatalf("stale effect acceptance err=%v", err)
	}
	pending, pendingErr := app.pendingScoutChatProposal(thread.ID, user.Email, messageID)
	if pendingErr != nil || pending.Status != "" {
		t.Fatalf("stale effect proposal was claimed: pending=%#v err=%v", pending, pendingErr)
	}
}

func TestConversationLauncherRejectsNamedRegistryWorkWithoutCapabilityExpansion(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Named registry guard", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("aj@shareability.com")
	message := scoutChatMessageRecord{ID: "named-registry-source", Role: "user", Text: "Create a presentation"}
	var launches atomic.Int32
	previousGoalStarter := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) { launches.Add(1) }
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStarter })
	_, err = app.startConversationPrivateWork(context.Background(), user, thread, message, conversationWorkDecision{
		Kind: conversationWorkRegistryTool, ToolID: packagingStudioProcessID, Objective: "Create a presentation",
		AgentID: "agent-research-only", AgentName: "Research Agent",
	}, "", proposalSourceChatRouter, func(...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		return scoutChatThreadRecord{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "not admitted for this work") || launches.Load() != 0 {
		t.Fatalf("named registry guard err=%v launches=%d", err, launches.Load())
	}
}

func TestConversationIntentImageOperationReplaysWithoutSecondGeneration(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "openai-image-test")
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-router-test"
	var routerCalls atomic.Int32
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_route" {
			t.Fatalf("unexpected workflow %q", request.Workflow)
		}
		routerCalls.Add(1)
		return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
			Outcome: string(conversationIntentStartPrivateWork), Route: "image", Objective: "A warm editorial portrait", Prompt: "A warm editorial portrait",
		}), nil
	})
	previousImageStarter := startScoutChatImageAsyncWithPending
	var imageStarts atomic.Int32
	startScoutChatImageAsyncWithPending = func(*kanbanBoardApp, string, string, string, string, string) {
		imageStarts.Add(1)
	}
	t.Cleanup(func() { startScoutChatImageAsyncWithPending = previousImageStarter })
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Image replay", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("aj@shareability.com")
	ctx := withConversationTurnOperation(context.Background(), conversationTurnOperation{
		ID: "conversation-image-replay-0001", BodyDigest: sha256Hex([]byte("image canonical body")),
	})
	first, err := app.appendScoutChatThreadMessage(ctx, user, thread.ID, "Make a warm editorial portrait", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := app.appendScoutChatThreadMessage(ctx, user, thread.ID, "Make a warm editorial portrait", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if routerCalls.Load() != 1 || imageStarts.Load() != 1 || replay["replayed"] != true {
		t.Fatalf("router=%d image=%d replay=%#v", routerCalls.Load(), imageStarts.Load(), replay)
	}
	firstAnswer := first["answer"].(scoutChatMessageRecord)
	replayAnswer := replay["answer"].(scoutChatMessageRecord)
	if firstAnswer.ID != replayAnswer.ID || replayAnswer.IntentOutcome != string(conversationIntentStartPrivateWork) || replayAnswer.ImageGeneration == nil {
		t.Fatalf("first=%#v replay=%#v", firstAnswer, replayAnswer)
	}
}

func savedWorkArtifactID(response map[string]any) string {
	answer, _ := response["answer"].(scoutChatMessageRecord)
	if answer.Thread == nil {
		return ""
	}
	return answer.Thread.ArtifactID
}
