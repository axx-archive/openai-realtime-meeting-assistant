package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type replayTestAuthority struct {
	mu            sync.Mutex
	snapshot      AmbientReplayAuthoritySnapshot
	drift         bool
	revalidations int
}

func (a *replayTestAuthority) Plan(_ context.Context, request AmbientReplayPlanRequest) (AmbientReplayAuthoritySnapshot, error) {
	snapshot := a.snapshot
	if snapshot.ApprovalReference == "" {
		snapshot.ApprovalReference = request.ApprovalReference
	}
	if snapshot.RollbackFloor == "" {
		snapshot.RollbackFloor = request.RollbackFloor
	}
	return snapshot, nil
}
func (a *replayTestAuthority) Revalidate(context.Context, AmbientReplayManifest) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.revalidations++
	if a.drift {
		return ErrAmbientReplayDrift
	}
	return nil
}

type replayTestStore struct {
	mu         sync.Mutex
	manifests  map[string]AmbientReplayManifest
	status     map[string]string
	executions map[string]string
	lease      map[string]time.Time
	receipts   map[string]AmbientReplayStageReceipt
}

func newReplayTestStore() *replayTestStore {
	return &replayTestStore{manifests: map[string]AmbientReplayManifest{}, status: map[string]string{}, executions: map[string]string{}, lease: map[string]time.Time{}, receipts: map[string]AmbientReplayStageReceipt{}}
}
func (s *replayTestStore) SaveManifest(_ context.Context, m AmbientReplayManifest) (AmbientReplayManifest, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.manifests[m.Digest]; ok {
		return old, false, nil
	}
	for _, old := range s.manifests {
		if old.TenantID == m.TenantID && old.IdempotencyKey == m.IdempotencyKey {
			if ambientReplayManifestEquivalentForRetry(old, m) {
				return old, false, nil
			}
			return AmbientReplayManifest{}, false, ErrAmbientReplayDrift
		}
	}
	for digest, old := range s.manifests {
		if old.TenantID == m.TenantID && old.RoomID == m.RoomID && old.SittingID == m.SittingID && (s.status[digest] == "planned" || s.status[digest] == "running") {
			if ambientReplayManifestEquivalentForRetry(old, m) {
				return old, false, nil
			}
			return AmbientReplayManifest{}, false, ErrAmbientReplayAlreadyActive
		}
	}
	s.manifests[m.Digest], s.status[m.Digest] = m, "planned"
	return m, true, nil
}
func (s *replayTestStore) LoadManifest(_ context.Context, digest string) (AmbientReplayManifest, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.manifests[digest]
	if !ok {
		return m, "", ErrAmbientReplayInvalid
	}
	return m, s.status[digest], nil
}
func (s *replayTestStore) BeginExecution(_ context.Context, m AmbientReplayManifest, id string, now time.Time) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prior := s.executions[m.Digest]; prior != "" {
		return prior, true, nil
	}
	if s.status[m.Digest] != "planned" {
		return "", false, ErrAmbientReplayUnauthorized
	}
	if len(m.Stages) == 0 {
		return "", false, ErrAmbientReplayInvalid
	}
	s.executions[m.Digest], s.status[m.Digest], s.lease[m.Digest] = id, "running", now.Add(ambientReplayExecutionLease)
	input := make([]AmbientReplayArtifact, 0, len(m.Sources))
	for _, source := range m.Sources {
		input = append(input, AmbientReplayArtifact{ID: source.ObjectID, Kind: "transcript", Digest: source.ContentDigest, SourceManifestDigest: m.SourceManifestDigest, ManifestDigest: m.Digest})
	}
	inputDigest, _ := digestAmbientReplayArtifacts(input)
	s.receipts[m.Digest+":"+id+":"+m.Stages[0].Name] = AmbientReplayStageReceipt{ManifestDigest: m.Digest, ExecutionID: id, Stage: m.Stages[0].Name, Status: "prepared", InputDigest: inputDigest, SourceDigest: m.SourceManifestDigest, StartedAt: now}
	return id, false, nil
}
func (s *replayTestStore) RenewExecutionLease(_ context.Context, digest, id string, now, expires time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status[digest] != "running" || s.executions[digest] != id || !s.lease[digest].After(now) {
		return ErrAmbientReplayDrift
	}
	s.lease[digest] = expires
	return nil
}
func (s *replayTestStore) RecordStageReceipt(_ context.Context, receipt AmbientReplayStageReceipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	observed := receipt.CompletedAt
	if observed.IsZero() {
		observed = receipt.StartedAt
	}
	if s.status[receipt.ManifestDigest] != "running" || s.executions[receipt.ManifestDigest] != receipt.ExecutionID || !s.lease[receipt.ManifestDigest].After(observed) {
		return ErrAmbientReplayDrift
	}
	key := receipt.ManifestDigest + ":" + receipt.ExecutionID + ":" + receipt.Stage
	if prior, ok := s.receipts[key]; ok && prior.ExecutionID != receipt.ExecutionID {
		return ErrAmbientReplayDrift
	}
	s.receipts[key] = receipt
	return nil
}
func (s *replayTestStore) CompleteExecution(_ context.Context, digest, id, status string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.executions[digest] != id || s.status[digest] != "running" || !s.lease[digest].After(at) {
		return ErrAmbientReplayDrift
	}
	s.status[digest] = status
	s.lease[digest] = time.Time{}
	return nil
}
func (s *replayTestStore) ReclaimExpired(_ context.Context, now time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int64
	for digest, manifest := range s.manifests {
		if s.status[digest] == "planned" && !manifest.ExpiresAt.After(now) {
			s.status[digest] = "expired"
			count++
		} else if s.status[digest] == "running" && !s.lease[digest].After(now) {
			s.status[digest], s.lease[digest] = "failed", time.Time{}
			for key, receipt := range s.receipts {
				if receipt.ManifestDigest == digest && receipt.ExecutionID == s.executions[digest] && (receipt.Status == "prepared" || receipt.Status == "running") {
					receipt.Status, receipt.ErrorCode, receipt.CompletedAt = "failed", "execution_lease_expired", now
					s.receipts[key] = receipt
				}
			}
			count++
		}
	}
	return count, nil
}

type replayTestRunner struct {
	mu     sync.Mutex
	inputs map[string][]AmbientReplayArtifact
	calls  int
	usage  AmbientReplayUsage
	onCall func(int)
	err    error
}

func (r *replayTestRunner) RunAmbientReplayStage(_ context.Context, m AmbientReplayManifest, stage AmbientReplayStageSpec, input []AmbientReplayArtifact) (AmbientReplayStageResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.inputs[stage.Name] = append([]AmbientReplayArtifact(nil), input...)
	if r.onCall != nil {
		r.onCall(r.calls)
	}
	if r.err != nil {
		return AmbientReplayStageResult{}, r.err
	}
	digest := digestBrainString(m.Digest + ":" + stage.Name)
	return AmbientReplayStageResult{Artifacts: []AmbientReplayArtifact{{ID: "artifact-" + stage.Name, Kind: stage.Name, Digest: digest, SourceManifestDigest: m.SourceManifestDigest, ManifestDigest: m.Digest}}, Usage: r.usage}, nil
}

var (
	replayTestPlanKey           = digestBrainString("replay-test-plan-key")
	replayTestApprovalReference = digestBrainString("replay-test-approval-receipt")
	replayTestRollbackFloor     = digestBrainString("replay-test-retained-rollback-receipt")
)

func replayFixture(now time.Time, count int) AmbientReplayAuthoritySnapshot {
	sources := make([]AmbientReplaySource, 0, count)
	for i := 0; i < count; i++ {
		sources = append(sources, AmbientReplaySource{ObjectID: fmt.Sprintf("transcript-%03d", i), CaptureSequence: uint64(i + 11), ContentRevision: 1, ContentDigest: digestBrainString(fmt.Sprintf("body-%d", i)), ACLVersion: 3, PurgeGeneration: 7, OccurredStart: now.Add(time.Duration(i) * time.Second), OccurredEnd: now.Add(time.Duration(i+1) * time.Second), ConsentFenceDigest: digestBrainString(fmt.Sprintf("consent-%d", i)), RoomID: officeRoomID, SittingID: "sitting-oldest"})
	}
	return AmbientReplayAuthoritySnapshot{Sources: sources, CursorDigests: map[string]string{"brain": digestBrainString("brain-cursor")}, PurgeGeneration: 7, ReleaseCommit: "0123456789abcdef0123456789abcdef01234567", ReleaseTreeDigest: digestBrainString("tree"), ReleaseReceiptDigest: digestBrainString("receipt")}
}

func replayPlanForTest(t *testing.T, stages []string) (*AmbientReplayEngine, *replayTestAuthority, *replayTestStore, AmbientReplayManifest) {
	t.Helper()
	now := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	authority := &replayTestAuthority{snapshot: replayFixture(now.Add(-time.Hour), 4)}
	store := newReplayTestStore()
	engine := &AmbientReplayEngine{Authority: authority, Store: store, Now: func() time.Time { return now }, NewID: func() string { return "11111111-1111-4111-8111-111111111111" }}
	manifest, err := engine.Plan(context.Background(), AmbientReplayPlanRequest{IdempotencyKey: replayTestPlanKey, TenantID: "bonfire", RoomID: officeRoomID, StageNames: stages, AuthorizedBy: "aj@shareability.com", ApprovalReference: replayTestApprovalReference, RollbackFloor: replayTestRollbackFloor, ExpiresAt: now.Add(10 * time.Minute)})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return engine, authority, store, manifest
}

func TestAmbientReplayPlanFreezesOneBoundedSittingAndDigest(t *testing.T) {
	now := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	authority := &replayTestAuthority{snapshot: replayFixture(now.Add(-time.Hour), ambientReplayMaxSources)}
	store := newReplayTestStore()
	engine := &AmbientReplayEngine{Authority: authority, Store: store, Now: func() time.Time { return now }}
	request := AmbientReplayPlanRequest{IdempotencyKey: replayTestPlanKey, TenantID: "bonfire", RoomID: officeRoomID, AuthorizedBy: "aj@shareability.com", ApprovalReference: replayTestApprovalReference, RollbackFloor: replayTestRollbackFloor, ExpiresAt: now.Add(time.Minute)}
	manifest, err := engine.Plan(context.Background(), request)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(manifest.Sources) != 48 || manifest.StartAfter != 10 || manifest.EndAt != 58 || manifest.Stages[0].Name != "brain" || manifest.CursorDigests["brain"] == "" || !isHexDigest(manifest.Digest) {
		t.Fatalf("manifest not frozen: %+v", manifest)
	}
	calculated, _ := ambientReplayManifestDigest(manifest)
	if calculated != manifest.Digest {
		t.Fatalf("digest=%s calculated=%s", manifest.Digest, calculated)
	}
	retry, err := engine.Plan(context.Background(), request)
	if err != nil || retry.Digest != manifest.Digest {
		t.Fatalf("idempotent plan=%+v err=%v", retry, err)
	}
}

func TestAmbientReplayPlanUsesExactSelectedTailAndCannotSkipExcludedSources(t *testing.T) {
	now := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	snapshot := replayFixture(now.Add(-time.Hour), ambientReplayMaxSources)
	for index := ambientReplayMaxSources; index < 60; index++ {
		snapshot.ExcludedSources = append(snapshot.ExcludedSources, fmt.Sprintf("transcript-%03d", index))
	}
	authority := &replayTestAuthority{snapshot: snapshot}
	engine := &AmbientReplayEngine{Authority: authority, Store: newReplayTestStore(), Now: func() time.Time { return now }}
	manifest, err := engine.Plan(context.Background(), AmbientReplayPlanRequest{IdempotencyKey: digestBrainString("exact-tail-plan"),
		TenantID: "bonfire", RoomID: officeRoomID, EndAt: 1_000, AuthorizedBy: "aj@shareability.com",
		ApprovalReference: replayTestApprovalReference, RollbackFloor: replayTestRollbackFloor, ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	wantTail := snapshot.Sources[len(snapshot.Sources)-1].CaptureSequence
	if manifest.EndAt != wantTail || manifest.EndAt == 1_000 || len(manifest.ExcludedSources) != 12 {
		t.Fatalf("endAt=%d want selected tail %d; exclusions=%v", manifest.EndAt, wantTail, manifest.ExcludedSources)
	}
}

func TestAmbientReplayPlanRetryUsesStableKeyAcrossGeneratedAtAndTerminalState(t *testing.T) {
	now := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	authority := &replayTestAuthority{snapshot: replayFixture(now.Add(-time.Hour), 2)}
	store := newReplayTestStore()
	engine := &AmbientReplayEngine{Authority: authority, Store: store, Now: func() time.Time { return now }}
	request := AmbientReplayPlanRequest{IdempotencyKey: digestBrainString("stable-plan-retry"), TenantID: "bonfire", RoomID: officeRoomID,
		AuthorizedBy: "aj@shareability.com", ApprovalReference: replayTestApprovalReference, RollbackFloor: replayTestRollbackFloor,
		ExpiresAt: now.Add(10 * time.Minute)}
	first, err := engine.Plan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	request.ExpiresAt = now.Add(9 * time.Minute)
	retry, err := engine.Plan(context.Background(), request)
	if err != nil || retry.Digest != first.Digest || !retry.GeneratedAt.Equal(first.GeneratedAt) {
		t.Fatalf("retry=%+v first=%+v err=%v", retry, first, err)
	}
	store.status[first.Digest] = "completed"
	now = now.Add(time.Minute)
	request.ExpiresAt = now.Add(8 * time.Minute)
	terminalRetry, err := engine.Plan(context.Background(), request)
	if err != nil || terminalRetry.Digest != first.Digest {
		t.Fatalf("terminal retry=%+v err=%v", terminalRetry, err)
	}
}

func TestAmbientReplayRejectsUnresolvedAuthorityFences(t *testing.T) {
	now := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	authority := &replayTestAuthority{snapshot: replayFixture(now.Add(-time.Hour), 1)}
	authority.snapshot.ApprovalReference = digestBrainString("different-approval")
	authority.snapshot.RollbackFloor = replayTestRollbackFloor
	engine := &AmbientReplayEngine{Authority: authority, Store: newReplayTestStore(), Now: func() time.Time { return now }}
	request := AmbientReplayPlanRequest{IdempotencyKey: digestBrainString("authority-fence-plan"), TenantID: "bonfire", RoomID: officeRoomID,
		AuthorizedBy: "aj@shareability.com", ApprovalReference: replayTestApprovalReference, RollbackFloor: replayTestRollbackFloor,
		ExpiresAt: now.Add(time.Minute)}
	if _, err := engine.Plan(context.Background(), request); !errors.Is(err, ErrAmbientReplayUnauthorized) {
		t.Fatalf("mismatched authority fence err=%v", err)
	}
	request.ApprovalReference = "typed-but-unverified"
	if _, err := engine.Plan(context.Background(), request); !errors.Is(err, ErrAmbientReplayInvalid) {
		t.Fatalf("weak authority reference err=%v", err)
	}
}

func TestAmbientReplayPlanRejectsBoardCrossSittingAndCeiling(t *testing.T) {
	engine, authority, _, _ := replayPlanForTest(t, []string{"brain"})
	base := AmbientReplayPlanRequest{IdempotencyKey: digestBrainString("replay-invalid-plan"), TenantID: "bonfire", RoomID: officeRoomID, AuthorizedBy: "aj@shareability.com", ApprovalReference: replayTestApprovalReference, RollbackFloor: replayTestRollbackFloor, ExpiresAt: engine.now().Add(time.Minute)}
	base.StageNames = []string{"brain", "board"}
	if _, err := engine.Plan(context.Background(), base); !errors.Is(err, ErrAmbientReplayInvalid) {
		t.Fatalf("board err=%v", err)
	}
	base.StageNames, base.MaxCalls = []string{"brain"}, ambientReplayDefaultMaxCalls+1
	if _, err := engine.Plan(context.Background(), base); !errors.Is(err, ErrAmbientReplayCeiling) {
		t.Fatalf("ceiling err=%v", err)
	}
	base.MaxCalls = 1
	authority.snapshot.Sources[1].SittingID = "other"
	if _, err := engine.Plan(context.Background(), base); !errors.Is(err, ErrAmbientReplayInvalid) {
		t.Fatalf("cross-sitting err=%v", err)
	}
}

func TestAmbientReplayExecuteRevalidatesEveryStageAndChainsOnlyManifestArtifacts(t *testing.T) {
	engine, authority, store, manifest := replayPlanForTest(t, []string{"brain", "decision", "day_fold"})
	runner := &replayTestRunner{inputs: map[string][]AmbientReplayArtifact{}, usage: AmbientReplayUsage{Calls: 0}}
	engine.Runner = runner
	execution, err := engine.Execute(context.Background(), manifest.Digest, manifest.AuthorizedBy)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if execution.Status != "completed" || len(execution.Receipts) != 3 || authority.revalidations != 4 {
		t.Fatalf("execution=%+v revalidations=%d", execution, authority.revalidations)
	}
	if got := runner.inputs["brain"]; len(got) != len(manifest.Sources) || got[0].Kind != "transcript" {
		t.Fatalf("brain inputs=%+v", got)
	}
	if got := runner.inputs["decision"]; len(got) != 1 || got[0].ID != "artifact-brain" || got[0].ManifestDigest != manifest.Digest {
		t.Fatalf("decision input escaped manifest: %+v", got)
	}
	if got := runner.inputs["day_fold"]; len(got) != 1 || got[0].ID != "artifact-decision" {
		t.Fatalf("fold input=%+v", got)
	}
	if store.status[manifest.Digest] != "completed" {
		t.Fatalf("status=%s", store.status[manifest.Digest])
	}
}

func TestAmbientReplayDriftStopsBeforeNextProviderAndPersistsReceipt(t *testing.T) {
	engine, authority, store, manifest := replayPlanForTest(t, []string{"brain", "decision"})
	runner := &replayTestRunner{inputs: map[string][]AmbientReplayArtifact{}}
	runner.onCall = func(call int) {
		if call == 1 {
			authority.drift = true
		}
	}
	engine.Runner = runner
	_, err := engine.Execute(context.Background(), manifest.Digest, manifest.AuthorizedBy)
	if !errors.Is(err, ErrAmbientReplayDrift) {
		t.Fatalf("drift err=%v", err)
	}
	if runner.calls != 1 || store.status[manifest.Digest] != "drifted" {
		t.Fatalf("calls=%d status=%s", runner.calls, store.status[manifest.Digest])
	}
}

func TestAmbientReplayCeilingFailsClosedAndRestartIsIdempotent(t *testing.T) {
	engine, _, store, manifest := replayPlanForTest(t, []string{"brain"})
	engine.Runner = &replayTestRunner{inputs: map[string][]AmbientReplayArtifact{}, usage: AmbientReplayUsage{Calls: 2}}
	if _, err := engine.Execute(context.Background(), manifest.Digest, manifest.AuthorizedBy); !errors.Is(err, ErrAmbientReplayCeiling) {
		t.Fatalf("ceiling err=%v", err)
	}
	if store.status[manifest.Digest] != "failed" {
		t.Fatalf("status=%s", store.status[manifest.Digest])
	}
	// A failed manifest cannot be silently restarted or spend again.
	restarted := &AmbientReplayEngine{Authority: engine.Authority, Store: store, Runner: engine.Runner, Now: engine.Now}
	if _, err := restarted.Execute(context.Background(), manifest.Digest, manifest.AuthorizedBy); !errors.Is(err, ErrAmbientReplayUnauthorized) {
		t.Fatalf("restart err=%v", err)
	}
}

func TestAmbientReplayExpiredPlanAndLeaseAreReclaimedWithoutStaleWrites(t *testing.T) {
	now := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	authority := &replayTestAuthority{snapshot: replayFixture(now.Add(-time.Hour), 2)}
	store := newReplayTestStore()
	engine := &AmbientReplayEngine{Authority: authority, Store: store, Now: func() time.Time { return now }}
	plan := func(key string, expires time.Time) AmbientReplayManifest {
		manifest, err := engine.Plan(context.Background(), AmbientReplayPlanRequest{IdempotencyKey: digestBrainString(key), TenantID: "bonfire", RoomID: officeRoomID,
			AuthorizedBy: "aj@shareability.com", ApprovalReference: replayTestApprovalReference, RollbackFloor: replayTestRollbackFloor, ExpiresAt: expires})
		if err != nil {
			t.Fatalf("plan %s: %v", key, err)
		}
		return manifest
	}
	expired := plan("expires-before-run", now.Add(time.Minute))
	now = now.Add(2 * time.Minute)
	replacement := plan("replacement-after-expiry", now.Add(10*time.Minute))
	if store.status[expired.Digest] != "expired" || replacement.Digest == expired.Digest {
		t.Fatalf("expired status=%s replacement=%s", store.status[expired.Digest], replacement.Digest)
	}

	executionID := "44444444-4444-4444-8444-444444444444"
	if _, existing, err := store.BeginExecution(context.Background(), replacement, executionID, now); err != nil || existing {
		t.Fatalf("begin existing=%v err=%v", existing, err)
	}
	preparedKey := replacement.Digest + ":" + executionID + ":brain"
	if store.receipts[preparedKey].Status != "prepared" {
		t.Fatalf("missing atomic prepared receipt: %+v", store.receipts[preparedKey])
	}
	now = now.Add(ambientReplayExecutionLease + time.Second)
	reclaimed, err := store.ReclaimExpired(context.Background(), now)
	if err != nil || reclaimed != 1 || store.status[replacement.Digest] != "failed" || store.receipts[preparedKey].ErrorCode != "execution_lease_expired" {
		t.Fatalf("reclaimed=%d status=%s receipt=%+v err=%v", reclaimed, store.status[replacement.Digest], store.receipts[preparedKey], err)
	}
	late := store.receipts[preparedKey]
	late.Status, late.OutputDigest, late.CompletedAt = "completed", digestBrainString("late-output"), now
	if err := store.RecordStageReceipt(context.Background(), late); !errors.Is(err, ErrAmbientReplayDrift) {
		t.Fatalf("late receipt err=%v", err)
	}
	if err := store.CompleteExecution(context.Background(), replacement.Digest, executionID, "completed", now); !errors.Is(err, ErrAmbientReplayDrift) {
		t.Fatalf("late completion err=%v", err)
	}
	newer := plan("replacement-after-lease", now.Add(5*time.Minute))
	if newer.Digest == replacement.Digest {
		t.Fatal("lease recovery reused abandoned manifest")
	}
}

func TestAmbientReplayCancellationFailsClosedAndAllowsFreshPlan(t *testing.T) {
	engine, _, store, manifest := replayPlanForTest(t, []string{"brain"})
	engine.Runner = &replayTestRunner{inputs: map[string][]AmbientReplayArtifact{}, err: context.Canceled}
	if _, err := engine.Execute(context.Background(), manifest.Digest, manifest.AuthorizedBy); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err=%v", err)
	}
	if store.status[manifest.Digest] != "failed" {
		t.Fatalf("status=%s", store.status[manifest.Digest])
	}
	foundCancelled := false
	for _, receipt := range store.receipts {
		if receipt.ManifestDigest == manifest.Digest && receipt.ErrorCode == "cancelled" && receipt.Status == "failed" {
			foundCancelled = true
		}
	}
	if !foundCancelled {
		t.Fatalf("cancel receipt missing: %+v", store.receipts)
	}
	_, err := engine.Plan(context.Background(), AmbientReplayPlanRequest{IdempotencyKey: digestBrainString("fresh-after-cancel"), TenantID: "bonfire", RoomID: officeRoomID,
		AuthorizedBy: manifest.AuthorizedBy, ApprovalReference: replayTestApprovalReference, RollbackFloor: replayTestRollbackFloor,
		ExpiresAt: engine.now().Add(time.Minute)})
	if err != nil {
		t.Fatalf("fresh plan after cancel: %v", err)
	}
}

func TestAmbientReplayConcurrentExecuteAdmitsOneExecution(t *testing.T) {
	engine, _, store, manifest := replayPlanForTest(t, []string{"brain"})
	runner := &replayTestRunner{inputs: map[string][]AmbientReplayArtifact{}}
	engine.Runner = runner
	second := &AmbientReplayEngine{Authority: engine.Authority, Store: store, Runner: runner, Now: engine.Now, NewID: func() string { return "22222222-2222-4222-8222-222222222222" }}
	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	for _, candidate := range []*AmbientReplayEngine{engine, second} {
		go func(candidate *AmbientReplayEngine) {
			defer wg.Done()
			_, err := candidate.Execute(context.Background(), manifest.Digest, manifest.AuthorizedBy)
			errs <- err
		}(candidate)
	}
	wg.Wait()
	close(errs)
	failures := 0
	for err := range errs {
		if err != nil {
			failures++
		}
	}
	if failures != 0 || runner.calls != 1 {
		t.Fatalf("failures=%d provider calls=%d", failures, runner.calls)
	}
}

func TestPostgresAmbientReplayStorePersistsManifestCursorAndReceipts(t *testing.T) {
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	store := &PostgresAmbientReplayStore{pool: canonical.pool}
	now := time.Now().UTC().Truncate(time.Second)
	authority := &replayTestAuthority{snapshot: replayFixture(now.Add(-time.Hour), 2)}
	engine := &AmbientReplayEngine{Authority: authority, Store: store, Now: func() time.Time { return now }, NewID: func() string { return "33333333-3333-4333-8333-333333333333" }}
	manifest, err := engine.Plan(ctx, AmbientReplayPlanRequest{IdempotencyKey: digestBrainString("postgres-plan-key"), TenantID: "bonfire", RoomID: officeRoomID, StageNames: []string{"brain"}, AuthorizedBy: "aj@shareability.com", ApprovalReference: replayTestApprovalReference, RollbackFloor: replayTestRollbackFloor, ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	engine.Runner = &replayTestRunner{inputs: map[string][]AmbientReplayArtifact{}}
	execution, err := engine.Execute(ctx, manifest.Digest, manifest.AuthorizedBy)
	if err != nil || execution.Status != "completed" {
		t.Fatalf("execute=%+v err=%v", execution, err)
	}
	var cursorNamespace, receiptStatus, manifestStatus string
	var through uint64
	if err := canonical.pool.QueryRow(ctx, `SELECT cursor_namespace,through_source_sequence FROM ambient_intelligence_replay_cursors WHERE stage='brain'`).Scan(&cursorNamespace, &through); err != nil {
		t.Fatal(err)
	}
	if err := canonical.pool.QueryRow(ctx, `SELECT status FROM ambient_intelligence_replay_stage_receipts WHERE stage='brain'`).Scan(&receiptStatus); err != nil {
		t.Fatal(err)
	}
	if err := canonical.pool.QueryRow(ctx, `SELECT status FROM ambient_intelligence_replay_manifests`).Scan(&manifestStatus); err != nil {
		t.Fatal(err)
	}
	if cursorNamespace != "replay:"+manifest.Digest || through != manifest.EndAt || receiptStatus != "completed" || manifestStatus != "completed" {
		t.Fatalf("cursor=%s/%d receipt=%s manifest=%s", cursorNamespace, through, receiptStatus, manifestStatus)
	}
}

func TestPostgresAmbientReplayStorePlanRetryIgnoresGeneratedAt(t *testing.T) {
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	store := &PostgresAmbientReplayStore{pool: canonical.pool}
	now := time.Now().UTC().Truncate(time.Second)
	authority := &replayTestAuthority{snapshot: replayFixture(now.Add(-time.Hour), 2)}
	engine := &AmbientReplayEngine{Authority: authority, Store: store, Now: func() time.Time { return now }}
	request := AmbientReplayPlanRequest{IdempotencyKey: digestBrainString("postgres-stable-plan-key"), TenantID: "bonfire", RoomID: officeRoomID,
		AuthorizedBy: "aj@shareability.com", ApprovalReference: replayTestApprovalReference, RollbackFloor: replayTestRollbackFloor,
		ExpiresAt: now.Add(10 * time.Minute)}
	first, err := engine.Plan(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	request.ExpiresAt = now.Add(9 * time.Minute)
	retry, err := engine.Plan(ctx, request)
	if err != nil || retry.Digest != first.Digest || !retry.GeneratedAt.Equal(first.GeneratedAt) {
		t.Fatalf("retry=%+v first=%+v err=%v", retry, first, err)
	}
	if _, err := canonical.pool.Exec(ctx, `UPDATE ambient_intelligence_replay_manifests SET status='expired',completed_at=$2 WHERE manifest_digest=decode($1,'hex')`, first.Digest, now); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	request.ExpiresAt = now.Add(8 * time.Minute)
	terminalRetry, err := engine.Plan(ctx, request)
	if err != nil || terminalRetry.Digest != first.Digest {
		t.Fatalf("terminal retry=%+v err=%v", terminalRetry, err)
	}
}

func TestPostgresAmbientReplayStoreReclaimsLeaseAndFencesLateCursorWrite(t *testing.T) {
	ctx, canonical, _ := migratedPostgresCanonicalStore(t)
	store := &PostgresAmbientReplayStore{pool: canonical.pool}
	now := time.Now().UTC().Truncate(time.Second)
	authority := &replayTestAuthority{snapshot: replayFixture(now.Add(-time.Hour), 2)}
	engine := &AmbientReplayEngine{Authority: authority, Store: store, Now: func() time.Time { return now }}
	manifest, err := engine.Plan(ctx, AmbientReplayPlanRequest{IdempotencyKey: digestBrainString("postgres-lease-plan"), TenantID: "bonfire", RoomID: officeRoomID,
		StageNames: []string{"brain"}, AuthorizedBy: "aj@shareability.com", ApprovalReference: replayTestApprovalReference,
		RollbackFloor: replayTestRollbackFloor, ExpiresAt: now.Add(10 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	executionID := "55555555-5555-4555-8555-555555555555"
	if _, existing, err := store.BeginExecution(ctx, manifest, executionID, now); err != nil || existing {
		t.Fatalf("begin existing=%v err=%v", existing, err)
	}
	var prepared string
	if err := canonical.pool.QueryRow(ctx, `SELECT status FROM ambient_intelligence_replay_stage_receipts WHERE manifest_digest=decode($1,'hex')`, manifest.Digest).Scan(&prepared); err != nil || prepared != "prepared" {
		t.Fatalf("prepared=%q err=%v", prepared, err)
	}
	reclaimAt := now.Add(ambientReplayExecutionLease + time.Second)
	count, err := store.ReclaimExpired(ctx, reclaimAt)
	if err != nil || count != 1 {
		t.Fatalf("reclaim count=%d err=%v", count, err)
	}
	initial := make([]AmbientReplayArtifact, 0, len(manifest.Sources))
	for _, source := range manifest.Sources {
		initial = append(initial, AmbientReplayArtifact{ID: source.ObjectID, Kind: "transcript", Digest: source.ContentDigest,
			SourceManifestDigest: manifest.SourceManifestDigest, ManifestDigest: manifest.Digest})
	}
	inputDigest, _ := digestAmbientReplayArtifacts(initial)
	late := AmbientReplayStageReceipt{ManifestDigest: manifest.Digest, ExecutionID: executionID, Stage: "brain", Ordinal: 0,
		Status: "completed", InputDigest: inputDigest, OutputDigest: digestBrainString("late-provider-output"), SourceDigest: manifest.SourceManifestDigest,
		StartedAt: now, CompletedAt: reclaimAt}
	if err := store.RecordStageReceipt(ctx, late); !errors.Is(err, ErrAmbientReplayDrift) {
		t.Fatalf("late receipt err=%v", err)
	}
	var manifestStatus, receiptStatus, receiptError string
	var through uint64
	var outputSet bool
	if err := canonical.pool.QueryRow(ctx, `SELECT status,last_error_code FROM ambient_intelligence_replay_manifests WHERE manifest_digest=decode($1,'hex')`, manifest.Digest).Scan(&manifestStatus, &receiptError); err != nil {
		t.Fatal(err)
	}
	if err := canonical.pool.QueryRow(ctx, `SELECT status,error_code FROM ambient_intelligence_replay_stage_receipts WHERE manifest_digest=decode($1,'hex')`, manifest.Digest).Scan(&receiptStatus, &receiptError); err != nil {
		t.Fatal(err)
	}
	if err := canonical.pool.QueryRow(ctx, `SELECT through_source_sequence,output_digest IS NOT NULL FROM ambient_intelligence_replay_cursors WHERE manifest_digest=decode($1,'hex')`, manifest.Digest).Scan(&through, &outputSet); err != nil {
		t.Fatal(err)
	}
	if manifestStatus != "failed" || receiptStatus != "failed" || receiptError != "execution_lease_expired" || through != manifest.StartAfter || outputSet {
		t.Fatalf("manifest=%s receipt=%s/%s cursor=%d output=%v", manifestStatus, receiptStatus, receiptError, through, outputSet)
	}

	now = reclaimAt
	replacement, err := engine.Plan(ctx, AmbientReplayPlanRequest{IdempotencyKey: digestBrainString("postgres-after-lease"), TenantID: "bonfire", RoomID: officeRoomID,
		StageNames: []string{"brain"}, AuthorizedBy: "aj@shareability.com", ApprovalReference: replayTestApprovalReference,
		RollbackFloor: replayTestRollbackFloor, ExpiresAt: now.Add(5 * time.Minute)})
	if err != nil || replacement.Digest == manifest.Digest {
		t.Fatalf("replacement=%+v err=%v", replacement, err)
	}
}

func TestAmbientReplayHTTPIsAdminSessionAndSameOriginGated(t *testing.T) {
	for _, handler := range []http.HandlerFunc{ambientReplayPlanHandler, ambientReplayExecuteHandler} {
		get := httptest.NewRecorder()
		handler(get, httptest.NewRequest(http.MethodGet, "http://example.test/api/admin/ambient-intelligence-replay/plan", nil))
		if get.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET status=%d", get.Code)
		}

		anonymous := httptest.NewRecorder()
		handler(anonymous, httptest.NewRequest(http.MethodPost, "http://example.test/api/admin/ambient-intelligence-replay/plan", nil))
		if anonymous.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous status=%d", anonymous.Code)
		}

		crossOriginRequest := httptest.NewRequest(http.MethodPost, "http://example.test/api/admin/ambient-intelligence-replay/plan", nil)
		crossOriginRequest.Header.Set("Origin", "https://attacker.example")
		crossOrigin := httptest.NewRecorder()
		handler(crossOrigin, crossOriginRequest)
		if crossOrigin.Code != http.StatusForbidden {
			t.Fatalf("cross-origin status=%d", crossOrigin.Code)
		}
	}
}

func TestAmbientReplayTenantIsPinnedAndNarrativeRouteMatchesProvider(t *testing.T) {
	tenant, err := ambientReplayTenantForRequest("")
	if err != nil || tenant != canonicalTenantID() {
		t.Fatalf("empty tenant=%q err=%v", tenant, err)
	}
	if _, err := ambientReplayTenantForRequest("attacker-tenant"); !errors.Is(err, ErrAmbientReplayUnauthorized) {
		t.Fatalf("cross-tenant err=%v", err)
	}

	t.Setenv("ANTHROPIC_API_KEY", "")
	stages := ambientReplayDefaultStages()
	narrative := stages[3]
	if narrative.Name != "narrative" || narrative.Provider != providerOpenAI || narrative.Model != meetingBrainModel() {
		t.Fatalf("keyless narrative=%+v", narrative)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-never-used")
	stages = ambientReplayDefaultStages()
	narrative = stages[3]
	if narrative.Provider != providerAnthropic || narrative.Model != chatModel() || narrative.Model == meetingBrainModel() {
		t.Fatalf("anthropic narrative=%+v", narrative)
	}
}

func TestAmbientReplayRuntimeDefaultsOffAndCannotExecute(t *testing.T) {
	t.Setenv(ambientReplayModeEnv, "")
	status := configureAmbientReplayRuntime(nil)
	if status.Mode != "off" || status.Enabled || !status.Ready || !status.BoardExcluded || status.MaxSources != 48 {
		t.Fatalf("status=%+v", status)
	}
	if currentAmbientReplayEngine() != nil {
		t.Fatal("off runtime installed an engine")
	}
}
