package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateTicketDraftFlagStampsDraftState(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	result, changed, err := app.createTicket(map[string]any{
		"title": "Add bandwidth estimation probe",
		"notes": "Measure per-subscriber available bandwidth.",
		"draft": true,
	})
	if err != nil {
		t.Fatalf("createTicket draft: %v", err)
	}
	if !changed {
		t.Fatal("createTicket draft changed=false, want true")
	}
	card, ok := result["card"].(kanbanCard)
	if !ok {
		t.Fatalf("createTicket result card type %T, want kanbanCard", result["card"])
	}
	if !card.Draft {
		t.Fatal("card.Draft=false, want true")
	}
	if strings.TrimSpace(card.DraftedAt) == "" {
		t.Fatal("card.DraftedAt empty, want a timestamp")
	}

	snapshot, ok := findSnapshotCard(app.snapshotState().Cards, card.ID)
	if !ok {
		t.Fatalf("draft card %q not in snapshot", card.ID)
	}
	if !snapshot.Draft || snapshot.DraftedAt == "" {
		t.Fatalf("snapshot draft state lost: draft=%v draftedAt=%q", snapshot.Draft, snapshot.DraftedAt)
	}
}

func TestCreateTicketWithoutDraftFlagStaysInstant(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	result, _, err := app.createTicket(map[string]any{"title": "Human card"})
	if err != nil {
		t.Fatalf("createTicket: %v", err)
	}
	card := result["card"].(kanbanCard)
	if card.Draft || card.DraftedAt != "" {
		t.Fatalf("human card must not be a draft: draft=%v draftedAt=%q", card.Draft, card.DraftedAt)
	}
}

func TestAcceptDraftTicketConvertsToNormalCard(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	created, _, err := app.createTicket(map[string]any{"title": "Draft card", "draft": true})
	if err != nil {
		t.Fatalf("createTicket draft: %v", err)
	}
	cardID := created["card"].(kanbanCard).ID

	result, changed, err := app.acceptDraftTicket(map[string]any{"card_id": cardID})
	if err != nil {
		t.Fatalf("acceptDraftTicket: %v", err)
	}
	if !changed {
		t.Fatal("acceptDraftTicket changed=false, want true")
	}
	card := result["card"].(kanbanCard)
	if card.Draft || card.DraftedAt != "" {
		t.Fatalf("accepted card still a draft: draft=%v draftedAt=%q", card.Draft, card.DraftedAt)
	}
	if card.Owner != "Unassigned" {
		t.Fatalf("accepted card owner=%q, want Unassigned until claimed", card.Owner)
	}

	// accepting twice fails — it is no longer a draft
	if _, _, err := app.acceptDraftTicket(map[string]any{"card_id": cardID}); err == nil {
		t.Fatal("accepting a non-draft card must error")
	}
}

func TestDismissDraftTicketRemovesCardAndSkipsUndo(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	created, _, err := app.createTicket(map[string]any{"title": "Draft card", "draft": true})
	if err != nil {
		t.Fatalf("createTicket draft: %v", err)
	}
	cardID := created["card"].(kanbanCard).ID

	result, changed, err := app.dismissDraftTicket(map[string]any{"card_id": cardID})
	if err != nil {
		t.Fatalf("dismissDraftTicket: %v", err)
	}
	if !changed {
		t.Fatal("dismissDraftTicket changed=false, want true")
	}
	if _, ok := result["card"].(kanbanCard); !ok {
		t.Fatalf("dismiss result card type %T, want kanbanCard", result["card"])
	}
	if _, ok := findSnapshotCard(app.snapshotState().Cards, cardID); ok {
		t.Fatal("dismissed draft still on the board")
	}
	if app.canUndoDelete() {
		t.Fatal("dismissed drafts must not enter the undo slot")
	}

	// dismissing a normal card fails
	normal, _, err := app.createTicket(map[string]any{"title": "Real card"})
	if err != nil {
		t.Fatalf("createTicket: %v", err)
	}
	if _, _, err := app.dismissDraftTicket(map[string]any{"card_id": normal["card"].(kanbanCard).ID}); err == nil {
		t.Fatal("dismissing a non-draft card must error")
	}
}

func TestResolveBoardDraftDismissWritesMemoryNote(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	created, _, err := app.createTicket(map[string]any{
		"title": "Add bandwidth estimation probe",
		"notes": "Measure per-subscriber bandwidth.",
		"draft": true,
	})
	if err != nil {
		t.Fatalf("createTicket draft: %v", err)
	}
	cardID := created["card"].(kanbanCard).ID

	if _, changed, err := app.resolveBoardDraft(cardID, "dismiss", "AJ", "already covered by simulcast work"); err != nil {
		t.Fatalf("resolveBoardDraft dismiss: %v", err)
	} else if !changed {
		t.Fatal("resolveBoardDraft dismiss changed=false, want true")
	}

	entries := app.memorySnapshot(20)
	found := false
	for _, entry := range entries {
		if entry.Kind == meetingMemoryKindBoardUpdate && entry.Metadata["source"] == "board_draft_dismiss" {
			found = true
			if entry.Metadata["cardId"] != cardID {
				t.Fatalf("memory note cardId=%q, want %q", entry.Metadata["cardId"], cardID)
			}
			if !strings.Contains(entry.Text, "Add bandwidth estimation probe") {
				t.Fatalf("memory note missing card title: %q", entry.Text)
			}
			if !strings.Contains(entry.Text, "already covered by simulcast work") {
				t.Fatalf("memory note missing dismiss reason: %q", entry.Text)
			}
		}
	}
	if !found {
		t.Fatal("dismissing a draft must write a board_update memory note")
	}
}

func TestBoardWorkerCreateTicketProducesDraft(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	result := app.applyMeetingBoardAnalysis(meetingBoardAnalysis{
		Summary: "One clear follow-up.",
		Operations: []meetingBoardOperation{
			{
				Tool:   "create_ticket",
				Reason: "The room committed to a bandwidth probe.",
				Arguments: map[string]any{
					"title": "Add bandwidth estimation probe",
					"notes": "Measure per-subscriber available bandwidth.",
				},
			},
		},
	})
	if result.ChangedCount != 1 {
		t.Fatalf("changed=%d, want 1", result.ChangedCount)
	}
	card, ok := result.Applications[0].Result["card"].(kanbanCard)
	if !ok {
		t.Fatalf("worker create result card type %T, want kanbanCard", result.Applications[0].Result["card"])
	}
	if !card.Draft {
		t.Fatal("board-worker-created card must be a draft (D4)")
	}
	if card.DraftedAt == "" {
		t.Fatal("board-worker draft missing DraftedAt")
	}
}

func TestKanbanBoardStateBackwardCompatWithoutDraftField(t *testing.T) {
	dir := t.TempDir()
	boardPath := filepath.Join(dir, "board.json")
	legacy := `{
  "cards": [
    {"id": "card-1", "status": "Backlog", "title": "Old card", "notes": "", "tags": []}
  ],
  "updatedAt": "2026-01-01T00:00:00Z"
}`
	if err := os.WriteFile(boardPath, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy board: %v", err)
	}

	state, ok, err := loadKanbanBoardState(boardPath)
	if err != nil {
		t.Fatalf("loadKanbanBoardState: %v", err)
	}
	if !ok {
		t.Fatal("legacy board not loaded")
	}
	if len(state.Cards) != 1 {
		t.Fatalf("cards=%d, want 1", len(state.Cards))
	}
	if state.Cards[0].Draft || state.Cards[0].DraftedAt != "" {
		t.Fatal("legacy cards must load as non-drafts")
	}

	// a stray draftedAt without draft=true is scrubbed on normalize
	stray := normalizeKanbanCards([]kanbanCard{{ID: "x", Title: "t", Status: kanbanStatusBacklog, DraftedAt: "2026-01-01T00:00:00Z"}})
	if stray[0].DraftedAt != "" {
		t.Fatal("non-draft cards must not keep a draftedAt stamp")
	}
}

// Frontend wiring guard, following the repo's index.html grep-test pattern:
// the board draft styling, actions, and endpoint wiring must stay together.
func TestIndexBoardDraftWiring(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	for _, want := range []string{
		"/assistant/board/drafts/",
		"card-draft__accept",
		"card-draft__dismiss",
		".card.is-draft",
		"drafted by scout · ",
		"dismissed · scout will remember why",
		"function submitBoardDraftAction(cardId, action)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing board draft anchor %q", want)
		}
	}
}
