package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

func TestSignalPeerConnectionsRebindsSameIDRepublishAndForwardsRTP(t *testing.T) {
	testSameIDRepublish(t, "publisher-stream", 1)
}

func TestSignalPeerConnectionsFallsBackToRemoveAddForChangedEnvelope(t *testing.T) {
	testSameIDRepublish(t, "republished-stream", 2)
}

func testSameIDRepublish(t *testing.T, replacementStreamID string, wantSignalCount int) {
	t.Helper()
	snapshotPeerState(t)

	serverPeer, err := newPeerConnection()
	if err != nil {
		t.Fatalf("new server PeerConnection: %v", err)
	}
	t.Cleanup(func() { _ = serverPeer.Close() })
	subscriberPeer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("new subscriber PeerConnection: %v", err)
	}
	t.Cleanup(func() { _ = subscriberPeer.Close() })

	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}
	oldTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "camera", "publisher-stream")
	if err != nil {
		t.Fatalf("new original track: %v", err)
	}
	newTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "camera", replacementStreamID)
	if err != nil {
		t.Fatalf("new republished track: %v", err)
	}
	if oldTrack == newTrack || oldTrack.ID() != newTrack.ID() {
		t.Fatal("test setup requires distinct TrackLocal pointers with the same ID")
	}

	received := make(chan uint16, 16)
	subscriberPeer.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		go func() {
			for {
				packet, _, readErr := track.ReadRTP()
				if readErr != nil {
					return
				}
				select {
				case received <- packet.SequenceNumber:
				default:
				}
			}
		}()
	})

	signalCount := 0
	var signalErr error
	signal := func(gatherComplete <-chan struct{}) error {
		signalCount++
		select {
		case <-gatherComplete:
		case <-time.After(5 * time.Second):
			return fmt.Errorf("server ICE gathering timed out")
		}
		offer := serverPeer.LocalDescription()
		if offer == nil {
			return fmt.Errorf("server local offer is nil")
		}
		if err := subscriberPeer.SetRemoteDescription(*offer); err != nil {
			return fmt.Errorf("subscriber set remote offer: %w", err)
		}
		answer, err := subscriberPeer.CreateAnswer(nil)
		if err != nil {
			return fmt.Errorf("subscriber create answer: %w", err)
		}
		subscriberGatherComplete := webrtc.GatheringCompletePromise(subscriberPeer)
		if err := subscriberPeer.SetLocalDescription(answer); err != nil {
			return fmt.Errorf("subscriber set local answer: %w", err)
		}
		select {
		case <-subscriberGatherComplete:
		case <-time.After(5 * time.Second):
			return fmt.Errorf("subscriber ICE gathering timed out")
		}
		if err := serverPeer.SetRemoteDescription(*subscriberPeer.LocalDescription()); err != nil {
			return fmt.Errorf("server set remote answer: %w", err)
		}

		return nil
	}

	listLock.Lock()
	peerConnections = []peerConnectionState{{
		peerConnection:  serverPeer,
		participantName: "Subscriber",
		sessionID:       "subscriber-1",
		signal: func(gatherComplete <-chan struct{}) error {
			signalErr = signal(gatherComplete)
			return signalErr
		},
	}}
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{oldTrack.ID(): oldTrack}
	trackParticipants = map[string]string{oldTrack.ID(): "Publisher"}
	trackParticipantSessions = map[string]string{oldTrack.ID(): "publisher-1"}
	trackRooms = map[string]string{}
	trackLayerGroups = map[string]string{}
	subscriberLayerTiers = map[string]string{}
	listLock.Unlock()

	signalPeerConnectionsWithRestart()
	if signalErr != nil {
		t.Fatalf("initial signal: %v", signalErr)
	}
	if signalCount != 1 {
		t.Fatalf("initial signal count=%d, want 1", signalCount)
	}
	waitForRepublishPacket(t, oldTrack, received, 7, 5*time.Second)

	listLock.Lock()
	trackLocals[oldTrack.ID()] = newTrack
	listLock.Unlock()
	signalPeerConnectionsWithRestart()

	if signalErr != nil {
		t.Fatalf("republish reconciliation signal: %v", signalErr)
	}
	if signalCount != wantSignalCount {
		t.Fatalf("same-ID replacement signaled %d total times, want %d", signalCount, wantSignalCount)
	}
	boundSenders := make([]webrtc.TrackLocal, 0, 1)
	for _, sender := range serverPeer.GetSenders() {
		if sender.Track() != nil {
			boundSenders = append(boundSenders, sender.Track())
		}
	}
	if len(boundSenders) != 1 {
		t.Fatalf("server bound senders=%d, want 1", len(boundSenders))
	}
	if got := boundSenders[0]; got != newTrack {
		t.Fatalf("sender remained bound to stale TrackLocal %p; want replacement %p", got, newTrack)
	}

	waitForRepublishPacket(t, newTrack, received, 42, 5*time.Second)
}

func waitForRepublishPacket(t *testing.T, track *webrtc.TrackLocalStaticRTP, received <-chan uint16, sequence uint16, timeout time.Duration) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()

	for {
		select {
		case got := <-received:
			if got == sequence {
				return
			}
		case <-ticker.C:
			if err := track.WriteRTP(&rtp.Packet{Header: rtp.Header{
				Version:        2,
				PayloadType:    96,
				SequenceNumber: sequence,
				Timestamp:      uint32(sequence) * 3000,
				SSRC:           1234,
			}, Payload: []byte{0x10, 0x00}}); err != nil {
				t.Fatalf("write RTP sequence=%d: %v", sequence, err)
			}
		case <-deadline.C:
			t.Fatalf("subscriber did not receive RTP sequence=%d from TrackLocal %p", sequence, track)
		}
	}
}

func TestSenderTrackReplacementCompatibilityRequiresSameEnvelope(t *testing.T) {
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}
	current, err := webrtc.NewTrackLocalStaticRTP(codec, "camera", "stream")
	if err != nil {
		t.Fatalf("new current track: %v", err)
	}
	compatible, _ := webrtc.NewTrackLocalStaticRTP(codec, "camera", "stream")
	differentStream, _ := webrtc.NewTrackLocalStaticRTP(codec, "camera", "other-stream")
	differentCodec, _ := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000}, "camera", "stream")

	if !senderTrackReplacementCompatible(current, compatible) {
		t.Fatal("same-ID same-codec TrackLocal replacement was rejected")
	}
	if senderTrackReplacementCompatible(current, differentStream) {
		t.Fatal("replacement with a different stream envelope was accepted")
	}
	if senderTrackReplacementCompatible(current, differentCodec) {
		t.Fatal("replacement with a different codec envelope was accepted")
	}
}
