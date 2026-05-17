package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoardMutationsNoopWhenStateAlreadyMatches(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	card := app.snapshotState().Cards[0]
	initialUpdatedAt := app.snapshotState().UpdatedAt

	if _, changed, err := app.moveTicket(map[string]any{
		"card_id": card.ID,
		"status":  string(card.Status),
	}); err != nil {
		t.Fatalf("moveTicket: %v", err)
	} else if changed {
		t.Fatal("moveTicket changed=true for matching status")
	}

	if _, changed, err := app.addTags(map[string]any{
		"card_id": card.ID,
		"tags":    []any{card.Tags[0]},
	}); err != nil {
		t.Fatalf("addTags duplicate: %v", err)
	} else if changed {
		t.Fatal("addTags changed=true for duplicate tag")
	}

	if _, changed, err := app.updateTicket(map[string]any{
		"card_id": card.ID,
		"title":   card.Title,
		"notes":   card.Notes,
		"owner":   card.Owner,
		"status":  string(card.Status),
		"tags":    []any{card.Tags[0]},
	}); err != nil {
		t.Fatalf("updateTicket matching fields: %v", err)
	} else if changed {
		t.Fatal("updateTicket changed=true for matching fields")
	}

	if _, changed, err := app.updateTicketDetails(map[string]any{
		"card_id": card.ID,
		"title":   card.Title,
		"notes":   card.Notes,
		"owner":   card.Owner,
		"status":  string(card.Status),
		"tags":    stringsToAny(card.Tags),
	}); err != nil {
		t.Fatalf("updateTicketDetails matching fields: %v", err)
	} else if changed {
		t.Fatal("updateTicketDetails changed=true for matching fields")
	}

	if updatedAt := app.snapshotState().UpdatedAt; updatedAt != initialUpdatedAt {
		t.Fatalf("updatedAt=%q after no-op mutations, want %q", updatedAt, initialUpdatedAt)
	}
}

func TestAddTagsRequiresAtLeastOneTag(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	card := app.snapshotState().Cards[0]

	if _, _, err := app.addTags(map[string]any{
		"card_id": card.ID,
		"tags":    []any{"  "},
	}); err == nil {
		t.Fatal("addTags accepted an empty tag set")
	}
}

func TestForwardedTrackLocalIDIsCollisionResistant(t *testing.T) {
	first := forwardedTrackLocalID("camera stream", "video", 123)
	second := forwardedTrackLocalID("camera stream", "video", 456)

	if first == second {
		t.Fatalf("forwarded ids collided: %q", first)
	}
	if strings.Contains(first, " ") {
		t.Fatalf("forwarded id contains whitespace: %q", first)
	}
	if !strings.Contains(first, "camera_stream:video:123") {
		t.Fatalf("forwarded id=%q, want stream, track, and SSRC", first)
	}
}

func TestWriteMeetingArchiveCreatesDirectoryAndWritesJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "archive.json")
	archive := meetingArchive{ID: "meeting-test"}

	if err := writeMeetingArchive(path, archive); err != nil {
		t.Fatalf("writeMeetingArchive: %v", err)
	}

	rawArchive, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if !strings.HasSuffix(string(rawArchive), "\n") {
		t.Fatalf("archive JSON does not end with newline: %q", rawArchive)
	}

	var decoded meetingArchive
	if err := json.Unmarshal(rawArchive, &decoded); err != nil {
		t.Fatalf("decode archive: %v", err)
	}
	if decoded.ID != archive.ID {
		t.Fatalf("archive id=%q, want %q", decoded.ID, archive.ID)
	}
}

func newIsolatedKanbanBoardApp(t *testing.T) *kanbanBoardApp {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))

	return newKanbanBoardApp()
}

func stringsToAny(values []string) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}

	return result
}
