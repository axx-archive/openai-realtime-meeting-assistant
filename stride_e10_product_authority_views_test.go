package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestStrideE10OrganizationViewsFilterAndDeepClone(t *testing.T) {
	s := NewOrganizationAuthorityService()
	s.persons["person_viewer"] = PersonPrincipal{Status: "active"}
	s.persons["person_target"] = PersonPrincipal{Status: "active"}
	s.profiles["person_viewer"] = PersonProfile{PersonID: "person_viewer", Status: "active", WorkModes: []string{"async"}}
	s.profiles["person_target"] = PersonProfile{PersonID: "person_target", Status: "active", WorkModes: []string{"paired"}, OpenTo: []string{"employment"}, OpenToEnabled: true, VisibleOrganizationIDs: []string{"org_private"}}
	s.organizations["org_visible"] = Organization{Header: STRIDEContractHeader{ID: "org_visible"}, Status: "active"}
	s.organizations["org_hidden"] = Organization{Header: STRIDEContractHeader{ID: "org_hidden"}, Status: "active"}
	s.memberships["membership_viewer"] = OrganizationMembership{Header: STRIDEContractHeader{ID: "membership_viewer", Revision: 2}, PersonID: "person_viewer", OrganizationID: "org_visible", Role: "admin", Status: "active"}
	s.memberships["membership_target"] = OrganizationMembership{Header: STRIDEContractHeader{ID: "membership_target", Revision: 4}, PersonID: "person_target", OrganizationID: "org_visible", Role: "member", Status: "active"}
	s.memberships["membership_hidden"] = OrganizationMembership{Header: STRIDEContractHeader{ID: "membership_hidden", Revision: 1}, PersonID: "person_target", OrganizationID: "org_hidden", Role: "owner", Status: "active"}
	s.memberProfiles["membership_target"] = OrganizationMemberProfile{Header: STRIDEContractHeader{ID: "profile_target", Revision: 1}, PersonID: "person_target", OrganizationID: "org_visible", MembershipID: "membership_target", MembershipRevision: 4, Team: "Platform"}
	s.joinRequests["request_visible"] = OrganizationJoinRequest{Header: STRIDEContractHeader{ID: "request_visible"}, OrganizationID: "org_visible"}
	s.joinRequests["request_hidden"] = OrganizationJoinRequest{Header: STRIDEContractHeader{ID: "request_hidden"}, OrganizationID: "org_hidden"}
	s.audit["org_visible"] = []OrganizationAuditEvent{{Header: STRIDEContractHeader{ID: "audit_visible"}}}

	viewer := StrideE10AuthorityViewer{PersonID: "person_viewer", OrganizationID: "org_visible", MembershipID: "membership_viewer", MembershipRevision: 2}
	profile, member, err := s.ReadStrideE10CoworkerProfile(viewer, "person_target")
	if err != nil || member.Team != "Platform" {
		t.Fatalf("coworker view = %+v %+v err=%v", profile, member, err)
	}
	if profile.OpenToEnabled || len(profile.OpenTo) != 0 || len(profile.VisibleOrganizationIDs) != 0 {
		t.Fatalf("coworker projection leaked private global visibility: %+v", profile)
	}
	profile.WorkModes[0] = "mutated"
	if s.profiles["person_target"].WorkModes[0] != "paired" {
		t.Fatal("coworker view aliases stored profile")
	}
	admin, err := s.ReadStrideE10OrganizationAdminView(viewer)
	if err != nil || len(admin.Memberships) != 2 || len(admin.JoinRequests) != 1 || len(admin.Audit) != 1 {
		t.Fatalf("admin view = %+v err=%v", admin, err)
	}
	admin.Audit[0].Header.ID = "mutated"
	if s.audit["org_visible"][0].Header.ID != "audit_visible" {
		t.Fatal("admin view aliases audit history")
	}
	viewer.MembershipRevision++
	if _, err := s.ReadStrideE10OrganizationAdminView(viewer); !errors.Is(err, ErrOrganizationAuthorityNotFound) {
		t.Fatalf("stale viewer err=%v", err)
	}
	if _, _, err := s.ReadStrideE10CoworkerProfile(StrideE10AuthorityViewer{PersonID: "person_viewer", OrganizationID: "org_visible", MembershipID: "membership_viewer", MembershipRevision: 2}, "person_absent"); !errors.Is(err, ErrOrganizationAuthorityNotFound) {
		t.Fatalf("cross-tenant/absent coworker err=%v", err)
	}
}

func TestStrideE10ContributionViewsBindControllerAndScope(t *testing.T) {
	controller := STRIDEControllerRevision{PrincipalID: "person_subject", AuthorityID: "authority_subject", AuthorityRevision: 1, PolicyRevision: 1}
	s, err := NewContributionAuthorityService([]ContributionAuthorityGrant{{ID: "grant_subject", Role: "subject", PersonID: "person_subject", Controller: controller}})
	if err != nil {
		t.Fatal(err)
	}
	s.claims["claim_b"] = ContributionClaim{Header: STRIDEContractHeader{ID: "claim_b"}, OrganizationID: "org_one", SubjectPersonID: "person_subject", SourceRefs: []STRIDEReference{{ID: "source", Revision: 1}}}
	s.claims["claim_a"] = ContributionClaim{Header: STRIDEContractHeader{ID: "claim_a"}, OrganizationID: "org_two", SubjectPersonID: "person_subject", SourceRefs: []STRIDEReference{{ID: "source", Revision: 1}}}
	s.claims["claim_hidden"] = ContributionClaim{Header: STRIDEContractHeader{ID: "claim_hidden"}, OrganizationID: "org_one", SubjectPersonID: "person_other"}
	s.approvals["approval_visible"] = FieldReleaseApproval{Header: STRIDEContractHeader{ID: "approval_visible"}, OrganizationID: "org_one", SubjectPersonID: "person_subject", RequiredPartyIDs: []string{"party_one"}}
	s.approvals["approval_hidden"] = FieldReleaseApproval{Header: STRIDEContractHeader{ID: "approval_hidden"}, OrganizationID: "org_one", SubjectPersonID: "person_other"}
	s.publications["publication_visible"] = PublishedContributionClaim{Header: STRIDEContractHeader{ID: "publication_visible"}, SubjectPersonID: "person_subject", Attestations: []STRIDEReference{{ID: "attestation_visible", Revision: 1, Digest: "digest"}}}

	view, err := s.ReadStrideE10ContributionView(StrideE10ContributionViewScope{GrantID: "grant_subject", Controller: controller})
	if err != nil || len(view.Claims) != 2 || view.Claims[0].Header.ID != "claim_a" || len(view.Approvals) != 1 || len(view.Publications) != 1 {
		t.Fatalf("subject view = %+v err=%v", view, err)
	}
	view.Claims[0].SourceRefs[0].ID = "mutated"
	if s.claims["claim_a"].SourceRefs[0].ID != "source" {
		t.Fatal("contribution view aliases source references")
	}
	wrong := controller
	wrong.AuthorityRevision++
	if _, err := s.ReadStrideE10ContributionView(StrideE10ContributionViewScope{GrantID: "grant_subject", Controller: wrong}); !errors.Is(err, ErrContributionAuthorityDenied) {
		t.Fatalf("wrong controller err=%v", err)
	}
	if _, err := (*ContributionAuthorityService)(nil).ReadStrideE10ContributionView(StrideE10ContributionViewScope{}); !errors.Is(err, ErrContributionAuthorityInvalid) {
		t.Fatalf("nil service err=%v", err)
	}
}

func TestStrideE10OrganizationContributionViewRequiresCurrentAdmin(t *testing.T) {
	controller := STRIDEControllerRevision{PrincipalID: "person_admin", AuthorityID: "authority_review", AuthorityRevision: 1, PolicyRevision: 1}
	s, err := NewContributionAuthorityService([]ContributionAuthorityGrant{{ID: "grant_review", Role: "organization_reviewer", OrganizationID: "org_one", Controller: controller}})
	if err != nil {
		t.Fatal(err)
	}
	s.claims["claim_visible"] = ContributionClaim{Header: STRIDEContractHeader{ID: "claim_visible"}, OrganizationID: "org_one", SubjectPersonID: "person_subject"}
	s.claims["claim_hidden"] = ContributionClaim{Header: STRIDEContractHeader{ID: "claim_hidden"}, OrganizationID: "org_two", SubjectPersonID: "person_other"}
	organizations := NewOrganizationAuthorityService()
	organizations.memberships["membership_admin"] = OrganizationMembership{Header: STRIDEContractHeader{ID: "membership_admin", Revision: 2}, PersonID: "person_admin", OrganizationID: "org_one", Role: "admin", Status: "active"}
	viewer := StrideE10AuthorityViewer{PersonID: "person_admin", OrganizationID: "org_one", MembershipID: "membership_admin", MembershipRevision: 2}
	scope := StrideE10ContributionViewScope{GrantID: "grant_review", Controller: controller}
	if _, err := s.ReadStrideE10ContributionView(scope); !errors.Is(err, ErrContributionAuthorityDenied) {
		t.Fatalf("static organization grant bypass err=%v", err)
	}
	view, err := s.ReadStrideE10OrganizationContributionView(organizations, viewer, scope)
	if err != nil || len(view.Claims) != 1 || view.Claims[0].Header.ID != "claim_visible" {
		t.Fatalf("organization contribution view=%+v err=%v", view, err)
	}
	revoked := organizations.memberships["membership_admin"]
	revoked.Status = "revoked"
	organizations.memberships["membership_admin"] = revoked
	if _, err := s.ReadStrideE10OrganizationContributionView(organizations, viewer, scope); !errors.Is(err, ErrContributionAuthorityDenied) {
		t.Fatalf("revoked admin retained approval visibility: %v", err)
	}
}

func TestStrideE10NetworkViewsFilterAuthorityAndDeepClone(t *testing.T) {
	s := NewNetworkAuthority(nil)
	s.membershipAuthorities["membership_one"] = NetworkMembershipAuthority{MembershipID: "membership_one", OrganizationID: "org_one", PersonID: "person_one", Revision: 3, Active: true}
	s.membershipAuthorities["membership_two"] = NetworkMembershipAuthority{MembershipID: "membership_two", OrganizationID: "org_two", PersonID: "person_two", Revision: 1, Active: true}
	s.profiles["profile_one"] = NetworkProfileProjection{Header: STRIDEContractHeader{ID: "profile_one"}, SubjectPersonID: "person_one", Fields: []NetworkPublishedField{{FieldKey: "bio"}}}
	s.profiles["profile_hidden"] = NetworkProfileProjection{Header: STRIDEContractHeader{ID: "profile_hidden"}, SubjectPersonID: "person_two"}
	s.grants["grant_one"] = TalentSearchGrant{Header: STRIDEContractHeader{ID: "grant_one"}, OrganizationID: "org_one", MembershipID: "membership_one", SearcherPersonID: "person_one"}
	s.grants["grant_hidden"] = TalentSearchGrant{Header: STRIDEContractHeader{ID: "grant_hidden"}, OrganizationID: "org_two", MembershipID: "membership_two", SearcherPersonID: "person_two"}
	s.searchReceipts["receipt_one"] = NetworkSearchReceipt{Header: STRIDEContractHeader{ID: "receipt_one"}, OrganizationID: "org_one", Grant: STRIDEReference{ID: "grant_one"}, PolicyReasonCodes: []string{"allowed"}}
	s.searchReceipts["receipt_hidden"] = NetworkSearchReceipt{Header: STRIDEContractHeader{ID: "receipt_hidden"}, OrganizationID: "org_two", Grant: STRIDEReference{ID: "grant_hidden"}}
	s.contacts["contact_one"] = ContactRequest{Header: STRIDEContractHeader{ID: "contact_one"}, SenderOrganizationID: "org_one", SenderPersonID: "person_one", RecipientPersonID: "person_two"}
	s.contacts["contact_hidden"] = ContactRequest{Header: STRIDEContractHeader{ID: "contact_hidden"}, SenderOrganizationID: "org_two", SenderPersonID: "person_two", RecipientPersonID: "person_three"}

	person, err := s.ReadStrideE10NetworkPersonView("person_one")
	if err != nil || len(person.Profiles) != 1 || len(person.Contacts) != 1 {
		t.Fatalf("person network view=%+v err=%v", person, err)
	}
	person.Profiles[0].Fields[0].FieldKey = "mutated"
	if s.profiles["profile_one"].Fields[0].FieldKey != "bio" {
		t.Fatal("network person view aliases fields")
	}
	viewer := StrideE10AuthorityViewer{PersonID: "person_one", OrganizationID: "org_one", MembershipID: "membership_one", MembershipRevision: 3}
	organization, err := s.ReadStrideE10NetworkOrganizationView(viewer)
	if err != nil || len(organization.Grants) != 1 || len(organization.SearchReceipts) != 1 || len(organization.Contacts) != 1 {
		t.Fatalf("organization network view=%+v err=%v", organization, err)
	}
	organization.SearchReceipts[0].PolicyReasonCodes[0] = "mutated"
	if s.searchReceipts["receipt_one"].PolicyReasonCodes[0] != "allowed" {
		t.Fatal("network organization view aliases receipt")
	}
	viewer.MembershipRevision++
	if _, err := s.ReadStrideE10NetworkOrganizationView(viewer); !errors.Is(err, ErrNetworkAuthorityDenied) {
		t.Fatalf("stale/cross-tenant viewer err=%v", err)
	}
}

func TestStrideE10RecruitingAdminViewIsOrgWideAndBodyMinimized(t *testing.T) {
	organizations := NewOrganizationAuthorityService()
	organizations.memberships["membership_admin"] = OrganizationMembership{Header: STRIDEContractHeader{ID: "membership_admin", Revision: 5}, PersonID: "person_admin", OrganizationID: "org_one", Role: "owner", Status: "active"}
	organizations.memberships["membership_member"] = OrganizationMembership{Header: STRIDEContractHeader{ID: "membership_member", Revision: 1}, PersonID: "person_member", OrganizationID: "org_one", Role: "member", Status: "active"}
	network := NewNetworkAuthority(nil)
	digest := strings.Repeat("a", 64)
	network.grants["grant_a"] = TalentSearchGrant{Header: STRIDEContractHeader{ID: "grant_a", Revision: 1, ContractType: STRIDEContractTalentSearchGrant, ContentDigest: digest}, OrganizationID: "org_one", MembershipID: "membership_searcher_a", SearcherPersonID: "person_searcher_a", State: "active"}
	network.grants["grant_b"] = TalentSearchGrant{Header: STRIDEContractHeader{ID: "grant_b", Revision: 1, ContractType: STRIDEContractTalentSearchGrant, ContentDigest: digest}, OrganizationID: "org_one", MembershipID: "membership_searcher_b", SearcherPersonID: "person_searcher_b", State: "revoked"}
	network.grants["grant_hidden"] = TalentSearchGrant{Header: STRIDEContractHeader{ID: "grant_hidden", Revision: 1, ContractType: STRIDEContractTalentSearchGrant, ContentDigest: digest}, OrganizationID: "org_two", MembershipID: "membership_other", SearcherPersonID: "person_other"}
	network.searchReceipts["search_visible"] = NetworkSearchReceipt{Header: STRIDEContractHeader{ID: "search_visible", Revision: 2}, OrganizationID: "org_one", Grant: STRIDEReference{ID: "grant_a", Revision: 1, ContractType: STRIDEContractTalentSearchGrant, Digest: digest}, OriginalQueryDigest: strings.Repeat("b", 64), PolicyVerdict: "allow", PolicyReasonCodes: []string{"safe"}, Results: []NetworkSearchResultReason{{Why: []string{"matched"}}}}
	network.contacts["contact_visible"] = ContactRequest{Header: STRIDEContractHeader{ID: "contact_visible", Revision: 3}, SenderOrganizationID: "org_one", SenderPersonID: "person_searcher_a", RecipientPersonID: "person_candidate", NoteDigest: strings.Repeat("c", 64), AcceptedChannelDigest: strings.Repeat("d", 64), CollaborationType: "employment", State: "accepted"}
	network.contacts["contact_hidden"] = ContactRequest{Header: STRIDEContractHeader{ID: "contact_hidden", Revision: 1}, SenderOrganizationID: "org_two", SenderPersonID: "person_other", RecipientPersonID: "person_candidate"}

	admin := StrideE10AuthorityViewer{PersonID: "person_admin", OrganizationID: "org_one", MembershipID: "membership_admin", MembershipRevision: 5}
	view, err := network.ReadStrideE10NetworkRecruitingAdminView(organizations, admin)
	if err != nil || len(view.Grants) != 2 || len(view.SearchReceipts) != 1 || len(view.Contacts) != 1 || view.Limits.RecordedSearches != 1 {
		t.Fatalf("recruiting admin view=%+v err=%v", view, err)
	}
	payload, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"NoteDigest":`, `"AcceptedChannelDigest":`, `"RecipientPersonID":`, `"OriginalQueryDigest":`, `"Results":`, `"results":`, "person_candidate", strings.Repeat("c", 64), strings.Repeat("d", 64)} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("recruiting admin view leaked %q: %s", forbidden, payload)
		}
	}
	member := StrideE10AuthorityViewer{PersonID: "person_member", OrganizationID: "org_one", MembershipID: "membership_member", MembershipRevision: 1}
	if _, err := network.ReadStrideE10NetworkRecruitingAdminView(organizations, member); !errors.Is(err, ErrNetworkAuthorityDenied) {
		t.Fatalf("ordinary member admin view err=%v", err)
	}
	admin.MembershipRevision--
	if _, err := network.ReadStrideE10NetworkRecruitingAdminView(organizations, admin); !errors.Is(err, ErrNetworkAuthorityDenied) {
		t.Fatalf("stale admin view err=%v", err)
	}
}

func TestStrideE10AuthorityViewsAreNilSafe(t *testing.T) {
	if _, err := (*OrganizationAuthorityService)(nil).ReadStrideE10SelfOrganizationView("person_one"); !errors.Is(err, ErrOrganizationAuthorityInvalid) {
		t.Fatalf("org nil err=%v", err)
	}
	if _, err := (*NetworkAuthority)(nil).ReadStrideE10NetworkPersonView("person_one"); !errors.Is(err, ErrNetworkAuthorityInvalid) {
		t.Fatalf("network nil err=%v", err)
	}
}
