package main

import (
	"path/filepath"
	"testing"
)

// Mute silences AMBIENT volume only. A direct mention still delivers — muting a
// channel means "stop buzzing me for every message", never "make me
// unreachable", and conflating those is how someone misses being paged.
func TestMuteSilencesAmbientButNotMentions(t *testing.T) {
	ambient := notificationRecord{ThreadID: "table-1", Kind: notificationKindChat}
	mention := notificationRecord{ThreadID: "table-1", Kind: notificationKindChat, UserEmail: "aj@x.com"}

	if deviceLaneDelivers(ambient, true) {
		t.Fatal("ambient message delivered to a muted thread")
	}
	if !deviceLaneDelivers(mention, true) {
		t.Fatal("direct mention was swallowed by a thread mute")
	}
	if !deviceLaneDelivers(ambient, false) {
		t.Fatal("ambient message dropped on an unmuted thread")
	}
}

func TestThreadMuteRoundTripsAndUnmutes(t *testing.T) {
	t.Setenv("THREAD_MUTES_PATH", filepath.Join(t.TempDir(), "mutes.json"))

	if threadMuted("", "aj@x.com", "table-1") {
		t.Fatal("thread muted before anything was set")
	}
	if err := setThreadMuted("", "AJ@X.com", "table-1", true); err != nil {
		t.Fatalf("mute: %v", err)
	}
	// Stored normalized, so a lookup in different case still matches.
	if !threadMuted("", "aj@x.com", "table-1") {
		t.Fatal("thread not muted after setThreadMuted(true)")
	}
	if err := setThreadMuted("", "aj@x.com", "table-1", false); err != nil {
		t.Fatalf("unmute: %v", err)
	}
	if threadMuted("", "aj@x.com", "table-1") {
		t.Fatal("thread still muted after setThreadMuted(false)")
	}
}

// Muting twice must not stack rows, or unmuting once would leave the thread
// muted and the control would appear broken.
func TestThreadMuteIsIdempotent(t *testing.T) {
	t.Setenv("THREAD_MUTES_PATH", filepath.Join(t.TempDir(), "mutes.json"))

	for index := 0; index < 3; index++ {
		if err := setThreadMuted("", "aj@x.com", "table-1", true); err != nil {
			t.Fatalf("mute %d: %v", index, err)
		}
	}
	if got := len(snapshotThreadMuteStore().Mutes); got != 1 {
		t.Fatalf("mutes = %d, want 1", got)
	}
	if err := setThreadMuted("", "aj@x.com", "table-1", false); err != nil {
		t.Fatalf("unmute: %v", err)
	}
	if threadMuted("", "aj@x.com", "table-1") {
		t.Fatal("one unmute did not clear a thrice-set mute")
	}
}

// One person muting #team must not mute it for everyone else.
func TestThreadMuteIsPerUser(t *testing.T) {
	t.Setenv("THREAD_MUTES_PATH", filepath.Join(t.TempDir(), "mutes.json"))

	if err := setThreadMuted("", "aj@x.com", "table-1", true); err != nil {
		t.Fatalf("mute: %v", err)
	}
	if threadMuted("", "dana@x.com", "table-1") {
		t.Fatal("one user's mute leaked to another user")
	}
}

func TestThreadMutedForUserIgnoresRecordsWithoutAThread(t *testing.T) {
	t.Setenv("THREAD_MUTES_PATH", filepath.Join(t.TempDir(), "mutes.json"))

	if err := setThreadMuted("", "aj@x.com", "table-1", true); err != nil {
		t.Fatalf("mute: %v", err)
	}
	// A record with no thread cannot be muted by a per-thread setting.
	if threadMutedForUser("aj@x.com", notificationRecord{Kind: notificationKindAlert}) {
		t.Fatal("a threadless record was suppressed by a thread mute")
	}
	if !threadMutedForUser("aj@x.com", notificationRecord{ThreadID: "table-1"}) {
		t.Fatal("an ambient record in a muted thread was not suppressed")
	}
}
