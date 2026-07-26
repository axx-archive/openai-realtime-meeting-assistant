package main

import (
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func endpointMediaStatesFromSnapshot(t *testing.T, snapshot map[string]any) map[string]map[string]participantMediaState {
	t.Helper()
	states, ok := snapshot["endpointMediaStates"].(map[string]map[string]participantMediaState)
	if !ok {
		t.Fatalf("endpointMediaStates=%T, want map[string]map[string]participantMediaState", snapshot["endpointMediaStates"])
	}
	return states
}

func legacyMediaStatesFromSnapshot(t *testing.T, snapshot map[string]any) map[string]participantMediaState {
	t.Helper()
	states, ok := snapshot["mediaStates"].(map[string]participantMediaState)
	if !ok {
		t.Fatalf("mediaStates=%T, want map[string]participantMediaState", snapshot["mediaStates"])
	}
	return states
}

func TestEndpointMediaStatesAreIndependentAndProjectLegacyState(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-laptop", "endpoint-laptop"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-phone", "endpoint-phone"); err != nil {
		t.Fatal(err)
	}

	if _, err := app.setParticipantEndpointMediaStateInRoom(officeRoomID, "AJ", "endpoint-laptop", "aj-laptop", participantMediaState{
		MicMuted: true, CameraOff: true,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := app.setParticipantEndpointMediaStateInRoom(officeRoomID, "AJ", "endpoint-phone", "aj-phone", participantMediaState{
		MicMuted: true, CameraOff: true, ScreenSharing: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	endpointStates := endpointMediaStatesFromSnapshot(t, snapshot)["AJ"]
	if len(endpointStates) != 2 || !endpointStates["endpoint-laptop"].CameraOff || !endpointStates["endpoint-phone"].ScreenSharing {
		t.Fatalf("endpoint states=%+v, want independent laptop and phone rows", endpointStates)
	}
	legacy := legacyMediaStatesFromSnapshot(t, snapshot)["AJ"]
	if !legacy.MicMuted || !legacy.CameraOff || !legacy.ScreenSharing {
		t.Fatalf("legacy projection=%+v, want all-muted/all-camera-off/any-share", legacy)
	}

	snapshot, err = app.setParticipantEndpointMediaStateInRoom(officeRoomID, "AJ", "endpoint-laptop", "aj-laptop", participantMediaState{})
	if err != nil {
		t.Fatal(err)
	}
	legacy = legacyMediaStatesFromSnapshot(t, snapshot)["AJ"]
	if legacy.MicMuted || legacy.CameraOff || !legacy.ScreenSharing {
		t.Fatalf("legacy projection=%+v, want live laptop plus sharing phone", legacy)
	}
	if endpointMediaStatesFromSnapshot(t, snapshot)["AJ"]["endpoint-phone"].CameraOff != true {
		t.Fatal("updating the laptop clobbered the phone's endpoint media state")
	}
}

func TestLegacyScreenSharingSetterFansOutToEndpointState(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-laptop", "endpoint-laptop"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-phone", "endpoint-phone"); err != nil {
		t.Fatal(err)
	}

	snapshot := app.setParticipantScreenSharingInRoom(officeRoomID, "AJ", true)
	endpointStates := endpointMediaStatesFromSnapshot(t, snapshot)["AJ"]
	if !endpointStates["endpoint-laptop"].ScreenSharing || !endpointStates["endpoint-phone"].ScreenSharing {
		t.Fatalf("endpoint states=%+v, want account-wide legacy sharing command on both endpoints", endpointStates)
	}
	if !legacyMediaStatesFromSnapshot(t, snapshot)["AJ"].ScreenSharing {
		t.Fatal("legacy projection did not preserve account-wide screen sharing")
	}

	snapshot = app.setParticipantScreenSharingInRoom(officeRoomID, "AJ", false)
	endpointStates = endpointMediaStatesFromSnapshot(t, snapshot)["AJ"]
	if endpointStates["endpoint-laptop"].ScreenSharing || endpointStates["endpoint-phone"].ScreenSharing {
		t.Fatalf("endpoint states=%+v, want account-wide legacy sharing stop on both endpoints", endpointStates)
	}
}

func TestEndpointMediaReplacementRejectsStaleSessionAndCleansOnLeave(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-old", "endpoint-laptop"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.setParticipantEndpointMediaStateInRoom(officeRoomID, "AJ", "endpoint-laptop", "aj-old", participantMediaState{CameraOff: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-new", "endpoint-laptop"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.setParticipantEndpointMediaStateInRoom(officeRoomID, "AJ", "endpoint-laptop", "aj-old", participantMediaState{CameraOff: true}); err == nil {
		t.Fatal("replaced session updated its successor's endpoint media state")
	}
	snapshot := app.roomSnapshot()
	if got := endpointMediaStatesFromSnapshot(t, snapshot)["AJ"]["endpoint-laptop"]; got.CameraOff || got.MicMuted || got.ScreenSharing {
		t.Fatalf("replacement endpoint state=%+v, want reset media state", got)
	}

	removed, stillPresent := app.forgetParticipantSessionResult("AJ", "aj-new")
	if !removed || stillPresent {
		t.Fatalf("last endpoint leave removed=%v stillPresent=%v, want true/false", removed, stillPresent)
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	room := app.roomLiveLocked(officeRoomID)
	if _, ok := room.participantEndpointMedia["AJ"]; ok {
		t.Fatal("endpoint media survived the participant's final endpoint leave")
	}
}

func TestEndpointMediaStateIsReprojectedAfterPartialLivenessReap(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-laptop", "endpoint-laptop"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-phone", "endpoint-phone"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.setParticipantEndpointMediaStateInRoom(officeRoomID, "AJ", "endpoint-laptop", "aj-laptop", participantMediaState{}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.setParticipantEndpointMediaStateInRoom(officeRoomID, "AJ", "endpoint-phone", "aj-phone", participantMediaState{MicMuted: true, CameraOff: true}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	app.mu.Lock()
	room := app.roomLiveLocked(officeRoomID)
	room.participants["AJ"] = now
	room.participantSessionLiveness["AJ"]["aj-laptop"] = now
	room.participantSessionLiveness["AJ"]["aj-phone"] = now.Add(-2 * participantLivenessTimeout)
	reaped := app.reapStaleParticipantSessionsLocked(now, participantLivenessTimeout)[officeRoomID]
	app.mu.Unlock()
	if len(reaped) != 1 || !reaped[0].stillPresent || len(reaped[0].sessionIDs) != 1 || reaped[0].sessionIDs[0] != "aj-phone" {
		t.Fatalf("reaped=%+v, want stale phone only", reaped)
	}
	snapshot := app.roomSnapshot()
	endpointStates := endpointMediaStatesFromSnapshot(t, snapshot)["AJ"]
	if len(endpointStates) != 1 {
		t.Fatalf("endpoint states=%+v, want laptop only", endpointStates)
	}
	if _, ok := endpointStates["endpoint-phone"]; ok {
		t.Fatal("reaped phone endpoint media survived")
	}
	legacy := legacyMediaStatesFromSnapshot(t, snapshot)["AJ"]
	if legacy.MicMuted || legacy.CameraOff {
		t.Fatalf("legacy state=%+v, want surviving laptop projection", legacy)
	}
}

func TestTransferAdmissionRetainsOnlyNewEndpointWithoutSeatFlicker(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-laptop", "endpoint-laptop"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-phone", "endpoint-phone"); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	room := app.roomLiveLocked(officeRoomID)
	if err := app.validateParticipantTransferAdmissionLocked(room, "AJ"); err != nil {
		app.mu.Unlock()
		t.Fatalf("transfer at endpoint cap was rejected: %v", err)
	}
	_, firstEndpoint, retired, err := app.transferParticipantSessionEndpointInRoomLocked(room, "AJ", "aj-tablet", "endpoint-tablet")
	app.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if firstEndpoint {
		t.Fatal("same-seat transfer reported a participant join")
	}
	sort.Strings(retired)
	if len(retired) != 2 || retired[0] != "aj-laptop" || retired[1] != "aj-phone" {
		t.Fatalf("retired sessions=%v, want laptop and phone", retired)
	}
	if !app.participantSessionCurrent("AJ", "aj-tablet") || app.participantSessionCurrent("AJ", "aj-laptop") || app.participantSessionCurrent("AJ", "aj-phone") {
		t.Fatal("transfer did not retain exactly the new session")
	}
	snapshot := app.roomSnapshot()
	if count := snapshot["endpointCounts"].(map[string]int)["AJ"]; count != 1 {
		t.Fatalf("endpoint count=%d, want 1 after transfer", count)
	}
	endpointStates := endpointMediaStatesFromSnapshot(t, snapshot)["AJ"]
	if len(endpointStates) != 1 {
		t.Fatalf("endpoint states=%+v, want new endpoint only", endpointStates)
	}
	if _, ok := endpointStates["endpoint-tablet"]; !ok {
		t.Fatal("new transfer endpoint is missing from endpoint media state")
	}
}

func TestTransferAdmissionInitializesMigratedEndpointMaps(t *testing.T) {
	now := time.Now().UTC()
	app := &kanbanBoardApp{}
	room := &roomLiveState{
		participants:      map[string]time.Time{"AJ": now},
		participantCounts: map[string]int{"AJ": 1},
		participantMedia:  map[string]participantMediaState{"AJ": {CameraOff: true}},
	}

	_, firstEndpoint, retired, err := app.transferParticipantSessionEndpointInRoomLocked(room, "AJ", "aj-tablet", "endpoint-tablet")
	if err != nil {
		t.Fatal(err)
	}
	if firstEndpoint || len(retired) != 0 {
		t.Fatalf("firstEndpoint=%v retired=%v, want existing migrated seat without known old sessions", firstEndpoint, retired)
	}
	if room.participantEndpoints["AJ"]["endpoint-tablet"] != "aj-tablet" {
		t.Fatalf("participantEndpoints=%+v, want initialized transfer endpoint", room.participantEndpoints)
	}
	if _, ok := room.participantSessionLiveness["AJ"]["aj-tablet"]; !ok {
		t.Fatalf("participantSessionLiveness=%+v, want initialized transfer liveness", room.participantSessionLiveness)
	}
}

func TestTransferRetiresOnlySameRoomPeersTracksAndPublishesEndpointMetadata(t *testing.T) {
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}
	laptopTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "aj-laptop-video", "aj-laptop-stream")
	if err != nil {
		t.Fatal(err)
	}
	phoneTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "aj-phone-video", "aj-phone-stream")
	if err != nil {
		t.Fatal(err)
	}
	otherRoomTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "aj-other-video", "aj-other-stream")
	if err != nil {
		t.Fatal(err)
	}

	snapshotPeerState(t)
	listLock.Lock()
	previousOwners := trackMediaOwners
	activeParticipantConnections = map[string]peerConnectionState{
		participantConnectionKey("AJ", "endpoint-laptop"): {participantName: "AJ", sessionID: "aj-laptop", endpointID: "endpoint-laptop", roomID: "room-a"},
		participantConnectionKey("AJ", "endpoint-phone"):  {participantName: "AJ", sessionID: "aj-phone", endpointID: "endpoint-phone", roomID: "room-a"},
		participantConnectionKey("AJ", "endpoint-other"):  {participantName: "AJ", sessionID: "aj-other", endpointID: "endpoint-other", roomID: "room-b"},
		participantConnectionKey("Tim", "endpoint-tim"):   {participantName: "Tim", sessionID: "tim-room-a", endpointID: "endpoint-tim", roomID: "room-a"},
	}
	peerConnections = []peerConnectionState{
		{participantName: "AJ", sessionID: "aj-laptop", endpointID: "endpoint-laptop", roomID: "room-a"},
		{participantName: "AJ", sessionID: "aj-phone", endpointID: "endpoint-phone", roomID: "room-a"},
		{participantName: "AJ", sessionID: "aj-other", endpointID: "endpoint-other", roomID: "room-b"},
		{participantName: "Tim", sessionID: "tim-room-a", endpointID: "endpoint-tim", roomID: "room-a"},
	}
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{
		laptopTrack.ID(): laptopTrack, phoneTrack.ID(): phoneTrack, otherRoomTrack.ID(): otherRoomTrack,
	}
	trackParticipants = map[string]string{
		laptopTrack.ID(): "AJ", phoneTrack.ID(): "AJ", otherRoomTrack.ID(): "AJ",
	}
	trackParticipantSessions = map[string]string{
		laptopTrack.ID(): "aj-laptop", phoneTrack.ID(): "aj-phone", otherRoomTrack.ID(): "aj-other",
	}
	trackRooms = map[string]string{
		laptopTrack.ID(): "room-a", phoneTrack.ID(): "room-a", otherRoomTrack.ID(): "room-b",
	}
	trackSourceIDs = map[string]string{
		laptopTrack.ID(): "laptop-source", phoneTrack.ID(): "phone-source", otherRoomTrack.ID(): "other-source",
	}
	trackMediaOwners = map[string]trackMediaOwner{
		otherRoomTrack.ID(): {track: otherRoomTrack, generation: 7, endpointID: "endpoint-other"},
	}
	listLock.Unlock()
	t.Cleanup(func() {
		listLock.Lock()
		trackMediaOwners = previousOwners
		listLock.Unlock()
	})

	replaceParticipantSessionEndpointInRoom("room-a", "AJ", "aj-tablet", "endpoint-tablet", nil, nil, "aj@example.com", true)
	listLock.RLock()
	if _, ok := trackLocals[laptopTrack.ID()]; ok {
		listLock.RUnlock()
		t.Fatal("laptop track survived explicit transfer")
	}
	if _, ok := trackLocals[phoneTrack.ID()]; ok {
		listLock.RUnlock()
		t.Fatal("phone track survived explicit transfer")
	}
	if _, ok := trackLocals[otherRoomTrack.ID()]; !ok {
		listLock.RUnlock()
		t.Fatal("transfer removed the account's other-room track")
	}
	if len(peerConnections) != 2 {
		listLock.RUnlock()
		t.Fatalf("peerConnections=%+v, want other-room AJ and same-room Tim", peerConnections)
	}
	if current := activeParticipantConnections[participantConnectionKey("AJ", "endpoint-tablet")]; current.sessionID != "aj-tablet" {
		listLock.RUnlock()
		t.Fatalf("current transfer session=%+v, want aj-tablet", current)
	}
	snapshots := participantTrackSnapshotsLockedForGeneration("room-b", "", 7, true)
	listLock.RUnlock()
	if len(snapshots) != 1 || snapshots[0].EndpointID != "endpoint-other" {
		t.Fatalf("replayed participant_track=%+v, want owning endpoint identity", snapshots)
	}
	livePayload := participantTrackPayloadFields("AJ", "video", "forwarded", "source", "stream", "endpoint-tablet")
	if livePayload["endpointId"] != "endpoint-tablet" {
		t.Fatalf("live participant_track payload=%+v, want endpointId", livePayload)
	}
}
