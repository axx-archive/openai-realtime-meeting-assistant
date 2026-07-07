package main

// Card 070 frontend pins: the private/public scope toggle is gone; the chat
// rail shows two always-visible labeled sections (channels · whole office /
// private · you + Scout), each with its own create +, and the composer carries
// a destination guard naming where the next message lands (hot for a public
// channel, calm for a private thread).

import (
	"os"
	"strings"
	"testing"
)

func readIndexForThreadNavSplit(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// The dual-purpose scope toggle is fully retired from markup and JS.
func TestIndexThreadNavScopeToggleGone(t *testing.T) {
	html := readIndexForThreadNavSplit(t)
	for _, gone := range []string{
		"chat-new-scope",
		`id="chatScopeChannel"`,
		`id="chatScopePrivate"`,
		"newChatThreadVisibility",
		"function setNewChatThreadVisibility",
		"syncChatScopeToSelectedThread",
	} {
		if strings.Contains(html, gone) {
			t.Errorf("index.html still carries retired scope-toggle marker %q", gone)
		}
	}
}

// Both sections render at once: each caption is present, unhidden, and headlines
// its audience; each has its own create affordance.
func TestIndexThreadNavBothSectionsAlwaysVisible(t *testing.T) {
	html := readIndexForThreadNavSplit(t)
	for _, want := range []string{
		`<span id="chatChannelsLabel" class="chat-threads__label">channels · whole office</span>`,
		`<span id="chatPrivateLabel" class="chat-threads__label">private · you + Scout</span>`,
		`id="chatNewChannel"`,
		`id="chatNewThread"`,
		".chat-threads__section-head {",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing both-sections marker %q", want)
		}
	}
	// the captions must no longer be display:none — the section-head style is
	// their new home and they carry no hidden attribute
	if strings.Contains(html, `id="chatChannelsLabel" class="chat-threads__label" hidden`) {
		t.Error("the channels caption must render permanently, not hidden behind a scope")
	}

	// the channels create + opens the inline glass name field; new-thread is
	// private-only now (no public branch in startNewScoutThread)
	if !strings.Contains(html, "chatNewChannel?.addEventListener('click', () => setChannelCreateOpen(true))") {
		t.Error("the channels + must open the inline channel-create form")
	}
	startBody := functionBody(html, "async function startNewScoutThread()")
	if startBody == "" {
		t.Fatal("could not extract startNewScoutThread body")
	}
	if strings.Contains(startBody, "setChannelCreateOpen(true)") {
		t.Error("startNewScoutThread must always create a private thread, never open channel creation")
	}
	if !strings.Contains(startBody, "createScoutChatThreadOnServer('Scout', 'private')") {
		t.Error("startNewScoutThread must create a private thread")
	}
}

// renderChatAgentThreads populates both lists on every pass with no scope gate.
func TestIndexThreadNavRendersBothLists(t *testing.T) {
	html := readIndexForThreadNavSplit(t)
	body := functionBody(html, "function renderChatAgentThreads()")
	if body == "" {
		t.Fatal("could not extract renderChatAgentThreads body")
	}
	for _, want := range []string{
		"const channels = scoutChatThreads.filter(thread => chatThreadIsChannel(thread))",
		"const privates = scoutChatThreads.filter(thread => !chatThreadIsChannel(thread))",
		"chatDefaultThread.hidden = privates.length > 0",
		"chatChannelsEmpty.hidden = channels.length > 0",
		"chatChannelThreads?.replaceChildren(",
		"chatAgentThreads.replaceChildren(",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("renderChatAgentThreads body missing %q", want)
		}
	}
}

// The composer destination guard exists, is synced when the active thread is
// (re)rendered, and names the public audience hot vs the private audience calm.
func TestIndexComposerDestinationGuard(t *testing.T) {
	html := readIndexForThreadNavSplit(t)
	if !strings.Contains(html, `id="scoutChatDestination"`) {
		t.Fatal("index.html missing the #scoutChatDestination pill")
	}
	if !strings.Contains(html, `aria-live="polite"`) {
		t.Error("the destination pill should announce changes politely")
	}
	if !strings.Contains(html, ".scout-chat-destination--channel {") {
		t.Error("index.html missing the hot channel-tint style for the destination pill")
	}

	renderBody := functionBody(html, "function renderScoutChatDestination()")
	if renderBody == "" {
		t.Fatal("could not extract renderScoutChatDestination body")
	}
	for _, want := range []string{
		"everyone in the office",
		"private to you",
		"scout-chat-destination--channel",
		"setAttribute('aria-label'",
	} {
		if !strings.Contains(renderBody, want) {
			t.Errorf("renderScoutChatDestination body missing %q", want)
		}
	}

	// the active-thread render pass must keep the guard in lockstep
	activeBody := functionBody(html, "function renderActiveScoutThread()")
	if !strings.Contains(activeBody, "renderScoutChatDestination()") {
		t.Error("renderActiveScoutThread must sync the destination guard")
	}
	headBody := functionBody(html, "function syncChatConvoHeader()")
	if !strings.Contains(headBody, "renderScoutChatDestination()") {
		t.Error("syncChatConvoHeader must sync the destination guard")
	}
}
