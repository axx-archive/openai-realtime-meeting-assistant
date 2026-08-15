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
		"function refreshPrivateRiff(",
		"function renderPrivateRiffShare(",
		"function publishPrivateRiffConversation(",
		"function privateRiffReplyIsShareable(",
		"function privateRiffPacificTimestamp(",
		"timeZone: 'America/Los_Angeles'",
		"message?.activity?.version === 'stride-private-riff/v1'",
		"throughMessageId: through",
		"Share all to source",
		"Share this reply to source",
		"Private Riff · only you and Scout",
		`data-icon="guitar"`,
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
	renderActive := functionBody(html, "function renderActiveScoutThread(")
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
		!strings.Contains(emptyState, "!isChannel && !isRiff && !hasStarters") {
		t.Fatalf("an empty Private Riff must explain the source-bound conversation without offering durable-work starters: %s", emptyState)
	}
	refresh := functionBody(html, "async function refreshPrivateRiff(")
	if !strings.Contains(refresh, "!thread?.riff") || !strings.Contains(refresh, "renderActiveScoutThread()") {
		t.Fatalf("Private Riff checkpoint refresh must work in both the desktop rail and narrow conversation: %s", refresh)
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
	narrow := functionBody(html, "function scoutChatMessageRecordNode(")
	if !strings.Contains(narrow, "!desktopChatLayoutQuery.matches") || !strings.Contains(narrow, "privateRiffShareTriggerNode(activeThread, message)") {
		t.Fatalf("narrow web must retain full-screen Riff publication controls: %s", narrow)
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
		`<span>Private Riff</span>`,
		`data-icon="guitar"`,
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
		"Reviewing the frozen channel checkpoint…",
		"Worked ${privateRiffElapsedLabel(activity.elapsedMs)}",
		"considered ${Number(activity.sourceCount || 0)} channel messages",
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
