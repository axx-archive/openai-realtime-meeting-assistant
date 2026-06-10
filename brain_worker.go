package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	meetingBrainAgentName             = "meeting brain"
	defaultMeetingBrainInterval       = 5 * time.Minute
	defaultMeetingBrainMinTranscripts = 4
	defaultMeetingBrainMaxTranscripts = 80
	meetingBrainRequestTimeout        = 90 * time.Second
)

func meetingBrainAgent() ambientAgentConfig {
	return ambientAgentConfig{
		name:              meetingBrainAgentName,
		defaultInterval:   defaultMeetingBrainInterval,
		intervalEnv:       "MEETING_BRAIN_INTERVAL",
		disabledEnv:       "MEETING_BRAIN_DISABLED",
		backfillEnv:       "MEETING_BRAIN_BACKFILL",
		minBatchEnv:       "MEETING_BRAIN_MIN_TRANSCRIPTS",
		defaultMinBatch:   defaultMeetingBrainMinTranscripts,
		maxBatchEnv:       "MEETING_BRAIN_MAX_TRANSCRIPTS",
		defaultMaxBatch:   defaultMeetingBrainMaxTranscripts,
		inputKind:         meetingMemoryKindTranscript,
		artifactKind:      meetingMemoryKindBrain,
		cursorMetadataKey: "throughTranscriptId",
		requestTimeout:    meetingBrainRequestTimeout,
		produce:           (*kanbanBoardApp).produceMeetingBrainWriteUp,
	}
}

func (app *kanbanBoardApp) startMeetingBrainWorker(apiKey string) {
	app.startAmbientAgent(meetingBrainAgent(), apiKey)
}

func (app *kanbanBoardApp) runMeetingBrainOnce(ctx context.Context, apiKey string, responder openAITextResponder) (meetingMemoryEntry, error) {
	agent := meetingBrainAgent()
	return app.runAmbientAgentOnce(agent, ctx, apiKey, responder, agent.minBatch())
}

func (app *kanbanBoardApp) produceMeetingBrainWriteUp(ctx context.Context, apiKey string, transcripts []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error) {
	model := meetingBrainModel()
	text, err := responder(ctx, apiKey, openAITextRequest{
		Model:           model,
		Instructions:    meetingBrainInstructions(),
		Input:           buildMeetingBrainInput(transcripts, app.snapshotState(), app.participantSnapshot(), time.Now().UTC()),
		ReasoningEffort: "low",
		Verbosity:       "low",
		MaxOutputTokens: 900,
	})
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return meetingMemoryEntry{}, nil
	}

	firstTranscript := transcripts[0]
	lastTranscript := transcripts[len(transcripts)-1]
	metadata := map[string]string{
		"source":                     "openai_responses",
		"model":                      model,
		"fromTranscriptId":           firstTranscript.ID,
		"throughTranscriptId":        lastTranscript.ID,
		"fromTranscriptCreatedAt":    firstTranscript.CreatedAt.Format(time.RFC3339Nano),
		"throughTranscriptCreatedAt": lastTranscript.CreatedAt.Format(time.RFC3339Nano),
		"transcriptCount":            strconv.Itoa(len(transcripts)),
	}
	id := fmt.Sprintf("brain-%s", time.Now().UTC().Format("20060102-150405-000000000"))
	entry, appended, err := app.memory.appendBrainWriteUp(id, text, metadata)
	if err != nil || !appended {
		return entry, err
	}

	broadcastKanbanEvent("memory_brain", entry)
	broadcastAssistantEvent("action", "Scout updated the room brain.", map[string]any{"kind": meetingMemoryKindBrain})

	return entry, nil
}

func positiveIntEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return fallback
	}

	return value
}

func meetingBrainInstructions() string {
	return strings.Join([]string{
		"You are Scout's durable meeting brain for Bonfire.",
		"Create a faithful, high-signal memory write-up from the supplied transcript window.",
		"Preserve who said what. Do not invent facts, participants, clients, dates, decisions, or action items.",
		"When the transcript is unclear, say it is unclear instead of guessing.",
		"Write compact markdown with these sections: Overview, People, Topics, Decisions, Follow-ups, Project and client notes, Transcript reference.",
		"Keep the Transcript reference brief but include the transcript IDs that matter so raw transcript entries can be checked later.",
	}, " ")
}

func buildMeetingBrainInput(transcripts []meetingMemoryEntry, board kanbanBoardState, participants []string, generatedAt time.Time) string {
	var builder strings.Builder
	builder.WriteString("# Generated at\n")
	builder.WriteString(generatedAt.Format(time.RFC3339))
	builder.WriteString("\n\n# Active participants\n")
	if len(participants) == 0 {
		builder.WriteString("Unknown\n")
	} else {
		builder.WriteString(strings.Join(participants, ", "))
		builder.WriteByte('\n')
	}

	builder.WriteString("\n# Current board snapshot\n")
	if rawBoard, err := json.Marshal(board.Cards); err == nil {
		builder.WriteString(string(rawBoard))
	} else {
		builder.WriteString("[]")
	}

	builder.WriteString("\n\n# Transcript window\n")
	for _, entry := range transcripts {
		builder.WriteString("- ")
		builder.WriteString(entry.ID)
		builder.WriteString(" | ")
		builder.WriteString(entry.CreatedAt.Format(time.RFC3339))
		if speaker := strings.TrimSpace(entry.Metadata["speaker"]); speaker != "" {
			builder.WriteString(" | speaker=")
			builder.WriteString(speaker)
		}
		builder.WriteString(" | ")
		builder.WriteString(entry.Text)
		builder.WriteByte('\n')
	}

	return builder.String()
}

func (store *meetingMemoryStore) unsummarizedTranscripts(limit int) []meetingMemoryEntry {
	return store.unsummarizedTranscriptsAfter(limit, "")
}

func (store *meetingMemoryStore) unsummarizedTranscriptsAfter(limit int, baselineTranscriptID string) []meetingMemoryEntry {
	return store.unconsumedEntriesAfter(meetingMemoryKindTranscript, meetingMemoryKindBrain, "throughTranscriptId", limit, baselineTranscriptID)
}

func (store *meetingMemoryStore) latestTranscriptID() string {
	return store.latestEntryIDOfKind(meetingMemoryKindTranscript)
}
