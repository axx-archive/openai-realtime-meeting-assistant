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
		"if (isIOSDevice) {",
		"const useLocalMirrorCanvas = isIOSDevice",
		"localVideo?.classList.toggle('local-mirror-source', hasReadyCanvas)",
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
		"fetch('/client-config', { cache: 'no-store'",
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
		// rtc §5.1: the "N/5" counter is held until attempt >= 2 so a single
		// self-healing blip never shows an alarming count.
		"const reconnectLabel = attempt >= 2",
		"? `reconnecting media ${attempt}/${maxIceRestartAttempts}`",
		"function handleMediaDisconnected(detail)",
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

// TestIndexWave14PolishMarkers pins the Wave 14 delight, device-recovery, and
// wake-word arming behaviors so a future index.html refactor can't silently drop
// them. Behavioral markers are functionBody-SCOPED (the Wave-6 lesson: a flat
// file-wide Contains can pass on an unrelated match elsewhere in a 40k-line
// file); only genuinely top-level artifacts (CSS rules, DOM ids, module-scope
// consts) are checked file-wide.
func TestIndexWave14PolishMarkers(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	// Each behavior must appear INSIDE the function that owns it.
	scoped := []struct {
		signature string
		markers   []string
	}{
		// #14 copy-link confirm
		{"function flashCopyConfirm(button)", []string{" copied ✓", "is-copied"}},
		// #15 empty-state serif voice
		{"function intelEmptyNode(text)", []string{"intel-empty t-empty-poetry"}},
		// #12 quarantine subtle-exit
		{"function trayEntryExit(wrap, done)", []string{"is-leaving", "220"}},
		// device recovery (rtc §5.2/§5.3)
		{"async function reconcileAudioDeviceChange()", []string{"offerDeviceSwitch", "recoverLocalMicrophone"}},
		{"function offerDeviceSwitch(device)", []string{"deviceOfferChipTargetId = device.deviceId", "ensureDeviceOfferChip()"}},
		{"async function restoreAudioAfterVisibility()", []string{"audioContext.resume", "recoverLocalMicrophone"}},
		// card-003 W4 gap 4: a server-closed PC re-dials through the signaling
		// reconnect seam (rebuilds the PC) rather than firing a futile
		// restart_ice — see TestIndexMediaDisconnectRedialsInsteadOfRestart.
		{"function handleMediaDisconnected(detail)", []string{"mediaDisconnectRecoveryTried", "scheduleSignalingReconnect"}},
		// bounded mic-ended recovery (rtc §5: never an unbounded rebuild loop)
		{"async function handleLocalAudioEnded()", []string{"maxLocalAudioRecoveryAttempts", "microphone unavailable — pick another in settings"}},
		// wake-word arming safety invariant: gated on a live ROOM voice session
		// (never a private grill, never a typed message).
		{"function armScoutForWake()", []string{"wakeWordArmingEnabled()", "if (realtimeVoiceMode !== 'room') return"}},
	}
	for _, group := range scoped {
		body := functionBody(html, group.signature)
		if body == "" {
			t.Fatalf("index.html missing Wave 14 function %q", group.signature)
		}
		for _, marker := range group.markers {
			if !strings.Contains(body, marker) {
				t.Fatalf("index.html: %q must appear INSIDE %s (functionBody-scoped, the Wave-6 lesson)", marker, group.signature)
			}
		}
	}

	// Genuinely top-level artifacts: CSS rules, DOM ids, module-scope consts.
	for _, want := range []string{
		".t-empty-poetry {",    // #15 CSS
		"tray-slide-out 220ms", // #12 CSS
		"pkg-card-sweep 640ms", // #6 CSS
		"const lastPackageStages = new Map()",
		"const WAKE_WORD_ARMING_KEY = 'bonfire.wakeword.arming.v1'",
		"id=\"wakeWordArming\"",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing Wave 14 top-level marker %q", want)
		}
	}

	// the reduced-motion block must neutralize the new motion (state kept).
	for _, want := range []string{
		".tray__entry.is-leaving { animation: none; opacity: 0; }",
		".t-empty-poetry { animation: none; }",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing reduced-motion pin for Wave 14 delight: %q", want)
		}
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
		`data-tool="office" aria-label="BonfireOS home" aria-pressed="true"`,
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
		`class="office-launch__wave pressable"`,
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
		// The 4 composer starter pills were cut with the propose-confirm
		// router (spec §2); frontend_router_test.go pins their absence and the
		// confirmation card that replaced them.
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
		"data-research-followup",
		"data-research-version",
		"data-research-readiness",
		`id="scoutFollowUpTarget" class="scout-followup-target"`,
		"function renderScoutFollowUpTarget()",
		"function readinessChipNode(metadata, mode)",
		"followUpArtifactId: followUp.artifactId",
		"scout-chat-research__delta",
		"chat-rich__rule",
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
		"addArtifactEntry(result.artifact, { select: false })",
		"notes sent · ${meetingName} → memory",
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
		`id="brandMark" class="topbar__mark" role="img" aria-label="BonfireOS"`,
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
		"const TOOL_IDS = ['office', 'room', 'chat', 'artifacts', ...agentToolIds, 'board', 'memory', 'files']",
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
		`<section class="intel-section" aria-label="Decision ledger">`,
		`id="intelDecisionsStamp" class="intel-section__hint"`,
		`id="intelDecisions" class="intel-col intel-col--ledger"`,
		"function renderIntelDecisions()",
		"case 'decision':",
		"no decisions on record yet — scout logs them as meetings settle things",
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
		"return 'the company brain, visible'",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing Mission Intelligence marker %q", want)
		}
	}
	// The package binder segment: an intelligence/packages segmented control,
	// a packages pane fed by GET /assistant/packages + the "package" office
	// event, and open-in-place for attached artifacts through the all-user
	// artifact library (which stays OUTSIDE both panes for that reason).
	for _, want := range []string{
		`<button id="intelSegBrain" class="intel-seg__btn" type="button" aria-pressed="true">intelligence</button>`,
		`<button id="intelSegPackages" class="intel-seg__btn" type="button" aria-pressed="false">packages</button>`,
		`<div id="intelBrainPane" class="intel-pane">`,
		`<div id="intelPackagesPane" class="intel-pane" hidden>`,
		`id="packageCreateForm" class="package-create" hidden`,
		`id="packageList" class="package-list"`,
		"async function loadPackages(force = false)",
		"fetch('/assistant/packages', { cache: 'no-store' })",
		"function renderPackages()",
		"function renderPackageDetails(record)",
		"function openPackageArtifact(artifactId)",
		"setIntelLibraryOpen(true)",
		"case 'package':",
		"no venture packages yet — create one here, or ask scout to create a package",
		`.intel-seg__btn[aria-pressed="true"]`,
		".intel-pane[hidden]",
		".package-row__details",
		`label.setAttribute('aria-current', 'step')`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing package binder marker %q", want)
		}
	}
	brainPaneIndex := strings.Index(html, `<div id="intelBrainPane"`)
	packagesPaneIndex := strings.Index(html, `<div id="intelPackagesPane"`)
	libraryIndex := strings.Index(html, `<section class="intel-section" data-admin-artifacts`)
	if !(brainPaneIndex != -1 && brainPaneIndex < packagesPaneIndex && packagesPaneIndex < libraryIndex) {
		t.Fatal("intel canvas order must be brain pane, packages pane, then the shared artifact library outside both panes")
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
		`id="brandMark" class="topbar__mark" role="img" aria-label="BonfireOS"`,
		"top: 50%;",
		"inset: 0 auto 0 0;",
		"border-right: 1px solid var(--line-1);",
		"width: 60px;",
		"padding: 16px 0 14px;",
		"transform: translateY(-50%);",
		"display: none;",
		".tool-rail:hover,",
		".tool-rail:focus-within",
		"overflow: visible;",
		"left: calc(100% + 14px);",
		"max-width: none;",
		"pointer-events: none;",
		".tool-rail__tool:hover .tool-rail__label {",
		".tool-rail__tool:focus-visible .tool-rail__label {",
		"transition-delay: 350ms;",
		"#appShell.is-authed .workspace",
		"padding-left: 60px;",
		`<span class="tool-rail__label">BonfireOS</span>`,
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
		"padding-bottom: max(96px, calc(var(--sp-2) + env(safe-area-inset-bottom)));",
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
		"inset: 0 auto 0 0;",
		"width: 60px;",
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
	desktopShellBlock := functionBody(html, "@media (min-width: 641px) {")
	for _, want := range []string{
		"#appShell.is-authed {",
		"--shell-topbar-height: 52px;",
		"padding-left: 60px;",
		"#appShell.is-authed .topbar {",
		"display: flex;",
	} {
		if !strings.Contains(desktopShellBlock, want) {
			t.Fatalf("desktop shell must clear the 60px rail and show the 52px header; missing %q in the min-width 641px block", want)
		}
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

	scoutBody := functionBody(html, "function scoutChatMessageNode(kind, text, ts, files, authorLabel, viaScout = false)")
	if scoutBody == "" {
		t.Fatal("index.html missing scoutChatMessageNode")
	}
	for _, want := range []string{
		"const rich = kind === 'scout'",
		"body.className = `scout-chat-text${rich ? ' chat-rich' : ''}`",
		"appendChatRichTextNodes(body, text)",
		// non-rich messages render through the mention-highlight pass, which is
		// DOM-built (createTextNode + textContent) — as injection-safe as the
		// bare body.textContent it replaced; frontend_chat_mentions_test.go
		// holds the no-innerHTML rule inside that function.
		"appendChatMentionTextNodes(body, text)",
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
		"async function replaceLocalVideoTrack(nextTrack, options = {})",
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
		"return handleSignal(message, { socket, sessionPeer: pc })",
		"const contextIsCurrent = () => ws === socket && pc === sessionPeer",
		"await waitForStableSignaling(sessionPeer, contextIsCurrent)",
		"async function waitForStableSignaling(sessionPeer = pc, isCurrent = () => pc === sessionPeer, timeoutMs = 2500)",
		"if (contextIsCurrent() && socket.readyState === WebSocket.OPEN)",
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
		// Wave 9: capture constraints are strategy-driven, not per-mode toggled.
		"function resolveSuppressionStrategy(",
		"voiceIsolation: { ideal: strategy.voiceIsolation }",
		"suppressLocalAudioPlayback: { ideal: strategy.echoCancellation }",
		"googNoiseSuppression2: strategy.browserNoiseSuppression",
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

func TestIndexKeepsVoiceFocusPersistentAndHonest(t *testing.T) {
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
		// Wave 9: schema bumped to the unified v8 AV record, default-on desktop.
		"const audioSettingsSchemaVersion = 8",
		"function canonicalAudioMode(mode)",
		"function audioProfileName()",
		"mode: isMobileDevice ? 'standard' : 'voice-focus'",
		"savedMode === 'voice-focus' || savedMode === 'off' ? savedMode : defaults.mode",
		"preferredInput: null",
		"function audioSettingsAccountStorageKey(user = authedUser)",
		"function syncAudioSettingsForAccount()",
		"window.localStorage?.getItem(key)",
		"version: audioSettingsSchemaVersion",
		"function resolvePreferredAudioInputDeviceId(devices)",
		"function savePreferredAudioInput(track, fallbackDeviceId = '')",
		"savePreferredAudioInput(nextTrack, selectedDeviceId)",
		// Honest, mechanism-aware chip copy replaced the old "trained" strings.
		"function voiceFocusStatusText()",
		"voice focus active · ",
		"this browser's isolation",
		"function renderAudioSavedHint()",
		"function renderPreferredMicHonesty()",
		// Privacy-mute machinery is retained (dormant) for the mute path.
		"let voiceTrainingPrivacyMute = false",
		"function effectiveMicMuted()",
		"return Boolean(isMicMuted || voiceTrainingPrivacyMute)",
		"function setVoiceTrainingPrivacyMute(muted)",
		"setVoiceTrainingPrivacyMute(false)",
		"sourceTrack.enabled = voiceTrainingPrivacyMute || !isMicMuted",
		"outputTrack.enabled = !effectiveMicMuted()",
		"function createMutedOutboundAudioClone(sourceTrack)",
		"micMuted: effectiveMicMuted()",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing persistent honest voice focus setup %q", want)
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

// Frontend wiring guard for the 2026-07 simulation quick fixes: completion
// reaches the requester's thread card, board/memory read without a room join,
// unread signals surface outside the room, proposal toasts respect the PiP,
// channel agent launches need an explicit prefix, the refresh cooldown counts
// down, the meeting label derives from the brain, and signed-out tabs stay
// quiet on the network.
func TestIndexSimulationQuickFixWiring(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	for _, want := range []string{
		// completion notifications land on the originating thread card
		"function chatThreadForArtifactId(artifactId)",
		// board + memory reads over authed HTTP without joining the call
		// (options.force re-syncs the board after leaving a room — the live
		// office socket would otherwise skip the fallback forever)
		"async function loadBoardSnapshot(options = {})",
		"loadBoardSnapshot({ force: true })",
		"async function loadMemorySnapshot()",
		"fetch('/assistant/board', { cache: 'no-store' })",
		"fetch('/assistant/memory', { cache: 'no-store' })",
		"function applyBoardSnapshot(data)",
		"function applyMemorySnapshot(data)",
		// the bell badge fills at sign-in and clears when the popover opens
		"loadNotifications().then(() => markAllNotificationsRead())",
		// room-chat unread rides the rail icon and the meeting PiP
		"function renderRoomChatUnreadBadges()",
		`id="roomRailUnread"`,
		`id="pipUnread"`,
		// per-channel unread dots from the device-local lastSeen map
		"const chatThreadSeenStorageKey = 'bonfire.chat.lastSeen.v1'",
		"function chatThreadHasUnread(thread)",
		// proposal toasts stack below the PiP and dock into the bell
		"body:has(#pipMeeting:not([hidden])) .proposal-deck",
		"function scheduleCodexProposalDock(id)",
		"function resolveCodexProposalBellEntry(proposal)",
		// channel agent launches require an explicit "mode:" prefix
		"function scoutChannelModePrefixForText(text)",
		"mention @scout to launch this",
		// own-message detection keys on the session email, not display name
		"if (kind === 'user' && authorEmail && authorEmail !== selfEmail) {",
		// refresh-themes 429 disables the button with a countdown
		"function beginIntelRefreshCooldown(seconds)",
		"response.headers.get('Retry-After')",
		// meeting identity derives from the brain's dominant theme
		"function meetingDisplayName()",
		"function syncMeetingIdentityLabel()",
		// signed-out tabs must not poll authed endpoints into 401 noise
		"// A signed-out tab must not poll authed endpoints into 401 noise.",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing simulation quick-fix marker %q", want)
		}
	}

	if strings.Contains(html, "platform standup") {
		t.Fatal("the meeting label must derive from the brain's dominant theme (or a dated fallback), not a hardcoded standup name")
	}
}

// Frontend wiring guard for the office event socket: it opens on auth (not
// room join), sends the `office` hello, feeds kanban events directly (no
// signaling chain), reconnects with backoff, closes on sign-out, heartbeats
// so a half-open socket cannot read OPEN forever, and demotes the HTTP
// snapshot reads to fallback-only while live (the chat poll, by contrast,
// always reconciles — see the poll assertions below).
func TestIndexOfficeSocketWiring(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	for _, want := range []string{
		// office socket state + lifecycle
		"let officeWs = null",
		"function officeSocketLive()",
		"function ensureOfficeSocket()",
		"function scheduleOfficeSocketReconnect()",
		"function closeOfficeSocket()",
		"JSON.stringify({ event: 'office', data: '{}' })",
		// reconnect backoff: 1s doubling capped at 30s
		"Math.min(1000 * 2 ** officeWsAttempts, 30000)",
		// kanban envelopes bypass the room signal chain on this socket
		"case 'office_granted':",
		// the socket follows auth: every authedUser flip funnels through
		// renderLoginMode
		"// the office event socket follows auth, not room membership: every",
		// poll supersession: HTTP board/memory snapshots stay fallback-only
		// while the office socket is live
		"if (!authedUser || ws?.readyState === WebSocket.OPEN || officeSocketLive()) {",
		// heartbeat: ping every 30s, stamp every inbound frame, force-close
		// a nominally OPEN socket after 75s of silence so the backoff
		// reconnect + office_granted catch-up fire
		"function ensureOfficeHeartbeat()",
		"function stopOfficeHeartbeat()",
		"JSON.stringify({ event: 'office_ping' })",
		"lastOfficeFrameAt = Date.now()",
		"Date.now() - lastOfficeFrameAt > 75000",
		// a channel notification deep-linking to a thread doubles as a
		// debounced chat reconcile signal
		"function nudgeChatThreadsFromNotification()",
		// room-chat unread accumulated outside the room clears on a fresh join
		"clearRoomChatUnread()",
		// meeting_archived rides the union fan-out — dedupe by download URL
		"let lastMeetingArchivedUrl = ''",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing office socket marker %q", want)
		}
	}

	// Poll = true reconciliation: a half-open office socket reads OPEN
	// forever, so the chat poll must never defer to readyState — it runs
	// whenever authed, 15s with the chat tool in front and 45s everywhere
	// else. The fingerprint dedup in loadScoutChatThreads keeps it cheap.
	pollBody := functionBody(html, "function syncChatThreadPolling()")
	if strings.Contains(pollBody, "officeSocketLive()") {
		t.Fatal("the chat thread poll must reconcile even while the office socket looks live — half-open sockets read OPEN forever")
	}
	if !strings.Contains(pollBody, "15000") || !strings.Contains(pollBody, "45000") {
		t.Fatal("the chat thread poll must run at 15s on the chat tool and 45s everywhere else while authed")
	}

	// ensureOfficeSocket never rides the room socket state: it must not
	// reference `ws` (the room socket) at all.
	ensureBody := functionBody(html, "function ensureOfficeSocket()")
	if strings.Contains(ensureBody, " ws ") || strings.Contains(ensureBody, "(ws") {
		t.Fatal("ensureOfficeSocket must not couple to the room websocket")
	}
}

// Frontend wiring guard for first-class meeting records: the `meeting` kanban
// event feeds a shared server-anchored room clock (join-relative clocks die)
// and the meeting label prefers the record's server-derived title.
func TestIndexMeetingRecordWiring(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	for _, want := range []string{
		// the `meeting` event lands in the inner kanban switch
		"case 'meeting':",
		"function handleMeetingRecord(payload)",
		// shared clock state: server startedAt + serverNow skew anchor
		"let sharedMeetingStartMs = 0",
		"let meetingRecord = null",
		"let meetingClockOffsetMs = 0",
		"meetingClockOffsetMs = Date.now() - serverNowMs",
		// the room clock counts from the server meeting start when known
		"roomStartedAt = sharedMeetingStartMs || Date.now()",
		// the meeting label prefers the record's server-derived title
		"if (meetingRecord?.title) {",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing meeting record marker %q", want)
		}
	}

	if strings.Contains(html, "roomStartedAt = Date.now()") {
		t.Fatal("join-relative room clocks must not come back: the clock anchors on sharedMeetingStartMs when the meeting record is known")
	}
}

// Frontend wiring guard for the artifact model: every signed-in account reads
// the artifact library (only external-write approval stays admin-gated),
// display titles derive from the artifact body when the stored title is still
// the launch prompt, and close-the-loop room-chat deliveries render an
// artifact chip that opens the report in the right-side artifact stage over
// the room — never a tool yank to the Intelligence tab.
func TestIndexArtifactModelWiring(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	for _, want := range []string{
		// permissions: the library follows sign-in; approval keeps the admin
		// email check
		"function canUseArtifactLibrary()",
		"function canApproveExternalWrites()",
		"const canApprove = approvalRequired && canApproveExternalWrites()",
		// display-title fallback for prompt-titled artifacts
		"function artifactDisplayTitle(entry)",
		"title.textContent = artifactDisplayTitle(entry)",
		// the artifact editor keeps editing the raw stored title
		"artifactTitleInput.value = artifact?.metadata?.title || ''",
		// close-the-loop delivery chip on room-chat bubbles: the chip opens
		// the report in the artifact stage (the reroute is LIVE code, not a
		// comment); the stage header keeps "open in intelligence" as the
		// explicit data-room escape
		"room-chat-artifact-chip",
		"const artifactId = String(message?.artifactId || '').trim()",
		"chip.addEventListener('click', () => openArtifactStage(artifactId, entry ? artifactDisplayTitle(entry) : 'report'))",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing artifact model marker %q", want)
		}
	}

	// The read gate is sign-in, not the admin email: the email check may only
	// survive inside canApproveExternalWrites.
	libraryGateBody := functionBody(html, "function canUseArtifactLibrary()")
	if !strings.Contains(libraryGateBody, "return Boolean(authedUser)") || strings.Contains(libraryGateBody, "artifactAdminEmail") {
		t.Fatal("canUseArtifactLibrary must gate on sign-in only; artifactAdminEmail belongs to canApproveExternalWrites")
	}
	approveGateBody := functionBody(html, "function canApproveExternalWrites()")
	if !strings.Contains(approveGateBody, "artifactAdminEmail") {
		t.Fatal("canApproveExternalWrites must keep the admin email check for the external-write gate")
	}

	// Admin-only dashboard copy is dead: artifacts belong to the whole team.
	// The retired chip dispatch (an open_tool yank to the Intelligence tab)
	// must not come back — in live code OR as a marker-satisfying comment.
	for _, stale := range []string{
		"'admin only'",
		"Scout has the company brain",
		"title.textContent = entry.metadata?.title || artifactModeLabel(entry)",
		"{ type: 'open_tool', tool: 'artifacts', artifactId },",
	} {
		if strings.Contains(html, stale) {
			t.Fatalf("index.html still contains pre-relaxation artifact marker %q", stale)
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
		"setVoiceIslandState('connecting', 'connecting…')",
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
		"padding-bottom: calc(max(16px, env(safe-area-inset-bottom)) + 96px);",
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
		"color-mix(in srgb, var(--accent) 60%, var(--bg-app))",
		// hidden must survive the .chat-thread-item display:flex rule so the
		// default Scout row really disappears once a real private thread exists
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

	// card 070: no scope toggle — both audiences render at once into their own
	// always-visible section; the default Scout row is the private section's
	// empty state, and the channels-empty note is the channel section's
	renderBody := functionBody(html, "function renderChatAgentThreads()")
	if !strings.Contains(renderBody, "const channels = scoutChatThreads.filter(thread => chatThreadIsChannel(thread))") ||
		!strings.Contains(renderBody, "const privates = scoutChatThreads.filter(thread => !chatThreadIsChannel(thread))") {
		t.Fatal("renderChatAgentThreads must always compute both the channels and private lists")
	}
	if !strings.Contains(renderBody, "chatDefaultThread.hidden = privates.length > 0") {
		t.Fatal("the default private Scout row must hide once a real private thread exists")
	}
	if !strings.Contains(renderBody, "chatChannelsEmpty.hidden = channels.length > 0") {
		t.Fatal("the channels-empty note must hide once a real channel exists")
	}
	// the row's pressed highlight tracks the actually-open thread
	if !strings.Contains(renderBody, "chatDefaultThread.setAttribute('aria-pressed', selectedScoutChatThread() ? 'false' : 'true')") {
		t.Fatal("the default Scout row's aria-pressed must follow the open thread")
	}

	// transcript/brain pulses ride the 15s intel cache — only a fresh
	// mission_insight or decision event (each carrying new synthesized
	// content with no cached payload to patch) may force the canvas fetch
	kanbanBody := functionBody(html, "function handleKanbanMessage(message)")
	if strings.Count(kanbanBody, "loadMissionIntelligence(true)") > 2 || !strings.Contains(kanbanBody, "loadMissionIntelligence()") {
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

// Frontend wiring guard for the A7 voice-tool surfaces: the wake-word visual
// pulse (server "wake" assistant event → brand-mark/voice-island breathe),
// the grill island label, and the channel-notification deep link must stay
// wired together.
func TestIndexWakePulseGrillLabelAndChannelDeepLinkWiring(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	for _, want := range []string{
		// wake pulse — CSS: separate is-wake class so setConnectionState's
		// is-listening ownership is never fought, breathe hook extended
		".topbar__mark.is-wake {",
		"#appShell:has(.topbar__mark.is-wake)",
		".voice-island.is-wake .bf-wave-bar {",
		// wake pulse — JS: debounced one-cycle pulse, island only while calm
		"function pulseScoutWake()",
		"const WAKE_PULSE_DEBOUNCE = 3000",
		"const WAKE_PULSE_DURATION = 2400",
		"brandMarkEl.classList.add('is-wake')",
		// grill status relabels the voice island while active
		"`grilling — ${String(event?.topic || '').trim() || 'the room'}`",
		// channel notifications deep-link to the thread
		"threadId: String(payload.threadId || '')",
		"selectScoutChatThread(entry.threadId)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing A7 voice-tool wiring %q", want)
		}
	}

	assistantBody := functionBody(html, "function handleAssistantEvent(event)")
	if !strings.Contains(assistantBody, "event?.kind === 'wake'") || !strings.Contains(assistantBody, "pulseScoutWake()") {
		t.Fatal("handleAssistantEvent must route the wake event to the visual pulse (and never the feed)")
	}

	openEntryBody := functionBody(html, "function openNotificationEntry(entry)")
	if !strings.Contains(openEntryBody, "if (entry.threadId) {") || !strings.Contains(openEntryBody, "setActiveTool('chat')") {
		t.Fatal("openNotificationEntry must deep-link threadId notifications into chat before the artifact route")
	}

	// nothing may loop under reduced motion: both wake selectors are covered
	reduced := html[strings.LastIndex(html, "@media (prefers-reduced-motion: reduce)"):]
	for _, want := range []string{".topbar__mark.is-wake svg", ".voice-island.is-wake .bf-wave-bar"} {
		if !strings.Contains(reduced, want) {
			t.Fatalf("reduced-motion block missing wake pulse coverage %q", want)
		}
	}
}

// Frontend wiring guard for the office-shell resilience fixes: a
// network-failed /auth/me never signs the tab out, the duplicated
// server_shutdown delivery on dual-socket tabs is suppressed, broadcast run
// cards only land in chat panes that reference them, and chat-thread
// auto-select respects the scope pill so labels stay truthful.
func TestIndexOfficeShellResilienceGuards(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	// refreshAuthState: only a real 401/403 clears authedUser — a thrown
	// fetch or a proxy 5xx during a deploy keeps the signed-in state so the
	// office socket backoff can carry the tab across a server restart.
	authBody := functionBody(html, "async function refreshAuthState()")
	if !strings.Contains(authBody, "response.status === 401 || response.status === 403") {
		t.Fatal("refreshAuthState must clear authedUser only on a real 401/403")
	}
	if strings.Contains(authBody, "response.ok ? await response.json() : null") {
		t.Fatal("refreshAuthState must not treat every non-ok /auth/me as signed-out")
	}
	if !strings.Contains(authBody, "// a network-failed /auth/me (server restarting, wifi blip) must") {
		t.Fatal("refreshAuthState catch must keep authedUser on network failure")
	}

	// server_shutdown rides the union fan-out: an in-room tab receives it on
	// both sockets, and the second delivery must not burn another reconnect
	// attempt or re-arm the pending retry timer.
	shutdownBody := functionBody(html, "function handleServerShutdown(payload)")
	if !strings.Contains(shutdownBody, "if (isSignalReconnecting) {") {
		t.Fatal("handleServerShutdown must dedupe the room+office union delivery via isSignalReconnecting")
	}

	// Broadcast assistant events inject run cards only into panes that own
	// them: an existing card flips in place, otherwise the active thread
	// must reference the run (voice/board launches stay out of open panes).
	for _, want := range []string{
		"function activeScoutThreadReferencesRun(artifactId, runId)",
		"if (existingRunCard || activeScoutThreadReferencesRun(cardArtifactId, thread?.id)) {",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing run-card ownership marker %q", want)
		}
	}
	// The follow-up affordance mirrors the server rule (artifact must be
	// referenced by the target thread) instead of arming a guaranteed 400.
	if !strings.Contains(html, "activeScoutThreadReferencesRun(card.dataset.threadArtifactId, card.dataset.threadRunId)") {
		t.Fatal("run-card follow-up must be gated on the active thread referencing the artifact")
	}

	// card 070: with both sections always rendered there is no scope pill to
	// sync — auto-select on archive/reload lands on a fallback thread of the
	// archived one's audience (channel→channel, private→private), else newest.
	for _, want := range []string{
		"function fallbackScoutThreadIdForScope(reference)",
		"activeScoutThreadId = fallbackScoutThreadIdForScope()",
		"activeScoutThreadId = fallbackScoutThreadIdForScope(thread)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing fallback-selection marker %q", want)
		}
	}
	if strings.Contains(html, "activeScoutThreadId = scoutChatThreads[0]?.id || ''") {
		t.Fatal("thread fallback selection must go through fallbackScoutThreadIdForScope, not scoutChatThreads[0]")
	}
	// the retired scope-sync helper must be fully gone
	if strings.Contains(html, "syncChatScopeToSelectedThread") {
		t.Fatal("syncChatScopeToSelectedThread must be removed with the scope toggle")
	}
}

// TestIndexBonfireOSRenameAndAgentToken pins the Spectacular OS Wave 5
// design-system foundation: the "Office" tab renamed to BonfireOS everywhere it
// surfaces, and the single agent-working ember accent token. A future refactor
// that drops the rename or the token fails CI here. The load-bearing `office`
// data-tool KEY must survive the rename — it is asserted to stay.
func TestIndexBonfireOSRenameAndAgentToken(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	// The rename surfaces (labels only).
	for _, want := range []string{
		`<span class="tool-rail__label">BonfireOS</span>`,
		`data-tool="office" aria-label="BonfireOS home" aria-pressed="true"`,
		`aria-label="Back to BonfireOS"`,
		`id="officeTool" class="office-tool" aria-label="BonfireOS"`,
		"office: 'BonfireOS',",
		"? 'BonfireOS'",
		"|| 'BonfireOS'",
		"'ready'",
		// the one warm ignition accent (coral ember), migrated from the
		// heritage flame; --agent now aliases the sanctioned --ember token
		"--ember-500: #FF6B4A;",
		"--agent: var(--ember);",
		"--agent-soft:",
		"--glow-agent:",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing BonfireOS rename / agent-token marker %q", want)
		}
	}

	// The `office` data-tool KEY is load-bearing and must never be renamed.
	for _, key := range []string{
		`<main id="appShell" data-tool="office">`,
		`data-tool="office"`,
		"const TOOL_IDS = ['office', 'room', 'chat', 'artifacts', ...agentToolIds, 'board', 'memory', 'files']",
	} {
		if !strings.Contains(html, key) {
			t.Fatalf("index.html dropped the load-bearing office data-tool key %q", key)
		}
	}

	// The rename must not leave the tab labelled "Office" in the title map.
	if strings.Contains(html, "office: 'Office',") {
		t.Fatal("toolTitles still maps office -> 'Office'; expected 'BonfireOS'")
	}
	// The ember accent (via the --agent alias) is defined in BOTH theme blocks.
	if got := strings.Count(html, "--agent: var(--ember);"); got < 2 {
		t.Fatalf("--agent must alias --ember in both theme blocks, found %d", got)
	}
}
