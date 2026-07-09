package main

import (
	"context"
	"strings"
	"testing"
)

// stubSuggestionResponder returns a fixed JSON body and records the input it
// was handed so a test can assert the dedupe context reached the model.
func stubSuggestionResponder(t *testing.T, body string, capture *string) openAITextResponder {
	t.Helper()
	return func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if capture != nil {
			*capture = request.Input
		}
		return body, nil
	}
}

func researchProposals(t *testing.T, app *kanbanBoardApp) []meetingMemoryEntry {
	t.Helper()
	var out []meetingMemoryEntry
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindCodexProposal, 0) {
		if strings.EqualFold(entry.Metadata["mode"], "research") {
			out = append(out, entry)
		}
	}
	return out
}

func TestResearchSuggestionWorkerProposesFromDiscussion(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	launches := 0
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { launches++ }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Discussion\n- The team kept circling the Samsung TV+ opportunity and said someone should really dig into whether it is worth pursuing. No one committed.", nil); err != nil || !appended {
		t.Fatalf("append brain write-up: appended=%v err=%v", appended, err)
	}

	responder := stubSuggestionResponder(t, `{
		"suggestions": [
			{
				"title": "Samsung TV+ opportunity brief",
				"query": "Research whether the Samsung TV+ opportunity is worth pursuing and draft a brief with sources.",
				"reason": "The room circled it and asked for someone to dig in without committing."
			}
		]
	}`, nil)

	if _, err := app.runResearchSuggestionOnce(context.Background(), "test-key", responder); err != nil {
		t.Fatalf("runResearchSuggestionOnce: %v", err)
	}

	proposals := researchProposals(t, app)
	if len(proposals) != 1 {
		t.Fatalf("research proposals=%d, want 1", len(proposals))
	}
	proposal := proposals[0]
	if proposal.Metadata["status"] != codexProposalStatusProposed {
		t.Fatalf("status=%q, want proposed", proposal.Metadata["status"])
	}
	if proposal.Metadata["proposedBy"] != "suggestion_worker" {
		t.Fatalf("proposedBy=%q, want suggestion_worker", proposal.Metadata["proposedBy"])
	}
	if !strings.Contains(proposal.Metadata["title"], "Samsung TV+") {
		t.Fatalf("title=%q, want Samsung TV+ topic", proposal.Metadata["title"])
	}
	// Confirm-first trust model: proposing must never launch a thread.
	if launches != 0 {
		t.Fatalf("launches=%d, a suggestion must never auto-launch", launches)
	}

	// The pass advanced the in-memory cursor past the window it consumed, so a
	// second pass over the same brain proposes nothing new.
	if got := app.ambientAgentBaselineID(researchSuggestionAgentName); got != "brain-1" {
		t.Fatalf("baseline=%q, want brain-1 (cursor advanced)", got)
	}
	if _, err := app.runResearchSuggestionOnce(context.Background(), "test-key", responder); err != nil {
		t.Fatalf("second runResearchSuggestionOnce: %v", err)
	}
	if proposals := researchProposals(t, app); len(proposals) != 1 {
		t.Fatalf("research proposals after re-run=%d, want still 1 (window already consumed)", len(proposals))
	}
}

func TestResearchSuggestionWorkerDedupesKnownTopic(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	// A research proposal already exists for the topic (e.g. the board worker or
	// a prior suggestion pass proposed it).
	if _, _, err := app.proposeCodexTask(map[string]any{
		"title": "Samsung TV+ opportunity brief",
		"mode":  "research",
		"query": "Research whether the Samsung TV+ opportunity is worth pursuing.",
	}, "board_worker"); err != nil {
		t.Fatalf("seed existing proposal: %v", err)
	}
	if len(researchProposals(t, app)) != 1 {
		t.Fatalf("seed proposal count != 1")
	}

	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Discussion\n- More Samsung TV+ chatter.", nil); err != nil || !appended {
		t.Fatalf("append brain write-up: appended=%v err=%v", appended, err)
	}

	var seenInput string
	responder := stubSuggestionResponder(t, `{
		"suggestions": [
			{
				"title": "Samsung TV Plus opportunity",
				"query": "Research whether the Samsung TV Plus opportunity is worth pursuing and draft a brief.",
				"reason": "Room kept discussing Samsung TV+."
			}
		]
	}`, &seenInput)

	if _, err := app.runResearchSuggestionOnce(context.Background(), "test-key", responder); err != nil {
		t.Fatalf("runResearchSuggestionOnce: %v", err)
	}

	// The overlapping suggestion was dropped by the programmatic dedupe backstop,
	// so no second proposal was created.
	if proposals := researchProposals(t, app); len(proposals) != 1 {
		t.Fatalf("research proposals=%d, want 1 (duplicate suppressed)", len(proposals))
	}
	// The already-known topic was also surfaced to the model as dedupe context.
	if !strings.Contains(seenInput, "Already proposed or running research") || !strings.Contains(seenInput, "Samsung TV+ opportunity brief") {
		t.Fatalf("model input missing the known-research dedupe section: %s", seenInput)
	}
}

func TestResearchSuggestionWorkerCapsPerPass(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Discussion\n- Three distinct research threads came up.", nil); err != nil || !appended {
		t.Fatalf("append brain write-up: appended=%v err=%v", appended, err)
	}

	responder := stubSuggestionResponder(t, `{
		"suggestions": [
			{"title": "Rodeo creator landscape", "query": "Research the rodeo creator landscape with sources."},
			{"title": "Ball Dogs licensing precedent", "query": "Research comparable IP licensing deals for Ball Dogs."},
			{"title": "TV+ ad economics", "query": "Research free-ad-supported TV economics for the opportunity."}
		]
	}`, nil)

	if _, err := app.runResearchSuggestionOnce(context.Background(), "test-key", responder); err != nil {
		t.Fatalf("runResearchSuggestionOnce: %v", err)
	}

	if proposals := researchProposals(t, app); len(proposals) != researchSuggestionMaxPerPass {
		t.Fatalf("research proposals=%d, want the per-pass cap %d", len(proposals), researchSuggestionMaxPerPass)
	}
}

func TestResearchSuggestionWorkerAdvancesCursorOnSilence(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Discussion\n- Pure logistics, nothing researchable.", nil); err != nil || !appended {
		t.Fatalf("append brain write-up: appended=%v err=%v", appended, err)
	}

	responder := stubSuggestionResponder(t, `{"suggestions": []}`, nil)
	if _, err := app.runResearchSuggestionOnce(context.Background(), "test-key", responder); err != nil {
		t.Fatalf("runResearchSuggestionOnce: %v", err)
	}

	if proposals := researchProposals(t, app); len(proposals) != 0 {
		t.Fatalf("research proposals=%d, want 0 on a silent pass", len(proposals))
	}
	// A silent pass still advances the cursor so the window is never reconsidered.
	if got := app.ambientAgentBaselineID(researchSuggestionAgentName); got != "brain-1" {
		t.Fatalf("baseline=%q, want brain-1 (cursor advanced even with no suggestion)", got)
	}
}

func TestResearchSuggestionInstructionsCarryLooserBar(t *testing.T) {
	instructions := researchSuggestionInstructions()
	for _, want := range []string{"DISCUSSED", "at most two", "human confirms", "read-only"} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("research suggestion instructions missing %q", want)
		}
	}
}
