package main

// 2026-07-10 incident: a subscription can freeze PERMANENTLY — PLIs forward,
// keyframes flow, renegotiations answer (a fresh subscriber decodes the same
// trackLocal fine), yet this subscriber never recovers until a full rejoin.
// The freeze watch's 6s PLI/track-refresh cadence cannot cure that class, so
// a still-frozen tile climbs a bounded escalation ladder riding the SAME
// frozenMs clock (2026-07-06 flicker lesson: never a competing repair loop):
// request_participant_tracks at 12s, ONE restart_ice at 24s.
//
// Gate follow-ups (2026-07-10): the incident's OWN class — a subscription that
// goes deaf (inbound bytes stop entirely, so Chrome marks the track muted) —
// used to early-return before the ladder; it now routes into the SAME ladder
// when the roster says the camera is still on. Rung 1 forces past the
// track-refresh throttle so it provably sends, and the restart_ice cap +
// cooldown are per-CONNECTION so a transport-wide stall fires ONE restart.

import (
	"os"
	"strings"
	"testing"
)

func readIndexHTMLForEscalation(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(data)
}

// The ladder's thresholds: rung 1 (server-side track rebind) after 12s
// frozen, rung 2 (full transport renegotiation) after 24s, 60s cooldown and a
// cap of 2 on ICE restarts.
func TestIndexStaleTileEscalationThresholds(t *testing.T) {
	html := readIndexHTMLForEscalation(t)

	for _, want := range []string{
		"const remoteVideoStaleEscalationRefreshMs = 12000",
		"const remoteVideoStaleEscalationIceRestartMs = 24000",
		"const remoteVideoStaleEscalationIceCooldownMs = 60000",
		"const remoteVideoStaleEscalationMaxIceRestarts = 2",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html is missing %q", want)
		}
	}
}

// The ladder rides the existing freeze watch — noteRemoteVideoFreezeSample
// calls it with the same frozenMs clock the 6s repair cadence uses; there is
// no second stall detector to fight the repair passes.
func TestIndexStaleTileEscalationRidesFreezeWatch(t *testing.T) {
	html := readIndexHTMLForEscalation(t)

	sampleBody := functionBody(html, "function noteRemoteVideoFreezeSample(track, inbound, now)")
	if sampleBody == "" {
		t.Fatal("could not extract noteRemoteVideoFreezeSample body")
	}
	if !strings.Contains(sampleBody, "escalateStaleRemoteVideoTile(track, state, frozenMs, now)") {
		t.Error("the freeze watch must drive the escalation ladder (no competing loop)")
	}
	// recovery resets the per-episode rungs so a healed-then-refrozen tile
	// climbs from the bottom again (the cap and cooldown persist per connection)
	advanceAt := strings.Index(sampleBody, "if (framesAdvanced) {")
	if advanceAt == -1 {
		t.Fatal("noteRemoteVideoFreezeSample lost its framesAdvanced branch")
	}
	advance := sampleBody[advanceAt:]
	if cut := strings.Index(advance, "return"); cut != -1 {
		advance = advance[:cut]
	}
	for _, want := range []string{
		"state.escalatedTrackRefresh = false",
		"state.escalatedIceRestart = false",
	} {
		if !strings.Contains(advance, want) {
			t.Errorf("frame advance must reset the per-episode escalation rung %q", want)
		}
	}
}

// Rung mechanics: rung 1 (track refresh) fires once per freeze episode and
// FORCES past the request throttle; the ICE-restart rung single-shots per
// episode per track, and is additionally capped + cooled PER CONNECTION. The
// sends are the already-guest-allowlisted events, so the ladder cures members
// AND guests without a rejoin.
func TestIndexStaleTileEscalationSingleShotAndCooldown(t *testing.T) {
	html := readIndexHTMLForEscalation(t)

	body := functionBody(html, "function escalateStaleRemoteVideoTile(track, state, frozenMs, now)")
	if body == "" {
		t.Fatal("could not extract escalateStaleRemoteVideoTile body")
	}

	for _, want := range []string{
		// no socket, no ladder
		"if (!ws || ws.readyState !== WebSocket.OPEN) {",
		// rung 1: single-shot track rebind after the conservative window,
		// forced past the 900ms throttle so it provably sends (finding 2)
		"if (frozenMs < remoteVideoStaleEscalationRefreshMs) {",
		"if (!state.escalatedTrackRefresh) {",
		// finding 5: only burn the one-shot flag when the forced send actually
		// cleared the 4s floor and went out
		"if (forceParticipantTrackRefresh('stale tile escalation')) {",
		"state.escalatedTrackRefresh = true",
		// rung 2: single-shot per episode (per track); the cap + cooldown now
		// live in the shared budgeted-restart helper (finding 3), which the
		// congestion-breaker probe also draws on
		"if (frozenMs < remoteVideoStaleEscalationIceRestartMs || state.escalatedIceRestart) {",
		"if (!requestBudgetedIceRestart('stale tile escalation', now)) {",
		"state.escalatedIceRestart = true",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("escalateStaleRemoteVideoTile is missing %q", want)
		}
	}

	// finding 5: the flag must be set INSIDE the forced-send success branch, so
	// a floored (dropped) send leaves escalatedTrackRefresh false and rung 1
	// retries next tick instead of silently no-op'ing forever
	forceGuardAt := strings.Index(body, "if (forceParticipantTrackRefresh('stale tile escalation')) {")
	flagSetAt := strings.Index(body, "state.escalatedTrackRefresh = true")
	if forceGuardAt == -1 || flagSetAt == -1 || flagSetAt < forceGuardAt {
		t.Error("rung 1 must set escalatedTrackRefresh only after the forced send confirms it went out (finding 5)")
	}

	// the per-connection ICE budget (cap 2, 60s cooldown) lives in the shared
	// helper that both rung 2 and the congestion-breaker second-stage probe
	// call — one budget, so a stuck-open breaker and a transport stall cannot
	// double-spend restarts
	helper := functionBody(html, "function requestBudgetedIceRestart(reason, now)")
	if helper == "" {
		t.Fatal("requestBudgetedIceRestart is missing")
	}
	for _, want := range []string{
		"if (!ws || ws.readyState !== WebSocket.OPEN) {",
		"if (remoteVideoEscalationIceRestartCount >= remoteVideoStaleEscalationMaxIceRestarts) {",
		"if (remoteVideoEscalationLastIceRestartAt && now - remoteVideoEscalationLastIceRestartAt < remoteVideoStaleEscalationIceCooldownMs) {",
		"remoteVideoEscalationIceRestartCount++",
		"remoteVideoEscalationLastIceRestartAt = now",
		"event: 'restart_ice'",
		"data: JSON.stringify({ reason })",
		"return true",
		"return false",
	} {
		if !strings.Contains(helper, want) {
			t.Errorf("requestBudgetedIceRestart is missing %q", want)
		}
	}
	// the budget is spent (count++/stamp) only after the cap + cooldown gates
	capGateAt := strings.Index(helper, "if (remoteVideoEscalationIceRestartCount >= remoteVideoStaleEscalationMaxIceRestarts) {")
	spendAt := strings.Index(helper, "remoteVideoEscalationIceRestartCount++")
	if capGateAt == -1 || spendAt == -1 || spendAt < capGateAt {
		t.Error("requestBudgetedIceRestart must check the cap before spending the budget")
	}

	// rung 1 returns before rung 2 — one rung per tick, never both at once
	refreshAt := strings.Index(body, "forceParticipantTrackRefresh('stale tile escalation')")
	iceGateAt := strings.Index(body, "if (frozenMs < remoteVideoStaleEscalationIceRestartMs")
	if refreshAt == -1 || iceGateAt == -1 || refreshAt > iceGateAt {
		t.Error("the track-refresh rung must gate (and return) before the restart_ice rung")
	}

	// the per-track ladder state is seeded with the freeze sample; the ICE cap
	// and cooldown are no longer per-track (they moved to the connection)
	seedAt := strings.Index(html, "remoteVideoFreezeStates.set(track.id, {")
	if seedAt == -1 {
		t.Fatal("remoteVideoFreezeStates seeding is missing")
	}
	seed := html[seedAt:]
	if end := strings.Index(seed, "})"); end != -1 {
		seed = seed[:end]
	}
	for _, want := range []string{
		"escalatedTrackRefresh: false",
		"escalatedIceRestart: false",
	} {
		if !strings.Contains(seed, want) {
			t.Errorf("the freeze-state seed is missing %q", want)
		}
	}
}

// Finding 1 (the incident's own class): a deaf subscription — inbound bytes
// stop entirely, Chrome marks the remote track muted — used to hit the quiet
// muted / no-bytes early-return and never reach the ladder. It now routes into
// the SAME ladder, but ONLY when the roster (trustworthy for guests now) says
// the camera is on and a real rendered gallery tile is stranded; a camera-off
// or unknown-roster keeps the old early-return (anti-flap).
func TestIndexStaleTileEscalationDeafSubscription(t *testing.T) {
	html := readIndexHTMLForEscalation(t)

	sampleBody := functionBody(html, "function noteRemoteVideoFreezeSample(track, inbound, now)")
	if sampleBody == "" {
		t.Fatal("could not extract noteRemoteVideoFreezeSample body")
	}
	for _, want := range []string{
		// bytes stopped (muted) OR the byte counter stalled → deaf candidate
		"if (track.muted || !bytesAdvanced) {",
		"const deafTile = deafSubscriptionRemoteTile(track, framesDecoded)",
		// the deaf case marks the tile frozen and climbs the SAME ladder
		"frozenRemoteVideoTrackIds.add(track.id)",
		"escalateStaleRemoteVideoTile(track, state, frozenMs, now)",
	} {
		if !strings.Contains(sampleBody, want) {
			t.Errorf("noteRemoteVideoFreezeSample is missing the deaf-path wiring %q", want)
		}
	}

	// the deaf branch is an exception that precedes the quiet muted early-return
	// (camera-off / unknown-roster fall through to that return)
	deafAt := strings.Index(sampleBody, "const deafTile = deafSubscriptionRemoteTile(track, framesDecoded)")
	mutedReturnAt := strings.Index(sampleBody, "if (track.muted) {")
	if deafAt == -1 || mutedReturnAt == -1 || deafAt > mutedReturnAt {
		t.Error("the deaf escalation branch must gate before the muted early-return")
	}

	// the deaf gate: rendered gallery tile + roster frame-capable media; no roster
	// entry or camera-off without an active screen share keeps the old behavior
	helper := functionBody(html, "function deafSubscriptionRemoteTile(track, framesDecoded)")
	if helper == "" {
		t.Fatal("could not extract deafSubscriptionRemoteTile body")
	}
	for _, want := range []string{
		"if (framesDecoded <= 0) {", // a never-rendered fresh track is not deaf
		"remoteTileShowingTrack(track)",
		"!participantMediaStates.has(name)",                        // no roster state → not deaf
		"const mediaState = participantMediaState(name)",           // current roster media truth
		"if (mediaState.cameraOff && !mediaState.screenSharing) {", // camera off without a share → not deaf
	} {
		if !strings.Contains(helper, want) {
			t.Errorf("deafSubscriptionRemoteTile is missing %q", want)
		}
	}
}

// Finding 2: rung 1 is a no-op without a throttle bypass — the 6s repair
// cadence fires the same request on the same tick, and requestParticipantTrackRefresh's
// 900ms global throttle would swallow the rung's send while it still burns its
// one-shot flag. A dedicated forceParticipantTrackRefresh is the fix (the
// throttled and forced paths share one send that stamps the same clock), used
// ONLY by the rung — the throttled entry point keeps its exact signature (a
// sibling pin in frontend_latency_test.go depends on it).
func TestIndexParticipantTrackRefreshForceBypass(t *testing.T) {
	html := readIndexHTMLForEscalation(t)

	// the throttled entry point is unchanged; a sibling force path skips it
	if !strings.Contains(html, "function requestParticipantTrackRefresh(reason = '')") {
		t.Error("the throttled requestParticipantTrackRefresh entry point must keep its signature")
	}
	forced := functionBody(html, "function forceParticipantTrackRefresh(reason = '')")
	if forced == "" {
		t.Fatal("forceParticipantTrackRefresh bypass is missing")
	}
	// the forced path does NOT gate on the 900ms throttle
	if strings.Contains(forced, "< 900") {
		t.Error("forceParticipantTrackRefresh must not re-apply the 900ms throttle")
	}
	// both paths funnel through one send that stamps the shared clock, so a
	// forced send still throttles the next unforced one
	if !strings.Contains(forced, "sendParticipantTrackRefresh(reason)") {
		t.Error("forceParticipantTrackRefresh must share the send that stamps lastParticipantTrackRefreshAt")
	}
	// (2026-07-10 spiral: the shared send now reads the clock once for the 4s
	// global floor and stamps that same `now` — see
	// TestIndexParticipantTrackRefreshGlobalFloor for the floor pins)
	send := functionBody(html, "function sendParticipantTrackRefresh(reason)")
	if !strings.Contains(send, "lastParticipantTrackRefreshAt = now") {
		t.Error("the shared send must stamp lastParticipantTrackRefreshAt so forced sends still throttle")
	}
	// rung 1 is the forced caller
	ladder := functionBody(html, "function escalateStaleRemoteVideoTile(track, state, frozenMs, now)")
	if !strings.Contains(ladder, "forceParticipantTrackRefresh('stale tile escalation')") {
		t.Error("the track-refresh rung must force past the throttle so it provably sends")
	}
}

// Finding 3: restart_ice is transport-wide, so its cap + cooldown are
// per-CONNECTION module state, not per-track — when a whole transport stalls,
// every frozen tile crosses 24s in one stats pass, and only ONE restart may
// fire per cooldown window. A fresh session peer resets the budget.
func TestIndexStaleTileEscalationIceRestartPerConnection(t *testing.T) {
	html := readIndexHTMLForEscalation(t)

	for _, want := range []string{
		"let remoteVideoEscalationIceRestartCount = 0",
		"let remoteVideoEscalationLastIceRestartAt = 0",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html is missing the per-connection ICE-restart state %q", want)
		}
	}

	watch := functionBody(html, "function startRemoteVideoFreezeWatch(sessionPeer)")
	if watch == "" {
		t.Fatal("could not extract startRemoteVideoFreezeWatch body")
	}
	for _, want := range []string{
		"remoteVideoEscalationIceRestartCount = 0",
		"remoteVideoEscalationLastIceRestartAt = 0",
	} {
		if !strings.Contains(watch, want) {
			t.Errorf("startRemoteVideoFreezeWatch must reset the per-connection ICE budget %q", want)
		}
	}

	// the per-track seed no longer carries the ICE cap/cooldown fields
	seedAt := strings.Index(html, "remoteVideoFreezeStates.set(track.id, {")
	if seedAt == -1 {
		t.Fatal("remoteVideoFreezeStates seeding is missing")
	}
	seed := html[seedAt:]
	if end := strings.Index(seed, "})"); end != -1 {
		seed = seed[:end]
	}
	for _, gone := range []string{"iceRestartCount:", "lastEscalationIceRestartAt:"} {
		if strings.Contains(seed, gone) {
			t.Errorf("the per-track freeze seed must not carry the now per-connection field %q", gone)
		}
	}
}
