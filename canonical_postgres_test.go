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
	"sync"
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
	if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 17 {
		t.Fatalf("migration rows=%d err=%v, want 17", count, err)
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

func TestCanonicalMigrationRuntimePolicy(t *testing.T) {
	t.Setenv(canonicalMigrationMaxVersionEnv, "")
	t.Setenv(canonicalAllowFutureMigrationsEnv, "")
	maxVersion, allowFuture, err := canonicalMigrationRuntimePolicy(17)
	if err != nil || maxVersion != 17 || allowFuture {
		t.Fatalf("default policy=(%d,%t,%v), want (17,false,nil)", maxVersion, allowFuture, err)
	}
	t.Setenv(canonicalMigrationMaxVersionEnv, "13")
	t.Setenv(canonicalAllowFutureMigrationsEnv, "true")
	maxVersion, allowFuture, err = canonicalMigrationRuntimePolicy(17)
	if err != nil || maxVersion != 13 || !allowFuture {
		t.Fatalf("compatibility policy=(%d,%t,%v), want (13,true,nil)", maxVersion, allowFuture, err)
	}
	for _, test := range []struct {
		name  string
		max   string
		allow string
	}{
		{name: "zero max", max: "0"},
		{name: "future max", max: "18"},
		{name: "malformed max", max: "latest"},
		{name: "malformed allow", allow: "sometimes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(canonicalMigrationMaxVersionEnv, test.max)
			t.Setenv(canonicalAllowFutureMigrationsEnv, test.allow)
			if _, _, err := canonicalMigrationRuntimePolicy(17); err == nil {
				t.Fatal("invalid migration policy was accepted")
			}
		})
	}
}

func TestPostgresCanonicalMigrationCompatibilityCapAndFutureTolerance(t *testing.T) {
	t.Setenv(canonicalMigrationMaxVersionEnv, "13")
	t.Setenv(canonicalAllowFutureMigrationsEnv, "true")
	ctx, pool := startDisposableCanonicalPostgres(t)
	store := NewPostgresCanonicalStore(pool, testCanonicalRegistry(t))
	if err := store.ApplyMigrations(ctx); err != nil {
		t.Fatalf("apply capped canonical migrations: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 13 {
		t.Fatalf("capped migration rows=%d err=%v, want 13", count, err)
	}
	var identityTable *string
	if err := pool.QueryRow(ctx, "SELECT to_regclass('public.stride_person_profiles')::text").Scan(&identityTable); err != nil {
		t.Fatalf("inspect capped schema: %v", err)
	}
	if identityTable != nil {
		t.Fatalf("migration 14 table exists under cap: %q", *identityTable)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO schema_migrations(version,sha256) VALUES (18,decode($1,'hex'))", strings.Repeat("f", 64)); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyMigrations(ctx); err != nil {
		t.Fatalf("compatibility binary rejected additive future migration: %v", err)
	}
	t.Setenv(canonicalAllowFutureMigrationsEnv, "false")
	if err := store.ApplyMigrations(ctx); !errors.Is(err, ErrCanonicalUnknownMigration) {
		t.Fatalf("strict binary future migration error=%v, want ErrCanonicalUnknownMigration", err)
	}
}

func TestPostgresSTRIDEMigrationsRejectNestedBodiesCredentialsAndMalformedReferences(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	for _, test := range []struct {
		name string
		doc  string
		want bool
	}{
		{name: "nested api key", doc: `{"metadata":{"api_key":"secret"}}`, want: true},
		{name: "array nested body", doc: `{"items":[{"body":"full transcript"}]}`, want: true},
		{name: "mixed case credential", doc: `{"outer":{"Credential":"secret"}}`, want: true},
		{name: "body free metadata", doc: `{"source":"meeting","counts":{"participants":3}}`, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var got bool
			err := store.pool.QueryRow(ctx, `SELECT stride_jsonb_has_forbidden_key($1::jsonb,
				ARRAY['body','text','audio','secret','token','api_key','authorization','credential','credentials','password','cookie'])`, test.doc).Scan(&got)
			if err != nil || got != test.want {
				t.Fatalf("forbidden=%t err=%v, want %t for %s", got, err, test.want, test.doc)
			}
		})
	}

	digest := strings.Repeat("a", 64)
	valid := fmt.Sprintf(`[{"contractType":"rich_message_part","id":"asset_1","revision":1,"digest":"%s"}]`, digest)
	invalid := map[string]string{
		"nested body":      fmt.Sprintf(`[{"contractType":"rich_message_part","id":"asset_1","revision":1,"digest":"%s","metadata":{"body":"transcript"}}]`, digest),
		"unknown field":    fmt.Sprintf(`[{"contractType":"rich_message_part","id":"asset_1","revision":1,"digest":"%s","label":"x"}]`, digest),
		"string revision":  fmt.Sprintf(`[{"contractType":"rich_message_part","id":"asset_1","revision":"1","digest":"%s"}]`, digest),
		"bad digest":       `[{"contractType":"rich_message_part","id":"asset_1","revision":1,"digest":"short"}]`,
		"unknown contract": fmt.Sprintf(`[{"contractType":"unreviewed_type","id":"asset_1","revision":1,"digest":"%s"}]`, digest),
		"scalar item":      `["asset_1"]`,
		"not an array":     `{"contractType":"rich_message_part"}`,
	}
	var validResult bool
	if err := store.pool.QueryRow(ctx, "SELECT stride_structured_refs_are_valid($1::jsonb)", valid).Scan(&validResult); err != nil || !validResult {
		t.Fatalf("valid structured ref accepted=%t err=%v", validResult, err)
	}
	for name, payload := range invalid {
		t.Run(name, func(t *testing.T) {
			var got bool
			if err := store.pool.QueryRow(ctx, "SELECT stride_structured_refs_are_valid($1::jsonb)", payload).Scan(&got); err != nil || got {
				t.Fatalf("malformed structured ref accepted=%t err=%v payload=%s", got, err, payload)
			}
		})
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_contract_revisions (
		tenant_id,contract_type,contract_id,revision,schema_version,content_digest,acl_version,audience_digest,
		retention_policy,purge_generation,status,audit,created_at
	) VALUES ('tenant','test_contract','contract_nested_secret',1,1,decode($1,'hex'),1,decode($1,'hex'),
		'retain',0,'draft',$2::jsonb,now())`, digest, `{"metadata":{"api_key":"secret"}}`); err == nil {
		t.Fatal("stride_contract_revisions constraint accepted a nested credential")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_conversation_events (
		tenant_id,event_id,event_revision,sequence,schema_version,idempotency_key,source_type,source_id,
		author_principal,author_name,occurred_at,ingested_at,event_type,content_revision,content_digest,
		audience_digest,visibility,acl_version,retention_policy,purge_generation,provenance,structured_refs
	) VALUES ('tenant','event_malformed_ref',1,1,1,'idem_malformed_ref','channel','team','user_1','User',now(),now(),
		'message',1,decode($1,'hex'),decode($1,'hex'),'channel',1,'retain',0,'client',$2::jsonb)`, digest, invalid["nested body"]); err == nil {
		t.Fatal("stride_conversation_events constraint accepted a body-bearing structured reference")
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

func TestPostgresCanonicalLegacyImportRetryIgnoresCollectionTimestampDrift(t *testing.T) {
	ctx, pool := startDisposableCanonicalPostgres(t)
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresCanonicalStore(pool, registry)
	if err := store.ApplyMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	originals := make([]CanonicalEvent, 26)
	for index := range originals {
		originals[index] = canonicalLegacyBoardTestEvent(t, registry, fmt.Sprintf("card-%03d", index), false)
		if _, err := store.Append(ctx, originals[index]); err != nil {
			t.Fatal(err)
		}
	}
	var sequenceBefore, outboxBefore int64
	if err := store.pool.QueryRow(ctx, "SELECT max(sequence) FROM canonical_events").Scan(&sequenceBefore); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM outbox").Scan(&outboxBefore); err != nil {
		t.Fatal(err)
	}
	for _, original := range originals {
		retry := original
		retry.OccurredAt = retry.OccurredAt.Add(18 * 24 * time.Hour)
		retry.RecordedAt = retry.RecordedAt.Add(18 * 24 * time.Hour)
		result, err := store.Append(ctx, retry)
		if err != nil || !result.Existing || !result.Event.OccurredAt.Equal(original.OccurredAt) {
			t.Fatalf("legacy retry result=%+v err=%v", result, err)
		}
	}
	for index := 26; index < 165; index++ {
		missing := canonicalLegacyBoardTestEvent(t, registry, fmt.Sprintf("card-%03d", index), false)
		if result, err := store.Append(ctx, missing); err != nil || result.Existing {
			t.Fatalf("new import result=%+v err=%v", result, err)
		}
	}
	var eventCount, objectCount, outboxCount, sequenceAfter int64
	if err := store.pool.QueryRow(ctx, "SELECT count(*), max(sequence) FROM canonical_events").Scan(&eventCount, &sequenceAfter); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM objects").Scan(&objectCount); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM outbox").Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if sequenceBefore != 26 || outboxBefore != 26 || eventCount != 165 || objectCount != 165 || outboxCount != 165 || sequenceAfter != 165 {
		t.Fatalf("retry/new counts before=(%d,%d) after=(events:%d objects:%d outbox:%d sequence:%d)", sequenceBefore, outboxBefore, eventCount, objectCount, outboxCount, sequenceAfter)
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

func TestPostgresCanonicalAcceptsExactLegacyImportAsFirstObservedBaseline(t *testing.T) {
	ctx, pool := startDisposableCanonicalPostgres(t)
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresCanonicalStore(pool, registry)
	if err := store.ApplyMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	event := canonicalLegacyBaselineTestEvent(t, registry, "meeting", "meeting-observed-at-v2", 2)
	result, err := store.Append(ctx, event)
	if err != nil || result.Existing {
		t.Fatalf("legacy baseline append=%+v err=%v", result, err)
	}
	var stateRevision int64
	if err := pool.QueryRow(ctx, `SELECT state_revision FROM objects WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3`,
		event.TenantID, event.AggregateType, event.AggregateID).Scan(&stateRevision); err != nil {
		t.Fatal(err)
	}
	if stateRevision != 2 {
		t.Fatalf("legacy baseline state revision=%d, want 2", stateRevision)
	}
	checkpoint := canonicalLegacyBaselineTestEvent(t, registry, "meeting", event.AggregateID, 5)
	if _, err := store.Append(ctx, checkpoint); err != nil {
		t.Fatalf("legacy checkpoint append: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT state_revision FROM objects WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3`,
		event.TenantID, event.AggregateType, event.AggregateID).Scan(&stateRevision); err != nil {
		t.Fatal(err)
	}
	if stateRevision != 5 {
		t.Fatalf("legacy checkpoint state revision=%d, want 5", stateRevision)
	}
}

// A legacy checkpoint may bridge missing snapshots only inside a legacy-import
// history. A caller-controlled Actor field is not authority to jump over a
// native canonical event for the same aggregate.
func TestPostgresCanonicalLegacyCheckpointCannotJumpOverNativeHistory(t *testing.T) {
	ctx, pool := startDisposableCanonicalPostgres(t)
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("artifact.revised", 1, CanonicalPayloadSchema{Fields: map[string]CanonicalPayloadField{
		"artifact_id":      {Kind: CanonicalPayloadIdentifier, Required: true},
		"content_revision": {Kind: CanonicalPayloadRevision, Required: true},
		"content_sha256":   {Kind: CanonicalPayloadDigest, Required: true},
		"content_ref":      {Kind: CanonicalPayloadContentRef},
		"visibility":       {Kind: CanonicalPayloadEnum, Required: true, Enums: []string{"private", "organization"}},
	}}); err != nil {
		t.Fatal(err)
	}
	store := NewPostgresCanonicalStore(pool, registry)
	if err := store.ApplyMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	native := canonicalTestEvent(t, registry, uuid.New(), "meeting-native-history", 1, "native-meeting-v1", "private")
	native.TenantID = "tenant-a"
	native.AggregateType = "meeting"
	if _, err := store.Append(ctx, native); err != nil {
		t.Fatal(err)
	}
	checkpoint := canonicalLegacyBaselineTestEvent(t, registry, "meeting", native.AggregateID, 5)
	if _, err := store.Append(ctx, checkpoint); !errors.Is(err, ErrCanonicalProjectionOrder) {
		t.Fatalf("legacy checkpoint over native history error=%v, want ErrCanonicalProjectionOrder", err)
	}
	var events, objects, outbox int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM canonical_events), (SELECT count(*) FROM objects), (SELECT count(*) FROM outbox)`).
		Scan(&events, &objects, &outbox); err != nil {
		t.Fatal(err)
	}
	if events != 1 || objects != 1 || outbox != 1 {
		t.Fatalf("failed checkpoint was not transactional: events=%d objects=%d outbox=%d", events, objects, outbox)
	}
}

func TestPostgresCanonicalConcurrentLegacyCheckpointsCannotRegressProjection(t *testing.T) {
	ctx, pool := startDisposableCanonicalPostgres(t)
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresCanonicalStore(pool, registry)
	if err := store.ApplyMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	baseline := canonicalLegacyBaselineTestEvent(t, registry, "meeting", "meeting-concurrent-checkpoints", 2)
	if _, err := store.Append(ctx, baseline); err != nil {
		t.Fatal(err)
	}
	checkpoints := []CanonicalEvent{
		canonicalLegacyBaselineTestEvent(t, registry, "meeting", baseline.AggregateID, 5),
		canonicalLegacyBaselineTestEvent(t, registry, "meeting", baseline.AggregateID, 8),
	}
	entered := make(chan struct{}, len(checkpoints))
	release := make(chan struct{})
	store.Failpoint = func(point string) error {
		if point == "after_event_before_projection" {
			entered <- struct{}{}
			<-release
		}
		return nil
	}
	type appendOutcome struct {
		index int
		err   error
	}
	outcomes := make(chan appendOutcome, len(checkpoints))
	var wait sync.WaitGroup
	for index, event := range checkpoints {
		wait.Add(1)
		go func(index int, event CanonicalEvent) {
			defer wait.Done()
			_, appendErr := store.Append(ctx, event)
			outcomes <- appendOutcome{index: index, err: appendErr}
		}(index, event)
	}
	for range checkpoints {
		<-entered
	}
	close(release)
	wait.Wait()
	close(outcomes)
	store.Failpoint = nil
	failed := map[int]error{}
	for outcome := range outcomes {
		if outcome.err != nil {
			failed[outcome.index] = outcome.err
		}
	}
	// Regardless of which serializable transaction won, replaying the highest
	// checkpoint must converge the projection at v8 without stale overwrite.
	if _, err := store.Append(ctx, checkpoints[1]); err != nil {
		t.Fatalf("retry highest checkpoint after concurrent append: first failures=%v retry=%v", failed, err)
	}
	stale, err := store.Append(ctx, checkpoints[0])
	if err == nil && !stale.Existing {
		t.Fatal("stale lower checkpoint created a new event")
	}
	if err != nil && !errors.Is(err, ErrCanonicalProjectionOrder) && !errors.Is(err, ErrCanonicalAggregateConflict) {
		t.Fatalf("stale lower checkpoint error=%v, want an idempotent existing result or projection/aggregate conflict", err)
	}
	var revision, lastSequence, projectedEventVersion int64
	if err := pool.QueryRow(ctx, `SELECT o.state_revision,o.last_event_sequence,e.aggregate_version
		FROM objects o JOIN canonical_events e ON e.sequence=o.last_event_sequence
		WHERE o.tenant_id=$1 AND o.object_type=$2 AND o.object_id=$3`, baseline.TenantID, baseline.AggregateType, baseline.AggregateID).
		Scan(&revision, &lastSequence, &projectedEventVersion); err != nil {
		t.Fatal(err)
	}
	if revision != 8 || projectedEventVersion != 8 || lastSequence < 1 {
		t.Fatalf("concurrent checkpoint projection revision=%d eventVersion=%d sequence=%d, want v8", revision, projectedEventVersion, lastSequence)
	}
}

func TestPostgresCanonicalRejectsForgedLegacyBaselineTransactionally(t *testing.T) {
	ctx, pool := startDisposableCanonicalPostgres(t)
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresCanonicalStore(pool, registry)
	if err := store.ApplyMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	event := canonicalLegacyBaselineTestEvent(t, registry, "meeting", "meeting-forged-v2", 2)
	event.Actor.ID = "canonical-runtime"
	if _, err := store.Append(ctx, event); !errors.Is(err, ErrCanonicalProjectionOrder) {
		t.Fatalf("forged legacy baseline error=%v, want ErrCanonicalProjectionOrder", err)
	}
	for _, table := range []string{"canonical_events", "objects", "outbox"} {
		var got int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != 0 {
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
