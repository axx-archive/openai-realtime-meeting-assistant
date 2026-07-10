package main

// 2026-07-10 incident (room "Impossible Moments"): guest room chat rendered
// INVISIBLY — history arrived and painted, but #roomChatPanel is a child of
// <aside class="scout-rail"> and the guest chrome hide
// (#appShell.is-guest .scout-rail { display: none !important }) beat the
// member chat reveal, which carries no !important (an !important conflict,
// not specificity). These pins hold the narrow guest exception: the rail
// chrome stays gone, but opening room chat re-shows the rail as a chat-only
// host with the Scout panel pinned hidden.

import (
	"strings"
	"testing"
)

func cssRuleBodyForGuestChat(t *testing.T, html, selector string) string {
	t.Helper()
	at := strings.Index(html, selector)
	if at == -1 {
		t.Fatalf("CSS rule %q is missing", selector)
	}
	block := html[at:]
	if end := strings.Index(block, "}"); end != -1 {
		block = block[:end]
	}
	return block
}

// The reveal: a guest tab with room chat open re-shows the rail (the chat
// host). It must carry !important — the guest chrome hide is !important, so
// specificity alone can never win — and it must mirror the member reveal's
// scoping (in-room, room tool, board not expanded) so the sheet keeps its
// mobile display rules.
func TestIndexGuestRoomChatPanelVisibleWhenOpen(t *testing.T) {
	html := readIndexHTMLForGuest(t)

	reveal := cssRuleBodyForGuestChat(t, html,
		`#appShell.is-guest.is-room-chat-open.is-in-room[data-tool="room"]:not(.is-board-expanded) .scout-rail`)
	if !strings.Contains(reveal, "display: grid !important") {
		t.Error("the guest room-chat reveal must display: grid !important (the guest chrome hide is !important)")
	}

	// the Scout panel never exists for guests, even while the rail hosts chat
	panel := cssRuleBodyForGuestChat(t, html, "#appShell.is-guest .scout-rail .scout-panel")
	if !strings.Contains(panel, "display: none !important") {
		t.Error("the guest-scoped Scout panel hide must display: none !important")
	}

	// the reveal is an exception, not an unhide: the rail chrome hide keeps
	// its !important teeth and precedes the reveal in the sheet
	hideAt := strings.Index(html, "#appShell.is-guest .scout-rail,")
	revealAt := strings.Index(html, "#appShell.is-guest.is-room-chat-open")
	if hideAt == -1 || revealAt == -1 || revealAt < hideAt {
		t.Error("the guest chrome hide must stay in place, with the room-chat reveal as a later exception")
	}

	// the chat toggle is guest chrome — it must never join the member-only
	// meeting-bar hide list
	if strings.Contains(html, "#appShell.is-guest #roomChatToggle") {
		t.Error("#roomChatToggle must stay visible for guests (room chat is part of the guest surface)")
	}
}
