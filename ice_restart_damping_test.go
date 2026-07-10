package main

// card-003 W4 ICE-restart hardening. restart_ice was the last media-inbound
// event left unbucketed after the 2026-07-10 keyframe-spiral damping wave: each
// one forces a full ICERestart renegotiation plus a dispatchKeyFrame walk, so a
// socket-line-rate flood re-melts the room the way the repair storm did. These
// tests pin:
//   gap 1  member/guest restart_ice is token-bucketed per principal (burst 4,
//          refill 1 per 5s — sized to clear the client's 5-attempt restart
//          ladder, see TestIceRestartBucketSurvivesClientLadder), the bucket is
//          independent of the repair/media buckets, and its per-socket member
//          key is released on cleanup, plus the handler wiring (charge, silent
//          drop with a rate-limited log, reap release);
//   gap 2  /client-config advertises the TURN credential TTL so a long-lived
//          client can refresh before its relay creds expire.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

/* ---------- gap 1: restart_ice token bucket ---------- */

func TestMemberIceRestartTokenBucket(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	now := time.Now().UTC()

	// The burst (>= 2, covering the client's bounded restart ladder) always
	// passes for a legit client.
	for i := 0; i < iceRestartBucketBurst; i++ {
		if !app.allowMemberIceRestart("room-x", "member-session", now) {
			t.Fatalf("burst restart request %d was rejected", i)
		}
	}
	// A storm past the burst is dropped.
	if app.allowMemberIceRestart("room-x", "member-session", now) {
		t.Fatal("restart request past the burst window should be rejected")
	}
	// One token refills per refill interval.
	if !app.allowMemberIceRestart("room-x", "member-session", now.Add(iceRestartBucketRefill+time.Millisecond)) {
		t.Fatal("refilled restart token was rejected")
	}
	if app.allowMemberIceRestart("room-x", "member-session", now.Add(iceRestartBucketRefill+2*time.Millisecond)) {
		t.Fatal("second restart right after a single refill should be rejected")
	}
	// Another member session has its own bucket.
	if !app.allowMemberIceRestart("room-x", "other-session", now) {
		t.Fatal("another session's restart bucket was drained by the first")
	}
}

func TestGuestIceRestartTokenBucket(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	now := time.Now().UTC()

	for i := 0; i < iceRestartBucketBurst; i++ {
		if !app.allowGuestIceRestart("room-x", "guest-key", now) {
			t.Fatalf("burst guest restart request %d was rejected", i)
		}
	}
	if app.allowGuestIceRestart("room-x", "guest-key", now) {
		t.Fatal("guest restart past the burst should be rejected")
	}
	// A second guest session has its own bucket.
	if !app.allowGuestIceRestart("room-x", "guest-key-2", now) {
		t.Fatal("a second guest session's restart bucket was drained by the first")
	}
}

// TestIceRestartBucketSurvivesClientLadder is the regression that pins the
// sizing to the client's own recovery ladder. The client (index.html:
// iceRestartThrottleMs 3500, maxIceRestartAttempts 5, backoff [0,1,2,4,8]s,
// recursive re-arm) fires restart_ice at t ≈ 0/3.5/7/11/19s, the 5th rung
// landing ~1s before the 20s connectionRecovery eject. EVERY rung must be
// admitted or a member that would have healed on a throttled rung is ejected
// instead — the exact bug the old burst 2 / refill 1 per 15s bucket caused
// (rungs 3 and 4 silently denied). The compounding variant charges a budgeted
// stale-tile restart from the SAME per-session bucket 5s before the outage.
func TestIceRestartBucketSurvivesClientLadder(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))

	// The client restart-ladder cadence, in offsets from the outage start.
	ladder := []time.Duration{
		0,
		3500 * time.Millisecond,
		7 * time.Second,
		11 * time.Second,
		19 * time.Second,
	}

	t.Run("cold ladder — all five rungs admitted", func(t *testing.T) {
		app := newKanbanBoardApp()
		base := time.Now().UTC()
		for i, off := range ladder {
			if !app.allowMemberIceRestart("room-x", "member-session", base.Add(off)) {
				t.Fatalf("ladder rung %d (t=%s) was throttled — client recovery starved into an eject", i+1, off)
			}
		}
	})

	t.Run("compounding — a budgeted restart 5s before the outage", func(t *testing.T) {
		app := newKanbanBoardApp()
		base := time.Now().UTC()
		// a budgeted stale-tile restart draws from the SAME per-session bucket 5s
		// before the outage ladder begins; the 5s refill must repay it.
		if !app.allowMemberIceRestart("room-x", "member-session", base.Add(-5*time.Second)) {
			t.Fatal("budgeted pre-outage restart was itself throttled")
		}
		for i, off := range ladder {
			if !app.allowMemberIceRestart("room-x", "member-session", base.Add(off)) {
				t.Fatalf("compounding ladder rung %d (t=%s) was throttled despite the 5s pre-spend refill", i+1, off)
			}
		}
	})

	t.Run("guest ladder shares the sizing — all five rungs admitted", func(t *testing.T) {
		app := newKanbanBoardApp()
		base := time.Now().UTC()
		for i, off := range ladder {
			if !app.allowGuestIceRestart("room-x", "guest-key", base.Add(off)) {
				t.Fatalf("guest ladder rung %d (t=%s) was throttled", i+1, off)
			}
		}
	})

	t.Run("a genuine flood is still capped", func(t *testing.T) {
		app := newKanbanBoardApp()
		base := time.Now().UTC()
		// socket-line-rate burst at one instant: only the burst is admitted.
		instant := 0
		for i := 0; i < 100; i++ {
			if app.allowMemberIceRestart("room-x", "flooder", base) {
				instant++
			}
		}
		if instant != int(iceRestartBucketBurst) {
			t.Fatalf("instant flood admitted %d restarts, burst is %v — storm not capped to the burst", instant, iceRestartBucketBurst)
		}
		// a full minute at line rate: burst + at most one token per refill.
		perMinute := 0
		for i := 0; i < 1000; i++ {
			at := base.Add(time.Duration(i) * 60 * time.Millisecond) // 1000 attempts across ~60s
			if app.allowMemberIceRestart("room-x", "flooder-minute", at) {
				perMinute++
			}
		}
		maxPerMinute := int(iceRestartBucketBurst) + int((60*time.Second)/iceRestartBucketRefill) + 1
		if perMinute > maxPerMinute {
			t.Fatalf("minute-long flood admitted %d restarts, cap is ~%d (burst %v + refill 1 per %s) — storm not capped", perMinute, maxPerMinute, iceRestartBucketBurst, iceRestartBucketRefill)
		}
	})
}

// The restart_ice bucket must be independent of the request_participant_tracks
// repair bucket and the guest restart bucket — they cap different actions /
// principals even when the key string collides.
func TestIceRestartBucketIndependentOfOtherBuckets(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	now := time.Now().UTC()

	for i := 0; i < iceRestartBucketBurst; i++ {
		app.allowMemberIceRestart("room-x", "same-key", now)
	}
	if app.allowMemberIceRestart("room-x", "same-key", now) {
		t.Fatal("member ice-restart bucket should be drained")
	}
	// draining ice-restart must not touch the repair bucket for the same key
	if !app.allowMemberMediaRepair("room-x", "same-key", now) {
		t.Fatal("member repair bucket was drained by ice-restart spam — buckets are not independent")
	}
	// nor the guest ice-restart bucket keyed identically
	if !app.allowGuestIceRestart("room-x", "same-key", now) {
		t.Fatal("guest ice-restart bucket was drained by member ice-restart spam — buckets are not independent")
	}
}

// The member bucket keys on the per-socket participant session id, so the
// session cleanup seam must drop it or the map grows one entry per socket.
func TestDropMemberIceRestartBucket(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	now := time.Now().UTC()

	app.allowMemberIceRestart("room-x", "member-session", now)
	app.dropMemberIceRestartBucket("room-x", "member-session")

	app.mu.Lock()
	_, ok := app.roomLiveLocked("room-x").memberIceRestartBuckets["member-session"]
	app.mu.Unlock()
	if ok {
		t.Fatal("dropMemberIceRestartBucket left the session's bucket behind")
	}
}

// TestRestartIceRateLimitWiredInHandler pins the handler seam the way
// TestMemberMediaRepairWiredInHandler does: restart_ice must charge the member
// or guest bucket, drop silently on limit with a rate-limited
// restart_ice_rate_limited log, and release the per-socket bucket on both the
// read-loop cleanup seam and the liveness reap.
func TestRestartIceRateLimitWiredInHandler(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	main := string(source)
	for _, want := range []string{
		`case "restart_ice":`,
		"kanbanApp.allowGuestIceRestart(connRoomID, guest.SessionKey",
		"kanbanApp.allowMemberIceRestart(connRoomID, participantSessionID",
		"restart_ice_rate_limited",
		"dropMemberIceRestartBucket(connRoomID, participantSessionID)",
	} {
		if !strings.Contains(main, want) {
			t.Errorf("main.go is missing the restart_ice rate-limit wiring: %q", want)
		}
	}

	kanbanSource, err := os.ReadFile("kanban.go")
	if err != nil {
		t.Fatalf("read kanban.go: %v", err)
	}
	if !strings.Contains(string(kanbanSource), "delete(state.memberIceRestartBuckets, sessionID)") {
		t.Error("the liveness reap (kanban.go) must release the per-socket ice-restart bucket")
	}
}

/* ---------- gap 2: /client-config TURN credential TTL ---------- */

// The client memoizes /client-config once per page load, so the payload must
// advertise how long the minted TURN credentials live — non-zero only on the
// HMAC mint path, 0 for static or absent creds.
func TestClientConfigTurnCredentialTTL(t *testing.T) {
	turnEnvKeys := []string{
		"MEETING_TURN_SECRET",
		"MEETING_TURN_USERNAME",
		"MEETING_TURN_CREDENTIAL",
		"MEETING_TURN_TTL_SECONDS",
	}
	cases := []struct {
		name string
		env  map[string]string
		want int64
	}{
		{"no turn config", map[string]string{}, 0},
		{"hmac secret default ttl", map[string]string{"MEETING_TURN_SECRET": "s3cr3t"}, 12 * 60 * 60},
		{"hmac secret custom ttl", map[string]string{"MEETING_TURN_SECRET": "s3cr3t", "MEETING_TURN_TTL_SECONDS": "3600"}, 3600},
		{"hmac secret ttl clamped below floor", map[string]string{"MEETING_TURN_SECRET": "s3cr3t", "MEETING_TURN_TTL_SECONDS": "10"}, 12 * 60 * 60},
		{"static creds never expire", map[string]string{"MEETING_TURN_USERNAME": "u", "MEETING_TURN_CREDENTIAL": "c", "MEETING_TURN_SECRET": "s3cr3t"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range turnEnvKeys {
				t.Setenv(key, "")
			}
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			if got := turnCredentialTTLSecondsForClient(); got != tc.want {
				t.Fatalf("turnCredentialTTLSecondsForClient() = %d, want %d", got, tc.want)
			}
			// the field must ride the actual /client-config payload
			cfg := nativeRoomClientConfig()
			raw, ok := cfg["turnCredentialTTLSeconds"]
			if !ok {
				t.Fatal("client config payload is missing turnCredentialTTLSeconds")
			}
			got, ok := raw.(int64)
			if !ok || got != tc.want {
				t.Fatalf("client config turnCredentialTTLSeconds = %v (%T), want %d", raw, raw, tc.want)
			}
		})
	}
}
