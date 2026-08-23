package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const canonicalMigrationAdvisoryLock int64 = 0x426f6e6669726532 // "Bonfire2"

const (
	canonicalMigrationMaxVersionEnv   = "BONFIRE_CANONICAL_MIGRATION_MAX_VERSION"
	canonicalAllowFutureMigrationsEnv = "BONFIRE_CANONICAL_ALLOW_FUTURE_MIGRATIONS"
)

func canonicalMigrationRuntimePolicy(highestEmbedded int64) (int64, bool, error) {
	maxVersion := highestEmbedded
	if raw := strings.TrimSpace(os.Getenv(canonicalMigrationMaxVersionEnv)); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 || value > highestEmbedded {
			return 0, false, fmt.Errorf("invalid %s", canonicalMigrationMaxVersionEnv)
		}
		maxVersion = value
	}
	allowFuture := false
	if raw := strings.TrimSpace(os.Getenv(canonicalAllowFutureMigrationsEnv)); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return 0, false, fmt.Errorf("invalid %s", canonicalAllowFutureMigrationsEnv)
		}
		allowFuture = value
	}
	return maxVersion, allowFuture, nil
}

var (
	ErrCanonicalMigrationDrift   = errors.New("canonical migration checksum drift")
	ErrCanonicalUnknownMigration = errors.New("database contains canonical migration unknown to this binary")
	ErrCanonicalStoreUnhealthy   = errors.New("canonical PostgreSQL store is unhealthy")
)

type PostgresCanonicalStore struct {
	pool                 *pgxpool.Pool
	registry             *CanonicalPayloadRegistry
	Failpoint            func(string) error
	replyMediaReceiptTTL time.Duration
}

func (store *PostgresCanonicalStore) projectChatReplyMediaReceiptTTL() time.Duration {
	if store != nil && store.replyMediaReceiptTTL > 0 {
		return store.replyMediaReceiptTTL
	}
	return 30 * time.Minute
}

func OpenPostgresCanonicalStore(ctx context.Context, databaseURL string, registry *CanonicalPayloadRegistry) (*PostgresCanonicalStore, error) {
	config, err := pgxpool.ParseConfig(strings.TrimSpace(databaseURL))
	if err != nil {
		return nil, fmt.Errorf("parse canonical database URL: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open canonical PostgreSQL pool: %w", err)
	}
	store := &PostgresCanonicalStore{pool: pool, registry: registry}
	if err := store.Health(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func NewPostgresCanonicalStore(pool *pgxpool.Pool, registry *CanonicalPayloadRegistry) *PostgresCanonicalStore {
	return &PostgresCanonicalStore{pool: pool, registry: registry}
}

func (store *PostgresCanonicalStore) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

func (store *PostgresCanonicalStore) Health(ctx context.Context) error {
	if store == nil || store.pool == nil {
		return ErrCanonicalStoreUnhealthy
	}
	var one int
	if err := store.pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		return fmt.Errorf("%w: ping failed: %v", ErrCanonicalStoreUnhealthy, err)
	}
	return nil
}

// ApplyMigrations serializes all application instances on one session-level
// advisory lock. An already-applied version must have the byte-identical
// embedded checksum; edited historical SQL is refused rather than replayed.
func (store *PostgresCanonicalStore) ApplyMigrations(ctx context.Context) error {
	if err := store.Health(ctx); err != nil {
		return err
	}
	migrations, err := loadCanonicalMigrations()
	if err != nil {
		return err
	}
	if len(migrations) == 0 {
		return errors.New("canonical migration inventory is empty")
	}
	highestEmbedded := migrations[len(migrations)-1].Version
	maxVersion, allowFuture, err := canonicalMigrationRuntimePolicy(highestEmbedded)
	if err != nil {
		return err
	}
	conn, err := store.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire canonical migration connection: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", canonicalMigrationAdvisoryLock); err != nil {
		return fmt.Errorf("lock canonical migrations: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", canonicalMigrationAdvisoryLock)
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version bigint PRIMARY KEY,
		sha256 bytea NOT NULL CHECK (octet_length(sha256) = 32),
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("bootstrap canonical migration ledger: %w", err)
	}
	embeddedVersions := make(map[int64]struct{}, len(migrations))
	for _, migration := range migrations {
		embeddedVersions[migration.Version] = struct{}{}
	}
	versionRows, err := conn.Query(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return fmt.Errorf("list applied canonical migrations: %w", err)
	}
	var appliedVersions []int64
	for versionRows.Next() {
		var version int64
		if err := versionRows.Scan(&version); err != nil {
			versionRows.Close()
			return fmt.Errorf("scan applied canonical migration: %w", err)
		}
		appliedVersions = append(appliedVersions, version)
	}
	if err := versionRows.Err(); err != nil {
		versionRows.Close()
		return fmt.Errorf("list applied canonical migrations: %w", err)
	}
	versionRows.Close()
	for _, version := range appliedVersions {
		if _, known := embeddedVersions[version]; !known && !(allowFuture && version > highestEmbedded) {
			return fmt.Errorf("%w: version %d", ErrCanonicalUnknownMigration, version)
		}
	}
	for _, migration := range migrations {
		if migration.Version > maxVersion {
			continue
		}
		var stored []byte
		err := conn.QueryRow(ctx, "SELECT sha256 FROM schema_migrations WHERE version=$1", migration.Version).Scan(&stored)
		if err == nil {
			if len(stored) != sha256.Size || !equalBytes(stored, migration.SHA256[:]) {
				return fmt.Errorf("%w: version %d (%s)", ErrCanonicalMigrationDrift, migration.Version, migration.Name)
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read canonical migration %d: %w", migration.Version, err)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin canonical migration %d: %w", migration.Version, err)
		}
		if _, err := tx.Exec(ctx, migrationBody(migration.SQL)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply canonical migration %d: %w", migration.Version, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations(version, sha256) VALUES ($1,$2)", migration.Version, migration.SHA256[:]); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record canonical migration %d: %w", migration.Version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit canonical migration %d: %w", migration.Version, err)
		}
	}
	return nil
}

func migrationBody(sql string) string {
	body := strings.TrimSpace(sql)
	if strings.HasPrefix(strings.ToUpper(body), "BEGIN;") {
		body = strings.TrimSpace(body[len("BEGIN;"):])
	}
	if strings.HasSuffix(strings.ToUpper(body), "COMMIT;") {
		body = strings.TrimSpace(body[:len(body)-len("COMMIT;")])
	}
	return body
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var different byte
	for i := range left {
		different |= left[i] ^ right[i]
	}
	return different == 0
}

func (store *PostgresCanonicalStore) Append(ctx context.Context, event CanonicalEvent) (CanonicalAppendResult, error) {
	if store == nil || store.pool == nil {
		return CanonicalAppendResult{}, ErrCanonicalStoreUnhealthy
	}
	event = normalizeCanonicalPostgresEvent(event)
	if err := event.Validate(store.registry); err != nil {
		return CanonicalAppendResult{}, err
	}
	if existing, found, err := store.findRetry(ctx, event); err != nil {
		return CanonicalAppendResult{}, err
	} else if found {
		notifyProductionBrainProjectionCanonicalEvent(store, existing.Event)
		return existing, nil
	}

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return CanonicalAppendResult{}, fmt.Errorf("begin canonical append: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	projectionKey := brainProjectionKeyForCanonicalEvent(event)
	if projectionKey.Validate() == nil {
		if err := lockBrainProjectionSource(ctx, tx, projectionKey); err != nil {
			return CanonicalAppendResult{}, fmt.Errorf("lock canonical projection source: %w", err)
		}
	}
	sequence, err := insertCanonicalEvent(ctx, tx, event)
	if err != nil {
		_ = tx.Rollback(ctx)
		if isUniqueViolation(err) {
			if existing, found, retryErr := store.findRetry(ctx, event); retryErr != nil {
				return CanonicalAppendResult{}, retryErr
			} else if found {
				notifyProductionBrainProjectionCanonicalEvent(store, existing.Event)
				return existing, nil
			}
			return CanonicalAppendResult{}, ErrCanonicalAggregateConflict
		}
		return CanonicalAppendResult{}, fmt.Errorf("insert canonical event: %w", err)
	}
	if store.Failpoint != nil {
		if err := store.Failpoint("after_event_before_projection"); err != nil {
			return CanonicalAppendResult{}, err
		}
	}
	if err := projectCanonicalEvent(ctx, tx, event, sequence); err != nil {
		return CanonicalAppendResult{}, err
	}
	outboxPayload, err := json.Marshal(event)
	if err != nil {
		return CanonicalAppendResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox(event_id, topic, payload) VALUES ($1,$2,$3::jsonb)`, event.EventID, event.EventType, outboxPayload); err != nil {
		return CanonicalAppendResult{}, fmt.Errorf("insert canonical outbox: %w", err)
	}
	if err := registerBrainProjectionScopeDurably(ctx, tx, store.pool, projectionKey); err != nil {
		return CanonicalAppendResult{}, fmt.Errorf("register brain projection scope: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return CanonicalAppendResult{}, fmt.Errorf("commit canonical append: %w", err)
	}
	notifyProductionBrainProjectionCanonicalEvent(store, event)
	return CanonicalAppendResult{Event: cloneCanonicalEvent(event)}, nil
}

func insertCanonicalEvent(ctx context.Context, tx pgx.Tx, event CanonicalEvent) (int64, error) {
	var sequence int64
	err := tx.QueryRow(ctx, `INSERT INTO canonical_events (
		event_id,tenant_id,aggregate_type,aggregate_id,aggregate_version,event_type,schema_version,
		occurred_at,recorded_at,actor_type,actor_id,room_id,meeting_id,correlation_id,causation_id,
		idempotency_key,classification,consent_snapshot_id,acl_version,payload,content_ref,payload_sha256,retain_until
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,''),NULLIF($13,''),NULLIF($14,''),$15,
		NULLIF($16,''),$17,$18,$19,$20::jsonb,NULLIF($21,''),$22,$23) RETURNING sequence`,
		event.EventID, event.TenantID, event.AggregateType, event.AggregateID, event.AggregateVersion,
		event.EventType, event.SchemaVersion, event.OccurredAt, event.RecordedAt, event.Actor.Kind, event.Actor.ID,
		event.RoomID, event.MeetingID, event.CorrelationID, event.CausationID, event.IdempotencyKey,
		event.Classification, event.ConsentSnapshotID, event.ACLVersion, []byte(event.Payload), event.ContentRef,
		event.PayloadSHA256[:], event.RetainUntil).Scan(&sequence)
	return sequence, err
}

func projectCanonicalEvent(ctx context.Context, tx pgx.Tx, event CanonicalEvent, sequence int64) error {
	contentRevision, contentDigest, err := canonicalEventContentBinding(event)
	if err != nil {
		return err
	}
	deleted := canonicalDeletionEvent(event.EventType)
	if event.EventType == canonicalLegacyImportEventType {
		var imported struct {
			Deleted bool `json:"deleted"`
		}
		if err := json.Unmarshal(event.Payload, &imported); err != nil {
			return fmt.Errorf("decode imported lifecycle state: %w", err)
		}
		deleted = imported.Deleted
	}
	var deletedAt any
	if deleted {
		deletedAt = event.OccurredAt
	}
	roomID := NormalizeCanonicalRoomID(event.RoomID)
	expectedProjectionRevision := event.AggregateVersion - 1
	if event.AggregateVersion != 1 {
		var currentRevision, lastEventSequence int64
		err := tx.QueryRow(ctx, `SELECT state_revision,last_event_sequence FROM objects WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3 FOR UPDATE`,
			event.TenantID, event.AggregateType, event.AggregateID).Scan(&currentRevision, &lastEventSequence)
		_, legacyCheckpoint := canonicalLegacyImportBaselineInvariant(event)
		switch {
		case errors.Is(err, pgx.ErrNoRows) && legacyCheckpoint:
			expectedProjectionRevision = 0
		case err != nil:
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrCanonicalProjectionOrder
			}
			return fmt.Errorf("check canonical projection predecessor: %w", err)
		case currentRevision == event.AggregateVersion-1:
			expectedProjectionRevision = currentRevision
		case legacyCheckpoint && currentRevision < event.AggregateVersion:
			previous, previousErr := queryCanonicalEvent(ctx, tx, "sequence=$1", lastEventSequence)
			if previousErr != nil {
				return fmt.Errorf("load canonical checkpoint predecessor: %w", previousErr)
			}
			if _, previousWasLegacyCheckpoint := canonicalLegacyImportBaselineInvariant(previous); !previousWasLegacyCheckpoint {
				return ErrCanonicalProjectionOrder
			}
			// Preserve the exact prior revision in the UPDATE predicate so a
			// concurrent writer cannot turn this explicit legacy gap into a
			// stale overwrite.
			expectedProjectionRevision = currentRevision
		default:
			return ErrCanonicalProjectionOrder
		}
	}
	commandTag, err := tx.Exec(ctx, `INSERT INTO objects (
		tenant_id,object_type,object_id,state_revision,content_revision,room_id,meeting_id,classification,
		state,content_sha256,acl_version,last_event_sequence,deleted_at,retain_until
	) VALUES ($1,$2,$3,$4,COALESCE($5,0),$6,NULLIF($7,''),$8,$9::jsonb,$10,$11,$12,$13,$14)
	ON CONFLICT (tenant_id,object_type,object_id) DO UPDATE SET
		state_revision=EXCLUDED.state_revision,
		content_revision=CASE WHEN $5::bigint IS NULL THEN objects.content_revision ELSE $5 END,
		room_id=EXCLUDED.room_id, meeting_id=EXCLUDED.meeting_id, classification=EXCLUDED.classification,
		state=objects.state || EXCLUDED.state,
		content_sha256=CASE WHEN $5::bigint IS NULL THEN objects.content_sha256 ELSE $10 END,
		acl_version=EXCLUDED.acl_version,
		last_event_sequence=EXCLUDED.last_event_sequence, deleted_at=EXCLUDED.deleted_at, retain_until=EXCLUDED.retain_until
	WHERE objects.state_revision = $15`,
		event.TenantID, event.AggregateType, event.AggregateID, event.AggregateVersion, contentRevision, roomID, event.MeetingID,
		event.Classification, []byte(event.Payload), contentDigest, event.ACLVersion, sequence, deletedAt, event.RetainUntil,
		expectedProjectionRevision)
	if err != nil {
		return fmt.Errorf("project canonical event: %w", err)
	}
	if commandTag.RowsAffected() != 1 {
		return ErrCanonicalProjectionOrder
	}
	return nil
}

// PostgreSQL timestamptz has microsecond precision. Normalize before every
// semantic comparison and write so a successful first append and its exact
// retry have byte-identical fingerprints instead of differing only because the
// database discarded sub-microsecond precision.
func normalizeCanonicalPostgresEvent(event CanonicalEvent) CanonicalEvent {
	event.RoomID = NormalizeCanonicalRoomID(event.RoomID)
	event.MeetingID = strings.TrimSpace(event.MeetingID)
	event.OccurredAt = canonicalPostgresTime(event.OccurredAt)
	event.RecordedAt = canonicalPostgresTime(event.RecordedAt)
	if event.RetainUntil != nil {
		normalized := canonicalPostgresTime(*event.RetainUntil)
		event.RetainUntil = &normalized
	}
	return event
}

func canonicalPostgresTime(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return value.UTC().Truncate(time.Microsecond)
}

// canonicalEventContentBinding separates immutable content identity from the
// metadata event payload's own checksum. State-only events carry neither field
// and preserve the object's existing content binding.
func canonicalEventContentBinding(event CanonicalEvent) (*int64, []byte, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return nil, nil, fmt.Errorf("decode canonical content binding: %w", err)
	}
	revisionRaw, hasRevision := payload["content_revision"]
	digestRaw, hasDigest := payload["content_sha256"]
	if !hasRevision && !hasDigest {
		return nil, nil, nil
	}
	if !hasRevision || !hasDigest {
		return nil, nil, fmt.Errorf("%w: content revision and digest must appear together", ErrCanonicalInvalidEvent)
	}
	var revision int64
	var digestText string
	if err := json.Unmarshal(revisionRaw, &revision); err != nil || revision < 1 {
		return nil, nil, fmt.Errorf("%w: invalid content revision", ErrCanonicalInvalidEvent)
	}
	if err := json.Unmarshal(digestRaw, &digestText); err != nil || !isHexDigest(digestText) {
		return nil, nil, fmt.Errorf("%w: invalid content digest", ErrCanonicalInvalidEvent)
	}
	digest, _ := hex.DecodeString(digestText)
	return &revision, digest, nil
}

func (store *PostgresCanonicalStore) findRetry(ctx context.Context, event CanonicalEvent) (CanonicalAppendResult, bool, error) {
	if existing, err := queryCanonicalEvent(ctx, store.pool, "event_id=$1", event.EventID); err == nil {
		return compareCanonicalRetry(existing, event)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return CanonicalAppendResult{}, false, err
	}
	if event.IdempotencyKey != "" {
		if existing, err := queryCanonicalEvent(ctx, store.pool, "tenant_id=$1 AND idempotency_key=$2", event.TenantID, event.IdempotencyKey); err == nil {
			return compareCanonicalRetry(existing, event)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return CanonicalAppendResult{}, false, err
		}
	}
	var exists bool
	err := store.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM canonical_events WHERE tenant_id=$1 AND aggregate_type=$2 AND aggregate_id=$3 AND aggregate_version=$4)`,
		event.TenantID, event.AggregateType, event.AggregateID, event.AggregateVersion).Scan(&exists)
	if err != nil {
		return CanonicalAppendResult{}, false, err
	}
	if exists {
		return CanonicalAppendResult{}, false, ErrCanonicalAggregateConflict
	}
	return CanonicalAppendResult{}, false, nil
}

func compareCanonicalRetry(existing, candidate CanonicalEvent) (CanonicalAppendResult, bool, error) {
	equal, err := canonicalEventsIdempotentlyEqual(existing, candidate)
	if err != nil {
		return CanonicalAppendResult{}, false, err
	}
	if !equal {
		return CanonicalAppendResult{}, false, ErrCanonicalIdempotencyConflict
	}
	return CanonicalAppendResult{Event: existing, Existing: true}, true, nil
}

type canonicalRowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type canonicalRowScanner interface {
	Scan(...any) error
}

const canonicalEventSelectColumns = `event_id::text,tenant_id,aggregate_type,aggregate_id,aggregate_version,event_type,schema_version,
	occurred_at,recorded_at,actor_type,actor_id,COALESCE(room_id,''),COALESCE(meeting_id,''),COALESCE(correlation_id,''),
	causation_id::text,COALESCE(idempotency_key,''),classification,consent_snapshot_id::text,acl_version,
	payload::text,COALESCE(content_ref,''),payload_sha256,retain_until`

func queryCanonicalEvent(ctx context.Context, queryer canonicalRowQuerier, where string, args ...any) (CanonicalEvent, error) {
	query := `SELECT ` + canonicalEventSelectColumns + ` FROM canonical_events WHERE ` + where + ` ORDER BY sequence LIMIT 1`
	return scanCanonicalEvent(queryer.QueryRow(ctx, query, args...))
}

func scanCanonicalEvent(row canonicalRowScanner) (CanonicalEvent, error) {
	var event CanonicalEvent
	var eventID string
	var causation, consent pgtype.Text
	var payload string
	var digest []byte
	var retain pgtype.Timestamptz
	err := row.Scan(
		&eventID, &event.TenantID, &event.AggregateType, &event.AggregateID, &event.AggregateVersion, &event.EventType,
		&event.SchemaVersion, &event.OccurredAt, &event.RecordedAt, &event.Actor.Kind, &event.Actor.ID, &event.RoomID,
		&event.MeetingID, &event.CorrelationID, &causation, &event.IdempotencyKey, &event.Classification, &consent,
		&event.ACLVersion, &payload, &event.ContentRef, &digest, &retain)
	if err != nil {
		return CanonicalEvent{}, err
	}
	event.EventID, err = uuid.Parse(eventID)
	if err != nil {
		return CanonicalEvent{}, err
	}
	if causation.Valid {
		parsed, parseErr := uuid.Parse(causation.String)
		if parseErr != nil {
			return CanonicalEvent{}, parseErr
		}
		event.CausationID = &parsed
	}
	if consent.Valid {
		parsed, parseErr := uuid.Parse(consent.String)
		if parseErr != nil {
			return CanonicalEvent{}, parseErr
		}
		event.ConsentSnapshotID = &parsed
	}
	decoder := json.NewDecoder(bytes.NewBufferString(payload))
	decoder.UseNumber()
	var payloadValue any
	if err := decoder.Decode(&payloadValue); err != nil {
		return CanonicalEvent{}, fmt.Errorf("decode stored canonical payload: %w", err)
	}
	normalizedPayload, err := canonicalJSON(payloadValue)
	if err != nil {
		return CanonicalEvent{}, fmt.Errorf("normalize stored canonical payload: %w", err)
	}
	event.Payload = json.RawMessage(normalizedPayload)
	if len(digest) != sha256.Size {
		return CanonicalEvent{}, fmt.Errorf("canonical event has invalid payload digest")
	}
	copy(event.PayloadSHA256[:], digest)
	if retain.Valid {
		t := retain.Time.UTC()
		event.RetainUntil = &t
	}
	// PostgreSQL decodes timestamptz in the session's local location. Canonical
	// fingerprints encode time.Time, so normalize the same instant back to UTC.
	event.OccurredAt = event.OccurredAt.UTC()
	event.RecordedAt = event.RecordedAt.UTC()
	return event, nil
}

func (store *PostgresCanonicalStore) Events(ctx context.Context) ([]CanonicalEvent, error) {
	if store == nil || store.pool == nil {
		return nil, ErrCanonicalStoreUnhealthy
	}
	rows, err := store.pool.Query(ctx, `SELECT `+canonicalEventSelectColumns+` FROM canonical_events ORDER BY sequence`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]CanonicalEvent, 0)
	for rows.Next() {
		event, err := scanCanonicalEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

const canonicalImportPrefetchBatchSize = 2048

// ApplyCanonicalImportPlan preserves Append's validation and conflict rules,
// but avoids re-running its point lookups for every deterministic import event
// already present. The bounded set read is tenant-scoped; missing events still
// travel through Append so concurrent writers and aggregate/idempotency
// conflicts retain the same transactional behavior.
func (store *PostgresCanonicalStore) ApplyCanonicalImportPlan(ctx context.Context, plan CanonicalImportPlan) error {
	if store == nil || store.pool == nil {
		return ErrCanonicalStoreUnhealthy
	}
	if strings.TrimSpace(plan.TenantID) == "" {
		return errors.New("canonical import plan tenant is required")
	}
	candidates := make([]CanonicalEvent, 0, len(plan.Events))
	candidatesByID := make(map[uuid.UUID]CanonicalEvent, len(plan.Events))
	for _, raw := range plan.Events {
		event := normalizeCanonicalPostgresEvent(raw)
		if event.TenantID != plan.TenantID {
			return fmt.Errorf("canonical import event %s tenant mismatch", event.EventID)
		}
		if err := event.Validate(store.registry); err != nil {
			return fmt.Errorf("validate canonical import %s/%s v%d: %w", event.AggregateType, event.AggregateID, event.AggregateVersion, err)
		}
		if prior, duplicate := candidatesByID[event.EventID]; duplicate {
			equal, err := canonicalEventsIdempotentlyEqual(prior, event)
			if err != nil {
				return err
			}
			if !equal {
				return fmt.Errorf("canonical import event %s: %w", event.EventID, ErrCanonicalIdempotencyConflict)
			}
			continue
		}
		candidatesByID[event.EventID] = event
		candidates = append(candidates, event)
	}
	if len(candidates) == 0 {
		return nil
	}

	existingByID := make(map[uuid.UUID]CanonicalEvent, len(candidates))
	for start := 0; start < len(candidates); start += canonicalImportPrefetchBatchSize {
		end := start + canonicalImportPrefetchBatchSize
		if end > len(candidates) {
			end = len(candidates)
		}
		ids := make([]uuid.UUID, end-start)
		for index := start; index < end; index++ {
			ids[index-start] = candidates[index].EventID
		}
		rows, err := store.pool.Query(ctx, `SELECT `+canonicalEventSelectColumns+`
			FROM canonical_events WHERE tenant_id=$1 AND event_id=ANY($2::uuid[]) ORDER BY sequence`, plan.TenantID, ids)
		if err != nil {
			return fmt.Errorf("prefetch canonical import events: %w", err)
		}
		for rows.Next() {
			event, scanErr := scanCanonicalEvent(rows)
			if scanErr != nil {
				rows.Close()
				return fmt.Errorf("scan canonical import event: %w", scanErr)
			}
			existingByID[event.EventID] = event
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("prefetch canonical import events: %w", err)
		}
		rows.Close()
	}

	for _, event := range candidates {
		if existing, found := existingByID[event.EventID]; found {
			equal, err := canonicalEventsIdempotentlyEqual(existing, event)
			if err != nil {
				return err
			}
			if !equal {
				return fmt.Errorf("canonical import %s/%s v%d event %s: %w", event.AggregateType, event.AggregateID, event.AggregateVersion, event.EventID, ErrCanonicalIdempotencyConflict)
			}
			continue
		}
		if _, err := store.Append(ctx, event); err != nil {
			return fmt.Errorf("append canonical import %s/%s v%d event %s: %w", event.AggregateType, event.AggregateID, event.AggregateVersion, event.EventID, err)
		}
	}
	return nil
}

type canonicalImportObjectRef struct {
	family   string
	objectID string
}

type canonicalImportProjectionState struct {
	stateRevision   int64
	aclVersion      int64
	contentRevision int64
}

type canonicalExpectedImportGrant struct {
	id          uuid.UUID
	objectType  string
	objectID    string
	aclVersion  int64
	revision    *int64
	subjectType string
	subjectID   string
	action      string
	roomID      string
}

func (grant canonicalExpectedImportGrant) revisionValue() any {
	if grant.revision == nil {
		return nil
	}
	return *grant.revision
}

type canonicalStoredImportGrant struct {
	id          uuid.UUID
	objectType  string
	objectID    string
	aclVersion  int64
	revision    pgtype.Int8
	subjectType string
	subjectID   string
	action      string
	roomID      string
	sittingID   string
	expiresAt   pgtype.Timestamptz
	revokedAt   pgtype.Timestamptz
	conditions  []byte
}

func canonicalImportGrantMatches(stored canonicalStoredImportGrant, expected canonicalExpectedImportGrant) bool {
	if stored.id != expected.id || stored.objectType != expected.objectType || stored.objectID != expected.objectID ||
		stored.aclVersion != expected.aclVersion || stored.subjectType != expected.subjectType ||
		stored.subjectID != expected.subjectID || stored.action != expected.action || stored.roomID != expected.roomID ||
		stored.sittingID != "" || stored.expiresAt.Valid || stored.revokedAt.Valid || stored.revision.Valid != (expected.revision != nil) {
		return false
	}
	if expected.revision != nil && stored.revision.Int64 != *expected.revision {
		return false
	}
	var conditions map[string]any
	return json.Unmarshal(stored.conditions, &conditions) == nil && conditions != nil && len(conditions) == 0
}

// SyncImportGrants replaces only grants owned by the canonical importer. It
// never touches human/admin grants, and runs as one transaction so parity
// cannot observe a half-migrated principal set. Projection and importer-owned
// grant state are loaded in bounded set queries; unchanged rows cause no DML.
func (store *PostgresCanonicalStore) SyncImportGrants(ctx context.Context, plan CanonicalImportPlan) error {
	if store == nil || store.pool == nil {
		return ErrCanonicalStoreUnhealthy
	}
	if strings.TrimSpace(plan.TenantID) == "" {
		return errors.New("canonical import plan tenant is required")
	}
	expectedEvents := make(map[string]CanonicalEvent, len(plan.Events))
	for _, event := range plan.Events {
		if event.TenantID != plan.TenantID || event.AggregateVersion < 1 || event.ACLVersion < 1 {
			return fmt.Errorf("invalid canonical import event binding for %s/%s", event.AggregateType, event.AggregateID)
		}
		key := event.AggregateType + "\x00" + event.AggregateID
		if _, duplicate := expectedEvents[key]; duplicate {
			return fmt.Errorf("duplicate canonical import event binding for %s/%s", event.AggregateType, event.AggregateID)
		}
		expectedEvents[key] = event
	}
	objectsByKey := make(map[string]CanonicalImportedObject, len(plan.Objects))
	refs := make([]canonicalImportObjectRef, 0, len(plan.Objects))
	for _, object := range plan.Objects {
		if strings.TrimSpace(object.Family) == "" || strings.TrimSpace(object.ObjectID) == "" || object.AggregateVersion < 1 {
			return errors.New("canonical import plan contains an invalid object binding")
		}
		key := object.Family + "\x00" + object.ObjectID
		if _, duplicate := objectsByKey[key]; duplicate {
			return fmt.Errorf("duplicate canonical import object binding for %s/%s", object.Family, object.ObjectID)
		}
		event, found := expectedEvents[key]
		if !found || event.AggregateVersion != object.AggregateVersion || event.EventID != object.EventID {
			return fmt.Errorf("canonical import object/event mismatch for %s/%s", object.Family, object.ObjectID)
		}
		objectsByKey[key] = object
		refs = append(refs, canonicalImportObjectRef{family: object.Family, objectID: object.ObjectID})
	}
	if len(expectedEvents) != len(objectsByKey) {
		return errors.New("canonical import plan contains an event without an object binding")
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].family != refs[j].family {
			return refs[i].family < refs[j].family
		}
		return refs[i].objectID < refs[j].objectID
	})

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	projections := make(map[string]canonicalImportProjectionState, len(refs))
	for start := 0; start < len(refs); start += canonicalImportPrefetchBatchSize {
		end := start + canonicalImportPrefetchBatchSize
		if end > len(refs) {
			end = len(refs)
		}
		families := make([]string, end-start)
		objectIDs := make([]string, end-start)
		for index := start; index < end; index++ {
			families[index-start] = refs[index].family
			objectIDs[index-start] = refs[index].objectID
		}
		rows, queryErr := tx.Query(ctx, `SELECT o.object_type,o.object_id,o.state_revision,o.acl_version,o.content_revision
			FROM objects o JOIN unnest($2::text[],$3::text[]) AS wanted(object_type,object_id)
			ON wanted.object_type=o.object_type AND wanted.object_id=o.object_id
			WHERE o.tenant_id=$1 ORDER BY o.object_type,o.object_id FOR UPDATE OF o`, plan.TenantID, families, objectIDs)
		if queryErr != nil {
			return fmt.Errorf("prefetch imported ACL objects: %w", queryErr)
		}
		for rows.Next() {
			var family, objectID string
			var state canonicalImportProjectionState
			if scanErr := rows.Scan(&family, &objectID, &state.stateRevision, &state.aclVersion, &state.contentRevision); scanErr != nil {
				rows.Close()
				return fmt.Errorf("scan imported ACL object: %w", scanErr)
			}
			projections[family+"\x00"+objectID] = state
		}
		if queryErr := rows.Err(); queryErr != nil {
			rows.Close()
			return fmt.Errorf("prefetch imported ACL objects: %w", queryErr)
		}
		rows.Close()
	}

	expectedGrants := make(map[uuid.UUID]canonicalExpectedImportGrant)
	for _, ref := range refs {
		key := ref.family + "\x00" + ref.objectID
		object := objectsByKey[key]
		projection, found := projections[key]
		if !found {
			return fmt.Errorf("resolve imported ACL object %s/%s: %w", object.Family, object.ObjectID, pgx.ErrNoRows)
		}
		event := expectedEvents[key]
		if projection.stateRevision != object.AggregateVersion {
			return fmt.Errorf("imported state revision mismatch for %s/%s: projection=%d plan=%d", object.Family, object.ObjectID, projection.stateRevision, object.AggregateVersion)
		}
		if projection.aclVersion != event.ACLVersion {
			return fmt.Errorf("imported ACL version mismatch for %s/%s: projection=%d plan=%d", object.Family, object.ObjectID, projection.aclVersion, event.ACLVersion)
		}
		for _, grant := range object.ImportGrants {
			if !validACLAction(grant.Action) || strings.TrimSpace(grant.SubjectID) == "" {
				return fmt.Errorf("invalid imported grant for %s/%s", object.Family, object.ObjectID)
			}
			subjectType := string(grant.SubjectKind)
			switch grant.SubjectKind {
			case ACLSubjectTeam:
				if grant.SubjectPrincipalKind != "" {
					return errors.New("team import grant cannot carry a principal kind")
				}
			case ACLSubjectPrincipal:
				if !validACLPrincipalKind(grant.SubjectPrincipalKind) || grant.SubjectPrincipalKind == ACLPrincipalGuest || grant.SubjectPrincipalKind == ACLPrincipalService || grant.SubjectPrincipalKind == ACLPrincipalCapability {
					return fmt.Errorf("legacy durable grant cannot authorize %q", grant.SubjectPrincipalKind)
				}
				subjectType = string(grant.SubjectPrincipalKind)
			default:
				return fmt.Errorf("invalid imported grant subject kind %q", grant.SubjectKind)
			}
			var revisionValue any
			var revision *int64
			if grant.Action == ACLReadContent {
				if grant.Revision < 1 || grant.Revision != projection.contentRevision {
					return fmt.Errorf("imported content grant revision mismatch for %s/%s", object.Family, object.ObjectID)
				}
				current := projection.contentRevision
				revision = &current
				revisionValue = current
			} else if grant.Revision != 0 {
				return errors.New("metadata import grant must not bind a content revision")
			}
			grantID := uuid.NewSHA1(canonicalImportNamespace, []byte(strings.Join([]string{
				"legacy-import-grant-v1", plan.TenantID, object.Family, object.ObjectID,
				fmt.Sprint(projection.aclVersion), fmt.Sprint(revisionValue), subjectType, grant.SubjectID, string(grant.Action),
			}, "\x1f")))
			if _, duplicate := expectedGrants[grantID]; duplicate {
				return fmt.Errorf("duplicate imported grant for %s/%s", object.Family, object.ObjectID)
			}
			expectedGrants[grantID] = canonicalExpectedImportGrant{
				id: grantID, objectType: object.Family, objectID: object.ObjectID, aclVersion: projection.aclVersion,
				revision: revision, subjectType: subjectType, subjectID: grant.SubjectID, action: string(grant.Action), roomID: object.RoomID,
			}
		}
	}

	storedGrants := make(map[uuid.UUID]canonicalStoredImportGrant)
	for start := 0; start < len(refs); start += canonicalImportPrefetchBatchSize {
		end := start + canonicalImportPrefetchBatchSize
		if end > len(refs) {
			end = len(refs)
		}
		families := make([]string, end-start)
		objectIDs := make([]string, end-start)
		for index := start; index < end; index++ {
			families[index-start] = refs[index].family
			objectIDs[index-start] = refs[index].objectID
		}
		rows, queryErr := tx.Query(ctx, `SELECT g.grant_id::text,g.object_type,g.object_id,g.acl_version,g.revision,
			g.subject_type,g.subject_id,g.action,COALESCE(g.room_id,''),COALESCE(g.sitting_id,''),g.expires_at,g.revoked_at,g.conditions
			FROM object_grants g JOIN unnest($2::text[],$3::text[]) AS wanted(object_type,object_id)
			ON wanted.object_type=g.object_type AND wanted.object_id=g.object_id
			WHERE g.tenant_id=$1 AND g.granted_by_type='service' AND g.granted_by_id='canonical-import'
			ORDER BY g.object_type,g.object_id,g.grant_id FOR UPDATE OF g`, plan.TenantID, families, objectIDs)
		if queryErr != nil {
			return fmt.Errorf("prefetch canonical importer grants: %w", queryErr)
		}
		for rows.Next() {
			var id string
			var stored canonicalStoredImportGrant
			if scanErr := rows.Scan(&id, &stored.objectType, &stored.objectID, &stored.aclVersion, &stored.revision,
				&stored.subjectType, &stored.subjectID, &stored.action, &stored.roomID, &stored.sittingID,
				&stored.expiresAt, &stored.revokedAt, &stored.conditions); scanErr != nil {
				rows.Close()
				return fmt.Errorf("scan canonical importer grant: %w", scanErr)
			}
			stored.id, queryErr = uuid.Parse(id)
			if queryErr != nil {
				rows.Close()
				return fmt.Errorf("parse canonical importer grant: %w", queryErr)
			}
			storedGrants[stored.id] = stored
		}
		if queryErr := rows.Err(); queryErr != nil {
			rows.Close()
			return fmt.Errorf("prefetch canonical importer grants: %w", queryErr)
		}
		rows.Close()
	}

	upserts := make([]canonicalExpectedImportGrant, 0)
	for id, expected := range expectedGrants {
		stored, found := storedGrants[id]
		if !found || !canonicalImportGrantMatches(stored, expected) {
			upserts = append(upserts, expected)
		}
	}
	sort.Slice(upserts, func(i, j int) bool { return upserts[i].id.String() < upserts[j].id.String() })
	staleIDs := make([]uuid.UUID, 0)
	for id := range storedGrants {
		if _, retained := expectedGrants[id]; !retained {
			staleIDs = append(staleIDs, id)
		}
	}
	sort.Slice(staleIDs, func(i, j int) bool { return staleIDs[i].String() < staleIDs[j].String() })

	for _, grant := range upserts {
		command, execErr := tx.Exec(ctx, `INSERT INTO object_grants (
			grant_id,tenant_id,object_type,object_id,acl_version,revision,subject_type,subject_id,action,
			room_id,sitting_id,granted_by_type,granted_by_id,conditions
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),NULL,'service','canonical-import','{}'::jsonb)
		ON CONFLICT (grant_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,object_type=EXCLUDED.object_type,
			object_id=EXCLUDED.object_id,acl_version=EXCLUDED.acl_version,revision=EXCLUDED.revision,
			subject_type=EXCLUDED.subject_type,subject_id=EXCLUDED.subject_id,action=EXCLUDED.action,
			room_id=EXCLUDED.room_id,sitting_id=NULL,expires_at=NULL,revoked_at=NULL,conditions=EXCLUDED.conditions
		WHERE object_grants.granted_by_type='service' AND object_grants.granted_by_id='canonical-import'`,
			grant.id, plan.TenantID, grant.objectType, grant.objectID, grant.aclVersion, grant.revisionValue(),
			grant.subjectType, grant.subjectID, grant.action, grant.roomID)
		if execErr != nil {
			return fmt.Errorf("sync canonical importer grant %s: %w", grant.id, execErr)
		}
		if command.RowsAffected() != 1 {
			return fmt.Errorf("canonical importer grant %s conflicts with a non-importer grant", grant.id)
		}
	}
	for start := 0; start < len(staleIDs); start += canonicalImportPrefetchBatchSize {
		end := start + canonicalImportPrefetchBatchSize
		if end > len(staleIDs) {
			end = len(staleIDs)
		}
		command, execErr := tx.Exec(ctx, `DELETE FROM object_grants WHERE tenant_id=$1 AND grant_id=ANY($2::uuid[])
			AND granted_by_type='service' AND granted_by_id='canonical-import'`, plan.TenantID, staleIDs[start:end])
		if execErr != nil {
			return fmt.Errorf("delete stale canonical importer grants: %w", execErr)
		}
		if command.RowsAffected() != int64(end-start) {
			return errors.New("canonical importer grants changed during synchronization")
		}
	}
	return tx.Commit(ctx)
}

// PostgresCanonicalParityACL resolves the exact current projection and runs
// the production authorization kernel. User principals carry the migrated
// organization team; guests, services, and capabilities never do.
type PostgresCanonicalParityACL struct {
	store    *PostgresCanonicalStore
	tenantID string
}

func NewPostgresCanonicalParityACL(store *PostgresCanonicalStore, tenantID string) PostgresCanonicalParityACL {
	return PostgresCanonicalParityACL{store: store, tenantID: strings.TrimSpace(tenantID)}
}

func (resolver PostgresCanonicalParityACL) CanReadCanonicalObject(ctx context.Context, principal string, event CanonicalEvent) (bool, error) {
	return canReadCanonicalObjectWithStore(ctx, resolver.store, resolver.tenantID, principal, event, nil)
}

type immutableCanonicalParityACLStore struct {
	objects map[string]ACLObject
	grants  map[string][]ACLGrant
}

func (store immutableCanonicalParityACLStore) ResolveACLObject(_ context.Context, ref ACLObjectRef) (ACLObject, error) {
	object, ok := store.objects[aclObjectKey(ref)]
	if !ok {
		return ACLObject{}, ErrACLObjectNotFound
	}
	object.RequiredConsentScopes = append([]string(nil), object.RequiredConsentScopes...)
	return object, nil
}

func (store immutableCanonicalParityACLStore) ListACLGrants(_ context.Context, ref ACLObjectRef) ([]ACLGrant, error) {
	stored := store.grants[aclObjectKey(ref)]
	grants := make([]ACLGrant, len(stored))
	for index, grant := range stored {
		grants[index] = grant
		grants[index].Actions = append([]ACLAction(nil), grant.Actions...)
		grants[index].Obligations = append([]string(nil), grant.Obligations...)
	}
	return grants, nil
}

type immutableCanonicalParityACL struct {
	store    immutableCanonicalParityACLStore
	tenantID string
	at       time.Time
}

func (resolver immutableCanonicalParityACL) CanReadCanonicalObject(ctx context.Context, principal string, event CanonicalEvent) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return canReadCanonicalObjectWithStore(ctx, resolver.store, resolver.tenantID, principal, event, func() time.Time { return resolver.at })
}

type canonicalParityACLRef struct {
	objectType string
	objectID   string
}

// SnapshotCanonicalParityACL loads every object and current-version grant
// needed by one reconciliation in two bounded set queries under a read-only
// repeatable-read transaction. Authorization then runs against the immutable
// in-memory snapshot. The normal resolver above deliberately remains live and
// uncached for request-time authorization and subsequent reconcile passes.
func (resolver PostgresCanonicalParityACL) SnapshotCanonicalParityACL(ctx context.Context, events []CanonicalEvent) (CanonicalParityACLResolver, error) {
	if resolver.store == nil || resolver.store.pool == nil || resolver.tenantID == "" {
		return nil, ErrCanonicalStoreUnhealthy
	}
	refsByKey := make(map[string]canonicalParityACLRef, len(events))
	for _, event := range events {
		if event.TenantID != resolver.tenantID || strings.TrimSpace(event.AggregateType) == "" || strings.TrimSpace(event.AggregateID) == "" {
			return nil, errors.New("canonical parity ACL snapshot contains an invalid object reference")
		}
		ref := canonicalParityACLRef{objectType: event.AggregateType, objectID: event.AggregateID}
		refsByKey[event.AggregateType+"\x00"+event.AggregateID] = ref
	}
	refs := make([]canonicalParityACLRef, 0, len(refsByKey))
	for _, ref := range refsByKey {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].objectType != refs[j].objectType {
			return refs[i].objectType < refs[j].objectType
		}
		return refs[i].objectID < refs[j].objectID
	})
	snapshot := immutableCanonicalParityACL{
		store:    immutableCanonicalParityACLStore{objects: make(map[string]ACLObject, len(refs)), grants: make(map[string][]ACLGrant, len(refs))},
		tenantID: resolver.tenantID,
	}
	if len(refs) == 0 {
		snapshot.at = time.Now().UTC()
		return snapshot, nil
	}
	objectTypes := make([]string, len(refs))
	objectIDs := make([]string, len(refs))
	for index, ref := range refs {
		objectTypes[index] = ref.objectType
		objectIDs[index] = ref.objectID
	}
	tx, err := resolver.store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin canonical parity ACL snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	objectRows, err := tx.Query(ctx, `SELECT o.tenant_id,o.object_type,o.object_id,o.acl_version,COALESCE(o.room_id,''),o.state,o.content_revision,o.content_sha256,o.deleted_at
		FROM objects o JOIN unnest($2::text[],$3::text[]) AS wanted(object_type,object_id)
		ON wanted.object_type=o.object_type AND wanted.object_id=o.object_id
		WHERE o.tenant_id=$1 ORDER BY o.object_type,o.object_id`, resolver.tenantID, objectTypes, objectIDs)
	if err != nil {
		return nil, fmt.Errorf("load canonical parity ACL objects: %w", err)
	}
	for objectRows.Next() {
		object, scanErr := scanCanonicalACLObject(objectRows)
		if scanErr != nil {
			objectRows.Close()
			return nil, fmt.Errorf("scan canonical parity ACL object: %w", scanErr)
		}
		snapshot.store.objects[aclObjectKey(object.Ref)] = object
	}
	if err := objectRows.Err(); err != nil {
		objectRows.Close()
		return nil, fmt.Errorf("load canonical parity ACL objects: %w", err)
	}
	objectRows.Close()
	grantRows, err := tx.Query(ctx, `SELECT g.grant_id::text,g.tenant_id,g.object_type,g.object_id,g.acl_version,
		g.subject_type,g.subject_id,g.action,COALESCE(g.room_id,''),COALESCE(g.sitting_id,''),g.expires_at,g.revoked_at,g.conditions
		FROM object_grants g
		JOIN objects o ON o.tenant_id=g.tenant_id AND o.object_type=g.object_type AND o.object_id=g.object_id
		JOIN unnest($2::text[],$3::text[]) AS wanted(object_type,object_id)
		ON wanted.object_type=g.object_type AND wanted.object_id=g.object_id
		WHERE g.tenant_id=$1 AND g.acl_version=o.acl_version
		AND (g.revision IS NULL OR g.revision=o.content_revision)
		ORDER BY g.object_type,g.object_id,g.grant_id`, resolver.tenantID, objectTypes, objectIDs)
	if err != nil {
		return nil, fmt.Errorf("load canonical parity ACL grants: %w", err)
	}
	for grantRows.Next() {
		grant, scanErr := scanCanonicalACLGrant(grantRows)
		if scanErr != nil {
			grantRows.Close()
			return nil, fmt.Errorf("scan canonical parity ACL grant: %w", scanErr)
		}
		key := aclObjectKey(ACLObjectRef{TenantID: grant.TenantID, Type: grant.ObjectType, ID: grant.ObjectID})
		snapshot.store.grants[key] = append(snapshot.store.grants[key], grant)
	}
	if err := grantRows.Err(); err != nil {
		grantRows.Close()
		return nil, fmt.Errorf("load canonical parity ACL grants: %w", err)
	}
	grantRows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit canonical parity ACL snapshot: %w", err)
	}
	// Evaluate expirations after the snapshot transaction closes, never at a
	// stale pre-query timestamp if pool acquisition or the set reads were delayed.
	snapshot.at = time.Now().UTC()
	return snapshot, nil
}

func canReadCanonicalObjectWithStore(ctx context.Context, store ACLStore, tenantID, principal string, event CanonicalEvent, now func() time.Time) (bool, error) {
	kind, id, ok := splitCanonicalImportPrincipal(principal)
	if !ok || store == nil || tenantID == "" {
		return false, nil
	}
	aclPrincipal := ACLPrincipal{TenantID: tenantID, Kind: kind, ID: id}
	if kind == ACLPrincipalUser {
		aclPrincipal.TeamIDs = []string{canonicalLegacyOrgTeamID}
	}
	ref := ACLObjectRef{TenantID: tenantID, Type: event.AggregateType, ID: event.AggregateID, ACLVersion: event.ACLVersion}
	object, err := store.ResolveACLObject(ctx, ref)
	if err != nil {
		if errors.Is(err, ErrACLObjectNotFound) {
			return false, nil
		}
		return false, err
	}
	action := ACLReadMetadata
	revision := ACLRevisionRef{}
	if object.CurrentContentRevision > 0 && isHexDigest(object.CurrentContentDigest) {
		action = ACLReadContent
		revision = ACLRevisionRef{ContentRevision: object.CurrentContentRevision, ContentDigest: object.CurrentContentDigest}
	}
	decision := (AuthorizationKernel{Store: store, Now: now}).Authorize(ctx, aclPrincipal, action, ref, revision)
	if decision.DenialCode == ACLDenialUnavailable {
		return false, errors.New(decision.PolicyReason)
	}
	return decision.Allowed, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func scanCanonicalACLObject(row canonicalRowScanner) (ACLObject, error) {
	var object ACLObject
	var stateBytes []byte
	var contentDigest []byte
	var deleted pgtype.Timestamptz
	err := row.Scan(
		&object.Ref.TenantID, &object.Ref.Type, &object.Ref.ID, &object.Ref.ACLVersion, &object.RoomID, &stateBytes,
		&object.CurrentContentRevision, &contentDigest, &deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return ACLObject{}, ErrACLObjectNotFound
	}
	if err != nil {
		return ACLObject{}, err
	}
	object.Deleted = deleted.Valid
	object.CurrentContentDigest = hex.EncodeToString(contentDigest)
	var state struct {
		SittingID             string   `json:"sitting_id"`
		GuestLiveAccess       bool     `json:"guest_live_access"`
		RequiredConsentScopes []string `json:"required_consent_scopes"`
	}
	_ = json.Unmarshal(stateBytes, &state)
	object.SittingID = state.SittingID
	object.GuestLiveAccess = state.GuestLiveAccess
	object.RequiredConsentScopes = append([]string(nil), state.RequiredConsentScopes...)
	return object, nil
}

func (store *PostgresCanonicalStore) ResolveACLObject(ctx context.Context, ref ACLObjectRef) (ACLObject, error) {
	if store == nil || store.pool == nil {
		return ACLObject{}, ErrCanonicalStoreUnhealthy
	}
	return scanCanonicalACLObject(store.pool.QueryRow(ctx, `SELECT tenant_id,object_type,object_id,acl_version,COALESCE(room_id,''),state,content_revision,content_sha256,deleted_at
		FROM objects WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3`, ref.TenantID, ref.Type, ref.ID))
}

func scanCanonicalACLGrant(row canonicalRowScanner) (ACLGrant, error) {
	var grant ACLGrant
	var subjectType, action string
	var expires, revoked pgtype.Timestamptz
	var conditions []byte
	if err := row.Scan(&grant.ID, &grant.TenantID, &grant.ObjectType, &grant.ObjectID, &grant.ACLVersion,
		&subjectType, &grant.SubjectID, &action, &grant.RoomID, &grant.SittingID, &expires, &revoked, &conditions); err != nil {
		return ACLGrant{}, err
	}
	if subjectType == string(ACLSubjectTeam) {
		grant.SubjectKind = ACLSubjectTeam
	} else {
		grant.SubjectKind = ACLSubjectPrincipal
		grant.SubjectPrincipalKind = ACLPrincipalKind(subjectType)
	}
	grant.Actions = []ACLAction{ACLAction(action)}
	if expires.Valid {
		t := expires.Time
		grant.ExpiresAt = &t
	}
	if revoked.Valid {
		t := revoked.Time
		grant.RevokedAt = &t
	}
	var conditionState struct {
		Obligations []string `json:"obligations"`
	}
	_ = json.Unmarshal(conditions, &conditionState)
	grant.Obligations = uniqueSortedStrings(conditionState.Obligations)
	return grant, nil
}

func (store *PostgresCanonicalStore) ListACLGrants(ctx context.Context, ref ACLObjectRef) ([]ACLGrant, error) {
	if store == nil || store.pool == nil {
		return nil, ErrCanonicalStoreUnhealthy
	}
	rows, err := store.pool.Query(ctx, `SELECT grant_id::text,g.tenant_id,g.object_type,g.object_id,g.acl_version,
		g.subject_type,g.subject_id,g.action,COALESCE(g.room_id,''),COALESCE(g.sitting_id,''),g.expires_at,g.revoked_at,g.conditions
		FROM object_grants g JOIN objects o ON o.tenant_id=g.tenant_id AND o.object_type=g.object_type AND o.object_id=g.object_id
		WHERE g.tenant_id=$1 AND g.object_type=$2 AND g.object_id=$3 AND g.acl_version=o.acl_version
		AND (g.revision IS NULL OR g.revision=o.content_revision)`, ref.TenantID, ref.Type, ref.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var grants []ACLGrant
	for rows.Next() {
		grant, scanErr := scanCanonicalACLGrant(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}
