package main

// Default-off production composition for the W6 network qualification runtime.
// Environment values bind public identifiers and paths only. Managed key
// material, current session authority, and the purge executor can enter the
// process only through a compiled provider implementation.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var strideE10W6ProductionClock = time.Now
var strideE10W6ProductionWorkerInterval = time.Second

const (
	strideE10W6ManagedProductionContract = "stride.e10.w6.managed-production.v1"
	strideE10W6SessionAuthorityContract  = "current_person_org_membership_session_callback_v1"
	strideE10W6PurgeExecutorContract     = "derived_purge_exact_store_idempotent_v1"
)

type StrideE10W6ManagedProductionExpectation struct {
	AdapterID                 string
	KeyID                     string
	KeyVersion                uint64
	MinimumKeyVersion         uint64
	KeyRefsDigest             string
	PolicyPath                string
	QualificationPath         string
	BindingPath               string
	ShadowSnapshotPath        string
	PurgeStorePath            string
	MinimumGeneration         uint64
	ReleaseCommit             string
	W4Mode                    string
	W4ActivationID            string
	W4ActivationReceiptDigest string
	W4Generation              uint64
	W4SchemaVersion           uint64
	SessionAuthorityID        string
	PurgeExecutorID           string
	PolicyID                  string
	PolicyRevision            int64
	PolicyDigest              string
	QualificationReceiptID    string
	QualificationRevision     int64
	QualificationDigest       string
	BindingTenantID           string
	BindingCohortID           string
	BindingDigest             string
}

func (e StrideE10W6ManagedProductionExpectation) paths() []string {
	return []string{e.PolicyPath, e.QualificationPath, e.BindingPath, e.ShadowSnapshotPath, e.PurgeStorePath}
}

func (e StrideE10W6ManagedProductionExpectation) valid() bool {
	if !strideIdentifier(e.AdapterID) || !strideIdentifier(e.KeyID) || e.KeyVersion == 0 || e.MinimumKeyVersion == 0 || e.MinimumKeyVersion > e.KeyVersion || !isHexDigest(e.KeyRefsDigest) ||
		e.MinimumGeneration == 0 || !releaseCommitPattern.MatchString(e.ReleaseCommit) || e.W4Mode != strideE10W4NetworkMode ||
		!isHexDigest(e.W4ActivationID) || !isHexDigest(e.W4ActivationReceiptDigest) ||
		e.W4Generation == 0 || e.W4SchemaVersion == 0 || !strideIdentifier(e.SessionAuthorityID) ||
		!strideIdentifier(e.PurgeExecutorID) || !strideIdentifier(e.PolicyID) || e.PolicyRevision < 1 || !isHexDigest(e.PolicyDigest) ||
		!strideIdentifier(e.QualificationReceiptID) || e.QualificationRevision < 1 || !isHexDigest(e.QualificationDigest) ||
		!strideIdentifier(e.BindingTenantID) || !strideIdentifier(e.BindingCohortID) || !isHexDigest(e.BindingDigest) {
		return false
	}
	seen := map[string]bool{}
	for _, path := range e.paths() {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || seen[path] {
			return false
		}
		seen[path] = true
	}
	return true
}

type StrideE10W6ManagedProductionAttestation struct {
	Contract                  string
	AdapterID                 string
	KeyID                     string
	KeyVersion                uint64
	MinimumKeyVersion         uint64
	KeyRefsDigest             string
	PolicyPath                string
	QualificationPath         string
	BindingPath               string
	ShadowSnapshotPath        string
	PurgeStorePath            string
	MinimumGeneration         uint64
	ReleaseCommit             string
	W4Mode                    string
	W4ActivationID            string
	W4ActivationReceiptDigest string
	W4Generation              uint64
	W4SchemaVersion           uint64
	SessionAuthorityID        string
	SessionAuthorityContract  string
	PurgeExecutorID           string
	PurgeExecutorContract     string
	PolicyID                  string
	PolicyRevision            int64
	PolicyDigest              string
	QualificationReceiptID    string
	QualificationRevision     int64
	QualificationDigest       string
	BindingTenantID           string
	BindingCohortID           string
	BindingDigest             string
	ObservedAt                time.Time
	ExpiresAt                 time.Time
}

type StrideE10W6ManagedProductionAdapters struct {
	Key           W6ManagedMACKey
	RetainedKeys  []W6ManagedMACKey
	Sessions      StrideE10W6SessionAuthority
	PurgeExecutor STRIDENetworkShadowPurgeExecutor
}

// StrideE10W6ManagedProductionProvider is a compiled external trust boundary.
// This repository deliberately supplies no environment-key or local provider.
type StrideE10W6ManagedProductionProvider interface {
	PreflightStrideE10W6ManagedProduction(context.Context, StrideE10W6ManagedProductionExpectation) (StrideE10W6ManagedProductionAdapters, StrideE10W6ManagedProductionAttestation, error)
}

var strideE10W6ManagedProviderState struct {
	sync.RWMutex
	provider StrideE10W6ManagedProductionProvider
}

var strideE10W6ProductionRuntimeState struct {
	sync.Mutex
	runtime *StrideE10W6Runtime
	cancel  context.CancelFunc
	done    chan struct{}
	claimed bool
}

func currentStrideE10W6ProductionRuntime() *StrideE10W6Runtime {
	strideE10W6ProductionRuntimeState.Lock()
	defer strideE10W6ProductionRuntimeState.Unlock()
	return strideE10W6ProductionRuntimeState.runtime
}

func closeStrideE10W6ProductionRuntime() {
	strideE10W6ProductionRuntimeState.Lock()
	cancel, done := strideE10W6ProductionRuntimeState.cancel, strideE10W6ProductionRuntimeState.done
	strideE10W6ProductionRuntimeState.runtime = nil
	strideE10W6ProductionRuntimeState.cancel = nil
	strideE10W6ProductionRuntimeState.done = nil
	strideE10W6ProductionRuntimeState.claimed = false
	strideE10W6ProductionRuntimeState.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func runStrideE10W6ProductionWorker(ctx context.Context, runtime *StrideE10W6Runtime, done chan<- struct{}) {
	defer close(done)
	if runtime == nil {
		return
	}
	ticker := time.NewTicker(strideE10W6ProductionWorkerInterval)
	defer ticker.Stop()
	process := func() {
		if err := runtime.PersistShadowIfChanged(); err != nil {
			return
		}
		for attempts := 0; attempts < 64; attempts++ {
			_, changed, err := runtime.ProcessPurgeWork(ctx)
			if err != nil || !changed {
				return
			}
		}
	}
	process()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			process()
		}
	}
}

func claimStrideE10W6ProductionWorker() (func(), error) {
	strideE10W6ProductionRuntimeState.Lock()
	defer strideE10W6ProductionRuntimeState.Unlock()
	if strideE10W6ProductionRuntimeState.runtime != nil || strideE10W6ProductionRuntimeState.cancel != nil || strideE10W6ProductionRuntimeState.claimed {
		return nil, ErrStrideE10W6RuntimeConflict
	}
	strideE10W6ProductionRuntimeState.claimed = true
	return func() {
		strideE10W6ProductionRuntimeState.Lock()
		if strideE10W6ProductionRuntimeState.runtime == nil && strideE10W6ProductionRuntimeState.cancel == nil {
			strideE10W6ProductionRuntimeState.claimed = false
		}
		strideE10W6ProductionRuntimeState.Unlock()
	}, nil
}

func installStrideE10W6ProductionWorker(runtime *StrideE10W6Runtime) error {
	if runtime == nil {
		return ErrStrideE10W6RuntimeUnavailable
	}
	strideE10W6ProductionRuntimeState.Lock()
	if strideE10W6ProductionRuntimeState.runtime != nil || strideE10W6ProductionRuntimeState.cancel != nil || !strideE10W6ProductionRuntimeState.claimed {
		strideE10W6ProductionRuntimeState.Unlock()
		return ErrStrideE10W6RuntimeConflict
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	strideE10W6ProductionRuntimeState.runtime = runtime
	strideE10W6ProductionRuntimeState.cancel = cancel
	strideE10W6ProductionRuntimeState.done = done
	strideE10W6ProductionRuntimeState.claimed = false
	strideE10W6ProductionRuntimeState.Unlock()
	go runStrideE10W6ProductionWorker(ctx, runtime, done)
	return nil
}

func strideE10W6ManagedProductionProvider() StrideE10W6ManagedProductionProvider {
	strideE10W6ManagedProviderState.RLock()
	defer strideE10W6ManagedProviderState.RUnlock()
	return strideE10W6ManagedProviderState.provider
}

func installStrideE10W6ManagedProductionProvider(provider StrideE10W6ManagedProductionProvider) func() {
	strideE10W6ManagedProviderState.Lock()
	prior := strideE10W6ManagedProviderState.provider
	strideE10W6ManagedProviderState.provider = provider
	strideE10W6ManagedProviderState.Unlock()
	return func() {
		strideE10W6ManagedProviderState.Lock()
		strideE10W6ManagedProviderState.provider = prior
		strideE10W6ManagedProviderState.Unlock()
	}
}

// InstallStrideE10W6ManagedProductionProvider is the compiled-provider
// composition seam. It is not an environment secret loader and enables no
// switch; callers must still satisfy the full startup preflight.
func InstallStrideE10W6ManagedProductionProvider(provider StrideE10W6ManagedProductionProvider) func() {
	return installStrideE10W6ManagedProductionProvider(provider)
}

func strideE10W6ManagedKeyRefsDigest(current W6ManagedMACKey, retained []W6ManagedMACKey) (string, error) {
	refs := make([]string, 0, len(retained)+1)
	seen := map[string]bool{}
	for _, key := range append([]W6ManagedMACKey{current}, retained...) {
		if !validStrideE10W6ManagedKey(key) || key.ID != current.ID || key.Version > current.Version {
			return "", ErrStrideE10W6RuntimeInvalid
		}
		ref := strideE10W6ManagedKeyRef(key.ID, key.Version)
		if seen[ref] {
			return "", ErrStrideE10W6RuntimeInvalid
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	digest := sha256.Sum256([]byte(strings.Join(refs, "\n")))
	return hex.EncodeToString(digest[:]), nil
}

func strideE10W6ManagedExpectationFromEnvironment() (string, StrideE10W6ManagedProductionExpectation, error) {
	mode := strings.TrimSpace(os.Getenv("STRIDE_E10_W6_MODE"))
	if mode == "" || mode == "off" {
		return "off", StrideE10W6ManagedProductionExpectation{}, nil
	}
	if mode != "managed" {
		return "", StrideE10W6ManagedProductionExpectation{}, ErrStrideE10W6RuntimeInvalid
	}
	parse := func(name string) (uint64, error) {
		return strconv.ParseUint(strings.TrimSpace(os.Getenv(name)), 10, 64)
	}
	parseRevision := func(name string) (int64, error) {
		return strconv.ParseInt(strings.TrimSpace(os.Getenv(name)), 10, 64)
	}
	keyVersion, keyErr := parse("STRIDE_E10_W6_KEY_VERSION")
	minimumKeyVersion, minimumKeyErr := parse("STRIDE_E10_W6_MINIMUM_KEY_VERSION")
	minimumGeneration, generationErr := parse("STRIDE_E10_W6_MINIMUM_GENERATION")
	w4Generation, w4GenerationErr := parse("STRIDE_E10_W6_W4_GENERATION")
	w4SchemaVersion, w4SchemaErr := parse("STRIDE_E10_W6_W4_SCHEMA_VERSION")
	policyRevision, policyRevisionErr := parseRevision("STRIDE_E10_W6_POLICY_REVISION")
	qualificationRevision, qualificationRevisionErr := parseRevision("STRIDE_E10_W6_QUALIFICATION_REVISION")
	if keyErr != nil || minimumKeyErr != nil || generationErr != nil || w4GenerationErr != nil || w4SchemaErr != nil || policyRevisionErr != nil || qualificationRevisionErr != nil {
		return "", StrideE10W6ManagedProductionExpectation{}, ErrStrideE10W6RuntimeInvalid
	}
	expectation := StrideE10W6ManagedProductionExpectation{
		AdapterID: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_MANAGED_ADAPTER_ID")), KeyID: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_KEY_ID")), KeyVersion: keyVersion, MinimumKeyVersion: minimumKeyVersion, KeyRefsDigest: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_KEY_REFS_SHA256")),
		PolicyPath: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_POLICY_PATH")), QualificationPath: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_QUALIFICATION_PATH")), BindingPath: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_BINDING_PATH")),
		ShadowSnapshotPath: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_SHADOW_SNAPSHOT_PATH")), PurgeStorePath: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_PURGE_STORE_PATH")), MinimumGeneration: minimumGeneration,
		ReleaseCommit: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_RELEASE_COMMIT")), W4ActivationID: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_W4_ACTIVATION_ID")), W4ActivationReceiptDigest: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_W4_RECEIPT_SHA256")),
		W4Mode:       strings.TrimSpace(os.Getenv("STRIDE_E10_W6_W4_MODE")),
		W4Generation: w4Generation, W4SchemaVersion: w4SchemaVersion, SessionAuthorityID: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_SESSION_AUTHORITY_ID")), PurgeExecutorID: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_PURGE_EXECUTOR_ID")),
		PolicyID: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_POLICY_ID")), PolicyRevision: policyRevision, PolicyDigest: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_POLICY_SHA256")),
		QualificationReceiptID: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_QUALIFICATION_RECEIPT_ID")), QualificationRevision: qualificationRevision, QualificationDigest: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_QUALIFICATION_SHA256")),
		BindingTenantID: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_BINDING_TENANT_ID")), BindingCohortID: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_BINDING_COHORT_ID")), BindingDigest: strings.TrimSpace(os.Getenv("STRIDE_E10_W6_BINDING_SHA256")),
	}
	if !expectation.valid() {
		return "", StrideE10W6ManagedProductionExpectation{}, ErrStrideE10W6RuntimeInvalid
	}
	return "managed", expectation, nil
}

func validateStrideE10W6ManagedAttestation(e StrideE10W6ManagedProductionExpectation, a StrideE10W6ManagedProductionAttestation, now time.Time) error {
	if !e.valid() || a.Contract != strideE10W6ManagedProductionContract || a.AdapterID != e.AdapterID || a.KeyID != e.KeyID || a.KeyVersion != e.KeyVersion || a.MinimumKeyVersion != e.MinimumKeyVersion || a.KeyRefsDigest != e.KeyRefsDigest ||
		a.PolicyPath != e.PolicyPath || a.QualificationPath != e.QualificationPath || a.BindingPath != e.BindingPath || a.ShadowSnapshotPath != e.ShadowSnapshotPath || a.PurgeStorePath != e.PurgeStorePath ||
		a.MinimumGeneration != e.MinimumGeneration || a.ReleaseCommit != e.ReleaseCommit || a.W4Mode != e.W4Mode || a.W4ActivationID != e.W4ActivationID || a.W4ActivationReceiptDigest != e.W4ActivationReceiptDigest ||
		a.W4Generation != e.W4Generation || a.W4SchemaVersion != e.W4SchemaVersion || a.SessionAuthorityID != e.SessionAuthorityID || a.SessionAuthorityContract != strideE10W6SessionAuthorityContract ||
		a.PurgeExecutorID != e.PurgeExecutorID || a.PurgeExecutorContract != strideE10W6PurgeExecutorContract || a.ObservedAt.IsZero() || a.ExpiresAt.IsZero() ||
		a.PolicyID != e.PolicyID || a.PolicyRevision != e.PolicyRevision || a.PolicyDigest != e.PolicyDigest || a.QualificationReceiptID != e.QualificationReceiptID || a.QualificationRevision != e.QualificationRevision || a.QualificationDigest != e.QualificationDigest ||
		a.BindingTenantID != e.BindingTenantID || a.BindingCohortID != e.BindingCohortID || a.BindingDigest != e.BindingDigest ||
		a.ObservedAt.After(now) || !now.Before(a.ExpiresAt) || a.ExpiresAt.Sub(a.ObservedAt) > 15*time.Minute {
		return ErrStrideE10W6RuntimeInvalid
	}
	return nil
}

type strideE10W6PathIdentity struct {
	Path          string
	FileDev       uint64
	FileIno       uint64
	FileSize      int64
	ContentDigest string
	ParentDev     uint64
	ParentIno     uint64
}

func preflightStrideE10W6Path(path string) (strideE10W6PathIdentity, error) {
	var identity strideE10W6PathIdentity
	parent := filepath.Dir(path)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil || resolvedParent != parent {
		return identity, ErrStrideE10W6RuntimeInvalid
	}
	if rejectUnsafeMyMindCustodyPath(path) != nil || rejectUnsafeMyMindCustodyPath(path+".lock") != nil {
		return identity, ErrStrideE10W6RuntimeInvalid
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 || parentInfo.Mode().Perm()&0o077 != 0 {
		return identity, ErrStrideE10W6RuntimeInvalid
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		return identity, ErrStrideE10W6RuntimeInvalid
	}
	parentStat, parentOK := parentInfo.Sys().(*syscall.Stat_t)
	fileStat, fileOK := info.Sys().(*syscall.Stat_t)
	if !parentOK || !fileOK || int(parentStat.Uid) != os.Geteuid() || int(fileStat.Uid) != os.Geteuid() || fileStat.Nlink != 1 {
		return identity, ErrStrideE10W6RuntimeInvalid
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return identity, ErrStrideE10W6RuntimeInvalid
	}
	defer file.Close()
	openedInfo, openedErr := file.Stat()
	if openedErr != nil {
		return identity, ErrStrideE10W6RuntimeInvalid
	}
	openedStat, openedOK := openedInfo.Sys().(*syscall.Stat_t)
	body, readErr := io.ReadAll(io.LimitReader(file, 16<<20+1))
	afterInfo, afterErr := file.Stat()
	if afterErr != nil {
		return identity, ErrStrideE10W6RuntimeInvalid
	}
	afterStat, afterOK := afterInfo.Sys().(*syscall.Stat_t)
	if !openedOK || readErr != nil || len(body) == 0 || len(body) > 16<<20 || !afterOK ||
		uint64(openedStat.Dev) != uint64(fileStat.Dev) || uint64(openedStat.Ino) != uint64(fileStat.Ino) || openedInfo.Size() != info.Size() ||
		openedStat.Dev != afterStat.Dev || openedStat.Ino != afterStat.Ino || openedInfo.Size() != afterInfo.Size() || !openedInfo.ModTime().Equal(afterInfo.ModTime()) {
		return identity, ErrStrideE10W6RuntimeInvalid
	}
	identity = strideE10W6PathIdentity{Path: path, FileDev: uint64(fileStat.Dev), FileIno: uint64(fileStat.Ino), FileSize: info.Size(), ContentDigest: sha256Hex(body), ParentDev: uint64(parentStat.Dev), ParentIno: uint64(parentStat.Ino)}
	return identity, nil
}

func preflightStrideE10W6Paths(paths []string) ([]strideE10W6PathIdentity, error) {
	identities := make([]strideE10W6PathIdentity, 0, len(paths))
	infos := make([]os.FileInfo, 0, len(paths))
	for _, path := range paths {
		identity, err := preflightStrideE10W6Path(path)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return nil, ErrStrideE10W6RuntimeInvalid
		}
		for _, prior := range infos {
			if os.SameFile(prior, info) {
				return nil, ErrStrideE10W6RuntimeInvalid
			}
		}
		infos = append(infos, info)
		identities = append(identities, identity)
	}
	return identities, nil
}

func strideE10W6SemanticDigest(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return sha256Hex(body)
}

func validateStrideE10W6SemanticFiles(expectation StrideE10W6ManagedProductionExpectation) error {
	policy, policyErr := loadStrideE10W6JSON[W6NetworkPolicyRevision](expectation.PolicyPath)
	qualification, qualificationErr := loadStrideE10W6JSON[W6NetworkQualificationReceipt](expectation.QualificationPath)
	binding, bindingErr := loadStrideE10W6JSON[StrideE10W6RuntimeBinding](expectation.BindingPath)
	if policyErr != nil || qualificationErr != nil || bindingErr != nil || policy.PolicyID != expectation.PolicyID || policy.Revision != expectation.PolicyRevision || strideE10W6SemanticDigest(policy) != expectation.PolicyDigest ||
		qualification.ReceiptID != expectation.QualificationReceiptID || qualification.Revision != expectation.QualificationRevision || strideE10W6SemanticDigest(qualification) != expectation.QualificationDigest ||
		binding.TenantID != expectation.BindingTenantID || binding.CohortID != expectation.BindingCohortID || strideE10W6SemanticDigest(binding) != expectation.BindingDigest ||
		binding.PolicyID != expectation.PolicyID || binding.PolicyRevision != expectation.PolicyRevision || binding.QualificationReceiptID != expectation.QualificationReceiptID || binding.QualificationRevision != expectation.QualificationRevision {
		return ErrStrideE10W6RuntimeInvalid
	}
	return nil
}

func strideE10W6W4MatchesExpectationLocked(expectation StrideE10W6ManagedProductionExpectation) bool {
	return strideE10W4RuntimeState.ready && strideE10W4RuntimeState.mode == expectation.W4Mode && strideE10W4RuntimeState.activationID == expectation.W4ActivationID &&
		strideE10W4RuntimeState.activationReceiptDigest == expectation.W4ActivationReceiptDigest && strideE10W4RuntimeState.generation == expectation.W4Generation &&
		strideE10W4RuntimeState.schemaVersion == expectation.W4SchemaVersion
}

func PreflightStrideE10W6ProductionRuntime(ctx context.Context, expectation StrideE10W6ManagedProductionExpectation, provider StrideE10W6ManagedProductionProvider, now time.Time) (StrideE10W6RuntimeConfig, StrideE10W6SessionAuthority, error) {
	if ctx == nil || provider == nil || now.IsZero() || !expectation.valid() {
		return StrideE10W6RuntimeConfig{}, nil, ErrStrideE10W6RuntimeInvalid
	}
	pathIdentities, err := preflightStrideE10W6Paths(expectation.paths())
	if err != nil {
		return StrideE10W6RuntimeConfig{}, nil, err
	}
	if err := validateStrideE10W6SemanticFiles(expectation); err != nil {
		return StrideE10W6RuntimeConfig{}, nil, err
	}
	release := currentReleaseIdentity()
	w4 := strideE10W4ReadinessSnapshot()
	if !release.ProcessQualified || release.ReleaseCommit != expectation.ReleaseCommit || w4["ready"] != true || w4["mode"] != expectation.W4Mode || w4["activationId"] != expectation.W4ActivationID ||
		w4["activationReceiptDigest"] != expectation.W4ActivationReceiptDigest || w4["generation"] != expectation.W4Generation || w4["schemaVersion"] != expectation.W4SchemaVersion {
		return StrideE10W6RuntimeConfig{}, nil, ErrStrideE10W6RuntimeInvalid
	}
	adapters, attestation, err := provider.PreflightStrideE10W6ManagedProduction(ctx, expectation)
	keyRefsDigest, keyRefsErr := strideE10W6ManagedKeyRefsDigest(adapters.Key, adapters.RetainedKeys)
	if err != nil || keyRefsErr != nil || keyRefsDigest != expectation.KeyRefsDigest || adapters.Sessions == nil || adapters.PurgeExecutor == nil || !validStrideE10W6ManagedKey(adapters.Key) || adapters.Key.ID != expectation.KeyID || adapters.Key.Version != expectation.KeyVersion ||
		validateStrideE10W6ManagedAttestation(expectation, attestation, now.UTC()) != nil {
		return StrideE10W6RuntimeConfig{}, nil, ErrStrideE10W6RuntimeInvalid
	}
	currentIdentities, pathErr := preflightStrideE10W6Paths(expectation.paths())
	currentRelease := currentReleaseIdentity()
	currentW4 := strideE10W4ReadinessSnapshot()
	if pathErr != nil || len(currentIdentities) != len(pathIdentities) || currentRelease != release || currentW4["ready"] != true || currentW4["mode"] != expectation.W4Mode || currentW4["activationId"] != expectation.W4ActivationID || currentW4["activationReceiptDigest"] != expectation.W4ActivationReceiptDigest || currentW4["generation"] != expectation.W4Generation || currentW4["schemaVersion"] != expectation.W4SchemaVersion {
		return StrideE10W6RuntimeConfig{}, nil, ErrStrideE10W6RuntimeInvalid
	}
	for i := range pathIdentities {
		if pathIdentities[i] != currentIdentities[i] {
			return StrideE10W6RuntimeConfig{}, nil, ErrStrideE10W6RuntimeInvalid
		}
	}
	finalAdmission := func(use func() error) error {
		if use == nil {
			return ErrStrideE10W6RuntimeInvalid
		}
		identities, identityErr := preflightStrideE10W6Paths(expectation.paths())
		if identityErr != nil || len(identities) != len(pathIdentities) || currentReleaseIdentity() != release || validateStrideE10W6ManagedAttestation(expectation, attestation, strideE10W6ProductionClock().UTC()) != nil || validateStrideE10W6SemanticFiles(expectation) != nil {
			return ErrStrideE10W6RuntimeInvalid
		}
		for i := range pathIdentities {
			if identities[i] != pathIdentities[i] {
				return ErrStrideE10W6RuntimeInvalid
			}
		}
		strideE10W4RuntimeState.RLock()
		defer strideE10W4RuntimeState.RUnlock()
		if !strideE10W6W4MatchesExpectationLocked(expectation) || currentReleaseIdentity() != release {
			return ErrStrideE10W6RuntimeInvalid
		}
		return use()
	}
	config := StrideE10W6RuntimeConfig{PolicyPath: expectation.PolicyPath, QualificationPath: expectation.QualificationPath, BindingPath: expectation.BindingPath, ShadowSnapshotPath: expectation.ShadowSnapshotPath, PurgeStorePath: expectation.PurgeStorePath, Key: cloneStrideE10W6ManagedKey(adapters.Key), RetainedKeys: cloneStrideE10W6ManagedKeys(adapters.RetainedKeys), MinimumKeyVersion: expectation.MinimumKeyVersion, PurgeExecutor: adapters.PurgeExecutor, MinimumGeneration: expectation.MinimumGeneration, Now: strideE10W6ProductionClock, FinalAdmission: finalAdmission}
	return config, adapters.Sessions, nil
}

func strideE10W6AnySwitchEnabled() bool {
	if strideE10LiveProductRuntime == nil {
		return false
	}
	for _, feature := range append(append([]STRIDEFeature{}, strideE10W6ActivationSwitches...), strideE10W6AlwaysDisabledSwitches...) {
		if strideE10LiveProductRuntime.Enabled(feature) {
			return true
		}
	}
	return false
}

func installStrideE10W6ProductionRuntimeFromEnvironment(ctx context.Context) error {
	mode, expectation, err := strideE10W6ManagedExpectationFromEnvironment()
	if err != nil {
		return err
	}
	if mode == "off" {
		if strideE10W6AnySwitchEnabled() {
			return ErrStrideE10W6RuntimeUnavailable
		}
		return nil
	}
	if strideE10W6AnySwitchEnabled() {
		return ErrStrideE10W6RuntimeUnavailable
	}
	releaseWorkerClaim, err := claimStrideE10W6ProductionWorker()
	if err != nil {
		return err
	}
	defer releaseWorkerClaim()
	config, sessions, err := PreflightStrideE10W6ProductionRuntime(ctx, expectation, strideE10W6ManagedProductionProvider(), strideE10W6ProductionClock().UTC())
	if err != nil {
		return err
	}
	if strideE10LiveProductRuntime == nil {
		return ErrStrideE10W6RuntimeUnavailable
	}
	runtime, err := InstallStrideE10W6ProductionRuntime(ctx, strideE10LiveProductRuntime, sessions, config)
	if err != nil {
		return err
	}
	return installStrideE10W6ProductionWorker(runtime)
}
