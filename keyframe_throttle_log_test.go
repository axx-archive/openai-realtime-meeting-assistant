package main

// Pins FIX 2 of the 2026-07-10 incident: the per-source keyframe throttle in
// requestSourceKeyframe dropped subscriber PLIs SILENTLY, which hid the
// forward path's behavior during the frozen-tile diagnosis. The drop must be
// counted and logged (rate-limited), with no behavior change to the throttle.

import (
	"testing"
	"time"
)

func TestThrottledKeyframeRequestDropIsCountedAndRateLimitedLogged(t *testing.T) {
	const trackID = "stream:track:12345" // throttle-log pin source
	t.Cleanup(func() { subscriberKeyframeThrottle.forget(trackID) })
	keyframeThrottleDropLogStamp.Store(0)

	// Seed the window, as a first subscriber's forwarded PLI would.
	if !subscriberKeyframeThrottle.allow(trackID, time.Now()) {
		t.Fatal("fresh source should pass the throttle")
	}

	before := keyframeThrottleDrops.Load()
	requestSourceKeyframe(trackID, "Tom", "tom-1")
	if got := keyframeThrottleDrops.Load(); got != before+1 {
		t.Fatalf("throttled PLI drop count=%d, want %d — the drop is still silent", got, before+1)
	}
	stampAfterFirst := keyframeThrottleDropLogStamp.Load()
	if stampAfterFirst == 0 {
		t.Fatal("first throttled drop did not log")
	}

	// A second drop inside the log interval is counted but not re-logged.
	requestSourceKeyframe(trackID, "Tim", "tim-1")
	if got := keyframeThrottleDrops.Load(); got != before+2 {
		t.Fatalf("second throttled drop count=%d, want %d", got, before+2)
	}
	if keyframeThrottleDropLogStamp.Load() != stampAfterFirst {
		t.Fatal("drop log is not rate-limited inside the interval")
	}
}
