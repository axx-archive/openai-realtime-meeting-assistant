package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOnTrackAndMediaRegistryUseCapturedAdmissionLeaseFences(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	onTrack := sourceSectionForAdmissionTest(t, source, "pc.OnTrack(func", "// Proactively recover from network changes")
	if !strings.Contains(onTrack, "trackAdmission := participantAdmission.Load()") {
		t.Fatal("OnTrack no longer captures the socket's pointer-stable admission lease")
	}
	if !strings.Contains(onTrack, "trackAdmission.whileCurrent(func()") {
		t.Fatal("OnTrack enqueue/broadcast is no longer lease-gated")
	}
	readAt := strings.Index(onTrack, "packet, _, err := t.ReadRTP()")
	if readAt < 0 {
		t.Fatal("OnTrack RTP read loop missing")
	}
	afterRead := onTrack[readAt:]
	fenceAt := strings.Index(afterRead, "if !trackAdmission.isCurrent()")
	telemetryAt := strings.Index(afterRead, "silenceWatch.lastRTPNanos.Store")
	forwardAt := strings.Index(afterRead, "forwardPublisherRTP(")
	if fenceAt < 0 || telemetryAt < 0 || forwardAt < 0 || !(fenceAt < telemetryAt && fenceAt < forwardAt) {
		t.Fatalf("post-ReadRTP lease fence order fence=%d telemetry=%d forward=%d", fenceAt, telemetryAt, forwardAt)
	}

	mediaReady := sourceSectionForAdmissionTest(t, source, `case "media_ready":`, `case "request_participant_tracks":`)
	if !strings.Contains(mediaReady, "admission.whileCurrent(func()") || !strings.Contains(mediaReady, "peerConnections = append") {
		t.Fatal("media_ready registry install is no longer admission-lease gated")
	}
}

func commitAdmissionForLeaseTest(app *kanbanBoardApp, roomID, sessionID, endpointID string, transfer bool) (participantAdmissionResult, error) {
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	var result participantAdmissionResult
	var err error
	if transfer {
		result, err = app.transferParticipantSessionEndpointInRoomWithLeaseLocked(state, "AJ", sessionID, endpointID)
	} else {
		result, err = app.admitParticipantSessionEndpointInRoomWithLeaseLocked(state, "AJ", sessionID, endpointID)
	}
	if err == nil {
		result.retired = append(result.retired, app.retireParticipantSeatsOutsideRoomLocked("AJ", roomID)...)
	}
	app.mu.Unlock()
	if err != nil {
		return participantAdmissionResult{}, err
	}
	return result, nil
}

func TestAdmissionLeaseTransferDrainsInFlightGrantAndRejectsStaleInstall(t *testing.T) {
	app := &kanbanBoardApp{roomLive: map[string]*roomLiveState{}}
	first, err := commitAdmissionForLeaseTest(app, "room-a", "session-old", "endpoint-old", false)
	if err != nil {
		t.Fatal(err)
	}
	if !first.lease.isCurrent() {
		t.Fatal("first admission did not receive a current lease")
	}

	gateEntered := make(chan struct{})
	releaseGate := make(chan struct{})
	oldEffect := make(chan struct{})
	go func() {
		if !first.lease.whileCurrent(func() {
			close(gateEntered)
			<-releaseGate
			close(oldEffect)
		}) {
			panic("current admission could not enter its lease gate")
		}
	}()
	<-gateEntered

	retirementPublished := make(chan participantAdmissionResult, 1)
	transferDone := make(chan participantAdmissionResult, 1)
	transferErr := make(chan error, 1)
	go func() {
		second, err := commitAdmissionForLeaseTest(app, "room-a", "session-new", "endpoint-new", true)
		if err != nil {
			transferErr <- err
			return
		}
		retirementPublished <- second
		drainParticipantAdmissionRetirements(second.retired)
		transferDone <- second
	}()
	var second participantAdmissionResult
	select {
	case second = <-retirementPublished:
	case err := <-transferErr:
		t.Fatal(err)
	}
	if first.lease.isCurrent() {
		t.Fatal("transfer did not atomically retire the old admission lease")
	}
	if !second.lease.isCurrent() {
		t.Fatal("transfer did not mint a current successor lease")
	}
	select {
	case <-transferDone:
		t.Fatal("transfer returned before the already-started grant/install gate drained")
	default:
	}

	close(releaseGate)
	select {
	case <-oldEffect:
	case <-time.After(time.Second):
		t.Fatal("old lease operation did not finish")
	}
	select {
	case <-transferDone:
	case <-time.After(time.Second):
		t.Fatal("transfer did not finish after the lease gate drained")
	}

	var staleInstall atomic.Bool
	if first.lease.whileCurrent(func() { staleInstall.Store(true) }) {
		t.Fatal("retired admission re-entered the install/grant gate")
	}
	if staleInstall.Load() {
		t.Fatal("retired admission installed registry state after transfer")
	}
}

func TestForgetDrainsAdmissionLeaseBeforeRegistryTeardownCanProceed(t *testing.T) {
	app := &kanbanBoardApp{roomLive: map[string]*roomLiveState{}}
	admission, err := commitAdmissionForLeaseTest(app, "room-a", "session-leaving", "endpoint-leaving", false)
	if err != nil {
		t.Fatal(err)
	}

	gateEntered := make(chan struct{})
	releaseGate := make(chan struct{})
	effectFinished := make(chan struct{})
	go admission.lease.whileCurrent(func() {
		close(gateEntered)
		<-releaseGate
		close(effectFinished)
	})
	<-gateEntered

	type forgetResult struct {
		removed      bool
		stillPresent bool
	}
	forgetDone := make(chan forgetResult, 1)
	go func() {
		removed, stillPresent := app.forgetParticipantSessionResultInRoom("room-a", "AJ", "session-leaving")
		forgetDone <- forgetResult{removed: removed, stillPresent: stillPresent}
	}()
	deadline := time.Now().Add(time.Second)
	for admission.lease.isCurrent() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if admission.lease.isCurrent() {
		t.Fatal("forget did not publish lease retirement")
	}
	select {
	case <-forgetDone:
		t.Fatal("forget returned before the in-flight admission gate drained")
	default:
	}

	close(releaseGate)
	select {
	case <-effectFinished:
	case <-time.After(time.Second):
		t.Fatal("in-flight lease operation did not finish")
	}
	select {
	case result := <-forgetDone:
		if !result.removed || result.stillPresent {
			t.Fatalf("forget result=%+v, want removed final endpoint", result)
		}
	case <-time.After(time.Second):
		t.Fatal("forget did not return after lease drain")
	}
}

func TestEveryLeaseTeardownDrainsBeforeRegistryOrMediaRemoval(t *testing.T) {
	mainRaw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	mainSource := string(mainRaw)
	cleanup := sourceSectionForAdmissionTest(t, mainSource, "cleanupParticipantSession := func", "// When this frame returns close the Websocket")
	forgetAt := strings.Index(cleanup, "forgetParticipantSessionResultInRoom(")
	unregisterAt := strings.Index(cleanup, "unregisterParticipantSession(")
	if forgetAt < 0 || unregisterAt < 0 || forgetAt >= unregisterAt {
		t.Fatalf("socket cleanup order forget/drain=%d unregister=%d", forgetAt, unregisterAt)
	}

	kanbanRaw, err := os.ReadFile("kanban.go")
	if err != nil {
		t.Fatal(err)
	}
	kanbanSource := string(kanbanRaw)
	forget := sourceSectionForAdmissionTest(t, kanbanSource, "func (app *kanbanBoardApp) forgetParticipantSessionResultInRoom", "// participantSessionCurrent reports")
	unlockAt := strings.LastIndex(forget, "app.mu.Unlock()")
	drainAt := strings.LastIndex(forget, "drainParticipantAdmissionLeases(")
	if unlockAt < 0 || drainAt < 0 || unlockAt >= drainAt {
		t.Fatalf("forget lock/drain order unlock=%d drain=%d", unlockAt, drainAt)
	}
	sweep := sourceSectionForAdmissionTest(t, kanbanSource, "func (app *kanbanBoardApp) sweepStaleParticipantSessions()", "// startParticipantLivenessSweeper")
	drainAt = strings.Index(sweep, "drainParticipantAdmissionLeases(entry.leases)")
	closeAt := strings.Index(sweep, "closeSessionMedia(sessionID)")
	unregisterAt = strings.Index(sweep, "unregisterParticipantSession(entry.name, sessionID)")
	if drainAt < 0 || closeAt < 0 || unregisterAt < 0 || !(drainAt < closeAt && closeAt < unregisterAt) {
		t.Fatalf("liveness teardown order drain=%d close=%d unregister=%d", drainAt, closeAt, unregisterAt)
	}

	roomLiveRaw, err := os.ReadFile("room_live.go")
	if err != nil {
		t.Fatal(err)
	}
	archive := sourceSectionForAdmissionTest(t, string(roomLiveRaw), "func (app *kanbanBoardApp) closeRoomForArchive", "/* ---------- rooms-list office event")
	drainAt = strings.Index(archive, "drainParticipantAdmissionLeases(seat.leases)")
	closeAt = strings.Index(archive, "closeSessionMedia(sessionID)")
	unregisterAt = strings.Index(archive, "unregisterParticipantSession(seat.name, sessionID)")
	if drainAt < 0 || closeAt < 0 || unregisterAt < 0 || !(drainAt < closeAt && closeAt < unregisterAt) {
		t.Fatalf("archive teardown order drain=%d close=%d unregister=%d", drainAt, closeAt, unregisterAt)
	}
}

func TestLivenessReapCarriesRetiredLeaseToPostLockDrain(t *testing.T) {
	app := &kanbanBoardApp{roomLive: map[string]*roomLiveState{}}
	admission, err := commitAdmissionForLeaseTest(app, "room-a", "session-stale", "endpoint-stale", false)
	if err != nil {
		t.Fatal(err)
	}

	gateEntered := make(chan struct{})
	releaseGate := make(chan struct{})
	go admission.lease.whileCurrent(func() {
		close(gateEntered)
		<-releaseGate
	})
	<-gateEntered

	now := time.Now().UTC()
	app.mu.Lock()
	state := app.roomLiveLocked("room-a")
	state.participants["AJ"] = now.Add(-2 * participantLivenessTimeout)
	state.participantSessionLiveness["AJ"]["session-stale"] = now.Add(-2 * participantLivenessTimeout)
	reaped := app.reapStaleParticipantSessionsLocked(now, participantLivenessTimeout)
	app.mu.Unlock()
	entries := reaped["room-a"]
	if len(entries) != 1 || len(entries[0].leases) != 1 || entries[0].leases[0] != admission.lease {
		t.Fatalf("liveness reap lease handoff=%+v, want exact retired session lease", entries)
	}
	if admission.lease.isCurrent() {
		t.Fatal("liveness reap did not revoke lease under app.mu")
	}

	drainDone := make(chan struct{})
	go func() {
		drainParticipantAdmissionLeases(entries[0].leases)
		close(drainDone)
	}()
	select {
	case <-drainDone:
		t.Fatal("post-lock liveness drain did not wait for in-flight lease operation")
	default:
	}
	close(releaseGate)
	select {
	case <-drainDone:
	case <-time.After(time.Second):
		t.Fatal("post-lock liveness drain did not finish")
	}
}

func TestConcurrentTwoRoomJoinPipelineHasOneDeterministicWinner(t *testing.T) {
	installNamedRoomIDsForTest(t, "room-a", "room-b")
	dir := t.TempDir()
	memory, err := newMeetingMemoryStore(filepath.Join(dir, "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	meetings, err := loadMeetingStore(filepath.Join(dir, "meetings.json"))
	if err != nil {
		t.Fatal(err)
	}
	app := &kanbanBoardApp{memory: memory, meetings: meetings, roomLive: map[string]*roomLiveState{}}
	if err := app.initializeAdmissionAnchorStore(filepath.Join(dir, "admission-anchors.json")); err != nil {
		t.Fatal(err)
	}
	sittingA := app.prepareMeetingSittingID("room-a")
	sittingB := app.prepareMeetingSittingID("room-b")
	principal := memberAdmissionPrincipal("aj@example.com")
	firstCommitted := make(chan participantAdmissionResult, 1)
	firstErr := make(chan error, 1)
	releaseFirstInstall := make(chan struct{})
	firstFinished := make(chan bool, 1)

	go func() {
		first, err := app.admitParticipantWithAnchorResult(context.Background(), "room-a", "AJ", "session-room-a", "endpoint-a", sittingA, principal, false)
		if err != nil {
			firstErr <- err
			return
		}
		firstCommitted <- first
		<-releaseFirstInstall
		firstFinished <- first.lease.whileCurrent(func() {})
	}()
	var first participantAdmissionResult
	select {
	case first = <-firstCommitted:
	case err := <-firstErr:
		t.Fatal(err)
	}

	// This is the second overlapping handler pipeline. Its app.mu transaction
	// is deliberately last, so room B is the deterministic winner.
	second, err := app.admitParticipantWithAnchorResult(context.Background(), "room-b", "AJ", "session-room-b", "endpoint-b", sittingB, principal, false)
	if err != nil {
		t.Fatal(err)
	}
	close(releaseFirstInstall)
	if installed := <-firstFinished; installed {
		t.Fatal("earlier room-A handler installed after room-B admission won")
	}
	if first.lease.isCurrent() || !second.lease.isCurrent() {
		t.Fatalf("lease winner old=%t new=%t, want false/true", first.lease.isCurrent(), second.lease.isCurrent())
	}

	app.mu.Lock()
	roomA := app.roomLiveLocked("room-a")
	roomB := app.roomLiveLocked("room-b")
	roomACount := roomA.participantCounts["AJ"]
	roomBCount := roomB.participantCounts["AJ"]
	roomBSession := roomB.participantEndpoints["AJ"]["endpoint-b"]
	app.mu.Unlock()
	if roomACount != 0 || roomBCount != 1 || roomBSession != "session-room-b" {
		t.Fatalf("concurrent room seats roomA=%d roomB=%d roomBSession=%q, want 0/1/session-room-b", roomACount, roomBCount, roomBSession)
	}
	if len(second.retired) != 1 || second.retired[0].roomID != "room-a" || second.retired[0].sessionID != "session-room-a" {
		t.Fatalf("winner retirements=%+v, want room-a/session-room-a", second.retired)
	}
}
