package main

// §6.5 hardening (2026-07-10 incident, adversarial-gate finding 1): the four
// newly guest-reachable inbound events — participant_media_state and
// request_participant_tracks (each fans out a room-wide roster broadcast / a
// global peer-sync walk) and media_quality/media_error (unbounded log writes)
// — must be token-bucketed per guest session so a hostile guest-link holder
// can't spam them at socket line rate. These tests pin the bucket math (like
// TestGuestChatTokenBucket) and the handler wiring (like the read-loop
// redaction source pin).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGuestMediaStateTokenBucket(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	now := time.Now().UTC()

	// Burst passes, the one past the burst is dropped.
	for i := 0; i < guestMediaStateBucketBurst; i++ {
		if !app.allowGuestMediaStateEvent("room-x", "guest-key", now) {
			t.Fatalf("burst event %d was rejected", i)
		}
	}
	if app.allowGuestMediaStateEvent("room-x", "guest-key", now) {
		t.Fatal("event past the burst window should be rejected")
	}
	// One token refills per refill interval.
	if !app.allowGuestMediaStateEvent("room-x", "guest-key", now.Add(guestMediaStateBucketRefill+time.Millisecond)) {
		t.Fatal("refilled token was rejected")
	}
	if app.allowGuestMediaStateEvent("room-x", "guest-key", now.Add(guestMediaStateBucketRefill+2*time.Millisecond)) {
		t.Fatal("second event right after a single refill should be rejected")
	}
	// A different guest session has its own bucket.
	if !app.allowGuestMediaStateEvent("room-x", "other-key", now) {
		t.Fatal("another session's bucket was drained by the first")
	}
}

func TestGuestTelemetryTokenBucket(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	now := time.Now().UTC()

	for i := 0; i < guestTelemetryBucketBurst; i++ {
		if !app.allowGuestTelemetryEvent("room-x", "guest-key", now) {
			t.Fatalf("burst telemetry %d was rejected", i)
		}
	}
	if app.allowGuestTelemetryEvent("room-x", "guest-key", now) {
		t.Fatal("telemetry past the burst window should be rejected")
	}
	if !app.allowGuestTelemetryEvent("room-x", "guest-key", now.Add(guestTelemetryBucketRefill+time.Millisecond)) {
		t.Fatal("refilled telemetry token was rejected")
	}
	if app.allowGuestTelemetryEvent("room-x", "guest-key", now.Add(guestTelemetryBucketRefill+2*time.Millisecond)) {
		t.Fatal("second telemetry right after a single refill should be rejected")
	}
	if !app.allowGuestTelemetryEvent("room-x", "other-key", now) {
		t.Fatal("another session's telemetry bucket was drained by the first")
	}
}

// The state/repair and telemetry buckets are independent: draining one must not
// throttle the other for the same guest session (they cap different fan-outs).
func TestGuestMediaBucketsAreIndependent(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	now := time.Now().UTC()

	for i := 0; i < guestMediaStateBucketBurst; i++ {
		app.allowGuestMediaStateEvent("room-x", "guest-key", now)
	}
	if app.allowGuestMediaStateEvent("room-x", "guest-key", now) {
		t.Fatal("state bucket should be drained")
	}
	if !app.allowGuestTelemetryEvent("room-x", "guest-key", now) {
		t.Fatal("telemetry bucket was drained by state-event spam — buckets are not independent")
	}
}

// TestGuestMediaRateLimitWiredInHandlers pins the handler seam the way
// TestWebsocketReadLoopLogsThroughScrubber pins the read loop: the four
// guest-reachable inbound events must gate on the matching bucket, drop
// silently with a rate-limited log line, and gate on `guest != nil` so
// authenticated members are never throttled.
func TestGuestMediaRateLimitWiredInHandlers(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	main := string(source)

	// state/repair bucket guards both fan-out events, members exempt.
	for _, want := range []string{
		`guest != nil && !kanbanApp.allowGuestMediaStateEvent(connRoomID, guest.SessionKey, time.Now())`,
		`guest != nil && !kanbanApp.allowGuestTelemetryEvent(connRoomID, guest.SessionKey, time.Now())`,
		`guest_media_state_rate_limited`,
		`guest_media_repair_rate_limited`,
		`guest_media_telemetry_rate_limited`,
	} {
		if !strings.Contains(main, want) {
			t.Errorf("main.go is missing the guest rate-limit wiring: %q", want)
		}
	}

	// Each guarded event must reference its bucket call at least as many times
	// as it appears — cheap guard against wiring only one of the pair.
	if got := strings.Count(main, "allowGuestMediaStateEvent(connRoomID"); got < 2 {
		t.Errorf("allowGuestMediaStateEvent wired %d times, want >=2 (participant_media_state + request_participant_tracks)", got)
	}
	if got := strings.Count(main, "allowGuestTelemetryEvent(connRoomID"); got < 2 {
		t.Errorf("allowGuestTelemetryEvent wired %d times, want >=2 (media_quality + media_error)", got)
	}
}
