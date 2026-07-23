package dr

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testSigningKey() SigningKey {
	return SigningKey{ID: "dr-test-key-v1", Secret: bytes.Repeat([]byte{0x42}, 32)}
}

func testDigest(label string) string {
	digest := sha256Bytes([]byte(label))
	return digest
}

func sha256Bytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func testRelease() string { return strings.Repeat("a", 40) }

func testAuthority(t *testing.T) *PurgeAuthority {
	t.Helper()
	root := t.TempDir()
	protected := filepath.Join(root, "protected", "meeting-data")
	if err := os.MkdirAll(protected, 0o700); err != nil {
		t.Fatal(err)
	}
	authority, err := OpenPurgeAuthority(filepath.Join(root, "independent", "purge-authority.jsonl"), testSigningKey(), []string{protected})
	if err != nil {
		t.Fatal(err)
	}
	return authority
}

func testArtifacts() []SnapshotArtifact {
	return []SnapshotArtifact{
		{Volume: VolumeData, SnapshotID: "snap-meeting-data", SHA256: testDigest("meeting-data"), SizeBytes: 101},
		{Volume: VolumeCanonical, SnapshotID: "snap-canonical-pg", SHA256: testDigest("canonical"), SizeBytes: 102},
		{Volume: VolumeUsage, SnapshotID: "snap-usage-ledger", SHA256: testDigest("usage"), SizeBytes: 103},
		{Volume: VolumeQueue, SnapshotID: "snap-codex-queue", SHA256: testDigest("queue"), SizeBytes: 104},
	}
}

func testManifest(t *testing.T, state PurgeState, authorityRecord PurgeAuthorityRecord) BackupManifest {
	t.Helper()
	artifacts := testArtifacts()
	setDigest, err := snapshotSetDigest(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	spec := BackupManifestSpec{
		TenantID: state.TenantID, CreatedAt: "2026-07-22T20:00:00Z", ReleaseCommit: testRelease(), Artifacts: artifacts,
		Purge: state, PurgeAuthorityRecordSHA256: authorityRecord.RecordSHA256,
		Custody:    CustodyEvidence{Custodian: "ops-primary", ReceiptID: "custody-receipt-1", Location: "independent-vault", RetainedAt: "2026-07-22T20:01:00Z", SnapshotSetSHA256: setDigest},
		Encryption: EncryptionEvidence{Encrypted: true, Algorithm: "AES-256-GCM", KeyID: "backup-key-v3", EnvelopeSHA256: testDigest("encrypted-envelope")},
		Offsite:    OffsiteEvidence{Provider: "spaces", Bucket: "bonfire-dr", ObjectKey: "snapshots/set-1.enc", ObjectVersion: "version-1", ReceiptID: "offsite-receipt-1", ObjectSHA256: testDigest("encrypted-envelope"), StoredAt: "2026-07-22T20:02:00Z"},
	}
	manifest, err := NewBackupManifest(spec, testSigningKey())
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func candidateFor(manifest BackupManifest) RestoreCandidate {
	return RestoreCandidate{
		TenantID: manifest.TenantID, ManifestSHA256: manifest.ManifestSHA256, ReleaseCommit: manifest.ReleaseCommit,
		Artifacts: append([]SnapshotArtifact(nil), manifest.Artifacts...), DatabasePurge: manifest.Purge,
		CustodyReceiptID: manifest.Custody.ReceiptID, EncryptionKeyID: manifest.Encryption.KeyID, OffsiteReceiptID: manifest.Offsite.ReceiptID,
	}
}

func TestPurgeAuthorityIsIndependentAppendOnlyAndRejectsRollback(t *testing.T) {
	root := t.TempDir()
	protected := filepath.Join(root, "meeting-data")
	if err := os.MkdirAll(protected, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPurgeAuthority(filepath.Join(protected, "purge-authority.jsonl"), testSigningKey(), []string{protected}); err == nil {
		t.Fatal("authority inside protected snapshot root was accepted")
	}
	authority, err := OpenPurgeAuthority(filepath.Join(root, "independent", "purge-authority.jsonl"), testSigningKey(), []string{protected})
	if err != nil {
		t.Fatal(err)
	}
	state5 := PurgeState{TenantID: "tenant-a", HighWater: 5, ManifestSHA256: testDigest("purges-through-5")}
	first, appended, err := authority.Append(context.Background(), state5, testRelease(), time.Date(2026, 7, 22, 19, 0, 0, 0, time.UTC))
	if err != nil || !appended || first.Sequence != 1 {
		t.Fatalf("first=%+v appended=%t err=%v", first, appended, err)
	}
	repeat, appended, err := authority.Append(context.Background(), state5, testRelease(), time.Date(2026, 7, 22, 19, 1, 0, 0, time.UTC))
	if err != nil || appended || repeat.RecordSHA256 != first.RecordSHA256 {
		t.Fatalf("idempotent append=%+v appended=%t err=%v", repeat, appended, err)
	}
	rollback := state5
	rollback.HighWater = 4
	rollback.ManifestSHA256 = testDigest("purges-through-4")
	if _, _, err := authority.Append(context.Background(), rollback, testRelease(), time.Now().UTC()); !errors.Is(err, ErrAuthorityRollback) {
		t.Fatalf("rollback err=%v", err)
	}
	conflict := state5
	conflict.ManifestSHA256 = testDigest("conflicting-five")
	if _, _, err := authority.Append(context.Background(), conflict, testRelease(), time.Now().UTC()); !errors.Is(err, ErrAuthorityMismatch) {
		t.Fatalf("same-water conflict err=%v", err)
	}
}

func TestBackupManifestAndRestorePreflightAreDeterministicAndBodyFree(t *testing.T) {
	authority := testAuthority(t)
	state := PurgeState{TenantID: "tenant-a", HighWater: 5, ManifestSHA256: testDigest("purges-through-5")}
	record, _, err := authority.Append(context.Background(), state, testRelease(), time.Date(2026, 7, 22, 19, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	manifest := testManifest(t, state, record)
	again := testManifest(t, state, record)
	if !reflect.DeepEqual(manifest, again) || VerifyBackupManifest(manifest, testSigningKey()) != nil {
		t.Fatal("backup manifest was not deterministic and verifiable")
	}
	evaluatedAt := time.Date(2026, 7, 22, 21, 0, 0, 0, time.UTC)
	receipt := PreflightRestore(manifest, candidateFor(manifest), authority, testRelease(), evaluatedAt, testSigningKey())
	receiptAgain := PreflightRestore(manifest, candidateFor(manifest), authority, testRelease(), evaluatedAt, testSigningKey())
	if !receipt.Ready || len(receipt.FailureCodes) != 0 || !reflect.DeepEqual(receipt, receiptAgain) || VerifyDrillReceipt(receipt, testSigningKey()) != nil {
		t.Fatalf("receipt=%+v again=%+v", receipt, receiptAgain)
	}
	raw, _ := json.Marshal(receipt)
	for _, secretBody := range []string{"meeting transcript private body", "customer artifact contents", "purge destruction evidence"} {
		if bytes.Contains(raw, []byte(secretBody)) {
			t.Fatalf("DR receipt leaked body %q: %s", secretBody, raw)
		}
	}
}

func TestRestorePreflightRejectsReleaseSnapshotAndMissingEvidence(t *testing.T) {
	authority := testAuthority(t)
	state := PurgeState{TenantID: "tenant-a", HighWater: 2, ManifestSHA256: testDigest("purges-two")}
	record, _, err := authority.Append(context.Background(), state, testRelease(), time.Date(2026, 7, 22, 19, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	manifest := testManifest(t, state, record)
	candidate := candidateFor(manifest)
	candidate.ReleaseCommit = strings.Repeat("b", 40)
	candidate.Artifacts[0].SHA256 = testDigest("tampered-snapshot")
	candidate.CustodyReceiptID = ""
	candidate.EncryptionKeyID = ""
	candidate.OffsiteReceiptID = ""
	receipt := PreflightRestore(manifest, candidate, authority, testRelease(), time.Date(2026, 7, 22, 21, 0, 0, 0, time.UTC), testSigningKey())
	want := []string{FailureCustodyMissing, FailureEncryptionMissing, FailureOffsiteMissing, FailureReleaseMismatch, FailureSnapshotMismatch}
	if receipt.Ready || !reflect.DeepEqual(receipt.FailureCodes, want) {
		t.Fatalf("receipt=%+v want failures=%v", receipt, want)
	}
}

func TestRestorePreflightRejectsOlderDatabaseWhenPurgeAuthorityIsNewer(t *testing.T) {
	authority := testAuthority(t)
	state5 := PurgeState{TenantID: "tenant-a", HighWater: 5, ManifestSHA256: testDigest("purges-five")}
	record5, _, err := authority.Append(context.Background(), state5, testRelease(), time.Date(2026, 7, 22, 19, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	manifest := testManifest(t, state5, record5)
	state6 := PurgeState{TenantID: "tenant-a", HighWater: 6, ManifestSHA256: testDigest("purges-six")}
	if _, _, err := authority.Append(context.Background(), state6, testRelease(), time.Date(2026, 7, 22, 20, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	receipt := PreflightRestore(manifest, candidateFor(manifest), authority, testRelease(), time.Date(2026, 7, 22, 21, 0, 0, 0, time.UTC), testSigningKey())
	if receipt.Ready || !reflect.DeepEqual(receipt.FailureCodes, []string{FailurePurgeRollback}) {
		t.Fatalf("older DB was accepted against newer purge authority: %+v", receipt)
	}
}

func TestPurgeAuthorityAndBackupManifestTamperFailClosed(t *testing.T) {
	authority := testAuthority(t)
	state := PurgeState{TenantID: "tenant-a", HighWater: 3, ManifestSHA256: testDigest("purges-three")}
	record, _, err := authority.Append(context.Background(), state, testRelease(), time.Date(2026, 7, 22, 19, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	manifest := testManifest(t, state, record)
	manifest.Artifacts[0].SHA256 = testDigest("tampered")
	if err := VerifyBackupManifest(manifest, testSigningKey()); err == nil {
		t.Fatal("tampered signed manifest verified")
	}
	raw, err := os.ReadFile(authority.Path())
	if err != nil {
		t.Fatal(err)
	}
	var disk PurgeAuthorityRecord
	if err := json.Unmarshal(bytes.TrimSpace(raw), &disk); err != nil {
		t.Fatal(err)
	}
	disk.PurgeHighWater++
	tampered, _ := json.Marshal(disk)
	if err := os.WriteFile(authority.Path(), append(tampered, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := authority.Latest(state.TenantID); !errors.Is(err, ErrAuthorityTampered) {
		t.Fatalf("tampered authority err=%v", err)
	}
}
