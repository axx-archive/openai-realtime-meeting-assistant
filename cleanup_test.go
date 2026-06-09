package main

import (
	"testing"

	"github.com/pion/webrtc/v4"
)

// snapshotPeerState saves and restores the global fan-out bookkeeping so cleanup
// tests can mutate it in isolation.
func snapshotPeerState(t *testing.T) {
	t.Helper()
	listLock.Lock()
	prevPeerConnections := peerConnections
	prevTrackLocals := trackLocals
	prevActive := activeParticipantConnections
	prevTrackParticipants := trackParticipants
	prevTrackParticipantSessions := trackParticipantSessions
	prevTrackSourceIDs := trackSourceIDs
	prevTrackLayerRIDs := trackLayerRIDs
	prevTrackLayerGroups := trackLayerGroups
	prevSubscriberLayerTiers := subscriberLayerTiers
	listLock.Unlock()
	t.Cleanup(func() {
		listLock.Lock()
		peerConnections = prevPeerConnections
		trackLocals = prevTrackLocals
		activeParticipantConnections = prevActive
		trackParticipants = prevTrackParticipants
		trackParticipantSessions = prevTrackParticipantSessions
		trackSourceIDs = prevTrackSourceIDs
		trackLayerRIDs = prevTrackLayerRIDs
		trackLayerGroups = prevTrackLayerGroups
		subscriberLayerTiers = prevSubscriberLayerTiers
		listLock.Unlock()
	})
}

// TestUnregisterParticipantSessionReleasesTracksAndConnection proves that when a
// participant disconnects, their tracks and pool entry are released while other
// participants' media is retained — the core "release tracks promptly" guarantee
// of card-004.
func TestUnregisterParticipantSessionReleasesTracksAndConnection(t *testing.T) {
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}
	ajTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "aj-video", "aj-stream")
	if err != nil {
		t.Fatalf("create AJ track: %v", err)
	}
	timTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "tim-video", "tim-stream")
	if err != nil {
		t.Fatalf("create Tim track: %v", err)
	}

	snapshotPeerState(t)
	listLock.Lock()
	activeParticipantConnections = map[string]peerConnectionState{
		"AJ": {participantName: "AJ", sessionID: "aj-1"},
	}
	peerConnections = []peerConnectionState{
		{participantName: "AJ", sessionID: "aj-1"},
		{participantName: "Tim", sessionID: "tim-1"},
	}
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{
		ajTrack.ID():  ajTrack,
		timTrack.ID(): timTrack,
	}
	trackParticipants = map[string]string{ajTrack.ID(): "AJ", timTrack.ID(): "Tim"}
	trackParticipantSessions = map[string]string{ajTrack.ID(): "aj-1", timTrack.ID(): "tim-1"}
	trackSourceIDs = map[string]string{ajTrack.ID(): "aj-source", timTrack.ID(): "tim-source"}
	listLock.Unlock()

	unregisterParticipantSession("AJ", "aj-1")

	listLock.RLock()
	defer listLock.RUnlock()
	if _, ok := activeParticipantConnections["AJ"]; ok {
		t.Fatal("AJ active session remained after disconnect")
	}
	if len(peerConnections) != 1 || peerConnections[0].participantName != "Tim" {
		t.Fatalf("peerConnections=%v, want only Tim retained", peerConnections)
	}
	if _, ok := trackLocals[ajTrack.ID()]; ok {
		t.Fatal("AJ track remained after disconnect")
	}
	if _, ok := trackSourceIDs[ajTrack.ID()]; ok {
		t.Fatal("AJ track source remained after disconnect")
	}
	if _, ok := trackLocals[timTrack.ID()]; !ok {
		t.Fatal("Tim track was wrongly removed when AJ disconnected")
	}
}

// TestUnregisterParticipantSessionIgnoresStaleSession ensures a disconnect from
// an older session does not evict the participant's current session/tracks.
func TestUnregisterParticipantSessionIgnoresStaleSession(t *testing.T) {
	snapshotPeerState(t)
	listLock.Lock()
	activeParticipantConnections = map[string]peerConnectionState{
		"AJ": {participantName: "AJ", sessionID: "aj-2"},
	}
	peerConnections = []peerConnectionState{{participantName: "AJ", sessionID: "aj-2"}}
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{}
	trackParticipants = map[string]string{}
	trackParticipantSessions = map[string]string{}
	trackSourceIDs = map[string]string{}
	listLock.Unlock()

	unregisterParticipantSession("AJ", "aj-1") // stale session id

	listLock.RLock()
	defer listLock.RUnlock()
	if state, ok := activeParticipantConnections["AJ"]; !ok || state.sessionID != "aj-2" {
		t.Fatalf("current AJ session was evicted by a stale disconnect: %+v", state)
	}
	if len(peerConnections) != 1 {
		t.Fatalf("peerConnections=%d, want current AJ connection retained", len(peerConnections))
	}
}

// TestPrunePeerConnectionPoolRemovesMatchingConnection proves a failed/closed
// peer connection is dropped from the fan-out pool and active index immediately,
// without disturbing other peers.
func TestPrunePeerConnectionPoolRemovesMatchingConnection(t *testing.T) {
	deadPC, err := newPeerConnection()
	if err != nil {
		t.Fatalf("create dead pc: %v", err)
	}
	defer deadPC.Close() //nolint:errcheck
	livePC, err := newPeerConnection()
	if err != nil {
		t.Fatalf("create live pc: %v", err)
	}
	defer livePC.Close() //nolint:errcheck

	snapshotPeerState(t)
	listLock.Lock()
	peerConnections = []peerConnectionState{
		{peerConnection: deadPC, participantName: "AJ", sessionID: "aj-1"},
		{peerConnection: livePC, participantName: "Tim", sessionID: "tim-1"},
	}
	activeParticipantConnections = map[string]peerConnectionState{
		"AJ":  {peerConnection: deadPC, participantName: "AJ", sessionID: "aj-1"},
		"Tim": {peerConnection: livePC, participantName: "Tim", sessionID: "tim-1"},
	}
	listLock.Unlock()

	if removed := prunePeerConnectionPool(deadPC); !removed {
		t.Fatal("prunePeerConnectionPool reported no removal for a pooled connection")
	}

	listLock.RLock()
	defer listLock.RUnlock()
	if len(peerConnections) != 1 || peerConnections[0].peerConnection != livePC {
		t.Fatalf("dead pc not pruned from pool: %+v", peerConnections)
	}
	if _, ok := activeParticipantConnections["AJ"]; ok {
		t.Fatal("dead pc remained in active participant index")
	}
	if _, ok := activeParticipantConnections["Tim"]; !ok {
		t.Fatal("live pc wrongly removed from active participant index")
	}
}

func TestPrunePeerConnectionPoolNilIsNoOp(t *testing.T) {
	if prunePeerConnectionPool(nil) {
		t.Fatal("pruning a nil peer connection should report no removal")
	}
}
