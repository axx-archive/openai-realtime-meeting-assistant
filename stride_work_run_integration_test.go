package main

import (
	"context"
	"testing"
	"time"
)

func launchSTRIDEWorkRunIntegrationFixture(t *testing.T, mode string) (*kanbanBoardApp, *userAccount, scoutChatThreadRecord, scoutAgentThread) {
	t.Helper()
	app, user, thread, source, binding := newAcceptedPublicWorkFixture(t)
	previousStarter := startAgentThreadAsync
	t.Cleanup(func() { startAgentThreadAsync = previousStarter })
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	proposalID := "proposal-work-run-" + mode
	proposal := scoutRouterProposal{
		Kind: scoutRouterProposalKindWorkstream, IntentOutcome: string(conversationIntentApprovalRequired), EffectClass: "expanded_audience",
		Mode: mode, Objective: "Create the exact approved " + mode + " deliverable", Query: source.Text,
		Lane: approvalLaneStandard, WeightLabel: scoutProposalWeightQuickPass, Summary: "Work prepared", Status: "accepted",
	}
	var err error
	thread, err = app.commitScoutChatThreadMessages(user.Email, thread.ID, scoutChatMessageRecord{
		ID: proposalID, Kind: scoutChatMessageKindProposal, Role: "scout", Proposal: &proposal,
		CausedByMessageID: source.ID, CreatedAt: source.CreatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := app.startAcceptedPublicScoutWork(context.Background(), user, thread, proposalID, proposal, nil, binding)
	if err != nil {
		t.Fatal(err)
	}
	return app, user, thread, response["agentThread"].(scoutAgentThread)
}

func TestWorkRunStartupReconcilesTerminalArtifactCommitGapWithoutDuplicateCard(t *testing.T) {
	app, _, _, work := launchSTRIDEWorkRunIntegrationFixture(t, "research")
	committed, _, err := app.updateOSArtifactWithMetadata(work.Artifact.ID, "", "# Durable result\n\nCommitted before process loss.", STRIDEWorkAgentResearcher, map[string]string{
		"status": "complete", "threadStatus": "complete", "goalStatus": "verified", "latestThreadRun": work.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := app.workRuns.SideCard(work.ID)
	if err != nil || terminalSTRIDEWorkRunStatus(before.Status) {
		t.Fatalf("pre-reconcile card=%+v err=%v", before, err)
	}

	restarted, err := NewSTRIDEWorkRunRepository(strideWorkRunPath())
	if err != nil {
		t.Fatal(err)
	}
	app.workRuns = restarted
	if err := app.reconcileSTRIDEWorkRunTerminalGaps(); err != nil {
		t.Fatal(err)
	}
	after, err := app.workRuns.SideCard(work.ID)
	if err != nil || after.Status != "completed" || len(after.ArtifactLineage) != 1 {
		t.Fatalf("reconciled card=%+v err=%v", after, err)
	}
	wantAt, err := time.Parse(time.RFC3339Nano, committed.Metadata["updatedAt"])
	if err != nil {
		t.Fatal(err)
	}
	if got := after.Activity[len(after.Activity)-1].OccurredAt; !got.Equal(wantAt) {
		t.Fatalf("terminal activity at=%s, want exact durable artifact update %s", got, wantAt)
	}
	eventsBefore, _ := app.workRuns.Events(work.ID)
	if err := app.reconcileSTRIDEWorkRunTerminalGaps(); err != nil {
		t.Fatal(err)
	}
	eventsAfter, _ := app.workRuns.Events(work.ID)
	if len(eventsAfter) != len(eventsBefore) {
		t.Fatalf("reconciliation duplicated one Work card/run: before=%d after=%d", len(eventsBefore), len(eventsAfter))
	}
}

func TestAcceptedPublicWorkRunUsesFixedAssignmentsAndReplayAcrossRestart(t *testing.T) {
	for _, test := range []struct {
		mode, output, specialist string
	}{
		{mode: "research", output: "research", specialist: STRIDEWorkAgentResearcher},
		{mode: "design", output: "presentation", specialist: STRIDEWorkAgentPresenter},
	} {
		t.Run(test.mode, func(t *testing.T) {
			app, user, _, work := launchSTRIDEWorkRunIntegrationFixture(t, test.mode)
			card, err := app.workRuns.SideCard(work.ID)
			if err != nil {
				t.Fatal(err)
			}
			if card.Run.OutputKind != test.output || card.Run.AccountableAgent != STRIDEWorkAgentScout || card.Status != "running" {
				t.Fatalf("launch replay=%+v", card)
			}
			agents := map[string]string{}
			for _, assignment := range card.Assignments {
				agents[assignment.Assignment.Agent] = assignment.Status
			}
			if agents[STRIDEWorkAgentScout] != "running" || agents[test.specialist] != "assigned" || len(agents) != 2 {
				t.Fatalf("fixed assignments=%v", agents)
			}
			eventsBefore, err := app.workRuns.Events(work.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := app.ensureSTRIDEPublicWorkRun(work); err != nil {
				t.Fatal(err)
			}
			eventsAfter, _ := app.workRuns.Events(work.ID)
			if len(eventsAfter) != len(eventsBefore) {
				t.Fatalf("idempotent launch appended events: before=%d after=%d", len(eventsBefore), len(eventsAfter))
			}

			channel, _, err := app.scoutChatThreadByID(user.Email, work.Artifact.Metadata["originId"])
			if err != nil {
				t.Fatal(err)
			}
			projected := app.projectScoutChatThreadForViewer(user.Email, channel)
			found := false
			for _, message := range projected.Messages {
				if message.Thread != nil && message.Thread.ID == work.ID {
					found = message.Thread.WorkRunRequired && message.Thread.WorkRun != nil && message.Thread.WorkRun.Status == card.Status && message.Thread.WorkRun.LastSequence == card.LastSequence
				}
			}
			if !found {
				t.Fatalf("customer card did not hydrate from WorkRun replay: %+v", projected.Messages)
			}
			currentRepository := app.workRuns
			app.workRuns = nil
			missingReplay := app.projectScoutChatThreadForViewer(user.Email, channel)
			app.workRuns = currentRepository
			missingFound := false
			for _, message := range missingReplay.Messages {
				if message.Thread != nil && message.Thread.ID == work.ID {
					missingFound = message.Thread.WorkRunRequired && message.Thread.WorkRun == nil
				}
			}
			if !missingFound {
				t.Fatalf("canonical card lost its durable WorkRun-required marker when replay was unavailable: %+v", missingReplay.Messages)
			}

			restarted, err := NewSTRIDEWorkRunRepository(strideWorkRunPath())
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := restarted.SideCard(work.ID)
			if err != nil || replayed.LastSequence != card.LastSequence || replayed.Status != card.Status {
				t.Fatalf("restart replay=%+v err=%v", replayed, err)
			}
		})
	}
}

func TestWorkRunProgressAndTerminalLineageFollowDurableArtifactCommits(t *testing.T) {
	app, _, _, work := launchSTRIDEWorkRunIntegrationFixture(t, "research")
	app.persistAgentThreadProgress(work, AgentProgress{
		Stage: "collect_evidence", ProgressPercent: 35, GoalStatus: "running", ReviewGate: "pending", Note: "Verified the first source set",
	})
	progressCard, err := app.workRuns.SideCard(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	assignment, status, ok := strideWorkRunSpecialistAssignment(progressCard)
	if !ok || assignment.Agent != STRIDEWorkAgentResearcher || status != "running" || progressCard.Phase != STRIDEWorkRunEvidence {
		t.Fatalf("progress replay=%+v", progressCard)
	}

	committed, _, err := app.updateOSArtifactWithMetadata(work.Artifact.ID, "", "# Exact research result\n\nCommitted evidence.", STRIDEWorkAgentResearcher, map[string]string{
		"status": "complete", "threadStatus": "complete", "goalStatus": "verified",
	})
	if err != nil {
		t.Fatal(err)
	}
	app.recordSTRIDEWorkRunTerminal(work, committed, "complete")
	terminal, err := app.workRuns.SideCard(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.Status != "completed" || len(terminal.ArtifactLineage) != 1 {
		t.Fatalf("terminal replay=%+v", terminal)
	}
	lineage := terminal.ArtifactLineage[0].Artifact
	if lineage.ID != committed.ID || lineage.Revision != int64(artifactVersion(committed)) || lineage.Digest != artifactCapabilityDigest(committed) {
		t.Fatalf("lineage=%+v committed version=%d digest=%s", lineage, artifactVersion(committed), artifactCapabilityDigest(committed))
	}
	eventsBefore, _ := app.workRuns.Events(work.ID)
	app.recordSTRIDEWorkRunTerminal(work, committed, "complete")
	eventsAfter, _ := app.workRuns.Events(work.ID)
	if len(eventsAfter) != len(eventsBefore) {
		t.Fatalf("terminal retry appended duplicate events: before=%d after=%d", len(eventsBefore), len(eventsAfter))
	}
	restarted, err := NewSTRIDEWorkRunRepository(strideWorkRunPath())
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := restarted.SideCard(work.ID)
	if err != nil || replayed.Status != terminal.Status || replayed.LastSequence != terminal.LastSequence || len(replayed.ArtifactLineage) != 1 {
		t.Fatalf("terminal restart replay=%+v err=%v", replayed, err)
	}
}
