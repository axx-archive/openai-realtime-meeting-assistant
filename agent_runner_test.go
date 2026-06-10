package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
		calls = append(calls, "brain")
		return "## Overview\nBoot Barn shoot confirmed for Friday.", nil
	}

	appendTestTranscript(t, app, "event-1", "Boot Barn shoot confirmed for Friday.")

	result, err := app.archiveMeeting("AJ")
	if err != nil {
		t.Fatalf("archiveMeeting: %v", err)
	}
	if len(calls) != 2 || calls[0] != "brain" || calls[1] != "board" {
		t.Fatalf("calls=%v, want one brain pass then one board pass", calls)
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
