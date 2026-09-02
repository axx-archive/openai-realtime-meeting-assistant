package main

import (
	"os"
	"strings"
	"testing"
)

// Wave 8 (Memory that compounds) frontend pins: the memory inspector ("What
// Scout knows"), its close / correct / forget actions, the "Remember this"
// message action, the recall-coverage chip beside Scout's source chips, the
// work-result door into Work, and the composer's Drive contextRefs on send.

func readIndexForMemoryWave8(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestFrontendMemoryInspectorSurfaceAndRoutes(t *testing.T) {
	html := readIndexForMemoryWave8(t)
	for _, want := range []string{
		// the tool keeps its pinned section line; the inspector leads, the
		// meeting timeline stays as the second tab
		`<section id="memoryTool" class="memory-tool" aria-label="Meetings">`,
		`data-memory-view="inspector">What Scout knows</button>`,
		`data-memory-view="timeline" tabindex="-1">Meetings</button>`,
		`id="memoryInspectorPanel" class="memory-inspector" role="tabpanel"`,
		`id="memoryTimelinePanel" class="memory-timeline" role="tabpanel"`,
		`id="memorySearch"`,
		`id="memoryList" class="memory-list" role="log"`,
		// filter row: subject text, person menu, since presets, kind chips
		`id="memoryInspectorSubject"`,
		`id="memoryInspectorPerson" class="memory-inspector__filter" type="button" aria-haspopup="menu"`,
		`id="memoryInspectorSince" class="memory-inspector__filter" type="button" aria-haspopup="menu"`,
		`id="memoryInspectorKinds" class="memory-inspector__kinds" role="group"`,
		`id="memoryInspectorList" class="memory-inspector__list" role="list" aria-label="What Scout knows"`,
		`id="memoryInspectorProfile" class="memory-profile"`,
		// the details drawer is sheet-tier glass
		`id="memoryInspectorDrawer" class="memory-drawer glass-sheet" role="dialog"`,
		// routes
		"fetch(`/assistant/memory/inspect${memoryInspectorQuery()}`, { cache: 'no-store' })",
		`postAuthJSON('/assistant/memory/inspect/action', { id: String(item?.id || ''), action, ...(correction ? { correction } : {}) })`,
		`params.set('subject', memoryInspectorFilter.subject)`,
		`params.set('person', memoryInspectorFilter.person)`,
		`params.set('since', since.toISOString())`,
		`params.set('kinds', [...memoryInspectorFilter.kinds].join(','))`,
		// the three verbs, and forget behind a confirm on own notes only
		`run('close')`,
		`run('correct', correction)`,
		`run('forget')`,
		`forget: kind === 'note' && Boolean(item?.own) && status !== 'forgotten'`,
		`close: ['ledger', 'decision'].includes(kind) && !settled`,
		`Forget this note? It leaves recall for everyone; the fact that it existed stays on record.`,
		// the viewer's own profile card with Correct
		`'About you · what Scout has distilled'`,
		`'Correct what Scout knows about you'`,
		// honest empty states
		`'Scout has nothing on this yet.'`,
		`'memory is unavailable right now'`,
		// kind glyphs come from the one icon system
		`user_profile: { label: 'about you', icon: 'user' }`,
		`work_result: { label: 'work result', icon: 'work' }`,
		`strideIcon(meta.icon, { size: 16 })`,
		// provenance doors: meeting record, source conversation, Work stage
		`() => void jumpToMemoryMeeting(id)`,
		`openMemoryConversationLink(id, messageId)`,
		`openMemoryWorkResult(item, id)`,
		// D9: a work result opens on the Work hub's artifact stage
		`replace(/^ldg-work_result-/, '')`,
		`selectPD1Destination('Work')`,
		`void openArtifactStage(artifactId, String(item?.title || 'deliverable'))`,
		// the tool front page is the inspector; meeting jumps land on the timeline
		`setMemoryView(memoryEntryView())`,
		`if (path === '/meetings' || path.startsWith('/meetings/')) return 'timeline'`,
		`setMemoryView('timeline')`,
		// a 'memory' socket event refreshes the open inspector
		`scheduleMemoryInspectorRefresh()`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("memory inspector missing %q", want)
		}
	}
	for _, css := range []string{
		`.memory-drawer {`,
		`.memory-drawer[hidden] {`,
		`.memory-status[data-status="closed"],`,
		`.memory-kind-chip[aria-pressed="true"] {`,
		`.memory-item__head {`,
		`.memory-provenance {`,
	} {
		if !strings.Contains(html, css) {
			t.Errorf("memory inspector CSS missing %q", css)
		}
	}
	if strings.Contains(functionBody(html, "function renderMemoryInspectorDrawer(item, options = {})"), "--accent") {
		t.Error("the inspector drawer must not spend ember")
	}
}

func TestFrontendRememberThisMessageAction(t *testing.T) {
	html := readIndexForMemoryWave8(t)
	for _, want := range []string{
		// the more-menu grows the item in both the feed and the reply rail
		`const { onShareAll, onShareReply, onRemember } = arguments[0] || {}`,
		`const remember = bfEl('button', '', 'Remember this')`,
		`onRemember: canRemember ? trigger => rememberChatMessage(activeThread, message, trigger) : null,`,
		`onRemember: canRemember ? trigger => rememberChatMessage(thread, message, trigger) : null,`,
		`if (desktopChatMessageIsOwn(activeThread, message) || generatedImage || canRemember) {`,
		`if (isOwn || hasShare || canRemember) {`,
		// the explicit write: threadId + messageId on POST /assistant/remember
		`postAuthJSON('/assistant/remember', { ...body, private: privateNote })`,
		`threadId: String(thread?.id || ''),`,
		`messageId: String(message?.id || '')`,
		// private threads choose the audience before anything is written
		`{ id: 'company', label: 'Remember for the company', hint: 'teammates can recall it'`,
		`{ id: 'private', label: 'Remember privately', hint: 'only you can recall it'`,
		`'Saved to company memory · private to you'`,
		`'Saved to company memory'`,
		`action: { label: 'Open in memory', onSelect: () => void openMemoryInspectorItem(String(saved.id || '')) }`,
		// composer shortcut
		`const rememberShortcut = /^\/remember\s+([\s\S]+)$/i.exec(trimmed)`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("remember action missing %q", want)
		}
	}
	// the pinned more-menu signature and the pinned send path stay verbatim
	for _, pinned := range []string{
		"function desktopChatMoreMenuControl({ label = 'More actions', onEdit, onProject, onDelete, onRegenerate } = {})",
		"async function sendScoutChatViaOffice(text, files = [])",
	} {
		if !strings.Contains(html, pinned) {
			t.Errorf("pinned signature changed: %q", pinned)
		}
	}
	// editing never opens on someone else's message just because it is rememberable
	if !strings.Contains(html, "onEdit: generatedImage || !desktopChatMessageIsOwn(activeThread, message) ? null : () => beginDesktopFeedMessageEdit(activeThread, message, node),") {
		t.Error("feed more-menu must keep edit own-only when the menu opens for Remember this")
	}
}

func TestFrontendScoutAnswerCoverageChip(t *testing.T) {
	html := readIndexForMemoryWave8(t)
	for _, want := range []string{
		`function scoutAnswerCoverageChip(message)`,
		`const copy = { complete: 'memory complete', partial: 'partial memory', unavailable: 'memory unavailable' }[coverage]`,
		`const chip = bfEl('span', 'desktop-chat-coverage', copy)`,
		`chip.dataset.coverage = coverage`,
		// rendered beside the source chips in the feed and the reply rail
		`if (sources.length || coverageChip) {`,
		`if (coverageChip) sourceRow.appendChild(coverageChip)`,
		// mono machine fact; partial warns, unavailable alarms, complete stays quiet
		`#chatTool .desktop-chat-coverage {`,
		`#chatTool .desktop-chat-coverage[data-coverage="partial"] { color: var(--warn-text); }`,
		`#chatTool .desktop-chat-coverage[data-coverage="unavailable"] { color: var(--danger-text); }`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("coverage chip missing %q", want)
		}
	}
	chip := functionBody(html, "function scoutAnswerCoverageChip(message)")
	if chip == "" || !strings.Contains(chip, "if (!copy) return null") {
		t.Error("coverage chip must be hidden when the server did not stamp coverage")
	}
	rule := html[strings.Index(html, "#chatTool .desktop-chat-coverage {"):]
	rule = rule[:strings.Index(rule, "}")]
	for _, want := range []string{"color: var(--text-3);", "font: var(--type-label);"} {
		if !strings.Contains(rule, want) {
			t.Errorf("coverage chip base rule missing %q", want)
		}
	}
}

func TestFrontendComposerAttachFromDriveContextRefs(t *testing.T) {
	html := readIndexForMemoryWave8(t)
	for _, want := range []string{
		`id="scoutChatContextRefs" class="scout-chat-context-refs" role="group"`,
		`{ id: 'drive-work', label: 'Attach from Drive', hint: 'as context for a work request', onSelect: () => void attachDriveRefsForWork() }`,
		`const refs = await pickDriveFilesForWork({ threadId: String(activeScoutThreadId || ''), multiple: true, title: 'Attach from Drive' })`,
		`window.composeWorkRequestWithDriveRefs = refs => {`,
		`.scout-chat-context-ref__remove`,
		`remove.setAttribute('aria-label', ` + "`Remove ${label} from the request`)",
		// the send carries contextRefs and hands the chips back on failure
		`const contextRefs = pendingScoutContextRefs.splice(0, pendingScoutContextRefs.length)`,
		`if (contextRefs.length) messagePayload.contextRefs = contextRefs.map(entry => ({ ref: entry.ref, sourceId: entry.sourceId || '', sourceRevision: entry.sourceRevision || '' }))`,
		`pendingScoutContextRefs = contextRefs.slice()`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("composer contextRefs missing %q", want)
		}
	}
	office := functionBody(html, "async function sendScoutChatViaOffice(text, files = [])")
	if office == "" || !strings.Contains(office, "messagePayload.contextRefs") || !strings.Contains(office, "const requestBody = JSON.stringify(messagePayload)") {
		t.Error("sendScoutChatViaOffice must serialize contextRefs into the request body")
	}
	send := functionBody(html, "function sendScoutChat(text)")
	if send == "" || !strings.Contains(send, "sendScoutChatViaOffice(trimmed, files)") {
		t.Error("sendScoutChat must keep forwarding text and files verbatim")
	}
}

// Drive refs are picked for one conversation and never carry across a thread
// switch (reviewer finding): selectScoutChatThread clears them behind the
// same guard that resets the other per-thread composer state, and the helper
// both empties the array and repaints the chip row.
func TestIndexMemoryWave8ContextRefsClearOnThreadSwitch(t *testing.T) {
	html := readIndexForMemoryWave8(t)
	selectBody := functionBody(html, "function selectScoutChatThread(id)")
	if selectBody == "" {
		t.Fatal("could not extract selectScoutChatThread body")
	}
	guard := strings.Index(selectBody, "if (nextThreadId !== activeScoutThreadId) {")
	call := strings.Index(selectBody, "clearPendingScoutContextRefsForThreadSwitch()")
	if guard == -1 || call == -1 || call < guard {
		t.Fatalf("selectScoutChatThread must clear pending Drive refs inside the thread-changed guard (guard=%d call=%d)", guard, call)
	}
	helper := functionBody(html, "function clearPendingScoutContextRefsForThreadSwitch()")
	if helper == "" {
		t.Fatal("could not extract clearPendingScoutContextRefsForThreadSwitch body")
	}
	for _, want := range []string{"pendingScoutContextRefs = []", "renderPendingScoutContextRefs()"} {
		if !strings.Contains(helper, want) {
			t.Errorf("clearPendingScoutContextRefsForThreadSwitch missing %q", want)
		}
	}
}
