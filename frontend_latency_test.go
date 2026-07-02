package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestIndexUsesSyncedStableWebRTCVideoSettings(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		"width: { ideal: 1280, max: 1280 }",
		"maxBitrate: 1500000",
		"groupMaxBitrate: 800000",
		"groupMaxWidth: 960",
		"crowdedMaxBitrate: 450000",
		"crowdedMaxWidth: 640",
		"constrainedMaxBitrate: 250000",
		"constrainedMaxWidth: 480",
		"function useGroupVideoLimits()",
		"function useCrowdedVideoLimits()",
		"return currentRoomParticipantCount() >= 5",
		"function retuneLocalCameraCapture()",
		"configureOutboundSenders().catch(error => {",
		"function startMediaQualityMonitor(sessionPeer)",
		"function constrainCameraForLag(reason)",
		"const sustainedLag = mediaQualityLagSamples >= 2",
		"if (isMobileDevice) {",
		"Reduced mobile sender bandwidth without restarting camera capture",
		"screenShareMaxBitrate: 2500000",
		"parameters.degradationPreference = isScreenShare",
		": 'maintain-framerate'",
		"function remoteVideoStreamForTrack(stream, videoTrack)",
		"function mediaStreamTrackSignature(stream)",
		"function videoPlaybackStreamForElement(video, stream)",
		"return new MediaStream(stream.getTracks().filter(liveTrack))",
		"function primaryVideoElementForParticipant(name)",
		"if (safariBrowser && isMobileDevice)",
		"primaryVideo && primaryVideo !== video && videoElementHasLiveVideoTrack(primaryVideo)",
		"function syncedRemotePlaybackStream(stream, audioTrack, preferredVideoTrack)",
		"function promoteRemotePlaybackToVideo(key, video, stream, name)",
		"function demoteRemotePlaybackFromVideo(key, name)",
		"function disposeRemoteTile(tile)",
		"attachAudioMonitor(key, name, event.track, { play: true, playbackStream: stream, playbackElement })",
		"video.dataset.remotePlayback !== 'synced'",
		"setTrackContentHint(track, cameraContentHint)",
		"setTrackContentHint(screenTrack, screenShareContentHint)",
		"function loadRTCConfiguration()",
		"fetch('/client-config', { cache: 'no-store' })",
		"pc = new RTCPeerConnection(rtcConfiguration)",
		"const forcedSafariMediaPath = new URLSearchParams(window.location.search).get('forceSafariMedia') === '1'",
		"const safariBrowser = forcedSafariMediaPath || /^((?!chrome|android|crios|fxios|edgios).)*safari/i.test(navigator.userAgent)",
		"function scheduleConnectionRecovery(sessionPeer)",
		"const connectionRecoveryDelayMs = 20000",
		"function requestIceRestart(reason)",
		"event: 'restart_ice'",
		"state === 'disconnected'",
		"function applyBrowserVideoCodecPreference(transceiver)",
		"RTCRtpSender.getCapabilities?.('video')",
		"const h264 = codecs.filter",
		"const preferred = h264.length ? h264 : vp8",
		"transceiver.setCodecPreferences([...preferred, ...rest])",
		"return false",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing stable synchronized media setting %q", want)
		}
	}

	for _, unwanted := range []string{
		"lowLatencyJitterBufferTargetMs",
		"jitterBufferTarget",
	} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("index.html still forces choppy receiver buffering via %q", unwanted)
		}
	}
}

func TestIndexProvidesRoomRecoveryAndAuthoritativeActiveSpeaker(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		"function openRoomWebSocket(options = {})",
		"function handleRoomWebSocketClose(event, options = {})",
		"function scheduleSignalingReconnect(reason, options = {})",
		"const signalingReconnectDelaysMs = [500, 1000, 2000, 4000, 8000, 12000]",
		"keeping your mic and camera while the room reconnects.",
		"await beginMediaSession({ voiceOnly, reuseLocalMedia: reconnectingSignal })",
		"const maxIceRestartAttempts = 5",
		"function performIceRestart(reason, attempt)",
		"setConnectionState('connecting', `reconnecting media ${attempt}/${maxIceRestartAttempts}`)",
		"case 'active_speaker':",
		"function handleAuthoritativeActiveSpeaker(payload)",
		"function authoritativeActiveSpeakerName()",
		"if (!serverSpeaker && visibleSpeakerName && loudestLevel > 0.045 && visibleSpeakerName !== activeSpeakerName)",
		"case 'server_shutdown':",
		"function handleServerShutdown(payload)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing room recovery or authoritative speaker behavior %q", want)
		}
	}
	if strings.Contains(html, "ws.onclose = () => leaveRoom()") {
		t.Fatal("unexpected websocket close should enter reconnect state, not immediately leave the room")
	}
}

func TestIndexKeepsWidescreenCaptureAndCalmRemoteTiles(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		// Every capture and retune path pins 16:9 so cameras never fall back
		// to square-ish 4:3 sensor modes that the cover-fit tiles crop.
		"const widescreenAspectRatio = { ideal: 16 / 9 }",
		"aspectRatio: widescreenAspectRatio",
		// A muted receiver track carries no frames; seating it as a placeholder
		// renders a black ghost tile that pops in and out of the room.
		"track.muted",
		"function watchRemoteVideoTrackStall(tile, track)",
		"watchRemoteVideoTrackStall(tile, track)",
		// A stalled remote track hides its frozen last frame behind the avatar.
		"tile.classList.add('is-video-stalled')",
		".video-tile.is-video-stalled video",
		// Repair must not recreate tiles for participants who already left.
		"participantDisplayNameInRoom(participantName)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing widescreen capture or calm remote tile hardening %q", want)
		}
	}

	retune := functionBody(html, "function retuneLocalCameraCapture()")
	if retune == "" {
		t.Fatal("missing retuneLocalCameraCapture helper")
	}
	if got := strings.Count(retune, "aspectRatio: widescreenAspectRatio"); got != 4 {
		t.Fatalf("retuneLocalCameraCapture should pin 16:9 in all four desktop quality tiers, found %d", got)
	}
	// Phones keep their native orientation: pinning a landscape aspect ratio and
	// re-applying capture constraints on every roster change made the mobile feed
	// flip between portrait and landscape for all participants. Capture pins are
	// gated behind cameraAspectRatioConstraint (undefined on mobile) and the
	// retune restart is skipped on mobile entirely.
	for _, want := range []string{
		"const isMobileDevice = /Mobi|Android|iPhone|iPad|iPod/i.test(navigator.userAgent)",
		"const cameraAspectRatioConstraint = isMobileDevice ? undefined : widescreenAspectRatio",
		"...(cameraAspectRatioConstraint ? { aspectRatio: cameraAspectRatioConstraint } : {})",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing mobile-aware capture guard %q", want)
		}
	}
	if !strings.Contains(retune, "if (isMobileDevice) {") {
		t.Fatal("retuneLocalCameraCapture must skip the capture restart on mobile to avoid orientation flips")
	}
}

func TestIndexProvidesAuthenticatedWaveformHomeAndFloatingAssistant(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		`<main id="appShell" data-tool="office">`,
		`data-tool="office" aria-label="Home" aria-pressed="true"`,
		`data-tool="room" aria-label="The room" aria-pressed="false"`,
		`data-tool="chat" aria-label="Chat" aria-pressed="false"`,
		`id="accountMenuButton" class="tool-rail__tool tool-rail__account-button" type="button" aria-haspopup="dialog" aria-expanded="false" aria-label="User settings"`,
		"let railHidden = false",
		"function loadRailHiddenPreference()",
		"window.localStorage?.removeItem('bonfire.rail.hidden.v1')",
		"function setRailHidden(hidden, options = {})",
		"setRailHidden(railHidden, { persist: false })",
		"setRailHidden(false, { persist: false })",
		`id="officeTool" class="office-tool"`,
		`class="office-launch__wave"`,
		`data-assistant-mode="workflow"`,
		`id="osAssistant" class="os-assistant"`,
		`.os-assistant__toggle[hidden]`,
		`id="osAssistantToggle" class="os-assistant__toggle" type="button" aria-label="Start Realtime 2 voice" title="Start Realtime 2 voice" aria-expanded="false" hidden`,
		"function signInToOffice()",
		"fetch('/assistant/query'",
		"fetch('/assistant/threads'",
		"fetch('/artifacts'",
		"const artifactAdminEmail = 'aj@shareability.com'",
		"const adminArtifactNodes = Array.from(document.querySelectorAll('[data-admin-artifacts]'))",
		"function canUseArtifactLibrary()",
		"function syncArtifactLibraryAccess()",
		`id="artifactsTool" class="artifacts-tool"`,
		`id="artifactDetailForm" class="artifact-detail"`,
		`id="artifactSearch" class="artifacts-search__input"`,
		`id="artifactEditButton"`,
		`.artifact-detail:not(.is-editing) .artifact-detail__body`,
		`.artifact-detail__actions [hidden]`,
		`id="artifactPublishButton"`,
		`id="researchTool" class="agent-tool" data-agent-tool="research"`,
		`class="research-workspace"`,
		`id="researchArtifactSearch" class="research-library__search-input"`,
		`id="researchArtifactList" class="research-library__list"`,
		`id="designTool" class="agent-tool" data-agent-tool="design"`,
		`id="grillTool" class="agent-tool" data-agent-tool="grill"`,
		`<span class="tool-rail__slot">
          <button class="tool-rail__tool" type="button" data-tool="artifacts" aria-label="Intelligence" aria-pressed="false">`,
		`<span class="tool-rail__label">intel</span>`,
		`<p class="scout-private-caption">private · voice and chat route Scout work here</p>`,
		`<div class="scout-work-starters" aria-label="Start Scout work">`,
		`data-scout-starter="research"`,
		`data-scout-starter="design"`,
		`data-scout-starter="grill"`,
		`data-scout-starter="workflow"`,
		"function renderArtifacts()",
		`id="chatAgentThreads" class="chat-agent-threads"`,
		"event: 'scout_chat_reset'",
		"function renderChatAgentThreads()",
		"function openAgentThreadInChat(entry)",
		"function promptScoutForWork(mode, text = '')",
		"function openAgentArtifact(entry)",
		"async function runAgentTool(event)",
		"function renderAgentWorkspaces()",
		"function renderResearchArtifactList(activeEntry)",
		"function renderResearchJobEntry(entry, activeEntry)",
		"function scoutThreadModeForText(text)",
		"function upsertScoutChatResearchNode(thread, options = {})",
		"function updateScoutChatResearchNode(card, status, artifact)",
		`class="scout-chat-research__flow"`,
		"data-research-download",
		"data-research-copy",
		"data-research-library",
		"renderArtifactRead(report, entry)",
		"data-agent-tool-form=\"research\"",
		"data-agent-tool-form=\"design\"",
		"data-agent-tool-form=\"grill\"",
		"function addArtifactEntry(entry, options = {})",
		"function renderArtifactDetail()",
		"function artifactMatchesQuery(entry, query)",
		"...Object.values(metadata)",
		"const ARTIFACT_SECTION_LABELS",
		"function appendArtifactBodyNodes(container, body)",
		"function appendArtifactInlineNodes(container, text)",
		"className = `artifact-item__status artifact-item__status--${artifactStatusValue(entry)}`",
		"async function saveSelectedArtifact(event)",
		"async function toggleSelectedArtifactPublished()",
		"function artifactPublished(entry)",
		"function latestPublishedArtifacts(limit = 3)",
		"function scheduleArtifactRefresh()",
		"if (!canUseArtifactLibrary())",
		"artifactButton.hidden = !hasArtifact",
		"syncScoutChatResearchCards()",
		"['queued', 'running'].includes(artifactStatusValue(entry))",
		"fetch('/assistant/chat-threads'",
		"function loadScoutChatThreads(options = {})",
		"function archiveScoutChatThread(id)",
		"chatAgentThreads.replaceChildren(...privates.map(thread => chatThreadRowNode(thread, q)))",
		"chatChannelThreads?.replaceChildren(...channels.map(thread => chatThreadRowNode(thread, q)))",
		"case 'chat_thread':",
		"function handleChatThreadEvent(payload)",
		"function syncChatThreadPolling()",
		`id="chatChannelThreads"`,
		`id="chatScopeChannel"`,
		"addArtifactEntry(result.artifact, { select: false })",
		"meeting artifact saved",
		"method: 'PATCH'",
		"voiceIslandMain.addEventListener('click', () => openOfficeTool('chat'))",
		"function shouldShowVoiceIsland()",
		"return (appShell.dataset.tool || 'office') !== 'office'",
		"let realtimeVoiceMode = 'idle'",
		"let roomEntryInProgress = false",
		"function privateRealtimeVoiceSurfaceAvailable()",
		"function privateRealtimeVoiceActive()",
		"function roomRealtimeVoiceActive()",
		"function roomMediaActive()",
		"function stopPrivateRealtimeVoiceForRoom()",
		"function assertPrivateRealtimeVoiceSession(sessionToken, cleanup)",
		"function handleOSAssistantActions(actions)",
		"handleOSAssistantActions(payload.actions)",
		"type === 'open_tool'",
		"type === 'select_artifact'",
		`id="osAssistantVoice" class="os-assistant__voice"`,
		"window.SpeechRecognition || window.webkitSpeechRecognition",
		"function toggleOSAssistantVoice()",
		"function startOSAssistantVoiceRecognition()",
		"function submitOSAssistantQuery(query, options = {})",
		"function syncOSAssistantAvailability()",
		"turn this into a goal workflow",
		"goal workflow",
		"#appShell.is-in-room ~ .os-assistant",
		"--shell-topbar-height: 0px;",
		`id="toolRail" class="tool-rail" aria-label="Tools"`,
		`id="brandMark" class="topbar__mark" role="img" aria-label="Bonfire"`,
		".tool-rail:hover,",
		".tool-rail__label",
		`id="accountMenuButton" class="tool-rail__tool tool-rail__account-button"`,
		`id="themeToggle" class="tool-rail__tool tool-rail__theme" type="button" aria-label="Switch theme" aria-pressed="false"`,
		`id="profileDisplayName" type="text" autocomplete="name"`,
		`id="profileAvatarInput" type="file" accept="image/png,image/jpeg,image/webp,image/gif" hidden`,
		"async function saveAccountProfile(event)",
		"postAuthJSON('/auth/profile'",
		`id="recordMeeting" class="btn btn--ghost btn--recording is-recording"`,
		"event: 'set_recording'",
		"function updateRoomRecordingControls()",
		"function roomListeningLabel()",
		"the room is not listening",
		"function applyRoomListeningStatus()",
		"setRoomListeningConnectionState()",
		"roomRecordingEnabled = recording.enabled !== false",
		".tool-rail__slot[hidden]",
		".tool-rail__avatar[hidden]",
		"display: none !important;",
		"const agentToolIds = ['research', 'design', 'grill']",
		"const TOOL_IDS = ['office', 'room', 'chat', 'artifacts', ...agentToolIds, 'board', 'memory']",
		`<span class="tool-rail__slot" hidden>
          <button class="tool-rail__tool" type="button" data-tool="research" aria-label="Research" aria-pressed="false">`,
		`<span class="tool-rail__slot" hidden>
          <button class="tool-rail__tool" type="button" data-tool="design" aria-label="Design" aria-pressed="false">`,
		`<button class="os-assistant__mode" type="button" data-assistant-mode="research" aria-pressed="false" hidden>research</button>`,
		`<button class="os-assistant__mode" type="button" data-assistant-mode="design" aria-pressed="false" hidden>design</button>`,
		`<button class="os-assistant__mode" type="button" data-assistant-mode="grill" aria-pressed="false" hidden>grill</button>`,
		`id="artifactReadPane" class="artifact-read" aria-label="artifact preview"`,
		"function artifactSections(entry)",
		"function renderArtifactRead(container, entry, options = {})",
		"function artifactPreviewText(entry)",
		"function renderDesignTool()",
		"data-design-context",
		"renderArtifactRead(output, entry, { surface: 'design' })",
		"item.addEventListener('click', () => openAgentArtifact(entry))",
		"Ask Scout in Chat or voice; research, design, grill, and plans land here as durable outputs.",
		"appShell.classList.toggle('is-authed', Boolean(authedUser))",
		"setActiveTool('office')",
		"stopRealtimeVoiceConversation({ notifyServer: false })",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing waveform home or assistant marker %q", want)
		}
	}
	for _, unwanted := range []string{
		`id="topbarRailToggle"`,
		`id="officeRailToggle"`,
		`id="mobileToolLauncher"`,
		`id="railAvatar"`,
		`class="rail-switch"`,
		`class="office-launch__nav-toggle"`,
		`class="topbar__nav-toggle"`,
		`class="is-rail-hidden"`,
		`class="is-mobile-tool-rail-open"`,
		"const railHiddenStorageKey",
		"window.localStorage?.getItem(railHiddenStorageKey)",
		"window.localStorage?.setItem(railHiddenStorageKey",
	} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("index.html still contains removed nav toggle marker %q", unwanted)
		}
	}

	if strings.Contains(html, `data-office-tool="dashboard"`) || strings.Contains(html, `id="dashboardTool"`) {
		t.Fatal("waveform home must not retain a separate dashboard route or CTA")
	}
	setActiveToolBody := functionBody(html, "function setActiveTool(tool)")
	if setActiveToolBody == "" {
		t.Fatal("index.html missing setActiveTool")
	}
	if strings.Contains(setActiveToolBody, "setRailHidden(true)") || strings.Contains(setActiveToolBody, "matchMedia?.('(max-width: 640px)')") {
		t.Fatal("tab navigation should not auto-hide a user-enabled nav bar")
	}
	for _, removedDashPrompt := range []string{
		"tap the waveform and tell Scout what you need",
		"Jump into the conference room",
		"Research which segment to launch first",
		"Summarize this morning's standup",
		"office-launch__hint",
		"office-launch__commands",
		"office-launch__command",
	} {
		if strings.Contains(html, removedDashPrompt) {
			t.Fatalf("waveform home should not render the old dashboard prompt cluster %q", removedDashPrompt)
		}
	}
	if !strings.Contains(html, `<span class="tool-rail__slot" hidden>`) {
		t.Fatal("non-prototype tools should be retained off-rail, not shown in the prototype rail")
	}
	for _, visibleTool := range []string{`data-tool="office"`, `data-tool="room"`, `data-tool="chat"`} {
		if !strings.Contains(html, visibleTool) {
			t.Fatalf("prototype rail tool %s should be present in the OS rail", visibleTool)
		}
	}
	for _, hiddenTool := range []string{`data-tool="artifacts"`, `data-tool="research"`, `data-tool="design"`, `data-tool="grill"`, `data-tool="board"`, `data-tool="memory"`} {
		if !strings.Contains(html, hiddenTool) {
			t.Fatalf("off-rail tool %s should remain addressable for assistant/tool routing", hiddenTool)
		}
	}
	openOfficeToolBody := functionBody(html, "function openOfficeTool(tool)")
	if openOfficeToolBody == "" {
		t.Fatal("index.html missing openOfficeTool")
	}
	if !strings.Contains(openOfficeToolBody, "promptScoutForWork(next)") {
		t.Fatal("agent-mode tool opens should route into Scout Chat instead of separate mode pages")
	}
	openOSAssistantBody := functionBody(html, "function openOSAssistant(mode)")
	if openOSAssistantBody == "" {
		t.Fatal("index.html missing openOSAssistant")
	}
	if !strings.Contains(openOSAssistantBody, "promptScoutForWork(requested)") || !strings.Contains(openOSAssistantBody, "setActiveTool('chat')") {
		t.Fatal("assistant mode opens should resolve to the Chat entry surface")
	}
	if !strings.Contains(openOSAssistantBody, "requested === 'artifacts'") || !strings.Contains(openOSAssistantBody, "setActiveTool('artifacts')") {
		t.Fatal("assistant artifact opens should resolve through the Intelligence (artifacts) route")
	}
	// Mission Intelligence: the artifacts tool id now routes every signed-in
	// user to the intel canvas — only the library SECTION stays admin-gated.
	if strings.Contains(setActiveToolBody, "canUseArtifactLibrary()") {
		t.Fatal("setActiveTool must not bounce non-admins off the intel tool")
	}
	applyToolStateBody := functionBody(html, "function applyToolState(tool)")
	if applyToolStateBody == "" {
		t.Fatal("index.html missing applyToolState")
	}
	if strings.Contains(applyToolStateBody, "tool = authedUser ? 'chat' : 'office'") {
		t.Fatal("applyToolState must not reroute non-admins away from the intel canvas")
	}
	if !strings.Contains(applyToolStateBody, "loadMissionIntelligence()") {
		t.Fatal("opening the intel tool must load the mission intelligence canvas")
	}
	syncLibraryBody := functionBody(html, "function syncArtifactLibraryAccess()")
	if strings.Contains(syncLibraryBody, "applyToolState(authedUser ? 'chat' : 'office')") {
		t.Fatal("syncArtifactLibraryAccess must gate only the library nodes, never kick users off the intel tool")
	}
	for _, want := range []string{
		`<section id="artifactsTool" class="artifacts-tool" aria-label="Mission Intelligence">`,
		`<div class="intel-canvas mount-stagger">`,
		`id="intelRefreshButton" class="btn btn--ghost"`,
		`id="intelPulseGrid" class="intel-tiles"`,
		`id="intelContribList" class="intel-contrib"`,
		`id="intelThemes" class="intel-col__body"`,
		`id="intelQuestions" class="intel-col__body"`,
		`id="intelAlignments" class="intel-col__body"`,
		`<section class="intel-section" data-admin-artifacts hidden aria-label="Artifact library">`,
		".intel-section[hidden]",
		"intelPulseLive.hidden = !(Number(pulse?.liveParticipants) > 0)",
		`id="intelLibraryToggle" class="btn btn--ghost" type="button" aria-expanded="false"`,
		`id="intelLibrary" class="artifacts-workspace" hidden`,
		"fuel for the brain — every word makes the company smarter",
		"scout needs an api key to synthesize themes",
		"async function loadMissionIntelligence(force = false)",
		"fetch('/assistant/mission', { cache: 'no-store' })",
		"fetch('/assistant/mission/refresh', { method: 'POST' })",
		"function renderMissionIntelligence()",
		"case 'mission_insight':",
		"themes are already fresh",
		"unattributed · ${unattributed} spoken",
		".intel-contrib__fill",
		"background: var(--accent);",
		".intel-live__dot { animation: none; }",
		`artifacts: 'Intelligence',`,
		"`${intelPulseTotal()} ingested · ${intelThemeCount()} themes`",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing Mission Intelligence marker %q", want)
		}
	}
	if strings.Contains(html, `body.textContent = 'Use artifact, research, design, or grill mode in the assistant`) {
		t.Fatal("artifact empty state should not teach separate assistant modes")
	}
	if strings.Contains(html, "openOfficeTool(mode === 'research' || mode === 'design' ? mode : 'artifacts')") {
		t.Fatal("agent thread cards should open Artifacts, not legacy Research/Design pages")
	}
	if !strings.Contains(html, `<span class="tool-rail__slot">
          <button id="toolBoard" class="tool-rail__tool" type="button" data-tool="board" aria-label="Board" aria-pressed="false">`) {
		t.Fatal("board rail slot should be visible and enabled; the expanded board gates editing, not entry")
	}
	if !strings.Contains(html, `<span class="tool-rail__slot">
          <button class="tool-rail__tool" type="button" data-tool="memory" aria-label="Memory" aria-pressed="false">`) {
		t.Fatal("memory rail slot should be visible so the memory browser is reachable")
	}
	if strings.Contains(html, "toolBoardButton.disabled") {
		t.Fatal("board rail entry must not be re-disabled by board readiness; the expanded surface owns its locked state")
	}
	if !strings.Contains(html, `id="themeToggle" class="tool-rail__tool tool-rail__theme" type="button" aria-label="Switch theme" aria-pressed="false"`) {
		t.Fatal("left rail theme toggle should be visible at the bottom of the rail")
	}
	if strings.Contains(html, `id="themeToggle" class="tool-rail__tool tool-rail__theme" type="button" aria-label="Switch theme" aria-pressed="false" hidden`) {
		t.Fatal("left rail theme toggle should not be hidden")
	}
	if !strings.Contains(html, `id="accountMenuButton" class="tool-rail__tool tool-rail__account-button" type="button" aria-haspopup="dialog" aria-expanded="false" aria-label="User settings"`) {
		t.Fatal("prototype rail should expose account settings at the bottom")
	}
	if strings.Contains(html, `class="tool-rail__flame"`) {
		t.Fatal("brand flame should live in the topbar, not inside the expanding rail")
	}
	if strings.Contains(html, `id="topbarAccountEmail"`) || strings.Contains(html, `class="topbar__account-email"`) {
		t.Fatal("topbar account trigger should show the display name and avatar without the email line")
	}

	for _, unwanted := range []string{
		"mode === 'authed' ? `Enter as",
		"renderLoginMode()\n          joinRoom()",
	} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("index.html still couples sign-in directly to room entry via %q", unwanted)
		}
	}
}

func TestIndexAccountMenuPreservesDraftNameDuringAvatarPreview(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	syncBody := functionBody(html, "function syncAccountChrome()")
	if syncBody == "" {
		t.Fatal("missing syncAccountChrome")
	}
	if strings.Contains(syncBody, "profileDisplayName.value") {
		t.Fatal("syncAccountChrome must not overwrite an unsaved typed display name during avatar preview/clear")
	}

	openMenuBody := functionBody(html, "function setAccountMenuOpen(open)")
	if strings.Contains(openMenuBody, "profileDisplayName.value") {
		t.Fatal("the popover no longer hosts the profile form; prefill belongs to prepareProfileForm for the settings window")
	}
	prepareBody := functionBody(html, "function prepareProfileForm()")
	if !strings.Contains(prepareBody, "profileDisplayName.value = authedUser ? authedUser.name || authedUser.email || '' : ''") {
		t.Fatal("settings window open should initialize the display-name form once via prepareProfileForm")
	}
	// functionBody would stop at the `{}` default parameter, so pin the exact open sequence instead
	if !strings.Contains(html, "settingsRestoreFocusEl = options.restoreFocusTo || audioSettingsButton\n        prepareProfileForm()") {
		t.Fatal("openSettings must prefill the profile form or the modal shows a stale display name")
	}
	saveBody := functionBody(html, "async function saveAccountProfile(event)")
	if !strings.Contains(saveBody, "profileDisplayName.value = authedUser.name || ''") {
		t.Fatal("successful profile save should reinitialize the form from the persisted identity")
	}
	if !strings.Contains(html, "profileAvatarDraft = ''") || !strings.Contains(html, "avatar cleared. save profile to keep it.") {
		t.Fatal("avatar clear should mark a draft clear without resetting the typed display name")
	}
}

func TestIndexLoginUsesOnlyRosterNamePicker(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	for _, want := range []string{
		`<span>name</span>`,
		`<option value="Joel">Joel</option>`,
		`<option value="Caitlyn">Caitlyn</option>`,
		`<option value="Tyler">Tyler</option>`,
		`<option value="AJ">AJ</option>`,
		`<option value="Tim">Tim</option>`,
		`<option value="Erick">Erick</option>`,
		`<option value="Tom">Tom</option>`,
		`name: selectedLoginAccountName()`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("login card missing roster-only marker %q", want)
		}
	}
	for _, unwanted := range []string{
		"Custom email",
		"Jake Mercer",
		"<option>Jake</option>",
		"<option>Guest 1</option>",
		"<option>Guest 2</option>",
		`email: loginEmailInput.value.trim()`,
	} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("login card still contains non-roster marker %q", unwanted)
		}
	}
}

func TestIndexAccountMenuAndFloatingRailInteractionsAreWired(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		`id="toolRail" class="tool-rail" aria-label="Tools"`,
		`id="brandMark" class="topbar__mark" role="img" aria-label="Bonfire"`,
		"top: 50%;",
		"left: max(16px, env(safe-area-inset-left));",
		"max-height: min(640px, calc(100svh - 48px));",
		"width: 64px;",
		"border-radius: calc(var(--r-2xl) + 4px);",
		"transform: translateY(-50%);",
		"display: none;",
		".tool-rail:hover,",
		".tool-rail:focus-within",
		"width: 64px;",
		"overflow: visible;",
		"left: calc(100% + 14px);",
		"max-width: none;",
		"pointer-events: none;",
		".tool-rail__tool:hover .tool-rail__label,",
		".tool-rail__tool:focus-visible .tool-rail__label",
		"transition-delay: 300ms;",
		"#appShell.is-authed .workspace",
		"padding-left: max(96px, calc(env(safe-area-inset-left) + 96px));",
		`<span class="tool-rail__label">office</span>`,
		`<span class="tool-rail__label">the room</span>`,
		`<span class="tool-rail__label">chat</span>`,
		`id="accountMenuButton" class="tool-rail__tool tool-rail__account-button" type="button" aria-haspopup="dialog" aria-expanded="false" aria-label="User settings"`,
		`class="tool-rail__account-icon"`,
		`aria-haspopup="dialog"`,
		`role="dialog" aria-label="Account menu"`,
		"accountMenuButton.addEventListener('click'",
		"setAccountMenuOpen(accountMenu.hidden)",
		"if (!accountMenu.hidden && !topbarAccount.contains(event.target) && !accountMenu.contains(event.target))",
		"if (!accountMenu.hidden) {\n            setAccountMenuOpen(false)",
		"openSettings({ section: 'profile', allowLocked: true, restoreFocusTo: accountMenuButton })",
		"accountMenuSignOut.addEventListener('click', signOutOfAccount)",
		`id="settingsRegion" class="settings-region" role="dialog" aria-modal="true" aria-labelledby="settingsTitle"`,
		`<nav class="settings-nav" aria-label="Settings sections">`,
		`data-settings-section="profile"`,
		`data-settings-section="account"`,
		`data-settings-section="devices"`,
		`data-settings-section="appearance"`,
		"audioSettingsButton.addEventListener('click', () => openSettings({ section: 'devices' }))",
		"settingsRegion.addEventListener('keydown', trapSettingsFocus)",
		"width: min(720px, calc(100vw - 48px));",
		"#appShell:has(#accountMenuButton[aria-expanded=\"true\"]) .tool-rail",
		"if (next && window.matchMedia('(max-width: 640px)').matches) {",
		"document.body.appendChild(accountMenu)",
		"topbarAccount.appendChild(accountMenu)",
		"width: min(340px, calc(100vw - 28px));",
		"position: fixed;",
		"inset: auto auto max(16px, env(safe-area-inset-bottom)) 50%;",
		"width: max-content;",
		"max-width: calc(100dvw - 24px);",
		"transform: translateX(-50%);",
		"backdrop-filter: blur(22px) saturate(1.45);",
		"#appShell.is-authed .workspace",
		"padding-bottom: max(84px, calc(var(--sp-2) + env(safe-area-inset-bottom)));",
		".tool-rail__theme svg",
		"transform: translate(-50%, -50%) scale(0.25);",
		"filter: blur(4px);",
		"bottom: calc(76px + env(safe-area-inset-bottom));",
		"#appShell.is-in-room:not(.is-board-expanded) ~ .account-menu",
		"bottom: calc(186px + env(safe-area-inset-bottom));",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing account menu or rail interaction marker %q", want)
		}
	}
	if strings.Contains(html, `class="tool-rail__flame"`) {
		t.Fatal("brand flame should stay anchored in the topbar above the rail")
	}
	if strings.Contains(html, `id="toolRail" class="tool-rail mount-stagger"`) {
		t.Fatal("tool rail must not use mount-stagger; its transform anchors desktop left-center and mobile bottom-island positioning")
	}
	if strings.Contains(html, `id="topbarAccountEmail"`) || strings.Contains(html, `class="topbar__account-email"`) {
		t.Fatal("topbar account trigger should not render an email line")
	}
	linkBlock := functionBody(html, ".account-menu__link")
	if !strings.Contains(linkBlock, "min-height: 44px;") {
		t.Fatal("account menu settings/sign-out rows must keep a 44px touch target")
	}
}

func TestToolRailFloatingIslandAnchorsStayViewportSafe(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		`id="toolRail" class="tool-rail" aria-label="Tools"`,
		"left: max(16px, env(safe-area-inset-left));",
		"transform: translateY(-50%);",
		"inset: auto auto max(16px, env(safe-area-inset-bottom)) 50%;",
		"left: 50%;",
		"width: max-content;",
		"max-width: calc(100dvw - 24px);",
		"transform: translateX(-50%);",
		"#appShell.is-in-room:not(.is-board-expanded) .tool-rail",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing floating rail viewport-safe anchor %q", want)
		}
	}
	if strings.Contains(html, `id="toolRail" class="tool-rail mount-stagger"`) {
		t.Fatal("tool rail must not use mount-stagger; mount transforms can override its centering anchors")
	}
	if !strings.Contains(html, "gutter (padding-left above) survives in-room.") {
		t.Fatal("in-room tool pages must document that the dock clearance folds away while the desktop rail gutter survives")
	}
	inRoomToolWorkspaceBlock := functionBody(html, "#appShell.is-in-room[data-tool=\"chat\"]:not(.is-board-expanded) .workspace")
	if !strings.Contains(inRoomToolWorkspaceBlock, "padding-bottom: 0;") {
		t.Fatal("in-room chat/memory/artifact tool pages must zero the dock clearance (the dock is hidden there)")
	}
	if strings.Contains(inRoomToolWorkspaceBlock, "padding: 0;") {
		t.Fatal("in-room tool pages must not reset the whole padding box; the desktop rail gutter (padding-left) must survive in-room")
	}
	desktopGutterBlock := functionBody(html, "@media (min-width: 641px) {")
	for _, want := range []string{
		"#appShell.is-authed[data-tool=\"chat\"] .workspace,",
		"#appShell.is-authed[data-tool=\"memory\"] .workspace,",
		"#appShell.is-authed[data-tool=\"artifacts\"] .workspace,",
		"#appShell.is-authed[data-tool=\"research\"] .workspace,",
		"#appShell.is-authed[data-tool=\"design\"] .workspace,",
		"#appShell.is-authed[data-tool=\"grill\"] .workspace {",
		"padding-left: max(96px, calc(env(safe-area-inset-left) + 96px));",
	} {
		if !strings.Contains(desktopGutterBlock, want) {
			t.Fatalf("desktop panel tool pages must restore the rail gutter; missing %q in the min-width 641px block", want)
		}
	}
	if strings.Contains(desktopGutterBlock, "#appShell.is-authed[data-tool=\"room\"] .workspace") || strings.Contains(desktopGutterBlock, "[data-tool=\"board\"] .workspace") {
		t.Fatal("the room and the expanded board stay full-bleed; the tool-page gutter override must not include them")
	}

	const phoneViewport = 390.0
	const railMaxWidth = phoneViewport - 24.0
	railLeft := (phoneViewport - railMaxWidth) / 2.0
	if railLeft < 12.0 {
		t.Fatalf("centered mobile rail loses safe edge clearance left=%v viewport=%v", railLeft, phoneViewport)
	}
}

func TestScoutChatRendersAssistantMarkdownAsRichText(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		".chat-rich",
		"let scoutChatThreads = []",
		"let pendingScoutFiles = []",
		"function appendChatRichTextNodes(container, rawText)",
		"function appendChatInlineNodes(container, text)",
		"function scoutChatFilePayload(file)",
		"function renderPendingScoutFiles()",
		"`/assistant/chat-threads/${encodeURIComponent(thread.id)}/messages`",
		"document.createElement('strong')",
		"document.createElement('code')",
		"document.createElement(ordered ? 'ol' : 'ul')",
		"document.createTextNode(value.slice(lastIndex))",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing safe chat rich-text marker %q", want)
		}
	}

	scoutBody := functionBody(html, "function scoutChatMessageNode(kind, text, ts, files, authorLabel)")
	if scoutBody == "" {
		t.Fatal("index.html missing scoutChatMessageNode")
	}
	for _, want := range []string{
		"const rich = kind === 'scout'",
		"body.className = `scout-chat-text${rich ? ' chat-rich' : ''}`",
		"appendChatRichTextNodes(body, text)",
		"body.textContent = text",
	} {
		if !strings.Contains(scoutBody, want) {
			t.Fatalf("scout chat body missing rich-rendering marker %q", want)
		}
	}
	if strings.Contains(scoutBody, "body.innerHTML") {
		t.Fatal("scout chat messages must not render model text with innerHTML")
	}

	assistantBody := functionBody(html, "function renderAssistantMessage(entry, entering = false)")
	if assistantBody == "" {
		t.Fatal("index.html missing renderAssistantMessage")
	}
	for _, want := range []string{
		"text.classList.add('chat-rich')",
		"appendChatRichTextNodes(text, messageText)",
		"text.textContent = messageText",
	} {
		if !strings.Contains(assistantBody, want) {
			t.Fatalf("assistant feed body missing rich-rendering marker %q", want)
		}
	}
	if strings.Contains(assistantBody, "text.innerHTML") {
		t.Fatal("assistant feed messages must not render model text with innerHTML")
	}
}

// TestMediaFixesBehaveCorrectly executes the shipped media helpers extracted from
// index.html against the exact bug + regression scenarios for the Safari flicker,
// mobile orientation-swap, and screen-share-for-all fixes. This is behavioral
// verification of the JS logic, not a string match. (Device-engine behavior — real
// Safari rVFC, a physical phone sensor, live getDisplayMedia — still needs a manual
// pass on hardware; this proves the fix logic is correct.)
func TestMediaFixesBehaveCorrectly(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not available for the media fix verification")
	}

	output, err := exec.Command(node, "scripts/media-fix-verification.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("media fix verification failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `"ok":true`) {
		t.Fatalf("media fix verification did not report ok: %s", output)
	}
}

func TestIndexKeepsScreenShareTrackAndFullscreenPresentation(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		"function activeScreenShareTrack()",
		"return liveTrack(track) ? track : null",
		"function outboundVideoTrack()",
		"return activeScreenShareTrack() || localVideoTrack()",
		"const screenShareSupported = Boolean(navigator.mediaDevices?.getDisplayMedia)",
		"function outboundTrackForKind(kind)",
		"return kind === 'video' ? outboundVideoTrack() : localAudioTrack()",
		"const track = outboundTrackForKind(section.kind)",
		".presentation-tile.is-screen-sharing {",
		"grid-template-columns: minmax(0, 1fr);",
		"gap: 0;",
		".presentation-tile.is-screen-sharing .hearth-stage {",
		"display: none;",
		".presentation-tile.is-screen-sharing .hearth-seat.is-sharing-screen",
		"function currentRoomLayout()",
		"return 'screen-share'",
		"video: screenStageVideo",
		"? screenShareStream",
		"function scheduleScreenShareMediaRepair(reason, participantName = '')",
		"scheduleScreenShareMediaRepair('screen share started'",
		"scheduleScreenShareMediaRepair('screen share stopped'",
		"screen_share_restore_camera",
		"async function restoreCameraAfterScreenShare(sender)",
		"await sender.replaceTrack(null)",
		"camera did not provide video after screen sharing",
		"function replaceLocalVideoTrack(nextTrack)",
		"ignoreCameraOff: true",
		"!target.ignoreCameraOff && participantMediaState(target.name).cameraOff",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing screen-share track or participant-strip guard %q", want)
		}
	}

	if strings.Contains(html, "grid-template-columns: minmax(0, 1fr) 200px;") {
		t.Fatal("screen sharing should be full-screen instead of reserving a participant strip column")
	}
}

func TestIndexDeduplicatesParticipantsAndPrunesStaleMedia(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		"function normalizeParticipantList(names)",
		"return normalizeParticipantList(names)",
		"participantsInRoom = normalizeParticipantList([...participantsInRoom, name])",
		"participantPreview = participantsInRoom",
		"activeScreenShareParticipant && !participantMediaState(activeScreenShareParticipant).screenSharing",
		"const nextKeys = new Set(participantsInRoom.map(name => name.toLowerCase()))",
		"removeRemoteParticipantMediaByName(name)",
		"case 'participant_left':",
		"function handleParticipantLeft(participant)",
		"function reconcileParticipantsInRoom(nextParticipants, options = {})",
		"function pruneRemoteMediaOutsideRoom()",
		"reconcileParticipantsInRoom(names, { announce: false })",
		"videoStack.querySelectorAll(':scope > .video-tile:not(.is-local):not(.video-drag-slot)')",
		"case 'session_replaced':",
		"case 'media_disconnected':",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing participant hardening %q", want)
		}
	}

	if strings.Contains(html, "['failed', 'closed', 'disconnected'].includes(state)") {
		t.Fatal("transient disconnected state should not immediately leave the room")
	}
}

func TestIndexSerializesRealtimeSignaling(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		"let signalChain = Promise.resolve()",
		"signalChain = signalChain",
		".then(() => handleSignal(message))",
		"await waitForStableSignaling()",
		"function waitForStableSignaling(timeoutMs = 2500)",
		"if (ws?.readyState === WebSocket.OPEN)",
		"offerId: message.offerId || ''",
		"revision: Number(message.revision) || 0",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing serialized signaling guard %q", want)
		}
	}
}

func TestIndexKeepsRemoteAudioTracksIndependent(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	if !strings.Contains(html, "return remoteSDPTrackIdForMid(event.transceiver?.mid) || event.track.id || reliableRemoteStreamIdForEvent(event)") {
		t.Fatal("remoteTrackKey should prefer per-track identifiers before stream ids")
	}

	videoCleanup := functionBody(html, "function removeRemoteParticipantVideoTracksByName")
	if videoCleanup == "" {
		t.Fatal("missing remote participant video cleanup helper")
	}
	if strings.Contains(videoCleanup, "audioMonitors") || strings.Contains(videoCleanup, "detachAudioMonitor") {
		t.Fatal("video cleanup should not remove participant audio monitors")
	}

	if !strings.Contains(html, "removeRemoteParticipantMediaByName(name)") {
		t.Fatal("participant rejoin should clear stale same-name media")
	}
	mediaCleanup := functionBody(html, "function removeRemoteParticipantMediaByName")
	if mediaCleanup == "" {
		t.Fatal("missing remote participant media cleanup helper")
	}
	if !strings.Contains(mediaCleanup, "removeRemoteParticipantAudioTracksByName(participantName)") {
		t.Fatal("rejoin media cleanup should remove stale same-name audio monitors")
	}
}

func TestIndexHasLayeredVoiceFocusNoiseReduction(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		"voice-focus",
		"function createOutboundAudioForSource(sourceTrack)",
		"voiceFocusRNNoiseProcessorName",
		"voiceFocusRNNoiseWasmPath = '/public/voice-focus/rnnoise.wasm'",
		"function voiceFocusRNNoiseProcessorOptions(context)",
		"new AudioWorkletNode(context, voiceFocusRNNoiseProcessorName",
		"processor: data.processor || 'rnnoise-wasm'",
		"ensureVoiceFocusWorklet(context)",
		"new AudioWorkletNode(context, voiceFocusProcessorName)",
		"highpass.type = 'highpass'",
		"lowpass.type = 'lowpass'",
		"compressor.threshold.value = -40",
		"this.floorGain = 0.0015",
		"function createVoiceFocusScriptProcessor(context)",
		"const gain = voiceFocusFrameGain(state, input)",
		"const zeroCrossingRate = crossings / Math.max(1, reference.length - 1)",
		"strength: 0.998",
		"speechConfidence: 0",
		"const forcedNoise = transient || hissNoise || rumbleNoise",
		"const hissNoise = zeroCrossingRate > 0.28",
		"const rumbleNoise = zeroCrossingRate < 0.006 && rms < threshold * 1.55",
		"const speechNoiseBlend = Math.max(isolationBlend",
		"state.noiseBias = Math.min(state.noiseFloor * biasMultiplier",
		"function updateVoiceFocusDiagnostics(metrics = {})",
		"function voiceFocusDiagnosticsSnapshot()",
		"function audioProcessorDiagnosticsSnapshot()",
		"rnnoiseReady: Boolean",
		"frameSize: Number",
		"const blend = Math.min(1, Math.max(0, (rms - closeAt)",
		"voiceIsolation: { ideal: voiceFocusEnabled() }",
		"suppressLocalAudioPlayback: { ideal: audioProcessingEnabled() }",
		"googNoiseSuppression2: audioProcessingEnabled()",
		"function trainVoiceFocus()",
		"localAudioSourceTrack?.getSettings?.().deviceId",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing voice focus noise reduction %q", want)
		}
	}
}

func TestVoiceFocusRNNoiseAssetsAreBundled(t *testing.T) {
	for _, path := range []string{
		"public/voice-focus/rnnoise-processor.js",
		"public/voice-focus/rnnoise.wasm",
		"public/voice-focus/RNNOISE_WASM_COPYING.txt",
	} {
		if info, err := os.Stat(path); err != nil {
			t.Fatalf("missing RNNoise voice focus asset %s: %v", path, err)
		} else if info.Size() == 0 {
			t.Fatalf("RNNoise voice focus asset %s is empty", path)
		}
	}

	rawMain, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	for _, want := range []string{
		`http.HandleFunc("/public/", publicAssetHandler)`,
		`http.StripPrefix("/public/", http.FileServer(http.Dir("public")))`,
	} {
		if !strings.Contains(string(rawMain), want) {
			t.Fatalf("main.go missing RNNoise asset serving %q", want)
		}
	}
}

func TestVoiceFocusBenchmarkPasses(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not available for the voice focus benchmark")
	}

	output, err := exec.Command(node, "scripts/voice-focus-benchmark.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("voice focus benchmark failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `"ok": true`) {
		t.Fatalf("voice focus benchmark did not report ok: %s", output)
	}
}

func TestIndexKeepsVoiceFocusTrainingPrivateAndPersistent(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		"async function getInitialUserMedia()",
		"async function getAudioStreamWithFallback(deviceId)",
		"function relaxedAudioConstraintsForDevice(deviceId)",
		"function minimalAudioConstraintsForDevice(deviceId)",
		"Media capture recovered with ${attempt.label}",
		"const audioSettingsStorageKey = 'bonfire.audio.settings.v1'",
		"const audioSettingsAccountStoragePrefix = `${audioSettingsStorageKey}.account.`",
		"const audioSettingsSchemaVersion = 7",
		"function canonicalAudioMode(mode)",
		"function audioProfileName()",
		"mode: 'voice-focus'",
		"savedMode === 'voice-focus' || savedMode === 'off' ? savedMode : defaults.mode",
		"preferredInput: null",
		"function audioSettingsAccountStorageKey(user = authedUser)",
		"function syncAudioSettingsForAccount()",
		"window.localStorage?.getItem(key)",
		"version: audioSettingsSchemaVersion",
		"function resolvePreferredAudioInputDeviceId(devices)",
		"function savePreferredAudioInput(track, fallbackDeviceId = '')",
		"savePreferredAudioInput(nextTrack, selectedDeviceId)",
		"function voiceFocusStatusText()",
		"rnnoise voice focus active",
		"native voice isolation active",
		"let voiceTrainingPrivacyMute = false",
		"function effectiveMicMuted()",
		"return Boolean(isMicMuted || voiceTrainingPrivacyMute)",
		"function setVoiceTrainingPrivacyMute(muted)",
		"setVoiceTrainingPrivacyMute(true)",
		"await applyAudioSettingsToLiveMicrophone({ announce: false })",
		"cancelVoiceFocusTraining({ keepSamples: true, keepPrivacyMute: true })",
		"setVoiceTrainingPrivacyMute(false)",
		"sourceTrack.enabled = voiceTrainingPrivacyMute || !isMicMuted",
		"outputTrack.enabled = !effectiveMicMuted()",
		"function createMutedOutboundAudioClone(sourceTrack)",
		"micMuted: effectiveMicMuted()",
		"you are muted to the room while this runs",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing private persistent voice focus setup %q", want)
		}
	}
}

func TestIndexHidesInRoomFooterClock(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	if !strings.Contains(string(rawHTML), "#appShell.is-in-room .room-clock") {
		t.Fatal("in-room footer status should be hidden so it cannot overlap meeting controls")
	}
}

func TestIndexSupportsDragReorderedVideoFeeds(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		"const videoFeedOrderStorageKey = 'bonfire.video.order.v1'",
		"videoStack.addEventListener('pointerdown', beginVideoTilePointerDrag)",
		"function applyVideoTileOrder()",
		"function rememberVideoOrderFromDOM()",
		"function beginVideoTilePointerDrag(event)",
		"function handleVideoReorderKey(event)",
		".video-tile.is-dragging",
		".video-reorder-handle",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing drag-reordered video support %q", want)
		}
	}
}

func TestIndexLocksControlsAndUsesGreenSpeakerAccent(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		"--speaker-accent: #30D158",
		"--glow-speaker-md:",
		`data-room-layout="grid"`,
		"function currentRoomLayout()",
		"return 'grid'",
		"return 'pinned'",
		"function stageParticipantDisplayName()",
		"presentationTile.classList.toggle('is-pinned-view'",
		"activeSpeakerDisplayName({ ignorePinned: true })",
		"#appShell.is-in-room:not(.is-board-expanded) .meeting-bar",
		"position: fixed;",
		"width: fit-content;",
		".video-tile.is-active-speaker",
		".video-tile.is-active-speaker::after",
		".hearth-speaker::after",
		".hearth-stage[data-room-layout=\"pinned\"] .hearth-seat.is-on-stage",
		".hearth-stage[data-room-layout=\"grid\"] .hearth-seat.is-active-speaker",
		".hearth-stage[data-room-layout=\"grid\"] .hearth-seat.is-active-speaker::after",
		"inset 0 0 0 2px var(--speaker-accent)",
		".board-video-tile.is-speaker",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing locked controls or green speaker accent %q", want)
		}
	}
	for _, unwanted := range []string{
		`data-stage-mode="gallery"`,
		`data-stage-mode="stage"`,
		`aria-label="Video layout"`,
		`>gallery</button>`,
		`>stage</button>`,
	} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("index.html still exposes gallery/stage selector marker %q", unwanted)
		}
	}
}

func TestIndexUsesRemoteTrackAliasesForRelabelingAndDedupe(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		"function normalizeRemoteTrackKeys(keys)",
		"function reliableRemoteStreamId(streamId)",
		"function rememberRemoteStreamLabel(streamId, name)",
		"function remoteStreamLabel(streamId)",
		"remoteLabelConflictsByStream.add(key)",
		"function remoteTrackKeysForEvent(event)",
		"function remoteTrackIdentityKeysForEvent(event)",
		"function rememberRemoteTileKeys(tile, keys)",
		"function remoteTileForKeys(keys)",
		"function forgetRemoteTile(tile)",
		"function relabelRemoteTileByKeys(keys, name)",
		"function repairMissingRemoteVideoTiles(reason = '')",
		"createRemoteVideoTile(keys, stream, participantName, track)",
		"rememberRemoteTileKeys(tile, [...remoteKeysForTile(tile), ...keys])",
		"const sourceTrackId = track?.sourceTrackId || ''",
		"remoteLabelsByTrack.set(sourceTrackId, name)",
		"renameAudioMonitorByKeys(keys, name)",
		"removeRemoteParticipantVideoTracksByName(name, { exceptKeys: trackKeys.length ? trackKeys : keys })",
		"if (!relabelRemoteTileByKeys(keys, name))",
		"const identityKeys = remoteTrackIdentityKeysForEvent(event)",
		"removeRemoteParticipantVideoTracksByName(participantName, { exceptKeys: identityKeys.length ? identityKeys : keys })",
		"if (remoteTileForKeys(identityKeys.length ? identityKeys : keys))",
		"rememberRemoteTileKeys(tile, keys)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing remote track alias hardening %q", want)
		}
	}
}

func TestIndexPrunesDeadRemoteVideoTiles(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		"function remoteTileHasLiveVideo(tile)",
		"function pruneDeadRemoteVideoTiles()",
		"function pruneStaleUnidentifiedRemoteVideoTiles()",
		"function remoteMediaHealthSnapshot()",
		"function repairRemoteMediaHealth(reason = '')",
		"function refreshStalledRemoteVideoTiles(reason = '')",
		"function repairAuxiliaryVideoPlayback(reason = '')",
		"function auxiliaryVideoPlaybackTargets()",
		"function videoElementNeedsRefresh(video)",
		"function watchVideoElementFrames(video)",
		"setVideoElementStream(video, stream, { local: target.local, force: true })",
		"const boardVideoTilesByParticipant = new Map()",
		"createRemoteVideoTile(keys, stream, participantName, videoTrack)",
		"function repairMissingRemoteAudioMonitors(reason = '')",
		"function pruneDuplicateRemoteVideoTilesByName(name)",
		"function pruneDuplicateAudioMonitorsByName(name)",
		"function requestParticipantTrackRefresh(reason = '')",
		"event: 'request_participant_tracks'",
		"function scheduleUnidentifiedRemoteTileRepair(tile)",
		"if (remoteTileHasLiveVideo(tile))",
		"disposeRemoteTile(tile)",
		"forgetRemoteTile(tile)",
		"pruneDeadRemoteVideoTiles()",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing dead remote video pruning %q", want)
		}
	}
}

func TestIndexKeepsRemoteAudioSeparateForLowLatency(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		"function remoteVideoElementForParticipant(name)",
		"function promoteAudioMonitorToVideo(key, monitor, name)",
		"function promoteParticipantAudioToVideo(name)",
		"function demoteRemotePlaybackElementFromVideo(video, name)",
		"function removeRemoteParticipantAudioTracksByName(name, options = {})",
		"function pruneStaleUnidentifiedAudioMonitors(reason = '')",
		"function scheduleUnidentifiedAudioMonitorRepair(key)",
		"function remoteAudioMonitors()",
		"function shouldUseSyncedRemoteAudioPlayback()",
		"function shouldUseElementRemoteAudioPlayback()",
		"return true",
		"function retuneRemoteAudioPlaybackForRoomLoad()",
		"function shouldRenderBoardDockVideo()",
		"return !useCrowdedVideoLimits()",
		"const useWebAudioPlayback = options.play && context.createGain && context.destination && !shouldUseElementRemoteAudioPlayback()",
		"return false",
		"demoteRemotePlaybackElementFromVideo(video, tile.dataset.participant)",
		"audio = createRemoteAudioElement(stream, name)",
		"remoteVideoTracksByParticipant.set(participantName, track)",
		"const playbackElement = shouldUseSyncedRemoteAudioPlayback() ? remoteVideoElementForParticipant(name) : null",
		"attachAudioMonitor(key, name, event.track, { play: true, playbackStream: stream, playbackElement })",
		"playbackGain.connect(context.destination)",
		"monitor.playbackGain?.disconnect()",
		"function remoteAudioSignalSnapshot(monitors = remoteAudioMonitors())",
		"remoteAudioPlaybackPaths: audioSignal.playbackPaths",
		"remoteAudioMaxLevel: audioSignal.maxLevel",
		"function startRoomStateRefresh()",
		"function stopRoomStateRefresh()",
		"refreshRoomStateSnapshot('periodic room state refresh')",
		"startRoomStateRefresh()",
		"stopRoomStateRefresh()",
		"function remotePlaybackNeedsGesture(element)",
		"function remotePlaybackPendingCount(options = {})",
		"function roomAudioPlaybackBlocked()",
		"function notifyRoomAudioBlocked()",
		"function startRoomAudioUnlockRetry()",
		"click anywhere to enable room audio",
		"stopRoomAudioUnlockRetry()",
		"pruneRemoteMediaOutsideRoom() || repaired",
		"const audioLevelSampleIntervalMs = 80",
		"lastAudioLevelSampleAt = now",
		"const visibleSpeakerName = participantDisplayNameInRoom(loudestName)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing low-latency remote audio hardening %q", want)
		}
	}
}

func TestIndexReportsBrowserMediaQualityDiagnostics(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		"const mediaQualityReportIntervalMs = 12000",
		"function sendMediaQualityReport(snapshot, previous, laggy = false, remoteHealth = remoteMediaHealthSnapshot())",
		"event: 'media_quality'",
		"remote: remoteHealth",
		"function reportClientMediaError(stage, error)",
		"event: 'media_error'",
		"lastError.mediaAttempts = failures",
		"function trackSettingsSnapshot(track, keys)",
		"function voiceFocusProcessorType()",
		"sourceSettings: trackSettingsSnapshot(localAudioSourceTrack || localAudioTrack()",
		"profile: audioProfileName()",
		"processorState: audioProcessorDiagnosticsSnapshot()",
		"voiceFocus: voiceFocusEnabled()",
		"voiceFocusMetrics: voiceFocusDiagnosticsSnapshot()",
		"viewport: viewportDiagnosticsSnapshot()",
		"render: renderMediaDiagnosticsSnapshot(remoteHealth)",
		"function renderMediaDiagnosticsSnapshot(remoteHealth = remoteMediaHealthSnapshot())",
		"function videoElementDiagnosticsSnapshot(video)",
		"videoAttachmentRevision += 1",
		"remoteAudioSignalSnapshot(audioMonitorsForRemoteParticipants)",
		"remoteAudioPlaybackPaths: audioSignal.playbackPaths",
		"remoteAudioLevels: audioSignal.levels",
		"function syncRoomAudioPlaybackState()",
		"pendingRemotePlaybackElements.add(element)",
		"notifyRoomAudioBlocked()",
		"summarizeCandidatePair(selectedCandidatePair, report)",
		"function mediaQualityDelta(snapshot, previous)",
		"outboundAudioPacketsSent: snapshot.outboundAudioPacketsSent - previous.outboundAudioPacketsSent",
		"outboundVideoFramesSent: snapshot.outboundVideoFramesSent - previous.outboundVideoFramesSent",
		"inboundVideoPacketsLost: snapshot.inboundVideoLost - previous.inboundVideoLost",
		"inboundAudioPacketsLost: snapshot.inboundAudioLost - previous.inboundAudioLost",
		"videoLostDelta / Math.max(1, videoReceivedDelta + videoLostDelta) > 0.08",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing browser media quality diagnostics %q", want)
		}
	}
}

func TestServerLogsClientMediaQualityDiagnostics(t *testing.T) {
	rawMain, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	mainSource := string(rawMain)
	for _, want := range []string{
		"func logClientMediaQualityReport(rawData string, participantName string, sessionID string)",
		"Client media quality participant=",
		"platform=%s clientVersion=%s",
		"client := mapFromPayload(payload, \"client\")",
		"case \"media_quality\":",
		"logClientMediaQualityReport(message.Data, currentParticipantName(), participantSessionID)",
	} {
		if !strings.Contains(mainSource, want) {
			t.Fatalf("main.go missing client media quality diagnostics %q", want)
		}
	}

	for _, unwanted := range []string{
		"logBrowserMediaQualityReport",
		"Browser media quality participant=",
		"Failed to unmarshal browser media quality report",
	} {
		if strings.Contains(mainSource, unwanted) {
			t.Fatalf("main.go still has browser-only media quality diagnostics %q", unwanted)
		}
	}
}

func TestServerLogsClientMediaErrorDiagnostics(t *testing.T) {
	rawMain, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	mainSource := string(rawMain)
	for _, want := range []string{
		"func logClientMediaErrorReport(rawData string, participantName string, sessionID string)",
		"Client media error participant=",
		"case \"media_error\":",
		"logClientMediaErrorReport(message.Data, currentParticipantName(), participantSessionID)",
	} {
		if !strings.Contains(mainSource, want) {
			t.Fatalf("main.go missing client media error diagnostics %q", want)
		}
	}

	for _, unwanted := range []string{
		"logBrowserMediaErrorReport",
		"Browser media error participant=",
		"Failed to unmarshal browser media error report",
	} {
		if strings.Contains(mainSource, unwanted) {
			t.Fatalf("main.go still has browser-only media error diagnostics %q", unwanted)
		}
	}
}

func TestRoomRelayPreservesRTPHeaderExtensions(t *testing.T) {
	rawMain, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	main := string(rawMain)
	for _, unwanted := range []string{
		"packet.Extension = false",
		"packet.Extensions = nil",
	} {
		if strings.Contains(main, unwanted) {
			t.Fatalf("room relay must not strip mobile video RTP metadata with %q", unwanted)
		}
	}
	for _, want := range []string{
		"Preserve RTP header extensions from the publisher",
		"trackLocal.WriteRTP(packet)",
	} {
		if !strings.Contains(main, want) {
			t.Fatalf("main.go missing RTP extension preservation marker %q", want)
		}
	}
}

func functionBody(source string, signature string) string {
	start := strings.Index(source, signature)
	if start == -1 {
		return ""
	}
	open := strings.Index(source[start:], "{")
	if open == -1 {
		return ""
	}
	bodyStart := start + open
	depth := 0
	for index := bodyStart; index < len(source); index++ {
		switch source[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return source[bodyStart : index+1]
			}
		}
	}
	return ""
}

func TestIndexRoomEntryChoreographyHoldsFinalVisibleState(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)

	// The .mount-stagger base state is opacity 0, so any animation that
	// overrides mount-rise (which uses a forwards fill) must also hold its
	// final frame. A bare backwards fill snaps the room back to opacity 0
	// once the entry animation ends, leaving a first-time visitor staring
	// at the background gradient until they refresh (the reload path takes
	// the .is-fast-mount branch, which masks the bug).
	for _, want := range []string{
		"#appShell.is-in-room .hearth-presentation { animation: rise-in 480ms var(--ease-out) both; }",
		"#appShell.is-in-room .scout-rail          { animation: rise-in 480ms var(--ease-out) 80ms both; }",
		"#appShell.is-in-room .board-rail          { animation: rise-in 480ms var(--ease-out) 160ms both; }",
		"#appShell.is-in-room .meeting-bar         { animation: rise-in 480ms var(--ease-out) 240ms both; }",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing room-entry animation that stays visible after it ends: %q", want)
		}
	}

	if strings.Contains(html, "backwards;") {
		t.Fatal("index.html uses a bare backwards animation fill; over an opacity-0 base state the element vanishes when the animation ends — use both")
	}
}

func TestRealtimeVoiceActionCanCloseVoiceIsland(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		"type === 'set_voice_control'",
		"stopRealtimeVoiceConversation({ notifyServer: false })",
		"const notifyServer = options?.notifyServer !== false",
		"if (enabled && appShell.classList.contains('is-in-room'))",
		"if (notifyServer && roomRealtimeVoiceActive()) {\n          sendVoiceControlState(false)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing realtime voice close action %q", want)
		}
	}
}

func TestRoomEntryStopsPrivateRealtimeBeforeRoomVoiceState(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	joinStart := strings.Index(html, "async function joinRoom(options = {})")
	if joinStart < 0 {
		t.Fatal("index.html missing joinRoom function")
	}
	stopIndex := strings.Index(html[joinStart:], "stopPrivateRealtimeVoiceForRoom()")
	if stopIndex < 0 {
		t.Fatal("joinRoom must stop private Realtime voice at the room boundary")
	}
	entryIndex := strings.Index(html[joinStart:], "roomEntryInProgress = true")
	if entryIndex < 0 {
		t.Fatal("joinRoom must mark room entry in progress before room state starts")
	}
	roomModeIndex := strings.Index(html[joinStart:], "if (voiceOnly) {\n          setRealtimeVoiceMode('room')")
	if roomModeIndex < 0 {
		t.Fatal("joinRoom must set explicit room voice mode for voice-only joins")
	}
	if stopIndex > roomModeIndex {
		t.Fatal("joinRoom must stop private Realtime voice before setting room voice-only state")
	}
	if entryIndex > roomModeIndex {
		t.Fatal("joinRoom must mark room entry in progress before setting room voice-only state")
	}

	for _, want := range []string{
		"if (inRoom) {\n          stopPrivateRealtimeVoiceForRoom()",
		"if (tool === 'room') {\n          stopPrivateRealtimeVoiceForRoom()",
		`<span id="topbarRoomState">waiting room</span>`,
		"const inRoom = appShell.classList.contains('is-in-room') && roomMediaActive()",
		"waitingParticipants.textContent = `${seatCount} invited",
		"const inRoom = roomMediaActive() && appShell.classList.contains('is-in-room')",
		"return !roomEntryInProgress && !roomMediaActive() && !appShell.classList.contains('is-in-room')",
		"return Boolean(authedUser) && privateRealtimeVoiceSurfaceAvailable()",
		"who: names.length ? `${Math.max(occupiedSeats, names.length)} ${inRoom ? 'in the room' : 'invited'}` : 'the team'",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing private/room boundary marker %q", want)
		}
	}
}

func TestRealtimeWaveformLaunchersUsePrivateVoiceIslandOutsideRoom(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		`data-start-realtime-voice`,
		"const realtimeVoiceOpeners = Array.from(document.querySelectorAll('[data-start-realtime-voice]'))",
		"realtimeVoiceOpeners.forEach(button => {\n        button.addEventListener('click', startRealtimeVoiceFromWaveform)",
		`id="osAssistantToggle" class="os-assistant__toggle" type="button" aria-label="Start Realtime 2 voice" title="Start Realtime 2 voice" aria-expanded="false" hidden`,
		"osAssistantToggle.hidden = true",
		"function startRealtimeVoiceFromWaveform(event)",
		"if (appShell.classList.contains('is-in-room')) {\n          startRealtimeVoiceConversation(event)",
		"if (privateRealtimeVoiceSurfaceAvailable()) {\n            startRealtimeVoiceFromWaveform(event)",
		"await startPrivateRealtimeVoiceConversation()",
		"setRealtimeVoiceMode('private')",
		"setRealtimeVoiceMode('room')",
		"if (!privateRealtimeVoiceActive() && voiceIslandState !== 'error')",
		"if (roomEntryInProgress || appShell.classList.contains('is-in-room') || roomMediaActive())",
		"&& privateRealtimeVoiceSurfaceAvailable()",
		"const localSDP = await waitForRealtimeOfferSDP(peer, offer)",
		"const current = String(peer.localDescription?.sdp || offer?.sdp || '')",
		"if (current.trim())",
		"const sdp = normalizeRealtimeSDPForBrowser(offerSDP)",
		"if (!sdp.trim())",
		"const answerSDP = normalizeRealtimeSDPForBrowser(result.data?.sdp)",
		"function normalizeRealtimeSDPForBrowser(sdp)",
		"join('\\r\\n')",
		"function shouldShowVoiceIsland()",
		"return (appShell.dataset.tool || 'office') !== 'office'",
		"let privateRealtimeVoicePeer",
		"let privateRealtimeVoiceHandledCalls = new Set()",
		"let privateRealtimeVoiceSessionToken = 0",
		"let voiceIslandState = 'idle'",
		"await beginPrivateRealtimeVoiceSession(sessionToken)",
		"assertPrivateRealtimeVoiceSession(sessionToken",
		"postAuthJSON('/assistant/realtime-offer'",
		"postAuthJSON('/assistant/realtime-tool'",
		"function handlePrivateRealtimeToolCall(item)",
		"type: 'function_call_output'",
		"type: 'response.create'",
		"function closePrivateRealtimeVoiceSession()",
		"setVoiceIslandState('connecting', 'connecting...')",
		"top: max(82px, calc(env(safe-area-inset-top) + 72px));",
		"background: var(--glass-chrome);",
		"border-radius: var(--r-full);",
		"voiceIslandDetailForEvent('hearing', text)",
		"Start Realtime 2 voice",
		"let osAssistantConversationTurns = []",
		"const history = osAssistantConversationTurns.slice(-12)",
		"body: JSON.stringify({ query, mode: osAssistantMode, history })",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing Realtime 2 voice island marker %q", want)
		}
	}
	if strings.Contains(html, "function startScoutVoiceFromWaveform(event)") {
		t.Fatal("visible waveform launchers should use the Realtime 2 voice island path")
	}
	if strings.Contains(html, "joinRoom({ voiceOnly: true })") {
		t.Fatal("waveform Realtime voice launchers must not enter the room join path")
	}
	privateStart := strings.Index(html, "async function startPrivateRealtimeVoiceConversation()")
	privateEnd := strings.Index(html, "async function beginPrivateRealtimeVoiceSession(sessionToken)")
	if privateStart < 0 || privateEnd < 0 || privateEnd <= privateStart {
		t.Fatal("could not isolate private Realtime voice launcher")
	}
	if strings.Contains(html[privateStart:privateEnd], "sendVoiceControlState(true)") {
		t.Fatal("private Realtime voice must not send room voice_control messages")
	}
}

// Frontend wiring guard for the wave-4 audit fixes: mobile composer
// clearance, channel scope filtering without preview bleed, inline channel
// creation, notification dismissal, mobile meeting bar, room chat sheet
// treatment, the speaking-only signal ring, and the quiet login gate.
func TestIndexAuditFixWiring(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	for _, want := range []string{
		// chat/tool pages clear the fixed bottom rail on phones
		"padding-bottom: calc(max(16px, env(safe-area-inset-bottom)) + 64px);",
		// inline glass channel creation replaces window.prompt
		`id="chatChannelCreate"`,
		`id="chatChannelName"`,
		`id="chatChannelsEmpty"`,
		`id="chatPrivateLabel"`,
		"function setChannelCreateOpen(open)",
		"async function createScoutChatThreadOnServer(title, visibility)",
		// notifications: header action group + nav/Escape dismissal
		`class="notification-panel__actions"`,
		// mobile meeting bar: derived dock clearance + icon-only Send notes
		"--dock-h: 152px;",
		"@media (max-width: 400px) {",
		`class="archive-label"`,
		`class="archive-icon"`,
		"function setArchiveMeetingLabel(text)",
		// room chat: carded desktop panel, opaque sheet + stage scrim
		"box-shadow: 0 0 0 100vmax var(--scrim), var(--shadow-3);",
		// the ring means speaking NOW, not merely present
		"function participantIsAudiblyLive(name)",
		"function scheduleActiveSpeakerRingDecay(speaking)",
		// login: status pill earns its slot; disabled stays a faded primary
		`id="loginStatusPill"`,
		"function setLoginStatusPill(state, text)",
		"color-mix(in srgb, var(--accent) 45%, var(--bg-app))",
		// hidden must survive the .chat-thread-item display:flex rule so the
		// default Scout row really disappears under the channel scope
		".chat-thread-item[hidden]",
		// the board surface mirrors the stage: occupancy excludes the local
		// identity outside the room, the ring requires genuine audio, and the
		// exit chrome only says 'back to room' when there is a room to go to
		"function boardOccupantNames()",
		"const speakerAudible = participantIsAudiblyLive(speaker)",
		"name === speaker && speakerAudible",
		"function updateBoardExitChrome()",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing audit-fix anchor %q", want)
		}
	}

	for _, unwanted := range []string{
		// channel names come from the inline glass row now
		"window.prompt(",
		// the static always-on pill is gone from the login card
		`<span class="pill">not connected</span>`,
		// button label writes go through the span-aware helper
		"archiveMeetingButton.textContent = '",
	} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("index.html still contains retired audit-fix marker %q", unwanted)
		}
	}

	// the segmented scope filters the list: channel view never shows the
	// private Scout thread, private view never shows channels
	renderBody := functionBody(html, "function renderChatAgentThreads()")
	if !strings.Contains(renderBody, "const channelScope = newChatThreadVisibility === 'public'") {
		t.Fatal("renderChatAgentThreads must treat the scope control as a list filter")
	}
	if !strings.Contains(renderBody, "chatDefaultThread.hidden = channelScope || privates.length > 0") {
		t.Fatal("the default private Scout row must hide in the channel scope")
	}
	// the row's pressed highlight tracks the actually-open thread, not the
	// emptiness of the current scope
	if !strings.Contains(renderBody, "chatDefaultThread.setAttribute('aria-pressed', selectedScoutChatThread() ? 'false' : 'true')") {
		t.Fatal("the default Scout row's aria-pressed must follow the open thread")
	}

	// transcript/brain pulses ride the 15s intel cache — only a fresh
	// mission_insight event may force the canvas fetch
	kanbanBody := functionBody(html, "function handleKanbanMessage(message)")
	if strings.Count(kanbanBody, "loadMissionIntelligence(true)") > 1 || !strings.Contains(kanbanBody, "loadMissionIntelligence()") {
		t.Fatal("memory_transcript/memory_brain events must use the cached loadMissionIntelligence() path")
	}

	// a channel post must never bleed into the private Scout row's preview
	updateBody := functionBody(html, "function updateChatThreadItem(text, ts)")
	if !strings.Contains(updateBody, "chatThreadIsChannel(selectedScoutChatThread())") {
		t.Fatal("updateChatThreadItem must refuse to mirror channel posts onto the private row")
	}

	// switching tools dismisses the notifications window
	toolBody := functionBody(html, "function setActiveTool(tool)")
	if !strings.Contains(toolBody, "setNotificationPanelOpen(false)") {
		t.Fatal("setActiveTool must close the notifications panel")
	}

	// Escape closes the notifications panel and the room chat sheet
	if !strings.Contains(html, "if (!notificationPanel.hidden) {\n            setNotificationPanelOpen(false)") {
		t.Fatal("the Escape handler must close the notifications panel")
	}
	if !strings.Contains(html, "if (isRoomChatOpen()) {\n            setRoomChatOpen(false)") {
		t.Fatal("the Escape handler must close the room chat sheet")
	}

	// the signal ring is gated on genuine audio, both on the stage tile and
	// the grid tiles
	hearthBody := functionBody(html, "function updateHearthParticipants()")
	if !strings.Contains(hearthBody, "const activeSpeakerAudible = participantIsAudiblyLive(activeSpeaker)") {
		t.Fatal("updateHearthParticipants must derive ring state from audible liveness")
	}
	if !strings.Contains(hearthBody, "Boolean(activeSpeakerAudible && stageName && stageName === activeSpeaker)") ||
		!strings.Contains(hearthBody, "Boolean(activeSpeakerAudible && name && name === activeSpeaker)") {
		t.Fatal("is-active-speaker toggles must require audible liveness")
	}
}
