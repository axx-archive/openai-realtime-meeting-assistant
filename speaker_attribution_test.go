package main

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestBlockedActiveSpeakerBroadcastDoesNotLoseAttributionFrames(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	base := time.Now().UTC()
	app.mu.Lock()
	app.roomLive[officeRoomID].participants["Tom"] = base
	app.roomLive[officeRoomID].participants["Tyler"] = base
	app.mu.Unlock()

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	delivered := make(chan string, 2)
	var first atomic.Bool
	app.activeSpeakerPublishMu.Lock()
	app.activeSpeakerPublishDeliver = func(publication activeSpeakerPublication) {
		if first.CompareAndSwap(false, true) {
			started <- struct{}{}
			<-release
		}
		delivered <- publication.payload.Name
	}
	app.activeSpeakerPublishMu.Unlock()

	// Promote Tom and block only the derived UI publication.
	app.NoteAudioActivity(base, []audioActivityLevel{{ParticipantName: "Tom", RMS: 2200}})
	app.NoteAudioActivity(base.Add(activeSpeakerStabilityWindow+50*time.Millisecond), []audioActivityLevel{{ParticipantName: "Tom", RMS: 2100}})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("active-speaker publication did not reach the blocking seam")
	}

	// Tyler takes over while the UI publisher is blocked. Every 20ms-class
	// attribution frame must still be ingested synchronously.
	turnStart := base.Add(time.Second)
	turnStop := turnStart.Add(900 * time.Millisecond)
	app.mu.Lock()
	app.roomLive[officeRoomID].currentSpeechStartedAt = turnStart
	app.roomLive[officeRoomID].currentSpeechStoppedAt = turnStop
	app.mu.Unlock()
	callStarted := time.Now()
	for index := 0; index < 10; index++ {
		app.NoteAudioActivity(turnStart.Add(time.Duration(index)*100*time.Millisecond), []audioActivityLevel{
			{ParticipantName: "Tyler", RMS: 1800},
			{ParticipantName: "Tom", RMS: 120},
		})
	}
	if elapsed := time.Since(callStarted); elapsed > 50*time.Millisecond {
		t.Fatalf("local attribution waited %s for blocked UI publication", elapsed)
	}

	speaker, confidence := app.speakerForCompletedTranscript(turnStop.Add(100 * time.Millisecond))
	if speaker != "Tyler" || confidence != "dominant" {
		t.Fatalf("speaker=%q confidence=%q, want Tyler/dominant after blocked publication", speaker, confidence)
	}

	close(release)
	select {
	case name := <-delivered:
		if name != "Tom" {
			t.Fatalf("first publication=%q, want Tom", name)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked publication did not release")
	}
	select {
	case name := <-delivered:
		if name != "Tyler" {
			t.Fatalf("latest publication=%q, want Tyler", name)
		}
	case <-time.After(time.Second):
		t.Fatal("latest active speaker was not published")
	}
}

func TestSpeakerForCompletedTranscriptUsesDominantParticipantAudio(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	now := time.Now().UTC()
	app.mu.Lock()
	app.roomLive[officeRoomID].currentSpeechStartedAt = now
	app.roomLive[officeRoomID].currentSpeechStoppedAt = now.Add(700 * time.Millisecond)
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
	app.roomLive[officeRoomID].currentSpeechStartedAt = base
	app.roomLive[officeRoomID].currentSpeechStoppedAt = base.Add(600 * time.Millisecond)
	app.mu.Unlock()
	app.NoteAudioActivity(base.Add(100*time.Millisecond), []audioActivityLevel{
		{ParticipantName: "Tom", RMS: 1800},
		{ParticipantName: "Tyler", RMS: 200},
	})
	app.freezeAttributionWindowAtCommit()

	// B's (Tyler's) turn overwrites the shared markers while A's completed is still
	// in flight; Tyler-dominant audio lands well outside A's frozen window.
	app.mu.Lock()
	app.roomLive[officeRoomID].currentSpeechStartedAt = base.Add(800 * time.Millisecond)
	app.roomLive[officeRoomID].currentSpeechStoppedAt = base.Add(1400 * time.Millisecond)
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
	app.roomLive[officeRoomID].participants["Tom"] = now
	app.roomLive[officeRoomID].participants["Caitlyn"] = now

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
	app.roomLive[officeRoomID].participants["Tom"] = now
	app.roomLive[officeRoomID].participants["Caitlyn"] = now
	app.roomLive[officeRoomID].participantMedia["Tom"] = participantMediaState{MicMuted: true}

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

	delete(app.roomLive[officeRoomID].participants, "Caitlyn")
	if snapshot := app.activeSpeakerSnapshot(); snapshot != nil {
		t.Fatalf("departed active speaker should not be reported: %+v", snapshot)
	}
}
