package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type strideE10TenantSessionContextKey struct{}

type strideE10MainTenantAuthorityResolver struct {
	sessions      *sessionStore
	organizations *OrganizationAuthorityService
	now           func() time.Time
}

type strideE10HeldTenantAuthorityContextKey struct{}

type strideE10HeldTenantAuthorityFence struct {
	resolver             *strideE10MainTenantAuthorityResolver
	converter            *StrideE10TenantConverter
	principal            StrideE10TenantPrincipal
	accountSubjectDigest string
	snapshot             StrideE10TenantAuthoritySnapshot
	now                  time.Time
	active               atomic.Bool
}

func strideE10BindCurrentHeldTenantAuthority(ctx context.Context, converter *StrideE10TenantConverter, principal StrideE10TenantPrincipal, sessionHash string, surface StrideE10TenantSurface) (context.Context, func(), error) {
	ctx = strideE10TenantContextWithSessionHash(ctx, sessionHash)
	ctx = context.WithValue(ctx, strideE10TenantPrincipalContextKey{}, principal)
	if converter == nil {
		return ctx, func() {}, ErrStrideE10TenantAuthorityStale
	}
	resolver, ok := converter.resolver.(*strideE10MainTenantAuthorityResolver)
	if !ok {
		return ctx, func() {}, nil
	}
	now := time.Now().UTC()
	if resolver.now != nil {
		now = resolver.now().UTC()
	}
	snapshot, err := resolver.currentTenantAuthoritySnapshotLocked(sessionHash, now)
	if err != nil {
		return ctx, func() {}, err
	}
	verified, err := converter.principalFromSnapshot(snapshot, sessionHash, surface)
	if err != nil || verified != principal {
		return ctx, func() {}, ErrStrideE10TenantAuthorityStale
	}
	fence := &strideE10HeldTenantAuthorityFence{
		resolver: resolver, converter: converter, principal: principal, snapshot: snapshot,
		accountSubjectDigest: snapshot.Session.AccountSubjectDigest, now: now,
	}
	fence.active.Store(true)
	return strideE10ContextWithHeldTenantAuthority(ctx, fence), func() { fence.active.Store(false) }, nil
}

func strideE10CreateAuthenticatedSession(email string) (string, error) {
	if !strideE10TenantCutoverEnabled() && !strideE10W4ProductionRuntimeReady() {
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
	// Mint the global-person row first. When the person has exactly one current
	// active membership, immediately drive that same token through the durable
	// organization-switch operation below. Zero memberships remain person-only;
	// multiple memberships deliberately require an explicit human choice.
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
	token, err := userSessionStore().createMemberSession(email, personID, "", "", 0, 0, authorizePerson)
	if err != nil {
		return "", err
	}
	membership, single, err := strideE10SingleActiveOrganizationMembership(organizations, personID)
	if err != nil {
		userSessionStore().destroy(token)
		return "", err
	}
	if !single {
		return token, nil
	}
	if err := strideE10BindAuthenticatedSessionOrganization(token, personID, membership); err != nil {
		// The raw bearer has not been returned to the caller. Revoke it rather
		// than exposing a partially applied cross-store login operation. The
		// immutable switch operation remains available for receipt-first repair.
		userSessionStore().destroy(token)
		return "", err
	}
	return token, nil
}

// strideE10SingleActiveOrganizationMembership returns a login default only
// when current authority has exactly one unambiguous active membership. An
// active membership whose organization is missing, invalid, or inactive is a
// stale-authority failure rather than permission to fall back to person-only.
func strideE10SingleActiveOrganizationMembership(organizations *OrganizationAuthorityService, personID string) (OrganizationMembership, bool, error) {
	if organizations == nil || !strideIdentifier(personID) {
		return OrganizationMembership{}, false, ErrStrideE10TenantAuthorityStale
	}
	organizations.mu.RLock()
	defer organizations.mu.RUnlock()
	candidates := make([]OrganizationMembership, 0, 2)
	for _, membership := range organizations.memberships {
		if membership.PersonID != personID || membership.Status != "active" || membership.EndedAt != nil {
			continue
		}
		organization, ok := organizations.organizations[membership.OrganizationID]
		if membership.Validate() != nil || !ok || organization.Validate() != nil || organization.Status != "active" {
			return OrganizationMembership{}, false, ErrStrideE10TenantAuthorityStale
		}
		candidates = append(candidates, cloneOrganizationMembership(membership))
		if len(candidates) > 1 {
			return OrganizationMembership{}, false, nil
		}
	}
	if len(candidates) != 1 {
		return OrganizationMembership{}, false, nil
	}
	return candidates[0], true, nil
}

// strideE10BindAuthenticatedSessionOrganization reuses the ordinary durable
// organization-switch operation. It does not write a special login-only
// authority row or bypass the operation journal, active-session receipt,
// membership recheck, runtime snapshot, or session-store postimage.
func strideE10BindAuthenticatedSessionOrganization(token, personID string, membership OrganizationMembership) error {
	runtime := strideE10LiveProductRuntime
	if runtime == nil || strings.TrimSpace(token) == "" || !strideIdentifier(personID) || membership.Validate() != nil || membership.PersonID != personID || membership.Status != "active" || membership.EndedAt != nil {
		return ErrStrideE10TenantAuthorityStale
	}
	principal := StrideE10ProductPrincipal{PersonID: personID}
	runtime.mintProjectionActions(principal, "organizations")
	runtime.mu.RLock()
	var binding StrideE10LiveActionBinding
	for _, candidate := range runtime.actions {
		if candidate.Type == "organization-switch" && candidate.Surface == "organizations" && candidate.PersonID == personID && candidate.Target.ID == membership.Header.ID && candidate.Target.Revision == membership.Header.Revision && candidate.Target.Digest == membership.Header.ContentDigest && runtime.now().Before(candidate.ExpiresAt) {
			binding = cloneStrideE10LiveActionBinding(candidate)
			break
		}
	}
	runtime.mu.RUnlock()
	if binding.ID == "" {
		return ErrStrideE10TenantAuthorityStale
	}
	body, err := json.Marshal(map[string]any{"action": "organization-switch", "surface": "organizations", "expectedRevision": binding.ExpectedRevision, "values": map[string]any{}})
	if err != nil {
		return ErrStrideE10TenantAuthorityStale
	}
	sessionHash := hashResetToken(token)
	command := StrideE10ProductCommand{
		Operation: "session.switch_organization", Method: http.MethodPost,
		Path: "/api/stride/v1/mobile/actions/" + binding.ID, ResourceID: binding.ID,
		ExpectedRevision: binding.ExpectedRevision, IdempotencyKey: "login-auto-bind-" + sessionHash[:24], Body: body,
	}
	ctx := context.WithValue(context.Background(), strideE10LiveSessionTokenKey{}, token)
	if _, _, err := runtime.Execute(ctx, principal, command); err != nil {
		return ErrStrideE10TenantAuthorityStale
	}
	record, ok := userSessionStore().lookupRecord(token)
	if !ok || record.PersonID != personID || record.ActiveOrganizationID != membership.OrganizationID || record.OrganizationMembershipID != membership.Header.ID || record.OrganizationMembershipRev != membership.Header.Revision || record.ActiveOrganizationSessionRev != 1 || record.AuthorityGeneration < 2 {
		return ErrStrideE10TenantAuthorityStale
	}
	return nil
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
	snapshot, err := r.currentTenantAuthoritySnapshotLocked(sessionHash, now)
	if err != nil {
		return err
	}
	return use(snapshot)
}

func (r *strideE10MainTenantAuthorityResolver) currentTenantAuthoritySnapshotLocked(sessionHash string, now time.Time) (StrideE10TenantAuthoritySnapshot, error) {
	record, ok := r.sessions.sessions[sessionHash]
	if !ok || record.Kind != "" || !now.Before(record.Expires) || !strideIdentifier(record.PersonID) || !isHexDigest(record.AccountSubjectDigest) || record.AuthorityGeneration < 1 {
		return StrideE10TenantAuthoritySnapshot{}, ErrStrideE10TenantAuthorityStale
	}
	person, ok := r.organizations.persons[record.PersonID]
	if !ok || person.Validate() != nil || person.Status != "active" || person.AccountSubjectDigest != record.AccountSubjectDigest || r.organizations.accountPersons[record.AccountSubjectDigest] != record.PersonID {
		return StrideE10TenantAuthoritySnapshot{}, ErrStrideE10TenantAuthorityStale
	}
	snapshot := StrideE10TenantAuthoritySnapshot{SessionHash: sessionHash, Session: record, Person: clonePersonPrincipal(person), Legacy: StrideE10LegacyPrincipalProjection{TenantID: canonicalTenantID(), AccountSubjectDigest: record.AccountSubjectDigest}, Generation: record.AuthorityGeneration}
	zeroOrganization := record.ActiveOrganizationID == "" && record.OrganizationMembershipID == "" && record.OrganizationMembershipRev == 0 && record.ActiveOrganizationSessionRev == 0
	if zeroOrganization {
		snapshot.Legacy.TenantID = STRIDEGlobalPersonTenant
	} else {
		organization, organizationOK := r.organizations.organizations[record.ActiveOrganizationID]
		membership, membershipOK := r.organizations.memberships[record.OrganizationMembershipID]
		activeSession, sessionOK := r.organizations.sessions[sessionHash]
		if !organizationOK || !membershipOK || !sessionOK {
			return StrideE10TenantAuthoritySnapshot{}, ErrStrideE10TenantAuthorityStale
		}
		snapshot.Organization = cloneOrganization(organization)
		snapshot.Membership = cloneOrganizationMembership(membership)
		snapshot.ActiveSession = cloneActiveOrganizationSession(activeSession)
	}
	return snapshot, nil
}

func strideE10ContextWithHeldTenantAuthority(ctx context.Context, fence *strideE10HeldTenantAuthorityFence) context.Context {
	if ctx == nil || fence == nil {
		return ctx
	}
	return context.WithValue(ctx, strideE10HeldTenantAuthorityContextKey{}, fence)
}

func strideE10HeldTenantAuthorityFromContext(ctx context.Context) *strideE10HeldTenantAuthorityFence {
	if ctx == nil {
		return nil
	}
	fence, _ := ctx.Value(strideE10HeldTenantAuthorityContextKey{}).(*strideE10HeldTenantAuthorityFence)
	if fence == nil || !fence.active.Load() {
		return nil
	}
	return fence
}

// authorizesWebSocket is called only while resolver authority locks are held
// by the outer effect. It validates another live session directly against
// those fenced maps, avoiding a recursive resolver lock while preserving the
// exact current-session check for every socket that receives the effect.
func (fence *strideE10HeldTenantAuthorityFence) authorizesWebSocket(lease *strideE10TenantWebSocketLease) bool {
	if fence == nil || !fence.active.Load() || fence.resolver == nil || fence.converter == nil || lease == nil || !lease.canonical || !validStrideE10SessionHash(lease.sessionHash) {
		return false
	}
	snapshot, err := fence.resolver.currentTenantAuthoritySnapshotLocked(lease.sessionHash, fence.now)
	if err != nil || snapshot.Session.AccountSubjectDigest != fence.accountSubjectDigest || snapshot.Session.PersonID != fence.principal.PersonID {
		return false
	}
	principal, err := fence.converter.principalFromSnapshot(snapshot, lease.sessionHash, StrideE10TenantSurfaceWebSocket)
	return err == nil && principal == lease.principal && principal.PersonID == fence.principal.PersonID && principal.TenantID == fence.principal.TenantID
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
	return t.writeJSONWithTenantAuthorityContext(context.Background(), value)
}

func (t *threadSafeWriter) writeJSONWithTenantAuthorityContext(ctx context.Context, value any) error {
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
	var err error
	if fence := strideE10HeldTenantAuthorityFromContext(ctx); fence != nil {
		if fence.authorizesWebSocket(t.tenantLease) {
			err = write()
		} else {
			err = ErrStrideE10TenantAuthorityStale
		}
	} else {
		err = t.tenantLease.withCurrent(context.Background(), write)
	}
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
