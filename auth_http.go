package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

	// Guest sessions (multi-room §3.2) live in the SAME sessions.json under a
	// SEPARATE cookie, so a signed-in member clicking a guest link never
	// clobbers their real session; when both cookies exist the member wins.
	guestCookieName  = "bonfire_guest"
	guestSessionTTL  = 12 * time.Hour
	sessionKindGuest = "guest"

	loginAttemptLimit  = 12
	loginAttemptWindow = 5 * time.Minute
	authBodyLimit      = 16 * 1024
	profileBodyLimit   = 256 * 1024
	avatarDataURLLimit = 192 * 1024
)

// Kind "" means "user": legacy rows keep resolving as member sessions, so a
// deploy logs nobody out (§9.4).
type sessionRecord struct {
	Email                        string    `json:"email"`
	Expires                      time.Time `json:"expires"`
	Kind                         string    `json:"kind,omitempty"`
	RoomID                       string    `json:"roomId,omitempty"`
	GuestName                    string    `json:"guestName,omitempty"`
	PersonID                     string    `json:"personId,omitempty"`
	ActiveOrganizationID         string    `json:"activeOrganizationId,omitempty"`
	OrganizationMembershipID     string    `json:"organizationMembershipId,omitempty"`
	OrganizationMembershipRev    int64     `json:"organizationMembershipRevision,omitempty"`
	ActiveOrganizationSessionRev int64     `json:"activeOrganizationSessionRevision,omitempty"`
	AccountSubjectDigest         string    `json:"accountSubjectDigest,omitempty"`
	AuthorityGeneration          uint64    `json:"authorityGeneration,omitempty"`
}

// sessionStore keeps SHA-256 hashes of session tokens (never the tokens
// themselves) in a JSON file next to the other room state, so a leaked data
// directory does not hand out live sessions.
type sessionStore struct {
	mu       sync.Mutex
	path     string
	sessions map[string]sessionRecord
}

// revokedMemberSession is the exact authority edge removed from sessions.json.
// Downstream lifecycle cleanup consumes the hash rather than a user-wide flag,
// so revoking one device can never tear down a newer session's live transport.
type revokedMemberSession struct {
	Email       string
	SessionHash string
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
	if err := writeFileAtomicallyDurable(s.path, raw, 0o600); err != nil {
		log.Errorf("Failed to persist session store: %v", err)
	}
	// Guest-link expiry rides the session-persist seam (multi-room §5.1):
	// every session mutation already sweeps expired sessions above, and the
	// same heartbeat retires expired guest links from the room store.
	sweepExpiredGuestLinksIfOpen()
}

func (s *sessionStore) create(email string) (string, error) {
	if strideE10TenantCutoverEnabled() {
		return "", ErrStrideE10TenantAuthorityStale
	}
	return s.createMemberSession(email, "", "", "", 0, 0, nil)
}

// createMemberSession is the additive W1 session-authority seam. Existing
// logins continue to call create and therefore persist the legacy email-only
// representation until the reviewed person/organization migration activates
// canonical readers. A canonical caller must provide the complete binding;
// partial person or membership authority fails closed.
func (s *sessionStore) createMemberSession(email, personID, organizationID, membershipID string, membershipRevision, sessionRevision int64, authorize func(string, string, string, int64) error) (string, error) {
	email = normalizeAccountEmail(email)
	canonical := personID != "" || organizationID != "" || membershipID != "" || membershipRevision != 0 || sessionRevision != 0
	zeroOrganization := canonical && strideIdentifier(personID) && organizationID == "" && membershipID == "" && membershipRevision == 0 && sessionRevision == 0
	activeOrganization := canonical && strideIdentifier(personID) && strideIdentifier(organizationID) && strideIdentifier(membershipID) && membershipRevision >= 1 && sessionRevision >= 1
	if email == "" || canonical && (!zeroOrganization && !activeOrganization || authorize == nil) {
		return "", errors.New("invalid member session authority")
	}
	// Wave 5 D11: every member-session mint (password, passkey, native, the
	// change-password rotation) funnels through here, so a disabled account
	// can never obtain a fresh session regardless of which ceremony proved it.
	if accountIsDisabled(email) {
		return "", errors.New("account is disabled")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)

	devicePushAuthorityMu.Lock()
	defer devicePushAuthorityMu.Unlock()
	if canonical {
		if err := authorize(personID, organizationID, membershipID, membershipRevision); err != nil {
			return "", errors.New("active organization membership denied")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	generation := uint64(0)
	if canonical {
		generation = 1
		if sessionRevision > 1 {
			generation = uint64(sessionRevision)
		}
	}
	s.sessions[hashResetToken(token)] = sessionRecord{
		Email:                        email,
		Expires:                      time.Now().Add(sessionTTL),
		PersonID:                     personID,
		ActiveOrganizationID:         organizationID,
		OrganizationMembershipID:     membershipID,
		OrganizationMembershipRev:    membershipRevision,
		ActiveOrganizationSessionRev: sessionRevision,
		AccountSubjectDigest:         sha256Hex([]byte(email)),
		AuthorityGeneration:          generation,
	}
	s.persistLocked()
	return token, nil
}

// rebindActiveOrganization changes one exact canonical member session. The
// expected revisions prevent a stale tab or replay from switching authority
// after a concurrent membership/session change. Legacy sessions and partial
// bindings are deliberately ineligible.
func (s *sessionStore) rebindActiveOrganization(token, personID, organizationID, membershipID string, expectedMembershipRevision, expectedSessionRevision, nextSessionRevision int64, authorize func(string, string, string, int64) error) (sessionRecord, error) {
	if strings.TrimSpace(token) == "" || !strideIdentifier(personID) || !strideIdentifier(organizationID) || !strideIdentifier(membershipID) || expectedMembershipRevision < 1 || expectedSessionRevision < 0 || nextSessionRevision != expectedSessionRevision+1 || authorize == nil {
		return sessionRecord{}, errors.New("invalid active organization session binding")
	}
	// The session store cannot infer organization authority from caller-supplied
	// identifiers. Require the organization authority adapter to resolve the
	// exact current active membership before any session state is changed.
	devicePushAuthorityMu.Lock()
	defer devicePushAuthorityMu.Unlock()
	if err := authorize(personID, organizationID, membershipID, expectedMembershipRevision); err != nil {
		return sessionRecord{}, errors.New("active organization membership denied")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := hashResetToken(token)
	record, ok := s.sessions[key]
	if !ok || record.Kind != "" || time.Now().After(record.Expires) || record.PersonID != personID || record.ActiveOrganizationSessionRev != expectedSessionRevision {
		return sessionRecord{}, errors.New("active organization session conflict")
	}
	if expectedSessionRevision == 0 && (record.ActiveOrganizationID != "" || record.OrganizationMembershipID != "" || record.OrganizationMembershipRev != 0) {
		return sessionRecord{}, errors.New("active organization session conflict")
	}
	record.ActiveOrganizationID = organizationID
	record.OrganizationMembershipID = membershipID
	record.OrganizationMembershipRev = expectedMembershipRevision
	record.ActiveOrganizationSessionRev = nextSessionRevision
	record.AuthorityGeneration++
	if record.AuthorityGeneration == 0 {
		record.AuthorityGeneration = uint64(nextSessionRevision)
	}
	s.sessions[key] = record
	s.persistLocked()
	return record, nil
}

// destroyAllForMembershipRevision is the W1 revocation linearization seam for
// organization-bound sessions and push authority. W3 extends the same
// generation fence across sockets, caches, Drive, rooms, brain, Scout, and
// workers before activation.
func (s *sessionStore) destroyAllForMembershipRevision(personID, organizationID, membershipID string, throughRevision int64) int {
	if !strideIdentifier(personID) || !strideIdentifier(organizationID) || !strideIdentifier(membershipID) || throughRevision < 1 {
		return 0
	}
	devicePushAuthorityMu.Lock()
	s.mu.Lock()
	removed := 0
	revoked := make([]revokedMemberSession, 0)
	for key, record := range s.sessions {
		if record.Kind == "" && record.PersonID == personID && record.ActiveOrganizationID == organizationID && record.OrganizationMembershipID == membershipID && record.OrganizationMembershipRev <= throughRevision {
			delete(s.sessions, key)
			removed++
			revoked = append(revoked, revokedMemberSession{Email: record.Email, SessionHash: key})
		}
	}
	if removed > 0 {
		s.persistLocked()
	}
	s.mu.Unlock()
	devicePushAuthorityMu.Unlock()
	terminalizePrivateRealtimeVoiceRevokedSessions(revoked, time.Now().UTC())
	return removed
}

func (s *sessionStore) lookup(token string) (string, bool) {
	record, ok := s.lookupRecord(token)
	if !ok {
		return "", false
	}
	return record.Email, true
}

func (s *sessionStore) lookupRecord(token string) (sessionRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.sessions[hashResetToken(token)]
	if !ok || time.Now().After(record.Expires) {
		return sessionRecord{}, false
	}
	return record, true
}

// lookupMemberRecordByHash is the narrow server-authority seam used by native
// push delivery. Device registrations persist only this SHA-256 key, never the
// raw bearer token. Unknown session kinds fail closed: Kind="" is the one
// member-session representation (including legacy member rows), while guest
// and any future unreviewed session kind are ineligible.
func (s *sessionStore) lookupMemberRecordByHash(sessionHash string, now time.Time) (sessionRecord, bool) {
	sessionHash = strings.TrimSpace(sessionHash)
	if len(sessionHash) != 64 || now.IsZero() {
		return sessionRecord{}, false
	}
	if _, err := hex.DecodeString(sessionHash); err != nil {
		return sessionRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.sessions[sessionHash]
	if !ok || record.Kind != "" || normalizeAccountEmail(record.Email) == "" || !now.Before(record.Expires) {
		return sessionRecord{}, false
	}
	record.Email = normalizeAccountEmail(record.Email)
	return record, true
}

// privateRealtimeLeaseSessionCurrent resolves a persisted one-way lease digest
// against the exact still-live member session. This lets a new login distinguish
// a genuinely concurrent device (conflict) from a lease whose session has
// already been revoked, including after a process restart.
func (s *sessionStore) privateRealtimeLeaseSessionCurrent(email, authSessionDigest string, now time.Time) bool {
	email = normalizeAccountEmail(email)
	authSessionDigest = strings.TrimSpace(authSessionDigest)
	if email == "" || authSessionDigest == "" || now.IsZero() {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for sessionHash, record := range s.sessions {
		if record.Kind != "" || normalizeAccountEmail(record.Email) != email || !now.Before(record.Expires) {
			continue
		}
		if privateRealtimeLeaseDigest("auth-session", sessionHash) == authSessionDigest {
			return true
		}
	}
	return false
}

// createGuest mints a guest session (multi-room §3.2): no account email,
// Kind=guest, bound to exactly ONE room, 12h TTL. It reuses the session
// store's persistence and expiry sweep, and is deliberately invisible to
// userFromRequest.
func (s *sessionStore) createGuest(roomID, guestName string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[hashResetToken(token)] = sessionRecord{
		Expires:   time.Now().Add(guestSessionTTL),
		Kind:      sessionKindGuest,
		RoomID:    roomID,
		GuestName: guestName,
	}
	s.persistLocked()
	return token, nil
}

func (s *sessionStore) destroy(token string) {
	// Serialize revocation against native push delivery. A delivery that already
	// crossed its authority check finishes before destroy returns; every delivery
	// after this write lock observes the session as absent.
	devicePushAuthorityMu.Lock()
	s.mu.Lock()
	key := hashResetToken(token)
	record, found := s.sessions[key]
	delete(s.sessions, key)
	s.persistLocked()
	s.mu.Unlock()
	devicePushAuthorityMu.Unlock()
	if found && record.Kind == "" {
		terminalizePrivateRealtimeVoiceRevokedSessions([]revokedMemberSession{{Email: record.Email, SessionHash: key}}, time.Now().UTC())
	}
}

func (s *sessionStore) destroyAllForEmail(email string) {
	email = normalizeAccountEmail(email)
	// Password reset/rotation has the same linearization contract as logout.
	devicePushAuthorityMu.Lock()
	s.mu.Lock()
	revoked := make([]revokedMemberSession, 0)
	for key, record := range s.sessions {
		if record.Kind == "" && normalizeAccountEmail(record.Email) == email {
			delete(s.sessions, key)
			revoked = append(revoked, revokedMemberSession{Email: record.Email, SessionHash: key})
		}
	}
	s.persistLocked()
	s.mu.Unlock()
	devicePushAuthorityMu.Unlock()
	terminalizePrivateRealtimeVoiceRevokedSessions(revoked, time.Now().UTC())
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

type sessionAuthoritySource string

const (
	sessionAuthorityNone           sessionAuthoritySource = "none"
	sessionAuthorityCookie         sessionAuthoritySource = "cookie"
	sessionAuthorityBearer         sessionAuthoritySource = "authorization_bearer"
	sessionAuthorityExplicitHeader sessionAuthoritySource = "x_bonfire_session"
)

func sessionCookieToken(r *http.Request) string {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if token := strings.TrimSpace(cookie.Value); token != "" {
			return token
		}
	}
	return ""
}

func bearerSessionToken(r *http.Request) string {
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); len(auth) >= 7 && strings.EqualFold(auth[:7], "Bearer ") {
		if token := strings.TrimSpace(auth[7:]); token != "" {
			return token
		}
	}
	return ""
}

func explicitSessionHeaderToken(r *http.Request) string {
	if token := strings.TrimSpace(r.Header.Get("X-Bonfire-Session")); token != "" {
		return token
	}
	return ""
}

// sessionAuthorityFromRequest resolves exactly one member-session authority.
// Reviewed native clients explicitly carry their durable credential, so their
// Authorization/session header must not be shadowed by an ambient browser
// cookie. Web requests retain cookie-first behavior for compatibility and to
// avoid allowing an injected header to silently replace browser authority.
func sessionAuthorityFromRequest(r *http.Request) (string, sessionAuthoritySource) {
	if wantsNativeSessionToken(r) {
		if token := bearerSessionToken(r); token != "" {
			return token, sessionAuthorityBearer
		}
		if token := explicitSessionHeaderToken(r); token != "" {
			return token, sessionAuthorityExplicitHeader
		}
		if token := sessionCookieToken(r); token != "" {
			return token, sessionAuthorityCookie
		}
		return "", sessionAuthorityNone
	}
	if token := sessionCookieToken(r); token != "" {
		return token, sessionAuthorityCookie
	}
	if token := bearerSessionToken(r); token != "" {
		return token, sessionAuthorityBearer
	}
	if token := explicitSessionHeaderToken(r); token != "" {
		return token, sessionAuthorityExplicitHeader
	}
	return "", sessionAuthorityNone
}

// sessionTokenFromRequest returns the single authoritative member session for
// all auth consumers, including device push registration.
func sessionTokenFromRequest(r *http.Request) string {
	token, _ := sessionAuthorityFromRequest(r)
	return token
}

// wantsNativeSessionToken is true for native mobile clients that need the
// session token in the JSON body (cookie jars are unreliable across RN fetch
// and WKWebView). Web browsers keep the HttpOnly cookie only.
func wantsNativeSessionToken(r *http.Request) bool {
	client := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Bonfire-Client")))
	switch client {
	case "expo", "ios", "android", "native", "mobile":
		return true
	default:
		return false
	}
}

// identityPayloadForRequest is identityPayload plus an optional sessionToken
// field when the caller is a native client that just established a session.
func identityPayloadForRequest(r *http.Request, user *userAccount, sessionToken string) map[string]any {
	payload := identityPayload(user)
	token := strings.TrimSpace(sessionToken)
	if token == "" && r != nil {
		token = sessionTokenFromRequest(r)
	}
	payload["shellAccess"] = shellAccessForSession(user, token)
	if sessionToken != "" && wantsNativeSessionToken(r) {
		payload["sessionToken"] = sessionToken
	}
	return payload
}

// shellAccessForSession is the one server-owned projection that decides which
// top-level destinations a signed-in client may advertise. The compact member
// shell is the fail-closed default. AJ retains the founder/owner workspace even
// while a legacy or global-person session has no active organization; everyone
// else needs an exact current owner/admin membership on the authenticated
// session before the unfinished Work/Network/You surfaces may be shown.
func shellAccessForSession(user *userAccount, token string) string {
	if user != nil && normalizeAccountEmail(user.Email) == founderOwnerEmail {
		return "full"
	}
	if user == nil || strings.TrimSpace(token) == "" {
		return "core"
	}
	converter := currentStrideE10TenantRuntimeConverter()
	if converter == nil {
		return "core"
	}
	resolver, ok := converter.resolver.(*strideE10MainTenantAuthorityResolver)
	if !ok || resolver == nil {
		return "core"
	}
	full := false
	err := resolver.WithCurrentTenantAuthority(context.Background(), StrideE10TenantSurfaceHTTP, hashResetToken(token), func(snapshot StrideE10TenantAuthoritySnapshot) error {
		if normalizeAccountEmail(snapshot.Session.Email) != normalizeAccountEmail(user.Email) ||
			snapshot.Membership.Validate() != nil || snapshot.Membership.Status != "active" || snapshot.Membership.EndedAt != nil ||
			snapshot.Membership.PersonID != snapshot.Session.PersonID || snapshot.Membership.OrganizationID != snapshot.Session.ActiveOrganizationID ||
			snapshot.Membership.Header.ID != snapshot.Session.OrganizationMembershipID ||
			snapshot.Membership.Header.Revision != snapshot.Session.OrganizationMembershipRev {
			return ErrStrideE10TenantAuthorityStale
		}
		full = oneOf(snapshot.Membership.Role, "owner", "admin")
		return nil
	})
	if err != nil || !full {
		return "core"
	}
	return "full"
}

// userFromRequest resolves the signed-in account from the session cookie or
// a native bearer/session header, or nil when the request carries no live session.
func userFromRequest(r *http.Request) *userAccount {
	token := sessionTokenFromRequest(r)
	if token == "" {
		return nil
	}
	record, ok := userSessionStore().lookupRecord(token)
	if !ok {
		return nil
	}
	// CRITICAL (multi-room §3.2): a guest session must NEVER resolve to a
	// member account, even if its token lands in the member cookie. This
	// explicit guard — not the implicit empty-Email → findUser("")==nil
	// invariant — is the allowlist guarantee for every session-gated
	// endpoint.
	if record.Kind == sessionKindGuest {
		return nil
	}
	user := accountStore().findUser(record.Email)
	// A disabled account's sessions are revoked on disable; this check is the
	// belt to that suspender so a session persisted by a concurrent write can
	// never keep serving after the stamp lands.
	if user.disabled() {
		return nil
	}
	return user
}

// guestPrincipal is the resolved identity of a guest session: the hashed
// session key (safe to hold as a seat key), its ONE room, and the sanitized
// display name without the server-applied "Guest " prefix.
type guestPrincipal struct {
	SessionKey string
	RoomID     string
	Name       string
}

// guestFromRequest resolves a guest principal from the bonfire_guest cookie
// ONLY, and only for Kind=guest records — a member session in the guest
// cookie slot resolves to nothing.
func guestFromRequest(r *http.Request) *guestPrincipal {
	cookie, err := r.Cookie(guestCookieName)
	if err != nil || cookie.Value == "" {
		return nil
	}
	record, ok := userSessionStore().lookupRecord(cookie.Value)
	if !ok || record.Kind != sessionKindGuest {
		return nil
	}
	return &guestPrincipal{
		SessionKey: hashResetToken(cookie.Value),
		RoomID:     record.RoomID,
		Name:       record.GuestName,
	}
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

func setGuestSessionCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     guestCookieName,
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

// appleAppSiteAssociationHandler binds the App Store identity to the public
// Bonfire relying-party domain. iOS verifies this document before allowing a
// native AuthenticationServices passkey ceremony for thebonfire.xyz.
func appleAppSiteAssociationHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	appID := strings.TrimSpace(os.Getenv("BONFIRE_APPLE_APP_ID"))
	if appID == "" {
		appID = "73PT36P58W.xyz.thebonfire.app"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"applinks": map[string]any{
			"apps": []string{},
			"details": []any{
				map[string]any{
					"appIDs": []string{appID},
					"components": []any{
						map[string]any{
							"/":       "/",
							"?":       map[string]string{"reset": "*"},
							"comment": "Open password reset links in Stride.",
						},
					},
				},
			},
		},
		"webcredentials": map[string]any{
			"apps": []string{appID},
		},
	})
}

func decodeAuthBody(r *http.Request, dest any) error {
	return decodeAuthBodyWithLimit(r, dest, authBodyLimit)
}

func decodeAuthBodyWithLimit(r *http.Request, dest any, limit int64) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, limit))
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
	case r.URL.Path == "/auth/native-web-session" && r.Method == http.MethodGet:
		handleAuthNativeWebSession(w, r)
	case r.URL.Path == "/auth/profile" && r.Method == http.MethodPost:
		handleAuthProfile(w, r)
	case r.URL.Path == "/auth/theme" && r.Method == http.MethodPost:
		handleAuthTheme(w, r)
	case r.URL.Path == "/auth/change-password" && r.Method == http.MethodPost:
		handleAuthChangePassword(w, r)
	case r.URL.Path == "/auth/reset/request" && r.Method == http.MethodPost:
		handleAuthResetRequest(w, r)
	case r.URL.Path == "/auth/reset/confirm" && r.Method == http.MethodPost:
		handleAuthResetConfirm(w, r)
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
		"email":         user.Email,
		"name":          user.Name,
		"avatarDataURL": user.AvatarDataURL,
		"passkeys":      len(user.Credentials),
		"hasPasskeys":   len(user.Credentials) > 0,
		"themePref":     user.ThemePref,
		"shellAccess":   shellAccessForSession(user, ""),
		"organization":  workspaceOrganizationName(),
	}
}

// defaultWorkspaceOrganizationName is the label the shell shows when the
// organization authority has nothing to say (keyless sandboxes, a session that
// is not organization-bound yet). The workspace is single-organization by
// product contract, so "unavailable" is never a truthful label.
const defaultWorkspaceOrganizationName = "Bonfire"

// workspaceOrganizationName resolves the one organization every account
// belongs to: the authority store's single active organization when it has
// one, else STRIDE_ORGANIZATION_NAME, else the product default.
func workspaceOrganizationName() string {
	if runtime := strideE10LiveProductRuntime; runtime != nil {
		if name := strings.TrimSpace(runtime.organization.SingleActiveOrganizationName()); name != "" {
			return name
		}
	}
	if name := strings.TrimSpace(os.Getenv("STRIDE_ORGANIZATION_NAME")); name != "" {
		return name
	}
	return defaultWorkspaceOrganizationName
}

// handleAuthTheme persists the account-level theme preference ("light" |
// "dark" | "system") so it follows the user across devices; /auth/me carries
// it back via identityPayload for the client's session-bootstrap re-apply.
func handleAuthTheme(w http.ResponseWriter, r *http.Request) {
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	payload := struct {
		Theme string `json:"theme"`
	}{}
	if err := decodeAuthBody(r, &payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	theme := strings.TrimSpace(strings.ToLower(payload.Theme))
	if theme != "light" && theme != "dark" && theme != "system" {
		writeAuthError(w, http.StatusBadRequest, "theme must be light, dark, or system")
		return
	}

	updated, err := accountStore().updateThemePref(user.Email, theme)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not save theme")
		return
	}
	writeAuthJSON(w, http.StatusOK, identityPayloadForRequest(r, updated, ""))
}

func handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	payload := struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}{}
	if err := decodeAuthBody(r, &payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := normalizeRosterLoginName(payload.Name)
	if name == "" {
		writeAuthError(w, http.StatusBadRequest, "select a listed account")
		return
	}

	// Throttle per source IP and per target account, so neither rotating
	// source addresses nor spraying one address across accounts gets
	// unlimited guesses.
	if !authAttemptAllowedForKeys(
		"login|"+clientIPForRateLimit(r),
		"login-name|"+name,
	) {
		writeAuthError(w, http.StatusTooManyRequests, "too many sign-in attempts; try again in a few minutes")
		return
	}

	user, ok := accountStore().authenticateRosterName(payload.Name, payload.Password)
	if !ok {
		writeAuthError(w, http.StatusUnauthorized, "that name and password don't match")
		return
	}

	token, err := strideE10CreateAuthenticatedSession(user.Email)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not start a session")
		return
	}
	clearAuthAttempts("login|"+clientIPForRateLimit(r), "login-name|"+name)
	setSessionCookie(w, r, token, int(sessionTTL/time.Second))
	writeAuthJSON(w, http.StatusOK, identityPayloadForRequest(r, user, token))
}

func handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	// Native clients authenticate with the bearer/session header rather than
	// the browser cookie. Resolve all supported transports so signing out from
	// iOS actually revokes the server-side session instead of only clearing the
	// local Keychain copy and leaving a live 30-day token behind.
	user := userFromRequest(r)
	sessionToken, sessionAuthority := sessionAuthorityFromRequest(r)
	sessionHash := ""
	if sessionToken != "" {
		sessionHash = hashResetToken(sessionToken)
	}
	payload := struct {
		DeviceToken string `json:"deviceToken"`
	}{}
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload)
	}
	if sessionToken != "" {
		userSessionStore().destroy(sessionToken)
	}
	deviceBindingRemoved := false
	deviceCleanupPending := false
	if user != nil && strings.TrimSpace(payload.DeviceToken) != "" {
		if err := removeDeviceTokenBinding(canonicalTenantID(), user.Email, payload.DeviceToken, sessionHash); err != nil {
			// Session revocation is the privacy boundary and already completed.
			// Exact-session delivery validation makes this row inert, so cleanup is
			// best-effort storage hygiene and must not turn a successful logout into
			// an ambiguous HTTP failure.
			log.Errorf("Authenticated logout revoked the session but could not remove its inert device binding: %v", err)
			deviceCleanupPending = true
		} else {
			deviceBindingRemoved = true
		}
	}
	// A native bearer can coexist with a different browser session cookie in a
	// shared cookie jar. Never erase that unrelated authority. Web logout and a
	// native logout whose explicit token matches the cookie still clear it.
	ambientCookie := sessionCookieToken(r)
	if sessionAuthority == sessionAuthorityCookie || (ambientCookie != "" && ambientCookie == sessionToken) {
		setSessionCookie(w, r, "", -1)
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok": true, "sessionRevoked": true, "deviceBindingRemoved": deviceBindingRemoved, "deviceCleanupPending": deviceCleanupPending,
		"sessionAuthoritySource": string(sessionAuthority), "sessionAuthorityHash": sessionHash,
	})
}

func handleAuthMe(w http.ResponseWriter, r *http.Request) {
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	// Do not re-emit sessionToken on /auth/me — the native client already
	// holds it. Emitting it again would expand the surface of every me poll.
	writeAuthJSON(w, http.StatusOK, identityPayloadForRequest(r, user, ""))
}

// handleAuthNativeWebSession is a narrow bearer-to-HttpOnly-cookie bridge for
// the few rich web surfaces still embedded by the native app. The bearer is
// carried only in the initial request header, never exposed to page JavaScript,
// a URL, a redirect Location, or response JSON.
func handleAuthNativeWebSession(w http.ResponseWriter, r *http.Request) {
	token := sessionTokenFromRequest(r)
	if token == "" || userFromRequest(r) == nil || !wantsNativeSessionToken(r) {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	destination := strings.TrimSpace(r.URL.Query().Get("path"))
	if destination == "" {
		destination = "/"
	}
	if !strings.HasPrefix(destination, "/") || strings.HasPrefix(destination, "//") || strings.ContainsAny(destination, "\r\n") {
		writeAuthError(w, http.StatusBadRequest, "invalid destination")
		return
	}
	setSessionCookie(w, r, token, int(sessionTTL/time.Second))
	http.Redirect(w, r, destination, http.StatusSeeOther)
}

func handleAuthProfile(w http.ResponseWriter, r *http.Request) {
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	payload := struct {
		DisplayName   string `json:"displayName"`
		AvatarDataURL string `json:"avatarDataURL"`
	}{}
	if err := decodeAuthBodyWithLimit(r, &payload, profileBodyLimit); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}

	displayName, err := cleanDisplayName(payload.DisplayName)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	avatarDataURL, err := cleanAvatarDataURL(payload.AvatarDataURL)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}

	updated, err := accountStore().updateProfile(user.Email, displayName, avatarDataURL)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not update profile")
		return
	}
	writeAuthJSON(w, http.StatusOK, identityPayloadForRequest(r, updated, ""))
}

func cleanDisplayName(value string) (string, error) {
	name := strings.Join(strings.Fields(value), " ")
	if name == "" {
		return "", errors.New("display name is required")
	}
	if len(name) > 80 {
		return "", errors.New("display name must be 80 characters or fewer")
	}
	return name, nil
}

func cleanAvatarDataURL(value string) (string, error) {
	avatar := strings.TrimSpace(value)
	if avatar == "" {
		return "", nil
	}
	if len(avatar) > avatarDataURLLimit {
		return "", fmt.Errorf("avatar image must be smaller than %d KB", avatarDataURLLimit/1024)
	}
	if !strings.HasPrefix(avatar, "data:image/") {
		return "", errors.New("avatar must be an image data URL")
	}
	parts := strings.SplitN(avatar, ";base64,", 2)
	if len(parts) != 2 {
		return "", errors.New("avatar must be base64 encoded")
	}
	switch strings.TrimPrefix(parts[0], "data:") {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
	default:
		return "", errors.New("avatar must be a PNG, JPEG, WebP, or GIF image")
	}
	if _, err := base64.StdEncoding.DecodeString(parts[1]); err != nil {
		return "", errors.New("avatar image data is invalid")
	}
	return avatar, nil
}

// publicBaseURL is where emailed links should point. The request Host header
// is attacker-controlled and must never reach a reset email (reset-link
// poisoning), so only BONFIRE_PUBLIC_URL or a loopback dev host is trusted.
func publicBaseURL(r *http.Request) (string, error) {
	if base := strings.TrimSpace(os.Getenv("BONFIRE_PUBLIC_URL")); base != "" {
		return strings.TrimRight(base, "/"), nil
	}

	host := r.Host
	if splitHost, _, err := net.SplitHostPort(r.Host); err == nil {
		host = splitHost
	}
	if ip := net.ParseIP(host); strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback()) {
		scheme := "http"
		if requestIsSecure(r) {
			scheme = "https"
		}
		return scheme + "://" + r.Host, nil
	}

	return "", errors.New("BONFIRE_PUBLIC_URL is not set; refusing to build an email link from the Host header")
}

func handleAuthResetRequest(w http.ResponseWriter, r *http.Request) {
	payload := struct {
		Email string `json:"email"`
	}{}
	if err := decodeAuthBody(r, &payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}

	if !authAttemptAllowedForKeys(
		"reset|"+clientIPForRateLimit(r),
		"reset-email|"+normalizeAccountEmail(payload.Email),
	) {
		writeAuthError(w, http.StatusTooManyRequests, "too many reset requests; try again in a few minutes")
		return
	}

	// Always answer 202 so the endpoint cannot be used to discover which
	// addresses have accounts.
	if user := accountStore().findUser(payload.Email); user != nil {
		if base, err := publicBaseURL(r); err != nil {
			log.Errorf("Password reset email for %s not sent: %v", user.Email, err)
		} else if token, err := accountStore().createPasswordResetToken(user.Email); err == nil {
			resetURL := base + "/?reset=" + token
			if err := sendAccountEmail(user.Email, "Reset your Stride password", passwordResetEmailHTML(user.Name, resetURL)); err != nil {
				log.Errorf("Failed to send password reset email to %s: %v", user.Email, err)
			}
		}
	}
	writeAuthJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func handleAuthResetConfirm(w http.ResponseWriter, r *http.Request) {
	payload := struct {
		Token       string `json:"token"`
		NewPassword string `json:"newPassword"`
	}{}
	if err := decodeAuthBody(r, &payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}

	email, ok := accountStore().consumePasswordResetToken(payload.Token)
	if !ok {
		writeAuthError(w, http.StatusBadRequest, "that reset link is no longer valid; request a new one")
		return
	}
	if err := accountStore().setPassword(email, payload.NewPassword); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}

	// A reset proves control of the inbox, not of existing sessions — sign
	// everything out so a stolen session dies with the old password.
	userSessionStore().destroyAllForEmail(email)
	writeAuthJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
	token, err := strideE10CreateAuthenticatedSession(user.Email)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not start a new session")
		return
	}
	setSessionCookie(w, r, token, int(sessionTTL/time.Second))
	writeAuthJSON(w, http.StatusOK, identityPayloadForRequest(r, user, token))
}

/* ---------- Wave 5 D11: owner-only account lifecycle ---------- */

// adminAccountPayload is the wire shape for one roster row on the lifecycle
// surface. Deliberately thin: no hashes, no passkeys, no avatars.
func adminAccountPayload(user *userAccount) map[string]any {
	payload := map[string]any{
		"email":    user.Email,
		"name":     user.Name,
		"disabled": user.disabled(),
	}
	if user.disabled() {
		payload["disabledAt"] = user.DisabledAt.UTC().Format(time.RFC3339Nano)
	}
	return payload
}

// adminAccountsHandler serves the owner-only account lifecycle surface:
//   - GET   lists the roster with each account's disabled state
//   - PATCH {email, disabled} stamps or clears disabledAt; disabling also
//     revokes every live session for that account (the password-rotation
//     precedent). The record is never deleted.
//
// Owner means the founder account (isFounderOwner), the same principal the
// shell already projects as shellAccess=full without an organization
// membership. Everyone else — including artifact approval admins — is 403.
func adminAccountsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPatch {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if !isFounderOwner(user) {
		writeAuthError(w, http.StatusForbidden, "account lifecycle is owner-only")
		return
	}
	store := accountStore()
	if r.Method == http.MethodGet {
		accounts := make([]map[string]any, 0, len(seededAccounts))
		for _, email := range store.accountEmails() {
			if account := store.findUser(email); account != nil {
				accounts = append(accounts, adminAccountPayload(account))
			}
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "accounts": accounts})
		return
	}

	payload := struct {
		Email    string `json:"email"`
		Disabled *bool  `json:"disabled"`
	}{}
	if err := decodeAuthBody(r, &payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	email := normalizeAccountEmail(payload.Email)
	if email == "" || payload.Disabled == nil {
		writeAuthError(w, http.StatusBadRequest, "email and disabled are required")
		return
	}
	if email == normalizeAccountEmail(user.Email) {
		// The owner disabling the owner would strand the workspace with nobody
		// able to reverse it.
		writeAuthError(w, http.StatusBadRequest, "the owner account cannot be disabled")
		return
	}
	if store.findUser(email) == nil {
		writeAuthError(w, http.StatusNotFound, "no such account")
		return
	}
	account, err := store.setDisabled(email, *payload.Disabled, time.Now().UTC())
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if *payload.Disabled {
		// Revoke every live session now; userFromRequest also refuses the
		// disabled account so nothing persisted concurrently keeps serving.
		userSessionStore().destroyAllForEmail(email)
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "account": adminAccountPayload(account)})
}
