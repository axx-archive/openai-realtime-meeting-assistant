package main

import (
	"strings"
	"testing"
)

func TestRoomPublisherOfferUsesNativeCompatibleH264Only(t *testing.T) {
	peer, err := newRoomPeerConnection()
	if err != nil {
		t.Fatalf("newRoomPeerConnection: %v", err)
	}
	t.Cleanup(func() { _ = peer.Close() })

	if err := addRoomPublisherUplinkTransceivers(peer); err != nil {
		t.Fatalf("addRoomPublisherUplinkTransceivers: %v", err)
	}

	offer, err := peer.CreateOffer(nil)
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	video := sdpMediaSectionForKind(offer.SDP, "video")
	for _, want := range []string{
		"m=video 9 UDP/TLS/RTP/SAVPF 102 103",
		"a=rtpmap:102 H264/90000",
		"a=fmtp:103 apt=102",
	} {
		if !strings.Contains(video, want) {
			t.Fatalf("video offer missing %q:\n%s", want, video)
		}
	}
	if strings.Contains(strings.ToUpper(video), "VP8/90000") {
		t.Fatalf("native-compatible publisher offer still includes VP8:\n%s", video)
	}
}

func sdpMediaSectionForKind(sdp, kind string) string {
	marker := "m=" + kind + " "
	start := strings.Index(sdp, marker)
	if start < 0 {
		return ""
	}
	rest := sdp[start:]
	if next := strings.Index(rest[2:], "\r\nm="); next >= 0 {
		return rest[:next+2]
	}
	return rest
}
