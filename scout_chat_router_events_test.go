package main

// W1 item 11 + W0 items 6/7 (docs/model-routing-master-plan-2026-07-11.md).
//
// DisableThinking: Sonnet 5 runs ADAPTIVE thinking when the field is omitted,
// and max_tokens caps thinking + tool call COMBINED — inside the router's
// 700-token budget the thinking burn truncated the tool_use mid-JSON, so a
// typed work ask silently degraded to an inline answer with NO proposal card.
// The router request must pin thinking off exactly like the chat path
// (anthropic_text.go / anthropic_text_test.go).
//
// Eval funnel: every routing turn records exactly one router_outcome event
// (proposed_tool / choice_pills / inline / deterministic_guard), max_tokens
// stops and unusable tool calls record router_truncation, undecodable tool
// JSON records parse_failure, and persisted chat proposal cards record
// minted / resolved / launched lifecycle events joined on the card's message
// id — the proposal-per-work-ask and acceptance-rate series the W0 rollup
// reads.

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

// readRouterLedgerEvents loads every telemetry event the isolated ledger dir
// captured. Globbing (rather than naming today's file) keeps the helper safe
// across a midnight rotation mid-test.
func readRouterLedgerEvents(t *testing.T, dir string) []map[string]any {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, evalLedgerFilePrefix+"-*.jsonl"))
	if err != nil {
		t.Fatalf("glob eval ledger: %v", err)
	}
	events := []map[string]any{}
	for _, path := range paths {
		events = append(events, readLedgerLines(t, path)...)
	}
	return events
}

// filterLedgerEvents keeps the events matching one type + kind pair.
func filterLedgerEvents(events []map[string]any, eventType string, kind string) []map[string]any {
	matched := []map[string]any{}
	for _, event := range events {
		if event["type"] == eventType && event["kind"] == kind {
			matched = append(matched, event)
		}
	}
	return matched
}

// ledgerEventFields returns the event's fields payload, never nil.
func ledgerEventFields(event map[string]any) map[string]any {
	fields, _ := event["fields"].(map[string]any)
	if fields == nil {
		fields = map[string]any{}
	}
	return fields
}

// The routing turn's wire request pins DisableThinking (W1 item 11, mirroring
// anthropic_text_test.go): the 700-token budget belongs to the tool call, not
// to adaptive thinking. The rest of the request shape — router model, doctrine
// floor effort, registry-injected tool schemas — rides along unchanged.
func TestScoutRouterRequestPinsDisableThinking(t *testing.T) {
	ledgerTestDir(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-router-test")
	t.Setenv("BONFIRE_ROUTER_MODEL", "")
	t.Setenv("BONFIRE_ROUTER_EFFORT", "")
	app := newIsolatedKanbanBoardApp(t)

	calls := 0
	var got anthropicMessagesRequest
	swapAnthropicMessagesResponder(t, func(_ context.Context, apiKey string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		calls++
		if apiKey != "sk-ant-router-test" {
			t.Fatalf("apiKey=%q, want sk-ant-router-test", apiKey)
		}
		got = request
		return anthropicMessagesResponse{
			StopReason: "end_turn",
			Content:    []json.RawMessage{mockAnthropicTextBlock("quick inline answer")},
		}, nil
	})

	if verdict := app.routeScoutChatTurn(context.Background(), "sort the launch follow-ups into a plan for tomorrow", nil); verdict != nil {
		t.Fatalf("verdict=%#v, want nil (Tier 0 inline)", verdict)
	}
	if calls != 1 {
		t.Fatalf("router wire calls=%d, want exactly one routing turn", calls)
	}
	if !got.DisableThinking {
		t.Fatal("router request must set DisableThinking (Sonnet 5 adaptive thinking shares the 700-token tool-call budget and truncates proposals)")
	}
	if got.Model != "claude-sonnet-5" {
		t.Fatalf("model=%q, want the claude-sonnet-5 router default", got.Model)
	}
	if got.MaxTokens != scoutRouterMaxTokens || got.Effort != "medium" {
		t.Fatalf("budget=%d/%q, want %d/medium (doctrine floor)", got.MaxTokens, got.Effort, scoutRouterMaxTokens)
	}
	if len(got.Tools) < 4 {
		t.Fatalf("tools=%d, want the registry-injected routing schemas", len(got.Tools))
	}
}

// One router_outcome event per routing turn, router_truncation on max_tokens
// or an unusable tool call, parse_failure on undecodable tool JSON — the W0
// item-6 funnel that proves the DisableThinking fix live (truncated-rate ~0).
func TestScoutRouterOutcomeAndTruncationEvents(t *testing.T) {
	cases := []struct {
		name            string
		text            string
		response        anthropicMessagesResponse
		responderErr    error
		wantWireCalls   int
		wantVerdict     string
		wantDegraded    string
		wantSource      string
		wantTruncations int
		wantStopReason  string
		wantUnusable    bool
		wantParseTool   string
	}{
		{
			name: "model proposal records proposed_tool",
			response: anthropicMessagesResponse{
				StopReason: "tool_use",
				Content: []json.RawMessage{
					mockAnthropicToolUseBlock("toolu_ws", "propose_workstream", map[string]any{
						"mode":  "research",
						"query": "map the rodeo creator market",
					}),
				},
			},
			wantWireCalls: 1,
			wantVerdict:   routerVerdictProposedTool,
			wantSource:    proposalSourceChatRouter,
		},
		{
			name: "clarifying question records choice_pills",
			response: anthropicMessagesResponse{
				StopReason: "tool_use",
				Content: []json.RawMessage{
					mockAnthropicToolUseBlock("toolu_choices", "offer_choices", map[string]any{
						"question": "outline work, or the deck built out?",
						"options": []map[string]any{
							{"label": "tighten the outline"},
							{"label": "build the deck"},
						},
					}),
				},
			},
			wantWireCalls: 1,
			wantVerdict:   routerVerdictChoicePills,
			wantSource:    proposalSourceChatRouter,
		},
		{
			name: "plain answer records inline",
			response: anthropicMessagesResponse{
				StopReason: "end_turn",
				Content:    []json.RawMessage{mockAnthropicTextBlock("that is a chat answer")},
			},
			wantWireCalls: 1,
			wantVerdict:   routerVerdictInline,
		},
		{
			name:            "max_tokens stop records truncation then inline",
			response:        anthropicMessagesResponse{StopReason: "max_tokens"},
			wantWireCalls:   1,
			wantVerdict:     routerVerdictInline,
			wantTruncations: 1,
			wantStopReason:  "max_tokens",
		},
		{
			name: "unusable tool call records parse_failure and truncation",
			response: anthropicMessagesResponse{
				StopReason: "max_tokens",
				Content: []json.RawMessage{
					// A tool_use whose input was cut off mid-JSON — the exact
					// silent-degrade signature DisableThinking exists to kill.
					json.RawMessage(`{"type":"tool_use","id":"toolu_cut","name":"propose_workstream","input":"cut-off"}`),
				},
			},
			wantWireCalls:   1,
			wantVerdict:     routerVerdictInline,
			wantTruncations: 1,
			wantStopReason:  "max_tokens",
			wantUnusable:    true,
			wantParseTool:   "propose_workstream",
		},
		{
			name:          "wire error degrades to inline with the degraded stamp",
			responderErr:  errors.New("529 overloaded"),
			wantWireCalls: 1,
			wantVerdict:   routerVerdictInline,
			wantDegraded:  "router_error",
		},
		{
			name:          "deterministic guard commits before the wire",
			text:          "package this end to end",
			wantWireCalls: 0,
			wantVerdict:   routerVerdictDeterministicGuard,
			wantSource:    proposalSourceDeterministicGuard,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := ledgerTestDir(t)
			t.Setenv("ANTHROPIC_API_KEY", "sk-ant-router-test")
			t.Setenv("BONFIRE_ROUTER_MODEL", "")
			app := newIsolatedKanbanBoardApp(t)

			calls := 0
			swapAnthropicMessagesResponder(t, func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
				calls++
				return tc.response, tc.responderErr
			})

			text := tc.text
			if text == "" {
				text = "sort the launch follow-ups into a plan for tomorrow"
			}
			verdict := app.routeScoutChatTurn(context.Background(), text, nil)
			if calls != tc.wantWireCalls {
				t.Fatalf("wire calls=%d, want %d", calls, tc.wantWireCalls)
			}
			if tc.wantSource != "" && (verdict == nil || verdict.source != tc.wantSource) {
				t.Fatalf("verdict=%#v, want provenance source %q", verdict, tc.wantSource)
			}

			events := readRouterLedgerEvents(t, dir)
			outcomes := filterLedgerEvents(events, telemetryTypeEval, evalKindRouterOutcome)
			if len(outcomes) != 1 {
				t.Fatalf("router_outcome events=%d, want exactly one per routing turn", len(outcomes))
			}
			if outcomes[0]["lane"] != seatRouter {
				t.Fatalf("outcome lane=%v, want %q", outcomes[0]["lane"], seatRouter)
			}
			fields := ledgerEventFields(outcomes[0])
			if fields["verdict"] != tc.wantVerdict {
				t.Fatalf("verdict=%v, want %q", fields["verdict"], tc.wantVerdict)
			}
			if tc.wantDegraded != "" && fields["degraded"] != tc.wantDegraded {
				t.Fatalf("degraded=%v, want %q", fields["degraded"], tc.wantDegraded)
			}

			truncations := filterLedgerEvents(events, telemetryTypeEval, evalKindRouterTruncation)
			if len(truncations) != tc.wantTruncations {
				t.Fatalf("router_truncation events=%d, want %d", len(truncations), tc.wantTruncations)
			}
			if tc.wantTruncations == 1 {
				truncationFields := ledgerEventFields(truncations[0])
				if truncationFields["stop_reason"] != tc.wantStopReason {
					t.Fatalf("stop_reason=%v, want %q", truncationFields["stop_reason"], tc.wantStopReason)
				}
				if tc.wantUnusable && truncationFields["unusable_tool_call"] != true {
					t.Fatalf("unusable_tool_call=%v, want true", truncationFields["unusable_tool_call"])
				}
			}

			parseFailures := filterLedgerEvents(events, telemetryTypeEval, evalKindParseFailure)
			if tc.wantParseTool == "" {
				if len(parseFailures) != 0 {
					t.Fatalf("parse_failure events=%d, want none", len(parseFailures))
				}
				return
			}
			if len(parseFailures) != 1 {
				t.Fatalf("parse_failure events=%d, want exactly one", len(parseFailures))
			}
			parseFields := ledgerEventFields(parseFailures[0])
			if parseFields["seat"] != seatRouter || parseFields["model"] != "claude-sonnet-5" || parseFields["tool"] != tc.wantParseTool {
				t.Fatalf("parse_failure fields=%v, want seat/model/tool stamped per the evalKindParseFailure contract", parseFields)
			}
		})
	}
}

// A persisted chat proposal card mints exactly one lifecycle event with
// message lineage, and the workstream confirm records resolved + launched
// joined on the card's message id — plus the router_outcome confirmed event
// the acceptance-rate series reads (W0 item 7).
func TestScoutChatProposalLifecycleEvents(t *testing.T) {
	setupAuthTestEnv(t)
	dir := ledgerTestDir(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-router-test")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	swapAnthropicMessagesResponder(t, func(_ context.Context, _ string, _ anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		return anthropicMessagesResponse{
			StopReason: "tool_use",
			Content: []json.RawMessage{
				mockAnthropicToolUseBlock("toolu_ws", "propose_workstream", map[string]any{
					"mode":  "research",
					"query": "map the rodeo creator market",
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
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, private.ID, "dig into the rodeo creator market", nil, "")
	if err != nil {
		t.Fatalf("append routed message: %v", err)
	}
	saved := response["thread"].(scoutChatThreadRecord)
	if len(saved.Messages) != 2 || saved.Messages[1].Proposal == nil {
		t.Fatalf("messages=%#v, want user turn + proposal card", saved.Messages)
	}
	userMessageID, cardID := saved.Messages[0].ID, saved.Messages[1].ID

	events := readRouterLedgerEvents(t, dir)
	minted := filterLedgerEvents(events, telemetryTypeProposal, proposalEventMinted)
	if len(minted) != 1 {
		t.Fatalf("minted events=%d, want exactly one per persisted card", len(minted))
	}
	mintFields := ledgerEventFields(minted[0])
	if mintFields["proposal_id"] != cardID {
		t.Fatalf("mint proposal_id=%v, want the card's message id %q", mintFields["proposal_id"], cardID)
	}
	if mintFields["source"] != proposalSourceChatRouter || mintFields["thread_id"] != private.ID || mintFields["from_message_id"] != userMessageID {
		t.Fatalf("mint lineage=%v, want chat_router source + thread + originating message", mintFields)
	}
	if mintFields["kind"] != scoutRouterProposalKindWorkstream || mintFields["tool_id"] != "research" || mintFields["lane"] != saved.Messages[1].Proposal.Lane {
		t.Fatalf("mint route fields=%v, want the persisted card's kind/mode/lane", mintFields)
	}

	// The accept IS the workstream confirm: resolved + launched join the mint
	// on the card id, and the eval acceptance series gets its confirmed event.
	if _, err := kanbanApp.resolveScoutChatProposal(context.Background(), user, private.ID, scoutChatProposalAction{
		Action:    "accepted",
		MessageID: cardID,
	}); err != nil {
		t.Fatalf("accept workstream: %v", err)
	}

	events = readRouterLedgerEvents(t, dir)
	resolved := filterLedgerEvents(events, telemetryTypeProposal, proposalEventResolved)
	if len(resolved) != 1 {
		t.Fatalf("resolved events=%d, want exactly one", len(resolved))
	}
	resolvedFields := ledgerEventFields(resolved[0])
	if resolvedFields["proposal_id"] != cardID || resolvedFields["resolution"] != routerVerdictConfirmed {
		t.Fatalf("resolved fields=%v, want confirmed on the card id", resolvedFields)
	}

	// launchAgentThreadWithSpec's choke point is the SINGLE launched emitter: the
	// chat door threads the card id + chat_workstream path onto the spec, so the
	// one launched row carries the card id and its canonical thread_id (no
	// separate proposal_id-less row for this path).
	launched := []map[string]any{}
	for _, event := range filterLedgerEvents(events, telemetryTypeProposal, proposalEventLaunched) {
		if ledgerEventFields(event)["proposal_id"] == cardID {
			launched = append(launched, event)
		}
	}
	if len(launched) != 1 {
		t.Fatalf("card-id launched events=%d, want exactly one", len(launched))
	}
	launchedFields := ledgerEventFields(launched[0])
	if launchedFields["proposal_id"] != cardID || launchedFields["path"] != "chat_workstream" {
		t.Fatalf("launched fields=%v, want the chat_workstream path on the card id", launchedFields)
	}
	if threadID, _ := launchedFields["thread_id"].(string); threadID == "" {
		t.Fatalf("launched fields=%v, want the launched thread id", launchedFields)
	}

	confirmed := 0
	for _, event := range filterLedgerEvents(events, telemetryTypeEval, evalKindRouterOutcome) {
		fields := ledgerEventFields(event)
		if fields["verdict"] == routerVerdictConfirmed && fields["proposal_id"] == cardID {
			confirmed++
		}
	}
	if confirmed != 1 {
		t.Fatalf("router_outcome confirmed events=%d, want exactly one", confirmed)
	}
}

// A dismissal records resolved (dismissed) and the dismissed outcome — and
// NEVER a launched event.
func TestScoutChatProposalDismissRecordsResolvedNeverLaunched(t *testing.T) {
	setupAuthTestEnv(t)
	dir := ledgerTestDir(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {
		t.Fatal("a dismissal must never launch")
	}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	// Query stays empty so the dismissal skips the Tier-0 re-ask — no model
	// seam needed; the events are the subject here.
	cardID := seedScoutChatProposalCard(t, private.ID, "aj@shareability.com", scoutRouterProposal{
		Kind:      scoutRouterProposalKindWorkstream,
		Mode:      "research",
		Objective: "the rodeo creator market",
	})

	if _, err := kanbanApp.resolveScoutChatProposal(context.Background(), user, private.ID, scoutChatProposalAction{
		Action:    "dismissed",
		MessageID: cardID,
	}); err != nil {
		t.Fatalf("dismiss: %v", err)
	}

	events := readRouterLedgerEvents(t, dir)
	resolved := filterLedgerEvents(events, telemetryTypeProposal, proposalEventResolved)
	if len(resolved) != 1 {
		t.Fatalf("resolved events=%d, want exactly one", len(resolved))
	}
	resolvedFields := ledgerEventFields(resolved[0])
	if resolvedFields["proposal_id"] != cardID || resolvedFields["resolution"] != routerVerdictDismissed {
		t.Fatalf("resolved fields=%v, want dismissed on the card id", resolvedFields)
	}
	if launched := filterLedgerEvents(events, telemetryTypeProposal, proposalEventLaunched); len(launched) != 0 {
		t.Fatalf("launched events=%d, want none on a dismissal", len(launched))
	}
	dismissedOutcomes := 0
	for _, event := range filterLedgerEvents(events, telemetryTypeEval, evalKindRouterOutcome) {
		fields := ledgerEventFields(event)
		if fields["verdict"] == routerVerdictDismissed && fields["proposal_id"] == cardID {
			dismissedOutcomes++
		}
	}
	if dismissedOutcomes != 1 {
		t.Fatalf("router_outcome dismissed events=%d, want exactly one", dismissedOutcomes)
	}
}

// A guard-committed card carries deterministic_guard provenance on its minted
// event — the model never ran, and the funnel must say so.
func TestScoutChatDeterministicGuardMintCarriesGuardSource(t *testing.T) {
	setupAuthTestEnv(t)
	dir := ledgerTestDir(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-router-test")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	swapAnthropicMessagesResponder(t, func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		t.Fatal("the deterministic guard must commit before any wire call")
		return anthropicMessagesResponse{}, nil
	})

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, private.ID, "package this end to end", nil, "")
	if err != nil {
		t.Fatalf("append guard message: %v", err)
	}
	saved := response["thread"].(scoutChatThreadRecord)
	if len(saved.Messages) != 2 || saved.Messages[1].Proposal == nil {
		t.Fatalf("messages=%#v, want user turn + guard-committed card", saved.Messages)
	}

	events := readRouterLedgerEvents(t, dir)
	minted := filterLedgerEvents(events, telemetryTypeProposal, proposalEventMinted)
	if len(minted) != 1 {
		t.Fatalf("minted events=%d, want exactly one", len(minted))
	}
	mintFields := ledgerEventFields(minted[0])
	if mintFields["source"] != proposalSourceDeterministicGuard || mintFields["proposal_id"] != saved.Messages[1].ID {
		t.Fatalf("mint fields=%v, want deterministic_guard provenance on the card id", mintFields)
	}
	outcomes := filterLedgerEvents(events, telemetryTypeEval, evalKindRouterOutcome)
	if len(outcomes) != 1 || ledgerEventFields(outcomes[0])["verdict"] != routerVerdictDeterministicGuard {
		t.Fatalf("outcomes=%v, want one deterministic_guard verdict", outcomes)
	}
}

// A tool-armed pill tap mints a NEW proposal card: chat_router provenance
// (the router authored the pill), via=choice_pill, lineage back to the
// choices card the tap resolved.
func TestScoutChatChoicePillArmMintsProposalEvent(t *testing.T) {
	setupAuthTestEnv(t)
	dir := ledgerTestDir(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

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

	response, err := kanbanApp.resolveScoutChatChoice(context.Background(), user, threadID, scoutChatChoiceAction{
		MessageID: messageID,
		OptionID:  "opt-2",
	})
	if err != nil {
		t.Fatalf("resolve choice: %v", err)
	}
	saved := response["thread"].(scoutChatThreadRecord)
	if len(saved.Messages) != 3 || saved.Messages[2].Proposal == nil {
		t.Fatalf("messages=%#v, want choices card + reply + armed proposal", saved.Messages)
	}

	events := readRouterLedgerEvents(t, dir)
	minted := filterLedgerEvents(events, telemetryTypeProposal, proposalEventMinted)
	if len(minted) != 1 {
		t.Fatalf("minted events=%d, want exactly one for the armed card", len(minted))
	}
	mintFields := ledgerEventFields(minted[0])
	if mintFields["proposal_id"] != saved.Messages[2].ID || mintFields["source"] != proposalSourceChatRouter {
		t.Fatalf("mint fields=%v, want chat_router provenance on the armed card", mintFields)
	}
	if mintFields["via"] != "choice_pill" || mintFields["from_message_id"] != messageID {
		t.Fatalf("mint lineage=%v, want via=choice_pill back to the choices card", mintFields)
	}
	if mintFields["tool_id"] != "packaging_studio" {
		t.Fatalf("mint tool_id=%v, want packaging_studio", mintFields["tool_id"])
	}
}
