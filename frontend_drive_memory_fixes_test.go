package main

// Drive + Memory verified-finding fixes (V1–V8) — grep-style pins on
// index.html in the frontend_drive_wave5_test.go idiom: the symbol, the one
// line that proves the seam, and the ordering wherever ordering is the fix.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForDriveMemoryFixes(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func driveMemoryFixScope(t *testing.T, html, start, end string) string {
	t.Helper()
	a := strings.Index(html, start)
	if a < 0 {
		t.Fatalf("index.html missing scope start %q", start)
	}
	b := strings.Index(html[a:], end)
	if b < 0 {
		t.Fatalf("index.html missing scope end %q after %q", end, start)
	}
	return html[a : a+b]
}

func requireDriveMemoryFix(t *testing.T, label, hay string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(hay, want) {
			t.Errorf("%s missing %q", label, want)
		}
	}
}

func forbidDriveMemoryFix(t *testing.T, label, hay string, nots ...string) {
	t.Helper()
	for _, not := range nots {
		if strings.Contains(hay, not) {
			t.Errorf("%s must not contain %q", label, not)
		}
	}
}

// orderedDriveMemoryFix asserts each needle appears, in order, each after the
// previous one.
func orderedDriveMemoryFix(t *testing.T, label, hay string, needles ...string) {
	t.Helper()
	at := 0
	for _, needle := range needles {
		i := strings.Index(hay[at:], needle)
		if i < 0 {
			t.Fatalf("%s: %q must come after the previous needle (not found from offset %d)", label, needle, at)
		}
		at += i + len(needle)
	}
}

// V1 — the local pre-filter mirrors the server's name-side fields exactly
// (name, uploaderName, uploaderEmail); originThreadTitle is not one of them,
// so a thread-title hit can neither hide the "in content" tag nor pre-show a
// row the server then drops.
func TestIndexDriveFixLocalSearchMirrorsServer(t *testing.T) {
	html := readIndexForDriveMemoryFixes(t)
	body := driveMemoryFixScope(t, html, "function driveSearchMatchesLocally(file, query)", "function renderFilesSurface()")
	requireDriveMemoryFix(t, "driveSearchMatchesLocally", body,
		"String(file?.name || '').toLowerCase().includes(query)",
		"String(file?.uploaderName || '').toLowerCase().includes(query)",
		"String(file?.uploaderEmail || '').toLowerCase().includes(query)",
	)
	forbidDriveMemoryFix(t, "driveSearchMatchesLocally", body, "originThreadTitle")
	raw, err := os.ReadFile("files.go")
	if err != nil {
		t.Fatalf("read files.go: %v", err)
	}
	requireDriveMemoryFix(t, "files.go name-side search", string(raw),
		"strings.Contains(strings.ToLower(row.Name), needle)",
		"strings.Contains(strings.ToLower(row.UploaderName), needle)",
		"strings.Contains(strings.ToLower(row.UploaderEmail), needle)",
	)
}

// V2 — a failed send hands the Drive refs back by MERGING: originals first,
// refs attached while the send was in flight behind them, deduped by ref id,
// under the 8-ref cap, then the chips repaint.
func TestIndexMemoryFixContextRefsMergeOnFailedSend(t *testing.T) {
	html := readIndexForDriveMemoryFixes(t)
	office := driveMemoryFixScope(t, html, "async function sendScoutChatViaOffice(text, files = [])", "function scoutOfficeChatHistoryPayload()")
	restore := driveMemoryFixScope(t, office, "// a refused Drive ref (403) or a lost send hands the chips back", "scheduleScoutChatProjectContextRefresh({ preserveAttempt: true })")
	orderedDriveMemoryFix(t, "contextRefs restore", restore,
		"const attachedSince = pendingScoutContextRefs.filter(entry => !contextRefs.some(original => String(original?.ref || '') === String(entry?.ref || '')))",
		"pendingScoutContextRefs = contextRefs.slice()",
		"if (pendingScoutContextRefs.length >= 8) break",
		"pendingScoutContextRefs.push(entry)",
		"renderPendingScoutContextRefs()",
	)
}

// V3 — the memory drawer: focus returns to the same action (by data-action)
// or the first focusable after a repaint; Esc rides document while the drawer
// is open; a socket refresh only marks the drawer updated (never repaints
// under the reader); the role=status line is mounted once and re-texted.
func TestIndexMemoryFixDrawerFocusEscAndLiveStatus(t *testing.T) {
	html := readIndexForDriveMemoryFixes(t)
	requireDriveMemoryFix(t, "drawer helpers", html,
		"let memoryInspectorDrawerStatusLine = null",
		"let memoryInspectorDrawerRenderedKey = ''",
		"function memoryInspectorDrawerStatusNode()",
		"function setMemoryInspectorDrawerStatus(text, tone = '')",
		"function markMemoryInspectorDrawerUpdated(item)",
		"function focusMemoryInspectorDrawerAction(action)",
		"function onMemoryDrawerDocumentEscape(event)",
		".memory-drawer__updated {",
	)
	open := driveMemoryFixScope(t, html, "function openMemoryInspectorDrawer(id, options = {})", "function memoryInspectorActionsFor(item)")
	requireDriveMemoryFix(t, "openMemoryInspectorDrawer", open, "document.addEventListener('keydown', onMemoryDrawerDocumentEscape)")
	closeBody := driveMemoryFixScope(t, html, "function closeMemoryInspectorDrawer({ restoreFocus = false } = {})", "function openMemoryInspectorDrawer(id, options = {})")
	requireDriveMemoryFix(t, "closeMemoryInspectorDrawer", closeBody,
		"document.removeEventListener('keydown', onMemoryDrawerDocumentEscape)",
		"setMemoryInspectorDrawerStatus('')",
		"memoryInspectorList?.querySelector('.memory-item__head')",
	)
	render := driveMemoryFixScope(t, html, "function renderMemoryInspectorDrawer(item, options = {})", "async function runMemoryInspectorAction(item, action, correction)")
	requireDriveMemoryFix(t, "renderMemoryInspectorDrawer", render,
		"const statusLine = memoryInspectorDrawerStatusNode()",
		"const focusedAction = document.activeElement instanceof Element ? String(document.activeElement.closest('[data-memory-action]')?.dataset.memoryAction || '') : ''",
		"focus: focusAfter",
		"memoryInspectorDrawer.insertBefore(body, statusLine)",
		"memoryInspectorDrawer.replaceChildren(head, body, statusLine, foot)",
		"memoryInspectorDrawerRenderedKey = memoryInspectorItemKey(item)",
		"if (options.focus) focusMemoryInspectorDrawerAction(options.focus)",
		"closeMemoryInspectorDrawer({ restoreFocus: true })",
	)
	forbidDriveMemoryFix(t, "renderMemoryInspectorDrawer", render,
		"bfEl('p', 'memory-drawer__status'",
		"replaceChildren(head, body, foot)",
		"restoreFocus: false",
	)
	load := driveMemoryFixScope(t, html, "async function loadMemoryInspector(options = {})", "function scheduleMemoryInspectorRefresh()")
	requireDriveMemoryFix(t, "loadMemoryInspector", load, "else markMemoryInspectorDrawerUpdated(selected)")
	forbidDriveMemoryFix(t, "loadMemoryInspector", load, "renderMemoryInspectorDrawer(")
	// the status line is a direct child of the drawer, so the sheet lays out
	// as a flex column (the old three-row grid would have shoved the foot)
	sheet := driveMemoryFixScope(t, html, "\n      .memory-drawer {", "}")
	requireDriveMemoryFix(t, ".memory-drawer", sheet, "display: flex", "flex-direction: column")
	forbidDriveMemoryFix(t, ".memory-drawer", sheet, "grid-template-rows")
}

// V4 — ← / → aimed at <video controls> / <audio controls> stay with the
// media; the dialog only walks the list for other targets.
func TestIndexDriveFixPreviewerArrowsSkipMedia(t *testing.T) {
	html := readIndexForDriveMemoryFixes(t)
	previewer := driveMemoryFixScope(t, html, "function ensureDrivePreviewer()", "function openDriveFilePreview(file, opener)")
	orderedDriveMemoryFix(t, "previewer keydown", previewer,
		"if ((event.key === 'ArrowLeft' || event.key === 'ArrowRight') && event.target instanceof Element && event.target.closest('video, audio')) return",
		"if (event.key === 'ArrowLeft') { event.preventDefault(); step(-1) }",
		"else if (event.key === 'ArrowRight') { event.preventDefault(); step(1) }",
	)
}

// V5 — "Copy link": reuse a live link whose url is known, otherwise mint —
// then copy; a clipboard write that fails after the await selects the file
// and reveals the link selected in the panel with a synchronous Copy.
func TestIndexDriveFixShareLinkMintThenCopy(t *testing.T) {
	html := readIndexForDriveMemoryFixes(t)
	requireDriveMemoryFix(t, "share link state", html,
		"let filesShareRevealLinkId = ''",
		"function driveLiveShareLink(links)",
		".drive-share__link.is-selected {",
	)
	mint := driveMemoryFixScope(t, html, "async function mintDriveFileShareLink(file, expiresDays = filesShareExpiryDays)", "async function revokeDriveFileShareLink(file, link)")
	orderedDriveMemoryFix(t, "mintDriveFileShareLink", mint,
		"link = driveLiveShareLink(await loadDriveFileShareLinks(file, true))",
		"if (!link) {",
		"method: 'POST'",
		"body: JSON.stringify({ fileId: String(file?.id || ''), expiresDays: Number(expiresDays) || 7 })",
		"await navigator.clipboard.writeText(shareLinkAbsoluteUrl(link))",
		"filesShareRevealLinkId = String(link.id || '')",
		"filesSelectedId = fileId",
	)
	forbidDriveMemoryFix(t, "mintDriveFileShareLink", mint, "await copyShareLinkUrl(")
	section := driveMemoryFixScope(t, html, "function renderDriveShareSection(file)", "async function loadDriveFileVersions(file, force = false)")
	requireDriveMemoryFix(t, "renderDriveShareSection", section,
		"copy.addEventListener('click', () => copyShareLinkUrl(link))",
		"if (filesShareRevealLinkId && filesShareRevealLinkId === String(link.id || '')) {",
		"row.classList.add('is-selected')",
		"range.selectNodeContents(label)",
		"copy.focus({ preventScroll: true })",
	)
	kebab := driveMemoryFixScope(t, html, "async function copyDriveFileLink(file)", "function driveShareExpiryTrigger(onChange)")
	requireDriveMemoryFix(t, "copyDriveFileLink", kebab, "if (filesSelectedId === String(file?.id || '')) renderFilesSurface()")
}

// V6 — the search count is one persistent role=status node in the markup,
// re-texted in place; renderFilesSurface never creates it.
func TestIndexDriveFixSearchCountLiveRegion(t *testing.T) {
	html := readIndexForDriveMemoryFixes(t)
	orderedDriveMemoryFix(t, "files surface markup", html,
		`<p id="filesSearchState" class="files-search-state" role="status" aria-live="polite"></p>`,
		`<div id="filesList" class="memory-list" role="log" aria-label="Drive files">`,
	)
	requireDriveMemoryFix(t, "files search state", html,
		"const filesSearchState = document.getElementById('filesSearchState')",
		".files-search-state:empty { margin: 0; }",
	)
	render := driveMemoryFixScope(t, html, "function renderFilesSurface()", "function driveUploadFailureCopy(file, status, payload)")
	requireDriveMemoryFix(t, "renderFilesSurface", render, "filesSearchState.textContent = searchState")
	forbidDriveMemoryFix(t, "renderFilesSurface", render, "bfEl('p', 'files-search-state'", "stateLine")
}

// V7 — restore refetches the live list right away (like delete), and a scope
// switch refetches whenever the list is marked stale (filesLoadedAt === 0).
func TestIndexDriveFixRestoreRefetches(t *testing.T) {
	html := readIndexForDriveMemoryFixes(t)
	restore := driveMemoryFixScope(t, html, "async function restoreDriveFile(file)", "async function emptyDriveTrash()")
	orderedDriveMemoryFix(t, "restoreDriveFile", restore,
		"filesLoadedAt = 0",
		"renderFilesSurface()",
		"loadFilesSurface(true)",
	)
	scope := driveMemoryFixScope(t, html, "filesScopeButtons.forEach(button => button.addEventListener('click', () => {", "filesEmptyTrashButton?.addEventListener('click'")
	requireDriveMemoryFix(t, "scope buttons", scope, "if (filesLoadedAt === 0) loadFilesSurface()")
}

// V8 — "/remember <text>" keeps the draft until the POST succeeds (composer
// disabled, "remembering…"), leaves it with the toast on failure, and never
// clears the file chips (a note is words only; the toast says they stayed).
func TestIndexMemoryFixRememberKeepsDraftUntilSaved(t *testing.T) {
	html := readIndexForDriveMemoryFixes(t)
	send := driveMemoryFixScope(t, html, "function sendScoutChat(text) {", "async function sendScoutChatViaOffice(text, files = [])")
	orderedDriveMemoryFix(t, "sendScoutChat remember branch", send,
		"const rememberShortcut = /^\\/remember\\s+([\\s\\S]+)$/i.exec(trimmed)",
		"void rememberComposerNote(rememberShortcut[1])",
		"return false",
	)
	branch := driveMemoryFixScope(t, send, "void rememberComposerNote(rememberShortcut[1])", "}")
	forbidDriveMemoryFix(t, "sendScoutChat remember branch", branch, "return true")
	remember := driveMemoryFixScope(t, html, "async function rememberComposerNote(text)", "function scoutAnswerCoverageChip(message)")
	orderedDriveMemoryFix(t, "rememberComposerNote", remember,
		"const draft = scoutChatInput ? scoutChatInput.value : ''",
		"scoutChatInput.disabled = true",
		"showToast({ text: 'remembering…', kind: 'note' })",
		"await postRememberNote({ text: trimmed }, { keptFiles: pendingScoutFiles.length > 0 })",
		"scoutChatInput.disabled = false",
		"if (scoutChatInput.value === draft) scoutChatInput.value = ''",
		"scoutChatInput.value = draft",
	)
	forbidDriveMemoryFix(t, "rememberComposerNote", remember, "pendingScoutFiles = []", "renderPendingScoutFiles()")
	requireDriveMemoryFix(t, "postRememberNote", html,
		"async function postRememberNote(body, { privateNote = false, keptFiles = false } = {})",
		"const filesNote = keptFiles ? ' · files stay attached for your next message' : ''",
	)
}

// M1 / M2 — the two Drive bfMenus are fixed (body-appended, placed off the
// trigger rect): the expiry trigger is built detached, so an in-flow menu
// inserted 'afterend' at build time never reached the DOM (and the expiry
// could never change); the upload-visibility button has no positioned
// ancestor before .workspace, so its in-flow menu landed a workspace-height
// below the trigger. The expiry menu is rebuilt per details render, so the
// previous instance is destroyed first.
func TestIndexDriveFixMenusAreFixed(t *testing.T) {
	html := readIndexForDriveMemoryFixes(t)
	requireDriveMemoryFix(t, "expiry menu state", html, "let driveShareExpiryMenu = null")
	expiry := driveMemoryFixScope(t, html, "function driveShareExpiryTrigger(onChange)", "function renderDriveShareSection(file)")
	orderedDriveMemoryFix(t, "driveShareExpiryTrigger", expiry,
		"driveShareExpiryMenu?.destroy?.()",
		"driveShareExpiryMenu = bfMenu(trigger, {",
		"fixed: true,",
		"[7, 30, 90].map(days => ({",
	)
	visibility := driveMemoryFixScope(t, html, "function driveUploadVisibilityInit()", "function driveNavIconsInit()")
	orderedDriveMemoryFix(t, "driveUploadVisibilityInit", visibility,
		"bfMenu(filesUploadVisibilityButton, {",
		"fixed: true,",
		"label: 'Who can see new uploads',",
	)
}
