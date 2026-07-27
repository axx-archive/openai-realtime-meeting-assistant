package main

import (
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
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
	if !strings.Contains(video, videoOrientationExtensionURI) {
		t.Fatalf("native publisher video offer missing orientation extension %q:\n%s", videoOrientationExtensionURI, video)
	}
}

func TestRoomPublisherAndSubscriberOffersUseSameVideoOrientationExtensionID(t *testing.T) {
	publisher, err := newRoomPeerConnection()
	if err != nil {
		t.Fatalf("new publisher PeerConnection: %v", err)
	}
	t.Cleanup(func() { _ = publisher.Close() })
	if err := addRoomPublisherUplinkTransceivers(publisher); err != nil {
		t.Fatalf("add publisher uplink transceivers: %v", err)
	}
	publisherOffer, err := publisher.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create publisher offer: %v", err)
	}

	subscriber, err := newRoomPeerConnection()
	if err != nil {
		t.Fatalf("new subscriber PeerConnection: %v", err)
	}
	t.Cleanup(func() { _ = subscriber.Close() })
	downlink, err := webrtc.NewTrackLocalStaticRTP(
		roomH264Codec.RTPCodecCapability,
		"phone-camera",
		"phone-stream",
	)
	if err != nil {
		t.Fatalf("create subscriber downlink track: %v", err)
	}
	if _, err := subscriber.AddTransceiverFromTrack(downlink, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionSendonly,
	}); err != nil {
		t.Fatalf("add production-direction subscriber downlink track: %v", err)
	}
	subscriberOffer, err := subscriber.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create subscriber offer: %v", err)
	}

	publisherExtmap := sdpExtmapForURI(
		sdpMediaSectionForKind(publisherOffer.SDP, "video"),
		videoOrientationExtensionURI,
	)
	subscriberExtmap := sdpExtmapForURI(
		sdpMediaSectionForKind(subscriberOffer.SDP, "video"),
		videoOrientationExtensionURI,
	)
	if publisherExtmap == "" || subscriberExtmap == "" {
		t.Fatalf(
			"orientation extension missing across SFU legs: publisher=%q subscriber=%q",
			publisherExtmap,
			subscriberExtmap,
		)
	}
	if publisherExtmap != subscriberExtmap {
		t.Fatalf(
			"orientation extension id differs across SFU legs: publisher=%q subscriber=%q",
			publisherExtmap,
			subscriberExtmap,
		)
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

func sdpExtmapForURI(mediaSection, uri string) string {
	for _, line := range strings.Split(mediaSection, "\r\n") {
		if strings.HasPrefix(line, "a=extmap:") && strings.Contains(line, " "+uri) {
			return line
		}
	}
	return ""
}
