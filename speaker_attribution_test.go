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
