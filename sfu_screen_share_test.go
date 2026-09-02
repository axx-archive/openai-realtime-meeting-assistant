package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

/* ---------- Wave 6 D4: screen share as a second publisher transceiver pair ---------- */

// sdpMediaSections splits an SDP into its m= sections of one kind, in order.
func sdpMediaSections(sdp string, kind string) []string {
	var sections []string
	for _, section := range strings.Split(sdp, "\nm=")[1:] {
		if strings.HasPrefix(section, kind+" ") {
			sections = append(sections, "m="+section)
		}
	}
	return sections
}

func TestRoomPublisherOfferCarriesCameraAndScreenRecvonlyPairs(t *testing.T) {
	peer, err := newRoomPeerConnection()
	if err != nil {
		t.Fatalf("newRoomPeerConnection: %v", err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	uplink, err := addRoomPublisherUplinkTransceiversWithSources(peer)
	if err != nil {
		t.Fatalf("addRoomPublisherUplinkTransceiversWithSources: %v", err)
	}
	transceivers := peer.GetTransceivers()
	wantKinds := []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio, webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio}
	wantSources := []string{trackSourceCamera, trackSourceCamera, trackSourceScreen, trackSourceScreen}
	if len(transceivers) != len(wantKinds) {
		t.Fatalf("publisher transceivers=%d, want %d (camera video, camera audio, screen video, screen audio)", len(transceivers), len(wantKinds))
	}
	for i, transceiver := range transceivers {
		if transceiver.Kind() != wantKinds[i] {
			t.Fatalf("transceiver[%d] kind=%s, want %s", i, transceiver.Kind(), wantKinds[i])
		}
		if transceiver.Direction() != webrtc.RTPTransceiverDirectionRecvonly {
			t.Fatalf("transceiver[%d] direction=%s, want recvonly", i, transceiver.Direction())
		}
		if got := uplink.sourceForReceiver(peer, transceiver.Receiver()); got != wantSources[i] {
			t.Fatalf("transceiver[%d] source=%q, want %q", i, got, wantSources[i])
		}
	}
	if got := uplink.sourceForReceiver(peer, nil); got != trackSourceCamera {
		t.Fatalf("unknown receiver source=%q, want camera fallback", got)
	}

	offer, err := peer.CreateOffer(nil)
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	videoSections := sdpMediaSections(offer.SDP, "video")
	audioSections := sdpMediaSections(offer.SDP, "audio")
	if len(videoSections) != 2 || len(audioSections) != 2 {
		t.Fatalf("offer sections video=%d audio=%d, want 2 and 2:\n%s", len(videoSections), len(audioSections), offer.SDP)
	}
	for i, section := range append(videoSections, audioSections...) {
		if !strings.Contains(section, "a=recvonly") {
			t.Fatalf("section %d is not recvonly:\n%s", i, section)
		}
	}
	for i, section := range videoSections {
		if !strings.Contains(section, "H264/90000") || strings.Contains(strings.ToUpper(section), "VP8/90000") {
			t.Fatalf("video section %d is not H.264-only (the share must match the camera envelope):\n%s", i, section)
		}
	}
	// m-line order is the client contract: video, audio, video, audio.
	order := []string{}
	for _, section := range strings.Split(offer.SDP, "\nm=")[1:] {
		order = append(order, strings.Fields(section)[0])
	}
	if strings.Join(order, ",") != "video,audio,video,audio" {
		t.Fatalf("m-line order=%v, want video,audio,video,audio", order)
	}
}

// The live signaling path: a publisher's server offer over the websocket
// carries both pairs, and a native answerer negotiates all four m-lines.
func TestWebsocketPublisherOfferCarriesShareSectionsAndNegotiates(t *testing.T) {
	conn := newIsolatedNativeWebsocket(t, "tom@shareability.com")
	writeNativeWebsocketEvent(t, conn, "participant", map[string]any{
		"client": map[string]string{"platform": "ios", "version": "test"},
	})
	waitForKanbanEvent(t, conn, "access_granted", 5*time.Second)
	writeNativeWebsocketEvent(t, conn, "media_ready", map[string]any{
		"client": map[string]string{"platform": "ios"},
		"media":  map[string]bool{"audio": true, "video": true},
	})
	offer := waitForServerOffer(t, conn, 5*time.Second)
	if got := len(sdpMediaSections(offer.SDP, "video")); got != 2 {
		t.Fatalf("server offer video sections=%d, want 2:\n%s", got, offer.SDP)
	}
	if got := len(sdpMediaSections(offer.SDP, "audio")); got != 2 {
		t.Fatalf("server offer audio sections=%d, want 2:\n%s", got, offer.SDP)
	}
	nativePeer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create native peer: %v", err)
	}
	t.Cleanup(func() { _ = nativePeer.Close() })
	answerServerOffer(t, conn, nativePeer, offer)
	if got := len(nativePeer.GetTransceivers()); got != 4 {
		t.Fatalf("native answerer transceivers=%d, want 4", got)
	}
}

func seedForwardedTrack(t *testing.T, id, kind, participant, session, roomID, source string) *webrtc.TrackLocalStaticRTP {
	t.Helper()
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}
	if kind == "audio" {
		codec = webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}
	}
	track, err := webrtc.NewTrackLocalStaticRTP(codec, id, participant+"-stream-"+source)
	if err != nil {
		t.Fatalf("create %s track: %v", id, err)
	}
	trackLocals[track.ID()] = track
	trackParticipants[track.ID()] = participant
	trackParticipantSessions[track.ID()] = session
	trackRooms[track.ID()] = roomID
	trackSources[track.ID()] = source
	trackSourceIDs[track.ID()] = id + "-src"
	return track
}

func resetForwardedTrackRegistryForTest(t *testing.T) {
	t.Helper()
	snapshotPeerState(t)
	listLock.Lock()
	prevSources, prevPaused, prevOwners := trackSources, trackPaused, trackMediaOwners
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{}
	trackParticipants = map[string]string{}
	trackParticipantSessions = map[string]string{}
	trackRooms = map[string]string{}
	trackSourceIDs = map[string]string{}
	trackLayerRIDs = map[string]string{}
	trackLayerGroups = map[string]string{}
	trackSources = map[string]string{}
	trackPaused = map[string]bool{}
	trackMediaOwners = map[string]trackMediaOwner{}
	subscriberLayerTiers = map[string]string{}
	listLock.Unlock()
	t.Cleanup(func() {
		listLock.Lock()
		trackSources, trackPaused, trackMediaOwners = prevSources, prevPaused, prevOwners
		listLock.Unlock()
	})
}

func TestTrackRegistrySourceTagsAndSharePauseResume(t *testing.T) {
	resetForwardedTrackRegistryForTest(t)
	listLock.Lock()
	camera := seedForwardedTrack(t, "tom-camera", "video", "Tom", "tom-1", "room-share", trackSourceCamera)
	shareVideo := seedForwardedTrack(t, "tom-share-video", "video", "Tom", "tom-1", "room-share", trackSourceScreen)
	shareAudio := seedForwardedTrack(t, "tom-share-audio", "audio", "Tom", "tom-1", "room-share", trackSourceScreen)
	subscriber := peerConnectionState{participantName: "Tim", sessionID: "tim-1", roomID: "room-share"}
	listLock.Unlock()

	accepts := func(track *webrtc.TrackLocalStaticRTP) bool {
		listLock.RLock()
		defer listLock.RUnlock()
		return subscriber.acceptsTrack(track)
	}
	snapshots := func() []participantTrackSnapshot {
		listLock.RLock()
		defer listLock.RUnlock()
		return participantTrackSnapshotsLocked("room-share", "")
	}
	for _, track := range []*webrtc.TrackLocalStaticRTP{camera, shareVideo, shareAudio} {
		if !accepts(track) {
			t.Fatalf("subscriber refused live track %s", track.ID())
		}
	}
	bySource := map[string]int{}
	for _, snapshot := range snapshots() {
		bySource[snapshot.Source]++
	}
	if bySource[trackSourceCamera] != 1 || bySource[trackSourceScreen] != 2 {
		t.Fatalf("snapshot sources=%v, want camera:1 screen:2", bySource)
	}

	// Stop: both share tracks are withdrawn; the camera is untouched.
	changed := setParticipantSourceTracksPaused("room-share", "Tom", "tom-1", trackSourceScreen, true)
	if len(changed) != 2 || changed[0].Kind != "audio" || changed[1].Kind != "video" || changed[0].Source != trackSourceScreen || changed[1].Source != trackSourceScreen {
		t.Fatalf("pause changed=%+v, want the share audio then video", changed)
	}
	if accepts(shareVideo) || accepts(shareAudio) {
		t.Fatal("paused share tracks are still offered to the subscriber")
	}
	if !accepts(camera) {
		t.Fatal("pausing the share withdrew the camera")
	}
	if got := snapshots(); len(got) != 1 || got[0].TrackID != camera.ID() || got[0].Source != trackSourceCamera {
		t.Fatalf("snapshots while paused=%+v, want only the camera", got)
	}
	if again := setParticipantSourceTracksPaused("room-share", "Tom", "tom-1", trackSourceScreen, true); again != nil {
		t.Fatalf("second pause changed=%+v, want nothing (idempotent)", again)
	}
	// Another session of the same name is never touched.
	if other := setParticipantSourceTracksPaused("room-share", "Tom", "tom-2", trackSourceScreen, false); other != nil {
		t.Fatalf("resume for a different session changed=%+v", other)
	}
	// Start: the same forwarded tracks come back and are re-announced.
	resumed := setParticipantSourceTracksPaused("room-share", "Tom", "tom-1", trackSourceScreen, false)
	if len(resumed) != 2 || resumed[1].TrackID != shareVideo.ID() || resumed[1].RoomID != "room-share" {
		t.Fatalf("resume changed=%+v", resumed)
	}
	if !accepts(shareVideo) || !accepts(shareAudio) {
		t.Fatal("resumed share tracks are not offered again")
	}
	if got := snapshots(); len(got) != 3 {
		t.Fatalf("snapshots after resume=%d, want 3", len(got))
	}
	// The forwarding registry payload shape carries the tag.
	raw, _ := json.Marshal(resumed[1])
	if !strings.Contains(string(raw), `"source":"screen"`) || !strings.Contains(string(raw), `"trackId":"tom-share-video"`) {
		t.Fatalf("participant_track snapshot json=%s", raw)
	}

	// Session teardown clears the tag and pause bookkeeping with the tracks.
	setParticipantSourceTracksPaused("room-share", "Tom", "tom-1", trackSourceScreen, true)
	listLock.Lock()
	removed := removeParticipantTracksLocked("Tom", "tom-1")
	sources, paused := len(trackSources), len(trackPaused)
	listLock.Unlock()
	if !removed || sources != 0 || paused != 0 {
		t.Fatalf("teardown removed=%t sources=%d paused=%d, want all cleared", removed, sources, paused)
	}
}

// Pion-level proof that a stopped share leaves no sender behind on the
// subscriber's server peer, and a restarted share brings it back.
func TestSignalPeerConnectionsWithdrawsPausedShareSender(t *testing.T) {
	resetForwardedTrackRegistryForTest(t)

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

	var signalErr error
	signal := func(gatherComplete <-chan struct{}) error {
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
		gather := webrtc.GatheringCompletePromise(subscriberPeer)
		if err := subscriberPeer.SetLocalDescription(answer); err != nil {
			return fmt.Errorf("subscriber set local answer: %w", err)
		}
		select {
		case <-gather:
		case <-time.After(5 * time.Second):
			return fmt.Errorf("subscriber ICE gathering timed out")
		}
		return serverPeer.SetRemoteDescription(*subscriberPeer.LocalDescription())
	}

	// Roster names: the registry matches publishers through the canonical
	// roster (sameParticipantName), so synthetic names would never match.
	listLock.Lock()
	seedForwardedTrack(t, "publisher-camera", "video", "Tom", "publisher-1", "", trackSourceCamera)
	share := seedForwardedTrack(t, "publisher-share", "video", "Tom", "publisher-1", "", trackSourceScreen)
	peerConnections = []peerConnectionState{{
		peerConnection:  serverPeer,
		participantName: "Tim",
		sessionID:       "subscriber-1",
		signal: func(gatherComplete <-chan struct{}) error {
			signalErr = signal(gatherComplete)
			return signalErr
		},
	}}
	listLock.Unlock()

	boundSenders := func() []string {
		ids := []string{}
		for _, sender := range serverPeer.GetSenders() {
			if track := sender.Track(); track != nil {
				ids = append(ids, track.ID())
			}
		}
		return ids
	}
	signalPeerConnectionsWithRestart()
	if signalErr != nil {
		t.Fatalf("initial signal: %v", signalErr)
	}
	if got := boundSenders(); len(got) != 2 {
		t.Fatalf("bound senders after publish=%v, want camera and share", got)
	}

	if changed := setParticipantSourceTracksPaused("", "Tom", "publisher-1", trackSourceScreen, true); len(changed) != 1 || changed[0].TrackID != share.ID() {
		t.Fatalf("pause changed=%+v", changed)
	}
	signalPeerConnectionsWithRestart()
	if signalErr != nil {
		t.Fatalf("signal after share stop: %v", signalErr)
	}
	if got := boundSenders(); len(got) != 1 || got[0] != "publisher-camera" {
		t.Fatalf("bound senders after share stop=%v, want only the camera (no zombie share sender)", got)
	}

	setParticipantSourceTracksPaused("", "Tom", "publisher-1", trackSourceScreen, false)
	signalPeerConnectionsWithRestart()
	if signalErr != nil {
		t.Fatalf("signal after share restart: %v", signalErr)
	}
	if got := boundSenders(); len(got) != 2 {
		t.Fatalf("bound senders after share restart=%v, want camera and share again", got)
	}
}

// Websocket contract: screen_share_stopped withdraws the publisher's share
// tracks; screen_share_started re-announces them as participant_track with
// source "screen" so receivers relabel before the senders return.
func TestWebsocketScreenShareStopWithdrawsAndStartReannouncesShareTracks(t *testing.T) {
	server, roomID := newInCallRoomServer(t)
	tomConn := dialInCallMember(t, server, "tom@shareability.com", roomID)
	admitInCallMember(t, tomConn, roomID)
	timConn := dialInCallMember(t, server, "tim@shareability.com", roomID)
	admitInCallMember(t, timConn, roomID)

	var tomSession string
	listLock.RLock()
	for _, state := range peerConnections {
		if sameParticipantName(state.participantName, "Tom") && normalizeRoomID(state.roomID) == roomID {
			tomSession = state.sessionID
		}
	}
	listLock.RUnlock()
	if tomSession == "" {
		t.Fatal("Tom's media session not found in the pool")
	}
	listLock.Lock()
	if trackSources == nil {
		trackSources = map[string]string{}
	}
	if trackPaused == nil {
		trackPaused = map[string]bool{}
	}
	share := seedForwardedTrack(t, "tom-ws-share", "video", "Tom", tomSession, roomID, trackSourceScreen)
	listLock.Unlock()
	t.Cleanup(func() {
		listLock.Lock()
		delete(trackLocals, share.ID())
		delete(trackParticipants, share.ID())
		delete(trackParticipantSessions, share.ID())
		delete(trackRooms, share.ID())
		delete(trackSources, share.ID())
		delete(trackSourceIDs, share.ID())
		delete(trackPaused, share.ID())
		listLock.Unlock()
	})
	isPaused := func() bool {
		listLock.RLock()
		defer listLock.RUnlock()
		return trackPaused[share.ID()]
	}

	writeNativeWebsocketEvent(t, tomConn, "screen_share_stopped", map[string]any{})
	waitForKanbanEvent(t, timConn, "screen_share_stopped", 5*time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for !isPaused() {
		if time.Now().After(deadline) {
			t.Fatal("screen_share_stopped did not withdraw the share track")
		}
		time.Sleep(10 * time.Millisecond)
	}

	writeNativeWebsocketEvent(t, tomConn, "screen_share_started", map[string]any{})
	var announced struct {
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		TrackID string `json:"trackId"`
		Source  string `json:"source"`
		RoomID  string `json:"roomId"`
	}
	for announced.TrackID != share.ID() {
		if err := json.Unmarshal(waitForKanbanEvent(t, timConn, "participant_track", 5*time.Second), &announced); err != nil {
			t.Fatalf("decode participant_track: %v", err)
		}
	}
	if announced.Name != "Tom" || announced.Kind != "video" || announced.Source != trackSourceScreen || announced.RoomID != roomID {
		t.Fatalf("re-announced share=%+v", announced)
	}
	if isPaused() {
		t.Fatal("screen_share_started left the share track paused")
	}
}

/* ---------- reviewer regressions: guest bucket, ICE restart stability, ungraceful rejoin ---------- */

// sdpMidsInOrder returns the a=mid value of every m= section, in order.
func sdpMidsInOrder(sdp string) []string {
	mids := []string{}
	for _, section := range strings.Split(sdp, "\nm=")[1:] {
		mid := ""
		for _, line := range strings.Split(section, "\n") {
			if strings.HasPrefix(line, "a=mid:") {
				mid = strings.TrimSpace(strings.TrimPrefix(line, "a=mid:"))
				break
			}
		}
		mids = append(mids, mid)
	}
	return mids
}

// countKanbanEventsUntilQuiet drains conn counting `event` frames until no
// frame arrives for quiet (or maxWindow elapses). A read timeout is the normal
// terminator, so the connection is spent afterward.
func countKanbanEventsUntilQuiet(t *testing.T, conn *websocket.Conn, event string, quiet, maxWindow time.Duration) int {
	t.Helper()
	count := 0
	overall := time.Now().Add(maxWindow)
	for {
		deadline := time.Now().Add(quiet)
		if deadline.After(overall) {
			deadline = overall
		}
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			return count
		}
		if message.Event != "kanban" {
			continue
		}
		var inner struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal([]byte(message.Data), &inner); err != nil {
			t.Fatalf("decode kanban envelope: %v", err)
		}
		if inner.Event == event {
			count++
		}
	}
}

func TestGuestMediaRateLimitGuardsScreenShareEvents(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	main := string(source)
	if want := `guest_screen_share_rate_limited event=screen_share_started`; !strings.Contains(main, want) {
		t.Fatalf("main.go is missing the guest share rate-limit wiring: %q", want)
	}
	// Teardown is never rate-limited: a dropped stop left a dead share
	// registered and un-paused (frozen last frame, roster still sharing).
	if strings.Contains(main, `guest_screen_share_rate_limited event=screen_share_stopped`) {
		t.Fatal("screen_share_stopped must not charge the guest media-state bucket")
	}
	if got := strings.Count(main, "allowGuestMediaStateEvent(connRoomID"); got != 3 {
		t.Fatalf("allowGuestMediaStateEvent wired %d times, want exactly 3 (media state, repair, share start)", got)
	}

	// Live: a guest storm past the burst yields a bounded number of
	// room-wide screen_share_started broadcasts, and the stop sent while the
	// bucket is still exhausted lands exactly once.
	server, roomID := newInCallRoomServer(t)
	timConn := dialInCallMember(t, server, "tim@shareability.com", roomID)
	admitInCallMember(t, timConn, roomID)
	guestToken, err := userSessionStore().createGuest(roomID, "Sam")
	if err != nil {
		t.Fatalf("create guest session: %v", err)
	}
	guestConn, _, err := dialGuestWebsocket(t, server, guestToken)
	if err != nil {
		t.Fatalf("guest dial: %v", err)
	}
	if err := guestConn.WriteJSON(map[string]string{"event": "participant", "data": `{}`}); err != nil {
		t.Fatalf("guest hello: %v", err)
	}
	guestName := joinInCallMedia(t, guestConn, roomID)
	guestSharing := func() bool {
		snapshot := kanbanApp.roomSnapshotForRoom(roomID)
		mediaStates, _ := snapshot["mediaStates"].(map[string]participantMediaState)
		return mediaStates[guestName].ScreenSharing
	}
	waitForGuestSharing := func(want bool, what string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for guestSharing() != want {
			if time.Now().After(deadline) {
				t.Fatalf("%s: guest sharing=%t on the roster, want %t", what, guestSharing(), want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	const storm = int(guestMediaStateBucketBurst) + 3
	for i := 0; i < storm; i++ {
		if err := guestConn.WriteJSON(map[string]string{"event": "screen_share_started", "data": `{}`}); err != nil {
			t.Fatalf("guest share start %d: %v", i, err)
		}
	}
	// The bucket is exhausted (three starts were dropped); the stop must
	// still apply — once, with its room-wide fan-out.
	if err := guestConn.WriteJSON(map[string]string{"event": "screen_share_stopped", "data": `{}`}); err != nil {
		t.Fatalf("guest share stop: %v", err)
	}
	// A second stop on an already-stopped share changes nothing and fans out
	// nothing (a stop storm is bounded by the no-change check, not a bucket).
	if err := guestConn.WriteJSON(map[string]string{"event": "screen_share_stopped", "data": `{}`}); err != nil {
		t.Fatalf("guest share stop again: %v", err)
	}
	counts := countKanbanEventsByNameUntilQuiet(t, timConn, []string{"screen_share_started", "screen_share_stopped"}, 1500*time.Millisecond, 8*time.Second)
	if counts["screen_share_started"] != int(guestMediaStateBucketBurst) {
		t.Fatalf("%d guest screen_share_started frames produced %d broadcasts, want exactly the burst (%d)", storm, counts["screen_share_started"], int(guestMediaStateBucketBurst))
	}
	if counts["screen_share_stopped"] != 1 {
		t.Fatalf("stop after the bucket was exhausted produced %d screen_share_stopped broadcasts, want exactly 1", counts["screen_share_stopped"])
	}
	waitForGuestSharing(false, "after the un-bucketed stop")

	// The participant_media_state that CLEARS a share is exempt too. Let one
	// token refill, land a start, exhaust the bucket again, then clear.
	time.Sleep(guestMediaStateBucketRefill + 200*time.Millisecond)
	if err := guestConn.WriteJSON(map[string]string{"event": "screen_share_started", "data": `{}`}); err != nil {
		t.Fatalf("guest share restart: %v", err)
	}
	waitForGuestSharing(true, "after the refilled start")
	for i := 0; i < int(guestMediaStateBucketBurst)+1; i++ {
		if err := guestConn.WriteJSON(map[string]string{"event": "screen_share_started", "data": `{}`}); err != nil {
			t.Fatalf("guest share re-storm %d: %v", i, err)
		}
	}
	if err := guestConn.WriteJSON(map[string]string{"event": "participant_media_state", "data": `{"micMuted":false,"cameraOff":false,"screenSharing":false}`}); err != nil {
		t.Fatalf("guest media-state clear: %v", err)
	}
	waitForGuestSharing(false, "after the un-bucketed media-state clear")
}

// countKanbanEventsByNameUntilQuiet is countKanbanEventsUntilQuiet for several
// event names in one drain (the connection is spent afterwards either way).
func countKanbanEventsByNameUntilQuiet(t *testing.T, conn *websocket.Conn, events []string, quiet, maxWindow time.Duration) map[string]int {
	t.Helper()
	wanted := map[string]bool{}
	for _, event := range events {
		wanted[event] = true
	}
	counts := map[string]int{}
	overall := time.Now().Add(maxWindow)
	for {
		deadline := time.Now().Add(quiet)
		if deadline.After(overall) {
			deadline = overall
		}
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			return counts
		}
		if message.Event != "kanban" {
			continue
		}
		var inner struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal([]byte(message.Data), &inner); err != nil {
			t.Fatalf("decode kanban envelope: %v", err)
		}
		if wanted[inner.Event] {
			counts[inner.Event]++
		}
	}
}

// (a) An explicit restart_ice after the 4-m-line uplink is established keeps
// every m-line and mid in place (no re-added or re-ordered share sections —
// this feature class has a damping/spiral history).
func TestWebsocketRestartIceKeepsPublisherUplinkMidsStable(t *testing.T) {
	conn := newIsolatedNativeWebsocket(t, "tom@shareability.com")
	writeNativeWebsocketEvent(t, conn, "participant", map[string]any{
		"client": map[string]string{"platform": "ios", "version": "test"},
	})
	waitForKanbanEvent(t, conn, "access_granted", 5*time.Second)
	writeNativeWebsocketEvent(t, conn, "media_ready", map[string]any{
		"client": map[string]string{"platform": "ios"},
		"media":  map[string]bool{"audio": true, "video": true},
	})
	initial := waitForServerOffer(t, conn, 5*time.Second)
	initialMids := sdpMidsInOrder(initial.SDP)
	if len(initialMids) != 4 {
		t.Fatalf("initial offer mids=%v, want 4 sections", initialMids)
	}
	nativePeer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create native peer: %v", err)
	}
	t.Cleanup(func() { _ = nativePeer.Close() })
	answerServerOffer(t, conn, nativePeer, initial)

	var before []*webrtc.RTPTransceiver
	listLock.RLock()
	for _, state := range peerConnections {
		if sameParticipantName(state.participantName, "Tom") && state.peerConnection != nil {
			before = state.peerConnection.GetTransceivers()
		}
	}
	listLock.RUnlock()
	if len(before) != 4 {
		t.Fatalf("server publisher transceivers before restart=%d, want 4", len(before))
	}

	writeNativeWebsocketEvent(t, conn, "restart_ice", map[string]any{"reason": "native-network-change"})
	restart := waitForServerOffer(t, conn, 8*time.Second)
	if iceUfragFromSDP(t, restart.SDP) == iceUfragFromSDP(t, initial.SDP) {
		t.Fatal("restart offer reused the initial ICE ufrag; no restart happened")
	}
	restartMids := sdpMidsInOrder(restart.SDP)
	if strings.Join(restartMids, ",") != strings.Join(initialMids, ",") {
		t.Fatalf("restart offer mids=%v, want the initial %v", restartMids, initialMids)
	}
	if v, a := len(sdpMediaSections(restart.SDP, "video")), len(sdpMediaSections(restart.SDP, "audio")); v != 2 || a != 2 {
		t.Fatalf("restart offer sections video=%d audio=%d, want 2 and 2", v, a)
	}
	var after []*webrtc.RTPTransceiver
	listLock.RLock()
	for _, state := range peerConnections {
		if sameParticipantName(state.participantName, "Tom") && state.peerConnection != nil {
			after = state.peerConnection.GetTransceivers()
		}
	}
	listLock.RUnlock()
	if len(after) != len(before) {
		t.Fatalf("server publisher transceivers after restart=%d, want %d", len(after), len(before))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("transceiver[%d] was replaced across the ICE restart", i)
		}
	}
	answerServerOffer(t, conn, nativePeer, restart)
}

// publishNativeCameraAndShare attaches a camera video track and a share video
// track to the native answerer BEFORE answering, so pion associates them with
// the first and third offered m-lines (camera video, screen video). It then
// pumps RTP on both until stop is called and feeds server ICE candidates to
// the native peer while waiting for the server to announce both tracks.
func publishNativeCameraAndShare(t *testing.T, conn *websocket.Conn, nativePeer *webrtc.PeerConnection, offer webrtc.SessionDescription) (stop func(), announced map[string]string) {
	t.Helper()
	camera, err := webrtc.NewTrackLocalStaticRTP(roomH264Codec.RTPCodecCapability, "native-camera", "native-stream")
	if err != nil {
		t.Fatalf("create camera track: %v", err)
	}
	share, err := webrtc.NewTrackLocalStaticRTP(roomH264Codec.RTPCodecCapability, "native-share", "native-share-stream")
	if err != nil {
		t.Fatalf("create share track: %v", err)
	}
	if _, err := nativePeer.AddTrack(camera); err != nil {
		t.Fatalf("add camera track: %v", err)
	}
	if _, err := nativePeer.AddTrack(share); err != nil {
		t.Fatalf("add share track: %v", err)
	}
	answerServerOffer(t, conn, nativePeer, offer)

	stopCh := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		var sequence uint16
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				sequence++
				for _, track := range []*webrtc.TrackLocalStaticRTP{camera, share} {
					_ = track.WriteRTP(&rtp.Packet{Header: rtp.Header{
						Version: 2, PayloadType: 102, SequenceNumber: sequence, Timestamp: uint32(sequence) * 3000, SSRC: 1234,
					}, Payload: []byte{0x65, 0x88, 0x84, 0x00}})
				}
			}
		}
	}()
	stop = func() {
		select {
		case <-stopCh:
		default:
			close(stopCh)
		}
		<-stopped
	}

	// Drain signaling: feed server candidates to the native peer, answer any
	// re-offer, and collect the server's participant_track announcements.
	announced = map[string]string{}
	deadline := time.Now().Add(15 * time.Second)
	for len(announced) < 2 {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			stop()
			t.Fatalf("waiting for both publisher tracks (announced=%v): %v", announced, err)
		}
		switch message.Event {
		case "candidate":
			var candidate webrtc.ICECandidateInit
			if err := json.Unmarshal([]byte(message.Data), &candidate); err == nil {
				_ = nativePeer.AddICECandidate(candidate)
			}
		case "offer":
			var reoffer struct {
				Type string `json:"type"`
				SDP  string `json:"sdp"`
			}
			if err := json.Unmarshal([]byte(message.Data), &reoffer); err == nil && nativePeer.SignalingState() == webrtc.SignalingStateStable {
				answerServerOffer(t, conn, nativePeer, webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: reoffer.SDP})
			}
		case "kanban":
			var inner struct {
				Event string `json:"event"`
				Data  struct {
					Name    string `json:"name"`
					Kind    string `json:"kind"`
					TrackID string `json:"trackId"`
					Source  string `json:"source"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(message.Data), &inner); err == nil && inner.Event == "participant_track" && inner.Data.Name == "Tom" && inner.Data.Kind == "video" {
				announced[inner.Data.Source] = inner.Data.TrackID
			}
		}
	}
	return stop, announced
}

func tomForwardedTrackIDs() (ids []string, sources int, paused int) {
	listLock.RLock()
	defer listLock.RUnlock()
	for trackID := range trackLocals {
		if sameParticipantName(trackParticipants[trackID], "Tom") {
			ids = append(ids, trackID)
		}
	}
	for trackID := range trackSources {
		if sameParticipantName(trackParticipants[trackID], "Tom") || trackLocals[trackID] == nil {
			sources++
		}
	}
	return ids, sources, len(trackPaused)
}

// (b) The 2026-07-06 incident shape: a publisher mid-share drops
// UNGRACEFULLY (PeerConnection + socket close, no screen_share_stopped). Both
// forwarded tracks must leave the registry through the OnTrack read-error /
// session-teardown path — including trackSources/trackPaused — subscribers
// must hear participant_left and get no replayed share label, and a rejoin
// must seat the same name once with a clean 4-m-line uplink.
func TestWebsocketUngracefulRejoinMidShareClearsShareTracks(t *testing.T) {
	server, roomID := newInCallRoomServer(t)
	timConn := dialInCallMember(t, server, "tim@shareability.com", roomID)
	admitInCallMember(t, timConn, roomID)

	tomConn := dialInCallMember(t, server, "tom@shareability.com", roomID)
	writeNativeWebsocketEvent(t, tomConn, "participant", map[string]any{
		"client": map[string]string{"platform": "ios", "version": "test"},
	})
	waitForKanbanEvent(t, tomConn, "access_granted", 5*time.Second)
	writeNativeWebsocketEvent(t, tomConn, "media_ready", map[string]any{
		"client": map[string]string{"platform": "ios"},
		"media":  map[string]bool{"audio": true, "video": true},
	})
	offer := waitForServerOffer(t, tomConn, 5*time.Second)
	nativePeer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create native peer: %v", err)
	}
	t.Cleanup(func() { _ = nativePeer.Close() })
	stop, announced := publishNativeCameraAndShare(t, tomConn, nativePeer, offer)
	defer stop()
	if announced[trackSourceCamera] == "" || announced[trackSourceScreen] == "" {
		t.Fatalf("server announced sources=%v, want camera and screen video", announced)
	}
	ids, _, _ := tomForwardedTrackIDs()
	if len(ids) != 2 {
		t.Fatalf("forwarded tracks for Tom=%v, want camera and share", ids)
	}

	// Ungraceful drop: no screen_share_stopped, no leave — transport and socket vanish.
	stop()
	_ = nativePeer.Close()
	_ = tomConn.Close()

	var left struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(waitForKanbanEvent(t, timConn, "participant_left", 10*time.Second), &left); err != nil {
		t.Fatalf("decode participant_left: %v", err)
	}
	if left.Name != "Tom" {
		t.Fatalf("participant_left=%+v, want Tom", left)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		ids, sources, paused := tomForwardedTrackIDs()
		if len(ids) == 0 && sources == 0 && paused == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the ungraceful drop Tom still has tracks=%v sources=%d paused=%d", ids, sources, paused)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// A subscriber's replay must not resurrect the share label.
	writeNativeWebsocketEvent(t, timConn, "request_participant_tracks", map[string]any{})
	if err := timConn.WriteJSON(map[string]string{"event": "room_hand", "data": `{"raised":false}`}); err != nil {
		t.Fatalf("sentinel: %v", err)
	}
	for {
		if err := timConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var message websocketMessage
		if err := timConn.ReadJSON(&message); err != nil {
			t.Fatalf("read waiting for sentinel: %v", err)
		}
		if message.Event != "kanban" {
			continue
		}
		var inner struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		_ = json.Unmarshal([]byte(message.Data), &inner)
		if inner.Event == "participant_track" && strings.Contains(string(inner.Data), `"name":"Tom"`) {
			t.Fatalf("zombie share label replayed after the ungraceful drop: %s", inner.Data)
		}
		if inner.Event == "room_hand" {
			break
		}
	}

	// Rejoin: same name, fresh session, clean 4-m-line uplink, seated once.
	rejoin := dialInCallMember(t, server, "tom@shareability.com", roomID)
	writeNativeWebsocketEvent(t, rejoin, "participant", map[string]any{})
	waitForKanbanEvent(t, rejoin, "access_granted", 5*time.Second)
	writeNativeWebsocketEvent(t, rejoin, "media_ready", map[string]any{})
	reoffer := waitForServerOffer(t, rejoin, 5*time.Second)
	if v, a := len(sdpMediaSections(reoffer.SDP, "video")), len(sdpMediaSections(reoffer.SDP, "audio")); v != 2 || a != 2 {
		t.Fatalf("rejoin offer sections video=%d audio=%d, want 2 and 2", v, a)
	}
	participants := kanbanApp.participantSnapshotForRoom(roomID)
	tomSeats := 0
	for _, name := range participants {
		if name == "Tom" {
			tomSeats++
		}
	}
	if tomSeats != 1 {
		t.Fatalf("participants after rejoin=%v, want Tom seated exactly once", participants)
	}
	if _, _, paused := tomForwardedTrackIDs(); paused != 0 {
		t.Fatalf("stale trackPaused entries after rejoin=%d", paused)
	}
}
