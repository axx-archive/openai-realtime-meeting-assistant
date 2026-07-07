package main

// Card 081 frontend pins: the Mission Intelligence decision ledger rows are
// clickable buttons that expand an inline detail and offer a jump into the
// source meeting in the Memory tool. Frontend behaviour is asserted by parsing
// index.html (the frontend_*_test.go convention), reusing functionBody from
// frontend_latency_test.go.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForIntelLinks(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// The shared intelDecisionNode builds a <button> row carrying data-decision-id,
// toggles expandedIntelDecisionId, and offers an "open meeting →" jump.
func TestIndexIntelDecisionNodeIsClickable(t *testing.T) {
	html := readIndexForIntelLinks(t)
	body := functionBody(html, "function intelDecisionNode(decision, rerender)")
	if body == "" {
		t.Fatal("could not extract intelDecisionNode body")
	}
	for _, want := range []string{
		"document.createElement('button')",
		"item.className = 'intel-item intel-item--decision'",
		"item.dataset.decisionId = id",
		"id === expandedIntelDecisionId",
		"expandedIntelDecisionId = open ? '' : id",
		"'intel-decision__detail'",
		"jumpToMemoryMeeting(meetingId)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("intelDecisionNode body missing %q", want)
		}
	}
}

// jumpToMemoryMeeting mirrors jumpToBoardCard: switch to the Memory tool, force
// a meetings reload, and toast honestly when the record has aged out.
func TestIndexJumpToMemoryMeeting(t *testing.T) {
	html := readIndexForIntelLinks(t)
	if !strings.Contains(html, "async function jumpToMemoryMeeting(meetingId)") {
		t.Fatal("index.html missing jumpToMemoryMeeting definition")
	}
	body := functionBody(html, "function jumpToMemoryMeeting(meetingId)")
	if body == "" {
		t.Fatal("could not extract jumpToMemoryMeeting body")
	}
	for _, want := range []string{
		"expandedMemoryMeetingId = meetingId",
		"setActiveTool('memory')",
		"await loadMeetingsForMemory(true)",
		".memory-card[data-meeting-id=\"${selectorEscape(meetingId)}\"]",
		"that meeting is no longer in memory",
		"node.classList.add('is-fresh')",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("jumpToMemoryMeeting body missing %q", want)
		}
	}
}

// The memory card carries the jump anchor jumpToMemoryMeeting queries.
func TestIndexMemoryMeetingCardHasAnchor(t *testing.T) {
	html := readIndexForIntelLinks(t)
	body := functionBody(html, "function renderMemoryMeetingCard(meeting, bucket)")
	if body == "" {
		t.Fatal("could not extract renderMemoryMeetingCard body")
	}
	if !strings.Contains(body, "item.dataset.meetingId = String(meeting?.id || '')") {
		t.Error("renderMemoryMeetingCard must stamp data-meeting-id as the jump anchor")
	}
}

// The package binder reuses the shared clickable ledger row rather than a
// hand-rolled one, so package-attached decisions expand and jump too.
func TestIndexPackageBinderReusesIntelDecisionNode(t *testing.T) {
	html := readIndexForIntelLinks(t)
	body := functionBody(html, "function renderPackageDetails(record)")
	if body == "" {
		t.Fatal("could not extract renderPackageDetails body")
	}
	if !strings.Contains(body, "intelDecisionNode(decision, renderPackages)") {
		t.Error("renderPackageDetails must route package decisions through the shared intelDecisionNode helper")
	}
}

// The expand/hover/focus affordance and the jump-landing flash are styled, not
// unstyled stubs.
func TestIndexIntelDecisionStyles(t *testing.T) {
	html := readIndexForIntelLinks(t)
	for _, want := range []string{
		".intel-item--decision {",
		".intel-item--decision:focus-visible {",
		".intel-decision__detail {",
		".memory-card.is-fresh {",
		"@keyframes memory-card-flash {",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing decision-links style marker %q", want)
		}
	}
}
