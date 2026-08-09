package main

import (
	"testing"
	"time"
)

func identityOrganizationHeader(kind STRIDEContractType, tenant, id string, at time.Time) STRIDEContractHeader {
	return STRIDEContractHeader{
		TenantID: tenant, ID: id, Revision: 1, SchemaVersion: STRIDEContractSchemaVersion,
		ContractType: kind, ContentDigest: strideTestDigest("d"), CreatedAt: at,
	}
}

func TestPersonProfileIsGlobalSafeAndSelfContained(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	profile := PersonProfile{
		Header:   identityOrganizationHeader(STRIDEContractPersonProfile, STRIDEGlobalPersonTenant, "person_aj", now),
		PersonID: "person_aj", DisplayName: "AJ Hart", AvatarBlobRef: "blob_avatar_aj", Pronouns: "he/him", Bio: "Builder and decision owner.",
		WorkModes: []string{"builder", "decision_owner"}, OpenTo: []string{"collaboration", "advisory"}, OpenToEnabled: true,
		VisibleOrganizationIDs: []string{"org_bonfire"}, Status: "active", UpdatedAt: now,
	}
	if err := profile.Validate(); err != nil {
		t.Fatalf("valid profile: %v", err)
	}
	profile.Header.TenantID = "org_bonfire"
	if err := profile.Validate(); err == nil {
		t.Fatal("organization-scoped global profile accepted")
	}
	profile.Header.TenantID = STRIDEGlobalPersonTenant
	profile.OpenToEnabled = false
	if err := profile.Validate(); err == nil {
		t.Fatal("disabled open-to preference retained discoverable values")
	}
}

func TestPersonProfileOptionalListsAreEmptyOrClosedUniqueValues(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	profile := PersonProfile{Header: identityOrganizationHeader(STRIDEContractPersonProfile, STRIDEGlobalPersonTenant, "person_zero", now), PersonID: "person_zero", DisplayName: "Zero Org", Status: "active", UpdatedAt: now}
	if err := profile.Validate(); err != nil {
		t.Fatalf("empty optional lists rejected: %v", err)
	}
	for name, mutate := range map[string]func(*PersonProfile){
		"duplicate work mode":     func(value *PersonProfile) { value.WorkModes = []string{"async", "async"} },
		"invalid work mode id":    func(value *PersonProfile) { value.WorkModes = []string{"not valid"} },
		"duplicate organization":  func(value *PersonProfile) { value.VisibleOrganizationIDs = []string{"org_one", "org_one"} },
		"invalid organization id": func(value *PersonProfile) { value.VisibleOrganizationIDs = []string{"not valid"} },
		"invalid open-to enum":    func(value *PersonProfile) { value.OpenToEnabled = true; value.OpenTo = []string{"anything"} },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := profile
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("invalid optional list accepted")
			}
		})
	}
}

func TestOrganizationAndMembershipContractsAreClosed(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	organization := Organization{
		Header: identityOrganizationHeader(STRIDEContractOrganization, STRIDEGlobalPersonTenant, "org_bonfire", now),
		Name:   "Bonfire", Slug: "bonfire", Status: "active", Discoverability: "private", CreatorPersonID: "person_aj",
		PolicyRevision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := organization.Validate(); err != nil {
		t.Fatalf("valid organization: %v", err)
	}
	organization.Discoverability = "public"
	if err := organization.Validate(); err == nil {
		t.Fatal("open-web organization discoverability accepted")
	}

	membership := OrganizationMembership{
		Header:   identityOrganizationHeader(STRIDEContractOrganizationMembership, "org_bonfire", "membership_aj", now),
		PersonID: "person_aj", OrganizationID: "org_bonfire", Role: "owner", Status: "active", GrantedAt: now,
	}
	if err := membership.Validate(); err != nil {
		t.Fatalf("valid membership: %v", err)
	}
	membership.Role = "contractor"
	if err := membership.Validate(); err == nil {
		t.Fatal("legacy non-MVP organization role accepted")
	}
}

func TestOrganizationMemberProfileIsMembershipScoped(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	profile := OrganizationMemberProfile{
		Header:   identityOrganizationHeader(STRIDEContractOrganizationMemberProfile, "org_bonfire", "member_profile_aj", now),
		PersonID: "person_aj", OrganizationID: "org_bonfire", MembershipID: "membership_aj", MembershipRevision: 2,
		Title: "Founder", Team: "Product", JoinedAt: now.Add(-time.Hour), UpdatedByMembershipID: "membership_admin",
		UpdatedByMembershipRevision: 3, UpdatedAt: now,
	}
	if err := profile.Validate(); err != nil {
		t.Fatalf("valid member profile: %v", err)
	}
	profile.Header.TenantID = STRIDEGlobalPersonTenant
	if err := profile.Validate(); err == nil {
		t.Fatal("global-tenant organization member profile accepted")
	}
}

func TestOrganizationJoinRequestTerminalAuthority(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	request := OrganizationJoinRequest{
		Header:   identityOrganizationHeader(STRIDEContractOrganizationJoinRequest, "org_bonfire", "join_tim", now),
		PersonID: "person_tim", OrganizationID: "org_bonfire", Status: "pending", RequestedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid pending request: %v", err)
	}
	decided := now.Add(time.Hour)
	request.Status = "approved"
	request.DecidedAt = &decided
	if err := request.Validate(); err == nil {
		t.Fatal("approval without deciding membership accepted")
	}
	request.DecidedByMembershipID = "membership_aj"
	request.DecisionReasonDigest = strideTestDigest("e")
	if err := request.Validate(); err != nil {
		t.Fatalf("valid approved request: %v", err)
	}
}

func TestActiveOrganizationSessionBindsExactMembershipRevision(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	binding := ActiveOrganizationSession{
		Header:               identityOrganizationHeader(STRIDEContractActiveOrganizationSession, STRIDEGlobalPersonTenant, "session_binding_aj", now),
		SessionSubjectDigest: strideTestDigest("f"), PersonID: "person_aj", OrganizationID: "org_bonfire", MembershipID: "membership_aj",
		MembershipRevision: 2, SessionRevision: 3, Status: "active", BoundAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour),
	}
	if err := binding.Validate(); err != nil {
		t.Fatalf("valid session binding: %v", err)
	}
	binding.MembershipRevision = 0
	if err := binding.Validate(); err == nil {
		t.Fatal("session without exact membership revision accepted")
	}
	binding.MembershipRevision = 2
	expiredAt := binding.ExpiresAt
	binding.Header.CreatedAt = expiredAt
	binding.Status = "expired"
	binding.InvalidatedAt = &expiredAt
	if err := binding.Validate(); err != nil {
		t.Fatalf("valid expired session binding: %v", err)
	}
}

func TestOrganizationAuditEventIsBodyFreeAndRevisionBound(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	event := OrganizationAuditEvent{
		Header:         identityOrganizationHeader(STRIDEContractOrganizationAuditEvent, "org_bonfire", "audit_approve_tim", now),
		OrganizationID: "org_bonfire", ActorPersonID: "person_aj", ActorMembershipID: "membership_aj", ActorMembershipRevision: 2,
		SubjectPersonID: "person_tim", Action: "approve", PriorRevision: 1, NewRevision: 2, ReasonDigest: strideTestDigest("1"),
		CorrelationID: "correlation_join_tim", IdempotencyKeyDigest: strideTestDigest("2"), OccurredAt: now,
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("valid audit event: %v", err)
	}
	event.ActorMembershipRevision = 0
	if err := event.Validate(); err == nil {
		t.Fatal("audit actor without exact membership revision accepted")
	}
}

func TestW1OrganizationContributionAndNetworkContractTypesAreClosed(t *testing.T) {
	for _, kind := range []STRIDEContractType{
		STRIDEContractPersonProfile, STRIDEContractOrganizationMemberProfile, STRIDEContractOrganization, STRIDEContractOrganizationMembership,
		STRIDEContractOrganizationJoinRequest, STRIDEContractActiveOrganizationSession, STRIDEContractOrganizationAuditEvent,
		STRIDEContractContributionClaim, STRIDEContractContributionAttestation, STRIDEContractPublishedContributionClaim,
		STRIDEContractAgentInfluenceReceipt, STRIDEContractFieldReleaseApproval, STRIDEContractNetworkProfileProjection,
		STRIDEContractTalentSearchGrant, STRIDEContractNetworkSearchReceipt, STRIDEContractContactRequest,
		STRIDEContractNetworkBlock, STRIDEContractDerivedPurgeReceipt,
	} {
		if !validSTRIDEContractType(kind) {
			t.Errorf("W1 contract type %q is not closed", kind)
		}
	}
}
