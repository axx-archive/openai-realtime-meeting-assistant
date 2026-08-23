package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

type blockingEventsCanonicalEventStore struct {
	inner   CanonicalEventStore
	entered chan struct{}
}

func (store *blockingEventsCanonicalEventStore) Append(ctx context.Context, event CanonicalEvent) (CanonicalAppendResult, error) {
	return store.inner.Append(ctx, event)
}

func (store *blockingEventsCanonicalEventStore) Events(ctx context.Context) ([]CanonicalEvent, error) {
	select {
	case store.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

type releaseOnceEventsCanonicalEventStore struct {
	inner   CanonicalEventStore
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

type pacedEventsCanonicalEventStore struct {
	inner   CanonicalEventStore
	entered chan int
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (store *pacedEventsCanonicalEventStore) Append(ctx context.Context, event CanonicalEvent) (CanonicalAppendResult, error) {
	return store.inner.Append(ctx, event)
}

func (store *pacedEventsCanonicalEventStore) Events(ctx context.Context) ([]CanonicalEvent, error) {
	store.mu.Lock()
	store.calls++
	call := store.calls
	store.mu.Unlock()
	select {
	case store.entered <- call:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-store.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return store.inner.Events(ctx)
}

func (store *pacedEventsCanonicalEventStore) eventCalls() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.calls
}

func (store *releaseOnceEventsCanonicalEventStore) Append(ctx context.Context, event CanonicalEvent) (CanonicalAppendResult, error) {
	return store.inner.Append(ctx, event)
}

func (store *releaseOnceEventsCanonicalEventStore) Events(ctx context.Context) ([]CanonicalEvent, error) {
	store.mu.Lock()
	store.calls++
	call := store.calls
	store.mu.Unlock()
	if call == 1 {
		close(store.entered)
		select {
		case <-store.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return store.inner.Events(ctx)
}

func (store *releaseOnceEventsCanonicalEventStore) eventCalls() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.calls
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

func TestCanonicalReconcileRetryBacksOffOperatorRepairDivergence(t *testing.T) {
	if delay := canonicalReconcileRetryDelay(1, canonicalParityDivergenceError{Candidates: 25}); delay != 10*time.Minute {
		t.Fatalf("parity retry delay=%s, want operator-safe interval", delay)
	}
	if delay := canonicalReconcileRetryDelay(1, errors.New("temporary database outage")); delay < 250*time.Millisecond || delay > time.Second {
		t.Fatalf("transient retry delay=%s, want short exponential recovery", delay)
	}
}

func TestCanonicalShadowInitialReconcileRunsAfterServingGate(t *testing.T) {
	canonicalRuntimeTestEnv(t, "shadow")
	runtime, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entry := []byte(`{"id":"shadow-boot","kind":"note","text":"defer","createdAt":"2026-01-01T00:00:00Z"}` + "\n")
	if err := writeFileAtomicallyForCanonicalMode(meetingMemoryPath(), entry, 0o600); err != nil {
		t.Fatal(err)
	}
	blocked := &blockingEventsCanonicalEventStore{
		inner:   NewMemoryCanonicalEventStore(runtime.registry),
		entered: make(chan struct{}, 1),
	}
	runtime.events = blocked
	started := time.Now()
	runtime.startInitialShadowReconcile()
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("shadow serving gate blocked for %s", elapsed)
	}
	select {
	case <-blocked.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("deferred shadow reconcile never started")
	}
	if snapshot := canonicalRuntimeSnapshot(); snapshot.DirtyHighWater != runtime.spoolHighWater() {
		t.Fatalf("shadow reconcile lost the startup high-water: %+v", snapshot)
	}
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

func TestCanonicalRuntimeStalledReconcileDoesNotBlockSnapshotsOrWrites(t *testing.T) {
	dir := canonicalRuntimeTestEnv(t, "shadow")
	runtime, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	blocked := &blockingEventsCanonicalEventStore{
		inner:   NewMemoryCanonicalEventStore(runtime.registry),
		entered: make(chan struct{}, 1),
	}
	runtime.events = blocked
	first := []byte(`{"id":"first","kind":"note","text":"first","createdAt":"2026-01-01T00:00:00Z"}` + "\n")
	if err := writeFileAtomicallyForCanonicalMode(meetingMemoryPath(), first, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Reconcile(ctx) }()
	select {
	case <-blocked.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("reconcile did not enter stalled event scan")
	}

	snapshotDone := make(chan CanonicalRuntimeSnapshot, 1)
	go func() { snapshotDone <- canonicalRuntimeSnapshot() }()
	select {
	case snapshot := <-snapshotDone:
		if snapshot.DirtyHighWater != 2 {
			t.Fatalf("unexpected snapshot while reconcile stalled: %+v", snapshot)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("runtime snapshot blocked behind stalled reconcile")
	}

	second := []byte(`{"id":"second","kind":"note","text":"second","createdAt":"2026-01-01T00:00:01Z"}` + "\n")
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- appendFileDurably(filepath.Join(dir, "meeting-memory.jsonl"), second, 0o600)
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("covered legacy write blocked behind stalled reconcile")
	}
	if snapshot := canonicalRuntimeSnapshot(); snapshot.DirtyHighWater != 4 || runtime.spoolHighWater() != 4 {
		t.Fatalf("covered write did not advance canonical capture: snapshot=%+v spool=%d", snapshot, runtime.spoolHighWater())
	}

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("reconcile error=%v, want context canceled", err)
	}
}

func TestCanonicalRuntimeConcurrentMutationForcesOneFreshFollowUp(t *testing.T) {
	dir := canonicalRuntimeTestEnv(t, "shadow")
	runtime, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	gate := &releaseOnceEventsCanonicalEventStore{
		inner: NewMemoryCanonicalEventStore(runtime.registry), entered: make(chan struct{}), release: make(chan struct{}),
	}
	runtime.events = gate
	runtime.reconcileSignal = make(chan struct{}, 1)
	first := []byte(`{"id":"follow-up-first","kind":"note","text":"first","createdAt":"2026-01-01T00:00:00Z"}` + "\n")
	if err := writeFileAtomicallyForCanonicalMode(meetingMemoryPath(), first, 0o600); err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- runtime.Reconcile(context.Background()) }()
	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first reconcile did not reach parity scan")
	}
	second := []byte(`{"id":"follow-up-second","kind":"note","text":"second","createdAt":"2026-01-01T00:00:01Z"}` + "\n")
	if err := appendFileDurably(filepath.Join(dir, "meeting-memory.jsonl"), second, 0o600); err != nil {
		t.Fatal(err)
	}
	close(gate.release)
	if err := <-firstDone; !errors.Is(err, errCanonicalReconcileSuperseded) {
		t.Fatalf("concurrent reconcile error=%v, want superseded", err)
	}
	if runtime.checkpointValid || runtime.reconciledHighWater == runtime.dirtyHighWater {
		t.Fatalf("stale pass published a checkpoint: %+v", canonicalRuntimeSnapshot())
	}
	if snapshot := canonicalRuntimeSnapshot(); snapshot.ReconcileOutcome != "superseded" || snapshot.ReconcileSuperseded != 1 || snapshot.ReconcileAttempt != 2 {
		t.Fatalf("superseded attempt was not separately visible: %+v", snapshot)
	}
	select {
	case <-runtime.reconcileSignal:
		// Put the observed signal back; the next pass consumes/coalesces it.
		runtime.reconcileSignal <- struct{}{}
	default:
		t.Fatal("concurrent mutation did not retain a follow-up signal")
	}
	if err := runtime.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if snapshot := canonicalRuntimeSnapshot(); !snapshot.CheckpointValid || snapshot.ReconciledHighWater != snapshot.DirtyHighWater || snapshot.DirtyHighWater != 4 {
		t.Fatalf("fresh follow-up did not publish latest checkpoint: %+v", snapshot)
	} else if snapshot.ReconcileOutcome != "checkpointed" || snapshot.ReconcileSuperseded != 1 || snapshot.ReconcileAttempt != 4 {
		t.Fatalf("checkpoint progress telemetry lost superseded history: %+v", snapshot)
	}
	if calls := gate.eventCalls(); calls != 2 {
		t.Fatalf("reconcile event scans=%d, want stale pass plus exactly one follow-up", calls)
	}
}

func TestCanonicalRuntimeContinuousWriteChurnStaysFailClosedThenConverges(t *testing.T) {
	dir := canonicalRuntimeTestEnv(t, "shadow")
	runtime, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	gate := &pacedEventsCanonicalEventStore{
		inner: NewMemoryCanonicalEventStore(runtime.registry), entered: make(chan int, 1), release: make(chan struct{}),
	}
	runtime.events = gate
	runtime.reconcileSignal = make(chan struct{}, 1)
	path := filepath.Join(dir, "meeting-memory.jsonl")
	first := []byte(`{"id":"churn-00","kind":"note","text":"generation 0","createdAt":"2026-01-01T00:00:00Z"}` + "\n")
	if err := writeFileAtomicallyForCanonicalMode(path, first, 0o600); err != nil {
		t.Fatal(err)
	}

	const supersededPasses = 4
	for pass := 0; pass < supersededPasses; pass++ {
		before := canonicalRuntimeSnapshot()
		done := make(chan error, 1)
		go func() { done <- runtime.Reconcile(context.Background()) }()
		select {
		case call := <-gate.entered:
			if call != pass+1 {
				t.Fatalf("event scan call=%d, want %d", call, pass+1)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("pass %d did not reach parity", pass)
		}
		entry := []byte(fmt.Sprintf(`{"id":"churn-%02d","kind":"note","text":"generation %d","createdAt":"2026-01-01T00:00:%02dZ"}`+"\n", pass+1, pass+1, pass+1))
		if err := appendFileDurably(path, entry, 0o600); err != nil {
			t.Fatal(err)
		}
		gate.release <- struct{}{}
		if err := <-done; !errors.Is(err, errCanonicalReconcileSuperseded) {
			t.Fatalf("pass %d error=%v, want superseded", pass, err)
		}
		after := canonicalRuntimeSnapshot()
		if after.CheckpointValid || after.ReconciledHighWater != 0 || after.CheckpointHighWater != 0 {
			t.Fatalf("pass %d published churn as progress: %+v", pass, after)
		}
		if after.DirtyHighWater != before.DirtyHighWater+2 || after.ReconcileOutcome != "superseded" || after.ReconcileSuperseded != uint64(pass+1) {
			t.Fatalf("pass %d lost dirty/superseded truth: before=%+v after=%+v", pass, before, after)
		}
	}

	finalDone := make(chan error, 1)
	go func() { finalDone <- runtime.Reconcile(context.Background()) }()
	select {
	case call := <-gate.entered:
		if call != supersededPasses+1 {
			t.Fatalf("final event scan call=%d", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("quiet reconcile did not reach parity")
	}
	gate.release <- struct{}{}
	if err := <-finalDone; err != nil {
		t.Fatal(err)
	}
	if snapshot := canonicalRuntimeSnapshot(); !snapshot.CheckpointValid || snapshot.ReconciledHighWater != snapshot.DirtyHighWater || snapshot.CheckpointHighWater != snapshot.DirtyHighWater || snapshot.ReconcileOutcome != "checkpointed" || snapshot.ReconcileSuperseded != supersededPasses {
		t.Fatalf("quiet pass did not converge exact latest truth: %+v", snapshot)
	}
}

func TestCanonicalRuntimeSupersededLoopCoalescesBurstBehindQuietWindow(t *testing.T) {
	dir := canonicalRuntimeTestEnv(t, "shadow")
	runtime, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	runtime.reconcileQuietWindow = 80 * time.Millisecond
	runtime.reconcileMaxLatency = 400 * time.Millisecond
	gate := &pacedEventsCanonicalEventStore{
		inner: NewMemoryCanonicalEventStore(runtime.registry), entered: make(chan int, 1), release: make(chan struct{}),
	}
	runtime.events = gate
	runtime.startReconcileLoop()
	path := filepath.Join(dir, "meeting-memory.jsonl")
	first := []byte(`{"id":"coalesce-00","kind":"note","text":"generation 0","createdAt":"2026-01-01T00:00:00Z"}` + "\n")
	if err := writeFileAtomicallyForCanonicalMode(path, first, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case call := <-gate.entered:
		if call != 1 {
			t.Fatalf("first event scan call=%d", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reconcile loop did not start")
	}

	const burstWrites = 12
	for index := 1; index <= burstWrites; index++ {
		entry := []byte(fmt.Sprintf(`{"id":"coalesce-%02d","kind":"note","text":"generation %d","createdAt":"2026-01-01T00:00:%02dZ"}`+"\n", index, index, index))
		started := time.Now()
		if err := appendFileDurably(path, entry, 0o600); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
			t.Fatalf("write %d blocked behind full reconcile for %s", index, elapsed)
		}
	}
	releasedAt := time.Now()
	gate.release <- struct{}{}
	select {
	case call := <-gate.entered:
		if call != 2 {
			t.Fatalf("follow-up event scan call=%d", call)
		}
		if elapsed := time.Since(releasedAt); elapsed < 60*time.Millisecond {
			t.Fatalf("superseded pass busy-rescanned after %s", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("coalesced follow-up did not start")
	}
	if snapshot := canonicalRuntimeSnapshot(); snapshot.CheckpointValid || snapshot.ReconciledHighWater != 0 || snapshot.ReconcileOutcome != "superseded" || snapshot.ReconcileSuperseded != 1 {
		t.Fatalf("superseded loop pass was misreported: %+v", snapshot)
	}
	gate.release <- struct{}{}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := canonicalRuntimeSnapshot()
		if snapshot.CheckpointValid && snapshot.ReconciledHighWater == snapshot.DirtyHighWater && snapshot.ReconcileOutcome == "checkpointed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if snapshot := canonicalRuntimeSnapshot(); !snapshot.CheckpointValid || snapshot.ReconciledHighWater != snapshot.DirtyHighWater || snapshot.CheckpointHighWater != snapshot.DirtyHighWater {
		t.Fatalf("coalesced quiet pass did not converge: %+v", snapshot)
	}
	time.Sleep(200 * time.Millisecond)
	if calls := gate.eventCalls(); calls != 2 {
		t.Fatalf("event scans=%d, want one superseded attempt and one quiet follow-up", calls)
	}
}

func TestCanonicalRuntimeReconcileWindowBoundsContinuousWriteCadence(t *testing.T) {
	dir := canonicalRuntimeTestEnv(t, "shadow")
	runtime, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	runtime.reconcileQuietWindow = 200 * time.Millisecond
	runtime.reconcileMaxLatency = 350 * time.Millisecond
	gate := &pacedEventsCanonicalEventStore{
		inner: NewMemoryCanonicalEventStore(runtime.registry), entered: make(chan int, 1), release: make(chan struct{}),
	}
	runtime.events = gate
	runtime.startReconcileLoop()
	path := filepath.Join(dir, "meeting-memory.jsonl")
	first := []byte(`{"id":"bounded-00","kind":"note","text":"generation 0","createdAt":"2026-01-01T00:00:00Z"}` + "\n")
	started := time.Now()
	if err := writeFileAtomicallyForCanonicalMode(path, first, 0o600); err != nil {
		t.Fatal(err)
	}

	stopWrites := make(chan struct{})
	writesDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for index := 1; ; index++ {
			select {
			case <-stopWrites:
				writesDone <- nil
				return
			case <-ticker.C:
				entry := []byte(fmt.Sprintf(`{"id":"bounded-%02d","kind":"note","text":"generation %d","createdAt":"2026-01-01T00:00:%02dZ"}`+"\n", index, index, index%60))
				if err := appendFileDurably(path, entry, 0o600); err != nil {
					writesDone <- err
					return
				}
			}
		}
	}()
	select {
	case call := <-gate.entered:
		if call != 1 {
			t.Fatalf("first event scan call=%d", call)
		}
		elapsed := time.Since(started)
		if elapsed < 250*time.Millisecond {
			t.Fatalf("continuous writes did not hold the quiet window; scan began after %s", elapsed)
		}
		if elapsed > time.Second {
			t.Fatalf("continuous writes starved the bounded reconcile attempt for %s", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("maximum reconcile latency did not bound continuous write churn")
	}
	close(stopWrites)
	if err := <-writesDone; err != nil {
		t.Fatal(err)
	}
	// This mutation is definitively after the first pass sampled its target.
	final := []byte(`{"id":"bounded-final","kind":"note","text":"final generation","createdAt":"2026-01-01T00:01:00Z"}` + "\n")
	if err := appendFileDurably(path, final, 0o600); err != nil {
		t.Fatal(err)
	}
	gate.release <- struct{}{}

	select {
	case call := <-gate.entered:
		if call != 2 {
			t.Fatalf("follow-up event scan call=%d", call)
		}
		if snapshot := canonicalRuntimeSnapshot(); snapshot.CheckpointValid || snapshot.ReconcileOutcome != "superseded" || snapshot.ReconcileSuperseded != 1 {
			t.Fatalf("bounded churn attempt was misreported as progress: %+v", snapshot)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued quiet follow-up did not run")
	}
	gate.release <- struct{}{}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := canonicalRuntimeSnapshot()
		if snapshot.CheckpointValid && snapshot.ReconciledHighWater == snapshot.DirtyHighWater && snapshot.ReconcileOutcome == "checkpointed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if snapshot := canonicalRuntimeSnapshot(); !snapshot.CheckpointValid || snapshot.ReconciledHighWater != snapshot.DirtyHighWater || snapshot.ReconcileSuperseded != 1 {
		t.Fatalf("bounded cadence follow-up did not converge: %+v", snapshot)
	}
	time.Sleep(250 * time.Millisecond)
	if calls := gate.eventCalls(); calls != 2 {
		t.Fatalf("event scans=%d, want one max-latency attempt and one quiet follow-up", calls)
	}
}

func TestCanonicalRuntimeVisiblePublishBeforeFailedCommitCannotClearHealthOrCheckpoint(t *testing.T) {
	canonicalRuntimeTestEnv(t, "shadow")
	runtime, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	memoryEvents := NewMemoryCanonicalEventStore(runtime.registry)
	runtime.events = memoryEvents
	first := []byte(`{"id":"publish-window-1","kind":"note","text":"baseline","createdAt":"2026-01-01T00:00:00Z"}` + "\n")
	if err := writeFileAtomicallyForCanonicalMode(meetingMemoryPath(), first, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	targetSampled := make(chan struct{})
	targetRelease := make(chan struct{})
	published := make(chan struct{})
	publishRelease := make(chan struct{})
	var targetOnce, targetReleaseOnce, publishedOnce, publishReleaseOnce sync.Once
	runtime.reconcileAfterTarget = func() {
		targetOnce.Do(func() { close(targetSampled) })
		<-targetRelease
	}
	runtime.mutationAfterLegacyPublish = func() {
		publishedOnce.Do(func() { close(published) })
		<-publishRelease
	}
	t.Cleanup(func() {
		targetReleaseOnce.Do(func() { close(targetRelease) })
		publishReleaseOnce.Do(func() { close(publishRelease) })
	})
	gate := &releaseOnceEventsCanonicalEventStore{inner: memoryEvents, entered: make(chan struct{}), release: make(chan struct{})}
	runtime.events = gate
	reconcileDone := make(chan error, 1)
	go func() { reconcileDone <- runtime.Reconcile(context.Background()) }()
	select {
	case <-targetSampled:
	case <-time.After(2 * time.Second):
		t.Fatal("reconcile did not sample its target")
	}
	second := []byte(`{"id":"publish-window-2","kind":"note","text":"visible before commit","createdAt":"2026-01-01T00:00:01Z"}` + "\n")
	writeDone := make(chan error, 1)
	go func() { writeDone <- appendFileDurably(meetingMemoryPath(), second, 0o600) }()
	select {
	case <-published:
	case <-time.After(2 * time.Second):
		t.Fatal("writer did not publish before its capture commit")
	}
	targetReleaseOnce.Do(func() { close(targetRelease) })
	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("reconcile did not consume the visible uncommitted source")
	}
	runtime.spool.mu.Lock()
	runtime.spool.poisoned = true
	runtime.spool.mu.Unlock()
	publishReleaseOnce.Do(func() { close(publishRelease) })
	if err := <-writeDone; err == nil || !strings.Contains(err.Error(), "canonical commit failed") {
		t.Fatalf("writer commit error=%v", err)
	}
	close(gate.release)
	if err := <-reconcileDone; err == nil || !strings.Contains(err.Error(), "capture integrity changed") {
		t.Fatalf("reconcile error=%v, want capture-integrity refusal", err)
	}
	raw, err := os.ReadFile(meetingMemoryPath())
	if err != nil || !bytes.Contains(raw, []byte("visible before commit")) {
		t.Fatalf("adversarial source was not visibly published: err=%v body=%s", err, raw)
	}
	snapshot := canonicalRuntimeSnapshot()
	if snapshot.CheckpointValid || snapshot.CheckpointHighWater != 2 || snapshot.ReconciledHighWater != 2 || snapshot.DirtyHighWater != 2 || snapshot.Healthy {
		t.Fatalf("failed capture was checkpointed or reported healthy: %+v", snapshot)
	}
	if !strings.Contains(snapshot.Error, "canonical commit failed") || snapshot.ReconcileOutcome != "failed" {
		t.Fatalf("capture failure was cleared by reconcile: %+v", snapshot)
	}
}

func TestCanonicalRuntimeCaptureFailureRejectsLaterWritesUntilRestartRecovery(t *testing.T) {
	dir := canonicalRuntimeTestEnv(t, "shadow")
	runtime, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "meeting-memory.jsonl")

	originalOpen := canonicalCaptureOpenAppend
	openCalls := 0
	failCommit := true
	canonicalCaptureOpenAppend = func(path string) (canonicalAppendFile, bool, error) {
		_, statErr := os.Stat(path)
		created := errors.Is(statErr, os.ErrNotExist)
		file, openErr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if openErr != nil {
			return nil, false, openErr
		}
		openCalls++
		wrapped := &faultCanonicalAppendFile{File: file, writeBytes: -1}
		if failCommit && openCalls == 2 {
			// The prepare is open one. Fail the commit's first fsync; the
			// following rollback fsync succeeds, so the handle stays usable.
			wrapped.failSync = 1
		}
		return wrapped, created, nil
	}
	t.Cleanup(func() { canonicalCaptureOpenAppend = originalOpen })

	first := []byte(`{"id":"capture-recovery-1","kind":"note","text":"visible first generation","createdAt":"2026-01-01T00:00:00Z"}` + "\n")
	firstErr := writeFileAtomicallyForCanonicalMode(path, first, 0o600)
	if firstErr == nil || !strings.Contains(firstErr.Error(), "canonical commit failed") {
		t.Fatalf("first write error=%v, want capture commit failure", firstErr)
	}
	failCommit = false
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, first) {
		t.Fatalf("first generation was not visibly published: err=%v body=%q", err, raw)
	}
	runtime.spool.mu.Lock()
	poisoned := runtime.spool.poisoned
	records := len(runtime.spool.records)
	runtime.spool.mu.Unlock()
	if poisoned || records != 1 || runtime.spoolHighWater() != 1 || len(runtime.spool.CommittedFacts()) != 0 {
		t.Fatalf("commit failure was not rollback-clean: poisoned=%v records=%d highWater=%d facts=%d", poisoned, records, runtime.spoolHighWater(), len(runtime.spool.CommittedFacts()))
	}

	second := []byte(`{"id":"capture-recovery-2","kind":"note","text":"must not publish before recovery","createdAt":"2026-01-01T00:00:01Z"}` + "\n")
	secondErr := appendFileDurably(path, second, 0o600)
	if secondErr == nil || !strings.Contains(secondErr.Error(), "rejected before prepare") || !errors.Is(secondErr, firstErr) {
		t.Fatalf("second covered write error=%v, want latched pre-prepare rejection", secondErr)
	}
	if raw, err = os.ReadFile(path); err != nil || !bytes.Equal(raw, first) {
		t.Fatalf("rejected write changed legacy bytes: err=%v body=%q", err, raw)
	}
	if runtime.spoolHighWater() != 1 || len(runtime.spool.CommittedFacts()) != 0 {
		t.Fatalf("rejected write entered capture chain: highWater=%d facts=%d", runtime.spoolHighWater(), len(runtime.spool.CommittedFacts()))
	}

	closeCanonicalRuntime()
	restarted, err := initializeCanonicalRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	facts := restarted.spool.CommittedFacts()
	if restarted.captureErr != nil || restarted.spool.Frozen("memory") || len(facts) != 1 || facts[0].Prepare.AfterStateDigest != sha256Hex(first) || facts[0].Commit.Sequence != 2 {
		t.Fatalf("restart did not prove and commit the visible after-state: captureErr=%v frozen=%v facts=%+v", restarted.captureErr, restarted.spool.Frozen("memory"), facts)
	}
	if snapshot := canonicalRuntimeSnapshot(); snapshot.Pending != 0 || snapshot.HighWater != 2 {
		t.Fatalf("restart recovery left the capture chain unresolved: %+v", snapshot)
	}
	if err := appendFileDurably(path, second, 0o600); err != nil {
		t.Fatalf("covered writes did not reopen after recovery: %v", err)
	}
	if raw, err = os.ReadFile(path); err != nil || !bytes.Equal(raw, append(append([]byte(nil), first...), second...)) {
		t.Fatalf("post-recovery write did not publish exact bytes: err=%v body=%q", err, raw)
	}
	if facts = restarted.spool.CommittedFacts(); len(facts) != 2 || facts[1].Commit.Sequence != 4 {
		t.Fatalf("post-recovery write was not captured: %+v", facts)
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
