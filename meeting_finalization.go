package main

// Durable, meeting-scoped close finalization. The meeting record owns the
// receipt so an EndedAt stamp, its exact source high-water, and the recovery
// state survive or roll back together. Core outputs are ordinary permissioned
// Brain/digest records; the receipt only proves which durable outputs cover the
// closed source and never becomes a second source of transcript truth.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	meetingFinalizationClosing   = "closing"
	meetingFinalizationFinalized = "finalized"
	meetingFinalizationDegraded  = "degraded"

	meetingFinalizationStagePending  = "pending"
	meetingFinalizationStageComplete = "complete"
	meetingFinalizationStageDegraded = "degraded"

	meetingFinalizationStageBrain                = "brain"
	meetingFinalizationStageDigest               = "digest"
	meetingFinalizationStageActions              = "actions"
	meetingFinalizationSourceDigestMetadataKey   = "finalizationSourceDigest"
	meetingFinalizationOutputRevisionMetadataKey = "finalizationOutputRevision"
	meetingFinalizationOutputDigestMetadataKey   = "finalizationOutputBodyDigest"

	meetingFinalizationCoreTimeout      = 3 * time.Minute
	meetingFinalizationMaxPasses        = 128
	meetingFinalizationRetryBase        = 5 * time.Second
	meetingFinalizationRetryMax         = 5 * time.Minute
	meetingFinalizationRetryExponentCap = 7
)

type meetingFinalizationSourceDigestContextKey struct{}
type meetingFinalizationMeetingIDContextKey struct{}

func meetingFinalizationSourceDigestFromContext(ctx context.Context) string {
	value, _ := ctx.Value(meetingFinalizationSourceDigestContextKey{}).(string)
	return strings.TrimSpace(value)
}

func meetingFinalizationMeetingIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(meetingFinalizationMeetingIDContextKey{}).(string)
	return strings.TrimSpace(value)
}

func withMeetingFinalizationContext(ctx context.Context, meetingID string, source meetingFinalizationSourceHighWater) context.Context {
	ctx = context.WithValue(ctx, meetingFinalizationSourceDigestContextKey{}, source.ManifestDigest)
	return context.WithValue(ctx, meetingFinalizationMeetingIDContextKey{}, strings.TrimSpace(meetingID))
}

type meetingFinalizationSourceHighWater struct {
	TranscriptID    string `json:"transcriptId,omitempty"`
	CaptureSequence uint64 `json:"captureSequence,omitempty"`
	TranscriptCount int    `json:"transcriptCount"`
	CapturedAt      string `json:"capturedAt,omitempty"`
	ManifestDigest  string `json:"manifestDigest"`
}

type meetingFinalizationStageReceipt struct {
	State            string `json:"state"`
	OutputID         string `json:"outputId,omitempty"`
	OutputBodyDigest string `json:"outputBodyDigest,omitempty"`
	OutputRevision   int    `json:"outputRevision,omitempty"`
	CompletedAt      string `json:"completedAt,omitempty"`
	ErrorCode        string `json:"errorCode,omitempty"`
	ItemCount        int    `json:"itemCount,omitempty"`
	Disposition      string `json:"disposition,omitempty"`
}

type meetingFinalizationReceipt struct {
	State  string                             `json:"state"`
	Source meetingFinalizationSourceHighWater `json:"source"`
	// ObservedRevision advances for every accepted transcript mutation,
	// including a same-id/same-sequence correction. SourceObservedRevision is
	// the exact observation generation whose memory snapshot Source describes.
	// The pair closes the ABA hole that a sequence/id-only watermark cannot see.
	ObservedRevision        uint64                          `json:"observedRevision,omitempty"`
	SourceObservedRevision  uint64                          `json:"sourceObservedRevision,omitempty"`
	ObservedCaptureSequence uint64                          `json:"observedCaptureSequence,omitempty"`
	ObservedTranscriptID    string                          `json:"observedTranscriptId,omitempty"`
	Brain                   meetingFinalizationStageReceipt `json:"brain"`
	Digest                  meetingFinalizationStageReceipt `json:"digest"`
	Actions                 meetingFinalizationStageReceipt `json:"actions"`
	StartedAt               string                          `json:"startedAt"`
	UpdatedAt               string                          `json:"updatedAt"`
	FinalizedAt             string                          `json:"finalizedAt,omitempty"`
	DegradedAt              string                          `json:"degradedAt,omitempty"`
	ArchiveSyncedAt         string                          `json:"archiveSyncedAt,omitempty"`
	LastError               string                          `json:"lastError,omitempty"`
	RetryAttempt            int                             `json:"retryAttempt,omitempty"`
	RetryAfter              string                          `json:"retryAfter,omitempty"`
}

type meetingFinalizationOutputBinding struct {
	BodyDigest string
	Revision   int
}

func meetingFinalizationOutputEntry(entry meetingMemoryEntry) bool {
	return (entry.Kind == meetingMemoryKindBrain || entry.Kind == meetingMemoryKindMeetingDigest) &&
		strings.TrimSpace(entry.Metadata[meetingFinalizationSourceDigestMetadataKey]) != "" &&
		strings.TrimSpace(entry.Metadata["meetingId"]) != ""
}

func meetingFinalizationOutputRevision(entry meetingMemoryEntry) (int, bool) {
	if entry.Metadata == nil {
		return 0, false
	}
	raw, present := entry.Metadata[meetingFinalizationOutputRevisionMetadataKey]
	if !present || strings.TrimSpace(raw) == "" {
		return 0, false
	}
	revision, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || revision <= 0 {
		return 0, false
	}
	return revision, true
}

func meetingFinalizationOutputBindingForEntry(entry meetingMemoryEntry) meetingFinalizationOutputBinding {
	digest := strings.TrimSpace(entry.BodyDigest)
	if digest == "" {
		digest = sha256Hex([]byte(entry.Text))
	}
	revision, _ := meetingFinalizationOutputRevision(entry)
	return meetingFinalizationOutputBinding{BodyDigest: digest, Revision: revision}
}

func meetingFinalizationOutputEntryIntact(entry meetingMemoryEntry) bool {
	revision, revisionOK := meetingFinalizationOutputRevision(entry)
	if !meetingFinalizationOutputEntry(entry) || !revisionOK || revision != 1 {
		return false
	}
	actual := sha256Hex([]byte(entry.Text))
	return actual != "" && strings.TrimSpace(entry.Metadata[meetingFinalizationOutputDigestMetadataKey]) == actual
}

var ErrMeetingFinalizationSourceAdvanced = errors.New("meeting finalization source advanced")

func cloneMeetingFinalizationReceipt(receipt meetingFinalizationReceipt) meetingFinalizationReceipt {
	return receipt
}

func meetingFinalizationReceiptReady(receipt *meetingFinalizationReceipt) bool {
	return receipt != nil && receipt.State == meetingFinalizationFinalized && strings.TrimSpace(receipt.FinalizedAt) != "" &&
		receipt.Brain.State == meetingFinalizationStageComplete && receipt.Digest.State == meetingFinalizationStageComplete && receipt.Actions.State == meetingFinalizationStageComplete
}

func newMeetingFinalizationReceipt(source meetingFinalizationSourceHighWater, now time.Time) meetingFinalizationReceipt {
	stamp := now.UTC().Format(time.RFC3339Nano)
	return meetingFinalizationReceipt{
		State: meetingFinalizationClosing, Source: source,
		ObservedCaptureSequence: source.CaptureSequence, ObservedTranscriptID: source.TranscriptID,
		StartedAt: stamp, UpdatedAt: stamp,
		Brain:   meetingFinalizationStageReceipt{State: meetingFinalizationStagePending},
		Digest:  meetingFinalizationStageReceipt{State: meetingFinalizationStagePending},
		Actions: meetingFinalizationStageReceipt{State: meetingFinalizationStagePending},
	}
}

func (source meetingFinalizationSourceHighWater) equal(other meetingFinalizationSourceHighWater) bool {
	return source == other
}

func (store *meetingStore) finalizationObservedRevision(id string) (uint64, bool) {
	if store == nil || strings.TrimSpace(id) == "" {
		return 0, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.recordIndexes == nil {
		store.rebuildDirectoryCursorIndexesLocked()
	}
	index, ok := store.recordIndexes[strings.TrimSpace(id)]
	if !ok || index < 0 || index >= len(store.records) || store.records[index].Finalization == nil {
		return 0, false
	}
	return store.records[index].Finalization.ObservedRevision, true
}

func (store *meetingStore) beginFinalizationAtRevision(id string, source meetingFinalizationSourceHighWater, observedRevision uint64, forceRepair bool, now time.Time) (meetingRecord, error) {
	if store == nil || strings.TrimSpace(id) == "" {
		return meetingRecord{}, ErrMeetingRecordStore
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.recordIndexes == nil {
		store.rebuildDirectoryCursorIndexesLocked()
	}
	index, ok := store.recordIndexes[strings.TrimSpace(id)]
	if !ok || index < 0 || index >= len(store.records) {
		return meetingRecord{}, fmt.Errorf("%w: meeting %s is absent", ErrMeetingRecordStore, id)
	}
	prior := cloneMeetingRecord(store.records[index])
	receipt := store.records[index].Finalization
	if receipt != nil && receipt.ObservedRevision != observedRevision {
		return cloneMeetingRecord(store.records[index]), ErrMeetingFinalizationSourceAdvanced
	}
	if receipt == nil || !receipt.Source.equal(source) {
		created := newMeetingFinalizationReceipt(source, now)
		created.SourceObservedRevision = observedRevision
		created.ObservedRevision = observedRevision
		if receipt != nil {
			created.ObservedRevision = receipt.ObservedRevision
			if receipt.ObservedCaptureSequence > created.ObservedCaptureSequence {
				created.ObservedCaptureSequence = receipt.ObservedCaptureSequence
				created.ObservedTranscriptID = receipt.ObservedTranscriptID
			}
		}
		store.records[index].Finalization = &created
	} else if forceRepair || receipt.SourceObservedRevision != observedRevision || receipt.State == meetingFinalizationDegraded || (receipt.State == meetingFinalizationFinalized && !meetingFinalizationReceiptReady(receipt)) || (receipt.State != meetingFinalizationClosing && receipt.State != meetingFinalizationFinalized) {
		receipt.State = meetingFinalizationClosing
		receipt.SourceObservedRevision = observedRevision
		receipt.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
		receipt.DegradedAt = ""
		receipt.LastError = ""
		receipt.RetryAfter = ""
		if forceRepair {
			receipt.RetryAttempt = 0
		}
		receipt.ArchiveSyncedAt = ""
		for _, stage := range []*meetingFinalizationStageReceipt{&receipt.Brain, &receipt.Digest, &receipt.Actions} {
			if forceRepair || stage.State == meetingFinalizationStageDegraded {
				*stage = meetingFinalizationStageReceipt{State: meetingFinalizationStagePending}
			}
		}
	}
	if err := store.persistLocked(); err != nil {
		store.resolvePersistFailureLocked(err, func() { store.records[index] = prior })
		return cloneMeetingRecord(store.records[index]), fmt.Errorf("%w: %v", ErrMeetingRecordStore, err)
	}
	return cloneMeetingRecord(store.records[index]), nil
}

// observeEndedTranscript is the write-ahead half of every accepted transcript
// mutation: append, correction, or deletion. Advancing the mutation revision
// and reopening the receipt happen before memory bytes change; persistence or
// archive-downgrade failure therefore rejects the source mutation. The capture
// watermark remains monotonic diagnostic history even when deletion lowers the
// current source high-water. A boot audit repairs the narrow cross-file crash
// seam without replaying already-finalized historical meetings.
func (store *meetingStore) observeEndedTranscript(meetingID, transcriptID string, captureSequence uint64, now time.Time) (meetingRecord, bool, error) {
	if store == nil || strings.TrimSpace(meetingID) == "" {
		return meetingRecord{}, false, ErrMeetingRecordStore
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.recordIndexes == nil {
		store.rebuildDirectoryCursorIndexesLocked()
	}
	index, ok := store.recordIndexes[strings.TrimSpace(meetingID)]
	if !ok || index < 0 || index >= len(store.records) || store.records[index].EndedAt == "" {
		return meetingRecord{}, false, nil
	}
	prior := cloneMeetingRecord(store.records[index])
	receipt := store.records[index].Finalization
	if receipt == nil {
		created := newMeetingFinalizationReceipt(meetingFinalizationSourceHighWater{}, now)
		receipt = &created
		store.records[index].Finalization = receipt
	}
	receipt.ObservedRevision++
	if captureSequence >= receipt.ObservedCaptureSequence {
		receipt.ObservedCaptureSequence = captureSequence
		receipt.ObservedTranscriptID = strings.TrimSpace(transcriptID)
	}
	receipt.State = meetingFinalizationClosing
	receipt.FinalizedAt = ""
	receipt.DegradedAt = ""
	receipt.ArchiveSyncedAt = ""
	receipt.LastError = ""
	receipt.RetryAttempt = 0
	receipt.RetryAfter = ""
	receipt.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	if err := store.persistLocked(); err != nil {
		store.resolvePersistFailureLocked(err, func() { store.records[index] = prior })
		return cloneMeetingRecord(store.records[index]), false, fmt.Errorf("%w: %v", ErrMeetingRecordStore, err)
	}
	return cloneMeetingRecord(store.records[index]), true, nil
}

func (store *meetingStore) markFinalizationArchiveSynced(id, archiveID string, expected meetingFinalizationReceipt, now time.Time) (meetingRecord, error) {
	if store == nil || strings.TrimSpace(id) == "" || strings.TrimSpace(archiveID) == "" {
		return meetingRecord{}, ErrMeetingRecordStore
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.recordIndexes == nil {
		store.rebuildDirectoryCursorIndexesLocked()
	}
	index, ok := store.recordIndexes[strings.TrimSpace(id)]
	if !ok || index < 0 || index >= len(store.records) || store.records[index].Finalization == nil || store.records[index].ArchiveID != strings.TrimSpace(archiveID) {
		return meetingRecord{}, fmt.Errorf("%w: archive finalization target is absent", ErrMeetingRecordStore)
	}
	receipt := store.records[index].Finalization
	if receipt.State != meetingFinalizationFinalized && receipt.State != meetingFinalizationDegraded {
		return cloneMeetingRecord(store.records[index]), fmt.Errorf("archive finalization is not settled")
	}
	if receipt.State != expected.State || !receipt.Source.equal(expected.Source) || receipt.UpdatedAt != expected.UpdatedAt || receipt.FinalizedAt != expected.FinalizedAt || receipt.DegradedAt != expected.DegradedAt {
		return cloneMeetingRecord(store.records[index]), ErrMeetingFinalizationSourceAdvanced
	}
	prior := cloneMeetingRecord(store.records[index])
	receipt.ArchiveSyncedAt = now.UTC().Format(time.RFC3339Nano)
	receipt.UpdatedAt = receipt.ArchiveSyncedAt
	if err := store.persistLocked(); err != nil {
		store.resolvePersistFailureLocked(err, func() { store.records[index] = prior })
		return cloneMeetingRecord(store.records[index]), fmt.Errorf("%w: %v", ErrMeetingRecordStore, err)
	}
	return cloneMeetingRecord(store.records[index]), nil
}

// endMeetingWithFinalizationIfIdleGeneration atomically stamps EndedAt and the
// closing receipt under the same generation fence. A crash can therefore leave
// closing work to resume, never a bare ended record that looks complete.
func (store *meetingStore) endMeetingWithFinalizationIfIdleGeneration(roomID, id string, endedAt time.Time, generation uint64, source meetingFinalizationSourceHighWater) (meetingRecord, bool, error) {
	if store == nil || strings.TrimSpace(id) == "" {
		return meetingRecord{}, false, ErrMeetingRecordStore
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if generation != store.idleGenerations[normalizeRoomID(roomID)] {
		return meetingRecord{}, false, nil
	}
	return store.endMeetingWithFinalizationLocked(id, endedAt, meetingEndedReasonIdle, "", source)
}

func (store *meetingStore) endMeetingWithFinalization(id string, endedAt time.Time, reason, archiveID string, source meetingFinalizationSourceHighWater) (meetingRecord, bool, error) {
	if store == nil || strings.TrimSpace(id) == "" {
		return meetingRecord{}, false, ErrMeetingRecordStore
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.endMeetingWithFinalizationLocked(id, endedAt, reason, archiveID, source)
}

// rolloverMeetingWithFinalization atomically closes one occupied sitting and
// opens its anchored successor in a single meetings.json replacement. The
// archive path stages successor admission anchors first; until this write
// lands, the predecessor remains the only open record. Ambiguous durable
// replacement is resolved by reloading and recognizing the exact committed
// pair rather than rolling memory/media back across an already-landed seam.
func (store *meetingStore) rolloverMeetingWithFinalization(roomID, predecessorID string, endedAt time.Time, archiveID string, source meetingFinalizationSourceHighWater, successorID string, successorStartedAt time.Time, participants []string) (meetingRecord, meetingRecord, error) {
	predecessorID = strings.TrimSpace(predecessorID)
	successorID = strings.TrimSpace(successorID)
	roomID = normalizeRoomID(roomID)
	if store == nil || predecessorID == "" || successorID == "" || predecessorID == successorID || endedAt.IsZero() || successorStartedAt.IsZero() {
		return meetingRecord{}, meetingRecord{}, ErrMeetingRecordStore
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.recordIndexes == nil {
		store.rebuildDirectoryCursorIndexesLocked()
	}
	predecessorIndex, found := store.recordIndexes[predecessorID]
	if !found || predecessorIndex < 0 || predecessorIndex >= len(store.records) || store.records[predecessorIndex].EndedAt != "" || meetingRoomID(store.records[predecessorIndex]) != roomID {
		return meetingRecord{}, meetingRecord{}, fmt.Errorf("%w: active rollover predecessor is absent", ErrMeetingRecordStore)
	}
	if _, exists := store.recordIndexes[successorID]; exists {
		return meetingRecord{}, meetingRecord{}, fmt.Errorf("%w: rollover successor already exists", ErrMeetingRecordStore)
	}
	priorRecords := make([]meetingRecord, len(store.records))
	for index := range store.records {
		priorRecords[index] = cloneMeetingRecord(store.records[index])
	}
	predecessor := &store.records[predecessorIndex]
	predecessor.EndedAt = endedAt.UTC().Format(time.RFC3339Nano)
	predecessor.EndedReason = meetingEndedReasonArchive
	predecessor.ArchiveID = strings.TrimSpace(archiveID)
	predecessor.IdleDeadlineAt = ""
	receipt := newMeetingFinalizationReceipt(source, endedAt)
	predecessor.Finalization = &receipt
	union, _ := unionMeetingParticipants(nil, participants)
	successor := meetingRecord{
		ID:           successorID,
		RoomID:       storedMeetingRoomID(roomID),
		ListenOnly:   predecessor.ListenOnly,
		StartedAt:    successorStartedAt.UTC().Format(time.RFC3339Nano),
		Participants: union,
	}
	store.records = append(store.records, successor)
	store.rebuildDirectoryCursorIndexesLocked()
	if err := store.persistLocked(); err != nil {
		store.resolvePersistFailureLocked(err, func() { store.records = priorRecords })
		// An atomic rename may have landed even when directory sync was
		// indeterminate. Treat only the exact pair as committed.
		predecessorIndex, predecessorFound := store.recordIndexes[predecessorID]
		successorIndex, successorFound := store.recordIndexes[successorID]
		if predecessorFound && successorFound && predecessorIndex >= 0 && successorIndex >= 0 && predecessorIndex < len(store.records) && successorIndex < len(store.records) {
			persistedPredecessor := store.records[predecessorIndex]
			persistedSuccessor := store.records[successorIndex]
			if persistedPredecessor.EndedReason == meetingEndedReasonArchive && persistedPredecessor.ArchiveID == strings.TrimSpace(archiveID) && persistedPredecessor.EndedAt != "" && persistedSuccessor.EndedAt == "" && meetingRoomID(persistedSuccessor) == roomID {
				return cloneMeetingRecord(persistedPredecessor), cloneMeetingRecord(persistedSuccessor), nil
			}
		}
		return meetingRecord{}, meetingRecord{}, fmt.Errorf("%w: %v", ErrMeetingRecordStore, err)
	}
	return cloneMeetingRecord(*predecessor), cloneMeetingRecord(successor), nil
}

func (store *meetingStore) endMeetingWithFinalizationLocked(id string, endedAt time.Time, reason, archiveID string, source meetingFinalizationSourceHighWater) (meetingRecord, bool, error) {
	id = strings.TrimSpace(id)
	reason = strings.TrimSpace(reason)
	archiveID = strings.TrimSpace(archiveID)
	for index := len(store.records) - 1; index >= 0; index-- {
		if store.records[index].ID != id {
			continue
		}
		if store.records[index].EndedAt != "" {
			return cloneMeetingRecord(store.records[index]), false, nil
		}
		prior := cloneMeetingRecord(store.records[index])
		store.records[index].EndedAt = endedAt.UTC().Format(time.RFC3339Nano)
		store.records[index].EndedReason = reason
		store.records[index].ArchiveID = archiveID
		store.records[index].IdleDeadlineAt = ""
		receipt := newMeetingFinalizationReceipt(source, endedAt)
		store.records[index].Finalization = &receipt
		if err := store.persistLocked(); err != nil {
			store.resolvePersistFailureLocked(err, func() { store.records[index] = prior })
			// Atomic replacement can commit the close and still report an
			// indeterminate parent-directory fsync. Reconcile the visible
			// generation and recognize only the exact intended close. Returning
			// changed=true lets the caller run the idempotent close side effects;
			// treating it as failure would strand a durably ended sitting.
			if resolvedIndex, found := store.recordIndexes[id]; found && resolvedIndex >= 0 && resolvedIndex < len(store.records) {
				resolved := store.records[resolvedIndex]
				if resolved.EndedAt == endedAt.UTC().Format(time.RFC3339Nano) && resolved.EndedReason == reason && resolved.ArchiveID == archiveID && resolved.IdleDeadlineAt == "" && resolved.Finalization != nil && resolved.Finalization.State == meetingFinalizationClosing && resolved.Finalization.Source.equal(source) {
					return cloneMeetingRecord(resolved), true, nil
				}
				return cloneMeetingRecord(resolved), false, fmt.Errorf("%w: %v", ErrMeetingRecordStore, err)
			}
			return meetingRecord{}, false, fmt.Errorf("%w: %v", ErrMeetingRecordStore, err)
		}
		return cloneMeetingRecord(store.records[index]), true, nil
	}
	return meetingRecord{}, false, fmt.Errorf("%w: meeting %s is absent", ErrMeetingRecordStore, id)
}

func (store *meetingStore) markFinalizationStage(id, stageName, state, outputID, errorCode, disposition string, itemCount int, now time.Time, outputBinding ...meetingFinalizationOutputBinding) (meetingRecord, error) {
	if store == nil || strings.TrimSpace(id) == "" {
		return meetingRecord{}, ErrMeetingRecordStore
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.recordIndexes == nil {
		store.rebuildDirectoryCursorIndexesLocked()
	}
	index, ok := store.recordIndexes[strings.TrimSpace(id)]
	if !ok || index < 0 || index >= len(store.records) || store.records[index].Finalization == nil {
		return meetingRecord{}, fmt.Errorf("%w: finalization receipt for %s is absent", ErrMeetingRecordStore, id)
	}
	prior := cloneMeetingRecord(store.records[index])
	receipt := store.records[index].Finalization
	var stage *meetingFinalizationStageReceipt
	switch stageName {
	case meetingFinalizationStageBrain:
		stage = &receipt.Brain
	case meetingFinalizationStageDigest:
		stage = &receipt.Digest
	case meetingFinalizationStageActions:
		stage = &receipt.Actions
	default:
		return meetingRecord{}, fmt.Errorf("unknown finalization stage %q", stageName)
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	*stage = meetingFinalizationStageReceipt{State: state, OutputID: strings.TrimSpace(outputID), ErrorCode: strings.TrimSpace(errorCode), ItemCount: itemCount, Disposition: strings.TrimSpace(disposition)}
	if len(outputBinding) > 0 {
		stage.OutputBodyDigest = strings.TrimSpace(outputBinding[0].BodyDigest)
		stage.OutputRevision = outputBinding[0].Revision
	}
	if state == meetingFinalizationStageComplete {
		stage.CompletedAt = stamp
	}
	receipt.UpdatedAt = stamp
	if state == meetingFinalizationStageDegraded {
		receipt.State = meetingFinalizationDegraded
		receipt.DegradedAt = stamp
		receipt.ArchiveSyncedAt = ""
		receipt.LastError = firstNonEmptyString(strings.TrimSpace(errorCode), "core_stage_failed")
		if meetingFinalizationRetryableCode(receipt.LastError) {
			if receipt.RetryAttempt < meetingFinalizationRetryExponentCap {
				receipt.RetryAttempt++
			}
			receipt.RetryAfter = now.UTC().Add(meetingFinalizationRetryDuration(receipt.RetryAttempt)).Format(time.RFC3339Nano)
		} else {
			receipt.RetryAfter = ""
		}
	}
	if err := store.persistLocked(); err != nil {
		store.resolvePersistFailureLocked(err, func() { store.records[index] = prior })
		return cloneMeetingRecord(store.records[index]), fmt.Errorf("%w: %v", ErrMeetingRecordStore, err)
	}
	return cloneMeetingRecord(store.records[index]), nil
}

func (store *meetingStore) markFinalizationComplete(id string, source meetingFinalizationSourceHighWater, now time.Time) (meetingRecord, error) {
	if store == nil || strings.TrimSpace(id) == "" {
		return meetingRecord{}, ErrMeetingRecordStore
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.recordIndexes == nil {
		store.rebuildDirectoryCursorIndexesLocked()
	}
	index, ok := store.recordIndexes[strings.TrimSpace(id)]
	if !ok || index < 0 || index >= len(store.records) || store.records[index].Finalization == nil {
		return meetingRecord{}, fmt.Errorf("%w: finalization receipt for %s is absent", ErrMeetingRecordStore, id)
	}
	prior := cloneMeetingRecord(store.records[index])
	receipt := store.records[index].Finalization
	if !receipt.Source.equal(source) || receipt.Brain.State != meetingFinalizationStageComplete || receipt.Digest.State != meetingFinalizationStageComplete || receipt.Actions.State != meetingFinalizationStageComplete {
		return cloneMeetingRecord(store.records[index]), fmt.Errorf("meeting finalization stages are incomplete")
	}
	// ObservedRevision is the authoritative mutation generation. Capture
	// sequence/id remain monotonic diagnostics, so deletion of the former high
	// water can legitimately leave them above the new source high-water.
	if receipt.ObservedRevision != receipt.SourceObservedRevision {
		return cloneMeetingRecord(store.records[index]), ErrMeetingFinalizationSourceAdvanced
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	receipt.State = meetingFinalizationFinalized
	receipt.UpdatedAt = stamp
	receipt.FinalizedAt = stamp
	receipt.DegradedAt = ""
	receipt.ArchiveSyncedAt = ""
	receipt.LastError = ""
	receipt.RetryAttempt = 0
	receipt.RetryAfter = ""
	if err := store.persistLocked(); err != nil {
		store.resolvePersistFailureLocked(err, func() { store.records[index] = prior })
		return cloneMeetingRecord(store.records[index]), fmt.Errorf("%w: %v", ErrMeetingRecordStore, err)
	}
	return cloneMeetingRecord(store.records[index]), nil
}

func (store *meetingStore) recordsNeedingFinalization() []meetingRecord {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	records := make([]meetingRecord, 0)
	for _, record := range store.records {
		if record.EndedAt == "" || record.Finalization == nil || (record.Finalization.State != meetingFinalizationClosing && record.Finalization.State != meetingFinalizationDegraded) {
			continue
		}
		records = append(records, cloneMeetingRecord(record))
	}
	sort.Slice(records, func(i, j int) bool { return records[i].EndedAt < records[j].EndedAt })
	return records
}

func (store *meetingStore) recordsWithFinalization() []meetingRecord {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	records := make([]meetingRecord, 0)
	for _, record := range store.records {
		if record.EndedAt == "" || record.Finalization == nil {
			continue
		}
		records = append(records, cloneMeetingRecord(record))
	}
	sort.Slice(records, func(i, j int) bool { return records[i].EndedAt < records[j].EndedAt })
	return records
}

func (app *kanbanBoardApp) meetingFinalizationSource(meetingID string) meetingFinalizationSourceHighWater {
	entries := app.memorySnapshotForMeeting(strings.TrimSpace(meetingID), 0)
	type orderedTranscript struct {
		entry    meetingMemoryEntry
		sequence uint64
	}
	transcripts := make([]orderedTranscript, 0)
	for _, entry := range entries {
		if entry.Kind != meetingMemoryKindTranscript || strings.TrimSpace(entry.Metadata["meetingId"]) != strings.TrimSpace(meetingID) {
			continue
		}
		sequence, _ := entryCaptureSequence(entry)
		transcripts = append(transcripts, orderedTranscript{entry: entry, sequence: sequence})
	}
	// The file is append-only in normal operation, but recovery must not make a
	// different receipt merely because entries were replayed in a different
	// physical order. Capture sequence is authoritative; legacy sequence-less
	// rows fall back to timestamp then durable id.
	sort.Slice(transcripts, func(i, j int) bool {
		if transcripts[i].sequence != transcripts[j].sequence {
			return transcripts[i].sequence < transcripts[j].sequence
		}
		if !transcripts[i].entry.CreatedAt.Equal(transcripts[j].entry.CreatedAt) {
			return transcripts[i].entry.CreatedAt.Before(transcripts[j].entry.CreatedAt)
		}
		return transcripts[i].entry.ID < transcripts[j].entry.ID
	})
	manifest := make([]string, 0, len(transcripts))
	for _, transcript := range transcripts {
		metadata, _ := json.Marshal(transcript.entry.Metadata)
		manifest = append(manifest, strings.Join([]string{
			transcript.entry.ID,
			strconv.FormatUint(transcript.sequence, 10),
			transcript.entry.CreatedAt.UTC().Format(time.RFC3339Nano),
			transcript.entry.BodyDigest,
			sha256Hex(metadata),
		}, "\x00"))
	}
	result := meetingFinalizationSourceHighWater{TranscriptCount: len(transcripts), ManifestDigest: sha256Hex([]byte(strings.Join(manifest, "\n")))}
	if len(transcripts) > 0 {
		latest := transcripts[len(transcripts)-1]
		result.TranscriptID = latest.entry.ID
		result.CaptureSequence = latest.sequence
		result.CapturedAt = latest.entry.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	return result
}

// meetingFinalizationSourceObservation binds a memory snapshot to the durable
// transcript-observation generation read immediately before it. The later
// beginFinalizationAtRevision CAS rejects the snapshot if an append or same-id
// correction crossed this read.
func (app *kanbanBoardApp) meetingFinalizationSourceObservation(meetingID string) (meetingFinalizationSourceHighWater, uint64) {
	revision, _ := app.meetings.finalizationObservedRevision(meetingID)
	return app.meetingFinalizationSource(meetingID), revision
}

// meetingFinalizationOutputsReady validates that a finalized receipt still
// points at the durable, source-stamped core outputs it claims. This prevents a
// missing entry or a superseded digest from turning a structurally complete
// receipt into false analysisReady truth after restart.
func (app *kanbanBoardApp) meetingFinalizationOutputsReady(record meetingRecord) bool {
	if app == nil || app.memory == nil || record.Finalization == nil || !meetingFinalizationReceiptReady(record.Finalization) {
		return false
	}
	receipt := record.Finalization
	if receipt.Source.TranscriptCount == 0 {
		return receipt.Brain.OutputID == "" && receipt.Digest.OutputID == "" && receipt.Actions.OutputID == "" &&
			receipt.Brain.Disposition == "no_source" && receipt.Digest.Disposition == "no_source" && receipt.Actions.Disposition == "no_source"
	}
	entries := app.memory.snapshotForMeeting(record.ID, 0)
	var brain, digest meetingMemoryEntry
	foundBrain, foundDigest := false, false
	for _, entry := range entries {
		switch {
		case entry.Kind == meetingMemoryKindBrain && entry.ID == receipt.Brain.OutputID:
			brain, foundBrain = entry, true
		case entry.Kind == meetingMemoryKindMeetingDigest && entry.ID == receipt.Digest.OutputID && digestEntryCurrent(entry):
			digest, foundDigest = entry, true
		}
	}
	if !foundBrain || strings.TrimSpace(brain.Metadata["meetingId"]) != record.ID || strings.TrimSpace(brain.Metadata[meetingFinalizationSourceDigestMetadataKey]) != receipt.Source.ManifestDigest ||
		!meetingFinalizationStageOutputMatches(receipt.Brain, brain) {
		return false
	}
	if !foundDigest || digest.ID != receipt.Digest.OutputID || strings.TrimSpace(digest.Metadata[meetingFinalizationSourceDigestMetadataKey]) != receipt.Source.ManifestDigest ||
		!meetingFinalizationStageOutputMatches(receipt.Digest, digest) {
		return false
	}
	if receipt.Actions.OutputID != digest.ID || receipt.Actions.Disposition != "digest_action_items" || !meetingFinalizationStageOutputMatches(receipt.Actions, digest) {
		return false
	}
	_, valid := parseMeetingDigest(digest.Text)
	return valid
}

func meetingFinalizationStageOutputMatches(stage meetingFinalizationStageReceipt, entry meetingMemoryEntry) bool {
	if !meetingFinalizationOutputEntryIntact(entry) {
		return false
	}
	binding := meetingFinalizationOutputBindingForEntry(entry)
	return strings.TrimSpace(stage.OutputBodyDigest) != "" && stage.OutputBodyDigest == binding.BodyDigest &&
		stage.OutputRevision == 1 && binding.Revision == 1
}

func finalizationEntryCursorIndex(entries []meetingMemoryEntry, kind, cursorKey, cursorID string) int {
	cursorID = strings.TrimSpace(cursorID)
	if cursorID == "" {
		return -1
	}
	for index, entry := range entries {
		if entry.Kind == kind && entry.ID == cursorID {
			return index
		}
	}
	return -1
}

func meetingFinalizationEntriesOfKind(entries []meetingMemoryEntry, kind string) []meetingMemoryEntry {
	result := make([]meetingMemoryEntry, 0)
	for _, entry := range entries {
		if entry.Kind == kind {
			result = append(result, entry)
		}
	}
	return result
}

func meetingFinalizationOrderedTranscripts(entries []meetingMemoryEntry) []meetingMemoryEntry {
	transcripts := meetingFinalizationEntriesOfKind(entries, meetingMemoryKindTranscript)
	sort.Slice(transcripts, func(i, j int) bool {
		leftSequence, _ := entryCaptureSequence(transcripts[i])
		rightSequence, _ := entryCaptureSequence(transcripts[j])
		if leftSequence != rightSequence {
			return leftSequence < rightSequence
		}
		if !transcripts[i].CreatedAt.Equal(transcripts[j].CreatedAt) {
			return transcripts[i].CreatedAt.Before(transcripts[j].CreatedAt)
		}
		return transcripts[i].ID < transcripts[j].ID
	})
	return transcripts
}

func meetingFinalizationBrainProgress(entries []meetingMemoryEntry, source meetingFinalizationSourceHighWater, limit int) (meetingMemoryEntry, []meetingMemoryEntry, bool) {
	transcripts := meetingFinalizationOrderedTranscripts(entries)
	if source.TranscriptCount == 0 || len(transcripts) == 0 {
		return meetingMemoryEntry{}, nil, true
	}
	target := finalizationEntryCursorIndex(transcripts, meetingMemoryKindTranscript, "", source.TranscriptID)
	if target < 0 {
		target = len(transcripts) - 1
	}
	bestCursor := -1
	var best meetingMemoryEntry
	for _, entry := range meetingFinalizationEntriesOfKind(entries, meetingMemoryKindBrain) {
		if strings.TrimSpace(entry.Metadata[meetingFinalizationSourceDigestMetadataKey]) != source.ManifestDigest {
			continue
		}
		if !meetingFinalizationOutputEntryIntact(entry) {
			continue
		}
		cursor := finalizationEntryCursorIndex(transcripts, meetingMemoryKindTranscript, "", entry.Metadata[meetingBrainCursorMetadataKey])
		if cursor > bestCursor {
			bestCursor, best = cursor, entry
		}
	}
	if bestCursor >= target {
		return best, nil, true
	}
	start := bestCursor + 1
	end := target + 1
	if limit > 0 && end-start > limit {
		end = start + limit
	}
	return best, append([]meetingMemoryEntry(nil), transcripts[start:end]...), false
}

func meetingFinalizationDigestProgress(entries []meetingMemoryEntry, source meetingFinalizationSourceHighWater, digest meetingMemoryEntry, hasDigest bool, limit int) (meetingMemoryEntry, []meetingMemoryEntry, bool) {
	brains := make([]meetingMemoryEntry, 0)
	for _, brain := range meetingFinalizationEntriesOfKind(entries, meetingMemoryKindBrain) {
		if strings.TrimSpace(brain.Metadata[meetingFinalizationSourceDigestMetadataKey]) == source.ManifestDigest && meetingFinalizationOutputEntryIntact(brain) {
			brains = append(brains, brain)
		}
	}
	if hasDigest && (strings.TrimSpace(digest.Metadata[meetingFinalizationSourceDigestMetadataKey]) != source.ManifestDigest || !meetingFinalizationOutputEntryIntact(digest)) {
		digest = meetingMemoryEntry{}
		hasDigest = false
	}
	if source.TranscriptCount == 0 || len(brains) == 0 {
		return digest, nil, true
	}
	target := len(brains) - 1
	if hasDigest {
		if highWater, err := strconv.ParseUint(strings.TrimSpace(digest.Metadata[meetingDigestCaptureMetadataKey]), 10, 64); err == nil && source.CaptureSequence > 0 && highWater >= source.CaptureSequence && finalizationEntryCursorIndex(brains, meetingMemoryKindBrain, "", digest.Metadata[meetingDigestCursorMetadataKey]) >= target {
			return digest, nil, true
		}
	}
	cursor := -1
	if hasDigest {
		cursor = finalizationEntryCursorIndex(brains, meetingMemoryKindBrain, "", digest.Metadata[meetingDigestCursorMetadataKey])
	}
	if cursor >= target {
		return digest, nil, true
	}
	start := cursor + 1
	end := target + 1
	if limit > 0 && end-start > limit {
		end = start + limit
	}
	return digest, append([]meetingMemoryEntry(nil), brains[start:end]...), false
}

func meetingFinalizationErrorCode(err error) string {
	if err == nil {
		return "core_output_unavailable"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "core_timeout"
	}
	if errors.Is(err, ErrMeetingRecordStore) {
		return "receipt_persistence_unavailable"
	}
	if strings.Contains(strings.ToLower(err.Error()), "api key is unavailable") {
		return "configuration_unavailable"
	}
	if isProviderInvocationFailure(err) {
		return "provider_unavailable"
	}
	return "core_stage_failed"
}

func meetingFinalizationRetryableCode(code string) bool {
	switch strings.TrimSpace(code) {
	case "provider_unavailable", "core_timeout", "receipt_persistence_unavailable", "core_output_unavailable", "core_stage_failed":
		return true
	default:
		return false
	}
}

func meetingFinalizationRetryDuration(attempt int) time.Duration {
	base := meetingFinalizationRetryBase
	if raw := strings.TrimSpace(os.Getenv("MEETING_FINALIZATION_RETRY_BASE")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			base = parsed
		}
	}
	maximum := meetingFinalizationRetryMax
	if raw := strings.TrimSpace(os.Getenv("MEETING_FINALIZATION_RETRY_MAX")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed >= base {
			maximum = parsed
		}
	}
	if attempt < 1 {
		attempt = 1
	}
	if attempt > meetingFinalizationRetryExponentCap {
		attempt = meetingFinalizationRetryExponentCap
	}
	delay := base
	for index := 1; index < attempt && delay < maximum; index++ {
		delay *= 2
		if delay > maximum {
			delay = maximum
		}
	}
	return delay
}

func (app *kanbanBoardApp) degradeMeetingFinalization(id, stage string, err error) error {
	if err == nil {
		err = errors.New("core output unavailable")
	}
	code := meetingFinalizationErrorCode(err)
	if _, persistErr := app.meetings.markFinalizationStage(id, stage, meetingFinalizationStageDegraded, "", code, "", 0, time.Now().UTC()); persistErr != nil {
		return errors.Join(err, persistErr)
	}
	return err
}

// finalizeMeetingCore is the restart-safe core close runner. It operates on one
// exact meeting snapshot, not the room's current meeting id, so a degraded old
// close can recover after a successor has started without crossing sittings.
// Durable output discovery precedes every provider call, closing the crash
// window between an output append and its stage receipt.
func (app *kanbanBoardApp) finalizeMeetingCore(ctx context.Context, meetingID string, responder openAITextResponder) (meetingRecord, error) {
	if app == nil || app.memory == nil || app.meetings == nil || strings.TrimSpace(meetingID) == "" {
		return meetingRecord{}, ErrMeetingRecordStore
	}
	meetingID = strings.TrimSpace(meetingID)
	runLock := app.ambientAgentRunLock("meeting-finalization:" + meetingID)
	runLock.Lock()
	defer runLock.Unlock()
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, meetingFinalizationCoreTimeout)
		defer cancel()
	}

	app.mu.Lock()
	apiKey := app.apiKey
	app.mu.Unlock()
	for pass := 0; pass < meetingFinalizationMaxPasses; pass++ {
		source, observedRevision := app.meetingFinalizationSourceObservation(meetingID)
		record, err := app.meetings.beginFinalizationAtRevision(meetingID, source, observedRevision, false, time.Now().UTC())
		if errors.Is(err, ErrMeetingFinalizationSourceAdvanced) {
			continue
		}
		if err != nil {
			return record, err
		}
		if record.Finalization != nil && record.Finalization.State == meetingFinalizationFinalized && record.Finalization.Source.equal(source) {
			if app.meetingFinalizationOutputsReady(record) {
				app.clearFinalizedLiveTemporalBrain(record)
				return record, nil
			}
			record, err = app.meetings.beginFinalizationAtRevision(meetingID, source, observedRevision, true, time.Now().UTC())
			if errors.Is(err, ErrMeetingFinalizationSourceAdvanced) {
				continue
			}
			if err != nil {
				return record, err
			}
		}
		if source.TranscriptCount == 0 {
			for _, stage := range []string{meetingFinalizationStageBrain, meetingFinalizationStageDigest, meetingFinalizationStageActions} {
				if _, err := app.meetings.markFinalizationStage(meetingID, stage, meetingFinalizationStageComplete, "", "", "no_source", 0, time.Now().UTC()); err != nil {
					return meetingRecord{}, err
				}
			}
			finalized, err := app.meetings.markFinalizationComplete(meetingID, source, time.Now().UTC())
			if errors.Is(err, ErrMeetingFinalizationSourceAdvanced) {
				continue
			}
			if err == nil {
				app.clearFinalizedLiveTemporalBrain(finalized)
			}
			return finalized, err
		}
		if strings.TrimSpace(apiKey) == "" {
			err := fmt.Errorf("OpenAI API key is unavailable")
			return record, app.degradeMeetingFinalization(meetingID, meetingFinalizationStageBrain, err)
		}

		entries := app.memorySnapshotForMeeting(meetingID, 0)
		brain, transcriptBatch, brainComplete := meetingFinalizationBrainProgress(entries, source, meetingBrainAgent().maxBatch())
		if !brainComplete {
			if len(transcriptBatch) == 0 {
				err := fmt.Errorf("final transcript window is unavailable")
				return record, app.degradeMeetingFinalization(meetingID, meetingFinalizationStageBrain, err)
			}
			stageLock := app.ambientAgentRunLock("meeting-finalization-stage:" + meetingBrainAgentName + ":" + meetingID)
			stageLock.Lock()
			stageCtx := withMeetingFinalizationContext(ctx, meetingID, source)
			brain, err = app.produceMeetingBrainWriteUp(stageCtx, apiKey, transcriptBatch, firstNonNilResponder(responder))
			stageLock.Unlock()
			if err != nil || brain.ID == "" {
				return record, app.degradeMeetingFinalization(meetingID, meetingFinalizationStageBrain, err)
			}
			continue
		}
		brainBinding := meetingFinalizationOutputBindingForEntry(brain)
		if _, err := app.meetings.markFinalizationStage(meetingID, meetingFinalizationStageBrain, meetingFinalizationStageComplete, brain.ID, "", "", 0, time.Now().UTC(), brainBinding); err != nil {
			return meetingRecord{}, err
		}

		entries = app.memorySnapshotForMeeting(meetingID, 0)
		digest, hasDigest := app.memory.currentDigest(meetingMemoryKindMeetingDigest, meetingID)
		digest, brainBatch, digestComplete := meetingFinalizationDigestProgress(entries, source, digest, hasDigest, meetingDigestAgent().maxBatch())
		if !digestComplete {
			if len(brainBatch) == 0 {
				err := fmt.Errorf("final brain window is unavailable")
				return record, app.degradeMeetingFinalization(meetingID, meetingFinalizationStageDigest, err)
			}
			stageLock := app.ambientAgentRunLock("meeting-finalization-stage:" + meetingDigestAgentName + ":" + meetingID)
			stageLock.Lock()
			stageCtx := withMeetingFinalizationContext(ctx, meetingID, source)
			digest, err = app.produceMeetingDigests(stageCtx, apiKey, brainBatch, firstNonNilResponder(responder))
			stageLock.Unlock()
			if err != nil || digest.ID == "" {
				return record, app.degradeMeetingFinalization(meetingID, meetingFinalizationStageDigest, err)
			}
			continue
		}
		if digest.ID == "" {
			err := fmt.Errorf("final meeting digest is unavailable")
			return record, app.degradeMeetingFinalization(meetingID, meetingFinalizationStageDigest, err)
		}
		digestBinding := meetingFinalizationOutputBindingForEntry(digest)
		if _, err := app.meetings.markFinalizationStage(meetingID, meetingFinalizationStageDigest, meetingFinalizationStageComplete, digest.ID, "", "", 0, time.Now().UTC(), digestBinding); err != nil {
			return meetingRecord{}, err
		}
		payload, ok := parseMeetingDigest(digest.Text)
		if !ok {
			err := fmt.Errorf("final meeting digest is invalid")
			return record, app.degradeMeetingFinalization(meetingID, meetingFinalizationStageActions, err)
		}
		if _, err := app.meetings.markFinalizationStage(meetingID, meetingFinalizationStageActions, meetingFinalizationStageComplete, digest.ID, "", "digest_action_items", len(payload.ActionItems), time.Now().UTC(), digestBinding); err != nil {
			return meetingRecord{}, err
		}
		latestSource, latestObservedRevision := app.meetingFinalizationSourceObservation(meetingID)
		if !latestSource.equal(source) {
			// A finalized transcription callback landed during the close pass. Reset
			// against the stronger source and consume its suffix before claiming final.
			if _, err := app.meetings.beginFinalizationAtRevision(meetingID, latestSource, latestObservedRevision, false, time.Now().UTC()); err != nil && !errors.Is(err, ErrMeetingFinalizationSourceAdvanced) {
				return meetingRecord{}, err
			}
			continue
		}
		finalized, err := app.meetings.markFinalizationComplete(meetingID, source, time.Now().UTC())
		if errors.Is(err, ErrMeetingFinalizationSourceAdvanced) {
			continue
		}
		if err == nil {
			app.clearFinalizedLiveTemporalBrain(finalized)
		}
		return finalized, err
	}
	err := fmt.Errorf("meeting finalization did not reach a stable source high-water")
	return meetingRecord{}, app.degradeMeetingFinalization(meetingID, meetingFinalizationStageBrain, err)
}

func (app *kanbanBoardApp) clearFinalizedLiveTemporalBrain(record meetingRecord) {
	if app == nil || app.strideRuntime == nil || record.ID == "" {
		return
	}
	if err := app.strideRuntime.ClearLiveTemporalMeetingBrain(canonicalTenantID(), meetingRoomID(record), record.ID); err != nil && !errors.Is(err, ErrSTRIDERuntimeDisabled) {
		log.Errorf("Could not clear finalized live temporal brain for meeting %s: %v", record.ID, err)
	}
}

func firstNonNilResponder(responder openAITextResponder) openAITextResponder {
	if responder != nil {
		return responder
	}
	return createOpenAITextResponse
}

func (app *kanbanBoardApp) scheduleMeetingNonCoreRollups(roomID string) {
	for _, agent := range closeNonCoreFlushChain() {
		app.nudgeAmbientAgentForRoom(agent.name, agent.scopeRoomID(roomID))
	}
}

func (app *kanbanBoardApp) refreshMeetingArchiveFinalization(record meetingRecord) {
	if app == nil || app.meetings == nil || strings.TrimSpace(record.ID) == "" {
		return
	}
	current, found := app.meetings.recordByID(record.ID)
	if !found || strings.TrimSpace(current.ArchiveID) == "" || current.Finalization == nil ||
		(current.Finalization.State != meetingFinalizationFinalized && current.Finalization.State != meetingFinalizationDegraded) {
		return
	}
	record = current
	if err := app.writeMeetingArchiveFinalizationTruth(record, true); err != nil {
		log.Errorf("Could not refresh meeting %s archive finalization: %v", record.ID, err)
		return
	}
	if _, err := app.meetings.markFinalizationArchiveSynced(record.ID, record.ArchiveID, *record.Finalization, time.Now().UTC()); err != nil {
		// A durable transcript may have reopened the receipt after the archive
		// write. Repair the archive to the newest closing truth before returning;
		// the append-side hook performs the same downgrade from the other race
		// direction. Never leave a stale finalized embed while the store says
		// newer accepted source is uncovered.
		if latest, ok := app.meetings.recordByID(record.ID); ok {
			if repairErr := app.writeMeetingArchiveFinalizationTruth(latest, false); repairErr != nil {
				log.Errorf("Could not repair meeting %s archive after finalization race: %v", record.ID, repairErr)
			}
		}
		if !errors.Is(err, ErrMeetingFinalizationSourceAdvanced) {
			log.Errorf("Could not receipt meeting %s archive finalization: %v", record.ID, err)
		}
	}
}

// writeMeetingArchiveFinalizationTruth only mutates the archive owned by the
// exact meeting record. refreshMemory=false is safe from the transcript append
// hook while memory.mu is held: it downgrades the embedded receipt immediately
// without recursively reading the memory store.
func (app *kanbanBoardApp) writeMeetingArchiveFinalizationTruth(record meetingRecord, refreshMemory bool) error {
	if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.ArchiveID) == "" || record.Finalization == nil {
		return nil
	}
	var memory []meetingMemoryEntry
	if refreshMemory {
		// memory.mu must always precede meetingArchiveMu: author deletion holds
		// memory.mu while it removes raw archive truth. The receipt version check
		// under the archive lock rejects this snapshot if a mutation crossed it.
		memory = app.memorySnapshotForMeeting(record.ID, 2000)
	}
	path, err := meetingArchivePath(record.ArchiveID)
	if err != nil {
		return err
	}
	app.meetingArchiveMu.Lock()
	defer app.meetingArchiveMu.Unlock()
	current, found := app.meetings.recordByID(record.ID)
	if !found || current.Finalization == nil || strings.TrimSpace(current.ArchiveID) != strings.TrimSpace(record.ArchiveID) {
		return ErrMeetingFinalizationSourceAdvanced
	}
	if refreshMemory && !sameMeetingFinalizationVersion(record.Finalization, current.Finalization) {
		return ErrMeetingFinalizationSourceAdvanced
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var archive meetingArchive
	if err := json.Unmarshal(raw, &archive); err != nil {
		return err
	}
	if strings.TrimSpace(archive.MeetingID) != record.ID {
		return fmt.Errorf("archive belongs to meeting %s", strings.TrimSpace(archive.MeetingID))
	}
	embedded := cloneMeetingRecord(current)
	archive.Meeting = &embedded
	if refreshMemory {
		archive.Memory = memory
		archive.Notes = buildMeetingNotes(archive.ID, archive.ArchivedAt, archive.ArchivedBy, archive.Board, archive.Memory, archive.Participants)
	}
	if err := writeMeetingArchive(path, archive); err != nil {
		return err
	}
	return nil
}

func sameMeetingFinalizationVersion(left, right *meetingFinalizationReceipt) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.State == right.State && left.Source.equal(right.Source) &&
		left.ObservedRevision == right.ObservedRevision && left.SourceObservedRevision == right.SourceObservedRevision &&
		left.UpdatedAt == right.UpdatedAt && left.FinalizedAt == right.FinalizedAt && left.DegradedAt == right.DegradedAt &&
		left.Brain == right.Brain && left.Digest == right.Digest && left.Actions == right.Actions
}

// prepareTranscriptDeletionFinalizationFence is the write-ahead deletion
// transaction. Before raw memory bytes can be removed it reopens the ended
// receipt and removes that exact row from the owned archive under the archive
// RMW lock. Any receipt/archive failure rejects the source deletion.
func (app *kanbanBoardApp) prepareTranscriptDeletionFinalizationFence(entry meetingMemoryEntry) error {
	if app == nil || app.meetings == nil || entry.Kind != meetingMemoryKindTranscript {
		return nil
	}
	meetingID := strings.TrimSpace(entry.Metadata["meetingId"])
	if meetingID == "" {
		return nil
	}
	sequence, _ := entryCaptureSequence(entry)
	record, changed, err := app.meetings.observeEndedTranscript(meetingID, entry.ID, sequence, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("advance meeting %s finalization fence: %w", meetingID, err)
	}
	if !changed || record.EndedAt == "" || strings.TrimSpace(record.ArchiveID) == "" {
		return nil
	}
	path, err := meetingArchivePath(record.ArchiveID)
	if err != nil {
		return fmt.Errorf("downgrade meeting %s archive for transcript deletion: %w", record.ID, err)
	}
	app.meetingArchiveMu.Lock()
	defer app.meetingArchiveMu.Unlock()
	current, found := app.meetings.recordByID(record.ID)
	if !found || current.Finalization == nil || strings.TrimSpace(current.ArchiveID) != strings.TrimSpace(record.ArchiveID) {
		return fmt.Errorf("downgrade meeting %s archive for transcript deletion: %w", record.ID, ErrMeetingFinalizationSourceAdvanced)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("downgrade meeting %s archive for transcript deletion: %w", record.ID, err)
	}
	var archive meetingArchive
	if err := json.Unmarshal(raw, &archive); err != nil {
		return fmt.Errorf("downgrade meeting %s archive for transcript deletion: %w", record.ID, err)
	}
	if strings.TrimSpace(archive.MeetingID) != record.ID {
		return fmt.Errorf("downgrade meeting %s archive for transcript deletion: archive belongs to meeting %s", record.ID, strings.TrimSpace(archive.MeetingID))
	}
	filtered := make([]meetingMemoryEntry, 0, len(archive.Memory))
	for _, archived := range archive.Memory {
		if archived.Kind == meetingMemoryKindTranscript && archived.ID == entry.ID {
			continue
		}
		filtered = append(filtered, archived)
	}
	archive.Memory = filtered
	archive.Notes = buildMeetingNotes(archive.ID, archive.ArchivedAt, archive.ArchivedBy, archive.Board, archive.Memory, archive.Participants)
	embedded := cloneMeetingRecord(current)
	archive.Meeting = &embedded
	if err := writeMeetingArchive(path, archive); err != nil {
		return fmt.Errorf("downgrade meeting %s archive for transcript deletion: %w", record.ID, err)
	}
	return nil
}

// prepareTranscriptFinalizationFence is the write-ahead half of transcript
// durability. An ended meeting is durably reopened before the memory mutation
// is allowed to commit, so receipt persistence failure rejects the transcript
// instead of silently accepting source beneath stale finalized truth.
func (app *kanbanBoardApp) prepareTranscriptFinalizationFence(entry meetingMemoryEntry) error {
	if app == nil || app.meetings == nil || entry.Kind != meetingMemoryKindTranscript {
		return nil
	}
	meetingID := strings.TrimSpace(entry.Metadata["meetingId"])
	if meetingID == "" {
		return nil
	}
	sequence, _ := entryCaptureSequence(entry)
	record, changed, err := app.meetings.observeEndedTranscript(meetingID, entry.ID, sequence, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("advance meeting %s finalization fence: %w", meetingID, err)
	}
	if !changed || record.EndedAt == "" {
		return nil
	}
	if err := app.writeMeetingArchiveFinalizationTruth(record, false); err != nil {
		return fmt.Errorf("downgrade meeting %s archive for transcript fence: %w", record.ID, err)
	}
	return nil
}

// handleDurableTranscriptCommit is the post-commit half. The receipt was
// already reopened by prepareTranscriptFinalizationFence; only now that source
// is durable may the resumable model worker run.
func (app *kanbanBoardApp) handleDurableTranscriptCommit(entry meetingMemoryEntry) {
	if app == nil || app.meetings == nil || entry.Kind != meetingMemoryKindTranscript {
		return
	}
	meetingID := strings.TrimSpace(entry.Metadata["meetingId"])
	record, found := app.meetings.recordByID(meetingID)
	if !found || record.EndedAt == "" {
		return
	}
	app.meetingFinalizationRunMu.Lock()
	publishing := app.meetingArchivePublishing[record.ID]
	app.meetingFinalizationRunMu.Unlock()
	if !publishing {
		app.scheduleMeetingCoreFinalization(record.ID)
	}
}

// handleMeetingFinalizationOutputMutation makes output integrity self-healing
// in the current process. Read paths fail analysisReady immediately by
// re-hashing the exact body/revision; this nudge then reopens/regenerates the
// exact ended meeting without waiting for the boot audit.
func (app *kanbanBoardApp) handleMeetingFinalizationOutputMutation(entry meetingMemoryEntry) {
	if app == nil || app.meetings == nil || !meetingFinalizationOutputEntry(entry) {
		return
	}
	meetingID := strings.TrimSpace(entry.Metadata["meetingId"])
	record, found := app.meetings.recordByID(meetingID)
	if !found || record.EndedAt == "" || record.Finalization == nil {
		return
	}
	app.scheduleMeetingCoreFinalization(meetingID)
}

func (app *kanbanBoardApp) beginMeetingArchivePublication(meetingID string) {
	if app == nil || strings.TrimSpace(meetingID) == "" {
		return
	}
	app.meetingFinalizationRunMu.Lock()
	if app.meetingArchivePublishing == nil {
		app.meetingArchivePublishing = map[string]bool{}
	}
	app.meetingArchivePublishing[strings.TrimSpace(meetingID)] = true
	app.meetingFinalizationRunMu.Unlock()
	notifyCanonicalReconcileDeferred()
}

func (app *kanbanBoardApp) endMeetingArchivePublication(meetingID string, schedule bool) {
	if app == nil || strings.TrimSpace(meetingID) == "" {
		return
	}
	meetingID = strings.TrimSpace(meetingID)
	if schedule {
		app.scheduleMeetingCoreFinalization(meetingID)
	}
	app.meetingFinalizationRunMu.Lock()
	delete(app.meetingArchivePublishing, meetingID)
	app.meetingFinalizationRunMu.Unlock()
}

func (app *kanbanBoardApp) scheduleMeetingCoreFinalization(meetingID string) {
	app.scheduleMeetingCoreFinalizationPriority(meetingID, true, false)
}

func (app *kanbanBoardApp) scheduleMeetingCoreFinalizationBacklog(meetingID string) {
	app.scheduleMeetingCoreFinalizationPriority(meetingID, false, false)
}

func (app *kanbanBoardApp) scheduleMeetingCoreFinalizationRetry(meetingID string) {
	app.scheduleMeetingCoreFinalizationPriority(meetingID, false, true)
}

func (app *kanbanBoardApp) scheduleMeetingCoreFinalizationPriority(meetingID string, fresh, retry bool) {
	meetingID = strings.TrimSpace(meetingID)
	if app == nil || meetingID == "" {
		return
	}
	app.meetingFinalizationRunMu.Lock()
	if app.meetingFinalizationRetryTimers == nil {
		app.meetingFinalizationRetryTimers = map[string]*time.Timer{}
	}
	if fresh {
		if timer := app.meetingFinalizationRetryTimers[meetingID]; timer != nil {
			timer.Stop()
			delete(app.meetingFinalizationRetryTimers, meetingID)
		}
	}
	if app.meetingFinalizationRunning == nil {
		app.meetingFinalizationRunning = map[string]struct{}{}
	}
	if app.meetingFinalizationQueuedPriority == nil {
		app.meetingFinalizationQueuedPriority = map[string]bool{}
	}
	if app.meetingFinalizationActive == nil {
		app.meetingFinalizationActive = map[string]bool{}
	}
	if app.meetingFinalizationRetryQueued == nil {
		app.meetingFinalizationRetryQueued = map[string]bool{}
	}
	if app.meetingFinalizationRetryActive == nil {
		app.meetingFinalizationRetryActive = map[string]bool{}
	}
	if _, tracked := app.meetingFinalizationRunning[meetingID]; tracked {
		if retry {
			if _, active := app.meetingFinalizationActive[meetingID]; active {
				app.meetingFinalizationRetryActive[meetingID] = true
			} else {
				app.meetingFinalizationRetryQueued[meetingID] = true
			}
		}
		if _, active := app.meetingFinalizationActive[meetingID]; active {
			// Only a fresh, append-side observation needs a second pass. Repeated
			// boot snapshots do not create work and must not spin the queue.
			if fresh {
				if app.meetingFinalizationAgain == nil {
					app.meetingFinalizationAgain = map[string]bool{}
				}
				app.meetingFinalizationAgain[meetingID] = true
			}
		} else if queuedFresh, queued := app.meetingFinalizationQueuedPriority[meetingID]; queued && fresh && !queuedFresh {
			// A live late-source/current-close signal promotes an already queued
			// boot job in place; it never remains behind the historical tail.
			app.meetingFinalizationBacklog = removeMeetingFinalizationQueueID(app.meetingFinalizationBacklog, meetingID)
			app.meetingFinalizationQueue = append(app.meetingFinalizationQueue, meetingID)
			app.meetingFinalizationQueuedPriority[meetingID] = true
		}
		app.ensureMeetingFinalizationWorkersLocked()
		app.meetingFinalizationRunMu.Unlock()
		notifyCanonicalReconcileDeferred()
		return
	}
	app.meetingFinalizationRunning[meetingID] = struct{}{}
	app.meetingFinalizationQueuedPriority[meetingID] = fresh
	if retry {
		app.meetingFinalizationRetryQueued[meetingID] = true
	}
	if fresh {
		app.meetingFinalizationQueue = append(app.meetingFinalizationQueue, meetingID)
	} else {
		app.meetingFinalizationBacklog = append(app.meetingFinalizationBacklog, meetingID)
	}
	app.ensureMeetingFinalizationWorkersLocked()
	app.meetingFinalizationRunMu.Unlock()
	notifyCanonicalReconcileDeferred()
}

// scheduleMeetingFinalizationRetry installs one timer per meeting. Durable
// RetryAfter/RetryAttempt are the restart authority; the timer merely avoids
// waiting for another process boot. Retries enter the serial backlog lane so
// a fresh close or late transcript always retains priority.
func (app *kanbanBoardApp) scheduleMeetingFinalizationRetry(record meetingRecord, runErr error) bool {
	if app == nil || strings.TrimSpace(record.ID) == "" || record.Finalization == nil {
		return false
	}
	// A fresh append/output-repair signal may have completed while the failed
	// worker was releasing its queue slot. Never arm a timer from that stale
	// worker snapshot; the current durable degraded receipt is the authority.
	current, found := app.meetings.recordByID(record.ID)
	if !found || current.Finalization == nil || current.Finalization.State != meetingFinalizationDegraded {
		return false
	}
	record = current
	receipt := record.Finalization
	code := strings.TrimSpace(receipt.LastError)
	if code == "" {
		code = meetingFinalizationErrorCode(runErr)
	}
	if !meetingFinalizationRetryableCode(code) {
		return false
	}
	delay := meetingFinalizationRetryDuration(max(receipt.RetryAttempt, 1))
	if retryAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(receipt.RetryAfter)); err == nil {
		delay = time.Until(retryAt)
		if delay < 0 {
			delay = 0
		}
	}
	meetingID := strings.TrimSpace(record.ID)
	expectedRetryAfter := strings.TrimSpace(receipt.RetryAfter)
	expectedRetryAttempt := receipt.RetryAttempt
	app.meetingFinalizationRunMu.Lock()
	if app.meetingFinalizationRetriesStopped {
		app.meetingFinalizationRunMu.Unlock()
		return false
	}
	if app.meetingFinalizationRetryTimers == nil {
		app.meetingFinalizationRetryTimers = map[string]*time.Timer{}
	}
	if existing := app.meetingFinalizationRetryTimers[meetingID]; existing != nil {
		existing.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(delay, func() {
		app.meetingFinalizationRunMu.Lock()
		if app.meetingFinalizationRetryTimers[meetingID] != timer {
			app.meetingFinalizationRunMu.Unlock()
			return
		}
		delete(app.meetingFinalizationRetryTimers, meetingID)
		app.meetingFinalizationRunMu.Unlock()
		current, found := app.meetings.recordByID(meetingID)
		if !found || current.Finalization == nil || current.Finalization.State != meetingFinalizationDegraded ||
			strings.TrimSpace(current.Finalization.RetryAfter) != expectedRetryAfter || current.Finalization.RetryAttempt != expectedRetryAttempt {
			return
		}
		app.scheduleMeetingCoreFinalizationRetry(meetingID)
	})
	app.meetingFinalizationRetryTimers[meetingID] = timer
	app.meetingFinalizationRunMu.Unlock()
	return true
}

func (app *kanbanBoardApp) cancelMeetingFinalizationRetry(meetingID string) {
	if app == nil {
		return
	}
	app.meetingFinalizationRunMu.Lock()
	if timer := app.meetingFinalizationRetryTimers[strings.TrimSpace(meetingID)]; timer != nil {
		timer.Stop()
		delete(app.meetingFinalizationRetryTimers, strings.TrimSpace(meetingID))
	}
	app.meetingFinalizationRunMu.Unlock()
}

func (app *kanbanBoardApp) stopMeetingFinalizationRetries() {
	if app == nil {
		return
	}
	app.meetingFinalizationRunMu.Lock()
	app.meetingFinalizationRetriesStopped = true
	for meetingID, timer := range app.meetingFinalizationRetryTimers {
		if timer != nil {
			timer.Stop()
		}
		delete(app.meetingFinalizationRetryTimers, meetingID)
	}
	app.meetingFinalizationRunMu.Unlock()
}

// canonicalReconcileDeferred keeps whole-history parity work out of the live
// meeting and current-close critical path. Durable capture remains active for
// every write. Historical boot repair does not hold this gate: only an open
// sitting, archive publication, or fresh finalization work for the meeting
// that just closed postpones the scan.
func (app *kanbanBoardApp) canonicalReconcileDeferred() bool {
	if app == nil {
		return false
	}
	if app.meetings != nil && len(app.meetings.openRoomIDs()) > 0 {
		return true
	}
	app.meetingFinalizationRunMu.Lock()
	defer app.meetingFinalizationRunMu.Unlock()
	if len(app.meetingArchivePublishing) > 0 || len(app.meetingFinalizationAgain) > 0 || len(app.meetingFinalizationRetryQueued) > 0 || len(app.meetingFinalizationRetryActive) > 0 {
		return true
	}
	for _, fresh := range app.meetingFinalizationQueuedPriority {
		if fresh {
			return true
		}
	}
	for _, fresh := range app.meetingFinalizationActive {
		if fresh {
			return true
		}
	}
	return false
}

func removeMeetingFinalizationQueueID(queue []string, meetingID string) []string {
	for index, candidate := range queue {
		if candidate == meetingID {
			return append(queue[:index], queue[index+1:]...)
		}
	}
	return queue
}

func (app *kanbanBoardApp) ensureMeetingFinalizationWorkersLocked() {
	// Historical recovery gets one serial lane. A second lane exists only while
	// fresh work is queued, so a three-minute degraded old call cannot hold a
	// current close hostage and boot cannot fan years of history out at once.
	start := 0
	if app.meetingFinalizationWorkers == 0 && (len(app.meetingFinalizationQueue) > 0 || (len(app.meetingFinalizationBacklog) > 0 && !app.meetingFinalizationBacklogActive)) {
		start = 1
	}
	if len(app.meetingFinalizationQueue) > 0 && app.meetingFinalizationWorkers+start < 2 {
		start++
	}
	for range start {
		app.meetingFinalizationWorkers++
		app.meetingFinalizationWorker = true
		go app.runMeetingFinalizationQueue()
	}
}

func (app *kanbanBoardApp) nextMeetingFinalizationLocked() (string, bool, bool) {
	if len(app.meetingFinalizationQueue) > 0 {
		meetingID := app.meetingFinalizationQueue[0]
		app.meetingFinalizationQueue = app.meetingFinalizationQueue[1:]
		delete(app.meetingFinalizationQueuedPriority, meetingID)
		delete(app.meetingFinalizationRetryQueued, meetingID)
		app.meetingFinalizationActive[meetingID] = true
		return meetingID, true, true
	}
	if len(app.meetingFinalizationBacklog) > 0 && !app.meetingFinalizationBacklogActive {
		meetingID := app.meetingFinalizationBacklog[0]
		app.meetingFinalizationBacklog = app.meetingFinalizationBacklog[1:]
		delete(app.meetingFinalizationQueuedPriority, meetingID)
		if app.meetingFinalizationRetryQueued[meetingID] {
			app.meetingFinalizationRetryActive[meetingID] = true
		}
		delete(app.meetingFinalizationRetryQueued, meetingID)
		app.meetingFinalizationActive[meetingID] = false
		app.meetingFinalizationBacklogActive = true
		return meetingID, false, true
	}
	return "", false, false
}

func (app *kanbanBoardApp) runMeetingFinalizationQueue() {
	for {
		app.meetingFinalizationRunMu.Lock()
		meetingID, fresh, ok := app.nextMeetingFinalizationLocked()
		if !ok {
			app.meetingFinalizationWorkers--
			app.meetingFinalizationWorker = app.meetingFinalizationWorkers > 0
			app.meetingFinalizationRunMu.Unlock()
			return
		}
		app.meetingFinalizationRunMu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), meetingFinalizationCoreTimeout)
		finalized, err := app.finalizeMeetingCore(ctx, meetingID, nil)
		cancel()
		var retryRecord *meetingRecord
		if err != nil {
			log.Errorf("Meeting %s late-source finalization remains degraded: %v", meetingID, err)
			if current, ok := app.meetings.recordByID(meetingID); ok {
				app.broadcastMeetingRecord(current)
				app.refreshMeetingArchiveFinalization(current)
				retry := cloneMeetingRecord(current)
				retryRecord = &retry
			}
		} else {
			app.cancelMeetingFinalizationRetry(meetingID)
			app.broadcastMeetingRecord(finalized)
			app.refreshMeetingArchiveFinalization(finalized)
			app.scheduleMeetingNonCoreRollups(meetingRoomID(finalized))
		}
		app.meetingFinalizationRunMu.Lock()
		delete(app.meetingFinalizationActive, meetingID)
		delete(app.meetingFinalizationRetryActive, meetingID)
		if !fresh {
			app.meetingFinalizationBacklogActive = false
		}
		if app.meetingFinalizationAgain[meetingID] {
			delete(app.meetingFinalizationAgain, meetingID)
			app.meetingFinalizationQueue = append(app.meetingFinalizationQueue, meetingID)
			app.meetingFinalizationQueuedPriority[meetingID] = true
		} else {
			delete(app.meetingFinalizationRunning, meetingID)
		}
		app.ensureMeetingFinalizationWorkersLocked()
		app.meetingFinalizationRunMu.Unlock()
		if retryRecord != nil {
			app.scheduleMeetingFinalizationRetry(*retryRecord, err)
		}
	}
}

// resumeMeetingFinalizationsAtBoot installs every known closing/degraded
// receipt onto the bounded priority queue before ordinary ambient workers
// start. Historical work has one serial lane; a second lane is reserved for a
// fresh close/late-source fence. Boot never waits for a model or performs
// per-meeting disk writes. The stale-finalized crash audit runs in the
// background from the same immutable snapshot.
func (app *kanbanBoardApp) resumeMeetingFinalizationsAtBoot(apiKey string) {
	if app == nil || app.meetings == nil {
		return
	}
	app.mu.Lock()
	if strings.TrimSpace(app.apiKey) == "" {
		app.apiKey = strings.TrimSpace(apiKey)
	}
	app.mu.Unlock()
	records := app.meetings.recordsWithFinalization()
	for _, record := range records {
		if record.Finalization != nil && (record.Finalization.State == meetingFinalizationClosing || record.Finalization.State == meetingFinalizationDegraded) {
			if record.Finalization.State != meetingFinalizationDegraded || !app.scheduleMeetingFinalizationRetry(record, nil) {
				app.scheduleMeetingCoreFinalizationBacklog(record.ID)
			}
		}
	}
	go app.auditMeetingFinalizationsAfterBoot(records)
}

func (app *kanbanBoardApp) auditMeetingFinalizationsAfterBoot(records []meetingRecord) {
	// A process may have died after the transcript append fsync and before its
	// append-side receipt update. Audit only meetings that already participate
	// in the receipt protocol; legacy ended history stays untracked and cannot
	// trigger an accidental years-long backfill.
	for _, record := range records {
		if record.Finalization == nil {
			continue
		}
		if record.Finalization.State == meetingFinalizationFinalized {
			source, observedRevision := app.meetingFinalizationSourceObservation(record.ID)
			if meetingFinalizationReceiptReady(record.Finalization) && record.Finalization.Source.equal(source) && app.meetingFinalizationOutputsReady(record) {
				if record.ArchiveID != "" && record.Finalization.ArchiveSyncedAt == "" {
					app.refreshMeetingArchiveFinalization(record)
				}
				continue
			}
			if _, err := app.meetings.beginFinalizationAtRevision(record.ID, source, observedRevision, true, time.Now().UTC()); err != nil {
				log.Errorf("Could not reopen stale meeting %s finalization receipt: %v", record.ID, err)
				continue
			}
			app.scheduleMeetingCoreFinalizationBacklog(record.ID)
		} else if record.Finalization.State != meetingFinalizationClosing && record.Finalization.State != meetingFinalizationDegraded {
			source, observedRevision := app.meetingFinalizationSourceObservation(record.ID)
			if _, err := app.meetings.beginFinalizationAtRevision(record.ID, source, observedRevision, false, time.Now().UTC()); err != nil {
				log.Errorf("Could not normalize meeting %s finalization receipt: %v", record.ID, err)
				continue
			}
			app.scheduleMeetingCoreFinalizationBacklog(record.ID)
		}
	}
}
