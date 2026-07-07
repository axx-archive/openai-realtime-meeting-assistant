package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentThreadUsesCodexExecWorkerWhenConfigured(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("BONFIRE_AGENT_THREAD_WORKER", "codex_exec")
	t.Setenv("BONFIRE_CODEX_RUNNER_MODE", "local_exec")
	t.Setenv("BONFIRE_CODEX_CWD", t.TempDir())
	t.Setenv("BONFIRE_CODEX_SANDBOX", "read-only")
	t.Setenv("BONFIRE_CODEX_APPROVAL_POLICY", "never")
	t.Setenv("BONFIRE_CODEX_REASONING_EFFORT", "medium")

	originalRunner := runCodexExecCommand
	defer func() { runCodexExecCommand = originalRunner }()

	var capturedConfig codexExecConfig
	var capturedPrompt string
	runCodexExecCommand = func(_ context.Context, cfg codexExecConfig, prompt string) (codexExecResult, error) {
		capturedConfig = cfg
		capturedPrompt = prompt
		return codexExecResult{FinalMessage: "Vision: Realtime controls Codex.\n\n## Codex worker evidence\n- Worker: codex exec"}, nil
	}

	thread := scoutAgentThread{
		ID:     "agent-thread-workflow-test",
		Mode:   "workflow",
		Query:  "run the multi-agent process for a research report",
		Status: "running",
	}
	result, err := app.produceAgentThreadArtifactWithWorker(context.Background(), thread, nil)
	if err != nil {
		t.Fatalf("produceAgentThreadArtifactWithWorker: %v", err)
	}
	if got := result.Metadata["worker"]; got != agentThreadWorkerCodexExec {
		t.Fatalf("worker metadata=%q, want %q", got, agentThreadWorkerCodexExec)
	}
	if capturedConfig.Sandbox != "read-only" || capturedConfig.ApprovalPolicy != "never" || capturedConfig.Reasoning != "medium" {
		t.Fatalf("capturedConfig=%+v, want explicit sandbox/approval/reasoning", capturedConfig)
	}
	for _, want := range []string{"Use Codex subagents", "research agent", "skeptical review agent", thread.Query, "Gate before shipping"} {
		if !strings.Contains(capturedPrompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, capturedPrompt)
		}
	}
	if !strings.Contains(result.Text, "Realtime controls Codex") {
		t.Fatalf("result.Text=%q, want Codex final message", result.Text)
	}
}

func TestAgentThreadQueuesCodexSidecarJobByDefault(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	queueDir := t.TempDir()
	t.Setenv("BONFIRE_AGENT_THREAD_WORKER", "codex_exec")
	t.Setenv("BONFIRE_CODEX_QUEUE_PATH", queueDir)

	artifact, _, err := app.createOSArtifactWithMetadata("research", "Map the market", "queued", "tester", map[string]string{
		"title": "Market map",
	})
	if err != nil {
		t.Fatalf("createOSArtifactWithMetadata: %v", err)
	}
	thread := scoutAgentThread{
		ID:       "agent-thread-research-test",
		Mode:     "research",
		Query:    "research the current market and write a sourced report",
		Artifact: artifact,
	}

	result, err := app.produceAgentThreadArtifactWithWorker(context.Background(), thread, nil)
	if err != nil {
		t.Fatalf("produceAgentThreadArtifactWithWorker: %v", err)
	}
	if result.Terminal {
		t.Fatal("result.Terminal=true, want queued sidecar job")
	}
	if got := result.Metadata["threadStatus"]; got != codexJobStatusQueued {
		t.Fatalf("threadStatus=%q, want queued", got)
	}
	if result.Metadata["workerBoundary"] != "codex_sidecar_queue" || result.Metadata["runnerJobId"] == "" {
		t.Fatalf("metadata=%v, want sidecar queue job id", result.Metadata)
	}

	store := newCodexRunnerJobStore(queueDir)
	job, err := store.claimNext("test-runner")
	if err != nil {
		t.Fatalf("claimNext: %v", err)
	}
	if job == nil {
		t.Fatal("claimNext returned nil, want queued job")
	}
	if job.ArtifactID != artifact.ID || job.ThreadID != thread.ID {
		t.Fatalf("job=%+v, want artifact/thread ids", job)
	}
	if job.Authority != codexJobAuthorityReadOnly {
		t.Fatalf("authority=%q, want read_only", job.Authority)
	}
	if !strings.Contains(job.Prompt, "EXTERNAL_WRITE_APPROVAL_REQUIRED") || !strings.Contains(job.Prompt, thread.Query) {
		t.Fatalf("prompt missing approval gate or query:\n%s", job.Prompt)
	}
}

func TestAgentThreadBlocksExternalWriteBeforeCodexRun(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("BONFIRE_AGENT_THREAD_WORKER", "codex_exec")
	t.Setenv("BONFIRE_CODEX_QUEUE_PATH", t.TempDir())

	artifact, _, err := app.createOSArtifactWithMetadata("workflow", "Deploy", "queued", "tester", map[string]string{
		"title": "Deploy",
	})
	if err != nil {
		t.Fatalf("createOSArtifactWithMetadata: %v", err)
	}
	thread := scoutAgentThread{
		ID:       "agent-thread-deploy-test",
		Mode:     "workflow",
		Query:    "commit, push, SSH to the VPS, and deploy this now",
		Artifact: artifact,
	}

	result, err := app.produceAgentThreadArtifactWithWorker(context.Background(), thread, nil)
	if err != nil {
		t.Fatalf("produceAgentThreadArtifactWithWorker: %v", err)
	}
	if result.Terminal {
		t.Fatal("result.Terminal=true, want approval gate")
	}
	if result.Metadata["threadStatus"] != codexJobStatusApprovalRequired || result.Metadata["reviewGate"] != "approval_required" {
		t.Fatalf("metadata=%v, want approval gate", result.Metadata)
	}
	if !strings.Contains(result.Text, "approval required") {
		t.Fatalf("text=%q, want approval-required artifact", result.Text)
	}
}

func TestAgentThreadBlocksExternalWriteBeforeLocalCodexExec(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("BONFIRE_AGENT_THREAD_WORKER", "codex_exec")
	t.Setenv("BONFIRE_CODEX_RUNNER_MODE", "local_exec")
	t.Setenv("BONFIRE_CODEX_CWD", t.TempDir())

	originalRunner := runCodexExecCommand
	defer func() { runCodexExecCommand = originalRunner }()
	runCodexExecCommand = func(_ context.Context, _ codexExecConfig, _ string) (codexExecResult, error) {
		t.Fatal("local codex exec should not run before external-write approval")
		return codexExecResult{}, nil
	}

	thread := scoutAgentThread{
		ID:     "agent-thread-local-deploy-test",
		Mode:   "workflow",
		Query:  "ship this live, commit it, push it, and restart production",
		Status: "running",
	}
	result, err := app.produceAgentThreadArtifactWithWorker(context.Background(), thread, nil)
	if err != nil {
		t.Fatalf("produceAgentThreadArtifactWithWorker: %v", err)
	}
	if result.Terminal {
		t.Fatal("result.Terminal=true, want approval gate")
	}
	if result.Metadata["threadStatus"] != codexJobStatusApprovalRequired || result.Metadata["workerBoundary"] != "codex_external_write_gate" {
		t.Fatalf("metadata=%v, want external-write approval gate", result.Metadata)
	}
}

func TestInternalCodexRunnerCallbackUpdatesArtifact(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	defer func() { kanbanApp = previousApp }()
	t.Setenv("BONFIRE_RUNNER_TOKEN", "runner-secret")

	artifact, _, err := app.createOSArtifactWithMetadata("workflow", "Build it", "queued", "tester", map[string]string{
		"title":        "Build it",
		"threadStatus": codexJobStatusQueued,
	})
	if err != nil {
		t.Fatalf("createOSArtifactWithMetadata: %v", err)
	}
	body, err := json.Marshal(codexRunnerCallbackPayload{
		JobID:      "codex-job-test",
		ArtifactID: artifact.ID,
		Status:     codexJobStatusComplete,
		Text:       "Vision: finished\n\n## Codex worker evidence\n- Worker: codex exec",
		Metadata: map[string]string{
			"runnerId":        "test-runner",
			"progressPercent": "100",
			"reviewGate":      "passed",
		},
	})
	if err != nil {
		t.Fatalf("marshal callback: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/internal/codex/jobs/result", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runner-secret")
	recorder := httptest.NewRecorder()

	internalCodexRunnerResultHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	updated, exists := app.osArtifactByID(artifact.ID)
	if !exists {
		t.Fatalf("artifact %q not found", artifact.ID)
	}
	if updated.Metadata["threadStatus"] != codexJobStatusComplete || updated.Metadata["runnerJobId"] != "codex-job-test" {
		t.Fatalf("metadata=%v, want complete runner callback", updated.Metadata)
	}
	if !strings.Contains(updated.Text, "finished") {
		t.Fatalf("updated.Text=%q, want callback text", updated.Text)
	}
}

func TestInternalCodexRunnerCallbackRejectsStaleJobID(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	defer func() { kanbanApp = previousApp }()
	t.Setenv("BONFIRE_RUNNER_TOKEN", "runner-secret")

	artifact, _, err := app.createOSArtifactWithMetadata("workflow", "Build it", "queued", "tester", map[string]string{
		"title":        "Build it",
		"threadStatus": codexJobStatusQueued,
		"runnerJobId":  "codex-job-current",
	})
	if err != nil {
		t.Fatalf("createOSArtifactWithMetadata: %v", err)
	}
	body, err := json.Marshal(codexRunnerCallbackPayload{
		JobID:      "codex-job-stale",
		ArtifactID: artifact.ID,
		Status:     codexJobStatusComplete,
		Text:       "stale result",
	})
	if err != nil {
		t.Fatalf("marshal callback: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/internal/codex/jobs/result", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runner-secret")
	recorder := httptest.NewRecorder()

	internalCodexRunnerResultHandler(recorder, req)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want conflict", recorder.Code, recorder.Body.String())
	}
	updated, exists := app.osArtifactByID(artifact.ID)
	if !exists {
		t.Fatalf("artifact %q not found", artifact.ID)
	}
	if updated.Text == "stale result" || updated.Metadata["runnerJobId"] != "codex-job-current" {
		t.Fatalf("updated=%+v, stale callback should not overwrite artifact", updated)
	}
}

func TestApproveCodexArtifactExternalWriteQueuesApprovedJob(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	queueDir := t.TempDir()
	t.Setenv("BONFIRE_CODEX_QUEUE_PATH", queueDir)

	artifact, _, err := app.createOSArtifactWithMetadata("workflow", "Deploy it", "approval needed", "tester", map[string]string{
		"title":        "Deploy it",
		"threadId":     "agent-thread-approve-test",
		"threadQuery":  "commit, push, SSH to the VPS, and deploy this now",
		"threadStatus": codexJobStatusApprovalRequired,
		"reviewGate":   "approval_required",
	})
	if err != nil {
		t.Fatalf("createOSArtifactWithMetadata: %v", err)
	}

	updated, actions, err := app.approveCodexArtifactExternalWrite(artifact, "AJ")
	if err != nil {
		t.Fatalf("approveCodexArtifactExternalWrite: %v", err)
	}
	if updated.Metadata["threadStatus"] != codexJobStatusQueued || updated.Metadata["approvedBy"] != "AJ" {
		t.Fatalf("metadata=%v, want queued approved artifact", updated.Metadata)
	}
	if len(actions) == 0 {
		t.Fatal("actions empty, want artifact navigation")
	}

	store := newCodexRunnerJobStore(queueDir)
	job, err := store.claimNext("test-runner")
	if err != nil {
		t.Fatalf("claimNext: %v", err)
	}
	if job == nil {
		t.Fatal("claimNext returned nil, want approved job")
	}
	if job.Authority != codexJobAuthorityExternalWrite || !strings.Contains(job.Prompt, "external_write") {
		t.Fatalf("job=%+v, want approved external_write prompt", job)
	}
}

func TestRunCodexExecCommandContextBuildsNoninteractiveCommand(t *testing.T) {
	dir := t.TempDir()
	recordArgs := filepath.Join(dir, "args.txt")
	recordPrompt := filepath.Join(dir, "prompt.txt")
	fakeCodex := filepath.Join(dir, "codex")
	script := `#!/bin/sh
printf '%s\n' "$@" > "$RECORD_ARGS"
cat > "$RECORD_PROMPT"
previous=""
for arg in "$@"; do
  if [ "$previous" = "--output-last-message" ]; then
    printf 'Vision: fake codex finished\n' > "$arg"
  fi
  previous="$arg"
done
`
	if err := os.WriteFile(fakeCodex, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}

	t.Setenv("RECORD_ARGS", recordArgs)
	t.Setenv("RECORD_PROMPT", recordPrompt)
	result, err := runCodexExecCommandContext(context.Background(), codexExecConfig{
		Command:        fakeCodex,
		CWD:            dir,
		Sandbox:        "read-only",
		ApprovalPolicy: "never",
		Reasoning:      "high",
		Timeout:        defaultCodexExecTimeout,
		MaxOutputBytes: 4096,
		Search:         true,
		Ephemeral:      true,
		SkipGitCheck:   true,
	}, "hello codex worker")
	if err != nil {
		t.Fatalf("runCodexExecCommandContext: %v", err)
	}
	if strings.TrimSpace(result.FinalMessage) != "Vision: fake codex finished" {
		t.Fatalf("FinalMessage=%q", result.FinalMessage)
	}
	rawArgs, err := os.ReadFile(recordArgs)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	args := string(rawArgs)
	if !strings.HasPrefix(args, "--search\nexec\n") {
		t.Fatalf("args=%q, want top-level --search before exec", args)
	}
	for _, want := range []string{"--search\n", "exec\n", "--cd\n" + dir, "--sandbox\nread-only", "approval_policy=\"never\"", "model_reasoning_effort=\"high\"", "--ephemeral", "--skip-git-repo-check", "-\n"} {
		if !strings.Contains(args, want) {
			t.Fatalf("args missing %q:\n%s", want, args)
		}
	}
	rawPrompt, err := os.ReadFile(recordPrompt)
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	if strings.TrimSpace(string(rawPrompt)) != "hello codex worker" {
		t.Fatalf("prompt=%q", string(rawPrompt))
	}
}

func TestCodexExecEnvironmentMapsOpenAIKeyToCodexKey(t *testing.T) {
	env := codexExecEnvironment([]string{"OPENAI_API_KEY=sk-test", "PATH=/bin"})
	if envValue(env, "CODEX_API_KEY") != "sk-test" {
		t.Fatalf("CODEX_API_KEY=%q, want mapped API key", envValue(env, "CODEX_API_KEY"))
	}

	env = codexExecEnvironment([]string{"OPENAI_API_KEY=sk-test", "CODEX_API_KEY=sk-codex"})
	if envValue(env, "CODEX_API_KEY") != "sk-codex" {
		t.Fatalf("CODEX_API_KEY=%q, want existing codex key preserved", envValue(env, "CODEX_API_KEY"))
	}
}

func TestCodexOutputRequiresExternalApprovalMatchesGateMarkerOnly(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   bool
	}{
		{
			name:   "standalone marker",
			output: "Gate\n\nEXTERNAL_WRITE_APPROVAL_REQUIRED: run deploy after approval",
			want:   true,
		},
		{
			name:   "bulleted marker",
			output: "Gate\n\n- **EXTERNAL_WRITE_APPROVAL_REQUIRED:** send email after approval",
			want:   true,
		},
		{
			name:   "negated prose mention",
			output: "Gate\n\nNo `EXTERNAL_WRITE_APPROVAL_REQUIRED` action is currently required.",
			want:   false,
		},
		{
			name:   "instructional mention",
			output: "Report\n\nThe marker EXTERNAL_WRITE_APPROVAL_REQUIRED is only used for external side effects.",
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := codexOutputRequiresExternalApproval(tc.output); got != tc.want {
				t.Fatalf("codexOutputRequiresExternalApproval()=%v, want %v", got, tc.want)
			}
		})
	}
}

// The codex exec path is the THIRD instruction site (prod children run
// worker=codex_exec): its prompt demanded "a polished Markdown artifact with
// stable headings for: Vision, Goal, ..." — at war with a raw-document
// contract — and appendCodexWorkerEvidence bolts a markdown section AFTER the
// output, trailing junk after a deck's closing </html>. Both must yield when
// the child carries a raw-document outputContract.
func TestCodexPromptHonorsRawDocumentContract(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread := scoutAgentThread{
		ID:    "agent-thread-artifacts-raw",
		Mode:  "artifacts",
		Query: "Ship — the self-contained presenter deck",
		Artifact: meetingMemoryEntry{
			ID:       "os-artifact-artifacts-raw",
			Kind:     "os_artifact",
			Metadata: map[string]string{"outputContract": "packaging_deck_v1"},
		},
	}
	prompt := app.buildCodexAgentThreadPrompt(thread, time.Now(), toolAuthorityWorkspaceWrite)
	for _, banned := range []string{"polished Markdown artifact", "Vision, Goal"} {
		if strings.Contains(prompt, banned) {
			t.Fatalf("raw-document codex prompt still demands %q", banned)
		}
	}
	if !strings.Contains(prompt, "<!doctype html>") {
		t.Fatalf("raw-document codex prompt must demand the doctype-first file:\n%s", prompt[len(prompt)-400:])
	}

	// The generic prompt keeps its shape.
	plain := thread
	plain.Artifact.Metadata = map[string]string{}
	if got := app.buildCodexAgentThreadPrompt(plain, time.Now(), toolAuthorityWorkspaceWrite); !strings.Contains(got, "polished Markdown artifact") {
		t.Fatal("plain codex prompt lost its generic artifact contract")
	}

	// Worker evidence never trails a raw document; plain output keeps it.
	deck := "<!doctype html><html><body><section class=\"pg\">s</section></body></html>"
	cfg := codexExecConfig{CWD: "/tmp", Sandbox: "workspace-write", ApprovalPolicy: "never"}
	if got := appendCodexWorkerEvidenceForContract(deck, cfg, "packaging_deck_v1"); got != deck {
		t.Fatalf("raw deck grew trailing evidence:\n%s", got[len(deck):])
	}
	if got := appendCodexWorkerEvidenceForContract("report body", cfg, ""); !strings.Contains(got, "Codex worker evidence") {
		t.Fatal("plain output lost the worker evidence section")
	}
}

// The sidecar queue path (prod's worker=codex_exec runs MODE=sidecar_queue) is
// a SECOND evidence-append site: the runner's result handler appended the
// worker-evidence footer to every output, so a raw-HTML deck got a "## Codex
// worker evidence" block after </html> — which the PDF export rendered as a
// trailing junk page (caught live on the Ember run). The contract must ride
// the queued job so the runner keeps the footer off a deck.
func TestCodexQueuedJobCarriesOutputContract(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("BONFIRE_CODEX_QUEUE_PATH", t.TempDir())

	thread := scoutAgentThread{
		ID:    "agent-thread-artifacts-deck",
		Mode:  "artifacts",
		Query: "Ship — the self-contained presenter deck",
		Artifact: meetingMemoryEntry{
			ID:       "os-artifact-artifacts-deck",
			Kind:     "os_artifact",
			Metadata: map[string]string{"outputContract": "packaging_deck_v1"},
		},
	}
	if _, err := app.enqueueCodexAgentThreadJob(thread, toolAuthorityWorkspaceWrite); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	store := newCodexRunnerJobStore(codexRunnerQueuePath())
	job, err := store.claimNext("test-runner")
	if err != nil || job == nil {
		t.Fatalf("claimNext: job=%v err=%v", job, err)
	}
	if job.Metadata["outputContract"] != "packaging_deck_v1" {
		t.Fatalf("queued job outputContract=%q, want the raw-document contract propagated", job.Metadata["outputContract"])
	}
	// The runner's contract-aware append leaves the deck untouched.
	deck := "<!doctype html><html><body><section class=\"pg\">s</section></body></html>"
	cfg := codexExecConfig{CWD: "/tmp", Sandbox: "workspace-write", ApprovalPolicy: "never"}
	if got := appendCodexWorkerEvidenceForContract(deck, cfg, job.Metadata["outputContract"]); got != deck {
		t.Fatalf("deck grew trailing evidence in the runner path:\n%s", got[len(deck):])
	}
}
