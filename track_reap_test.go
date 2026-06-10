package main

import (
	"testing"

	"github.com/pion/webrtc/v4"
)

// TestReapStaleLayerTwinsLockedRemovesOlderSameRIDEntry proves that when a
// source re-publishes (renegotiation/SSRC churn), the older forwarded twin in
// the same group+RID is dropped so the newest track wins, while genuine
// simulcast siblings and other sources are untouched.
func TestReapStaleLayerTwinsLockedRemovesOlderSameRIDEntry(t *testing.T) {
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}
	staleTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "stream:cam:999", "stream")
	if err != nil {
		t.Fatalf("create stale track: %v", err)
	}
	siblingTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "stream:cam:3", "stream")
	if err != nil {
		t.Fatalf("create sibling track: %v", err)
	}
	otherTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "stream:mic:9", "stream")
	if err != nil {
		t.Fatalf("create other track: %v", err)
	}

	snapshotPeerState(t)
	listLock.Lock()
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{
		"stream:cam:999": staleTrack,
		"stream:cam:3":   siblingTrack,
		"stream:mic:9":   otherTrack,
	}
	trackParticipants = map[string]string{"stream:cam:999": "AJ", "stream:cam:3": "AJ", "stream:mic:9": "AJ"}
	trackParticipantSessions = map[string]string{"stream:cam:999": "aj-1", "stream:cam:3": "aj-1", "stream:mic:9": "aj-1"}
	trackSourceIDs = map[string]string{"stream:cam:999": "cam", "stream:cam:3": "cam", "stream:mic:9": "mic"}
	trackLayerRIDs = map[string]string{"stream:cam:999": "", "stream:cam:3": "f", "stream:mic:9": ""}
	trackLayerGroups = map[string]string{"stream:cam:999": "stream:cam", "stream:cam:3": "stream:cam", "stream:mic:9": "stream:mic"}
	listLock.Unlock()

	listLock.Lock()
	reapStaleLayerTwinsLocked("stream:cam", "", "stream:cam:1000")
	listLock.Unlock()

	listLock.RLock()
	defer listLock.RUnlock()
	if _, ok := trackLocals["stream:cam:999"]; ok {
		t.Fatal("stale same-group same-RID twin was not reaped")
	}
	for _, m := range []map[string]string{trackParticipants, trackParticipantSessions, trackSourceIDs, trackLayerRIDs, trackLayerGroups} {
		if _, ok := m["stream:cam:999"]; ok {
			t.Fatal("stale twin bookkeeping was not fully reaped")
		}
	}
	if _, ok := trackLocals["stream:cam:3"]; !ok {
		t.Fatal("simulcast sibling with a different RID was wrongly reaped")
	}
	if _, ok := trackLocals["stream:mic:9"]; !ok {
		t.Fatal("track from another group was wrongly reaped")
	}
}

// TestReapStaleLayerTwinsLockedKeepsNewTrack ensures the reap never removes the
// entry it is making room for, even when called after the insert.
func TestReapStaleLayerTwinsLockedKeepsNewTrack(t *testing.T) {
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}
	liveTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "stream:cam:1000", "stream")
	if err != nil {
		t.Fatalf("create live track: %v", err)
	}

	snapshotPeerState(t)
	listLock.Lock()
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{"stream:cam:1000": liveTrack}
	trackParticipants = map[string]string{"stream:cam:1000": "AJ"}
	trackParticipantSessions = map[string]string{"stream:cam:1000": "aj-1"}
	trackSourceIDs = map[string]string{"stream:cam:1000": "cam"}
	trackLayerRIDs = map[string]string{"stream:cam:1000": ""}
	trackLayerGroups = map[string]string{"stream:cam:1000": "stream:cam"}
	listLock.Unlock()

	listLock.Lock()
	reapStaleLayerTwinsLocked("stream:cam", "", "stream:cam:1000")
	listLock.Unlock()

	listLock.RLock()
	defer listLock.RUnlock()
	if _, ok := trackLocals["stream:cam:1000"]; !ok {
		t.Fatal("the new track itself must never be reaped")
	}
}
