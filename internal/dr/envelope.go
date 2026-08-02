package dr

// Authenticated DR envelope helpers. The production backup worker's BFBKUP01
// format intentionally encrypts a single opaque blob. The secure four-root
// recovery path needs an independently verifiable envelope with no plaintext
// capture metadata, so BFBKUP02 puts both its metadata and payload inside the
// AES-GCM ciphertext.

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// EnvelopeMagic identifies the versioned, metadata-bearing encrypted
	// envelope. It is GCM AAD and is never accepted as interchangeable with a
	// legacy backup object.
	EnvelopeMagic     = "BFBKUP02"
	legacyBackupMagic = "BFBKUP01"

	envelopeMetadataVersion = 1
	maxEnvelopeFileBytes    = int64(2 << 30) // bounded because AES-GCM is one-shot here.
	maxEnvelopeMetadata     = 64 << 10
)

// EnvelopeMetadata is encrypted and authenticated with the payload. It must
// never contain a source path, transcript, artifact name, or other sensitive
// capture content. PlaintextSHA256 and PlaintextBytes are recomputed on open.
type EnvelopeMetadata struct {
	Format          string `json:"format"`
	Version         int    `json:"version"`
	Purpose         string `json:"purpose"`
	PlaintextSHA256 string `json:"plaintextSha256"`
	PlaintextBytes  int64  `json:"plaintextBytes"`
	SealedAt        string `json:"sealedAt"`
}

// EnvelopeFileResult is safe to write to an operator log: it contains only
// envelope digest/size and authenticated metadata, never a key or payload.
type EnvelopeFileResult struct {
	Metadata       EnvelopeMetadata
	EnvelopeSHA256 string
	EnvelopeBytes  int64
}

// ParseEnvelopeKey accepts exactly 32 bytes directly, hex, or standard/raw
// base64. It deliberately does not derive a key from a passphrase.
func ParseEnvelopeKey(raw []byte) ([]byte, error) {
	if len(raw) == 32 {
		return append([]byte(nil), raw...), nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, errors.New("AES-256-GCM envelope key is required")
	}
	for _, decode := range []func(string) ([]byte, error){
		hex.DecodeString,
		func(value string) ([]byte, error) { return decodeBase64(value, false) },
		func(value string) ([]byte, error) { return decodeBase64(value, true) },
	} {
		decoded, err := decode(trimmed)
		if err == nil && len(decoded) == 32 {
			return decoded, nil
		}
	}
	return nil, errors.New("AES-256-GCM envelope key must decode to exactly 32 bytes")
}

func decodeBase64(value string, raw bool) ([]byte, error) {
	if raw {
		return base64.RawStdEncoding.DecodeString(value)
	}
	return base64.StdEncoding.DecodeString(value)
}

// SealEnvelope encrypts plaintext into BFBKUP02. The metadata's integrity
// fields are assigned from plaintext rather than trusting caller input.
func SealEnvelope(key, plaintext []byte, purpose string, now time.Time) ([]byte, EnvelopeMetadata, error) {
	if err := validateEnvelopeKey(key); err != nil {
		return nil, EnvelopeMetadata{}, err
	}
	purpose = strings.TrimSpace(purpose)
	if purpose == "" || len(purpose) > 128 || strings.ContainsAny(purpose, "\r\n\x00") {
		return nil, EnvelopeMetadata{}, errors.New("envelope purpose must be a non-empty single-line value of at most 128 bytes")
	}
	sum := sha256.Sum256(plaintext)
	metadata := EnvelopeMetadata{
		Format:          EnvelopeMagic,
		Version:         envelopeMetadataVersion,
		Purpose:         purpose,
		PlaintextSHA256: hex.EncodeToString(sum[:]),
		PlaintextBytes:  int64(len(plaintext)),
		SealedAt:        now.UTC().Format(time.RFC3339Nano),
	}
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return nil, EnvelopeMetadata{}, err
	}
	if len(metadataBytes) > maxEnvelopeMetadata {
		return nil, EnvelopeMetadata{}, errors.New("envelope metadata exceeds bound")
	}
	inner := make([]byte, 4+len(metadataBytes)+len(plaintext))
	binary.BigEndian.PutUint32(inner[:4], uint32(len(metadataBytes)))
	copy(inner[4:], metadataBytes)
	copy(inner[4+len(metadataBytes):], plaintext)
	blob, err := sealEnvelopeBytes(key, []byte(EnvelopeMagic), inner)
	if err != nil {
		return nil, EnvelopeMetadata{}, err
	}
	return blob, metadata, nil
}

// OpenEnvelope authenticates, parses, and verifies a BFBKUP02 envelope. It
// also reads the old BFBKUP01 backup format so recovery tooling can inspect a
// legacy local snapshot, but that format returns synthetic metadata because it
// did not store capture metadata at seal time.
func OpenEnvelope(key, blob []byte) ([]byte, EnvelopeMetadata, error) {
	if err := validateEnvelopeKey(key); err != nil {
		return nil, EnvelopeMetadata{}, err
	}
	if len(blob) < len(EnvelopeMagic) {
		return nil, EnvelopeMetadata{}, errors.New("encrypted envelope is truncated")
	}
	magic := string(blob[:len(EnvelopeMagic)])
	switch magic {
	case EnvelopeMagic:
		inner, err := openEnvelopeBytes(key, []byte(EnvelopeMagic), blob)
		if err != nil {
			return nil, EnvelopeMetadata{}, fmt.Errorf("authenticate encrypted envelope: %w", err)
		}
		return parseEnvelopeInner(inner)
	case legacyBackupMagic:
		plaintext, err := openEnvelopeBytes(key, []byte(legacyBackupMagic), blob)
		if err != nil {
			return nil, EnvelopeMetadata{}, fmt.Errorf("authenticate legacy backup envelope: %w", err)
		}
		sum := sha256.Sum256(plaintext)
		return plaintext, EnvelopeMetadata{
			Format:          legacyBackupMagic,
			Version:         0,
			Purpose:         "legacy-backup-blob",
			PlaintextSHA256: hex.EncodeToString(sum[:]),
			PlaintextBytes:  int64(len(plaintext)),
		}, nil
	default:
		return nil, EnvelopeMetadata{}, errors.New("encrypted envelope has unknown magic")
	}
}

// SealEnvelopeFile creates a 0600 atomically-renamed BFBKUP02 output. Input
// is opened without following symlinks and verified stable over the read.
func SealEnvelopeFile(key []byte, inputPath, outputPath, purpose string, now time.Time) (EnvelopeFileResult, error) {
	if err := requireDistinctEnvelopePaths(inputPath, outputPath); err != nil {
		return EnvelopeFileResult{}, err
	}
	plaintext, err := readStableEnvelopeFile(inputPath)
	if err != nil {
		return EnvelopeFileResult{}, fmt.Errorf("read envelope input: %w", err)
	}
	blob, metadata, err := SealEnvelope(key, plaintext, purpose, now)
	if err != nil {
		return EnvelopeFileResult{}, err
	}
	if err := WriteFileAtomic0600(outputPath, blob); err != nil {
		return EnvelopeFileResult{}, fmt.Errorf("write encrypted envelope: %w", err)
	}
	sum := sha256.Sum256(blob)
	return EnvelopeFileResult{Metadata: metadata, EnvelopeSHA256: hex.EncodeToString(sum[:]), EnvelopeBytes: int64(len(blob))}, nil
}

// OpenEnvelopeFile authenticates an envelope and atomically writes only the
// verified plaintext with mode 0600.
func OpenEnvelopeFile(key []byte, inputPath, outputPath string) (EnvelopeFileResult, error) {
	if err := requireDistinctEnvelopePaths(inputPath, outputPath); err != nil {
		return EnvelopeFileResult{}, err
	}
	blob, err := readStableEnvelopeFile(inputPath)
	if err != nil {
		return EnvelopeFileResult{}, fmt.Errorf("read encrypted envelope: %w", err)
	}
	plaintext, metadata, err := OpenEnvelope(key, blob)
	if err != nil {
		return EnvelopeFileResult{}, err
	}
	if err := WriteFileAtomic0600(outputPath, plaintext); err != nil {
		return EnvelopeFileResult{}, fmt.Errorf("write verified envelope plaintext: %w", err)
	}
	sum := sha256.Sum256(blob)
	return EnvelopeFileResult{Metadata: metadata, EnvelopeSHA256: hex.EncodeToString(sum[:]), EnvelopeBytes: int64(len(blob))}, nil
}

func requireDistinctEnvelopePaths(inputPath, outputPath string) error {
	inputAbs, err := filepath.Abs(filepath.Clean(inputPath))
	if err != nil {
		return fmt.Errorf("resolve envelope input path: %w", err)
	}
	outputAbs, err := filepath.Abs(filepath.Clean(outputPath))
	if err != nil {
		return fmt.Errorf("resolve envelope output path: %w", err)
	}
	if inputAbs == outputAbs {
		return errors.New("envelope input and output must be different files")
	}
	inputInfo, inputErr := os.Stat(inputAbs)
	outputInfo, outputErr := os.Stat(outputAbs)
	if inputErr == nil && outputErr == nil && os.SameFile(inputInfo, outputInfo) {
		return errors.New("envelope input and output must be different files")
	}
	return nil
}

// WriteFileAtomic0600 prevents readers from observing a partial custody
// artifact. The output itself is always mode 0600, including replacement of a
// pre-existing file.
func WriteFileAtomic0600(path string, data []byte) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("output path is required")
	}
	dir := filepath.Dir(filepath.Clean(path))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".bonfire-dr-envelope-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
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
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func parseEnvelopeInner(inner []byte) ([]byte, EnvelopeMetadata, error) {
	if len(inner) < 4 {
		return nil, EnvelopeMetadata{}, errors.New("authenticated envelope payload is truncated")
	}
	metadataLength := int(binary.BigEndian.Uint32(inner[:4]))
	if metadataLength <= 0 || metadataLength > maxEnvelopeMetadata || len(inner) < 4+metadataLength {
		return nil, EnvelopeMetadata{}, errors.New("authenticated envelope metadata length is invalid")
	}
	var metadata EnvelopeMetadata
	decoder := json.NewDecoder(bytes.NewReader(inner[4 : 4+metadataLength]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		return nil, EnvelopeMetadata{}, fmt.Errorf("decode authenticated envelope metadata: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, EnvelopeMetadata{}, errors.New("authenticated envelope metadata has trailing content")
	}
	plaintext := inner[4+metadataLength:]
	if err := validateEnvelopeMetadata(metadata, plaintext); err != nil {
		return nil, EnvelopeMetadata{}, err
	}
	return plaintext, metadata, nil
}

func validateEnvelopeMetadata(metadata EnvelopeMetadata, plaintext []byte) error {
	if metadata.Format != EnvelopeMagic || metadata.Version != envelopeMetadataVersion {
		return errors.New("authenticated envelope metadata has unsupported format")
	}
	if strings.TrimSpace(metadata.Purpose) == "" || len(metadata.Purpose) > 128 || strings.ContainsAny(metadata.Purpose, "\r\n\x00") {
		return errors.New("authenticated envelope metadata has invalid purpose")
	}
	if metadata.PlaintextBytes != int64(len(plaintext)) {
		return errors.New("authenticated envelope metadata size mismatch")
	}
	if _, err := time.Parse(time.RFC3339Nano, metadata.SealedAt); err != nil {
		return errors.New("authenticated envelope metadata has invalid seal time")
	}
	sum := sha256.Sum256(plaintext)
	if metadata.PlaintextSHA256 != hex.EncodeToString(sum[:]) {
		return errors.New("authenticated envelope plaintext hash mismatch")
	}
	return nil
}

func sealEnvelopeBytes(key, aad, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(aad)+len(nonce)+len(plaintext)+gcm.Overhead())
	out = append(out, aad...)
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, plaintext, aad), nil
}

func openEnvelopeBytes(key, aad, blob []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(blob) < len(aad)+gcm.NonceSize()+gcm.Overhead() || !bytes.Equal(blob[:len(aad)], aad) {
		return nil, errors.New("encrypted envelope is truncated or has wrong magic")
	}
	nonce := blob[len(aad) : len(aad)+gcm.NonceSize()]
	ciphertext := blob[len(aad)+gcm.NonceSize():]
	return gcm.Open(nil, nonce, ciphertext, aad)
}

func validateEnvelopeKey(key []byte) error {
	if len(key) != 32 {
		return errors.New("AES-256-GCM envelope key must be exactly 32 bytes")
	}
	return nil
}

func readStableEnvelopeFile(path string) ([]byte, error) {
	file, err := openPathNoFollow(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() < 0 || before.Size() > maxEnvelopeFileBytes {
		return nil, fmt.Errorf("envelope file must be a stable regular file no larger than %d bytes", maxEnvelopeFileBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(file, before.Size()+1))
	if err != nil || int64(len(raw)) != before.Size() {
		return nil, errors.New("envelope file changed during read")
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || after.Size() != before.Size() || after.ModTime() != before.ModTime() || after.Mode() != before.Mode() {
		return nil, errors.New("envelope file changed while opened")
	}
	if err := verifyPathStillOpened(path, file); err != nil {
		return nil, err
	}
	return raw, nil
}
