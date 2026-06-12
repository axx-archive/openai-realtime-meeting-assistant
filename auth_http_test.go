package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func setupAuthTestEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("BONFIRE_USERS_PATH", filepath.Join(dir, "users.json"))
	t.Setenv("BONFIRE_SESSIONS_PATH", filepath.Join(dir, "sessions.json"))
	resetAuthRateLimitersForTest()
}

func postAuthJSON(t *testing.T, path, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	authHandler(recorder, req)
	return recorder
}

func loginAs(t *testing.T, email, password string) []*http.Cookie {
	t.Helper()
	recorder := postAuthJSON(t, "/auth/login", fmt.Sprintf(`{"email":%q,"password":%q}`, email, password), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login failed: status %d body %s", recorder.Code, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected login to set a session cookie")
	}
	return cookies
}

func TestLoginSetsSessionCookieAndMeWorks(t *testing.T) {
	setupAuthTestEnv(t)

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	var sessionCookie *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name == sessionCookieName {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil {
		t.Fatalf("expected %s cookie", sessionCookieName)
	}
	if !sessionCookie.HttpOnly {
		t.Error("expected session cookie to be HttpOnly")
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.AddCookie(sessionCookie)
	recorder := httptest.NewRecorder()
	authHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected /auth/me to return 200, got %d", recorder.Code)
	}
	var payload struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal /auth/me: %v", err)
	}
	if payload.Email != "aj@shareability.com" || payload.Name != "AJ" {
		t.Errorf("unexpected identity: %+v", payload)
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	setupAuthTestEnv(t)

	recorder := postAuthJSON(t, "/auth/login", `{"email":"aj@shareability.com","password":"nope"}`, nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad password, got %d", recorder.Code)
	}
	recorder = postAuthJSON(t, "/auth/login", `{"email":"stranger@evil.com","password":"B0NFIRE!"}`, nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unknown account, got %d", recorder.Code)
	}
}

func TestAuthMeWithoutSession(t *testing.T) {
	setupAuthTestEnv(t)

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	recorder := httptest.NewRecorder()
	authHandler(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without session, got %d", recorder.Code)
	}
}

func TestLogoutDestroysSession(t *testing.T) {
	setupAuthTestEnv(t)

	cookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	recorder := postAuthJSON(t, "/auth/logout", "", cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected logout 200, got %d", recorder.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	after := httptest.NewRecorder()
	authHandler(after, req)
	if after.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d", after.Code)
	}
}

func TestSessionsPersistAcrossReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := newSessionStore(path)
	token, err := store.create("aj@shareability.com")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	reloaded := newSessionStore(path)
	if email, ok := reloaded.lookup(token); !ok || email != "aj@shareability.com" {
		t.Fatalf("expected session to survive reload, got %q ok=%v", email, ok)
	}
}

func TestCrossOriginAuthPostRejected(t *testing.T) {
	setupAuthTestEnv(t)

	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"email":"aj@shareability.com","password":"B0NFIRE!"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()
	authHandler(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-origin auth POST, got %d", recorder.Code)
	}
}

func TestChangePasswordEndpoint(t *testing.T) {
	setupAuthTestEnv(t)

	cookies := loginAs(t, "tyler@shareability.com", "B0NFIRE!")

	recorder := postAuthJSON(t, "/auth/change-password", `{"currentPassword":"wrong","newPassword":"freshpass99"}`, cookies)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for wrong current password, got %d", recorder.Code)
	}

	recorder = postAuthJSON(t, "/auth/change-password", `{"currentPassword":"B0NFIRE!","newPassword":"freshpass99"}`, cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200 for password change, got %d body %s", recorder.Code, recorder.Body.String())
	}

	if _, ok := accountStore().authenticate("tyler@shareability.com", "B0NFIRE!"); ok {
		t.Error("expected old password to stop working")
	}
	if _, ok := accountStore().authenticate("tyler@shareability.com", "freshpass99"); !ok {
		t.Error("expected new password to work")
	}
}

func TestLoginRateLimited(t *testing.T) {
	setupAuthTestEnv(t)

	var last *httptest.ResponseRecorder
	for i := 0; i < loginAttemptLimit+1; i++ {
		last = postAuthJSON(t, "/auth/login", `{"email":"aj@shareability.com","password":"nope"}`, nil)
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after %d attempts, got %d", loginAttemptLimit+1, last.Code)
	}
}
