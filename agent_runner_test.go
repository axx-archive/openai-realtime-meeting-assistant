package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestAmbientAgent(produced *[][]string) ambientAgentConfig {
	artifactIndex := 0
	return ambientAgentConfig{
		name:              "test agent",
		defaultInterval:   time.Minute,
		intervalEnv:       "TEST_AGENT_INTERVAL",
		disabledEnv:       "TEST_AGENT_DISABLED",
		backfillEnv:       "TEST_AGENT_BACKFILL",
		minBatchEnv:       "TEST_AGENT_MIN",
		defaultMinBatch:   2,
		maxBatchEnv:       "TEST_AGENT_MAX",
		defaultMaxBatch:   3,
		inputKind:         meetingMemoryKindTranscript,
		artifactKind:      "test_artifact",
		cursorMetadataKey: "throughTestId",
		requestTimeout:    time.Second,
		produce: func(app *kanbanBoardApp, _ context.Context, _ string, inputs []meetingMemoryEntry, _ openAITextResponder) (meetingMemoryEntry, error) {
			ids := make([]string, 0, len(inputs))
			for _, input := range inputs {
				ids = append(ids, input.ID)
			}
			*produced = append(*produced, ids)
			artifactIndex++
			entry, _, err := app.memory.appendEntry("test_artifact", fmt.Sprintf("test-artifact-%d", artifactIndex), "test artifact", map[string]string{
				"throughTestId": inputs[len(inputs)-1].ID,
			})
			return entry, err
		},
	}
}

func appendTestTranscript(t *testing.T, app *kanbanBoardApp, id string, text string) {
	t.Helper()
	if _, appended, err := app.memory.appendAttributedTranscript(id, id, "Tom", "dominant", text); err != nil {
		t.Fatalf("append transcript %s: %v", id, err)
	} else if !appended {
		t.Fatalf("transcript %s appended=false, want true", id)
	}
}

func TestUnconsumedCursorNeverRewindsWhenLateOldMeetingArtifactAppends(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	appendTestTranscript(t, app, "cursor-tx-1", "first source")
	appendTestTranscript(t, app, "cursor-tx-2", "second source")
	meetingID := app.memory.currentMeetingID(officeRoomID)
	if _, appended, err := app.memory.appendBrainWriteUp("cursor-brain-through-two", "covers both", map[string]string{
		"meetingId": meetingID, meetingBrainCursorMetadataKey: "cursor-tx-2",
	}); err != nil || !appended {
		t.Fatalf("append current cursor artifact=%v err=%v", appended, err)
	}
	// A delayed recovery for an older source may append physically later. It
	// must never become the room cursor merely because it is the newest row.
	if _, appended, err := app.memory.appendBrainWriteUp("cursor-brain-late-old", "late old output", map[string]string{
		"meetingId": meetingID, meetingBrainCursorMetadataKey: "cursor-tx-1",
	}); err != nil || !appended {
		t.Fatalf("append late cursor artifact=%v err=%v", appended, err)
	}
	remaining := app.memory.unconsumedEntriesAfterForRoomForPrincipal(
		meetingMemoryKindTranscript, meetingMemoryKindBrain, meetingBrainCursorMetadataKey, 10, "", officeRoomID, RecallPrincipal{},
	)
	if len(remaining) != 0 {
		t.Fatalf("late old artifact rewound cursor; remaining=%+v", remaining)
	}
}

func cannedArchiveMeetingDigestJSON(anchor string) string {
	anchor = strings.TrimSpace(anchor)
	return strings.ReplaceAll(strings.ReplaceAll(cannedMeetingDigestJSON(), `"tx-1"`, strconv.Quote(anchor)), `"tx-2"`, strconv.Quote(anchor))
}

func expireAmbientAgentBackoffForTest(app *kanbanBoardApp, key string) {
	app.mu.Lock()
	if failure := app.agentFailures[key]; failure != nil && !failure.providerOpen {
		failure.backoffUntil = time.Now().Add(-time.Second)
	}
	app.mu.Unlock()
}

func setAmbientAgentFailureForTest(app *kanbanBoardApp, key string, failure *ambientAgentFailure) {
	app.mu.Lock()
	if app.agentFailures == nil {
		app.agentFailures = map[string]*ambientAgentFailure{}
	}
	app.agentFailures[key] = failure
	app.mu.Unlock()
}

func newHeldWindowTestAgent(name string, roomScoped bool, observed *[][]string) ambientAgentConfig {
	artifactKind := "held_window_artifact_" + strings.ReplaceAll(name, " ", "_")
	return ambientAgentConfig{
		name: name, defaultInterval: time.Hour,
		intervalEnv: "HELD_WINDOW_INTERVAL", disabledEnv: "HELD_WINDOW_DISABLED", backfillEnv: "HELD_WINDOW_BACKFILL",
		minBatchEnv: "HELD_WINDOW_MIN", defaultMinBatch: 1, maxBatchEnv: "HELD_WINDOW_MAX", defaultMaxBatch: 8,
		inputKind: meetingMemoryKindBrain, artifactKind: artifactKind, cursorMetadataKey: "throughBrainId", requestTimeout: time.Second,
		roomScoped: roomScoped,
		produce: func(app *kanbanBoardApp, _ context.Context, _ string, inputs []meetingMemoryEntry, _ openAITextResponder) (meetingMemoryEntry, error) {
			ids := make([]string, 0, len(inputs))
			for _, input := range inputs {
				ids = append(ids, input.ID)
			}
			*observed = append(*observed, ids)
			roomID := ambientWindowRoomID(inputs)
			entry, _, err := app.memory.appendEntry(artifactKind, durableTimestampID("held-window", time.Now()), "held window consumed", map[string]string{
				"roomId": roomID, "throughBrainId": inputs[len(inputs)-1].ID,
			})
			return entry, err
		},
	}
}

func appendHeldWindowBrain(t *testing.T, app *kanbanBoardApp, id, roomID string) {
	t.Helper()
	metadata := map[string]string{"visibility": "organization"}
	if normalizeRoomID(roomID) != officeRoomID {
		metadata["roomId"] = normalizeRoomID(roomID)
	}
	if _, appended, err := app.memory.appendBrainWriteUp(id, "## Overview\nDurable held input "+id+".", metadata); err != nil || !appended {
		t.Fatalf("append %s: appended=%v err=%v", id, appended, err)
	}
}

func TestAmbientAgentRunnerCursorAndBatchDispatch(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	var produced [][]string
	agent := newTestAmbientAgent(&produced)

	appendTestTranscript(t, app, "input-1", "Boot Barn kickoff planning notes.")
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", nil, agent.minBatch()); err != nil {
		t.Fatalf("runAmbientAgentOnce below min batch: %v", err)
	}
	if len(produced) != 0 {
		t.Fatalf("produced=%v, want no dispatch below the min batch", produced)
	}

	appendTestTranscript(t, app, "input-2", "Boot Barn follow-up commitments.")
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", nil, agent.minBatch()); err != nil {
		t.Fatalf("runAmbientAgentOnce at min batch: %v", err)
	}
	if len(produced) != 1 || strings.Join(produced[0], ",") != "input-1,input-2" {
		t.Fatalf("produced=%v, want one batch of input-1,input-2", produced)
	}

	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", nil, 1); err != nil {
		t.Fatalf("runAmbientAgentOnce with consumed inputs: %v", err)
	}
	if len(produced) != 1 {
		t.Fatalf("produced=%v, want no dispatch after the cursor consumed everything", produced)
	}

	for index := 3; index <= 6; index++ {
		appendTestTranscript(t, app, fmt.Sprintf("input-%d", index), fmt.Sprintf("Boot Barn detail number %d.", index))
	}
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", nil, 1); err != nil {
		t.Fatalf("runAmbientAgentOnce above max batch: %v", err)
	}
	if len(produced) != 2 || strings.Join(produced[1], ",") != "input-3,input-4,input-5" {
		t.Fatalf("produced=%v, want a max-capped batch resuming after the cursor", produced)
	}
}

// A3: a burst of nudges before any receiver drains coalesces to a single
// buffered wake and never blocks (debounce), and firing for an unstarted agent
// is safe.
func TestNudgeAmbientAgentDebouncesAndIsNonBlocking(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	for i := 0; i < 200; i++ {
		app.nudgeAmbientAgent("test agent")
	}
	ch := app.ambientAgentNudgeChannel("test agent")
	select {
	case <-ch:
	default:
		t.Fatal("expected one buffered wake after a burst of nudges")
	}
	select {
	case <-ch:
		t.Fatal("expected the burst to coalesce to a single wake")
	default:
	}
}

// A3: peekUnconsumedWindow reports the oldest-input id, the (min-batch-capped)
// count, and the oldest input's age without advancing any cursor.
func TestPeekUnconsumedWindow(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	var produced [][]string
	agent := newTestAmbientAgent(&produced) // minBatch 2, maxBatch 3

	if _, _, _, ok := app.peekUnconsumedWindow(agent, officeRoomID); ok {
		t.Fatal("empty store should report no window")
	}
	appendTestTranscript(t, app, "peek-1", "Boot Barn kickoff.")
	head, count, age, ok := app.peekUnconsumedWindow(agent, officeRoomID)
	if !ok || head != "peek-1" || count != 1 {
		t.Fatalf("peek=%q/%d/%v, want peek-1/1/true", head, count, ok)
	}
	if age < 0 {
		t.Fatalf("age=%v, want >= 0", age)
	}
	// The peek must not consume: a real pass still sees the input.
	if _, _, _, ok := app.peekUnconsumedWindow(agent, officeRoomID); !ok {
		t.Fatal("peek must not advance the cursor")
	}
}

// nudgeCadenceAgent is a fast, model-free agent whose produce signals a channel,
// used to observe the A3 event-driven loop firing between safety-floor ticks.
func nudgeCadenceAgent(name string, minBatch int, nudgeMaxAge time.Duration, fired chan []string) ambientAgentConfig {
	artifact := name + "_artifact"
	index := 0
	return ambientAgentConfig{
		name:              name,
		defaultInterval:   time.Hour, // floor far away; only a nudge/stale-timer should fire it
		intervalEnv:       "NUDGE_CADENCE_INTERVAL",
		disabledEnv:       "NUDGE_CADENCE_DISABLED",
		backfillEnv:       "NUDGE_CADENCE_BACKFILL",
		minBatchEnv:       "NUDGE_CADENCE_MIN",
		defaultMinBatch:   minBatch,
		maxBatchEnv:       "NUDGE_CADENCE_MAX",
		defaultMaxBatch:   5,
		inputKind:         meetingMemoryKindTranscript,
		artifactKind:      artifact,
		cursorMetadataKey: "throughCadenceId",
		requestTimeout:    time.Second,
		nudgeMaxAge:       nudgeMaxAge,
		produce: func(app *kanbanBoardApp, _ context.Context, _ string, inputs []meetingMemoryEntry, _ openAITextResponder) (meetingMemoryEntry, error) {
			ids := make([]string, 0, len(inputs))
			for _, in := range inputs {
				ids = append(ids, in.ID)
			}
			index++
			entry, _, err := app.memory.appendEntry(artifact, fmt.Sprintf("%s-art-%d", name, index), "x", map[string]string{
				"throughCadenceId": inputs[len(inputs)-1].ID,
			})
			fired <- ids
			return entry, err
		},
	}
}

// A3: a nudge fires a full pass the moment minBatch is ready, without waiting
// for the far-off safety-floor tick.
func TestAmbientAgentLoopFiresOnNudge(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	fired := make(chan []string, 4)
	agent := nudgeCadenceAgent("nudge-fire", 1, 0, fired)

	cancel := make(chan struct{})
	done := make(chan struct{})
	go app.runAmbientAgentLoop(agent, "test-key", time.Hour, cancel, done)
	defer func() { close(cancel); <-done }()

	appendTestTranscript(t, app, "nf-1", "Boot Barn kickoff.")
	app.nudgeAmbientAgent(agent.name)
	select {
	case ids := <-fired:
		if strings.Join(ids, ",") != "nf-1" {
			t.Fatalf("fired=%v, want nf-1", ids)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nudge did not fire a pass before the safety-floor tick")
	}
}

// A3: a lone input short of minBatch is not left dark — the staleness timer
// fires a short pass once the oldest input crosses nudgeMaxAge, even though no
// further nudge arrives (edge-triggered appends went silent).
func TestAmbientAgentLoopFiresOnStaleness(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	fired := make(chan []string, 4)
	agent := nudgeCadenceAgent("nudge-stale", 3, 40*time.Millisecond, fired)

	cancel := make(chan struct{})
	done := make(chan struct{})
	go app.runAmbientAgentLoop(agent, "test-key", time.Hour, cancel, done)
	defer func() { close(cancel); <-done }()

	appendTestTranscript(t, app, "ns-1", "Boot Barn lone remark.")
	app.nudgeAmbientAgent(agent.name) // below minBatch(3): arms the staleness timer
	select {
	case ids := <-fired:
		if strings.Join(ids, ",") != "ns-1" {
			t.Fatalf("fired=%v, want the lone ns-1 brained on staleness", ids)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("staleness timer did not fire a short pass for the lone input")
	}
}

// A8: consecutive failures on the SAME window back off (no immediate retry),
// halve the batch each attempt, and finally dead-letter the poison head by
// advancing the baseline past it.
func TestAmbientAgentFailureBackoffHalvingAndDeadLetter(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	var produced [][]string
	agent := newTestAmbientAgent(&produced) // minBatch 2, maxBatch 3
	for i := 1; i <= 3; i++ {
		appendTestTranscript(t, app, fmt.Sprintf("fail-%d", i), fmt.Sprintf("Boot Barn detail %d.", i))
	}
	head, _, _, ok := app.peekUnconsumedWindow(agent, officeRoomID)
	if !ok || head != "fail-1" {
		t.Fatalf("peek head=%q ok=%v, want fail-1", head, ok)
	}

	// Fresh window: full batch, may proceed.
	if proceed, limit := app.ambientAgentAttemptBudget(agent, head, officeRoomID); !proceed || limit != agent.maxBatch() {
		t.Fatalf("fresh budget=%v/%d, want true/%d", proceed, limit, agent.maxBatch())
	}

	// One failure arms a backoff that holds off the immediate retry.
	app.recordAmbientAgentFailure(agent, head, officeRoomID)
	if proceed, _ := app.ambientAgentAttemptBudget(agent, head, officeRoomID); proceed {
		t.Fatal("expected an armed backoff to hold off the immediate retry")
	}

	// Past the backoff, one prior attempt halves the batch (maxBatch 3 -> 2).
	app.mu.Lock()
	app.agentFailures[agent.name] = &ambientAgentFailure{windowID: head, attempts: 1, backoffUntil: time.Now().Add(-time.Second)}
	app.mu.Unlock()
	if proceed, limit := app.ambientAgentAttemptBudget(agent, head, officeRoomID); !proceed || limit != 2 {
		t.Fatalf("halved budget=%v/%d, want true/2", proceed, limit)
	}

	// Reaching the attempt cap dead-letters the head: baseline advances past it
	// and the failure record clears. Start from a clean slate so exactly
	// ambientAgentMaxWindowAttempts failures accrue (a further failure would
	// re-open a fresh record on the now-skipped head).
	app.clearAmbientAgentFailure(agent.name)
	for i := 0; i < ambientAgentMaxWindowAttempts; i++ {
		app.recordAmbientAgentFailure(agent, head, officeRoomID)
	}
	if base := app.ambientAgentBaselineID(agent.name); base != head {
		t.Fatalf("baseline=%q, want dead-letter advance to %q", base, head)
	}
	app.mu.Lock()
	_, stillTracked := app.agentFailures[agent.name]
	app.mu.Unlock()
	if stillTracked {
		t.Fatal("dead-lettered window should clear its failure record")
	}
	if newHead, _, _, ok := app.peekUnconsumedWindow(agent, officeRoomID); !ok || newHead == head {
		t.Fatalf("post-dead-letter head=%q ok=%v, want the poison head skipped", newHead, ok)
	}
}

func TestAmbientAgentProviderFailureHoldsEveryProducerCursor(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	appendTestTranscript(t, app, "provider-held-transcript", "This raw transcript must survive a provider outage.")
	var produced [][]string
	agent := newTestAmbientAgent(&produced)
	agent.defaultMinBatch = 1
	agent.produce = func(*kanbanBoardApp, context.Context, string, []meetingMemoryEntry, openAITextResponder) (meetingMemoryEntry, error) {
		return meetingMemoryEntry{}, &openAIProviderFailure{err: fmt.Errorf("provider unavailable")}
	}
	key := ambientAgentKey(agent.name, officeRoomID)
	for attempt := 0; attempt < ambientAgentMaxWindowAttempts+2; attempt++ {
		app.fireAmbientAgentPass(agent, "test-key", 1, officeRoomID)
		app.mu.Lock()
		if failure := app.agentFailures[key]; failure != nil {
			failure.backoffUntil = time.Now().Add(-time.Second)
		}
		app.mu.Unlock()
	}
	if baseline := app.ambientAgentBaselineID(key); baseline == "provider-held-transcript" {
		t.Fatalf("provider outage advanced shared ambient baseline: %q", baseline)
	}
	if deadLetters := app.memory.entriesOfKind(meetingMemoryKindDeadLetter, 0); len(deadLetters) != 0 {
		t.Fatalf("provider outage dead-lettered non-digest input: %+v", deadLetters)
	}
	app.mu.Lock()
	failure := app.agentFailures[key]
	app.mu.Unlock()
	if failure == nil || !failure.providerOpen || failure.attempts != ambientProviderMaxWindowAttempts {
		t.Fatalf("provider circuit=%+v, want open after %d attempts", failure, ambientProviderMaxWindowAttempts)
	}
	if proceed, limit := app.ambientAgentAttemptBudget(agent, "provider-held-transcript", officeRoomID); proceed || limit != 0 {
		t.Fatalf("open provider circuit budget=%v/%d, want false/0", proceed, limit)
	}
	priorApp := kanbanApp
	kanbanApp = app
	evidence := ambientCapabilityEvidence(capabilityBrain, agent, time.Now().UTC())
	kanbanApp = priorApp
	if evidence["circuit"] != "open" || evidence["retrySuppressed"] != true || evidence["retryAttempts"] != ambientProviderMaxWindowAttempts {
		t.Fatalf("provider circuit evidence=%v", evidence)
	}
}

func TestAmbientRejectedOutputOpensBoundedCircuitForDurableBrainLanes(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	for _, test := range []struct {
		name  string
		agent ambientAgentConfig
	}{
		{name: "meeting board", agent: meetingBoardAgent()},
		{name: "research suggestion", agent: researchSuggestionAgent()},
		{name: "mission intelligence", agent: missionIntelligenceAgent()},
		{name: "decision ledger", agent: decisionLedgerAgent()},
		{name: "narrative maintainer", agent: narrativeMaintainerAgent()},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := newIsolatedKanbanBoardApp(t)
			inputID := "brain-rejected-" + strings.ReplaceAll(test.name, " ", "-")
			if _, appended, err := app.memory.appendBrainWriteUp(inputID, "## Overview\nThis raw company fact must survive rejected model output.", map[string]string{"visibility": "organization"}); err != nil || !appended {
				t.Fatalf("append brain: appended=%v err=%v", appended, err)
			}

			calls := 0
			responder := func(context.Context, string, openAITextRequest) (string, error) {
				calls++
				return "not json", nil
			}
			key := ambientAgentKey(test.agent.name, officeRoomID)
			for attempt := 0; attempt < ambientProviderMaxWindowAttempts; attempt++ {
				_, err := app.invokeAmbientAgentGuarded(test.agent, context.Background(), "test-key", responder, 1, officeRoomID)
				if !isAmbientAgentHoldError(err) && !isProviderOutputRejection(err) {
					t.Fatalf("attempt %d error=%v, want provider-output/cursor-holding rejection", attempt+1, err)
				}
				expireAmbientAgentBackoffForTest(app, key)
			}
			if calls != ambientProviderMaxWindowAttempts {
				t.Fatalf("wire calls=%d, want %d", calls, ambientProviderMaxWindowAttempts)
			}
			app.mu.Lock()
			failure := app.agentFailures[key]
			app.mu.Unlock()
			if failure == nil || !failure.providerOpen || failure.attempts != ambientProviderMaxWindowAttempts {
				t.Fatalf("circuit=%+v, want bounded open circuit", failure)
			}
			if baseline := app.ambientAgentBaselineID(key); baseline == inputID {
				t.Fatalf("rejected output advanced raw cursor to %q", baseline)
			}
			if deadLetters := app.memory.entriesOfKind(meetingMemoryKindDeadLetter, 0); len(deadLetters) != 0 {
				t.Fatalf("rejected output created dead letters: %+v", deadLetters)
			}
			_, err := app.invokeAmbientAgentGuarded(test.agent, context.Background(), "test-key", responder, 1, officeRoomID)
			var circuitErr *ambientAgentCircuitOpenError
			if !errors.As(err, &circuitErr) || !circuitErr.RestartRequired {
				t.Fatalf("open-circuit error=%v, want restart required", err)
			}
			if calls != ambientProviderMaxWindowAttempts {
				t.Fatalf("open circuit made another wire call: %d", calls)
			}
		})
	}
}

func TestStartAmbientAgentClearsCircuitOnlyAfterPriorSupervisorExits(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	app := newIsolatedKanbanBoardApp(t)
	var produced [][]string
	agent := newTestAmbientAgent(&produced)

	oldCancel := make(chan struct{})
	oldDone := make(chan struct{})
	oldCancelObserved := make(chan struct{})
	releaseOldExit := make(chan struct{})
	go func() {
		<-oldCancel
		close(oldCancelObserved)
		<-releaseOldExit
		close(oldDone)
	}()
	app.mu.Lock()
	app.agentCancels = map[string]chan struct{}{}
	app.agentDones = map[string]chan struct{}{}
	if app.agentFailures == nil {
		app.agentFailures = map[string]*ambientAgentFailure{}
	}
	app.agentCancels[agent.name] = oldCancel
	app.agentDones[agent.name] = oldDone
	app.agentFailures[agent.name] = &ambientAgentFailure{windowID: "held", attempts: ambientProviderMaxWindowAttempts, providerOpen: true}
	app.mu.Unlock()

	restarted := make(chan struct{})
	go func() {
		app.startAmbientAgent(agent, "test-key")
		close(restarted)
	}()
	select {
	case <-oldCancelObserved:
	case <-time.After(2 * time.Second):
		t.Fatal("restart never asked the prior supervisor to exit")
	}

	app.mu.Lock()
	stillOpen := app.agentFailures[agent.name]
	stillOld := app.agentCancels[agent.name] == oldCancel
	app.mu.Unlock()
	if stillOpen == nil || !stillOpen.providerOpen || !stillOld {
		t.Fatalf("restart reset state before old exit: failure=%+v stillOld=%v", stillOpen, stillOld)
	}
	select {
	case <-restarted:
		t.Fatal("restart returned before the prior supervisor exited")
	default:
	}

	close(releaseOldExit)
	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("restart did not install the successor after prior exit")
	}
	app.mu.Lock()
	_, stillFailed := app.agentFailures[agent.name]
	newCancel := app.agentCancels[agent.name]
	app.mu.Unlock()
	if stillFailed || newCancel == nil || newCancel == oldCancel {
		t.Fatalf("successor registration/circuit reset failed: stillFailed=%v newCancel=%p", stillFailed, newCancel)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("close app: %v", err)
	}
}

func TestReplaceSpecialtySupervisorClearsCircuitOnlyAfterPriorExit(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	name := tasteAnalystAgentName
	oldCancel := make(chan struct{})
	oldDone := make(chan struct{})
	oldCancelObserved := make(chan struct{})
	releaseOldExit := make(chan struct{})
	go func() {
		<-oldCancel
		close(oldCancelObserved)
		<-releaseOldExit
		close(oldDone)
	}()
	app.mu.Lock()
	app.agentCancels = map[string]chan struct{}{}
	app.agentDones = map[string]chan struct{}{}
	if app.agentFailures == nil {
		app.agentFailures = map[string]*ambientAgentFailure{}
	}
	app.agentCancels[name] = oldCancel
	app.agentDones[name] = oldDone
	app.agentFailures[name] = &ambientAgentFailure{windowID: "held", attempts: ambientProviderMaxWindowAttempts, providerOpen: true}
	app.mu.Unlock()

	newCancel := make(chan struct{})
	newDone := make(chan struct{})
	restarted := make(chan struct{})
	go func() {
		app.replaceSpecialtyAgentSupervisor(ambientAgentConfig{name: name}, newCancel, newDone, nil)
		close(restarted)
	}()
	select {
	case <-oldCancelObserved:
	case <-time.After(2 * time.Second):
		t.Fatal("specialty restart never asked the prior supervisor to exit")
	}
	app.mu.Lock()
	stillOpen := app.agentFailures[name]
	stillOld := app.agentCancels[name] == oldCancel
	app.mu.Unlock()
	if stillOpen == nil || !stillOpen.providerOpen || !stillOld {
		t.Fatalf("specialty restart reset before old exit: failure=%+v stillOld=%v", stillOpen, stillOld)
	}
	close(releaseOldExit)
	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("specialty restart did not install successor")
	}
	app.mu.Lock()
	_, stillFailed := app.agentFailures[name]
	installed := app.agentCancels[name] == newCancel && app.agentDones[name] == newDone
	app.mu.Unlock()
	if stillFailed || !installed {
		t.Fatalf("specialty successor state: stillFailed=%v installed=%v", stillFailed, installed)
	}
	close(newCancel)
	close(newDone)
}

func TestAmbientHeldWindowSurvivesSameProcessAndProcessRestart(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	t.Setenv("HELD_WINDOW_INTERVAL", "1h")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))

	var firstObserved [][]string
	agent := newHeldWindowTestAgent("held generic restart", false, &firstObserved)
	app := newKanbanBoardApp()
	appendHeldWindowBrain(t, app, "held-base", officeRoomID)
	app.setAmbientAgentBaselineID(agent.name, "held-base")
	if _, err := app.ensureAmbientScopeCheckpoint(agent, officeRoomID, "held-base"); err != nil {
		t.Fatalf("seed pre-existing scope checkpoint: %v", err)
	}
	app.startAmbientAgent(agent, "test-key")
	appendHeldWindowBrain(t, app, "held-exact", officeRoomID)
	app.recordAmbientAgentHoldFailure(agent, "held-exact", officeRoomID)

	held, ok, err := app.ambientHeldWindow(agent.name)
	if err != nil || !ok || held.WindowID != "held-exact" || held.BaselineID != "held-base" {
		t.Fatalf("same-process held checkpoint=%+v ok=%v err=%v", held, ok, err)
	}
	app.startAmbientAgent(agent, "test-key")
	if baseline := app.ambientAgentBaselineID(agent.name); baseline != "held-base" {
		t.Fatalf("same-process restart baseline=%q, want held-base", baseline)
	}
	app.mu.Lock()
	_, circuitStillOpen := app.agentFailures[agent.name]
	app.mu.Unlock()
	if circuitStillOpen {
		t.Fatal("same-process restart did not clear the in-memory circuit")
	}
	if _, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "test-key", nil, 1, officeRoomID); err != nil {
		t.Fatalf("same-process held retry: %v", err)
	}
	if len(firstObserved) != 1 || strings.Join(firstObserved[0], ",") != "held-exact" {
		t.Fatalf("same-process retried windows=%v, want exact held-exact window", firstObserved)
	}
	if _, ok, err := app.ambientHeldWindow(agent.name); err != nil || ok {
		t.Fatalf("same-process checkpoint remained after success: ok=%v err=%v", ok, err)
	}

	// Hold a second window, then reconstruct the app from the same files to
	// prove the process boundary preserves the next exact cursor as well.
	appendHeldWindowBrain(t, app, "held-process", officeRoomID)
	app.recordAmbientAgentHoldFailure(agent, "held-process", officeRoomID)
	if err := app.Close(); err != nil {
		t.Fatalf("close first process: %v", err)
	}

	var restartedObserved [][]string
	restartedAgent := newHeldWindowTestAgent(agent.name, false, &restartedObserved)
	restarted := newKanbanBoardApp()
	restarted.startAmbientAgent(restartedAgent, "test-key")
	if baseline := restarted.ambientAgentBaselineID(agent.name); baseline != "held-base" {
		t.Fatalf("process restart baseline=%q, want held-base", baseline)
	}
	if _, err := restarted.invokeAmbientAgentGuarded(restartedAgent, context.Background(), "test-key", nil, 1, officeRoomID); err != nil {
		t.Fatalf("retry held window: %v", err)
	}
	if len(restartedObserved) != 1 || strings.Join(restartedObserved[0], ",") != "held-process" {
		t.Fatalf("retried windows=%v, want exact held-process window", restartedObserved)
	}
	if _, ok, err := restarted.ambientHeldWindow(agent.name); err != nil || ok {
		t.Fatalf("held checkpoint remained after success: ok=%v err=%v", ok, err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatalf("close restarted process: %v", err)
	}
}

func TestAmbientHeldWindowSurvivesNamedRoomLazyBoot(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
	roomID := "room-held1111"
	var observed [][]string
	agent := newHeldWindowTestAgent("held named room", true, &observed)
	first := newKanbanBoardApp()
	appendHeldWindowBrain(t, first, "named-base", roomID)
	key := ambientAgentKey(agent.name, roomID)
	first.setAmbientAgentBaselineID(key, "named-base")
	appendHeldWindowBrain(t, first, "named-held", roomID)
	first.recordAmbientAgentHoldFailure(agent, "named-held", roomID)

	restarted := newKanbanBoardApp()
	if baseline := restarted.ensureAmbientAgentRoomBaseline(agent, roomID); baseline != "named-base" {
		t.Fatalf("named-room lazy baseline=%q, want named-base", baseline)
	}
	if _, err := restarted.invokeAmbientAgentGuarded(agent, context.Background(), "test-key", nil, 1, roomID); err != nil {
		t.Fatalf("retry named-room held window: %v", err)
	}
	if len(observed) != 1 || strings.Join(observed[0], ",") != "named-held" {
		t.Fatalf("named-room retried windows=%v, want exact named-held window", observed)
	}
	if _, ok, err := restarted.ambientHeldWindow(key); err != nil || ok {
		t.Fatalf("named-room checkpoint remained after success: ok=%v err=%v", ok, err)
	}
}

func TestCloseFlushChainRetiresMeetingBoardWorker(t *testing.T) {
	for _, candidate := range closeFlushChain() {
		if candidate.name == meetingBoardAgentName {
			t.Fatalf("retired Board worker remains in close flush chain: %+v", candidate)
		}
	}
}

func TestGuardedConcurrentProviderFailuresShareAtomicFourCallBudget(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	app := newIsolatedKanbanBoardApp(t)
	appendHeldWindowBrain(t, app, "concurrent-held", officeRoomID)

	var calls atomic.Int32
	agent := newHeldWindowTestAgent("atomic provider budget", false, new([][]string))
	agent.produce = func(_ *kanbanBoardApp, ctx context.Context, apiKey string, _ []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error) {
		_, err := responder(ctx, apiKey, openAITextRequest{Model: "injected-only"})
		return meetingMemoryEntry{}, err
	}
	responder := func(context.Context, string, openAITextRequest) (string, error) {
		calls.Add(1)
		return "", &openAIProviderFailure{err: errors.New("injected provider outage")}
	}
	key := ambientAgentScopeKey(agent, officeRoomID)
	for wave := 0; wave < ambientProviderMaxWindowAttempts; wave++ {
		var group sync.WaitGroup
		for caller := 0; caller < 16; caller++ {
			group.Add(1)
			go func() {
				defer group.Done()
				_, _ = app.invokeAmbientAgentGuarded(agent, context.Background(), "injected-only", responder, 1, officeRoomID)
			}()
		}
		group.Wait()
		expireAmbientAgentBackoffForTest(app, key)
	}
	// A fresh concurrent burst after the fourth completion is fully suppressed.
	var final sync.WaitGroup
	for caller := 0; caller < 16; caller++ {
		final.Add(1)
		go func() {
			defer final.Done()
			_, _ = app.invokeAmbientAgentGuarded(agent, context.Background(), "injected-only", responder, 1, officeRoomID)
		}()
	}
	final.Wait()

	if got := calls.Load(); got != ambientProviderMaxWindowAttempts {
		t.Fatalf("concurrent provider calls=%d, want exactly bounded %d", got, ambientProviderMaxWindowAttempts)
	}
	app.mu.Lock()
	failure := app.agentFailures[key]
	app.mu.Unlock()
	if failure == nil || !failure.providerOpen || failure.attempts != ambientProviderMaxWindowAttempts {
		t.Fatalf("concurrent circuit=%+v, want bounded open circuit", failure)
	}
	if baseline := app.ambientAgentBaselineID(key); baseline == "concurrent-held" {
		t.Fatalf("concurrent outage advanced held cursor to %q", baseline)
	}
	if deadLetters := app.memory.entriesOfKind(meetingMemoryKindDeadLetter, 0); len(deadLetters) != 0 {
		t.Fatalf("concurrent outage dead-lettered held input: %+v", deadLetters)
	}
}

func TestConcurrentNamedRoomCloseSharesGlobalAgentCircuitAndPass(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "injected-only"
	app.mu.Unlock()
	for _, candidate := range closeFlushChain() {
		if candidate.name != companyDigestAgentName {
			t.Setenv(candidate.disabledEnv, "true")
		}
	}
	t.Setenv(companyDigestAgent().disabledEnv, "false")
	t.Setenv(companyDigestAgent().intervalEnv, "1h")
	t.Setenv(companyDigestAgent().backfillEnv, "true")

	raw, err := json.Marshal(ledgerEventPayload{
		Op: ledgerOpAdd,
		Record: ledgerRecord{
			ID: "ldg-global-close", Entity: ledgerEntityTopic, Title: "Global close circuit",
			Status: ledgerStatusActive, ValidFrom: time.Now().UTC().Format(time.RFC3339),
		},
		At: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	if appended, err := app.memory.appendLedgerEvents([]meetingMemoryEntry{{
		ID: "ledger-global-close", Kind: meetingMemoryKindLedgerEvent, Text: string(raw), CreatedAt: time.Now().UTC(),
		Metadata: map[string]string{"visibility": "organization"},
	}}); err != nil || appended != 1 {
		t.Fatalf("seed ledger input: appended=%d err=%v", appended, err)
	}

	var calls atomic.Int32
	responder := func(context.Context, string, openAITextRequest) (string, error) {
		calls.Add(1)
		return "", &openAIProviderFailure{err: errors.New("injected global close outage")}
	}
	agent := companyDigestAgent()
	key := ambientAgentScopeKey(agent, officeRoomID)
	for wave := 0; wave < ambientProviderMaxWindowAttempts; wave++ {
		var group sync.WaitGroup
		for _, roomID := range []string{"room-global-a", "room-global-b"} {
			roomID := roomID
			group.Add(1)
			go func() {
				defer group.Done()
				app.flushAmbientAgentsForCloseWithResponder("concurrent-close", roomID, false, responder)
			}()
		}
		group.Wait()
		expireAmbientAgentBackoffForTest(app, key)
	}
	app.flushAmbientAgentsForCloseWithResponder("fifth-close", "room-global-c", false, responder)

	if got := calls.Load(); got != ambientProviderMaxWindowAttempts {
		t.Fatalf("global close provider calls=%d, want %d", got, ambientProviderMaxWindowAttempts)
	}
	app.mu.Lock()
	failure := app.agentFailures[key]
	_, roomAFailure := app.agentFailures[ambientAgentKey(agent.name, "room-global-a")]
	_, roomBFailure := app.agentFailures[ambientAgentKey(agent.name, "room-global-b")]
	app.mu.Unlock()
	if failure == nil || !failure.providerOpen || roomAFailure || roomBFailure {
		t.Fatalf("global close scope failure=%+v roomA=%v roomB=%v", failure, roomAFailure, roomBFailure)
	}
	if baseline := app.ambientAgentBaselineID(key); baseline == "ledger-global-close" {
		t.Fatalf("global close advanced held cursor to %q", baseline)
	}
	if deadLetters := app.memory.entriesOfKind(meetingMemoryKindDeadLetter, 0); len(deadLetters) != 0 {
		t.Fatalf("global close dead-lettered held input: %+v", deadLetters)
	}
}

func TestAmbientHeldWindowPersistenceFaultsFailClosedAndRestartSafely(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	t.Setenv("HELD_WINDOW_INTERVAL", "1h")
	resetCapabilityRuntimeForTest(t)
	originalPersist := ambientHeldWindowStatePersist
	t.Cleanup(func() { ambientHeldWindowStatePersist = originalPersist })

	faults := []struct {
		name string
		wrap func(string, ambientHeldWindowState) error
	}{
		{name: "write", wrap: func(string, ambientHeldWindowState) error { return errors.New("injected write failure") }},
		{name: "fsync", wrap: func(string, ambientHeldWindowState) error { return errors.New("injected fsync failure") }},
		{name: "ambiguous rename", wrap: func(path string, state ambientHeldWindowState) error {
			if err := originalPersist(path, state); err != nil {
				return err
			}
			return fmt.Errorf("%w: injected parent fsync failure", ErrDurableReplaceAmbiguous)
		}},
	}
	for _, fault := range faults {
		t.Run(fault.name, func(t *testing.T) {
			ambientHeldWindowStatePersist = originalPersist
			dir := t.TempDir()
			t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
			t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
			var observed [][]string
			agent := newHeldWindowTestAgent("held persistence "+fault.name, false, &observed)
			app := newKanbanBoardApp()
			appendHeldWindowBrain(t, app, "persistence-base", officeRoomID)
			app.setAmbientAgentBaselineID(agent.name, "persistence-base")
			if _, err := app.ensureAmbientScopeCheckpoint(agent, officeRoomID, "persistence-base"); err != nil {
				t.Fatalf("seed durable scope anchor: %v", err)
			}
			appendHeldWindowBrain(t, app, "persistence-held", officeRoomID)

			ambientHeldWindowStatePersist = fault.wrap
			_, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "injected-only", func(context.Context, string, openAITextRequest) (string, error) {
				t.Fatal("persistence fault reached injected provider")
				return "", nil
			}, 1, officeRoomID)
			if !isAmbientAgentHoldError(err) {
				t.Fatalf("fault error=%v, want held persistence error", err)
			}
			if len(observed) != 0 {
				t.Fatalf("persistence fault invoked producer: %v", observed)
			}
			if baseline := app.ambientAgentBaselineID(agent.name); baseline != "persistence-base" {
				t.Fatalf("fault advanced baseline=%q, want persistence-base", baseline)
			}
			previousApp := kanbanApp
			kanbanApp = app
			health := ambientWorkerCapabilitySnapshot(agent, time.Now().UTC(), providerOpenAI, true)
			kanbanApp = previousApp
			if health["status"] != "degraded" || health["circuit"] != "persistence_error" || health["persistenceError"] != true {
				t.Fatalf("persistence health=%v", health)
			}
			if deadLetters := app.memory.entriesOfKind(meetingMemoryKindDeadLetter, 0); len(deadLetters) != 0 {
				t.Fatalf("persistence fault dead-lettered input: %+v", deadLetters)
			}

			// Simulate a clean process restart after storage recovers. Both a prior
			// anchor (write/fsync-before-publish) and a visibly published ambiguous
			// held marker must restore the old baseline and exact input.
			ambientHeldWindowStatePersist = originalPersist
			var restartedObserved [][]string
			restartedAgent := newHeldWindowTestAgent(agent.name, false, &restartedObserved)
			restarted := newKanbanBoardApp()
			restarted.startAmbientAgent(restartedAgent, "injected-only")
			if baseline := restarted.ambientAgentBaselineID(agent.name); baseline != "persistence-base" {
				t.Fatalf("restart baseline=%q, want persistence-base", baseline)
			}
			if _, err := restarted.invokeAmbientAgentGuarded(restartedAgent, context.Background(), "injected-only", func(context.Context, string, openAITextRequest) (string, error) {
				return "injected", nil
			}, 1, officeRoomID); err != nil {
				t.Fatalf("restart retry: %v", err)
			}
			if len(restartedObserved) != 1 || strings.Join(restartedObserved[0], ",") != "persistence-held" {
				t.Fatalf("restart observed=%v, want exact persistence-held", restartedObserved)
			}
			if err := restarted.Close(); err != nil {
				t.Fatalf("close restarted app: %v", err)
			}
		})
	}
}

func TestNoSidecarMigratesGlobalAndRoomContinuityFromDurableCursor(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	t.Setenv("HELD_WINDOW_INTERVAL", "1h")
	for _, test := range []struct {
		name       string
		roomID     string
		roomScoped bool
	}{
		{name: "global", roomID: officeRoomID, roomScoped: false},
		{name: "room scoped", roomID: "room-continuity", roomScoped: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
			t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
			var observed [][]string
			agent := newHeldWindowTestAgent("continuity "+test.name, test.roomScoped, &observed)
			seed := newKanbanBoardApp()
			appendHeldWindowBrain(t, seed, "continuity-consumed", test.roomID)
			metadata := map[string]string{"throughBrainId": "continuity-consumed", "visibility": "organization"}
			if normalizeRoomID(test.roomID) != officeRoomID {
				metadata["roomId"] = normalizeRoomID(test.roomID)
			}
			if _, appended, err := seed.memory.appendEntry(agent.artifactKind, "continuity-artifact", "durable consumed-through cursor", metadata); err != nil || !appended {
				t.Fatalf("seed durable cursor: appended=%v err=%v", appended, err)
			}
			appendHeldWindowBrain(t, seed, "continuity-pending", test.roomID)
			if _, err := os.Stat(seed.ambientHeldWindowPath()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("precondition sidecar exists or stat failed: %v", err)
			}

			restarted := newKanbanBoardApp()
			restarted.startAmbientAgent(agent, "injected-only")
			if _, err := restarted.invokeAmbientAgentGuarded(agent, context.Background(), "injected-only", func(context.Context, string, openAITextRequest) (string, error) {
				return "injected", nil
			}, 1, test.roomID); err != nil {
				t.Fatalf("migrated continuity pass: %v", err)
			}
			if len(observed) != 1 || strings.Join(observed[0], ",") != "continuity-pending" {
				t.Fatalf("migrated window=%v, want only pending input", observed)
			}
			key := ambientAgentScopeKey(agent, test.roomID)
			checkpoint, ok, err := restarted.ambientScopeCheckpoint(key)
			if err != nil || !ok || checkpoint.BaselineID != "continuity-consumed" || checkpoint.BlockedReason != "" {
				t.Fatalf("migrated checkpoint=%+v ok=%v err=%v", checkpoint, ok, err)
			}
			if err := restarted.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWrongKindAmbientCheckpointCannotSkipInputs(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	t.Setenv("HELD_WINDOW_INTERVAL", "1h")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
	var observed [][]string
	agent := newHeldWindowTestAgent("wrong kind checkpoint", false, &observed)
	seed := newKanbanBoardApp()
	appendHeldWindowBrain(t, seed, "wrong-kind-input-a", officeRoomID)
	if _, _, err := seed.memory.appendEntry(meetingMemoryKindDecision, "wrong-kind-baseline", "Unrelated later artifact.", map[string]string{"visibility": "organization"}); err != nil {
		t.Fatalf("append unrelated artifact: %v", err)
	}
	appendHeldWindowBrain(t, seed, "wrong-kind-input-b", officeRoomID)
	state := ambientHeldWindowState{Version: 1, Windows: map[string]ambientHeldWindow{
		agent.name: {Agent: agent.name, RoomID: officeRoomID, BaselineID: "wrong-kind-baseline"},
	}}
	if err := persistAmbientHeldWindowState(seed.ambientHeldWindowPath(), state); err != nil {
		t.Fatalf("persist bad checkpoint: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	restarted := newKanbanBoardApp()
	defer restarted.Close()
	restarted.startAmbientAgent(agent, "injected-only")
	calls := 0
	_, err := restarted.invokeAmbientAgentGuarded(agent, context.Background(), "injected-only", func(context.Context, string, openAITextRequest) (string, error) {
		calls++
		return "injected", nil
	}, 1, officeRoomID)
	var circuitErr *ambientAgentCircuitOpenError
	if !errors.As(err, &circuitErr) || !circuitErr.RestartRequired || calls != 0 || len(observed) != 0 {
		t.Fatalf("wrong-kind checkpoint err=%v calls=%d observed=%v", err, calls, observed)
	}
	checkpoint, ok, checkpointErr := restarted.ambientScopeCheckpoint(agent.name)
	if checkpointErr != nil || !ok || checkpoint.BlockedReason == "" {
		t.Fatalf("blocked checkpoint=%+v ok=%v err=%v", checkpoint, ok, checkpointErr)
	}
}

func TestInvalidNonHeldCheckpointRepairsFromDurableCursor(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	t.Setenv("HELD_WINDOW_INTERVAL", "1h")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
	var observed [][]string
	agent := newHeldWindowTestAgent("repair invalid checkpoint", false, &observed)
	seed := newKanbanBoardApp()
	appendHeldWindowBrain(t, seed, "repair-consumed", officeRoomID)
	if _, _, err := seed.memory.appendEntry(agent.artifactKind, "repair-artifact", "Consumed cursor.", map[string]string{
		"visibility": "organization", agent.cursorMetadataKey: "repair-consumed",
	}); err != nil {
		t.Fatalf("append durable cursor: %v", err)
	}
	appendHeldWindowBrain(t, seed, "repair-pending", officeRoomID)
	state := ambientHeldWindowState{Version: 1, Windows: map[string]ambientHeldWindow{
		agent.name: {Agent: agent.name, RoomID: officeRoomID, BaselineID: "missing-invalid-baseline"},
	}}
	if err := persistAmbientHeldWindowState(seed.ambientHeldWindowPath(), state); err != nil {
		t.Fatalf("persist bad checkpoint: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	restarted := newKanbanBoardApp()
	defer restarted.Close()
	restarted.startAmbientAgent(agent, "injected-only")
	if _, err := restarted.invokeAmbientAgentGuarded(agent, context.Background(), "injected-only", nil, 1, officeRoomID); err != nil {
		t.Fatalf("repaired pass: %v", err)
	}
	if len(observed) != 1 || strings.Join(observed[0], ",") != "repair-pending" {
		t.Fatalf("repaired observed=%v, want only repair-pending", observed)
	}
	checkpoint, ok, checkpointErr := restarted.ambientScopeCheckpoint(agent.name)
	if checkpointErr != nil || !ok || checkpoint.BaselineID != "repair-consumed" || checkpoint.BlockedReason != "" || checkpoint.InputKind != agent.inputKind || checkpoint.ArtifactKind != agent.artifactKind {
		t.Fatalf("repaired checkpoint=%+v ok=%v err=%v", checkpoint, ok, checkpointErr)
	}
}

func TestWrongKindHeldWindowFailsClosed(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	t.Setenv("HELD_WINDOW_INTERVAL", "1h")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
	var observed [][]string
	agent := newHeldWindowTestAgent("wrong held checkpoint", false, &observed)
	seed := newKanbanBoardApp()
	appendHeldWindowBrain(t, seed, "wrong-held-base", officeRoomID)
	if _, _, err := seed.memory.appendEntry(agent.artifactKind, "wrong-held-artifact", "Not an input head.", map[string]string{
		agent.cursorMetadataKey: "wrong-held-base", "visibility": "organization",
	}); err != nil {
		t.Fatalf("append worker artifact: %v", err)
	}
	state := ambientHeldWindowState{Version: 1, Windows: map[string]ambientHeldWindow{
		agent.name: {Agent: agent.name, RoomID: officeRoomID, BaselineID: "wrong-held-base", WindowID: "wrong-held-artifact"},
	}}
	if err := persistAmbientHeldWindowState(seed.ambientHeldWindowPath(), state); err != nil {
		t.Fatalf("persist bad held checkpoint: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	restarted := newKanbanBoardApp()
	defer restarted.Close()
	restarted.startAmbientAgent(agent, "injected-only")
	checkpoint, ok, checkpointErr := restarted.ambientScopeCheckpoint(agent.name)
	if checkpointErr != nil || !ok || checkpoint.BlockedReason != ambientContinuityHeldWindowInvalid {
		t.Fatalf("held checkpoint=%+v ok=%v err=%v", checkpoint, ok, checkpointErr)
	}
	restarted.mu.Lock()
	appFailure := restarted.agentFailures[agent.name]
	restarted.mu.Unlock()
	if appFailure == nil || !appFailure.continuityOpen || len(observed) != 0 {
		t.Fatalf("held continuity failure=%+v observed=%v", appFailure, observed)
	}
}

func TestLegacyCursorlessArtifactNormalizesToInputCursor(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	t.Setenv("HELD_WINDOW_INTERVAL", "1h")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
	var observed [][]string
	agent := newHeldWindowTestAgent("legacy cursorless normalization", false, &observed)
	seed := newKanbanBoardApp()
	appendHeldWindowBrain(t, seed, "legacy-normalized-input", officeRoomID)
	if _, _, err := seed.memory.appendEntry(agent.artifactKind, "legacy-cursorless-artifact", "Legacy artifact.", map[string]string{"visibility": "organization"}); err != nil {
		t.Fatalf("append cursorless artifact: %v", err)
	}
	appendHeldWindowBrain(t, seed, "legacy-normalized-pending", officeRoomID)
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	restarted := newKanbanBoardApp()
	defer restarted.Close()
	restarted.startAmbientAgent(agent, "injected-only")
	checkpoint, ok, checkpointErr := restarted.ambientScopeCheckpoint(agent.name)
	if checkpointErr != nil || !ok || checkpoint.BaselineID != "legacy-normalized-input" || checkpoint.BaselineID == "legacy-cursorless-artifact" {
		t.Fatalf("normalized checkpoint=%+v ok=%v err=%v", checkpoint, ok, checkpointErr)
	}
	if _, err := restarted.invokeAmbientAgentGuarded(agent, context.Background(), "injected-only", nil, 1, officeRoomID); err != nil {
		t.Fatalf("legacy normalized pass: %v", err)
	}
	if len(observed) != 1 || strings.Join(observed[0], ",") != "legacy-normalized-pending" {
		t.Fatalf("legacy normalized observed=%v", observed)
	}
}

func TestNoSidecarAmbiguousRawContinuityStaysFailClosedAcrossRestart(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	t.Setenv("HELD_WINDOW_INTERVAL", "1h")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
	agent := newHeldWindowTestAgent("ambiguous continuity", false, new([][]string))
	seed := newKanbanBoardApp()
	appendHeldWindowBrain(t, seed, "ambiguous-raw", officeRoomID)

	for restart := 0; restart < 2; restart++ {
		app := newKanbanBoardApp()
		app.startAmbientAgent(agent, "injected-only")
		_, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "injected-only", func(context.Context, string, openAITextRequest) (string, error) {
			t.Fatal("ambiguous continuity reached provider")
			return "", nil
		}, 1, officeRoomID)
		var circuitErr *ambientAgentCircuitOpenError
		if !errors.As(err, &circuitErr) || !circuitErr.RestartRequired {
			t.Fatalf("restart %d error=%v, want fail-closed continuity circuit", restart, err)
		}
		if baseline := app.ambientAgentBaselineID(agent.name); baseline == "ambiguous-raw" {
			t.Fatalf("restart %d silently baselined past raw input", restart)
		}
		app.mu.Lock()
		failure := app.agentFailures[agent.name]
		app.mu.Unlock()
		if failure == nil || !failure.continuityOpen {
			t.Fatalf("restart %d continuity failure=%+v", restart, failure)
		}
		if err := app.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNoSidecarProvablyCleanInstallAcceptsOnlyPostStartWork(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	t.Setenv("HELD_WINDOW_INTERVAL", "1h")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
	var observed [][]string
	agent := newHeldWindowTestAgent("clean continuity", false, &observed)
	app := newKanbanBoardApp()
	app.startAmbientAgent(agent, "injected-only")
	app.mu.Lock()
	failure := app.agentFailures[agent.name]
	app.mu.Unlock()
	if failure != nil {
		t.Fatalf("provably empty install opened circuit: %+v", failure)
	}
	appendHeldWindowBrain(t, app, "clean-post-start", officeRoomID)
	if _, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "injected-only", func(context.Context, string, openAITextRequest) (string, error) {
		return "injected", nil
	}, 1, officeRoomID); err != nil {
		t.Fatalf("clean-install first pass: %v", err)
	}
	if len(observed) != 1 || strings.Join(observed[0], ",") != "clean-post-start" {
		t.Fatalf("clean-install window=%v", observed)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNoSidecarSlopContinuityResumesFromDurablePassCursor(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("SLOP_CLASSIFIER_INTERVAL", "1h")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
	agent := slopClassifierAgent()
	seed := newKanbanBoardApp()
	if _, appended, err := seed.memory.appendTranscript("slop-migrated-consumed", "", "Previously classified transcript."); err != nil || !appended {
		t.Fatalf("seed consumed transcript: appended=%v err=%v", appended, err)
	}
	if _, _, err := seed.memory.appendSlopPass("slop-migrated-pass", "prior pass", map[string]string{slopClassifierCursorKey: "slop-migrated-consumed"}); err != nil {
		t.Fatalf("seed Slop cursor: %v", err)
	}
	for index := 0; index < 8; index++ {
		id := fmt.Sprintf("slop-migrated-pending-%d", index)
		if _, appended, err := seed.memory.appendTranscript(id, "", fmt.Sprintf("Settled held transcript %d about obsolete office logistics.", index)); err != nil || !appended {
			t.Fatalf("seed pending transcript: appended=%v err=%v", appended, err)
		}
	}
	seed.memory.mu.Lock()
	for index := range seed.memory.entries {
		if strings.HasPrefix(seed.memory.entries[index].ID, "slop-migrated-pending-") {
			seed.memory.entries[index].CreatedAt = time.Now().UTC().Add(-10 * 24 * time.Hour)
		}
	}
	if err := seed.memory.rewriteLocked(true); err != nil {
		seed.memory.mu.Unlock()
		t.Fatalf("persist backdated Slop migration inputs: %v", err)
	}
	seed.memory.mu.Unlock()

	restarted := newKanbanBoardApp()
	restarted.startSlopClassifierWorker("injected-only")
	if baseline := restarted.ambientAgentBaselineID(agent.name); baseline != "slop-migrated-consumed" {
		t.Fatalf("Slop migrated baseline=%q", baseline)
	}
	calls := 0
	pendingIDs := make([]string, 0, 8)
	for index := 0; index < 8; index++ {
		pendingIDs = append(pendingIDs, fmt.Sprintf("slop-migrated-pending-%d", index))
	}
	if err := restarted.runSlopClassifierOnce(agent, context.Background(), "injected-only", func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		if strings.Contains(request.Input, "slop-migrated-consumed") {
			t.Fatalf("Slop migration re-fed consumed input: %s", request.Input)
		}
		return slopKeepVerdictsJSON(t, pendingIDs), nil
	}, 8); err != nil {
		t.Fatalf("Slop migrated pass: %v", err)
	}
	if calls != 1 {
		t.Fatalf("Slop migrated calls=%d, want 1", calls)
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNoSidecarTasteAndHouseSpecialtiesResumeTheirDurableCursors(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Run("taste profile cursor", func(t *testing.T) {
		t.Setenv("TASTE_ANALYST_MIN_SIGNALS", "1")
		dir := t.TempDir()
		t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
		t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
		seed := newKanbanBoardApp()
		consumed := recordTasteTestSignal(t, seed, "AJ", signalEventArtifactEdited, map[string]string{"removedSections": "Intro"})
		if err := seed.runTasteAnalystOnce(context.Background(), "injected-only", func(context.Context, string, openAITextRequest) (string, error) {
			return tasteTestResponse(t, []string{consumed.ID}, nil), nil
		}); err != nil {
			t.Fatalf("seed taste profile: %v", err)
		}
		pending := recordTasteTestSignal(t, seed, "AJ", signalEventSurveyOff, map[string]string{"note": "less jargon"})
		if _, err := os.Stat(seed.ambientHeldWindowPath()); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Taste sidecar precondition: %v", err)
		}

		restarted := newKanbanBoardApp()
		if !tasteAnalystWorkDue(restarted, time.Now().UTC()) {
			t.Fatal("pending Taste signal was lost across no-sidecar restart")
		}
		if err := restarted.runTasteAnalystOnce(context.Background(), "injected-only", func(_ context.Context, _ string, request openAITextRequest) (string, error) {
			ids := tasteWindowIDsFromInput(request.Input)
			if len(ids) != 1 || ids[0] != pending.ID {
				t.Fatalf("Taste resumed window=%v, want only %s", ids, pending.ID)
			}
			return tasteTestResponse(t, ids, nil), nil
		}); err != nil {
			t.Fatalf("resume Taste: %v", err)
		}
		profile, ok := restarted.tasteProfileForUser("AJ")
		if !ok || profile.Metadata[tasteAnalystCursorKey] != pending.ID {
			t.Fatalf("Taste cursor after resume=%v", profile.Metadata)
		}
	})

	t.Run("house style binder cursor", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
		t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
		seed := newKanbanBoardApp()
		published := seedHouseStyleSourceArtifact(t, seed)
		if err := seed.runHouseStyleDistillerOnce(context.Background(), "injected-only", func(context.Context, string, openAITextRequest) (string, error) {
			return houseStyleTestBody(published.ID), nil
		}); err != nil {
			t.Fatalf("seed House Style: %v", err)
		}
		binder := seedHouseStyleBinderArtifact(t, seed)
		if _, err := os.Stat(seed.ambientHeldWindowPath()); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("House sidecar precondition: %v", err)
		}

		restarted := newKanbanBoardApp()
		if !houseStyleWorkDue(restarted, time.Now().UTC()) {
			t.Fatal("pending House binder was lost across no-sidecar restart")
		}
		if err := restarted.runHouseStyleDistillerOnce(context.Background(), "injected-only", func(_ context.Context, _ string, request openAITextRequest) (string, error) {
			if !strings.Contains(request.Input, binder.ID) {
				t.Fatalf("House resumed prompt omitted pending binder: %s", request.Input)
			}
			return houseStyleTestBody(binder.ID), nil
		}); err != nil {
			t.Fatalf("resume House Style: %v", err)
		}
		style, ok := restarted.houseStyleArtifact()
		if !ok || style.Metadata[houseStyleCursorKey] != binder.ID {
			t.Fatalf("House cursor after resume=%v", style.Metadata)
		}
	})
}

func TestFutureAmbientCheckpointVersionFailsClosedWithoutRewrite(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	t.Setenv("HELD_WINDOW_INTERVAL", "1h")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
	app := newKanbanBoardApp()
	agent := newHeldWindowTestAgent("future checkpoint", false, new([][]string))
	appendHeldWindowBrain(t, app, "future-version-input", officeRoomID)
	path := app.ambientHeldWindowPath()
	raw := []byte(`{"version":2,"windows":{}}` + "\n")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	app.startAmbientAgent(agent, "injected-only")
	_, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "injected-only", func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("future checkpoint version reached provider")
		return "", nil
	}, 1, officeRoomID)
	var circuitErr *ambientAgentCircuitOpenError
	if !errors.As(err, &circuitErr) || !circuitErr.RestartRequired {
		t.Fatalf("future-version error=%v, want fail-closed circuit", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil || string(after) != string(raw) {
		t.Fatalf("future-version checkpoint was rewritten: err=%v raw=%s", readErr, after)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAmbientAgentRunnerBaselineSkipsHistory(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	var produced [][]string
	agent := newTestAmbientAgent(&produced)

	appendTestTranscript(t, app, "historic", "Historic Boot Barn note.")
	app.setAmbientAgentBaselineID(agent.name, app.memory.latestEntryIDOfKind(agent.inputKind))

	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", nil, 1); err != nil {
		t.Fatalf("runAmbientAgentOnce before new input: %v", err)
	}
	if len(produced) != 0 {
		t.Fatalf("produced=%v, want history before the baseline skipped", produced)
	}

	appendTestTranscript(t, app, "fresh", "Fresh Boot Barn follow-up.")
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", nil, 1); err != nil {
		t.Fatalf("runAmbientAgentOnce after new input: %v", err)
	}
	if len(produced) != 1 || strings.Join(produced[0], ",") != "fresh" {
		t.Fatalf("produced=%v, want only the post-baseline input", produced)
	}
}

func TestArchiveMeetingFlushesAgentsBeforeSnapshot(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.noteMeetingAdmission(officeRoomID, "AJ")
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	t.Setenv("MEETING_BRAIN_MIN_TRANSCRIPTS", "4")
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	var calls []string
	var callsMu sync.Mutex
	recordCall := func(call string) {
		callsMu.Lock()
		calls = append(calls, call)
		callsMu.Unlock()
	}
	originalResponder := createOpenAITextResponse
	defer func() { createOpenAITextResponse = originalResponder }()
	createOpenAITextResponse = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if strings.Contains(request.Instructions, "board intelligence") {
			recordCall("board")
			return `{"summary":"No actionable board changes.","operations":[]}`, nil
		}
		if strings.Contains(request.Instructions, "decision ledger") {
			recordCall("ledger")
			return `{"decisions":[]}`, nil
		}
		if strings.Contains(request.Instructions, "mission intelligence") {
			recordCall("mission")
			return `{"themes":[],"openQuestions":[],"alignments":[]}`, nil
		}
		if strings.Contains(request.Instructions, "narrative maintainer") {
			recordCall("narrative")
			return `{"narratives":[]}`, nil
		}
		if strings.Contains(request.Instructions, "meeting digest compiler") {
			recordCall("digest")
			return cannedArchiveMeetingDigestJSON("event-1"), nil
		}
		if strings.Contains(request.Instructions, "company digest narrator") {
			recordCall("company")
			return "The Zebra packaging pilot is decided.", nil
		}
		if strings.Contains(request.Instructions, "entity-ledger adjudicator") || strings.Contains(request.Instructions, "end-of-day reflection") {
			t.Errorf("unexpected model call at archive flush: %s", request.Instructions)
			return "", nil
		}
		recordCall("brain")
		return "## Overview\nBoot Barn shoot confirmed for Friday.", nil
	}

	appendTestTranscript(t, app, "event-1", "Boot Barn shoot confirmed for Friday.")

	result, err := app.archiveMeeting("AJ")
	if err != nil {
		t.Fatalf("archiveMeeting: %v", err)
	}
	waitForMeetingFinalizationState(t, app, result.MeetingID, meetingFinalizationFinalized)
	waitForMeetingArchiveFinalizationSync(t, app, result.MeetingID)
	waitForMeetingFinalizationQueueIdle(t, app)
	// The user-visible archive boundary returns before provider work. Once the
	// durable close queue settles, only the receipted meeting-scoped core has
	// run; wider organization rollups remain asynchronous.
	callsMu.Lock()
	gotCalls := append([]string(nil), calls...)
	callsMu.Unlock()
	if strings.Join(gotCalls, ",") != "brain,digest" {
		t.Fatalf("calls=%v, want only final Brain then meeting digest", gotCalls)
	}
	if !strings.Contains(result.DownloadURL, "?key=") {
		t.Fatalf("downloadUrl=%q, want embedded room key", result.DownloadURL)
	}

	archivePath, err := meetingArchivePath(result.ID)
	if err != nil {
		t.Fatalf("meetingArchivePath: %v", err)
	}
	rawArchive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	var archive meetingArchive
	if err := json.Unmarshal(rawArchive, &archive); err != nil {
		t.Fatalf("decode archive: %v", err)
	}
	kinds := map[string]bool{}
	for _, entry := range archive.Memory {
		kinds[entry.Kind] = true
	}
	if !kinds[meetingMemoryKindBrain] || kinds[meetingMemoryKindBoardUpdate] {
		t.Fatalf("archive memory kinds=%v, want core Brain and no retired board_update", kinds)
	}
	if archive.Meeting == nil || archive.Meeting.Finalization == nil || archive.Meeting.Finalization.State != meetingFinalizationFinalized {
		t.Fatalf("archive meeting receipt=%+v, want finalized", archive.Meeting)
	}
}

// TestAmbientAgentPassesSerialize locks in the per-agent run mutex: a flush
// pass that starts while a ticker pass is mid-produce must wait for the
// cursor to advance instead of consuming the same input batch twice.
func TestAmbientAgentPassesSerialize(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	var produced [][]string
	agent := newTestAmbientAgent(&produced)

	started := make(chan struct{})
	release := make(chan struct{})
	innerProduce := agent.produce
	passCount := 0
	agent.produce = func(app *kanbanBoardApp, ctx context.Context, apiKey string, inputs []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error) {
		passCount++
		if passCount == 1 {
			close(started)
			<-release // hold the first pass mid-"model call"
		}
		return innerProduce(app, ctx, apiKey, inputs, responder)
	}

	appendTestTranscript(t, app, "input-1", "Boot Barn kickoff planning notes.")
	appendTestTranscript(t, app, "input-2", "Boot Barn follow-up commitments.")

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", nil, 1); err != nil {
			t.Errorf("first pass: %v", err)
		}
	}()
	<-started

	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", nil, 1); err != nil {
			t.Errorf("second pass: %v", err)
		}
	}()

	select {
	case <-secondDone:
		t.Fatal("second pass finished while the first held the run lock")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	<-firstDone
	<-secondDone

	if len(produced) != 1 || strings.Join(produced[0], ",") != "input-1,input-2" {
		t.Fatalf("produced=%v, want the batch consumed exactly once", produced)
	}
}

// TestArchiveFlushSkipsIntervalDisabledAgents covers the second disable form:
// an operator turning an agent off via its interval env must also keep it
// from running at archive time.
func TestArchiveFlushSkipsIntervalDisabledAgents(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("MEETING_BRAIN_INTERVAL", "off")
	t.Setenv("MEETING_BOARD_INTERVAL", "off")
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	originalResponder := createOpenAITextResponse
	defer func() { createOpenAITextResponse = originalResponder }()
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		t.Error("disabled agents must not call the model at archive flush")
		return "", nil
	}

	appendTestTranscript(t, app, "event-1", "Boot Barn shoot confirmed for Friday.")
	app.flushAmbientAgentsForArchive()
}

// TestArchiveFlushDoesNotConsumePreBootHistory: when an agent's loop never
// started this boot, the flush must use the baseline the loop would have
// registered instead of backfilling transcripts from previous sessions.
func TestArchiveFlushDoesNotConsumePreBootHistory(t *testing.T) {
	dir := t.TempDir()
	memoryPath := filepath.Join(dir, "memory.jsonl")
	t.Setenv("MEETING_MEMORY_PATH", memoryPath)
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))

	// persist a transcript from a "previous session" before the app boots.
	preBootStore, err := newMeetingMemoryStore(memoryPath)
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	if _, appended, err := preBootStore.appendAttributedTranscript("pre-boot", "pre-boot", "Tom", "dominant", "Boot Barn notes from last week's meeting."); err != nil || !appended {
		t.Fatalf("append pre-boot transcript: appended=%v err=%v", appended, err)
	}

	app := newKanbanBoardApp()
	// This fixture exercises an already-established boot floor. Seed durable
	// scope anchors for the chain; dedicated continuity tests cover migration
	// when the sidecar itself is absent.
	for _, agent := range closeFlushChain() {
		baseline := app.memory.bootBaselineIDOfKindForRoom(agent.inputKind, agent.windowRoomID(officeRoomID))
		app.setAmbientAgentBaselineID(ambientAgentScopeKey(agent, officeRoomID), baseline)
		if _, err := app.ensureAmbientScopeCheckpoint(agent, officeRoomID, baseline); err != nil {
			t.Fatalf("seed %s scope checkpoint: %v", agent.name, err)
		}
	}
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	var calls []string
	originalResponder := createOpenAITextResponse
	defer func() { createOpenAITextResponse = originalResponder }()
	createOpenAITextResponse = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if strings.Contains(request.Input, "pre-boot") || strings.Contains(request.Input, "last week's meeting") {
			t.Errorf("flush consumed pre-boot history: %s", request.Input)
		}
		if strings.Contains(request.Instructions, "board intelligence") {
			calls = append(calls, "board")
			return `{"summary":"No actionable board changes.","operations":[]}`, nil
		}
		if strings.Contains(request.Instructions, "decision ledger") {
			calls = append(calls, "ledger")
			return `{"decisions":[]}`, nil
		}
		if strings.Contains(request.Instructions, "mission intelligence") {
			calls = append(calls, "mission")
			return `{"themes":[],"openQuestions":[],"alignments":[]}`, nil
		}
		if strings.Contains(request.Instructions, "narrative maintainer") {
			calls = append(calls, "narrative")
			return `{"narratives":[]}`, nil
		}
		if strings.Contains(request.Instructions, "meeting digest compiler") {
			calls = append(calls, "digest")
			return cannedArchiveMeetingDigestJSON("fresh"), nil
		}
		if strings.Contains(request.Instructions, "entity-ledger adjudicator") {
			t.Error("flush must not spend an adjudication call on all-new facts")
			return "", nil
		}
		if strings.Contains(request.Instructions, "end-of-day reflection") {
			t.Error("flush must not reflect without completed-day material")
			return "", nil
		}
		if strings.Contains(request.Instructions, "company digest narrator") {
			calls = append(calls, "company")
			return "The Zebra packaging pilot is decided and the pricing sheet is underway.", nil
		}
		calls = append(calls, "brain")
		return "## Overview\nBoot Barn shoot confirmed for Friday.", nil
	}

	// nothing new since boot: the flush must stay silent.
	app.flushAmbientAgentsForArchive()
	if len(calls) != 0 {
		t.Fatalf("calls=%v, want none when only pre-boot history exists", calls)
	}

	// fresh in-meeting transcript: the flush picks up from the boot baseline
	// and runs the whole close chain in dependency order. The day fold and the
	// entity-ledger consolidation are deterministic (no model call); the
	// company narrative rides the ledger events the consolidation just landed.
	appendTestTranscript(t, app, "fresh", "Boot Barn shoot confirmed for Friday.")
	app.flushAmbientAgentsForArchive()
	if strings.Join(calls, ",") != "brain,digest,ledger,mission,narrative,company" {
		t.Fatalf("calls=%v, want core Brain/digest before the non-core recovery chain", calls)
	}
	if entries := app.memory.entriesOfKind(meetingMemoryKindDayDigest, 0); len(entries) == 0 {
		t.Fatal("archive flush did not fold a day digest")
	}
	if entries := app.memory.entriesOfKind(meetingMemoryKindLedgerEvent, 0); len(entries) == 0 {
		t.Fatal("archive flush did not consolidate ledger events")
	}
	if _, ok := app.memory.latestCompanyDigest(); !ok {
		t.Fatal("archive flush did not refresh the company digest")
	}
}

func TestArchiveMeetingFlushSkipsWithoutAPIKey(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	originalResponder := createOpenAITextResponse
	defer func() { createOpenAITextResponse = originalResponder }()
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("responder should not run without an api key")
		return "", nil
	}

	appendTestTranscript(t, app, "event-1", "Boot Barn shoot confirmed for Friday.")
	if _, err := app.archiveMeeting("AJ"); err != nil {
		t.Fatalf("archiveMeeting: %v", err)
	}
}

// The supersession gate is enumerated, not assumed. Gen 249's trap was a scope
// that could NEVER anchor; the opposite failures — anchoring past unprocessed
// input for a worker that has produced, papering over a bad held head, or
// looping on the anchor's own checkpoint — are worse, so every condition that
// permits a supersession and every condition that must refuse one is pinned.
func TestAmbientContinuityFirstRunAnchorSupersedesOnlyUnresolvableBlocks(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	t.Setenv("HELD_WINDOW_INTERVAL", "1h")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))

	seed := newKanbanBoardApp()
	appendHeldWindowBrain(t, seed, "anchor-gate-pre-boot", officeRoomID)
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	app := newKanbanBoardApp()
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() {
		kanbanApp = previousApp
		_ = app.Close()
	})

	optedIn := newHeldWindowTestAgent("anchor gate opted in", false, new([][]string))
	optedIn.firstRunAnchor = true
	optedOut := newHeldWindowTestAgent("anchor gate opted out", false, new([][]string))
	produced := newHeldWindowTestAgent("anchor gate produced", false, new([][]string))
	produced.firstRunAnchor = true
	// An expired artifact still proves consumption: "no VISIBLE artifact" is
	// never "never produced".
	appendContinuityArtifact(t, app, produced, "anchor-gate-produced-artifact", officeRoomID, "", true)

	checkpoint := func(agent ambientAgentConfig, reason string) ambientHeldWindow {
		return ambientHeldWindow{
			Agent: agent.name, RoomID: officeRoomID, InputKind: agent.inputKind,
			ArtifactKind: agent.artifactKind, CursorMetadataKey: agent.cursorMetadataKey,
			BlockedReason: reason,
		}
	}
	marked := checkpoint(optedIn, ambientContinuityAmbiguous)
	marked.FirstRunAnchor = true
	held := checkpoint(optedIn, ambientContinuityAmbiguous)
	held.WindowID = "anchor-gate-pre-boot"

	for _, testCase := range []struct {
		name       string
		agent      ambientAgentConfig
		checkpoint ambientHeldWindow
		wantAnchor string
		wantOK     bool
	}{
		{"the opt-in is required", optedOut, checkpoint(optedOut, ambientContinuityAmbiguous), "", false},
		{"production's shape anchors", optedIn, checkpoint(optedIn, ambientContinuityAmbiguous), "anchor-gate-pre-boot", true},
		{"an unresolvable checkpoint anchors", optedIn, checkpoint(optedIn, ambientContinuityCheckpointInvalid), "anchor-gate-pre-boot", true},
		{"a contract mismatch anchors", optedIn, checkpoint(optedIn, ambientContinuityContractMismatch), "anchor-gate-pre-boot", true},
		{"a bad held head is not an unresolved start", optedIn, checkpoint(optedIn, ambientContinuityHeldWindowInvalid), "", false},
		{"an unrecognized reason fails closed", optedIn, checkpoint(optedIn, "provider_wedged"), "", false},
		{"an unblocked checkpoint is left alone", optedIn, checkpoint(optedIn, ""), "", false},
		{"the anchor's own marker stops a second anchor", optedIn, marked, "", false},
		{"held work is never anchored past", optedIn, held, "", false},
		{"a scope that has produced never anchors", produced, checkpoint(produced, ambientContinuityAmbiguous), "", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			anchor, ok := app.ambientFirstRunAnchorSupersedesCheckpoint(testCase.agent, officeRoomID, testCase.checkpoint)
			if ok != testCase.wantOK || anchor != testCase.wantAnchor {
				t.Fatalf("anchor=%q ok=%v, want %q/%v", anchor, ok, testCase.wantAnchor, testCase.wantOK)
			}
		})
	}
}

// A scope blocked inside an ALREADY-RUNNING process must anchor on its next
// pass, not wait for a restart: production sat blocked for the whole life of
// the release, and the ticker is the only thing that fires on its own.
func TestAmbientContinuityBlockedScopeAnchorsOnTheNextPass(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	t.Setenv("HELD_WINDOW_INTERVAL", "1h")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))

	seed := newKanbanBoardApp()
	appendHeldWindowBrain(t, seed, "live-repair-pre-boot", officeRoomID)
	holdPath := seed.ambientHeldWindowPath()
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	var observed [][]string
	agent := newHeldWindowTestAgent("live repair anchor", false, &observed)
	agent.firstRunAnchor = true
	if err := persistAmbientHeldWindowState(holdPath, ambientHeldWindowState{Version: 1, Windows: map[string]ambientHeldWindow{
		agent.name: {
			Agent: agent.name, RoomID: officeRoomID, InputKind: agent.inputKind,
			ArtifactKind: agent.artifactKind, CursorMetadataKey: agent.cursorMetadataKey,
			BlockedReason: ambientContinuityAmbiguous,
		},
	}}); err != nil {
		t.Fatalf("persist the stale checkpoint: %v", err)
	}

	app := newKanbanBoardApp()
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() {
		kanbanApp = previousApp
		_ = app.Close()
	})

	// the state a running worker is left in by a blocked boot
	app.setAmbientAgentBaselineID(agent.name, "")
	app.recordAmbientAgentContinuityFailure(agent, officeRoomID)
	appendHeldWindowBrain(t, app, "live-repair-post-boot", officeRoomID)

	responder := func(context.Context, string, openAITextRequest) (string, error) { return "injected", nil }
	if _, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "injected-only", responder, 1, officeRoomID); err != nil {
		t.Fatalf("next pass after the block: %v", err)
	}
	if len(observed) != 1 || len(observed[0]) != 1 || observed[0][0] != "live-repair-post-boot" {
		t.Fatalf("observed=%v, want only the post-boot input; pre-boot history is never replayed by an anchor", observed)
	}
	checkpoint, ok, err := app.ambientScopeCheckpoint(agent.name)
	if err != nil || !ok || !checkpoint.FirstRunAnchor || checkpoint.BlockedReason != "" {
		t.Fatalf("checkpoint=%+v ok=%v err=%v, want the live supersession recorded", checkpoint, ok, err)
	}
	app.mu.Lock()
	failure := app.agentFailures[agent.name]
	app.mu.Unlock()
	if failure != nil {
		t.Fatalf("continuity circuit still open after the repair: %+v", failure)
	}
}

// hideHeldWindowBrain expires a seeded held-window input in place, which is how
// a row leaves recall in production (expiry, quarantine, supersession). The
// entry keeps its slot, so its store index stays comparable across the change.
func hideHeldWindowBrain(t *testing.T, app *kanbanBoardApp, id string) {
	t.Helper()
	text, found := "", false
	app.memory.mu.RLock()
	for _, entry := range app.memory.entries {
		if entry.ID == id && entry.Kind == meetingMemoryKindBrain {
			text, found = entry.Text, true
		}
	}
	app.memory.mu.RUnlock()
	if !found {
		t.Fatalf("brain %s is not in the store", id)
	}
	if _, updated, err := app.memory.updateEntryWithMetadata(meetingMemoryKindBrain, id, text, map[string]string{relevanceMetadataKey: relevanceExpired}); err != nil || !updated {
		t.Fatalf("hide brain %s: updated=%v err=%v", id, updated, err)
	}
}

// heldWindowInputIndex is the scope-ordered position of a baseline, so a test
// can assert the ORDER of two cursors rather than merely that they differ. The
// empty baseline is the start of the stream (replay everything), so it ranks
// before every real input.
func heldWindowInputIndex(t *testing.T, app *kanbanBoardApp, id string) int {
	t.Helper()
	if strings.TrimSpace(id) == "" {
		return -1
	}
	app.memory.mu.RLock()
	defer app.memory.mu.RUnlock()
	for index, entry := range app.memory.entries {
		if entry.ID == id {
			return index
		}
	}
	t.Fatalf("baseline %q is not in the store", id)
	return 0
}

// newAnchorLoopTestApp seeds three pre-boot inputs and restarts the app so the
// boot head is real (the anchor resolves against store.bootLatestIDs, which is
// only populated by a load).
func newAnchorLoopTestApp(t *testing.T, inputs ...string) (*kanbanBoardApp, string) {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	t.Setenv("HELD_WINDOW_INTERVAL", "1h")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))

	seed := newKanbanBoardApp()
	for _, id := range inputs {
		appendHeldWindowBrain(t, seed, id, officeRoomID)
	}
	holdPath := seed.ambientHeldWindowPath()
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	app := newKanbanBoardApp()
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() {
		kanbanApp = previousApp
		_ = app.Close()
	})
	return app, holdPath
}

// persistStaleBlockedCheckpoint writes the gen-248 shape production carried:
// blocked, unmarked, no held window.
func persistStaleBlockedCheckpoint(t *testing.T, holdPath string, agents ...ambientAgentConfig) {
	t.Helper()
	windows := map[string]ambientHeldWindow{}
	for _, agent := range agents {
		windows[ambientAgentScopeKey(agent, officeRoomID)] = ambientHeldWindow{
			Agent: agent.name, RoomID: officeRoomID, InputKind: agent.inputKind,
			ArtifactKind: agent.artifactKind, CursorMetadataKey: agent.cursorMetadataKey,
			BlockedReason: ambientContinuityAmbiguous,
		}
	}
	if err := persistAmbientHeldWindowState(holdPath, ambientHeldWindowState{Version: 1, Windows: windows}); err != nil {
		t.Fatalf("persist the stale checkpoints: %v", err)
	}
}

// THE RESIDUAL (verifier finding, 2026-09-03). The anchor-loop stop used to
// refuse ANY checkpoint carrying FirstRunAnchor. That re-opened the exact class
// of permanent trap the supersession exists to remove: a scope that anchored,
// still produced nothing, and whose anchor ROW later left recall (expired,
// quarantined, superseded) has an unresolvable baseline, is re-graded
// durable_cursor_ambiguous, and under an absolute marker could never be
// repaired by any future release.
//
// CANNOT SKIP is asserted structurally, not by "a pass ran": the re-anchored
// baseline's position in the input stream must be at or before the position of
// the baseline it replaces, so the cursor only ever moves BACKWARD (replay).
func TestAmbientContinuityFirstRunAnchorReanchorsWhenItsOwnAnchorRowIsHidden(t *testing.T) {
	app, holdPath := newAnchorLoopTestApp(t, "reanchor-a", "reanchor-b", "reanchor-c")
	agent := newHeldWindowTestAgent("reanchor hidden row", false, new([][]string))
	agent.firstRunAnchor = true
	scope := ambientAgentScopeKey(agent, officeRoomID)
	persistStaleBlockedCheckpoint(t, holdPath, agent)

	firstBaseline, blocked, err := app.bootstrapAmbientContinuity(agent, officeRoomID)
	if err != nil || blocked != "" || firstBaseline != "reanchor-c" {
		t.Fatalf("first anchor baseline=%q blocked=%q err=%v, want the newest pre-boot input", firstBaseline, blocked, err)
	}
	marked, ok, err := app.ambientScopeCheckpoint(scope)
	if err != nil || !ok || !marked.FirstRunAnchor || marked.BaselineID != firstBaseline {
		t.Fatalf("checkpoint=%+v ok=%v err=%v, want the anchor recorded", marked, ok, err)
	}

	// A marked checkpoint whose anchor row STILL RESOLVES is refused — even
	// while it carries a supersedable block, which is the only shape where the
	// refusal is load-bearing. The stop is narrowed, not removed.
	blockedButResolvable := marked
	blockedButResolvable.BlockedReason = ambientContinuityAmbiguous
	for _, refused := range []ambientHeldWindow{marked, blockedButResolvable} {
		if anchor, ok := app.ambientFirstRunAnchorSupersedesCheckpoint(agent, officeRoomID, refused); ok {
			t.Fatalf("a marked checkpoint with a resolvable baseline re-anchored to %q (checkpoint=%+v)", anchor, refused)
		}
	}
	if !app.ambientCheckpointBaselineResolves(agent, officeRoomID, blockedButResolvable) {
		t.Fatalf("baseline %q must still resolve; the refusal above would otherwise be vacuous", blockedButResolvable.BaselineID)
	}

	// Live input keeps arriving after boot: the re-anchor must NOT reach it.
	// This is what makes the backward-order assertion below load-bearing rather
	// than a restatement of the expected id.
	appendHeldWindowBrain(t, app, "reanchor-post-boot", officeRoomID)

	// The anchor row leaves recall. Under the absolute marker this scope was
	// blocked forever.
	hideHeldWindowBrain(t, app, "reanchor-c")

	secondBaseline, blocked, err := app.bootstrapAmbientContinuity(agent, officeRoomID)
	if err != nil || blocked != "" {
		t.Fatalf("baseline=%q blocked=%q err=%v, want the hidden anchor row repaired rather than a permanent block", secondBaseline, blocked, err)
	}
	// CANNOT SKIP, asserted as an ORDER between the two cursors: the new
	// baseline sits at or before the one it replaces, so every input the old
	// cursor had passed is replayed and none is stepped over.
	if before, after := heldWindowInputIndex(t, app, firstBaseline), heldWindowInputIndex(t, app, secondBaseline); after > before {
		t.Fatalf("re-anchor moved the cursor FORWARD (%s@%d -> %s@%d); a re-anchor may only replay, never skip",
			firstBaseline, before, secondBaseline, after)
	}
	if secondBaseline != "reanchor-b" {
		t.Fatalf("re-anchored baseline=%q, want the newest still-visible pre-boot input reanchor-b", secondBaseline)
	}
	repaired, ok, err := app.ambientScopeCheckpoint(scope)
	if err != nil || !ok || !repaired.FirstRunAnchor || repaired.BlockedReason != "" || repaired.BaselineID != secondBaseline {
		t.Fatalf("checkpoint=%+v ok=%v err=%v, want the re-anchor recorded and the block cleared", repaired, ok, err)
	}

	// The live pass recovers on the same terms, without a restart: a running
	// process left blocked by the hidden row anchors on its next pass.
	if err := persistAmbientHeldWindowState(holdPath, ambientHeldWindowState{Version: 1, Windows: map[string]ambientHeldWindow{
		scope: {
			Agent: agent.name, RoomID: officeRoomID, InputKind: agent.inputKind,
			ArtifactKind: agent.artifactKind, CursorMetadataKey: agent.cursorMetadataKey,
			BaselineID: "reanchor-c", BlockedReason: ambientContinuityAmbiguous, FirstRunAnchor: true,
		},
	}}); err != nil {
		t.Fatalf("persist the re-blocked live checkpoint: %v", err)
	}
	app.recordAmbientAgentContinuityFailure(agent, officeRoomID)
	// The operator signal moves with the behaviour: gen 249 was only
	// diagnosable because a stuck scope and a self-repairing one looked
	// identical. A marked scope whose anchor row went hidden must report as
	// anchorable, not as one more permanently blocked scope.
	diagnostics := ambientWorkerCheckpointDiagnostics(app, agent)
	if diagnostics["blockedScopeCount"] != 1 || diagnostics["blockedAnchorableScopes"] != 1 || diagnostics["firstRunAnchorScopes"] != 1 {
		t.Fatalf("diagnostics=%v, want the re-blocked marked scope counted as blocked AND anchorable", diagnostics)
	}
	if !app.repairBlockedAmbientContinuityWithFirstRunAnchor(agent, officeRoomID) {
		t.Fatal("the live pass refused to repair a marked checkpoint whose anchor row is hidden")
	}
	live, ok, err := app.ambientScopeCheckpoint(scope)
	if err != nil || !ok || live.BlockedReason != "" || live.BaselineID != "reanchor-b" {
		t.Fatalf("live checkpoint=%+v ok=%v err=%v, want the same backward re-anchor", live, ok, err)
	}
}

// CANNOT LOOP. Narrowing the stop is only safe if the re-anchor is a fixed
// point: it must settle the moment the baseline resolves again, walk strictly
// backward while it does not, and stop dead the moment the scope has produced.
// All three are driven here as repeated passes, because a one-shot assertion
// cannot tell a converging repair from an oscillating one.
func TestAmbientContinuityFirstRunAnchorReanchorConvergesAndCannotLoop(t *testing.T) {
	app, holdPath := newAnchorLoopTestApp(t, "loop-a", "loop-b", "loop-c")
	settled := newHeldWindowTestAgent("reanchor converges", false, new([][]string))
	settled.firstRunAnchor = true
	drained := newHeldWindowTestAgent("reanchor drains backward", false, new([][]string))
	drained.firstRunAnchor = true
	produced := newHeldWindowTestAgent("reanchor stops on production", false, new([][]string))
	produced.firstRunAnchor = true
	persistStaleBlockedCheckpoint(t, holdPath, settled, drained, produced)

	// 1. RESOLVABLE AGAIN => converged. Anchor, hide the anchor row once, then
	//    run three more consecutive passes with the new baseline resolvable.
	if baseline, blocked, err := app.bootstrapAmbientContinuity(settled, officeRoomID); err != nil || blocked != "" || baseline != "loop-c" {
		t.Fatalf("settled first anchor baseline=%q blocked=%q err=%v", baseline, blocked, err)
	}
	hideHeldWindowBrain(t, app, "loop-c")
	reanchored, blocked, err := app.bootstrapAmbientContinuity(settled, officeRoomID)
	if err != nil || blocked != "" || reanchored != "loop-b" {
		t.Fatalf("settled re-anchor baseline=%q blocked=%q err=%v, want loop-b", reanchored, blocked, err)
	}
	settledScope := ambientAgentScopeKey(settled, officeRoomID)
	for pass := 1; pass <= 3; pass++ {
		baseline, blocked, err := app.bootstrapAmbientContinuity(settled, officeRoomID)
		if err != nil || blocked != "" || baseline != reanchored {
			t.Fatalf("settled pass %d baseline=%q blocked=%q err=%v, want the checkpoint fixed at %q", pass, baseline, blocked, err, reanchored)
		}
		checkpoint, ok, err := app.ambientScopeCheckpoint(settledScope)
		if err != nil || !ok || checkpoint.BaselineID != reanchored || checkpoint.BlockedReason != "" || !checkpoint.FirstRunAnchor {
			t.Fatalf("settled pass %d checkpoint=%+v ok=%v err=%v, want a stable marked checkpoint", pass, checkpoint, ok, err)
		}
		blockedCheckpoint := checkpoint
		blockedCheckpoint.BlockedReason = ambientContinuityAmbiguous
		for _, refused := range []ambientHeldWindow{checkpoint, blockedCheckpoint} {
			if anchor, ok := app.ambientFirstRunAnchorSupersedesCheckpoint(settled, officeRoomID, refused); ok {
				t.Fatalf("settled pass %d re-anchored a resolvable marked checkpoint to %q (checkpoint=%+v)", pass, anchor, refused)
			}
		}
		if !app.ambientCheckpointBaselineResolves(settled, officeRoomID, blockedCheckpoint) {
			t.Fatalf("settled pass %d: baseline %q must still resolve", pass, blockedCheckpoint.BaselineID)
		}
	}

	// 2. STILL UNRESOLVABLE => strictly backward, and it terminates. Each hide
	//    may buy exactly one more supersession, walking toward the empty
	//    baseline, which always resolves.
	drainedScope := ambientAgentScopeKey(drained, officeRoomID)
	if baseline, blocked, err := app.bootstrapAmbientContinuity(drained, officeRoomID); err != nil || blocked != "" || baseline != "loop-b" {
		// loop-c is already hidden above, so the first anchor is loop-b.
		t.Fatalf("drained first anchor baseline=%q blocked=%q err=%v, want loop-b", baseline, blocked, err)
	}
	previous := "loop-b"
	for _, hidden := range []string{"loop-b", "loop-a"} {
		hideHeldWindowBrain(t, app, hidden)
		baseline, blocked, err := app.bootstrapAmbientContinuity(drained, officeRoomID)
		if err != nil || blocked != "" {
			t.Fatalf("drained baseline=%q blocked=%q err=%v after hiding %s, want another backward re-anchor", baseline, blocked, err, hidden)
		}
		if after, before := heldWindowInputIndex(t, app, baseline), heldWindowInputIndex(t, app, previous); after >= before {
			t.Fatalf("re-anchor after hiding %s did not move strictly backward (%s@%d -> %s@%d)", hidden, previous, before, baseline, after)
		}
		previous = baseline
	}
	if previous != "" {
		t.Fatalf("final drained baseline=%q, want the empty baseline once no visible pre-boot input remains", previous)
	}
	// The empty baseline is the terminal state: it resolves, so three more
	// passes change nothing and the scope is not blocked.
	for pass := 1; pass <= 3; pass++ {
		baseline, blocked, err := app.bootstrapAmbientContinuity(drained, officeRoomID)
		if err != nil || blocked != "" || baseline != "" {
			t.Fatalf("drained terminal pass %d baseline=%q blocked=%q err=%v, want a settled empty baseline", pass, baseline, blocked, err)
		}
		checkpoint, ok, err := app.ambientScopeCheckpoint(drainedScope)
		if err != nil || !ok || checkpoint.BaselineID != "" || checkpoint.BlockedReason != "" {
			t.Fatalf("drained terminal pass %d checkpoint=%+v ok=%v err=%v", pass, checkpoint, ok, err)
		}
		blockedCheckpoint := checkpoint
		blockedCheckpoint.BlockedReason = ambientContinuityAmbiguous
		for _, refused := range []ambientHeldWindow{checkpoint, blockedCheckpoint} {
			if anchor, ok := app.ambientFirstRunAnchorSupersedesCheckpoint(drained, officeRoomID, refused); ok {
				t.Fatalf("drained terminal pass %d re-anchored to %q; the empty baseline resolves and must stop (checkpoint=%+v)", pass, anchor, refused)
			}
		}
	}

	// 3. HAS PRODUCED => stops immediately, hidden anchor row or not. The
	//    already-produced guard is untouched by the narrowing, and an EXPIRED
	//    artifact still proves consumption.
	producedScope := ambientAgentScopeKey(produced, officeRoomID)
	appendHeldWindowBrain(t, app, "loop-d", officeRoomID)
	if baseline, blocked, err := app.bootstrapAmbientContinuity(produced, officeRoomID); err != nil || blocked != "" || baseline != "" {
		t.Fatalf("produced first anchor baseline=%q blocked=%q err=%v, want the empty anchor (no visible pre-boot input)", baseline, blocked, err)
	}
	// A hidden artifact with a cursor that resolves to nothing: production
	// evidence for the anchor, still ambiguous for the resolver.
	appendContinuityArtifact(t, app, produced, "loop-produced-artifact", officeRoomID, "no-such-brain", true)
	if err := persistAmbientHeldWindowState(holdPath, ambientHeldWindowState{Version: 1, Windows: map[string]ambientHeldWindow{
		producedScope: {
			Agent: produced.name, RoomID: officeRoomID, InputKind: produced.inputKind,
			ArtifactKind: produced.artifactKind, CursorMetadataKey: produced.cursorMetadataKey,
			BaselineID: "loop-c", BlockedReason: ambientContinuityAmbiguous, FirstRunAnchor: true,
		},
	}}); err != nil {
		t.Fatalf("persist the produced scope's blocked marked checkpoint: %v", err)
	}
	for pass := 1; pass <= 3; pass++ {
		baseline, blocked, err := app.bootstrapAmbientContinuity(produced, officeRoomID)
		if err != nil || blocked != ambientContinuityAmbiguous || baseline != "loop-c" {
			t.Fatalf("produced pass %d baseline=%q blocked=%q err=%v, want the block held: a scope that has produced never anchors past unprocessed input",
				pass, baseline, blocked, err)
		}
	}
	if app.repairBlockedAmbientContinuityWithFirstRunAnchor(produced, officeRoomID) {
		t.Fatal("the live pass anchored a scope that has already produced")
	}
	// ...and the same is true of a VISIBLE artifact.
	appendContinuityArtifact(t, app, produced, "loop-produced-visible", officeRoomID, "no-such-brain", false)
	if baseline, blocked, err := app.bootstrapAmbientContinuity(produced, officeRoomID); err != nil || blocked != ambientContinuityAmbiguous || baseline != "loop-c" {
		t.Fatalf("visible-artifact baseline=%q blocked=%q err=%v, want the block held", baseline, blocked, err)
	}
}
