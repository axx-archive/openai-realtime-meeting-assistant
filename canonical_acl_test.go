package main

import (
	"context"
	"testing"
	"time"
)

type aclConsentStub struct{ allowed bool }

func (s aclConsentStub) HasConsent(context.Context, ACLPrincipal, ACLObject, string) (bool, error) {
	return s.allowed, nil
}

func testACLFixture() (*MemoryACLStore, ACLObjectRef, ACLObject, ACLPrincipal) {
	ref := ACLObjectRef{TenantID: "bonfire", Type: "artifact", ID: "artifact-1", ACLVersion: 3}
	object := ACLObject{Ref: ref, CurrentContentRevision: 7, CurrentContentDigest: "sha256:body-v7"}
	principal := ACLPrincipal{TenantID: "bonfire", ID: "alice", Kind: ACLPrincipalUser, TeamIDs: []string{"company"}}
	store := &MemoryACLStore{Objects: map[string]ACLObject{aclObjectKey(ref): object}, Grants: map[string][]ACLGrant{}}
	return store, ref, object, principal
}

func TestAuthorizationDefaultsDenyWithoutImplicitOwnerOrAdminBypass(t *testing.T) {
	store, ref, _, principal := testACLFixture()
	kernel := AuthorizationKernel{Store: store}
	decision := kernel.Authorize(context.Background(), principal, ACLReadContent, ref, ACLRevisionRef{ContentRevision: 7, ContentDigest: "sha256:body-v7"})
	if decision.Allowed || decision.DenialCode != ACLDenialNotFound {
		t.Fatalf("decision=%+v, want opaque default deny", decision)
	}

	// Even team membership (including a caller that an HTTP layer calls admin)
	// is not authority until an explicit grant exists.
	principal.TeamIDs = append(principal.TeamIDs, "admins")
	if got := kernel.Authorize(context.Background(), principal, ACLManage, ref, ACLRevisionRef{}); got.Allowed {
		t.Fatalf("admin-like principal bypassed grants: %+v", got)
	}
}

func TestAuthorizationRequiresExactTenantACLAndContentRevision(t *testing.T) {
	store, ref, _, principal := testACLFixture()
	store.Grants[aclObjectKey(ref)] = []ACLGrant{{
		ID: "grant-1", TenantID: ref.TenantID, ObjectType: ref.Type, ObjectID: ref.ID, ACLVersion: ref.ACLVersion,
		SubjectKind: ACLSubjectPrincipal, SubjectID: principal.ID, SubjectPrincipalKind: ACLPrincipalUser, Actions: []ACLAction{ACLReadContent},
	}}
	kernel := AuthorizationKernel{Store: store}
	exact := ACLRevisionRef{ContentRevision: 7, ContentDigest: "sha256:body-v7"}
	if got := kernel.Authorize(context.Background(), principal, ACLReadContent, ref, exact); !got.Allowed || got.MatchedGrantID != "grant-1" {
		t.Fatalf("exact decision=%+v, want allow", got)
	}
	for name, candidate := range map[string]ACLRevisionRef{
		"old revision": {ContentRevision: 6, ContentDigest: exact.ContentDigest},
		"wrong digest": {ContentRevision: 7, ContentDigest: "sha256:other"},
		"blank":        {},
	} {
		if got := kernel.Authorize(context.Background(), principal, ACLReadContent, ref, candidate); got.Allowed || got.DenialCode != ACLDenialNotFound {
			t.Fatalf("%s decision=%+v, want opaque deny", name, got)
		}
	}
	stale := ref
	stale.ACLVersion--
	if got := kernel.Authorize(context.Background(), principal, ACLReadContent, stale, exact); got.Allowed || got.DenialCode != ACLDenialNotFound {
		t.Fatalf("stale ACL decision=%+v", got)
	}
	otherTenant := principal
	otherTenant.TenantID = "other"
	if got := kernel.Authorize(context.Background(), otherTenant, ACLReadContent, ref, exact); got.Allowed || got.DenialCode != ACLDenialNotFound {
		t.Fatalf("cross-tenant decision=%+v", got)
	}
}

func TestAuthorizationHonorsExpiryRevocationConsentAndObligations(t *testing.T) {
	store, ref, object, principal := testACLFixture()
	now := time.Date(2026, 7, 12, 20, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	store.Grants[aclObjectKey(ref)] = []ACLGrant{{
		ID: "grant-active", TenantID: ref.TenantID, ObjectType: ref.Type, ObjectID: ref.ID, ACLVersion: ref.ACLVersion,
		SubjectKind: ACLSubjectTeam, SubjectID: "company", Actions: []ACLAction{ACLReadContent}, ExpiresAt: &future,
		Obligations: []string{"redact", "audit"},
	}}
	object.RequiredConsentScopes = []string{"model_analysis"}
	store.Objects[aclObjectKey(ref)] = object
	revision := ACLRevisionRef{ContentRevision: 7, ContentDigest: "sha256:body-v7"}
	denied := AuthorizationKernel{Store: store, Consent: aclConsentStub{allowed: false}, Now: func() time.Time { return now }}.
		Authorize(context.Background(), principal, ACLReadContent, ref, revision)
	if denied.Allowed || len(denied.Obligations) != 1 || denied.Obligations[0] != "consent_required" {
		t.Fatalf("missing-consent decision=%+v", denied)
	}
	allowed := AuthorizationKernel{Store: store, Consent: aclConsentStub{allowed: true}, Now: func() time.Time { return now }}.
		Authorize(context.Background(), principal, ACLReadContent, ref, revision)
	if !allowed.Allowed || len(allowed.Obligations) != 2 || allowed.Obligations[0] != "audit" || allowed.Obligations[1] != "redact" {
		t.Fatalf("allowed decision=%+v", allowed)
	}
	if expired := (AuthorizationKernel{Store: store, Consent: aclConsentStub{allowed: true}, Now: func() time.Time { return future }}).
		Authorize(context.Background(), principal, ACLReadContent, ref, revision); expired.Allowed {
		t.Fatalf("expired grant allowed: %+v", expired)
	}
}

func TestGuestGrantIsBoundToExactRoomAndSittingAndNeverPrivileged(t *testing.T) {
	store, ref, object, _ := testACLFixture()
	object.RoomID, object.SittingID, object.GuestLiveAccess = "room-a", "meeting-a", true
	store.Objects[aclObjectKey(ref)] = object
	guest := ACLPrincipal{TenantID: ref.TenantID, ID: "guest-session-hash", Kind: ACLPrincipalGuest, RoomID: "room-a", SittingID: "meeting-a"}
	store.Grants[aclObjectKey(ref)] = []ACLGrant{{
		ID: "guest-live", TenantID: ref.TenantID, ObjectType: ref.Type, ObjectID: ref.ID, ACLVersion: ref.ACLVersion,
		SubjectKind: ACLSubjectPrincipal, SubjectID: guest.ID, SubjectPrincipalKind: ACLPrincipalGuest, RoomID: "room-a", SittingID: "meeting-a",
		Actions: []ACLAction{ACLReadContent, ACLWrite, ACLShare, ACLExecute, ACLManage},
	}}
	kernel := AuthorizationKernel{Store: store}
	revision := ACLRevisionRef{ContentRevision: 7, ContentDigest: "sha256:body-v7"}
	if got := kernel.Authorize(context.Background(), guest, ACLReadContent, ref, revision); !got.Allowed {
		t.Fatalf("same-sitting live guest denied: %+v", got)
	}
	wrongRoom := guest
	wrongRoom.RoomID = "room-b"
	if got := kernel.Authorize(context.Background(), wrongRoom, ACLReadContent, ref, revision); got.Allowed {
		t.Fatalf("cross-room guest allowed: %+v", got)
	}
	for _, action := range []ACLAction{ACLShare, ACLExecute, ACLManage} {
		if got := kernel.Authorize(context.Background(), guest, action, ref, revision); got.Allowed {
			t.Fatalf("guest allowed privileged action %s: %+v", action, got)
		}
	}
}

func TestPrincipalGrantBindsPrincipalKind(t *testing.T) {
	store, ref, _, user := testACLFixture()
	store.Grants[aclObjectKey(ref)] = []ACLGrant{{
		ID: "user-only", TenantID: ref.TenantID, ObjectType: ref.Type, ObjectID: ref.ID, ACLVersion: ref.ACLVersion,
		SubjectKind: ACLSubjectPrincipal, SubjectID: user.ID, SubjectPrincipalKind: ACLPrincipalUser, Actions: []ACLAction{ACLReadContent},
	}}
	revision := ACLRevisionRef{ContentRevision: 7, ContentDigest: "sha256:body-v7"}
	kernel := AuthorizationKernel{Store: store}
	capability := user
	capability.Kind = ACLPrincipalCapability
	if got := kernel.Authorize(context.Background(), capability, ACLReadContent, ref, revision); got.Allowed {
		t.Fatalf("colliding capability id inherited user grant: %+v", got)
	}
	service := user
	service.Kind = ACLPrincipalService
	if got := kernel.Authorize(context.Background(), service, ACLReadContent, ref, revision); got.Allowed {
		t.Fatalf("colliding service id inherited user grant: %+v", got)
	}
}
