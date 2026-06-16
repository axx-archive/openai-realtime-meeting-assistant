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
