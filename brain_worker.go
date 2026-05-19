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
	defaultMeetingBrainInterval       = 5 * time.Minute
	defaultMeetingBrainMinTranscripts = 4
	defaultMeetingBrainMaxTranscripts = 80
	meetingBrainRequestTimeout        = 90 * time.Second
)

func (app *kanbanBoardApp) startMeetingBrainWorker(apiKey string) {
	if app == nil || app.memory == nil || strings.TrimSpace(apiKey) == "" || boolEnv("MEETING_BRAIN_DISABLED") {
		return
	}
	interval := meetingBrainInterval()
	if interval <= 0 {
		return
	}

	cancel := make(chan struct{})
	done := make(chan struct{})
	baselineID := ""
	if !meetingBrainBackfillEnabled() {
		baselineID = app.memory.latestTranscriptID()
	}

	app.mu.Lock()
	oldCancel := app.brainWorkerCancel
	oldDone := app.brainWorkerDone
	app.brainWorkerCancel = cancel
	app.brainWorkerDone = done
	app.brainWorkerBaselineID = baselineID
	app.mu.Unlock()

	if oldCancel != nil {
		close(oldCancel)
		if oldDone != nil {
			<-oldDone
		}
	}

	go app.runMeetingBrainWorker(apiKey, interval, cancel, done)
}

func (app *kanbanBoardApp) runMeetingBrainWorker(apiKey string, interval time.Duration, cancel <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ctx, cancelRequest := context.WithTimeout(context.Background(), meetingBrainRequestTimeout)
			if _, err := app.runMeetingBrainOnce(ctx, apiKey, createOpenAITextResponse); err != nil {
				log.Errorf("Meeting brain worker failed: %v", err)
			}
			cancelRequest()
		case <-cancel:
			return
		}
	}
}

func (app *kanbanBoardApp) runMeetingBrainOnce(ctx context.Context, apiKey string, responder openAITextResponder) (meetingMemoryEntry, error) {
	if app == nil || app.memory == nil {
		return meetingMemoryEntry{}, nil
	}
	if responder == nil {
		responder = createOpenAITextResponse
	}

	app.mu.Lock()
	baselineID := app.brainWorkerBaselineID
	app.mu.Unlock()

	transcripts := app.memory.unsummarizedTranscriptsAfter(meetingBrainMaxTranscripts(), baselineID)
	if len(transcripts) < meetingBrainMinTranscripts() {
		return meetingMemoryEntry{}, nil
	}

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

func meetingBrainInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("MEETING_BRAIN_INTERVAL"))
	if raw == "" {
		return defaultMeetingBrainInterval
	}
	switch strings.ToLower(raw) {
	case "0", "off", "false", "disabled":
		return 0
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval < time.Second {
		return defaultMeetingBrainInterval
	}

	return interval
}

func meetingBrainMinTranscripts() int {
	return positiveIntEnv("MEETING_BRAIN_MIN_TRANSCRIPTS", defaultMeetingBrainMinTranscripts)
}

func meetingBrainBackfillEnabled() bool {
	return boolEnv("MEETING_BRAIN_BACKFILL")
}

func meetingBrainMaxTranscripts() int {
	return positiveIntEnv("MEETING_BRAIN_MAX_TRANSCRIPTS", defaultMeetingBrainMaxTranscripts)
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
	if store == nil || limit <= 0 {
		return nil
	}

	store.mu.Lock()
	entries := cloneMemoryEntries(store.entries)
	store.mu.Unlock()

	startIndex := 0
	baselineTranscriptID = strings.TrimSpace(baselineTranscriptID)
	if baselineTranscriptID != "" {
		for index := len(entries) - 1; index >= 0; index-- {
			if entries[index].ID == baselineTranscriptID {
				startIndex = index + 1
				break
			}
		}
	}
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if entry.Kind != meetingMemoryKindBrain {
			continue
		}
		throughTranscriptID := strings.TrimSpace(entry.Metadata["throughTranscriptId"])
		if throughTranscriptID != "" {
			for transcriptIndex := len(entries) - 1; transcriptIndex >= 0; transcriptIndex-- {
				if entries[transcriptIndex].ID == throughTranscriptID {
					if transcriptIndex+1 > startIndex {
						startIndex = transcriptIndex + 1
					}
					break
				}
			}
		} else {
			if index+1 > startIndex {
				startIndex = index + 1
			}
		}
		break
	}

	transcripts := make([]meetingMemoryEntry, 0, limit)
	for _, entry := range entries[startIndex:] {
		if entry.Kind != meetingMemoryKindTranscript {
			continue
		}
		transcripts = append(transcripts, entry)
		if len(transcripts) >= limit {
			break
		}
	}

	return transcripts
}

func (store *meetingMemoryStore) latestTranscriptID() string {
	if store == nil {
		return ""
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	for index := len(store.entries) - 1; index >= 0; index-- {
		if store.entries[index].Kind == meetingMemoryKindTranscript {
			return store.entries[index].ID
		}
	}

	return ""
}
