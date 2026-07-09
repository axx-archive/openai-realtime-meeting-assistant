package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
)

// TestTwoEndpointsOfOneAccountBothStayAdmitted is the mandated case: the same
// account joining from two devices (distinct endpoint ids) keeps BOTH sessions
// live and current. Neither device evicts the other.
func TestTwoEndpointsOfOneAccountBothStayAdmitted(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))

	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-laptop", "endpoint-laptop"); err != nil {
		t.Fatalf("admit AJ laptop: %v", err)
	}
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-phone", "endpoint-phone"); err != nil {
		t.Fatalf("admit AJ phone: %v", err)
	}

	if !app.participantSessionCurrent("AJ", "aj-laptop") {
		t.Fatal("laptop session was evicted when the phone joined")
	}
	if !app.participantSessionCurrent("AJ", "aj-phone") {
		t.Fatal("phone session is not current after joining")
	}

	if snapshot := app.participantSnapshot(); !containsParticipant(snapshot, "AJ") {
		t.Fatalf("AJ missing from roster with two endpoints: %v", snapshot)
	}
}

// TestSameEndpointRefreshReplacesPriorSession proves the load-bearing refresh
// protection survives: a new session on the SAME endpoint retires the old one
// (a reloaded tab), so a zombie tab cannot fight a fresh one.
func TestSameEndpointRefreshReplacesPriorSession(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))

	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-old", "endpoint-laptop"); err != nil {
		t.Fatalf("admit AJ old: %v", err)
	}
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-new", "endpoint-laptop"); err != nil {
		t.Fatalf("re-admit AJ on same endpoint: %v", err)
	}

	if app.participantSessionCurrent("AJ", "aj-old") {
		t.Fatal("stale session on the refreshed endpoint is still current")
	}
	if !app.participantSessionCurrent("AJ", "aj-new") {
		t.Fatal("fresh session on the refreshed endpoint is not current")
	}
	if count := app.activeParticipantCount(officeRoomID); count != 1 {
		t.Fatalf("active participants=%d, want 1 (a refresh is not a second seat)", count)
	}
}

// TestTwoEndpointsCountAsOneSeat confirms capacity is measured per person, so a
// laptop+phone pair does not consume two of the room's seats.
func TestTwoEndpointsCountAsOneSeat(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("MEETING_ROOM_MAX_PARTICIPANTS", "2")

	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-laptop", "endpoint-laptop"); err != nil {
		t.Fatalf("admit AJ laptop: %v", err)
	}
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-phone", "endpoint-phone"); err != nil {
		t.Fatalf("admit AJ phone: %v", err)
	}
	if count := app.activeParticipantCount(officeRoomID); count != 1 {
		t.Fatalf("active participants=%d, want 1 seat for AJ's two devices", count)
	}

	// The second seat is still free for a different person.
	if _, _, err := app.admitParticipantSessionEndpoint("Tim", "tim-laptop", "endpoint-tim"); err != nil {
		t.Fatalf("admit Tim into the room's second seat: %v", err)
	}
	if count := app.activeParticipantCount(officeRoomID); count != 2 {
		t.Fatalf("active participants=%d, want 2 (AJ + Tim)", count)
	}

	// AJ's endpoint counts surface for the roster affordance.
	snapshot := app.roomSnapshot()
	endpointCounts, ok := snapshot["endpointCounts"].(map[string]int)
	if !ok {
		t.Fatalf("endpointCounts=%T, want map[string]int", snapshot["endpointCounts"])
	}
	if endpointCounts["AJ"] != 2 {
		t.Fatalf("AJ endpoint count=%d, want 2", endpointCounts["AJ"])
	}
	if endpointCounts["Tim"] != 1 {
		t.Fatalf("Tim endpoint count=%d, want 1", endpointCounts["Tim"])
	}
}

// TestThirdEndpointRejectedCleanly caps concurrent devices per account and
// returns a legible message rather than silently dropping a session.
func TestThirdEndpointRejectedCleanly(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("BONFIRE_MAX_ENDPOINTS_PER_USER", "")

	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-1", "endpoint-1"); err != nil {
		t.Fatalf("admit endpoint 1: %v", err)
	}
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-2", "endpoint-2"); err != nil {
		t.Fatalf("admit endpoint 2: %v", err)
	}
	_, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-3", "endpoint-3")
	if err == nil {
		t.Fatal("third endpoint was admitted past the per-account cap")
	}
	if !strings.Contains(err.Error(), "devices") {
		t.Fatalf("third-endpoint error=%q, want a device-cap explanation", err.Error())
	}

	// The two existing endpoints are untouched by the rejection.
	if !app.participantSessionCurrent("AJ", "aj-1") || !app.participantSessionCurrent("AJ", "aj-2") {
		t.Fatal("rejecting the third endpoint disturbed the first two")
	}
	if count := app.activeParticipantCount(officeRoomID); count != 1 {
		t.Fatalf("active participants=%d, want 1", count)
	}
}

// TestEndpointCapIsConfigurable lets an operator widen the device cap.
func TestEndpointCapIsConfigurable(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("BONFIRE_MAX_ENDPOINTS_PER_USER", "3")

	app := newKanbanBoardApp()
	for i, session := range []string{"aj-1", "aj-2", "aj-3"} {
		endpoint := "endpoint-" + session
		if _, _, err := app.admitParticipantSessionEndpoint("AJ", session, endpoint); err != nil {
			t.Fatalf("admit endpoint %d: %v", i+1, err)
		}
	}
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-4", "endpoint-4"); err == nil {
		t.Fatal("fourth endpoint admitted past a cap of 3")
	}
}

// TestEmptyEndpointIDKeepsLegacySingleSlot proves old clients (and native Apple,
// which send no endpoint id) behave exactly as before: one shared slot,
// last-writer-wins, one seat.
func TestEmptyEndpointIDKeepsLegacySingleSlot(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))

	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-old", ""); err != nil {
		t.Fatalf("admit AJ old (legacy): %v", err)
	}
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-new", ""); err != nil {
		t.Fatalf("re-admit AJ (legacy): %v", err)
	}

	// Second legacy session replaces the first in the single shared slot.
	if app.participantSessionCurrent("AJ", "aj-old") {
		t.Fatal("legacy old session should have been replaced")
	}
	if !app.participantSessionCurrent("AJ", "aj-new") {
		t.Fatal("legacy new session is not current")
	}
	if count := app.activeParticipantCount(officeRoomID); count != 1 {
		t.Fatalf("active participants=%d, want 1", count)
	}
	// Legacy clients can never exceed one endpoint, so the device cap is moot.
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-newer", ""); err != nil {
		t.Fatalf("a third legacy admit should still just replace the slot: %v", err)
	}
}

// TestForgetOneEndpointLeavesTheOther makes sure a device leaving (its socket
// closing) removes only its own session, not the account's other device.
func TestForgetOneEndpointLeavesTheOther(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))

	app := newKanbanBoardApp()
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-laptop", "endpoint-laptop"); err != nil {
		t.Fatalf("admit AJ laptop: %v", err)
	}
	if _, _, err := app.admitParticipantSessionEndpoint("AJ", "aj-phone", "endpoint-phone"); err != nil {
		t.Fatalf("admit AJ phone: %v", err)
	}

	if !app.forgetParticipantSession("AJ", "aj-phone") {
		t.Fatal("forgetting the phone session reported no change")
	}
	if !app.participantSessionCurrent("AJ", "aj-laptop") {
		t.Fatal("the laptop session was dropped when the phone left")
	}
	if snapshot := app.participantSnapshot(); !containsParticipant(snapshot, "AJ") {
		t.Fatalf("AJ left the roster while the laptop is still connected: %v", snapshot)
	}
	if count := app.activeParticipantCount(officeRoomID); count != 1 {
		t.Fatalf("active participants=%d, want AJ still present", count)
	}

	// A stale (already-replaced) session is a no-op.
	if app.forgetParticipantSession("AJ", "aj-phone") {
		t.Fatal("forgetting an already-removed session reported a change")
	}

	// The last device leaving clears the account entirely.
	if !app.forgetParticipantSession("AJ", "aj-laptop") {
		t.Fatal("forgetting the last session reported no change")
	}
	if snapshot := app.participantSnapshot(); containsParticipant(snapshot, "AJ") {
		t.Fatalf("AJ remained after every device left: %v", snapshot)
	}
}

// TestPresenceTransitionsGateJoinLeftBroadcasts locks the semantics the
// join/left room broadcasts depend on: firstEndpoint is true only when an
// account goes from absent to present, and stillPresent stays true while another
// device of the account remains — so a second device joining or one of two
// devices leaving never fires a spurious "joined"/"left" to the room.
func TestPresenceTransitionsGateJoinLeftBroadcasts(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))

	app := newKanbanBoardApp()

	// First device: this is a genuine arrival.
	_, firstEndpoint, err := app.admitParticipantSessionEndpoint("AJ", "aj-laptop", "endpoint-laptop")
	if err != nil {
		t.Fatalf("admit laptop: %v", err)
	}
	if !firstEndpoint {
		t.Fatal("first device should report a genuine arrival")
	}

	// Second device: NOT an arrival — the person is already here.
	_, firstEndpoint, err = app.admitParticipantSessionEndpoint("AJ", "aj-phone", "endpoint-phone")
	if err != nil {
		t.Fatalf("admit phone: %v", err)
	}
	if firstEndpoint {
		t.Fatal("second device must not report a room arrival")
	}

	// Same-endpoint refresh of a still-2-device account: not an arrival either.
	_, firstEndpoint, err = app.admitParticipantSessionEndpoint("AJ", "aj-phone-2", "endpoint-phone")
	if err != nil {
		t.Fatalf("refresh phone: %v", err)
	}
	if firstEndpoint {
		t.Fatal("refreshing an endpoint of a present account is not an arrival")
	}

	// One of two devices leaves: removed, but the person is still present.
	removed, stillPresent := app.forgetParticipantSessionResult("AJ", "aj-phone-2")
	if !removed || !stillPresent {
		t.Fatalf("one device leaving: removed=%v stillPresent=%v, want true/true", removed, stillPresent)
	}

	// Last device leaves: now the person is gone.
	removed, stillPresent = app.forgetParticipantSessionResult("AJ", "aj-laptop")
	if !removed || stillPresent {
		t.Fatalf("last device leaving: removed=%v stillPresent=%v, want true/false", removed, stillPresent)
	}

	// A brand-new account after everyone left is once again a genuine arrival.
	_, firstEndpoint, err = app.admitParticipantSessionEndpoint("Tim", "tim-1", "endpoint-tim")
	if err != nil {
		t.Fatalf("admit Tim: %v", err)
	}
	if !firstEndpoint {
		t.Fatal("a fresh account should report an arrival")
	}
}

// TestReplaceEndpointSessionPreservesOtherDeviceTracks is the SFU-level proof
// that a second device's join (or a refresh of the first) does not tear down the
// account's other endpoint's forwarded media — the media analogue of the
// session-model guarantee above, mirroring
// TestReplaceExistingParticipantSessionRemovesSameParticipantTracks.
func TestReplaceEndpointSessionPreservesOtherDeviceTracks(t *testing.T) {
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}
	laptopTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "aj-laptop-video", "aj-laptop-stream")
	if err != nil {
		t.Fatalf("create laptop track: %v", err)
	}
	phoneTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "aj-phone-video", "aj-phone-stream")
	if err != nil {
		t.Fatalf("create phone track: %v", err)
	}

	listLock.Lock()
	previousPeerConnections := peerConnections
	previousActiveParticipantConnections := activeParticipantConnections
	previousTrackLocals := trackLocals
	previousTrackParticipants := trackParticipants
	previousTrackParticipantSessions := trackParticipantSessions
	previousTrackSourceIDs := trackSourceIDs
	activeParticipantConnections = map[string]peerConnectionState{
		participantConnectionKey("AJ", "endpoint-laptop"): {participantName: "AJ", sessionID: "aj-laptop-old", endpointID: "endpoint-laptop"},
		participantConnectionKey("AJ", "endpoint-phone"):  {participantName: "AJ", sessionID: "aj-phone", endpointID: "endpoint-phone"},
	}
	peerConnections = []peerConnectionState{
		{participantName: "AJ", sessionID: "aj-laptop-old", endpointID: "endpoint-laptop"},
		{participantName: "AJ", sessionID: "aj-phone", endpointID: "endpoint-phone"},
	}
	trackLocals = map[string]*webrtc.TrackLocalStaticRTP{
		laptopTrack.ID(): laptopTrack,
		phoneTrack.ID():  phoneTrack,
	}
	trackParticipants = map[string]string{
		laptopTrack.ID(): "AJ",
		phoneTrack.ID():  "AJ",
	}
	trackParticipantSessions = map[string]string{
		laptopTrack.ID(): "aj-laptop-old",
		phoneTrack.ID():  "aj-phone",
	}
	trackSourceIDs = map[string]string{
		laptopTrack.ID(): "aj-laptop-source",
		phoneTrack.ID():  "aj-phone-source",
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

	// The laptop refreshes: same endpoint, new session. Only the laptop's stale
	// session and tracks should be evicted; the phone endpoint is untouched.
	replaceExistingParticipantSessionEndpoint("AJ", "aj-laptop-new", "endpoint-laptop", nil, nil, "aj@shareability.com")

	listLock.RLock()
	defer listLock.RUnlock()
	if state := activeParticipantConnections[participantConnectionKey("AJ", "endpoint-laptop")]; state.sessionID != "aj-laptop-new" {
		t.Fatalf("laptop endpoint session=%q, want replacement session", state.sessionID)
	}
	if state := activeParticipantConnections[participantConnectionKey("AJ", "endpoint-phone")]; state.sessionID != "aj-phone" {
		t.Fatalf("phone endpoint session=%q, want untouched", state.sessionID)
	}
	if _, ok := trackLocals[laptopTrack.ID()]; ok {
		t.Fatal("stale laptop track remained after the refresh")
	}
	if _, ok := trackLocals[phoneTrack.ID()]; !ok {
		t.Fatal("phone track was removed during the laptop's refresh")
	}
	for _, state := range peerConnections {
		if state.sessionID == "aj-laptop-old" {
			t.Fatal("stale laptop peer connection was not pruned")
		}
	}
	phoneRetained := false
	for _, state := range peerConnections {
		if state.sessionID == "aj-phone" {
			phoneRetained = true
		}
	}
	if !phoneRetained {
		t.Fatal("phone peer connection was pruned during the laptop's refresh")
	}
}
