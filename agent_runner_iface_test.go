package main

import (
	"context"
	"testing"
)

// clearAgentRunnerEnv resets every env var the selection functions read so a
// case sees only the overrides it sets, not whatever the host environment
// carries.
func clearAgentRunnerEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"ANTHROPIC_API_KEY",
		"BONFIRE_AGENT_RUNNER",
		"BONFIRE_EXECUTION_RUNNER",
		"BONFIRE_AGENT_THREAD_WORKER",
		"BONFIRE_CODEX_AGENT_THREADS",
		"BONFIRE_CODEX_RUNNER_MODE",
	} {
		t.Setenv(key, "")
	}
}

func TestSelectedAgentRunnerNameMatrix(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"default keyless falls back to openai_text", nil, agentRunnerOpenAIText},
		{"default with anthropic key is orchestrator", map[string]string{"ANTHROPIC_API_KEY": "sk-test"}, agentRunnerAnthropicFable},
		{"explicit anthropic keyless degrades to openai_text", map[string]string{"BONFIRE_AGENT_RUNNER": "anthropic_fable"}, agentRunnerOpenAIText},
		{"explicit fable alias with key", map[string]string{"BONFIRE_AGENT_RUNNER": "fable", "ANTHROPIC_API_KEY": "sk-test"}, agentRunnerAnthropicFable},
		{"explicit openai overrides key", map[string]string{"BONFIRE_AGENT_RUNNER": "openai_text", "ANTHROPIC_API_KEY": "sk-test"}, agentRunnerOpenAIText},
		{"explicit codex overrides key", map[string]string{"BONFIRE_AGENT_RUNNER": "codex", "ANTHROPIC_API_KEY": "sk-test"}, agentRunnerCodexSidecar},
		{"explicit stub", map[string]string{"BONFIRE_AGENT_RUNNER": "stub"}, agentRunnerStub},
		{"back-compat worker=codex_exec", map[string]string{"BONFIRE_AGENT_THREAD_WORKER": "codex_exec"}, agentRunnerCodexSidecar},
		{"back-compat worker=codex_exec local mode", map[string]string{"BONFIRE_AGENT_THREAD_WORKER": "codex_exec", "BONFIRE_CODEX_RUNNER_MODE": "local"}, agentRunnerCodexLocal},
		{"back-compat BONFIRE_CODEX_AGENT_THREADS", map[string]string{"BONFIRE_CODEX_AGENT_THREADS": "1"}, agentRunnerCodexSidecar},
		{"unknown value falls back to legacy worker", map[string]string{"BONFIRE_AGENT_RUNNER": "wat"}, agentRunnerOpenAIText},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAgentRunnerEnv(t)
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			if got := selectedAgentRunnerName(); got != tc.want {
				t.Fatalf("selectedAgentRunnerName()=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestSelectedExecutionRunnerNameMatrix(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want string
	}{
		{"default is codex_sidecar", "", agentRunnerCodexSidecar},
		{"explicit codex_local", "codex_local", agentRunnerCodexLocal},
		{"explicit fable", "fable", agentRunnerAnthropicFable},
		{"none maps to stub", "none", agentRunnerStub},
		{"unknown falls back to codex_sidecar", "wat", agentRunnerCodexSidecar},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAgentRunnerEnv(t)
			t.Setenv("BONFIRE_EXECUTION_RUNNER", tc.env)
			if got := selectedExecutionRunnerName(); got != tc.want {
				t.Fatalf("selectedExecutionRunnerName()=%q, want %q", got, tc.want)
			}
		})
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
