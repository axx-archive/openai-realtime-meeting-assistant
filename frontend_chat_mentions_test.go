package main

// The @-mention contract's frontend half. These grep-style pins hold: the
// composer's autocomplete popover exists and is channel-gated, keyboard
// steering runs in capture phase ahead of the Enter-send handler, completion
// inserts "@Name " at the token anchor, and sent bubbles lift mentions via a
// DOM-built (injection-safe) span pass in the message-text renderer.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForChatMentions(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func TestIndexMentionAutocompleteWiring(t *testing.T) {
	html := readIndexForChatMentions(t)
	for _, want := range []string{
		// the popover element + its listbox
		`id="scoutMentionPopover"`,
		`id="scoutMentionList"`,
		`role="listbox"`,
		// channel-gated token detection: private threads never open the popover
		"function mentionTokenAtCaret()",
		"chatThreadIsChannel(selectedScoutChatThread())",
		// roster + scout as the candidate set
		"function mentionRosterNames()",
		"[...participantNames(), 'scout']",
		// open/steer/select machinery
		"function updateMentionAutocomplete()",
		"function renderMentionPopover()",
		"function applyMentionCompletion(name)",
		"function mentionPopoverIsOpen()",
		"scout-mention-popover__item",
		// completion inserts "@Name " and re-seats the caret
		"const inserted = '@' + name + ' '",
		"scoutChatInput.setSelectionRange(end, end)",
		// composer wiring: input opens/filters, blur closes
		"scoutChatInput?.addEventListener('input', updateMentionAutocomplete)",
		"scoutChatInput?.addEventListener('blur', closeMentionPopover)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing mention autocomplete hook %q", want)
		}
	}

	// The steering keydown must run in capture phase (trailing `, true)`) so
	// Enter completes the mention instead of sending the half-typed message.
	keydownStart := strings.Index(html, "if (!mentionPopoverIsOpen()) return")
	if keydownStart < 0 {
		t.Fatal("mention keydown guard missing")
	}
	keydownTail := html[keydownStart:]
	end := strings.Index(keydownTail, "}, true)")
	if end < 0 || end > 1600 {
		t.Fatal("mention keydown handler must register in capture phase (`, true`) like the palette handler")
	}
	for _, want := range []string{"'ArrowDown'", "'ArrowUp'", "'Enter' || event.key === 'Tab'", "'Escape'"} {
		if !strings.Contains(keydownTail[:end], want) {
			t.Fatalf("mention keydown handler missing %q", want)
		}
	}
}

func TestIndexMentionHighlightInMessageRenderer(t *testing.T) {
	html := readIndexForChatMentions(t)
	for _, want := range []string{
		// the safe span pass + its hook inside scoutChatMessageNode's text branch
		"function appendChatMentionTextNodes(target, text)",
		"appendChatMentionTextNodes(body, text)",
		"chip.className = 'scout-chat-mention'",
		// DOM-built, never innerHTML: the highlight must stay injection-safe
		"target.appendChild(document.createTextNode(value.slice(last, start)))",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing mention highlight hook %q", want)
		}
	}
	highlightStart := strings.Index(html, "function appendChatMentionTextNodes(target, text)")
	highlightEnd := strings.Index(html, "function chatPeerInitials(label)")
	if highlightStart < 0 || highlightEnd < 0 || highlightEnd <= highlightStart {
		t.Fatal("cannot scope appendChatMentionTextNodes")
	}
	if strings.Contains(html[highlightStart:highlightEnd], "innerHTML") {
		t.Fatal("appendChatMentionTextNodes must never touch innerHTML")
	}
}
