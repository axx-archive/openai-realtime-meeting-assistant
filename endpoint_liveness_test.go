package main

import (
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func reapParticipantEndpointsAt(app *kanbanBoardApp, now time.Time, timeout time.Duration) map[string][]participantLivenessReap {
	app.mu.Lock()
	defer app.mu.Unlock()
	return app.reapStaleParticipantSessionsLocked(now, timeout)
}

func TestEndpointLivenessReapsOnlyStaleDevice(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-laptop", "endpoint-laptop"); err != nil {
		t.Fatalf("admit laptop: %v", err)
	}
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-phone", "endpoint-phone"); err != nil {
		t.Fatalf("admit phone: %v", err)
	}

	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	wantMedia := participantMediaState{MicMuted: true, CameraOff: true, UpdatedAt: "preserve-me"}
	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	// Deliberately make the legacy aggregate inconsistent/stale. The explicit
	// fresh laptop stamp must win, or the old account-scoped bug survives.
	state.participants["AJ"] = now.Add(-2 * participantLivenessTimeout)
	state.participantSessionLiveness["AJ"]["aj-laptop"] = now
	state.participantSessionLiveness["AJ"]["aj-phone"] = now.Add(-2 * participantLivenessTimeout)
	state.participantMedia["AJ"] = wantMedia
	state.memberRepairBuckets["aj-laptop"] = &guestChatBucket{}
	state.memberRepairBuckets["aj-phone"] = &guestChatBucket{}
	state.memberIceRestartBuckets["aj-laptop"] = &guestChatBucket{}
	state.memberIceRestartBuckets["aj-phone"] = &guestChatBucket{}
	app.mu.Unlock()

	reaped := reapParticipantEndpointsAt(app, now, participantLivenessTimeout)[officeRoomID]
	if len(reaped) != 1 {
		t.Fatalf("reaped=%#v, want one account result", reaped)
	}
	if reaped[0].name != "AJ" || !reaped[0].stillPresent || reaped[0].reapedEndpoints != 1 {
		t.Fatalf("reap=%#v, want AJ partial one-endpoint reap", reaped[0])
	}
	if len(reaped[0].sessionIDs) != 1 || reaped[0].sessionIDs[0] != "aj-phone" {
		t.Fatalf("reaped sessions=%v, want only phone", reaped[0].sessionIDs)
	}
	if !app.participantSessionCurrent("AJ", "aj-laptop") {
		t.Fatal("healthy laptop session was removed")
	}
	if app.participantSessionCurrent("AJ", "aj-phone") {
		t.Fatal("stale phone session survived")
	}
	if count := app.activeParticipantCount(officeRoomID); count != 1 {
		t.Fatalf("seat count=%d, want AJ to remain one seat", count)
	}
	if snapshot := app.participantSnapshot(); !containsParticipant(snapshot, "AJ") {
		t.Fatalf("AJ disappeared from roster after partial reap: %v", snapshot)
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	state = app.roomLiveLocked(officeRoomID)
	if state.participantCounts["AJ"] != 1 {
		t.Fatalf("endpoint count=%d, want 1", state.participantCounts["AJ"])
	}
	if got := state.participants["AJ"]; !got.Equal(now) {
		t.Fatalf("aggregate liveness=%s, want surviving laptop stamp %s", got, now)
	}
	if got := state.participantMedia["AJ"]; got != wantMedia {
		t.Fatalf("shared media=%#v, want preserved %#v", got, wantMedia)
	}
	if _, ok := state.memberRepairBuckets["aj-phone"]; ok {
		t.Fatal("stale phone repair bucket survived")
	}
	if _, ok := state.memberIceRestartBuckets["aj-phone"]; ok {
		t.Fatal("stale phone ICE bucket survived")
	}
	if _, ok := state.memberRepairBuckets["aj-laptop"]; !ok {
		t.Fatal("healthy laptop repair bucket was removed")
	}
	if _, ok := state.memberIceRestartBuckets["aj-laptop"]; !ok {
		t.Fatal("healthy laptop ICE bucket was removed")
	}
}

func TestEndpointLivenessAllStaleRemovesAccount(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	for endpoint, session := range map[string]string{
		"endpoint-laptop": "aj-laptop",
		"endpoint-phone":  "aj-phone",
	} {
		if _, _, err := app.admitParticipantSessionEndpoint("AJ", session, endpoint); err != nil {
			t.Fatalf("admit %s: %v", endpoint, err)
		}
	}

	now := time.Date(2026, 7, 12, 13, 0, 0, 0, time.UTC)
	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	// Keep the aggregate fresh to prove the endpoint stamps, not the old
	// account-only timeout, drive this removal.
	state.participants["AJ"] = now
	state.participantSessionLiveness["AJ"]["aj-laptop"] = now.Add(-2 * participantLivenessTimeout)
	state.participantSessionLiveness["AJ"]["aj-phone"] = now.Add(-2 * participantLivenessTimeout)
	app.mu.Unlock()

	reaped := reapParticipantEndpointsAt(app, now, participantLivenessTimeout)[officeRoomID]
	if len(reaped) != 1 || reaped[0].stillPresent || reaped[0].reapedEndpoints != 2 {
		t.Fatalf("reaped=%#v, want one last-endpoint account removal", reaped)
	}
	sort.Strings(reaped[0].sessionIDs)
	if got := reaped[0].sessionIDs; len(got) != 2 || got[0] != "aj-laptop" || got[1] != "aj-phone" {
		t.Fatalf("reaped sessions=%v, want both devices", got)
	}
	if count := app.activeParticipantCount(officeRoomID); count != 0 {
		t.Fatalf("seat count=%d, want empty room", count)
	}
	if snapshot := app.participantSnapshot(); containsParticipant(snapshot, "AJ") {
		t.Fatalf("AJ survived last-endpoint reap: %v", snapshot)
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	state = app.roomLiveLocked(officeRoomID)
	if _, ok := state.participantEndpoints["AJ"]; ok {
		t.Fatal("endpoint map survived last-endpoint reap")
	}
	if _, ok := state.participantSessionLiveness["AJ"]; ok {
		t.Fatal("session liveness map survived last-endpoint reap")
	}
	if _, ok := state.participantMedia["AJ"]; ok {
		t.Fatal("shared media survived last-endpoint reap")
	}
}

func TestEndpointLivenessStaleCleanupCannotRemoveReplacement(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-old", "endpoint-laptop"); err != nil {
		t.Fatalf("admit old session: %v", err)
	}

	now := time.Date(2026, 7, 12, 14, 0, 0, 0, time.UTC)
	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	state.participants["AJ"] = now.Add(-2 * participantLivenessTimeout)
	state.participantSessionLiveness["AJ"]["aj-old"] = now.Add(-2 * participantLivenessTimeout)
	app.mu.Unlock()
	reaped := reapParticipantEndpointsAt(app, now, participantLivenessTimeout)[officeRoomID]
	if len(reaped) != 1 || len(reaped[0].sessionIDs) != 1 || reaped[0].sessionIDs[0] != "aj-old" {
		t.Fatalf("reaped=%#v, want old session", reaped)
	}

	// A refresh lands after the sweep released app.mu but before the old
	// socket's deferred cleanup finishes. That cleanup is session-scoped and
	// therefore cannot evict the new session in the same endpoint slot.
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-new", "endpoint-laptop"); err != nil {
		t.Fatalf("admit replacement: %v", err)
	}
	app.mu.Lock()
	beforeLateFrame := app.roomLiveLocked(officeRoomID).participants["AJ"]
	app.mu.Unlock()
	app.touchParticipantSessionLivenessInRoom(officeRoomID, "AJ", "aj-old")
	app.mu.Lock()
	state = app.roomLiveLocked(officeRoomID)
	if got := state.participants["AJ"]; !got.Equal(beforeLateFrame) {
		app.mu.Unlock()
		t.Fatalf("late replaced-session frame changed aggregate stamp from %s to %s", beforeLateFrame, got)
	}
	if _, ok := state.participantSessionLiveness["AJ"]["aj-old"]; ok {
		app.mu.Unlock()
		t.Fatal("late frame resurrected the replaced session's liveness stamp")
	}
	app.mu.Unlock()
	if removed, stillPresent := app.forgetParticipantSessionResult("AJ", "aj-old"); removed || !stillPresent {
		t.Fatalf("stale cleanup removed replacement: removed=%v stillPresent=%v", removed, stillPresent)
	}
	if !app.participantSessionCurrent("AJ", "aj-new") {
		t.Fatal("replacement session is not current after stale cleanup")
	}
	if app.participantSessionCurrent("AJ", "aj-old") {
		t.Fatal("old session became current again")
	}
	if count := app.activeParticipantCount(officeRoomID); count != 1 {
		t.Fatalf("seat count=%d, want replacement account present", count)
	}
}

func TestTouchParticipantSessionLivenessRefreshesOnlyCurrentSocket(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-laptop", "endpoint-laptop"); err != nil {
		t.Fatalf("admit laptop: %v", err)
	}
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-phone", "endpoint-phone"); err != nil {
		t.Fatalf("admit phone: %v", err)
	}

	staleAt := time.Now().UTC().Add(-2 * participantLivenessTimeout)
	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	state.participants["AJ"] = staleAt
	state.participantSessionLiveness["AJ"]["aj-laptop"] = staleAt
	state.participantSessionLiveness["AJ"]["aj-phone"] = staleAt
	app.mu.Unlock()

	app.touchParticipantSessionLivenessInRoom(officeRoomID, "AJ", "aj-laptop")
	app.mu.Lock()
	state = app.roomLiveLocked(officeRoomID)
	laptopAt := state.participantSessionLiveness["AJ"]["aj-laptop"]
	phoneAt := state.participantSessionLiveness["AJ"]["aj-phone"]
	aggregateAt := state.participants["AJ"]
	app.mu.Unlock()
	if !laptopAt.After(staleAt) {
		t.Fatalf("laptop stamp=%s, want newer than %s", laptopAt, staleAt)
	}
	if !phoneAt.Equal(staleAt) {
		t.Fatalf("phone stamp=%s, want untouched %s", phoneAt, staleAt)
	}
	if !aggregateAt.Equal(laptopAt) {
		t.Fatalf("aggregate=%s, want refreshed laptop stamp %s", aggregateAt, laptopAt)
	}

	reaped := reapParticipantEndpointsAt(app, laptopAt, participantLivenessTimeout)[officeRoomID]
	if len(reaped) != 1 || !reaped[0].stillPresent || len(reaped[0].sessionIDs) != 1 || reaped[0].sessionIDs[0] != "aj-phone" {
		t.Fatalf("reaped=%#v, want stale phone only after laptop frame", reaped)
	}
}

func TestGuestEndpointLivenessPreservesThenReleasesSeatAndBuckets(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	const roomID = "room-guests"
	const guestSessionKey = "guest-auth-session"
	guestName, _, err := app.admitGuestParticipant(roomID, guestSessionKey, "Ada", "guest-phone")
	if err != nil {
		t.Fatalf("admit guest phone: %v", err)
	}
	guestName2, _, err := app.admitGuestParticipant(roomID, guestSessionKey, "Ada", "guest-laptop")
	if err != nil {
		t.Fatalf("admit guest laptop: %v", err)
	}
	if guestName2 != guestName {
		t.Fatalf("same guest session got names %q and %q", guestName, guestName2)
	}

	now := time.Date(2026, 7, 12, 15, 0, 0, 0, time.UTC)
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	state.participants[guestName] = now
	state.participantSessionLiveness[guestName]["guest-laptop"] = now
	state.participantSessionLiveness[guestName]["guest-phone"] = now.Add(-2 * participantLivenessTimeout)
	state.chatBuckets[guestSessionKey] = &guestChatBucket{}
	state.mediaStateBuckets[guestSessionKey] = &guestChatBucket{}
	state.telemetryBuckets[guestSessionKey] = &guestChatBucket{}
	state.guestIceRestartBuckets[guestSessionKey] = &guestChatBucket{}
	app.mu.Unlock()

	partial := reapParticipantEndpointsAt(app, now, participantLivenessTimeout)[roomID]
	if len(partial) != 1 || !partial[0].stillPresent || len(partial[0].sessionIDs) != 1 || partial[0].sessionIDs[0] != "guest-phone" {
		t.Fatalf("partial guest reap=%#v, want only stale phone", partial)
	}
	app.mu.Lock()
	state = app.roomLiveLocked(roomID)
	if state.guestSeats[guestSessionKey] != guestName {
		t.Fatal("guest seat was released while laptop remained")
	}
	if state.participantCounts[guestName] != 1 {
		t.Fatalf("guest endpoint count=%d, want 1", state.participantCounts[guestName])
	}
	for label, buckets := range map[string]map[string]*guestChatBucket{
		"chat": state.chatBuckets, "media": state.mediaStateBuckets,
		"telemetry": state.telemetryBuckets, "ice": state.guestIceRestartBuckets,
	} {
		if _, ok := buckets[guestSessionKey]; !ok {
			t.Fatalf("guest %s bucket removed during partial reap", label)
		}
	}
	app.mu.Unlock()

	later := now.Add(3 * participantLivenessTimeout)
	app.mu.Lock()
	state = app.roomLiveLocked(roomID)
	state.participants[guestName] = later
	state.participantSessionLiveness[guestName]["guest-laptop"] = now
	app.mu.Unlock()
	final := reapParticipantEndpointsAt(app, later, participantLivenessTimeout)[roomID]
	if len(final) != 1 || final[0].stillPresent || len(final[0].sessionIDs) != 1 || final[0].sessionIDs[0] != "guest-laptop" {
		t.Fatalf("final guest reap=%#v, want last laptop removal", final)
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	state = app.roomLiveLocked(roomID)
	if _, ok := state.guestSeats[guestSessionKey]; ok {
		t.Fatal("guest seat survived last-endpoint reap")
	}
	for label, buckets := range map[string]map[string]*guestChatBucket{
		"chat": state.chatBuckets, "media": state.mediaStateBuckets,
		"telemetry": state.telemetryBuckets, "ice": state.guestIceRestartBuckets,
	} {
		if _, ok := buckets[guestSessionKey]; ok {
			t.Fatalf("guest %s bucket survived last-endpoint reap", label)
		}
	}
}

func TestEndpointLivenessLegacyAccountStampFallback(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	now := time.Date(2026, 7, 12, 16, 0, 0, 0, time.UTC)

	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	state.participants["AJ"] = now.Add(-2 * participantLivenessTimeout)
	state.participantCounts["AJ"] = 1
	state.participantEndpoints["AJ"] = map[string]string{"legacy-aj": "aj-session"}
	delete(state.participantSessionLiveness, "AJ")
	state.participants["Tim"] = now
	state.participantCounts["Tim"] = 1
	state.participantEndpoints["Tim"] = map[string]string{"legacy-tim": "tim-session"}
	delete(state.participantSessionLiveness, "Tim")
	app.mu.Unlock()

	reaped := reapParticipantEndpointsAt(app, now, participantLivenessTimeout)[officeRoomID]
	if len(reaped) != 1 || reaped[0].name != "AJ" || len(reaped[0].sessionIDs) != 1 || reaped[0].sessionIDs[0] != "aj-session" {
		t.Fatalf("legacy reap=%#v, want stale AJ only", reaped)
	}
	if !app.participantSessionCurrent("Tim", "tim-session") {
		t.Fatal("fresh legacy Tim session was reaped")
	}
	if snapshot := app.participantSnapshot(); containsParticipant(snapshot, "AJ") || !containsParticipant(snapshot, "Tim") {
		t.Fatalf("legacy roster=%v, want Tim only", snapshot)
	}
}
