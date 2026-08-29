package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func finalizedMeetingForSourceEpisodeTest(t *testing.T) (*kanbanBoardApp, meetingRecord, *FileSourceEpisodeLedger) {
	t.Helper()
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	app.noteMeetingAdmission(officeRoomID, "AJ")
	authority := newAmbientConsentAuthorityForTest(t)
	grantAmbientConsentForTest(t, app, authority, officeRoomID, "tom@shareability.com")
	appendTestTranscript(t, app, "source-episode-transcript", "The team approved the exact post-close publication plan.")
	record, found := app.meetings.activeRecord(officeRoomID)
	if !found {
		t.Fatal("missing active meeting")
	}
	binding, err := app.consentBindingForPrincipal(context.Background(), memberAdmissionPrincipal("tom@shareability.com"), officeRoomID, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	contributors, err := encodeConsentContributorBindings([]ConsentAdmissionBinding{binding})
	if err != nil {
		t.Fatal(err)
	}
	if _, changed, err := app.memory.updateEntryWithMetadata(meetingMemoryKindTranscript, "source-episode-transcript",
		"Tom: The team approved the exact post-close publication plan.", map[string]string{consentContributorBindingsMetadataKey: contributors}); err != nil || !changed {
		t.Fatalf("stamp contributor authority changed=%v err=%v", changed, err)
	}
	source := app.meetingFinalizationSource(record.ID)
	if _, changed, err := app.meetings.endMeetingWithFinalization(record.ID, time.Now().UTC(), meetingEndedReasonIdle, "", source); err != nil || !changed {
		t.Fatalf("end meeting changed=%v err=%v", changed, err)
	}
	// Finalize the durable analysis while publication is deliberately absent;
	// each test then controls the exact ledger write/retry boundary.
	app.sourceEpisodes = nil
	var calls atomic.Int32
	finalized, err := app.finalizeMeetingCore(context.Background(), record.ID, meetingFinalizationResponder(&calls, "source-episode-transcript"))
	if err != nil || !app.meetingFinalizationOutputsReady(finalized) {
		t.Fatalf("finalization err=%v receipt=%+v", err, finalized.Finalization)
	}
	ledger, err := OpenFileSourceEpisodeLedger(postCloseMeetingSourceEpisodeLedgerPath())
	if err != nil {
		t.Fatal(err)
	}
	app.sourceEpisodes = ledger
	app.sourceEpisodesErr = nil
	return app, finalized, ledger
}

func waitForSourceEpisodeAbsent(t *testing.T, ledger *FileSourceEpisodeLedger, meetingID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, found, err := ledger.CurrentSourceEpisode(context.Background(), canonicalTenantID(), meetingSourceEpisodeID(meetingID))
		if err == nil && !found {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("source episode remained active")
}

func waitForSourceEpisodePresent(t *testing.T, ledger *FileSourceEpisodeLedger, meetingID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, found, err := ledger.CurrentSourceEpisode(context.Background(), canonicalTenantID(), meetingSourceEpisodeID(meetingID))
		if err == nil && found {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("source episode was not retried after successor close")
}

func sourceEpisodeTestEntry(t *testing.T, app *kanbanBoardApp, meetingID, kind, id string) meetingMemoryEntry {
	t.Helper()
	for _, entry := range meetingSourceEpisodeEntries(app.memory, meetingID) {
		if entry.Kind == kind && entry.ID == id {
			return entry
		}
	}
	t.Fatalf("missing %s entry %s", kind, id)
	return meetingMemoryEntry{}
}

func TestPostCloseMeetingSourceEpisodePersistsRetrievesAndReplaysAfterRestart(t *testing.T) {
	app, finalized, ledger := finalizedMeetingForSourceEpisodeTest(t)
	ref, err := app.publishFinalizedMeetingSourceEpisode(context.Background(), finalized)
	if err != nil {
		t.Fatal(err)
	}
	episode, found, err := ledger.CurrentSourceEpisode(context.Background(), canonicalTenantID(), ref.ID)
	if err != nil || !found {
		t.Fatalf("current found=%v err=%v", found, err)
	}
	if episode.Kind != SourceEpisodeMeetingAnalysis || episode.RawMeetingTranscriptAccess != MeetingSourceEpisodeRawExactSegments ||
		episode.RetrievalBody.SourceFamily != SourceEpisodeFamilyMeetingAnalysisBody || episode.Scope.SittingID != finalized.ID {
		t.Fatalf("episode=%+v", episode)
	}
	byBody, found, err := ledger.FindSourceEpisodeByRetrievalBody(context.Background(), canonicalTenantID(), sourceEpisodeBodyLocator(episode.RetrievalBody))
	if err != nil || !found || byBody.Header.ContentDigest != episode.Header.ContentDigest {
		t.Fatalf("body lookup found=%v err=%v episode=%+v", found, err, byBody)
	}
	principal := ACLPrincipal{Kind: ACLPrincipalUser, ID: "tom@shareability.com"}
	allowed, err := app.sourceEpisodeRegistry.AuthorizeSourceEpisodeMetadata(context.Background(), principal, episode)
	if err != nil || !allowed {
		t.Fatalf("meeting analysis metadata authorization allowed=%v err=%v", allowed, err)
	}
	var body SourceEpisodeNativeBody
	if err := app.sourceEpisodeRegistry.WithCurrentSourceEpisodeAuthority(context.Background(), episode, func() error {
		var readErr error
		body, readErr = app.sourceEpisodeRegistry.ReadExactSourceEpisodeBody(context.Background(), episode.RetrievalBody)
		return readErr
	}); err != nil || body.Body == "" || body.Revision != episode.RetrievalBody {
		t.Fatalf("authorized analysis body err=%v body=%+v", err, body)
	}
	second, err := app.publishFinalizedMeetingSourceEpisode(context.Background(), finalized)
	if err != nil || second != ref || len(ledger.records) != 1 {
		t.Fatalf("retry ref=%+v records=%d err=%v", second, len(ledger.records), err)
	}
	restarted, err := OpenFileSourceEpisodeLedger(filepath.Clean(postCloseMeetingSourceEpisodeLedgerPath()))
	if err != nil {
		t.Fatal(err)
	}
	replayed, found, err := restarted.CurrentSourceEpisode(context.Background(), canonicalTenantID(), ref.ID)
	if err != nil || !found || replayed.Header.ContentDigest != episode.Header.ContentDigest {
		t.Fatalf("restart found=%v err=%v episode=%+v", found, err, replayed)
	}
}

func TestPostClosePublicationStallDoesNotBlockSuccessorCapture(t *testing.T) {
	app, finalized, ledger := finalizedMeetingForSourceEpisodeTest(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var stallFirstWrite sync.Once
	ledger.write = func(path string, raw []byte) error {
		stallFirstWrite.Do(func() {
			close(entered)
			<-release
		})
		return appendFileDurably(path, raw, 0o600)
	}
	published := make(chan error, 1)
	go func() {
		_, err := app.publishFinalizedMeetingSourceEpisode(context.Background(), finalized)
		published <- err
	}()
	<-entered
	app.memory.rotateMeetingIDIfCurrent(officeRoomID, finalized.ID)
	app.noteMeetingAdmission(officeRoomID, "AJ")
	successor, found := app.meetings.activeRecord(officeRoomID)
	if !found || successor.ID == finalized.ID {
		t.Fatalf("successor=%+v found=%v", successor, found)
	}
	appended := make(chan error, 1)
	go func() {
		_, ok, err := app.memory.appendAttributedTranscriptEntry(officeRoomID, "successor-live-transcript", "", "AJ", "source_bound", "The successor live capture is not blocked.", nil, true, successor.ID)
		if err == nil && !ok {
			err = errors.New("successor transcript was not appended")
		}
		appended <- err
	}()
	select {
	case err := <-appended:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("successor live append waited on old-sitting publication")
	}
	close(release)
	if err := <-published; err != nil {
		t.Fatal(err)
	}
}

func TestPostClosePublicationPreemptionRetriesAfterSuccessorClose(t *testing.T) {
	app, finalized, ledger := finalizedMeetingForSourceEpisodeTest(t)
	app.memory.rotateMeetingIDIfCurrent(officeRoomID, finalized.ID)
	app.noteMeetingAdmission(officeRoomID, "AJ")
	successor, found := app.meetings.activeRecord(officeRoomID)
	if !found || successor.ID == finalized.ID {
		t.Fatalf("successor=%+v found=%v", successor, found)
	}
	if _, err := app.publishFinalizedMeetingSourceEpisode(context.Background(), finalized); !errors.Is(err, ErrMeetingSourceEpisodePreempted) {
		t.Fatalf("publication error = %v, want preempted", err)
	}

	source := app.meetingFinalizationSource(successor.ID)
	closed, changed, err := app.meetings.endMeetingWithFinalization(successor.ID, time.Now().UTC(), meetingEndedReasonArchive, "", source)
	if err != nil || !changed {
		t.Fatalf("close successor changed=%v err=%v record=%+v", changed, err, closed)
	}
	if _, err := app.finalizeMeetingCore(context.Background(), successor.ID, nil); err != nil {
		t.Fatal(err)
	}
	waitForSourceEpisodePresent(t, ledger, finalized.ID)
}

func TestPostClosePublicationLinearizesLateTranscriptAndRetractsWithoutGhost(t *testing.T) {
	app, finalized, ledger := finalizedMeetingForSourceEpisodeTest(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var stallFirstWrite sync.Once
	ledger.write = func(path string, raw []byte) error {
		stallFirstWrite.Do(func() {
			close(entered)
			<-release
		})
		return appendFileDurably(path, raw, 0o600)
	}
	published := make(chan error, 1)
	go func() {
		_, err := app.publishFinalizedMeetingSourceEpisode(context.Background(), finalized)
		published <- err
	}()
	<-entered
	late := make(chan error, 1)
	go func() {
		_, ok, err := app.memory.appendAttributedTranscriptEntry(officeRoomID, "late-final-transcript", "", "Tom", "source_bound", "A late exact final arrived.", nil, true, finalized.ID)
		if err == nil && !ok {
			err = errors.New("late transcript was not appended")
		}
		late <- err
	}()
	select {
	case err := <-late:
		t.Fatalf("late old-sitting mutation crossed publication lease: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-published; err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.apiKey = ""
	app.mu.Unlock()
	if err := <-late; err != nil {
		t.Fatal(err)
	}
	waitForSourceEpisodeAbsent(t, ledger, finalized.ID)
}

func TestPostClosePublicationLinearizesNewFinalizationOutputAppend(t *testing.T) {
	app, finalized, ledger := finalizedMeetingForSourceEpisodeTest(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var stallFirstWrite sync.Once
	ledger.write = func(path string, raw []byte) error {
		stallFirstWrite.Do(func() {
			close(entered)
			<-release
		})
		return appendFileDurably(path, raw, 0o600)
	}
	published := make(chan error, 1)
	go func() {
		_, err := app.publishFinalizedMeetingSourceEpisode(context.Background(), finalized)
		published <- err
	}()
	<-entered

	prior := sourceEpisodeTestEntry(t, app, finalized.ID, meetingMemoryKindBrain, finalized.Finalization.Brain.OutputID)
	text := prior.Text + "\nReplacement finalization output."
	metadata := cloneStringMap(prior.Metadata)
	metadata[meetingFinalizationOutputDigestMetadataKey] = sha256Hex([]byte(text))
	appended := make(chan error, 1)
	go func() {
		_, ok, err := app.memory.appendBrainWriteUp("replacement-finalization-output", text, metadata)
		if err == nil && !ok {
			err = errors.New("replacement finalization output was not appended")
		}
		appended <- err
	}()
	select {
	case err := <-appended:
		t.Fatalf("new output append crossed publication lease: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-published; err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.apiKey = ""
	app.mu.Unlock()
	if err := <-appended; err != nil {
		t.Fatal(err)
	}
	waitForSourceEpisodeAbsent(t, ledger, finalized.ID)
}

func TestPostClosePublicationFailureAndOutputMutationsLeaveNoActiveGhost(t *testing.T) {
	t.Run("definite write failure", func(t *testing.T) {
		app, finalized, ledger := finalizedMeetingForSourceEpisodeTest(t)
		ledger.write = func(string, []byte) error { return errors.New("injected episode write failure") }
		if _, err := app.publishFinalizedMeetingSourceEpisode(context.Background(), finalized); err == nil {
			t.Fatal("publication unexpectedly succeeded")
		}
		restarted, err := OpenFileSourceEpisodeLedger(postCloseMeetingSourceEpisodeLedgerPath())
		if err != nil {
			t.Fatal(err)
		}
		if _, found, err := restarted.CurrentSourceEpisode(context.Background(), canonicalTenantID(), meetingSourceEpisodeID(finalized.ID)); err != nil || found {
			t.Fatalf("failed publication ghost found=%v err=%v", found, err)
		}
	})

	for _, mutation := range []string{"analysis_update", "transcript_delete"} {
		t.Run(mutation, func(t *testing.T) {
			app, finalized, ledger := finalizedMeetingForSourceEpisodeTest(t)
			if _, err := app.publishFinalizedMeetingSourceEpisode(context.Background(), finalized); err != nil {
				t.Fatal(err)
			}
			app.mu.Lock()
			app.apiKey = ""
			app.mu.Unlock()
			var err error
			if mutation == "analysis_update" {
				brain := sourceEpisodeTestEntry(t, app, finalized.ID, meetingMemoryKindBrain, finalized.Finalization.Brain.OutputID)
				_, _, err = app.memory.updateEntryWithMetadata(brain.Kind, brain.ID, brain.Text+"\nCorrected after publication.", nil)
			} else {
				_, _, err = app.memory.deleteEntryByID("source-episode-transcript")
			}
			if err != nil {
				t.Fatal(err)
			}
			waitForSourceEpisodeAbsent(t, ledger, finalized.ID)
		})
	}
}
