package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/openai/openai-realtime-meeting-assistant/internal/e10evidence"
)

const (
	qualificationLedgerAnchorSchema     = "stride.e10.qualification-ledger-anchor/v1"
	qualificationAnchorAuthorityTimeout = 5 * time.Second
)

// QualificationEvidenceStore is deliberately not a provider trust root. Local
// seed evidence can only produce structure-only candidates. Separately, the
// store can durably import opaque qualification capabilities minted by
// e10evidence after independent trust-root, registry, source-packet, evaluator,
// and dual-signature verification; it cannot mint those capabilities itself.
type QualificationEvidenceStore struct {
	mu                              sync.Mutex
	ledgerPath                      string
	tenantID                        string
	now                             func() time.Time
	attempts                        map[string]StoredProviderAttemptEvidence
	targets                         map[string]StoredTranscriptionEvidenceTarget
	dictation                       map[string]StoredDictationEvidenceBatch
	consumed                        map[string]bool
	trustedResults                  map[string]StoredTrustedQualificationResult
	trustedSources                  map[string]bool
	trustedPackets                  map[string]bool
	sequence                        int64
	lastDigest                      string
	approvedRoots                   e10evidence.ApprovedTrustRoots
	approvedTrustRootSHA256         string
	anchorAuthority                 QualificationEvidenceAnchorAuthority
	anchorTimeout                   time.Duration
	exactAnchor                     *QualificationLedgerAnchor
	authorityReconciliationRequired bool
	write                           func(*os.File, []byte) (int, error)
	sync                            func(*os.File) error
}

// QualificationLedgerAnchor is the externally persisted minimum trusted
// prefix for one tenant's qualification ledger. A local ledger file or its
// recomputable hash chain is not an independent anchor.
type QualificationLedgerAnchor struct {
	SchemaVersion string `json:"schemaVersion"`
	TenantID      string `json:"tenantId"`
	Sequence      int64  `json:"sequence"`
	Digest        string `json:"digest"`
}

// QualificationEvidenceTrustRootAnchor is returned by external custody. The
// store never accepts roots and their alleged pin as sibling local arguments.
type QualificationEvidenceTrustRootAnchor struct {
	TrustRootsRaw           []byte
	ApprovedTrustRootSHA256 string
}

// QualificationEvidenceAuthorityState is one atomic custody read. The trust
// root pin and ledger head must come from the same authority revision so a
// root rotation cannot race evidence verification.
type QualificationEvidenceAuthorityState struct {
	TrustRootAnchor QualificationEvidenceTrustRootAnchor
	LedgerAnchor    QualificationLedgerAnchor
}

// QualificationEvidenceAnchorAuthority is an external custody boundary. No
// production local/file implementation exists in this repository. A trusted
// store cannot open until an operator-provisioned authority supplies both the
// approved roots and the exact current tenant ledger head, and an import is not
// accepted until that authority compare-and-swaps the new head.
type QualificationEvidenceAnchorAuthority interface {
	QualificationAuthorityState(context.Context, string) (QualificationEvidenceAuthorityState, error)
	CompareAndSwapQualificationLedgerAnchor(context.Context, string, string, QualificationLedgerAnchor, QualificationLedgerAnchor) error
}

type QualificationEvidenceTrustConfig struct {
	AnchorAuthority QualificationEvidenceAnchorAuthority
}

// StoredProviderAttemptEvidence is locally supplied attempt evidence. Opaque
// provider digests are relationship bindings, not provider-authenticated
// receipts.
type StoredProviderAttemptEvidence struct {
	Ref         TranscriptionProviderAttemptRef `json:"ref"`
	Observation TranscriptionObservation        `json:"observation"`
}

type QualificationEvidenceSeed struct {
	ProviderAttempts     []StoredProviderAttemptEvidence     `json:"providerAttempts"`
	TranscriptionTargets []StoredTranscriptionEvidenceTarget `json:"transcriptionTargets"`
	DictationBatches     []StoredDictationEvidenceBatch      `json:"dictationBatches"`
}

// StoredTrustedQualificationResult is durable evidence that a separately
// anchored registry owner, operator, and independent reviewer signed an exact
// evaluator result over an exact signed source packet. It is not derived from
// QualificationEvidenceSeed and does not itself enable a route or release.
type StoredTrustedQualificationResult struct {
	Record     e10evidence.QualificationImportRecord `json:"record"`
	ImportedAt time.Time                             `json:"importedAt"`
}

type qualificationLedgerEvent struct {
	Sequence      int64                             `json:"sequence"`
	OccurredAt    time.Time                         `json:"occurredAt"`
	Kind          string                            `json:"kind"`
	TokenDigest   string                            `json:"tokenDigest"`
	PriorDigest   string                            `json:"priorDigest,omitempty"`
	Digest        string                            `json:"digest"`
	TrustedResult *StoredTrustedQualificationResult `json:"trustedResult,omitempty"`
	TrustedBundle json.RawMessage                   `json:"trustedBundle,omitempty"`
}

// OpenQualificationEvidenceStore admits caller-supplied seed evidence only into
// the local structure-only maps. There is no signing key or self-anchored
// registry here; the trusted-result import API accepts only opaque capabilities
// already verified against independently administered e10evidence trust roots.
func OpenQualificationEvidenceStore(ledgerPath string, seed QualificationEvidenceSeed, tenantID string, now func() time.Time) (*QualificationEvidenceStore, error) {
	return openQualificationEvidenceStore(ledgerPath, seed, tenantID, now, nil)
}

// OpenTrustedQualificationEvidenceStore is the only constructor that enables
// signed-bundle import or reload. Both independent anchors are mandatory.
func OpenTrustedQualificationEvidenceStore(ledgerPath string, seed QualificationEvidenceSeed, tenantID string, now func() time.Time, trust QualificationEvidenceTrustConfig) (*QualificationEvidenceStore, error) {
	if trust.AnchorAuthority == nil {
		return nil, errors.New("qualification evidence external anchor authority is required")
	}
	return openQualificationEvidenceStore(ledgerPath, seed, tenantID, now, &qualificationStoreTrust{anchorAuthority: trust.AnchorAuthority})
}

type qualificationStoreTrust struct {
	anchorAuthority QualificationEvidenceAnchorAuthority
}

func openQualificationEvidenceStore(ledgerPath string, seed QualificationEvidenceSeed, tenantID string, now func() time.Time, trust *qualificationStoreTrust) (*QualificationEvidenceStore, error) {
	if strings.TrimSpace(ledgerPath) == "" || !strideIdentifier(tenantID) || now == nil {
		return nil, errors.New("qualification evidence store configuration is invalid")
	}
	cloned := cloneQualificationEvidenceSeed(seed)
	store := &QualificationEvidenceStore{
		ledgerPath:     ledgerPath,
		tenantID:       tenantID,
		now:            now,
		attempts:       map[string]StoredProviderAttemptEvidence{},
		targets:        map[string]StoredTranscriptionEvidenceTarget{},
		dictation:      map[string]StoredDictationEvidenceBatch{},
		consumed:       map[string]bool{},
		trustedResults: map[string]StoredTrustedQualificationResult{},
		trustedSources: map[string]bool{},
		trustedPackets: map[string]bool{},
		anchorTimeout:  qualificationAnchorAuthorityTimeout,
		write: func(file *os.File, value []byte) (int, error) {
			return file.Write(value)
		},
		sync: func(file *os.File) error { return file.Sync() },
	}
	if trust != nil {
		store.anchorAuthority = trust.anchorAuthority
	}
	for _, attempt := range cloned.ProviderAttempts {
		token := attempt.Ref.Receipt
		if strings.TrimSpace(token) == "" || attempt.Ref.TenantID != tenantID || store.attempts[token].Ref.Receipt != "" {
			return nil, errors.New("qualification evidence store has an invalid or duplicate provider-attempt seed")
		}
		store.attempts[token] = attempt
	}
	for _, target := range cloned.TranscriptionTargets {
		token := target.Ref.Receipt
		if strings.TrimSpace(token) == "" || target.Ref.TenantID != tenantID || store.targets[token].Ref.Receipt != "" {
			return nil, errors.New("qualification evidence store has an invalid or duplicate transcription-target seed")
		}
		store.targets[token] = target
	}
	for _, batch := range cloned.DictationBatches {
		token := batch.Ref.Receipt
		if strings.TrimSpace(token) == "" || batch.Ref.TenantID != tenantID || store.dictation[token].Ref.Receipt != "" {
			return nil, errors.New("qualification evidence store has an invalid or duplicate dictation-batch seed")
		}
		store.dictation[token] = batch
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.anchorAuthority != nil {
		if err := store.refreshQualificationAuthorityLocked(context.Background()); err != nil {
			return nil, err
		}
	}
	file, created, err := store.openLedgerLocked()
	if err != nil {
		return nil, err
	}
	defer closeQualificationLedger(file)
	if err := store.reloadLedgerLocked(file); err != nil {
		return nil, err
	}
	if created {
		if err := store.sync(file); err != nil {
			return nil, fmt.Errorf("sync new qualification evidence ledger: %w", err)
		}
		if err := syncQualificationLedgerDirectory(filepath.Dir(ledgerPath)); err != nil {
			return nil, err
		}
	}
	return store, nil
}

// GenesisQualificationLedgerAnchor is the explicit external anchor for a new
// empty tenant ledger. It is deterministic but still must be supplied through
// the trusted constructor; the store never silently invents authority.
func GenesisQualificationLedgerAnchor(tenantID string) (QualificationLedgerAnchor, error) {
	if !strideIdentifier(tenantID) {
		return QualificationLedgerAnchor{}, errors.New("qualification ledger tenant is invalid")
	}
	return QualificationLedgerAnchor{SchemaVersion: qualificationLedgerAnchorSchema, TenantID: tenantID, Sequence: 0, Digest: qualificationLedgerGenesisDigest(tenantID)}, nil
}

func (store *QualificationEvidenceStore) refreshQualificationAuthorityLocked(ctx context.Context) error {
	if store == nil || store.anchorAuthority == nil || store.authorityReconciliationRequired {
		return errors.New("qualification evidence external anchor authority is unavailable")
	}
	authorityCtx, cancelAuthority := store.qualificationAnchorCallContext(ctx)
	state, err := store.anchorAuthority.QualificationAuthorityState(authorityCtx, store.tenantID)
	cancelAuthority()
	if err != nil {
		return fmt.Errorf("qualification evidence external authority state: %w", err)
	}
	return store.applyQualificationAuthorityStateLocked(state)
}

func (store *QualificationEvidenceStore) applyQualificationAuthorityStateLocked(state QualificationEvidenceAuthorityState) error {
	trust := state.TrustRootAnchor
	approved, err := e10evidence.LoadApprovedTrustRoots(trust.TrustRootsRaw, trust.ApprovedTrustRootSHA256)
	if err != nil {
		return fmt.Errorf("qualification evidence approved trust roots: %w", err)
	}
	anchor := state.LedgerAnchor
	if err := validateQualificationLedgerAnchor(anchor, store.tenantID); err != nil {
		return err
	}
	store.approvedRoots = approved
	store.approvedTrustRootSHA256 = trust.ApprovedTrustRootSHA256
	store.exactAnchor = &anchor
	return nil
}

func (store *QualificationEvidenceStore) qualificationAnchorCallContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	timeout := store.anchorTimeout
	if timeout <= 0 {
		timeout = qualificationAnchorAuthorityTimeout
	}
	return context.WithTimeout(parent, timeout)
}

// ImportVerifiedQualificationResult deliberately refuses the old in-process
// capability path: it cannot carry the signed material needed for independent
// cross-process or restart verification. Use ImportQualificationBundle.
func (store *QualificationEvidenceStore) ImportVerifiedQualificationResult(verified e10evidence.VerifiedQualificationResult) (StoredTrustedQualificationResult, error) {
	_ = store
	_ = verified
	return StoredTrustedQualificationResult{}, errors.New("trusted qualification import requires a complete serialized signed bundle and external anchors")
}

// ImportQualificationBundle re-verifies the complete serialized signature
// chain against store-configured approved roots, appends the exact bundle, and
// returns the new head for external compare-and-swap custody.
func (store *QualificationEvidenceStore) ImportQualificationBundle(bundleRaw []byte) (StoredTrustedQualificationResult, QualificationLedgerAnchor, error) {
	if store == nil || store.anchorAuthority == nil {
		return StoredTrustedQualificationResult{}, QualificationLedgerAnchor{}, errors.New("trusted qualification import requires external trust-root and ledger anchors")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	file, _, err := store.openLedgerLocked()
	if err != nil {
		return StoredTrustedQualificationResult{}, QualificationLedgerAnchor{}, err
	}
	defer closeQualificationLedger(file)
	if err := store.refreshQualificationAuthorityLocked(context.Background()); err != nil {
		return StoredTrustedQualificationResult{}, QualificationLedgerAnchor{}, err
	}
	if err := store.reloadLedgerLocked(file); err != nil {
		return StoredTrustedQualificationResult{}, QualificationLedgerAnchor{}, err
	}
	_, verified, err := e10evidence.VerifyQualificationImportBundle(bundleRaw, store.approvedRoots)
	if err != nil {
		return StoredTrustedQualificationResult{}, QualificationLedgerAnchor{}, err
	}
	record, err := e10evidence.QualificationImport(verified)
	if err != nil || record.TenantID != store.tenantID {
		return StoredTrustedQualificationResult{}, QualificationLedgerAnchor{}, errors.New("trusted qualification result denied")
	}
	stored := StoredTrustedQualificationResult{Record: record, ImportedAt: store.now().UTC()}
	if _, exists := store.trustedResults[record.ResultID]; exists || store.trustedSources[record.SourcePacketSHA256] || store.trustedPackets[record.ResultPacketSHA256] {
		return StoredTrustedQualificationResult{}, QualificationLedgerAnchor{}, errors.New("trusted qualification result or source packet was already imported")
	}
	startOffset, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return StoredTrustedQualificationResult{}, QualificationLedgerAnchor{}, err
	}
	previousAnchor := store.currentLedgerAnchorLocked()
	anchor, err := store.appendTrustedResultLocked(file, stored, bundleRaw)
	if err != nil {
		return StoredTrustedQualificationResult{}, QualificationLedgerAnchor{}, err
	}
	if err := store.commitQualificationAnchorCASLocked(file, startOffset, previousAnchor, anchor); err != nil {
		return StoredTrustedQualificationResult{}, QualificationLedgerAnchor{}, err
	}
	return stored, anchor, nil
}

// CurrentQualificationLedgerAnchor returns the fully scanned current head. It
// is an observation to publish through independent custody, not by itself an
// approved minimum anchor for another process.
func (store *QualificationEvidenceStore) CurrentQualificationLedgerAnchor() (QualificationLedgerAnchor, error) {
	if store == nil {
		return QualificationLedgerAnchor{}, errors.New("qualification evidence store is absent")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	file, _, err := store.openLedgerLocked()
	if err != nil {
		return QualificationLedgerAnchor{}, err
	}
	defer closeQualificationLedger(file)
	if store.anchorAuthority != nil {
		if err := store.refreshQualificationAuthorityLocked(context.Background()); err != nil {
			return QualificationLedgerAnchor{}, err
		}
	}
	if err := store.reloadLedgerLocked(file); err != nil {
		return QualificationLedgerAnchor{}, err
	}
	return store.currentLedgerAnchorLocked(), nil
}

func (store *QualificationEvidenceStore) TrustedQualificationResult(resultID string) (StoredTrustedQualificationResult, bool, error) {
	if store == nil {
		return StoredTrustedQualificationResult{}, false, errors.New("qualification evidence store is absent")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	file, _, err := store.openLedgerLocked()
	if err != nil {
		return StoredTrustedQualificationResult{}, false, err
	}
	defer closeQualificationLedger(file)
	if store.anchorAuthority != nil {
		if err := store.refreshQualificationAuthorityLocked(context.Background()); err != nil {
			return StoredTrustedQualificationResult{}, false, err
		}
	}
	if err := store.reloadLedgerLocked(file); err != nil {
		return StoredTrustedQualificationResult{}, false, err
	}
	result, ok := store.trustedResults[strings.TrimSpace(resultID)]
	return result, ok, nil
}

// currentMeetingSpecialistQualification is the concrete production adapter
// from the externally anchored evidence store to the join boundary. It reloads
// and re-verifies the complete signed bundle before exact comparison; no local
// interface implementation or echoed configuration can mint eligibility.
func (store *QualificationEvidenceStore) currentMeetingSpecialistQualification(request MeetingSpecialistQualificationRequest, currentTime func() time.Time) (StoredTrustedQualificationResult, error) {
	if store == nil || store.anchorAuthority == nil || request.validate() != nil || currentTime == nil {
		return StoredTrustedQualificationResult{}, ErrMeetingSpecialistJoinQualification
	}
	stored, found, err := store.TrustedQualificationResult(request.ResultID)
	if err != nil || !found {
		return StoredTrustedQualificationResult{}, ErrMeetingSpecialistJoinQualification
	}
	now := currentTime().UTC()
	if now.IsZero() {
		return StoredTrustedQualificationResult{}, ErrMeetingSpecialistJoinQualification
	}
	record := stored.Record
	evaluatedAt, evaluatedErr := time.Parse(time.RFC3339Nano, record.EvaluatedAt)
	if evaluatedErr != nil || evaluatedAt.After(now) || !now.Before(evaluatedAt.Add(meetingSpecialistQualificationMaxAge)) ||
		record.TenantID != request.TenantID || record.ResultID != request.ResultID || record.TargetID != request.TargetID || record.Lane != "meeting_specialist" || !record.Qualified ||
		record.EvaluatorConfigSHA256 != request.EvaluatorConfigDigest || record.EvaluatorResultSHA256 != request.EvaluatorResultDigest ||
		record.FixtureSHA256 != request.FixtureDigest || record.QualificationSubjectSHA256 != request.QualificationSubjectDigest ||
		record.Candidate != request.Candidate || record.MeetingSpecialistBinding != request.Binding {
		return StoredTrustedQualificationResult{}, ErrMeetingSpecialistJoinQualification
	}
	return stored, nil
}

func (store *QualificationEvidenceStore) ConsumeProviderAttempt(_ context.Context, ref TranscriptionProviderAttemptRef) (TranscriptionObservation, error) {
	if store == nil {
		return TranscriptionObservation{}, errors.New("qualification evidence store is absent")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	stored, ok := store.attempts[ref.Receipt]
	if !ok || stored.Ref != ref {
		return TranscriptionObservation{}, errors.New("provider-attempt evidence denied")
	}
	if err := store.consumeLocked("provider_attempt", ref.Receipt); err != nil {
		return TranscriptionObservation{}, err
	}
	return cloneTranscriptionObservation(stored.Observation), nil
}

func (store *QualificationEvidenceStore) ConsumeEvidenceTarget(_ context.Context, ref TranscriptionEvidenceTargetRef) (StoredTranscriptionEvidenceTarget, error) {
	if store == nil {
		return StoredTranscriptionEvidenceTarget{}, errors.New("qualification evidence store is absent")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	stored, ok := store.targets[ref.Receipt]
	if !ok || stored.Ref != ref {
		return StoredTranscriptionEvidenceTarget{}, errors.New("transcription-target evidence denied")
	}
	if err := store.consumeLocked("transcription_target", ref.Receipt); err != nil {
		return StoredTranscriptionEvidenceTarget{}, err
	}
	return cloneStoredTranscriptionEvidenceTarget(stored), nil
}

func (store *QualificationEvidenceStore) ConsumeDictationEvidence(_ context.Context, ref DictationEvidenceBatchRef) (StoredDictationEvidenceBatch, error) {
	if store == nil {
		return StoredDictationEvidenceBatch{}, errors.New("qualification evidence store is absent")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	stored, ok := store.dictation[ref.Receipt]
	if !ok || stored.Ref != ref {
		return StoredDictationEvidenceBatch{}, errors.New("dictation-batch evidence denied")
	}
	if err := store.consumeLocked("dictation_batch", ref.Receipt); err != nil {
		return StoredDictationEvidenceBatch{}, err
	}
	return cloneStoredDictationEvidenceBatch(stored), nil
}

func (store *QualificationEvidenceStore) consumptionKey(kind, token string) string {
	return kind + ":" + workDigest(token)
}

func (store *QualificationEvidenceStore) consumeLocked(kind, token string) error {
	file, _, err := store.openLedgerLocked()
	if err != nil {
		return err
	}
	defer closeQualificationLedger(file)
	if store.anchorAuthority != nil {
		if err := store.refreshQualificationAuthorityLocked(context.Background()); err != nil {
			return err
		}
	}
	if err := store.reloadLedgerLocked(file); err != nil {
		return err
	}
	key := store.consumptionKey(kind, token)
	if store.consumed[key] {
		return errors.New("qualification evidence was already consumed")
	}
	return store.appendLedgerEventLocked(file, kind, workDigest(token))
}

func (store *QualificationEvidenceStore) openLedgerLocked() (*os.File, bool, error) {
	directory := filepath.Dir(store.ledgerPath)
	directoryInfo, err := os.Lstat(directory)
	if err != nil || !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 || directoryInfo.Mode().Perm()&0o077 != 0 {
		return nil, false, errors.New("qualification evidence ledger directory must be a private real directory")
	}
	created := false
	pathInfo, statErr := os.Lstat(store.ledgerPath)
	if errors.Is(statErr, os.ErrNotExist) {
		created = true
	} else if statErr != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || pathInfo.Mode().Perm() != 0o600 {
		return nil, false, errors.New("qualification evidence ledger must be a private regular file")
	}
	file, err := os.OpenFile(store.ledgerPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, false, err
	}
	openedInfo, err := file.Stat()
	finalPathInfo, pathErr := os.Lstat(store.ledgerPath)
	if err != nil || pathErr != nil || !openedInfo.Mode().IsRegular() || openedInfo.Mode().Perm() != 0o600 || finalPathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedInfo, finalPathInfo) {
		closeQualificationLedger(file)
		return nil, false, errors.New("qualification evidence ledger changed during secure open")
	}
	return file, created, nil
}

func closeQualificationLedger(file *os.File) {
	if file == nil {
		return
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func (store *QualificationEvidenceStore) reloadLedgerLocked(file *os.File) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	store.consumed = map[string]bool{}
	store.trustedResults = map[string]StoredTrustedQualificationResult{}
	store.trustedSources = map[string]bool{}
	store.trustedPackets = map[string]bool{}
	store.sequence = 0
	store.lastDigest = ""
	scanner := bufio.NewScanner(file)
	// Trusted events contain the complete signed source/result bundle. Keep the
	// bound finite, but large enough for the preregistered evidence artifacts.
	scanner.Buffer(make([]byte, 4096), 16*1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		event, decodeErr := e10evidence.DecodeStrict[qualificationLedgerEvent](line)
		canonical, marshalErr := json.Marshal(event)
		if decodeErr != nil || marshalErr != nil || !bytes.Equal(line, canonical) || event.Sequence != store.sequence+1 || event.OccurredAt.IsZero() || event.PriorDigest != store.lastDigest || event.Digest != qualificationLedgerEventDigest(event) || !isHexDigest(event.TokenDigest) || !qualificationEvidenceLedgerKind(event.Kind) || store.validQualificationLedgerPayload(event) != nil {
			return errors.New("qualification evidence ledger integrity failure")
		}
		store.sequence = event.Sequence
		store.lastDigest = event.Digest
		store.consumed[event.Kind+":"+event.TokenDigest] = true
		if event.TrustedResult != nil {
			record := event.TrustedResult.Record
			if record.TenantID != store.tenantID || store.trustedResults[record.ResultID].Record.ResultID != "" || store.trustedSources[record.SourcePacketSHA256] || store.trustedPackets[record.ResultPacketSHA256] {
				return errors.New("qualification evidence ledger trusted-result replay or tenant failure")
			}
			store.trustedResults[record.ResultID] = *event.TrustedResult
			store.trustedSources[record.SourcePacketSHA256] = true
			store.trustedPackets[record.ResultPacketSHA256] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if store.exactAnchor != nil && store.currentLedgerAnchorLocked() != *store.exactAnchor {
		return errors.New("qualification evidence ledger differs from the exact externally anchored head")
	}
	return nil
}

func (store *QualificationEvidenceStore) validQualificationLedgerPayload(event qualificationLedgerEvent) error {
	if event.Kind != "trusted_qualification_result" {
		if event.TrustedResult != nil || len(event.TrustedBundle) != 0 {
			return errors.New("non-trusted ledger event contains trusted payload")
		}
		return nil
	}
	if store.anchorAuthority == nil || event.TrustedResult == nil || len(event.TrustedBundle) == 0 || event.TrustedResult.ImportedAt.IsZero() || !event.OccurredAt.Equal(event.TrustedResult.ImportedAt) || e10evidence.ValidateQualificationImportRecord(event.TrustedResult.Record) != nil {
		return errors.New("trusted qualification ledger payload lacks external authority")
	}
	_, verified, err := e10evidence.VerifyQualificationImportBundle(event.TrustedBundle, store.approvedRoots)
	if err != nil {
		return err
	}
	record, err := e10evidence.QualificationImport(verified)
	if err != nil || record != event.TrustedResult.Record || record.TenantID != store.tenantID || event.TokenDigest != workDigest(record.ResultPacketSHA256) {
		return errors.New("trusted qualification ledger bundle does not match its stored projection")
	}
	return nil
}

func (store *QualificationEvidenceStore) appendLedgerEventLocked(file *os.File, kind, tokenDigest string) error {
	event := qualificationLedgerEvent{Sequence: store.sequence + 1, OccurredAt: store.now().UTC(), Kind: kind, TokenDigest: tokenDigest, PriorDigest: store.lastDigest}
	if store.anchorAuthority == nil {
		return store.appendQualificationEventLocked(file, event)
	}
	startOffset, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	previousAnchor := store.currentLedgerAnchorLocked()
	if err := store.appendQualificationEventLocked(file, event); err != nil {
		return err
	}
	return store.commitQualificationAnchorCASLocked(file, startOffset, previousAnchor, store.currentLedgerAnchorLocked())
}

func (store *QualificationEvidenceStore) appendTrustedResultLocked(file *os.File, result StoredTrustedQualificationResult, bundleRaw []byte) (QualificationLedgerAnchor, error) {
	event := qualificationLedgerEvent{Sequence: store.sequence + 1, OccurredAt: result.ImportedAt, Kind: "trusted_qualification_result", TokenDigest: workDigest(result.Record.ResultPacketSHA256), PriorDigest: store.lastDigest, TrustedResult: &result, TrustedBundle: append(json.RawMessage(nil), bundleRaw...)}
	if err := store.appendQualificationEventLocked(file, event); err != nil {
		return QualificationLedgerAnchor{}, err
	}
	return store.currentLedgerAnchorLocked(), nil
}

func (store *QualificationEvidenceStore) appendQualificationEventLocked(file *os.File, event qualificationLedgerEvent) error {
	event.Digest = qualificationLedgerEventDigest(event)
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	startOffset, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	written := 0
	for written < len(raw) {
		n, writeErr := store.write(file, raw[written:])
		if n < 0 || n > len(raw)-written {
			writeErr = io.ErrShortWrite
			n = 0
		}
		written += n
		if writeErr != nil || n == 0 {
			if writeErr == nil {
				writeErr = io.ErrShortWrite
			}
			return rollbackQualificationLedger(file, store.sync, startOffset, writeErr)
		}
	}
	if err := store.sync(file); err != nil {
		return rollbackQualificationLedger(file, store.sync, startOffset, err)
	}
	store.sequence = event.Sequence
	store.lastDigest = event.Digest
	store.consumed[event.Kind+":"+event.TokenDigest] = true
	if event.TrustedResult != nil {
		record := event.TrustedResult.Record
		store.trustedResults[record.ResultID] = *event.TrustedResult
		store.trustedSources[record.SourcePacketSHA256] = true
		store.trustedPackets[record.ResultPacketSHA256] = true
	}
	return nil
}

// commitQualificationAnchorCASLocked resolves an ambiguous CAS response before
// deciding whether the durable local append may be accepted or rolled back.
// If custody advanced despite a lost response, the append is accepted. If
// custody is still at the prior head, the append is truncated and state is
// reloaded. Any third state or unreadable authority poisons this store instance
// for explicit operator reconciliation; blindly truncating could otherwise put
// the local ledger behind an already-advanced external head.
func (store *QualificationEvidenceStore) commitQualificationAnchorCASLocked(file *os.File, startOffset int64, previousAnchor, nextAnchor QualificationLedgerAnchor) error {
	casCtx, cancelCAS := store.qualificationAnchorCallContext(nil)
	casErr := store.anchorAuthority.CompareAndSwapQualificationLedgerAnchor(casCtx, store.tenantID, store.approvedTrustRootSHA256, previousAnchor, nextAnchor)
	cancelCAS()
	if casErr == nil {
		store.exactAnchor = &nextAnchor
		return nil
	}
	observeCtx, cancelObserve := store.qualificationAnchorCallContext(nil)
	observed, observeErr := store.anchorAuthority.QualificationAuthorityState(observeCtx, store.tenantID)
	cancelObserve()
	validObserved := observeErr == nil && validateQualificationLedgerAnchor(observed.LedgerAnchor, store.tenantID) == nil
	if validObserved && observed.TrustRootAnchor.ApprovedTrustRootSHA256 == store.approvedTrustRootSHA256 && observed.LedgerAnchor == nextAnchor {
		store.exactAnchor = &nextAnchor
		return nil
	}
	if validObserved && observed.LedgerAnchor == previousAnchor {
		rollbackErr := rollbackQualificationLedger(file, store.sync, startOffset, casErr)
		applyErr := store.applyQualificationAuthorityStateLocked(observed)
		reloadErr := store.reloadLedgerLocked(file)
		return errors.Join(errors.New("qualification evidence external anchor compare-and-swap failed"), rollbackErr, applyErr, reloadErr)
	}
	store.authorityReconciliationRequired = true
	return errors.Join(errors.New("qualification evidence external anchor state is ambiguous; operator reconciliation is required"), casErr, observeErr)
}

func rollbackQualificationLedger(file *os.File, syncFile func(*os.File) error, offset int64, cause error) error {
	truncateErr := file.Truncate(offset)
	_, seekErr := file.Seek(offset, io.SeekStart)
	syncErr := syncFile(file)
	return errors.Join(cause, truncateErr, seekErr, syncErr)
}

func syncQualificationLedgerDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync qualification evidence ledger directory: %w", err)
	}
	return nil
}

func qualificationEvidenceLedgerKind(kind string) bool {
	return kind == "provider_attempt" || kind == "transcription_target" || kind == "dictation_batch" || kind == "trusted_qualification_result"
}

func qualificationLedgerEventDigest(event qualificationLedgerEvent) string {
	event.Digest = ""
	return workDigest(event)
}

func qualificationLedgerGenesisDigest(tenantID string) string {
	return workDigest(struct {
		Purpose  string `json:"purpose"`
		TenantID string `json:"tenantId"`
	}{Purpose: "stride.e10.qualification-ledger-genesis/v1", TenantID: tenantID})
}

func validateQualificationLedgerAnchor(anchor QualificationLedgerAnchor, tenantID string) error {
	if anchor.SchemaVersion != qualificationLedgerAnchorSchema || anchor.TenantID != tenantID || anchor.Sequence < 0 || !isHexDigest(anchor.Digest) {
		return errors.New("qualification evidence ledger anchor is invalid or cross-tenant")
	}
	if anchor.Sequence == 0 && anchor.Digest != qualificationLedgerGenesisDigest(tenantID) {
		return errors.New("qualification evidence genesis anchor is invalid")
	}
	return nil
}

func (store *QualificationEvidenceStore) currentLedgerAnchorLocked() QualificationLedgerAnchor {
	digest := store.lastDigest
	if store.sequence == 0 {
		digest = qualificationLedgerGenesisDigest(store.tenantID)
	}
	return QualificationLedgerAnchor{SchemaVersion: qualificationLedgerAnchorSchema, TenantID: store.tenantID, Sequence: store.sequence, Digest: digest}
}

func cloneQualificationEvidenceSeed(seed QualificationEvidenceSeed) QualificationEvidenceSeed {
	clone := QualificationEvidenceSeed{
		ProviderAttempts:     make([]StoredProviderAttemptEvidence, len(seed.ProviderAttempts)),
		TranscriptionTargets: make([]StoredTranscriptionEvidenceTarget, len(seed.TranscriptionTargets)),
		DictationBatches:     make([]StoredDictationEvidenceBatch, len(seed.DictationBatches)),
	}
	for index, attempt := range seed.ProviderAttempts {
		clone.ProviderAttempts[index] = attempt
		clone.ProviderAttempts[index].Observation = cloneTranscriptionObservation(attempt.Observation)
	}
	for index, target := range seed.TranscriptionTargets {
		clone.TranscriptionTargets[index] = cloneStoredTranscriptionEvidenceTarget(target)
	}
	for index, batch := range seed.DictationBatches {
		clone.DictationBatches[index] = cloneStoredDictationEvidenceBatch(batch)
	}
	return clone
}

func cloneTranscriptionObservation(value TranscriptionObservation) TranscriptionObservation {
	value.ObservedSpeakers = append([]string(nil), value.ObservedSpeakers...)
	return value
}

func cloneStoredTranscriptionEvidenceTarget(value StoredTranscriptionEvidenceTarget) StoredTranscriptionEvidenceTarget {
	value.IntegrityBindings = append([]TranscriptionIntegrityBinding(nil), value.IntegrityBindings...)
	value.IntegrityEvents = append([]TranscriptionIntegrityEvent(nil), value.IntegrityEvents...)
	return value
}

func cloneStoredDictationEvidenceBatch(value StoredDictationEvidenceBatch) StoredDictationEvidenceBatch {
	value.Observations = append([]DictationQualificationObservation(nil), value.Observations...)
	for index := range value.Observations {
		value.Observations[index].PostReceiptDigests = append([]string(nil), value.Observations[index].PostReceiptDigests...)
		value.Observations[index].ModelCallReceiptDigests = append([]string(nil), value.Observations[index].ModelCallReceiptDigests...)
	}
	value.TranscriptionManifest.Cases = append([]TranscriptionCorpusCase(nil), value.TranscriptionManifest.Cases...)
	for index := range value.TranscriptionManifest.Cases {
		value.TranscriptionManifest.Cases[index].Tags = append([]string(nil), value.TranscriptionManifest.Cases[index].Tags...)
		value.TranscriptionManifest.Cases[index].RequiredTerms = append([]string(nil), value.TranscriptionManifest.Cases[index].RequiredTerms...)
		value.TranscriptionManifest.Cases[index].ExpectedSpeakers = append([]string(nil), value.TranscriptionManifest.Cases[index].ExpectedSpeakers...)
	}
	value.TranscriptionObservations = append([]TranscriptionObservation(nil), value.TranscriptionObservations...)
	for index := range value.TranscriptionObservations {
		value.TranscriptionObservations[index] = cloneTranscriptionObservation(value.TranscriptionObservations[index])
	}
	return value
}
