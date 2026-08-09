package main

// ContributionAuthorityService is a route-free, body-free deterministic policy
// adapter. It proves controller, CAS, idempotency, lifecycle, and drift-fencing
// behavior without granting an HTTP route, worker, provider, or feature switch.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	ErrContributionAuthorityDenied   = errors.New("contribution authority denied")
	ErrContributionAuthorityConflict = errors.New("contribution authority conflict")
	ErrContributionAuthorityInvalid  = errors.New("invalid contribution authority request")
	ErrContributionAuthorityNotFound = errors.New("contribution authority object not found")
)

type ContributionAuthorityGrant struct {
	ID             string                   `json:"id"`
	Role           string                   `json:"role"`
	OrganizationID string                   `json:"organizationId,omitempty"`
	PersonID       string                   `json:"personId,omitempty"`
	PartyID        string                   `json:"partyId,omitempty"`
	Controller     STRIDEControllerRevision `json:"controller"`
}

func (v ContributionAuthorityGrant) Validate() error {
	if !strideIdentifier(v.ID) || v.Controller.Validate() != nil || !oneOf(v.Role, "subject", "organization_reviewer", "named_party", "signing_issuer", "person_publisher", "outcome_reviewer", "drift_controller") {
		return ErrContributionAuthorityInvalid
	}
	switch v.Role {
	case "subject", "person_publisher":
		if !strideIdentifier(v.PersonID) || v.OrganizationID != "" || v.PartyID != "" || v.Controller.PrincipalID != v.PersonID {
			return ErrContributionAuthorityInvalid
		}
	case "named_party":
		if !strideIdentifier(v.PartyID) || v.OrganizationID != "" || v.PersonID != "" || v.Controller.PrincipalID != v.PartyID {
			return ErrContributionAuthorityInvalid
		}
	default:
		if !strideIdentifier(v.OrganizationID) || v.PersonID != "" || v.PartyID != "" {
			return ErrContributionAuthorityInvalid
		}
	}
	return nil
}

type ContributionAuthorityAssertion struct {
	GrantID              string                   `json:"grantId"`
	Controller           STRIDEControllerRevision `json:"controller"`
	ExpectedRevision     int64                    `json:"expectedRevision"`
	IdempotencyKeyDigest string                   `json:"idempotencyKeyDigest"`
	At                   time.Time                `json:"at"`
}

func (v ContributionAuthorityAssertion) Validate() error {
	if !strideIdentifier(v.GrantID) || v.Controller.Validate() != nil || v.ExpectedRevision < 0 || !isHexDigest(v.IdempotencyKeyDigest) || v.At.IsZero() {
		return ErrContributionAuthorityInvalid
	}
	return nil
}

type ContributionAuthorityDrift struct {
	Kind               string           `json:"kind"`
	OrganizationID     string           `json:"organizationId"`
	Source             *STRIDEReference `json:"source,omitempty"`
	CurrentSource      *STRIDEReference `json:"currentSource,omitempty"`
	ApprovalID         string           `json:"approvalId,omitempty"`
	NewACLRevision     int64            `json:"newAclRevision,omitempty"`
	NewConsentRevision int64            `json:"newConsentRevision,omitempty"`
	NewPurgeGeneration int64            `json:"newPurgeGeneration"`
	ReasonDigest       string           `json:"reasonDigest"`
}

func (v ContributionAuthorityDrift) Validate() error {
	if !oneOf(v.Kind, "source", "acl", "consent", "purge", "field_approval") || !strideIdentifier(v.OrganizationID) || v.NewPurgeGeneration < 1 || !isHexDigest(v.ReasonDigest) {
		return ErrContributionAuthorityInvalid
	}
	if v.Kind == "field_approval" {
		if !strideIdentifier(v.ApprovalID) || v.Source != nil || v.CurrentSource != nil {
			return ErrContributionAuthorityInvalid
		}
	} else if v.Source == nil || v.Source.Validate() != nil || v.ApprovalID != "" {
		return ErrContributionAuthorityInvalid
	}
	if v.Kind == "source" && (v.CurrentSource == nil || v.CurrentSource.Validate() != nil || v.CurrentSource.ContractType != v.Source.ContractType || v.CurrentSource.ID != v.Source.ID || v.CurrentSource.Revision <= v.Source.Revision) || v.Kind != "source" && v.CurrentSource != nil {
		return ErrContributionAuthorityInvalid
	}
	if v.Kind == "acl" && v.NewACLRevision < 1 || v.Kind == "consent" && v.NewConsentRevision < 1 {
		return ErrContributionAuthorityInvalid
	}
	return nil
}

type ContributionFenceEffect struct {
	Claim                STRIDEReference     `json:"claim"`
	Publication          STRIDEReference     `json:"publication"`
	AffectedFields       []string            `json:"affectedFields"`
	AffectedFieldsDigest string              `json:"affectedFieldsDigest"`
	PurgeReceipt         DerivedPurgeReceipt `json:"purgeReceipt"`
}

type contributionIdempotencyResult struct {
	Operation     string
	RequestDigest string
	Value         any
}

type ContributionAuthorityService struct {
	mu                 sync.RWMutex
	grants             map[string]ContributionAuthorityGrant
	claims             map[string]ContributionClaim
	claimHistory       map[string]ContributionClaim
	approvals          map[string]FieldReleaseApproval
	approvalHistory    map[string]FieldReleaseApproval
	attestations       map[string]ContributionAttestation
	attestationHistory map[string]ContributionAttestation
	publications       map[string]PublishedContributionClaim
	publicationHistory map[string]PublishedContributionClaim
	influences         map[string]AgentInfluenceReceipt
	fencedFields       map[string]map[string]bool
	purgeQueue         map[string]DerivedPurgeReceipt
	purgeGenerations   map[string]int64
	idempotency        map[string]contributionIdempotencyResult
}

func NewContributionAuthorityService(grants []ContributionAuthorityGrant) (*ContributionAuthorityService, error) {
	service := &ContributionAuthorityService{grants: map[string]ContributionAuthorityGrant{}, claims: map[string]ContributionClaim{}, claimHistory: map[string]ContributionClaim{}, approvals: map[string]FieldReleaseApproval{}, approvalHistory: map[string]FieldReleaseApproval{}, attestations: map[string]ContributionAttestation{}, attestationHistory: map[string]ContributionAttestation{}, publications: map[string]PublishedContributionClaim{}, publicationHistory: map[string]PublishedContributionClaim{}, influences: map[string]AgentInfluenceReceipt{}, fencedFields: map[string]map[string]bool{}, purgeQueue: map[string]DerivedPurgeReceipt{}, purgeGenerations: map[string]int64{}, idempotency: map[string]contributionIdempotencyResult{}}
	for _, grant := range grants {
		if grant.Validate() != nil || service.grants[grant.ID].ID != "" {
			return nil, ErrContributionAuthorityInvalid
		}
		service.grants[grant.ID] = grant
	}
	return service, nil
}

func (s *ContributionAuthorityService) authorize(assertion ContributionAuthorityAssertion, role, organizationID, personID, partyID string) (ContributionAuthorityGrant, error) {
	if s == nil || assertion.Validate() != nil {
		return ContributionAuthorityGrant{}, ErrContributionAuthorityInvalid
	}
	grant, ok := s.grants[assertion.GrantID]
	if !ok || grant.Role != role || grant.Controller != assertion.Controller || grant.OrganizationID != organizationID || grant.PersonID != personID || grant.PartyID != partyID {
		return ContributionAuthorityGrant{}, ErrContributionAuthorityDenied
	}
	return grant, nil
}

func (s *ContributionAuthorityService) replay(key, operation, requestDigest string) (any, bool, error) {
	prior, ok := s.idempotency[key]
	if !ok {
		return nil, false, nil
	}
	if prior.Operation != operation || prior.RequestDigest != requestDigest {
		return nil, false, ErrContributionAuthorityConflict
	}
	return cloneContributionValue(prior.Value), true, nil
}

func (s *ContributionAuthorityService) record(key, operation, requestDigest string, value any) {
	s.idempotency[key] = contributionIdempotencyResult{Operation: operation, RequestDigest: requestDigest, Value: cloneContributionValue(value)}
}

func authorityRequestDigest(values ...any) (string, error) { return STRIDEContractDigest(values) }

func (s *ContributionAuthorityService) CreateClaim(claim ContributionClaim, assertion ContributionAuthorityAssertion) (ContributionClaim, error) {
	if claim.Validate() != nil || claim.State != "candidate" || assertion.ExpectedRevision != 0 {
		return ContributionClaim{}, ErrContributionAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.authorize(assertion, "organization_reviewer", claim.OrganizationID, "", ""); err != nil {
		return ContributionClaim{}, err
	}
	digest, _ := authorityRequestDigest(claim, assertion.Controller)
	if prior, ok, err := s.replay(assertion.IdempotencyKeyDigest, "create_claim", digest); err != nil {
		return ContributionClaim{}, err
	} else if ok {
		return prior.(ContributionClaim), nil
	}
	if _, exists := s.claims[claim.Header.ID]; exists {
		return ContributionClaim{}, ErrContributionAuthorityConflict
	}
	if err := s.storeClaimLocked(claim); err != nil {
		return ContributionClaim{}, err
	}
	s.record(assertion.IdempotencyKeyDigest, "create_claim", digest, claim)
	return claim, nil
}

func (s *ContributionAuthorityService) SubjectReview(claimID string, disputed bool, assertion ContributionAuthorityAssertion) (ContributionClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	claim, ok := s.claims[claimID]
	if !ok {
		return ContributionClaim{}, ErrContributionAuthorityNotFound
	}
	if _, err := s.authorize(assertion, "subject", "", claim.SubjectPersonID, ""); err != nil {
		return ContributionClaim{}, err
	}
	target := "subject_review"
	if disputed {
		target = "disputed"
	}
	digest, _ := authorityRequestDigest(claimID, target, assertion.Controller, assertion.ExpectedRevision)
	if prior, ok, err := s.replay(assertion.IdempotencyKeyDigest, "subject_review", digest); err != nil {
		return ContributionClaim{}, err
	} else if ok {
		return prior.(ContributionClaim), nil
	}
	if assertion.ExpectedRevision != claim.Header.Revision || !ContributionClaimTransitionAllowed(claim.State, target) {
		return ContributionClaim{}, ErrContributionAuthorityConflict
	}
	claim.SubjectReview = &assertion.Controller
	claim.State = target
	advanceClaimRevision(&claim, target, assertion.At)
	if err := s.storeClaimLocked(claim); err != nil {
		return ContributionClaim{}, err
	}
	s.record(assertion.IdempotencyKeyDigest, "subject_review", digest, claim)
	return claim, nil
}

func (s *ContributionAuthorityService) ReviewClaim(claimID string, assertion ContributionAuthorityAssertion) (ContributionClaim, error) {
	return s.SubjectReview(claimID, false, assertion)
}

func (s *ContributionAuthorityService) DisputeClaim(claimID string, assertion ContributionAuthorityAssertion) (ContributionClaim, error) {
	return s.SubjectReview(claimID, true, assertion)
}

func (s *ContributionAuthorityService) VerifyClaim(claimID string, assertion ContributionAuthorityAssertion) (ContributionClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	claim, ok := s.claims[claimID]
	if !ok {
		return ContributionClaim{}, ErrContributionAuthorityNotFound
	}
	if _, err := s.authorize(assertion, "organization_reviewer", claim.OrganizationID, "", ""); err != nil {
		return ContributionClaim{}, err
	}
	digest, _ := authorityRequestDigest(claimID, "verified", assertion.Controller, assertion.ExpectedRevision)
	if prior, ok, err := s.replay(assertion.IdempotencyKeyDigest, "verify_claim", digest); err != nil {
		return ContributionClaim{}, err
	} else if ok {
		return prior.(ContributionClaim), nil
	}
	if assertion.ExpectedRevision != claim.Header.Revision || claim.SubjectReview == nil || !ContributionClaimTransitionAllowed(claim.State, "verified") {
		return ContributionClaim{}, ErrContributionAuthorityConflict
	}
	claim.OrganizationReview = &assertion.Controller
	claim.State = "verified"
	advanceClaimRevision(&claim, "verified", assertion.At)
	if claim.Validate() != nil {
		return ContributionClaim{}, ErrContributionAuthorityInvalid
	}
	if err := s.storeClaimLocked(claim); err != nil {
		return ContributionClaim{}, err
	}
	s.record(assertion.IdempotencyKeyDigest, "verify_claim", digest, claim)
	return claim, nil
}

func (s *ContributionAuthorityService) RequireClaimRevalidation(claimID string, purgeGeneration int64, reasonDigest string, assertion ContributionAuthorityAssertion) (ContributionClaim, []ContributionFenceEffect, error) {
	if purgeGeneration < 1 || !isHexDigest(reasonDigest) {
		return ContributionClaim{}, nil, ErrContributionAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	claim, ok := s.claims[claimID]
	if !ok {
		return ContributionClaim{}, nil, ErrContributionAuthorityNotFound
	}
	if _, err := s.authorize(assertion, "drift_controller", claim.OrganizationID, "", ""); err != nil {
		return ContributionClaim{}, nil, err
	}
	digest, _ := authorityRequestDigest(claimID, purgeGeneration, reasonDigest, assertion.Controller, assertion.ExpectedRevision)
	if prior, ok, err := s.replay(assertion.IdempotencyKeyDigest, "require_claim_revalidation", digest); err != nil {
		return ContributionClaim{}, nil, err
	} else if ok {
		result := prior.(claimFenceResult)
		return result.Claim, result.Effects, nil
	}
	if assertion.ExpectedRevision != claim.Header.Revision || purgeGeneration <= claim.PurgeGeneration || !ContributionClaimTransitionAllowed(claim.State, "revalidation_required") {
		return ContributionClaim{}, nil, ErrContributionAuthorityConflict
	}
	claim.State = "revalidation_required"
	claim.PurgeGeneration = purgeGeneration
	advanceClaimRevision(&claim, "revalidation_required:"+reasonDigest, assertion.At)
	if err := s.storeClaimLocked(claim); err != nil {
		return ContributionClaim{}, nil, err
	}
	effects := s.fenceClaimLocked(claim, "revalidation_required", purgeGeneration, assertion.At, nil)
	result := claimFenceResult{Claim: claim, Effects: effects}
	s.record(assertion.IdempotencyKeyDigest, "require_claim_revalidation", digest, result)
	return claim, effects, nil
}

func (s *ContributionAuthorityService) RevokeClaim(claimID string, assertion ContributionAuthorityAssertion) (ContributionClaim, []ContributionFenceEffect, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	claim, ok := s.claims[claimID]
	if !ok {
		return ContributionClaim{}, nil, ErrContributionAuthorityNotFound
	}
	if _, err := s.authorize(assertion, "organization_reviewer", claim.OrganizationID, "", ""); err != nil {
		return ContributionClaim{}, nil, err
	}
	digest, _ := authorityRequestDigest(claimID, "revoked", assertion.Controller, assertion.ExpectedRevision)
	if prior, ok, err := s.replay(assertion.IdempotencyKeyDigest, "revoke_claim", digest); err != nil {
		return ContributionClaim{}, nil, err
	} else if ok {
		result := prior.(claimFenceResult)
		return result.Claim, result.Effects, nil
	}
	if assertion.ExpectedRevision != claim.Header.Revision || !ContributionClaimTransitionAllowed(claim.State, "revoked") {
		return ContributionClaim{}, nil, ErrContributionAuthorityConflict
	}
	claim.State = "revoked"
	claim.OrganizationReview = &assertion.Controller
	advanceClaimRevision(&claim, "revoked", assertion.At)
	if claim.Validate() != nil {
		return ContributionClaim{}, nil, ErrContributionAuthorityInvalid
	}
	if err := s.storeClaimLocked(claim); err != nil {
		return ContributionClaim{}, nil, err
	}
	effects := s.fenceClaimLocked(claim, "claim_revoke", claim.PurgeGeneration+1, assertion.At, nil)
	result := claimFenceResult{Claim: claim, Effects: effects}
	s.record(assertion.IdempotencyKeyDigest, "revoke_claim", digest, result)
	return claim, effects, nil
}

type claimFenceResult struct {
	Claim   ContributionClaim
	Effects []ContributionFenceEffect
}

type approvalFenceResult struct {
	Approval FieldReleaseApproval
	Effects  []ContributionFenceEffect
}
type attestationFenceResult struct {
	Attestation ContributionAttestation
	Effects     []ContributionFenceEffect
}
type publicationFenceResult struct {
	Publication PublishedContributionClaim
	Effects     []ContributionFenceEffect
}

func (s *ContributionAuthorityService) SupersedeClaim(claimID string, replacement ContributionClaim, assertion ContributionAuthorityAssertion) (ContributionClaim, error) {
	if replacement.Validate() != nil || replacement.State != "verified" || replacement.Header.ID == claimID {
		return ContributionClaim{}, ErrContributionAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	claim, ok := s.claims[claimID]
	if !ok {
		return ContributionClaim{}, ErrContributionAuthorityNotFound
	}
	if _, err := s.authorize(assertion, "organization_reviewer", claim.OrganizationID, "", ""); err != nil {
		return ContributionClaim{}, err
	}
	digest, _ := authorityRequestDigest(claimID, replacement, assertion.Controller, assertion.ExpectedRevision)
	if prior, ok, err := s.replay(assertion.IdempotencyKeyDigest, "supersede_claim", digest); err != nil {
		return ContributionClaim{}, err
	} else if ok {
		return prior.(ContributionClaim), nil
	}
	if assertion.ExpectedRevision != claim.Header.Revision || replacement.OrganizationID != claim.OrganizationID || replacement.SubjectPersonID != claim.SubjectPersonID || !ContributionClaimTransitionAllowed(claim.State, "superseded") {
		return ContributionClaim{}, ErrContributionAuthorityConflict
	}
	if _, exists := s.claims[replacement.Header.ID]; exists {
		return ContributionClaim{}, ErrContributionAuthorityConflict
	}
	claim.State = "superseded"
	claim.OrganizationReview = &assertion.Controller
	advanceClaimRevision(&claim, "superseded", assertion.At)
	if replacement.SubjectReview == nil || replacement.SubjectReview.PrincipalID != replacement.SubjectPersonID || replacement.OrganizationReview == nil || *replacement.OrganizationReview != assertion.Controller || replacement.Header.Revision != 1 || replacement.Supersedes != nil {
		return ContributionClaim{}, ErrContributionAuthorityDenied
	}
	if !s.hasExactGrantLocked("subject", "", replacement.SubjectPersonID, "", *replacement.SubjectReview) {
		return ContributionClaim{}, ErrContributionAuthorityDenied
	}
	if err := s.storeClaimLocked(claim); err != nil {
		return ContributionClaim{}, err
	}
	if err := s.storeClaimLocked(replacement); err != nil {
		return ContributionClaim{}, err
	}
	s.fenceClaimLocked(claim, "claim_supersede", claim.PurgeGeneration+1, assertion.At, nil)
	s.record(assertion.IdempotencyKeyDigest, "supersede_claim", digest, claim)
	return claim, nil
}

// CorrectClaim records correction as an immutable replacement and supersedes
// the old verified revision; it never edits the prior evidence in place.
func (s *ContributionAuthorityService) CorrectClaim(claimID string, corrected ContributionClaim, assertion ContributionAuthorityAssertion) (ContributionClaim, error) {
	return s.SupersedeClaim(claimID, corrected, assertion)
}

func (s *ContributionAuthorityService) PutFieldApproval(approval FieldReleaseApproval, assertion ContributionAuthorityAssertion) (FieldReleaseApproval, error) {
	if approval.Validate() != nil || approval.State != "pending" || assertion.ExpectedRevision != 0 {
		return FieldReleaseApproval{}, ErrContributionAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	claimAtt, ok := s.attestations[approval.Attestation.ID]
	if !ok && approval.Attestation.Revision != 1 {
		return FieldReleaseApproval{}, ErrContributionAuthorityNotFound
	}
	_ = claimAtt // A pending approval may precede issuance of attestation revision 1.
	if _, err := s.authorize(assertion, "organization_reviewer", approval.OrganizationID, "", ""); err != nil {
		return FieldReleaseApproval{}, err
	}
	digest, _ := authorityRequestDigest(approval, assertion.Controller)
	if prior, ok, err := s.replay(assertion.IdempotencyKeyDigest, "put_field_approval", digest); err != nil {
		return FieldReleaseApproval{}, err
	} else if ok {
		return prior.(FieldReleaseApproval), nil
	}
	if _, exists := s.approvals[approval.Header.ID]; exists {
		return FieldReleaseApproval{}, ErrContributionAuthorityConflict
	}
	if err := s.storeApprovalLocked(approval); err != nil {
		return FieldReleaseApproval{}, err
	}
	s.record(assertion.IdempotencyKeyDigest, "put_field_approval", digest, approval)
	return approval, nil
}

func (s *ContributionAuthorityService) DecideFieldApproval(approvalID, decision string, assertion ContributionAuthorityAssertion) (FieldReleaseApproval, []ContributionFenceEffect, error) {
	if !oneOf(decision, "approved", "denied", "withdrawn") {
		return FieldReleaseApproval{}, nil, ErrContributionAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	approval, ok := s.approvals[approvalID]
	if !ok {
		return FieldReleaseApproval{}, nil, ErrContributionAuthorityNotFound
	}
	role, organization, person, party := "named_party", "", "", approval.ApproverPartyID
	if approval.ApproverRole == "subject" {
		role, person, party = "subject", approval.SubjectPersonID, ""
	}
	if approval.ApproverRole == "organization" {
		role, organization, party = "organization_reviewer", approval.OrganizationID, ""
	}
	if _, err := s.authorize(assertion, role, organization, person, party); err != nil {
		return FieldReleaseApproval{}, nil, err
	}
	digest, _ := authorityRequestDigest(approvalID, decision, assertion.Controller, assertion.ExpectedRevision)
	if prior, ok, err := s.replay(assertion.IdempotencyKeyDigest, "decide_field_approval", digest); err != nil {
		return FieldReleaseApproval{}, nil, err
	} else if ok {
		result := prior.(approvalFenceResult)
		return result.Approval, result.Effects, nil
	}
	if assertion.ExpectedRevision != approval.Header.Revision || approval.State != "pending" && !(approval.State == "approved" && decision == "withdrawn") {
		return FieldReleaseApproval{}, nil, ErrContributionAuthorityConflict
	}
	approval.State = decision
	approval.Controller = assertion.Controller
	approval.StateChangedAt = assertion.At
	priorApproval := refForHeader(approval.Header)
	approval.Header = nextAuthorityHeader(approval.Header, decision, assertion.At)
	approval.Supersedes = &priorApproval
	if decision == "approved" {
		at := assertion.At
		approval.ApprovedAt = &at
	} else {
		approval.ApprovedAt = nil
	}
	if approval.Validate() != nil {
		return FieldReleaseApproval{}, nil, ErrContributionAuthorityInvalid
	}
	if err := s.storeApprovalLocked(approval); err != nil {
		return FieldReleaseApproval{}, nil, err
	}
	var effects []ContributionFenceEffect
	if decision == "withdrawn" {
		effects = s.fenceApprovalLocked(approval, assertion.At)
	}
	s.record(assertion.IdempotencyKeyDigest, "decide_field_approval", digest, approvalFenceResult{Approval: approval, Effects: effects})
	return approval, effects, nil
}

func (s *ContributionAuthorityService) IssueAttestation(attestation ContributionAttestation, assertion ContributionAuthorityAssertion) (ContributionAttestation, error) {
	if attestation.Validate() != nil || attestation.State != "active" || assertion.ExpectedRevision != 0 {
		return ContributionAttestation{}, ErrContributionAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.authorize(assertion, "signing_issuer", attestation.OrganizationID, "", ""); err != nil {
		return ContributionAttestation{}, err
	}
	digest, _ := authorityRequestDigest(attestation, assertion.Controller)
	if prior, ok, err := s.replay(assertion.IdempotencyKeyDigest, "issue_attestation", digest); err != nil {
		return ContributionAttestation{}, err
	} else if ok {
		return prior.(ContributionAttestation), nil
	}
	if err := s.validateAttestationAdmissionLocked(attestation); err != nil {
		return ContributionAttestation{}, err
	}
	if _, exists := s.attestations[attestation.Header.ID]; exists {
		return ContributionAttestation{}, ErrContributionAuthorityConflict
	}
	if err := s.storeAttestationLocked(attestation); err != nil {
		return ContributionAttestation{}, err
	}
	s.record(assertion.IdempotencyKeyDigest, "issue_attestation", digest, attestation)
	return attestation, nil
}

func (s *ContributionAuthorityService) RevokeAttestation(attestationID string, assertion ContributionAuthorityAssertion) (ContributionAttestation, []ContributionFenceEffect, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	attestation, ok := s.attestations[attestationID]
	if !ok {
		return ContributionAttestation{}, nil, ErrContributionAuthorityNotFound
	}
	if _, err := s.authorize(assertion, "signing_issuer", attestation.OrganizationID, "", ""); err != nil {
		return ContributionAttestation{}, nil, err
	}
	digest, _ := authorityRequestDigest(attestationID, "revoke", assertion.Controller, assertion.ExpectedRevision)
	if prior, ok, err := s.replay(assertion.IdempotencyKeyDigest, "revoke_attestation", digest); err != nil {
		return ContributionAttestation{}, nil, err
	} else if ok {
		result := prior.(attestationFenceResult)
		return result.Attestation, result.Effects, nil
	}
	if assertion.ExpectedRevision != attestation.Header.Revision || attestation.State != "active" {
		return ContributionAttestation{}, nil, ErrContributionAuthorityConflict
	}
	at := assertion.At
	priorAttestation := refForHeader(attestation.Header)
	attestation.State = "revoked"
	attestation.RevokedAt = &at
	attestation.Header = nextAuthorityHeader(attestation.Header, "revoked", at)
	attestation.Supersedes = &priorAttestation
	if err := s.storeAttestationLocked(attestation); err != nil {
		return ContributionAttestation{}, nil, err
	}
	claim := s.claims[attestation.Claim.ID]
	effects := s.fenceClaimLocked(claim, "attestation_revoke", claim.PurgeGeneration+1, at, nil)
	s.record(assertion.IdempotencyKeyDigest, "revoke_attestation", digest, attestationFenceResult{Attestation: attestation, Effects: effects})
	return attestation, effects, nil
}

func (s *ContributionAuthorityService) SupersedeAttestation(attestationID string, replacement ContributionAttestation, assertion ContributionAuthorityAssertion) (ContributionAttestation, error) {
	if replacement.Validate() != nil || replacement.State != "active" || replacement.Header.ID == attestationID {
		return ContributionAttestation{}, ErrContributionAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.attestations[attestationID]
	if !ok {
		return ContributionAttestation{}, ErrContributionAuthorityNotFound
	}
	if _, err := s.authorize(assertion, "signing_issuer", current.OrganizationID, "", ""); err != nil {
		return ContributionAttestation{}, err
	}
	digest, _ := authorityRequestDigest(attestationID, replacement, assertion.Controller, assertion.ExpectedRevision)
	if prior, ok, err := s.replay(assertion.IdempotencyKeyDigest, "supersede_attestation", digest); err != nil {
		return ContributionAttestation{}, err
	} else if ok {
		return prior.(ContributionAttestation), nil
	}
	if assertion.ExpectedRevision != current.Header.Revision || current.State != "active" || replacement.OrganizationID != current.OrganizationID || replacement.SubjectPersonID != current.SubjectPersonID {
		return ContributionAttestation{}, ErrContributionAuthorityConflict
	}
	if err := s.validateAttestationAdmissionLocked(replacement); err != nil {
		return ContributionAttestation{}, err
	}
	priorAttestation := refForHeader(current.Header)
	current.State = "superseded"
	at := assertion.At
	current.RevokedAt = &at
	current.Header = nextAuthorityHeader(current.Header, "superseded", at)
	current.Supersedes = &priorAttestation
	if err := s.storeAttestationLocked(current); err != nil {
		return ContributionAttestation{}, err
	}
	if err := s.storeAttestationLocked(replacement); err != nil {
		return ContributionAttestation{}, err
	}
	claim := s.claims[current.Claim.ID]
	s.fenceClaimLocked(claim, "attestation_supersede", claim.PurgeGeneration+1, at, nil)
	s.record(assertion.IdempotencyKeyDigest, "supersede_attestation", digest, current)
	return current, nil
}

func (s *ContributionAuthorityService) Publish(publication PublishedContributionClaim, assertion ContributionAuthorityAssertion) (PublishedContributionClaim, error) {
	if publication.Validate() != nil || publication.State != "published" || assertion.ExpectedRevision != 0 {
		return PublishedContributionClaim{}, ErrContributionAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.authorize(assertion, "person_publisher", "", publication.SubjectPersonID, ""); err != nil {
		return PublishedContributionClaim{}, err
	}
	digest, _ := authorityRequestDigest(publication, assertion.Controller)
	if prior, ok, err := s.replay(assertion.IdempotencyKeyDigest, "publish", digest); err != nil {
		return PublishedContributionClaim{}, err
	} else if ok {
		return prior.(PublishedContributionClaim), nil
	}
	for _, ref := range publication.Attestations {
		att, ok := s.attestations[ref.ID]
		if !ok || att.Header.Revision != ref.Revision || att.Header.ContentDigest != ref.Digest || att.State != "active" || att.SubjectPersonID != publication.SubjectPersonID {
			return PublishedContributionClaim{}, ErrContributionAuthorityDenied
		}
		if err := s.validateAttestationAdmissionLocked(att); err != nil {
			return PublishedContributionClaim{}, ErrContributionAuthorityDenied
		}
	}
	if _, exists := s.publications[publication.Header.ID]; exists {
		return PublishedContributionClaim{}, ErrContributionAuthorityConflict
	}
	if err := s.storePublicationLocked(publication); err != nil {
		return PublishedContributionClaim{}, err
	}
	s.record(assertion.IdempotencyKeyDigest, "publish", digest, publication)
	return publication, nil
}

func (s *ContributionAuthorityService) WithdrawPublication(publicationID string, assertion ContributionAuthorityAssertion) (PublishedContributionClaim, []ContributionFenceEffect, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	publication, ok := s.publications[publicationID]
	if !ok {
		return PublishedContributionClaim{}, nil, ErrContributionAuthorityNotFound
	}
	if _, err := s.authorize(assertion, "person_publisher", "", publication.SubjectPersonID, ""); err != nil {
		return PublishedContributionClaim{}, nil, err
	}
	digest, _ := authorityRequestDigest(publicationID, "withdraw", assertion.Controller, assertion.ExpectedRevision)
	if prior, ok, err := s.replay(assertion.IdempotencyKeyDigest, "withdraw_publication", digest); err != nil {
		return PublishedContributionClaim{}, nil, err
	} else if ok {
		result := prior.(publicationFenceResult)
		return result.Publication, result.Effects, nil
	}
	if assertion.ExpectedRevision != publication.Header.Revision || !PublishedContributionTransitionAllowed(publication.State, "withdrawn") {
		return PublishedContributionClaim{}, nil, ErrContributionAuthorityConflict
	}
	publication.State = "withdrawn"
	publication.Visibility = "private"
	publication.StateChangedAt = assertion.At
	priorPublication := refForHeader(publication.Header)
	publication.Header = nextAuthorityHeader(publication.Header, "withdrawn", assertion.At)
	publication.Supersedes = &priorPublication
	if err := s.storePublicationLocked(publication); err != nil {
		return PublishedContributionClaim{}, nil, err
	}
	effects := s.fencePublicationLocked(publication, nil, "publication_withdraw", 1, assertion.At)
	s.record(assertion.IdempotencyKeyDigest, "withdraw_publication", digest, publicationFenceResult{Publication: publication, Effects: effects})
	return publication, effects, nil
}

func (s *ContributionAuthorityService) AdmitAgentInfluence(receipt AgentInfluenceReceipt, assertion ContributionAuthorityAssertion) (AgentInfluenceReceipt, error) {
	if receipt.Validate() != nil || receipt.State != "verified" || assertion.ExpectedRevision != 0 {
		return AgentInfluenceReceipt{}, ErrContributionAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.authorize(assertion, "outcome_reviewer", receipt.OrganizationID, "", ""); err != nil || receipt.Reviewer != assertion.Controller {
		if err != nil {
			return AgentInfluenceReceipt{}, err
		}
		return AgentInfluenceReceipt{}, ErrContributionAuthorityDenied
	}
	digest, _ := authorityRequestDigest(receipt, assertion.Controller)
	if prior, ok, err := s.replay(assertion.IdempotencyKeyDigest, "admit_agent_influence", digest); err != nil {
		return AgentInfluenceReceipt{}, err
	} else if ok {
		return prior.(AgentInfluenceReceipt), nil
	}
	if _, exists := s.influences[receipt.Header.ID]; exists {
		return AgentInfluenceReceipt{}, ErrContributionAuthorityConflict
	}
	s.influences[receipt.Header.ID] = cloneContract(receipt)
	s.record(assertion.IdempotencyKeyDigest, "admit_agent_influence", digest, receipt)
	return receipt, nil
}

func (s *ContributionAuthorityService) FenceDrift(drift ContributionAuthorityDrift, assertion ContributionAuthorityAssertion) ([]ContributionFenceEffect, error) {
	if drift.Validate() != nil {
		return nil, ErrContributionAuthorityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.authorize(assertion, "drift_controller", drift.OrganizationID, "", ""); err != nil {
		return nil, err
	}
	digest, _ := authorityRequestDigest(drift, assertion.Controller)
	if prior, ok, err := s.replay(assertion.IdempotencyKeyDigest, "fence_drift", digest); err != nil {
		return nil, err
	} else if ok {
		return prior.([]ContributionFenceEffect), nil
	}
	var effects []ContributionFenceEffect
	for id, claim := range s.claims {
		if claim.OrganizationID != drift.OrganizationID || claim.State != "verified" {
			continue
		}
		var affectedFields []string
		matched := false
		if drift.Kind == "field_approval" {
			approval, ok := s.approvals[drift.ApprovalID]
			if !ok {
				continue
			}
			for _, attestation := range s.attestations {
				if attestation.Claim.ID == id {
					for _, field := range attestation.ReleasedFields {
						for _, ref := range field.ApprovalRefs {
							if ref.ID == approval.Header.ID {
								matched = true
								affectedFields = append(affectedFields, field.FieldKey)
							}
						}
					}
				}
			}
		} else {
			for _, source := range claim.SourceRefs {
				if source == *drift.Source {
					matched = true
					break
				}
			}
		}
		if !matched {
			continue
		}
		if drift.NewPurgeGeneration <= claim.PurgeGeneration {
			return nil, ErrContributionAuthorityConflict
		}
		claim.State = "revalidation_required"
		claim.PurgeGeneration = drift.NewPurgeGeneration
		if drift.NewACLRevision > 0 {
			claim.ACLRevision = drift.NewACLRevision
		}
		if drift.NewConsentRevision > 0 {
			claim.ConsentRevision = drift.NewConsentRevision
		}
		advanceClaimRevision(&claim, "revalidation_required:"+drift.ReasonDigest, assertion.At)
		if err := s.storeClaimLocked(claim); err != nil {
			return nil, err
		}
		effects = append(effects, s.fenceClaimLocked(claim, drift.Kind, drift.NewPurgeGeneration, assertion.At, affectedFields)...)
	}
	s.record(assertion.IdempotencyKeyDigest, "fence_drift", digest, effects)
	return effects, nil
}

func (s *ContributionAuthorityService) FieldEligible(publicationID, field string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	publication, ok := s.publications[publicationID]
	if !ok || publication.State != "published" {
		return false
	}
	return !s.fencedFields[publicationID][field]
}

func (s *ContributionAuthorityService) PurgeQueue() []DerivedPurgeReceipt {
	s.mu.RLock()
	defer s.mu.RUnlock()
	values := make([]DerivedPurgeReceipt, 0, len(s.purgeQueue))
	for _, value := range s.purgeQueue {
		values = append(values, cloneContract(value))
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Header.ID < values[j].Header.ID })
	return values
}

func (s *ContributionAuthorityService) fenceApprovalLocked(approval FieldReleaseApproval, at time.Time) []ContributionFenceEffect {
	var effects []ContributionFenceEffect
	for _, attestation := range s.attestations {
		for _, field := range attestation.ReleasedFields {
			for _, ref := range field.ApprovalRefs {
				if ref.ID == approval.Header.ID {
					claim := s.claims[attestation.Claim.ID]
					effects = append(effects, s.fenceClaimLocked(claim, "field_approval", claim.PurgeGeneration+1, at, []string{field.FieldKey})...)
				}
			}
		}
	}
	return effects
}

func (s *ContributionAuthorityService) fenceClaimLocked(claim ContributionClaim, reason string, generation int64, at time.Time, onlyFields []string) []ContributionFenceEffect {
	var effects []ContributionFenceEffect
	for _, publication := range s.publications {
		if publication.State != "published" {
			continue
		}
		var fields []string
		for _, attestationRef := range publication.Attestations {
			attestation, ok := s.attestations[attestationRef.ID]
			if !ok || attestation.Claim.ID != claim.Header.ID {
				continue
			}
			for _, field := range attestation.ReleasedFields {
				if len(onlyFields) == 0 || containsSTRIDEString(onlyFields, field.FieldKey) {
					fields = append(fields, field.FieldKey)
				}
			}
		}
		if len(fields) > 0 {
			effects = append(effects, s.fencePublicationLocked(publication, fields, reason, generation, at)...)
		}
	}
	return effects
}

func (s *ContributionAuthorityService) fencePublicationLocked(publication PublishedContributionClaim, fields []string, reason string, generation int64, at time.Time) []ContributionFenceEffect {
	if len(fields) == 0 {
		fields = []string{"all_published_fields"}
	}
	sort.Strings(fields)
	fields = uniqueAuthorityStrings(fields)
	if s.fencedFields[publication.Header.ID] == nil {
		s.fencedFields[publication.Header.ID] = map[string]bool{}
	}
	for _, field := range fields {
		s.fencedFields[publication.Header.ID][field] = true
	}
	fieldsDigest := sha256Hex([]byte(fmt.Sprintf("%q", fields)))
	if generation <= s.purgeGenerations[publication.Header.ID] {
		generation = s.purgeGenerations[publication.Header.ID] + 1
	}
	s.purgeGenerations[publication.Header.ID] = generation
	receiptID := "purge_" + sha256Hex([]byte(publication.Header.ID + fmt.Sprint(generation) + reason))[:24]
	stores := make([]PurgeStoreResult, 0, len(contributionPurgeStores))
	for _, store := range contributionPurgeStores {
		stores = append(stores, PurgeStoreResult{Store: store, State: "queued", AttemptCount: 1})
	}
	receipt := DerivedPurgeReceipt{Header: STRIDEContractHeader{TenantID: STRIDEGlobalPersonTenant, ID: receiptID, Revision: 1, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractDerivedPurgeReceipt, ContentDigest: fieldsDigest, CreatedAt: at}, SubjectPersonID: publication.SubjectPersonID, Trigger: refForHeader(publication.Header), PurgeGeneration: generation, AffectedFieldsDigest: fieldsDigest, Stores: stores, EligibilityFencedAt: at, RecordedAt: at, State: "queued"}
	s.purgeQueue[receiptID] = cloneContract(receipt)
	claimRef := STRIDEReference{}
	if len(publication.Attestations) > 0 {
		if att, ok := s.attestations[publication.Attestations[0].ID]; ok {
			claimRef = att.Claim
		}
	}
	return []ContributionFenceEffect{{Claim: claimRef, Publication: refForHeader(publication.Header), AffectedFields: fields, AffectedFieldsDigest: fieldsDigest, PurgeReceipt: receipt}}
}

func advanceClaimRevision(claim *ContributionClaim, operation string, at time.Time) {
	prior := refForHeader(claim.Header)
	claim.Header = nextAuthorityHeader(claim.Header, operation, at)
	claim.Supersedes = &prior
	claim.StateChangedAt = at
}
func nextAuthorityHeader(header STRIDEContractHeader, operation string, at time.Time) STRIDEContractHeader {
	header.Revision++
	header.CreatedAt = at
	header.ContentDigest = sha256Hex([]byte(header.ID + fmt.Sprint(header.Revision) + operation))
	return header
}
func refForHeader(header STRIDEContractHeader) STRIDEReference {
	return STRIDEReference{ContractType: header.ContractType, ID: header.ID, Revision: header.Revision, Digest: header.ContentDigest}
}
func uniqueAuthorityStrings(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func authorityHistoryKey(id string, revision int64) string { return fmt.Sprintf("%s:%d", id, revision) }

func (s *ContributionAuthorityService) storeClaimLocked(value ContributionClaim) error {
	key := authorityHistoryKey(value.Header.ID, value.Header.Revision)
	if _, exists := s.claimHistory[key]; exists {
		return ErrContributionAuthorityConflict
	}
	stored := cloneContract(value)
	s.claimHistory[key], s.claims[value.Header.ID] = stored, stored
	return nil
}
func (s *ContributionAuthorityService) storeApprovalLocked(value FieldReleaseApproval) error {
	key := authorityHistoryKey(value.Header.ID, value.Header.Revision)
	if _, exists := s.approvalHistory[key]; exists {
		return ErrContributionAuthorityConflict
	}
	stored := cloneContract(value)
	s.approvalHistory[key], s.approvals[value.Header.ID] = stored, stored
	return nil
}
func (s *ContributionAuthorityService) storeAttestationLocked(value ContributionAttestation) error {
	key := authorityHistoryKey(value.Header.ID, value.Header.Revision)
	if _, exists := s.attestationHistory[key]; exists {
		return ErrContributionAuthorityConflict
	}
	stored := cloneContract(value)
	s.attestationHistory[key], s.attestations[value.Header.ID] = stored, stored
	return nil
}
func (s *ContributionAuthorityService) storePublicationLocked(value PublishedContributionClaim) error {
	key := authorityHistoryKey(value.Header.ID, value.Header.Revision)
	if _, exists := s.publicationHistory[key]; exists {
		return ErrContributionAuthorityConflict
	}
	stored := cloneContract(value)
	s.publicationHistory[key], s.publications[value.Header.ID] = stored, stored
	return nil
}

func (s *ContributionAuthorityService) validateAttestationAdmissionLocked(attestation ContributionAttestation) error {
	claim, ok := s.claims[attestation.Claim.ID]
	if !ok || refForHeader(claim.Header) != attestation.Claim || claim.State != "verified" || claim.SubjectPersonID != attestation.SubjectPersonID || claim.EvidenceManifestDigest != attestation.EvidenceManifestDigest {
		return ErrContributionAuthorityConflict
	}
	for _, field := range attestation.ReleasedFields {
		roles := map[string]bool{}
		required, named := map[string]bool{}, map[string]bool{}
		for _, approvalRef := range field.ApprovalRefs {
			approval, ok := s.approvals[approvalRef.ID]
			if !ok || refForHeader(approval.Header) != approvalRef || approval.Attestation != refForHeader(attestation.Header) || approval.State != "approved" || approval.FieldKey != field.FieldKey || approval.FieldValueDigest != field.ValueDigest || approval.SubjectPersonID != attestation.SubjectPersonID || approval.OrganizationID != attestation.OrganizationID {
				return ErrContributionAuthorityDenied
			}
			roles[approval.ApproverRole] = true
			if approval.ApproverRole == "named_party" {
				named[approval.ApproverPartyID] = true
				for _, party := range approval.RequiredPartyIDs {
					required[party] = true
				}
			}
		}
		if !roles["subject"] || !roles["organization"] {
			return ErrContributionAuthorityDenied
		}
		for party := range required {
			if !named[party] {
				return ErrContributionAuthorityDenied
			}
		}
	}
	return nil
}

func (s *ContributionAuthorityService) hasExactGrantLocked(role, organizationID, personID, partyID string, controller STRIDEControllerRevision) bool {
	for _, grant := range s.grants {
		if grant.Role == role && grant.OrganizationID == organizationID && grant.PersonID == personID && grant.PartyID == partyID && grant.Controller == controller {
			return true
		}
	}
	return false
}

func cloneContract[T any](value T) T {
	payload, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned T
	if json.Unmarshal(payload, &cloned) != nil {
		return value
	}
	return cloned
}

func cloneContributionValue(value any) any {
	switch typed := value.(type) {
	case ContributionClaim:
		return cloneContract(typed)
	case FieldReleaseApproval:
		return cloneContract(typed)
	case ContributionAttestation:
		return cloneContract(typed)
	case PublishedContributionClaim:
		return cloneContract(typed)
	case AgentInfluenceReceipt:
		return cloneContract(typed)
	case claimFenceResult:
		return cloneContract(typed)
	case approvalFenceResult:
		return cloneContract(typed)
	case attestationFenceResult:
		return cloneContract(typed)
	case publicationFenceResult:
		return cloneContract(typed)
	case []ContributionFenceEffect:
		return cloneContract(typed)
	default:
		return value
	}
}
