package main

import (
	"sync"
	"testing"
)

func TestRoomRecordingSnapshotCarriesAuthoritativeRevisionAndAvailability(t *testing.T) {
	t.Setenv("MEETING_TRANSCRIPT_LANE_ENABLED", "1")
	app := newIsolatedKanbanBoardApp(t)

	initial, ok := app.roomSnapshotForRoom(officeRoomID)["recording"].(roomRecordingState)
	if !ok {
		t.Fatal("recording snapshot is missing")
	}
	if initial.Revision == 0 || initial.StatusRevision == 0 {
		t.Fatal("recording and status revisions must start above zero")
	}
	if initial.Available || initial.Connected {
		t.Fatal("recording must be unavailable and disconnected until the room owns a transcript lane")
	}

	lane := &meetingTranscriptionLane{}
	app.mu.Lock()
	app.transcriptLane = lane
	app.mu.Unlock()
	armed := app.roomSnapshotForRoom(officeRoomID)["recording"].(roomRecordingState)
	if !armed.Available || armed.Connected || armed.Revision != initial.Revision || armed.StatusRevision != initial.StatusRevision || !armed.Enabled {
		t.Fatalf("armed recording=%+v, want available at unchanged enabled revision", armed)
	}
	lane.mu.Lock()
	lane.connected = true
	lane.mu.Unlock()
	connected := app.roomSnapshotForRoom(officeRoomID)["recording"].(roomRecordingState)
	if !connected.Connected || connected.Revision != armed.Revision || connected.StatusRevision != armed.StatusRevision {
		t.Fatalf("connected recording=%+v, want same recording revision with provider connected", connected)
	}

	paused := app.setTranscriptRecordingInRoom(officeRoomID, false, "AJ")["recording"].(roomRecordingState)
	if paused.Enabled || !paused.Available || !paused.Connected || paused.Revision <= armed.Revision || paused.StatusRevision <= armed.StatusRevision || paused.UpdatedAt == "" || paused.UpdatedBy != "AJ" {
		t.Fatalf("paused recording=%+v, want confirmed paused revision", paused)
	}
	duplicate := app.setTranscriptRecordingInRoom(officeRoomID, false, "AJ")["recording"].(roomRecordingState)
	if duplicate.Revision != paused.Revision || duplicate.StatusRevision != paused.StatusRevision || duplicate.UpdatedAt != paused.UpdatedAt {
		t.Fatalf("idempotent pause advanced authority: before=%+v after=%+v", paused, duplicate)
	}
	resumed := app.setTranscriptRecordingInRoom(officeRoomID, true, "AJ")["recording"].(roomRecordingState)
	if !resumed.Enabled || resumed.Revision <= paused.Revision || resumed.StatusRevision <= paused.StatusRevision {
		t.Fatalf("resumed recording=%+v, want later enabled revision", resumed)
	}
}

func TestConcurrentRoomRecordingUpdatesPublishOneMonotonicAuthority(t *testing.T) {
	t.Setenv("MEETING_TRANSCRIPT_LANE_ENABLED", "1")
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.transcriptLane = &meetingTranscriptionLane{}
	app.mu.Unlock()

	start := app.roomSnapshotForRoom(officeRoomID)["recording"].(roomRecordingState)
	var wg sync.WaitGroup
	for index := 0; index < 32; index++ {
		wg.Add(1)
		go func(enabled bool) {
			defer wg.Done()
			app.setTranscriptRecordingInRoom(officeRoomID, enabled, "AJ")
		}(index%2 == 0)
	}
	wg.Wait()

	final := app.roomSnapshotForRoom(officeRoomID)["recording"].(roomRecordingState)
	if final.Revision < start.Revision || final.Revision == 0 || final.StatusRevision < start.StatusRevision || final.StatusRevision == 0 || !final.Available {
		t.Fatalf("final recording authority regressed: start=%+v final=%+v", start, final)
	}
}

func TestRoomRecordingConnectionTracksSourceLaneEdges(t *testing.T) {
	t.Setenv("MEETING_TRANSCRIPT_LANE_ENABLED", "1")
	app := newIsolatedKanbanBoardApp(t)
	manager := newMeetingTranscriptionSourceManagerForRoomGeneration(app, "test-key", transcriptionLaneModel(), officeRoomID, 0)
	child := newMeetingTranscriptionLaneForRoomGeneration(app, "test-key", transcriptionLaneModel(), officeRoomID, 0)
	manager.sourceMu.Lock()
	manager.sourceLanes["track-aj"] = child
	manager.sourceMu.Unlock()
	app.mu.Lock()
	app.transcriptLane = manager
	app.mu.Unlock()

	armed := app.roomSnapshotForRoom(officeRoomID)["recording"].(roomRecordingState)
	if !armed.Available || armed.Connected {
		t.Fatalf("armed recording=%+v, want allocated but not connected", armed)
	}
	child.setConnected(true)
	live := app.roomSnapshotForRoom(officeRoomID)["recording"].(roomRecordingState)
	if !live.Connected || live.Revision != armed.Revision || live.StatusRevision <= armed.StatusRevision {
		t.Fatalf("live recording=%+v, want connected refinement at the same revision", live)
	}
	child.setConnected(false)
	reconnecting := app.roomSnapshotForRoom(officeRoomID)["recording"].(roomRecordingState)
	if reconnecting.Connected || reconnecting.Revision != armed.Revision || reconnecting.StatusRevision <= live.StatusRevision {
		t.Fatalf("reconnecting recording=%+v, want disconnected refinement at the same revision", reconnecting)
	}
}

func TestTranscriptionConnectionStatusRevisionsStayUniqueUnderConcurrency(t *testing.T) {
	t.Setenv("MEETING_TRANSCRIPT_LANE_ENABLED", "1")
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.transcriptLane = &meetingTranscriptionLane{}
	app.mu.Unlock()

	start := app.roomSnapshotForRoom(officeRoomID)["recording"].(roomRecordingState).StatusRevision
	const edges = 64
	revisions := make(chan uint64, edges)
	var wg sync.WaitGroup
	for index := 0; index < edges; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snapshot := app.roomSnapshotForTranscriptionConnectionEdge(officeRoomID)
			revisions <- snapshot["recording"].(roomRecordingState).StatusRevision
		}()
	}
	wg.Wait()
	close(revisions)

	seen := make(map[uint64]struct{}, edges)
	for revision := range revisions {
		if revision <= start {
			t.Fatalf("connection authority did not advance: start=%d got=%d", start, revision)
		}
		if _, duplicate := seen[revision]; duplicate {
			t.Fatalf("two concurrent connection edges reused status revision %d", revision)
		}
		seen[revision] = struct{}{}
	}
	if len(seen) != edges {
		t.Fatalf("status revisions=%d, want %d", len(seen), edges)
	}
	final := app.roomSnapshotForRoom(officeRoomID)["recording"].(roomRecordingState)
	if final.StatusRevision != start+edges {
		t.Fatalf("final status revision=%d, want %d", final.StatusRevision, start+edges)
	}
}
