package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresAmbientReplayStore struct{ pool *pgxpool.Pool }

func (store *PostgresAmbientReplayStore) LoadAmbientReplayPromotionReceipt(ctx context.Context, manifestDigestText string) (AmbientReplayPromotionReceipt, bool, error) {
	if store == nil || store.pool == nil || !isHexDigest(manifestDigestText) {
		return AmbientReplayPromotionReceipt{}, false, ErrAmbientReplayInvalid
	}
	manifestDigest, _ := hex.DecodeString(manifestDigestText)
	var receipt AmbientReplayPromotionReceipt
	var sourceDigest, stageDigest, bodyDigest []byte
	err := store.pool.QueryRow(ctx, `SELECT execution_id::text,tenant_id,room_id,sitting_id,source_manifest_digest,
		meeting_digest_stage_output_digest,canonical_meeting_digest_body_digest,approval_reference,rollback_floor,release_commit,recorded_at
		FROM ambient_intelligence_replay_promotions WHERE manifest_digest=$1`, manifestDigest).Scan(&receipt.ExecutionID, &receipt.TenantID,
		&receipt.RoomID, &receipt.SittingID, &sourceDigest, &stageDigest, &bodyDigest, &receipt.ApprovalReference, &receipt.RollbackFloor,
		&receipt.ReleaseCommit, &receipt.RecordedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AmbientReplayPromotionReceipt{}, false, nil
	}
	if err != nil {
		return AmbientReplayPromotionReceipt{}, false, err
	}
	receipt.ManifestDigest = manifestDigestText
	receipt.SourceManifestDigest = hex.EncodeToString(sourceDigest)
	receipt.MeetingDigestStageOutputDigest = hex.EncodeToString(stageDigest)
	receipt.CanonicalMeetingDigestBodyHash = hex.EncodeToString(bodyDigest)
	return receipt, true, nil
}

// FinalizePromotedExecution is the receipt-first crash recovery boundary. A
// committed promotion is canonical truth even when the process died after the
// PostgreSQL transaction and before the ordinary execution completion update;
// recovery therefore does not depend on the expired execution lease.
func (store *PostgresAmbientReplayStore) FinalizePromotedExecution(ctx context.Context, receipt AmbientReplayPromotionReceipt, at time.Time) error {
	if store == nil || store.pool == nil || !isHexDigest(receipt.ManifestDigest) || strings.TrimSpace(receipt.ExecutionID) == "" || at.IsZero() {
		return ErrAmbientReplayInvalid
	}
	manifestDigest, _ := hex.DecodeString(receipt.ManifestDigest)
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var status, executionID string
	if err := tx.QueryRow(ctx, `SELECT status,execution_id::text FROM ambient_intelligence_replay_manifests
		WHERE manifest_digest=$1 FOR UPDATE`, manifestDigest).Scan(&status, &executionID); err != nil {
		return err
	}
	var receiptExecutionID string
	if err := tx.QueryRow(ctx, `SELECT execution_id::text FROM ambient_intelligence_replay_promotions
		WHERE manifest_digest=$1`, manifestDigest).Scan(&receiptExecutionID); err != nil {
		return err
	}
	if executionID != receipt.ExecutionID || receiptExecutionID != receipt.ExecutionID {
		return ErrAmbientReplayDrift
	}
	if status == "completed" {
		return tx.Commit(ctx)
	}
	if status != "running" {
		return ErrAmbientReplayDrift
	}
	tag, err := tx.Exec(ctx, `UPDATE ambient_intelligence_replay_manifests SET status='completed',completed_at=$2,
		lease_expires_at=NULL,last_error_code=NULL WHERE manifest_digest=$1 AND status='running'`, manifestDigest, at.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrAmbientReplayDrift
	}
	return tx.Commit(ctx)
}

func (store *PostgresAmbientReplayStore) CommitAmbientReplayPromotionReceipt(ctx context.Context, receipt AmbientReplayPromotionReceipt) (bool, error) {
	if store == nil || store.pool == nil || !isHexDigest(receipt.ManifestDigest) || !isHexDigest(receipt.SourceManifestDigest) ||
		!isHexDigest(receipt.MeetingDigestStageOutputDigest) || !isHexDigest(receipt.CanonicalMeetingDigestBodyHash) ||
		strings.TrimSpace(receipt.ExecutionID) == "" || strings.TrimSpace(receipt.TenantID) == "" ||
		strings.TrimSpace(receipt.RoomID) == "" || strings.TrimSpace(receipt.SittingID) == "" || receipt.RecordedAt.IsZero() {
		return false, ErrAmbientReplayInvalid
	}
	manifestDigest, _ := hex.DecodeString(receipt.ManifestDigest)
	sourceDigest, _ := hex.DecodeString(receipt.SourceManifestDigest)
	stageDigest, _ := hex.DecodeString(receipt.MeetingDigestStageOutputDigest)
	bodyDigest, _ := hex.DecodeString(receipt.CanonicalMeetingDigestBodyHash)
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "ambient-replay-promotion\x1f"+receipt.ManifestDigest); err != nil {
		return false, err
	}
	var executionID, tenantID, roomID, sittingID, approval, rollback, release string
	var existingSource, existingStage, existingBody []byte
	var recordedAt time.Time
	existingErr := tx.QueryRow(ctx, `SELECT execution_id::text,tenant_id,room_id,sitting_id,source_manifest_digest,
		meeting_digest_stage_output_digest,canonical_meeting_digest_body_digest,approval_reference,rollback_floor,release_commit,recorded_at
		FROM ambient_intelligence_replay_promotions WHERE manifest_digest=$1 FOR UPDATE`, manifestDigest).Scan(&executionID, &tenantID, &roomID, &sittingID,
		&existingSource, &existingStage, &existingBody, &approval, &rollback, &release, &recordedAt)
	if existingErr == nil {
		if executionID != receipt.ExecutionID || tenantID != receipt.TenantID || roomID != normalizeRoomID(receipt.RoomID) || sittingID != receipt.SittingID ||
			hex.EncodeToString(existingSource) != receipt.SourceManifestDigest || hex.EncodeToString(existingStage) != receipt.MeetingDigestStageOutputDigest ||
			hex.EncodeToString(existingBody) != receipt.CanonicalMeetingDigestBodyHash || approval != receipt.ApprovalReference || rollback != receipt.RollbackFloor ||
			release != receipt.ReleaseCommit || !recordedAt.Equal(receipt.RecordedAt.UTC()) {
			return false, ErrAmbientReplayDrift
		}
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, nil
	}
	if !errors.Is(existingErr, pgx.ErrNoRows) {
		return false, existingErr
	}
	_, err = tx.Exec(ctx, `INSERT INTO ambient_intelligence_replay_promotions (
		manifest_digest,execution_id,tenant_id,room_id,sitting_id,source_manifest_digest,meeting_digest_stage_output_digest,
		canonical_meeting_digest_body_digest,approval_reference,rollback_floor,release_commit,recorded_at
	) VALUES ($1,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	`, manifestDigest, receipt.ExecutionID, receipt.TenantID, normalizeRoomID(receipt.RoomID), receipt.SittingID,
		sourceDigest, stageDigest, bodyDigest, receipt.ApprovalReference, receipt.RollbackFloor, receipt.ReleaseCommit, receipt.RecordedAt.UTC())
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (store *PostgresAmbientReplayStore) SaveManifest(ctx context.Context, manifest AmbientReplayManifest) (AmbientReplayManifest, bool, error) {
	if store == nil || store.pool == nil || !isHexDigest(manifest.Digest) {
		return AmbientReplayManifest{}, false, ErrAmbientReplayUnavailable
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return AmbientReplayManifest{}, false, err
	}
	digest, _ := hex.DecodeString(manifest.Digest)
	idempotencyKey, keyErr := hex.DecodeString(manifest.IdempotencyKey)
	if keyErr != nil || len(idempotencyKey) != 32 {
		return AmbientReplayManifest{}, false, ErrAmbientReplayInvalid
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return AmbientReplayManifest{}, false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	// Serialize the no-existing-row case as well as the active-row case. A
	// row-level lock cannot protect two concurrent first plans, while this
	// transaction-scoped sitting lock makes a lost-response retry observe the
	// first committed manifest and return it idempotently.
	planLock := manifest.TenantID + "\x1f" + manifest.IdempotencyKey
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, planLock); err != nil {
		return AmbientReplayManifest{}, false, err
	}
	sittingLock := manifest.TenantID + "\x1f" + manifest.RoomID + "\x1f" + manifest.SittingID
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, sittingLock); err != nil {
		return AmbientReplayManifest{}, false, err
	}
	var keyedRaw []byte
	keyedErr := tx.QueryRow(ctx, `SELECT manifest FROM ambient_intelligence_replay_manifests
		WHERE tenant_id=$1 AND idempotency_key=$2 FOR UPDATE`, manifest.TenantID, idempotencyKey).Scan(&keyedRaw)
	if keyedErr == nil {
		var keyed AmbientReplayManifest
		if json.Unmarshal(keyedRaw, &keyed) != nil || !ambientReplayManifestEquivalentForRetry(keyed, manifest) {
			return AmbientReplayManifest{}, false, ErrAmbientReplayDrift
		}
		if err := tx.Commit(ctx); err != nil {
			return AmbientReplayManifest{}, false, err
		}
		return keyed, false, nil
	}
	if !errors.Is(keyedErr, pgx.ErrNoRows) {
		return AmbientReplayManifest{}, false, keyedErr
	}
	var activeRaw []byte
	activeErr := tx.QueryRow(ctx, `SELECT manifest FROM ambient_intelligence_replay_manifests
		WHERE tenant_id=$1 AND room_id=$2 AND sitting_id=$3 AND status IN ('planned','running') FOR UPDATE`,
		manifest.TenantID, manifest.RoomID, manifest.SittingID).Scan(&activeRaw)
	if activeErr == nil {
		var active AmbientReplayManifest
		if json.Unmarshal(activeRaw, &active) != nil {
			return AmbientReplayManifest{}, false, ErrAmbientReplayDrift
		}
		if !ambientReplayManifestEquivalentForRetry(active, manifest) {
			return AmbientReplayManifest{}, false, ErrAmbientReplayAlreadyActive
		}
		if err := tx.Commit(ctx); err != nil {
			return AmbientReplayManifest{}, false, err
		}
		return active, false, nil
	}
	if !errors.Is(activeErr, pgx.ErrNoRows) {
		return AmbientReplayManifest{}, false, activeErr
	}
	tag, err := tx.Exec(ctx, `INSERT INTO ambient_intelligence_replay_manifests (
		manifest_digest,schema_version,idempotency_key,tenant_id,room_id,sitting_id,start_after,end_at,source_count,purge_generation,
		authorized_by,approval_reference,generated_at,expires_at,release_commit,rollback_floor,manifest
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17::jsonb)
	ON CONFLICT (manifest_digest) DO NOTHING`, digest, manifest.Schema, idempotencyKey, manifest.TenantID, manifest.RoomID, manifest.SittingID,
		manifest.StartAfter, manifest.EndAt, len(manifest.Sources), manifest.PurgeGeneration, manifest.AuthorizedBy, manifest.ApprovalReference,
		manifest.GeneratedAt, manifest.ExpiresAt, manifest.ReleaseCommit, manifest.RollbackFloor, raw)
	if err != nil {
		if strings.Contains(err.Error(), "ambient_intelligence_replay_one_active_sitting") {
			return AmbientReplayManifest{}, false, ErrAmbientReplayAlreadyActive
		}
		return AmbientReplayManifest{}, false, err
	}
	created := tag.RowsAffected() == 1
	if !created {
		var existingRaw []byte
		if err := tx.QueryRow(ctx, `SELECT manifest FROM ambient_intelligence_replay_manifests WHERE manifest_digest=$1`, digest).Scan(&existingRaw); err != nil {
			return AmbientReplayManifest{}, false, err
		}
		var existing AmbientReplayManifest
		if json.Unmarshal(existingRaw, &existing) != nil || existing.Digest != manifest.Digest {
			return AmbientReplayManifest{}, false, ErrAmbientReplayDrift
		}
		if err := tx.Commit(ctx); err != nil {
			return AmbientReplayManifest{}, false, err
		}
		return existing, false, nil
	}
	for ordinal, source := range manifest.Sources {
		contentDigest, _ := hex.DecodeString(source.ContentDigest)
		consentDigest, _ := hex.DecodeString(source.ConsentFenceDigest)
		if _, err := tx.Exec(ctx, `INSERT INTO ambient_intelligence_replay_sources (
			manifest_digest,ordinal,object_id,capture_sequence,content_revision,content_digest,acl_version,purge_generation,
			occurred_start,occurred_end,consent_fence_digest
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, digest, ordinal, source.ObjectID, source.CaptureSequence,
			source.ContentRevision, contentDigest, source.ACLVersion, source.PurgeGeneration, source.OccurredStart, source.OccurredEnd, consentDigest); err != nil {
			return AmbientReplayManifest{}, false, err
		}
	}
	for _, stage := range manifest.Stages {
		input := manifest.CursorDigests[stage.Name]
		if !isHexDigest(input) {
			input = manifest.SourceManifestDigest
		}
		inputDigest, _ := hex.DecodeString(input)
		if _, err := tx.Exec(ctx, `INSERT INTO ambient_intelligence_replay_cursors (
			manifest_digest,stage,cursor_namespace,through_source_sequence,input_digest,updated_at
		) VALUES ($1,$2,$3,$4,$5,$6)`, digest, stage.Name, "replay:"+manifest.Digest, manifest.StartAfter, inputDigest, manifest.GeneratedAt); err != nil {
			return AmbientReplayManifest{}, false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return AmbientReplayManifest{}, false, err
	}
	return manifest, true, nil
}

func (store *PostgresAmbientReplayStore) LoadManifest(ctx context.Context, digestText string) (AmbientReplayManifest, string, error) {
	if store == nil || store.pool == nil || !isHexDigest(digestText) {
		return AmbientReplayManifest{}, "", ErrAmbientReplayInvalid
	}
	digest, _ := hex.DecodeString(digestText)
	var raw []byte
	var status string
	if err := store.pool.QueryRow(ctx, `SELECT manifest,status FROM ambient_intelligence_replay_manifests WHERE manifest_digest=$1`, digest).Scan(&raw, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AmbientReplayManifest{}, "", ErrAmbientReplayInvalid
		}
		return AmbientReplayManifest{}, "", err
	}
	var manifest AmbientReplayManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return AmbientReplayManifest{}, "", ErrAmbientReplayDrift
	}
	return manifest, status, nil
}

func (store *PostgresAmbientReplayStore) BeginExecution(ctx context.Context, manifest AmbientReplayManifest, executionID string, now time.Time) (string, bool, error) {
	if store == nil || store.pool == nil || !isHexDigest(manifest.Digest) {
		return "", false, ErrAmbientReplayUnavailable
	}
	digest, _ := hex.DecodeString(manifest.Digest)
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var status string
	var currentID *string
	var expires time.Time
	if err := tx.QueryRow(ctx, `SELECT status,execution_id::text,expires_at FROM ambient_intelligence_replay_manifests WHERE manifest_digest=$1 FOR UPDATE`, digest).Scan(&status, &currentID, &expires); err != nil {
		return "", false, err
	}
	if status == "completed" || status == "running" {
		id := ""
		if currentID != nil {
			id = *currentID
		}
		return id, true, tx.Commit(ctx)
	}
	if status != "planned" || !now.Before(expires) {
		return "", false, ErrAmbientReplayUnauthorized
	}
	tag, err := tx.Exec(ctx, `UPDATE ambient_intelligence_replay_manifests SET status='running',execution_id=$2::uuid,
		started_at=$3,lease_expires_at=$4,last_error_code=NULL WHERE manifest_digest=$1 AND status='planned'`, digest, executionID, now, now.Add(ambientReplayExecutionLease))
	if err != nil || tag.RowsAffected() != 1 {
		return "", false, fmt.Errorf("begin replay execution: %w", err)
	}
	if len(manifest.Stages) == 0 {
		return "", false, ErrAmbientReplayInvalid
	}
	initial := make([]AmbientReplayArtifact, 0, len(manifest.Sources))
	for _, source := range manifest.Sources {
		initial = append(initial, AmbientReplayArtifact{ID: source.ObjectID, Kind: "transcript", Digest: source.ContentDigest,
			SourceManifestDigest: manifest.SourceManifestDigest, ManifestDigest: manifest.Digest})
	}
	inputDigestText, digestErr := digestAmbientReplayArtifacts(initial)
	if digestErr != nil {
		return "", false, digestErr
	}
	inputDigest, _ := hex.DecodeString(inputDigestText)
	sourceDigest, _ := hex.DecodeString(manifest.SourceManifestDigest)
	if _, err := tx.Exec(ctx, `INSERT INTO ambient_intelligence_replay_stage_receipts (
		manifest_digest,execution_id,stage,ordinal,status,input_artifact_digest,source_manifest_digest,started_at
	) VALUES ($1,$2::uuid,$3,0,'prepared',$4,$5,$6)`, digest, executionID, manifest.Stages[0].Name, inputDigest, sourceDigest, now); err != nil {
		return "", false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, err
	}
	return executionID, false, nil
}

func (store *PostgresAmbientReplayStore) RenewExecutionLease(ctx context.Context, digestText, executionID string, now, expiresAt time.Time) error {
	if store == nil || store.pool == nil || !isHexDigest(digestText) || strings.TrimSpace(executionID) == "" || !expiresAt.After(now) {
		return ErrAmbientReplayInvalid
	}
	digest, _ := hex.DecodeString(digestText)
	tag, err := store.pool.Exec(ctx, `UPDATE ambient_intelligence_replay_manifests SET lease_expires_at=$4
		WHERE manifest_digest=$1 AND execution_id=$2::uuid AND status='running' AND lease_expires_at > $3`, digest, executionID, now, expiresAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrAmbientReplayDrift
	}
	return nil
}

func (store *PostgresAmbientReplayStore) RecordStageReceipt(ctx context.Context, receipt AmbientReplayStageReceipt) error {
	if store == nil || store.pool == nil || !isHexDigest(receipt.ManifestDigest) || !isHexDigest(receipt.InputDigest) || !isHexDigest(receipt.SourceDigest) {
		return ErrAmbientReplayInvalid
	}
	manifestDigest, _ := hex.DecodeString(receipt.ManifestDigest)
	inputDigest, _ := hex.DecodeString(receipt.InputDigest)
	sourceDigest, _ := hex.DecodeString(receipt.SourceDigest)
	var outputDigest []byte
	if receipt.OutputDigest != "" {
		if !isHexDigest(receipt.OutputDigest) {
			return ErrAmbientReplayInvalid
		}
		outputDigest, _ = hex.DecodeString(receipt.OutputDigest)
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var executionMatches, leaseLive bool
	if err := tx.QueryRow(ctx, `SELECT execution_id=$2::uuid, status='running' AND lease_expires_at > now()
		FROM ambient_intelligence_replay_manifests WHERE manifest_digest=$1 FOR UPDATE`, manifestDigest, receipt.ExecutionID).Scan(&executionMatches, &leaseLive); err != nil {
		return err
	}
	if !executionMatches || !leaseLive {
		return ErrAmbientReplayDrift
	}
	tag, err := tx.Exec(ctx, `INSERT INTO ambient_intelligence_replay_stage_receipts (
		manifest_digest,execution_id,stage,ordinal,status,input_artifact_digest,output_artifact_digest,source_manifest_digest,
		calls,input_tokens,output_tokens,cost_micros,started_at,completed_at,error_code
	) VALUES ($1,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
	ON CONFLICT (manifest_digest,execution_id,stage) DO UPDATE SET
		status=EXCLUDED.status,output_artifact_digest=EXCLUDED.output_artifact_digest,calls=EXCLUDED.calls,input_tokens=EXCLUDED.input_tokens,
		output_tokens=EXCLUDED.output_tokens,cost_micros=EXCLUDED.cost_micros,completed_at=EXCLUDED.completed_at,error_code=EXCLUDED.error_code
	WHERE ambient_intelligence_replay_stage_receipts.execution_id=EXCLUDED.execution_id
		AND ambient_intelligence_replay_stage_receipts.input_artifact_digest=EXCLUDED.input_artifact_digest`, manifestDigest, receipt.ExecutionID,
		receipt.Stage, receipt.Ordinal, receipt.Status, inputDigest, outputDigest, sourceDigest, receipt.Usage.Calls, receipt.Usage.InputTokens,
		receipt.Usage.OutputTokens, receipt.Usage.CostMicros, receipt.StartedAt, nullableReplayTime(receipt.CompletedAt), nullableReplayString(receipt.ErrorCode))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrAmbientReplayDrift
	}
	if receipt.Status == "completed" {
		if len(outputDigest) != 32 {
			return ErrAmbientReplayInvalid
		}
		manifestDigestText := receipt.ManifestDigest
		tag, updateErr := tx.Exec(ctx, `UPDATE ambient_intelligence_replay_cursors SET
			through_source_sequence=(SELECT end_at FROM ambient_intelligence_replay_manifests WHERE manifest_digest=$1),
			output_digest=$3,updated_at=$4
			WHERE manifest_digest=$1 AND stage=$2 AND cursor_namespace=$5`, manifestDigest, receipt.Stage, outputDigest, receipt.CompletedAt, "replay:"+manifestDigestText)
		if updateErr != nil {
			return updateErr
		}
		if tag.RowsAffected() != 1 {
			return ErrAmbientReplayDrift
		}
	}
	return tx.Commit(ctx)
}

func (store *PostgresAmbientReplayStore) CompleteExecution(ctx context.Context, digestText, executionID, status string, at time.Time) error {
	if store == nil || store.pool == nil || !isHexDigest(digestText) || (status != "completed" && status != "failed" && status != "drifted") {
		return ErrAmbientReplayInvalid
	}
	digest, _ := hex.DecodeString(digestText)
	tag, err := store.pool.Exec(ctx, `UPDATE ambient_intelligence_replay_manifests SET status=$3,completed_at=$4,
		lease_expires_at=NULL,last_error_code=$5 WHERE manifest_digest=$1 AND execution_id=$2::uuid
		AND status='running' AND lease_expires_at > now()`,
		digest, executionID, status, at, nullableReplayString(map[bool]string{true: "", false: status}[status == "completed"]))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrAmbientReplayDrift
	}
	return nil
}

// ReclaimExpired is the crash/restart recovery boundary. It never resumes or
// re-spends an ambiguous provider stage. Expired planned manifests become
// terminal, and a running execution whose bounded lease elapsed becomes a
// terminal failed attempt with every non-terminal receipt failed closed. The
// active-sitting uniqueness fence is thereby released for a fresh, separately
// approved manifest.
func (store *PostgresAmbientReplayStore) ReclaimExpired(ctx context.Context, now time.Time) (int64, error) {
	if store == nil || store.pool == nil || now.IsZero() {
		return 0, ErrAmbientReplayUnavailable
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	rows, err := tx.Query(ctx, `SELECT manifest_digest,execution_id::text,status FROM ambient_intelligence_replay_manifests
		WHERE (status='planned' AND expires_at <= $1) OR (status='running' AND lease_expires_at <= $1)
		ORDER BY generated_at FOR UPDATE`, now)
	if err != nil {
		return 0, err
	}
	type reclaim struct {
		digest      []byte
		executionID *string
		status      string
	}
	var candidates []reclaim
	for rows.Next() {
		var row reclaim
		if err := rows.Scan(&row.digest, &row.executionID, &row.status); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, candidate := range candidates {
		if candidate.status == "planned" {
			if _, err := tx.Exec(ctx, `UPDATE ambient_intelligence_replay_manifests SET status='expired',completed_at=$2,last_error_code='authorization_expired'
				WHERE manifest_digest=$1 AND status='planned'`, candidate.digest, now); err != nil {
				return 0, err
			}
			continue
		}
		if candidate.executionID == nil || strings.TrimSpace(*candidate.executionID) == "" {
			return 0, ErrAmbientReplayDrift
		}
		if _, err := tx.Exec(ctx, `UPDATE ambient_intelligence_replay_stage_receipts SET status='failed',completed_at=$3,error_code='execution_lease_expired'
			WHERE manifest_digest=$1 AND execution_id=$2::uuid AND status IN ('prepared','running')`, candidate.digest, *candidate.executionID, now); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE ambient_intelligence_replay_manifests SET status='failed',completed_at=$3,lease_expires_at=NULL,last_error_code='execution_lease_expired'
			WHERE manifest_digest=$1 AND execution_id=$2::uuid AND status='running'`, candidate.digest, *candidate.executionID, now); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int64(len(candidates)), nil
}

func nullableReplayTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
func nullableReplayString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}
