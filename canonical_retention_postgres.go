package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresPurgeLedger is the durable restore authority. It is intentionally a
// separate repository from retention bodies: restoring an older content
// backup must never roll this ledger backward with the content it governs.
type PostgresPurgeLedger struct{ pool *pgxpool.Pool }

func NewPostgresPurgeLedger(pool *pgxpool.Pool) *PostgresPurgeLedger {
	return &PostgresPurgeLedger{pool: pool}
}

func (ledger *PostgresPurgeLedger) RecordPurge(ctx context.Context, entry PurgeLedgerEntry) error {
	if ledger == nil || ledger.pool == nil {
		return ErrRetentionRestoreGate
	}
	// PostgreSQL timestamptz has microsecond precision. Normalize before both
	// insert and retry comparison so a nanosecond-bearing first attempt remains
	// idempotent after a process restart.
	entry.PurgedAt = entry.PurgedAt.UTC().Truncate(time.Microsecond)
	if err := validatePurgeLedgerEntry(entry); err != nil {
		return err
	}
	evidence := make(map[string]string, len(entry.DestructionEvidence))
	for class, proof := range entry.DestructionEvidence {
		evidence[string(class)] = proof
	}
	rawEvidence, err := json.Marshal(evidence)
	if err != nil {
		return ErrRetentionInvalid
	}
	digest, _ := hex.DecodeString(entry.ContentDigest)
	tx, err := ledger.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin purge ledger fence: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var roomID, sittingID string
	err = tx.QueryRow(ctx, `SELECT COALESCE(room_id,''),COALESCE(meeting_id,'') FROM objects
		WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3`,
		entry.Key.TenantID, entry.Key.ObjectType, entry.Key.ObjectID).Scan(&roomID, &sittingID)
	if err != nil {
		return fmt.Errorf("resolve canonical object for purge: %w", err)
	}
	projectionKey := BrainProjectionCheckpointKey{
		TenantID: entry.Key.TenantID, ProjectorVersion: brainProjectionProjectorVersion,
		RoomID: roomID, SittingID: sittingID, SourceFamily: entry.Key.ObjectType,
	}
	if projectionKey.Validate() == nil {
		if err := lockBrainProjectionSource(ctx, tx, projectionKey); err != nil {
			return fmt.Errorf("lock purge projection source: %w", err)
		}
	}
	var lockedRoomID, lockedSittingID string
	err = tx.QueryRow(ctx, `SELECT COALESCE(room_id,''),COALESCE(meeting_id,'') FROM objects
		WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3 FOR UPDATE`,
		entry.Key.TenantID, entry.Key.ObjectType, entry.Key.ObjectID).Scan(&lockedRoomID, &lockedSittingID)
	if err != nil || lockedRoomID != roomID || lockedSittingID != sittingID {
		return fmt.Errorf("lock canonical object for purge: %w", ErrRetentionInvalid)
	}
	result, err := tx.Exec(ctx, `INSERT INTO purge_ledger (
		tenant_id,object_type,object_id,revision_id,content_sha256,policy_id,purged_at,destruction_evidence
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb) ON CONFLICT (tenant_id,object_type,object_id,revision_id) DO NOTHING`,
		entry.Key.TenantID, entry.Key.ObjectType, entry.Key.ObjectID, entry.Key.RevisionID, digest, entry.PolicyID, entry.PurgedAt.UTC(), rawEvidence)
	if err != nil {
		return fmt.Errorf("insert purge ledger: %w", err)
	}
	if result.RowsAffected() == 1 {
		if err := registerBrainProjectionScopeDurably(ctx, tx, ledger.pool, projectionKey); err != nil {
			return fmt.Errorf("register purge projection scope: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit purge ledger: %w", err)
	}
	if result.RowsAffected() == 1 {
		notifyProductionBrainProjectionScope(ledger.pool, projectionKey)
		return nil
	}
	prior, found, err := ledger.LookupPurge(ctx, entry.Key)
	if err != nil {
		return err
	}
	if !found || prior.ContentDigest != entry.ContentDigest || prior.PolicyID != entry.PolicyID || !prior.PurgedAt.Equal(entry.PurgedAt) || !equalDestructionEvidence(prior.DestructionEvidence, entry.DestructionEvidence) {
		return ErrRetentionInvalid
	}
	return nil
}

func (ledger *PostgresPurgeLedger) LookupPurge(ctx context.Context, key RetentionKey) (PurgeLedgerEntry, bool, error) {
	if ledger == nil || ledger.pool == nil {
		return PurgeLedgerEntry{}, false, ErrRetentionRestoreGate
	}
	if !validRetentionKey(key) {
		return PurgeLedgerEntry{}, false, ErrRetentionInvalid
	}
	var digest []byte
	var policyID string
	var purgedAt time.Time
	var rawEvidence []byte
	err := ledger.pool.QueryRow(ctx, `SELECT content_sha256,policy_id,purged_at,destruction_evidence
		FROM purge_ledger WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3 AND revision_id=$4`,
		key.TenantID, key.ObjectType, key.ObjectID, key.RevisionID).Scan(&digest, &policyID, &purgedAt, &rawEvidence)
	if errors.Is(err, pgx.ErrNoRows) {
		return PurgeLedgerEntry{}, false, nil
	}
	if err != nil {
		return PurgeLedgerEntry{}, false, fmt.Errorf("lookup purge ledger: %w", err)
	}
	var encoded map[string]string
	if err := json.Unmarshal(rawEvidence, &encoded); err != nil {
		return PurgeLedgerEntry{}, false, fmt.Errorf("decode purge ledger evidence: %w", err)
	}
	evidence := make(map[RetentionResourceClass]string, len(encoded))
	for class, proof := range encoded {
		evidence[RetentionResourceClass(class)] = proof
	}
	entry := PurgeLedgerEntry{Key: key, ContentDigest: hex.EncodeToString(digest), PolicyID: policyID, PurgedAt: purgedAt.UTC(), DestructionEvidence: evidence}
	if err := validatePurgeLedgerEntry(entry); err != nil {
		return PurgeLedgerEntry{}, false, fmt.Errorf("invalid persisted purge ledger row: %w", err)
	}
	return entry, true, nil
}
