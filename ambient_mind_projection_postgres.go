package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresAmbientMindProjectionStore durably records immutable shadow events
// and transactionally replaces their materialized typed graph. It is never
// wired to a product read path in E10-R2.
type PostgresAmbientMindProjectionStore struct {
	pool *pgxpool.Pool
}

func NewPostgresAmbientMindProjectionStore(pool *pgxpool.Pool) *PostgresAmbientMindProjectionStore {
	return &PostgresAmbientMindProjectionStore{pool: pool}
}

func (store *PostgresAmbientMindProjectionStore) Apply(ctx context.Context, event AmbientMindProjectionEvent) (AmbientMindProjectionSnapshot, error) {
	if store == nil || store.pool == nil || event.Validate() != nil {
		return AmbientMindProjectionSnapshot{}, ErrAmbientMindProjectionInvalid
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return AmbientMindProjectionSnapshot{}, err
	}
	defer tx.Rollback(ctx)
	if err := ambientMindProjectionLock(ctx, tx, event.TenantID); err != nil {
		return AmbientMindProjectionSnapshot{}, err
	}
	events, generation, err := loadAmbientMindProjectionEvents(ctx, tx, event.TenantID, true)
	if err != nil {
		return AmbientMindProjectionSnapshot{}, err
	}
	fingerprint, err := STRIDEContractDigest(event)
	if err != nil {
		return AmbientMindProjectionSnapshot{}, err
	}
	for _, existing := range events {
		existingFingerprint, _ := STRIDEContractDigest(existing)
		if existing.EventID == event.EventID || existing.IdempotencyKey == event.IdempotencyKey {
			if existingFingerprint != fingerprint {
				return AmbientMindProjectionSnapshot{}, ErrAmbientMindProjectionConflict
			}
			snapshot, rebuildErr := rebuildAmbientMindProjection(events, generation)
			return snapshot, rebuildErr
		}
	}
	wantSequence := uint64(len(events) + 1)
	if event.Sequence != wantSequence || (len(events) > 0 && event.SourceHighWater < events[len(events)-1].SourceHighWater) {
		return AmbientMindProjectionSnapshot{}, ErrAmbientMindProjectionGap
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return AmbientMindProjectionSnapshot{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO stride_ambient_projection_events
		(tenant_id,event_id,idempotency_key,sequence,source_high_water,operation,event_digest,event_document,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,decode($7,'hex'),$8::jsonb,$9)`, event.TenantID, event.EventID, event.IdempotencyKey,
		event.Sequence, event.SourceHighWater, event.Operation, fingerprint, raw, event.OccurredAt.UTC()); err != nil {
		return AmbientMindProjectionSnapshot{}, err
	}
	events = append(events, cloneAmbientMindEvent(event))
	snapshot, err := rebuildAmbientMindProjection(events, generation+1)
	if err != nil {
		return AmbientMindProjectionSnapshot{}, err
	}
	if err := persistAmbientMindProjectionSnapshot(ctx, tx, event.TenantID, snapshot); err != nil {
		return AmbientMindProjectionSnapshot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		if errors.Is(err, pgx.ErrTxCommitRollback) {
			return AmbientMindProjectionSnapshot{}, ErrAmbientMindProjectionConflict
		}
		return AmbientMindProjectionSnapshot{}, err
	}
	return snapshot, nil
}

func (store *PostgresAmbientMindProjectionStore) Load(ctx context.Context, tenantID string) (AmbientMindProjectionSnapshot, error) {
	if store == nil || store.pool == nil || !strideIdentifier(tenantID) {
		return AmbientMindProjectionSnapshot{}, ErrAmbientMindProjectionInvalid
	}
	events, generation, err := loadAmbientMindProjectionEvents(ctx, store.pool, tenantID, false)
	if err != nil {
		return AmbientMindProjectionSnapshot{}, err
	}
	snapshot, err := rebuildAmbientMindProjection(events, generation)
	if err != nil {
		return AmbientMindProjectionSnapshot{}, err
	}
	if len(events) == 0 {
		return snapshot, nil
	}
	var through, sourceHighWater, projectionHighWater uint64
	var sourceDigest, projectionDigest string
	var freshThrough *time.Time
	err = store.pool.QueryRow(ctx, `SELECT through_sequence,source_high_water,projection_high_water,fresh_through,
		encode(source_manifest_digest,'hex'),encode(projection_digest,'hex') FROM stride_ambient_projection_checkpoints WHERE tenant_id=$1`, tenantID).
		Scan(&through, &sourceHighWater, &projectionHighWater, &freshThrough, &sourceDigest, &projectionDigest)
	if err != nil {
		return AmbientMindProjectionSnapshot{}, err
	}
	if through != snapshot.Checkpoint.ThroughSequence || sourceHighWater != snapshot.Checkpoint.SourceHighWater || projectionHighWater != snapshot.Checkpoint.ProjectionHighWater ||
		sourceDigest != snapshot.Checkpoint.SourceManifestDigest || projectionDigest != snapshot.Checkpoint.ProjectionDigest ||
		(freshThrough == nil) != snapshot.Checkpoint.FreshThrough.IsZero() || (freshThrough != nil && !freshThrough.UTC().Equal(snapshot.Checkpoint.FreshThrough.UTC())) {
		return AmbientMindProjectionSnapshot{}, ErrAmbientMindProjectionConflict
	}
	return snapshot, nil
}

func (store *PostgresAmbientMindProjectionStore) Rebuild(ctx context.Context, tenantID string) (AmbientMindProjectionSnapshot, error) {
	if store == nil || store.pool == nil || !strideIdentifier(tenantID) {
		return AmbientMindProjectionSnapshot{}, ErrAmbientMindProjectionInvalid
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return AmbientMindProjectionSnapshot{}, err
	}
	defer tx.Rollback(ctx)
	if err := ambientMindProjectionLock(ctx, tx, tenantID); err != nil {
		return AmbientMindProjectionSnapshot{}, err
	}
	events, generation, err := loadAmbientMindProjectionEvents(ctx, tx, tenantID, true)
	if err != nil {
		return AmbientMindProjectionSnapshot{}, err
	}
	snapshot, err := rebuildAmbientMindProjection(events, generation+1)
	if err != nil {
		return AmbientMindProjectionSnapshot{}, err
	}
	if err := persistAmbientMindProjectionSnapshot(ctx, tx, tenantID, snapshot); err != nil {
		return AmbientMindProjectionSnapshot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AmbientMindProjectionSnapshot{}, err
	}
	return snapshot, nil
}

func (store *PostgresAmbientMindProjectionStore) QueryForPrincipal(ctx context.Context, tenantID, principal string) ([]AmbientMindProjectionNode, AmbientMindProjectionCheckpoint, error) {
	if !strideIdentifier(principal) {
		return nil, AmbientMindProjectionCheckpoint{}, ErrAmbientMindProjectionDenied
	}
	snapshot, err := store.Load(ctx, tenantID)
	if err != nil {
		return nil, AmbientMindProjectionCheckpoint{}, err
	}
	projector, err := RestoreAmbientMindProjector(snapshot)
	if err != nil {
		return nil, AmbientMindProjectionCheckpoint{}, err
	}
	return projector.QueryForPrincipal(tenantID, principal)
}

type ambientMindProjectionQuery interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func ambientMindProjectionLock(ctx context.Context, tx pgx.Tx, tenantID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('stride-ambient-projection:' || $1, 0))`, tenantID)
	return err
}

func loadAmbientMindProjectionEvents(ctx context.Context, query ambientMindProjectionQuery, tenantID string, lock bool) ([]AmbientMindProjectionEvent, uint64, error) {
	generation := uint64(0)
	statement := `SELECT generation FROM stride_ambient_projection_checkpoints WHERE tenant_id=$1`
	if lock {
		statement += ` FOR UPDATE`
	}
	err := query.QueryRow(ctx, statement, tenantID).Scan(&generation)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, err
	}
	rows, err := query.Query(ctx, `SELECT event_document::text FROM stride_ambient_projection_events WHERE tenant_id=$1 ORDER BY sequence`, tenantID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	events := []AmbientMindProjectionEvent{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, 0, err
		}
		var event AmbientMindProjectionEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil || event.TenantID != tenantID {
			return nil, 0, ErrAmbientMindProjectionConflict
		}
		events = append(events, event)
	}
	return events, generation, rows.Err()
}

func persistAmbientMindProjectionSnapshot(ctx context.Context, tx pgx.Tx, tenantID string, snapshot AmbientMindProjectionSnapshot) error {
	// Immutable source and node rows remain durable. Only their derived state
	// selector and checkpoint are replaced during a deterministic rebuild.
	for _, source := range snapshot.Sources {
		audienceDigest, _ := STRIDEContractDigest(source.Audience)
		principals, _ := json.Marshal(source.Audience.Principals)
		command, err := tx.Exec(ctx, `INSERT INTO stride_ambient_projection_sources
			(tenant_id,source_contract_type,source_contract_id,source_revision,source_digest,visibility,audience_principals,audience_digest,acl_version,source_high_water,fresh_through)
			VALUES ($1,$2,$3,$4,decode($5,'hex'),$6,$7::jsonb,decode($8,'hex'),$9,$10,$11) ON CONFLICT DO NOTHING`,
			tenantID, source.Ref.ContractType, source.Ref.ID, source.Ref.Revision, source.Ref.Digest, source.Audience.Visibility, principals, audienceDigest,
			source.ACLVersion, source.SourceHighWater, source.FreshThrough.UTC())
		if err != nil || command.RowsAffected() != 1 {
			if err != nil {
				return err
			}
			var exact bool
			err = tx.QueryRow(ctx, `SELECT visibility=$6 AND audience_principals=$7::jsonb AND encode(audience_digest,'hex')=$8 AND acl_version=$9 AND source_high_water=$10 AND fresh_through=$11
				FROM stride_ambient_projection_sources WHERE tenant_id=$1 AND source_contract_type=$2 AND source_contract_id=$3 AND source_revision=$4 AND encode(source_digest,'hex')=$5`,
				tenantID, source.Ref.ContractType, source.Ref.ID, source.Ref.Revision, source.Ref.Digest, source.Audience.Visibility, principals, audienceDigest,
				source.ACLVersion, source.SourceHighWater, source.FreshThrough.UTC()).Scan(&exact)
			if err != nil || !exact {
				return ErrAmbientMindProjectionConflict
			}
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM stride_ambient_projection_node_states WHERE tenant_id=$1`, tenantID); err != nil {
		return err
	}
	for _, state := range snapshot.Nodes {
		node := state.Node
		audienceDigest, _ := STRIDEContractDigest(node.Audience)
		principals, _ := json.Marshal(node.Audience.Principals)
		var supersedesType, supersedesID, supersedesRevision, supersedesDigest any
		if node.SupersedesRef != nil {
			supersedesType, supersedesID, supersedesRevision, supersedesDigest = node.SupersedesRef.ContractType, node.SupersedesRef.ID, node.SupersedesRef.Revision, node.SupersedesRef.Digest
		}
		command, err := tx.Exec(ctx, `INSERT INTO stride_ambient_projection_nodes
			(tenant_id,node_contract_type,node_id,node_revision,node_digest,logical_id,kind,visibility,audience_principals,audience_digest,acl_version,source_high_water,fresh_through,supersedes_contract_type,supersedes_id,supersedes_revision,supersedes_digest)
			VALUES ($1,$2,$3,$4,decode($5,'hex'),$6,$7,$8,$9::jsonb,decode($10,'hex'),$11,$12,$13,$14,$15,$16,
			CASE WHEN $17::text IS NULL THEN NULL ELSE decode($17,'hex') END) ON CONFLICT DO NOTHING`,
			tenantID, node.Ref.ContractType, node.Ref.ID, node.Ref.Revision, node.Ref.Digest, node.LogicalID, node.Kind, node.Audience.Visibility,
			principals, audienceDigest, node.ACLVersion, node.SourceHighWater, node.FreshThrough.UTC(), supersedesType, supersedesID, supersedesRevision, supersedesDigest)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			var exact bool
			err = tx.QueryRow(ctx, `SELECT logical_id=$6 AND kind=$7 AND visibility=$8 AND audience_principals=$9::jsonb AND
				encode(audience_digest,'hex')=$10 AND acl_version=$11 AND source_high_water=$12 AND fresh_through=$13 AND
				supersedes_contract_type IS NOT DISTINCT FROM $14::text AND supersedes_id IS NOT DISTINCT FROM $15::text AND
				supersedes_revision IS NOT DISTINCT FROM $16::bigint AND
				encode(supersedes_digest,'hex') IS NOT DISTINCT FROM $17::text
				FROM stride_ambient_projection_nodes
				WHERE tenant_id=$1 AND node_contract_type=$2 AND node_id=$3 AND node_revision=$4 AND encode(node_digest,'hex')=$5`,
				tenantID, node.Ref.ContractType, node.Ref.ID, node.Ref.Revision, node.Ref.Digest, node.LogicalID, node.Kind, node.Audience.Visibility,
				principals, audienceDigest, node.ACLVersion, node.SourceHighWater, node.FreshThrough.UTC(), supersedesType, supersedesID, supersedesRevision, supersedesDigest).Scan(&exact)
			if err != nil || !exact {
				return ErrAmbientMindProjectionConflict
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO stride_ambient_projection_node_states
			(tenant_id,node_contract_type,node_id,node_revision,node_digest,logical_id,status,reason,projection_high_water)
			VALUES ($1,$2,$3,$4,decode($5,'hex'),$6,$7,$8,$9)`, tenantID, node.Ref.ContractType, node.Ref.ID, node.Ref.Revision,
			node.Ref.Digest, node.LogicalID, state.Status, nullableAmbientMindString(state.Reason), snapshot.Checkpoint.ProjectionHighWater); err != nil {
			return err
		}
		for _, source := range node.SourceRefs {
			if _, err := tx.Exec(ctx, `INSERT INTO stride_ambient_projection_source_edges
				(tenant_id,source_contract_type,source_contract_id,source_revision,source_digest,node_contract_type,node_id,node_revision,node_digest)
				VALUES ($1,$2,$3,$4,decode($5,'hex'),$6,$7,$8,decode($9,'hex')) ON CONFLICT DO NOTHING`, tenantID, source.ContractType,
				source.ID, source.Revision, source.Digest, node.Ref.ContractType, node.Ref.ID, node.Ref.Revision, node.Ref.Digest); err != nil {
				return err
			}
		}
		for _, parent := range node.ParentRefs {
			if _, err := tx.Exec(ctx, `INSERT INTO stride_ambient_projection_node_edges
				(tenant_id,parent_contract_type,parent_id,parent_revision,parent_digest,child_contract_type,child_id,child_revision,child_digest)
				VALUES ($1,$2,$3,$4,decode($5,'hex'),$6,$7,$8,decode($9,'hex')) ON CONFLICT DO NOTHING`, tenantID, parent.ContractType,
				parent.ID, parent.Revision, parent.Digest, node.Ref.ContractType, node.Ref.ID, node.Ref.Revision, node.Ref.Digest); err != nil {
				return err
			}
		}
	}
	var freshThrough any
	if !snapshot.Checkpoint.FreshThrough.IsZero() {
		freshThrough = snapshot.Checkpoint.FreshThrough.UTC()
	}
	_, err := tx.Exec(ctx, `INSERT INTO stride_ambient_projection_checkpoints
		(tenant_id,generation,through_sequence,source_high_water,projection_high_water,fresh_through,source_manifest_digest,projection_digest,rebuilt_at)
		VALUES ($1,$2,$3,$4,$5,$6,decode($7,'hex'),decode($8,'hex'),now())
		ON CONFLICT (tenant_id) DO UPDATE SET generation=EXCLUDED.generation,through_sequence=EXCLUDED.through_sequence,source_high_water=EXCLUDED.source_high_water,
		projection_high_water=EXCLUDED.projection_high_water,fresh_through=EXCLUDED.fresh_through,source_manifest_digest=EXCLUDED.source_manifest_digest,
		projection_digest=EXCLUDED.projection_digest,rebuilt_at=EXCLUDED.rebuilt_at`, tenantID, snapshot.Checkpoint.Generation,
		snapshot.Checkpoint.ThroughSequence, snapshot.Checkpoint.SourceHighWater, snapshot.Checkpoint.ProjectionHighWater, freshThrough,
		snapshot.Checkpoint.SourceManifestDigest, snapshot.Checkpoint.ProjectionDigest)
	return err
}

func nullableAmbientMindString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func ambientMindProjectionStoreStatus(snapshot AmbientMindProjectionSnapshot) string {
	return fmt.Sprintf("generation=%d source=%d projection=%d", snapshot.Checkpoint.Generation, snapshot.Checkpoint.SourceHighWater, snapshot.Checkpoint.ProjectionHighWater)
}
