package e10evidence

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const validationReceiptConsumptionSchema = "stride.e10.validation-receipt-consumption/v2"

// ReverifyEncodedTargetRegistryReceipt replays the complete source-envelope
// verification and then requires the serialized receipt to match the newly
// derived receipt byte-for-byte. The receipt itself is never a trust root.
func ReverifyEncodedTargetRegistryReceipt(encoded, raw, registrySignature, registryPublicKey []byte, approved ApprovedTrustRoots) (VerifiedValidationReceipt, error) {
	_, expected, err := VerifyTargetRegistryReceipt(raw, registrySignature, registryPublicKey, approved)
	if err != nil {
		return VerifiedValidationReceipt{}, err
	}
	return matchEncodedValidationReceipt(encoded, expected)
}

// ReverifyEncodedCorpusReceipt replays registry, corpus, artifact-set, and
// dual-signature verification before accepting serialized receipt bytes.
func ReverifyEncodedCorpusReceipt(encoded, raw, registryRaw, registrySignature, registryPublicKey, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey []byte, approved ApprovedTrustRoots) (VerifiedValidationReceipt, error) {
	_, expected, err := VerifyCorpusReceipt(raw, registryRaw, registrySignature, registryPublicKey, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey, approved)
	if err != nil {
		return VerifiedValidationReceipt{}, err
	}
	return matchEncodedValidationReceipt(encoded, expected)
}

// ReverifyEncodedPilotReceipt replays registry, per-reviewer, packet, and
// dual-signature verification before accepting serialized receipt bytes.
func ReverifyEncodedPilotReceipt(encoded, raw, registryRaw, registrySignature, registryPublicKey, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey []byte, approved ApprovedTrustRoots) (VerifiedValidationReceipt, error) {
	_, expected, err := VerifyPilotReceipt(raw, registryRaw, registrySignature, registryPublicKey, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey, approved)
	if err != nil {
		return VerifiedValidationReceipt{}, err
	}
	return matchEncodedValidationReceipt(encoded, expected)
}

// ReverifyEncodedMatrixReceipt replays registry, target, observation,
// statistical, artifact-set, and dual-signature verification before accepting
// serialized receipt bytes.
func ReverifyEncodedMatrixReceipt(encoded, raw, registryRaw, registrySignature, registryPublicKey, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey []byte, approved ApprovedTrustRoots) (VerifiedValidationReceipt, error) {
	_, expected, err := VerifyMatrixReceipt(raw, registryRaw, registrySignature, registryPublicKey, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey, approved)
	if err != nil {
		return VerifiedValidationReceipt{}, err
	}
	return matchEncodedValidationReceipt(encoded, expected)
}

func matchEncodedValidationReceipt(encoded []byte, expected VerifiedValidationReceipt) (VerifiedValidationReceipt, error) {
	// Decode first so malformed, duplicate-key, and unknown-field receipts fail
	// with the same strict JSON boundary as source packets. Exact comparison
	// below also rejects harmless-looking formatting or ordering drift.
	if _, err := DecodeStrict[ValidationReceipt](encoded); err != nil {
		return VerifiedValidationReceipt{}, fmt.Errorf("serialized validation receipt: %w", err)
	}
	expectedBytes, err := EncodeReceipt(expected)
	if err != nil {
		return VerifiedValidationReceipt{}, err
	}
	if !bytes.Equal(encoded, expectedBytes) {
		return VerifiedValidationReceipt{}, errors.New("serialized validation receipt does not exactly match the receipt derived from the original signed source envelope")
	}
	return expected, nil
}

// ValidationReceiptConsumer durably rejects receipt replay across goroutines,
// consumer instances, and process restarts. Its ledger records source-identity
// and receipt digests plus an integrity chain; source evidence and signatures
// remain in the approved evidence store. Source identity excludes trust-root
// metadata so re-approving the same packet cannot replay its downstream effect.
type ValidationReceiptConsumer struct {
	mu         sync.Mutex
	ledgerPath string
	now        func() time.Time
	write      func(*os.File, []byte) (int, error)
	sync       func(*os.File) error
}

type validationReceiptConsumptionEvent struct {
	SchemaVersion        string    `json:"schemaVersion"`
	Sequence             int64     `json:"sequence"`
	OccurredAt           time.Time `json:"occurredAt"`
	SourceIdentitySHA256 string    `json:"sourceIdentitySha256"`
	ReceiptSHA256        string    `json:"receiptSha256"`
	PriorDigest          string    `json:"priorDigest,omitempty"`
	Digest               string    `json:"digest"`
}

// OpenValidationReceiptConsumer opens or creates a private append-only replay
// ledger. The parent directory must already exist, be real (not a symlink), and
// grant no group or world permissions.
func OpenValidationReceiptConsumer(ledgerPath string) (*ValidationReceiptConsumer, error) {
	consumer := &ValidationReceiptConsumer{
		ledgerPath: ledgerPath,
		now:        time.Now,
		write:      func(file *os.File, value []byte) (int, error) { return file.Write(value) },
		sync:       func(file *os.File) error { return file.Sync() },
	}
	if ledgerPath == "" {
		return nil, errors.New("validation receipt replay-ledger path is required")
	}
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	file, created, err := consumer.openLedgerLocked()
	if err != nil {
		return nil, err
	}
	defer closeValidationReceiptLedger(file)
	if _, _, err := scanValidationReceiptLedger(file); err != nil {
		return nil, err
	}
	if created {
		if err := consumer.sync(file); err != nil {
			return nil, fmt.Errorf("sync new validation receipt replay ledger: %w", err)
		}
		if err := syncValidationReceiptLedgerDirectory(filepath.Dir(ledgerPath)); err != nil {
			return nil, err
		}
	}
	return consumer, nil
}

// Consume verifies the opaque receipt capability and durably claims the
// validated source identity once, while recording the exact serialized receipt
// digest. A second claim for the same source fails closed even when approval
// metadata changes, through another consumer instance, or after process restart.
func (consumer *ValidationReceiptConsumer) Consume(verified VerifiedValidationReceipt) error {
	if consumer == nil {
		return errors.New("validation receipt consumer is absent")
	}
	encoded, err := EncodeReceipt(verified)
	if err != nil {
		return err
	}
	receiptDigest := digestBytes(encoded)
	sourceIdentityDigest := validationReceiptSourceIdentityDigest(verified.receipt)

	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	file, _, err := consumer.openLedgerLocked()
	if err != nil {
		return err
	}
	defer closeValidationReceiptLedger(file)
	consumed, prior, err := scanValidationReceiptLedger(file)
	if err != nil {
		return err
	}
	if consumed[sourceIdentityDigest] {
		return errors.New("validation receipt source envelope was already consumed")
	}
	event := validationReceiptConsumptionEvent{
		SchemaVersion:        validationReceiptConsumptionSchema,
		Sequence:             int64(len(consumed) + 1),
		OccurredAt:           consumer.now().UTC(),
		SourceIdentitySHA256: sourceIdentityDigest,
		ReceiptSHA256:        receiptDigest,
		PriorDigest:          prior,
	}
	event.Digest = validationReceiptConsumptionEventDigest(event)
	return consumer.appendEventLocked(file, event)
}

func (consumer *ValidationReceiptConsumer) openLedgerLocked() (*os.File, bool, error) {
	directory := filepath.Dir(consumer.ledgerPath)
	directoryInfo, err := os.Lstat(directory)
	if err != nil || !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 || directoryInfo.Mode().Perm()&0o077 != 0 {
		return nil, false, errors.New("validation receipt replay-ledger directory must be a private real directory")
	}
	created := false
	pathInfo, statErr := os.Lstat(consumer.ledgerPath)
	if errors.Is(statErr, os.ErrNotExist) {
		created = true
	} else if statErr != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || pathInfo.Mode().Perm() != 0o600 {
		return nil, false, errors.New("validation receipt replay ledger must be a private regular file")
	}
	file, err := os.OpenFile(consumer.ledgerPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, false, err
	}
	openedInfo, openedErr := file.Stat()
	finalPathInfo, pathErr := os.Lstat(consumer.ledgerPath)
	if openedErr != nil || pathErr != nil || !openedInfo.Mode().IsRegular() || openedInfo.Mode().Perm() != 0o600 || finalPathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedInfo, finalPathInfo) {
		closeValidationReceiptLedger(file)
		return nil, false, errors.New("validation receipt replay ledger changed during secure open")
	}
	return file, created, nil
}

func closeValidationReceiptLedger(file *os.File) {
	if file == nil {
		return
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func scanValidationReceiptLedger(file *os.File) (map[string]bool, string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	consumed := map[string]bool{}
	sequence := int64(0)
	prior := ""
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		event, err := DecodeStrict[validationReceiptConsumptionEvent](line)
		if err != nil {
			return nil, "", errors.New("validation receipt replay-ledger integrity failure")
		}
		canonical, marshalErr := json.Marshal(event)
		if marshalErr != nil || !bytes.Equal(line, canonical) || event.SchemaVersion != validationReceiptConsumptionSchema || event.Sequence != sequence+1 || event.OccurredAt.IsZero() || !validSHA(event.SourceIdentitySHA256) || !validSHA(event.ReceiptSHA256) || consumed[event.SourceIdentitySHA256] || event.PriorDigest != prior || event.Digest != validationReceiptConsumptionEventDigest(event) {
			return nil, "", errors.New("validation receipt replay-ledger integrity failure")
		}
		sequence = event.Sequence
		prior = event.Digest
		consumed[event.SourceIdentitySHA256] = true
	}
	if err := scanner.Err(); err != nil {
		return nil, "", err
	}
	return consumed, prior, nil
}

func (consumer *ValidationReceiptConsumer) appendEventLocked(file *os.File, event validationReceiptConsumptionEvent) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	startOffset, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	written, writeErr := consumer.write(file, raw)
	if writeErr == nil && written != len(raw) {
		writeErr = io.ErrShortWrite
	}
	if writeErr != nil {
		_ = file.Truncate(startOffset)
		_, _ = file.Seek(startOffset, io.SeekStart)
		_ = file.Sync()
		return fmt.Errorf("append validation receipt replay event: %w", writeErr)
	}
	if err := consumer.sync(file); err != nil {
		_ = file.Truncate(startOffset)
		_, _ = file.Seek(startOffset, io.SeekStart)
		_ = file.Sync()
		return fmt.Errorf("sync validation receipt replay event: %w", err)
	}
	return nil
}

func validationReceiptConsumptionEventDigest(event validationReceiptConsumptionEvent) string {
	type digestPayload struct {
		SchemaVersion        string    `json:"schemaVersion"`
		Sequence             int64     `json:"sequence"`
		OccurredAt           time.Time `json:"occurredAt"`
		SourceIdentitySHA256 string    `json:"sourceIdentitySha256"`
		ReceiptSHA256        string    `json:"receiptSha256"`
		PriorDigest          string    `json:"priorDigest,omitempty"`
	}
	raw, _ := json.Marshal(digestPayload{
		SchemaVersion:        event.SchemaVersion,
		Sequence:             event.Sequence,
		OccurredAt:           event.OccurredAt,
		SourceIdentitySHA256: event.SourceIdentitySHA256,
		ReceiptSHA256:        event.ReceiptSHA256,
		PriorDigest:          event.PriorDigest,
	})
	sum := sha256.Sum256(append([]byte("stride-e10-validation-receipt-consumption/v2\x00"), raw...))
	return fmt.Sprintf("%x", sum[:])
}

func validationReceiptSourceIdentityDigest(receipt ValidationReceipt) string {
	type sourceIdentity struct {
		PacketKind     string           `json:"packetKind"`
		InputSHA256    string           `json:"inputSha256"`
		RegistrySHA256 string           `json:"registrySha256"`
		Candidate      CandidateBinding `json:"candidate"`
	}
	raw, _ := json.Marshal(sourceIdentity{
		PacketKind: receipt.PacketKind, InputSHA256: receipt.InputSHA256,
		RegistrySHA256: receipt.RegistrySHA256, Candidate: receipt.Candidate,
	})
	return digestBytes(append([]byte("stride-e10-validation-source-identity/v1\x00"), raw...))
}

func syncValidationReceiptLedgerDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync validation receipt replay-ledger directory: %w", err)
	}
	return nil
}
