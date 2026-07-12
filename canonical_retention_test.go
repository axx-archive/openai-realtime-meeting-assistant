package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func retentionFixture(kind string) (RetentionHeader, RetentionBody) {
	key := RetentionKey{TenantID: "tenant-1", ObjectID: "artifact-1", RevisionID: "revision-1"}
	created := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	header := RetentionHeader{
		Key: key, EventType: "artifact.revised", OccurredAt: created, RecordedAt: created.Add(time.Second),
		ActorPseudonymousID: "hmac-sha256:" + strings.Repeat("b", 64), ContentDigest: strings.Repeat("a", 64), Classification: "internal",
	}
	body := RetentionBody{
		BodyID: "body-1", Key: key, Kind: kind, CreatedAt: created, Bytes: []byte("erasable secret body"),
		References: map[RetentionResourceClass][]string{
			RetentionRevisionBody: {"body-1"}, RetentionBlob: {"blob-a", "blob-b"}, RetentionEmbedding: {"embedding-1"},
			RetentionDigest: {"digest-1"}, RetentionExcerpt: {"excerpt-1"}, RetentionCache: {"cache-key"},
			RetentionExport: {"render-1"}, RetentionBackup: {"backup-manifest-1"},
		},
	}
	if kind == "raw_audio" {
		until := RawAudioRetainUntil(created)
		body.RetainUntil = &until
	}
	return header, body
}

func TestRawAudioRetentionIsExactly72Hours(t *testing.T) {
	header, body := retentionFixture("raw_audio")
	if got := body.RetainUntil.Sub(body.CreatedAt); got != 72*time.Hour {
		t.Fatalf("TTL = %s", got)
	}
	if RawAudioPurgeDue(body, body.RetainUntil.Add(-time.Nanosecond)) {
		t.Fatal("raw audio was due before its TTL")
	}
	if !RawAudioPurgeDue(body, *body.RetainUntil) {
		t.Fatal("raw audio was not due at its TTL")
	}
	bad := body
	until := body.CreatedAt.Add(71 * time.Hour)
	bad.RetainUntil = &until
	if err := validateRetentionPair(header, bad); !errors.Is(err, ErrRetentionInvalid) {
		t.Fatalf("invalid TTL error = %v", err)
	}
}

func TestRetentionSeparatesImmutableHeaderAndErasableBody(t *testing.T) {
	ctx := context.Background()
	ledger := NewMemoryPurgeLedger()
	store := NewMemoryRetentionStore(ledger)
	header, body := retentionFixture("artifact_body")
	if existing, err := store.Register(ctx, header, body); err != nil || existing {
		t.Fatalf("register existing=%v err=%v", existing, err)
	}
	read, err := store.ReadBody(ctx, header.Key)
	if err != nil || string(read.Bytes) != string(body.Bytes) {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	read.Bytes[0] = 'X'
	readAgain, _ := store.ReadBody(ctx, header.Key)
	if string(readAgain.Bytes) != string(body.Bytes) {
		t.Fatal("body escaped by reference")
	}
	record, err := store.SoftDelete(ctx, header.Key, header.RecordedAt.Add(time.Hour))
	if err != nil || record.State != RetentionSoftDeleted || !record.BodyPresent || record.Header != header {
		t.Fatalf("soft delete record=%+v err=%v", record, err)
	}
	if _, err := store.ReadBody(ctx, header.Key); !errors.Is(err, ErrRetentionReadDenied) {
		t.Fatalf("soft-deleted body read error = %v", err)
	}
	// Idempotent retry keeps the original deletion instant.
	first := *record.SoftDeletedAt
	record, _ = store.SoftDelete(ctx, header.Key, first.Add(time.Hour))
	if !record.SoftDeletedAt.Equal(first) {
		t.Fatalf("soft-delete timestamp changed: %s -> %s", first, record.SoftDeletedAt)
	}
}

func TestLegalHoldBlocksEveryPurgeTransitionButNotAccessRevocation(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryRetentionStore(NewMemoryPurgeLedger())
	header, body := retentionFixture("artifact_body")
	_, _ = store.Register(ctx, header, body)
	if _, err := store.SetLegalHold(ctx, header.Key, "case-17", true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SoftDelete(ctx, header.Key, header.RecordedAt); err != nil {
		t.Fatalf("legal hold blocked access revocation: %v", err)
	}
	if _, err := store.ReadBody(ctx, header.Key); !errors.Is(err, ErrRetentionReadDenied) {
		t.Fatalf("held soft delete read error = %v", err)
	}
	if _, err := store.PlanPurge(ctx, header.Key, "policy-1", header.RecordedAt); !errors.Is(err, ErrRetentionLegalHold) {
		t.Fatalf("plan under hold error = %v", err)
	}
	_, _ = store.SetLegalHold(ctx, header.Key, "", false)
	if _, err := store.PlanPurge(ctx, header.Key, "policy-1", header.RecordedAt); err != nil {
		t.Fatal(err)
	}
	_, _ = store.SetLegalHold(ctx, header.Key, "case-18", true)
	if _, err := store.BeginPurge(ctx, header.Key); !errors.Is(err, ErrRetentionLegalHold) {
		t.Fatalf("begin under hold error = %v", err)
	}
	if _, err := store.CompletePurgeTarget(ctx, header.Key, RetentionBlob, "deleted"); !errors.Is(err, ErrRetentionLegalHold) {
		t.Fatalf("target under hold error = %v", err)
	}
	if _, err := store.FinalizePurge(ctx, header.Key); !errors.Is(err, ErrRetentionLegalHold) {
		t.Fatalf("finalize under hold error = %v", err)
	}
}

func TestPurgePlanCoversEveryLeakSurfaceAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryRetentionStore(NewMemoryPurgeLedger())
	header, body := retentionFixture("artifact_body")
	_, _ = store.Register(ctx, header, body)
	plan, err := store.PlanPurge(ctx, header.Key, "policy-1", header.RecordedAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Tasks) != len(mandatoryPurgeClasses) {
		t.Fatalf("task count=%d", len(plan.Tasks))
	}
	for index, class := range mandatoryPurgeClasses {
		if plan.Tasks[index].Class != class {
			t.Fatalf("task %d class=%q want %q", index, plan.Tasks[index].Class, class)
		}
	}
	for field := range plan.Tombstone {
		if _, ok := retentionTombstoneAllowlist[field]; !ok {
			t.Fatalf("unsafe planned tombstone field %q", field)
		}
	}
	retry, err := store.PlanPurge(ctx, header.Key, "policy-1", header.RecordedAt.Add(2*time.Hour))
	if err != nil || retry.ID != plan.ID || !retry.CreatedAt.Equal(plan.CreatedAt) {
		t.Fatalf("retry=%+v err=%v", retry, err)
	}
	if _, err := store.PlanPurge(ctx, header.Key, "other-policy", header.RecordedAt); !errors.Is(err, ErrRetentionInvalid) {
		t.Fatalf("policy conflict error=%v", err)
	}
}

func TestPurgeStateMachineRequiresAllEvidenceAndWritesLedgerBeforeErase(t *testing.T) {
	ctx := context.Background()
	ledger := NewMemoryPurgeLedger()
	store := NewMemoryRetentionStore(ledger)
	header, body := retentionFixture("artifact_body")
	_, _ = store.Register(ctx, header, body)
	_, _ = store.PlanPurge(ctx, header.Key, "policy-1", header.RecordedAt)
	if record, err := store.BeginPurge(ctx, header.Key); err != nil || record.State != RetentionPurging {
		t.Fatalf("begin=%+v err=%v", record, err)
	}
	if _, err := store.FinalizePurge(ctx, header.Key); !errors.Is(err, ErrRetentionIncomplete) {
		t.Fatalf("early finalize error=%v", err)
	}
	for _, class := range mandatoryPurgeClasses {
		if _, err := store.CompletePurgeTarget(ctx, header.Key, class, "deleted"); err != nil {
			t.Fatalf("complete %s: %v", class, err)
		}
	}
	record, err := store.FinalizePurge(ctx, header.Key)
	if err != nil || record.State != RetentionPurged || record.BodyPresent || record.Header != header {
		t.Fatalf("final=%+v err=%v", record, err)
	}
	if !safeRetentionTombstone(record.Plan.Tombstone) {
		t.Fatalf("final tombstone unsafe: %+v", record.Plan.Tombstone)
	}
	for _, task := range record.Plan.Tasks {
		if len(task.References) != 0 {
			t.Fatalf("purged task retained source refs: %+v", task)
		}
	}
	entry, found, err := ledger.LookupPurge(ctx, header.Key)
	if err != nil || !found || len(entry.DestructionEvidence) != len(mandatoryPurgeClasses) {
		t.Fatalf("ledger=%+v found=%v err=%v", entry, found, err)
	}
	if _, err := store.ReadBody(ctx, header.Key); !errors.Is(err, ErrRetentionReadDenied) {
		t.Fatalf("purged read error=%v", err)
	}
	// Terminal retries are no-ops and cannot resurrect the body.
	retry, err := store.FinalizePurge(ctx, header.Key)
	if err != nil || retry.State != RetentionPurged || retry.BodyPresent {
		t.Fatalf("retry=%+v err=%v", retry, err)
	}
}

func TestFinalizeFailsClosedWithoutDurablePurgeLedger(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryRetentionStore(nil)
	header, body := retentionFixture("artifact_body")
	_, _ = store.Register(ctx, header, body)
	_, _ = store.PlanPurge(ctx, header.Key, "policy-1", header.RecordedAt)
	_, _ = store.BeginPurge(ctx, header.Key)
	for _, class := range mandatoryPurgeClasses {
		_, _ = store.CompletePurgeTarget(ctx, header.Key, class, "deleted")
	}
	if _, err := store.FinalizePurge(ctx, header.Key); !errors.Is(err, ErrRetentionRestoreGate) {
		t.Fatalf("finalize error=%v", err)
	}
	record, _ := store.Record(ctx, header.Key)
	if !record.BodyPresent || record.State == RetentionPurged {
		t.Fatalf("body erased before purge ledger: %+v", record)
	}
}

func TestRestoreGateRejectsPurgedBodiesDerivedDataBackupsAndUnsafeTombstones(t *testing.T) {
	ctx := context.Background()
	ledger, key, tombstone := completedPurgeFixture(t)
	gate := RestorePurgeLedgerGate{Ledger: ledger}
	for _, class := range mandatoryPurgeClasses {
		err := gate.Validate(ctx, []RestoreCandidate{{Key: key, Class: class}})
		if !errors.Is(err, ErrRetentionRestoreResurrection) {
			t.Fatalf("class %s restore error=%v", class, err)
		}
	}
	unsafe := cloneStringMap(tombstone)
	unsafe["title"] = "secret title"
	if err := gate.Validate(ctx, []RestoreCandidate{{Key: key, Class: RetentionTombstone, TombstoneFields: unsafe}}); !errors.Is(err, ErrRetentionRestoreResurrection) {
		t.Fatalf("unsafe tombstone error=%v", err)
	}
	if err := gate.Validate(ctx, []RestoreCandidate{{Key: key, Class: RetentionTombstone, TombstoneFields: tombstone}}); err != nil {
		t.Fatalf("safe tombstone rejected: %v", err)
	}
	if err := (RestorePurgeLedgerGate{}).Validate(ctx, nil); !errors.Is(err, ErrRetentionRestoreGate) {
		t.Fatalf("missing ledger error=%v", err)
	}
}

func TestRestoreGateRejectsSensitiveOrNoncanonicalTombstoneValues(t *testing.T) {
	ctx := context.Background()
	ledger, key, tombstone := completedPurgeFixture(t)
	gate := RestorePurgeLedgerGate{Ledger: ledger}
	cases := []struct {
		name  string
		field string
		value string
	}{
		{"actor name", "actor_pseudonymous_id", "Alice Smith"},
		{"actor email", "actor_pseudonymous_id", "alice@example.com"},
		{"actor phone", "actor_pseudonymous_id", "+1-310-555-0199"},
		{"event prose", "event_type", "Customer asked us to delete this"},
		{"spaced tenant", "tenant_id", "tenant one"},
		{"spaced object", "object_id", "private filename.pdf"},
		{"invalid occurred timestamp", "occurred_at", "yesterday"},
		{"offset occurred timestamp", "occurred_at", "2026-07-12T05:00:00-07:00"},
		{"noncanonical fractional timestamp", "recorded_at", "2026-07-12T12:00:01.000000000Z"},
		{"uppercase content digest", "content_digest", strings.Repeat("A", 64)},
		{"nondigest content", "content_digest", "customer-secret"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneStringMap(tombstone)
			candidate[test.field] = test.value
			err := gate.Validate(ctx, []RestoreCandidate{{Key: key, Class: RetentionTombstone, TombstoneFields: candidate}})
			if !errors.Is(err, ErrRetentionRestoreResurrection) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestRetentionConcurrentSoftDeleteAndHoldRemainFailClosed(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryRetentionStore(NewMemoryPurgeLedger())
	header, body := retentionFixture("artifact_body")
	_, _ = store.Register(ctx, header, body)
	var wg sync.WaitGroup
	for index := 0; index < 20; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, _ = store.SoftDelete(ctx, header.Key, header.RecordedAt.Add(time.Duration(index)*time.Second))
			_, _ = store.SetLegalHold(ctx, header.Key, fmt.Sprintf("case-%d", index), true)
		}(index)
	}
	wg.Wait()
	if _, err := store.ReadBody(ctx, header.Key); !errors.Is(err, ErrRetentionReadDenied) {
		t.Fatalf("concurrent delete read error=%v", err)
	}
	record, _ := store.Record(ctx, header.Key)
	if !record.LegalHold || record.State != RetentionSoftDeleted {
		t.Fatalf("record=%+v", record)
	}
}

func TestRetentionRejectsUserTextInImmutableFieldsAndPurgeEvidence(t *testing.T) {
	header, body := retentionFixture("artifact_body")
	cases := []func(*RetentionHeader){
		func(h *RetentionHeader) { h.EventType = "A title with spaces" },
		func(h *RetentionHeader) { h.ActorPseudonymousID = "Alice Smith" },
		func(h *RetentionHeader) { h.ActorPseudonymousID = "alice@example.com" },
		func(h *RetentionHeader) { h.ActorPseudonymousID = "+1-310-555-0199" },
		func(h *RetentionHeader) { h.ActorPseudonymousID = strings.Repeat("b", 64) },
		func(h *RetentionHeader) { h.ContentDigest = strings.Repeat("A", 64) },
		func(h *RetentionHeader) { h.Classification = "customer secret" },
	}
	for index, mutate := range cases {
		candidate := header
		mutate(&candidate)
		if err := validateRetentionPair(candidate, body); !errors.Is(err, ErrRetentionInvalid) {
			t.Fatalf("case %d error=%v", index, err)
		}
	}
	store := NewMemoryRetentionStore(NewMemoryPurgeLedger())
	ctx := context.Background()
	_, _ = store.Register(ctx, header, body)
	_, _ = store.PlanPurge(ctx, header.Key, "policy-1", header.RecordedAt)
	_, _ = store.BeginPurge(ctx, header.Key)
	if _, err := store.CompletePurgeTarget(ctx, header.Key, RetentionBlob, "deleted from /secret/customer-file.pdf"); !errors.Is(err, ErrRetentionInvalid) {
		t.Fatalf("unsafe evidence error=%v", err)
	}
}

func completedPurgeFixture(t *testing.T) (*MemoryPurgeLedger, RetentionKey, map[string]string) {
	t.Helper()
	ctx := context.Background()
	ledger := NewMemoryPurgeLedger()
	store := NewMemoryRetentionStore(ledger)
	header, body := retentionFixture("artifact_body")
	_, _ = store.Register(ctx, header, body)
	_, _ = store.PlanPurge(ctx, header.Key, "policy-1", header.RecordedAt)
	_, _ = store.BeginPurge(ctx, header.Key)
	for _, class := range mandatoryPurgeClasses {
		_, _ = store.CompletePurgeTarget(ctx, header.Key, class, "deleted")
	}
	record, err := store.FinalizePurge(ctx, header.Key)
	if err != nil {
		t.Fatal(err)
	}
	return ledger, header.Key, cloneStringMap(record.Plan.Tombstone)
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
