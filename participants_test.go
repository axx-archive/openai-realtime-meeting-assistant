package main

import (
	"path/filepath"
	"strings"
	"testing"
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
