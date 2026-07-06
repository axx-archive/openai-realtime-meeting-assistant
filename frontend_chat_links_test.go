package main

// Pasted links + scrollable chat (cards 065/092). These grep-style pins
// hold: every chat surface (room chat, scout private threads, public
// channels) renders bare http(s) URLs through one DOM-built anchor recipe
// (never innerHTML), the anchor styling covers plain bubbles in both
// themes, and the phone convo pane re-pins to the newest message after the
// stacked navigator flips it visible.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForChatLinks(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func TestIndexChatLinkifyWiring(t *testing.T) {
	html := readIndexForChatLinks(t)
	for _, want := range []string{
		// the shared anchor recipe: scheme-pinned href, new tab, no opener
		"function chatLinkNode(href, label)",
		"link.target = '_blank'",
		"link.rel = 'noreferrer noopener'",
		// the bare-URL pass over plain text + the trail-punctuation peel
		"function appendChatLinkTextNodes(target, text, appendSegment)",
		"function appendChatUrlNodes(target, raw)",
		// room chat bubbles route through it (was body.textContent)
		"appendChatLinkTextNodes(body, String(message?.text || ''))",
		// scout user/peer bubbles: URLs split before the mention pass
		"appendChatLinkTextNodes(target, text, segment => appendChatMentionSegmentNodes(target, segment))",
		// scout rich text: the inline pattern gains a bare https?:// alternative
		"|(https?:\\/\\/[^\\s<>]+)/g",
		// plain-bubble anchors styled beside the rich ones (both themes inherit)
		".chat-rich a,\n      .scout-chat-text a {",
		"overflow-wrap: anywhere",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing chat link hook %q", want)
		}
	}
	// The whole link pass stays DOM-built — never innerHTML on message text.
	start := strings.Index(html, "function chatLinkNode(href, label)")
	end := strings.Index(html, "function scoutChatSelfHandle()")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("cannot scope the chat link helpers")
	}
	if strings.Contains(html[start:end], "innerHTML") {
		t.Fatal("chat link helpers must never touch innerHTML")
	}
}

func TestIndexMobileConvoScrollRepin(t *testing.T) {
	html := readIndexForChatLinks(t)
	body := functionBody(html, "function setMobileChatView(view)")
	if body == "" {
		t.Fatal("index.html missing setMobileChatView")
	}
	for _, want := range []string{
		"chatToolSection.dataset.chatView = mobileChatView",
		// the convo pane was display:none while the thread rendered, so the
		// flip re-pins to the newest message (rAF for post-layout certainty)
		"mobileChatView === 'convo'",
		"scoutChatThread.scrollTop = scoutChatThread.scrollHeight",
		"window.requestAnimationFrame",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("setMobileChatView missing convo re-pin marker %q", want)
		}
	}
}
