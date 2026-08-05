package main

import (
	"strings"
	"testing"
	"time"
)

func TestRoomScoutContextKeepsPeopleAttributedAndPrivateProfilesOut(t *testing.T) {
	setupAuthTestEnv(t)
	app := newW2ATestApp(t)
	t.Cleanup(func() { _ = app.Close() })
	roomID := "room-people1111"
	sittingID := app.memory.ensureMeetingID(roomID)
	scope := RoomScoutScope{RoomID: roomID, SittingID: sittingID, MediaGeneration: 7}
	bundle, err := newRoomRealtimeBundle(scope, func(string, any) {})
	if err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	state.mediaSittingID = sittingID
	state.mediaGen = scope.MediaGeneration
	state.participants["AJ"] = time.Now().UTC()
	state.participantCounts["AJ"] = 1
	state.participants["Tom"] = time.Now().UTC()
	state.participantCounts["Tom"] = 1
	state.activeSpeakerName = "AJ"
	state.scoutInvited = true
	state.realtime = bundle
	app.mu.Unlock()

	query := app.prepareSTRIDERoomRequesterModelQuery(scope, "aj@shareability.com", "AJ", "What do you think, Scout?")
	for _, marker := range []string{
		"requester=AJ (user:",
		"room=" + roomID,
		"sitting=" + sittingID,
		"participants=AJ, Tom",
		"speaker-attributed meeting history distinct",
		"Never access, infer from, or reveal private chats, Settings imports, or private user profiles",
	} {
		if !strings.Contains(query, marker) {
			t.Fatalf("room text context missing %q: %s", marker, query)
		}
	}

	instructions := app.roomScoutSessionInstructions(scope)
	for _, marker := range []string{
		"Human roster for this sitting: AJ (user:",
		"Tom (user:",
		"Treat each human as a separate coworker",
		"speaker attribution from the shared meeting transcript",
		"never receives private chats, Settings imports, or private user-profile memory",
	} {
		if !strings.Contains(instructions, marker) {
			t.Fatalf("room voice instructions missing %q: %s", marker, instructions)
		}
	}

	result, _, err := app.applyRoomScoutToolArgs(t.Context(), scope, "answer_room_question", map[string]any{"request": "What is Scout's opinion?"})
	if err != nil || result["requester"] != "AJ" || !strings.Contains(asString(result["memoryBoundary"]), "no private chats") {
		t.Fatalf("room spoken-answer attribution=%v err=%v", result, err)
	}
}
