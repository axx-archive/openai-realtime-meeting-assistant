package main

import (
	"context"
	"net/http"
	"path/filepath"
	"sync"
)

var strideE10W5ProductState struct {
	sync.RWMutex
	handler http.Handler
}

type StrideE10W5ProductionConfig struct {
	StatePath string
	StateKeys MyMindCustodyStateKeyring
	HighWater MyMindCustodyHighWaterStore
	Keys      MyMindCustodyKeyring
}

func (c StrideE10W5ProductionConfig) valid() bool {
	return filepath.IsAbs(c.StatePath) && filepath.Clean(c.StatePath) == c.StatePath && c.StateKeys != nil && c.HighWater != nil && c.Keys != nil
}

// InstallStrideE10W5ProductionRuntime is the managed-adapter composition
// boundary. It deliberately has no environment-key fallback: production must
// provide independent custody, destruction verification, and monotonic
// high-water implementations before the feature can be installed.
func InstallStrideE10W5ProductionRuntime(ctx context.Context, config StrideE10W5ProductionConfig) (*FileMyMindCustody, error) {
	if ctx == nil || !config.valid() || strideE10LiveProductRuntime == nil {
		return nil, ErrMyMindCustodyDenied
	}
	authority := &strideE10W5CanonicalAuthorityResolver{runtime: strideE10LiveProductRuntime}
	custody, err := NewFileMyMindCustody(config.StatePath, config.StateKeys, config.HighWater, config.Keys, authority)
	if err != nil {
		return nil, err
	}
	if err := custody.VerifyRestore(ctx); err != nil {
		return nil, err
	}
	// NewFileMyMindCustody performs boot recovery before returning. Repeat both
	// resumptions with the caller's bounded startup context so the production
	// composition explicitly proves that no prepared source/person destruction
	// journal is left unresolved before the handler is installed.
	if err := custody.ResumeSourceForgets(ctx); err != nil {
		return nil, err
	}
	if err := custody.ResumeDeletions(ctx); err != nil {
		return nil, err
	}
	if err := installStrideE10W5ProductRuntime(custody); err != nil {
		return nil, err
	}
	return custody, nil
}

// strideE10W5CanonicalAuthorityResolver holds the same session -> organization
// lock order used by active-organization rebinding through the final custody
// callback. A caller-supplied snapshot never authorizes by itself.
type strideE10W5CanonicalAuthorityResolver struct {
	runtime *StrideE10ProductLiveRuntime
}

func (r *strideE10W5CanonicalAuthorityResolver) WithCurrentMyMindPrivateAuthority(ctx context.Context, requested MyMindPrivateAuthority, use func(MyMindPrivateAuthority) error) error {
	if r == nil || r.runtime == nil || r.runtime.organization == nil || use == nil || !validMyMindPrivateAuthority(requested) {
		return ErrMyMindCustodyDenied
	}
	store := userSessionStore()
	store.mu.Lock()
	record, ok := store.sessions[requested.SessionSubjectDigest]
	if !ok || record.PersonID != requested.PersonID || record.ActiveOrganizationID != requested.OrganizationID || record.OrganizationMembershipID != requested.MembershipID || record.OrganizationMembershipRev != requested.MembershipRevision || record.ActiveOrganizationSessionRev != requested.SessionRevision || !r.runtime.now().UTC().Before(record.Expires) {
		store.mu.Unlock()
		return ErrMyMindCustodyDenied
	}
	organization := r.runtime.organization
	organization.mu.RLock()
	defer organization.mu.RUnlock()
	defer store.mu.Unlock()
	person, personOK := organization.persons[requested.PersonID]
	membership, membershipOK := organization.memberships[requested.MembershipID]
	var session ActiveOrganizationSession
	for _, candidate := range organization.sessions {
		if candidate.SessionSubjectDigest == requested.SessionSubjectDigest {
			session = candidate
			break
		}
	}
	if !personOK || !membershipOK || session.Header.ID == "" {
		return ErrMyMindCustodyDenied
	}
	current, err := ResolveMyMindPrivateAuthority(person, membership, session, r.runtime.now().UTC())
	if err != nil || !sameMyMindPrivateAuthority(current, requested) {
		return ErrMyMindCustodyDenied
	}
	return use(current)
}

func sameMyMindPrivateAuthority(left, right MyMindPrivateAuthority) bool {
	return left.PersonID == right.PersonID && left.OrganizationID == right.OrganizationID && left.MembershipID == right.MembershipID && left.MembershipRevision == right.MembershipRevision && left.SessionSubjectDigest == right.SessionSubjectDigest && left.SessionRevision == right.SessionRevision
}

// installStrideE10W5ProductRuntime installs only an already-constructed
// custody service. Key custody, destruction evidence, and monotonic high-water
// adapters remain outside the web process and must be established first.
func installStrideE10W5ProductRuntime(custody StrideE10W5Custody) error {
	if custody == nil || strideE10LiveProductRuntime == nil || strideE10LiveProductRuntime.organization == nil {
		return ErrMyMindCustodyDenied
	}
	handler := NewStrideE10W5HTTP(custody, resolveStrideE10W5PrivateAuthority, strideE10W5FeatureEnabled)
	strideE10W5ProductState.Lock()
	strideE10W5ProductState.handler = handler
	strideE10W5ProductState.Unlock()
	return nil
}

func uninstallStrideE10W5ProductRuntime() {
	strideE10W5ProductState.Lock()
	strideE10W5ProductState.handler = nil
	strideE10W5ProductState.Unlock()
}

func strideE10W5ProductHandler(w http.ResponseWriter, r *http.Request) {
	strideE10W5ProductState.RLock()
	handler := strideE10W5ProductState.handler
	strideE10W5ProductState.RUnlock()
	if handler == nil {
		w.Header().Set("Cache-Control", "no-store")
		writeStrideE10W5Error(w, http.StatusServiceUnavailable, "feature_unavailable")
		return
	}
	handler.ServeHTTP(w, r)
}

func strideE10W5FeatureEnabled() bool {
	runtime := strideE10LiveProductRuntime
	if runtime == nil {
		return false
	}
	runtime.mu.RLock()
	enabled := runtime.features[STRIDEFeaturePersonMyMindContext]
	runtime.mu.RUnlock()
	return enabled
}

func strideE10W5RuntimeReadinessSnapshot() map[string]any {
	enabled := strideE10W5FeatureEnabled()
	strideE10W5ProductState.RLock()
	installed := strideE10W5ProductState.handler != nil
	strideE10W5ProductState.RUnlock()
	return map[string]any{
		"configured": enabled,
		"installed":  installed,
		"ready":      !enabled || installed,
	}
}

func resolveStrideE10W5PrivateAuthority(request *http.Request) (MyMindPrivateAuthority, error) {
	runtime := strideE10LiveProductRuntime
	if runtime == nil || request == nil || runtime.organization == nil {
		return MyMindPrivateAuthority{}, ErrMyMindCustodyDenied
	}
	principal, err := runtime.ResolvePrincipal(request)
	if err != nil || !strideE10CompleteOrganizationPrincipal(principal) {
		return MyMindPrivateAuthority{}, ErrMyMindCustodyDenied
	}
	token := sessionTokenFromRequest(request)
	if token == "" {
		return MyMindPrivateAuthority{}, ErrMyMindCustodyDenied
	}
	sessionDigest := hashResetToken(token)
	organization := runtime.organization
	organization.mu.RLock()
	person, personOK := organization.persons[principal.PersonID]
	membership, membershipOK := organization.memberships[principal.OrganizationMembershipID]
	var activeSession ActiveOrganizationSession
	for _, candidate := range organization.sessions {
		if candidate.SessionSubjectDigest == sessionDigest {
			activeSession = candidate
			break
		}
	}
	organization.mu.RUnlock()
	if !personOK || !membershipOK || activeSession.Header.ID == "" || activeSession.SessionRevision != principal.ActiveOrganizationSessionRev {
		return MyMindPrivateAuthority{}, ErrMyMindCustodyDenied
	}
	resolved, err := ResolveMyMindPrivateAuthority(person, membership, activeSession, runtime.now().UTC())
	if err != nil {
		return MyMindPrivateAuthority{}, ErrMyMindCustodyDenied
	}
	if resolved.PersonID != principal.PersonID || resolved.OrganizationID != principal.ActiveOrganizationID || resolved.MembershipID != principal.OrganizationMembershipID || resolved.MembershipRevision != principal.OrganizationMembershipRev || resolved.SessionSubjectDigest != sessionDigest || resolved.SessionRevision != principal.ActiveOrganizationSessionRev {
		return MyMindPrivateAuthority{}, ErrMyMindCustodyDenied
	}
	return resolved, nil
}

func registerStrideE10W5ProductRoutes(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("/api/mymind/v1/", strideE10W5ProductHandler)
}

var _ MyMindPrivateAuthorityResolver = (*strideE10W5CanonicalAuthorityResolver)(nil)
