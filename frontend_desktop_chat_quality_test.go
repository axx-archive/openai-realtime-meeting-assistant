package main

import (
	"os"
	"strings"
	"testing"
)

func desktopChatQualityHTML(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func desktopChatQualitySection(t *testing.T, html string) string {
	t.Helper()
	start := strings.Index(html, "/* E4 desktop chat quality slice.")
	end := strings.Index(html[start:], "/* ---------- Room chat")
	if start < 0 || end < 0 {
		t.Fatal("desktop chat quality CSS section is missing")
	}
	return html[start : start+end]
}

func TestDesktopChatQualityIsDesktopIsolatedAndResponsive(t *testing.T) {
	html := desktopChatQualityHTML(t)
	css := desktopChatQualitySection(t, html)
	for _, want := range []string{
		"@media (min-width: 861px)",
		"@media (min-width: 1280px)",
		"@media (min-width: 1728px)",
		"@media (min-width: 861px) and (max-width: 1279px)",
		"--desktop-chat-measure: 760px;",
		"padding: 30px var(--desktop-chat-gutter);",
		"width: min(var(--desktop-chat-measure), calc(100% - (var(--desktop-chat-gutter) * 2)));",
		"@media (prefers-reduced-motion: reduce) and (min-width: 861px)",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("desktop chat CSS missing %q", want)
		}
	}
	if strings.Contains(css, "transition: all") || strings.Contains(css, "will-change: all") {
		t.Fatal("desktop chat CSS must transition only explicit properties and never use will-change: all")
	}
	if !strings.Contains(html, "const desktopChatLayoutQuery = window.matchMedia('(min-width: 861px)')") {
		t.Fatal("desktop chat JS must share the isolated desktop breakpoint")
	}
}

func TestDesktopChatHeaderAndMessageHierarchyWiring(t *testing.T) {
	html := desktopChatQualityHTML(t)
	for _, want := range []string{
		`id="chatConvoContext" class="desktop-chat-context"`,
		`id="chatConvoScope" class="desktop-chat-context__scope"`,
		`id="chatConvoPolicy" class="desktop-chat-context__policy"`,
		"const scope = !isChannel ? 'private' : isTeam ? 'pinned · #team' : 'project channel'",
		"const policy = !isChannel ? `only you + ${privateTarget}` : isTeam ? 'whole office · shared memory' : 'members · project memory'",
		"if (appShell?.dataset.tool === 'chat') syncToolTopbar()",
		"? `Channel chat ${chatThreadDisplayTitle(thread)}`",
		"start the conversation — @scout can help when you want it.",
		"scoutChatThread.querySelector('.scout-chat-brain-chip')",
		"'.scout-chat-empty, .scout-chat-brain-chip, .scout-starters'",
		"function desktopChatUnreadBoundaryNode()",
		"function decorateDesktopChatMessage(node, message, kind, authorLabel)",
		"message.replyTo",
		"message.reactions",
		"message.sources",
		"message.editedAt",
		"replyToMessageId",
		"closeDesktopChatContext({ restoreFocus: false })",
		"binding.handle.setAttribute('aria-valuemax', String(responsiveMax))",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("desktop conversation hierarchy missing %q", want)
		}
	}
}

func TestDesktopChatChromeKeepsOneUsefulConversationIdentity(t *testing.T) {
	html := desktopChatQualityHTML(t)
	css := desktopChatQualitySection(t, html)
	for _, want := range []string{
		`#appShell[data-tool="chat"] .topbar__heading`,
		`#appShell[data-tool="chat"] .topbar__subtitle`,
		`#chatTool .chat-convo-head__meta`,
		`#chatTool .desktop-chat-context__scope`,
		`#chatTool .desktop-chat-context__policy`,
		"min-height: 64px;",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("desktop chat chrome contract missing %q", want)
		}
	}
	if !strings.Contains(html, `<span class="wordmark topbar__wordmark" aria-hidden="true"></span>`) {
		t.Fatal("the desktop rail should be anchored by the Stride wordmark")
	}
}

func TestDesktopChatRichPreviewsStayOnAuthorizedSameOriginRoutes(t *testing.T) {
	html := desktopChatQualityHTML(t)
	start := strings.Index(html, "function mountDesktopChatLinkPreview(stack, text)")
	end := strings.Index(html[start:], "function renderDesktopChatLinkPreviewFallback(card, url)")
	if start < 0 || end < 0 {
		t.Fatal("could not isolate desktop link preview renderer")
	}
	preview := html[start : start+end]
	for _, want := range []string{
		"fetch(`/assistant/link-preview?url=${encodeURIComponent(url)}`",
		"credentials: 'same-origin'",
		"imageURL.startsWith('/assistant/link-preview/image?')",
		"card.rel = 'noreferrer noopener'",
		"desktopChatPreviewCache",
		"is-loading",
		"is-text-only",
		"is-degraded",
		`const previewKind = String(preview?.kind || 'link').toLowerCase()`,
		`card.dataset.kind = previewKind`,
		"stack.closest('.scout-chat-msg')?.classList.add('scout-chat-msg--media')",
	} {
		if !strings.Contains(preview, want) {
			t.Errorf("authorized link preview path missing %q", want)
		}
	}
	if strings.Contains(preview, "fetch(imageURL") || strings.Contains(preview, "src = preview") {
		t.Fatal("desktop renderer must not fetch or mount a provider-origin image URL")
	}

	css := desktopChatQualitySection(t, html)
	for _, want := range []string{
		"min-height: 168px;",
		"min-height: 96px;",
		"aspect-ratio: 16 / 10;",
		"aspect-ratio: 16 / 9;",
		"outline: 1px solid rgba(0, 0, 0, 0.1);",
		"outline-color: rgba(255, 255, 255, 0.1);",
		"object-fit: contain;",
		"#chatTool .scout-chat-msg--media",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("stable rich-media CSS missing %q", want)
		}
	}
}

func TestDesktopChatInteractionTargetsAndComposerStates(t *testing.T) {
	html := desktopChatQualityHTML(t)
	css := desktopChatQualitySection(t, html)
	for _, want := range []string{
		"min-width: 40px;",
		"min-height: 40px;",
		"transform: scale(0.96);",
		"max-height: 148px;",
		`.stride-dictation-composer[data-dictation-state="recording"] .scout-chat-input`,
		`.stride-dictation-composer:not([data-dictation-state="idle"]) > .scout-chat-send`,
		`.stride-dictation-mic:focus-visible`,
		".scout-chat-form:focus-within",
		".desktop-chat-actions button:focus-visible",
		".desktop-chat-link-preview:focus-visible",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("desktop interaction/composer CSS missing %q", want)
		}
	}
	for _, want := range []string{
		"optimisticMessage.dataset.delivery = 'sending'",
		"optimisticState.textContent = 'not sent'",
		"function updateDesktopChatReaction(messageId, emoji, set)",
		"method: set ? 'PUT' : 'DELETE'",
		"openDesktopMessageContext(message, replyButton)",
		"submitDesktopThreadReply",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("desktop interaction state wiring missing %q", want)
		}
	}
}

func TestDesktopThreadReplyKeepsDraftAndOmitsRedundantEmptyState(t *testing.T) {
	html := desktopChatQualityHTML(t)
	if strings.Contains(html, "No replies yet. Start the side conversation here.") {
		t.Fatal("desktop thread rail still renders the redundant empty-state instruction")
	}
	for _, want := range []string{
		"Connection interrupted — your reply is still here. Try again.",
		"Service briefly unavailable — your reply is still here. Check the thread, then try again.",
		"Reply status could not be confirmed — your text is still here. Refresh the thread before retrying.",
		"chatContextReplySend.disabled = false",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("desktop thread reply recovery missing %q", want)
		}
	}
}

func TestPendingAttachmentChipCannotOverflowComposer(t *testing.T) {
	html := desktopChatQualityHTML(t)
	for _, want := range []string{
		".scout-chat-pending-file__body",
		"width: min(360px, 100%);",
		"overflow: hidden;",
		"text-overflow: ellipsis;",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("pending attachment overflow guard missing %q", want)
		}
	}
	for _, want := range []string{
		"body.className = 'scout-chat-pending-file__body'",
		"name.title = file.name || 'file'",
		"meta.title = meta.textContent",
		"chip.append(body, remove)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("pending attachment truncation wiring missing %q", want)
		}
	}
}

func TestDesktopChatFileCardsExposeSafeTypeRevisionAndActions(t *testing.T) {
	html := desktopChatQualityHTML(t)
	start := strings.Index(html, "function scoutChatFilesNode(files)")
	end := strings.Index(html[start:], "function scoutChatFileSizeLabel(size)")
	if start < 0 || end < 0 {
		t.Fatal("could not isolate desktop file renderer")
	}
	files := html[start : start+end]
	for _, want := range []string{
		"scout-chat-file--gif",
		"scout-chat-file--pdf",
		"scout-chat-file__action",
		"open PDF",
		"revision ${file.sourceRevision}",
		"authorized source",
		"scoutChatBlobUrl(ref, file?.name)",
	} {
		if !strings.Contains(files, want) {
			t.Errorf("desktop file card missing %q", want)
		}
	}
}

func TestDesktopWorkContextReconcilesDurableTerminalState(t *testing.T) {
	html := desktopChatQualityHTML(t)
	for _, want := range []string{
		"function syncDesktopOpenChatContext()",
		"syncDesktopOpenChatContext()",
		"run stopped before delivery",
		"delivered this work and saved it durably",
		"Terminal socket/poll updates must",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("desktop work context is missing live terminal reconciliation %q", want)
		}
	}
	workerStart := strings.Index(html, "function artifactWorkerLabel(entry)")
	if workerStart < 0 {
		t.Fatal("could not isolate artifact worker label")
	}
	workerEnd := strings.Index(html[workerStart:], "function artifactIsHTMLDeck(entry)")
	if workerEnd < 0 {
		t.Fatal("could not isolate artifact worker label")
	}
	worker := html[workerStart : workerStart+workerEnd]
	for _, want := range []string{"metadata?.agentName", "metadata?.agentRole", "`${agentName} · ${agentRole}`"} {
		if !strings.Contains(worker, want) {
			t.Fatalf("named coworker attribution missing %q", want)
		}
	}
}
