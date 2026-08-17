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
	Context     AgentJobContext   // current authorized source window and domain vocab
	Origin      map[string]string // originKind/originId/originMeetingId (delivery)
	RequestedBy string            // signed-in email; provenance + authority checks

	// Effort / MaxTokens are retained only for historical Anthropic-run receipt
	// decoding and its isolated tests. Active OpenAI/Codex routes ignore them;
	// model and reasoning are selected by the server-owned job lane.
	Effort    string
	MaxTokens int

	thread scoutAgentThread // full launch record for the wrapper providers
}

// AgentJobContext is the read-only working context handed to a runner. Board is
// retained only for decoding older provider receipts; new jobs leave it empty
// and use current authorized sources plus server-owned Work/Project bindings.
type AgentJobContext struct {
	Board  kanbanBoardState
	Memory []meetingMemoryEntry
}

func activeAgentMemory(entries []meetingMemoryEntry) []meetingMemoryEntry {
	filtered := make([]meetingMemoryEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Kind == meetingMemoryKindBoardUpdate {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
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
// progress the engine drains onto the artifact. Active admission is limited to
// the OpenAI text runner or an independently fenced Codex adapter; historical
// Anthropic assignments resolve to the unavailable stub.
type AgentRunner interface {
	Name() string
	Capabilities() AgentCapabilities
	RunJob(ctx context.Context, job AgentJob) (<-chan AgentProgress, error)
}

// Runner name constants include historical persisted labels for decode and
// fail-closed compatibility.
const (
	agentRunnerAnthropicFable = "anthropic_fable"
	agentRunnerCodexSidecar   = "codex_sidecar"
	agentRunnerCodexLocal     = "codex_local"
	agentRunnerOpenAIText     = "openai_text"
	agentRunnerStub           = "stub"
)

// defaultReviewModel is the founder-approved final review/ship-gate model.
// The actual goal engine also pins max reasoning for this seat.
const defaultReviewModel = "gpt-5.6-sol"

func reviewModel() string {
	return defaultReviewModel
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
	requestedBy := firstNonEmptyString(strings.TrimSpace(meta["requestedBy"]), strings.TrimSpace(meta["createdBy"]))
	principal, principalOK := app.agentThreadRecallPrincipal(requestedBy, meta)
	var memory []meetingMemoryEntry
	if principalOK {
		memory = activeAgentMemory(app.memorySnapshotForPrincipal(context.Background(), principal, 20))
	}
	job := AgentJob{
		JobID:       thread.ID,
		ArtifactID:  thread.Artifact.ID,
		ThreadID:    thread.ID,
		Mode:        thread.Mode,
		Objective:   firstNonEmptyString(strings.TrimSpace(meta["objective"]), thread.Query),
		Authority:   authority,
		Context:     AgentJobContext{Memory: memory},
		Origin:      agentJobOrigin(meta),
		RequestedBy: requestedBy,
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
// Default: the bounded OpenAI Responses worker. Anthropic aliases are retired
// and fail closed through the stub; the presence of an Anthropic credential
// must never alter provider selection.
func selectedAgentRunnerName() string {
	explicit := strings.ToLower(strings.TrimSpace(os.Getenv("BONFIRE_AGENT_RUNNER")))
	switch explicit {
	case "":
		return legacyWorkerRunnerName()
	case agentRunnerAnthropicFable, "anthropic", "fable":
		return agentRunnerStub
	case agentRunnerCodexSidecar, "codex", "codex_exec", "sidecar":
		if !codexExecutionEnabled() {
			return agentRunnerStub
		}
		return agentRunnerCodexSidecar
	case agentRunnerCodexLocal, "local", "local_exec":
		if !codexExecutionEnabled() {
			return agentRunnerStub
		}
		return agentRunnerCodexLocal
	case agentRunnerOpenAIText, "openai", "responses", "text":
		return agentRunnerOpenAIText
	case agentRunnerStub, "none":
		return agentRunnerStub
	default:
		return legacyWorkerRunnerName()
	}
}

// legacyWorkerRunnerName maps pre-existing worker envs without letting stale
// Codex settings reactivate the retired executor.
func legacyWorkerRunnerName() string {
	switch configuredAgentThreadWorkerMode() {
	case agentThreadWorkerCodexExec:
		if !codexExecutionEnabled() {
			return agentRunnerStub
		}
		if configuredCodexRunnerMode() == codexRunnerModeLocalExec {
			return agentRunnerCodexLocal
		}
		return agentRunnerCodexSidecar
	default:
		return agentRunnerOpenAIText
	}
}

// selectedExecutionRunnerName resolves the execution backend for can-shell /
// can-edit sub-jobs. Codex selections default to the unavailable stub until a
// new externally isolated adapter replaces the retired shared runner.
func selectedExecutionRunnerName() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BONFIRE_EXECUTION_RUNNER"))) {
	case agentRunnerCodexLocal, "local", "local_exec":
		if !codexExecutionEnabled() {
			return agentRunnerStub
		}
		return agentRunnerCodexLocal
	case agentRunnerAnthropicFable, "anthropic", "fable":
		return agentRunnerStub
	case "none":
		return agentRunnerStub
	case "", agentRunnerCodexSidecar, "codex", "sidecar":
		if !codexExecutionEnabled() {
			return agentRunnerStub
		}
		return agentRunnerCodexSidecar
	default:
		if !codexExecutionEnabled() {
			return agentRunnerStub
		}
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
	} else if normalizeAgentThreadMode(job.Mode) == "research" {
		// Research is an OpenAI execution seat. It must not inherit a deployment
		// default merely because a legacy credential is present. An explicit
		// retired persisted assignment above still fails closed.
		name = agentRunnerOpenAIText
	}
	name = admittedAgentRunnerName(name)
	if name == agentRunnerOpenAIText && !openAITextJobIsToolIndependent(job) {
		if app != nil && app.openAIToolRuntime != nil && app.openAIToolRuntime.Enabled && app.openAIToolRuntime.Carrier != nil && app.openAIToolRuntime.Carrier.Enabled {
			return &openAIToolProductRunner{app: app, runtime: app.openAIToolRuntime}
		}
		return &stubAgentRunner{}
	}
	switch name {
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

// openAITextJobIsToolIndependent names the exact work that can honestly use
// the current single-response writer without the retired Bonfire function
// loop. Workstream modes (research, design, grill, workflow) have their own
// hosted contracts. A process writer is admitted only when the durable goal
// stage carries a concrete output contract and all prior-stage inputs were
// assembled into its prompt. Ordinary chat work, free-form goals, and direct
// tools remain unavailable until the OpenAI function-tool loop reaches
// authority/capability parity.
func openAITextJobIsToolIndependent(job AgentJob) bool {
	switch normalizeAgentThreadMode(job.Mode) {
	case "research", "design", "grill", "workflow":
		return true
	}
	metadata := job.thread.Artifact.Metadata
	return strings.TrimSpace(metadata["goalParentId"]) != "" &&
		strings.TrimSpace(metadata["goalSubtaskId"]) != "" &&
		strings.EqualFold(strings.TrimSpace(metadata["goalDeliverable"]), "true") &&
		strings.TrimSpace(metadata["outputContract"]) != ""
}

// admittedAgentRunnerName is the final runner-selection choke point. Legacy
// env aliases and persisted assignedRunner metadata cannot bypass the E9
// compile-time closure even if an earlier resolver regresses.
func admittedAgentRunnerName(name string) string {
	if name == agentRunnerAnthropicFable {
		return agentRunnerStub
	}
	if (name == agentRunnerCodexSidecar || name == agentRunnerCodexLocal) && !codexExecutionEnabled() {
		return agentRunnerStub
	}
	return name
}

// resolveAssignedRunnerName validates a per-subtask runner override against the
// known runner names; an unknown/empty value returns "" so the env-selected
// default stands. Retired Anthropic assignments fail closed rather than
// executing or silently changing to a less-capable OpenAI path.
func resolveAssignedRunnerName(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case agentRunnerAnthropicFable, "anthropic", "fable":
		return agentRunnerStub
	case agentRunnerCodexSidecar:
		if !codexExecutionEnabled() {
			return agentRunnerStub
		}
		return agentRunnerCodexSidecar
	case agentRunnerCodexLocal:
		if !codexExecutionEnabled() {
			return agentRunnerStub
		}
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
