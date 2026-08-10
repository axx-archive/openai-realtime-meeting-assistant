package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type stridePI0TestConsent struct {
	mu      sync.Mutex
	revoked bool
	calls   int
	held    atomic.Bool
}

func (c *stridePI0TestConsent) WithCurrentStridePI0Consent(_ context.Context, principal StridePI0Principal, refs []StridePI0Reference, effect func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.revoked || principal.validate() != nil || effect == nil {
		return ErrStridePI0Unavailable
	}
	for _, ref := range refs {
		if ref.Type != "consent" || ref.validate() != nil {
			return ErrStridePI0Unavailable
		}
	}
	c.held.Store(true)
	defer c.held.Store(false)
	return effect()
}

func stridePI0SyntheticTestPrincipal() StridePI0Principal {
	return StridePI0Principal{Kind: "human", PersonID: "synthetic_person_alpha", OrganizationID: "synthetic_organization_alpha", MembershipID: "synthetic_membership_alpha", MembershipRevision: 2, SessionSubjectDigest: stridePI0TestDigest("synthetic-session"), SessionRevision: 3}
}

func openStridePI0TestRuntime(t *testing.T, dir string, keys *stridePI0TestKeyring, authority *stridePI0TestCurrentAuthority, consent *stridePI0TestConsent, highWater StridePI0CarrierHighWaterStore) *StridePI0SyntheticRuntime {
	t.Helper()
	runtime, err := OpenStridePI0SyntheticRuntime(context.Background(), StridePI0SyntheticRuntimeConfig{
		Mode: StridePI0RuntimeSyntheticOnly, CarrierPath: filepath.Join(dir, "carrier.json"), GovernancePath: filepath.Join(dir, "governance.json"), Keys: keys,
		CarrierHighWater: highWater, GovernanceHighWater: highWater, Authority: authority, Consent: consent,
	})
	if err != nil {
		t.Fatalf("open synthetic runtime: %v", err)
	}
	return runtime
}

type stridePI0RuntimePathFailHighWater struct {
	base     *stridePI0TestHighWater
	failPath string
	failOnce bool
	observe  func(string) error
}

func (s *stridePI0RuntimePathFailHighWater) ReadStridePI0CarrierHighWater(ctx context.Context, path string) (StridePI0CarrierHighWater, error) {
	return s.base.ReadStridePI0CarrierHighWater(ctx, path)
}

func (s *stridePI0RuntimePathFailHighWater) CompareAndSwapStridePI0CarrierHighWater(ctx context.Context, path string, prior, next StridePI0CarrierHighWater) error {
	if s.observe != nil {
		if err := s.observe(path); err != nil {
			return err
		}
	}
	if err := s.base.CompareAndSwapStridePI0CarrierHighWater(ctx, path, prior, next); err != nil {
		return err
	}
	if s.failOnce && path == s.failPath {
		s.failOnce = false
		return errors.New("synthetic lost response after path commit")
	}
	return nil
}

func TestStridePI0SyntheticRuntimeOffParityAndFailClosedPreflight(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := OpenStridePI0SyntheticRuntime(ctx, StridePI0SyntheticRuntimeConfig{Mode: StridePI0RuntimeOff, CarrierPath: filepath.Join(dir, "must-not-exist")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Carrier(); !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("off carrier available: %v", err)
	}
	after, _ := os.ReadDir(dir)
	if len(before) != len(after) {
		t.Fatalf("off mode created bytes: before=%d after=%d", len(before), len(after))
	}

	valid := StridePI0SyntheticRuntimeConfig{Mode: StridePI0RuntimeSyntheticOnly, CarrierPath: filepath.Join(dir, "carrier.json"), GovernancePath: filepath.Join(dir, "governance.json")}
	for name, mutate := range map[string]func(*StridePI0SyntheticRuntimeConfig){
		"missing keys": func(c *StridePI0SyntheticRuntimeConfig) {},
		"missing authority": func(c *StridePI0SyntheticRuntimeConfig) {
			c.Keys = newStridePI0TestKeyring()
			c.CarrierHighWater = &stridePI0TestHighWater{values: map[string]StridePI0CarrierHighWater{}}
			c.GovernanceHighWater = c.CarrierHighWater
		},
		"relative path": func(c *StridePI0SyntheticRuntimeConfig) {
			c.Keys = newStridePI0TestKeyring()
			c.CarrierHighWater = &stridePI0TestHighWater{values: map[string]StridePI0CarrierHighWater{}}
			c.GovernanceHighWater = c.CarrierHighWater
			c.Authority = &stridePI0TestCurrentAuthority{}
			c.Consent = &stridePI0TestConsent{}
			c.CarrierPath = "relative"
		},
		"unknown mode": func(c *StridePI0SyntheticRuntimeConfig) { c.Mode = "enabled" },
	} {
		t.Run(name, func(t *testing.T) {
			config := valid
			mutate(&config)
			if _, err := OpenStridePI0SyntheticRuntime(ctx, config); err == nil {
				t.Fatal("unsafe configuration admitted")
			}
		})
	}
}

func stridePI0PrepareSyntheticAppend(t *testing.T, runtime *StridePI0SyntheticRuntime, keys *stridePI0TestKeyring, authority *stridePI0TestCurrentAuthority, at time.Time) (StridePI0Principal, StridePI0Postimage, StridePI0LifecycleEvent, StridePI0CompoundJournal) {
	return stridePI0PrepareSyntheticAppendFor(t, runtime, keys, authority, stridePI0SyntheticTestPrincipal(), "event_synthetic_effect", "synthetic_operation_alpha", "synthetic_trace_alpha", "organization", at)
}

func stridePI0PrepareSyntheticAppendFor(t *testing.T, runtime *StridePI0SyntheticRuntime, keys *stridePI0TestKeyring, authority *stridePI0TestCurrentAuthority, principal StridePI0Principal, eventID, operationID, traceID, visibility string, at time.Time) (StridePI0Principal, StridePI0Postimage, StridePI0LifecycleEvent, StridePI0CompoundJournal) {
	t.Helper()
	ctx := context.Background()
	event := stridePI0TestEvent(at, eventID, "effect.applied", "event_parent", "effect", "effect_"+operationID, 1)
	event.TenantID, event.Principal = principal.OrganizationID, principal
	event.Audience.Visibility = visibility
	if visibility == "public" {
		event.Audience.PrincipalIDs = nil
	} else {
		event.Audience.PrincipalIDs = []string{principal.OrganizationID}
	}
	event.TraceID = traceID
	event.JournalOperationID = operationID
	event = sealStridePI0TestEvent(t, keys, event)
	descriptor, err := stridePI0EventDescriptor(event)
	if err != nil {
		t.Fatal(err)
	}
	draft := stridePI0TestJournal(at)
	draft.OperationID, draft.TenantID, draft.TraceID, draft.Principal, draft.Aggregate = operationID, event.TenantID, event.TraceID, principal, event.Aggregate
	draft.OperationFingerprint = stridePI0TestDigest(operationID)
	draft.RequestedEvents = []StridePI0JournalEvent{descriptor}
	draft.AdapterOperationID, draft.ExpectedEffectReceiptID = operationID+"_adapter", operationID+"_effect_receipt"
	journal, err := PrepareStridePI0CompoundJournal(ctx, keys, authority, draft)
	if err != nil || runtime.carrier.CreateJournal(ctx, journal) != nil {
		t.Fatalf("prepare journal: %v", err)
	}
	for i, phase := range []string{"effect_requested", "effect_approved"} {
		next, transitionErr := TransitionStridePI0CompoundJournal(ctx, keys, authority, journal, phase, nil, "", at.Add(time.Duration(i+1)*time.Second))
		if transitionErr != nil || runtime.carrier.CompareAndSwapJournal(ctx, journal, next) != nil {
			t.Fatalf("phase %s: %v", phase, transitionErr)
		}
		journal = next
	}
	evidence := &stridePI0TestEvidenceKeyring{key: StridePI0EffectEvidenceKey{ID: "pi0_evidence_key", Version: 4, Secret: []byte(strings.Repeat("e", 32))}}
	effectReceipt := stridePI0TestEffectReceipt(t, evidence, journal, journal.ExpectedPostimages, "applied", at.Add(3*time.Second))
	applied, err := RecordStridePI0EffectReceipt(ctx, keys, evidence, authority, journal, effectReceipt, at.Add(3*time.Second))
	if err != nil || runtime.carrier.CompareAndSwapJournal(ctx, journal, applied) != nil {
		t.Fatalf("persist effect: %v", err)
	}
	fence := StridePI0Postimage{Store: "pi0_fence_store", Type: "journal", ID: operationID + "_fence", Revision: 1, Digest: stridePI0TestDigest(operationID + "_fence"), HighWater: 1}
	return principal, fence, event, applied
}

func TestStridePI0SyntheticRuntimeAppendRestartConsentAndCAS(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	keys, authority, consent := newStridePI0TestKeyring(), &stridePI0TestCurrentAuthority{}, &stridePI0TestConsent{}
	highWater := &stridePI0TestHighWater{values: map[string]StridePI0CarrierHighWater{}}
	runtime := openStridePI0TestRuntime(t, dir, keys, authority, consent, highWater)
	at := time.Date(2026, 8, 9, 21, 0, 0, 0, time.UTC)
	principal, fence, event, applied := stridePI0PrepareSyntheticAppend(t, runtime, keys, authority, at)
	receipt, err := runtime.Append(ctx, principal, applied.OperationID, applied.OperationFingerprint, fence, []StridePI0LifecycleEvent{event}, at.Add(4*time.Second))
	if err != nil || VerifyStridePI0EventAppendReceipt(ctx, keys, receipt) != nil {
		t.Fatalf("append: %v", err)
	}
	restarted := openStridePI0TestRuntime(t, dir, keys, authority, consent, highWater)
	replayed, err := restarted.Append(ctx, principal, applied.OperationID, applied.OperationFingerprint, fence, []StridePI0LifecycleEvent{event}, at.Add(4*time.Second))
	if err != nil || replayed.ReceiptID != receipt.ReceiptID {
		t.Fatalf("restart replay: %v", err)
	}

	before, _ := os.ReadFile(restarted.carrier.path)
	consent.revoked = true
	if _, err := restarted.Append(ctx, principal, applied.OperationID, applied.OperationFingerprint, fence, []StridePI0LifecycleEvent{event}, at.Add(4*time.Second)); !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("revoked consent admitted: %v", err)
	}
	after, _ := os.ReadFile(restarted.carrier.path)
	if !bytes.Equal(before, after) {
		t.Fatal("revoked append changed carrier")
	}
	nonSynthetic := principal
	nonSynthetic.PersonID = "person_real"
	if _, err := restarted.Append(ctx, nonSynthetic, applied.OperationID, applied.OperationFingerprint, fence, []StridePI0LifecycleEvent{event}, at.Add(4*time.Second)); !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("real principal admitted: %v", err)
	}
}

func TestStridePI0SyntheticRuntimeRightsRetentionBackupAndBaseline(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	keys, authority, consent := newStridePI0TestKeyring(), &stridePI0TestCurrentAuthority{}, &stridePI0TestConsent{}
	highWater := &stridePI0TestHighWater{values: map[string]StridePI0CarrierHighWater{}}
	runtime := openStridePI0TestRuntime(t, dir, keys, authority, consent, highWater)
	at := time.Date(2026, 8, 9, 22, 0, 0, 0, time.UTC)
	principal, fence, event, applied := stridePI0PrepareSyntheticAppend(t, runtime, keys, authority, at)
	if _, err := runtime.Append(ctx, principal, applied.OperationID, applied.OperationFingerprint, fence, []StridePI0LifecycleEvent{event}, at.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	consentRefs := event.ConsentRefs

	ref := StridePI0Reference(event.Aggregate)
	export, err := runtime.CreateExport(ctx, principal, consentRefs, "synthetic_export_alpha", "person", principal.PersonID, []StridePI0Reference{ref}, at.Add(5*time.Second), at.Add(6*24*time.Hour))
	if err != nil || export.MAC == "" {
		t.Fatalf("export: %v", err)
	}
	if _, err := runtime.CreateExport(ctx, principal, consentRefs, "synthetic_export_long", "person", principal.PersonID, []StridePI0Reference{ref}, at, at.Add(8*24*time.Hour)); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("long export admitted: %v", err)
	}
	replayedExport, err := runtime.CreateExport(ctx, principal, consentRefs, "synthetic_export_alpha", "person", principal.PersonID, []StridePI0Reference{ref}, at.Add(5*time.Second), at.Add(6*24*time.Hour))
	leftExport, _ := canonicalJSON(replayedExport)
	rightExport, _ := canonicalJSON(export)
	if err != nil || !bytes.Equal(leftExport, rightExport) {
		t.Fatalf("exact export replay changed identity: %#v %v", replayedExport, err)
	}
	if _, err := runtime.CreateExport(ctx, principal, consentRefs, "synthetic_export_alpha", "person", principal.PersonID, []StridePI0Reference{ref}, at.Add(5*time.Second), at.Add(5*24*time.Hour)); !errors.Is(err, ErrStridePI0Conflict) {
		t.Fatalf("export id collision admitted: %v", err)
	}
	backup, err := runtime.CreateBackupManifest(ctx, "synthetic_backup_before_delete", at.Add(6*time.Second))
	if err != nil || runtime.VerifyBackupForRestore(ctx, backup) != nil {
		t.Fatalf("backup: %v", err)
	}
	receipt, err := runtime.DeleteScope(ctx, principal, consentRefs, "subject", principal.PersonID, "synthetic_purge_alpha", at.Add(7*time.Second))
	if err != nil || receipt.Generation < 2 || receipt.EventCount != 1 || receipt.ExportCount != 1 || receipt.JournalCount != 1 || receipt.DerivedCount != 1 || receipt.IndexCount != 0 {
		t.Fatalf("delete: %#v %v", receipt, err)
	}
	carrierState, err := runtime.carrier.readLocked(ctx)
	if err != nil || len(carrierState.Events) != 0 || len(carrierState.Journals) != 0 || len(carrierState.AppendReceipts) != 0 {
		t.Fatalf("deleted scope remains readable in carrier: %#v %v", carrierState, err)
	}
	governanceState, err := runtime.governance.read(ctx)
	if err != nil || len(governanceState.Exports) != 0 || len(governanceState.Tombstones) != 1 || len(governanceState.PurgeReceipts) != 1 {
		t.Fatalf("deleted export/tombstone state: %#v %v", governanceState, err)
	}
	if _, err := runtime.Append(ctx, principal, applied.OperationID, applied.OperationFingerprint, fence, []StridePI0LifecycleEvent{event}, at.Add(8*time.Second)); !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("subject tombstone did not suppress carrier replay: %v", err)
	}
	if err := runtime.VerifyBackupForRestore(ctx, backup); !errors.Is(err, ErrStridePI0Conflict) {
		t.Fatalf("old backup resurrected: %v", err)
	}
	if _, err := runtime.CreateExport(ctx, principal, consentRefs, "synthetic_export_after_delete", "person", principal.PersonID, []StridePI0Reference{ref}, at, at.Add(time.Hour)); !errors.Is(err, ErrStridePI0Conflict) {
		t.Fatalf("tombstoned export admitted: %v", err)
	}

	candidates, err := runtime.RetentionCandidates(ctx, at.Add(800*24*time.Hour))
	if err != nil || len(candidates) != 0 {
		t.Fatalf("retention candidates: %v %v", candidates, err)
	}
	d := stridePI0TestDigest
	baseline, err := runtime.MintSyntheticBaseline(ctx, "synthetic_baseline_alpha", d("release"), d("schema"), d("policy"), d("switch"), map[string]int64{"eligible": 1, "linked": 1, "unknown": 0}, 1, 1, 0, at)
	if err != nil || baseline.EvidenceClass != "synthetic_deterministic_only_not_real_baseline" || baseline.MAC == "" {
		t.Fatalf("baseline: %#v %v", baseline, err)
	}
	if _, err := runtime.MintSyntheticBaseline(ctx, "synthetic_baseline_false", d("release"), d("schema"), d("policy"), d("switch"), nil, 1, 2, 0, at); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("false baseline admitted: %v", err)
	}
	if _, err := runtime.MintSyntheticBaseline(ctx, "synthetic_baseline_mismatch", d("release"), d("schema"), d("policy"), d("switch"), map[string]int64{"eligible": 2, "linked": 1, "unknown": 0}, 1, 1, 0, at); !errors.Is(err, ErrStridePI0Invalid) {
		t.Fatalf("inconsistent count map admitted: %v", err)
	}
}

func TestStridePI0SyntheticRuntimeRetentionApplyHeldAndRestartSafe(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	keys, authority, consent := newStridePI0TestKeyring(), &stridePI0TestCurrentAuthority{}, &stridePI0TestConsent{}
	baseHighWater := &stridePI0TestHighWater{values: map[string]StridePI0CarrierHighWater{}}
	highWater := &stridePI0RuntimePathFailHighWater{base: baseHighWater}
	runtime := openStridePI0TestRuntime(t, dir, keys, authority, consent, highWater)
	at := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	principal, fence, event, applied := stridePI0PrepareSyntheticAppend(t, runtime, keys, authority, at)
	if _, err := runtime.Append(ctx, principal, applied.OperationID, applied.OperationFingerprint, fence, []StridePI0LifecycleEvent{event}, at.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	ref := StridePI0Reference(event.Aggregate)
	if _, err := runtime.CreateExport(ctx, principal, event.ConsentRefs, "synthetic_retention_export", "person", principal.PersonID, []StridePI0Reference{ref}, at.Add(5*time.Second), at.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	applyAt := at.Add(800 * 24 * time.Hour)
	candidates, err := runtime.RetentionCandidates(ctx, applyAt)
	if err != nil || len(candidates) != 1 || candidates[0] != event.EventID {
		t.Fatalf("retention candidates: %v %v", candidates, err)
	}
	beforeCarrier, _ := os.ReadFile(runtime.carrier.path)
	consent.revoked = true
	if _, err := runtime.ApplyRetention(ctx, principal, event.ConsentRefs, "synthetic_retention_apply", applyAt); !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("revoked retention admitted: %v", err)
	}
	afterDenied, _ := os.ReadFile(runtime.carrier.path)
	if !bytes.Equal(beforeCarrier, afterDenied) {
		t.Fatal("denied retention changed carrier")
	}
	consent.revoked = false
	heldChecks := 0
	highWater.observe = func(string) error {
		heldChecks++
		if !authority.held.Load() || !consent.held.Load() {
			return errors.New("retention final write escaped held authority or consent")
		}
		return nil
	}
	backup, err := runtime.CreateBackupManifest(ctx, "synthetic_retention_backup", applyAt.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := runtime.ApplyRetention(ctx, principal, event.ConsentRefs, "synthetic_retention_apply", applyAt)
	if err != nil || receipt.Scope != "retention" || receipt.EventCount != 1 || receipt.ExportCount != 1 || receipt.JournalCount != 1 || receipt.DerivedCount != 1 || receipt.IndexCount != 0 {
		t.Fatalf("retention receipt: %#v %v", receipt, err)
	}
	if heldChecks < 3 {
		t.Fatalf("retention held checks=%d, want tombstone+carrier+receipt commits", heldChecks)
	}
	highWater.observe = nil
	restarted := openStridePI0TestRuntime(t, dir, keys, authority, consent, highWater)
	replayed, err := restarted.ApplyRetention(ctx, principal, event.ConsentRefs, "synthetic_retention_apply", applyAt)
	left, _ := canonicalJSON(receipt)
	right, _ := canonicalJSON(replayed)
	if err != nil || !bytes.Equal(left, right) {
		t.Fatalf("retention replay: %#v %v", replayed, err)
	}
	if _, err := restarted.ApplyRetention(ctx, principal, event.ConsentRefs, "synthetic_retention_apply", applyAt.Add(time.Second)); !errors.Is(err, ErrStridePI0Conflict) {
		t.Fatalf("retention id collision admitted: %v", err)
	}
	state, err := restarted.carrier.readLocked(ctx)
	if err != nil || len(state.Events) != 0 || len(state.Journals) != 0 || len(state.AppendReceipts) != 0 {
		t.Fatalf("retained carrier rows readable: %#v %v", state, err)
	}
	if err := restarted.VerifyBackupForRestore(ctx, backup); !errors.Is(err, ErrStridePI0Conflict) {
		t.Fatalf("pre-retention backup admitted: %v", err)
	}
	if _, err := restarted.Append(ctx, principal, applied.OperationID, applied.OperationFingerprint, fence, []StridePI0LifecycleEvent{event}, applyAt.Add(time.Second)); !errors.Is(err, ErrStridePI0Conflict) {
		t.Fatalf("expired event resurrected after retention: %v", err)
	}
}

func TestStridePI0SyntheticRetentionResumesAfterCarrierCommitLostResponse(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	keys, authority, consent := newStridePI0TestKeyring(), &stridePI0TestCurrentAuthority{}, &stridePI0TestConsent{}
	base := &stridePI0TestHighWater{values: map[string]StridePI0CarrierHighWater{}}
	highWater := &stridePI0RuntimePathFailHighWater{base: base}
	runtime := openStridePI0TestRuntime(t, dir, keys, authority, consent, highWater)
	at := time.Date(2026, 8, 10, 1, 30, 0, 0, time.UTC)
	principal, fence, event, applied := stridePI0PrepareSyntheticAppend(t, runtime, keys, authority, at)
	if _, err := runtime.Append(ctx, principal, applied.OperationID, applied.OperationFingerprint, fence, []StridePI0LifecycleEvent{event}, at.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	highWater.failPath, highWater.failOnce = runtime.carrier.path, true
	applyAt := at.Add(800 * 24 * time.Hour)
	if _, err := runtime.ApplyRetention(ctx, principal, event.ConsentRefs, "synthetic_retention_crash", applyAt); err == nil {
		t.Fatal("carrier lost response not surfaced")
	}
	governance, err := runtime.governance.read(ctx)
	if err != nil || len(governance.Tombstones) != 1 || len(governance.PurgeReceipts) != 0 {
		t.Fatalf("pending retention tombstone not durable: %#v %v", governance, err)
	}
	restarted := openStridePI0TestRuntime(t, dir, keys, authority, consent, highWater)
	receipt, err := restarted.ApplyRetention(ctx, principal, event.ConsentRefs, "synthetic_retention_crash", applyAt)
	if err != nil || receipt.EventCount != 1 || receipt.JournalCount != 1 || receipt.DerivedCount != 1 {
		t.Fatalf("retention recovery lost measured counts: %#v %v", receipt, err)
	}
	carrier, err := restarted.carrier.readLocked(ctx)
	if err != nil || len(carrier.Events)+len(carrier.Journals)+len(carrier.AppendReceipts) != 0 {
		t.Fatalf("carrier rows survived restart recovery: %#v %v", carrier, err)
	}
}

func TestStridePI0SyntheticTraceDeletePendingTombstoneBlocksExportAndResumes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	keys, authority, consent := newStridePI0TestKeyring(), &stridePI0TestCurrentAuthority{}, &stridePI0TestConsent{}
	highWater := &stridePI0TestHighWater{values: map[string]StridePI0CarrierHighWater{}}
	runtime := openStridePI0TestRuntime(t, dir, keys, authority, consent, highWater)
	at := time.Date(2026, 8, 10, 1, 45, 0, 0, time.UTC)
	principal, fence, event, applied := stridePI0PrepareSyntheticAppend(t, runtime, keys, authority, at)
	if _, err := runtime.Append(ctx, principal, applied.OperationID, applied.OperationFingerprint, fence, []StridePI0LifecycleEvent{event}, at.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	ref := StridePI0Reference(event.Aggregate)
	if _, err := runtime.CreateExport(ctx, principal, event.ConsentRefs, "synthetic_trace_export_before_delete", "person", principal.PersonID, []StridePI0Reference{ref}, at.Add(5*time.Second), at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	plan, err := runtime.carrierPurgePlan(ctx,
		func(candidate StridePI0LifecycleEvent) bool {
			return candidate.TenantID == principal.OrganizationID && candidate.TraceID == event.TraceID
		},
		func(candidate StridePI0CompoundJournal) bool {
			return candidate.TenantID == principal.OrganizationID && candidate.TraceID == event.TraceID
		})
	if err != nil {
		t.Fatal(err)
	}
	commitment, err := runtime.scopeCommitment(ctx, "trace", event.TraceID)
	if err != nil {
		t.Fatal(err)
	}
	const receiptID = "synthetic_trace_pending_delete"
	if err := runtime.governance.mutate(ctx, func(state *stridePI0SyntheticGovernanceState) error {
		state.Exports = nil
		state.Tombstones = append(state.Tombstones, StridePI0SyntheticTombstone{
			Scope: "trace", ScopeCommitment: commitment, ReceiptID: receiptID,
			Generation: state.HighWater + 1, DeletedAt: at.Add(6 * time.Second),
			SuppressedEvents: append([]string(nil), plan.eventIDs...), SuppressedEventRefs: append([]StridePI0Reference(nil), plan.eventRefs...),
			SuppressedOps: append([]string(nil), plan.operationIDs...), EventCount: plan.eventCount,
			ExportCount: 1, JournalCount: plan.journalCount, DerivedCount: plan.derivedCount,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	carrierBefore, _ := os.ReadFile(runtime.carrier.path)
	governanceBefore, _ := os.ReadFile(runtime.governance.path)
	if _, err := runtime.CreateExport(ctx, principal, event.ConsentRefs, "synthetic_trace_export_during_delete", "person", principal.PersonID, []StridePI0Reference{ref}, at.Add(7*time.Second), at.Add(time.Hour)); !errors.Is(err, ErrStridePI0Conflict) {
		t.Fatalf("pending trace tombstone export admitted: %v", err)
	}
	carrierAfter, _ := os.ReadFile(runtime.carrier.path)
	governanceAfter, _ := os.ReadFile(runtime.governance.path)
	if !bytes.Equal(carrierBefore, carrierAfter) || !bytes.Equal(governanceBefore, governanceAfter) {
		t.Fatal("denied pending-tombstone export changed durable state")
	}
	receipt, err := runtime.DeleteScope(ctx, principal, event.ConsentRefs, "trace", event.TraceID, receiptID, at.Add(6*time.Second))
	if err != nil || receipt.EventCount != 1 || receipt.ExportCount != 1 || receipt.JournalCount != 1 || receipt.DerivedCount != 1 {
		t.Fatalf("pending trace deletion did not resume exactly: %#v %v", receipt, err)
	}
	restarted := openStridePI0TestRuntime(t, dir, keys, authority, consent, highWater)
	if _, err := restarted.CreateExport(ctx, principal, event.ConsentRefs, "synthetic_trace_export_after_restart", "person", principal.PersonID, []StridePI0Reference{ref}, at.Add(8*time.Second), at.Add(time.Hour)); !errors.Is(err, ErrStridePI0Conflict) {
		t.Fatalf("completed trace tombstone export admitted after restart: %v", err)
	}
}

func TestStridePI0SyntheticRetentionFinalSweepAndReceiptRecoverAtomically(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	keys, authority, consent := newStridePI0TestKeyring(), &stridePI0TestCurrentAuthority{}, &stridePI0TestConsent{}
	base := &stridePI0TestHighWater{values: map[string]StridePI0CarrierHighWater{}}
	highWater := &stridePI0RuntimePathFailHighWater{base: base}
	runtime := openStridePI0TestRuntime(t, dir, keys, authority, consent, highWater)
	at := time.Date(2026, 8, 10, 1, 55, 0, 0, time.UTC)
	principal, fence, event, applied := stridePI0PrepareSyntheticAppend(t, runtime, keys, authority, at)
	if _, err := runtime.Append(ctx, principal, applied.OperationID, applied.OperationFingerprint, fence, []StridePI0LifecycleEvent{event}, at.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	ref := StridePI0Reference(event.Aggregate)
	if _, err := runtime.CreateExport(ctx, principal, event.ConsentRefs, "synthetic_retention_interleaved_export", "person", principal.PersonID, []StridePI0Reference{ref}, at.Add(5*time.Second), at.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	applyAt := at.Add(800 * 24 * time.Hour)
	const receiptID = "synthetic_retention_atomic_completion"
	commitment, err := MintStridePI0ManagedCommitment(ctx, keys, stridePI0IdempotencyDomain, "synthetic-retention", principal.OrganizationID, principal.PersonID, receiptID, applyAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := runtime.carrierPurgePlan(ctx,
		func(candidate StridePI0LifecycleEvent) bool {
			return candidate.Principal == principal && candidate.TenantID == principal.OrganizationID && !candidate.Retention.RetainUntil.After(applyAt)
		},
		func(StridePI0CompoundJournal) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	// Model the exact pre-fix crash/interleaving postimage: the pending
	// tombstone is durable, while a covered export is still present and has not
	// yet been included in its measured count.
	if err := runtime.governance.mutate(ctx, func(state *stridePI0SyntheticGovernanceState) error {
		state.Tombstones = append(state.Tombstones, StridePI0SyntheticTombstone{
			Scope: "retention", ScopeCommitment: commitment, ReceiptID: receiptID,
			Generation: state.HighWater + 1, DeletedAt: applyAt,
			SuppressedEvents: append([]string(nil), plan.eventIDs...), SuppressedEventRefs: append([]StridePI0Reference(nil), plan.eventRefs...),
			SuppressedOps: append([]string(nil), plan.operationIDs...), EventCount: plan.eventCount,
			JournalCount: plan.journalCount, DerivedCount: plan.derivedCount,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	highWater.failPath, highWater.failOnce = runtime.governance.path, true
	if _, err := runtime.ApplyRetention(ctx, principal, event.ConsentRefs, receiptID, applyAt); err == nil {
		t.Fatal("lost final governance response not surfaced")
	}
	restarted := openStridePI0TestRuntime(t, dir, keys, authority, consent, highWater)
	state, err := restarted.governance.read(ctx)
	if err != nil || len(state.Exports) != 0 || len(state.Tombstones) != 1 || len(state.PurgeReceipts) != 1 {
		t.Fatalf("atomic final sweep/receipt not recovered: %#v %v", state, err)
	}
	if len(state.Tombstones[0].SuppressedEventRefs) != 1 || state.Tombstones[0].SuppressedEventRefs[0] != ref || state.Tombstones[0].ExportCount != 1 || state.PurgeReceipts[0].ExportCount != 1 {
		t.Fatalf("recovered measured tombstone/receipt mismatch: %#v %#v", state.Tombstones[0], state.PurgeReceipts[0])
	}
	carrier, err := restarted.carrier.readLocked(ctx)
	if err != nil || len(carrier.Events)+len(carrier.Journals)+len(carrier.AppendReceipts) != 0 {
		t.Fatalf("retained carrier rows survived atomic recovery: %#v %v", carrier, err)
	}
	replayed, err := restarted.ApplyRetention(ctx, principal, event.ConsentRefs, receiptID, applyAt)
	left, _ := canonicalJSON(replayed)
	right, _ := canonicalJSON(state.PurgeReceipts[0])
	if err != nil || !bytes.Equal(left, right) {
		t.Fatalf("recovered retention completion did not replay exactly: %#v %v", replayed, err)
	}
	if _, err := restarted.CreateExport(ctx, principal, event.ConsentRefs, "synthetic_retention_export_after_restart", "person", principal.PersonID, []StridePI0Reference{ref}, applyAt.Add(time.Second), applyAt.Add(time.Hour)); !errors.Is(err, ErrStridePI0Conflict) {
		t.Fatalf("retention-covered export admitted after completion: %v", err)
	}
	if _, err := restarted.Append(ctx, principal, applied.OperationID, applied.OperationFingerprint, fence, []StridePI0LifecycleEvent{event}, at.Add(5*time.Second)); !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("retention-covered event resurrected with a pre-expiry clock: %v", err)
	}
}

func TestStridePI0SyntheticRuntimePublicExportIsTenantBounded(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	keys, authority, consent := newStridePI0TestKeyring(), &stridePI0TestCurrentAuthority{}, &stridePI0TestConsent{}
	highWater := &stridePI0TestHighWater{values: map[string]StridePI0CarrierHighWater{}}
	runtime := openStridePI0TestRuntime(t, dir, keys, authority, consent, highWater)
	at := time.Date(2026, 8, 10, 2, 0, 0, 0, time.UTC)
	principal, fence, event, applied := stridePI0PrepareSyntheticAppendFor(t, runtime, keys, authority, stridePI0SyntheticTestPrincipal(), "event_public_alpha", "synthetic_public_operation_alpha", "synthetic_public_trace_alpha", "public", at)
	if _, err := runtime.Append(ctx, principal, applied.OperationID, applied.OperationFingerprint, fence, []StridePI0LifecycleEvent{event}, at.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	ref := StridePI0Reference(event.Aggregate)
	if _, err := runtime.CreateExport(ctx, principal, event.ConsentRefs, "synthetic_public_export", "public", principal.OrganizationID, []StridePI0Reference{ref}, at.Add(5*time.Second), at.Add(time.Hour)); err != nil {
		t.Fatalf("own public export: %v", err)
	}
	foreign := principal
	foreign.PersonID, foreign.OrganizationID, foreign.MembershipID = "synthetic_person_foreign", "synthetic_organization_foreign", "synthetic_membership_foreign"
	foreign.SessionSubjectDigest = stridePI0TestDigest("synthetic-foreign-session")
	foreignPrincipal, foreignFence, foreignEvent, foreignApplied := stridePI0PrepareSyntheticAppendFor(t, runtime, keys, authority, foreign, "event_public_foreign", "synthetic_public_operation_foreign", "synthetic_public_trace_foreign", "public", at.Add(time.Minute))
	if _, err := runtime.Append(ctx, foreignPrincipal, foreignApplied.OperationID, foreignApplied.OperationFingerprint, foreignFence, []StridePI0LifecycleEvent{foreignEvent}, at.Add(time.Minute+4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.CreateExport(ctx, principal, event.ConsentRefs, "synthetic_cross_tenant_export", "public", principal.OrganizationID, []StridePI0Reference{StridePI0Reference(foreignEvent.Aggregate)}, at.Add(2*time.Minute), at.Add(3*time.Minute)); !errors.Is(err, ErrStridePI0Unavailable) {
		t.Fatalf("cross-tenant public export admitted: %v", err)
	}
}

func TestStridePI0SyntheticRuntimeGovernanceFailureNeverTouchesCarrier(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	carrierPath := filepath.Join(dir, "carrier.json")
	governancePath := filepath.Join(dir, "governance.json")
	if err := os.WriteFile(governancePath, []byte(`{"schema":"invalid"}`), 0600); err != nil {
		t.Fatal(err)
	}
	keys := newStridePI0TestKeyring()
	highWater := &stridePI0TestHighWater{values: map[string]StridePI0CarrierHighWater{}}
	_, err := OpenStridePI0SyntheticRuntime(ctx, StridePI0SyntheticRuntimeConfig{Mode: StridePI0RuntimeSyntheticOnly, CarrierPath: carrierPath, GovernancePath: governancePath, Keys: keys, CarrierHighWater: highWater, GovernanceHighWater: highWater, Authority: &stridePI0TestCurrentAuthority{}, Consent: &stridePI0TestConsent{}})
	if err == nil {
		t.Fatal("invalid governance admitted")
	}
	for _, path := range []string{carrierPath, carrierPath + ".lock", carrierPath + ".txn"} {
		if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("governance failure touched carrier path %s: %v", path, statErr)
		}
	}
}

func TestStridePI0SyntheticGovernanceCrashRecoveryAndConcurrentCAS(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	keys, authority, consent := newStridePI0TestKeyring(), &stridePI0TestCurrentAuthority{}, &stridePI0TestConsent{}
	highWater := &stridePI0TestHighWater{values: map[string]StridePI0CarrierHighWater{}}
	runtime := openStridePI0TestRuntime(t, dir, keys, authority, consent, highWater)
	at := time.Date(2026, 8, 9, 23, 0, 0, 0, time.UTC)
	principal := stridePI0SyntheticTestPrincipal()
	refs := []StridePI0Reference{stridePI0TestRef("consent", "consent_alpha", 2)}
	highWater.failAfterCommit = true
	if _, err := runtime.DeleteScope(ctx, principal, refs, "trace", "synthetic_trace_crash", "synthetic_purge_crash", at); err == nil {
		t.Fatal("lost response not surfaced")
	}
	restarted := openStridePI0TestRuntime(t, dir, keys, authority, consent, highWater)
	state, err := restarted.governance.read(ctx)
	if err != nil || len(state.Tombstones) != 1 || len(state.PurgeReceipts) != 0 {
		t.Fatalf("recover transaction: %#v %v", state, err)
	}
	replayed, err := restarted.DeleteScope(ctx, principal, refs, "trace", "synthetic_trace_crash", "synthetic_purge_crash", at)
	if err != nil || replayed.ReceiptID != "synthetic_purge_crash" || replayed.Generation != state.Tombstones[0].Generation {
		t.Fatalf("lost-response replay was not idempotent: %#v %v", replayed, err)
	}

	// Two independently opened stores serialize under the durable file lock;
	// both distinct operations commit without either overwriting the other.
	other := openStridePI0TestRuntime(t, dir, keys, authority, consent, highWater)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i, candidate := range []*StridePI0SyntheticRuntime{restarted, other} {
		wg.Add(1)
		go func(index int, runtime *StridePI0SyntheticRuntime) {
			defer wg.Done()
			// No carrier event exists in this crash-only fixture, so exercise
			// governance CAS directly with two distinct body-free receipts.
			err := runtime.governance.mutate(ctx, func(state *stridePI0SyntheticGovernanceState) error {
				state.PurgeReceipts = append(state.PurgeReceipts, StridePI0SyntheticPurgeReceipt{ReceiptID: "synthetic_concurrent_" + string(rune('a'+index)), Scope: "trace", Generation: state.HighWater + 1, EventCount: 1, ExportCount: 1, IndexCount: 1, JournalCount: 1, DerivedCount: 1, CompletedAt: at})
				return nil
			})
			results <- err
		}(i, candidate)
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 2 {
		t.Fatalf("expected two serialized CAS commits, got %d", successes)
	}
	final := openStridePI0TestRuntime(t, dir, keys, authority, consent, highWater)
	finalState, err := final.governance.read(ctx)
	if err != nil || len(finalState.PurgeReceipts) != 3 {
		t.Fatalf("winner not durable: %v", err)
	}
}
