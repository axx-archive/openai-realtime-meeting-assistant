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

// meetingArchiveFlushTimeout bounds the WHOLE close-time flush chain (up to
// eight sequential agent passes: brain → decision ledger → board → mission
// intel → meeting digest → day digest → entity ledger → company digest). It is
// a ceiling, not a target — agents with nothing unconsumed skip without a
// model call, and an expired context only fails the remaining passes (their
// tickers retry later); the caller always proceeds afterwards (archive
// snapshot / idle rotation), so liveness never depends on the model.
const meetingArchiveFlushTimeout = 4 * time.Minute

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
	produce           func(app *kanbanBoardApp, ctx context.Context, apiKey string, inputs []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error)
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

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ctx, cancelRequest := context.WithTimeout(context.Background(), agent.requestTimeout)
			if _, err := app.runAmbientAgentOnce(agent, ctx, apiKey, nil, agent.minBatch()); err != nil {
				log.Errorf("%s worker failed: %v", agent.name, err)
			}
			cancelRequest()
		case <-cancel:
			return
		}
	}
}

func (app *kanbanBoardApp) runAmbientAgentOnce(agent ambientAgentConfig, ctx context.Context, apiKey string, responder openAITextResponder, minBatch int) (meetingMemoryEntry, error) {
	if app == nil || app.memory == nil {
		return meetingMemoryEntry{}, nil
	}
	if responder == nil {
		responder = createOpenAITextResponse
	}
	if minBatch < 1 {
		minBatch = 1
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

	inputs := app.memory.unconsumedEntriesAfter(agent.inputKind, agent.artifactKind, agent.cursorMetadataKey, agent.maxBatch(), app.ambientAgentBaselineID(agent.name))
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
// record before rotation), then the Track-2 rollup tiers — meeting digest
// (consumes the fresh brains: the closing meeting's cumulative T2 digest),
// day digest (folds the fresh meeting digests into the local-day T3 slices),
// entity ledger (consolidates the digest's facts plus new decision rows into
// the canonical registry), and company digest (refreshes T4 from the fresh
// ledger deltas). Every stage is cursor-gated and upsert-idempotent, so a
// double flush (archive racing idle, or a flush racing the ticker) is safe.
func closeFlushChain() []ambientAgentConfig {
	return []ambientAgentConfig{
		meetingBrainAgent(),
		decisionLedgerAgent(),
		meetingBoardAgent(),
		missionIntelligenceAgent(),
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
		app.ensureAmbientAgentBaseline(agent)
		if _, err := app.runAmbientAgentOnce(agent, ctx, apiKey, nil, 1); err != nil {
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
