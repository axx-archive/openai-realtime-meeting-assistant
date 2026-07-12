package main

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresPurgeLedgerSurvivesRepositoryRestartAndGatesRestore(t *testing.T) {
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	ledger := NewPostgresPurgeLedger(canonical.pool)
	header, body := retentionFixture("artifact_body")
	retention := NewMemoryRetentionStore(ledger)
	if _, err := retention.Register(ctx, header, body); err != nil {
		t.Fatal(err)
	}
	if _, err := retention.PlanPurge(ctx, header.Key, "policy-1", header.RecordedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := retention.BeginPurge(ctx, header.Key); err != nil {
		t.Fatal(err)
	}
	for _, class := range mandatoryPurgeClasses {
		if _, err := retention.CompletePurgeTarget(ctx, header.Key, class, "deleted"); err != nil {
			t.Fatal(err)
		}
	}
	purged, err := retention.FinalizePurge(ctx, header.Key)
	if err != nil {
		t.Fatal(err)
	}

	// A new pool and repository instance models process restart. The restore
	// gate reads durable PostgreSQL state, not the now-discarded in-memory store.
	restartedPool, err := pgxpool.New(ctx, canonical.pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restartedPool.Close)
	restarted := NewPostgresPurgeLedger(restartedPool)
	entry, found, err := restarted.LookupPurge(ctx, header.Key)
	if err != nil || !found || entry.ContentDigest != header.ContentDigest {
		t.Fatalf("restart lookup=%+v found=%v err=%v", entry, found, err)
	}

	gate := RestorePurgeLedgerGate{Ledger: restarted}
	for _, restoredClass := range mandatoryPurgeClasses {
		if err := gate.Validate(ctx, []RestoreCandidate{{Key: header.Key, Class: restoredClass}}); !errors.Is(err, ErrRetentionRestoreResurrection) {
			t.Fatalf("restored %s error=%v", restoredClass, err)
		}
	}
	if err := gate.Validate(ctx, []RestoreCandidate{{Key: header.Key, Class: RetentionTombstone, TombstoneFields: cloneStringMap(purged.Plan.Tombstone)}}); err != nil {
		t.Fatalf("safe tombstone rejected after restart: %v", err)
	}
}

func TestPostgresPurgeLedgerRetryIsIdempotentAndConflictFailsClosed(t *testing.T) {
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	ledger := NewPostgresPurgeLedger(canonical.pool)
	header, _ := retentionFixture("artifact_body")
	evidence := make(map[RetentionResourceClass]string, len(mandatoryPurgeClasses))
	for _, class := range mandatoryPurgeClasses {
		evidence[class] = "deleted"
	}
	entry := PurgeLedgerEntry{Key: header.Key, ContentDigest: header.ContentDigest, PolicyID: "policy-1", PurgedAt: header.RecordedAt.Add(123), DestructionEvidence: evidence}
	if err := ledger.RecordPurge(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if err := ledger.RecordPurge(ctx, entry); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	conflict := entry
	conflict.PolicyID = "policy-2"
	if err := ledger.RecordPurge(ctx, conflict); !errors.Is(err, ErrRetentionInvalid) {
		t.Fatalf("conflict error=%v", err)
	}
}

var _ PurgeLedger = (*PostgresPurgeLedger)(nil)
