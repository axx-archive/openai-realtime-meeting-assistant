package main

import "context"

// The wrapper providers adapt today's three worker paths to the AgentRunner
// interface. Each RunJob delegates to the exact function the pre-runner switch
// called and emits one progress carrying that function's result verbatim, so
// draining it reproduces the old agentThreadWorkerResult with zero behavior
// change. The async channel model makes the codex sidecar's "queued now,
// terminal via HTTP callback" flow first-class instead of a special case.

// openAITextAgentRunner wraps the single-completion Responses writer.
type openAITextAgentRunner struct {
	app       *kanbanBoardApp
	responder openAITextResponder
}

func (r *openAITextAgentRunner) Name() string { return agentRunnerOpenAIText }

func (r *openAITextAgentRunner) Capabilities() AgentCapabilities {
	return AgentCapabilities{ToolLoop: false, MaxRuntime: defaultAgentThreadRequestTimeout}
}

func (r *openAITextAgentRunner) RunJob(ctx context.Context, job AgentJob) (<-chan AgentProgress, error) {
	out := make(chan AgentProgress, 1)
	go func() {
		defer close(out)
		output, err := r.app.produceAgentThreadArtifact(ctx, job.thread, r.responder)
		out <- AgentProgress{
			Terminal: true,
			Text:     output,
			Err:      err,
			Metadata: map[string]string{
				"worker":         "openai_text_response",
				"workerBoundary": "responses_artifact_writer",
			},
		}
	}()
	return out, nil
}

// codexSidecarAgentRunner wraps the codex queue (sidecar) or local-exec path.
// RunJob enqueues (or runs) and emits one progress; for the sidecar queue that
// progress is non-terminal ({queued}) and the existing HTTP callback
// (internalCodexRunnerResultHandler) lands the terminal state.
type codexSidecarAgentRunner struct {
	app   *kanbanBoardApp
	local bool
}

func (r *codexSidecarAgentRunner) Name() string {
	if r.local {
		return agentRunnerCodexLocal
	}
	return agentRunnerCodexSidecar
}

func (r *codexSidecarAgentRunner) Capabilities() AgentCapabilities {
	return AgentCapabilities{
		CanShell:    true,
		CanBrowse:   true,
		CanEditRepo: true,
		CanCommit:   true, // still admin-gated at the external_write ladder
		ToolLoop:    false,
		MaxRuntime:  codexExecConfigFromEnv().Timeout,
	}
}

func (r *codexSidecarAgentRunner) RunJob(ctx context.Context, job AgentJob) (<-chan AgentProgress, error) {
	out := make(chan AgentProgress, 1)
	go func() {
		defer close(out)
		var result agentThreadWorkerResult
		var err error
		if r.local {
			result, err = r.app.produceCodexAgentThreadArtifact(ctx, job.thread)
		} else {
			result, err = r.app.enqueueCodexAgentThreadArtifact(ctx, job.thread)
		}
		out <- AgentProgress{
			Terminal: result.Terminal,
			Text:     result.Text,
			Err:      err,
			Metadata: result.Metadata,
		}
	}()
	return out, nil
}

// stubAgentRunner is the keyless-local fallback. It mirrors the "worker not
// configured — handoff pending" artifact the OpenAI path writes when no key is
// present, so the whole shell stays up and agentic features degrade cleanly.
type stubAgentRunner struct{}

func (r *stubAgentRunner) Name() string { return agentRunnerStub }

func (r *stubAgentRunner) Capabilities() AgentCapabilities {
	return AgentCapabilities{MaxRuntime: defaultAgentThreadRequestTimeout}
}

func (r *stubAgentRunner) RunJob(ctx context.Context, job AgentJob) (<-chan AgentProgress, error) {
	out := make(chan AgentProgress, 1)
	go func() {
		defer close(out)
		out <- AgentProgress{
			Terminal:        true,
			Stage:           "gate_before_shipping",
			ProgressPercent: 72,
			GoalStatus:      "needs_attention",
			ReviewGate:      "blocked",
			Note:            "agent worker is not configured",
			Err:             errAgentWorkerNotConfigured,
			Metadata: map[string]string{
				"worker":         agentRunnerStub,
				"workerBoundary": "stub_handoff_pending",
			},
		}
	}()
	return out, nil
}
