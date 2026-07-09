package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	if _, _, _, ok := app.peekUnconsumedWindow(agent); ok {
		t.Fatal("empty store should report no window")
	}
	appendTestTranscript(t, app, "peek-1", "Boot Barn kickoff.")
	head, count, age, ok := app.peekUnconsumedWindow(agent)
	if !ok || head != "peek-1" || count != 1 {
		t.Fatalf("peek=%q/%d/%v, want peek-1/1/true", head, count, ok)
	}
	if age < 0 {
		t.Fatalf("age=%v, want >= 0", age)
	}
	// The peek must not consume: a real pass still sees the input.
	if _, _, _, ok := app.peekUnconsumedWindow(agent); !ok {
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
	head, _, _, ok := app.peekUnconsumedWindow(agent)
	if !ok || head != "fail-1" {
		t.Fatalf("peek head=%q ok=%v, want fail-1", head, ok)
	}

	// Fresh window: full batch, may proceed.
	if proceed, limit := app.ambientAgentAttemptBudget(agent, head); !proceed || limit != agent.maxBatch() {
		t.Fatalf("fresh budget=%v/%d, want true/%d", proceed, limit, agent.maxBatch())
	}

	// One failure arms a backoff that holds off the immediate retry.
	app.recordAmbientAgentFailure(agent, head)
	if proceed, _ := app.ambientAgentAttemptBudget(agent, head); proceed {
		t.Fatal("expected an armed backoff to hold off the immediate retry")
	}

	// Past the backoff, one prior attempt halves the batch (maxBatch 3 -> 2).
	app.mu.Lock()
	app.agentFailures[agent.name] = &ambientAgentFailure{windowID: head, attempts: 1, backoffUntil: time.Now().Add(-time.Second)}
	app.mu.Unlock()
	if proceed, limit := app.ambientAgentAttemptBudget(agent, head); !proceed || limit != 2 {
		t.Fatalf("halved budget=%v/%d, want true/2", proceed, limit)
	}

	// Reaching the attempt cap dead-letters the head: baseline advances past it
	// and the failure record clears. Start from a clean slate so exactly
	// ambientAgentMaxWindowAttempts failures accrue (a further failure would
	// re-open a fresh record on the now-skipped head).
	app.clearAmbientAgentFailure(agent.name)
	for i := 0; i < ambientAgentMaxWindowAttempts; i++ {
		app.recordAmbientAgentFailure(agent, head)
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
	if newHead, _, _, ok := app.peekUnconsumedWindow(agent); !ok || newHead == head {
		t.Fatalf("post-dead-letter head=%q ok=%v, want the poison head skipped", newHead, ok)
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
	t.Setenv("MEETING_BRAIN_MIN_TRANSCRIPTS", "4")
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	var calls []string
	originalResponder := createOpenAITextResponse
	defer func() { createOpenAITextResponse = originalResponder }()
	createOpenAITextResponse = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
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
			return cannedMeetingDigestJSON(), nil
		}
		if strings.Contains(request.Instructions, "company digest narrator") {
			calls = append(calls, "company")
			return "The Zebra packaging pilot is decided.", nil
		}
		if strings.Contains(request.Instructions, "entity-ledger adjudicator") || strings.Contains(request.Instructions, "end-of-day reflection") {
			t.Errorf("unexpected model call at archive flush: %s", request.Instructions)
			return "", nil
		}
		calls = append(calls, "brain")
		return "## Overview\nBoot Barn shoot confirmed for Friday.", nil
	}

	appendTestTranscript(t, app, "event-1", "Boot Barn shoot confirmed for Friday.")

	result, err := app.archiveMeeting("AJ")
	if err != nil {
		t.Fatalf("archiveMeeting: %v", err)
	}
	// the close chain in dependency order; the day fold and the entity-ledger
	// consolidation are deterministic (no model call).
	if strings.Join(calls, ",") != "brain,ledger,board,mission,narrative,digest,company" {
		t.Fatalf("calls=%v, want brain, decision-ledger, board, mission, narrative, meeting-digest, then company", calls)
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
	if !kinds[meetingMemoryKindBrain] || !kinds[meetingMemoryKindBoardUpdate] {
		t.Fatalf("archive memory kinds=%v, want flushed brain and board_update artifacts in the snapshot", kinds)
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
			return cannedMeetingDigestJSON(), nil
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
	if strings.Join(calls, ",") != "brain,ledger,board,mission,narrative,digest,company" {
		t.Fatalf("calls=%v, want brain, decision-ledger, board, mission, narrative, meeting-digest, then company for post-boot input", calls)
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
