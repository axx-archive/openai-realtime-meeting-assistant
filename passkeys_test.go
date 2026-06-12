package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
)

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
