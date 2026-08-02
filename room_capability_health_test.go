package main

import (
	"errors"
	"testing"
	"time"
)

func TestRoomOperationalCapabilityRowsAreRoomScopedAndKeepHumanMediaIndependent(t *testing.T) {
	now := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	laneA := newMeetingTranscriptionLaneForRoomGeneration(nil, "key", "gpt-realtime-whisper", "room-a", 3)
	laneA.setConnected(true)
	bundleA, err := newRoomRealtimeBundle(RoomScoutScope{RoomID: "room-a", SittingID: "sitting-a", MediaGeneration: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	bundleA.mu.Lock()
	bundleA.status = RoomScoutDegraded
	bundleA.lastError = "provider failed"
	bundleA.mu.Unlock()
	store := &meetingMemoryStore{entries: []meetingMemoryEntry{
		{ID: "tx-a", Kind: meetingMemoryKindTranscript, Text: "room a", CreatedAt: now.Add(-time.Minute), Metadata: map[string]string{"roomId": "room-a", "meetingId": "sitting-a"}},
		{ID: "brain-b", Kind: meetingMemoryKindBrain, Text: "room b only", CreatedAt: now, Metadata: map[string]string{"roomId": "room-b", "meetingId": "sitting-b"}},
	}}
	app := &kanbanBoardApp{memory: store, roomLive: map[string]*roomLiveState{
		"room-a": {id: "room-a", participants: map[string]time.Time{"AJ": now}, recordingEnabled: true, mediaActor: &roomMediaActor{}, mixer: &audioMixer{}, lane: laneA, realtime: bundleA, mediaGen: 3, mediaSittingID: "sitting-a"},
		"room-b": {id: "room-b", participants: map[string]time.Time{}, mediaGen: 1, mediaSittingID: "sitting-b"},
	}}
	rows, degraded := roomOperationalCapabilityRows(app, now, true, nil)
	if len(rows) != 2 || asString(rows[0]["roomId"]) != "room-a" || asString(rows[1]["roomId"]) != "room-b" {
		t.Fatalf("rows=%+v", rows)
	}
	roomA := rows[0]
	if roomA["media"].(map[string]any)["status"] != "healthy" {
		t.Fatalf("AI failure incorrectly degraded human media: %+v", roomA)
	}
	if roomA["scout"].(map[string]any)["status"] != string(RoomScoutDegraded) || roomA["transcript"].(map[string]any)["status"] != "fresh" {
		t.Fatalf("room A capability truth=%+v", roomA)
	}
	if roomA["analysis"].(map[string]any)["status"] != "missing" {
		t.Fatal("room B brain evidence leaked into room A")
	}
	if len(degraded) != 1 || degraded[0] != "rooms.room-a.scout" {
		t.Fatalf("degraded=%v", degraded)
	}
}

func TestRoomOperationalCapabilityRowsExposeConsentAndActiveSTTFailureWithoutSecrets(t *testing.T) {
	now := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	app := &kanbanBoardApp{memory: &meetingMemoryStore{}, roomLive: map[string]*roomLiveState{
		"room-a": {id: "room-a", participants: map[string]time.Time{"AJ": now}, recordingEnabled: true, mediaActor: &roomMediaActor{}, mixer: &audioMixer{}, mediaGen: 2, mediaSittingID: "sitting-a"},
	}}
	rows, degraded := roomOperationalCapabilityRows(app, now, false, errors.New("postgres connection secret host unavailable"))
	if len(rows) != 1 {
		t.Fatalf("rows=%v", rows)
	}
	row := rows[0]
	if row["stt"].(map[string]any)["status"] != "degraded" || row["consent"].(map[string]any)["status"] != "degraded" || row["media"].(map[string]any)["status"] != "healthy" {
		t.Fatalf("row=%+v", row)
	}
	if len(degraded) != 3 {
		t.Fatalf("degraded=%v", degraded)
	}
	redactCapabilityErrors(row)
	if _, leaked := row["consent"].(map[string]any)["lastError"]; leaked {
		t.Fatal("public capability row leaked consent error")
	}
}
