package main

import (
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
)

// Every track in prod logged has_rtx=false: without video/rtx apt codecs in the
// media engine, pion strips RTX from browser offers and never allocates repair
// SSRCs on its own senders, so the only loss recovery is PLI (full keyframe).
// These tests pin that room SDP negotiates NACK-driven RTX repair both ways.

func TestRoomOfferNegotiatesRTXAndNack(t *testing.T) {
	peerConnection, err := newPeerConnection()
	if err != nil {
		t.Fatalf("newPeerConnection: %v", err)
	}
	defer peerConnection.Close()

	if _, err := peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo); err != nil {
		t.Fatalf("AddTransceiverFromKind: %v", err)
	}

	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}

	for _, want := range []string{
		// Existing negotiation must be preserved.
		"a=rtpmap:102 H264/90000",
		"profile-level-id=42e01f",
		"a=rtpmap:96 VP8/90000",
		// NACK feedback so publishers retransmit lost uplink packets.
		"a=rtcp-fb:102 nack",
		"a=rtcp-fb:96 nack",
		// RTX repair codecs (RFC 4588) bound to each video codec.
		"a=rtpmap:103 rtx/90000",
		"a=fmtp:103 apt=102",
		"a=rtpmap:97 rtx/90000",
		"a=fmtp:97 apt=96",
	} {
		if !strings.Contains(offer.SDP, want) {
			t.Errorf("offer SDP missing %q", want)
		}
	}
}

func TestRoomSenderOfferCarriesRTXRepairStream(t *testing.T) {
	peerConnection, err := newPeerConnection()
	if err != nil {
		t.Fatalf("newPeerConnection: %v", err)
	}
	defer peerConnection.Close()

	trackLocal, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeH264,
		ClockRate:   90000,
		SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
	}, "video", "room-rtx-test")
	if err != nil {
		t.Fatalf("NewTrackLocalStaticRTP: %v", err)
	}
	if _, err := peerConnection.AddTrack(trackLocal); err != nil {
		t.Fatalf("AddTrack: %v", err)
	}

	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}

	// The FID ssrc-group is what lets the subscribing browser bind the NACK
	// responder's retransmissions (sent on the repair SSRC) back to the media
	// stream instead of discarding them as an unknown SSRC.
	if !strings.Contains(offer.SDP, "a=ssrc-group:FID ") {
		t.Errorf("sender offer SDP missing RTX repair ssrc-group:FID\n%s", offer.SDP)
	}
}
