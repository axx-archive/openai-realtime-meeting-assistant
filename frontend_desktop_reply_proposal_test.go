package main

import (
	"os"
	"strings"
	"testing"
)

func TestDesktopReplyContextMountsActionableProposalCard(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)
	contextCard := functionBodyAfterSignature(html, "function desktopContextMessageCard(thread, message, options = {})")
	if contextCard == "" {
		t.Fatal("desktopContextMessageCard not found")
	}

	for _, want := range []string{
		"const actionableProposal = !options.root",
		"String(message?.kind || '') === 'proposal'",
		"Boolean(message?.proposal)",
		"card.classList.add('chat-context-card--proposal')",
		"scoutProposalCardNode(message)",
	} {
		if !strings.Contains(contextCard, want) {
			t.Errorf("desktop reply proposal rendering missing %q", want)
		}
	}
	if strings.Contains(contextCard, "buildScoutProposalCardNode(message)") {
		t.Fatal("reply rail must reuse the cached proposal-card renderer, not build a duplicate action surface")
	}

	renderContext := functionBodyAfterSignature(html, "function renderDesktopMessageContext(thread, root, options = {})")
	if !strings.Contains(renderContext, "replies.forEach(reply => fragment.appendChild(desktopContextMessageCard(thread, reply)))") {
		t.Fatal("reply context no longer routes each reply through desktopContextMessageCard")
	}
	if !strings.Contains(html, "#chatTool .chat-context-card--proposal .scout-proposal-card") {
		t.Fatal("reply-rail proposal card is missing its scoped width treatment")
	}
}

func TestDesktopReplyProposalUsesExistingAcceptDismissWiring(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)
	proposal := functionBody(html, "function buildScoutProposalCardNode(message)")
	if proposal == "" {
		t.Fatal("buildScoutProposalCardNode not found")
	}
	for _, want := range []string{
		"scout-proposal-card__run",
		"run.addEventListener('click'",
		"postScoutProposalAction('accepted', proposal, message",
		"scout-proposal-card__escape",
		"escape.addEventListener('click'",
		"postScoutProposalAction('dismissed', proposal, message, {})",
	} {
		if !strings.Contains(proposal, want) {
			t.Errorf("shared proposal action wiring missing %q", want)
		}
	}
}
