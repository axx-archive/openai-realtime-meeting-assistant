package main

// E10-W6 production persistence/composition. This file intentionally owns no
// HTTP route, feature enablement, environment parsing, provider, or release
// behavior. Installation only wires already-authenticated, qualified authority
// into the route-free W1/W3 services; all W4 feature flags remain unchanged.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

var (
	ErrStrideE10W6RuntimeInvalid     = errors.New("invalid stride e10 w6 runtime")
	ErrStrideE10W6RuntimeUnavailable = errors.New("stride e10 w6 runtime unavailable")
	ErrStrideE10W6RuntimeConflict    = errors.New("stride e10 w6 runtime conflict")
)

const (
	strideE10W6BindingSchema = "stride.e10.w6.runtime-binding.v1"
	strideE10W6BindingDomain = "stride.e10.w6.runtime-binding.v1"
	strideE10W6PurgeSchema   = "stride.e10.w6.purge-store.v1"
	strideE10W6PurgeDomain   = "stride.e10.w6.purge-store.v1"
)

type StrideE10W6RuntimeBinding struct {
	Schema                 string    `json:"schema"`
	TenantID               string    `json:"tenantId"`
	CohortID               string    `json:"cohortId"`
	PolicyID               string    `json:"policyId"`
	PolicyRevision         int64     `json:"policyRevision"`
	QualificationReceiptID string    `json:"qualificationReceiptId"`
	QualificationRevision  int64     `json:"qualificationRevision"`
	QualificationDigest    string    `json:"qualificationDigest"`
	Enabled                bool      `json:"enabled"`
	BoundAt                time.Time `json:"boundAt"`
	ExpiresAt              time.Time `json:"expiresAt"`
	KeyID                  string    `json:"keyId"`
	KeyVersion             uint64    `json:"keyVersion"`
	MAC                    string    `json:"mac"`
}

func (v StrideE10W6RuntimeBinding) valid(policy W6NetworkPolicyRevision, qualification W6NetworkQualificationReceipt, at time.Time) bool {
	qd, _ := STRIDEContractDigest(qualification)
	return v.Schema == strideE10W6BindingSchema && strideIdentifier(v.TenantID) && strideIdentifier(v.CohortID) && v.Enabled &&
		v.PolicyID == policy.PolicyID && v.PolicyRevision == policy.Revision && v.QualificationReceiptID == qualification.ReceiptID &&
		v.QualificationRevision == qualification.Revision && v.QualificationDigest == qd && containsSTRIDEString(policy.CohortIDs, v.CohortID) &&
		!v.BoundAt.IsZero() && v.ExpiresAt.After(v.BoundAt) && !at.Before(v.BoundAt) && at.Before(v.ExpiresAt) && !v.ExpiresAt.After(policy.ExpiresAt) &&
		strideIdentifier(v.KeyID) && v.KeyVersion > 0 && isHexDigest(v.MAC)
}

type strideE10W6ManagedKeyring struct {
	current W6ManagedMACKey
	keys    map[string]W6ManagedMACKey
}

func strideE10W6ManagedKeyRef(id string, version uint64) string {
	return fmt.Sprintf("%s\x00%d", id, version)
}

func newStrideE10W6ManagedKeyring(current W6ManagedMACKey, retained []W6ManagedMACKey) (*strideE10W6ManagedKeyring, error) {
	if !validStrideE10W6ManagedKey(current) {
		return nil, ErrStrideE10W6RuntimeInvalid
	}
	keyring := &strideE10W6ManagedKeyring{current: cloneStrideE10W6ManagedKey(current), keys: map[string]W6ManagedMACKey{}}
	for _, key := range append([]W6ManagedMACKey{current}, retained...) {
		if !validStrideE10W6ManagedKey(key) || key.ID != current.ID || key.Version > current.Version {
			return nil, ErrStrideE10W6RuntimeInvalid
		}
		ref := strideE10W6ManagedKeyRef(key.ID, key.Version)
		if _, exists := keyring.keys[ref]; exists {
			return nil, ErrStrideE10W6RuntimeInvalid
		}
		keyring.keys[ref] = cloneStrideE10W6ManagedKey(key)
	}
	return keyring, nil
}

func (k *strideE10W6ManagedKeyring) CurrentW6ManagedMACKey(context.Context) (W6ManagedMACKey, error) {
	if k == nil || !validStrideE10W6ManagedKey(k.current) {
		return W6ManagedMACKey{}, ErrStrideE10W6RuntimeUnavailable
	}
	return cloneStrideE10W6ManagedKey(k.current), nil
}
func (k *strideE10W6ManagedKeyring) ResolveW6ManagedMACKey(_ context.Context, id string, version uint64) (W6ManagedMACKey, error) {
	if k == nil {
		return W6ManagedMACKey{}, ErrStrideE10W6RuntimeUnavailable
	}
	key, ok := k.keys[strideE10W6ManagedKeyRef(id, version)]
	if !ok || !validStrideE10W6ManagedKey(key) {
		return W6ManagedMACKey{}, ErrStrideE10W6RuntimeUnavailable
	}
	return cloneStrideE10W6ManagedKey(key), nil
}
func (k *strideE10W6ManagedKeyring) CurrentSTRIDENetworkShadowSnapshotKey() (STRIDENetworkShadowSnapshotKey, error) {
	if k == nil || !validStrideE10W6ManagedKey(k.current) {
		return STRIDENetworkShadowSnapshotKey{}, ErrStrideE10W6RuntimeUnavailable
	}
	return STRIDENetworkShadowSnapshotKey{KeyID: k.current.ID, Version: k.current.Version, Key: append([]byte(nil), k.current.Secret...)}, nil
}
func (k *strideE10W6ManagedKeyring) ResolveSTRIDENetworkShadowSnapshotKey(id string, version uint64) (STRIDENetworkShadowSnapshotKey, error) {
	key, err := k.ResolveW6ManagedMACKey(context.Background(), id, version)
	if err != nil {
		return STRIDENetworkShadowSnapshotKey{}, err
	}
	return STRIDENetworkShadowSnapshotKey{KeyID: key.ID, Version: key.Version, Key: key.Secret}, nil
}

func validStrideE10W6ManagedKey(key W6ManagedMACKey) bool {
	return strideIdentifier(key.ID) && key.Version > 0 && len(key.Secret) >= 32
}
func cloneStrideE10W6ManagedKey(key W6ManagedMACKey) W6ManagedMACKey {
	key.Secret = append([]byte(nil), key.Secret...)
	return key
}

func cloneStrideE10W6ManagedKeys(keys []W6ManagedMACKey) []W6ManagedMACKey {
	out := make([]W6ManagedMACKey, len(keys))
	for i, key := range keys {
		out[i] = cloneStrideE10W6ManagedKey(key)
	}
	return out
}

type StrideE10W6CurrentSession struct {
	SessionHash                  string
	PersonID                     string
	OrganizationID               string
	MembershipID                 string
	MembershipRevision           int64
	ActiveOrganizationSessionID  string
	ActiveOrganizationSessionRev int64
}

type StrideE10W6SessionAuthority interface {
	WithCurrentStrideE10W6Session(context.Context, string, string, func(StrideE10W6CurrentSession) error) error
}

type StrideE10W6RuntimeConfig struct {
	PolicyPath         string
	QualificationPath  string
	BindingPath        string
	ShadowSnapshotPath string
	PurgeStorePath     string
	Key                W6ManagedMACKey
	RetainedKeys       []W6ManagedMACKey
	MinimumKeyVersion  uint64
	PurgeExecutor      STRIDENetworkShadowPurgeExecutor
	MinimumGeneration  uint64
	Now                func() time.Time
	FinalAdmission     func(func() error) error
}

func (c StrideE10W6RuntimeConfig) validate() error {
	paths := []string{c.PolicyPath, c.QualificationPath, c.BindingPath, c.ShadowSnapshotPath, c.PurgeStorePath}
	seen := map[string]bool{}
	for _, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || seen[path] {
			return ErrStrideE10W6RuntimeInvalid
		}
		seen[path] = true
	}
	if _, err := newStrideE10W6ManagedKeyring(c.Key, c.RetainedKeys); err != nil || c.MinimumKeyVersion < 1 || c.MinimumKeyVersion > c.Key.Version || c.PurgeExecutor == nil || c.MinimumGeneration < 1 || c.Now == nil || c.Now().UTC().IsZero() {
		return ErrStrideE10W6RuntimeInvalid
	}
	return nil
}

type StrideE10W6Runtime struct {
	mu             sync.RWMutex
	config         StrideE10W6RuntimeConfig
	keys           *strideE10W6ManagedKeyring
	policy         *W6NetworkPolicyAuthority
	qualification  *W6NetworkQualificationAuthority
	binding        StrideE10W6RuntimeBinding
	shadow         *STRIDENetworkShadowService
	purgeStore     *strideE10W6FilePurgeStore
	live           *StrideE10ProductLiveRuntime
	installed      bool
	canonicalBound bool
	reason         string
}

type StrideE10W6RuntimeReadiness struct {
	Configured                 bool   `json:"configured"`
	Installed                  bool   `json:"installed"`
	Ready                      bool   `json:"ready"`
	Reason                     string `json:"reason,omitempty"`
	TenantID                   string `json:"tenantId,omitempty"`
	CohortID                   string `json:"cohortId,omitempty"`
	PolicyID                   string `json:"policyId,omitempty"`
	PolicyRevision             int64  `json:"policyRevision,omitempty"`
	QualificationReceiptID     string `json:"qualificationReceiptId,omitempty"`
	QualificationRevision      int64  `json:"qualificationRevision,omitempty"`
	ShadowRevision             int64  `json:"shadowRevision,omitempty"`
	ShadowIndexedRevision      int64  `json:"shadowIndexedRevision,omitempty"`
	ShadowGeneration           uint64 `json:"shadowGeneration,omitempty"`
	PurgeQueued                int    `json:"purgeQueued"`
	PurgeRunning               int    `json:"purgeRunning"`
	PurgeFailed                int    `json:"purgeFailed"`
	PurgeCompleted             int    `json:"purgeCompleted"`
	DeterministicParser        bool   `json:"deterministicParser"`
	QueryParserProviderEnabled bool   `json:"queryParserProviderEnabled"`
	SemanticRerankerEnabled    bool   `json:"semanticRerankerEnabled"`
	CohortQualified            bool   `json:"cohortQualified"`
	CanonicalBound             bool   `json:"canonicalBound"`
}

var strideE10W6RuntimeReadinessState struct {
	sync.RWMutex
	value StrideE10W6RuntimeReadiness
}

func strideE10W6RuntimeReadinessSnapshot() StrideE10W6RuntimeReadiness {
	strideE10W6RuntimeReadinessState.RLock()
	defer strideE10W6RuntimeReadinessState.RUnlock()
	return strideE10W6RuntimeReadinessState.value
}

func publishStrideE10W6RuntimeReadiness(value StrideE10W6RuntimeReadiness) {
	strideE10W6RuntimeReadinessState.Lock()
	strideE10W6RuntimeReadinessState.value = value
	strideE10W6RuntimeReadinessState.Unlock()
}

func strideE10W6NetworkFeaturesEnabled() bool {
	live := strideE10LiveProductRuntime
	if live == nil {
		return false
	}
	live.mu.RLock()
	defer live.mu.RUnlock()
	return live.features[STRIDEFeatureNetworkProfilePublication] || live.features[STRIDEFeatureNetworkSearch] || live.features[STRIDEFeatureNetworkContact]
}

// InstallStrideE10W6ProductionRuntime is the only composition hook. It does
// not mutate feature flags. On every error the live W4 authority remains
// untouched because ConfigureW6Qualification is the final step.
func InstallStrideE10W6ProductionRuntime(ctx context.Context, live *StrideE10ProductLiveRuntime, sessions StrideE10W6SessionAuthority, config StrideE10W6RuntimeConfig) (*StrideE10W6Runtime, error) {
	if ctx == nil || live == nil || live.network == nil || sessions == nil || config.validate() != nil {
		return nil, ErrStrideE10W6RuntimeInvalid
	}
	keys, err := newStrideE10W6ManagedKeyring(config.Key, config.RetainedKeys)
	if err != nil {
		return nil, err
	}
	runtime := &StrideE10W6Runtime{config: config, keys: keys, live: live, reason: "restoring"}
	policy, qualification, binding, err := runtime.loadAuthorities(ctx)
	if err != nil {
		runtime.reason = "authority_restore_failed"
		return nil, err
	}
	runtime.policy, runtime.qualification, runtime.binding = policy, qualification, binding
	store, err := newStrideE10W6FilePurgeStore(config.PurgeStorePath, runtime.keys)
	if err != nil {
		runtime.reason = "purge_store_restore_failed"
		return nil, err
	}
	resolver := &strideE10W6LiveAuthorityResolver{network: live.network}
	searchResolver := &strideE10W6LiveSearchAuthorityResolver{network: live.network, sessions: sessions}
	shadowConfig := STRIDENetworkShadowConfig{Enabled: true, SearchOrganizationID: binding.TenantID, Now: config.Now, PurgeAuthority: resolver, AuthorityResolver: resolver, SearchAuthority: searchResolver, SnapshotKeys: runtime.keys, MinimumSnapshotGeneration: config.MinimumGeneration, MinimumSnapshotKeyVersion: config.MinimumKeyVersion, PurgeReceipts: store, PurgeExecutor: config.PurgeExecutor, PurgeMaxAttempts: 3}
	snapshot, err := loadStrideE10W6JSON[STRIDENetworkShadowSnapshot](config.ShadowSnapshotPath)
	if err != nil {
		runtime.reason = "shadow_snapshot_missing_or_invalid"
		return nil, ErrStrideE10W6RuntimeUnavailable
	}
	shadow, err := RestoreSTRIDENetworkShadowService(shadowConfig, snapshot)
	if err != nil {
		runtime.reason = "shadow_restore_failed"
		return nil, err
	}
	now := config.Now().UTC()
	if err := shadow.BindCurrentW6Policy(ctx, policy, qualification, binding.PolicyRevision, binding.CohortID, now); err != nil {
		runtime.reason = "shadow_policy_bind_failed"
		return nil, err
	}
	runtime.shadow, runtime.purgeStore = shadow, store
	if err := runtime.reconcileDurablePurgeState(ctx); err != nil {
		runtime.reason = "purge_reconcile_failed"
		return nil, err
	}
	// Preflight every readiness condition before touching the live authority.
	// The provisional installed bit is local to this unpublished runtime.
	runtime.installed, runtime.reason = true, ""
	if ready := runtime.Readiness(ctx); !(ready.Configured && ready.Installed && ready.CohortQualified && ready.ShadowRevision > 0 && ready.ShadowRevision == ready.ShadowIndexedRevision && ready.PurgeQueued == 0 && ready.PurgeRunning == 0 && ready.PurgeFailed == 0 && ready.Reason == "") {
		if ready.PurgeQueued+ready.PurgeRunning+ready.PurgeFailed > 0 {
			runtime.reason = "purge_backlog"
			publishStrideE10W6RuntimeReadiness(runtime.Readiness(ctx))
			return runtime, nil
		}
		runtime.installed = false
		runtime.reason = "readiness_preflight_failed"
		return nil, ErrStrideE10W6RuntimeUnavailable
	}
	// Final mutation: everything above has authenticated and restored.
	bind := func() error {
		return live.network.ConfigureW6Qualification(policy, qualification, shadow, binding.CohortID)
	}
	if config.FinalAdmission != nil {
		err = config.FinalAdmission(bind)
	} else {
		err = bind()
	}
	if err != nil {
		runtime.installed = false
		runtime.reason = "final_admission_or_canonical_bind_failed"
		return nil, err
	}
	runtime.canonicalBound, runtime.reason = true, ""
	ready := runtime.Readiness(ctx)
	if !ready.Ready {
		runtime.reason = "authority_changed_after_bind"
		ready = runtime.Readiness(ctx)
	}
	publishStrideE10W6RuntimeReadiness(ready)
	return runtime, nil
}

func (r *StrideE10W6Runtime) loadAuthorities(ctx context.Context) (*W6NetworkPolicyAuthority, *W6NetworkQualificationAuthority, StrideE10W6RuntimeBinding, error) {
	policyValue, err := loadStrideE10W6JSON[W6NetworkPolicyRevision](r.config.PolicyPath)
	if err != nil {
		return nil, nil, StrideE10W6RuntimeBinding{}, ErrStrideE10W6RuntimeUnavailable
	}
	policy := NewW6NetworkPolicyAuthority(r.keys)
	if policy.Install(ctx, policyValue) != nil {
		return nil, nil, StrideE10W6RuntimeBinding{}, ErrStrideE10W6RuntimeUnavailable
	}
	qualificationValue, err := loadStrideE10W6JSON[W6NetworkQualificationReceipt](r.config.QualificationPath)
	if err != nil {
		return nil, nil, StrideE10W6RuntimeBinding{}, ErrStrideE10W6RuntimeUnavailable
	}
	now := r.config.Now().UTC()
	qualification := NewW6NetworkQualificationAuthority(r.keys)
	if qualification.Install(ctx, policyValue, qualificationValue, now) != nil {
		return nil, nil, StrideE10W6RuntimeBinding{}, ErrStrideE10W6RuntimeUnavailable
	}
	binding, err := loadStrideE10W6JSON[StrideE10W6RuntimeBinding](r.config.BindingPath)
	key, keyErr := r.keys.ResolveW6ManagedMACKey(ctx, binding.KeyID, binding.KeyVersion)
	if err != nil || keyErr != nil || !verifyStrideE10W6Binding(key, binding) || !binding.valid(policyValue, qualificationValue, now) {
		return nil, nil, StrideE10W6RuntimeBinding{}, ErrStrideE10W6RuntimeUnavailable
	}
	return policy, qualification, binding, nil
}

func (r *StrideE10W6Runtime) PersistShadow() error {
	if r == nil || !r.installed || r.shadow == nil {
		return ErrStrideE10W6RuntimeUnavailable
	}
	snapshot, err := r.shadow.Snapshot()
	if err != nil {
		return err
	}
	if err := writeStrideE10W6JSON(r.config.ShadowSnapshotPath, snapshot); err != nil {
		r.mu.Lock()
		r.reason = "shadow_persist_failed"
		r.mu.Unlock()
		publishStrideE10W6RuntimeReadiness(r.Readiness(context.Background()))
		return err
	}
	return nil
}

func (r *StrideE10W6Runtime) PersistShadowIfChanged() error {
	if r == nil || !r.installed || r.shadow == nil {
		return ErrStrideE10W6RuntimeUnavailable
	}
	snapshot, err := r.shadow.Snapshot()
	if err != nil {
		return err
	}
	prior, priorErr := loadStrideE10W6JSON[STRIDENetworkShadowSnapshot](r.config.ShadowSnapshotPath)
	if priorErr == nil && prior.KeyID == snapshot.KeyID && prior.KeyVersion == snapshot.KeyVersion && prior.Generation == snapshot.Generation && prior.Digest == snapshot.Digest && prior.Signature == snapshot.Signature {
		return nil
	}
	if err := writeStrideE10W6JSON(r.config.ShadowSnapshotPath, snapshot); err != nil {
		r.mu.Lock()
		r.reason = "shadow_persist_failed"
		r.mu.Unlock()
		publishStrideE10W6RuntimeReadiness(r.Readiness(context.Background()))
		return err
	}
	return nil
}

// reconcileDurablePurgeState closes the only crash window between the purge
// receipt CAS and the shadow snapshot write. The authenticated purge store is
// the recovery journal; no provider call is made here.
func (r *StrideE10W6Runtime) reconcileDurablePurgeState(ctx context.Context) error {
	if r == nil || r.shadow == nil || r.purgeStore == nil {
		return ErrStrideE10W6RuntimeUnavailable
	}
	works, err := r.purgeStore.ListSTRIDENetworkShadowPurgeWork(ctx)
	if err != nil {
		return err
	}
	sort.Slice(works, func(i, j int) bool {
		if works[i].Receipt.PurgeGeneration == works[j].Receipt.PurgeGeneration {
			return works[i].Receipt.Header.ID < works[j].Receipt.Header.ID
		}
		return works[i].Receipt.PurgeGeneration < works[j].Receipt.PurgeGeneration
	})
	changed := false
	r.shadow.w6HealthMu.Lock()
	r.shadow.mu.Lock()
	for _, work := range works {
		if !validSTRIDENetworkShadowPurgeWork(work) {
			r.shadow.mu.Unlock()
			r.shadow.w6HealthMu.Unlock()
			return ErrStrideE10W6RuntimeUnavailable
		}
		receipt := work.Receipt
		high := r.shadow.purgeHighWater[receipt.SubjectPersonID]
		if receipt.PurgeGeneration < high {
			r.shadow.mu.Unlock()
			r.shadow.w6HealthMu.Unlock()
			return ErrStrideE10W6RuntimeConflict
		}
		if receipt.PurgeGeneration == high {
			prior, ok := r.shadow.purges[receipt.Header.ID]
			if !ok || !sameSTRIDENetworkShadowPurgeIdentity(prior, receipt) {
				r.shadow.mu.Unlock()
				r.shadow.w6HealthMu.Unlock()
				return ErrStrideE10W6RuntimeConflict
			}
			if prior.State != receipt.State {
				r.shadow.purges[receipt.Header.ID] = cloneContract(receipt)
				r.shadow.revision++
				r.shadow.indexedRevision = r.shadow.revision
				changed = true
			}
			continue
		}
		if record, ok := r.shadow.records[receipt.SubjectPersonID]; ok && !shadowTriggerMatches(record.admission, receipt.Trigger) {
			r.shadow.mu.Unlock()
			r.shadow.w6HealthMu.Unlock()
			return ErrStrideE10W6RuntimeConflict
		}
		if err := r.shadow.applyPurgeLocked(receipt); err != nil {
			r.shadow.mu.Unlock()
			r.shadow.w6HealthMu.Unlock()
			return err
		}
		changed = true
	}
	r.shadow.mu.Unlock()
	r.shadow.w6HealthMu.Unlock()
	if !changed {
		return nil
	}
	snapshot, err := r.shadow.Snapshot()
	if err != nil {
		return err
	}
	return writeStrideE10W6JSON(r.config.ShadowSnapshotPath, snapshot)
}

func (r *StrideE10W6Runtime) ProcessPurgeWork(ctx context.Context) (STRIDENetworkShadowPurgeWork, bool, error) {
	if r == nil || !r.installed || r.shadow == nil {
		return STRIDENetworkShadowPurgeWork{}, false, ErrStrideE10W6RuntimeUnavailable
	}
	work, changed, err := r.shadow.ProcessPurgeWork(ctx, r.config.Now().UTC())
	if changed && r.PersistShadow() != nil {
		return work, changed, ErrStrideE10W6RuntimeUnavailable
	}
	if err == nil && changed {
		_ = r.tryBindCanonical(ctx)
	}
	return work, changed, err
}

func (r *StrideE10W6Runtime) tryBindCanonical(ctx context.Context) error {
	if r == nil || !r.installed || r.live == nil || r.live.network == nil {
		return ErrStrideE10W6RuntimeUnavailable
	}
	r.mu.Lock()
	if r.canonicalBound {
		r.mu.Unlock()
		return nil
	}
	r.reason = ""
	r.mu.Unlock()
	ready := r.Readiness(ctx)
	if ready.PurgeQueued+ready.PurgeRunning+ready.PurgeFailed > 0 || ready.ShadowRevision == 0 || ready.ShadowRevision != ready.ShadowIndexedRevision || ready.Reason != "" {
		r.mu.Lock()
		r.reason = "purge_backlog"
		r.mu.Unlock()
		return ErrStrideE10W6RuntimeUnavailable
	}
	bind := func() error {
		return r.live.network.ConfigureW6Qualification(r.policy, r.qualification, r.shadow, r.binding.CohortID)
	}
	var err error
	if r.config.FinalAdmission != nil {
		err = r.config.FinalAdmission(bind)
	} else {
		err = bind()
	}
	if err != nil {
		r.mu.Lock()
		r.reason = "final_admission_or_canonical_bind_failed"
		r.mu.Unlock()
		return err
	}
	r.mu.Lock()
	r.canonicalBound = true
	r.mu.Unlock()
	publishStrideE10W6RuntimeReadiness(r.Readiness(ctx))
	return nil
}

func (r *StrideE10W6Runtime) Readiness(ctx context.Context) StrideE10W6RuntimeReadiness {
	value := StrideE10W6RuntimeReadiness{Configured: r != nil && r.config.validate() == nil, Installed: r != nil && r.installed, DeterministicParser: true}
	if r == nil {
		value.Reason = "not_configured"
		return value
	}
	r.mu.RLock()
	value.Reason = r.reason
	value.TenantID, value.CohortID = r.binding.TenantID, r.binding.CohortID
	value.PolicyID, value.PolicyRevision = r.binding.PolicyID, r.binding.PolicyRevision
	value.QualificationReceiptID, value.QualificationRevision = r.binding.QualificationReceiptID, r.binding.QualificationRevision
	value.CanonicalBound = r.canonicalBound
	r.mu.RUnlock()
	if r.purgeStore != nil {
		works, err := r.purgeStore.ListSTRIDENetworkShadowPurgeWork(ctx)
		if err != nil {
			value.Reason = "purge_store_unavailable"
			return value
		}
		for _, work := range works {
			switch work.State {
			case strideNetworkShadowPurgeQueued:
				value.PurgeQueued++
			case strideNetworkShadowPurgeRunning:
				value.PurgeRunning++
			case strideNetworkShadowPurgeCompleted:
				value.PurgeCompleted++
			case strideNetworkShadowPurgeFailed:
				value.PurgeFailed++
			default:
				value.Reason = "purge_store_invalid"
				return value
			}
		}
	}
	if r.shadow != nil {
		snapshot, err := r.shadow.Snapshot()
		if err != nil {
			value.Reason = "shadow_unavailable"
			return value
		}
		value.ShadowRevision, value.ShadowIndexedRevision, value.ShadowGeneration = snapshot.Revision, snapshot.IndexedRevision, snapshot.Generation
		if err := r.shadow.WithHealthyCurrentW6Shadow(ctx, W6ShadowHealthExpectation{OrganizationID: r.binding.TenantID, PolicyRevision: r.binding.PolicyRevision}, func(W6ShadowHealthSnapshot) error { return nil }); err != nil {
			value.Reason = "shadow_authority_unhealthy"
			return value
		}
	}
	value.CohortQualified = r.binding.Enabled && value.PolicyRevision > 0 && value.QualificationRevision > 0
	value.Ready = value.Configured && value.Installed && value.CanonicalBound && value.CohortQualified && value.ShadowRevision > 0 && value.ShadowRevision == value.ShadowIndexedRevision && value.PurgeQueued == 0 && value.PurgeRunning == 0 && value.PurgeFailed == 0 && value.Reason == ""
	return value
}

func SignStrideE10W6RuntimeBinding(key W6ManagedMACKey, value StrideE10W6RuntimeBinding) (StrideE10W6RuntimeBinding, error) {
	if !validStrideE10W6ManagedKey(key) {
		return StrideE10W6RuntimeBinding{}, ErrStrideE10W6RuntimeInvalid
	}
	value.Schema, value.KeyID, value.KeyVersion, value.MAC = strideE10W6BindingSchema, key.ID, key.Version, ""
	payload, err := strideE10W6BindingPayload(value)
	if err != nil {
		return StrideE10W6RuntimeBinding{}, err
	}
	value.MAC = strideE10W6MAC(key.Secret, strideE10W6BindingDomain, payload)
	return value, nil
}

func verifyStrideE10W6Binding(key W6ManagedMACKey, value StrideE10W6RuntimeBinding) bool {
	if value.KeyID != key.ID || value.KeyVersion != key.Version || !isHexDigest(value.MAC) {
		return false
	}
	payload, err := strideE10W6BindingPayload(value)
	if err != nil {
		return false
	}
	want, _ := hex.DecodeString(strideE10W6MAC(key.Secret, strideE10W6BindingDomain, payload))
	got, err := hex.DecodeString(value.MAC)
	return err == nil && hmac.Equal(want, got)
}

func strideE10W6BindingPayload(value StrideE10W6RuntimeBinding) ([]byte, error) {
	value.MAC = ""
	return json.Marshal(value)
}

func strideE10W6MAC(secret []byte, domain string, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "%s\x00", domain)
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func loadStrideE10W6JSON[T any](path string) (T, error) {
	var value T
	identity, err := preflightStrideE10W6Path(path)
	if err != nil {
		return value, ErrStrideE10W6RuntimeUnavailable
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return value, ErrStrideE10W6RuntimeUnavailable
	}
	defer file.Close()
	info, err := file.Stat()
	stat, ok := info.Sys().(*syscall.Stat_t)
	if err != nil || !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || stat.Nlink != 1 || int(stat.Uid) != os.Geteuid() || uint64(stat.Dev) != identity.FileDev || uint64(stat.Ino) != identity.FileIno || info.Size() != identity.FileSize {
		return value, ErrStrideE10W6RuntimeUnavailable
	}
	body, err := io.ReadAll(file)
	current, currentErr := preflightStrideE10W6Path(path)
	if err != nil || len(body) == 0 || currentErr != nil || current != identity || sha256Hex(body) != identity.ContentDigest {
		return value, ErrStrideE10W6RuntimeUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return value, ErrStrideE10W6RuntimeUnavailable
	}
	return value, nil
}

func writeStrideE10W6JSON(path string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeFileAtomicallyDurable(path, body, 0o600)
}
