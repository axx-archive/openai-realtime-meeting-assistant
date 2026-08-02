package dr

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnvelopeRoundTripAuthenticatesMetadataAndPayload(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	plaintext := []byte("the four-root capture bundle")
	now := time.Date(2026, 7, 29, 5, 0, 0, 0, time.UTC)
	blob, sealed, err := SealEnvelope(key, plaintext, "four-volume-capture", now)
	if err != nil {
		t.Fatal(err)
	}
	got, opened, err := OpenEnvelope(key, blob)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plaintext) || opened != sealed {
		t.Fatalf("round trip mismatch: got=%q opened=%+v sealed=%+v", got, opened, sealed)
	}
	sum := sha256.Sum256(plaintext)
	if opened.PlaintextSHA256 != hex.EncodeToString(sum[:]) || opened.PlaintextBytes != int64(len(plaintext)) {
		t.Fatalf("metadata did not bind plaintext: %+v", opened)
	}
}

func TestEnvelopeRejectsWrongKeyTamperAndTruncation(t *testing.T) {
	key := bytes.Repeat([]byte{0x43}, 32)
	blob, _, err := SealEnvelope(key, []byte("confidential bundle"), "four-volume-capture", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	wrong := bytes.Repeat([]byte{0x44}, 32)
	if _, _, err := OpenEnvelope(wrong, blob); err == nil {
		t.Fatal("wrong key accepted")
	}
	tampered := append([]byte(nil), blob...)
	tampered[len(tampered)-1] ^= 0xff
	if _, _, err := OpenEnvelope(key, tampered); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	for _, truncated := range [][]byte{blob[:0], blob[:len(EnvelopeMagic)], blob[:len(blob)-1]} {
		if _, _, err := OpenEnvelope(key, truncated); err == nil {
			t.Fatal("truncated ciphertext accepted")
		}
	}
}

func TestEnvelopeRejectsAuthenticatedMetadataHashMismatch(t *testing.T) {
	key := bytes.Repeat([]byte{0x45}, 32)
	metadata := EnvelopeMetadata{Format: EnvelopeMagic, Version: envelopeMetadataVersion, Purpose: "four-volume-capture", PlaintextSHA256: strings.Repeat("0", 64), PlaintextBytes: 7, SealedAt: "2026-07-29T05:00:00Z"}
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	inner := make([]byte, 4+len(metadataBytes)+7)
	binary.BigEndian.PutUint32(inner[:4], uint32(len(metadataBytes)))
	copy(inner[4:], metadataBytes)
	copy(inner[4+len(metadataBytes):], []byte("payload"))
	blob, err := sealEnvelopeBytes(key, []byte(EnvelopeMagic), inner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenEnvelope(key, blob); err == nil || !strings.Contains(err.Error(), "plaintext hash mismatch") {
		t.Fatalf("inconsistent authenticated metadata accepted: %v", err)
	}
}

func TestEnvelopeReadsLegacyBFBKUP01(t *testing.T) {
	key := bytes.Repeat([]byte{0x46}, 32)
	plaintext := []byte("legacy local snapshot")
	blob, err := sealEnvelopeBytes(key, []byte(legacyBackupMagic), plaintext)
	if err != nil {
		t.Fatal(err)
	}
	got, metadata, err := OpenEnvelope(key, blob)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plaintext) || metadata.Format != legacyBackupMagic || metadata.Version != 0 {
		t.Fatalf("legacy result=%q metadata=%+v", got, metadata)
	}
}

func TestEnvelopeFileIOIsAtomicAndPrivate(t *testing.T) {
	key := bytes.Repeat([]byte{0x47}, 32)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "capture.tar.gz")
	envelope := filepath.Join(dir, "capture.enc")
	opened := filepath.Join(dir, "opened.tar.gz")
	plaintext := []byte("stable capture bytes")
	if err := os.WriteFile(input, plaintext, 0o644); err != nil {
		t.Fatal(err)
	}
	sealed, err := SealEnvelopeFile(key, input, envelope, "four-volume-capture", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if sealed.EnvelopeSHA256 == "" || sealed.EnvelopeBytes == 0 {
		t.Fatalf("missing result digest: %+v", sealed)
	}
	info, err := os.Stat(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("envelope mode=%#o, want 0600", info.Mode().Perm())
	}
	if _, err := OpenEnvelopeFile(key, envelope, opened); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(opened)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("opened=%q want=%q", got, plaintext)
	}
	openedInfo, err := os.Stat(opened)
	if err != nil {
		t.Fatal(err)
	}
	if openedInfo.Mode().Perm() != 0o600 {
		t.Fatalf("plaintext output mode=%#o, want 0600", openedInfo.Mode().Perm())
	}
}

func TestEnvelopeFileRejectsSameInputAndOutput(t *testing.T) {
	key := bytes.Repeat([]byte{0x49}, 32)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "capture")
	original := []byte("do not overwrite the only capture")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SealEnvelopeFile(key, path, path, "four-volume-capture", time.Now().UTC()); err == nil {
		t.Fatal("same-file seal accepted")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("same-file rejection mutated input: got %q", got)
	}
}

func TestParseEnvelopeKeyRequiresExactAES256Key(t *testing.T) {
	key := bytes.Repeat([]byte{0x48}, 32)
	if got, err := ParseEnvelopeKey(key); err != nil || !bytes.Equal(got, key) {
		t.Fatalf("raw key result=%x err=%v", got, err)
	}
	if _, err := ParseEnvelopeKey([]byte("short")); err == nil {
		t.Fatal("short key accepted")
	}
}
