package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type strideE10W5TestManagedProvider struct {
	mu          sync.Mutex
	adapters    StrideE10W5ManagedProductionAdapters
	attestation StrideE10W5ManagedProductionAttestation
	err         error
	calls       int
}

func (p *strideE10W5TestManagedProvider) PreflightStrideE10W5ManagedProduction(_ context.Context, _ StrideE10W5ManagedProductionExpectation) (StrideE10W5ManagedProductionAdapters, StrideE10W5ManagedProductionAttestation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.adapters, p.attestation, p.err
}

func (p *strideE10W5TestManagedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func strideE10W5TestManagedExpectation(path string) StrideE10W5ManagedProductionExpectation {
	return StrideE10W5ManagedProductionExpectation{
		StatePath:                  path,
		AdapterID:                  "external_custody_adapter",
		CustodyKeyNamespace:        "bonfire_mymind_private",
		StateKeyID:                 "mymind_state_mac",
		StateKeyVersion:            3,
		HighWaterStoreID:           "external_mymind_highwater",
		DestructionEvidenceKeyID:   "external_destruction_evidence",
		DestructionEvidenceVersion: 2,
		CustodyPolicyDigest:        strings.Repeat("a", 64),
		NamedCustodyOwnersDigest:   strings.Repeat("b", 64),
	}
}

func strideE10W5TestManagedAttestation(expectation StrideE10W5ManagedProductionExpectation, now time.Time) StrideE10W5ManagedProductionAttestation {
	return StrideE10W5ManagedProductionAttestation{
		Contract:                   strideE10W5ManagedProductionContract,
		AdapterID:                  expectation.AdapterID,
		CustodyKeyNamespace:        expectation.CustodyKeyNamespace,
		StateKeyID:                 expectation.StateKeyID,
		StateKeyVersion:            expectation.StateKeyVersion,
		HighWaterStoreID:           expectation.HighWaterStoreID,
		DestructionEvidenceKeyID:   expectation.DestructionEvidenceKeyID,
		DestructionEvidenceVersion: expectation.DestructionEvidenceVersion,
		CustodyPolicyDigest:        expectation.CustodyPolicyDigest,
		NamedCustodyOwnersDigest:   expectation.NamedCustodyOwnersDigest,
		StateMACContract:           strideE10W5StateMACContract,
		CustodyKeyContract:         strideE10W5CustodyKeyContract,
		HighWaterContract:          strideE10W5HighWaterContract,
		DestructionContract:        strideE10W5DestructionContract,
		OwnershipContract:          strideE10W5OwnershipContract,
		ObservedAt:                 now.Add(-time.Minute),
		ExpiresAt:                  now.Add(10 * time.Minute),
	}
}

func strideE10W5TestManagedProviderFor(expectation StrideE10W5ManagedProductionExpectation, now time.Time) (*strideE10W5TestManagedProvider, *testMyMindStateControl, *testMyMindKeyring) {
	state := newTestMyMindStateControl(MyMindCustodyStateKey{ID: expectation.StateKeyID, Version: expectation.StateKeyVersion, Material: []byte("0123456789abcdef0123456789abcdef")})
	keys := newTestMyMindKeyring("person-one")
	provider := &strideE10W5TestManagedProvider{
		adapters:    StrideE10W5ManagedProductionAdapters{StateKeys: state, HighWater: state, Keys: keys},
		attestation: strideE10W5TestManagedAttestation(expectation, now),
	}
	return provider, state, keys
}

func setStrideE10W5ManagedEnvironment(t *testing.T, expectation StrideE10W5ManagedProductionExpectation) {
	t.Helper()
	t.Setenv("STRIDE_E10_W5_CUSTODY_MODE", "managed")
	t.Setenv("STRIDE_E10_W5_STATE_PATH", expectation.StatePath)
	t.Setenv("STRIDE_E10_W5_MANAGED_ADAPTER_ID", expectation.AdapterID)
	t.Setenv("STRIDE_E10_W5_CUSTODY_KEY_NAMESPACE", expectation.CustodyKeyNamespace)
	t.Setenv("STRIDE_E10_W5_STATE_KEY_ID", expectation.StateKeyID)
	t.Setenv("STRIDE_E10_W5_STATE_KEY_VERSION", "3")
	t.Setenv("STRIDE_E10_W5_HIGH_WATER_STORE_ID", expectation.HighWaterStoreID)
	t.Setenv("STRIDE_E10_W5_DESTRUCTION_EVIDENCE_KEY_ID", expectation.DestructionEvidenceKeyID)
	t.Setenv("STRIDE_E10_W5_DESTRUCTION_EVIDENCE_KEY_VERSION", "2")
	t.Setenv("STRIDE_E10_W5_CUSTODY_POLICY_SHA256", expectation.CustodyPolicyDigest)
	t.Setenv("STRIDE_E10_W5_CUSTODY_OWNERS_SHA256", expectation.NamedCustodyOwnersDigest)
}

func TestStrideE10W5ProductionPreflightIsReadOnlyAndExact(t *testing.T) {
	now := time.Date(2026, 8, 9, 20, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "private", "mymind-custody.json")
	expectation := strideE10W5TestManagedExpectation(path)
	provider, state, keys := strideE10W5TestManagedProviderFor(expectation, now)

	config, err := PreflightStrideE10W5ProductionRuntime(context.Background(), expectation, provider, now)
	if err != nil || !config.valid() || provider.callCount() != 1 {
		t.Fatalf("exact managed preflight failed: config=%+v calls=%d err=%v", config, provider.callCount(), err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only preflight created custody state: %v", err)
	}
	state.mu.Lock()
	advanceCalls := state.advanceCalls
	state.mu.Unlock()
	keys.mu.Lock()
	destroyCalls := len(keys.destroyCalls)
	keys.mu.Unlock()
	if advanceCalls != 0 || destroyCalls != 0 {
		t.Fatalf("read-only preflight mutated managed providers: highwater=%d destruction=%d", advanceCalls, destroyCalls)
	}
}

func TestStrideE10W5ProductionPreflightRejectsDriftAndUnavailableCustody(t *testing.T) {
	now := time.Date(2026, 8, 9, 20, 0, 0, 0, time.UTC)
	expectation := strideE10W5TestManagedExpectation(filepath.Join(t.TempDir(), "mymind-custody.json"))
	baseProvider, state, _ := strideE10W5TestManagedProviderFor(expectation, now)

	tests := []struct {
		name   string
		mutate func(*strideE10W5TestManagedProvider)
	}{
		{name: "provider outage", mutate: func(p *strideE10W5TestManagedProvider) { p.err = errors.New("provider unavailable") }},
		{name: "adapter drift", mutate: func(p *strideE10W5TestManagedProvider) { p.attestation.AdapterID = "other_adapter" }},
		{name: "state key drift", mutate: func(p *strideE10W5TestManagedProvider) { p.attestation.StateKeyVersion++ }},
		{name: "high water drift", mutate: func(p *strideE10W5TestManagedProvider) { p.attestation.HighWaterStoreID = "other_highwater" }},
		{name: "destruction evidence drift", mutate: func(p *strideE10W5TestManagedProvider) { p.attestation.DestructionEvidenceVersion++ }},
		{name: "policy drift", mutate: func(p *strideE10W5TestManagedProvider) { p.attestation.CustodyPolicyDigest = strings.Repeat("c", 64) }},
		{name: "owners drift", mutate: func(p *strideE10W5TestManagedProvider) {
			p.attestation.NamedCustodyOwnersDigest = strings.Repeat("d", 64)
		}},
		{name: "capability widening", mutate: func(p *strideE10W5TestManagedProvider) { p.attestation.CustodyKeyContract = "shared_master_key_v1" }},
		{name: "expired attestation", mutate: func(p *strideE10W5TestManagedProvider) { p.attestation.ExpiresAt = now }},
		{name: "long lived attestation", mutate: func(p *strideE10W5TestManagedProvider) { p.attestation.ExpiresAt = now.Add(time.Hour) }},
		{name: "missing destruction adapter", mutate: func(p *strideE10W5TestManagedProvider) { p.adapters.Keys = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copyProvider := strideE10W5TestManagedProvider{adapters: baseProvider.adapters, attestation: baseProvider.attestation}
			test.mutate(&copyProvider)
			if _, err := PreflightStrideE10W5ProductionRuntime(context.Background(), expectation, &copyProvider, now); !errors.Is(err, ErrMyMindCustodyDenied) {
				t.Fatalf("unsafe managed preflight accepted: %v", err)
			}
		})
	}

	state.mu.Lock()
	state.current.Version++
	state.mu.Unlock()
	if _, err := PreflightStrideE10W5ProductionRuntime(context.Background(), expectation, baseProvider, now); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("current managed state key drift accepted: %v", err)
	}
}

func TestStrideE10W5ProductionPreflightRejectsAliasedState(t *testing.T) {
	now := time.Date(2026, 8, 9, 20, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte("not custody state"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(dir, "mymind-custody.json")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	expectation := strideE10W5TestManagedExpectation(symlink)
	provider, _, _ := strideE10W5TestManagedProviderFor(expectation, now)
	if _, err := PreflightStrideE10W5ProductionRuntime(context.Background(), expectation, provider, now); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("symlink custody state passed preflight: %v", err)
	}
}

func TestStrideE10W5ProductionBootstrapDefaultsOffAndRejectsEnvironmentKeys(t *testing.T) {
	priorRuntime := strideE10LiveProductRuntime
	runtime := NewStrideE10ProductLiveRuntime(time.Now)
	strideE10LiveProductRuntime = runtime
	defer func() {
		uninstallStrideE10W5ProductRuntime()
		strideE10LiveProductRuntime = priorRuntime
	}()
	t.Setenv("STRIDE_E10_W5_CUSTODY_MODE", "")
	if err := installStrideE10W5ProductionRuntimeFromEnvironment(context.Background()); err != nil {
		t.Fatalf("default-off bootstrap changed carrier behavior: %v", err)
	}
	if snapshot := strideE10W5RuntimeReadinessSnapshot(); snapshot["installed"] != false || snapshot["configured"] != false {
		t.Fatalf("default-off bootstrap installed or enabled custody: %+v", snapshot)
	}

	runtime.features[STRIDEFeaturePersonMyMindContext] = true
	if err := installStrideE10W5ProductionRuntimeFromEnvironment(context.Background()); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("enabled feature without managed custody was accepted: %v", err)
	}
	runtime.features[STRIDEFeaturePersonMyMindContext] = false

	expectation := strideE10W5TestManagedExpectation(filepath.Join(t.TempDir(), "mymind-custody.json"))
	setStrideE10W5ManagedEnvironment(t, expectation)
	// Raw environment material is intentionally not part of the contract and
	// cannot replace a compiled external managed provider.
	t.Setenv("STRIDE_E10_W5_STATE_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("STRIDE_E10_W5_CUSTODY_MASTER_KEY", "abcdef0123456789abcdef0123456789")
	if err := installStrideE10W5ProductionRuntimeFromEnvironment(context.Background()); !errors.Is(err, ErrMyMindCustodyDenied) {
		t.Fatalf("environment key fallback installed custody: %v", err)
	}
	if _, err := os.Stat(expectation.StatePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed bootstrap mutated state: %v", err)
	}
}

func TestStrideE10W5ProductionBootstrapInstallsManagedAdaptersWithoutEnablingSwitchAndRestarts(t *testing.T) {
	priorRuntime := strideE10LiveProductRuntime
	runtime := NewStrideE10ProductLiveRuntime(time.Now)
	strideE10LiveProductRuntime = runtime
	defer func() {
		uninstallStrideE10W5ProductRuntime()
		strideE10LiveProductRuntime = priorRuntime
	}()

	path := filepath.Join(t.TempDir(), "custody", "mymind-custody.json")
	expectation := strideE10W5TestManagedExpectation(path)
	provider, _, _ := strideE10W5TestManagedProviderFor(expectation, time.Now().UTC())
	restoreProvider := installStrideE10W5ManagedProductionProvider(provider)
	defer restoreProvider()
	setStrideE10W5ManagedEnvironment(t, expectation)

	if err := installStrideE10W5ProductionRuntimeFromEnvironment(context.Background()); err != nil {
		t.Fatalf("managed production bootstrap failed: %v", err)
	}
	if runtime.features[STRIDEFeaturePersonMyMindContext] {
		t.Fatal("managed custody bootstrap enabled person_mymind_context")
	}
	if snapshot := strideE10W5RuntimeReadinessSnapshot(); snapshot["installed"] != true || snapshot["configured"] != false || snapshot["ready"] != true {
		t.Fatalf("managed default-off readiness mismatch: %+v", snapshot)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty managed bootstrap unexpectedly wrote custody state: %v", err)
	}

	uninstallStrideE10W5ProductRuntime()
	if err := installStrideE10W5ProductionRuntimeFromEnvironment(context.Background()); err != nil {
		t.Fatalf("managed production restart/resume failed: %v", err)
	}
	if provider.callCount() != 2 || runtime.features[STRIDEFeaturePersonMyMindContext] {
		t.Fatalf("restart did not re-preflight without activation: calls=%d feature=%v", provider.callCount(), runtime.features[STRIDEFeaturePersonMyMindContext])
	}
}
