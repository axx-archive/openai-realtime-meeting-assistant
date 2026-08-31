package main

import (
	"os"
	"strings"
	"testing"
)

func TestFrontendPrivateRiffKeepsPublicContextVisibleAndPublishesWithTwoExplicitScopes(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)
	for _, want := range []string{
		`id="chatConvoRiff"`,
		"function openPrivateRiff(",
		"function renderPrivateRiffContext(",
		"private-riff-checkpoint--conversation",
		"function renderPrivateRiffShare(",
		"function publishPrivateRiffConversation(",
		"function privateRiffReplyIsShareable(",
		"function privateRiffPacificTimestamp(",
		"timeZone: 'America/Los_Angeles'",
		"message?.activity?.version === 'stride-private-riff/v1'",
		"throughMessageId: through",
		"entryPoint",
		"Share all to source",
		"Share this reply to source",
		"Your Riff · private to you",
		"Riff privately with Scout…",
		`data-icon="riff"`,
		"mount(chatContextReplyForm, chatContextReplyInput, 'chat')",
		`.chat-context-reply__composer:has(> .stride-dictation-composer:not([data-dictation-state="idle"]))`,
		"Hidden chain-of-thought is not shown.",
		"Shared by ${message.publication.sharedBy || 'a teammate'} from a private riff",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing Private Riff contract %q", want)
		}
	}
	open := functionBody(html, "function openPrivateRiff(")
	if !strings.Contains(open, "sourceThreadId") || !strings.Contains(open, "const desktopRail = desktopChatLayoutQuery.matches") ||
		!strings.Contains(open, "select: !desktopRail") || !strings.Contains(open, "selectScoutChatThread(thread.id)") {
		t.Fatalf("Private Riff must preserve the public center pane on desktop and open the Riff conversation on narrow web: %s", open)
	}
	renderActive := functionBodyAfterSignature(html, "function renderActiveScoutThread(options = {})")
	if !strings.Contains(renderActive, "privateRiffCheckpointNode(thread)") {
		t.Fatalf("Private Riff conversation must retain its checkpoint controls on narrow web: %s", renderActive)
	}
	clearFeed := functionBody(html, "function clearScoutChatThreadNodes(")
	if !strings.Contains(clearFeed, ".private-riff-checkpoint--conversation") {
		t.Fatalf("Private Riff checkpoint must be removed on every feed rebuild so it cannot duplicate or cross threads: %s", clearFeed)
	}
	emptyState := functionBody(html, "function ensureScoutChatEmptyState(")
	if !strings.Contains(emptyState, "const isRiff = Boolean(thread?.riff)") ||
		!strings.Contains(emptyState, "Riff from this checkpoint") ||
		!strings.Contains(emptyState, "if (!isChannel && !isRiff) empty.after(buildScoutStarterRow())") {
		t.Fatalf("an empty Private Riff must explain the source-bound conversation without offering durable-work starters: %s", emptyState)
	}
	checkpoint := functionBody(html, "function privateRiffCheckpointNode(")
	if strings.Contains(checkpoint, "Update context") || strings.Contains(checkpoint, "will be included when you send") ||
		!strings.Contains(checkpoint, "Open source") || !strings.Contains(checkpoint, "Riff in Realtime") {
		t.Fatalf("Private Riff checkpoint must keep the compact source and Realtime actions without the retired freshness lecture: %s", checkpoint)
	}
	share := functionBody(html, "function renderPrivateRiffShare(")
	if strings.Count(share, "Share all to source") != 1 || strings.Count(share, "Share this reply to source") != 1 ||
		strings.Contains(share, "checkbox") || strings.Contains(share, "paragraph") {
		t.Fatalf("Private Riff share disclosure must offer exactly the two approved publication choices: %s", share)
	}
	publish := functionBody(html, "async function publishPrivateRiffConversation(")
	for _, want := range []string{
		"`/assistant/chat-threads/${encodeURIComponent(thread.id)}/riff-publish`",
		"{ operationId: attempt.operationId, scope",
		"scope === 'reply' ? { messageId: String(message.id) } : {}",
		"privateRiffPublishAttempts.get(key)",
		"episodeId",
	} {
		if !strings.Contains(publish, want) {
			t.Fatalf("Private Riff publication is missing %q: %s", want, publish)
		}
	}
	for _, forbidden := range []string{"riff-share-preview", "paragraphTokens", "Use in my message", "Share as Scout"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("Private Riff web still exposes the retired selective-share contract %q", forbidden)
		}
	}
	narrow := functionBody(html, "function privateRiffMessageCard(")
	if !strings.Contains(narrow, "onShareAll:") || !strings.Contains(narrow, "onShareReply:") ||
		!strings.Contains(narrow, "desktopContextMessageCard(thread, message") {
		t.Fatalf("Private Riff replies must retain both explicit publication controls in the shared responsive message card: %s", narrow)
	}
	realtime := functionBody(html, "async function startPrivateRiffRealtime(")
	if !strings.Contains(realtime, "startPrivateRealtimeVoiceConversation({ threadId })") || !strings.Contains(realtime, "current?.riff") || !strings.Contains(realtime, "stillVisible") {
		t.Fatalf("Private Riff Realtime must bind the exact still-visible Riff thread: %s", realtime)
	}
	if !strings.Contains(html, "const requestedThreadId = String(options?.threadId || '').trim()") ||
		!strings.Contains(html, "privateRealtimeVoiceThreadID = String(reconnect?.threadId || requestedThreadId || '')") {
		t.Fatal("the shared Realtime launcher must validate, consume, and retain an explicit threadId")
	}
	offer := functionBody(html, "async function exchangePrivateRealtimeOffer(")
	if !strings.Contains(offer, "threadId: expectedThreadId") || !strings.Contains(offer, "expectedThreadId && threadId !== expectedThreadId") {
		t.Fatalf("the Realtime offer must send and exact-match the requested Riff threadId: %s", offer)
	}
	for _, signature := range []string{
		"function renderPrivateRiffContext(",
		"function renderPrivateRiffShare(",
		"function privateRiffGuitarIcon(",
	} {
		body := functionBody(html, signature)
		if strings.Contains(body, "innerHTML") {
			t.Fatalf("%s inserts runtime Private Riff content through innerHTML", signature)
		}
	}
}

func TestFrontendPrivateRiffIsAChannelWorkspaceNotAPrivateChatRow(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)
	renderThreads := functionBody(html, "function renderChatAgentThreads(")
	if !strings.Contains(renderThreads, "!privateRiffThread(thread)") {
		t.Fatalf("ordinary private history must exclude channel Riff Spaces: %s", renderThreads)
	}
	classifier := functionBody(html, "function privateRiffThread(")
	if !strings.Contains(classifier, "conversationKind") || !strings.Contains(classifier, "channel_riff") {
		t.Fatalf("Riff list filtering must use the closed server-owned conversation kind: %s", classifier)
	}
	open := functionBody(html, "async function openPrivateRiff(")
	for _, want := range []string{
		"const entryPoint = exactMessage ? 'message' : 'resume'",
		"{ throughMessageId: through, entryPoint, agentId: '', operationId: attempt.operationId }",
	} {
		if !strings.Contains(open, want) {
			t.Fatalf("channel guitar and exact-message entry must have distinct episode semantics; missing %q: %s", want, open)
		}
	}
	if !strings.Contains(html, "Your Riff") || !strings.Contains(html, "Current pass") {
		t.Fatal("Riff Space UI must name the stable workspace and current episode")
	}
	history := functionBody(html, "function privateRiffHistoryNode(")
	if !strings.Contains(history, "riff.episodes") || !strings.Contains(history, "viewPrivateRiffEpisode") ||
		!strings.Contains(history, "legacyEpisodeCount") || !strings.Contains(history, "preserved as legacy history") {
		t.Fatalf("prior Riff episodes must remain inspectable without becoming private-chat rows: %s", history)
	}
	view := functionBody(html, "async function viewPrivateRiffEpisode(")
	if !strings.Contains(view, "scoutChatTailHydrationURL(thread.id, { episodeId })") || !strings.Contains(view, "readOnlyEpisode") {
		t.Fatalf("looking at an earlier pass must use the source-reauthorized read-only endpoint: %s", view)
	}
	resume := functionBody(html, "async function resumePrivateRiffEpisode(")
	for _, want := range []string{"entryPoint: 'resume'", "episodeId", "riff-resume"} {
		if !strings.Contains(resume, want) {
			t.Fatalf("resuming a prior pass must use the closed server episode contract; missing %q: %s", want, resume)
		}
	}
	if !strings.Contains(html, "Resume this pass") || !strings.Contains(functionBody(html, "function submitDesktopThreadReply("), "state.readOnlyEpisode") ||
		!strings.Contains(functionBody(html, "function sendScoutChat("), "privateRiffViewingEarlier") {
		t.Fatal("earlier-pass inspection must lock both Riff composers until explicit resume")
	}
}

func TestFrontendPrivateRiffHeaderEntryIsCompactAndKeepsItsHitTarget(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)
	for _, want := range []string{
		`--desktop-chat-header-gutter: clamp(18px, 1.5vw, 26px);`,
		`padding: 13px var(--desktop-chat-header-gutter) 12px;`,
		`padding: 0 12px 0 10px;`,
		`height: 44px;`,
		`transition-property: background-color, box-shadow, transform;`,
		`.chat-convo-head__riff:active { transform: scale(0.96); }`,
		`<span>Riff</span>`,
		`data-icon="riff"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("compact Private Riff header entry missing %q", want)
		}
	}
	if strings.Contains(html, `<span aria-hidden="true">↗</span> Riff privately`) {
		t.Fatal("Private Riff header entry must not use the oversized text-glyph treatment")
	}
}

func TestFrontendPrivateRiffUsesSemanticActivityInsteadOfReasoningTranscript(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)
	for _, want := range []string{
		"Refreshing the authorized channel and company context…",
		"Worked ${privateRiffElapsedLabel(message.activity.elapsedMs)}",
		"considered ${Number(message.activity.sourceCount || 0)} channel messages",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("semantic Private Riff activity missing %q", want)
		}
	}
	for _, forbidden := range []string{"raw reasoning", "chainOfThought", "chain_of_thought", "modelName", "reasoningEffort"} {
		if strings.Contains(functionBody(html, "function renderPrivateRiffContext("), forbidden) {
			t.Fatalf("Private Riff rail exposes forbidden process field %q", forbidden)
		}
	}
}
