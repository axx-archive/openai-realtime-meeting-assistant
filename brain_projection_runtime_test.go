package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func appendProjectionCanonicalEvent(t *testing.T, ctx context.Context, store *PostgresCanonicalStore, registry *CanonicalPayloadRegistry, key BrainProjectionCheckpointKey, objectID string, version int64, body string) CanonicalEvent {
	t.Helper()
	contentDigest := sha256.Sum256([]byte(body))
	payload, payloadDigest, err := NewCanonicalEventPayload(registry, "artifact.revised", 1, map[string]any{
		"artifact_id": objectID, "content_revision": version, "content_sha256": hex.EncodeToString(contentDigest[:]), "visibility": "organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	event := CanonicalEvent{
		EventID: uuid.New(), TenantID: key.TenantID, AggregateType: key.SourceFamily, AggregateID: objectID,
		AggregateVersion: version, EventType: "artifact.revised", SchemaVersion: 1,
		OccurredAt: time.Date(2026, 7, 22, 12, int(version), 0, 0, time.UTC), RecordedAt: time.Date(2026, 7, 22, 13, int(version), 0, 0, time.UTC),
		Actor: CanonicalPrincipalRef{Kind: "service", ID: "projection-fixture"}, RoomID: key.RoomID, MeetingID: key.SittingID,
		IdempotencyKey: key.TenantID + ":" + key.SourceFamily + ":" + key.RoomID + ":" + key.SittingID + ":" + objectID + ":" + string(rune('0'+version)),
		Classification: "internal", ACLVersion: 1, Payload: payload, PayloadSHA256: payloadDigest,
	}
	if _, err := store.Append(ctx, event); err != nil {
		t.Fatal(err)
	}
	return event
}

func resolveProjectionManifestForTest(t *testing.T, ctx context.Context, store *PostgresCanonicalStore, resolver *postgresBrainProjectionSourceResolver, key BrainProjectionCheckpointKey) (BrainProjectionSourceManifest, BrainProjectionSourceState) {
	t.Helper()
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	manifest, state, err := resolver.ResolveBrainProjectionSourceManifest(ctx, tx, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return manifest, state
}

func assertProjectionReplayUnique(t *testing.T, replay BrainProjectionSanitizedReplay) {
	t.Helper()
	seenClaims, seenSources := map[string]bool{}, map[string]bool{}
	for _, claim := range replay.Claims {
		if seenClaims[claim.ClaimID] || seenSources[claim.SourceObjectID] {
			t.Fatalf("duplicate replay claim: %+v", replay.Claims)
		}
		seenClaims[claim.ClaimID], seenSources[claim.SourceObjectID] = true, true
	}
}

func TestBrainProjectionProductionResolverReplayCrashSupersessionPurgeAndRebuild(t *testing.T) {
	ctx, canonical, registry := migratedPostgresCanonicalStore(t)
	key := BrainProjectionCheckpointKey{TenantID: "tenant-projection", ProjectorVersion: "company-brain/v2", RoomID: "office", SittingID: "sitting-42", SourceFamily: "memory"}
	resolver := &postgresBrainProjectionSourceResolver{}
	first := appendProjectionCanonicalEvent(t, ctx, canonical, registry, key, "memory-a", 1, "sanitized body a v1")
	appendProjectionCanonicalEvent(t, ctx, canonical, registry, key, "memory-b", 1, "sanitized body b v1")
	decoyRoom := key
	decoyRoom.RoomID = "other-room"
	appendProjectionCanonicalEvent(t, ctx, canonical, registry, decoyRoom, "wrong-room", 1, "not in scope")
	decoyFamily := key
	decoyFamily.SourceFamily = "artifact"
	appendProjectionCanonicalEvent(t, ctx, canonical, registry, decoyFamily, "wrong-family", 1, "not in scope")

	manifest1, state1 := resolveProjectionManifestForTest(t, ctx, canonical, resolver, key)
	manifest1Replay, state1Replay := resolveProjectionManifestForTest(t, ctx, canonical, resolver, key)
	if state1.HighWater != 2 || state1 != state1Replay || !reflect.DeepEqual(manifest1, manifest1Replay) || len(manifest1.Events) != 2 || len(manifest1.Objects) != 2 {
		t.Fatalf("exact deterministic source manifest state=%+v replay=%+v manifest=%+v", state1, state1Replay, manifest1)
	}
	otherProjector := key
	otherProjector.ProjectorVersion = "company-brain/v3"
	_, otherState := resolveProjectionManifestForTest(t, ctx, canonical, resolver, otherProjector)
	if otherState.HighWater != state1.HighWater || otherState.ManifestSHA256 == state1.ManifestSHA256 {
		t.Fatalf("projector key was not bound: v2=%x v3=%x", state1.ManifestSHA256, otherState.ManifestSHA256)
	}
	replay1, body1, derived1, err := BuildBrainProjectionSanitizedReplay(manifest1)
	if err != nil || len(replay1.Claims) != 2 || derived1 != state1.HighWater {
		t.Fatalf("initial replay=%+v derived=%d err=%v", replay1, derived1, err)
	}
	assertProjectionReplayUnique(t, replay1)
	replayAgain, bodyAgain, derivedAgain, err := BuildBrainProjectionSanitizedReplay(manifest1Replay)
	if err != nil || !reflect.DeepEqual(replay1, replayAgain) || !bytes.Equal(body1, bodyAgain) || derived1 != derivedAgain {
		t.Fatalf("replay twice diverged replay=%+v/%+v derived=%d/%d err=%v", replay1, replayAgain, derived1, derivedAgain, err)
	}

	sinkRoot := filepath.Join(t.TempDir(), "derived")
	sink, err := NewFileBrainProjectionDerivedSink(sinkRoot)
	if err != nil {
		t.Fatal(err)
	}
	sink.now = func() time.Time { return time.Date(2026, 7, 22, 14, 0, 0, 0, time.UTC) }
	checkpointStore := NewBrainProjectionCheckpointStore(canonical, resolver)
	publisher := newBrainProjectionCheckpointPublisher(checkpointStore, sink)
	output1 := testBrainProjectionOutput(t, key, 0, BrainProjectionRebuildFenceToken{}, state1, derived1, string(body1))
	publisher.failpoint = func(point string) error {
		if point == "after_derived_before_checkpoint" {
			return errors.New("simulated process crash")
		}
		return nil
	}
	if _, err := publisher.Publish(ctx, output1); err == nil {
		t.Fatal("expected crash after durable derived append")
	}
	status, err := checkpointStore.Status(ctx, key)
	if err != nil || status.Reason != BrainProjectionStaleCheckpointAbsent {
		t.Fatalf("crash checkpoint status=%+v err=%v", status, err)
	}

	// A fresh sink and publisher have no process memory. They must recover the
	// exact receipt and publish once, then replay without a duplicate claim/file.
	restartedSink, err := NewFileBrainProjectionDerivedSink(sinkRoot)
	if err != nil {
		t.Fatal(err)
	}
	restarted := newBrainProjectionCheckpointPublisher(NewBrainProjectionCheckpointStore(canonical, resolver), restartedSink)
	result1, err := restarted.Publish(ctx, output1)
	if err != nil || result1.Existing {
		t.Fatalf("restart publication=%+v err=%v", result1, err)
	}
	replayedResult, err := restarted.Publish(ctx, output1)
	if err != nil || !replayedResult.Existing || replayedResult.DerivedID != result1.DerivedID {
		t.Fatalf("idempotent replay=%+v err=%v", replayedResult, err)
	}
	files, err := filepath.Glob(filepath.Join(sinkRoot, "*", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("derived files=%v err=%v", files, err)
	}

	appendProjectionCanonicalEvent(t, ctx, canonical, registry, key, "memory-a", 2, "sanitized body a v2")
	manifest2, state2 := resolveProjectionManifestForTest(t, ctx, canonical, resolver, key)
	replay2, body2, derived2, err := BuildBrainProjectionSanitizedReplay(manifest2)
	if err != nil || state2.HighWater <= state1.HighWater || len(replay2.Claims) != 2 || derived2 <= derived1 {
		t.Fatalf("supersession state=%+v replay=%+v derived=%d err=%v", state2, replay2, derived2, err)
	}
	assertProjectionReplayUnique(t, replay2)
	for _, claim := range replay2.Claims {
		if claim.SourceObjectID == "memory-a" && claim.SourceRevision != 2 {
			t.Fatalf("superseded revision survived replay: %+v", claim)
		}
	}
	output2 := testBrainProjectionOutput(t, key, 0, BrainProjectionRebuildFenceToken{}, state2, derived2, string(body2))
	if output2.DerivedID == output1.DerivedID {
		t.Fatal("supersession reused derived identity")
	}
	if _, err := restarted.Publish(ctx, output2); err != nil {
		t.Fatal(err)
	}

	var bodyBDigest []byte
	if err := canonical.pool.QueryRow(ctx, `SELECT content_sha256 FROM objects WHERE tenant_id=$1 AND object_type=$2 AND object_id='memory-b'`, key.TenantID, key.SourceFamily).Scan(&bodyBDigest); err != nil {
		t.Fatal(err)
	}
	if _, err := canonical.pool.Exec(ctx, `INSERT INTO purge_ledger (
		tenant_id,object_type,object_id,revision_id,content_sha256,policy_id,purged_at,destruction_evidence
	) VALUES ($1,$2,'memory-b','1',$3,'projection-test',now(),'{"blob":"destroyed"}'::jsonb)`, key.TenantID, key.SourceFamily, bodyBDigest); err != nil {
		t.Fatal(err)
	}
	manifest3, state3 := resolveProjectionManifestForTest(t, ctx, canonical, resolver, key)
	replay3, body3, derived3, err := BuildBrainProjectionSanitizedReplay(manifest3)
	if err != nil || state3.HighWater != state2.HighWater || state3.ManifestSHA256 == state2.ManifestSHA256 || len(replay3.Claims) != 1 || replay3.Claims[0].SourceObjectID != "memory-a" || derived3 <= derived2 {
		t.Fatalf("purge state=%+v replay=%+v derived=%d err=%v", state3, replay3, derived3, err)
	}
	assertProjectionReplayUnique(t, replay3)
	output3 := testBrainProjectionOutput(t, key, 0, BrainProjectionRebuildFenceToken{}, state3, derived3, string(body3))
	if _, err := restarted.Publish(ctx, output3); err != nil {
		t.Fatal(err)
	}

	fence, err := beginTestBrainProjectionRebuild(ctx, checkpointStore, BrainProjectionRebuildRequest{Key: key, ExpectedGeneration: 0, StartSourceHighWater: 0, EndSourceHighWater: state3.HighWater})
	if err != nil {
		t.Fatal(err)
	}
	rebuildOutput := testBrainProjectionOutput(t, key, fence.Generation, fence.Token, state3, derived3, string(body3))
	rebuildOutputAgain := testBrainProjectionOutput(t, key, fence.Generation, fence.Token, state3, derived3, string(body3))
	if rebuildOutput.DerivedID != rebuildOutputAgain.DerivedID || rebuildOutput.DerivedSHA256 != rebuildOutputAgain.DerivedSHA256 {
		t.Fatal("rebuild output identity was not deterministic")
	}
	rebuilt, err := restarted.Publish(ctx, rebuildOutput)
	if err != nil || rebuilt.Checkpoint.PublishedGeneration != 1 || rebuilt.Checkpoint.SourceManifestSHA256 != state3.ManifestSHA256 {
		t.Fatalf("rebuild result=%+v err=%v", rebuilt, err)
	}

	// Manifest entries prove only digests/identity. The fixture body's literal
	// bytes are absent from both the manifest and durable replay output.
	manifestRaw, _ := canonicalJSON(manifest3)
	if bytes.Contains(manifestRaw, []byte("sanitized body")) || bytes.Contains(body3, []byte("sanitized body")) || first.Payload == nil {
		t.Fatal("source body leaked into sanitized projection artifacts")
	}
}

func TestBrainProjectionPublicationSharesCanonicalSourceFence(t *testing.T) {
	ctx, canonical, registry := migratedPostgresCanonicalStore(t)
	key := BrainProjectionCheckpointKey{TenantID: "tenant-source-fence", ProjectorVersion: brainProjectionProjectorVersion, RoomID: officeRoomID, SittingID: "sitting-source-fence", SourceFamily: "memory"}
	appendProjectionCanonicalEvent(t, ctx, canonical, registry, key, "memory-a", 1, "source v1")
	resolver := &postgresBrainProjectionSourceResolver{}
	manifest, source := resolveProjectionManifestForTest(t, ctx, canonical, resolver, key)
	_, body, derived, err := BuildBrainProjectionSanitizedReplay(manifest)
	if err != nil {
		t.Fatal(err)
	}
	output := testBrainProjectionOutput(t, key, 0, BrainProjectionRebuildFenceToken{}, source, derived, string(body))
	sink, err := NewFileBrainProjectionDerivedSink(filepath.Join(t.TempDir(), "derived"))
	if err != nil {
		t.Fatal(err)
	}
	checkpointStore := NewBrainProjectionCheckpointStore(canonical, resolver)
	publisher := newBrainProjectionCheckpointPublisher(checkpointStore, sink)

	appendEntered, releaseAppend := make(chan struct{}), make(chan struct{})
	canonical.Failpoint = func(point string) error {
		if point == "after_event_before_projection" {
			close(appendEntered)
			<-releaseAppend
		}
		return nil
	}
	appendDone := make(chan error, 1)
	go func() {
		contentDigest := sha256.Sum256([]byte("source v2"))
		payload, payloadDigest, payloadErr := NewCanonicalEventPayload(registry, "artifact.revised", 1, map[string]any{
			"artifact_id": "memory-a", "content_revision": int64(2), "content_sha256": hex.EncodeToString(contentDigest[:]), "visibility": "organization",
		})
		if payloadErr != nil {
			appendDone <- payloadErr
			return
		}
		event := CanonicalEvent{
			EventID: uuid.New(), TenantID: key.TenantID, AggregateType: key.SourceFamily, AggregateID: "memory-a", AggregateVersion: 2,
			EventType: "artifact.revised", SchemaVersion: 1, OccurredAt: time.Now().UTC(), RecordedAt: time.Now().UTC(),
			Actor: CanonicalPrincipalRef{Kind: "service", ID: "source-fence-test"}, RoomID: key.RoomID, MeetingID: key.SittingID,
			IdempotencyKey: "source-fence-v2", Classification: "internal", ACLVersion: 1, Payload: payload, PayloadSHA256: payloadDigest,
		}
		_, appendErr := canonical.Append(ctx, event)
		appendDone <- appendErr
	}()
	select {
	case <-appendEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("canonical append did not enter the shared source fence")
	}
	publishDone := make(chan error, 1)
	go func() {
		_, publishErr := publisher.Publish(ctx, output)
		publishDone <- publishErr
	}()
	select {
	case err := <-publishDone:
		t.Fatalf("publication crossed active canonical append: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseAppend)
	if err := <-appendDone; err != nil {
		t.Fatal(err)
	}
	if err := <-publishDone; !errors.Is(err, ErrBrainProjectionSourceMoved) {
		t.Fatalf("publication err=%v, want moved source", err)
	}
	status, err := checkpointStore.Status(ctx, key)
	if err != nil || status.Reason != BrainProjectionStaleCheckpointAbsent {
		t.Fatalf("stale output mutated checkpoint: status=%+v err=%v", status, err)
	}
}

func TestBrainProjectionPurgeIdentityIsFamilyQualified(t *testing.T) {
	ctx, canonical, registry := migratedPostgresCanonicalStore(t)
	memoryKey := BrainProjectionCheckpointKey{TenantID: "tenant-family-purge", ProjectorVersion: brainProjectionProjectorVersion, RoomID: officeRoomID, SittingID: "sitting-family-purge", SourceFamily: "memory"}
	artifactKey := memoryKey
	artifactKey.SourceFamily = "artifact"
	appendProjectionCanonicalEvent(t, ctx, canonical, registry, memoryKey, "shared-object", 1, "memory body")
	appendProjectionCanonicalEvent(t, ctx, canonical, registry, artifactKey, "shared-object", 1, "artifact body")
	var memoryDigest []byte
	if err := canonical.pool.QueryRow(ctx, `SELECT content_sha256 FROM objects WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3`,
		memoryKey.TenantID, memoryKey.SourceFamily, "shared-object").Scan(&memoryDigest); err != nil {
		t.Fatal(err)
	}
	if _, err := canonical.pool.Exec(ctx, `INSERT INTO purge_ledger (
		tenant_id,object_type,object_id,revision_id,content_sha256,policy_id,purged_at,destruction_evidence
	) VALUES ($1,$2,$3,'1',$4,'family-test',now(),'{"proof":"destroyed"}'::jsonb)`,
		memoryKey.TenantID, memoryKey.SourceFamily, "shared-object", memoryDigest); err != nil {
		t.Fatal(err)
	}
	resolver := &postgresBrainProjectionSourceResolver{}
	memoryManifest, _ := resolveProjectionManifestForTest(t, ctx, canonical, resolver, memoryKey)
	artifactManifest, _ := resolveProjectionManifestForTest(t, ctx, canonical, resolver, artifactKey)
	memoryReplay, _, _, err := BuildBrainProjectionSanitizedReplay(memoryManifest)
	if err != nil {
		t.Fatal(err)
	}
	artifactReplay, _, _, err := BuildBrainProjectionSanitizedReplay(artifactManifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(memoryReplay.Claims) != 0 || len(artifactReplay.Claims) != 1 || artifactReplay.Claims[0].SourceObjectID != "shared-object" {
		t.Fatalf("cross-family purge leaked: memory=%+v artifact=%+v", memoryReplay, artifactReplay)
	}
}

func TestFileBrainProjectionDerivedSinkReceiptSurvivesRestartAndDetectsTamper(t *testing.T) {
	key := testBrainProjectionKey("memory")
	state := BrainProjectionSourceState{HighWater: 7, ManifestSHA256: sha256.Sum256([]byte("manifest"))}
	output := testBrainProjectionOutput(t, key, 0, BrainProjectionRebuildFenceToken{}, state, 7, `{"claims":[]}`)
	root := filepath.Join(t.TempDir(), "derived")
	first, err := NewFileBrainProjectionDerivedSink(root)
	if err != nil {
		t.Fatal(err)
	}
	first.now = func() time.Time { return time.Date(2026, 7, 22, 15, 0, 0, 0, time.UTC) }
	receipt, err := first.PutBrainProjectionDerived(context.Background(), output)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewFileBrainProjectionDerivedSink(root)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := restarted.PutBrainProjectionDerived(context.Background(), output)
	if err != nil || recovered != receipt {
		t.Fatalf("recovered receipt=%+v want=%+v err=%v", recovered, receipt, err)
	}
	path, err := restarted.outputPath(output)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var envelope fileBrainProjectionDerivedEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Body[0] ^= 0xff
	tampered, _ := json.Marshal(envelope)
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restarted.VerifyBrainProjectionDerived(context.Background(), output, receipt); !errors.Is(err, ErrBrainProjectionDerivedNotDurable) {
		t.Fatalf("tampered receipt verification err=%v", err)
	}
}

func TestFileBrainProjectionDerivedSinkRejectsActualOversizeEnvelope(t *testing.T) {
	key := testBrainProjectionKey("memory")
	state := BrainProjectionSourceState{HighWater: 7, ManifestSHA256: sha256.Sum256([]byte("manifest"))}
	root := filepath.Join(t.TempDir(), "derived")
	sink, err := NewFileBrainProjectionDerivedSink(root)
	if err != nil {
		t.Fatal(err)
	}

	output := testBrainProjectionOutput(t, key, 0, BrainProjectionRebuildFenceToken{}, state, 7, `{"claims":[]}`)
	receipt, err := sink.PutBrainProjectionDerived(context.Background(), output)
	if err != nil {
		t.Fatal(err)
	}
	path, err := sink.outputPath(output)
	if err != nil {
		t.Fatal(err)
	}
	// Sparse growth preserves the valid JSON prefix and reproduces the case a
	// LimitReader-only implementation incorrectly accepted at artificial EOF.
	if err := os.Truncate(path, brainProjectionMaxFileBytes+1); err != nil {
		t.Fatal(err)
	}
	if err := sink.VerifyBrainProjectionDerived(context.Background(), output, receipt); !errors.Is(err, ErrBrainProjectionDerivedNotDurable) {
		t.Fatalf("oversize on-disk envelope verification err=%v", err)
	}

	oversizeBody := make([]byte, brainProjectionMaxFileBytes*3/4+1)
	oversize, err := NewBrainProjectionDerivedOutput(key, 0, BrainProjectionRebuildFenceToken{}, state, 8, oversizeBody)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sink.PutBrainProjectionDerived(context.Background(), oversize); !errors.Is(err, ErrBrainProjectionDerivedNotDurable) {
		t.Fatalf("oversize envelope write err=%v", err)
	}
	oversizePath, err := sink.outputPath(oversize)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oversizePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversize envelope became visible: %v", err)
	}
}

func TestBrainProjectionProductionRuntimeIsDefaultOffShadowAndNeverBaselines(t *testing.T) {
	prior := currentProductionBrainProjectionRuntime()
	t.Cleanup(func() {
		current := currentProductionBrainProjectionRuntime()
		brainProjectionRuntimeState.Lock()
		brainProjectionRuntimeState.runtime = prior
		brainProjectionRuntimeState.Unlock()
		if current != nil && current != prior {
			current.stop()
		}
	})
	t.Setenv(brainProjectionRuntimeModeEnv, "")
	status := configureProductionBrainProjectionRuntime(nil)
	if status.Mode != brainProjectionRuntimeOff || status.Enabled || !status.Ready || status.AutomaticBaseline {
		t.Fatalf("default status=%+v", status)
	}

	ctx, canonical, registry := migratedPostgresCanonicalStore(t)
	historicalKey := BrainProjectionCheckpointKey{TenantID: "historical-tenant", ProjectorVersion: brainProjectionProjectorVersion, RoomID: officeRoomID, SittingID: "historical-sitting", SourceFamily: "memory"}
	appendProjectionCanonicalEvent(t, ctx, canonical, registry, historicalKey, "historical-memory", 1, "pre-enable history")
	t.Setenv(brainProjectionRuntimeModeEnv, brainProjectionRuntimeShadow)
	status = configureProductionBrainProjectionRuntime(&CanonicalRuntime{mode: CanonicalModeShadow, dataDir: t.TempDir(), postgres: canonical})
	production := currentProductionBrainProjectionRuntime()
	if !status.Enabled || !status.Ready || !status.Database || !status.DurableSink || !status.WorkerRunning || status.AutomaticBaseline || production == nil || production.publisher == nil {
		t.Fatalf("shadow status=%+v runtime=%+v", status, production)
	}
	var checkpoints int
	if err := canonical.pool.QueryRow(ctx, "SELECT count(*) FROM brain_projection_checkpoints").Scan(&checkpoints); err != nil || checkpoints != 0 {
		t.Fatalf("shadow startup baselined history: checkpoints=%d err=%v", checkpoints, err)
	}
}

func TestBrainProjectionProductionScopeTracksReconciledMemoryWithoutAutomaticBaseline(t *testing.T) {
	dir := canonicalRuntimeTestEnv(t, "shadow")
	ctx, pool := startDisposableCanonicalPostgres(t)
	registry, err := NewCanonicalImportPayloadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	canonical := NewPostgresCanonicalStore(pool, registry)
	if err := canonical.ApplyMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "canonical")
	spool, err := OpenCanonicalCaptureSpool(filepath.Join(root, "mutation-spool.bcs"), CanonicalModeShadow)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := OpenFileCanonicalObjectVersionMap(filepath.Join(root, "object-versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	runtime := &CanonicalRuntime{
		mode: CanonicalModeShadow, dataDir: dir, root: root, tenantID: canonicalTenantID(), registry: registry,
		spool: spool, versions: versions, postgres: canonical, events: canonical, lastOK: time.Now().UTC(),
	}
	priorCanonical := currentCanonicalRuntime()
	setCanonicalRuntime(runtime)
	priorProjection := currentProductionBrainProjectionRuntime()
	t.Cleanup(func() {
		current := currentProductionBrainProjectionRuntime()
		brainProjectionRuntimeState.Lock()
		brainProjectionRuntimeState.runtime = priorProjection
		brainProjectionRuntimeState.Unlock()
		if current != nil && current != priorProjection {
			current.stop()
		}
		setCanonicalRuntime(priorCanonical)
		// Force the lazily cached authority to bind to the restored runtime on
		// its next use; otherwise this test can leave later Realtime/Scout tests
		// holding a consent store backed by the disposable PostgreSQL instance.
		consentAuthorityRuntime.Lock()
		consentAuthorityRuntime.authority = nil
		consentAuthorityRuntime.canonical = nil
		consentAuthorityRuntime.policy = ""
		consentAuthorityRuntime.Unlock()
	})
	t.Setenv(brainProjectionRuntimeModeEnv, brainProjectionRuntimeShadow)
	if status := configureProductionBrainProjectionRuntime(runtime); !status.Ready || status.AutomaticBaseline {
		t.Fatalf("production projection runtime status=%+v", status)
	}

	key := BrainProjectionCheckpointKey{
		TenantID: runtime.tenantID, ProjectorVersion: "company-brain/v2", RoomID: officeRoomID,
		SittingID: "production-sitting", SourceFamily: "memory",
	}
	resolver := &postgresBrainProjectionSourceResolver{}
	empty, emptyState := resolveProjectionManifestForTest(t, ctx, canonical, resolver, key)
	if emptyState.HighWater != 0 || len(empty.Events) != 0 || len(empty.Objects) != 0 {
		t.Fatalf("fresh canonical scope was not empty: state=%+v manifest=%+v", emptyState, empty)
	}
	memory, err := newMeetingMemoryStore(meetingMemoryPath())
	if err != nil {
		t.Fatal(err)
	}
	appendMemory := func(roomID, id string) {
		t.Helper()
		if _, appended, appendErr := memory.appendEntryForMeeting(roomID, meetingMemoryKindNote, id, "production memory "+id,
			map[string]string{"roomId": roomID, "meetingId": key.SittingID}, ""); appendErr != nil || !appended {
			t.Fatalf("append production memory %s appended=%t err=%v", id, appended, appendErr)
		}
	}
	appendMemory(officeRoomID, "production-memory-1")
	// The projection is canonical-only: a durable legacy mutation is visible
	// after reconciliation, never by an implicit checkpoint/baseline scan.
	beforeReconcile, beforeState := resolveProjectionManifestForTest(t, ctx, canonical, resolver, key)
	if beforeState.HighWater != 0 || len(beforeReconcile.Objects) != 0 {
		t.Fatalf("unreconciled legacy bytes entered projection scope: state=%+v manifest=%+v", beforeState, beforeReconcile)
	}
	if err := runtime.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	first, firstState := resolveProjectionManifestForTest(t, ctx, canonical, resolver, key)
	if firstState.HighWater <= 0 || len(first.Events) != 1 || len(first.Objects) != 1 || first.Objects[0].ObjectID != "production-memory-1" {
		t.Fatalf("first reconciled production memory missing: state=%+v manifest=%+v", firstState, first)
	}

	appendMemory("other-room", "production-memory-decoy")
	if err := runtime.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	decoy, decoyState := resolveProjectionManifestForTest(t, ctx, canonical, resolver, key)
	if decoyState != firstState || !reflect.DeepEqual(decoy, first) {
		t.Fatalf("cross-room production memory polluted exact scope: first=%+v decoy=%+v", first, decoy)
	}

	appendMemory(officeRoomID, "production-memory-2")
	if err := runtime.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	second, secondState := resolveProjectionManifestForTest(t, ctx, canonical, resolver, key)
	if secondState.HighWater <= firstState.HighWater || secondState.ManifestSHA256 == firstState.ManifestSHA256 || len(second.Events) != 2 || len(second.Objects) != 2 {
		t.Fatalf("second production memory did not advance exact scope: first=%+v second=%+v manifest=%+v", firstState, secondState, second)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, statusErr := currentProductionBrainProjectionRuntime().checkpoints.Status(ctx, key)
		if statusErr == nil && !status.Stale && status.Checkpoint.SourceHighWater == secondState.HighWater {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("projection worker did not publish reconciled scope: status=%+v err=%v", status, statusErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var checkpoints int
	if err := canonical.pool.QueryRow(ctx, "SELECT count(*) FROM brain_projection_checkpoints").Scan(&checkpoints); err != nil || checkpoints != 2 {
		t.Fatalf("production worker checkpoints=%d err=%v, want exact office and decoy scopes", checkpoints, err)
	}
	var queued int
	if err := canonical.pool.QueryRow(ctx, "SELECT count(*) FROM brain_projection_work").Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("durable projection work was not acknowledged: queued=%d err=%v", queued, err)
	}
}

func newStoppedProjectionRuntimeForTest(t *testing.T, canonical *PostgresCanonicalStore) *productionBrainProjectionRuntime {
	t.Helper()
	resolver := &postgresBrainProjectionSourceResolver{}
	sink, err := NewFileBrainProjectionDerivedSink(filepath.Join(t.TempDir(), "derived"))
	if err != nil {
		t.Fatal(err)
	}
	checkpoints := NewBrainProjectionCheckpointStore(canonical, resolver)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &productionBrainProjectionRuntime{
		status: BrainProjectionRuntimeStatus{
			Mode: brainProjectionRuntimeShadow, Enabled: true, Database: true, DurableSink: true, WorkerRunning: true,
		},
		canonicalPool: canonical.pool, resolver: resolver, sink: sink, checkpoints: checkpoints,
		publisher: newBrainProjectionCheckpointPublisher(checkpoints, sink), ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1),
	}
}

func insertProjectionWorkForTest(t *testing.T, ctx context.Context, canonical *PostgresCanonicalStore, key BrainProjectionCheckpointKey, requestedAgo, availableIn time.Duration, lastError string) {
	t.Helper()
	if _, err := canonical.pool.Exec(ctx, `INSERT INTO brain_projection_work (
		tenant_id,projector_version,room_id,sitting_id,source_family,first_requested_at,requested_at,available_at,last_error,failure_since
	) VALUES ($1,$2,$3,$4,$5,now()-$6::interval,now()-$6::interval,now()+$7::interval,$8,
		CASE WHEN $8='' THEN NULL ELSE now()-$6::interval END)`, key.TenantID, key.ProjectorVersion,
		key.RoomID, key.SittingID, key.SourceFamily, requestedAgo.String(), availableIn.String(), lastError); err != nil {
		t.Fatal(err)
	}
}

func TestBrainProjectionDurableQueueStatusSurvivesRestartAndMixedSuccess(t *testing.T) {
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	failedKey := BrainProjectionCheckpointKey{TenantID: "queue-tenant", ProjectorVersion: brainProjectionProjectorVersion, RoomID: "office", SittingID: "failed", SourceFamily: "memory"}
	successKey := failedKey
	successKey.SittingID = "success"
	insertProjectionWorkForTest(t, ctx, canonical, failedKey, 2*time.Minute, time.Hour, "durable projector failure")
	insertProjectionWorkForTest(t, ctx, canonical, successKey, time.Minute, time.Hour, "")

	restarted := newStoppedProjectionRuntimeForTest(t, canonical)
	restarted.refreshDurableStatus(ctx)
	status := restarted.snapshot()
	if !status.QueueKnown || status.PendingScopes != 2 || status.FailedScopes != 1 || status.BackoffScopes != 1 ||
		status.OldestPendingSecs < 119 || status.Ready || status.Error != "durable projector failure" {
		t.Fatalf("restart queue status=%+v", status)
	}

	// Completing an unrelated healthy scope must not erase the durable failed
	// row or make the process ready.
	if _, err := canonical.pool.Exec(ctx, `UPDATE brain_projection_work SET available_at=now() WHERE sitting_id=$1`, successKey.SittingID); err != nil {
		t.Fatal(err)
	}
	successWork, ok := restarted.takePending(ctx)
	if !ok || successWork.key != successKey {
		t.Fatalf("healthy work lease=%+v ok=%t", successWork, ok)
	}
	if err := restarted.completeDurableScope(ctx, successWork); err != nil {
		t.Fatal(err)
	}
	restarted.refreshDurableStatus(ctx)
	status = restarted.snapshot()
	if status.PendingScopes != 1 || status.FailedScopes != 1 || status.Ready || status.Error == "" {
		t.Fatalf("unrelated success cleared durable failure: %+v", status)
	}
	var firstRequested, failureSince time.Time
	if err := canonical.pool.QueryRow(ctx, `SELECT first_requested_at,failure_since FROM brain_projection_work WHERE sitting_id=$1`, failedKey.SittingID).Scan(&firstRequested, &failureSince); err != nil {
		t.Fatal(err)
	}
	if err := restarted.scheduleExplicitRebuildWork(ctx, failedKey); err != nil {
		t.Fatal(err)
	}
	var firstAfter, failureAfter time.Time
	var failure string
	if err := canonical.pool.QueryRow(ctx, `SELECT first_requested_at,failure_since,last_error FROM brain_projection_work WHERE sitting_id=$1`, failedKey.SittingID).Scan(&firstAfter, &failureAfter, &failure); err != nil {
		t.Fatal(err)
	}
	if !firstAfter.Equal(firstRequested) || !failureAfter.Equal(failureSince) || failure == "" {
		t.Fatalf("new request masked durable age/failure first=%v/%v failed=%v/%v error=%q", firstRequested, firstAfter, failureSince, failureAfter, failure)
	}
	restarted.refreshDurableStatus(ctx)
	if status = restarted.snapshot(); status.Ready || status.FailedScopes != 1 || status.OldestPendingSecs < 119 {
		t.Fatalf("newer request masked readiness=%+v", status)
	}
	if _, err := canonical.pool.Exec(ctx, `UPDATE brain_projection_work SET available_at=now() WHERE sitting_id=$1`, failedKey.SittingID); err != nil {
		t.Fatal(err)
	}
	failedWork, ok := restarted.takePending(ctx)
	if !ok || failedWork.key != failedKey {
		t.Fatalf("failed work lease=%+v ok=%t", failedWork, ok)
	}
	if err := restarted.completeDurableScope(ctx, failedWork); err != nil {
		t.Fatal(err)
	}
	restarted.refreshDurableStatus(ctx)
	status = restarted.snapshot()
	if status.PendingScopes != 0 || status.FailedScopes != 0 || status.BackoffScopes != 0 || !status.Ready || status.Error != "" {
		t.Fatalf("drained queue status=%+v", status)
	}
}

func TestBrainProjectionLeaseAndRequestGenerationPreventLostWork(t *testing.T) {
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	runtime := newStoppedProjectionRuntimeForTest(t, canonical)
	key := BrainProjectionCheckpointKey{TenantID: "generation-tenant", ProjectorVersion: brainProjectionProjectorVersion, RoomID: "office", SittingID: "generation", SourceFamily: "memory"}
	insertProjectionWorkForTest(t, ctx, canonical, key, time.Second, -time.Second, "")
	claimed, ok := runtime.takePending(ctx)
	if !ok || claimed.key != key || claimed.generation != 1 {
		t.Fatalf("claimed=%+v ok=%t", claimed, ok)
	}
	var leased bool
	if err := canonical.pool.QueryRow(ctx, `SELECT available_at > now() FROM brain_projection_work
		WHERE tenant_id=$1 AND projector_version=$2 AND room_id=$3 AND sitting_id=$4 AND source_family=$5`, key.TenantID,
		key.ProjectorVersion, key.RoomID, key.SittingID, key.SourceFamily).Scan(&leased); err != nil || !leased {
		t.Fatalf("durable lease=%t err=%v", leased, err)
	}

	// A newer request arriving during the lease increments the durable
	// generation. The old worker's acknowledgement is then harmless.
	if err := runtime.scheduleExplicitRebuildWork(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := runtime.completeDurableScope(ctx, claimed); !errors.Is(err, ErrBrainProjectionLeaseLost) {
		t.Fatalf("stale lease acknowledgement err=%v", err)
	}
	var generation int64
	var available bool
	if err := canonical.pool.QueryRow(ctx, `SELECT request_generation,available_at <= now() FROM brain_projection_work
		WHERE tenant_id=$1 AND projector_version=$2 AND room_id=$3 AND sitting_id=$4 AND source_family=$5`, key.TenantID,
		key.ProjectorVersion, key.RoomID, key.SittingID, key.SourceFamily).Scan(&generation, &available); err != nil {
		t.Fatal(err)
	}
	if generation <= claimed.generation || !available {
		t.Fatalf("new durable request generation=%d available=%t", generation, available)
	}

	restarted := newStoppedProjectionRuntimeForTest(t, canonical)
	restarted.refreshDurableStatus(ctx)
	if status := restarted.snapshot(); !status.QueueKnown || status.PendingScopes != 1 || status.CaughtUp || status.Ready {
		t.Fatalf("leased/new work was lost across restart: %+v", status)
	}
}

func TestBrainProjectionHistoricalBackfillHTTPRequiresAuthenticatedAdmin(t *testing.T) {
	setupAuthTestEnv(t)
	ctx, canonical, registry := migratedPostgresCanonicalStore(t)
	key := BrainProjectionCheckpointKey{TenantID: "http-tenant", ProjectorVersion: brainProjectionProjectorVersion, RoomID: "office", SittingID: "http", SourceFamily: "memory"}
	appendProjectionCanonicalEvent(t, ctx, canonical, registry, key, "http-memory", 1, "http history")
	_, source := resolveProjectionManifestForTest(t, ctx, canonical, &postgresBrainProjectionSourceResolver{}, key)
	runtime := newStoppedProjectionRuntimeForTest(t, canonical)
	prior := currentProductionBrainProjectionRuntime()
	brainProjectionRuntimeState.Lock()
	brainProjectionRuntimeState.runtime = runtime
	brainProjectionRuntimeState.Unlock()
	t.Cleanup(func() {
		brainProjectionRuntimeState.Lock()
		brainProjectionRuntimeState.runtime = prior
		brainProjectionRuntimeState.Unlock()
	})
	payload := fmt.Sprintf(`{"requestId":"http-backfill","tenantId":%q,"projectorVersion":%q,"roomId":%q,"sittingId":%q,"sourceFamily":%q,"expectedGeneration":0,"startSourceHighWater":0,"endSourceHighWater":%d,"approvalReference":"operator-ticket-1","authorizationExpiresAt":%q}`,
		key.TenantID, key.ProjectorVersion, key.RoomID, key.SittingID, key.SourceFamily, source.HighWater, time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano))
	invoke := func(cookies []*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/brain-projection/backfill", strings.NewReader(payload))
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		brainProjectionHistoricalBackfillHandler(recorder, req)
		return recorder
	}
	if recorder := invoke(nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := invoke(loginAs(t, "tim@shareability.com", defaultMeetingRoomPassword)); recorder.Code != http.StatusForbidden {
		t.Fatalf("member status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := invoke(loginAs(t, artifactLibraryAdminEmail, defaultMeetingRoomPassword)); recorder.Code != http.StatusAccepted {
		t.Fatalf("admin status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	audit, found, err := runtime.loadBackfillAudit(ctx, "http-backfill")
	if err != nil || !found || !audit.HasFence || audit.AuthorizedBy != artifactLibraryAdminEmail {
		t.Fatalf("authenticated audit=%+v found=%t err=%v", audit, found, err)
	}
}

func TestBrainProjectionLeaseTokenAndGlobalGenerationRejectABA(t *testing.T) {
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	runtime := newStoppedProjectionRuntimeForTest(t, canonical)
	key := BrainProjectionCheckpointKey{TenantID: "aba-tenant", ProjectorVersion: brainProjectionProjectorVersion, RoomID: "office", SittingID: "aba", SourceFamily: "memory"}
	insertProjectionWorkForTest(t, ctx, canonical, key, time.Second, -time.Second, "")
	first, ok := runtime.takePending(ctx)
	if !ok {
		t.Fatal("first lease was not acquired")
	}
	if _, err := canonical.pool.Exec(ctx, `UPDATE brain_projection_work SET available_at=now(),lease_expires_at=now()
		WHERE sitting_id=$1`, key.SittingID); err != nil {
		t.Fatal(err)
	}
	second, ok := runtime.takePending(ctx)
	if !ok || second.generation != first.generation || second.leaseToken == first.leaseToken {
		t.Fatalf("replacement lease first=%+v second=%+v ok=%t", first, second, ok)
	}
	if err := runtime.completeDurableScope(ctx, first); !errors.Is(err, ErrBrainProjectionLeaseLost) {
		t.Fatalf("expired lease acknowledgement err=%v", err)
	}
	if err := runtime.completeDurableScope(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := runtime.scheduleExplicitRebuildWork(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := runtime.completeDurableScope(ctx, second); !errors.Is(err, ErrBrainProjectionLeaseLost) {
		t.Fatalf("ABA acknowledgement err=%v", err)
	}
	var generation int64
	if err := canonical.pool.QueryRow(ctx, `SELECT request_generation FROM brain_projection_work WHERE sitting_id=$1`, key.SittingID).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if generation <= second.generation {
		t.Fatalf("recreated generation=%d did not advance beyond stale generation=%d", generation, second.generation)
	}
}

func historicalBackfillRequestForTest(key BrainProjectionCheckpointKey, expected, start, end int64, requestID string) BrainProjectionHistoricalBackfillRequest {
	authorization := BrainProjectionHistoricalBackfillAuthorization{
		RequestID: requestID, Key: key, ExpectedGeneration: expected, StartSourceHighWater: start, EndSourceHighWater: end,
		AuthorizedBy: "operator@example.test", ApprovalReference: "approval-" + requestID, ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	return BrainProjectionHistoricalBackfillRequest{
		Rebuild:       BrainProjectionRebuildRequest{Key: key, ExpectedGeneration: expected, StartSourceHighWater: start, EndSourceHighWater: end},
		Authorization: authorization,
	}
}

func TestBrainProjectionHistoricalBackfillIsExactBoundedAuditedAndRestartSafe(t *testing.T) {
	ctx, canonical, registry := migratedPostgresCanonicalStore(t)
	key := BrainProjectionCheckpointKey{TenantID: "history-tenant", ProjectorVersion: brainProjectionProjectorVersion, RoomID: "office", SittingID: "historical", SourceFamily: "memory"}
	appendProjectionCanonicalEvent(t, ctx, canonical, registry, key, "memory-a", 1, "historical a")
	appendProjectionCanonicalEvent(t, ctx, canonical, registry, key, "memory-b", 1, "historical b")
	_, source := resolveProjectionManifestForTest(t, ctx, canonical, &postgresBrainProjectionSourceResolver{}, key)
	runtime := newStoppedProjectionRuntimeForTest(t, canonical)

	// Merely starting/recovering the runtime must not baseline pre-enable
	// history or enqueue its scope.
	if err := runtime.recoverExplicitRebuildWork(ctx); err != nil {
		t.Fatal(err)
	}
	var checkpoints, work int
	if err := canonical.pool.QueryRow(ctx, `SELECT count(*) FROM brain_projection_checkpoints`).Scan(&checkpoints); err != nil || checkpoints != 0 {
		t.Fatalf("historical startup checkpoint count=%d err=%v", checkpoints, err)
	}
	if err := canonical.pool.QueryRow(ctx, `SELECT count(*) FROM brain_projection_work`).Scan(&work); err != nil || work != 0 {
		t.Fatalf("historical startup work count=%d err=%v", work, err)
	}

	request := historicalBackfillRequestForTest(key, 0, 0, source.HighWater, "backfill-history-1")
	result, err := runtime.ScheduleHistoricalBackfill(ctx, request)
	if err != nil || !result.Scheduled || result.Completed || result.Fence.Generation != 1 {
		t.Fatalf("schedule result=%+v err=%v", result, err)
	}
	var audits int
	if err := canonical.pool.QueryRow(ctx, `SELECT count(*) FROM brain_projection_backfill_requests WHERE request_id=$1
		AND tenant_id=$2 AND start_source_high_water=0 AND end_source_high_water=$3 AND fence_generation=1`,
		request.Authorization.RequestID, key.TenantID, source.HighWater).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("durable exact audit count=%d err=%v", audits, err)
	}

	// A response-loss retry reuses the same fence and one queue key.
	retry, err := runtime.ScheduleHistoricalBackfill(ctx, request)
	if err != nil || !retry.Existing || !retry.Scheduled || retry.Fence.Token != result.Fence.Token {
		t.Fatalf("idempotent retry=%+v err=%v", retry, err)
	}
	if err := canonical.pool.QueryRow(ctx, `SELECT count(*) FROM brain_projection_work`).Scan(&work); err != nil || work != 1 {
		t.Fatalf("idempotent work count=%d err=%v", work, err)
	}

	claimed, ok := runtime.takePending(ctx)
	if !ok || claimed.key != key {
		t.Fatalf("claimed historical work=%+v ok=%t", claimed, ok)
	}
	if err := runtime.projectScope(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := runtime.completeDurableScope(ctx, claimed); err != nil {
		t.Fatal(err)
	}
	completed, err := runtime.ScheduleHistoricalBackfill(ctx, request)
	if err != nil || !completed.Existing || !completed.Completed || completed.Scheduled {
		t.Fatalf("completed idempotent retry=%+v err=%v", completed, err)
	}

	// Reusing the durable request authority for any other exact scope fails.
	tampered := request
	tampered.Authorization.Key.SittingID = "different-scope"
	if _, err := runtime.ScheduleHistoricalBackfill(ctx, tampered); !errors.Is(err, ErrBrainProjectionBackfillUnauthorized) {
		t.Fatalf("tampered exact authority err=%v", err)
	}
}

func TestBrainProjectionHistoricalBackfillRejectsHistorySkipAndRecoversOnlyExplicitFence(t *testing.T) {
	ctx, canonical, registry := migratedPostgresCanonicalStore(t)
	key := BrainProjectionCheckpointKey{TenantID: "history-canary", ProjectorVersion: brainProjectionProjectorVersion, RoomID: "office", SittingID: "skip", SourceFamily: "memory"}
	appendProjectionCanonicalEvent(t, ctx, canonical, registry, key, "memory-a", 1, "historical a")
	appendProjectionCanonicalEvent(t, ctx, canonical, registry, key, "memory-b", 1, "historical b")
	_, source := resolveProjectionManifestForTest(t, ctx, canonical, &postgresBrainProjectionSourceResolver{}, key)
	runtime := newStoppedProjectionRuntimeForTest(t, canonical)

	skip := historicalBackfillRequestForTest(key, 0, 1, source.HighWater, "backfill-skip")
	if _, err := runtime.ScheduleHistoricalBackfill(ctx, skip); !errors.Is(err, ErrBrainProjectionHistorySkip) {
		t.Fatalf("history skip err=%v", err)
	}
	var checkpoints int
	if err := canonical.pool.QueryRow(ctx, `SELECT count(*) FROM brain_projection_checkpoints`).Scan(&checkpoints); err != nil || checkpoints != 0 {
		t.Fatalf("history-skip canary created checkpoint=%d err=%v", checkpoints, err)
	}

	orphanKey := key
	orphanKey.SittingID = "explicit-orphan"
	appendProjectionCanonicalEvent(t, ctx, canonical, registry, orphanKey, "memory-orphan", 1, "orphan")
	_, orphanSource := resolveProjectionManifestForTest(t, ctx, canonical, &postgresBrainProjectionSourceResolver{}, orphanKey)
	if _, err := beginTestBrainProjectionRebuild(ctx, runtime.checkpoints, BrainProjectionRebuildRequest{
		Key: orphanKey, ExpectedGeneration: 0, StartSourceHighWater: 0, EndSourceHighWater: orphanSource.HighWater,
	}); err != nil {
		t.Fatal(err)
	}
	decoyKey := key
	decoyKey.SittingID = "unfenced-history"
	appendProjectionCanonicalEvent(t, ctx, canonical, registry, decoyKey, "memory-decoy", 1, "decoy")
	if err := runtime.recoverExplicitRebuildWork(ctx); err != nil {
		t.Fatal(err)
	}
	var orphanWork, decoyWork int
	if err := canonical.pool.QueryRow(ctx, `SELECT count(*) FROM brain_projection_work WHERE sitting_id=$1`, orphanKey.SittingID).Scan(&orphanWork); err != nil {
		t.Fatal(err)
	}
	if err := canonical.pool.QueryRow(ctx, `SELECT count(*) FROM brain_projection_work WHERE sitting_id=$1`, decoyKey.SittingID).Scan(&decoyWork); err != nil {
		t.Fatal(err)
	}
	if orphanWork != 0 || decoyWork != 0 {
		t.Fatalf("explicit recovery orphan=%d unfenced-history=%d", orphanWork, decoyWork)
	}
}

func TestBrainProjectionHistoricalBackfillRejectsExpiredUnfencedAudit(t *testing.T) {
	ctx, canonical, registry := migratedPostgresCanonicalStore(t)
	key := BrainProjectionCheckpointKey{TenantID: "expired-tenant", ProjectorVersion: brainProjectionProjectorVersion, RoomID: "office", SittingID: "expired", SourceFamily: "memory"}
	appendProjectionCanonicalEvent(t, ctx, canonical, registry, key, "memory-expired", 1, "expired history")
	_, source := resolveProjectionManifestForTest(t, ctx, canonical, &postgresBrainProjectionSourceResolver{}, key)
	runtime := newStoppedProjectionRuntimeForTest(t, canonical)
	request := historicalBackfillRequestForTest(key, 0, 0, source.HighWater, "expired-audit")
	request.Authorization.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	if _, err := canonical.pool.Exec(ctx, `INSERT INTO brain_projection_backfill_requests (
		request_id,tenant_id,projector_version,room_id,sitting_id,source_family,expected_generation,
		start_source_high_water,end_source_high_water,authorized_by,approval_reference,authorization_expires_at,accepted_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::timestamptz,$12::timestamptz-interval '1 hour')`, request.Authorization.RequestID, key.TenantID,
		key.ProjectorVersion, key.RoomID, key.SittingID, key.SourceFamily, request.Authorization.ExpectedGeneration,
		request.Authorization.StartSourceHighWater, request.Authorization.EndSourceHighWater, request.Authorization.AuthorizedBy,
		request.Authorization.ApprovalReference, request.Authorization.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ScheduleHistoricalBackfill(ctx, request); !errors.Is(err, ErrBrainProjectionBackfillUnauthorized) {
		t.Fatalf("expired unfenced audit err=%v", err)
	}
	var checkpoints, work int
	if err := canonical.pool.QueryRow(ctx, `SELECT count(*) FROM brain_projection_checkpoints`).Scan(&checkpoints); err != nil {
		t.Fatal(err)
	}
	if err := canonical.pool.QueryRow(ctx, `SELECT count(*) FROM brain_projection_work`).Scan(&work); err != nil {
		t.Fatal(err)
	}
	if checkpoints != 0 || work != 0 {
		t.Fatalf("expired audit created checkpoint=%d work=%d", checkpoints, work)
	}
}

func TestBrainProjectionBoundedBackfillSurvivesAppendAndPurgeThenCatchesUp(t *testing.T) {
	ctx, canonical, registry := migratedPostgresCanonicalStore(t)
	key := BrainProjectionCheckpointKey{TenantID: "moving-tenant", ProjectorVersion: brainProjectionProjectorVersion, RoomID: "office", SittingID: "moving", SourceFamily: "memory"}
	appendProjectionCanonicalEvent(t, ctx, canonical, registry, key, "memory-a", 1, "historical a")
	appendProjectionCanonicalEvent(t, ctx, canonical, registry, key, "memory-b", 1, "historical b")
	_, boundedSource := resolveProjectionManifestForTest(t, ctx, canonical, &postgresBrainProjectionSourceResolver{}, key)
	runtime := newStoppedProjectionRuntimeForTest(t, canonical)
	prior := currentProductionBrainProjectionRuntime()
	brainProjectionRuntimeState.Lock()
	brainProjectionRuntimeState.runtime = runtime
	brainProjectionRuntimeState.Unlock()
	t.Cleanup(func() {
		brainProjectionRuntimeState.Lock()
		brainProjectionRuntimeState.runtime = prior
		brainProjectionRuntimeState.Unlock()
	})
	request := historicalBackfillRequestForTest(key, 0, 0, boundedSource.HighWater, "moving-backfill")
	result, err := runtime.ScheduleHistoricalBackfill(ctx, request)
	if err != nil || !result.Scheduled {
		t.Fatalf("schedule=%+v err=%v", result, err)
	}
	boundedWork, ok := runtime.takePending(ctx)
	if !ok {
		t.Fatal("bounded work was not leased")
	}
	appendProjectionCanonicalEvent(t, ctx, canonical, registry, key, "memory-c", 1, "new after fence")
	digestB := sha256.Sum256([]byte("historical b"))
	evidence := make(map[RetentionResourceClass]string, len(mandatoryPurgeClasses))
	for _, class := range mandatoryPurgeClasses {
		evidence[class] = "deleted"
	}
	if err := NewPostgresPurgeLedger(canonical.pool).RecordPurge(ctx, PurgeLedgerEntry{Key: RetentionKey{
		TenantID: key.TenantID, ObjectType: key.SourceFamily, ObjectID: "memory-b", RevisionID: "1",
	}, ContentDigest: hex.EncodeToString(digestB[:]), PolicyID: "bounded-test", PurgedAt: time.Now().UTC(), DestructionEvidence: evidence}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.projectScope(ctx, key); err != nil {
		t.Fatalf("bounded generation failed after source movement: %v", err)
	}
	if err := runtime.completeDurableScope(ctx, boundedWork); !errors.Is(err, ErrBrainProjectionLeaseLost) {
		t.Fatalf("superseded bounded lease err=%v", err)
	}
	nextWork, ok := runtime.takePending(ctx)
	if !ok || nextWork.generation <= boundedWork.generation {
		t.Fatalf("incremental work=%+v ok=%t bounded=%+v", nextWork, ok, boundedWork)
	}
	if err := runtime.projectScope(ctx, key); err != nil {
		t.Fatalf("incremental catch-up failed: %v", err)
	}
	if err := runtime.completeDurableScope(ctx, nextWork); err != nil {
		t.Fatal(err)
	}
	status, err := runtime.checkpoints.Status(ctx, key)
	if err != nil || status.Stale || status.Checkpoint.SourceHighWater <= boundedSource.HighWater {
		t.Fatalf("final status=%+v err=%v", status, err)
	}
	manifest, _ := resolveProjectionManifestForTest(t, ctx, canonical, &postgresBrainProjectionSourceResolver{}, key)
	replay, _, _, err := BuildBrainProjectionSanitizedReplay(manifest)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, claim := range replay.Claims {
		got[claim.SourceObjectID] = true
	}
	if !got["memory-a"] || !got["memory-c"] || got["memory-b"] {
		t.Fatalf("purge/append catch-up claims=%+v", replay.Claims)
	}
}
