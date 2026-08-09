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
	"sync"
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

type strideE10W6ManagedKeyring struct{ key W6ManagedMACKey }

func (k *strideE10W6ManagedKeyring) CurrentW6ManagedMACKey(context.Context) (W6ManagedMACKey, error) {
	if k == nil || !validStrideE10W6ManagedKey(k.key) {
		return W6ManagedMACKey{}, ErrStrideE10W6RuntimeUnavailable
	}
	return cloneStrideE10W6ManagedKey(k.key), nil
}
func (k *strideE10W6ManagedKeyring) ResolveW6ManagedMACKey(_ context.Context, id string, version uint64) (W6ManagedMACKey, error) {
	if k == nil || !validStrideE10W6ManagedKey(k.key) || id != k.key.ID || version != k.key.Version {
		return W6ManagedMACKey{}, ErrStrideE10W6RuntimeUnavailable
	}
	return cloneStrideE10W6ManagedKey(k.key), nil
}
func (k *strideE10W6ManagedKeyring) CurrentSTRIDENetworkShadowSnapshotKey() (STRIDENetworkShadowSnapshotKey, error) {
	if k == nil || !validStrideE10W6ManagedKey(k.key) {
		return STRIDENetworkShadowSnapshotKey{}, ErrStrideE10W6RuntimeUnavailable
	}
	return STRIDENetworkShadowSnapshotKey{KeyID: k.key.ID, Version: k.key.Version, Key: append([]byte(nil), k.key.Secret...)}, nil
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
	PurgeExecutor      STRIDENetworkShadowPurgeExecutor
	MinimumGeneration  uint64
	Now                func() time.Time
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
	if !validStrideE10W6ManagedKey(c.Key) || c.PurgeExecutor == nil || c.MinimumGeneration < 1 || c.Now == nil || c.Now().UTC().IsZero() {
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
	runtime := &StrideE10W6Runtime{config: config, keys: &strideE10W6ManagedKeyring{key: cloneStrideE10W6ManagedKey(config.Key)}, live: live, reason: "restoring"}
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
	shadowConfig := STRIDENetworkShadowConfig{Enabled: true, SearchOrganizationID: binding.TenantID, Now: config.Now, PurgeAuthority: resolver, AuthorityResolver: resolver, SearchAuthority: searchResolver, SnapshotKeys: runtime.keys, MinimumSnapshotGeneration: config.MinimumGeneration, MinimumSnapshotKeyVersion: config.Key.Version, PurgeReceipts: store, PurgeExecutor: config.PurgeExecutor, PurgeMaxAttempts: 3}
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
	if err := live.network.ConfigureW6Qualification(policy, qualification, shadow, binding.CohortID); err != nil {
		runtime.installed = false
		runtime.reason = "canonical_bind_failed"
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
	if err != nil || !verifyStrideE10W6Binding(r.keys.key, binding) || !binding.valid(policyValue, qualificationValue, now) {
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
	if err := r.live.network.ConfigureW6Qualification(r.policy, r.qualification, r.shadow, r.binding.CohortID); err != nil {
		r.mu.Lock()
		r.reason = "canonical_bind_failed"
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
	body, err := os.ReadFile(path)
	if err != nil || len(body) == 0 {
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
