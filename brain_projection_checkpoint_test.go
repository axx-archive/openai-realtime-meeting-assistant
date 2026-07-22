package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func testBrainProjectionManifest(value string) [sha256.Size]byte {
	return sha256.Sum256([]byte(value))
}

type testBrainProjectionSourceResolver struct {
	mu    sync.RWMutex
	state BrainProjectionSourceState
	err   error
	calls atomic.Int64
}

func (resolver *testBrainProjectionSourceResolver) ResolveBrainProjectionSourceState(_ context.Context, tx pgx.Tx, _ BrainProjectionCheckpointKey) (BrainProjectionSourceState, error) {
	if tx == nil {
		return BrainProjectionSourceState{}, errors.New("source resolution was not transaction-fenced")
	}
	resolver.calls.Add(1)
	resolver.mu.RLock()
	defer resolver.mu.RUnlock()
	if resolver.err != nil {
		return BrainProjectionSourceState{}, resolver.err
	}
	return resolver.state, nil
}

func (resolver *testBrainProjectionSourceResolver) set(highWater int64, manifest string) BrainProjectionSourceState {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	resolver.state = BrainProjectionSourceState{HighWater: highWater, ManifestSHA256: testBrainProjectionManifest(manifest)}
	return resolver.state
}

func (resolver *testBrainProjectionSourceResolver) setError(err error) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	resolver.err = err
}

type testBrainProjectionSink struct {
	mu        sync.Mutex
	outputs   map[string]BrainProjectionDerivedOutput
	receipts  map[string]BrainProjectionDurableReceipt
	appends   int
	verifyErr error
	durableAt time.Time
}

func newTestBrainProjectionSink() *testBrainProjectionSink {
	return &testBrainProjectionSink{outputs: map[string]BrainProjectionDerivedOutput{}, receipts: map[string]BrainProjectionDurableReceipt{}}
}

func (sink *testBrainProjectionSink) PutBrainProjectionDerived(_ context.Context, output BrainProjectionDerivedOutput) (BrainProjectionDurableReceipt, error) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if prior, found := sink.outputs[output.DerivedID]; found {
		if prior.DerivedSHA256 != output.DerivedSHA256 || prior.SourceManifestSHA256 != output.SourceManifestSHA256 ||
			prior.RebuildFenceToken != output.RebuildFenceToken || !bytes.Equal(prior.Body, output.Body) {
			return BrainProjectionDurableReceipt{}, ErrBrainProjectionConflict
		}
		return sink.receipts[output.DerivedID], nil
	}
	durableAt := sink.durableAt
	if durableAt.IsZero() {
		durableAt = time.Now().UTC()
	}
	receipt := BrainProjectionDurableReceipt{
		DerivedID: output.DerivedID, DerivedHighWater: output.DerivedHighWater, DerivedSHA256: output.DerivedSHA256,
		SourceManifestSHA256: output.SourceManifestSHA256, RebuildFenceToken: output.RebuildFenceToken, DurableAt: durableAt,
	}
	sink.outputs[output.DerivedID], sink.receipts[output.DerivedID] = output, receipt
	sink.appends++
	return receipt, nil
}

func (sink *testBrainProjectionSink) VerifyBrainProjectionDerived(_ context.Context, output BrainProjectionDerivedOutput, receipt BrainProjectionDurableReceipt) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.verifyErr != nil {
		return sink.verifyErr
	}
	stored, found := sink.outputs[receipt.DerivedID]
	if !found || stored.DerivedID != output.DerivedID || stored.DerivedSHA256 != receipt.DerivedSHA256 || !bytes.Equal(stored.Body, output.Body) {
		return errors.New("derived bytes are not durably present")
	}
	return nil
}

func (sink *testBrainProjectionSink) appendCount() int {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.appends
}

func testBrainProjectionKey(sourceFamily string) BrainProjectionCheckpointKey {
	return BrainProjectionCheckpointKey{
		TenantID: "tenant-shareability", ProjectorVersion: "meeting-brain/v2", RoomID: "office",
		SittingID: "sitting-2026-07-22", SourceFamily: sourceFamily,
	}
}

func testBrainProjectionStore(t *testing.T, source BrainProjectionSourceState) (context.Context, *PostgresCanonicalStore, *PostgresBrainProjectionCheckpointStore, *testBrainProjectionSourceResolver) {
	t.Helper()
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	resolver := &testBrainProjectionSourceResolver{state: source}
	return ctx, canonical, NewBrainProjectionCheckpointStore(canonical, resolver), resolver
}

func testBrainProjectionOutput(t *testing.T, key BrainProjectionCheckpointKey, generation int64, token BrainProjectionRebuildFenceToken, source BrainProjectionSourceState, derivedHighWater int64, body string) BrainProjectionDerivedOutput {
	t.Helper()
	output, err := NewBrainProjectionDerivedOutput(key, generation, token, source, derivedHighWater, []byte(body))
	if err != nil {
		t.Fatalf("new derived output: %v", err)
	}
	return output
}

func TestBrainProjectionCheckpointMigrationIsAdditiveAndDedicated(t *testing.T) {
	migrations, err := loadCanonicalMigrations()
	if err != nil || len(migrations) < 4 || migrations[3].Name != "0004_brain_projection_checkpoints.sql" {
		t.Fatalf("migrations=%+v err=%v", migrations, err)
	}
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	rows, err := canonical.pool.Query(ctx, `SELECT a.attname FROM pg_index i
		JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=ANY(i.indkey)
		WHERE i.indrelid='brain_projection_checkpoints'::regclass AND i.indisprimary
		ORDER BY array_position(i.indkey,a.attnum)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, column)
	}
	want := []string{"tenant_id", "projector_version", "room_id", "sitting_id", "source_family"}
	if !reflect.DeepEqual(columns, want) {
		t.Fatalf("primary key=%v, want %v", columns, want)
	}
	for _, column := range []string{"source_manifest_sha256", "published_rebuild_fence_token", "rebuild_source_manifest_sha256", "rebuild_fence_token"} {
		var found bool
		if err := canonical.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns
			WHERE table_name='brain_projection_checkpoints' AND column_name=$1)`, column).Scan(&found); err != nil || !found {
			t.Fatalf("required column %s found=%v err=%v", column, found, err)
		}
	}
}

func TestBrainProjectionStoreHasNoPublicPublicationBypassAndSinkMustVerify(t *testing.T) {
	if _, exposed := reflect.TypeOf((*PostgresBrainProjectionCheckpointStore)(nil)).MethodByName("Publish"); exposed {
		t.Fatal("checkpoint store must not expose direct Publish")
	}
	source := BrainProjectionSourceState{HighWater: 4, ManifestSHA256: testBrainProjectionManifest("manifest-4")}
	ctx, canonical, store, _ := testBrainProjectionStore(t, source)
	sink := newTestBrainProjectionSink()
	sink.verifyErr = errors.New("forged receipt")
	publisher := newBrainProjectionCheckpointPublisher(store, sink)
	output := testBrainProjectionOutput(t, testBrainProjectionKey("transcript"), 0, BrainProjectionRebuildFenceToken{}, source, 1, "body")
	if _, err := publisher.Publish(ctx, output); !errors.Is(err, ErrBrainProjectionDerivedNotDurable) {
		t.Fatalf("unverified derived publication error=%v", err)
	}
	var count int
	if err := canonical.pool.QueryRow(ctx, "SELECT count(*) FROM brain_projection_checkpoints").Scan(&count); err != nil || count != 0 {
		t.Fatalf("unverified receipt advanced checkpoint: count=%d err=%v", count, err)
	}
}

func TestBrainProjectionDerivedIdentityBindsSourceHighWaterExplicitly(t *testing.T) {
	key := testBrainProjectionKey("transcript")
	manifest := testBrainProjectionManifest("same-manifest")
	left := testBrainProjectionOutput(t, key, 0, BrainProjectionRebuildFenceToken{}, BrainProjectionSourceState{HighWater: 10, ManifestSHA256: manifest}, 2, "same bytes")
	right := testBrainProjectionOutput(t, key, 0, BrainProjectionRebuildFenceToken{}, BrainProjectionSourceState{HighWater: 11, ManifestSHA256: manifest}, 2, "same bytes")
	if left.DerivedID == right.DerivedID {
		t.Fatalf("derived id did not bind source high-water: %q", left.DerivedID)
	}
}

func TestBrainProjectionDerivedDurableCheckpointAbsentReplaysAfterRestart(t *testing.T) {
	source := BrainProjectionSourceState{HighWater: 41, ManifestSHA256: testBrainProjectionManifest("source-41")}
	ctx, canonical, store, resolver := testBrainProjectionStore(t, source)
	key := testBrainProjectionKey("transcript")
	output := testBrainProjectionOutput(t, key, 0, BrainProjectionRebuildFenceToken{}, source, 7, "stable derived bytes")
	sink := newTestBrainProjectionSink()
	publisher := newBrainProjectionCheckpointPublisher(store, sink)
	publisher.failpoint = func(point string) error {
		if point == "after_derived_before_checkpoint" {
			return errors.New("simulated crash")
		}
		return nil
	}
	if result, err := publisher.Publish(ctx, output); err == nil || result.DerivedID != output.DerivedID {
		t.Fatalf("crash result=%+v err=%v", result, err)
	}
	status, err := store.Status(ctx, key)
	if err != nil || !status.Stale || status.Reason != BrainProjectionStaleCheckpointAbsent || sink.appendCount() != 1 {
		t.Fatalf("post-crash status=%+v appends=%d err=%v", status, sink.appendCount(), err)
	}
	restartedStore := NewPostgresBrainProjectionCheckpointStore(canonical.pool, resolver)
	restarted := newBrainProjectionCheckpointPublisher(restartedStore, sink)
	first, err := restarted.Publish(ctx, output)
	if err != nil || first.Existing {
		t.Fatalf("restart publish=%+v err=%v", first, err)
	}
	replay, err := restarted.Publish(ctx, output)
	if err != nil || !replay.Existing || replay.DerivedID != output.DerivedID || sink.appendCount() != 1 {
		t.Fatalf("replay=%+v appends=%d err=%v", replay, sink.appendCount(), err)
	}
}

func TestBrainProjectionAuthoritativeSourceRejectsFutureRegressingAndChangedManifests(t *testing.T) {
	source := BrainProjectionSourceState{HighWater: 20, ManifestSHA256: testBrainProjectionManifest("source-20")}
	ctx, _, store, resolver := testBrainProjectionStore(t, source)
	key := testBrainProjectionKey("transcript")
	sink := newTestBrainProjectionSink()
	publisher := newBrainProjectionCheckpointPublisher(store, sink)
	initial := testBrainProjectionOutput(t, key, 0, BrainProjectionRebuildFenceToken{}, source, 4, "initial")
	if _, err := publisher.Publish(ctx, initial); err != nil {
		t.Fatal(err)
	}
	for _, request := range []BrainProjectionRebuildRequest{
		{Key: key, ExpectedGeneration: 0, StartSourceHighWater: 0, EndSourceHighWater: 21},
		{Key: key, ExpectedGeneration: 0, StartSourceHighWater: 0, EndSourceHighWater: 19},
	} {
		if _, err := store.BeginRebuild(ctx, request); !errors.Is(err, ErrBrainProjectionSourceMoved) {
			t.Fatalf("non-authoritative range %+v error=%v", request, err)
		}
	}
	resolver.set(19, "source-19")
	if _, err := store.BeginRebuild(ctx, BrainProjectionRebuildRequest{Key: key, ExpectedGeneration: 0, StartSourceHighWater: 0, EndSourceHighWater: 19}); !errors.Is(err, ErrBrainProjectionRegression) {
		t.Fatalf("replacement below published cursor error=%v", err)
	}
	changed := resolver.set(21, "source-21")
	staleOutput := testBrainProjectionOutput(t, key, 0, BrainProjectionRebuildFenceToken{}, source, 5, "stale source")
	if _, err := publisher.Publish(ctx, staleOutput); !errors.Is(err, ErrBrainProjectionSourceMoved) {
		t.Fatalf("stale publication error=%v", err)
	}
	wrongManifest := changed
	wrongManifest.ManifestSHA256 = testBrainProjectionManifest("forged-manifest")
	forged := testBrainProjectionOutput(t, key, 0, BrainProjectionRebuildFenceToken{}, wrongManifest, 5, "wrong manifest")
	if _, err := publisher.Publish(ctx, forged); !errors.Is(err, ErrBrainProjectionSourceMoved) {
		t.Fatalf("forged manifest publication error=%v", err)
	}
}

func TestBrainProjectionRebuildBindsOpaqueFenceManifestAndDurabilityTime(t *testing.T) {
	source := BrainProjectionSourceState{HighWater: 30, ManifestSHA256: testBrainProjectionManifest("source-30")}
	ctx, _, store, _ := testBrainProjectionStore(t, source)
	key := testBrainProjectionKey("transcript")
	sink := newTestBrainProjectionSink()
	publisher := newBrainProjectionCheckpointPublisher(store, sink)
	initial := testBrainProjectionOutput(t, key, 0, BrainProjectionRebuildFenceToken{}, source, 9, "generation zero")
	if _, err := publisher.Publish(ctx, initial); err != nil {
		t.Fatal(err)
	}
	request := BrainProjectionRebuildRequest{Key: key, ExpectedGeneration: 0, StartSourceHighWater: 4, EndSourceHighWater: 30}
	fence, err := store.BeginRebuild(ctx, request)
	if err != nil || fence.Token.isZero() || fence.SourceManifestSHA256 != source.ManifestSHA256 {
		t.Fatalf("rebuild fence=%+v err=%v", fence, err)
	}
	retry, err := store.BeginRebuild(ctx, request)
	if err != nil || !retry.Existing || retry.Token != fence.Token {
		t.Fatalf("idempotent rebuild=%+v err=%v", retry, err)
	}
	conflict := request
	conflict.StartSourceHighWater = 5
	if _, err := store.BeginRebuild(ctx, conflict); !errors.Is(err, ErrBrainProjectionFenceLost) {
		t.Fatalf("conflicting rebuild error=%v", err)
	}
	old := testBrainProjectionOutput(t, key, 0, BrainProjectionRebuildFenceToken{}, source, 10, "old worker")
	if _, err := publisher.Publish(ctx, old); !errors.Is(err, ErrBrainProjectionFenceLost) {
		t.Fatalf("old worker error=%v", err)
	}
	wrongToken, err := newBrainProjectionRebuildFenceToken()
	if err != nil {
		t.Fatal(err)
	}
	forged := testBrainProjectionOutput(t, key, 1, wrongToken, source, 2, "forged fence")
	if _, err := publisher.Publish(ctx, forged); !errors.Is(err, ErrBrainProjectionFenceLost) {
		t.Fatalf("forged fence error=%v", err)
	}
	tooEarlySink := newTestBrainProjectionSink()
	tooEarlySink.durableAt = fence.StartedAt.Add(-time.Second)
	tooEarlyPublisher := newBrainProjectionCheckpointPublisher(store, tooEarlySink)
	complete := testBrainProjectionOutput(t, key, 1, fence.Token, source, 2, "complete rebuild")
	if _, err := tooEarlyPublisher.Publish(ctx, complete); !errors.Is(err, ErrBrainProjectionDerivedNotDurable) {
		t.Fatalf("pre-fence durable receipt error=%v", err)
	}
	result, err := publisher.Publish(ctx, complete)
	if err != nil || result.Checkpoint.PublishedGeneration != 1 || result.Checkpoint.PublishedRebuildFenceToken != fence.Token {
		t.Fatalf("complete rebuild=%+v err=%v", result, err)
	}
}

func TestBrainProjectionLoadRejectsTamperedDerivedIdentity(t *testing.T) {
	source := BrainProjectionSourceState{HighWater: 8, ManifestSHA256: testBrainProjectionManifest("source-8")}
	ctx, canonical, store, _ := testBrainProjectionStore(t, source)
	key := testBrainProjectionKey("transcript")
	output := testBrainProjectionOutput(t, key, 0, BrainProjectionRebuildFenceToken{}, source, 2, "derived")
	if _, err := newBrainProjectionCheckpointPublisher(store, newTestBrainProjectionSink()).Publish(ctx, output); err != nil {
		t.Fatal(err)
	}
	if _, err := canonical.pool.Exec(ctx, `UPDATE brain_projection_checkpoints SET derived_id='brain-projection-tampered'
		WHERE tenant_id=$1 AND projector_version=$2 AND room_id=$3 AND sitting_id=$4 AND source_family=$5`,
		key.TenantID, key.ProjectorVersion, key.RoomID, key.SittingID, key.SourceFamily); err != nil {
		t.Fatal(err)
	}
	status, err := store.Status(ctx, key)
	if !errors.Is(err, ErrBrainProjectionCheckpointCorrupt) || !status.Stale || status.Reason != BrainProjectionStaleCheckpointCorrupt {
		t.Fatalf("tampered status=%+v err=%v", status, err)
	}
}

func TestBrainProjectionStatusUsesAuthoritativeManifestAndFailsClosedOnResolverOutage(t *testing.T) {
	source := BrainProjectionSourceState{HighWater: 13, ManifestSHA256: testBrainProjectionManifest("source-13")}
	ctx, _, store, resolver := testBrainProjectionStore(t, source)
	key := testBrainProjectionKey("transcript")
	output := testBrainProjectionOutput(t, key, 0, BrainProjectionRebuildFenceToken{}, source, 3, "derived")
	if _, err := newBrainProjectionCheckpointPublisher(store, newTestBrainProjectionSink()).Publish(ctx, output); err != nil {
		t.Fatal(err)
	}
	resolver.set(13, "source-13-rewritten")
	status, err := store.Status(ctx, key)
	if err != nil || !status.Stale || status.Reason != BrainProjectionStaleSourceManifestChanged {
		t.Fatalf("same-watermark manifest drift status=%+v err=%v", status, err)
	}
	resolver.setError(errors.New("canonical source unavailable"))
	status, err = store.Status(ctx, key)
	if !errors.Is(err, ErrBrainProjectionCheckpointUnavailable) || !status.Stale || status.Reason != BrainProjectionStaleCheckpointUnavailable {
		t.Fatalf("resolver outage status=%+v err=%v", status, err)
	}
}

func TestBrainProjectionPostgresUnavailableHoldsCheckpoint(t *testing.T) {
	source := BrainProjectionSourceState{HighWater: 55, ManifestSHA256: testBrainProjectionManifest("source-55")}
	ctx, canonical, _, resolver := testBrainProjectionStore(t, source)
	sink := newTestBrainProjectionSink()
	unavailable := NewPostgresBrainProjectionCheckpointStore(nil, resolver)
	publisher := newBrainProjectionCheckpointPublisher(unavailable, sink)
	output := testBrainProjectionOutput(t, testBrainProjectionKey("transcript"), 0, BrainProjectionRebuildFenceToken{}, source, 12, "durable")
	result, err := publisher.Publish(ctx, output)
	if !errors.Is(err, ErrBrainProjectionCheckpointUnavailable) || !result.Status.Stale || sink.appendCount() != 1 {
		t.Fatalf("unavailable result=%+v appends=%d err=%v", result, sink.appendCount(), err)
	}
	status, statusErr := unavailable.Status(ctx, output.Key)
	if !errors.Is(statusErr, ErrBrainProjectionCheckpointUnavailable) || !status.Stale || status.Reason != BrainProjectionStaleCheckpointUnavailable {
		t.Fatalf("unavailable status=%+v err=%v", status, statusErr)
	}
	var count int
	if err := canonical.pool.QueryRow(ctx, "SELECT count(*) FROM brain_projection_checkpoints").Scan(&count); err != nil || count != 0 {
		t.Fatalf("checkpoint advanced while unavailable: %d err=%v", count, err)
	}
}

func TestBrainProjectionCompetingPublishAndPublishVersusRebuildRaces(t *testing.T) {
	source := BrainProjectionSourceState{HighWater: 88, ManifestSHA256: testBrainProjectionManifest("source-88")}
	ctx, _, store, resolver := testBrainProjectionStore(t, source)
	key := testBrainProjectionKey("transcript")
	sink := newTestBrainProjectionSink()
	publisher := newBrainProjectionCheckpointPublisher(store, sink)
	left := testBrainProjectionOutput(t, key, 0, BrainProjectionRebuildFenceToken{}, source, 14, "left")
	right := testBrainProjectionOutput(t, key, 0, BrainProjectionRebuildFenceToken{}, source, 14, "right")
	errs := make(chan error, 2)
	var fresh atomic.Int64
	for _, output := range []BrainProjectionDerivedOutput{left, right} {
		go func(output BrainProjectionDerivedOutput) {
			result, err := publisher.Publish(ctx, output)
			if err == nil && !result.Existing {
				fresh.Add(1)
			}
			errs <- err
		}(output)
	}
	var conflicts int
	for range 2 {
		if err := <-errs; errors.Is(err, ErrBrainProjectionConflict) {
			conflicts++
		} else if err != nil {
			t.Fatalf("competing publish error=%v", err)
		}
	}
	if fresh.Load() != 1 || conflicts != 1 {
		t.Fatalf("fresh=%d conflicts=%d, want 1/1", fresh.Load(), conflicts)
	}
	status, err := store.Status(ctx, key)
	if err != nil || status.Stale {
		t.Fatalf("published status=%+v err=%v", status, err)
	}

	next := resolver.set(89, "source-89")
	incremental := testBrainProjectionOutput(t, key, 0, status.Checkpoint.PublishedRebuildFenceToken, next, 15, "incremental")
	rebuildRequest := BrainProjectionRebuildRequest{Key: key, ExpectedGeneration: 0, StartSourceHighWater: 0, EndSourceHighWater: 89}
	publicationDone := make(chan error, 1)
	rebuildDone := make(chan error, 1)
	go func() { _, err := publisher.Publish(ctx, incremental); publicationDone <- err }()
	go func() { _, err := store.BeginRebuild(ctx, rebuildRequest); rebuildDone <- err }()
	publicationErr, rebuildErr := <-publicationDone, <-rebuildDone
	if rebuildErr != nil || (publicationErr != nil && !errors.Is(publicationErr, ErrBrainProjectionFenceLost)) {
		t.Fatalf("publish-vs-rebuild publication=%v rebuild=%v", publicationErr, rebuildErr)
	}
	status, err = store.Status(ctx, key)
	if err != nil || !status.Stale || !status.Checkpoint.RebuildActive || status.Checkpoint.RebuildGeneration != 1 {
		t.Fatalf("race did not leave fenced rebuild: status=%+v err=%v", status, err)
	}
}

func TestBrainProjectionAmbiguousCommitReplaysIdempotently(t *testing.T) {
	source := BrainProjectionSourceState{HighWater: 101, ManifestSHA256: testBrainProjectionManifest("source-101")}
	ctx, _, store, _ := testBrainProjectionStore(t, source)
	key := testBrainProjectionKey("transcript")
	sink := newTestBrainProjectionSink()
	publisher := newBrainProjectionCheckpointPublisher(store, sink)
	output := testBrainProjectionOutput(t, key, 0, BrainProjectionRebuildFenceToken{}, source, 20, "ambiguous")
	var failed atomic.Bool
	store.failpoint = func(point string) error {
		if point == "after_checkpoint_commit" && failed.CompareAndSwap(false, true) {
			return errors.New("connection lost after commit")
		}
		return nil
	}
	if _, err := publisher.Publish(ctx, output); err == nil {
		t.Fatal("ambiguous commit simulation returned success")
	}
	replay, err := publisher.Publish(ctx, output)
	if err != nil || !replay.Existing || sink.appendCount() != 1 {
		t.Fatalf("ambiguous replay=%+v appends=%d err=%v", replay, sink.appendCount(), err)
	}
}
