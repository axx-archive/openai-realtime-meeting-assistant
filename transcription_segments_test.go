package main

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTranscriptionSegmentBindingsResolveOutOfOrderTerminalsByItem(t *testing.T) {
	bindings := newTranscriptionSegmentBindings()
	if err := bindings.Commit("segment-a", 1.25); err != nil {
		t.Fatal(err)
	}
	if err := bindings.Commit("segment-b", 2.5); err != nil {
		t.Fatal(err)
	}
	// The newer provider acknowledgement arrives first. It cannot be mapped by
	// delivery order, so it remains deferred until its predecessor creates the
	// root-anchored item chain.
	if _, err := bindings.BindCommitted("item-b", "item-a"); !errors.Is(err, errTranscriptionSegmentBindingDeferred) {
		t.Fatalf("early child binding err=%v, want deferred", err)
	}
	got, err := bindings.BindCommitted("item-a", "")
	if err != nil || len(got) != 2 || got[0].SegmentID != "segment-a" || got[1].SegmentID != "segment-b" {
		t.Fatalf("root chain binding = %#v, %v", got, err)
	}

	second, err := bindings.Consume("item-b")
	if err != nil || second.SegmentID != "segment-b" || second.AudioSeconds != 2.5 {
		t.Fatalf("consume item-b = %#v, %v", second, err)
	}
	first, err := bindings.Consume("item-a")
	if err != nil || first.SegmentID != "segment-a" || first.AudioSeconds != 1.25 {
		t.Fatalf("consume item-a = %#v, %v", first, err)
	}
	if bindings.Pending() != 0 {
		t.Fatalf("pending=%d, want 0", bindings.Pending())
	}
}

func TestTranscriptionSegmentBindingsAreIdempotentAtBindAndFailClosedAtTerminal(t *testing.T) {
	bindings := newTranscriptionSegmentBindings()
	if err := bindings.Commit("segment-a", 1); err != nil {
		t.Fatal(err)
	}
	firstBound, err := bindings.BindCommitted("item-a", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(firstBound) != 1 {
		t.Fatalf("first bound=%#v", firstBound)
	}
	second, err := bindings.BindCommitted("item-a", "")
	if err != nil || len(second) != 1 || second[0] != firstBound[0] {
		t.Fatalf("duplicate bind = %#v, %v; want %#v", second, err, firstBound)
	}
	if _, err := bindings.Consume("unknown-item"); !errors.Is(err, errTranscriptionSegmentUnknown) {
		t.Fatalf("unknown consume err=%v", err)
	}
	if _, err := bindings.Consume("item-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := bindings.Consume("item-a"); !errors.Is(err, errTranscriptionSegmentAlreadyClosed) {
		t.Fatalf("duplicate terminal err=%v", err)
	}
}

func TestTranscriptionSegmentBindingsResetFencesPriorConnection(t *testing.T) {
	bindings := newTranscriptionSegmentBindings()
	if err := bindings.Commit("segment-old", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := bindings.BindCommitted("item-old", ""); err != nil {
		t.Fatal(err)
	}
	bindings.Reset()
	if bindings.Pending() != 0 {
		t.Fatalf("pending=%d after reset", bindings.Pending())
	}
	if _, err := bindings.Consume("item-old"); !errors.Is(err, errTranscriptionSegmentUnknown) {
		t.Fatalf("old item survived reset: %v", err)
	}
	if err := bindings.Commit("segment-new", 2); err != nil {
		t.Fatal(err)
	}
	if got, err := bindings.BindCommitted("item-new", ""); err != nil || len(got) != 1 || got[0].SegmentID != "segment-new" {
		t.Fatalf("new binding=%#v, err=%v", got, err)
	}
}

func TestTranscriptionSegmentBindingsFailClosedForConflictingBranchAndCycle(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events [][2]string
	}{
		{
			name:   "branch",
			events: [][2]string{{"item-a", ""}, {"item-b", "item-a"}, {"item-c", "item-a"}},
		},
		{
			name:   "cycle",
			events: [][2]string{{"item-a", "item-b"}, {"item-b", "item-a"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bindings := newTranscriptionSegmentBindings()
			for i := range tc.events {
				if err := bindings.Commit("segment-"+string(rune('a'+i)), 1); err != nil {
					t.Fatal(err)
				}
			}
			for index, event := range tc.events {
				_, err := bindings.BindCommitted(event[0], event[1])
				if index < len(tc.events)-1 && tc.name == "cycle" && !errors.Is(err, errTranscriptionSegmentBindingDeferred) {
					t.Fatalf("event %d err=%v, want deferred", index, err)
				}
				if index == len(tc.events)-1 && !errors.Is(err, errTranscriptionSegmentChainInvalid) {
					t.Fatalf("event %d err=%v, want invalid chain", index, err)
				}
			}
			if bindings.Pending() != len(tc.events) {
				t.Fatalf("invalid chain bound a segment: pending=%d", bindings.Pending())
			}
		})
	}
}

func TestTranscriptionSegmentBindingsTerminalBeforeAckAndReconnectFenceFailClosed(t *testing.T) {
	bindings := newTranscriptionSegmentBindings()
	if err := bindings.Commit("segment-a", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := bindings.Consume("item-a"); !errors.Is(err, errTranscriptionSegmentUnknown) {
		t.Fatalf("early terminal err=%v", err)
	}
	if _, err := bindings.BindCommitted("item-a", ""); !errors.Is(err, errTranscriptionSegmentAlreadyClosed) {
		t.Fatalf("terminal-before-ack rebound err=%v", err)
	}
	bindings.Reset()
	if err := bindings.Commit("segment-new", 1); err != nil {
		t.Fatal(err)
	}
	if got, err := bindings.BindCommitted("item-new", ""); err != nil || len(got) != 1 || got[0].SegmentID != "segment-new" {
		t.Fatalf("reconnect binding=%#v err=%v", got, err)
	}
	if _, err := bindings.Consume("item-a"); !errors.Is(err, errTranscriptionSegmentUnknown) {
		t.Fatalf("old terminal crossed reconnect fence: %v", err)
	}
}

func TestTranscriptionSegmentBindingsDeferredChildTerminalCannotBindWhenRootArrives(t *testing.T) {
	bindings := newTranscriptionSegmentBindings()
	for _, segmentID := range []string{"segment-a", "segment-b"} {
		if err := bindings.Commit(segmentID, 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := bindings.BindCommitted("item-b", "item-a"); !errors.Is(err, errTranscriptionSegmentBindingDeferred) {
		t.Fatalf("child ack err=%v, want deferred", err)
	}
	if _, err := bindings.Consume("item-b"); !errors.Is(err, errTranscriptionSegmentUnknown) {
		t.Fatalf("early child terminal err=%v", err)
	}
	if _, err := bindings.BindCommitted("item-a", ""); !errors.Is(err, errTranscriptionSegmentAlreadyClosed) {
		t.Fatalf("root rebound terminal child err=%v", err)
	}
}

func TestTranscriptionSegmentBindingsConcurrentOutOfOrderAcksRemainExact(t *testing.T) {
	// This is deliberately a race-friendly version of the normal out-of-order
	// case. Whichever provider ack wins the scheduler, only the rooted chain
	// may bind the two application-owned segments.
	for iteration := 0; iteration < 64; iteration++ {
		bindings := newTranscriptionSegmentBindings()
		if err := bindings.Commit("segment-a", 1); err != nil {
			t.Fatal(err)
		}
		if err := bindings.Commit("segment-b", 2); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for _, event := range [][2]string{{"item-a", ""}, {"item-b", "item-a"}} {
			event := event
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := bindings.BindCommitted(event[0], event[1])
				if err != nil && !errors.Is(err, errTranscriptionSegmentBindingDeferred) {
					errs <- err
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("iteration %d bind err=%v", iteration, err)
		}
		second, err := bindings.Consume("item-b")
		if err != nil || second.SegmentID != "segment-b" || second.AudioSeconds != 2 {
			t.Fatalf("iteration %d item-b=%#v err=%v", iteration, second, err)
		}
		first, err := bindings.Consume("item-a")
		if err != nil || first.SegmentID != "segment-a" || first.AudioSeconds != 1 {
			t.Fatalf("iteration %d item-a=%#v err=%v", iteration, first, err)
		}
	}
}

func TestTranscriptionLaneBindsOutOfOrderTerminalsToApplicationSegments(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))
	app := newKanbanBoardApp()
	admitMemberWithTranscriptConsentForTest(t, app, officeRoomID, "tom@shareability.com")
	bindings := newTranscriptionSegmentBindings()

	freeze := func(segmentID string, at time.Time) {
		app.noteAudioActivityForRoom(officeRoomID, at, []audioActivityLevel{{ParticipantName: "Tom", RMS: 900}})
		app.mu.Lock()
		state := app.roomLiveLocked(officeRoomID)
		state.currentSpeechStartedAt = at
		state.currentSpeechStoppedAt = at.Add(250 * time.Millisecond)
		app.mu.Unlock()
		app.freezeAttributionWindowAtCommitForScopeWithSegmentAndConsent(RoomScoutScope{RoomID: officeRoomID}, segmentID, nil, nil)
	}
	base := time.Now().UTC().Add(-time.Second)
	freeze("segment-a", base)
	freeze("segment-b", base.Add(500*time.Millisecond))
	if err := bindings.Commit("segment-a", 1.25); err != nil {
		t.Fatal(err)
	}
	if err := bindings.Commit("segment-b", 2.5); err != nil {
		t.Fatal(err)
	}
	// Ack order is deliberately inverted. The provider chain, not socket
	// arrival, maps item-a to segment-a and item-b to segment-b.
	for _, raw := range [][]byte{
		[]byte(`{"type":"input_audio_buffer.committed","item_id":"item-b","previous_item_id":"item-a"}`),
		[]byte(`{"type":"input_audio_buffer.committed","item_id":"item-a","previous_item_id":""}`),
	} {
		if app.handleTranscriptionLaneEventForScopeWithBindings(RoomScoutScope{RoomID: officeRoomID}, raw, "test-model", bindings) {
			t.Fatal("commit acknowledgement requested reconnect")
		}
	}

	completedB := []byte(`{"type":"conversation.item.input_audio_transcription.completed","event_id":"event-b","item_id":"item-b","transcript":"Second bound segment."}`)
	completedA := []byte(`{"type":"conversation.item.input_audio_transcription.completed","event_id":"event-a","item_id":"item-a","transcript":"First bound segment."}`)
	app.handleTranscriptionLaneEventForScopeWithBindings(RoomScoutScope{RoomID: officeRoomID}, completedB, "test-model", bindings)
	app.handleTranscriptionLaneEventForScopeWithBindings(RoomScoutScope{RoomID: officeRoomID}, completedA, "test-model", bindings)

	entries := app.memorySnapshot(10)
	if len(entries) != 2 {
		t.Fatalf("entries=%d, want 2", len(entries))
	}
	if !strings.Contains(entries[0].Text, "Second bound segment") || entries[0].Metadata["segmentId"] != "segment-b" {
		t.Fatalf("first terminal entry=%+v", entries[0])
	}
	if !strings.Contains(entries[1].Text, "First bound segment") || entries[1].Metadata["segmentId"] != "segment-a" {
		t.Fatalf("second terminal entry=%+v", entries[1])
	}

	app.handleTranscriptionLaneEventForScopeWithBindings(RoomScoutScope{RoomID: officeRoomID}, completedB, "test-model", bindings)
	if entries := app.memorySnapshot(10); len(entries) != 2 {
		t.Fatalf("duplicate terminal appended %d entries, want 2", len(entries))
	}
}

func TestTranscriptionLaneRejectsUnknownProviderItemWithoutConsumingAttribution(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	bindings := newTranscriptionSegmentBindings()
	app.mu.Lock()
	app.roomLiveLocked(officeRoomID).pendingAttributionWindows = []attributionWindow{{segmentID: "segment-known", startedAt: time.Now().UTC()}}
	app.mu.Unlock()
	app.handleTranscriptionLaneEventForScopeWithBindings(RoomScoutScope{RoomID: officeRoomID}, []byte(`{"type":"conversation.item.input_audio_transcription.failed","item_id":"item-unknown"}`), "test-model", bindings)
	app.mu.Lock()
	remaining := len(app.roomLiveLocked(officeRoomID).pendingAttributionWindows)
	app.mu.Unlock()
	if remaining != 1 {
		t.Fatalf("unknown item consumed %d attribution windows; want one retained", 1-remaining)
	}
}

func TestTranscriptionLaneRejectsConflictingChainAndTerminalBeforeAckWithoutAttributionLeak(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	bindings := newTranscriptionSegmentBindings()
	base := time.Now().UTC()
	app.mu.Lock()
	app.roomLiveLocked(officeRoomID).pendingAttributionWindows = []attributionWindow{{segmentID: "segment-a", startedAt: base}, {segmentID: "segment-b", startedAt: base}}
	app.mu.Unlock()
	if err := bindings.Commit("segment-a", 1); err != nil {
		t.Fatal(err)
	}
	if err := bindings.Commit("segment-b", 1); err != nil {
		t.Fatal(err)
	}
	// A terminal before its matching ack must not consume an arbitrary frozen
	// attribution window, and a late ack for that terminal may not revive it.
	app.handleTranscriptionLaneEventForScopeWithBindings(RoomScoutScope{RoomID: officeRoomID}, []byte(`{"type":"conversation.item.input_audio_transcription.completed","item_id":"item-a","transcript":"must not persist"}`), "test-model", bindings)
	if reconnect := app.handleTranscriptionLaneEventForScopeWithBindings(RoomScoutScope{RoomID: officeRoomID}, []byte(`{"type":"input_audio_buffer.committed","item_id":"item-a","previous_item_id":""}`), "test-model", bindings); !reconnect {
		t.Fatal("late acknowledgement after terminal did not fence the corrupted connection")
	}
	app.mu.Lock()
	remaining := len(app.roomLiveLocked(officeRoomID).pendingAttributionWindows)
	app.mu.Unlock()
	if remaining != 2 {
		t.Fatalf("terminal-before-ack consumed attribution: remaining=%d", remaining)
	}
	if entries := app.memorySnapshot(10); len(entries) != 0 {
		t.Fatalf("terminal-before-ack persisted transcript: %+v", entries)
	}
}
