package main

import (
	"strings"
	"testing"
)

// Board doctrine v2 — the worker's category enum is build|fix|workflow|
// business, business cards are captured (owner + concrete next step, always
// a draft) rather than silently cut, and the doctrine rewrite can never drop
// the prompt phrases other tests pin (codex_proposals_test.go, linkage_test.go).

func TestBoardDoctrineInstructionsCategoryEnumAndPinnedPhrases(t *testing.T) {
	instructions := meetingBoardInstructions()

	for _, want := range []string{
		"build, fix, workflow, or business",
		"concrete next step",
		"name their deliverable",
	} {
		if !strings.Contains(instructions, want) {
			t.Errorf("board instructions missing doctrine phrase %q", want)
		}
	}
	if strings.Contains(instructions, "product, process, or business") {
		t.Error("board instructions still carry the old product|process|business enum")
	}

	// The doctrine edit must leave the prompt-pinned invariants intact.
	for _, pinned := range []string{
		"never auto-run",
		"read-only",
		"pass its card_id if known, otherwise reuse the card's exact title",
	} {
		if !strings.Contains(instructions, pinned) {
			t.Errorf("board instructions dropped pinned phrase %q", pinned)
		}
	}
}

func TestBusinessCreateRequiresOwnerAndNextStep(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	seededCards := len(app.snapshotState().Cards)

	result := app.applyMeetingBoardAnalysis(meetingBoardAnalysis{
		Summary: "doctrine gate",
		Operations: []meetingBoardOperation{
			{
				Tool:   "create_ticket",
				Reason: "business item without an owner",
				Arguments: map[string]any{
					"title": "Renew the studio sublease",
					"owner": "Unassigned",
					"notes": "Call the landlord and countersign the sublease by Friday.",
					"tags":  []any{"business", "facilities"},
				},
			},
			{
				Tool:   "create_ticket",
				Reason: "business item without a next step",
				Arguments: map[string]any{
					"title": "Renew the studio sublease",
					"owner": "AJ",
					"tags":  []any{"business"},
				},
			},
			{
				Tool:   "create_ticket",
				Reason: "business item with owner and next step",
				Arguments: map[string]any{
					"title": "Renew the studio sublease",
					"owner": "AJ",
					"notes": "Call the landlord and countersign the sublease by Friday.",
					"tags":  []any{"business", "facilities"},
				},
			},
		},
	})

	if len(result.Applications) != 3 {
		t.Fatalf("applications=%d, want 3", len(result.Applications))
	}
	if result.Applications[0].Error == "" || !strings.Contains(result.Applications[0].Error, "named owner") {
		t.Fatalf("owner-less business create error=%q, want named-owner rejection", result.Applications[0].Error)
	}
	if result.Applications[0].Changed {
		t.Fatal("owner-less business create must not change the board")
	}
	if result.Applications[1].Error == "" || !strings.Contains(result.Applications[1].Error, "next step") {
		t.Fatalf("next-step-less business create error=%q, want next-step rejection", result.Applications[1].Error)
	}
	if result.Applications[1].Changed {
		t.Fatal("next-step-less business create must not change the board")
	}
	if result.Applications[2].Error != "" {
		t.Fatalf("compliant business create errored: %s", result.Applications[2].Error)
	}
	if !result.Applications[2].Changed {
		t.Fatal("compliant business create must land")
	}
	if result.ErrorCount != 2 || result.ChangedCount != 1 {
		t.Fatalf("errors=%d changed=%d, want 2/1", result.ErrorCount, result.ChangedCount)
	}

	board := app.snapshotState()
	if len(board.Cards) != seededCards+1 {
		t.Fatalf("cards=%d, want the seeded board plus exactly the compliant create", len(board.Cards))
	}
	card, found := boardDoctrineCardByTitle(board, "Renew the studio sublease")
	if !found {
		t.Fatal("compliant business card missing from the board")
	}
	if !card.Draft {
		t.Fatal("business card must land as a draft — accept/dismiss is the debate")
	}
	hasBusinessTag := false
	for _, tag := range card.Tags {
		if strings.EqualFold(tag, "business") {
			hasBusinessTag = true
		}
	}
	if !hasBusinessTag {
		t.Fatalf("tags=%v, business tag must survive the create", card.Tags)
	}
}

func boardDoctrineCardByTitle(board kanbanBoardState, title string) (kanbanCard, bool) {
	for _, card := range board.Cards {
		if card.Title == title {
			return card, true
		}
	}
	return kanbanCard{}, false
}

func TestNonBusinessCreateUnaffectedByDoctrine(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	result := app.applyMeetingBoardAnalysis(meetingBoardAnalysis{
		Operations: []meetingBoardOperation{
			{
				Tool:   "create_ticket",
				Reason: "eng work needs no owner yet",
				Arguments: map[string]any{
					"title": "Fix the tile flicker on rejoin",
					"owner": "Unassigned",
					"tags":  []any{"fix", "webrtc"},
				},
			},
		},
	})

	if result.ErrorCount != 0 || result.ChangedCount != 1 {
		t.Fatalf("errors=%d changed=%d, want 0/1 — the doctrine gate is business-only", result.ErrorCount, result.ChangedCount)
	}
	card, found := boardDoctrineCardByTitle(app.snapshotState(), "Fix the tile flicker on rejoin")
	if !found {
		t.Fatal("owner-less non-business create must still land")
	}
	if !card.Draft {
		t.Fatal("worker-created card must land as a draft (D4)")
	}
}
