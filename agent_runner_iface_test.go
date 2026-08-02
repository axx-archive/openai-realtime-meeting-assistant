package main

import (
	"context"
	"strings"
	"testing"
)

// clearAgentRunnerEnv resets every env var the selection functions read so a
// case sees only the overrides it sets, not whatever the host environment
// carries.
func clearAgentRunnerEnv(t *testing.T) {
	t.Helper()
	codexExecutionTestGate.Store(false)
	for _, key := range []string{
		"ANTHROPIC_API_KEY",
		"BONFIRE_AGENT_RUNNER",
		"BONFIRE_EXECUTION_RUNNER",
		"BONFIRE_AGENT_THREAD_WORKER",
		"BONFIRE_CODEX_AGENT_THREADS",
		"BONFIRE_CODEX_RUNNER_MODE",
		"BONFIRE_CODEX_EXECUTION_ENABLED",
	} {
		t.Setenv(key, "")
	}
}

func enableCodexExecutionForTest(t *testing.T) {
	t.Helper()
	previous := codexExecutionTestGate.Load()
	codexExecutionTestGate.Store(true)
	t.Cleanup(func() { codexExecutionTestGate.Store(previous) })
}

func TestSelectedAgentRunnerNameMatrix(t *testing.T) {
	cases := []struct {
		name        string
		env         map[string]string
		enableCodex bool
		want        string
	}{
		{"default keyless falls back to openai_text", nil, false, agentRunnerOpenAIText},
		{"default with anthropic key is orchestrator", map[string]string{"ANTHROPIC_API_KEY": "sk-test"}, false, agentRunnerAnthropicFable},
		{"explicit anthropic keyless degrades to openai_text", map[string]string{"BONFIRE_AGENT_RUNNER": "anthropic_fable"}, false, agentRunnerOpenAIText},
		{"explicit fable alias with key", map[string]string{"BONFIRE_AGENT_RUNNER": "fable", "ANTHROPIC_API_KEY": "sk-test"}, false, agentRunnerAnthropicFable},
		{"explicit openai overrides key", map[string]string{"BONFIRE_AGENT_RUNNER": "openai_text", "ANTHROPIC_API_KEY": "sk-test"}, false, agentRunnerOpenAIText},
		{"explicit codex is closed", map[string]string{"BONFIRE_AGENT_RUNNER": "codex", "ANTHROPIC_API_KEY": "sk-test"}, false, agentRunnerStub},
		{"invented environment gate cannot enable Codex", map[string]string{"BONFIRE_AGENT_RUNNER": "codex", "BONFIRE_CODEX_EXECUTION_ENABLED": "true"}, false, agentRunnerStub},
		{"explicit codex legacy unit gate", map[string]string{"BONFIRE_AGENT_RUNNER": "codex"}, true, agentRunnerCodexSidecar},
		{"explicit stub", map[string]string{"BONFIRE_AGENT_RUNNER": "stub"}, false, agentRunnerStub},
		{"back-compat worker is closed", map[string]string{"BONFIRE_AGENT_THREAD_WORKER": "codex_exec"}, false, agentRunnerStub},
		{"back-compat local mode is closed", map[string]string{"BONFIRE_AGENT_THREAD_WORKER": "codex_exec", "BONFIRE_CODEX_RUNNER_MODE": "local"}, false, agentRunnerStub},
		{"back-compat boolean is closed", map[string]string{"BONFIRE_CODEX_AGENT_THREADS": "1"}, false, agentRunnerStub},
		{"unknown value falls back to legacy worker", map[string]string{"BONFIRE_AGENT_RUNNER": "wat"}, false, agentRunnerOpenAIText},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAgentRunnerEnv(t)
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			if tc.enableCodex {
				enableCodexExecutionForTest(t)
			}
			if got := selectedAgentRunnerName(); got != tc.want {
				t.Fatalf("selectedAgentRunnerName()=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestSelectedExecutionRunnerNameMatrix(t *testing.T) {
	cases := []struct {
		name        string
		env         string
		enableCodex bool
		want        string
	}{
		{"default is closed", "", false, agentRunnerStub},
		{"explicit codex local is closed", "codex_local", false, agentRunnerStub},
		{"explicit fable", "fable", false, agentRunnerAnthropicFable},
		{"none maps to stub", "none", false, agentRunnerStub},
		{"unknown is closed", "wat", false, agentRunnerStub},
		{"legacy unit gate reaches sidecar", "", true, agentRunnerCodexSidecar},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAgentRunnerEnv(t)
			t.Setenv("BONFIRE_EXECUTION_RUNNER", tc.env)
			if tc.enableCodex {
				enableCodexExecutionForTest(t)
			}
			if got := selectedExecutionRunnerName(); got != tc.want {
				t.Fatalf("selectedExecutionRunnerName()=%q, want %q", got, tc.want)
			}
		})
	}
}

// A /goal deliverable subtask (goalDeliverable metadata flag) carries a heavier
// per-job budget so its contract-bearing artifact does not truncate under the
// planning default; every other job carries no override.
func TestNewAgentJobDeliverableBudget(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("BONFIRE_DELIVERABLE_MAX_TOKENS", "")
	t.Setenv("BONFIRE_DELIVERABLE_EFFORT", "")

	plain := app.newAgentJob(scoutAgentThread{ID: "t1", Mode: "workflow", Query: "x",
		Artifact: meetingMemoryEntry{ID: "a1", Metadata: map[string]string{}}})
	if plain.MaxTokens != 0 || plain.Effort != "" {
		t.Fatalf("plain job carried a budget override: effort=%q max=%d", plain.Effort, plain.MaxTokens)
	}

	deliverable := app.newAgentJob(scoutAgentThread{ID: "t2", Mode: "design", Query: "write the one-pager",
		Artifact: meetingMemoryEntry{ID: "a2", Metadata: map[string]string{"goalDeliverable": "true"}}})
	if deliverable.MaxTokens != 32768 {
		t.Fatalf("deliverable maxTokens=%d, want default 32768", deliverable.MaxTokens)
	}
	if deliverable.Effort != "high" {
		t.Fatalf("deliverable effort=%q, want default high", deliverable.Effort)
	}

	t.Setenv("BONFIRE_DELIVERABLE_MAX_TOKENS", "12000")
	t.Setenv("BONFIRE_DELIVERABLE_EFFORT", "medium")
	overridden := app.newAgentJob(scoutAgentThread{ID: "t3", Mode: "design", Query: "y",
		Artifact: meetingMemoryEntry{ID: "a3", Metadata: map[string]string{"goalDeliverable": "true"}}})
	if overridden.MaxTokens != 12000 || overridden.Effort != "medium" {
		t.Fatalf("env override not honored: max=%d effort=%q", overridden.MaxTokens, overridden.Effort)
	}
}

// scriptedRunner is a fake AgentRunner that replays a scripted progress channel
// with no network — used to prove the progress→artifact-metadata fold.
type scriptedRunner struct {
	updates []AgentProgress
}

func (r *scriptedRunner) Name() string                    { return "scripted" }
func (r *scriptedRunner) Capabilities() AgentCapabilities { return AgentCapabilities{} }

func (r *scriptedRunner) RunJob(_ context.Context, _ AgentJob) (<-chan AgentProgress, error) {
	out := make(chan AgentProgress, len(r.updates))
	for _, update := range r.updates {
		out <- update
	}
	close(out)
	return out, nil
}

func TestDrainAgentProgressFoldsMetadata(t *testing.T) {
	runner := &scriptedRunner{updates: []AgentProgress{
		{Stage: "execute_in_order", ProgressPercent: 35, GoalStatus: "running", ReviewGate: "pending", Note: "starting"},
		{Stage: "review_against_original_goal", ProgressPercent: 80, GoalStatus: "review", ReviewGate: "pending"},
		{
			Terminal: true, Stage: "verify_goal_completed", ProgressPercent: 100,
			GoalStatus: "verified", ReviewGate: "passed", Text: "final artifact body",
			Metadata: map[string]string{"orchestratorModel": "claude-fable-5"},
		},
	}}

	seen := 0
	out, err := runner.RunJob(context.Background(), AgentJob{})
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	result, runErr := drainAgentProgress(out, func(AgentProgress) { seen++ })
	if runErr != nil {
		t.Fatalf("drainAgentProgress runErr: %v", runErr)
	}
	if seen != 3 {
		t.Fatalf("onProgress called %d times, want 3", seen)
	}
	if !result.Terminal {
		t.Fatal("result.Terminal=false, want true")
	}
	if result.Text != "final artifact body" {
		t.Fatalf("result.Text=%q, want final artifact body", result.Text)
	}
	// The last update wins for each mapped field.
	for key, want := range map[string]string{
		"progressPercent":   "100",
		"currentStage":      "verify_goal_completed",
		"goalStatus":        "verified",
		"reviewGate":        "passed",
		"orchestratorModel": "claude-fable-5",
	} {
		if result.Metadata[key] != want {
			t.Fatalf("metadata[%q]=%q, want %q", key, result.Metadata[key], want)
		}
	}
}

func TestDrainAgentProgressSurfacesTerminalError(t *testing.T) {
	runner := &scriptedRunner{updates: []AgentProgress{
		{Stage: "execute_in_order", ProgressPercent: 35, GoalStatus: "running"},
		{Terminal: true, GoalStatus: "needs_attention", ReviewGate: "blocked", Err: errAgentWorkerNotConfigured},
	}}
	out, _ := runner.RunJob(context.Background(), AgentJob{})
	result, runErr := drainAgentProgress(out, nil)
	if runErr != errAgentWorkerNotConfigured {
		t.Fatalf("runErr=%v, want errAgentWorkerNotConfigured", runErr)
	}
	if result.Metadata["goalStatus"] != "needs_attention" || result.Metadata["reviewGate"] != "blocked" {
		t.Fatalf("metadata=%v, want needs_attention/blocked", result.Metadata)
	}
}

// reviewModel resolves the dedicated reviewer/ship-gate model: Opus by default
// (reviews read whole artifacts; that wants Opus-tier context, not the Fable
// ceiling), env-overridable like every other model dial.
func TestReviewModelDefaultAndOverride(t *testing.T) {
	t.Setenv("BONFIRE_REVIEW_MODEL", "")
	if got := reviewModel(); got != defaultReviewModel {
		t.Fatalf("reviewModel()=%q, want the default %q", got, defaultReviewModel)
	}
	if defaultReviewModel != "claude-opus-4-8" {
		t.Fatalf("defaultReviewModel=%q, want claude-opus-4-8 (Wave 3 item 16)", defaultReviewModel)
	}
	t.Setenv("BONFIRE_REVIEW_MODEL", "claude-sonnet-5")
	if got := reviewModel(); got != "claude-sonnet-5" {
		t.Fatalf("reviewModel()=%q, want the env override", got)
	}
	// The judging seat is a worker seat under the routing doctrine: a haiku id
	// on the dial is refused and the Opus default stands (never Haiku).
	t.Setenv("BONFIRE_REVIEW_MODEL", "claude-haiku-4-5")
	if got := reviewModel(); got != defaultReviewModel {
		t.Fatalf("reviewModel() with haiku=%q, want the %s doctrine default", got, defaultReviewModel)
	}
}

// The per-turn note rides the artifact as metadata["progressNote"] (the client
// renders it under the bar), capped for storage because notes derive from model
// output and tool names, not a bounded vocabulary. An empty note adds no key —
// the metadata write stays additive.
func TestAgentProgressMetadataWritesCappedProgressNote(t *testing.T) {
	long := strings.Repeat("evidence sweep ", 20)
	metadata := agentProgressMetadata(AgentProgress{ProgressPercent: 48, Note: long})
	if metadata["progressPercent"] != "48" {
		t.Fatalf("progressPercent=%q, want 48", metadata["progressPercent"])
	}
	note := metadata["progressNote"]
	if note == "" {
		t.Fatal("progressNote missing")
	}
	if want := trimForStorage(compactAssistantLine(long), 140); note != want {
		t.Fatalf("progressNote=%q, want the storage cap %q", note, want)
	}
	// trimForStorage keeps limit-1 runes plus its "..." marker.
	if got := len([]rune(note)); got > 142 {
		t.Fatalf("progressNote is %d runes, want <= 142", got)
	}
	if _, ok := agentProgressMetadata(AgentProgress{ProgressPercent: 10})["progressNote"]; ok {
		t.Fatal("empty note must not add a progressNote key")
	}
}
