package main

// The conversational intent layer's server contract: the router's offer_choices
// turn persists a Kind="choices" quick-reply card, a pill tap resolves through
// the stored record only (tool pills ARM a proposal — the propose-confirm law —
// and plain pills answer as Tier 0), the founder's four scenario phrasings
// route to their intended proposals, and keyless deploys stay plain Q&A with
// zero pills and zero errors. See docs/plans/conversational-intents.md.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The choices card survives the persistence round trip: options, the tool arm,
// the originating query, and (after a tap) the answered status + selection.
func TestScoutChatChoicesMessageRoundTrip(t *testing.T) {
	thread := scoutChatThreadRecord{
		ID:         "scout-chat-rt",
		Title:      "Scout",
		OwnerEmail: "aj@shareability.com",
		CreatedAt:  "2026-07-06T00:00:00Z",
		UpdatedAt:  "2026-07-06T00:00:00Z",
		Messages: []scoutChatMessageRecord{{
			ID:        "scout-chat-message-1",
			Kind:      scoutChatMessageKindChoices,
			Role:      "scout",
			Text:      "outline work, or the built deck?",
			CreatedAt: "2026-07-06T00:00:00Z",
			Choices: &scoutChatChoices{
				Question: "outline work, or the built deck?",
				Query:    "work on the deck",
				Options: []scoutChatChoiceOption{
					{ID: "opt-1", Label: "tighten the outline", ToolID: "deck_outline"},
					{ID: "opt-2", Label: "full packaging run", Reply: "run the full packaging build", ToolID: "packaging_studio"},
					{ID: "opt-3", Label: "just talk it through"},
				},
				Status:     "answered",
				SelectedID: "opt-2",
			},
		}},
	}
	encoded, err := encodeScoutChatThread(thread)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, ok := decodeScoutChatThreadEntry(meetingMemoryEntry{
		ID:       thread.ID,
		Kind:     meetingMemoryKindScoutChat,
		Text:     encoded,
		Metadata: scoutChatThreadMetadata(thread),
	})
	if !ok {
		t.Fatal("decode round trip failed")
	}
	if len(decoded.Messages) != 1 || decoded.Messages[0].Kind != scoutChatMessageKindChoices {
		t.Fatalf("messages=%#v, want the one choices message", decoded.Messages)
	}
	choices := decoded.Messages[0].Choices
	if choices == nil {
		t.Fatal("choices data lost in the round trip")
	}
	if choices.Question != "outline work, or the built deck?" || choices.Query != "work on the deck" {
		t.Fatalf("choices=%#v, want question + query preserved", choices)
	}
	if len(choices.Options) != 3 || choices.Options[1].ToolID != "packaging_studio" || choices.Options[1].Reply != "run the full packaging build" || choices.Options[2].ToolID != "" {
		t.Fatalf("options=%#v, want labels/replies/tool arms preserved", choices.Options)
	}
	if choices.Status != "answered" || choices.SelectedID != "opt-2" {
		t.Fatalf("status=%q selected=%q, want the resolution preserved", choices.Status, choices.SelectedID)
	}
}

// An offer_choices routing turn persists a quick-reply card and launches
// NOTHING: a hallucinated tool_id degrades that pill to a plain reply, blank
// labels drop, and the card caps at 4 options.
func TestScoutChatRouterOffersChoicesNeverLaunches(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-router-test")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {
		t.Fatal("a choices card must never launch an agent thread")
	}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	swapAnthropicTextResponder(t, func(context.Context, string, anthropicTextRequest) (string, error) {
		t.Fatal("a choices turn must not also run the Q&A path")
		return "", nil
	})

	swapAnthropicMessagesResponder(t, func(_ context.Context, _ string, _ anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		return anthropicMessagesResponse{
			StopReason: "tool_use",
			Content: []json.RawMessage{
				mockAnthropicToolUseBlock("toolu_choices", "offer_choices", map[string]any{
					"question": "do you want outline work, or the deck built end to end?",
					"options": []map[string]any{
						{"label": "tighten the outline", "tool_id": "deck_outline"},
						{"label": "full packaging run", "reply": "run the full packaging build from the outline", "tool_id": "packaging_studio"},
						{"label": "phantom pill", "tool_id": "not_a_tool"},
						{"label": ""},
						{"label": "option five"},
						{"label": "option six"},
					},
				}),
			},
		}, nil
	})

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}

	text := "we need to work on the deck"
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, private.ID, text, nil, "")
	if err != nil {
		t.Fatalf("append routed message: %v", err)
	}
	if _, launched := response["agentThread"]; launched {
		t.Fatalf("response keys=%v — NEVER silent-launch", responseKeys(response))
	}
	choices, ok := response["choices"].(*scoutChatChoices)
	if !ok {
		t.Fatalf("choices type=%T, want *scoutChatChoices", response["choices"])
	}
	if choices.Question != "do you want outline work, or the deck built end to end?" || choices.Query != text {
		t.Fatalf("choices=%#v, want the question + the originating query", choices)
	}
	if len(choices.Options) != 4 {
		t.Fatalf("options=%#v, want blank labels dropped and the card capped at 4", choices.Options)
	}
	if choices.Options[0].ToolID != "deck_outline" || choices.Options[1].ToolID != "packaging_studio" {
		t.Fatalf("options=%#v, want the registry tool arms preserved", choices.Options)
	}
	if choices.Options[1].Reply != "run the full packaging build from the outline" {
		t.Fatalf("options[1]=%#v, want the crafted reply preserved", choices.Options[1])
	}
	if choices.Options[2].Label != "phantom pill" || choices.Options[2].ToolID != "" {
		t.Fatalf("options[2]=%#v — an unknown tool_id must degrade to a plain reply pill", choices.Options[2])
	}

	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || answer.Kind != scoutChatMessageKindChoices || answer.Choices == nil {
		t.Fatalf("answer=%#v, want a persisted Kind=choices message", response["answer"])
	}
	saved := response["thread"].(scoutChatThreadRecord)
	if len(saved.Messages) != 2 || saved.Messages[1].Choices == nil {
		t.Fatalf("persisted messages=%#v, want user turn + choices card", saved.Messages)
	}
}

// seedChoicesThread commits one pending choices card into a fresh private
// thread and returns the thread and card message ids.
func seedChoicesThread(t *testing.T, choices *scoutChatChoices) (string, string) {
	t.Helper()
	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	message := scoutChatMessageRecord{
		ID:        fmt.Sprintf("scout-chat-message-choices-%s", thread.ID),
		Kind:      scoutChatMessageKindChoices,
		Role:      "scout",
		Text:      choices.Question,
		CreatedAt: "2026-07-06T00:00:00Z",
		Choices:   choices,
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages("aj@shareability.com", thread.ID, message); err != nil {
		t.Fatalf("seed choices card: %v", err)
	}
	return thread.ID, message.ID
}

// A tool-armed pill tap commits the reply as the user's turn plus the
// DETERMINISTIC proposal card for that tool — built from the stored record,
// never the request — and flips the card answered. It never launches; the
// proposal card's Run button stays the only door. A replayed tap rejects.
func TestScoutChatChoicePillToolArmCommitsProposal(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {
		t.Fatal("a pill tap must never launch — it arms the proposal card only")
	}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	previousGoalRunner := startGoalThreadAsync
	startGoalThreadAsync = func(_ *kanbanBoardApp, _ string) {
		t.Fatal("a pill tap must never launch a goal pipeline")
	}
	t.Cleanup(func() { startGoalThreadAsync = previousGoalRunner })

	threadID, messageID := seedChoicesThread(t, &scoutChatChoices{
		Question: "outline work, or the deck built end to end?",
		Query:    "we need to work on the deck for the WME meeting",
		Options: []scoutChatChoiceOption{
			{ID: "opt-1", Label: "tighten the outline", ToolID: "deck_outline"},
			{ID: "opt-2", Label: "full packaging run", Reply: "build the deck end to end from the existing outline", ToolID: "packaging_studio"},
		},
	})
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}

	response, err := kanbanApp.resolveScoutChatChoice(context.Background(), user, threadID, scoutChatChoiceAction{MessageID: messageID, OptionID: "opt-2"})
	if err != nil {
		t.Fatalf("resolve choice: %v", err)
	}
	if _, launched := response["agentThread"]; launched {
		t.Fatalf("response keys=%v — the pill must arm, never launch", responseKeys(response))
	}
	proposal, ok := response["proposal"].(*scoutRouterProposal)
	if !ok {
		t.Fatalf("proposal type=%T, want the armed *scoutRouterProposal", response["proposal"])
	}
	if proposal.Kind != scoutRouterProposalKindToolRun || proposal.ToolID != "packaging_studio" {
		t.Fatalf("proposal=%#v, want a packaging_studio tool_run", proposal)
	}
	if proposal.ToolName != "Packaging Studio" || proposal.GroupLabel != "Processes" {
		t.Fatalf("proposal name/group=%q/%q, want the process registry values", proposal.ToolName, proposal.GroupLabel)
	}
	if proposal.Objective != "build the deck end to end from the existing outline" {
		t.Fatalf("objective=%q, want the pill's crafted reply", proposal.Objective)
	}
	if proposal.Query != "we need to work on the deck for the WME meeting" {
		t.Fatalf("query=%q, want the originating ask as the Tier-0 escape", proposal.Query)
	}
	if !strings.Contains(proposal.Summary, "human checkpoint") {
		t.Fatalf("summary=%q, want the process checkpoint sentence", proposal.Summary)
	}

	saved := response["thread"].(scoutChatThreadRecord)
	if len(saved.Messages) != 3 {
		t.Fatalf("messages=%d, want choices card + reply + proposal", len(saved.Messages))
	}
	if saved.Messages[1].Role != "user" || saved.Messages[1].Text != "build the deck end to end from the existing outline" {
		t.Fatalf("reply message=%#v, want the pill reply as the user's turn", saved.Messages[1])
	}
	if saved.Messages[0].Choices.Status != "answered" || saved.Messages[0].Choices.SelectedID != "opt-2" {
		t.Fatalf("choices card=%#v, want answered + the selection recorded", saved.Messages[0].Choices)
	}
	if saved.Messages[2].Kind != scoutChatMessageKindProposal || saved.Messages[2].Proposal == nil {
		t.Fatalf("armed message=%#v, want a persisted proposal card", saved.Messages[2])
	}

	// First tap wins: the replay and the sibling pill both reject.
	if _, err := kanbanApp.resolveScoutChatChoice(context.Background(), user, threadID, scoutChatChoiceAction{MessageID: messageID, OptionID: "opt-2"}); err == nil {
		t.Fatal("replayed tap must reject — the card was already answered")
	}
	if _, err := kanbanApp.resolveScoutChatChoice(context.Background(), user, threadID, scoutChatChoiceAction{MessageID: messageID, OptionID: "opt-1"}); err == nil {
		t.Fatal("the sibling pill must reject after the card resolved")
	}
}

// A plain pill (no tool arm) commits the reply and answers it as Tier 0 —
// exactly the typed-message path, no router turn, no card.
func TestScoutChatChoicePillPlainReplyAnswersTier0(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-router-test")

	swapAnthropicMessagesResponder(t, func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		t.Fatal("a pill reply must not re-enter the router")
		return anthropicMessagesResponse{}, nil
	})
	swapAnthropicTextResponder(t, func(_ context.Context, _ string, request anthropicTextRequest) (string, error) {
		return "here's the short version, no run needed.", nil
	})

	threadID, messageID := seedChoicesThread(t, &scoutChatChoices{
		Question: "want the full research pass, or just my read?",
		Query:    "what do you think about the buyer landscape?",
		Options: []scoutChatChoiceOption{
			{ID: "opt-1", Label: "full research pass", ToolID: "deep_research"},
			{ID: "opt-2", Label: "just give me your read"},
		},
	})
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}

	response, err := kanbanApp.resolveScoutChatChoice(context.Background(), user, threadID, scoutChatChoiceAction{MessageID: messageID, OptionID: "opt-2"})
	if err != nil {
		t.Fatalf("resolve plain choice: %v", err)
	}
	if _, proposed := response["proposal"]; proposed {
		t.Fatalf("response keys=%v, want no proposal for a plain pill", responseKeys(response))
	}
	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || answer.Kind != "message" || answer.Text != "here's the short version, no run needed." {
		t.Fatalf("answer=%#v, want the Tier-0 inline answer", response["answer"])
	}
	saved := response["thread"].(scoutChatThreadRecord)
	if len(saved.Messages) != 3 || saved.Messages[1].Text != "just give me your read" {
		t.Fatalf("messages=%#v, want card + pill reply + answer", saved.Messages)
	}
}

// The founder's four scenarios: each phrasing, routed by the model to its
// intended target, must come back as a valid proposal carrying the registry's
// truth — including packaging_studio, which the enum offered but validation
// used to drop (toolByID never resolved processes).
func TestScoutChatRouterScenarioPhrasingsRouteToIntendedProposal(t *testing.T) {
	scenarios := []struct {
		name       string
		utterance  string
		toolID     string
		objective  string
		wantName   string
		wantGroup  string
		wantInWord string
	}{
		{
			name:       "pitch outline",
			utterance:  "let's work on the pitch outline for Station Tenn",
			toolID:     "deck_outline",
			objective:  "sequence the Station Tenn pitch slide by slide",
			wantName:   "Deck Outline",
			wantGroup:  "Package",
			wantInWord: "kill condition",
		},
		{
			name:       "design identity",
			utterance:  "we need to develop a design identity for this",
			toolID:     "brand_design_brief",
			objective:  "a brand and design brief for the venture",
			wantName:   "Brand & Design Brief",
			wantGroup:  "Package",
			wantInWord: "kill condition",
		},
		{
			name:       "deck from existing outline",
			utterance:  "take the outline we already have and build the deck from it",
			toolID:     "packaging_studio",
			objective:  "build the deck end to end using the existing outline as the spine",
			wantName:   "Packaging Studio",
			wantGroup:  "Processes",
			wantInWord: "human checkpoint",
		},
		{
			name:       "full end-to-end packaging",
			utterance:  "package this end to end, the full run",
			toolID:     "packaging_studio",
			objective:  "the full packaging run, founder's words to shipped package",
			wantName:   "Packaging Studio",
			wantGroup:  "Processes",
			wantInWord: "human checkpoint",
		},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			setupAuthTestEnv(t)
			t.Setenv("ANTHROPIC_API_KEY", "sk-ant-router-test")
			previousApp := kanbanApp
			kanbanApp = newIsolatedKanbanBoardApp(t)
			t.Cleanup(func() { kanbanApp = previousApp })

			previousRunner := startAgentThreadAsync
			startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {
				t.Fatal("a proposal must never launch")
			}
			t.Cleanup(func() { startAgentThreadAsync = previousRunner })

			var routed anthropicMessagesRequest
			swapAnthropicMessagesResponder(t, func(_ context.Context, _ string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
				routed = request
				return anthropicMessagesResponse{
					StopReason: "tool_use",
					Content: []json.RawMessage{
						mockAnthropicToolUseBlock("toolu_scenario", "propose_tool_run", map[string]any{
							"tool_id":   scenario.toolID,
							"objective": scenario.objective,
						}),
					},
				}, nil
			})

			user := accountStore().findUser("aj@shareability.com")
			if user == nil {
				t.Fatal("seed user aj@shareability.com missing")
			}
			private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
			if err != nil {
				t.Fatalf("create private thread: %v", err)
			}
			response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, private.ID, scenario.utterance, nil, "")
			if err != nil {
				t.Fatalf("append scenario message: %v", err)
			}

			// The intent map rides the system prompt — that is what makes these
			// phrasings route well on the live model.
			for _, anchor := range []string{"Intent map", "deck_outline", "brand_design_brief", "packaging_studio", "offer_choices"} {
				if !strings.Contains(routed.System, anchor) {
					t.Fatalf("router system prompt missing intent-map anchor %q", anchor)
				}
			}

			if _, launched := response["agentThread"]; launched {
				t.Fatalf("response keys=%v — NEVER silent-launch", responseKeys(response))
			}
			proposal, ok := response["proposal"].(*scoutRouterProposal)
			if !ok {
				t.Fatalf("proposal type=%T, want *scoutRouterProposal — %q must survive validation", response["proposal"], scenario.toolID)
			}
			if proposal.ToolID != scenario.toolID || proposal.ToolName != scenario.wantName || proposal.GroupLabel != scenario.wantGroup {
				t.Fatalf("proposal=%#v, want %s / %s / %s", proposal, scenario.toolID, scenario.wantName, scenario.wantGroup)
			}
			if proposal.Objective != scenario.objective {
				t.Fatalf("objective=%q, want the routed objective", proposal.Objective)
			}
			if !strings.Contains(proposal.Summary, scenario.wantInWord) {
				t.Fatalf("summary=%q, want %q named", proposal.Summary, scenario.wantInWord)
			}
			if proposal.Query != scenario.utterance {
				t.Fatalf("query=%q, want the utterance for the Tier-0 escape", proposal.Query)
			}
		})
	}
}

// The choice route is wired: a pill tap over HTTP resolves against the stored
// record under the normal session guard.
func TestAssistantChatThreadChoiceRoute(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	threadID, messageID := seedChoicesThread(t, &scoutChatChoices{
		Question: "outline work, or the deck built end to end?",
		Query:    "work on the deck",
		Options: []scoutChatChoiceOption{
			{ID: "opt-1", Label: "tighten the outline", ToolID: "deck_outline"},
			{ID: "opt-2", Label: "just talk it through"},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+threadID+"/choice", strings.NewReader(fmt.Sprintf(`{"messageId":%q,"optionId":"opt-1"}`, messageID)))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	assistantChatThreadHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Proposal *scoutRouterProposal `json:"proposal"`
		Thread   struct {
			Messages []scoutChatMessageRecord `json:"messages"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Proposal == nil || payload.Proposal.ToolID != "deck_outline" {
		t.Fatalf("body=%s, want the armed deck_outline proposal", rec.Body.String())
	}
	if len(payload.Thread.Messages) != 3 || payload.Thread.Messages[0].Choices.Status != "answered" {
		t.Fatalf("thread=%#v, want the card answered + reply + proposal", payload.Thread.Messages)
	}
}

// Keyless the whole layer disappears cleanly: no router turn means no choices
// card ever exists — the append path already proves plain Q&A; this pins that
// the response also carries no choices key.
func TestScoutChatChoicesKeylessNeverOffered(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.mu.Lock()
	kanbanApp.apiKey = "test-key"
	kanbanApp.mu.Unlock()
	t.Cleanup(func() { kanbanApp = previousApp })

	swapAnthropicMessagesResponder(t, func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		t.Fatal("keyless deploys must never attempt a router turn")
		return anthropicMessagesResponse{}, nil
	})
	swapOpenAITextResponder(t, func(context.Context, string, openAITextRequest) (string, error) {
		return "keyless answer.", nil
	})

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, private.ID, "should we work on the outline or the full deck?", nil, "")
	if err != nil {
		t.Fatalf("keyless append must degrade to plain Q&A, got error: %v", err)
	}
	if _, offered := response["choices"]; offered {
		t.Fatalf("response keys=%v, want no choices keyless", responseKeys(response))
	}
	if answer, ok := response["answer"].(scoutChatMessageRecord); !ok || answer.Text != "keyless answer." {
		t.Fatalf("answer=%#v, want the plain Q&A answer", response["answer"])
	}
}
