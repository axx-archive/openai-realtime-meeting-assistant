package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Wave 11 — Packaging Studio. Founder intent (2026-09-02): "we want the work tab
// to be renamed Packaging Studio and in there you can start three types of
// work"; commissions take structured briefs; clarifying questions arrive as
// threaded replies; every deliverable can be renamed / duplicated / saved /
// exported and tagged with a project that auto-files it into Drive. These pins
// hold the frontend half: the rename, the three studios, the brief sheets, the
// deliverable verbs, the project picker, the inline clarifying questions, the
// retired starter pills, and the canon (no edge bars, ember only on live).

func readIndexForPackagingWave11(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// packagingWave11Function is functionBody for signatures that carry a default
// object parameter (`options = {}`): the brace search starts after the
// signature, not inside it.
func packagingWave11Function(html, signature string) string {
	start := strings.Index(html, signature)
	if start < 0 {
		return ""
	}
	return functionBody(html[start+len(signature)-1:], "{")
}

func packagingWave11Section(t *testing.T, html, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(html, startMarker)
	if start < 0 {
		t.Fatalf("section start %q missing", startMarker)
	}
	end := strings.Index(html[start:], endMarker)
	if end < 0 {
		t.Fatalf("section end %q missing after %q", endMarker, startMarker)
	}
	return html[start : start+end]
}

func TestIndexPackagingStudioRenameEverywhere(t *testing.T) {
	html := readIndexForPackagingWave11(t)
	for _, want := range []string{
		// nav: the accessible name and the visible word; the destination id stays "Work"
		`data-pd1-destination="Work" aria-label="Packaging Studio" aria-current="false" tabindex="-1" aria-describedby="workRailBadge">`,
		"<span>Packaging Studio</span>",
		// topbar title
		"research: 'Packaging Studio',",
		// hub heading — Wave 11 D15 moved the visible title into the topbar
		// (tab name + mono subline); the <h2> stays for assistive tech only
		`<header class="studio-projects__head sr-only">`,
		`<h2 id="studioProjectsTitle">Packaging Studio</h2>`,
		"|| ['Packaging Studio', 'Presentations and research stay organized from brief to final file.']",
		// states
		"'Packaging Studio is temporarily unavailable'",
		"'Packaging Studio could not be loaded'",
		"'Nothing in the studio yet'",
		"'View in Packaging Studio'",
		// the phone dock holds the two-line word
		`.pd1-primary-nav__item[data-pd1-destination="Work"] > span:not(.pd1-primary-nav__count) { white-space: normal; line-height: 1.05; text-align: center; }`,
		// ⌘/⌥ tooltips derive from the aria-label, so they follow the rename
		"button.title = `${label} · ${pd1ShortcutModifier}${index + 1}`",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("Packaging Studio rename missing %q", want)
		}
	}
	// ids and routes are not renamed
	for _, keep := range []string{
		`const PD1_DESTINATIONS = Object.freeze(['Home', 'Video', 'Conversations', 'Work', 'Drive'])`,
		`id="researchTool" class="agent-tool" data-agent-tool="research"`,
		`'/research': { destination: 'Work', output: 'document' }`,
	} {
		if !strings.Contains(html, keep) {
			t.Errorf("rename must not touch ids/routes; missing %q", keep)
		}
	}
	// no user-facing "Work" label survives in the nav or the hub chrome
	nav := packagingWave11Section(t, html, `<nav id="pd1PrimaryNav"`, `</nav>`)
	if strings.Contains(nav, `aria-label="Work"`) || strings.Contains(nav, ">Work<") {
		t.Error("the primary nav still says Work")
	}
	if strings.Contains(html, "<h2 id=\"studioProjectsTitle\">Recent work</h2>") {
		t.Error("the hub heading still says Recent work")
	}
}

func TestIndexPackagingStudioThreeStudios(t *testing.T) {
	html := readIndexForPackagingWave11(t)
	hub := packagingWave11Section(t, html, `<section id="researchTool"`, `<div id="studioProjectsWorkspace"`)
	for _, want := range []string{
		`<section class="packaging-studios" aria-labelledby="studioAppsTitle">`,
		`<h3 id="studioAppsTitle" class="packaging-studios__title">Studios</h3>`,
		`data-packaging-studio="research"`,
		`data-packaging-studio="presentation"`,
		`data-packaging-studio="story"`,
		`<span class="packaging-studio__name">Research Desk</span>`,
		`<span class="packaging-studio__name">Presentation Studio</span>`,
		`<span class="packaging-studio__name">Story Studio</span>`,
		// mono kicker + title + one line, brief + start empty per studio
		`<span class="packaging-studio__kicker">research</span>`,
		`data-studio-app-kind="research" data-studio-app-action="brief"`,
		`data-studio-app-kind="presentation" data-studio-app-action="brief"`,
		`data-studio-app-kind="story" data-studio-app-action="brief"`,
		`data-studio-app-kind="document" data-studio-app-action="new">Start empty</button>`,
		`data-studio-app-kind="presentation" data-studio-app-action="new">Start empty</button>`,
		`data-studio-app-kind="story" data-studio-app-action="new">Start empty</button>`,
	} {
		if !strings.Contains(hub, want) {
			t.Errorf("studios row missing %q", want)
		}
	}
	// documents are chat-harness work, not a studio tile; the old launcher is gone
	for _, gone := range []string{`data-studio-app="document"`, `class="studio-apps__grid"`, `Installed apps`, `id="studioNewWithScout"`} {
		if strings.Contains(hub, gone) {
			t.Errorf("the Wave 3 launcher must be gone from the hub; found %q", gone)
		}
	}
	// no gradient cards: quiet rows over hairlines (the theme swatch inside the
	// brief sheet is the one gradient in the block, and it is a swatch)
	css := packagingWave11Section(t, html, "/* ---------- Wave 11: Packaging Studio ----------\n         Three quiet studio rows", "/* the row: a button (the row) beside its kebab")
	if strings.Contains(css, "gradient(") {
		t.Error("studio rows are quiet rows, not gradient cards")
	}
	for _, want := range []string{
		".packaging-studio__kicker {\n        color: var(--ink-3);\n        font: 500 11px/1.2 var(--font-mono);",
		".packaging-studio {\n        display: grid;",
		"border-bottom: 1px solid var(--line);",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("studio row CSS missing %q", want)
		}
	}
	handler := functionBody(html, "document.querySelectorAll('[data-studio-app-action]').forEach(button => {")
	for _, want := range []string{"if (action === 'brief') {", "void openPackagingBriefSheet(studioKind)", "if (kind === 'story') void createBlankStoryOutline()", "createBlankStudioProject(kind === 'document' ? 'document' : 'presentation')"} {
		if !strings.Contains(handler, want) {
			t.Errorf("studio launcher handler missing %q", want)
		}
	}
}

func TestIndexPackagingBriefSheets(t *testing.T) {
	html := readIndexForPackagingWave11(t)
	sheet := packagingWave11Function(html, "async function openPackagingBriefSheet(kind, options = {}) {")
	if sheet == "" {
		t.Fatal("openPackagingBriefSheet missing")
	}
	for _, want := range []string{
		"dialog.className = 'packaging-brief glass-sheet'",
		"dialog.showModal()",
		// research desk: pills + open question + sources + "just describe it"
		"packagingPillGroup('Scope', ['company', 'market', 'competitor', 'technical', 'people'], 'market')",
		// the brief shapes are the server's structs verbatim
		"return { question, scope: fields.scope.value, depth: fields.depth.value, format: fields.format.value, audience, sources: packagingSourceList(fields.sources.value) }",
		"lookFeel: { themeId: fields.theme.value, notes: fields.lookNotes.value }",
		"research = { commissionFirst: true, brief: { question, scope: 'market', depth: 'standard', format: 'report', audience } }",
		"packagingPillGroup('Depth', ['brief', 'standard', 'deep'], 'standard')",
		"packagingPillGroup('Format', ['report', 'one-pager', 'memo'], 'report')",
		"packagingPillGroup('Audience', ['leadership', 'team', 'board', 'investors', 'customers']",
		"fields.sources = packagingSourcesField({ threadId: options.threadId })",
		"const modeDescribe = bfEl('button', 'packaging-brief__mode-option', 'Just describe it')",
		// presentation studio: subject, audience, copy style, look & feel, imagery, research, story outline, length
		"packagingPillGroup('Copy style', ['crisp', 'narrative', 'data-led', 'persuasive'], 'crisp')",
		"fields.theme = packagingThemeField(await packagingThemeCatalog(), '')",
		"fields.imagery = packagingImageryField('hybrid')",
		"{ id: 'attach', label: 'attach existing' }, { id: 'commission', label: 'commission first' }",
		"fields.story = packagingSelectField('Story outline'",
		"packagingPillGroup('Length', [{ id: '8', label: '8 slides' }, { id: '12', label: '12 slides' }, { id: '16', label: '16 slides' }, { id: '24', label: '24 slides' }], '12')",
		// story studio: subject, audience, thesis, length → the outline thread
		"fields.thesis = packagingTextField('Thesis'",
		"return { subject, audience, thesis: fields.thesis.value, length: fields.length.value }",
		"packagingPillGroup('Length', ['short', 'standard', 'long'], 'standard')",
		"if (studioKind === 'story') await createStoryOutline(brief, options)",
		"else await submitPackagingCommission(studioKind, brief, options)",
		// scoped to a thread (D14 pre-scoping)
		"if (options.threadId) head.appendChild(bfEl('p', 'packaging-brief__scope', 'scoped to this conversation · Scout replies here'))",
	} {
		if !strings.Contains(sheet, want) {
			t.Errorf("brief sheet missing %q", want)
		}
	}
	sources := packagingWave11Function(html, "function packagingSourcesField(options = {}) {")
	if !strings.Contains(sources, "pickDriveFilesForWork({ threadId: String(options.threadId || ''), multiple: true, title: 'Attach sources from Drive' })") {
		t.Error("research sources attach through pickDriveFilesForWork")
	}
	imagery := functionBody(html, "function packagingImageryField(initial = 'hybrid') {")
	for _, want := range []string{"{ id: 'full-bleed', label: 'Full-bleed'", "{ id: 'on-slide', label: 'On-slide'", "{ id: 'hybrid', label: 'Hybrid'", "packagingImageryThumb(mode.id)"} {
		if !strings.Contains(imagery, want) {
			t.Errorf("imagery mode field missing %q", want)
		}
	}
	submit := packagingWave11Function(html, "async function submitPackagingCommission(kind, brief, options = {}) {")
	for _, want := range []string{
		"fetch('/assistant/packaging/commissions', {",
		"const body = { kind, brief, operationId: packagingOperationId('commission') }",
		"if (options.threadId) body.threadId = String(options.threadId)",
		// honest when the route has not landed
		"if (packagingRouteMissing(response)) throw new Error('Commissions are not available on this server yet.')",
		"rememberPackagingCommission(commission, { kind, title })",
		"schedulePackagingCommissionPoll()",
	} {
		if !strings.Contains(submit, want) {
			t.Errorf("submitPackagingCommission missing %q", want)
		}
	}
	story := packagingWave11Function(html, "async function createStoryOutline(brief, options = {}) {")
	for _, want := range []string{"fetch('/assistant/packaging/stories', {", "await openStoryOutlineThread(story)"} {
		if !strings.Contains(story, want) {
			t.Errorf("createStoryOutline missing %q", want)
		}
	}
	build := functionBody(html, "async function buildDeckFromStory(story, button) {")
	if !strings.Contains(build, "fetch(`/assistant/packaging/stories/${encodeURIComponent(id)}/deck`, {") {
		t.Error("the outline builds the deck through POST /assistant/packaging/stories/{id}/deck")
	}
	bar := functionBody(html, "function syncPackagingStoryBar() {")
	for _, want := range []string{"draftedBy.startsWith('model:') ? 'drafted by Scout' : draftedBy.startsWith('scaffold') ? 'starter outline (no model)' : ''", "`v${Number(story.version)}`"} {
		if !strings.Contains(bar, want) {
			t.Errorf("story bar provenance missing %q", want)
		}
	}
	if !strings.Contains(html, "'Build the deck from this outline'") {
		t.Error("the outline thread must offer Build the deck from this outline")
	}
}

func TestIndexPackagingDeliverableActionsAndProjects(t *testing.T) {
	html := readIndexForPackagingWave11(t)
	menu := functionBody(html, "function studioProjectMenuItems(project, trigger) {")
	for _, want := range []string{
		"label: 'Rename'", "label: 'Duplicate'", "label: 'Save to Drive'", "label: 'Download DOCX'", "label: 'Download PDF'",
		"project.kind === 'presentation' ? 'Open in Deck Studio' : 'Open in editor'",
		"`Project · ${project.project}` : 'Project…'",
		"label: `Open Projects/${project.project} in Drive`",
	} {
		if !strings.Contains(menu, want) {
			t.Errorf("row menu missing %q", want)
		}
	}
	row := functionBody(html, "function studioProjectRowNode(project) {")
	for _, want := range []string{"const item = bfEl('div', 'studio-project-item')", "item.append(row, studioProjectRowMenu(project))", "packagingWaitingLabel(packagingCommissionFor(project))"} {
		if !strings.Contains(row, want) {
			t.Errorf("row node missing %q", want)
		}
	}
	tools := packagingWave11Section(t, html, "function studioProjectToolsRow(project, context) {", "/* D4: ask for changes from Work.")
	for _, want := range []string{
		"studioProjectAction('Open in editor', false, () => void openStudioProjectInEditor(project))",
		"studioProjectAction('Duplicate', false, () => void duplicateStudioProject(project))",
		"studioProjectAction('DOCX', false, () => downloadStudioProjectDocx(project, docx))",
		// review fix: the tag lands on the studio ROOT (studioProjectTagArtifactId),
		// not the ship_compile child /artifacts/project refuses
		"chooseArtifactProject(studioProjectTagArtifactId(project), projectPick, { current: project.project || '' })",
		"studioProjectAction('Open in Drive', false, () => void openDriveFolder(project.projectFolderId || '', project.project))",
		// the Wave 3 verb survives verbatim
		"studioProjectAction('Ask for changes', false, () => askForStudioProjectChanges(project))",
	} {
		if !strings.Contains(tools, want) {
			t.Errorf("detail tools row missing %q", want)
		}
	}
	// research opens in the document editor (probe, then the Wave 4 import path)
	research := functionBody(html, "async function openResearchInDocumentEditor(project) {")
	for _, want := range []string{"fetch(`/artifacts/document?id=${encodeURIComponent(artifactId)}`", "fetch('/artifacts/document/import', {", "return openDocumentStudio("} {
		if !strings.Contains(research, want) {
			t.Errorf("research → document editor missing %q", want)
		}
	}
	// inline rename through the studio PATCH
	rename := functionBody(html, "function beginStudioProjectInlineRename(project) {")
	for _, want := range []string{"input.className = 'studio-project-detail__title-input'", "fetch('/api/studio-projects/v1', {", "expectedRevision: project.revision", "if (event.key === 'Escape')"} {
		if !strings.Contains(rename, want) {
			t.Errorf("inline rename missing %q", want)
		}
	}
	if !strings.Contains(html, "rename.addEventListener('click', () => beginStudioProjectInlineRename(project))") {
		t.Error("the detail Rename must be inline, not a prompt")
	}
	// duplicate + DOCX
	if !strings.Contains(packagingWave11Function(html, "async function duplicateStudioArtifact(artifactId, title, options = {}) {"), "fetch('/artifacts/duplicate', {") {
		t.Error("Duplicate posts /artifacts/duplicate")
	}
	if !strings.Contains(functionBody(html, "async function downloadStudioProjectDocx(project, button) {"), "fetch('/artifacts/export-docx', {") {
		t.Error("Download DOCX posts /artifacts/export-docx")
	}
	// project picker: existing + New project…, PATCH /artifacts/project, folder door
	picker := packagingWave11Function(html, "async function chooseArtifactProject(artifactId, trigger, options = {}) {")
	// The Open folder door is offered only against the folder id the tag door
	// actually returned — a tag the Drive fences degraded (folderId "") gets
	// neither the door nor a "Filed under" claim. See
	// TestIndexProjectTagToastReportsTheFilingTheServerActuallyDid.
	for _, want := range []string{"const projects = await loadPackagingProjects()", "{ id: 'none', label: 'No project', radio: true", "label: 'New project…'", "bfMenu(trigger, { items, origin: 'auto', fixed: true, radio: true, label: 'Project'", "label: 'Open folder', onSelect: () => void openDriveFolder(projectFolderId, filed)"} {
		if !strings.Contains(picker, want) {
			t.Errorf("project picker missing %q", want)
		}
	}
	if !strings.Contains(functionBody(html, "async function setArtifactProject(artifactId, name) {"), "fetch('/artifacts/project', {\n          method: 'PATCH'") {
		t.Error("project tags go through PATCH /artifacts/project")
	}
	if !strings.Contains(functionBody(html, "async function loadPackagingProjects(force = false) {"), "fetch('/assistant/projects', { cache: 'no-store' })") {
		t.Error("projects list from GET /assistant/projects")
	}
	// hub filter by project
	for _, want := range []string{
		`<div id="studioProjectProjects" class="studio-projects__projects" role="group" aria-label="Filter by project" hidden></div>`,
		"if (studioProjectProjectFilter) params.set('project', studioProjectProjectFilter)",
		// String(): the filter is a hoisted var that may still be undefined at boot
		"if (studioProjectProjectFilter && String(project?.project || '').trim().toLowerCase() !== String(studioProjectProjectFilter).toLowerCase()) return false",
		"function renderStudioProjectProjectFilter() {",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("project filter missing %q", want)
		}
	}
	// editors carry the same verbs
	for _, want := range []string{
		`<button type="button" role="menuitem" data-doc-action="rename">Rename</button>`,
		`<button type="button" role="menuitem" data-doc-action="duplicate">Duplicate</button>`,
		`<button type="button" role="menuitem" data-doc-action="project">Project…</button>`,
		`<button type="button" role="menuitem" data-action="duplicate"><span>Duplicate</span></button>`,
		`<button type="button" role="menuitem" data-action="project"><span>Project…</span></button>`,
		"else if (action === 'duplicate') void duplicateStudioArtifact(state.artifactId, titleInput.value.trim() || state.title, { reloadHub: appShell.dataset.tool === 'research' })",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("editor verbs missing %q", want)
		}
	}
}

func TestIndexPackagingThreadedQuestions(t *testing.T) {
	html := readIndexForPackagingWave11(t)
	node := functionBody(html, "function packagingClarifyingNode(message) {")
	for _, want := range []string{
		"const clarifying = packagingClarifying(message)",
		"node.dataset.state = answered ? 'answered' : 'open'",
		"const pill = bfEl('button', 'packaging-clarify__pill', option)",
		"pill.setAttribute('role', 'radio')",
		"input.className = 'packaging-clarify__input'",
		"'Send answers'",
		"'answered · brief complete'",
	} {
		if !strings.Contains(node, want) {
			t.Errorf("clarifying node missing %q", want)
		}
	}
	answer := functionBody(html, "async function answerPackagingQuestions(message, clarifying, answers, threadId, node, button) {")
	for _, want := range []string{
		"fetch(`/assistant/chat-threads/${encodeURIComponent(threadId)}/messages`, {",
		"replyToMessageId: String(message.id)",
		"clarifying: { commissionId: clarifying.commissionId, answers:",
		// optimistic brief-complete, reconciled by the poll
		"{ briefComplete: true, briefCompleteAt: Date.now(), waitingOn: null }",
		"schedulePackagingCommissionPoll()",
	} {
		if !strings.Contains(answer, want) {
			t.Errorf("answerPackagingQuestions missing %q", want)
		}
	}
	// rendered in the feed and in the reply rail
	feed := functionBody(html, "function decorateDesktopChatMessage(node, message, kind, authorLabel) {")
	if !strings.Contains(feed, "const clarifyingNode = packagingClarifyingNode(message)") {
		t.Error("the feed must render the clarifying questions inline")
	}
	rail := packagingWave11Function(html, "function desktopContextMessageCard(thread, message, options = {}) {")
	if !strings.Contains(rail, "const clarifyingCard = packagingClarifyingNode(message)") {
		t.Error("the reply rail must render the clarifying questions inline")
	}
	// the row reads waiting on you · N questions and links to the thread
	waiting := functionBody(html, "function packagingWaitingLabel(commission) {")
	if !strings.Contains(waiting, "return `waiting on ${mine ? 'you' : name}${questions}`") {
		t.Error("waiting label must read waiting on you · N questions")
	}
	commission := functionBody(html, "function studioProjectCommissionNode(project) {")
	for _, want := range []string{"'Answer in the thread'", "openPackagingCommissionThread(commission)", "'brief complete · Scout is starting'"} {
		if !strings.Contains(commission, want) {
			t.Errorf("commission block missing %q", want)
		}
	}
	poll := functionBody(html, "async function pollPackagingCommissions() {")
	if !strings.Contains(poll, "fetch(`/assistant/packaging/commissions/${encodeURIComponent(record.id)}`") {
		t.Error("commission state reconciles through GET /assistant/packaging/commissions/{id}")
	}
}

func TestIndexPackagingPrivateThreadRealDoorsOnly(t *testing.T) {
	html := readIndexForPackagingWave11(t)
	for _, gone := range []string{"function buildScoutStarterRow(", "'Pitch the idea'", "'Research the question'", "'Model the business'", "'Shape the visual direction'", ".scout-starter {"} {
		if strings.Contains(html, gone) {
			t.Errorf("starter pills must be gone; found %q", gone)
		}
	}
	doors := functionBody(html, "function buildPrivateThreadActionsRow(thread) {")
	for _, want := range []string{
		"add('research', 'Research Desk', () => void openPackagingBriefSheet('research', { threadId }))",
		"add('present', 'Presentation Studio', () => void openPackagingBriefSheet('presentation', { threadId }))",
		"add('list', 'Story Studio', () => void openPackagingBriefSheet('story', { threadId }))",
		"add('drive', 'Attach from Drive', () => void attachDriveRefsForWork())",
		"add('clock', 'Ask about a meeting', trigger => void openMeetingContextPicker(trigger))",
	} {
		if !strings.Contains(doors, want) {
			t.Errorf("private thread doors missing %q", want)
		}
	}
	if strings.Contains(doors, "scoutChatInput.value =") {
		t.Error("a door must never merely type words into the composer")
	}
	empty := functionBody(html, "function ensureScoutChatEmptyState(")
	if !strings.Contains(empty, "privateChatContinuityNode(continuityItem)") {
		t.Error("the empty state keeps Continue <last thread>")
	}
	picker := functionBody(html, "async function openMeetingContextPicker(trigger) {")
	for _, want := range []string{"fetch('/assistant/meetings?view=index&limit=12'", "pendingScoutMeetingContext = { id: String(meeting.id), title: memoryMeetingTitle(meeting), when }"} {
		if !strings.Contains(picker, want) {
			t.Errorf("meeting picker missing %q", want)
		}
	}
	if !strings.Contains(html, "text = consumePendingScoutMeetingContext(text)") {
		t.Error("the chosen meeting must ride the send as context")
	}
}

// Reviewer findings (2026-09-02): the sheet returns focus to its opener, and
// the "waiting on you · N questions" state survives a reload because the
// renderer reads commission.waitingOn from the project payload — the local
// cache is only the optimistic overlay until the next poll, the poll is seeded
// from server rows, and a route switch clears it.
func TestIndexPackagingSheetFocusAndCommissionTruth(t *testing.T) {
	html := readIndexForPackagingWave11(t)
	sheet := packagingWave11Function(html, "async function openPackagingBriefSheet(kind, options = {}) {")
	for _, want := range []string{
		"const opener = document.activeElement",
		"if (opener instanceof HTMLElement && opener.isConnected) opener.focus({ preventScroll: true })",
	} {
		if !strings.Contains(sheet, want) {
			t.Errorf("brief sheet focus restore missing %q", want)
		}
	}
	if strings.Index(sheet, "const opener = document.activeElement") > strings.Index(sheet, "dialog.showModal()") {
		t.Error("the opener must be captured before showModal()")
	}
	merge := functionBody(html, "function packagingCommissionFor(project) {")
	for _, want := range []string{
		"const server = project.commission && typeof project.commission === 'object' ? project.commission : null",
		"? { ...(local || {}), ...server, waitingOn: server.waitingOn ?? null, waitingOnName: String(server.waitingOnName || ''), questions: Array.isArray(server.questions) ? server.questions : [], briefComplete: server.briefComplete === true }",
		"if (local?.briefComplete && Date.now() - Number(local.briefCompleteAt || 0) < 90000) {",
	} {
		if !strings.Contains(merge, want) {
			t.Errorf("packagingCommissionFor must read the server's commission first; missing %q", want)
		}
	}
	// the row renderer reaches the server fields through the same merge
	if !strings.Contains(functionBody(html, "function studioProjectRowNode(project) {"), "packagingWaitingLabel(packagingCommissionFor(project))") {
		t.Error("the row reads commission.waitingOn from the project payload")
	}
	open := functionBody(html, "function packagingOpenCommissions() {")
	for _, want := range []string{"const commission = project?.commission", "if (!id || (!commission.waitingOn && terminal(commission.status || project.status))) return", "packagingCommissionsLocal.forEach(record => {"} {
		if !strings.Contains(open, want) {
			t.Errorf("poll seed missing %q", want)
		}
	}
	if !strings.Contains(functionBody(html, "async function pollPackagingCommissions() {"), "const open = packagingOpenCommissions()") {
		t.Error("the poll iterates the seeded open set")
	}
	schedule := functionBody(html, "function schedulePackagingCommissionPoll() {")
	if !strings.Contains(schedule, "appShell.dataset.tool !== 'research'") || !strings.Contains(schedule, "if (!packagingOpenCommissions().length) return") {
		t.Error("the poll arms only on the hub and only with open commissions")
	}
	if !strings.Contains(functionBody(html, "function syncPD1Destination(destination) {"), "if (next !== 'Work' && typeof clearPackagingCommissionPoll === 'function') clearPackagingCommissionPoll()") {
		t.Error("a route switch must clear the commission poll")
	}
	if !strings.Contains(functionBody(html, "function renderStudioProjects() {"), "schedulePackagingCommissionPoll()") {
		t.Error("the hub render seeds the poll from server rows")
	}
}

func TestIndexPackagingStudioCanon(t *testing.T) {
	html := readIndexForPackagingWave11(t)
	css := packagingWave11Section(t, html, "/* ---------- Wave 11: Packaging Studio ----------\n         Three quiet studio rows", ".studio-projects__head {")
	// no active-state edge bars anywhere in the block
	for _, bar := range []string{"inset 2px 0", "inset 3px 0", "border-left:", "border-right:"} {
		if strings.Contains(css, bar) {
			t.Errorf("Wave 11 CSS paints an edge bar (%q)", bar)
		}
	}
	// ember only on live / earned states: none of these surfaces are live
	if strings.Contains(css, "--ember") || strings.Contains(css, "--accent") || strings.Contains(css, "--agent") {
		t.Error("ember leaked into a resting Packaging Studio surface")
	}
	// one hex only (theme swatch fallbacks live in JS); tokens carry the rest
	if hex := regexp.MustCompile(`#[0-9a-fA-F]{3,6}\b`).FindAllString(css, -1); len(hex) != 0 {
		t.Errorf("Wave 11 CSS uses raw hexes %v; use the token ladder", hex)
	}
	// glass: the sheet tier, no bespoke material
	if !strings.Contains(html, "dialog.className = 'packaging-brief glass-sheet'") {
		t.Error("the brief sheet rides the sheet tier")
	}
	// menus are bfMenu, icons are strideIcon
	for _, want := range []string{
		// review fix: bindTrigger:false — the kebab handler owns the toggle, so
		// bfMenu must not also bind the trigger (second click closed + re-opened)
		"bfMenu(kebab, { items: studioProjectMenuItems(project, kebab), origin: 'auto', fixed: true, label: 'Deliverable actions', closeOnSelect: true, bindTrigger: false })",
		"strideIconButton('studio-project-row__kebab', 'more-vertical', { size: 16 })",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("canon component missing %q", want)
		}
	}
	// every timing rides a token
	for _, line := range strings.Split(css, "\n") {
		if strings.Contains(line, "transition:") && !strings.Contains(line, "var(--dur-") {
			t.Errorf("untokenised transition: %q", strings.TrimSpace(line))
		}
	}
}
