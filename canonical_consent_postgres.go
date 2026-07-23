package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type postgresConsentEvidence struct {
	Kind                        string  `json:"kind"`
	Ref                         string  `json:"ref"`
	LastAcceptedCaptureSequence *uint64 `json:"lastAcceptedCaptureSequence,omitempty"`
}

// PostgresConsentStore is deliberately a wrapper instead of another method
// set on PostgresCanonicalStore: Go does not overload the canonical event
// store's Append method, and the two logs remain separate contracts.
type PostgresConsentStore struct {
	canonical *PostgresCanonicalStore
}

func NewPostgresConsentStore(canonical *PostgresCanonicalStore) *PostgresConsentStore {
	return &PostgresConsentStore{canonical: canonical}
}

// acquireBindingLock serializes the file-backed transcript commit boundary
// with consent choices across app processes. The session advisory lock is
// held on one dedicated pool connection while the caller revalidates durable
// consent and commits local JSONL; a dropped connection releases it in
// PostgreSQL automatically.
func (store *PostgresConsentStore) acquireBindingLock(ctx context.Context, key int64) (func(), error) {
	return store.acquireBindingLocks(ctx, []int64{key})
}

// acquireBindingLocks holds an ordered set of advisory locks on one dedicated
// connection. A mixed segment may have more contributors than the pool has
// spare connections; consuming one connection per contributor would
// self-starve while already holding the earlier locks.
func (store *PostgresConsentStore) acquireBindingLocks(ctx context.Context, keys []int64) (func(), error) {
	if store == nil || store.canonical == nil || store.canonical.pool == nil {
		return nil, ErrCanonicalStoreUnhealthy
	}
	if len(keys) == 0 {
		return func() {}, nil
	}
	conn, err := store.canonical.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire consent advisory connection: %w", err)
	}
	acquired := make([]int64, 0, len(keys))
	for _, key := range keys {
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
			unlockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			for index := len(acquired) - 1; index >= 0; index-- {
				_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, acquired[index])
			}
			cancel()
			conn.Release()
			return nil, fmt.Errorf("acquire consent advisory lock: %w", err)
		}
		acquired = append(acquired, key)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			for index := len(acquired) - 1; index >= 0; index-- {
				_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, acquired[index])
			}
			conn.Release()
		})
	}, nil
}

// Append implements ConsentStore on the canonical PostgreSQL authority. The
// row is immutable; a retry with the byte-equivalent record is idempotent and
// a reused UUID with different facts is a hard conflict.
func (store *PostgresConsentStore) Append(ctx context.Context, record ConsentRecord) (bool, error) {
	if store == nil || store.canonical == nil || store.canonical.pool == nil {
		return false, ErrCanonicalStoreUnhealthy
	}
	parsedID, err := uuid.Parse(strings.TrimSpace(record.ID))
	if err != nil {
		return false, ErrConsentInvalid
	}
	record.ID = parsedID.String()
	record.RecordedAt = record.RecordedAt.UTC().Truncate(time.Microsecond)
	if err := record.Validate(); err != nil {
		return false, err
	}
	evidence, err := json.Marshal(postgresConsentEvidence{
		Kind: record.EvidenceKind, Ref: record.EvidenceRef,
		LastAcceptedCaptureSequence: record.LastAcceptedCaptureSequence,
	})
	if err != nil {
		return false, fmt.Errorf("encode consent evidence: %w", err)
	}
	scopes := make([]string, len(record.Scopes))
	for index, scope := range record.Scopes {
		scopes[index] = string(scope)
	}
	var withdrawnAt *time.Time
	if record.Disposition == ConsentWithdrawn {
		stamp := record.RecordedAt.UTC()
		withdrawnAt = &stamp
	}
	tag, err := store.canonical.pool.Exec(ctx, `INSERT INTO consent_records
		(consent_id, tenant_id, principal_type, principal_id, room_id, sitting_id,
		 policy_version, scopes, status, evidence, effective_at, withdrawn_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (consent_id) DO NOTHING`,
		record.ID, record.TenantID, string(record.PrincipalKind), record.PrincipalID,
		record.RoomID, record.SittingID, record.PolicyVersion, scopes,
		string(record.Disposition), evidence, record.RecordedAt.UTC(), withdrawnAt)
	if err != nil {
		return false, fmt.Errorf("append consent record: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return false, nil
	}
	existing, err := store.readConsentRecord(ctx, record.ID)
	if err != nil {
		return false, fmt.Errorf("read conflicting consent record: %w", err)
	}
	if !equalConsentRecord(existing, record) {
		return false, ErrConsentConflict
	}
	return true, nil
}

// Effective resolves every dependency-expanded scope directly from durable
// rows. Nothing is cached across calls: another process's withdrawal becomes
// authoritative on the next frame/segment/fence check.
func (store *PostgresConsentStore) Effective(ctx context.Context, query ConsentQuery) (ConsentDecision, error) {
	if store == nil || store.canonical == nil || store.canonical.pool == nil {
		return ConsentDecision{}, ErrCanonicalStoreUnhealthy
	}
	requested, err := normalizeConsentQuery(query)
	if err != nil {
		return ConsentDecision{}, err
	}
	decision := ConsentDecision{
		Allowed: true, RecordIDs: make(map[ConsentScope]string, len(requested)),
		Dispositions: make(map[ConsentScope]ConsentDisposition, len(requested)),
	}
	for _, scope := range requested {
		var recordID, status string
		err := store.canonical.pool.QueryRow(ctx, `SELECT consent_id::text, status
			FROM consent_records
			WHERE tenant_id=$1 AND principal_type=$2 AND principal_id=$3
			  AND room_id=$4 AND sitting_id=$5 AND policy_version=$6
			  AND $7 = ANY(scopes)
			  AND (expires_at IS NULL OR expires_at > now())
			ORDER BY effective_at DESC,
			  CASE status WHEN 'withdrawn' THEN 3 WHEN 'denied' THEN 2 ELSE 1 END DESC,
			  consent_id DESC
			LIMIT 1`, query.TenantID, string(query.PrincipalKind), query.PrincipalID,
			query.RoomID, query.SittingID, query.PolicyVersion, string(scope)).Scan(&recordID, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			decision.Allowed = false
			decision.MissingScopes = append(decision.MissingScopes, scope)
			continue
		}
		if err != nil {
			return ConsentDecision{}, fmt.Errorf("resolve effective consent: %w", err)
		}
		decision.RecordIDs[scope] = recordID
		decision.Dispositions[scope] = ConsentDisposition(status)
		if ConsentDisposition(status) != ConsentGranted {
			decision.Allowed = false
			decision.MissingScopes = append(decision.MissingScopes, scope)
		}
	}
	return decision, nil
}

func (store *PostgresConsentStore) readConsentRecord(ctx context.Context, id string) (ConsentRecord, error) {
	var record ConsentRecord
	var principalKind, disposition string
	var scopes []string
	var evidenceRaw []byte
	err := store.canonical.pool.QueryRow(ctx, `SELECT consent_id::text, tenant_id, principal_type, principal_id,
		room_id, sitting_id, policy_version, scopes, status, evidence, effective_at
		FROM consent_records WHERE consent_id=$1`, id).Scan(
		&record.ID, &record.TenantID, &principalKind, &record.PrincipalID,
		&record.RoomID, &record.SittingID, &record.PolicyVersion, &scopes,
		&disposition, &evidenceRaw, &record.RecordedAt)
	if err != nil {
		return ConsentRecord{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(evidenceRaw))
	decoder.DisallowUnknownFields()
	var evidence postgresConsentEvidence
	if err := decoder.Decode(&evidence); err != nil {
		return ConsentRecord{}, fmt.Errorf("decode consent evidence: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return ConsentRecord{}, fmt.Errorf("decode consent evidence: %w", err)
	}
	record.PrincipalKind = ACLPrincipalKind(principalKind)
	record.Disposition = ConsentDisposition(disposition)
	record.EvidenceKind = evidence.Kind
	record.EvidenceRef = evidence.Ref
	record.LastAcceptedCaptureSequence = evidence.LastAcceptedCaptureSequence
	for _, scope := range scopes {
		record.Scopes = append(record.Scopes, ConsentScope(scope))
	}
	if err := record.Validate(); err != nil {
		return ConsentRecord{}, err
	}
	return record, nil
}
