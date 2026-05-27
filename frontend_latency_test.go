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
		"width: { ideal: 640, max: 854 }",
		"maxBitrate: 480000",
		"groupMaxBitrate: 320000",
		"constrainedMaxBitrate: 220000",
		"function useGroupVideoLimits()",
		"configureOutboundSenders().catch(error => {",
		"function startMediaQualityMonitor(sessionPeer)",
		"function constrainCameraForLag(reason)",
		"screenShareMaxBitrate: 1600000",
		"parameters.degradationPreference = isScreenShare",
		": 'maintain-framerate'",
		"function remoteVideoStreamForTrack(stream, videoTrack)",
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
		"const safariBrowser = /^((?!chrome|android|crios|fxios|edgios).)*safari/i.test(navigator.userAgent)",
		"function scheduleConnectionRecovery(sessionPeer)",
		"const connectionRecoveryDelayMs = 20000",
		"function requestIceRestart(reason)",
		"event: 'restart_ice'",
		"state === 'disconnected'",
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
		"state.noiseBias = Math.min(state.noiseFloor * biasMultiplier",
		"const blend = Math.min(1, Math.max(0, (rms - closeAt)",
		"voiceIsolation: { ideal: voiceFocusEnabled() }",
		"suppressLocalAudioPlayback: { ideal: audioProcessingEnabled() }",
		"function trainVoiceFocus()",
		"localAudioSourceTrack?.getSettings?.().deviceId",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing voice focus noise reduction %q", want)
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
		"const audioSettingsSchemaVersion = 2",
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
		"--speaker-accent: #34D399",
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
		"function shouldUseSyncedRemoteAudioPlayback()",
		"return false",
		"demoteRemotePlaybackElementFromVideo(video, tile.dataset.participant)",
		"audio = createRemoteAudioElement(stream, name)",
		"remoteVideoTracksByParticipant.set(participantName, track)",
		"const playbackElement = shouldUseSyncedRemoteAudioPlayback() ? remoteVideoElementForParticipant(name) : null",
		"attachAudioMonitor(key, name, event.track, { play: true, playbackStream: stream, playbackElement })",
		"playbackGain.connect(context.destination)",
		"monitor.playbackGain?.disconnect()",
		"function remotePlaybackNeedsGesture(element)",
		"function remotePlaybackPendingCount(options = {})",
		"function roomAudioPlaybackBlocked()",
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
		"function sendMediaQualityReport(snapshot, previous, laggy = false)",
		"event: 'media_quality'",
		"function reportClientMediaError(stage, error)",
		"event: 'media_error'",
		"lastError.mediaAttempts = failures",
		"function trackSettingsSnapshot(track, keys)",
		"function voiceFocusProcessorType()",
		"sourceSettings: trackSettingsSnapshot(localAudioSourceTrack || localAudioTrack()",
		"voiceFocus: voiceFocusEnabled()",
		"summarizeCandidatePair(selectedCandidatePair, report)",
		"function mediaQualityDelta(snapshot, previous)",
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
