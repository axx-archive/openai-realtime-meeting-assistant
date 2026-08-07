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
		`id="chatContextMentionPopover"`,
		`id="chatContextMentionList"`,
		`id="roomChatMentionPopover"`,
		`id="roomChatMentionList"`,
		`role="listbox"`,
		// channel-gated token detection: private threads never open the popover
		"function mentionTokenAtCaret(input)",
		"chatThreadIsChannel(selectedScoutChatThread())",
		// roster + scout as the candidate set
		"function mentionRosterCandidates()",
		"desktopChatMentionCandidates",
		// open/steer/select machinery
		"function updateMentionAutocomplete(input = scoutChatInput)",
		"function renderMentionPopover()",
		"roleTitle: clean.toLowerCase() === 'scout' ? 'Chief of staff' : ''",
		"`${roleTitle || 'Chief of staff'} · AI`",
		"`${roleTitle || 'Specialist'} · AI`",
		"roleTitle || 'Teammate'",
		"function applyMentionCompletion(candidate)",
		"function mentionPopoverIsOpen()",
		"scout-mention-popover__item",
		// completion inserts "@Name " and re-seats the caret
		"const inserted = '@' + handle + ' '",
		"input.setSelectionRange(end, end)",
		// composer wiring: input opens/filters, blur closes
		"void updateDocumentAutocomplete(scoutChatInput)",
		"if (!documentTokenAtCaret(scoutChatInput)) updateMentionAutocomplete(scoutChatInput)",
		"void updateDocumentAutocomplete(chatContextReplyInput)",
		"if (!documentTokenAtCaret(chatContextReplyInput)) updateMentionAutocomplete(chatContextReplyInput)",
		"updateMentionAutocomplete(roomChatInput)",
		"scoutChatInput?.addEventListener('blur', closeMentionPopover)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing mention autocomplete hook %q", want)
		}
	}
	popoverStart := strings.Index(html, "function renderMentionPopover()")
	if popoverStart < 0 {
		t.Fatal("could not isolate mention popover renderer")
	}
	popoverEnd := strings.Index(html[popoverStart:], "function applyMentionCompletion(candidate)")
	if popoverEnd < 0 {
		t.Fatal("could not isolate mention popover renderer")
	}
	popover := html[popoverStart : popoverStart+popoverEnd]
	for _, forbidden := range []string{"agent · confirm work", "AI teammate"} {
		if strings.Contains(popover, forbidden) {
			t.Fatalf("mention popover retained obsolete role label %q", forbidden)
		}
	}
	for _, want := range []string{
		"candidate?.handle",
		"replace(/\\s+/g, '-')",
		"@([A-Za-z0-9._-]*)$",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("multiword mention handle support missing %q", want)
		}
	}

	// The shared steering handler must be installed in capture phase on both
	// composers so Enter completes a mention before either submit path runs.
	keydownStart := strings.Index(html, "function handleMentionKeydown(event)")
	if keydownStart < 0 {
		t.Fatal("shared mention keydown handler missing")
	}
	keydownTail := html[keydownStart:]
	for _, want := range []string{"'ArrowDown'", "'ArrowUp'", "'Enter' || event.key === 'Tab'", "'Escape'"} {
		if !strings.Contains(keydownTail[:1800], want) {
			t.Fatalf("mention keydown handler missing %q", want)
		}
	}
	for _, want := range []string{
		"scoutChatInput?.addEventListener('keydown', handleMentionKeydown, true)",
		"chatContextReplyInput?.addEventListener('keydown', handleMentionKeydown, true)",
		"roomChatInput?.addEventListener('keydown', handleMentionKeydown, true)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("mention keydown capture wiring missing %q", want)
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

func TestIndexChatComposersUseTypedMentionsWithoutPersistentScoutShortcuts(t *testing.T) {
	html := readIndexForChatMentions(t)
	for _, forbidden := range []string{`id="scoutChatScoutMention"`, "insertChannelScoutMention", `id="roomChatScoutMention"`, "insertRoomScoutMention"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("channel composer retained obsolete Scout shortcut %q", forbidden)
		}
	}
}
