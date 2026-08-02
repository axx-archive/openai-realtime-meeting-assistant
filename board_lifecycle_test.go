package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func resetBoardLifecycleHooksForTest(t *testing.T) {
	t.Helper()
	previousAppend := boardLifecycleAppend
	previousReplace := boardLifecycleReplace
	previousSyncDirectory := boardLifecycleSyncDirectory
	previousAtomicDirectorySync := syncDirectoryForAtomicWrite
	previousAtomicFileSync := syncTemporaryFileForAtomicWrite
	t.Cleanup(func() {
		boardLifecycleAppend = previousAppend
		boardLifecycleReplace = previousReplace
		boardLifecycleSyncDirectory = previousSyncDirectory
		syncDirectoryForAtomicWrite = previousAtomicDirectorySync
		syncTemporaryFileForAtomicWrite = previousAtomicFileSync
	})
}

func lifecycleRecordPhaseForTest(t *testing.T, raw []byte) string {
	t.Helper()
	var record CanonicalLifecycleJournalRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("decode lifecycle append: %v", err)
	}
	return record.Phase
}

func firstBoardCardForTest(t *testing.T, app *kanbanBoardApp) kanbanCard {
	t.Helper()
	cards := app.snapshotState().Cards
	if len(cards) == 0 {
		t.Fatal("test board has no cards")
	}
	return cards[0]
}

func boardHasCardForTest(app *kanbanBoardApp, cardID string) bool {
	for _, card := range app.snapshotState().Cards {
		if card.ID == cardID {
			return true
		}
	}
	return false
}

func TestBoardLifecyclePrepareFailureDoesNotPublishOrReplaceUndo(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	card := firstBoardCardForTest(t, app)
	priorUndo := cloneKanbanCard(app.snapshotState().Cards[1])
	app.lastDeletedCard = &priorUndo
	resetBoardLifecycleHooksForTest(t)
	boardLifecycleAppend = func(_ string, data []byte) error {
		if lifecycleRecordPhaseForTest(t, data) == canonicalLifecyclePhasePrepared {
			return errors.New("forced prepare failure")
		}
		return nil
	}

	if _, changed, err := app.deleteTicket(map[string]any{"card_id": card.ID}); err == nil || changed {
		t.Fatalf("delete after prepare failure changed=%v err=%v", changed, err)
	}
	if !boardHasCardForTest(app, card.ID) {
		t.Fatal("prepare failure published the board delete")
	}
	if app.lastDeletedCard == nil || app.lastDeletedCard.ID != priorUndo.ID {
		t.Fatalf("prepare failure replaced prior undo: %+v", app.lastDeletedCard)
	}
	if records, err := readCanonicalLifecycleJournal(boardLifecycleJournalPath()); err != nil || len(records) != 0 {
		t.Fatalf("prepare failure journal=%+v err=%v", records, err)
	}
}

func TestBoardLifecycleModeOffFileSyncFailureNeverCommits(t *testing.T) {
	t.Setenv("BONFIRE_CANONICAL_MODE", "off")
	app := newIsolatedKanbanBoardApp(t)
	card := firstBoardCardForTest(t, app)
	resetBoardLifecycleHooksForTest(t)
	syncTemporaryFileForAtomicWrite = func(*os.File) error { return errors.New("forced board file sync failure") }

	if _, changed, err := app.deleteTicket(map[string]any{"card_id": card.ID}); err == nil || changed {
		t.Fatalf("delete after board failure changed=%v err=%v", changed, err)
	}
	if !boardHasCardForTest(app, card.ID) {
		t.Fatal("prepublication failure removed the card")
	}
	records, err := readCanonicalLifecycleJournal(boardLifecycleJournalPath())
	if err != nil || len(records) != 2 ||
		records[0].Phase != canonicalLifecyclePhasePrepared ||
		records[1].Phase != canonicalLifecyclePhaseAborted {
		t.Fatalf("prepublication lifecycle=%+v err=%v", records, err)
	}
	if committed, err := boardLifecycleCommittedRecords(boardLifecycleJournalPath()); err != nil || len(committed) != 0 {
		t.Fatalf("file-sync failure exposed COMMITTED=%+v err=%v", committed, err)
	}
}

func TestBoardLifecycleModeOffSuccessfulDeleteCrossesFileAndDirectorySync(t *testing.T) {
	t.Setenv("BONFIRE_CANONICAL_MODE", "off")
	app := newIsolatedKanbanBoardApp(t)
	card := firstBoardCardForTest(t, app)
	resetBoardLifecycleHooksForTest(t)
	journalPath := boardLifecycleJournalPath()
	if err := os.WriteFile(journalPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syncDirectoryForAtomicWrite(filepath.Dir(journalPath)); err != nil {
		t.Fatal(err)
	}
	realFileSync := syncTemporaryFileForAtomicWrite
	realDirectorySync := syncDirectoryForAtomicWrite
	fileSyncs := 0
	directorySyncs := 0
	syncTemporaryFileForAtomicWrite = func(file *os.File) error {
		fileSyncs++
		return realFileSync(file)
	}
	syncDirectoryForAtomicWrite = func(path string) error {
		directorySyncs++
		return realDirectorySync(path)
	}

	if _, changed, err := app.deleteTicket(map[string]any{"card_id": card.ID}); err != nil || !changed {
		t.Fatalf("mode-off durable delete changed=%v err=%v", changed, err)
	}
	if fileSyncs != 1 || directorySyncs != 1 {
		t.Fatalf("mode-off durability fileSyncs=%d directorySyncs=%d, want 1/1", fileSyncs, directorySyncs)
	}
	if committed, err := boardLifecycleCommittedRecords(journalPath); err != nil || len(committed) != 1 {
		t.Fatalf("durable delete committed=%+v err=%v", committed, err)
	}
}

func TestBoardLifecycleCommitFailureFreezesThenRecoversFromVisibleAfter(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	card := firstBoardCardForTest(t, app)
	priorUndo := cloneKanbanCard(app.snapshotState().Cards[1])
	app.lastDeletedCard = &priorUndo
	resetBoardLifecycleHooksForTest(t)
	realAppend := boardLifecycleAppend
	failCommit := true
	boardLifecycleAppend = func(path string, data []byte) error {
		if failCommit && lifecycleRecordPhaseForTest(t, data) == canonicalLifecyclePhaseCommitted {
			return errors.New("forced commit failure")
		}
		return realAppend(path, data)
	}

	if _, changed, err := app.deleteTicket(map[string]any{"card_id": card.ID}); err == nil || changed {
		t.Fatalf("delete after commit failure changed=%v err=%v", changed, err)
	}
	if boardHasCardForTest(app, card.ID) {
		t.Fatal("commit append failure lost the already-published after state")
	}
	if !app.boardLifecycleFrozen {
		t.Fatal("commit append failure did not freeze board mutation")
	}
	if app.lastDeletedCard == nil || app.lastDeletedCard.ID != priorUndo.ID {
		t.Fatalf("indeterminate delete replaced prior undo: %+v", app.lastDeletedCard)
	}

	failCommit = false
	if _, changed, err := app.createTicket(map[string]any{"title": "Allowed only after recovery"}); err != nil || !changed {
		t.Fatalf("next mutation did not recover pending commit changed=%v err=%v", changed, err)
	}
	if app.boardLifecycleFrozen {
		t.Fatal("successful exact-after recovery left board frozen")
	}
	records, err := readCanonicalLifecycleJournal(boardLifecycleJournalPath())
	if err != nil || len(records) != 2 || records[1].Phase != canonicalLifecyclePhaseCommitted {
		t.Fatalf("recovered commit lifecycle=%+v err=%v", records, err)
	}
}

func TestBoardLifecycleAmbiguousVisibleAfterSyncsDirectoryBeforeCommit(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	card := firstBoardCardForTest(t, app)
	resetBoardLifecycleHooksForTest(t)
	boardLifecycleReplace = func(path string, data []byte) error {
		if err := writeFileAtomicallyBestEffort(path, data, 0o600); err != nil {
			return err
		}
		return ErrDurableReplaceAmbiguous
	}
	synced := false
	boardLifecycleSyncDirectory = func(string) error {
		synced = true
		return nil
	}

	if _, changed, err := app.deleteTicket(map[string]any{"card_id": card.ID}); err != nil || !changed {
		t.Fatalf("durably resolved visible-after delete changed=%v err=%v", changed, err)
	}
	if !synced {
		t.Fatal("visible-after recovery committed without syncing the board directory")
	}
	if boardHasCardForTest(app, card.ID) || app.boardLifecycleFrozen {
		t.Fatalf("visible-after resolution cardPresent=%v frozen=%v", boardHasCardForTest(app, card.ID), app.boardLifecycleFrozen)
	}
	records, err := readCanonicalLifecycleJournal(boardLifecycleJournalPath())
	if err != nil || len(records) != 2 || records[1].Phase != canonicalLifecyclePhaseCommitted {
		t.Fatalf("visible-after lifecycle=%+v err=%v", records, err)
	}
}

func TestBoardLifecycleAmbiguousVisibleAfterSyncFailureStaysFrozenUntilRecovery(t *testing.T) {
	t.Setenv("BONFIRE_CANONICAL_MODE", "off")
	app := newIsolatedKanbanBoardApp(t)
	card := firstBoardCardForTest(t, app)
	resetBoardLifecycleHooksForTest(t)
	// Keep the lifecycle journal's creation outside the injected board
	// directory-sync failure so PREPARED itself remains durable.
	if err := os.WriteFile(boardLifecycleJournalPath(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syncDirectoryForAtomicWrite(filepath.Dir(boardLifecycleJournalPath())); err != nil {
		t.Fatal(err)
	}
	failSync := true
	realDirectorySync := syncDirectoryForAtomicWrite
	syncDirectoryForAtomicWrite = func(path string) error {
		if failSync {
			return errors.New("forced directory sync failure")
		}
		return realDirectorySync(path)
	}

	if _, changed, err := app.deleteTicket(map[string]any{"card_id": card.ID}); err == nil || changed {
		t.Fatalf("directory-sync failure delete changed=%v err=%v", changed, err)
	}
	if !app.boardLifecycleFrozen || boardHasCardForTest(app, card.ID) {
		t.Fatalf("directory-sync failure cardPresent=%v frozen=%v", boardHasCardForTest(app, card.ID), app.boardLifecycleFrozen)
	}
	if records, err := readCanonicalLifecycleJournal(boardLifecycleJournalPath()); err != nil || len(records) != 1 ||
		records[0].Phase != canonicalLifecyclePhasePrepared {
		t.Fatalf("sync-failed lifecycle=%+v err=%v", records, err)
	}
	if committed, err := boardLifecycleCommittedRecords(boardLifecycleJournalPath()); err != nil || len(committed) != 0 {
		t.Fatalf("parent-directory sync failure exposed COMMITTED=%+v err=%v", committed, err)
	}

	failSync = false
	if _, changed, err := app.createTicket(map[string]any{"title": "After durable recovery"}); err != nil || !changed {
		t.Fatalf("directory-sync recovery changed=%v err=%v", changed, err)
	}
	if app.boardLifecycleFrozen {
		t.Fatal("durable recovery did not clear board freeze")
	}
}

func TestBoardLifecycleAbortFailureFreezesThenRecoversFromVisibleBefore(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	card := firstBoardCardForTest(t, app)
	resetBoardLifecycleHooksForTest(t)
	realAppend := boardLifecycleAppend
	failAbort := true
	boardLifecycleAppend = func(path string, data []byte) error {
		if failAbort && lifecycleRecordPhaseForTest(t, data) == canonicalLifecyclePhaseAborted {
			return errors.New("forced abort failure")
		}
		return realAppend(path, data)
	}
	boardLifecycleReplace = func(string, []byte) error { return errors.New("forced board failure") }

	if _, changed, err := app.deleteTicket(map[string]any{"card_id": card.ID}); err == nil || changed {
		t.Fatalf("delete after abort failure changed=%v err=%v", changed, err)
	}
	if !boardHasCardForTest(app, card.ID) || !app.boardLifecycleFrozen {
		t.Fatalf("abort failure state cardPresent=%v frozen=%v", boardHasCardForTest(app, card.ID), app.boardLifecycleFrozen)
	}

	failAbort = false
	boardLifecycleReplace = func(path string, data []byte) error {
		return writeFileAtomicallyForCanonicalMode(path, data, 0o600)
	}
	if _, changed, err := app.moveTicket(map[string]any{"card_id": card.ID, "status": "Blocked"}); err != nil || !changed {
		t.Fatalf("next mutation did not recover pending abort changed=%v err=%v", changed, err)
	}
	records, err := readCanonicalLifecycleJournal(boardLifecycleJournalPath())
	if err != nil || len(records) != 2 || records[1].Phase != canonicalLifecyclePhaseAborted {
		t.Fatalf("recovered abort lifecycle=%+v err=%v", records, err)
	}
}

func prepareManualBoardDeleteForTest(t *testing.T, app *kanbanBoardApp, publishAfter bool) CanonicalLifecycleJournalRecord {
	t.Helper()
	card := firstBoardCardForTest(t, app)
	object, err := canonicalBoardCardImportedObject(card, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	beforeRaw, err := os.ReadFile(kanbanBoardPath())
	if err != nil {
		t.Fatal(err)
	}
	before, found, err := loadKanbanBoardState(kanbanBoardPath())
	if err != nil || !found {
		t.Fatalf("load board found=%v err=%v", found, err)
	}
	next := make([]kanbanCard, 0, len(before.Cards)-1)
	for _, candidate := range before.Cards {
		if candidate.ID != card.ID {
			next = append(next, candidate)
		}
	}
	afterRaw, err := marshalKanbanBoardState(kanbanBoardState{
		Cards:     next,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	record := CanonicalLifecycleJournalRecord{
		Family: "board_card", ObjectID: card.ID, StateDigest: object.StateDigest,
		OperationID: "operation-" + strings.ReplaceAll(card.ID, ":", "-"),
		Phase:       canonicalLifecyclePhasePrepared, At: time.Now().UTC(), Reason: "test_crash",
		BoardBeforeSHA256: exactSHA256(beforeRaw), BoardAfterSHA256: exactSHA256(afterRaw),
	}
	if err := appendBoardLifecyclePhase(boardLifecycleJournalPath(), record); err != nil {
		t.Fatal(err)
	}
	if publishAfter {
		if err := writeFileAtomicallyForCanonicalMode(kanbanBoardPath(), afterRaw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return record
}

func TestBoardLifecycleCrashAfterPrepareRecoversAbortIdempotently(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	record := prepareManualBoardDeleteForTest(t, app, false)
	if err := recoverBoardLifecycleTransactions(kanbanBoardPath(), boardLifecycleJournalPath()); err != nil {
		t.Fatal(err)
	}
	if err := recoverBoardLifecycleTransactions(kanbanBoardPath(), boardLifecycleJournalPath()); err != nil {
		t.Fatal(err)
	}
	records, err := readCanonicalLifecycleJournal(boardLifecycleJournalPath())
	if err != nil || len(records) != 2 || records[1].OperationID != record.OperationID ||
		records[1].Phase != canonicalLifecyclePhaseAborted {
		t.Fatalf("prepare crash recovery=%+v err=%v", records, err)
	}
}

func TestBoardLifecycleCrashAfterPublishRecoversCommitIdempotently(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	record := prepareManualBoardDeleteForTest(t, app, true)
	if err := recoverBoardLifecycleTransactions(kanbanBoardPath(), boardLifecycleJournalPath()); err != nil {
		t.Fatal(err)
	}
	if err := recoverBoardLifecycleTransactions(kanbanBoardPath(), boardLifecycleJournalPath()); err != nil {
		t.Fatal(err)
	}
	records, err := readCanonicalLifecycleJournal(boardLifecycleJournalPath())
	if err != nil || len(records) != 2 || records[1].OperationID != record.OperationID ||
		records[1].Phase != canonicalLifecyclePhaseCommitted {
		t.Fatalf("publish crash recovery=%+v err=%v", records, err)
	}
}

func TestBoardLifecycleUnknownVisibleDigestFreezesMutationAndImport(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	record := prepareManualBoardDeleteForTest(t, app, false)
	record.OperationID = "unknown-visible-digest"
	record.BoardBeforeSHA256 = strings.Repeat("a", 64)
	record.BoardAfterSHA256 = strings.Repeat("b", 64)
	// Replace the valid prepared-only journal with a single internally valid but
	// externally unprovable operation.
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(boardLifecycleJournalPath(), append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := app.createTicket(map[string]any{"title": "Must be fenced"}); err == nil || changed {
		t.Fatalf("unknown visible digest mutation changed=%v err=%v", changed, err)
	}
	if !app.boardLifecycleFrozen {
		t.Fatal("unknown digest did not freeze board")
	}

	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	versions, err := OpenFileCanonicalObjectVersionMap(filepath.Join(t.TempDir(), "versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	importer := &CanonicalImporter{
		TenantID: "test", Registry: registry, Versions: versions,
		Paths: CanonicalImportPaths{Board: kanbanBoardPath(), DeletedJournal: boardLifecycleJournalPath()},
	}
	if _, err := importer.Build(t.Context()); err == nil || !strings.Contains(err.Error(), "recover board lifecycle") {
		t.Fatalf("canonical import did not fail closed: %v", err)
	}
}

func TestBoardLifecycleRejectsExactAfterWithTargetPresentAndExactBeforeWithTargetAbsent(t *testing.T) {
	t.Run("exact_after_target_present", func(t *testing.T) {
		app := newIsolatedKanbanBoardApp(t)
		card := firstBoardCardForTest(t, app)
		object, err := canonicalBoardCardImportedObject(card, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(kanbanBoardPath())
		if err != nil {
			t.Fatal(err)
		}
		record := CanonicalLifecycleJournalRecord{
			Family: "board_card", ObjectID: card.ID, StateDigest: object.StateDigest,
			OperationID: "after-still-present", Phase: canonicalLifecyclePhasePrepared,
			BoardBeforeSHA256: strings.Repeat("a", 64), BoardAfterSHA256: exactSHA256(raw),
			At: time.Now().UTC(), Reason: "adversarial_after",
		}
		if err := appendBoardLifecyclePhase(boardLifecycleJournalPath(), record); err != nil {
			t.Fatal(err)
		}
		if err := recoverBoardLifecycleTransactions(kanbanBoardPath(), boardLifecycleJournalPath()); err == nil ||
			!strings.Contains(err.Error(), "indeterminate") {
			t.Fatalf("exact-after with target present was guessed: %v", err)
		}
	})

	t.Run("exact_before_target_absent", func(t *testing.T) {
		app := newIsolatedKanbanBoardApp(t)
		card := firstBoardCardForTest(t, app)
		object, err := canonicalBoardCardImportedObject(card, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		state := app.snapshotState()
		state.Cards = state.Cards[1:]
		state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		raw, err := marshalKanbanBoardState(state)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeFileAtomicallyForCanonicalMode(kanbanBoardPath(), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		record := CanonicalLifecycleJournalRecord{
			Family: "board_card", ObjectID: card.ID, StateDigest: object.StateDigest,
			OperationID: "before-already-absent", Phase: canonicalLifecyclePhasePrepared,
			BoardBeforeSHA256: exactSHA256(raw), BoardAfterSHA256: strings.Repeat("b", 64),
			At: time.Now().UTC(), Reason: "adversarial_before",
		}
		if err := appendBoardLifecyclePhase(boardLifecycleJournalPath(), record); err != nil {
			t.Fatal(err)
		}
		if err := recoverBoardLifecycleTransactions(kanbanBoardPath(), boardLifecycleJournalPath()); err == nil ||
			!strings.Contains(err.Error(), "indeterminate") {
			t.Fatalf("exact-before with target absent was guessed: %v", err)
		}
	})
}

func TestBoardLifecycleImporterCannotAbortInFlightPreparedDelete(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	card := firstBoardCardForTest(t, app)
	resetBoardLifecycleHooksForTest(t)
	realReplace := boardLifecycleReplace
	replaceEntered := make(chan struct{})
	allowReplace := make(chan struct{})
	boardLifecycleReplace = func(path string, data []byte) error {
		close(replaceEntered)
		<-allowReplace
		return realReplace(path, data)
	}
	deleteDone := make(chan error, 1)
	go func() {
		_, _, err := app.deleteTicket(map[string]any{"card_id": card.ID})
		deleteDone <- err
	}()
	<-replaceEntered

	importStarted := make(chan struct{})
	importDone := make(chan error, 1)
	go func() {
		close(importStarted)
		_, err := boardLifecycleCommittedRecords(boardLifecycleJournalPath())
		importDone <- err
	}()
	<-importStarted
	select {
	case err := <-importDone:
		t.Fatalf("import observed in-flight PREPARED instead of blocking: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(allowReplace)
	if err := <-deleteDone; err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := <-importDone; err != nil {
		t.Fatalf("committed import: %v", err)
	}
	records, err := readCanonicalLifecycleJournal(boardLifecycleJournalPath())
	if err != nil || len(records) != 2 || records[1].Phase != canonicalLifecyclePhaseCommitted {
		t.Fatalf("in-flight lifecycle=%+v err=%v", records, err)
	}
}

func TestBoardLifecycleRejectsConflictingDuplicateOperation(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	record := prepareManualBoardDeleteForTest(t, app, false)
	conflict := record
	conflict.ObjectID = "different-target"
	conflict.At = conflict.At.Add(time.Second)
	if err := appendBoardLifecyclePhase(boardLifecycleJournalPath(), conflict); err != nil {
		t.Fatal(err)
	}
	if err := recoverBoardLifecycleTransactions(kanbanBoardPath(), boardLifecycleJournalPath()); err == nil ||
		!strings.Contains(err.Error(), "conflicting duplicate") {
		t.Fatalf("conflicting operation was not rejected: %v", err)
	}
}

func TestBoardLifecycleExactDuplicatePrepareRecoveryIsIdempotent(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	record := prepareManualBoardDeleteForTest(t, app, false)
	duplicate := record
	duplicate.At = duplicate.At.Add(time.Millisecond)
	if err := appendBoardLifecyclePhase(boardLifecycleJournalPath(), duplicate); err != nil {
		t.Fatal(err)
	}
	if err := recoverBoardLifecycleTransactions(kanbanBoardPath(), boardLifecycleJournalPath()); err != nil {
		t.Fatal(err)
	}
	if err := recoverBoardLifecycleTransactions(kanbanBoardPath(), boardLifecycleJournalPath()); err != nil {
		t.Fatal(err)
	}
	records, err := readCanonicalLifecycleJournal(boardLifecycleJournalPath())
	if err != nil || len(records) != 3 ||
		records[0].OperationID != record.OperationID ||
		records[1].OperationID != record.OperationID ||
		records[2].Phase != canonicalLifecyclePhaseAborted {
		t.Fatalf("duplicate prepare recovery=%+v err=%v", records, err)
	}
}

func TestLifecycleImporterExcludesPreparedAndAbortedOperations(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	prepared := prepareManualBoardDeleteForTest(t, app, false)
	objects, err := importLifecycleJournal(boardLifecycleJournalPath(), "tombstone")
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 0 {
		t.Fatalf("prepared operation leaked into import: %+v", objects)
	}
	aborted := prepared
	aborted.Phase = canonicalLifecyclePhaseAborted
	aborted.At = time.Now().UTC()
	if err := appendBoardLifecyclePhase(boardLifecycleJournalPath(), aborted); err != nil {
		t.Fatal(err)
	}
	objects, err = importLifecycleJournal(boardLifecycleJournalPath(), "tombstone")
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 0 {
		t.Fatalf("aborted operation leaked into import: %+v", objects)
	}
}
