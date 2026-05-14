package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKanbanBoardPersistsAcrossAppInstances(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))

	app := newKanbanBoardApp()
	if _, changed, err := app.createTicket(map[string]any{
		"title": "Persisted ticket",
		"notes": "This should survive a restart.",
		"owner": "AJ",
		"tags":  []any{"persistence"},
	}); err != nil {
		t.Fatalf("createTicket: %v", err)
	} else if !changed {
		t.Fatal("createTicket changed=false, want true")
	}

	reloaded := newKanbanBoardApp()
	cards := reloaded.snapshotState().Cards
	for _, card := range cards {
		if card.Title == "Persisted ticket" {
			if card.Owner != "AJ" {
				t.Fatalf("persisted owner=%q, want AJ", card.Owner)
			}
			return
		}
	}

	t.Fatalf("reloaded cards=%v, want persisted ticket", cards)
}

func TestKanbanBoardCanPersistEmptyState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	boardPath := filepath.Join(dir, "kanban-board.json")
	if err := writeKanbanBoardState(boardPath, kanbanBoardState{Cards: []kanbanCard{}}); err != nil {
		t.Fatalf("write empty board: %v", err)
	}
	if _, err := os.Stat(boardPath); err != nil {
		t.Fatalf("stat empty board: %v", err)
	}

	app := newKanbanBoardApp()
	if cards := app.snapshotState().Cards; len(cards) != 0 {
		t.Fatalf("cards=%v, want empty persisted board", cards)
	}
}
