package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestMeetingRoomDefaultSupportsTenParticipants(t *testing.T) {
	t.Setenv("MEETING_ROOM_MAX_PARTICIPANTS", "")

	if configuredMeetingRoomCapacity() != defaultMeetingRoomCapacity {
		t.Fatalf("capacity=%d, want %d", configuredMeetingRoomCapacity(), defaultMeetingRoomCapacity)
	}
	if len(meetingParticipantNames) < configuredMeetingRoomCapacity() {
		t.Fatalf("participant seats=%d, want at least %d", len(meetingParticipantNames), configuredMeetingRoomCapacity())
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

	if _, err := app.admitParticipant("Jake"); err == nil {
		t.Fatal("admit Jake succeeded in a full room")
	} else if !strings.Contains(err.Error(), "supports 2 people") {
		t.Fatalf("full room error=%q, want capacity detail", err.Error())
	}

	if count := app.activeParticipantCount(); count != 2 {
		t.Fatalf("active participants=%d, want 2", count)
	}

	app.forgetParticipant("AJ")
	if _, err := app.admitParticipant("Jake"); err != nil {
		t.Fatalf("admit Jake after one leaves: %v", err)
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

	if count := app.activeParticipantCount(); count != 2 {
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
	listLock.Unlock()
	defer func() {
		listLock.Lock()
		peerConnections = previousPeerConnections
		activeParticipantConnections = previousActiveParticipantConnections
		trackLocals = previousTrackLocals
		trackParticipants = previousTrackParticipants
		trackParticipantSessions = previousTrackParticipantSessions
		listLock.Unlock()
	}()

	replaceExistingParticipantSession("AJ", "new", nil, nil)

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

func TestGuestSeatsDoNotCreateEmailRecipients(t *testing.T) {
	if canonicalParticipantName("guest 1") != "Guest 1" {
		t.Fatal("guest seat should be a canonical participant")
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
