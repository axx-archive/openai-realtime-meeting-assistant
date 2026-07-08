package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestMeetingBoardWorkerAppliesSummaryUpdatesAndWritesArtifact(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))
	t.Setenv("OPENAI_BOARD_MODEL", "gpt-board")
	t.Setenv("MEETING_BOARD_MIN_SUMMARIES", "1")

	app := newKanbanBoardApp()
	card := app.snapshotState().Cards[0]
	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Follow-ups\n- Tim started the retransmission buffer and it is waiting on packet loss metrics.", map[string]string{
		"fromTranscriptId":    "event-1",
		"throughTranscriptId": "event-3",
	}); err != nil {
		t.Fatalf("append brain write-up: %v", err)
	} else if !appended {
		t.Fatal("brain write-up appended=false, want true")
	}

	entry, err := app.runMeetingBoardOnce(context.Background(), "test-key", func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Model != "gpt-board" {
			t.Fatalf("model=%q, want gpt-board", request.Model)
		}
		if !strings.Contains(request.Input, "brain-1") || !strings.Contains(request.Input, card.ID) {
			t.Fatalf("board worker input missing summary or card context: %s", request.Input)
		}
		return `{
			"summary": "The retransmission buffer moved forward but is blocked on packet loss metrics.",
			"operations": [
				{
					"tool": "update_ticket",
					"reason": "The brain summary names existing retransmission buffer work and a blocker.",
					"arguments": {
						"card_id": "` + card.ID + `",
						"status": "Blocked",
						"owner": "Tim",
						"notes": "Waiting on packet loss metrics.",
						"tags": ["blocked", "metrics"]
					}
				}
			]
		}`, nil
	})
	if err != nil {
		t.Fatalf("runMeetingBoardOnce: %v", err)
	}
	if entry.Kind != meetingMemoryKindBoardUpdate {
		t.Fatalf("entry kind=%q, want board_update", entry.Kind)
	}
	if entry.Metadata["fromBrainId"] != "brain-1" || entry.Metadata["throughBrainId"] != "brain-1" {
		t.Fatalf("board update metadata=%v, want brain window", entry.Metadata)
	}
	if !strings.Contains(entry.Text, "update_ticket changed=true") {
		t.Fatalf("board update artifact missing operation audit: %q", entry.Text)
	}

	updated, ok := findSnapshotCard(app.snapshotState().Cards, card.ID)
	if !ok {
		t.Fatalf("updated card %q not found", card.ID)
	}
	if updated.Status != kanbanStatusBlocked {
		t.Fatalf("status=%q, want Blocked", updated.Status)
	}
	if updated.Owner != "Tim" {
		t.Fatalf("owner=%q, want Tim", updated.Owner)
	}
	if !strings.Contains(updated.Notes, "packet loss metrics") {
		t.Fatalf("notes=%q, want packet loss metrics", updated.Notes)
	}
	if remaining := app.memory.unprocessedBrainWriteUpsAfter(10, ""); len(remaining) != 0 {
		t.Fatalf("unprocessed brain write-ups=%d, want 0", len(remaining))
	}
}

func TestMeetingBoardWorkerBaselineSkipsHistoricalSummaries(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))
	t.Setenv("MEETING_BOARD_MIN_SUMMARIES", "1")

	app := newKanbanBoardApp()
	if _, appended, err := app.memory.appendBrainWriteUp("old-brain", "## Overview\nHistorical summary only.", nil); err != nil {
		t.Fatalf("append historical brain write-up: %v", err)
	} else if !appended {
		t.Fatal("historical brain write-up appended=false, want true")
	}

	app.setAmbientAgentBaselineID(meetingBoardAgentName, app.memory.latestBrainWriteUpID())
	if entry, err := app.runMeetingBoardOnce(context.Background(), "test-key", func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("responder should not run for historical summaries before the baseline")
		return "", nil
	}); err != nil {
		t.Fatalf("runMeetingBoardOnce before new summary: %v", err)
	} else if entry.ID != "" {
		t.Fatalf("entry=%v, want no board update before new summary", entry)
	}

	if _, appended, err := app.memory.appendBrainWriteUp("new-brain", "## Follow-ups\n- No actionable board changes.", nil); err != nil {
		t.Fatalf("append new brain write-up: %v", err)
	} else if !appended {
		t.Fatal("new brain write-up appended=false, want true")
	}

	entry, err := app.runMeetingBoardOnce(context.Background(), "test-key", func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if strings.Contains(request.Input, "old-brain") || strings.Contains(request.Input, "Historical summary only") {
			t.Fatalf("board worker input included historical summary before baseline: %s", request.Input)
		}
		if !strings.Contains(request.Input, "new-brain") {
			t.Fatalf("board worker input missing new summary: %s", request.Input)
		}
		return `{"summary":"No actionable board changes.","operations":[]}`, nil
	})
	if err != nil {
		t.Fatalf("runMeetingBoardOnce after new summary: %v", err)
	}
	if entry.Kind != meetingMemoryKindBoardUpdate {
		t.Fatalf("entry kind=%q, want board_update", entry.Kind)
	}
	if !strings.Contains(entry.Text, "No board operations needed") {
		t.Fatalf("board update artifact=%q, want no-op audit", entry.Text)
	}
}

func TestMeetingBoardWorkerRejectsDestructiveSummaryTools(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))
	t.Setenv("MEETING_BOARD_MIN_SUMMARIES", "1")

	app := newKanbanBoardApp()
	initialCards := app.snapshotState().Cards
	if _, appended, err := app.memory.appendBrainWriteUp("brain-risky", "## Overview\nSomeone mentioned deleting a card, but no explicit board deletion was decided.", nil); err != nil {
		t.Fatalf("append brain write-up: %v", err)
	} else if !appended {
		t.Fatal("brain write-up appended=false, want true")
	}

	entry, err := app.runMeetingBoardOnce(context.Background(), "test-key", func(context.Context, string, openAITextRequest) (string, error) {
		return `{"summary":"Risky operation rejected.","operations":[{"tool":"delete_ticket","arguments":{"card_id":"` + initialCards[0].ID + `"}}]}`, nil
	})
	if err != nil {
		t.Fatalf("runMeetingBoardOnce: %v", err)
	}
	if !strings.Contains(entry.Text, "unsupported board worker tool") {
		t.Fatalf("board update artifact=%q, want unsupported tool audit", entry.Text)
	}
	if got := len(app.snapshotState().Cards); got != len(initialCards) {
		t.Fatalf("card count=%d, want %d", got, len(initialCards))
	}
}

func TestMeetingBoardWorkerToleratesAliasStatusAndMissingCardID(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	existing := app.snapshotState().Cards[0]

	result := app.applyMeetingBoardAnalysis(meetingBoardAnalysis{
		Summary: "Alias status and title-addressed update.",
		Operations: []meetingBoardOperation{
			{
				Tool: "create_ticket",
				Arguments: map[string]any{
					"title":  "Ship the onboarding email",
					"notes":  "Draft and send the onboarding email to the pilot cohort.",
					"owner":  "Unassigned",
					"tags":   []any{"workflow", "email"},
					"status": "To Do",
				},
			},
			{
				Tool: "move_ticket",
				Arguments: map[string]any{
					"title":  existing.Title,
					"status": "Blocked",
				},
			},
		},
	})
	if result.ErrorCount != 0 {
		t.Fatalf("errors=%d (%+v), want 0", result.ErrorCount, result.Applications)
	}
	if result.ChangedCount != 2 {
		t.Fatalf("changed=%d, want 2", result.ChangedCount)
	}

	var created kanbanCard
	found := false
	for _, card := range app.snapshotState().Cards {
		if card.Title == "Ship the onboarding email" {
			created = card
			found = true
			break
		}
	}
	if !found {
		t.Fatal("card created with status \"To Do\" was dropped")
	}
	if created.Status != kanbanStatusBacklog {
		t.Fatalf("created status=%q, want Backlog", created.Status)
	}
	if !created.Draft {
		t.Fatal("worker-created card draft=false, want true")
	}

	moved, ok := findSnapshotCard(app.snapshotState().Cards, existing.ID)
	if !ok || moved.Status != kanbanStatusBlocked {
		t.Fatalf("card %q status=%q, want Blocked via title resolution", existing.ID, moved.Status)
	}
}

func TestMeetingBoardInstructionsEnumerateColumnsAndDraftFlag(t *testing.T) {
	instructions := meetingBoardInstructions()
	for _, want := range []string{"Backlog, In Progress, Blocked, Done", "NOT a status", "card_id"} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("meetingBoardInstructions missing %q", want)
		}
	}
}

func TestParseMeetingBoardAnalysisAcceptsFencedJSON(t *testing.T) {
	analysis, err := parseMeetingBoardAnalysis("```json\n{\"summary\":\"ok\",\"operations\":[{\"tool\":\"do_nothing\",\"arguments\":{\"reason\":\"nothing actionable\"}}]}\n```")
	if err != nil {
		t.Fatalf("parseMeetingBoardAnalysis: %v", err)
	}
	if analysis.Summary != "ok" {
		t.Fatalf("summary=%q, want ok", analysis.Summary)
	}
	if len(analysis.Operations) != 1 || normalizeMeetingBoardToolName(analysis.Operations[0]) != "do_nothing" {
		t.Fatalf("operations=%v, want one do_nothing", analysis.Operations)
	}
}
