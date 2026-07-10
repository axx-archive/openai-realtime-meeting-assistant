package main

// Pins FIX 3 of the 2026-07-10 incident: RTP header extension ids are
// negotiated PER TRANSPORT (RFC 8285 §6), so forwarding the PUBLISHER's
// identity/transport-scoped extensions (sdes:mid, rid, repaired-rid) verbatim
// to subscribers — whose demux resolves the same id against THEIR OWN
// negotiation — is spec-incorrect and the suspected wedge behind Tom's
// permanent per-subscriber freeze. Media-scoped extensions must survive the
// strip: commit 0a46b50 started preserving extensions precisely because a
// strip-everything forward made phone video orientation unstable.

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

const (
	testAbsSendTimeURI = "http://www.webrtc.org/experiments/rtp-hdrext/abs-send-time"
	testAudioLevelURI  = "urn:ietf:params:rtp-hdrext:ssrc-audio-level"
)

func publisherRTPPacketWithExtensions(t *testing.T) *rtp.Packet {
	t.Helper()
	packet := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 100,
			Timestamp:      1000,
			SSRC:           42,
		},
		Payload: []byte{0x01, 0x02, 0x03},
	}
	// The live guest published extension ids 1 (sdes:mid) and 4; mirror that
	// shape plus rid and two media-scoped extensions.
	for id, payload := range map[uint8][]byte{
		1: []byte("0"),        // sdes:mid — publisher-transport identity
		2: []byte("hi"),       // sdes:rtp-stream-id
		4: {0xAA, 0xBB, 0xCC}, // abs-send-time — media-scoped, must survive
		5: {0x80},             // ssrc-audio-level — media-scoped, must survive
	} {
		if err := packet.Header.SetExtension(id, payload); err != nil {
			t.Fatalf("set extension %d: %v", id, err)
		}
	}
	return packet
}

func TestTransportScopedRTPExtensionIDsResolveSDESURIs(t *testing.T) {
	params := webrtc.RTPParameters{HeaderExtensions: []webrtc.RTPHeaderExtensionParameter{
		{ID: 1, URI: sdesMidExtensionURI},
		{ID: 2, URI: sdesRTPStreamIDExtensionURI},
		{ID: 3, URI: sdesRepairedRTPStreamIDExtensionURI},
		{ID: 4, URI: testAbsSendTimeURI},
		{ID: 5, URI: testAudioLevelURI},
	}}
	ids := transportScopedRTPExtensionIDsFromParameters(params)
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Fatalf("transport-scoped ids=%v, want the three SDES ids [1 2 3]", ids)
	}
	if got := transportScopedRTPExtensionIDs(nil); got != nil {
		t.Fatalf("nil receiver should resolve no ids, got %v", got)
	}
}

func TestForwardPathStripsTransportScopedExtensionsAndKeepsMediaScoped(t *testing.T) {
	packet := publisherRTPPacketWithExtensions(t)
	trackLocal, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, "fwd-video", "fwd-stream")
	if err != nil {
		t.Fatalf("create forward track: %v", err)
	}

	// The exact write seam OnTrack uses (unbound track: the write itself is a
	// no-op, the strip must already have happened on the shared packet).
	if err := forwardPublisherRTP(trackLocal, packet, []uint8{1, 2, 3}); err != nil {
		t.Fatalf("forward publisher packet: %v", err)
	}

	if packet.Header.GetExtension(1) != nil || packet.Header.GetExtension(2) != nil {
		t.Fatalf("sdes:mid/rid survived the forward path: ids=%v", packet.Header.GetExtensionIDs())
	}
	if !bytes.Equal(packet.Header.GetExtension(4), []byte{0xAA, 0xBB, 0xCC}) {
		t.Fatal("media-scoped abs-send-time extension was not preserved (the 0a46b50 constraint)")
	}
	if !bytes.Equal(packet.Header.GetExtension(5), []byte{0x80}) {
		t.Fatal("media-scoped audio-level extension was not preserved")
	}
	if !packet.Header.Extension {
		t.Fatal("header extension flag dropped while media-scoped extensions remain")
	}

	// Wire truth: the stripped packet round-trips with only the media-scoped
	// extensions.
	raw, err := packet.Marshal()
	if err != nil {
		t.Fatalf("marshal stripped packet: %v", err)
	}
	roundTrip := &rtp.Packet{}
	if err := roundTrip.Unmarshal(raw); err != nil {
		t.Fatalf("unmarshal stripped packet: %v", err)
	}
	if ids := roundTrip.Header.GetExtensionIDs(); len(ids) != 2 {
		t.Fatalf("wire packet extension ids=%v, want exactly the two media-scoped ones", ids)
	}
	if !bytes.Equal(roundTrip.Payload, packet.Payload) {
		t.Fatal("payload changed across the strip")
	}
}

func TestStripAllExtensionsDisablesTheExtensionBlock(t *testing.T) {
	packet := &rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 96, SSRC: 42}, Payload: []byte{0x01}}
	if err := packet.Header.SetExtension(1, []byte("0")); err != nil {
		t.Fatalf("set mid extension: %v", err)
	}
	stripTransportScopedRTPExtensions(packet, []uint8{1})
	if packet.Header.Extension {
		t.Fatal("empty extension list must not marshal as a zero-length extension block")
	}
	raw, err := packet.Marshal()
	if err != nil {
		t.Fatalf("marshal fully stripped packet: %v", err)
	}
	roundTrip := &rtp.Packet{}
	if err := roundTrip.Unmarshal(raw); err != nil {
		t.Fatalf("unmarshal fully stripped packet: %v", err)
	}
	if roundTrip.Header.Extension || len(roundTrip.Header.GetExtensionIDs()) != 0 {
		t.Fatalf("fully stripped packet still carries extensions: %v", roundTrip.Header.GetExtensionIDs())
	}

	// Absent negotiated ids (nothing to strip): the packet is untouched — the
	// preserve-by-default posture of 0a46b50 stands.
	preserved := publisherRTPPacketWithExtensions(t)
	stripTransportScopedRTPExtensions(preserved, nil)
	if len(preserved.Header.GetExtensionIDs()) != 4 {
		t.Fatalf("nil strip list mutated the packet: %v", preserved.Header.GetExtensionIDs())
	}
}

// TestOnTrackForwardsThroughTheStripSeam pins (source-level, the
// registeredHTTPRoutes idiom) that the OnTrack read loop actually forwards
// through forwardPublisherRTP with the publisher-negotiated strip ids — a
// regression to a raw trackLocal.WriteRTP(packet) would silently reintroduce
// the transport-scoped leak.
func TestOnTrackForwardsThroughTheStripSeam(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !strings.Contains(string(source), "stripExtensionIDs := transportScopedRTPExtensionIDs(receiver)") {
		t.Fatal("OnTrack no longer resolves the publisher's transport-scoped extension ids from its receiver")
	}
	if !strings.Contains(string(source), "forwardPublisherRTP(trackLocal, packet, stripExtensionIDs)") {
		t.Fatal("OnTrack read loop no longer forwards through forwardPublisherRTP (the strip seam)")
	}
}
