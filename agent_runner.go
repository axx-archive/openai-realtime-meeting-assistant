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
	"os"
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
	produce     func(app *kanbanBoardApp, ctx context.Context, apiKey string, inputs []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error)
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
	}
	interval := agent.interval()
	if interval <= 0 {
		return
	}

	cancel := make(chan struct{})
	done := make(chan struct{})
	baselineID := ""
	if !boolEnv(agent.backfillEnv) {
		baselineID = app.memory.latestEntryIDOfKind(agent.inputKind)
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

	// A3 debounce timer: when a nudge finds inputs queued but still short of
	// minBatch AND younger than the staleness floor, the loop arms this one-shot
	// for exactly when the oldest input crosses nudgeAge. Nudges are edge-
	// triggered on append, so a room that then falls silent sends no further
	// wake — this timer is what still brains the trailing short exchange.
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

	// evaluate runs on every nudge / stale-timer wake. It only reads the
	// in-memory window (no model call) until it decides to fire: a full pass the
	// moment minBatch has accumulated, a short pass once the oldest input is
	// stale, else it arms the timer for the remaining wait. Cheap and idempotent,
	// so a burst of coalesced nudges cannot spin the model.
	evaluate := func() {
		stopStale()
		_, count, oldest, ok := app.peekUnconsumedWindow(agent)
		if !ok {
			return
		}
		if count >= agent.minBatch() {
			app.fireAmbientAgentPass(agent, apiKey, agent.minBatch())
			return
		}
		if oldest >= agent.nudgeAge() {
			app.fireAmbientAgentPass(agent, apiKey, ambientNudgeShortBatch)
			return
		}
		stale = time.NewTimer(agent.nudgeAge() - oldest)
		staleC = stale.C
	}

	for {
		select {
		case <-ticker.C:
			stopStale()
			app.fireAmbientAgentPass(agent, apiKey, agent.minBatch())
		case <-nudge:
			evaluate()
		case <-staleC:
			stale = nil
			staleC = nil
			evaluate()
		case <-cancel:
			return
		}
	}
}

// fireAmbientAgentPass runs one guarded ticker/nudge pass: it peeks the window
// to key A8 backoff off a stable boundary, honors any active backoff, halves the
// batch on retries, runs under the per-agent run lock, and records or clears the
// failure state by the outcome. The archive-flush path deliberately does NOT go
// through here — a close flush is a one-shot best-effort sweep, not a retrying
// ticker, so backoff/dead-letter would only get in its way.
func (app *kanbanBoardApp) fireAmbientAgentPass(agent ambientAgentConfig, apiKey string, minBatch int) {
	if minBatch < 1 {
		minBatch = 1
	}
	headID, count, _, ok := app.peekUnconsumedWindow(agent)
	if !ok || count < minBatch {
		// Nothing ready at this floor: drop any stale failure record so a window
		// that drained (or was dead-lettered) does not keep a phantom backoff.
		app.clearAmbientAgentFailure(agent.name)
		return
	}
	proceed, limit := app.ambientAgentAttemptBudget(agent, headID)
	if !proceed {
		return // still cooling down after a recent failure on this same window
	}
	ctx, cancelRequest := context.WithTimeout(context.Background(), agent.requestTimeout)
	_, err := app.runAmbientAgentOnceLimited(agent, ctx, apiKey, nil, minBatch, limit)
	cancelRequest()
	if err != nil {
		log.Errorf("%s worker failed: %v", agent.name, err)
		app.recordAmbientAgentFailure(agent, headID)
		return
	}
	app.clearAmbientAgentFailure(agent.name)
}

func (app *kanbanBoardApp) runAmbientAgentOnce(agent ambientAgentConfig, ctx context.Context, apiKey string, responder openAITextResponder, minBatch int) (meetingMemoryEntry, error) {
	return app.runAmbientAgentOnceLimited(agent, ctx, apiKey, responder, minBatch, agent.maxBatch())
}

// runAmbientAgentOnceLimited is runAmbientAgentOnce with an explicit batch
// ceiling so the A8 retry path can HALVE the window on a failing pass (shrinking
// the blast radius of a poison entry) without touching the agent's configured
// maxBatch. maxBatch <= 0 (or above the configured ceiling) falls back to the
// configured maxBatch.
func (app *kanbanBoardApp) runAmbientAgentOnceLimited(agent ambientAgentConfig, ctx context.Context, apiKey string, responder openAITextResponder, minBatch int, maxBatch int) (meetingMemoryEntry, error) {
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

	// One pass at a time per agent: the cursor only advances when produce
	// appends its artifact at the end of a pass, so overlapping passes (the
	// ticker loop vs an archive flush, or two concurrent archives) would
	// consume — and apply — the same input batch twice. The unconsumed window
	// is read after the lock is held, so a waiting pass sees the cursor the
	// previous pass advanced.
	runLock := app.ambientAgentRunLock(agent.name)
	runLock.Lock()
	defer runLock.Unlock()

	inputs := app.memory.unconsumedEntriesAfter(agent.inputKind, agent.artifactKind, agent.cursorMetadataKey, maxBatch, app.ambientAgentBaselineID(agent.name))
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

// nudgeAmbientAgent (A3) wakes an agent's runner so it re-evaluates its window
// immediately instead of waiting for the next safety-floor tick. Non-blocking:
// a full buffer already carries a pending wake, so a burst collapses to one
// pass. Safe for an agent that never started (keyless / disabled) — the single
// buffered slot absorbs the send with no receiver ever draining it.
func (app *kanbanBoardApp) nudgeAmbientAgent(name string) {
	if app == nil {
		return
	}
	select {
	case app.ambientAgentNudgeChannel(name) <- struct{}{}:
	default:
	}
}

// peekUnconsumedWindow reports the oldest unconsumed input's id (the stable A8
// backoff key), how many inputs are queued (capped at minBatch — enough to know
// the batch is ready), and how long the oldest has waited, all WITHOUT advancing
// any cursor. The A3 nudge path uses it to choose between firing now and arming
// the staleness timer, and fireAmbientAgentPass uses the head id to key retries.
func (app *kanbanBoardApp) peekUnconsumedWindow(agent ambientAgentConfig) (headID string, count int, oldestAge time.Duration, ok bool) {
	if app == nil || app.memory == nil {
		return "", 0, 0, false
	}
	limit := agent.minBatch()
	if limit < 1 {
		limit = 1
	}
	inputs := app.memory.unconsumedEntriesAfter(agent.inputKind, agent.artifactKind, agent.cursorMetadataKey, limit, app.ambientAgentBaselineID(agent.name))
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
func (app *kanbanBoardApp) ambientAgentAttemptBudget(agent ambientAgentConfig, headID string) (bool, int) {
	full := agent.maxBatch()

	app.mu.Lock()
	fail := app.agentFailures[agent.name]
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
func (app *kanbanBoardApp) recordAmbientAgentFailure(agent ambientAgentConfig, headID string) {
	app.mu.Lock()
	if app.agentFailures == nil {
		app.agentFailures = map[string]*ambientAgentFailure{}
	}
	fail := app.agentFailures[agent.name]
	if fail == nil || fail.windowID != headID {
		fail = &ambientAgentFailure{windowID: headID}
		app.agentFailures[agent.name] = fail
	}
	fail.attempts++
	attempts := fail.attempts
	deadLetter := attempts >= ambientAgentMaxWindowAttempts
	if deadLetter {
		delete(app.agentFailures, agent.name)
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
		app.setAmbientAgentBaselineID(agent.name, headID)
		log.Errorf("%s worker dead-lettered input %s after %d failed attempts; advancing the baseline past it", agent.name, headID, attempts)
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
// history persisted before this process started.
func (app *kanbanBoardApp) ensureAmbientAgentBaseline(agent ambientAgentConfig) {
	app.mu.Lock()
	defer app.mu.Unlock()

	if _, registered := app.agentBaselineIDs[agent.name]; registered {
		return
	}
	baselineID := ""
	if !boolEnv(agent.backfillEnv) {
		baselineID = app.memory.bootBaselineIDOfKind(agent.inputKind)
	}
	app.setAmbientAgentBaselineIDLocked(agent.name, baselineID)
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
func (app *kanbanBoardApp) flushAmbientAgentsForArchive() {
	app.flushAmbientAgentsForClose("archive")
}

// flushAmbientAgentsForClose is the shared boundary flush for BOTH meeting
// close seams — explicit archive and idle end (the Track-2 idle-close hole:
// that path previously wrote no final rollup at all, so idle-closed meetings
// never got a digest and "what did I miss" silently skipped them). Bounded by
// meetingArchiveFlushTimeout and best-effort throughout: every failure only
// logs, the caller always proceeds.
func (app *kanbanBoardApp) flushAmbientAgentsForClose(seam string) {
	if app == nil || app.memory == nil {
		return
	}
	app.mu.Lock()
	apiKey := app.apiKey
	app.mu.Unlock()
	if strings.TrimSpace(apiKey) == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), meetingArchiveFlushTimeout)
	defer cancel()
	for _, agent := range closeFlushChain() {
		// honor both disable forms (interval=0/off/false/disabled and the
		// _DISABLED env): a turned-off agent must not run at close time.
		if boolEnv(agent.disabledEnv) || agent.interval() <= 0 {
			continue
		}
		// A8: the overall ceiling is a backstop, not a per-call budget — once it
		// is spent, stop rather than spin failing every remaining pass.
		if ctx.Err() != nil {
			log.Errorf("%s flush reached the overall %s ceiling; skipping the remaining passes", seam, meetingArchiveFlushTimeout)
			break
		}
		app.ensureAmbientAgentBaseline(agent)
		// A8: each pass gets its OWN deadline (bounded by whatever remains of the
		// overall ceiling) so a slow upstream pass can no longer starve the
		// mission / narrative / digest passes queued behind it.
		passTimeout := agent.requestTimeout
		if passTimeout <= 0 || passTimeout > meetingArchiveFlushPassTimeout {
			passTimeout = meetingArchiveFlushPassTimeout
		}
		passCtx, cancelPass := context.WithTimeout(ctx, passTimeout)
		_, err := app.runAmbientAgentOnce(agent, passCtx, apiKey, nil, 1)
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
	if store == nil || limit <= 0 {
		return nil
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
		if entry.Kind != artifactKind {
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
		if entry.Kind != inputKind {
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
	if store == nil {
		return ""
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	for index := len(store.entries) - 1; index >= 0; index-- {
		if store.entries[index].Kind == kind {
			return store.entries[index].ID
		}
	}

	return ""
}
