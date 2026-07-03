package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Test harness ------------------------------------------------------------

// goalResponderRoutes fakes the orchestrator model per stage, keyed off the
// distinct system prompt each stage sends. Any stage without an override gets a
// sensible default (decompose -> one subtask, review/gate/verify -> pass).
type goalResponderRoutes struct {
	decompose string
	review    string
	gate      string
	report    string
	verify    string
}

func (routes goalResponderRoutes) responder(t *testing.T) anthropicMessagesResponder {
	t.Helper()
	return func(_ context.Context, apiKey string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		if apiKey != "test-key" {
			t.Fatalf("apiKey=%q, want test-key", apiKey)
		}
		system := strings.ToLower(request.System)
		text := ""
		switch {
		case strings.Contains(system, "decomposer"):
			text = firstNonEmptyString(routes.decompose, `{"subtasks":[{"id":"st-1","title":"Market map","mode":"research","authority":"read_only","dependsOn":[]}]}`)
		case strings.Contains(system, "reviewer"):
			text = firstNonEmptyString(routes.review, `{"verdict":"pass","score":9,"reasons":"meets the subtask"}`)
		case strings.Contains(system, "ship gate"):
			text = firstNonEmptyString(routes.gate, `{"safe":true,"external_write_required":false,"command":"","reason":"safe to ship"}`)
		case strings.Contains(system, "reporting a finished goal"):
			text = firstNonEmptyString(routes.report, `{"changed":"packaged the IP","headline":"Aurora one-pager ready","gap":"","next":"share with investors","assumed_claim_count":1}`)
		case strings.Contains(system, "final verifier"):
			text = firstNonEmptyString(routes.verify, `{"verdict":"pass","reasons":"goal met"}`)
		default:
			t.Fatalf("unexpected system prompt: %q", request.System)
		}
		return anthropicMessagesResponse{StopReason: "end_turn", Content: []json.RawMessage{mockAnthropicTextBlock(text)}}, nil
	}
}

type capturedChild struct {
	threadID  string
	subtaskID string
	authority string
	mode      string
}

// installFakeChildRunner replaces startAgentThreadAsync so a launched subtask
// child completes synthetically and folds back into the parent plan — the same
// path production takes, minus the real runner. It records every launch so a
// test can assert what was (and was not) dispatched.
func installFakeChildRunner(t *testing.T) *[]capturedChild {
	t.Helper()
	var mu sync.Mutex
	launched := &[]capturedChild{}

	original := startAgentThreadAsync
	t.Cleanup(func() { startAgentThreadAsync = original })
	startAgentThreadAsync = func(app *kanbanBoardApp, thread scoutAgentThread) {
		meta := thread.Artifact.Metadata
		mu.Lock()
		*launched = append(*launched, capturedChild{
			threadID:  thread.ID,
			subtaskID: meta["goalSubtaskId"],
			authority: meta["authority"],
			mode:      thread.Mode,
		})
		mu.Unlock()
		parent := meta["goalParentId"]
		sub := meta["goalSubtaskId"]
		if parent == "" {
			return
		}
		go func() {
			child, _, err := app.updateOSArtifactWithMetadata(thread.Artifact.ID, "", "subtask output: "+thread.Query, "tester", map[string]string{
				"threadStatus": "complete",
				"status":       "complete",
			})
			if err != nil {
				return
			}
			app.foldGoalChildCompletion(parent, sub, child, "complete")
		}()
	}
	return launched
}

func installFakeResponder(t *testing.T, routes goalResponderRoutes) {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	original := createAnthropicMessagesResponse
	t.Cleanup(func() { createAnthropicMessagesResponse = original })
	createAnthropicMessagesResponse = routes.responder(t)
	// launchGoalThread starts the engine on its own goroutine; the tests drive
	// it synchronously instead so assertions are deterministic.
	originalStart := startGoalThreadAsync
	t.Cleanup(func() { startGoalThreadAsync = originalStart })
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
}

func waitForGoalStage(t *testing.T, app *kanbanBoardApp, parentID string, want string) goalPlan {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if artifact, ok := app.osArtifactByID(parentID); ok {
			if artifact.Metadata["currentStage"] == want {
				plan, _ := decodeGoalPlan(artifact.Metadata["goalPlan"])
				return plan
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	artifact, _ := app.osArtifactByID(parentID)
	t.Fatalf("goal never reached %q; last stage=%q", want, artifact.Metadata["currentStage"])
	return goalPlan{}
}

// --- Happy path: full state machine -----------------------------------------

func TestGoalEngineHappyPathReachesVerified(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{
		// Three subtasks with a dependency chain to exercise topological order.
		decompose: `{"subtasks":[
			{"id":"st-1","title":"Market map","mode":"research","authority":"read_only","dependsOn":[]},
			{"id":"st-2","title":"One-pager draft","mode":"design","authority":"workspace_write","dependsOn":["st-1"]},
			{"id":"st-3","title":"Investor deck","mode":"design","authority":"workspace_write","dependsOn":["st-1"]}
		]}`,
	})
	launched := installFakeChildRunner(t)

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Package the Aurora IP for investors", CreatedBy: "aj@shareability.com"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)

	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)
	if plan.Verification.Verdict != goalReviewPass {
		t.Fatalf("verification verdict=%q, want pass", plan.Verification.Verdict)
	}
	if plan.Report.Headline == "" || plan.Report.AssumedClaimCount != 1 {
		t.Fatalf("report not populated: %+v", plan.Report)
	}
	for _, st := range plan.Subtasks {
		if st.Status != subtaskComplete {
			t.Fatalf("subtask %s status=%q, want complete", st.ID, st.Status)
		}
		if st.Review == nil || st.Review.Verdict != goalReviewPass {
			t.Fatalf("subtask %s missing pass review: %+v", st.ID, st.Review)
		}
	}
	// One launch per subtask, no duplicates.
	if len(*launched) != 3 {
		t.Fatalf("launched %d children, want 3: %+v", len(*launched), *launched)
	}

	artifact, _ := app.osArtifactByID(thread.Artifact.ID)
	if artifact.Metadata["goalStatus"] != "verified" || artifact.Metadata["progressPercent"] != "100" {
		t.Fatalf("terminal metadata=%v, want verified/100", artifact.Metadata)
	}
}

// --- Concurrency cap ---------------------------------------------------------

func TestGoalEngineRespectsConcurrencyCap(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{
		decompose: `{"subtasks":[
			{"id":"st-1","title":"A","mode":"research","authority":"read_only","dependsOn":[]},
			{"id":"st-2","title":"B","mode":"research","authority":"read_only","dependsOn":[]},
			{"id":"st-3","title":"C","mode":"research","authority":"read_only","dependsOn":[]},
			{"id":"st-4","title":"D","mode":"research","authority":"read_only","dependsOn":[]}
		]}`,
	})

	// A child start that does NOT auto-complete: it only records the launch, so
	// we can observe how many subtasks are in-flight at the first dispatch.
	var mu sync.Mutex
	running := 0
	original := startAgentThreadAsync
	t.Cleanup(func() { startAgentThreadAsync = original })
	startAgentThreadAsync = func(_ *kanbanBoardApp, thread scoutAgentThread) {
		if thread.Artifact.Metadata["goalParentId"] == "" {
			return
		}
		mu.Lock()
		running++
		mu.Unlock()
	}

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Run four independent research probes", CreatedBy: "aj@shareability.com"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)

	mu.Lock()
	got := running
	mu.Unlock()
	if got != 2 {
		t.Fatalf("dispatched %d children at once, want concurrency cap 2", got)
	}
	plan, _ := decodeGoalPlan(mustArtifact(t, app, thread.Artifact.ID).Metadata["goalPlan"])
	if goalCountStatus(&plan, subtaskRunning) != 2 || goalCountStatus(&plan, subtaskReady) != 2 {
		t.Fatalf("plan status counts wrong: running=%d ready=%d", goalCountStatus(&plan, subtaskRunning), goalCountStatus(&plan, subtaskReady))
	}
}

// --- Review fail retry bound -------------------------------------------------

func TestGoalEngineReviewFailBlocksAfterRevisionBound(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{
		decompose: `{"subtasks":[{"id":"st-1","title":"Draft","mode":"design","authority":"workspace_write","dependsOn":[]}]}`,
		review:    `{"verdict":"fail","score":3,"reasons":"does not meet the goal"}`,
	})
	launched := installFakeChildRunner(t)

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Produce the flawless draft", CreatedBy: "aj@shareability.com"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)

	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateBlocked)
	if plan.Subtasks[0].Status != subtaskBlocked {
		t.Fatalf("subtask status=%q, want blocked", plan.Subtasks[0].Status)
	}
	if plan.Subtasks[0].Revisions != goalMaxRevisions {
		t.Fatalf("revisions=%d, want %d", plan.Subtasks[0].Revisions, goalMaxRevisions)
	}
	// Initial launch + goalMaxRevisions re-queued launches.
	if len(*launched) != goalMaxRevisions+1 {
		t.Fatalf("launched %d children, want %d", len(*launched), goalMaxRevisions+1)
	}
	if plan.Blocker == "" {
		t.Fatal("blocked goal has no blocker line")
	}
}

// --- Gate -> approval_required for external_write (safety regression) --------

func TestGoalEngineExternalWriteStopsAtApprovalWithoutLaunchingCommit(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	queueDir := t.TempDir()
	t.Setenv("BONFIRE_CODEX_QUEUE_PATH", queueDir)
	installFakeResponder(t, goalResponderRoutes{
		decompose: `{"subtasks":[{"id":"st-1","title":"Prepare release notes","mode":"research","authority":"read_only","dependsOn":[]}]}`,
		// Even a gate that says "safe" must not self-approve an external write.
		gate: `{"safe":true,"external_write_required":false,"command":"","reason":"looks fine"}`,
	})
	launched := installFakeChildRunner(t)

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Build and deploy the release to production", CreatedBy: "aj@shareability.com"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	if thread.Query == "" {
		t.Fatal("thread query empty")
	}
	app.runGoalThread(thread.Artifact.ID)

	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateApproval)
	if plan.Authority != codexJobAuthorityExternalWrite {
		t.Fatalf("authority=%q, want external_write", plan.Authority)
	}
	if !plan.Gate.ApprovalRequired || plan.Gate.Status != goalStateApproval {
		t.Fatalf("gate did not force approval: %+v", plan.Gate)
	}

	// SAFETY: no child was launched with external_write authority.
	for _, child := range *launched {
		if child.authority == codexJobAuthorityExternalWrite {
			t.Fatalf("subtask %s launched with external_write authority", child.subtaskID)
		}
	}
	// SAFETY: no external_write sidecar job was enqueued before approval.
	store := newCodexRunnerJobStore(queueDir)
	if job, err := store.claimNext("test-runner"); err != nil {
		t.Fatalf("claimNext: %v", err)
	} else if job != nil {
		t.Fatalf("a sidecar job was enqueued before approval: %+v", job)
	}

	artifact := mustArtifact(t, app, thread.Artifact.ID)
	if artifact.Metadata["reviewGate"] != "approval_required" || artifact.Metadata["threadStatus"] != codexJobStatusApprovalRequired {
		t.Fatalf("approval metadata not set for the admin gate: %v", artifact.Metadata)
	}
}

// --- resumeApprovedGoal is the ONLY path to an external_write job ------------

func TestResumeApprovedGoalRefusesWithoutApprovalRecord(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})

	// A plain running goal (no approval gate) must not be resumable into commit.
	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Write a research brief", CreatedBy: "aj@shareability.com"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	if err := app.resumeApprovedGoal(thread.Artifact.ID, "aj@shareability.com"); err == nil {
		t.Fatal("resumeApprovedGoal accepted a goal that never reached the approval gate")
	}
}

func TestResumeApprovedGoalEnqueuesExternalWriteJobAfterApproval(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	queueDir := t.TempDir()
	t.Setenv("BONFIRE_CODEX_QUEUE_PATH", queueDir)
	installFakeResponder(t, goalResponderRoutes{
		decompose: `{"subtasks":[{"id":"st-1","title":"Prep","mode":"research","authority":"read_only","dependsOn":[]}]}`,
		gate:      `{"safe":true,"external_write_required":true,"command":"git push origin main","reason":"needs a push"}`,
	})
	installFakeChildRunner(t)

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Ship the fix to production", CreatedBy: "aj@shareability.com"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)
	waitForGoalStage(t, app, thread.Artifact.ID, goalStateApproval)

	// Admin approval record -> the single external_write sidecar job is enqueued.
	if err := app.resumeApprovedGoal(thread.Artifact.ID, "aj@shareability.com"); err != nil {
		t.Fatalf("resumeApprovedGoal: %v", err)
	}
	store := newCodexRunnerJobStore(queueDir)
	job, err := store.claimNext("test-runner")
	if err != nil {
		t.Fatalf("claimNext: %v", err)
	}
	if job == nil {
		t.Fatal("no external_write job enqueued after approval")
	}
	if job.Authority != codexJobAuthorityExternalWrite {
		t.Fatalf("job authority=%q, want external_write", job.Authority)
	}
}

// --- Plan validation ---------------------------------------------------------

func TestValidateGoalPlan(t *testing.T) {
	base := func(subtasks []goalSubtask) *goalPlan {
		return &goalPlan{PlanVersion: goalPlanVersion, State: goalStateDecompose, Subtasks: subtasks}
	}
	cases := []struct {
		name    string
		plan    *goalPlan
		wantErr bool
	}{
		{"empty", base(nil), true},
		{"valid chain", base([]goalSubtask{
			{ID: "st-1", Title: "A", Mode: "research"},
			{ID: "st-2", Title: "B", Mode: "design", DependsOn: []string{"st-1"}},
		}), false},
		{"too many", base([]goalSubtask{
			{ID: "st-1", Title: "A", Mode: "research"}, {ID: "st-2", Title: "B", Mode: "research"},
			{ID: "st-3", Title: "C", Mode: "research"}, {ID: "st-4", Title: "D", Mode: "research"},
			{ID: "st-5", Title: "E", Mode: "research"}, {ID: "st-6", Title: "F", Mode: "research"},
			{ID: "st-7", Title: "G", Mode: "research"},
		}), true},
		{"duplicate id", base([]goalSubtask{
			{ID: "st-1", Title: "A", Mode: "research"}, {ID: "st-1", Title: "B", Mode: "research"},
		}), true},
		{"bad mode", base([]goalSubtask{{ID: "st-1", Title: "A", Mode: "telepathy"}}), true},
		{"unknown dep", base([]goalSubtask{{ID: "st-1", Title: "A", Mode: "research", DependsOn: []string{"ghost"}}}), true},
		{"self dep", base([]goalSubtask{{ID: "st-1", Title: "A", Mode: "research", DependsOn: []string{"st-1"}}}), true},
		{"cycle", base([]goalSubtask{
			{ID: "st-1", Title: "A", Mode: "research", DependsOn: []string{"st-2"}},
			{ID: "st-2", Title: "B", Mode: "research", DependsOn: []string{"st-1"}},
		}), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateGoalPlan(tc.plan)
			if tc.wantErr != (err != nil) {
				t.Fatalf("validateGoalPlan err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestGoalDecomposeRejectsMalformedThenNeedsAttention(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{decompose: "not json at all, just prose"})

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Do the thing", CreatedBy: "aj@shareability.com"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)

	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateBlocked)
	if plan.DecomposeAttempts != goalMaxDecomposeTries {
		t.Fatalf("decompose attempts=%d, want %d", plan.DecomposeAttempts, goalMaxDecomposeTries)
	}
	if !strings.Contains(plan.Blocker, "decomposition failed") {
		t.Fatalf("blocker=%q, want decomposition failure", plan.Blocker)
	}
}

// --- Assignment (pure, re-derivable) ----------------------------------------

func TestAssignGoalRunners(t *testing.T) {
	t.Setenv("BONFIRE_EXECUTION_RUNNER", "codex_sidecar")
	plan := &goalPlan{Subtasks: []goalSubtask{
		{ID: "st-1", Title: "Research the market", Mode: "research"},
		{ID: "st-2", Title: "Implement the fix and run the tests", Mode: "workflow"},
	}}
	assignGoalRunners(plan)
	if plan.Subtasks[0].Runner != selectedAgentRunnerName() {
		t.Fatalf("research subtask runner=%q, want orchestrator %q", plan.Subtasks[0].Runner, selectedAgentRunnerName())
	}
	if plan.Subtasks[1].Runner != agentRunnerCodexSidecar {
		t.Fatalf("shell subtask runner=%q, want execution runner", plan.Subtasks[1].Runner)
	}
}

// --- Authority clamp: subtask never out-privileges its parent goal ----------

func TestGoalChildAuthorityClampsToParent(t *testing.T) {
	cases := []struct {
		subtask string
		parent  string
		want    string
	}{
		{codexJobAuthorityWorkspaceWrite, codexJobAuthorityReadOnly, codexJobAuthorityReadOnly}, // parent caps it down
		{codexJobAuthorityReadOnly, codexJobAuthorityWorkspaceWrite, codexJobAuthorityReadOnly}, // subtask stays lower
		{codexJobAuthorityWorkspaceWrite, codexJobAuthorityWorkspaceWrite, codexJobAuthorityWorkspaceWrite},
		{codexJobAuthorityExternalWrite, codexJobAuthorityExternalWrite, codexJobAuthorityWorkspaceWrite}, // never external_write for a child
		{codexJobAuthorityExternalWrite, codexJobAuthorityReadOnly, codexJobAuthorityReadOnly},
	}
	for _, tc := range cases {
		if got := goalChildAuthority(tc.subtask, tc.parent); got != tc.want {
			t.Fatalf("goalChildAuthority(%q,%q)=%q, want %q", tc.subtask, tc.parent, got, tc.want)
		}
	}
}

// --- Resumability: reconciler folds completed children, no duplicates --------

func TestReconcileGoalThreadResumesInFlightPlan(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})
	launched := installFakeChildRunner(t)

	// A completed child artifact for st-1 (as if it finished before the crash).
	child, _, err := app.createOSArtifactWithMetadata("research", "Market map", "the market map output", "tester", map[string]string{
		"threadStatus": "complete",
		"status":       "complete",
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// An in-flight plan: st-1 running with a now-terminal child, st-2 pending on it.
	plan := goalPlan{
		PlanVersion:  goalPlanVersion,
		GoalID:       "agent-thread-goal-resume",
		Objective:    "Package the Aurora IP",
		CreatedBy:    "aj@shareability.com",
		Authority:    codexJobAuthorityWorkspaceWrite,
		State:        goalStateExecute,
		Gate:         goalGate{Status: "pending"},
		Verification: goalVerification{Verdict: "pending"},
		Subtasks: []goalSubtask{
			{ID: "st-1", Title: "Market map", Mode: "research", Authority: codexJobAuthorityReadOnly, Status: subtaskRunning, ArtifactID: child.ID, Attempts: 1},
			{ID: "st-2", Title: "One-pager", Mode: "design", Authority: codexJobAuthorityWorkspaceWrite, DependsOn: []string{"st-1"}, Status: subtaskPending},
		},
	}
	raw, _ := json.Marshal(plan)
	parent, _, err := app.createOSArtifactWithMetadata("workflow", plan.Objective, buildGoalScaffold(plan), "aj@shareability.com", map[string]string{
		"mode":         "goal",
		"goalPlan":     string(raw),
		"currentStage": goalStateExecute,
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	app.reconcileGoalThread(parent.ID)

	resumed := waitForGoalStage(t, app, parent.ID, goalStateVerified)
	for _, st := range resumed.Subtasks {
		if st.Status != subtaskComplete {
			t.Fatalf("subtask %s status=%q, want complete", st.ID, st.Status)
		}
	}
	// st-1 was folded from its already-terminal child (not relaunched); only
	// st-2 gets a fresh launch. No duplicate for st-1.
	if len(*launched) != 1 || (*launched)[0].subtaskID != "st-2" {
		t.Fatalf("launched=%+v, want only st-2 dispatched", *launched)
	}
}

func TestReconcileGoalThreadSkipsTerminalAndApprovalStates(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	for _, state := range []string{goalStateVerified, goalStateBlocked, goalStateApproval} {
		if !isTerminalGoalState(state) {
			t.Fatalf("state %q should be skipped by the reconciler", state)
		}
	}
	// A verified goal is not re-driven (no panic, no state change).
	plan := goalPlan{PlanVersion: goalPlanVersion, State: goalStateVerified, Objective: "done"}
	raw, _ := json.Marshal(plan)
	artifact, _, err := app.createOSArtifactWithMetadata("workflow", "done", "body", "aj", map[string]string{
		"mode":         "goal",
		"goalPlan":     string(raw),
		"currentStage": goalStateVerified,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	app.reconcileGoalThreadsAtBoot()
	time.Sleep(20 * time.Millisecond)
	if got := mustArtifact(t, app, artifact.ID).Metadata["currentStage"]; got != goalStateVerified {
		t.Fatalf("verified goal changed stage to %q", got)
	}
}

func mustArtifact(t *testing.T, app *kanbanBoardApp, id string) meetingMemoryEntry {
	t.Helper()
	artifact, ok := app.osArtifactByID(id)
	if !ok {
		t.Fatalf("artifact %s not found", id)
	}
	return artifact
}

func waitForArtifactMeta(t *testing.T, app *kanbanBoardApp, id string, key string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if artifact, ok := app.osArtifactByID(id); ok {
			if value := strings.TrimSpace(artifact.Metadata[key]); value != "" {
				return value
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("artifact %s never got metadata %q", id, key)
	return ""
}

func postCodexCallback(t *testing.T, artifactID string, jobID string, status string) {
	t.Helper()
	body, err := json.Marshal(codexRunnerCallbackPayload{
		JobID:      jobID,
		ArtifactID: artifactID,
		Status:     status,
		Text:       "Vision: shipped\n\n## Codex worker evidence\n- Worker: codex exec",
		Metadata:   map[string]string{"runnerId": "test-runner"},
	})
	if err != nil {
		t.Fatalf("marshal callback: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/internal/codex/jobs/result", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runner-secret")
	recorder := httptest.NewRecorder()
	internalCodexRunnerResultHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("callback status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func countQueueJobs(t *testing.T, dir string) int {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatalf("glob queue: %v", err)
	}
	return len(files)
}

// A subtask routed to the codex sidecar completes via the async HTTP callback
// (internalCodexRunnerResultHandler), NOT the synchronous runAgentThread seam.
// This drives the real queue+callback path (no fake-runner harness) and asserts
// the fold hook in the callback advances the parent plan to verified — the
// regression the reviewer flagged (execution subtasks otherwise strand forever).
func TestGoalEngineCodexSubtaskFoldsViaCallback(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	queueDir := t.TempDir()
	t.Setenv("BONFIRE_CODEX_QUEUE_PATH", queueDir)
	t.Setenv("BONFIRE_EXECUTION_RUNNER", "codex_sidecar")
	t.Setenv("BONFIRE_RUNNER_TOKEN", "runner-secret")
	installFakeResponder(t, goalResponderRoutes{
		// A shell-flavored subtask so assignGoalRunners routes it to the
		// execution runner (codex_sidecar), exercising the queued path.
		decompose: `{"subtasks":[{"id":"st-1","title":"Implement the fix and run the tests","mode":"workflow","authority":"workspace_write","dependsOn":[]}]}`,
	})

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Fix the failing build", CreatedBy: "aj@shareability.com"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)

	// The subtask enqueued a codex job and parked; the parent is NOT verified yet
	// because the queued child never passed through the synchronous fold seam.
	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateExecute)
	if plan.Subtasks[0].Runner != agentRunnerCodexSidecar {
		t.Fatalf("subtask runner=%q, want codex_sidecar", plan.Subtasks[0].Runner)
	}
	childID := plan.Subtasks[0].ArtifactID
	if childID == "" {
		t.Fatal("subtask child artifact id not recorded")
	}
	jobID := waitForArtifactMeta(t, app, childID, "runnerJobId")

	// The sidecar finishes and calls back: the fold hook must advance the parent.
	postCodexCallback(t, childID, jobID, codexJobStatusComplete)

	resumed := waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)
	if resumed.Subtasks[0].Status != subtaskComplete {
		t.Fatalf("subtask status=%q, want complete after callback fold", resumed.Subtasks[0].Status)
	}
}

// commit_push must be idempotent: one admin approval ships exactly one
// external_write job, and a server restart while parked at commit_push must NOT
// re-enqueue a duplicate push/deploy. Then the commit callback verifies the goal.
func TestGoalEngineCommitPushIsIdempotentAcrossRestart(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	queueDir := t.TempDir()
	t.Setenv("BONFIRE_CODEX_QUEUE_PATH", queueDir)
	t.Setenv("BONFIRE_RUNNER_TOKEN", "runner-secret")
	installFakeResponder(t, goalResponderRoutes{
		decompose: `{"subtasks":[{"id":"st-1","title":"Prep release notes","mode":"research","authority":"read_only","dependsOn":[]}]}`,
		gate:      `{"safe":true,"external_write_required":true,"command":"git push origin main","reason":"needs a push"}`,
	})
	installFakeChildRunner(t)

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Ship the fix to production", CreatedBy: "aj@shareability.com"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)
	waitForGoalStage(t, app, thread.Artifact.ID, goalStateApproval)

	// Admin approves -> exactly one external_write job enqueued.
	if err := app.resumeApprovedGoal(thread.Artifact.ID, "aj@shareability.com"); err != nil {
		t.Fatalf("resumeApprovedGoal: %v", err)
	}
	if got := countQueueJobs(t, queueDir); got != 1 {
		t.Fatalf("after approval queue has %d jobs, want 1", got)
	}
	plan := mustGoalPlan(t, app, thread.Artifact.ID)
	if plan.State != goalStateCommit || plan.Gate.CommitChildID == "" {
		t.Fatalf("plan not parked at commit_push with a child: state=%q child=%q", plan.State, plan.Gate.CommitChildID)
	}

	// Simulate two server restarts while parked at commit_push: neither may
	// enqueue a second external_write job.
	app.reconcileGoalThread(thread.Artifact.ID)
	app.reconcileGoalThread(thread.Artifact.ID)
	if got := countQueueJobs(t, queueDir); got != 1 {
		t.Fatalf("after restarts queue has %d jobs, want 1 (no duplicate push)", got)
	}

	// The commit callback lands on the commit child and verifies the goal.
	commitChildID := plan.Gate.CommitChildID
	jobID := waitForArtifactMeta(t, app, commitChildID, "runnerJobId")
	postCodexCallback(t, commitChildID, jobID, codexJobStatusComplete)
	verified := waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)
	if verified.Verification.Verdict != goalReviewPass {
		t.Fatalf("verification verdict=%q, want pass after commit", verified.Verification.Verdict)
	}
}

func mustGoalPlan(t *testing.T, app *kanbanBoardApp, id string) goalPlan {
	t.Helper()
	plan, ok := decodeGoalPlan(mustArtifact(t, app, id).Metadata["goalPlan"])
	if !ok {
		t.Fatalf("goal plan not decodable for %s", id)
	}
	return plan
}
