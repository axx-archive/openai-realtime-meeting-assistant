package main

// Card 088 frontend contract: the free-form goal proposal card (kind goal_run)
// and the 069 governance-lane caption. A goal_run renders like the
// conversational branch — one editable objective + a package field — and its
// Run converges on runGoalPipeline with NO toolTemplate (a plain goal), so it
// reuses the single /assistant/goal door rather than forking a bespoke fetch.

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
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing goal_run/lane hook %q", want)
		}
	}

	// Scope the card body and prove a goal_run does NOT take the workstream/image
	// proposal route (which never runs a pipeline) — it must fall through to
	// runGoalPipeline. isGoal is deliberately absent from that gate.
	cardStart := strings.Index(html, "function buildScoutProposalCardNode(message)")
	cardEnd := strings.Index(html, "function markProposalCardResolved")
	if cardStart < 0 || cardEnd < 0 || cardEnd <= cardStart {
		t.Fatal("cannot scope the proposal card function body")
	}
	cardBody := html[cardStart:cardEnd]
	if !strings.Contains(cardBody, "if (isWorkstream || isImage) {") {
		t.Fatal("the Run branch must gate only workstream/image on the proposal route so a goal_run falls through to runGoalPipeline")
	}
	if strings.Contains(cardBody, "if (isWorkstream || isImage || isGoal)") {
		t.Fatal("a goal_run must NOT take the workstream/image proposal route — it launches via runGoalPipeline")
	}
	// The package field renders for a goal (it can target a package binder), so
	// its gate must not exclude isGoal.
	if !strings.Contains(cardBody, "if (!isWorkstream && !isImage) {") {
		t.Fatal("the package field must render for tool runs and goals alike")
	}
	if !strings.Contains(cardBody, "runGoalPipeline({") {
		t.Fatal("the card's Run must converge on runGoalPipeline")
	}
}
