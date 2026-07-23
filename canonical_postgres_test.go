package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func startDisposableCanonicalPostgres(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	initdb, initErr := exec.LookPath("initdb")
	_, postgresErr := exec.LookPath("postgres")
	pgCtl, ctlErr := exec.LookPath("pg_ctl")
	if initErr != nil || postgresErr != nil || ctlErr != nil {
		t.Skipf("disposable PostgreSQL skipped: initdb/postgres/pg_ctl unavailable (initdb=%v postgres=%v pg_ctl=%v)", initErr, postgresErr, ctlErr)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("disposable PostgreSQL skipped: reserve port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	dir := filepath.Join(t.TempDir(), "pgdata")
	initCommand := exec.Command(initdb, "-D", dir, "-A", "trust", "-U", "postgres", "--no-locale", "--encoding=UTF8")
	if output, err := initCommand.CombinedOutput(); err != nil {
		t.Skipf("disposable PostgreSQL skipped: initdb failed: %v\n%s", err, output)
	}
	logPath := filepath.Join(filepath.Dir(dir), "postgres.log")
	options := fmt.Sprintf("-F -p %d -h 127.0.0.1", port)
	startCommand := exec.Command(pgCtl, "-D", dir, "-l", logPath, "-o", options, "-w", "start")
	if output, err := startCommand.CombinedOutput(); err != nil {
		logBytes, _ := os.ReadFile(logPath)
		t.Skipf("disposable PostgreSQL skipped: start failed: %v\n%s\n%s", err, output, logBytes)
	}
	t.Cleanup(func() {
		_ = exec.Command(pgCtl, "-D", dir, "-m", "immediate", "-w", "stop").Run()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	url := fmt.Sprintf("postgres://postgres@127.0.0.1:%d/postgres?sslmode=disable", port)
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("open disposable PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping disposable PostgreSQL: %v", err)
	}
	return ctx, pool
}

func migratedPostgresCanonicalStore(t *testing.T) (context.Context, *PostgresCanonicalStore, *CanonicalPayloadRegistry) {
	t.Helper()
	ctx, pool := startDisposableCanonicalPostgres(t)
	registry := testCanonicalRegistry(t)
	store := NewPostgresCanonicalStore(pool, registry)
	if err := store.ApplyMigrations(ctx); err != nil {
		t.Fatalf("apply canonical migrations: %v", err)
	}
	return ctx, store, registry
}

func TestPostgresCanonicalMigrationsAreIdempotentAndRefuseDrift(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	if err := store.ApplyMigrations(ctx); err != nil {
		t.Fatalf("second migration apply: %v", err)
	}
	var count int
	if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 7 {
		t.Fatalf("migration rows=%d err=%v, want 7", count, err)
	}
	if _, err := store.pool.Exec(ctx, "UPDATE schema_migrations SET sha256=decode($1,'hex') WHERE version=1", strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyMigrations(ctx); !errors.Is(err, ErrCanonicalMigrationDrift) {
		t.Fatalf("drift apply error=%v, want ErrCanonicalMigrationDrift", err)
	}
}

func TestPostgresCanonicalMigrationsRefuseUnknownFutureVersion(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	if _, err := store.pool.Exec(ctx, "INSERT INTO schema_migrations(version,sha256) VALUES (999,decode($1,'hex'))", strings.Repeat("f", 64)); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyMigrations(ctx); !errors.Is(err, ErrCanonicalUnknownMigration) {
		t.Fatalf("future migration error=%v, want ErrCanonicalUnknownMigration", err)
	}
}

func TestPostgresCanonicalAppendIsTransactionalIdempotentAndConflicted(t *testing.T) {
	ctx, store, registry := migratedPostgresCanonicalStore(t)
	event := canonicalTestEvent(t, registry, uuid.MustParse("01950c74-7d49-7cc2-ae84-51f3be0a8978"), "artifact-a", 1, "request-1", "private")
	first, err := store.Append(ctx, event)
	if err != nil || first.Existing {
		t.Fatalf("first append=%+v err=%v", first, err)
	}
	retry := event
	retry.EventID = uuid.New()
	retry.RecordedAt = retry.RecordedAt.Add(time.Minute)
	second, err := store.Append(ctx, retry)
	if err != nil || !second.Existing || second.Event.EventID != event.EventID {
		t.Fatalf("retry append=%+v err=%v", second, err)
	}

	conflict := canonicalTestEvent(t, registry, uuid.New(), "artifact-a", 1, "request-2", "private")
	if _, err := store.Append(ctx, conflict); !errors.Is(err, ErrCanonicalAggregateConflict) {
		t.Fatalf("aggregate conflict=%v", err)
	}
	idemConflict := canonicalTestEvent(t, registry, uuid.New(), "artifact-b", 1, "request-1", "organization")
	if _, err := store.Append(ctx, idemConflict); !errors.Is(err, ErrCanonicalIdempotencyConflict) {
		t.Fatalf("idempotency conflict=%v", err)
	}

	for table, want := range map[string]int{"canonical_events": 1, "objects": 1, "outbox": 1} {
		var got int
		if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s rows=%d err=%v, want %d", table, got, err, want)
		}
	}
	events, err := store.Events(ctx)
	if err != nil || len(events) != 1 || events[0].EventID != event.EventID {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestPostgresCanonicalFailpointRollsBackEventProjectionAndOutbox(t *testing.T) {
	ctx, store, registry := migratedPostgresCanonicalStore(t)
	store.Failpoint = func(point string) error {
		if point == "after_event_before_projection" {
			return errors.New("injected projection failure")
		}
		return nil
	}
	event := canonicalTestEvent(t, registry, uuid.New(), "artifact-fail", 1, "request-fail", "private")
	if _, err := store.Append(ctx, event); err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("append failpoint error=%v", err)
	}
	for _, table := range []string{"canonical_events", "objects", "outbox"} {
		var got int
		if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != 0 {
			t.Fatalf("%s rows=%d err=%v, want rollback to zero", table, got, err)
		}
	}
}

func TestPostgresCanonicalACLReadsFeedDefaultDenyKernel(t *testing.T) {
	ctx, store, registry := migratedPostgresCanonicalStore(t)
	event := canonicalTestEvent(t, registry, uuid.New(), "artifact-acl", 1, "request-acl", "private")
	if _, err := store.Append(ctx, event); err != nil {
		t.Fatal(err)
	}
	grantID := uuid.New()
	if _, err := store.pool.Exec(ctx, `INSERT INTO object_grants (
		grant_id,tenant_id,object_type,object_id,acl_version,subject_type,subject_id,action,granted_by_type,granted_by_id,conditions
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'user','owner',$9::jsonb)`, grantID, event.TenantID, event.AggregateType,
		event.AggregateID, event.ACLVersion, string(ACLPrincipalUser), "alice", string(ACLReadContent), `{"obligations":["redact"]}`); err != nil {
		t.Fatal(err)
	}
	ref := ACLObjectRef{TenantID: event.TenantID, Type: event.AggregateType, ID: event.AggregateID, ACLVersion: event.ACLVersion}
	principal := ACLPrincipal{TenantID: event.TenantID, ID: "alice", Kind: ACLPrincipalUser}
	contentDigest := sha256.Sum256([]byte("content"))
	revision := ACLRevisionRef{ContentRevision: event.AggregateVersion, ContentDigest: hex.EncodeToString(contentDigest[:])}
	decision := (AuthorizationKernel{Store: store}).Authorize(ctx, principal, ACLReadContent, ref, revision)
	if !decision.Allowed || decision.MatchedGrantID != grantID.String() {
		t.Fatalf("ACL decision=%+v", decision)
	}
	serviceCollision := principal
	serviceCollision.Kind = ACLPrincipalService
	if got := (AuthorizationKernel{Store: store}).Authorize(ctx, serviceCollision, ACLReadContent, ref, revision); got.Allowed {
		t.Fatalf("service with colliding id inherited user grant: %+v", got)
	}

	// The adapter's state revision is the aggregate version, not an unrelated
	// insertion order or sequence number.
	var revisionText string
	if err := store.pool.QueryRow(ctx, "SELECT state_revision::text FROM objects WHERE object_id=$1", event.AggregateID).Scan(&revisionText); err != nil || revisionText != strconv.FormatInt(event.AggregateVersion, 10) {
		t.Fatalf("state revision=%q err=%v", revisionText, err)
	}
}

func TestPostgresCanonicalACLVersionBumpInvalidatesOldGrantUntilReissued(t *testing.T) {
	ctx, store, registry := migratedPostgresCanonicalStore(t)
	event := canonicalTestEvent(t, registry, uuid.New(), "artifact-acl-bump", 1, "request-acl-bump", "private")
	if _, err := store.Append(ctx, event); err != nil {
		t.Fatal(err)
	}
	principal := ACLPrincipal{TenantID: event.TenantID, ID: "alice", Kind: ACLPrincipalUser}
	contentDigest := sha256.Sum256([]byte("content"))
	revision := ACLRevisionRef{ContentRevision: 1, ContentDigest: hex.EncodeToString(contentDigest[:])}
	insertGrant := func(aclVersion int64) string {
		t.Helper()
		id := uuid.New()
		if _, err := store.pool.Exec(ctx, `INSERT INTO object_grants (
			grant_id,tenant_id,object_type,object_id,acl_version,subject_type,subject_id,action,granted_by_type,granted_by_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'user','owner')`, id, event.TenantID, event.AggregateType,
			event.AggregateID, aclVersion, string(ACLPrincipalUser), principal.ID, string(ACLReadContent)); err != nil {
			t.Fatal(err)
		}
		return id.String()
	}
	oldGrantID := insertGrant(1)
	refV1 := ACLObjectRef{TenantID: event.TenantID, Type: event.AggregateType, ID: event.AggregateID, ACLVersion: 1}
	if got := (AuthorizationKernel{Store: store}).Authorize(ctx, principal, ACLReadContent, refV1, revision); !got.Allowed || got.MatchedGrantID != oldGrantID {
		t.Fatalf("v1 authorization=%+v", got)
	}
	if _, err := store.pool.Exec(ctx, "UPDATE objects SET acl_version=2 WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3", event.TenantID, event.AggregateType, event.AggregateID); err != nil {
		t.Fatal(err)
	}
	refV2 := refV1
	refV2.ACLVersion = 2
	if got := (AuthorizationKernel{Store: store}).Authorize(ctx, principal, ACLReadContent, refV2, revision); got.Allowed {
		t.Fatalf("old v1 grant survived ACL bump: %+v", got)
	}
	newGrantID := insertGrant(2)
	if got := (AuthorizationKernel{Store: store}).Authorize(ctx, principal, ACLReadContent, refV2, revision); !got.Allowed || got.MatchedGrantID != newGrantID {
		t.Fatalf("reissued v2 authorization=%+v", got)
	}
}

func TestPostgresCanonicalStateOnlyEventPreservesContentBinding(t *testing.T) {
	ctx, store, registry := migratedPostgresCanonicalStore(t)
	if err := registry.Register("artifact.visibility", 1, CanonicalPayloadSchema{Fields: map[string]CanonicalPayloadField{
		"artifact_id": {Kind: CanonicalPayloadIdentifier, Required: true},
		"visibility":  {Kind: CanonicalPayloadEnum, Required: true, Enums: []string{"private", "organization"}},
	}}); err != nil {
		t.Fatal(err)
	}
	first := canonicalTestEvent(t, registry, uuid.New(), "artifact-state-only", 1, "state-1", "private")
	if _, err := store.Append(ctx, first); err != nil {
		t.Fatal(err)
	}
	payload, payloadDigest, err := NewCanonicalEventPayload(registry, "artifact.visibility", 1, map[string]any{
		"artifact_id": first.AggregateID, "visibility": "organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.EventID = uuid.New()
	second.AggregateVersion = 2
	second.EventType = "artifact.visibility"
	second.IdempotencyKey = "state-2"
	second.Payload = payload
	second.PayloadSHA256 = payloadDigest
	second.OccurredAt = second.OccurredAt.Add(time.Second)
	second.RecordedAt = second.RecordedAt.Add(time.Second)
	if _, err := store.Append(ctx, second); err != nil {
		t.Fatal(err)
	}
	var stateRevision, contentRevision int64
	var contentSHA []byte
	if err := store.pool.QueryRow(ctx, `SELECT state_revision,content_revision,content_sha256 FROM objects WHERE object_id=$1`, first.AggregateID).
		Scan(&stateRevision, &contentRevision, &contentSHA); err != nil {
		t.Fatal(err)
	}
	wantContent := sha256.Sum256([]byte("content"))
	if stateRevision != 2 || contentRevision != 1 || !equalBytes(contentSHA, wantContent[:]) {
		t.Fatalf("state=%d content=%d hash=%x, want 2/1/%x", stateRevision, contentRevision, contentSHA, wantContent)
	}
}

func TestPostgresCanonicalRejectsFirstAggregateVersionAboveOneTransactionally(t *testing.T) {
	ctx, store, registry := migratedPostgresCanonicalStore(t)
	event := canonicalTestEvent(t, registry, uuid.New(), "artifact-missing-v1", 2, "missing-v1", "private")
	if _, err := store.Append(ctx, event); !errors.Is(err, ErrCanonicalProjectionOrder) {
		t.Fatalf("first version 2 error=%v, want ErrCanonicalProjectionOrder", err)
	}
	for _, table := range []string{"canonical_events", "objects", "outbox"} {
		var got int
		if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != 0 {
			t.Fatalf("%s rows=%d err=%v, want transactional rollback", table, got, err)
		}
	}
}

func TestPostgresCanonicalNanosecondRetryNormalizesToDatabasePrecision(t *testing.T) {
	ctx, store, registry := migratedPostgresCanonicalStore(t)
	event := canonicalTestEvent(t, registry, uuid.New(), "artifact-nanos", 1, "nanos-1", "private")
	event.OccurredAt = time.Date(2026, 7, 12, 20, 0, 0, 987654321, time.FixedZone("offset", -7*60*60))
	event.RecordedAt = time.Date(2026, 7, 12, 20, 1, 0, 123456789, time.FixedZone("offset", -7*60*60))
	retain := time.Date(2026, 7, 15, 20, 0, 0, 333222111, time.FixedZone("offset", -7*60*60))
	event.RetainUntil = &retain
	first, err := store.Append(ctx, event)
	if err != nil || first.Existing {
		t.Fatalf("first append=%+v err=%v", first, err)
	}
	if first.Event.OccurredAt.Location() != time.UTC || first.Event.OccurredAt.Nanosecond()%1000 != 0 ||
		first.Event.RecordedAt.Location() != time.UTC || first.Event.RecordedAt.Nanosecond()%1000 != 0 ||
		first.Event.RetainUntil == nil || first.Event.RetainUntil.Location() != time.UTC || first.Event.RetainUntil.Nanosecond()%1000 != 0 {
		t.Fatalf("first event timestamps were not normalized: %+v", first.Event)
	}
	retry, err := store.Append(ctx, event)
	if err != nil || !retry.Existing || retry.Event.EventID != first.Event.EventID {
		t.Fatalf("exact nanosecond retry=%+v err=%v", retry, err)
	}
	if !retry.Event.OccurredAt.Equal(first.Event.OccurredAt) || !retry.Event.RecordedAt.Equal(first.Event.RecordedAt) ||
		retry.Event.RetainUntil == nil || !retry.Event.RetainUntil.Equal(*first.Event.RetainUntil) {
		t.Fatalf("retry timestamps differ: first=%+v retry=%+v", first.Event, retry.Event)
	}
}
