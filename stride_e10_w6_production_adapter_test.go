package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type strideE10W6TestManagedProvider struct {
	mu          sync.Mutex
	adapters    StrideE10W6ManagedProductionAdapters
	attestation StrideE10W6ManagedProductionAttestation
	err         error
	hook        func()
	calls       int
}

func (p *strideE10W6TestManagedProvider) PreflightStrideE10W6ManagedProduction(_ context.Context, _ StrideE10W6ManagedProductionExpectation) (StrideE10W6ManagedProductionAdapters, StrideE10W6ManagedProductionAttestation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.hook != nil {
		p.hook()
	}
	return p.adapters, p.attestation, p.err
}

func (p *strideE10W6TestManagedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func isolateStrideE10W6ProductionAdapterTest(t *testing.T) {
	t.Helper()
	priorLive := strideE10LiveProductRuntime
	priorReadiness := strideE10W6RuntimeReadinessSnapshot()
	priorClock := strideE10W6ProductionClock
	priorInterval := strideE10W6ProductionWorkerInterval
	priorProvider := strideE10W6ManagedProductionProvider()
	closeStrideE10W6ProductionRuntime()
	t.Cleanup(func() {
		closeStrideE10W6ProductionRuntime()
		strideE10LiveProductRuntime = priorLive
		publishStrideE10W6RuntimeReadiness(priorReadiness)
		strideE10W6ProductionClock = priorClock
		strideE10W6ProductionWorkerInterval = priorInterval
		strideE10W6ManagedProviderState.Lock()
		strideE10W6ManagedProviderState.provider = priorProvider
		strideE10W6ManagedProviderState.Unlock()
	})
	isolateStrideE10W4ReadinessForTest(t)
}

func strideE10W6ProductionExpectation(t *testing.T, fixture strideE10W6RuntimeFixture) StrideE10W6ManagedProductionExpectation {
	t.Helper()
	t.Cleanup(closeStrideE10W6ProductionRuntime)
	release := configureQualifiedReleaseIdentity(t)
	canonical := func(path string) string {
		value, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	expectation := StrideE10W6ManagedProductionExpectation{
		AdapterID: "managed_w6_adapter", KeyID: fixture.config.Key.ID, KeyVersion: fixture.config.Key.Version, MinimumKeyVersion: fixture.config.MinimumKeyVersion,
		PolicyPath: canonical(fixture.config.PolicyPath), QualificationPath: canonical(fixture.config.QualificationPath), BindingPath: canonical(fixture.config.BindingPath),
		ShadowSnapshotPath: canonical(fixture.config.ShadowSnapshotPath), PurgeStorePath: canonical(fixture.config.PurgeStorePath), MinimumGeneration: fixture.config.MinimumGeneration,
		ReleaseCommit: release.ReleaseCommit, W4Mode: strideE10W4NetworkMode, W4ActivationID: sha256Hex([]byte("w4-activation")), W4ActivationReceiptDigest: sha256Hex([]byte("w4-receipt")),
		W4Generation: 106, W4SchemaVersion: 2, SessionAuthorityID: "canonical_session_authority", PurgeExecutorID: "canonical_network_purge_executor",
		PolicyID: fixture.policy.PolicyID, PolicyRevision: fixture.policy.Revision, PolicyDigest: strideE10W6SemanticDigest(fixture.policy),
		QualificationReceiptID: fixture.qual.ReceiptID, QualificationRevision: fixture.qual.Revision, QualificationDigest: strideE10W6SemanticDigest(fixture.qual),
		BindingTenantID: fixture.binding.TenantID, BindingCohortID: fixture.binding.CohortID, BindingDigest: strideE10W6SemanticDigest(fixture.binding),
	}
	keyRefsDigest, err := strideE10W6ManagedKeyRefsDigest(fixture.config.Key, fixture.config.RetainedKeys)
	if err != nil {
		t.Fatal(err)
	}
	expectation.KeyRefsDigest = keyRefsDigest
	updateStrideE10W4RuntimeReadiness(expectation.W4Mode, expectation.W4Generation, expectation.W4SchemaVersion, expectation.W4ActivationID, expectation.W4ActivationReceiptDigest, nil)
	strideE10W6ProductionClock = fixture.config.Now
	return expectation
}

func strideE10W6ProductionAttestation(expectation StrideE10W6ManagedProductionExpectation, now time.Time) StrideE10W6ManagedProductionAttestation {
	return StrideE10W6ManagedProductionAttestation{
		Contract: strideE10W6ManagedProductionContract, AdapterID: expectation.AdapterID, KeyID: expectation.KeyID, KeyVersion: expectation.KeyVersion, MinimumKeyVersion: expectation.MinimumKeyVersion, KeyRefsDigest: expectation.KeyRefsDigest,
		PolicyPath: expectation.PolicyPath, QualificationPath: expectation.QualificationPath, BindingPath: expectation.BindingPath, ShadowSnapshotPath: expectation.ShadowSnapshotPath, PurgeStorePath: expectation.PurgeStorePath,
		MinimumGeneration: expectation.MinimumGeneration, ReleaseCommit: expectation.ReleaseCommit, W4Mode: expectation.W4Mode, W4ActivationID: expectation.W4ActivationID, W4ActivationReceiptDigest: expectation.W4ActivationReceiptDigest,
		W4Generation: expectation.W4Generation, W4SchemaVersion: expectation.W4SchemaVersion, SessionAuthorityID: expectation.SessionAuthorityID, SessionAuthorityContract: strideE10W6SessionAuthorityContract,
		PurgeExecutorID: expectation.PurgeExecutorID, PurgeExecutorContract: strideE10W6PurgeExecutorContract, ObservedAt: now.Add(-time.Minute), ExpiresAt: now.Add(10 * time.Minute),
		PolicyID: expectation.PolicyID, PolicyRevision: expectation.PolicyRevision, PolicyDigest: expectation.PolicyDigest,
		QualificationReceiptID: expectation.QualificationReceiptID, QualificationRevision: expectation.QualificationRevision, QualificationDigest: expectation.QualificationDigest,
		BindingTenantID: expectation.BindingTenantID, BindingCohortID: expectation.BindingCohortID, BindingDigest: expectation.BindingDigest,
	}
}

func setStrideE10W6ManagedEnvironment(t *testing.T, e StrideE10W6ManagedProductionExpectation) {
	t.Helper()
	values := map[string]string{
		"STRIDE_E10_W6_MODE": "managed", "STRIDE_E10_W6_MANAGED_ADAPTER_ID": e.AdapterID, "STRIDE_E10_W6_KEY_ID": e.KeyID, "STRIDE_E10_W6_KEY_VERSION": strconv.FormatUint(e.KeyVersion, 10), "STRIDE_E10_W6_MINIMUM_KEY_VERSION": strconv.FormatUint(e.MinimumKeyVersion, 10), "STRIDE_E10_W6_KEY_REFS_SHA256": e.KeyRefsDigest,
		"STRIDE_E10_W6_POLICY_PATH": e.PolicyPath, "STRIDE_E10_W6_QUALIFICATION_PATH": e.QualificationPath, "STRIDE_E10_W6_BINDING_PATH": e.BindingPath, "STRIDE_E10_W6_SHADOW_SNAPSHOT_PATH": e.ShadowSnapshotPath,
		"STRIDE_E10_W6_PURGE_STORE_PATH": e.PurgeStorePath, "STRIDE_E10_W6_MINIMUM_GENERATION": strconv.FormatUint(e.MinimumGeneration, 10), "STRIDE_E10_W6_RELEASE_COMMIT": e.ReleaseCommit,
		"STRIDE_E10_W6_W4_MODE":          e.W4Mode,
		"STRIDE_E10_W6_W4_ACTIVATION_ID": e.W4ActivationID, "STRIDE_E10_W6_W4_RECEIPT_SHA256": e.W4ActivationReceiptDigest, "STRIDE_E10_W6_W4_GENERATION": strconv.FormatUint(e.W4Generation, 10),
		"STRIDE_E10_W6_W4_SCHEMA_VERSION": strconv.FormatUint(e.W4SchemaVersion, 10), "STRIDE_E10_W6_SESSION_AUTHORITY_ID": e.SessionAuthorityID, "STRIDE_E10_W6_PURGE_EXECUTOR_ID": e.PurgeExecutorID,
		"STRIDE_E10_W6_POLICY_ID": e.PolicyID, "STRIDE_E10_W6_POLICY_REVISION": strconv.FormatInt(e.PolicyRevision, 10), "STRIDE_E10_W6_POLICY_SHA256": e.PolicyDigest,
		"STRIDE_E10_W6_QUALIFICATION_RECEIPT_ID": e.QualificationReceiptID, "STRIDE_E10_W6_QUALIFICATION_REVISION": strconv.FormatInt(e.QualificationRevision, 10), "STRIDE_E10_W6_QUALIFICATION_SHA256": e.QualificationDigest,
		"STRIDE_E10_W6_BINDING_TENANT_ID": e.BindingTenantID, "STRIDE_E10_W6_BINDING_COHORT_ID": e.BindingCohortID, "STRIDE_E10_W6_BINDING_SHA256": e.BindingDigest,
	}
	for name, value := range values {
		t.Setenv(name, value)
	}
}

func strideE10W6ManagedProviderFor(fixture strideE10W6RuntimeFixture, expectation StrideE10W6ManagedProductionExpectation) *strideE10W6TestManagedProvider {
	now := fixture.config.Now().UTC()
	return &strideE10W6TestManagedProvider{
		adapters:    StrideE10W6ManagedProductionAdapters{Key: cloneStrideE10W6ManagedKey(fixture.config.Key), RetainedKeys: cloneStrideE10W6ManagedKeys(fixture.config.RetainedKeys), Sessions: fixture.sessions, PurgeExecutor: fixture.config.PurgeExecutor},
		attestation: strideE10W6ProductionAttestation(expectation, now),
	}
}

func TestStrideE10W6ProductionBootstrapOffParityAndSwitchFence(t *testing.T) {
	isolateStrideE10W6ProductionAdapterTest(t)
	fixture := newStrideE10W6RuntimeFixture(t)
	strideE10LiveProductRuntime = fixture.live
	t.Setenv("STRIDE_E10_W6_MODE", "off")
	before := strideE10W6RuntimeReadinessSnapshot()
	if err := installStrideE10W6ProductionRuntimeFromEnvironment(context.Background()); err != nil {
		t.Fatalf("off bootstrap changed behavior: %v", err)
	}
	if after := strideE10W6RuntimeReadinessSnapshot(); after != before {
		t.Fatalf("off bootstrap changed readiness: before=%+v after=%+v", before, after)
	}
	fixture.live.setFeatureForTest(STRIDEFeatureNetworkProjectionShadow, true)
	if err := installStrideE10W6ProductionRuntimeFromEnvironment(context.Background()); !errors.Is(err, ErrStrideE10W6RuntimeUnavailable) {
		t.Fatalf("enabled W6 switch without installed authority passed: %v", err)
	}
}

func TestStrideE10W6ManagedBootstrapInstallsWithoutActivationAndRepreflightsOnRestart(t *testing.T) {
	isolateStrideE10W6ProductionAdapterTest(t)
	fixture := newStrideE10W6RuntimeFixture(t)
	strideE10LiveProductRuntime = fixture.live
	expectation := strideE10W6ProductionExpectation(t, fixture)
	setStrideE10W6ManagedEnvironment(t, expectation)
	provider := strideE10W6ManagedProviderFor(fixture, expectation)
	restore := installStrideE10W6ManagedProductionProvider(provider)
	defer restore()
	if err := installStrideE10W6ProductionRuntimeFromEnvironment(context.Background()); err != nil {
		t.Fatalf("managed bootstrap: %v", err)
	}
	if ready := strideE10W6RuntimeReadinessSnapshot(); !ready.Ready || !ready.Installed || !ready.CanonicalBound {
		t.Fatalf("managed runtime not ready: %+v", ready)
	}
	for _, feature := range append(append([]STRIDEFeature{}, strideE10W6ActivationSwitches...), strideE10W6AlwaysDisabledSwitches...) {
		if fixture.live.Enabled(feature) {
			t.Fatalf("managed install enabled %s", feature)
		}
	}
	closeStrideE10W6ProductionRuntime()
	restartedLive := NewStrideE10ProductLiveRuntime(fixture.config.Now)
	restartedLive.network = fixture.live.network
	strideE10LiveProductRuntime = restartedLive
	if err := installStrideE10W6ProductionRuntimeFromEnvironment(context.Background()); err != nil {
		t.Fatalf("managed restart: %v", err)
	}
	if provider.callCount() != 2 {
		t.Fatalf("restart did not repeat external preflight: calls=%d", provider.callCount())
	}
}

func TestStrideE10W6ManagedBootstrapFailsClosedBeforeMutation(t *testing.T) {
	cases := map[string]func(*testing.T, *strideE10W6RuntimeFixture, *StrideE10W6ManagedProductionExpectation, *strideE10W6TestManagedProvider){
		"missing provider": func(_ *testing.T, _ *strideE10W6RuntimeFixture, _ *StrideE10W6ManagedProductionExpectation, p *strideE10W6TestManagedProvider) {
			p.err = ErrStrideE10W6RuntimeUnavailable
		},
		"attestation drift": func(_ *testing.T, _ *strideE10W6RuntimeFixture, _ *StrideE10W6ManagedProductionExpectation, p *strideE10W6TestManagedProvider) {
			p.attestation.SessionAuthorityID = "other_session_authority"
		},
		"semantic attestation drift": func(_ *testing.T, _ *strideE10W6RuntimeFixture, _ *StrideE10W6ManagedProductionExpectation, p *strideE10W6TestManagedProvider) {
			p.attestation.PolicyDigest = strings.Repeat("f", 64)
		},
		"expired attestation": func(_ *testing.T, f *strideE10W6RuntimeFixture, _ *StrideE10W6ManagedProductionExpectation, p *strideE10W6TestManagedProvider) {
			p.attestation.ExpiresAt = f.config.Now()
		},
		"wrong managed key": func(_ *testing.T, _ *strideE10W6RuntimeFixture, _ *StrideE10W6ManagedProductionExpectation, p *strideE10W6TestManagedProvider) {
			p.adapters.Key.ID = "wrong_key"
		},
		"release drift": func(_ *testing.T, _ *strideE10W6RuntimeFixture, e *StrideE10W6ManagedProductionExpectation, _ *strideE10W6TestManagedProvider) {
			e.ReleaseCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		},
		"W4 lineage drift": func(_ *testing.T, _ *strideE10W6RuntimeFixture, e *StrideE10W6ManagedProductionExpectation, _ *strideE10W6TestManagedProvider) {
			e.W4Generation++
		},
		"missing path": func(t *testing.T, _ *strideE10W6RuntimeFixture, e *StrideE10W6ManagedProductionExpectation, _ *strideE10W6TestManagedProvider) {
			e.BindingPath = filepath.Join(t.TempDir(), "missing.json")
		},
		"unsafe symlink path": func(t *testing.T, _ *strideE10W6RuntimeFixture, e *StrideE10W6ManagedProductionExpectation, _ *strideE10W6TestManagedProvider) {
			target := filepath.Join(t.TempDir(), "target.json")
			if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(t.TempDir(), "policy.json")
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			e.PolicyPath = link
		},
		"hardlink alias": func(t *testing.T, _ *strideE10W6RuntimeFixture, e *StrideE10W6ManagedProductionExpectation, _ *strideE10W6TestManagedProvider) {
			alias := filepath.Join(filepath.Dir(e.BindingPath), "binding-hardlink.json")
			if err := os.Link(e.PolicyPath, alias); err != nil {
				t.Fatal(err)
			}
			e.BindingPath = alias
		},
		"path swapped during provider": func(t *testing.T, _ *strideE10W6RuntimeFixture, e *StrideE10W6ManagedProductionExpectation, p *strideE10W6TestManagedProvider) {
			p.hook = func() {
				tmp := filepath.Join(filepath.Dir(e.BindingPath), "replacement.json")
				if err := os.WriteFile(tmp, []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(tmp, e.BindingPath); err != nil {
					t.Fatal(err)
				}
			}
		},
		"same inode semantic substitution during provider": func(t *testing.T, f *strideE10W6RuntimeFixture, e *StrideE10W6ManagedProductionExpectation, p *strideE10W6TestManagedProvider) {
			p.hook = func() {
				before, err := os.Stat(e.BindingPath)
				if err != nil {
					t.Fatal(err)
				}
				alternate := f.binding
				alternate.TenantID = "tenant_substituted"
				alternate, err = SignStrideE10W6RuntimeBinding(f.config.Key, alternate)
				if err != nil {
					t.Fatal(err)
				}
				body, err := json.Marshal(alternate)
				if err != nil || os.WriteFile(e.BindingPath, body, 0o600) != nil {
					t.Fatal("write alternate binding")
				}
				after, err := os.Stat(e.BindingPath)
				if err != nil || !os.SameFile(before, after) {
					t.Fatal("fixture did not preserve inode")
				}
			}
		},
		"W4 changed during provider": func(_ *testing.T, _ *strideE10W6RuntimeFixture, e *StrideE10W6ManagedProductionExpectation, p *strideE10W6TestManagedProvider) {
			p.hook = func() {
				updateStrideE10W4RuntimeReadiness(e.W4Mode, e.W4Generation+1, e.W4SchemaVersion, e.W4ActivationID, e.W4ActivationReceiptDigest, nil)
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			isolateStrideE10W6ProductionAdapterTest(t)
			fixture := newStrideE10W6RuntimeFixture(t)
			strideE10LiveProductRuntime = fixture.live
			expectation := strideE10W6ProductionExpectation(t, fixture)
			provider := strideE10W6ManagedProviderFor(fixture, expectation)
			mutate(t, &fixture, &expectation, provider)
			setStrideE10W6ManagedEnvironment(t, expectation)
			restore := installStrideE10W6ManagedProductionProvider(provider)
			defer restore()
			before, err := os.ReadFile(fixture.config.ShadowSnapshotPath)
			if err != nil {
				t.Fatal(err)
			}
			readiness := strideE10W6RuntimeReadinessSnapshot()
			if err := installStrideE10W6ProductionRuntimeFromEnvironment(context.Background()); err == nil {
				t.Fatal("unsafe managed bootstrap passed")
			}
			after, err := os.ReadFile(fixture.config.ShadowSnapshotPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) || strideE10W6RuntimeReadinessSnapshot() != readiness {
				t.Fatal("failed preflight mutated durable state or readiness")
			}
		})
	}
}

func TestStrideE10W6SecondManagedInstallConflictsBeforeAnyCanonicalOrDurableMutation(t *testing.T) {
	isolateStrideE10W6ProductionAdapterTest(t)
	fixture := newStrideE10W6RuntimeFixture(t)
	strideE10LiveProductRuntime = fixture.live
	expectation := strideE10W6ProductionExpectation(t, fixture)
	setStrideE10W6ManagedEnvironment(t, expectation)
	provider := strideE10W6ManagedProviderFor(fixture, expectation)
	restore := installStrideE10W6ManagedProductionProvider(provider)
	defer restore()
	if err := installStrideE10W6ProductionRuntimeFromEnvironment(context.Background()); err != nil {
		t.Fatal(err)
	}
	installed := currentStrideE10W6ProductionRuntime()
	fixture.live.network.mu.Lock()
	policy, qualification, shadow, cohort := fixture.live.network.w6Policy, fixture.live.network.w6Qualification, fixture.live.network.w6Shadow, fixture.live.network.w6CohortID
	fixture.live.network.mu.Unlock()
	readiness := strideE10W6RuntimeReadinessSnapshot()
	before := map[string][]byte{}
	for _, path := range expectation.paths() {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = body
	}
	if err := installStrideE10W6ProductionRuntimeFromEnvironment(context.Background()); !errors.Is(err, ErrStrideE10W6RuntimeConflict) {
		t.Fatalf("second install=%v", err)
	}
	if provider.callCount() != 1 || currentStrideE10W6ProductionRuntime() != installed || strideE10W6RuntimeReadinessSnapshot() != readiness {
		t.Fatal("worker conflict reached preflight or replaced installed runtime/readiness")
	}
	fixture.live.network.mu.Lock()
	unchanged := fixture.live.network.w6Policy == policy && fixture.live.network.w6Qualification == qualification && fixture.live.network.w6Shadow == shadow && fixture.live.network.w6CohortID == cohort
	fixture.live.network.mu.Unlock()
	if !unchanged {
		t.Fatal("second install conflict replaced canonical W6 authority")
	}
	for path, prior := range before {
		body, err := os.ReadFile(path)
		if err != nil || string(body) != string(prior) {
			t.Fatalf("second install mutated %s", path)
		}
	}
}

func TestStrideE10W6FinalAdmissionHoldsW4GenerationThroughFinalUse(t *testing.T) {
	isolateStrideE10W6ProductionAdapterTest(t)
	fixture := newStrideE10W6RuntimeFixture(t)
	expectation := strideE10W6ProductionExpectation(t, fixture)
	provider := strideE10W6ManagedProviderFor(fixture, expectation)
	config, _, err := PreflightStrideE10W6ProductionRuntime(context.Background(), expectation, provider, fixture.config.Now().UTC())
	if err != nil || config.FinalAdmission == nil {
		t.Fatalf("preflight=%v", err)
	}
	started := make(chan struct{})
	changed := make(chan struct{})
	err = config.FinalAdmission(func() error {
		go func() {
			close(started)
			updateStrideE10W4RuntimeReadiness(expectation.W4Mode, expectation.W4Generation+1, expectation.W4SchemaVersion, expectation.W4ActivationID, expectation.W4ActivationReceiptDigest, nil)
			close(changed)
		}()
		<-started
		select {
		case <-changed:
			return errors.New("W4 generation changed during final use")
		case <-time.After(20 * time.Millisecond):
			return nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("W4 generation update did not resume after final use")
	}
	invoked := false
	if err := config.FinalAdmission(func() error { invoked = true; return nil }); err == nil || invoked {
		t.Fatal("stale W4 generation reached final canonical use")
	}
}

func TestStrideE10W6ManagedBootstrapRejectsEnvironmentSecretWithoutProvider(t *testing.T) {
	isolateStrideE10W6ProductionAdapterTest(t)
	fixture := newStrideE10W6RuntimeFixture(t)
	strideE10LiveProductRuntime = fixture.live
	expectation := strideE10W6ProductionExpectation(t, fixture)
	setStrideE10W6ManagedEnvironment(t, expectation)
	t.Setenv("STRIDE_E10_W6_KEY_SECRET", "not-authority")
	t.Setenv("STRIDE_E10_W6_KEY_SECRET_BASE64", "bm90LWF1dGhvcml0eQ==")
	if err := installStrideE10W6ProductionRuntimeFromEnvironment(context.Background()); err == nil {
		t.Fatal("raw environment key installed W6 authority")
	}
}

func TestStrideE10W6ManagedBootstrapRejectsAlreadyEnabledSwitchBeforePreflight(t *testing.T) {
	isolateStrideE10W6ProductionAdapterTest(t)
	fixture := newStrideE10W6RuntimeFixture(t)
	strideE10LiveProductRuntime = fixture.live
	expectation := strideE10W6ProductionExpectation(t, fixture)
	setStrideE10W6ManagedEnvironment(t, expectation)
	provider := strideE10W6ManagedProviderFor(fixture, expectation)
	restore := installStrideE10W6ManagedProductionProvider(provider)
	defer restore()
	fixture.live.setFeatureForTest(STRIDEFeatureNetworkSearch, true)
	if err := installStrideE10W6ProductionRuntimeFromEnvironment(context.Background()); !errors.Is(err, ErrStrideE10W6RuntimeUnavailable) {
		t.Fatalf("managed bootstrap accepted an enabled switch: %v", err)
	}
	if provider.callCount() != 0 {
		t.Fatalf("enabled switch reached external preflight: calls=%d", provider.callCount())
	}
}

func TestStrideE10W6ProductionWorkerPersistsChangedShadow(t *testing.T) {
	isolateStrideE10W6ProductionAdapterTest(t)
	fixture := newStrideE10W6RuntimeFixture(t)
	strideE10LiveProductRuntime = fixture.live
	expectation := strideE10W6ProductionExpectation(t, fixture)
	setStrideE10W6ManagedEnvironment(t, expectation)
	provider := strideE10W6ManagedProviderFor(fixture, expectation)
	restore := installStrideE10W6ManagedProductionProvider(provider)
	defer restore()
	strideE10W6ProductionWorkerInterval = 5 * time.Millisecond
	if err := installStrideE10W6ProductionRuntimeFromEnvironment(context.Background()); err != nil {
		t.Fatalf("managed bootstrap: %v", err)
	}
	runtime := currentStrideE10W6ProductionRuntime()
	if runtime == nil || runtime.shadow == nil {
		t.Fatal("production bootstrap discarded its runtime")
	}
	before, err := loadStrideE10W6JSON[STRIDENetworkShadowSnapshot](fixture.config.ShadowSnapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	runtime.shadow.mu.Lock()
	runtime.shadow.revision++
	runtime.shadow.indexedRevision = runtime.shadow.revision
	wantRevision := runtime.shadow.revision
	runtime.shadow.mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for {
		after, loadErr := loadStrideE10W6JSON[STRIDENetworkShadowSnapshot](fixture.config.ShadowSnapshotPath)
		if loadErr == nil && after.Revision == wantRevision && after.Generation > before.Generation {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("production worker did not persist shadow revision %d", wantRevision)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStrideE10W6ManagedKeyRotationRestoresAndResealsMutableState(t *testing.T) {
	fixture := newStrideE10W6RuntimeFixture(t)
	priorKey := cloneStrideE10W6ManagedKey(fixture.config.Key)
	currentKey := W6ManagedMACKey{ID: priorKey.ID, Version: priorKey.Version + 1, Secret: []byte(strings.Repeat("n", 32))}
	fixture.config.Key = currentKey
	fixture.config.RetainedKeys = []W6ManagedMACKey{priorKey}
	fixture.config.MinimumKeyVersion = priorKey.Version
	runtime, err := InstallStrideE10W6ProductionRuntime(context.Background(), fixture.live, fixture.sessions, fixture.config)
	if err != nil {
		t.Fatalf("restore with retained key: %v", err)
	}
	if err := runtime.PersistShadow(); err != nil {
		t.Fatalf("reseal shadow: %v", err)
	}
	snapshot, err := loadStrideE10W6JSON[STRIDENetworkShadowSnapshot](fixture.config.ShadowSnapshotPath)
	if err != nil || snapshot.KeyID != currentKey.ID || snapshot.KeyVersion != currentKey.Version {
		t.Fatalf("shadow not resealed under current key: snapshot=%+v err=%v", snapshot, err)
	}
	if err := runtime.purgeStore.locked(true, func(*strideE10W6PurgeStoreEnvelope) error { return nil }); err != nil {
		t.Fatalf("reseal purge store: %v", err)
	}
	purges, err := loadStrideE10W6JSON[strideE10W6PurgeStoreEnvelope](fixture.config.PurgeStorePath)
	if err != nil || purges.KeyID != currentKey.ID || purges.KeyVersion != currentKey.Version {
		t.Fatalf("purge store not resealed under current key: envelope=%+v err=%v", purges, err)
	}
	withoutPrior := fixture.config
	withoutPrior.RetainedKeys = nil
	if _, err := InstallStrideE10W6ProductionRuntime(context.Background(), NewStrideE10ProductLiveRuntime(fixture.config.Now), fixture.sessions, withoutPrior); err == nil {
		t.Fatal("retired authority files loaded without their retained verification key")
	}
}
