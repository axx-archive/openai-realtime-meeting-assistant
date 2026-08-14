package main

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRoomAdmissionStartsTranscriptionWithoutSilentlyInvitingScout(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	roomID := "room-agent-boundary"
	app.memory.ensureMeetingID(roomID)
	app.ensureRoomMedia(roomID)
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	invited, realtime := state.scoutInvited, state.realtime
	app.mu.Unlock()
	if invited || realtime != nil {
		t.Fatalf("ordinary room admission launched Scout invited=%t realtime=%v", invited, realtime)
	}
	if participants := app.roomAgentParticipantsSnapshot(roomID); len(participants) != 0 {
		t.Fatalf("ordinary room admission projected agents=%+v", participants)
	}
}

func TestExplicitScoutInviteProjectsParticipantAndAttributesProviderSpeech(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv(roomScoutVoiceModeEnv, "qualified")
	receipt := strings.Repeat("a", 64)
	t.Setenv(roomScoutVoiceQualificationEnv, receipt)
	t.Cleanup(installRoomScoutVoiceQualificationVerifier(func(candidate string) bool { return candidate == receipt }))
	app := newKanbanBoardApp()
	authority := newAmbientConsentAuthorityForTest(t)
	sittingID := grantAmbientConsentForTest(t, app, authority, officeRoomID, "aj@shareability.com")
	if _, _, err := app.admitParticipantWithAnchor(context.Background(), officeRoomID, participantNameForEmail("aj@shareability.com"), "room-agent-session", "room-agent-endpoint", sittingID, memberAdmissionPrincipal("aj@shareability.com")); err != nil {
		t.Fatal(err)
	}
	app.noteMeetingAdmissionForSitting(officeRoomID, participantNameForEmail("aj@shareability.com"), sittingID)
	generation := app.ensureRoomMedia(officeRoomID)
	user := accountStore().findUser("aj@shareability.com")
	participants, err := app.inviteRoomScout(context.Background(), user, officeRoomID)
	if err != nil {
		t.Fatal(err)
	}
	if len(participants) != 1 || participants[0].ID != "scout" || participants[0].InvitationID == "" {
		t.Fatalf("Scout projection=%+v", participants)
	}
	scope := RoomScoutScope{RoomID: officeRoomID, SittingID: sittingID, MediaGeneration: generation}
	epoch := app.recordingEpochForRoom(officeRoomID)
	app.rememberRoomAgentTranscriptForEpoch(scope, kanbanRealtimeEvent{
		EventID: "scout-agent-output-1", Transcript: "I can join as an attributed participant.",
	}, "agent_voice", "gpt-realtime-test", "scout", scoutParticipantName, epoch)
	entries := app.memory.snapshot(0)
	if len(entries) != 1 {
		t.Fatalf("agent transcript entries=%d want 1: %+v", len(entries), entries)
	}
	entry := entries[0]
	if entry.Metadata["speaker"] != scoutParticipantName || entry.Metadata["speakerKind"] != "agent" || entry.Metadata["agentId"] != "scout" || entry.Metadata["mediaGeneration"] != strconv.FormatUint(generation, 10) || !strings.HasPrefix(entry.Text, scoutParticipantName+": ") {
		t.Fatalf("agent transcript attribution=%+v", entry)
	}
	app.setTranscriptRecordingInRoom(officeRoomID, false, "AJ")
	app.setTranscriptRecordingInRoom(officeRoomID, true, "AJ")
	app.rememberRoomAgentTranscriptForEpoch(scope, kanbanRealtimeEvent{
		EventID: "scout-agent-output-stale", Transcript: "This response belonged to the prior recording epoch.",
	}, "agent_voice", "gpt-realtime-test", "scout", scoutParticipantName, epoch)
	if got := len(app.memory.snapshot(0)); got != 1 {
		t.Fatalf("stale agent transcript crossed the recording boundary: entries=%d", got)
	}
	if got := app.dismissRoomScout(officeRoomID, sittingID, "test_complete"); len(got) != 0 || len(app.roomAgentParticipantsSnapshot(officeRoomID)) != 0 {
		t.Fatalf("dismiss left agent participant got=%+v snapshot=%+v", got, app.roomAgentParticipantsSnapshot(officeRoomID))
	}
}

func TestRoomScoutVoiceIsDefaultOffUntilExactQualification(t *testing.T) {
	t.Cleanup(installRoomScoutVoiceQualificationVerifier(nil))
	t.Setenv(roomScoutVoiceModeEnv, "")
	t.Setenv(roomScoutVoiceQualificationEnv, "")
	if gate := currentRoomScoutVoiceAvailability(); gate.Enabled || gate.Reason != "quality_gate_pending" {
		t.Fatalf("default room Scout voice gate=%+v", gate)
	}
	if _, err := (&kanbanBoardApp{}).inviteRoomScout(context.Background(), &userAccount{}, officeRoomID); !errors.Is(err, ErrRoomScoutVoiceDisabled) {
		t.Fatalf("unqualified invite error=%v", err)
	}
	t.Setenv(roomScoutVoiceModeEnv, "qualified")
	t.Setenv(roomScoutVoiceQualificationEnv, strings.Repeat("b", 63))
	if currentRoomScoutVoiceAvailability().Enabled {
		t.Fatal("malformed qualification receipt enabled room voice")
	}
	receipt := strings.Repeat("b", 64)
	t.Setenv(roomScoutVoiceQualificationEnv, receipt)
	if gate := currentRoomScoutVoiceAvailability(); gate.Enabled || gate.Reason != "trusted_qualification_unavailable" {
		t.Fatalf("local configuration enabled room Scout without a trusted verifier: %+v", gate)
	}
	restoreTrusted := installRoomScoutVoiceQualificationVerifier(func(candidate string) bool { return candidate == receipt })
	t.Cleanup(restoreTrusted)
	if gate := currentRoomScoutVoiceAvailability(); !gate.Enabled || gate.Reason != "qualified" {
		t.Fatalf("qualified room Scout voice gate=%+v", gate)
	}
	restoreTrusted()
	if gate := currentRoomScoutVoiceAvailability(); gate.Enabled || gate.Reason != "trusted_qualification_unavailable" {
		t.Fatalf("revoked trusted verifier left room Scout voice enabled: %+v", gate)
	}
	t.Cleanup(installRoomScoutVoiceQualificationVerifier(func(string) bool { return false }))
	if gate := currentRoomScoutVoiceAvailability(); gate.Enabled || gate.Reason != "qualification_not_current" {
		t.Fatalf("stale trusted receipt enabled room Scout voice: %+v", gate)
	}
}
