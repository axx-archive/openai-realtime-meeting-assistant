package main

// This runtime is deliberately uninstalled and accepts synthetic fixtures only.
// It exercises the PI0-A durability and data-rights contracts without collecting
// product traffic or creating a production baseline.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

const (
	StridePI0RuntimeOff           = "off"
	StridePI0RuntimeSyntheticOnly = "synthetic_only"
	stridePI0GovernanceSchema     = "stride.pi0.synthetic-governance.v1"
	stridePI0GovernanceDomain     = "meetingassist/stride/pi0/synthetic-governance/v1"
	stridePI0ExportSchema         = "stride.pi0.synthetic-export-manifest.v1"
	stridePI0BackupSchema         = "stride.pi0.synthetic-backup-manifest.v1"
	stridePI0BaselineSchema       = "stride.pi0.synthetic-pre-migration-baseline.v1"
)

type StridePI0CurrentConsent interface {
	WithCurrentStridePI0Consent(context.Context, StridePI0Principal, []StridePI0Reference, func() error) error
}

type StridePI0SyntheticRuntimeConfig struct {
	Mode                string
	CarrierPath         string
	GovernancePath      string
	Keys                StridePI0ManagedMACKeyring
	CarrierHighWater    StridePI0CarrierHighWaterStore
	GovernanceHighWater StridePI0CarrierHighWaterStore
	Authority           StridePI0CurrentAuthority
	Consent             StridePI0CurrentConsent
}

type StridePI0SyntheticRuntime struct {
	mode       string
	carrier    *StridePI0FileCarrier
	governance *stridePI0SyntheticGovernanceStore
	keys       StridePI0ManagedMACKeyring
	authority  StridePI0CurrentAuthority
	consent    StridePI0CurrentConsent
}

func OpenStridePI0SyntheticRuntime(ctx context.Context, config StridePI0SyntheticRuntimeConfig) (*StridePI0SyntheticRuntime, error) {
	if config.Mode == StridePI0RuntimeOff {
		return &StridePI0SyntheticRuntime{mode: StridePI0RuntimeOff}, nil
	}
	if config.Mode != StridePI0RuntimeSyntheticOnly || config.Keys == nil || config.CarrierHighWater == nil || config.GovernanceHighWater == nil || config.Authority == nil || config.Consent == nil || !stridePI0CleanAbsolutePath(config.CarrierPath) || !stridePI0CleanAbsolutePath(config.GovernancePath) || config.CarrierPath == config.GovernancePath {
		return nil, ErrStridePI0Unavailable
	}
	if _, _, _, err := stridePI0CurrentSeparatedKeys(ctx, config.Keys); err != nil {
		return nil, ErrStridePI0Unavailable
	}
	// Governance is the admission boundary for suppression, deletion, export,
	// and retention. It must open before the carrier so a failed governance
	// preflight cannot create, reseal, recover, or lock the carrier.
	governance, err := openStridePI0SyntheticGovernanceStore(ctx, config.GovernancePath, config.Keys, config.GovernanceHighWater)
	if err != nil {
		return nil, err
	}
	carrier, err := OpenStridePI0FileCarrier(ctx, config.CarrierPath, config.Keys, config.CarrierHighWater)
	if err != nil {
		return nil, err
	}
	return &StridePI0SyntheticRuntime{mode: config.Mode, carrier: carrier, governance: governance, keys: config.Keys, authority: config.Authority, consent: config.Consent}, nil
}

func stridePI0CleanAbsolutePath(path string) bool {
	return path != "" && filepath.IsAbs(path) && filepath.Clean(path) == path
}

func (r *StridePI0SyntheticRuntime) Carrier() (*StridePI0FileCarrier, error) {
	if r == nil || r.mode != StridePI0RuntimeSyntheticOnly || r.carrier == nil {
		return nil, ErrStridePI0Unavailable
	}
	return r.carrier, nil
}

func stridePI0SyntheticPrincipal(principal StridePI0Principal) bool {
	return principal.validate() == nil && len(principal.OrganizationID) > 10 && principal.OrganizationID[:10] == "synthetic_" && (principal.Kind != "human" || (len(principal.PersonID) > 10 && principal.PersonID[:10] == "synthetic_"))
}

func (r *StridePI0SyntheticRuntime) Append(ctx context.Context, principal StridePI0Principal, operationID, fingerprint string, fence StridePI0Postimage, events []StridePI0LifecycleEvent, at time.Time) (StridePI0EventAppendReceipt, error) {
	if r == nil || r.mode != StridePI0RuntimeSyntheticOnly || !stridePI0SyntheticPrincipal(principal) || len(events) == 0 {
		return StridePI0EventAppendReceipt{}, ErrStridePI0Unavailable
	}
	consentRefs := append([]StridePI0Reference(nil), events[0].ConsentRefs...)
	for _, event := range events {
		if event.Principal != principal || event.TenantID != principal.OrganizationID || !stridePI0ExactReferences(event.ConsentRefs, consentRefs) || !event.Retention.RetainUntil.After(at) {
			return StridePI0EventAppendReceipt{}, ErrStridePI0Conflict
		}
	}
	var receipt StridePI0EventAppendReceipt
	err := r.withAuthorityAndConsent(ctx, principal, consentRefs, func() error {
		return r.governance.withLock(func() error {
			if recoverErr := r.governance.recover(ctx); recoverErr != nil {
				return recoverErr
			}
			state, readErr := r.governance.readUnlocked(ctx)
			if readErr != nil {
				return readErr
			}
			if suppressed, suppressErr := r.eventsSuppressedByTombstone(ctx, state, events); suppressErr != nil || suppressed {
				if suppressErr != nil {
					return suppressErr
				}
				return ErrStridePI0Conflict
			}
			var appendErr error
			receipt, appendErr = r.carrier.appendEventsOnce(ctx, principal, operationID, fingerprint, fence, events, at)
			return appendErr
		})
	})
	if err != nil {
		return StridePI0EventAppendReceipt{}, ErrStridePI0Unavailable
	}
	return receipt, nil
}

// AppendCorrection is append-only: it cannot overwrite the original event and
// accepts only the contract's exact correction lifecycle event.
func (r *StridePI0SyntheticRuntime) AppendCorrection(ctx context.Context, principal StridePI0Principal, operationID, fingerprint string, fence StridePI0Postimage, correction StridePI0LifecycleEvent, at time.Time) (StridePI0EventAppendReceipt, error) {
	if correction.EventType != "lifecycle.corrected" {
		return StridePI0EventAppendReceipt{}, ErrStridePI0Invalid
	}
	return r.Append(ctx, principal, operationID, fingerprint, fence, []StridePI0LifecycleEvent{correction}, at)
}

func stridePI0ExactReferences(a, b []StridePI0Reference) bool {
	left, _ := canonicalJSON(a)
	right, _ := canonicalJSON(b)
	return bytes.Equal(left, right)
}

func (r *StridePI0SyntheticRuntime) scopeCommitment(ctx context.Context, scope, scopeID string) (StridePI0ManagedCommitment, error) {
	return MintStridePI0ManagedCommitment(ctx, r.keys, stridePI0IdempotencyDomain, "synthetic-scope", scope, scopeID)
}

func (r *StridePI0SyntheticRuntime) eventsSuppressedByTombstone(ctx context.Context, state stridePI0SyntheticGovernanceState, events []StridePI0LifecycleEvent) (bool, error) {
	for _, event := range events {
		eventRef := StridePI0Reference(event.Aggregate)
		for _, tombstone := range state.Tombstones {
			if stridePI0ReferenceInSet(eventRef, tombstone.SuppressedEventRefs) {
				return true, nil
			}
		}
		for _, candidate := range []struct{ scope, id string }{{"subject", event.Principal.PersonID}, {"trace", event.TraceID}} {
			if candidate.id == "" {
				continue
			}
			commitment, err := r.scopeCommitment(ctx, candidate.scope, candidate.id)
			if err != nil {
				return false, err
			}
			for _, tombstone := range state.Tombstones {
				if tombstone.ScopeCommitment == commitment {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

type StridePI0SyntheticTombstone struct {
	Scope               string                     `json:"scope"`
	ScopeCommitment     StridePI0ManagedCommitment `json:"scopeCommitment"`
	ReceiptID           string                     `json:"receiptId"`
	Generation          uint64                     `json:"generation"`
	DeletedAt           time.Time                  `json:"deletedAt"`
	SuppressedEvents    []string                   `json:"suppressedEvents"`
	SuppressedEventRefs []StridePI0Reference       `json:"suppressedEventRefs"`
	SuppressedOps       []string                   `json:"suppressedOperations"`
	EventCount          int                        `json:"eventCount"`
	ExportCount         int                        `json:"exportCount"`
	IndexCount          int                        `json:"indexCount"`
	JournalCount        int                        `json:"journalCount"`
	DerivedCount        int                        `json:"derivedCount"`
}

type StridePI0SyntheticPurgeReceipt struct {
	ReceiptID       string                     `json:"receiptId"`
	Scope           string                     `json:"scope"`
	ScopeCommitment StridePI0ManagedCommitment `json:"scopeCommitment"`
	Generation      uint64                     `json:"generation"`
	EventCount      int                        `json:"eventCount"`
	ExportCount     int                        `json:"exportCount"`
	IndexCount      int                        `json:"indexCount"`
	JournalCount    int                        `json:"journalCount"`
	DerivedCount    int                        `json:"derivedCount"`
	CompletedAt     time.Time                  `json:"completedAt"`
}

type StridePI0SyntheticExportManifest struct {
	Schema          string                     `json:"schema"`
	ExportID        string                     `json:"exportId"`
	Scope           string                     `json:"scope"`
	ScopeCommitment StridePI0ManagedCommitment `json:"scopeCommitment"`
	EventRefs       []StridePI0Reference       `json:"eventRefs"`
	IssuedAt        time.Time                  `json:"issuedAt"`
	ExpiresAt       time.Time                  `json:"expiresAt"`
	Generation      uint64                     `json:"generation"`
	KeyID           string                     `json:"keyId"`
	KeyVersion      uint64                     `json:"keyVersion"`
	MAC             string                     `json:"mac"`
}

type StridePI0SyntheticBackupManifest struct {
	Schema               string                       `json:"schema"`
	BackupID             string                       `json:"backupId"`
	CarrierHighWater     StridePI0CarrierHighWater    `json:"carrierHighWater"`
	GovernanceHighWater  StridePI0CarrierHighWater    `json:"governanceHighWater"`
	TombstoneCommitments []StridePI0ManagedCommitment `json:"tombstoneCommitments"`
	CreatedAt            time.Time                    `json:"createdAt"`
	KeyID                string                       `json:"keyId"`
	KeyVersion           uint64                       `json:"keyVersion"`
	MAC                  string                       `json:"mac"`
}

type StridePI0SyntheticBaselineManifest struct {
	Schema        string           `json:"schema"`
	EvidenceClass string           `json:"evidenceClass"`
	ManifestID    string           `json:"manifestId"`
	ReleaseDigest string           `json:"releaseDigest"`
	SchemaDigest  string           `json:"schemaDigest"`
	PolicyDigest  string           `json:"policyDigest"`
	SwitchDigest  string           `json:"switchDigest"`
	Counts        map[string]int64 `json:"counts"`
	Eligible      int64            `json:"eligible"`
	Linked        int64            `json:"linked"`
	Unknown       int64            `json:"unknown"`
	CapturedAt    time.Time        `json:"capturedAt"`
	KeyID         string           `json:"keyId"`
	KeyVersion    uint64           `json:"keyVersion"`
	MAC           string           `json:"mac"`
}

type stridePI0SyntheticGovernanceState struct {
	Schema        string                             `json:"schema"`
	Generation    uint64                             `json:"generation"`
	HighWater     uint64                             `json:"highWater"`
	Tombstones    []StridePI0SyntheticTombstone      `json:"tombstones"`
	PurgeReceipts []StridePI0SyntheticPurgeReceipt   `json:"purgeReceipts"`
	Exports       []StridePI0SyntheticExportManifest `json:"exports"`
	KeyID         string                             `json:"keyId"`
	KeyVersion    uint64                             `json:"keyVersion"`
	MAC           string                             `json:"mac"`
}

type stridePI0SyntheticGovernanceStore struct {
	path      string
	txnPath   string
	lockPath  string
	keys      StridePI0ManagedMACKeyring
	highWater StridePI0CarrierHighWaterStore
	mu        sync.Mutex
}

type stridePI0SyntheticGovernanceTxn struct {
	Schema     string                            `json:"schema"`
	Prior      StridePI0CarrierHighWater         `json:"prior"`
	Next       StridePI0CarrierHighWater         `json:"next"`
	NextState  stridePI0SyntheticGovernanceState `json:"nextState"`
	KeyID      string                            `json:"keyId"`
	KeyVersion uint64                            `json:"keyVersion"`
	MAC        string                            `json:"mac"`
}

func stridePI0GovernancePayload(state stridePI0SyntheticGovernanceState) ([]byte, error) {
	state.MAC = ""
	return canonicalJSON(struct {
		Domain string                            `json:"domain"`
		State  stridePI0SyntheticGovernanceState `json:"state"`
	}{stridePI0GovernanceDomain, state})
}

func stridePI0SealGovernance(ctx context.Context, keys StridePI0ManagedMACKeyring, state stridePI0SyntheticGovernanceState) (stridePI0SyntheticGovernanceState, error) {
	key, err := stridePI0CurrentStateKey(ctx, keys)
	if err != nil {
		return state, ErrStridePI0Unavailable
	}
	state.Schema, state.KeyID, state.KeyVersion, state.MAC = stridePI0GovernanceSchema, key.ID, key.Version, ""
	payload, err := stridePI0GovernancePayload(state)
	if err != nil {
		return state, ErrStridePI0Invalid
	}
	state.MAC = stridePI0MAC(key.Secret, payload)
	return state, nil
}

func stridePI0VerifyGovernance(ctx context.Context, keys StridePI0ManagedMACKeyring, state stridePI0SyntheticGovernanceState) error {
	if state.Schema != stridePI0GovernanceSchema || state.Generation < 1 || state.HighWater < state.Generation || !strideIdentifier(state.KeyID) || state.KeyVersion < 1 || !isHexDigest(state.MAC) {
		return ErrStridePI0Invalid
	}
	for _, tombstone := range state.Tombstones {
		if !oneOf(tombstone.Scope, "subject", "trace", "retention") || tombstone.ScopeCommitment.validate(stridePI0IdempotencyDomain) != nil || !strideIdentifier(tombstone.ReceiptID) || tombstone.Generation < 1 || tombstone.DeletedAt.IsZero() || tombstone.EventCount < 0 || tombstone.ExportCount < 0 || tombstone.IndexCount < 0 || tombstone.JournalCount < 0 || tombstone.DerivedCount < 0 || len(tombstone.SuppressedEvents) != tombstone.EventCount || len(tombstone.SuppressedEventRefs) != tombstone.EventCount {
			return ErrStridePI0Invalid
		}
		for index, eventID := range tombstone.SuppressedEvents {
			if !strideIdentifier(eventID) || tombstone.SuppressedEventRefs[index].validate() != nil {
				return ErrStridePI0Invalid
			}
		}
		for _, operationID := range tombstone.SuppressedOps {
			if !strideIdentifier(operationID) {
				return ErrStridePI0Invalid
			}
		}
	}
	key, err := stridePI0ResolveStateKey(ctx, keys, state.KeyID, state.KeyVersion)
	payload, payloadErr := stridePI0GovernancePayload(state)
	if err != nil || payloadErr != nil || !hmac.Equal([]byte(state.MAC), []byte(stridePI0MAC(key.Secret, payload))) {
		return ErrStridePI0Invalid
	}
	return nil
}

func stridePI0GovernanceDigest(state stridePI0SyntheticGovernanceState) string {
	raw, _ := canonicalJSON(state)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func openStridePI0SyntheticGovernanceStore(ctx context.Context, path string, keys StridePI0ManagedMACKeyring, highWater StridePI0CarrierHighWaterStore) (*stridePI0SyntheticGovernanceStore, error) {
	store := &stridePI0SyntheticGovernanceStore{path: path, txnPath: path + ".txn", lockPath: path + ".lock", keys: keys, highWater: highWater}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, ErrStridePI0Unavailable
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		state, sealErr := stridePI0SealGovernance(ctx, keys, stridePI0SyntheticGovernanceState{Generation: 1, HighWater: 1})
		if sealErr != nil {
			return nil, sealErr
		}
		if writeErr := stridePI0AtomicWrite(path, state); writeErr != nil {
			return nil, writeErr
		}
		mark := StridePI0CarrierHighWater{Generation: 1, Digest: stridePI0GovernanceDigest(state)}
		if casErr := highWater.CompareAndSwapStridePI0CarrierHighWater(ctx, path, StridePI0CarrierHighWater{}, mark); casErr != nil {
			return nil, ErrStridePI0Conflict
		}
	} else if err != nil {
		return nil, ErrStridePI0Unavailable
	}
	if err := store.withLock(func() error {
		if err := store.recover(ctx); err != nil {
			return err
		}
		_, err := store.readUnlocked(ctx)
		return err
	}); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *stridePI0SyntheticGovernanceStore) withLock(effect func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return ErrStridePI0Unavailable
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX) != nil {
		return ErrStridePI0Unavailable
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return effect()
}

func (s *stridePI0SyntheticGovernanceStore) readUnlocked(ctx context.Context) (stridePI0SyntheticGovernanceState, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return stridePI0SyntheticGovernanceState{}, ErrStridePI0Unavailable
	}
	var state stridePI0SyntheticGovernanceState
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&state) != nil || stridePI0EnsureEOF(decoder) != nil || stridePI0VerifyGovernance(ctx, s.keys, state) != nil {
		return state, ErrStridePI0Invalid
	}
	mark, err := s.highWater.ReadStridePI0CarrierHighWater(ctx, s.path)
	if err != nil || mark != (StridePI0CarrierHighWater{Generation: state.Generation, Digest: stridePI0GovernanceDigest(state)}) {
		return state, ErrStridePI0Conflict
	}
	return state, nil
}

func (s *stridePI0SyntheticGovernanceStore) read(ctx context.Context) (stridePI0SyntheticGovernanceState, error) {
	var state stridePI0SyntheticGovernanceState
	err := s.withLock(func() error {
		if err := s.recover(ctx); err != nil {
			return err
		}
		var readErr error
		state, readErr = s.readUnlocked(ctx)
		return readErr
	})
	return state, err
}

func (s *stridePI0SyntheticGovernanceStore) txnMAC(secret []byte, txn stridePI0SyntheticGovernanceTxn) string {
	txn.MAC = ""
	raw, _ := canonicalJSON(struct {
		Domain      string                          `json:"domain"`
		Transaction stridePI0SyntheticGovernanceTxn `json:"transaction"`
	}{"meetingassist/stride/pi0/synthetic-governance-transaction/v1", txn})
	return stridePI0MAC(secret, raw)
}

func (s *stridePI0SyntheticGovernanceStore) recover(ctx context.Context) error {
	raw, err := os.ReadFile(s.txnPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return ErrStridePI0Unavailable
	}
	var txn stridePI0SyntheticGovernanceTxn
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&txn) != nil || stridePI0EnsureEOF(decoder) != nil || txn.Schema != "stride.pi0.synthetic-governance-transaction.v1" || stridePI0VerifyGovernance(ctx, s.keys, txn.NextState) != nil {
		return ErrStridePI0Invalid
	}
	key, err := stridePI0ResolveStateKey(ctx, s.keys, txn.KeyID, txn.KeyVersion)
	if err != nil || !hmac.Equal([]byte(txn.MAC), []byte(s.txnMAC(key.Secret, txn))) || txn.Next != (StridePI0CarrierHighWater{Generation: txn.NextState.Generation, Digest: stridePI0GovernanceDigest(txn.NextState)}) {
		return ErrStridePI0Invalid
	}
	mark, err := s.highWater.ReadStridePI0CarrierHighWater(ctx, s.path)
	if err != nil {
		return ErrStridePI0Unavailable
	}
	if mark == txn.Prior {
		if err := s.highWater.CompareAndSwapStridePI0CarrierHighWater(ctx, s.path, txn.Prior, txn.Next); err != nil {
			return ErrStridePI0Conflict
		}
	} else if mark != txn.Next {
		return ErrStridePI0Conflict
	}
	if err := stridePI0AtomicWrite(s.path, txn.NextState); err != nil {
		return err
	}
	if err := os.Remove(s.txnPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrStridePI0Unavailable
	}
	return stridePI0SyncDirectory(filepath.Dir(s.path))
}

func (s *stridePI0SyntheticGovernanceStore) mutate(ctx context.Context, change func(*stridePI0SyntheticGovernanceState) error) error {
	return s.withLock(func() error {
		if err := s.recover(ctx); err != nil {
			return err
		}
		state, err := s.readUnlocked(ctx)
		if err != nil {
			return err
		}
		next := state
		next.Tombstones = append([]StridePI0SyntheticTombstone(nil), state.Tombstones...)
		next.PurgeReceipts = append([]StridePI0SyntheticPurgeReceipt(nil), state.PurgeReceipts...)
		next.Exports = append([]StridePI0SyntheticExportManifest(nil), state.Exports...)
		if err := change(&next); err != nil {
			if errors.Is(err, errStridePI0NoMutation) {
				return nil
			}
			return err
		}
		next.Generation, next.HighWater = state.Generation+1, state.HighWater+1
		next, err = stridePI0SealGovernance(ctx, s.keys, next)
		if err != nil {
			return err
		}
		prior := StridePI0CarrierHighWater{Generation: state.Generation, Digest: stridePI0GovernanceDigest(state)}
		mark := StridePI0CarrierHighWater{Generation: next.Generation, Digest: stridePI0GovernanceDigest(next)}
		key, err := stridePI0CurrentStateKey(ctx, s.keys)
		if err != nil {
			return err
		}
		txn := stridePI0SyntheticGovernanceTxn{Schema: "stride.pi0.synthetic-governance-transaction.v1", Prior: prior, Next: mark, NextState: next, KeyID: key.ID, KeyVersion: key.Version}
		txn.MAC = s.txnMAC(key.Secret, txn)
		if err := stridePI0AtomicWrite(s.txnPath, txn); err != nil {
			return err
		}
		if err := s.highWater.CompareAndSwapStridePI0CarrierHighWater(ctx, s.path, prior, mark); err != nil {
			return ErrStridePI0Conflict
		}
		if err := stridePI0AtomicWrite(s.path, next); err != nil {
			return err
		}
		if err := os.Remove(s.txnPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return ErrStridePI0Unavailable
		}
		return stridePI0SyncDirectory(filepath.Dir(s.path))
	})
}

func (r *StridePI0SyntheticRuntime) withAuthorityAndConsent(ctx context.Context, principal StridePI0Principal, consentRefs []StridePI0Reference, effect func() error) error {
	if r == nil || r.mode != StridePI0RuntimeSyntheticOnly || !stridePI0SyntheticPrincipal(principal) {
		return ErrStridePI0Unavailable
	}
	return r.consent.WithCurrentStridePI0Consent(ctx, principal, consentRefs, func() error { return r.authority.WithCurrentStridePI0Principal(ctx, principal, effect) })
}

type stridePI0SyntheticPurgePlan struct {
	eventIDs     []string
	operationIDs []string
	eventRefs    []StridePI0Reference
	eventCount   int
	journalCount int
	derivedCount int
}

func (r *StridePI0SyntheticRuntime) carrierPurgePlan(ctx context.Context, eventMatches func(StridePI0LifecycleEvent) bool, journalMatches func(StridePI0CompoundJournal) bool) (stridePI0SyntheticPurgePlan, error) {
	var plan stridePI0SyntheticPurgePlan
	err := r.carrier.withLock(func() error {
		state, err := r.carrier.readLocked(ctx)
		if err != nil {
			return err
		}
		operations := map[string]bool{}
		for _, event := range state.Events {
			if !eventMatches(event) {
				continue
			}
			plan.eventIDs = append(plan.eventIDs, event.EventID)
			plan.eventRefs = append(plan.eventRefs, StridePI0Reference(event.Aggregate))
			plan.eventCount++
			if event.JournalOperationID != "" {
				operations[event.JournalOperationID] = true
			}
		}
		for _, journal := range state.Journals {
			if journalMatches(journal) || operations[journal.OperationID] {
				operations[journal.OperationID] = true
				plan.journalCount++
			}
		}
		for _, receipt := range state.AppendReceipts {
			if operations[receipt.OperationID] {
				plan.derivedCount++
			}
		}
		for operationID := range operations {
			plan.operationIDs = append(plan.operationIDs, operationID)
		}
		sort.Strings(plan.eventIDs)
		sort.Strings(plan.operationIDs)
		return nil
	})
	return plan, err
}

func stridePI0ReferenceInSet(ref StridePI0Reference, refs []StridePI0Reference) bool {
	for _, candidate := range refs {
		if candidate == ref {
			return true
		}
	}
	return false
}

func stridePI0ExportMatchesPlan(manifest StridePI0SyntheticExportManifest, commitment StridePI0ManagedCommitment, refs []StridePI0Reference) bool {
	if manifest.ScopeCommitment == commitment {
		return true
	}
	for _, ref := range manifest.EventRefs {
		if stridePI0ReferenceInSet(ref, refs) {
			return true
		}
	}
	return false
}

func stridePI0ExportCoveredByTombstone(manifest StridePI0SyntheticExportManifest, tombstone StridePI0SyntheticTombstone) bool {
	return stridePI0ExportMatchesPlan(manifest, tombstone.ScopeCommitment, tombstone.SuppressedEventRefs)
}

func stridePI0RefsCoveredByTombstone(refs []StridePI0Reference, tombstone StridePI0SyntheticTombstone) bool {
	for _, ref := range refs {
		if stridePI0ReferenceInSet(ref, tombstone.SuppressedEventRefs) {
			return true
		}
	}
	return false
}

// finalizePurge performs the last export sweep and publishes the measured
// completion receipt in one authenticated governance generation. A crash or
// lost response can therefore expose either the pending tombstone or the exact
// completed postimage, never a receipt that leaves a covered export readable.
func (r *StridePI0SyntheticRuntime) finalizePurge(ctx context.Context, receiptID, scope string, commitment StridePI0ManagedCommitment, at time.Time) (StridePI0SyntheticPurgeReceipt, error) {
	var receipt StridePI0SyntheticPurgeReceipt
	err := r.governance.mutate(ctx, func(state *stridePI0SyntheticGovernanceState) error {
		for _, existing := range state.PurgeReceipts {
			if existing.ReceiptID != receiptID {
				continue
			}
			if existing.Scope != scope || existing.ScopeCommitment != commitment {
				return ErrStridePI0Conflict
			}
			receipt = existing
			return errStridePI0NoMutation
		}
		tombstoneIndex := -1
		for index, existing := range state.Tombstones {
			if existing.ReceiptID == receiptID && existing.Scope == scope && existing.ScopeCommitment == commitment {
				tombstoneIndex = index
				break
			}
		}
		if tombstoneIndex < 0 {
			return ErrStridePI0Conflict
		}
		tombstone := state.Tombstones[tombstoneIndex]
		keptExports := state.Exports[:0]
		finalExportCount := 0
		for _, export := range state.Exports {
			if stridePI0ExportCoveredByTombstone(export, tombstone) {
				finalExportCount++
				continue
			}
			keptExports = append(keptExports, export)
		}
		state.Exports = keptExports
		tombstone.ExportCount += finalExportCount
		state.Tombstones[tombstoneIndex] = tombstone
		receipt = StridePI0SyntheticPurgeReceipt{
			ReceiptID: receiptID, Scope: scope, ScopeCommitment: commitment,
			Generation: tombstone.Generation, EventCount: tombstone.EventCount,
			ExportCount: tombstone.ExportCount, IndexCount: tombstone.IndexCount,
			JournalCount: tombstone.JournalCount, DerivedCount: tombstone.DerivedCount,
			CompletedAt: at.UTC(),
		}
		state.PurgeReceipts = append(state.PurgeReceipts, receipt)
		return nil
	})
	return receipt, err
}

func (r *StridePI0SyntheticRuntime) purgeCarrierPlan(ctx context.Context, plan stridePI0SyntheticPurgePlan) error {
	events, operations := map[string]bool{}, map[string]bool{}
	for _, id := range plan.eventIDs {
		events[id] = true
	}
	for _, id := range plan.operationIDs {
		operations[id] = true
	}
	return r.carrier.mutate(ctx, func(state *stridePI0CarrierState) error {
		changed := false
		keptEvents := state.Events[:0]
		for _, event := range state.Events {
			if events[event.EventID] {
				changed = true
				continue
			}
			keptEvents = append(keptEvents, event)
		}
		state.Events = keptEvents
		keptJournals := state.Journals[:0]
		for _, journal := range state.Journals {
			if operations[journal.OperationID] {
				changed = true
				continue
			}
			keptJournals = append(keptJournals, journal)
		}
		state.Journals = keptJournals
		keptReceipts := state.AppendReceipts[:0]
		for _, receipt := range state.AppendReceipts {
			if operations[receipt.OperationID] {
				changed = true
				continue
			}
			keptReceipts = append(keptReceipts, receipt)
		}
		state.AppendReceipts = keptReceipts
		if !changed {
			return errStridePI0NoMutation
		}
		return nil
	})
}

func (r *StridePI0SyntheticRuntime) DeleteScope(ctx context.Context, principal StridePI0Principal, consentRefs []StridePI0Reference, scope, scopeID, receiptID string, at time.Time) (StridePI0SyntheticPurgeReceipt, error) {
	if r == nil || r.mode != StridePI0RuntimeSyntheticOnly || !oneOf(scope, "subject", "trace") || !strideIdentifier(scopeID) || !strideIdentifier(receiptID) || at.IsZero() {
		return StridePI0SyntheticPurgeReceipt{}, ErrStridePI0Invalid
	}
	if (scope == "subject" && scopeID != principal.PersonID) || (scope == "trace" && principal.OrganizationID == "") {
		return StridePI0SyntheticPurgeReceipt{}, ErrStridePI0Unavailable
	}
	commitment, err := r.scopeCommitment(ctx, scope, scopeID)
	if err != nil {
		return StridePI0SyntheticPurgeReceipt{}, err
	}
	var tombstone StridePI0SyntheticTombstone
	var receipt StridePI0SyntheticPurgeReceipt
	completed := false
	err = r.withAuthorityAndConsent(ctx, principal, consentRefs, func() error {
		if err := r.governance.mutate(ctx, func(state *stridePI0SyntheticGovernanceState) error {
			for _, existingReceipt := range state.PurgeReceipts {
				if existingReceipt.ReceiptID != receiptID {
					continue
				}
				if existingReceipt.Scope != scope || existingReceipt.ScopeCommitment != commitment {
					return ErrStridePI0Conflict
				}
				receipt, completed = existingReceipt, true
				return errStridePI0NoMutation
			}
			for _, existing := range state.Tombstones {
				if existing.ScopeCommitment == commitment {
					if existing.ReceiptID != receiptID || existing.Scope != scope {
						return ErrStridePI0Conflict
					}
					tombstone = existing
					return errStridePI0NoMutation
				}
			}
			plan, planErr := r.carrierPurgePlan(ctx,
				func(event StridePI0LifecycleEvent) bool {
					if scope == "subject" {
						return event.TenantID == principal.OrganizationID && event.Principal.PersonID == scopeID
					}
					return event.TenantID == principal.OrganizationID && event.TraceID == scopeID
				},
				func(journal StridePI0CompoundJournal) bool {
					if scope == "subject" {
						return journal.TenantID == principal.OrganizationID && journal.Principal.PersonID == scopeID
					}
					return journal.TenantID == principal.OrganizationID && journal.TraceID == scopeID
				})
			if planErr != nil {
				return planErr
			}
			keptExports := state.Exports[:0]
			exportCount := 0
			for _, export := range state.Exports {
				if stridePI0ExportMatchesPlan(export, commitment, plan.eventRefs) {
					exportCount++
					continue
				}
				keptExports = append(keptExports, export)
			}
			state.Exports = keptExports
			tombstone = StridePI0SyntheticTombstone{Scope: scope, ScopeCommitment: commitment, ReceiptID: receiptID, Generation: state.HighWater + 1, DeletedAt: at.UTC(), SuppressedEvents: plan.eventIDs, SuppressedEventRefs: plan.eventRefs, SuppressedOps: plan.operationIDs, EventCount: plan.eventCount, ExportCount: exportCount, JournalCount: plan.journalCount, DerivedCount: plan.derivedCount}
			state.Tombstones = append(state.Tombstones, tombstone)
			return nil
		}); err != nil || completed {
			return err
		}
		plan := stridePI0SyntheticPurgePlan{eventIDs: append([]string(nil), tombstone.SuppressedEvents...), operationIDs: append([]string(nil), tombstone.SuppressedOps...)}
		if err := r.purgeCarrierPlan(ctx, plan); err != nil {
			return err
		}
		var finalizeErr error
		receipt, finalizeErr = r.finalizePurge(ctx, receiptID, scope, commitment, at)
		return finalizeErr
	})
	if err != nil {
		return StridePI0SyntheticPurgeReceipt{}, err
	}
	return receipt, nil
}

func (r *StridePI0SyntheticRuntime) CreateExport(ctx context.Context, principal StridePI0Principal, consentRefs []StridePI0Reference, exportID, scope, scopeID string, refs []StridePI0Reference, issuedAt, expiresAt time.Time) (StridePI0SyntheticExportManifest, error) {
	if !strideIdentifier(exportID) || !oneOf(scope, "person", "organization", "public") || !strideIdentifier(scopeID) || len(refs) == 0 || len(refs) > 128 || expiresAt.After(issuedAt.Add(7*24*time.Hour)) || !expiresAt.After(issuedAt) {
		return StridePI0SyntheticExportManifest{}, ErrStridePI0Invalid
	}
	if (scope == "person" && scopeID != principal.PersonID) || (scope == "organization" && scopeID != principal.OrganizationID) || (scope == "public" && scopeID != principal.OrganizationID) {
		return StridePI0SyntheticExportManifest{}, ErrStridePI0Unavailable
	}
	if !stridePI0UniqueReferences(refs) {
		return StridePI0SyntheticExportManifest{}, ErrStridePI0Invalid
	}
	for _, ref := range refs {
		if ref.validate() != nil {
			return StridePI0SyntheticExportManifest{}, ErrStridePI0Invalid
		}
	}
	commitmentScope := scope
	if scope == "person" {
		commitmentScope = "subject"
	}
	commitment, err := r.scopeCommitment(ctx, commitmentScope, scopeID)
	if err != nil {
		return StridePI0SyntheticExportManifest{}, err
	}
	manifest := StridePI0SyntheticExportManifest{Schema: stridePI0ExportSchema, ExportID: exportID, Scope: scope, ScopeCommitment: commitment, EventRefs: append([]StridePI0Reference(nil), refs...), IssuedAt: issuedAt.UTC(), ExpiresAt: expiresAt.UTC()}
	err = r.withAuthorityAndConsent(ctx, principal, consentRefs, func() error {
		return r.governance.mutate(ctx, func(state *stridePI0SyntheticGovernanceState) error {
			for _, tombstone := range state.Tombstones {
				if tombstone.ScopeCommitment == commitment || stridePI0RefsCoveredByTombstone(refs, tombstone) {
					return ErrStridePI0Conflict
				}
			}
			for _, existing := range state.Exports {
				if existing.ExportID != exportID {
					continue
				}
				if !stridePI0SameExportRequest(existing, manifest) {
					return ErrStridePI0Conflict
				}
				manifest = existing
				return errStridePI0NoMutation
			}
			if readErr := r.authorizedExportRefs(ctx, principal, scope, refs); readErr != nil {
				return readErr
			}
			manifest.Generation = state.HighWater + 1
			sealed, sealErr := r.sealExport(ctx, manifest)
			if sealErr != nil {
				return sealErr
			}
			manifest = sealed
			state.Exports = append(state.Exports, sealed)
			return nil
		})
	})
	return manifest, err
}

func stridePI0SameExportRequest(left, right StridePI0SyntheticExportManifest) bool {
	return left.Schema == right.Schema && left.ExportID == right.ExportID && left.Scope == right.Scope && left.ScopeCommitment == right.ScopeCommitment && stridePI0ExactReferences(left.EventRefs, right.EventRefs) && left.IssuedAt.Equal(right.IssuedAt) && left.ExpiresAt.Equal(right.ExpiresAt)
}

func (r *StridePI0SyntheticRuntime) authorizedExportRefs(ctx context.Context, principal StridePI0Principal, scope string, refs []StridePI0Reference) error {
	return r.carrier.withLock(func() error {
		state, err := r.carrier.readLocked(ctx)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			found := false
			for _, event := range state.Events {
				if event.Aggregate.Type != ref.Type || event.Aggregate.ID != ref.ID || event.Aggregate.Revision != ref.Revision || event.Aggregate.Digest != ref.Digest {
					continue
				}
				switch scope {
				case "person":
					found = event.Principal.Kind == "human" && event.Principal.PersonID == principal.PersonID && event.Audience.Visibility != "public"
				case "organization":
					found = event.TenantID == principal.OrganizationID && event.Audience.Visibility == "organization"
				case "public":
					found = event.TenantID == principal.OrganizationID && event.Audience.Visibility == "public"
				}
				if found {
					break
				}
			}
			if !found {
				return ErrStridePI0Unavailable
			}
		}
		return nil
	})
}

func (r *StridePI0SyntheticRuntime) sealExport(ctx context.Context, manifest StridePI0SyntheticExportManifest) (StridePI0SyntheticExportManifest, error) {
	key, err := stridePI0CurrentStateKey(ctx, r.keys)
	if err != nil {
		return manifest, err
	}
	manifest.KeyID, manifest.KeyVersion, manifest.MAC = key.ID, key.Version, ""
	payload, err := canonicalJSON(struct {
		Domain   string                           `json:"domain"`
		Manifest StridePI0SyntheticExportManifest `json:"manifest"`
	}{"meetingassist/stride/pi0/synthetic-export/v1", manifest})
	if err != nil {
		return manifest, err
	}
	manifest.MAC = stridePI0MAC(key.Secret, payload)
	return manifest, nil
}

func (r *StridePI0SyntheticRuntime) RetentionCandidates(ctx context.Context, at time.Time) ([]string, error) {
	if r == nil || r.mode != StridePI0RuntimeSyntheticOnly || at.IsZero() {
		return nil, ErrStridePI0Unavailable
	}
	governance, err := r.governance.read(ctx)
	if err != nil {
		return nil, err
	}
	suppressed := map[string]bool{}
	for _, tombstone := range governance.Tombstones {
		for _, eventID := range tombstone.SuppressedEvents {
			suppressed[eventID] = true
		}
	}
	var ids []string
	err = r.carrier.withLock(func() error {
		state, err := r.carrier.readLocked(ctx)
		if err != nil {
			return err
		}
		for _, event := range state.Events {
			if !event.Retention.RetainUntil.After(at) && !suppressed[event.EventID] {
				ids = append(ids, event.EventID)
			}
		}
		return nil
	})
	sort.Strings(ids)
	return ids, err
}

// ApplyRetention is the mutating retention seam. Candidate discovery is never
// authority: current consent and exact principal authority remain held through
// tombstone admission, carrier deletion, and the completed measured receipt.
func (r *StridePI0SyntheticRuntime) ApplyRetention(ctx context.Context, principal StridePI0Principal, consentRefs []StridePI0Reference, receiptID string, at time.Time) (StridePI0SyntheticPurgeReceipt, error) {
	if r == nil || r.mode != StridePI0RuntimeSyntheticOnly || !stridePI0SyntheticPrincipal(principal) || !strideIdentifier(receiptID) || at.IsZero() {
		return StridePI0SyntheticPurgeReceipt{}, ErrStridePI0Invalid
	}
	commitment, err := MintStridePI0ManagedCommitment(ctx, r.keys, stridePI0IdempotencyDomain, "synthetic-retention", principal.OrganizationID, principal.PersonID, receiptID, at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return StridePI0SyntheticPurgeReceipt{}, err
	}
	var tombstone StridePI0SyntheticTombstone
	var receipt StridePI0SyntheticPurgeReceipt
	completed := false
	err = r.withAuthorityAndConsent(ctx, principal, consentRefs, func() error {
		if err := r.governance.mutate(ctx, func(state *stridePI0SyntheticGovernanceState) error {
			for _, existing := range state.PurgeReceipts {
				if existing.ReceiptID != receiptID {
					continue
				}
				if existing.Scope != "retention" || existing.ScopeCommitment != commitment {
					return ErrStridePI0Conflict
				}
				receipt, completed = existing, true
				return errStridePI0NoMutation
			}
			for _, existing := range state.Tombstones {
				if existing.ScopeCommitment != commitment {
					continue
				}
				if existing.Scope != "retention" || existing.ReceiptID != receiptID {
					return ErrStridePI0Conflict
				}
				tombstone = existing
				return errStridePI0NoMutation
			}
			plan, planErr := r.carrierPurgePlan(ctx,
				func(event StridePI0LifecycleEvent) bool {
					return event.Principal == principal && event.TenantID == principal.OrganizationID && !event.Retention.RetainUntil.After(at)
				},
				func(StridePI0CompoundJournal) bool { return false })
			if planErr != nil {
				return planErr
			}
			keptExports := state.Exports[:0]
			exportCount := 0
			for _, export := range state.Exports {
				if stridePI0ExportMatchesPlan(export, StridePI0ManagedCommitment{}, plan.eventRefs) {
					exportCount++
					continue
				}
				keptExports = append(keptExports, export)
			}
			state.Exports = keptExports
			tombstone = StridePI0SyntheticTombstone{Scope: "retention", ScopeCommitment: commitment, ReceiptID: receiptID, Generation: state.HighWater + 1, DeletedAt: at.UTC(), SuppressedEvents: plan.eventIDs, SuppressedEventRefs: plan.eventRefs, SuppressedOps: plan.operationIDs, EventCount: plan.eventCount, ExportCount: exportCount, JournalCount: plan.journalCount, DerivedCount: plan.derivedCount}
			state.Tombstones = append(state.Tombstones, tombstone)
			return nil
		}); err != nil || completed {
			return err
		}
		if err := r.purgeCarrierPlan(ctx, stridePI0SyntheticPurgePlan{eventIDs: append([]string(nil), tombstone.SuppressedEvents...), operationIDs: append([]string(nil), tombstone.SuppressedOps...)}); err != nil {
			return err
		}
		var finalizeErr error
		receipt, finalizeErr = r.finalizePurge(ctx, receiptID, "retention", commitment, at)
		return finalizeErr
	})
	if err != nil {
		return StridePI0SyntheticPurgeReceipt{}, err
	}
	return receipt, nil
}

func (r *StridePI0SyntheticRuntime) CreateBackupManifest(ctx context.Context, backupID string, at time.Time) (StridePI0SyntheticBackupManifest, error) {
	if r == nil || r.mode != StridePI0RuntimeSyntheticOnly || !strideIdentifier(backupID) || at.IsZero() {
		return StridePI0SyntheticBackupManifest{}, ErrStridePI0Unavailable
	}
	carrierMark, err := r.carrier.highWater.ReadStridePI0CarrierHighWater(ctx, r.carrier.path)
	if err != nil {
		return StridePI0SyntheticBackupManifest{}, err
	}
	governanceState, err := r.governance.read(ctx)
	if err != nil {
		return StridePI0SyntheticBackupManifest{}, err
	}
	manifest := StridePI0SyntheticBackupManifest{Schema: stridePI0BackupSchema, BackupID: backupID, CarrierHighWater: carrierMark, GovernanceHighWater: StridePI0CarrierHighWater{Generation: governanceState.Generation, Digest: stridePI0GovernanceDigest(governanceState)}, CreatedAt: at.UTC()}
	for _, tombstone := range governanceState.Tombstones {
		manifest.TombstoneCommitments = append(manifest.TombstoneCommitments, tombstone.ScopeCommitment)
	}
	key, err := stridePI0CurrentStateKey(ctx, r.keys)
	if err != nil {
		return manifest, err
	}
	manifest.KeyID, manifest.KeyVersion = key.ID, key.Version
	manifest.MAC = r.backupMAC(key.Secret, manifest)
	return manifest, nil
}

func (r *StridePI0SyntheticRuntime) backupMAC(secret []byte, manifest StridePI0SyntheticBackupManifest) string {
	manifest.MAC = ""
	raw, _ := canonicalJSON(struct {
		Domain   string                           `json:"domain"`
		Manifest StridePI0SyntheticBackupManifest `json:"manifest"`
	}{"meetingassist/stride/pi0/synthetic-backup/v1", manifest})
	return stridePI0MAC(secret, raw)
}

func (r *StridePI0SyntheticRuntime) VerifyBackupForRestore(ctx context.Context, manifest StridePI0SyntheticBackupManifest) error {
	if r == nil || r.mode != StridePI0RuntimeSyntheticOnly || manifest.Schema != stridePI0BackupSchema || !strideIdentifier(manifest.BackupID) || !isHexDigest(manifest.MAC) {
		return ErrStridePI0Invalid
	}
	key, err := stridePI0ResolveStateKey(ctx, r.keys, manifest.KeyID, manifest.KeyVersion)
	if err != nil || !hmac.Equal([]byte(manifest.MAC), []byte(r.backupMAC(key.Secret, manifest))) {
		return ErrStridePI0Invalid
	}
	carrierMark, err := r.carrier.highWater.ReadStridePI0CarrierHighWater(ctx, r.carrier.path)
	if err != nil {
		return ErrStridePI0Unavailable
	}
	state, err := r.governance.read(ctx)
	if err != nil {
		return err
	}
	if manifest.CarrierHighWater != carrierMark || manifest.GovernanceHighWater != (StridePI0CarrierHighWater{Generation: state.Generation, Digest: stridePI0GovernanceDigest(state)}) || len(manifest.TombstoneCommitments) != len(state.Tombstones) {
		return ErrStridePI0Conflict
	}
	for i := range state.Tombstones {
		if manifest.TombstoneCommitments[i] != state.Tombstones[i].ScopeCommitment {
			return ErrStridePI0Conflict
		}
	}
	return nil
}

func (r *StridePI0SyntheticRuntime) MintSyntheticBaseline(ctx context.Context, manifestID, releaseDigest, schemaDigest, policyDigest, switchDigest string, counts map[string]int64, eligible, linked, unknown int64, at time.Time) (StridePI0SyntheticBaselineManifest, error) {
	if r == nil || r.mode != StridePI0RuntimeSyntheticOnly || !strideIdentifier(manifestID) || !isHexDigest(releaseDigest) || !isHexDigest(schemaDigest) || !isHexDigest(policyDigest) || !isHexDigest(switchDigest) || eligible < 0 || linked < 0 || unknown < 0 || linked > eligible || at.IsZero() {
		return StridePI0SyntheticBaselineManifest{}, ErrStridePI0Invalid
	}
	if counts == nil || counts["eligible"] != eligible || counts["linked"] != linked || counts["unknown"] != unknown {
		return StridePI0SyntheticBaselineManifest{}, ErrStridePI0Invalid
	}
	for key, count := range counts {
		if !oneOf(key, "eligible", "linked", "partial", "unknown", "invalid", "legacy_import") || count < 0 {
			return StridePI0SyntheticBaselineManifest{}, ErrStridePI0Invalid
		}
	}
	if linked+unknown+counts["partial"]+counts["invalid"] != eligible || counts["legacy_import"] > linked {
		return StridePI0SyntheticBaselineManifest{}, ErrStridePI0Invalid
	}
	closedCounts := make(map[string]int64, len(counts))
	for key, count := range counts {
		closedCounts[key] = count
	}
	manifest := StridePI0SyntheticBaselineManifest{Schema: stridePI0BaselineSchema, EvidenceClass: "synthetic_deterministic_only_not_real_baseline", ManifestID: manifestID, ReleaseDigest: releaseDigest, SchemaDigest: schemaDigest, PolicyDigest: policyDigest, SwitchDigest: switchDigest, Counts: closedCounts, Eligible: eligible, Linked: linked, Unknown: unknown, CapturedAt: at.UTC()}
	key, err := stridePI0CurrentStateKey(ctx, r.keys)
	if err != nil {
		return manifest, err
	}
	manifest.KeyID, manifest.KeyVersion = key.ID, key.Version
	raw, _ := canonicalJSON(struct {
		Domain   string                             `json:"domain"`
		Manifest StridePI0SyntheticBaselineManifest `json:"manifest"`
	}{"meetingassist/stride/pi0/synthetic-baseline/v1", manifest})
	manifest.MAC = stridePI0MAC(key.Secret, raw)
	return manifest, nil
}
