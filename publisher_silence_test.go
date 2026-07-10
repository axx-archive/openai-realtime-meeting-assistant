package main

// 2026-07-10 silent-uplink incident (room StationTenn): Tim's Safari outbound
// AUDIO sender silently stopped producing RTP for 7+ minutes
// (outAudioPackets=0 client-side) while his PeerConnection stayed healthy (RTT
// 16-26ms, downlink perfect). The server's publisher read pump saw ZERO
// signal — the track looked alive for 13m22s until EOF at leave — so the only
// diagnosis path was client telemetry. Others just saw/heard nothing from him.
//
// These tests pin the per-publisher-track silence watchdog that makes the
// stall visible server-side (and gently nudges a stalled VIDEO uplink):
//   - a track that WAS producing RTP and then stops crosses the threshold and
//     is reported once, then re-reported only on the rate-limit boundary with a
//     repeat count;
//   - a track that never produced RTP (join-muted) is NOT flagged;
//   - a VIDEO nudge is routed through the SAME per-source keyframe throttle as
//     every other PLI (never bypasses the budget); AUDIO gets no PLI;
//   - RTP resuming after a silent period logs a recovery with the gap length;
//   - watchdog state is cleaned up on track removal with no map leak;
//   - the hot-path stamp is the only per-packet cost and a healthy track
//     produces no action.

import (
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

const (
	ms = int64(time.Millisecond)
	s  = int64(time.Second)
)

// newSilentTestWatch builds a watch with a valid forwarded-track key
// (stream:track:ssrc) so requestSourceKeyframe can parse the SSRC, mirroring
// the throttle-log test's "stream:track:12345" convention.
func newSilentTestWatch(sourceKey string, kind webrtc.RTPCodecType) *publisherTrackWatch {
	return &publisherTrackWatch{
		sourceKey:   sourceKey,
		participant: "Tim",
		session:     "tim-1",
		kind:        kind,
	}
}

func TestPublisherSilenceThresholdsAreIncidentGrade(t *testing.T) {
	// The incident stall was minutes long; a 5s floor detects a real stall fast
	// without tripping on ordinary jitter, and the log must not become its own
	// storm during a sustained stall.
	if publisherSilenceThreshold != 5*time.Second {
		t.Fatalf("publisherSilenceThreshold=%s, want 5s", publisherSilenceThreshold)
	}
	if publisherSilenceLogInterval < 30*time.Second {
		t.Fatalf("publisherSilenceLogInterval=%s lets the silent log storm; want >= 30s", publisherSilenceLogInterval)
	}
}

func TestPublisherSilenceEvaluateStateMachine(t *testing.T) {
	base := int64(1_700_000_000) * s
	w := newSilentTestWatch("stream:track:11111", webrtc.RTPCodecTypeAudio)
	w.lastRTPNanos.Store(base) // one packet landed at base

	// Healthy: age under threshold => nothing.
	if obs := w.evaluate(base + 2*s); obs.action != publisherSilenceNone {
		t.Fatalf("age 2s: action=%v, want none", obs.action)
	}

	// Cross the threshold: onset, silent_ms ~= age, repeat starts at 0.
	obs := w.evaluate(base + 6*s)
	if obs.action != publisherSilenceOnset {
		t.Fatalf("age 6s: action=%v, want onset", obs.action)
	}
	if obs.silentMs != 6000 {
		t.Fatalf("onset silent_ms=%d, want 6000", obs.silentMs)
	}
	if obs.repeat != 0 {
		t.Fatalf("onset repeat=%d, want 0", obs.repeat)
	}

	// Still silent, inside the log window: suppressed (none), but tracked.
	if obs := w.evaluate(base + 9*s); obs.action != publisherSilenceNone {
		t.Fatalf("age 9s inside log window: action=%v, want none (rate-limited)", obs.action)
	}

	// RTP resumes (a fresh packet at base+40s), sweep sees a recent stamp:
	// recovery fires once with the gap length (40s - 0 last-pre-silence).
	w.lastRTPNanos.Store(base + 40*s)
	rec := w.evaluate(base + 41*s)
	if rec.action != publisherSilenceRecovered {
		t.Fatalf("resumed: action=%v, want recovered", rec.action)
	}
	if rec.silentMs != 40000 {
		t.Fatalf("recovered silent_ms=%d, want 40000 (base..base+40s gap)", rec.silentMs)
	}

	// After recovery a healthy track is quiet again.
	if obs := w.evaluate(base + 41*s); obs.action != publisherSilenceNone {
		t.Fatalf("post-recovery: action=%v, want none", obs.action)
	}
}

func TestPublisherSilenceNeverProducedIsIgnored(t *testing.T) {
	// A join-muted track that never emitted RTP must not be flagged — the
	// incident is specifically about a track that WAS producing and stopped.
	w := newSilentTestWatch("stream:track:33333", webrtc.RTPCodecTypeAudio)
	if obs := w.evaluate(int64(1_700_000_000)*s + time.Hour.Nanoseconds()); obs.action != publisherSilenceNone {
		t.Fatalf("never-produced track: action=%v, want none", obs.action)
	}
}

func TestPublisherSilenceLogIsRateLimitedWithRepeatCount(t *testing.T) {
	base := int64(1_700_000_000) * s
	w := newSilentTestWatch("stream:track:44444", webrtc.RTPCodecTypeAudio)
	w.lastRTPNanos.Store(base)

	onsets := 0
	ongoings := 0
	lastRepeat := 0
	// Sweep at the production 3s cadence from +6s (first sweep past the 5s
	// floor) through +40s. Onset once; ongoing only after a full log interval.
	for now := base + 6*s; now <= base+40*s; now += 3 * s {
		switch obs := w.evaluate(now); obs.action {
		case publisherSilenceOnset:
			onsets++
		case publisherSilenceOngoing:
			ongoings++
			lastRepeat = obs.repeat
		}
	}
	if onsets != 1 {
		t.Fatalf("onset logs=%d, want exactly 1", onsets)
	}
	if ongoings != 1 {
		t.Fatalf("ongoing logs=%d over 34s, want exactly 1 (rate-limited to 30s)", ongoings)
	}
	if lastRepeat <= 0 {
		t.Fatalf("ongoing repeat=%d, want a positive suppressed-observation count", lastRepeat)
	}
}

func TestPublisherSilenceVideoNudgeGoesThroughThrottle(t *testing.T) {
	const sourceKey = "stream:track:55555"
	t.Cleanup(func() { subscriberKeyframeThrottle.forget(sourceKey) })

	w := newSilentTestWatch(sourceKey, webrtc.RTPCodecTypeVideo)
	w.nudgeIfVideo()

	// The nudge must have spent this source's budget in the SHARED throttle, so
	// an immediate second request is suppressed — proof it did not bypass the
	// budget (requestSourceKeyframe consults subscriberKeyframeThrottle first).
	if subscriberKeyframeThrottle.allow(sourceKey, time.Now()) {
		t.Fatal("video nudge did not consume the shared keyframe budget — it bypassed the throttle")
	}
}

func TestPublisherSilenceAudioGetsNoNudge(t *testing.T) {
	const sourceKey = "stream:track:66666"
	t.Cleanup(func() { subscriberKeyframeThrottle.forget(sourceKey) })

	w := newSilentTestWatch(sourceKey, webrtc.RTPCodecTypeAudio)
	w.nudgeIfVideo()

	// Audio has no PLI; the budget for this source must be untouched (a fresh
	// request still passes).
	if !subscriberKeyframeThrottle.allow(sourceKey, time.Now()) {
		t.Fatal("audio watch spent keyframe budget — audio must be log-only")
	}
}

func TestPublisherSilenceRegistryCleanupNoLeak(t *testing.T) {
	reg := newPublisherSilenceRegistry()
	w1 := reg.register("stream:track:77777", "Tim", "tim-1", webrtc.RTPCodecTypeVideo, nil)
	if reg.size() != 1 {
		t.Fatalf("after register size=%d, want 1", reg.size())
	}

	// A same-key republish replaces the entry; forgetting the STALE watch must
	// not evict the fresh one (mirrors removeTrack's identity guard).
	w2 := reg.register("stream:track:77777", "Tim", "tim-2", webrtc.RTPCodecTypeVideo, nil)
	reg.forget(w1)
	if reg.size() != 1 {
		t.Fatalf("forgetting stale watch evicted the fresh one: size=%d, want 1", reg.size())
	}

	reg.forget(w2)
	if reg.size() != 0 {
		t.Fatalf("after forget size=%d, want 0 — watchdog state leaked", reg.size())
	}
}

func TestPublisherSilenceStampFlipsSilentBackToHealthy(t *testing.T) {
	// The per-packet stamp is the ONLY hot-path write; a fresh stamp is what
	// keeps a busy track out of the silent path entirely (zero action when
	// healthy).
	base := int64(1_700_000_000) * s
	w := newSilentTestWatch("stream:track:88888", webrtc.RTPCodecTypeVideo)
	w.lastRTPNanos.Store(base)

	if obs := w.evaluate(base + 100*ms); obs.action != publisherSilenceNone {
		t.Fatalf("freshly stamped track: action=%v, want none", obs.action)
	}
	// A stamp that advances with the sweep keeps it silent-free forever.
	for now := base; now <= base+30*s; now += 3 * s {
		w.lastRTPNanos.Store(now) // a packet each sweep
		if obs := w.evaluate(now); obs.action != publisherSilenceNone {
			t.Fatalf("continuously stamped track at %d: action=%v, want none", now, obs.action)
		}
	}
}
