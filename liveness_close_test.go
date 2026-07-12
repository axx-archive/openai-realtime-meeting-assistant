package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

// TestLivenessSweepClosesReapedPeerConnection proves card-004's core gap is
// closed: the liveness sweep now tears down the *webrtc.PeerConnection a wedged
// session leaves behind, not just its map/slice bookkeeping. Without
// closeSessionMedia the pool entry is unregistered but the PC (and its
// ICE/DTLS/SRTP transports + read pumps) leak until GC never comes.
func TestLivenessSweepClosesReapedPeerConnection(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	snapshotPeerState(t)

	const roomID = "room-a"
	if _, _, err := app.admitParticipantSessionEndpointInRoom(roomID, "AJ", "aj-1", "endpoint-1"); err != nil {
		t.Fatalf("admit AJ: %v", err)
	}

	// A real PC standing in for the half-open session's transport. It carries no
	// media callbacks, so pc.Close() just releases transports and lands in the
	// Closed state — exactly what we assert.
	pc, err := newPeerConnection()
	if err != nil {
		t.Fatalf("create pc: %v", err)
	}
	defer pc.Close() //nolint:errcheck

	listLock.Lock()
	peerConnections = []peerConnectionState{
		{peerConnection: pc, participantName: "AJ", sessionID: "aj-1", roomID: roomID},
	}
	activeParticipantConnections = map[string]peerConnectionState{
		"AJ": {peerConnection: pc, participantName: "AJ", sessionID: "aj-1", roomID: roomID},
	}
	listLock.Unlock()

	// AJ's room socket goes silent past the timeout.
	app.mu.Lock()
	staleAt := time.Now().UTC().Add(-participantLivenessTimeout - time.Minute)
	state := app.roomLiveLocked(roomID)
	state.participants["AJ"] = staleAt
	state.participantSessionLiveness["AJ"]["aj-1"] = staleAt
	app.mu.Unlock()

	app.sweepStaleParticipantSessions()

	if state := pc.ConnectionState(); state != webrtc.PeerConnectionStateClosed {
		t.Fatalf("reaped peer connection state = %s, want Closed", state)
	}

	listLock.RLock()
	defer listLock.RUnlock()
	for _, s := range peerConnections {
		if s.sessionID == "aj-1" {
			t.Fatal("reaped session still in fan-out pool")
		}
	}
	if _, ok := activeParticipantConnections["AJ"]; ok {
		t.Fatal("reaped session still in active participant index")
	}
}

// TestLivenessReapReleasesMemberRepairBucket proves the reap path drops a
// member's per-socket repair bucket — the drop the read-loop defer's
// dropMemberMediaRepairBucket never gets to run for a wedged socket, so without
// it the map grows one entry per reaped connection.
func TestLivenessReapReleasesMemberRepairBucket(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	snapshotPeerState(t)

	const roomID = "room-a"
	if _, _, err := app.admitParticipantSessionEndpointInRoom(roomID, "AJ", "aj-1", "endpoint-1"); err != nil {
		t.Fatalf("admit AJ: %v", err)
	}

	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	state.memberRepairBuckets["aj-1"] = &guestChatBucket{tokens: 1, last: time.Now().UTC()}
	staleAt := time.Now().UTC().Add(-participantLivenessTimeout - time.Minute)
	state.participants["AJ"] = staleAt
	state.participantSessionLiveness["AJ"]["aj-1"] = staleAt
	app.mu.Unlock()

	app.sweepStaleParticipantSessions()

	app.mu.Lock()
	defer app.mu.Unlock()
	if _, ok := app.roomLiveLocked(roomID).memberRepairBuckets["aj-1"]; ok {
		t.Fatal("member repair bucket survived the reap")
	}
}

// TestLivenessReapReleasesGuestMediaBuckets proves the reap releases ALL three
// guest-session buckets (chat + mediaState + telemetry) plus the seat mapping,
// mirroring releaseGuestSeatIfGone — before this fix only chatBuckets was
// cleared, so mediaStateBuckets/telemetryBuckets leaked one entry per guest.
func TestLivenessReapReleasesGuestMediaBuckets(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	snapshotPeerState(t)

	const roomID = "room-a"
	const guestName = "Guest Ada"
	const seatKey = "ada-seat"
	if _, _, err := app.admitParticipantSessionEndpointInRoom(roomID, guestName, "ada-1", "endpoint-1"); err != nil {
		t.Fatalf("admit guest: %v", err)
	}

	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	state.guestSeats[seatKey] = guestName
	state.chatBuckets[seatKey] = &guestChatBucket{}
	state.mediaStateBuckets[seatKey] = &guestChatBucket{}
	state.telemetryBuckets[seatKey] = &guestChatBucket{}
	staleAt := time.Now().UTC().Add(-participantLivenessTimeout - time.Minute)
	state.participants[guestName] = staleAt
	state.participantSessionLiveness[guestName]["ada-1"] = staleAt
	app.mu.Unlock()

	app.sweepStaleParticipantSessions()

	app.mu.Lock()
	defer app.mu.Unlock()
	state = app.roomLiveLocked(roomID)
	if _, ok := state.guestSeats[seatKey]; ok {
		t.Fatal("guest seat survived the reap")
	}
	if _, ok := state.chatBuckets[seatKey]; ok {
		t.Fatal("guest chat bucket survived the reap")
	}
	if _, ok := state.mediaStateBuckets[seatKey]; ok {
		t.Fatal("guest media-state bucket survived the reap")
	}
	if _, ok := state.telemetryBuckets[seatKey]; ok {
		t.Fatal("guest telemetry bucket survived the reap")
	}
}
