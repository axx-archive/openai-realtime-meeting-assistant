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

func TestContextEntriesForQueryUnderstandsRecentDuration(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	location := meetingTimeLocation()
	now := time.Date(2026, 5, 19, 10, 0, 0, 0, location)
	store.entries = []meetingMemoryEntry{
		{
			ID:        "ten-minutes-ago",
			Kind:      meetingMemoryKindTranscript,
			Text:      "Tom: Boot Barn follow-up sounded positive.",
			CreatedAt: now.Add(-10 * time.Minute).UTC(),
			Metadata:  map[string]string{"speaker": "Tom"},
		},
		{
			ID:        "twenty-minutes-ago",
			Kind:      meetingMemoryKindTranscript,
			Text:      "Tyler: Older unrelated topic.",
			CreatedAt: now.Add(-20 * time.Minute).UTC(),
			Metadata:  map[string]string{"speaker": "Tyler"},
		},
		{
			ID:        "one-minute-ago",
			Kind:      meetingMemoryKindTranscript,
			Text:      "AJ: New unrelated topic.",
			CreatedAt: now.Add(-1 * time.Minute).UTC(),
			Metadata:  map[string]string{"speaker": "AJ"},
		},
	}

	entries := store.contextEntriesForQuery("what happened 10 minutes ago?", 10, now)
	if !memoryEntriesContain(entries, "ten-minutes-ago") {
		t.Fatal("context missing transcript from around 10 minutes ago")
	}
	if memoryEntriesContain(entries, "twenty-minutes-ago") {
		t.Fatal("context should not include transcript from 20 minutes ago")
	}
	if memoryEntriesContain(entries, "one-minute-ago") {
		t.Fatal("context should not include transcript from 1 minute ago for a 10-minutes-ago query")
	}
}

func TestContextEntriesForQueryUnderstandsPastDuration(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "memory.jsonl"))
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}

	location := meetingTimeLocation()
	now := time.Date(2026, 5, 19, 10, 0, 0, 0, location)
	store.entries = []meetingMemoryEntry{
		{
			ID:        "recent",
			Kind:      meetingMemoryKindTranscript,
			Text:      "Tom: Recent Boot Barn note.",
			CreatedAt: now.Add(-5 * time.Minute).UTC(),
			Metadata:  map[string]string{"speaker": "Tom"},
		},
		{
			ID:        "old",
			Kind:      meetingMemoryKindTranscript,
			Text:      "Tom: Older Boot Barn note.",
			CreatedAt: now.Add(-15 * time.Minute).UTC(),
			Metadata:  map[string]string{"speaker": "Tom"},
		},
	}

	entries := store.contextEntriesForQuery("what did Tom say in the last 10 minutes?", 10, now)
	if !memoryEntriesContain(entries, "recent") {
		t.Fatal("context missing recent transcript")
	}
	if memoryEntriesContain(entries, "old") {
		t.Fatal("context should not include transcript older than the requested recent window")
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

// Completed artifacts earn a real display title from the body: first markdown
// heading wins, then a "Title:" line, then a short first line — mode scaffold
// openers never become titles, and an unusable body keeps the fallback.
func TestArtifactTitleFromBody(t *testing.T) {
	for _, tt := range []struct {
		name     string
		body     string
		fallback string
		want     string
	}{
		{
			name:     "first heading wins and sheds punctuation",
			body:     "## Coyote pricing teardown.\n\nEvidence follows.",
			fallback: "dig into coyote pricing",
			want:     "Coyote pricing teardown",
		},
		{
			name:     "scaffold opener heading is skipped for the real one",
			body:     "# Scout work thread\n\n## Realtime margin audit\n\nbody",
			fallback: "prompt",
			want:     "Realtime margin audit",
		},
		{
			name:     "title line beats the first plain line",
			body:     "Research brief\n\nTitle: Q3 pipeline reconciliation\n\nDetails.",
			fallback: "prompt",
			want:     "Q3 pipeline reconciliation",
		},
		{
			name:     "short first line is a title",
			body:     "Margin plan for Q3\n\nLong details follow here.",
			fallback: "prompt",
			want:     "Margin plan for Q3",
		},
		{
			name:     "overlong first line keeps the fallback",
			body:     strings.Repeat("margin ", 20) + "\n\nbody",
			fallback: "the original prompt",
			want:     "the original prompt",
		},
		{
			name:     "scaffold-only body keeps the fallback",
			body:     "Scout work thread\n\nStatus: running",
			fallback: "the original prompt",
			want:     "the original prompt",
		},
		{
			name:     "empty body keeps the fallback",
			body:     "",
			fallback: "the original prompt",
			want:     "the original prompt",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := artifactTitleFromBody(tt.body, tt.fallback); got != tt.want {
				t.Fatalf("artifactTitleFromBody=%q, want %q", got, tt.want)
			}
		})
	}
}

// Scout retrieval: a completed artifact whose title matches the query enters
// query context truncated to budget (with the full-artifact marker), while a
// running scaffold with the same subject stays out.
func TestContextEntriesForQueryBudgetsCompletedArtifactBodies(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	longBody := "# Coyote pricing teardown\n\n" + strings.Repeat("Carrier margin evidence with routing detail. ", 80)
	completed, _, err := app.createOSArtifactWithMetadata("research", "coyote pricing", longBody, "AJ", map[string]string{
		"title":        "Coyote pricing teardown",
		"threadStatus": "complete",
		"status":       "complete",
	})
	if err != nil {
		t.Fatalf("create completed artifact: %v", err)
	}
	running, _, err := app.createOSArtifactWithMetadata("research", "coyote pricing second pass", "Scout work thread\n\nVision: coyote pricing second pass", "AJ", map[string]string{
		"title":        "Coyote pricing second pass",
		"threadStatus": "running",
		"status":       "running",
	})
	if err != nil {
		t.Fatalf("create running scaffold: %v", err)
	}

	entries := app.memory.contextEntriesForQuery("what did the coyote pricing teardown conclude", 12, time.Now())
	var artifactEntry *meetingMemoryEntry
	for index := range entries {
		if entries[index].ID == completed.ID {
			artifactEntry = &entries[index]
		}
		if entries[index].ID == running.ID {
			t.Fatal("running scaffold must not enter query context")
		}
	}
	if artifactEntry == nil {
		t.Fatalf("completed artifact %s missing from context entries", completed.ID)
	}
	if len([]rune(artifactEntry.Text)) >= len([]rune(longBody)) {
		t.Fatalf("artifact context len=%d, want truncated below the raw body (%d)", len(artifactEntry.Text), len(longBody))
	}
	if !strings.Contains(artifactEntry.Text, "[truncated — full artifact id="+completed.ID) {
		t.Fatalf("artifact context missing the truncation marker: %q", artifactEntry.Text[len(artifactEntry.Text)-160:])
	}
}

// Recall/report-flavored questions that name a completed artifact at title
// strength skip the board short-circuit so the model answers from the
// artifact body; plain board questions keep the fast path.
func TestQueryPrefersArtifactContext(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, _, err := app.createOSArtifactWithMetadata("research", "reconcile coyote pricing against the q3 board", "# Coyote pricing teardown\n\nEvidence.", "AJ", map[string]string{
		"title":        "Coyote pricing teardown",
		"threadStatus": "complete",
		"status":       "complete",
	}); err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	if !app.queryPrefersArtifactContext("compare the coyote pricing teardown with the q3 board figures") {
		t.Fatal("artifact-naming comparison question must prefer artifact context")
	}
	if app.queryPrefersArtifactContext("what is on the board right now") {
		t.Fatal("plain board question must keep the board short-circuit")
	}
	if app.queryPrefersArtifactContext("compare the roadmap cards") {
		t.Fatal("flavored question naming no artifact must keep the board short-circuit")
	}
}
