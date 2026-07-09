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

// mintGuestRoomAndToken creates a guest-enabled room and returns it with a
// live link token, straight from the store (the HTTP mint path has its own
// coverage in rooms_test.go).
func mintGuestRoomAndToken(t *testing.T) (roomRecord, string) {
	t.Helper()
	room, err := appRoomStore().create("Guest Suite", "", "aj@shareability.com", false)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	token, _, err := appRoomStore().mintGuestLink(room.ID, "", "aj@shareability.com", 0)
	if err != nil {
		t.Fatalf("mint guest link: %v", err)
	}
	return room, token
}

func postGuestJoin(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/guest/join", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	guestJoinHandler(recorder, req)
	return recorder
}

func guestCookieFrom(t *testing.T, recorder *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == guestCookieName {
			return cookie
		}
	}
	t.Fatalf("expected a %s cookie, got %v", guestCookieName, recorder.Result().Cookies())
	return nil
}

/* ---------- session kind + resolver hardening ---------- */

func TestLegacySessionRowsStillResolveAsUsers(t *testing.T) {
	// No-logout pin (§9.4): rows persisted BEFORE the Kind field existed must
	// keep resolving as member sessions after a deploy.
	dir := t.TempDir()
	t.Setenv("BONFIRE_USERS_PATH", filepath.Join(dir, "users.json"))
	sessionsPath := filepath.Join(dir, "sessions.json")
	t.Setenv("BONFIRE_SESSIONS_PATH", sessionsPath)
	resetAuthRateLimitersForTest()

	token := strings.Repeat("42", 32)
	legacy := fmt.Sprintf(`{%q: {"email":"aj@shareability.com","expires":%q}}`,
		hashResetToken(token), time.Now().Add(time.Hour).Format(time.RFC3339Nano))
	if err := os.WriteFile(sessionsPath, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy sessions.json: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	user := userFromRequest(req)
	if user == nil || user.Email != "aj@shareability.com" {
		t.Fatalf("legacy session row must still resolve to its account, got %+v", user)
	}
}

func TestGuestSessionNeverSatisfiesUserFromRequest(t *testing.T) {
	setupRoomsTestEnv(t)

	token, err := userSessionStore().createGuest(officeRoomID, "Sam")
	if err != nil {
		t.Fatalf("create guest session: %v", err)
	}

	// The explicit Kind=="guest" pin: even with the guest token planted in
	// the MEMBER cookie slot, userFromRequest must return nil — the implicit
	// empty-email fallthrough is not the enforcement point.
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	if user := userFromRequest(req); user != nil {
		t.Fatalf("a guest session in the member cookie must resolve to nil, got %+v", user)
	}

	// The guest cookie satisfies guestFromRequest and only that.
	req = httptest.NewRequest(http.MethodGet, "/guest/me", nil)
	req.AddCookie(&http.Cookie{Name: guestCookieName, Value: token})
	principal := guestFromRequest(req)
	if principal == nil || principal.RoomID != officeRoomID || principal.Name != "Sam" {
		t.Fatalf("expected guest principal for the guest cookie, got %+v", principal)
	}
	if user := userFromRequest(req); user != nil {
		t.Fatalf("the guest cookie must never satisfy userFromRequest, got %+v", user)
	}

	// And the inverse: a MEMBER token in the guest cookie slot resolves to no
	// guest principal.
	cookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	req = httptest.NewRequest(http.MethodGet, "/guest/me", nil)
	req.AddCookie(&http.Cookie{Name: guestCookieName, Value: cookies[0].Value})
	if principal := guestFromRequest(req); principal != nil {
		t.Fatalf("a member session in the guest cookie must resolve to nil, got %+v", principal)
	}
}

func TestGuestSessionsPersistAndExpire(t *testing.T) {
	setupRoomsTestEnv(t)
	sessionsPath := sessionsFilePath()

	token, err := userSessionStore().createGuest("room-abc123", "Priya")
	if err != nil {
		t.Fatalf("create guest session: %v", err)
	}

	reloaded := newSessionStore(sessionsPath)
	record, ok := reloaded.lookupRecord(token)
	if !ok || record.Kind != sessionKindGuest || record.RoomID != "room-abc123" || record.GuestName != "Priya" {
		t.Fatalf("guest session must survive reload with its kind/room/name, got %+v ok=%v", record, ok)
	}
	if record.Email != "" {
		t.Fatalf("guest sessions carry no account email, got %q", record.Email)
	}
	// 12h TTL (§3.2), not the 30d member TTL.
	if record.Expires.After(time.Now().Add(guestSessionTTL + time.Minute)) {
		t.Fatalf("guest session TTL must be 12h, got expiry %v", record.Expires)
	}
}

/* ---------- POST /guest/join ---------- */

func TestGuestJoinMintsGuestSessionCookie(t *testing.T) {
	setupRoomsTestEnv(t)
	room, token := mintGuestRoomAndToken(t)

	// Regression on the fixed roster-collision predicate (§5.2): a
	// legitimate non-roster name must PASS.
	recorder := postGuestJoin(t, fmt.Sprintf(`{"token":%q,"name":"  Priya   Chen "}`, token))
	if recorder.Code != http.StatusOK {
		t.Fatalf("guest join = %d body %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		RoomID    string `json:"roomId"`
		RoomName  string `json:"roomName"`
		GuestName string `json:"guestName"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal join response: %v", err)
	}
	if payload.RoomID != room.ID || payload.RoomName != "Guest Suite" || payload.GuestName != "Priya Chen" {
		t.Fatalf("unexpected join payload: %+v", payload)
	}

	cookie := guestCookieFrom(t, recorder)
	if !cookie.HttpOnly {
		t.Error("guest cookie must be HttpOnly")
	}
	if cookie.MaxAge != int(guestSessionTTL/time.Second) {
		t.Errorf("guest cookie MaxAge = %d, want 12h", cookie.MaxAge)
	}

	// The minted session is a guest session bound to THAT room.
	req := httptest.NewRequest(http.MethodGet, "/guest/me", nil)
	req.AddCookie(cookie)
	principal := guestFromRequest(req)
	if principal == nil || principal.RoomID != room.ID || principal.Name != "Priya Chen" {
		t.Fatalf("expected guest principal bound to %s, got %+v", room.ID, principal)
	}
}

func TestGuestJoinRejectsRosterCollisionNames(t *testing.T) {
	setupRoomsTestEnv(t)
	_, token := mintGuestRoomAndToken(t)

	for _, name := range []string{"AJ", "aj", "  erick ", "Tyler"} {
		recorder := postGuestJoin(t, fmt.Sprintf(`{"token":%q,"name":%q}`, token, name))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("guest name %q = %d, want 400 (roster impersonation)", name, recorder.Code)
		}
	}
	// Non-roster names sharing a prefix must not be over-matched.
	recorder := postGuestJoin(t, fmt.Sprintf(`{"token":%q,"name":"AJane"}`, token))
	if recorder.Code != http.StatusOK {
		t.Fatalf("guest name AJane = %d, want 200", recorder.Code)
	}
}

func TestGuestJoinSanitizesNames(t *testing.T) {
	setupRoomsTestEnv(t)
	_, token := mintGuestRoomAndToken(t)

	// JSON \u escapes decode to real control/zero-width runes; the sanitizer
	// must strip them and collapse the leftover whitespace.
	recorder := postGuestJoin(t, fmt.Sprintf(`{"token":%q,"name":"Sam\u0000\u0007  Reyes\u200b"}`, token))
	if recorder.Code != http.StatusOK {
		t.Fatalf("sanitizable name = %d body %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		GuestName string `json:"guestName"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal join response: %v", err)
	}
	if payload.GuestName != "Sam Reyes" {
		t.Fatalf("control/unprintable runes must be stripped, got %q", payload.GuestName)
	}

	if code := postGuestJoin(t, fmt.Sprintf(`{"token":%q,"name":"\u0001\u0002"}`, token)).Code; code != http.StatusBadRequest {
		t.Fatalf("all-control name = %d, want 400", code)
	}
	if code := postGuestJoin(t, fmt.Sprintf(`{"token":%q,"name":""}`, token)).Code; code != http.StatusBadRequest {
		t.Fatalf("empty name = %d, want 400", code)
	}
	long := strings.Repeat("a", maxGuestNameRunes+1)
	if code := postGuestJoin(t, fmt.Sprintf(`{"token":%q,"name":%q}`, token, long)).Code; code != http.StatusBadRequest {
		t.Fatalf("41-rune name = %d, want 400", code)
	}
}

func TestGuestJoinRejectsDeadTokens(t *testing.T) {
	setupRoomsTestEnv(t)
	room, token := mintGuestRoomAndToken(t)

	if code := postGuestJoin(t, `{"token":"not-a-token","name":"Priya"}`).Code; code != http.StatusForbidden {
		t.Fatalf("malformed token = %d, want 403", code)
	}
	if code := postGuestJoin(t, fmt.Sprintf(`{"token":%q,"name":"Priya"}`, strings.Repeat("0", 64))).Code; code != http.StatusForbidden {
		t.Fatalf("unknown token = %d, want 403", code)
	}

	links, err := appRoomStore().listGuestLinks(room.ID)
	if err != nil || len(links) != 1 {
		t.Fatalf("expected one link, got %v err=%v", links, err)
	}
	if err := appRoomStore().revokeGuestLink(room.ID, links[0].ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if code := postGuestJoin(t, fmt.Sprintf(`{"token":%q,"name":"Priya"}`, token)).Code; code != http.StatusForbidden {
		t.Fatalf("revoked token = %d, want 403 (liveness re-checked per use)", code)
	}
}

func TestGuestJoinRateLimited(t *testing.T) {
	setupRoomsTestEnv(t)

	var last *httptest.ResponseRecorder
	for i := 0; i < loginAttemptLimit+1; i++ {
		last = postGuestJoin(t, fmt.Sprintf(`{"token":%q,"name":"Priya"}`, strings.Repeat("0", 64)))
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after %d attempts, got %d", loginAttemptLimit+1, last.Code)
	}
}

func TestGuestJoinCrossOriginRejected(t *testing.T) {
	setupRoomsTestEnv(t)
	_, token := mintGuestRoomAndToken(t)

	req := httptest.NewRequest(http.MethodPost, "/guest/join", strings.NewReader(fmt.Sprintf(`{"token":%q,"name":"Priya"}`, token)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()
	guestJoinHandler(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin guest join = %d, want 403", recorder.Code)
	}
}

/* ---------- POST /guest/lookup (rooms-UX RW2) ---------- */

func postGuestLookup(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/guest/lookup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	guestLookupHandler(recorder, req)
	return recorder
}

// The lookup names the room for the gate and MINTS NOTHING: no Set-Cookie, no
// session row, and the token stays redeemable afterwards (lookup never
// consumes the capability).
func TestGuestLookupNamesRoomWithoutMinting(t *testing.T) {
	setupRoomsTestEnv(t)
	room, token := mintGuestRoomAndToken(t)

	sessionsBefore, _ := os.ReadFile(sessionsFilePath())
	recorder := postGuestLookup(t, fmt.Sprintf(`{"token":%q}`, token))
	if recorder.Code != http.StatusOK {
		t.Fatalf("guest lookup = %d body %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		RoomName         string `json:"roomName"`
		Live             bool   `json:"live"`
		ParticipantCount int    `json:"participantCount"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal lookup response: %v", err)
	}
	if payload.RoomName != "Guest Suite" || payload.Live || payload.ParticipantCount != 0 {
		t.Fatalf("unexpected lookup payload: %+v", payload)
	}
	// The lookup names the room but hands out NO identifier a join wouldn't:
	// the payload carries no roomId (the join response is the id's only door).
	if strings.Contains(recorder.Body.String(), "roomId") {
		t.Fatalf("lookup must not carry the room id, got %s", recorder.Body.String())
	}
	if cookies := recorder.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("lookup must set no cookie, got %v", cookies)
	}
	sessionsAfter, _ := os.ReadFile(sessionsFilePath())
	if string(sessionsBefore) != string(sessionsAfter) {
		t.Fatal("lookup must not mint or mutate any session")
	}
	if _, ok := appRoomStore().redeemGuestToken(token); !ok {
		t.Fatal("lookup must not consume the token")
	}
	if redeemed, ok := appRoomStore().redeemGuestToken(token); !ok || redeemed.ID != room.ID {
		t.Fatal("token must stay bound to its room after lookup")
	}
}

// Every dead shape — malformed, well-formed-unknown, revoked — answers the
// SAME uniform 403 body: no oracle distinguishing "not a token" from "was a
// token", matching /guest/join's constant-time posture.
func TestGuestLookupRejectsDeadTokensUniformly(t *testing.T) {
	setupRoomsTestEnv(t)
	room, token := mintGuestRoomAndToken(t)

	links, err := appRoomStore().listGuestLinks(room.ID)
	if err != nil || len(links) != 1 {
		t.Fatalf("expected one link, got %v err=%v", links, err)
	}
	if err := appRoomStore().revokeGuestLink(room.ID, links[0].ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	responses := map[string]*httptest.ResponseRecorder{
		"malformed": postGuestLookup(t, `{"token":"not-a-token"}`),
		"unknown":   postGuestLookup(t, fmt.Sprintf(`{"token":%q}`, strings.Repeat("0", 64))),
		"revoked":   postGuestLookup(t, fmt.Sprintf(`{"token":%q}`, token)),
	}
	var wantBody string
	for name, recorder := range responses {
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s token = %d, want 403", name, recorder.Code)
		}
		if wantBody == "" {
			wantBody = recorder.Body.String()
		}
		if recorder.Body.String() != wantBody {
			t.Fatalf("%s token body %q differs from %q — dead tokens must be indistinguishable", name, recorder.Body.String(), wantBody)
		}
	}
}

// The lookup rides the SAME guestjoin budget as /guest/join, so it cannot be
// used as a cheaper brute-force channel — and exhausting it via lookup also
// closes the join door for that IP.
func TestGuestLookupRateLimitedUnderGuestJoinBudget(t *testing.T) {
	setupRoomsTestEnv(t)

	var last *httptest.ResponseRecorder
	for i := 0; i < loginAttemptLimit+1; i++ {
		last = postGuestLookup(t, fmt.Sprintf(`{"token":%q}`, strings.Repeat("0", 64)))
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after %d lookups, got %d", loginAttemptLimit+1, last.Code)
	}
	if code := postGuestJoin(t, fmt.Sprintf(`{"token":%q,"name":"Priya"}`, strings.Repeat("0", 64))).Code; code != http.StatusTooManyRequests {
		t.Fatalf("guest join after lookup exhaustion = %d, want 429 (shared budget)", code)
	}
}

func TestGuestLookupCrossOriginAndMethodRejected(t *testing.T) {
	setupRoomsTestEnv(t)
	_, token := mintGuestRoomAndToken(t)

	req := httptest.NewRequest(http.MethodPost, "/guest/lookup", strings.NewReader(fmt.Sprintf(`{"token":%q}`, token)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()
	guestLookupHandler(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin guest lookup = %d, want 403", recorder.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/guest/lookup", nil)
	recorder = httptest.NewRecorder()
	guestLookupHandler(recorder, req)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /guest/lookup = %d, want 405", recorder.Code)
	}
}

/* ---------- GET /guest/me ---------- */

func TestGuestMeResumesSession(t *testing.T) {
	setupRoomsTestEnv(t)
	room, token := mintGuestRoomAndToken(t)

	recorder := postGuestJoin(t, fmt.Sprintf(`{"token":%q,"name":"Priya"}`, token))
	if recorder.Code != http.StatusOK {
		t.Fatalf("guest join = %d", recorder.Code)
	}
	cookie := guestCookieFrom(t, recorder)

	// Without the cookie: no resume.
	req := httptest.NewRequest(http.MethodGet, "/guest/me", nil)
	rec := httptest.NewRecorder()
	guestMeHandler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /guest/me without cookie = %d, want 401", rec.Code)
	}

	// With the cookie: the deploy-refresh resume payload (§5.2) — no link
	// token needed.
	req = httptest.NewRequest(http.MethodGet, "/guest/me", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	guestMeHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /guest/me = %d body %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		RoomID           string `json:"roomId"`
		RoomName         string `json:"roomName"`
		GuestName        string `json:"guestName"`
		Live             bool   `json:"live"`
		ParticipantCount int    `json:"participantCount"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal /guest/me: %v", err)
	}
	if payload.RoomID != room.ID || payload.RoomName != "Guest Suite" || payload.GuestName != "Priya" {
		t.Fatalf("unexpected /guest/me payload: %+v", payload)
	}

	// An archived room fails closed: the seat could not be re-admitted.
	if err := appRoomStore().archive(room.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "/guest/me", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	guestMeHandler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /guest/me for archived room = %d, want 401", rec.Code)
	}
}

/* ---------- sampled protected-route sweep ---------- */

func TestGuestSessionRejectedAcrossProtectedRoutes(t *testing.T) {
	setupRoomsTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	_, token := mintGuestRoomAndToken(t)
	recorder := postGuestJoin(t, fmt.Sprintf(`{"token":%q,"name":"Priya"}`, token))
	if recorder.Code != http.StatusOK {
		t.Fatalf("guest join = %d", recorder.Code)
	}
	guestCookie := guestCookieFrom(t, recorder)

	// Sampled sweep (§5.3; W7 walks the full mux): a minted guest session —
	// presented in BOTH cookie slots at once — must bounce off every
	// member-gated endpoint. The exhaustive fail-closed walk lands in W7.
	routes := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		path    string
	}{
		{"auth me", authHandler, http.MethodGet, "/auth/me"},
		{"client config", clientConfigHandler, http.MethodGet, "/client-config"},
		{"native config", nativeClientConfigHandler, http.MethodGet, "/native/config"},
		{"board", assistantBoardHandler, http.MethodGet, "/assistant/board"},
		{"meetings", assistantMeetingsHandler, http.MethodGet, "/assistant/meetings"},
		{"artifacts", artifactsHandler, http.MethodGet, "/artifacts"},
		{"rooms", roomsHandler, http.MethodGet, "/rooms"},
		{"room archive", roomActionHandler, http.MethodPost, "/rooms/office/archive"},
	}
	for _, route := range routes {
		req := httptest.NewRequest(route.method, route.path, strings.NewReader(""))
		req.AddCookie(guestCookie)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: guestCookie.Value})
		rec := httptest.NewRecorder()
		route.handler(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: guest session got %d, want 401", route.name, rec.Code)
		}
	}

	// /participants stays public but a guest session gets only the legacy
	// office seat count — never the roster snapshot (§5.3).
	req := httptest.NewRequest(http.MethodGet, "/participants", nil)
	req.AddCookie(guestCookie)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: guestCookie.Value})
	rec := httptest.NewRecorder()
	participantsHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/participants for guest = %d, want the signed-out summary", rec.Code)
	}
	var summary map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("unmarshal presence summary: %v", err)
	}
	if _, ok := summary["participants"]; ok {
		t.Fatalf("guest must not receive the roster snapshot, got %s", rec.Body.String())
	}
}
