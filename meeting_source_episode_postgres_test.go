package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func postgresMeetingSourceFixture(t *testing.T, ctx context.Context, canonical *PostgresCanonicalStore) (*PostgresMeetingSourceEpisodeStore, postCloseMeetingSourceMaterial, SourceEpisode) {
	t.Helper()
	t.Setenv("BONFIRE_CANONICAL_TENANT_ID", "company_1")
	store := NewPostgresMeetingSourceEpisodeStore(canonical)
	now := sourceEpisodeTestTime
	binding := ConsentAdmissionBinding{TenantID: "company_1", PrincipalKind: ACLPrincipalUser, PrincipalID: "person_1", RoomID: "room_1", SittingID: "sitting_1", AnchorID: "anchor_1"}
	authority := NewConsentLaneAuthority(NewMemoryConsentStore(), "policy_1")
	fences := []ConsentFence{}
	for _, lane := range []ConsentLane{ConsentLaneModelAnalysis, ConsentLaneOrgMemory} {
		decision, err := authority.Authorize(ctx, binding, lane)
		if err != nil || !decision.Allowed {
			t.Fatalf("consent lane %s: %v", lane, err)
		}
		fences = append(fences, decision.Fence)
	}
	type consentMaterial struct {
		Binding      string
		Lane         ConsentLane
		Policy       string
		Generation   uint64
		RecordDigest string
	}
	parts := []consentMaterial{}
	for _, fence := range fences {
		parts = append(parts, consentMaterial{consentBindingKey(fence.binding), fence.lane, fence.policy, fence.generation, fence.recordDigest})
	}
	consentDigest, _ := STRIDEContractDigest(parts)
	transcript := meetingMemoryEntry{ID: "pg_transcript_1", Kind: meetingMemoryKindTranscript, Text: "approved", Metadata: map[string]string{"meetingId": "sitting_1"}}
	brain := meetingMemoryEntry{ID: "analysis_body_1", Kind: meetingMemoryKindBrain, Text: strings.Repeat("A", 256), Metadata: map[string]string{"meetingId": "sitting_1"}}
	digest := meetingMemoryEntry{ID: "pg_digest_1", Kind: meetingMemoryKindMeetingDigest, Text: "digest", Metadata: map[string]string{"meetingId": "sitting_1"}}
	transcript.BodyDigest, brain.BodyDigest, digest.BodyDigest = sha256Hex([]byte(transcript.Text)), sha256Hex([]byte(brain.Text)), sha256Hex([]byte(digest.Text))
	material := postCloseMeetingSourceMaterial{
		Record: meetingRecord{ID: "sitting_1", RoomID: "room_1"}, ReceiptDigest: strings.Repeat("4", 64), Transcripts: []meetingMemoryEntry{transcript}, Brain: brain, Digest: digest,
		Audience: STRIDEAudience{Visibility: "meeting", Principals: []string{"person_1"}}, Consent: fences, ConsentRev: 2, ConsentDigest: consentDigest, Purge: 0,
	}
	for index, entry := range meetingSourceCanonicalObjects(material) {
		eventID := uuid.New()
		sequence := int64(0)
		if err := canonical.pool.QueryRow(ctx, `INSERT INTO canonical_events(event_id,tenant_id,aggregate_type,aggregate_id,aggregate_version,event_type,schema_version,occurred_at,actor_type,actor_id,meeting_id,classification,acl_version,payload,payload_sha256)
			VALUES($1,'company_1','memory',$2,1,'legacy_imported',1,$3,'service','test','sitting_1','internal',4,'{}',decode($4,'hex')) RETURNING sequence`,
			eventID, entry.ID, now.Add(time.Duration(index)*time.Second), strings.Repeat("d", 64)).Scan(&sequence); err != nil {
			t.Fatal(err)
		}
		if _, err := canonical.pool.Exec(ctx, `INSERT INTO objects(tenant_id,object_type,object_id,state_revision,content_revision,room_id,meeting_id,classification,state,content_sha256,acl_version,last_event_sequence)
			VALUES('company_1','memory',$1,1,1,'room_1','sitting_1','internal','{}',decode($2,'hex'),4,$3)`, entry.ID, entry.BodyDigest, sequence); err != nil {
			t.Fatal(err)
		}
		if _, err := canonical.pool.Exec(ctx, `INSERT INTO object_grants(grant_id,tenant_id,object_type,object_id,acl_version,revision,subject_type,subject_id,action,room_id,sitting_id,granted_by_type,granted_by_id)
			VALUES($1,'company_1','memory',$2,4,NULL,'user','person_1','read_metadata','room_1','sitting_1','service','test'),
			($3,'company_1','memory',$2,4,1,'user','person_1','read_content','room_1','sitting_1','service','test')`, uuid.New(), entry.ID, uuid.New()); err != nil {
			t.Fatal(err)
		}
	}
	resolved, err := store.ResolveAuthority(ctx, material)
	if err != nil {
		t.Fatal(err)
	}
	material = resolved
	input := sourceEpisodeAdapterFixture(SourceEpisodeMeetingAnalysis)
	input.Header.ID = "episode_pg_meeting"
	input.RetrievalBody = SourceEpisodeRevisionRef{
		SourceFamily: SourceEpisodeFamilyMeetingAnalysisBody, ObjectID: brain.ID, ContentRevision: 1,
		ContentDigest: brain.BodyDigest, SizeBytes: int64(len(brain.Text)),
	}
	input.Authority = SourceEpisodeAuthoritySnapshot{Audience: material.Audience, ACLRevision: material.ACLRevision, ACLDigest: material.ACLDigest, ConsentRevision: material.ConsentRev, ConsentDigest: material.ConsentDigest, PurgeGeneration: material.Purge, RetentionPolicy: "company_default", ObservedAt: input.Authority.ObservedAt}
	input.PhaseProof.ReceiptDigest = material.ReceiptDigest
	episode, err := AdaptMeetingAnalysisSourceEpisode(input)
	if err != nil {
		t.Fatal(err)
	}
	return store, material, episode
}

func TestPostgresMeetingSourceEpisodeRejectsWiderAudienceWithoutPerObjectAuthority(t *testing.T) {
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	store, material, _ := postgresMeetingSourceFixture(t, ctx, canonical)
	binding := ConsentAdmissionBinding{TenantID: "company_1", PrincipalKind: ACLPrincipalUser, PrincipalID: "person_2", RoomID: "room_1", SittingID: "sitting_1", AnchorID: "anchor_2"}
	authority := NewConsentLaneAuthority(NewMemoryConsentStore(), "policy_1")
	for _, lane := range []ConsentLane{ConsentLaneModelAnalysis, ConsentLaneOrgMemory} {
		decision, err := authority.Authorize(ctx, binding, lane)
		if err != nil || !decision.Allowed {
			t.Fatalf("consent lane %s: %v", lane, err)
		}
		material.Consent = append(material.Consent, decision.Fence)
	}
	material.Audience.Principals = append(material.Audience.Principals, "person_2")
	if _, err := store.ResolveAuthority(ctx, material); !errors.Is(err, ErrMeetingSourceEpisodeStale) {
		t.Fatalf("wider audience without metadata+content authority err=%v", err)
	}
}

func TestPostgresMeetingSourceEpisodeCatalogFeedsAuthorizedBrainAndACLDriftRemovesIt(t *testing.T) {
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	store, material, episode := postgresMeetingSourceFixture(t, ctx, canonical)
	if _, err := store.CommitMeetingSourceEpisode(ctx, episode, nil, material); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListSourceEpisodes(ctx, "company_1", "")
	if err != nil || len(page.Episodes) != 1 || !page.Terminal || page.PurgeGeneration != 0 {
		t.Fatalf("catalog page=%+v err=%v", page, err)
	}
	if found, ok, err := store.FindSourceEpisodeByRetrievalBody(ctx, "company_1", sourceEpisodeBodyLocator(episode.RetrievalBody)); err != nil || !ok || found.Header.ContentDigest != episode.Header.ContentDigest {
		t.Fatalf("body catalog found=%v episode=%+v err=%v", ok, found.Header, err)
	}
	aclRef := ACLObjectRef{TenantID: "company_1", Type: episode.RetrievalBody.SourceFamily, ID: episode.RetrievalBody.ObjectID, ACLVersion: episode.Authority.ACLRevision}
	if found, ok, err := store.FindSourceEpisodeByACLObject(ctx, aclRef); err != nil || !ok || found.Header.ContentDigest != episode.Header.ContentDigest {
		t.Fatalf("ACL catalog found=%v episode=%+v err=%v", ok, found.Header, err)
	}
	authority := &sourceEpisodeBrainTestAuthority{revoked: map[string]string{}, currentPurge: 0}
	bodies := &sourceEpisodeBrainTestBodies{bodies: map[SourceEpisodeRevisionRef]string{episode.RetrievalBody: material.Brain.Text}}
	adapter := &SourceEpisodeBrainAdapter{Catalog: store, Authority: authority, Bodies: bodies, PageSize: 10, Now: func() time.Time { return sourceEpisodeTestTime.Add(time.Hour) }}
	sources, _ := collectSourceEpisodeInventory(t, adapter, sourceEpisodeBrainPrincipal("person_1"))
	if len(sources) != 1 {
		t.Fatalf("authorized PG inventory=%d, want 1", len(sources))
	}
	read, err := adapter.ReadBrainSource(ctx, sources[0].Evidence)
	if err != nil || !read.BodyAvailable || read.Body != material.Brain.Text {
		t.Fatalf("PG brain read=%+v err=%v", read, err)
	}
	if _, err := canonical.pool.Exec(ctx, `DELETE FROM object_grants WHERE tenant_id='company_1' AND object_type='memory' AND object_id='pg_transcript_1' AND action='read_content'`); err != nil {
		t.Fatal(err)
	}
	sources, _ = collectSourceEpisodeInventory(t, adapter, sourceEpisodeBrainPrincipal("person_1"))
	if len(sources) != 0 {
		t.Fatalf("ACL drift left %d meeting episodes visible", len(sources))
	}
	if _, found, err := store.FindSourceEpisodeByACLObject(ctx, aclRef); err != nil || found {
		t.Fatalf("ACL drift lookup found=%v err=%v", found, err)
	}
}

func TestCompositeSourceEpisodePlannerRetrievesPostgresMeetingAndNativeSource(t *testing.T) {
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	meetings, material, meetingEpisode := postgresMeetingSourceFixture(t, ctx, canonical)
	if _, err := meetings.CommitMeetingSourceEpisode(ctx, meetingEpisode, nil, material); err != nil {
		t.Fatal(err)
	}
	native, err := OpenFileSourceEpisodeLedger("")
	if err != nil {
		t.Fatal(err)
	}
	input := sourceEpisodeAdapterFixture(SourceEpisodeDriveFileRevision)
	input.Header.ID = "episode_native_drive_composite"
	input.Authority.PurgeGeneration = 0
	nativeBody := strings.Repeat("N", 128)
	input.Source.ObjectID = "native_drive_composite"
	input.Source.ContentDigest = digestBrainString(nativeBody)
	input.RetrievalBody = input.Source
	nativeEpisode, err := AdaptDriveFileSourceEpisode(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := native.DualWriteSourceEpisode(ctx, nativeEpisode, nil); err != nil {
		t.Fatal(err)
	}
	catalog, err := NewCompositeSourceEpisodeCatalog(native, meetings)
	if err != nil {
		t.Fatal(err)
	}
	authority := &sourceEpisodeBrainTestAuthority{revoked: map[string]string{}, currentPurge: 0}
	bodies := &sourceEpisodeBrainTestBodies{bodies: map[SourceEpisodeRevisionRef]string{
		meetingEpisode.RetrievalBody: material.Brain.Text,
		nativeEpisode.RetrievalBody:  nativeBody,
	}}
	registry := NewSourceEpisodeRuntimeRegistry()
	for _, family := range []string{SourceEpisodeFamilyMeetingAnalysis, SourceEpisodeFamilyDriveFileRevision} {
		if err := registry.RegisterAuthority(family, authority); err != nil {
			t.Fatal(err)
		}
	}
	for _, family := range []string{SourceEpisodeFamilyMeetingAnalysisBody, SourceEpisodeFamilyDriveFileRevision} {
		if err := registry.RegisterBodyReader(family, bodies); err != nil {
			t.Fatal(err)
		}
	}
	adapter, err := NewSourceEpisodeShadowBrainAdapter(catalog, registry, 16, func() time.Time { return sourceEpisodeTestTime.Add(time.Hour) })
	if err != nil {
		t.Fatal(err)
	}
	planner, err := NewSourceEpisodeCatalogPlanner(adapter, catalog, registry, meetings, nil, BrainPromptLimits{MaxSourceChunkBytes: 128, MaxPromptBytes: 1024, MaxFoldInputs: 4, MaxFoldOutputBytes: 128})
	if err != nil {
		t.Fatal(err)
	}
	principal := sourceEpisodeBrainPrincipal("person_1")
	principal.TeamIDs = []string{"organization"}
	result, err := planner.Resolve(ctx, BrainRetrievalRequest{Principal: principal, Query: "approved native", Temporal: sourceEpisodeBrainTemporal()})
	if err != nil || len(result.Sources) != 2 || len(result.Snapshot.Sources) != 2 {
		t.Fatalf("composite planner sources=%d snapshot=%d evidence=%+v err=%v", len(result.Sources), len(result.Snapshot.Sources), result.Sources, err)
	}
	joined := result.Sources[0].Body + "\n" + result.Sources[1].Body
	if !strings.Contains(joined, material.Brain.Text) || !strings.Contains(joined, nativeBody) {
		t.Fatalf("composite planner did not read both exact bodies: %q", joined)
	}
	// Final-snapshot reauthorization removes a native source immediately.
	authority.revoked[nativeEpisode.Header.ID] = "acl"
	result, err = planner.Resolve(ctx, BrainRetrievalRequest{Principal: principal, Query: "approved native", Temporal: sourceEpisodeBrainTemporal()})
	if err != nil || len(result.Sources) != 1 || result.Sources[0].Evidence.SourceFamily != SourceEpisodeFamilyMeetingAnalysisBody {
		t.Fatalf("composite reauthorization result=%+v err=%v", result.Sources, err)
	}
}

func TestPostgresMeetingSourceEpisodeAtomicCASReplayAndSourceDriftTombstone(t *testing.T) {
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	store, material, episode := postgresMeetingSourceFixture(t, ctx, canonical)
	result, err := store.CommitMeetingSourceEpisode(ctx, episode, nil, material)
	if err != nil || result.Replayed {
		t.Fatalf("commit=%+v err=%v", result, err)
	}
	replay, err := store.CommitMeetingSourceEpisode(ctx, episode, nil, material)
	if err != nil || !replay.Replayed {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	if current, found, err := store.CurrentSourceEpisode(ctx, "company_1", episode.Header.ID); err != nil || !found || current.Header.ContentDigest != episode.Header.ContentDigest {
		t.Fatalf("current found=%v err=%v", found, err)
	}
	if _, err := canonical.pool.Exec(ctx, `UPDATE objects SET content_revision=2,content_sha256=decode($3,'hex') WHERE tenant_id=$1 AND object_type='memory' AND object_id=$2`, "company_1", "pg_transcript_1", strings.Repeat("e", 64)); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.CurrentSourceEpisode(ctx, "company_1", episode.Header.ID); err != nil || found {
		t.Fatalf("drift left active found=%v err=%v", found, err)
	}
}

func TestPostgresMeetingSourceEpisodeRollbackAndConcurrentDriftLinearize(t *testing.T) {
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	store, material, episode := postgresMeetingSourceFixture(t, ctx, canonical)
	injected := errors.New("injected before insert")
	store.Failpoint = func(string) error { return injected }
	if _, err := store.CommitMeetingSourceEpisode(ctx, episode, nil, material); !errors.Is(err, injected) {
		t.Fatalf("rollback err=%v", err)
	}
	if _, found, err := store.CurrentSourceEpisode(ctx, "company_1", episode.Header.ID); err != nil || found {
		t.Fatalf("rollback ghost found=%v err=%v", found, err)
	}

	entered, release := make(chan struct{}), make(chan struct{})
	var once atomic.Bool
	store.Failpoint = func(string) error {
		if once.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		return nil
	}
	commitDone := make(chan error, 1)
	go func() {
		_, err := store.CommitMeetingSourceEpisode(context.Background(), episode, nil, material)
		commitDone <- err
	}()
	<-entered
	driftDone := make(chan error, 1)
	go func() {
		_, err := canonical.pool.Exec(context.Background(), `UPDATE objects SET content_revision=2,content_sha256=decode($3,'hex') WHERE tenant_id=$1 AND object_type='memory' AND object_id=$2`, "company_1", "pg_transcript_1", strings.Repeat("e", 64))
		driftDone <- err
	}()
	select {
	case err := <-driftDone:
		t.Fatalf("source drift crossed locked publication: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-commitDone; err != nil {
		t.Fatal(err)
	}
	if err := <-driftDone; err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.CurrentSourceEpisode(ctx, "company_1", episode.Header.ID); err != nil || found {
		t.Fatalf("post-drift active found=%v err=%v", found, err)
	}
}

func TestPostgresMeetingSourceEpisodeAuthorityLeaseHoldsThroughBodyUse(t *testing.T) {
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	store, material, episode := postgresMeetingSourceFixture(t, ctx, canonical)
	if _, err := store.CommitMeetingSourceEpisode(ctx, episode, nil, material); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	leaseDone := make(chan error, 1)
	go func() {
		leaseDone <- store.WithCurrentSourceEpisodeAuthority(context.Background(), []SourceEpisode{episode}, func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	driftDone := make(chan error, 1)
	go func() {
		_, err := canonical.pool.Exec(context.Background(), `DELETE FROM object_grants
			WHERE tenant_id='company_1' AND object_type='memory' AND object_id='pg_transcript_1' AND action='read_content'`)
		driftDone <- err
	}()
	select {
	case err := <-driftDone:
		t.Fatalf("ACL drift crossed body-use authority lease: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-leaseDone; err != nil {
		t.Fatalf("authority lease: %v", err)
	}
	if err := <-driftDone; err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.CurrentSourceEpisode(ctx, "company_1", episode.Header.ID); err != nil || found {
		t.Fatalf("post-lease ACL drift left active found=%v err=%v", found, err)
	}
}
