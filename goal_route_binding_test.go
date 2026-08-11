package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// launchConversationOwnedGoalForTest gives engine-mechanics fixtures the same
// immutable private-chat provenance production receives from the five-way
// router. It is deliberately test-only; no production bypass marks a plan
// trusted without re-reading this source at every restart.
func launchConversationOwnedGoalForTest(t *testing.T, app *kanbanBoardApp, spec goalLaunchSpec) (scoutAgentThread, error) {
	t.Helper()
	requester := normalizeAccountEmail(firstNonEmptyString(spec.Origin["requestedBy"], spec.CreatedBy, "aj@shareability.com"))
	var thread scoutChatThreadRecord
	var err error
	if strings.TrimSpace(spec.Origin["originKind"]) == agentThreadOriginPrivateThread && strings.TrimSpace(spec.Origin["originId"]) != "" {
		thread, _, err = app.scoutChatThreadByID(requester, spec.Origin["originId"])
	}
	if thread.ID == "" {
		thread, err = app.createScoutChatThread(requester, firstNonEmptyString(spec.CreatedBy, requester), "Goal route fixture", scoutChatVisibilityPrivate)
	}
	if err != nil {
		return scoutAgentThread{}, err
	}
	now := time.Now().UTC()
	operation := conversationTurnOperation{
		ID:         "test-goal-route-" + sha256Hex([]byte(t.Name() + "\x00" + thread.ID + "\x00" + spec.Objective))[:24],
		BodyDigest: sha256Hex([]byte("test-goal-route/v1\x00" + spec.Objective)),
	}
	message := scoutChatMessageRecord{
		ID: "test-goal-source-" + sha256Hex([]byte(operation.ID))[:24], Kind: "message", Role: "user", Text: spec.Objective,
		AuthorName: firstNonEmptyString(spec.CreatedBy, "AJ"), AuthorEmail: requester, CreatedAt: now.Format(time.RFC3339Nano),
		SourceOperationID: operation.ID, SourceOperationDigest: operation.BodyDigest,
	}
	thread.Messages = append(thread.Messages, message)
	thread.UpdatedAt = now.Format(time.RFC3339Nano)
	if err := app.saveScoutChatThread(thread); err != nil {
		return scoutAgentThread{}, err
	}
	_, binding, err := scoutChatSourceWindow(thread, message.ID)
	if err != nil {
		return scoutAgentThread{}, err
	}
	spec.Origin = map[string]string{
		"originKind": agentThreadOriginPrivateThread, "originId": thread.ID, "originSurface": "chat:" + thread.ID,
		"requestedBy": requester, "sourceMessageId": message.ID, "sourceMessageDigest": binding.MessageDigest, "sourceWindowDigest": binding.WindowDigest,
		"operationId": operation.ID, "operationBodyDigest": operation.BodyDigest,
	}
	return app.launchGoalThread(spec)
}

func TestGoalBootQuarantinesUnmarkedLegacyToolAndProcessRoutes(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "goal-route-test")
	var providerCalls atomic.Int32
	swapOpenAITextResponder(t, func(context.Context, string, openAITextRequest) (string, error) {
		providerCalls.Add(1)
		return `{"subtasks":[]}`, nil
	})

	fixtures := []struct {
		name         string
		toolTemplate string
		processID    string
	}{
		{name: "known legacy tool", toolTemplate: "deep_research"},
		{name: "known legacy process", toolTemplate: packagingStudioProcessID, processID: packagingStudioProcessID},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			app := newIsolatedKanbanBoardApp(t)
			plan := goalPlan{
				PlanVersion: goalPlanVersion, GoalID: "legacy-" + strings.ReplaceAll(fixture.name, " ", "-"),
				Objective: "Legacy client-selected work", CreatedBy: "AJ", RequestedBy: "aj@shareability.com",
				Authority: codexJobAuthorityWorkspaceWrite, ToolTemplate: fixture.toolTemplate, ProcessID: fixture.processID,
				State: goalStateIdentify, Gate: goalGate{Status: "pending"}, Verification: goalVerification{Verdict: "pending"},
			}
			raw, err := json.Marshal(plan)
			if err != nil {
				t.Fatal(err)
			}
			artifact, _, err := app.createOSArtifactWithMetadata("workflow", plan.Objective, "legacy goal", "AJ", map[string]string{
				"mode": "goal", "threadId": plan.GoalID, "goalPlan": string(raw), "status": "running", "threadStatus": "running",
			})
			if err != nil {
				t.Fatal(err)
			}

			// A fresh app exercises the same decode-and-reconcile boundary used at
			// boot. Registry membership alone must not revive this assignment.
			restarted := newKanbanBoardApp()
			restarted.reconcileGoalThread(artifact.ID)
			got := mustGoalPlan(t, restarted, artifact.ID)
			if got.State != goalStateBlocked || !strings.Contains(got.Blocker, "no verified conversation route") {
				t.Fatalf("legacy plan state=%q blocker=%q, want fail-closed quarantine", got.State, got.Blocker)
			}
			for _, entry := range restarted.memory.snapshot(0) {
				if entry.Metadata["goalParentId"] == artifact.ID {
					t.Fatalf("legacy route created downstream child %s", entry.ID)
				}
			}
		})
	}
	if providerCalls.Load() != 0 {
		t.Fatalf("legacy routes made %d provider calls, want zero", providerCalls.Load())
	}
}

func TestConversationOwnedGoalRouteSurvivesBootReconciliation(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "goal-route-test")
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "goal-route-test"

	originalGoalStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = originalGoalStart })
	originalAgentStart := startAgentThreadAsync
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = originalAgentStart })

	var routerCalls atomic.Int32
	var goalCalls atomic.Int32
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow == "scout_route" {
			routerCalls.Add(1)
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
				Outcome: string(conversationIntentStartPrivateWork), Route: "tool_run", ToolID: "one_pager",
				Objective: "Create the source-bound Aurora investor one-pager",
			}), nil
		}
		if strings.Contains(strings.ToLower(request.Instructions), "goal decomposer") {
			goalCalls.Add(1)
			return `{"subtasks":[{"id":"st-1","title":"Write the investor one-pager","mode":"artifacts","authority":"read_only","dependsOn":[]}]}`, nil
		}
		t.Fatalf("unexpected provider request workflow=%q instructions=%q", request.Workflow, request.Instructions)
		return "", nil
	})

	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Aurora", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("aj@shareability.com")
	operation := conversationTurnOperation{ID: "conversation-goal-route-boot-0001", BodyDigest: sha256Hex([]byte("source-bound one-pager request"))}
	response, err := app.appendScoutChatThreadMessage(withConversationTurnOperation(context.Background(), operation), user, thread.ID, "Create an investor one-pager for Aurora", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	work, ok := response["agentThread"].(scoutAgentThread)
	if !ok || work.Artifact.ID == "" {
		t.Fatalf("conversation did not create goal work: %#v", response)
	}
	before := mustGoalPlan(t, app, work.Artifact.ID)
	if before.RouteReceipt == nil || !isHexDigest(before.RouteReceipt.Digest) {
		t.Fatalf("conversation route receipt missing: %+v", before.RouteReceipt)
	}

	// Reload the durable stores and run the production boot reconciler seam.
	restarted := newKanbanBoardApp()
	restarted.reconcileGoalThread(work.Artifact.ID)
	after := mustGoalPlan(t, restarted, work.Artifact.ID)
	if after.State == goalStateBlocked || after.RouteReceipt == nil {
		t.Fatalf("verified current route did not survive restart: state=%q blocker=%q", after.State, after.Blocker)
	}
	// The deterministic intent guard may recognize this catalog request without
	// spending a router call; either way boot admits exactly one goal decompose.
	if routerCalls.Load() > 1 || goalCalls.Load() != 1 {
		t.Fatalf("router calls=%d goal calls=%d, want at most one router call and one goal call", routerCalls.Load(), goalCalls.Load())
	}
}

func TestLegacyGoalChildCannotBecomeIndependentFollowUpAuthority(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	legacy := goalPlan{
		PlanVersion: goalPlanVersion, GoalID: "legacy-child-parent", Objective: "Legacy deck",
		CreatedBy: "AJ", RequestedBy: "aj@shareability.com", Authority: codexJobAuthorityWorkspaceWrite,
		ToolTemplate: "one_pager", State: goalStateBlocked, Gate: goalGate{Status: subtaskBlocked},
		Subtasks: []goalSubtask{{ID: "writer", Title: "Write the deck", Mode: "artifacts", Runner: agentRunnerOpenAIText, Status: subtaskComplete}},
	}
	raw, _ := json.Marshal(legacy)
	parent, _, err := app.createOSArtifactWithMetadata("workflow", legacy.Objective, "legacy parent", "AJ", map[string]string{
		"mode": "goal", "threadId": legacy.GoalID, "goalPlan": string(raw), "status": "error", "threadStatus": "error",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := app.createOSArtifactWithMetadata("artifacts", "Legacy writer", "legacy child bytes", "AJ", map[string]string{
		"source": "scout_thread", "mode": "artifacts", "threadId": "legacy-child", "threadQuery": "Legacy writer",
		"goalParentId": parent.ID, "goalSubtaskId": "writer", "toolTemplate": "one_pager", "goalDeliverable": "true",
		"assignedRunner": agentRunnerOpenAIText, "authority": codexJobAuthorityReadOnly,
		"requestedBy": "aj@shareability.com", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.agentThreadProviderContext(context.Background(), scoutAgentThread{ID: "legacy-child", Mode: "artifacts", Artifact: child}); err == nil {
		t.Fatal("legacy goal child reached provider-context admission")
	}
	if _, err := app.dispatchArtifactFollowUp(child.ID, "revise it", "aj@shareability.com", nil); err == nil {
		t.Fatal("legacy goal child reached follow-up execution")
	}
	stored, _ := app.osArtifactByID(child.ID)
	if stored.Text != child.Text || agentThreadStatusValue(stored) != "complete" {
		t.Fatalf("legacy child changed despite refusal: %+v", stored)
	}
}

func TestGoalChildFollowUpStopsWhenParentConversationSourceChanges(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	installFakeResponder(t, goalResponderRoutes{})
	originalAgentStart := startAgentThreadAsync
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = originalAgentStart })

	parent, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Write the source-bound Aurora one-pager", CreatedBy: "aj@shareability.com", ToolTemplate: "one_pager",
	})
	if err != nil {
		t.Fatal(err)
	}
	app.runGoalThread(parent.Artifact.ID)
	plan := mustGoalPlan(t, app, parent.Artifact.ID)
	if len(plan.Subtasks) != 1 || plan.Subtasks[0].ArtifactID == "" {
		t.Fatalf("goal did not create its governed child: %+v", plan.Subtasks)
	}
	child, _ := app.osArtifactByID(plan.Subtasks[0].ArtifactID)
	receipt := plan.RouteReceipt
	thread, _, err := app.scoutChatThreadByID(receipt.Requester, receipt.OriginID)
	if err != nil {
		t.Fatal(err)
	}
	for index := range thread.Messages {
		if thread.Messages[index].ID == receipt.SourceMessageID {
			thread.Messages[index].Text = "changed after launch"
		}
	}
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	before, _ := app.osArtifactByID(child.ID)
	if _, err := app.dispatchArtifactFollowUp(child.ID, "revise it", "aj@shareability.com", nil); err == nil {
		t.Fatal("changed parent source admitted a child follow-up")
	}
	after, _ := app.osArtifactByID(child.ID)
	if after.Text != before.Text || agentThreadStatusValue(after) != agentThreadStatusValue(before) {
		t.Fatalf("changed-source child mutated before refusal: before=%+v after=%+v", before, after)
	}
}

func TestGoalChildFoldRejectsStaleRevisionAfterRestart(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	previousGoalStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStart })
	previousAgentStart := startAgentThreadAsync
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousAgentStart })

	parent, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Research the source-bound Aurora market", CreatedBy: "aj@shareability.com", ToolTemplate: "deep_research",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := mustGoalPlan(t, app, parent.Artifact.ID)
	engine := newGoalEngine(app)
	if err := engine.prepareGoalRoute(&plan, parent.Artifact.ID); err != nil {
		t.Fatal(err)
	}
	receipt := plan.RouteReceipt
	origin := goalRouteChildBindingMetadata(&plan)
	childSpec := func(query string) agentThreadGoalSpec {
		return agentThreadGoalSpec{
			Objective: query, ToolTemplate: "deep_research", Deliverable: true, RequestedBy: receipt.Requester, Authority: codexJobAuthorityReadOnly,
			ParentGoalID: parent.Artifact.ID, SubtaskID: "research", AssignedRunner: agentRunnerOpenAIText,
			SourceMessageID: receipt.SourceMessageID, SourceMessageDigest: receipt.SourceMessageDigest,
			SourceWindowDigest: receipt.SourceWindowDigest, OperationID: receipt.OperationID,
			OperationBodyDigest: receipt.OperationBodyDigest, ParentGoalRouteDigest: receipt.Digest,
		}
	}
	launch := func(query string, spec agentThreadGoalSpec) scoutAgentThread {
		t.Helper()
		child, launchErr := app.launchGoalAgentThreadScaffold("research", query, "AJ", origin, spec)
		if launchErr != nil {
			t.Fatal(launchErr)
		}
		return child
	}
	staleSpec := childSpec("Aurora market attempt one")
	currentSpec := childSpec("Aurora market revision two")
	stale := launch("Aurora market attempt one", staleSpec)
	current := launch("Aurora market revision two", currentSpec)
	plan.Subtasks = []goalSubtask{{
		ID: "research", Title: "Aurora market", Mode: "research", Authority: codexJobAuthorityReadOnly,
		Runner: agentRunnerOpenAIText, Status: subtaskRunning, ArtifactID: current.Artifact.ID, Attempts: 2, Revisions: 1,
	}}
	plan.State = goalStateExecute
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.updateOSArtifactWithMetadata(parent.Artifact.ID, "", parent.Artifact.Text, scoutParticipantName, map[string]string{"goalPlan": string(encoded)}); err != nil {
		t.Fatal(err)
	}
	if err := app.activateReservedGoalAgentThread(current, currentSpec, "AJ"); err != nil {
		t.Fatal(err)
	}

	restarted := newKanbanBoardApp()
	providerCalls := 0
	if _, err := restarted.produceAgentThreadArtifactWithWorker(context.Background(), stale, func(context.Context, string, openAITextRequest) (string, error) {
		providerCalls++
		return "stale output", nil
	}); err == nil || providerCalls != 0 {
		t.Fatalf("stale revision reached provider admission: calls=%d err=%v", providerCalls, err)
	}
	restarted.foldGoalChildCompletion(parent.Artifact.ID, "research", stale.Artifact, codexJobStatusComplete)
	after := mustGoalPlan(t, restarted, parent.Artifact.ID)
	got := after.subtaskByID("research")
	if got == nil || got.Status != subtaskRunning || got.ArtifactID != current.Artifact.ID || got.Revisions != 1 {
		t.Fatalf("stale revision folded after restart: %+v", got)
	}
}

func TestGoalChildFoldRejectsTamperedCurrentRoute(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	previousGoalStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStart })
	previousAgentStart := startAgentThreadAsync
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousAgentStart })

	parent, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Research the governed Vega market", CreatedBy: "aj@shareability.com", ToolTemplate: "deep_research",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := mustGoalPlan(t, app, parent.Artifact.ID)
	engine := newGoalEngine(app)
	if err := engine.prepareGoalRoute(&plan, parent.Artifact.ID); err != nil {
		t.Fatal(err)
	}
	receipt := plan.RouteReceipt
	childSpec := agentThreadGoalSpec{
		Objective: "Vega market", ToolTemplate: "deep_research", Deliverable: true, RequestedBy: receipt.Requester, Authority: codexJobAuthorityReadOnly,
		ParentGoalID: parent.Artifact.ID, SubtaskID: "research", AssignedRunner: agentRunnerOpenAIText,
		SourceMessageID: receipt.SourceMessageID, SourceMessageDigest: receipt.SourceMessageDigest,
		SourceWindowDigest: receipt.SourceWindowDigest, OperationID: receipt.OperationID,
		OperationBodyDigest: receipt.OperationBodyDigest, ParentGoalRouteDigest: receipt.Digest,
	}
	child, err := app.launchGoalAgentThreadScaffold("research", "Vega market", "AJ", goalRouteChildBindingMetadata(&plan), childSpec)
	if err != nil {
		t.Fatal(err)
	}
	plan.Subtasks = []goalSubtask{{
		ID: "research", Title: "Vega market", Mode: "research", Authority: codexJobAuthorityReadOnly,
		Runner: agentRunnerOpenAIText, Status: subtaskRunning, ArtifactID: child.Artifact.ID, Attempts: 1,
	}}
	plan.State = goalStateExecute
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.updateOSArtifactWithMetadata(parent.Artifact.ID, "", parent.Artifact.Text, scoutParticipantName, map[string]string{"goalPlan": string(encoded)}); err != nil {
		t.Fatal(err)
	}
	if err := app.activateReservedGoalAgentThread(child, childSpec, "AJ"); err != nil {
		t.Fatal(err)
	}
	child.Artifact, _ = app.osArtifactByID(child.Artifact.ID)

	tampered := child.Artifact
	tampered.Metadata = cloneStringMap(child.Artifact.Metadata)
	tampered.Metadata["operationId"] = "forged-operation"
	app.foldGoalChildCompletion(parent.Artifact.ID, "research", tampered, codexJobStatusComplete)
	afterPlan := mustGoalPlan(t, app, parent.Artifact.ID)
	after := afterPlan.subtaskByID("research")
	if after == nil || after.Status != subtaskRunning || after.ArtifactID != child.Artifact.ID {
		t.Fatalf("tampered current child folded: %+v", after)
	}
}

func TestGoalChildReservedBeforeCrashRestartsExactArtifactOnce(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	installFakeResponder(t, goalResponderRoutes{})
	previousGoalStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStart })
	previousAgentStart := startAgentThreadAsync
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousAgentStart })

	parent, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Write the restart-safe Nova one-pager", CreatedBy: "aj@shareability.com", ToolTemplate: "one_pager",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := mustGoalPlan(t, app, parent.Artifact.ID)
	engine := newGoalEngine(app)
	if err := engine.prepareGoalRoute(&plan, parent.Artifact.ID); err != nil {
		t.Fatal(err)
	}
	receipt := plan.RouteReceipt
	spec := agentThreadGoalSpec{
		Objective: "Nova one-pager", ToolTemplate: "one_pager", Deliverable: true, RequestedBy: receipt.Requester, Authority: codexJobAuthorityReadOnly,
		ParentGoalID: parent.Artifact.ID, SubtaskID: "writer", AssignedRunner: agentRunnerOpenAIText,
		SourceMessageID: receipt.SourceMessageID, SourceMessageDigest: receipt.SourceMessageDigest,
		SourceWindowDigest: receipt.SourceWindowDigest, OperationID: receipt.OperationID,
		OperationBodyDigest: receipt.OperationBodyDigest, ParentGoalRouteDigest: receipt.Digest,
	}
	child, err := app.launchGoalAgentThreadScaffold("artifacts", "Nova one-pager", "AJ", goalRouteChildBindingMetadata(&plan), spec)
	if err != nil {
		t.Fatal(err)
	}
	plan.Subtasks = []goalSubtask{{
		ID: "writer", Title: "Nova one-pager", Mode: "artifacts", Authority: codexJobAuthorityReadOnly,
		Runner: agentRunnerOpenAIText, Status: subtaskRunning, ArtifactID: child.Artifact.ID, ThreadID: child.ID, Attempts: 1,
	}}
	plan.State = goalStateExecute
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.updateOSArtifactWithMetadata(parent.Artifact.ID, "", parent.Artifact.Text, scoutParticipantName, map[string]string{
		"goalPlan": string(encoded), "currentStage": goalStateExecute,
	}); err != nil {
		t.Fatal(err)
	}

	// Simulate a crash after the parent reservation became durable but before
	// activation. Boot must start the exact reserved child, never make a twin.
	restarted := newKanbanBoardApp()
	var starts atomic.Int32
	startedID := ""
	startAgentThreadAsync = func(_ *kanbanBoardApp, thread scoutAgentThread) {
		starts.Add(1)
		startedID = thread.Artifact.ID
	}
	restarted.reconcileGoalThread(parent.Artifact.ID)
	restarted.reconcileGoalThread(parent.Artifact.ID)
	if starts.Load() != 1 || startedID != child.Artifact.ID {
		t.Fatalf("duplicate boot reconciliation starts=%d child=%q, want one exact start of %q", starts.Load(), startedID, child.Artifact.ID)
	}
	stored, ok := restarted.osArtifactByID(child.Artifact.ID)
	if !ok || stored.Metadata["goalChildActivationState"] != goalChildActivationStarted {
		t.Fatalf("reserved child was not durably activated: %+v", stored.Metadata)
	}
	afterPlan := mustGoalPlan(t, restarted, parent.Artifact.ID)
	after := afterPlan.subtaskByID("writer")
	if after == nil || after.ArtifactID != child.Artifact.ID || after.Status != subtaskRunning {
		t.Fatalf("boot changed the reserved assignment: %+v", after)
	}
	if afterPlan.State != goalStateExecute {
		t.Fatalf("duplicate reconciliation blocked an in-process recovered child: state=%q blocker=%q", afterPlan.State, afterPlan.Blocker)
	}
	stored, _, err = restarted.updateOSArtifactWithMetadata(stored.ID, "", lawSweepCleanOnePagerBody(), "AJ", map[string]string{
		"status": codexJobStatusComplete, "threadStatus": codexJobStatusComplete,
	})
	if err != nil {
		t.Fatal(err)
	}
	restarted.foldGoalChildCompletion(parent.Artifact.ID, "writer", stored, codexJobStatusComplete)
	folded := mustGoalPlan(t, restarted, parent.Artifact.ID)
	completed := folded.subtaskByID("writer")
	if completed == nil || completed.Status != subtaskComplete || completed.ArtifactID != child.Artifact.ID {
		t.Fatalf("exact recovered child did not fold after duplicate reconciliation: state=%q blocker=%q subtask=%+v", folded.State, folded.Blocker, completed)
	}
	if restarted.goalChildStartedInProcess(child.Artifact.ID) {
		t.Fatal("terminal recovered child retained an unbounded process ownership marker")
	}
}

func TestGoalChildParentReservationFailureStartsNothing(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	previousGoalStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStart })
	previousAgentStart := startAgentThreadAsync
	var starts atomic.Int32
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) { starts.Add(1) }
	t.Cleanup(func() { startAgentThreadAsync = previousAgentStart })

	parent, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Research the fail-closed Lyra market", CreatedBy: "aj@shareability.com", ToolTemplate: "deep_research",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := mustGoalPlan(t, app, parent.Artifact.ID)
	plan.Subtasks = []goalSubtask{{
		ID: "research", Title: "Lyra market", Mode: "research", Authority: codexJobAuthorityReadOnly,
		Runner: agentRunnerOpenAIText, Status: subtaskRunning, Attempts: 1,
	}}
	plan.State = goalStateExecute
	engine := newGoalEngine(app)
	if err := engine.prepareGoalRoute(&plan, parent.Artifact.ID); err != nil {
		t.Fatal(err)
	}
	// Force only the parent reservation CAS to fail after the child scaffold is
	// durable. The child must be quarantined before any async/provider seam.
	engine.expectedPersistHeader = &ArtifactAuthorizationHeader{ObjectID: "wrong-parent-revision"}
	engine.expectedPersistBody = parent.Artifact.Text
	if err := engine.launchSubtask(&plan, &plan.Subtasks[0], parent.Artifact.ID); err == nil {
		t.Fatal("goal child launch succeeded despite failed parent reservation")
	}
	if starts.Load() != 0 {
		t.Fatalf("failed parent reservation started %d agents, want zero", starts.Load())
	}
	children := 0
	for _, entry := range app.memory.snapshot(0) {
		if entry.Metadata["goalParentId"] != parent.Artifact.ID {
			continue
		}
		children++
		if entry.Metadata["threadStatus"] != "error" || entry.Metadata["currentStage"] != "parent_reservation_failed" {
			t.Fatalf("failed reservation child was not quarantined: %+v", entry.Metadata)
		}
	}
	if children != 1 {
		t.Fatalf("failed reservation created %d children, want one quarantined scaffold", children)
	}
	durable := mustGoalPlan(t, app, parent.Artifact.ID)
	if len(durable.Subtasks) != 0 {
		t.Fatalf("failed reservation leaked child assignment into parent: %+v", durable.Subtasks)
	}
}
