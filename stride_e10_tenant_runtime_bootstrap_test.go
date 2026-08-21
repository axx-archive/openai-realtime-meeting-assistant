package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStrideE10TenantProductionBootstrapOffPreservesLegacyAndInvalidActivationFails(t *testing.T) {
	organizations := NewOrganizationAuthorityService()
	sessions := &sessionStore{sessions: map[string]sessionRecord{}}
	restore, err := installStrideE10TenantProductionRuntime(strideE10TenantProductionBootstrapConfig{mode: strideE10TenantRuntimeModeOff, sessions: sessions, organizations: organizations})
	if err != nil {
		t.Fatal(err)
	}
	legacyCalls, canonicalCalls := 0, 0
	err = withStrideE10TenantRuntimeAuthority(context.Background(), StrideE10TenantSurfaceHTTP, strings.Repeat("a", 64), func() error {
		legacyCalls++
		return nil
	}, func(StrideE10TenantPrincipal) error {
		canonicalCalls++
		return nil
	})
	restore()
	if err != nil || legacyCalls != 1 || canonicalCalls != 0 {
		t.Fatalf("off composition drift err=%v legacy=%d canonical=%d", err, legacyCalls, canonicalCalls)
	}
	if _, err := installStrideE10TenantProductionRuntime(strideE10TenantProductionBootstrapConfig{mode: string(StrideE10TenantConversionCutover), sessions: sessions, organizations: organizations}); !errors.Is(err, ErrStrideE10TenantAuthorityInvalid) {
		t.Fatalf("invalid requested activation did not fail closed: %v", err)
	}
	validButPrematureCutover := strideE10TenantProductionBootstrapConfig{
		mode: string(StrideE10TenantConversionCutover), sessions: sessions, organizations: organizations,
		receiptPath: filepath.Join(t.TempDir(), "receipts.jsonl"),
		receiptKey:  StrideE10TenantReceiptKey{ID: "tenant-receipt-w4-required", Version: 1, Secret: []byte(strings.Repeat("r", 32))},
		envelopeKey: StrideE10TenantAuthorityEnvelopeKey{ID: "tenant-envelope-w4-required", Version: 1, Secret: []byte(strings.Repeat("e", 32))},
	}
	if _, err := installStrideE10TenantProductionRuntime(validButPrematureCutover); !errors.Is(err, ErrStrideE10TenantAuthorityInvalid) {
		t.Fatalf("W3 production accepted cutover without W4 durable stores: %v", err)
	}
	t.Setenv(strideE10TenantRuntimeModeEnv, string(StrideE10TenantConversionCutover))
	if _, err := strideE10TenantProductionBootstrapConfigFromEnvironment(); !errors.Is(err, ErrStrideE10TenantAuthorityInvalid) {
		t.Fatalf("environment bootstrap accepted W3 production cutover: %v", err)
	}
}

func TestStrideE10TenantProductionBootstrapShadowReachesMainResolverWithoutAuthorizing(t *testing.T) {
	setupAuthTestEnv(t)
	now := time.Now().UTC()
	organizations := NewOrganizationAuthorityService()
	email := "bootstrap@example.com"
	digest := sha256Hex([]byte(email))
	person := organizationTestPerson("person-bootstrap-runtime", 'b', now.Add(-time.Hour))
	person.AccountSubjectDigest = digest
	if err := organizations.RegisterPerson(person); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("7", 64)
	sessionHash := hashResetToken(token)
	sessions := userSessionStore()
	sessions.mu.Lock()
	sessions.sessions[sessionHash] = sessionRecord{Email: email, Expires: now.Add(time.Hour), PersonID: person.Header.ID, AccountSubjectDigest: digest, AuthorityGeneration: 1}
	sessions.mu.Unlock()
	receiptPath := filepath.Join(t.TempDir(), "private", "tenant-receipts.jsonl")
	receiptKey := StrideE10TenantReceiptKey{ID: "tenant-receipt-bootstrap", Version: 1, Secret: []byte(strings.Repeat("r", 32))}
	envelopeKey := StrideE10TenantAuthorityEnvelopeKey{ID: "tenant-envelope-bootstrap", Version: 1, Secret: []byte(strings.Repeat("e", 32))}
	priorRuntime := strideE10LiveProductRuntime
	testRuntime := NewStrideE10ProductLiveRuntime(func() time.Time { return now })
	testRuntime.organization = organizations
	strideE10LiveProductRuntime = testRuntime
	t.Cleanup(func() { strideE10LiveProductRuntime = priorRuntime })
	t.Setenv(strideE10TenantRuntimeModeEnv, string(StrideE10TenantConversionShadow))
	t.Setenv(strideE10TenantReceiptPathEnv, receiptPath)
	t.Setenv(strideE10TenantReceiptKeyIDEnv, receiptKey.ID)
	t.Setenv(strideE10TenantReceiptKeyVersionEnv, "1")
	t.Setenv(strideE10TenantReceiptKeySecretEnv, base64.StdEncoding.EncodeToString(receiptKey.Secret))
	t.Setenv(strideE10TenantEnvelopeKeyIDEnv, envelopeKey.ID)
	t.Setenv(strideE10TenantEnvelopeKeyVersionEnv, "1")
	t.Setenv(strideE10TenantEnvelopeKeySecretEnv, base64.StdEncoding.EncodeToString(envelopeKey.Secret))
	restore, err := installStrideE10TenantProductionRuntimeFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	called := false
	handler := strideE10TenantHTTPHandler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if principal, ok := strideE10TenantPrincipalFromContext(request.Context()); ok || principal.PersonID != "" {
			t.Errorf("shadow authorized canonical principal=%+v ok=%t", principal, ok)
			return
		}
		if !strideE10TenantSurfaceUseBound(request.Context(), StrideE10TenantSurfaceHTTP) {
			t.Error("shadow legacy callback lost surface binding")
			return
		}
		called = true
		writer.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/stride/v1/mobile/surfaces/profile", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if !called || recorder.Code != http.StatusNoContent {
		t.Fatalf("shadow composition was unreachable called=%t status=%d body=%s", called, recorder.Code, recorder.Body.String())
	}
	body, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 1 {
		// Admission persists one private parity receipt; final use revalidates
		// the same exact authority under the resolver callback without creating
		// a second durable observation.
		t.Fatalf("receipt count=%d body=%s", len(lines), body)
	}
	for _, line := range lines {
		var receipt StrideE10TenantDiscrepancyReceipt
		if json.Unmarshal([]byte(line), &receipt) != nil || receipt.ValidateWithKey(receiptKey) != nil || !receipt.Matches || receipt.Surface != StrideE10TenantSurfaceHTTP {
			t.Fatalf("invalid main-composition receipt: %s", line)
		}
	}
	keyring := strideE10CurrentTenantEnvelopeRuntime()
	if keyring == nil || keyring.keys == nil {
		t.Fatal("managed envelope keyring was not installed")
	}
	key, err := keyring.keys.CurrentStrideE10TenantAuthorityEnvelopeKey(context.Background())
	if err != nil || key.ID != envelopeKey.ID || key.Version != envelopeKey.Version {
		t.Fatalf("managed envelope key=%+v err=%v", key, err)
	}
}

func TestStrideE10TenantFileReceiptSinkDeduplicatesStableShadowObservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "tenant-receipts.jsonl")
	key := StrideE10TenantReceiptKey{ID: "tenant-receipt-dedupe", Version: 1, Secret: []byte(strings.Repeat("r", 32))}
	sink := &strideE10TenantFileReceiptSink{path: path, key: key}
	principal := StrideE10TenantPrincipal{TenantID: "org-one", PersonID: "person-one", AuthorityGeneration: 7}
	legacy := StrideE10LegacyPrincipalProjection{TenantID: canonicalTenantID(), AccountSubjectDigest: strings.Repeat("d", 64)}
	receipt := strideE10TenantComparisonReceipt(key, StrideE10TenantSurfaceWebSocket, principal, legacy, principal.PersonID)
	for index := 0; index < 100; index++ {
		if err := sink.RecordStrideE10TenantDiscrepancy(context.Background(), receipt); err != nil {
			t.Fatal(err)
		}
	}
	second := strideE10TenantComparisonReceipt(key, StrideE10TenantSurfaceChat, principal, legacy, principal.PersonID)
	if err := sink.RecordStrideE10TenantDiscrepancy(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 2 {
		t.Fatalf("stable shadow observations should be written once per receipt id: count=%d body=%s", len(lines), body)
	}
}
