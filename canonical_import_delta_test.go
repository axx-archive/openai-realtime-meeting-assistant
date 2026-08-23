package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func canonicalDeltaImportPlan(t *testing.T, registry *CanonicalPayloadRegistry, tenant string, count, changedIndex int, version int64, roomID string, deleted bool) CanonicalImportPlan {
	t.Helper()
	plan := CanonicalImportPlan{TenantID: tenant}
	for index := 0; index < count; index++ {
		objectID := fmt.Sprintf("delta-card-%04d", index)
		objectVersion := int64(1)
		objectRoom := "office"
		objectDeleted := false
		if index == changedIndex {
			objectVersion = version
			objectRoom = roomID
			objectDeleted = deleted
		}
		stateDigest := sha256Hex([]byte(fmt.Sprintf("%s/%s/v%d/%s/deleted=%t", tenant, objectID, objectVersion, objectRoom, objectDeleted)))
		contentDigest := sha256Hex([]byte("content/" + tenant + "/" + objectID))
		objectKey, err := CanonicalLegacyObjectKey("board_card", objectID)
		if err != nil {
			t.Fatal(err)
		}
		eventID, err := CanonicalImportEventID(tenant, "board_card", objectKey, canonicalLegacyImportEventType, stateDigest)
		if err != nil {
			t.Fatal(err)
		}
		payload, payloadDigest, err := NewCanonicalEventPayload(registry, canonicalLegacyImportEventType, 1, map[string]any{
			"object_id": objectID, "source_kind": "board_card", "source_revision": objectVersion,
			"room_id": NormalizeCanonicalRoomID(objectRoom), "status": "unknown", "payload_sha256": stateDigest,
			"deleted": objectDeleted, "content_revision": 1, "content_sha256": contentDigest,
			"content_ref": "legacy:board-card:" + objectID,
		})
		if err != nil {
			t.Fatal(err)
		}
		stamp := time.Date(2026, 8, 23, 12, 0, int(objectVersion), 0, time.UTC)
		event := CanonicalEvent{
			EventID: eventID, TenantID: tenant, AggregateType: "board_card", AggregateID: objectID,
			AggregateVersion: objectVersion, EventType: canonicalLegacyImportEventType, SchemaVersion: 1,
			OccurredAt: stamp, RecordedAt: stamp, Actor: CanonicalPrincipalRef{Kind: "service", ID: "legacy-import"},
			RoomID: NormalizeCanonicalRoomID(objectRoom), IdempotencyKey: "legacy-import/" + eventID.String(),
			Classification: "internal", ACLVersion: 1, Payload: payload, ContentRef: "legacy:board-card:" + objectID,
			PayloadSHA256: payloadDigest,
		}
		object := CanonicalImportedObject{
			Family: "board_card", ObjectID: objectID, ObjectKey: objectKey, StateDigest: stateDigest,
			AggregateVersion: objectVersion, EventID: eventID, RoomID: NormalizeCanonicalRoomID(objectRoom),
			ContentRevision: 1, ContentDigest: contentDigest, ContentRef: "legacy:board-card:" + objectID,
			Status: "unknown", OccurredAt: stamp, Deleted: objectDeleted, Visibility: "team",
		}
		if !objectDeleted {
			object.Principals = []string{"user:delta@example.com"}
			object.ImportGrants = []CanonicalImportGrant{
				{SubjectKind: ACLSubjectTeam, SubjectID: canonicalLegacyOrgTeamID, Action: ACLReadMetadata},
				{SubjectKind: ACLSubjectTeam, SubjectID: canonicalLegacyOrgTeamID, Action: ACLReadContent, Revision: 1},
			}
		}
		plan.Objects = append(plan.Objects, object)
		plan.Events = append(plan.Events, event)
	}
	return plan
}

func canonicalTracedImportStore(t *testing.T, ctx context.Context, store *PostgresCanonicalStore, registry *CanonicalPayloadRegistry) (*PostgresCanonicalStore, *canonicalQueryBudgetTracer) {
	t.Helper()
	tracer := &canonicalQueryBudgetTracer{}
	config := store.pool.Config()
	config.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return NewPostgresCanonicalStore(pool, registry), tracer
}

func canonicalImportMutationCounts(tracer *canonicalQueryBudgetTracer) [3]int {
	return [3]int{
		tracer.countContaining("INSERT INTO canonical_events ("),
		tracer.countContaining("INSERT INTO object_grants ("),
		tracer.countContaining("DELETE FROM object_grants"),
	}
}

func TestPostgresCanonicalImportNoOpDoesZeroDMLAndMutationWorkScalesWithDelta(t *testing.T) {
	ctx, pool := startDisposableCanonicalPostgres(t)
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresCanonicalStore(pool, registry)
	if err := store.ApplyMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	const smallCount = 4
	const largeCount = 320
	small := canonicalDeltaImportPlan(t, registry, "delta-small", smallCount, -1, 1, "office", false)
	large := canonicalDeltaImportPlan(t, registry, "delta-large", largeCount, -1, 1, "office", false)
	for _, plan := range []CanonicalImportPlan{small, large} {
		if err := plan.Apply(ctx, store); err != nil {
			t.Fatalf("seed %s events: %v", plan.TenantID, err)
		}
		if err := store.SyncImportGrants(ctx, plan); err != nil {
			t.Fatalf("seed %s grants: %v", plan.TenantID, err)
		}
	}

	noopStore, noopTrace := canonicalTracedImportStore(t, ctx, store, registry)
	if err := large.Apply(ctx, noopStore); err != nil {
		t.Fatal(err)
	}
	if err := noopStore.SyncImportGrants(ctx, large); err != nil {
		t.Fatal(err)
	}
	if got := canonicalImportMutationCounts(noopTrace); got != [3]int{} {
		t.Fatalf("no-op import DML=(events:%d grants:%d deletes:%d), want zero", got[0], got[1], got[2])
	}
	if pointRetries := noopTrace.countContaining("WHERE event_id=$1"); pointRetries != 0 {
		t.Fatalf("no-op import used %d point event retries, want bounded set prefetch only", pointRetries)
	}
	if eventSets := noopTrace.countContaining("event_id=ANY($2::uuid[])"); eventSets != 1 {
		t.Fatalf("no-op event set queries=%d, want 1 for %d objects", eventSets, largeCount)
	}
	if objectSets, grantSets := noopTrace.countContaining("FROM objects o JOIN unnest"), noopTrace.countContaining("FROM object_grants g JOIN unnest"); objectSets != 1 || grantSets != 1 {
		t.Fatalf("no-op grant prefetch queries objects=%d grants=%d, want 1/1", objectSets, grantSets)
	}

	changedSmall := canonicalDeltaImportPlan(t, registry, small.TenantID, smallCount, 2, 2, "studio", false)
	changedLarge := canonicalDeltaImportPlan(t, registry, large.TenantID, largeCount, 217, 2, "studio", false)
	var deltaCounts [][3]int
	for _, plan := range []CanonicalImportPlan{changedSmall, changedLarge} {
		deltaStore, deltaTrace := canonicalTracedImportStore(t, ctx, store, registry)
		if err := plan.Apply(ctx, deltaStore); err != nil {
			t.Fatalf("apply one-object delta to %s: %v", plan.TenantID, err)
		}
		if err := deltaStore.SyncImportGrants(ctx, plan); err != nil {
			t.Fatalf("sync one-object delta to %s: %v", plan.TenantID, err)
		}
		deltaCounts = append(deltaCounts, canonicalImportMutationCounts(deltaTrace))
	}
	wantDelta := [3]int{1, 2, 0}
	if deltaCounts[0] != wantDelta || deltaCounts[1] != wantDelta {
		t.Fatalf("one-object mutation DML small=%v large=%v, want %v independent of corpus size", deltaCounts[0], deltaCounts[1], wantDelta)
	}

	changedObject := changedLarge.Objects[217]
	humanGrantID := uuid.New()
	if _, err := store.pool.Exec(ctx, `INSERT INTO object_grants (
		grant_id,tenant_id,object_type,object_id,acl_version,subject_type,subject_id,action,granted_by_type,granted_by_id,conditions
	) VALUES ($1,$2,$3,$4,1,'user','human-reviewer','read_metadata','user','admin','{}'::jsonb)`,
		humanGrantID, changedLarge.TenantID, changedObject.Family, changedObject.ObjectID); err != nil {
		t.Fatal(err)
	}
	deleted := canonicalDeltaImportPlan(t, registry, large.TenantID, largeCount, 217, 3, "studio", true)
	deleteStore, deleteTrace := canonicalTracedImportStore(t, ctx, store, registry)
	if err := deleted.Apply(ctx, deleteStore); err != nil {
		t.Fatal(err)
	}
	if err := deleteStore.SyncImportGrants(ctx, deleted); err != nil {
		t.Fatal(err)
	}
	if got, want := canonicalImportMutationCounts(deleteTrace), [3]int{1, 0, 1}; got != want {
		t.Fatalf("one-object grant removal DML=%v, want %v", got, want)
	}
	var humanRows int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM object_grants WHERE grant_id=$1 AND granted_by_type='user' AND granted_by_id='admin'`, humanGrantID).Scan(&humanRows); err != nil || humanRows != 1 {
		t.Fatalf("human/admin grant rows=%d err=%v, want untouched", humanRows, err)
	}

	if err := large.Apply(ctx, store); err != nil {
		t.Fatalf("historical deterministic event retry: %v", err)
	}
	if err := store.SyncImportGrants(ctx, large); err == nil || !strings.Contains(err.Error(), "state revision mismatch") ||
		!strings.Contains(err.Error(), "projection=3") || !strings.Contains(err.Error(), "plan=1") {
		t.Fatalf("target-ahead plan did not fail closed: %v", err)
	}

	conflicted := large
	conflicted.Events = append([]CanonicalEvent(nil), large.Events...)
	conflicted.Events[0].Classification = "confidential"
	if err := conflicted.Apply(ctx, store); !errors.Is(err, ErrCanonicalIdempotencyConflict) {
		t.Fatalf("prefetched retry mismatch error=%v, want ErrCanonicalIdempotencyConflict", err)
	}
}
