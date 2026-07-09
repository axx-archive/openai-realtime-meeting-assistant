package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestMeetingBrainWorkerWritesSummaryForNewTranscripts(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))
	t.Setenv("OPENAI_BRAIN_MODEL", "gpt-5.5")
	t.Setenv("MEETING_BRAIN_MIN_TRANSCRIPTS", "1")

	app := newKanbanBoardApp()
	if _, appended, err := app.memory.appendAttributedTranscript("event-1", "item-1", "Tom", "dominant", "Boot Barn meeting went well."); err != nil {
		t.Fatalf("append first transcript: %v", err)
	} else if !appended {
		t.Fatal("first transcript appended=false, want true")
	}
	if _, appended, err := app.memory.appendAttributedTranscript("event-2", "item-2", "Tyler", "dominant", "Tom and Tyler agreed to follow up tomorrow."); err != nil {
		t.Fatalf("append second transcript: %v", err)
	} else if !appended {
		t.Fatal("second transcript appended=false, want true")
	}

	entry, err := app.runMeetingBrainOnce(context.Background(), "test-key", func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Model != "gpt-5.5" {
			t.Fatalf("model=%q, want gpt-5.5", request.Model)
		}
		if !strings.Contains(request.Input, "Tom: Boot Barn meeting went well.") {
			t.Fatalf("brain input missing attributed transcript: %s", request.Input)
		}
		return "## Overview\nTom said the Boot Barn meeting went well.\n\n## Transcript reference\n- event-1", nil
	})
	if err != nil {
		t.Fatalf("runMeetingBrainOnce: %v", err)
	}
	if entry.Kind != meetingMemoryKindBrain {
		t.Fatalf("entry kind=%q, want brain", entry.Kind)
	}
	if !strings.Contains(entry.Text, "Boot Barn meeting went well") {
		t.Fatalf("brain text missing summary: %q", entry.Text)
	}
	if entry.Metadata["fromTranscriptId"] != "event-1" || entry.Metadata["throughTranscriptId"] != "event-2" {
		t.Fatalf("brain metadata=%v, want transcript window", entry.Metadata)
	}
	if remaining := app.memory.unsummarizedTranscripts(10); len(remaining) != 0 {
		t.Fatalf("unsummarized transcripts=%d, want 0", len(remaining))
	}
}

// A7: the brain's output budget scales with the transcript window so the
// trailing Transcript-reference section is not truncated mid-word in a dense
// window, and is capped so a large backfill window cannot request an unbounded
// completion.
func TestBrainMaxOutputTokensScalesAndCaps(t *testing.T) {
	if got := brainMaxOutputTokens(0); got != meetingBrainBaseMaxOutputTokens {
		t.Fatalf("empty-window budget=%d, want base %d", got, meetingBrainBaseMaxOutputTokens)
	}
	if got, want := brainMaxOutputTokens(10), meetingBrainBaseMaxOutputTokens+10*meetingBrainPerTranscriptTokens; got != want {
		t.Fatalf("scaled budget=%d, want %d", got, want)
	}
	if got := brainMaxOutputTokens(1_000_000); got != meetingBrainMaxOutputTokensCap {
		t.Fatalf("huge-window budget=%d, want cap %d", got, meetingBrainMaxOutputTokensCap)
	}
	if got := brainMaxOutputTokens(defaultMeetingBrainMaxTranscripts); got <= meetingBrainBaseMaxOutputTokens {
		t.Fatalf("full-window budget=%d must exceed the base %d so the reference tail survives", got, meetingBrainBaseMaxOutputTokens)
	}
}

// A7: the produce path actually requests the scaled budget for its window.
func TestMeetingBrainWorkerRequestsScaledOutputBudget(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))
	t.Setenv("MEETING_BRAIN_MIN_TRANSCRIPTS", "1")

	app := newKanbanBoardApp()
	for i := 0; i < 6; i++ {
		if _, appended, err := app.memory.appendAttributedTranscript(fmt.Sprintf("scale-%d", i), fmt.Sprintf("item-%d", i), "Tom", "dominant", fmt.Sprintf("Boot Barn detail number %d.", i)); err != nil || !appended {
			t.Fatalf("append transcript %d: appended=%v err=%v", i, appended, err)
		}
	}

	var gotBudget int
	if _, err := app.runMeetingBrainOnce(context.Background(), "test-key", func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		gotBudget = request.MaxOutputTokens
		return "## Overview\nsix details.", nil
	}); err != nil {
		t.Fatalf("runMeetingBrainOnce: %v", err)
	}
	if want := brainMaxOutputTokens(6); gotBudget != want {
		t.Fatalf("MaxOutputTokens=%d, want scaled %d for a 6-transcript window", gotBudget, want)
	}
	if gotBudget <= 900 {
		t.Fatalf("MaxOutputTokens=%d, want the dense window above the old flat 900", gotBudget)
	}
}

func TestMeetingBrainWorkerBaselineSkipsHistoricalTranscripts(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))
	t.Setenv("MEETING_BRAIN_MIN_TRANSCRIPTS", "1")

	app := newKanbanBoardApp()
	if _, appended, err := app.memory.appendAttributedTranscript("old-event", "old-item", "Tom", "dominant", "Historical Boot Barn note."); err != nil {
		t.Fatalf("append historical transcript: %v", err)
	} else if !appended {
		t.Fatal("historical transcript appended=false, want true")
	}

	app.setAmbientAgentBaselineID(meetingBrainAgentName, app.memory.latestTranscriptID())
	if entry, err := app.runMeetingBrainOnce(context.Background(), "test-key", func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("responder should not run for historical transcripts before the baseline")
		return "", nil
	}); err != nil {
		t.Fatalf("runMeetingBrainOnce before new transcript: %v", err)
	} else if entry.ID != "" {
		t.Fatalf("entry=%v, want no brain entry before new transcript", entry)
	}

	if _, appended, err := app.memory.appendAttributedTranscript("new-event", "new-item", "Tyler", "dominant", "New follow-up after startup."); err != nil {
		t.Fatalf("append new transcript: %v", err)
	} else if !appended {
		t.Fatal("new transcript appended=false, want true")
	}

	entry, err := app.runMeetingBrainOnce(context.Background(), "test-key", func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if strings.Contains(request.Input, "Historical Boot Barn note") {
			t.Fatalf("brain input included historical transcript before baseline: %s", request.Input)
		}
		if !strings.Contains(request.Input, "Tyler: New follow-up after startup.") {
			t.Fatalf("brain input missing new transcript: %s", request.Input)
		}
		return "## Overview\nTyler added a new follow-up.", nil
	})
	if err != nil {
		t.Fatalf("runMeetingBrainOnce after new transcript: %v", err)
	}
	if entry.Kind != meetingMemoryKindBrain {
		t.Fatalf("entry kind=%q, want brain", entry.Kind)
	}
}
