package main

import (
	"reflect"
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestParticipantVideoTrackIDsScopesByKindParticipantAndSession(t *testing.T) {
	snapshotPeerState(t)

	newTrack := func(codec webrtc.RTPCodecCapability, id string) *webrtc.TrackLocalStaticRTP {
		t.Helper()
		track, err := webrtc.NewTrackLocalStaticRTP(codec, id, "publisher-stream")
		if err != nil {
			t.Fatalf("new track %s: %v", id, err)
		}
		return track
	}
	videoCodec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}
	audioCodec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}
	videoCamera := newTrack(videoCodec, "stream:camera:1001")
	videoScreen := newTrack(videoCodec, "stream:screen:1002")
	audio := newTrack(audioCodec, "stream:audio:1003")
	otherSessionVideo := newTrack(videoCodec, "stream:other-session:1004")
	otherParticipantVideo := newTrack(videoCodec, "stream:other-participant:1005")

	listLock.Lock()
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{
		videoCamera.ID():           videoCamera,
		videoScreen.ID():           videoScreen,
		audio.ID():                 audio,
		otherSessionVideo.ID():     otherSessionVideo,
		otherParticipantVideo.ID(): otherParticipantVideo,
	}
	trackParticipants = map[string]string{
		videoCamera.ID():           "AJ",
		videoScreen.ID():           "aj",
		audio.ID():                 "AJ",
		otherSessionVideo.ID():     "AJ",
		otherParticipantVideo.ID(): "Tim",
	}
	trackParticipantSessions = map[string]string{
		videoCamera.ID():           "aj-1",
		videoScreen.ID():           "aj-1",
		audio.ID():                 "aj-1",
		otherSessionVideo.ID():     "aj-2",
		otherParticipantVideo.ID(): "tim-1",
	}
	listLock.Unlock()

	got := participantVideoTrackIDs("AJ", "aj-1")
	want := []string{videoCamera.ID(), videoScreen.ID()}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("participant video targets=%v, want %v", got, want)
	}
}
