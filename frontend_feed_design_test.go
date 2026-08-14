package main

// frontend_feed_design_test.go — the AAA feed redesign pins (§0-§7): the run
// log container, the one-measure feed column, the goalcard hero (headline
// title, hairline trust footer, ink primary door), the ember sweep (ember =
// "machine working NOW", nothing else), and the §7 artifact stage that opens
// deliverables IN the chat instead of detouring to Intelligence.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForFeedDesign(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// One measure: the --feed-measure token exists and the machine cards center
// on it (runlog + goalcard), while the run log CSS block is present.
func TestIndexFeedMeasureAndRunlogStyles(t *testing.T) {
	html := readIndexForFeedDesign(t)
	for _, want := range []string{
		"--feed-measure: 680px;",
		".runlog {",
		".runlog__list::before {",
		".runlog__entry[data-live=\"1\"] .runlog__node {",
		"animation: goalcard-dot-breathe var(--pulse-cycle) var(--ease) infinite;",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing feed-measure/runlog style %q", want)
		}
	}
	// both centered machine surfaces ride the shared measure
	if strings.Count(html, "width: min(var(--feed-measure), 100%);") < 2 {
		t.Error("runlog and goalcard must both center on width: min(var(--feed-measure), 100%)")
	}
}

// §7 the artifact stage: chat-context opens route to openArtifactStage; the
// panel closes on Esc/scrim/✕ with focus return, respects reduced motion,
// reuses the sandboxed deck viewer, and keeps "open in intelligence" as the
// data-room escape hatch.
func TestIndexArtifactStageContract(t *testing.T) {
	html := readIndexForFeedDesign(t)
	for _, want := range []string{
		".artifact-stage {",
		".artifact-stage__panel {",
		".artifact-stage__scrim {",
		"@media (prefers-reduced-motion: reduce) {\n        .artifact-stage__panel { animation: none; }\n      }",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing artifact stage style %q", want)
		}
	}
	// the third parameter is the manifest card's present routing
	// (frontend_manifest_test.go pins it) — the stage contract is unchanged
	body := functionBody(html, "async function openArtifactStage(artifactId, fallbackTitle, options)")
	if body == "" {
		t.Fatal("could not extract openArtifactStage body")
	}
	for _, want := range []string{
		// An evolving room/work card can arrive before the newest-100 artifact
		// window. Opening must fetch the exact authorized artifact instead of
		// dumping the reader into an empty Intelligence library.
		"entry = await fetchArtifactEntryById(id)",
		// dispatch mirrors the read pane: sandboxed deck iframe, injection-safe
		// renderer, newest-pdf embed for text-less pdf payloads
		"artifactIsHTMLDeck(entry)",
		"renderArtifactDeck(body, entry",
		"renderArtifactRead(read, entry)",
		"embed.type = 'application/pdf'",
		"artifactBlobUrl(newest)",
		// the escape hatch to the data room
		"'open in intelligence'",
		"openAgentArtifact(entry)",
		// Esc + focus trap
		"if (event.key === 'Escape')",
		"closeArtifactStage()",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("openArtifactStage body missing %q", want)
		}
	}
	fetchBody := functionBody(html, "async function fetchArtifactEntryById(id)")
	for _, want := range []string{
		"fetch(`/artifacts?id=${encodeURIComponent(want)}`",
		"if (!response.ok) return null",
		"if (!artifact?.id || String(artifact.id) !== want) return null",
		"addArtifactEntry(artifact, { select: false })",
	} {
		if !strings.Contains(fetchBody, want) {
			t.Errorf("fetchArtifactEntryById missing exact authorized open behavior %q", want)
		}
	}
	closeBody := functionBody(html, "function closeArtifactStage()")
	if !strings.Contains(closeBody, "back.focus()") {
		t.Error("closeArtifactStage must return focus to the opener")
	}
}

// §4 the goalcard hero: headline title, the ink primary door ("open the
// deliverable" → the stage, not Intelligence), and the trust line rebuilt as
// a hairline footer with a numeric score span and a warn ASSUMED flag.
func TestIndexGoalcardHeroContract(t *testing.T) {
	html := readIndexForFeedDesign(t)
	terminal := functionBody(html, "function goalCardRenderTerminal(card, artifact, plan, state, prevState)")
	if terminal == "" {
		t.Fatal("could not extract goalCardRenderTerminal body")
	}
	for _, want := range []string{
		"'goalcard__link goalcard__link--primary', 'open the deliverable'",
		"openArtifactStage(artifact.id,",
		"goalcard__trust-score",
		"goalcard__trust-flag",
	} {
		if !strings.Contains(terminal, want) {
			t.Errorf("goalCardRenderTerminal missing hero marker %q", want)
		}
	}
	for _, want := range []string{
		".goalcard__link--primary {",
		".goalcard__trust-score {",
		".goalcard__trust-flag { color: var(--warn); }",
		"border-top: 1px solid var(--line-1);",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing goalcard hero style %q", want)
		}
	}
}

func TestIndexGoalcardFailureIsACompactRecoveryBrief(t *testing.T) {
	html := readIndexForFeedDesign(t)
	terminal := functionBody(html, "function goalCardRenderTerminal(card, artifact, plan, state, prevState)")
	for _, want := range []string{
		"goalcard__failure-copy",
		"Stopped before delivery",
		"goalcard__failure-activity",
		"Retry keeps completed stages and resumes at the blocker.",
		"view activity",
	} {
		if !strings.Contains(terminal, want) {
			t.Errorf("goalcard recovery brief missing %q", want)
		}
	}
	for _, want := range []string{
		`.goalcard[data-state="error"] {`,
		"width: min(860px, 100%);",
		".goalcard__failure {",
		".goalcard__failure-actions {",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("goalcard recovery styling missing %q", want)
		}
	}
}

// §6 the ember sweep: static feed chrome dropped --agent — review doors and
// stage links read ink; ember survives only on working-now signals.
func TestIndexEmberSweepStaticConsumers(t *testing.T) {
	html := readIndexForFeedDesign(t)
	if strings.Contains(html, ".goalcard__link--accent { border-color: var(--agent); color: var(--agent); }") {
		t.Error("goalcard__link--accent must not wear the ember — links are ink, ember means working NOW")
	}
	if !strings.Contains(html, ".goalcard__link--accent { border-color: var(--line-2); color: var(--text-1); }") {
		t.Error("goalcard__link--accent must re-ink to the default ink outline")
	}
	// the park line keeps its one earned ember dot
	if !strings.Contains(html, ".scout-chat-note--park::before {") {
		t.Error("missing the park-line ember dot")
	}
}

func TestDesktopLongConversationStaysAMessageAndUsesTheThreadInspector(t *testing.T) {
	html := readIndexForFeedDesign(t)
	messageBody := functionBody(html, "function scoutChatMessageNode(kind, text, ts, files, authorLabel, viaScout = false)")
	if messageBody == "" {
		t.Fatal("could not extract scoutChatMessageNode")
	}
	for _, forbidden := range []string{
		"length > 400",
		"a letter",
		"read the full letter",
		"collapse the letter",
	} {
		if strings.Contains(strings.ToLower(messageBody), strings.ToLower(forbidden)) {
			t.Errorf("ordinary message renderer still infers an artifact from length: %q", forbidden)
		}
	}
	if !strings.Contains(messageBody, "mountScoutChatMessageOverflow(item, body, stack)") {
		t.Error("ordinary message renderer must mount the rendered-line overflow treatment")
	}
	overflow := functionBody(html, "function mountScoutChatMessageOverflow(item, body, stack)")
	for _, want := range []string{
		"window.getComputedStyle(body).lineHeight",
		"body.scrollHeight > (lineHeight * 8) + 1",
		"'Show more'",
		"'Show less'",
		"aria-controls",
		"aria-expanded",
	} {
		if !strings.Contains(overflow, want) {
			t.Errorf("rendered-line overflow contract missing %q", want)
		}
	}
	if strings.Contains(html, "· a letter") || strings.Contains(html, "read the full letter") {
		t.Error("desktop conversation must not expose inferred letter copy")
	}
	for _, forbidden := range []string{
		"#chatTool .scout-chat-msg--longform {",
		".scout-chat-msg--longform .scout-chat-text {",
		".scout-chat-msg--longform .scout-chat-msg__stack {",
	} {
		if strings.Contains(html, forbidden) {
			t.Errorf("overflow must not change ordinary message geometry or skin: found %q", forbidden)
		}
	}
	if !strings.Contains(html, ".scout-chat-msg--user .scout-chat-msg__expand { align-self: flex-end; }") {
		t.Error("a user's Show more control must preserve the user's normal right alignment")
	}
	for _, want := range []string{
		".scout-chat-msg__expand::after {",
		`content: "\2193";`,
		`.scout-chat-msg__expand[aria-expanded="true"]::after { content: "\2191"; }`,
		"font: 600 12px/1 var(--font-sans);",
		"color: var(--text-1);",
		"border: 1px solid var(--line-2);",
		"background: var(--surface-2);",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("Show more must be a visible, directional affordance: missing %q", want)
		}
	}
	if strings.Contains(html, `#chatTool:has(#chatContextReplyForm:not([hidden])) #scoutChatForm`) {
		t.Error("opening the independent reply workstation must not hide the main-channel composer")
	}
	for _, want := range []string{
		`@media (min-width: 861px) and (max-width: 1599px)`,
		`#chatTool:has(#chatContextRail:not([hidden])) .chat-threads`,
		`@media (min-width: 861px) and (max-width: 1099px)`,
		`#chatTool:has(#chatContextRail:not([hidden])) .chat-conversation`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("thread inspector must arbitrate constrained desktop space: missing %q", want)
		}
	}
	decorate := functionBody(html, "function decorateDesktopChatMessage(node, message, kind, authorLabel)")
	for _, want := range []string{
		"openDesktopMessageContext(message, replyButton)",
		"desktopChatThreadSummaryNode(message, threadMessages)",
	} {
		if !strings.Contains(decorate, want) {
			t.Errorf("desktop reply/thread inspector contract missing %q", want)
		}
	}
	openContext := functionBody(html, "function openDesktopMessageContext(message, trigger)")
	for _, want := range []string{
		"const hasReplies = desktopChatReplyTopology(messages).repliesFor(root).length > 0",
		"renderDesktopMessageContext(thread, root, { scrollToBottom: hasReplies })",
	} {
		if !strings.Contains(openContext, want) {
			t.Errorf("new long-message threads must open at the parent beginning: missing %q", want)
		}
	}
	contextStart := strings.Index(html, "function renderDesktopMessageContext(thread, root, options = {})")
	contextEnd := -1
	if contextStart >= 0 {
		if relative := strings.Index(html[contextStart:], "function desktopChatThreadSummaryNode"); relative >= 0 {
			contextEnd = contextStart + relative
		}
	}
	if contextStart < 0 || contextEnd <= contextStart {
		t.Fatal("could not isolate renderDesktopMessageContext")
	}
	context := html[contextStart:contextEnd]
	for _, want := range []string{
		"desktopContextMessageCard(thread, resolvedRoot, { root: true })",
		"replies.forEach",
		"chatContextParent.replaceChildren(desktopContextMessageCard(thread, resolvedRoot, { root: true }))",
		"chatContextParent.hidden = false",
		"chatContextBody.replaceChildren(fragment)",
		"options.scrollToBottom === true || (options.scrollToBottom === undefined && wasNearBottom)",
	} {
		if !strings.Contains(context, want) {
			t.Errorf("thread inspector must retain full parent and replies: missing %q", want)
		}
	}
}
