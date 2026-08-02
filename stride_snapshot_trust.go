package main

// Snapshot MACs establish an authenticity boundary for offline STRIDE state.
// The key must come from an external secret store; it is deliberately never
// serialized with a snapshot. The caller must also persist the highest
// accepted generation outside the snapshot so an older, otherwise authentic
// file cannot be replayed after a restore.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

const strideSnapshotMinimumMACKeyBytes = 32

type STRIDESnapshotMACAuthority struct {
	KeyID string
	Key   []byte
}

type STRIDESnapshotRestorePolicy struct {
	Authority         STRIDESnapshotMACAuthority
	MinimumGeneration uint64
}

func (authority STRIDESnapshotMACAuthority) valid() bool {
	return strideIdentifier(authority.KeyID) && len(authority.Key) >= strideSnapshotMinimumMACKeyBytes
}

func strideSnapshotMAC(authority STRIDESnapshotMACAuthority, domain string, generation uint64, digest string) (string, error) {
	if !authority.valid() || !strideIdentifier(domain) || generation == 0 || !isHexDigest(digest) {
		return "", ErrSTRIDEWorkforceInvalid
	}
	material, err := canonicalJSON(struct {
		Domain     string `json:"domain"`
		KeyID      string `json:"keyId"`
		Generation uint64 `json:"generation"`
		Digest     string `json:"digest"`
	}{domain, authority.KeyID, generation, digest})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, authority.Key)
	_, _ = mac.Write(material)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func verifySTRIDESnapshotMAC(policy STRIDESnapshotRestorePolicy, domain, keyID string, generation uint64, digest, signature string) bool {
	if !policy.Authority.valid() || keyID != policy.Authority.KeyID || generation == 0 || generation < policy.MinimumGeneration {
		return false
	}
	want, err := strideSnapshotMAC(policy.Authority, domain, generation, digest)
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	expected, _ := hex.DecodeString(want)
	return hmac.Equal(got, expected)
}
