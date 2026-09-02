package main

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

// Wave 6 D7: a chat-only Scout seat is available while the voice lane stays
// hard off; the voice invite is still refused; the roster carries the mode;
// dismiss clears it; /readyz reports scoutText separately from voice.
func TestRoomScoutTextInviteSeatsChatOnlyScoutWhileVoiceHardOff(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Cleanup(installRoomScoutVoiceQualificationVerifier(nil))
	t.Setenv(roomScoutVoiceModeEnv, "")
	t.Setenv(roomScoutVoiceQualificationEnv, "")
	if currentRoomScoutVoiceAvailability().Enabled {
		t.Fatal("voice gate unexpectedly enabled")
	}
	if text := currentRoomScoutTextAvailability(); !text.Enabled || text.Status != "available" {
		t.Fatalf("text availability=%+v, want available", text)
	}

	app := newKanbanBoardApp()
	authority := newAmbientConsentAuthorityForTest(t)
	sittingID := grantAmbientConsentForTest(t, app, authority, officeRoomID, "aj@shareability.com")
	if _, _, err := app.admitParticipantWithAnchor(context.Background(), officeRoomID, participantNameForEmail("aj@shareability.com"), "room-agent-text-session", "room-agent-text-endpoint", sittingID, memberAdmissionPrincipal("aj@shareability.com")); err != nil {
		t.Fatal(err)
	}
	app.noteMeetingAdmissionForSitting(officeRoomID, participantNameForEmail("aj@shareability.com"), sittingID)
	app.ensureRoomMedia(officeRoomID)
	user := accountStore().findUser("aj@shareability.com")

	if _, err := app.inviteRoomScout(context.Background(), user, officeRoomID); !errors.Is(err, ErrRoomScoutVoiceDisabled) {
		t.Fatalf("voice invite with voice hard off error=%v, want %v", err, ErrRoomScoutVoiceDisabled)
	}
	if _, err := app.inviteRoomScoutWithMode(context.Background(), user, officeRoomID, "hologram"); !errors.Is(err, ErrRoomScoutModeInvalid) {
		t.Fatalf("unknown mode error=%v, want %v", err, ErrRoomScoutModeInvalid)
	}
	participants, err := app.inviteRoomScoutWithMode(context.Background(), user, officeRoomID, "text")
	if err != nil {
		t.Fatalf("text invite with voice hard off: %v", err)
	}
	if len(participants) != 1 || participants[0].ID != "scout" || participants[0].Mode != roomScoutModeText || participants[0].VoiceState != "off" || participants[0].Status != string(RoomScoutReady) || participants[0].ProviderSessionStarted || participants[0].InvitationID == "" {
		t.Fatalf("text seat projection=%+v", participants)
	}
	if roster := app.roomAgentParticipantsSnapshot(officeRoomID); len(roster) != 1 || roster[0].Mode != roomScoutModeText {
		t.Fatalf("roster snapshot=%+v, want Scout with mode text", roster)
	}
	if mode := app.roomScoutModeSnapshot(officeRoomID); mode != roomScoutModeText {
		t.Fatalf("scout mode snapshot=%q, want text", mode)
	}
	// No realtime lane, no provider audio: the office peer never started.
	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	realtime, pc, starting := state.realtime, app.pc, app.realtimeStarting
	app.mu.Unlock()
	if realtime != nil || pc != nil || starting {
		t.Fatalf("text seat started a voice lane realtime=%v pc=%v starting=%t", realtime, pc, starting)
	}
	// A repeated text invite is idempotent; a voice invite is still refused.
	if again, err := app.inviteRoomScoutWithMode(context.Background(), user, officeRoomID, "text"); err != nil || len(again) != 1 || again[0].InvitationID != participants[0].InvitationID {
		t.Fatalf("repeat text invite err=%v participants=%+v", err, again)
	}
	if _, err := app.inviteRoomScout(context.Background(), user, officeRoomID); !errors.Is(err, ErrRoomScoutVoiceDisabled) {
		t.Fatalf("voice invite after text seat error=%v, want %v", err, ErrRoomScoutVoiceDisabled)
	}

	rows, degraded := roomOperationalCapabilityRows(app, time.Now(), true, nil)
	var officeRow map[string]any
	for _, row := range rows {
		if asString(row["roomId"]) == officeRoomID {
			officeRow = row
		}
	}
	if officeRow == nil {
		t.Fatalf("no office capability row in %+v", rows)
	}
	scoutText, _ := officeRow["scoutText"].(map[string]any)
	if scoutText["status"] != "available" || scoutText["invited"] != true || scoutText["enabled"] != true {
		t.Fatalf("readyz scoutText=%+v, want available+invited", scoutText)
	}
	scout, _ := officeRow["scout"].(map[string]any)
	if scout["mode"] != roomScoutModeText || scout["invited"] != true || scout["voiceSeat"] != false {
		t.Fatalf("readyz scout row=%+v, want mode text without a voice seat", scout)
	}
	// The voice lane is reported on its own terms: never relabeled by the
	// text seat.
	if scout["status"] == "healthy" || scout["connected"] == true {
		t.Fatalf("text seat relabeled the voice lane healthy: %+v", scout)
	}
	_ = degraded

	if got := app.dismissRoomScout(officeRoomID, sittingID, "test_complete"); len(got) != 0 || len(app.roomAgentParticipantsSnapshot(officeRoomID)) != 0 || app.roomScoutModeSnapshot(officeRoomID) != "" {
		t.Fatalf("dismiss left the text seat got=%+v snapshot=%+v mode=%q", got, app.roomAgentParticipantsSnapshot(officeRoomID), app.roomScoutModeSnapshot(officeRoomID))
	}
	rows, _ = roomOperationalCapabilityRows(app, time.Now(), true, nil)
	for _, row := range rows {
		if asString(row["roomId"]) != officeRoomID {
			continue
		}
		if scoutText, _ := row["scoutText"].(map[string]any); scoutText["invited"] != false || scoutText["status"] != "available" {
			t.Fatalf("readyz scoutText after dismiss=%+v", scoutText)
		}
	}
}

// A voice invite that follows a text seat upgrades it in place (same
// invitation) once voice is qualified; the seat mode flips to voice.
func TestRoomScoutTextSeatUpgradesToVoiceWhenQualified(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv(roomScoutVoiceModeEnv, "")
	t.Setenv(roomScoutVoiceQualificationEnv, "")
	t.Cleanup(installRoomScoutVoiceQualificationVerifier(nil))
	app := newKanbanBoardApp()
	authority := newAmbientConsentAuthorityForTest(t)
	sittingID := grantAmbientConsentForTest(t, app, authority, officeRoomID, "aj@shareability.com")
	if _, _, err := app.admitParticipantWithAnchor(context.Background(), officeRoomID, participantNameForEmail("aj@shareability.com"), "room-agent-upgrade-session", "room-agent-upgrade-endpoint", sittingID, memberAdmissionPrincipal("aj@shareability.com")); err != nil {
		t.Fatal(err)
	}
	app.noteMeetingAdmissionForSitting(officeRoomID, participantNameForEmail("aj@shareability.com"), sittingID)
	app.ensureRoomMedia(officeRoomID)
	user := accountStore().findUser("aj@shareability.com")
	text, err := app.inviteRoomScoutWithMode(context.Background(), user, officeRoomID, "text")
	if err != nil {
		t.Fatal(err)
	}
	receipt := strings.Repeat("c", 64)
	t.Setenv(roomScoutVoiceModeEnv, "qualified")
	t.Setenv(roomScoutVoiceQualificationEnv, receipt)
	t.Cleanup(installRoomScoutVoiceQualificationVerifier(func(candidate string) bool { return candidate == receipt }))
	voice, err := app.inviteRoomScout(context.Background(), user, officeRoomID)
	if err != nil {
		t.Fatalf("qualified voice invite after text seat: %v", err)
	}
	if len(voice) != 1 || voice[0].Mode != roomScoutModeVoice || voice[0].InvitationID != text[0].InvitationID || voice[0].VoiceState != "starting" {
		t.Fatalf("upgraded seat=%+v (text was %+v)", voice, text)
	}
	if got := app.dismissRoomScout(officeRoomID, sittingID, "test_complete"); len(got) != 0 || len(app.roomAgentParticipantsSnapshot(officeRoomID)) != 0 {
		t.Fatalf("dismiss left the upgraded seat got=%+v", got)
	}
}
