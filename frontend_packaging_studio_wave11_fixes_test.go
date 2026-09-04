package main

import (
	"os"
	"strings"
	"testing"
)

// Wave 11 review fixes. Each pin holds the SCENARIO the defect broke, not just
// the literal that happened to encode it — a reviewer changing the wording must
// keep the behaviour these describe.

func readIndexForWave11Fixes(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// wave11FixesBody handles signatures carrying a default object parameter
// (`options = {}`): the brace scan starts at the signature's own last brace.
func wave11FixesBody(html, signature string) string {
	start := strings.Index(html, signature)
	if start < 0 {
		return ""
	}
	return functionBody(html[start+len(signature)-1:], "{")
}

func wave11FixesSlice(t *testing.T, html, start, end string) string {
	t.Helper()
	from := strings.Index(html, start)
	if from < 0 {
		t.Fatalf("missing %q", start)
	}
	to := strings.Index(html[from:], end)
	if to < 0 {
		t.Fatalf("missing %q after %q", end, start)
	}
	return html[from : from+to]
}

// A commission deliverable is a goal CHILD (ship_compile, goalParentId set).
// PATCH /artifacts/project only accepts what artifactProjectTaggable
// recognizes — a studio project root — and the hub reads the tag back off that
// same root, so the picker must address the root, while the deliverable verbs
// (duplicate / DOCX / open in editor) keep addressing the result artifact.
func TestIndexPackagingProjectTagAddressesTheStudioRoot(t *testing.T) {
	html := readIndexForWave11Fixes(t)
	tag := functionBody(html, "function studioProjectTagArtifactId(project) {")
	if tag == "" {
		t.Fatal("studioProjectTagArtifactId missing — the project picker must not reuse the deliverable id")
	}
	if strings.Index(tag, "rootArtifactId") > strings.Index(tag, "result?.artifactId") {
		t.Errorf("the tag id must prefer the root over the goal child:\n%s", tag)
	}
	// both picker doors — the detail pane verb and the row kebab item
	for _, want := range []string{
		"chooseArtifactProject(studioProjectTagArtifactId(project), projectPick, { current: project.project || '' })",
		"chooseArtifactProject(studioProjectTagArtifactId(project), trigger, { current: project?.project || '' })",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("project picker call site missing %q", want)
		}
	}
	if strings.Contains(html, "chooseArtifactProject(studioProjectArtifactId(") {
		t.Error("the project picker must never be handed studioProjectArtifactId (the ship_compile child) — /artifacts/project rejects it")
	}
	// the deliverable verbs are unchanged: they still act on the result
	deliverable := functionBody(html, "function studioProjectArtifactId(project) {")
	if !strings.Contains(deliverable, "project?.result?.artifactId || project?.rootArtifactId") {
		t.Errorf("deliverable verbs must still prefer result.artifactId:\n%s", deliverable)
	}
	for _, want := range []string{
		"duplicateStudioArtifact(studioProjectArtifactId(project), studioProjectDisplayTitle(project))",
		"const artifactId = studioProjectArtifactId(project)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("deliverable verb call site missing %q", want)
		}
	}
}

// packagingCommissionStatus can only ever answer queued/running/
// needs_attention/ready/stopped (studioProjectStatus with
// hasActionableCheckpoint=false), so testing it for wait/clarif/needs_input was
// always false — and the poll then force-wrote briefComplete:false and
// waitingOn:null over the server's own intake truth on every tick.
func TestIndexPackagingPollDerivesWaitingFromThePayloadNotTheStatusString(t *testing.T) {
	html := readIndexForWave11Fixes(t)
	poll := functionBody(html, "async function pollPackagingCommissions() {")
	if poll == "" {
		t.Fatal("pollPackagingCommissions missing")
	}
	if strings.Contains(poll, "/wait|clarif|needs_input/") {
		t.Error("the waiting state must not be sniffed out of the status string — that vocabulary is unreachable")
	}
	if !strings.Contains(poll, "next.briefComplete !== true && Boolean(next.waitingOn)") {
		t.Errorf("stillWaiting must be derived from the payload's own briefComplete/waitingOn:\n%s", poll)
	}
	if strings.Contains(poll, "briefComplete: false") {
		t.Error("the poll must never force briefComplete:false — the server view owns that field")
	}
	// the server view still wins on the way in, and a no-longer-waiting
	// commission still drops its stale local questions
	for _, want := range []string{"waitingOn: null", "questions: []"} {
		if !strings.Contains(poll, want) {
			t.Errorf("poll missing %q", want)
		}
	}
	// an unchanged 200 must not rebuild the hub every 15 s
	if !strings.Contains(poll, "JSON.stringify(before) !== JSON.stringify(merged)") {
		t.Errorf("only a real difference may set changed:\n%s", poll)
	}
	if strings.Contains(poll, "rememberPackagingCommission(next, optimistic\n") && strings.Contains(poll, "            changed = true") {
		t.Error("changed must be conditional on a real diff, not set for every successful fetch")
	}
}

// A background re-render (the 15 s commission poll, the 30 s list fallback,
// os_events) must not replace the detail subtree while an inline rename input
// is live: Chrome fires no blur for a removed focused node, so the typed title
// would be lost with no commit and no toast.
func TestIndexPackagingBackgroundRenderNeverEatsALiveRename(t *testing.T) {
	html := readIndexForWave11Fixes(t)
	render := functionBody(html, "function renderStudioProjects() {")
	if render == "" {
		t.Fatal("renderStudioProjects missing")
	}
	guard := strings.Index(render, `!unavailableSelected && studioProjectDetail.dataset.workId === selectedStudioProjectId && studioProjectDetail.querySelector('.studio-project-detail__title-input, [data-work-review-dirty="true"]')`)
	if guard < 0 {
		t.Fatalf("no live-rename guard in the detail render:\n%s", render)
	}
	detail := strings.Index(render, "renderStudioProjectDetail(projects.find(")
	if detail < 0 || guard > detail {
		t.Error("the rename guard must come before the detail pane is rebuilt")
	}
	// the class name the guard keys on is the one the rename actually mounts
	if !strings.Contains(functionBody(html, "function beginStudioProjectInlineRename(project) {"), "input.className = 'studio-project-detail__title-input'") {
		t.Error("the inline rename must mount .studio-project-detail__title-input, which the render guard keys on")
	}
}

// bfMenu binds a PERMANENT click listener to whatever trigger it is handed.
// The row kebab owns its own toggle, and the project picker is rebuilt on every
// invocation, so neither may let bfMenu bind the trigger as well.
func TestIndexPackagingMenusOwnTheirTriggerExactlyOnce(t *testing.T) {
	html := readIndexForWave11Fixes(t)
	kebab := functionBody(html, "function studioProjectRowMenu(project) {")
	if !strings.Contains(kebab, "bindTrigger: false") {
		t.Errorf("the row kebab toggles the menu itself — bfMenu must not bind the trigger too:\n%s", kebab)
	}
	if !strings.Contains(kebab, "controller?.toggle()") {
		t.Error("the kebab handler stays the single owner of the toggle")
	}
	picker := wave11FixesBody(html, "async function chooseArtifactProject(artifactId, trigger, options = {}) {")
	if picker == "" {
		t.Fatal("chooseArtifactProject missing")
	}
	if !strings.Contains(picker, "bindTrigger: false") {
		t.Errorf("a per-invocation menu must not stack a listener on the trigger:\n%s", picker)
	}
	if !strings.Contains(picker, "destroy()") {
		t.Error("the project menu must destroy itself on close so it neither leaks a node nor keeps a listener")
	}
}

// Story outlines are minted with source "story_studio", which both
// studioProjectCandidate and studioLegacyProjectCandidate reject, so they never
// appear in /api/studio-projects/v1. The hub must read them from their own
// route or every story door is dead after a reload.
func TestIndexPackagingStoriesComeFromTheStoriesRoute(t *testing.T) {
	html := readIndexForWave11Fixes(t)
	loader := wave11FixesBody(html, "async function loadPackagingStories(force = false) {")
	if loader == "" {
		t.Fatal("loadPackagingStories missing — outlines cannot come from the studio-project list")
	}
	for _, want := range []string{
		"fetch('/assistant/packaging/stories', { cache: 'no-store' })",
		"Array.isArray(payload?.stories) ? payload.stories : []",
		"syncPackagingStoryThreads()",
		"syncPackagingStoryBar()",
	} {
		if !strings.Contains(loader, want) {
			t.Errorf("loadPackagingStories missing %q", want)
		}
	}
	projects := functionBody(html, "function packagingStoryProjects() {")
	if strings.Contains(projects, "studioProjects") {
		t.Errorf("the outline list must not be filtered out of studioProjects — it is never in there:\n%s", projects)
	}
	if !strings.Contains(projects, "packagingStories()") {
		t.Errorf("packagingStoryProjects must source the loaded outlines:\n%s", projects)
	}
	sync := functionBody(html, "function syncPackagingStoryThreads() {")
	if !strings.Contains(sync, "packagingStories().forEach") {
		t.Errorf("outline threads must be registered from the loaded outlines:\n%s", sync)
	}
	// the Presentation Studio sheet's Story-outline select is built from real
	// outlines, and they are loaded before the sheet is assembled
	if !strings.Contains(html, "if (studioKind === 'presentation') await loadPackagingStories()") {
		t.Error("the presentation brief sheet must load outlines before it builds the Story outline select")
	}
	if !strings.Contains(html, "...stories.map(story => ({ id: String(story.id), label: String(story.title || 'Untitled outline') }))") {
		t.Error("the Story outline options must come from outline records, not studio project rows")
	}
	// the composer bar recovers after a reload
	bar := functionBody(html, "function syncPackagingStoryBar() {")
	if !strings.Contains(bar, "!packagingStoriesCache?.at) void loadPackagingStories()") {
		t.Errorf("the outline build bar must load outlines when none are registered yet:\n%s", bar)
	}
	// identity boundary: another account's outlines never survive
	reset := functionBody(html, "function resetStudioProjectsState() {")
	if !strings.Contains(reset, "packagingStoriesCache = { at: 0, stories: [] }") {
		t.Errorf("the outline cache is a per-identity projection:\n%s", reset)
	}
}

// The "Ask about a meeting" chip is consumed as a PREFIX on the next send, so
// it obeys the same one-conversation rule as Drive refs and the follow-up
// target: a meeting picked in a private thread must never prefix a message sent
// in the channel the reader switched to.
func TestIndexChatMeetingContextDoesNotRideAcrossAThreadSwitch(t *testing.T) {
	html := readIndexForWave11Fixes(t)
	clear := functionBody(html, "function clearPendingScoutContextRefsForThreadSwitch() {")
	if clear == "" {
		t.Fatal("clearPendingScoutContextRefsForThreadSwitch missing")
	}
	if !strings.Contains(clear, "pendingScoutMeetingContext = null") {
		t.Errorf("the meeting chip must be dropped on a thread switch:\n%s", clear)
	}
	// and the early return must not skip that when only the meeting chip is set
	if !strings.Contains(clear, "if (!hadRefs && !pendingScoutMeetingContext) return") {
		t.Errorf("an empty Drive-ref list must not short-circuit past the meeting chip:\n%s", clear)
	}
	// still called from the thread switch
	if !strings.Contains(html, "clearPendingScoutContextRefsForThreadSwitch()\n\t\t\t  // neither does an armed follow-up target") {
		t.Error("selectScoutChatThread must still clear per-conversation composer state")
	}
}

// A message row opened from the unified search can be clicked from any tab —
// selectScoutChatThread only re-renders the (hidden) chat pane, so without the
// destination hop the rail just vanished and nothing happened.
func TestIndexUnifiedSearchMessageRowHopsToConversations(t *testing.T) {
	html := readIndexForWave11Fixes(t)
	open := functionBody(html, "async function openChatSearchResult(result) {")
	if open == "" {
		t.Fatal("openChatSearchResult missing")
	}
	hop := strings.Index(open, "selectPD1Destination('Conversations', { mode: 'chat' })")
	pick := strings.Index(open, "selectScoutChatThread(threadId)")
	if hop < 0 {
		t.Errorf("openChatSearchResult must switch to Conversations first:\n%s", open)
	}
	if pick < 0 || hop > pick {
		t.Error("the destination hop must come before the thread selection")
	}
	if !strings.Contains(open, "setActiveTool('chat')") {
		t.Error("the hop needs the same setActiveTool fallback openScoutChatThread uses")
	}
}

// The stale-tail projection renders the OLD messages against the NEW updatedAt.
// Advancing the device-local seen marker there clobbers the unread seam before
// the render that finally shows the new messages.
func TestIndexChatStaleTailDoesNotAdvanceTheSeenMarker(t *testing.T) {
	html := readIndexForWave11Fixes(t)
	window := wave11FixesSlice(t, html, "const priorSeenAt = chatThreadPriorSeenAt(thread)", "syncChatConvoHeader()")
	if !strings.Contains(window, "thread.messagesStale !== true") {
		t.Errorf("markChatThreadSeen must be deferred while the tail is a stale projection:\n%s", window)
	}
	if !strings.Contains(window, "markChatThreadSeen(thread)") {
		t.Fatal("the seen marker write moved — re-check the stale-tail guard")
	}
	// the stale projection is still what makes the early return unreachable
	merge := functionBody(html, "function mergeScoutChatIndexRows(rows) {")
	if !strings.Contains(merge, "messagesStale: true") {
		t.Error("mergeScoutChatIndexRows must still mark the kept tail stale")
	}
}

// A generated image whose blob was swept by chat media retention gets the same
// honest placeholder the attachment lane shows — never a broken <img> over a
// dead blob with a live save/regenerate control.
func TestIndexChatExpiredImageRendersThePlaceholder(t *testing.T) {
	html := readIndexForWave11Fixes(t)
	node := functionBody(html, "function scoutChatImageNode(message) {")
	if node == "" {
		t.Fatal("scoutChatImageNode missing")
	}
	for _, want := range []string{"if (image.expired) {", "scout-chat-file--expired", "'expired · not saved to Drive'"} {
		if !strings.Contains(node, want) {
			t.Errorf("image renderer missing %q", want)
		}
	}
	expired := strings.Index(node, "if (image.expired) {")
	for _, live := range []string{
		"img.src = artifactBlobUrl(",
		"scoutChatImageSaveControl(image)",
		"attachScoutChatImageRegenerateControl(message, figure)",
	} {
		at := strings.Index(node, live)
		if at < 0 {
			t.Fatalf("image renderer no longer builds %q — re-check the expired short-circuit", live)
		}
		if expired > at {
			t.Errorf("the expired short-circuit must precede %q", live)
		}
	}
}

// The unified search rail: scopes settle out of order, the rail is a dialog
// with no focus trap, and the studio scope reuses a list the kind/project chips
// scope server-side.
func TestIndexUnifiedSearchRailCorrectness(t *testing.T) {
	html := readIndexForWave11Fixes(t)
	js := wave11FixesSlice(t, html, "/* ---------- Wave 11 D15: unified search (⌘K) ----------", "/* live chip — shows whenever you are in the room behind another tab */")

	// the highlight is identity-bound, not positional
	if !strings.Contains(js, "activeId: ''") {
		t.Error("globalSearch must carry the active row's id")
	}
	if !strings.Contains(js, "const restored = globalSearch.rows.findIndex(row => row.id === globalSearch.activeId)") {
		t.Error("the highlight must be restored by row id after every rebuild, so a late scope cannot move it under the caret")
	}
	if !strings.Contains(js, "globalSearch.activeId = String(rows[globalSearch.active]?.id || '')") {
		t.Error("moving the highlight must record which row it landed on")
	}
	if !strings.Contains(js, "globalSearch.activeId = ''") {
		t.Error("a new query / scope change must forget the previous highlight")
	}

	// re-opening resumes scopes whose fetch was aborted by the close
	open := wave11FixesBody(js, "function setGlobalSearchOpen(open, options = {}) {")
	if !strings.Contains(open, "!globalSearch.results.has(scope) && !globalSearch.pending.has(scope)") {
		t.Errorf("an aborted scope has neither a result nor a pending marker — re-opening must resume it, not claim \"nothing in <scope>\":\n%s", open)
	}
	if !strings.Contains(open, "void runGlobalSearch(resumeQuery)") {
		t.Error("the unfinished scopes must actually be re-run on the way back in")
	}

	// the studio scope may only reuse the loaded library when it is the whole
	// library — the chips scope loadStudioProjects server-side
	studio := functionBody(js, "async function globalSearchStudio(query, signal) {")
	if !strings.Contains(studio, "Boolean(studioProjectKindSpec(studioProjectFilter)) || Boolean(studioProjectProjectFilter)") {
		t.Errorf("a kind/project chip must force the unfiltered read:\n%s", studio)
	}
	if !strings.Contains(studio, "!scopedList && Array.isArray(studioProjects)") {
		t.Error("the in-memory list is only reusable when no chip is scoping it")
	}

	// escape works even after Tab walked focus out of the rail
	if !strings.Contains(js, "if (event.defaultPrevented || event.key !== 'Escape' || !globalSearchIsOpen()) return") {
		t.Error("Escape must be handled on the document, guarded by the rail being open")
	}
	railHandler := wave11FixesSlice(t, js, "globalSearchRail?.addEventListener('keydown', event => {", "// ⌘K / Ctrl+K from anywhere in the shell")
	if strings.Contains(railHandler, "event.key === 'Escape'") {
		t.Error("the rail-scoped keydown must no longer be the only Escape door")
	}
	if !strings.Contains(js, "globalSearchRail?.addEventListener('focusout', event => {") {
		t.Error("focus leaving the rail must close it — it has no focus trap")
	}

	// the live region does not re-announce an unchanged string
	if !strings.Contains(js, "if (!globalSearchStatusEl || globalSearchStatusEl.textContent === text) return") {
		t.Error("the status line must only be written when it actually changes")
	}
}

// The root-addressing rule reached the two HUB doors but not the two studio
// EDITOR doors: Document Studio and Deck Studio still handed the picker their
// own state.artifactId, which for a commission deliverable IS the ship_compile
// child that PATCH /artifacts/project refuses (artifact_projects_test.go pins
// that 404 for a goalParentId artifact). An editor has no hub row to read a
// root off, so it resolves one from the payload it already loaded —
// deck_editor.go and document_editor.go both publish artifact.goalId as
// goalId||goalParentId — which works on a cold load where the hub was never
// fetched.
func TestIndexStudioEditorProjectDoorAddressesTheGoalRoot(t *testing.T) {
	html := readIndexForWave11Fixes(t)
	resolver := functionBody(html, "function studioEditorProjectArtifactId(artifact, fallbackId) {")
	if resolver == "" {
		t.Fatal("studioEditorProjectArtifactId missing — a studio editor cannot tag a commission deliverable through its own child id")
	}
	if !strings.Contains(resolver, "artifact?.goalId") {
		t.Errorf("the root must come from the loaded payload's own goalId:\n%s", resolver)
	}
	if strings.Index(resolver, "artifact?.goalId") > strings.Index(resolver, "fallbackId") {
		t.Errorf("the goal root must win over the editor's own artifact id:\n%s", resolver)
	}
	if strings.Contains(resolver, "studioProjects") {
		t.Error("resolving through the hub list would fail on a cold load where studioProjects was never fetched")
	}
	// both editors resolve the root once, at open, off their own payload
	const carried = "projectArtifactId: studioEditorProjectArtifactId(payload?.artifact, artifactId),"
	if count := strings.Count(html, carried); count != 2 {
		t.Errorf("Document Studio and Deck Studio must both carry the resolved root (%q found %d times, want 2)", carried, count)
	}
	// ...and both project doors address it, never the raw deliverable id
	for _, want := range []string{
		`chooseArtifactProject(state.projectArtifactId || state.artifactId, event.target.closest('[data-doc-action="project"]'))`,
		`chooseArtifactProject(state.projectArtifactId || state.artifactId, event.target.closest('[data-action="project"]'))`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("studio editor project door missing %q", want)
		}
	}
	if strings.Contains(html, "chooseArtifactProject(state.artifactId,") {
		t.Error("no studio editor may hand the project picker its own state.artifactId — for a commission that is the ship_compile child")
	}
	// the deliverable verbs in the same menus are unchanged
	if !strings.Contains(html, "duplicateStudioArtifact(state.artifactId, titleInput.value.trim() || state.title") {
		t.Error("duplicate must still address the deliverable itself, not the goal root")
	}
}

// artifactProjectHandler answers an artifact it will not tag with a 404 whose
// body is {"error": …}; a server without the route answers net/http's own
// plain-text 404. Running the blanket packagingRouteMissing check first turned
// every per-artifact rejection into "Projects are not available on this server
// yet" — the feature reads as unbuilt and the real reason is lost.
func TestIndexSetArtifactProjectDoesNotReadA404AsAMissingRoute(t *testing.T) {
	html := readIndexForWave11Fixes(t)
	body := functionBody(html, "async function setArtifactProject(artifactId, name) {")
	if body == "" {
		t.Fatal("setArtifactProject missing")
	}
	if strings.Contains(body, "packagingRouteMissing(response)") {
		t.Errorf("the blanket route check must not swallow a per-artifact 404:\n%s", body)
	}
	if !strings.Contains(body, "response.status === 404 && !payload?.error") {
		t.Errorf("only a bodyless 404 may read as a missing route:\n%s", body)
	}
	if !strings.Contains(body, "'Projects are not available on this server yet.'") {
		t.Error("a 501/405 must still name the missing route")
	}
	if !strings.Contains(body, "throw new Error(payload?.error || 'The project could not be changed')") {
		t.Errorf("a rejected artifact must surface the server's own reason:\n%s", body)
	}
	// the payload is read before the verdict, so the JSON body can decide it
	if strings.Index(body, "const payload = await response.json()") > strings.Index(body, "response.status === 404") {
		t.Errorf("the body must be read before the route verdict:\n%s", body)
	}
}

// The expired-image short-circuit removed the figure the desktop hover-action
// "Regenerate image" mounts its editor on, but image.prompt survives expiry, so
// the menu item survived too — and clicking it found no
// .scout-chat-image[data-message-id] and returned with no editor and no toast.
// The menu now matches the inline lane: an expired render offers no image door.
func TestIndexExpiredChatImageOffersNoRegenerateDoor(t *testing.T) {
	html := readIndexForWave11Fixes(t)
	decorate := functionBody(html, "function decorateDesktopChatMessage(node, message, kind, authorLabel) {")
	if decorate == "" {
		t.Fatal("decorateDesktopChatMessage missing")
	}
	gate := "const generatedImage = String(message?.kind || '') === 'image' && message?.image?.expired !== true && String(message?.image?.prompt || '').trim()"
	if !strings.Contains(decorate, gate) {
		t.Errorf("the image-action gate must exclude an expired render — the surviving prompt is not enough:\n%s", decorate)
	}
	if !strings.Contains(decorate, "onRegenerate: generatedImage ? () => beginScoutChatImageRegenerate(message) : null") {
		t.Error("the regenerate door must stay bound to that gate")
	}
	// why it had to close: the door mounts on a node the expired branch never builds
	regenerate := functionBody(html, "function beginScoutChatImageRegenerate(message, figure) {")
	if !strings.Contains(regenerate, "querySelectorAll('.scout-chat-image')") {
		t.Errorf("the regenerate editor still mounts on the .scout-chat-image figure:\n%s", regenerate)
	}
	node := functionBody(html, "function scoutChatImageNode(message) {")
	expired := strings.Index(node, "if (image.expired) {")
	figure := strings.Index(node, "figure.dataset.messageId = String(message?.id || '')")
	if expired < 0 || figure < 0 {
		t.Fatal("scoutChatImageNode no longer short-circuits on expiry or no longer tags the figure")
	}
	if expired > figure {
		t.Error("the expired short-circuit must precede the figure — that is why the menu door has to close")
	}
}
