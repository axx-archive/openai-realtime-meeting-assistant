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
	"time"
)

const meetingArchiveFlushTimeout = 2 * time.Minute

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

// flushAmbientAgentsForArchive synchronously runs one brain pass then one
// board pass with a batch minimum of one, so the last minutes of a meeting
// are summarized and applied to the board before the archive snapshot is
// taken. Skips silently when no API key is configured or nothing new exists.
func (app *kanbanBoardApp) flushAmbientAgentsForArchive() {
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
	for _, agent := range []ambientAgentConfig{meetingBrainAgent(), meetingBoardAgent()} {
		if boolEnv(agent.disabledEnv) {
			continue
		}
		if _, err := app.runAmbientAgentOnce(agent, ctx, apiKey, nil, 1); err != nil {
			log.Errorf("%s archive flush failed: %v", agent.name, err)
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
