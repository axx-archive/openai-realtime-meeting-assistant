package dr

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const SecureEvidenceFormat = 2

type EvidenceRole string

const (
	RoleAuthority  EvidenceRole = "authority"
	RoleManifest   EvidenceRole = "manifest"
	RoleReceipt    EvidenceRole = "restore_receipt"
	RoleProvider   EvidenceRole = "offsite_provider"
	RoleCustody    EvidenceRole = "custody"
	RoleEncryption EvidenceRole = "encryption"
	RoleRelease    EvidenceRole = "release"
)

type PrivateSigner struct {
	ID      string
	Role    EvidenceRole
	Private ed25519.PrivateKey
}

type PublicVerifier struct {
	ID     string
	Role   EvidenceRole
	Public ed25519.PublicKey
}

func ParsePrivateSigner(id string, role EvidenceRole, encoded string) (PrivateSigner, error) {
	raw, err := decodeKey(encoded)
	if err != nil {
		return PrivateSigner{}, err
	}
	if len(raw) == ed25519.SeedSize {
		raw = ed25519.NewKeyFromSeed(raw)
	}
	signer := PrivateSigner{ID: strings.TrimSpace(id), Role: role, Private: ed25519.PrivateKey(raw)}
	if err := signer.Validate(); err != nil {
		return PrivateSigner{}, err
	}
	return signer, nil
}

func ParsePublicVerifier(id string, role EvidenceRole, encoded string) (PublicVerifier, error) {
	raw, err := decodeKey(encoded)
	if err != nil {
		return PublicVerifier{}, err
	}
	verifier := PublicVerifier{ID: strings.TrimSpace(id), Role: role, Public: ed25519.PublicKey(raw)}
	if err := verifier.Validate(); err != nil {
		return PublicVerifier{}, err
	}
	return verifier, nil
}

func decodeKey(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	for _, decoder := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		if raw, err := decoder.DecodeString(encoded); err == nil {
			return raw, nil
		}
	}
	if raw, err := hex.DecodeString(encoded); err == nil {
		return raw, nil
	}
	return nil, fmt.Errorf("%w: key must be base64 or hex", ErrInvalid)
}

func (signer PrivateSigner) Validate() error {
	if !validToken(signer.ID) || signer.Role == "" || len(signer.Private) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: Ed25519 private signer", ErrInvalid)
	}
	return nil
}

func (verifier PublicVerifier) Validate() error {
	if !validToken(verifier.ID) || verifier.Role == "" || len(verifier.Public) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: Ed25519 public verifier", ErrInvalid)
	}
	return nil
}

func signingMaterial(role EvidenceRole, kind string, payload any) ([]byte, error) {
	if role == "" || !validToken(kind) {
		return nil, ErrInvalid
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return append([]byte("bonfire-dr/v2/"+string(role)+"/"+kind+"\x00"), raw...), nil
}

func signPayload(signer PrivateSigner, kind string, payload any) (string, error) {
	if err := signer.Validate(); err != nil {
		return "", err
	}
	raw, err := signingMaterial(signer.Role, kind, payload)
	if err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(ed25519.Sign(signer.Private, raw)), nil
}

func verifyPayload(verifier PublicVerifier, kind string, payload any, signature string) error {
	if err := verifier.Validate(); err != nil {
		return err
	}
	raw, err := signingMaterial(verifier.Role, kind, payload)
	if err != nil {
		return err
	}
	sig, err := base64.RawStdEncoding.DecodeString(signature)
	if err != nil || !ed25519.Verify(verifier.Public, raw, sig) {
		return ErrAuthorityTampered
	}
	return nil
}

type ArtifactSource struct {
	Volume     string `json:"volume"`
	SnapshotID string `json:"snapshotId"`
	Path       string `json:"path"`
}

// HashArtifactSources derives evidence from opened bytes. It rejects symlinks,
// devices, sockets, and files replaced between lstat and open.
func HashArtifactSources(sources []ArtifactSource) ([]SnapshotArtifact, error) {
	if len(sources) != len(requiredVolumes) {
		return nil, fmt.Errorf("%w: exactly four artifact sources required", ErrInvalid)
	}
	artifacts := make([]SnapshotArtifact, 0, len(sources))
	seenPaths := map[string]bool{}
	for _, source := range sources {
		cleanPath := filepath.Clean(strings.TrimSpace(source.Path))
		if !validToken(source.Volume) || !validToken(source.SnapshotID) || !filepath.IsAbs(cleanPath) || cleanPath != source.Path || seenPaths[cleanPath] {
			return nil, ErrInvalid
		}
		seenPaths[cleanPath] = true
		digest, size, err := hashPathNoLinks(source.Path)
		if err != nil {
			return nil, fmt.Errorf("hash %s: %w", source.Volume, err)
		}
		artifacts = append(artifacts, SnapshotArtifact{Volume: source.Volume, SnapshotID: source.SnapshotID, SHA256: digest, SizeBytes: size})
	}
	if _, err := snapshotSetDigest(artifacts); err != nil {
		return nil, err
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Volume < artifacts[j].Volume })
	return artifacts, nil
}

func hashPathNoLinks(root string) (string, int64, error) {
	file, err := openPathNoFollow(root)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	h := sha256.New()
	total, err := hashOpenedArtifact(file, ".", h, true)
	if err != nil {
		return "", 0, err
	}
	if err := verifyPathStillOpened(root, file); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), total, nil
}

func verifyPathStillOpened(path string, opened *os.File) error {
	current, err := openPathNoFollow(path)
	if err != nil {
		return fmt.Errorf("%w: path changed after traversal", ErrInvalid)
	}
	defer current.Close()
	openedInfo, openedErr := opened.Stat()
	currentInfo, currentErr := current.Stat()
	if openedErr != nil || currentErr != nil || !os.SameFile(openedInfo, currentInfo) {
		return fmt.Errorf("%w: path inode changed after traversal", ErrInvalid)
	}
	return nil
}

func openPathNoFollow(path string) (*os.File, error) {
	abs, err := filepath.Abs(filepath.Clean(strings.TrimSpace(path)))
	if err != nil || abs == string(filepath.Separator) {
		return nil, ErrInvalid
	}
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(abs, string(filepath.Separator)), string(filepath.Separator))
	for index, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC
		if index < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, openErr := unix.Openat(fd, part, flags, 0)
		unix.Close(fd)
		if openErr != nil {
			return nil, fmt.Errorf("%w: no-follow open %s", ErrInvalid, part)
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), abs), nil
}

func hashOpenedArtifact(file *os.File, rel string, h io.Writer, root bool) (int64, error) {
	before, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if before.Mode().IsRegular() {
		if !root {
			fmt.Fprintf(h, "file:%s:%d\x00", filepath.ToSlash(rel), before.Size())
		}
		n, err := io.Copy(h, file)
		after, statErr := file.Stat()
		if err != nil || statErr != nil || n != before.Size() || !os.SameFile(before, after) || after.Size() != before.Size() || after.ModTime() != before.ModTime() || after.Mode() != before.Mode() {
			return 0, fmt.Errorf("%w: artifact changed while opened", ErrInvalid)
		}
		return n, nil
	}
	if !before.IsDir() {
		return 0, fmt.Errorf("%w: non-regular artifact member", ErrInvalid)
	}
	fmt.Fprintf(h, "dir:%s\x00", filepath.ToSlash(rel))
	entries, err := file.ReadDir(-1)
	if err != nil {
		return 0, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	names := make([]string, len(entries))
	var total int64
	for index, entry := range entries {
		names[index] = entry.Name()
		fd, openErr := unix.Openat(int(file.Fd()), entry.Name(), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if openErr != nil {
			return 0, fmt.Errorf("%w: no-follow member %s", ErrInvalid, entry.Name())
		}
		child := os.NewFile(uintptr(fd), entry.Name())
		childRel := entry.Name()
		if rel != "." {
			childRel = filepath.Join(rel, entry.Name())
		}
		n, childErr := hashOpenedArtifact(child, childRel, h, false)
		closeErr := child.Close()
		if childErr != nil {
			return 0, childErr
		}
		if closeErr != nil {
			return 0, closeErr
		}
		total += n
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	afterEntries, err := file.ReadDir(-1)
	if err != nil {
		return 0, err
	}
	sort.Slice(afterEntries, func(i, j int) bool { return afterEntries[i].Name() < afterEntries[j].Name() })
	if len(afterEntries) != len(names) {
		return 0, fmt.Errorf("%w: directory changed while opened", ErrInvalid)
	}
	for index := range names {
		if afterEntries[index].Name() != names[index] {
			return 0, fmt.Errorf("%w: directory changed while opened", ErrInvalid)
		}
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || after.ModTime() != before.ModTime() || after.Mode() != before.Mode() {
		return 0, fmt.Errorf("%w: root inode changed while opened", ErrInvalid)
	}
	return total, nil
}

// HashArtifactPath applies the protected-artifact hashing rules to an
// encrypted object download or running binary.
func HashArtifactPath(path string) (string, int64, error) { return hashPathNoLinks(path) }

// ValidateArtifactRoot verifies that a configured root is a stable regular
// file or directory reached without following a symlink in any component.
func ValidateArtifactRoot(path string) error {
	file, err := openPathNoFollow(path)
	if err != nil {
		return err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || (!before.Mode().IsRegular() && !before.IsDir()) {
		return fmt.Errorf("%w: protected root type", ErrInvalid)
	}
	current, err := openPathNoFollow(path)
	if err != nil {
		return err
	}
	defer current.Close()
	after, err := current.Stat()
	if err != nil || !os.SameFile(before, after) {
		return fmt.Errorf("%w: protected root inode changed", ErrInvalid)
	}
	return nil
}

// ReadFileNoLinks reads a small control-plane file through descriptor-relative,
// O_NOFOLLOW traversal. This rejects a symlink in any path component and
// verifies that the opened inode and size remain stable for the whole read.
func ReadFileNoLinks(path string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, ErrInvalid
	}
	file, err := openPathNoFollow(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() > maxBytes {
		return nil, fmt.Errorf("%w: control file must be a bounded regular file", ErrInvalid)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("%w: control file read", ErrInvalid)
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || after.Size() != before.Size() || after.ModTime() != before.ModTime() || after.Mode() != before.Mode() || int64(len(raw)) != before.Size() {
		return nil, fmt.Errorf("%w: control file changed during read", ErrInvalid)
	}
	if err := verifyPathStillOpened(path, file); err != nil {
		return nil, err
	}
	return raw, nil
}

// ReadPinnedDigestFile reads a digest delivered by an independently mounted
// authority-head adapter. It refuses symlinks and replacement during open.
func ReadPinnedDigestFile(path string) (string, error) {
	raw, err := ReadFileNoLinks(path, 256)
	if err != nil {
		return "", err
	}
	digest := strings.TrimSpace(string(raw))
	if !isSHA256(digest) {
		return "", fmt.Errorf("%w: authority pin digest", ErrInvalid)
	}
	return digest, nil
}

func ValidateIndependentPath(path string, protectedRoots []string) error {
	if strings.TrimSpace(path) == "" || len(protectedRoots) != len(requiredVolumes) {
		return ErrInvalid
	}
	resolved, err := resolvePath(path)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, root := range protectedRoots {
		root, err = resolvePath(root)
		if err != nil || seen[root] {
			return ErrInvalid
		}
		seen[root] = true
		if pathWithin(resolved, root) {
			return fmt.Errorf("%w: path is inside protected restore root", ErrInvalid)
		}
	}
	return nil
}

type CaptureBarrier struct {
	BarrierID        string `json:"barrierId"`
	WriteFenceSHA256 string `json:"writeFenceSha256"`
	PostgresLSN      string `json:"postgresLsn"`
	MeetingHighWater string `json:"meetingHighWater"`
	QueueHighWater   string `json:"queueHighWater"`
	UsageHighWater   string `json:"usageHighWater"`
	StartedAt        string `json:"startedAt"`
	CompletedAt      string `json:"completedAt"`
}

func (barrier CaptureBarrier) Validate() error {
	start, e1 := time.Parse(time.RFC3339Nano, barrier.StartedAt)
	end, e2 := time.Parse(time.RFC3339Nano, barrier.CompletedAt)
	if !validToken(barrier.BarrierID) || !isSHA256(barrier.WriteFenceSHA256) || !validToken(barrier.PostgresLSN) || !validToken(barrier.MeetingHighWater) || !validToken(barrier.QueueHighWater) || !validToken(barrier.UsageHighWater) || e1 != nil || e2 != nil || end.Before(start) {
		return fmt.Errorf("%w: capture barrier", ErrInvalid)
	}
	return nil
}

type ExternalEvidence struct {
	Format        int          `json:"format"`
	Kind          EvidenceRole `json:"kind"`
	ReceiptID     string       `json:"receiptId"`
	Provider      string       `json:"provider"`
	Location      string       `json:"location"`
	SubjectSHA256 string       `json:"subjectSha256"`
	IssuedAt      string       `json:"issuedAt"`
	ExpiresAt     string       `json:"expiresAt"`
	SignerID      string       `json:"signerId"`
	Signature     string       `json:"signature"`
}

type externalEvidencePayload struct {
	Format        int          `json:"format"`
	Kind          EvidenceRole `json:"kind"`
	ReceiptID     string       `json:"receiptId"`
	Provider      string       `json:"provider"`
	Location      string       `json:"location"`
	SubjectSHA256 string       `json:"subjectSha256"`
	IssuedAt      string       `json:"issuedAt"`
	ExpiresAt     string       `json:"expiresAt"`
	SignerID      string       `json:"signerId"`
}

func (e ExternalEvidence) payload() externalEvidencePayload {
	return externalEvidencePayload{e.Format, e.Kind, e.ReceiptID, e.Provider, e.Location, e.SubjectSHA256, e.IssuedAt, e.ExpiresAt, e.SignerID}
}

func SignExternalEvidence(e ExternalEvidence, signer PrivateSigner) (ExternalEvidence, error) {
	if signer.Role != e.Kind || !validToken(e.ReceiptID) || !validToken(e.Provider) || strings.TrimSpace(e.Location) == "" || !isSHA256(e.SubjectSHA256) || !validTimestamp(e.IssuedAt) || !validTimestamp(e.ExpiresAt) {
		return ExternalEvidence{}, ErrInvalid
	}
	e.Format, e.SignerID = SecureEvidenceFormat, signer.ID
	sig, err := signPayload(signer, "external_evidence", e.payload())
	e.Signature = sig
	return e, err
}

func VerifyExternalEvidence(e ExternalEvidence, verifier PublicVerifier, now time.Time) error {
	issued, e1 := time.Parse(time.RFC3339Nano, e.IssuedAt)
	expires, e2 := time.Parse(time.RFC3339Nano, e.ExpiresAt)
	if e.Format != SecureEvidenceFormat || e.Kind != verifier.Role || e.SignerID != verifier.ID || !validToken(e.ReceiptID) || !validToken(e.Provider) || strings.TrimSpace(e.Location) == "" || !isSHA256(e.SubjectSHA256) || e1 != nil || e2 != nil || expires.Before(issued) || now.Before(issued) || !now.Before(expires) {
		return ErrInvalid
	}
	return verifyPayload(verifier, "external_evidence", e.payload(), e.Signature)
}

type ReleaseAttestation struct {
	Format              int    `json:"format"`
	ReleaseCommit       string `json:"releaseCommit"`
	ImageDigest         string `json:"imageDigest"`
	BinarySHA256        string `json:"binarySha256"`
	SourceArchiveSHA256 string `json:"sourceArchiveSha256"`
	IssuedAt            string `json:"issuedAt"`
	ExpiresAt           string `json:"expiresAt"`
	SignerID            string `json:"signerId"`
	Signature           string `json:"signature"`
}
type releasePayload struct {
	Format              int    `json:"format"`
	ReleaseCommit       string `json:"releaseCommit"`
	ImageDigest         string `json:"imageDigest"`
	BinarySHA256        string `json:"binarySha256"`
	SourceArchiveSHA256 string `json:"sourceArchiveSha256"`
	IssuedAt            string `json:"issuedAt"`
	ExpiresAt           string `json:"expiresAt"`
	SignerID            string `json:"signerId"`
}

func (a ReleaseAttestation) payload() releasePayload {
	return releasePayload{a.Format, a.ReleaseCommit, a.ImageDigest, a.BinarySHA256, a.SourceArchiveSHA256, a.IssuedAt, a.ExpiresAt, a.SignerID}
}
func SignReleaseAttestation(a ReleaseAttestation, signer PrivateSigner) (ReleaseAttestation, error) {
	issued, issueErr := time.Parse(time.RFC3339Nano, a.IssuedAt)
	expires, expiryErr := time.Parse(time.RFC3339Nano, a.ExpiresAt)
	if signer.Role != RoleRelease || !validReleaseCommit(a.ReleaseCommit) || !isSHA256(strings.TrimPrefix(a.ImageDigest, "sha256:")) || !isSHA256(a.BinarySHA256) || !isSHA256(a.SourceArchiveSHA256) || issueErr != nil || expiryErr != nil || !expires.After(issued) || expires.Sub(issued) > 30*24*time.Hour {
		return ReleaseAttestation{}, ErrInvalid
	}
	a.Format, a.SignerID = SecureEvidenceFormat, signer.ID
	sig, err := signPayload(signer, "release_attestation", a.payload())
	a.Signature = sig
	return a, err
}
func VerifyReleaseAttestation(a ReleaseAttestation, v PublicVerifier, now time.Time) error {
	issued, issueErr := time.Parse(time.RFC3339Nano, a.IssuedAt)
	expires, expiryErr := time.Parse(time.RFC3339Nano, a.ExpiresAt)
	if a.Format != SecureEvidenceFormat || v.Role != RoleRelease || a.SignerID != v.ID || !validReleaseCommit(a.ReleaseCommit) || !isSHA256(strings.TrimPrefix(a.ImageDigest, "sha256:")) || !isSHA256(a.BinarySHA256) || !isSHA256(a.SourceArchiveSHA256) || issueErr != nil || expiryErr != nil || !expires.After(issued) || expires.Sub(issued) > 30*24*time.Hour || now.Before(issued) || !now.Before(expires) {
		return ErrInvalid
	}
	return verifyPayload(v, "release_attestation", a.payload(), a.Signature)
}

type AuthorityHead struct {
	Format             int          `json:"format"`
	Sequence           uint64       `json:"sequence"`
	RecordSHA256       string       `json:"recordSha256"`
	PreviousHeadSHA256 string       `json:"previousHeadSha256,omitempty"`
	RecordedAt         string       `json:"recordedAt"`
	Tenants            []PurgeState `json:"tenants"`
	SignerID           string       `json:"signerId"`
	Signature          string       `json:"signature"`
}
type authorityHeadPayload struct {
	Format             int          `json:"format"`
	Sequence           uint64       `json:"sequence"`
	RecordSHA256       string       `json:"recordSha256"`
	PreviousHeadSHA256 string       `json:"previousHeadSha256,omitempty"`
	RecordedAt         string       `json:"recordedAt"`
	Tenants            []PurgeState `json:"tenants"`
	SignerID           string       `json:"signerId"`
}

func (h AuthorityHead) payload() authorityHeadPayload {
	return authorityHeadPayload{h.Format, h.Sequence, h.RecordSHA256, h.PreviousHeadSHA256, h.RecordedAt, append([]PurgeState(nil), h.Tenants...), h.SignerID}
}
func SignAuthorityHead(h AuthorityHead, s PrivateSigner) (AuthorityHead, error) {
	sort.Slice(h.Tenants, func(i, j int) bool { return h.Tenants[i].TenantID < h.Tenants[j].TenantID })
	if s.Role != RoleAuthority || h.Sequence == 0 || !isSHA256(h.RecordSHA256) || (h.PreviousHeadSHA256 != "" && !isSHA256(h.PreviousHeadSHA256)) || !validTimestamp(h.RecordedAt) || validateTenantStates(h.Tenants) != nil {
		return AuthorityHead{}, ErrInvalid
	}
	h.Format, h.SignerID = SecureEvidenceFormat, s.ID
	sig, err := signPayload(s, "authority_head", h.payload())
	h.Signature = sig
	return h, err
}
func VerifyAuthorityHead(h AuthorityHead, v PublicVerifier, pinnedSHA string) error {
	if h.Format != SecureEvidenceFormat || v.Role != RoleAuthority || h.SignerID != v.ID || h.Sequence == 0 || !isSHA256(h.RecordSHA256) || (h.PreviousHeadSHA256 != "" && !isSHA256(h.PreviousHeadSHA256)) || !validTimestamp(h.RecordedAt) || !isSHA256(pinnedSHA) || validateTenantStates(h.Tenants) != nil {
		return ErrInvalid
	}
	if err := verifyPayload(v, "authority_head", h.payload(), h.Signature); err != nil {
		return err
	}
	digest, err := EvidenceDigest(h)
	if err != nil || digest != pinnedSHA {
		return ErrAuthorityRollback
	}
	return nil
}

func EvidenceDigest(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func validateTenantStates(states []PurgeState) error {
	if len(states) == 0 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, state := range states {
		if state.Validate() != nil || seen[state.TenantID] {
			return ErrInvalid
		}
		seen[state.TenantID] = true
	}
	return nil
}

type SecureManifest struct {
	Format              int                `json:"format"`
	CreatedAt           string             `json:"createdAt"`
	Release             ReleaseAttestation `json:"release"`
	Barrier             CaptureBarrier     `json:"barrier"`
	Artifacts           []SnapshotArtifact `json:"artifacts"`
	SnapshotSetSHA256   string             `json:"snapshotSetSha256"`
	Tenants             []PurgeState       `json:"tenants"`
	DatabaseSHA256      string             `json:"databaseSha256"`
	AuthorityHeadSHA256 string             `json:"authorityHeadSha256"`
	AuthorityHead       AuthorityHead      `json:"authorityHead"`
	Provider            ExternalEvidence   `json:"provider"`
	Custody             ExternalEvidence   `json:"custody"`
	Encryption          ExternalEvidence   `json:"encryption"`
	ManifestSHA256      string             `json:"manifestSha256"`
	SignerID            string             `json:"signerId"`
	Signature           string             `json:"signature"`
}
type secureManifestPayload struct {
	Format              int                `json:"format"`
	CreatedAt           string             `json:"createdAt"`
	Release             ReleaseAttestation `json:"release"`
	Barrier             CaptureBarrier     `json:"barrier"`
	Artifacts           []SnapshotArtifact `json:"artifacts"`
	SnapshotSetSHA256   string             `json:"snapshotSetSha256"`
	Tenants             []PurgeState       `json:"tenants"`
	DatabaseSHA256      string             `json:"databaseSha256"`
	AuthorityHeadSHA256 string             `json:"authorityHeadSha256"`
	AuthorityHead       AuthorityHead      `json:"authorityHead"`
	Provider            ExternalEvidence   `json:"provider"`
	Custody             ExternalEvidence   `json:"custody"`
	Encryption          ExternalEvidence   `json:"encryption"`
	SignerID            string             `json:"signerId"`
}

func (m SecureManifest) payload() secureManifestPayload {
	return secureManifestPayload{m.Format, m.CreatedAt, m.Release, m.Barrier, append([]SnapshotArtifact(nil), m.Artifacts...), m.SnapshotSetSHA256, append([]PurgeState(nil), m.Tenants...), m.DatabaseSHA256, m.AuthorityHeadSHA256, m.AuthorityHead, m.Provider, m.Custody, m.Encryption, m.SignerID}
}

type SecureTrust struct{ Authority, Manifest, Provider, Custody, Encryption, Release PublicVerifier }

func NewSecureManifest(m SecureManifest, signer PrivateSigner, trust SecureTrust, now time.Time) (SecureManifest, error) {
	if signer.Role != RoleManifest {
		return SecureManifest{}, ErrInvalid
	}
	m.Format, m.SignerID = SecureEvidenceFormat, signer.ID
	sort.Slice(m.Artifacts, func(i, j int) bool { return m.Artifacts[i].Volume < m.Artifacts[j].Volume })
	sort.Slice(m.Tenants, func(i, j int) bool { return m.Tenants[i].TenantID < m.Tenants[j].TenantID })
	set, err := snapshotSetDigest(m.Artifacts)
	if err != nil {
		return SecureManifest{}, err
	}
	m.SnapshotSetSHA256 = set
	if err := validateSecureManifest(m, trust, now); err != nil {
		return SecureManifest{}, err
	}
	raw, _ := json.Marshal(m.payload())
	sum := sha256.Sum256(raw)
	m.ManifestSHA256 = hex.EncodeToString(sum[:])
	m.Signature, err = signPayload(signer, "manifest", m.payload())
	return m, err
}
func validateSecureManifest(m SecureManifest, t SecureTrust, now time.Time) error {
	if m.Format != SecureEvidenceFormat || !validTimestamp(m.CreatedAt) || m.Barrier.Validate() != nil || !isSHA256(m.DatabaseSHA256) || !isSHA256(m.AuthorityHeadSHA256) || len(m.Tenants) == 0 {
		return ErrInvalid
	}
	set, err := snapshotSetDigest(m.Artifacts)
	if err != nil || set != m.SnapshotSetSHA256 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, p := range m.Tenants {
		if p.Validate() != nil || seen[p.TenantID] {
			return ErrInvalid
		}
		seen[p.TenantID] = true
	}
	if err := VerifyReleaseAttestation(m.Release, t.Release, now); err != nil {
		return err
	}
	for e, v := range map[ExternalEvidence]PublicVerifier{m.Provider: t.Provider, m.Custody: t.Custody, m.Encryption: t.Encryption} {
		if err := VerifyExternalEvidence(e, v, now); err != nil {
			return err
		}
	}
	if m.Provider.SubjectSHA256 != m.Encryption.SubjectSHA256 || m.Custody.SubjectSHA256 != set {
		return ErrInvalid
	}
	if err := VerifyAuthorityHead(m.AuthorityHead, t.Authority, m.AuthorityHeadSHA256); err != nil || !equalJSON(m.Tenants, m.AuthorityHead.Tenants) {
		return ErrInvalid
	}
	if err := distinctPublicVerifiers(t.Authority, t.Manifest, t.Provider, t.Custody, t.Encryption, t.Release); err != nil {
		return err
	}
	ids := []string{m.Provider.SignerID, m.Custody.SignerID, m.Encryption.SignerID, m.Release.SignerID, m.SignerID}
	sort.Strings(ids)
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			return fmt.Errorf("%w: evidence roles must use distinct keys", ErrInvalid)
		}
	}
	return nil
}

func distinctPublicVerifiers(verifiers ...PublicVerifier) error {
	seen := map[string]EvidenceRole{}
	for _, verifier := range verifiers {
		if err := verifier.Validate(); err != nil {
			return err
		}
		fingerprint := hex.EncodeToString(verifier.Public)
		if prior, ok := seen[fingerprint]; ok {
			return fmt.Errorf("%w: %s and %s reuse one public key", ErrInvalid, prior, verifier.Role)
		}
		seen[fingerprint] = verifier.Role
	}
	return nil
}
func VerifySecureManifest(m SecureManifest, t SecureTrust, now time.Time) error {
	if t.Manifest.Role != RoleManifest || m.SignerID != t.Manifest.ID {
		return ErrInvalid
	}
	if err := validateSecureManifest(m, t, now); err != nil {
		return err
	}
	raw, _ := json.Marshal(m.payload())
	sum := sha256.Sum256(raw)
	want := hex.EncodeToString(sum[:])
	if m.ManifestSHA256 != want {
		return ErrAuthorityTampered
	}
	return verifyPayload(t.Manifest, "manifest", m.payload(), m.Signature)
}

type SecureRestoreInput struct {
	EnvironmentID             string
	Nonce                     string
	EvaluatedAt               time.Time
	ExpiresAt                 time.Time
	Sources                   []ArtifactSource
	DownloadedEnvelopePath    string
	Tenants                   []PurgeState
	AuthorityTenantPrefixes   []PurgeState
	DatabaseSHA256            string
	RunningReleaseCommit      string
	RunningImageDigest        string
	RunningBinaryPath         string
	PinnedAuthorityHeadSHA256 string
	AuthorityHead             AuthorityHead
}
type SecureDrillReceipt struct {
	Format              int      `json:"format"`
	EnvironmentID       string   `json:"environmentId"`
	NonceSHA256         string   `json:"nonceSha256"`
	EvaluatedAt         string   `json:"evaluatedAt"`
	ExpiresAt           string   `json:"expiresAt"`
	ManifestSHA256      string   `json:"manifestSha256"`
	CandidateSHA256     string   `json:"candidateSha256"`
	AuthorityHeadSHA256 string   `json:"authorityHeadSha256"`
	DatabaseSHA256      string   `json:"databaseSha256"`
	ReleaseCommit       string   `json:"releaseCommit"`
	ImageDigest         string   `json:"imageDigest"`
	Ready               bool     `json:"ready"`
	FailureCodes        []string `json:"failureCodes"`
	ReceiptSHA256       string   `json:"receiptSha256"`
	SignerID            string   `json:"signerId"`
	Signature           string   `json:"signature"`
}
type secureReceiptPayload struct {
	Format              int      `json:"format"`
	EnvironmentID       string   `json:"environmentId"`
	NonceSHA256         string   `json:"nonceSha256"`
	EvaluatedAt         string   `json:"evaluatedAt"`
	ExpiresAt           string   `json:"expiresAt"`
	ManifestSHA256      string   `json:"manifestSha256"`
	CandidateSHA256     string   `json:"candidateSha256"`
	AuthorityHeadSHA256 string   `json:"authorityHeadSha256"`
	DatabaseSHA256      string   `json:"databaseSha256"`
	ReleaseCommit       string   `json:"releaseCommit"`
	ImageDigest         string   `json:"imageDigest"`
	Ready               bool     `json:"ready"`
	FailureCodes        []string `json:"failureCodes"`
	SignerID            string   `json:"signerId"`
}

func (r SecureDrillReceipt) payload() secureReceiptPayload {
	return secureReceiptPayload{r.Format, r.EnvironmentID, r.NonceSHA256, r.EvaluatedAt, r.ExpiresAt, r.ManifestSHA256, r.CandidateSHA256, r.AuthorityHeadSHA256, r.DatabaseSHA256, r.ReleaseCommit, r.ImageDigest, r.Ready, append([]string(nil), r.FailureCodes...), r.SignerID}
}

func SecurePreflight(m SecureManifest, in SecureRestoreInput, trust SecureTrust, receiptSigner PrivateSigner) SecureDrillReceipt {
	r := SecureDrillReceipt{Format: SecureEvidenceFormat, EnvironmentID: in.EnvironmentID, EvaluatedAt: in.EvaluatedAt.UTC().Format(time.RFC3339Nano), ExpiresAt: in.ExpiresAt.UTC().Format(time.RFC3339Nano), ManifestSHA256: m.ManifestSHA256, AuthorityHeadSHA256: in.PinnedAuthorityHeadSHA256, DatabaseSHA256: in.DatabaseSHA256, ReleaseCommit: strings.ToLower(in.RunningReleaseCommit), ImageDigest: in.RunningImageDigest, SignerID: receiptSigner.ID, FailureCodes: []string{}}
	nonce := sha256.Sum256([]byte(in.Nonce))
	r.NonceSHA256 = hex.EncodeToString(nonce[:])
	fail := map[string]bool{}
	if receiptSigner.Validate() != nil || receiptSigner.Role != RoleReceipt {
		fail["receipt_signer_invalid"] = true
	} else {
		receiptPublic := PublicVerifier{ID: receiptSigner.ID, Role: receiptSigner.Role, Public: receiptSigner.Private.Public().(ed25519.PublicKey)}
		if distinctPublicVerifiers(trust.Authority, trust.Manifest, trust.Provider, trust.Custody, trust.Encryption, trust.Release, receiptPublic) != nil {
			fail["receipt_signer_invalid"] = true
		}
	}
	if !validToken(in.EnvironmentID) || len(in.Nonce) < 32 || !in.ExpiresAt.After(in.EvaluatedAt) || in.ExpiresAt.Sub(in.EvaluatedAt) > time.Hour {
		fail["receipt_context_invalid"] = true
	}
	if err := VerifySecureManifest(m, trust, in.EvaluatedAt); err != nil {
		fail["manifest_invalid"] = true
	}
	if err := VerifyAuthorityHead(in.AuthorityHead, trust.Authority, in.PinnedAuthorityHeadSHA256); err != nil || in.PinnedAuthorityHeadSHA256 != m.AuthorityHeadSHA256 || !equalJSON(in.AuthorityHead, m.AuthorityHead) {
		fail["authority_head_invalid"] = true
	}
	artifacts, err := HashArtifactSources(in.Sources)
	if err != nil || !equalJSON(artifacts, m.Artifacts) {
		fail["artifact_bytes_mismatch"] = true
	}
	envelopeDigest, _, envelopeErr := hashPathNoLinks(in.DownloadedEnvelopePath)
	if envelopeErr != nil || envelopeDigest != m.Provider.SubjectSHA256 || envelopeDigest != m.Encryption.SubjectSHA256 {
		fail["offsite_object_mismatch"] = true
	}
	tenantCopy := append([]PurgeState(nil), in.Tenants...)
	sort.Slice(tenantCopy, func(i, j int) bool { return tenantCopy[i].TenantID < tenantCopy[j].TenantID })
	prefixCopy := append([]PurgeState(nil), in.AuthorityTenantPrefixes...)
	sort.Slice(prefixCopy, func(i, j int) bool { return prefixCopy[i].TenantID < prefixCopy[j].TenantID })
	if !tenantStatesAtOrAbove(tenantCopy, prefixCopy, in.AuthorityHead.Tenants) {
		fail["tenant_coverage_mismatch"] = true
	}
	if !isSHA256(in.DatabaseSHA256) {
		fail["database_digest_invalid"] = true
	} else if in.DatabaseSHA256 != m.DatabaseSHA256 {
		fail["database_digest_mismatch"] = true
	}
	binaryDigest, _, berr := hashPathNoLinks(in.RunningBinaryPath)
	if berr != nil || binaryDigest != m.Release.BinarySHA256 || !strings.EqualFold(in.RunningReleaseCommit, m.Release.ReleaseCommit) || in.RunningImageDigest != m.Release.ImageDigest {
		fail["running_release_mismatch"] = true
	}
	candidate := struct {
		EnvironmentID, NonceSHA256                                    string
		Artifacts                                                     []SnapshotArtifact
		Tenants                                                       []PurgeState
		AuthorityTenantPrefixes                                       []PurgeState
		ReleaseCommit, ImageDigest, BinarySHA256, AuthorityHeadSHA256 string
		DatabaseSHA256                                                string
		EnvelopeSHA256                                                string
	}{in.EnvironmentID, r.NonceSHA256, artifacts, tenantCopy, prefixCopy, strings.ToLower(in.RunningReleaseCommit), in.RunningImageDigest, binaryDigest, in.PinnedAuthorityHeadSHA256, in.DatabaseSHA256, envelopeDigest}
	r.CandidateSHA256, _ = EvidenceDigest(candidate)
	for code := range fail {
		r.FailureCodes = append(r.FailureCodes, code)
	}
	sort.Strings(r.FailureCodes)
	r.Ready = len(r.FailureCodes) == 0 && receiptSigner.Role == RoleReceipt
	raw, _ := json.Marshal(r.payload())
	sum := sha256.Sum256(raw)
	r.ReceiptSHA256 = hex.EncodeToString(sum[:])
	r.Signature, _ = signPayload(receiptSigner, "restore_receipt", r.payload())
	return r
}

// VerifySecureCandidate reopens every mutable input and derives the candidate
// digest without a signing key. Restore-mode startup calls it again so a file,
// database, image, authority head, or binary changed after preflight cannot
// reuse the prior ready receipt.
func VerifySecureCandidate(m SecureManifest, in SecureRestoreInput, trust SecureTrust) (string, []string) {
	fail := map[string]bool{}
	if err := VerifySecureManifest(m, trust, in.EvaluatedAt); err != nil {
		fail["manifest_invalid"] = true
	}
	if err := VerifyAuthorityHead(in.AuthorityHead, trust.Authority, in.PinnedAuthorityHeadSHA256); err != nil || in.PinnedAuthorityHeadSHA256 != m.AuthorityHeadSHA256 || !equalJSON(in.AuthorityHead, m.AuthorityHead) {
		fail["authority_head_invalid"] = true
	}
	artifacts, err := HashArtifactSources(in.Sources)
	if err != nil || !equalJSON(artifacts, m.Artifacts) {
		fail["artifact_bytes_mismatch"] = true
	}
	envelopeDigest, _, envelopeErr := hashPathNoLinks(in.DownloadedEnvelopePath)
	if envelopeErr != nil || envelopeDigest != m.Provider.SubjectSHA256 || envelopeDigest != m.Encryption.SubjectSHA256 {
		fail["offsite_object_mismatch"] = true
	}
	tenants := append([]PurgeState(nil), in.Tenants...)
	prefixes := append([]PurgeState(nil), in.AuthorityTenantPrefixes...)
	sort.Slice(tenants, func(i, j int) bool { return tenants[i].TenantID < tenants[j].TenantID })
	sort.Slice(prefixes, func(i, j int) bool { return prefixes[i].TenantID < prefixes[j].TenantID })
	if !tenantStatesAtOrAbove(tenants, prefixes, in.AuthorityHead.Tenants) {
		fail["tenant_coverage_mismatch"] = true
	}
	if !isSHA256(in.DatabaseSHA256) {
		fail["database_digest_invalid"] = true
	} else if in.DatabaseSHA256 != m.DatabaseSHA256 {
		fail["database_digest_mismatch"] = true
	}
	binaryDigest, _, binaryErr := hashPathNoLinks(in.RunningBinaryPath)
	if binaryErr != nil || binaryDigest != m.Release.BinarySHA256 || !strings.EqualFold(in.RunningReleaseCommit, m.Release.ReleaseCommit) || in.RunningImageDigest != m.Release.ImageDigest {
		fail["running_release_mismatch"] = true
	}
	nonce := sha256.Sum256([]byte(in.Nonce))
	candidate := struct {
		EnvironmentID, NonceSHA256                                    string
		Artifacts                                                     []SnapshotArtifact
		Tenants, AuthorityTenantPrefixes                              []PurgeState
		ReleaseCommit, ImageDigest, BinarySHA256, AuthorityHeadSHA256 string
		DatabaseSHA256                                                string
		EnvelopeSHA256                                                string
	}{in.EnvironmentID, hex.EncodeToString(nonce[:]), artifacts, tenants, prefixes, strings.ToLower(in.RunningReleaseCommit), in.RunningImageDigest, binaryDigest, in.PinnedAuthorityHeadSHA256, in.DatabaseSHA256, envelopeDigest}
	digest, _ := EvidenceDigest(candidate)
	failures := make([]string, 0, len(fail))
	for code := range fail {
		failures = append(failures, code)
	}
	sort.Strings(failures)
	return digest, failures
}

func tenantStatesAtOrAbove(current, prefixes, authority []PurgeState) bool {
	return len(current) == len(authority) && tenantStatesExtendAuthority(current, prefixes, authority)
}

// tenantStatesExtendAuthority applies the issuance monotonicity rule: every
// tenant in the preceding signed head remains present, its current ledger is
// at or above the old high-water, and its exact digest at that boundary is
// unchanged. Current state may additionally contain newly discovered tenants.
func tenantStatesExtendAuthority(current, prefixes, authority []PurgeState) bool {
	if len(current) < len(authority) || len(prefixes) != len(authority) {
		return false
	}
	currentByTenant := map[string]PurgeState{}
	prefixByTenant := map[string]PurgeState{}
	for _, state := range current {
		if _, exists := currentByTenant[state.TenantID]; exists || state.Validate() != nil {
			return false
		}
		currentByTenant[state.TenantID] = state
	}
	for _, state := range prefixes {
		if _, exists := prefixByTenant[state.TenantID]; exists || state.Validate() != nil {
			return false
		}
		prefixByTenant[state.TenantID] = state
	}
	seenAuthority := map[string]bool{}
	for _, head := range authority {
		if seenAuthority[head.TenantID] || head.Validate() != nil {
			return false
		}
		seenAuthority[head.TenantID] = true
		currentState, currentOK := currentByTenant[head.TenantID]
		prefixState, prefixOK := prefixByTenant[head.TenantID]
		if !currentOK || !prefixOK || currentState.HighWater < head.HighWater || prefixState.HighWater != head.HighWater || prefixState.ManifestSHA256 != head.ManifestSHA256 {
			return false
		}
	}
	return true
}

func VerifySecureReceipt(r SecureDrillReceipt, v PublicVerifier, environmentID, nonce, candidateSHA, databaseSHA, imageDigest, authorityHead string, now time.Time) error {
	if v.Role != RoleReceipt || r.Format != SecureEvidenceFormat || r.SignerID != v.ID || !r.Ready || len(r.FailureCodes) != 0 || r.EnvironmentID != environmentID || r.CandidateSHA256 != candidateSHA || r.DatabaseSHA256 != databaseSHA || !isSHA256(databaseSHA) || r.ImageDigest != imageDigest || r.AuthorityHeadSHA256 != authorityHead {
		return ErrInvalid
	}
	sum := sha256.Sum256([]byte(nonce))
	if r.NonceSHA256 != hex.EncodeToString(sum[:]) {
		return ErrInvalid
	}
	issued, e1 := time.Parse(time.RFC3339Nano, r.EvaluatedAt)
	expires, e2 := time.Parse(time.RFC3339Nano, r.ExpiresAt)
	if e1 != nil || e2 != nil || !expires.After(issued) || expires.Sub(issued) > time.Hour || now.Before(issued) || !now.Before(expires) {
		return ErrInvalid
	}
	raw, _ := json.Marshal(r.payload())
	digest := sha256.Sum256(raw)
	if r.ReceiptSHA256 != hex.EncodeToString(digest[:]) {
		return ErrAuthorityTampered
	}
	return verifyPayload(v, "restore_receipt", r.payload(), r.Signature)
}

func ConsumeSecureReceipt(markerPath string, r SecureDrillReceipt) error {
	markerPath = filepath.Clean(strings.TrimSpace(markerPath))
	if !filepath.IsAbs(markerPath) || filepath.Base(markerPath) == "." || filepath.Base(markerPath) == string(filepath.Separator) {
		return ErrInvalid
	}
	directory, err := openPathNoFollow(filepath.Dir(markerPath))
	if err != nil {
		return err
	}
	defer directory.Close()
	info, err := directory.Stat()
	if err != nil || !info.IsDir() {
		return ErrInvalid
	}
	fd, err := unix.Openat(int(directory.Fd()), filepath.Base(markerPath), unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("%w: restore receipt already consumed", ErrInvalid)
	}
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), markerPath)
	defer f.Close()
	_, err = fmt.Fprintln(f, r.ReceiptSHA256)
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = directory.Sync()
	}
	return err
}

func equalJSON(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}
