package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

var errOrganizationExecutionUnavailable = errors.New("organization execution is unavailable on this instance")

// This is a compatibility fence, not tenant cutover or a grant to another
// organization. The engine still owns exactly its configured tenant stores.
type organizationExecutionScope struct {
	SessionHash string
	Principal   StrideE10TenantPrincipal
}

// The session and W4 authority stores are single-process stores. Their lock
// order is session then organization. Only scope capture/comparison executes
// inside use; handlers and network I/O must run after these locks are released.
func withOrganizationExecutionScope(ctx context.Context, sessionHash string, expected *organizationExecutionScope, compare bool, use func(*organizationExecutionScope) error) error {
	if use == nil {
		return errOrganizationExecutionUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validStrideE10SessionHash(sessionHash) {
		if expected != nil {
			return errOrganizationExecutionUnavailable
		}
		return use(nil)
	}
	sessions := userSessionStore()
	sessions.mu.Lock()
	sessionLocked := true
	defer func() {
		if sessionLocked {
			sessions.mu.Unlock()
		}
	}()
	record, found := sessions.sessions[sessionHash]
	bound := found && (record.PersonID != "" || record.ActiveOrganizationID != "" || record.OrganizationMembershipID != "" || record.OrganizationMembershipRev != 0 || record.ActiveOrganizationSessionRev != 0)
	if !bound {
		if expected != nil {
			return errOrganizationExecutionUnavailable
		}
		// Existing email-only sessions retain their existing handler authorization.
		// No W4 fence is held across their output.
		sessions.mu.Unlock()
		sessionLocked = false
		return use(nil)
	}
	if record.Kind != "" || !time.Now().Before(record.Expires) || record.ActiveOrganizationID == "" ||
		record.ActiveOrganizationID != canonicalTenantID() || record.ActiveOrganizationID != canonicalArtifactTenantID() ||
		sha256Hex([]byte(normalizeAccountEmail(record.Email))) != record.AccountSubjectDigest || strideE10LiveProductRuntime == nil || strideE10LiveProductRuntime.organization == nil {
		return errOrganizationExecutionUnavailable
	}
	organizations := strideE10LiveProductRuntime.organization
	organizations.mu.RLock()
	defer organizations.mu.RUnlock()
	resolver := &strideE10MainTenantAuthorityResolver{sessions: sessions, organizations: organizations}
	snapshot, err := resolver.currentTenantAuthoritySnapshotLocked(sessionHash, time.Now().UTC())
	if err != nil {
		return errOrganizationExecutionUnavailable
	}
	// Reuse the full W4 person/membership/session consistency rules without
	// making shadow observations into canonical execution capabilities.
	validator := &StrideE10TenantConverter{}
	principal, err := validator.principalFromSnapshot(snapshot, sessionHash, StrideE10TenantSurfaceHTTP)
	if err != nil {
		return errOrganizationExecutionUnavailable
	}
	current := &organizationExecutionScope{SessionHash: sessionHash, Principal: principal}
	if compare && (expected == nil || *current != *expected) {
		return errOrganizationExecutionUnavailable
	}
	return use(current)
}

func organizationExecutionScopeForSession(ctx context.Context, sessionHash string) (*organizationExecutionScope, error) {
	var scope *organizationExecutionScope
	err := withOrganizationExecutionScope(ctx, sessionHash, nil, false, func(current *organizationExecutionScope) error { scope = current; return nil })
	return scope, err
}

func organizationExecutionAuthorityRoute(request *http.Request) bool {
	// Only routes actually owned by the W4 authority router bypass the legacy
	// instance fence. Prefix-like URLs cannot invent an authority exemption.
	if _, ok := strideE10MatchRoute(request.Method, request.URL.Path); ok {
		return true
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) != 6 || parts[0] != "api" || parts[1] != "stride" || parts[2] != "v1" || parts[3] != "mobile" {
		return false
	}
	if request.Method == http.MethodGet && parts[4] == "surfaces" {
		_, ok := strideE10MobileSurfaces[parts[5]]
		return ok
	}
	return request.Method == http.MethodPost && parts[4] == "actions" && parts[5] != ""
}
func organizationExecutionGuestSocket(request *http.Request) bool {
	return request.URL.Path == "/websocket" && request.URL.Query().Get("as") == "guest" && guestFromRequest(request) != nil
}
func organizationExecutionProtectedRequest(request *http.Request) bool {
	if request == nil || organizationExecutionAuthorityRoute(request) || organizationExecutionGuestSocket(request) {
		return false
	}
	if oneOf(request.URL.Path, "/websocket", "/calendar/event.ics", "/calendar/meetings.ics", "/signals/survey", "/client-config", "/native/config") {
		return true
	}
	_, protected := strideE10TenantSurfaceForHTTPPath(request.URL.Path)
	return protected
}

// HTTP handlers retain their normal locks and authorization. Revalidate the
// captured organization again at response emission, rather than holding the
// session lock across handlers which themselves read sessions or stream work.
type organizationExecutionResponseWriter struct {
	http.ResponseWriter
	ctx         context.Context
	hash        string
	scope       *organizationExecutionScope
	wroteHeader bool
	blocked     bool
}

func (w *organizationExecutionResponseWriter) unavailable() {
	if w.blocked {
		return
	}
	w.blocked = true
	if !w.wroteHeader {
		w.wroteHeader = true
		writeAuthError(w.ResponseWriter, http.StatusConflict, "organization_execution_unavailable")
	}
}

// Authorization is checked immediately before each output chunk. The accepted
// chunk may finish after a concurrent switch, but subsequent chunks are denied.
// Never hold global identity locks while a network peer can stall a write.
func (w *organizationExecutionResponseWriter) withWrite(use func(*organizationExecutionScope) error) error {
	if err := withOrganizationExecutionScope(w.ctx, w.hash, w.scope, true, func(*organizationExecutionScope) error { return nil }); err != nil {
		return err
	}
	return use(w.scope)
}
func (w *organizationExecutionResponseWriter) WriteHeader(status int) {
	if w.blocked || w.wroteHeader {
		return
	}
	err := w.withWrite(func(*organizationExecutionScope) error {
		w.ResponseWriter.WriteHeader(status)
		w.wroteHeader = true
		return nil
	})
	if err != nil {
		w.unavailable()
	}
}
func (w *organizationExecutionResponseWriter) Write(body []byte) (int, error) {
	if w.blocked {
		return 0, errOrganizationExecutionUnavailable
	}
	var n int
	err := w.withWrite(func(*organizationExecutionScope) error {
		var err error
		n, err = w.ResponseWriter.Write(body)
		w.wroteHeader = true
		return err
	})
	if err != nil {
		w.unavailable()
	}
	return n, err
}
func (w *organizationExecutionResponseWriter) Flush() {
	if w.blocked {
		return
	}
	err := w.withWrite(func(*organizationExecutionScope) error {
		if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
			flusher.Flush()
			w.wroteHeader = true
		}
		return nil
	})
	if err != nil {
		w.unavailable()
	}
}
func (w *organizationExecutionResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func organizationExecutionHTTPUse(writer http.ResponseWriter, request *http.Request, next http.Handler) {
	if !organizationExecutionProtectedRequest(request) || strideE10TenantCutoverEnabled() {
		next.ServeHTTP(writer, request)
		return
	}
	hash := strideE10SessionHashFromRequest(request)
	scope, err := organizationExecutionScopeForSession(request.Context(), hash)
	if err != nil {
		writeAuthError(writer, http.StatusConflict, "organization_execution_unavailable")
		return
	}
	// Upgrade authority and per-frame checks live in the socket lease. A plain
	// ResponseWriter wrapper must not strip Gorilla's Hijacker interface.
	if request.URL.Path == "/websocket" {
		next.ServeHTTP(writer, request)
		return
	}
	next.ServeHTTP(&organizationExecutionResponseWriter{ResponseWriter: writer, ctx: request.Context(), hash: hash, scope: scope}, request)
}
