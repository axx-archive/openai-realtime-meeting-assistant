package main

import (
	"os"
	"strings"
	"testing"
)

// Board delivery projection frontend — every canonical card stays visible in
// one of three lifecycle lanes, with project filtering layered over the
// underlying process status. The retired business rail remains inert markup
// for compatibility but must never split company work out of the main view.

func readIndexForBusinessTrack(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(data)
}

func TestBusinessTrackMarkupStaysInertForCompatibility(t *testing.T) {
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

func TestRenderBoardProjectsEveryCardIntoDeliveryLanes(t *testing.T) {
	html := readIndexForBusinessTrack(t)

	board := functionBodyAfterSignature(html, "function renderBoard(changes = { moved: new Set(), completed: new Set(), fresh: new Set(), toasts: [], commentPreviews: [] })")
	if board == "" {
		t.Fatal("renderBoard not found")
	}
	for _, want := range []string{
		"boardDeliveryStages().map(stage =>",
		"laneCards.filter(card => boardCardDeliveryStage(card) === stage.id)",
		"renderBusinessTrack([], changes)",
		"syncBoardProjectFilter()",
	} {
		if !strings.Contains(board, want) {
			t.Errorf("renderBoard missing delivery projection %q", want)
		}
	}
	for _, forbidden := range []string{
		"cards.filter(card => !isBusinessCard(card))",
		"cards.filter(card => isBusinessCard(card))",
	} {
		if strings.Contains(board, forbidden) {
			t.Errorf("renderBoard still splits work into the retired business rail: %q", forbidden)
		}
	}
}

func TestBoardDeliveryStagesAndProjectResolutionAreExplicit(t *testing.T) {
	html := readIndexForBusinessTrack(t)
	for _, want := range []string{
		"function boardDeliveryStages()",
		"{ id: 'requested', label: 'Work requested'",
		"{ id: 'delivered', label: 'Work delivered'",
		"{ id: 'drive', label: 'Saved to Drive'",
		"function boardCardProject(card)",
		"title: 'Needs project'",
		"new Option('All projects', 'all')",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("board delivery contract missing %q", want)
		}
	}
}

func TestBoardPreviewRemainsAvailable(t *testing.T) {
	html := readIndexForBusinessTrack(t)

	preview := functionBodyAfterSignature(html, "function renderBoardPreview(changes = { moved: new Set() })")
	if preview == "" {
		t.Fatal("renderBoardPreview not found")
	}
	if !strings.Contains(preview, "boardRailCount") {
		t.Error("renderBoardPreview must retain a compact board count")
	}
	for _, want := range []string{
		"boardDeliveryStages().map(stage =>",
		"cards.filter(card => boardCardDeliveryStage(card) === stage.id)",
	} {
		if !strings.Contains(preview, want) {
			t.Errorf("renderBoardPreview missing delivery projection %q", want)
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
