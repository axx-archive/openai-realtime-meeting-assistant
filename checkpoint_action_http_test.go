package main

// The /artifacts/action door's checkpoint teeth (Wave 5 fix): the handler must
// decode {choice} and forward it through resumeApprovedGoalWithChoice. Before
// this fix the payload struct silently dropped choice and the goal branch
// called resumeApprovedGoal (choice=""), so every negative option — "hold the
// package" at ship_approval, "send back for changes" at founder_pass — decayed
// into a silent PROCEED. The engine tests exercise the app seam directly;
// these two drive the REAL HTTP handler end to end.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func postArtifactAction(t *testing.T, cookies []*http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/artifacts/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	artifactRunnerActionHandler(recorder, req)
	return recorder
}

// A hold-action choice posted through POST /artifacts/action keeps the goal
// PARKED with Held=true — tapping "hold the package" actually holds, and a
// hold is never recorded as a human sign-off (no share-unlocking approval
// stamp).
func TestArtifactActionHTTPHoldChoiceKeepsGoalParked(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	installFakeResponder(t, goalResponderRoutes{})
	installFakeChildRunner(t)

	thread, err := kanbanApp.launchGoalThread(goalLaunchSpec{
		Objective:    "Probe the HTTP hold door",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "process_probe",
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	kanbanApp.runGoalThread(thread.Artifact.ID)
	waitForGoalStage(t, kanbanApp, thread.Artifact.ID, goalStateApproval)

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	recorder := postArtifactAction(t, cookies, fmt.Sprintf(`{"id":%q,"action":"approve","choice":"hold"}`, thread.Artifact.ID))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("hold choice status=%d body=%s, want 202", recorder.Code, recorder.Body.String())
	}

	artifact := mustArtifact(t, kanbanApp, thread.Artifact.ID)
	if artifact.Metadata["currentStage"] != goalStateApproval || artifact.Metadata["reviewGate"] != "approval_required" {
		t.Fatalf("hold choice un-parked the goal: %v", artifact.Metadata)
	}
	plan, ok := decodeGoalPlan(artifact.Metadata["goalPlan"])
	if !ok {
		t.Fatal("goal plan missing after the HTTP hold")
	}
	if plan.Checkpoint == nil || !plan.Checkpoint.Held || plan.Checkpoint.ResolvedAt != "" {
		t.Fatalf("HTTP hold choice did not hold the checkpoint: %+v", plan.Checkpoint)
	}
	if artifact.Metadata[artifactHumanApprovedAtKey] != "" || artifact.Metadata[artifactHumanApprovedByKey] != "" {
		t.Fatalf("a hold must not stamp the durable human-approval record: %v", artifact.Metadata)
	}

	// The subsequent proceed-action choice, through the same HTTP door, is the
	// only way forward.
	recorder = postArtifactAction(t, cookies, fmt.Sprintf(`{"id":%q,"action":"approve","choice":"ship"}`, thread.Artifact.ID))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("proceed after hold status=%d body=%s, want 202", recorder.Code, recorder.Body.String())
	}
	plan = waitForGoalStage(t, kanbanApp, thread.Artifact.ID, goalStateVerified)
	if plan.Checkpoint == nil || plan.Checkpoint.Choice != "ship" || plan.Checkpoint.ResolvedAt == "" {
		t.Fatalf("proceed choice did not resolve the held checkpoint: %+v", plan.Checkpoint)
	}
}

// A revise-action choice posted through POST /artifacts/action re-queues the
// option's target stage with the choice text as revision notes and re-parks
// the checkpoint — the founder's send-back notes reach the pipeline.
func TestArtifactActionHTTPReviseChoiceRequeuesTarget(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	installFakeResponder(t, goalResponderRoutes{})
	launched := installFakeChildRunner(t)
	registerReviseProbeForTest(t, "process_http_revise_probe")

	thread, err := kanbanApp.launchGoalThread(goalLaunchSpec{
		Objective:    "Probe the HTTP revise door",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "process_http_revise_probe",
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	kanbanApp.runGoalThread(thread.Artifact.ID)
	waitForGoalStage(t, kanbanApp, thread.Artifact.ID, goalStateApproval)

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	recorder := postArtifactAction(t, cookies, fmt.Sprintf(`{"id":%q,"action":"approve","choice":"send back — HTTP door check"}`, thread.Artifact.ID))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("revise choice status=%d body=%s, want 202", recorder.Code, recorder.Body.String())
	}

	// The target re-queued (a second w1 launch carrying the notes) and the
	// checkpoint RE-PARKED, unresolved — the send-back was not a proceed.
	plan := waitForGoalStage(t, kanbanApp, thread.Artifact.ID, goalStateApproval)
	if plan.Checkpoint == nil || plan.Checkpoint.StageID != "pass" || plan.Checkpoint.ResolvedAt != "" {
		t.Fatalf("checkpoint did not re-park after the HTTP send-back: %+v", plan.Checkpoint)
	}
	if len(*launched) != 2 || (*launched)[1].subtaskID != "w1" {
		t.Fatalf("launched children=%+v, want the initial w1 + one send-back redo", *launched)
	}
	if !strings.Contains((*launched)[1].query, "send back — HTTP door check") {
		t.Fatalf("redo query does not carry the HTTP choice notes:\n%s", (*launched)[1].query)
	}

	// A send-back is NOT a sign-off: no durable human-approval stamp, no
	// "approved · sent" fan-out — the founder asked for changes.
	updated, _ := kanbanApp.osArtifactByID(thread.Artifact.ID)
	if updated.Metadata["humanApprovedBy"] != "" || updated.Metadata["humanApprovedAt"] != "" {
		t.Fatalf("send-back stamped human approval: %v", updated.Metadata)
	}
	if plan.Checkpoint.LastAction != processCheckpointActionRevise {
		t.Fatalf("checkpoint lastAction=%q, want revise", plan.Checkpoint.LastAction)
	}
}
