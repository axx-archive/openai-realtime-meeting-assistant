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
	"fmt"
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
	windowID     string
	attempts     int
	backoffUntil time.Time
}

type ambientAgentConfig struct {
	name              string
	defaultInterval   time.Duration
	intervalEnv       string // duration override; "0"/"off"/"false"/"disabled" turns the agent off
	disabledEnv       string // truthy disables the agent
	backfillEnv       string // truthy consumes history from the start at boot
	minBatchEnv       string
	defaultMinBatch   int
	maxBatchEnv       string
	defaultMaxBatch   int
	inputKind         string // memory kind the agent consumes
	artifactKind      string // memory kind the agent appends
	cursorMetadataKey string // artifact metadata key holding the consumed-through input id
	requestTimeout    time.Duration
	// nudgeMaxAge overrides defaultAmbientNudgeMaxAge for this agent's A3 nudge
	// staleness floor; zero uses the default.
	nudgeMaxAge time.Duration
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
	// is per-user and Anthropic-keyed, so it cannot ride this generic
	// OpenAI-keyed loop — instead it registers alongside the first ambient
	// agent of the boot (the brain worker, from JoinConferenceRoom), on its own
	// key gate. Keyless (no ANTHROPIC_API_KEY) it silently never starts.
	if agent.name != tasteAnalystAgentName {
		app.ensureTasteAnalystStarted()
		// The House-Style Distiller (house_style.go) rides the same seam: the
		// seventh instance, per-office and Anthropic-keyed, so it registers
		// alongside on its own key gate too. Keyless it silently never starts.
		app.ensureHouseStyleDistillerStarted()
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

	cancel := make(chan struct{})
	done := make(chan struct{})
	// The startup baseline registers under the OFFICE key (the bare agent
	// name); named rooms register lazily on first touch via
	// ensureAmbientAgentRoomBaseline so a room-scoped agent never backfills a
	// room's pre-boot history (W4 §7.4).
	baselineID := ""
	if !boolEnv(agent.backfillEnv) {
		baselineID = app.memory.latestEntryIDOfKindForRoom(agent.inputKind, agent.windowRoomID(officeRoomID))
	}

	app.mu.Lock()
	if app.agentCancels == nil {
		app.agentCancels = map[string]chan struct{}{}
		app.agentDones = map[string]chan struct{}{}
	}
	oldCancel := app.agentCancels[agent.name]
	oldDone := app.agentDones[agent.name]
	app.agentCancels[agent.name] = cancel
	app.agentDones[agent.name] = done
	app.setAmbientAgentBaselineIDLocked(agent.name, baselineID)
	app.mu.Unlock()

	if oldCancel != nil {
		close(oldCancel)
		if oldDone != nil {
			<-oldDone
		}
	}

	go app.runAmbientAgentLoop(agent, apiKey, interval, cancel, done)
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
// failure state by the outcome. The archive-flush path deliberately does NOT go
// through here — a close flush is a one-shot best-effort sweep, not a retrying
// ticker, so backoff/dead-letter would only get in its way.
func (app *kanbanBoardApp) fireAmbientAgentPass(agent ambientAgentConfig, apiKey string, minBatch int, roomID string) {
	if minBatch < 1 {
		minBatch = 1
	}
	roomID = normalizeRoomID(roomID)
	key := ambientAgentKey(agent.name, roomID)
	// §6.5 guests-only deferral: an unattended guest room accumulates input
	// (transcription continues) but spends no model budget until a member is
	// present or the close-flush chain runs its one bounded pass (which calls
	// runAmbientAgentOnceForRoom directly and is not deferred).
	if agent.defersWhenGuestsOnly && app.roomGuestsOnly(roomID) {
		return
	}
	headID, count, _, ok := app.peekUnconsumedWindow(agent, roomID)
	if !ok || count < minBatch {
		// Nothing ready at this floor: drop any stale failure record so a window
		// that drained (or was dead-lettered) does not keep a phantom backoff.
		app.clearAmbientAgentFailure(key)
		return
	}
	proceed, limit := app.ambientAgentAttemptBudget(agent, headID, roomID)
	if !proceed {
		return // still cooling down after a recent failure on this same window
	}
	ctx, cancelRequest := context.WithTimeout(context.Background(), agent.requestTimeout)
	_, err := app.runAmbientAgentOnceLimited(agent, ctx, apiKey, nil, minBatch, limit, roomID)
	cancelRequest()
	if err != nil {
		log.Errorf("%s worker failed: %v", agent.name, err)
		app.recordAmbientAgentFailure(agent, headID, roomID)
		return
	}
	app.clearAmbientAgentFailure(key)
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

	// One pass at a time per (agent, room): the cursor only advances when
	// produce appends its artifact at the end of a pass, so overlapping passes
	// (the ticker loop vs an archive flush, or two concurrent archives) would
	// consume — and apply — the same input batch twice. Per-room locks mean two
	// rooms' close flushes neither serialize nor deadlock (W4 §7.4). The
	// unconsumed window is read after the lock is held, so a waiting pass sees
	// the cursor the previous pass advanced.
	runLock := app.ambientAgentRunLock(ambientAgentKey(agent.name, roomID))
	runLock.Lock()
	defer runLock.Unlock()

	inputs := app.memory.unconsumedEntriesAfterForRoom(agent.inputKind, agent.artifactKind, agent.cursorMetadataKey, maxBatch, app.ambientAgentWindowBaseline(agent, roomID), agent.windowRoomID(roomID))
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
// registering it on first touch: a room-scoped agent meeting a room with
// pre-boot history baselines at that room's newest input (never backfills); a
// room born after boot has none and baselines at now. Office registration
// matches the legacy ensureAmbientAgentBaseline semantics.
func (app *kanbanBoardApp) ensureAmbientAgentRoomBaseline(agent ambientAgentConfig, roomID string) string {
	roomID = normalizeRoomID(roomID)
	key := ambientAgentKey(agent.name, roomID)

	app.mu.Lock()
	if baseline, registered := app.agentBaselineIDs[key]; registered {
		app.mu.Unlock()
		return baseline
	}
	app.mu.Unlock()

	baseline := ""
	if !boolEnv(agent.backfillEnv) {
		if windowRoom := agent.windowRoomID(roomID); windowRoom == "" {
			// company-global agent: the boot baseline spans every room.
			baseline = app.memory.bootBaselineIDOfKind(agent.inputKind)
		} else {
			baseline = app.memory.bootBaselineIDOfKindForRoom(agent.inputKind, windowRoom)
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
	inputs := app.memory.unconsumedEntriesAfterForRoom(agent.inputKind, agent.artifactKind, agent.cursorMetadataKey, limit, app.ambientAgentWindowBaseline(agent, roomID), agent.windowRoomID(roomID))
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
	fail := app.agentFailures[ambientAgentKey(agent.name, roomID)]
	if fail == nil || fail.windowID != headID {
		app.mu.Unlock()
		return true, full
	}
	attempts := fail.attempts
	backoffUntil := fail.backoffUntil
	app.mu.Unlock()

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
	key := ambientAgentKey(agent.name, roomID)
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
func (app *kanbanBoardApp) clearAmbientAgentFailure(name string) {
	app.mu.Lock()
	delete(app.agentFailures, name)
	app.mu.Unlock()
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
// the original archive four (brain summarizes the final transcript window,
// the decision ledger and board consume it, mission intel titles the RIGHT
// record before rotation), the narrative maintainer (axx/main: storyline
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
		meetingBoardAgent(),
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
		_, err := app.runAmbientAgentOnceForRoom(agent, passCtx, apiKey, responder, 1, roomID)
		cancelPass()
		if err != nil {
			log.Errorf("%s %s flush failed: %v", agent.name, seam, err)
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
	if store == nil || limit <= 0 {
		return nil
	}
	roomID = strings.TrimSpace(roomID)
	matchesRoom := func(entry meetingMemoryEntry) bool {
		return roomID == "" || normalizeRoomID(entry.Metadata["roomId"]) == roomID
	}

	store.mu.Lock()
	entries := cloneMemoryEntries(store.entries)
	store.mu.Unlock()

	startIndex := 0
	baselineID = strings.TrimSpace(baselineID)
	if baselineID != "" {
		for index := len(entries) - 1; index >= 0; index-- {
			if entries[index].ID == baselineID {
				startIndex = index + 1
				break
			}
		}
	}
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if entry.Kind != artifactKind || !matchesRoom(entry) {
			continue
		}
		cursorID := strings.TrimSpace(entry.Metadata[cursorKey])
		if cursorID != "" {
			for inputIndex := len(entries) - 1; inputIndex >= 0; inputIndex-- {
				if entries[inputIndex].ID == cursorID {
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
		if entry.Kind != inputKind || !matchesRoom(entry) {
			continue
		}
		inputs = append(inputs, entry)
		if len(inputs) >= limit {
			break
		}
	}

	return inputs
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
		if store.entries[index].Kind != kind {
			continue
		}
		if roomID != "" && normalizeRoomID(store.entries[index].Metadata["roomId"]) != roomID {
			continue
		}
		return store.entries[index].ID
	}

	return ""
}
