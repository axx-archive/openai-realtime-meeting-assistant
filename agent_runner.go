package main

// Generic runner for ambient agents: scheduled workers that consume a window
// of meeting-memory entries of one kind and append a durable artifact entry of
// another kind.
//
// Registering a new ambient agent (research, strategy, design, ...):
//  1. Build an ambientAgentConfig: a unique name, the memory kind it consumes,
//     the artifact kind it appends, and the artifact metadata key that records
//     the last consumed input id (the durable cursor).
//  2. Write a produce func that turns the supplied input batch into one
//     appended artifact entry; stamp the cursor metadata key with the last
//     input's id so the next pass resumes after it.
//  3. Call app.startAmbientAgent(yourAgent(), apiKey) from JoinConferenceRoom.
//
// The runner owns the ticker lifecycle, env-based disable/interval/backfill
// overrides, min/max batch sizing, the startup baseline cursor, and shutdown.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// meetingArchiveFlushTimeout is the OVERALL ceiling on the close-time flush
// chain — the nine sequential passes of closeFlushChain (brain → decision
// ledger → board → mission intel → narrative → meeting digest → day digest →
// entity ledger → company digest). A8: each pass ALSO gets its own
// meetingArchiveFlushPassTimeout so one slow upstream pass can no longer starve
// the mission / narrative / digest passes behind it out of a single shared
// budget (the old single 3-minute deadline shared across every call). Both are
// ceilings, not targets — agents with nothing unconsumed skip without a model
// call, and an expired context only fails the remaining passes (their tickers
// retry later); the caller always proceeds afterwards (archive snapshot / idle
// rotation), so liveness never depends on the model.
const (
	meetingArchiveFlushTimeout     = 7 * time.Minute
	meetingArchiveFlushPassTimeout = 90 * time.Second
)

// A3 nudge cadence + A8 failure-backoff tuning for the ambient runner loop.
const (
	// defaultAmbientNudgeMaxAge is A3's staleness floor: once the OLDEST
	// unconsumed input has waited this long a nudge fires a short pass even below
	// minBatch, so a lone short exchange is not left dark until the next
	// safety-floor tick. Per-agent nudgeMaxAge overrides it.
	defaultAmbientNudgeMaxAge = 90 * time.Second
	// ambientNudgeShortBatch is the batch floor a staleness / cascade nudge fires
	// with when a full minBatch has not accumulated — one input is enough to keep
	// a short exchange from going dark.
	ambientNudgeShortBatch = 1
	// ambientAgentMaxWindowAttempts is how many consecutive failures on the SAME
	// window (keyed by its oldest-input id) the runner tolerates before it
	// dead-letters that head input — advancing the agent's baseline past it — so
	// a permanently-poison entry can never wedge the cursor and re-send forever.
	ambientAgentMaxWindowAttempts = 4
	// ambientProviderMaxWindowAttempts bounds infrastructure/provider retries
	// without consuming or dead-lettering the source window. At the cap the
	// per-agent circuit remains open until the worker is explicitly restarted;
	// a periodic ticker may never turn a provider outage into an endless bill.
	ambientProviderMaxWindowAttempts = 4
	// ambientAgentBackoffBase / Cap bound the exponential backoff between retries
	// of a failing window so a hard-down model does not hot-retry every tick.
	ambientAgentBackoffBase = 30 * time.Second
	ambientAgentBackoffCap  = 10 * time.Minute
)

// ambientAgentFailure tracks A8 same-window retry state for one agent: the
// oldest-input id of the window that keeps failing (stable across retries since
// the runner halves the batch from the newer end), how many attempts it has
// cost, and when the next retry may fire. Lives on kanbanBoardApp.agentFailures
// under app.mu; only the agent's single loop goroutine mutates its own record.
type ambientAgentFailure struct {
	windowID        string
	attempts        int
	backoffUntil    time.Time
	providerOpen    bool
	persistenceOpen bool
	continuityOpen  bool
}

// ambientAgentHoldError is a recoverable infrastructure/configuration outage.
// It participates in bounded backoff but is categorically forbidden from
// dead-lettering input or advancing a baseline.
type ambientAgentHoldError struct{ err error }

func (failure *ambientAgentHoldError) Error() string { return failure.err.Error() }
func (failure *ambientAgentHoldError) Unwrap() error { return failure.err }

func isAmbientAgentHoldError(err error) bool {
	var failure *ambientAgentHoldError
	return errors.As(err, &failure)
}

type ambientOutputRejection struct {
	agent  string
	reason string
}

func (failure *ambientOutputRejection) Error() string {
	return fmt.Sprintf("%s output rejected: %s", failure.agent, firstNonEmptyString(failure.reason, "invalid output"))
}

func (failure *ambientOutputRejection) providerOutputRejection() {}

// ambientHeldWindow is the durable recovery hint for a provider-held source
// cursor. Raw input and output stay in their existing stores; this sidecar
// records only the pre-window baseline and head needed to prevent a restart
// from re-baselining past held work.
type ambientHeldWindow struct {
	Agent             string `json:"agent"`
	RoomID            string `json:"roomId"`
	InputKind         string `json:"inputKind,omitempty"`
	ArtifactKind      string `json:"artifactKind,omitempty"`
	CursorMetadataKey string `json:"cursorMetadataKey,omitempty"`
	WindowID          string `json:"windowId"`
	BaselineID        string `json:"baselineId,omitempty"`
	BlockedReason     string `json:"blockedReason,omitempty"`
}

type ambientHeldWindowState struct {
	Version int                          `json:"version"`
	Windows map[string]ambientHeldWindow `json:"windows"`
}

var ambientHeldWindowStateMu sync.Mutex

const ambientContinuityAmbiguous = "durable_cursor_ambiguous"

const (
	ambientContinuityCheckpointInvalid = "durable_checkpoint_invalid"
	ambientContinuityHeldWindowInvalid = "durable_held_window_invalid"
	ambientContinuityContractMismatch  = "durable_checkpoint_contract_mismatch"
)

// ambientHeldWindowStatePersist is an injectable durability seam. Production
// always uses the fsync'd atomic writer; tests replace it to prove that write,
// fsync, and post-rename ambiguity all fail closed before provider admission.
var ambientHeldWindowStatePersist = persistAmbientHeldWindowState

func (app *kanbanBoardApp) ambientHeldWindowPath() string {
	if app == nil || app.memory == nil || strings.TrimSpace(app.memory.path) == "" {
		return ""
	}
	return app.memory.path + ".ambient-holds.json"
}

func loadAmbientHeldWindowState(path string) (ambientHeldWindowState, error) {
	state := ambientHeldWindowState{Version: 1, Windows: map[string]ambientHeldWindow{}}
	if strings.TrimSpace(path) == "" {
		return state, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return ambientHeldWindowState{}, err
	}
	if state.Version != 1 {
		return ambientHeldWindowState{}, fmt.Errorf("unsupported ambient checkpoint version %d", state.Version)
	}
	if state.Windows == nil {
		state.Windows = map[string]ambientHeldWindow{}
	}
	return state, nil
}

func persistAmbientHeldWindowState(path string, state ambientHeldWindowState) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if state.Version != 1 {
		return fmt.Errorf("refusing unsupported ambient checkpoint version %d", state.Version)
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomicallyDurable(path, append(raw, '\n'), 0o600)
}

func (app *kanbanBoardApp) ambientHeldWindow(key string) (ambientHeldWindow, bool, error) {
	window, ok, err := app.ambientScopeCheckpoint(key)
	if err != nil || !ok || strings.TrimSpace(window.WindowID) == "" {
		return ambientHeldWindow{}, false, err
	}
	return window, true, nil
}

func (app *kanbanBoardApp) ambientScopeCheckpoint(key string) (ambientHeldWindow, bool, error) {
	ambientHeldWindowStateMu.Lock()
	defer ambientHeldWindowStateMu.Unlock()
	state, err := loadAmbientHeldWindowState(app.ambientHeldWindowPath())
	if err != nil {
		return ambientHeldWindow{}, false, err
	}
	window, ok := state.Windows[key]
	return window, ok, nil
}

func (app *kanbanBoardApp) persistAmbientHeldWindow(agent ambientAgentConfig, headID, roomID string) error {
	roomID = agent.scopeRoomID(roomID)
	key := ambientAgentScopeKey(agent, roomID)
	window := ambientHeldWindow{
		Agent:             agent.name,
		RoomID:            roomID,
		InputKind:         agent.inputKind,
		ArtifactKind:      agent.artifactKind,
		CursorMetadataKey: agent.cursorMetadataKey,
		WindowID:          strings.TrimSpace(headID),
		BaselineID:        app.ambientAgentBaselineID(key),
	}
	ambientHeldWindowStateMu.Lock()
	defer ambientHeldWindowStateMu.Unlock()
	path := app.ambientHeldWindowPath()
	state, err := loadAmbientHeldWindowState(path)
	if err != nil {
		return err
	}
	state.Windows[key] = window
	return ambientHeldWindowStatePersist(path, state)
}

// ensureAmbientScopeCheckpoint durably anchors the scope's boot baseline even
// while no provider window is held. A later pre-invocation checkpoint failure
// can therefore never make a restart infer a newer baseline and skip the raw
// input that was waiting when durability failed.
func (app *kanbanBoardApp) ensureAmbientScopeCheckpoint(agent ambientAgentConfig, roomID, baselineID string, blockedReason ...string) (ambientHeldWindow, error) {
	roomID = agent.scopeRoomID(roomID)
	key := ambientAgentScopeKey(agent, roomID)
	ambientHeldWindowStateMu.Lock()
	defer ambientHeldWindowStateMu.Unlock()
	path := app.ambientHeldWindowPath()
	state, err := loadAmbientHeldWindowState(path)
	if err != nil {
		return ambientHeldWindow{}, err
	}
	checkpoint := state.Windows[key]
	reason := ""
	if len(blockedReason) > 0 {
		reason = strings.TrimSpace(blockedReason[0])
	}
	if strings.TrimSpace(checkpoint.WindowID) == "" {
		checkpoint.BaselineID = strings.TrimSpace(baselineID)
		checkpoint.BlockedReason = reason
	}
	checkpoint.Agent = agent.name
	checkpoint.RoomID = roomID
	checkpoint.InputKind = agent.inputKind
	checkpoint.ArtifactKind = agent.artifactKind
	checkpoint.CursorMetadataKey = agent.cursorMetadataKey
	state.Windows[key] = checkpoint
	// Rewrite even an existing checkpoint. Besides refreshing the 0600 atomic
	// envelope, this is the recovery probe that must succeed before an explicit
	// worker restart may clear a prior persistence-open circuit.
	if err := ambientHeldWindowStatePersist(path, state); err != nil {
		return checkpoint, err
	}
	return checkpoint, nil
}

func (app *kanbanBoardApp) clearAmbientHeldWindow(key string) error {
	ambientHeldWindowStateMu.Lock()
	defer ambientHeldWindowStateMu.Unlock()
	path := app.ambientHeldWindowPath()
	state, err := loadAmbientHeldWindowState(path)
	if err != nil {
		return err
	}
	window, ok := state.Windows[key]
	if !ok || strings.TrimSpace(window.WindowID) == "" {
		return nil
	}
	window.WindowID = ""
	state.Windows[key] = window
	return ambientHeldWindowStatePersist(path, state)
}

func (app *kanbanBoardApp) persistAmbientCheckpointBaseline(agent ambientAgentConfig, baselineID, roomID string) error {
	roomID = agent.scopeRoomID(roomID)
	key := ambientAgentScopeKey(agent, roomID)
	ambientHeldWindowStateMu.Lock()
	defer ambientHeldWindowStateMu.Unlock()
	path := app.ambientHeldWindowPath()
	state, err := loadAmbientHeldWindowState(path)
	if err != nil {
		return err
	}
	state.Windows[key] = ambientHeldWindow{
		Agent:             agent.name,
		RoomID:            roomID,
		InputKind:         agent.inputKind,
		ArtifactKind:      agent.artifactKind,
		CursorMetadataKey: agent.cursorMetadataKey,
		BaselineID:        strings.TrimSpace(baselineID),
	}
	return ambientHeldWindowStatePersist(path, state)
}

func (app *kanbanBoardApp) bootstrapAmbientContinuity(agent ambientAgentConfig, roomID string) (baselineID, blockedReason string, err error) {
	key := ambientAgentScopeKey(agent, roomID)
	checkpoint, ok, err := app.ambientScopeCheckpoint(key)
	if err != nil {
		return "", "", err
	}
	if ok {
		normalized, changed := app.validateAndNormalizeAmbientCheckpoint(agent, roomID, checkpoint)
		if changed {
			ambientHeldWindowStateMu.Lock()
			path := app.ambientHeldWindowPath()
			state, loadErr := loadAmbientHeldWindowState(path)
			if loadErr == nil {
				state.Windows[key] = normalized
				loadErr = ambientHeldWindowStatePersist(path, state)
			}
			ambientHeldWindowStateMu.Unlock()
			if loadErr != nil {
				return normalized.BaselineID, normalized.BlockedReason, loadErr
			}
			checkpoint = normalized
		}
		return checkpoint.BaselineID, checkpoint.BlockedReason, nil
	}
	if boolEnv(agent.backfillEnv) {
		return "", "", nil
	}
	if baseline, admitted := app.meetingAnalysisCurrentMeetingBootstrapBaseline(agent, roomID); admitted {
		return baseline, "", nil
	}
	baselineID, _, ambiguous := app.memory.ambientContinuityBaseline(agent, roomID)
	if ambiguous {
		return "", ambientContinuityAmbiguous, nil
	}
	return baselineID, "", nil
}

// validateAndNormalizeAmbientCheckpoint upgrades legacy untyped checkpoints and
// rejects cursor references that do not belong to this worker's input stream and
// scope. A bad held head is never discarded or skipped: it remains visible in
// the sidecar and opens a continuity circuit until an operator repairs it.
func (app *kanbanBoardApp) validateAndNormalizeAmbientCheckpoint(agent ambientAgentConfig, roomID string, checkpoint ambientHeldWindow) (ambientHeldWindow, bool) {
	roomID = agent.scopeRoomID(roomID)
	normalized := checkpoint
	contractMismatch := (strings.TrimSpace(checkpoint.Agent) != "" && checkpoint.Agent != agent.name) ||
		(strings.TrimSpace(checkpoint.RoomID) != "" && normalizeRoomID(checkpoint.RoomID) != roomID) ||
		(strings.TrimSpace(checkpoint.InputKind) != "" && checkpoint.InputKind != agent.inputKind) ||
		(strings.TrimSpace(checkpoint.ArtifactKind) != "" && checkpoint.ArtifactKind != agent.artifactKind) ||
		(strings.TrimSpace(checkpoint.CursorMetadataKey) != "" && checkpoint.CursorMetadataKey != agent.cursorMetadataKey)
	normalized.Agent = agent.name
	normalized.RoomID = roomID
	normalized.InputKind = agent.inputKind
	normalized.ArtifactKind = agent.artifactKind
	normalized.CursorMetadataKey = agent.cursorMetadataKey
	changed := normalized != checkpoint

	baseline, baselineOK, windowOK := app.memory.normalizeAmbientCheckpointReferences(agent, roomID, checkpoint.BaselineID, checkpoint.WindowID)
	if contractMismatch || strings.TrimSpace(checkpoint.BlockedReason) != "" {
		baselineOK = false
	}
	if !windowOK {
		normalized.BlockedReason = ambientContinuityHeldWindowInvalid
		return normalized, true
	}
	if baselineOK {
		if normalized.BaselineID != baseline || normalized.BlockedReason != "" {
			normalized.BaselineID = baseline
			normalized.BlockedReason = ""
			changed = true
		}
		return normalized, changed
	}

	// A bad sidecar can be repaired only from the newest durable artifact
	// cursor. A held sidecar additionally has to prove that cursor remains
	// strictly before the validated held input.
	recovered, _, ambiguous := app.memory.ambientContinuityBaseline(agent, roomID)
	if ambiguous {
		normalized.BlockedReason = ambientContinuityAmbiguous
		return normalized, true
	}
	_, recoveredOK, recoveredWindowOK := app.memory.normalizeAmbientCheckpointReferences(agent, roomID, recovered, checkpoint.WindowID)
	if !recoveredOK || !recoveredWindowOK {
		if contractMismatch {
			normalized.BlockedReason = ambientContinuityContractMismatch
		} else {
			normalized.BlockedReason = ambientContinuityCheckpointInvalid
		}
		return normalized, true
	}
	normalized.BaselineID = recovered
	normalized.BlockedReason = ""
	return normalized, true
}

// ambientAgentCircuitOpenError is safe to return to on-demand callers. It
// carries retry timing without provider payloads and distinguishes a cooling
// circuit from one requiring an explicit same-worker restart.
type ambientAgentCircuitOpenError struct {
	Agent           string
	RoomID          string
	RetryAt         time.Time
	RestartRequired bool
}

func (failure *ambientAgentCircuitOpenError) Error() string {
	if failure.RestartRequired {
		return fmt.Sprintf("%s is paused after repeated failures; retry after the worker is restarted", failure.Agent)
	}
	if !failure.RetryAt.IsZero() {
		return fmt.Sprintf("%s is cooling down until %s", failure.Agent, failure.RetryAt.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("%s is temporarily unavailable", failure.Agent)
}

func (failure *ambientAgentCircuitOpenError) retryAfterSeconds(now time.Time) int {
	if failure == nil || failure.RestartRequired || failure.RetryAt.IsZero() {
		return 0
	}
	remaining := time.Until(failure.RetryAt)
	if !now.IsZero() {
		remaining = failure.RetryAt.Sub(now)
	}
	if remaining <= 0 {
		return 1
	}
	return max(1, int(math.Ceil(remaining.Seconds())))
}

type ambientAgentConfig struct {
	name            string
	defaultInterval time.Duration
	intervalEnv     string // duration override; "0"/"off"/"false"/"disabled" turns the agent off
	disabledEnv     string // truthy disables the agent
	backfillEnv     string // truthy consumes history from the start at boot
	minBatchEnv     string
	defaultMinBatch int
	maxBatchEnv     string
	defaultMaxBatch int
	inputKind       string // memory kind the agent consumes
	artifactKind    string // memory kind the agent appends
	// healthSuccessAt optionally identifies the durable artifact contract that
	// proves this worker completed useful work. It is separate from artifactKind
	// for specialty workers that update a typed os_artifact in place rather than
	// append a dedicated memory kind.
	healthSuccessAt func(meetingMemoryEntry) (time.Time, bool)
	// healthWorkDue reports the specialty worker's actual cadence gate. It keeps
	// an intentionally old but not-yet-due living artifact healthy while making
	// genuinely pending work explicit and degraded. Generic ambient workers use
	// their normal interval freshness instead.
	healthWorkDue     func(*kanbanBoardApp, time.Time) bool
	cursorMetadataKey string // artifact metadata key holding the consumed-through input id
	requestTimeout    time.Duration
	// nudgeMaxAge overrides defaultAmbientNudgeMaxAge for this agent's A3 nudge
	// staleness floor; zero uses the default.
	nudgeMaxAge time.Duration
	// syntheticHeldWindow is reserved for specialty batches that can combine
	// multiple input kinds and therefore have no single input entry as their
	// provider-window key. Generic ambient workers must keep this false so their
	// held WindowID remains a typed, scope-ordered input cursor.
	syntheticHeldWindow bool
	// roomScoped partitions the agent's bookkeeping by room (multi-room W4
	// §7.4): the cursor for (agent, room) is the newest artifact-of-kind
	// stamped with that roomId (legacy artifacts without roomId are the OFFICE
	// cursors), inputs are filtered by roomId, and baselines / nudges /
	// failure-backoff / run locks key on (agent, room) — one room's pass can
	// never advance another room's window. The goroutine stays a singleton;
	// each tick iterates the rooms with unconsumed input. False keeps the
	// company-global single-cursor behavior (day digest, entity ledger,
	// company digest).
	roomScoped bool
	// defersWhenGuestsOnly (§6.5) holds the agent's scheduled/nudge passes for
	// a room whose live seats are guests only — an unattended guest cannot
	// drive summarization spend. Nudges accumulate (the ticker floor retries)
	// and the close-flush chain still runs its one bounded pass.
	defersWhenGuestsOnly bool
	produce              func(app *kanbanBoardApp, ctx context.Context, apiKey string, inputs []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error)
}

// windowRoomID resolves the room an agent pass runs for into the memory
// store's filter dimension: room-scoped agents filter by the (normalized)
// room, company-global agents scan every room ("" disables the filter —
// exactly the pre-room behavior).
func (agent ambientAgentConfig) windowRoomID(roomID string) string {
	if agent.roomScoped {
		return normalizeRoomID(roomID)
	}
	return ""
}

// scopeRoomID is the authority/bookkeeping scope, not necessarily the room
// that triggered a pass. Room-scoped workers own one circuit/cursor/lock per
// room; company-global workers own exactly one shared scope even when two
// named rooms close concurrently.
func (agent ambientAgentConfig) scopeRoomID(roomID string) string {
	if agent.roomScoped {
		return normalizeRoomID(roomID)
	}
	return officeRoomID
}

func ambientAgentScopeKey(agent ambientAgentConfig, roomID string) string {
	return ambientAgentKey(agent.name, agent.scopeRoomID(roomID))
}

// ambientContinuityBaseline migrates a pre-sidecar worker from durable truth.
// A consumed-through artifact is authoritative; no input at all is a provably
// clean install. Existing raw input without such evidence is ambiguous and
// must not be silently treated as already consumed.
func (store *meetingMemoryStore) ambientContinuityBaseline(agent ambientAgentConfig, roomID string) (baselineID string, clean bool, ambiguous bool) {
	if store == nil {
		return "", true, false
	}
	windowRoomID := agent.windowRoomID(roomID)
	matchesRoom := func(entry meetingMemoryEntry) bool {
		return windowRoomID == "" || normalizeRoomID(entry.Metadata["roomId"]) == windowRoomID
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	hasInput := false
	for _, entry := range store.entries {
		if entry.Kind == agent.inputKind && !memoryEntryHiddenFromRecall(entry) && matchesRoom(entry) {
			hasInput = true
		}
	}
	for index := len(store.entries) - 1; index >= 0; index-- {
		artifact := store.entries[index]
		if artifact.Kind != agent.artifactKind || memoryEntryHiddenFromRecall(artifact) || !matchesRoom(artifact) {
			continue
		}
		cursorID := strings.TrimSpace(artifact.Metadata[agent.cursorMetadataKey])
		if cursorID == "" {
			// Normalize a cursorless legacy artifact to the newest matching input
			// at or before that artifact. Persisting the artifact id itself keeps
			// the sidecar untyped and can skip unrelated raw inputs after a config
			// change.
			for inputIndex := index; inputIndex >= 0; inputIndex-- {
				input := store.entries[inputIndex]
				if input.Kind == agent.inputKind && !memoryEntryHiddenFromRecall(input) && matchesRoom(input) {
					return input.ID, false, false
				}
			}
			if !hasInput {
				return "", true, false
			}
			return "", false, true
		}
		for inputIndex := index; inputIndex >= 0; inputIndex-- {
			input := store.entries[inputIndex]
			if input.ID == cursorID && input.Kind == agent.inputKind && !memoryEntryHiddenFromRecall(input) && matchesRoom(input) {
				return cursorID, false, false
			}
		}
		return "", false, true
	}
	if hasInput {
		// Inputs appended after this store instance booted are provably new work,
		// not legacy history. A clean first install may therefore anchor at the
		// empty cursor and consume them even if the worker starts lazily.
		preBootInputID := store.bootLatestIDs[agent.inputKind]
		if windowRoomID != "" {
			preBootInputID = ""
			if rooms := store.bootLatestRoomIDs[agent.inputKind]; rooms != nil {
				preBootInputID = rooms[normalizeRoomID(windowRoomID)]
			}
		}
		if strings.TrimSpace(preBootInputID) == "" {
			return "", true, false
		}
		return "", false, true
	}
	return "", true, false
}

// normalizeAmbientCheckpointReferences resolves a baseline to this worker's
// input stream. Legacy artifact references are accepted only as migration input
// and normalized to a proven input cursor. The held head must be a later input
// in the same effective room.
func (store *meetingMemoryStore) normalizeAmbientCheckpointReferences(agent ambientAgentConfig, roomID, baselineID, windowID string) (normalizedBaseline string, baselineOK, windowOK bool) {
	if store == nil {
		return "", strings.TrimSpace(baselineID) == "", strings.TrimSpace(windowID) == ""
	}
	windowRoomID := agent.windowRoomID(roomID)
	matchesRoom := func(entry meetingMemoryEntry) bool {
		return windowRoomID == "" || normalizeRoomID(entry.Metadata["roomId"]) == normalizeRoomID(windowRoomID)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	find := func(id string) (meetingMemoryEntry, int, bool) {
		id = strings.TrimSpace(id)
		for index := len(store.entries) - 1; index >= 0; index-- {
			entry := store.entries[index]
			if entry.ID == id && !memoryEntryHiddenFromRecall(entry) {
				return entry, index, true
			}
		}
		return meetingMemoryEntry{}, -1, false
	}

	baselineID = strings.TrimSpace(baselineID)
	baselineIndex := -1
	baselineOK = baselineID == ""
	if baselineID != "" {
		entry, index, ok := find(baselineID)
		if ok && matchesRoom(entry) {
			switch entry.Kind {
			case agent.inputKind:
				normalizedBaseline, baselineIndex, baselineOK = entry.ID, index, true
			case agent.artifactKind:
				cursorID := strings.TrimSpace(entry.Metadata[agent.cursorMetadataKey])
				if cursorID != "" {
					cursor, cursorIndex, cursorOK := find(cursorID)
					if cursorOK && cursor.Kind == agent.inputKind && matchesRoom(cursor) && cursorIndex <= index {
						normalizedBaseline, baselineIndex, baselineOK = cursor.ID, cursorIndex, true
					}
				} else {
					for cursorIndex := index; cursorIndex >= 0; cursorIndex-- {
						cursor := store.entries[cursorIndex]
						if cursor.Kind == agent.inputKind && !memoryEntryHiddenFromRecall(cursor) && matchesRoom(cursor) {
							normalizedBaseline, baselineIndex, baselineOK = cursor.ID, cursorIndex, true
							break
						}
					}
				}
			}
		}
	}

	windowID = strings.TrimSpace(windowID)
	windowOK = windowID == ""
	if windowID != "" {
		if agent.syntheticHeldWindow {
			return normalizedBaseline, baselineOK, true
		}
		window, windowIndex, ok := find(windowID)
		windowOK = ok && window.Kind == agent.inputKind && matchesRoom(window) && (!baselineOK || baselineIndex < 0 || windowIndex > baselineIndex)
	}
	return normalizedBaseline, baselineOK, windowOK
}

// ambientAgentKey is the map key for one agent's per-room bookkeeping
// (baselines, run locks, failures). The office key is the bare agent name so
// every pre-room cursor, test seam, and boot registration keeps working
// unchanged; only named rooms extend the key.
func ambientAgentKey(name string, roomID string) string {
	roomID = normalizeRoomID(roomID)
	if roomID == officeRoomID {
		return name
	}
	return name + "@" + roomID
}

// ambientWindowRoomID derives the room a produce pass is running for from its
// (room-filtered) input window — absent roomId metadata reads as office, so
// legacy windows keep their office semantics.
func ambientWindowRoomID(inputs []meetingMemoryEntry) string {
	if len(inputs) == 0 {
		return officeRoomID
	}
	return normalizeRoomID(inputs[0].Metadata["roomId"])
}

func (agent ambientAgentConfig) interval() time.Duration {
	raw := strings.TrimSpace(os.Getenv(agent.intervalEnv))
	if raw == "" {
		return agent.defaultInterval
	}
	switch strings.ToLower(raw) {
	case "0", "off", "false", "disabled":
		return 0
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval < time.Second {
		return agent.defaultInterval
	}

	return interval
}

func (agent ambientAgentConfig) minBatch() int {
	return positiveIntEnv(agent.minBatchEnv, agent.defaultMinBatch)
}

func (agent ambientAgentConfig) maxBatch() int {
	return positiveIntEnv(agent.maxBatchEnv, agent.defaultMaxBatch)
}

// nudgeAge is A3's per-agent staleness floor, falling back to the shared
// default. It is how long the oldest unconsumed input may wait before a nudge
// fires a short pass rather than holding out for a full minBatch.
func (agent ambientAgentConfig) nudgeAge() time.Duration {
	if agent.nudgeMaxAge > 0 {
		return agent.nudgeMaxAge
	}
	return defaultAmbientNudgeMaxAge
}

func (app *kanbanBoardApp) startAmbientAgent(agent ambientAgentConfig, apiKey string) {
	if app == nil || app.memory == nil || strings.TrimSpace(apiKey) == "" || boolEnv(agent.disabledEnv) {
		return
	}
	// Registration seam for the taste analyst (taste_analyst.go): the analyst
	// is per-user and rides the founder-approved OpenAI key already admitted by
	// this generic ambient loop. A legacy Anthropic credential is irrelevant.
	if agent.name != tasteAnalystAgentName {
		app.ensureTasteAnalystStarted(apiKey)
		// The House-Style Distiller (house_style.go) rides the same OpenAI seam:
		// the seventh instance, per-office, using the already-admitted key.
		app.ensureHouseStyleDistillerStarted(apiKey)
		// The Embedding Maintainer (embeddings.go, study §6 item 2.4) rides the
		// same seam: it is OpenAI-keyed like this loop, so it registers with the
		// key this seam already proved non-empty above. It builds the in-process
		// semantic index the retrieval lane fuses with; keyless deploys never
		// reach here, so the index stays nil and recall degrades to lexical-only.
		app.ensureEmbeddingMaintainerStarted(apiKey)
	}
	interval := agent.interval()
	if interval <= 0 {
		return
	}
	// Serialize explicit starts/restarts for this worker. The circuit reset below
	// is legal only after the old supervisor has acknowledged exit.
	supervisorLock := app.ambientAgentRunLock("supervisor:" + agent.name)
	supervisorLock.Lock()
	defer supervisorLock.Unlock()

	cancel := make(chan struct{})
	done := make(chan struct{})
	app.mu.Lock()
	if app.agentCancels == nil {
		app.agentCancels = map[string]chan struct{}{}
		app.agentDones = map[string]chan struct{}{}
	}
	oldCancel := app.agentCancels[agent.name]
	oldDone := app.agentDones[agent.name]
	app.mu.Unlock()

	if oldCancel != nil {
		close(oldCancel)
		if oldDone != nil {
			<-oldDone
		}
	}

	// Resolve the replacement baseline only after the old loop exits. Its last
	// in-flight call may have persisted a held-window checkpoint while shutdown
	// was waiting; reading earlier could rebaseline past that exact window.
	baselineID, blockedReason, continuityErr := app.bootstrapAmbientContinuity(agent, officeRoomID)
	if continuityErr != nil {
		log.Errorf("%s could not read its scope checkpoint; failing safe to replay: %v", agent.name, continuityErr)
	}
	checkpoint, checkpointErr := app.ensureAmbientScopeCheckpoint(agent, officeRoomID, baselineID, blockedReason)
	if checkpointErr != nil {
		// The baseline that existed before the failed durable write is the only
		// safe floor. Empty means replay, never skip.
		baselineID = checkpoint.BaselineID
		log.Errorf("%s could not durably establish its scope checkpoint; failing closed: %v", agent.name, checkpointErr)
	} else {
		baselineID = checkpoint.BaselineID
	}

	app.mu.Lock()
	app.agentCancels[agent.name] = cancel
	app.agentDones[agent.name] = done
	app.setAmbientAgentBaselineIDLocked(agent.name, baselineID)
	// An explicit same-worker restart is the only reset for a provider-open
	// circuit. Prefix deletion covers every room-scoped instance.
	if checkpointErr == nil && continuityErr == nil && blockedReason == "" {
		for key := range app.agentFailures {
			if key == agent.name || strings.HasPrefix(key, agent.name+"@") {
				delete(app.agentFailures, key)
			}
		}
	}
	app.mu.Unlock()
	if checkpointErr != nil {
		app.recordAmbientAgentCheckpointFailure(agent, "", officeRoomID, checkpointErr)
	} else if continuityErr != nil {
		app.recordAmbientAgentCheckpointFailure(agent, "", officeRoomID, continuityErr)
	} else if blockedReason != "" {
		app.recordAmbientAgentContinuityFailure(agent, officeRoomID)
	}

	go app.runAmbientAgentLoop(agent, apiKey, interval, cancel, done)
}

// replaceSpecialtyAgentSupervisor gives non-generic ambient loops the same
// restart contract as startAmbientAgent: stop, await old-loop exit, then reset
// only that worker's circuit and publish the replacement registration.

func (app *kanbanBoardApp) replaceSpecialtyAgentSupervisor(agent ambientAgentConfig, cancel chan struct{}, done chan struct{}, baselineID *string) {
	name := agent.name
	supervisorLock := app.ambientAgentRunLock("supervisor:" + name)
	supervisorLock.Lock()
	defer supervisorLock.Unlock()
	app.mu.Lock()
	if app.agentCancels == nil {
		app.agentCancels = map[string]chan struct{}{}
		app.agentDones = map[string]chan struct{}{}
	}
	oldCancel := app.agentCancels[name]
	oldDone := app.agentDones[name]
	app.mu.Unlock()
	if oldCancel != nil {
		close(oldCancel)
		if oldDone != nil {
			<-oldDone
		}
	}
	checkpointBaseline, blockedReason := "", ""
	var continuityErr error
	if baselineID != nil {
		checkpointBaseline, blockedReason, continuityErr = app.bootstrapAmbientContinuity(agent, officeRoomID)
	} else if existing, ok, err := app.ambientScopeCheckpoint(agent.name); err != nil {
		continuityErr = err
	} else if ok {
		checkpointBaseline, blockedReason = existing.BaselineID, existing.BlockedReason
	}
	if continuityErr != nil {
		log.Errorf("%s could not read its scope checkpoint; failing safe to replay: %v", name, continuityErr)
	}
	checkpoint, checkpointErr := app.ensureAmbientScopeCheckpoint(agent, officeRoomID, checkpointBaseline, blockedReason)
	if checkpointErr != nil {
		checkpointBaseline = checkpoint.BaselineID
		log.Errorf("%s could not durably establish its scope checkpoint; failing closed: %v", name, checkpointErr)
	} else {
		checkpointBaseline = checkpoint.BaselineID
	}
	if baselineID != nil {
		*baselineID = checkpointBaseline
	}
	app.mu.Lock()
	app.agentCancels[name] = cancel
	app.agentDones[name] = done
	if baselineID != nil {
		app.setAmbientAgentBaselineIDLocked(name, *baselineID)
	}
	if checkpointErr == nil && continuityErr == nil && blockedReason == "" {
		for key := range app.agentFailures {
			if key == name || strings.HasPrefix(key, name+"@") {
				delete(app.agentFailures, key)
			}
		}
	}
	app.mu.Unlock()
	if checkpointErr != nil {
		app.recordAmbientAgentCheckpointFailure(agent, "", officeRoomID, checkpointErr)
	} else if continuityErr != nil {
		app.recordAmbientAgentCheckpointFailure(agent, "", officeRoomID, continuityErr)
	} else if blockedReason != "" {
		app.recordAmbientAgentContinuityFailure(agent, officeRoomID)
	}
}

func (app *kanbanBoardApp) runAmbientAgentLoop(agent ambientAgentConfig, apiKey string, interval time.Duration, cancel <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	// The ticker is the safety FLOOR (A3): even with no nudges the agent still
	// sweeps its window on this cadence. Nudges — a transcript append signalling
	// the brain, or the brain-append cascade to the downstream workers — wake the
	// loop between ticks so a pass fires the moment a batch is ready.
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	nudge := app.ambientAgentNudgeChannel(agent.name)

	// A3 debounce timer, per room since W4: when a nudge finds a room's inputs
	// queued but still short of minBatch AND younger than the staleness floor,
	// the loop tracks that room's deadline in `waiting` and arms this one-shot
	// for the SOONEST of them. Nudges are edge-triggered on append, so a room
	// that then falls silent sends no further wake — this timer is what still
	// brains the trailing short exchange (of every waiting room).
	waiting := map[string]time.Time{}
	var stale *time.Timer
	var staleC <-chan time.Time
	stopStale := func() {
		if stale != nil {
			stale.Stop()
			stale = nil
			staleC = nil
		}
	}
	defer stopStale()
	rearmStale := func() {
		stopStale()
		var soonest time.Time
		for _, deadline := range waiting {
			if soonest.IsZero() || deadline.Before(soonest) {
				soonest = deadline
			}
		}
		if soonest.IsZero() {
			return
		}
		wait := time.Until(soonest)
		if wait < 0 {
			wait = 0
		}
		stale = time.NewTimer(wait)
		staleC = stale.C
	}

	// evaluate runs on every nudge / stale-timer wake, for one room. It only
	// reads the in-memory window (no model call) until it decides to fire: a
	// full pass the moment minBatch has accumulated, a short pass once the
	// oldest input is stale, else it records the room's deadline. Cheap and
	// idempotent, so a burst of coalesced nudges cannot spin the model.
	evaluate := func(roomID string) {
		_, count, oldest, ok := app.peekUnconsumedWindow(agent, roomID)
		if !ok {
			delete(waiting, roomID)
			return
		}
		if count >= agent.minBatch() {
			delete(waiting, roomID)
			app.fireAmbientAgentPass(agent, apiKey, agent.minBatch(), roomID)
			return
		}
		if oldest >= agent.nudgeAge() {
			delete(waiting, roomID)
			app.fireAmbientAgentPass(agent, apiKey, ambientNudgeShortBatch, roomID)
			return
		}
		waiting[roomID] = time.Now().Add(agent.nudgeAge() - oldest)
	}

	for {
		select {
		case <-ticker.C:
			// The safety FLOOR sweeps every room with input of the agent's kind
			// (a single office pass for company-global agents), so the per-room
			// short-exchange debounce can never strand a room.
			waiting = map[string]time.Time{}
			stopStale()
			for _, roomID := range app.ambientAgentRooms(agent) {
				app.fireAmbientAgentPass(agent, apiKey, agent.minBatch(), roomID)
			}
		case <-nudge:
			for _, roomID := range app.drainAmbientAgentPendingRooms(agent.name) {
				evaluate(roomID)
			}
			rearmStale()
		case <-staleC:
			stale = nil
			staleC = nil
			now := time.Now()
			for roomID, deadline := range waiting {
				if !deadline.After(now) {
					evaluate(roomID)
				}
			}
			rearmStale()
		case <-cancel:
			return
		}
	}
}

// ambientAgentRooms lists the rooms one safety-floor tick sweeps: the office
// always (the pre-room behavior — an empty window no-ops inside the pass)
// plus, for room-scoped agents, every room holding input of the agent's kind.
func (app *kanbanBoardApp) ambientAgentRooms(agent ambientAgentConfig) []string {
	rooms := []string{officeRoomID}
	if !agent.roomScoped || app == nil || app.memory == nil {
		return rooms
	}
	for _, roomID := range app.memory.roomIDsOfKind(agent.inputKind) {
		if roomID != officeRoomID {
			rooms = append(rooms, roomID)
		}
	}
	return rooms
}

// fireAmbientAgentPass runs one guarded ticker/nudge pass: it peeks the window
// to key A8 backoff off a stable boundary, honors any active backoff, halves the
// batch on retries, runs under the per-agent run lock, and records or clears the
// failure state by the outcome. Close/archive flushes call the same guarded
// seam directly so no lifecycle boundary can bypass a held provider circuit.
func (app *kanbanBoardApp) fireAmbientAgentPass(agent ambientAgentConfig, apiKey string, minBatch int, roomID string) {
	if minBatch < 1 {
		minBatch = 1
	}
	roomID = normalizeRoomID(roomID)
	// §6.5 guests-only deferral: an unattended guest room accumulates input
	// (transcription continues) but spends no model budget until a member is
	// present or the close-flush chain runs its one bounded guarded pass.
	if agent.defersWhenGuestsOnly && app.roomGuestsOnly(roomID) {
		return
	}
	ctx, cancelRequest := context.WithTimeout(context.Background(), agent.requestTimeout)
	_, err := app.invokeAmbientAgentGuarded(agent, ctx, apiKey, nil, minBatch, roomID)
	cancelRequest()
	if err != nil {
		var circuitErr *ambientAgentCircuitOpenError
		if errors.As(err, &circuitErr) {
			return
		}
		log.Errorf("%s worker failed: %v", agent.name, err)
	}
}

// invokeAmbientAgentGuarded is the single admission/completion seam for
// scheduled and on-demand ambient work. It never bypasses backoff/open state,
// and it owns the only classification that may dead-letter or hold a cursor.
func (app *kanbanBoardApp) invokeAmbientAgentGuarded(agent ambientAgentConfig, ctx context.Context, apiKey string, responder openAITextResponder, minBatch int, roomID string) (meetingMemoryEntry, error) {
	if app == nil || app.memory == nil {
		return meetingMemoryEntry{}, nil
	}
	recordCapabilityPoll(agent.name, time.Now().UTC())
	if minBatch < 1 {
		minBatch = 1
	}
	roomID = normalizeRoomID(roomID)
	key := ambientAgentScopeKey(agent, roomID)
	// Admission, the retry-budget read, provider invocation, and completion
	// accounting are one scope-serial transaction. Locking only the producer
	// allowed a burst of callers to pre-admit against the same stale attempt
	// count and then spend beyond the four-call ceiling one by one.
	runLock := app.ambientAgentRunLock(key)
	runLock.Lock()
	defer runLock.Unlock()

	// Recovery must run before either admission check below. A continuity-open
	// scope has no readable window and no attempt budget by design, so leaving
	// this repair inside runAmbientAgentOnceLimitedUnlocked made the guarded
	// scheduler return at peek/budget forever even after a new active sitting
	// supplied the exact clean suffix required for a safe baseline.
	app.repairAmbientContinuityFromCurrentMeeting(agent, roomID)

	headID, count, _, ok := app.peekUnconsumedWindow(agent, roomID)
	if !ok || count < minBatch {
		app.mu.Lock()
		failure := app.agentFailures[key]
		continuityBlocked := failure != nil && failure.continuityOpen
		app.mu.Unlock()
		if continuityBlocked {
			return meetingMemoryEntry{}, app.ambientAgentCircuitError(agent, headID, roomID)
		}
		// Only a genuinely drained window clears obsolete state. A raw held
		// cursor remains visible to peek and can never reach this branch.
		if err := app.clearAmbientAgentFailure(key); err != nil {
			return meetingMemoryEntry{}, &ambientAgentHoldError{err: err}
		}
		return meetingMemoryEntry{}, nil
	}
	proceed, limit := app.ambientAgentAttemptBudget(agent, headID, roomID)
	if !proceed {
		return meetingMemoryEntry{}, app.ambientAgentCircuitError(agent, headID, roomID)
	}
	// The held-window checkpoint is durable BEFORE the provider is contacted.
	// Any persistence fault therefore spends zero provider calls and cannot be
	// converted by a restart into a newer boot baseline that skips this head.
	if err := app.persistAmbientHeldWindow(agent, headID, roomID); err != nil {
		app.recordAmbientAgentCheckpointFailure(agent, headID, roomID, err)
		return meetingMemoryEntry{}, &ambientAgentHoldError{err: fmt.Errorf("%s held-window persistence unavailable", agent.name)}
	}
	entry, err := app.runAmbientAgentOnceLimitedUnlocked(agent, ctx, apiKey, responder, minBatch, limit, roomID)
	if err != nil {
		if isAmbientAgentHoldError(err) || isProviderInvocationFailure(err) || isProviderOutputRejection(err) {
			app.recordAmbientAgentHoldFailure(agent, headID, roomID)
		} else {
			app.recordAmbientAgentFailure(agent, headID, roomID)
		}
		return entry, err
	}
	if err := app.clearAmbientAgentFailure(key); err != nil {
		return entry, &ambientAgentHoldError{err: err}
	}
	return entry, nil
}

func (app *kanbanBoardApp) ambientAgentCircuitError(agent ambientAgentConfig, headID, roomID string) error {
	key := ambientAgentScopeKey(agent, roomID)
	app.mu.Lock()
	failure := app.agentFailures[key]
	var retryAt time.Time
	restartRequired := false
	if failure != nil && (failure.windowID == headID || failure.persistenceOpen || failure.continuityOpen) {
		retryAt = failure.backoffUntil
		restartRequired = failure.providerOpen || failure.persistenceOpen || failure.continuityOpen
	}
	app.mu.Unlock()
	return &ambientAgentCircuitOpenError{Agent: agent.name, RoomID: normalizeRoomID(roomID), RetryAt: retryAt, RestartRequired: restartRequired}
}

func (app *kanbanBoardApp) recordAmbientAgentContinuityFailure(agent ambientAgentConfig, roomID string) {
	key := ambientAgentScopeKey(agent, roomID)
	app.mu.Lock()
	if app.agentFailures == nil {
		app.agentFailures = map[string]*ambientAgentFailure{}
	}
	fail := app.agentFailures[key]
	if fail == nil {
		fail = &ambientAgentFailure{}
		app.agentFailures[key] = fail
	}
	fail.continuityOpen = true
	fail.providerOpen = true
	fail.backoffUntil = time.Time{}
	app.mu.Unlock()
	recordCapabilityFailure(agent.name, time.Now().UTC(), errors.New("durable consumed-through continuity is ambiguous"))
}

// recordAmbientAgentCheckpointFailure is a fail-closed durability circuit.
// It is deliberately distinct from provider attempts: persistence faults spend
// no model budget, never dead-letter, and may be cleared only by a restart (or
// success path) that first proves the scope checkpoint durable again.
func (app *kanbanBoardApp) recordAmbientAgentCheckpointFailure(agent ambientAgentConfig, headID, roomID string, err error) {
	key := ambientAgentScopeKey(agent, roomID)
	app.recordAmbientCheckpointFailureKey(key, agent.name, headID, err)
}

func (app *kanbanBoardApp) recordAmbientCheckpointFailureKey(key, agentName, headID string, err error) {
	app.mu.Lock()
	if app.agentFailures == nil {
		app.agentFailures = map[string]*ambientAgentFailure{}
	}
	fail := app.agentFailures[key]
	if fail == nil || (strings.TrimSpace(headID) != "" && fail.windowID != headID) {
		fail = &ambientAgentFailure{windowID: strings.TrimSpace(headID)}
		app.agentFailures[key] = fail
	}
	fail.persistenceOpen = true
	fail.providerOpen = true
	fail.backoffUntil = time.Time{}
	app.mu.Unlock()
	recordCapabilityFailure(agentName, time.Now().UTC(), errors.New("held-window checkpoint persistence unavailable"))
	log.Errorf("%s worker opened its persistence circuit; provider admission is suppressed: %v", key, err)
}

// recordAmbientAgentHoldFailure arms the same capped retry backoff as a poison
// failure but never dead-letters and never changes the durable baseline.
func (app *kanbanBoardApp) recordAmbientAgentHoldFailure(agent ambientAgentConfig, headID string, roomID string) {
	key := ambientAgentScopeKey(agent, roomID)
	app.mu.Lock()
	if app.agentFailures == nil {
		app.agentFailures = map[string]*ambientAgentFailure{}
	}
	fail := app.agentFailures[key]
	if fail == nil || fail.windowID != headID {
		fail = &ambientAgentFailure{windowID: headID}
		app.agentFailures[key] = fail
	}
	if fail.attempts < ambientProviderMaxWindowAttempts {
		fail.attempts++
	}
	attempt := fail.attempts
	opened := attempt >= ambientProviderMaxWindowAttempts
	if opened {
		fail.providerOpen = true
		fail.backoffUntil = time.Time{}
	} else {
		backoff := ambientAgentBackoffBase
		for i := 1; i < attempt && backoff < ambientAgentBackoffCap; i++ {
			backoff *= 2
			if backoff > ambientAgentBackoffCap {
				backoff = ambientAgentBackoffCap
			}
		}
		fail.backoffUntil = time.Now().Add(backoff)
	}
	backoffUntil := fail.backoffUntil
	app.mu.Unlock()
	if err := app.persistAmbientHeldWindow(agent, headID, roomID); err != nil {
		app.recordAmbientAgentCheckpointFailure(agent, headID, roomID, err)
		log.Errorf("%s could not persist its held-window checkpoint and is fail-closed: %v", key, err)
		return
	}
	if opened {
		log.Errorf("%s worker opened its provider circuit after %d failures on input %s; cursor remains put until an explicit worker restart", key, attempt, headID)
		return
	}
	log.Warnf("%s worker is holding input %s after recoverable provider failure; cursor remains put until %s", key, headID, backoffUntil.UTC().Format(time.RFC3339))
}

func (app *kanbanBoardApp) runAmbientAgentOnce(agent ambientAgentConfig, ctx context.Context, apiKey string, responder openAITextResponder, minBatch int) (meetingMemoryEntry, error) {
	return app.runAmbientAgentOnceLimited(agent, ctx, apiKey, responder, minBatch, agent.maxBatch(), officeRoomID)
}

// runAmbientAgentOnceForRoom is the W4 room-dimensioned pass entry: the
// close-flush chain and the room recap force a specific room's window.
func (app *kanbanBoardApp) runAmbientAgentOnceForRoom(agent ambientAgentConfig, ctx context.Context, apiKey string, responder openAITextResponder, minBatch int, roomID string) (meetingMemoryEntry, error) {
	return app.runAmbientAgentOnceLimited(agent, ctx, apiKey, responder, minBatch, agent.maxBatch(), roomID)
}

// runAmbientAgentOnceLimited is runAmbientAgentOnce with an explicit batch
// ceiling so the A8 retry path can HALVE the window on a failing pass (shrinking
// the blast radius of a poison entry) without touching the agent's configured
// maxBatch. maxBatch <= 0 (or above the configured ceiling) falls back to the
// configured maxBatch.
func (app *kanbanBoardApp) runAmbientAgentOnceLimited(agent ambientAgentConfig, ctx context.Context, apiKey string, responder openAITextResponder, minBatch int, maxBatch int, roomID string) (meetingMemoryEntry, error) {
	if app == nil || app.memory == nil {
		return meetingMemoryEntry{}, nil
	}
	if responder == nil {
		responder = createOpenAITextResponse
	}
	if minBatch < 1 {
		minBatch = 1
	}
	if configured := agent.maxBatch(); maxBatch <= 0 || maxBatch > configured {
		maxBatch = configured
	}
	roomID = normalizeRoomID(roomID)

	// One pass at a time per effective agent scope: the cursor only advances when
	// produce appends its artifact at the end of a pass, so overlapping passes
	// (the ticker loop vs an archive flush, or two concurrent archives) would
	// consume — and apply — the same input batch twice. Per-room locks mean two
	// rooms' close flushes neither serialize nor deadlock (W4 §7.4). The
	// unconsumed window is read after the lock is held, so a waiting pass sees
	// the cursor the previous pass advanced.
	runLock := app.ambientAgentRunLock(ambientAgentScopeKey(agent, roomID))
	runLock.Lock()
	defer runLock.Unlock()
	return app.runAmbientAgentOnceLimitedUnlocked(agent, ctx, apiKey, responder, minBatch, maxBatch, roomID)
}

// runAmbientAgentOnceLimitedUnlocked is the provider/body half of a pass. The
// caller must hold ambientAgentRunLock(ambientAgentScopeKey(...)). The guarded
// seam keeps that lock across admission and completion; the direct test seams
// acquire it in runAmbientAgentOnceLimited above.
func (app *kanbanBoardApp) runAmbientAgentOnceLimitedUnlocked(agent ambientAgentConfig, ctx context.Context, apiKey string, responder openAITextResponder, minBatch int, maxBatch int, roomID string) (meetingMemoryEntry, error) {
	if app == nil || app.memory == nil {
		return meetingMemoryEntry{}, nil
	}
	if responder == nil {
		responder = createOpenAITextResponse
	}
	if minBatch < 1 {
		minBatch = 1
	}
	if configured := agent.maxBatch(); maxBatch <= 0 || maxBatch > configured {
		maxBatch = configured
	}
	roomID = normalizeRoomID(roomID)
	app.repairAmbientContinuityFromCurrentMeeting(agent, roomID)

	servicePrincipal := app.currentRoomMediaRecallPrincipal(roomID, app.memory.currentMeetingID(roomID))
	baselineID := app.ambientAgentWindowBaseline(agent, roomID)
	app.mu.Lock()
	failure := app.agentFailures[ambientAgentScopeKey(agent, roomID)]
	durabilityBlocked := failure != nil && (failure.persistenceOpen || failure.continuityOpen)
	app.mu.Unlock()
	if durabilityBlocked {
		return meetingMemoryEntry{}, app.ambientAgentCircuitError(agent, "", roomID)
	}
	inputs := app.memory.unconsumedEntriesAfterForRoomForPrincipal(agent.inputKind, agent.artifactKind, agent.cursorMetadataKey, maxBatch, baselineID, agent.windowRoomID(roomID), servicePrincipal)
	inputs = compatibleAmbientScopePrefix(inputs)
	if len(inputs) < minBatch {
		return meetingMemoryEntry{}, nil
	}

	return agent.produce(app, ctx, apiKey, inputs, responder)
}

func (app *kanbanBoardApp) ambientAgentBaselineID(name string) string {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.agentBaselineIDs[name]
}

// ambientAgentWindowBaseline resolves the baseline a window read uses. The
// OFFICE reads whatever is registered (possibly nothing — startAmbientAgent
// and the flush/recap ensure calls own office registration, exactly the
// pre-room contract, so direct test-seam runs stay backfill-visible). Named
// rooms register lazily on first touch so a room-scoped agent never backfills
// a room's pre-boot history.
func (app *kanbanBoardApp) ambientAgentWindowBaseline(agent ambientAgentConfig, roomID string) string {
	if normalizeRoomID(roomID) == officeRoomID {
		return app.ambientAgentBaselineID(agent.name)
	}
	return app.ensureAmbientAgentRoomBaseline(agent, roomID)
}

// ensureAmbientAgentRoomBaseline returns the (agent, room) baseline,
// registering it on first touch. A missing sidecar migrates from the durable
// consumed-through artifact cursor; raw history with no trustworthy cursor is
// blocked rather than silently treated as consumed.
func (app *kanbanBoardApp) ensureAmbientAgentRoomBaseline(agent ambientAgentConfig, roomID string) string {
	roomID = agent.scopeRoomID(roomID)
	key := ambientAgentScopeKey(agent, roomID)

	app.mu.Lock()
	if baseline, registered := app.agentBaselineIDs[key]; registered {
		app.mu.Unlock()
		return baseline
	}
	app.mu.Unlock()

	baseline, blockedReason, continuityErr := app.bootstrapAmbientContinuity(agent, roomID)
	if continuityErr != nil {
		log.Errorf("%s could not read its scope checkpoint; failing safe to replay: %v", key, continuityErr)
	}
	checkpoint, checkpointErr := app.ensureAmbientScopeCheckpoint(agent, roomID, baseline, blockedReason)
	if checkpointErr != nil {
		baseline = checkpoint.BaselineID
		app.recordAmbientAgentCheckpointFailure(agent, "", roomID, checkpointErr)
	} else {
		baseline = checkpoint.BaselineID
		if continuityErr != nil {
			app.recordAmbientAgentCheckpointFailure(agent, "", roomID, continuityErr)
		} else if blockedReason != "" {
			app.recordAmbientAgentContinuityFailure(agent, roomID)
		}
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	if registered, ok := app.agentBaselineIDs[key]; ok {
		return registered
	}
	app.setAmbientAgentBaselineIDLocked(key, baseline)
	return baseline
}

// repairAmbientContinuityFromCurrentMeeting is the narrow recovery path for a
// room-scoped meeting worker whose legacy office history has no single durable
// consumed-through cursor. It never guesses across old meetings and never
// replays them. Once a new active sitting supplies an exact clean suffix, the
// worker durably anchors immediately before that suffix, closes only the
// ambiguity circuit, and can process the current meeting normally.
func (app *kanbanBoardApp) repairAmbientContinuityFromCurrentMeeting(agent ambientAgentConfig, roomID string) bool {
	roomID = agent.scopeRoomID(roomID)
	key := ambientAgentScopeKey(agent, roomID)
	checkpoint, ok, err := app.ambientScopeCheckpoint(key)
	if err != nil || !ok || strings.TrimSpace(checkpoint.WindowID) != "" || checkpoint.BlockedReason != ambientContinuityAmbiguous {
		return false
	}
	app.mu.Lock()
	failure := app.agentFailures[key]
	blocked := failure != nil && failure.continuityOpen && !failure.persistenceOpen
	app.mu.Unlock()
	if !blocked {
		return false
	}
	baseline, admitted := app.meetingAnalysisCurrentMeetingBootstrapBaseline(agent, roomID)
	if !admitted {
		return false
	}
	if err := app.persistAmbientCheckpointBaseline(agent, baseline, roomID); err != nil {
		app.recordAmbientAgentCheckpointFailure(agent, "", roomID, err)
		return false
	}
	app.setAmbientAgentBaselineID(key, baseline)
	if err := app.clearAmbientAgentFailure(key); err != nil {
		return false
	}
	log.Infof("%s repaired ambiguous legacy continuity at the exact active-meeting boundary", key)
	return true
}

// ambientAgentRunLock returns the per-agent mutex that serializes whole
// runner passes (read window -> produce -> append artifact).
func (app *kanbanBoardApp) ambientAgentRunLock(name string) *sync.Mutex {
	app.mu.Lock()
	defer app.mu.Unlock()

	if app.agentRunLocks == nil {
		app.agentRunLocks = map[string]*sync.Mutex{}
	}
	lock, ok := app.agentRunLocks[name]
	if !ok {
		lock = &sync.Mutex{}
		app.agentRunLocks[name] = lock
	}

	return lock
}

// ambientAgentNudgeChannel returns (creating if needed) the A3 buffered(1) wake
// channel for an agent. A depth of one debounces a burst of transcript appends
// into a single wake: the runner re-reads the whole unconsumed window on each
// wake, so extra sends would only spin it. Reused across loop restarts (rejoin)
// since it is keyed by agent name, not by loop instance.
func (app *kanbanBoardApp) ambientAgentNudgeChannel(name string) chan struct{} {
	app.mu.Lock()
	defer app.mu.Unlock()

	if app.agentNudges == nil {
		app.agentNudges = map[string]chan struct{}{}
	}
	ch, ok := app.agentNudges[name]
	if !ok {
		ch = make(chan struct{}, 1)
		app.agentNudges[name] = ch
	}

	return ch
}

// nudgeAmbientAgent (A3) wakes an agent's runner for the OFFICE — every
// pre-room call site keeps its exact behavior.
func (app *kanbanBoardApp) nudgeAmbientAgent(name string) {
	app.nudgeAmbientAgentForRoom(name, officeRoomID)
}

// nudgeAmbientAgentForRoom (A3 + W4 §7.4) wakes an agent's runner so it
// re-evaluates the ROOM's window immediately instead of waiting for the next
// safety-floor tick. The room rides a pending set (never the channel), so a
// burst across rooms collapses to one wake without losing any room.
// Non-blocking and safe for an agent that never started (keyless / disabled)
// — the single buffered slot absorbs the send with no receiver draining it.
func (app *kanbanBoardApp) nudgeAmbientAgentForRoom(name string, roomID string) {
	if app == nil {
		return
	}
	roomID = normalizeRoomID(roomID)
	app.mu.Lock()
	if app.agentPendingRooms == nil {
		app.agentPendingRooms = map[string]map[string]struct{}{}
	}
	if app.agentPendingRooms[name] == nil {
		app.agentPendingRooms[name] = map[string]struct{}{}
	}
	app.agentPendingRooms[name][roomID] = struct{}{}
	app.mu.Unlock()
	select {
	case app.ambientAgentNudgeChannel(name) <- struct{}{}:
	default:
	}
}

// drainAmbientAgentPendingRooms pops the set of rooms nudged since the last
// wake. The runner re-reads each room's whole unconsumed window, so draining
// before evaluating can never lose input.
func (app *kanbanBoardApp) drainAmbientAgentPendingRooms(name string) []string {
	if app == nil {
		return nil
	}
	app.mu.Lock()
	pending := app.agentPendingRooms[name]
	delete(app.agentPendingRooms, name)
	app.mu.Unlock()
	if len(pending) == 0 {
		// a wake without a recorded room (a legacy direct channel send in a
		// test) still re-checks the office, the pre-room behavior.
		return []string{officeRoomID}
	}
	rooms := make([]string, 0, len(pending))
	for roomID := range pending {
		rooms = append(rooms, roomID)
	}
	return rooms
}

// peekUnconsumedWindow reports the oldest unconsumed input's id (the stable A8
// backoff key), how many inputs are queued (capped at minBatch — enough to know
// the batch is ready), and how long the oldest has waited, all WITHOUT advancing
// any cursor. The A3 nudge path uses it to choose between firing now and arming
// the staleness timer, and fireAmbientAgentPass uses the head id to key retries.
func (app *kanbanBoardApp) peekUnconsumedWindow(agent ambientAgentConfig, roomID string) (headID string, count int, oldestAge time.Duration, ok bool) {
	if app == nil || app.memory == nil {
		return "", 0, 0, false
	}
	limit := agent.minBatch()
	if limit < 1 {
		limit = 1
	}
	roomID = normalizeRoomID(roomID)
	principal := app.currentRoomMediaRecallPrincipal(roomID, app.memory.currentMeetingID(roomID))
	inputs := app.memory.unconsumedEntriesAfterForRoomForPrincipal(agent.inputKind, agent.artifactKind, agent.cursorMetadataKey, limit, app.ambientAgentWindowBaseline(agent, roomID), agent.windowRoomID(roomID), principal)
	if len(inputs) == 0 {
		return "", 0, 0, false
	}

	return inputs[0].ID, len(inputs), time.Since(inputs[0].CreatedAt), true
}

// ambientAgentAttemptBudget reports whether a pass on the window headed by
// headID may fire now and, if so, the batch ceiling to use. A fresh window (no
// recorded failure, or a different head than the one failing) runs the full
// maxBatch; a window still inside its backoff is held off; a window past its
// backoff runs a batch HALVED once per prior attempt so a poison entry's blast
// radius shrinks each retry until the head is finally dead-lettered.
func (app *kanbanBoardApp) ambientAgentAttemptBudget(agent ambientAgentConfig, headID string, roomID string) (bool, int) {
	full := agent.maxBatch()

	app.mu.Lock()
	fail := app.agentFailures[ambientAgentScopeKey(agent, roomID)]
	if fail == nil {
		app.mu.Unlock()
		return true, full
	}
	if fail.persistenceOpen || fail.continuityOpen {
		app.mu.Unlock()
		return false, 0
	}
	if fail.windowID != headID {
		app.mu.Unlock()
		return true, full
	}
	attempts := fail.attempts
	backoffUntil := fail.backoffUntil
	providerOpen := fail.providerOpen
	app.mu.Unlock()

	if providerOpen {
		return false, 0
	}
	if time.Now().Before(backoffUntil) {
		return false, 0
	}
	limit := full
	for i := 0; i < attempts && limit > 1; i++ {
		limit = (limit + 1) / 2
	}
	// Never halve below minBatch: a sub-minBatch window just no-ops (clearing the
	// failure record) and would stall the attempt count short of the dead-letter
	// cap, so the poison window could never be skipped.
	if min := agent.minBatch(); limit < min {
		limit = min
	}

	return true, limit
}

// recordAmbientAgentFailure (A8) accrues a failure on the window headed by
// headID. Under ambientAgentMaxWindowAttempts it arms an exponential backoff;
// at the cap it dead-letters the head — advancing the agent's baseline past it
// so the next pass tries the remainder instead of re-sending the poison window
// forever. Only the agent's single loop goroutine touches its own record.
func (app *kanbanBoardApp) recordAmbientAgentFailure(agent ambientAgentConfig, headID string, roomID string) {
	key := ambientAgentScopeKey(agent, roomID)
	app.mu.Lock()
	if app.agentFailures == nil {
		app.agentFailures = map[string]*ambientAgentFailure{}
	}
	fail := app.agentFailures[key]
	if fail == nil || fail.windowID != headID {
		fail = &ambientAgentFailure{windowID: headID}
		app.agentFailures[key] = fail
	}
	fail.attempts++
	attempts := fail.attempts
	deadLetter := attempts >= ambientAgentMaxWindowAttempts
	if deadLetter {
		delete(app.agentFailures, key)
	} else {
		backoff := ambientAgentBackoffBase << (attempts - 1)
		if backoff > ambientAgentBackoffCap {
			backoff = ambientAgentBackoffCap
		}
		fail.backoffUntil = time.Now().Add(backoff)
	}
	app.mu.Unlock()

	if deadLetter {
		// Advancing a poison baseline is itself recovery state. Publish the
		// durable anchor first; if that write is unavailable, hold the window and
		// surface a persistence circuit instead of creating a restart-only skip.
		if err := app.persistAmbientCheckpointBaseline(agent, headID, roomID); err != nil {
			app.recordAmbientAgentCheckpointFailure(agent, headID, roomID, err)
			return
		}
		// setAmbientAgentBaselineID re-locks app.mu, so it must run after the
		// unlock above (app.mu is not reentrant).
		app.setAmbientAgentBaselineID(key, headID)
		log.Errorf("%s worker dead-lettered input %s after %d failed attempts; advancing the baseline past it", key, headID, attempts)
		// Coverage honesty (memory study 1.4, gap #9): the raw input still exists
		// on disk but is now permanently skipped, so leave a tombstone the
		// coverage machinery can see. Without it, meetingCoverageDetail would keep
		// reading a "full" capture stamp for a meeting whose synthesis silently
		// lost a window.
		app.appendAmbientDeadLetterTombstone(agent, headID, roomID, attempts)
	}
}

// appendAmbientDeadLetterTombstone records that the runner abandoned a synthesis
// window (memory study 1.4). It resolves the abandoned head input to recover the
// meeting it belonged to and the moment it landed, so meetingCoverageDetail can
// flip that meeting to partial_synthesis. The tombstone is mint-free and
// relevance=expired, so it never enters recall or opens a phantom sitting; a
// missing head input (already swept) still leaves a span-less stub so the FACT of
// the skip survives. Best-effort: a write failure only loses the honesty flag,
// never the dead-letter itself (the baseline already advanced above).
func (app *kanbanBoardApp) appendAmbientDeadLetterTombstone(agent ambientAgentConfig, headID string, roomID string, attempts int) {
	if app == nil || app.memory == nil {
		return
	}
	roomID = normalizeRoomID(roomID)
	metadata := map[string]string{
		relevanceMetadataKey:           relevanceExpired,
		deadLetterAgentMetadataKey:     agent.name,
		deadLetterRoomMetadataKey:      roomID,
		deadLetterInputKindMetadataKey: agent.inputKind,
		deadLetterAttemptsMetadataKey:  strconv.Itoa(attempts),
		"roomId":                       roomID,
	}
	if head, ok := app.memory.entryByKindAndID(agent.inputKind, headID); ok {
		if meetingID := strings.TrimSpace(head.Metadata["meetingId"]); meetingID != "" {
			metadata["meetingId"] = meetingID
		}
		at := head.CreatedAt.UTC().Format(time.RFC3339)
		metadata[deadLetterSpanStartMetadataKey] = at
		metadata[deadLetterSpanEndMetadataKey] = at
	}
	text := fmt.Sprintf("%s abandoned %s input %s after %d failed synthesis attempts; the raw window was captured but never folded in.", agent.name, agent.inputKind, headID, attempts)
	id := fmt.Sprintf("dead-letter-%s-%s-%s", agent.name, roomID, headID)
	if _, _, err := app.memory.appendDeadLetter(id, text, metadata); err != nil {
		log.Errorf("%s failed to write dead-letter tombstone for %s: %v", agent.name, headID, err)
	}
}

// clearAmbientAgentFailure drops an agent's failure record after a clean pass
// (or when its window drained), so the next failure starts a fresh backoff.
func (app *kanbanBoardApp) clearAmbientAgentFailure(name string) error {
	// Clear the durable held marker before publishing the closed in-memory
	// circuit. If durability is unavailable the prior marker remains the safe
	// restart floor and the worker stays fail-closed.
	if err := app.clearAmbientHeldWindow(name); err != nil {
		agentName := name
		if at := strings.Index(agentName, "@"); at >= 0 {
			agentName = agentName[:at]
		}
		app.recordAmbientCheckpointFailureKey(name, agentName, "", err)
		return fmt.Errorf("%s held-window checkpoint persistence unavailable", name)
	}
	app.mu.Lock()
	delete(app.agentFailures, name)
	app.mu.Unlock()
	return nil
}

// ensureAmbientAgentBaseline registers the startup cursor for an agent whose
// loop never ran this boot (the flush can fire before startAmbientAgent), so
// an archive flush starts where the loop would have and cannot backfill
// history persisted before this process started. Office key; named rooms
// register lazily through ensureAmbientAgentRoomBaseline.
func (app *kanbanBoardApp) ensureAmbientAgentBaseline(agent ambientAgentConfig) {
	_ = app.ensureAmbientAgentRoomBaseline(agent, officeRoomID)
}

func (app *kanbanBoardApp) setAmbientAgentBaselineID(name string, baselineID string) {
	app.mu.Lock()
	defer app.mu.Unlock()

	app.setAmbientAgentBaselineIDLocked(name, baselineID)
}

func (app *kanbanBoardApp) setAmbientAgentBaselineIDLocked(name string, baselineID string) {
	if app.agentBaselineIDs == nil {
		app.agentBaselineIDs = map[string]string{}
	}
	app.agentBaselineIDs[name] = baselineID
}

// closeFlushChain is the ordered agent chain a CLOSING meeting flushes, in
// dependency order so each stage consumes what the previous one just landed:
// brain summarizes the final transcript window, the decision ledger consumes
// it, mission intel titles the RIGHT record before rotation, then the narrative maintainer (axx/main: storyline
// dossiers fold the meeting in), then the Track-2 rollup tiers — meeting digest
// (consumes the fresh brains: the closing meeting's cumulative T2 digest),
// day digest (folds the fresh meeting digests into the local-day T3 slices),
// entity ledger (consolidates the digest's facts plus new decision rows into
// the canonical registry), and company digest (refreshes T4 from the fresh
// ledger deltas). Every stage is cursor-gated and upsert-idempotent, so a
// double flush (archive racing idle, or a flush racing the ticker) is safe.
//
// The Item B research-suggestion worker (suggestion_agent.go) is deliberately
// ABSENT here: it volunteers a confirm-first proposal for a LIVE room to act on,
// so firing it as a closing meeting empties out — when no one is present to
// confirm — would only mint an orphan card. It rides its ticker floor only.
func closeFlushChain() []ambientAgentConfig {
	return []ambientAgentConfig{
		meetingBrainAgent(),
		decisionLedgerAgent(),
		missionIntelligenceAgent(),
		narrativeMaintainerAgent(),
		meetingDigestAgent(),
		dayDigestAgent(),
		entityLedgerAgent(),
		companyDigestAgent(),
	}
}

// flushAmbientAgentsForArchive synchronously runs the close flush chain with a
// batch minimum of one before the archive snapshot is taken (and before
// rotateMeetingID — a later ambient tick would otherwise consume the
// pre-archive write-ups and stamp the old meeting's output onto the successor
// id). Skips silently when no API key is configured or nothing new exists.
// The explicit archive path is an office seam, and the office sitting's latch
// rides along so a listen-only office archive still skips the board stage.
func (app *kanbanBoardApp) flushAmbientAgentsForArchive() {
	listenOnly := false
	if app != nil && app.meetings != nil {
		if record, ok := app.meetings.activeRecord(officeRoomID); ok {
			listenOnly = record.ListenOnly
		}
	}
	app.flushAmbientAgentsForClose("archive", officeRoomID, listenOnly)
}

// flushAmbientAgentsForClose is the shared boundary flush for BOTH meeting
// close seams — explicit archive and idle end (the Track-2 idle-close hole:
// that path previously wrote no final rollup at all, so idle-closed meetings
// never got a digest and "what did I miss" silently skipped them). Bounded by
// meetingArchiveFlushTimeout and best-effort throughout: every failure only
// logs, the caller always proceeds. W4 §7.4: the flush is ROOM-scoped — each
// room-scoped stage runs only the closing room's window under its own
// (agent, room) lock, so two rooms closing concurrently neither serialize nor
// deadlock; the company-global rollup stages keep their single cursor. A
// listen-only sitting SKIPS the board stage (mirroring the research-suggestion
// agent's standing exclusion from this chain) — §7.3 layer 1 at the close seam.
func (app *kanbanBoardApp) flushAmbientAgentsForClose(seam string, roomID string, listenOnly bool) {
	if app == nil || app.memory == nil {
		return
	}
	app.flushAmbientAgentsForCloseWithResponder(seam, roomID, listenOnly, nil)
}

// flushAmbientAgentsForCloseWithResponder is the injectable-responder seam the
// concurrency tests drive; production passes nil (the real OpenAI responder).
func (app *kanbanBoardApp) flushAmbientAgentsForCloseWithResponder(seam string, roomID string, listenOnly bool, responder openAITextResponder) {
	if app == nil || app.memory == nil {
		return
	}
	app.mu.Lock()
	apiKey := app.apiKey
	app.mu.Unlock()
	if strings.TrimSpace(apiKey) == "" {
		return
	}
	roomID = normalizeRoomID(roomID)

	ctx, cancel := context.WithTimeout(context.Background(), meetingArchiveFlushTimeout)
	defer cancel()
	for _, agent := range closeFlushChain() {
		// honor both disable forms (interval=0/off/false/disabled and the
		// _DISABLED env): a turned-off agent must not run at close time.
		if boolEnv(agent.disabledEnv) || agent.interval() <= 0 {
			continue
		}
		// §7.3: a listen-only sitting builds its record (brain, ledger, digest,
		// narrative) but never mutates the board at close time.
		if listenOnly && agent.name == meetingBoardAgentName {
			continue
		}
		// A8: the overall ceiling is a backstop, not a per-call budget — once it
		// is spent, stop rather than spin failing every remaining pass.
		if ctx.Err() != nil {
			log.Errorf("%s flush reached the overall %s ceiling; skipping the remaining passes", seam, meetingArchiveFlushTimeout)
			break
		}
		app.ensureAmbientAgentRoomBaseline(agent, roomID)
		// A8: each pass gets its OWN deadline (bounded by whatever remains of the
		// overall ceiling) so a slow upstream pass can no longer starve the
		// mission / narrative / digest passes queued behind it.
		passTimeout := agent.requestTimeout
		if passTimeout <= 0 || passTimeout > meetingArchiveFlushPassTimeout {
			passTimeout = meetingArchiveFlushPassTimeout
		}
		passCtx, cancelPass := context.WithTimeout(ctx, passTimeout)
		_, err := app.invokeAmbientAgentGuarded(agent, passCtx, apiKey, responder, 1, roomID)
		cancelPass()
		if err != nil {
			var circuitErr *ambientAgentCircuitOpenError
			if errors.As(err, &circuitErr) {
				log.Warnf("%s %s flush skipped while its bounded retry circuit is open or cooling down", agent.name, seam)
			} else {
				log.Errorf("%s %s flush failed: %v", agent.name, seam, err)
			}
		}
	}
}

// unconsumedEntriesAfter returns up to limit entries of inputKind that no
// artifactKind entry has consumed yet. The newest artifact's cursor metadata
// (or, absent that, the artifact's own position) marks where consumption
// stopped; baselineID additionally skips history at boot when backfill is off.
func (store *meetingMemoryStore) unconsumedEntriesAfter(inputKind string, artifactKind string, cursorKey string, limit int, baselineID string) []meetingMemoryEntry {
	return store.unconsumedEntriesAfterForRoom(inputKind, artifactKind, cursorKey, limit, baselineID, "")
}

// unconsumedEntriesAfterForRoom is unconsumedEntriesAfter with the W4 room
// dimension (§7.4 — the make-or-break): a non-empty roomID filters BOTH sides
// by room, so the cursor for (agent, room) is the newest artifact-of-kind
// stamped with that roomId — legacy artifacts without a roomId stamp read as
// office, which is exactly how the office pipeline resumes seamlessly across
// the deploy — and the inputs are only that room's. One room's pass can never
// advance another room's window. roomID == "" keeps the company-global
// single-cursor scan unchanged.
func (store *meetingMemoryStore) unconsumedEntriesAfterForRoom(inputKind string, artifactKind string, cursorKey string, limit int, baselineID string, roomID string) []meetingMemoryEntry {
	return store.unconsumedEntriesAfterForRoomForPrincipal(inputKind, artifactKind, cursorKey, limit, baselineID, roomID, RecallPrincipal{})
}

func (store *meetingMemoryStore) unconsumedEntriesAfterForRoomForPrincipal(inputKind string, artifactKind string, cursorKey string, limit int, baselineID string, roomID string, principal RecallPrincipal) []meetingMemoryEntry {
	if store == nil || limit <= 0 {
		return nil
	}
	roomID = strings.TrimSpace(roomID)
	matchesRoom := func(entry meetingMemoryEntry) bool {
		return roomID == "" || normalizeRoomID(entry.Metadata["roomId"]) == roomID
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	entries := store.entries

	startIndex := 0
	baselineID = strings.TrimSpace(baselineID)
	if baselineID != "" {
		for index := len(entries) - 1; index >= 0; index-- {
			if entries[index].ID == baselineID && entries[index].Kind == inputKind && !memoryEntryHiddenFromRecall(entries[index]) && matchesRoom(entries[index]) && (principal.Audience == "" || recallEntryScopeAllowed(entries[index].Metadata, principal)) {
				startIndex = index + 1
				break
			}
		}
	}
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if entry.Kind != artifactKind || memoryEntryHiddenFromRecall(entry) || !matchesRoom(entry) || (principal.Audience != "" && !recallEntryScopeAllowed(entry.Metadata, principal)) {
			continue
		}
		cursorID := strings.TrimSpace(entry.Metadata[cursorKey])
		if cursorID != "" {
			for inputIndex := len(entries) - 1; inputIndex >= 0; inputIndex-- {
				if entries[inputIndex].ID == cursorID && entries[inputIndex].Kind == inputKind && !memoryEntryHiddenFromRecall(entries[inputIndex]) && matchesRoom(entries[inputIndex]) && (principal.Audience == "" || recallEntryScopeAllowed(entries[inputIndex].Metadata, principal)) {
					if inputIndex+1 > startIndex {
						startIndex = inputIndex + 1
					}
					break
				}
			}
		} else if index+1 > startIndex {
			startIndex = index + 1
		}
		break
	}

	inputs := make([]meetingMemoryEntry, 0, limit)
	for _, entry := range entries[startIndex:] {
		if entry.Kind != inputKind || memoryEntryHiddenFromRecall(entry) || !matchesRoom(entry) {
			continue
		}
		if principal.Audience != "" && !recallEntryScopeAllowed(entry.Metadata, principal) {
			continue
		}
		inputs = append(inputs, cloneMemoryEntry(entry))
		if len(inputs) >= limit {
			break
		}
	}

	return inputs
}

func compatibleAmbientScopePrefix(inputs []meetingMemoryEntry) []meetingMemoryEntry {
	roomID, sittingID, mediaGeneration := "", "", ""
	for index, entry := range inputs {
		visibility := strings.ToLower(strings.TrimSpace(entry.Metadata["visibility"]))
		if visibility != "room" && visibility != "room_only" {
			continue
		}
		entryRoom := normalizeRoomID(entry.Metadata["roomId"])
		entrySitting := firstNonEmptyString(strings.TrimSpace(entry.Metadata["sittingId"]), strings.TrimSpace(entry.Metadata["meetingId"]))
		entryGeneration := strings.TrimSpace(entry.Metadata["mediaGeneration"])
		if roomID == "" {
			roomID, sittingID, mediaGeneration = entryRoom, entrySitting, entryGeneration
			continue
		}
		if entryRoom != roomID || entrySitting != sittingID || entryGeneration != mediaGeneration {
			return inputs[:index]
		}
	}
	return inputs
}

func ambientDerivedScopeMetadata(inputs []meetingMemoryEntry) map[string]string {
	metadata := map[string]string{"tenantId": canonicalArtifactTenantID(), "visibility": "organization"}
	for _, entry := range inputs {
		visibility := strings.ToLower(strings.TrimSpace(entry.Metadata["visibility"]))
		if visibility == "room" || visibility == "room_only" {
			metadata["visibility"] = "room_only"
			metadata["roomId"] = normalizeRoomID(entry.Metadata["roomId"])
			metadata["sittingId"] = firstNonEmptyString(strings.TrimSpace(entry.Metadata["sittingId"]), strings.TrimSpace(entry.Metadata["meetingId"]))
			if generation := strings.TrimSpace(entry.Metadata["mediaGeneration"]); generation != "" {
				metadata["mediaGeneration"] = generation
			}
		}
	}
	return metadata
}

func applyAmbientDerivedScope(metadata map[string]string, inputs []meetingMemoryEntry) map[string]string {
	if metadata == nil {
		metadata = map[string]string{}
	}
	for key, value := range ambientDerivedScopeMetadata(inputs) {
		metadata[key] = value
	}
	return metadata
}

func ambientServicePrincipalForInputs(inputs []meetingMemoryEntry) RecallPrincipal {
	roomID := ambientWindowRoomID(inputs)
	sittingID := ""
	if len(inputs) > 0 {
		sittingID = firstNonEmptyString(strings.TrimSpace(inputs[0].Metadata["sittingId"]), strings.TrimSpace(inputs[0].Metadata["meetingId"]))
	}
	principal := sharedRoomRecallPrincipal(roomID, sittingID)
	if len(inputs) > 0 {
		if generation, err := strconv.ParseUint(strings.TrimSpace(inputs[0].Metadata["mediaGeneration"]), 10, 64); err == nil {
			principal.MediaGeneration = generation
		}
	}
	return principal
}

func (store *meetingMemoryStore) latestEntryIDOfKind(kind string) string {
	return store.latestEntryIDOfKindForRoom(kind, "")
}

// latestEntryIDOfKindForRoom is the startup-baseline scan with the W4 room
// filter; roomID == "" spans every room (the company-global agents).
func (store *meetingMemoryStore) latestEntryIDOfKindForRoom(kind string, roomID string) string {
	if store == nil {
		return ""
	}
	roomID = strings.TrimSpace(roomID)

	store.mu.Lock()
	defer store.mu.Unlock()

	for index := len(store.entries) - 1; index >= 0; index-- {
		if store.entries[index].Kind != kind || memoryEntryHiddenFromRecall(store.entries[index]) {
			continue
		}
		if roomID != "" && normalizeRoomID(store.entries[index].Metadata["roomId"]) != roomID {
			continue
		}
		return store.entries[index].ID
	}

	return ""
}
