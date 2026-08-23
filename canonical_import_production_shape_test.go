//go:build canonicalperf

package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	canonicalProductionShapeObjects = 32844
	canonicalProductionShapeGrants  = 54790
)

type canonicalImportPerfTrace struct {
	totalQueries int
	eventSets    int
	objectSets   int
	grantSets    int
	eventInserts int
	grantUpserts int
	grantDeletes int
	objectWrites int
	outboxWrites int
	otherWrites  int
}

func canonicalProductionShapePlan(t *testing.T, registry *CanonicalPayloadRegistry, tenant string, changedIndex int, version int64, roomID string) CanonicalImportPlan {
	t.Helper()
	plan := canonicalDeltaImportPlan(t, registry, tenant, canonicalProductionShapeObjects, changedIndex, version, roomID, false)
	doubleGrantObjects := canonicalProductionShapeGrants - canonicalProductionShapeObjects
	if doubleGrantObjects < 0 || doubleGrantObjects > len(plan.Objects) {
		t.Fatalf("invalid production corpus shape objects=%d grants=%d", canonicalProductionShapeObjects, canonicalProductionShapeGrants)
	}
	for index := doubleGrantObjects; index < len(plan.Objects); index++ {
		plan.Objects[index].ImportGrants = plan.Objects[index].ImportGrants[:1]
	}
	return plan
}

func canonicalImportPerfSnapshot(tracer *canonicalQueryBudgetTracer) canonicalImportPerfTrace {
	tracer.mu.Lock()
	queries := append([]string(nil), tracer.queries...)
	tracer.mu.Unlock()
	result := canonicalImportPerfTrace{totalQueries: len(queries)}
	for _, query := range queries {
		normalized := strings.ToUpper(strings.TrimSpace(query))
		switch {
		case strings.Contains(query, "event_id=ANY($2::uuid[])"):
			result.eventSets++
		case strings.Contains(query, "FROM objects o JOIN unnest"):
			result.objectSets++
		case strings.Contains(query, "FROM object_grants g JOIN unnest"):
			result.grantSets++
		}
		switch {
		case strings.Contains(query, "INSERT INTO canonical_events ("):
			result.eventInserts++
		case strings.Contains(query, "INSERT INTO object_grants ("):
			result.grantUpserts++
		case strings.Contains(query, "DELETE FROM object_grants"):
			result.grantDeletes++
		case strings.Contains(query, "INSERT INTO objects (") || strings.HasPrefix(normalized, "UPDATE OBJECTS "):
			result.objectWrites++
		case strings.Contains(query, "INSERT INTO outbox("):
			result.outboxWrites++
		default:
			if strings.HasPrefix(normalized, "INSERT ") || strings.HasPrefix(normalized, "UPDATE ") || strings.HasPrefix(normalized, "DELETE ") {
				result.otherWrites++
			}
		}
	}
	return result
}

func canonicalProductionShapeCounts(t *testing.T, ctx context.Context, store *PostgresCanonicalStore, tenant string) (events, objects, grants int64) {
	t.Helper()
	queries := []struct {
		name string
		sql  string
		out  *int64
	}{
		{"events", `SELECT count(*) FROM canonical_events WHERE tenant_id=$1`, &events},
		{"objects", `SELECT count(*) FROM objects WHERE tenant_id=$1`, &objects},
		{"grants", `SELECT count(*) FROM object_grants WHERE tenant_id=$1 AND granted_by_type='service' AND granted_by_id='canonical-import'`, &grants},
	}
	for _, query := range queries {
		if err := store.pool.QueryRow(ctx, query.sql, tenant).Scan(query.out); err != nil {
			t.Fatalf("count production-shape %s: %v", query.name, err)
		}
	}
	return events, objects, grants
}

func TestPostgresCanonicalImportProductionShapePerformance(t *testing.T) {
	bootstrapCtx, pool := startDisposableCanonicalPostgres(t)
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresCanonicalStore(pool, registry)
	if err := store.ApplyMigrations(bootstrapCtx); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	const tenant = "production-shape"
	baseline := canonicalProductionShapePlan(t, registry, tenant, -1, 1, "office")
	actualGrants := 0
	for _, object := range baseline.Objects {
		actualGrants += len(object.ImportGrants)
	}
	if len(baseline.Objects) != canonicalProductionShapeObjects || len(baseline.Events) != canonicalProductionShapeObjects || actualGrants != canonicalProductionShapeGrants {
		t.Fatalf("corpus objects=%d events=%d grants=%d, want %d/%d/%d", len(baseline.Objects), len(baseline.Events), actualGrants, canonicalProductionShapeObjects, canonicalProductionShapeObjects, canonicalProductionShapeGrants)
	}

	seedEventsStarted := time.Now()
	if err := baseline.Apply(ctx, store); err != nil {
		t.Fatalf("seed production-shape events: %v", err)
	}
	seedEventsElapsed := time.Since(seedEventsStarted)
	seedGrantsStarted := time.Now()
	if err := store.SyncImportGrants(ctx, baseline); err != nil {
		t.Fatalf("seed production-shape grants: %v", err)
	}
	seedGrantsElapsed := time.Since(seedGrantsStarted)
	seedEvents, seedObjects, seedGrants := canonicalProductionShapeCounts(t, ctx, store, tenant)
	if seedEvents != canonicalProductionShapeObjects || seedObjects != canonicalProductionShapeObjects || seedGrants != canonicalProductionShapeGrants {
		t.Fatalf("seed cardinality events=%d objects=%d grants=%d, want %d/%d/%d", seedEvents, seedObjects, seedGrants, canonicalProductionShapeObjects, canonicalProductionShapeObjects, canonicalProductionShapeGrants)
	}

	wantBatches := (canonicalProductionShapeObjects + canonicalImportPrefetchBatchSize - 1) / canonicalImportPrefetchBatchSize
	noopStore, noopTracer := canonicalTracedImportStore(t, ctx, store, registry)
	noopStarted := time.Now()
	if err := baseline.Apply(ctx, noopStore); err != nil {
		t.Fatalf("no-op production-shape apply: %v", err)
	}
	if err := noopStore.SyncImportGrants(ctx, baseline); err != nil {
		t.Fatalf("no-op production-shape sync: %v", err)
	}
	noopElapsed := time.Since(noopStarted)
	noop := canonicalImportPerfSnapshot(noopTracer)
	if noop.eventInserts != 0 || noop.grantUpserts != 0 || noop.grantDeletes != 0 || noop.objectWrites != 0 || noop.outboxWrites != 0 || noop.otherWrites != 0 {
		t.Fatalf("no-op emitted DML: %+v", noop)
	}
	if noop.eventSets != wantBatches || noop.objectSets != wantBatches || noop.grantSets != wantBatches {
		t.Fatalf("no-op batch query counts=%+v, want %d event/object/grant sets", noop, wantBatches)
	}
	if noop.totalQueries > 3*wantBatches+4 {
		t.Fatalf("no-op total queries=%d, want <=%d", noop.totalQueries, 3*wantBatches+4)
	}

	// Keep the changed object in the two-grant portion of the corpus, so this
	// exercises one event/projection/outbox write and exactly two grant repairs.
	const changedIndex = 17923
	delta := canonicalProductionShapePlan(t, registry, tenant, changedIndex, 2, "studio")
	deltaStore, deltaTracer := canonicalTracedImportStore(t, ctx, store, registry)
	deltaStarted := time.Now()
	if err := delta.Apply(ctx, deltaStore); err != nil {
		t.Fatalf("one-object production-shape apply: %v", err)
	}
	if err := deltaStore.SyncImportGrants(ctx, delta); err != nil {
		t.Fatalf("one-object production-shape sync: %v", err)
	}
	deltaElapsed := time.Since(deltaStarted)
	deltaTrace := canonicalImportPerfSnapshot(deltaTracer)
	if deltaTrace.eventInserts != 1 || deltaTrace.grantUpserts != 2 || deltaTrace.grantDeletes != 0 || deltaTrace.objectWrites != 1 || deltaTrace.outboxWrites != 1 || deltaTrace.otherWrites != 0 {
		t.Fatalf("one-object delta DML is not O(1): %+v", deltaTrace)
	}
	if deltaTrace.eventSets != wantBatches || deltaTrace.objectSets != wantBatches || deltaTrace.grantSets != wantBatches {
		t.Fatalf("delta batch query counts=%+v, want %d event/object/grant sets", deltaTrace, wantBatches)
	}
	if deltaTrace.totalQueries > noop.totalQueries+12 {
		t.Fatalf("delta total queries=%d no-op=%d, want bounded additive overhead <=12", deltaTrace.totalQueries, noop.totalQueries)
	}
	afterEvents, afterObjects, afterGrants := canonicalProductionShapeCounts(t, ctx, store, tenant)
	if afterEvents != canonicalProductionShapeObjects+1 || afterObjects != canonicalProductionShapeObjects || afterGrants != canonicalProductionShapeGrants {
		t.Fatalf("delta cardinality events=%d objects=%d grants=%d, want %d/%d/%d", afterEvents, afterObjects, afterGrants, canonicalProductionShapeObjects+1, canonicalProductionShapeObjects, canonicalProductionShapeGrants)
	}

	// Keep the log stable and machine-readable for release evidence.
	measurements := []string{
		fmt.Sprintf("seed_events=%s", seedEventsElapsed.Round(time.Millisecond)),
		fmt.Sprintf("seed_grants=%s", seedGrantsElapsed.Round(time.Millisecond)),
		fmt.Sprintf("noop_apply_sync=%s", noopElapsed.Round(time.Millisecond)),
		fmt.Sprintf("delta_apply_sync=%s", deltaElapsed.Round(time.Millisecond)),
		fmt.Sprintf("noop_queries=%d", noop.totalQueries),
		fmt.Sprintf("delta_queries=%d", deltaTrace.totalQueries),
	}
	sort.Strings(measurements)
	t.Logf("canonical production-shape gate: objects=%d grants=%d batches=%d %s noop_trace=%+v delta_trace=%+v", canonicalProductionShapeObjects, canonicalProductionShapeGrants, wantBatches, strings.Join(measurements, " "), noop, deltaTrace)
}
