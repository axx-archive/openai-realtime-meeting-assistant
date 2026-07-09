package main

import (
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

// TestRequestParticipantTracksTriggersRenegotiation pins the in-place recovery
// path: a client that noticed missing remote tiles asks for the track roster,
// and the server must also schedule a signalPeerConnections reconciliation so
// the missing media is re-offered — metadata replay alone leaves the tiles
// absent until a full page refresh.
func TestRequestParticipantTracksTriggersRenegotiation(t *testing.T) {
	conn := newIsolatedNativeWebsocket(t, "tom@shareability.com")

	writeNativeWebsocketEvent(t, conn, "participant", map[string]any{
		"client": map[string]string{"platform": "ios", "version": "test"},
	})
	waitForKanbanEvent(t, conn, "access_granted", 5*time.Second)

	signalRequestLock.Lock()
	if signalRequestTimer != nil {
		signalRequestTimer.Stop()
		signalRequestTimer = nil
	}
	signalRequestLock.Unlock()

	writeNativeWebsocketEvent(t, conn, "request_participant_tracks", map[string]string{"reason": "missing remote tiles"})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		signalRequestLock.Lock()
		scheduled := signalRequestTimer != nil
		signalRequestLock.Unlock()
		if scheduled {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("request_participant_tracks did not schedule a peer-connection reconciliation pass")
}

// TestRemoveTrackSkipsRepublishedTrackWithSameID pins the same-SSRC republish
// guard: when a source re-publishes under the same forwarded ID, the stale
// track's deferred unpublish must not delete the fresh track's bookkeeping.
func TestRemoveTrackSkipsRepublishedTrackWithSameID(t *testing.T) {
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}
	oldTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "cam-video", "cam-stream")
	if err != nil {
		t.Fatalf("create old track: %v", err)
	}
	newTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "cam-video", "cam-stream")
	if err != nil {
		t.Fatalf("create republished track: %v", err)
	}

	snapshotPeerState(t)
	t.Cleanup(func() {
		signalRequestLock.Lock()
		if signalRequestTimer != nil {
			signalRequestTimer.Stop()
		}
		signalRequestTimer = nil
		signalRequestLock.Unlock()
	})
	listLock.Lock()
	peerConnections = nil
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{newTrack.ID(): newTrack}
	trackParticipants = map[string]string{newTrack.ID(): "AJ"}
	trackParticipantSessions = map[string]string{newTrack.ID(): "aj-2"}
	trackRooms = map[string]string{}
	trackSourceIDs = map[string]string{}
	trackLayerRIDs = map[string]string{}
	trackLayerGroups = map[string]string{}
	listLock.Unlock()

	removeTrack(oldTrack)

	listLock.RLock()
	stored := trackLocals[newTrack.ID()]
	name := trackParticipants[newTrack.ID()]
	listLock.RUnlock()
	if stored != newTrack || name != "AJ" {
		t.Fatalf("stale unpublish deleted the republished track: stored=%v participant=%q", stored, name)
	}

	removeTrack(newTrack)

	listLock.RLock()
	_, ok := trackLocals[newTrack.ID()]
	listLock.RUnlock()
	if ok {
		t.Fatal("removeTrack should still delete the track it owns")
	}
}
