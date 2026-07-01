package main

import (
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
)

type testTimeoutError struct{}

func (testTimeoutError) Error() string   { return "timeout" }
func (testTimeoutError) Timeout() bool   { return true }
func (testTimeoutError) Temporary() bool { return true }

func TestSignalingOfferMetadataIncrementsPerPeer(t *testing.T) {
	peer := peerConnectionState{sessionID: "participant 1"}

	first := startPendingOfferMetadata(&peer)
	second := startPendingOfferMetadata(&peer)

	if first.Revision != 1 || second.Revision != 2 {
		t.Fatalf("revisions = %d, %d; want 1, 2", first.Revision, second.Revision)
	}
	if first.OfferID == "" || second.OfferID == "" || first.OfferID == second.OfferID {
		t.Fatalf("offer ids should be non-empty and unique: first=%q second=%q", first.OfferID, second.OfferID)
	}
	if !strings.Contains(first.OfferID, "participant_1") {
		t.Fatalf("offer id %q should include sanitized session id", first.OfferID)
	}
}

func TestSignalingAnswerMetadataRejectsStaleAnswers(t *testing.T) {
	pending := signalingOfferMetadata{OfferID: "participant-1-offer-2", Revision: 2}
	cases := []struct {
		name   string
		answer signalingOfferMetadata
		ignore bool
	}{
		{
			name:   "legacy answer without metadata remains accepted",
			answer: signalingOfferMetadata{},
			ignore: false,
		},
		{
			name:   "matching id and revision accepted",
			answer: signalingOfferMetadata{OfferID: "participant-1-offer-2", Revision: 2},
			ignore: false,
		},
		{
			name:   "older revision ignored",
			answer: signalingOfferMetadata{OfferID: "participant-1-offer-1", Revision: 1},
			ignore: true,
		},
		{
			name:   "wrong id ignored even with matching revision",
			answer: signalingOfferMetadata{OfferID: "other-offer-2", Revision: 2},
			ignore: true,
		},
		{
			name:   "matching revision alone accepted",
			answer: signalingOfferMetadata{Revision: 2},
			ignore: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ignore, _ := shouldIgnoreAnswerForPendingOffer(tc.answer, pending)
			if ignore != tc.ignore {
				t.Fatalf("ignore=%v, want %v", ignore, tc.ignore)
			}
		})
	}
}

func TestSignalingMetadataCanComeFromEnvelopeOrData(t *testing.T) {
	fromEnvelope := signalingMetadataFromMessage(websocketMessage{
		OfferID:  "offer-top",
		Revision: 7,
		Data:     `{"type":"answer","sdp":"v=0","offerId":"offer-data","revision":3}`,
	})
	if fromEnvelope.OfferID != "offer-top" || fromEnvelope.Revision != 7 {
		t.Fatalf("top-level metadata should win, got %+v", fromEnvelope)
	}

	fromData := signalingMetadataFromMessage(websocketMessage{
		Data: `{"type":"answer","sdp":"v=0","offerId":"offer-data","revision":3}`,
	})
	if fromData.OfferID != "offer-data" || fromData.Revision != 3 {
		t.Fatalf("data metadata not parsed: %+v", fromData)
	}
}

func TestRoomWebsocketHeartbeatTimings(t *testing.T) {
	if !roomWebsocketHeartbeatTimingsValid() {
		t.Fatalf("invalid heartbeat timings: write=%s ping=%s read=%s", websocketWriteTimeout, websocketPingInterval, websocketReadTimeout)
	}
	if !isWebsocketReadTimeout(testTimeoutError{}) {
		t.Fatal("timeout net.Error should be recognized as a websocket read timeout")
	}
	if isWebsocketReadTimeout(nil) {
		t.Fatal("nil error must not be treated as a timeout")
	}
}

func TestStableRoomMediaEngineOfferAdvertisesFeedbackAndExtensions(t *testing.T) {
	peerConnection, err := newPeerConnection()
	if err != nil {
		t.Fatalf("newPeerConnection: %v", err)
	}
	defer peerConnection.Close() //nolint:errcheck

	for _, typ := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio} {
		if _, err := peerConnection.AddTransceiverFromKind(typ, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
			t.Fatalf("add %s transceiver: %v", typ, err)
		}
	}

	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	if err := peerConnection.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local description: %v", err)
	}

	for _, want := range []string{
		"a=rtcp-fb:102 nack",
		"a=rtcp-fb:102 nack pli",
		"a=rtcp-fb:102 transport-cc",
		"a=rtcp-fb:96 nack",
		"a=rtcp-fb:96 nack pli",
		"a=rtcp-fb:96 transport-cc",
		"urn:ietf:params:rtp-hdrext:sdes:mid",
		"urn:ietf:params:rtp-hdrext:sdes:rtp-stream-id",
		"http://www.ietf.org/id/draft-holmer-rmcat-transport-wide-cc-extensions-01",
	} {
		if !strings.Contains(offer.SDP, want) {
			t.Fatalf("offer SDP missing %q:\n%s", want, offer.SDP)
		}
	}
}
