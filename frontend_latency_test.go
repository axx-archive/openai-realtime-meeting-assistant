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
		"screenShareMaxBitrate: 2500000",
		"parameters.degradationPreference = isScreenShare",
		": 'maintain-framerate'",
		"function remoteVideoStreamForTrack(stream, videoTrack)",
		"function mediaStreamTrackSignature(stream)",
		"function videoPlaybackStreamForElement(video, stream)",
		"return new MediaStream(stream.getTracks().filter(liveTrack))",
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
		`<main id="appShell" class="is-rail-hidden" data-tool="office">`,
		`data-tool="office" aria-label="Home" title="Home"`,
		`data-tool="room" aria-label="The room" title="The room"`,
		`data-tool="chat" aria-label="Chat" title="Chat"`,
		`id="topbarRailToggle" class="rail-switch" type="button" role="switch" aria-label="toggle nav bar" aria-checked="false"`,
		`id="officeRailToggle" class="rail-switch" type="button" role="switch" aria-label="toggle nav bar" aria-checked="false"`,
		"const railHiddenStorageKey = 'bonfire.rail.hidden.v1'",
		"let railHidden = loadRailHiddenPreference()",
		"function loadRailHiddenPreference()",
		"window.localStorage?.getItem(railHiddenStorageKey)",
		"function persistRailHiddenPreference(hidden)",
		"window.localStorage?.setItem(railHiddenStorageKey, hidden ? 'hidden' : 'visible')",
		"function setRailHidden(hidden, options = {})",
		"setRailHidden(railHidden, { persist: false })",
		"setRailHidden(true, { persist: false })",
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
		`data-tool="artifacts"`,
		"function renderArtifacts()",
		`id="chatAgentThreads" class="chat-agent-threads"`,
		"event: 'scout_chat_reset'",
		"function renderChatAgentThreads()",
		"async function runAgentTool(event)",
		"function renderAgentWorkspaces()",
		"function renderResearchArtifactList(activeEntry)",
		"function renderResearchJobEntry(entry, activeEntry)",
		"function scoutThreadModeForText(text)",
		"function updateScoutChatResearchNode(card, status, artifact)",
		`class="scout-chat-research__flow"`,
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
		"--shell-topbar-height: 62px;",
		`"topbar topbar"`,
		`id="brandMark" class="topbar__mark" role="img" aria-label="Bonfire"`,
		".tool-rail:hover,",
		".tool-rail__label",
		`id="accountMenuButton" class="topbar__account-button"`,
		`id="themeToggle" class="tool-rail__tool tool-rail__theme" type="button" aria-label="Switch theme" title="Switch theme" aria-pressed="false"`,
		`id="profileDisplayName" type="text" autocomplete="name"`,
		`id="profileAvatarInput" type="file" accept="image/png,image/jpeg,image/webp,image/gif" hidden`,
		"async function saveAccountProfile(event)",
		"postAuthJSON('/auth/profile'",
		`id="recordMeeting" class="btn btn--ghost btn--recording is-recording"`,
		"event: 'set_recording'",
		"function updateRoomRecordingControls()",
		"roomRecordingEnabled = recording.enabled !== false",
		".tool-rail__slot[hidden]",
		".tool-rail__avatar[hidden]",
		"display: none !important;",
		"const agentToolIds = ['research', 'design', 'grill']",
		"const TOOL_IDS = ['office', 'room', 'chat', 'artifacts', ...agentToolIds, 'board', 'memory']",
		`data-tool="research" aria-label="Research" title="Research"`,
		`data-tool="design" aria-label="Design" title="Design"`,
		".topbar__nav-toggle > span",
		`id="artifactReadPane" class="artifact-read" aria-label="artifact preview"`,
		"function artifactSections(entry)",
		"function renderArtifactRead(container, entry, options = {})",
		"function artifactPreviewText(entry)",
		"function renderDesignTool()",
		"data-design-context",
		"renderArtifactRead(output, entry, { surface: 'design' })",
		"openOfficeTool(mode === 'research' || mode === 'design' ? mode : 'artifacts')",
		"appShell.classList.toggle('is-authed', Boolean(authedUser))",
		"setActiveTool('office')",
		"stopRealtimeVoiceConversation({ notifyServer: false })",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing waveform home or assistant marker %q", want)
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
	for _, visibleTool := range []string{`data-tool="office"`, `data-tool="room"`, `data-tool="chat"`, `data-tool="artifacts"`} {
		if !strings.Contains(html, visibleTool) {
			t.Fatalf("prototype rail tool %s should be present in the OS rail", visibleTool)
		}
	}
	for _, hiddenTool := range []string{`data-tool="research"`, `data-tool="design"`, `data-tool="grill"`, `data-tool="board"`, `data-tool="memory"`} {
		if !strings.Contains(html, hiddenTool) {
			t.Fatalf("off-rail tool %s should remain addressable for assistant/tool routing", hiddenTool)
		}
	}
	if !strings.Contains(html, `id="toolBoard" class="tool-rail__tool" type="button" data-tool="board" aria-label="Board" title="Board" aria-pressed="false" disabled`) {
		t.Fatal("board tool should remain addressable but disabled until the room is ready")
	}
	if !strings.Contains(html, `id="themeToggle" class="tool-rail__tool tool-rail__theme" type="button" aria-label="Switch theme" title="Switch theme" aria-pressed="false"`) {
		t.Fatal("left rail theme toggle should be visible at the bottom of the rail")
	}
	if strings.Contains(html, `id="themeToggle" class="tool-rail__tool tool-rail__theme" type="button" aria-label="Switch theme" title="Switch theme" aria-pressed="false" hidden`) {
		t.Fatal("left rail theme toggle should not be hidden")
	}
	if !strings.Contains(html, `id="railAvatar" class="tool-rail__tool tool-rail__signout" type="button" aria-label="Sign out" title="Sign out" aria-pressed="false"`) {
		t.Fatal("prototype rail should expose sign out at the bottom")
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

	openBody := functionBody(html, "function setAccountMenuOpen(open)")
	if !strings.Contains(openBody, "profileDisplayName.value = authedUser.name || authedUser.email || ''") {
		t.Fatal("account menu open should initialize the display-name form once")
	}
	saveBody := functionBody(html, "async function saveAccountProfile(event)")
	if !strings.Contains(saveBody, "profileDisplayName.value = authedUser.name || ''") {
		t.Fatal("successful profile save should reinitialize the form from the persisted identity")
	}
	if !strings.Contains(html, "profileAvatarDraft = ''") || !strings.Contains(html, "avatar cleared. save profile to keep it.") {
		t.Fatal("avatar clear should mark a draft clear without resetting the typed display name")
	}
}

func TestIndexAccountMenuAndExpandableRailInteractionsAreWired(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	html := string(rawHTML)
	for _, want := range []string{
		`"topbar topbar"`,
		`id="brandMark" class="topbar__mark" role="img" aria-label="Bonfire"`,
		"position: sticky;",
		"top: var(--shell-topbar-height);",
		"height: calc(100svh - var(--shell-topbar-height));",
		"align-self: flex-start;",
		"width: 68px;",
		"padding: 0 20px 0 0;",
		".tool-rail:hover,",
		".tool-rail:focus-within",
		"width: 228px;",
		"max-width: 140px;",
		".tool-rail:hover .tool-rail__theme,",
		".tool-rail:focus-within .tool-rail__theme",
		`<span class="tool-rail__label">office</span>`,
		`<span class="tool-rail__label">the room</span>`,
		`<span class="tool-rail__label">chat</span>`,
		`aria-haspopup="dialog"`,
		`role="dialog" aria-label="Account menu"`,
		"accountMenuButton.addEventListener('click'",
		"setAccountMenuOpen(accountMenu.hidden)",
		"if (!accountMenu.hidden && !topbarAccount.contains(event.target))",
		"if (!accountMenu.hidden) {\n            setAccountMenuOpen(false)",
		"openAudioSettings({ allowLocked: true, restoreFocusTo: accountMenuButton })",
		"accountMenuSignOut.addEventListener('click', signOutOfAccount)",
		"#appShell:has(#accountMenuButton[aria-expanded=\"true\"]) .topbar",
		"max-width: min(220px, 26vw);",
		"display: block;",
		"width: min(340px, calc(100vw - 28px));",
		"max-width: 44px;",
		"padding: 0 12px 0 0;",
		"width: min(204px, 78vw);",
		"width: min(340px, calc(100vw - 76px));",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing account menu or rail interaction marker %q", want)
		}
	}
	if strings.Contains(html, `class="tool-rail__flame"`) {
		t.Fatal("brand flame should stay anchored in the topbar above the rail")
	}
	if strings.Contains(html, `id="topbarAccountEmail"`) || strings.Contains(html, `class="topbar__account-email"`) {
		t.Fatal("topbar account trigger should not render an email line")
	}
	linkBlock := functionBody(html, ".account-menu__link")
	if !strings.Contains(linkBlock, "min-height: 44px;") {
		t.Fatal("account menu settings/sign-out rows must keep a 44px touch target")
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

func TestIndexKeepsScreenShareTrackAndParticipantStrip(t *testing.T) {
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
		"function outboundTrackForKind(kind)",
		"return kind === 'video' ? outboundVideoTrack() : localAudioTrack()",
		"const track = outboundTrackForKind(section.kind)",
		".presentation-tile.is-screen-sharing {",
		"grid-template-columns: minmax(0, 1fr) 200px;",
		".presentation-tile.is-screen-sharing .hearth-stage {",
		".presentation-tile.is-screen-sharing .hearth-seats,",
		".presentation-tile.is-screen-sharing .hearth-seat.is-sharing-screen",
		"grid-template-rows: minmax(0, 1fr) auto;",
		"video: screenStageVideo",
		"? screenShareStream",
		"ignoreCameraOff: true",
		"!target.ignoreCameraOff && participantMediaState(target.name).cameraOff",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing screen-share track or participant-strip guard %q", want)
		}
	}

	if strings.Contains(html, ".presentation-tile.is-screen-sharing .hearth-stage {\n        display: none;") {
		t.Fatal("screen sharing must keep the room video strip mounted, not hide the hearth stage")
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
		"const audioSettingsSchemaVersion = 5",
		"mode: 'standard'",
		"window.localStorage?.getItem(audioSettingsStorageKey)",
		"version: audioSettingsSchemaVersion",
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
		"#appShell.is-in-room:not(.is-board-expanded) .meeting-bar",
		"position: fixed;",
		"width: fit-content;",
		".video-tile.is-active-speaker",
		".hearth-stage[data-stage-mode=\"gallery\"] .hearth-seat.is-active-speaker",
		".board-video-tile.is-speaker",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing locked controls or green speaker accent %q", want)
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
		"voiceFocus: voiceFocusEnabled()",
		"voiceFocusMetrics: voiceFocusDiagnosticsSnapshot()",
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
		"background: rgba(32, 33, 37, 0.94);",
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
