package main

// STRIDE v2.0 Wave 1 (Conversations completeness) — the frontend half of the
// contracts. Static grep-style pins on index.html modeled on
// frontend_chat_mentions_test.go: the groups section and its create form post
// the human_group contract with an operationId, the members popover PATCHes
// the member route, read markers POST the flat read route, typing rides the
// chat_typing websocket frame, the notification menu offers exactly the three
// server levels, and the typing ripple is fully disabled under
// prefers-reduced-motion.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForConversationsWave1(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// wave1FunctionBody isolates one top-level function declaration by its
// signature so a contract can be pinned inside the right body.
func wave1FunctionBody(html, signature string) string {
	start := strings.Index(html, signature)
	if start < 0 {
		return ""
	}
	rest := html[start:]
	// the chat region mixes tab- and space-indented declarations; stop at
	// whichever sibling declaration comes first
	end := -1
	for _, marker := range []string{"\n\t      function ", "\n\t      async function ", "\n      function ", "\n      async function "} {
		if at := strings.Index(rest[1:], marker); at >= 0 && (end < 0 || at < end) {
			end = at
		}
	}
	if end < 0 {
		return rest
	}
	return rest[:end+1]
}

func TestIndexConversationsWave1GroupsSection(t *testing.T) {
	html := readIndexForConversationsWave1(t)
	for _, want := range []string{
		// the third sidebar section head mirrors the channels head (label + inline +)
		`<span id="chatGroupsLabel" class="chat-threads__label">groups · you + people</span>`,
		`id="chatNewGroup"`,
		// the create form is the channel form's visual family plus a member picker
		`<form id="chatGroupCreate" class="chat-channel-create chat-group-create" hidden>`,
		`id="chatGroupName"`,
		`id="chatGroupCreateGo"`,
		`<div id="chatGroupMembers" class="chat-member-picker" data-member-picker="group-create"></div>`,
		`<div id="chatGroupThreads" class="chat-agent-threads" aria-label="Group conversations"></div>`,
		`id="chatGroupsEmpty"`,
		// the + opens the form; submit needs at least one member
		"chatNewGroup?.addEventListener('click', () => setGroupCreateOpen(chatGroupCreate?.hidden !== false))",
		"function setGroupCreateOpen(open)",
		"function createChatMemberPicker(root, options = {})",
		"function chatThreadIsHumanGroup(thread)",
		"return String(thread?.conversationKind || '').toLowerCase() === 'human_group'",
		// the render buckets: two filters, groups leave the channel list for their own
		"const channels = scoutChatThreads.filter(thread => chatThreadIsChannel(thread)).filter(thread => !chatThreadIsHumanGroup(thread))",
		"const groups = scoutChatThreads.filter(thread => chatThreadIsHumanGroup(thread))",
		"chatGroupsEmpty.hidden = groups.length > 0",
		// one shared icon builder for rows, search hits and the conversations group
		"function chatThreadIconNode(kind, thread)",
		"const icon = chatThreadIconNode(chatThreadIconKind(thread), thread)",
		"return chatThreadIconNode(kind, thread || { memberEmails: [result?.authorEmail] })",
		// one shared avatar core with a fallback-text callback
		"function chatAvatarDiscNode(email, className, fallbackText)",
		"return chatAvatarDiscNode(key, className, () => (key ? chatPeerInitials(chatPersonDisplayName(key)) : '·'))",
		"return chatAvatarDiscNode(email, className, () => (agent",
		"chatGroupThreads?.replaceChildren(...groups.map(thread => chatThreadRowNode(thread, q)))",
		// group rows lead with the avatar cluster, not the # glyph
		"function chatThreadAvatarClusterNode(thread)",
		"icon.classList.add('chat-thread-item__icon--cluster')",
		"const faces = pool.slice(0, 3)",
		// the header eyebrow counts people
		"? `${groupCount} ${groupCount === 1 ? 'person' : 'people'} · shared memory`",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing groups-section marker %q", want)
		}
	}

	// section order: channels · groups · private
	channels := strings.Index(html, `id="chatChannelsLabel"`)
	groups := strings.Index(html, `id="chatGroupsLabel"`)
	private := strings.Index(html, `id="chatPrivateLabel"`)
	if !(channels >= 0 && groups > channels && private > groups) {
		t.Errorf("groups section must sit between channels and private (channels=%d groups=%d private=%d)", channels, groups, private)
	}

	// the member picker only offers people
	picker := wave1FunctionBody(html, "function chatMemberPickerCandidates(query, excluded)")
	if picker == "" {
		t.Fatal("could not isolate chatMemberPickerCandidates")
	}
	if !strings.Contains(picker, "if (String(candidate?.kind || 'person').toLowerCase() !== 'person') continue") {
		t.Error("member picker must offer only kind === 'person' directory entries")
	}
	if !strings.Contains(picker, "email === viewer") {
		t.Error("member picker must never offer the viewer")
	}
}

func TestIndexConversationsWave1CreatePostsHumanGroupContract(t *testing.T) {
	html := readIndexForConversationsWave1(t)
	body := wave1FunctionBody(html, "async function createHumanGroupOnServer(title, memberEmails)")
	if body == "" {
		t.Fatal("could not isolate createHumanGroupOnServer")
	}
	for _, want := range []string{
		"postAuthJSON('/assistant/chat-threads', {",
		"conversationKind: 'human_group',",
		"memberEmails,",
		"operationId: chatOperationId()",
		// the new group is selected like any channel row
		"selectScoutChatThread(thread.id)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("createHumanGroupOnServer missing %q", want)
		}
	}
	// the operation id is a real uuid with a fallback
	opID := wave1FunctionBody(html, "function chatOperationId()")
	if !strings.Contains(opID, "globalThis.crypto?.randomUUID?.()") {
		t.Error("chatOperationId must use crypto.randomUUID with a fallback")
	}
	// submit refuses an empty member list
	if !strings.Contains(html, "const memberEmails = chatGroupMemberPicker ? chatGroupMemberPicker.selected() : []") ||
		!strings.Contains(html, "if (!memberEmails.length) {\n\t          chatGroupMemberPicker?.focus()\n\t          return\n\t        }") {
		t.Error("group create must require at least one member before posting")
	}
}

func TestIndexConversationsWave1MembersPatchContract(t *testing.T) {
	html := readIndexForConversationsWave1(t)
	for _, want := range []string{
		`id="chatConvoMembers"`,
		`id="chatConvoMembersOpen"`,
		`id="chatConvoMembersPopover"`,
		`id="chatConvoMembersList"`,
		`id="chatConvoMembersAdd"`,
		`<div id="chatConvoMembersPicker" class="chat-member-picker" data-member-picker="members-add"></div>`,
		"function renderChatConvoMembers()",
		// owner marked in mono; × only for the owner
		"row.appendChild(bfEl('span', 'chat-convo-member__owner', 'owner'))",
		"} else if (viewerOwns) {",
		"if (chatConvoMembersAdd) chatConvoMembersAdd.hidden = !viewerOwns",
		// an inbound member list without the viewer drops the row
		"function dropChatThreadForViewer(threadId, options = {})",
		"dropChatThreadForViewer(id)",
		"const fallback = chatThreadFirstChannelId()",
		"memberEmails: Array.isArray(payload.memberEmails) ? payload.memberEmails : existing.memberEmails,",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing members marker %q", want)
		}
	}
	body := wave1FunctionBody(html, "async function patchChatThreadMembers(threadId, changes = {})")
	if body == "" {
		t.Fatal("could not isolate patchChatThreadMembers")
	}
	for _, want := range []string{
		"fetch(`/assistant/chat-threads/${encodeURIComponent(id)}/members`, {",
		"method: 'PATCH',",
		"body: JSON.stringify({ add, remove })",
		// optimistic with rollback; the 409 message comes from the server first
		"applyChatThreadPatch(id, { memberEmails: optimistic })",
		"applyChatThreadPatch(id, { memberEmails: previous })",
		"const serverMessage = String(payload?.error || payload?.message || '').trim()",
		"response.status === 409",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("patchChatThreadMembers missing %q", want)
		}
	}
	// the members mono owner tag really is mono
	if !strings.Contains(html, ".chat-convo-member__owner {\n        flex: none;\n        font: 600 10px/1.4 var(--font-mono);") {
		t.Error("owner tag must be set in the mono face (machine fact)")
	}
}

func TestIndexConversationsWave1ReadMarkers(t *testing.T) {
	html := readIndexForConversationsWave1(t)
	flush := wave1FunctionBody(html, "async function flushChatThreadReadMarker()")
	if flush == "" {
		t.Fatal("could not isolate flushChatThreadReadMarker")
	}
	if !strings.Contains(flush, "postAuthJSON('/assistant/threads/read', { threadId: pending.threadId, lastReadMessageId: pending.messageId })") {
		t.Error("read markers must POST /assistant/threads/read with threadId + lastReadMessageId")
	}
	schedule := wave1FunctionBody(html, "function scheduleChatThreadReadMarker(thread)")
	for _, want := range []string{
		"if (chatReadMarkerSent.get(threadId) === messageId) return",
		"chatReadMarkerTimer = window.setTimeout(flushChatThreadReadMarker, 400)",
	} {
		if !strings.Contains(schedule, want) {
			t.Errorf("read marker scheduling missing %q (debounce 400ms, skip unchanged)", want)
		}
	}
	for _, want := range []string{
		// selection and every tail render/append route through the in-view check
		"function noteScoutChatTailInView()",
		"if (!scoutChatThread || !scoutChatIsNearBottom()) return",
		// the sidebar dot and the seam derive from the server fields first
		"if (typeof thread.unreadCount === 'number' && Number.isFinite(thread.unreadCount)) {",
		"function chatThreadPriorSeenAt(thread)",
		"const lastRead = String(thread.lastReadMessageId || '')",
		"const priorSeenAt = chatThreadPriorSeenAt(thread)",
		// the device-local map stays as the fallback
		"return String(chatThreadSeenMap()[String(thread.id)] || '')",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing read-marker marker %q", want)
		}
	}
	// the desktop-only gate on the seam is gone
	for _, gone := range []string{
		"const priorSeenAt = desktopChatLayoutQuery.matches && thread?.id",
		"if (!desktopChatLayoutQuery.matches) {\n\t          markChatThreadSeen(selectedScoutChatThread())",
	} {
		if strings.Contains(html, gone) {
			t.Errorf("index.html still gates the unread seam on the desktop layout: %q", gone)
		}
	}
	// and the seam is styled below the desktop breakpoint too: a base rule
	// ahead of the desktop chat cascade, which restates it for ≥861px
	base := strings.Index(html, "#chatTool .desktop-chat-unread {")
	// Wave 11 D15: the desktop chat cascade now opens with the D15 note (the
	// chat-only topbar suppression it replaced is gone)
	desktop := strings.Index(html, "/* Wave 11 D15: the topbar reads \"Conversations\" + the destination")
	if base < 0 || desktop < 0 || base > desktop {
		t.Error("the unread seam needs a base (phone) style ahead of the desktop chat cascade")
	}
	if strings.Count(html, "#chatTool .desktop-chat-unread {") < 2 {
		t.Error("the unread seam should be styled once at the base and once in the desktop cascade")
	}
}

func TestIndexConversationsWave1TypingPresence(t *testing.T) {
	html := readIndexForConversationsWave1(t)
	for _, want := range []string{
		`<div id="scoutChatTyping" class="scout-chat-typing" role="status" aria-live="polite" aria-atomic="true" hidden>`,
		`id="scoutChatTypingText"`,
		// outbound frame shape and cadence
		"socket.send(JSON.stringify({ event: 'chat_typing', data: JSON.stringify({ threadId, typing }) }))",
		"if (chatTypingActive && chatTypingThreadId === threadId && now - chatTypingSentAt < 3000) return",
		"sendChatTypingSignal(Boolean(scoutChatInput.value.trim()))",
		"scoutChatInput?.addEventListener('blur', () => sendChatTypingSignal(false))",
		// inbound dispatch + rendering rules
		"case 'chat_typing':\n            handleChatTypingEvent(message.data)",
		"function handleChatTypingEvent(payload)",
		"if (email === String(authedUser.email || '').trim().toLowerCase()) return",
		"if (!name || name.toLowerCase() === 'scout') return",
		"entries.set(email, { name, expiresAt: Date.now() + 4000 })",
		"if (names.length === 1) return `${names[0]} is typing…`",
		"if (names.length === 2) return `${names[0]} and ${names[1]} are typing…`",
		"return `${names.length} people are typing…`",
		// the ripple rides the motion tokens
		"animation: bf-typing-ripple calc(var(--dur-slow) * 3) var(--ease) infinite;",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing typing marker %q", want)
		}
	}
	// the send path drops typing before the composer clears
	send := wave1FunctionBody(html, "function sendScoutChatFromForm(event)")
	if !strings.Contains(send, "sendChatTypingSignal(false)") {
		t.Error("sending must emit typing:false")
	}
	// reduced motion fully disables the ripple
	rule := strings.Index(html, ".scout-chat-typing__dots span { animation: none; opacity: 0.7; transform: none; }")
	if rule < 0 {
		t.Fatal("typing ripple must be disabled under prefers-reduced-motion")
	}
	window := html[max(0, rule-200):rule]
	if !strings.Contains(window, "@media (prefers-reduced-motion: reduce)") {
		t.Error("the typing ripple's animation:none rule must live inside a prefers-reduced-motion block")
	}
}

func TestIndexConversationsWave1MuteLevels(t *testing.T) {
	html := readIndexForConversationsWave1(t)
	for _, want := range []string{
		`id="chatConvoNotify"`,
		`id="chatConvoNotifyOpen"`,
		`id="chatConvoNotifyMenu"`,
		`role="menuitemradio" aria-checked="true" data-notify-level="all">All messages</button>`,
		`role="menuitemradio" aria-checked="false" data-notify-level="mentions">Mentions only</button>`,
		`role="menuitemradio" aria-checked="false" data-notify-level="none">Nothing</button>`,
		"const chatNotificationLevels = ['all', 'mentions', 'none']",
		"postAuthJSON('/assistant/threads/mute', { threadId: id, level: next })",
		// S2: notificationLevel is the one source of truth; `muted` is derived
		"return normalizeChatNotificationLevel(thread?.notificationLevel) || 'all'",
		"...chatThreadViewerStateFromPayload(payload, id),",
		"chatConvoNotifyOpen.dataset.muted = level === 'all' ? 'false' : 'true'",
		// the mute race: an in-flight POST keeps the optimistic level through merges
		"const chatNotifyPending = new Set()",
		"chatNotifyPending.add(id)",
		"if (!chatNotifyPending.has(id)) state.notificationLevel = normalizeChatNotificationLevel(row?.notificationLevel) || 'all'",
		"if (current && revisionOrder > 0) return { ...current, ...viewerState }",
		// viewer state rides the list fingerprint
		"`${thread?.id || ''}:${thread?.updatedAt || ''}:${thread?.unreadCount ?? ''}:${thread?.lastReadMessageId ?? ''}:${thread?.notificationLevel ?? ''}`",
		// sidebar: bell-slash beside the time, dot suppressed at level none
		"function chatThreadMutedGlyphNode(thread)",
		"if (mutedGlyph) row.appendChild(mutedGlyph)",
		"if (chatThreadMuteLevel(thread) === 'none') {\n\t          return false\n\t        }",
		".chat-thread-item__muted {",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing mute marker %q", want)
		}
	}
	// exactly three levels in the menu
	if got := strings.Count(html, `data-notify-level="`); got != 3 {
		t.Errorf("expected exactly three notification levels in the menu markup, found %d", got)
	}
	// the dead muted-boolean branch is gone and no patch stores `muted`
	for _, gone := range []string{"else if (typeof payload?.muted === 'boolean')", "muted: next !== 'all'", "muted: row.muted"} {
		if strings.Contains(html, gone) {
			t.Errorf("client must derive muted from notificationLevel, never store it (found %q)", gone)
		}
	}
	// the muted glyph reads in --text-3, never ember
	glyph := html[strings.Index(html, ".chat-thread-item__muted {"):]
	glyph = glyph[:strings.Index(glyph, "}")]
	if !strings.Contains(glyph, "color: var(--text-3);") || strings.Contains(glyph, "ember") {
		t.Error("the muted glyph must be quiet (--text-3), not ember")
	}
}

func TestIndexConversationsWave1MessageSearch(t *testing.T) {
	html := readIndexForConversationsWave1(t)
	for _, want := range []string{
		`<div id="chatSearchResults" class="chat-search-results" role="listbox" aria-label="Message search results" hidden></div>`,
		// two characters, 250ms trailing debounce, limit 20, newest first from the server
		"const chatSearchMinChars = 2",
		"const chatSearchDebounceMs = 250",
		"const chatSearchLimit = 20",
		"fetch(`/assistant/chat-search?q=${encodeURIComponent(query)}&limit=${chatSearchLimit}`, {",
		// validation in code points, with the server's 200 ceiling mirrored
		"const length = [...query].length",
		"if (length < chatSearchMinChars) {",
		"if (length > chatSearchMaxChars) {",
		"const chatSearchMaxChars = 200",
		"}, chatSearchDebounceMs)",
		// conversations (local name matches) above messages (server hits)
		"function chatSearchConversationMatches(query)",
		"nodes.push(chatSearchGroupHead('conversations'))",
		"nodes.push(chatSearchGroupHead('messages'))",
		"const row = chatThreadRowNode(entry.thread, '')",
		"function chatSearchEntries()",
		// length-preserving case fold keeps <mark> offsets aligned
		"function chatSearchFoldCase(text)",
		"folded += lower.length === 1 ? lower : unit",
		"const lower = chatSearchFoldCase(text)",
		// the section lists hide while results show; the field stays
		`#chatTool[data-chat-search="results"] .chat-threads > :not(.chat-threads__search):not(#chatSearchResults) {`,
		"const next = mode === true ? 'results' : mode === 'results' || mode === 'unavailable' ? mode : ''",
		"if (next) chatToolSection.dataset.chatSearch = next",
		// rows: glyph · title · author + relative time (mono) · snippet with <mark>
		"function chatSearchGlyphNode(result)",
		"bfEl('span', 'chat-search-result__meta', when ? `${author} · ${when}` : author)",
		"mark.className = 'chat-search-result__mark'",
		"const mark = document.createElement('mark')",
		// empty state
		"bfEl('p', 'chat-search-results__empty', 'no messages match')",
		// opening pages back through history, capped, then rings the message
		"const chatSearchHistoryPageCap = 5",
		"async function ensureScoutChatMessageLoaded(threadId, messageId)",
		"await loadEarlierScoutChatMessages(id)",
		"showToast({ text: 'message is older than the loaded history', kind: 'note' })",
		"node.classList.add('is-search-hit')",
		"if (activeScoutThreadId !== threadId) selectScoutChatThread(threadId)",
		// wiring: input filters + searches, Escape restores, Enter opens
		"function filterChatThreads() {\n\t        renderChatAgentThreads()\n\t        // W1 D8: 2+ characters also ask the server for matching messages\n\t        syncChatSearch()",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing message-search marker %q", want)
		}
	}
	// the highlight is an ember well and nothing else
	markStart := strings.Index(html, ".chat-search-result__mark {")
	if markStart < 0 {
		t.Fatal("missing .chat-search-result__mark rule")
	}
	markBlock := html[markStart:]
	markBlock = markBlock[:strings.Index(markBlock, "}")]
	for _, want := range []string{
		"background: color-mix(in srgb, var(--ember) 10%, transparent);",
		"color: inherit;",
		"text-decoration: none;",
	} {
		if !strings.Contains(markBlock, want) {
			t.Errorf("search mark rule missing %q", want)
		}
	}
	for _, forbidden := range []string{"border:", "box-shadow", "font-weight", "underline"} {
		if strings.Contains(markBlock, forbidden) {
			t.Errorf("search mark rule must not decorate beyond the ember well (found %q)", forbidden)
		}
	}
	// Escape clears the query and restores the normal list
	keydown := strings.Index(html, "chatThreadSearch?.addEventListener('keydown', event => {")
	if keydown < 0 {
		t.Fatal("search field keydown handler missing")
	}
	escape := html[keydown : keydown+1400]
	for _, want := range []string{
		"if (event.key === 'Escape') {",
		"chatThreadSearch.value = ''",
		"clearChatSearch()",
		"renderChatAgentThreads()",
		"if (event.key === 'Enter') {",
		"const entries = chatSearchEntries()",
		"void openChatSearchEntry(entries[chatSearchActiveIndex] || entries[0])",
	} {
		if !strings.Contains(escape, want) {
			t.Errorf("search field keydown handler missing %q", want)
		}
	}
	// snippets are DOM-built, never innerHTML over server text
	snippet := wave1FunctionBody(html, "function appendChatSearchSnippet(target, snippet, query)")
	if snippet == "" || strings.Contains(snippet, "innerHTML") {
		t.Error("appendChatSearchSnippet must build the snippet from text nodes and <mark> elements")
	}
}

// D7 in the composer: once the participants directory is loaded it is the
// mention roster verbatim (Scout first, viewer excluded, server order); the
// hardcoded room roster is only the loading fallback and never offers the
// viewer either. The highlight pass keeps every name, viewer included.
func TestIndexConversationsWave1MentionRosterIsTheDirectory(t *testing.T) {
	html := readIndexForConversationsWave1(t)
	roster := wave1FunctionBody(html, "function mentionRosterCandidates()")
	if roster == "" {
		t.Fatal("could not isolate mentionRosterCandidates")
	}
	for _, want := range []string{
		"const source = mentionDirectoryLoaded() ? mentionDirectoryCandidates() : mentionRosterFallback()",
		"return source.filter(candidate => !mentionViewerMatches(candidate))",
	} {
		if !strings.Contains(roster, want) {
			t.Errorf("mentionRosterCandidates missing %q", want)
		}
	}
	for _, gone := range []string{"participantNames()", "byName.set(name.toLowerCase(), candidate)", "[...participantNames(), 'Scout']"} {
		if strings.Contains(roster, gone) {
			t.Errorf("mentionRosterCandidates must not prepend or overlay the hardcoded roster once the directory is loaded (found %q)", gone)
		}
	}
	fallback := wave1FunctionBody(html, "function mentionRosterFallback()")
	if !strings.Contains(fallback, "['Scout', ...participantNames()]") {
		t.Error("the loading fallback must list Scout first")
	}
	viewer := wave1FunctionBody(html, "function mentionViewerMatches(candidate)")
	if !strings.Contains(viewer, "return email === viewerEmail") {
		t.Error("the viewer must be excluded from the composer roster by email")
	}
	names := wave1FunctionBody(html, "function mentionRosterNames()")
	if !strings.Contains(names, "[...mentionDirectoryCandidates(), ...mentionRosterFallback()]") {
		t.Error("mentionRosterNames must keep every name (viewer included) for the highlight pass")
	}
}

// Recall-pass fixes: removed-member payloads, the read-marker flush on a fast
// thread switch, the members-only popover close, and the TDZ hygiene that
// hoists the participants directory ahead of every reader.
func TestIndexConversationsWave1RecallPassContracts(t *testing.T) {
	html := readIndexForConversationsWave1(t)
	for _, want := range []string{
		"if (payload.removed === true) {\n\t\t  dropChatThreadForViewer(id)",
		"if (chatReadMarkerPending && chatReadMarkerPending.threadId !== threadId) {",
		"void flushChatThreadReadMarker()",
		"chatConvoMembersController?.close()",
		"let chatConvoMembersController = null",
		"let chatConvoNotifyController = null",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing recall-pass marker %q", want)
		}
	}
	// hiding the members control must not close the bell menu
	members := wave1FunctionBody(html, "function renderChatConvoMembers()")
	if strings.Contains(members, "closeChatHeaderPopovers()") {
		t.Error("renderChatConvoMembers must close only the members popover")
	}
	// the directory is declared ahead of every reader; no TDZ guards remain
	decl := strings.Index(html, "const desktopChatParticipantDirectory = new Map()")
	firstReader := strings.Index(html, "function mentionDirectoryLoaded()")
	if decl < 0 || firstReader < 0 || decl > firstReader {
		t.Error("desktopChatParticipantDirectory must be declared before its first reader")
	}
	if strings.Count(html, "const desktopChatParticipantDirectory = new Map()") != 1 ||
		strings.Count(html, "let desktopChatMentionCandidates = []") != 1 ||
		strings.Count(html, "let desktopChatParticipantDirectoryLoaded = false") != 1 {
		t.Error("the directory state must be declared exactly once")
	}
	for _, signature := range []string{
		"function mentionDirectoryLoaded()",
		"function mentionDirectoryCandidates()",
		"function chatParticipantDirectoryEntry(email)",
		"function chatMemberPickerCandidates(query, excluded)",
	} {
		if strings.Contains(wave1FunctionBody(html, signature), "try {") {
			t.Errorf("%s must read the directory directly, without a TDZ guard", signature)
		}
	}
	if strings.Contains(html, "try { void ensureDesktopChatParticipantDirectory() }") {
		t.Error("ensureDesktopChatParticipantDirectory no longer needs a TDZ wrapper")
	}
}

// Rule 3: must-run work never rides requestAnimationFrame (it does not fire
// in a hidden pane) — popover focus and the read-marker check are macrotasks.
func TestIndexConversationsWave1MustRunWorkAvoidsRAF(t *testing.T) {
	html := readIndexForConversationsWave1(t)
	for _, signature := range []string{
		"function chatHeaderPopoverController(wrap, trigger, popover, options = {})",
		"function noteScoutChatTailInView()",
	} {
		body := wave1FunctionBody(html, signature)
		if body == "" {
			t.Fatalf("could not isolate %s", signature)
		}
		if strings.Contains(body, "requestAnimationFrame") {
			t.Errorf("%s must not schedule must-run work on requestAnimationFrame", signature)
		}
		if !strings.Contains(body, "window.setTimeout(() => {") {
			t.Errorf("%s must schedule its must-run work with window.setTimeout", signature)
		}
	}
}

// A failed server search says so instead of silently keeping the local filter.
func TestIndexConversationsWave1SearchUnavailableIsHonest(t *testing.T) {
	html := readIndexForConversationsWave1(t)
	for _, want := range []string{
		"const chatSearchUnavailableCopy = 'search unavailable · showing titles only'",
		"const chatSearchValidationCopy = 'query too long · showing titles only'",
		"function showChatSearchNotice(copy)",
		"function showChatSearchUnavailable()",
		"bfEl('p', 'chat-search-results__empty chat-search-results__notice', copy)",
		"setChatSearchMode('unavailable')",
		`#chatTool[data-chat-search="unavailable"] .chat-search-results {`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing search-unavailable marker %q", want)
		}
	}
	run := wave1FunctionBody(html, "async function runChatSearch(query)")
	// a 400 is validation (the quiet too-long line); anything else is unavailable
	if !strings.Contains(run, "if (response.status === 400) showChatSearchNotice(chatSearchValidationCopy)") ||
		strings.Count(run, "showChatSearchUnavailable()") < 2 {
		t.Error("runChatSearch must treat 400 as validation and surface unavailable on other failures and exceptions")
	}
	sync := wave1FunctionBody(html, "function syncChatSearch()")
	if !strings.Contains(sync, "showChatSearchNotice(chatSearchValidationCopy)") {
		t.Error("an over-long query must show the validation line without a request")
	}
	if strings.Contains(run, "setChatSearchMode(false)") {
		t.Error("runChatSearch must not silently drop back to the local filter")
	}
	// the notice must not hide the local lists: only the results mode does
	if !strings.Contains(html, `#chatTool[data-chat-search="results"] .chat-threads > :not(.chat-threads__search):not(#chatSearchResults) {`) ||
		strings.Contains(html, `#chatTool[data-chat-search="unavailable"] .chat-threads > :not(`) {
		t.Error("only the results mode may hide the section lists")
	}
}

func TestIndexConversationsWave1PopoversFollowTheGlassRecipe(t *testing.T) {
	html := readIndexForConversationsWave1(t)
	for _, selector := range []string{".chat-convo-popover {", ".chat-member-picker__list {"} {
		start := strings.Index(html, selector)
		if start < 0 {
			t.Fatalf("missing popover rule %q", selector)
		}
		block := html[start:]
		block = block[:strings.Index(block, "}")]
		for _, want := range []string{
			"color-mix(in oklab, var(--surface-1) 94%, transparent)",
			"backdrop-filter: var(--glass-blur-chrome) saturate(1.25)",
			"transition-property: opacity, transform, display;",
			"var(--ease-spring)",
			"transition-behavior: allow-discrete;",
		} {
			if !strings.Contains(block, want) {
				t.Errorf("%s missing glass recipe line %q", selector, want)
			}
		}
		if strings.Contains(block, "--ember") {
			t.Errorf("%s decorates with ember; new chrome must stay on surface/text tokens", selector)
		}
	}
	// every new popover closes on Escape and outside pointerdown
	controller := wave1FunctionBody(html, "function chatHeaderPopoverController(wrap, trigger, popover, options = {})")
	for _, want := range []string{
		"if (event.key !== 'Escape') return",
		"document.addEventListener('pointerdown', onOutsidePointerDown, true)",
		"document.addEventListener('keydown', onKeydown, true)",
	} {
		if !strings.Contains(controller, want) {
			t.Errorf("header popover controller missing %q", want)
		}
	}
}
