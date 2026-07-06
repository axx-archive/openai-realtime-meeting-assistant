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

// P2-3 — the stacked mobile navigator integrates with browser history so the
// OS edge-swipe / hardware back unwinds the SPA (convo -> thread list, drilled
// tool -> office) instead of exiting the app, and the 30px back chevron gets a
// 44px hit target.
func TestIndexRouterHistoryAndBackTarget(t *testing.T) {
	html := readIndexForRouter(t)

	// a popstate listener drives the back gesture
	if !strings.Contains(html, "window.addEventListener('popstate'") {
		t.Error("a popstate listener must map the OS back gesture into the stacked navigator")
	}

	// entering the convo pushes a history entry so back returns to the threads
	view := functionBody(html, "function setMobileChatView(view)")
	if view == "" {
		t.Fatal("could not extract setMobileChatView body")
	}
	if !strings.Contains(view, "history.pushState({ view: 'convo'") {
		t.Error("setMobileChatView must history.pushState a convo entry on the threads->convo transition")
	}

	// drilling into a tool from the office pushes an entry too
	tool := functionBody(html, "function setActiveTool(tool)")
	if !strings.Contains(tool, "history.pushState({ view: 'tool'") {
		t.Error("setActiveTool must history.pushState a tool entry on the office->tool drill-in")
	}

	// the popstate handler unwinds convo -> threads and tool -> office
	popStart := strings.Index(html, "window.addEventListener('popstate'")
	popBody := html[popStart : popStart+700]
	if !strings.Contains(popBody, "setMobileChatView('threads')") || !strings.Contains(popBody, "setActiveTool('office')") {
		t.Error("the popstate handler must return a convo to its threads and a drilled tool to the office")
	}

	// the 30px chevron carries the 44px ::before hit extension (P0-3 pattern)
	if !strings.Contains(html, ".chat-convo-head__back::before {") {
		t.Error("the back chevron must carry a ::before hit extension")
	}
	backBefore := html[strings.Index(html, ".chat-convo-head__back::before {"):]
	if !strings.Contains(backBefore[:200], "width: var(--hit-min); height: var(--hit-min);") {
		t.Error(".chat-convo-head__back::before must extend the hit target to --hit-min (44px)")
	}
	// the button must be a positioning context for the absolute ::before
	backRule := cssBlock(html, ".chat-convo-head__back {")
	if !strings.Contains(backRule, "position: relative") {
		t.Error(".chat-convo-head__back must be position: relative so the ::before hit box centers on it")
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
