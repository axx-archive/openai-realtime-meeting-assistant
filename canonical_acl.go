package main

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// ACLAction is the closed action vocabulary used by the canonical security
// boundary. Callers must authorize before fetching object bodies.
type ACLAction string

const (
	ACLReadMetadata ACLAction = "read_metadata"
	ACLReadContent  ACLAction = "read_content"
	ACLCreateChild  ACLAction = "create_child"
	ACLWrite        ACLAction = "write"
	ACLDelete       ACLAction = "delete"
	ACLShare        ACLAction = "share"
	ACLApprove      ACLAction = "approve"
	ACLExecute      ACLAction = "execute"
	ACLExport       ACLAction = "export"
	ACLManage       ACLAction = "manage_acl"
)

type ACLPrincipalKind string

const (
	ACLPrincipalUser       ACLPrincipalKind = "user"
	ACLPrincipalGuest      ACLPrincipalKind = "guest"
	ACLPrincipalService    ACLPrincipalKind = "service"
	ACLPrincipalCapability ACLPrincipalKind = "public_capability"
)

type ACLPrincipal struct {
	TenantID  string
	ID        string
	Kind      ACLPrincipalKind
	TeamIDs   []string
	RoomID    string
	SittingID string
}

type ACLObjectRef struct {
	TenantID   string
	Type       string
	ID         string
	ACLVersion int64
}

// ACLRevisionRef is mandatory for content-affecting actions. Both revision
// and digest must match; a learned object id or blob hash is never authority.
type ACLRevisionRef struct {
	ContentRevision int64
	ContentDigest   string
}

type ACLObject struct {
	Ref                    ACLObjectRef
	RoomID                 string
	SittingID              string
	Deleted                bool
	GuestLiveAccess        bool
	CurrentContentRevision int64
	CurrentContentDigest   string
	RequiredConsentScopes  []string
}

type ACLSubjectKind string

const (
	ACLSubjectPrincipal ACLSubjectKind = "principal"
	ACLSubjectTeam      ACLSubjectKind = "team"
)

type ACLGrant struct {
	ID          string
	TenantID    string
	ObjectType  string
	ObjectID    string
	ACLVersion  int64
	SubjectKind ACLSubjectKind
	SubjectID   string
	// SubjectPrincipalKind prevents a service/capability whose identifier
	// collides with a user's identifier from inheriting that user's grant.
	// Required when SubjectKind is principal.
	SubjectPrincipalKind ACLPrincipalKind
	Actions              []ACLAction
	RoomID               string
	SittingID            string
	ExpiresAt            *time.Time
	RevokedAt            *time.Time
	Obligations          []string
}

type ACLDecision struct {
	Allowed        bool
	DenialCode     string // safe for a public response; object denials are opaque
	PolicyReason   string // operator/audit detail; never return directly to clients
	MatchedGrantID string
	ACLVersion     int64
	Obligations    []string
}

const (
	ACLDenialNotFound        = "not_found"
	ACLDenialUnauthenticated = "unauthenticated"
	ACLDenialUnavailable     = "unavailable"
)

var ErrACLObjectNotFound = errors.New("canonical ACL object not found")

type ACLStore interface {
	ResolveACLObject(context.Context, ACLObjectRef) (ACLObject, error)
	ListACLGrants(context.Context, ACLObjectRef) ([]ACLGrant, error)
}

type ACLConsentChecker interface {
	HasConsent(context.Context, ACLPrincipal, ACLObject, string) (bool, error)
}

type AuthorizationKernel struct {
	Store   ACLStore
	Consent ACLConsentChecker
	Now     func() time.Time
}

func (k AuthorizationKernel) Authorize(ctx context.Context, principal ACLPrincipal, action ACLAction, ref ACLObjectRef, revision ACLRevisionRef) ACLDecision {
	deny := func(code, reason string, version int64, obligations ...string) ACLDecision {
		return ACLDecision{DenialCode: code, PolicyReason: reason, ACLVersion: version, Obligations: uniqueSortedStrings(obligations)}
	}
	if !validACLAction(action) {
		return deny(ACLDenialNotFound, "unknown action", ref.ACLVersion)
	}
	if strings.TrimSpace(principal.ID) == "" || strings.TrimSpace(principal.TenantID) == "" || !validACLPrincipalKind(principal.Kind) {
		return deny(ACLDenialUnauthenticated, "principal is missing or invalid", ref.ACLVersion)
	}
	if strings.TrimSpace(ref.TenantID) == "" || strings.TrimSpace(ref.Type) == "" || strings.TrimSpace(ref.ID) == "" || ref.ACLVersion < 1 {
		return deny(ACLDenialNotFound, "object reference is incomplete", ref.ACLVersion)
	}
	if principal.TenantID != ref.TenantID {
		return deny(ACLDenialNotFound, "tenant mismatch", ref.ACLVersion)
	}
	if k.Store == nil {
		return deny(ACLDenialUnavailable, "authorization store is unavailable", ref.ACLVersion)
	}
	object, err := k.Store.ResolveACLObject(ctx, ref)
	if err != nil {
		if errors.Is(err, ErrACLObjectNotFound) {
			return deny(ACLDenialNotFound, "object does not exist", ref.ACLVersion)
		}
		return deny(ACLDenialUnavailable, "authorization store failed", ref.ACLVersion)
	}
	if object.Deleted || object.Ref.TenantID != ref.TenantID || object.Ref.Type != ref.Type || object.Ref.ID != ref.ID || object.Ref.ACLVersion != ref.ACLVersion {
		return deny(ACLDenialNotFound, "object identity or ACL version is stale", object.Ref.ACLVersion)
	}
	if actionRequiresRevision(action) && (revision.ContentRevision < 1 || strings.TrimSpace(revision.ContentDigest) == "" ||
		revision.ContentRevision != object.CurrentContentRevision || revision.ContentDigest != object.CurrentContentDigest) {
		return deny(ACLDenialNotFound, "content revision is stale", object.Ref.ACLVersion)
	}
	if principal.Kind == ACLPrincipalGuest {
		if !object.GuestLiveAccess || strings.TrimSpace(principal.RoomID) == "" || strings.TrimSpace(principal.SittingID) == "" ||
			principal.RoomID != object.RoomID || principal.SittingID != object.SittingID || guestForbiddenAction(action) {
			return deny(ACLDenialNotFound, "guest is outside the live room/sitting scope", object.Ref.ACLVersion)
		}
	}

	grants, err := k.Store.ListACLGrants(ctx, object.Ref)
	if err != nil {
		return deny(ACLDenialUnavailable, "grant lookup failed", object.Ref.ACLVersion)
	}
	now := time.Now().UTC()
	if k.Now != nil {
		now = k.Now().UTC()
	}
	sort.SliceStable(grants, func(i, j int) bool { return grants[i].ID < grants[j].ID })
	for _, grant := range grants {
		if !grantApplies(grant, principal, object, action, now) {
			continue
		}
		obligations := append([]string{"audit"}, grant.Obligations...)
		for _, scope := range object.RequiredConsentScopes {
			if k.Consent == nil {
				return deny(ACLDenialNotFound, "consent checker unavailable", object.Ref.ACLVersion, "consent_required")
			}
			ok, checkErr := k.Consent.HasConsent(ctx, principal, object, scope)
			if checkErr != nil || !ok {
				return deny(ACLDenialNotFound, "required consent is absent", object.Ref.ACLVersion, "consent_required")
			}
		}
		return ACLDecision{Allowed: true, MatchedGrantID: grant.ID, ACLVersion: object.Ref.ACLVersion, Obligations: uniqueSortedStrings(obligations)}
	}
	return deny(ACLDenialNotFound, "no explicit active grant", object.Ref.ACLVersion)
}

func validACLAction(action ACLAction) bool {
	switch action {
	case ACLReadMetadata, ACLReadContent, ACLCreateChild, ACLWrite, ACLDelete, ACLShare, ACLApprove, ACLExecute, ACLExport, ACLManage:
		return true
	default:
		return false
	}
}

func validACLPrincipalKind(kind ACLPrincipalKind) bool {
	switch kind {
	case ACLPrincipalUser, ACLPrincipalGuest, ACLPrincipalService, ACLPrincipalCapability:
		return true
	default:
		return false
	}
}

func actionRequiresRevision(action ACLAction) bool {
	return action != ACLReadMetadata && action != ACLCreateChild && action != ACLManage
}

func guestForbiddenAction(action ACLAction) bool {
	switch action {
	case ACLDelete, ACLShare, ACLApprove, ACLExecute, ACLExport, ACLManage:
		return true
	default:
		return false
	}
}

func grantApplies(grant ACLGrant, principal ACLPrincipal, object ACLObject, action ACLAction, now time.Time) bool {
	if grant.TenantID != object.Ref.TenantID || grant.ObjectType != object.Ref.Type || grant.ObjectID != object.Ref.ID || grant.ACLVersion != object.Ref.ACLVersion ||
		grant.RevokedAt != nil || (grant.ExpiresAt != nil && !now.Before(grant.ExpiresAt.UTC())) || !containsACLAction(grant.Actions, action) {
		return false
	}
	switch grant.SubjectKind {
	case ACLSubjectPrincipal:
		if grant.SubjectID != principal.ID || grant.SubjectPrincipalKind != principal.Kind {
			return false
		}
	case ACLSubjectTeam:
		found := false
		for _, teamID := range principal.TeamIDs {
			if teamID == grant.SubjectID {
				found = true
				break
			}
		}
		if !found || principal.Kind == ACLPrincipalGuest || principal.Kind == ACLPrincipalCapability {
			return false
		}
	default:
		return false
	}
	if principal.Kind == ACLPrincipalGuest {
		return grant.RoomID == object.RoomID && grant.SittingID == object.SittingID && grant.RoomID == principal.RoomID && grant.SittingID == principal.SittingID
	}
	return true
}

func containsACLAction(actions []ACLAction, wanted ACLAction) bool {
	for _, action := range actions {
		if action == wanted {
			return true
		}
	}
	return false
}

func uniqueSortedStrings(values []string) []string {
	set := map[string]struct{}{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// MemoryACLStore is a test/development implementation. Production W1 uses the
// same interface with a transactional PostgreSQL implementation.
type MemoryACLStore struct {
	mu      sync.RWMutex
	Objects map[string]ACLObject
	Grants  map[string][]ACLGrant
}

func aclObjectKey(ref ACLObjectRef) string { return ref.TenantID + "\x00" + ref.Type + "\x00" + ref.ID }

func (s *MemoryACLStore) ResolveACLObject(_ context.Context, ref ACLObjectRef) (ACLObject, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	object, ok := s.Objects[aclObjectKey(ref)]
	if !ok {
		return ACLObject{}, ErrACLObjectNotFound
	}
	return object, nil
}

func (s *MemoryACLStore) ListACLGrants(_ context.Context, ref ACLObjectRef) ([]ACLGrant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]ACLGrant(nil), s.Grants[aclObjectKey(ref)]...), nil
}
