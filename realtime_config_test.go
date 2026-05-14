package main

import (
	"path/filepath"
	"testing"
)

func TestRealtimeSessionConfigUsesGptRealtime2Optimizations(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("OPENAI_REALTIME_REASONING_EFFORT", "")
	t.Setenv("OPENAI_REALTIME_VAD_TYPE", "")
	t.Setenv("OPENAI_REALTIME_VAD_EAGERNESS", "")

	app := newKanbanBoardApp()
	session := app.sessionConfig("gpt-realtime-2")

	reasoning, ok := session["reasoning"].(map[string]any)
	if !ok {
		t.Fatal("session reasoning config missing")
	}
	if effort := reasoning["effort"]; effort != defaultReasoningEffort {
		t.Fatalf("reasoning effort=%v, want %s", effort, defaultReasoningEffort)
	}

	audio := session["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	turnDetection := input["turn_detection"].(map[string]any)
	if vadType := turnDetection["type"]; vadType != "semantic_vad" {
		t.Fatalf("turn_detection.type=%v, want semantic_vad", vadType)
	}
	if eagerness := turnDetection["eagerness"]; eagerness != defaultVADEagerness {
		t.Fatalf("turn_detection.eagerness=%v, want %s", eagerness, defaultVADEagerness)
	}
}

func TestRealtimeConfigEnvironmentOverrides(t *testing.T) {
	t.Setenv("OPENAI_REALTIME_REASONING_EFFORT", "high")
	t.Setenv("OPENAI_REALTIME_VAD_TYPE", "server_vad")
	t.Setenv("OPENAI_REALTIME_VAD_EAGERNESS", "low")

	if effort := realtimeReasoningEffort(); effort != "high" {
		t.Fatalf("reasoning effort=%q, want high", effort)
	}
	turnDetection := realtimeTurnDetectionConfig()
	if vadType := turnDetection["type"]; vadType != "server_vad" {
		t.Fatalf("turn_detection.type=%v, want server_vad", vadType)
	}
	if _, ok := turnDetection["eagerness"]; ok {
		t.Fatal("server_vad config should not include semantic eagerness")
	}
}

func TestUpdateTicketAppliesRichRealtimeChangesAtomically(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))

	app := newKanbanBoardApp()
	createResult, changed, err := app.createTicket(map[string]any{
		"title":  "Billing launch",
		"notes":  "Initial rollout task.",
		"owner":  "AJ",
		"tags":   []any{"billing"},
		"status": "Backlog",
	})
	if err != nil {
		t.Fatalf("createTicket: %v", err)
	}
	if !changed {
		t.Fatal("createTicket changed=false, want true")
	}
	card := createResult["card"].(kanbanCard)

	if _, changed, err := app.updateTicket(map[string]any{
		"card_id": card.ID,
		"notes":   "Blocked by finance approval.",
		"owner":   "Tim",
		"tags":    []any{"blocked", "risk"},
		"status":  "Blocked",
	}); err != nil {
		t.Fatalf("updateTicket: %v", err)
	} else if !changed {
		t.Fatal("updateTicket changed=false, want true")
	}

	updated, ok := findSnapshotCard(app.snapshotState().Cards, card.ID)
	if !ok {
		t.Fatalf("updated card %q not found", card.ID)
	}
	if updated.Status != kanbanStatusBlocked {
		t.Fatalf("status=%q, want %q", updated.Status, kanbanStatusBlocked)
	}
	if updated.Owner != "Tim" {
		t.Fatalf("owner=%q, want Tim", updated.Owner)
	}
	if updated.Notes != "Blocked by finance approval." {
		t.Fatalf("notes=%q, want blocker note", updated.Notes)
	}
	if got, want := updated.Tags, []string{"billing", "blocked", "risk"}; !sameStringSlice(got, want) {
		t.Fatalf("tags=%v, want %v", got, want)
	}
}

func sameStringSlice(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

func findSnapshotCard(cards []kanbanCard, cardID string) (kanbanCard, bool) {
	for _, card := range cards {
		if card.ID == cardID {
			return card, true
		}
	}

	return kanbanCard{}, false
}
