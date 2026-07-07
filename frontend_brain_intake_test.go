package main

// Frontend half of the guided "Feed the brain" intake (card 082). Grep-style
// pins in the frontend_attachments_test.go idiom: index.html carries the
// Feed-the-brain entry point, startBrainIntake posts intake:'brain' to the
// chat-threads route and lands the user in the seeded thread, and the composer
// placeholder coaches the guided flow.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForBrainIntake(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func TestIndexBrainIntakeEntryPoint(t *testing.T) {
	html := readIndexForBrainIntake(t)

	// The empty-state door exists and is wired to startBrainIntake.
	for _, want := range []string{
		"brain.className = 'scout-chat-brain-chip'",
		"brain.textContent = 'Feed the brain'",
		"brain.addEventListener('click', startBrainIntake)",
		"async function startBrainIntake()",
		".scout-chat-brain-chip {",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing brain-intake entry hook %q", want)
		}
	}

	// startBrainIntake posts intake:'brain' and opens the returned seeded thread.
	body := functionBody(html, "async function startBrainIntake()")
	if body == "" {
		t.Fatal("cannot scope startBrainIntake")
	}
	for _, want := range []string{
		"postAuthJSON('/assistant/chat-threads', { title: 'Feed the brain', intake: 'brain' })",
		"upsertScoutChatThread(result.data.thread)",
		"selectScoutChatThread(thread.id)",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("startBrainIntake missing %q", want)
		}
	}
}

func TestIndexBrainIntakePlaceholderIsGuided(t *testing.T) {
	html := readIndexForBrainIntake(t)

	// The composer placeholder coaches the guided flow only for an intake
	// thread — the channel/private copy stays untouched otherwise.
	body := functionBody(html, "function scoutChatDefaultPlaceholder()")
	if body == "" {
		t.Fatal("cannot scope scoutChatDefaultPlaceholder")
	}
	if !strings.Contains(body, "String(thread?.intake || '').toLowerCase() === 'brain'") {
		t.Fatal("scoutChatDefaultPlaceholder must branch on the intake flag")
	}
	if !strings.Contains(body, "answer, attach files, or say") {
		t.Fatal("scoutChatDefaultPlaceholder missing intake coaching copy")
	}
	// The other two placeholders survive (the private copy is the topline-polish
	// wave's "tap + to run a task" line the intake branch layers onto).
	if !strings.Contains(body, "message the thread — @scout to ask") || !strings.Contains(body, "ask Scout, or tap + to run a task") {
		t.Fatal("scoutChatDefaultPlaceholder dropped an existing placeholder")
	}
}
