package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-realtime-meeting-assistant/internal/e10evidence"
)

func TestReadHexRequiresOneCanonicalLowercaseValue(t *testing.T) {
	directory := t.TempDir()
	validPath := filepath.Join(directory, "valid.hex")
	if err := os.WriteFile(validPath, []byte(strings.Repeat("ab", 32)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := readHex(validPath)
	if err != nil || len(value) != 32 {
		t.Fatalf("valid canonical hex: len=%d err=%v", len(value), err)
	}
	for name, body := range map[string]string{
		"uppercase":      strings.Repeat("AB", 32),
		"embedded-space": strings.Repeat("ab", 8) + " " + strings.Repeat("ab", 24),
		"multiple-lines": strings.Repeat("ab", 16) + "\n" + strings.Repeat("ab", 16),
		"empty":          "",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(directory, name+".hex")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readHex(path); err == nil {
				t.Fatal("non-canonical hex was accepted")
			}
		})
	}
}

func TestReadTrustRootsRequiresSeparatelyApprovedExactDigest(t *testing.T) {
	roots := e10evidence.TrustRoots{SchemaVersion: e10evidence.TrustRootsSchema, TrustRootID: "approved-roots-001", PreMeasurementTargetRegistrySHA256: strings.Repeat("9", 64), ApprovedSigners: []e10evidence.ApprovedSigner{
		{KeyID: "registry-key", IdentityID: "release-owner", Role: "registry_owner", PublicKeyFingerprintSHA256: strings.Repeat("a", 64)},
		{KeyID: "operator-key", IdentityID: "evidence-operator", Role: "operator", PublicKeyFingerprintSHA256: strings.Repeat("b", 64)},
		{KeyID: "reviewer-key", IdentityID: "independent-reviewer", Role: "independent_reviewer", PublicKeyFingerprintSHA256: strings.Repeat("c", 64)},
		{KeyID: "pilot-reviewer-key-one", IdentityID: "pilot-reviewer-one", Role: "pilot_reviewer", PublicKeyFingerprintSHA256: strings.Repeat("e", 64)},
		{KeyID: "pilot-reviewer-key-two", IdentityID: "pilot-reviewer-two", Role: "pilot_reviewer", PublicKeyFingerprintSHA256: strings.Repeat("f", 64)},
	}}
	raw, err := json.Marshal(roots)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "trust-roots.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readTrustRoots(path, strings.Repeat("d", 64)); err == nil {
		t.Fatal("unapproved trust-root bytes were accepted")
	}
	if _, err := readTrustRoots(path, e10evidence.RegistryDigest(raw)); err != nil {
		t.Fatalf("approved trust roots rejected: %v", err)
	}
}
