package main

import (
	"os"
	"strings"
	"testing"
)

// AJ 2026-09-02: #meetings + Bonfire Chat are the two pinned org channels in
// the thread list — sorted first under "channels · whole office", ember titles
// through the --ember-text token, no dot and no STRIDE badge, no archive
// affordance, no rename; unread weight and the mute bell keep working.
func TestIndexPinnedChannelsInThreadList(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	require := func(section, body string, wants ...string) {
		t.Helper()
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %q", section, want)
			}
		}
	}
	forbid := func(section, body string, gones ...string) {
		t.Helper()
		for _, gone := range gones {
			if strings.Contains(body, gone) {
				t.Errorf("%s: must not contain %q", section, gone)
			}
		}
	}

	// the classifier pair: #meetings is a public thread the server flags
	// system="meetings"; the pinned set is Bonfire Chat + #meetings
	require("classifiers", html,
		"function chatThreadIsSystemMeetings(thread) {\n\t        return chatThreadIsChannel(thread) && String(thread?.system || '').toLowerCase() === 'meetings'",
		"function chatThreadIsPinnedChannel(thread) {\n\t        return chatThreadIsTeam(thread) || chatThreadIsSystemMeetings(thread)",
		// a pinned channel is never a project channel
		"return chatThreadIsChannel(thread) && !chatThreadIsPinnedChannel(thread) && !chatThreadIsHumanGroup(thread) && !thread?.archivedAt",
	)

	// both sort sites: Bonfire Chat first, #meetings second, then the rest
	sortExpr := "Number(chatThreadIsTeam(right)) - Number(chatThreadIsTeam(left)) || Number(chatThreadIsSystemMeetings(right)) - Number(chatThreadIsSystemMeetings(left)))"
	if got := strings.Count(html, sortExpr); got != 2 {
		t.Errorf("pinned sort expression appears %d times, want 2 (upsert + section render)", got)
	}

	// the row: ember title class on both pinned channels, no badge, no archive
	// button; unread dot + mute glyph untouched
	row := functionBody(html, "function chatThreadRowNode(thread, q)")
	if row == "" {
		t.Fatal("chatThreadRowNode is missing")
	}
	require("thread row", row,
		"const isPinnedChannel = chatThreadIsPinnedChannel(thread)",
		"titleEl.classList.toggle('chat-thread-item__title--bonfire-chat', isPinnedChannel)",
		"item.classList.toggle('chat-thread-item--pinned', isPinnedChannel)",
		"if (!isPinnedChannel && (!isChannel || (ownEmail && ownerEmail === ownEmail) || canAdminArchive)) {",
		"unreadDot.className = 'chat-thread-item__unread-dot'",
		"const mutedGlyph = chatThreadMutedGlyphNode(thread)",
	)
	forbid("thread row", row, "strideTag", "'STRIDE'", "chat-thread-item__stride-tag")
	forbid("stylesheet", html, ".chat-thread-item__stride-tag", "chat-thread-item__stride-tag::before")

	// ember through the token, never raw hex
	require("ember title CSS", html, ".chat-thread-item__title--bonfire-chat {\n        color: var(--ember-text);")
	forbid("ember title CSS", html[strings.Index(html, ".chat-thread-item__title--bonfire-chat {"):][:200], "#FF5A19", "#ff5a19")

	// header: pinned channels cannot be renamed; the eyebrow drops "Stride"
	require("conversation header", html,
		"chatConvoRename.hidden = !thread || chatRenameActive || chatThreadIsPinnedChannel(thread) || privateRiffThread(thread)",
		"const scope = !isChannel ? 'private' : isPinned ? 'pinned · whole office' : 'project channel'",
	)
	forbid("conversation header", html, "'pinned · Stride'")
}
