package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const canonicalMigrationAdvisoryLock int64 = 0x426f6e6669726532 // "Bonfire2"

var (
	ErrCanonicalMigrationDrift   = errors.New("canonical migration checksum drift")
	ErrCanonicalUnknownMigration = errors.New("database contains canonical migration unknown to this binary")
	ErrCanonicalStoreUnhealthy   = errors.New("canonical PostgreSQL store is unhealthy")
)

type PostgresCanonicalStore struct {
	pool      *pgxpool.Pool
	registry  *CanonicalPayloadRegistry
	Failpoint func(string) error
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
		if _, known := embeddedVersions[version]; !known {
			return fmt.Errorf("%w: version %d", ErrCanonicalUnknownMigration, version)
		}
	}
	for _, migration := range migrations {
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
		notifyProductionBrainProjectionCanonicalEvent(store, event)
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
	if event.AggregateVersion != 1 {
		var objectExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM objects WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3)`,
			event.TenantID, event.AggregateType, event.AggregateID).Scan(&objectExists); err != nil {
			return fmt.Errorf("check canonical projection predecessor: %w", err)
		}
		if !objectExists {
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
	WHERE objects.state_revision = EXCLUDED.state_revision - 1`,
		event.TenantID, event.AggregateType, event.AggregateID, event.AggregateVersion, contentRevision, roomID, event.MeetingID,
		event.Classification, []byte(event.Payload), contentDigest, event.ACLVersion, sequence, deletedAt, event.RetainUntil)
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
	existingFingerprint, err := canonicalEventFingerprint(existing)
	if err != nil {
		return CanonicalAppendResult{}, false, err
	}
	candidateFingerprint, err := canonicalEventFingerprint(candidate)
	if err != nil {
		return CanonicalAppendResult{}, false, err
	}
	if existingFingerprint != candidateFingerprint {
		return CanonicalAppendResult{}, false, ErrCanonicalIdempotencyConflict
	}
	return CanonicalAppendResult{Event: existing, Existing: true}, true, nil
}

type canonicalRowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func queryCanonicalEvent(ctx context.Context, queryer canonicalRowQuerier, where string, args ...any) (CanonicalEvent, error) {
	query := `SELECT event_id::text,tenant_id,aggregate_type,aggregate_id,aggregate_version,event_type,schema_version,
		occurred_at,recorded_at,actor_type,actor_id,COALESCE(room_id,''),COALESCE(meeting_id,''),COALESCE(correlation_id,''),
		causation_id::text,COALESCE(idempotency_key,''),classification,consent_snapshot_id::text,acl_version,
		payload::text,COALESCE(content_ref,''),payload_sha256,retain_until
		FROM canonical_events WHERE ` + where + ` ORDER BY sequence LIMIT 1`
	var event CanonicalEvent
	var eventID string
	var causation, consent pgtype.Text
	var payload string
	var digest []byte
	var retain pgtype.Timestamptz
	err := queryer.QueryRow(ctx, query, args...).Scan(
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
	rows, err := store.pool.Query(ctx, "SELECT sequence FROM canonical_events ORDER BY sequence")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sequences []int64
	for rows.Next() {
		var sequence int64
		if err := rows.Scan(&sequence); err != nil {
			return nil, err
		}
		sequences = append(sequences, sequence)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	events := make([]CanonicalEvent, 0, len(sequences))
	for _, sequence := range sequences {
		event, err := queryCanonicalEvent(ctx, store.pool, "sequence=$1", sequence)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

// SyncImportGrants replaces only grants owned by the canonical importer. It
// never touches human/admin grants, and runs as one transaction so parity
// cannot observe a half-migrated principal set.
func (store *PostgresCanonicalStore) SyncImportGrants(ctx context.Context, plan CanonicalImportPlan) error {
	if store == nil || store.pool == nil {
		return ErrCanonicalStoreUnhealthy
	}
	expectedACLVersions := make(map[string]int64, len(plan.Events))
	for _, event := range plan.Events {
		expectedACLVersions[event.AggregateType+"\x00"+event.AggregateID] = event.ACLVersion
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	for _, object := range plan.Objects {
		var currentACLVersion, currentContentRevision int64
		if err := tx.QueryRow(ctx, `SELECT acl_version,content_revision FROM objects
			WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3 FOR UPDATE`, plan.TenantID, object.Family, object.ObjectID).
			Scan(&currentACLVersion, &currentContentRevision); err != nil {
			return fmt.Errorf("resolve imported ACL object %s/%s: %w", object.Family, object.ObjectID, err)
		}
		expectedACLVersion, found := expectedACLVersions[object.Family+"\x00"+object.ObjectID]
		if !found || expectedACLVersion < 1 || currentACLVersion != expectedACLVersion {
			return fmt.Errorf("imported ACL version mismatch for %s/%s: projection=%d plan=%d", object.Family, object.ObjectID, currentACLVersion, expectedACLVersion)
		}
		expectedIDs := make([]uuid.UUID, 0, len(object.ImportGrants))
		for _, grant := range object.ImportGrants {
			if !validACLAction(grant.Action) || strings.TrimSpace(grant.SubjectID) == "" {
				return fmt.Errorf("invalid imported grant for %s/%s", object.Family, object.ObjectID)
			}
			subjectType := string(grant.SubjectKind)
			switch grant.SubjectKind {
			case ACLSubjectTeam:
				if grant.SubjectPrincipalKind != "" {
					return fmt.Errorf("team import grant cannot carry a principal kind")
				}
			case ACLSubjectPrincipal:
				if !validACLPrincipalKind(grant.SubjectPrincipalKind) || grant.SubjectPrincipalKind == ACLPrincipalGuest || grant.SubjectPrincipalKind == ACLPrincipalService || grant.SubjectPrincipalKind == ACLPrincipalCapability {
					return fmt.Errorf("legacy durable grant cannot authorize %q", grant.SubjectPrincipalKind)
				}
				subjectType = string(grant.SubjectPrincipalKind)
			default:
				return fmt.Errorf("invalid imported grant subject kind %q", grant.SubjectKind)
			}
			var revision any
			if grant.Action == ACLReadContent {
				if grant.Revision < 1 || grant.Revision != currentContentRevision {
					return fmt.Errorf("imported content grant revision mismatch for %s/%s", object.Family, object.ObjectID)
				}
				revision = currentContentRevision
			} else if grant.Revision != 0 {
				return fmt.Errorf("metadata import grant must not bind a content revision")
			}
			grantID := uuid.NewSHA1(canonicalImportNamespace, []byte(strings.Join([]string{
				"legacy-import-grant-v1", plan.TenantID, object.Family, object.ObjectID,
				fmt.Sprint(currentACLVersion), fmt.Sprint(revision), subjectType, grant.SubjectID, string(grant.Action),
			}, "\x1f")))
			expectedIDs = append(expectedIDs, grantID)
			if _, err := tx.Exec(ctx, `INSERT INTO object_grants (
				grant_id,tenant_id,object_type,object_id,acl_version,revision,subject_type,subject_id,action,
				room_id,sitting_id,granted_by_type,granted_by_id,conditions
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),NULL,'service','canonical-import','{}'::jsonb)
			ON CONFLICT (grant_id) DO UPDATE SET acl_version=EXCLUDED.acl_version,revision=EXCLUDED.revision,
				subject_type=EXCLUDED.subject_type,subject_id=EXCLUDED.subject_id,action=EXCLUDED.action,
				room_id=EXCLUDED.room_id,sitting_id=NULL,revoked_at=NULL,conditions=EXCLUDED.conditions`,
				grantID, plan.TenantID, object.Family, object.ObjectID, currentACLVersion, revision, subjectType, grant.SubjectID, string(grant.Action), object.RoomID); err != nil {
				return err
			}
		}
		if len(expectedIDs) == 0 {
			if _, err := tx.Exec(ctx, `DELETE FROM object_grants WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3
				AND granted_by_type='service' AND granted_by_id='canonical-import'`, plan.TenantID, object.Family, object.ObjectID); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `DELETE FROM object_grants WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3
			AND granted_by_type='service' AND granted_by_id='canonical-import' AND NOT (grant_id = ANY($4::uuid[]))`,
			plan.TenantID, object.Family, object.ObjectID, expectedIDs); err != nil {
			return err
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
	kind, id, ok := splitCanonicalImportPrincipal(principal)
	if !ok || resolver.store == nil || resolver.tenantID == "" {
		return false, nil
	}
	aclPrincipal := ACLPrincipal{TenantID: resolver.tenantID, Kind: kind, ID: id}
	if kind == ACLPrincipalUser {
		aclPrincipal.TeamIDs = []string{canonicalLegacyOrgTeamID}
	}
	ref := ACLObjectRef{TenantID: resolver.tenantID, Type: event.AggregateType, ID: event.AggregateID, ACLVersion: event.ACLVersion}
	object, err := resolver.store.ResolveACLObject(ctx, ref)
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
	decision := (AuthorizationKernel{Store: resolver.store}).Authorize(ctx, aclPrincipal, action, ref, revision)
	if decision.DenialCode == ACLDenialUnavailable {
		return false, errors.New(decision.PolicyReason)
	}
	return decision.Allowed, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (store *PostgresCanonicalStore) ResolveACLObject(ctx context.Context, ref ACLObjectRef) (ACLObject, error) {
	var object ACLObject
	var stateBytes []byte
	var contentDigest []byte
	var deleted pgtype.Timestamptz
	err := store.pool.QueryRow(ctx, `SELECT tenant_id,object_type,object_id,acl_version,room_id,state,content_revision,content_sha256,deleted_at
		FROM objects WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3`, ref.TenantID, ref.Type, ref.ID).Scan(
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

func (store *PostgresCanonicalStore) ListACLGrants(ctx context.Context, ref ACLObjectRef) ([]ACLGrant, error) {
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
		var grant ACLGrant
		var subjectType, action string
		var expires, revoked pgtype.Timestamptz
		var conditions []byte
		if err := rows.Scan(&grant.ID, &grant.TenantID, &grant.ObjectType, &grant.ObjectID, &grant.ACLVersion,
			&subjectType, &grant.SubjectID, &action, &grant.RoomID, &grant.SittingID, &expires, &revoked, &conditions); err != nil {
			return nil, err
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
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}
