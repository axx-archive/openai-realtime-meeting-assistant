package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type strideE10TenantSessionContextKey struct{}

type strideE10MainTenantAuthorityResolver struct {
	sessions      *sessionStore
	organizations *OrganizationAuthorityService
	now           func() time.Time
}

func strideE10CreateAuthenticatedSession(email string) (string, error) {
	if !strideE10TenantCutoverEnabled() {
		return userSessionStore().create(email)
	}
	email = normalizeAccountEmail(email)
	digest := sha256Hex([]byte(email))
	organizations := strideE10LiveProductRuntime.organization
	if organizations == nil || email == "" || !isHexDigest(digest) {
		return "", ErrStrideE10TenantAuthorityStale
	}
	organizations.mu.RLock()
	personID := organizations.accountPersons[digest]
	person := organizations.persons[personID]
	organizations.mu.RUnlock()
	if !strideIdentifier(personID) || person.Validate() != nil || person.Status != "active" || person.AccountSubjectDigest != digest {
		return "", ErrStrideE10TenantAuthorityStale
	}
	// A fresh authenticated session starts in the explicit person-only state.
	// It gains organization authority only through the exact membership/session
	// CAS rebind path; login never guesses an organization from email or count.
	authorizePerson := func(wantPersonID, organizationID, membershipID string, membershipRevision int64) error {
		if wantPersonID != personID || organizationID != "" || membershipID != "" || membershipRevision != 0 {
			return ErrStrideE10TenantAuthorityStale
		}
		organizations.mu.RLock()
		defer organizations.mu.RUnlock()
		current, ok := organizations.persons[personID]
		if !ok || current.Validate() != nil || current.Status != "active" || current.AccountSubjectDigest != digest || organizations.accountPersons[digest] != personID {
			return ErrStrideE10TenantAuthorityStale
		}
		return nil
	}
	return userSessionStore().createMemberSession(email, personID, "", "", 0, 0, authorizePerson)
}

func (r *strideE10MainTenantAuthorityResolver) WithCurrentTenantAuthority(ctx context.Context, surface StrideE10TenantSurface, sessionHash string, use func(StrideE10TenantAuthoritySnapshot) error) error {
	if r == nil || r.sessions == nil || r.organizations == nil || use == nil || !validStrideE10TenantSurface(surface) || !validStrideE10SessionHash(sessionHash) {
		return ErrStrideE10TenantAuthorityInvalid
	}
	now := time.Now().UTC()
	if r.now != nil {
		now = r.now().UTC()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Global runtime lock order is session authority then organization
	// authority. The callback remains inside both read fences, so a session
	// switch, revoke, or membership mutation cannot interleave with final use.
	r.sessions.mu.Lock()
	defer r.sessions.mu.Unlock()
	r.organizations.mu.RLock()
	defer r.organizations.mu.RUnlock()
	record, ok := r.sessions.sessions[sessionHash]
	if !ok || record.Kind != "" || !now.Before(record.Expires) || !strideIdentifier(record.PersonID) || !isHexDigest(record.AccountSubjectDigest) || record.AuthorityGeneration < 1 {
		return ErrStrideE10TenantAuthorityStale
	}
	person, ok := r.organizations.persons[record.PersonID]
	if !ok || person.Validate() != nil || person.Status != "active" || person.AccountSubjectDigest != record.AccountSubjectDigest || r.organizations.accountPersons[record.AccountSubjectDigest] != record.PersonID {
		return ErrStrideE10TenantAuthorityStale
	}
	snapshot := StrideE10TenantAuthoritySnapshot{SessionHash: sessionHash, Session: record, Person: clonePersonPrincipal(person), Legacy: StrideE10LegacyPrincipalProjection{TenantID: canonicalTenantID(), AccountSubjectDigest: record.AccountSubjectDigest}, Generation: record.AuthorityGeneration}
	zeroOrganization := record.ActiveOrganizationID == "" && record.OrganizationMembershipID == "" && record.OrganizationMembershipRev == 0 && record.ActiveOrganizationSessionRev == 0
	if zeroOrganization {
		snapshot.Legacy.TenantID = STRIDEGlobalPersonTenant
	} else {
		membership, membershipOK := r.organizations.memberships[record.OrganizationMembershipID]
		activeSession, sessionOK := r.organizations.sessions[sessionHash]
		if !membershipOK || !sessionOK {
			return ErrStrideE10TenantAuthorityStale
		}
		snapshot.Membership = cloneOrganizationMembership(membership)
		snapshot.ActiveSession = cloneActiveOrganizationSession(activeSession)
	}
	return use(snapshot)
}

func strideE10TenantContextWithSessionHash(ctx context.Context, sessionHash string) context.Context {
	if ctx == nil || !validStrideE10SessionHash(sessionHash) {
		return ctx
	}
	return context.WithValue(ctx, strideE10TenantSessionContextKey{}, sessionHash)
}

func strideE10TenantSessionHashFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	hash, _ := ctx.Value(strideE10TenantSessionContextKey{}).(string)
	if !validStrideE10SessionHash(hash) {
		return ""
	}
	return hash
}

// strideE10TenantHTTPHandler holds current authority through the complete
// protected handler effect. Public assets, health probes, login forms and
// purpose-bound internal callbacks stay outside tenant conversion.
func strideE10TenantHTTPHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if next == nil {
			http.NotFound(writer, request)
			return
		}
		surface, protected := strideE10TenantSurfaceForHTTPPath(request.URL.Path)
		if !protected {
			next.ServeHTTP(writer, request)
			return
		}
		err := withStrideE10TenantRequestUse(request, surface, func(ctx context.Context, principal *StrideE10TenantPrincipal) error {
			if strideE10TenantCutoverEnabled() && (!strideE10TenantCutoverSurfaceAvailable(surface) || surface == StrideE10TenantSurfaceHTTP && !strideE10TenantCanonicalHTTPPath(request.URL.Path) || principal != nil && principal.ActiveOrganizationID == "" && !strideE10TenantZeroOrganizationHTTPPath(request.URL.Path)) {
				return ErrStrideE10TenantAuthorityStale
			}
			next.ServeHTTP(writer, request.WithContext(ctx))
			return nil
		})
		if err != nil {
			writeStrideE10TenantHookError(writer, err, "tenant_authority_unavailable")
		}
	})
}

func strideE10TenantSurfaceForHTTPPath(path string) (StrideE10TenantSurface, bool) {
	path = strings.TrimSpace(path)
	for _, prefix := range []string{"/healthz", "/livez", "/readyz", "/capabilities", "/auth/", "/.well-known/", "/public/", "/sw.js", "/a/", "/g", "/guest/", "/ice-test", "/client-config", "/native/config", "/internal/"} {
		if path == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(path, prefix) {
			return "", false
		}
	}
	if path == "/" || path == "/websocket" || path == "/calendar/event.ics" || path == "/signals/survey" {
		return "", false
	}
	switch {
	case strings.HasPrefix(path, strideRuntimeAPIBase+"marketplace"):
		return StrideE10TenantSurfaceMarketplace, true
	case strings.HasPrefix(path, "/assistant/files"):
		return StrideE10TenantSurfaceDrive, true
	case strings.HasPrefix(path, "/assistant/push"):
		return StrideE10TenantSurfacePushDelivery, true
	case strings.HasPrefix(path, "/assistant/notifications"):
		return StrideE10TenantSurfaceNotification, true
	case strings.HasPrefix(path, "/assistant/chat"), strings.HasPrefix(path, "/assistant/attachments"), strings.HasPrefix(path, "/assistant/giphy"):
		return StrideE10TenantSurfaceChat, true
	case strings.HasPrefix(path, "/assistant/board"):
		return StrideE10TenantSurfaceBoard, true
	case strings.HasPrefix(path, "/assistant/memory"), strings.HasPrefix(path, "/assistant/agent-mind"):
		return StrideE10TenantSurfaceBrain, true
	case strings.HasPrefix(path, "/assistant/query"), strings.HasPrefix(path, "/assistant/tools"), strings.HasPrefix(path, "/assistant/threads"):
		return StrideE10TenantSurfaceScout, true
	case strings.HasPrefix(path, "/assistant/goal"):
		return StrideE10TenantSurfaceWorkQueue, true
	case strings.HasPrefix(path, "/rooms"), strings.HasPrefix(path, "/participants"), strings.HasPrefix(path, "/archives/"):
		return StrideE10TenantSurfaceRoomAdmission, true
	case strings.HasPrefix(path, "/artifacts"):
		return StrideE10TenantSurfaceProductContext, true
	case strings.HasPrefix(path, "/assistant/"), strings.HasPrefix(path, "/api/"):
		return StrideE10TenantSurfaceHTTP, true
	default:
		return "", false
	}
}

func strideE10TenantCutoverSurfaceAvailable(surface StrideE10TenantSurface) bool {
	return oneOf(string(surface), string(StrideE10TenantSurfaceHTTP), string(StrideE10TenantSurfaceDrive), string(StrideE10TenantSurfacePushDelivery), string(StrideE10TenantSurfaceNotification))
}

func strideE10TenantCanonicalHTTPPath(path string) bool {
	for _, prefix := range []string{"/api/stride/v1/mobile/", "/api/identity/v1/", "/api/organizations", "/api/session/", "/api/work-record/", "/api/contributions/", "/api/network/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func strideE10TenantZeroOrganizationHTTPPath(path string) bool {
	if strings.HasPrefix(path, "/api/stride/v1/mobile/actions/") {
		return true
	}
	for _, exact := range []string{"/api/stride/v1/mobile/surfaces/profile", "/api/stride/v1/mobile/surfaces/organizations", "/api/identity/v1/me/profile", "/api/organizations"} {
		if path == exact {
			return true
		}
	}
	return false
}

type strideE10TenantWebSocketLease struct {
	sessionHash string
	principal   StrideE10TenantPrincipal
	canonical   bool
}

func (t *threadSafeWriter) ReadTenantMessage(ctx context.Context) (messageType int, payload []byte, err error) {
	read := func() error {
		messageType, payload, err = t.Conn.ReadMessage()
		return err
	}
	if t == nil || t.Conn == nil {
		return 0, nil, errors.New("websocket is closed")
	}
	if t.tenantLease == nil {
		err = read()
		return
	}
	err = t.tenantLease.withCurrent(ctx, read)
	if strideE10TenantAuthorityUnavailable(err) {
		_ = t.Conn.Close()
	}
	return
}

func (t *threadSafeWriter) writeJSONWithTenantAuthority(value any) error {
	write := func() error {
		t.Lock()
		defer t.Unlock()
		_ = t.Conn.SetWriteDeadline(time.Now().Add(websocketWriteTimeout))
		err := t.Conn.WriteJSON(value)
		_ = t.Conn.SetWriteDeadline(time.Time{})
		return err
	}
	if t.tenantLease == nil {
		return write()
	}
	err := t.tenantLease.withCurrent(context.Background(), write)
	if strideE10TenantAuthorityUnavailable(err) {
		_ = t.Conn.Close()
	}
	return err
}

func (t *threadSafeWriter) writeControlWithTenantAuthority(messageType int, data []byte, deadline time.Time) error {
	write := func() error {
		t.Lock()
		defer t.Unlock()
		return t.Conn.WriteControl(messageType, data, deadline)
	}
	if t.tenantLease == nil {
		return write()
	}
	err := t.tenantLease.withCurrent(context.Background(), write)
	if strideE10TenantAuthorityUnavailable(err) && messageType != websocket.CloseMessage {
		_ = t.Conn.Close()
	}
	return err
}

func strideE10BindTenantWebSocket(request *http.Request) (strideE10TenantWebSocketLease, error) {
	hash := strideE10SessionHashFromRequest(request)
	if !validStrideE10SessionHash(hash) {
		if strideE10TenantCutoverEnabled() {
			return strideE10TenantWebSocketLease{}, ErrStrideE10TenantAuthorityStale
		}
		return strideE10TenantWebSocketLease{}, nil
	}
	lease := strideE10TenantWebSocketLease{sessionHash: hash}
	err := withStrideE10TenantRuntimeAuthority(request.Context(), StrideE10TenantSurfaceWebSocket, hash,
		func() error { return nil },
		func(principal StrideE10TenantPrincipal) error {
			lease.principal, lease.canonical = principal, true
			return nil
		})
	return lease, err
}

func (l strideE10TenantWebSocketLease) withCurrent(ctx context.Context, effect func() error) error {
	if effect == nil {
		return ErrStrideE10TenantAuthorityInvalid
	}
	return withStrideE10TenantRuntimeAuthority(ctx, StrideE10TenantSurfaceWebSocket, l.sessionHash, effect, func(principal StrideE10TenantPrincipal) error {
		if !l.canonical || principal != l.principal {
			return ErrStrideE10TenantAuthorityStale
		}
		return effect()
	})
}

type strideE10TenantCacheKey struct {
	TenantID, PersonID, OrganizationID, MembershipID string
	MembershipRevision, SessionRevision              int64
	AuthorityGeneration                              uint64
	Namespace, ResourceID                            string
	SourceRevision                                   int64
}

func strideE10TenantCacheKeyFor(principal StrideE10TenantPrincipal, namespace, resourceID string, sourceRevision int64) (strideE10TenantCacheKey, error) {
	key := strideE10TenantCacheKey{TenantID: principal.TenantID, PersonID: principal.PersonID, OrganizationID: principal.ActiveOrganizationID, MembershipID: principal.OrganizationMembershipID, MembershipRevision: principal.OrganizationMembershipRev, SessionRevision: principal.ActiveOrganizationSessionRev, AuthorityGeneration: principal.AuthorityGeneration, Namespace: strings.TrimSpace(namespace), ResourceID: strings.TrimSpace(resourceID), SourceRevision: sourceRevision}
	if !strideIdentifier(key.TenantID) || !strideIdentifier(key.PersonID) || !strideIdentifier(key.OrganizationID) || !strideIdentifier(key.MembershipID) || key.MembershipRevision < 1 || key.SessionRevision < 1 || key.AuthorityGeneration < 1 || !strideIdentifier(key.Namespace) || !strideIdentifier(key.ResourceID) || key.SourceRevision < 1 {
		return strideE10TenantCacheKey{}, ErrStrideE10TenantAuthorityInvalid
	}
	return key, nil
}

type strideE10TenantCache struct {
	mu   sync.RWMutex
	rows map[strideE10TenantCacheKey]any
}

func (c *strideE10TenantCache) Put(principal StrideE10TenantPrincipal, namespace, resourceID string, sourceRevision int64, value any) error {
	key, err := strideE10TenantCacheKeyFor(principal, namespace, resourceID, sourceRevision)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rows == nil {
		c.rows = make(map[strideE10TenantCacheKey]any)
	}
	c.rows[key] = value
	return nil
}

func (c *strideE10TenantCache) GetCurrent(ctx context.Context, capability *StrideE10TenantCapability, namespace, resourceID string, sourceRevision int64) (any, bool, error) {
	converter := currentStrideE10TenantRuntimeConverter()
	if converter == nil || capability == nil {
		return nil, false, ErrStrideE10TenantAuthorityStale
	}
	var value any
	var found bool
	err := converter.WithCurrentPrincipal(ctx, capability, func(principal StrideE10TenantPrincipal) error {
		key, keyErr := strideE10TenantCacheKeyFor(principal, namespace, resourceID, sourceRevision)
		if keyErr != nil {
			return keyErr
		}
		c.mu.RLock()
		value, found = c.rows[key]
		c.mu.RUnlock()
		return nil
	})
	if err != nil {
		return nil, false, ErrStrideE10TenantAuthorityStale
	}
	return value, found, nil
}

func strideE10TenantAuthorityUnavailable(err error) bool {
	return errors.Is(err, ErrStrideE10TenantAuthorityInvalid) || errors.Is(err, ErrStrideE10TenantAuthorityStale) || errors.Is(err, ErrStrideE10TenantConversionDisabled)
}
