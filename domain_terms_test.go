package main

import (
	"strings"
	"testing"
)

func TestCanonicalizeDomainTermsCorrectsKnownMishearings(t *testing.T) {
	got := canonicalizeDomainTerms("Suit Barn needs Web RTC support for H E V C over R T P.")
	for _, want := range []string{"Boot Barn", "WebRTC", "HEVC", "RTP"} {
		if !strings.Contains(got, want) {
			t.Fatalf("canonicalized text %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "Suit Barn") {
		t.Fatalf("canonicalized text still contains Suit Barn: %q", got)
	}
}

func TestCardToolsCanonicalizeDomainTerms(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", t.TempDir()+"/memory.jsonl")
	t.Setenv("KANBAN_BOARD_PATH", t.TempDir()+"/board.json")

	app := newKanbanBoardApp()
	result, changed, err := app.createTicket(map[string]any{
		"title":  "Suit Barn launch",
		"notes":  "Open AI Web RTC follow-up for H E V C",
		"owner":  "AJ",
		"tags":   []any{"suit barn", "web rtc"},
		"status": "Backlog",
	})
	if err != nil {
		t.Fatalf("createTicket: %v", err)
	}
	if !changed {
		t.Fatal("createTicket changed=false, want true")
	}

	card := result["card"].(kanbanCard)
	if card.Title != "Boot Barn launch" {
		t.Fatalf("title=%q, want Boot Barn launch", card.Title)
	}
	if !strings.Contains(card.Notes, "OpenAI WebRTC") || !strings.Contains(card.Notes, "HEVC") {
		t.Fatalf("notes did not preserve canonical technical terms: %q", card.Notes)
	}
	if got, want := card.Tags, []string{"Boot Barn", "WebRTC"}; !sameStringSlice(got, want) {
		t.Fatalf("tags=%v, want %v", got, want)
	}
}
