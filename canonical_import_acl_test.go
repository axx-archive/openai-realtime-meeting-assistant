package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type canonicalQueryBudgetTracer struct {
	mu      sync.Mutex
	queries []string
}

func (tracer *canonicalQueryBudgetTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	tracer.mu.Lock()
	tracer.queries = append(tracer.queries, data.SQL)
	tracer.mu.Unlock()
	return ctx
}

func (*canonicalQueryBudgetTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (tracer *canonicalQueryBudgetTracer) countContaining(fragment string) int {
	tracer.mu.Lock()
	defer tracer.mu.Unlock()
	count := 0
	for _, query := range tracer.queries {
		if strings.Contains(query, fragment) {
			count++
		}
	}
	return count
}

func canonicalACLImportFixture(t *testing.T) (CanonicalImportPlan, *CanonicalPayloadRegistry) {
	t.Helper()
	dir := t.TempDir()
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	entries := []meetingMemoryEntry{
		{ID: "transcript-team", Kind: meetingMemoryKindTranscript, Text: "team transcript body", CreatedAt: now, Metadata: map[string]string{"roomId": "office", "meetingId": "meeting-1"}},
		{ID: "scout-private", Kind: meetingMemoryKindScoutChat, Text: "private thread body", CreatedAt: now.Add(time.Second), Metadata: map[string]string{"ownerEmail": "owner@example.com", "visibility": "private"}},
		{ID: "scout-public", Kind: meetingMemoryKindScoutChat, Text: "public channel body", CreatedAt: now.Add(2 * time.Second), Metadata: map[string]string{"ownerEmail": "owner@example.com", "visibility": scoutChatVisibilityPublic}},
		{ID: "artifact-private", Kind: meetingMemoryKindOSArtifact, Text: "private artifact body", CreatedAt: now.Add(3 * time.Second), Metadata: map[string]string{"ownerEmail": "owner@example.com", "visibility": "private"}},
		{ID: "artifact-team", Kind: meetingMemoryKindOSArtifact, Text: "team artifact body", CreatedAt: now.Add(4 * time.Second), Metadata: map[string]string{"ownerEmail": "owner@example.com", "visibility": "organization"}},
	}
	memoryPath := filepath.Join(dir, "meeting-memory.jsonl")
	file, err := os.OpenFile(memoryPath, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		raw, _ := json.Marshal(entry)
		if _, err := file.Write(append(raw, '\n')); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	notificationsPath := filepath.Join(dir, "notifications.json")
	notificationState := notificationStoreState{Notifications: []notificationRecord{
		{ID: "notification-private", UserEmail: "owner@example.com", Kind: "mention", Text: "private notification", CreatedAt: now.Format(time.RFC3339Nano)},
		{ID: "notification-team", Kind: "announcement", Text: "team notification", CreatedAt: now.Format(time.RFC3339Nano)},
	}}
	rawNotifications, _ := json.Marshal(notificationState)
	if err := os.WriteFile(notificationsPath, rawNotifications, 0o600); err != nil {
		t.Fatal(err)
	}
	boardPath := filepath.Join(dir, "kanban-board.json")
	boardState := kanbanBoardState{Cards: []kanbanCard{{ID: "board-team", Title: "Team card", Notes: "board body", Status: kanbanStatusBacklog}}, UpdatedAt: now.Format(time.RFC3339Nano)}
	rawBoard, _ := json.Marshal(boardState)
	if err := os.WriteFile(boardPath, rawBoard, 0o600); err != nil {
		t.Fatal(err)
	}
	versions, err := OpenFileCanonicalObjectVersionMap(filepath.Join(dir, "versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := (&CanonicalImporter{
		TenantID: "tenant-acl", Versions: versions, Registry: registry,
		Paths:         CanonicalImportPaths{MeetingMemory: memoryPath, Notifications: notificationsPath, Board: boardPath},
		OrgPrincipals: []string{"user:member@example.com", "user:nonowner@example.com", "user:owner@example.com"},
	}).Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return plan, registry
}

func canonicalImportedObjectByID(t *testing.T, plan CanonicalImportPlan, id string) CanonicalImportedObject {
	t.Helper()
	for _, object := range plan.Objects {
		if object.ObjectID == id {
			return object
		}
	}
	t.Fatalf("imported object %q not found", id)
	return CanonicalImportedObject{}
}

func containsCanonicalPrincipal(values []string, principal string) bool {
	for _, value := range values {
		if value == principal {
			return true
		}
	}
	return false
}

func TestCanonicalImporterDerivesNonVacuousLegacyACLWithoutGuestOrServiceAccess(t *testing.T) {
	plan, _ := canonicalACLImportFixture(t)
	for _, id := range []string{"transcript-team", "scout-public", "artifact-team", "notification-team"} {
		object := canonicalImportedObjectByID(t, plan, id)
		if !containsCanonicalPrincipal(object.Principals, "user:member@example.com") || !containsCanonicalPrincipal(object.Principals, "user:nonowner@example.com") {
			t.Fatalf("team object %s principals=%v", id, object.Principals)
		}
		if len(object.ImportGrants) < 2 {
			t.Fatalf("team object %s grants=%+v", id, object.ImportGrants)
		}
	}
	board := canonicalImportedObjectByID(t, plan, "board-team")
	if board.ContentRevision != 1 || !isHexDigest(board.ContentDigest) || !containsCanonicalPrincipal(board.Principals, "user:nonowner@example.com") {
		t.Fatalf("board content ACL is not revision-bound organization access: %+v", board)
	}
	for _, id := range []string{"scout-private", "artifact-private", "notification-private"} {
		object := canonicalImportedObjectByID(t, plan, id)
		if len(object.Principals) != 1 || object.Principals[0] != "user:owner@example.com" {
			t.Fatalf("private object %s principals=%v", id, object.Principals)
		}
	}
	for _, object := range plan.Objects {
		if containsCanonicalPrincipal(object.Principals, "guest:__legacy_guest__") || containsCanonicalPrincipal(object.Principals, "service:__legacy_service__") {
			t.Fatalf("durable guest/service access escaped on %s/%s", object.Family, object.ObjectID)
		}
		for _, grant := range object.ImportGrants {
			if grant.SubjectPrincipalKind == ACLPrincipalGuest || grant.SubjectPrincipalKind == ACLPrincipalService || grant.SubjectPrincipalKind == ACLPrincipalCapability {
				t.Fatalf("forbidden imported principal grant: %+v", grant)
			}
			if grant.Action == ACLReadContent && (grant.Revision != object.ContentRevision || grant.Revision < 1) {
				t.Fatalf("content grant is not revision-bound: object=%+v grant=%+v", object, grant)
			}
		}
	}
	for _, principal := range []string{"user:member@example.com", "user:nonowner@example.com", "user:owner@example.com", "guest:__legacy_guest__", "service:__legacy_service__"} {
		if !containsCanonicalPrincipal(plan.TestedPrincipals, principal) {
			t.Fatalf("tested corpus missing %q: %v", principal, plan.TestedPrincipals)
		}
	}
}

func TestPostgresImportGrantsAreIdempotentRevisionBoundAndParityChecked(t *testing.T) {
	ctx, pool := startDisposableCanonicalPostgres(t)
	plan, registry := canonicalACLImportFixture(t)
	store := NewPostgresCanonicalStore(pool, registry)
	if err := store.ApplyMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(ctx, store); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncImportGrants(ctx, plan); err != nil {
		t.Fatal(err)
	}
	var firstCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM object_grants WHERE granted_by_id='canonical-import'`).Scan(&firstCount); err != nil || firstCount == 0 {
		t.Fatalf("first grant count=%d err=%v", firstCount, err)
	}
	if err := store.SyncImportGrants(ctx, plan); err != nil {
		t.Fatal(err)
	}
	var secondCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM object_grants WHERE granted_by_id='canonical-import'`).Scan(&secondCount); err != nil || secondCount != firstCount {
		t.Fatalf("restart-safe grant count=%d/%d err=%v", firstCount, secondCount, err)
	}

	resolver := NewPostgresCanonicalParityACL(store, plan.TenantID)
	report, err := ReconcileCanonicalPlanWithOptions(ctx, plan, store, CanonicalReconcileOptions{ACL: resolver, TestedPrincipals: plan.TestedPrincipals})
	if err != nil || report.Diverged || !report.PrincipalParityProven {
		t.Fatalf("ACL parity report diverged=%v proven=%v candidates=%+v err=%v", report.Diverged, report.PrincipalParityProven, report.Candidates, err)
	}
	memberMemory := report.Source.Principals["user:member@example.com"]["memory"]
	ownerMemory := report.Source.Principals["user:owner@example.com"]["memory"]
	guestMemory := report.Source.Principals["guest:__legacy_guest__"]["memory"]
	if memberMemory.Count == 0 || memberMemory.Checksum == "" || ownerMemory.Count <= memberMemory.Count || guestMemory.Count != 0 {
		t.Fatalf("non-vacuous principal parity missing: member=%+v owner=%+v guest=%+v", memberMemory, ownerMemory, guestMemory)
	}
	privateObject := canonicalImportedObjectByID(t, plan, "artifact-private")
	privateEvent := plan.Events[0]
	for _, event := range plan.Events {
		if event.AggregateType == privateObject.Family && event.AggregateID == privateObject.ObjectID {
			privateEvent = event
			break
		}
	}
	if allowed, err := resolver.CanReadCanonicalObject(ctx, "user:nonowner@example.com", privateEvent); err != nil || allowed {
		t.Fatalf("nonowner private access allowed=%v err=%v", allowed, err)
	}
	if allowed, err := resolver.CanReadCanonicalObject(ctx, "user:owner@example.com", privateEvent); err != nil || !allowed {
		t.Fatalf("owner private access allowed=%v err=%v", allowed, err)
	}
	for _, principal := range []string{"guest:__legacy_guest__", "service:__legacy_service__"} {
		if allowed, err := resolver.CanReadCanonicalObject(ctx, principal, privateEvent); err != nil || allowed {
			t.Fatalf("%s durable access allowed=%v err=%v", principal, allowed, err)
		}
	}
	boardObject := canonicalImportedObjectByID(t, plan, "board-team")
	var boardEvent CanonicalEvent
	for _, event := range plan.Events {
		if event.AggregateType == boardObject.Family && event.AggregateID == boardObject.ObjectID {
			boardEvent = event
			break
		}
	}
	if allowed, err := resolver.CanReadCanonicalObject(ctx, "user:nonowner@example.com", boardEvent); err != nil || !allowed {
		t.Fatalf("organization board content access allowed=%v err=%v", allowed, err)
	}
	if allowed, err := resolver.CanReadCanonicalObject(ctx, "guest:__legacy_guest__", boardEvent); err != nil || allowed {
		t.Fatalf("guest board content access allowed=%v err=%v", allowed, err)
	}

	if _, err := pool.Exec(ctx, `UPDATE objects SET content_revision=content_revision+1 WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3`, plan.TenantID, privateObject.Family, privateObject.ObjectID); err != nil {
		t.Fatal(err)
	}
	if allowed, err := resolver.CanReadCanonicalObject(ctx, "user:owner@example.com", privateEvent); err != nil || allowed {
		t.Fatalf("stale revision grant survived content change: allowed=%v err=%v", allowed, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE objects SET content_revision=$4,acl_version=acl_version+1 WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3`, plan.TenantID, privateObject.Family, privateObject.ObjectID, privateObject.ContentRevision); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncImportGrants(ctx, plan); err == nil || !strings.Contains(err.Error(), "ACL version mismatch") {
		t.Fatalf("evolved ACL was silently overwritten: %v", err)
	}
}

func TestPostgresCanonicalParitySnapshotHasConstantQueryBudget(t *testing.T) {
	ctx, pool := startDisposableCanonicalPostgres(t)
	plan, registry := canonicalACLImportFixture(t)
	store := NewPostgresCanonicalStore(pool, registry)
	if err := store.ApplyMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(ctx, store); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncImportGrants(ctx, plan); err != nil {
		t.Fatal(err)
	}
	tracer := &canonicalQueryBudgetTracer{}
	config := pool.Config()
	config.ConnConfig.Tracer = tracer
	tracedPool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tracedPool.Close)
	resolver := NewPostgresCanonicalParityACL(NewPostgresCanonicalStore(tracedPool, registry), plan.TenantID)
	memoryEvents := NewMemoryCanonicalEventStore(registry)
	if err := plan.Apply(ctx, memoryEvents); err != nil {
		t.Fatal(err)
	}
	extraPrincipals := make([]string, 500)
	for index := range extraPrincipals {
		extraPrincipals[index] = fmt.Sprintf("service:postgres-budget-%03d", index)
	}
	report, err := ReconcileCanonicalPlanWithOptions(ctx, plan, memoryEvents, CanonicalReconcileOptions{
		ACL: resolver, TestedPrincipals: append(append([]string(nil), plan.TestedPrincipals...), extraPrincipals...),
	})
	if err != nil || report.Diverged {
		t.Fatalf("snapshot reconcile diverged=%v candidates=%+v err=%v", report.Diverged, report.Candidates, err)
	}
	if objects, grants := tracer.countContaining("FROM objects o JOIN unnest"), tracer.countContaining("FROM object_grants g"); objects != 1 || grants != 1 {
		t.Fatalf("snapshot query budget objects=%d grants=%d, want one set query each", objects, grants)
	}
	if liveObjects, liveGrants := tracer.countContaining("FROM objects WHERE tenant_id"), tracer.countContaining("g.object_type=$2"); liveObjects != 0 || liveGrants != 0 {
		t.Fatalf("reconcile fell back to per-principal live queries: objects=%d grants=%d", liveObjects, liveGrants)
	}
}

func TestPostgresCanonicalParitySnapshotIsImmutableButLiveResolverObservesRevocation(t *testing.T) {
	ctx, pool := startDisposableCanonicalPostgres(t)
	plan, registry := canonicalACLImportFixture(t)
	store := NewPostgresCanonicalStore(pool, registry)
	if err := store.ApplyMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(ctx, store); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncImportGrants(ctx, plan); err != nil {
		t.Fatal(err)
	}
	resolver := NewPostgresCanonicalParityACL(store, plan.TenantID)
	object := canonicalImportedObjectByID(t, plan, "artifact-private")
	var event CanonicalEvent
	for _, candidate := range plan.Events {
		if candidate.AggregateType == object.Family && candidate.AggregateID == object.ObjectID {
			event = candidate
			break
		}
	}
	principal := "user:owner@example.com"
	snapshot, err := resolver.SnapshotCanonicalParityACL(ctx, []CanonicalEvent{event})
	if err != nil {
		t.Fatal(err)
	}
	if allowed, err := snapshot.CanReadCanonicalObject(ctx, principal, event); err != nil || !allowed {
		t.Fatalf("pre-revocation snapshot allowed=%v err=%v", allowed, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE object_grants SET revoked_at=now()
		WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3 AND subject_type='user' AND subject_id='owner@example.com'`,
		plan.TenantID, object.Family, object.ObjectID); err != nil {
		t.Fatal(err)
	}
	if allowed, err := resolver.CanReadCanonicalObject(ctx, principal, event); err != nil || allowed {
		t.Fatalf("live resolver retained revoked authority: allowed=%v err=%v", allowed, err)
	}
	if allowed, err := snapshot.CanReadCanonicalObject(ctx, principal, event); err != nil || !allowed {
		t.Fatalf("immutable point-in-time snapshot changed after revocation: allowed=%v err=%v", allowed, err)
	}
	memoryEvents := NewMemoryCanonicalEventStore(registry)
	if err := plan.Apply(ctx, memoryEvents); err != nil {
		t.Fatal(err)
	}
	followUp, err := ReconcileCanonicalPlanWithOptions(ctx, plan, memoryEvents, CanonicalReconcileOptions{ACL: resolver, TestedPrincipals: plan.TestedPrincipals})
	if err != nil || !followUp.Diverged {
		t.Fatalf("fresh follow-up missed revocation: diverged=%v candidates=%+v err=%v", followUp.Diverged, followUp.Candidates, err)
	}
	found := false
	for _, candidate := range followUp.Candidates {
		if candidate.Family == object.Family && candidate.ObjectID == object.ObjectID && candidate.Principal == principal && candidate.Kind == "principal_missing_access" {
			found = true
		}
	}
	if !found {
		t.Fatalf("follow-up candidates omitted revoked authority: %+v", followUp.Candidates)
	}
}

func TestPostgresCanonicalParitySnapshotProductionScale(t *testing.T) {
	if os.Getenv("CANONICAL_PARITY_SCALE_TEST") != "1" {
		t.Skip("set CANONICAL_PARITY_SCALE_TEST=1 to run the 32,751-object snapshot soak")
	}
	ctx, store, registry := migratedPostgresCanonicalStore(t)
	anchor := canonicalTestEvent(t, registry, uuid.New(), "scale-anchor", 1, "scale-anchor", "private")
	if _, err := store.Append(ctx, anchor); err != nil {
		t.Fatal(err)
	}
	var sequence int64
	if err := store.pool.QueryRow(ctx, "SELECT sequence FROM canonical_events WHERE event_id=$1", anchor.EventID).Scan(&sequence); err != nil {
		t.Fatal(err)
	}
	const objectCount = 32751
	if _, err := store.pool.Exec(ctx, `INSERT INTO objects (
		tenant_id,object_type,object_id,state_revision,content_revision,classification,state,content_sha256,acl_version,last_event_sequence
	) SELECT $1,'artifact','scale-'||lpad(value::text,5,'0'),1,1,'internal','{}'::jsonb,decode(repeat('a',64),'hex'),1,$2
		FROM generate_series(1,$3) AS value`, anchor.TenantID, sequence, objectCount); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO object_grants (
		grant_id,tenant_id,object_type,object_id,acl_version,revision,subject_type,subject_id,action,granted_by_type,granted_by_id
	) SELECT (substr(md5(value::text),1,8)||'-'||substr(md5(value::text),9,4)||'-'||substr(md5(value::text),13,4)||'-'||substr(md5(value::text),17,4)||'-'||substr(md5(value::text),21,12))::uuid,
		$1,'artifact','scale-'||lpad(value::text,5,'0'),1,1,'team',$2,'read_content','service','scale-soak'
		FROM generate_series(1,$3) AS value`, anchor.TenantID, canonicalLegacyOrgTeamID, objectCount); err != nil {
		t.Fatal(err)
	}
	events := make([]CanonicalEvent, objectCount)
	for index := range events {
		events[index] = CanonicalEvent{TenantID: anchor.TenantID, AggregateType: "artifact", AggregateID: fmt.Sprintf("scale-%05d", index+1), ACLVersion: 1}
	}
	started := time.Now()
	snapshot, err := NewPostgresCanonicalParityACL(store, anchor.TenantID).SnapshotCanonicalParityACL(ctx, events)
	if err != nil {
		t.Fatal(err)
	}
	loadedIn := time.Since(started)
	principal := "user:scale@example.com"
	for _, event := range events {
		allowed, resolveErr := snapshot.CanReadCanonicalObject(ctx, principal, event)
		if resolveErr != nil || !allowed {
			t.Fatalf("snapshot authorization %s allowed=%v err=%v", event.AggregateID, allowed, resolveErr)
		}
	}
	t.Logf("loaded and authorized %d objects from one repeatable-read snapshot in %s", objectCount, time.Since(started))
	if loadedIn > 20*time.Second {
		t.Fatalf("32,751-object snapshot load took %s", loadedIn)
	}
}

func TestCanonicalLifecycleJournalAdvancesObservedAggregateToDeletedState(t *testing.T) {
	ctx, pool := startDisposableCanonicalPostgres(t)
	dir := t.TempDir()
	memoryPath := filepath.Join(dir, "meeting-memory.jsonl")
	deletedPath := filepath.Join(dir, "deleted-objects.jsonl")
	versions, err := OpenFileCanonicalObjectVersionMap(filepath.Join(dir, "versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	entry := meetingMemoryEntry{ID: "memory-deleted", Kind: meetingMemoryKindTranscript, Text: "body", CreatedAt: time.Now().UTC()}
	raw, _ := json.Marshal(entry)
	if err := os.WriteFile(memoryPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	build := func() CanonicalImportPlan {
		plan, err := (&CanonicalImporter{TenantID: "tenant-delete", Versions: versions, Registry: registry,
			Paths: CanonicalImportPaths{MeetingMemory: memoryPath, DeletedJournal: deletedPath}, OrgPrincipals: []string{"user:member@example.com"}}).Build(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	initial := build()
	store := NewPostgresCanonicalStore(pool, registry)
	if err := store.ApplyMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := initial.Apply(ctx, store); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncImportGrants(ctx, initial); err != nil {
		t.Fatal(err)
	}
	initialObject := canonicalImportedObjectByID(t, initial, entry.ID)
	if err := os.Remove(memoryPath); err != nil {
		t.Fatal(err)
	}
	journal := CanonicalLifecycleJournalRecord{Family: "memory", ObjectID: entry.ID, StateDigest: initialObject.StateDigest, At: time.Now().UTC().Add(time.Second), Reason: "deleted"}
	journalRaw, _ := json.Marshal(journal)
	if err := os.WriteFile(deletedPath, append(journalRaw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	deletedPlan := build()
	deletedObject := canonicalImportedObjectByID(t, deletedPlan, entry.ID)
	if !deletedObject.Deleted || deletedObject.AggregateVersion != initialObject.AggregateVersion+1 || len(deletedObject.ImportGrants) != 0 {
		t.Fatalf("deletion target=%+v initial=%+v", deletedObject, initialObject)
	}
	if err := deletedPlan.Apply(ctx, store); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncImportGrants(ctx, deletedPlan); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.ResolveACLObject(ctx, ACLObjectRef{TenantID: deletedPlan.TenantID, Type: "memory", ID: entry.ID, ACLVersion: 1})
	if err != nil || !resolved.Deleted {
		t.Fatalf("deleted projection=%+v err=%v", resolved, err)
	}
	report, err := ReconcileCanonicalPlanWithOptions(ctx, deletedPlan, store, CanonicalReconcileOptions{ACL: NewPostgresCanonicalParityACL(store, deletedPlan.TenantID), TestedPrincipals: deletedPlan.TestedPrincipals})
	if err != nil || report.Diverged {
		t.Fatalf("deleted parity diverged=%v candidates=%+v err=%v", report.Diverged, report.Candidates, err)
	}
}
