package main

// Card 070 frontend pins: the private/public scope toggle is gone; the chat
// rail shows two always-visible labeled sections (channels · whole office /
// private · you + Scout), each with its own create +, and the composer carries
// a destination guard naming where the next message lands (hot for a public
// channel, calm for a private thread).

import (
	"os"
	"strings"
	"testing"
)

func readIndexForThreadNavSplit(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// The dual-purpose scope toggle is fully retired from markup and JS.
func TestIndexThreadNavScopeToggleGone(t *testing.T) {
	html := readIndexForThreadNavSplit(t)
	for _, gone := range []string{
		"chat-new-scope",
		`id="chatScopeChannel"`,
		`id="chatScopePrivate"`,
		"newChatThreadVisibility",
		"function setNewChatThreadVisibility",
		"syncChatScopeToSelectedThread",
	} {
		if strings.Contains(html, gone) {
			t.Errorf("index.html still carries retired scope-toggle marker %q", gone)
		}
	}
}

// Both sections render at once: each caption is present, unhidden, and headlines
// its audience; each has its own create affordance.
func TestIndexThreadNavBothSectionsAlwaysVisible(t *testing.T) {
	html := readIndexForThreadNavSplit(t)
	for _, want := range []string{
		`<span id="chatChannelsLabel" class="chat-threads__label">channels · whole office</span>`,
		`<span id="chatPrivateLabel" class="chat-threads__label">private · you + Scout</span>`,
		`id="chatNewChannel"`,
		`id="chatNewThread"`,
		".chat-threads__section-head {",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing both-sections marker %q", want)
		}
	}
	// the captions must no longer be display:none — the section-head style is
	// their new home and they carry no hidden attribute
	if strings.Contains(html, `id="chatChannelsLabel" class="chat-threads__label" hidden`) {
		t.Error("the channels caption must render permanently, not hidden behind a scope")
	}

	// the channels create + opens the inline glass name field; new-thread is
	// private-only now (no public branch in startNewScoutThread)
	if !strings.Contains(html, "chatNewChannel?.addEventListener('click', () => setChannelCreateOpen(true))") {
		t.Error("the channels + must open the inline channel-create form")
	}
	startBody := functionBody(html, "async function startNewScoutThread()")
	if startBody == "" {
		t.Fatal("could not extract startNewScoutThread body")
	}
	if strings.Contains(startBody, "setChannelCreateOpen(true)") {
		t.Error("startNewScoutThread must always create a private thread, never open channel creation")
	}
	if !strings.Contains(startBody, "createScoutChatThreadOnServer('Scout', 'private')") {
		t.Error("startNewScoutThread must create a private thread")
	}
	for _, want := range []string{
		"if (scoutNewThreadPromise) return scoutNewThreadPromise",
		"selectScoutChatThread('')",
		"scoutNewThreadPromise = operation",
	} {
		if !strings.Contains(startBody, want) {
			t.Errorf("new-thread intent is not fenced before persistence: missing %q", want)
		}
	}
	createBody := functionBody(html, "async function createScoutChatThreadOnServer(title, visibility)")
	for _, want := range []string{
		"upsertScoutChatThread(result.data.thread, { select: false })",
		"selectScoutChatThread(thread.id)",
	} {
		if !strings.Contains(createBody, want) {
			t.Errorf("persisted new thread does not use the canonical selection path: missing %q", want)
		}
	}
	sendBody := functionBody(html, "async function sendScoutChatViaOffice(text, files = [])")
	if !strings.Contains(sendBody, "if (scoutNewThreadPromise) await scoutNewThreadPromise") {
		t.Error("a fast Send can still race into the previously selected thread")
	}
}

// renderChatAgentThreads populates both lists on every pass with no scope gate.
func TestIndexThreadNavRendersBothLists(t *testing.T) {
	html := readIndexForThreadNavSplit(t)
	body := functionBody(html, "function renderChatAgentThreads()")
	if body == "" {
		t.Fatal("could not extract renderChatAgentThreads body")
	}
	for _, want := range []string{
		"const channels = scoutChatThreads.filter(thread => chatThreadIsChannel(thread))",
		"const privates = scoutChatThreads.filter(thread => !chatThreadIsChannel(thread) && !privateRiffThread(thread))",
		"chatDefaultThread.hidden = privates.length > 0",
		"chatChannelsEmpty.hidden = channels.length > 0",
		"chatChannelThreads?.replaceChildren(",
		"chatAgentThreads.replaceChildren(",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("renderChatAgentThreads body missing %q", want)
		}
	}
}

func TestIndexPrivateEmptyRowWorksWhileAChannelIsSelected(t *testing.T) {
	html := readIndexForThreadNavSplit(t)
	for _, want := range []string{
		"const hasPrivateThread = scoutChatThreads.some(thread => !chatThreadIsChannel(thread) && !privateRiffThread(thread))",
		"if (authedUser && !hasPrivateThread)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("private empty-state row is missing %q", want)
		}
	}
	if strings.Contains(html, "if (authedUser && !selectedScoutChatThread())") {
		t.Fatal("a selected public channel must not block creation of the first private Scout thread")
	}
}

// The composer destination guard exists, is synced when the active thread is
// (re)rendered, and names the public audience hot vs the private audience calm.
func TestIndexComposerDestinationGuard(t *testing.T) {
	html := readIndexForThreadNavSplit(t)
	if !strings.Contains(html, `id="scoutChatDestination"`) {
		t.Fatal("index.html missing the #scoutChatDestination pill")
	}
	if !strings.Contains(html, `aria-live="polite"`) {
		t.Error("the destination pill should announce changes politely")
	}
	if !strings.Contains(html, ".scout-chat-destination--channel {") {
		t.Error("index.html missing the hot channel-tint style for the destination pill")
	}

	renderBody := functionBody(html, "function renderScoutChatDestination()")
	if renderBody == "" {
		t.Fatal("could not extract renderScoutChatDestination body")
	}
	for _, want := range []string{
		"everyone in the office",
		"project members only",
		"private to you",
		"scout-chat-destination--channel",
		"setAttribute('aria-label'",
	} {
		if !strings.Contains(renderBody, want) {
			t.Errorf("renderScoutChatDestination body missing %q", want)
		}
	}
	if !strings.Contains(renderBody, "const isTeam = chatThreadIsTeam(thread)") {
		t.Error("composer destination must distinguish the whole-office Table from a member-restricted project")
	}

	// the active-thread render pass must keep the guard in lockstep
	activeBody := functionBody(html, "function renderActiveScoutThread()")
	if !strings.Contains(activeBody, "renderScoutChatDestination()") {
		t.Error("renderActiveScoutThread must sync the destination guard")
	}
	headBody := functionBody(html, "function syncChatConvoHeader()")
	if !strings.Contains(headBody, "renderScoutChatDestination()") {
		t.Error("syncChatConvoHeader must sync the destination guard")
	}
}

func TestIndexAgentMessagesGroupByStampedIdentityNotSharedScoutRole(t *testing.T) {
	html := readIndexForThreadNavSplit(t)
	body := functionBody(html, "function scoutChatMessageRecordNode(message)")
	if body == "" {
		t.Fatal("could not extract scoutChatMessageRecordNode body")
	}
	if !strings.Contains(body, "authorEmail || authorName.toLowerCase() || kind") {
		t.Fatal("agent-authored messages without a human email must group by stamped author name")
	}
	if strings.Contains(body, "const followKey = `${kind}|${authorEmail || kind}`") {
		t.Fatal("shared scout-role grouping collapses distinct agent identities")
	}
}

func TestIndexCompletedWorkIsClearedBeforeThreadRerender(t *testing.T) {
	html := readIndexForThreadNavSplit(t)
	body := functionBody(html, "function clearScoutChatThreadNodes()")
	if !strings.Contains(body, ".scout-chat-work-record") {
		t.Fatal("governed completed-work cards must be cleared before a thread rerender to prevent duplicate results")
	}
}

func TestIndexCompletedWorkStaysInActivityAndFiles(t *testing.T) {
	html := readIndexForThreadNavSplit(t)
	body := functionBody(html, "function scoutChatMessageRecordNode(message)")
	if strings.Contains(body, "scoutChatWorkRecordNode(message)") {
		t.Fatal("generic completed work must not render as a center-timeline deliverable")
	}
	projection := functionBody(html, "function scoutChatRecordBelongsInTimeline(message)")
	for _, exactGate := range []string{"scoutActivityMessage(message)", "scoutThreadTimelineProjection(resultMessage).richResult"} {
		if !strings.Contains(projection, exactGate) {
			t.Fatalf("governed completed work is missing exact rich-result gate %q", exactGate)
		}
	}
	// The governed reader remains available to Activity/Files. Removing its
	// center-timeline promotion must not weaken its same-origin path checks.
	workBody := functionBody(html, "function scoutChatWorkRecordNode(message)")
	for _, expected := range []string{"workerName", "deliverable", "artifactHref", "evidenceHref", "providerExecutionFenced", "resultArtifactHref"} {
		if !strings.Contains(workBody, expected) {
			t.Fatalf("completed-work card is missing governed field %q", expected)
		}
	}
	for _, leaked := range []string{"scout-chat-work-record__progress", "work.currentStage", "work.progressPercent"} {
		if strings.Contains(workBody, leaked) {
			t.Fatalf("completed deliverable preview still exposes process chrome %q", leaked)
		}
	}
	if strings.Contains(workBody, "document.createElement('a')") || strings.Contains(workBody, ".href =") || strings.Contains(workBody, "Artifact:") {
		t.Fatal("completed-work card must not navigate to or expose a raw endpoint")
	}
	viewer := functionBody(html, "async function openGovernedWorkResource(href, work, resourceKind, returnFocus)")
	for _, expected := range []string{"fetch(path, { cache: 'no-store'", "payload?.artifact", "payload?.evidence", "Approved outcome", "Verified source", "Source remains verified"} {
		if !strings.Contains(viewer, expected) {
			t.Fatalf("structured completed-work viewer is missing %q", expected)
		}
	}
}

func TestCompletedWorkThreadPreviewUsesClosedStatusCopy(t *testing.T) {
	workMessage := scoutChatMessageRecord{
		Kind: "work_result",
		Text: "Artifact: /api/stride/v1/work/runs/raw-internal-path/artifact",
		Work: &scoutChatWorkRecordRef{Title: "Launch brief", Summary: "Evidence-linked launch brief is ready.", Status: "completed"},
	}
	thread := scoutChatThreadRecord{Messages: []scoutChatMessageRecord{workMessage}}
	preview := scoutChatThreadPreview(thread)
	if preview != "Work · Delivered" {
		t.Fatalf("closed completed-work preview=%q", preview)
	}
	if strings.Contains(preview, "/api/") {
		t.Fatal("thread rail leaked an internal artifact path")
	}
	updateScoutChatThreadSummary(&thread, scoutChatMessageRecord{}, workMessage)
	if thread.Preview != "Work · Delivered" || strings.Contains(thread.Preview, "/api/") || strings.Contains(thread.Preview, "Evidence-linked") {
		t.Fatalf("committed completed-work preview=%q", thread.Preview)
	}
}
