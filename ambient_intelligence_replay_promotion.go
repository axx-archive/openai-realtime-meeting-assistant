package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	ambientReplayPromotionManifestMetadataKey  = "ambientReplayManifestDigest"
	ambientReplayPromotionExecutionMetadataKey = "ambientReplayExecutionId"
	ambientReplayPromotionBodyMetadataKey      = "ambientReplayCanonicalBodyDigest"
)

type ambientReplayPromotionJournal struct {
	ManifestDigest                 string            `json:"manifestDigest"`
	ExecutionID                    string            `json:"executionId"`
	TenantID                       string            `json:"tenantId"`
	RoomID                         string            `json:"roomId"`
	SittingID                      string            `json:"sittingId"`
	SourceManifestDigest           string            `json:"sourceManifestDigest"`
	MeetingDigestStageOutputDigest string            `json:"meetingDigestStageOutputDigest"`
	CanonicalBodyDigest            string            `json:"canonicalBodyDigest"`
	CanonicalBody                  string            `json:"canonicalBody"`
	CanonicalMetadata              map[string]string `json:"canonicalMetadata"`
	ApprovalReference              string            `json:"approvalReference"`
	RollbackFloor                  string            `json:"rollbackFloor"`
	ReleaseCommit                  string            `json:"releaseCommit"`
	RecordedAt                     time.Time         `json:"recordedAt"`
}

type productionAmbientReplayPromoter struct {
	app          *kanbanBoardApp
	store        *PostgresAmbientReplayStore
	now          func() time.Time
	afterReceipt func() error // test-only crash seam
}

func newProductionAmbientReplayPromoter(app *kanbanBoardApp, store *PostgresAmbientReplayStore) *productionAmbientReplayPromoter {
	if app == nil || app.memory == nil || store == nil || store.pool == nil {
		return nil
	}
	return &productionAmbientReplayPromoter{app: app, store: store, now: func() time.Time { return time.Now().UTC() }}
}

func (promoter *productionAmbientReplayPromoter) currentTime() time.Time {
	if promoter != nil && promoter.now != nil {
		return promoter.now().UTC()
	}
	return time.Now().UTC()
}

func (promoter *productionAmbientReplayPromoter) PromoteAmbientReplay(ctx context.Context, manifest AmbientReplayManifest, executionID string, artifacts []AmbientReplayArtifact) error {
	journal, err := promoter.prepareJournal(manifest, executionID, artifacts)
	if err != nil {
		return err
	}
	if err := promoter.persistJournal(journal); err != nil {
		return err
	}
	receipt := journal.promotionReceipt()
	if _, err := promoter.store.CommitAmbientReplayPromotionReceipt(ctx, receipt); err != nil {
		return err
	}
	if promoter.afterReceipt != nil {
		if err := promoter.afterReceipt(); err != nil {
			return fmt.Errorf("%w: %v", ErrAmbientReplayPromotionPending, err)
		}
	}
	if err := promoter.finalizeJournal(journal); err != nil {
		return fmt.Errorf("%w: %v", ErrAmbientReplayPromotionPending, err)
	}
	return nil
}

func (promoter *productionAmbientReplayPromoter) RecoverAmbientReplayPromotions(ctx context.Context) error {
	if promoter == nil || promoter.app == nil || promoter.app.memory == nil || promoter.store == nil {
		return ErrAmbientReplayUnavailable
	}
	for _, entry := range promoter.app.memory.entriesOfKind(meetingMemoryKindAmbientReplayPromotion, 0) {
		var journal ambientReplayPromotionJournal
		if json.Unmarshal([]byte(entry.Text), &journal) != nil || journal.ManifestDigest == "" {
			return ErrAmbientReplayDrift
		}
		receipt, found, err := promoter.store.LoadAmbientReplayPromotionReceipt(ctx, journal.ManifestDigest)
		if err != nil {
			return err
		}
		if !found {
			// Journal-first is deliberate. A process may die or PostgreSQL may fail
			// before authority commits; such a body remains hidden and inert.
			continue
		}
		if !ambientReplayPromotionReceiptMatchesJournal(receipt, journal) {
			return ErrAmbientReplayDrift
		}
		if err := promoter.finalizeJournal(journal); err != nil {
			return err
		}
		if err := promoter.store.FinalizePromotedExecution(ctx, receipt, promoter.currentTime()); err != nil {
			return err
		}
	}
	return nil
}

func (promoter *productionAmbientReplayPromoter) prepareJournal(manifest AmbientReplayManifest, executionID string, artifacts []AmbientReplayArtifact) (ambientReplayPromotionJournal, error) {
	if promoter == nil || promoter.app == nil || promoter.app.memory == nil || promoter.store == nil ||
		manifest.Schema != ambientReplaySchema || !isHexDigest(manifest.Digest) || !isHexDigest(manifest.SourceManifestDigest) || strings.TrimSpace(executionID) == "" {
		return ambientReplayPromotionJournal{}, ErrAmbientReplayInvalid
	}
	meetingArtifacts := make([]AmbientReplayArtifact, 0, 1)
	for _, artifact := range artifacts {
		if artifact.Kind == "meeting_digest" {
			meetingArtifacts = append(meetingArtifacts, artifact)
		}
	}
	if len(meetingArtifacts) != 1 || strings.TrimSpace(meetingArtifacts[0].Text) == "" ||
		meetingArtifacts[0].ManifestDigest != manifest.Digest || meetingArtifacts[0].SourceManifestDigest != manifest.SourceManifestDigest {
		return ambientReplayPromotionJournal{}, ErrAmbientReplayDrift
	}
	stageDigest, err := digestAmbientReplayArtifacts(meetingArtifacts)
	if err != nil {
		return ambientReplayPromotionJournal{}, err
	}
	payload, ok := parseMeetingDigest(meetingArtifacts[0].Text)
	if !ok {
		return ambientReplayPromotionJournal{}, ErrAmbientReplayDrift
	}
	sources, spanStart, spanEnd, err := promoter.currentSources(manifest)
	if err != nil {
		return ambientReplayPromotionJournal{}, err
	}
	clampMeetingDigestPayload(&payload, manifest.SittingID, dayBucket(spanEnd), spanStart, spanEnd)
	allowedAnchors := make(map[string]struct{}, len(manifest.Sources))
	for _, source := range manifest.Sources {
		allowedAnchors[source.ObjectID] = struct{}{}
	}
	verifyMeetingDigestPayload(&payload, spanStart, spanEnd, allowedAnchors)
	canonical, err := json.Marshal(payload)
	if err != nil {
		return ambientReplayPromotionJournal{}, err
	}
	canonicalBody := string(canonical)
	canonicalBodyDigest := digestBrainString(canonicalBody)
	metadata := promoter.canonicalMetadata(manifest, payload, sources, spanStart, spanEnd, canonicalBodyDigest, executionID)
	recordedAt := promoter.currentTime()
	return ambientReplayPromotionJournal{
		ManifestDigest: manifest.Digest, ExecutionID: executionID, TenantID: manifest.TenantID, RoomID: manifest.RoomID, SittingID: manifest.SittingID,
		SourceManifestDigest: manifest.SourceManifestDigest, MeetingDigestStageOutputDigest: stageDigest,
		CanonicalBodyDigest: canonicalBodyDigest, CanonicalBody: canonicalBody, CanonicalMetadata: metadata,
		ApprovalReference: manifest.ApprovalReference, RollbackFloor: manifest.RollbackFloor, ReleaseCommit: manifest.ReleaseCommit, RecordedAt: recordedAt,
	}, nil
}

func (promoter *productionAmbientReplayPromoter) currentSources(manifest AmbientReplayManifest) ([]meetingMemoryEntry, time.Time, time.Time, error) {
	sources := make([]meetingMemoryEntry, 0, len(manifest.Sources))
	var spanStart, spanEnd time.Time
	for _, source := range manifest.Sources {
		entry, found := promoter.app.memory.entryByID(source.ObjectID)
		if !found || entry.Kind != meetingMemoryKindTranscript || digestBrainString(entry.Text) != source.ContentDigest ||
			normalizeRoomID(entry.Metadata["roomId"]) != manifest.RoomID || strings.TrimSpace(entry.Metadata["meetingId"]) != manifest.SittingID {
			return nil, time.Time{}, time.Time{}, ErrAmbientReplayDrift
		}
		if spanStart.IsZero() || source.OccurredStart.Before(spanStart) {
			spanStart = source.OccurredStart
		}
		if source.OccurredEnd.After(spanEnd) {
			spanEnd = source.OccurredEnd
		}
		sources = append(sources, entry)
	}
	if len(sources) == 0 || spanStart.IsZero() || spanEnd.IsZero() || spanEnd.Before(spanStart) {
		return nil, time.Time{}, time.Time{}, ErrAmbientReplayInvalid
	}
	return sources, spanStart, spanEnd, nil
}

func (promoter *productionAmbientReplayPromoter) canonicalMetadata(manifest AmbientReplayManifest, payload meetingDigestPayload, sources []meetingMemoryEntry, spanStart, spanEnd time.Time, bodyDigest, executionID string) map[string]string {
	model := ""
	for _, stage := range manifest.Stages {
		if stage.Name == "meeting_digest" {
			model = stage.Model
			break
		}
	}
	metadata := map[string]string{
		"source": "ambient_replay", "model": model, "meetingId": manifest.SittingID, "roomId": manifest.RoomID,
		digestDayMetadataKey: dayBucket(spanEnd), digestSpanStartMetadataKey: spanStart.UTC().Format(time.RFC3339),
		digestSpanEndMetadataKey: spanEnd.UTC().Format(time.RFC3339), meetingDigestCursorMetadataKey: manifest.Digest,
		meetingDigestCaptureMetadataKey: strconv.FormatUint(manifest.EndAt, 10), "generatedAt": promoter.currentTime().Format(time.RFC3339),
		ambientReplayPromotionManifestMetadataKey: manifest.Digest, ambientReplayPromotionExecutionMetadataKey: executionID,
		ambientReplayPromotionBodyMetadataKey: bodyDigest, "sourceManifestDigest": manifest.SourceManifestDigest,
	}
	metadata = applyAmbientDerivedScope(metadata, sources)
	if aliases := digestAliasesMetadata(payload.Aliases); aliases != "" {
		metadata[digestAliasesMetadataKey] = aliases
	}
	segments := meetingRecordSegments(promoter.app.memory.snapshotForMeeting(manifest.SittingID, 0), manifest.SittingID)
	metadata[meetingRecordDigestSourceRevisionsMetadataKey] = meetingRecordDigestSourceRevisionMetadata(payload, segments)
	record, hasRecord := promoter.app.meetingDirectoryRecord(manifest.SittingID)
	resolvable := hasRecord && !isLegacyMeetingKey(manifest.SittingID)
	var sittingStart time.Time
	if resolvable {
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(record.StartedAt)); err == nil {
			sittingStart = parsed
			metadata[digestSittingStartedAtMetadataKey] = record.StartedAt
		}
		if ended := strings.TrimSpace(record.EndedAt); ended != "" {
			metadata[digestSittingEndedAtMetadataKey] = ended
		}
	}
	coverage := promoter.app.memory.transcriptCoverageForMeeting(manifest.SittingID)
	metadata[digestCoverageMetadataKey] = meetingCoverageLabel(resolvable, sittingStart, spanStart, coverage.MaxInternalGap)
	if promoter.app.meetingListenOnly(manifest.SittingID) {
		metadata[listenOnlyMetadataKey] = "true"
		metadata[externalMayPredateCaptureMetadataKey] = "true"
	}
	return metadata
}

func (promoter *productionAmbientReplayPromoter) persistJournal(journal ambientReplayPromotionJournal) error {
	raw, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	id := "ambient-replay-promotion-" + journal.ManifestDigest
	if existing, found := promoter.app.memory.entryByID(id); found {
		if existing.Kind != meetingMemoryKindAmbientReplayPromotion || existing.Text != string(raw) {
			return ErrAmbientReplayDrift
		}
		return nil
	}
	_, appended, err := promoter.app.memory.appendAmbientEntry(meetingMemoryKindAmbientReplayPromotion, id, string(raw), map[string]string{
		relevanceMetadataKey: relevanceExpired, ambientReplayPromotionManifestMetadataKey: journal.ManifestDigest,
		ambientReplayPromotionExecutionMetadataKey: journal.ExecutionID, ambientReplayPromotionBodyMetadataKey: journal.CanonicalBodyDigest,
	})
	if err != nil {
		return err
	}
	if !appended {
		return ErrAmbientReplayDrift
	}
	return nil
}

func (promoter *productionAmbientReplayPromoter) finalizeJournal(journal ambientReplayPromotionJournal) error {
	if !isHexDigest(journal.ManifestDigest) || !isHexDigest(journal.CanonicalBodyDigest) ||
		digestBrainString(journal.CanonicalBody) != journal.CanonicalBodyDigest || strings.TrimSpace(journal.SittingID) == "" {
		return ErrAmbientReplayDrift
	}
	if current, found := promoter.app.memory.currentDigest(meetingMemoryKindMeetingDigest, journal.SittingID); found {
		if current.Metadata[ambientReplayPromotionManifestMetadataKey] == journal.ManifestDigest {
			if digestBrainString(current.Text) != journal.CanonicalBodyDigest {
				return ErrAmbientReplayDrift
			}
			return nil
		}
	}
	metadata := make(map[string]string, len(journal.CanonicalMetadata))
	for key, value := range journal.CanonicalMetadata {
		metadata[key] = value
	}
	entry, err := promoter.app.memory.upsertDigest(meetingMemoryKindMeetingDigest, journal.SittingID, journal.CanonicalBody, metadata)
	if err != nil {
		return err
	}
	if digestBrainString(entry.Text) != journal.CanonicalBodyDigest || entry.Metadata[ambientReplayPromotionManifestMetadataKey] != journal.ManifestDigest {
		return ErrAmbientReplayDrift
	}
	promoter.app.broadcastMeetingIntelligence(journal.RoomID, journal.SittingID)
	promoter.app.nudgeAmbientAgent(dayDigestAgentName)
	promoter.app.nudgeAmbientAgent(entityLedgerAgentName)
	return nil
}

func (journal ambientReplayPromotionJournal) promotionReceipt() AmbientReplayPromotionReceipt {
	return AmbientReplayPromotionReceipt{
		ManifestDigest: journal.ManifestDigest, ExecutionID: journal.ExecutionID, TenantID: journal.TenantID, RoomID: journal.RoomID,
		SittingID: journal.SittingID, SourceManifestDigest: journal.SourceManifestDigest,
		MeetingDigestStageOutputDigest: journal.MeetingDigestStageOutputDigest, CanonicalMeetingDigestBodyHash: journal.CanonicalBodyDigest,
		ApprovalReference: journal.ApprovalReference, RollbackFloor: journal.RollbackFloor, ReleaseCommit: journal.ReleaseCommit, RecordedAt: journal.RecordedAt,
	}
}

func ambientReplayPromotionReceiptMatchesJournal(receipt AmbientReplayPromotionReceipt, journal ambientReplayPromotionJournal) bool {
	want := journal.promotionReceipt()
	return receipt.ManifestDigest == want.ManifestDigest && receipt.ExecutionID == want.ExecutionID && receipt.TenantID == want.TenantID &&
		normalizeRoomID(receipt.RoomID) == normalizeRoomID(want.RoomID) && receipt.SittingID == want.SittingID &&
		receipt.SourceManifestDigest == want.SourceManifestDigest && receipt.MeetingDigestStageOutputDigest == want.MeetingDigestStageOutputDigest &&
		receipt.CanonicalMeetingDigestBodyHash == want.CanonicalMeetingDigestBodyHash && receipt.ApprovalReference == want.ApprovalReference &&
		receipt.RollbackFloor == want.RollbackFloor && receipt.ReleaseCommit == want.ReleaseCommit && receipt.RecordedAt.Equal(want.RecordedAt)
}

var _ AmbientReplayPromoter = (*productionAmbientReplayPromoter)(nil)
var _ AmbientReplayRecoveryPromoter = (*productionAmbientReplayPromoter)(nil)
