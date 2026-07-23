package dr

import (
	"bufio"
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
	"strings"
	"sync"
	"time"
)

const (
	PurgeAuthorityFormat = 1
	BackupManifestFormat = 1
	DrillReceiptFormat   = 1

	VolumeCanonical = "canonical_postgres"
	VolumeData      = "meeting_data"
	VolumeQueue     = "codex_queue"
	VolumeUsage     = "usage_ledger"
)

var (
	ErrInvalid           = errors.New("invalid DR evidence")
	ErrAuthorityRollback = errors.New("purge authority rollback refused")
	ErrAuthorityMismatch = errors.New("purge authority mismatch")
	ErrAuthorityTampered = errors.New("purge authority is corrupt or tampered")
	ErrAuthorityBusy     = errors.New("purge authority append is locked")
)

var requiredVolumes = []string{VolumeCanonical, VolumeQueue, VolumeData, VolumeUsage}

type SigningKey struct {
	ID     string
	Secret []byte
}

func (key SigningKey) Validate() error {
	if !validToken(key.ID) || len(key.Secret) < 32 {
		return fmt.Errorf("%w: signing key id and at least 32 secret bytes are required", ErrInvalid)
	}
	return nil
}

type PurgeState struct {
	TenantID       string `json:"tenantId"`
	HighWater      int64  `json:"highWater"`
	ManifestSHA256 string `json:"manifestSha256"`
}

func (state PurgeState) Validate() error {
	if !validToken(state.TenantID) || state.HighWater < 0 || !isSHA256(state.ManifestSHA256) {
		return fmt.Errorf("%w: purge state", ErrInvalid)
	}
	return nil
}

type PurgeAuthorityRecord struct {
	Format               int    `json:"format"`
	Sequence             uint64 `json:"sequence"`
	TenantID             string `json:"tenantId"`
	PurgeHighWater       int64  `json:"purgeHighWater"`
	PurgeManifestSHA256  string `json:"purgeManifestSha256"`
	ReleaseCommit        string `json:"releaseCommit"`
	RecordedAt           string `json:"recordedAt"`
	PreviousRecordSHA256 string `json:"previousRecordSha256,omitempty"`
	RecordSHA256         string `json:"recordSha256"`
	SigningKeyID         string `json:"signingKeyId"`
	Signature            string `json:"signature"`
}

type purgeAuthorityPayload struct {
	Format               int    `json:"format"`
	Sequence             uint64 `json:"sequence"`
	TenantID             string `json:"tenantId"`
	PurgeHighWater       int64  `json:"purgeHighWater"`
	PurgeManifestSHA256  string `json:"purgeManifestSha256"`
	ReleaseCommit        string `json:"releaseCommit"`
	RecordedAt           string `json:"recordedAt"`
	PreviousRecordSHA256 string `json:"previousRecordSha256,omitempty"`
	SigningKeyID         string `json:"signingKeyId"`
}

func (record PurgeAuthorityRecord) payload() purgeAuthorityPayload {
	return purgeAuthorityPayload{
		Format: record.Format, Sequence: record.Sequence, TenantID: record.TenantID,
		PurgeHighWater: record.PurgeHighWater, PurgeManifestSHA256: record.PurgeManifestSHA256,
		ReleaseCommit: record.ReleaseCommit, RecordedAt: record.RecordedAt,
		PreviousRecordSHA256: record.PreviousRecordSHA256, SigningKeyID: record.SigningKeyID,
	}
}

func (record PurgeAuthorityRecord) State() PurgeState {
	return PurgeState{TenantID: record.TenantID, HighWater: record.PurgeHighWater, ManifestSHA256: record.PurgeManifestSHA256}
}

type PurgeAuthority struct {
	mu   sync.Mutex
	path string
	key  SigningKey
}

// OpenPurgeAuthority refuses a path within any protected snapshot root. The
// authority must live on separately retained storage or an old content/DB
// restore could roll the deletion high-water backward with the data it guards.
func OpenPurgeAuthority(path string, key SigningKey, protectedRoots []string) (*PurgeAuthority, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" || len(protectedRoots) == 0 {
		return nil, fmt.Errorf("%w: authority path and protected roots are required", ErrInvalid)
	}
	canonicalPath, err := resolvePath(path)
	if err != nil {
		return nil, err
	}
	for _, root := range protectedRoots {
		canonicalRoot, rootErr := resolvePath(root)
		if rootErr != nil {
			return nil, rootErr
		}
		if pathWithin(canonicalPath, canonicalRoot) {
			return nil, fmt.Errorf("%w: purge authority must be outside protected root %s", ErrInvalid, canonicalRoot)
		}
	}
	if err := os.MkdirAll(filepath.Dir(canonicalPath), 0o700); err != nil {
		return nil, err
	}
	authority := &PurgeAuthority{path: canonicalPath, key: SigningKey{ID: key.ID, Secret: append([]byte(nil), key.Secret...)}}
	if _, err := authority.Records(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return authority, nil
}

func (authority *PurgeAuthority) Path() string {
	if authority == nil {
		return ""
	}
	return authority.path
}

func (authority *PurgeAuthority) Records() ([]PurgeAuthorityRecord, error) {
	if authority == nil || authority.path == "" || authority.key.Validate() != nil {
		return nil, ErrInvalid
	}
	file, err := os.Open(authority.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return decodeAuthority(file, authority.key)
}

func (authority *PurgeAuthority) Latest(tenantID string) (PurgeAuthorityRecord, bool, error) {
	records, err := authority.Records()
	if err != nil {
		return PurgeAuthorityRecord{}, false, err
	}
	tenantID = strings.TrimSpace(tenantID)
	for index := len(records) - 1; index >= 0; index-- {
		if records[index].TenantID == tenantID {
			return records[index], true, nil
		}
	}
	return PurgeAuthorityRecord{}, false, nil
}

// Append is monotonic per tenant and globally hash-chained. An identical latest
// tenant checkpoint is idempotent; a lower high-water or conflicting digest at
// the same high-water is refused before any byte is appended.
func (authority *PurgeAuthority) Append(ctx context.Context, state PurgeState, releaseCommit string, recordedAt time.Time) (PurgeAuthorityRecord, bool, error) {
	if authority == nil || state.Validate() != nil || !validReleaseCommit(releaseCommit) || recordedAt.IsZero() {
		return PurgeAuthorityRecord{}, false, ErrInvalid
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	releaseLock, err := acquireAuthorityFileLock(ctx, authority.path+".lock")
	if err != nil {
		return PurgeAuthorityRecord{}, false, err
	}
	defer releaseLock()
	records, err := authority.Records()
	if err != nil {
		return PurgeAuthorityRecord{}, false, err
	}
	var latestTenant PurgeAuthorityRecord
	foundTenant := false
	for index := len(records) - 1; index >= 0; index-- {
		if records[index].TenantID == state.TenantID {
			latestTenant, foundTenant = records[index], true
			break
		}
	}
	if foundTenant {
		switch {
		case state.HighWater < latestTenant.PurgeHighWater:
			return PurgeAuthorityRecord{}, false, ErrAuthorityRollback
		case state.HighWater == latestTenant.PurgeHighWater && state.ManifestSHA256 != latestTenant.PurgeManifestSHA256:
			return PurgeAuthorityRecord{}, false, ErrAuthorityMismatch
		case state.HighWater == latestTenant.PurgeHighWater && state.ManifestSHA256 == latestTenant.PurgeManifestSHA256 && releaseCommit == latestTenant.ReleaseCommit:
			return latestTenant, false, nil
		}
	}
	record := PurgeAuthorityRecord{
		Format: PurgeAuthorityFormat, Sequence: uint64(len(records) + 1), TenantID: state.TenantID,
		PurgeHighWater: state.HighWater, PurgeManifestSHA256: state.ManifestSHA256,
		ReleaseCommit: strings.ToLower(releaseCommit), RecordedAt: recordedAt.UTC().Format(time.RFC3339Nano), SigningKeyID: authority.key.ID,
	}
	if len(records) > 0 {
		record.PreviousRecordSHA256 = records[len(records)-1].RecordSHA256
	}
	if err := signAuthorityRecord(&record, authority.key); err != nil {
		return PurgeAuthorityRecord{}, false, err
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return PurgeAuthorityRecord{}, false, err
	}
	if err := appendDurably(authority.path, append(raw, '\n')); err != nil {
		return PurgeAuthorityRecord{}, false, err
	}
	return record, true, nil
}

func decodeAuthority(reader io.Reader, key SigningKey) ([]PurgeAuthorityRecord, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	records := []PurgeAuthorityRecord{}
	previous := ""
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			return nil, ErrAuthorityTampered
		}
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		var record PurgeAuthorityRecord
		if err := decoder.Decode(&record); err != nil || ensureJSONEOF(decoder) != nil || verifyAuthorityRecord(record, key) != nil || record.Sequence != uint64(len(records)+1) || record.PreviousRecordSHA256 != previous {
			return nil, ErrAuthorityTampered
		}
		previous = record.RecordSHA256
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func signAuthorityRecord(record *PurgeAuthorityRecord, key SigningKey) error {
	if record == nil || key.Validate() != nil || record.Format != PurgeAuthorityFormat || record.Sequence == 0 || record.State().Validate() != nil || !validReleaseCommit(record.ReleaseCommit) || !validTimestamp(record.RecordedAt) || record.SigningKeyID != key.ID || (record.PreviousRecordSHA256 != "" && !isSHA256(record.PreviousRecordSHA256)) {
		return ErrInvalid
	}
	raw, err := json.Marshal(record.payload())
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	record.RecordSHA256 = hex.EncodeToString(digest[:])
	record.Signature = signDigest(record.RecordSHA256, key.Secret)
	return nil
}

func verifyAuthorityRecord(record PurgeAuthorityRecord, key SigningKey) error {
	copy := record
	if err := signAuthorityRecord(&copy, key); err != nil || copy.RecordSHA256 != record.RecordSHA256 || !hmac.Equal([]byte(copy.Signature), []byte(record.Signature)) {
		return ErrAuthorityTampered
	}
	return nil
}

type SnapshotArtifact struct {
	Volume     string `json:"volume"`
	SnapshotID string `json:"snapshotId"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"sizeBytes"`
}

type CustodyEvidence struct {
	Custodian         string `json:"custodian"`
	ReceiptID         string `json:"receiptId"`
	Location          string `json:"location"`
	RetainedAt        string `json:"retainedAt"`
	SnapshotSetSHA256 string `json:"snapshotSetSha256"`
}

type EncryptionEvidence struct {
	Encrypted      bool   `json:"encrypted"`
	Algorithm      string `json:"algorithm"`
	KeyID          string `json:"keyId"`
	EnvelopeSHA256 string `json:"envelopeSha256"`
}

type OffsiteEvidence struct {
	Provider      string `json:"provider"`
	Bucket        string `json:"bucket"`
	ObjectKey     string `json:"objectKey"`
	ObjectVersion string `json:"objectVersion"`
	ReceiptID     string `json:"receiptId"`
	ObjectSHA256  string `json:"objectSha256"`
	StoredAt      string `json:"storedAt"`
}

type BackupManifestSpec struct {
	TenantID                   string             `json:"tenantId"`
	CreatedAt                  string             `json:"createdAt"`
	ReleaseCommit              string             `json:"releaseCommit"`
	Artifacts                  []SnapshotArtifact `json:"artifacts"`
	Purge                      PurgeState         `json:"purge"`
	PurgeAuthorityRecordSHA256 string             `json:"purgeAuthorityRecordSha256"`
	Custody                    CustodyEvidence    `json:"custody"`
	Encryption                 EncryptionEvidence `json:"encryption"`
	Offsite                    OffsiteEvidence    `json:"offsite"`
}

type BackupManifest struct {
	Format                     int                `json:"format"`
	TenantID                   string             `json:"tenantId"`
	CreatedAt                  string             `json:"createdAt"`
	ReleaseCommit              string             `json:"releaseCommit"`
	Artifacts                  []SnapshotArtifact `json:"artifacts"`
	SnapshotSetSHA256          string             `json:"snapshotSetSha256"`
	Purge                      PurgeState         `json:"purge"`
	PurgeAuthorityRecordSHA256 string             `json:"purgeAuthorityRecordSha256"`
	Custody                    CustodyEvidence    `json:"custody"`
	Encryption                 EncryptionEvidence `json:"encryption"`
	Offsite                    OffsiteEvidence    `json:"offsite"`
	ManifestSHA256             string             `json:"manifestSha256"`
	SigningKeyID               string             `json:"signingKeyId"`
	Signature                  string             `json:"signature"`
}

type backupManifestPayload struct {
	Format                     int                `json:"format"`
	TenantID                   string             `json:"tenantId"`
	CreatedAt                  string             `json:"createdAt"`
	ReleaseCommit              string             `json:"releaseCommit"`
	Artifacts                  []SnapshotArtifact `json:"artifacts"`
	SnapshotSetSHA256          string             `json:"snapshotSetSha256"`
	Purge                      PurgeState         `json:"purge"`
	PurgeAuthorityRecordSHA256 string             `json:"purgeAuthorityRecordSha256"`
	Custody                    CustodyEvidence    `json:"custody"`
	Encryption                 EncryptionEvidence `json:"encryption"`
	Offsite                    OffsiteEvidence    `json:"offsite"`
	SigningKeyID               string             `json:"signingKeyId"`
}

func (manifest BackupManifest) payload() backupManifestPayload {
	return backupManifestPayload{
		Format: manifest.Format, TenantID: manifest.TenantID, CreatedAt: manifest.CreatedAt, ReleaseCommit: manifest.ReleaseCommit,
		Artifacts: append([]SnapshotArtifact(nil), manifest.Artifacts...), SnapshotSetSHA256: manifest.SnapshotSetSHA256,
		Purge: manifest.Purge, PurgeAuthorityRecordSHA256: manifest.PurgeAuthorityRecordSHA256,
		Custody: manifest.Custody, Encryption: manifest.Encryption, Offsite: manifest.Offsite, SigningKeyID: manifest.SigningKeyID,
	}
}

func NewBackupManifest(spec BackupManifestSpec, key SigningKey) (BackupManifest, error) {
	manifest := BackupManifest{
		Format: BackupManifestFormat, TenantID: strings.TrimSpace(spec.TenantID), CreatedAt: spec.CreatedAt,
		ReleaseCommit: strings.ToLower(strings.TrimSpace(spec.ReleaseCommit)), Artifacts: append([]SnapshotArtifact(nil), spec.Artifacts...),
		Purge: spec.Purge, PurgeAuthorityRecordSHA256: spec.PurgeAuthorityRecordSHA256,
		Custody: spec.Custody, Encryption: spec.Encryption, Offsite: spec.Offsite, SigningKeyID: key.ID,
	}
	sort.Slice(manifest.Artifacts, func(i, j int) bool { return manifest.Artifacts[i].Volume < manifest.Artifacts[j].Volume })
	setDigest, err := snapshotSetDigest(manifest.Artifacts)
	if err != nil {
		return BackupManifest{}, err
	}
	manifest.SnapshotSetSHA256 = setDigest
	if err := validateBackupManifestFields(manifest, key); err != nil {
		return BackupManifest{}, err
	}
	raw, err := json.Marshal(manifest.payload())
	if err != nil {
		return BackupManifest{}, err
	}
	digest := sha256.Sum256(raw)
	manifest.ManifestSHA256 = hex.EncodeToString(digest[:])
	manifest.Signature = signDigest(manifest.ManifestSHA256, key.Secret)
	return manifest, nil
}

func VerifyBackupManifest(manifest BackupManifest, key SigningKey) error {
	if err := validateBackupManifestFields(manifest, key); err != nil {
		return err
	}
	raw, err := json.Marshal(manifest.payload())
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	wantDigest := hex.EncodeToString(digest[:])
	if manifest.ManifestSHA256 != wantDigest || !hmac.Equal([]byte(manifest.Signature), []byte(signDigest(wantDigest, key.Secret))) {
		return fmt.Errorf("%w: backup manifest signature", ErrAuthorityTampered)
	}
	return nil
}

func validateBackupManifestFields(manifest BackupManifest, key SigningKey) error {
	if key.Validate() != nil || manifest.Format != BackupManifestFormat || manifest.SigningKeyID != key.ID || !validToken(manifest.TenantID) || !validTimestamp(manifest.CreatedAt) || !validReleaseCommit(manifest.ReleaseCommit) || manifest.Purge.Validate() != nil || manifest.Purge.TenantID != manifest.TenantID || !isSHA256(manifest.PurgeAuthorityRecordSHA256) {
		return ErrInvalid
	}
	digest, err := snapshotSetDigest(manifest.Artifacts)
	if err != nil || manifest.SnapshotSetSHA256 != digest {
		return ErrInvalid
	}
	if !validToken(manifest.Custody.Custodian) || !validToken(manifest.Custody.ReceiptID) || strings.TrimSpace(manifest.Custody.Location) == "" || !validTimestamp(manifest.Custody.RetainedAt) || manifest.Custody.SnapshotSetSHA256 != digest {
		return fmt.Errorf("%w: custody evidence", ErrInvalid)
	}
	if !manifest.Encryption.Encrypted || manifest.Encryption.Algorithm != "AES-256-GCM" || !validToken(manifest.Encryption.KeyID) || !isSHA256(manifest.Encryption.EnvelopeSHA256) {
		return fmt.Errorf("%w: encryption evidence", ErrInvalid)
	}
	if !validToken(manifest.Offsite.Provider) || !validToken(manifest.Offsite.Bucket) || strings.TrimSpace(manifest.Offsite.ObjectKey) == "" || !validToken(manifest.Offsite.ObjectVersion) || !validToken(manifest.Offsite.ReceiptID) || !isSHA256(manifest.Offsite.ObjectSHA256) || !validTimestamp(manifest.Offsite.StoredAt) || manifest.Offsite.ObjectSHA256 != manifest.Encryption.EnvelopeSHA256 {
		return fmt.Errorf("%w: offsite evidence", ErrInvalid)
	}
	return nil
}

type RestoreCandidate struct {
	TenantID         string             `json:"tenantId"`
	ManifestSHA256   string             `json:"manifestSha256"`
	ReleaseCommit    string             `json:"releaseCommit"`
	Artifacts        []SnapshotArtifact `json:"artifacts"`
	DatabasePurge    PurgeState         `json:"databasePurge"`
	CustodyReceiptID string             `json:"custodyReceiptId"`
	EncryptionKeyID  string             `json:"encryptionKeyId"`
	OffsiteReceiptID string             `json:"offsiteReceiptId"`
}

type DrillReceipt struct {
	Format                     int      `json:"format"`
	DrillID                    string   `json:"drillId"`
	EvaluatedAt                string   `json:"evaluatedAt"`
	TenantID                   string   `json:"tenantId"`
	ManifestSHA256             string   `json:"manifestSha256"`
	SnapshotSetSHA256          string   `json:"snapshotSetSha256,omitempty"`
	PurgeAuthorityRecordSHA256 string   `json:"purgeAuthorityRecordSha256,omitempty"`
	PurgeHighWater             int64    `json:"purgeHighWater"`
	ReleaseCommit              string   `json:"releaseCommit"`
	Ready                      bool     `json:"ready"`
	FailureCodes               []string `json:"failureCodes"`
	ReceiptSHA256              string   `json:"receiptSha256"`
	SigningKeyID               string   `json:"signingKeyId"`
	Signature                  string   `json:"signature"`
}

type drillReceiptPayload struct {
	Format                     int      `json:"format"`
	DrillID                    string   `json:"drillId"`
	EvaluatedAt                string   `json:"evaluatedAt"`
	TenantID                   string   `json:"tenantId"`
	ManifestSHA256             string   `json:"manifestSha256"`
	SnapshotSetSHA256          string   `json:"snapshotSetSha256,omitempty"`
	PurgeAuthorityRecordSHA256 string   `json:"purgeAuthorityRecordSha256,omitempty"`
	PurgeHighWater             int64    `json:"purgeHighWater"`
	ReleaseCommit              string   `json:"releaseCommit"`
	Ready                      bool     `json:"ready"`
	FailureCodes               []string `json:"failureCodes"`
	SigningKeyID               string   `json:"signingKeyId"`
}

func (receipt DrillReceipt) payload() drillReceiptPayload {
	return drillReceiptPayload{
		Format: receipt.Format, DrillID: receipt.DrillID, EvaluatedAt: receipt.EvaluatedAt, TenantID: receipt.TenantID,
		ManifestSHA256: receipt.ManifestSHA256, SnapshotSetSHA256: receipt.SnapshotSetSHA256,
		PurgeAuthorityRecordSHA256: receipt.PurgeAuthorityRecordSHA256, PurgeHighWater: receipt.PurgeHighWater,
		ReleaseCommit: receipt.ReleaseCommit, Ready: receipt.Ready, FailureCodes: append([]string(nil), receipt.FailureCodes...), SigningKeyID: receipt.SigningKeyID,
	}
}

const (
	FailureManifestInvalid   = "manifest_invalid"
	FailureAuthorityMissing  = "purge_authority_missing"
	FailureAuthorityInvalid  = "purge_authority_invalid"
	FailurePurgeRollback     = "purge_rollback"
	FailurePurgeMismatch     = "purge_mismatch"
	FailureReleaseMismatch   = "release_mismatch"
	FailureSnapshotMismatch  = "snapshot_mismatch"
	FailureCustodyMissing    = "custody_evidence_missing"
	FailureEncryptionMissing = "encryption_evidence_missing"
	FailureOffsiteMissing    = "offsite_evidence_missing"
)

// PreflightRestore is read-only and fail-closed. It never applies a restore;
// callers may make data readable only when the signed receipt says Ready.
func PreflightRestore(manifest BackupManifest, candidate RestoreCandidate, authority *PurgeAuthority, expectedRelease string, evaluatedAt time.Time, key SigningKey) DrillReceipt {
	receipt := DrillReceipt{
		Format: DrillReceiptFormat, EvaluatedAt: evaluatedAt.UTC().Format(time.RFC3339Nano), TenantID: candidate.TenantID,
		ManifestSHA256: manifest.ManifestSHA256, SnapshotSetSHA256: manifest.SnapshotSetSHA256,
		PurgeAuthorityRecordSHA256: manifest.PurgeAuthorityRecordSHA256, PurgeHighWater: candidate.DatabasePurge.HighWater,
		ReleaseCommit: strings.ToLower(strings.TrimSpace(expectedRelease)), FailureCodes: []string{}, SigningKeyID: key.ID,
	}
	failures := map[string]bool{}
	if evaluatedAt.IsZero() || key.Validate() != nil || VerifyBackupManifest(manifest, key) != nil || candidate.TenantID != manifest.TenantID || candidate.ManifestSHA256 != manifest.ManifestSHA256 {
		failures[FailureManifestInvalid] = true
	}
	if !validReleaseCommit(expectedRelease) || !strings.EqualFold(expectedRelease, manifest.ReleaseCommit) || !strings.EqualFold(candidate.ReleaseCommit, manifest.ReleaseCommit) {
		failures[FailureReleaseMismatch] = true
	}
	candidateArtifacts := append([]SnapshotArtifact(nil), candidate.Artifacts...)
	sort.Slice(candidateArtifacts, func(i, j int) bool { return candidateArtifacts[i].Volume < candidateArtifacts[j].Volume })
	if left, leftErr := json.Marshal(candidateArtifacts); leftErr != nil {
		failures[FailureSnapshotMismatch] = true
	} else if right, rightErr := json.Marshal(manifest.Artifacts); rightErr != nil || !bytes.Equal(left, right) {
		failures[FailureSnapshotMismatch] = true
	}
	if candidate.CustodyReceiptID == "" || candidate.CustodyReceiptID != manifest.Custody.ReceiptID {
		failures[FailureCustodyMissing] = true
	}
	if candidate.EncryptionKeyID == "" || candidate.EncryptionKeyID != manifest.Encryption.KeyID {
		failures[FailureEncryptionMissing] = true
	}
	if candidate.OffsiteReceiptID == "" || candidate.OffsiteReceiptID != manifest.Offsite.ReceiptID {
		failures[FailureOffsiteMissing] = true
	}
	if candidate.DatabasePurge.Validate() != nil || candidate.DatabasePurge.TenantID != manifest.TenantID {
		failures[FailurePurgeMismatch] = true
	}
	latest, found, authorityErr := PurgeAuthorityRecord{}, false, error(nil)
	if authority == nil {
		failures[FailureAuthorityMissing] = true
	} else {
		latest, found, authorityErr = authority.Latest(manifest.TenantID)
		if authorityErr != nil {
			failures[FailureAuthorityInvalid] = true
		} else if !found {
			failures[FailureAuthorityMissing] = true
		}
	}
	if found {
		receipt.PurgeAuthorityRecordSHA256 = latest.RecordSHA256
		switch {
		case candidate.DatabasePurge.HighWater < latest.PurgeHighWater || manifest.Purge.HighWater < latest.PurgeHighWater:
			failures[FailurePurgeRollback] = true
		case candidate.DatabasePurge.HighWater != latest.PurgeHighWater || manifest.Purge.HighWater != latest.PurgeHighWater || candidate.DatabasePurge.ManifestSHA256 != latest.PurgeManifestSHA256 || manifest.Purge.ManifestSHA256 != latest.PurgeManifestSHA256 || manifest.PurgeAuthorityRecordSHA256 != latest.RecordSHA256:
			failures[FailurePurgeMismatch] = true
		}
	}
	for code := range failures {
		receipt.FailureCodes = append(receipt.FailureCodes, code)
	}
	sort.Strings(receipt.FailureCodes)
	receipt.Ready = len(receipt.FailureCodes) == 0
	drillMaterial, _ := json.Marshal(struct {
		TenantID       string   `json:"tenantId"`
		ManifestSHA256 string   `json:"manifestSha256"`
		EvaluatedAt    string   `json:"evaluatedAt"`
		Failures       []string `json:"failures"`
	}{receipt.TenantID, receipt.ManifestSHA256, receipt.EvaluatedAt, receipt.FailureCodes})
	drillDigest := sha256.Sum256(drillMaterial)
	receipt.DrillID = "drill-" + hex.EncodeToString(drillDigest[:])[:32]
	raw, _ := json.Marshal(receipt.payload())
	digest := sha256.Sum256(raw)
	receipt.ReceiptSHA256 = hex.EncodeToString(digest[:])
	receipt.Signature = signDigest(receipt.ReceiptSHA256, key.Secret)
	return receipt
}

func VerifyDrillReceipt(receipt DrillReceipt, key SigningKey) error {
	if key.Validate() != nil || receipt.Format != DrillReceiptFormat || receipt.SigningKeyID != key.ID || !validTimestamp(receipt.EvaluatedAt) || !isSHA256(receipt.ReceiptSHA256) || !strings.HasPrefix(receipt.DrillID, "drill-") {
		return ErrInvalid
	}
	raw, err := json.Marshal(receipt.payload())
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	want := hex.EncodeToString(digest[:])
	if want != receipt.ReceiptSHA256 || !hmac.Equal([]byte(receipt.Signature), []byte(signDigest(want, key.Secret))) {
		return ErrAuthorityTampered
	}
	return nil
}

func snapshotSetDigest(artifacts []SnapshotArtifact) (string, error) {
	if len(artifacts) != len(requiredVolumes) {
		return "", fmt.Errorf("%w: exactly four protected volume snapshots are required", ErrInvalid)
	}
	copy := append([]SnapshotArtifact(nil), artifacts...)
	sort.Slice(copy, func(i, j int) bool { return copy[i].Volume < copy[j].Volume })
	for index, volume := range requiredVolumes {
		if copy[index].Volume != volume || !validToken(copy[index].SnapshotID) || !isSHA256(copy[index].SHA256) || copy[index].SizeBytes <= 0 {
			return "", fmt.Errorf("%w: snapshot evidence for %s", ErrInvalid, volume)
		}
	}
	raw, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func signDigest(digest string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(digest))
	return hex.EncodeToString(mac.Sum(nil))
}

func isSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validToken(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 240 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("._:@/-", char) {
			continue
		}
		return false
	}
	return true
}

func validReleaseCommit(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validTimestamp(value string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && parsed.Format(time.RFC3339Nano) == value
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

func appendDurably(path string, data []byte) error {
	created := false
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		created = true
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	for len(data) > 0 {
		written, writeErr := file.Write(data)
		if writeErr != nil {
			_ = file.Close()
			return writeErr
		}
		if written <= 0 {
			_ = file.Close()
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if created {
		return syncDirectory(filepath.Dir(path))
	}
	return nil
}

func acquireAuthorityFileLock(ctx context.Context, path string) (func(), error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil, ErrAuthorityBusy
	}
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return func() {
		_ = os.Remove(path)
		_ = syncDirectory(filepath.Dir(path))
	}, nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func resolvePath(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(strings.TrimSpace(path)))
	if err != nil {
		return "", err
	}
	probe := abs
	tail := []string{}
	for {
		if _, err := os.Lstat(probe); err == nil {
			resolved, resolveErr := filepath.EvalSymlinks(probe)
			if resolveErr != nil {
				return "", resolveErr
			}
			for index := len(tail) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, tail[index])
			}
			return filepath.Clean(resolved), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return abs, nil
		}
		tail = append(tail, filepath.Base(probe))
		probe = parent
	}
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
