package main

// PiP-over-chat clearance + private-brain note (cards 071/072). These pins
// hold: the docked meeting PiP never buries the newest chat messages (the
// feed carries scrollable slack under both deck shapes, on desktop and
// tablet), the in-room chat rail stays clamped to the viewport so its
// composer never slides under the fixed meeting dock, and the private
// composer carries a persistent caveat that private threads still feed the
// company brain — hidden on public channels.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForPipPrivacy(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func TestIndexPipMeetingClearsChatFeed(t *testing.T) {
	html := readIndexForPipPrivacy(t)
	for _, want := range []string{
		// the composer end clears the docked window (pre-existing rules)
		`body:has(#pipMeeting:not([hidden])) #appShell[data-tool="chat"] .scout-chat-form`,
		// the feed itself gets bottom slack so the auto-scrolled newest
		// messages can always ride above the PiP band
		`body:has(#pipMeeting:not([hidden])) #appShell[data-tool="chat"] .scout-chat-thread`,
		"padding-bottom: 190px;",
		// in-room chat rail: viewport clamp so the thread scrolls instead of
		// pushing the composer under the fixed meeting dock
		"max-height: calc(100svh - var(--shell-topbar-height, 0px) - 116px);",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing PiP chat clearance marker %q", want)
		}
	}
	// the feed slack must exist in BOTH PiP ranges — desktop (≥861px) and
	// tablet (641–860px) — one rule per media block
	threadRule := `body:has(#pipMeeting:not([hidden])) #appShell[data-tool="chat"] .scout-chat-thread`
	if got := strings.Count(html, threadRule); got < 2 {
		t.Fatalf("chat feed PiP clearance must cover desktop and tablet blocks, found %d rule(s)", got)
	}
}

func TestIndexPrivateChatCarriesBrainNote(t *testing.T) {
	html := readIndexForPipPrivacy(t)
	for _, want := range []string{
		// the persistent caveat under the private composer
		`<p id="scoutChatBrainNote" class="scout-chat-brain-note">private from teammates · recorded to the company brain</p>`,
		".scout-chat-brain-note {",
		".scout-chat-brain-note[hidden] {",
		"const scoutChatBrainNote = document.getElementById('scoutChatBrainNote')",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing private brain-note marker %q", want)
		}
	}
	// the header sync owns the toggle: shown on private threads, hidden on
	// public channels (they're public anyway)
	body := functionBody(html, "function syncChatConvoHeader()")
	if body == "" {
		t.Fatal("index.html missing syncChatConvoHeader")
	}
	if !strings.Contains(body, "scoutChatBrainNote.hidden = isChannel") {
		t.Fatal("syncChatConvoHeader must hide the brain note on channels and show it on private threads")
	}
}
