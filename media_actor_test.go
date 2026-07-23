package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func TestRoomMediaActorCoalescesBurstsAndCloseFencesCallbacks(t *testing.T) {
	var calls atomic.Int64
	var observed atomic.Uint32
	handled := make(chan struct{}, 1)
	actor := newRoomMediaActor("room-actor", 1, func(_ string, commands roomMediaCommand) {
		calls.Add(1)
		for {
			current := observed.Load()
			if observed.CompareAndSwap(current, current|uint32(commands)) {
				break
			}
		}
		select {
		case handled <- struct{}{}:
		default:
		}
	})

	for i := 0; i < 500; i++ {
		if !actor.enqueue(roomMediaCommandSignal) {
			t.Fatal("live actor rejected signaling work")
		}
	}
	actor.enqueue(roomMediaCommandAdmit | roomMediaCommandTrack | roomMediaCommandRestart)

	select {
	case <-handled:
	case <-time.After(2 * time.Second):
		t.Fatal("actor did not drain the coalesced command burst")
	}
	if got := roomMediaCommand(observed.Load()); got&(roomMediaCommandSignal|roomMediaCommandAdmit|roomMediaCommandTrack|roomMediaCommandRestart) != (roomMediaCommandSignal | roomMediaCommandAdmit | roomMediaCommandTrack | roomMediaCommandRestart) {
		t.Fatalf("observed commands=%06b; required lifecycle work was lost", got)
	}
	if got := calls.Load(); got >= 500 {
		t.Fatalf("signal burst was not coalesced: handler calls=%d", got)
	}

	if !actor.enqueue(roomMediaCommandClose) {
		t.Fatal("live actor rejected close")
	}
	select {
	case <-actor.done:
	case <-time.After(time.Second):
		t.Fatal("actor did not close")
	}
	if actor.enqueue(roomMediaCommandSignal) {
		t.Fatal("closed actor accepted a stale callback")
	}
}

func TestRoomMediaActorCloseDrainsRequiredStateBehindBlockedHandler(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var enterOnce sync.Once
	var mu sync.Mutex
	handled := []roomMediaCommand{}
	actor := newRoomMediaActor("room-drain", 3, func(_ string, commands roomMediaCommand) {
		mu.Lock()
		handled = append(handled, commands)
		mu.Unlock()
		if commands&roomMediaCommandSignal != 0 {
			enterOnce.Do(func() { close(entered) })
			<-release
		}
	})
	if !actor.enqueue(roomMediaCommandSignal) {
		t.Fatal("actor rejected initial signal")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not enter blocking signal")
	}
	required := roomMediaCommandAdmit | roomMediaCommandLeave | roomMediaCommandTrack
	if !actor.enqueue(required) {
		t.Fatal("actor rejected required pre-close work")
	}
	if !actor.enqueue(roomMediaCommandClose) {
		t.Fatal("actor rejected close")
	}
	if actor.enqueue(roomMediaCommandRestart) {
		t.Fatal("actor accepted work after close linearized")
	}
	close(release)
	select {
	case <-actor.done:
	case <-time.After(time.Second):
		t.Fatal("actor did not drain and close")
	}
	mu.Lock()
	defer mu.Unlock()
	var observed roomMediaCommand
	for _, commands := range handled {
		observed |= commands
	}
	if observed&required != required {
		t.Fatalf("terminal close dropped required state: handled=%v", handled)
	}
}

func TestRoomMediaActorConcurrentEnqueueCannotSucceedBehindClose(t *testing.T) {
	actor := newRoomMediaActor("room-linear-close", 4, nil)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			actor.enqueue(roomMediaCommandSignal)
		}()
	}
	close(start)
	if !actor.enqueue(roomMediaCommandClose) {
		t.Fatal("actor rejected close")
	}
	wg.Wait()
	for i := 0; i < 64; i++ {
		if actor.enqueue(roomMediaCommandSignal) {
			t.Fatalf("enqueue %d succeeded after close acceptance", i)
		}
	}
	select {
	case <-actor.done:
	case <-time.After(time.Second):
		t.Fatal("actor did not close")
	}
}

func TestRoomMediaActorGenerationFencesOldSitting(t *testing.T) {
	resetRoomMediaActorsForTest(t)
	oldActor := actorForRoomGeneration("room-generation", 41)
	newActor := actorForRoomGeneration("room-generation", 42)
	if oldActor == newActor {
		t.Fatal("new sitting reused the prior generation's actor")
	}
	if requestRoomMediaCommandForGeneration("room-generation", 41, roomMediaCommandTrack) {
		t.Fatal("old sitting callback entered the successor actor")
	}
	if !requestRoomMediaCommandForGeneration("room-generation", 42, roomMediaCommandTrack) {
		t.Fatal("current sitting callback was rejected")
	}

	// A delayed teardown holding the old owner pointer must not delete or
	// close the already-installed successor.
	closeRoomMediaActorOwned("room-generation", oldActor)
	if roomMediaActorForGeneration("room-generation", 42) != newActor {
		t.Fatal("old teardown detached the successor sitting actor")
	}
	closeRoomMediaActorOwned("room-generation", newActor)
}

func TestForwardedTrackRegistryFencesExactMediaGeneration(t *testing.T) {
	snapshotPeerState(t)
	roomID := "room-track-generation"
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}
	oldTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "old-audio", "old-stream")
	if err != nil {
		t.Fatal(err)
	}
	currentTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "current-audio", "current-stream")
	if err != nil {
		t.Fatal(err)
	}
	listLock.Lock()
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{oldTrack.ID(): oldTrack, currentTrack.ID(): currentTrack}
	trackParticipants = map[string]string{oldTrack.ID(): "Scout", currentTrack.ID(): "Scout"}
	trackParticipantSessions = map[string]string{oldTrack.ID(): "old", currentTrack.ID(): "current"}
	trackRooms = map[string]string{oldTrack.ID(): roomID, currentTrack.ID(): roomID}
	trackSourceIDs = map[string]string{oldTrack.ID(): "old-source", currentTrack.ID(): "current-source"}
	trackMediaOwners = map[string]trackMediaOwner{
		oldTrack.ID(): {track: oldTrack, generation: 8}, currentTrack.ID(): {track: currentTrack, generation: 9},
	}
	peer := peerConnectionState{participantName: "AJ", roomID: roomID, mediaGeneration: 9}
	if peer.acceptsTrack(oldTrack) {
		listLock.Unlock()
		t.Fatal("successor peer accepted prior-sitting forwarded track")
	}
	if !peer.acceptsTrack(currentTrack) {
		listLock.Unlock()
		t.Fatal("successor peer rejected current-sitting forwarded track")
	}
	snapshots := participantTrackSnapshotsLockedForGeneration(roomID, "AJ", 9, true)
	listLock.Unlock()
	if len(snapshots) != 1 || snapshots[0].TrackID != currentTrack.ID() {
		t.Fatalf("generation-scoped snapshots=%+v, want current track only", snapshots)
	}
}

// TestRoomActorBlockedOfferDoesNotHeadOfLineAnotherRoom drives the real Pion
// offer path. Room A's offer delivery is held until the test releases it;
// room B must still create and deliver its own offer. This is the regression
// for the former listLock-held signal callback, which serialized every room.
func TestRoomActorBlockedOfferDoesNotHeadOfLineAnotherRoom(t *testing.T) {
	snapshotPeerState(t)
	resetRoomMediaActorsForTest(t)

	newOfferPeer := func(t *testing.T) *webrtc.PeerConnection {
		t.Helper()
		peer, err := newRoomPeerConnection()
		if err != nil {
			t.Fatalf("create Pion peer: %v", err)
		}
		if _, err := peer.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
			_ = peer.Close()
			t.Fatalf("add audio transceiver: %v", err)
		}
		t.Cleanup(func() { _ = peer.Close() })
		return peer
	}

	roomAPeer := newOfferPeer(t)
	roomBPeer := newOfferPeer(t)
	roomAEntered := make(chan struct{})
	releaseRoomA := make(chan struct{})
	roomACompleted := make(chan struct{})
	roomBDelivered := make(chan struct{})
	var roomAOnce sync.Once
	var roomACompleteOnce sync.Once
	var roomBOnce sync.Once

	listLock.Lock()
	peerConnections = []peerConnectionState{
		{
			peerConnection:  roomAPeer,
			participantName: "AJ",
			sessionID:       "a-1",
			roomID:          "room-a",
			shouldSignal:    func(int) bool { return true },
			signal: func(<-chan struct{}) error {
				roomAOnce.Do(func() { close(roomAEntered) })
				<-releaseRoomA
				roomACompleteOnce.Do(func() { close(roomACompleted) })
				return nil
			},
		},
		{
			peerConnection:  roomBPeer,
			participantName: "Tim",
			sessionID:       "b-1",
			roomID:          "room-b",
			shouldSignal:    func(int) bool { return true },
			signal: func(<-chan struct{}) error {
				roomBOnce.Do(func() { close(roomBDelivered) })
				return nil
			},
		},
	}
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{}
	trackParticipants = map[string]string{}
	trackParticipantSessions = map[string]string{}
	trackRooms = map[string]string{}
	trackSourceIDs = map[string]string{}
	trackLayerRIDs = map[string]string{}
	trackLayerGroups = map[string]string{}
	subscriberLayerTiers = map[string]string{}
	listLock.Unlock()

	roomAActor := actorForRoom("room-a")
	roomBActor := actorForRoom("room-b")
	requestRoomMediaCommand("room-a", roomMediaCommandSignal)
	select {
	case <-roomAEntered:
	case <-time.After(2 * time.Second):
		close(releaseRoomA)
		t.Fatal("room A did not enter its blocking offer seam")
	}

	startedB := time.Now()
	requestRoomMediaCommand("room-b", roomMediaCommandSignal)
	select {
	case <-roomBDelivered:
		if elapsed := time.Since(startedB); elapsed >= 2*time.Second {
			t.Fatalf("room B offer took %s while room A was blocked; want <2s", elapsed)
		}
	case <-time.After(2 * time.Second):
		close(releaseRoomA)
		t.Fatal("room B offer was head-of-line blocked by room A")
	}
	close(releaseRoomA)
	select {
	case <-roomACompleted:
	case <-time.After(time.Second):
		t.Fatal("room A signal pass did not finish after release")
	}
	closeRoomMediaActor("room-a")
	closeRoomMediaActor("room-b")
	for _, actor := range []*roomMediaActor{roomAActor, roomBActor} {
		select {
		case <-actor.done:
		case <-time.After(time.Second):
			t.Fatalf("room actor %s did not close", actor.roomID)
		}
	}
}

func TestPionVideoControlPlaneDoesNotDependOnAIProvider(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	snapshotPeerState(t)

	peer, err := newRoomPeerConnection()
	if err != nil {
		t.Fatalf("Pion must remain available without an AI key: %v", err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	if _, err := peer.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatalf("video transceiver unavailable without AI provider: %v", err)
	}
	delivered := make(chan struct{})
	listLock.Lock()
	peerConnections = []peerConnectionState{{
		peerConnection:  peer,
		participantName: "AJ",
		sessionID:       "video-no-ai",
		roomID:          "room-no-ai",
		shouldSignal:    func(int) bool { return true },
		signal: func(<-chan struct{}) error {
			close(delivered)
			return nil
		},
	}}
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{}
	trackParticipants = map[string]string{}
	trackParticipantSessions = map[string]string{}
	trackRooms = map[string]string{}
	trackSourceIDs = map[string]string{}
	trackLayerRIDs = map[string]string{}
	trackLayerGroups = map[string]string{}
	subscriberLayerTiers = map[string]string{}
	listLock.Unlock()

	signalPeerConnectionsForRoomWithRestart("room-no-ai")
	select {
	case <-delivered:
	default:
		t.Fatal("Pion video offer was coupled to unavailable AI configuration")
	}
}
