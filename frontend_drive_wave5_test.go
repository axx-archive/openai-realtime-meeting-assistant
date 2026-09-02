package main

// Wave 5 Drive (w5-frontend) — static pins on index.html for the access
// control, file share links, the previewer's keyboard + Esc contract, the
// versions chain, star / trash / empty-trash confirm, the debounced server
// search, the usage bar, and the "All files" Home head. Grep-style pins in the
// frontend_files_surface_test.go idiom: the id, the class, and the one line of
// markup or script that proves the seam exists.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForDriveWave5(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func driveWave5Scope(t *testing.T, html, start, end string) string {
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

func requireDriveWave5(t *testing.T, label, hay string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(hay, want) {
			t.Fatalf("%s missing %q", label, want)
		}
	}
}

// D1 / D2 — the details pane's Access section: a radiogroup of Only me /
// Company / People, uploader-only editing (canShare), grants add / remove
// through PATCH /assistant/files, and the upload door's visibility choice
// (default company) riding the multipart form.
func TestIndexDriveWave5AccessControl(t *testing.T) {
	html := readIndexForDriveWave5(t)
	requireDriveWave5(t, "access control", html,
		`id="filesUploadVisibility"`,
		`let filesUploadVisibility = 'company'`,
		`body.append('visibility', filesUploadVisibility)`,
		`function renderDriveAccessSection(file)`,
		`seg.setAttribute('role', 'radiogroup')`,
		`[['private', 'Only me'], ['company', 'Company'], ['people', 'People']]`,
		`const editable = file?.canShare === true`,
		`button.disabled = !editable`,
		`grants: { add: [email] }`,
		`grants: { remove: [email] }`,
		`createChatMemberPicker(root, {`,
		`if (driveFileHasAccess(file)) sections.push(renderDriveAccessSection(file))`,
	)
	access := driveWave5Scope(t, html, "function updateDriveFileAccess(file, patch)", "function driveVisibilityCopy(visibility)")
	requireDriveWave5(t, "updateDriveFileAccess", access, "method: 'PATCH'", "body: JSON.stringify({ id: String(file?.id || ''), ...patch })")
}

// D3 — file share links: mint through POST /artifacts/share {fileId,
// expiresDays} with a 7 / 30 / 90 day choice, list by ?fileId=, revoke by id.
func TestIndexDriveWave5ShareLinks(t *testing.T) {
	html := readIndexForDriveWave5(t)
	requireDriveWave5(t, "share links", html,
		`function renderDriveShareSection(file)`,
		"fetch(`/artifacts/share?fileId=${encodeURIComponent(id)}`, { cache: 'no-store' })",
		`body: JSON.stringify({ fileId: String(file?.id || ''), expiresDays: Number(expiresDays) || 7 })`,
		`async function revokeDriveFileShareLink(file, link)`,
		`[7, 30, 90].map(days => ({`,
		`bfEl('button', 'drive-detail__action', 'Copy link')`,
		`menuItem('Copy link'`,
		`if (driveFileCanShareLink(file)) sections.push(renderDriveShareSection(file))`,
	)
	revoke := driveWave5Scope(t, html, "async function revokeDriveFileShareLink(file, link)", "async function copyDriveFileLink(file)")
	requireDriveWave5(t, "revokeDriveFileShareLink", revoke, "method: 'DELETE'", "body: JSON.stringify({ id: String(link?.id || '') })")
}

// D4 — the previewer: a fullscreen glass sheet that enters with opacity only,
// walks the rendered list on ← / →, closes on Esc (the dialog cancel), and
// returns focus to the control that opened it. Decks / docs keep their doors.
func TestIndexDriveWave5PreviewerKeyboardAndEsc(t *testing.T) {
	html := readIndexForDriveWave5(t)
	previewer := driveWave5Scope(t, html, "function ensureDrivePreviewer()", "function openDriveFilePreview(file, opener)")
	requireDriveWave5(t, "previewer", previewer,
		`dialog.className = 'drive-previewer glass-sheet'`,
		`if (event.key === 'ArrowLeft') { event.preventDefault(); step(-1) }`,
		`else if (event.key === 'ArrowRight') { event.preventDefault(); step(1) }`,
		`dialog.addEventListener('cancel', event => { event.preventDefault(); closePreviewer() })`,
		`opener.focus({ preventScroll: true })`,
		`node = document.createElement('embed')`,
		`node.type = 'application/pdf'`,
		`node = document.createElement('video')`,
		`node.controls = true`,
	)
	requireDriveWave5(t, "previewer wiring", html,
		`function wireDriveNameLink(link, file)`,
		`if (file?.origin === 'deliverable' && file?.artifactId) return ''`,
		`@starting-style { .drive-previewer[open] { opacity: 0; transform: scale(0.98); } }`,
	)
	sheet := driveWave5Scope(t, html, "\n      .drive-previewer {", "}")
	// plan 013: hover/keystroke on --dur-fast; token residue — the previewer
	// arrives with a small composited scale alongside its fade (was opacity-only).
	requireDriveWave5(t, ".drive-previewer", sheet, "position: fixed", "transition: opacity var(--dur-med) var(--ease), transform var(--dur-med) var(--ease)")
}

// D5 — versions: ?versionsOf= newest first as mono rows, superseded rows
// hidden from the live list, an "N versions" chip on the newest row.
func TestIndexDriveWave5Versions(t *testing.T) {
	html := readIndexForDriveWave5(t)
	requireDriveWave5(t, "versions", html,
		"fetch(`/assistant/files?versionsOf=${encodeURIComponent(id)}`, { cache: 'no-store' })",
		`function renderDriveVersionsSection(file)`,
		`.sort((a, b) => Number(b?.version || 0) - Number(a?.version || 0))`,
		"bfEl('span', 'files-row__versions', `${version} versions`)",
		`return filesEntries.filter(row => row?.id && !row?.superseded && !row?.deletedAt)`,
	)
	row := driveWave5Scope(t, html, "\n      .drive-versions__row {", "}")
	requireDriveWave5(t, ".drive-versions__row", row, "var(--font-mono)", "tabular-nums")
}

// D6 — star + trash: PATCH {id, starred}, the Starred and Trash scopes,
// ?scope=trash, Restore per row, and Empty trash behind the glass-sheet
// confirm (never window.confirm).
func TestIndexDriveWave5StarTrashAndEmptyTrashConfirm(t *testing.T) {
	html := readIndexForDriveWave5(t)
	requireDriveWave5(t, "star + trash", html,
		`data-drive-scope="starred"`,
		`data-drive-scope="trash"`,
		`id="filesEmptyTrashButton"`,
		`body: JSON.stringify({ id: fileId, starred: next })`,
		`fetch('/assistant/files?scope=trash', { cache: 'no-store' })`,
		`fetch('/assistant/files/restore', {`,
		`function fileRestoreButtonNode(file)`,
		`menuItem('Restore', () => restoreDriveFile(file))`,
		`'Moved to trash — restore it from Trash within 30 days'`,
		`dialog.className = 'drive-save-dialog drive-confirm-dialog glass-sheet'`,
	)
	empty := driveWave5Scope(t, html, "async function emptyDriveTrash()", "function driveConfirmDialog(options = {})")
	requireDriveWave5(t, "emptyDriveTrash", empty,
		`const confirmed = await driveConfirmDialog({`,
		`fetch('/assistant/files/trash/empty', { method: 'POST' })`,
	)
	if strings.Contains(empty, "window.confirm") {
		t.Fatal("emptyDriveTrash must confirm through the glass sheet, not window.confirm")
	}
	if strings.Index(empty, "driveConfirmDialog") > strings.Index(empty, "trash/empty") {
		t.Fatal("emptyDriveTrash must confirm before it purges")
	}
	del := driveWave5Scope(t, html, "async function deleteDriveFile(file)", "function fileIsStudioText(file)")
	if strings.Contains(del, "window.confirm") {
		t.Fatal("deleteDriveFile must not use window.confirm")
	}
	// trashed rows never render in another scope: the live list filters deletedAt
	live := driveWave5Scope(t, html, "function driveLiveRows()", "function driveFolderDescendantIds(folderId)")
	requireDriveWave5(t, "driveLiveRows", live, "!row?.deletedAt", "filesScope === 'trash'")
}

// D7 — server search: 2+ characters debounce 250 ms into ?q=, under 2 the
// local filter runs alone; an "in content" tag marks a body-only match.
func TestIndexDriveWave5ServerSearchDebounce(t *testing.T) {
	html := readIndexForDriveWave5(t)
	requireDriveWave5(t, "server search", html,
		`filesSearchTimer = window.setTimeout(() => runDriveServerSearch(query), 250)`,
		`if (query.length < 2) {`,
		"fetch(`/assistant/files?q=${encodeURIComponent(query)}${scopeParam}`, { cache: 'no-store' })",
		`const serverMode = query.length >= 2 && Array.isArray(filesServerResults) && filesServerQuery === query`,
		`bfEl('span', 'files-row__tag', 'in content')`,
	)
}

// D9 — usage bar (mono label, transform-driven fill), the 413 quota copy,
// the "All files" Home head (never "Suggested for you"), Recent = last
// opened, parent-folder roll-up, and the scope copy strings.
func TestIndexDriveWave5UsageAndViewFixes(t *testing.T) {
	html := readIndexForDriveWave5(t)
	requireDriveWave5(t, "usage + view fixes", html,
		`id="filesUsage"`,
		`fetch('/assistant/files/usage', { cache: 'no-store' })`,
		"`${driveStorageLabel(used)} of ${driveStorageLabel(quota)}`",
		"fill.style.transform = `scaleX(${ratio.toFixed(4)})`",
		"Drive is full — ${driveStorageLabel(payload.bytesUsed)} of ${driveStorageLabel(payload.quotaBytes)} used.",
		`(filesScope === 'home' ? 'All files' : scopeTitle)`,
		`function driveNoteFileOpened(file)`,
		"`stride.drive.recent.${viewer}`",
		`return driveFileLastOpened(file) || String(file?.createdAt || '')`,
		`function driveFolderDescendantIds(folderId)`,
		`inScope.filter(file => folderIds.has(String(file?.folderId || '')))`,
		`trash: ['Trash', 'Your trashed uploads. Restore within 30 days, or empty the trash to free space now.']`,
		`starred: ['Starred', 'Files you starred, in one place.']`,
	)
	if strings.Contains(html, "Suggested for you") {
		t.Fatal("the Drive Home head must read \"All files\", not \"Suggested for you\"")
	}
	label := driveWave5Scope(t, html, "\n      .drive-usage__label {", "}")
	requireDriveWave5(t, ".drive-usage__label", label, "var(--font-mono)")
	fill := driveWave5Scope(t, html, "\n      .drive-usage__fill {", "}")
	requireDriveWave5(t, ".drive-usage__fill", fill, "transform: scaleX(0)", "transition: transform")
	if strings.Contains(fill, "width:") {
		t.Fatal(".drive-usage__fill must move by transform, not width")
	}
}

// D8 — the work-request picker: pickDriveFilesForWork resolves to
// {ref:'file|<id>', sourceId, sourceRevision} refs, resolving the exact
// source through /assistant/attachments/from-file when a thread is given;
// the details pane offers "Use in a request".
func TestIndexDriveWave5WorkRequestPicker(t *testing.T) {
	html := readIndexForDriveWave5(t)
	requireDriveWave5(t, "work picker", html,
		`async function pickDriveFilesForWork(options = {})`,
		"ref: `file|${String(file?.id || '')}`",
		`sourceId: String(attachment?.sourceId || '')`,
		`sourceRevision: String(attachment?.sourceRevision || '')`,
		`function useDriveFileInRequest(file)`,
		`bfEl('button', 'drive-detail__action', 'Use in a request')`,
	)
	resolve := driveWave5Scope(t, html, "async function resolveDriveWorkRefs(files, threadId)", "async function pickDriveFilesForWork(options = {})")
	requireDriveWave5(t, "resolveDriveWorkRefs", resolve, "fetch('/assistant/attachments/from-file', {")
}

// Design canon: nothing in the Wave 5 Drive CSS reaches for ember or the
// live green — file-family tints stay danger / warn / info.
func TestIndexDriveWave5NoEmberNoGreen(t *testing.T) {
	html := readIndexForDriveWave5(t)
	css := driveWave5Scope(t, html, "Wave 5 Drive: access, share links, previewer, versions,", "/* canon Mission Intelligence")
	for _, banned := range []string{"--ember", "--agent", "#FF5A19", "#ff5a19", "--live", "--accent"} {
		if strings.Contains(css, banned) {
			t.Fatalf("Wave 5 Drive CSS must not use %q", banned)
		}
	}
	for _, want := range []string{"var(--danger", "var(--warn)", "var(--text-3)", "var(--font-mono)"} {
		if !strings.Contains(css, want) {
			t.Fatalf("Wave 5 Drive CSS missing %q", want)
		}
	}
}
