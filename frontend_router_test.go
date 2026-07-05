package main

// The propose-confirm router's frontend contract (packaging OS §2, Wave 2
// item 8). These grep-style pins hold the client half of NEVER-silent-launch:
// the confirmation card is the only thing that turns a proposal into a run,
// its Run button posts the identical spec the palette Run posts, the escape
// hatch re-asks as Tier 0, and the composer pills that fed the retired
// keyword-sniff lane stay gone.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForRouter(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// The confirmation card: rendered from the persisted Kind=proposal message,
// with the trust-surface hooks the spec names — tool + group, one legible
// sentence, editable pre-filled fields (palette form-card classes reused),
// target package, authority class, weight label, Run, and the escape.
func TestIndexRouterConfirmationCardContract(t *testing.T) {
	html := readIndexForRouter(t)
	for _, want := range []string{
		// render branch: a proposal message becomes the card, everywhere the
		// thread renders (live send and reload alike)
		"=== 'proposal' && message.proposal",
		"function scoutProposalCardNode(message)",
		"card.dataset.proposalKind",
		// the trust surface's parts
		"scout-proposal-card__tool",
		"scout-proposal-card__group",
		"scout-proposal-card__summary",
		"scout-proposal-card__authority",
		"scout-proposal-card__weight",
		// editable pre-filled fields reuse the palette form-card pattern
		"input.value = String((proposal.fields || {})[field.key] || '')",
		"paletteBuildPackageField()",
		// Run posts the IDENTICAL palette spec — reuse, do not fork
		"toolTemplate: String(proposal.toolId || '')",
		"authorityHint: String(proposal.authority || tool?.authority || '')",
		// the escape hatch: Tier 0 + dismissal signal
		"'just answer instead'",
		"postScoutProposalAction('dismissed', proposal, message, {})",
		// the verdict route (accept/dismiss signals + workstream launch)
		"}/proposal`",
		// a spent card renders inert
		"function markProposalCardResolved(card, status)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing router confirmation-card hook %q", want)
		}
	}

	// Run must go through runGoalPipeline (the single /assistant/goal door)
	// inside the card handler, not a bespoke fetch.
	cardStart := strings.Index(html, "function scoutProposalCardNode(message)")
	cardEnd := strings.Index(html, "function markProposalCardResolved")
	if cardStart < 0 || cardEnd < 0 || cardEnd <= cardStart {
		t.Fatal("cannot scope the proposal card function body")
	}
	cardBody := html[cardStart:cardEnd]
	if !strings.Contains(cardBody, "runGoalPipeline({") {
		t.Fatal("the card's Run button must converge on runGoalPipeline — the same door as the palette Run")
	}
}

// The 4 composer pills are cut (spec §2): the router replaced them in the same
// release, so none of their hooks may survive.
func TestIndexComposerPillsRemoved(t *testing.T) {
	html := readIndexForRouter(t)
	for _, banned := range []string{
		"scout-work-starters",
		"data-scout-starter",
		"scoutWorkStarterButtons",
	} {
		if strings.Contains(html, banned) {
			t.Fatalf("index.html still contains composer-pill hook %q — the pills were cut with the router release", banned)
		}
	}
}
