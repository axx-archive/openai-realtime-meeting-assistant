package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParticipantEmailsUseShareabilityAliases(t *testing.T) {
	names := []string{"AJ", "Erick", "Tim", "AJ"}
	emails := participantEmails(names)
	want := []string{"aj@shareability.com", "e@shareability.com", "tim@shareability.com"}
	if len(emails) != len(want) {
		t.Fatalf("emails=%v, want %v", emails, want)
	}
	for index := range want {
		if emails[index] != want[index] {
			t.Fatalf("emails=%v, want %v", emails, want)
		}
	}
}

func TestMeetingNotesIncludeDecisionsAndProjectStatuses(t *testing.T) {
	archivedAt := time.Date(2026, 5, 13, 15, 30, 0, 0, time.UTC)
	board := kanbanBoardState{
		Cards: []kanbanCard{
			{
				ID:     "card-1",
				Status: kanbanStatusInProgress,
				Title:  "Realtime Notes",
				Owner:  "AJ",
				Notes:  "Summarize the meeting.",
				Tags:   []string{"notes"},
			},
		},
	}
	memory := []meetingMemoryEntry{
		{
			ID:        "memory-1",
			Kind:      meetingMemoryKindTranscript,
			Text:      "We decided to email notes after archive.",
			CreatedAt: archivedAt,
		},
	}

	notes := buildMeetingNotes("meeting-1", archivedAt, "AJ", board, memory, []string{"AJ"})
	if len(notes.Decisions) != 1 {
		t.Fatalf("decisions=%v, want one decision", notes.Decisions)
	}
	if len(notes.ProjectStatuses) != 1 {
		t.Fatalf("project statuses=%v, want one", notes.ProjectStatuses)
	}
	if !strings.Contains(notes.Text, "We decided to email notes after archive.") {
		t.Fatalf("notes text missing decision: %s", notes.Text)
	}
	if !strings.Contains(notes.Text, "Realtime Notes: In Progress. Owner: AJ") {
		t.Fatalf("notes text missing project status: %s", notes.Text)
	}
}

func TestDeleteTicketCanBeRestored(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", t.TempDir()+"/memory.jsonl")
	app := newKanbanBoardApp()
	cardID := app.snapshotState().Cards[0].ID
	if _, changed, err := app.deleteTicket(map[string]any{"card_id": cardID}); err != nil {
		t.Fatalf("deleteTicket: %v", err)
	} else if !changed {
		t.Fatal("deleteTicket changed=false, want true")
	}
	if !app.canUndoDelete() {
		t.Fatal("canUndoDelete=false, want true")
	}
	if _, changed, err := app.restoreLastDeletedTicket(); err != nil {
		t.Fatalf("restoreLastDeletedTicket: %v", err)
	} else if !changed {
		t.Fatal("restoreLastDeletedTicket changed=false, want true")
	}
	if app.canUndoDelete() {
		t.Fatal("canUndoDelete=true after restore, want false")
	}
}

func TestMeetingRoomPasswordSeedsAccountsFromEnvironment(t *testing.T) {
	t.Setenv("MEETING_ROOM_PASSWORD", "secret-room")
	store, err := newUserAccountStore(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("newUserAccountStore: %v", err)
	}
	if _, ok := store.authenticate("aj@shareability.com", "secret-room"); !ok {
		t.Fatal("expected env password to seed account passwords")
	}
	if _, ok := store.authenticate("aj@shareability.com", defaultMeetingRoomPassword); ok {
		t.Fatal("expected default password to be rejected while env override is set")
	}
}
