package main

import (
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
)

// Chrome treats congestion-control feedback as a PeerConnection-wide
// invariant. A receive m-section generated from the MediaEngine advertised
// transport-cc, while a source-specific m-section passed through
// SetCodecPreferences without it; Chrome then warned that feedback was
// inconsistent and ignored congestion control everywhere.
func TestRoomOfferKeepsTransportCCAcrossMixedMediaSections(t *testing.T) {
	peerConnection, err := newPeerConnection()
	if err != nil {
		t.Fatalf("newPeerConnection: %v", err)
	}
	defer peerConnection.Close()

	for _, kind := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio} {
		if _, err := peerConnection.AddTransceiverFromKind(kind, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
			t.Fatalf("add recvonly %s transceiver: %v", kind, err)
		}
	}

	for _, source := range []struct {
		codec    webrtc.RTPCodecCapability
		trackID  string
		streamID string
	}{
		{codec: roomH264Codec.RTPCodecCapability, trackID: "h264-camera", streamID: "h264-stream"},
		{codec: roomVP8Codec.RTPCodecCapability, trackID: "vp8-camera", streamID: "vp8-stream"},
		{codec: roomOpusCodec.RTPCodecCapability, trackID: "microphone", streamID: "audio-stream"},
	} {
		track, err := webrtc.NewTrackLocalStaticRTP(source.codec, source.trackID, source.streamID)
		if err != nil {
			t.Fatalf("new source track %s: %v", source.trackID, err)
		}
		transceiver, err := peerConnection.AddTransceiverFromTrack(track, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionSendonly,
		})
		if err != nil {
			t.Fatalf("add source track %s: %v", source.trackID, err)
		}
		if err := preferSourceTrackCodec(transceiver, track); err != nil {
			t.Fatalf("prefer source codec %s: %v", source.trackID, err)
		}
	}

	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}

	sections := roomMediaSections(offer.SDP)
	if len(sections) < 5 {
		t.Fatalf("offer has %d audio/video m-sections, want at least 5\n%s", len(sections), offer.SDP)
	}
	for index, section := range sections {
		if !strings.Contains(section, " transport-cc") {
			t.Errorf("media section %d omits transport-cc feedback:\n%s", index, section)
		}
	}
}

func roomMediaSections(sdp string) []string {
	lines := strings.Split(strings.ReplaceAll(sdp, "\r\n", "\n"), "\n")
	sections := []string{}
	var current []string
	flush := func() {
		if len(current) == 0 {
			return
		}
		sections = append(sections, strings.Join(current, "\n"))
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "m=") {
			flush()
			current = nil
			if strings.HasPrefix(line, "m=audio ") || strings.HasPrefix(line, "m=video ") {
				current = []string{line}
			}
			continue
		}
		if current != nil {
			current = append(current, line)
		}
	}
	flush()

	return sections
}

func TestRoomCodecPreferenceAddsTransportCCOnceWithoutMutatingCanonicalCodec(t *testing.T) {
	preferred := roomCodecPreferenceWithTransportCC(roomH264Codec)
	if got := countRTCPFeedback(preferred.RTCPFeedback, webrtc.TypeRTCPFBTransportCC); got != 1 {
		t.Fatalf("preferred H264 transport-cc feedback entries=%d, want 1", got)
	}
	if got := countRTCPFeedback(roomH264Codec.RTCPFeedback, webrtc.TypeRTCPFBTransportCC); got != 0 {
		t.Fatalf("canonical H264 codec was mutated with %d transport-cc entries", got)
	}
	preferred = roomCodecPreferenceWithTransportCC(preferred)
	if got := countRTCPFeedback(preferred.RTCPFeedback, webrtc.TypeRTCPFBTransportCC); got != 1 {
		t.Fatalf("idempotent preference transport-cc entries=%d, want 1", got)
	}
}

func countRTCPFeedback(feedback []webrtc.RTCPFeedback, feedbackType string) int {
	count := 0
	for _, entry := range feedback {
		if strings.EqualFold(strings.TrimSpace(entry.Type), feedbackType) {
			count++
		}
	}

	return count
}
