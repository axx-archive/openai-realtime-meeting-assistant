package main

// 2026-07-10 PLI/keyframe spiral (room StationTenn): one lossy mobile
// subscriber melted the room. Congestion froze EVERY remote tile at once and
// two client behaviours amplified it:
//
//   C1 — the deaf/byte-stall branch applied is-video-stalled (avatar swap)
//        after only 2s, so under transient congestion every tile blinked
//        video↔avatar ("flashing on and off"). The cure: HOLD the last
//        decoded frame through transient stalls; the avatar covers the tile
//        only after a LONG outage aligned with the ladder's rung-1 threshold
//        (12s). The ORIGINAL frames-stalled-while-bytes-flow path keeps its
//        pre-3c9971b behaviour exactly (class at 2s).
//
//   C2 — 193 request_participant_tracks in ~4min (each one ends in a global
//        keyframe walk server-side, no member rate limit). The cure: a
//        room-sickness circuit breaker (≥2 sick remote tiles, or >50% when
//        more than 3 are tracked, is CONGESTION — suppress the ladder and
//        the congestion repair sends) plus a global 4s send floor between
//        ANY two request_participant_tracks sends, force flag included,
//        with a post-join burst window so join-time adoption stays instant.

import (
	"os"
	"strings"
	"testing"
)

func readIndexHTMLForCongestion(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(data)
}

// C1: the deaf/byte-stall branch must NOT swap to the avatar at the 2s stall
// threshold. It still marks the track frozen at 2s (the breaker and the
// health snapshot need the sick signal) but the is-video-stalled class — the
// avatar swap — waits for the 12s long-outage threshold. The original
// frames-stalled-while-bytes-flow path keeps its pre-3c9971b 2s class.
func TestIndexDeafBranchHoldsLastFrameUntilLongOutage(t *testing.T) {
	html := readIndexHTMLForCongestion(t)

	if !strings.Contains(html, "const remoteVideoDeafAvatarHoldMs = 12000") {
		t.Error("index.html is missing the 12s deaf avatar-hold threshold (aligned with the ladder's rung 1)")
	}

	sampleBody := functionBody(html, "function noteRemoteVideoFreezeSample(track, inbound, now)")
	if sampleBody == "" {
		t.Fatal("could not extract noteRemoteVideoFreezeSample body")
	}

	// Slice out the deaf branch: from the deafTile lookup to the quiet muted
	// early-return that follows it.
	deafAt := strings.Index(sampleBody, "const deafTile = deafSubscriptionRemoteTile(track, framesDecoded)")
	mutedReturnAt := strings.Index(sampleBody, "if (track.muted) {")
	if deafAt == -1 || mutedReturnAt == -1 || deafAt > mutedReturnAt {
		t.Fatal("noteRemoteVideoFreezeSample lost its deaf branch / muted early-return ordering")
	}
	deafBranch := sampleBody[deafAt:mutedReturnAt]

	// the sick marker still lands at the 2s stall threshold
	if !strings.Contains(deafBranch, "frozenRemoteVideoTrackIds.add(track.id)") {
		t.Error("the deaf branch must still mark the track frozen at the stall threshold (breaker + health need the signal)")
	}
	// ...but the avatar swap is gated behind the 12s hold
	holdGateAt := strings.Index(deafBranch, "if (frozenMs >= remoteVideoDeafAvatarHoldMs) {")
	classAddAt := strings.Index(deafBranch, "deafTile.classList.add('is-video-stalled')")
	if holdGateAt == -1 {
		t.Fatal("the deaf branch is missing the remoteVideoDeafAvatarHoldMs gate")
	}
	if classAddAt == -1 {
		t.Fatal("the deaf branch lost its is-video-stalled avatar cover entirely (long outages must still swap)")
	}
	if classAddAt < holdGateAt {
		t.Error("the deaf branch applies is-video-stalled BEFORE the 12s hold gate — that is the 2s blink regression")
	}
	if strings.Count(deafBranch, "classList.add('is-video-stalled')") != 1 {
		t.Error("the deaf branch must have exactly one avatar-swap site (inside the 12s hold gate)")
	}

	// The frames-stalled-while-bytes-flow path marks the tile SICK at the 2s
	// stall threshold (census + health need the signal) but now HOLDS the last
	// frame until 4s before the avatar swap. The 7a56e46 2s-parity premise is
	// invalidated by a server change (finding 4): the per-source keyframe floor
	// was raised to 2.5s, so covering the tile at 2s would self-blink against
	// the keyframe already on the way. Sick-marking stays at 2s; only the
	// class-swap moved to 4s.
	if !strings.Contains(html, "const remoteVideoFrozenAvatarSwapMs = 4000") {
		t.Error("index.html is missing the 4s frozen-path avatar-swap threshold")
	}
	frozenPath := sampleBody[mutedReturnAt:]
	if !strings.Contains(frozenPath, "if (frozenMs < remoteVideoFreezeStallMs) {") {
		t.Error("the frozen path must still gate sick-marking at the 2s stall threshold")
	}
	sickMarkAt := strings.Index(frozenPath, "frozenRemoteVideoTrackIds.add(track.id)")
	swapGateAt := strings.Index(frozenPath, "if (tile && frozenMs >= remoteVideoFrozenAvatarSwapMs) {")
	frozenClassAt := strings.Index(frozenPath, "tile.classList.add('is-video-stalled')")
	if sickMarkAt == -1 || swapGateAt == -1 || frozenClassAt == -1 {
		t.Fatal("the frozen path lost its 2s sick-marking or its 4s-gated avatar swap")
	}
	if sickMarkAt > swapGateAt {
		t.Error("the frozen path must mark the tile sick (2s) before it gates the 4s avatar swap")
	}
	if frozenClassAt < swapGateAt {
		t.Error("the frozen path avatar swap must sit behind the 4s threshold (finding 4), not fire at 2s")
	}

	// recovery clears promptly: the frames-advanced branch removes the class
	advanceAt := strings.Index(sampleBody, "if (framesAdvanced) {")
	if advanceAt == -1 || advanceAt > deafAt {
		t.Fatal("noteRemoteVideoFreezeSample lost its framesAdvanced recovery branch")
	}
	if !strings.Contains(sampleBody[advanceAt:deafAt], "tile.classList.remove('is-video-stalled')") {
		t.Error("the recovery branch must clear is-video-stalled as soon as frames advance")
	}
}

// C2a: room-sickness circuit breaker — ≥2 sick remote tiles (or >50% when
// more than 3 are tracked) is CONGESTION, not a dead binding. It rides the
// freeze watch's own census, opens/closes with one logRoomRecovery line, and
// resets with the watch.
func TestIndexRoomCongestionBreakerThresholds(t *testing.T) {
	html := readIndexHTMLForCongestion(t)

	for _, want := range []string{
		"const roomCongestionBreakerMinSickTiles = 2",
		"const roomCongestionBreakerSickFraction = 0.5",
		"let roomCongestionBreakerOpen = false",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html is missing the breaker state %q", want)
		}
	}

	update := functionBody(html, "function updateRoomCongestionBreaker(now)")
	if update == "" {
		t.Fatal("updateRoomCongestionBreaker is missing")
	}
	for _, want := range []string{
		"sickTiles >= roomCongestionBreakerMinSickTiles",
		"trackedTiles <= 3 || sickTiles > trackedTiles * roomCongestionBreakerSickFraction",
		"'room_congestion_breaker_open' : 'room_congestion_breaker_closed'",
		"logRoomRecovery(",
	} {
		if !strings.Contains(update, want) {
			t.Errorf("updateRoomCongestionBreaker is missing %q", want)
		}
	}

	// the census rides the freeze watch's per-track state — sick = marked
	// frozen/deaf, advancing = frames decoded within the stall window — and
	// counts only FRAME-CAPABLE tiles (finding 2): a roster-declared camera-off
	// tile is excluded from BOTH the sick numerator and the tracked denominator
	// so it cannot dilute the breaker or mask a 1:1 room's single dead binding.
	census := functionBody(html, "function remoteVideoCongestionCensus(now)")
	if census == "" {
		t.Fatal("remoteVideoCongestionCensus is missing")
	}
	for _, want := range []string{
		"frozenRemoteVideoTrackIds.has(",
		"now - state.lastAdvanceAt < remoteVideoFreezeStallMs",
		"participantMediaStates.has(name)",
		"participantMediaState(name).cameraOff",
	} {
		if !strings.Contains(census, want) {
			t.Errorf("remoteVideoCongestionCensus is missing %q", want)
		}
	}

	// the breaker updates on every freeze-watch poll pass and resets with it
	poll := functionBody(html, "async function pollRemoteVideoFreezeStats(sessionPeer)")
	if poll == "" {
		t.Fatal("could not extract pollRemoteVideoFreezeStats body")
	}
	if !strings.Contains(poll, "updateRoomCongestionBreaker(") {
		t.Error("pollRemoteVideoFreezeStats must update the congestion breaker each pass")
	}
	stop := functionBody(html, "function stopRemoteVideoFreezeWatch()")
	if !strings.Contains(stop, "roomCongestionBreakerOpen = false") {
		t.Error("stopRemoteVideoFreezeWatch must reset the congestion breaker")
	}
}

// C2a suppression wiring: while the breaker is open, the escalation ladder
// (both rungs) and the congestion repair sends stay quiet. The ladder may
// only fire on the true deaf-BINDING signature — exactly ONE sick tile while
// at least one other remote tile is advancing frames (the "Tom" cure).
func TestIndexCongestionBreakerSuppressesRepairsAndLadder(t *testing.T) {
	html := readIndexHTMLForCongestion(t)

	ladder := functionBody(html, "function escalateStaleRemoteVideoTile(track, state, frozenMs, now)")
	if ladder == "" {
		t.Fatal("could not extract escalateStaleRemoteVideoTile body")
	}
	breakerGateAt := strings.Index(ladder, "if (roomCongestionBreakerOpen) {")
	// The dead-binding signature now includes the 1:1 / camera-off exception
	// (finding 2): exactly one frame-capable sick tile AND (a peer advancing OR
	// it is the only frame-capable remote tile). The census counts only
	// frame-capable tiles, so trackedTiles === 1 means a genuine 1:1 room.
	signatureGateAt := strings.Index(ladder, "sickTiles !== 1 || (advancingTiles < 1 && trackedTiles !== 1)")
	rung1At := strings.Index(ladder, "forceParticipantTrackRefresh('stale tile escalation')")
	if breakerGateAt == -1 {
		t.Error("the escalation ladder must be suppressed while the congestion breaker is open")
	}
	if signatureGateAt == -1 {
		t.Error("the ladder must require the single-sick-tile signature with the 1:1 / only-frame-capable-tile exception")
	}
	if rung1At == -1 || (breakerGateAt != -1 && breakerGateAt > rung1At) || (signatureGateAt != -1 && signatureGateAt > rung1At) {
		t.Error("both congestion gates must precede rung 1 (they gate BOTH rungs)")
	}
	// the ladder reads all three census fields so the 1:1 exception can fire
	if !strings.Contains(ladder, "const { sickTiles, advancingTiles, trackedTiles } = remoteVideoCongestionCensus(now)") {
		t.Error("the ladder must read trackedTiles from the census for the 1:1 exception")
	}

	// the frozen path's 6s repair block (refresh + 'frozen remote video'
	// send) is gated on the breaker
	sampleBody := functionBody(html, "function noteRemoteVideoFreezeSample(track, inbound, now)")
	if !strings.Contains(sampleBody, "if (!roomCongestionBreakerOpen && now - state.lastRepairAt >= remoteVideoFreezeRepairIntervalMs) {") {
		t.Error("the frozen-path repair block must be suppressed while the congestion breaker is open")
	}

	// tile dispose+recreate churn is congestion fuel — the stalled-tile
	// refresher goes quiet wholesale while the breaker is open
	refresher := functionBody(html, "function refreshStalledRemoteVideoTiles(reason = '')")
	if !strings.Contains(refresher, "if (roomCongestionBreakerOpen) {") {
		t.Error("refreshStalledRemoteVideoTiles must no-op while the congestion breaker is open")
	}

	// the congestion repair reasons go quiet at the send gate too (the
	// media-quality pass routes through requestParticipantTrackRefresh)
	for _, want := range []string{
		"const congestionSuppressedRepairReasons = new Set([",
		"'frozen remote video'",
		"'media quality monitor'",
		"'stalled remote video'",
		"'remote media health'",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html is missing the suppressed-reason set entry %q", want)
		}
	}
	request := functionBody(html, "function requestParticipantTrackRefresh(reason = '')")
	if !strings.Contains(request, "roomCongestionBreakerOpen && congestionSuppressedRepairReasons.has(reason)") {
		t.Error("requestParticipantTrackRefresh must drop congestion-repair sends while the breaker is open")
	}
}

// C2b: global send floor — minimum 4s between ANY two
// request_participant_tracks sends from this client, reason and force flag
// included. The force path keeps bypassing the 900ms throttle but funnels
// through the one send that enforces the floor. Join-time adoption stays
// instant via a post-join burst window (own session start AND a remote
// participant_joined both arm it).
func TestIndexParticipantTrackRefreshGlobalFloor(t *testing.T) {
	html := readIndexHTMLForCongestion(t)

	for _, want := range []string{
		"const participantTrackRefreshFloorMs = 4000",
		"const participantTrackRefreshJoinBurstMs = 10000",
		"const participantTrackRefreshJoinBurstFloorMs = 2000",
		"let participantTrackRefreshJoinBurstUntil = 0",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html is missing the send-floor state %q", want)
		}
	}

	send := functionBody(html, "function sendParticipantTrackRefresh(reason)")
	if send == "" {
		t.Fatal("could not extract sendParticipantTrackRefresh body")
	}
	for _, want := range []string{
		// inside the join burst the 4s floor is lifted for instant adoption but
		// sends are still paced at >=2s so the burst fits the server request
		// bucket — the bare 900ms throttle would overrun it (finding 6)
		"now < participantTrackRefreshJoinBurstUntil",
		"? participantTrackRefreshJoinBurstFloorMs",
		": participantTrackRefreshFloorMs",
		"now - lastParticipantTrackRefreshAt < floorMs",
		// a floored send must NOT stamp the clock (it did not send) — finding 5
		"return false",
		"lastParticipantTrackRefreshAt = now",
		"return true",
	} {
		if !strings.Contains(send, want) {
			t.Errorf("sendParticipantTrackRefresh is missing the floor wiring %q", want)
		}
	}
	floorAt := strings.Index(send, "now - lastParticipantTrackRefreshAt < floorMs")
	stampAt := strings.Index(send, "lastParticipantTrackRefreshAt = now")
	sendAt := strings.Index(send, "ws.send(")
	if floorAt == -1 || stampAt == -1 || sendAt == -1 || floorAt > stampAt || stampAt > sendAt {
		t.Error("the floor gate must precede the clock stamp, which must precede the actual send")
	}

	// the force path may bypass the 900ms throttle but NOT the floor: it must
	// not own a second ws.send — everything funnels through the floored send
	forced := functionBody(html, "function forceParticipantTrackRefresh(reason = '')")
	if forced == "" {
		t.Fatal("forceParticipantTrackRefresh is missing")
	}
	if strings.Contains(forced, "ws.send(") {
		t.Error("forceParticipantTrackRefresh must funnel through sendParticipantTrackRefresh so the 4s floor applies to forced sends")
	}
	if !strings.Contains(forced, "sendParticipantTrackRefresh(reason)") {
		t.Error("forceParticipantTrackRefresh must call the shared floored send")
	}

	// join burst: own session start (fresh session peer) and a remote
	// participant joining both arm the instant-adoption window
	arm := functionBody(html, "function armParticipantTrackRefreshJoinBurst()")
	if !strings.Contains(arm, "participantTrackRefreshJoinBurstUntil = performance.now() + participantTrackRefreshJoinBurstMs") {
		t.Error("armParticipantTrackRefreshJoinBurst must stamp the burst window")
	}
	watch := functionBody(html, "function startRemoteVideoFreezeWatch(sessionPeer)")
	if !strings.Contains(watch, "armParticipantTrackRefreshJoinBurst()") {
		t.Error("a fresh session peer must arm the join burst window (join-time adoption stays instant)")
	}
	joined := functionBody(html, "function handleParticipantJoined(participant)")
	if !strings.Contains(joined, "armParticipantTrackRefreshJoinBurst()") {
		t.Error("a remote participant joining must arm the join burst window (their first-attach stays instant)")
	}
}

// MUST-FIX 1: a stuck-open breaker must not suppress the multi-tile wedge cure
// forever. While the breaker stays open, a half-open probe cycle lets ONE cure
// request through every 20s — stage 0 a forced request_participant_tracks
// (still floored), stage 1 the single budgeted restart_ice — then repeats. A
// breaker transition (including close on frames advancing) resets the cycle, so
// storm damping still holds (~1 probe/20s) while the wedge cure is restored.
func TestIndexRoomCongestionBreakerHalfOpenProbe(t *testing.T) {
	html := readIndexHTMLForCongestion(t)

	for _, want := range []string{
		"const roomCongestionBreakerProbeMs = 20000",
		"let roomCongestionBreakerLastProbeAt = 0",
		"let roomCongestionBreakerProbeStage = 0",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html is missing the half-open probe state %q", want)
		}
	}

	// the breaker update drives the probe while open, and a transition resets
	// the probe clock + stage (so the first probe is one full interval out and
	// a close disarms the escape hatch)
	update := functionBody(html, "function updateRoomCongestionBreaker(now)")
	if update == "" {
		t.Fatal("updateRoomCongestionBreaker is missing")
	}
	for _, want := range []string{
		"if (roomCongestionBreakerOpen) {",
		"maybeProbeRoomCongestionBreaker(now)",
		"roomCongestionBreakerLastProbeAt = now",
		"roomCongestionBreakerProbeStage = 0",
	} {
		if !strings.Contains(update, want) {
			t.Errorf("updateRoomCongestionBreaker is missing the probe wiring %q", want)
		}
	}

	probe := functionBody(html, "function maybeProbeRoomCongestionBreaker(now)")
	if probe == "" {
		t.Fatal("maybeProbeRoomCongestionBreaker is missing")
	}
	for _, want := range []string{
		// the 20s interval gate
		"now - roomCongestionBreakerLastProbeAt < roomCongestionBreakerProbeMs",
		"roomCongestionBreakerLastProbeAt = now",
		// stage 0 → forced (still floored) track refresh + its own log line
		"if (roomCongestionBreakerProbeStage === 0) {",
		"roomCongestionBreakerProbeStage = 1",
		"logRoomRecovery('room_congestion_breaker_probe', { stage: 'track_refresh' })",
		"forceParticipantTrackRefresh('congestion breaker probe')",
		// stage 1 → the single budgeted ICE restart (shared per-connection cap)
		"requestBudgetedIceRestart('congestion breaker probe', now)",
		"stage: 'ice_restart'",
	} {
		if !strings.Contains(probe, want) {
			t.Errorf("maybeProbeRoomCongestionBreaker is missing %q", want)
		}
	}
	// the interval gate must precede the send so the probe cannot fire faster
	// than once per 20s (storm damping)
	gateAt := strings.Index(probe, "now - roomCongestionBreakerLastProbeAt < roomCongestionBreakerProbeMs")
	stampAt := strings.Index(probe, "roomCongestionBreakerLastProbeAt = now")
	stage0At := strings.Index(probe, "if (roomCongestionBreakerProbeStage === 0) {")
	if gateAt == -1 || stampAt == -1 || stage0At == -1 || gateAt > stampAt || stampAt > stage0At {
		t.Error("the probe interval gate must precede the stamp, which must precede the stage dispatch")
	}

	// the freeze-watch teardown resets the probe cycle with the breaker
	stop := functionBody(html, "function stopRemoteVideoFreezeWatch()")
	for _, want := range []string{
		"roomCongestionBreakerLastProbeAt = 0",
		"roomCongestionBreakerProbeStage = 0",
	} {
		if !strings.Contains(stop, want) {
			t.Errorf("stopRemoteVideoFreezeWatch must reset the probe state %q", want)
		}
	}
}

// MUST-FIX 2 (1:1 pin): the ladder must fire for the single sick tile in a 1:1
// room — where the breaker can never open (it needs >=2 sick tiles) and there
// is no advancing peer to satisfy the "Tom" signature. The frame-capable
// census + the trackedTiles === 1 exception make that case reachable.
func TestIndexStaleLadderFiresInOneOnOneRoom(t *testing.T) {
	html := readIndexHTMLForCongestion(t)

	ladder := functionBody(html, "function escalateStaleRemoteVideoTile(track, state, frozenMs, now)")
	if ladder == "" {
		t.Fatal("could not extract escalateStaleRemoteVideoTile body")
	}
	// the exact signature admits both the advancing-peer case AND the
	// only-frame-capable-tile (1:1 / camera-off room) case
	if !strings.Contains(ladder, "if (sickTiles !== 1 || (advancingTiles < 1 && trackedTiles !== 1)) {") {
		t.Error("the ladder gate must admit the 1:1 exception (advancingTiles < 1 && trackedTiles !== 1)")
	}
	// the breaker cannot open on a single sick tile, so the ladder is the sole
	// cure there — confirm the breaker floor is 2 sick tiles
	if !strings.Contains(html, "const roomCongestionBreakerMinSickTiles = 2") {
		t.Error("the breaker floor must be 2 sick tiles so a 1:1 room routes to the ladder, not the breaker")
	}
}

// MUST-FIX 3: the mute-watcher must follow the same hold policy as the deaf
// branch. A tile that has ALREADY rendered frames holds its last frame through
// a transient mute and only swaps to the avatar after the 12s long-outage hold
// (no 3s blink); a fresh attach that has NEVER rendered a frame keeps the 3s
// swap (an avatar beats a black rectangle). Unmute clears immediately.
func TestIndexMuteWatcherHoldsRenderedFrame(t *testing.T) {
	html := readIndexHTMLForCongestion(t)

	watch := functionBody(html, "function watchRemoteVideoTrackStall(tile, track)")
	if watch == "" {
		t.Fatal("could not extract watchRemoteVideoTrackStall body")
	}
	for _, want := range []string{
		// a rendered tile is detected by the element having painted a frame
		"const tileHasRenderedFrame = () => {",
		"video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA",
		"video.videoWidth > 0",
		// rendered → 12s hold (reuses the deaf branch threshold); never
		// rendered → the 3s swap
		"const holdMs = tileHasRenderedFrame() ? remoteVideoDeafAvatarHoldMs : 3000",
		"}, holdMs)",
		// unmute clears the stall immediately
		"track.addEventListener('unmute', clearStall)",
	} {
		if !strings.Contains(watch, want) {
			t.Errorf("watchRemoteVideoTrackStall is missing the hold-policy wiring %q", want)
		}
	}
	// the swap timer must be armed with the policy-chosen holdMs, not a bare
	// literal — a fresh 3000 in the setTimeout would re-introduce the 3s blink
	holdDeclAt := strings.Index(watch, "const holdMs = tileHasRenderedFrame() ? remoteVideoDeafAvatarHoldMs : 3000")
	timerAt := strings.Index(watch, "}, holdMs)")
	if holdDeclAt == -1 || timerAt == -1 || holdDeclAt > timerAt {
		t.Error("the mute-watcher must arm its swap timer with the rendered-vs-never-rendered holdMs")
	}
	// clearStall (the unmute/ended handler) removes the avatar cover promptly
	clear := functionBody(html, "const clearStall = () =>")
	if !strings.Contains(clear, "tile.classList.remove('is-video-stalled')") {
		t.Error("unmute/recovery must clear is-video-stalled immediately")
	}
}
