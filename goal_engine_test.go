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
	// Process runtime routes (Wave 4 item 17): the inline stage model calls.
	processGate string // the "process gate scorer" dimensions JSON
	synthesis   string // the "panel synthesizer" output
	stage       string // the "process stage synthesizer" single-voice output
	// fallback answers any OTHER system prompt (stage-authored panel persona
	// prompts are arbitrary text). Empty keeps the strict unexpected-prompt
	// failure for every non-process test.
	fallback string
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
		case strings.Contains(system, "process gate scorer"):
			text = firstNonEmptyString(routes.processGate, `{"dimensions":[{"name":"Quality","score":9.5,"gap":""},{"name":"Completeness","score":9.0,"gap":""}],"reasons":"clears the bar"}`)
		case strings.Contains(system, "panel synthesizer"):
			text = firstNonEmptyString(routes.synthesis, "Synthesis: the panel agrees.")
		case strings.Contains(system, "process stage synthesizer"):
			text = firstNonEmptyString(routes.stage, "Stage output.")
		default:
			if routes.fallback == "" {
				t.Fatalf("unexpected system prompt: %q", request.System)
			}
			text = routes.fallback
		}
		return anthropicMessagesResponse{StopReason: "end_turn", Content: []json.RawMessage{mockAnthropicTextBlock(text)}}, nil
	}
}

type capturedChild struct {
	threadID  string
	subtaskID string
	authority string
	mode      string
	query     string
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
	var wg sync.WaitGroup
	// The fake child folds back on its own goroutine (production's async path).
	// Drain every in-flight child before restoring the hook so no goroutine
	// outlives the test and races the next test's kanbanApp global swap.
	t.Cleanup(func() {
		wg.Wait()
		startAgentThreadAsync = original
	})
	startAgentThreadAsync = func(app *kanbanBoardApp, thread scoutAgentThread) {
		meta := thread.Artifact.Metadata
		mu.Lock()
		*launched = append(*launched, capturedChild{
			threadID:  thread.ID,
			subtaskID: meta["goalSubtaskId"],
			authority: meta["authority"],
			mode:      thread.Mode,
			query:     thread.Query,
		})
		mu.Unlock()
		parent := meta["goalParentId"]
		sub := meta["goalSubtaskId"]
		if parent == "" {
			return
		}
		// Add on the dispatching goroutine (before the spawn) so every child is
		// counted before the cleanup's Wait() runs; re-dispatched revisions Add
		// while an earlier fold is still running, so the counter never hits zero
		// mid-run.
		wg.Add(1)
		go func() {
			defer wg.Done()
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

func TestGoalLaunchPreservesDisplayNameAndCanonicalPrincipal(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})
	thread, err := app.launchGoalThread(goalLaunchSpec{
		Objective: "Prepare the company brief",
		CreatedBy: "AJ",
		Origin:    map[string]string{"requestedBy": "AJ@Shareability.com"},
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	plan := mustGoalPlan(t, app, thread.Artifact.ID)
	if plan.CreatedBy != "AJ" || plan.RequestedBy != "aj@shareability.com" {
		t.Fatalf("goal identity display=%q principal=%q", plan.CreatedBy, plan.RequestedBy)
	}
	if got := thread.Artifact.Metadata["requestedBy"]; got != "aj@shareability.com" {
		t.Fatalf("artifact canonical principal=%q", got)
	}
	if got := goalPlanRequestedBy(plan); got != "aj@shareability.com" {
		t.Fatalf("child principal=%q", got)
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
	artifact, ok := kanbanApp.osArtifactByID(artifactID)
	if !ok {
		t.Fatalf("artifact %s not found", artifactID)
	}
	threadID := artifact.Metadata["threadId"]
	payload := signedCodexCallbackPayload("runner-secret", codexRunnerCallbackPayload{
		JobID:      jobID,
		ArtifactID: artifactID,
		ThreadID:   threadID,
		Status:     status,
		Text:       "Vision: shipped\n\n## Codex worker evidence\n- Worker: codex exec",
		Metadata:   map[string]string{"runnerId": "test-runner"},
	})
	body, err := json.Marshal(payload)
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
	// Drain any child still folding before restoring the hook, so a test that
	// forgets to Wait() on the returned group cannot leak a goroutine into the
	// next test's kanbanApp global swap.
	t.Cleanup(func() {
		folds.Wait()
		startAgentThreadAsync = original
	})
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

// --- Protect lists: praise survives the requeue (Phase 1 mechanisms) ----------

// A revise verdict's strengths_to_keep must reach the revision child as a
// "DO NOT LOSE (protected)" block in the requeue prompt, and the accumulated
// list persists on the plan (goal artifact metadata) so later rounds inherit
// round-1 praise instead of relying on any single review.
func TestGoalRequeueCarriesProtectListIntoRevisionPrompt(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	var mu sync.Mutex
	reviews := 0
	responder := func(_ context.Context, apiKey string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		if apiKey != "test-key" {
			t.Fatalf("apiKey=%q, want test-key", apiKey)
		}
		system := strings.ToLower(request.System)
		text := ""
		switch {
		case strings.Contains(system, "decomposer"):
			text = `{"subtasks":[{"id":"st-1","title":"Draft","mode":"design","authority":"workspace_write","dependsOn":[]}]}`
		case strings.Contains(system, "reviewer"):
			mu.Lock()
			reviews++
			n := reviews
			mu.Unlock()
			if n == 1 {
				text = `{"verdict":"revise","score":6,"reasons":"the ask is buried","strengths_to_keep":["the comps table is airtight","the receipts appendix"]}`
			} else {
				// Round 2 repeats one praise (dedupe) and adds a new one (merge).
				text = `{"verdict":"pass","score":9,"reasons":"good now","strengths_to_keep":["The comps table is airtight","the tightened logline"]}`
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

	// A child runner that records every launch's query — the requeue prompt is
	// the thing under test.
	var queries []string
	originalChild := startAgentThreadAsync
	t.Cleanup(func() { startAgentThreadAsync = originalChild })
	startAgentThreadAsync = func(app *kanbanBoardApp, thread scoutAgentThread) {
		meta := thread.Artifact.Metadata
		parent, sub := meta["goalParentId"], meta["goalSubtaskId"]
		if parent == "" {
			return
		}
		mu.Lock()
		queries = append(queries, thread.Query)
		mu.Unlock()
		go func() {
			child, _, err := app.updateOSArtifactWithMetadata(thread.Artifact.ID, "", "draft body", "tester", map[string]string{
				"threadStatus": "complete",
				"status":       "complete",
			})
			if err != nil {
				return
			}
			app.foldGoalChildCompletion(parent, sub, child, "complete")
		}()
	}

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Produce the Aurora one-pager", CreatedBy: "aj@shareability.com"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)
	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)

	mu.Lock()
	defer mu.Unlock()
	if len(queries) != 2 {
		t.Fatalf("launched %d children, want 2 (initial + one revision)", len(queries))
	}
	if strings.Contains(queries[0], "DO NOT LOSE") {
		t.Fatal("the initial launch must not carry a protect block (nothing has been praised yet)")
	}
	requeue := queries[1]
	if !strings.Contains(requeue, "Revision notes from the goal review (address these): the ask is buried") {
		t.Fatalf("requeue prompt lost the revision notes:\n%s", requeue)
	}
	if !strings.Contains(requeue, "DO NOT LOSE (protected)") {
		t.Fatalf("requeue prompt has no protect block:\n%s", requeue)
	}
	for _, strength := range []string{"- the comps table is airtight", "- the receipts appendix"} {
		if !strings.Contains(requeue, strength) {
			t.Fatalf("requeue prompt protect block missing %q:\n%s", strength, requeue)
		}
	}

	// The accumulated protect list persisted with the plan: round-1 praise plus
	// the round-2 addition, case-insensitively deduped, first-seen order.
	st := plan.subtaskByID("st-1")
	if st == nil {
		t.Fatal("subtask st-1 missing from the final plan")
	}
	want := []string{"the comps table is airtight", "the receipts appendix", "the tightened logline"}
	if len(st.Protect) != len(want) {
		t.Fatalf("persisted protect list %v, want %v", st.Protect, want)
	}
	for i := range want {
		if st.Protect[i] != want[i] {
			t.Fatalf("persisted protect list %v, want %v", st.Protect, want)
		}
	}
}

func TestMergeGoalProtectList(t *testing.T) {
	got := mergeGoalProtectList([]string{"the logline", " ", ""}, []string{"  The Logline  ", "the comps table"})
	want := []string{"the logline", "the comps table"}
	if len(got) != len(want) {
		t.Fatalf("merged=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("merged=%v, want %v", got, want)
		}
	}
	if mergeGoalProtectList(nil, []string{"", "  "}) != nil {
		t.Fatal("all-blank input must merge to nil, not an empty slice")
	}
	// The cap holds, and earlier rounds' entries always survive a later round.
	var many []string
	for i := 0; i < goalProtectListCap+4; i++ {
		many = append(many, fmt.Sprintf("later strength %d", i))
	}
	capped := mergeGoalProtectList([]string{"round one praise"}, many)
	if len(capped) != goalProtectListCap {
		t.Fatalf("merged %d entries, want the cap %d", len(capped), goalProtectListCap)
	}
	if capped[0] != "round one praise" {
		t.Fatalf("existing praise lost under the cap: %v", capped)
	}
}

// --- Law sweeps: deterministic checks spend no reviewer tokens ----------------

// lawSweepCleanOnePagerBody is a one_pager_v1-compliant body: every contract
// heading present, no em dash anywhere.
func lawSweepCleanOnePagerBody() string {
	return strings.Join([]string{
		"Title / Logline: the hook that earns the meeting.",
		"The Thesis: the claim, and why it is true.",
		"Why Now: the timing argument from the market map.",
		"Comparables: the strongest comp and its value signal.",
		"The Team: why this studio for this IP.",
		"The Ask: exactly what we want from the reader.",
		"Sources appendix: every claim mapped to its package source.",
	}, "\n")
}

// A deliverable missing its contract headings is revised mechanically — the
// reviewer model is NEVER called, the verdict is stamped law_sweep, and the
// mechanical reason names what is missing.
func TestGoalLawSweepMissingHeadingSkipsReviewerModel(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	responder := func(_ context.Context, apiKey string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		if apiKey != "test-key" {
			t.Fatalf("apiKey=%q, want test-key", apiKey)
		}
		system := strings.ToLower(request.System)
		text := ""
		switch {
		case strings.Contains(system, "decomposer"):
			text = `{"subtasks":[{"id":"st-1","title":"One-pager","mode":"artifacts","authority":"workspace_write","dependsOn":[]}]}`
		case strings.Contains(system, "reviewer"):
			t.Fatal("law sweep must short-circuit BEFORE the reviewer model; a reviewer call was made")
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

	// The child only ever produces loose prose with no contract structure.
	launches := 0
	var mu sync.Mutex
	originalChild := startAgentThreadAsync
	t.Cleanup(func() { startAgentThreadAsync = originalChild })
	startAgentThreadAsync = func(app *kanbanBoardApp, thread scoutAgentThread) {
		meta := thread.Artifact.Metadata
		parent, sub := meta["goalParentId"], meta["goalSubtaskId"]
		if parent == "" {
			return
		}
		mu.Lock()
		launches++
		mu.Unlock()
		go func() {
			child, _, err := app.updateOSArtifactWithMetadata(thread.Artifact.ID, "", "A page of loose prose with no contract headings at all.", "tester", map[string]string{
				"threadStatus": "complete",
				"status":       "complete",
			})
			if err != nil {
				return
			}
			app.foldGoalChildCompletion(parent, sub, child, "complete")
		}()
	}

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Produce the Aurora one-pager", CreatedBy: "aj@shareability.com", ToolTemplate: "one_pager"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)
	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateBlocked)

	st := plan.subtaskByID("st-1")
	if st == nil || st.Status != subtaskBlocked {
		t.Fatalf("subtask not blocked: %+v", st)
	}
	if st.Revisions != goalMaxRevisions {
		t.Fatalf("revisions=%d, want %d", st.Revisions, goalMaxRevisions)
	}
	if st.Review == nil || st.Review.Verdict != goalReviewRevise || st.Review.By != "law_sweep" {
		t.Fatalf("review not stamped as a mechanical law-sweep verdict: %+v", st.Review)
	}
	if !strings.Contains(st.Review.Reasons, toolLawSweepPrefix) || !strings.Contains(st.Review.Reasons, "The Ask") {
		t.Fatalf("mechanical reason does not name the missing heading: %q", st.Review.Reasons)
	}
	if !strings.Contains(plan.Blocker, toolLawSweepPrefix) {
		t.Fatalf("blocker line lost the mechanical reason: %q", plan.Blocker)
	}
	mu.Lock()
	defer mu.Unlock()
	if launches != goalMaxRevisions+1 {
		t.Fatalf("launched %d children, want %d (initial + revisions)", launches, goalMaxRevisions+1)
	}
}

// An em dash on a client-facing contract is revised mechanically (no reviewer
// call); the clean revision then reaches the reviewer model exactly once and
// the goal verifies.
func TestGoalLawSweepEmDashShortCircuitsThenCleanCopyReachesReviewer(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	clean := lawSweepCleanOnePagerBody()
	dirty := clean + "\nOne more line with an em dash — the copy law bans this."

	var mu sync.Mutex
	reviewerCalls := 0
	responder := func(_ context.Context, apiKey string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		if apiKey != "test-key" {
			t.Fatalf("apiKey=%q, want test-key", apiKey)
		}
		system := strings.ToLower(request.System)
		text := ""
		switch {
		case strings.Contains(system, "decomposer"):
			text = `{"subtasks":[{"id":"st-1","title":"One-pager","mode":"artifacts","authority":"workspace_write","dependsOn":[]}]}`
		case strings.Contains(system, "reviewer"):
			userText := decodeAnthropicBlock(request.Messages[0].Content[0]).Text
			mu.Lock()
			reviewerCalls++
			mu.Unlock()
			if strings.Contains(userText, "em dash — the copy law") {
				t.Fatal("the dirty draft reached the reviewer model; the law sweep did not short-circuit")
			}
			text = `{"verdict":"pass","score":9,"reasons":"clean and compliant","strengths_to_keep":[]}`
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

	// Attempt 1 emits the em-dashed draft; the revision emits the clean one.
	attempts := 0
	var queries []string
	originalChild := startAgentThreadAsync
	t.Cleanup(func() { startAgentThreadAsync = originalChild })
	startAgentThreadAsync = func(app *kanbanBoardApp, thread scoutAgentThread) {
		meta := thread.Artifact.Metadata
		parent, sub := meta["goalParentId"], meta["goalSubtaskId"]
		if parent == "" {
			return
		}
		mu.Lock()
		attempts++
		queries = append(queries, thread.Query)
		body := dirty
		if attempts > 1 {
			body = clean
		}
		mu.Unlock()
		go func() {
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

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Produce the Aurora one-pager", CreatedBy: "aj@shareability.com", ToolTemplate: "one_pager"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)
	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)

	mu.Lock()
	defer mu.Unlock()
	if reviewerCalls != 1 {
		t.Fatalf("reviewer model called %d times, want exactly 1 (the sweep pays for round 1)", reviewerCalls)
	}
	if attempts != 2 {
		t.Fatalf("child launched %d times, want 2 (initial + one mechanical revision)", attempts)
	}
	// The requeue prompt carried the mechanical reason to the revising child.
	if !strings.Contains(queries[1], toolLawSweepPrefix) || !strings.Contains(queries[1], "em dash") {
		t.Fatalf("requeue prompt lost the mechanical em-dash reason:\n%s", queries[1])
	}
	st := plan.subtaskByID("st-1")
	if st == nil || st.Revisions != 1 || st.Review == nil || st.Review.Verdict != goalReviewPass || st.Review.By != "reviewer_model" {
		t.Fatalf("final review state wrong: %+v (review %+v)", st, st.Review)
	}
}

// toolLawSweep unit coverage: heading presence, the client-facing em-dash law,
// and the exemption for non-client-facing contracts.
func TestToolLawSweep(t *testing.T) {
	onePager, ok := toolByID("one_pager")
	if !ok || !onePager.ClientFacing {
		t.Fatal("one_pager must exist and be client-facing")
	}
	clean := lawSweepCleanOnePagerBody()
	if reason, violated := toolLawSweep(onePager, clean); violated {
		t.Fatalf("compliant body flagged: %q", reason)
	}

	missing := strings.Replace(clean, "The Ask", "What we want", 1)
	reason, violated := toolLawSweep(onePager, missing)
	if !violated || !strings.Contains(reason, "The Ask") {
		t.Fatalf("missing heading not flagged by name: violated=%v reason=%q", violated, reason)
	}
	if !strings.HasPrefix(reason, toolLawSweepPrefix) {
		t.Fatalf("law-sweep reason must open with %q: %q", toolLawSweepPrefix, reason)
	}

	dashed := clean + "\nAn em dash sneaks in — right here."
	reason, violated = toolLawSweep(onePager, dashed)
	if !violated || !strings.Contains(reason, "em dash") {
		t.Fatalf("client-facing em dash not flagged: violated=%v reason=%q", violated, reason)
	}

	// Internal contracts (research briefs) are exempt from the copy law.
	research, ok := toolByID("deep_research")
	if !ok || research.ClientFacing {
		t.Fatal("deep_research must exist and not be client-facing")
	}
	researchBody := strings.Join(toolContractHeadings["research_brief_v2"], "\n") + "\nAn em dash — fine in an internal brief."
	if reason, violated := toolLawSweep(research, researchBody); violated {
		t.Fatalf("internal contract wrongly swept for em dashes: %q", reason)
	}
}

// --- User-facing cancel (spec §2 "misfire economics", Wave 2 item 8c) ---------

// installRecordingChildRunner is installFakeChildRunner WITHOUT the synthetic
// completion: children stay running forever, so a test can cancel a goal
// mid-execute and then hand-fold a straggler to prove the fold is a no-op.
// Records subtaskID -> child artifact id.
func installRecordingChildRunner(t *testing.T) *sync.Map {
	t.Helper()
	children := &sync.Map{}
	original := startAgentThreadAsync
	t.Cleanup(func() { startAgentThreadAsync = original })
	startAgentThreadAsync = func(_ *kanbanBoardApp, thread scoutAgentThread) {
		meta := thread.Artifact.Metadata
		if meta["goalParentId"] == "" {
			return
		}
		children.Store(meta["goalSubtaskId"], thread.Artifact.ID)
	}
	return children
}

// One tap mid-execute: the goal parks terminal needs_attention with the cancel
// record, the ready subtask is never dispatched, and a child that finishes
// AFTER the cancel folds into a no-op (no plan mutation, no re-drive).
func TestGoalCancelMidExecuteHaltsDispatchAndFoldsAreNoOps(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{
		decompose: `{"subtasks":[
			{"id":"st-1","title":"A","mode":"research","authority":"read_only","dependsOn":[]},
			{"id":"st-2","title":"B","mode":"research","authority":"read_only","dependsOn":[]},
			{"id":"st-3","title":"C","mode":"research","authority":"read_only","dependsOn":[]}
		]}`,
	})
	children := installRecordingChildRunner(t)

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Three probes to abandon", CreatedBy: "aj@shareability.com"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)

	// Parked at execute: two running (concurrency cap 2), st-3 still ready.
	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateExecute)
	if goalCountStatus(&plan, subtaskRunning) != 2 || goalCountStatus(&plan, subtaskReady) != 1 {
		t.Fatalf("pre-cancel counts wrong: running=%d ready=%d, want 2/1", goalCountStatus(&plan, subtaskRunning), goalCountStatus(&plan, subtaskReady))
	}

	if err := app.cancelGoalThread(thread.Artifact.ID, "aj@shareability.com"); err != nil {
		t.Fatalf("cancelGoalThread: %v", err)
	}

	artifact := mustArtifact(t, app, thread.Artifact.ID)
	if artifact.Metadata["currentStage"] != goalStateBlocked || artifact.Metadata["goalStatus"] != "needs_attention" {
		t.Fatalf("cancel did not land needs_attention: stage=%q status=%q", artifact.Metadata["currentStage"], artifact.Metadata["goalStatus"])
	}
	if artifact.Metadata["cancelled"] != "true" || artifact.Metadata["cancelledBy"] != "aj@shareability.com" || artifact.Metadata["cancelledAt"] == "" {
		t.Fatalf("cancel record not stamped: %v", artifact.Metadata)
	}
	plan = mustGoalPlan(t, app, thread.Artifact.ID)
	if !plan.Cancelled || plan.State != goalStateBlocked || !strings.Contains(plan.Blocker, "cancelled by aj@shareability.com") {
		t.Fatalf("plan not cancelled: cancelled=%v state=%q blocker=%q", plan.Cancelled, plan.State, plan.Blocker)
	}

	// An in-flight child finishes AFTER the cancel: the fold must be a no-op,
	// and critically st-3 (ready) is never dispatched by a re-drive.
	rawChildID, ok := children.Load("st-1")
	if !ok {
		t.Fatal("st-1 child artifact not recorded")
	}
	child, _, err := app.updateOSArtifactWithMetadata(rawChildID.(string), "", "late child output", "tester", map[string]string{
		"threadStatus": "complete",
		"status":       "complete",
	})
	if err != nil {
		t.Fatalf("complete straggler child: %v", err)
	}
	app.foldGoalChildCompletion(thread.Artifact.ID, "st-1", child, "complete")

	after := mustGoalPlan(t, app, thread.Artifact.ID)
	if st := after.subtaskByID("st-1"); st == nil || st.Status != subtaskRunning {
		t.Fatalf("fold after cancel mutated st-1: %+v, want the no-op to leave it running", st)
	}
	if after.State != goalStateBlocked {
		t.Fatalf("fold after cancel re-drove the goal to %q", after.State)
	}
	if _, dispatched := children.Load("st-3"); dispatched {
		t.Fatal("st-3 was dispatched after the cancel — dispatchReady must refuse a cancelled goal")
	}

	// The second tap is an idempotent no-op, not an error.
	if err := app.cancelGoalThread(thread.Artifact.ID, "aj@shareability.com"); err != nil {
		t.Fatalf("second cancel: %v", err)
	}
}

// The misfire signal: one negative goal_cancelled entry carrying the stage at
// cancellation and the tool template — the router's tuning data.
func TestGoalCancelEmitsNegativeMisfireSignal(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})
	installRecordingChildRunner(t)

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Deep research misfire", CreatedBy: "aj@shareability.com", ToolTemplate: "deep_research"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)
	waitForGoalStage(t, app, thread.Artifact.ID, goalStateExecute)

	if err := app.cancelGoalThread(thread.Artifact.ID, "aj@shareability.com"); err != nil {
		t.Fatalf("cancelGoalThread: %v", err)
	}

	var record signalRecord
	count := 0
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindSignal, 0) {
		if decoded, ok := decodeSignalEntry(entry); ok && decoded.Event == signalEventGoalCancelled {
			record = decoded
			count++
		}
	}
	if count != 1 {
		t.Fatalf("goal_cancelled signals=%d, want exactly 1", count)
	}
	if record.Valence != signalValenceNegative || record.Actor != "aj@shareability.com" || record.ArtifactID != thread.Artifact.ID {
		t.Fatalf("signal record wrong: %#v, want negative/actor/goal id", record)
	}
	if record.Payload["stage"] != goalStateExecute {
		t.Fatalf("signal stage=%q, want %q (the stage at cancellation)", record.Payload["stage"], goalStateExecute)
	}
	if record.Payload["toolTemplate"] != "deep_research" {
		t.Fatalf("signal toolTemplate=%q, want deep_research", record.Payload["toolTemplate"])
	}
	// A cancel is a deliberate abandonment: no salvage signal, nothing attached.
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindSignal, 0) {
		if decoded, ok := decodeSignalEntry(entry); ok && decoded.Event == signalEventArtifactSalvaged {
			t.Fatalf("cancel ran the salvage path: %#v", decoded)
		}
	}
}

// A cancelled goal is terminal for cap purposes: the one tap frees the
// requester's in-flight slot immediately.
func TestGoalCancelFreesUserCapHeadroom(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})
	t.Setenv("BONFIRE_GOAL_USER_CAP", "1")

	first, err := app.launchGoalThread(goalLaunchSpec{Objective: "Wrong launch", CreatedBy: "aj@shareability.com"})
	if err != nil {
		t.Fatalf("first launch: %v", err)
	}
	_, err = app.launchGoalThread(goalLaunchSpec{Objective: "The launch that matters", CreatedBy: "aj@shareability.com"})
	var capErr *errGoalUserCapExceeded
	if !errors.As(err, &capErr) {
		t.Fatalf("second launch err=%v, want errGoalUserCapExceeded before the cancel", err)
	}

	if err := app.cancelGoalThread(first.Artifact.ID, "aj@shareability.com"); err != nil {
		t.Fatalf("cancelGoalThread: %v", err)
	}
	if inFlight := app.inFlightGoalsForUser("aj@shareability.com"); len(inFlight) != 0 {
		t.Fatalf("in-flight goals after cancel=%d, want 0: %+v", len(inFlight), inFlight)
	}
	if _, err := app.launchGoalThread(goalLaunchSpec{Objective: "The launch that matters", CreatedBy: "aj@shareability.com"}); err != nil {
		t.Fatalf("launch after cancel still capped: %v", err)
	}
}

// A verified goal has nothing to cancel; the tap is refused, not absorbed.
func TestGoalCancelRefusesVerifiedGoal(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})
	installFakeChildRunner(t)

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Finish cleanly", CreatedBy: "aj@shareability.com"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)
	waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)

	if err := app.cancelGoalThread(thread.Artifact.ID, "aj@shareability.com"); err == nil {
		t.Fatal("cancel accepted a verified goal")
	}
	if plan := mustGoalPlan(t, app, thread.Artifact.ID); plan.Cancelled || plan.State != goalStateVerified {
		t.Fatalf("refused cancel still mutated the plan: cancelled=%v state=%q", plan.Cancelled, plan.State)
	}
}

// The HTTP door: session-gated like /assistant/goal, permitted to the goal's
// requester or the approval admin — never a third teammate.
func TestGoalCancelHTTPAuthorization(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	installFakeResponder(t, goalResponderRoutes{})
	installRecordingChildRunner(t)

	thread, err := kanbanApp.launchGoalThread(goalLaunchSpec{Objective: "Joel's goal", CreatedBy: "joel@shareability.com"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	kanbanApp.runGoalThread(thread.Artifact.ID)
	waitForGoalStage(t, kanbanApp, thread.Artifact.ID, goalStateExecute)

	post := func(cookies []*http.Cookie, goalID string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"goalId": goalID})
		req := httptest.NewRequest(http.MethodPost, "/assistant/goal/cancel", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://localhost")
		req.Host = "localhost"
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		assistantGoalCancelHandler(rec, req)
		return rec
	}

	// A teammate who neither requested the goal nor is the admin gets 403, and
	// the goal keeps running.
	if rec := post(loginAs(t, "tyler@shareability.com", defaultMeetingRoomPassword), thread.Artifact.ID); rec.Code != http.StatusForbidden {
		t.Fatalf("non-requester cancel status=%d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusForbidden)
	}
	if got := mustArtifact(t, kanbanApp, thread.Artifact.ID).Metadata["currentStage"]; got != goalStateExecute {
		t.Fatalf("403 cancel still mutated the goal: stage=%q", got)
	}

	// Not signed in -> 401.
	if rec := post(nil, thread.Artifact.ID); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous cancel status=%d, want %d", rec.Code, http.StatusUnauthorized)
	}

	// The requester cancels their own goal.
	joel := loginAs(t, "joel@shareability.com", defaultMeetingRoomPassword)
	if rec := post(joel, thread.Artifact.ID); rec.Code != http.StatusOK {
		t.Fatalf("requester cancel status=%d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}
	artifact := mustArtifact(t, kanbanApp, thread.Artifact.ID)
	if artifact.Metadata["cancelled"] != "true" || artifact.Metadata["currentStage"] != goalStateBlocked {
		t.Fatalf("requester cancel did not land: %v", artifact.Metadata)
	}
	if artifact.Metadata["cancelledBy"] != "joel@shareability.com" {
		t.Fatalf("cancelledBy=%q, want the requester", artifact.Metadata["cancelledBy"])
	}

	// The admin can cancel someone else's goal (admin-equivalent, mirroring the
	// approval gate's authorization).
	second, err := kanbanApp.launchGoalThread(goalLaunchSpec{Objective: "Joel's second goal", CreatedBy: "joel@shareability.com"})
	if err != nil {
		t.Fatalf("second launch: %v", err)
	}
	admin := loginAs(t, artifactLibraryAdminEmail, defaultMeetingRoomPassword)
	if rec := post(admin, second.Artifact.ID); rec.Code != http.StatusOK {
		t.Fatalf("admin cancel status=%d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}
	if got := mustArtifact(t, kanbanApp, second.Artifact.ID).Metadata["cancelledBy"]; got != artifactLibraryAdminEmail {
		t.Fatalf("admin cancelledBy=%q, want %q", got, artifactLibraryAdminEmail)
	}

	// Unknown goal -> 404; a non-goal artifact -> 400.
	if rec := post(joel, "no-such-goal"); rec.Code != http.StatusNotFound {
		t.Fatalf("missing goal status=%d, want %d", rec.Code, http.StatusNotFound)
	}
	plain, _, err := kanbanApp.createOSArtifact("research", "not a goal", "just a brief", "Joel")
	if err != nil {
		t.Fatalf("createOSArtifact: %v", err)
	}
	if rec := post(joel, plain.ID); rec.Code != http.StatusBadRequest {
		t.Fatalf("non-goal cancel status=%d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// --- Panel primitive (Wave 3 item 12) ------------------------------------------

// newPanelTestEngine builds an engine with an injected responder and key, so
// panel tests never touch the global responder/env swap.
func newPanelTestEngine(t *testing.T, responder anthropicMessagesResponder) *goalEngine {
	t.Helper()
	engine := newGoalEngine(newIsolatedKanbanBoardApp(t))
	engine.apiKey = func() string { return "test-key" }
	engine.responder = responder
	return engine
}

// N personas fan out in parallel inside ONE engine step (no subtasks), each
// with its OWN system prompt plus the SHARED strict-JSON schema, and the
// synthesis call sees all N replies attributed by persona.
func TestRunGoalPanelFanOutAndSynthesisSeesAllVoices(t *testing.T) {
	var mu sync.Mutex
	personaSystems := []string{}
	synthesisUser := ""
	responder := func(_ context.Context, apiKey string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		if apiKey != "test-key" {
			t.Fatalf("apiKey=%q, want test-key", apiKey)
		}
		userText := decodeAnthropicBlock(request.Messages[0].Content[0]).Text
		text := ""
		switch {
		case strings.Contains(request.System, "PERSONA-ALPHA"):
			text = `{"vote":"alpha-verdict"}`
		case strings.Contains(request.System, "PERSONA-BETA"):
			text = `{"vote":"beta-verdict"}`
		case strings.Contains(request.System, "PERSONA-GAMMA"):
			text = `{"vote":"gamma-verdict"}`
		case strings.Contains(request.System, "SYNTH-MARKER"):
			mu.Lock()
			synthesisUser = userText
			mu.Unlock()
			return anthropicMessagesResponse{StopReason: "end_turn", Content: []json.RawMessage{mockAnthropicTextBlock("the synthesized result")}}, nil
		default:
			t.Fatalf("unexpected system prompt: %q", request.System)
		}
		mu.Lock()
		personaSystems = append(personaSystems, request.System)
		mu.Unlock()
		if userText != "the shared task" {
			t.Fatalf("persona user prompt=%q, want the shared task", userText)
		}
		return anthropicMessagesResponse{StopReason: "end_turn", Content: []json.RawMessage{mockAnthropicTextBlock(text)}}, nil
	}
	engine := newPanelTestEngine(t, responder)

	outcome, err := engine.runGoalPanel(context.Background(), goalPanelSpec{
		Task:   "the shared task",
		Schema: "SHARED-SCHEMA: return strict JSON",
		Personas: []goalPanelPersona{
			{Name: "alpha", System: "PERSONA-ALPHA"},
			{Name: "beta", System: "PERSONA-BETA"},
			{Name: "gamma", System: "PERSONA-GAMMA"},
		},
		Synthesis: "SYNTH-MARKER synthesize the panel",
	})
	if err != nil {
		t.Fatalf("runGoalPanel: %v", err)
	}
	if outcome.Synthesis != "the synthesized result" {
		t.Fatalf("synthesis=%q", outcome.Synthesis)
	}
	// Voices keep panel order and per-persona attribution.
	if len(outcome.Voices) != 3 {
		t.Fatalf("voices=%d, want 3", len(outcome.Voices))
	}
	for index, want := range []struct{ persona, text string }{
		{"alpha", `{"vote":"alpha-verdict"}`},
		{"beta", `{"vote":"beta-verdict"}`},
		{"gamma", `{"vote":"gamma-verdict"}`},
	} {
		voice := outcome.Voices[index]
		if voice.Persona != want.persona || voice.Text != want.text || voice.Err != nil {
			t.Fatalf("voice[%d]=%+v, want %+v", index, voice, want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	// Every persona call carried the shared schema appended to its own prompt.
	if len(personaSystems) != 3 {
		t.Fatalf("persona calls=%d, want 3", len(personaSystems))
	}
	for _, system := range personaSystems {
		if !strings.Contains(system, "SHARED-SCHEMA") {
			t.Fatalf("persona system missing the shared schema: %q", system)
		}
	}
	// The synthesis saw ALL N replies, attributed.
	for _, want := range []string{"Panelist: alpha", "Panelist: beta", "Panelist: gamma", "alpha-verdict", "beta-verdict", "gamma-verdict", "the shared task"} {
		if !strings.Contains(synthesisUser, want) {
			t.Fatalf("synthesis prompt missing %q:\n%s", want, synthesisUser)
		}
	}
}

// A failed seat degrades per-persona (the synthesizer is told, honestly); a
// panel where EVERY seat failed returns an error instead of synthesizing air.
func TestRunGoalPanelDegradesPerSeatAndFailsWhenAllSeatsFail(t *testing.T) {
	var synthesisUser string
	responder := func(_ context.Context, _ string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		switch {
		case strings.Contains(request.System, "PERSONA-OK"):
			return anthropicMessagesResponse{StopReason: "end_turn", Content: []json.RawMessage{mockAnthropicTextBlock(`{"vote":"ok"}`)}}, nil
		case strings.Contains(request.System, "PERSONA-DOWN"):
			return anthropicMessagesResponse{}, errors.New("seat is down")
		default:
			synthesisUser = decodeAnthropicBlock(request.Messages[0].Content[0]).Text
			return anthropicMessagesResponse{StopReason: "end_turn", Content: []json.RawMessage{mockAnthropicTextBlock("partial synthesis")}}, nil
		}
	}
	engine := newPanelTestEngine(t, responder)

	outcome, err := engine.runGoalPanel(context.Background(), goalPanelSpec{
		Task: "task",
		Personas: []goalPanelPersona{
			{Name: "ok", System: "PERSONA-OK"},
			{Name: "down", System: "PERSONA-DOWN"},
		},
	})
	if err != nil {
		t.Fatalf("one live seat must still synthesize: %v", err)
	}
	if outcome.Voices[1].Err == nil {
		t.Fatal("failed seat must keep its Err")
	}
	if outcome.Synthesis != "partial synthesis" {
		t.Fatalf("synthesis=%q", outcome.Synthesis)
	}
	if !strings.Contains(synthesisUser, "this panelist's call failed") {
		t.Fatalf("synthesizer was not told about the failed seat:\n%s", synthesisUser)
	}

	// All seats down: error, no synthesis call.
	allDown := func(_ context.Context, _ string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		if !strings.Contains(request.System, "PERSONA-DOWN") {
			t.Fatalf("no synthesis call may run when every seat failed; got system %q", request.System)
		}
		return anthropicMessagesResponse{}, errors.New("seat is down")
	}
	engine = newPanelTestEngine(t, allDown)
	if _, err := engine.runGoalPanel(context.Background(), goalPanelSpec{
		Task:     "task",
		Personas: []goalPanelPersona{{Name: "down", System: "PERSONA-DOWN"}},
	}); err == nil {
		t.Fatal("an all-failed panel must return an error")
	}
	// An empty panel is a spec bug, not a silent pass.
	if _, err := engine.runGoalPanel(context.Background(), goalPanelSpec{Task: "task"}); err == nil {
		t.Fatal("a persona-less panel must return an error")
	}
}

// --- Gate primitive (Wave 3 item 12) --------------------------------------------

// Threshold + per-dimension floor + bounded rounds + force-accept-with-
// disclosed-gaps, at the SKILL defaults (9.0 / 7.0 / 2 rounds).
func TestRunGoalGateThresholdFloorRoundsAndForceAccept(t *testing.T) {
	dims := func(scores ...float64) []goalGateDimension {
		out := make([]goalGateDimension, 0, len(scores))
		for index, score := range scores {
			out = append(out, goalGateDimension{Name: fmt.Sprintf("dim-%d", index+1), Score: score})
		}
		return out
	}
	cases := []struct {
		name        string
		round       goalGateRound
		spentRounds int
		forceAccept bool
		wantOutcome string
		wantGap     string
	}{
		{"above threshold and floors accepts", goalGateRound{Dimensions: dims(9.5, 9.2, 9.0)}, 0, false, goalGateOutcomeAccept, ""},
		{"floor breach revises while rounds remain", goalGateRound{Dimensions: dims(9.9, 6.0)}, 0, false, goalGateOutcomeRevise, "below the 7.0 floor"},
		{"threshold breach revises while rounds remain", goalGateRound{Dimensions: dims(8.0, 8.5)}, 1, false, goalGateOutcomeRevise, "below the 9.0 threshold"},
		{"rounds spent without force-accept blocks", goalGateRound{Dimensions: dims(8.0, 8.5)}, 2, false, goalGateOutcomeBlocked, "below the 9.0 threshold"},
		{"rounds spent with force-accept ships with disclosed gaps", goalGateRound{Dimensions: dims(8.9, 6.5)}, 2, true, goalGateOutcomeForceAccept, "below the 7.0 floor"},
		{"no verdict and no dimensions never passes", goalGateRound{}, 0, false, goalGateOutcomeRevise, "no verdict and no dimension scores"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := runGoalGate(context.Background(), goalGateSpec{
				Round:       tc.spentRounds,
				ForceAccept: tc.forceAccept,
				Score:       func(context.Context) goalGateRound { return tc.round },
			})
			if decision.Outcome != tc.wantOutcome {
				t.Fatalf("outcome=%q, want %q (gaps=%v)", decision.Outcome, tc.wantOutcome, decision.Gaps)
			}
			if tc.wantGap == "" {
				if len(decision.Gaps) != 0 {
					t.Fatalf("accepting decision must have no gaps: %v", decision.Gaps)
				}
				return
			}
			if !strings.Contains(strings.Join(decision.Gaps, "; "), tc.wantGap) {
				t.Fatalf("gaps=%v, want a gap naming %q", decision.Gaps, tc.wantGap)
			}
		})
	}

	// A dimension gap detail is disclosed verbatim alongside the floor breach.
	decision := runGoalGate(context.Background(), goalGateSpec{
		Round:       goalGateDefaultMaxRounds,
		ForceAccept: true,
		Score: func(context.Context) goalGateRound {
			return goalGateRound{Dimensions: []goalGateDimension{{Name: "persona", Score: 5, Gap: "unanswered: the price point"}}}
		},
	})
	if decision.Outcome != goalGateOutcomeForceAccept || !strings.Contains(strings.Join(decision.Gaps, "; "), "unanswered: the price point") {
		t.Fatalf("force-accept must disclose the dimension gap: %+v", decision)
	}
}

// Rubric-equivalence: the degenerate verdict-driven case behaves exactly like
// today's tool-rubric review — the reviewer's folded verdict decides (a pass
// with a LOW score still accepts; the threshold never second-guesses it), and
// the rounds policy matches requeueOrBlock's goalMaxRevisions bound.
func TestRunGoalGateDegenerateVerdictCasePreservesRubricBehavior(t *testing.T) {
	verdictRound := func(verdict string, score float64, reasons string) func(context.Context) goalGateRound {
		return func(context.Context) goalGateRound {
			return goalGateRound{Verdict: verdict, Score: score, Reasons: reasons}
		}
	}

	pass := runGoalGate(context.Background(), goalGateSpec{MaxRounds: goalMaxRevisions, Round: 0, Score: verdictRound(goalReviewPass, 3, "meets the contract")})
	if pass.Outcome != goalGateOutcomeAccept || pass.Verdict != goalReviewPass || pass.Score != 3 {
		t.Fatalf("verdict pass with a low score must accept (the model's rubric verdict decides): %+v", pass)
	}

	for spent := 0; spent < goalMaxRevisions; spent++ {
		revise := runGoalGate(context.Background(), goalGateSpec{MaxRounds: goalMaxRevisions, Round: spent, Score: verdictRound(goalReviewFail, 4, "does not meet the goal")})
		if revise.Outcome != goalGateOutcomeRevise || revise.Verdict != goalReviewFail {
			t.Fatalf("round %d: outcome=%q verdict=%q, want revise/fail while rounds remain", spent, revise.Outcome, revise.Verdict)
		}
	}
	blocked := runGoalGate(context.Background(), goalGateSpec{MaxRounds: goalMaxRevisions, Round: goalMaxRevisions, Score: verdictRound(goalReviewRevise, 6, "the ask is buried")})
	if blocked.Outcome != goalGateOutcomeBlocked || blocked.Verdict != goalReviewRevise || blocked.Reasons != "the ask is buried" {
		t.Fatalf("rounds spent must block (no force-accept in the rubric case): %+v", blocked)
	}
}

// --- save_what_worked distills real lessons (Wave 3 items 12/15) ----------------

// The save stage distills 2-4 one-line lessons — reviewer praise that survived
// (protect list), what needed revision, what the gate cleared — into the goal
// artifact metadata (savedLessons) AND exactly one goal_lessons signal for the
// Taste Analyst. Zero extra model calls.
func TestGoalSaveWhatWorkedDistillsLessonsAndEmitsSignal(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	var mu sync.Mutex
	reviews := 0
	responder := func(_ context.Context, apiKey string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		if apiKey != "test-key" {
			t.Fatalf("apiKey=%q, want test-key", apiKey)
		}
		system := strings.ToLower(request.System)
		text := ""
		switch {
		case strings.Contains(system, "decomposer"):
			text = `{"subtasks":[{"id":"st-1","title":"Draft","mode":"design","authority":"workspace_write","dependsOn":[]}]}`
		case strings.Contains(system, "reviewer"):
			mu.Lock()
			reviews++
			n := reviews
			mu.Unlock()
			if n == 1 {
				text = `{"verdict":"revise","score":6,"reasons":"the ask is buried","strengths_to_keep":["the comps table is airtight"]}`
			} else {
				text = `{"verdict":"pass","score":9,"reasons":"good now","strengths_to_keep":[]}`
			}
		case strings.Contains(system, "ship gate"):
			text = `{"safe":true,"external_write_required":false,"command":"","reason":"complete and grounded"}`
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
	// A contract-compliant body so the deep_research law sweep passes and the
	// reviewer model (revise -> pass) is what drives the lessons.
	folds := installAwaitableChildRunner(t, strings.Join(toolContractHeadings["research_brief_v2"], "\n"))

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Produce the Aurora one-pager", CreatedBy: "aj@shareability.com", ToolTemplate: "deep_research"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)
	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)
	folds.Wait()

	// The plan carries the distilled lessons, mirrored to artifact metadata.
	if len(plan.Report.SavedLessons) < 2 || len(plan.Report.SavedLessons) > goalLessonsMax {
		t.Fatalf("savedLessons=%v, want 2-%d one-line lessons", plan.Report.SavedLessons, goalLessonsMax)
	}
	artifact := mustArtifact(t, app, thread.Artifact.ID)
	var lessons []string
	if err := json.Unmarshal([]byte(artifact.Metadata["savedLessons"]), &lessons); err != nil {
		t.Fatalf("savedLessons metadata not JSON: %v (%q)", err, artifact.Metadata["savedLessons"])
	}
	joined := strings.Join(lessons, " | ")
	for _, want := range []string{
		"the comps table is airtight",         // protect-list survivor (what the review praised)
		"needed 1 revision",                   // what needed revision before it passed
		"Gate cleared: complete and grounded", // what the gate said on the way out
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("lessons missing %q: %v", want, lessons)
		}
	}
	if !strings.Contains(artifact.Text, "## What worked") {
		t.Fatal("goal brief missing the What worked section")
	}

	// Exactly one goal_lessons signal, positive, addressed to the Taste Analyst.
	var record signalRecord
	count := 0
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindSignal, 0) {
		if decoded, ok := decodeSignalEntry(entry); ok && decoded.Event == signalEventGoalLessons {
			record = decoded
			count++
		}
	}
	if count != 1 {
		t.Fatalf("goal_lessons signals=%d, want exactly 1", count)
	}
	if record.Valence != signalValencePositive || record.Actor != "aj@shareability.com" || record.ArtifactID != thread.Artifact.ID {
		t.Fatalf("signal record wrong: %#v", record)
	}
	if !strings.Contains(record.Payload["lessons"], "the comps table is airtight") {
		t.Fatalf("signal payload lessons=%q, want the protect-list survivor", record.Payload["lessons"])
	}
	if record.Payload["toolTemplate"] != "deep_research" {
		t.Fatalf("signal toolTemplate=%q, want deep_research", record.Payload["toolTemplate"])
	}
}

// --- Taste pinning into deliverable grounding (Wave 3 item 15) ----------------

// The wrapper grounding slots carry the office house style unconditionally and
// the requester's living profile when the requester-aware variant is used —
// pinned, never found by lexical slot-filling (packaging-os §5).
func TestGoalGroundingSlotsPinProfileAndHouseStyle(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	// Fresh office: nothing to pin, slots unchanged.
	_, _, _, memory := app.goalGroundingSlotsForRequester("", "aj@shareability.com")
	if strings.Contains(memory, "(pinned)") {
		t.Fatalf("empty office must pin nothing into the memory slot:\n%s", memory)
	}

	seedTasteProfileArtifact(t, app, "AJ", "Kill every unnamed comp. [sig-3]")
	seedHouseStyleArtifact(t, app, "House rule: every claim carries a receipt.")

	// The base slots (also used by the generation hop, toolPromptForThread)
	// carry the house style but never a requester profile.
	_, _, _, memory = app.goalGroundingSlots("")
	if !strings.Contains(memory, "Office house style (pinned):") || !strings.Contains(memory, "every claim carries a receipt") {
		t.Fatalf("base grounding slots lost the house style:\n%s", memory)
	}
	if strings.Contains(memory, "Requester taste profile (pinned):") {
		t.Fatalf("base grounding slots must not guess a requester:\n%s", memory)
	}

	// The requester-aware slots add the profile ahead of everything else.
	_, _, _, memory = app.goalGroundingSlotsForRequester("", "aj@shareability.com")
	if !strings.Contains(memory, "Requester taste profile (pinned):") || !strings.Contains(memory, "Kill every unnamed comp.") {
		t.Fatalf("requester grounding slots lost the profile:\n%s", memory)
	}
	if !strings.Contains(memory, "Office house style (pinned):") {
		t.Fatalf("requester grounding slots lost the house style:\n%s", memory)
	}

	// A requester without a profile degrades to the base slots.
	_, _, _, memory = app.goalGroundingSlotsForRequester("", "tom@shareability.com")
	if strings.Contains(memory, "Requester taste profile (pinned):") {
		t.Fatalf("profile-less requester must not pin a profile:\n%s", memory)
	}
}

// The deliverable wrapper context (what the decomposer hands the final,
// contract-bearing subtask) carries the requester's pinned profile via
// toolPromptContextForPlan.
func TestToolPromptContextForPlanCarriesPinnedProfile(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	seedTasteProfileArtifact(t, app, "AJ", "Lead with rights, not reach. [sig-9]")
	seedHouseStyleArtifact(t, app, "Banned: momentum language without numbers.")

	tool, ok := toolByID("one_pager")
	if !ok {
		t.Fatal("one_pager tool missing from the registry")
	}
	engine := newGoalEngine(app)
	ctx := engine.toolPromptContextForPlan(&goalPlan{Objective: "Package Aurora", CreatedBy: "aj@shareability.com"}, tool)
	if !strings.Contains(ctx.RelevantMemory, "Requester taste profile (pinned):") || !strings.Contains(ctx.RelevantMemory, "Lead with rights, not reach.") {
		t.Fatalf("deliverable grounding lost the requester profile:\n%s", ctx.RelevantMemory)
	}
	if !strings.Contains(ctx.RelevantMemory, "Office house style (pinned):") {
		t.Fatalf("deliverable grounding lost the house style:\n%s", ctx.RelevantMemory)
	}
	// The pinned block must survive into the assembled wrapper the decomposer
	// injects, not fall back to the "(none on record)" default.
	prompt := assembleToolPrompt(tool, ctx)
	if !strings.Contains(prompt, "Requester taste profile (pinned):") {
		t.Fatalf("assembled tool prompt lost the pinned profile:\n%.600s", prompt)
	}
}

// --- Review-model split (Wave 3 item 16) ---------------------------------------

// Per-subtask review and the ship gate route to BONFIRE_REVIEW_MODEL while
// orchestration (decompose, report, verify) stays on the orchestrator model —
// the reviewer reads whole artifacts, which wants Opus context, not the Fable
// ceiling.
func TestGoalReviewAndGateRouteToReviewModel(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("BONFIRE_ORCHESTRATOR_MODEL", "")
	t.Setenv("BONFIRE_REVIEW_MODEL", "review-model-x")

	var mu sync.Mutex
	models := map[string]string{}
	responder := func(_ context.Context, apiKey string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		if apiKey != "test-key" {
			t.Fatalf("apiKey=%q, want test-key", apiKey)
		}
		system := strings.ToLower(request.System)
		text := ""
		stage := ""
		switch {
		case strings.Contains(system, "decomposer"):
			stage = "decompose"
			text = `{"subtasks":[{"id":"st-1","title":"Market map","mode":"research","authority":"read_only","dependsOn":[]}]}`
		case strings.Contains(system, "reviewer"):
			stage = "review"
			text = `{"verdict":"pass","score":9,"reasons":"meets the subtask"}`
		case strings.Contains(system, "ship gate"):
			stage = "gate"
			text = `{"safe":true,"external_write_required":false,"command":"","reason":"safe"}`
		case strings.Contains(system, "reporting a finished goal"):
			stage = "report"
			text = `{"changed":"x","headline":"done","gap":"","next":"","assumed_claim_count":0}`
		case strings.Contains(system, "final verifier"):
			stage = "verify"
			text = `{"verdict":"pass","reasons":"ok"}`
		default:
			t.Fatalf("unexpected system prompt: %q", request.System)
		}
		mu.Lock()
		models[stage] = request.Model
		mu.Unlock()
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
	installFakeChildRunner(t)

	thread, err := app.launchGoalThread(goalLaunchSpec{Objective: "Route review to the review model", CreatedBy: "aj@shareability.com"})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)
	waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)

	mu.Lock()
	defer mu.Unlock()
	for _, stage := range []string{"review", "gate"} {
		if models[stage] != "review-model-x" {
			t.Fatalf("%s model=%q, want the review model", stage, models[stage])
		}
	}
	for _, stage := range []string{"decompose", "report", "verify"} {
		if models[stage] != orchestratorModel() {
			t.Fatalf("%s model=%q, want the orchestrator model %q", stage, models[stage], orchestratorModel())
		}
	}
}

// --- The ProcessDefinition runtime (spec §3, Wave 4 item 17) -------------------

// The probe process (writer → gate → human_checkpoint) drives the whole
// runtime: decompose instantiates the authored stages IN ORDER (never the
// free-form decomposer — its route is poisoned here), the writer dispatches as
// the only child, the gate scores inline, the checkpoint parks the goal
// approval_required-style with the {stageId, question, options} metadata, and
// resume-with-choice lands the choice and carries the goal to verified.
func TestProcessGoalInstantiatesStagesParksAndResumesWithChoice(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{
		// If the free-form decomposer ran, this would block the goal at
		// decompose — instantiation must never call it.
		decompose: "not json — the decomposer must not run for a process goal",
	})
	launched := installFakeChildRunner(t)

	thread, err := app.launchGoalThread(goalLaunchSpec{
		Objective:    "Probe the process runtime",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "process_probe",
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)

	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateApproval)
	if plan.ProcessID != "process_probe" {
		t.Fatalf("plan processId=%q, want process_probe", plan.ProcessID)
	}
	wantStages := []struct{ id, role, status string }{
		{"draft", processRoleWriter, subtaskComplete},
		{"note_gate", processRoleGate, subtaskComplete},
		{"ship_choice", processRoleHumanCheckpoint, subtaskRunning},
	}
	if len(plan.Subtasks) != len(wantStages) {
		t.Fatalf("plan has %d subtasks, want %d (one per stage): %+v", len(plan.Subtasks), len(wantStages), plan.Subtasks)
	}
	for index, want := range wantStages {
		st := plan.Subtasks[index]
		if st.ID != want.id || st.Role != want.role {
			t.Fatalf("subtask %d = %s/%s, want %s/%s (definition stages instantiate in order)", index, st.ID, st.Role, want.id, want.role)
		}
		if st.Status != want.status {
			t.Fatalf("subtask %s status=%q, want %q", st.ID, st.Status, want.status)
		}
	}
	// Only the WRITER dispatched a child thread; inline stages never do.
	if len(*launched) != 1 || (*launched)[0].subtaskID != "draft" {
		t.Fatalf("launched children=%+v, want exactly the draft writer", *launched)
	}
	// The gate stage left its decision artifact on the record.
	if gateSt := plan.subtaskByID("note_gate"); gateSt.ArtifactID == "" || gateSt.Review == nil || gateSt.Review.Verdict != goalReviewPass {
		t.Fatalf("gate stage record missing: %+v", gateSt)
	}

	// The park: approval metadata + the checkpoint card payload.
	artifact := mustArtifact(t, app, thread.Artifact.ID)
	if artifact.Metadata["threadStatus"] != codexJobStatusApprovalRequired || artifact.Metadata["reviewGate"] != "approval_required" {
		t.Fatalf("checkpoint did not park on the approval surface: %v", artifact.Metadata)
	}
	if artifact.Metadata["processId"] != "process_probe" {
		t.Fatalf("processId metadata=%q, want process_probe", artifact.Metadata["processId"])
	}
	var checkpoint goalProcessCheckpoint
	if err := json.Unmarshal([]byte(artifact.Metadata["checkpoint"]), &checkpoint); err != nil {
		t.Fatalf("decode checkpoint metadata: %v (%q)", err, artifact.Metadata["checkpoint"])
	}
	if checkpoint.StageID != "ship_choice" || checkpoint.Question == "" {
		t.Fatalf("checkpoint metadata=%+v, want stageId ship_choice with a question", checkpoint)
	}
	if len(checkpoint.Options) != 2 || checkpoint.Options[0].Label != "ship" || checkpoint.Options[1].Label != "hold" {
		t.Fatalf("checkpoint options=%v, want [ship hold]", checkpoint.Options)
	}
	// The options carry their mechanical actions: ship proceeds (the default),
	// hold actually holds — the label finally tells the truth.
	if checkpoint.Options[0].action() != processCheckpointActionProceed || checkpoint.Options[1].action() != processCheckpointActionHold {
		t.Fatalf("checkpoint option actions=%v, want [proceed hold]", checkpoint.Options)
	}

	// A choice outside the options is refused; the goal stays parked.
	if err := app.resumeApprovedGoalWithChoice(thread.Artifact.ID, "aj@shareability.com", "yolo"); err == nil {
		t.Fatal("resume accepted a choice that is not one of the checkpoint options")
	}
	// The real choice resumes the goal through to verified.
	if err := app.resumeApprovedGoalWithChoice(thread.Artifact.ID, "aj@shareability.com", "ship"); err != nil {
		t.Fatalf("resumeApprovedGoalWithChoice: %v", err)
	}
	plan = waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)
	if plan.Checkpoint == nil || plan.Checkpoint.Choice != "ship" || plan.Checkpoint.ResolvedAt == "" {
		t.Fatalf("checkpoint not resolved with the choice: %+v", plan.Checkpoint)
	}
	pick := plan.subtaskByID("ship_choice")
	if pick == nil || pick.Status != subtaskComplete || pick.ArtifactID == "" {
		t.Fatalf("checkpoint subtask did not complete: %+v", pick)
	}
	decision := mustArtifact(t, app, pick.ArtifactID)
	if !strings.Contains(decision.Text, "ship") || !strings.Contains(decision.Text, "Checkpoint decision") {
		t.Fatalf("checkpoint decision artifact does not carry the choice: %q", decision.Text)
	}
}

// The chosen option must FEED the next stage's input: a writer stage that
// declares the checkpoint as InputFrom launches with the choice in its query.
func TestProcessCheckpointChoiceFeedsNextStageInput(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})
	launched := installFakeChildRunner(t)
	registerProcessDefinitionForTest(t, ProcessDefinition{
		ID:          "process_choice_probe",
		Version:     1,
		Title:       "Choice Probe",
		Description: "Test-only: writer, checkpoint, writer.",
		Authority:   toolAuthorityWorkspaceWrite,
		Hidden:      true,
		Stages: []ProcessStage{
			{ID: "w1", Title: "Draft two directions", Role: processRoleWriter},
			{ID: "pick", Title: "Pick a direction", Role: processRoleHumanCheckpoint, InputFrom: []string{"w1"},
				CheckpointSpec: &ProcessCheckpointSpec{Question: "Which direction?", Options: []ProcessCheckpointOption{{Label: "option-a"}, {Label: "option-b"}}}},
			{ID: "w2", Title: "Write the chosen direction", Role: processRoleWriter, InputFrom: []string{"pick"}},
		},
	})

	thread, err := app.launchGoalThread(goalLaunchSpec{
		Objective:    "Choose and write a direction",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "process_choice_probe",
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)
	waitForGoalStage(t, app, thread.Artifact.ID, goalStateApproval)

	if err := app.resumeApprovedGoalWithChoice(thread.Artifact.ID, "aj@shareability.com", "option-b"); err != nil {
		t.Fatalf("resumeApprovedGoalWithChoice: %v", err)
	}
	waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)

	found := false
	for _, child := range *launched {
		if child.subtaskID != "w2" {
			continue
		}
		found = true
		if !strings.Contains(child.query, "option-b") {
			t.Fatalf("w2 launch query does not carry the checkpoint choice: %q", child.query)
		}
		if !strings.Contains(child.query, "Input from prior stages") {
			t.Fatalf("w2 launch query does not carry the stage inputs block: %q", child.query)
		}
	}
	if !found {
		t.Fatalf("the post-checkpoint writer never launched: %+v", *launched)
	}
}

// Sidecar-absent render: the stage records a DISCLOSED skip and the process
// continues to verified — a render stage never blocks a sidecar-less deploy.
func TestProcessRenderStageSkipsDisclosedWhenSidecarAbsent(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})
	installFakeChildRunner(t)
	// The isolated app's temp data dir has no render-runner heartbeat, so
	// renderSidecarAvailable() is false — exactly the sidecar-absent deploy.
	registerProcessDefinitionForTest(t, ProcessDefinition{
		ID:          "process_render_probe",
		Version:     1,
		Title:       "Render Probe",
		Description: "Test-only: writer then render.",
		Authority:   toolAuthorityWorkspaceWrite,
		Hidden:      true,
		Stages: []ProcessStage{
			{ID: "w1", Title: "Draft the deck", Role: processRoleWriter},
			{ID: "export", Title: "Export the deck PDF", Role: processRoleRender, InputFrom: []string{"w1"}},
		},
	})

	thread, err := app.launchGoalThread(goalLaunchSpec{
		Objective:    "Draft and export the probe deck",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "process_render_probe",
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)

	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)
	export := plan.subtaskByID("export")
	if export == nil || export.Status != subtaskComplete {
		t.Fatalf("render stage must complete (skip, not block) when the sidecar is absent: %+v", export)
	}
	record := mustArtifact(t, app, export.ArtifactID)
	if !strings.Contains(record.Text, "Render export skipped") || !strings.Contains(record.Text, "render sidecar not available") {
		t.Fatalf("render skip is not disclosed: %q", record.Text)
	}
	if record.Metadata["renderSkipped"] != "true" {
		t.Fatalf("render skip metadata missing: %v", record.Metadata)
	}
}

// A failing gate re-queues its input stage with the gaps as revision notes,
// bounded by the stage's MaxRounds, then blocks the goal (no ForceAccept).
func TestProcessGateReviseRequeuesInputThenBlocks(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{
		processGate: `{"dimensions":[{"name":"Directness","score":4,"gap":"does not answer the objective"}],"reasons":"misses the point"}`,
	})
	launched := installFakeChildRunner(t)

	thread, err := app.launchGoalThread(goalLaunchSpec{
		Objective:    "Probe the failing gate",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "process_probe",
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)

	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateBlocked)
	gate := plan.subtaskByID("note_gate")
	if gate == nil || gate.Status != subtaskBlocked {
		t.Fatalf("gate did not block after its rounds: %+v", gate)
	}
	if gate.Revisions != 2 {
		t.Fatalf("gate revisions=%d, want the probe's MaxRounds 2", gate.Revisions)
	}
	// Initial draft launch + one re-queued launch per revise round.
	if len(*launched) != 3 {
		t.Fatalf("launched %d writer children, want 3 (initial + 2 gate-driven revisions): %+v", len(*launched), *launched)
	}
	// The re-queued writer carried the gate's gaps as revision notes.
	if !strings.Contains((*launched)[1].query, "does not answer the objective") {
		t.Fatalf("revision launch query missing the gate gaps: %q", (*launched)[1].query)
	}
	if !strings.Contains(plan.Blocker, "note_gate") {
		t.Fatalf("blocker does not name the gate: %q", plan.Blocker)
	}
	// The checkpoint never ran — nothing downstream of a blocked gate executes.
	if pick := plan.subtaskByID("ship_choice"); pick.Status == subtaskComplete {
		t.Fatalf("checkpoint ran past a blocked gate: %+v", pick)
	}
}

// --- Negative checkpoint options: the mechanical teeth (Wave 4's disclosed gap) --

// registerReviseProbeForTest registers a two-stage test process — writer w1,
// then a checkpoint whose "send back" option carries Action=revise targeting
// w1 — the smallest pipeline that exercises the send-back teeth.
func registerReviseProbeForTest(t *testing.T, id string) {
	t.Helper()
	registerProcessDefinitionForTest(t, ProcessDefinition{
		ID:          id,
		Version:     1,
		Title:       "Revise Probe",
		Description: "Test-only: writer, then a checkpoint with a revise door.",
		Authority:   toolAuthorityWorkspaceWrite,
		Hidden:      true,
		Stages: []ProcessStage{
			{ID: "w1", Title: "Draft the note", Role: processRoleWriter},
			{ID: "pass", Title: "Taste pass", Role: processRoleHumanCheckpoint, InputFrom: []string{"w1"},
				CheckpointSpec: &ProcessCheckpointSpec{
					Question: "Ship the draft, or send it back?",
					Options: []ProcessCheckpointOption{
						{Label: "ship it"},
						{Label: "send back", Action: processCheckpointActionRevise, Target: "w1"},
					},
				}},
		},
	})
}

// A revise-action choice re-queues the option's target stage with the choice
// text as revision notes and the do_not_touch lines locked as protected, then
// RE-PARKS the checkpoint after the redo — the send-back finally does what its
// label says, and the human gets the revised work back for another look.
func TestProcessCheckpointReviseRequeuesTargetAndReparks(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})
	launched := installFakeChildRunner(t)
	registerReviseProbeForTest(t, "process_revise_probe")

	thread, err := app.launchGoalThread(goalLaunchSpec{
		Objective:    "Probe the revise teeth",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "process_revise_probe",
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)
	waitForGoalStage(t, app, thread.Artifact.ID, goalStateApproval)

	sendBack := "send back — tighten the close.\ndo_not_touch: keep the opening line exactly as written"
	if err := app.resumeApprovedGoalWithChoice(thread.Artifact.ID, "aj@shareability.com", sendBack); err != nil {
		t.Fatalf("revise resume: %v", err)
	}

	// The redo ran and the goal re-parked at the SAME checkpoint, unresolved.
	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateApproval)
	if plan.Checkpoint == nil || plan.Checkpoint.StageID != "pass" || plan.Checkpoint.ResolvedAt != "" {
		t.Fatalf("checkpoint did not re-park after the redo: %+v", plan.Checkpoint)
	}
	pass := plan.subtaskByID("pass")
	if pass == nil || pass.Revisions != 1 || pass.Status != subtaskRunning {
		t.Fatalf("checkpoint subtask did not spend a round and re-park: %+v", pass)
	}
	// The writer re-launched exactly once, carrying the choice text as revision
	// notes and the do_not_touch line as the protected block.
	if len(*launched) != 2 || (*launched)[1].subtaskID != "w1" {
		t.Fatalf("launched children=%+v, want the initial w1 + one send-back redo", *launched)
	}
	// The thread query rides compacted (one line), so assert the pieces rather
	// than the raw multiline choice.
	redo := (*launched)[1].query
	if !strings.Contains(redo, "Revision notes from the goal review (address these): send back — tighten the close.") {
		t.Fatalf("redo query does not carry the send-back notes:\n%s", redo)
	}
	if !strings.Contains(redo, "DO NOT LOSE (protected)") || !strings.Contains(redo, "do_not_touch: keep the opening line exactly as written") {
		t.Fatalf("redo query does not lock the do_not_touch line as protected:\n%s", redo)
	}
	// The send-back budget lives on the CHECKPOINT stage; the target's own
	// Revisions counter (the failure-retry / gate-round budget) stays untouched
	// so a founder send-back never burns the transient-failure allowance.
	w1 := plan.subtaskByID("w1")
	if w1 == nil || w1.Revisions != 0 || !containsString(w1.Protect, "do_not_touch: keep the opening line exactly as written") {
		t.Fatalf("target subtask should carry the protect list without spending its own revision budget: %+v", w1)
	}

	// The subsequent proceed choice resolves the re-parked checkpoint to verified.
	if err := app.resumeApprovedGoalWithChoice(thread.Artifact.ID, "aj@shareability.com", "ship it"); err != nil {
		t.Fatalf("proceed after redo: %v", err)
	}
	plan = waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)
	if plan.Checkpoint == nil || plan.Checkpoint.Choice != "ship it" || plan.Checkpoint.ResolvedAt == "" {
		t.Fatalf("checkpoint not resolved by the proceed choice: %+v", plan.Checkpoint)
	}
}

// A revise choice on a spent budget (the same MaxRounds discipline as gates)
// falls back to proceed with the send-back DISCLOSED on the decision record —
// never an unbounded loop, never a silent swallow.
func TestProcessCheckpointReviseBudgetSpentFallsBackDisclosed(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})
	launched := installFakeChildRunner(t)
	registerReviseProbeForTest(t, "process_revise_budget_probe")

	thread, err := app.launchGoalThread(goalLaunchSpec{
		Objective:    "Probe the revise budget",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "process_revise_budget_probe",
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)

	// Two send-backs spend the goalMaxRevisions budget; each re-parks.
	for round := 1; round <= goalMaxRevisions; round++ {
		waitForGoalStage(t, app, thread.Artifact.ID, goalStateApproval)
		if err := app.resumeApprovedGoalWithChoice(thread.Artifact.ID, "aj@shareability.com", fmt.Sprintf("send back — round %d", round)); err != nil {
			t.Fatalf("send-back round %d: %v", round, err)
		}
	}
	// The third send-back has no budget left: it proceeds with the disclosure.
	waitForGoalStage(t, app, thread.Artifact.ID, goalStateApproval)
	if err := app.resumeApprovedGoalWithChoice(thread.Artifact.ID, "aj@shareability.com", "send back — one more polish"); err != nil {
		t.Fatalf("budget-spent send-back: %v", err)
	}
	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)
	if plan.Checkpoint == nil || plan.Checkpoint.ResolvedAt == "" || plan.Checkpoint.Choice != "send back — one more polish" {
		t.Fatalf("budget-spent revise did not resolve as proceed: %+v", plan.Checkpoint)
	}
	// Exactly the initial launch + goalMaxRevisions redos — the fallback never
	// re-queued a third time.
	if len(*launched) != 1+goalMaxRevisions {
		t.Fatalf("launched %d writer children, want %d (initial + %d send-back redos)", len(*launched), 1+goalMaxRevisions, goalMaxRevisions)
	}
	// The disclosure is on the checkpoint review AND the decision artifact.
	pass := plan.subtaskByID("pass")
	if pass == nil || pass.Review == nil || !strings.Contains(pass.Review.Reasons, "send-back budget is spent") {
		t.Fatalf("checkpoint review does not disclose the fallback: %+v", pass)
	}
	decision := mustArtifact(t, app, pass.ArtifactID)
	if !strings.Contains(decision.Text, "- Disclosed: ") || !strings.Contains(decision.Text, "proceeded with the request disclosed") {
		t.Fatalf("decision record does not disclose the fallback:\n%s", decision.Text)
	}
}

// A hold-action choice keeps the goal PARKED with the choice on record: the
// approval surface stays up, the held badge rides the checkpoint mirror, a
// resume attempt without an explicit proceed choice is refused, and only a
// proceed-action choice ships it.
func TestProcessCheckpointHoldKeepsGoalParkedUntilProceed(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})
	installFakeChildRunner(t)

	thread, err := app.launchGoalThread(goalLaunchSpec{
		Objective:    "Probe the hold teeth",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "process_probe",
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)
	waitForGoalStage(t, app, thread.Artifact.ID, goalStateApproval)

	// The probe's "hold" option now mechanically holds.
	if err := app.resumeApprovedGoalWithChoice(thread.Artifact.ID, "aj@shareability.com", "hold"); err != nil {
		t.Fatalf("hold resume: %v", err)
	}
	artifact := mustArtifact(t, app, thread.Artifact.ID)
	if artifact.Metadata["currentStage"] != goalStateApproval || artifact.Metadata["reviewGate"] != "approval_required" {
		t.Fatalf("hold un-parked the goal: %v", artifact.Metadata)
	}
	plan, ok := decodeGoalPlan(artifact.Metadata["goalPlan"])
	if !ok {
		t.Fatal("goal plan missing after the hold")
	}
	if plan.Checkpoint == nil || !plan.Checkpoint.Held || plan.Checkpoint.HeldBy != "aj@shareability.com" || plan.Checkpoint.HeldAt == "" {
		t.Fatalf("hold not recorded on the checkpoint: %+v", plan.Checkpoint)
	}
	if plan.Checkpoint.ResolvedAt != "" {
		t.Fatalf("a held checkpoint must stay UNRESOLVED (still parked): %+v", plan.Checkpoint)
	}
	// The held record rides the metadata mirror (the card's held badge) and the
	// goal artifact body discloses it.
	var mirrored goalProcessCheckpoint
	if err := json.Unmarshal([]byte(artifact.Metadata["checkpoint"]), &mirrored); err != nil || !mirrored.Held {
		t.Fatalf("checkpoint mirror does not carry the held record: %v (%q)", err, artifact.Metadata["checkpoint"])
	}
	if !strings.Contains(artifact.Text, "HELD by aj@shareability.com") {
		t.Fatalf("goal artifact does not record the hold:\n%s", artifact.Text)
	}

	// A plain approve (no choice) does NOT resume a held goal — and neither
	// does holding again; both leave it parked.
	if err := app.resumeApprovedGoalWithChoice(thread.Artifact.ID, "aj@shareability.com", ""); err == nil {
		t.Fatal("a held goal resumed from a choiceless approve")
	}
	if err := app.resumeApprovedGoalWithChoice(thread.Artifact.ID, "aj@shareability.com", "hold"); err == nil {
		t.Fatal("a held goal accepted a second hold as a resume")
	}
	if artifact = mustArtifact(t, app, thread.Artifact.ID); artifact.Metadata["currentStage"] != goalStateApproval {
		t.Fatalf("refused resumes moved the goal off the park: %q", artifact.Metadata["currentStage"])
	}

	// The subsequent proceed-action choice is the ONLY way forward.
	if err := app.resumeApprovedGoalWithChoice(thread.Artifact.ID, "aj@shareability.com", "ship"); err != nil {
		t.Fatalf("proceed after hold: %v", err)
	}
	plan = waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)
	if plan.Checkpoint == nil || plan.Checkpoint.Choice != "ship" || plan.Checkpoint.ResolvedAt == "" {
		t.Fatalf("proceed did not resolve the held checkpoint: %+v", plan.Checkpoint)
	}
}

// Panel/judges stages fan out through runGoalPanel inside ONE engine step: the
// personas answer via the fallback route, the synthesis lands as the stage
// artifact, and no child thread dispatches for the panel.
func TestProcessPanelStageRunsInlineThroughRunGoalPanel(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{
		fallback:  `{"take":"the persona speaks"}`,
		synthesis: "Synthesis: the rivals agree on direction B.",
	})
	launched := installFakeChildRunner(t)
	registerProcessDefinitionForTest(t, ProcessDefinition{
		ID:          "process_panel_probe",
		Version:     1,
		Title:       "Panel Probe",
		Description: "Test-only: writer then judge panel.",
		Authority:   toolAuthorityWorkspaceWrite,
		Hidden:      true,
		Stages: []ProcessStage{
			{ID: "w1", Title: "Draft the pitch", Role: processRoleWriter},
			{ID: "judge", Title: "Judge the pitch", Role: processRoleJudges, InputFrom: []string{"w1"},
				Personas: []ProcessPersona{
					{Name: "Skeptical LP", System: "You are a skeptical LP judging the pitch."},
					{Name: "Story Editor", System: "You are a story editor judging the pitch."},
				}},
		},
	})

	thread, err := app.launchGoalThread(goalLaunchSpec{
		Objective:    "Draft and judge the probe pitch",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "process_panel_probe",
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)

	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)
	judge := plan.subtaskByID("judge")
	if judge == nil || judge.Status != subtaskComplete {
		t.Fatalf("panel stage did not complete: %+v", judge)
	}
	record := mustArtifact(t, app, judge.ArtifactID)
	if !strings.Contains(record.Text, "Synthesis: the rivals agree on direction B.") {
		t.Fatalf("panel artifact missing the synthesis: %q", record.Text)
	}
	for _, persona := range []string{"Skeptical LP", "Story Editor"} {
		if !strings.Contains(record.Text, persona) {
			t.Fatalf("panel artifact missing voice %q: %q", persona, record.Text)
		}
	}
	// The panel ran inline: only the writer dispatched.
	if len(*launched) != 1 || (*launched)[0].subtaskID != "w1" {
		t.Fatalf("launched=%+v, want only the writer (the panel is one engine step)", *launched)
	}
}

// Budgets override the engine defaults: MaxSubtasks admits a plan the free-form
// cap would reject, and MaxTokens/WallClock retune the engine for the drive.
func TestProcessBudgetsOverrideEngineDefaults(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	stages := make([]ProcessStage, 0, goalMaxSubtasks+1)
	for i := 0; i < goalMaxSubtasks+1; i++ {
		stages = append(stages, ProcessStage{
			ID:    fmt.Sprintf("w%d", i+1),
			Title: fmt.Sprintf("Writer %d", i+1),
			Role:  processRoleWriter,
		})
	}
	def := ProcessDefinition{
		ID:          "process_budget_probe",
		Version:     1,
		Title:       "Budget Probe",
		Description: "Test-only: more stages than the free-form cap.",
		Authority:   toolAuthorityWorkspaceWrite,
		Hidden:      true,
		Budgets:     ProcessBudgets{MaxSubtasks: goalMaxSubtasks + 2, MaxTokens: 48000, WallClock: 25 * time.Minute},
		Stages:      stages,
	}
	registerProcessDefinitionForTest(t, def)

	plan := &goalPlan{PlanVersion: goalPlanVersion, ProcessID: def.ID, Authority: codexJobAuthorityWorkspaceWrite, State: goalStateDecompose}
	if err := instantiateProcessPlan(def, plan); err != nil {
		t.Fatalf("instantiateProcessPlan under the budget: %v", err)
	}
	if len(plan.Subtasks) != goalMaxSubtasks+1 {
		t.Fatalf("instantiated %d subtasks, want %d (the budget admits past the free-form cap)", len(plan.Subtasks), goalMaxSubtasks+1)
	}
	// The identical plan fails under the free-form ceiling — the override is
	// the budget, not a loosened validator.
	if err := validateGoalPlanWithLimit(plan, goalMaxSubtasks); err == nil {
		t.Fatal("the free-form cap should reject this plan; only the process budget admits it")
	}

	engine := newGoalEngine(app)
	baseTimeout, baseTokens := engine.timeout, engine.maxTokens
	engine.applyProcessBudgets(plan)
	if engine.maxTokens != 48000 {
		t.Fatalf("maxTokens=%d, want the budget's 48000 (was %d)", engine.maxTokens, baseTokens)
	}
	if engine.timeout != 25*time.Minute {
		t.Fatalf("timeout=%v, want the budget's 25m (was %v)", engine.timeout, baseTimeout)
	}

	// A plan with no process keeps the defaults untouched.
	fresh := newGoalEngine(app)
	fresh.applyProcessBudgets(&goalPlan{})
	if fresh.timeout != baseTimeout || fresh.maxTokens != baseTokens {
		t.Fatalf("non-process plan changed the engine envelope: timeout=%v tokens=%d", fresh.timeout, fresh.maxTokens)
	}
}

// The blocked-goal recovery door: the live drive-through proved the
// follow-up-based "Retry from here" never reaches a blocked process stage
// (a writer that exhausted revisions during the API-credit outage). resume
// resets exhausted subtasks and re-drives from exactly where it stopped.
func TestResumeBlockedGoalResetsAndRedrives(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	installFakeResponder(t, goalResponderRoutes{})
	launched := installFakeChildRunner(t)
	registerReviseProbeForTest(t, "process_resume_probe")

	thread, err := kanbanApp.launchGoalThread(goalLaunchSpec{
		Objective:    "Probe the blocked-resume door",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "process_resume_probe",
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	kanbanApp.runGoalThread(thread.Artifact.ID)
	// Let the async child folds finish (the probe parks at its checkpoint)
	// so no goroutine re-persists over the hand-set blocked state below.
	waitForGoalStage(t, kanbanApp, thread.Artifact.ID, goalStateApproval)

	// Force the writer subtask into the blocked terminal by hand — the state
	// the credit outage produced live.
	parent, _ := kanbanApp.osArtifactByID(thread.Artifact.ID)
	plan, _ := decodeGoalPlan(parent.Metadata["goalPlan"])
	for index := range plan.Subtasks {
		if plan.Subtasks[index].ID == "w1" {
			plan.Subtasks[index].Status = subtaskBlocked
			plan.Subtasks[index].Revisions = goalMaxRevisions
		}
	}
	plan.State = goalStateBlocked
	engine := newGoalEngine(kanbanApp)
	engine.persist(&plan, thread.Artifact.ID, "")

	before := len(*launched)
	if err := kanbanApp.resumeBlockedGoal(thread.Artifact.ID, "aj@shareability.com"); err != nil {
		t.Fatalf("resumeBlockedGoal: %v", err)
	}
	updated, _ := kanbanApp.osArtifactByID(thread.Artifact.ID)
	resumedPlan, _ := decodeGoalPlan(updated.Metadata["goalPlan"])
	if resumedPlan.State == goalStateBlocked {
		t.Fatalf("plan still blocked after resume: %s", resumedPlan.State)
	}
	if len(*launched) <= before {
		t.Fatalf("resume did not re-dispatch the writer (launched %d -> %d)", before, len(*launched))
	}

	// Refusal outside the blocked state: a healthy goal cannot be "resumed".
	if err := kanbanApp.resumeBlockedGoal(thread.Artifact.ID, "aj@shareability.com"); err == nil {
		t.Fatal("resume of a non-blocked goal must refuse")
	}
}

// Wave 6: feedback on a deliverable of a COMPLETED goal re-opens exactly the
// producing stage — target ready with the note as revision reasons and the
// do_not_touch lines protected, dependents (including the checkpoint) cascade-
// reset, the 100% progress pin released — then the redo re-parks at the
// checkpoint for a fresh human sign-off. Neither existing resume door accepts
// a verified goal; this is the new seam the deliverables drawer routes to.
func TestResumeGoalWithFeedbackReopensVerifiedGoal(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	installFakeResponder(t, goalResponderRoutes{})
	launched := installFakeChildRunner(t)
	registerReviseProbeForTest(t, "process_reopen_probe")

	var pendingDrive func()
	previousResume := startGoalFeedbackResumeAsync
	startGoalFeedbackResumeAsync = func(run func()) { pendingDrive = run }
	t.Cleanup(func() { startGoalFeedbackResumeAsync = previousResume })

	thread, err := kanbanApp.launchGoalThread(goalLaunchSpec{
		Objective:    "Probe the completed-goal re-open door",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "process_reopen_probe",
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	parentID := thread.Artifact.ID
	kanbanApp.runGoalThread(parentID)
	waitForGoalStage(t, kanbanApp, parentID, goalStateApproval)
	if err := kanbanApp.resumeApprovedGoalWithChoice(parentID, "aj@shareability.com", "ship it"); err != nil {
		t.Fatalf("ship the probe: %v", err)
	}
	plan := waitForGoalStage(t, kanbanApp, parentID, goalStateVerified)
	w1 := plan.subtaskByID("w1")
	if w1 == nil || w1.ArtifactID == "" {
		t.Fatalf("w1=%+v, want a completed writer with its artifact", w1)
	}
	deliverableID := w1.ArtifactID

	// A running goal refuses feedback — hand-set execute to prove the guard,
	// then restore verified.
	verified, _ := kanbanApp.osArtifactByID(parentID)
	verifiedPlan, _ := decodeGoalPlan(verified.Metadata["goalPlan"])
	engine := newGoalEngine(kanbanApp)
	runningPlan := verifiedPlan
	runningPlan.State = goalStateExecute
	engine.persist(&runningPlan, parentID, "")
	if _, err := kanbanApp.resumeGoalWithFeedback(parentID, "tim@shareability.com", "tweak it", deliverableID); err == nil {
		t.Fatal("feedback on a running goal must refuse")
	}
	engine.persist(&verifiedPlan, parentID, "")

	note := "tighten the close\ndo_not_touch: keep the headline exactly as written"
	resumed, err := kanbanApp.resumeGoalWithFeedback(parentID, "tim@shareability.com", note, deliverableID)
	if err != nil {
		t.Fatalf("resumeGoalWithFeedback: %v", err)
	}
	if resumed.Mode != "goal" || resumed.Artifact.ID != parentID || resumed.Status != "running" {
		t.Fatalf("resumed thread=%+v, want a running goal-mode handle on the parent", resumed)
	}

	// Persisted BEFORE the drive: the re-open is durable (crash-safe), the
	// note rides the target's review, the pin is released.
	reopened, _ := kanbanApp.osArtifactByID(parentID)
	reopenedPlan, ok := decodeGoalPlan(reopened.Metadata["goalPlan"])
	if !ok || reopenedPlan.State != goalStateExecute {
		t.Fatalf("state=%q, want execute_in_order persisted before the drive", reopenedPlan.State)
	}
	target := reopenedPlan.subtaskByID("w1")
	if target == nil || target.Status != subtaskReady || target.Review == nil || !strings.Contains(target.Review.Reasons, "tighten the close") {
		t.Fatalf("target=%+v, want w1 ready with the feedback note as revision reasons", target)
	}
	if !containsString(target.Protect, "do_not_touch: keep the headline exactly as written") {
		t.Fatalf("target.Protect=%v, want the do_not_touch line locked", target.Protect)
	}
	if pass := reopenedPlan.subtaskByID("pass"); pass == nil || pass.Status == subtaskComplete {
		t.Fatalf("checkpoint stage=%+v, want cascade-reset so it re-parks", reopenedPlan.subtaskByID("pass"))
	}
	if reopened.Metadata["goalStatus"] == "verified" || reopened.Metadata["progressPercent"] == "100" {
		t.Fatalf("card metadata=%q/%q, want the terminal read released", reopened.Metadata["goalStatus"], reopened.Metadata["progressPercent"])
	}

	// The drive re-runs the writer with the note and re-parks at the checkpoint.
	if pendingDrive == nil {
		t.Fatal("re-open never scheduled its drive")
	}
	before := len(*launched)
	pendingDrive()
	plan = waitForGoalStage(t, kanbanApp, parentID, goalStateApproval)
	if plan.Checkpoint == nil || plan.Checkpoint.ResolvedAt != "" {
		t.Fatalf("checkpoint=%+v, want a fresh unresolved park after the redo", plan.Checkpoint)
	}
	if len(*launched) <= before {
		t.Fatalf("re-open did not re-dispatch the writer (launched %d -> %d)", before, len(*launched))
	}
	redo := (*launched)[len(*launched)-1]
	if redo.subtaskID != "w1" || !strings.Contains(redo.query, "tighten the close") || !strings.Contains(redo.query, "do_not_touch: keep the headline exactly as written") {
		t.Fatalf("redo child=%+v, want w1 carrying the note and the protected line", redo)
	}

	// The loop closes: a fresh sign-off ships the revision back to verified.
	if err := kanbanApp.resumeApprovedGoalWithChoice(parentID, "aj@shareability.com", "ship it"); err != nil {
		t.Fatalf("re-approve after the revision: %v", err)
	}
	waitForGoalStage(t, kanbanApp, parentID, goalStateVerified)

	// Cancelled goals stay dead: feedback must not resurrect them.
	final, _ := kanbanApp.osArtifactByID(parentID)
	finalPlan, _ := decodeGoalPlan(final.Metadata["goalPlan"])
	finalPlan.Cancelled = true
	engine.persist(&finalPlan, parentID, "")
	if _, err := kanbanApp.resumeGoalWithFeedback(parentID, "tim@shareability.com", "one more pass", deliverableID); err == nil {
		t.Fatal("feedback on a cancelled goal must refuse")
	}
}

// Wave 6 deep 1:1 linkage: feedback on a packaging ship deliverable re-runs
// the stage whose output that deliverable compiles FROM — The Wall is write's
// copy, The Talk is voice's script, the rigor companion is the red team's
// ledger — not blindly the checkpoint's declared ship_deck target. The
// findings record maps to no single stage and falls through to the checkpoint
// door.
func TestFeedbackTargetSubtaskMapsShipContracts(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	engine := newGoalEngine(app)

	plan := goalPlan{Subtasks: []goalSubtask{
		{ID: "red_team"}, {ID: "write"}, {ID: "voice"}, {ID: "ship_deck"},
		{ID: "pass", Role: processRoleHumanCheckpoint},
	}}
	plan.Checkpoint = &goalProcessCheckpoint{StageID: "pass", Options: []goalCheckpointOption{
		{Label: "approve the ship"},
		{Label: "send back — rebuild the deck", Action: processCheckpointActionRevise, Target: "ship_deck"},
	}}

	for contract, wantStage := range map[string]string{
		"packaging_wall_v1":  "write",
		"packaging_talk_v1":  "voice",
		"packaging_rigor_v1": "red_team",
		"packaging_deck_v1":  "ship_deck",
		// Findings aggregate every verdict — no single producing stage; the
		// checkpoint's declared send-back target catches it.
		"packaging_findings_v1": "ship_deck",
	} {
		artifact, _, err := app.createOSArtifactWithMetadata("workflow", "deliverable "+contract, "body", "AJ", map[string]string{
			"source":           "packaging_studio_ship",
			"artifactContract": contract,
			"goalId":           "os-artifact-workflow-map-probe",
		})
		if err != nil {
			t.Fatalf("seed %s: %v", contract, err)
		}
		target := engine.feedbackTargetSubtask(&plan, artifact.ID)
		if target == nil || target.ID != wantStage {
			t.Fatalf("contract %s targeted %+v, want stage %q", contract, target, wantStage)
		}
	}
}

// A process WRITER stage's OutputContract rides onto the child it dispatches
// (metadata "outputContract"), so the worker's instruction layer can honor
// raw-document contracts — the ship_deck child must KNOW its response is the
// HTML file itself.
func TestLaunchSubtaskStampsOutputContractOnChild(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})
	var dispatched []scoutAgentThread
	previousAsync := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, thread scoutAgentThread) {
		dispatched = append(dispatched, thread)
	}
	t.Cleanup(func() { startAgentThreadAsync = previousAsync })
	registerProcessDefinitionForTest(t, ProcessDefinition{
		ID: "process_contract_stamp_probe", Version: 1, Title: "Contract stamp probe",
		Description: "Test-only: one writer with a raw contract.", Authority: toolAuthorityWorkspaceWrite, Hidden: true,
		Stages: []ProcessStage{
			{ID: "w1", Title: "Ship the deck", Role: processRoleWriter, OutputContract: "packaging_deck_v1"},
		},
	})

	thread, err := app.launchGoalThread(goalLaunchSpec{
		Objective:    "Probe the contract stamp",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "process_contract_stamp_probe",
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)
	if len(dispatched) == 0 {
		t.Fatal("writer child was never dispatched")
	}
	child := dispatched[0]
	if child.Artifact.Metadata["outputContract"] != "packaging_deck_v1" {
		t.Fatalf("child outputContract=%q, want the stage's contract", child.Artifact.Metadata["outputContract"])
	}
}

// --- W0 items 3 + 6: ledger seat tags, gate-by-runner + parse-failure events ---

// Every goal-engine model call carries its ledger seat to the wire seam:
// orchestration lanes (decompose/panel/report/verify) bill as goal_engine on
// the orchestrator model; review/gate scoring bills as goal_review on the
// review model.
func TestGoalEngineCallsCarrySeatTags(t *testing.T) {
	var seats []string
	var models []string
	engine := &goalEngine{
		apiKey:      func() string { return "test-key" },
		model:       "claude-fable-5",
		reviewModel: "claude-opus-4-8",
		effort:      "high",
		maxTokens:   2048,
		responder: func(_ context.Context, _ string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
			seats = append(seats, request.Seat)
			models = append(models, request.Model)
			return anthropicMessagesResponse{StopReason: "end_turn", Content: []json.RawMessage{mockAnthropicTextBlock("ok")}}, nil
		},
	}
	if _, err := engine.callModel(context.Background(), "sys", "user"); err != nil {
		t.Fatalf("callModel: %v", err)
	}
	if _, err := engine.callReviewModel(context.Background(), "sys", "user"); err != nil {
		t.Fatalf("callReviewModel: %v", err)
	}
	if len(seats) != 2 || seats[0] != seatGoalEngine || seats[1] != seatGoalReview {
		t.Fatalf("seats=%v, want [%s %s]", seats, seatGoalEngine, seatGoalReview)
	}
	if models[0] != "claude-fable-5" || models[1] != "claude-opus-4-8" {
		t.Fatalf("models=%v, want the orchestrator then the review model", models)
	}
}

// pinGoalEvalLedger points the ledger at a temp dir with a fixed clock and
// returns the eval file path the events land in.
func pinGoalEvalLedger(t *testing.T) string {
	t.Helper()
	dir := ledgerTestDir(t)
	originalNow := usageLedgerNow
	usageLedgerNow = func() time.Time { return time.Date(2026, time.July, 11, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { usageLedgerNow = originalNow })
	return filepath.Join(dir, "eval-2026-07-11.jsonl")
}

// goalEvalEventFields splits an eval ledger file's rows by kind, returning
// each row's fields map.
func goalEvalEventFields(t *testing.T, path string, kind string) []map[string]any {
	t.Helper()
	var matched []map[string]any
	for _, row := range readLedgerLines(t, path) {
		if row["kind"] != kind {
			continue
		}
		fields, _ := row["fields"].(map[string]any)
		matched = append(matched, fields)
	}
	return matched
}

// A reviewer reply that fails strict-JSON decoding increments the goal_review
// parse-failure series (the designated flip-regression metric) AND the review
// verdict still lands as a runner-tagged gate_result — the per-runner
// gate-failure series the model_choice adoption gate reads.
func TestGoalEngineReviewEmitsGateResultAndParseFailureEvents(t *testing.T) {
	evalPath := pinGoalEvalLedger(t)
	app := newIsolatedKanbanBoardApp(t)
	engine := &goalEngine{
		app:         app,
		apiKey:      func() string { return "test-key" },
		model:       "claude-fable-5",
		reviewModel: "claude-opus-4-8",
		effort:      "high",
		maxTokens:   2048,
		responder: func(_ context.Context, _ string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
			// Brace-carrying invalid JSON: extractJSONObject finds an object,
			// Unmarshal fails — the strict-JSON parse-failure path.
			return anthropicMessagesResponse{StopReason: "end_turn", Content: []json.RawMessage{mockAnthropicTextBlock(`{"verdict": broken}`)}}, nil
		},
	}
	plan := &goalPlan{
		GoalID:    "goal-1",
		Objective: "test objective",
		Subtasks: []goalSubtask{{
			ID:     "st-1",
			Title:  "produce the thing",
			Mode:   "research",
			Status: subtaskComplete,
			Runner: agentRunnerAnthropicFable,
		}},
	}

	if outcome := engine.reviewSubtasks(context.Background(), plan); outcome != goalReviewOutcomeRequeue {
		t.Fatalf("outcome=%v, want requeue (malformed reviewer JSON folds to revise)", outcome)
	}

	parseFailures := goalEvalEventFields(t, evalPath, evalKindParseFailure)
	if len(parseFailures) != 1 || parseFailures[0]["seat"] != seatGoalReview || parseFailures[0]["model"] != "claude-opus-4-8" {
		t.Fatalf("parse_failure events=%v, want one on goal_review/claude-opus-4-8", parseFailures)
	}
	gateResults := goalEvalEventFields(t, evalPath, evalKindGateResult)
	if len(gateResults) != 1 {
		t.Fatalf("gate_result events=%v, want exactly one", gateResults)
	}
	if gateResults[0]["runner"] != agentRunnerAnthropicFable || gateResults[0]["verdict"] != goalReviewRevise || gateResults[0]["goal_id"] != "goal-1" {
		t.Fatalf("gate_result fields=%v, want runner/verdict/goal_id provenance", gateResults[0])
	}
}

// The plan-level ship gate settles into a runner-tagged gate_result on every
// branch: a clean pass records "passed" against the runner whose work was
// judged (the last sink subtask on a free-form goal).
func TestGoalEngineShipGateEmitsGateResultEvent(t *testing.T) {
	evalPath := pinGoalEvalLedger(t)
	app := newIsolatedKanbanBoardApp(t)
	engine := &goalEngine{
		app:         app,
		apiKey:      func() string { return "test-key" },
		model:       "claude-fable-5",
		reviewModel: "claude-opus-4-8",
		effort:      "high",
		maxTokens:   2048,
		responder: func(_ context.Context, _ string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
			return anthropicMessagesResponse{StopReason: "end_turn", Content: []json.RawMessage{mockAnthropicTextBlock(`{"safe":true,"external_write_required":false,"command":"","reason":"clean"}`)}}, nil
		},
	}
	plan := &goalPlan{
		GoalID:    "goal-2",
		Objective: "ship it",
		Subtasks: []goalSubtask{{
			ID:     "st-1",
			Title:  "write it",
			Mode:   "research",
			Status: subtaskComplete,
			Runner: agentRunnerCodexSidecar,
		}},
	}

	engine.gate(context.Background(), plan)
	if plan.Gate.Status != "passed" {
		t.Fatalf("gate status=%q, want passed", plan.Gate.Status)
	}

	gateResults := goalEvalEventFields(t, evalPath, evalKindGateResult)
	if len(gateResults) != 1 {
		t.Fatalf("gate_result events=%v, want exactly one", gateResults)
	}
	if gateResults[0]["runner"] != agentRunnerCodexSidecar || gateResults[0]["verdict"] != "passed" || gateResults[0]["goal_id"] != "goal-2" {
		t.Fatalf("gate_result fields=%v, want codex_sidecar/passed/goal-2", gateResults[0])
	}
}

// A ship-gate reply that fails strict-JSON decoding blocks the gate AND
// counts on the goal_review parse-failure series, with the blocked verdict
// still landing as a gate_result.
func TestGoalEngineShipGateMalformedJSONEmitsParseFailure(t *testing.T) {
	evalPath := pinGoalEvalLedger(t)
	app := newIsolatedKanbanBoardApp(t)
	engine := &goalEngine{
		app:         app,
		apiKey:      func() string { return "test-key" },
		model:       "claude-fable-5",
		reviewModel: "claude-opus-4-8",
		effort:      "high",
		maxTokens:   2048,
		responder: func(_ context.Context, _ string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
			return anthropicMessagesResponse{StopReason: "end_turn", Content: []json.RawMessage{mockAnthropicTextBlock(`{"safe": nope}`)}}, nil
		},
	}
	plan := &goalPlan{
		GoalID:    "goal-3",
		Objective: "ship it",
		Subtasks:  []goalSubtask{{ID: "st-1", Title: "write it", Mode: "research", Status: subtaskComplete, Runner: agentRunnerAnthropicFable}},
	}

	engine.gate(context.Background(), plan)
	if plan.Gate.Status != subtaskBlocked {
		t.Fatalf("gate status=%q, want blocked on malformed JSON", plan.Gate.Status)
	}

	parseFailures := goalEvalEventFields(t, evalPath, evalKindParseFailure)
	if len(parseFailures) != 1 || parseFailures[0]["seat"] != seatGoalReview {
		t.Fatalf("parse_failure events=%v, want one on goal_review", parseFailures)
	}
	gateResults := goalEvalEventFields(t, evalPath, evalKindGateResult)
	if len(gateResults) != 1 || gateResults[0]["verdict"] != subtaskBlocked || gateResults[0]["runner"] != agentRunnerAnthropicFable {
		t.Fatalf("gate_result events=%v, want one blocked verdict tagged anthropic_fable", gateResults)
	}
}
