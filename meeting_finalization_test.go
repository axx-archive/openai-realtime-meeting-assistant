package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func meetingFinalizationResponder(calls *atomic.Int32, anchor string) openAITextResponder {
	return func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls.Add(1)
		if strings.Contains(request.Instructions, "meeting digest compiler") {
			return cannedArchiveMeetingDigestJSON(anchor), nil
		}
		return "## Overview\nThe team chose the durable launch plan.\n## Transcript reference\n" + anchor, nil
	}
}

func waitForMeetingFinalizationState(t *testing.T, app *kanbanBoardApp, meetingID, state string) meetingRecord {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var record meetingRecord
	for time.Now().Before(deadline) {
		var found bool
		record, found = app.meetings.recordByID(meetingID)
		if found && record.Finalization != nil && record.Finalization.State == state {
			return record
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("meeting %s finalization did not reach %s: %+v", meetingID, state, record.Finalization)
	return meetingRecord{}
}

func waitForMeetingFinalizationQueueIdle(t *testing.T, app *kanbanBoardApp) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		app.meetingFinalizationRunMu.Lock()
		idle := !app.meetingFinalizationWorker && len(app.meetingFinalizationQueue) == 0 && len(app.meetingFinalizationBacklog) == 0 && len(app.meetingFinalizationRunning) == 0
		app.meetingFinalizationRunMu.Unlock()
		if idle {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("meeting finalization queue did not quiesce")
}

func TestCanonicalReconcileDeferredCoversConcreteMeetingCloseTransitions(t *testing.T) {
	for _, closeKind := range []string{"idle", "named-room-archive"} {
		t.Run(closeKind, func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", "")
			t.Setenv("BONFIRE_ROOMS_PATH", filepath.Join(t.TempDir(), "rooms.json"))
			app := newIsolatedKanbanBoardApp(t)
			roomID := officeRoomID
			if closeKind == "named-room-archive" {
				room, err := appRoomStore().create("canonical close fence", "", "aj@shareability.com", false)
				if err != nil {
					t.Fatal(err)
				}
				roomID = room.ID
			}
			record, changed := app.meetings.startMeeting(roomID, "meeting-"+closeKind, time.Now().UTC(), []string{"AJ"})
			if !changed || !app.canonicalReconcileDeferred() {
				t.Fatalf("open %s meeting did not defer parity", closeKind)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			app.canonicalReconcileAfterMeetingClosed = func() {
				close(entered)
				<-release
			}
			done := make(chan struct{})
			if closeKind == "idle" {
				app.meetings.mu.Lock()
				generation := app.meetings.idleGenerations[roomID]
				app.meetings.mu.Unlock()
				go func() {
					app.endMeetingForIdle(roomID, generation)
					close(done)
				}()
			} else {
				if err := appRoomStore().archive(roomID); err != nil {
					t.Fatal(err)
				}
				go func() {
					app.closeRoomForArchive(roomID)
					close(done)
				}()
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatalf("%s close did not reach post-durable transition", closeKind)
			}
			if _, open := app.meetings.activeRecord(roomID); open {
				t.Fatalf("%s record was still open at transition barrier", closeKind)
			}
			if !app.canonicalReconcileDeferred() {
				t.Fatalf("%s close exposed a parity gap after ending %s", closeKind, record.ID)
			}
			close(release)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatalf("%s close did not finish", closeKind)
			}
		})
	}
}

func TestCanonicalReconcileDeferredDistinguishesRetryFromBootBacklog(t *testing.T) {
	app := &kanbanBoardApp{
		meetingFinalizationRunning:        map[string]struct{}{"historical": {}},
		meetingFinalizationQueuedPriority: map[string]bool{"historical": false},
		meetingFinalizationActive:         map[string]bool{},
		meetingFinalizationRetryQueued:    map[string]bool{},
		meetingFinalizationRetryActive:    map[string]bool{},
		meetingArchivePublishing:          map[string]bool{},
	}
	if app.canonicalReconcileDeferred() {
		t.Fatal("historical boot backlog alone deferred full parity")
	}
	app.meetingFinalizationRetryQueued["retry"] = true
	if !app.canonicalReconcileDeferred() {
		t.Fatal("queued finalization retry did not defer full parity")
	}
	delete(app.meetingFinalizationRetryQueued, "retry")
	app.meetingFinalizationRetryActive["retry"] = true
	if !app.canonicalReconcileDeferred() {
		t.Fatal("active finalization retry did not defer full parity")
	}
	delete(app.meetingFinalizationRetryActive, "retry")
	app.meetingFinalizationQueuedPriority["fresh"] = true
	if !app.canonicalReconcileDeferred() {
		t.Fatal("fresh finalization did not defer full parity")
	}
}

func waitForMeetingArchiveFinalizationSync(t *testing.T, app *kanbanBoardApp, meetingID string) meetingRecord {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var record meetingRecord
	for time.Now().Before(deadline) {
		record, _ = app.meetings.recordByID(meetingID)
		if record.Finalization != nil && strings.TrimSpace(record.Finalization.ArchiveSyncedAt) != "" {
			return record
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("meeting %s archive did not receive finalized receipt: %+v", meetingID, record.Finalization)
	return meetingRecord{}
}

func corruptMeetingFinalizationOutputRevisionForTest(t *testing.T, app *kanbanBoardApp, kind, id, marker string, remove bool) meetingMemoryEntry {
	t.Helper()
	app.memory.mu.Lock()
	defer app.memory.mu.Unlock()
	for index := range app.memory.entries {
		entry := app.memory.entries[index]
		if entry.Kind != kind || entry.ID != id {
			continue
		}
		entry = cloneMemoryEntry(entry)
		if remove {
			delete(entry.Metadata, meetingFinalizationOutputRevisionMetadataKey)
		} else {
			entry.Metadata[meetingFinalizationOutputRevisionMetadataKey] = marker
		}
		app.memory.entries[index] = entry
		if err := app.memory.rewriteLocked(false); err != nil {
			t.Fatalf("persist corrupt %s revision marker: %v", kind, err)
		}
		return cloneMemoryEntry(entry)
	}
	t.Fatalf("missing %s finalization output %s", kind, id)
	return meetingMemoryEntry{}
}

func TestJoinInstallsProviderBeforeRecoveryAndWorkers(t *testing.T) {
	raw, err := os.ReadFile("kanban.go")
	if err != nil {
		t.Fatal(err)
	}
	section := sourceSectionForAdmissionTest(t, string(raw), "func (app *kanbanBoardApp) JoinConferenceRoom() error", "\n}\n")
	providerAt := strings.Index(section, "app.apiKey = apiKey")
	recoveryAt := strings.Index(section, "app.resumeMeetingFinalizationsAtBoot(apiKey)")
	workerAt := strings.Index(section, "app.startMeetingBrainWorker(apiKey)")
	if providerAt < 0 || recoveryAt < 0 || workerAt < 0 || !(providerAt < recoveryAt && recoveryAt < workerAt) {
		t.Fatalf("Join order provider=%d recovery=%d first-worker=%d", providerAt, recoveryAt, workerAt)
	}
}

func TestMeetingFinalizationCorePersistsIdempotentReceiptsAndReadTruth(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	app.noteMeetingAdmission(officeRoomID, "AJ")
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	appendTestTranscript(t, app, "finalize-tx-1", "We chose the durable launch plan and Tyler will publish the checklist.")
	record, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("missing active meeting")
	}
	strideConfig := strideIntegratedRuntimeConfig(t.TempDir())
	liveRuntime, err := NewSTRIDERuntime(strideConfig)
	if err != nil {
		t.Fatal(err)
	}
	app.strideRuntime = liveRuntime
	liveBrain, err := NewTemporalMeetingBrain(TemporalMeetingBrainConfig{TenantID: canonicalTenantID(), RoomID: officeRoomID, SittingID: record.ID, SittingStart: time.Now().UTC().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	liveRuntime.liveTemporal[strideRuntimeTemporalKey(officeRoomID, record.ID)] = liveBrain
	source := app.meetingFinalizationSource(record.ID)
	closed, changed, err := app.meetings.endMeetingWithFinalization(record.ID, time.Now().UTC(), meetingEndedReasonIdle, "", source)
	if err != nil || !changed || closed.Finalization == nil || closed.Finalization.State != meetingFinalizationClosing {
		t.Fatalf("begin finalization record=%+v changed=%v err=%v", closed, changed, err)
	}
	var calls atomic.Int32
	finalized, err := app.finalizeMeetingCore(context.Background(), record.ID, meetingFinalizationResponder(&calls, "finalize-tx-1"))
	if err != nil {
		t.Fatal(err)
	}
	if finalized.Finalization == nil || finalized.Finalization.State != meetingFinalizationFinalized {
		t.Fatalf("finalization=%+v, want finalized", finalized.Finalization)
	}
	if _, found := liveRuntime.liveTemporal[strideRuntimeTemporalKey(officeRoomID, record.ID)]; found {
		t.Fatal("durable meeting finalization retained raw live temporal brain")
	}
	if !finalized.Finalization.Source.equal(source) || finalized.Finalization.ObservedCaptureSequence != source.CaptureSequence {
		t.Fatalf("receipt source=%+v observed=%d, want %+v", finalized.Finalization.Source, finalized.Finalization.ObservedCaptureSequence, source)
	}
	if finalized.Finalization.Brain.State != meetingFinalizationStageComplete || finalized.Finalization.Brain.OutputID == "" ||
		finalized.Finalization.Digest.State != meetingFinalizationStageComplete || finalized.Finalization.Digest.OutputID == "" ||
		finalized.Finalization.Actions.State != meetingFinalizationStageComplete || finalized.Finalization.Actions.ItemCount != 1 {
		t.Fatalf("incomplete core stage receipts: %+v", finalized.Finalization)
	}
	payload := app.meetingRecordPayload(finalized, time.Now().UTC())
	if payload["finalizationState"] != meetingFinalizationFinalized || payload["analysisReady"] != true || payload["finalization"] == nil {
		t.Fatalf("read payload hides receipt truth: %+v", payload)
	}
	firstCalls := calls.Load()
	second, err := app.finalizeMeetingCore(context.Background(), record.ID, meetingFinalizationResponder(&calls, "finalize-tx-1"))
	if err != nil || second.Finalization == nil || second.Finalization.State != meetingFinalizationFinalized {
		t.Fatalf("idempotent retry=%+v err=%v", second.Finalization, err)
	}
	if got := calls.Load(); got != firstCalls {
		t.Fatalf("finalized retry made %d additional provider calls", got-firstCalls)
	}

	// Simulate an output disappearing after the receipt was sealed but before a
	// restart audit can reopen it. The structural helper still reflects the
	// receipt, while every production payload must fail readiness closed against
	// the exact durable outputs.
	if _, deleted, err := app.memory.deleteEntryByID(second.Finalization.Brain.OutputID); err != nil || !deleted {
		t.Fatalf("delete sealed Brain output deleted=%v err=%v", deleted, err)
	}
	if structural := meetingRecordPayload(second, time.Now().UTC()); structural["analysisReady"] != true {
		t.Fatalf("structural receipt fixture unexpectedly not ready: %+v", structural)
	}
	if validated := app.meetingRecordPayload(second, time.Now().UTC()); validated["analysisReady"] != false {
		t.Fatalf("validated payload exposed missing output as ready: %+v", validated)
	}

	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	req := httptest.NewRequest(http.MethodGet, "/assistant/meetings", nil)
	for _, cookie := range loginAs(t, "tom@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantMeetingsHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("meeting list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var listPayload struct {
		Meetings []map[string]any `json:"meetings"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("decode meeting list: %v", err)
	}
	foundMissingOutputMeeting := false
	for _, item := range listPayload.Meetings {
		if item["id"] != second.ID {
			continue
		}
		foundMissingOutputMeeting = true
		if item["analysisReady"] != false {
			t.Fatalf("production meeting list exposed missing output as ready: %+v", item)
		}
	}
	if !foundMissingOutputMeeting {
		t.Fatalf("production meeting list omitted authorized meeting %s: %s", second.ID, recorder.Body.String())
	}
}

func TestMeetingFinalizationRestartResumesClosingExactlyOnce(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	app.noteMeetingAdmission(officeRoomID, "AJ")
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	appendTestTranscript(t, app, "restart-finalize-tx", "The team committed to publish the release checklist.")
	record, _ := app.meetings.activeRecord(officeRoomID)
	source := app.meetingFinalizationSource(record.ID)
	if _, changed, err := app.meetings.endMeetingWithFinalization(record.ID, time.Now().UTC(), meetingEndedReasonRestart, "", source); err != nil || !changed {
		t.Fatalf("durable close changed=%v err=%v", changed, err)
	}

	restartedMemory, err := newMeetingMemoryStore(app.memory.path)
	if err != nil {
		t.Fatal(err)
	}
	restartedMeetings, err := loadMeetingStore(app.meetings.path)
	if err != nil {
		t.Fatal(err)
	}
	restarted := &kanbanBoardApp{memory: restartedMemory, meetings: restartedMeetings, apiKey: "test-key", meetingFinalizationRunning: map[string]struct{}{}}
	if err := restarted.initializeAdmissionAnchorStore(app.admissionAnchors.path); err != nil {
		t.Fatal(err)
	}
	restartedMemory.transcriptFenceHook = restarted.prepareTranscriptFinalizationFence
	restartedMemory.transcriptCommitHook = restarted.handleDurableTranscriptCommit
	var calls atomic.Int32
	originalResponder := createOpenAITextResponse
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })
	createOpenAITextResponse = meetingFinalizationResponder(&calls, "restart-finalize-tx")

	restarted.resumeMeetingFinalizationsAtBoot("test-key")
	waitForMeetingFinalizationState(t, restarted, record.ID, meetingFinalizationFinalized)
	waitForMeetingFinalizationQueueIdle(t, restarted)
	firstCalls := calls.Load()
	if firstCalls != 2 {
		t.Fatalf("restart provider calls=%d, want one Brain and one digest", firstCalls)
	}
	restarted.resumeMeetingFinalizationsAtBoot("test-key")
	waitForMeetingFinalizationQueueIdle(t, restarted)
	if got := calls.Load(); got != firstCalls {
		t.Fatalf("second boot recovery duplicated %d provider calls", got-firstCalls)
	}
}

func TestMeetingFinalizationTransientFailureRetriesWithBoundedBackoff(t *testing.T) {
	t.Setenv("MEETING_FINALIZATION_RETRY_BASE", "200ms")
	t.Setenv("MEETING_FINALIZATION_RETRY_MAX", "200ms")
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	app.noteMeetingAdmission(officeRoomID, "AJ")
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	appendTestTranscript(t, app, "retry-finalize-tx", "The team approved the retry-safe launch plan.")
	record, _ := app.meetings.activeRecord(officeRoomID)
	source := app.meetingFinalizationSource(record.ID)
	if _, changed, err := app.meetings.endMeetingWithFinalization(record.ID, time.Now().UTC(), meetingEndedReasonRestart, "", source); err != nil || !changed {
		t.Fatalf("durable close changed=%v err=%v", changed, err)
	}

	originalResponder := createOpenAITextResponse
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })
	var calls atomic.Int32
	createOpenAITextResponse = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		call := calls.Add(1)
		if call == 1 {
			return "", &openAIProviderFailure{err: errors.New("injected transient provider outage")}
		}
		if strings.Contains(request.Instructions, "meeting digest compiler") {
			return cannedArchiveMeetingDigestJSON("retry-finalize-tx"), nil
		}
		return "## Overview\nThe retry-safe launch plan was approved.\n## Transcript reference\nretry-finalize-tx", nil
	}

	app.scheduleMeetingCoreFinalization(record.ID)
	degraded := waitForMeetingFinalizationState(t, app, record.ID, meetingFinalizationDegraded)
	if degraded.Finalization.RetryAttempt != 1 || strings.TrimSpace(degraded.Finalization.RetryAfter) == "" || degraded.Finalization.LastError != "provider_unavailable" {
		t.Fatalf("transient failure lacks durable retry authority: %+v", degraded.Finalization)
	}
	retryAt, err := time.Parse(time.RFC3339Nano, degraded.Finalization.RetryAfter)
	if err != nil || time.Until(retryAt) <= 0 {
		t.Fatalf("retryAfter=%q err=%v, want a bounded future retry", degraded.Finalization.RetryAfter, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		app.meetingFinalizationRunMu.Lock()
		timerInstalled := app.meetingFinalizationRetryTimers[record.ID] != nil
		app.meetingFinalizationRunMu.Unlock()
		if timerInstalled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("transient failure did not install an in-process retry timer")
		}
		time.Sleep(5 * time.Millisecond)
	}

	finalized := waitForMeetingFinalizationState(t, app, record.ID, meetingFinalizationFinalized)
	waitForMeetingFinalizationQueueIdle(t, app)
	if finalized.Finalization.RetryAttempt != 0 || finalized.Finalization.RetryAfter != "" || finalized.Finalization.LastError != "" {
		t.Fatalf("successful retry retained stale backoff state: %+v", finalized.Finalization)
	}
	if got := calls.Load(); got < 3 {
		t.Fatalf("provider calls=%d, want failed Brain plus successful Brain and digest", got)
	}
	app.meetingFinalizationRunMu.Lock()
	pendingTimer := app.meetingFinalizationRetryTimers[record.ID] != nil
	app.meetingFinalizationRunMu.Unlock()
	if pendingTimer {
		t.Fatal("successful retry left another timer armed")
	}
}

func TestMeetingFinalizationBootHonorsDurableRetryAfter(t *testing.T) {
	t.Setenv("MEETING_FINALIZATION_RETRY_BASE", "1s")
	t.Setenv("MEETING_FINALIZATION_RETRY_MAX", "1s")
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	app.noteMeetingAdmission(officeRoomID, "AJ")
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	appendTestTranscript(t, app, "restart-retry-finalize-tx", "The durable retry must survive a process restart.")
	record, _ := app.meetings.activeRecord(officeRoomID)
	source := app.meetingFinalizationSource(record.ID)
	if _, changed, err := app.meetings.endMeetingWithFinalization(record.ID, time.Now().UTC(), meetingEndedReasonRestart, "", source); err != nil || !changed {
		t.Fatalf("durable close changed=%v err=%v", changed, err)
	}
	if _, err := app.finalizeMeetingCore(context.Background(), record.ID, func(context.Context, string, openAITextRequest) (string, error) {
		return "", &openAIProviderFailure{err: errors.New("injected pre-restart provider outage")}
	}); err == nil {
		t.Fatal("pre-restart finalization unexpectedly succeeded")
	}
	degraded, _ := app.meetings.recordByID(record.ID)
	if degraded.Finalization == nil || degraded.Finalization.State != meetingFinalizationDegraded || degraded.Finalization.RetryAttempt != 1 {
		t.Fatalf("pre-restart receipt=%+v, want durable degraded retry", degraded.Finalization)
	}
	retryAt, err := time.Parse(time.RFC3339Nano, degraded.Finalization.RetryAfter)
	if err != nil || time.Until(retryAt) < 500*time.Millisecond {
		t.Fatalf("pre-restart retryAt=%q remaining=%v err=%v", degraded.Finalization.RetryAfter, time.Until(retryAt), err)
	}

	originalResponder := createOpenAITextResponse
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })
	var calls atomic.Int32
	createOpenAITextResponse = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls.Add(1)
		if strings.Contains(request.Instructions, "meeting digest compiler") {
			return cannedArchiveMeetingDigestJSON("restart-retry-finalize-tx"), nil
		}
		return "## Overview\nThe durable retry resumed after restart.\n## Transcript reference\nrestart-retry-finalize-tx", nil
	}

	restarted := newKanbanBoardApp()
	restarted.mu.Lock()
	restarted.apiKey = "test-key"
	restarted.mu.Unlock()
	t.Cleanup(func() {
		restarted.stopMeetingFinalizationRetries()
		restarted.stopRoomArchiveCloseRetries()
		restarted.meetings.stopIdleEndsAndWait()
	})
	restarted.resumeMeetingFinalizationsAtBoot("test-key")
	restarted.meetingFinalizationRunMu.Lock()
	timerInstalled := restarted.meetingFinalizationRetryTimers[record.ID] != nil
	queuedImmediately := len(restarted.meetingFinalizationQueue) + len(restarted.meetingFinalizationBacklog)
	restarted.meetingFinalizationRunMu.Unlock()
	if !timerInstalled || queuedImmediately != 0 || calls.Load() != 0 {
		t.Fatalf("boot ignored durable RetryAfter: timer=%v queued=%d calls=%d", timerInstalled, queuedImmediately, calls.Load())
	}

	finalized := waitForMeetingFinalizationState(t, restarted, record.ID, meetingFinalizationFinalized)
	waitForMeetingFinalizationQueueIdle(t, restarted)
	if finalized.Finalization.RetryAttempt != 0 || finalized.Finalization.RetryAfter != "" || calls.Load() < 2 {
		t.Fatalf("restart retry did not settle exactly: receipt=%+v calls=%d", finalized.Finalization, calls.Load())
	}
}

func TestMeetingFinalizationSealCannotBeatDurableTranscriptObservation(t *testing.T) {
	store, err := loadMeetingStore(filepath.Join(t.TempDir(), "meetings.json"))
	if err != nil {
		t.Fatal(err)
	}
	const iterations = 32
	for index := 0; index < iterations; index++ {
		meetingID := "race-meeting-" + strconv.Itoa(index)
		startedAt := time.Now().UTC().Add(-time.Minute)
		if _, _, err := store.startMeetingDurable(officeRoomID, meetingID, startedAt, nil); err != nil {
			t.Fatal(err)
		}
		source := meetingFinalizationSourceHighWater{TranscriptID: "tx-1", CaptureSequence: 1, TranscriptCount: 1, ManifestDigest: "source-one"}
		if _, changed, err := store.endMeetingWithFinalization(meetingID, time.Now().UTC(), meetingEndedReasonIdle, "", source); err != nil || !changed {
			t.Fatalf("iteration %d close changed=%v err=%v", index, changed, err)
		}
		for _, stage := range []string{meetingFinalizationStageBrain, meetingFinalizationStageDigest, meetingFinalizationStageActions} {
			if _, err := store.markFinalizationStage(meetingID, stage, meetingFinalizationStageComplete, stage+"-output", "", "", 0, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, _ = store.markFinalizationComplete(meetingID, source, time.Now().UTC())
		}()
		go func() {
			defer wg.Done()
			<-start
			_, _, _ = store.observeEndedTranscript(meetingID, "tx-2", 2, time.Now().UTC())
		}()
		close(start)
		wg.Wait()
		got, _ := store.recordByID(meetingID)
		if got.Finalization == nil || got.Finalization.State == meetingFinalizationFinalized || got.Finalization.ObservedCaptureSequence != 2 {
			t.Fatalf("iteration %d falsely sealed stale source: %+v", index, got.Finalization)
		}
	}
}

func TestFailedTranscriptAppendAndCorrectionResealVisibleSourceWithoutRestart(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	app.noteMeetingAdmission(officeRoomID, "AJ")
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	appendTestTranscript(t, app, "failed-mutation-source", "The durable source stays unchanged when its file write fails.")
	record, _ := app.meetings.activeRecord(officeRoomID)
	source := app.meetingFinalizationSource(record.ID)
	if _, changed, err := app.meetings.endMeetingWithFinalization(record.ID, time.Now().UTC(), meetingEndedReasonRestart, "", source); err != nil || !changed {
		t.Fatalf("durable close changed=%v err=%v", changed, err)
	}
	var initialCalls atomic.Int32
	finalized, err := app.finalizeMeetingCore(context.Background(), record.ID, meetingFinalizationResponder(&initialCalls, "failed-mutation-source"))
	if err != nil || !app.meetingFinalizationOutputsReady(finalized) {
		t.Fatalf("initial finalization err=%v receipt=%+v", err, finalized.Finalization)
	}
	initialObservedRevision := finalized.Finalization.ObservedRevision

	originalResponder := createOpenAITextResponse
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })
	var repairProviderCalls atomic.Int32
	createOpenAITextResponse = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		repairProviderCalls.Add(1)
		if strings.Contains(request.Instructions, "meeting digest compiler") {
			return cannedArchiveMeetingDigestJSON("failed-mutation-source"), nil
		}
		return "## Overview\nThe unchanged source was resealed.\n## Transcript reference\nfailed-mutation-source", nil
	}

	originalPath := app.memory.path
	badAppendPath := t.TempDir()
	app.memory.mu.Lock()
	app.memory.path = badAppendPath
	app.memory.mu.Unlock()
	_, appended, appendErr := app.memory.appendEntryForMeeting(officeRoomID, meetingMemoryKindTranscript, "failed-late-append", "This row must not become durable.", nil, record.ID)
	app.memory.mu.Lock()
	app.memory.path = originalPath
	app.memory.mu.Unlock()
	if appendErr == nil || appended {
		t.Fatalf("append to directory path appended=%v err=%v, want a post-fence persistence failure", appended, appendErr)
	}
	if _, found := app.memory.entryByID("failed-late-append"); found {
		t.Fatal("failed transcript append leaked into visible memory")
	}
	appendResealed := waitForMeetingFinalizationState(t, app, record.ID, meetingFinalizationFinalized)
	waitForMeetingFinalizationQueueIdle(t, app)
	if !appendResealed.Finalization.Source.equal(source) || appendResealed.Finalization.ObservedRevision != initialObservedRevision+1 || !app.meetingFinalizationOutputsReady(appendResealed) {
		t.Fatalf("failed append stranded or changed finalization truth: before=%+v after=%+v", source, appendResealed.Finalization)
	}

	originalEntry, found := app.memory.entryByID("failed-mutation-source")
	if !found {
		t.Fatal("original transcript disappeared")
	}
	badCorrectionPath := t.TempDir()
	app.memory.mu.Lock()
	app.memory.path = badCorrectionPath
	app.memory.mu.Unlock()
	_, changed, correctionErr := app.memory.updateEntryWithMetadata(meetingMemoryKindTranscript, originalEntry.ID, originalEntry.Text+" This correction must roll back.", nil)
	app.memory.mu.Lock()
	app.memory.path = originalPath
	app.memory.mu.Unlock()
	if correctionErr == nil || changed {
		t.Fatalf("correction to directory path changed=%v err=%v, want a post-fence persistence failure", changed, correctionErr)
	}
	persisted, found := app.memory.entryByID(originalEntry.ID)
	if !found || persisted.Text != originalEntry.Text {
		t.Fatalf("failed correction changed visible transcript: before=%q after=%q found=%v", originalEntry.Text, persisted.Text, found)
	}
	correctionResealed := waitForMeetingFinalizationState(t, app, record.ID, meetingFinalizationFinalized)
	waitForMeetingFinalizationQueueIdle(t, app)
	if !correctionResealed.Finalization.Source.equal(source) || correctionResealed.Finalization.ObservedRevision != initialObservedRevision+2 || !app.meetingFinalizationOutputsReady(correctionResealed) {
		t.Fatalf("failed correction stranded or changed finalization truth: before=%+v after=%+v", source, correctionResealed.Finalization)
	}
	if got := repairProviderCalls.Load(); got != 0 {
		t.Fatalf("idempotent compensation regenerated unchanged outputs with %d provider calls", got)
	}
}

func TestFinalizationOutputMutationRevokesReadinessAndRegeneratesExactBindings(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	app.noteMeetingAdmission(officeRoomID, "AJ")
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	appendTestTranscript(t, app, "output-mutation-source", "The immutable output binding must detect every body edit.")
	record, _ := app.meetings.activeRecord(officeRoomID)
	source := app.meetingFinalizationSource(record.ID)
	if _, changed, err := app.meetings.endMeetingWithFinalization(record.ID, time.Now().UTC(), meetingEndedReasonRestart, "", source); err != nil || !changed {
		t.Fatalf("durable close changed=%v err=%v", changed, err)
	}
	var initialCalls atomic.Int32
	initial, err := app.finalizeMeetingCore(context.Background(), record.ID, meetingFinalizationResponder(&initialCalls, "output-mutation-source"))
	if err != nil || !app.meetingFinalizationOutputsReady(initial) {
		t.Fatalf("initial finalization err=%v receipt=%+v", err, initial.Finalization)
	}
	oldBrainID := initial.Finalization.Brain.OutputID
	oldDigestID := initial.Finalization.Digest.OutputID
	oldBrain, found := app.memory.entryByID(oldBrainID)
	if !found {
		t.Fatalf("missing sealed Brain %s", oldBrainID)
	}
	if stored := oldBrain.Metadata[meetingFinalizationOutputDigestMetadataKey]; stored == "" || stored != sha256Hex([]byte(oldBrain.Text)) {
		t.Fatalf("initial Brain lacks immutable body digest: metadata=%q body=%q", stored, sha256Hex([]byte(oldBrain.Text)))
	}

	repairStarted := make(chan struct{})
	releaseRepair := make(chan struct{})
	var startedOnce, releaseOnce sync.Once
	originalResponder := createOpenAITextResponse
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseRepair) })
		createOpenAITextResponse = originalResponder
	})
	createOpenAITextResponse = func(ctx context.Context, _ string, request openAITextRequest) (string, error) {
		if !strings.Contains(request.Instructions, "meeting digest compiler") {
			startedOnce.Do(func() { close(repairStarted) })
			select {
			case <-releaseRepair:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			return "## Overview\nThe regenerated Brain is bound to this exact body.\n## Transcript reference\noutput-mutation-source", nil
		}
		return cannedArchiveMeetingDigestJSON("output-mutation-source"), nil
	}

	mutated, changed, err := app.memory.updateEntryWithMetadata(meetingMemoryKindBrain, oldBrainID, oldBrain.Text+"\nTampered after finalization.", nil)
	if err != nil || !changed {
		t.Fatalf("mutate finalized Brain changed=%v err=%v", changed, err)
	}
	select {
	case <-repairStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("output mutation did not schedule in-process repair")
	}
	if revision, ok := meetingFinalizationOutputRevision(mutated); !ok || revision != 2 {
		t.Fatalf("mutated output revision=%d valid=%v, want valid revision 2", revision, ok)
	}
	if stored := mutated.Metadata[meetingFinalizationOutputDigestMetadataKey]; stored == sha256Hex([]byte(mutated.Text)) {
		t.Fatal("body mutation rewrote the immutable generation digest")
	}
	reopened, _ := app.meetings.recordByID(record.ID)
	if app.meetingRecordPayload(reopened, time.Now().UTC())["analysisReady"] != false || app.meetingFinalizationOutputsReady(reopened) {
		t.Fatalf("mutated output remained analysisReady while repair was blocked: %+v", reopened.Finalization)
	}

	releaseOnce.Do(func() { close(releaseRepair) })
	repaired := waitForMeetingFinalizationState(t, app, record.ID, meetingFinalizationFinalized)
	waitForMeetingFinalizationQueueIdle(t, app)
	if !app.meetingFinalizationOutputsReady(repaired) || app.meetingRecordPayload(repaired, time.Now().UTC())["analysisReady"] != true {
		t.Fatalf("regenerated output bindings did not restore readiness: %+v", repaired.Finalization)
	}
	if repaired.Finalization.Brain.OutputID == oldBrainID || repaired.Finalization.Digest.OutputID == oldDigestID {
		t.Fatalf("repair reused mutated generation: before brain=%s digest=%s after=%+v", oldBrainID, oldDigestID, repaired.Finalization)
	}
	for stageName, stage := range map[string]meetingFinalizationStageReceipt{
		"brain": repaired.Finalization.Brain, "digest": repaired.Finalization.Digest, "actions": repaired.Finalization.Actions,
	} {
		entry, found := app.memory.entryByID(stage.OutputID)
		if !found || !meetingFinalizationStageOutputMatches(stage, entry) {
			t.Fatalf("%s receipt is not bound to its exact immutable output: stage=%+v entry=%+v found=%v", stageName, stage, entry, found)
		}
	}
}

func TestFinalizationOutputRevisionMetadataFailsClosedAndRepairsBrainAndDigest(t *testing.T) {
	markers := []struct {
		name   string
		value  string
		remove bool
	}{
		{name: "missing", remove: true},
		{name: "malformed", value: "not-a-revision"},
		{name: "zero", value: "0"},
		{name: "superseded", value: "2"},
	}
	outputs := []struct {
		name string
		kind string
		id   func(meetingFinalizationReceipt) string
	}{
		{name: "brain", kind: meetingMemoryKindBrain, id: func(receipt meetingFinalizationReceipt) string { return receipt.Brain.OutputID }},
		{name: "digest", kind: meetingMemoryKindMeetingDigest, id: func(receipt meetingFinalizationReceipt) string { return receipt.Digest.OutputID }},
	}

	for _, output := range outputs {
		output := output
		for _, marker := range markers {
			marker := marker
			t.Run(output.name+"/"+marker.name, func(t *testing.T) {
				app := newIsolatedKanbanBoardApp(t)
				app.mu.Lock()
				app.apiKey = "test-key"
				app.mu.Unlock()
				app.noteMeetingAdmission(officeRoomID, "AJ")
				authority := newAmbientConsentAuthorityForTest(t)
				grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
				anchor := "revision-marker-" + output.name + "-" + marker.name
				appendTestTranscript(t, app, anchor, "The finalized analysis must stay bound to an explicit immutable output revision.")
				record, ok := app.meetings.activeRecord(officeRoomID)
				if !ok {
					t.Fatal("missing active meeting")
				}
				source := app.meetingFinalizationSource(record.ID)
				if _, changed, err := app.meetings.endMeetingWithFinalization(record.ID, time.Now().UTC(), meetingEndedReasonRestart, "", source); err != nil || !changed {
					t.Fatalf("durable close changed=%v err=%v", changed, err)
				}
				var calls atomic.Int32
				finalized, err := app.finalizeMeetingCore(context.Background(), record.ID, meetingFinalizationResponder(&calls, anchor))
				if err != nil || !app.meetingFinalizationOutputsReady(finalized) {
					t.Fatalf("initial finalization err=%v receipt=%+v", err, finalized.Finalization)
				}
				originalOutputID := output.id(*finalized.Finalization)
				corrupted := corruptMeetingFinalizationOutputRevisionForTest(t, app, output.kind, originalOutputID, marker.value, marker.remove)
				revision, revisionOK := meetingFinalizationOutputRevision(corrupted)
				if marker.name == "superseded" {
					if !revisionOK || revision != 2 {
						t.Fatalf("revision parser rejected explicit positive revision: revision=%d valid=%v", revision, revisionOK)
					}
				} else if revisionOK || revision != 0 {
					t.Fatalf("revision parser accepted %s marker: revision=%d valid=%v", marker.name, revision, revisionOK)
				}
				stale, found := app.meetings.recordByID(record.ID)
				if !found {
					t.Fatal("missing finalized meeting after output corruption")
				}
				if meetingFinalizationOutputEntryIntact(corrupted) || app.meetingFinalizationOutputsReady(stale) || app.meetingRecordPayload(stale, time.Now().UTC())["analysisReady"] != false {
					t.Fatalf("%s %s marker remained analysisReady: revision=%d valid=%v receipt=%+v", output.name, marker.name, revision, revisionOK, stale.Finalization)
				}

				repaired, err := app.finalizeMeetingCore(context.Background(), record.ID, meetingFinalizationResponder(&calls, anchor))
				if err != nil {
					t.Fatalf("repair finalization: %v", err)
				}
				if !app.meetingFinalizationOutputsReady(repaired) || app.meetingRecordPayload(repaired, time.Now().UTC())["analysisReady"] != true {
					t.Fatalf("%s %s marker repair did not restore exact readiness: %+v", output.name, marker.name, repaired.Finalization)
				}
				if repairedOutputID := output.id(*repaired.Finalization); repairedOutputID == originalOutputID {
					t.Fatalf("%s %s marker repair reused corrupt output %s", output.name, marker.name, originalOutputID)
				}
			})
		}
	}
}

func TestMeetingFinalizationSealCannotBeatSameIDCorrection(t *testing.T) {
	store, err := loadMeetingStore(filepath.Join(t.TempDir(), "meetings.json"))
	if err != nil {
		t.Fatal(err)
	}
	const iterations = 32
	for index := 0; index < iterations; index++ {
		meetingID := "correction-race-meeting-" + strconv.Itoa(index)
		if _, _, err := store.startMeetingDurable(officeRoomID, meetingID, time.Now().UTC().Add(-time.Minute), nil); err != nil {
			t.Fatal(err)
		}
		source := meetingFinalizationSourceHighWater{TranscriptID: "tx-1", CaptureSequence: 1, TranscriptCount: 1, ManifestDigest: "source-before-correction"}
		if _, changed, err := store.endMeetingWithFinalization(meetingID, time.Now().UTC(), meetingEndedReasonIdle, "", source); err != nil || !changed {
			t.Fatalf("iteration %d close changed=%v err=%v", index, changed, err)
		}
		for _, stage := range []string{meetingFinalizationStageBrain, meetingFinalizationStageDigest, meetingFinalizationStageActions} {
			if _, err := store.markFinalizationStage(meetingID, stage, meetingFinalizationStageComplete, stage+"-output", "", "", 0, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, _ = store.markFinalizationComplete(meetingID, source, time.Now().UTC())
		}()
		go func() {
			defer wg.Done()
			<-start
			_, _, _ = store.observeEndedTranscript(meetingID, "tx-1", 1, time.Now().UTC())
		}()
		close(start)
		wg.Wait()
		got, _ := store.recordByID(meetingID)
		if got.Finalization == nil || got.Finalization.State == meetingFinalizationFinalized || got.Finalization.ObservedRevision != 1 || got.Finalization.SourceObservedRevision != 0 {
			t.Fatalf("iteration %d falsely sealed same-id correction: %+v", index, got.Finalization)
		}
	}
}

func TestTranscriptMutationFailsClosedWhenFinalizationFenceCannotPersist(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	app.noteMeetingAdmission(officeRoomID, "AJ")
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	appendTestTranscript(t, app, "fence-persist-tx", "The launch is Tuesday.")
	record, _ := app.meetings.activeRecord(officeRoomID)
	if _, changed, err := app.meetings.endMeetingWithFinalization(record.ID, time.Now().UTC(), meetingEndedReasonIdle, "", app.meetingFinalizationSource(record.ID)); err != nil || !changed {
		t.Fatalf("close changed=%v err=%v", changed, err)
	}
	var calls atomic.Int32
	if _, err := app.finalizeMeetingCore(context.Background(), record.ID, meetingFinalizationResponder(&calls, "fence-persist-tx")); err != nil {
		t.Fatal(err)
	}
	before, found := app.memory.entryByKindAndID(meetingMemoryKindTranscript, "fence-persist-tx")
	if !found {
		t.Fatal("missing transcript before fence failure")
	}

	// An existing directory is not a replaceable meetings file. The write-ahead
	// fence must reject the correction before memory is mutated.
	app.meetings.mu.Lock()
	originalPath := app.meetings.path
	app.meetings.path = t.TempDir()
	app.meetings.mu.Unlock()
	_, changed, err := app.memory.updateEntryWithMetadata(meetingMemoryKindTranscript, "fence-persist-tx", "The launch is Wednesday.", map[string]string{"correctionState": "corrected"})
	app.meetings.mu.Lock()
	app.meetings.path = originalPath
	app.meetings.mu.Unlock()
	if err == nil || changed {
		t.Fatalf("correction changed=%v err=%v, want fail-closed fence error", changed, err)
	}
	entry, found := app.memory.entryByKindAndID(meetingMemoryKindTranscript, "fence-persist-tx")
	if !found || entry.Text != before.Text || entry.BodyDigest != before.BodyDigest {
		t.Fatalf("failed fence mutated transcript: %+v", entry)
	}
	settled, _ := app.meetings.recordByID(record.ID)
	if settled.Finalization == nil || settled.Finalization.State != meetingFinalizationFinalized {
		t.Fatalf("failed fence did not roll receipt back: %+v", settled.Finalization)
	}
}

func TestOrdinaryDigestCannotSupersedeReceiptedFinalDigest(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	const meetingID = "pinned-final-digest"
	final, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, meetingID, cannedArchiveMeetingDigestJSON("pinned-source"), map[string]string{
		"meetingId": meetingID,
		meetingFinalizationSourceDigestMetadataKey: "manifest-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ordinary, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, meetingID, cannedArchiveMeetingDigestJSON("ordinary-source"), map[string]string{"meetingId": meetingID})
	if err != nil {
		t.Fatal(err)
	}
	if ordinary.ID != final.ID {
		t.Fatalf("ordinary worker superseded final digest: final=%s ordinary=%s", final.ID, ordinary.ID)
	}
	current, found := app.memory.currentDigest(meetingMemoryKindMeetingDigest, meetingID)
	if !found || current.ID != final.ID || current.Metadata[meetingFinalizationSourceDigestMetadataKey] != "manifest-v1" {
		t.Fatalf("current final digest was not pinned: %+v", current)
	}
}

func TestMeetingFinalizationSourceDigestIsIndependentOfPhysicalReplayOrder(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	meetingID := app.memory.ensureMeetingID(officeRoomID)
	first, appended, err := app.memory.appendEntryForMeeting(officeRoomID, meetingMemoryKindTranscript, "ordered-tx-1", "first", map[string]string{"meetingId": meetingID}, meetingID)
	if err != nil || !appended {
		t.Fatalf("first append=%v err=%v", appended, err)
	}
	second, appended, err := app.memory.appendEntryForMeeting(officeRoomID, meetingMemoryKindTranscript, "ordered-tx-2", "second", map[string]string{"meetingId": meetingID}, meetingID)
	if err != nil || !appended {
		t.Fatalf("second append=%v err=%v", appended, err)
	}
	before := app.meetingFinalizationSource(meetingID)
	app.memory.mu.Lock()
	app.memory.entries[0], app.memory.entries[1] = app.memory.entries[1], app.memory.entries[0]
	app.memory.rebuildMeetingEntryIndexesLocked()
	app.memory.mu.Unlock()
	after := app.meetingFinalizationSource(meetingID)
	if !before.equal(after) {
		t.Fatalf("source changed after replay reorder: before=%+v after=%+v", before, after)
	}
	if after.TranscriptID != second.ID || after.TranscriptID == first.ID {
		t.Fatalf("high-water transcript=%q, want capture-order %q", after.TranscriptID, second.ID)
	}
}

func TestMeetingFinalizationBootAuditsCrashBetweenTranscriptAndReceipt(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	app.noteMeetingAdmission(officeRoomID, "AJ")
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	appendTestTranscript(t, app, "boot-audit-tx-1", "Initial close source.")
	record, _ := app.meetings.activeRecord(officeRoomID)
	source := app.meetingFinalizationSource(record.ID)
	if _, changed, err := app.meetings.endMeetingWithFinalization(record.ID, time.Now().UTC(), meetingEndedReasonIdle, "", source); err != nil || !changed {
		t.Fatalf("close changed=%v err=%v", changed, err)
	}
	var initialCalls atomic.Int32
	if _, err := app.finalizeMeetingCore(context.Background(), record.ID, meetingFinalizationResponder(&initialCalls, "boot-audit-tx-1")); err != nil {
		t.Fatal(err)
	}
	// Simulate a legacy/process-fault write that bypassed both halves of the
	// write-ahead transcript fence. Boot audit must still reconcile the durable
	// source file against the stale receipt.
	app.memory.transcriptFenceHook = nil
	app.memory.transcriptCommitHook = nil
	late, appended, err := app.memory.appendAttributedTranscriptEntry(officeRoomID, "boot-audit-tx-2", "boot-audit-item-2", "Tom", "dominant", "Late accepted source.", nil, true, record.ID)
	if err != nil || !appended {
		t.Fatalf("late append=%v err=%v", appended, err)
	}
	stale, _ := app.meetings.recordByID(record.ID)
	if stale.Finalization == nil || stale.Finalization.State != meetingFinalizationFinalized || stale.Finalization.Source.TranscriptID == late.ID {
		t.Fatalf("fault seam did not leave intended stale finalized receipt: %+v", stale.Finalization)
	}

	restartedMemory, err := newMeetingMemoryStore(app.memory.path)
	if err != nil {
		t.Fatal(err)
	}
	restartedMeetings, err := loadMeetingStore(app.meetings.path)
	if err != nil {
		t.Fatal(err)
	}
	restarted := &kanbanBoardApp{memory: restartedMemory, meetings: restartedMeetings, apiKey: "test-key", meetingFinalizationRunning: map[string]struct{}{}}
	if err := restarted.initializeAdmissionAnchorStore(app.admissionAnchors.path); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	originalResponder := createOpenAITextResponse
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })
	createOpenAITextResponse = meetingFinalizationResponder(&calls, late.ID)
	restarted.resumeMeetingFinalizationsAtBoot("test-key")
	deadline := time.Now().Add(5 * time.Second)
	var repaired meetingRecord
	for time.Now().Before(deadline) {
		repaired, _ = restarted.meetings.recordByID(record.ID)
		if repaired.Finalization != nil && repaired.Finalization.State == meetingFinalizationFinalized && repaired.Finalization.Source.TranscriptID == late.ID && repaired.Finalization.Source.TranscriptCount == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	waitForMeetingFinalizationQueueIdle(t, restarted)
	if repaired.Finalization == nil || repaired.Finalization.State != meetingFinalizationFinalized || repaired.Finalization.Source.TranscriptID != late.ID || repaired.Finalization.Source.TranscriptCount != 2 {
		t.Fatalf("boot audit failed to cover late source: %+v", repaired.Finalization)
	}
	if calls.Load() == 0 {
		t.Fatal("boot audit did not resume core synthesis")
	}
}

func TestIdleCloseDoesNotWaitForCoreAndRejoinMintsSuccessor(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	app.noteMeetingAdmission(officeRoomID, "AJ")
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	appendTestTranscript(t, app, "slow-close-old", "The first sitting selected the durable launch plan.")
	oldRecord, found := app.meetings.activeRecord(officeRoomID)
	if !found {
		t.Fatal("missing old sitting")
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	originalResponder := createOpenAITextResponse
	t.Cleanup(func() {
		defer func() { createOpenAITextResponse = originalResponder }()
		releaseOnce.Do(func() { close(release) })
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			app.meetingFinalizationRunMu.Lock()
			idle := len(app.meetingFinalizationRunning) == 0 && app.meetingFinalizationWorkers == 0
			app.meetingFinalizationRunMu.Unlock()
			if idle {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
	var first sync.Once
	createOpenAITextResponse = func(ctx context.Context, _ string, request openAITextRequest) (string, error) {
		if !strings.Contains(request.Instructions, "meeting digest compiler") {
			first.Do(func() { close(started) })
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			return "## Overview\nThe first sitting selected the durable launch plan.\n## Transcript reference\nslow-close-old", nil
		}
		return cannedArchiveMeetingDigestJSON("slow-close-old"), nil
	}

	startedClose := time.Now()
	fireIdleEndNow(app)
	if elapsed := time.Since(startedClose); elapsed > 500*time.Millisecond {
		t.Fatalf("idle close waited %v for asynchronous core finalization", elapsed)
	}
	if got := app.memory.currentMeetingID(officeRoomID); got != "" {
		t.Fatalf("closed sitting id %q remained current while core was blocked", got)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("core finalization did not start in background")
	}

	newSittingID := app.prepareMeetingSittingID(officeRoomID)
	if newSittingID == "" || newSittingID == oldRecord.ID {
		t.Fatalf("successor id=%q, want a fresh id after %s", newSittingID, oldRecord.ID)
	}
	if _, _, err := app.admitParticipantWithAnchor(context.Background(), officeRoomID, "AJ", "successor-session", "successor-endpoint", newSittingID, memberAdmissionPrincipal("aj@example.com")); err != nil {
		t.Fatalf("successor admission: %v", err)
	}
	newEntry, appended, err := app.memory.appendEntryForMeeting(officeRoomID, meetingMemoryKindTranscript, "slow-close-new", "The successor sitting chose a different launch date.", map[string]string{"meetingId": newSittingID}, newSittingID)
	if err != nil || !appended {
		t.Fatalf("successor transcript appended=%v err=%v", appended, err)
	}
	if got := strings.TrimSpace(newEntry.Metadata["meetingId"]); got != newSittingID || got == oldRecord.ID {
		t.Fatalf("successor transcript meetingId=%q, want %s", got, newSittingID)
	}
	current, found := app.meetings.activeRecord(officeRoomID)
	if !found || current.ID != newSittingID {
		t.Fatalf("active successor=%+v found=%v", current, found)
	}

	releaseOnce.Do(func() { close(release) })
	finalized := waitForMeetingFinalizationState(t, app, oldRecord.ID, meetingFinalizationFinalized)
	waitForMeetingFinalizationQueueIdle(t, app)
	if finalized.Finalization.Source.TranscriptCount != 1 || finalized.Finalization.Source.TranscriptID == newEntry.ID {
		t.Fatalf("old receipt crossed sitting boundary: %+v", finalized.Finalization.Source)
	}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindBrain, 0) {
		if strings.TrimSpace(entry.Metadata["meetingId"]) == oldRecord.ID && strings.Contains(entry.Text, "different launch date") {
			t.Fatalf("old sitting Brain consumed successor source: %+v", entry)
		}
	}
}

func TestManualArchiveRotatesBeforeSlowCoreAndSuccessorStartsOnlyFromAnchor(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	oldSittingID := app.prepareMeetingSittingID(officeRoomID)
	if _, _, err := app.admitParticipantWithAnchor(context.Background(), officeRoomID, "AJ", "archive-old-session", "archive-endpoint", oldSittingID, memberAdmissionPrincipal("aj@example.com")); err != nil {
		t.Fatal(err)
	}
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	appendTestTranscript(t, app, "manual-archive-old", "The old sitting approved the launch plan.")

	started := make(chan struct{})
	release := make(chan struct{})
	var first, releaseOnce sync.Once
	originalResponder := createOpenAITextResponse
	t.Cleanup(func() {
		defer func() { createOpenAITextResponse = originalResponder }()
		releaseOnce.Do(func() { close(release) })
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			app.meetingFinalizationRunMu.Lock()
			idle := len(app.meetingFinalizationRunning) == 0 && app.meetingFinalizationWorkers == 0
			app.meetingFinalizationRunMu.Unlock()
			if idle {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
	createOpenAITextResponse = func(ctx context.Context, _ string, request openAITextRequest) (string, error) {
		if strings.Contains(request.Instructions, "meeting digest compiler") {
			return cannedArchiveMeetingDigestJSON("manual-archive-old"), nil
		}
		first.Do(func() { close(started) })
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return "## Overview\nThe old sitting approved the launch plan.\n## Transcript reference\nmanual-archive-old", nil
	}

	archiveStarted := time.Now()
	result, err := app.archiveMeeting("AJ")
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(archiveStarted); elapsed > time.Second {
		t.Fatalf("manual archive waited %v for asynchronous core finalization", elapsed)
	}
	if result.MeetingID != oldSittingID {
		t.Fatalf("archive meeting=%q, want %q", result.MeetingID, oldSittingID)
	}
	successorID := app.memory.currentMeetingID(officeRoomID)
	if successorID == "" || successorID == oldSittingID {
		t.Fatalf("archive successor id=%q, want a fresh pre-admission sitting", successorID)
	}
	active, found := app.meetings.activeRecord(officeRoomID)
	if !found || active.ID != successorID {
		t.Fatalf("archive anchored successor record=%+v found=%v", active, found)
	}
	starts, err := app.admissionAnchors.SittingStarts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hasSuccessorAnchor := false
	for _, start := range starts {
		if start.SittingID == successorID && start.Principal == memberAdmissionPrincipal("aj@shareability.com") {
			hasSuccessorAnchor = true
		}
	}
	if !hasSuccessorAnchor {
		t.Fatalf("successor %s opened without its durable carried-admission anchor: %+v", successorID, starts)
	}
	if _, _, err := app.admitParticipantWithAnchor(context.Background(), officeRoomID, "AJ", "archive-new-session", "archive-endpoint", successorID, memberAdmissionPrincipal("aj@example.com")); err != nil {
		t.Fatalf("anchored successor admission: %v", err)
	}
	active, found = app.meetings.activeRecord(officeRoomID)
	if !found || active.ID != successorID {
		t.Fatalf("anchored successor record=%+v found=%v", active, found)
	}
	successorEntry, appended, err := app.memory.appendEntryForMeeting(officeRoomID, meetingMemoryKindTranscript, "manual-archive-new", "The successor selected a new date.", map[string]string{"meetingId": successorID}, successorID)
	if err != nil || !appended || successorEntry.Metadata["meetingId"] != successorID {
		t.Fatalf("successor transcript=%+v appended=%v err=%v", successorEntry, appended, err)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("manual archive core finalization did not start")
	}
	releaseOnce.Do(func() { close(release) })
	finalized := waitForMeetingFinalizationState(t, app, oldSittingID, meetingFinalizationFinalized)
	waitForMeetingArchiveFinalizationSync(t, app, oldSittingID)
	waitForMeetingFinalizationQueueIdle(t, app)
	if finalized.Finalization.Source.TranscriptCount != 1 || finalized.Finalization.Source.TranscriptID == successorEntry.ID {
		t.Fatalf("manual archive crossed successor boundary: %+v", finalized.Finalization.Source)
	}
}

func TestManualArchiveFencesBlockedSuccessorPersistenceFromLeaveMediaAndChat(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	oldSittingID := app.prepareMeetingSittingID(officeRoomID)
	if _, _, err := app.admitParticipantWithAnchor(context.Background(), officeRoomID, "AJ", "archive-fence-session", "archive-fence-endpoint", oldSittingID, memberAdmissionPrincipal("aj@shareability.com")); err != nil {
		t.Fatal(err)
	}
	oldGeneration := app.ensureRoomMedia(officeRoomID)
	app.mu.Lock()
	oldScope := RoomScoutScope{RoomID: officeRoomID, SittingID: app.roomLiveLocked(officeRoomID).mediaSittingID, MediaGeneration: oldGeneration}
	app.mu.Unlock()
	appendTestTranscript(t, app, "archive-fence-old-source", "The predecessor chose the launch plan.")

	persistEntered := make(chan struct{})
	releasePersist := make(chan struct{})
	manualRolloverBeforeSuccessorPersist = func() {
		close(persistEntered)
		<-releasePersist
	}
	t.Cleanup(func() { manualRolloverBeforeSuccessorPersist = nil })

	archiveDone := make(chan error, 1)
	go func() {
		_, err := app.archiveMeeting("AJ")
		archiveDone <- err
	}()
	select {
	case <-persistEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("manual archive did not reach blocked successor persistence")
	}
	if current := app.memory.currentMeetingID(officeRoomID); current != oldSittingID {
		t.Fatalf("recordless successor published while its file write was blocked: current=%q old=%q", current, oldSittingID)
	}

	leaveDone := make(chan bool, 1)
	go func() {
		removed, _ := app.forgetParticipantSessionResultInRoom(officeRoomID, "AJ", "archive-fence-session")
		leaveDone <- removed
	}()
	chatDone := make(chan bool, 1)
	go func() {
		_, ok := app.recordRoomChatMessageForScope(oldScope, "AJ", "This stale frame must not cross the archive boundary.", map[string]string{
			roomChatServerMessageIDMetadataKey: "archive-fence-stale-chat",
			"authorEmail":                      "aj@shareability.com",
		})
		chatDone <- ok
	}()
	mediaDone := make(chan uint64, 1)
	go func() { mediaDone <- app.roomMediaGeneration(officeRoomID) }()

	for label, done := range map[string]<-chan bool{"leave": leaveDone, "chat": chatDone} {
		select {
		case result := <-done:
			t.Fatalf("%s crossed blocked successor persistence early (result=%v)", label, result)
		case <-time.After(40 * time.Millisecond):
		}
	}
	select {
	case generation := <-mediaDone:
		t.Fatalf("media generation became observable during blocked successor persistence: %d", generation)
	case <-time.After(40 * time.Millisecond):
	}

	close(releasePersist)
	if err := <-archiveDone; err != nil {
		t.Fatal(err)
	}
	if removed := <-leaveDone; !removed {
		t.Fatal("concurrent participant leave did not complete after the archive boundary")
	}
	if accepted := <-chatDone; accepted {
		t.Fatal("old-generation chat was accepted after the archive boundary")
	}
	newGeneration := <-mediaDone
	successorID := app.memory.currentMeetingID(officeRoomID)
	if successorID == "" || successorID == oldSittingID || newGeneration <= oldGeneration {
		t.Fatalf("successor boundary id=%q generation=%d, want new id and generation above %d", successorID, newGeneration, oldGeneration)
	}
	successor, found := app.meetings.recordByID(successorID)
	if !found || successor.EndedAt != "" || len(successor.Participants) != 1 || successor.Participants[0] != "AJ" {
		t.Fatalf("durable successor did not bind the exact boundary occupant: %+v found=%v", successor, found)
	}
	app.mu.Lock()
	mediaSittingID := app.roomLiveLocked(officeRoomID).mediaSittingID
	app.mu.Unlock()
	if mediaSittingID != successorID {
		t.Fatalf("media sitting=%q, want durable successor %q", mediaSittingID, successorID)
	}
	if _, found := app.memory.entryByID("archive-fence-stale-chat"); found {
		t.Fatal("stale old-generation chat persisted across the archive boundary")
	}
}

func TestManualArchiveRolloverPersistenceFailureLeavesPredecessorDurableAcrossRestart(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	oldSittingID := app.prepareMeetingSittingID(officeRoomID)
	if _, _, err := app.admitParticipantWithAnchor(context.Background(), officeRoomID, "AJ", "archive-fail-session", "archive-fail-endpoint", oldSittingID, memberAdmissionPrincipal("aj@shareability.com")); err != nil {
		t.Fatal(err)
	}
	oldGeneration := app.ensureRoomMedia(officeRoomID)
	appendTestTranscript(t, app, "archive-fail-source", "The predecessor must survive a failed rollover transaction.")

	originalMeetingPath := app.meetings.path
	blockedParent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedParent, []byte("block meeting-store replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	manualRolloverBeforeSuccessorPersist = func() {
		app.meetings.path = filepath.Join(blockedParent, "meetings.json")
	}
	t.Cleanup(func() {
		manualRolloverBeforeSuccessorPersist = nil
		app.meetings.path = originalMeetingPath
	})

	if _, err := app.archiveMeeting("AJ"); !errors.Is(err, ErrMeetingRecordStore) {
		t.Fatalf("archive error=%v, want ErrMeetingRecordStore", err)
	}
	app.meetings.path = originalMeetingPath
	manualRolloverBeforeSuccessorPersist = nil

	active, found := app.meetings.activeRecord(officeRoomID)
	if !found || active.ID != oldSittingID || active.EndedAt != "" || active.Finalization != nil {
		t.Fatalf("failed rollover changed in-process predecessor: %+v found=%v", active, found)
	}
	if got := app.memory.currentMeetingID(officeRoomID); got != oldSittingID {
		t.Fatalf("failed rollover published memory successor %q, want %q", got, oldSittingID)
	}
	app.mu.Lock()
	mediaSittingID := app.roomLiveLocked(officeRoomID).mediaSittingID
	mediaGeneration := app.roomLiveLocked(officeRoomID).mediaGen
	app.mu.Unlock()
	if mediaSittingID != oldSittingID || mediaGeneration != oldGeneration {
		t.Fatalf("failed rollover crossed media seam: sitting=%q generation=%d want %q/%d", mediaSittingID, mediaGeneration, oldSittingID, oldGeneration)
	}

	reloaded, err := loadMeetingStore(originalMeetingPath)
	if err != nil {
		t.Fatalf("reload predecessor: %v", err)
	}
	persisted, found := reloaded.activeRecord(officeRoomID)
	if !found || persisted.ID != oldSittingID || persisted.EndedAt != "" {
		t.Fatalf("restart did not preserve open predecessor: %+v found=%v", persisted, found)
	}
	if pending, err := app.admissionAnchors.PendingRollovers(context.Background()); err != nil {
		t.Fatal(err)
	} else if len(pending) != 0 {
		t.Fatalf("failed rollover left boot-promotable staged anchors: %+v", pending)
	}
	starts, err := app.admissionAnchors.SittingStarts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, start := range starts {
		if start.SittingID != oldSittingID {
			t.Fatalf("failed rollover exposed successor anchor %q to boot recovery", start.SittingID)
		}
	}
}

func TestBootCompletesCommittedManualRolloverBeforeOrdinaryAnchorRecovery(t *testing.T) {
	t.Setenv("MEETING_IDLE_END_GRACE", "1h")
	app := newIsolatedKanbanBoardApp(t)
	oldSittingID := app.prepareMeetingSittingID(officeRoomID)
	if _, _, err := app.admitParticipantWithAnchor(context.Background(), officeRoomID, "AJ", "rollover-crash-session", "rollover-crash-endpoint", oldSittingID, memberAdmissionPrincipal("aj@shareability.com")); err != nil {
		t.Fatal(err)
	}

	// Simulate a process dying after the single meetings.json close/open commit
	// but before RAM publishes the successor identity or clears anchor staging.
	app.meetingLifecycleMu.Lock()
	app.mu.Lock()
	successorID := app.memory.mintSuccessorMeetingID(officeRoomID, oldSittingID)
	staged, err := app.stageManualRolloverSittingLocked(context.Background(), officeRoomID, oldSittingID, successorID)
	if err != nil {
		app.mu.Unlock()
		app.meetingLifecycleMu.Unlock()
		t.Fatal(err)
	}
	if _, successor, err := app.meetings.rolloverMeetingWithFinalization(officeRoomID, oldSittingID, time.Now().UTC(), "", meetingFinalizationSourceHighWater{}, successorID, staged.startedAt, staged.participants); err != nil || successor.ID != successorID {
		app.mu.Unlock()
		app.meetingLifecycleMu.Unlock()
		t.Fatalf("commit rollover successor=%+v err=%v", successor, err)
	}
	app.mu.Unlock()
	app.meetingLifecycleMu.Unlock()
	if got := app.memory.currentMeetingID(officeRoomID); got != oldSittingID {
		t.Fatalf("test did not stop at pre-publication crash seam: current=%q", got)
	}

	restarted := newKanbanBoardApp()
	if got := restarted.memory.currentMeetingID(officeRoomID); got != successorID {
		t.Fatalf("boot did not publish committed successor: got=%q want=%q", got, successorID)
	}
	active, found := restarted.meetings.activeRecord(officeRoomID)
	if !found || active.ID != successorID || active.EndedAt != "" {
		t.Fatalf("boot active sitting=%+v found=%v, want committed successor", active, found)
	}
	old, found := restarted.meetings.recordByID(oldSittingID)
	if !found || old.EndedAt == "" || old.EndedReason != meetingEndedReasonArchive {
		t.Fatalf("boot predecessor=%+v found=%v, want archive-closed", old, found)
	}
	pending, err := restarted.admissionAnchors.PendingRollovers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("boot did not commit staged successor anchors: %+v", pending)
	}
	starts, err := restarted.admissionAnchors.SittingStarts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	foundSuccessorAnchor := false
	for _, start := range starts {
		if start.SittingID == successorID {
			foundSuccessorAnchor = true
		}
	}
	if !foundSuccessorAnchor {
		t.Fatalf("boot successor %s lacks committed admission authority: %+v", successorID, starts)
	}
}

func TestBootRecoveryQueuesManyPendingWithoutWaitingForSlowProvider(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	app.noteMeetingAdmission(officeRoomID, "AJ")
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	appendTestTranscript(t, app, "boot-slow-source", "The oldest pending meeting needs model synthesis.")
	firstRecord, _ := app.meetings.activeRecord(officeRoomID)
	firstSource := app.meetingFinalizationSource(firstRecord.ID)
	base := time.Now().UTC()
	if _, changed, err := app.meetings.endMeetingWithFinalization(firstRecord.ID, base, meetingEndedReasonRestart, "", firstSource); err != nil || !changed {
		t.Fatalf("close first pending changed=%v err=%v", changed, err)
	}

	meetingIDs := []string{firstRecord.ID}
	const additional = 31
	for index := 0; index < additional; index++ {
		meetingID := "boot-pending-" + strconv.Itoa(index)
		startedAt := base.Add(time.Duration(index+1) * time.Millisecond)
		if _, _, err := app.meetings.startMeetingDurable(officeRoomID, meetingID, startedAt, nil); err != nil {
			t.Fatal(err)
		}
		source := app.meetingFinalizationSource(meetingID)
		if _, changed, err := app.meetings.endMeetingWithFinalization(meetingID, startedAt.Add(time.Millisecond), meetingEndedReasonRestart, "", source); err != nil || !changed {
			t.Fatalf("close %s changed=%v err=%v", meetingID, changed, err)
		}
		meetingIDs = append(meetingIDs, meetingID)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	originalResponder := createOpenAITextResponse
	t.Cleanup(func() {
		defer func() { createOpenAITextResponse = originalResponder }()
		releaseOnce.Do(func() { close(release) })
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			app.meetingFinalizationRunMu.Lock()
			idle := len(app.meetingFinalizationRunning) == 0 && app.meetingFinalizationWorkers == 0
			app.meetingFinalizationRunMu.Unlock()
			if idle {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
	var first sync.Once
	createOpenAITextResponse = func(ctx context.Context, _ string, request openAITextRequest) (string, error) {
		if !strings.Contains(request.Instructions, "meeting digest compiler") {
			first.Do(func() { close(started) })
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			return "## Overview\nRecovered the oldest pending meeting.\n## Transcript reference\nboot-slow-source", nil
		}
		return cannedArchiveMeetingDigestJSON("boot-slow-source"), nil
	}

	bootStarted := time.Now()
	app.resumeMeetingFinalizationsAtBoot("test-key")
	if elapsed := time.Since(bootStarted); elapsed > 500*time.Millisecond {
		t.Fatalf("boot recovery blocked %v while queueing %d meetings", elapsed, len(meetingIDs))
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("background finalization worker did not reach slow provider")
	}
	app.meetingFinalizationRunMu.Lock()
	queued := len(app.meetingFinalizationRunning)
	worker := app.meetingFinalizationWorker
	app.meetingFinalizationRunMu.Unlock()
	if !worker || queued != len(meetingIDs) {
		t.Fatalf("boot queue worker=%v tracked=%d, want %d pending receipts", worker, queued, len(meetingIDs))
	}

	releaseOnce.Do(func() { close(release) })
	for _, meetingID := range meetingIDs {
		waitForMeetingFinalizationState(t, app, meetingID, meetingFinalizationFinalized)
	}
	waitForMeetingFinalizationQueueIdle(t, app)
}

func TestFreshFinalizationAndPromotedBootJobBypassSlowHistoricalHead(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	app.noteMeetingAdmission(officeRoomID, "AJ")
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	appendTestTranscript(t, app, "historical-slow-source", "Historical backlog provider call must remain blocked.")
	historical, _ := app.meetings.activeRecord(officeRoomID)
	base := time.Now().UTC()
	if _, changed, err := app.meetings.endMeetingWithFinalization(historical.ID, base, meetingEndedReasonRestart, "", app.meetingFinalizationSource(historical.ID)); err != nil || !changed {
		t.Fatalf("close historical changed=%v err=%v", changed, err)
	}

	const promotedID = "historical-promoted-tail"
	if _, _, err := app.meetings.startMeetingDurable(officeRoomID, promotedID, base.Add(time.Millisecond), nil); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := app.meetings.endMeetingWithFinalization(promotedID, base.Add(2*time.Millisecond), meetingEndedReasonRestart, "", app.meetingFinalizationSource(promotedID)); err != nil || !changed {
		t.Fatalf("close promoted tail changed=%v err=%v", changed, err)
	}
	// Establish the successor's admission/consent authority before the old
	// provider acquires its fence. Mutating that authority while the historical
	// call is deliberately blocked would correctly stale the old fence and turn
	// this scheduler test into a consent-race test instead.
	app.memory.rotateMeetingIDIfCurrent(officeRoomID, historical.ID)
	freshID := app.memory.ensureMeetingID(officeRoomID)
	if freshID == "" || freshID == historical.ID {
		t.Fatalf("fresh meeting id=%q", freshID)
	}
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")

	historicalStarted := make(chan struct{})
	releaseHistorical := make(chan struct{})
	var releaseOnce sync.Once
	originalResponder := createOpenAITextResponse
	t.Cleanup(func() {
		defer func() { createOpenAITextResponse = originalResponder }()
		releaseOnce.Do(func() { close(releaseHistorical) })
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			app.meetingFinalizationRunMu.Lock()
			idle := len(app.meetingFinalizationRunning) == 0 && app.meetingFinalizationWorkers == 0
			app.meetingFinalizationRunMu.Unlock()
			if idle {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
	createOpenAITextResponse = func(ctx context.Context, _ string, request openAITextRequest) (string, error) {
		if strings.Contains(request.Instructions, "meeting digest compiler") {
			if strings.Contains(strings.ToLower(request.Input), "fresh priority") {
				return cannedArchiveMeetingDigestJSON("fresh-priority-source"), nil
			}
			return cannedArchiveMeetingDigestJSON("historical-slow-source"), nil
		}
		if strings.Contains(request.Input, "Historical backlog provider") {
			select {
			case <-historicalStarted:
			default:
				close(historicalStarted)
			}
			select {
			case <-releaseHistorical:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			return "## Overview\nHistorical backlog recovered.\n## Transcript reference\nhistorical-slow-source", nil
		}
		return "## Overview\nFresh priority close completed.\n## Transcript reference\nfresh-priority-source", nil
	}

	app.resumeMeetingFinalizationsAtBoot("test-key")
	select {
	case <-historicalStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("historical backlog did not occupy its serial lane")
	}

	// This job was already in the boot tail. A live signal must promote it out
	// of that queue instead of merely setting a rerun behind the same tail.
	app.scheduleMeetingCoreFinalization(promotedID)

	if _, _, err := app.meetings.startMeetingDurable(officeRoomID, freshID, time.Now().UTC(), nil); err != nil {
		t.Fatal(err)
	}
	appendTestTranscript(t, app, "fresh-priority-source", "The fresh priority sitting selected a launch date.")
	if _, changed, err := app.meetings.endMeetingWithFinalization(freshID, time.Now().UTC(), meetingEndedReasonIdle, "", app.meetingFinalizationSource(freshID)); err != nil || !changed {
		t.Fatalf("close fresh changed=%v err=%v", changed, err)
	}
	app.scheduleMeetingCoreFinalization(freshID)

	waitForMeetingFinalizationState(t, app, promotedID, meetingFinalizationFinalized)
	fresh := waitForMeetingFinalizationState(t, app, freshID, meetingFinalizationFinalized)
	if fresh.Finalization.Source.TranscriptID != "fresh-priority-source" {
		t.Fatalf("fresh receipt source=%+v, want fresh-priority-source", fresh.Finalization.Source)
	}
	stillBlocked, _ := app.meetings.recordByID(historical.ID)
	if stillBlocked.Finalization == nil || stillBlocked.Finalization.State == meetingFinalizationFinalized {
		t.Fatalf("historical head unexpectedly settled before release: %+v", stillBlocked.Finalization)
	}

	releaseOnce.Do(func() { close(releaseHistorical) })
	waitForMeetingFinalizationState(t, app, historical.ID, meetingFinalizationFinalized)
	waitForMeetingFinalizationQueueIdle(t, app)
}

func TestFinalizedTranscriptCorrectionReopensBeforeMutationReturns(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	app.noteMeetingAdmission(officeRoomID, "AJ")
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	appendTestTranscript(t, app, "correction-tx", "The launch is Tuesday.")
	record, _ := app.meetings.activeRecord(officeRoomID)
	source := app.meetingFinalizationSource(record.ID)
	if _, changed, err := app.meetings.endMeetingWithFinalization(record.ID, time.Now().UTC(), meetingEndedReasonIdle, "", source); err != nil || !changed {
		t.Fatalf("close changed=%v err=%v", changed, err)
	}
	var calls atomic.Int32
	if _, err := app.finalizeMeetingCore(context.Background(), record.ID, meetingFinalizationResponder(&calls, "correction-tx")); err != nil {
		t.Fatal(err)
	}
	// Keep the asynchronous retry from immediately resealing while this test
	// observes the synchronous append-side truth transition.
	app.mu.Lock()
	app.apiKey = ""
	app.mu.Unlock()
	if _, changed, err := app.memory.updateEntryWithMetadata(meetingMemoryKindTranscript, "correction-tx", "The launch is Wednesday.", map[string]string{"correctionState": "corrected"}); err != nil || !changed {
		t.Fatalf("correction changed=%v err=%v", changed, err)
	}
	reopened, _ := app.meetings.recordByID(record.ID)
	if reopened.Finalization == nil || reopened.Finalization.State == meetingFinalizationFinalized || reopened.Finalization.FinalizedAt != "" {
		t.Fatalf("correction returned while stale finalization remained sealed: %+v", reopened.Finalization)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		app.meetingFinalizationRunMu.Lock()
		_, running := app.meetingFinalizationRunning[record.ID]
		app.meetingFinalizationRunMu.Unlock()
		if !running {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("late-source finalization worker did not quiesce")
}

func TestLateTranscriptDowngradesFinalizedArchiveBeforeCommitReturns(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	originalResponder := createOpenAITextResponse
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })
	var calls atomic.Int32
	createOpenAITextResponse = meetingFinalizationResponder(&calls, "archive-fence-tx")
	app.noteMeetingAdmission(officeRoomID, "AJ")
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	appendTestTranscript(t, app, "archive-fence-tx", "The launch is Tuesday.")
	meetingID := app.memory.currentMeetingID(officeRoomID)
	fireIdleEndNow(app)
	waitForMeetingFinalizationState(t, app, meetingID, meetingFinalizationFinalized)
	settled := waitForMeetingArchiveFinalizationSync(t, app, meetingID)
	if settled.ArchiveID == "" {
		t.Fatal("finalized meeting has no archive")
	}

	// Hold the repair in degraded state after the synchronous append-side
	// downgrade; regardless of worker timing, the archive may say closing or
	// degraded but can never keep saying finalized for the stale source.
	app.mu.Lock()
	app.apiKey = ""
	app.mu.Unlock()
	if _, changed, err := app.memory.updateEntryWithMetadata(meetingMemoryKindTranscript, "archive-fence-tx", "The launch is Wednesday.", map[string]string{"correctionState": "corrected"}); err != nil || !changed {
		t.Fatalf("late correction changed=%v err=%v", changed, err)
	}
	archivePath, err := meetingArchivePath(settled.ArchiveID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	var archive meetingArchive
	if err := json.Unmarshal(raw, &archive); err != nil {
		t.Fatal(err)
	}
	if archive.Meeting == nil || archive.Meeting.Finalization == nil || archive.Meeting.Finalization.State == meetingFinalizationFinalized || archive.Meeting.Finalization.FinalizedAt != "" {
		t.Fatalf("late accepted source returned while archive still claimed finalized: %+v", archive.Meeting)
	}
	waitForMeetingFinalizationQueueIdle(t, app)
}

func TestFinalizedTranscriptDeleteDowngradesArchiveAndResealsReducedSource(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	originalResponder := createOpenAITextResponse
	var initialCalls atomic.Int32
	createOpenAITextResponse = meetingFinalizationResponder(&initialCalls, "delete-keep-chat")
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	app.noteMeetingAdmission(officeRoomID, "Tom")
	for _, input := range []struct{ id, text string }{
		{id: "delete-keep-chat", text: "Keep the Tuesday launch decision."},
		{id: "delete-high-chat", text: "Correction: launch on Wednesday."},
	} {
		if _, ok := app.recordRoomChatMessageWithMetadata(officeRoomID, "Tom", input.text, map[string]string{
			roomChatServerMessageIDMetadataKey: input.id,
			"authorEmail":                      "tom@shareability.com",
		}); !ok {
			t.Fatalf("seed room chat %s", input.id)
		}
	}
	meetingID := app.memory.currentMeetingID(officeRoomID)
	fireIdleEndNow(app)
	waitForMeetingFinalizationState(t, app, meetingID, meetingFinalizationFinalized)
	settled := waitForMeetingArchiveFinalizationSync(t, app, meetingID)
	before := *settled.Finalization
	if before.Source.TranscriptCount != 2 || before.Source.TranscriptID != "delete-high-chat" {
		t.Fatalf("initial finalized source=%+v, want two rows ending at delete-high-chat", before.Source)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	createOpenAITextResponse = func(ctx context.Context, _ string, request openAITextRequest) (string, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		if strings.Contains(request.Instructions, "meeting digest compiler") {
			return cannedArchiveMeetingDigestJSON("delete-keep-chat"), nil
		}
		return "## Overview\nThe Tuesday launch decision remains.\n## Transcript reference\ndelete-keep-chat", nil
	}
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })

	if _, ok := app.deleteRoomChatMessage("delete-high-chat", "tom@shareability.com", "Tom"); !ok {
		t.Fatal("author deletion of finalized transcript failed")
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("reduced-source finalization did not start")
	}
	if _, found := app.memory.entryByID("delete-high-chat"); found {
		t.Fatal("deleted transcript remains in durable memory")
	}
	reopened, _ := app.meetings.recordByID(meetingID)
	if reopened.Finalization == nil || reopened.Finalization.State == meetingFinalizationFinalized || reopened.Finalization.FinalizedAt != "" {
		t.Fatalf("delete returned under stale finalized truth: %+v", reopened.Finalization)
	}
	archivePath, err := meetingArchivePath(settled.ArchiveID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	var archive meetingArchive
	if err := json.Unmarshal(raw, &archive); err != nil {
		t.Fatal(err)
	}
	if archive.Meeting == nil || archive.Meeting.Finalization == nil || archive.Meeting.Finalization.State == meetingFinalizationFinalized {
		t.Fatalf("archive retained stale finalized truth after transcript delete: %+v", archive.Meeting)
	}

	releaseOnce.Do(func() { close(release) })
	finalized := waitForMeetingFinalizationState(t, app, meetingID, meetingFinalizationFinalized)
	finalized = waitForMeetingArchiveFinalizationSync(t, app, meetingID)
	if finalized.Finalization.Source.TranscriptCount != 1 || finalized.Finalization.Source.TranscriptID != "delete-keep-chat" || finalized.Finalization.Source.ManifestDigest == before.Source.ManifestDigest {
		t.Fatalf("reduced source did not reseal exactly: before=%+v after=%+v", before.Source, finalized.Finalization.Source)
	}
	if finalized.Finalization.ObservedCaptureSequence <= finalized.Finalization.Source.CaptureSequence {
		t.Fatalf("test did not exercise deleted high-water: observed=%d source=%d", finalized.Finalization.ObservedCaptureSequence, finalized.Finalization.Source.CaptureSequence)
	}
}

func TestManualArchiveEmailCompletionCannotRestoreConcurrentlyDeletedTranscript(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.noteMeetingAdmission(officeRoomID, "Tom")
	if _, ok := app.recordRoomChatMessageWithMetadata(officeRoomID, "Tom", "Remove this private correction from the final archive.", map[string]string{
		roomChatServerMessageIDMetadataKey: "manual-email-delete-race",
		"authorEmail":                      "tom@shareability.com",
	}); !ok {
		t.Fatal("seed room chat transcript")
	}

	emailEntered := make(chan struct{})
	releaseEmail := make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	meetingNotesEmailBeforeDelivery = func() {
		enteredOnce.Do(func() { close(emailEntered) })
		<-releaseEmail
	}
	t.Cleanup(func() {
		meetingNotesEmailBeforeDelivery = nil
		releaseOnce.Do(func() { close(releaseEmail) })
	})

	type archiveOutcome struct {
		result meetingArchiveResult
		err    error
	}
	done := make(chan archiveOutcome, 1)
	go func() {
		result, err := app.archiveMeeting("Tom")
		done <- archiveOutcome{result: result, err: err}
	}()
	select {
	case <-emailEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("manual archive did not reach blocked email completion")
	}

	if _, ok := app.deleteRoomChatMessage("manual-email-delete-race", "tom@shareability.com", "Tom"); !ok {
		t.Fatal("author deletion failed while manual archive email was blocked")
	}
	if _, found := app.memory.entryByID("manual-email-delete-race"); found {
		t.Fatal("author deletion returned before durable memory removal")
	}
	releaseOnce.Do(func() { close(releaseEmail) })
	outcome := <-done
	if outcome.err != nil {
		t.Fatalf("archive completion: %v", outcome.err)
	}
	path, err := meetingArchivePath(outcome.result.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var archive meetingArchive
	if err := json.Unmarshal(raw, &archive); err != nil {
		t.Fatal(err)
	}
	for _, entry := range archive.Memory {
		if entry.ID == "manual-email-delete-race" {
			t.Fatal("slow email completion restored a transcript already deleted by its author")
		}
	}
	latest, found := app.meetings.recordByID(archive.MeetingID)
	if !found || archive.Meeting == nil || archive.Meeting.Finalization == nil || latest.Finalization == nil ||
		archive.Meeting.Finalization.State != latest.Finalization.State ||
		!archive.Meeting.Finalization.Source.equal(latest.Finalization.Source) ||
		archive.Meeting.Finalization.ObservedRevision != latest.Finalization.ObservedRevision ||
		archive.Meeting.Finalization.Source.TranscriptCount != 0 || archive.Meeting.Finalization.Source.TranscriptID != "" {
		t.Fatalf("archive did not preserve the latest deletion-safe finalization truth: archive=%+v latest=%+v", archive.Meeting, latest)
	}
}

func TestIdleArchivePublishesAndStampsBeforeTranscriptDeletionCanCommit(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.noteMeetingAdmission(officeRoomID, "Tom")
	if _, ok := app.recordRoomChatMessageWithMetadata(officeRoomID, "Tom", "Delete only after the idle archive owns its source.", map[string]string{
		roomChatServerMessageIDMetadataKey: "idle-archive-delete-race",
		"authorEmail":                      "tom@shareability.com",
	}); !ok {
		t.Fatal("seed room chat transcript")
	}
	meetingID := app.memory.currentMeetingID(officeRoomID)

	archiveEntered := make(chan struct{})
	releaseArchive := make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	idleArchiveBeforeWrite = func() {
		enteredOnce.Do(func() { close(archiveEntered) })
		<-releaseArchive
	}
	t.Cleanup(func() {
		idleArchiveBeforeWrite = nil
		releaseOnce.Do(func() { close(releaseArchive) })
	})
	idleDone := make(chan struct{})
	go func() {
		fireIdleEndNow(app)
		close(idleDone)
	}()
	select {
	case <-archiveEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("idle close did not reach blocked archive publication")
	}

	deleteDone := make(chan bool, 1)
	go func() {
		_, ok := app.deleteRoomChatMessage("idle-archive-delete-race", "tom@shareability.com", "Tom")
		deleteDone <- ok
	}()
	select {
	case ok := <-deleteDone:
		t.Fatalf("transcript deletion crossed unfinished idle archive (ok=%v)", ok)
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(releaseArchive) })
	select {
	case <-idleDone:
	case <-time.After(5 * time.Second):
		t.Fatal("idle archive did not complete")
	}
	if ok := <-deleteDone; !ok {
		t.Fatal("author deletion failed after idle archive publication")
	}

	closed, found := app.meetings.recordByID(meetingID)
	if !found || closed.ArchiveID == "" || closed.EndedReason != meetingEndedReasonIdle {
		t.Fatalf("idle close lacks durable archive stamp: %+v found=%v", closed, found)
	}
	path, err := meetingArchivePath(closed.ArchiveID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var archive meetingArchive
	if err := json.Unmarshal(raw, &archive); err != nil {
		t.Fatal(err)
	}
	for _, entry := range archive.Memory {
		if entry.ID == "idle-archive-delete-race" {
			t.Fatal("idle archive retained source after author deletion returned")
		}
	}
}

func TestRoomChatDeleteKeepsAuthAndFinalizationOnExactHistoricalSitting(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	originalResponder := createOpenAITextResponse
	var calls atomic.Int32
	createOpenAITextResponse = meetingFinalizationResponder(&calls, "historical-owned-chat")
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	app.noteMeetingAdmission(officeRoomID, "Tom")
	if _, ok := app.recordRoomChatMessageWithMetadata(officeRoomID, "Tom", "Remove this from the prior sitting.", map[string]string{
		roomChatServerMessageIDMetadataKey: "historical-owned-chat",
		"authorEmail":                      "tom@shareability.com",
	}); !ok {
		t.Fatal("seed historical room chat")
	}
	oldID := app.memory.currentMeetingID(officeRoomID)
	fireIdleEndNow(app)
	waitForMeetingFinalizationState(t, app, oldID, meetingFinalizationFinalized)
	waitForMeetingArchiveFinalizationSync(t, app, oldID)

	app.noteMeetingAdmission(officeRoomID, "AJ")
	successor, found := app.meetings.activeRecord(officeRoomID)
	if !found || successor.ID == oldID {
		t.Fatalf("missing active successor: %+v found=%v", successor, found)
	}
	successorSource := app.meetingFinalizationSource(successor.ID)
	if _, ok := app.deleteRoomChatMessage("historical-owned-chat", "tim@shareability.com", "Tom"); ok {
		t.Fatal("cross-user deletion of historical chat succeeded")
	}
	if _, found := app.memory.entryByID("historical-owned-chat"); !found {
		t.Fatal("authorization failure removed historical chat")
	}
	if _, ok := app.deleteRoomChatMessage("historical-owned-chat", "tom@shareability.com", "Tom"); !ok {
		t.Fatal("author could not delete historical chat")
	}
	oldFinal := waitForMeetingFinalizationState(t, app, oldID, meetingFinalizationFinalized)
	if oldFinal.Finalization.Source.TranscriptCount != 0 {
		t.Fatalf("historical finalization retained deleted source: %+v", oldFinal.Finalization.Source)
	}
	current, found := app.meetings.activeRecord(officeRoomID)
	if !found || current.ID != successor.ID || !app.meetingFinalizationSource(successor.ID).equal(successorSource) {
		t.Fatalf("historical delete crossed into active successor: current=%+v source=%+v", current, app.meetingFinalizationSource(successor.ID))
	}
	for _, tombstone := range app.memory.entriesOfKind(meetingMemoryKindChatDelete, 0) {
		if tombstone.Metadata["deletedId"] == "historical-owned-chat" && tombstone.Metadata["meetingId"] != oldID {
			t.Fatalf("delete tombstone crossed sitting boundary: %+v", tombstone)
		}
	}
}

func TestFinalizedTranscriptDeleteFailsClosedWhenArchiveCannotDowngrade(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	originalResponder := createOpenAITextResponse
	var calls atomic.Int32
	createOpenAITextResponse = meetingFinalizationResponder(&calls, "archive-delete-failure-chat")
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	app.noteMeetingAdmission(officeRoomID, "Tom")
	if _, ok := app.recordRoomChatMessageWithMetadata(officeRoomID, "Tom", "This source must survive a broken archive downgrade.", map[string]string{
		roomChatServerMessageIDMetadataKey: "archive-delete-failure-chat",
		"authorEmail":                      "tom@shareability.com",
	}); !ok {
		t.Fatal("seed archive failure chat")
	}
	meetingID := app.memory.currentMeetingID(officeRoomID)
	fireIdleEndNow(app)
	waitForMeetingFinalizationState(t, app, meetingID, meetingFinalizationFinalized)
	settled := waitForMeetingArchiveFinalizationSync(t, app, meetingID)
	archivePath, err := meetingArchivePath(settled.ArchiveID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(archivePath); err != nil {
		t.Fatal(err)
	}
	removed, deleted, err := app.memory.deleteEntryByID("archive-delete-failure-chat")
	if err == nil || deleted || removed.ID != "" {
		t.Fatalf("archive downgrade failure did not fail deletion closed: removed=%+v deleted=%v err=%v", removed, deleted, err)
	}
	if !strings.Contains(err.Error(), "downgrade") {
		t.Fatalf("delete error=%v, want archive downgrade context", err)
	}
	if _, found := app.memory.entryByID("archive-delete-failure-chat"); !found {
		t.Fatal("transcript disappeared despite archive downgrade failure")
	}
	if source := app.meetingFinalizationSource(meetingID); source.TranscriptCount != 1 || source.TranscriptID != "archive-delete-failure-chat" {
		t.Fatalf("source changed despite failed deletion: %+v", source)
	}
}

func TestMeetingFinalizationDegradedIsNotAnalysisReady(t *testing.T) {
	store, err := loadMeetingStore(filepath.Join(t.TempDir(), "meetings.json"))
	if err != nil {
		t.Fatal(err)
	}
	record, _, err := store.startMeetingDurable(officeRoomID, "degraded-meeting", time.Now().UTC().Add(-time.Minute), nil)
	if err != nil {
		t.Fatal(err)
	}
	source := meetingFinalizationSourceHighWater{TranscriptID: "tx", CaptureSequence: 1, TranscriptCount: 1, ManifestDigest: "digest"}
	if _, _, err := store.endMeetingWithFinalization(record.ID, time.Now().UTC(), meetingEndedReasonIdle, "", source); err != nil {
		t.Fatal(err)
	}
	if _, err := store.markFinalizationStage(record.ID, meetingFinalizationStageBrain, meetingFinalizationStageDegraded, "", "provider_unavailable", "", 0, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	degraded, _ := store.recordByID(record.ID)
	payload := meetingRecordPayload(degraded, time.Now().UTC())
	if payload["finalizationState"] != meetingFinalizationDegraded || payload["analysisReady"] != false {
		t.Fatalf("degraded payload=%+v", payload)
	}
	if _, err := store.markFinalizationComplete(record.ID, source, time.Now().UTC()); err == nil || errors.Is(err, ErrMeetingFinalizationSourceAdvanced) {
		t.Fatalf("incomplete degraded stages unexpectedly finalized: %v", err)
	}
}
