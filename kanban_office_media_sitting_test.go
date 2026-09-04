package main

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func transcriptRowsForSittingInTest(t *testing.T, app *kanbanBoardApp, sittingID string) int {
	t.Helper()
	rows := 0
	for _, entry := range app.memorySnapshot(200) {
		if entry.Kind == meetingMemoryKindTranscript && entry.Metadata["meetingId"] == sittingID {
			rows++
		}
	}
	return rows
}

// TestOfficeRejoinAfterSittingRotationDoesNotInheritTheDeadSitting pins the
// 2026-09-03 production shape: /readyz served the office a sittingId minted
// ~18 hours earlier while the STT lane reported a fresh success, because
// ensureOfficeMedia only published state.mediaSittingID when it minted a new
// media actor. A rejoin that found a surviving actor kept the previous
// sitting's id, and every transcript segment of the live sitting was then
// dropped by the sitting gate in
// rememberTranscriptWithScopeAndSegmentAndSource — silently, with the room
// still reporting stt healthy.
func TestOfficeRejoinAfterSittingRotationDoesNotInheritTheDeadSitting(t *testing.T) {
	resetRoomMediaActorsForTest(t)
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	roomID := officeRoomID
	email := "aj@shareability.com"
	name := participantNameForEmail(email)

	sittingA := admitMemberWithTranscriptConsentForTest(t, app, roomID, email)
	generationA := app.ensureRoomMedia(roomID)
	app.mu.Lock()
	scopeA := RoomScoutScope{RoomID: roomID, SittingID: app.roomLiveLocked(roomID).mediaSittingID, MediaGeneration: generationA}
	app.mu.Unlock()
	if scopeA.SittingID != sittingA {
		t.Fatalf("first sitting media identity=%q want=%q", scopeA.SittingID, sittingA)
	}
	attributeNextTranscriptForTest(app, roomID, name)
	app.rememberTranscriptForMediaScope(scopeA, kanbanRealtimeEvent{
		EventID: "sitting-a-event", ItemID: "sitting-a-item",
		Transcript: "The first sitting's words land under the first sitting.",
	}, "transcript_lane", "test-model")
	if rows := transcriptRowsForSittingInTest(t, app, sittingA); rows != 1 {
		t.Fatalf("first sitting transcript rows=%d, want 1", rows)
	}

	// The production seam that manufactures the divergence: a record that is
	// already ended when the idle-end close chain runs makes
	// endMeetingWithFinalizationLocked report changed=false, and
	// endMeetingForIdle's !changed return then skips BOTH the memory-id
	// rotation and the media release. reconcileMeetingRecordsAtBootForRoom
	// closes a record the same way. The next admission re-mints the sitting id
	// and never touches the media plane.
	recordA, ok := app.meetings.activeRecord(roomID)
	if !ok {
		t.Fatal("no open meeting record for the first sitting")
	}
	if _, _, err := app.meetings.endMeetingWithFinalization(recordA.ID, time.Now().UTC(), meetingEndedReasonRestart, "", app.meetingFinalizationSource(recordA.ID)); err != nil {
		t.Fatalf("end the first sitting's record: %v", err)
	}

	sittingB := app.prepareMeetingSittingID(roomID)
	if sittingB == sittingA {
		t.Fatalf("rejoin did not rotate the sitting id: still %q", sittingB)
	}
	if _, _, err := app.admitParticipantWithAnchor(context.Background(), roomID, name, "rejoin-session", "rejoin-endpoint", sittingB, memberAdmissionPrincipal(email)); err != nil {
		t.Fatalf("admit the rejoin: %v", err)
	}
	if got := app.noteMeetingAdmissionForSitting(roomID, name, sittingB); got != sittingB {
		t.Fatalf("open the successor sitting got=%q want=%q", got, sittingB)
	}
	enableFullTranscriptConsentForTest(t, app, memberAdmissionPrincipal(email), roomID, sittingB)

	generationB := app.ensureRoomMedia(roomID)
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	mediaSittingID, mediaGen, actor := state.mediaSittingID, state.mediaGen, state.mediaActor
	app.mu.Unlock()
	if mediaSittingID != sittingB {
		t.Fatalf("rejoin inherited the dead sitting: media_sitting=%q current_sitting=%q", mediaSittingID, sittingB)
	}
	if generationB <= generationA || mediaGen != generationB || actor == nil || actor.generation != generationB {
		t.Fatalf("successor media identity not published: first=%d successor=%d stateGen=%d actor=%+v", generationA, generationB, mediaGen, actor)
	}

	// The retired scope must still be refused, not re-labelled onto the
	// successor sitting: a dead sitting's audio is never the live sitting's
	// transcript.
	attributeNextTranscriptForTest(app, roomID, name)
	app.rememberTranscriptForMediaScope(scopeA, kanbanRealtimeEvent{
		EventID: "retired-scope-event", ItemID: "retired-scope-item",
		Transcript: "This retired-scope line must not enter the successor sitting.",
	}, "transcript_lane", "test-model")
	if rows := transcriptRowsForSittingInTest(t, app, sittingB); rows != 0 {
		t.Fatalf("retired scope was re-labelled onto the successor sitting: rows=%d", rows)
	}
	if rows := transcriptRowsForSittingInTest(t, app, sittingA); rows != 1 {
		t.Fatalf("retired scope mutated the closed sitting: rows=%d, want 1", rows)
	}

	// And the live sitting captures: this is the assertion the production
	// snapshot failed, with stt healthy and transcript missing.
	scopeB := RoomScoutScope{RoomID: roomID, SittingID: mediaSittingID, MediaGeneration: generationB}
	attributeNextTranscriptForTest(app, roomID, name)
	app.rememberTranscriptForMediaScope(scopeB, kanbanRealtimeEvent{
		EventID: "sitting-b-event", ItemID: "sitting-b-item",
		Transcript: "The successor sitting's words land under the successor sitting.",
	}, "transcript_lane", "test-model")
	if rows := transcriptRowsForSittingInTest(t, app, sittingB); rows != 1 {
		t.Fatalf("successor sitting transcript rows=%d, want 1 — the live sitting is still not capturing", rows)
	}
}

// TestOfficeMediaStaysIdempotentWhileTheSittingHoldsStill guards the cure
// against being worse than the disease: ensureOfficeMedia runs on every
// admission, and re-minting media (or restarting the lane) per join would be a
// worse bug than the stale id it now heals.
func TestOfficeMediaStaysIdempotentWhileTheSittingHoldsStill(t *testing.T) {
	resetRoomMediaActorsForTest(t)
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	roomID := officeRoomID

	admitMemberWithTranscriptConsentForTest(t, app, roomID, "aj@shareability.com")
	first := app.ensureRoomMedia(roomID)
	app.mu.Lock()
	actor := app.roomLiveLocked(roomID).mediaActor
	laneToken := app.transcriptionStartToken
	app.mu.Unlock()
	for range 8 {
		if again := app.ensureRoomMedia(roomID); again != first {
			t.Fatalf("repeat admission moved the media generation: first=%d again=%d", first, again)
		}
	}
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	sameActor := state.mediaActor == actor
	gen := state.mediaGen
	sameLaneToken := app.transcriptionStartToken == laneToken
	app.mu.Unlock()
	if !sameActor || gen != first {
		t.Fatalf("repeat admission re-minted office media: sameActor=%t gen=%d first=%d", sameActor, gen, first)
	}
	// The lane-start token moves only when the lane is dropped or restarted.
	// Holding it still is how this test pins "no lane restart per join".
	if !sameLaneToken {
		t.Fatal("repeat admission dropped or restarted the office transcription lane")
	}
}

// TestOfficeMediaDivergenceOnlyMovesForward pins the race guard.
// ensureOfficeMedia reads the current sitting id before it takes app.mu (the
// memory store's lock must not be taken under app.mu), so a caller holding an
// older snapshot must never retire a successor identity another admission
// already published.
func TestOfficeMediaDivergenceOnlyMovesForward(t *testing.T) {
	resetRoomMediaActorsForTest(t)
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	roomID := officeRoomID

	sitting := admitMemberWithTranscriptConsentForTest(t, app, roomID, "aj@shareability.com")
	generation := app.ensureRoomMedia(roomID)

	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	staleRollover := app.retireDivergedOfficeMediaLocked(state, "meeting-20000101-000000-000000000")
	emptyRollover := app.retireDivergedOfficeMediaLocked(state, "")
	heldSitting, heldGen, heldActor := state.mediaSittingID, state.mediaGen, state.mediaActor
	app.mu.Unlock()

	if staleRollover.changed || emptyRollover.changed {
		t.Fatalf("an older or empty sitting id retired a live media identity: stale=%+v empty=%+v", staleRollover, emptyRollover)
	}
	if heldSitting != sitting || heldGen != generation || heldActor == nil {
		t.Fatalf("live media identity was disturbed: sitting=%q gen=%d actor=%+v", heldSitting, heldGen, heldActor)
	}
}

// TestOfficeMediaSnapshotCannotRegressConcurrentSittingRollover pins the
// nil-actor half of the snapshot race. ensureOfficeMedia reads memory before
// app.mu; a concurrent manual boundary publishes its successor under
// meetingLifecycleMu and deliberately leaves mediaActor nil for the next
// media mint. Without the matching read lease in ensureOfficeMedia, the
// paused admission resumes after that boundary and re-mints the predecessor
// id because retireDivergedOfficeMediaLocked has no live actor to compare.
func TestOfficeMediaSnapshotCannotRegressConcurrentSittingRollover(t *testing.T) {
	resetRoomMediaActorsForTest(t)
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	roomID := officeRoomID

	sittingA := admitMemberWithTranscriptConsentForTest(t, app, roomID, "aj@shareability.com")
	generationA := app.ensureRoomMedia(roomID)
	sittingB := app.memory.mintSuccessorMeetingID(roomID, sittingA)
	if sittingB == "" || sittingB <= sittingA {
		t.Fatalf("successor sitting=%q must sort after predecessor=%q", sittingB, sittingA)
	}

	snapshotRead := make(chan struct{})
	releaseAdmission := make(chan struct{})
	officeMediaAfterSittingSnapshotProbe = func() {
		close(snapshotRead)
		<-releaseAdmission
	}
	t.Cleanup(func() { officeMediaAfterSittingSnapshotProbe = nil })

	ensureDone := make(chan uint64, 1)
	go func() { ensureDone <- app.ensureRoomMedia(roomID) }()
	<-snapshotRead

	rolloverAttempted := make(chan struct{})
	rolloverDone := make(chan error, 1)
	go func() {
		close(rolloverAttempted)
		app.meetingLifecycleMu.Lock()
		app.mu.Lock()
		if !app.memory.transitionMeetingIDIfCurrent(roomID, sittingA, sittingB) {
			app.mu.Unlock()
			app.meetingLifecycleMu.Unlock()
			rolloverDone <- fmt.Errorf("transition %q to %q was refused", sittingA, sittingB)
			return
		}
		rollover := app.rolloverOfficeMediaAfterManualArchiveLocked(sittingA, sittingB)
		app.mu.Unlock()
		app.meetingLifecycleMu.Unlock()
		app.finishOfficeMediaAfterManualArchive(rollover)
		if !rollover.changed {
			rolloverDone <- fmt.Errorf("media rollover did not retire predecessor %q", sittingA)
			return
		}
		rolloverDone <- nil
	}()
	<-rolloverAttempted

	// The write-side sitting transition must wait until the admission has
	// published the exact snapshot it read. This timeout is not the product
	// assertion; the sitting identity checks below prove the resulting order.
	select {
	case err := <-rolloverDone:
		close(releaseAdmission)
		<-ensureDone
		t.Fatalf("sitting rollover crossed an in-flight media snapshot: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseAdmission)
	if got := <-ensureDone; got != generationA {
		t.Fatalf("unchanged predecessor admission generation=%d, want %d", got, generationA)
	}
	if err := <-rolloverDone; err != nil {
		t.Fatal(err)
	}

	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	rolledSitting, rolledActor := state.mediaSittingID, state.mediaActor
	app.mu.Unlock()
	if rolledSitting != sittingB || rolledActor != nil {
		t.Fatalf("stale snapshot regressed successor media: sitting=%q actor=%+v want sitting=%q actor=nil", rolledSitting, rolledActor, sittingB)
	}

	officeMediaAfterSittingSnapshotProbe = nil
	generationB := app.ensureRoomMedia(roomID)
	app.mu.Lock()
	state = app.roomLiveLocked(roomID)
	finalSitting, finalActor := state.mediaSittingID, state.mediaActor
	app.mu.Unlock()
	if finalSitting != sittingB || finalActor == nil || generationB <= generationA {
		t.Fatalf("successor media was not minted cleanly: sitting=%q actor=%+v oldGen=%d newGen=%d", finalSitting, finalActor, generationA, generationB)
	}
}
