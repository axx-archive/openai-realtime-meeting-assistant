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

func TestParseKanbanStatusAcceptsAliasesCaseInsensitively(t *testing.T) {
	cases := map[string]kanbanStatus{
		"Backlog":     kanbanStatusBacklog,
		"backlog":     kanbanStatusBacklog,
		"To Do":       kanbanStatusBacklog,
		"Todo":        kanbanStatusBacklog,
		"to-do":       kanbanStatusBacklog,
		"TO  DO":      kanbanStatusBacklog,
		"Draft":       kanbanStatusBacklog,
		"new":         kanbanStatusBacklog,
		"In Progress": kanbanStatusInProgress,
		"in progress": kanbanStatusInProgress,
		"In-Progress": kanbanStatusInProgress,
		"doing":       kanbanStatusInProgress,
		"WIP":         kanbanStatusInProgress,
		"Blocked":     kanbanStatusBlocked,
		"blocker":     kanbanStatusBlocked,
		"Done":        kanbanStatusDone,
		"DONE":        kanbanStatusDone,
		"complete":    kanbanStatusDone,
		"Completed":   kanbanStatusDone,
		"finished":    kanbanStatusDone,
		"shipped":     kanbanStatusDone,
		" To Do \n":   kanbanStatusBacklog,
	}
	for input, want := range cases {
		got, err := parseKanbanStatus(input)
		if err != nil {
			t.Fatalf("parseKanbanStatus(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("parseKanbanStatus(%q)=%q, want %q", input, got, want)
		}
	}

	for _, input := range []string{"", "someday", "column 5"} {
		if got, err := parseKanbanStatus(input); err == nil {
			t.Fatalf("parseKanbanStatus(%q)=%q, want error", input, got)
		}
	}
}

func TestCreateTicketUnknownStatusDefaultsToBacklogInsteadOfDropping(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	result, changed, err := app.createTicket(map[string]any{
		"title":  "Card with unknown status",
		"status": "Someday",
		"draft":  true,
	})
	if err != nil {
		t.Fatalf("createTicket with unknown status: %v", err)
	}
	if !changed {
		t.Fatal("createTicket changed=false, want true")
	}
	card, ok := result["card"].(kanbanCard)
	if !ok {
		t.Fatalf("createTicket result card=%T, want kanbanCard", result["card"])
	}
	if card.Status != kanbanStatusBacklog {
		t.Fatalf("status=%q, want Backlog", card.Status)
	}
	if !card.Draft {
		t.Fatal("draft=false, want true (Scout draft flag must survive the status fallback)")
	}
}

func TestCreateTicketAliasStatusLandsOnCanonicalColumn(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	result, _, err := app.createTicket(map[string]any{
		"title":  "Card created as To Do",
		"status": "To Do",
	})
	if err != nil {
		t.Fatalf("createTicket with alias status: %v", err)
	}
	card, ok := result["card"].(kanbanCard)
	if !ok {
		t.Fatalf("createTicket result card=%T, want kanbanCard", result["card"])
	}
	if card.Status != kanbanStatusBacklog {
		t.Fatalf("status=%q, want Backlog", card.Status)
	}
	if card.Draft {
		t.Fatal("draft=true for a human create without the draft flag, want false")
	}
}

func TestCardMutationsResolveTargetByTitleWhenCardIDMissing(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	card := app.snapshotState().Cards[0]

	if _, changed, err := app.moveTicket(map[string]any{
		"title":  strings.ToUpper(card.Title),
		"status": "Done",
	}); err != nil {
		t.Fatalf("moveTicket by title: %v", err)
	} else if !changed {
		t.Fatal("moveTicket by title changed=false, want true")
	}
	if moved, ok := findSnapshotCard(app.snapshotState().Cards, card.ID); !ok || moved.Status != kanbanStatusDone {
		t.Fatalf("card %q status=%q, want Done", card.ID, moved.Status)
	}

	if _, changed, err := app.addTags(map[string]any{
		"title": card.Title,
		"tags":  []any{"resolved-by-title"},
	}); err != nil {
		t.Fatalf("addTags by title: %v", err)
	} else if !changed {
		t.Fatal("addTags by title changed=false, want true")
	}

	if _, changed, err := app.updateTicket(map[string]any{
		"card_title": card.Title,
		"notes":      "Updated through title resolution.",
	}); err != nil {
		t.Fatalf("updateTicket by card_title: %v", err)
	} else if !changed {
		t.Fatal("updateTicket by card_title changed=false, want true")
	}
	if updated, ok := findSnapshotCard(app.snapshotState().Cards, card.ID); !ok || !strings.Contains(updated.Notes, "title resolution") {
		t.Fatalf("card %q notes=%q, want title-resolution update", card.ID, updated.Notes)
	}

	// No card_id and no title keeps the original error.
	if _, _, err := app.moveTicket(map[string]any{"status": "Done"}); err == nil {
		t.Fatal("moveTicket without card_id or title succeeded, want error")
	}

	// An ambiguous title (two cards sharing it) keeps the original error.
	if _, _, err := app.createTicket(map[string]any{"title": card.Title}); err != nil {
		t.Fatalf("createTicket duplicate title: %v", err)
	}
	if _, _, err := app.moveTicket(map[string]any{
		"title":  card.Title,
		"status": "Blocked",
	}); err == nil {
		t.Fatal("moveTicket with ambiguous title succeeded, want error")
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

func TestAddKeyDateToolUpdatesCardWithoutChangingNotes(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	card := app.snapshotState().Cards[0]

	if _, changed, err := app.applyToolCallArgs("add_key_date", map[string]any{
		"card_id": card.ID,
		"label":   "PDF to investors",
		"date":    "May 24",
	}); err != nil {
		t.Fatalf("add_key_date: %v", err)
	} else if !changed {
		t.Fatal("add_key_date changed=false, want true")
	}

	updated, ok := findSnapshotCard(app.snapshotState().Cards, card.ID)
	if !ok {
		t.Fatalf("updated card %q not found", card.ID)
	}
	if updated.Notes != card.Notes {
		t.Fatalf("notes=%q, want unchanged %q", updated.Notes, card.Notes)
	}
	if got, want := updated.KeyDates, []kanbanKeyDate{{Label: "PDF to investors", Date: "May 24"}}; !kanbanKeyDatesEqual(got, want) {
		t.Fatalf("keyDates=%v, want %v", got, want)
	}
}

func TestUpdateTicketCanAddDueDateAndKeyDates(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	card := app.snapshotState().Cards[0]

	if _, changed, err := app.updateTicket(map[string]any{
		"card_id":  card.ID,
		"due_date": "May 24",
		"key_dates": []any{
			map[string]any{"label": "PDF to investors", "date": "May 24"},
		},
	}); err != nil {
		t.Fatalf("updateTicket key dates: %v", err)
	} else if !changed {
		t.Fatal("updateTicket changed=false, want true")
	}

	updated, ok := findSnapshotCard(app.snapshotState().Cards, card.ID)
	if !ok {
		t.Fatalf("updated card %q not found", card.ID)
	}
	if updated.DueDate != "May 24" {
		t.Fatalf("dueDate=%q, want May 24", updated.DueDate)
	}
	if got, want := updated.KeyDates, []kanbanKeyDate{
		{Label: "PDF to investors", Date: "May 24"},
		{Label: "due", Date: "May 24"},
	}; !kanbanKeyDatesEqual(got, want) {
		t.Fatalf("keyDates=%v, want %v", got, want)
	}
}

func TestRemoveKeyDatesToolClearsCardDates(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	card := app.snapshotState().Cards[0]

	if _, changed, err := app.updateTicket(map[string]any{
		"card_id":  card.ID,
		"due_date": "May 24",
		"key_dates": []any{
			map[string]any{"label": "PDF to investors", "date": "May 24"},
		},
	}); err != nil {
		t.Fatalf("updateTicket key dates: %v", err)
	} else if !changed {
		t.Fatal("updateTicket changed=false, want true")
	}

	if _, changed, err := app.applyToolCallArgs("remove_key_dates", map[string]any{
		"card_id":    card.ID,
		"remove_all": true,
	}); err != nil {
		t.Fatalf("remove_key_dates: %v", err)
	} else if !changed {
		t.Fatal("remove_key_dates changed=false, want true")
	}

	updated, ok := findSnapshotCard(app.snapshotState().Cards, card.ID)
	if !ok {
		t.Fatalf("updated card %q not found", card.ID)
	}
	if updated.DueDate != "" {
		t.Fatalf("dueDate=%q, want cleared", updated.DueDate)
	}
	if len(updated.KeyDates) != 0 {
		t.Fatalf("keyDates=%v, want cleared", updated.KeyDates)
	}
}

func TestUpdateTicketCanReplaceKeyDatesWithEmptySet(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	card := app.snapshotState().Cards[0]

	if _, changed, err := app.addKeyDate(map[string]any{
		"card_id": card.ID,
		"label":   "review",
		"date":    "May 24",
	}); err != nil {
		t.Fatalf("addKeyDate: %v", err)
	} else if !changed {
		t.Fatal("addKeyDate changed=false, want true")
	}

	if _, changed, err := app.updateTicket(map[string]any{
		"card_id":           card.ID,
		"key_dates":         []any{},
		"replace_key_dates": true,
	}); err != nil {
		t.Fatalf("updateTicket replace key dates: %v", err)
	} else if !changed {
		t.Fatal("updateTicket changed=false, want true")
	}

	updated, ok := findSnapshotCard(app.snapshotState().Cards, card.ID)
	if !ok {
		t.Fatalf("updated card %q not found", card.ID)
	}
	if len(updated.KeyDates) != 0 {
		t.Fatalf("keyDates=%v, want cleared", updated.KeyDates)
	}
}

func TestManualUpdatePreservesDatesWhenDateFieldsAreOmitted(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	card := app.snapshotState().Cards[0]

	if _, changed, err := app.addKeyDate(map[string]any{
		"card_id": card.ID,
		"label":   "PDF to investors",
		"date":    "May 24",
	}); err != nil {
		t.Fatalf("addKeyDate: %v", err)
	} else if !changed {
		t.Fatal("addKeyDate changed=false, want true")
	}

	withDate, ok := findSnapshotCard(app.snapshotState().Cards, card.ID)
	if !ok {
		t.Fatalf("card %q not found after addKeyDate", card.ID)
	}
	if _, changed, err := app.updateTicketDetails(map[string]any{
		"card_id": withDate.ID,
		"title":   withDate.Title,
		"notes":   withDate.Notes,
		"owner":   withDate.Owner,
		"status":  "In Progress",
		"tags":    stringsToAny(withDate.Tags),
	}); err != nil {
		t.Fatalf("updateTicketDetails: %v", err)
	} else if !changed {
		t.Fatal("updateTicketDetails changed=false, want true")
	}

	updated, ok := findSnapshotCard(app.snapshotState().Cards, card.ID)
	if !ok {
		t.Fatalf("updated card %q not found", card.ID)
	}
	if got, want := updated.KeyDates, withDate.KeyDates; !kanbanKeyDatesEqual(got, want) {
		t.Fatalf("keyDates=%v, want preserved %v", got, want)
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
