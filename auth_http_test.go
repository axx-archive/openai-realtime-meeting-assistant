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
	t.Setenv("BONFIRE_PUBLIC_URL", "https://bonfire.test")
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
	name := participantNameForEmail(email)
	if name == "" {
		name = email
	}
	recorder := postAuthJSON(t, "/auth/login", fmt.Sprintf(`{"name":%q,"password":%q}`, name, password), nil)
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

	recorder := postAuthJSON(t, "/auth/login", `{"name":"AJ","password":"nope"}`, nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad password, got %d", recorder.Code)
	}
	recorder = postAuthJSON(t, "/auth/login", `{"name":"Jake","password":"B0NFIRE!"}`, nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for non-roster account, got %d", recorder.Code)
	}
	recorder = postAuthJSON(t, "/auth/login", `{"email":"aj@shareability.com","password":"B0NFIRE!"}`, nil)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for email login payload, got %d", recorder.Code)
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

	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"name":"AJ","password":"B0NFIRE!"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()
	authHandler(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-origin auth POST, got %d", recorder.Code)
	}
}

func TestParticipantsEndpointRequiresSession(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() {
		kanbanApp = previousApp
	})

	req := httptest.NewRequest(http.MethodGet, "/participants", nil)
	recorder := httptest.NewRecorder()
	participantsHandler(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected /participants without session to return 401, got %d", recorder.Code)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	req = httptest.NewRequest(http.MethodGet, "/participants", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	participantsHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected /participants with session to return 200, got %d body %s", recorder.Code, recorder.Body.String())
	}
}

func TestClientConfigEndpointRequiresSession(t *testing.T) {
	setupAuthTestEnv(t)

	req := httptest.NewRequest(http.MethodGet, "/client-config", nil)
	recorder := httptest.NewRecorder()
	clientConfigHandler(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected /client-config without session to return 401, got %d", recorder.Code)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	req = httptest.NewRequest(http.MethodGet, "/client-config", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	clientConfigHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected /client-config with session to return 200, got %d body %s", recorder.Code, recorder.Body.String())
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

func TestUpdateProfileEndpointPersistsNameAndAvatar(t *testing.T) {
	setupAuthTestEnv(t)

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	avatar := "data:image/png;base64,aGVsbG8="
	recorder := postAuthJSON(t, "/auth/profile", fmt.Sprintf(`{"displayName":"  AJ Hart  ","avatarDataURL":%q}`, avatar), cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected profile update 200, got %d body %s", recorder.Code, recorder.Body.String())
	}

	var payload struct {
		Email         string `json:"email"`
		Name          string `json:"name"`
		AvatarDataURL string `json:"avatarDataURL"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal profile response: %v", err)
	}
	if payload.Email != "aj@shareability.com" || payload.Name != "AJ Hart" || payload.AvatarDataURL != avatar {
		t.Fatalf("unexpected profile payload: %+v", payload)
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	after := httptest.NewRecorder()
	authHandler(after, req)
	if after.Code != http.StatusOK {
		t.Fatalf("expected /auth/me after update 200, got %d", after.Code)
	}
	payload = struct {
		Email         string `json:"email"`
		Name          string `json:"name"`
		AvatarDataURL string `json:"avatarDataURL"`
	}{}
	if err := json.Unmarshal(after.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal /auth/me after update: %v", err)
	}
	if payload.Name != "AJ Hart" || payload.AvatarDataURL != avatar {
		t.Fatalf("expected profile to persist, got %+v", payload)
	}
}

func TestUpdateProfileEndpointRejectsInvalidPayload(t *testing.T) {
	setupAuthTestEnv(t)

	recorder := postAuthJSON(t, "/auth/profile", `{"displayName":"AJ Hart"}`, nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected profile update without session to return 401, got %d", recorder.Code)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	recorder = postAuthJSON(t, "/auth/profile", `{"displayName":" ","avatarDataURL":""}`, cookies)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected blank display name to return 400, got %d", recorder.Code)
	}

	recorder = postAuthJSON(t, "/auth/profile", `{"displayName":"AJ Hart","avatarDataURL":"data:text/plain;base64,aGVsbG8="}`, cookies)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected non-image avatar to return 400, got %d", recorder.Code)
	}

	recorder = postAuthJSON(t, "/auth/profile", `{"displayName":"AJ Hart","avatarDataURL":"data:image/png;base64,not-valid***"}`, cookies)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected malformed avatar data to return 400, got %d", recorder.Code)
	}

	longName := strings.Repeat("a", 81)
	recorder = postAuthJSON(t, "/auth/profile", fmt.Sprintf(`{"displayName":%q,"avatarDataURL":""}`, longName), cookies)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected too-long display name to return 400, got %d", recorder.Code)
	}
}

func TestUpdateProfileEndpointSupportsAvatarClearingAndLimits(t *testing.T) {
	setupAuthTestEnv(t)

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	avatar := "data:image/gif;base64,aGVsbG8="
	recorder := postAuthJSON(t, "/auth/profile", fmt.Sprintf(`{"displayName":"AJ Hart","avatarDataURL":%q}`, avatar), cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected initial profile update 200, got %d body %s", recorder.Code, recorder.Body.String())
	}

	recorder = postAuthJSON(t, "/auth/profile", `{"displayName":"AJ Hart","avatarDataURL":""}`, cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected clearing avatar to return 200, got %d body %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		AvatarDataURL string `json:"avatarDataURL"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal cleared profile response: %v", err)
	}
	if payload.AvatarDataURL != "" {
		t.Fatalf("expected avatar to clear, got %q", payload.AvatarDataURL)
	}

	tooLargeAvatar := "data:image/png;base64," + strings.Repeat("a", avatarDataURLLimit)
	recorder = postAuthJSON(t, "/auth/profile", fmt.Sprintf(`{"displayName":"AJ Hart","avatarDataURL":%q}`, tooLargeAvatar), cookies)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected oversized avatar to return 400, got %d", recorder.Code)
	}

	tooLargeBody := fmt.Sprintf(`{"displayName":"AJ Hart","avatarDataURL":"data:image/png;base64,%s"}`, strings.Repeat("a", profileBodyLimit))
	recorder = postAuthJSON(t, "/auth/profile", tooLargeBody, cookies)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected oversized request body to return 400, got %d", recorder.Code)
	}
}

func TestLoginRateLimited(t *testing.T) {
	setupAuthTestEnv(t)

	var last *httptest.ResponseRecorder
	for i := 0; i < loginAttemptLimit+1; i++ {
		last = postAuthJSON(t, "/auth/login", `{"name":"AJ","password":"nope"}`, nil)
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after %d attempts, got %d", loginAttemptLimit+1, last.Code)
	}
}
