package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	// The codex callback folds on its own goroutine in production; run it inline
	// under test so a fold (and its broadcasts) fully completes before the
	// callback returns — no goroutine leaks across test boundaries.
	originalFold := foldGoalChildAsync
	t.Cleanup(func() { foldGoalChildAsync = originalFold })
	foldGoalChildAsync = func(app *kanbanBoardApp, parentID string, subtaskID string, child meetingMemoryEntry, status string) {
		app.foldGoalChildCompletion(parentID, subtaskID, child, status)
	}
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

// --- Per-user in-flight goal cap ----------------------------------------------

func TestGoalLaunchEnforcesPerUserInFlightCap(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})
	installFakeChildRunner(t)

	// Fill the default cap of 2 (startGoalThreadAsync is stubbed to a no-op, so
	// both goals park in a non-terminal stage). The second launch uses a
	// differently-cased email — same account, same bucket.
	first, err := app.launchGoalThread(goalLaunchSpec{Objective: "Package the Aurora IP", CreatedBy: "aj@shareability.com"})
	if err != nil {
		t.Fatalf("first launch: %v", err)
	}
	if _, err := app.launchGoalThread(goalLaunchSpec{Objective: "Draft the Vega one-pager", CreatedBy: "AJ@Shareability.com"}); err != nil {
		t.Fatalf("second launch: %v", err)
	}

	// The third launch breaches the cap with the typed refusal naming both
	// in-flight goals (id+title) in a speakable sentence.
	_, err = app.launchGoalThread(goalLaunchSpec{Objective: "Build the Nova deck", CreatedBy: "aj@shareability.com"})
	var capErr *errGoalUserCapExceeded
	if !errors.As(err, &capErr) {
		t.Fatalf("third launch err=%v, want errGoalUserCapExceeded", err)
	}
	if capErr.Cap != 2 || len(capErr.Goals) != 2 {
		t.Fatalf("cap breach: cap=%d goals=%+v, want cap 2 naming 2 goals", capErr.Cap, capErr.Goals)
	}
	for _, goal := range capErr.Goals {
		if goal.ID == "" || goal.Title == "" {
			t.Fatalf("cap breach ref missing id/title: %+v", capErr.Goals)
		}
	}
	if msg := capErr.Error(); !strings.Contains(msg, "Package the Aurora IP") || !strings.Contains(msg, "2 goals in flight") {
		t.Fatalf("speakable error does not name the goals: %q", msg)
	}

	// A different account has its own bucket — never blocked by someone else's.
	if _, err := app.launchGoalThread(goalLaunchSpec{Objective: "Independent objective", CreatedBy: "sam@shareability.com"}); err != nil {
		t.Fatalf("different user hit someone else's cap: %v", err)
	}

	// Driving the first goal to a terminal stage frees the slot.
	app.runGoalThread(first.Artifact.ID)
	waitForGoalStage(t, app, first.Artifact.ID, goalStateVerified)
	if _, err := app.launchGoalThread(goalLaunchSpec{Objective: "Build the Nova deck", CreatedBy: "aj@shareability.com"}); err != nil {
		t.Fatalf("launch after a goal completed still capped: %v", err)
	}
}

// The cap is check-then-append over the persisted store, so it must hold under
// concurrency: cap+2 simultaneous launches from one account (differently-cased
// emails — same bucket) must admit at most cap goals. Without the per-email
// launch lock every goroutine observes the pre-launch count and all pass.
func TestGoalLaunchUserCapHoldsUnderConcurrentLaunches(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})

	const capLimit = 2
	launches := capLimit + 2
	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0
	capRefusals := 0
	for i := 0; i < launches; i++ {
		wg.Add(1)
		email := "aj@shareability.com"
		if i%2 == 1 {
			email = "AJ@Shareability.com"
		}
		go func(index int, email string) {
			defer wg.Done()
			_, err := app.launchGoalThread(goalLaunchSpec{Objective: fmt.Sprintf("Concurrent objective %d", index), CreatedBy: email})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			default:
				var capErr *errGoalUserCapExceeded
				if !errors.As(err, &capErr) {
					t.Errorf("launch %d err=%v, want nil or errGoalUserCapExceeded", index, err)
					return
				}
				capRefusals++
			}
		}(i, email)
	}
	wg.Wait()

	if succeeded != capLimit || capRefusals != launches-capLimit {
		t.Fatalf("succeeded=%d refused=%d, want exactly %d launches admitted and %d cap refusals", succeeded, capRefusals, capLimit, launches-capLimit)
	}
	if inFlight := app.inFlightGoalsForUser("aj@shareability.com"); len(inFlight) != capLimit {
		t.Fatalf("in-flight goals=%d, want %d — the persisted store must agree with the admissions", len(inFlight), capLimit)
	}
}

func TestGoalLaunchUserCapEnvOverride(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})
	t.Setenv("BONFIRE_GOAL_USER_CAP", "1")

	if _, err := app.launchGoalThread(goalLaunchSpec{Objective: "Only goal allowed", CreatedBy: "aj@shareability.com"}); err != nil {
		t.Fatalf("first launch under cap=1: %v", err)
	}
	_, err := app.launchGoalThread(goalLaunchSpec{Objective: "One too many", CreatedBy: "aj@shareability.com"})
	var capErr *errGoalUserCapExceeded
	if !errors.As(err, &capErr) {
		t.Fatalf("second launch err=%v, want errGoalUserCapExceeded under BONFIRE_GOAL_USER_CAP=1", err)
	}
	if capErr.Cap != 1 || len(capErr.Goals) != 1 {
		t.Fatalf("cap breach: cap=%d goals=%+v, want cap 1 naming 1 goal", capErr.Cap, capErr.Goals)
	}

	// Raising the cap admits the same launch immediately.
	t.Setenv("BONFIRE_GOAL_USER_CAP", "3")
	if _, err := app.launchGoalThread(goalLaunchSpec{Objective: "One too many", CreatedBy: "aj@shareability.com"}); err != nil {
		t.Fatalf("launch under raised cap: %v", err)
	}
}

func TestGoalHTTPEndpointReturns429WhenUserCapExceeded(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	installFakeResponder(t, goalResponderRoutes{})
	t.Setenv("BONFIRE_GOAL_USER_CAP", "1")

	post := func(objective string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"objective": objective})
		req := httptest.NewRequest(http.MethodPost, "/assistant/goal", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://localhost")
		req.Host = "localhost"
		for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		assistantGoalHandler(rec, req)
		return rec
	}

	if rec := post("Package the Aurora IP"); rec.Code != http.StatusAccepted {
		t.Fatalf("first launch status=%d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusAccepted)
	}
	rec := post("One goal too many")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusTooManyRequests)
	}
	var payload struct {
		OK       bool              `json:"ok"`
		Error    string            `json:"error"`
		Cap      int               `json:"cap"`
		InFlight []goalInFlightRef `json:"inFlight"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode 429 body: %v (%s)", err, rec.Body.String())
	}
	if payload.OK || payload.Cap != 1 || len(payload.InFlight) != 1 {
		t.Fatalf("429 body=%+v, want ok=false cap=1 one in-flight goal", payload)
	}
	if payload.InFlight[0].ID == "" || !strings.Contains(payload.InFlight[0].Title, "Package the Aurora IP") {
		t.Fatalf("429 in-flight ref missing id/title: %+v", payload.InFlight)
	}
	if !strings.Contains(payload.Error, "Package the Aurora IP") {
		t.Fatalf("429 error does not name the blocking goal: %q", payload.Error)
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

// installAwaitableChildRunner is installFakeChildRunner with a caller-supplied
// child body plus a WaitGroup the test can await. The child fold is genuinely
// async (it must be — the driver holds the parent lock while dispatching, so a
// synchronous fold would deadlock), and the goal's terminal finish() runs
// notify + broadcast on that fold goroutine AFTER the stage is persisted. A test
// that asserts on those late side effects must wait for the goroutine to return,
// or it both reads too early and leaks the goroutine into the next test (where
// broadcastAssistantEvent races the shared kanbanApp). Wait() on the returned
// group after the goal reaches terminal guarantees every fold — including the
// terminal one — has fully completed.
func installAwaitableChildRunner(t *testing.T, body string) *sync.WaitGroup {
	t.Helper()
	var folds sync.WaitGroup
	original := startAgentThreadAsync
	t.Cleanup(func() { startAgentThreadAsync = original })
	startAgentThreadAsync = func(app *kanbanBoardApp, thread scoutAgentThread) {
		meta := thread.Artifact.Metadata
		parent, sub := meta["goalParentId"], meta["goalSubtaskId"]
		if parent == "" {
			return
		}
		// Add on the dispatching goroutine (before the spawn) so every child is
		// counted before Wait() is ever called. Re-dispatched revisions Add while
		// an earlier fold goroutine is still running, so the counter never returns
		// to zero mid-run.
		folds.Add(1)
		go func() {
			defer folds.Done()
			child, _, err := app.updateOSArtifactWithMetadata(thread.Artifact.ID, "", body, "tester", map[string]string{
				"threadStatus": "complete",
				"status":       "complete",
			})
			if err != nil {
				return
			}
			app.foldGoalChildCompletion(parent, sub, child, "complete")
		}()
	}
	return &folds
}

// A goal that terminates needs_attention because the deliverable missed the
// review bar must NOT orphan the produced draft: it is attached to the package,
// surfaced as the goal's result, and the honest gap is stamped — no gate bar is
// lowered (the goal is still needs_attention).
func TestGoalEngineSalvagesBlockedDeliverableIntoPackage(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	pkg, err := app.createVenturePackage("Aurora", "an IP thesis", "aj@shareability.com")
	if err != nil {
		t.Fatalf("createVenturePackage: %v", err)
	}
	installFakeResponder(t, goalResponderRoutes{
		decompose: `{"subtasks":[{"id":"st-1","title":"One-pager draft","mode":"design","authority":"workspace_write","dependsOn":[]}]}`,
		review:    `{"verdict":"fail","score":8,"reasons":"strong draft but the ask section is thin"}`,
	})
	longBody := "# Aurora One-Pager\n\n" + strings.Repeat("A substantial, contract-bearing paragraph of the one-pager deliverable. ", 20)
	folds := installAwaitableChildRunner(t, longBody)

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Package the Aurora IP", CreatedBy: "aj@shareability.com", PackageID: pkg.ID})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)

	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateBlocked)
	folds.Wait() // the terminal salvage + persist + notify have fully landed
	childID := plan.Subtasks[0].ArtifactID
	if childID == "" {
		t.Fatal("subtask produced no artifact id")
	}
	if plan.Report.DeliverableArtifactID != childID {
		t.Fatalf("deliverable not salvaged: report.DeliverableArtifactID=%q, want %q", plan.Report.DeliverableArtifactID, childID)
	}
	if !strings.Contains(plan.Report.Gap, "ask section") {
		t.Fatalf("gap line not honest about the miss: %q", plan.Report.Gap)
	}

	artifact := mustArtifact(t, app, thread.Artifact.ID)
	if artifact.Metadata["goalStatus"] != "needs_attention" {
		t.Fatalf("goalStatus=%q, want needs_attention (no bar lowered)", artifact.Metadata["goalStatus"])
	}
	if artifact.Metadata["deliverableArtifactId"] != childID {
		t.Fatalf("deliverableArtifactId metadata=%q, want %q", artifact.Metadata["deliverableArtifactId"], childID)
	}
	if artifact.Metadata["goalGap"] == "" {
		t.Fatal("goalGap metadata not stamped")
	}
	if !strings.Contains(artifact.Text, "Draft saved") || !strings.Contains(artifact.Text, childID) {
		t.Fatalf("goal brief missing the saved-draft section: %q", artifact.Text)
	}

	attached, _ := app.venturePackageByID(pkg.ID)
	found := false
	for _, id := range attached.ArtifactIDs {
		if id == childID {
			found = true
		}
	}
	if !found {
		t.Fatalf("salvaged draft %q not attached to package: %v", childID, attached.ArtifactIDs)
	}
}

// A requeue re-runs ONLY the failed subtask: an already-verified independent
// sibling keeps its artifact + pass verdict and is never re-dispatched.
func TestGoalEngineRequeuePreservesVerifiedSibling(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	var mu sync.Mutex
	betaReviews := 0
	responder := func(_ context.Context, apiKey string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		if apiKey != "test-key" {
			t.Fatalf("apiKey=%q, want test-key", apiKey)
		}
		system := strings.ToLower(request.System)
		text := ""
		switch {
		case strings.Contains(system, "decomposer"):
			text = `{"subtasks":[
				{"id":"st-1","title":"Alpha","mode":"research","authority":"read_only","dependsOn":[]},
				{"id":"st-2","title":"Beta","mode":"research","authority":"read_only","dependsOn":[]}
			]}`
		case strings.Contains(system, "reviewer"):
			userText := decodeAnthropicBlock(request.Messages[0].Content[0]).Text
			if strings.Contains(userText, "Beta") {
				mu.Lock()
				betaReviews++
				n := betaReviews
				mu.Unlock()
				if n == 1 {
					text = `{"verdict":"fail","score":4,"reasons":"needs work"}`
				} else {
					text = `{"verdict":"pass","score":9,"reasons":"good now"}`
				}
			} else {
				text = `{"verdict":"pass","score":9,"reasons":"alpha good"}`
			}
		case strings.Contains(system, "ship gate"):
			text = `{"safe":true,"external_write_required":false,"command":"","reason":"safe"}`
		case strings.Contains(system, "reporting a finished goal"):
			text = `{"changed":"x","headline":"done","gap":"","next":"","assumed_claim_count":0}`
		case strings.Contains(system, "final verifier"):
			text = `{"verdict":"pass","reasons":"ok"}`
		default:
			t.Fatalf("unexpected system prompt: %q", request.System)
		}
		return anthropicMessagesResponse{StopReason: "end_turn", Content: []json.RawMessage{mockAnthropicTextBlock(text)}}, nil
	}

	originalResp := createAnthropicMessagesResponse
	t.Cleanup(func() { createAnthropicMessagesResponse = originalResp })
	createAnthropicMessagesResponse = responder
	originalStart := startGoalThreadAsync
	t.Cleanup(func() { startGoalThreadAsync = originalStart })
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	originalFold := foldGoalChildAsync
	t.Cleanup(func() { foldGoalChildAsync = originalFold })
	foldGoalChildAsync = func(app *kanbanBoardApp, parentID string, subtaskID string, child meetingMemoryEntry, status string) {
		app.foldGoalChildCompletion(parentID, subtaskID, child, status)
	}
	launched := installFakeChildRunner(t)

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Two independent probes", CreatedBy: "aj@shareability.com"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)
	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)

	alpha, beta := 0, 0
	for _, c := range *launched {
		switch c.subtaskID {
		case "st-1":
			alpha++
		case "st-2":
			beta++
		}
	}
	if alpha != 1 {
		t.Fatalf("verified sibling Alpha dispatched %d times, want 1 — a requeue must not re-run it", alpha)
	}
	if beta != 2 {
		t.Fatalf("failed subtask Beta dispatched %d times, want 2 (initial + one revision)", beta)
	}
	st1 := plan.subtaskByID("st-1")
	if st1 == nil || st1.Attempts != 1 || st1.Review == nil || st1.Review.Verdict != goalReviewPass {
		t.Fatalf("verified sibling not preserved: %+v (review %+v)", st1, st1.Review)
	}
	st2 := plan.subtaskByID("st-2")
	if st2 == nil || st2.Attempts != 2 || st2.Revisions != 1 {
		t.Fatalf("failed subtask state wrong: attempts=%d revisions=%d", st2.Attempts, st2.Revisions)
	}
}

// --- Reviewer/gate read the FULL artifact body --------------------------------

// The per-subtask reviewer and the ship gate judge the artifact itself, not the
// 220-char compactAssistantLine thumbnail. A modest body arrives whole; a
// runaway body arrives head+tail with the truncation announced in the prompt.
func TestGoalReviewAndGateReadFullArtifactBody(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	// Alpha's body is well past the 220-char thumbnail cut but under the review
	// cap; Beta's blows past the cap so the head/tail truncation must kick in.
	alphaBody := strings.Repeat("alpha evidence paragraph. ", 20) + "ALPHA-TAIL-MARKER"
	if len(alphaBody) <= 220 || len(alphaBody) > goalReviewArtifactCap {
		t.Fatalf("alpha body length %d must exceed the thumbnail cut and stay under the cap", len(alphaBody))
	}
	filler := strings.Repeat("b", goalReviewArtifactCap/2)
	betaBody := "BETA-HEAD-MARKER " + filler + " BETA-MIDDLE-MARKER " + filler + " BETA-TAIL-MARKER"

	var mu sync.Mutex
	reviewPrompts := map[string]string{}
	gatePrompt := ""
	responder := func(_ context.Context, apiKey string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		if apiKey != "test-key" {
			t.Fatalf("apiKey=%q, want test-key", apiKey)
		}
		system := strings.ToLower(request.System)
		userText := decodeAnthropicBlock(request.Messages[0].Content[0]).Text
		text := ""
		switch {
		case strings.Contains(system, "decomposer"):
			text = `{"subtasks":[
				{"id":"st-1","title":"Alpha","mode":"research","authority":"read_only","dependsOn":[]},
				{"id":"st-2","title":"Beta","mode":"research","authority":"read_only","dependsOn":[]}
			]}`
		case strings.Contains(system, "reviewer"):
			mu.Lock()
			switch {
			case strings.Contains(userText, "Subtask: Alpha"):
				reviewPrompts["st-1"] = userText
			case strings.Contains(userText, "Subtask: Beta"):
				reviewPrompts["st-2"] = userText
			}
			mu.Unlock()
			text = `{"verdict":"pass","score":9,"reasons":"meets the subtask"}`
		case strings.Contains(system, "ship gate"):
			mu.Lock()
			gatePrompt = userText
			mu.Unlock()
			text = `{"safe":true,"external_write_required":false,"command":"","reason":"safe"}`
		case strings.Contains(system, "reporting a finished goal"):
			text = `{"changed":"x","headline":"done","gap":"","next":"","assumed_claim_count":0}`
		case strings.Contains(system, "final verifier"):
			text = `{"verdict":"pass","reasons":"ok"}`
		default:
			t.Fatalf("unexpected system prompt: %q", request.System)
		}
		return anthropicMessagesResponse{StopReason: "end_turn", Content: []json.RawMessage{mockAnthropicTextBlock(text)}}, nil
	}

	originalResp := createAnthropicMessagesResponse
	t.Cleanup(func() { createAnthropicMessagesResponse = originalResp })
	createAnthropicMessagesResponse = responder
	originalStart := startGoalThreadAsync
	t.Cleanup(func() { startGoalThreadAsync = originalStart })
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	originalFold := foldGoalChildAsync
	t.Cleanup(func() { foldGoalChildAsync = originalFold })
	foldGoalChildAsync = func(app *kanbanBoardApp, parentID string, subtaskID string, child meetingMemoryEntry, status string) {
		app.foldGoalChildCompletion(parentID, subtaskID, child, status)
	}

	// A child runner that writes a REAL per-subtask artifact body, mirroring
	// installFakeChildRunner but with controlled text.
	bodies := map[string]string{"st-1": alphaBody, "st-2": betaBody}
	originalChild := startAgentThreadAsync
	t.Cleanup(func() { startAgentThreadAsync = originalChild })
	startAgentThreadAsync = func(app *kanbanBoardApp, thread scoutAgentThread) {
		meta := thread.Artifact.Metadata
		parent, sub := meta["goalParentId"], meta["goalSubtaskId"]
		if parent == "" {
			return
		}
		go func() {
			child, _, err := app.updateOSArtifactWithMetadata(thread.Artifact.ID, "", bodies[sub], "tester", map[string]string{
				"threadStatus": "complete",
				"status":       "complete",
			})
			if err != nil {
				return
			}
			app.foldGoalChildCompletion(parent, sub, child, "complete")
		}()
	}

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Two evidence probes", CreatedBy: "aj@shareability.com"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)
	waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)

	mu.Lock()
	defer mu.Unlock()
	// Alpha's review saw the whole body, including text the old thumbnail cut.
	alphaReview := reviewPrompts["st-1"]
	if !strings.Contains(alphaReview, "ALPHA-TAIL-MARKER") {
		t.Fatalf("review prompt lost the artifact tail past the 220-char thumbnail:\n%s", alphaReview)
	}
	if !strings.Contains(alphaReview, "Produced artifact:\n"+alphaBody) {
		t.Fatal("review prompt does not carry Alpha's full artifact body verbatim")
	}
	// Beta's review saw head and tail with the truncation announced — never the
	// silently missing middle.
	betaReview := reviewPrompts["st-2"]
	if !strings.Contains(betaReview, "BETA-HEAD-MARKER") || !strings.Contains(betaReview, "BETA-TAIL-MARKER") {
		t.Fatal("truncated review prompt lost the artifact head or tail")
	}
	if strings.Contains(betaReview, "BETA-MIDDLE-MARKER") {
		t.Fatal("oversized artifact was not truncated for review")
	}
	if !strings.Contains(betaReview, "artifact truncated for review") {
		t.Fatal("truncation is not announced inside the review prompt")
	}
	// The ship gate judged artifact bodies too, labelled per subtask.
	if !strings.Contains(gatePrompt, "Produced artifacts") || !strings.Contains(gatePrompt, "### st-1 — Alpha") {
		t.Fatalf("gate prompt has no per-subtask artifact section:\n%.400s", gatePrompt)
	}
	if !strings.Contains(gatePrompt, "ALPHA-TAIL-MARKER") {
		t.Fatal("gate prompt lost Alpha's artifact body")
	}
	if strings.Contains(gatePrompt, "BETA-MIDDLE-MARKER") {
		t.Fatal("gate prompt was not capped against the runaway artifact")
	}
}

func TestGoalReviewArtifactBodyTruncation(t *testing.T) {
	small := "a modest artifact body"
	if got := goalReviewArtifactBody("  " + small + "  "); got != small {
		t.Fatalf("small body altered: %q", got)
	}
	exact := strings.Repeat("x", goalReviewArtifactCap)
	if got := goalReviewArtifactBody(exact); got != exact {
		t.Fatal("body at exactly the cap must pass through untouched")
	}

	half := goalReviewArtifactCap / 2
	head := "HEAD" + strings.Repeat("h", half-4)
	tail := strings.Repeat("t", half-4) + "TAIL"
	middle := strings.Repeat("m", 10*1024)
	got := goalReviewArtifactBody(head + middle + tail)
	if !strings.HasPrefix(got, head) || !strings.HasSuffix(got, tail) {
		t.Fatal("truncation must keep the exact head and tail halves")
	}
	if strings.Contains(got, "mmmm") {
		t.Fatal("truncation kept the middle")
	}
	want := fmt.Sprintf("[... artifact truncated for review: %d bytes omitted from the middle ...]", 10*1024)
	if !strings.Contains(got, want) {
		t.Fatalf("truncation notice missing or wrong; got:\n%.200s", got[half-50:half+150])
	}
	if len(got) > goalReviewArtifactCap+256 {
		t.Fatalf("truncated body length %d blows past the cap", len(got))
	}
}

// The advisory percent never runs backwards while a subtask revises, and a
// re-queued subtask surfaces an honest "revising (attempt N of 2)" signal.
func TestGoalPersistProgressIsMonotonicWithRevisionNote(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	engine := newGoalEngine(app)

	plan := goalPlan{
		PlanVersion: goalPlanVersion, GoalID: "g1", Objective: "x", State: goalStateReview,
		Subtasks: []goalSubtask{{ID: "st-1", Title: "A", Mode: "design", Status: subtaskComplete,
			Review: &goalSubtaskReview{Verdict: goalReviewPass}}},
		Gate: goalGate{Status: "pending"}, Verification: goalVerification{Verdict: "pending"},
	}
	raw, _ := json.Marshal(plan)
	parent, _, err := app.createOSArtifactWithMetadata("workflow", "x", "body", "aj@shareability.com", map[string]string{
		"mode": "goal", "goalPlan": string(raw), "currentStage": goalStateReview,
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	engine.persist(&plan, parent.ID, "")
	high := mustArtifact(t, app, parent.ID).Metadata["progressPercent"]
	if high == "" {
		t.Fatal("no progressPercent stamped")
	}

	// Simulate a revision: the subtask reverts to running with a revision count
	// and the state falls back to execute. The raw execute percent is lower.
	plan.State = goalStateExecute
	plan.Subtasks[0].Status = subtaskRunning
	plan.Subtasks[0].Revisions = 1
	engine.persist(&plan, parent.ID, "")
	art := mustArtifact(t, app, parent.ID)
	if art.Metadata["progressPercent"] != high {
		t.Fatalf("progress ran backwards on revision: %q -> %q (want monotonic)", high, art.Metadata["progressPercent"])
	}
	if art.Metadata["goalRevisionNote"] != "revising (attempt 1 of 2)" {
		t.Fatalf("revision note=%q, want 'revising (attempt 1 of 2)'", art.Metadata["goalRevisionNote"])
	}
}

// A whole goal fires exactly ONE creator notification (on the terminal state),
// not one per subtask/revision.
func TestGoalEngineNotifiesCreatorOnceOnTerminal(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{
		decompose: `{"subtasks":[
			{"id":"st-1","title":"A","mode":"research","authority":"read_only","dependsOn":[]},
			{"id":"st-2","title":"B","mode":"research","authority":"read_only","dependsOn":[]}
		]}`,
	})
	folds := installAwaitableChildRunner(t, "subtask output")

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Two probes", CreatedBy: "aj@shareability.com"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)
	waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)
	folds.Wait() // the terminal finish() (notify + broadcast) has fully landed

	// Exactly one notification references the goal artifact — the terminal one
	// from finish() — even though two subtasks ran to completion underneath.
	app.mu.Lock()
	goalNotes := 0
	for _, record := range app.notifications {
		if record.ArtifactID == thread.Artifact.ID {
			goalNotes++
		}
	}
	app.mu.Unlock()
	if goalNotes != 1 {
		t.Fatalf("goal fired %d notifications, want exactly 1 (terminal only)", goalNotes)
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
