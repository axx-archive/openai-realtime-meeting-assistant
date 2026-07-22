package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func expireGuestLinkForLifecycleTest(t *testing.T, store *roomStore, roomID, linkID string) guestLinkRecord {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	roomIndex, ok := store.roomByIDLocked(roomID)
	if !ok {
		t.Fatalf("room %s not found", roomID)
	}
	for linkIndex := range store.rooms[roomIndex].GuestLinks {
		link := &store.rooms[roomIndex].GuestLinks[linkIndex]
		if link.ID != linkID {
			continue
		}
		link.Expires = time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
		if err := store.persistLocked(); err != nil {
			t.Fatalf("persist expired guest link: %v", err)
		}
		return *link
	}
	t.Fatalf("guest link %s not found", linkID)
	return guestLinkRecord{}
}

func guestLinkLifecycleTestStore(t *testing.T) (*roomStore, roomRecord, guestLinkRecord, string) {
	t.Helper()
	dir := t.TempDir()
	store := newRoomStore(filepath.Join(dir, "rooms.json"))
	room, err := store.create("Canonical expiry", "", "owner@example.com", false)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	token, link, err := store.mintGuestLink(room.ID, "expiring", "owner@example.com", time.Hour)
	if err != nil {
		t.Fatalf("mint guest link: %v", err)
	}
	link = expireGuestLinkForLifecycleTest(t, store, room.ID, link.ID)
	return store, room, link, token
}

func readGuestLinkLifecycleJournal(t *testing.T, path string) []CanonicalLifecycleJournalRecord {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read lifecycle journal: %v", err)
	}
	var records []CanonicalLifecycleJournalRecord
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var record CanonicalLifecycleJournalRecord
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("decode lifecycle journal: %v", err)
		}
		records = append(records, record)
	}
	return records
}

func assertGuestLinkAbsentForLifecycleTest(t *testing.T, store *roomStore, roomID, linkID string) {
	t.Helper()
	links, err := store.listGuestLinks(roomID)
	if err != nil {
		t.Fatalf("list guest links: %v", err)
	}
	for _, link := range links {
		if link.ID == linkID {
			t.Fatalf("expired guest link %s remains in room store", linkID)
		}
	}
}

func canonicalGuestLinkByIDForLifecycleTest(t *testing.T, objects []CanonicalImportedObject, objectID string) CanonicalImportedObject {
	t.Helper()
	for _, object := range objects {
		if object.Family == "guest_link" && object.ObjectID == objectID {
			return object
		}
	}
	t.Fatalf("canonical guest_link %s not found", objectID)
	return CanonicalImportedObject{}
}

func journalGuestLinkExpiryForLifecycleTest(t *testing.T, journalPath, roomID string, link guestLinkRecord) CanonicalImportedObject {
	t.Helper()
	object, err := guestLinkCanonicalImportedObject(roomID, link)
	if err != nil {
		t.Fatalf("project guest link: %v", err)
	}
	if err := ensureCanonicalLifecycleJournal(journalPath, CanonicalLifecycleJournalRecord{
		Family: object.Family, ObjectID: object.ObjectID, StateDigest: object.StateDigest,
		At: time.Now().UTC(), Reason: "guest_link_expired",
	}); err != nil {
		t.Fatalf("journal guest-link expiry: %v", err)
	}
	return object
}

func guestLinkPresentForLifecycleTest(t *testing.T, store *roomStore, roomID, linkID string) bool {
	t.Helper()
	links, err := store.listGuestLinks(roomID)
	if err != nil {
		t.Fatalf("list guest links: %v", err)
	}
	for _, link := range links {
		if link.ID == linkID {
			return true
		}
	}
	return false
}

func buildGuestLinkLifecyclePlan(t *testing.T, ctx context.Context, roomsPath, journalPath string, versions *FileCanonicalObjectVersionMap, registry *CanonicalPayloadRegistry) CanonicalImportPlan {
	t.Helper()
	plan, err := (&CanonicalImporter{
		TenantID: "tenant-guest-link-expiry", Versions: versions, Registry: registry,
		Paths: CanonicalImportPaths{Rooms: roomsPath, EvictedJournal: journalPath},
	}).Build(ctx)
	if err != nil {
		t.Fatalf("build canonical plan: %v", err)
	}
	return plan
}

func TestExpiredGuestLinkJournalsExactImportedStateBeforeRemoval(t *testing.T) {
	store, room, link, _ := guestLinkLifecycleTestStore(t)
	objectID := room.ID + ":" + link.ID
	before, err := importRoomObjects(store.path)
	if err != nil {
		t.Fatalf("import rooms before expiry sweep: %v", err)
	}
	want := canonicalGuestLinkByIDForLifecycleTest(t, before, objectID)

	if err := store.sweepExpiredGuestLinks(); err != nil {
		t.Fatalf("sweep expired guest links: %v", err)
	}
	assertGuestLinkAbsentForLifecycleTest(t, store, room.ID, link.ID)
	assertGuestLinkAbsentForLifecycleTest(t, newRoomStore(store.path), room.ID, link.ID)

	records := readGuestLinkLifecycleJournal(t, filepath.Join(filepath.Dir(store.path), "evicted-objects.jsonl"))
	if len(records) != 1 {
		t.Fatalf("lifecycle journal records=%d, want 1: %+v", len(records), records)
	}
	got := records[0]
	if got.Family != want.Family || got.ObjectID != want.ObjectID || got.StateDigest != want.StateDigest || got.Reason != "guest_link_expired" || got.At.IsZero() {
		t.Fatalf("lifecycle journal=%+v, want family/object/digest=%s/%s/%s", got, want.Family, want.ObjectID, want.StateDigest)
	}
}

func TestExpiredGuestLinkJournalFailureLeavesRowButCapabilityUnusable(t *testing.T) {
	store, room, link, token := guestLinkLifecycleTestStore(t)
	journalPath := filepath.Join(filepath.Dir(store.path), "evicted-objects.jsonl")
	if err := os.Mkdir(journalPath, 0o700); err != nil {
		t.Fatalf("create journal failure fixture: %v", err)
	}

	err := store.sweepExpiredGuestLinks()
	if err == nil || !strings.Contains(err.Error(), "journal expired guest link") {
		t.Fatalf("journal failure error=%v", err)
	}
	links, listErr := store.listGuestLinks(room.ID)
	if listErr != nil || len(links) != 1 || links[0].ID != link.ID {
		t.Fatalf("in-memory links after journal failure=%+v err=%v", links, listErr)
	}
	reloaded := newRoomStore(store.path)
	links, listErr = reloaded.listGuestLinks(room.ID)
	if listErr != nil || len(links) != 1 || links[0].ID != link.ID {
		t.Fatalf("durable links after journal failure=%+v err=%v", links, listErr)
	}
	if _, ok := store.redeemGuestToken(token); ok {
		t.Fatal("expired capability redeemed after fail-closed journal failure")
	}
}

func TestExpiredGuestLinkSweepRetriesExistingJournalIdempotently(t *testing.T) {
	store, room, link, _ := guestLinkLifecycleTestStore(t)
	object, err := guestLinkCanonicalImportedObject(room.ID, link)
	if err != nil {
		t.Fatalf("project guest link: %v", err)
	}
	journalPath := filepath.Join(filepath.Dir(store.path), "evicted-objects.jsonl")
	if err := ensureCanonicalLifecycleJournal(journalPath, CanonicalLifecycleJournalRecord{
		Family: object.Family, ObjectID: object.ObjectID, StateDigest: object.StateDigest,
		At: time.Now().UTC().Add(-time.Minute), Reason: "guest_link_expired",
	}); err != nil {
		t.Fatalf("seed crash-surviving journal: %v", err)
	}

	if err := store.sweepExpiredGuestLinks(); err != nil {
		t.Fatalf("retry sweep: %v", err)
	}
	assertGuestLinkAbsentForLifecycleTest(t, store, room.ID, link.ID)
	records := readGuestLinkLifecycleJournal(t, journalPath)
	if len(records) != 1 || records[0].StateDigest != object.StateDigest {
		t.Fatalf("idempotent retry journal=%+v", records)
	}
	if err := store.sweepExpiredGuestLinks(); err != nil {
		t.Fatalf("empty retry sweep: %v", err)
	}
	if got := len(readGuestLinkLifecycleJournal(t, journalPath)); got != 1 {
		t.Fatalf("empty retry appended journal rows: got %d want 1", got)
	}
}

func TestExpiredGuestLinkRoomsPersistFailureLeavesRetryableJournal(t *testing.T) {
	store, room, link, _ := guestLinkLifecycleTestStore(t)
	originalPath := store.path
	blockedParent := filepath.Join(filepath.Dir(originalPath), "not-a-directory")
	if err := os.WriteFile(blockedParent, []byte("block atomic room-store replacement"), 0o600); err != nil {
		t.Fatalf("create persist failure fixture: %v", err)
	}
	store.path = filepath.Join(blockedParent, "rooms.json")

	err := store.sweepExpiredGuestLinks()
	store.path = originalPath
	if err == nil || !strings.Contains(err.Error(), "persist expired guest-link sweep") {
		t.Fatalf("room persist failure error=%v", err)
	}
	if !guestLinkPresentForLifecycleTest(t, store, room.ID, link.ID) {
		t.Fatal("in-memory row was not rolled back after rooms persist failure")
	}
	if !guestLinkPresentForLifecycleTest(t, newRoomStore(originalPath), room.ID, link.ID) {
		t.Fatal("durable source row disappeared despite rooms persist failure")
	}
	records := readGuestLinkLifecycleJournal(t, store.lifecycleJournalPath)
	if len(records) != 1 || records[0].ObjectID != room.ID+":"+link.ID {
		t.Fatalf("retryable lifecycle journal=%+v", records)
	}
}

func TestCanonicalBuildRecoversJournaledExpiredLinkAfterPrePersistCrash(t *testing.T) {
	ctx := context.Background()
	store, room, link, _ := guestLinkLifecycleTestStore(t)
	journalPath := store.lifecycleJournalPath
	versions, err := OpenFileCanonicalObjectVersionMap(filepath.Join(t.TempDir(), "versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	initial := buildGuestLinkLifecyclePlan(t, ctx, store.path, journalPath, versions, registry)
	initialObject := canonicalGuestLinkByIDForLifecycleTest(t, initial.Objects, room.ID+":"+link.ID)
	journaled := journalGuestLinkExpiryForLifecycleTest(t, journalPath, room.ID, link)
	if journaled.StateDigest != initialObject.StateDigest {
		t.Fatalf("journal digest=%s, imported digest=%s", journaled.StateDigest, initialObject.StateDigest)
	}
	if !guestLinkPresentForLifecycleTest(t, newRoomStore(store.path), room.ID, link.ID) {
		t.Fatal("crash fixture did not retain the journaled source row")
	}

	if err := recoverJournaledExpiredGuestLinks(store.path, journalPath); err != nil {
		t.Fatalf("boot guest-link recovery: %v", err)
	}
	assertGuestLinkAbsentForLifecycleTest(t, newRoomStore(store.path), room.ID, link.ID)
	after := buildGuestLinkLifecyclePlan(t, ctx, store.path, journalPath, versions, registry)
	deleted := canonicalGuestLinkByIDForLifecycleTest(t, after.Objects, initialObject.ObjectID)
	if !deleted.Deleted || deleted.AggregateVersion != initialObject.AggregateVersion+1 {
		t.Fatalf("recovered canonical object=%+v initial=%+v", deleted, initialObject)
	}
}

func TestCanonicalRuntimeBootCompletesJournaledGuestLinkExpiryCrashWindow(t *testing.T) {
	dir := canonicalRuntimeTestEnv(t, "shadow")
	roomsPath := filepath.Join(dir, "rooms.json")
	journalPath := filepath.Join(dir, "evicted-objects.jsonl")
	store := newRoomStore(roomsPath)
	room, err := store.create("Restart recovery", "", "owner@example.com", false)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	_, link, err := store.mintGuestLink(room.ID, "restart", "owner@example.com", time.Hour)
	if err != nil {
		t.Fatalf("mint guest link: %v", err)
	}
	link = expireGuestLinkForLifecycleTest(t, store, room.ID, link.ID)

	firstRuntime, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatalf("initial canonical runtime: %v", err)
	}
	initial, err := firstRuntime.buildLegacyPlan(context.Background())
	if err != nil {
		t.Fatalf("initial canonical build: %v", err)
	}
	initialObject := canonicalGuestLinkByIDForLifecycleTest(t, initial.Objects, room.ID+":"+link.ID)
	closeCanonicalRuntime()
	journaled := journalGuestLinkExpiryForLifecycleTest(t, journalPath, room.ID, link)
	if journaled.StateDigest != initialObject.StateDigest {
		t.Fatalf("journal digest=%s, imported digest=%s", journaled.StateDigest, initialObject.StateDigest)
	}

	restarted, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatalf("restart canonical runtime: %v", err)
	}
	assertGuestLinkAbsentForLifecycleTest(t, newRoomStore(roomsPath), room.ID, link.ID)
	after, err := restarted.buildLegacyPlan(context.Background())
	if err != nil {
		t.Fatalf("post-recovery canonical build: %v", err)
	}
	deleted := canonicalGuestLinkByIDForLifecycleTest(t, after.Objects, initialObject.ObjectID)
	if !deleted.Deleted || deleted.AggregateVersion != initialObject.AggregateVersion+1 {
		t.Fatalf("boot-recovered canonical object=%+v initial=%+v", deleted, initialObject)
	}
}

func TestJournaledGuestLinkRecoveryPreservesGeneralDigestConflict(t *testing.T) {
	ctx := context.Background()
	store, room, link, _ := guestLinkLifecycleTestStore(t)
	journalPath := store.lifecycleJournalPath
	versions, err := OpenFileCanonicalObjectVersionMap(filepath.Join(t.TempDir(), "versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	initial := buildGuestLinkLifecyclePlan(t, ctx, store.path, journalPath, versions, registry)
	object := canonicalGuestLinkByIDForLifecycleTest(t, initial.Objects, room.ID+":"+link.ID)
	wrongDigest := strings.Repeat("f", 64)
	if wrongDigest == object.StateDigest {
		wrongDigest = strings.Repeat("e", 64)
	}
	if err := ensureCanonicalLifecycleJournal(journalPath, CanonicalLifecycleJournalRecord{
		Family: "guest_link", ObjectID: object.ObjectID, StateDigest: wrongDigest,
		At: time.Now().UTC(), Reason: "guest_link_expired",
	}); err != nil {
		t.Fatalf("seed mismatched journal: %v", err)
	}
	if err := recoverJournaledExpiredGuestLinks(store.path, journalPath); err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("digest-mismatch recovery error=%v", err)
	}
	if !guestLinkPresentForLifecycleTest(t, newRoomStore(store.path), room.ID, link.ID) {
		t.Fatal("digest-mismatched live row was removed")
	}
	_, err = (&CanonicalImporter{
		TenantID: "tenant-guest-link-expiry", Versions: versions, Registry: registry,
		Paths: CanonicalImportPaths{Rooms: store.path, EvictedJournal: journalPath},
	}).Build(ctx)
	if err == nil || !strings.Contains(err.Error(), "lifecycle journal conflicts with live object") {
		t.Fatalf("general lifecycle conflict was weakened: %v", err)
	}
}

func TestPartialGuestLinkJournalBatchEventuallyConvergesWithoutResurrection(t *testing.T) {
	ctx := context.Background()
	store, room, first, _ := guestLinkLifecycleTestStore(t)
	_, second, err := store.mintGuestLink(room.ID, "second expiring link", "owner@example.com", time.Hour)
	if err != nil {
		t.Fatalf("mint second guest link: %v", err)
	}
	second = expireGuestLinkForLifecycleTest(t, store, room.ID, second.ID)
	journalPath := store.lifecycleJournalPath
	versions, err := OpenFileCanonicalObjectVersionMap(filepath.Join(t.TempDir(), "versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	initial := buildGuestLinkLifecyclePlan(t, ctx, store.path, journalPath, versions, registry)
	events := NewMemoryCanonicalEventStore(registry)
	if err := initial.Apply(ctx, events); err != nil {
		t.Fatalf("apply initial plan: %v", err)
	}

	// Simulate a process dying between the first and second durable journal
	// append. Both source rows are still live at restart.
	journalGuestLinkExpiryForLifecycleTest(t, journalPath, room.ID, first)
	if err := recoverJournaledExpiredGuestLinks(store.path, journalPath); err != nil {
		t.Fatalf("recover partial journal batch: %v", err)
	}
	restarted := newRoomStore(store.path)
	if guestLinkPresentForLifecycleTest(t, restarted, room.ID, first.ID) {
		t.Fatal("exactly journaled first link survived boot recovery")
	}
	if !guestLinkPresentForLifecycleTest(t, restarted, room.ID, second.ID) {
		t.Fatal("unjournaled second link was removed by partial-batch recovery")
	}
	intermediate := buildGuestLinkLifecyclePlan(t, ctx, store.path, journalPath, versions, registry)
	if err := intermediate.Apply(ctx, events); err != nil {
		t.Fatalf("apply intermediate plan: %v", err)
	}
	if report, reconcileErr := ReconcileCanonicalPlanWithOptions(ctx, intermediate, events, CanonicalReconcileOptions{ACL: canonicalParityACLFromPlan(intermediate)}); reconcileErr != nil || report.Diverged {
		t.Fatalf("intermediate parity diverged=%v candidates=%+v err=%v", report.Diverged, report.Candidates, reconcileErr)
	}

	if err := restarted.sweepExpiredGuestLinks(); err != nil {
		t.Fatalf("finish partial expiry batch: %v", err)
	}
	final := buildGuestLinkLifecyclePlan(t, ctx, store.path, journalPath, versions, registry)
	if err := final.Apply(ctx, events); err != nil {
		t.Fatalf("apply final plan: %v", err)
	}
	if report, reconcileErr := ReconcileCanonicalPlanWithOptions(ctx, final, events, CanonicalReconcileOptions{ACL: canonicalParityACLFromPlan(final)}); reconcileErr != nil || report.Diverged || len(report.Candidates) != 0 {
		t.Fatalf("final parity diverged=%v candidates=%+v err=%v", report.Diverged, report.Candidates, reconcileErr)
	}

	// A second restart/retry is a no-op: neither capability can reappear and
	// the two lifecycle records remain unique.
	if err := recoverJournaledExpiredGuestLinks(store.path, journalPath); err != nil {
		t.Fatalf("idempotent boot recovery: %v", err)
	}
	again := newRoomStore(store.path)
	assertGuestLinkAbsentForLifecycleTest(t, again, room.ID, first.ID)
	assertGuestLinkAbsentForLifecycleTest(t, again, room.ID, second.ID)
	if err := again.sweepExpiredGuestLinks(); err != nil {
		t.Fatalf("idempotent empty sweep: %v", err)
	}
	records := readGuestLinkLifecycleJournal(t, journalPath)
	guestLinkRecords := 0
	for _, record := range records {
		if record.Family == "guest_link" {
			guestLinkRecords++
		}
	}
	if guestLinkRecords != 2 {
		t.Fatalf("guest-link lifecycle records=%d, want 2: %+v", guestLinkRecords, records)
	}
}

func TestCanonicalReconciliationConvergesAfterGuestLinkExpiry(t *testing.T) {
	ctx := context.Background()
	store, room, link, _ := guestLinkLifecycleTestStore(t)
	journalPath := filepath.Join(filepath.Dir(store.path), "evicted-objects.jsonl")
	versions, err := OpenFileCanonicalObjectVersionMap(filepath.Join(t.TempDir(), "versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	objectID := room.ID + ":" + link.ID
	initial := buildGuestLinkLifecyclePlan(t, ctx, store.path, journalPath, versions, registry)
	initialObject := canonicalGuestLinkByIDForLifecycleTest(t, initial.Objects, objectID)
	events := NewMemoryCanonicalEventStore(registry)
	if err := initial.Apply(ctx, events); err != nil {
		t.Fatalf("apply initial plan: %v", err)
	}

	if err := store.sweepExpiredGuestLinks(); err != nil {
		t.Fatalf("sweep expired guest link: %v", err)
	}
	after := buildGuestLinkLifecyclePlan(t, ctx, store.path, journalPath, versions, registry)
	deleted := canonicalGuestLinkByIDForLifecycleTest(t, after.Objects, objectID)
	if !deleted.Deleted || deleted.Status != "closed" || deleted.AggregateVersion != initialObject.AggregateVersion+1 {
		t.Fatalf("deleted canonical object=%+v initial=%+v", deleted, initialObject)
	}
	foundExactJournal := false
	for _, object := range after.Objects {
		if object.Family == "eviction" && object.LifecycleFamily == "guest_link" && object.LifecycleObjectID == objectID {
			foundExactJournal = object.LifecycleStateDigest == initialObject.StateDigest
		}
	}
	if !foundExactJournal {
		t.Fatalf("canonical plan lacks exact guest-link lifecycle digest %s", initialObject.StateDigest)
	}
	if err := after.Apply(ctx, events); err != nil {
		t.Fatalf("apply post-expiry plan: %v", err)
	}
	report, err := ReconcileCanonicalPlanWithOptions(ctx, after, events, CanonicalReconcileOptions{
		ACL: canonicalParityACLFromPlan(after),
	})
	if err != nil {
		t.Fatalf("reconcile post-expiry plan: %v", err)
	}
	if report.Diverged || len(report.Candidates) != 0 {
		t.Fatalf("post-expiry canonical parity diverged: %+v", report)
	}
}
