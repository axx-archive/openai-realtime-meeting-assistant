package main

// Card 088 Slice B (propose_goal) + Slice A (governance lane as data). The
// router's fourth tool proposes a free-form multi-step goal — the typed twin of
// voice initiate_goal's free-form branch — as a Kind=proposal card that launches
// NOTHING until the card's Run posts POST /assistant/goal with no toolTemplate.
// Every proposal card carries its 069 governance lane (approval_lanes.go) as
// data so the honest approval caption renders and the accept/dismiss signal is
// measurable per lane.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A propose_goal routing turn persists a Kind=proposal card whose proposal.kind
// is goal_run: the objective survives, authority is clamped to workspace_write,
// the lane is standard (Scout-proposed work is never auto), the weight is the
// goal-loop label, and the summary names the decompose->gate loop. It never
// launches.
func TestScoutChatRouterProposesGoalRun(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-router-test")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	startAgentThreadAsyncPrev := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {
		t.Fatal("a goal_run proposal must never launch an agent thread")
	}
	t.Cleanup(func() { startAgentThreadAsync = startAgentThreadAsyncPrev })
	startGoalThreadAsyncPrev := startGoalThreadAsync
	startGoalThreadAsync = func(_ *kanbanBoardApp, _ string) {
		t.Fatal("a goal_run proposal must never launch a goal pipeline")
	}
	t.Cleanup(func() { startGoalThreadAsync = startGoalThreadAsyncPrev })
	swapAnthropicTextResponder(t, func(context.Context, string, anthropicTextRequest) (string, error) {
		t.Fatal("a proposal turn must not also run the Q&A path")
		return "", nil
	})

	swapAnthropicMessagesResponder(t, func(_ context.Context, _ string, _ anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		return anthropicMessagesResponse{
			StopReason: "tool_use",
			Content: []json.RawMessage{
				mockAnthropicToolUseBlock("toolu_goal", "propose_goal", map[string]any{
					"objective":      "package the Aurora IP into a one-pager and an investor deck",
					"authority_hint": "workspace_write",
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
	utterance := "help me take the Aurora IP from raw idea to a shipped package"
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, private.ID, utterance, nil, "")
	if err != nil {
		t.Fatalf("append routed message: %v", err)
	}
	if _, launched := response["agentThread"]; launched {
		t.Fatalf("response keys=%v — NEVER silent-launch", responseKeys(response))
	}
	proposal, ok := response["proposal"].(*scoutRouterProposal)
	if !ok {
		t.Fatalf("proposal type=%T, want *scoutRouterProposal", response["proposal"])
	}
	if proposal.Kind != scoutRouterProposalKindGoalRun {
		t.Fatalf("proposal.Kind=%q, want %q", proposal.Kind, scoutRouterProposalKindGoalRun)
	}
	if proposal.ToolID != "" || proposal.Mode != "" {
		t.Fatalf("a free-form goal carries no toolId/mode: %#v", proposal)
	}
	if proposal.Objective != "package the Aurora IP into a one-pager and an investor deck" {
		t.Fatalf("objective=%q, want the routed goal objective", proposal.Objective)
	}
	if proposal.Authority != toolAuthorityWorkspaceWrite {
		t.Fatalf("authority=%q, want workspace_write", proposal.Authority)
	}
	if proposal.Lane != approvalLaneStandard {
		t.Fatalf("lane=%q, want the standard governance lane", proposal.Lane)
	}
	if proposal.WeightLabel != scoutProposalWeightGoalLoop {
		t.Fatalf("weight=%q, want the goal-loop label", proposal.WeightLabel)
	}
	if !strings.Contains(proposal.Summary, "goal loop") || !strings.Contains(proposal.Summary, "gates before") {
		t.Fatalf("summary=%q, want the decompose->gate loop named", proposal.Summary)
	}
	if proposal.Query != utterance {
		t.Fatalf("query=%q, want the utterance for the Tier-0 escape", proposal.Query)
	}

	// It persists as a Kind=proposal message (the render dispatch keys on that).
	saved := response["thread"].(scoutChatThreadRecord)
	if len(saved.Messages) != 2 || saved.Messages[1].Kind != scoutChatMessageKindProposal || saved.Messages[1].Proposal == nil {
		t.Fatalf("persisted messages=%#v, want user turn + goal_run proposal card", saved.Messages)
	}
	if saved.Messages[1].Proposal.Kind != scoutRouterProposalKindGoalRun {
		t.Fatalf("persisted proposal kind=%q, want goal_run", saved.Messages[1].Proposal.Kind)
	}
}

// Authority is clamped exactly like voice initiate_goal / assistantGoalHandler:
// read_only survives, workspace_write survives, and anything else (including a
// model that hallucinates external_write, or a blank hint) degrades to
// workspace_write — external_write is earned only at the ship gate.
func TestScoutChatGoalProposalClampsAuthority(t *testing.T) {
	cases := []struct {
		hint string
		want string
	}{
		{"read_only", toolAuthorityReadOnly},
		{"workspace_write", toolAuthorityWorkspaceWrite},
		{"external_write", toolAuthorityWorkspaceWrite},
		{"", toolAuthorityWorkspaceWrite},
		{"garbage", toolAuthorityWorkspaceWrite},
	}
	for _, tc := range cases {
		proposal := scoutRouterGoalProposal("ship the thing end to end", tc.hint, "", "ship the thing")
		if proposal == nil {
			t.Fatalf("hint=%q: goal proposal must not be nil for a non-empty objective", tc.hint)
		}
		if proposal.Authority != tc.want {
			t.Fatalf("hint=%q: authority=%q, want %q (external_write is never granted here)", tc.hint, proposal.Authority, tc.want)
		}
		// The read_only lane is still standard (system-proposed), and workspace
		// write never escalates a plain goal past standard.
		if proposal.Lane != approvalLaneStandard {
			t.Fatalf("hint=%q: lane=%q, want standard", tc.hint, proposal.Lane)
		}
	}
	// A blank objective (and blank fallback query) degrades to nil — an inline
	// answer, never a card.
	if scoutRouterGoalProposal("", "", "", "") != nil {
		t.Fatal("a blank objective must degrade to nil")
	}
}

// The propose_goal validation path (JSON tool_use) mirrors the pure builder:
// a package_id rides through, and a blank objective falls back to the query.
func TestScoutChatGoalProposalFromToolUse(t *testing.T) {
	block := decodeAnthropicBlock(mockAnthropicToolUseBlock("toolu_goal", "propose_goal", map[string]any{
		"objective":      "",
		"package_id":     "pkg-aurora",
		"authority_hint": "read_only",
	}))
	proposal := scoutRouterProposalFromToolUse(block, "take Aurora from idea to shipped pitch")
	if proposal == nil {
		t.Fatal("propose_goal with a blank objective must fall back to the query, not nil")
	}
	if proposal.Kind != scoutRouterProposalKindGoalRun {
		t.Fatalf("kind=%q, want goal_run", proposal.Kind)
	}
	if proposal.Objective != "take Aurora from idea to shipped pitch" {
		t.Fatalf("objective=%q, want the query fallback", proposal.Objective)
	}
	if proposal.PackageID != "pkg-aurora" {
		t.Fatalf("packageId=%q, want the routed package id", proposal.PackageID)
	}
	if proposal.Authority != toolAuthorityReadOnly {
		t.Fatalf("authority=%q, want read_only preserved", proposal.Authority)
	}
}

// Accepting a goal_run card is signal-only: like tool_run, the launch is the
// card's Run button (runGoalPipeline -> POST /assistant/goal), so the accept
// route records the acceptance signal — carrying the lane — and launches
// nothing. First verdict wins; a replay rejects.
func TestScoutChatGoalRunAcceptIsSignalOnly(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	startAgentThreadAsyncPrev := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {
		t.Fatal("accepting a goal_run card must never launch an agent thread")
	}
	t.Cleanup(func() { startAgentThreadAsync = startAgentThreadAsyncPrev })
	startGoalThreadAsyncPrev := startGoalThreadAsync
	startGoalThreadAsync = func(_ *kanbanBoardApp, _ string) {
		t.Fatal("accepting a goal_run card must never launch a goal pipeline server-side")
	}
	t.Cleanup(func() { startGoalThreadAsync = startGoalThreadAsyncPrev })

	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	card := scoutChatMessageRecord{
		ID:        "scout-chat-message-goal-1",
		Kind:      scoutChatMessageKindProposal,
		Role:      "scout",
		Text:      "this launches the multi-step goal loop",
		CreatedAt: "2026-07-06T00:00:00Z",
		Proposal:  scoutRouterGoalProposal("package Aurora into a one-pager and a deck", "workspace_write", "", "package Aurora"),
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages("aj@shareability.com", thread.ID, card); err != nil {
		t.Fatalf("seed goal_run card: %v", err)
	}
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}

	response, err := kanbanApp.resolveScoutChatProposal(context.Background(), user, thread.ID, scoutChatProposalAction{
		Action:    "accepted",
		MessageID: card.ID,
	})
	if err != nil {
		t.Fatalf("resolve goal_run accept: %v", err)
	}
	if _, launched := response["agentThread"]; launched {
		t.Fatalf("response keys=%v — goal_run accept is signal-only", responseKeys(response))
	}

	// The acceptance signal carries the lane so acceptance is measurable per lane.
	if !signalRecordedWithLane(kanbanApp, signalEventRouterProposalAccepted, approvalLaneStandard) {
		t.Fatalf("no %s signal carrying lane=%q was recorded", signalEventRouterProposalAccepted, approvalLaneStandard)
	}

	// First verdict wins.
	if _, err := kanbanApp.resolveScoutChatProposal(context.Background(), user, thread.ID, scoutChatProposalAction{
		Action:    "accepted",
		MessageID: card.ID,
	}); err == nil {
		t.Fatal("a replayed accept must reject — the card was already resolved")
	}
}

// Keyless deploys never attempt a router turn, so propose_goal never fires and
// the ask degrades to plain Q&A with no proposal.
func TestScoutChatGoalRunKeylessNeverProposes(t *testing.T) {
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
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, private.ID, "take this from idea to a shipped pitch end to end as a goal", nil, "")
	if err != nil {
		t.Fatalf("keyless append must degrade to plain Q&A, got error: %v", err)
	}
	if _, proposed := response["proposal"]; proposed {
		t.Fatalf("response keys=%v, want no proposal keyless", responseKeys(response))
	}
	if answer, ok := response["answer"].(scoutChatMessageRecord); !ok || answer.Text != "keyless answer." {
		t.Fatalf("answer=%#v, want the plain Q&A answer", response["answer"])
	}
}

// Every proposal-card builder stamps the 069 governance lane (approval_lanes.go)
// as data: tool runs, single-shot renders, and free-form goals all classify
// standard (Scout-proposed work is never "auto"), and external_write authority
// classifies heavy — the fence that keeps the card honest about what its confirm
// triggers.
func TestScoutProposalCardsCarryGovernanceLane(t *testing.T) {
	// A read_only registry tool run.
	toolRun := scoutRouterProposalForToolID("deep_research", "dig the buyer landscape", "who buys this")
	if toolRun == nil {
		t.Fatal("deep_research proposal must build")
	}
	if toolRun.Lane != approvalLaneStandard {
		t.Fatalf("tool_run lane=%q, want standard", toolRun.Lane)
	}

	// The single-shot concept render.
	image := scoutRouterImageProposal("a neon poster of the venture", "make me a poster")
	if image == nil {
		t.Fatal("image proposal must build")
	}
	if image.Lane != approvalLaneStandard {
		t.Fatalf("image lane=%q, want standard", image.Lane)
	}

	// A free-form goal.
	goal := scoutRouterGoalProposal("ship the whole thing", "", "", "ship it")
	if goal.Lane != approvalLaneStandard {
		t.Fatalf("goal_run lane=%q, want standard", goal.Lane)
	}

	// The deterministic guard's flagship proposal carries a lane too.
	if verdict := deterministicRouterGuard("package this end to end"); verdict == nil || verdict.proposal == nil {
		t.Fatal("the guard must arm the flagship on 'end to end'")
	} else if verdict.proposal.Lane == "" {
		t.Fatal("the guard's flagship proposal must carry a governance lane")
	}

	// The fence: external_write work is heavy, never standard. This is the
	// dimension the card must never soften.
	if lane := scoutProposalLane("goal", "some_tool", codexJobAuthorityExternalWrite); lane != approvalLaneHeavy {
		t.Fatalf("external_write lane=%q, want heavy", lane)
	}
}

// signalRecordedWithLane scans the memory store for a signal of the given event
// carrying the expected lane payload. Signals are filtered out of snapshot()
// (distillation-only, may quote private text), so read them via entriesOfKind.
func signalRecordedWithLane(app *kanbanBoardApp, event string, lane string) bool {
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindSignal, 0) {
		record, ok := decodeSignalEntry(entry)
		if !ok || record.Event != event {
			continue
		}
		if record.Payload != nil && record.Payload["lane"] == lane {
			return true
		}
	}
	return false
}
