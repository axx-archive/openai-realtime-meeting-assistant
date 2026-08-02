package main

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLegacyCodexProposalSTRIDEFenceRejectsMintConfirmLaunchAndTicker(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	id := "codex-proposal-pre-stride"
	appendTickerProposal(t, app, id, map[string]string{
		"status":     codexProposalStatusProposed,
		"proposedBy": "AJ",
	})
	if app.strideRuntime != nil {
		_ = app.strideRuntime.Close()
	}
	app.strideRuntime = &STRIDERuntime{config: STRIDERuntimeConfig{ProductPreviewEnabled: true}}

	var providerCalls atomic.Int64
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { providerCalls.Add(1) }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	if _, _, err := app.proposeCodexTask(map[string]any{
		"title": "Must use Suggested Work",
		"mode":  "research",
		"query": "Research this through canonical STRIDE work.",
	}, "AJ"); !errors.Is(err, ErrLegacyCodexProposalRetired) {
		t.Fatalf("legacy mint error=%v, want retired fence", err)
	}
	if _, launched, err := app.resolveCodexProposal(id, codexProposalActionConfirm, "Tom", "tom@shareability.com"); !errors.Is(err, ErrLegacyCodexProposalRetired) || launched {
		t.Fatalf("unauthorized legacy confirm launched=%v err=%v, want retired fence", launched, err)
	}
	entry, ok := app.memory.entryByKindAndID(meetingMemoryKindCodexProposal, id)
	if !ok || entry.Metadata["status"] != codexProposalStatusProposed || entry.Metadata["launchClaimId"] != "" {
		t.Fatalf("STRIDE-fenced confirm mutated proposal: ok=%v metadata=%#v", ok, entry.Metadata)
	}
	if _, err := app.launchApprovedProposal(entry, "Tom", "tom@shareability.com", nil, launchFunnelLineage{}); !errors.Is(err, ErrLegacyCodexProposalRetired) {
		t.Fatalf("direct legacy launch error=%v, want retired fence", err)
	}
	if got := app.runWorkflowTickerOnce(time.Now()); got != 0 {
		t.Fatalf("STRIDE-fenced ticker launched=%d, want 0", got)
	}
	if snapshot := app.codexProposalsSnapshot(codexProposalHistoryLimit); len(snapshot) != 0 {
		t.Fatalf("STRIDE mode exposed legacy org-wide proposals: %#v", snapshot)
	}
	if app.proposalAwaitingAction(id) {
		t.Fatal("STRIDE mode left a legacy broadcast notification actionable")
	}
	if got := providerCalls.Load(); got != 0 {
		t.Fatalf("providerCalls=%d, want 0", got)
	}
}

func TestLegacyCodexProposalClaimSurvivesRestartAndConcurrentReplay(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	id := "codex-proposal-crash-after-launch-before-stamps"
	claimID := legacyCodexProposalLaunchClaim(id)
	appendTickerProposal(t, app, id, map[string]string{
		"status":           codexProposalStatusConfirmed,
		"confirmedBy":      "AJ",
		"confirmedByEmail": "aj@shareability.com",
		"resolvedAt":       time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339Nano),
		"launchClaimId":    claimID,
		"launchState":      codexProposalLaunchClaimed,
	})
	if err := app.Close(); err != nil {
		t.Fatalf("close before simulated restart: %v", err)
	}

	var providerCalls atomic.Int64
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { providerCalls.Add(1) }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	restarted := newKanbanBoardApp()
	t.Cleanup(func() { _ = restarted.Close() })

	const replays = 16
	var wait sync.WaitGroup
	wait.Add(replays)
	for index := 0; index < replays; index++ {
		go func() {
			defer wait.Done()
			if got := restarted.runWorkflowTickerOnce(time.Now()); got != 0 {
				t.Errorf("replay launched=%d, want 0", got)
			}
		}()
	}
	wait.Wait()

	if _, launched, err := restarted.resolveCodexProposal(id, codexProposalActionConfirm, "AJ", "aj@shareability.com"); err != nil || launched {
		t.Fatalf("confirmed replay launched=%v err=%v, want idempotent no-op", launched, err)
	}
	entry, ok := restarted.memory.entryByKindAndID(meetingMemoryKindCodexProposal, id)
	if !ok || entry.Metadata["launchClaimId"] != claimID || entry.Metadata["launchState"] != codexProposalLaunchClaimed || entry.Metadata["threadId"] != "" {
		t.Fatalf("restart changed stable ambiguous claim: ok=%v metadata=%#v", ok, entry.Metadata)
	}
	if got := providerCalls.Load(); got != 0 {
		t.Fatalf("providerCalls=%d, want 0 across restart/replay", got)
	}
}
