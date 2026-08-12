package main

// PiP-over-chat clearance + concise private-audience treatment (cards 071/072). These pins
// hold: the docked meeting PiP never buries the newest chat messages (the
// feed carries scrollable slack under both deck shapes, on desktop and
// tablet), the in-room chat rail stays clamped to the viewport so its
// composer never slides under the fixed meeting dock. The private audience is
// expressed in the conversation header instead of a redundant footer below
// the composer. The server-side
// proof of that contract lives in TestPrivateChatBrainContract
// (private_chat_brain_contract_test.go): private thread messages never reach
// store.search, contextEntriesForQuery, the brain window, or meeting memory.

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

func TestIndexPrivateChatKeepsAudienceInHeaderWithoutComposerFooter(t *testing.T) {
	html := readIndexForPipPrivacy(t)
	for _, want := range []string{
		`id="chatConvoPolicy" class="desktop-chat-context__policy">only you + Scout</span>`,
		"const policy = !isChannel ? `only you + ${privateTarget}`",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing concise private-audience marker %q", want)
		}
	}
	for _, forbidden := range []string{
		`id="scoutChatBrainNote"`,
		".scout-chat-brain-note",
		"const scoutChatBrainNote",
		"scoutChatBrainNote.hidden",
		"not shared with your organization",
		"not shared with the company brain",
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("index.html must not render redundant private-composer footer %q", forbidden)
		}
	}
}
