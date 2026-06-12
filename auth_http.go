package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookieName = "bonfire_session"
	sessionTTL        = 30 * 24 * time.Hour

	loginAttemptLimit  = 12
	loginAttemptWindow = 5 * time.Minute
)

type sessionRecord struct {
	Email   string    `json:"email"`
	Expires time.Time `json:"expires"`
}

// sessionStore keeps SHA-256 hashes of session tokens (never the tokens
// themselves) in a JSON file next to the other room state, so a leaked data
// directory does not hand out live sessions.
type sessionStore struct {
	mu       sync.Mutex
	path     string
	sessions map[string]sessionRecord
}

func newSessionStore(path string) *sessionStore {
	store := &sessionStore{path: path, sessions: map[string]sessionRecord{}}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &store.sessions); err != nil {
			log.Errorf("Ignoring malformed session store at %s: %v", path, err)
			store.sessions = map[string]sessionRecord{}
		}
	}
	return store
}

func (s *sessionStore) persistLocked() {
	for key, record := range s.sessions {
		if time.Now().After(record.Expires) {
			delete(s.sessions, key)
		}
	}
	raw, err := json.MarshalIndent(s.sessions, "", "  ")
	if err != nil {
		log.Errorf("Failed to encode session store: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		log.Errorf("Failed to create session store directory: %v", err)
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		log.Errorf("Failed to persist session store: %v", err)
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		log.Errorf("Failed to persist session store: %v", err)
	}
}

func (s *sessionStore) create(email string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[hashResetToken(token)] = sessionRecord{
		Email:   normalizeAccountEmail(email),
		Expires: time.Now().Add(sessionTTL),
	}
	s.persistLocked()
	return token, nil
}

func (s *sessionStore) lookup(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.sessions[hashResetToken(token)]
	if !ok || time.Now().After(record.Expires) {
		return "", false
	}
	return record.Email, true
}

func (s *sessionStore) destroy(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, hashResetToken(token))
	s.persistLocked()
}

func (s *sessionStore) destroyAllForEmail(email string) {
	email = normalizeAccountEmail(email)
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, record := range s.sessions {
		if record.Email == email {
			delete(s.sessions, key)
		}
	}
	s.persistLocked()
}

var (
	sessionStoreMu    sync.Mutex
	sessionStoreCache = map[string]*sessionStore{}
)

func sessionsFilePath() string {
	if path := strings.TrimSpace(os.Getenv("BONFIRE_SESSIONS_PATH")); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "sessions.json")
}

func userSessionStore() *sessionStore {
	path := sessionsFilePath()
	sessionStoreMu.Lock()
	defer sessionStoreMu.Unlock()
	if store, ok := sessionStoreCache[path]; ok {
		return store
	}
	store := newSessionStore(path)
	sessionStoreCache[path] = store
	return store
}

// userFromRequest resolves the signed-in account from the session cookie, or
// nil when the request carries no live session.
func userFromRequest(r *http.Request) *userAccount {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil
	}
	email, ok := userSessionStore().lookup(cookie.Value)
	if !ok {
		return nil
	}
	return accountStore().findUser(email)
}

func requestIsSecure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// --- rate limiting -----------------------------------------------------------

type attemptWindow struct {
	count   int
	started time.Time
}

var (
	authRateMu       sync.Mutex
	authRateAttempts = map[string]attemptWindow{}
)

func clientIPForRateLimit(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	// Honor X-Forwarded-For only when the direct peer is the local reverse
	// proxy (Caddy on the compose network); a remote client setting the header
	// itself must not be able to mint fresh rate-limit identities.
	remote := net.ParseIP(host)
	if remote != nil && (remote.IsLoopback() || remote.IsPrivate()) {
		if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); forwarded != "" {
			return forwarded
		}
	}
	return host
}

// maxTrackedAttemptWindows bounds the limiter map; past it, expired windows
// are swept before admitting new keys so hostile traffic cannot grow memory
// without bound.
const maxTrackedAttemptWindows = 10000

func authAttemptAllowed(scope string, r *http.Request) bool {
	return authAttemptAllowedForKeys(scope + "|" + clientIPForRateLimit(r))
}

func authAttemptAllowedForKeys(keys ...string) bool {
	authRateMu.Lock()
	defer authRateMu.Unlock()

	if len(authRateAttempts) > maxTrackedAttemptWindows {
		for key, window := range authRateAttempts {
			if time.Since(window.started) > loginAttemptWindow {
				delete(authRateAttempts, key)
			}
		}
	}

	allowed := true
	for _, key := range keys {
		window, ok := authRateAttempts[key]
		if !ok || time.Since(window.started) > loginAttemptWindow {
			window = attemptWindow{started: time.Now()}
		}
		window.count++
		authRateAttempts[key] = window
		if window.count > loginAttemptLimit {
			allowed = false
		}
	}
	return allowed
}

func clearAuthAttempts(keys ...string) {
	authRateMu.Lock()
	defer authRateMu.Unlock()
	for _, key := range keys {
		delete(authRateAttempts, key)
	}
}

func resetAuthRateLimitersForTest() {
	authRateMu.Lock()
	defer authRateMu.Unlock()
	authRateAttempts = map[string]attemptWindow{}
}

// --- handlers ----------------------------------------------------------------

func writeAuthJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload != nil {
		_ = json.NewEncoder(w).Encode(payload)
	}
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	writeAuthJSON(w, status, map[string]string{"error": message})
}

func decodeAuthBody(r *http.Request, dest any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 16*1024))
	if err := decoder.Decode(dest); err != nil {
		return errors.New("could not read request body")
	}
	return nil
}

func authHandler(w http.ResponseWriter, r *http.Request) {
	// Session cookies authenticate every /auth POST, so reject cross-origin
	// callers outright; same-origin browsers and non-browser clients (no
	// Origin header) pass through.
	if r.Method == http.MethodPost && !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}

	switch {
	case r.URL.Path == "/auth/login" && r.Method == http.MethodPost:
		handleAuthLogin(w, r)
	case r.URL.Path == "/auth/logout" && r.Method == http.MethodPost:
		handleAuthLogout(w, r)
	case r.URL.Path == "/auth/me" && r.Method == http.MethodGet:
		handleAuthMe(w, r)
	case r.URL.Path == "/auth/change-password" && r.Method == http.MethodPost:
		handleAuthChangePassword(w, r)
	case r.URL.Path == "/auth/passkey/register/begin" && r.Method == http.MethodPost:
		handlePasskeyRegisterBegin(w, r)
	case r.URL.Path == "/auth/passkey/register/finish" && r.Method == http.MethodPost:
		handlePasskeyRegisterFinish(w, r)
	case r.URL.Path == "/auth/passkey/login/begin" && r.Method == http.MethodPost:
		handlePasskeyLoginBegin(w, r)
	case r.URL.Path == "/auth/passkey/login/finish" && r.Method == http.MethodPost:
		handlePasskeyLoginFinish(w, r)
	case r.URL.Path == "/auth/passkeys" && r.Method == http.MethodGet:
		handlePasskeyList(w, r)
	case r.URL.Path == "/auth/passkey/delete" && r.Method == http.MethodPost:
		handlePasskeyDelete(w, r)
	default:
		http.NotFound(w, r)
	}
}

func identityPayload(user *userAccount) map[string]any {
	return map[string]any{
		"email":       user.Email,
		"name":        user.Name,
		"passkeys":    len(user.Credentials),
		"hasPasskeys": len(user.Credentials) > 0,
	}
}

func handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	payload := struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}{}
	if err := decodeAuthBody(r, &payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Throttle per source IP and per target account, so neither rotating
	// source addresses nor spraying one address across accounts gets
	// unlimited guesses.
	if !authAttemptAllowedForKeys(
		"login|"+clientIPForRateLimit(r),
		"login-email|"+normalizeAccountEmail(payload.Email),
	) {
		writeAuthError(w, http.StatusTooManyRequests, "too many sign-in attempts; try again in a few minutes")
		return
	}

	user, ok := accountStore().authenticate(payload.Email, payload.Password)
	if !ok {
		writeAuthError(w, http.StatusUnauthorized, "that email and password don't match")
		return
	}

	token, err := userSessionStore().create(user.Email)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not start a session")
		return
	}
	clearAuthAttempts("login|"+clientIPForRateLimit(r), "login-email|"+user.Email)
	setSessionCookie(w, r, token, int(sessionTTL/time.Second))
	writeAuthJSON(w, http.StatusOK, identityPayload(user))
}

func handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		userSessionStore().destroy(cookie.Value)
	}
	setSessionCookie(w, r, "", -1)
	writeAuthJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func handleAuthMe(w http.ResponseWriter, r *http.Request) {
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	writeAuthJSON(w, http.StatusOK, identityPayload(user))
}

func handleAuthChangePassword(w http.ResponseWriter, r *http.Request) {
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	payload := struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}{}
	if err := decodeAuthBody(r, &payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := accountStore().changePassword(user.Email, payload.CurrentPassword, payload.NewPassword); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Rotate sessions: a password change revokes every other signed-in
	// device, then re-issues a fresh session for this one.
	userSessionStore().destroyAllForEmail(user.Email)
	if token, err := userSessionStore().create(user.Email); err == nil {
		setSessionCookie(w, r, token, int(sessionTTL/time.Second))
	}
	writeAuthJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
