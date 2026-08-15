package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	canonicalLifecyclePhasePrepared  = "prepared"
	canonicalLifecyclePhaseCommitted = "committed"
	canonicalLifecyclePhaseAborted   = "aborted"
)

// These indirections are intentionally narrow test seams. Production always
// uses the durable append and atomic board replacement implementations.
var (
	boardLifecycleAppend = func(path string, data []byte) error {
		return appendFileDurably(path, data, 0o600)
	}
	boardLifecycleReplace = func(path string, data []byte) error {
		// A lifecycle COMMITTED marker is authoritative regardless of whether
		// canonical capture is enabled. The board file and its directory entry
		// therefore cross the durable-write fence in every canonical mode.
		return writeFileAtomicallyDurable(path, data, 0o600)
	}
	boardLifecycleSyncDirectory = func(path string) error { return syncDirectoryForAtomicWrite(path) }
)

type boardLifecycleOperation struct {
	prepared *CanonicalLifecycleJournalRecord
	terminal *CanonicalLifecycleJournalRecord
}

func boardLifecycleJournalPath() string {
	return canonicalDeletedLifecycleJournalPath()
}

func marshalKanbanBoardState(state kanbanBoardState) ([]byte, error) {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Kanban board: %w", err)
	}
	return append(raw, '\n'), nil
}

func exactSHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func boardLifecycleTerminalTime(preparedAt time.Time) time.Time {
	now := time.Now().UTC()
	if !now.After(preparedAt) {
		return preparedAt.Add(time.Nanosecond)
	}
	return now
}

func appendBoardLifecyclePhaseLocked(path string, record CanonicalLifecycleJournalRecord) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := boardLifecycleAppend(path, append(encoded, '\n')); err != nil {
		return err
	}
	return nil
}

func appendBoardLifecyclePhase(path string, record CanonicalLifecycleJournalRecord) error {
	canonicalLifecycleJournalMu.Lock()
	defer canonicalLifecycleJournalMu.Unlock()
	return appendBoardLifecyclePhaseLocked(path, record)
}

func validBoardLifecycleRecord(record CanonicalLifecycleJournalRecord) error {
	if record.OperationID == "" {
		return errors.New("operation_id is required for phased lifecycle records")
	}
	switch record.Phase {
	case canonicalLifecyclePhasePrepared, canonicalLifecyclePhaseCommitted, canonicalLifecyclePhaseAborted:
	default:
		return fmt.Errorf("unknown lifecycle phase %q", record.Phase)
	}
	if strings.TrimSpace(record.ObjectID) == "" {
		return errors.New("phased lifecycle records require an object target")
	}
	if !isHexDigest(record.StateDigest) {
		return errors.New("phased lifecycle record requires an exact state digest")
	}
	if record.Family == "board_card" {
		if !isHexDigest(record.BoardBeforeSHA256) || !isHexDigest(record.BoardAfterSHA256) {
			return errors.New("board lifecycle record requires exact before and after SHA-256 digests")
		}
		if record.BoardBeforeSHA256 == record.BoardAfterSHA256 {
			return errors.New("board lifecycle before and after digests must differ")
		}
	} else {
		switch record.Family {
		case "memory", "artifact_revision", "notification", "file_folder", "file_assignment":
		default:
			return fmt.Errorf("unsupported phased lifecycle family %q", record.Family)
		}
		if record.BoardBeforeSHA256 != "" || record.BoardAfterSHA256 != "" {
			return errors.New("non-board lifecycle record cannot carry board digests")
		}
	}
	return nil
}

func sameBoardLifecycleOperation(left, right CanonicalLifecycleJournalRecord) bool {
	return left.OperationID == right.OperationID &&
		left.Family == right.Family &&
		left.ObjectID == right.ObjectID &&
		left.StateDigest == right.StateDigest &&
		left.BoardBeforeSHA256 == right.BoardBeforeSHA256 &&
		left.BoardAfterSHA256 == right.BoardAfterSHA256 &&
		left.Reason == right.Reason &&
		left.EvidenceBasis == right.EvidenceBasis
}

// classifyLifecycleJournal treats phase-less historical entries as committed.
// New phased operations must be a consistent PREPARED -> COMMITTED|ABORTED
// append-only sequence. Conflicting duplicates fail closed.
func classifyLifecycleJournal(records []CanonicalLifecycleJournalRecord) ([]CanonicalLifecycleJournalRecord, map[string]boardLifecycleOperation, error) {
	committed := make([]CanonicalLifecycleJournalRecord, 0, len(records))
	operations := map[string]boardLifecycleOperation{}
	for index := range records {
		record := records[index]
		if strings.TrimSpace(record.Phase) == "" {
			committed = append(committed, record)
			continue
		}
		if err := validBoardLifecycleRecord(record); err != nil {
			return nil, nil, fmt.Errorf("lifecycle operation %q: %w", record.OperationID, err)
		}
		operation := operations[record.OperationID]
		switch record.Phase {
		case canonicalLifecyclePhasePrepared:
			if operation.prepared != nil {
				if !sameBoardLifecycleOperation(*operation.prepared, record) {
					return nil, nil, fmt.Errorf("conflicting duplicate lifecycle prepare %s", record.OperationID)
				}
				continue
			}
			copy := record
			operation.prepared = &copy
		case canonicalLifecyclePhaseCommitted, canonicalLifecyclePhaseAborted:
			if operation.prepared == nil {
				return nil, nil, fmt.Errorf("lifecycle terminal phase without prepare %s", record.OperationID)
			}
			if !sameBoardLifecycleOperation(*operation.prepared, record) {
				return nil, nil, fmt.Errorf("lifecycle terminal phase conflicts with prepare %s", record.OperationID)
			}
			if operation.terminal != nil {
				if operation.terminal.Phase != record.Phase || !sameBoardLifecycleOperation(*operation.terminal, record) {
					return nil, nil, fmt.Errorf("conflicting lifecycle terminal phase %s", record.OperationID)
				}
				continue
			}
			copy := record
			operation.terminal = &copy
			if record.Phase == canonicalLifecyclePhaseCommitted {
				committed = append(committed, record)
			}
		}
		operations[record.OperationID] = operation
	}
	return committed, operations, nil
}

func boardContainsExactTarget(raw []byte, objectID, stateDigest string) (bool, error) {
	var state kanbanBoardState
	if err := json.Unmarshal(raw, &state); err != nil {
		return false, fmt.Errorf("decode visible Kanban board: %w", err)
	}
	for _, card := range state.Cards {
		if card.ID != objectID {
			continue
		}
		object, err := canonicalBoardCardImportedObject(card, time.Time{})
		if err != nil {
			return false, err
		}
		if object.StateDigest != stateDigest {
			return false, fmt.Errorf("visible target %s has unexpected state digest", objectID)
		}
		return true, nil
	}
	return false, nil
}

// recoverBoardLifecycleTransactions resolves only outcomes proven by the exact
// visible board bytes. It never infers intent from an object ID alone.
func recoverBoardLifecycleTransactions(boardPath, journalPath string) error {
	canonicalLifecycleJournalMu.Lock()
	defer canonicalLifecycleJournalMu.Unlock()
	return recoverBoardLifecycleTransactionsLocked(boardPath, journalPath)
}

func recoverBoardLifecycleTransactionsLocked(boardPath, journalPath string) error {
	records, err := readCanonicalLifecycleJournal(journalPath)
	if err != nil {
		return fmt.Errorf("read board lifecycle journal: %w", err)
	}
	_, operations, err := classifyLifecycleJournal(records)
	if err != nil {
		return err
	}
	pendingIDs := make([]string, 0)
	for operationID, operation := range operations {
		if operation.prepared != nil && operation.terminal == nil && operation.prepared.Family == "board_card" {
			pendingIDs = append(pendingIDs, operationID)
		}
	}
	sort.Slice(pendingIDs, func(i, j int) bool {
		return operations[pendingIDs[i]].prepared.At.Before(operations[pendingIDs[j]].prepared.At)
	})
	for _, operationID := range pendingIDs {
		prepared := *operations[operationID].prepared
		raw, readErr := os.ReadFile(boardPath)
		if readErr != nil {
			return fmt.Errorf("recover board lifecycle %s: read visible board: %w", operationID, readErr)
		}
		visibleDigest := exactSHA256(raw)
		present, targetErr := boardContainsExactTarget(raw, prepared.ObjectID, prepared.StateDigest)
		if targetErr != nil {
			return fmt.Errorf("recover board lifecycle %s: %w", operationID, targetErr)
		}
		terminal := prepared
		terminal.At = boardLifecycleTerminalTime(prepared.At)
		switch {
		case visibleDigest == prepared.BoardBeforeSHA256 && present:
			terminal.Phase = canonicalLifecyclePhaseAborted
		case visibleDigest == prepared.BoardAfterSHA256 && !present:
			// A prior atomic rename may have returned
			// ErrDurableReplaceAmbiguous because the parent-directory fsync
			// failed. Visibility alone is not crash durability; repair that
			// durability boundary before recording COMMITTED.
			if err := boardLifecycleSyncDirectory(filepath.Dir(boardPath)); err != nil {
				return fmt.Errorf("recover board lifecycle %s sync board directory: %w", operationID, err)
			}
			terminal.Phase = canonicalLifecyclePhaseCommitted
		default:
			return fmt.Errorf("recover board lifecycle %s: visible board digest/target state is indeterminate", operationID)
		}
		if err := appendBoardLifecyclePhaseLocked(journalPath, terminal); err != nil {
			return fmt.Errorf("recover board lifecycle %s append %s: %w", operationID, terminal.Phase, err)
		}
	}
	return nil
}

func (app *kanbanBoardApp) recoverBoardLifecycleLocked() error {
	if err := recoverBoardLifecycleTransactions(kanbanBoardPath(), boardLifecycleJournalPath()); err != nil {
		app.boardLifecycleFrozen = true
		app.boardLifecycleErr = err
		return fmt.Errorf("project board lifecycle is frozen: %w", err)
	}
	visible, found, err := loadKanbanBoardState(kanbanBoardPath())
	if err != nil {
		app.boardLifecycleFrozen = true
		app.boardLifecycleErr = err
		return fmt.Errorf("project board lifecycle is frozen: %w", err)
	}
	if found {
		app.cards = cloneKanbanCards(visible.Cards)
		if parsed, parseErr := time.Parse(time.RFC3339Nano, visible.UpdatedAt); parseErr == nil {
			app.updatedAt = parsed.UTC()
		}
	}
	app.boardLifecycleFrozen = false
	app.boardLifecycleErr = nil
	return nil
}

// recoverBoardLifecycleWithJournalLockLocked is called only while both app.mu
// and canonicalLifecycleJournalMu are held, in that order. Holding the journal
// lock from PREPARED through the board replacement and terminal append prevents
// an importer/recovery observer from aborting an in-flight deletion.
func (app *kanbanBoardApp) recoverBoardLifecycleWithJournalLockLocked() error {
	if err := recoverBoardLifecycleTransactionsLocked(kanbanBoardPath(), boardLifecycleJournalPath()); err != nil {
		app.boardLifecycleFrozen = true
		app.boardLifecycleErr = err
		return fmt.Errorf("project board lifecycle is frozen: %w", err)
	}
	visible, found, err := loadKanbanBoardState(kanbanBoardPath())
	if err != nil {
		app.boardLifecycleFrozen = true
		app.boardLifecycleErr = err
		return fmt.Errorf("project board lifecycle is frozen: %w", err)
	}
	if found {
		app.cards = cloneKanbanCards(visible.Cards)
		if parsed, parseErr := time.Parse(time.RFC3339Nano, visible.UpdatedAt); parseErr == nil {
			app.updatedAt = parsed.UTC()
		}
	}
	app.boardLifecycleFrozen = false
	app.boardLifecycleErr = nil
	return nil
}

func (app *kanbanBoardApp) reloadVisibleBoardLockedBestEffort() {
	visible, found, err := loadKanbanBoardState(kanbanBoardPath())
	if err != nil || !found {
		return
	}
	app.cards = cloneKanbanCards(visible.Cards)
	if parsed, parseErr := time.Parse(time.RFC3339Nano, visible.UpdatedAt); parseErr == nil {
		app.updatedAt = parsed.UTC()
	}
}

func (app *kanbanBoardApp) boardLifecycleSnapshot() map[string]any {
	if app == nil {
		return map[string]any{"healthy": false, "reason": "app_unavailable"}
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	result := map[string]any{"healthy": !app.boardLifecycleFrozen}
	if app.boardLifecycleFrozen {
		result["reason"] = "recovery_required"
	}
	return result
}

func (app *kanbanBoardApp) deleteBoardCardTwoPhaseLocked(cardID, reason string, exposeUndo bool) (kanbanCard, error) {
	canonicalLifecycleJournalMu.Lock()
	defer canonicalLifecycleJournalMu.Unlock()

	if err := app.recoverBoardLifecycleWithJournalLockLocked(); err != nil {
		return kanbanCard{}, err
	}
	index := -1
	for candidateIndex := range app.cards {
		if app.cards[candidateIndex].ID == cardID {
			index = candidateIndex
			break
		}
	}
	if index == -1 {
		return kanbanCard{}, fmt.Errorf("unknown card_id after lifecycle recovery: %s", cardID)
	}
	deletedCard := cloneKanbanCard(app.cards[index])
	object, err := canonicalBoardCardImportedObject(deletedCard, time.Time{})
	if err != nil {
		return kanbanCard{}, fmt.Errorf("project board deletion %s: %w", deletedCard.ID, err)
	}
	beforeRaw, err := os.ReadFile(kanbanBoardPath())
	if err != nil {
		return kanbanCard{}, fmt.Errorf("read project board before deletion: %w", err)
	}
	present, err := boardContainsExactTarget(beforeRaw, deletedCard.ID, object.StateDigest)
	if err != nil || !present {
		return kanbanCard{}, fmt.Errorf("project board deletion %s source mismatch: %w", deletedCard.ID, err)
	}
	nextCards := cloneKanbanCards(app.cards)
	nextCards = append(nextCards[:index], nextCards[index+1:]...)
	nextUpdatedAt := time.Now().UTC()
	afterState := kanbanBoardState{Cards: nextCards, UpdatedAt: nextUpdatedAt.Format(time.RFC3339Nano)}
	afterRaw, err := marshalKanbanBoardState(afterState)
	if err != nil {
		return kanbanCard{}, err
	}
	prepared := CanonicalLifecycleJournalRecord{
		Family: object.Family, ObjectID: object.ObjectID, StateDigest: object.StateDigest,
		OperationID: uuid.NewString(), Phase: canonicalLifecyclePhasePrepared,
		BoardBeforeSHA256: exactSHA256(beforeRaw), BoardAfterSHA256: exactSHA256(afterRaw),
		At: time.Now().UTC(), Reason: reason,
	}
	journalPath := boardLifecycleJournalPath()
	if err := appendBoardLifecyclePhaseLocked(journalPath, prepared); err != nil {
		return kanbanCard{}, fmt.Errorf("prepare board deletion %s: %w", deletedCard.ID, err)
	}

	writeErr := boardLifecycleReplace(kanbanBoardPath(), afterRaw)
	if writeErr != nil {
		// Establish the exact visible outcome. A proven old board aborts; a
		// proven new board commits. Any other outcome freezes future writes.
		if recoveryErr := recoverBoardLifecycleTransactionsLocked(kanbanBoardPath(), journalPath); recoveryErr != nil {
			app.boardLifecycleFrozen = true
			app.boardLifecycleErr = recoveryErr
			app.reloadVisibleBoardLockedBestEffort()
			return kanbanCard{}, fmt.Errorf("publish board deletion %s: %v; recovery: %w", deletedCard.ID, writeErr, recoveryErr)
		}
		visibleRaw, readErr := os.ReadFile(kanbanBoardPath())
		if readErr != nil {
			app.boardLifecycleFrozen = true
			app.boardLifecycleErr = readErr
			return kanbanCard{}, fmt.Errorf("publish board deletion %s: %v; read recovered board: %w", deletedCard.ID, writeErr, readErr)
		}
		if exactSHA256(visibleRaw) != prepared.BoardAfterSHA256 {
			return kanbanCard{}, fmt.Errorf("publish board deletion %s aborted: %w", deletedCard.ID, writeErr)
		}
	}

	app.cards = nextCards
	app.updatedAt = nextUpdatedAt
	committed := prepared
	committed.Phase = canonicalLifecyclePhaseCommitted
	committed.At = boardLifecycleTerminalTime(prepared.At)
	if writeErr == nil {
		if err := appendBoardLifecyclePhaseLocked(journalPath, committed); err != nil {
			app.boardLifecycleFrozen = true
			app.boardLifecycleErr = err
			return kanbanCard{}, fmt.Errorf("commit board deletion %s: %w", deletedCard.ID, err)
		}
	}
	app.boardLifecycleFrozen = false
	app.boardLifecycleErr = nil
	if exposeUndo {
		copy := cloneKanbanCard(deletedCard)
		app.lastDeletedCard = &copy
	}
	return deletedCard, nil
}

// boardLifecycleCommittedRecords is the importer view: legacy phase-less
// records plus only fully committed new operations.
func boardLifecycleCommittedRecords(path string) ([]CanonicalLifecycleJournalRecord, error) {
	canonicalLifecycleJournalMu.Lock()
	defer canonicalLifecycleJournalMu.Unlock()
	return boardLifecycleCommittedRecordsLocked(path)
}

func boardLifecycleCommittedRecordsLocked(path string) ([]CanonicalLifecycleJournalRecord, error) {
	records, err := readCanonicalLifecycleJournal(path)
	if err != nil {
		return nil, err
	}
	committed, _, err := classifyLifecycleJournal(records)
	return committed, err
}
