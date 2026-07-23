package main

// Multi-room W4 §7.4 cursor-partitioning battery (docs/plans/
// multi-room-2026-07-08.md): THE make-or-break class — one room's ambient
// pass must never advance another room's window. Rooms A and B interleave
// transcripts, each room's brain pass consumes ONLY its own window, both end
// fully summarized, legacy no-roomId artifacts act as the OFFICE cursors, a
// room's pre-boot history is never backfilled, guests-only rooms defer
// scheduled passes, and two rooms' close flushes run concurrently without
// deadlock (the -race target).

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func appendRoomTestTranscript(t *testing.T, app *kanbanBoardApp, roomID string, id string, text string) {
	t.Helper()
	if _, appended, err := app.memory.appendAttributedTranscriptEntry(roomID, id, id, "Tom", "dominant", text, nil, false, ""); err != nil {
		t.Fatalf("append transcript %s: %v", id, err)
	} else if !appended {
		t.Fatalf("transcript %s appended=false, want true", id)
	}
}

// brainWindowResponder records every model input and returns a fixed
// write-up, so the test can assert exactly which transcripts fed each pass.
func brainWindowResponder(inputs *[]string) openAITextResponder {
	return func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		*inputs = append(*inputs, request.Input)
		return "## Overview\nSummary of the window.", nil
	}
}

func TestRoomCursorIsolationAcrossInterleavedTranscripts(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	roomB := "room-bbbb1111"
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	grantAmbientConsentForTest(t, app, authority, roomB, "tom@shareability.com")

	// Interleave the two rooms' transcript streams.
	appendRoomTestTranscript(t, app, officeRoomID, "office-1", "Boot Barn kickoff planning notes for the office.")
	appendRoomTestTranscript(t, app, roomB, "roomb-1", "Deal room diligence notes on the Aurora asset.")
	appendRoomTestTranscript(t, app, officeRoomID, "office-2", "Office decided the pilot ships on Friday.")
	appendRoomTestTranscript(t, app, roomB, "roomb-2", "Deal room agreed to request the data room index.")

	agent := meetingBrainAgent()
	var modelInputs []string
	responder := brainWindowResponder(&modelInputs)
	ctx := context.Background()

	// Office pass first: its window is ONLY the office transcripts.
	officeEntry, err := app.runAmbientAgentOnceForRoom(agent, ctx, "test-key", responder, 1, officeRoomID)
	if err != nil {
		t.Fatalf("office brain pass: %v", err)
	}
	if len(modelInputs) != 1 {
		t.Fatalf("model calls=%d, want 1", len(modelInputs))
	}
	for _, want := range []string{"office-1", "office-2"} {
		if !strings.Contains(modelInputs[0], want) {
			t.Fatalf("office window missing %s:\n%s", want, modelInputs[0])
		}
	}
	for _, leak := range []string{"roomb-1", "roomb-2"} {
		if strings.Contains(modelInputs[0], leak) {
			t.Fatalf("office window leaked room B transcript %s:\n%s", leak, modelInputs[0])
		}
	}
	if got := officeEntry.Metadata["roomId"]; got != officeRoomID {
		t.Fatalf("office brain roomId=%q, want office", got)
	}
	if got := officeEntry.Metadata["throughTranscriptId"]; got != "office-2" {
		t.Fatalf("office cursor=%q, want office-2", got)
	}

	// Room B's pass AFTER the office one: its full window is intact — the
	// office pass never advanced room B's cursor (the make-or-break).
	roomBEntry, err := app.runAmbientAgentOnceForRoom(agent, ctx, "test-key", responder, 1, roomB)
	if err != nil {
		t.Fatalf("room B brain pass: %v", err)
	}
	if len(modelInputs) != 2 {
		t.Fatalf("model calls=%d, want 2", len(modelInputs))
	}
	for _, want := range []string{"roomb-1", "roomb-2"} {
		if !strings.Contains(modelInputs[1], want) {
			t.Fatalf("room B window missing %s (office pass advanced it?):\n%s", want, modelInputs[1])
		}
	}
	for _, leak := range []string{"office-1", "office-2"} {
		if strings.Contains(modelInputs[1], leak) {
			t.Fatalf("room B window leaked office transcript %s:\n%s", leak, modelInputs[1])
		}
	}
	if got := roomBEntry.Metadata["roomId"]; got != roomB {
		t.Fatalf("room B brain roomId=%q, want %q", got, roomB)
	}
	if got := roomBEntry.Metadata["throughTranscriptId"]; got != "roomb-2" {
		t.Fatalf("room B cursor=%q, want roomb-2", got)
	}

	// Each room's sitting id is its own: the artifacts must key to DIFFERENT
	// meeting ids (the W2 mint fence, exercised through the W4 agent layer).
	if officeEntry.Metadata["meetingId"] == roomBEntry.Metadata["meetingId"] {
		t.Fatalf("both rooms' brains share meetingId %q", officeEntry.Metadata["meetingId"])
	}

	// Fully consumed: neither room re-feeds.
	if _, err := app.runAmbientAgentOnceForRoom(agent, ctx, "test-key", responder, 1, officeRoomID); err != nil {
		t.Fatalf("office re-pass: %v", err)
	}
	if _, err := app.runAmbientAgentOnceForRoom(agent, ctx, "test-key", responder, 1, roomB); err != nil {
		t.Fatalf("room B re-pass: %v", err)
	}
	if len(modelInputs) != 2 {
		t.Fatalf("model calls=%d after re-passes, want 2 (both rooms fully summarized)", len(modelInputs))
	}
}

// Legacy brain artifacts carry no roomId: they are the OFFICE cursors (§9.5),
// so the office pipeline resumes exactly where it left off while a named
// room's window is untouched by them.
func TestLegacyArtifactsWithoutRoomIDAreOfficeCursors(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	roomB := "room-bbbb2222"

	appendRoomTestTranscript(t, app, officeRoomID, "office-1", "Office planning notes before the deploy.")
	appendRoomTestTranscript(t, app, roomB, "roomb-1", "Deal room notes from before the deploy.")
	// A pre-room artifact: no roomId metadata at all (appendAmbientEntry
	// stamps nothing), cursor through office-1.
	if _, _, err := app.memory.appendAmbientEntry(meetingMemoryKindBrain, "brain-legacy", "## Overview\nOld write-up.", map[string]string{
		"throughTranscriptId": "office-1",
	}); err != nil {
		t.Fatalf("append legacy artifact: %v", err)
	}
	appendRoomTestTranscript(t, app, officeRoomID, "office-2", "Office follow-up commitments after the deploy.")

	agent := meetingBrainAgent()
	office := app.memory.unconsumedEntriesAfterForRoom(agent.inputKind, agent.artifactKind, agent.cursorMetadataKey, 10, "", officeRoomID)
	if len(office) != 1 || office[0].ID != "office-2" {
		t.Fatalf("office window=%v, want exactly office-2 (legacy artifact is the office cursor)", office)
	}
	roomWindow := app.memory.unconsumedEntriesAfterForRoom(agent.inputKind, agent.artifactKind, agent.cursorMetadataKey, 10, "", roomB)
	if len(roomWindow) != 1 || roomWindow[0].ID != "roomb-1" {
		t.Fatalf("room B window=%v, want exactly roomb-1 (never consumed by the legacy office cursor)", roomWindow)
	}
}

// A room with pre-boot history baselines at its newest input on first touch —
// the agent never backfills it — while entries appended after boot flow.
func TestRoomScopedAgentBaselinesAtBootNeverBackfillsRoomHistory(t *testing.T) {
	first := newIsolatedKanbanBoardApp(t)
	roomB := "room-bbbb3333"
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, first, authority, roomB, "tom@shareability.com")
	appendRoomTestTranscript(t, first, roomB, "roomb-old-1", "Deal room history from a previous boot.")
	appendRoomTestTranscript(t, first, roomB, "roomb-old-2", "More deal room history from a previous boot.")

	// Reboot against the same data directory: the entries above are pre-boot.
	rebooted := newKanbanBoardApp()

	agent := meetingBrainAgent()
	var modelInputs []string
	responder := brainWindowResponder(&modelInputs)
	if _, err := rebooted.runAmbientAgentOnceForRoom(agent, context.Background(), "test-key", responder, 1, roomB); err != nil {
		t.Fatalf("room B pass on rebooted app: %v", err)
	}
	if len(modelInputs) != 0 {
		t.Fatalf("rebooted room B pass consumed pre-boot history:\n%s", strings.Join(modelInputs, "\n"))
	}

	appendRoomTestTranscript(t, rebooted, roomB, "roomb-new-1", "Fresh deal room discussion after the reboot.")
	if _, err := rebooted.runAmbientAgentOnceForRoom(agent, context.Background(), "test-key", responder, 1, roomB); err != nil {
		t.Fatalf("room B pass after fresh input: %v", err)
	}
	if len(modelInputs) != 1 || !strings.Contains(modelInputs[0], "roomb-new-1") || strings.Contains(modelInputs[0], "roomb-old-1") {
		t.Fatalf("post-boot window=%v, want only roomb-new-1", modelInputs)
	}
}

// newRoomScopedTestAgent is newTestAmbientAgent with the W4 room dimension:
// the artifact stamps its window's room so the per-room cursor holds.
func newRoomScopedTestAgent(produced *[][]string, defersWhenGuestsOnly bool) ambientAgentConfig {
	artifactIndex := 0
	return ambientAgentConfig{
		name:                 "room test agent",
		defaultInterval:      time.Minute,
		intervalEnv:          "ROOM_TEST_AGENT_INTERVAL",
		disabledEnv:          "ROOM_TEST_AGENT_DISABLED",
		backfillEnv:          "ROOM_TEST_AGENT_BACKFILL",
		minBatchEnv:          "ROOM_TEST_AGENT_MIN",
		defaultMinBatch:      2,
		maxBatchEnv:          "ROOM_TEST_AGENT_MAX",
		defaultMaxBatch:      3,
		inputKind:            meetingMemoryKindTranscript,
		artifactKind:         "room_test_artifact",
		cursorMetadataKey:    "throughTestId",
		requestTimeout:       time.Second,
		roomScoped:           true,
		defersWhenGuestsOnly: defersWhenGuestsOnly,
		produce: func(app *kanbanBoardApp, _ context.Context, _ string, inputs []meetingMemoryEntry, _ openAITextResponder) (meetingMemoryEntry, error) {
			ids := make([]string, 0, len(inputs))
			for _, input := range inputs {
				ids = append(ids, input.ID)
			}
			*produced = append(*produced, ids)
			artifactIndex++
			entry, _, err := app.memory.appendEntry("room_test_artifact", fmt.Sprintf("room-test-artifact-%d", artifactIndex), "room test artifact", map[string]string{
				"throughTestId": inputs[len(inputs)-1].ID,
				"roomId":        ambientWindowRoomID(inputs),
			})
			return entry, err
		},
	}
}

// §6.5: a room whose live seats are guests only defers its scheduled passes;
// a member seat (or the direct close-flush run) lifts the deferral.
func TestGuestsOnlyRoomDefersScheduledPassesUntilMemberPresent(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	roomB := "room-guestsonly"
	var produced [][]string
	agent := newRoomScopedTestAgent(&produced, true)

	appendRoomTestTranscript(t, app, roomB, "roomb-g1", "Guests discussing the venue logistics at length.")
	appendRoomTestTranscript(t, app, roomB, "roomb-g2", "Guests kept talking about the schedule details.")

	// Seat a guest only.
	app.mu.Lock()
	state := app.roomLiveLocked(roomB)
	state.guestSeats["guest-session-1"] = "Guest Sam"
	state.participants["Guest Sam"] = time.Now()
	state.participantCounts["Guest Sam"] = 1
	app.mu.Unlock()

	app.fireAmbientAgentPass(agent, "test-key", agent.minBatch(), roomB)
	if len(produced) != 0 {
		t.Fatalf("produced=%v, want the guests-only room deferred", produced)
	}

	// A member joins: the next scheduled pass fires.
	app.mu.Lock()
	state.participants["AJ"] = time.Now()
	state.participantCounts["AJ"] = 1
	app.mu.Unlock()

	app.fireAmbientAgentPass(agent, "test-key", agent.minBatch(), roomB)
	if len(produced) != 1 || strings.Join(produced[0], ",") != "roomb-g1,roomb-g2" {
		t.Fatalf("produced=%v, want one pass over both inputs once a member is present", produced)
	}

	// The close-flush seam is never deferred: guests-only again, direct run.
	app.mu.Lock()
	delete(state.participants, "AJ")
	delete(state.participantCounts, "AJ")
	app.mu.Unlock()
	appendRoomTestTranscript(t, app, roomB, "roomb-g3", "One last guest remark before the room emptied.")
	if _, err := app.runAmbientAgentOnceForRoom(agent, context.Background(), "test-key", nil, 1, roomB); err != nil {
		t.Fatalf("close-flush style pass: %v", err)
	}
	if len(produced) != 2 {
		t.Fatalf("produced=%v, want the bounded close pass to run despite guests-only", produced)
	}
}

// W4 §7.4: the A3 brain nudges carry the room — a named room's fresh
// transcript/chat wakes THAT room's window evaluation, never just the
// office's (which would leave the room waiting on the safety-floor tick).
func TestTranscriptAndChatNudgesCarryTheRoom(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	roomB := "room-nudge-bbbb"

	if _, ok := app.recordRoomChatMessage(roomB, "AJ", "typed exchange in room B"); !ok {
		t.Fatal("recordRoomChatMessage did not append")
	}
	app.mu.Lock()
	_, pending := app.agentPendingRooms[meetingBrainAgentName][roomB]
	app.mu.Unlock()
	if !pending {
		t.Fatal("room chat must nudge the brain worker FOR ITS ROOM")
	}

	// The drain hands the runner exactly the nudged rooms.
	rooms := app.drainAmbientAgentPendingRooms(meetingBrainAgentName)
	found := false
	for _, roomID := range rooms {
		if roomID == roomB {
			found = true
		}
	}
	if !found {
		t.Fatalf("drained rooms=%v, want %s", rooms, roomB)
	}
}

// closeFlushSupersetJSON parses in every close-chain stage: the brain treats
// it as text, and each JSON consumer finds its own (empty) sections.
const closeFlushSupersetJSON = `{"summary":"ok","operations":[],"decisions":[],"themes":[],"openQuestions":[],"alignments":[],"narratives":[],"topics":[],"actionItems":[]}`

// W4 §7.4: two rooms closing concurrently neither serialize on one lock nor
// deadlock — every room-scoped stage runs under its own (agent, room) lock.
// This is the -race target of the wave.
func TestTwoRoomsCloseFlushConcurrentlyWithoutDeadlock(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	roomA := "room-close-aaaa"
	roomB := "room-close-bbbb"
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, roomA, "tom@shareability.com")
	grantAmbientConsentForTest(t, app, authority, roomB, "tom@shareability.com")
	appendRoomTestTranscript(t, app, roomA, "close-a1", "Room A wrapped up the vendor selection discussion.")
	appendRoomTestTranscript(t, app, roomB, "close-b1", "Room B finalized the launch checklist review.")

	var calls atomic.Int64
	responder := func(context.Context, string, openAITextRequest) (string, error) {
		calls.Add(1)
		return closeFlushSupersetJSON, nil
	}

	done := make(chan string, 2)
	for _, roomID := range []string{roomA, roomB} {
		go func(roomID string) {
			app.flushAmbientAgentsForCloseWithResponder("idle-end", roomID, false, responder)
			done <- roomID
		}(roomID)
	}
	finished := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case roomID := <-done:
			finished[roomID] = true
		case <-time.After(60 * time.Second):
			t.Fatalf("concurrent close flush deadlocked; finished=%v", finished)
		}
	}
	if !finished[roomA] || !finished[roomB] {
		t.Fatalf("finished=%v, want both rooms", finished)
	}
	if calls.Load() == 0 {
		t.Fatal("close flush never reached the model responder")
	}

	// Each room's chain summarized ITS OWN transcript into a room-stamped brain.
	sawRoom := map[string]bool{}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindBrain, 0) {
		sawRoom[normalizeRoomID(entry.Metadata["roomId"])] = true
	}
	if !sawRoom[roomA] || !sawRoom[roomB] {
		t.Fatalf("brain rooms=%v, want both %s and %s", sawRoom, roomA, roomB)
	}
}
