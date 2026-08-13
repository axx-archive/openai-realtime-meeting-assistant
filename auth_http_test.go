package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		Email       string `json:"email"`
		Name        string `json:"name"`
		ShellAccess string `json:"shellAccess"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal /auth/me: %v", err)
	}
	if payload.Email != "aj@shareability.com" || payload.Name != "AJ" {
		t.Errorf("unexpected identity: %+v", payload)
	}
	if payload.ShellAccess != "full" {
		t.Errorf("AJ shellAccess=%q, want full", payload.ShellAccess)
	}

	memberCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	memberRequest := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	for _, cookie := range memberCookies {
		memberRequest.AddCookie(cookie)
	}
	memberRecorder := httptest.NewRecorder()
	authHandler(memberRecorder, memberRequest)
	var memberIdentity struct {
		ShellAccess string `json:"shellAccess"`
	}
	if memberRecorder.Code != http.StatusOK || json.Unmarshal(memberRecorder.Body.Bytes(), &memberIdentity) != nil || memberIdentity.ShellAccess != "core" {
		t.Fatalf("ordinary member shell projection=%d %s, want core", memberRecorder.Code, memberRecorder.Body.String())
	}
}

func TestNativeLoginReturnsSessionTokenAndBearerAuthWorks(t *testing.T) {
	setupAuthTestEnv(t)

	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"name":"AJ","password":"B0NFIRE!"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Bonfire-Client", "expo")
	recorder := httptest.NewRecorder()
	authHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("native login failed: status %d body %s", recorder.Code, recorder.Body.String())
	}
	var loginPayload struct {
		Email        string `json:"email"`
		Name         string `json:"name"`
		SessionToken string `json:"sessionToken"`
		ShellAccess  string `json:"shellAccess"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &loginPayload); err != nil {
		t.Fatalf("unmarshal native login: %v", err)
	}
	if loginPayload.SessionToken == "" {
		t.Fatal("expected sessionToken in native login response")
	}
	if loginPayload.Email != "aj@shareability.com" {
		t.Fatalf("unexpected email: %s", loginPayload.Email)
	}
	if loginPayload.ShellAccess != "full" {
		t.Fatalf("native AJ shellAccess=%q, want full", loginPayload.ShellAccess)
	}

	// Browser-style login must NOT leak the raw session token in JSON.
	web := postAuthJSON(t, "/auth/login", `{"name":"Tim","password":"B0NFIRE!"}`, nil)
	if web.Code != http.StatusOK {
		t.Fatalf("web login failed: %d", web.Code)
	}
	var webPayload map[string]any
	if err := json.Unmarshal(web.Body.Bytes(), &webPayload); err != nil {
		t.Fatalf("unmarshal web login: %v", err)
	}
	if _, ok := webPayload["sessionToken"]; ok {
		t.Fatal("web login must not include sessionToken")
	}

	// Bearer auth must resolve the same account as the cookie.
	meReq := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+loginPayload.SessionToken)
	meRec := httptest.NewRecorder()
	authHandler(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("expected /auth/me with Bearer to return 200, got %d body %s", meRec.Code, meRec.Body.String())
	}
	var mePayload struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal(meRec.Body.Bytes(), &mePayload); err != nil {
		t.Fatalf("unmarshal me: %v", err)
	}
	if mePayload.Name != "AJ" {
		t.Fatalf("unexpected me payload: %+v", mePayload)
	}

	// X-Bonfire-Session header is an alternate native transport.
	hdrReq := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	hdrReq.Header.Set("X-Bonfire-Session", loginPayload.SessionToken)
	hdrRec := httptest.NewRecorder()
	authHandler(hdrRec, hdrReq)
	if hdrRec.Code != http.StatusOK {
		t.Fatalf("expected /auth/me with X-Bonfire-Session to return 200, got %d", hdrRec.Code)
	}
}

func TestShellAccessRequiresExactCurrentOwnerOrAdminAuthority(t *testing.T) {
	setupAuthTestEnv(t)
	now := time.Now().UTC()
	user := accountStore().findUser("tim@shareability.com")
	if user == nil {
		t.Fatal("missing Tim fixture")
	}
	digest := sha256Hex([]byte(normalizeAccountEmail(user.Email)))
	person := organizationTestPerson("person-shell-admin", 'a', now.Add(-2*time.Hour))
	person.AccountSubjectDigest = digest
	organizations := NewOrganizationAuthorityService()
	if err := organizations.RegisterPerson(person); err != nil {
		t.Fatal(err)
	}
	organizationID, membershipID := "organization-shell-admin", "membership-shell-admin"
	organization := Organization{
		Header: organizationTestHeader(STRIDEGlobalPersonTenant, organizationID, 1, STRIDEContractOrganization, 'b', now.Add(-time.Hour)),
		Name:   "Shell Admin Studio", Slug: "shell-admin-studio", Status: "active", Discoverability: "private",
		CreatorPersonID: person.Header.ID, PolicyRevision: 1, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute),
	}
	membership := organizationTestMembership(membershipID, person.Header.ID, organizationID, "admin", 3, now.Add(-time.Hour), "membership-shell-owner")
	token := "shell-admin-session-token"
	hash := hashResetToken(token)
	active := ActiveOrganizationSession{
		Header:               organizationTestHeader(STRIDEGlobalPersonTenant, "active-session-shell-admin", 5, STRIDEContractActiveOrganizationSession, 'c', now.Add(-time.Hour)),
		SessionSubjectDigest: hash, PersonID: person.Header.ID, OrganizationID: organizationID, MembershipID: membershipID,
		MembershipRevision: membership.Header.Revision, SessionRevision: 5, Status: "active", BoundAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	}
	organizations.mu.Lock()
	organizations.organizations[organizationID] = organization
	organizations.memberships[membershipID] = membership
	organizations.sessions[hash] = active
	organizations.mu.Unlock()
	sessions := userSessionStore()
	sessions.mu.Lock()
	sessions.sessions[hash] = sessionRecord{
		Email: user.Email, Expires: active.ExpiresAt, PersonID: person.Header.ID, ActiveOrganizationID: organizationID,
		OrganizationMembershipID: membershipID, OrganizationMembershipRev: membership.Header.Revision,
		ActiveOrganizationSessionRev: active.SessionRevision, AccountSubjectDigest: digest, AuthorityGeneration: 5,
	}
	sessions.mu.Unlock()
	restore := InstallStrideE10TenantRuntimeConverter(&StrideE10TenantConverter{resolver: &strideE10MainTenantAuthorityResolver{sessions: sessions, organizations: organizations, now: func() time.Time { return now }}})
	t.Cleanup(restore)
	if got := shellAccessForSession(user, token); got != "full" {
		t.Fatalf("admin shellAccess=%q, want full", got)
	}
	organizations.mu.Lock()
	next := organizations.memberships[membershipID]
	next.Role = "member"
	organizations.memberships[membershipID] = next
	organizations.mu.Unlock()
	if got := shellAccessForSession(user, token); got != "core" {
		t.Fatalf("member shellAccess=%q, want core", got)
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

func TestNativeLogoutDestroysBearerSession(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))

	loginReq := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"name":"AJ","password":"B0NFIRE!"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("X-Bonfire-Client", "expo")
	loginRec := httptest.NewRecorder()
	authHandler(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("native login failed: %d %s", loginRec.Code, loginRec.Body.String())
	}
	var login struct {
		SessionToken string `json:"sessionToken"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &login); err != nil || login.SessionToken == "" {
		t.Fatalf("native login token missing: err=%v body=%s", err, loginRec.Body.String())
	}
	deviceToken := "ExponentPushToken[logout-device]"
	if err := upsertDeviceToken(deviceTokenRecord{TenantID: canonicalTenantID(), UserEmail: "aj@shareability.com", Token: deviceToken,
		Platform: "ios", SessionHash: hashResetToken(login.SessionToken)}); err != nil {
		t.Fatalf("register device: %v", err)
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/auth/logout", strings.NewReader(`{"deviceToken":"`+deviceToken+`"}`))
	logoutReq.Header.Set("Content-Type", "application/json")
	logoutReq.Header.Set("X-Bonfire-Client", "expo")
	logoutReq.Header.Set("Authorization", "Bearer "+login.SessionToken)
	logoutReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: login.SessionToken})
	logoutRec := httptest.NewRecorder()
	authHandler(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("native logout failed: %d %s", logoutRec.Code, logoutRec.Body.String())
	}
	if tokens := snapshotDeviceTokenStore().Tokens; len(tokens) != 0 {
		t.Fatalf("device binding survived authenticated logout: %+v", tokens)
	}
	clearedMatchingCookie := false
	for _, cookie := range logoutRec.Result().Cookies() {
		if cookie.Name == sessionCookieName && (cookie.MaxAge < 0 || cookie.Value == "") {
			clearedMatchingCookie = true
		}
	}
	if !clearedMatchingCookie {
		t.Fatal("native logout did not clear its matching ambient session cookie")
	}

	meReq := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+login.SessionToken)
	meRec := httptest.NewRecorder()
	authHandler(meRec, meReq)
	if meRec.Code != http.StatusUnauthorized {
		t.Fatalf("bearer session survived native logout: status=%d body=%s", meRec.Code, meRec.Body.String())
	}
}

func TestNativeLogoutPrefersBearerOverConflictingAmbientCookie(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))

	bearerToken, err := userSessionStore().create("aj@shareability.com")
	if err != nil {
		t.Fatal(err)
	}
	cookieToken, err := userSessionStore().create("tim@shareability.com")
	if err != nil {
		t.Fatal(err)
	}

	// Preserve the browser contract: without a reviewed native-client marker,
	// the HttpOnly cookie remains authoritative over an ambient bearer header.
	webRequest := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	webRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookieToken})
	webRequest.Header.Set("Authorization", "Bearer "+bearerToken)
	webUser := userFromRequest(webRequest)
	if webUser == nil || webUser.Email != "tim@shareability.com" {
		t.Fatalf("web authority=%+v, want cookie account", webUser)
	}

	request := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	request.Header.Set("X-Bonfire-Client", "expo")
	request.Header.Set("Authorization", "Bearer "+bearerToken)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookieToken})
	recorder := httptest.NewRecorder()
	authHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("native logout status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var receipt struct {
		OK                     bool   `json:"ok"`
		SessionRevoked         bool   `json:"sessionRevoked"`
		SessionAuthoritySource string `json:"sessionAuthoritySource"`
		SessionAuthorityHash   string `json:"sessionAuthorityHash"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if !receipt.OK || !receipt.SessionRevoked || receipt.SessionAuthoritySource != string(sessionAuthorityBearer) || receipt.SessionAuthorityHash != hashResetToken(bearerToken) {
		t.Fatalf("logout receipt=%+v, want exact bearer revocation proof", receipt)
	}
	if strings.Contains(recorder.Body.String(), bearerToken) || strings.Contains(recorder.Body.String(), cookieToken) {
		t.Fatal("logout receipt exposed a raw session token")
	}
	if _, ok := userSessionStore().lookup(bearerToken); ok {
		t.Fatal("selected native bearer session survived logout")
	}
	if email, ok := userSessionStore().lookup(cookieToken); !ok || email != "tim@shareability.com" {
		t.Fatalf("unrelated ambient cookie session was destroyed: email=%q ok=%v", email, ok)
	}
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			t.Fatalf("native bearer logout mutated unrelated ambient cookie: %+v", cookie)
		}
	}

	stillLive := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	stillLive.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookieToken})
	stillLiveRecorder := httptest.NewRecorder()
	authHandler(stillLiveRecorder, stillLive)
	if stillLiveRecorder.Code != http.StatusOK || !strings.Contains(stillLiveRecorder.Body.String(), "tim@shareability.com") {
		t.Fatalf("ambient web session no longer live: status=%d body=%s", stillLiveRecorder.Code, stillLiveRecorder.Body.String())
	}

	revoked := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	revoked.Header.Set("X-Bonfire-Client", "expo")
	revoked.Header.Set("Authorization", "Bearer "+bearerToken)
	revokedRecorder := httptest.NewRecorder()
	authHandler(revokedRecorder, revoked)
	if revokedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("revoked native bearer remained live: status=%d body=%s", revokedRecorder.Code, revokedRecorder.Body.String())
	}
}

func TestNativeLogoutReportsRevocationSuccessWhenDeviceCleanupFails(t *testing.T) {
	setupAuthTestEnv(t)
	devicePath := filepath.Join(t.TempDir(), "devices.json")
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", devicePath)
	sessionToken := upsertLiveDeviceTokenForTest(t, "aj@shareability.com", "ExponentPushToken[cleanup-failure]")

	// Turn the store target itself into a directory after registration so the
	// exact-binding rewrite fails deterministically without affecting sessions.
	if err := os.Remove(devicePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(devicePath, 0o700); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/auth/logout", strings.NewReader(`{"deviceToken":"ExponentPushToken[cleanup-failure]"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+sessionToken)
	recorder := httptest.NewRecorder()
	authHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("logout status=%d body=%s, session revocation must not become ambiguous", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		OK                   bool `json:"ok"`
		SessionRevoked       bool `json:"sessionRevoked"`
		DeviceCleanupPending bool `json:"deviceCleanupPending"`
		DeviceBindingRemoved bool `json:"deviceBindingRemoved"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || !payload.SessionRevoked || !payload.DeviceCleanupPending || payload.DeviceBindingRemoved {
		t.Fatalf("logout receipt=%v, want revoked session plus explicit deferred cleanup", payload)
	}
	if _, ok := userSessionStore().lookup(sessionToken); ok {
		t.Fatal("session survived a logout whose device cleanup failed")
	}
}

func TestAppleAppSiteAssociationPublishesPasskeyAppID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/.well-known/apple-app-site-association", nil)
	recorder := httptest.NewRecorder()
	appleAppSiteAssociationHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("AASA status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("AASA content type=%q", contentType)
	}
	var payload struct {
		WebCredentials struct {
			Apps []string `json:"apps"`
		} `json:"webcredentials"`
		AppLinks struct {
			Details []struct {
				AppIDs     []string         `json:"appIDs"`
				Components []map[string]any `json:"components"`
			} `json:"details"`
		} `json:"applinks"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode AASA: %v", err)
	}
	if len(payload.WebCredentials.Apps) != 1 || payload.WebCredentials.Apps[0] != "73PT36P58W.xyz.thebonfire.app" {
		t.Fatalf("unexpected AASA apps: %v", payload.WebCredentials.Apps)
	}
	if len(payload.AppLinks.Details) != 1 || len(payload.AppLinks.Details[0].Components) != 1 {
		t.Fatalf("unexpected AASA applinks: %+v", payload.AppLinks.Details)
	}
	if got := payload.AppLinks.Details[0].AppIDs; len(got) != 1 || got[0] != "73PT36P58W.xyz.thebonfire.app" {
		t.Fatalf("unexpected applink app IDs: %v", got)
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

func TestNativeChangePasswordRotatesBearerSession(t *testing.T) {
	setupAuthTestEnv(t)

	loginReq := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"name":"Tyler","password":"B0NFIRE!"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("X-Bonfire-Client", "expo")
	loginRec := httptest.NewRecorder()
	authHandler(loginRec, loginReq)
	var login struct {
		SessionToken string `json:"sessionToken"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &login); err != nil || login.SessionToken == "" {
		t.Fatalf("native login failed: err=%v body=%s", err, loginRec.Body.String())
	}

	changeReq := httptest.NewRequest(http.MethodPost, "/auth/change-password", strings.NewReader(`{"currentPassword":"B0NFIRE!","newPassword":"freshpass99"}`))
	changeReq.Header.Set("Content-Type", "application/json")
	changeReq.Header.Set("X-Bonfire-Client", "expo")
	changeReq.Header.Set("Authorization", "Bearer "+login.SessionToken)
	changeRec := httptest.NewRecorder()
	authHandler(changeRec, changeReq)
	if changeRec.Code != http.StatusOK {
		t.Fatalf("native password change failed: %d %s", changeRec.Code, changeRec.Body.String())
	}
	var changed struct {
		SessionToken string `json:"sessionToken"`
	}
	if err := json.Unmarshal(changeRec.Body.Bytes(), &changed); err != nil || changed.SessionToken == "" {
		t.Fatalf("rotated token missing: err=%v body=%s", err, changeRec.Body.String())
	}
	if changed.SessionToken == login.SessionToken {
		t.Fatal("password change must rotate the native token")
	}

	for token, want := range map[string]int{
		login.SessionToken:   http.StatusUnauthorized,
		changed.SessionToken: http.StatusOK,
	} {
		meReq := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
		meReq.Header.Set("Authorization", "Bearer "+token)
		meRec := httptest.NewRecorder()
		authHandler(meRec, meReq)
		if meRec.Code != want {
			t.Fatalf("/auth/me token status=%d want=%d body=%s", meRec.Code, want, meRec.Body.String())
		}
	}
}

func TestNativeWebSessionBridgeSetsHttpOnlyCookieAndSafeRedirect(t *testing.T) {
	setupAuthTestEnv(t)

	loginReq := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"name":"AJ","password":"B0NFIRE!"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("X-Bonfire-Client", "expo")
	loginRec := httptest.NewRecorder()
	authHandler(loginRec, loginReq)
	var login struct {
		SessionToken string `json:"sessionToken"`
	}
	_ = json.Unmarshal(loginRec.Body.Bytes(), &login)

	req := httptest.NewRequest(http.MethodGet, "/auth/native-web-session?path=%2F%3Ftool%3Dchat", nil)
	req.Header.Set("X-Bonfire-Client", "expo")
	req.Header.Set("Authorization", "Bearer "+login.SessionToken)
	recorder := httptest.NewRecorder()
	authHandler(recorder, req)
	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "/?tool=chat" {
		t.Fatalf("bridge redirect status=%d location=%q", recorder.Code, recorder.Header().Get("Location"))
	}
	foundCookie := false
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			foundCookie = true
			if !cookie.HttpOnly || cookie.Value != login.SessionToken {
				t.Fatalf("bridge cookie not secure: %+v", cookie)
			}
		}
	}
	if !foundCookie {
		t.Fatal("bridge did not set the session cookie")
	}

	bad := httptest.NewRequest(http.MethodGet, "/auth/native-web-session?path=https%3A%2F%2Fevil.example", nil)
	bad.Header.Set("X-Bonfire-Client", "expo")
	bad.Header.Set("Authorization", "Bearer "+login.SessionToken)
	badRec := httptest.NewRecorder()
	authHandler(badRec, bad)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("external bridge path status=%d want=400", badRec.Code)
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

// TestAuthThemePreferenceRoundTrip pins the account-level theme persistence
// (founder call 2026-07-10): POST /auth/theme stores light|dark|system on the
// user record, /auth/me carries it back for the session-bootstrap re-apply,
// the value survives a store reload, and the endpoint is auth- and
// value-gated.
func TestAuthThemePreferenceRoundTrip(t *testing.T) {
	setupAuthTestEnv(t)

	// unauth → 401
	recorder := postAuthJSON(t, "/auth/theme", `{"theme":"dark"}`, nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauth, got %d", recorder.Code)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")

	// bad value → 400
	recorder = postAuthJSON(t, "/auth/theme", `{"theme":"blue"}`, cookies)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad theme, got %d", recorder.Code)
	}

	// happy path stores + echoes via identityPayload
	recorder = postAuthJSON(t, "/auth/theme", `{"theme":"dark"}`, cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", recorder.Code, recorder.Body.String())
	}
	var saved struct {
		ThemePref string `json:"themePref"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &saved); err != nil {
		t.Fatalf("unmarshal /auth/theme: %v", err)
	}
	if saved.ThemePref != "dark" {
		t.Fatalf("themePref = %q, want dark", saved.ThemePref)
	}

	// /auth/me carries it
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	me := httptest.NewRecorder()
	authHandler(me, req)
	if me.Code != http.StatusOK {
		t.Fatalf("/auth/me status %d", me.Code)
	}
	var identity struct {
		ThemePref string `json:"themePref"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &identity); err != nil {
		t.Fatalf("unmarshal /auth/me: %v", err)
	}
	if identity.ThemePref != "dark" {
		t.Fatalf("/auth/me themePref = %q, want dark", identity.ThemePref)
	}

	// survives a store reload (fresh store instance over the same file), and
	// other record fields are intact
	reloaded, err := newUserAccountStore(usersFilePath())
	if err != nil {
		t.Fatalf("reload account store: %v", err)
	}
	user := reloaded.findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("aj account missing after reload")
	}
	if user.ThemePref != "dark" {
		t.Fatalf("reloaded themePref = %q, want dark", user.ThemePref)
	}
	if user.Name != "AJ" || len(user.PasswordHash) == 0 {
		t.Fatalf("reload corrupted other fields: name=%q hashLen=%d", user.Name, len(user.PasswordHash))
	}

	// "system" is a legal stored value
	recorder = postAuthJSON(t, "/auth/theme", `{"theme":"system"}`, cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200 for system, got %d", recorder.Code)
	}
}
