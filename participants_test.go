package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestMeetingRoomDefaultMatchesRosterCapacity(t *testing.T) {
	t.Setenv("MEETING_ROOM_MAX_PARTICIPANTS", "")

	if configuredMeetingRoomCapacity() != defaultMeetingRoomCapacity {
		t.Fatalf("capacity=%d, want %d", configuredMeetingRoomCapacity(), defaultMeetingRoomCapacity)
	}
	if len(meetingParticipantNames) != configuredMeetingRoomCapacity() {
		t.Fatalf("participant seats=%d, want %d", len(meetingParticipantNames), configuredMeetingRoomCapacity())
	}
}

func TestMeetingRoomRosterHasSeededAccountEmails(t *testing.T) {
	for _, name := range meetingParticipantNames {
		email := participantEmail(name)
		if email == "" {
			t.Fatalf("participant %q has no seeded email", name)
		}
		if participantNameForEmail(email) != name {
			t.Fatalf("participant %q email %q resolves to %q", name, email, participantNameForEmail(email))
		}
	}
}

func TestExpectedKanbanBroadcastCloseDetection(t *testing.T) {
	for _, message := range []string{
		"websocket: close sent",
		"write tcp 172.18.0.3:3000->172.18.0.4:46680: write: broken pipe",
		"write tcp: use of closed network connection",
	} {
		if !isExpectedKanbanBroadcastClose(errors.New(message)) {
			t.Fatalf("close error %q was not treated as expected", message)
		}
	}

	if isExpectedKanbanBroadcastClose(errors.New("temporary upstream write failure")) {
		t.Fatal("unexpected write failures should still be logged")
	}
}

func TestMeetingRoomCapacityCanComeFromEnvironment(t *testing.T) {
	t.Setenv("MEETING_ROOM_MAX_PARTICIPANTS", "6")
	if configuredMeetingRoomCapacity() != 6 {
		t.Fatalf("capacity=%d, want 6", configuredMeetingRoomCapacity())
	}
}

func TestAdmitParticipantEnforcesCapacity(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("MEETING_ROOM_MAX_PARTICIPANTS", "2")

	app := newKanbanBoardApp()
	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("admit AJ: %v", err)
	}
	if _, err := app.admitParticipant("Tim"); err != nil {
		t.Fatalf("admit Tim: %v", err)
	}

	if _, err := app.admitParticipant("Tom"); err == nil {
		t.Fatal("admit Tom succeeded in a full room")
	} else if !strings.Contains(err.Error(), "supports 2 people") {
		t.Fatalf("full room error=%q, want capacity detail", err.Error())
	}

	if count := app.activeParticipantCount(officeRoomID); count != 2 {
		t.Fatalf("active participants=%d, want 2", count)
	}

	app.forgetParticipant("AJ")
	if _, err := app.admitParticipant("Tom"); err != nil {
		t.Fatalf("admit Tom after one leaves: %v", err)
	}
}

func TestAdmitParticipantAllowsSameNameReconnectWhenRoomFull(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("MEETING_ROOM_MAX_PARTICIPANTS", "2")

	app := newKanbanBoardApp()
	if _, err := app.admitParticipantSession("AJ", "aj-old"); err != nil {
		t.Fatalf("admit AJ: %v", err)
	}
	if _, err := app.admitParticipantSession("Tim", "tim"); err != nil {
		t.Fatalf("admit Tim: %v", err)
	}
	if _, err := app.admitParticipantSession("AJ", "aj-new"); err != nil {
		t.Fatalf("re-admit AJ in full room: %v", err)
	}

	if count := app.activeParticipantCount(officeRoomID); count != 2 {
		t.Fatalf("active participants=%d, want unique count 2", count)
	}

	if app.forgetParticipantSession("AJ", "aj-old") {
		t.Fatal("stale AJ session removed the fresh reconnect")
	}
	if snapshot := app.participantSnapshot(); !containsParticipant(snapshot, "AJ") {
		t.Fatalf("AJ was removed by stale session cleanup: %v", snapshot)
	}
	if !app.forgetParticipantSession("AJ", "aj-new") {
		t.Fatal("fresh AJ session was not removed")
	}
	if snapshot := app.participantSnapshot(); containsParticipant(snapshot, "AJ") {
		t.Fatalf("AJ remained after fresh session left: %v", snapshot)
	}
}

func TestParticipantReconnectResetsMediaState(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))

	app := newKanbanBoardApp()
	if _, err := app.admitParticipantSession("Joel", "joel-old"); err != nil {
		t.Fatalf("admit Joel: %v", err)
	}
	if _, err := app.setParticipantMediaState("Joel", participantMediaState{
		MicMuted:      true,
		CameraOff:     true,
		ScreenSharing: true,
	}); err != nil {
		t.Fatalf("set media state: %v", err)
	}
	if _, err := app.admitParticipantSession("Joel", "joel-new"); err != nil {
		t.Fatalf("re-admit Joel: %v", err)
	}

	snapshot := app.roomSnapshot()
	rawMediaStates, ok := snapshot["mediaStates"].(map[string]participantMediaState)
	if !ok {
		t.Fatalf("mediaStates=%T, want map[string]participantMediaState", snapshot["mediaStates"])
	}
	joelState := rawMediaStates["Joel"]
	if joelState.MicMuted || joelState.CameraOff || joelState.ScreenSharing {
		t.Fatalf("Joel media state=%+v, want reset after reconnect", joelState)
	}
}

func TestReplaceExistingParticipantSessionRemovesSameParticipantTracks(t *testing.T) {
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}
	ajTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "aj-video", "aj-stream")
	if err != nil {
		t.Fatalf("create AJ track: %v", err)
	}
	timTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "tim-video", "tim-stream")
	if err != nil {
		t.Fatalf("create Tim track: %v", err)
	}

	listLock.Lock()
	previousPeerConnections := peerConnections
	previousTrackLocals := trackLocals
	previousActiveParticipantConnections := activeParticipantConnections
	previousTrackParticipants := trackParticipants
	previousTrackParticipantSessions := trackParticipantSessions
	previousTrackSourceIDs := trackSourceIDs
	activeParticipantConnections = map[string]peerConnectionState{
		"AJ": {participantName: "AJ", sessionID: "old"},
	}
	peerConnections = []peerConnectionState{{participantName: "AJ", sessionID: "old"}}
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{
		ajTrack.ID():  ajTrack,
		timTrack.ID(): timTrack,
	}
	trackParticipants = map[string]string{
		ajTrack.ID():  "AJ",
		timTrack.ID(): "Tim",
	}
	trackParticipantSessions = map[string]string{
		ajTrack.ID():  "old",
		timTrack.ID(): "tim",
	}
	trackSourceIDs = map[string]string{
		ajTrack.ID():  "aj-source",
		timTrack.ID(): "tim-source",
	}
	listLock.Unlock()
	defer func() {
		listLock.Lock()
		peerConnections = previousPeerConnections
		activeParticipantConnections = previousActiveParticipantConnections
		trackLocals = previousTrackLocals
		trackParticipants = previousTrackParticipants
		trackParticipantSessions = previousTrackParticipantSessions
		trackSourceIDs = previousTrackSourceIDs
		listLock.Unlock()
	}()

	replaceExistingParticipantSession("AJ", "new", nil, nil, "aj@shareability.com")

	listLock.RLock()
	defer listLock.RUnlock()
	if len(peerConnections) != 0 {
		t.Fatalf("peerConnections=%d, want stale AJ connection removed", len(peerConnections))
	}
	if state := activeParticipantConnections["AJ"]; state.sessionID != "new" {
		t.Fatalf("active AJ session=%q, want replacement session", state.sessionID)
	}
	if _, ok := trackLocals[ajTrack.ID()]; ok {
		t.Fatal("AJ track remained after replacement")
	}
	if _, ok := trackLocals[timTrack.ID()]; !ok {
		t.Fatal("Tim track was removed during AJ replacement")
	}
	if _, ok := trackSourceIDs[ajTrack.ID()]; ok {
		t.Fatal("AJ track source remained after replacement")
	}
}

func TestParticipantTrackSnapshotsReplayExistingRemoteTracks(t *testing.T) {
	videoCodec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}
	audioCodec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}
	ajTrack, err := webrtc.NewTrackLocalStaticRTP(videoCodec, "aj-video", "aj-stream")
	if err != nil {
		t.Fatalf("create AJ track: %v", err)
	}
	timTrack, err := webrtc.NewTrackLocalStaticRTP(audioCodec, "tim-audio", "tim-stream")
	if err != nil {
		t.Fatalf("create Tim track: %v", err)
	}

	listLock.Lock()
	previousTrackLocals := trackLocals
	previousTrackParticipants := trackParticipants
	previousTrackParticipantSessions := trackParticipantSessions
	previousTrackSourceIDs := trackSourceIDs
	previousTrackRooms := trackRooms
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{
		ajTrack.ID():  ajTrack,
		timTrack.ID(): timTrack,
	}
	trackParticipants = map[string]string{
		ajTrack.ID():  "AJ",
		timTrack.ID(): "Tim",
	}
	trackParticipantSessions = map[string]string{
		ajTrack.ID():  "aj-session",
		timTrack.ID(): "tim-session",
	}
	trackSourceIDs = map[string]string{
		ajTrack.ID():  "aj-camera-source",
		timTrack.ID(): "tim-mic-source",
	}
	// No trackRooms rows at all — legacy office entries (§9).
	trackRooms = map[string]string{}
	listLock.Unlock()
	defer func() {
		listLock.Lock()
		trackLocals = previousTrackLocals
		trackParticipants = previousTrackParticipants
		trackParticipantSessions = previousTrackParticipantSessions
		trackSourceIDs = previousTrackSourceIDs
		trackRooms = previousTrackRooms
		listLock.Unlock()
	}()

	snapshots := participantTrackSnapshots(officeRoomID, "AJ")
	if len(snapshots) != 1 {
		t.Fatalf("snapshots=%v, want only Tim's remote track for AJ", snapshots)
	}
	snapshot := snapshots[0]
	if snapshot.Name != "Tim" {
		t.Fatalf("snapshot name=%q, want Tim", snapshot.Name)
	}
	if snapshot.Kind != "audio" {
		t.Fatalf("snapshot kind=%q, want audio", snapshot.Kind)
	}
	if snapshot.TrackID != timTrack.ID() {
		t.Fatalf("snapshot trackID=%q, want %q", snapshot.TrackID, timTrack.ID())
	}
	if snapshot.SourceTrackID != "tim-mic-source" {
		t.Fatalf("snapshot sourceTrackID=%q, want tim-mic-source", snapshot.SourceTrackID)
	}
	if snapshot.StreamID != "tim-stream" {
		t.Fatalf("snapshot streamID=%q, want tim-stream", snapshot.StreamID)
	}
	if snapshot.RoomID != officeRoomID {
		t.Fatalf("snapshot roomID=%q, want office (legacy trackRooms rows are office, §9)", snapshot.RoomID)
	}
}

func TestRoomPeerConnectionOffersStableSafariCompatibleVideoCodecs(t *testing.T) {
	peerConnection, err := newPeerConnection()
	if err != nil {
		t.Fatalf("create peer connection: %v", err)
	}
	defer peerConnection.Close()

	if _, err := peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionSendonly,
	}); err != nil {
		t.Fatalf("add video transceiver: %v", err)
	}

	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	if !strings.Contains(offer.SDP, "VP8/90000") {
		t.Fatalf("offer SDP missing VP8 codec:\n%s", offer.SDP)
	}
	if !strings.Contains(offer.SDP, "H264/90000") {
		t.Fatalf("offer SDP missing Safari-compatible H264 codec:\n%s", offer.SDP)
	}
	if !strings.Contains(offer.SDP, "profile-level-id=42e01f") {
		t.Fatalf("offer SDP missing constrained baseline H264 fmtp:\n%s", offer.SDP)
	}
	videoLine := ""
	for _, line := range strings.Split(offer.SDP, "\n") {
		if strings.HasPrefix(line, "m=video ") {
			videoLine = line
			break
		}
	}
	if videoLine == "" {
		t.Fatalf("offer SDP missing video m-line:\n%s", offer.SDP)
	}
	h264Index := strings.Index(videoLine, " 102")
	vp8Index := strings.Index(videoLine, " 96")
	if h264Index == -1 || vp8Index == -1 || h264Index > vp8Index {
		t.Fatalf("offer SDP should prefer constrained-baseline H264 before VP8 for Safari; video m-line=%q", videoLine)
	}
}

func TestForwardedTrackOfferUsesSourceCodecPreference(t *testing.T) {
	peerConnection, err := newPeerConnection()
	if err != nil {
		t.Fatalf("create peer connection: %v", err)
	}
	defer peerConnection.Close()

	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
		MimeType:     webrtc.MimeTypeVP8,
		ClockRate:    90000,
		RTCPFeedback: []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}, {Type: "goog-remb"}},
	}, "guest-video", "guest-stream")
	if err != nil {
		t.Fatalf("create forwarded track: %v", err)
	}

	transceiver, err := peerConnection.AddTransceiverFromTrack(track, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionSendonly,
	})
	if err != nil {
		t.Fatalf("add forwarded track: %v", err)
	}
	if err := preferSourceTrackCodec(transceiver, track); err != nil {
		t.Fatalf("prefer source codec: %v", err)
	}

	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	videoLine := ""
	for _, line := range strings.Split(offer.SDP, "\n") {
		if strings.HasPrefix(line, "m=video ") {
			videoLine = line
			break
		}
	}
	if !strings.Contains(offer.SDP, "VP8/90000") {
		t.Fatalf("offer SDP missing VP8 codec:\n%s", offer.SDP)
	}
	if strings.Contains(videoLine, " 102") {
		t.Fatalf("forwarded VP8 track offer should not advertise H264 before binding; video m-line=%q", videoLine)
	}
}

func TestRoomSnapshotIncludesParticipantMediaState(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))

	app := newKanbanBoardApp()
	if _, err := app.admitParticipant("AJ"); err != nil {
		t.Fatalf("admit AJ: %v", err)
	}
	if _, err := app.setParticipantMediaState("AJ", participantMediaState{
		MicMuted:  true,
		CameraOff: true,
	}); err != nil {
		t.Fatalf("set media state: %v", err)
	}

	snapshot := app.roomSnapshot()
	rawMediaStates, ok := snapshot["mediaStates"].(map[string]participantMediaState)
	if !ok {
		t.Fatalf("mediaStates=%T, want map[string]participantMediaState", snapshot["mediaStates"])
	}
	ajState := rawMediaStates["AJ"]
	if !ajState.MicMuted || !ajState.CameraOff {
		t.Fatalf("AJ media state=%+v, want muted camera-off", ajState)
	}
	if ajState.UpdatedAt == "" {
		t.Fatal("AJ media state UpdatedAt is empty")
	}

	app.forgetParticipant("AJ")
	snapshot = app.roomSnapshot()
	rawMediaStates = snapshot["mediaStates"].(map[string]participantMediaState)
	if _, ok := rawMediaStates["AJ"]; ok {
		t.Fatal("AJ media state remained after participant left")
	}
}

func TestGuestNamesAreNotRosterParticipants(t *testing.T) {
	if canonicalParticipantName("guest 1") != "" {
		t.Fatal("guest names should not be canonical participants")
	}
	if email := participantEmail("Guest 1"); email != "" {
		t.Fatalf("guest email=%q, want empty", email)
	}
}

func containsParticipant(participants []string, name string) bool {
	for _, participant := range participants {
		if participant == name {
			return true
		}
	}
	return false
}
