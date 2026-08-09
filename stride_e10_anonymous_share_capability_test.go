package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type strideE10AnonymousShareTestKeys struct {
	mu      sync.Mutex
	current StrideE10AnonymousShareKey
	keys    map[string]StrideE10AnonymousShareKey
}

func (m *strideE10AnonymousShareTestKeys) CurrentStrideE10AnonymousShareKey() (StrideE10AnonymousShareKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current, nil
}

func (m *strideE10AnonymousShareTestKeys) ResolveStrideE10AnonymousShareKey(id string, version uint64) (StrideE10AnonymousShareKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ok := m.keys[fmt.Sprintf("%s:%d", id, version)]
	if !ok {
		return StrideE10AnonymousShareKey{}, errors.New("unknown key")
	}
	return key, nil
}

func strideE10AnonymousShareTestManager() *strideE10AnonymousShareTestKeys {
	key := StrideE10AnonymousShareKey{ID: "share_key", Version: 1, Secret: []byte("0123456789abcdef0123456789abcdef")}
	return &strideE10AnonymousShareTestKeys{current: key, keys: map[string]StrideE10AnonymousShareKey{"share_key:1": key}}
}

func TestStrideE10AnonymousShareCapabilityOpenRevokeDeleteRotateTamperRestart(t *testing.T) {
	shareLinkTestEnv(t)
	manager := strideE10AnonymousShareTestManager()
	restore := InstallStrideE10AnonymousShareKeyManager(manager)
	defer restore()
	artifact := seedShareArtifact(t, artifactStatusApproved, "signed anonymous body", nil)
	header := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact))
	now := time.Now().UTC()
	record := shareLinkRecord{
		ID: "share_signed", ArtifactID: artifact.ID, TenantID: header.TenantID, ObjectType: "artifact", Revision: artifactVersion(artifact), ACLGeneration: header.ACLVersion,
		ContentDigest: artifactCapabilityDigest(artifact), Action: "read_content", Status: shareLinkStatusActive, CreatedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano),
	}
	token, err := mintStrideE10AnonymousShareCapability(StrideE10AnonymousShareCapability{
		LinkID: record.ID, TenantID: record.TenantID, ArtifactID: record.ArtifactID, Revision: record.Revision, ACLGeneration: record.ACLGeneration,
		ContentDigest: record.ContentDigest, ExpiresUnix: now.Add(time.Hour).Unix(), Nonce: "nonce_signed",
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, _ := resolveStrideE10AnonymousShareCapability(token, now)
	record.KeyID, record.KeyVersion = claims.KeyID, claims.KeyVersion
	record.TokenHash = sha256Hex([]byte(token))
	if err := saveShareLinks([]shareLinkRecord{record}); err != nil {
		t.Fatal(err)
	}
	open := func(candidate string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/a/"+candidate, nil)
		request.Header.Set("Authorization", "Bearer wrong-org-session-cannot-escalate")
		response := httptest.NewRecorder()
		shareLinkPublicCutoverHandler(response, request)
		return response
	}
	if response := open(token); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "signed anonymous body") {
		t.Fatalf("valid anonymous open status=%d body=%s", response.Code, response.Body.String())
	}
	tampered := "A" + token[1:]
	if token[0] == 'A' {
		tampered = "B" + token[1:]
	}
	if response := open(tampered); response.Code != http.StatusNotFound {
		t.Fatalf("tampered token status=%d", response.Code)
	}

	// A restart with the same managed key and durable record remains valid.
	restore()
	restore = InstallStrideE10AnonymousShareKeyManager(manager)
	if response := open(token); response.Code != http.StatusOK {
		t.Fatalf("restart open status=%d", response.Code)
	}
	originalExpiry := record.ExpiresAt
	record.ExpiresAt = now.Add(2 * time.Hour).Format(time.RFC3339Nano)
	if err := saveShareLinks([]shareLinkRecord{record}); err != nil {
		t.Fatal(err)
	}
	if response := open(token); response.Code != http.StatusNotFound {
		t.Fatalf("expiry-binding tamper status=%d", response.Code)
	}
	record.ExpiresAt = originalExpiry
	if err := saveShareLinks([]shareLinkRecord{record}); err != nil {
		t.Fatal(err)
	}

	// Removing the old managed key is a deliberate rotation fence.
	manager.mu.Lock()
	manager.current = StrideE10AnonymousShareKey{ID: "share_key", Version: 2, Secret: []byte("abcdef0123456789abcdef0123456789")}
	manager.keys = map[string]StrideE10AnonymousShareKey{"share_key:2": manager.current}
	manager.mu.Unlock()
	if response := open(token); response.Code != http.StatusNotFound {
		t.Fatalf("retired key status=%d", response.Code)
	}
	manager.mu.Lock()
	manager.keys["share_key:1"] = StrideE10AnonymousShareKey{ID: "share_key", Version: 1, Secret: []byte("0123456789abcdef0123456789abcdef")}
	manager.mu.Unlock()

	record.Status, record.TokenHash = shareLinkStatusRevoked, ""
	if err := saveShareLinks([]shareLinkRecord{record}); err != nil {
		t.Fatal(err)
	}
	if response := open(token); response.Code != http.StatusNotFound {
		t.Fatalf("revoked token status=%d", response.Code)
	}
	record.Status, record.TokenHash = shareLinkStatusActive, sha256Hex([]byte(token))
	if err := saveShareLinks([]shareLinkRecord{record}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := kanbanApp.memory.updateOSArtifactMetadata(artifact.ID, map[string]string{"aclVersion": "2"}); err != nil {
		t.Fatal(err)
	}
	if response := open(token); response.Code != http.StatusNotFound {
		t.Fatalf("acl-generation change status=%d", response.Code)
	}
	if _, _, deleted, err := kanbanApp.memory.deleteOSArtifactWithProjection(artifact.ID); err != nil || !deleted {
		t.Fatalf("delete artifact deleted=%t err=%v", deleted, err)
	}
	if response := open(token); response.Code != http.StatusNotFound {
		t.Fatalf("deleted artifact status=%d", response.Code)
	}
}

func TestStrideE10AnonymousShareCapabilityWrongTenantAndExpiry(t *testing.T) {
	manager := strideE10AnonymousShareTestManager()
	restore := InstallStrideE10AnonymousShareKeyManager(manager)
	defer restore()
	now := time.Now().UTC()
	claims := StrideE10AnonymousShareCapability{LinkID: "share_one", TenantID: "org_one", ArtifactID: "artifact_one", Revision: 1, ACLGeneration: 1, ContentDigest: strings.Repeat("a", 64), ExpiresUnix: now.Add(time.Minute).Unix(), Nonce: "nonce_one"}
	token, err := mintStrideE10AnonymousShareCapability(claims)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveStrideE10AnonymousShareCapability(token, now)
	if err != nil || resolved.TenantID != "org_one" {
		t.Fatalf("resolve=%+v err=%v", resolved, err)
	}
	if _, err := resolveStrideE10AnonymousShareCapability(token, now.Add(2*time.Minute)); !errors.Is(err, ErrStrideE10AnonymousShareCapability) {
		t.Fatalf("expired capability err=%v", err)
	}
	resolved.TenantID = "org_other"
	if resolved.TenantID == claims.TenantID {
		t.Fatal("wrong tenant capability comparison escalated")
	}
}
