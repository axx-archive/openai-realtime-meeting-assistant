package main

import (
	"os"
	"strings"
	"testing"
)

func TestFrontendPrivateRiffKeepsPublicContextVisibleAndPublishesSelectively(t *testing.T) {
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
		"function publishPrivateRiffSelection(",
		"message?.activity?.version === 'stride-private-riff/v1'",
		"throughMessageId: through",
		"paragraphTokens",
		"Use in my message",
		"Share as Scout",
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
	publish := functionBody(html, "async function publishPrivateRiffSelection(")
	if !strings.Contains(publish, "destinationId !== String(activeScoutThreadId || '')") || !strings.Contains(publish, "await hydrateScoutChatThread(destinationId)") {
		t.Fatalf("Private Riff draft must navigate to the server-returned source before prefilling: %s", publish)
	}
	for _, signature := range []string{
		"function renderPrivateRiffContext(",
		"function renderPrivateRiffShare(",
	} {
		body := functionBody(html, signature)
		if strings.Contains(body, "innerHTML") {
			t.Fatalf("%s inserts runtime Private Riff content through innerHTML", signature)
		}
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
