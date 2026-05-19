package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestContextEntriesForQueryIncludesMentionedParticipantsAndYesterday(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	location := meetingTimeLocation()
	now := time.Date(2026, 5, 19, 10, 0, 0, 0, location)
	yesterday := time.Date(2026, 5, 18, 15, 0, 0, 0, location).UTC()
	twoDaysAgo := time.Date(2026, 5, 17, 15, 0, 0, 0, location).UTC()

	store.entries = []meetingMemoryEntry{
		{
			ID:        "tom-yesterday",
			Kind:      meetingMemoryKindTranscript,
			Text:      "Tom: Boot Barn meeting went well.",
			CreatedAt: yesterday,
			Metadata:  map[string]string{"speaker": "Tom"},
		},
		{
			ID:        "tyler-yesterday",
			Kind:      meetingMemoryKindTranscript,
			Text:      "Tyler: Tom and Tyler talked about next steps.",
			CreatedAt: yesterday.Add(10 * time.Minute),
			Metadata:  map[string]string{"speaker": "Tyler"},
		},
		{
			ID:        "tom-old",
			Kind:      meetingMemoryKindTranscript,
			Text:      "Tom: Older unrelated update.",
			CreatedAt: twoDaysAgo,
			Metadata:  map[string]string{"speaker": "Tom"},
		},
	}

	entries := store.contextEntriesForQuery("what did Tom and Tyler talk about yesterday?", 10, now)
	if !memoryEntriesContain(entries, "tom-yesterday") {
		t.Fatal("context missing Tom's yesterday transcript")
	}
	if !memoryEntriesContain(entries, "tyler-yesterday") {
		t.Fatal("context missing Tyler's yesterday transcript")
	}
	if memoryEntriesContain(entries, "tom-old") {
		t.Fatal("context should keep yesterday-scoped questions inside yesterday's transcript window")
	}
}

func TestAssistantQueryAnswersCurrentBoardCardStatus(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	if _, changed, err := app.createTicket(map[string]any{
		"title":  "Dog Perfect",
		"notes":  "Waiting on Erick for launch approval.",
		"owner":  "Erick",
		"tags":   []any{"client", "approval"},
		"status": "Blocked",
	}); err != nil {
		t.Fatalf("createTicket: %v", err)
	} else if !changed {
		t.Fatal("createTicket changed=false, want true")
	}

	result, changed, err := app.answerAssistantQuery("what is the current status of DogPerfect?")
	if err != nil {
		t.Fatalf("answerAssistantQuery: %v", err)
	}
	if changed {
		t.Fatal("answerAssistantQuery changed=true, want false")
	}

	answer := asString(result["answer"])
	for _, want := range []string{"Dog Perfect", "Blocked", "Erick"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("answer=%q, missing %q", answer, want)
		}
	}
	if source := asString(result["source"]); source != "board" {
		t.Fatalf("source=%q, want board", source)
	}
}

func memoryEntriesContain(entries []meetingMemoryEntry, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}

	return false
}
