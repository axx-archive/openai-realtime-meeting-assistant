package main

import (
	"os"
	"strings"
	"testing"
)

// Board doctrine v2 frontend — business-tagged cards leave the four eng
// lanes for a collapsed Business track rail under the board, and the
// left-rail preview mirrors the clean lanes. Grep-pinned against index.html
// like the rest of the frontend contracts.

func readIndexForBusinessTrack(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(data)
}

func TestBusinessTrackMarkupIsCollapsedDetailsRail(t *testing.T) {
	html := readIndexForBusinessTrack(t)

	if !strings.Contains(html, `<details id="businessTrack" class="business-track" hidden>`) {
		t.Fatal("business track must be a hidden <details> rail (collapsed by default, no open attribute)")
	}
	if !strings.Contains(html, `<summary id="businessTrackSummary">`) {
		t.Error("business track summary element missing")
	}
	if !strings.Contains(html, `<div id="businessTrackStack" class="business-track__stack">`) {
		t.Error("business track card stack missing")
	}
}

func TestRenderBoardSplitsBusinessCardsOutOfLanes(t *testing.T) {
	html := readIndexForBusinessTrack(t)

	board := functionBodyAfterSignature(html, "function renderBoard(changes = { moved: new Set(), completed: new Set(), fresh: new Set(), toasts: [], commentPreviews: [] })")
	if board == "" {
		t.Fatal("renderBoard not found")
	}
	for _, want := range []string{
		"cards.filter(card => !isBusinessCard(card))",
		"cards.filter(card => isBusinessCard(card))",
		"laneCards.filter(card => card.status === status)",
		"renderBusinessTrack(businessCards, changes)",
	} {
		if !strings.Contains(board, want) {
			t.Errorf("renderBoard missing %q — business cards would leak into the eng lanes", want)
		}
	}

	helper := functionBody(html, "function isBusinessCard(")
	if helper == "" {
		t.Fatal("isBusinessCard helper not found")
	}
	if !strings.Contains(helper, "'business'") || !strings.Contains(helper, "toLowerCase()") {
		t.Error("isBusinessCard must match the business tag case-insensitively")
	}
}

func TestRenderBusinessTrackKeepsDraftsAndOpenState(t *testing.T) {
	html := readIndexForBusinessTrack(t)

	track := functionBodyAfterSignature(html, "function renderBusinessTrack(businessCards, changes = { moved: new Set() })")
	if track == "" {
		t.Fatal("renderBusinessTrack not found")
	}
	// Drafts must render through renderCard so accept/dismiss survives in
	// the rail — that review is the debate.
	if !strings.Contains(track, "renderCard(") {
		t.Error("renderBusinessTrack must render cards through renderCard so drafts keep accept/dismiss")
	}
	// renderBoard rebuilds the DOM on every board event; the open state must
	// come from the module-level flag or the rail snaps shut mid-read.
	for _, want := range []string{"businessTrackOpen", "track.hidden = true", "Business track · ${businessCards.length}"} {
		if !strings.Contains(track, want) {
			t.Errorf("renderBusinessTrack missing %q", want)
		}
	}
	if !strings.Contains(html, "let businessTrackOpen = false") {
		t.Error("businessTrackOpen must be a module-level flag so re-renders preserve the open state")
	}
}

func TestBoardPreviewExcludesBusinessCards(t *testing.T) {
	html := readIndexForBusinessTrack(t)

	preview := functionBodyAfterSignature(html, "function renderBoardPreview(changes = { moved: new Set() })")
	if preview == "" {
		t.Fatal("renderBoardPreview not found")
	}
	if !strings.Contains(preview, "cards.filter(card => !isBusinessCard(card))") {
		t.Error("renderBoardPreview must exclude business cards so the rail mirrors the clean lanes")
	}
	for _, want := range []string{
		"railCards.some(card => card.status === status)",
		"railCards.filter(card => card.status === status)",
		"railCards.filter(card => card.status === 'Backlog')",
	} {
		if !strings.Contains(preview, want) {
			t.Errorf("renderBoardPreview rail sections must filter railCards, missing %q", want)
		}
	}
}

func TestBusinessTrackCSSExists(t *testing.T) {
	html := readIndexForBusinessTrack(t)

	for _, want := range []string{
		".business-track {",
		".business-track__stack {",
		".business-track__stack .card {",
		"flex: 0 0 240px",
		"overflow-x: auto",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("business track CSS missing %q", want)
		}
	}
}
