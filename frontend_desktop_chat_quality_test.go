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
		"width: calc(100% - (var(--desktop-chat-gutter) * 2));",
		"--desktop-chat-image-preview-measure: 420px;",
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
		"const scope = !isChannel ? 'private' : isTeam ? 'pinned · Stride' : 'project channel'",
		"function chatThreadIsTeam(thread)",
		"return 'Bonfire Chat'",
		"chat-thread-item__title--bonfire-chat",
		"chat-thread-item__stride-tag",
		"Number(chatThreadIsTeam(right)) - Number(chatThreadIsTeam(left))",
		"const policy = !isChannel ? `only you + ${privateTarget}` : isTeam ? 'whole office · shared memory' : 'members · project memory'",
		"if (appShell?.dataset.tool === 'chat') syncToolTopbar()",
		"scoutPrivatePane.dataset.audience = isChannel ? 'channel' : 'private'",
		`#chatTool .chat-conversation[data-audience="private"] .scout-chat-form`,
		"? `Channel chat ${chatThreadDisplayTitle(thread)}`",
		"Message the team. Mention @Scout when you want help.",
		"'.scout-chat-empty, .scout-starters'",
		"function desktopChatUnreadBoundaryNode(count = 0)",
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
	// AJ: org-first shell 2026-09-02 — the desktop header carries no Stride
	// wordmark; the organization is named in the rail.
	if strings.Contains(html, `<span class="topbar__brand-wordmark" role="img" aria-label="Stride"></span>`) {
		t.Fatal("the desktop header must not carry the Stride wordmark (org-first shell)")
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
		"transform: scale(var(--press-scale));", // plan 011: one press token
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
		"optimisticState.textContent = 'not sent · draft restored'",
		"function updateDesktopChatReaction(messageId, emoji, set, options = {})",
		"method: intent.requested ? 'PUT' : 'DELETE'",
		"queueMicrotask(() => void flushDesktopChatReactionIntent(intent))",
		"openDesktopMessageContext(message, replyButton)",
		"submitDesktopThreadReply",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("desktop interaction state wiring missing %q", want)
		}
	}
}

func TestDesktopChatSendRenderIsOneScrollStableTransaction(t *testing.T) {
	html := desktopChatQualityHTML(t)
	render := functionBodyAfterSignature(html, "function renderActiveScoutThread(options = {})")
	if render == "" {
		t.Fatal("could not extract renderActiveScoutThread body")
	}
	for _, want := range []string{
		"withScoutChatRenderTransaction(() => {",
		"return withScoutChatRenderTransaction",
	} {
		if !strings.Contains(render, want) {
			t.Errorf("chat render must be wrapped in one synchronous transaction: missing %q", want)
		}
	}
	transaction := functionBodyAfterSignature(html, "function withScoutChatRenderTransaction(render, options = {})")
	if transaction == "" {
		t.Fatal("missing chat render transaction helper")
	}
	for _, want := range []string{
		"scoutChatRenderBatchDepth += 1",
		"scoutChatRenderBatchDepth -= 1",
		"captureScoutChatViewport(options)",
		"restoreScoutChatViewport(viewport)",
	} {
		if !strings.Contains(transaction, want) {
			t.Errorf("chat render transaction missing scroll-stability guard %q", want)
		}
	}
	appendNode := functionBodyAfterSignature(html, "function appendScoutChatNode(node, options = {})")
	if appendNode == "" || !strings.Contains(appendNode, "scoutChatRenderBatchDepth === 0 && scoutChatIsNearBottom()") {
		t.Fatal("chat node append must not force scrollTop during a batched render")
	}
	upsert := functionBodyAfterSignature(html, "function upsertScoutChatThread(thread, options = {})")
	for _, want := range []string{
		"const incomingUpdatedAt = String(thread.updatedAt || '')",
		"const currentUpdatedAt = String(current?.updatedAt || '')",
		"incomingRevision < currentRevision",
		"return current",
	} {
		if !strings.Contains(upsert, want) {
			t.Errorf("HTTP/socket reconciliation must reject stale thread snapshots: missing %q", want)
		}
	}
}

func TestDesktopThreadReplyKeepsDraftAndOmitsRedundantEmptyState(t *testing.T) {
	html := desktopChatQualityHTML(t)
	if strings.Contains(html, `#chatTool:has(#chatContextReplyForm:not([hidden])) #scoutChatForm`) {
		t.Fatal("opening a side reply thread still hides the independent main-channel composer")
	}
	for _, want := range []string{
		`<form id="scoutChatForm" class="scout-chat-form">`,
		`<form id="chatContextReplyForm" class="chat-context-reply" hidden>`,
		`scoutChatForm.addEventListener('submit', sendScoutChatFromForm)`,
		`chatContextReplyForm?.addEventListener('submit', submitDesktopThreadReply)`,
		`replyToMessageId: state.rootMessageId`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("desktop dual-composer contract missing %q", want)
		}
	}
	if strings.Contains(html, "No replies yet. Start the side conversation here.") {
		t.Fatal("desktop thread rail still renders the redundant empty-state instruction")
	}
	if strings.Contains(html, "· reply without losing your place") {
		t.Fatal("desktop thread rail still renders the redundant header instruction")
	}
	if strings.Contains(html, `class="chat-context-reply__label"`) {
		t.Fatal("desktop thread composer still renders a redundant visible label")
	}
	if !strings.Contains(html, `placeholder="Message the thread…" aria-label="Message the thread"`) {
		t.Fatal("desktop thread composer does not mirror the channel composer language")
	}
	for _, want := range []string{
		"operationId: replyAttempt.operationId",
		"Service briefly unavailable — your reply is still here. Check the thread, then try again.",
		"Reply status could not be confirmed — your text is still here. Refresh the thread before retrying.",
		"chatContextReplySend.disabled = false",
		"const hasReplies = desktopChatReplyTopology(messages).repliesFor(root).length > 0",
		"renderDesktopMessageContext(thread, root, { scrollToBottom: hasReplies })",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("desktop thread reply recovery missing %q", want)
		}
	}
}

func TestDesktopThreadRepliesExposeOwnedEditAndDeleteActions(t *testing.T) {
	html := desktopChatQualityHTML(t)
	css := desktopChatQualitySection(t, html)
	for _, want := range []string{
		"function desktopChatMessageIsOwn(thread, message)",
		"function beginDesktopContextMessageEdit(thread, message, card)",
		"function deleteDesktopContextMessage(thread, message, control)",
		"function desktopChatMoreMenuControl({ label = 'More actions', onEdit, onProject, onDelete, onRegenerate } = {})",
		"label: 'More reply actions'",
		"reply.setAttribute('aria-label', 'Reply in this thread')",
		"chatContextReplyInput?.focus()",
		"onEdit: isOwn ? () => beginDesktopContextMessageEdit(thread, message, card) : null",
		"onDelete: isOwn ? remove => deleteDesktopContextMessage(thread, message, remove) : null",
		"menu.setAttribute('role', 'menu')",
		"pressTimer = window.setTimeout(() => {",
		"if (moreControl) moreControl.open()",
		"method: 'PATCH'",
		"method: 'DELETE'",
		"body: JSON.stringify({ text: input.value.trim() })",
		"if (!response.ok || !payload?.thread)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("desktop reply mutation wiring missing %q", want)
		}
	}
	for _, want := range []string{
		".chat-context-card__message-action",
		"min-width: 40px;",
		"min-height: 40px;",
		"transform: scale(var(--press-scale));", // plan 011: one press token
		"transition-property: color, background-color, transform;",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("desktop reply action styling missing %q", want)
		}
	}
	if strings.Contains(css, "transition: all") {
		t.Fatal("desktop reply actions must not use transition: all")
	}
}

func TestDesktopEditedMetadataNeverPushesReactionPillsOffTheBubble(t *testing.T) {
	html := desktopChatQualityHTML(t)
	css := desktopChatQualitySection(t, html)
	for _, want := range []string{
		"#chatTool .desktop-chat-state",
		"position: absolute;",
		"#chatTool .scout-chat-msg:hover .desktop-chat-state",
		"#chatTool .scout-chat-msg[data-delivery] .desktop-chat-state",
		"margin-top: -12px;",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("stable edited/reaction layout missing %q", want)
		}
	}
}

func TestDesktopThreadReplyRailMatchesFeedMediaAndComposerCapabilities(t *testing.T) {
	html := desktopChatQualityHTML(t)
	css := desktopChatQualitySection(t, html)
	card := functionBodyAfterSignature(html, "function desktopContextMessageCard(thread, message, options = {})")
	for _, want := range []string{
		`id="chatContextReplyFileInput" type="file" multiple hidden`,
		`id="chatContextReplyAttach"`,
		`id="chatContextReplyPending"`,
		"function addPendingDesktopReplyFiles()",
		"pendingDesktopReplyFiles.push(await scoutChatFilePayload(file))",
		"JSON.stringify({ text, files, replyToMessageId: state.rootMessageId, operationId: replyAttempt.operationId, ...(projectContextToken ? { projectContextToken } : {}) })",
		"up to 6 files per reply",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("desktop reply composer parity missing %q", want)
		}
	}
	for _, want := range []string{
		"scoutChatFilesNode(files)",
		"appendChatRichTextNodes(body, rawText)",
		"appendChatMentionTextNodes(body, rawText)",
		"mountDesktopChatLinkPreview(content, rawText)",
		"attachDesktopContextMessageActions(thread, message, card, options)",
		"sourceRow.setAttribute('aria-label', 'Reply sources')",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("desktop reply rendering parity missing %q", want)
		}
	}
	interactions := functionBodyAfterSignature(html, "function attachDesktopContextMessageActions(thread, message, card, options = {})")
	if !strings.Contains(interactions, "desktopContextReactionRowNode(message)") {
		t.Error("desktop reply interactions no longer project the durable reaction row")
	}
	for _, want := range []string{
		".chat-context-card--message .scout-chat-files",
		".chat-context-card--message .desktop-chat-link-preview",
		".chat-context-card__reaction",
		".chat-context-card__content.desktop-chat-stack--link-only .chat-context-card__body",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("desktop reply media styling missing %q", want)
		}
	}
}

func TestDesktopMessageOwnershipActionsUseContextualMoreMenu(t *testing.T) {
	html := desktopChatQualityHTML(t)
	css := desktopChatQualitySection(t, html)
	for _, want := range []string{
		"function desktopChatMoreMenuControl({ label = 'More actions', onEdit, onProject, onDelete, onRegenerate } = {})",
		"trigger.setAttribute('aria-haspopup', 'menu')",
		"menu.setAttribute('role', 'menu')",
		"Edit message",
		"Delete message…",
		"label: generatedImage ? 'More image actions' : 'More message actions'",
		"onRegenerate: generatedImage ? () => beginScoutChatImageRegenerate(message) : null",
		"label: 'More reply actions'",
		"pressTimer = window.setTimeout(() => {",
		"if (moreControl) moreControl.open()",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("contextual ownership action model missing %q", want)
		}
	}
	for _, want := range []string{
		".desktop-chat-more__menu",
		".chat-context-card--message:hover .chat-context-card__message-actions",
		".chat-context-card--message.show-actions .chat-context-card__message-actions",
		"#chatTool .scout-chat-msg > .scout-chat-msg__delete",
		"display: none;",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("contextual ownership action styling missing %q", want)
		}
	}
}

func TestDesktopReplyMutationsDoNotRebuildMainFeed(t *testing.T) {
	html := desktopChatQualityHTML(t)
	sync := functionBodyAfterSignature(html, "function syncDesktopReplySurfaces(thread, rootMessageId, options = {})")
	if sync == "" {
		t.Fatal("syncDesktopReplySurfaces not found")
	}
	for _, want := range []string{
		"rootNode = scoutChatThread?.querySelector",
		"current.replaceWith(next)",
		"renderDesktopMessageContext(thread, root",
		"scrollRailToBottom",
	} {
		if !strings.Contains(sync, want) {
			t.Errorf("targeted reply surface sync missing %q", want)
		}
	}
	if strings.Contains(sync, "renderActiveScoutThread()") {
		t.Fatal("targeted reply sync must never rebuild the main feed")
	}

	for signature, wants := range map[string][]string{
		"function submitDesktopThreadReply(event)": {
			"syncDesktopReplySurfaces(merged, sourceRootMessageId",
		},
		"function beginDesktopContextMessageEdit(thread, message, card)": {
			"syncDesktopReplySurfaces(merged, root?.id",
		},
		"function deleteDesktopContextMessage(thread, message, control)": {
			"syncDesktopReplySurfaces(payload.thread, root?.id",
		},
	} {
		body := functionBody(html, signature)
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s missing isolated sync %q", signature, want)
			}
		}
		if strings.Contains(body, "renderActiveScoutThread()") || strings.Contains(body, "openDesktopMessageContext(") {
			t.Errorf("%s still rebuilds or reopens the center/thread surfaces", signature)
		}
	}

	reaction := functionBodyAfterSignature(html, "function updateDesktopChatReaction(messageId, emoji, set, options = {})")
	for _, want := range []string{
		"desktopChatReactionIntents.set(key, intent)",
		"patchDesktopChatReactionSurfaces(threadId, messageId)",
		"flushDesktopChatReactionIntent(intent)",
	} {
		if !strings.Contains(reaction, want) {
			t.Errorf("reply reaction isolation missing %q", want)
		}
	}
	for signature, wants := range map[string][]string{
		"function handleChatThreadEvent(payload)": {
			"desktopChatLayoutQuery.matches && message?.replyTo?.messageId",
			"syncDesktopReplySurfaces(candidate, root?.id || message.replyTo.messageId, { skipRail: patched })",
		},
		"function removeScoutChatThreadMessage(threadId, messageId)": {
			"desktopChatLayoutQuery.matches && replyRoot?.id",
			"syncDesktopReplySurfaces(thread, replyRoot.id)",
		},
	} {
		body := functionBody(html, signature)
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s missing live isolated reply update %q", signature, want)
			}
		}
	}
	renderRail := functionBodyAfterSignature(html, "function renderDesktopMessageContext(thread, root, options = {})")
	for _, want := range []string{"captureDesktopChatContextViewport(options)", "chatContextParent.replaceChildren", "restoreDesktopChatContextViewport(viewport)"} {
		if !strings.Contains(renderRail, want) {
			t.Errorf("reply rail scroll stability missing %q", want)
		}
	}
}

func TestDesktopThreadRepliesStayDiscoverableAndAvatarLed(t *testing.T) {
	html := desktopChatQualityHTML(t)
	css := desktopChatQualitySection(t, html)
	renderBody := functionBodyAfterSignature(html, "function renderActiveScoutThread(options = {})")
	for _, want := range []string{
		"const feedMessages = desktopChatLayoutQuery.matches",
		"messages.filter(message => !message?.replyTo?.messageId)",
		"projectedFeedMessages.forEach((message, messageIndex) =>",
	} {
		if !strings.Contains(renderBody, want) {
			t.Errorf("desktop root-only feed projection missing %q", want)
		}
	}
	for _, want := range []string{
		"function desktopChatReplyTopology(messages)",
		"const desktopChatReplyTopologyCache = new WeakMap()",
		"function renderDesktopMessageContext(thread, root, options = {})",
		"function desktopChatAvatarNode(message, className = 'chat-context-card__avatar')",
		"fetch('/assistant/chat-participants', { cache: 'no-store' })",
		"desktopChatParticipantDirectoryViewer !== viewer",
		"desktopChatParticipantDirectoryGeneration += 1",
		"desktopChatParticipantDirectoryPromise = null",
		"const requestViewer = viewer",
		"const requestGeneration = desktopChatParticipantDirectoryGeneration",
		"desktopChatParticipantDirectoryViewer !== requestViewer",
		"desktopChatParticipantDirectoryGeneration !== requestGeneration",
		"desktopChatParticipantDirectoryPromise === requestPromise",
		"summary.className = 'desktop-chat-thread-summary'",
		"openDesktopMessageContext(message, summary)",
		"openDesktopMessageContext(message, quote)",
		"if (chatContextState.mode === 'thread')",
		"const feedMessages = desktopChatLayoutQuery.matches",
		"messages.filter(message => !message?.replyTo?.messageId)",
		"projectedFeedMessages.forEach((message, messageIndex) =>",
		"openDesktopMessageContext(message, document.activeElement)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("desktop reply discovery contract missing %q", want)
		}
	}
	for _, want := range []string{
		"#chatTool .desktop-chat-thread-summary",
		"#chatTool .desktop-chat-thread-summary__avatars",
		"#chatTool .chat-context-card__avatar",
		"grid-template-columns: 34px minmax(0, 1fr);",
		"min-height: 40px;",
		"transform: scale(var(--press-scale));", // plan 011: one press token
		"font-variant-numeric: tabular-nums;",
		"#chatTool .scout-chat-msg__stack",
		// Wave 2 moved the reply surfaces onto the shared glass-chrome tier; the
		// material marker replaces the per-surface filter line.
		"/* material: .glass-chrome tier */",
		"radial-gradient(circle at 18% 0%",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("desktop reply visual contract missing %q", want)
		}
	}
	eventBody := functionBody(html, "function handleChatThreadEvent(payload)")
	if strings.Contains(eventBody, "messages.push(message)") || strings.Contains(eventBody, "messages[messageIndex] = message") {
		t.Fatal("live chat events must replace the messages array so cached reply topology cannot go stale")
	}
	for _, want := range []string{"candidate.messages = [...messages, message]", "candidate.messages = messages.map("} {
		if !strings.Contains(eventBody, want) {
			t.Errorf("live reply invalidation contract missing %q", want)
		}
	}
}

func TestDesktopParticipantDirectoryDiscardsOverlappingViewerResponse(t *testing.T) {
	html := desktopChatQualityHTML(t)
	body := functionBody(html, "function ensureDesktopChatParticipantDirectory()")
	for _, want := range []string{
		"desktopChatParticipantDirectoryGeneration += 1",
		"desktopChatParticipantDirectoryPromise = null",
		"const requestViewer = viewer",
		"const requestGeneration = desktopChatParticipantDirectoryGeneration",
		"desktopChatParticipantDirectoryViewer !== requestViewer",
		"desktopChatParticipantDirectoryGeneration !== requestGeneration",
		"desktopChatParticipantDirectoryPromise === requestPromise",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("participant-directory account-race guard missing %q", want)
		}
	}
	staleGuard := strings.Index(body, "desktopChatParticipantDirectoryViewer !== requestViewer")
	populate := strings.Index(body, "desktopChatParticipantDirectory.set(email, participant)")
	if staleGuard < 0 || populate < 0 || staleGuard > populate {
		t.Fatal("a stale viewer response must be rejected before it can populate the avatar directory")
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
		"delivered the package; a follow-up check needs attention",
		"Delivered · Follow-up needs attention",
		"compactArtifactPreview(String(artifact?.text",
		"researchArtifactSources(artifact)",
		"desktopWorkFamily",
		"desktopSafeWorkNote",
		"desktopSaveToDriveControl(terminalResultArtifact",
		"artifactPdfControl(terminalResultArtifact",
		"'Regenerate'",
		"Terminal socket/poll updates must",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("desktop work context is missing live terminal reconciliation %q", want)
		}
	}
	context := functionBody(html, "function renderDesktopWorkContext(")
	for _, forbidden := range []string{"orchestratorModel", "reasoningEffort", "artifact ${ref.artifactId", "run ${ref.id"} {
		if strings.Contains(context, forbidden) {
			t.Fatalf("desktop work context exposes server-owned runtime detail %q", forbidden)
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

func TestDesktopNeedsAttentionUsesClosedDurableRecoveryCopy(t *testing.T) {
	html := desktopChatQualityHTML(t)
	reason := functionBody(html, "function desktopWorkDurableAttentionReason(")
	copy := functionBody(html, "function desktopWorkAttentionCopy(")
	context := functionBody(html, "function renderDesktopWorkContext(")
	terminal := functionBody(html, "function goalCardRenderTerminal(")
	research := functionBody(html, "function updateScoutChatResearchNode(")
	if reason == "" || copy == "" || context == "" || terminal == "" || research == "" {
		t.Fatal("could not isolate desktop needs-attention recovery functions")
	}
	for _, want := range []string{
		"plan?.blocker",
		"artifact?.metadata?.goalBlocker",
		"artifact?.metadata?.error",
		"ref?.attentionReason",
	} {
		if !strings.Contains(reason, want) {
			t.Errorf("durable attention reason omitted %q", want)
		}
	}
	for _, want := range []string{
		"research_scope_failed",
		"bounded comparative evidence lane",
		"authorized direct-evidence dimensions",
		"evidence_gate_failed",
		"The evidence gate stayed closed",
		"Retry will narrow the research scope",
	} {
		if !strings.Contains(copy, want) {
			t.Errorf("closed recovery copy omitted %q", want)
		}
	}
	if strings.Contains(copy, "value.includes('unavailable')") || strings.Contains(copy, "value.includes('timeout')") {
		t.Fatal("generic unavailable/timeout text must not be mislabeled as a research-provider outage")
	}
	for label, body := range map[string]string{
		"work context":  context,
		"goal terminal": terminal,
		"research card": research,
	} {
		if !strings.Contains(body, "desktopWorkDurableAttentionReason(") || !strings.Contains(body, "desktopWorkAttentionCopy(") {
			t.Errorf("%s does not render from the closed durable recovery mapper", label)
		}
	}
	if strings.Contains(terminal, "plan?.blocker ||") || strings.Contains(terminal, "m.blocker ||") {
		t.Fatal("goal recovery card still renders an internal blocker verbatim")
	}
}

// M6: both chat menus transition `display` with allow-discrete, so their
// hidden rules must also fade, shrink, and stop hit-testing immediately —
// otherwise the closed menu sits opaque for the fade-out and swallows the
// click meant for the message behind it.
func TestDesktopChatMenusFadeAndStopHitTestingOnClose(t *testing.T) {
	html := desktopChatQualityHTML(t)
	for _, selector := range []string{
		"#chatTool .desktop-chat-more__menu[hidden] {",
		"#chatTool .desktop-chat-reaction-picker__menu[hidden] {",
	} {
		start := strings.LastIndex(html, selector)
		if start < 0 {
			t.Fatalf("missing hidden rule %q", selector)
		}
		block := html[start:]
		block = block[:strings.Index(block, "}")]
		for _, want := range []string{"opacity: 0;", "transform: translateY(-4px) scale(0.96);", "pointer-events: none;"} {
			if !strings.Contains(block, want) {
				t.Errorf("%s missing exit target %q", selector, want)
			}
		}
	}
}
