package main

// Work hub + Studios fix pins (2026-09-02 QA findings). Static reads of
// index.html, so they hold with no browser in the loop:
//
//   W1 an identity switch resets every Work-hub binding (window, ETag,
//      selection, cursor, deep-link marker), clears the DOM and fences a page
//      still in flight for the previous account;
//   W2 a kind chip drops the cached window's Load older cursor, and a forced
//      re-read that lands while a page is in flight is queued, not dropped;
//   W3 Document Studio print only touches the editor DOM inside studioPrint's
//      before callback and reports a refused print instead of mutating first;
//   W4 an earlier version never reaches the presenter unless the version ref
//      itself carries canPresent === true; otherwise it opens read-only;
//   W6 a checkpoint decision in flight is held in a module-level map the card
//      renderer consults, and settle re-queries the live cards;
//   W8 the Work list and detail containers carry no aria-live; one status
//      line updates its text in place.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForWorkHubFixes(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func workHubFixSection(t *testing.T, html, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(html, startMarker)
	if start < 0 {
		t.Fatalf("index.html missing section start %q", startMarker)
	}
	end := strings.Index(html[start:], endMarker)
	if end < 0 {
		t.Fatalf("index.html missing section end %q after %q", endMarker, startMarker)
	}
	return html[start : start+end]
}

func requireWorkHubFixMarkers(t *testing.T, label, body string, wants []string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("%s missing %q", label, want)
		}
	}
}

// requireWorkHubFixOrder asserts each marker appears, and after the previous one.
func requireWorkHubFixOrder(t *testing.T, label, body string, ordered []string) {
	t.Helper()
	cursor := 0
	for _, want := range ordered {
		at := strings.Index(body[cursor:], want)
		if at < 0 {
			t.Errorf("%s missing %q (in order after offset %d)", label, want, cursor)
			return
		}
		cursor += at + len(want)
	}
}

// W1: user B must never see user A's Work rows — in memory or in the DOM.
func TestIndexWorkHubIdentitySwitchResetsProjects(t *testing.T) {
	html := readIndexForWorkHubFixes(t)
	reset := workHubFixSection(t, html, "function resetStudioProjectsState() {", "async function loadStudioProjects(options = {}) {")
	requireWorkHubFixMarkers(t, "resetStudioProjectsState", reset, []string{
		"studioProjectsLoadEpoch += 1",
		"studioProjects = []",
		"studioProjectsETag = ''",
		"selectedStudioProjectId = ''",
		"studioProjectsNextBefore = ''",
		"studioProjectsHasMore = false",
		"studioProjectDeepLinkUnavailable = ''",
		"studioProjectsError = ''",
		"studioProjectsLoading = false",
		"studioProjectsAppending = false",
		"studioProjectsAppendQueued = false",
		"studioProjectsReloadQueued = false",
		"studioProjectCheckpointSubmitting?.clear()",
		"studioProjectList?.replaceChildren()",
		"studioProjectDetail?.replaceChildren()",
		"if (appShell.dataset.tool === 'research') renderStudioProjects()",
	})
	auth := workHubFixSection(t, html, "function setAuthenticatedUser(nextUser) {", "let roomRecordingUpdatedAt = ''")
	// the reset sits on the identity-changed side of the early return, next to
	// the rail count it already cleared, and the mounted Work tool re-reads
	requireWorkHubFixOrder(t, "setAuthenticatedUser", auth, []string{
		"if (!identityChanged) {",
		"return",
		"studioProjectsRunningCount = 0",
		"studioProjectsBadgeETag = ''",
		"resetStudioProjectsState()",
		"if (appShell.dataset.tool === 'research') void loadStudioProjects({ force: true })",
	})
	load := workHubFixSection(t, html, "async function loadStudioProjects(options = {}) {", "/* ---------- Wave 3: Work hub truth ----------")
	// a page fenced by the epoch touches neither the window nor the flags
	requireWorkHubFixMarkers(t, "loadStudioProjects epoch fence", load, []string{
		"const epoch = studioProjectsLoadEpoch",
		"if (epoch !== studioProjectsLoadEpoch) return",
		"if (epoch === studioProjectsLoadEpoch) studioProjectsError = error?.message || 'Work could not be loaded'", // Wave 11 D1
		"if (epoch === studioProjectsLoadEpoch) {",
	})
	if strings.Count(load, "if (epoch !== studioProjectsLoadEpoch) return") < 3 {
		t.Error("every await in loadStudioProjects (list, payload, exact card) must re-check the load epoch")
	}
	badge := workHubFixSection(t, html, "async function refreshWorkRunningCount(options = {}) {", "function scheduleWorkHubRefresh() {")
	requireWorkHubFixMarkers(t, "refreshWorkRunningCount", badge, []string{
		"const epoch = studioProjectsLoadEpoch",
		"if (epoch !== studioProjectsLoadEpoch || response.status === 304) return",
	})
	for _, want := range []string{"let studioProjectsLoadEpoch = 0", "let studioProjectsReloadQueued = false", "let studioProjectsReloadWaiters = []"} {
		if !strings.Contains(html, want) {
			t.Errorf("Work-hub state missing %q", want)
		}
	}
}

// W2: a kind change must never let Load older send the previous kind's cursor,
// and its forced re-read must survive a page already in flight.
func TestIndexWorkHubKindChangeResetsPaginationAndQueuesReload(t *testing.T) {
	html := readIndexForWorkHubFixes(t)
	chip := workHubFixSection(t, html, "function setStudioProjectKindFilter(kind) {", "function renderStudioProjectKindChips() {")
	requireWorkHubFixOrder(t, "setStudioProjectKindFilter", chip, []string{
		"studioProjectFilter = next",
		"studioProjectsETag = ''",
		"studioProjectsHasMore = false",
		"studioProjectsNextBefore = ''",
		"studioProjectsAppendQueued = false",
		"renderStudioProjects()",
		"void loadStudioProjects({ force: true })",
	})
	load := workHubFixSection(t, html, "async function loadStudioProjects(options = {}) {", "/* ---------- Wave 3: Work hub truth ----------")
	requireWorkHubFixOrder(t, "loadStudioProjects force queue", load, []string{
		"if (!append && (studioProjectsLoading || studioProjectsAppending)) {",
		"if (!options.force) return",
		"studioProjectsReloadQueued = true",
		"return new Promise(resolve => studioProjectsReloadWaiters.push(resolve))",
	})
	requireWorkHubFixMarkers(t, "loadStudioProjects cursor scope", load, []string{
		"const requestedKind = studioProjectKindSpec(studioProjectFilter) ? studioProjectFilter : ''",
		"const kindStillCurrent = requestedKind === (studioProjectKindSpec(studioProjectFilter) ? studioProjectFilter : '')",
		"studioProjectsETag = kindStillCurrent ? (response.headers.get('ETag') || '') : ''",
		"studioProjectsHasMore = kindStillCurrent && payload.hasMore === true",
		"studioProjectsNextBefore = studioProjectsHasMore ? String(payload.nextBefore || '') : ''",
		// the kind-scoped request itself is unchanged
		"if (studioProjectKindSpec(studioProjectFilter)) params.set('kind', studioProjectFilter)",
	})
	// the queued re-read drains after the in-flight page settles and wins over
	// a queued append (whose cursor belongs to the window just replaced)
	requireWorkHubFixOrder(t, "loadStudioProjects drain", load, []string{
		"} finally {",
		"if (studioProjectsReloadQueued) {",
		"studioProjectsReloadQueued = false",
		"studioProjectsAppendQueued = false",
		"loadStudioProjects({ force: true }).finally(() => waiters.forEach(resolve => resolve()))",
		"} else if (!append && studioProjectsAppendQueued) {",
		"void loadStudioProjects({ append: true })",
	})
}

// W3: print must not mutate the editor before studioPrint accepts the job.
func TestIndexStudioDocumentPrintMutatesInsideBeforeCallback(t *testing.T) {
	html := readIndexForWorkHubFixes(t)
	print := workHubFixSection(t, html, "function printDocumentStudio() {", "async function exportDocumentPDF(button) {")
	requireWorkHubFixOrder(t, "printDocumentStudio", print, []string{
		"const printed = state.sourceMode",
		"? studioPrint('document', {",
		"before: () => {",
		"markdownIntoDocumentEditor(rich, source.value)",
		"rich.hidden = false",
		"source.hidden = true",
		"after: () => { if (state.sourceMode) { rich.hidden = true; source.hidden = false } }",
		": studioPrint('document')",
		"if (!printed) showToast({ text: 'Print is still open', kind: 'note' })",
	})
	// nothing about the editor changes above the studioPrint call
	head := print[:strings.Index(print, "studioPrint('document'")]
	for _, forbidden := range []string{"markdownIntoDocumentEditor(", "rich.hidden", "source.hidden"} {
		if strings.Contains(head, forbidden) {
			t.Errorf("printDocumentStudio mutates the editor (%q) before studioPrint can refuse", forbidden)
		}
	}
	studioPrint := workHubFixSection(t, html, "function studioPrint(kind, { before, after } = {}) {", "function triggerStudioBlobDownload(")
	requireWorkHubFixOrder(t, "studioPrint", studioPrint, []string{
		"if (document.body.dataset.studioPrint) return false",
		"try { before?.() } catch (_) { finish(); return false }",
		"return true",
	})
}

// W4: an earlier version opens read-only; the presenter is reachable only when
// the version ref itself says it may present.
func TestIndexWorkHubVersionRowsOpenReadOnlyNeverPresent(t *testing.T) {
	html := readIndexForWorkHubFixes(t)
	open := workHubFixSection(t, html, "async function openStudioProjectVersion(project, version, button) {", "async function startStudioProjectWithScout() {")
	requireWorkHubFixOrder(t, "openStudioProjectVersion", open, []string{
		"if (version.canPresent === true) {",
		"return openDeckPresentation(result.artifactId, title, {",
		"qualityState: 'admitted', canPresent: true",
		"openStudioVersionStage({",
		"kind: 'deck'",
		"row: { version: number, at: String(version.at || ''), editedBy: String(version.source || '') }",
		"body: paintDeckVersionSheet(deck)",
		"return true",
		// non-deck bodies keep the Wave 3 read-only snapshot stage
		"canEdit: false, hideEdit: true,",
		"snapshot: { version: number, at: String(version.at || ''), source: String(version.source || ''), text }",
	})
	if strings.Count(open, "openDeckPresentation(") != 1 {
		t.Errorf("openStudioProjectVersion must reach the presenter only inside the canPresent branch, got %d calls", strings.Count(open, "openDeckPresentation("))
	}
	// the sheet painter is module-level and shares the presenter's element painter
	for _, want := range []string{
		"function paintDeckVersionSheet(documentModel) {",
		"function paintDeckPresenterElement(element, documentModel) {",
		"function deckPresenterSlideBackground(slide, documentModel) {",
		"const paint = element => paintDeckPresenterElement(element, documentModel)",
		"box.style.setProperty('--deck-background', deckPresenterSlideBackground(slide, documentModel))",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("version sheet painter missing %q", want)
		}
	}
	sheet := workHubFixSection(t, html, "function paintDeckVersionSheet(documentModel) {", "var activeDeckPresenter = null")
	for _, forbidden := range []string{"data-present-action", "openDeckPresentation", "activeDeckPresenter"} {
		if strings.Contains(sheet, forbidden) {
			t.Errorf("paintDeckVersionSheet must be a view, not the presenter (%q)", forbidden)
		}
	}
	// the presenter gate that the version row used to skip is still there
	presenter := workHubFixSection(t, html, "async function openDeckPresentation(artifactId, title, initialPayload = null) {", "function closeDeckPresentation() {")
	requireWorkHubFixMarkers(t, "presenter gate", presenter, []string{
		"Object.prototype.hasOwnProperty.call(payload || {}, 'canPresent') && payload?.canPresent !== true",
		"showToast({ text: 'This draft needs review before it can be presented.', kind: 'note' })",
	})
}

// W6: one submission per checkpoint, however many times the card is rebuilt.
func TestIndexWorkHubCheckpointSubmitOnce(t *testing.T) {
	html := readIndexForWorkHubFixes(t)
	if !strings.Contains(html, "var studioProjectCheckpointSubmitting = new Map()") {
		t.Fatal("studioProjectCheckpointSubmitting must be a module-level, boot-safe map")
	}
	resolve := workHubFixSection(t, html, "async function resolveStudioProjectCheckpoint(project, checkpoint, option, button, checkpointNote = '') {", "async function continueStudioProjectResult(project, button) {")
	requireWorkHubFixOrder(t, "resolveStudioProjectCheckpoint", resolve, []string{
		"if (!checkpointId || studioProjectCheckpointSubmitting.has(checkpointId)) return",
		"studioProjectCheckpointSubmitting.set(checkpointId, { optionId: String(option?.id || '') })",
		"cards.forEach(node => setStudioProjectCheckpointCardBusy(node, true))",
		"if (button) button.textContent = 'Submitting…'",
		"await submitCheckpointOption(project.rootArtifactId, checkpoint.id, option.id, checkpointNote)",
		"await loadStudioProjects({ force: true })",
		"} finally {",
		"studioProjectCheckpointSubmitting.delete(checkpointId)",
		"studioProjectCheckpointCards(checkpointId).forEach(node => setStudioProjectCheckpointCardBusy(node, false))",
	})
	// the error path no longer re-enables the node captured at click time
	if strings.Contains(resolve, "controls.forEach(control => { control.disabled = false })") {
		t.Error("resolveStudioProjectCheckpoint must re-query the live cards on settle, not re-enable the captured controls")
	}
	live := workHubFixSection(t, html, "function studioProjectCheckpointCards(checkpointId) {", "function setStudioProjectCheckpointCardBusy(card, busy) {")
	requireWorkHubFixMarkers(t, "studioProjectCheckpointCards", live, []string{
		"document.querySelectorAll('.studio-project-decision')",
		"card.dataset.checkpointId === checkpointId",
	})
	card := workHubFixSection(t, html, "function studioProjectDecisionNode(project, checkpoint) {", "function renderStudioProjectDetail(project) {")
	requireWorkHubFixOrder(t, "studioProjectDecisionNode", card, []string{
		"card.dataset.checkpointId = String(checkpoint?.id || '')",
		"const submitting = studioProjectCheckpointSubmitting?.get(String(checkpoint?.id || '')) || null",
		"if (submitting) card.setAttribute('aria-busy', 'true')",
		"button.dataset.idleLabel = option.label",
		"if (submitting) {",
		"button.disabled = true",
		"if (submitting.optionId === String(option.id || '')) button.textContent = 'Submitting…'",
		"send.dataset.idleLabel = 'Send changes'",
	})
}

// W8: no aria-live on containers that are rebuilt every render.
func TestIndexWorkHubSingleStatusLiveRegion(t *testing.T) {
	html := readIndexForWorkHubFixes(t)
	requireWorkHubFixOrder(t, "Work library markup", html, []string{
		`<div id="studioProjectKinds" class="studio-projects__kinds" role="group" aria-label="Filter work by kind"></div>`,
		`<p id="studioProjectsStatus" class="studio-projects__status" role="status"></p>`,
		`<div id="studioProjectList" class="studio-projects__list"></div>`,
		`<section id="studioProjectDetail" class="studio-projects__detail"></section>`,
	})
	for _, forbidden := range []string{
		`id="studioProjectList" class="studio-projects__list" aria-live`,
		`id="studioProjectDetail" class="studio-projects__detail" aria-live`,
	} {
		if strings.Contains(html, forbidden) {
			t.Errorf("rebuilt Work container must not be a live region: %q", forbidden)
		}
	}
	if !strings.Contains(html, "const studioProjectsStatus = document.getElementById('studioProjectsStatus')") {
		t.Error("studioProjectsStatus must be resolved once with the other Work-hub nodes")
	}
	render := workHubFixSection(t, html, "function renderStudioProjects() {", "document.addEventListener('visibilitychange', () => {")
	requireWorkHubFixOrder(t, "renderStudioProjects", render, []string{
		"const status = studioProjectSubtitle()",
		"if (studioProjectsStatus.textContent !== status) studioProjectsStatus.textContent = status",
	})
	css := workHubFixSection(t, html, ".studio-projects__status {", ".studio-projects__list {")
	requireWorkHubFixMarkers(t, "status line CSS", css, []string{"clip: rect(0 0 0 0);", "clip-path: inset(50%);", "white-space: nowrap;"})
}
