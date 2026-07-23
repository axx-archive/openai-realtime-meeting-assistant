package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/openai/openai-realtime-meeting-assistant/internal/dr"
)

type stringList []string

func (list *stringList) String() string { return strings.Join(*list, ",") }
func (list *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("path is empty")
	}
	*list = append(*list, value)
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "bonfire-dr:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("command required: secure-manifest-create, secure-preflight, authority-head-advance, or a legacy drill command")
	}
	switch args[0] {
	case "secure-manifest-create":
		return runSecureManifestCreate(args[1:], stdout)
	case "secure-preflight":
		return runSecurePreflight(args[1:], stdout)
	case "authority-head-advance":
		return runAuthorityHeadAdvance(args[1:], stdout)
	case "release-attest":
		return runReleaseAttest(args[1:], stdout)
	}
	key, err := signingKeyFromEnvironment()
	if err != nil {
		return err
	}
	switch args[0] {
	case "authority-append":
		return runAuthorityAppend(args[1:], stdout, key)
	case "manifest-create":
		return runManifestCreate(args[1:], stdout, key)
	case "preflight":
		return runPreflight(args[1:], stdout, key)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

type releaseAttestSpec struct {
	ReleaseCommit     string `json:"releaseCommit"`
	BinaryPath        string `json:"binaryPath"`
	SourceArchivePath string `json:"sourceArchivePath"`
	IssuedAt          string `json:"issuedAt"`
	ExpiresAt         string `json:"expiresAt"`
}

func runReleaseAttest(args []string, stdout io.Writer) error {
	set := flag.NewFlagSet("release-attest", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	specPath := set.String("spec", "", "release attestation input")
	outPath := set.String("out", "", "signed release attestation")
	if err := set.Parse(args); err != nil {
		return err
	}
	var spec releaseAttestSpec
	if err := readJSONFile(*specPath, &spec); err != nil {
		return err
	}
	binaryDigest, _, err := dr.HashArtifactPath(spec.BinaryPath)
	if err != nil {
		return err
	}
	sourceDigest, _, err := dr.HashArtifactPath(spec.SourceArchivePath)
	if err != nil {
		return err
	}
	imageDigest, err := dr.ReadPinnedDigestFile(os.Getenv("BONFIRE_RUNNING_IMAGE_DIGEST_PIN_PATH"))
	if err != nil {
		return err
	}
	signer, err := privateSignerFromEnvironment(dr.RoleRelease)
	if err != nil {
		return err
	}
	attestation, err := dr.SignReleaseAttestation(dr.ReleaseAttestation{ReleaseCommit: spec.ReleaseCommit, ImageDigest: "sha256:" + imageDigest, BinarySHA256: binaryDigest, SourceArchiveSHA256: sourceDigest, IssuedAt: spec.IssuedAt, ExpiresAt: spec.ExpiresAt}, signer)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*outPath) == "" {
		return errors.New("out is required")
	}
	if err := writeJSONFile(*outPath, attestation); err != nil {
		return err
	}
	return writeJSON(stdout, attestation)
}

type secureManifestCreateSpec struct {
	CreatedAt           string                `json:"createdAt"`
	Release             dr.ReleaseAttestation `json:"release"`
	Barrier             dr.CaptureBarrier     `json:"barrier"`
	Sources             []dr.ArtifactSource   `json:"sources"`
	EnvelopePath        string                `json:"envelopePath"`
	AuthorityHeadSHA256 string                `json:"authorityHeadSha256"`
	AuthorityHead       dr.AuthorityHead      `json:"authorityHead"`
	Provider            dr.ExternalEvidence   `json:"provider"`
	Custody             dr.ExternalEvidence   `json:"custody"`
	Encryption          dr.ExternalEvidence   `json:"encryption"`
}

func runSecureManifestCreate(args []string, stdout io.Writer) error {
	set := flag.NewFlagSet("secure-manifest-create", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	specPath := set.String("spec", "", "capture spec containing real four-volume source paths")
	databaseURL := set.String("database-url", "", "capture PostgreSQL URL; queried in a read-only repeatable-read transaction")
	outPath := set.String("out", "", "signed secure manifest JSON")
	if err := set.Parse(args); err != nil {
		return err
	}
	var spec secureManifestCreateSpec
	if err := readJSONFile(*specPath, &spec); err != nil {
		return err
	}
	if err := verifyConfiguredArtifactSources(spec.Sources); err != nil {
		return err
	}
	artifacts, err := dr.HashArtifactSources(spec.Sources)
	if err != nil {
		return err
	}
	envelopeDigest, _, err := dr.HashArtifactPath(spec.EnvelopePath)
	if err != nil {
		return fmt.Errorf("encrypted object bytes: %w", err)
	}
	if envelopeDigest != spec.Provider.SubjectSHA256 || envelopeDigest != spec.Encryption.SubjectSHA256 {
		return errors.New("encrypted object bytes do not match provider and encryption evidence")
	}
	authorityPin, err := dr.ReadPinnedDigestFile(os.Getenv("BONFIRE_DR_AUTHORITY_HEAD_PIN_PATH"))
	if err != nil {
		return err
	}
	if spec.AuthorityHeadSHA256 != authorityPin {
		return errors.New("capture spec authority head does not match independently mounted pin")
	}
	databaseState, err := dr.QueryDatabaseState(context.Background(), *databaseURL, spec.AuthorityHead.Tenants)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(databaseState.Tenants, databaseState.AuthorityTenantPrefixes) {
		return errors.New("capture database is not exactly at the signed authority head")
	}
	trust, err := secureTrustFromEnvironment()
	if err != nil {
		return err
	}
	signer, err := privateSignerFromEnvironment(dr.RoleManifest)
	if err != nil {
		return err
	}
	now, err := time.Parse(time.RFC3339Nano, spec.CreatedAt)
	if err != nil {
		return err
	}
	manifest, err := dr.NewSecureManifest(dr.SecureManifest{CreatedAt: spec.CreatedAt, Release: spec.Release, Barrier: spec.Barrier, Artifacts: artifacts, Tenants: databaseState.Tenants, DatabaseSHA256: databaseState.LogicalSHA256, AuthorityHeadSHA256: spec.AuthorityHeadSHA256, AuthorityHead: spec.AuthorityHead, Provider: spec.Provider, Custody: spec.Custody, Encryption: spec.Encryption}, signer, trust, now)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*outPath) == "" {
		return errors.New("out is required")
	}
	if err := writeJSONFile(*outPath, manifest); err != nil {
		return err
	}
	return writeJSON(stdout, manifest)
}

type securePreflightSpec struct {
	EnvironmentID             string              `json:"environmentId"`
	Nonce                     string              `json:"nonce"`
	EvaluatedAt               string              `json:"evaluatedAt"`
	ExpiresAt                 string              `json:"expiresAt"`
	Sources                   []dr.ArtifactSource `json:"sources"`
	DownloadedEnvelopePath    string              `json:"downloadedEnvelopePath"`
	RunningReleaseCommit      string              `json:"runningReleaseCommit"`
	RunningImageDigest        string              `json:"runningImageDigest"`
	RunningBinaryPath         string              `json:"runningBinaryPath"`
	PinnedAuthorityHeadSHA256 string              `json:"pinnedAuthorityHeadSha256"`
	AuthorityHead             dr.AuthorityHead    `json:"authorityHead"`
}

func runSecurePreflight(args []string, stdout io.Writer) error {
	set := flag.NewFlagSet("secure-preflight", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	manifestPath := set.String("manifest", "", "signed secure backup manifest")
	specPath := set.String("spec", "", "isolated restore evidence spec")
	databaseURL := set.String("database-url", "", "restored PostgreSQL URL")
	receiptPath := set.String("receipt-out", "", "signed one-time receipt")
	if err := set.Parse(args); err != nil {
		return err
	}
	var manifest dr.SecureManifest
	if err := readJSONFile(*manifestPath, &manifest); err != nil {
		return err
	}
	var spec securePreflightSpec
	if err := readJSONFile(*specPath, &spec); err != nil {
		return err
	}
	if err := verifyConfiguredArtifactSources(spec.Sources); err != nil {
		return err
	}
	authorityPin, err := dr.ReadPinnedDigestFile(os.Getenv("BONFIRE_DR_AUTHORITY_HEAD_PIN_PATH"))
	if err != nil {
		return err
	}
	if spec.PinnedAuthorityHeadSHA256 != authorityPin {
		return errors.New("restore spec authority head does not match independently mounted pin")
	}
	imagePin, err := dr.ReadPinnedDigestFile(os.Getenv("BONFIRE_RUNNING_IMAGE_DIGEST_PIN_PATH"))
	if err != nil {
		return err
	}
	if spec.RunningImageDigest != "sha256:"+imagePin {
		return errors.New("restore spec image digest does not match runtime-mounted OCI pin")
	}
	evaluatedAt, err := time.Parse(time.RFC3339Nano, spec.EvaluatedAt)
	if err != nil {
		return err
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, spec.ExpiresAt)
	if err != nil {
		return err
	}
	databaseState, err := dr.QueryDatabaseState(context.Background(), *databaseURL, spec.AuthorityHead.Tenants)
	if err != nil {
		return err
	}
	trust, err := secureTrustFromEnvironment()
	if err != nil {
		return err
	}
	signer, err := privateSignerFromEnvironment(dr.RoleReceipt)
	if err != nil {
		return err
	}
	receipt := dr.SecurePreflight(manifest, dr.SecureRestoreInput{EnvironmentID: spec.EnvironmentID, Nonce: spec.Nonce, EvaluatedAt: evaluatedAt, ExpiresAt: expiresAt, Sources: spec.Sources, DownloadedEnvelopePath: spec.DownloadedEnvelopePath, Tenants: databaseState.Tenants, AuthorityTenantPrefixes: databaseState.AuthorityTenantPrefixes, DatabaseSHA256: databaseState.LogicalSHA256, RunningReleaseCommit: spec.RunningReleaseCommit, RunningImageDigest: spec.RunningImageDigest, RunningBinaryPath: spec.RunningBinaryPath, PinnedAuthorityHeadSHA256: spec.PinnedAuthorityHeadSHA256, AuthorityHead: spec.AuthorityHead}, trust, signer)
	if strings.TrimSpace(*receiptPath) == "" {
		return errors.New("receipt-out is required")
	}
	if err := writeJSONFile(*receiptPath, receipt); err != nil {
		return err
	}
	if err := writeJSON(stdout, receipt); err != nil {
		return err
	}
	if !receipt.Ready {
		return fmt.Errorf("secure restore readiness refused: %s", strings.Join(receipt.FailureCodes, ","))
	}
	return nil
}

type authorityHeadAdvanceSpec struct {
	Previous       *dr.AuthorityHead `json:"previous,omitempty"`
	PreviousSHA256 string            `json:"previousSha256,omitempty"`
	RecordSHA256   string            `json:"recordSha256"`
	RecordedAt     string            `json:"recordedAt"`
}

func runAuthorityHeadAdvance(args []string, stdout io.Writer) error {
	set := flag.NewFlagSet("authority-head-advance", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	specPath := set.String("spec", "", "authority head advance spec")
	outPath := set.String("out", "", "new signed head")
	databaseURL := set.String("database-url", "", "authoritative PostgreSQL URL")
	if err := set.Parse(args); err != nil {
		return err
	}
	var spec authorityHeadAdvanceSpec
	if err := readJSONFile(*specPath, &spec); err != nil {
		return err
	}
	signer, err := privateSignerFromEnvironment(dr.RoleAuthority)
	if err != nil {
		return err
	}
	sequence := uint64(1)
	previousDigest := ""
	if spec.Previous != nil {
		verifier, verifyErr := publicVerifierFromEnvironment(dr.RoleAuthority)
		if verifyErr != nil {
			return verifyErr
		}
		if err := dr.VerifyAuthorityHead(*spec.Previous, verifier, spec.PreviousSHA256); err != nil {
			return fmt.Errorf("previous externally pinned authority head: %w", err)
		}
		sequence = spec.Previous.Sequence + 1
		previousDigest, err = dr.EvidenceDigest(*spec.Previous)
		if err != nil {
			return err
		}
	}
	var tenants []dr.PurgeState
	if spec.Previous == nil {
		tenants, _, err = dr.QueryPurgeStates(context.Background(), *databaseURL, nil)
	} else {
		tenants, err = dr.QueryPurgeStatesForAuthorityAdvance(context.Background(), *databaseURL, spec.Previous.Tenants)
	}
	if err != nil {
		return err
	}
	head, err := dr.SignAuthorityHead(dr.AuthorityHead{Sequence: sequence, RecordSHA256: spec.RecordSHA256, PreviousHeadSHA256: previousDigest, RecordedAt: spec.RecordedAt, Tenants: tenants}, signer)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*outPath) == "" {
		return errors.New("out is required")
	}
	if err := writeJSONFile(*outPath, head); err != nil {
		return err
	}
	digest, _ := dr.EvidenceDigest(head)
	return writeJSON(stdout, struct {
		Head   dr.AuthorityHead `json:"head"`
		SHA256 string           `json:"sha256"`
	}{head, digest})
}

func privateSignerFromEnvironment(role dr.EvidenceRole) (dr.PrivateSigner, error) {
	prefix := "BONFIRE_DR_" + strings.ToUpper(string(role))
	return dr.ParsePrivateSigner(os.Getenv(prefix+"_PRIVATE_KEY_ID"), role, os.Getenv(prefix+"_PRIVATE_KEY"))
}

func publicVerifierFromEnvironment(role dr.EvidenceRole) (dr.PublicVerifier, error) {
	prefix := "BONFIRE_DR_" + strings.ToUpper(string(role))
	return dr.ParsePublicVerifier(os.Getenv(prefix+"_PUBLIC_KEY_ID"), role, os.Getenv(prefix+"_PUBLIC_KEY"))
}

func secureTrustFromEnvironment() (dr.SecureTrust, error) {
	roles := []dr.EvidenceRole{dr.RoleAuthority, dr.RoleManifest, dr.RoleProvider, dr.RoleCustody, dr.RoleEncryption, dr.RoleRelease}
	verifiers := map[dr.EvidenceRole]dr.PublicVerifier{}
	for _, role := range roles {
		verifier, err := publicVerifierFromEnvironment(role)
		if err != nil {
			return dr.SecureTrust{}, fmt.Errorf("%s verifier: %w", role, err)
		}
		verifiers[role] = verifier
	}
	return dr.SecureTrust{Authority: verifiers[dr.RoleAuthority], Manifest: verifiers[dr.RoleManifest], Provider: verifiers[dr.RoleProvider], Custody: verifiers[dr.RoleCustody], Encryption: verifiers[dr.RoleEncryption], Release: verifiers[dr.RoleRelease]}, nil
}

func verifyConfiguredArtifactSources(sources []dr.ArtifactSource) error {
	configPath := strings.TrimSpace(os.Getenv("BONFIRE_DR_PROTECTED_ROOTS_PATH"))
	raw, err := dr.ReadFileNoLinks(configPath, 64<<10)
	if err != nil {
		return fmt.Errorf("BONFIRE_DR_PROTECTED_ROOTS_PATH: %w", err)
	}
	var configured map[string]string
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configured); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("protected roots JSON has trailing content")
	}
	wantVolumes := map[string]bool{dr.VolumeCanonical: true, dr.VolumeData: true, dr.VolumeQueue: true, dr.VolumeUsage: true}
	if len(configured) != len(wantVolumes) || len(sources) != len(wantVolumes) {
		return errors.New("protected roots must configure exactly the four required volumes")
	}
	seen := map[string]bool{}
	for _, source := range sources {
		if !wantVolumes[source.Volume] || seen[source.Volume] {
			return errors.New("artifact sources contain a missing, duplicate, or unknown volume")
		}
		seen[source.Volume] = true
		expected, ok := configured[source.Volume]
		if !ok {
			return fmt.Errorf("protected root for %s is missing", source.Volume)
		}
		if !filepath.IsAbs(source.Path) || !filepath.IsAbs(expected) || filepath.Clean(source.Path) != filepath.Clean(expected) {
			return fmt.Errorf("artifact source %s is outside its configured protected root", source.Volume)
		}
		if err := dr.ValidateArtifactRoot(expected); err != nil {
			return fmt.Errorf("protected root %s: %w", source.Volume, err)
		}
	}
	return nil
}

func signingKeyFromEnvironment() (dr.SigningKey, error) {
	id := strings.TrimSpace(os.Getenv("BONFIRE_DR_SIGNING_KEY_ID"))
	raw := strings.TrimSpace(os.Getenv("BONFIRE_DR_SIGNING_KEY"))
	var secret []byte
	if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) >= 32 {
		secret = decoded
	} else if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil && len(decoded) >= 32 {
		secret = decoded
	} else if decoded, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(decoded) >= 32 {
		secret = decoded
	} else if len(raw) >= 32 {
		secret = []byte(raw)
	}
	key := dr.SigningKey{ID: id, Secret: secret}
	if err := key.Validate(); err != nil {
		return dr.SigningKey{}, fmt.Errorf("BONFIRE_DR_SIGNING_KEY_ID and BONFIRE_DR_SIGNING_KEY are required: %w", err)
	}
	return key, nil
}

func authorityFlags(set *flag.FlagSet) (*string, *stringList) {
	authorityPath := set.String("authority", "", "path to independently retained purge authority JSONL")
	roots := &stringList{}
	set.Var(roots, "protected-root", "content/DB/queue/usage snapshot root; repeat for every mounted root")
	return authorityPath, roots
}

func runAuthorityAppend(args []string, stdout io.Writer, key dr.SigningKey) error {
	set := flag.NewFlagSet("authority-append", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	authorityPath, roots := authorityFlags(set)
	tenant := set.String("tenant", "", "tenant id")
	highWater := set.Int64("high-water", -1, "purge manifest high-water")
	digest := set.String("purge-digest", "", "body-free purge manifest sha256")
	release := set.String("release", "", "release git commit")
	recordedAt := set.String("recorded-at", "", "RFC3339Nano time (defaults to now)")
	if err := set.Parse(args); err != nil {
		return err
	}
	at := time.Now().UTC()
	if strings.TrimSpace(*recordedAt) != "" {
		var err error
		at, err = time.Parse(time.RFC3339Nano, *recordedAt)
		if err != nil {
			return err
		}
	}
	authority, err := dr.OpenPurgeAuthority(*authorityPath, key, *roots)
	if err != nil {
		return err
	}
	record, appended, err := authority.Append(context.Background(), dr.PurgeState{TenantID: *tenant, HighWater: *highWater, ManifestSHA256: strings.ToLower(*digest)}, *release, at)
	if err != nil {
		return err
	}
	return writeJSON(stdout, struct {
		Appended bool                    `json:"appended"`
		Record   dr.PurgeAuthorityRecord `json:"record"`
	}{appended, record})
}

func runManifestCreate(args []string, stdout io.Writer, key dr.SigningKey) error {
	set := flag.NewFlagSet("manifest-create", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	authorityPath, roots := authorityFlags(set)
	specPath := set.String("spec", "", "backup manifest spec JSON")
	outPath := set.String("out", "", "signed backup manifest JSON")
	if err := set.Parse(args); err != nil {
		return err
	}
	var spec dr.BackupManifestSpec
	if err := readJSONFile(*specPath, &spec); err != nil {
		return err
	}
	authority, err := dr.OpenPurgeAuthority(*authorityPath, key, *roots)
	if err != nil {
		return err
	}
	latest, found, err := authority.Latest(spec.TenantID)
	if err != nil {
		return err
	}
	if !found || latest.State() != spec.Purge || latest.RecordSHA256 != spec.PurgeAuthorityRecordSHA256 {
		return errors.New("manifest spec is not bound to the latest independent purge authority")
	}
	manifest, err := dr.NewBackupManifest(spec, key)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*outPath) != "" {
		if err := writeJSONFile(*outPath, manifest); err != nil {
			return err
		}
	}
	return writeJSON(stdout, manifest)
}

func runPreflight(args []string, stdout io.Writer, key dr.SigningKey) error {
	set := flag.NewFlagSet("preflight", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	authorityPath, roots := authorityFlags(set)
	manifestPath := set.String("manifest", "", "signed backup manifest JSON")
	candidatePath := set.String("candidate", "", "restore candidate evidence JSON")
	receiptPath := set.String("receipt-out", "", "body-free signed drill receipt JSON")
	release := set.String("expected-release", "", "release git commit running after restore")
	evaluatedAt := set.String("evaluated-at", "", "RFC3339Nano time (defaults to now)")
	if err := set.Parse(args); err != nil {
		return err
	}
	var manifest dr.BackupManifest
	if err := readJSONFile(*manifestPath, &manifest); err != nil {
		return err
	}
	var candidate dr.RestoreCandidate
	if err := readJSONFile(*candidatePath, &candidate); err != nil {
		return err
	}
	at := time.Now().UTC()
	if strings.TrimSpace(*evaluatedAt) != "" {
		var err error
		at, err = time.Parse(time.RFC3339Nano, *evaluatedAt)
		if err != nil {
			return err
		}
	}
	authority, err := dr.OpenPurgeAuthority(*authorityPath, key, *roots)
	if err != nil {
		return err
	}
	receipt := dr.PreflightRestore(manifest, candidate, authority, *release, at, key)
	if strings.TrimSpace(*receiptPath) == "" {
		return errors.New("receipt-out is required; every drill must leave durable evidence")
	}
	if err := writeJSONFile(*receiptPath, receipt); err != nil {
		return err
	}
	if err := writeJSON(stdout, receipt); err != nil {
		return err
	}
	if !receipt.Ready {
		return fmt.Errorf("restore readiness refused: %s", strings.Join(receipt.FailureCodes, ","))
	}
	return nil
}

func readJSONFile(path string, output any) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("JSON file path is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON file has trailing content")
	}
	return nil
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeJSONFile(path string, value any) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("output path is required")
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".bonfire-dr-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
