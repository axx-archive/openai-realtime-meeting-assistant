package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	strideE10W5ManagedProductionContract = "stride.mymind.managed-production.v1"
	strideE10W5StateMACContract          = "managed_rotation_retired_resolve_reseal_v1"
	strideE10W5CustodyKeyContract        = "independently_destructible_per_person_source_v1"
	strideE10W5HighWaterContract         = "external_monotonic_cas_v1"
	strideE10W5DestructionContract       = "journal_bound_idempotent_verified_receipt_v1"
	strideE10W5OwnershipContract         = "named_external_custody_v1"
)

// StrideE10W5ManagedProductionExpectation is the operator-controlled binding
// to an independently managed W5 custody service. It contains identifiers and
// digests only; raw custody, state-MAC, or destruction-evidence keys must never
// enter the application environment.
type StrideE10W5ManagedProductionExpectation struct {
	StatePath                  string
	AdapterID                  string
	CustodyKeyNamespace        string
	StateKeyID                 string
	StateKeyVersion            int64
	HighWaterStoreID           string
	DestructionEvidenceKeyID   string
	DestructionEvidenceVersion int64
	CustodyPolicyDigest        string
	NamedCustodyOwnersDigest   string
}

func (e StrideE10W5ManagedProductionExpectation) valid() bool {
	return filepath.IsAbs(e.StatePath) && filepath.Clean(e.StatePath) == e.StatePath &&
		strideIdentifier(e.AdapterID) && strideIdentifier(e.CustodyKeyNamespace) &&
		strideIdentifier(e.StateKeyID) && e.StateKeyVersion > 0 &&
		strideIdentifier(e.HighWaterStoreID) && strideIdentifier(e.DestructionEvidenceKeyID) &&
		e.DestructionEvidenceVersion > 0 && isHexDigest(e.CustodyPolicyDigest) &&
		isHexDigest(e.NamedCustodyOwnersDigest)
}

// StrideE10W5ManagedProductionAttestation is returned by the installed managed
// adapter after a read-only provider preflight. The application requires exact
// equality with the operator expectation and closed capability contracts.
type StrideE10W5ManagedProductionAttestation struct {
	Contract                   string
	AdapterID                  string
	CustodyKeyNamespace        string
	StateKeyID                 string
	StateKeyVersion            int64
	HighWaterStoreID           string
	DestructionEvidenceKeyID   string
	DestructionEvidenceVersion int64
	CustodyPolicyDigest        string
	NamedCustodyOwnersDigest   string
	StateMACContract           string
	CustodyKeyContract         string
	HighWaterContract          string
	DestructionContract        string
	OwnershipContract          string
	ObservedAt                 time.Time
	ExpiresAt                  time.Time
}

type StrideE10W5ManagedProductionAdapters struct {
	StateKeys MyMindCustodyStateKeyring
	HighWater MyMindCustodyHighWaterStore
	Keys      MyMindCustodyKeyring
}

// StrideE10W5ManagedProductionProvider is the external trust boundary. Its
// preflight must be read-only and must authenticate the attestation against the
// independently managed provider. This repository intentionally supplies no
// local or environment-key implementation of this interface.
type StrideE10W5ManagedProductionProvider interface {
	PreflightStrideE10W5ManagedProduction(context.Context, StrideE10W5ManagedProductionExpectation) (StrideE10W5ManagedProductionAdapters, StrideE10W5ManagedProductionAttestation, error)
}

var strideE10W5ManagedProviderState struct {
	sync.RWMutex
	provider StrideE10W5ManagedProductionProvider
}

func strideE10W5ManagedProductionProvider() StrideE10W5ManagedProductionProvider {
	strideE10W5ManagedProviderState.RLock()
	defer strideE10W5ManagedProviderState.RUnlock()
	return strideE10W5ManagedProviderState.provider
}

// installStrideE10W5ManagedProductionProvider is intentionally package-local:
// only a compiled production adapter may install the trust boundary. Tests use
// it to prove the fail-closed composition without making environment input an
// authority source.
func installStrideE10W5ManagedProductionProvider(provider StrideE10W5ManagedProductionProvider) func() {
	strideE10W5ManagedProviderState.Lock()
	prior := strideE10W5ManagedProviderState.provider
	strideE10W5ManagedProviderState.provider = provider
	strideE10W5ManagedProviderState.Unlock()
	return func() {
		strideE10W5ManagedProviderState.Lock()
		strideE10W5ManagedProviderState.provider = prior
		strideE10W5ManagedProviderState.Unlock()
	}
}

func strideE10W5ManagedExpectationFromEnvironment() (string, StrideE10W5ManagedProductionExpectation, error) {
	mode := strings.TrimSpace(os.Getenv("STRIDE_E10_W5_CUSTODY_MODE"))
	if mode == "" || mode == "off" {
		return "off", StrideE10W5ManagedProductionExpectation{}, nil
	}
	if mode != "managed" {
		return "", StrideE10W5ManagedProductionExpectation{}, ErrMyMindCustodyDenied
	}
	stateVersion, stateErr := strconv.ParseInt(strings.TrimSpace(os.Getenv("STRIDE_E10_W5_STATE_KEY_VERSION")), 10, 64)
	destructionVersion, destructionErr := strconv.ParseInt(strings.TrimSpace(os.Getenv("STRIDE_E10_W5_DESTRUCTION_EVIDENCE_KEY_VERSION")), 10, 64)
	if stateErr != nil || destructionErr != nil {
		return "", StrideE10W5ManagedProductionExpectation{}, ErrMyMindCustodyDenied
	}
	expectation := StrideE10W5ManagedProductionExpectation{
		StatePath:                  strings.TrimSpace(os.Getenv("STRIDE_E10_W5_STATE_PATH")),
		AdapterID:                  strings.TrimSpace(os.Getenv("STRIDE_E10_W5_MANAGED_ADAPTER_ID")),
		CustodyKeyNamespace:        strings.TrimSpace(os.Getenv("STRIDE_E10_W5_CUSTODY_KEY_NAMESPACE")),
		StateKeyID:                 strings.TrimSpace(os.Getenv("STRIDE_E10_W5_STATE_KEY_ID")),
		StateKeyVersion:            stateVersion,
		HighWaterStoreID:           strings.TrimSpace(os.Getenv("STRIDE_E10_W5_HIGH_WATER_STORE_ID")),
		DestructionEvidenceKeyID:   strings.TrimSpace(os.Getenv("STRIDE_E10_W5_DESTRUCTION_EVIDENCE_KEY_ID")),
		DestructionEvidenceVersion: destructionVersion,
		CustodyPolicyDigest:        strings.TrimSpace(os.Getenv("STRIDE_E10_W5_CUSTODY_POLICY_SHA256")),
		NamedCustodyOwnersDigest:   strings.TrimSpace(os.Getenv("STRIDE_E10_W5_CUSTODY_OWNERS_SHA256")),
	}
	if !expectation.valid() {
		return "", StrideE10W5ManagedProductionExpectation{}, ErrMyMindCustodyDenied
	}
	return "managed", expectation, nil
}

func validateStrideE10W5ManagedAttestation(expectation StrideE10W5ManagedProductionExpectation, attestation StrideE10W5ManagedProductionAttestation, now time.Time) error {
	if !expectation.valid() || attestation.Contract != strideE10W5ManagedProductionContract ||
		attestation.AdapterID != expectation.AdapterID || attestation.CustodyKeyNamespace != expectation.CustodyKeyNamespace ||
		attestation.StateKeyID != expectation.StateKeyID || attestation.StateKeyVersion != expectation.StateKeyVersion ||
		attestation.HighWaterStoreID != expectation.HighWaterStoreID ||
		attestation.DestructionEvidenceKeyID != expectation.DestructionEvidenceKeyID ||
		attestation.DestructionEvidenceVersion != expectation.DestructionEvidenceVersion ||
		attestation.CustodyPolicyDigest != expectation.CustodyPolicyDigest ||
		attestation.NamedCustodyOwnersDigest != expectation.NamedCustodyOwnersDigest ||
		attestation.StateMACContract != strideE10W5StateMACContract ||
		attestation.CustodyKeyContract != strideE10W5CustodyKeyContract ||
		attestation.HighWaterContract != strideE10W5HighWaterContract ||
		attestation.DestructionContract != strideE10W5DestructionContract ||
		attestation.OwnershipContract != strideE10W5OwnershipContract ||
		attestation.ObservedAt.IsZero() || attestation.ExpiresAt.IsZero() ||
		attestation.ObservedAt.After(now) || !now.Before(attestation.ExpiresAt) ||
		attestation.ExpiresAt.Sub(attestation.ObservedAt) > 15*time.Minute {
		return ErrMyMindCustodyDenied
	}
	return nil
}

// PreflightStrideE10W5ProductionRuntime performs provider and local read-only
// checks. It never creates custody state, advances the high-water, destroys a
// key, installs a handler, or enables the feature switch.
func PreflightStrideE10W5ProductionRuntime(ctx context.Context, expectation StrideE10W5ManagedProductionExpectation, provider StrideE10W5ManagedProductionProvider, now time.Time) (StrideE10W5ProductionConfig, error) {
	if ctx == nil || provider == nil || now.IsZero() || !expectation.valid() {
		return StrideE10W5ProductionConfig{}, ErrMyMindCustodyDenied
	}
	if rejectUnsafeMyMindCustodyPath(expectation.StatePath) != nil ||
		rejectUnsafeMyMindCustodyPath(expectation.StatePath+".lock") != nil ||
		rejectUnsafeMyMindCustodyPath(expectation.StatePath+".txn") != nil {
		return StrideE10W5ProductionConfig{}, ErrMyMindCustodyDenied
	}
	adapters, attestation, err := provider.PreflightStrideE10W5ManagedProduction(ctx, expectation)
	if err != nil || adapters.StateKeys == nil || adapters.HighWater == nil || adapters.Keys == nil || validateStrideE10W5ManagedAttestation(expectation, attestation, now.UTC()) != nil {
		return StrideE10W5ProductionConfig{}, ErrMyMindCustodyDenied
	}
	stateKey, err := adapters.StateKeys.CurrentMyMindCustodyStateKey(ctx)
	if err != nil || !stateKey.valid() || stateKey.ID != expectation.StateKeyID || stateKey.Version != expectation.StateKeyVersion {
		return StrideE10W5ProductionConfig{}, ErrMyMindCustodyDenied
	}
	highWater, err := adapters.HighWater.ReadMyMindCustodyHighWater(ctx, expectation.StatePath)
	if err != nil || highWater.Generation < 0 || (highWater.Generation == 0 && highWater.PayloadDigest != "") || (highWater.Generation > 0 && !isHexDigest(highWater.PayloadDigest)) {
		return StrideE10W5ProductionConfig{}, ErrMyMindCustodyDenied
	}
	return StrideE10W5ProductionConfig{StatePath: expectation.StatePath, StateKeys: adapters.StateKeys, HighWater: adapters.HighWater, Keys: adapters.Keys}, nil
}

// installStrideE10W5ProductionRuntimeFromEnvironment is the boot/operator
// composition. Off is the default and preserves the current carrier behavior.
// Managed mode installs custody only after exact preflight; it never mutates
// person_mymind_context. If the switch is already on while custody mode is off,
// startup refuses rather than serving an unfenced route.
func installStrideE10W5ProductionRuntimeFromEnvironment(ctx context.Context) error {
	mode, expectation, err := strideE10W5ManagedExpectationFromEnvironment()
	if err != nil {
		return err
	}
	if mode == "off" {
		if strideE10W5FeatureEnabled() {
			return ErrMyMindCustodyDenied
		}
		return nil
	}
	config, err := PreflightStrideE10W5ProductionRuntime(ctx, expectation, strideE10W5ManagedProductionProvider(), time.Now().UTC())
	if err != nil {
		return err
	}
	_, err = InstallStrideE10W5ProductionRuntime(ctx, config)
	return err
}
