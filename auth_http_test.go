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

	// Signed-out callers get the D8 presence summary — a seat count and
	// nothing else. Names, media state, and capacity stay session-gated.
	req := httptest.NewRequest(http.MethodGet, "/participants", nil)
	recorder := httptest.NewRecorder()
	participantsHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected /participants without session to return the presence summary, got %d", recorder.Code)
	}
	var summary map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &summary); err != nil {
		t.Fatalf("presence summary is not JSON: %v", err)
	}
	if _, ok := summary["occupiedSeats"]; !ok {
		t.Fatalf("presence summary should carry occupiedSeats, got %s", recorder.Body.String())
	}
	for _, leaked := range []string{"participants", "mediaStates", "capacity", "availableSeats", "recording"} {
		if _, ok := summary[leaked]; ok {
			t.Fatalf("presence summary must not leak %q pre-auth, got %s", leaked, recorder.Body.String())
		}
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
	var payload struct {
		RTCConfiguration map[string]any `json:"rtcConfiguration"`
		ProtocolVersion  string         `json:"protocolVersion"`
		Auth             string         `json:"auth"`
		WebsocketPath    string         `json:"websocketPath"`
		SignalingRole    string         `json:"signalingRole"`
		SupportedLayers  []string       `json:"supportedLayers"`
		NativeHints      map[string]any `json:"nativeHints"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal /client-config: %v", err)
	}
	if payload.RTCConfiguration == nil {
		t.Fatal("expected existing rtcConfiguration field to remain present")
	}
	if payload.ProtocolVersion != nativeClientProtocolV1 {
		t.Fatalf("protocolVersion=%q, want %q", payload.ProtocolVersion, nativeClientProtocolV1)
	}
	if payload.Auth != "cookie" || payload.WebsocketPath != "/websocket" || payload.SignalingRole != "server-offer" {
		t.Fatalf("unexpected native signaling metadata: %+v", payload)
	}
	if strings.Join(payload.SupportedLayers, ",") != "low,medium,high" {
		t.Fatalf("supportedLayers=%v, want low/medium/high", payload.SupportedLayers)
	}
	if payload.NativeHints["mediaReadyEvent"] != "media_ready" {
		t.Fatalf("nativeHints=%v, want media_ready hint", payload.NativeHints)
	}
}

func TestIceTestEndpointRequiresSessionAndRedactsConfig(t *testing.T) {
	setupAuthTestEnv(t)

	req := httptest.NewRequest(http.MethodGet, "/ice-test", nil)
	recorder := httptest.NewRecorder()
	iceTestHandler(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected /ice-test without session to return 401, got %d", recorder.Code)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	req = httptest.NewRequest(http.MethodGet, "/ice-test", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	iceTestHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected /ice-test with session to return 200, got %d body %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{
		"ICE candidate test",
		"fetch('/client-config'",
		"RTCPeerConnection",
		"relay",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("/ice-test missing %q", want)
		}
	}
	for _, secretShape := range []string{
		"credential",
		"username",
		"iceServers",
	} {
		if strings.Contains(body, secretShape) {
			t.Fatalf("/ice-test should not inline RTC config field %q", secretShape)
		}
	}
}

func TestNativeClientConfigRequiresSession(t *testing.T) {
	setupAuthTestEnv(t)

	// Multi-room §5.3 hardening: the payload carries the full member roster,
	// which must not be readable unauthenticated once guest links put
	// outsiders on this origin.
	req := httptest.NewRequest(http.MethodGet, "/native/config", nil)
	recorder := httptest.NewRecorder()
	nativeClientConfigHandler(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected /native/config without session to return 401, got %d", recorder.Code)
	}
}

func TestNativeClientConfigPublishesRosterAndProtocol(t *testing.T) {
	setupAuthTestEnv(t)

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	req := httptest.NewRequest(http.MethodGet, "/native/config", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	nativeClientConfigHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected /native/config to return 200, got %d body %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		ProtocolVersion string `json:"protocolVersion"`
		Auth            struct {
			Mode      string `json:"mode"`
			LoginPath string `json:"loginPath"`
		} `json:"auth"`
		Room struct {
			ClientConfigPath string `json:"clientConfigPath"`
			WebsocketPath    string `json:"websocketPath"`
			MaxParticipants  int    `json:"maxParticipants"`
			Participants     []struct {
				Name  string `json:"name"`
				Email string `json:"email"`
			} `json:"participants"`
		} `json:"room"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal /native/config: %v", err)
	}
	if payload.ProtocolVersion != nativeClientProtocolV1 {
		t.Fatalf("protocolVersion=%q, want %q", payload.ProtocolVersion, nativeClientProtocolV1)
	}
	if payload.Auth.Mode != "cookie" || payload.Auth.LoginPath != "/auth/login" {
		t.Fatalf("auth config=%+v, want cookie login path", payload.Auth)
	}
	if payload.Room.ClientConfigPath != "/client-config" || payload.Room.WebsocketPath != "/websocket" {
		t.Fatalf("room config=%+v, want client config and websocket paths", payload.Room)
	}
	if payload.Room.MaxParticipants != configuredMeetingRoomCapacity() {
		t.Fatalf("maxParticipants=%d, want configured capacity", payload.Room.MaxParticipants)
	}
	if len(payload.Room.Participants) != len(meetingParticipantNames) {
		t.Fatalf("participants=%d, want roster size %d", len(payload.Room.Participants), len(meetingParticipantNames))
	}
	if payload.Room.Participants[0].Name != "Joel" || payload.Room.Participants[0].Email != "joel@shareability.com" {
		t.Fatalf("first participant=%+v, want Joel roster entry", payload.Room.Participants[0])
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
