package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConversationIntentContractAcceptsExactlyFiveExclusiveShapes(t *testing.T) {
	valid := []conversationIntentDecision{
		{Outcome: conversationIntentConversationalReply},
		// clarify_once with formal choices card (Question + Options)
		{Outcome: conversationIntentClarifyOnce, Question: "Who is this for?", Options: []scoutChatChoiceOption{{ID: "opt-1", Label: "Investors"}, {ID: "opt-2", Label: "The team"}}},
		// clarify_once with prose direction pass (Message only - Approach B)
		{Outcome: conversationIntentClarifyOnce, Message: "What's the deck about, and who needs to buy into it?"},
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
		// conversational_reply cannot have a Message field
		{Outcome: conversationIntentConversationalReply, Message: "This should fail"},
		// clarify_once cannot have both choices AND prose message
		{Outcome: conversationIntentClarifyOnce, Question: "Which?", Options: []scoutChatChoiceOption{{Label: "Deck"}, {Label: "Memo"}}, Message: "Cannot have both"},
		// clarify_once options cannot have tool_id
		{Outcome: conversationIntentClarifyOnce, Question: "Which?", Options: []scoutChatChoiceOption{{Label: "Deck", ToolID: "deck_outline"}, {Label: "Memo"}}},
		// clarify_once without either choices or message is invalid
		{Outcome: conversationIntentClarifyOnce, Question: "Only a question with no options?"},
		{Outcome: conversationIntentStartPrivateWork, Work: &conversationWorkDecision{Kind: conversationWorkGoal, Objective: "Deploy it", Authority: codexJobAuthorityExternalWrite}},
		{Outcome: conversationIntentApprovalRequired, Approval: &conversationApprovalDecision{EffectClass: "deletion", Summary: "Delete?"}},
		{Outcome: conversationIntentUnavailable},
		// start_private_work cannot have a Message field
		{Outcome: conversationIntentStartPrivateWork, Message: "This should fail", Work: &conversationWorkDecision{Kind: conversationWorkWorkstream, Mode: "research", Objective: "Research the market"}},
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

func TestConversationIntentClarifyOnceProseDirectionPass(t *testing.T) {
	// Formal choices card: Question + Options
	choicesDecision, err := scoutConversationIntentFromOpenAI(openAIScoutRouterOutput{
		Outcome:  string(conversationIntentClarifyOnce),
		Question: "Who is the deck for?",
		Options: []openAIScoutRouterOption{
			{Label: "Investors", Reply: "Focus on market opportunity"},
			{Label: "The team", Reply: "Focus on execution plan"},
		},
	}, "make a deck")
	if err != nil {
		t.Fatalf("choices decision err=%v", err)
	}
	if choicesDecision.Outcome != conversationIntentClarifyOnce || choicesDecision.Question == "" || len(choicesDecision.Options) != 2 || choicesDecision.Message != "" {
		t.Fatalf("expected formal choices card, got=%+v", choicesDecision)
	}

	// Prose direction pass: Message only (Approach B)
	proseDecision, err := scoutConversationIntentFromOpenAI(openAIScoutRouterOutput{
		Outcome: string(conversationIntentClarifyOnce),
		Message: "What's the deck about, and who needs to buy into it?",
	}, "make a deck")
	if err != nil {
		t.Fatalf("prose decision err=%v", err)
	}
	if proseDecision.Outcome != conversationIntentClarifyOnce || proseDecision.Message == "" || len(proseDecision.Options) != 0 {
		t.Fatalf("expected prose direction pass, got=%+v", proseDecision)
	}

	// Verdict from prose direction pass should have directionPass, not choices
	proseVerdict, err := scoutRouterVerdictFromConversationIntent(proseDecision, "make a deck")
	if err != nil {
		t.Fatalf("prose verdict err=%v", err)
	}
	if proseVerdict == nil || proseVerdict.directionPass == "" || proseVerdict.choices != nil {
		t.Fatalf("expected directionPass verdict, got=%+v", proseVerdict)
	}

	// Verdict from choices should have choices, not directionPass
	choicesVerdict, err := scoutRouterVerdictFromConversationIntent(choicesDecision, "make a deck")
	if err != nil {
		t.Fatalf("choices verdict err=%v", err)
	}
	if choicesVerdict == nil || choicesVerdict.choices == nil || choicesVerdict.directionPass != "" {
		t.Fatalf("expected choices verdict, got=%+v", choicesVerdict)
	}
}

func TestConversationIntentPrivateToolRequestStartsWithoutProposal(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-router-test"
	previousGoalStarter := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStarter })
	previousAgentStarter := startAgentThreadAsync
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousAgentStarter })
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
			if strings.Contains(strings.ToLower(request.Input), "based on the convo tyler, tom and i are having") {
				return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
					Outcome: string(conversationIntentStartPrivateWork), Route: "workstream", Mode: "research",
					Objective: "Research the opportunity discussed by Tyler, Tom, and AJ using the available conversation and transcript analysis",
				}), nil
			}
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

func TestConversationIntentHTTPLegacyNativeMissingOperationIDIsNarrowlyCompatible(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = "openai-router-test"
	t.Cleanup(func() { kanbanApp = previousApp })
	var providerCalls atomic.Int32
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		providerCalls.Add(1)
		switch request.Workflow {
		case "scout_route":
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Outcome: string(conversationIntentConversationalReply)}), nil
		case "scout_chat":
			return "I can help with that.", nil
		default:
			t.Fatalf("unexpected workflow %q", request.Workflow)
			return "", nil
		}
	})
	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Legacy native", "")
	if err != nil {
		t.Fatal(err)
	}

	loginRequest := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"name":"AJ","password":"B0NFIRE!"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	loginRequest.Header.Set("X-Bonfire-Client", "expo")
	loginResponse := httptest.NewRecorder()
	authHandler(loginResponse, loginRequest)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("native login status=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}
	var login struct {
		SessionToken string `json:"sessionToken"`
	}
	if err := json.Unmarshal(loginResponse.Body.Bytes(), &login); err != nil || login.SessionToken == "" {
		t.Fatalf("native login payload=%s err=%v", loginResponse.Body.String(), err)
	}

	legacy := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+thread.ID+"/messages", strings.NewReader(`{"text":"Hey! What's up?"}`))
	legacy.Header.Set("Content-Type", "application/json")
	legacy.Header.Set("X-Bonfire-Client", "expo")
	legacy.Header.Set("Authorization", "Bearer "+login.SessionToken)
	legacyResponse := httptest.NewRecorder()
	assistantChatThreadHandler(legacyResponse, legacy)
	if legacyResponse.Code != http.StatusOK {
		t.Fatalf("legacy native status=%d body=%s", legacyResponse.Code, legacyResponse.Body.String())
	}
	var projected map[string]any
	if err := json.Unmarshal(legacyResponse.Body.Bytes(), &projected); err != nil {
		t.Fatal(err)
	}
	issued, _ := projected["operationId"].(string)
	if projected["legacyOperationIdIssued"] != true || !strings.HasPrefix(issued, "legacy-native-conversation-") {
		t.Fatalf("legacy response=%v, want explicit server-issued operation id", projected)
	}
	if providerCalls.Load() != 2 {
		t.Fatalf("provider calls=%d, want router plus answer", providerCalls.Load())
	}

	// A process restart and lost response cannot mint a second turn. The exact
	// authenticated session/thread/body alias is durable and reuses the prior
	// server operation; neither router nor answer provider runs again.
	kanbanApp = newKanbanBoardApp()
	kanbanApp.apiKey = "openai-router-test"
	retry := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+thread.ID+"/messages", strings.NewReader(`{"text":"Hey! What's up?"}`))
	retry.Header.Set("Content-Type", "application/json")
	retry.Header.Set("X-Bonfire-Client", "expo")
	retry.Header.Set("Authorization", "Bearer "+login.SessionToken)
	retryResponse := httptest.NewRecorder()
	assistantChatThreadHandler(retryResponse, retry)
	if retryResponse.Code != http.StatusOK {
		t.Fatalf("legacy restart retry status=%d body=%s", retryResponse.Code, retryResponse.Body.String())
	}
	var retryProjected map[string]any
	if err := json.Unmarshal(retryResponse.Body.Bytes(), &retryProjected); err != nil {
		t.Fatal(err)
	}
	if retryProjected["legacyOperationIdReused"] != true || retryProjected["operationId"] != issued || retryProjected["replayed"] != true || providerCalls.Load() != 2 {
		t.Fatalf("restart retry=%v provider calls=%d, want exact replay", retryProjected, providerCalls.Load())
	}
	if bytes.Contains(retryResponse.Body.Bytes(), []byte("legacyConversationOperations")) || bytes.Contains(retryResponse.Body.Bytes(), []byte("sessionDigest")) {
		t.Fatalf("server-only legacy alias leaked: %s", retryResponse.Body.String())
	}
	reloaded, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", thread.ID)
	if err != nil || len(reloaded.Messages) != 2 {
		t.Fatalf("reloaded messages=%d err=%v, want one user plus one answer", len(reloaded.Messages), err)
	}

	// Reserving a distinct body does not retire the last accepted alias until
	// that distinct turn itself commits. This preserves the older response-loss
	// retry when the newer request is interrupted before its conversation
	// append. Reserve the newer body directly at that exact durable seam.
	const distinctText = "A distinct turn interrupted after reservation"
	distinctBody, err := canonicalJSON(map[string]any{
		"threadId": thread.ID, "requester": "aj@shareability.com", "text": distinctText,
		"files": []scoutChatFileAttachment(nil), "followUpArtifactId": "", "replyToMessageId": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionProbe := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+thread.ID+"/messages", nil)
	sessionProbe.Header.Set("Authorization", "Bearer "+login.SessionToken)
	distinctDigest := sha256Hex(append([]byte("conversation-http-turn/v1\x00"), distinctBody...))
	distinctOperationID, reused, err := kanbanApp.reserveLegacyNativeConversationOperation("aj@shareability.com", thread.ID, strideE10SessionHashFromRequest(sessionProbe), distinctDigest)
	if err != nil || reused || distinctOperationID == "" {
		t.Fatalf("distinct reservation id=%q reused=%t err=%v", distinctOperationID, reused, err)
	}
	olderRetry := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+thread.ID+"/messages", strings.NewReader(`{"text":"Hey! What's up?"}`))
	olderRetry.Header.Set("Content-Type", "application/json")
	olderRetry.Header.Set("X-Bonfire-Client", "expo")
	olderRetry.Header.Set("Authorization", "Bearer "+login.SessionToken)
	olderRetryResponse := httptest.NewRecorder()
	assistantChatThreadHandler(olderRetryResponse, olderRetry)
	var olderProjected map[string]any
	if olderRetryResponse.Code != http.StatusOK || json.Unmarshal(olderRetryResponse.Body.Bytes(), &olderProjected) != nil || olderProjected["operationId"] != issued || olderProjected["replayed"] != true || providerCalls.Load() != 2 {
		t.Fatalf("older replay status=%d payload=%v provider calls=%d", olderRetryResponse.Code, olderProjected, providerCalls.Load())
	}

	// The distinct turn's exact retry reuses its reserved id and, only after
	// successful commit, retires the older alias. Repeating the older text after
	// that accepted turn is therefore a deliberate new message, not a replay.
	distinct := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+thread.ID+"/messages", strings.NewReader(`{"text":"A distinct turn interrupted after reservation"}`))
	distinct.Header.Set("Content-Type", "application/json")
	distinct.Header.Set("X-Bonfire-Client", "expo")
	distinct.Header.Set("Authorization", "Bearer "+login.SessionToken)
	distinctResponse := httptest.NewRecorder()
	assistantChatThreadHandler(distinctResponse, distinct)
	var distinctProjected map[string]any
	if distinctResponse.Code != http.StatusOK || json.Unmarshal(distinctResponse.Body.Bytes(), &distinctProjected) != nil || distinctProjected["legacyOperationIdReused"] != true || distinctProjected["operationId"] != distinctOperationID || providerCalls.Load() != 4 {
		t.Fatalf("distinct retry status=%d payload=%v provider calls=%d", distinctResponse.Code, distinctProjected, providerCalls.Load())
	}
	repeatedAfterAccepted := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+thread.ID+"/messages", strings.NewReader(`{"text":"Hey! What's up?"}`))
	repeatedAfterAccepted.Header.Set("Content-Type", "application/json")
	repeatedAfterAccepted.Header.Set("X-Bonfire-Client", "expo")
	repeatedAfterAccepted.Header.Set("Authorization", "Bearer "+login.SessionToken)
	repeatedAfterAcceptedResponse := httptest.NewRecorder()
	assistantChatThreadHandler(repeatedAfterAcceptedResponse, repeatedAfterAccepted)
	var repeatedProjected map[string]any
	if repeatedAfterAcceptedResponse.Code != http.StatusOK || json.Unmarshal(repeatedAfterAcceptedResponse.Body.Bytes(), &repeatedProjected) != nil || repeatedProjected["legacyOperationIdReused"] == true || repeatedProjected["operationId"] == issued || providerCalls.Load() != 6 {
		t.Fatalf("intentional repeat status=%d payload=%v provider calls=%d", repeatedAfterAcceptedResponse.Code, repeatedProjected, providerCalls.Load())
	}

	// The exact failed Build 53 shape from the live report now reaches the
	// conversation router. In a public channel it is held as the existing
	// explicit Scout proposal, with the transcript-backed research objective
	// intact, instead of dying at transport validation.
	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Hey! What's Up?", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	research := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+channel.ID+"/messages", strings.NewReader(`{"text":"@Scout based on the convo Tyler, Tom and I are having can you run a research report? dig into the transcript analysis to scope it out if needed"}`))
	research.Header.Set("Content-Type", "application/json")
	research.Header.Set("X-Bonfire-Client", "expo")
	research.Header.Set("Authorization", "Bearer "+login.SessionToken)
	researchResponse := httptest.NewRecorder()
	assistantChatThreadHandler(researchResponse, research)
	if researchResponse.Code != http.StatusOK {
		t.Fatalf("legacy native research status=%d body=%s", researchResponse.Code, researchResponse.Body.String())
	}
	var researchProjected map[string]any
	if err := json.Unmarshal(researchResponse.Body.Bytes(), &researchProjected); err != nil {
		t.Fatal(err)
	}
	proposal, _ := researchProjected["proposal"].(map[string]any)
	if researchProjected["legacyOperationIdIssued"] != true || proposal == nil || !strings.Contains(strings.ToLower(asString(proposal["objective"])), "transcript") {
		t.Fatalf("legacy native research response=%v, want public proposal with transcript-grounded objective", researchProjected)
	}

	// A browser cookie cannot use the compatibility door.
	web := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+thread.ID+"/messages", strings.NewReader(`{"text":"browser missing id"}`))
	web.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		web.AddCookie(cookie)
	}
	webResponse := httptest.NewRecorder()
	assistantChatThreadHandler(webResponse, web)
	if webResponse.Code != http.StatusBadRequest {
		t.Fatalf("web missing operation id status=%d body=%s", webResponse.Code, webResponse.Body.String())
	}

	// Nor can a modern native request replace a malformed supplied retry key
	// with a fresh server id; only the exactly-missing legacy field is bridged.
	malformed := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+thread.ID+"/messages", strings.NewReader("{\"text\":\"bad id\",\"operationId\":\"bad\\nkey\"}"))
	malformed.Header.Set("Content-Type", "application/json")
	malformed.Header.Set("X-Bonfire-Client", "expo")
	malformed.Header.Set("Authorization", "Bearer "+login.SessionToken)
	malformedResponse := httptest.NewRecorder()
	assistantChatThreadHandler(malformedResponse, malformed)
	if malformedResponse.Code != http.StatusBadRequest {
		t.Fatalf("malformed native operation id status=%d body=%s", malformedResponse.Code, malformedResponse.Body.String())
	}
}

func TestLegacyNativeConversationPendingAliasesSurviveConcurrentAcceptanceAndCapacity(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Legacy concurrent aliases", "")
	if err != nil {
		t.Fatal(err)
	}
	sessionDigest := sha256Hex([]byte("legacy-native-session"))
	bodyA := sha256Hex([]byte("body-a"))
	bodyB := sha256Hex([]byte("body-b"))
	opA, reused, err := app.reserveLegacyNativeConversationOperation("aj@shareability.com", thread.ID, sessionDigest, bodyA)
	if err != nil || reused {
		t.Fatalf("reserve A id=%q reused=%t err=%v", opA, reused, err)
	}
	opB, reused, err := app.reserveLegacyNativeConversationOperation("aj@shareability.com", thread.ID, sessionDigest, bodyB)
	if err != nil || reused {
		t.Fatalf("reserve B id=%q reused=%t err=%v", opB, reused, err)
	}
	if err := app.retireLegacyNativeConversationOperations("aj@shareability.com", thread.ID, sessionDigest, opA); err != nil {
		t.Fatalf("accept A: %v", err)
	}
	if replayB, reused, err := app.reserveLegacyNativeConversationOperation("aj@shareability.com", thread.ID, sessionDigest, bodyB); err != nil || !reused || replayB != opB {
		t.Fatalf("A acceptance destroyed pending B: id=%q reused=%t err=%v", replayB, reused, err)
	}
	if err := app.retireLegacyNativeConversationOperations("aj@shareability.com", thread.ID, sessionDigest, opB); err != nil {
		t.Fatalf("accept B after A: %v", err)
	}
	reloaded, _, err := app.scoutChatThreadByID("aj@shareability.com", thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.LegacyConversationOperations) != 1 || reloaded.LegacyConversationOperations[0].OperationID != opB || strings.TrimSpace(reloaded.LegacyConversationOperations[0].AcceptedAt) == "" {
		t.Fatalf("aliases=%+v, want only accepted B after its later commit", reloaded.LegacyConversationOperations)
	}

	capacityThread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Legacy capacity", "")
	if err != nil {
		t.Fatal(err)
	}
	firstBody := sha256Hex([]byte("pending-0"))
	firstOperation := ""
	for index := 0; index < maxLegacyConversationOperationAliases; index++ {
		bodyDigest := sha256Hex([]byte(fmt.Sprintf("pending-%d", index)))
		op, reused, reserveErr := app.reserveLegacyNativeConversationOperation("aj@shareability.com", capacityThread.ID, sessionDigest, bodyDigest)
		if reserveErr != nil || reused {
			t.Fatalf("capacity reserve %d id=%q reused=%t err=%v", index, op, reused, reserveErr)
		}
		if index == 0 {
			firstOperation = op
		}
	}
	if _, _, err := app.reserveLegacyNativeConversationOperation("aj@shareability.com", capacityThread.ID, sessionDigest, sha256Hex([]byte("overflow"))); !errors.Is(err, ErrSTRIDEConversationConflict) {
		t.Fatalf("overflow err=%v, want fail-closed conflict", err)
	}
	if replay, reused, err := app.reserveLegacyNativeConversationOperation("aj@shareability.com", capacityThread.ID, sessionDigest, firstBody); err != nil || !reused || replay != firstOperation {
		t.Fatalf("capacity evicted pending authority: id=%q reused=%t err=%v", replay, reused, err)
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

func TestPrivateRealtimeVoiceRoutesTodaysMeetingsToAuthorizedBriefingWithoutClarifying(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Voice meeting recall", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(meetingTimeLocation())
	spanStart := time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, now.Location())
	spanEnd := spanStart.Add(45 * time.Minute)
	digest := `{"meetingId":"meeting-today","title":"Launch review","day":"` + spanStart.Format("2006-01-02") + `",` +
		`"topics":[{"t":"Launch readiness","importance":4}],` +
		`"decisions":[{"d":"Keep the Friday launch with a rollback owner assigned","status":"decided","importance":5}],` +
		`"openQuestions":[{"q":"Who verifies the rollback receipt?","importance":3}]}`
	upsertBriefingTestDigest(t, app, "meeting-today", digest, spanStart.Format("2006-01-02"), spanStart.UTC().Format(time.RFC3339), spanEnd.UTC().Format(time.RFC3339))

	providerCalls := 0
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		providerCalls++
		t.Fatalf("meeting briefing reached generic provider workflow %q", request.Workflow)
		return "", nil
	})

	utterance := "What, um, can you catch me up on the meetings that happened today, anything interesting come up?"
	first, changed, err := app.applyPrivateRealtimeVoiceModelTool(context.Background(), "aj@shareability.com", "call_voice_meetings_today", "route_conversation_turn", map[string]any{"utterance": utterance})
	if err != nil || changed {
		t.Fatalf("first=%v changed=%v err=%v", first, changed, err)
	}
	message := asString(first["message"])
	if first["outcome"] != string(conversationIntentConversationalReply) || !strings.Contains(message, "Keep the Friday launch") || !strings.Contains(message, "Launch review") {
		t.Fatalf("briefing result=%v", first)
	}
	if providerCalls != 0 {
		t.Fatalf("generic provider calls=%d, want 0", providerCalls)
	}

	replay, changed, err := app.applyPrivateRealtimeVoiceModelTool(context.Background(), "aj@shareability.com", "call_voice_meetings_today", "route_conversation_turn", map[string]any{"utterance": utterance})
	if err != nil || changed || replay["message"] != first["message"] || providerCalls != 0 {
		t.Fatalf("replay=%v changed=%v providerCalls=%d err=%v", replay, changed, providerCalls, err)
	}
	saved, _, err := app.scoutChatThreadByID("aj@shareability.com", thread.ID)
	if err != nil || len(saved.Messages) != 2 {
		t.Fatalf("saved messages=%#v err=%v", saved.Messages, err)
	}
}

func TestConversationMeetingBriefingIntentRequiresMeetingAndExactTimeRange(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.FixedZone("PDT", -7*60*60))
	for _, utterance := range []string{
		"Catch me up on Aurora today",
		"Catch me up on the meetings",
		"Schedule a meeting today",
		"What happened yesterday in the project thread?",
	} {
		if got, ok := conversationMeetingBriefingRange(utterance, now); ok {
			t.Fatalf("utterance %q matched as %q", utterance, got)
		}
	}
	if got, ok := conversationMeetingBriefingRange("Summarize yesterday's calls", now); !ok || got == "" {
		t.Fatalf("explicit meeting range was not recognized: got=%q ok=%v", got, ok)
	}
}

func seedMeetingAgentContextRecord(t *testing.T, app *kanbanBoardApp, meetingID, title, decision string, started time.Time) {
	t.Helper()
	app.meetings.startMeeting(officeRoomID, meetingID, started, []string{"AJ", "Tim"})
	app.meetings.endMeeting(meetingID, started.Add(8*time.Minute), meetingEndedReasonIdle, "")
	segment, _, err := app.memory.appendAttributedTranscriptWithMetadata(
		meetingID+"-segment", meetingID+"-item", "AJ", "high", decision,
		map[string]string{"meetingId": meetingID, "visibility": "organization", "roomId": officeRoomID},
	)
	if err != nil {
		t.Fatalf("append %s transcript: %v", meetingID, err)
	}
	payload := meetingDigestPayload{
		MeetingID: meetingID, Title: title, Day: dayBucket(started),
		Decisions: []meetingDigestDecision{{D: decision, By: "AJ", Status: "decided", Anchor: segment.ID, Importance: 5}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s digest: %v", meetingID, err)
	}
	segments := meetingRecordSegments(app.memory.snapshotForMeeting(meetingID, 0), meetingID)
	if _, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, meetingID, string(body), map[string]string{
		"meetingId": meetingID, "visibility": "organization", digestCoverageMetadataKey: coverageLabelFull,
		digestSpanStartMetadataKey:                    started.UTC().Format(time.RFC3339),
		digestSpanEndMetadataKey:                      started.Add(8 * time.Minute).UTC().Format(time.RFC3339),
		meetingRecordDigestSourceRevisionsMetadataKey: meetingRecordDigestSourceRevisionMetadata(payload, segments),
	}); err != nil {
		t.Fatalf("upsert %s digest: %v", meetingID, err)
	}
}

func TestMeetingRangeContextBindsExactAuthorizedRecordsAndFailsClosedOnRevisionChange(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	now := time.Now().In(meetingTimeLocation())
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	started := now.Add(-40 * time.Minute)
	if started.Before(dayStart) {
		started = dayStart.Add(time.Minute)
	}
	seedMeetingAgentContextRecord(t, app, "meeting-agent-range-1", "Launch review", "Keep the Friday launch and assign a rollback owner.", started)
	seedMeetingAgentContextRecord(t, app, "meeting-agent-range-2", "Partner review", "Advance the partner pilot with a two-week checkpoint.", started.Add(12*time.Minute))

	aj := accountStore().findUser("aj@shareability.com")
	if aj == nil {
		t.Fatal("seeded AJ account missing")
	}
	principal := recallPrincipalForUser(aj)
	ref, recognized, err := app.meetingRangeContextRefForPrincipal(context.Background(), principal, "Analyze today's meetings and prepare a report", now)
	if err != nil || !recognized || ref == "" {
		t.Fatalf("ref=%q recognized=%v err=%v", ref, recognized, err)
	}
	claims, ok := resolveMeetingRangeContextRef(ref)
	if !ok || len(claims.Records) != 2 || claims.Records[0].MeetingID != "meeting-agent-range-1" || claims.Records[1].MeetingID != "meeting-agent-range-2" {
		t.Fatalf("claims=%+v ok=%v, want two chronological exact records", claims, ok)
	}
	entry, readable := app.assistantContextEntryForRef(context.Background(), principal, ref)
	if !readable || !strings.Contains(entry.Text, "Keep the Friday launch") || !strings.Contains(entry.Text, "Advance the partner pilot") || len(entry.Text) > meetingRangeContextTextCap {
		t.Fatalf("entry readable=%v bytes=%d text=%s", readable, len(entry.Text), entry.Text)
	}
	tim := accountStore().findUser("tim@shareability.com")
	if tim == nil {
		t.Fatal("seeded Tim account missing")
	}
	if _, readable := app.assistantContextEntryForRef(context.Background(), recallPrincipalForUser(tim), ref); readable {
		t.Fatal("another principal reused the owner-bound meeting range manifest")
	}
	dot := strings.LastIndex(ref, ".")
	if dot < 0 || dot+1 >= len(ref) {
		t.Fatalf("meeting range ref omitted signature: %q", ref)
	}
	replacement := byte('A')
	if ref[dot+1] == replacement {
		replacement = 'B'
	}
	tampered := ref[:dot+1] + string(replacement) + ref[dot+2:]
	if _, readable := app.assistantContextEntryForRef(context.Background(), principal, tampered); readable {
		t.Fatal("tampered meeting range manifest remained readable")
	}

	seedMeetingAgentContextRecord(t, app, "meeting-agent-range-2", "Partner review", "Do not advance the partner pilot yet.", started.Add(12*time.Minute))
	if _, readable := app.assistantContextEntryForRef(context.Background(), principal, ref); readable {
		t.Fatal("changed Meeting Record revision remained readable through the old manifest")
	}
}

func TestMeetingRangeContextReportsStaleAnalysisGapAndFallsBackToCurrentTranscript(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	now := time.Now().In(meetingTimeLocation())
	started := now.Add(-20 * time.Minute)
	if dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()); started.Before(dayStart) {
		started = dayStart.Add(time.Minute)
	}
	seedMeetingAgentContextRecord(t, app, "meeting-analysis-gap", "Launch review", "Keep the launch on Friday.", started)
	user := accountStore().findUser("aj@shareability.com")
	principal := recallPrincipalForUser(user)
	ref, recognized, err := app.meetingRangeContextRefForPrincipal(context.Background(), principal, "Analyze today's meetings", now)
	if err != nil || !recognized || ref == "" {
		t.Fatalf("ref=%q recognized=%v err=%v", ref, recognized, err)
	}
	claims, ok := resolveMeetingRangeContextRef(ref)
	if !ok || len(claims.Records) != 1 {
		t.Fatalf("claims=%+v ok=%v", claims, ok)
	}
	entries := app.memory.snapshotForMeeting("meeting-analysis-gap", 0)
	segments := meetingRecordSegments(entries, "meeting-analysis-gap")
	if len(segments) != 1 {
		t.Fatalf("segments=%+v", segments)
	}
	if _, appended, appendErr := app.memory.appendAttributedTranscriptWithMetadata(
		"meeting-analysis-gap-segment-v2", "meeting-analysis-gap-item-v2", "AJ", "high", "Move the launch to Monday.",
		map[string]string{"meetingId": "meeting-analysis-gap", "visibility": "organization", "roomId": officeRoomID, "supersedesId": segments[0].ID},
	); appendErr != nil || !appended {
		t.Fatalf("append correction: appended=%v err=%v", appended, appendErr)
	}
	projection, readable := app.meetingRecordProjectionForPrincipal(context.Background(), principal, "meeting-analysis-gap")
	if !readable || projection == nil {
		t.Fatal("corrected meeting projection unavailable")
	}
	claims.Records[0].RecordRevision = projection.index.RecordRevision
	ref, err = mintMeetingRangeContextRef(claims)
	if err != nil {
		t.Fatal(err)
	}
	entry, readable := app.assistantContextEntryForRef(context.Background(), principal, ref)
	if !readable || !strings.Contains(entry.Text, "analysis_gap=1") || !strings.Contains(entry.Text, "Move the launch to Monday") || strings.Contains(entry.Text, "decision: Keep the launch on Friday") {
		t.Fatalf("stale-analysis projection readable=%v text=%s", readable, entry.Text)
	}
}

func TestPrivateAgentWorkCarriesMeetingRangeIntoCurrentSourceProviderContext(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-meeting-agent-context-test"
	previousGoalStarter := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStarter })
	previousAgentStarter := startAgentThreadAsync
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousAgentStarter })
	now := time.Now().In(meetingTimeLocation())
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	started := now.Add(-35 * time.Minute)
	if started.Before(dayStart) {
		started = dayStart.Add(time.Minute)
	}
	seedMeetingAgentContextRecord(t, app, "meeting-agent-work-1", "Customer launch", "Ship the customer launch on Friday.", started)
	seedMeetingAgentContextRecord(t, app, "meeting-agent-work-2", "Operations review", "Assign Tim to verify the rollback receipt.", started.Add(12*time.Minute))

	routerCalls := 0
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_route" {
			t.Fatalf("meeting-backed work reached unexpected workflow %q", request.Workflow)
		}
		routerCalls++
		return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
			Outcome: string(conversationIntentStartPrivateWork), Route: "workstream", Mode: "research",
			Objective: "Analyze today's meetings and prepare a report",
		}), nil
	})
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Meeting agent work", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("aj@shareability.com")
	response, err := app.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "Analyze today's meetings and prepare a report", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	launched, ok := response["agentThread"].(scoutAgentThread)
	if !ok || launched.ID == "" {
		t.Fatalf("response=%+v, want launched source-bound agent work", response)
	}
	refs := decodeAssistantContextRefs(launched.Artifact.Metadata["contextRefs"])
	if len(refs) != 1 || !strings.HasPrefix(refs[0], meetingRangeContextRefPrefix+"|") {
		t.Fatalf("contextRefs=%v, want one server-signed meeting range manifest", refs)
	}
	providerContext, err := app.agentThreadProviderContext(context.Background(), launched)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range providerContext.Memory {
		if entry.Metadata["meetingRangeContext"] == "true" {
			found = strings.Contains(entry.Text, "Ship the customer launch") && strings.Contains(entry.Text, "verify the rollback receipt")
		}
	}
	if !found || routerCalls != 1 {
		t.Fatalf("provider context omitted meeting range or routerCalls=%d: %+v", routerCalls, providerContext.Memory)
	}
}

func TestTypedScoutInlineMeetingAnalysisUsesBoundedCurrentRecordContext(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-inline-meeting-context-test"
	now := time.Now().In(meetingTimeLocation())
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	started := now.Add(-20 * time.Minute)
	if started.Before(dayStart) {
		started = dayStart.Add(time.Minute)
	}
	seedMeetingAgentContextRecord(t, app, "meeting-inline-analysis", "Launch risk review", "Keep a named rollback owner before Friday's launch.", started)

	providerCalls := 0
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		providerCalls++
		if request.Workflow != "scout_chat" || !strings.Contains(request.Input, "Exact authorized Meeting Record range") || !strings.Contains(request.Input, "named rollback owner") {
			t.Fatalf("inline meeting analysis input=%q workflow=%q", request.Input, request.Workflow)
		}
		return "The key cross-meeting risk is that Friday's launch still needs a named rollback owner.", nil
	})
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Inline meeting analysis", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	response, err := app.appendScoutChatThreadMessage(context.Background(), accountStore().findUser("aj@shareability.com"), thread.ID, "Analyze today's meetings for the biggest risk", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	answer, _ := response["answer"].(scoutChatMessageRecord)
	if providerCalls != 1 || !strings.Contains(answer.Text, "rollback owner") {
		t.Fatalf("providerCalls=%d answer=%+v", providerCalls, answer)
	}
}

func TestTypedPrivateScoutUsesSameAuthorizedMeetingBriefingAsVoice(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Typed meeting recall", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("test user missing")
	}
	now := time.Now().In(meetingTimeLocation())
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	spanStart := now.Add(-time.Hour)
	if spanStart.Before(dayStart) {
		spanStart = dayStart
	}
	digest := `{"meetingId":"typed-meeting-today","title":"Partner review","day":"` + now.Format("2006-01-02") + `",` +
		`"decisions":[{"d":"Advance the partner pilot with a two-week checkpoint","status":"decided","importance":5}]}`
	upsertBriefingTestDigest(t, app, "typed-meeting-today", digest, now.Format("2006-01-02"), spanStart.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))

	providerCalls := 0
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		providerCalls++
		t.Fatalf("typed meeting briefing reached generic provider workflow %q", request.Workflow)
		return "", nil
	})
	response, err := app.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "Catch me up on today's meetings", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	answer, _ := response["answer"].(scoutChatMessageRecord)
	if !strings.Contains(answer.Text, "Advance the partner pilot") || providerCalls != 0 {
		t.Fatalf("answer=%#v providerCalls=%d", answer, providerCalls)
	}
}

func TestMeetingBriefingParityAcrossTypedVoiceAndNamedAgent(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := fixture.app
	now := time.Now().In(meetingTimeLocation())
	spanStart := time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, now.Location())
	spanEnd := spanStart.Add(45 * time.Minute)
	digest := `{"meetingId":"meeting-parity-today","title":"Launch parity review","day":"` + spanStart.Format("2006-01-02") + `",` +
		`"decisions":[{"d":"Keep Friday's launch and assign one rollback owner","status":"decided","importance":5}],` +
		`"openQuestions":[{"q":"Who verifies the rollback receipt?","importance":3}]}`
	upsertBriefingTestDigest(t, app, "meeting-parity-today", digest, spanStart.Format("2006-01-02"), spanStart.UTC().Format(time.RFC3339), spanEnd.UTC().Format(time.RFC3339))

	providerCalls := 0
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		providerCalls++
		t.Fatalf("exact meeting briefing reached generic provider workflow %q", request.Workflow)
		return "", nil
	})

	typedThread, err := app.createScoutChatThread(fixture.user.Email, fixture.user.Name, "Typed meeting parity", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	voiceSessionID := "voice-meeting-parity"
	voiceThread, _, err := app.ensurePrivateRealtimeVoiceConversation(fixture.user.Email, fixture.user.Name, voiceSessionID)
	if err != nil {
		t.Fatal(err)
	}
	directThreadID := strideProductAgentDirectThreadPrefix + "colton_meeting_parity"
	_ = hireResearchAgentForBridgeTest(t, fixture, "colton-research", directThreadID)
	directThread, _, err := app.ensureScoutChatThread(directThreadID, fixture.user.Email, fixture.user.Name, "Colton · agent", scoutChatVisibilityPrivate, nil)
	if err != nil {
		t.Fatal(err)
	}

	utterance := "Catch me up on today's meetings. What was important?"
	typedResponse, err := app.appendScoutChatThreadMessage(context.Background(), fixture.user, typedThread.ID, utterance, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	voiceResult, changed, err := app.applyPrivateRealtimeVoiceSessionModelTool(context.Background(), fixture.user.Email, voiceSessionID, voiceThread.ID, "meeting-parity-voice-call", "route_conversation_turn", map[string]any{"utterance": utterance})
	if err != nil || changed {
		t.Fatalf("voice result=%v changed=%v err=%v", voiceResult, changed, err)
	}
	directResponse, err := app.appendScoutChatThreadMessage(context.Background(), fixture.user, directThread.ID, utterance, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	typedAnswer, typedOK := typedResponse["answer"].(scoutChatMessageRecord)
	directAnswer, directOK := directResponse["answer"].(scoutChatMessageRecord)
	voiceAnswer := strings.TrimSpace(asString(voiceResult["message"]))
	if !typedOK || !directOK || typedAnswer.Text == "" || typedAnswer.Text != voiceAnswer || directAnswer.Text != typedAnswer.Text {
		t.Fatalf("briefing parity failed typed=%+v voice=%q direct=%+v", typedAnswer, voiceAnswer, directAnswer)
	}
	for _, required := range []string{"Launch parity review", "Keep Friday's launch", "Who verifies the rollback receipt?"} {
		if !strings.Contains(typedAnswer.Text, required) {
			t.Fatalf("shared briefing omitted %q: %s", required, typedAnswer.Text)
		}
	}
	if typedAnswer.AuthorName != scoutParticipantName || directAnswer.AuthorName != "Colton" {
		t.Fatalf("worker attribution typed=%q direct=%q", typedAnswer.AuthorName, directAnswer.AuthorName)
	}
	typedBriefing, typedBriefingOK := typedResponse["meetingBriefing"].(map[string]any)
	directBriefing, directBriefingOK := directResponse["meetingBriefing"].(map[string]any)
	if !typedBriefingOK || !directBriefingOK || typedBriefing["source"] != directBriefing["source"] || typedBriefing["coverage"] != directBriefing["coverage"] {
		t.Fatalf("structured briefing parity typed=%v direct=%v", typedBriefing, directBriefing)
	}
	if providerCalls != 0 || voiceResult["outcome"] != string(conversationIntentConversationalReply) {
		t.Fatalf("providerCalls=%d voice=%v", providerCalls, voiceResult)
	}
}

func TestPrivateRealtimeVoiceFallsBackToAuthorizedTranscriptWhenDigestLags(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-meeting-map-test"
	if _, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Voice transcript fallback", scoutChatVisibilityPrivate); err != nil {
		t.Fatal(err)
	}
	appendTestTranscript(t, app, "voice-fallback-tx-1", "The team decided to keep Friday's customer launch and assigned AJ as rollback owner.")
	appendTestTranscript(t, app, "voice-fallback-tx-2", "The open question is whether the partner checklist is complete.")

	mapCalls := 0
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Instructions != meetingDigestInstructions() {
			t.Fatalf("raw fallback reached unrelated workflow %q", request.Workflow)
		}
		mapCalls++
		if !strings.Contains(request.Input, "Friday's customer launch") {
			t.Fatalf("authorized transcript was absent from bounded map input: %s", request.Input)
		}
		return `{"meetingId":"ignored-model-id","title":"Customer launch review","day":"1999-01-01",` +
			`"decisions":[{"d":"Keep Friday's customer launch","by":"attributed to AJ","status":"decided","importance":5}],` +
			`"openQuestions":[{"q":"Is the partner checklist complete?","importance":3}]}`, nil
	})

	result, changed, err := app.applyPrivateRealtimeVoiceModelTool(context.Background(), "aj@shareability.com", "call_voice_transcript_fallback", "route_conversation_turn", map[string]any{
		"utterance": "Catch me up on today's meetings. What was important?",
	})
	if err != nil || changed {
		t.Fatalf("result=%v changed=%v err=%v", result, changed, err)
	}
	if message := asString(result["message"]); !strings.Contains(message, "Keep Friday's customer launch") || !strings.Contains(message, "Composed on demand") {
		t.Fatalf("raw transcript briefing=%v", result)
	}
	if mapCalls != 1 {
		t.Fatalf("map calls=%d, want exactly 1", mapCalls)
	}

	replay, changed, err := app.applyPrivateRealtimeVoiceModelTool(context.Background(), "aj@shareability.com", "call_voice_transcript_fallback", "route_conversation_turn", map[string]any{
		"utterance": "Catch me up on today's meetings. What was important?",
	})
	if err != nil || changed || replay["message"] != result["message"] || mapCalls != 1 {
		t.Fatalf("replay=%v changed=%v mapCalls=%d err=%v", replay, changed, mapCalls, err)
	}
}

func TestPrincipalMeetingBriefingVisitsOnlyRequestedWindowDespiteLargeUnrelatedLedger(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	now := time.Now().In(meetingTimeLocation())
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	spanStart := now.Add(-time.Hour)
	if spanStart.Before(dayStart) {
		spanStart = dayStart
	}
	app.memory.mu.Lock()
	for index := 0; index < 1000; index++ {
		app.memory.entries = append(app.memory.entries, meetingMemoryEntry{
			ID: fmt.Sprintf("unrelated-recall-%04d", index), Kind: meetingMemoryKindNote,
			Text: "unrelated long-lived memory", CreatedAt: now.Add(-time.Duration(index+1) * time.Hour),
			Metadata: map[string]string{"visibility": "organization"},
		})
	}
	app.memory.rebuildMeetingEntryIndexesLocked()
	app.memory.mu.Unlock()
	digest := `{"meetingId":"bounded-briefing","title":"Daily operations","day":"` + now.Format(dayBucketLayout) + `",` +
		`"decisions":[{"d":"Keep the rollout checkpoint on Friday","status":"decided","importance":5}]}`
	upsertBriefingTestDigest(t, app, "bounded-briefing", digest, now.Format(dayBucketLayout), spanStart.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))

	visits := 0
	app.memory.authorizationEntryVisitHook = func() { visits++ }
	principal, ok := authenticatedRecallPrincipal("aj@shareability.com")
	if !ok {
		t.Fatal("missing authenticated principal")
	}
	result, changed, err := app.crossMeetingBriefingToolForPrincipal(map[string]any{"range": "today"}, principal)
	if err != nil || changed || !strings.Contains(asString(result["briefing"]), "Keep the rollout checkpoint") {
		t.Fatalf("result=%v changed=%v err=%v", result, changed, err)
	}
	if visits != 2 {
		t.Fatalf("principal briefing inspected %d durable rows, want only the exact digest and its current source despite 1000 unrelated rows", visits)
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
	if routerCalls.Load() != 0 || starts.Load() != 1 || replay["replayed"] != true {
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
