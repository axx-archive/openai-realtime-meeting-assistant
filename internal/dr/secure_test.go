package dr

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

func securePair(role EvidenceRole, fill byte) (PrivateSigner, PublicVerifier) {
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = fill
	}
	private := ed25519.NewKeyFromSeed(seed)
	id := string(role) + "-key-v1"
	return PrivateSigner{ID: id, Role: role, Private: private}, PublicVerifier{ID: id, Role: role, Public: private.Public().(ed25519.PublicKey)}
}

func secureFixture(t *testing.T) (SecureManifest, SecureRestoreInput, SecureTrust, PrivateSigner, PublicVerifier) {
	t.Helper()
	now := time.Date(2026, 7, 22, 20, 0, 0, 0, time.UTC)
	roles := []EvidenceRole{RoleAuthority, RoleManifest, RoleReceipt, RoleProvider, RoleCustody, RoleEncryption, RoleRelease}
	signers := map[EvidenceRole]PrivateSigner{}
	verifiers := map[EvidenceRole]PublicVerifier{}
	for index, role := range roles {
		signers[role], verifiers[role] = securePair(role, byte(index+1))
	}
	trust := SecureTrust{Authority: verifiers[RoleAuthority], Manifest: verifiers[RoleManifest], Provider: verifiers[RoleProvider], Custody: verifiers[RoleCustody], Encryption: verifiers[RoleEncryption], Release: verifiers[RoleRelease]}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sources := []ArtifactSource{}
	for index, volume := range requiredVolumes {
		path := filepath.Join(root, volume+".snapshot")
		if err := os.WriteFile(path, []byte(strings.Repeat(volume, index+1)), 0o600); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, ArtifactSource{Volume: volume, SnapshotID: "snapshot-" + volume, Path: path})
	}
	artifacts, err := HashArtifactSources(sources)
	if err != nil {
		t.Fatal(err)
	}
	setDigest, _ := snapshotSetDigest(artifacts)
	envelopePath := filepath.Join(root, "four-volume-envelope.enc")
	envelopeBytes := []byte("encrypted-four-volume-envelope")
	if err := os.WriteFile(envelopePath, envelopeBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	envelope := digestBytes(envelopeBytes)
	provider, err := SignExternalEvidence(ExternalEvidence{Kind: RoleProvider, ReceiptID: "provider-receipt-1", Provider: "object-store", Location: "region-b/object-v1", SubjectSHA256: envelope, IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano)}, signers[RoleProvider])
	if err != nil {
		t.Fatal(err)
	}
	custody, _ := SignExternalEvidence(ExternalEvidence{Kind: RoleCustody, ReceiptID: "custody-receipt-1", Provider: "ops-vault", Location: "immutable-vault", SubjectSHA256: setDigest, IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano)}, signers[RoleCustody])
	encryption, _ := SignExternalEvidence(ExternalEvidence{Kind: RoleEncryption, ReceiptID: "encryption-receipt-1", Provider: "kms", Location: "key-v3", SubjectSHA256: envelope, IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano)}, signers[RoleEncryption])
	binaryPath := filepath.Join(root, "meetingassist")
	if err := os.WriteFile(binaryPath, []byte("immutable release binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	binaryDigest, _, _ := hashPathNoLinks(binaryPath)
	releaseCommit := strings.Repeat("a", 40)
	imageDigest := "sha256:" + testDigest("oci-image")
	tenants := []PurgeState{{TenantID: "tenant-a", HighWater: 4, ManifestSHA256: testDigest("tenant-a-purges")}, {TenantID: "tenant-b", HighWater: 2, ManifestSHA256: testDigest("tenant-b-purges")}}
	release, _ := SignReleaseAttestation(ReleaseAttestation{ReleaseCommit: releaseCommit, ImageDigest: imageDigest, BinarySHA256: binaryDigest, SourceArchiveSHA256: testDigest("source-archive"), IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339Nano)}, signers[RoleRelease])
	head, _ := SignAuthorityHead(AuthorityHead{Sequence: 7, RecordSHA256: testDigest("authority-record-7"), PreviousHeadSHA256: testDigest("authority-head-6"), RecordedAt: now.Format(time.RFC3339Nano), Tenants: tenants}, signers[RoleAuthority])
	headDigest, _ := EvidenceDigest(head)
	databaseDigest := testDigest("complete-logical-database")
	manifest, err := NewSecureManifest(SecureManifest{CreatedAt: now.Format(time.RFC3339Nano), Release: release, Barrier: CaptureBarrier{BarrierID: "capture-7", WriteFenceSHA256: testDigest("write-fence"), PostgresLSN: "0/16B6C50", MeetingHighWater: "memory-104", QueueHighWater: "queue-9", UsageHighWater: "usage-81", StartedAt: now.Add(-time.Minute).Format(time.RFC3339Nano), CompletedAt: now.Format(time.RFC3339Nano)}, Artifacts: artifacts, Tenants: tenants, DatabaseSHA256: databaseDigest, AuthorityHeadSHA256: headDigest, AuthorityHead: head, Provider: provider, Custody: custody, Encryption: encryption}, signers[RoleManifest], trust, now)
	if err != nil {
		t.Fatal(err)
	}
	input := SecureRestoreInput{EnvironmentID: "restore-host-20260722", Nonce: strings.Repeat("n", 32), EvaluatedAt: now.Add(time.Minute), ExpiresAt: now.Add(31 * time.Minute), Sources: sources, DownloadedEnvelopePath: envelopePath, Tenants: tenants, AuthorityTenantPrefixes: tenants, DatabaseSHA256: databaseDigest, RunningReleaseCommit: releaseCommit, RunningImageDigest: imageDigest, RunningBinaryPath: binaryPath, PinnedAuthorityHeadSHA256: headDigest, AuthorityHead: head}
	return manifest, input, trust, signers[RoleReceipt], verifiers[RoleReceipt]
}

func TestSecurePreflightHashesBytesAndBindsReceipt(t *testing.T) {
	manifest, input, trust, receiptSigner, receiptVerifier := secureFixture(t)
	receipt := SecurePreflight(manifest, input, trust, receiptSigner)
	if !receipt.Ready || len(receipt.FailureCodes) != 0 {
		t.Fatalf("receipt=%+v", receipt)
	}
	if err := VerifySecureReceipt(receipt, receiptVerifier, input.EnvironmentID, input.Nonce, receipt.CandidateSHA256, input.DatabaseSHA256, input.RunningImageDigest, input.PinnedAuthorityHeadSHA256, input.EvaluatedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input.Sources[0].Path, []byte("changed restored bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	tampered := SecurePreflight(manifest, input, trust, receiptSigner)
	if tampered.Ready || !containsString(tampered.FailureCodes, "artifact_bytes_mismatch") {
		t.Fatalf("tampered receipt=%+v", tampered)
	}
}

func TestSecurePreflightRejectsAuthorityRollbackAndMissingTenant(t *testing.T) {
	manifest, input, trust, signer, _ := secureFixture(t)
	input.PinnedAuthorityHeadSHA256 = testDigest("rolled-back-external-head")
	input.Tenants = input.Tenants[:1]
	receipt := SecurePreflight(manifest, input, trust, signer)
	if receipt.Ready || !containsString(receipt.FailureCodes, "authority_head_invalid") || !containsString(receipt.FailureCodes, "tenant_coverage_mismatch") {
		t.Fatalf("receipt=%+v", receipt)
	}
}

func TestSecureCandidateRejectsDatabaseOlderThanCurrentAuthorityHead(t *testing.T) {
	manifest, input, trust, signer, _ := secureFixture(t)
	input.Tenants = append([]PurgeState(nil), input.Tenants...)
	input.AuthorityTenantPrefixes = append([]PurgeState(nil), input.AuthorityTenantPrefixes...)
	input.Tenants[0].HighWater--
	input.AuthorityTenantPrefixes[0].HighWater--
	receipt := SecurePreflight(manifest, input, trust, signer)
	if receipt.Ready || !containsString(receipt.FailureCodes, "tenant_coverage_mismatch") {
		t.Fatalf("old database accepted against current authority head: %+v", receipt)
	}
	if _, failures := VerifySecureCandidate(manifest, input, trust); !containsString(failures, "tenant_coverage_mismatch") {
		t.Fatalf("boot candidate accepted old database: %v", failures)
	}
}

func TestAuthorityAdvanceRequiresExactOldPrefixesAndAllowsNewTenants(t *testing.T) {
	old := []PurgeState{
		{TenantID: "tenant-a", HighWater: 2, ManifestSHA256: testDigest("tenant-a-prefix-2")},
		{TenantID: "tenant-b", HighWater: 1, ManifestSHA256: testDigest("tenant-b-prefix-1")},
	}
	current := []PurgeState{
		{TenantID: "tenant-a", HighWater: 4, ManifestSHA256: testDigest("tenant-a-current-4")},
		{TenantID: "tenant-b", HighWater: 1, ManifestSHA256: testDigest("tenant-b-prefix-1")},
		{TenantID: "tenant-new", HighWater: 0, ManifestSHA256: testDigest("empty")},
	}
	prefixes := append([]PurgeState(nil), old...)
	if !tenantStatesExtendAuthority(current, prefixes, old) {
		t.Fatal("valid monotonic extension with a new tenant was refused")
	}

	changedPrefix := append([]PurgeState(nil), prefixes...)
	changedPrefix[0].ManifestSHA256 = testDigest("laundered-old-prefix")
	if tenantStatesExtendAuthority(current, changedPrefix, old) {
		t.Fatal("changed old-tenant purge prefix was accepted")
	}

	removedTenant := current[:1]
	if tenantStatesExtendAuthority(removedTenant, prefixes, old) {
		t.Fatal("removal of an old authority tenant was accepted")
	}
}

func TestSecureCandidateReopensBytesAfterPreflight(t *testing.T) {
	manifest, input, trust, signer, verifier := secureFixture(t)
	receipt := SecurePreflight(manifest, input, trust, signer)
	if !receipt.Ready {
		t.Fatalf("preflight was not ready: %+v", receipt)
	}
	if err := os.WriteFile(input.DownloadedEnvelopePath, []byte("mutated after preflight"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate, failures := VerifySecureCandidate(manifest, input, trust)
	if !containsString(failures, "offsite_object_mismatch") || candidate == receipt.CandidateSHA256 {
		t.Fatalf("post-preflight mutation was not rebound: candidate=%s failures=%v", candidate, failures)
	}
	if err := VerifySecureReceipt(receipt, verifier, input.EnvironmentID, input.Nonce, candidate, input.DatabaseSHA256, input.RunningImageDigest, input.PinnedAuthorityHeadSHA256, input.EvaluatedAt); err == nil {
		t.Fatal("old receipt accepted recomputed mutated candidate")
	}
}

func TestSecureCandidateRejectsPostPreflightArbitraryDatabaseRowMutation(t *testing.T) {
	manifest, input, trust, signer, verifier := secureFixture(t)
	receipt := SecurePreflight(manifest, input, trust, signer)
	if !receipt.Ready || receipt.DatabaseSHA256 != input.DatabaseSHA256 {
		t.Fatalf("preflight did not bind database digest: %+v", receipt)
	}
	input.DatabaseSHA256 = testDigest("logical-database-after-non-purge-row-update")
	candidate, failures := VerifySecureCandidate(manifest, input, trust)
	if !containsString(failures, "database_digest_mismatch") || candidate == receipt.CandidateSHA256 {
		t.Fatalf("database mutation was not reflected in candidate: candidate=%s failures=%v", candidate, failures)
	}
	if err := VerifySecureReceipt(receipt, verifier, input.EnvironmentID, input.Nonce, candidate, input.DatabaseSHA256, input.RunningImageDigest, input.PinnedAuthorityHeadSHA256, input.EvaluatedAt); err == nil {
		t.Fatal("pre-mutation receipt accepted the post-mutation logical database")
	}
}

func TestSecurePreflightRejectsDatabaseMutatedAfterCapture(t *testing.T) {
	manifest, input, trust, signer, _ := secureFixture(t)
	input.DatabaseSHA256 = testDigest("database-mutated-after-signed-capture")
	receipt := SecurePreflight(manifest, input, trust, signer)
	if receipt.Ready || !containsString(receipt.FailureCodes, "database_digest_mismatch") {
		t.Fatalf("preflight blessed a database different from the signed capture: %+v", receipt)
	}
	manifest.DatabaseSHA256 = input.DatabaseSHA256
	forged := SecurePreflight(manifest, input, trust, signer)
	if forged.Ready || !containsString(forged.FailureCodes, "manifest_invalid") {
		t.Fatalf("unsigned replacement of the manifest database digest was accepted: %+v", forged)
	}
}

func TestVerifySecureReceiptRejectsForgedOverlongLifetime(t *testing.T) {
	manifest, input, trust, signer, verifier := secureFixture(t)
	receipt := SecurePreflight(manifest, input, trust, signer)
	receipt.ExpiresAt = input.EvaluatedAt.Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	raw, err := json.Marshal(receipt.payload())
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	receipt.ReceiptSHA256 = hex.EncodeToString(digest[:])
	receipt.Signature, err = signPayload(signer, "restore_receipt", receipt.payload())
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySecureReceipt(receipt, verifier, input.EnvironmentID, input.Nonce, receipt.CandidateSHA256, input.DatabaseSHA256, input.RunningImageDigest, input.PinnedAuthorityHeadSHA256, input.EvaluatedAt.Add(time.Minute)); err == nil {
		t.Fatal("validly signed receipt with a lifetime over one hour was accepted")
	}
}

func TestReleaseAttestationExpires(t *testing.T) {
	manifest, _, trust, _, _ := secureFixture(t)
	expires, err := time.Parse(time.RFC3339Nano, manifest.Release.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyReleaseAttestation(manifest.Release, trust.Release, expires); err == nil {
		t.Fatal("stale OCI release attestation accepted")
	}
}

func TestSecureReceiptIsEnvironmentNonceAndSingleUseBound(t *testing.T) {
	manifest, input, trust, signer, verifier := secureFixture(t)
	receipt := SecurePreflight(manifest, input, trust, signer)
	if err := VerifySecureReceipt(receipt, verifier, "other-host", input.Nonce, receipt.CandidateSHA256, input.DatabaseSHA256, input.RunningImageDigest, input.PinnedAuthorityHeadSHA256, input.EvaluatedAt); err == nil {
		t.Fatal("receipt replayed in another environment")
	}
	markerDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(markerDir, "receipt")
	if err := ConsumeSecureReceipt(marker, receipt); err != nil {
		t.Fatal(err)
	}
	if err := ConsumeSecureReceipt(marker, receipt); err == nil {
		t.Fatal("receipt consumed twice")
	}
}

func TestSecureManifestRejectsOneKeyAcrossEvidenceRoles(t *testing.T) {
	manifest, input, trust, signer, _ := secureFixture(t)
	trust.Custody.Public = append(ed25519.PublicKey(nil), trust.Provider.Public...)
	if receipt := SecurePreflight(manifest, input, trust, signer); receipt.Ready || !containsString(receipt.FailureCodes, "manifest_invalid") || !containsString(receipt.FailureCodes, "receipt_signer_invalid") {
		t.Fatalf("same-key evidence was accepted: %+v", receipt)
	}
}

func TestHashArtifactSourcesRejectsSymlink(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	sources := []ArtifactSource{}
	for index, volume := range requiredVolumes {
		path := filepath.Join(root, fmt.Sprintf("artifact-%d", index))
		if err := os.WriteFile(path, []byte(volume), 0o600); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, ArtifactSource{Volume: volume, SnapshotID: "snap-" + volume, Path: path})
	}
	sources[0].Path = link
	if _, err := HashArtifactSources(sources); err == nil {
		t.Fatal("symlink artifact accepted")
	}
	pin := filepath.Join(root, "head.sha256")
	if err := os.WriteFile(pin, []byte(testDigest("head")+"\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadPinnedDigestFile(pin); err != nil || got != testDigest("head") {
		t.Fatalf("pin=%q err=%v", got, err)
	}
	if _, err := ReadPinnedDigestFile(link); err == nil {
		t.Fatal("symlink authority pin accepted")
	}
	if err := ValidateArtifactRoot(link); err == nil {
		t.Fatal("symlink configured root accepted")
	}
}

func TestHashArtifactSourcesRejectsSymlinkedParent(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	realParent := filepath.Join(root, "real-parent")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(realParent, "artifact")
	if err := os.WriteFile(artifact, []byte("snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(root, "linked-parent")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatal(err)
	}
	sources := make([]ArtifactSource, 0, len(requiredVolumes))
	for index, volume := range requiredVolumes {
		path := filepath.Join(root, fmt.Sprintf("parent-artifact-%d", index))
		if err := os.WriteFile(path, []byte(volume), 0o600); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, ArtifactSource{Volume: volume, SnapshotID: "snap-" + volume, Path: path})
	}
	sources[0].Path = filepath.Join(linkedParent, "artifact")
	if _, err := HashArtifactSources(sources); err == nil {
		t.Fatal("artifact beneath symlinked parent accepted")
	}
	if _, err := ReadFileNoLinks(filepath.Join(linkedParent, "artifact"), 64); err == nil {
		t.Fatal("control file beneath symlinked parent accepted")
	}
	if err := ValidateArtifactRoot(filepath.Join(linkedParent, "artifact")); err == nil {
		t.Fatal("configured root beneath symlinked parent accepted")
	}
}

func TestPathStabilityRejectsRootSwapAfterOpen(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "candidate")
	if err := os.WriteFile(path, []byte("verified bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	opened, err := openPathNoFollow(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyPathStillOpened(path, opened); err == nil {
		t.Fatal("root inode swap after open was accepted")
	}
}

func TestReceiptMarkerMustBeOutsideAllFourRestoredRoots(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	roots := []string{}
	for _, name := range requiredVolumes {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		roots = append(roots, path)
	}
	if err := ValidateIndependentPath(filepath.Join(roots[0], "used"), roots); err == nil {
		t.Fatal("receipt marker inside restored root accepted")
	}
	if err := ValidateIndependentPath(filepath.Join(root, "independent", "used"), roots); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalAndTenantTableRegistriesMatchMigrations(t *testing.T) {
	entries, err := os.ReadDir("../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	tablePattern := regexp.MustCompile(`(?is)CREATE TABLE(?: IF NOT EXISTS)?\s+([a-z_]+)\s*\((.*?)\);`)
	tenantPattern := regexp.MustCompile(`(?m)^\s*tenant_id\s+`)
	allTables := []string{}
	tenantTables := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("../../migrations", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range tablePattern.FindAllSubmatch(raw, -1) {
			allTables = append(allTables, string(match[1]))
			if tenantPattern.Match(match[2]) {
				tenantTables = append(tenantTables, string(match[1]))
			}
		}
	}
	sort.Strings(allTables)
	sort.Strings(tenantTables)
	expectedAll := append([]string(nil), canonicalTables...)
	expectedTenant := append([]string(nil), tenantBearingTables...)
	sort.Strings(expectedAll)
	sort.Strings(expectedTenant)
	if !reflect.DeepEqual(allTables, expectedAll) {
		t.Fatalf("canonical registry drifted from migrations: migrations=%v registry=%v", allTables, expectedAll)
	}
	if !reflect.DeepEqual(tenantTables, expectedTenant) {
		t.Fatalf("tenant registry drifted from migrations: migrations=%v registry=%v", tenantTables, expectedTenant)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func digestBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
