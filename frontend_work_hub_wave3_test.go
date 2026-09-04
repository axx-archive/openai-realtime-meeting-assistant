package main

// Wave 3 (Work hub truth) static pins for index.html. They read the file, so
// they hold with no browser in the loop:
//
//   D1 the Work list consumes artifact_progress / artifact_completed os_events
//      (registered below the osEventHandlers const, never above it), the poll
//      is a 30 s fallback, and the rail count is mono / --text-3 / no ember;
//   D2 Save to Files posts the plain door first and only falls back to the
//      env-gated disposition route on 404/501;
//   D3 one chip per kind the list route accepts (?kind=), kind glyphs from
//      strideIcon, and the server's openAction drives the primary verb;
//   D4 Ask for changes arms the follow-up seam and launches nothing;
//   D5 versions open read-only from a bodyRef, else stay disabled with a reason;
//   D6 room-launched work names its room with the rooms glyph;
//   D7 the step rail has the four states and gates its pulse on reducedMotion;
//   D8 honest status copy — never "Done" for a failed run.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForWorkHubWave3(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// workHubWave3Section returns html[start:end) located by two unique markers.
func workHubWave3Section(t *testing.T, html, startMarker, endMarker string) string {
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

func TestIndexWorkHubLiveListAndRailCount(t *testing.T) {
	html := readIndexForWorkHubWave3(t)
	registry := strings.Index(html, "const osEventHandlers = []")
	handler := strings.Index(html, "if (event.kind === 'artifact_progress' || event.kind === 'artifact_completed') scheduleWorkHubRefresh()")
	if registry < 0 || handler < 0 {
		t.Fatal("the Work hub must register an osEventHandlers consumer for artifact_progress / artifact_completed")
	}
	if handler < registry {
		t.Fatal("the Work hub os_event handler must be registered below the osEventHandlers const (boot-time TDZ)")
	}
	refresh := workHubWave3Section(t, html, "function scheduleWorkHubRefresh()", "function studioProjectDeliverableCopy(")
	for _, want := range []string{
		"loadStudioProjects({ onlyIfChanged: true })",
		"refreshWorkRunningCount()",
		"}, 500)",
	} {
		if !strings.Contains(refresh, want) {
			t.Errorf("scheduleWorkHubRefresh missing %q", want)
		}
	}
	if !strings.Contains(html, "studioProjectsRefreshTimer = window.setTimeout(() => loadStudioProjects({ onlyIfChanged: true }), 30000)") {
		t.Error("the Work list poll must stay as a 30 s fallback")
	}
	if strings.Contains(html, "loadStudioProjects({ onlyIfChanged: true }), 6000)") {
		t.Error("the 6 s Work poll must not survive the live list")
	}
	for _, want := range []string{
		`<span id="workRailBadge" class="pd1-primary-nav__count" hidden aria-label="">0</span>`,
		// STRIDE 3 presents the durable outcome destination as Work.
		`data-pd1-destination="Work" aria-label="Work"`,
		"function syncWorkRailBadge()",
		"workRailBadge.hidden = count === 0",
		"['queued', 'running'].includes(String(project?.status || ''))",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("Work rail count missing %q", want)
		}
	}
	badge := workHubWave3Section(t, html, ".pd1-primary-nav__item > .pd1-primary-nav__count {", "}")
	for _, want := range []string{"color: var(--text-3)", "var(--font-mono)", "font-variant-numeric: tabular-nums"} {
		if !strings.Contains(badge, want) {
			t.Errorf("rail count rule missing %q", want)
		}
	}
	if strings.Contains(badge, "ember") {
		t.Error("the rail count is a machine fact — it must not carry ember")
	}
}

func TestIndexWorkHubSaveToFilesOrder(t *testing.T) {
	html := readIndexForWorkHubWave3(t)
	control := workHubWave3Section(t, html, "function artifactSaveToFilesControl(entry, options = {})", "function validArtifactDispositionRef(value)")
	plain := strings.Index(control, "fetch('/assistant/files/save', {")
	gated := strings.Index(control, "fetch('/api/artifact-drive-saves/v1', {")
	if plain < 0 || gated < 0 {
		t.Fatal("artifactSaveToFilesControl must know both save doors")
	}
	if plain > gated {
		t.Fatal("the plain save door must be posted before the env-gated disposition route")
	}
	for _, want := range []string{
		"} else if (plain.status !== 404 && plain.status !== 501) {",
		"const artifactId = String(entry?.id || entry?.artifactId || '').trim()",
		"options.qualified !== true && !artifactQualifiesForFiles(entry)",
		"body: JSON.stringify({ artifactId, fileName: destination.fileName, folderId: destination.folderId })",
		"showToast({ text: `${savedLabel} · ${savedFile.name}`, kind: 'done', action: { label: 'Open', onSelect: () => openDriveFileByID(savedFileId) } })",
		// the exact-binding drift gate still guards both doors
		"artifactEntryCapabilityDigest(refreshedArtifact) !== expectedDigest",
		"This deliverable changed before it could be saved",
	} {
		if !strings.Contains(control, want) {
			t.Errorf("artifactSaveToFilesControl missing %q", want)
		}
	}
	detail := workHubWave3Section(t, html, "function studioProjectSaveControl(project)", "function studioProjectToolsRow(")
	for _, want := range []string{
		"artifactSaveToFilesControl(project.result, {",
		"qualified: true",
		"readyLabel: 'Save to Drive'", // Wave 11 D7: Drive is the destination's name
		"savedLabel: 'Saved to Drive'",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("Work detail Save to Files missing %q", want)
		}
	}
	stage := workHubWave3Section(t, html, "async function openArtifactStage(artifactId, fallbackTitle, options)", "async function openAuthorizedChatThreadAction(")
	if !strings.Contains(stage, "const saveToFilesControl = artifactSaveToFilesControl(entry, { expectedBinding })") {
		t.Error("the artifact stage must keep routing its Save control through artifactSaveToFilesControl (same door order)")
	}
	if !strings.Contains(html, "if (payload.action?.label && typeof payload.action.onSelect === 'function') {") {
		t.Error("showToast must render the in-app Open action")
	}
}

func TestIndexWorkHubKindChipsGlyphsAndOpenVerb(t *testing.T) {
	html := readIndexForWorkHubWave3(t)
	if !strings.Contains(html, `<div id="studioProjectKinds" class="studio-projects__kinds" role="group" aria-label="Filter work by kind"></div>`) {
		t.Fatal("the Work library must carry the kind chip row")
	}
	kinds := workHubWave3Section(t, html, "const STUDIO_PROJECT_KINDS = Object.freeze([", "])")
	for _, want := range []string{
		"{ id: 'presentation', label: 'Presentation', chip: 'Decks', icon: 'present'",
		"{ id: 'document', label: 'Document', chip: 'Docs', icon: 'file-text'",
		"{ id: 'image', label: 'Image', chip: 'Images', icon: 'image'",
		"{ id: 'sheet', label: 'Spreadsheet', chip: 'Sheets', icon: 'table'",
		"{ id: 'research', label: 'Research', chip: 'Research', icon: 'research'",
		"{ id: 'artifact', label: 'File', chip: 'Files', icon: 'file'",
	} {
		if !strings.Contains(kinds, want) {
			t.Errorf("STUDIO_PROJECT_KINDS missing %q", want)
		}
	}
	for _, want := range []string{
		"function setStudioProjectKindFilter(kind)",
		"function renderStudioProjectKindChips()",
		"chip.setAttribute('aria-pressed', chip.dataset.studioKind === current ? 'true' : 'false')",
		"if (studioProjectKindSpec(studioProjectFilter)) params.set('kind', studioProjectFilter)",
		"const params = new URLSearchParams({ limit: '200' })",
		"icon.appendChild(strideIcon(studioProjectIconName(kind), { size: 18 }))",
		"failed: 'Failed' })",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("kind chips / glyphs missing %q", want)
		}
	}
	// every kind glyph resolves in the icon table
	icons := workHubWave3Section(t, html, "const STRIDE_ICON_PATHS = {", "function strideIcon(")
	for _, name := range []string{"present:", "'file-text':", "image:", "table:", "research:", "file:", "rooms:", "check:", "close:", "'chevron-left':", "'chevron-down':", "work:"} {
		if !strings.Contains(icons, name) {
			t.Errorf("STRIDE_ICON_PATHS missing glyph %q for the Work hub", name)
		}
	}
	verb := workHubWave3Section(t, html, "function studioProjectOpenAction(project)", "function studioProjectSaveControl(")
	for _, want := range []string{
		"const action = String(result.openAction || '').trim()",
		"if (action === 'download' && result.downloadUrl) return studioProjectDownloadAction(project, true)",
		"if (action === 'image') return studioProjectAction('Open image', true,",
		"if (action === 'present') return studioProjectAction('Present', true,",
		"if (action === 'document') return studioProjectAction('Open', true,",
	} {
		if !strings.Contains(verb, want) {
			t.Errorf("openAction verb missing %q", want)
		}
	}
	download := workHubWave3Section(t, html, "function studioProjectDownloadAction(project, primary)", "function studioProjectOpenAction(")
	for _, want := range []string{"const url = String(result.downloadUrl || '').trim()", "link.href = url", "link.download = name"} {
		if !strings.Contains(download, want) {
			t.Errorf("downloadUrl verb missing %q", want)
		}
	}
	// the kind verbs row for decks is unchanged; hub verbs live in their own row
	if !strings.Contains(html, "const tools = studioProjectToolsRow(project, { resultReady })") || !strings.Contains(html, "bfEl('div', 'studio-project-detail__tools')") {
		t.Error("hub verbs must render in the studio-project-detail__tools row beneath the kind verbs")
	}
}

func TestIndexWorkHubAskForChangesNeverLaunches(t *testing.T) {
	html := readIndexForWorkHubWave3(t)
	ask := workHubWave3Section(t, html, "async function askForStudioProjectChanges(project)", "function studioProjectOriginNode(")
	for _, want := range []string{
		"await openStudioProjectSource(project)",
		"if (activeScoutThreadId !== threadId) return false",
		"armScoutFollowUpTarget(artifactId, {",
		"prompt: `Changes to “${title}”: `",
		"prefill: true",
		"scoutChatInput.focus()",
	} {
		if !strings.Contains(ask, want) {
			t.Errorf("askForStudioProjectChanges missing %q", want)
		}
	}
	for _, forbidden := range []string{"fetch(", "/artifacts/action", "postAuthJSON(", "launchGoal", "sendScoutChat"} {
		if strings.Contains(ask, forbidden) {
			t.Errorf("askForStudioProjectChanges must never launch anything itself; found %q", forbidden)
		}
	}
	if !strings.Contains(html, "studioProjectAction('Ask for changes', false, () => askForStudioProjectChanges(project))") {
		t.Error("the Work detail must offer Ask for changes")
	}
}

func TestIndexWorkHubVersionsAndRoomOrigin(t *testing.T) {
	html := readIndexForWorkHubWave3(t)
	versions := workHubWave3Section(t, html, "function studioProjectVersionsNode(project)", "async function openStudioProjectVersion(")
	for _, want := range []string{
		"bfEl('details', 'studio-project-versions')",
		"Array.isArray(result?.versions) ? result.versions : []",
		"const bodyRef = String(version.bodyRef || '').trim()",
		"row.addEventListener('click', () => openStudioProjectVersion(project, version, row))",
		"row.disabled = true",
		"has no stored snapshot, so it cannot be opened.",
		"bfEl('span', 'studio-project-version__time', studioProjectRelativeTime(version.at) || '—')",
	} {
		if !strings.Contains(versions, want) {
			t.Errorf("versions disclosure missing %q", want)
		}
	}
	open := workHubWave3Section(t, html, "async function openStudioProjectVersion(project, version, button)", "async function startStudioProjectWithScout()")
	for _, want := range []string{
		"artifactBlobUrl({ ref: bodyRef, name: title })",
		"canEdit: false, hideEdit: true,",
		"snapshot: { version: number, at: String(version.at || ''), source: String(version.source || ''), text }",
	} {
		if !strings.Contains(open, want) {
			t.Errorf("openStudioProjectVersion missing %q", want)
		}
	}
	stage := workHubWave3Section(t, html, "async function openArtifactStage(artifactId, fallbackTitle, options)", "async function openAuthorizedChatThreadAction(")
	for _, want := range []string{
		"const stageSnapshot = options?.snapshot && typeof options.snapshot === 'object' && typeof options.snapshot.text === 'string' ? options.snapshot : null",
		"const managedAuthoredEntry = !stageSnapshot && artifactPublicationManagedHint(entry, stageProjectedPublication)",
		"if (saveToFilesControl && !stageSnapshot) headActions.appendChild(saveToFilesControl)",
		"frame.setAttribute('sandbox', '')",
		"frame.srcdoc = stageSnapshot.text",
		"'artifact-stage__snapshot'",
	} {
		if !strings.Contains(stage, want) {
			t.Errorf("read-only version stage missing %q", want)
		}
	}
	// mono for the machine facts: version tag and time
	for _, want := range []string{
		".studio-project-version__tag,\n      .studio-project-version__time { color: var(--ink-2); font: var(--type-numeric); font-variant-numeric: tabular-nums; white-space: nowrap; }",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("versions CSS missing %q", want)
		}
	}
	origin := workHubWave3Section(t, html, "function studioProjectOriginNode(project)", "function studioProjectStepsNode(")
	for _, want := range []string{
		"if (!origin || origin.kind !== 'room') return null",
		"strideIcon('rooms', { size: 14 })",
		"`from room ${roomTitle || 'call'}`",
	} {
		if !strings.Contains(origin, want) {
			t.Errorf("room origin missing %q", want)
		}
	}
	if !strings.Contains(html, "const roomMark = strideIcon('rooms', { size: 12, className: 'studio-project-row__origin' })") {
		t.Error("list rows must carry the small rooms glyph for room-launched work")
	}
}

func TestIndexWorkHubStepRailStatesAndReducedMotion(t *testing.T) {
	html := readIndexForWorkHubWave3(t)
	steps := workHubWave3Section(t, html, "function studioProjectStepsNode(project)", "function studioProjectVersionsNode(")
	for _, want := range []string{
		"['queued', 'running', 'done', 'failed'].includes(String(step.state || ''))",
		"item.dataset.state = state",
		"if (state === 'done') node.appendChild(strideIcon('check', { size: 14 }))",
		"if (state === 'failed') node.appendChild(strideIcon('close', { size: 14 }))",
		"if (state === 'running' && !reducedMotion.matches) node.classList.add('is-pulsing')",
	} {
		if !strings.Contains(steps, want) {
			t.Errorf("step rail missing %q", want)
		}
	}
	for _, want := range []string{
		`.studio-project-step[data-state="running"] .studio-project-step__node.is-pulsing::before { animation: studio-step-pulse var(--dur-breathe) var(--ease) infinite; }`,
		`.studio-project-step[data-state="failed"] .studio-project-step__node { color: var(--danger-text); }`,
		`.studio-project-step[data-state="failed"] .studio-project-step__label { color: var(--danger-text); }`,
		".studio-project-step__node::before { content: \"\"; width: 8px; height: 8px; border-radius: var(--r-full); border: 1.5px solid var(--ink-3); background: var(--surface-1); }",
		"@keyframes studio-step-pulse {",
		".studio-project-step__node.is-pulsing::before { animation: none; }",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("step rail CSS missing %q", want)
		}
	}
	reduced := workHubWave3Section(t, html, "@media (prefers-reduced-motion: reduce) {\n        .studio-project-step__node.is-pulsing::before", "}")
	if !strings.Contains(reduced, "animation: none") {
		t.Error("the step pulse must die under prefers-reduced-motion")
	}
	// ember in the Wave 3 block belongs to the running node alone
	block := workHubWave3Section(t, html, "/* ---------- Wave 3: Work hub truth ----------", ".studio-projects__loading,")
	for _, line := range strings.Split(block, "\n") {
		if !strings.Contains(line, "--ember") {
			continue
		}
		if strings.Contains(line, `data-state="running"`) || strings.Contains(line, "box-shadow: 0 0 0 3px var(--ember-soft); }") || strings.HasPrefix(strings.TrimSpace(line), "the running step node") {
			continue
		}
		t.Errorf("ember leaked into a resting Work surface rule: %q", strings.TrimSpace(line))
	}
	if !strings.Contains(html, ".studio-project-row__icon {\n        width: 36px;\n        height: 36px;\n        display: grid;\n        place-items: center;\n        border-radius: 12px;\n        /* Wave 3: a list at rest holds no ember") {
		t.Error("the row kind glyph must sit on the well in ink, not on an ember wash")
	}
}

func TestIndexWorkHubHonestStatusCopy(t *testing.T) {
	html := readIndexForWorkHubWave3(t)
	edge := workHubWave3Section(t, html, "function studioProjectRowEdgeLabel(project)", "function studioProjectDownloadAction(")
	for _, want := range []string{
		"if (status === 'ready') return 'Done'",
		"if (status === 'failed') return 'Failed'",
		"if (status === 'stopped') return 'Stopped'",
	} {
		if !strings.Contains(edge, want) {
			t.Errorf("row edge label missing %q", want)
		}
	}
	if strings.Contains(html, "project.status === 'ready' ? 'Done' : `${Math.max(0, Math.min(100, Number(project.progressPercent || 0)))}%`") {
		t.Error("the row edge must route through studioProjectRowEdgeLabel")
	}
	copyFn := workHubWave3Section(t, html, "function studioProjectDeliverableCopy(project, context)", "function studioProjectRowEdgeLabel(")
	for _, want := range []string{
		"if (status === 'failed') {",
		"kicker: resultReady ? 'Run failed · last draft' : 'Run failed'",
		"The run ended before a finished file existed.",
		"if (status === 'stopped') {",
		"title: resultReady ? (project.result.title || project.title) : 'This work was stopped'",
		"if (status === 'queued' && !resultReady) {",
		"title: 'Waiting to start'",
		"'A decision is needed'",
		"'Scout needs attention'",
		"'Scout is working'",
		"'Final deliverable'",
	} {
		if !strings.Contains(copyFn, want) {
			t.Errorf("honest deliverable copy missing %q", want)
		}
	}
	if strings.Contains(copyFn, "'Done'") {
		t.Error("the deliverable copy must never call a failed or stopped run Done")
	}
	for _, want := range []string{
		"['Recent', project => ['ready', 'stopped', 'failed'].includes(project.status)]",
		`.studio-project-status[data-status="failed"] { background: var(--danger-soft); color: var(--danger-text); }`,
		`.studio-project-status[data-status="stopped"] { background: var(--well); color: var(--ink-3); }`,
		`.studio-project-row__percent[data-state="failed"] { color: var(--danger-text); }`,
		"function studioProjectEmptyCopy(kind)",
		"function studioProjectFilterTitles(kind)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("honest states missing %q", want)
		}
	}
}
