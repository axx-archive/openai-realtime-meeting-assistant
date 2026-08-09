package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

func TestPasskeySessionMintPreservesOffAndUsesCanonicalZeroOrganizationCutover(t *testing.T) {
	setupAuthTestEnv(t)
	now := time.Now().UTC()
	off, _, _, _ := strideE10TenantTestConverter(now, false, StrideE10TenantConversionCutover)
	restore := InstallStrideE10TenantRuntimeConverter(off)
	token, err := strideE10CreatePasskeyAuthenticatedSession("aj@shareability.com")
	restore()
	if err != nil {
		t.Fatal(err)
	}
	legacy, ok := userSessionStore().lookupRecord(token)
	if !ok || legacy.Email != "aj@shareability.com" || legacy.PersonID != "" || legacy.AuthorityGeneration != 0 {
		t.Fatalf("off passkey session drifted: %+v ok=%t", legacy, ok)
	}

	converter, _, _, _ := strideE10TenantTestConverter(now, true, StrideE10TenantConversionCutover)
	restore = InstallStrideE10TenantRuntimeConverter(converter)
	defer restore()
	runtime := NewStrideE10ProductLiveRuntime(func() time.Time { return now })
	priorRuntime := strideE10LiveProductRuntime
	strideE10LiveProductRuntime = runtime
	defer func() { strideE10LiveProductRuntime = priorRuntime }()
	email := "passkey-canonical@example.com"
	countSessions := func() int {
		store := userSessionStore()
		store.mu.Lock()
		defer store.mu.Unlock()
		return len(store.sessions)
	}
	before := countSessions()
	if _, err := strideE10CreatePasskeyAuthenticatedSession(email); !errors.Is(err, ErrStrideE10TenantAuthorityStale) || countSessions() != before {
		t.Fatalf("missing person mapping minted passkey session err=%v before=%d after=%d", err, before, countSessions())
	}
	person := organizationTestPerson("person-passkey-canonical", '8', now.Add(-time.Hour))
	person.AccountSubjectDigest = sha256Hex([]byte(strings.ToLower(email)))
	if err := runtime.organization.RegisterPerson(person); err != nil {
		t.Fatal(err)
	}
	canonicalToken, err := strideE10CreatePasskeyAuthenticatedSession(email)
	if err != nil {
		t.Fatal(err)
	}
	canonical, ok := userSessionStore().lookupRecord(canonicalToken)
	if !ok || canonical.PersonID != person.Header.ID || canonical.AccountSubjectDigest != person.AccountSubjectDigest || canonical.AuthorityGeneration != 1 || canonical.ActiveOrganizationID != "" || canonical.OrganizationMembershipID != "" || canonical.OrganizationMembershipRev != 0 || canonical.ActiveOrganizationSessionRev != 0 {
		t.Fatalf("canonical zero-org passkey session=%+v ok=%t", canonical, ok)
	}
	runtime.organization.mu.Lock()
	revoked := runtime.organization.persons[person.Header.ID]
	revoked.Status = "revoked"
	runtime.organization.persons[person.Header.ID] = revoked
	runtime.organization.mu.Unlock()
	before = countSessions()
	if _, err := strideE10CreatePasskeyAuthenticatedSession(email); !errors.Is(err, ErrStrideE10TenantAuthorityStale) || countSessions() != before {
		t.Fatalf("revoked person minted passkey session err=%v before=%d after=%d", err, before, countSessions())
	}
}

func TestNativePasskeyBeginReturnsOneUseCeremonyHeader(t *testing.T) {
	setupAuthTestEnv(t)

	req := httptest.NewRequest(http.MethodPost, "/auth/passkey/login/begin", nil)
	req.Header.Set("X-Bonfire-Client", "expo")
	recorder := httptest.NewRecorder()
	authHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("native passkey begin status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	ceremonyID := recorder.Header().Get(webauthnCeremonyHeaderName)
	if ceremonyID == "" {
		t.Fatal("native passkey begin did not return a ceremony header")
	}

	finishReq := httptest.NewRequest(http.MethodPost, "/auth/passkey/login/finish", nil)
	finishReq.Header.Set("X-Bonfire-Client", "expo")
	finishReq.Header.Set(webauthnCeremonyHeaderName, ceremonyID)
	ceremony, err := takeWebauthnCeremony(finishReq)
	if err != nil || ceremony == nil || ceremony.session == nil {
		t.Fatalf("native ceremony header was not accepted: ceremony=%v err=%v", ceremony, err)
	}
	if _, err := takeWebauthnCeremony(finishReq); err == nil {
		t.Fatal("native ceremony header must be one-use")
	}
}

func TestPasskeyRegisterBeginRequiresSession(t *testing.T) {
	setupAuthTestEnv(t)

	recorder := postAuthJSON(t, "/auth/passkey/register/begin", "", nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without session, got %d", recorder.Code)
	}
}

func TestPasskeyRegisterBeginReturnsChallenge(t *testing.T) {
	setupAuthTestEnv(t)

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	recorder := postAuthJSON(t, "/auth/passkey/register/begin", "", cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", recorder.Code, recorder.Body.String())
	}

	var payload struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			RP        struct {
				ID string `json:"id"`
			} `json:"rp"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode options: %v", err)
	}
	if payload.PublicKey.Challenge == "" {
		t.Fatal("expected a registration challenge")
	}

	ceremonySet := false
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == webauthnCeremonyCookieName && cookie.Value != "" {
			ceremonySet = true
		}
	}
	if !ceremonySet {
		t.Fatal("expected a ceremony cookie to be set")
	}
}

func TestPasskeyLoginBeginReturnsChallenge(t *testing.T) {
	setupAuthTestEnv(t)

	recorder := postAuthJSON(t, "/auth/passkey/login/begin", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode options: %v", err)
	}
	if payload.PublicKey.Challenge == "" {
		t.Fatal("expected a login challenge")
	}
}

func TestPasskeyListAndDelete(t *testing.T) {
	setupAuthTestEnv(t)

	if err := accountStore().updateCredentials("aj@shareability.com", func(user *userAccount) {
		user.Credentials = append(user.Credentials, webauthn.Credential{ID: []byte("test-credential-1")})
	}); err != nil {
		t.Fatalf("seed credential: %v", err)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")

	req := httptest.NewRequest(http.MethodGet, "/auth/passkeys", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	authHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected passkey list 200, got %d", recorder.Code)
	}
	var listing struct {
		Passkeys []struct {
			ID string `json:"id"`
		} `json:"passkeys"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listing); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listing.Passkeys) != 1 || listing.Passkeys[0].ID == "" {
		t.Fatalf("expected one listed passkey, got %+v", listing.Passkeys)
	}

	deleteBody, _ := json.Marshal(map[string]string{"id": listing.Passkeys[0].ID})
	recorder = postAuthJSON(t, "/auth/passkey/delete", string(deleteBody), cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected delete 200, got %d body %s", recorder.Code, recorder.Body.String())
	}

	if creds := accountStore().findUser("aj@shareability.com").Credentials; len(creds) != 0 {
		t.Fatalf("expected credential removed, still have %d", len(creds))
	}
}
