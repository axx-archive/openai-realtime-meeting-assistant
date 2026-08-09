package main

// This file exposes read-only, body-minimized snapshots for the default-off E10
// product adapters. The caller must pass a server-derived principal. Every
// method rechecks the exact current authority under the owning service lock;
// none returns a backing map or slice.

import (
	"sort"
	"time"
)

type StrideE10AuthorityViewer struct {
	PersonID           string
	OrganizationID     string
	MembershipID       string
	MembershipRevision int64
}

type StrideE10OrganizationSelfView struct {
	Profile       *PersonProfile
	Organizations []Organization
	Memberships   []OrganizationMembership
	JoinRequests  []OrganizationJoinRequest
}

type StrideE10OrganizationAdminView struct {
	Organization Organization
	Memberships  []OrganizationMembership
	Profiles     []OrganizationMemberProfile
	JoinRequests []OrganizationJoinRequest
	Audit        []OrganizationAuditEvent
}

func (s *OrganizationAuthorityService) ReadStrideE10SelfOrganizationView(personID string) (StrideE10OrganizationSelfView, error) {
	if s == nil || !strideIdentifier(personID) {
		return StrideE10OrganizationSelfView{}, ErrOrganizationAuthorityInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	person, ok := s.persons[personID]
	if !ok || person.Status != "active" {
		return StrideE10OrganizationSelfView{}, ErrOrganizationAuthorityNotFound
	}
	view := StrideE10OrganizationSelfView{}
	seenOrganizations := map[string]bool{}
	if profile, ok := s.profiles[personID]; ok {
		copy := clonePersonProfile(profile)
		view.Profile = &copy
	}
	for _, membership := range s.memberships {
		if membership.PersonID != personID {
			continue
		}
		view.Memberships = append(view.Memberships, cloneOrganizationMembership(membership))
		if organization, ok := s.organizations[membership.OrganizationID]; ok && !seenOrganizations[membership.OrganizationID] {
			view.Organizations = append(view.Organizations, cloneOrganization(organization))
			seenOrganizations[membership.OrganizationID] = true
		}
	}
	for _, request := range s.joinRequests {
		if request.PersonID == personID {
			view.JoinRequests = append(view.JoinRequests, cloneOrganizationJoinRequest(request))
		}
	}
	sort.Slice(view.Memberships, func(i, j int) bool { return view.Memberships[i].Header.ID < view.Memberships[j].Header.ID })
	sort.Slice(view.Organizations, func(i, j int) bool { return view.Organizations[i].Header.ID < view.Organizations[j].Header.ID })
	sort.Slice(view.JoinRequests, func(i, j int) bool { return view.JoinRequests[i].Header.ID < view.JoinRequests[j].Header.ID })
	return view, nil
}

func (s *OrganizationAuthorityService) ReadStrideE10CoworkerProfile(viewer StrideE10AuthorityViewer, targetPersonID string) (PersonProfile, OrganizationMemberProfile, error) {
	if s == nil || !strideIdentifier(targetPersonID) {
		return PersonProfile{}, OrganizationMemberProfile{}, ErrOrganizationAuthorityInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := s.strideE10ActiveViewerLocked(viewer, false); err != nil {
		return PersonProfile{}, OrganizationMemberProfile{}, err
	}
	for _, membership := range s.memberships {
		if membership.OrganizationID != viewer.OrganizationID || membership.PersonID != targetPersonID || membership.Status != "active" {
			continue
		}
		profile, profileOK := s.profiles[targetPersonID]
		memberProfile, memberOK := s.memberProfiles[membership.Header.ID]
		if !profileOK || profile.Status != "active" || !memberOK || memberProfile.MembershipRevision != membership.Header.Revision {
			return PersonProfile{}, OrganizationMemberProfile{}, ErrOrganizationAuthorityNotFound
		}
		// A coworker projection never reveals the person's hidden organization list
		// or global open-to preferences.
		profile.VisibleOrganizationIDs = nil
		profile.OpenTo = nil
		profile.OpenToEnabled = false
		return clonePersonProfile(profile), cloneContract(memberProfile), nil
	}
	return PersonProfile{}, OrganizationMemberProfile{}, ErrOrganizationAuthorityNotFound
}

func (s *OrganizationAuthorityService) ReadStrideE10OrganizationAdminView(viewer StrideE10AuthorityViewer) (StrideE10OrganizationAdminView, error) {
	if s == nil {
		return StrideE10OrganizationAdminView{}, ErrOrganizationAuthorityInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := s.strideE10ActiveViewerLocked(viewer, true); err != nil {
		return StrideE10OrganizationAdminView{}, err
	}
	organization, ok := s.organizations[viewer.OrganizationID]
	if !ok || organization.Status != "active" {
		return StrideE10OrganizationAdminView{}, ErrOrganizationAuthorityNotFound
	}
	view := StrideE10OrganizationAdminView{Organization: cloneOrganization(organization)}
	for _, membership := range s.memberships {
		if membership.OrganizationID != viewer.OrganizationID {
			continue
		}
		view.Memberships = append(view.Memberships, cloneOrganizationMembership(membership))
		if profile, ok := s.memberProfiles[membership.Header.ID]; ok && profile.MembershipRevision == membership.Header.Revision {
			view.Profiles = append(view.Profiles, cloneContract(profile))
		}
	}
	for _, request := range s.joinRequests {
		if request.OrganizationID == viewer.OrganizationID {
			view.JoinRequests = append(view.JoinRequests, cloneOrganizationJoinRequest(request))
		}
	}
	view.Audit = cloneContract(s.audit[viewer.OrganizationID])
	sort.Slice(view.Memberships, func(i, j int) bool { return view.Memberships[i].Header.ID < view.Memberships[j].Header.ID })
	sort.Slice(view.Profiles, func(i, j int) bool { return view.Profiles[i].Header.ID < view.Profiles[j].Header.ID })
	sort.Slice(view.JoinRequests, func(i, j int) bool { return view.JoinRequests[i].Header.ID < view.JoinRequests[j].Header.ID })
	sort.Slice(view.Audit, func(i, j int) bool { return view.Audit[i].Header.ID < view.Audit[j].Header.ID })
	return view, nil
}

func (s *OrganizationAuthorityService) strideE10ActiveViewerLocked(viewer StrideE10AuthorityViewer, administrator bool) (OrganizationMembership, error) {
	if !strideIdentifier(viewer.PersonID) || !strideIdentifier(viewer.OrganizationID) || !strideIdentifier(viewer.MembershipID) || viewer.MembershipRevision < 1 {
		return OrganizationMembership{}, ErrOrganizationAuthorityInvalid
	}
	membership, ok := s.memberships[viewer.MembershipID]
	if !ok || membership.PersonID != viewer.PersonID || membership.OrganizationID != viewer.OrganizationID || membership.Header.Revision != viewer.MembershipRevision || membership.Status != "active" {
		return OrganizationMembership{}, ErrOrganizationAuthorityNotFound
	}
	if administrator && membership.Role != "owner" && membership.Role != "admin" {
		return OrganizationMembership{}, ErrOrganizationAuthorityDenied
	}
	return membership, nil
}

type StrideE10ContributionViewScope struct {
	GrantID    string
	Controller STRIDEControllerRevision
}

type StrideE10ContributionView struct {
	Claims       []ContributionClaim
	Approvals    []FieldReleaseApproval
	Attestations []ContributionAttestation
	Publications []PublishedContributionClaim
	Influences   []AgentInfluenceReceipt
	Purges       []DerivedPurgeReceipt
}

// ReadStrideE10ControllerDecisionView exposes only the exact objects on which
// the current named party or signing issuer is itself the active controller.
// It intentionally does not broaden these roles into an organization review.
func (s *ContributionAuthorityService) ReadStrideE10ControllerDecisionView(scope StrideE10ContributionViewScope) (StrideE10ContributionView, error) {
	if s == nil || !strideIdentifier(scope.GrantID) || scope.Controller.Validate() != nil {
		return StrideE10ContributionView{}, ErrContributionAuthorityInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	grant, ok := s.grants[scope.GrantID]
	if !ok || grant.Controller != scope.Controller || !oneOf(grant.Role, "named_party", "signing_issuer") {
		return StrideE10ContributionView{}, ErrContributionAuthorityDenied
	}
	view := StrideE10ContributionView{}
	if grant.Role == "named_party" {
		for _, approval := range s.approvals {
			if approval.ApproverRole == "named_party" && approval.ApproverPartyID == grant.PartyID && approval.Controller == grant.Controller {
				view.Approvals = append(view.Approvals, cloneContract(approval))
			}
		}
		sort.Slice(view.Approvals, func(i, j int) bool { return view.Approvals[i].Header.ID < view.Approvals[j].Header.ID })
		return view, nil
	}
	for _, attestation := range s.attestations {
		if attestation.OrganizationID == grant.OrganizationID && attestation.Issuer == grant.Controller {
			view.Attestations = append(view.Attestations, cloneContract(attestation))
		}
	}
	sort.Slice(view.Attestations, func(i, j int) bool { return view.Attestations[i].Header.ID < view.Attestations[j].Header.ID })
	return view, nil
}

func (s *ContributionAuthorityService) ReadStrideE10ContributionView(scope StrideE10ContributionViewScope) (StrideE10ContributionView, error) {
	if s == nil || !strideIdentifier(scope.GrantID) || scope.Controller.Validate() != nil {
		return StrideE10ContributionView{}, ErrContributionAuthorityInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	grant, ok := s.grants[scope.GrantID]
	if !ok || grant.Controller != scope.Controller {
		return StrideE10ContributionView{}, ErrContributionAuthorityDenied
	}
	// Organization-private reads additionally require a current organization
	// administrator and must use ReadStrideE10OrganizationContributionView.
	if grant.OrganizationID != "" {
		return StrideE10ContributionView{}, ErrContributionAuthorityDenied
	}
	return s.strideE10ContributionViewLocked(grant), nil
}

// ReadStrideE10OrganizationContributionView uses the global reader lock order:
// organization authority first, then contribution authority. This prevents a
// static reviewer grant from surviving a membership revoke/demotion.
func (s *ContributionAuthorityService) ReadStrideE10OrganizationContributionView(organizations *OrganizationAuthorityService, viewer StrideE10AuthorityViewer, scope StrideE10ContributionViewScope) (StrideE10ContributionView, error) {
	if s == nil || organizations == nil || !strideIdentifier(scope.GrantID) || scope.Controller.Validate() != nil {
		return StrideE10ContributionView{}, ErrContributionAuthorityInvalid
	}
	organizations.mu.RLock()
	defer organizations.mu.RUnlock()
	if _, err := organizations.strideE10ActiveViewerLocked(viewer, true); err != nil {
		return StrideE10ContributionView{}, ErrContributionAuthorityDenied
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	grant, ok := s.grants[scope.GrantID]
	if !ok || grant.Controller != scope.Controller || grant.Role != "organization_reviewer" || grant.OrganizationID != viewer.OrganizationID {
		return StrideE10ContributionView{}, ErrContributionAuthorityDenied
	}
	return s.strideE10ContributionViewLocked(grant), nil
}

func (s *ContributionAuthorityService) strideE10ContributionViewLocked(grant ContributionAuthorityGrant) StrideE10ContributionView {
	view := StrideE10ContributionView{}
	personVisible := func(personID string) bool { return grant.PersonID != "" && grant.PersonID == personID }
	orgVisible := func(organizationID string) bool {
		return grant.OrganizationID != "" && grant.OrganizationID == organizationID
	}
	for _, value := range s.claims {
		if personVisible(value.SubjectPersonID) || orgVisible(value.OrganizationID) {
			view.Claims = append(view.Claims, cloneContract(value))
		}
	}
	for _, value := range s.approvals {
		visible := personVisible(value.SubjectPersonID) || orgVisible(value.OrganizationID)
		if grant.Role == "named_party" {
			visible = value.ApproverRole == "named_party" && value.ApproverPartyID == grant.PartyID
		}
		if visible {
			view.Approvals = append(view.Approvals, cloneContract(value))
		}
	}
	for _, value := range s.attestations {
		if personVisible(value.SubjectPersonID) || orgVisible(value.OrganizationID) {
			view.Attestations = append(view.Attestations, cloneContract(value))
		}
	}
	for _, value := range s.publications {
		if personVisible(value.SubjectPersonID) || orgVisible(s.strideE10PublicationOrganizationLocked(value)) {
			view.Publications = append(view.Publications, cloneContract(value))
		}
	}
	for _, value := range s.influences {
		if personVisible(value.SubjectPersonID) || orgVisible(value.OrganizationID) {
			view.Influences = append(view.Influences, cloneContract(value))
		}
	}
	for _, value := range s.purgeQueue {
		if personVisible(value.SubjectPersonID) || orgVisible(s.strideE10TriggerOrganizationLocked(value.Trigger)) {
			view.Purges = append(view.Purges, cloneContract(value))
		}
	}
	strideE10SortContributionView(&view)
	return view
}

func (s *ContributionAuthorityService) strideE10PublicationOrganizationLocked(value PublishedContributionClaim) string {
	for _, ref := range value.Attestations {
		if attestation, ok := s.attestations[ref.ID]; ok && strideE10ReferenceMatchesHeader(ref, attestation.Header) {
			return attestation.OrganizationID
		}
	}
	return ""
}

func (s *ContributionAuthorityService) strideE10TriggerOrganizationLocked(ref STRIDEReference) string {
	switch ref.ContractType {
	case STRIDEContractContributionClaim:
		value, ok := s.claims[ref.ID]
		if ok && strideE10ReferenceMatchesHeader(ref, value.Header) {
			return value.OrganizationID
		}
	case STRIDEContractFieldReleaseApproval:
		value, ok := s.approvals[ref.ID]
		if ok && strideE10ReferenceMatchesHeader(ref, value.Header) {
			return value.OrganizationID
		}
	case STRIDEContractContributionAttestation:
		value, ok := s.attestations[ref.ID]
		if ok && strideE10ReferenceMatchesHeader(ref, value.Header) {
			return value.OrganizationID
		}
	case STRIDEContractPublishedContributionClaim:
		value, ok := s.publications[ref.ID]
		if ok && strideE10ReferenceMatchesHeader(ref, value.Header) {
			return s.strideE10PublicationOrganizationLocked(value)
		}
	}
	return ""
}

func strideE10ReferenceMatchesHeader(ref STRIDEReference, header STRIDEContractHeader) bool {
	return ref.ID == header.ID && ref.Revision == header.Revision && ref.Digest == header.ContentDigest && ref.ContractType == header.ContractType
}

func strideE10SortContributionView(view *StrideE10ContributionView) {
	sort.Slice(view.Claims, func(i, j int) bool { return view.Claims[i].Header.ID < view.Claims[j].Header.ID })
	sort.Slice(view.Approvals, func(i, j int) bool { return view.Approvals[i].Header.ID < view.Approvals[j].Header.ID })
	sort.Slice(view.Attestations, func(i, j int) bool { return view.Attestations[i].Header.ID < view.Attestations[j].Header.ID })
	sort.Slice(view.Publications, func(i, j int) bool { return view.Publications[i].Header.ID < view.Publications[j].Header.ID })
	sort.Slice(view.Influences, func(i, j int) bool { return view.Influences[i].Header.ID < view.Influences[j].Header.ID })
	sort.Slice(view.Purges, func(i, j int) bool { return view.Purges[i].Header.ID < view.Purges[j].Header.ID })
}

type StrideE10NetworkPersonView struct {
	Profiles []NetworkProfileProjection
	Contacts []ContactRequest
	Blocks   []NetworkBlock
	Purges   []DerivedPurgeReceipt
}

type StrideE10NetworkOrganizationView struct {
	Grants         []TalentSearchGrant
	SearchReceipts []NetworkSearchReceipt
	Contacts       []ContactRequest
	Purges         []DerivedPurgeReceipt
}

type StrideE10RecruitingLimits struct {
	SearchesPerHour        int
	ResultsPerSearch       int
	DistinctResultsPerHour int
	ContactsPerDay         int
	RecordedSearches       int
	RecordedContacts       int
}

type StrideE10RecruitingSearchSummary struct {
	ID                string
	Revision          int64
	PolicyVerdict     string
	PolicyReasonCodes []string
	FilterCount       int
	ResultCount       int
	SearchedAt        string
}

type StrideE10RecruitingContactSummary struct {
	ID                string
	Revision          int64
	State             string
	CollaborationType string
	StateChangedAt    string
}

type StrideE10RecruitingAuditSummary struct {
	Kind       string
	ID         string
	Revision   int64
	State      string
	OccurredAt string
}

type StrideE10NetworkRecruitingAdminView struct {
	Grants         []TalentSearchGrant
	Limits         StrideE10RecruitingLimits
	SearchReceipts []StrideE10RecruitingSearchSummary
	Contacts       []StrideE10RecruitingContactSummary
	Audit          []StrideE10RecruitingAuditSummary
}

func (s *NetworkAuthority) ReadStrideE10NetworkPersonView(personID string) (StrideE10NetworkPersonView, error) {
	if s == nil || !strideIdentifier(personID) {
		return StrideE10NetworkPersonView{}, ErrNetworkAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	view := StrideE10NetworkPersonView{}
	for _, value := range s.profiles {
		if value.SubjectPersonID == personID {
			view.Profiles = append(view.Profiles, cloneNetworkProjection(value))
		}
	}
	for _, value := range s.contacts {
		if value.SenderPersonID == personID || value.RecipientPersonID == personID {
			view.Contacts = append(view.Contacts, cloneContactRequest(value))
		}
	}
	for _, value := range s.blocks {
		if value.BlockerPersonID == personID {
			view.Blocks = append(view.Blocks, cloneContract(value))
		}
	}
	for _, value := range s.purges {
		if value.SubjectPersonID == personID {
			view.Purges = append(view.Purges, cloneDerivedPurgeReceipt(value))
		}
	}
	sort.Slice(view.Profiles, func(i, j int) bool { return view.Profiles[i].Header.ID < view.Profiles[j].Header.ID })
	sort.Slice(view.Contacts, func(i, j int) bool { return view.Contacts[i].Header.ID < view.Contacts[j].Header.ID })
	sort.Slice(view.Blocks, func(i, j int) bool { return view.Blocks[i].Header.ID < view.Blocks[j].Header.ID })
	sort.Slice(view.Purges, func(i, j int) bool { return view.Purges[i].Header.ID < view.Purges[j].Header.ID })
	return view, nil
}

func (s *NetworkAuthority) ReadStrideE10NetworkOrganizationView(viewer StrideE10AuthorityViewer) (StrideE10NetworkOrganizationView, error) {
	if s == nil {
		return StrideE10NetworkOrganizationView{}, ErrNetworkAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	authority, ok := s.membershipAuthorities[viewer.MembershipID]
	if !ok || !authority.Active || authority.PersonID != viewer.PersonID || authority.OrganizationID != viewer.OrganizationID || authority.Revision != viewer.MembershipRevision {
		return StrideE10NetworkOrganizationView{}, ErrNetworkAuthorityDenied
	}
	view := StrideE10NetworkOrganizationView{}
	for _, value := range s.grants {
		if value.OrganizationID == viewer.OrganizationID && value.SearcherPersonID == viewer.PersonID && value.MembershipID == viewer.MembershipID {
			view.Grants = append(view.Grants, cloneTalentSearchGrant(value))
		}
	}
	for _, value := range s.searchReceipts {
		if value.OrganizationID != viewer.OrganizationID {
			continue
		}
		grant, ok := s.grants[value.Grant.ID]
		if ok && strideE10ReferenceMatchesHeader(value.Grant, grant.Header) && grant.SearcherPersonID == viewer.PersonID && grant.MembershipID == viewer.MembershipID {
			view.SearchReceipts = append(view.SearchReceipts, cloneNetworkSearchReceipt(value))
		}
	}
	for _, value := range s.contacts {
		if value.SenderOrganizationID == viewer.OrganizationID && value.SenderPersonID == viewer.PersonID {
			view.Contacts = append(view.Contacts, cloneContactRequest(value))
		}
	}
	for _, value := range s.purges {
		if s.strideE10NetworkTriggerOrganizationLocked(value.Trigger) == viewer.OrganizationID {
			view.Purges = append(view.Purges, cloneDerivedPurgeReceipt(value))
		}
	}
	sort.Slice(view.Grants, func(i, j int) bool { return view.Grants[i].Header.ID < view.Grants[j].Header.ID })
	sort.Slice(view.SearchReceipts, func(i, j int) bool { return view.SearchReceipts[i].Header.ID < view.SearchReceipts[j].Header.ID })
	sort.Slice(view.Contacts, func(i, j int) bool { return view.Contacts[i].Header.ID < view.Contacts[j].Header.ID })
	sort.Slice(view.Purges, func(i, j int) bool { return view.Purges[i].Header.ID < view.Purges[j].Header.ID })
	return view, nil
}

// ReadStrideE10NetworkRecruitingAdminView uses the global reader lock order:
// organization authority first, then network authority. It returns no contact
// note/channel, candidate identity/result, query text/digest, or private body.
func (s *NetworkAuthority) ReadStrideE10NetworkRecruitingAdminView(organizations *OrganizationAuthorityService, viewer StrideE10AuthorityViewer) (StrideE10NetworkRecruitingAdminView, error) {
	if s == nil || organizations == nil {
		return StrideE10NetworkRecruitingAdminView{}, ErrNetworkAuthorityInvalid
	}
	organizations.mu.RLock()
	defer organizations.mu.RUnlock()
	if _, err := organizations.strideE10ActiveViewerLocked(viewer, true); err != nil {
		return StrideE10NetworkRecruitingAdminView{}, ErrNetworkAuthorityDenied
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	view := StrideE10NetworkRecruitingAdminView{}
	grantIDs := map[string]TalentSearchGrant{}
	for _, value := range s.grants {
		if value.OrganizationID != viewer.OrganizationID {
			continue
		}
		clone := cloneTalentSearchGrant(value)
		view.Grants = append(view.Grants, clone)
		grantIDs[value.Header.ID] = value
		view.Audit = append(view.Audit, StrideE10RecruitingAuditSummary{Kind: "grant", ID: value.Header.ID, Revision: value.Header.Revision, State: value.State, OccurredAt: value.GrantedAt.UTC().Format(time.RFC3339Nano)})
	}
	for _, value := range s.searchReceipts {
		grant, ok := grantIDs[value.Grant.ID]
		if !ok || !strideE10ReferenceMatchesHeader(value.Grant, grant.Header) {
			continue
		}
		view.SearchReceipts = append(view.SearchReceipts, StrideE10RecruitingSearchSummary{ID: value.Header.ID, Revision: value.Header.Revision, PolicyVerdict: value.PolicyVerdict, PolicyReasonCodes: append([]string(nil), value.PolicyReasonCodes...), FilterCount: len(value.StructuredFilters), ResultCount: len(value.Results), SearchedAt: value.SearchedAt.UTC().Format(time.RFC3339Nano)})
		view.Audit = append(view.Audit, StrideE10RecruitingAuditSummary{Kind: "search", ID: value.Header.ID, Revision: value.Header.Revision, State: value.PolicyVerdict, OccurredAt: value.SearchedAt.UTC().Format(time.RFC3339Nano)})
	}
	for _, value := range s.contacts {
		if value.SenderOrganizationID != viewer.OrganizationID {
			continue
		}
		view.Contacts = append(view.Contacts, StrideE10RecruitingContactSummary{ID: value.Header.ID, Revision: value.Header.Revision, State: value.State, CollaborationType: value.CollaborationType, StateChangedAt: value.StateChangedAt.UTC().Format(time.RFC3339Nano)})
		view.Audit = append(view.Audit, StrideE10RecruitingAuditSummary{Kind: "contact", ID: value.Header.ID, Revision: value.Header.Revision, State: value.State, OccurredAt: value.StateChangedAt.UTC().Format(time.RFC3339Nano)})
	}
	view.Limits = StrideE10RecruitingLimits{SearchesPerHour: networkSearchesPerHour, ResultsPerSearch: networkResultsPerSearch, DistinctResultsPerHour: networkDistinctResultsPerHour, ContactsPerDay: networkContactsPerDay, RecordedSearches: len(view.SearchReceipts), RecordedContacts: len(view.Contacts)}
	sort.Slice(view.Grants, func(i, j int) bool { return view.Grants[i].Header.ID < view.Grants[j].Header.ID })
	sort.Slice(view.SearchReceipts, func(i, j int) bool { return view.SearchReceipts[i].ID < view.SearchReceipts[j].ID })
	sort.Slice(view.Contacts, func(i, j int) bool { return view.Contacts[i].ID < view.Contacts[j].ID })
	sort.Slice(view.Audit, func(i, j int) bool {
		if view.Audit[i].Kind == view.Audit[j].Kind {
			return view.Audit[i].ID < view.Audit[j].ID
		}
		return view.Audit[i].Kind < view.Audit[j].Kind
	})
	return view, nil
}

func (s *NetworkAuthority) strideE10NetworkTriggerOrganizationLocked(ref STRIDEReference) string {
	switch ref.ContractType {
	case STRIDEContractTalentSearchGrant:
		value, ok := s.grants[ref.ID]
		if ok && strideE10ReferenceMatchesHeader(ref, value.Header) {
			return value.OrganizationID
		}
	case STRIDEContractContactRequest:
		value, ok := s.contacts[ref.ID]
		if ok && strideE10ReferenceMatchesHeader(ref, value.Header) {
			return value.SenderOrganizationID
		}
	}
	return ""
}
