package main

// This file is a route-free, provider-free E10-W1 policy kernel. It owns only
// deterministic authority state and body-minimized receipts; callers must still
// supply a server-derived person/membership principal and separately gate any
// HTTP route, worker, provider, projection, or index.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrNetworkAuthorityInvalid     = errors.New("invalid network authority request")
	ErrNetworkAuthorityDenied      = errors.New("network authority denied")
	ErrNetworkAuthorityNotFound    = errors.New("network authority object not found")
	ErrNetworkAuthorityConflict    = errors.New("network authority revision conflict")
	ErrNetworkIdempotencyConflict  = errors.New("network authority idempotency conflict")
	ErrNetworkRateLimited          = errors.New("network search or contact rate limited")
	ErrNetworkBulkExtraction       = errors.New("network bulk extraction contained")
	ErrNetworkConfirmationRequired = errors.New("safe structured interpretation confirmation required")
)

const (
	networkSearchesPerHour        = 20
	networkResultsPerSearch       = 10
	networkDistinctResultsPerHour = 40
	networkContactsPerDay         = 5
	defaultNetworkPolicyRevision  = 1
)

type TalentSearchCapabilityAuthority struct {
	ID                 string
	Revision           int64
	OrganizationID     string
	ControllerPersonID string
	MembershipID       string
	MembershipRevision int64
	PolicyRevision     int64
	Active             bool
}

func (v TalentSearchCapabilityAuthority) validate() error {
	if !strideIdentifier(v.ID) || v.Revision < 1 || !strideIdentifier(v.OrganizationID) || !strideIdentifier(v.ControllerPersonID) ||
		!strideIdentifier(v.MembershipID) || v.MembershipRevision < 1 || v.PolicyRevision < 1 || !v.Active {
		return ErrNetworkAuthorityInvalid
	}
	return nil
}

type TalentSearchCapabilityAssertion struct {
	AuthorityID        string
	AuthorityRevision  int64
	ControllerPersonID string
}

// NetworkMembershipAuthority is installed from the canonical organization
// authority. Search grants never make organization membership authoritative.
type NetworkMembershipAuthority struct {
	MembershipID, OrganizationID, PersonID string
	Revision                               int64
	Active                                 bool
}

func (v NetworkMembershipAuthority) validate() error {
	if !strideIdentifier(v.MembershipID) || !strideIdentifier(v.OrganizationID) || !strideIdentifier(v.PersonID) || v.Revision < 1 {
		return ErrNetworkAuthorityInvalid
	}
	return nil
}

type NetworkContactExpiryAuthority struct {
	Controller STRIDEControllerRevision
	Active     bool
}

type NetworkSearchRequest struct {
	GrantRef                STRIDEReference
	SearcherPersonID        string
	OrganizationID          string
	MembershipID            string
	MembershipRevision      int64
	HumanQuery              string
	OriginalQueryDigest     string
	StructuredFilters       []NetworkSearchFilter
	InterpretationConfirmed bool
	Limit                   int
	IdempotencyKeyDigest    string
	At                      time.Time
}

type NetworkContactAdmission struct {
	GrantRef             STRIDEReference
	SenderPersonID       string
	SenderOrganizationID string
	MembershipID         string
	MembershipRevision   int64
	RecipientProjection  STRIDEReference
	Purpose              string
	NoteDigest           string
	CollaborationType    string
	ExpiresAt            time.Time
	IdempotencyKeyDigest string
	At                   time.Time
}

type networkIdempotencyRecord struct {
	Digest    string
	Kind      string
	ID        string
	Revision  int64
	RelatedID string
}

type networkTimedSearch struct {
	At         time.Time
	Candidates []string
}

type NetworkAuthority struct {
	mu                    sync.Mutex
	now                   func() time.Time
	profiles              map[string]NetworkProfileProjection
	grants                map[string]TalentSearchGrant
	capabilityAuthorities map[string]TalentSearchCapabilityAuthority
	membershipAuthorities map[string]NetworkMembershipAuthority
	publications          map[string]PublishedContributionClaim
	claims                map[string]ContributionClaim
	approvals             map[string]FieldReleaseApproval
	attestations          map[string]ContributionAttestation
	expiryAuthorities     map[string]NetworkContactExpiryAuthority
	searchReceipts        map[string]NetworkSearchReceipt
	blocks                map[string]NetworkBlock
	contacts              map[string]ContactRequest
	purges                map[string]DerivedPurgeReceipt
	idempotency           map[string]networkIdempotencyRecord
	searchWindows         map[string][]networkTimedSearch
	contactWindows        map[string][]time.Time
	purgeGenerations      map[string]int64
	profileVersions       map[string]NetworkProfileProjection
	grantVersions         map[string]TalentSearchGrant
	blockVersions         map[string]NetworkBlock
	contactVersions       map[string]ContactRequest
}

func NewNetworkAuthority(now func() time.Time) *NetworkAuthority {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &NetworkAuthority{
		now: now, profiles: map[string]NetworkProfileProjection{}, grants: map[string]TalentSearchGrant{},
		capabilityAuthorities: map[string]TalentSearchCapabilityAuthority{}, searchReceipts: map[string]NetworkSearchReceipt{},
		membershipAuthorities: map[string]NetworkMembershipAuthority{}, publications: map[string]PublishedContributionClaim{}, claims: map[string]ContributionClaim{}, approvals: map[string]FieldReleaseApproval{}, attestations: map[string]ContributionAttestation{}, expiryAuthorities: map[string]NetworkContactExpiryAuthority{},
		blocks: map[string]NetworkBlock{}, contacts: map[string]ContactRequest{}, purges: map[string]DerivedPurgeReceipt{},
		idempotency: map[string]networkIdempotencyRecord{}, searchWindows: map[string][]networkTimedSearch{},
		contactWindows: map[string][]time.Time{}, purgeGenerations: map[string]int64{}, profileVersions: map[string]NetworkProfileProjection{}, grantVersions: map[string]TalentSearchGrant{}, blockVersions: map[string]NetworkBlock{}, contactVersions: map[string]ContactRequest{},
	}
}

func (s *NetworkAuthority) InstallMembershipAuthority(authority NetworkMembershipAuthority) error {
	if s == nil || authority.validate() != nil {
		return ErrNetworkAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prior, ok := s.membershipAuthorities[authority.MembershipID]; ok && authority.Revision <= prior.Revision {
		return ErrNetworkAuthorityConflict
	}
	s.membershipAuthorities[authority.MembershipID] = authority
	return nil
}

// InstallPublicationAuthority installs the exact current public claim and all
// attestations it references. Projection publication resolves these records;
// syntactically valid references alone confer no evidence authority.
func (s *NetworkAuthority) InstallPublicationAuthority(claim PublishedContributionClaim, attestations []ContributionAttestation) error {
	if s == nil || claim.Validate() != nil {
		return ErrNetworkAuthorityInvalid
	}
	resolved := make(map[string]ContributionAttestation, len(attestations))
	for _, attestation := range attestations {
		if attestation.Validate() != nil || attestation.SubjectPersonID != claim.SubjectPersonID || claim.State == "published" && attestation.State != "active" {
			return ErrNetworkAuthorityInvalid
		}
		resolved[attestation.Header.ID] = attestation
	}
	for _, ref := range claim.Attestations {
		attestation, ok := resolved[ref.ID]
		if claim.State == "published" && (!ok || ref.Revision != attestation.Header.Revision || ref.Digest != attestation.Header.ContentDigest) {
			return ErrNetworkAuthorityInvalid
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prior, ok := s.publications[claim.Header.ID]; ok && claim.Header.Revision <= prior.Header.Revision {
		if claim.Header.Revision == prior.Header.Revision && claim.Header.ContentDigest == prior.Header.ContentDigest {
			return nil
		}
		return ErrNetworkAuthorityConflict
	}
	s.publications[claim.Header.ID] = claim
	for id, attestation := range resolved {
		s.attestations[id] = attestation
	}
	s.fenceInvalidPublicationProfilesLocked(claim.Header.ID, claim.StateChangedAt)
	return nil
}

// InstallClaimAuthority installs the exact current governed claim revision and
// immediately fences every public projection whose evidence still resolves to
// an older, non-verified, revoked, superseded, or revalidation-required claim.
func (s *NetworkAuthority) InstallClaimAuthority(claim ContributionClaim) error {
	if s == nil || claim.Validate() != nil {
		return ErrNetworkAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prior, ok := s.claims[claim.Header.ID]; ok && claim.Header.Revision <= prior.Header.Revision {
		if claim.Header.Revision == prior.Header.Revision && claim.Header.ContentDigest == prior.Header.ContentDigest {
			return nil
		}
		return ErrNetworkAuthorityConflict
	}
	s.claims[claim.Header.ID] = cloneContract(claim)
	s.fencePublicationsForClaimLocked(claim.Header.ID, claim.StateChangedAt)
	return nil
}

// InstallFieldApprovalAuthority installs the exact current approval revision.
// Approval withdrawal/expiry/supersession is authority loss, not a UI hint.
func (s *NetworkAuthority) InstallFieldApprovalAuthority(approval FieldReleaseApproval) error {
	if s == nil || approval.Validate() != nil {
		return ErrNetworkAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prior, ok := s.approvals[approval.Header.ID]; ok && approval.Header.Revision <= prior.Header.Revision {
		if approval.Header.Revision == prior.Header.Revision && approval.Header.ContentDigest == prior.Header.ContentDigest {
			return nil
		}
		return ErrNetworkAuthorityConflict
	}
	s.approvals[approval.Header.ID] = cloneContract(approval)
	for _, attestation := range s.attestations {
		for _, field := range attestation.ReleasedFields {
			for _, ref := range field.ApprovalRefs {
				if ref.ID == approval.Header.ID {
					s.fencePublicationsForAttestationLocked(attestation.Header.ID, approval.StateChangedAt)
				}
			}
		}
	}
	return nil
}

// InstallAttestationAuthority installs revocation/supersession independently
// from a publication rewrite and immediately fences every dependent profile.
func (s *NetworkAuthority) InstallAttestationAuthority(attestation ContributionAttestation) error {
	if s == nil || attestation.Validate() != nil {
		return ErrNetworkAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prior, ok := s.attestations[attestation.Header.ID]; ok && attestation.Header.Revision <= prior.Header.Revision {
		if attestation.Header.Revision == prior.Header.Revision && attestation.Header.ContentDigest == prior.Header.ContentDigest {
			return nil
		}
		return ErrNetworkAuthorityConflict
	}
	s.attestations[attestation.Header.ID] = attestation
	for publicationID, publication := range s.publications {
		for _, ref := range publication.Attestations {
			if ref.ID == attestation.Header.ID {
				s.fenceInvalidPublicationProfilesLocked(publicationID, attestation.Header.CreatedAt)
				break
			}
		}
	}
	return nil
}

func (s *NetworkAuthority) fencePublicationsForClaimLocked(claimID string, at time.Time) {
	for _, attestation := range s.attestations {
		if attestation.Claim.ID == claimID {
			s.fencePublicationsForAttestationLocked(attestation.Header.ID, at)
		}
	}
}

func (s *NetworkAuthority) fencePublicationsForAttestationLocked(attestationID string, at time.Time) {
	for publicationID, publication := range s.publications {
		for _, ref := range publication.Attestations {
			if ref.ID == attestationID {
				s.fenceInvalidPublicationProfilesLocked(publicationID, at)
				break
			}
		}
	}
}

func (s *NetworkAuthority) InstallContactExpiryAuthority(authority NetworkContactExpiryAuthority) error {
	if s == nil || authority.Controller.Validate() != nil {
		return ErrNetworkAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prior, ok := s.expiryAuthorities[authority.Controller.AuthorityID]
	if ok && authority.Controller.AuthorityRevision <= prior.Controller.AuthorityRevision {
		return ErrNetworkAuthorityConflict
	}
	s.expiryAuthorities[authority.Controller.AuthorityID] = authority
	return nil
}

func (s *NetworkAuthority) InstallTalentSearchCapabilityAuthority(authority TalentSearchCapabilityAuthority) error {
	if s == nil || authority.validate() != nil {
		return ErrNetworkAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prior, ok := s.capabilityAuthorities[authority.ID]; ok && authority.Revision <= prior.Revision {
		return ErrNetworkAuthorityConflict
	}
	s.capabilityAuthorities[authority.ID] = authority
	return nil
}

func (s *NetworkAuthority) PutProfile(actor STRIDEControllerRevision, next NetworkProfileProjection, expectedRevision int64, idempotencyKeyDigest string) (NetworkProfileProjection, *DerivedPurgeReceipt, bool, error) {
	if s == nil || actor.Validate() != nil || next.Validate() != nil || actor != next.Controller || actor.PrincipalID != next.SubjectPersonID || !isHexDigest(idempotencyKeyDigest) {
		return NetworkProfileProjection{}, nil, false, ErrNetworkAuthorityInvalid
	}
	digest, _ := STRIDEContractDigest(next)
	s.mu.Lock()
	defer s.mu.Unlock()
	if replay, purge, ok, err := s.profileReplayLocked(actor.PrincipalID, idempotencyKeyDigest, digest); ok || err != nil {
		return replay, purge, true, err
	}
	prior, exists := s.profiles[next.Header.ID]
	if (!exists || oneOf(next.State, "draft", "published")) && s.validateProfileEvidenceLocked(next) != nil {
		return NetworkProfileProjection{}, nil, false, ErrNetworkAuthorityDenied
	}
	if !exists {
		if expectedRevision != 0 || next.Header.Revision != 1 || next.State != "draft" || next.Discoverability != "unlisted" {
			return NetworkProfileProjection{}, nil, false, ErrNetworkAuthorityConflict
		}
	} else {
		if prior.SubjectPersonID != next.SubjectPersonID || expectedRevision != prior.Header.Revision || next.Header.Revision != prior.Header.Revision+1 || !networkProfileTransitionAllowed(prior.State, next.State) {
			return NetworkProfileProjection{}, nil, false, ErrNetworkAuthorityConflict
		}
		if next.PurgeGeneration < prior.PurgeGeneration {
			return NetworkProfileProjection{}, nil, false, ErrNetworkAuthorityConflict
		}
	}
	var purge *DerivedPurgeReceipt
	if exists && oneOf(next.State, "paused", "off", "deleted") && next.State != prior.State {
		if next.PurgeGeneration != prior.PurgeGeneration+1 {
			return NetworkProfileProjection{}, nil, false, ErrNetworkAuthorityConflict
		}
		receipt := s.emitPurgeLocked(next.SubjectPersonID, referenceFromHeader(next.Header), next.PurgeGeneration, next.FieldsDigest, next.State, next.StateChangedAt)
		purge = &receipt
		if s.purgeGenerations[next.SubjectPersonID] < next.PurgeGeneration {
			s.purgeGenerations[next.SubjectPersonID] = next.PurgeGeneration
		}
		s.fenceContactsLocked(next.SubjectPersonID, "", next.StateChangedAt)
	}
	s.profiles[next.Header.ID] = cloneNetworkProjection(next)
	s.profileVersions[networkVersionKey(next.Header.ID, next.Header.Revision)] = cloneNetworkProjection(next)
	relatedID := ""
	if purge != nil {
		relatedID = purge.Header.ID
	}
	s.idempotency[networkIdempotencyKey("profile", actor.PrincipalID, idempotencyKeyDigest)] = networkIdempotencyRecord{Digest: digest, Kind: "profile", ID: next.Header.ID, Revision: next.Header.Revision, RelatedID: relatedID}
	result := cloneNetworkProjection(next)
	return result, purge, false, nil
}

func networkProfileTransitionAllowed(from, to string) bool {
	switch from {
	case "draft":
		return oneOf(to, "draft", "published", "off", "deleted")
	case "published":
		return oneOf(to, "published", "paused", "off", "deleted")
	case "paused":
		return oneOf(to, "paused", "published", "off", "deleted")
	case "off":
		return oneOf(to, "off", "draft", "deleted")
	}
	return false
}

func (s *NetworkAuthority) validateProfileEvidenceLocked(profile NetworkProfileProjection) error {
	publication, ok := s.publications[profile.Publication.ID]
	if !ok || publication.Header.Revision != profile.Publication.Revision || publication.Header.ContentDigest != profile.Publication.Digest ||
		publication.SubjectPersonID != profile.SubjectPersonID || publication.State != "published" || publication.Visibility != "signed_in_network" {
		return ErrNetworkAuthorityDenied
	}
	for _, field := range networkVisiblePublishedFields(profile.Fields) {
		if field.EvidenceLabel == "self_described" {
			continue
		}
		if field.Claim == nil || *field.Claim != profile.Publication {
			return ErrNetworkAuthorityDenied
		}
		verified := false
		for _, ref := range publication.Attestations {
			attestation, exists := s.attestations[ref.ID]
			if !exists || attestation.Header.Revision != ref.Revision || attestation.Header.ContentDigest != ref.Digest || attestation.State != "active" || attestation.VerificationTier != field.EvidenceLabel {
				continue
			}
			claim, claimExists := s.claims[attestation.Claim.ID]
			if !claimExists || claim.Header.Revision != attestation.Claim.Revision || claim.Header.ContentDigest != attestation.Claim.Digest || claim.State != "verified" || claim.SubjectPersonID != profile.SubjectPersonID {
				continue
			}
			for _, released := range attestation.ReleasedFields {
				if networkReleasedFieldMatches(field.FieldKey, released.FieldKey) && released.ValueDigest == field.ValueDigest && s.releasedFieldApprovalsCurrentLocked(attestation, released) {
					verified = true
					break
				}
			}
			if verified {
				break
			}
		}
		if !verified {
			return ErrNetworkAuthorityDenied
		}
	}
	return nil
}

func (s *NetworkAuthority) releasedFieldApprovalsCurrentLocked(attestation ContributionAttestation, released ReleasedContributionField) bool {
	if len(released.ApprovalRefs) == 0 {
		return false
	}
	for _, ref := range released.ApprovalRefs {
		approval, ok := s.approvals[ref.ID]
		if !ok || approval.Header.Revision != ref.Revision || approval.Header.ContentDigest != ref.Digest || approval.State != "approved" || approval.Attestation.ID != attestation.Header.ID || approval.Attestation.Revision != attestation.Header.Revision || approval.FieldKey != released.FieldKey || approval.FieldValueDigest != released.ValueDigest {
			return false
		}
	}
	return true
}

func (s *NetworkAuthority) fenceInvalidPublicationProfilesLocked(publicationID string, at time.Time) {
	for id, prior := range s.profiles {
		if prior.Publication.ID != publicationID || prior.State != "published" || s.validateProfileEvidenceLocked(prior) == nil {
			continue
		}
		next := cloneNetworkProjection(prior)
		next.Header.Revision++
		next.Header.CreatedAt = at.UTC()
		next.Header.ContentDigest = sha256Hex([]byte(prior.Header.ContentDigest + "\x00evidence_fenced"))
		next.State = "paused"
		next.Discoverability = "unlisted"
		next.StateChangedAt = at.UTC()
		next.PurgeGeneration++
		receipt := s.emitPurgeLocked(next.SubjectPersonID, referenceFromHeader(next.Header), next.PurgeGeneration, next.FieldsDigest, "evidence_authority_changed", at)
		if s.purgeGenerations[next.SubjectPersonID] < next.PurgeGeneration {
			s.purgeGenerations[next.SubjectPersonID] = next.PurgeGeneration
		}
		s.fenceContactsLocked(next.SubjectPersonID, "", at)
		s.profiles[id] = next
		s.profileVersions[networkVersionKey(id, next.Header.Revision)] = cloneNetworkProjection(next)
		_ = receipt
	}
}

func networkReleasedFieldMatches(networkField, releasedField string) bool {
	if networkField == "problem_class" {
		return releasedField == "category"
	}
	if networkField == "outcome_class" {
		return releasedField == "outcome"
	}
	return networkField == releasedField
}

func (s *NetworkAuthority) PutTalentSearchGrant(assertion TalentSearchCapabilityAssertion, next TalentSearchGrant, expectedRevision int64, idempotencyKeyDigest string) (TalentSearchGrant, *DerivedPurgeReceipt, bool, error) {
	if s == nil || next.Validate() != nil || !isHexDigest(idempotencyKeyDigest) {
		return TalentSearchGrant{}, nil, false, ErrNetworkAuthorityInvalid
	}
	digest, _ := STRIDEContractDigest(next)
	s.mu.Lock()
	defer s.mu.Unlock()
	authority, ok := s.capabilityAuthorities[assertion.AuthorityID]
	if !ok || !authority.Active || authority.Revision != assertion.AuthorityRevision || authority.ControllerPersonID != assertion.ControllerPersonID ||
		authority.OrganizationID != next.OrganizationID || authority.ID != next.CapabilityAdministrator.AuthorityID || authority.Revision != next.CapabilityAdministrator.AuthorityRevision ||
		authority.ControllerPersonID != next.CapabilityAdministrator.PrincipalID || authority.PolicyRevision != next.CapabilityAdministrator.PolicyRevision {
		return TalentSearchGrant{}, nil, false, ErrNetworkAuthorityDenied
	}
	key := networkIdempotencyKey("grant", assertion.ControllerPersonID, idempotencyKeyDigest)
	if record, exists := s.idempotency[key]; exists {
		if record.Digest != digest {
			return TalentSearchGrant{}, nil, true, ErrNetworkIdempotencyConflict
		}
		return cloneTalentSearchGrant(s.grantVersions[networkVersionKey(record.ID, record.Revision)]), s.replayedPurgeLocked(record.RelatedID), true, nil
	}
	prior, exists := s.grants[next.Header.ID]
	if !exists {
		if expectedRevision != 0 || next.Header.Revision != 1 || next.State != "active" {
			return TalentSearchGrant{}, nil, false, ErrNetworkAuthorityConflict
		}
	} else if expectedRevision != prior.Header.Revision || next.Header.Revision != prior.Header.Revision+1 || prior.OrganizationID != next.OrganizationID || prior.SearcherPersonID != next.SearcherPersonID || !talentGrantTransitionAllowed(prior.State, next.State) {
		return TalentSearchGrant{}, nil, false, ErrNetworkAuthorityConflict
	}
	var purge *DerivedPurgeReceipt
	if exists && prior.State == "active" && next.State != "active" {
		receipt := s.emitPurgeLocked(next.SearcherPersonID, referenceFromHeader(next.Header), s.nextPurgeGenerationLocked(next.SearcherPersonID), strideTestlessDigest(next.Header.ID), "grant_"+next.State, next.Header.CreatedAt)
		purge = &receipt
		s.fenceContactsLocked(next.SearcherPersonID, next.OrganizationID, next.Header.CreatedAt)
	}
	s.grants[next.Header.ID] = cloneTalentSearchGrant(next)
	s.grantVersions[networkVersionKey(next.Header.ID, next.Header.Revision)] = cloneTalentSearchGrant(next)
	relatedID := ""
	if purge != nil {
		relatedID = purge.Header.ID
	}
	s.idempotency[key] = networkIdempotencyRecord{Digest: digest, Kind: "grant", ID: next.Header.ID, Revision: next.Header.Revision, RelatedID: relatedID}
	return cloneTalentSearchGrant(next), purge, false, nil
}

func talentGrantTransitionAllowed(from, to string) bool {
	return from == "active" && oneOf(to, "revoked", "expired")
}

func (s *NetworkAuthority) Search(request NetworkSearchRequest) (NetworkSearchReceipt, bool, error) {
	if s == nil || request.GrantRef.Validate() != nil || request.GrantRef.ContractType != STRIDEContractTalentSearchGrant ||
		!strideIdentifier(request.SearcherPersonID) || !strideIdentifier(request.OrganizationID) || !strideIdentifier(request.MembershipID) || request.MembershipRevision < 1 ||
		!isHexDigest(request.OriginalQueryDigest) || sha256Hex([]byte(request.HumanQuery)) != request.OriginalQueryDigest || !isHexDigest(request.IdempotencyKeyDigest) || request.At.IsZero() ||
		request.Limit < 1 || request.Limit > networkResultsPerSearch {
		return NetworkSearchReceipt{}, false, ErrNetworkAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	grant, err := s.currentGrantLocked(request.GrantRef, request.SearcherPersonID, request.OrganizationID, request.MembershipID, request.MembershipRevision, request.At)
	if err != nil {
		return NetworkSearchReceipt{}, false, err
	}
	requestDigest, _ := STRIDEContractDigest(request)
	key := networkIdempotencyKey("search", request.SearcherPersonID, request.IdempotencyKeyDigest)
	if record, exists := s.idempotency[key]; exists {
		if record.Digest != requestDigest {
			return NetworkSearchReceipt{}, true, ErrNetworkIdempotencyConflict
		}
		return cloneNetworkSearchReceipt(s.searchReceipts[record.ID]), true, nil
	}
	if !validSearchFilters(request.StructuredFilters) {
		receipt := s.newSearchReceiptLocked(request, grant, "reject", []string{"invalid_or_prohibited_filter"}, nil)
		s.recordSearchReceiptLocked(key, requestDigest, receipt)
		return receipt, false, ErrNetworkAuthorityDenied
	}
	verdict, reasons := deterministicNetworkPolicy(request.HumanQuery, request.StructuredFilters, request.InterpretationConfirmed)
	if verdict != "allow" {
		receipt := s.newSearchReceiptLocked(request, grant, verdict, reasons, nil)
		s.recordSearchReceiptLocked(key, requestDigest, receipt)
		if verdict == "abstain" && containsSTRIDEString(reasons, "safe_interpretation_confirmation_required") {
			return receipt, false, ErrNetworkConfirmationRequired
		}
		return receipt, false, ErrNetworkAuthorityDenied
	}
	searchKeys := []string{"person:" + request.SearcherPersonID, "organization:" + request.OrganizationID, "global"}
	windows := make(map[string][]networkTimedSearch, len(searchKeys))
	for _, searchKey := range searchKeys {
		windows[searchKey] = pruneSearchWindow(s.searchWindows[searchKey], request.At.Add(-time.Hour))
	}
	if len(windows[searchKeys[0]]) >= networkSearchesPerHour || len(windows[searchKeys[1]]) >= networkSearchesPerHour*10 || len(windows[searchKeys[2]]) >= networkSearchesPerHour*100 {
		receipt := s.newSearchReceiptLocked(request, grant, "abstain", []string{"search_rate_limit"}, nil)
		s.recordSearchReceiptLocked(key, requestDigest, receipt)
		return receipt, false, ErrNetworkRateLimited
	}
	results := s.matchPublishedProfilesLocked(request)
	if len(results) > request.Limit {
		results = results[:request.Limit]
	}
	bulkContained := false
	for index, searchKey := range searchKeys {
		seen := map[string]bool{}
		for _, prior := range windows[searchKey] {
			for _, id := range prior.Candidates {
				seen[id] = true
			}
		}
		for _, result := range results {
			seen[result.Projection.ID] = true
		}
		multipliers := []int{1, 10, 100}
		if len(seen) > networkDistinctResultsPerHour*multipliers[index] {
			bulkContained = true
		}
	}
	if bulkContained {
		receipt := s.newSearchReceiptLocked(request, grant, "abstain", []string{"bulk_extraction_contained"}, nil)
		s.recordSearchReceiptLocked(key, requestDigest, receipt)
		return receipt, false, ErrNetworkBulkExtraction
	}
	receipt := s.newSearchReceiptLocked(request, grant, "allow", []string{"structured_policy_allowed"}, results)
	s.recordSearchReceiptLocked(key, requestDigest, receipt)
	ids := make([]string, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.Projection.ID)
	}
	for _, searchKey := range searchKeys {
		s.searchWindows[searchKey] = append(windows[searchKey], networkTimedSearch{At: request.At, Candidates: ids})
	}
	return cloneNetworkSearchReceipt(receipt), false, nil
}

func deterministicNetworkPolicy(query string, filters []NetworkSearchFilter, confirmed bool) (string, []string) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" || len(filters) == 0 {
		return "abstain", []string{"structured_filter_required"}
	}
	if !confirmed {
		return "abstain", []string{"safe_interpretation_confirmation_required"}
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "personality") {
		for _, filter := range filters {
			if filter.Field != "work_mode" {
				return "reject", []string{"prohibited_criterion"}
			}
		}
		lower = strings.ReplaceAll(lower, "personality", "work mode")
	}
	if containsProhibitedSearchTerm(lower) {
		return "reject", []string{"prohibited_criterion"}
	}
	return "allow", []string{"structured_policy_allowed"}
}

func (s *NetworkAuthority) PutBlock(actor STRIDEControllerRevision, next NetworkBlock, expectedRevision int64, idempotencyKeyDigest string) (NetworkBlock, *DerivedPurgeReceipt, bool, error) {
	if s == nil || actor.Validate() != nil || next.Validate() != nil || actor != next.Controller || actor.PrincipalID != next.BlockerPersonID || !isHexDigest(idempotencyKeyDigest) {
		return NetworkBlock{}, nil, false, ErrNetworkAuthorityInvalid
	}
	digest, _ := STRIDEContractDigest(next)
	s.mu.Lock()
	defer s.mu.Unlock()
	key := networkIdempotencyKey("block", actor.PrincipalID, idempotencyKeyDigest)
	if record, exists := s.idempotency[key]; exists {
		if record.Digest != digest {
			return NetworkBlock{}, nil, true, ErrNetworkIdempotencyConflict
		}
		return s.blockVersions[networkVersionKey(record.ID, record.Revision)], s.replayedPurgeLocked(record.RelatedID), true, nil
	}
	prior, exists := s.blocks[next.Header.ID]
	if !exists {
		if expectedRevision != 0 || next.Header.Revision != 1 || next.State != "active" {
			return NetworkBlock{}, nil, false, ErrNetworkAuthorityConflict
		}
	} else if expectedRevision != prior.Header.Revision || next.Header.Revision != prior.Header.Revision+1 || prior.BlockerPersonID != next.BlockerPersonID || prior.BlockedPersonID != next.BlockedPersonID || prior.BlockedOrganizationID != next.BlockedOrganizationID || prior.State != "active" || next.State != "withdrawn" {
		return NetworkBlock{}, nil, false, ErrNetworkAuthorityConflict
	}
	var purge *DerivedPurgeReceipt
	if next.State == "active" {
		receipt := s.emitPurgeLocked(next.BlockerPersonID, referenceFromHeader(next.Header), s.nextPurgeGenerationLocked(next.BlockerPersonID), strideTestlessDigest(next.Header.ID), "block", next.StateChangedAt)
		purge = &receipt
		s.fenceBlockedContactsLocked(next, next.StateChangedAt)
	}
	s.blocks[next.Header.ID] = next
	s.blockVersions[networkVersionKey(next.Header.ID, next.Header.Revision)] = next
	relatedID := ""
	if purge != nil {
		relatedID = purge.Header.ID
	}
	s.idempotency[key] = networkIdempotencyRecord{Digest: digest, Kind: "block", ID: next.Header.ID, Revision: next.Header.Revision, RelatedID: relatedID}
	return next, purge, false, nil
}

func (s *NetworkAuthority) CreateContact(admission NetworkContactAdmission) (ContactRequest, bool, error) {
	if s == nil || admission.GrantRef.Validate() != nil || admission.GrantRef.ContractType != STRIDEContractTalentSearchGrant ||
		!strideIdentifier(admission.SenderPersonID) || !strideIdentifier(admission.SenderOrganizationID) || !strideIdentifier(admission.MembershipID) || admission.MembershipRevision < 1 ||
		admission.RecipientProjection.Validate() != nil || admission.RecipientProjection.ContractType != STRIDEContractNetworkProfileProjection ||
		!strideIdentifier(admission.Purpose) || !isHexDigest(admission.NoteDigest) || !oneOf(admission.CollaborationType, "collaboration", "advisory", "employment", "recruiting", "organization_join") ||
		!admission.ExpiresAt.After(admission.At) || !isHexDigest(admission.IdempotencyKeyDigest) || admission.At.IsZero() {
		return ContactRequest{}, false, ErrNetworkAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.currentGrantLocked(admission.GrantRef, admission.SenderPersonID, admission.SenderOrganizationID, admission.MembershipID, admission.MembershipRevision, admission.At)
	if err != nil {
		return ContactRequest{}, false, err
	}
	projection, ok := s.profiles[admission.RecipientProjection.ID]
	if !ok || projection.Header.Revision != admission.RecipientProjection.Revision || projection.Header.ContentDigest != admission.RecipientProjection.Digest || projection.State != "published" || projection.Discoverability != "signed_in_network" {
		return ContactRequest{}, false, ErrNetworkAuthorityNotFound
	}
	if s.blockedLocked(admission.SenderPersonID, admission.SenderOrganizationID, projection.SubjectPersonID) {
		return ContactRequest{}, false, ErrNetworkAuthorityDenied
	}
	digest, _ := STRIDEContractDigest(admission)
	key := networkIdempotencyKey("contact_create", admission.SenderPersonID, admission.IdempotencyKeyDigest)
	if record, exists := s.idempotency[key]; exists {
		if record.Digest != digest {
			return ContactRequest{}, true, ErrNetworkIdempotencyConflict
		}
		return cloneContactRequest(s.contactVersions[networkVersionKey(record.ID, record.Revision)]), true, nil
	}
	contactKeys := []string{"person:" + admission.SenderPersonID, "organization:" + admission.SenderOrganizationID, "global"}
	windows := make(map[string][]time.Time, len(contactKeys))
	for _, contactKey := range contactKeys {
		windows[contactKey] = pruneTimes(s.contactWindows[contactKey], admission.At.Add(-24*time.Hour))
	}
	if len(windows[contactKeys[0]]) >= networkContactsPerDay || len(windows[contactKeys[1]]) >= networkContactsPerDay*10 || len(windows[contactKeys[2]]) >= networkContactsPerDay*100 {
		return ContactRequest{}, false, ErrNetworkRateLimited
	}
	id := "contact_" + admission.IdempotencyKeyDigest[:24]
	contact := ContactRequest{
		Header:               STRIDEContractHeader{TenantID: admission.SenderOrganizationID, ID: id, Revision: 1, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractContactRequest, ContentDigest: digest, CreatedAt: admission.At.UTC()},
		SenderOrganizationID: admission.SenderOrganizationID, SenderPersonID: admission.SenderPersonID, RecipientPersonID: projection.SubjectPersonID,
		RecipientProjection: admission.RecipientProjection, Purpose: admission.Purpose, NoteDigest: admission.NoteDigest, CollaborationType: admission.CollaborationType,
		State: "pending", ExpiresAt: admission.ExpiresAt.UTC(), StateChangedAt: admission.At.UTC(),
	}
	if contact.Validate() != nil {
		return ContactRequest{}, false, ErrNetworkAuthorityInvalid
	}
	s.contacts[id] = contact
	s.contactVersions[networkVersionKey(id, 1)] = cloneContactRequest(contact)
	s.idempotency[key] = networkIdempotencyRecord{Digest: digest, Kind: "contact", ID: id, Revision: 1}
	for _, contactKey := range contactKeys {
		s.contactWindows[contactKey] = append(windows[contactKey], admission.At.UTC())
	}
	return cloneContactRequest(contact), false, nil
}

func (s *NetworkAuthority) DecideContact(actor STRIDEControllerRevision, contactID string, expectedRevision int64, decision, acceptedChannelDigest, idempotencyKeyDigest string, at time.Time) (ContactRequest, bool, error) {
	if s == nil || actor.Validate() != nil || !strideIdentifier(contactID) || !oneOf(decision, "accepted", "declined", "withdrawn", "expired") ||
		!isHexDigest(idempotencyKeyDigest) || at.IsZero() || (decision == "accepted") != isHexDigest(acceptedChannelDigest) {
		return ContactRequest{}, false, ErrNetworkAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prior, ok := s.contacts[contactID]
	if !ok {
		return ContactRequest{}, false, ErrNetworkAuthorityNotFound
	}
	requestDigest, _ := STRIDEContractDigest(struct {
		ID                string
		Revision          int64
		Decision, Channel string
	}{contactID, expectedRevision, decision, acceptedChannelDigest})
	key := networkIdempotencyKey("contact_decide", actor.PrincipalID, idempotencyKeyDigest)
	if record, exists := s.idempotency[key]; exists {
		if record.Digest != requestDigest {
			return ContactRequest{}, true, ErrNetworkIdempotencyConflict
		}
		return cloneContactRequest(s.contactVersions[networkVersionKey(record.ID, record.Revision)]), true, nil
	}
	if prior.State != "pending" || prior.Header.Revision != expectedRevision {
		return ContactRequest{}, false, ErrNetworkAuthorityConflict
	}
	expiryAuthority, expiryInstalled := s.expiryAuthorities[actor.AuthorityID]
	if decision == "withdrawn" && actor.PrincipalID != prior.SenderPersonID || oneOf(decision, "accepted", "declined") && actor.PrincipalID != prior.RecipientPersonID || decision == "expired" && (!expiryInstalled || !expiryAuthority.Active || expiryAuthority.Controller != actor || at.Before(prior.ExpiresAt)) {
		return ContactRequest{}, false, ErrNetworkAuthorityDenied
	}
	next := cloneContactRequest(prior)
	next.Header.Revision++
	if decision != "expired" {
		next.Header.CreatedAt = at.UTC()
	}
	next.Header.ContentDigest = requestDigest
	next.State = decision
	next.StateChangedAt = at.UTC()
	if decision == "accepted" {
		next.AcceptedChannelDigest = acceptedChannelDigest
		controller := actor
		next.RecipientController = &controller
	}
	if next.Validate() != nil {
		return ContactRequest{}, false, ErrNetworkAuthorityInvalid
	}
	s.contacts[contactID] = next
	s.contactVersions[networkVersionKey(contactID, next.Header.Revision)] = cloneContactRequest(next)
	s.idempotency[key] = networkIdempotencyRecord{Digest: requestDigest, Kind: "contact", ID: contactID, Revision: next.Header.Revision}
	return cloneContactRequest(next), false, nil
}

func (s *NetworkAuthority) currentGrantLocked(ref STRIDEReference, personID, organizationID, membershipID string, membershipRevision int64, at time.Time) (TalentSearchGrant, error) {
	grant, ok := s.grants[ref.ID]
	membership, membershipOK := s.membershipAuthorities[membershipID]
	if !ok || grant.Header.Revision != ref.Revision || grant.Header.ContentDigest != ref.Digest || grant.State != "active" || !at.Before(grant.ExpiresAt) ||
		grant.SearcherPersonID != personID || grant.OrganizationID != organizationID || grant.MembershipID != membershipID || grant.MembershipRevision != membershipRevision ||
		!membershipOK || !membership.Active || membership.Revision != membershipRevision || membership.PersonID != personID || membership.OrganizationID != organizationID {
		return TalentSearchGrant{}, ErrNetworkAuthorityDenied
	}
	return grant, nil
}

func (s *NetworkAuthority) matchPublishedProfilesLocked(request NetworkSearchRequest) []NetworkSearchResultReason {
	results := make([]NetworkSearchResultReason, 0)
	for _, profile := range s.profiles {
		if profile.State != "published" || profile.Discoverability != "signed_in_network" || s.validateProfileEvidenceLocked(profile) != nil || s.blockedLocked(request.SearcherPersonID, request.OrganizationID, profile.SubjectPersonID) {
			continue
		}
		matched := make([]string, 0, len(request.StructuredFilters))
		visibleFields := networkVisiblePublishedFields(profile.Fields)
		for _, filter := range request.StructuredFilters {
			found := false
			for _, field := range visibleFields {
				if networkFieldMatchesFilter(field, profile, filter, request.At) {
					found = true
					break
				}
			}
			if found {
				matched = append(matched, "Matched published "+filter.Field)
			}
		}
		if len(matched) != len(request.StructuredFilters) {
			continue
		}
		results = append(results, NetworkSearchResultReason{Projection: referenceFromHeader(profile.Header), Why: matched, Unknown: []string{"Unpublished and private fields were not considered"}})
	}
	sort.Slice(results, func(i, j int) bool {
		if len(results[i].Why) != len(results[j].Why) {
			return len(results[i].Why) > len(results[j].Why)
		}
		left := sha256Hex([]byte(request.OriginalQueryDigest + "\x00" + results[i].Projection.ID))
		right := sha256Hex([]byte(request.OriginalQueryDigest + "\x00" + results[j].Projection.ID))
		return left < right
	})
	return results
}

// networkVisiblePublishedFields is the single disclosure projection shared by
// W1 authority and W3 shadow semantics. A nil VisibleValue is a private
// commitment: it cannot affect evidence admission, matching, parity, indexing,
// or result disclosure.
func networkVisiblePublishedFields(fields []NetworkPublishedField) []NetworkPublishedField {
	visible := make([]NetworkPublishedField, 0, len(fields))
	for _, field := range fields {
		if len(field.VisibleValue) == 0 {
			continue
		}
		visible = append(visible, cloneContract(field))
	}
	return visible
}

func networkFieldMatchesFilter(field NetworkPublishedField, profile NetworkProfileProjection, filter NetworkSearchFilter, at time.Time) bool {
	if filter.Field == "freshness_bucket" {
		age := at.Sub(profile.StateChangedAt)
		bucket := "older"
		if age <= 30*24*time.Hour {
			bucket = "last_30_days"
		} else if age <= 90*24*time.Hour {
			bucket = "last_90_days"
		}
		return sha256Hex([]byte(bucket)) == filter.ValueDigest
	}
	needle := networkSearchIndexKey(filter.Field, filter.ValueDigest)
	for _, key := range networkFieldStaticIndexKeys(field) {
		if key == needle {
			return true
		}
	}
	return false
}

func networkSearchIndexKey(field, digest string) string {
	return field + "\x00" + digest
}

// networkFieldStaticIndexKeys is the single closed semantic projection used
// by both canonical W1 matching and the W3 shadow index. A commitment without
// an approved visible value is deliberately not searchable.
func networkFieldStaticIndexKeys(field NetworkPublishedField) []string {
	if len(field.VisibleValue) == 0 {
		return nil
	}
	keys := []string{networkSearchIndexKey("verification_label", sha256Hex([]byte(field.EvidenceLabel)))}
	seen := map[string]bool{keys[0]: true}
	add := func(fieldKey, digest string) {
		key := networkSearchIndexKey(fieldKey, digest)
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	add(field.FieldKey, field.ValueDigest)
	var visible any
	if json.Unmarshal(field.VisibleValue, &visible) == nil {
		switch value := visible.(type) {
		case string:
			add(field.FieldKey, sha256Hex([]byte(value)))
		case []any:
			for _, item := range value {
				if text, ok := item.(string); ok {
					add(field.FieldKey, sha256Hex([]byte(text)))
				}
			}
		}
	}
	return keys
}

func (s *NetworkAuthority) blockedLocked(searcherPersonID, searcherOrganizationID, candidatePersonID string) bool {
	for _, block := range s.blocks {
		if block.State != "active" {
			continue
		}
		if block.BlockerPersonID == candidatePersonID && (block.BlockedPersonID == searcherPersonID || block.BlockedOrganizationID == searcherOrganizationID) ||
			block.BlockerPersonID == searcherPersonID && block.BlockedPersonID == candidatePersonID {
			return true
		}
	}
	return false
}

func (s *NetworkAuthority) newSearchReceiptLocked(request NetworkSearchRequest, grant TalentSearchGrant, verdict string, reasons []string, results []NetworkSearchResultReason) NetworkSearchReceipt {
	idDigest := sha256Hex([]byte(request.IdempotencyKeyDigest + "\x00" + request.OriginalQueryDigest))
	receipt := NetworkSearchReceipt{
		Header:         STRIDEContractHeader{TenantID: request.OrganizationID, ID: "search_" + idDigest[:24], Revision: 1, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractNetworkSearchReceipt, ContentDigest: idDigest, CreatedAt: request.At.UTC()},
		OrganizationID: request.OrganizationID, Grant: referenceFromHeader(grant.Header), OriginalQueryDigest: request.OriginalQueryDigest,
		PolicyRevision: defaultNetworkPolicyRevision, PolicyVerdict: verdict, PolicyReasonCodes: append([]string(nil), reasons...), Ordering: []string{"declared_query_match", "privacy_shuffle"}, SearchedAt: request.At.UTC(),
	}
	if verdict == "allow" {
		receipt.StructuredFilters = append([]NetworkSearchFilter(nil), request.StructuredFilters...)
		receipt.InterpretationConfirmed = request.InterpretationConfirmed
		receipt.Results = cloneSearchResults(results)
	}
	return receipt
}

func (s *NetworkAuthority) recordSearchReceiptLocked(key, requestDigest string, receipt NetworkSearchReceipt) {
	s.searchReceipts[receipt.Header.ID] = cloneNetworkSearchReceipt(receipt)
	s.idempotency[key] = networkIdempotencyRecord{Digest: requestDigest, Kind: "search", ID: receipt.Header.ID, Revision: receipt.Header.Revision}
}

func (s *NetworkAuthority) emitPurgeLocked(personID string, trigger STRIDEReference, generation int64, fieldsDigest, reason string, at time.Time, _ ...[]string) DerivedPurgeReceipt {
	results := make([]PurgeStoreResult, 0, len(contributionPurgeStores))
	for _, store := range contributionPurgeStores {
		results = append(results, PurgeStoreResult{Store: store, State: "queued", AttemptCount: 1})
	}
	idDigest := sha256Hex([]byte(personID + "\x00" + trigger.ID + "\x00" + fmt.Sprint(generation) + "\x00" + reason))
	receipt := DerivedPurgeReceipt{
		Header:          STRIDEContractHeader{TenantID: triggerTenant(trigger, personID), ID: "purge_" + idDigest[:24], Revision: 1, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractDerivedPurgeReceipt, ContentDigest: idDigest, CreatedAt: at.UTC()},
		SubjectPersonID: personID, Trigger: trigger, PurgeGeneration: generation, AffectedFieldsDigest: fieldsDigest, Stores: results,
		EligibilityFencedAt: at.UTC(), RecordedAt: at.UTC(), State: "queued",
	}
	s.purges[receipt.Header.ID] = receipt
	return cloneDerivedPurgeReceipt(receipt)
}

func triggerTenant(_ STRIDEReference, personID string) string {
	if strideIdentifier(personID) {
		return STRIDEGlobalPersonTenant
	}
	return "global"
}

func (s *NetworkAuthority) nextPurgeGenerationLocked(personID string) int64 {
	s.purgeGenerations[personID]++
	return s.purgeGenerations[personID]
}

func (s *NetworkAuthority) fenceContactsLocked(personID, organizationID string, at time.Time) {
	for id, prior := range s.contacts {
		if prior.State == "declined" || prior.State == "withdrawn" || prior.State == "expired" {
			continue
		}
		if personID != "" && prior.RecipientPersonID != personID && prior.SenderPersonID != personID || organizationID != "" && prior.SenderOrganizationID != organizationID {
			continue
		}
		next := cloneContactRequest(prior)
		next.Header.Revision++
		next.Header.CreatedAt = at.UTC()
		next.Header.ContentDigest = sha256Hex([]byte(prior.Header.ContentDigest + "\x00fenced"))
		next.State = "expired"
		next.StateChangedAt = at.UTC()
		next.AcceptedChannelDigest = ""
		next.RecipientController = nil
		s.contacts[id] = next
	}
}

func (s *NetworkAuthority) fenceBlockedContactsLocked(block NetworkBlock, at time.Time) {
	for id, prior := range s.contacts {
		if oneOf(prior.State, "declined", "withdrawn", "expired") {
			continue
		}
		relevant := block.BlockedPersonID != "" && ((prior.SenderPersonID == block.BlockerPersonID && prior.RecipientPersonID == block.BlockedPersonID) || (prior.SenderPersonID == block.BlockedPersonID && prior.RecipientPersonID == block.BlockerPersonID))
		if block.BlockedOrganizationID != "" {
			relevant = prior.SenderOrganizationID == block.BlockedOrganizationID && prior.RecipientPersonID == block.BlockerPersonID
		}
		if !relevant {
			continue
		}
		next := cloneContactRequest(prior)
		next.Header.Revision++
		next.Header.CreatedAt = at.UTC()
		next.Header.ContentDigest = sha256Hex([]byte(prior.Header.ContentDigest + "\x00blocked"))
		next.State = "expired"
		next.StateChangedAt = at.UTC()
		next.AcceptedChannelDigest = ""
		next.RecipientController = nil
		s.contacts[id] = next
		s.contactVersions[networkVersionKey(id, next.Header.Revision)] = cloneContactRequest(next)
	}
}

func (s *NetworkAuthority) profileReplayLocked(personID, keyDigest, requestDigest string) (NetworkProfileProjection, *DerivedPurgeReceipt, bool, error) {
	record, ok := s.idempotency[networkIdempotencyKey("profile", personID, keyDigest)]
	if !ok {
		return NetworkProfileProjection{}, nil, false, nil
	}
	if record.Digest != requestDigest {
		return NetworkProfileProjection{}, nil, true, ErrNetworkIdempotencyConflict
	}
	return cloneNetworkProjection(s.profileVersions[networkVersionKey(record.ID, record.Revision)]), s.replayedPurgeLocked(record.RelatedID), true, nil
}

func networkVersionKey(id string, revision int64) string {
	return fmt.Sprintf("%s\x00%d", id, revision)
}

func (s *NetworkAuthority) replayedPurgeLocked(id string) *DerivedPurgeReceipt {
	if id == "" {
		return nil
	}
	receipt, ok := s.purges[id]
	if !ok {
		return nil
	}
	clone := cloneDerivedPurgeReceipt(receipt)
	return &clone
}

func networkIdempotencyKey(kind, actor, digest string) string {
	return kind + "\x00" + actor + "\x00" + digest
}

func pruneSearchWindow(values []networkTimedSearch, cutoff time.Time) []networkTimedSearch {
	out := values[:0]
	for _, value := range values {
		if !value.At.Before(cutoff) {
			out = append(out, value)
		}
	}
	return out
}

func pruneTimes(values []time.Time, cutoff time.Time) []time.Time {
	out := values[:0]
	for _, value := range values {
		if !value.Before(cutoff) {
			out = append(out, value)
		}
	}
	return out
}

func strideTestlessDigest(value string) string { return sha256Hex([]byte(strings.TrimSpace(value))) }

func cloneNetworkProjection(value NetworkProfileProjection) NetworkProfileProjection {
	value.Fields = append([]NetworkPublishedField(nil), value.Fields...)
	for index := range value.Fields {
		value.Fields[index].VisibleValue = append([]byte(nil), value.Fields[index].VisibleValue...)
		if value.Fields[index].Claim != nil {
			claim := *value.Fields[index].Claim
			value.Fields[index].Claim = &claim
		}
	}
	return value
}

func cloneTalentSearchGrant(value TalentSearchGrant) TalentSearchGrant {
	if value.RevokedAt != nil {
		at := *value.RevokedAt
		value.RevokedAt = &at
	}
	return value
}

func cloneSearchResults(values []NetworkSearchResultReason) []NetworkSearchResultReason {
	out := append([]NetworkSearchResultReason(nil), values...)
	for index := range out {
		out[index].Why = append([]string(nil), out[index].Why...)
		out[index].Unknown = append([]string(nil), out[index].Unknown...)
	}
	return out
}

func cloneNetworkSearchReceipt(value NetworkSearchReceipt) NetworkSearchReceipt {
	value.PolicyReasonCodes = append([]string(nil), value.PolicyReasonCodes...)
	value.StructuredFilters = append([]NetworkSearchFilter(nil), value.StructuredFilters...)
	value.Ordering = append([]string(nil), value.Ordering...)
	value.Results = cloneSearchResults(value.Results)
	if value.RouteRevision != nil {
		ref := *value.RouteRevision
		value.RouteRevision = &ref
	}
	return value
}

func cloneContactRequest(value ContactRequest) ContactRequest {
	if value.RecipientController != nil {
		controller := *value.RecipientController
		value.RecipientController = &controller
	}
	return value
}

func cloneDerivedPurgeReceipt(value DerivedPurgeReceipt) DerivedPurgeReceipt {
	value.Stores = append([]PurgeStoreResult(nil), value.Stores...)
	for index := range value.Stores {
		if value.Stores[index].CompletedAt != nil {
			at := *value.Stores[index].CompletedAt
			value.Stores[index].CompletedAt = &at
		}
	}
	return value
}
