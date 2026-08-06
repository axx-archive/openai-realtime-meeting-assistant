package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func ambientProjectionRef(kind STRIDEContractType, id, seed string, revision int64) STRIDEReference {
	return STRIDEReference{ContractType: kind, ID: id, Revision: revision, Digest: digestBrainString(seed)}
}

func ambientProjectionSource(ref STRIDEReference, principals []string, highWater uint64, fresh time.Time) AmbientMindSourceAuthority {
	return AmbientMindSourceAuthority{
		Ref: ref, Audience: STRIDEAudience{Visibility: "channel", Principals: principals}, ACLVersion: 1,
		SourceHighWater: highWater, FreshThrough: fresh.UTC(),
	}
}

func ambientProjectionUpsert(tenant, eventID, key string, sequence, highWater uint64, node AmbientMindProjectionNode, sources ...AmbientMindSourceAuthority) AmbientMindProjectionEvent {
	return AmbientMindProjectionEvent{
		TenantID: tenant, EventID: eventID, IdempotencyKey: key, Sequence: sequence, SourceHighWater: highWater,
		Operation: AmbientMindProjectionUpsert, Node: &node, Sources: sources, OccurredAt: node.FreshThrough.UTC(),
	}
}

func TestAmbientMindProjectionFailsClosedAcrossAudienceTenantAndIdempotency(t *testing.T) {
	now := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	sourceRef := ambientProjectionRef(STRIDEContractConversationEvent, "event_ball_dogs", "source", 1)
	source := ambientProjectionSource(sourceRef, []string{"aj", "scout"}, 10, now)
	node := AmbientMindProjectionNode{
		Ref: ambientProjectionRef(STRIDEContractAnalysisProjection, "decision_ball_dogs", "decision", 1), LogicalID: "ball_dogs_strategy",
		Kind: AmbientMindDecision, SourceRefs: []STRIDEReference{sourceRef}, Audience: STRIDEAudience{Visibility: "channel", Principals: []string{"aj"}},
		ACLVersion: 1, SourceHighWater: 10, FreshThrough: now,
	}
	event := ambientProjectionUpsert("bonfire", "projection_event_1", "projection_key_1", 1, 10, node, source)
	projector := NewAmbientMindProjector()
	first, err := projector.Apply(event)
	if err != nil || first.ThroughSequence != 1 {
		t.Fatalf("apply=%+v err=%v", first, err)
	}
	if retry, err := projector.Apply(event); err != nil || retry != first || len(projector.Snapshot().Events) != 1 {
		t.Fatalf("idempotent retry=%+v err=%v events=%d", retry, err, len(projector.Snapshot().Events))
	}
	conflict := cloneAmbientMindEvent(event)
	conflict.EventID = "projection_event_conflict"
	if _, err := projector.Apply(conflict); !errors.Is(err, ErrAmbientMindProjectionConflict) {
		t.Fatalf("idempotency collision=%v", err)
	}
	crossTenant := cloneAmbientMindEvent(event)
	crossTenant.TenantID, crossTenant.EventID, crossTenant.IdempotencyKey = "other_company", "projection_other", "projection_other_key"
	if _, err := projector.Apply(crossTenant); !errors.Is(err, ErrAmbientMindProjectionDenied) {
		t.Fatalf("cross-tenant apply=%v", err)
	}
	visible, checkpoint, err := projector.QueryForPrincipal("bonfire", "aj")
	if err != nil || len(visible) != 1 || checkpoint.SourceHighWater != 10 {
		t.Fatalf("AJ query=%+v checkpoint=%+v err=%v", visible, checkpoint, err)
	}
	if hidden, _, err := projector.QueryForPrincipal("bonfire", "scout"); err != nil || len(hidden) != 0 {
		t.Fatalf("node audience widened to Scout: %+v err=%v", hidden, err)
	}

	widened := cloneAmbientMindEvent(event)
	widened.EventID, widened.IdempotencyKey, widened.Sequence = "projection_event_2", "projection_key_2", 2
	widened.Node.Audience.Principals = []string{"aj", "outsider"}
	if err := widened.Validate(); !errors.Is(err, ErrAmbientMindProjectionDenied) {
		t.Fatalf("widened audience validation=%v", err)
	}
	extra := cloneAmbientMindEvent(event)
	extra.EventID, extra.IdempotencyKey, extra.Sequence = "projection_event_3", "projection_key_3", 2
	extra.Sources = append(extra.Sources, ambientProjectionSource(ambientProjectionRef(STRIDEContractConversationEvent, "unbound_source", "extra", 1), []string{"aj"}, 10, now))
	if err := extra.Validate(); !errors.Is(err, ErrAmbientMindProjectionInvalid) {
		t.Fatalf("unbound source validation=%v", err)
	}
}

func TestAmbientMindProjectionRebuildRevocationAndFreshnessParity(t *testing.T) {
	base := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	sourceOneRef := ambientProjectionRef(STRIDEContractConversationEvent, "source_one", "source-one", 1)
	sourceTwoRef := ambientProjectionRef(STRIDEContractTranscriptRevision, "source_two", "source-two", 1)
	sourceOne := ambientProjectionSource(sourceOneRef, []string{"aj"}, 1, base.Add(time.Minute))
	sourceTwo := ambientProjectionSource(sourceTwoRef, []string{"aj"}, 2, base.Add(20*time.Minute))
	firstNode := AmbientMindProjectionNode{
		Ref: ambientProjectionRef(STRIDEContractAnalysisProjection, "decision_v1", "decision-v1", 1), LogicalID: "decision_launch", Kind: AmbientMindDecision,
		SourceRefs: []STRIDEReference{sourceOneRef}, Audience: STRIDEAudience{Visibility: "channel", Principals: []string{"aj"}}, ACLVersion: 1,
		SourceHighWater: 1, FreshThrough: sourceOne.FreshThrough,
	}
	projector := NewAmbientMindProjector()
	if _, err := projector.Apply(ambientProjectionUpsert("bonfire", "event_1", "key_1", 1, 1, firstNode, sourceOne)); err != nil {
		t.Fatal(err)
	}
	secondNode := AmbientMindProjectionNode{
		Ref: ambientProjectionRef(STRIDEContractAnalysisProjection, "decision_v2", "decision-v2", 2), LogicalID: firstNode.LogicalID, Kind: AmbientMindDecision,
		SourceRefs: []STRIDEReference{sourceTwoRef}, Audience: STRIDEAudience{Visibility: "channel", Principals: []string{"aj"}}, ACLVersion: 2,
		SourceHighWater: 2, FreshThrough: sourceTwo.FreshThrough, SupersedesRef: &firstNode.Ref,
	}
	if _, err := projector.Apply(ambientProjectionUpsert("bonfire", "event_2", "key_2", 2, 2, secondNode, sourceTwo)); err != nil {
		t.Fatal(err)
	}
	childNode := AmbientMindProjectionNode{
		Ref: ambientProjectionRef(STRIDEContractKnowledgeAssertion, "storyline_v1", "storyline-v1", 1), LogicalID: "storyline_launch", Kind: AmbientMindStoryline,
		SourceRefs: []STRIDEReference{sourceTwoRef}, ParentRefs: []STRIDEReference{secondNode.Ref}, Audience: STRIDEAudience{Visibility: "channel", Principals: []string{"aj"}},
		ACLVersion: 2, SourceHighWater: 3, FreshThrough: base.Add(15 * time.Minute),
	}
	if _, err := projector.Apply(ambientProjectionUpsert("bonfire", "event_3", "key_3", 3, 3, childNode, sourceTwo)); err != nil {
		t.Fatal(err)
	}
	beforeRevoke := projector.Snapshot()
	if !beforeRevoke.Checkpoint.FreshThrough.Equal(childNode.FreshThrough) {
		t.Fatalf("freshThrough=%s want current-node minimum %s", beforeRevoke.Checkpoint.FreshThrough, childNode.FreshThrough)
	}
	revoke := AmbientMindProjectionEvent{TenantID: "bonfire", EventID: "event_4", IdempotencyKey: "key_4", Sequence: 4, SourceHighWater: 4,
		Operation: AmbientMindProjectionRevoke, TargetRef: &sourceTwoRef, Reason: "source_revoked", OccurredAt: base.Add(30 * time.Minute)}
	if _, err := projector.Apply(revoke); err != nil {
		t.Fatal(err)
	}
	afterRevoke := projector.Snapshot()
	if !afterRevoke.Checkpoint.FreshThrough.IsZero() {
		t.Fatalf("no-current-node freshness=%s, want unknown", afterRevoke.Checkpoint.FreshThrough)
	}
	statuses := map[string]AmbientMindProjectionStatus{}
	for _, state := range afterRevoke.Nodes {
		statuses[state.Node.Ref.ID] = state.Status
	}
	if statuses[firstNode.Ref.ID] != AmbientMindProjectionSuperseded || statuses[secondNode.Ref.ID] != AmbientMindProjectionRetracted || statuses[childNode.Ref.ID] != AmbientMindProjectionRetracted {
		t.Fatalf("statuses after source revoke=%+v", statuses)
	}
	digest := afterRevoke.Digest
	generation := afterRevoke.Checkpoint.Generation
	if _, err := projector.Rebuild(); err != nil {
		t.Fatal(err)
	}
	rebuilt := projector.Snapshot()
	if rebuilt.Digest != digest || rebuilt.Checkpoint.Generation != generation+1 || rebuilt.Checkpoint.ProjectionDigest != afterRevoke.Checkpoint.ProjectionDigest {
		t.Fatalf("rebuild drift before=%+v after=%+v", afterRevoke.Checkpoint, rebuilt.Checkpoint)
	}
	restored, err := RestoreAmbientMindProjector(rebuilt)
	if err != nil || restored.Snapshot().Digest != rebuilt.Digest {
		t.Fatalf("restore digest=%s err=%v", restored.Snapshot().Digest, err)
	}
	gap := cloneAmbientMindEvent(revoke)
	gap.EventID, gap.IdempotencyKey, gap.Sequence = "event_gap", "key_gap", 6
	if _, err := restored.Apply(gap); !errors.Is(err, ErrAmbientMindProjectionGap) {
		t.Fatalf("sequence gap=%v", err)
	}
}

func TestPostgresAmbientMindProjectionShadowIsDefaultOffAndRestartSafe(t *testing.T) {
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	var enabled bool
	if err := canonical.pool.QueryRow(ctx, `SELECT enabled FROM stride_feature_switches WHERE feature_key='ambient_mind_projection_shadow'`).Scan(&enabled); err != nil || enabled {
		t.Fatalf("shadow feature enabled=%t err=%v", enabled, err)
	}
	store := NewPostgresAmbientMindProjectionStore(canonical.pool)
	now := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	sourceRef := ambientProjectionRef(STRIDEContractConversationEvent, "pg_source", "pg-source", 1)
	source := ambientProjectionSource(sourceRef, []string{"aj"}, 1, now)
	node := AmbientMindProjectionNode{
		Ref: ambientProjectionRef(STRIDEContractAnalysisProjection, "pg_decision", "pg-decision", 1), LogicalID: "pg_launch", Kind: AmbientMindDecision,
		SourceRefs: []STRIDEReference{sourceRef}, Audience: STRIDEAudience{Visibility: "channel", Principals: []string{"aj"}}, ACLVersion: 1,
		SourceHighWater: 1, FreshThrough: now,
	}
	event := ambientProjectionUpsert("bonfire", "pg_event_1", "pg_key_1", 1, 1, node, source)
	applied, err := store.Apply(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewPostgresAmbientMindProjectionStore(canonical.pool)
	loaded, err := restarted.Load(ctx, "bonfire")
	if err != nil || loaded.Digest != applied.Digest || loaded.Checkpoint != applied.Checkpoint {
		t.Fatalf("restart load=%+v err=%v want=%+v", loaded.Checkpoint, err, applied.Checkpoint)
	}
	visible, _, err := restarted.QueryForPrincipal(ctx, "bonfire", "aj")
	if err != nil || len(visible) != 1 {
		t.Fatalf("authorized query=%+v err=%v", visible, err)
	}
	if hidden, _, err := restarted.QueryForPrincipal(ctx, "bonfire", "outsider"); err != nil || len(hidden) != 0 {
		t.Fatalf("unauthorized query leaked=%+v err=%v", hidden, err)
	}
	rebuilt, err := restarted.Rebuild(ctx, "bonfire")
	if err != nil || rebuilt.Digest != loaded.Digest || rebuilt.Checkpoint.Generation != loaded.Checkpoint.Generation+1 {
		t.Fatalf("postgres rebuild=%+v err=%v", rebuilt.Checkpoint, err)
	}
	if _, err := canonical.pool.Exec(ctx, `INSERT INTO stride_ambient_projection_sources
		(tenant_id,source_contract_type,source_contract_id,source_revision,source_digest,visibility,audience_principals,audience_digest,acl_version,source_high_water,fresh_through)
		VALUES ('bonfire','conversation_event','invalid_audience',1,decode($1,'hex'),'channel','["aj","aj"]'::jsonb,decode($2,'hex'),1,1,now())`,
		digestBrainString("invalid-source"), digestBrainString("invalid-audience")); err == nil {
		t.Fatal("database accepted duplicate audience principals")
	}
	if _, err := canonical.pool.Exec(ctx, `UPDATE stride_ambient_projection_nodes SET logical_id='tampered' WHERE tenant_id='bonfire' AND node_id='pg_decision'`); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Rebuild(context.Background(), "bonfire"); !errors.Is(err, ErrAmbientMindProjectionConflict) {
		t.Fatalf("immutable node drift rebuild=%v", err)
	}
}
