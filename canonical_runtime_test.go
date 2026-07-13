package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func canonicalRuntimeTestEnv(t *testing.T, mode string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("BONFIRE_CANONICAL_MODE", mode)
	t.Setenv("BONFIRE_CANONICAL_DATABASE_URL", "")
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "meeting-memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "kanban-board.json"))
	t.Setenv("BONFIRE_ROOMS_PATH", filepath.Join(dir, "rooms.json"))
	t.Setenv("MEETINGS_PATH", filepath.Join(dir, "meetings.json"))
	t.Setenv("NOTIFICATIONS_PATH", filepath.Join(dir, "notifications.json"))
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", filepath.Join(dir, "file-folders.json"))
	t.Setenv("BONFIRE_CODEX_QUEUE_PATH", filepath.Join(dir, "codex-jobs"))
	t.Setenv("BONFIRE_RENDER_QUEUE_PATH", filepath.Join(dir, "render-jobs"))
	setCanonicalRuntime(nil)
	t.Cleanup(closeCanonicalRuntime)
	return dir
}

func TestCanonicalRuntimeOffPreservesLegacyWrite(t *testing.T) {
	dir := canonicalRuntimeTestEnv(t, "off")
	runtime, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "meeting-memory.jsonl")
	if err := writeFileAtomicallyForCanonicalMode(path, []byte("legacy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.spool != nil {
		t.Fatal("off mode must not construct a capture spool")
	}
	if _, err := os.Stat(filepath.Join(dir, "canonical")); !os.IsNotExist(err) {
		t.Fatalf("off mode created canonical state: %v", err)
	}
}

func TestCanonicalRuntimeShadowFencesConcurrentAppendsAndReportsMissingPG(t *testing.T) {
	dir := canonicalRuntimeTestEnv(t, "shadow")
	if _, err := initializeCanonicalRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "meeting-memory.jsonl")
	const count = 12
	var wait sync.WaitGroup
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := appendFileDurably(path, []byte("{}\n"), 0o600); err != nil {
				t.Errorf("append: %v", err)
			}
		}()
	}
	wait.Wait()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(raw, []byte("{}\n")); got != count {
		t.Fatalf("appended records=%d want=%d", got, count)
	}
	snapshot := canonicalRuntimeSnapshot()
	if snapshot.Healthy || snapshot.Database || snapshot.HighWater != count*2 || snapshot.Pending != 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if !strings.Contains(snapshot.Error, "PostgreSQL") {
		t.Fatalf("missing database degradation not visible: %+v", snapshot)
	}
}

func TestCanonicalRuntimeRestartRecoversPreparedLegacyMutation(t *testing.T) {
	dir := canonicalRuntimeTestEnv(t, "shadow")
	runtime, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "meeting-memory.jsonl")
	after := []byte(`{"id":"one","kind":"note","text":"after","createdAt":"2026-01-01T00:00:00Z"}` + "\n")
	digest := sha256Hex(after)
	fact := []byte(`{"object_id":"one"}`)
	if _, err := runtime.spool.Prepare("crash-window", "memory", path, "", digest, fact); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomicallyUnfenced(path, after, 0o600, true); err != nil {
		t.Fatal(err)
	}
	closeCanonicalRuntime()
	if _, err := initializeCanonicalRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := canonicalRuntimeSnapshot()
	if snapshot.Pending != 0 || snapshot.HighWater != 2 {
		t.Fatalf("recovery snapshot=%+v", snapshot)
	}
}

func TestCanonicalRuntimeRestartMaterializesCommittedBlobDeleteJournal(t *testing.T) {
	dir := canonicalRuntimeTestEnv(t, "shadow")
	if _, err := initializeCanonicalRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	ref := strings.Repeat("b", 64)
	path := filepath.Join(dir, "blobs", ref[:2], ref)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("blob"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := canonicalFenceRemoveMutation(path, func() error { return os.Remove(path) }); err != nil {
		t.Fatal(err)
	}
	closeCanonicalRuntime()
	if _, err := initializeCanonicalRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "evicted-objects.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"object_id":"`+ref+`"`)) {
		t.Fatalf("committed deletion journal missing: %s", raw)
	}
}

func TestCanonicalRuntimeShadowSurfacesMalformedFolderStore(t *testing.T) {
	dir := canonicalRuntimeTestEnv(t, "shadow")
	if err := os.WriteFile(filepath.Join(dir, "file-folders.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := initializeCanonicalRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := canonicalRuntimeSnapshot()
	if snapshot.Healthy || !strings.Contains(snapshot.Error, "boot scan degraded") {
		t.Fatalf("malformed folder state not surfaced: %+v", snapshot)
	}
}

func TestCanonicalRuntimeRequiredRefusesMissingDatabase(t *testing.T) {
	canonicalRuntimeTestEnv(t, "required")
	if _, err := initializeCanonicalRuntime(context.Background()); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("required startup err=%v", err)
	}
}

func TestCanonicalRuntimeAmbiguousFolderPublishCommitsVisibleAfterState(t *testing.T) {
	dir := canonicalRuntimeTestEnv(t, "shadow")
	if _, err := initializeCanonicalRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	previousSync := syncDirectoryForAtomicWrite
	syncDirectoryForAtomicWrite = func(string) error { return errors.New("injected parent sync failure") }
	t.Cleanup(func() { syncDirectoryForAtomicWrite = previousSync })
	store := newFileFolderStore(filepath.Join(dir, "file-folders.json"))
	if _, err := store.create("Published despite ambiguous fsync", "owner@example.com"); !errors.Is(err, ErrDurableReplaceAmbiguous) {
		t.Fatalf("create err=%v, want durable ambiguity", err)
	}
	reloaded := newFileFolderStore(filepath.Join(dir, "file-folders.json"))
	folders, _ := reloaded.snapshot()
	if len(folders) != 1 || folders[0].Name != "Published despite ambiguous fsync" {
		t.Fatalf("visible disk state was not reloaded: %+v", folders)
	}
	snapshot := canonicalRuntimeSnapshot()
	if snapshot.Pending != 0 || snapshot.HighWater != 2 {
		t.Fatalf("canonical spool disagrees with visible after-state: %+v", snapshot)
	}
}

func TestCanonicalRuntimeCoverageIncludesEveryImportedFamily(t *testing.T) {
	covered, uncovered := canonicalRuntimeCoverage()
	if len(uncovered) != 0 {
		t.Fatalf("uncovered imported families: %v", uncovered)
	}
	if len(covered) != len(canonicalLegacyFamilies) {
		t.Fatalf("covered=%v imported=%v", covered, canonicalLegacyFamilies)
	}
}

func TestCanonicalRuntimeMutationReconcilesLogicalObjectIdentityAtHighWater(t *testing.T) {
	canonicalRuntimeTestEnv(t, "shadow")
	runtime, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	memoryEvents := NewMemoryCanonicalEventStore(runtime.registry)
	runtime.events = memoryEvents
	entry := []byte(`{"id":"logical-note","kind":"note","text":"current","createdAt":"2026-01-01T00:00:00Z"}` + "\n")
	if err := writeFileAtomicallyForCanonicalMode(meetingMemoryPath(), entry, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	events, err := memoryEvents.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].AggregateType != "memory" || events[0].AggregateID != "logical-note" {
		t.Fatalf("logical canonical identity not reconciled: %+v", events)
	}
	snapshot := canonicalRuntimeSnapshot()
	if snapshot.DirtyHighWater != snapshot.ReconciledHighWater || snapshot.ReconciledHighWater != 2 {
		t.Fatalf("high-water was not drained: %+v", snapshot)
	}
}

type failingCanonicalEventStore struct{ err error }

func (store failingCanonicalEventStore) Append(context.Context, CanonicalEvent) (CanonicalAppendResult, error) {
	return CanonicalAppendResult{}, store.err
}

type recoveringCanonicalEventStore struct {
	mu      sync.Mutex
	failing bool
	err     error
	memory  *MemoryCanonicalEventStore
}

type blockingCanonicalEventStore struct{ entered chan struct{} }

func (store *blockingCanonicalEventStore) Append(ctx context.Context, _ CanonicalEvent) (CanonicalAppendResult, error) {
	select {
	case store.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return CanonicalAppendResult{}, ctx.Err()
}
func (store *blockingCanonicalEventStore) Events(ctx context.Context) ([]CanonicalEvent, error) {
	select {
	case store.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

type countingCanonicalEventStore struct {
	inner   CanonicalEventStore
	mu      sync.Mutex
	appends int
}

func (store *countingCanonicalEventStore) Append(ctx context.Context, event CanonicalEvent) (CanonicalAppendResult, error) {
	store.mu.Lock()
	store.appends++
	store.mu.Unlock()
	return store.inner.Append(ctx, event)
}
func (store *countingCanonicalEventStore) Events(ctx context.Context) ([]CanonicalEvent, error) {
	return store.inner.Events(ctx)
}
func (store *countingCanonicalEventStore) appendCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.appends
}

func (store *recoveringCanonicalEventStore) Append(ctx context.Context, event CanonicalEvent) (CanonicalAppendResult, error) {
	store.mu.Lock()
	failing, err := store.failing, store.err
	store.mu.Unlock()
	if failing {
		return CanonicalAppendResult{}, err
	}
	return store.memory.Append(ctx, event)
}
func (store *recoveringCanonicalEventStore) Events(ctx context.Context) ([]CanonicalEvent, error) {
	store.mu.Lock()
	failing, err := store.failing, store.err
	store.mu.Unlock()
	if failing {
		return nil, err
	}
	return store.memory.Events(ctx)
}
func (store *recoveringCanonicalEventStore) recover() {
	store.mu.Lock()
	store.failing = false
	store.mu.Unlock()
}
func (store failingCanonicalEventStore) Events(context.Context) ([]CanonicalEvent, error) {
	return nil, store.err
}

func TestCanonicalRuntimeReconcileFailureRetainsRetryableDirtyHighWater(t *testing.T) {
	canonicalRuntimeTestEnv(t, "shadow")
	runtime, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	runtime.events = failingCanonicalEventStore{err: errors.New("injected database outage")}
	entry := []byte(`{"id":"retry-note","kind":"note","text":"retry","createdAt":"2026-01-01T00:00:00Z"}` + "\n")
	if err := writeFileAtomicallyForCanonicalMode(meetingMemoryPath(), entry, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Reconcile(context.Background()); err == nil {
		t.Fatal("reconcile unexpectedly succeeded during database outage")
	}
	failed := canonicalRuntimeSnapshot()
	if failed.DirtyHighWater != 2 || failed.ReconciledHighWater != 0 || failed.Healthy {
		t.Fatalf("dirty retry state lost: %+v", failed)
	}
	runtime.events = NewMemoryCanonicalEventStore(runtime.registry)
	if err := runtime.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered := canonicalRuntimeSnapshot()
	if recovered.DirtyHighWater != recovered.ReconciledHighWater {
		t.Fatalf("retry did not drain: %+v", recovered)
	}
}

func TestCanonicalRuntimeReconcileLoopAutomaticallyRecoversFromOutage(t *testing.T) {
	canonicalRuntimeTestEnv(t, "shadow")
	runtime, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	store := &recoveringCanonicalEventStore{failing: true, err: errors.New("temporary database outage"), memory: NewMemoryCanonicalEventStore(runtime.registry)}
	runtime.events = store
	runtime.startReconcileLoop()
	entry := []byte(`{"id":"auto-retry","kind":"note","text":"retry","createdAt":"2026-01-01T00:00:00Z"}` + "\n")
	if err := writeFileAtomicallyForCanonicalMode(meetingMemoryPath(), entry, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(canonicalRuntimeSnapshot().Error, "temporary database outage") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	store.recover()
	for time.Now().Before(deadline) {
		snapshot := canonicalRuntimeSnapshot()
		if snapshot.ReconciledHighWater == snapshot.DirtyHighWater && snapshot.DirtyHighWater == 2 && snapshot.CheckpointValid {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("automatic retry did not recover: %+v", canonicalRuntimeSnapshot())
}

func TestCanonicalRuntimeShutdownCancelsStalledReconcile(t *testing.T) {
	canonicalRuntimeTestEnv(t, "shadow")
	runtime, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	blocked := &blockingCanonicalEventStore{entered: make(chan struct{}, 1)}
	runtime.events = blocked
	runtime.startReconcileLoop()
	runtime.reconcileSignal <- struct{}{}
	select {
	case <-blocked.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("reconcile did not enter stalled store")
	}
	done := make(chan struct{})
	go func() {
		closeCanonicalRuntime()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel stalled reconcile")
	}
}

func TestCanonicalRuntimeCheckpointRestartAndCorruptionFallback(t *testing.T) {
	dir := canonicalRuntimeTestEnv(t, "shadow")
	runtime, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	runtime.events = NewMemoryCanonicalEventStore(runtime.registry)
	entry := []byte(`{"id":"checkpoint-note","kind":"note","text":"checkpoint","createdAt":"2026-01-01T00:00:00Z"}` + "\n")
	if err := writeFileAtomicallyForCanonicalMode(meetingMemoryPath(), entry, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	checkpointPath := filepath.Join(dir, "canonical", "reconcile-checkpoint.json")
	closeCanonicalRuntime()
	restarted, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !restarted.checkpointValid || restarted.checkpointHighWater != 2 {
		t.Fatalf("valid checkpoint was not resumed: valid=%v highWater=%d", restarted.checkpointValid, restarted.checkpointHighWater)
	}
	if err := os.WriteFile(checkpointPath, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if valid, err := restarted.loadReconcileCheckpoint(); err != nil || valid || restarted.checkpointValid {
		t.Fatalf("corrupt checkpoint trusted: valid=%v err=%v", valid, err)
	}
	restarted.events = NewMemoryCanonicalEventStore(restarted.registry)
	restarted.dirtyHighWater = restarted.spoolHighWater()
	if err := restarted.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !restarted.checkpointValid {
		t.Fatal("full reconcile did not replace corrupt checkpoint")
	}
}

func TestCanonicalRuntimeValidCheckpointSkipsApplyAndSourceMismatchForcesFull(t *testing.T) {
	canonicalRuntimeTestEnv(t, "shadow")
	runtime, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	counting := &countingCanonicalEventStore{inner: NewMemoryCanonicalEventStore(runtime.registry)}
	runtime.events = counting
	entry := []byte(`{"id":"resume-note","kind":"note","text":"resume","createdAt":"2026-01-01T00:00:00Z"}` + "\n")
	if err := writeFileAtomicallyForCanonicalMode(meetingMemoryPath(), entry, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	baselineAppends := counting.appendCount()

	restarted := &CanonicalRuntime{mode: CanonicalModeShadow, dataDir: runtime.dataDir, root: runtime.root, tenantID: runtime.tenantID,
		registry: runtime.registry, spool: runtime.spool, versions: runtime.versions, events: counting}
	if valid, err := restarted.loadReconcileCheckpoint(); err != nil || !valid {
		t.Fatalf("load valid checkpoint: valid=%v err=%v", valid, err)
	}
	plan, err := restarted.buildLegacyPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resumed, err := restarted.tryResumeCheckpoint(context.Background(), plan); err != nil || !resumed {
		t.Fatalf("checkpoint did not resume: resumed=%v err=%v", resumed, err)
	}
	if counting.appendCount() != baselineAppends {
		t.Fatalf("valid resume called Apply: appends=%d want=%d", counting.appendCount(), baselineAppends)
	}

	// Source bytes drift without a matching spool/checkpoint generation: the
	// source hash invalidates resume and requires the caller's full path.
	drift := append(entry, []byte(`{"id":"drift","kind":"note","text":"drift","createdAt":"2026-01-01T00:00:01Z"}`+"\n")...)
	if err := writeFileAtomicallyUnfenced(meetingMemoryPath(), drift, 0o600, true); err != nil {
		t.Fatal(err)
	}
	driftPlan, err := restarted.buildLegacyPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resumed, err := restarted.tryResumeCheckpoint(context.Background(), driftPlan); err != nil || resumed || restarted.checkpointValid {
		t.Fatalf("source mismatch trusted checkpoint: resumed=%v valid=%v err=%v", resumed, restarted.checkpointValid, err)
	}
}

func TestCanonicalRuntimePostgresHealthRequiresDrainedKnownOutbox(t *testing.T) {
	canonicalRuntimeTestEnv(t, "shadow")
	runtime, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	runtime.events = NewMemoryCanonicalEventStore(runtime.registry)
	if err := runtime.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	runtime.postgres = &PostgresCanonicalStore{}
	runtime.outboxKnown = false
	if snapshot := canonicalRuntimeSnapshot(); snapshot.Healthy {
		t.Fatalf("unknown outbox reported healthy: %+v", snapshot)
	}
	runtime.outboxKnown, runtime.outboxPending = true, 1
	if snapshot := canonicalRuntimeSnapshot(); snapshot.Healthy {
		t.Fatalf("pending outbox reported healthy: %+v", snapshot)
	}
	runtime.outboxPending, runtime.outboxFailed = 0, 0
	if snapshot := canonicalRuntimeSnapshot(); !snapshot.Healthy {
		t.Fatalf("drained outbox did not satisfy health: %+v", snapshot)
	}
}

func TestCanonicalRuntimeDrainsVerifiedImportOutbox(t *testing.T) {
	ctx, pool := startDisposableCanonicalPostgres(t)
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresCanonicalStore(pool, registry)
	if err := store.ApplyMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	stateDigest := sha256Hex([]byte("legacy-state"))
	payload, payloadDigest, err := NewCanonicalEventPayload(registry, canonicalLegacyImportEventType, 1, map[string]any{
		"object_id": "runtime-outbox", "source_kind": "memory", "source_revision": 1,
		"room_id": "office", "status": "active", "deleted": false, "payload_sha256": stateDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	event := CanonicalEvent{EventID: uuid.New(), TenantID: "bonfire", AggregateType: "memory", AggregateID: "runtime-outbox", AggregateVersion: 1,
		EventType: canonicalLegacyImportEventType, SchemaVersion: 1, OccurredAt: now, RecordedAt: now,
		Actor: CanonicalPrincipalRef{Kind: "service", ID: "test"}, RoomID: "office", Classification: "internal", ACLVersion: 1,
		Payload: payload, PayloadSHA256: payloadDigest}
	if _, err := store.Append(ctx, event); err != nil {
		t.Fatal(err)
	}
	runtime := &CanonicalRuntime{postgres: store}
	if err := runtime.drainCanonicalImportOutbox(ctx); err != nil {
		t.Fatal(err)
	}
	if !runtime.outboxKnown || runtime.outboxPending != 0 || runtime.outboxFailed != 0 {
		t.Fatalf("outbox was not drained: known=%v pending=%d failed=%d", runtime.outboxKnown, runtime.outboxPending, runtime.outboxFailed)
	}
	var delivered bool
	if err := pool.QueryRow(ctx, "SELECT delivered_at IS NOT NULL FROM outbox WHERE event_id=$1", event.EventID).Scan(&delivered); err != nil || !delivered {
		t.Fatalf("delivered=%v err=%v", delivered, err)
	}
}
