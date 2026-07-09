package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSpeakerForCompletedTranscriptUsesDominantParticipantAudio(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	now := time.Now().UTC()
	app.mu.Lock()
	app.currentSpeechStartedAt = now
	app.currentSpeechStoppedAt = now.Add(700 * time.Millisecond)
	app.mu.Unlock()

	app.NoteAudioActivity(now.Add(100*time.Millisecond), []audioActivityLevel{
		{ParticipantName: "Tom", RMS: 1800},
		{ParticipantName: "Tyler", RMS: 400},
	})
	app.NoteAudioActivity(now.Add(300*time.Millisecond), []audioActivityLevel{
		{ParticipantName: "Tom", RMS: 1600},
	})

	speaker, confidence := app.speakerForCompletedTranscript(now.Add(time.Second))
	if speaker != "Tom" {
		t.Fatalf("speaker=%q, want Tom", speaker)
	}
	if confidence != "dominant" {
		t.Fatalf("confidence=%q, want dominant", confidence)
	}
}

func TestFrozenAttributionWindowSurvivesRapidHandoff(t *testing.T) {
	// A6: A speaks and their turn commits; B starts immediately and overwrites the
	// shared speech markers before A's transcription.completed lands. The frozen
	// window must still attribute A's committed text to A, not B.
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	base := time.Now().UTC()

	// A's (Tom's) turn: set the live markers, record Tom-dominant audio, freeze.
	app.mu.Lock()
	app.currentSpeechStartedAt = base
	app.currentSpeechStoppedAt = base.Add(600 * time.Millisecond)
	app.mu.Unlock()
	app.NoteAudioActivity(base.Add(100*time.Millisecond), []audioActivityLevel{
		{ParticipantName: "Tom", RMS: 1800},
		{ParticipantName: "Tyler", RMS: 200},
	})
	app.freezeAttributionWindowAtCommit()

	// B's (Tyler's) turn overwrites the shared markers while A's completed is still
	// in flight; Tyler-dominant audio lands well outside A's frozen window.
	app.mu.Lock()
	app.currentSpeechStartedAt = base.Add(800 * time.Millisecond)
	app.currentSpeechStoppedAt = base.Add(1400 * time.Millisecond)
	app.mu.Unlock()
	// base+1400ms clears Tom's padded window (stop base+600ms + 650ms padding =
	// base+1250ms) yet sits inside Tyler's live window.
	app.NoteAudioActivity(base.Add(1400*time.Millisecond), []audioActivityLevel{
		{ParticipantName: "Tyler", RMS: 1800},
		{ParticipantName: "Tom", RMS: 200},
	})

	// A's completed returns now: the frozen window must resolve Tom.
	speaker, confidence := app.speakerForCommittedTranscript(base.Add(1500 * time.Millisecond))
	if speaker != "Tom" {
		t.Fatalf("committed speaker=%q confidence=%q, want Tom (frozen window)", speaker, confidence)
	}

	// The FIFO is now empty: a completed with no frozen window falls back to the
	// live markers, which point at Tyler's turn.
	speaker, _ = app.speakerForCommittedTranscript(base.Add(1600 * time.Millisecond))
	if speaker != "Tyler" {
		t.Fatalf("fallback speaker=%q, want Tyler (live markers)", speaker)
	}
}

func TestDominantTranscriptSpeakerReportsMixedSpeakers(t *testing.T) {
	speaker, confidence := dominantTranscriptSpeaker(map[string]float64{
		"Tom":   1000,
		"Tyler": 900,
	})
	if speaker != "Tom + Tyler" {
		t.Fatalf("speaker=%q, want mixed speaker label", speaker)
	}
	if confidence != "mixed" {
		t.Fatalf("confidence=%q, want mixed", confidence)
	}
}

func TestActiveSpeakerSnapshotRequiresStableInRoomAudio(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	now := time.Now().UTC()
	app.participants["Tom"] = now
	app.participants["Caitlyn"] = now

	app.NoteAudioActivity(now, []audioActivityLevel{
		{ParticipantName: "Tom", RMS: 2200},
		{ParticipantName: "Caitlyn", RMS: 500},
	})
	if snapshot := app.activeSpeakerSnapshot(); snapshot != nil {
		t.Fatalf("active speaker promoted before stability window: %+v", snapshot)
	}

	app.NoteAudioActivity(now.Add(activeSpeakerStabilityWindow+50*time.Millisecond), []audioActivityLevel{
		{ParticipantName: "Tom", RMS: 2100},
		{ParticipantName: "Caitlyn", RMS: 450},
	})
	snapshot := app.activeSpeakerSnapshot()
	if snapshot == nil {
		t.Fatal("expected stable active speaker snapshot")
	}
	if snapshot.Name != "Tom" {
		t.Fatalf("active speaker=%q, want Tom", snapshot.Name)
	}
	if snapshot.Source != "server" {
		t.Fatalf("source=%q, want server", snapshot.Source)
	}
	if snapshot.Level <= 0 || snapshot.Confidence <= 0.5 {
		t.Fatalf("unexpected active speaker level/confidence: %+v", snapshot)
	}
}

func TestActiveSpeakerIgnoresMutedAndDepartedParticipants(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	now := time.Now().UTC()
	app.participants["Tom"] = now
	app.participants["Caitlyn"] = now
	app.participantMedia["Tom"] = participantMediaState{MicMuted: true}

	app.NoteAudioActivity(now, []audioActivityLevel{
		{ParticipantName: "Tom", RMS: 4000},
		{ParticipantName: "Caitlyn", RMS: 900},
	})
	app.NoteAudioActivity(now.Add(activeSpeakerStabilityWindow+50*time.Millisecond), []audioActivityLevel{
		{ParticipantName: "Tom", RMS: 4000},
		{ParticipantName: "Caitlyn", RMS: 900},
	})
	if snapshot := app.activeSpeakerSnapshot(); snapshot == nil || snapshot.Name != "Caitlyn" {
		t.Fatalf("muted Tom should be ignored, got %+v", snapshot)
	}

	delete(app.participants, "Caitlyn")
	if snapshot := app.activeSpeakerSnapshot(); snapshot != nil {
		t.Fatalf("departed active speaker should not be reported: %+v", snapshot)
	}
}
