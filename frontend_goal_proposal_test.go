package main

// Card 088 frontend contract: the free-form goal proposal card (kind goal_run)
// and the 069 governance-lane caption. A goal_run renders like the
// conversational branch. Governed approval renders the persisted objective
// read-only so accepting cannot expand its effect class.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForGoalProposal(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func TestIndexGoalRunProposalCard(t *testing.T) {
	html := readIndexForGoalProposal(t)

	for _, want := range []string{
		// the goal_run branch is recognized as its own kind
		"const isGoal = String(proposal.kind || '') === 'goal_run'",
		// it renders with a goal label, not a bogus "tool run"
		"'Multi-step goal'",
		// a goal has no registry tool: skip the tool lookup + the fields fetch
		"(!isWorkstream && !isImage && !isGoal) ? paletteToolById(proposal.toolId) : null",
		// the governance-lane caption + its label mapping
		"scout-proposal-card__lane",
		"function scoutProposalLaneLabel(lane)",
		"const isExactApproval = String(proposal.intentOutcome || message.intentOutcome || '') === 'approval_required' || Boolean(String(proposal.effectClass || '').trim())",
		"objectiveInput.readOnly = isExactApproval",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing goal_run/lane hook %q", want)
		}
	}

	// Scope the card body and prove all branches accept through the proposal
	// route and none starts a second client-owned goal pipeline.
	cardStart := strings.Index(html, "function buildScoutProposalCardNode(message)")
	cardEnd := strings.Index(html, "function markProposalCardResolved")
	if cardStart < 0 || cardEnd < 0 || cardEnd <= cardStart {
		t.Fatal("cannot scope the proposal card function body")
	}
	cardBody := html[cardStart:cardEnd]
	if !strings.Contains(cardBody, "if (isWorkstream || isImage) {") {
		t.Fatal("the specialized workstream/image proposal route is missing")
	}
	if !strings.Contains(cardBody, "isExactApproval ? proposal.objective") {
		t.Fatal("governed approval must post the exact persisted objective")
	}
	if strings.Contains(cardBody, "paletteBuildPackageField()") {
		t.Fatal("goal proposal exposes a client-owned package/tool picker")
	}
	if strings.Contains(cardBody, "runGoalPipeline({") || strings.Contains(cardBody, "toolTemplate: String(proposal.toolId") {
		t.Fatal("goal proposal forks into a second client-selected launch")
	}
}
