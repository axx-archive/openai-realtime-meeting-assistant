package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurrentMeetingTranscriptPublicationIsExactRoomSittingAndGeneration(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	roomID := "private-room"
	sittingID := store.ensureMeetingID(roomID)
	app := &kanbanBoardApp{memory: store, roomLive: map[string]*roomLiveState{}}
	app.mu.Lock()
	app.roomLiveLocked(roomID).mediaGen = 7
	app.mu.Unlock()

	listLock.Lock()
	previousPeers := peerConnections
	peerConnections = []peerConnectionState{
		{sessionID: "current", roomID: roomID, sittingID: sittingID, mediaGeneration: 7, websocket: mediaSoakTestWriter(t)},
		{sessionID: "stale-generation", roomID: roomID, sittingID: sittingID, mediaGeneration: 6, websocket: mediaSoakTestWriter(t)},
		{sessionID: "stale-sitting", roomID: roomID, sittingID: "prior-sitting", mediaGeneration: 7, websocket: mediaSoakTestWriter(t)},
		{sessionID: "office", roomID: officeRoomID, sittingID: sittingID, mediaGeneration: 7, websocket: mediaSoakTestWriter(t)},
		{sessionID: "other-room", roomID: "other-room", sittingID: sittingID, mediaGeneration: 7, websocket: mediaSoakTestWriter(t)},
	}
	listLock.Unlock()
	t.Cleanup(func() {
		listLock.Lock()
		peerConnections = previousPeers
		listLock.Unlock()
	})

	entry := meetingMemoryEntry{
		ID:   "transcript-current",
		Kind: meetingMemoryKindTranscript,
		Text: "Private launch details.",
		Metadata: map[string]string{
			"meetingId":       sittingID,
			"mediaGeneration": "7",
		},
	}
	publication, ok := app.broadcastCurrentMeetingTranscript(roomID, entry)
	if !ok || !publication.Scope.same(RoomScoutScope{RoomID: roomID, SittingID: sittingID, MediaGeneration: 7}) {
		t.Fatalf("current transcript publication refused or widened: ok=%t scope=%+v", ok, publication.Scope)
	}
	assertExactTranscriptRecipients := func(event string, acknowledgements []scopedRoomDeliveryAcknowledgement) {
		t.Helper()
		bySession := map[string]scopedRoomDeliveryAcknowledgement{}
		for _, acknowledgement := range acknowledgements {
			bySession[acknowledgement.SessionID] = acknowledgement
		}
		if current := bySession["current"]; !current.Authorized || !current.Delivered {
			t.Fatalf("%s missed exact current recipient: %+v", event, current)
		}
		for _, sessionID := range []string{"stale-generation", "stale-sitting", "office", "other-room"} {
			if acknowledgement := bySession[sessionID]; acknowledgement.Authorized || acknowledgement.Delivered {
				t.Fatalf("%s leaked to %s: %+v", event, sessionID, acknowledgement)
			}
		}
	}
	assertExactTranscriptRecipients("assistant_event", publication.AssistantEvent)
	assertExactTranscriptRecipients("memory_transcript", publication.MemoryTranscriptEvent)

	stale := entry
	stale.ID = "transcript-stale"
	stale.Metadata = map[string]string{"meetingId": sittingID, "mediaGeneration": "6"}
	if rejected, accepted := app.broadcastCurrentMeetingTranscript(roomID, stale); accepted || len(rejected.AssistantEvent) != 0 || len(rejected.MemoryTranscriptEvent) != 0 {
		t.Fatalf("stale transcript was published: accepted=%t publication=%+v", accepted, rejected)
	}
	store.rotateMeetingID(roomID)
	successorSittingID := store.ensureMeetingID(roomID)
	if successorSittingID == sittingID {
		t.Fatal("test setup did not rotate the sitting")
	}
	if rejected, accepted := app.broadcastCurrentMeetingTranscript(roomID, entry); accepted || len(rejected.AssistantEvent) != 0 || len(rejected.MemoryTranscriptEvent) != 0 {
		t.Fatalf("prior-sitting transcript was published into successor: accepted=%t publication=%+v", accepted, rejected)
	}
}

func TestSpokenAndAgentTranscriptPathsNeverUseOfficeWideBodyBroadcast(t *testing.T) {
	for _, path := range []string{"kanban.go", "room_agents.go"} {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(source), `broadcastAssistantEvent("transcript"`) {
			t.Fatalf("%s still routes transcript bodies through the office-wide assistant broadcast", path)
		}
	}
}

func TestTypedTranscriptRejectsStaleMediaGenerationBeforePersistence(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	roomID := "typed-private-room"
	sittingID := store.ensureMeetingID(roomID)
	app := &kanbanBoardApp{memory: store, roomLive: map[string]*roomLiveState{}}
	app.mu.Lock()
	app.roomLiveLocked(roomID).mediaGen = 8
	app.roomLiveLocked(roomID).mediaSittingID = sittingID
	app.mu.Unlock()
	current := RoomScoutScope{RoomID: roomID, SittingID: sittingID, MediaGeneration: 8}
	if _, ok := app.recordRoomChatMessageForScope(current, "AJ", "Current typed work stays in this sitting.", nil); !ok {
		t.Fatal("current typed transcript was refused")
	}
	before := len(store.snapshotForMeeting(sittingID, 0))
	stale := RoomScoutScope{RoomID: roomID, SittingID: sittingID, MediaGeneration: 7}
	if _, ok := app.recordRoomChatMessageForScope(stale, "AJ", "This stale body must never persist.", nil); ok {
		t.Fatal("stale-generation typed transcript was accepted")
	}
	afterEntries := store.snapshotForMeeting(sittingID, 0)
	if len(afterEntries) != before {
		t.Fatalf("stale typed transcript changed durable memory: before=%d after=%d", before, len(afterEntries))
	}
	if len(afterEntries) != 1 || afterEntries[0].Metadata["mediaGeneration"] != "8" || !strings.Contains(afterEntries[0].Text, "Current typed work") {
		t.Fatalf("current typed transcript missing exact generation: %+v", afterEntries)
	}
}
