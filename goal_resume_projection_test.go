package main

import (
	"strings"
	"testing"
)

// A production retry exposed a split-brain root card: the plan and card
// metadata were already running, while Inspect work still opened the previous
// terminal "needs attention" body and its active Blocker section. The retry
// transition must publish one truthful running projection while retaining the
// saved draft and evidence that make the retry useful.
func TestResumeBlockedGoalProjectsRunningBodyAndRetainsDraftEvidence(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	installFakeResponder(t, goalResponderRoutes{})
	installFakeChildRunner(t)
	registerReviseProbeForTest(t, "process_resume_projection_probe")

	thread, err := launchConversationOwnedGoalForTest(t, kanbanApp, goalLaunchSpec{
		Objective:    "Build an evidence-grounded presentation",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "process_resume_projection_probe",
	})
	if err != nil {
		t.Fatalf("launch goal: %v", err)
	}
	parentID := thread.Artifact.ID
	kanbanApp.runGoalThread(parentID)
	waitForGoalStage(t, kanbanApp, parentID, goalStateApproval)

	parent, _ := kanbanApp.osArtifactByID(parentID)
	blocked, ok := decodeGoalPlan(parent.Metadata["goalPlan"])
	if !ok {
		t.Fatal("goal plan missing before block fixture")
	}
	writer := blocked.subtaskByID("w1")
	if writer == nil || strings.TrimSpace(writer.ArtifactID) == "" {
		t.Fatalf("writer=%+v, want its completed draft artifact", writer)
	}
	draftID := writer.ArtifactID
	draftBefore, found := kanbanApp.osArtifactByID(draftID)
	if !found {
		t.Fatalf("draft %q not found", draftID)
	}

	priorBlocker := "external evidence receipt did not cover every cited source"
	priorGap := "one proof point still needs a provider-backed citation"
	writer.Status = subtaskBlocked
	writer.Revisions = goalMaxRevisions
	blocked.State = goalStateBlocked
	blocked.Blocker = priorBlocker
	blocked.Report.Gap = priorGap
	blocked.Report.ArtifactIDs = []string{draftID}
	blocked.Report.DeliverableArtifactID = draftID
	newGoalEngine(kanbanApp).finish(&blocked, parentID)

	terminal, _ := kanbanApp.osArtifactByID(parentID)
	if !strings.Contains(terminal.Text, "Status: needs attention") || !strings.Contains(terminal.Text, "## Blocker") || !strings.Contains(terminal.Text, priorBlocker) {
		t.Fatalf("terminal fixture is not a needs-attention body: %q", terminal.Text)
	}

	// Hold the retried writer in flight so the assertion observes the exact
	// synchronous resume projection returned to the client, before any later
	// child completion can recompose the goal again.
	previousChildStart := startAgentThreadAsync
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousChildStart })

	if err := kanbanApp.resumeBlockedGoal(parentID, "AJ"); err != nil {
		t.Fatalf("resume blocked goal: %v", err)
	}
	resumed, _ := kanbanApp.osArtifactByID(parentID)
	resumedPlan, ok := decodeGoalPlan(resumed.Metadata["goalPlan"])
	if !ok {
		t.Fatal("resumed goal plan missing")
	}
	if resumedPlan.State != goalStateExecute || resumed.Metadata["goalStatus"] != "running" || resumed.Metadata["threadStatus"] != "running" {
		t.Fatalf("resume projection state=%q goalStatus=%q threadStatus=%q, want one running projection", resumedPlan.State, resumed.Metadata["goalStatus"], resumed.Metadata["threadStatus"])
	}
	if !strings.Contains(resumed.Text, "Status: running") || strings.Contains(resumed.Text, "Status: needs attention") || strings.Contains(resumed.Text, "## Blocker") || strings.Contains(resumed.Text, priorBlocker) {
		t.Fatalf("Inspect work body still projects the terminal blocker: %q", resumed.Text)
	}
	if resumedPlan.Blocker != "" || strings.TrimSpace(resumed.Metadata["goalBlocker"]) != "" {
		t.Fatalf("active blocker survived resume: plan=%q metadata=%q", resumedPlan.Blocker, resumed.Metadata["goalBlocker"])
	}

	resumedWriter := resumedPlan.subtaskByID("w1")
	if resumedWriter == nil || resumedWriter.Status != subtaskRunning || strings.TrimSpace(resumedWriter.ArtifactID) == "" || resumedWriter.Review == nil || !strings.Contains(resumedWriter.Review.Reasons, priorBlocker) {
		t.Fatalf("retry lost the running stage or prior diagnostic evidence: %+v", resumedWriter)
	}
	if resumedPlan.Report.DeliverableArtifactID != draftID || len(resumedPlan.Report.ArtifactIDs) != 1 || resumedPlan.Report.ArtifactIDs[0] != draftID || resumedPlan.Report.Gap != priorGap {
		t.Fatalf("retry lost report evidence: %+v", resumedPlan.Report)
	}
	if !strings.Contains(resumed.Text, "## Draft saved") || !strings.Contains(resumed.Text, draftID) || !strings.Contains(resumed.Text, priorGap) {
		t.Fatalf("running body lost the saved-draft handoff: %q", resumed.Text)
	}
	draftAfter, found := kanbanApp.osArtifactByID(draftID)
	if !found || draftAfter.Text != draftBefore.Text {
		t.Fatalf("retry mutated prior draft: found=%v before=%q after=%q", found, draftBefore.Text, draftAfter.Text)
	}
}
