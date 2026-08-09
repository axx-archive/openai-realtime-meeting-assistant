package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"time"
)

var ErrStrideE10AnonymousShareCapability = errors.New("invalid anonymous share capability")

type StrideE10AnonymousShareKey struct {
	ID      string
	Version uint64
	Secret  []byte
}

type StrideE10AnonymousShareKeyManager interface {
	CurrentStrideE10AnonymousShareKey() (StrideE10AnonymousShareKey, error)
	ResolveStrideE10AnonymousShareKey(string, uint64) (StrideE10AnonymousShareKey, error)
}

type StrideE10AnonymousShareCapability struct {
	SchemaVersion int    `json:"schemaVersion"`
	KeyID         string `json:"keyId"`
	KeyVersion    uint64 `json:"keyVersion"`
	LinkID        string `json:"linkId"`
	TenantID      string `json:"tenantId"`
	ArtifactID    string `json:"artifactId"`
	Revision      int    `json:"revision"`
	ACLGeneration int64  `json:"aclGeneration"`
	ContentDigest string `json:"contentDigest"`
	ExpiresUnix   int64  `json:"expiresUnix"`
	Nonce         string `json:"nonce"`
}

var strideE10AnonymousShareKeys atomic.Pointer[strideE10AnonymousShareKeyHolder]

type strideE10AnonymousShareKeyHolder struct {
	manager StrideE10AnonymousShareKeyManager
}

func InstallStrideE10AnonymousShareKeyManager(manager StrideE10AnonymousShareKeyManager) (restore func()) {
	var next *strideE10AnonymousShareKeyHolder
	if manager != nil {
		next = &strideE10AnonymousShareKeyHolder{manager: manager}
	}
	prior := strideE10AnonymousShareKeys.Swap(next)
	return func() { strideE10AnonymousShareKeys.Store(prior) }
}

func currentStrideE10AnonymousShareKeyManager() StrideE10AnonymousShareKeyManager {
	holder := strideE10AnonymousShareKeys.Load()
	if holder == nil {
		return nil
	}
	return holder.manager
}

func mintStrideE10AnonymousShareCapability(claims StrideE10AnonymousShareCapability) (string, error) {
	manager := currentStrideE10AnonymousShareKeyManager()
	if manager == nil {
		return "", ErrStrideE10AnonymousShareCapability
	}
	key, err := manager.CurrentStrideE10AnonymousShareKey()
	if err != nil || !validStrideE10AnonymousShareKey(key) {
		return "", ErrStrideE10AnonymousShareCapability
	}
	claims.SchemaVersion, claims.KeyID, claims.KeyVersion = 1, key.ID, key.Version
	if !validStrideE10AnonymousShareClaims(claims, time.Now().UTC()) {
		return "", ErrStrideE10AnonymousShareCapability
	}
	payload, _ := json.Marshal(claims)
	mac := hmac.New(sha256.New, key.Secret)
	_, _ = mac.Write([]byte("stride-e10-anonymous-share-v1\x00"))
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func resolveStrideE10AnonymousShareCapability(token string, now time.Time) (StrideE10AnonymousShareCapability, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 || now.IsZero() {
		return StrideE10AnonymousShareCapability{}, ErrStrideE10AnonymousShareCapability
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(payload) > 4096 {
		return StrideE10AnonymousShareCapability{}, ErrStrideE10AnonymousShareCapability
	}
	var claims StrideE10AnonymousShareCapability
	if json.Unmarshal(payload, &claims) != nil || !validStrideE10AnonymousShareClaims(claims, now.UTC()) {
		return StrideE10AnonymousShareCapability{}, ErrStrideE10AnonymousShareCapability
	}
	manager := currentStrideE10AnonymousShareKeyManager()
	if manager == nil {
		return StrideE10AnonymousShareCapability{}, ErrStrideE10AnonymousShareCapability
	}
	key, err := manager.ResolveStrideE10AnonymousShareKey(claims.KeyID, claims.KeyVersion)
	if err != nil || !validStrideE10AnonymousShareKey(key) || key.ID != claims.KeyID || key.Version != claims.KeyVersion {
		return StrideE10AnonymousShareCapability{}, ErrStrideE10AnonymousShareCapability
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return StrideE10AnonymousShareCapability{}, ErrStrideE10AnonymousShareCapability
	}
	mac := hmac.New(sha256.New, key.Secret)
	_, _ = mac.Write([]byte("stride-e10-anonymous-share-v1\x00"))
	_, _ = mac.Write(payload)
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return StrideE10AnonymousShareCapability{}, ErrStrideE10AnonymousShareCapability
	}
	return claims, nil
}

func validStrideE10AnonymousShareKey(key StrideE10AnonymousShareKey) bool {
	return strideIdentifier(key.ID) && key.Version > 0 && len(key.Secret) >= 32
}

func validStrideE10AnonymousShareClaims(claims StrideE10AnonymousShareCapability, now time.Time) bool {
	return claims.SchemaVersion == 1 && strideIdentifier(claims.KeyID) && claims.KeyVersion > 0 && strideIdentifier(claims.LinkID) &&
		strideIdentifier(claims.TenantID) && strideIdentifier(claims.ArtifactID) && claims.Revision > 0 && claims.ACLGeneration > 0 &&
		isHexDigest(claims.ContentDigest) && claims.ExpiresUnix > now.Unix() && strideIdentifier(claims.Nonce)
}
