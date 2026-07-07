package main

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"
)

// AgentJob is the provider-agnostic unit of agentic work. It is derived from a
// scoutAgentThread + its os_artifact, so nothing new is persisted to launch a
// job — the artifact IS the durable record. The exported fields are the
// provider contract; the unexported `thread` carries the full launch record so
// the wrapper providers (codex/openai) can call today's functions unchanged.
type AgentJob struct {
	JobID       string // == artifact/thread run id
	ArtifactID  string // durable os_artifact this job writes back to
	ThreadID    string
	Mode        string            // research|design|grill|workflow|artifacts|goal
	Objective   string            // the user's goal text (thread.Query)
	Authority   string            // read_only|workspace_write|external_write
	Context     AgentJobContext   // board snapshot, memory window, domain vocab
	Origin      map[string]string // originKind/originId/originMeetingId (delivery)
	RequestedBy string            // signed-in email; provenance + authority checks

	// Effort / MaxTokens override the runner's env defaults for THIS job. The
	// /goal engine stamps a heavier budget on the deliverable subtask (via the
	// goalDeliverable metadata flag) so the contract-bearing artifact does not
	// truncate. Zero/empty means "use the runner default", so every other job is
	// unchanged.
	Effort    string
	MaxTokens int

	thread scoutAgentThread // full launch record for the wrapper providers
}

// AgentJobContext is the read-only working context handed to a runner. It is a
// snapshot taken at launch so a slow provider never reads a mutating board.
type AgentJobContext struct {
	Board  kanbanBoardState
	Memory []meetingMemoryEntry
}

// AgentCapabilities is what a provider can physically do. The /goal engine
// (Wave 2) reads these to decide which runner may take a subtask (a
// CanShell:false runner never gets a "run the tests" subtask).
type AgentCapabilities struct {
	CanShell    bool // run commands, tests
	CanBrowse   bool // live web / --search
	CanEditRepo bool // mutate files in a git workspace
	CanCommit   bool // external_write side effects (still admin-gated)
	ToolLoop    bool // can call Bonfire in-process tools mid-run
	MaxRuntime  time.Duration
}

// AgentProgress is one streamed update. The engine translates it to artifact
// metadata (progressPercent, currentStage, goalStatus, reviewGate) and a
// broadcastAssistantEvent, reusing the terminal-seam plumbing today.
type AgentProgress struct {
	Stage           string // one of the goalWorkflowStage* strings
	ProgressPercent int    // 0..100 -> metadata["progressPercent"]
	GoalStatus      string // running|review|approval_required|verified|needs_attention
	ReviewGate      string // pending|passed|blocked|approval_required
	Note            string // short operator-voice line, broadcast to the UI
	Terminal        bool
	Text            string            // set on Terminal: the finished artifact body
	Err             error             // set on Terminal failure
	Metadata        map[string]string // provider evidence (model, tokens, sandbox…)
}

// AgentRunner is the one seam. RunJob is non-blocking: it returns a channel of
// progress the engine drains onto the artifact. The codex sidecar provider
// emits one {queued} progress then closes; its HTTP callback lands the terminal
// state through the existing artifact-update path. The in-process anthropic
// provider streams execute → review → gate transitions as they happen.
type AgentRunner interface {
	Name() string
	Capabilities() AgentCapabilities
	RunJob(ctx context.Context, job AgentJob) (<-chan AgentProgress, error)
}

// Runner name constants. These are the values BONFIRE_AGENT_RUNNER /
// BONFIRE_EXECUTION_RUNNER resolve to.
const (
	agentRunnerAnthropicFable = "anthropic_fable"
	agentRunnerCodexSidecar   = "codex_sidecar"
	agentRunnerCodexLocal     = "codex_local"
	agentRunnerOpenAIText     = "openai_text"
	agentRunnerStub           = "stub"
)

// defaultReviewModel is the dedicated reviewer/ship-gate model (Wave 3 item
// 16). Reviews read WHOLE artifact bodies (goalReviewArtifactBody), so the
// judging seat wants Opus-tier context at Opus rates rather than the Fable
// ceiling, while orchestration (decompose/report/verify) stays on the
// orchestrator model.
const defaultReviewModel = "claude-opus-4-8"

// reviewModel resolves the reviewer/gate model — the review-side twin of the
// per-subtask runner override above: env-with-default, resolved once at engine
// construction and routed per call (goal_engine.callReviewModel).
func reviewModel() string {
	return getenvDefault("BONFIRE_REVIEW_MODEL", defaultReviewModel)
}

// newAgentJob derives an AgentJob from a launched thread. It reads the additive
// goal-spec metadata (absent = today's behavior) and snapshots board + memory
// so a runner never reads a mutating board.
func (app *kanbanBoardApp) newAgentJob(thread scoutAgentThread) AgentJob {
	meta := thread.Artifact.Metadata
	authority := strings.TrimSpace(meta["authority"])
	if authority == "" {
		authority = codexJobAuthorityForThread(thread)
	}
	job := AgentJob{
		JobID:       thread.ID,
		ArtifactID:  thread.Artifact.ID,
		ThreadID:    thread.ID,
		Mode:        thread.Mode,
		Objective:   firstNonEmptyString(strings.TrimSpace(meta["objective"]), thread.Query),
		Authority:   authority,
		Context:     AgentJobContext{Board: app.snapshotState(), Memory: app.memorySnapshotForClients(20)},
		Origin:      agentJobOrigin(meta),
		RequestedBy: firstNonEmptyString(strings.TrimSpace(meta["requestedBy"]), strings.TrimSpace(meta["createdBy"])),
		thread:      thread,
	}
	// A /goal deliverable subtask asks for a heavier budget so its
	// contract-bearing artifact does not truncate under the planning default.
	if strings.EqualFold(strings.TrimSpace(meta["goalDeliverable"]), "true") {
		job.Effort = deliverableEffort()
		job.MaxTokens = deliverableMaxTokens()
	}
	return job
}

func agentJobOrigin(meta map[string]string) map[string]string {
	origin := map[string]string{}
	for _, key := range agentThreadOriginMetadataKeys {
		if value := strings.TrimSpace(meta[key]); value != "" {
			origin[key] = value
		}
	}
	if len(origin) == 0 {
		return nil
	}
	return origin
}

// selectedAgentRunnerName resolves the orchestrator runner from env, honoring
// back-compat aliases and the keyless fallback. It is a pure function (no app)
// so the selection matrix is testable without a live board.
//
// Default: anthropic_fable when ANTHROPIC_API_KEY is set, else today's worker
// behavior (openai_text or codex_* per BONFIRE_AGENT_THREAD_WORKER /
// BONFIRE_CODEX_AGENT_THREADS). Deploys without the key are unchanged.
func selectedAgentRunnerName() string {
	explicit := strings.ToLower(strings.TrimSpace(os.Getenv("BONFIRE_AGENT_RUNNER")))
	switch explicit {
	case "":
		if hasAnthropicAPIKey() {
			return agentRunnerAnthropicFable
		}
		return legacyWorkerRunnerName()
	case agentRunnerAnthropicFable, "anthropic", "fable":
		if hasAnthropicAPIKey() {
			return agentRunnerAnthropicFable
		}
		// Keyless fallback: an anthropic request without the key would 503;
		// degrade to today's worker so the shell stays up.
		return legacyWorkerRunnerName()
	case agentRunnerCodexSidecar, "codex", "codex_exec", "sidecar":
		return agentRunnerCodexSidecar
	case agentRunnerCodexLocal, "local", "local_exec":
		return agentRunnerCodexLocal
	case agentRunnerOpenAIText, "openai", "responses", "text":
		return agentRunnerOpenAIText
	case agentRunnerStub, "none":
		return agentRunnerStub
	default:
		return legacyWorkerRunnerName()
	}
}

// legacyWorkerRunnerName maps the pre-existing worker envs onto runner names so
// BONFIRE_AGENT_THREAD_WORKER=codex_exec and BONFIRE_CODEX_AGENT_THREADS keep
// working without a deploy config change.
func legacyWorkerRunnerName() string {
	switch configuredAgentThreadWorkerMode() {
	case agentThreadWorkerCodexExec:
		if configuredCodexRunnerMode() == codexRunnerModeLocalExec {
			return agentRunnerCodexLocal
		}
		return agentRunnerCodexSidecar
	default:
		return agentRunnerOpenAIText
	}
}

// selectedExecutionRunnerName resolves the execution backend for can-shell /
// can-edit sub-jobs (Wave 2 wires it to /goal children). Default codex_sidecar,
// where the sandbox + authority ladder already live.
func selectedExecutionRunnerName() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BONFIRE_EXECUTION_RUNNER"))) {
	case agentRunnerCodexLocal, "local", "local_exec":
		return agentRunnerCodexLocal
	case agentRunnerAnthropicFable, "anthropic", "fable":
		return agentRunnerAnthropicFable
	case "none":
		return agentRunnerStub
	case "", agentRunnerCodexSidecar, "codex", "sidecar":
		return agentRunnerCodexSidecar
	default:
		return agentRunnerCodexSidecar
	}
}

// selectAgentRunner builds the concrete orchestrator runner for a job. The
// responder is threaded through so the openai_text provider stays test-injectable
// exactly as produceAgentThreadArtifact did.
func (app *kanbanBoardApp) selectAgentRunner(job AgentJob, responder openAITextResponder) AgentRunner {
	name := selectedAgentRunnerName()
	// A /goal subtask carries the concrete runner its capability match assigned
	// (assignGoalRunners). Honoring it routes shell/repo subtasks to the
	// execution runner while everything else stays on the orchestrator. Only
	// goal children set this key, so non-goal threads are unchanged.
	if override := resolveAssignedRunnerName(job.thread.Artifact.Metadata["assignedRunner"]); override != "" {
		name = override
	}
	switch name {
	case agentRunnerAnthropicFable:
		return newAnthropicFableRunner(app)
	case agentRunnerCodexSidecar:
		return &codexSidecarAgentRunner{app: app, local: false}
	case agentRunnerCodexLocal:
		return &codexSidecarAgentRunner{app: app, local: true}
	case agentRunnerStub:
		return &stubAgentRunner{}
	default:
		return &openAITextAgentRunner{app: app, responder: responder}
	}
}

// resolveAssignedRunnerName validates a per-subtask runner override against the
// known runner names; an unknown/empty value returns "" so the env-selected
// default stands. The anthropic path still degrades keyless (an anthropic
// override with no key would 503) by falling back to the legacy worker.
func resolveAssignedRunnerName(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case agentRunnerAnthropicFable:
		if hasAnthropicAPIKey() {
			return agentRunnerAnthropicFable
		}
		return legacyWorkerRunnerName()
	case agentRunnerCodexSidecar:
		return agentRunnerCodexSidecar
	case agentRunnerCodexLocal:
		return agentRunnerCodexLocal
	case agentRunnerOpenAIText:
		return agentRunnerOpenAIText
	case agentRunnerStub:
		return agentRunnerStub
	default:
		return ""
	}
}

// agentProgressMetadata maps a single progress update to additive artifact
// metadata. Only non-zero typed fields add their key; provider Metadata passes
// through wholesale so the wrapper providers carry today's exact worker metadata.
func agentProgressMetadata(progress AgentProgress) map[string]string {
	metadata := map[string]string{}
	if progress.ProgressPercent > 0 {
		percent := progress.ProgressPercent
		if percent > 100 {
			percent = 100
		}
		metadata["progressPercent"] = strconv.Itoa(percent)
	}
	if strings.TrimSpace(progress.Stage) != "" {
		metadata["currentStage"] = progress.Stage
	}
	if strings.TrimSpace(progress.GoalStatus) != "" {
		metadata["goalStatus"] = progress.GoalStatus
	}
	if strings.TrimSpace(progress.ReviewGate) != "" {
		metadata["reviewGate"] = progress.ReviewGate
	}
	// The live line the client renders under the progress bar ("consulting
	// memory", "drafting the report"). Capped for storage: notes derive from
	// model output and tool names, not a bounded vocabulary.
	if strings.TrimSpace(progress.Note) != "" {
		metadata["progressNote"] = trimForStorage(compactAssistantLine(progress.Note), 140)
	}
	for key, value := range progress.Metadata {
		if strings.TrimSpace(value) != "" {
			metadata[key] = value
		}
	}
	return metadata
}

// drainAgentProgress folds a runner's progress channel into the synchronous
// agentThreadWorkerResult the produceAgentThreadArtifactWithWorker seam returns.
// onProgress (may be nil) sees each update for live UI streaming. The fold
// preserves the exact result shape the pre-runner switch returned: Terminal is
// set only by a terminal progress, Text/Metadata accumulate (last non-empty
// wins), and any Err — terminal or not — becomes the returned error.
func drainAgentProgress(out <-chan AgentProgress, onProgress func(AgentProgress)) (agentThreadWorkerResult, error) {
	result := agentThreadWorkerResult{Metadata: map[string]string{}}
	var runErr error
	for progress := range out {
		for key, value := range agentProgressMetadata(progress) {
			result.Metadata[key] = value
		}
		if strings.TrimSpace(progress.Text) != "" {
			result.Text = progress.Text
		}
		if progress.Terminal {
			result.Terminal = true
		}
		if progress.Err != nil {
			runErr = progress.Err
		}
		if onProgress != nil {
			onProgress(progress)
		}
	}
	return result, runErr
}

func hasAnthropicAPIKey() bool {
	return strings.TrimSpace(currentAnthropicAPIKey()) != ""
}
