// usage_ledger.go — the append-only LLM usage + telemetry-event books (W0 items
// 1, 6, 7, 8 of docs/model-routing-master-plan-2026-07-11.md).
//
// Every LLM call in the tree records one llmUsageEntry here; every quality
// signal (board-op fidelity, router outcomes, gate-by-runner, parse failures,
// transcript segments, proposal lifecycle, workflow-run provenance) records a
// telemetryEvent. Both are daily-rotated JSONL files living NEXT TO the
// meeting-memory store (docker volume — never the stale /opt/meetingassist/data
// trap), exactly like the embeddings sidecar:
//
//	filepath.Dir(meetingMemoryPath())/usage/usage-YYYY-MM-DD.jsonl
//	filepath.Dir(meetingMemoryPath())/usage/eval-YYYY-MM-DD.jsonl
//
// Contract (frozen — five downstream instrumentation agents code against it):
//
//   - recordLLMUsage / recordEvalEvent / recordProposalEvent / recordWorkflowRun
//     NEVER fail the caller. A write error is logged once per boot, then only
//     counted (usageLedgerDroppedWrites); a disabled ledger is a silent no-op.
//   - USAGE_LEDGER_DISABLED=1 kills all recording (usage AND events).
//   - USAGE_LEDGER_PATH overrides the ledger DIRECTORY.
//   - recordLLMUsage fills TS (now, UTC) and Seat (seatUntagged) when unset, and
//     computes EstCostUSD from the token fields via models_pricing.go when the
//     caller left it zero — callers only fill what they parsed off the wire.
//   - Unknown model ids never error: PriceMissing=true is stamped on the entry
//     (the typo'd-env-flip tripwire the rollup alerts on).
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------------
// Seat vocabulary — the ~25-seat naming contract from the routing audit
// (docs/llm-routing-audit-2026-07-11.md call-site inventory). Every LLM call
// site tags exactly one of these; the W4 unified seat table and the rollup
// artifact group by them. Do NOT invent ad-hoc seat strings at call sites.
// ---------------------------------------------------------------------------

const (
	// Anthropic executive stack (agent_runner_anthropic.go, goal_engine.go).
	seatOrchestrator = "orchestrator" // Fable 5 multi-turn agent-thread loop
	seatDeliverable  = "deliverable"  // deliverable-flagged subtask turns (heavier budgets)
	seatReview       = "review"       // Opus 4.8 review/gate (callReviewModel)
	seatFallback     = "fallback"     // refusal/transport fallback replays (FallbackUsed=true)
	seatGoalEngine   = "goal_engine"  // /goal decompose/panel/report single-shot calls
	seatGoalReview   = "goal_review"  // /goal verify passes

	// Sonnet worker seats (product voice, human-visible).
	seatChat        = "chat"         // answerAssistantQueryWithModelAttachments
	seatRouter      = "router"       // routeScoutChatTurn propose-confirm classifier
	seatMemoryQA    = "memory_qa"    // ask-anything memory answers (typed)
	seatVoiceRecall = "voice_recall" // answerMemoryQuestionWithModel (spoken turn)
	seatFollowup    = "followup"     // agent_thread_followup rewrites
	seatAttachments = "attachments"  // attachments.go vision transcription
	seatNarrative   = "narrative"    // narrative_maintainer
	seatTaste       = "taste"        // taste_analyst distiller
	seatHouseStyle  = "house_style"  // house_style distiller

	// OpenAI ambient extraction fleet (inherits OPENAI_BRAIN_MODEL unless pinned).
	seatBrain           = "brain"             // brain_worker 5m tick
	seatBoard           = "board"             // board_worker 2m tick (Terra pin lane)
	seatSuggestion      = "suggestion"        // suggestion_agent 3m tick
	seatDecisionLedger  = "decision_ledger"   // decision_ledger extraction
	seatEntityLedger    = "entity_ledger"     // entity_ledger adjudication
	seatMeetingDigest   = "meeting_digest"    // meeting digest folds
	seatCompanyDigest   = "company_digest"    // company digest folds
	seatMissionIntel    = "mission_intel"     // mission_intelligence
	seatSlop            = "slop"              // slop_classifier
	seatRecallMapReduce = "recall_mapreduce"  // tiered-digest recall map/reduce
	seatAgentThreadText = "agent_thread_text" // legacy openai_text thread runner + keyless fallbacks

	// Realtime voice + transcription surfaces.
	seatVoiceRoom            = "voice_room"            // shared room Scout voice (server-metered)
	seatVoicePrivate         = "voice_private"         // private dashboard voice (browser beacon)
	seatTranscriptionLane    = "transcription_lane"    // authoritative transcript lane (per-segment)
	seatTranscriptionSession = "transcription_session" // voice-peer realtime transcription (fallback lane)

	// Non-text lanes.
	seatEmbeddings = "embeddings" // text-embedding-3-small maintainer
	seatImages     = "images"     // gpt-image-2 generations
	seatCodex      = "codex"      // codex sidecar execution jobs

	// Sentinel: a call that reached the wire without a seat tag. Visible gaps
	// beat invisible ones — the W0 seat-coverage checklist greps for this.
	seatUntagged = "untagged"
)

// allLLMSeats is the boot-validation + rollup iteration order. Keep it in sync
// with the constants above (usage_ledger_test.go asserts uniqueness and shape).
var allLLMSeats = []string{
	seatOrchestrator, seatDeliverable, seatReview, seatFallback,
	seatGoalEngine, seatGoalReview,
	seatChat, seatRouter, seatMemoryQA, seatVoiceRecall, seatFollowup,
	seatAttachments, seatNarrative, seatTaste, seatHouseStyle,
	seatBrain, seatBoard, seatSuggestion, seatDecisionLedger, seatEntityLedger,
	seatMeetingDigest, seatCompanyDigest, seatMissionIntel, seatSlop,
	seatRecallMapReduce, seatAgentThreadText,
	seatVoiceRoom, seatVoicePrivate, seatTranscriptionLane, seatTranscriptionSession,
	seatEmbeddings, seatImages, seatCodex,
	seatUntagged,
}

// Provider tags — the only two wire providers today (venice et al. register
// their own string when a client exists).
const (
	providerAnthropic = "anthropic"
	providerOpenAI    = "openai"
)

// ---------------------------------------------------------------------------
// Usage entries
// ---------------------------------------------------------------------------

// llmUsageEntry is one JSONL line of usage-YYYY-MM-DD.jsonl: exactly one wire
// call (or one billable unit on duration lanes, e.g. a transcription segment).
// Callers fill what they know; recordLLMUsage defaults the rest. Token counts
// are per-call absolutes, never deltas.
type llmUsageEntry struct {
	TS                   time.Time `json:"ts"`
	Provider             string    `json:"provider"`
	Model                string    `json:"model"`
	Seat                 string    `json:"seat"`
	RoomID               string    `json:"room_id,omitempty"`
	ThreadID             string    `json:"thread_id,omitempty"`
	GoalID               string    `json:"goal_id,omitempty"`
	Workflow             string    `json:"workflow,omitempty"`
	RequestedServiceTier string    `json:"requested_service_tier,omitempty"`
	ServiceTier          string    `json:"service_tier,omitempty"`

	InputTokens         int64 `json:"input_tokens,omitempty"`
	CachedInputTokens   int64 `json:"cached_input_tokens,omitempty"`   // cache READS (90% off tier)
	CacheCreationTokens int64 `json:"cache_creation_tokens,omitempty"` // cache WRITES (1.25x tier)
	OutputTokens        int64 `json:"output_tokens,omitempty"`

	// Realtime voice lanes split audio from text; duration lanes (STT) bill by
	// the minute via AudioSeconds. Zero on plain text calls.
	AudioInputTokens       int64   `json:"audio_input_tokens,omitempty"`
	CachedAudioInputTokens int64   `json:"cached_audio_input_tokens,omitempty"`
	AudioOutputTokens      int64   `json:"audio_output_tokens,omitempty"`
	AudioSeconds           float64 `json:"audio_seconds,omitempty"`

	DurationMS          int64   `json:"duration_ms,omitempty"`
	EstCostUSD          float64 `json:"est_cost_usd,omitempty"`
	PriceMissing        bool    `json:"price_missing,omitempty"` // model id had no price row — rollup alarms on ANY of these
	FallbackUsed        bool    `json:"fallback_used,omitempty"` // this call was a same-call fallback replay
	Estimated           bool    `json:"estimated,omitempty"`     // wall-clock estimate, not wire-reported usage
	Error               string  `json:"error,omitempty"`         // non-empty when the call failed (429/529 storms still cost latency)
	WireSuccess         bool    `json:"wire_success,omitempty"`
	AcceptedOutput      bool    `json:"accepted_output,omitempty"`
	OutputFailureReason string  `json:"output_failure_reason,omitempty"`
}

// recordLLMUsage appends one entry to today's usage file. It never fails the
// caller: disabled → no-op; write errors are logged once per boot then counted.
func recordLLMUsage(entry llmUsageEntry) {
	if !usageLedgerEnabled() {
		return
	}
	if entry.TS.IsZero() {
		entry.TS = usageLedgerNow().UTC()
	}
	if entry.Seat == "" {
		entry.Seat = seatUntagged
	}
	if entry.EstCostUSD == 0 {
		cost, priced := estimateCostUSDAt(entry.Model, entry.TS, llmTokenUsage{
			InputTokens:            entry.InputTokens,
			CachedInputTokens:      entry.CachedInputTokens,
			CacheCreationTokens:    entry.CacheCreationTokens,
			OutputTokens:           entry.OutputTokens,
			AudioInputTokens:       entry.AudioInputTokens,
			CachedAudioInputTokens: entry.CachedAudioInputTokens,
			AudioOutputTokens:      entry.AudioOutputTokens,
			AudioSeconds:           entry.AudioSeconds,
		})
		entry.EstCostUSD = cost
		if !priced {
			entry.PriceMissing = true
		}
	}
	appendLedgerLine(usageLedgerFilePrefix, entry.TS, entry)
}

// ---------------------------------------------------------------------------
// Telemetry events (eval funnel, proposal lifecycle, workflow provenance) —
// same JSONL discipline, separate eval-YYYY-MM-DD.jsonl file, same kill switch.
// ---------------------------------------------------------------------------

// Event type discriminator (telemetryEvent.Type).
const (
	telemetryTypeEval        = "eval"
	telemetryTypeProposal    = "proposal"
	telemetryTypeWorkflowRun = "workflow_run"
)

// Eval-event kinds (W0 item 6 funnel). Lane is the seat/surface the signal
// belongs to (a seat* constant or a reserved lane like "transcript").
const (
	evalKindBoardOpFidelity   = "board_op_fidelity"  // fields: op_count, error_count, error_classes
	evalKindRouterOutcome     = "router_outcome"     // fields: verdict, proposal_id, confirmed/dismissed
	evalKindRouterTruncation  = "router_truncation"  // fields: stop_reason (max_tokens => truncated)
	evalKindGateResult        = "gate_result"        // fields: runner, verdict, goal_id — gate-by-runner metric
	evalKindParseFailure      = "parse_failure"      // fields: seat, model — strict-JSON lane counter
	evalKindDigestStructure   = "digest_structure"   // fields: check, pass — deterministic structural checks
	evalKindTranscriptSegment = "transcript_segment" // fields: status (completed|failed), room_id, audio_seconds
	evalKindCorrectionHit     = "correction_hit"     // fields: term, room_id — canonicalizeDomainTerms regex hit
	evalKindNoVocabWarning    = "no_vocab_warning"   // authoritative STT lane running without vocabulary biasing
	evalKindDigestOutput      = "digest_output"      // fields: outcome, reason, attempt_hash — accepted/rejected/circuit_open
)

// Reserved eval lane for the W2 STT harness scores (transcript_wer,
// vocab_hit_rate, speaker_attribution_rate ride fields on this lane).
const evalLaneTranscript = "transcript"

// Proposal lifecycle kinds (W0 item 7 taxonomy).
const (
	proposalEventMinted   = "minted"   // proposal card created
	proposalEventResolved = "resolved" // confirmed or dismissed (fields: resolution)
	proposalEventLaunched = "launched" // confirm turned into a launch (fields: path)
	proposalEventTerminal = "terminal" // kickoff reached a terminal state (fields: outcome)
)

// Proposal source surfaces (fields["source"] on minted events).
const (
	proposalSourceBoardWorker        = "board_worker"
	proposalSourceSuggestionWorker   = "suggestion_worker"
	proposalSourceRoomVoice          = "room_voice"
	proposalSourcePrivateVoice       = "private_voice"
	proposalSourceChatRouter         = "chat_router"
	proposalSourceDeterministicGuard = "deterministic_guard"
	proposalSourceCommitmentsSweep   = "commitments_sweep"
)

// telemetryEvent is one JSONL line of eval-YYYY-MM-DD.jsonl. Fields carries
// kind-specific payload (all values must be JSON-marshalable); proposal events
// stamp proposal_id + transcript lineage (from_brain_id, through_transcript_id,
// transcript_created_at) into Fields so time-to-proposal is computable from
// events alone. Event metadata NEVER feeds Scout search context.
type telemetryEvent struct {
	TS     time.Time      `json:"ts"`
	Type   string         `json:"type"`
	Lane   string         `json:"lane,omitempty"`
	Kind   string         `json:"kind,omitempty"`
	Fields map[string]any `json:"fields,omitempty"`
}

// recordEvalEvent appends one eval-funnel event. lane is the seat/surface the
// signal belongs to (seat* constant or evalLaneTranscript), kind an evalKind*
// constant. Never fails the caller.
func recordEvalEvent(lane, kind string, fields map[string]any) {
	if !usageLedgerEnabled() {
		return
	}
	now := usageLedgerNow().UTC()
	appendLedgerLine(evalLedgerFilePrefix, now, telemetryEvent{
		TS: now, Type: telemetryTypeEval, Lane: lane, Kind: kind, Fields: fields,
	})
}

// recordProposalEvent appends one proposal-lifecycle event. kind is a
// proposalEvent* constant; proposalID joins mint→resolve→launch→terminal.
// Callers stamp source (proposalSource* constant) and transcript lineage
// (from_brain_id, through_transcript_id, transcript_created_at) into fields on
// minted events. Never fails the caller.
func recordProposalEvent(kind, proposalID string, fields map[string]any) {
	if !usageLedgerEnabled() {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	if proposalID != "" {
		fields["proposal_id"] = proposalID
	}
	now := usageLedgerNow().UTC()
	appendLedgerLine(evalLedgerFilePrefix, now, telemetryEvent{
		TS: now, Type: telemetryTypeProposal, Kind: kind, Fields: fields,
	})
}

// Workflow trigger surfaces (workflowRunEntry.TriggerSurface).
const (
	triggerSurfacePalette         = "palette"
	triggerSurfaceGoalDoor        = "goal_door"
	triggerSurfaceChatRouter      = "chat_router"
	triggerSurfaceChannel         = "channel"
	triggerSurfaceRoomVoice       = "room_voice"
	triggerSurfacePrivateVoice    = "private_voice"
	triggerSurfaceSuggestionAgent = "suggestion_agent"
	triggerSurfaceScheduler       = "scheduler"
)

// Workflow outcomes (workflowRunEntry.Outcome). Record one entry at launch
// (workflowOutcomeLaunched) and one at the terminal state; join on ThreadID.
const (
	workflowOutcomeLaunched       = "launched"
	workflowOutcomeCompleted      = "completed"
	workflowOutcomeFailed         = "failed"
	workflowOutcomeNeedsAttention = "needs_attention"
	workflowOutcomeCancelled      = "cancelled"
)

// workflowRunEntry is the W0 item-8 provenance record: who kicked what from
// where, under which approval lane, on which runner seats, and how it ended.
type workflowRunEntry struct {
	WorkflowID      string   `json:"workflow_id"`
	WorkflowVersion string   `json:"workflow_version,omitempty"`
	TriggerSurface  string   `json:"trigger_surface"` // triggerSurface* constant
	Proposer        string   `json:"proposer,omitempty"`
	Approver        string   `json:"approver,omitempty"`
	Lane            string   `json:"lane,omitempty"` // auto | standard | heavy
	Seats           []string `json:"seats,omitempty"`
	Outcome         string   `json:"outcome"` // workflowOutcome* constant
	ProposalID      string   `json:"proposal_id,omitempty"`
	ThreadID        string   `json:"thread_id,omitempty"`
	GoalID          string   `json:"goal_id,omitempty"`
	RoomID          string   `json:"room_id,omitempty"`
	DurationMS      int64    `json:"duration_ms,omitempty"`
}

// recordWorkflowRun appends one workflow-run provenance event. Never fails the
// caller.
func recordWorkflowRun(entry workflowRunEntry) {
	if !usageLedgerEnabled() {
		return
	}
	now := usageLedgerNow().UTC()
	appendLedgerLine(evalLedgerFilePrefix, now, telemetryEvent{
		TS: now, Type: telemetryTypeWorkflowRun, Kind: entry.Outcome,
		Fields: map[string]any{"run": entry},
	})
}

// ---------------------------------------------------------------------------
// Plumbing: paths, rotation, append, failure accounting
// ---------------------------------------------------------------------------

const (
	usageLedgerFilePrefix = "usage" // usage-YYYY-MM-DD.jsonl
	evalLedgerFilePrefix  = "eval"  // eval-YYYY-MM-DD.jsonl
)

// usageLedgerNow is swappable in tests to exercise daily rotation.
var usageLedgerNow = time.Now

// usageLedgerEnabled: USAGE_LEDGER_DISABLED kills usage AND event recording.
func usageLedgerEnabled() bool {
	return !boolEnv("USAGE_LEDGER_DISABLED")
}

// usageLedgerDir is the ledger directory: USAGE_LEDGER_PATH override, else a
// usage/ subdir beside the meeting-memory store (docker volume in prod),
// derived exactly like embeddingsPath — never the same file as any store.
func usageLedgerDir() string {
	if path := strings.TrimSpace(os.Getenv("USAGE_LEDGER_PATH")); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "usage")
}

var (
	usageLedgerMu        sync.Mutex
	usageLedgerWarnOnce  sync.Once
	usageLedgerDropCount atomic.Int64
)

// usageLedgerDroppedWrites reports how many entries/events were dropped on
// write failure since boot — the rollup surfaces it so silent loss is visible.
func usageLedgerDroppedWrites() int64 {
	return usageLedgerDropCount.Load()
}

// appendLedgerLine marshals payload and appends it (one line) to
// <dir>/<prefix>-YYYY-MM-DD.jsonl under the ledger mutex. O_APPEND +
// open-per-write keeps rotation trivial and midnight-safe; volumes here are a
// few lines a minute, so the open cost is noise. All failure modes are
// swallowed: warn once per boot, count forever.
func appendLedgerLine(prefix string, ts time.Time, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		ledgerWriteFailed(err)
		return
	}
	data = append(data, '\n')
	path := filepath.Join(usageLedgerDir(), prefix+"-"+ts.UTC().Format("2006-01-02")+".jsonl")

	usageLedgerMu.Lock()
	defer usageLedgerMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		ledgerWriteFailed(err)
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		ledgerWriteFailed(err)
		return
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		ledgerWriteFailed(writeErr)
		return
	}
	if closeErr != nil {
		ledgerWriteFailed(closeErr)
	}
}

// ledgerWriteFailed implements the never-fail-the-caller discipline: the first
// failure per boot logs loudly, every failure increments the dropped counter.
func ledgerWriteFailed(err error) {
	usageLedgerDropCount.Add(1)
	usageLedgerWarnOnce.Do(func() {
		log.Errorf("usage ledger: write failed (logged once per boot, further failures only counted): %v", err)
	})
}
