package main

import (
	"os"
	"strings"
	"testing"
)

// Production bug (AJ, 2026-09-01): a manually created private thread rendered
// the previous thread's DOCUMENT card ("document · <title> · Open · Edit" with
// the research-brief preview) under the starters. Two seams:
//   1. the feed rebuild (clearScoutChatThreadNodes) enumerated top-level node
//      classes and left .scout-chat-document-result out, so the card survived
//      into the empty thread;
//   2. an armed follow-up target (Work hub "Ask for changes", a card's
//      feedback pill) rode across a thread switch when its threadId was empty.
// The armed follow-up now renders as a compact chip row above the composer
// ("Following up on · <title> · ×") whose title opens the deliverable, and a
// new thread / thread switch clears it like it clears Drive refs.

func readIndexHTMLForFollowUpChip(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(body)
}

func requireAllFollowUp(t *testing.T, haystack, where string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(haystack, want) {
			t.Errorf("%s is missing %q", where, want)
		}
	}
}

func TestIndexChatFollowUpChipMarkup(t *testing.T) {
	html := readIndexHTMLForFollowUpChip(t)
	// the chip row lives ABOVE the composer, outside the message log
	threadAt := strings.Index(html, `id="scoutChatThread"`)
	threadEnd := strings.Index(html[threadAt:], `id="scoutChatDestination"`)
	targetAt := strings.Index(html, `id="scoutFollowUpTarget" class="scout-followup-target" hidden`)
	if threadAt == -1 || threadEnd == -1 || targetAt == -1 || targetAt < threadAt+threadEnd {
		t.Fatal("the follow-up target row must sit after the message log, above the composer")
	}
	render := functionBody(html, "function renderScoutFollowUpTarget()")
	if render == "" {
		t.Fatal("could not extract renderScoutFollowUpTarget")
	}
	requireAllFollowUp(t, render, "renderScoutFollowUpTarget",
		"chip.className = 'scout-followup-chip scout-followup-chip--target'",
		"chip.dataset.artifactId = String(target.artifactId || '')",
		"eyebrow.className = 'scout-followup-chip__eyebrow'",
		"eyebrow.textContent = 'Following up on'",
		"sep.className = 'scout-followup-chip__sep'",
		"title.className = 'scout-followup-chip__title'",
		"if (target.artifactId) openArtifactStage(target.artifactId, targetName)",
		"clear.className = 'scout-followup-chip__clear'",
		"clear.setAttribute('aria-label', 'Clear follow-up target')",
		"chip.append(eyebrow, sep, title, clear)",
	)
	if strings.Contains(render, "follow-up →") {
		t.Error("the old arrow chip copy must be gone")
	}
	// the eyebrow is a machine-facing label → mono; the title is a real button
	requireAllFollowUp(t, html, "chip CSS",
		".scout-followup-chip__eyebrow {", "font: 500 10.5px var(--font-mono);",
		".scout-followup-chip__title {", ".scout-followup-chip__title:focus-visible {",
	)
	// arming never appends a node to the message log
	arm := functionBody(html, "function armScoutFollowUpTarget(artifactId, opts)")
	if arm == "" {
		t.Fatal("could not extract armScoutFollowUpTarget")
	}
	for _, forbidden := range []string{"appendScoutChatNode(", "scoutChatThread.appendChild(", "scoutChatThread.insertBefore("} {
		if strings.Contains(arm, forbidden) {
			t.Errorf("armScoutFollowUpTarget must not render into the message log: found %q", forbidden)
		}
	}
	requireAllFollowUp(t, arm, "armScoutFollowUpTarget", "renderScoutFollowUpTarget()", "threadId: activeScoutThreadId || ''")
}

func TestIndexChatEmptyThreadHasNoHeroCard(t *testing.T) {
	html := readIndexHTMLForFollowUpChip(t)
	// every top-level result card class is torn down on the feed rebuild —
	// the leak was exactly a class missing from this list
	clear := functionBody(html, "function clearScoutChatThreadNodes()")
	if clear == "" {
		t.Fatal("could not extract clearScoutChatThreadNodes")
	}
	requireAllFollowUp(t, clear, "clearScoutChatThreadNodes",
		".scout-chat-document-result", ".scout-chat-structured-result", ".scout-chat-deck-result", ".scout-chat-work-record", ".scout-chat-research",
	)
	// the empty state renders the invitation, one continuity row and the
	// starters — never an artifact/document card
	empty := functionBody(html, "function ensureScoutChatEmptyState()")
	if empty == "" {
		t.Fatal("could not extract ensureScoutChatEmptyState")
	}
	for _, forbidden := range []string{"scout-chat-document-result", "scoutMarkdownDocumentRefRecordNode(", "renderArtifactRead(", "scoutFollowUpTarget"} {
		if strings.Contains(empty, forbidden) {
			t.Errorf("ensureScoutChatEmptyState must not render a hero card: found %q", forbidden)
		}
	}
	// the document card's own renderer is the only place the DOCUMENT card
	// is built, and it is a feed node (cleared above), not a composer node
	requireAllFollowUp(t, html, "document card", "result.className = 'scout-chat-document-result'")
}

func TestIndexChatFollowUpClearedOnThreadSwitch(t *testing.T) {
	html := readIndexHTMLForFollowUpChip(t)
	clear := functionBody(html, "function clearScoutFollowUpTargetForThreadSwitch(nextThreadId)")
	if clear == "" {
		t.Fatal("could not extract clearScoutFollowUpTargetForThreadSwitch")
	}
	requireAllFollowUp(t, clear, "clearScoutFollowUpTargetForThreadSwitch", "scoutFollowUpTarget = null", "renderScoutFollowUpTarget()", "scoutFollowUpTarget.threadId === String(nextThreadId || '')) return")
	sel := functionBody(html, "function selectScoutChatThread(id)")
	if sel == "" {
		t.Fatal("could not extract selectScoutChatThread")
	}
	// the clear sits in the same id-changed branch as the Drive-ref clear
	branch := functionBody(sel, "if (nextThreadId !== activeScoutThreadId)")
	requireAllFollowUp(t, branch, "thread-switch branch",
		"clearPendingScoutContextRefsForThreadSwitch()",
		"clearScoutFollowUpTargetForThreadSwitch(nextThreadId)",
	)
	// a manually created new thread goes through the same seam
	fresh := functionBody(html, "async function startNewScoutThread()")
	requireAllFollowUp(t, fresh, "startNewScoutThread", "selectScoutChatThread('')", "createScoutChatThreadOnServer('Scout', 'private')")
	create := functionBody(html, "async function createScoutChatThreadOnServer(title, visibility)")
	requireAllFollowUp(t, create, "createScoutChatThreadOnServer", "if (thread?.id) selectScoutChatThread(thread.id)")
}
