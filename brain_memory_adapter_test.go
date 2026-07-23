package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type adapterBrainPurgeGeneration int64

func (generation adapterBrainPurgeGeneration) CurrentPurgeGeneration(context.Context, string) (int64, error) {
	return int64(generation), nil
}

type selectiveBrainConsent struct{ denied map[string]bool }

func (consent selectiveBrainConsent) VerifyBrainSourceConsent(_ context.Context, entry meetingMemoryEntry) error {
	if consent.denied[entry.ID] {
		return ErrBrainSourceConsentAbsent
	}
	return nil
}

type failingBrainConsent struct{ err error }

func (consent failingBrainConsent) VerifyBrainSourceConsent(context.Context, meetingMemoryEntry) error {
	return consent.err
}

type brainAuthorizationTrace struct {
	mu     sync.Mutex
	events []string
}

func (trace *brainAuthorizationTrace) add(event string) {
	trace.mu.Lock()
	trace.events = append(trace.events, event)
	trace.mu.Unlock()
}

func (trace *brainAuthorizationTrace) reset() {
	trace.mu.Lock()
	trace.events = nil
	trace.mu.Unlock()
}

func (trace *brainAuthorizationTrace) snapshot() []string {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return append([]string(nil), trace.events...)
}

type tracedBrainACLStore struct {
	ACLStore
	trace *brainAuthorizationTrace
}

func (store tracedBrainACLStore) ResolveACLObject(ctx context.Context, ref ACLObjectRef) (ACLObject, error) {
	store.trace.add("acl")
	return store.ACLStore.ResolveACLObject(ctx, ref)
}

func (store tracedBrainACLStore) ListACLGrants(ctx context.Context, ref ACLObjectRef) ([]ACLGrant, error) {
	store.trace.add("acl")
	return store.ACLStore.ListACLGrants(ctx, ref)
}

type tracedBrainPurge struct {
	trace      *brainAuthorizationTrace
	generation int64
}

func (purge tracedBrainPurge) CurrentPurgeGeneration(context.Context, string) (int64, error) {
	purge.trace.add("purge")
	return purge.generation, nil
}

type tracedBrainConsent struct{ trace *brainAuthorizationTrace }

func (consent tracedBrainConsent) VerifyBrainSourceConsent(context.Context, meetingMemoryEntry) error {
	consent.trace.add("consent")
	return nil
}

func persistAdapterEntries(t *testing.T, store *meetingMemoryStore, entries []meetingMemoryEntry) {
	t.Helper()
	var raw bytes.Buffer
	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		raw.Write(line)
		raw.WriteByte('\n')
	}
	if err := os.WriteFile(store.path, raw.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.entries = append([]meetingMemoryEntry(nil), entries...)
	store.seen = make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		store.seen[entry.ID] = struct{}{}
	}
	store.mu.Unlock()
}

func writeAdapterFileOnly(t *testing.T, store *meetingMemoryStore, entries []meetingMemoryEntry) {
	t.Helper()
	var raw bytes.Buffer
	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		raw.Write(line)
		raw.WriteByte('\n')
	}
	if err := os.WriteFile(store.path, raw.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMeetingMemoryBrainAdapterBuildsAuthorizedExactInventoryAndReadsLocalBytes(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	entries := []meetingMemoryEntry{
		{ID: "source-a", Kind: meetingMemoryKindTranscript, Text: "AJ: launch remains blocked by review", CreatedAt: start.Add(time.Minute), Metadata: map[string]string{
			"roomId": "room-a", "meetingId": "sitting-a", "speaker": "AJ", "captureSequence": "1", "capturedAt": start.Add(time.Minute).Format(time.RFC3339Nano),
		}},
		{ID: "source-denied", Kind: meetingMemoryKindTranscript, Text: "Tim: private source", CreatedAt: start.Add(2 * time.Minute), Metadata: map[string]string{
			"roomId": "room-a", "meetingId": "sitting-a", "speaker": "Tim", "captureSequence": "2", "capturedAt": start.Add(2 * time.Minute).Format(time.RFC3339Nano),
		}},
	}
	persistAdapterEntries(t, store, entries)

	acl := &MemoryACLStore{Objects: map[string]ACLObject{}, Grants: map[string][]ACLGrant{}}
	principal := ACLPrincipal{TenantID: "tenant-a", ID: "aj@shareability.com", Kind: ACLPrincipalUser}
	for _, entry := range entries {
		ref := ACLObjectRef{TenantID: principal.TenantID, Type: "memory", ID: entry.ID, ACLVersion: 3}
		digest := digestBrainString(entry.Text)
		if entry.ID == "source-denied" {
			digest = digestBrainString("canonical body drift hidden from this principal")
		}
		acl.Objects[aclObjectKey(ref)] = ACLObject{Ref: ref, CurrentContentRevision: 1, CurrentContentDigest: digest}
		if entry.ID == "source-a" {
			acl.Grants[aclObjectKey(ref)] = []ACLGrant{{
				ID: "grant-" + entry.ID, TenantID: ref.TenantID, ObjectType: ref.Type, ObjectID: ref.ID, ACLVersion: ref.ACLVersion,
				SubjectKind: ACLSubjectPrincipal, SubjectID: principal.ID, SubjectPrincipalKind: principal.Kind,
				Actions: []ACLAction{ACLReadMetadata, ACLReadContent},
			}}
		}
	}
	temporal, err := NewBoundedTemporalQuery(TemporalExplicitRange, start, start.Add(time.Hour), "UTC", "room-a", "sitting-a", "adapter exact-range test")
	if err != nil {
		t.Fatal(err)
	}
	adapter := &MeetingMemoryBrainAdapter{
		Memory: store, Objects: aclBrainCurrentObjectResolver{Store: acl}, Kernel: AuthorizationKernel{Store: acl}, Purge: adapterBrainPurgeGeneration(0),
		Consent: selectiveBrainConsent{}, Now: func() time.Time { return start.Add(30 * time.Minute) },
	}
	planner := BrainRetrievalPlanner{
		Inventory: adapter, Bodies: adapter, Kernel: AuthorizationKernel{Store: acl}, Purge: adapterBrainPurgeGeneration(0),
		PromptLimits: BrainPromptLimits{MaxSourceChunkBytes: 32, MaxPromptBytes: 128, MaxFoldInputs: 2, MaxFoldOutputBytes: 32},
	}
	result, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: principal, Query: "launch blocker", Temporal: temporal})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sources) != 1 || result.Sources[0].Evidence.ObjectID != "source-a" || result.Sources[0].Body != entries[0].Text {
		t.Fatalf("retrieval sources=%+v", result.Sources)
	}
	if result.Coverage.Status != RecallCoverageComplete || result.Coverage.AuthorizedSources != 1 || result.Coverage.SourceHighWater != 1 || result.Coverage.CaptureCompleteThrough != 1 {
		t.Fatalf("coverage=%+v", result.Coverage)
	}
	if result.Sources[0].Evidence.SourceFamily != "memory" || result.Sources[0].Evidence.ACLVersion != 3 || result.Sources[0].Evidence.ContentDigest != digestBrainString(entries[0].Text) {
		t.Fatalf("evidence=%+v", result.Sources[0].Evidence)
	}
}

func TestMeetingMemoryBrainAdapterDetectsBodyDriftAndCaptureGaps(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	entry := meetingMemoryEntry{ID: "source-a", Kind: meetingMemoryKindTranscript, Text: "original", CreatedAt: stamp, Metadata: map[string]string{
		"roomId": "office", "meetingId": "sitting-a", "speaker": "AJ", "captureSequence": "2", "capturedAt": stamp.Format(time.RFC3339Nano),
	}}
	persistAdapterEntries(t, store, []meetingMemoryEntry{entry})
	ref := ACLObjectRef{TenantID: "tenant-a", Type: "memory", ID: entry.ID, ACLVersion: 1}
	principal := ACLPrincipal{TenantID: "tenant-a", ID: "aj@shareability.com", Kind: ACLPrincipalUser}
	acl := &MemoryACLStore{Objects: map[string]ACLObject{aclObjectKey(ref): {Ref: ref, CurrentContentRevision: 1, CurrentContentDigest: digestBrainString(entry.Text)}}, Grants: map[string][]ACLGrant{
		aclObjectKey(ref): {{ID: "grant-a", TenantID: ref.TenantID, ObjectType: ref.Type, ObjectID: ref.ID, ACLVersion: ref.ACLVersion, SubjectKind: ACLSubjectPrincipal, SubjectID: principal.ID, SubjectPrincipalKind: principal.Kind, Actions: []ACLAction{ACLReadMetadata, ACLReadContent}}},
	}}
	adapter := &MeetingMemoryBrainAdapter{
		Memory: store, Objects: aclBrainCurrentObjectResolver{Store: acl}, Kernel: AuthorizationKernel{Store: acl}, Purge: adapterBrainPurgeGeneration(0), Consent: selectiveBrainConsent{}, Now: func() time.Time { return stamp.Add(time.Hour) },
	}
	temporal, _ := NewBoundedTemporalQuery(TemporalExplicitRange, stamp.Add(-time.Minute), stamp.Add(time.Minute), "UTC", "office", "sitting-a", "drift test")
	page, err := adapter.InventoryBrainSources(context.Background(), BrainSourceInventoryRequest{TenantID: "tenant-a", Principal: principal, Temporal: temporal}, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.CaptureCompleteThrough != 0 || page.SourceHighWater != 2 || len(page.Sources) != 1 {
		t.Fatalf("gap proof page=%+v", page)
	}
	mutated := entry
	mutated.Text = "mutated"
	writeAdapterFileOnly(t, store, []meetingMemoryEntry{mutated})
	store.mu.Lock()
	if store.entries[0].Text != entry.Text {
		store.mu.Unlock()
		t.Fatal("test failed to preserve a stale in-memory cache")
	}
	store.mu.Unlock()
	if _, err := adapter.ReadBrainSource(context.Background(), page.Sources[0].Evidence); !errors.Is(err, ErrBrainRetrievalRetry) {
		t.Fatalf("body drift err=%v, want retry", err)
	}
	evidenceID, _ := brainRetrievalEvidenceID(page.Sources[0].Evidence)
	if err := adapter.ReauthorizeEvidence(context.Background(), principal, []RetrievalSnapshotSource{{EvidenceID: evidenceID, Evidence: page.Sources[0].Evidence}}); !errors.Is(err, ErrRetrievalSnapshotStale) {
		t.Fatalf("publication body drift err=%v, want stale snapshot", err)
	}
}

func TestMeetingMemoryBrainAdapterDeniedSequencesNeverAffectContinuity(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	entry := func(id string, sequence int, offset time.Duration) meetingMemoryEntry {
		return meetingMemoryEntry{ID: id, Kind: meetingMemoryKindTranscript, Text: id, CreatedAt: stamp.Add(offset), Metadata: map[string]string{
			"roomId": "office", "meetingId": "sitting-a", "captureSequence": fmt.Sprint(sequence), "capturedAt": stamp.Add(offset).Format(time.RFC3339Nano),
		}}
	}
	entries := []meetingMemoryEntry{entry("authorized-1", 1, time.Minute), entry("denied-2", 2, 2*time.Minute), entry("authorized-3", 3, 3*time.Minute)}
	persistAdapterEntries(t, store, entries)
	principal := ACLPrincipal{TenantID: "tenant-a", ID: "aj@shareability.com", Kind: ACLPrincipalUser}
	acl := &MemoryACLStore{Objects: map[string]ACLObject{}, Grants: map[string][]ACLGrant{}}
	for _, source := range entries {
		ref := ACLObjectRef{TenantID: principal.TenantID, Type: "memory", ID: source.ID, ACLVersion: 1}
		acl.Objects[aclObjectKey(ref)] = ACLObject{Ref: ref, CurrentContentRevision: 1, CurrentContentDigest: digestBrainString(source.Text)}
		acl.Grants[aclObjectKey(ref)] = []ACLGrant{{ID: "grant-" + source.ID, TenantID: ref.TenantID, ObjectType: ref.Type, ObjectID: ref.ID, ACLVersion: 1,
			SubjectKind: ACLSubjectPrincipal, SubjectID: principal.ID, SubjectPrincipalKind: principal.Kind, Actions: []ACLAction{ACLReadMetadata, ACLReadContent}}}
	}
	temporal, _ := NewBoundedTemporalQuery(TemporalExplicitRange, stamp, stamp.Add(time.Hour), "UTC", "office", "sitting-a", "denied continuity canary")
	adapter := &MeetingMemoryBrainAdapter{Memory: store, Objects: aclBrainCurrentObjectResolver{Store: acl}, Kernel: AuthorizationKernel{Store: acl}, Purge: adapterBrainPurgeGeneration(0), Consent: selectiveBrainConsent{denied: map[string]bool{"denied-2": true}}}
	request := BrainSourceInventoryRequest{TenantID: principal.TenantID, Principal: principal, Temporal: temporal}
	withDenied, err := adapter.InventoryBrainSources(context.Background(), request, "")
	if err != nil {
		t.Fatal(err)
	}
	persistAdapterEntries(t, store, []meetingMemoryEntry{entries[0], entries[2]})
	withoutDenied, err := adapter.InventoryBrainSources(context.Background(), request, "")
	if err != nil {
		t.Fatal(err)
	}
	if withDenied.CaptureCompleteThrough != 1 || withoutDenied.CaptureCompleteThrough != 1 ||
		withDenied.SourceHighWater != 3 || withoutDenied.SourceHighWater != 3 || withDenied.ExpectedSourceCount != withoutDenied.ExpectedSourceCount {
		t.Fatalf("denied source changed coverage: with=%+v without=%+v", withDenied, withoutDenied)
	}
}

func TestMeetingMemoryBrainAdapterDistinguishesConsentAbsenceFromAuthorityOutage(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	entry := meetingMemoryEntry{ID: "source-a", Kind: meetingMemoryKindTranscript, Text: "authorized body", CreatedAt: stamp, Metadata: map[string]string{
		"roomId": "office", "meetingId": "sitting-a", "captureSequence": "1", "capturedAt": stamp.Format(time.RFC3339Nano),
	}}
	persistAdapterEntries(t, store, []meetingMemoryEntry{entry})
	ref := ACLObjectRef{TenantID: "tenant-a", Type: "memory", ID: entry.ID, ACLVersion: 1}
	principal := ACLPrincipal{TenantID: "tenant-a", ID: "aj@shareability.com", Kind: ACLPrincipalUser}
	acl := &MemoryACLStore{Objects: map[string]ACLObject{aclObjectKey(ref): {Ref: ref, CurrentContentRevision: 1, CurrentContentDigest: digestBrainString(entry.Text)}}, Grants: map[string][]ACLGrant{
		aclObjectKey(ref): {{ID: "grant-a", TenantID: ref.TenantID, ObjectType: ref.Type, ObjectID: ref.ID, ACLVersion: 1, SubjectKind: ACLSubjectPrincipal,
			SubjectID: principal.ID, SubjectPrincipalKind: principal.Kind, Actions: []ACLAction{ACLReadMetadata, ACLReadContent}}},
	}}
	temporal, _ := NewBoundedTemporalQuery(TemporalExplicitRange, stamp.Add(-time.Minute), stamp.Add(time.Minute), "UTC", "office", "sitting-a", "consent outage")
	base := &MeetingMemoryBrainAdapter{Memory: store, Objects: aclBrainCurrentObjectResolver{Store: acl}, Kernel: AuthorizationKernel{Store: acl}, Purge: adapterBrainPurgeGeneration(0), Consent: selectiveBrainConsent{}}
	page, err := base.InventoryBrainSources(context.Background(), BrainSourceInventoryRequest{TenantID: principal.TenantID, Principal: principal, Temporal: temporal}, "")
	if err != nil || len(page.Sources) != 1 {
		t.Fatalf("inventory err=%v page=%+v", err, page)
	}
	base.Consent = selectiveBrainConsent{denied: map[string]bool{entry.ID: true}}
	read, err := base.ReadBrainSource(context.Background(), page.Sources[0].Evidence)
	if err != nil || read.Status != RecallSourceOmitted {
		t.Fatalf("absent consent read=%+v err=%v", read, err)
	}
	base.Consent = failingBrainConsent{err: errors.New("consent database unavailable")}
	if _, err := base.ReadBrainSource(context.Background(), page.Sources[0].Evidence); !errors.Is(err, ErrBrainRetrievalUnavailable) {
		t.Fatalf("authority outage err=%v, want unavailable", err)
	}
	evidenceID, _ := brainRetrievalEvidenceID(page.Sources[0].Evidence)
	if err := base.ReauthorizeEvidence(context.Background(), principal, []RetrievalSnapshotSource{{EvidenceID: evidenceID, Evidence: page.Sources[0].Evidence}}); !errors.Is(err, ErrBrainRetrievalUnavailable) {
		t.Fatalf("publication authority outage err=%v, want unavailable", err)
	}
	base.Consent = selectiveBrainConsent{denied: map[string]bool{entry.ID: true}}
	if err := base.ReauthorizeEvidence(context.Background(), principal, []RetrievalSnapshotSource{{EvidenceID: evidenceID, Evidence: page.Sources[0].Evidence}}); !errors.Is(err, ErrRetrievalSnapshotStale) {
		t.Fatalf("publication consent absence err=%v, want stale", err)
	}
}

func TestProductionCatchUpReauthorizationEndsWithFreshPurgeAndACLAfterBodyConsent(t *testing.T) {
	store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "meeting-memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	entry := meetingMemoryEntry{ID: "source-a", Kind: meetingMemoryKindTranscript, Text: "decision evidence", CreatedAt: stamp, Metadata: map[string]string{
		"roomId": "office", "meetingId": "sitting-a", "captureSequence": "1", "capturedAt": stamp.Format(time.RFC3339Nano),
	}}
	persistAdapterEntries(t, store, []meetingMemoryEntry{entry})
	principal := ACLPrincipal{TenantID: "tenant-a", ID: "aj@shareability.com", Kind: ACLPrincipalUser}
	ref := ACLObjectRef{TenantID: principal.TenantID, Type: "memory", ID: entry.ID, ACLVersion: 1}
	baseACL := &MemoryACLStore{Objects: map[string]ACLObject{aclObjectKey(ref): {Ref: ref, CurrentContentRevision: 1, CurrentContentDigest: digestBrainString(entry.Text)}}, Grants: map[string][]ACLGrant{
		aclObjectKey(ref): {{ID: "grant-a", TenantID: ref.TenantID, ObjectType: ref.Type, ObjectID: ref.ID, ACLVersion: 1, SubjectKind: ACLSubjectPrincipal,
			SubjectID: principal.ID, SubjectPrincipalKind: principal.Kind, Actions: []ACLAction{ACLReadMetadata, ACLReadContent}}},
	}}
	trace := &brainAuthorizationTrace{}
	acl := tracedBrainACLStore{ACLStore: baseACL, trace: trace}
	purge := tracedBrainPurge{trace: trace, generation: 9}
	adapter := &MeetingMemoryBrainAdapter{Memory: store, Objects: aclBrainCurrentObjectResolver{Store: acl}, Kernel: AuthorizationKernel{Store: acl}, Purge: purge, Consent: tracedBrainConsent{trace: trace}, Now: func() time.Time { return stamp.Add(time.Minute) }}
	temporal, _ := NewBoundedTemporalQuery(TemporalExplicitRange, stamp.Add(-time.Minute), stamp.Add(time.Minute), "UTC", "office", "sitting-a", "publication ordering")
	planner := BrainRetrievalPlanner{Inventory: adapter, Bodies: adapter, Kernel: AuthorizationKernel{Store: acl}, Purge: purge,
		PromptLimits: BrainPromptLimits{MaxSourceChunkBytes: 64, MaxPromptBytes: 1024, MaxFoldInputs: 2, MaxFoldOutputBytes: 128}}
	result, err := planner.Resolve(context.Background(), BrainRetrievalRequest{Principal: principal, Query: "decision", Temporal: temporal})
	if err != nil {
		t.Fatal(err)
	}
	trace.reset()
	// This unit uses an in-memory ACL store, so exercise the same ordering used
	// to mint production publication fences before the PostgreSQL conditional
	// commit owns the canonical rows.
	if _, err := adapter.ReauthorizeEvidenceWithConsentFences(context.Background(), principal, result.Snapshot.Sources); err != nil {
		t.Fatal(err)
	}
	if err := ReauthorizeRetrievalSnapshot(context.Background(), planner.Kernel, planner.Purge, principal, result.Snapshot); err != nil {
		t.Fatal(err)
	}
	events := trace.snapshot()
	last := func(want string) int {
		for index := len(events) - 1; index >= 0; index-- {
			if events[index] == want {
				return index
			}
		}
		return -1
	}
	if last("consent") < 0 || last("purge") <= last("consent") || last("acl") <= last("purge") {
		t.Fatalf("publication order=%v, want body consent before final purge and ACL", events)
	}
}

func TestProductionCatchUpConditionalPublicationRejectsPostPreflightMutation(t *testing.T) {
	for _, mutation := range []string{"disk body rewrite", "purge", "ACL revoke"} {
		t.Run(mutation, func(t *testing.T) {
			ctx, canonical, registry := migratedPostgresCanonicalStore(t)
			store, err := newMeetingMemoryStore(filepath.Join(t.TempDir(), "meeting-memory.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			stamp := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
			entry := meetingMemoryEntry{ID: "source-a", Kind: meetingMemoryKindTranscript, Text: "conditional publication evidence", CreatedAt: stamp, Metadata: map[string]string{
				"roomId": "office", "meetingId": "sitting-a", "captureSequence": "1", "capturedAt": stamp.Format(time.RFC3339Nano), "source": transcriptSourceRoomChat,
			}}
			persistAdapterEntries(t, store, []meetingMemoryEntry{entry})
			key := BrainProjectionCheckpointKey{TenantID: "tenant-catchup-commit", ProjectorVersion: "company-brain/v2", RoomID: "office", SittingID: "sitting-a", SourceFamily: "memory"}
			appendProjectionCanonicalEvent(t, ctx, canonical, registry, key, entry.ID, 1, entry.Text)
			principal := ACLPrincipal{TenantID: key.TenantID, ID: "aj@shareability.com", Kind: ACLPrincipalUser, TeamIDs: []string{"organization"}, RoomID: key.RoomID, SittingID: key.SittingID}
			for _, action := range []ACLAction{ACLReadMetadata, ACLReadContent} {
				if _, err := canonical.pool.Exec(ctx, `INSERT INTO object_grants (
					grant_id,tenant_id,object_type,object_id,acl_version,subject_type,subject_id,action,granted_by_type,granted_by_id
				) VALUES ($1,$2,$3,$4,1,$5,$6,$7,'service','catch-up-test')`, uuid.New(), key.TenantID, key.SourceFamily, entry.ID,
					string(ACLPrincipalUser), principal.ID, string(action)); err != nil {
					t.Fatal(err)
				}
			}
			purge := &PostgresPurgeGenerationResolver{pool: canonical.pool}
			adapter := &MeetingMemoryBrainAdapter{
				Memory: store, Objects: aclBrainCurrentObjectResolver{Store: canonical}, Kernel: AuthorizationKernel{Store: canonical},
				Purge: purge, Consent: selectiveBrainConsent{}, Now: func() time.Time { return stamp.Add(time.Minute) },
			}
			temporal, err := NewBoundedTemporalQuery(TemporalExplicitRange, stamp.Add(-time.Minute), stamp.Add(time.Minute), "UTC", key.RoomID, key.SittingID, "conditional publication")
			if err != nil {
				t.Fatal(err)
			}
			planner := BrainRetrievalPlanner{Inventory: adapter, Bodies: adapter, Kernel: AuthorizationKernel{Store: canonical}, Purge: purge,
				PromptLimits: BrainPromptLimits{MaxSourceChunkBytes: 8 << 10, MaxPromptBytes: 64 << 10, MaxFoldInputs: 8, MaxFoldOutputBytes: 4 << 10}}
			result, err := planner.Resolve(ctx, BrainRetrievalRequest{Principal: principal, Query: "catch me up", Temporal: temporal})
			if err != nil || len(result.Snapshot.Sources) != 1 {
				t.Fatalf("resolve result=%+v err=%v", result, err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			resolver := &productionCatchUpResolver{Planner: planner, Sources: adapter, Postgres: canonical, beforeCommit: func() {
				close(entered)
				<-release
			}}
			type commitResult struct {
				published bool
				err       error
			}
			done := make(chan commitResult, 1)
			go func() {
				published := false
				err := resolver.CommitCatchUpPublication(context.Background(), principal, result.Snapshot, func() error {
					published = true
					return nil
				})
				done <- commitResult{published: published, err: err}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("publication did not reach post-preflight barrier")
			}
			switch mutation {
			case "disk body rewrite":
				changed := entry
				changed.Text = "rewritten after successful preflight"
				writeAdapterFileOnly(t, store, []meetingMemoryEntry{changed})
			case "purge":
				digest, decodeErr := hex.DecodeString(digestBrainString(entry.Text))
				if decodeErr != nil {
					t.Fatal(decodeErr)
				}
				if _, err := canonical.pool.Exec(ctx, `INSERT INTO purge_ledger (
					tenant_id,object_type,object_id,revision_id,content_sha256,policy_id,purged_at,destruction_evidence
				) VALUES ($1,$2,$3,'1',$4,'catch-up-test',now(),'{"proof":"destroyed"}'::jsonb)`, key.TenantID, key.SourceFamily, entry.ID, digest); err != nil {
					t.Fatal(err)
				}
			case "ACL revoke":
				if _, err := canonical.pool.Exec(ctx, `UPDATE object_grants SET revoked_at=now()
					WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3 AND action=$4`, key.TenantID, key.SourceFamily, entry.ID, string(ACLReadContent)); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			select {
			case got := <-done:
				if got.err == nil || got.published {
					t.Fatalf("post-preflight %s published=%t err=%v", mutation, got.published, got.err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("conditional publication did not finish")
			}
		})
	}
}
