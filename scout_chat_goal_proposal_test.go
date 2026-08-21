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
	"errors"
	"strings"
	"testing"
)

// A safe private goal request starts directly and persists a truthful work card.
func TestScoutChatRouterStartsPrivateGoalRun(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "openai-router-test")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = "openai-router-test"
	t.Cleanup(func() { kanbanApp = previousApp })

	startGoalThreadAsyncPrev := startGoalThreadAsync
	startGoalThreadAsync = func(_ *kanbanBoardApp, _ string) {}
	t.Cleanup(func() { startGoalThreadAsync = startGoalThreadAsyncPrev })
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_route" {
			t.Fatal("a work-routing turn must not also run the Q&A path")
		}
		return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
			Outcome: string(conversationIntentStartPrivateWork), Route: "goal_run", Objective: "package the Aurora IP into a one-pager and an investor deck", AuthorityHint: "workspace_write",
		}), nil
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
	launched, ok := response["agentThread"].(scoutAgentThread)
	if !ok {
		t.Fatalf("agentThread type=%T, want direct goal launch", response["agentThread"])
	}
	if launched.Mode != "goal" || launched.Query != "package the Aurora IP into a one-pager and an investor deck" {
		t.Fatalf("launched=%#v", launched)
	}
	if launched.Artifact.Metadata["authority"] != toolAuthorityWorkspaceWrite {
		t.Fatalf("authority=%q, want workspace_write", launched.Artifact.Metadata["authority"])
	}
	if response["intentOutcome"] != string(conversationIntentStartPrivateWork) || response["proposal"] != nil {
		t.Fatalf("response=%#v", response)
	}
	saved := response["thread"].(scoutChatThreadRecord)
	if len(saved.Messages) != 2 || saved.Messages[1].Kind != "thread" || saved.Messages[1].Thread == nil || saved.Messages[1].IntentOutcome != string(conversationIntentStartPrivateWork) {
		t.Fatalf("persisted messages=%#v, want user turn + work card", saved.Messages)
	}
}

func TestAcceptedPrivateToolRunReconcilesLostCardWithoutDuplicateLaunch(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "private-proposal-test")
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "private-proposal-test"
	starts := 0
	previousGoalStarter := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) { starts++ }
	previousProbe := conversationWorkBeforeCardCommitProbe
	crashed := false
	conversationWorkBeforeCardCommitProbe = func(scoutAgentThread) error {
		if !crashed {
			crashed = true
			return errors.New("simulated restart before private tool card projection")
		}
		return nil
	}
	t.Cleanup(func() {
		startGoalThreadAsync = previousGoalStarter
		conversationWorkBeforeCardCommitProbe = previousProbe
	})
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Private tool proposal", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("aj@shareability.com")
	source := scoutChatMessageRecord{ID: "private-tool-source", Kind: "message", Role: "user", AuthorName: user.Name, AuthorEmail: user.Email, Text: "Build the actual five-slide deck", CreatedAt: "2026-08-21T12:00:00Z"}
	proposal := scoutRouterProposalForToolID(packagingStudioProcessID, "Build the actual five-slide deck", source.Text)
	if proposal == nil || proposal.IntentOutcome != "" {
		t.Fatalf("fixture must be a normal private proposal: %+v", proposal)
	}
	card := scoutChatMessageRecord{ID: "private-tool-proposal", Kind: scoutChatMessageKindProposal, Role: "scout", Text: proposal.Summary, Proposal: proposal, CausedByMessageID: source.ID, CreatedAt: "2026-08-21T12:00:01Z"}
	if _, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, source, card); err != nil {
		t.Fatal(err)
	}
	_, err = app.resolveScoutChatProposal(context.Background(), user, thread.ID, scoutChatProposalAction{Action: "accepted", MessageID: card.ID})
	var pending *conversationWorkProjectionPendingError
	if !errors.As(err, &pending) || starts != 1 {
		t.Fatalf("first accept err=%v starts=%d, want one launched run with pending projection", err, starts)
	}
	before, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil || len(before.Messages) != 2 || before.Messages[1].Proposal.Status != "accepted" {
		t.Fatalf("accepted pending thread=%#v err=%v", before.Messages, err)
	}
	conversationWorkBeforeCardCommitProbe = nil // simulate the restarted process
	recovered, handled, err := app.reconcileAcceptedScoutChatProposal(user, thread.ID, card.ID)
	if err != nil || !handled || recovered["reconciled"] != true || starts != 1 {
		t.Fatalf("recovery handled=%v err=%v starts=%d response=%#v", handled, err, starts, recovered)
	}
	work, ok := recovered["agentThread"].(scoutAgentThread)
	saved := recovered["thread"].(scoutChatThreadRecord)
	if !ok || work.Artifact.Metadata["processId"] != packagingStudioProcessID || len(saved.Messages) != 3 || saved.Messages[2].Thread == nil || saved.Messages[2].Thread.ID != work.ID {
		t.Fatalf("recovered work=%#v messages=%#v", recovered["agentThread"], saved.Messages)
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

// Accepting a private goal_run card is the launch authority. It creates one
// durable work card/provider run; retries reconcile that exact operation and
// never silently succeed or create a duplicate.
func TestScoutChatGoalRunAcceptLaunchesExactlyOnce(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "private-proposal-test")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = "private-proposal-test"
	t.Cleanup(func() { kanbanApp = previousApp })

	launches := 0
	startGoalThreadAsyncPrev := startGoalThreadAsync
	startGoalThreadAsync = func(_ *kanbanBoardApp, _ string) {
		launches++
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
	launched, ok := response["agentThread"].(scoutAgentThread)
	if !ok || launched.Mode != "goal" || launches != 1 {
		t.Fatalf("response=%#v launches=%d, want one private goal launch", response, launches)
	}

	// The acceptance signal carries the lane so acceptance is measurable per lane.
	if !signalRecordedWithLane(kanbanApp, signalEventRouterProposalAccepted, approvalLaneStandard) {
		t.Fatalf("no %s signal carrying lane=%q was recorded", signalEventRouterProposalAccepted, approvalLaneStandard)
	}

	// A retry recovers the accepted operation rather than launching again.
	replayed, err := kanbanApp.resolveScoutChatProposal(context.Background(), user, thread.ID, scoutChatProposalAction{
		Action:    "accepted",
		MessageID: card.ID,
	})
	if err != nil || launches != 1 || replayed["reconciled"] != true {
		t.Fatalf("replayed accept err=%v launches=%d response=%#v, want exact reconciliation", err, launches, replayed)
	}
	replayedThread, ok := replayed["agentThread"].(scoutAgentThread)
	if !ok || replayedThread.ID != launched.ID {
		t.Fatalf("replayed work=%#v, want original %s", replayed["agentThread"], launched.ID)
	}
}

// Keyless deploys never attempt a router turn, so propose_goal never fires.
// Conversational Scout also fails honestly instead of substituting an
// unrelated deterministic memory-search answer for the missing model turn.
func TestScoutChatGoalRunKeylessNeverProposes(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = ""
	t.Cleanup(func() { kanbanApp = previousApp })

	swapOpenAITextResponder(t, func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("keyless deploys must never attempt a core provider turn")
		return "", nil
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
		t.Fatalf("keyless append err=%v, want persisted unavailable outcome", err)
	}
	if response["intentOutcome"] != string(conversationIntentUnavailable) || response["proposal"] != nil || response["agentThread"] != nil {
		t.Fatalf("response=%#v, want unavailable without work", response)
	}
	saved, _, readErr := kanbanApp.scoutChatThreadByID(user.Email, private.ID)
	if readErr != nil {
		t.Fatalf("read keyless thread: %v", readErr)
	}
	if len(saved.Messages) != 2 || saved.Messages[0].Role != "user" || saved.Messages[1].Role != "scout" || saved.Messages[1].IntentOutcome != string(conversationIntentUnavailable) {
		t.Fatalf("saved messages=%#v, want persisted human request plus unavailable outcome", saved.Messages)
	}
	if saved.Messages[0].Text == "" || !strings.Contains(saved.Messages[1].Text, "couldn't answer safely") {
		t.Fatalf("saved messages=%#v, want the request and honest provider error", saved.Messages)
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
