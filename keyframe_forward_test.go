package main

import (
	"testing"
	"time"

	"github.com/pion/rtcp"
)

// TestKeyframeRequestThrottleCoalescesPerSource proves the core guarantee of
// subscriber keyframe forwarding: a burst of PLIs for one source collapses to
// a single upstream request per interval, and the window re-opens afterwards.
func TestKeyframeRequestThrottleCoalescesPerSource(t *testing.T) {
	throttle := newKeyframeRequestThrottle(time.Second)
	base := time.Unix(1000, 0)

	if !throttle.allow("stream:cam:1", base) {
		t.Fatal("first request for a source must be allowed")
	}

	// N subscribers asking at once (or one spamming) inside the window: all denied.
	allowed := 0
	for i := 0; i < 5; i++ {
		if throttle.allow("stream:cam:1", base.Add(time.Duration(i+1)*100*time.Millisecond)) {
			allowed++
		}
	}
	if allowed != 0 {
		t.Fatalf("requests inside the throttle window were allowed %d times, want 0", allowed)
	}

	if throttle.allow("stream:cam:1", base.Add(999*time.Millisecond)) {
		t.Fatal("request just inside the window must be denied")
	}
	if !throttle.allow("stream:cam:1", base.Add(time.Second)) {
		t.Fatal("request at the interval boundary must be allowed again")
	}
	if throttle.allow("stream:cam:1", base.Add(time.Second+time.Millisecond)) {
		t.Fatal("the boundary request must start a fresh window")
	}
}

// TestKeyframeRequestThrottleSourcesAreIndependent ensures throttling one
// source never starves another publisher's keyframe requests.
func TestKeyframeRequestThrottleSourcesAreIndependent(t *testing.T) {
	throttle := newKeyframeRequestThrottle(time.Second)
	base := time.Unix(1000, 0)

	if !throttle.allow("stream:cam:1", base) {
		t.Fatal("first request for cam must be allowed")
	}
	if !throttle.allow("stream:screen:2", base) {
		t.Fatal("a different source must not be throttled by cam's request")
	}
	if throttle.allow("stream:cam:1", base.Add(10*time.Millisecond)) {
		t.Fatal("cam must still be inside its own window")
	}
}

// TestKeyframeRequestThrottleForgetReleasesState verifies removed tracks drop
// their throttle entry (no unbounded growth, and a re-published source with
// the same forwarded ID starts fresh).
func TestKeyframeRequestThrottleForgetReleasesState(t *testing.T) {
	throttle := newKeyframeRequestThrottle(time.Second)
	base := time.Unix(1000, 0)

	if !throttle.allow("stream:cam:1", base) {
		t.Fatal("first request must be allowed")
	}
	throttle.forget("stream:cam:1")
	if !throttle.allow("stream:cam:1", base.Add(time.Millisecond)) {
		t.Fatal("after forget the source must be allowed immediately")
	}
	if len(throttle.last) != 1 {
		t.Fatalf("throttle retained %d entries, want 1", len(throttle.last))
	}
}

// TestForwardedTrackSSRC covers the parse of the SSRC segment that
// forwardedTrackLocalID embeds as the final ":"-separated field.
func TestForwardedTrackSSRC(t *testing.T) {
	cases := []struct {
		id   string
		ssrc uint32
		ok   bool
	}{
		{"stream:cam:12345", 12345, true},
		{"stream:cam:0", 0, true},
		{"stream:cam:4294967295", 4294967295, true},
		{"stream:with:extra:colons:77", 77, true},
		{"stream:cam:4294967296", 0, false}, // overflows uint32
		{"stream:cam:notanumber", 0, false},
		{"nocolons", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		ssrc, ok := forwardedTrackSSRC(tc.id)
		if ok != tc.ok || ssrc != tc.ssrc {
			t.Fatalf("forwardedTrackSSRC(%q) = (%d, %t), want (%d, %t)", tc.id, ssrc, ok, tc.ssrc, tc.ok)
		}
	}
}

// TestPublisherKeyframeTargetMapsForwardedTrackToSource proves the
// forwarded-track -> publisher mapping: the trackParticipantSessions
// bookkeeping resolves the owning session, that session's PeerConnection is
// found in the fan-out pool, and the SSRC embedded in the forwarded ID rides
// along as the PLI MediaSSRC.
func TestPublisherKeyframeTargetMapsForwardedTrackToSource(t *testing.T) {
	publisherPC, err := newPeerConnection()
	if err != nil {
		t.Fatalf("create publisher pc: %v", err)
	}
	defer publisherPC.Close() //nolint:errcheck
	otherPC, err := newPeerConnection()
	if err != nil {
		t.Fatalf("create other pc: %v", err)
	}
	defer otherPC.Close() //nolint:errcheck

	snapshotPeerState(t)
	listLock.Lock()
	trackParticipantSessions = map[string]string{"stream:cam:12345": "aj-1"}
	peerConnections = []peerConnectionState{
		{peerConnection: otherPC, participantName: "Tim", sessionID: "tim-1"},
		{peerConnection: publisherPC, participantName: "AJ", sessionID: "aj-1"},
	}
	listLock.Unlock()

	gotPC, gotSSRC, ok := publisherKeyframeTarget("stream:cam:12345")
	if !ok {
		t.Fatal("expected a keyframe target for a mapped forwarded track")
	}
	if gotPC != publisherPC {
		t.Fatal("keyframe target resolved to the wrong publisher PeerConnection")
	}
	if gotSSRC != 12345 {
		t.Fatalf("keyframe target ssrc=%d, want 12345", gotSSRC)
	}
}

// TestPublisherKeyframeTargetMissingMappings ensures the lookup fails closed:
// unknown forwarded IDs, sessions with no pooled connection, nil connections,
// and unparseable IDs all yield no target (so no PLI is sent anywhere).
func TestPublisherKeyframeTargetMissingMappings(t *testing.T) {
	livePC, err := newPeerConnection()
	if err != nil {
		t.Fatalf("create pc: %v", err)
	}
	defer livePC.Close() //nolint:errcheck

	snapshotPeerState(t)
	listLock.Lock()
	trackParticipantSessions = map[string]string{
		"stream:cam:1": "gone-1", // session no longer pooled
		"stream:cam:2": "nil-1",  // pooled entry with nil pc
		"stream:cam:x": "live-1", // unparseable ssrc
	}
	peerConnections = []peerConnectionState{
		{peerConnection: nil, participantName: "Nil", sessionID: "nil-1"},
		{peerConnection: livePC, participantName: "Live", sessionID: "live-1"},
	}
	listLock.Unlock()

	if _, _, ok := publisherKeyframeTarget("stream:unknown:9"); ok {
		t.Fatal("unknown forwarded track must have no keyframe target")
	}
	if _, _, ok := publisherKeyframeTarget("stream:cam:1"); ok {
		t.Fatal("a departed publisher session must have no keyframe target")
	}
	if _, _, ok := publisherKeyframeTarget("stream:cam:2"); ok {
		t.Fatal("a nil pooled PeerConnection must not be returned")
	}
	if _, _, ok := publisherKeyframeTarget("stream:cam:x"); ok {
		t.Fatal("an unparseable forwarded ID must have no keyframe target")
	}
}

// TestRTCPHasKeyframeRequest classifies the packet types that must trigger a
// forwarded keyframe request (PLI, FIR) versus routine RTCP that must not.
func TestRTCPHasKeyframeRequest(t *testing.T) {
	if rtcpHasKeyframeRequest(nil) {
		t.Fatal("empty batch must not read as a keyframe request")
	}
	if rtcpHasKeyframeRequest([]rtcp.Packet{&rtcp.ReceiverReport{}, &rtcp.SourceDescription{}}) {
		t.Fatal("receiver reports must not read as a keyframe request")
	}
	if !rtcpHasKeyframeRequest([]rtcp.Packet{&rtcp.ReceiverReport{}, &rtcp.PictureLossIndication{MediaSSRC: 7}}) {
		t.Fatal("a PLI in a compound batch must read as a keyframe request")
	}
	if !rtcpHasKeyframeRequest([]rtcp.Packet{&rtcp.FullIntraRequest{MediaSSRC: 7}}) {
		t.Fatal("a FIR must read as a keyframe request")
	}
}

// TestServerICEServersFromEnv locks the server-side TURN opt-in rules: only
// offered when TURN URLs and credentials are both resolvable, and the
// explicit disable flag wins.
func TestServerICEServersFromEnv(t *testing.T) {
	t.Setenv("MEETING_TURN_URLS", "")
	t.Setenv("MEETING_TURN_USERNAME", "")
	t.Setenv("MEETING_TURN_CREDENTIAL", "")
	t.Setenv("MEETING_TURN_SECRET", "")
	t.Setenv("MEETING_DISABLE_SERVER_TURN", "")

	if servers := serverICEServersFromEnv(); servers != nil {
		t.Fatalf("no TURN configured: servers=%v, want nil", servers)
	}

	t.Setenv("MEETING_TURN_URLS", "turn:turn.example.com:3478?transport=udp turn:turn.example.com:3478?transport=tcp")
	if servers := serverICEServersFromEnv(); servers != nil {
		t.Fatalf("TURN URLs without credentials must be skipped (pion rejects them): servers=%v", servers)
	}

	t.Setenv("MEETING_TURN_SECRET", "shared-secret")
	servers := serverICEServersFromEnv()
	if len(servers) != 1 {
		t.Fatalf("got %d ICE servers, want 1", len(servers))
	}
	if len(servers[0].URLs) != 2 {
		t.Fatalf("got URLs %v, want both transports", servers[0].URLs)
	}
	if servers[0].Username == "" || servers[0].Credential == "" {
		t.Fatal("minted ephemeral TURN credentials must be attached")
	}

	t.Setenv("MEETING_DISABLE_SERVER_TURN", "1")
	if servers := serverICEServersFromEnv(); servers != nil {
		t.Fatalf("disable flag must win: servers=%v, want nil", servers)
	}
}

// TestNewRoomPeerConnectionFallsBackOnBadTurnURL ensures a malformed
// MEETING_TURN_URLS entry (which fails pion's constructor-time validation)
// cannot take the room down: the room PeerConnection is still created,
// host-only, exactly as before the server-side TURN change.
func TestNewRoomPeerConnectionFallsBackOnBadTurnURL(t *testing.T) {
	t.Setenv("MEETING_TURN_URLS", "not-a-turn-url")
	t.Setenv("MEETING_TURN_SECRET", "shared-secret")
	t.Setenv("MEETING_DISABLE_SERVER_TURN", "")
	t.Setenv("MEETING_TURN_USERNAME", "")
	t.Setenv("MEETING_TURN_CREDENTIAL", "")

	peerConnection, err := newRoomPeerConnection()
	if err != nil {
		t.Fatalf("room peer connection must fall back to host-only ICE, got error: %v", err)
	}
	defer peerConnection.Close() //nolint:errcheck
	if got := len(peerConnection.GetConfiguration().ICEServers); got != 0 {
		t.Fatalf("fallback connection carries %d ICE servers, want 0", got)
	}
}

// TestNewRoomPeerConnectionUsesConfiguredTurn verifies the room constructor
// attaches the configured relay so the server gathers relay candidates.
func TestNewRoomPeerConnectionUsesConfiguredTurn(t *testing.T) {
	t.Setenv("MEETING_TURN_URLS", "turn:turn.example.com:3478?transport=udp")
	t.Setenv("MEETING_TURN_SECRET", "shared-secret")
	t.Setenv("MEETING_DISABLE_SERVER_TURN", "")
	t.Setenv("MEETING_TURN_USERNAME", "")
	t.Setenv("MEETING_TURN_CREDENTIAL", "")

	peerConnection, err := newRoomPeerConnection()
	if err != nil {
		t.Fatalf("create room peer connection with TURN: %v", err)
	}
	defer peerConnection.Close() //nolint:errcheck
	iceServers := peerConnection.GetConfiguration().ICEServers
	if len(iceServers) != 1 || len(iceServers[0].URLs) != 1 {
		t.Fatalf("configuration ICE servers = %+v, want the configured TURN relay", iceServers)
	}
	if iceServers[0].Username == "" {
		t.Fatal("TURN server must carry minted credentials")
	}
}
