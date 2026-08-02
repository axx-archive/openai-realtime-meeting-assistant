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

// QualificationEvidenceStore is deliberately not a provider trust root. Local
// seed evidence can only produce structure-only candidates. Separately, the
// store can durably import opaque qualification capabilities minted by
// e10evidence after independent trust-root, registry, source-packet, evaluator,
// and dual-signature verification; it cannot mint those capabilities itself.
type QualificationEvidenceStore struct {
	mu             sync.Mutex
	ledgerPath     string
	tenantID       string
	now            func() time.Time
	attempts       map[string]StoredProviderAttemptEvidence
	targets        map[string]StoredTranscriptionEvidenceTarget
	dictation      map[string]StoredDictationEvidenceBatch
	consumed       map[string]bool
	trustedResults map[string]StoredTrustedQualificationResult
	trustedSources map[string]bool
	trustedPackets map[string]bool
	sequence       int64
	lastDigest     string
	write          func(*os.File, []byte) (int, error)
	sync           func(*os.File) error
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
}

// OpenQualificationEvidenceStore admits caller-supplied seed evidence only into
// the local structure-only maps. There is no signing key or self-anchored
// registry here; the trusted-result import API accepts only opaque capabilities
// already verified against independently administered e10evidence trust roots.
func OpenQualificationEvidenceStore(ledgerPath string, seed QualificationEvidenceSeed, tenantID string, now func() time.Time) (*QualificationEvidenceStore, error) {
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
		write: func(file *os.File, value []byte) (int, error) {
			return file.Write(value)
		},
		sync: func(file *os.File) error { return file.Sync() },
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

// ImportVerifiedQualificationResult is the only trusted-result ingestion
// path. The e10evidence package must first mint the opaque capability from the
// anchored registry, exact signed source packet, exact signed result packet,
// and independently approved trust roots. Import is one-use for both source
// and result packet and is durable across store instances and restarts.
func (store *QualificationEvidenceStore) ImportVerifiedQualificationResult(verified e10evidence.VerifiedQualificationResult) (StoredTrustedQualificationResult, error) {
	if store == nil {
		return StoredTrustedQualificationResult{}, errors.New("qualification evidence store is absent")
	}
	record, err := e10evidence.QualificationImport(verified)
	if err != nil || record.TenantID != store.tenantID {
		return StoredTrustedQualificationResult{}, errors.New("trusted qualification result denied")
	}
	stored := StoredTrustedQualificationResult{Record: record, ImportedAt: store.now().UTC()}
	store.mu.Lock()
	defer store.mu.Unlock()
	file, _, err := store.openLedgerLocked()
	if err != nil {
		return StoredTrustedQualificationResult{}, err
	}
	defer closeQualificationLedger(file)
	if err := store.reloadLedgerLocked(file); err != nil {
		return StoredTrustedQualificationResult{}, err
	}
	if _, exists := store.trustedResults[record.ResultID]; exists || store.trustedSources[record.SourcePacketSHA256] || store.trustedPackets[record.ResultPacketSHA256] {
		return StoredTrustedQualificationResult{}, errors.New("trusted qualification result or source packet was already imported")
	}
	if err := store.appendTrustedResultLocked(file, stored); err != nil {
		return StoredTrustedQualificationResult{}, err
	}
	return stored, nil
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
	if err := store.reloadLedgerLocked(file); err != nil {
		return StoredTrustedQualificationResult{}, false, err
	}
	result, ok := store.trustedResults[strings.TrimSpace(resultID)]
	return result, ok, nil
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
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		event, decodeErr := e10evidence.DecodeStrict[qualificationLedgerEvent](line)
		canonical, marshalErr := json.Marshal(event)
		if decodeErr != nil || marshalErr != nil || !bytes.Equal(line, canonical) || event.Sequence != store.sequence+1 || event.OccurredAt.IsZero() || event.PriorDigest != store.lastDigest || event.Digest != qualificationLedgerEventDigest(event) || !isHexDigest(event.TokenDigest) || !qualificationEvidenceLedgerKind(event.Kind) || !validQualificationLedgerPayload(event) {
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
	return scanner.Err()
}

func validQualificationLedgerPayload(event qualificationLedgerEvent) bool {
	if event.Kind != "trusted_qualification_result" {
		return event.TrustedResult == nil
	}
	if event.TrustedResult == nil || event.TrustedResult.ImportedAt.IsZero() || !event.OccurredAt.Equal(event.TrustedResult.ImportedAt) || e10evidence.ValidateQualificationImportRecord(event.TrustedResult.Record) != nil {
		return false
	}
	return event.TokenDigest == workDigest(event.TrustedResult.Record.ResultPacketSHA256)
}

func (store *QualificationEvidenceStore) appendLedgerEventLocked(file *os.File, kind, tokenDigest string) error {
	event := qualificationLedgerEvent{Sequence: store.sequence + 1, OccurredAt: store.now().UTC(), Kind: kind, TokenDigest: tokenDigest, PriorDigest: store.lastDigest}
	return store.appendQualificationEventLocked(file, event)
}

func (store *QualificationEvidenceStore) appendTrustedResultLocked(file *os.File, result StoredTrustedQualificationResult) error {
	event := qualificationLedgerEvent{Sequence: store.sequence + 1, OccurredAt: result.ImportedAt, Kind: "trusted_qualification_result", TokenDigest: workDigest(result.Record.ResultPacketSHA256), PriorDigest: store.lastDigest, TrustedResult: &result}
	return store.appendQualificationEventLocked(file, event)
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
