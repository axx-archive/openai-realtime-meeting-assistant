package main

// 2026-07-10 keyframe-spiral incident (room StationTenn): one lossy mobile
// subscriber PLI'd continuously; every client repair message ran a global
// signal walk whose deferred dispatchKeyFrame PLI'd EVERY publisher while
// BYPASSING subscriberKeyframeThrottle, and the MEMBER
// request_participant_tracks path had no rate limit at all (193 messages in
// ~4 min, entirely unlogged). Publishers pumped keyframe-heavy bursts until
// the droplet's egress saturated and all five participants lost media.
//
// These tests pin the damping:
//   S1(a) the signal-walk keyframe dispatch respects the per-source throttle
//         (storm of walks => at most floor-rate PLIs per source; a fresh
//         source still recovers immediately),
//   S1(b) member request_participant_tracks is token-bucketed per session
//         (burst covers a just-joined member's first snapshot request;
//         sustained spam drops silently with a rate-limited log; the guest
//         bucket path is untouched),
//   S2    member repair requests are logged (rate-limited, sanitized reason)
//         so the next incident is visible in server forensics.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

/* ---------- S1(a): signal-walk keyframe throttle ---------- */

// The per-source floor must cap storm cadence around one keyframe per
// 2.5-3s (the incident's forwarded cadence was 1 per 1-2s per source, which
// saturated a 5-way fanout) while staying snappy enough for single-tile
// recovery.
func TestSubscriberKeyframeIntervalCapsStormCadence(t *testing.T) {
	if subscriberKeyframeInterval < 2500*time.Millisecond {
		t.Fatalf("subscriberKeyframeInterval=%s lets a PLI storm sustain the incident cadence; want >= 2.5s", subscriberKeyframeInterval)
	}
	if subscriberKeyframeInterval > 3*time.Second {
		t.Fatalf("subscriberKeyframeInterval=%s makes single-tile recovery sluggish; want <= 3s", subscriberKeyframeInterval)
	}
}

// A storm of signal walks (the incident ran one every ~1.2s; this simulates
// one every 250ms for 10s) must yield at most floor-rate PLIs per source, and
// a source the throttle has never seen must pass immediately so a
// just-published track still gets its first keyframe without delay.
func TestSignalWalkKeyframeStormThrottledPerSource(t *testing.T) {
	const stormSource = "storm-test-stream:storm-test-track:1111"
	const freshSource = "fresh-test-stream:fresh-test-track:2222"
	t.Cleanup(func() {
		subscriberKeyframeThrottle.forget(stormSource)
		subscriberKeyframeThrottle.forget(freshSource)
	})

	base := time.Now()
	allowed := 0
	for i := 0; i < 40; i++ { // 40 walks x 250ms = a 10s storm
		if allowSignalWalkKeyframe(stormSource, base.Add(time.Duration(i)*250*time.Millisecond)) {
			allowed++
		}
	}
	// 10s at >= 2.5s per source = at most 4 intervals + the initial hit.
	maxAllowed := int(10*time.Second/subscriberKeyframeInterval) + 1
	if allowed > maxAllowed {
		t.Fatalf("storm of 40 walks forwarded %d keyframe requests for one source, want <= %d", allowed, maxAllowed)
	}
	if allowed < 1 {
		t.Fatal("throttle never let a keyframe through — recovery would be impossible")
	}

	// Mid-storm, an unseen source (new publisher) must not be starved.
	if !allowSignalWalkKeyframe(freshSource, base.Add(5*time.Second)) {
		t.Fatal("a source the throttle has never seen must pass immediately (just-joined publisher recovery)")
	}
}

// The dispatch walk itself must gate every receiver PLI through the shared
// per-source throttle (keyed exactly like the forwarded-subscriber-PLI path,
// so both spend one budget) and keep the room_keyframe_throttled accounting.
func TestDispatchKeyFrameGatesThroughSubscriberThrottle(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	main := string(source)
	for _, want := range []string{
		// dispatchKeyFrame consults the throttle per receiver source...
		`allowSignalWalkKeyframe(forwardedRemoteTrackID(`,
		// ...and suppressions feed the existing drop accounting.
		"func allowSignalWalkKeyframe(",
		"noteThrottledKeyframeRequestDrop(sourceKey",
	} {
		if !strings.Contains(main, want) {
			t.Errorf("main.go is missing the signal-walk keyframe throttle wiring: %q", want)
		}
	}
}

/* ---------- S1(b): member request_participant_tracks bucket ---------- */

func TestMemberMediaRepairTokenBucket(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	now := time.Now().UTC()

	// A just-joined member's first snapshot request always succeeds, and the
	// burst absorbs a legitimate reconnect flurry.
	for i := 0; i < memberMediaRepairBucketBurst; i++ {
		if !app.allowMemberMediaRepair("room-x", "member-session", now) {
			t.Fatalf("burst repair request %d was rejected", i)
		}
	}
	// Sustained spam past the burst is dropped.
	if app.allowMemberMediaRepair("room-x", "member-session", now) {
		t.Fatal("repair request past the burst window should be rejected")
	}
	// One token refills per refill interval.
	if !app.allowMemberMediaRepair("room-x", "member-session", now.Add(memberMediaRepairBucketRefill+time.Millisecond)) {
		t.Fatal("refilled repair token was rejected")
	}
	if app.allowMemberMediaRepair("room-x", "member-session", now.Add(memberMediaRepairBucketRefill+2*time.Millisecond)) {
		t.Fatal("second repair right after a single refill should be rejected")
	}
	// Another member session has its own bucket.
	if !app.allowMemberMediaRepair("room-x", "other-session", now) {
		t.Fatal("another session's repair bucket was drained by the first")
	}
}

// Draining a member's repair bucket must not touch the guest buckets and vice
// versa — they cap different principals.
func TestMemberRepairBucketIndependentOfGuestBuckets(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	now := time.Now().UTC()

	for i := 0; i < memberMediaRepairBucketBurst; i++ {
		app.allowMemberMediaRepair("room-x", "same-key", now)
	}
	if app.allowMemberMediaRepair("room-x", "same-key", now) {
		t.Fatal("member bucket should be drained")
	}
	if !app.allowGuestMediaStateEvent("room-x", "same-key", now) {
		t.Fatal("guest media-state bucket was drained by member repair spam — buckets are not independent")
	}
}

// The bucket is keyed by the per-socket participant session id, so the session
// cleanup seam must drop it or the map grows one entry per socket forever.
func TestDropMemberMediaRepairBucket(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	now := time.Now().UTC()

	app.allowMemberMediaRepair("room-x", "member-session", now)
	app.dropMemberMediaRepairBucket("room-x", "member-session")

	app.mu.Lock()
	_, ok := app.roomLiveLocked("room-x").memberRepairBuckets["member-session"]
	app.mu.Unlock()
	if ok {
		t.Fatal("dropMemberMediaRepairBucket left the session's bucket behind")
	}
}

// TestMemberMediaRepairWiredInHandler pins the handler seam the way
// TestGuestMediaRateLimitWiredInHandlers does: the member path of
// request_participant_tracks must charge the member bucket, drop silently on
// limit with the member_media_repair_rate_limited log, log accepted requests
// (S2, sanitized reason), release the bucket on session cleanup, and leave the
// guest bucket path exactly as it was.
func TestMemberMediaRepairWiredInHandler(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	main := string(source)

	for _, want := range []string{
		// member gate, keyed by the per-socket session id (guests keep their
		// own bucket — the member branch is inside `if guest == nil`).
		`!kanbanApp.allowMemberMediaRepair(connRoomID, participantSessionID`,
		// drop log on limit.
		"member_media_repair_rate_limited",
		// S2: accepted member repairs are logged with the sanitized reason.
		"member_media_repair session=",
		"participantTrackRefreshReason(message.Data)",
		// per-socket bucket released in the session cleanup seam.
		"dropMemberMediaRepairBucket(connRoomID, participantSessionID)",
	} {
		if !strings.Contains(main, want) {
			t.Errorf("main.go is missing the member repair rate-limit wiring: %q", want)
		}
	}

	// Guest path untouched: the guest bucket still guards both fan-out events.
	if got := strings.Count(main, "allowGuestMediaStateEvent(connRoomID"); got < 2 {
		t.Errorf("allowGuestMediaStateEvent wired %d times, want >=2 — the guest path must stay as-is", got)
	}
}

/* ---------- S2: sanitized client-supplied reason ---------- */

func TestParticipantTrackRefreshReasonSanitized(t *testing.T) {
	// Ordinary reasons pass through (the incident forensics grep on these).
	if got := participantTrackRefreshReason(`{"reason":"frozen remote video"}`); got != "frozen remote video" {
		t.Fatalf("plain reason mangled: %q", got)
	}
	// Control characters cannot forge log lines.
	if got := participantTrackRefreshReason("{\"reason\":\"evil\\nroom_keyframe_forwarded forged=1\\r\\tx\"}"); strings.ContainsAny(got, "\n\r\t") {
		t.Fatalf("reason kept control characters: %q", got)
	}
	// Garbage payloads degrade to empty, never crash.
	if got := participantTrackRefreshReason("not json"); got != "" {
		t.Fatalf("invalid payload should yield empty reason, got %q", got)
	}
	if got := participantTrackRefreshReason(`{"reason":42}`); got != "" {
		t.Fatalf("non-string reason should yield empty, got %q", got)
	}
	// One field cannot bury a log line.
	long := participantTrackRefreshReason(`{"reason":"` + strings.Repeat("a", 5000) + `"}`)
	if len(long) > 256 {
		t.Fatalf("reason not capped: %d runes", len(long))
	}
}
