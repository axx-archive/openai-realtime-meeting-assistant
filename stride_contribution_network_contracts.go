package main

// E10-W1 contribution and network contracts are body-minimized authority
// records. They do not contain private source bodies, MyMind/AgentMind data,
// credentials, hidden memberships, or model-authored scores.

import (
	"encoding/json"
	"strings"
	"time"
)

type STRIDEControllerRevision struct {
	PrincipalID       string `json:"principalId"`
	AuthorityID       string `json:"authorityId"`
	AuthorityRevision int64  `json:"authorityRevision"`
	PolicyRevision    int64  `json:"policyRevision"`
}

func (v STRIDEControllerRevision) Validate() error {
	if !strideIdentifier(v.PrincipalID) || !strideIdentifier(v.AuthorityID) || v.AuthorityRevision < 1 || v.PolicyRevision < 1 {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type ContributionClaim struct {
	Header                 STRIDEContractHeader      `json:"header"`
	OrganizationID         string                    `json:"organizationId"`
	SubjectPersonID        string                    `json:"subjectPersonId"`
	ContributionKind       string                    `json:"contributionKind"`
	ProblemClass           string                    `json:"problemClass"`
	OutcomeClass           string                    `json:"outcomeClass"`
	SourceRefs             []STRIDEReference         `json:"sourceRefs"`
	EvidenceManifestDigest string                    `json:"evidenceManifestDigest"`
	AttributionMethod      string                    `json:"attributionMethod"`
	ACLRevision            int64                     `json:"aclRevision"`
	ConsentRevision        int64                     `json:"consentRevision"`
	PurgeGeneration        int64                     `json:"purgeGeneration"`
	PolicyRevision         int64                     `json:"policyRevision"`
	State                  string                    `json:"state"`
	SubjectReview          *STRIDEControllerRevision `json:"subjectReview,omitempty"`
	OrganizationReview     *STRIDEControllerRevision `json:"organizationReview,omitempty"`
	Supersedes             *STRIDEReference          `json:"supersedes,omitempty"`
	StateChangedAt         time.Time                 `json:"stateChangedAt"`
}

func (v ContributionClaim) Validate() error {
	if v.Header.Validate(STRIDEContractContributionClaim) != nil || v.Header.TenantID != v.OrganizationID ||
		!strideIdentifier(v.OrganizationID) || !strideIdentifier(v.SubjectPersonID) ||
		!oneOf(v.ContributionKind, "originated", "shaped", "reviewed", "decided", "delivered") ||
		!strideIdentifier(v.ProblemClass) || !strideIdentifier(v.OutcomeClass) || !validateSTRIDERefs(v.SourceRefs) ||
		!isHexDigest(v.EvidenceManifestDigest) || v.ACLRevision < 1 || v.ConsentRevision < 1 || v.PurgeGeneration < 0 ||
		v.PolicyRevision < 1 || !oneOf(v.AttributionMethod, "source_observed", "subject_submitted", "reviewer_submitted") ||
		!oneOf(v.State, "candidate", "subject_review", "disputed", "verified", "revalidation_required", "revoked", "superseded") || v.StateChangedAt.IsZero() {
		return ErrSTRIDEContractInvalid
	}
	for _, ref := range v.SourceRefs {
		if !allowedContributionEvidenceType(ref.ContractType) {
			return ErrSTRIDEContractInvalid
		}
	}
	if v.SubjectReview != nil && v.SubjectReview.Validate() != nil || v.OrganizationReview != nil && v.OrganizationReview.Validate() != nil ||
		v.Supersedes != nil && (v.Supersedes.Validate() != nil || v.Supersedes.ContractType != STRIDEContractContributionClaim || v.Supersedes.ID != v.Header.ID || v.Supersedes.Revision != v.Header.Revision-1) {
		return ErrSTRIDEContractInvalid
	}
	if (v.State == "verified" || v.State == "revalidation_required" || v.State == "superseded") &&
		(v.SubjectReview == nil || v.OrganizationReview == nil) {
		return ErrSTRIDEContractInvalid
	}
	if v.State == "revoked" && v.OrganizationReview == nil {
		return ErrSTRIDEContractInvalid
	}
	if v.SubjectReview != nil && v.SubjectReview.PrincipalID != v.SubjectPersonID {
		return ErrSTRIDEContractInvalid
	}
	if (v.Header.Revision > 1) != (v.Supersedes != nil) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

func ContributionClaimTransitionAllowed(from, to string) bool {
	switch from {
	case "candidate":
		return oneOf(to, "subject_review", "revoked")
	case "subject_review":
		return oneOf(to, "disputed", "verified", "revoked")
	case "disputed":
		return oneOf(to, "subject_review", "revoked")
	case "verified":
		return oneOf(to, "revalidation_required", "revoked", "superseded")
	case "revalidation_required":
		return oneOf(to, "verified", "revoked", "superseded")
	}
	return false
}

type FieldReleaseApproval struct {
	Header                STRIDEContractHeader     `json:"header"`
	OrganizationID        string                   `json:"organizationId"`
	SubjectPersonID       string                   `json:"subjectPersonId"`
	Attestation           STRIDEReference          `json:"attestation"`
	FieldKey              string                   `json:"fieldKey"`
	FieldValueDigest      string                   `json:"fieldValueDigest"`
	Source                STRIDEReference          `json:"source"`
	SourceConsentRevision int64                    `json:"sourceConsentRevision"`
	SourceACLRevision     int64                    `json:"sourceAclRevision"`
	SourcePurgeGeneration int64                    `json:"sourcePurgeGeneration"`
	Visibility            string                   `json:"visibility"`
	RequiredPartyIDs      []string                 `json:"requiredPartyIds"`
	ApproverRole          string                   `json:"approverRole"`
	ApproverPartyID       string                   `json:"approverPartyId"`
	Controller            STRIDEControllerRevision `json:"controller"`
	State                 string                   `json:"state"`
	ApprovedAt            *time.Time               `json:"approvedAt,omitempty"`
	ExpiresAt             *time.Time               `json:"expiresAt,omitempty"`
	StateChangedAt        time.Time                `json:"stateChangedAt"`
	Supersedes            *STRIDEReference         `json:"supersedes,omitempty"`
}

func (v FieldReleaseApproval) Validate() error {
	if v.Header.Validate(STRIDEContractFieldReleaseApproval) != nil || v.Header.TenantID != v.OrganizationID ||
		!strideIdentifier(v.OrganizationID) || !strideIdentifier(v.SubjectPersonID) || v.Attestation.Validate() != nil ||
		v.Attestation.ContractType != STRIDEContractContributionAttestation || !allowedReleasedField(v.FieldKey) || !isHexDigest(v.FieldValueDigest) ||
		v.Source.Validate() != nil || v.SourceConsentRevision < 1 || v.SourceACLRevision < 1 || v.SourcePurgeGeneration < 0 ||
		!oneOf(v.Visibility, "private", "signed_in_network", "exact_link") || !oneOf(v.ApproverRole, "subject", "organization", "named_party") ||
		v.Controller.Validate() != nil || !oneOf(v.State, "pending", "approved", "denied", "withdrawn", "expired", "superseded") || v.StateChangedAt.IsZero() {
		return ErrSTRIDEContractInvalid
	}
	if v.ApproverRole == "named_party" {
		if !uniqueSTRIDEIDs(v.RequiredPartyIDs) || !strideIdentifier(v.ApproverPartyID) || !containsSTRIDEString(v.RequiredPartyIDs, v.ApproverPartyID) || v.Controller.PrincipalID != v.ApproverPartyID {
			return ErrSTRIDEContractInvalid
		}
	} else if len(v.RequiredPartyIDs) != 0 || v.ApproverPartyID != "" || v.ApproverRole == "subject" && v.Controller.PrincipalID != v.SubjectPersonID {
		return ErrSTRIDEContractInvalid
	}
	if (v.State == "approved") != (v.ApprovedAt != nil) || v.ApprovedAt != nil && v.ApprovedAt.After(v.StateChangedAt) ||
		v.ExpiresAt != nil && (v.ApprovedAt == nil || !v.ExpiresAt.After(*v.ApprovedAt)) {
		return ErrSTRIDEContractInvalid
	}
	if (v.Header.Revision > 1) != (v.Supersedes != nil) || v.Supersedes != nil &&
		(v.Supersedes.Validate() != nil || v.Supersedes.ContractType != STRIDEContractFieldReleaseApproval || v.Supersedes.ID != v.Header.ID || v.Supersedes.Revision != v.Header.Revision-1) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type ReleasedContributionField struct {
	FieldKey     string            `json:"fieldKey"`
	ValueDigest  string            `json:"valueDigest"`
	ApprovalRefs []STRIDEReference `json:"approvalRefs"`
}

func (v ReleasedContributionField) Validate() error {
	if !allowedReleasedField(v.FieldKey) || !isHexDigest(v.ValueDigest) || !validateSTRIDERefs(v.ApprovalRefs) {
		return ErrSTRIDEContractInvalid
	}
	for _, ref := range v.ApprovalRefs {
		if ref.ContractType != STRIDEContractFieldReleaseApproval {
			return ErrSTRIDEContractInvalid
		}
	}
	return nil
}

type ContributionAttestation struct {
	Header                 STRIDEContractHeader        `json:"header"`
	OrganizationID         string                      `json:"organizationId"`
	SubjectPersonID        string                      `json:"subjectPersonId"`
	Claim                  STRIDEReference             `json:"claim"`
	EvidenceManifestDigest string                      `json:"evidenceManifestDigest"`
	ReleasedFieldsDigest   string                      `json:"releasedFieldsDigest"`
	ReleasedFields         []ReleasedContributionField `json:"releasedFields"`
	VerificationTier       string                      `json:"verificationTier"`
	Issuer                 STRIDEControllerRevision    `json:"issuer"`
	SigningKeyID           string                      `json:"signingKeyId"`
	SigningKeyRevision     int64                       `json:"signingKeyRevision"`
	SignatureDigest        string                      `json:"signatureDigest"`
	State                  string                      `json:"state"`
	Supersedes             *STRIDEReference            `json:"supersedes,omitempty"`
	RevokedAt              *time.Time                  `json:"revokedAt,omitempty"`
}

func (v ContributionAttestation) Validate() error {
	if v.Header.Validate(STRIDEContractContributionAttestation) != nil || v.Header.TenantID != v.OrganizationID || !strideIdentifier(v.OrganizationID) ||
		!strideIdentifier(v.SubjectPersonID) || v.Claim.Validate() != nil || v.Claim.ContractType != STRIDEContractContributionClaim ||
		!isHexDigest(v.EvidenceManifestDigest) || !isHexDigest(v.ReleasedFieldsDigest) || !oneOf(v.VerificationTier, "organization_verified_opaque", "organization_verified_redacted") ||
		v.Issuer.Validate() != nil || !strideIdentifier(v.SigningKeyID) || v.SigningKeyRevision < 1 || !isHexDigest(v.SignatureDigest) ||
		!oneOf(v.State, "active", "revoked", "superseded") || (v.State == "active") != (v.RevokedAt == nil) {
		return ErrSTRIDEContractInvalid
	}
	if len(v.ReleasedFields) == 0 || !uniqueReleasedFields(v.ReleasedFields) || v.Supersedes != nil && (v.Supersedes.Validate() != nil || v.Supersedes.ContractType != STRIDEContractContributionAttestation || v.Supersedes.ID != v.Header.ID || v.Supersedes.Revision != v.Header.Revision-1) {
		return ErrSTRIDEContractInvalid
	}
	digest, err := STRIDEContractDigest(v.ReleasedFields)
	if err != nil || digest != v.ReleasedFieldsDigest {
		return ErrSTRIDEContractInvalid
	}
	if (v.Header.Revision > 1) != (v.Supersedes != nil) || v.VerificationTier == "organization_verified_opaque" && hasIdentifyingReleasedField(v.ReleasedFields) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type PublishedContributionClaim struct {
	Header               STRIDEContractHeader     `json:"header"`
	SubjectPersonID      string                   `json:"subjectPersonId"`
	NarrativeDigest      string                   `json:"narrativeDigest"`
	Attestations         []STRIDEReference        `json:"attestations"`
	ReleasedFieldsDigest string                   `json:"releasedFieldsDigest"`
	Visibility           string                   `json:"visibility"`
	Controller           STRIDEControllerRevision `json:"controller"`
	State                string                   `json:"state"`
	StateChangedAt       time.Time                `json:"stateChangedAt"`
	Supersedes           *STRIDEReference         `json:"supersedes,omitempty"`
}

func (v PublishedContributionClaim) Validate() error {
	if v.Header.Validate(STRIDEContractPublishedContributionClaim) != nil || v.Header.TenantID != STRIDEGlobalPersonTenant ||
		!strideIdentifier(v.SubjectPersonID) || !isHexDigest(v.NarrativeDigest) || !validateSTRIDERefs(v.Attestations) || !isHexDigest(v.ReleasedFieldsDigest) ||
		!oneOf(v.Visibility, "private", "signed_in_network", "exact_link") || v.Controller.Validate() != nil || v.Controller.PrincipalID != v.SubjectPersonID ||
		!oneOf(v.State, "draft", "approval_required", "published", "superseded", "withdrawn") || v.StateChangedAt.IsZero() {
		return ErrSTRIDEContractInvalid
	}
	for _, ref := range v.Attestations {
		if ref.ContractType != STRIDEContractContributionAttestation {
			return ErrSTRIDEContractInvalid
		}
	}
	if v.State != "published" && v.Visibility != "private" {
		return ErrSTRIDEContractInvalid
	}
	if (v.Header.Revision > 1) != (v.Supersedes != nil) || v.Supersedes != nil &&
		(v.Supersedes.Validate() != nil || v.Supersedes.ContractType != STRIDEContractPublishedContributionClaim || v.Supersedes.ID != v.Header.ID || v.Supersedes.Revision != v.Header.Revision-1) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

func PublishedContributionTransitionAllowed(from, to string) bool {
	switch from {
	case "draft":
		return to == "approval_required"
	case "approval_required":
		return oneOf(to, "draft", "published", "withdrawn")
	case "published":
		return oneOf(to, "withdrawn", "superseded")
	}
	return false
}

type AgentInfluenceReceipt struct {
	Header           STRIDEContractHeader     `json:"header"`
	OrganizationID   string                   `json:"organizationId"`
	SubjectPersonID  string                   `json:"subjectPersonId"`
	AgentProfile     STRIDEReference          `json:"agentProfile"`
	RuntimeRevision  STRIDEReference          `json:"runtimeRevision"`
	ModelRevision    STRIDEReference          `json:"modelRevision"`
	AgentRun         STRIDEReference          `json:"agentRun"`
	AgentOutput      STRIDEReference          `json:"agentOutput"`
	HumanInteraction STRIDEReference          `json:"humanInteraction"`
	HumanAdoption    STRIDEReference          `json:"humanAdoption"`
	Outcome          STRIDEReference          `json:"outcome"`
	Reviewer         STRIDEControllerRevision `json:"reviewer"`
	State            string                   `json:"state"`
}

func (v AgentInfluenceReceipt) Validate() error {
	refs := []STRIDEReference{v.AgentProfile, v.RuntimeRevision, v.ModelRevision, v.AgentRun, v.AgentOutput, v.HumanInteraction, v.HumanAdoption, v.Outcome}
	if v.Header.Validate(STRIDEContractAgentInfluenceReceipt) != nil || v.Header.TenantID != v.OrganizationID || !strideIdentifier(v.OrganizationID) ||
		!strideIdentifier(v.SubjectPersonID) || !validateSTRIDERefs(refs) || v.AgentProfile.ContractType != STRIDEContractAgentCoreProfile ||
		v.RuntimeRevision.ContractType != STRIDEContractAgentCapabilityManifest || v.ModelRevision.ContractType != STRIDEContractKnowledgeAssertion ||
		v.AgentRun.ContractType != STRIDEContractWorkRun || v.AgentOutput.ContractType != STRIDEContractOutcome || v.HumanInteraction.ContractType != STRIDEContractConversationEvent ||
		v.HumanAdoption.ContractType != STRIDEContractKnowledgeAssertion || v.Outcome.ContractType != STRIDEContractOutcome ||
		v.Reviewer.Validate() != nil || !oneOf(v.State, "verified", "revalidation_required", "revoked", "superseded") {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type NetworkPublishedField struct {
	FieldKey      string           `json:"fieldKey"`
	ValueDigest   string           `json:"valueDigest"`
	VisibleValue  json.RawMessage  `json:"visibleValue,omitempty"`
	EvidenceLabel string           `json:"evidenceLabel"`
	Claim         *STRIDEReference `json:"claim,omitempty"`
}

func (v NetworkPublishedField) Validate() error {
	if !allowedNetworkField(v.FieldKey) || !isHexDigest(v.ValueDigest) || !oneOf(v.EvidenceLabel, "self_described", "organization_verified_opaque", "organization_verified_redacted", "public_source_verified") {
		return ErrSTRIDEContractInvalid
	}
	if len(v.VisibleValue) > 0 {
		if sha256Hex(v.VisibleValue) != v.ValueDigest || !validNetworkVisibleValue(v.FieldKey, v.VisibleValue) {
			return ErrSTRIDEContractInvalid
		}
	}
	if v.EvidenceLabel == "self_described" {
		if v.Claim != nil {
			return ErrSTRIDEContractInvalid
		}
	} else if v.Claim == nil || v.Claim.Validate() != nil || v.Claim.ContractType != STRIDEContractPublishedContributionClaim {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

func validNetworkVisibleValue(fieldKey string, raw json.RawMessage) bool {
	var value any
	if len(raw) > 1024 || json.Unmarshal(raw, &value) != nil {
		return false
	}
	if oneOf(fieldKey, "work_mode", "open_to") {
		items, ok := value.([]any)
		if !ok || len(items) > 20 {
			return false
		}
		seen := map[string]bool{}
		for _, item := range items {
			text, ok := item.(string)
			if !ok || strings.TrimSpace(text) == "" || len(text) > 64 || seen[text] {
				return false
			}
			seen[text] = true
		}
		return true
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return false
	}
	limits := map[string]int{"display_name": 80, "pronouns": 40, "bio": 280, "problem_class": 160, "outcome_class": 160, "contribution_role": 160, "coarse_date": 32, "issuer": 160, "artifact": 160, "outcome": 160, "avatar": 500}
	limit := limits[fieldKey]
	return limit > 0 && len(text) <= limit
}

type NetworkProfileProjection struct {
	Header          STRIDEContractHeader     `json:"header"`
	SubjectPersonID string                   `json:"subjectPersonId"`
	Publication     STRIDEReference          `json:"publication"`
	Fields          []NetworkPublishedField  `json:"fields"`
	FieldsDigest    string                   `json:"fieldsDigest"`
	Discoverability string                   `json:"discoverability"`
	PurgeGeneration int64                    `json:"purgeGeneration"`
	Controller      STRIDEControllerRevision `json:"controller"`
	State           string                   `json:"state"`
	StateChangedAt  time.Time                `json:"stateChangedAt"`
}

func (v NetworkProfileProjection) Validate() error {
	if v.Header.Validate(STRIDEContractNetworkProfileProjection) != nil || v.Header.TenantID != STRIDEGlobalPersonTenant || !strideIdentifier(v.SubjectPersonID) ||
		v.Publication.Validate() != nil || v.Publication.ContractType != STRIDEContractPublishedContributionClaim || len(v.Fields) == 0 || !uniqueNetworkFields(v.Fields) ||
		!isHexDigest(v.FieldsDigest) || !oneOf(v.Discoverability, "unlisted", "signed_in_network", "exact_link") || v.PurgeGeneration < 0 ||
		v.Controller.Validate() != nil || v.Controller.PrincipalID != v.SubjectPersonID || !oneOf(v.State, "draft", "published", "paused", "off", "deleted") || v.StateChangedAt.IsZero() {
		return ErrSTRIDEContractInvalid
	}
	digest, err := STRIDEContractDigest(v.Fields)
	if err != nil || digest != v.FieldsDigest {
		return ErrSTRIDEContractInvalid
	}
	if v.State != "published" && v.Discoverability != "unlisted" {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type TalentSearchGrant struct {
	Header                  STRIDEContractHeader     `json:"header"`
	OrganizationID          string                   `json:"organizationId"`
	MembershipID            string                   `json:"membershipId"`
	MembershipRevision      int64                    `json:"membershipRevision"`
	SearcherPersonID        string                   `json:"searcherPersonId"`
	CapabilityAdministrator STRIDEControllerRevision `json:"capabilityAdministrator"`
	PolicyRevision          int64                    `json:"policyRevision"`
	State                   string                   `json:"state"`
	GrantedAt               time.Time                `json:"grantedAt"`
	ExpiresAt               time.Time                `json:"expiresAt"`
	RevokedAt               *time.Time               `json:"revokedAt,omitempty"`
}

func (v TalentSearchGrant) Validate() error {
	if v.Header.Validate(STRIDEContractTalentSearchGrant) != nil || v.Header.TenantID != v.OrganizationID || !strideIdentifier(v.OrganizationID) ||
		!strideIdentifier(v.MembershipID) || v.MembershipRevision < 1 || !strideIdentifier(v.SearcherPersonID) || v.CapabilityAdministrator.Validate() != nil ||
		v.PolicyRevision < 1 || !oneOf(v.State, "active", "revoked", "expired") || v.GrantedAt.IsZero() || !v.ExpiresAt.After(v.GrantedAt) ||
		(v.State == "active") != (v.RevokedAt == nil) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type NetworkSearchFilter struct {
	Field        string `json:"field"`
	Operation    string `json:"operation"`
	VisibleValue string `json:"visibleValue"`
	ValueDigest  string `json:"valueDigest"`
}

func (v NetworkSearchFilter) Validate() error {
	return combineContract(nil, !oneOf(v.Field, "problem_class", "outcome_class", "contribution_role", "work_mode", "verification_label", "freshness_bucket") ||
		!oneOf(v.Operation, "equals", "contains", "any_of") || strings.TrimSpace(v.VisibleValue) == "" || len(v.VisibleValue) > 160 ||
		containsProhibitedSearchTerm(v.VisibleValue) || !isHexDigest(v.ValueDigest) || sha256Hex([]byte(v.VisibleValue)) != v.ValueDigest)
}

type NetworkSearchResultReason struct {
	Projection STRIDEReference `json:"projection"`
	Why        []string        `json:"why"`
	Unknown    []string        `json:"unknown"`
}

func (v NetworkSearchResultReason) Validate() error {
	if v.Projection.Validate() != nil || v.Projection.ContractType != STRIDEContractNetworkProfileProjection || !validVisibleReasonList(v.Why) || !validVisibleReasonList(v.Unknown) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type NetworkSearchReceipt struct {
	Header                  STRIDEContractHeader        `json:"header"`
	OrganizationID          string                      `json:"organizationId"`
	Grant                   STRIDEReference             `json:"grant"`
	OriginalQueryDigest     string                      `json:"originalQueryDigest"`
	PolicyRevision          int64                       `json:"policyRevision"`
	PolicyVerdict           string                      `json:"policyVerdict"`
	PolicyReasonCodes       []string                    `json:"policyReasonCodes"`
	StructuredFilters       []NetworkSearchFilter       `json:"structuredFilters"`
	InterpretationConfirmed bool                        `json:"interpretationConfirmed"`
	Ordering                []string                    `json:"ordering"`
	Results                 []NetworkSearchResultReason `json:"results"`
	RouteRevision           *STRIDEReference            `json:"routeRevision,omitempty"`
	CostMicrounits          int64                       `json:"costMicrounits"`
	SearchedAt              time.Time                   `json:"searchedAt"`
}

func (v NetworkSearchReceipt) Validate() error {
	if v.Header.Validate(STRIDEContractNetworkSearchReceipt) != nil || v.Header.TenantID != v.OrganizationID || !strideIdentifier(v.OrganizationID) ||
		v.Grant.Validate() != nil || v.Grant.ContractType != STRIDEContractTalentSearchGrant || !isHexDigest(v.OriginalQueryDigest) || v.PolicyRevision < 1 ||
		!oneOf(v.PolicyVerdict, "allow", "transform_with_confirmation", "abstain", "reject") || !uniqueSTRIDEIDs(v.PolicyReasonCodes) ||
		!validSearchFilters(v.StructuredFilters) || !validOrdering(v.Ordering) || !validSearchResults(v.Results) || v.CostMicrounits < 0 || v.SearchedAt.IsZero() ||
		v.RouteRevision != nil && v.RouteRevision.Validate() != nil {
		return ErrSTRIDEContractInvalid
	}
	if oneOf(v.PolicyVerdict, "abstain", "reject") && (len(v.StructuredFilters) != 0 || len(v.Results) != 0 || v.RouteRevision != nil || v.CostMicrounits != 0) {
		return ErrSTRIDEContractInvalid
	}
	if v.PolicyVerdict == "transform_with_confirmation" && !v.InterpretationConfirmed {
		return ErrSTRIDEContractInvalid
	}
	if v.PolicyVerdict == "allow" && len(v.StructuredFilters) == 0 {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type ContactRequest struct {
	Header                STRIDEContractHeader      `json:"header"`
	SenderOrganizationID  string                    `json:"senderOrganizationId"`
	SenderPersonID        string                    `json:"senderPersonId"`
	RecipientPersonID     string                    `json:"recipientPersonId"`
	RecipientProjection   STRIDEReference           `json:"recipientProjection"`
	Purpose               string                    `json:"purpose"`
	NoteDigest            string                    `json:"noteDigest"`
	CollaborationType     string                    `json:"collaborationType"`
	AcceptedChannelDigest string                    `json:"acceptedChannelDigest,omitempty"`
	RecipientController   *STRIDEControllerRevision `json:"recipientController,omitempty"`
	State                 string                    `json:"state"`
	ExpiresAt             time.Time                 `json:"expiresAt"`
	StateChangedAt        time.Time                 `json:"stateChangedAt"`
}

func (v ContactRequest) Validate() error {
	if v.Header.Validate(STRIDEContractContactRequest) != nil || v.Header.TenantID != v.SenderOrganizationID || !strideIdentifier(v.SenderOrganizationID) ||
		!strideIdentifier(v.SenderPersonID) || !strideIdentifier(v.RecipientPersonID) || v.SenderPersonID == v.RecipientPersonID ||
		v.RecipientProjection.Validate() != nil || v.RecipientProjection.ContractType != STRIDEContractNetworkProfileProjection || !strideIdentifier(v.Purpose) ||
		!isHexDigest(v.NoteDigest) || !oneOf(v.CollaborationType, "collaboration", "advisory", "employment", "recruiting", "organization_join") ||
		!oneOf(v.State, "pending", "accepted", "declined", "withdrawn", "expired") || v.ExpiresAt.IsZero() || v.StateChangedAt.IsZero() || !v.ExpiresAt.After(v.Header.CreatedAt) {
		return ErrSTRIDEContractInvalid
	}
	if v.State == "accepted" {
		if !isHexDigest(v.AcceptedChannelDigest) || v.RecipientController == nil || v.RecipientController.Validate() != nil || v.RecipientController.PrincipalID != v.RecipientPersonID {
			return ErrSTRIDEContractInvalid
		}
	} else if v.AcceptedChannelDigest != "" || v.RecipientController != nil {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type NetworkBlock struct {
	Header                STRIDEContractHeader     `json:"header"`
	BlockerPersonID       string                   `json:"blockerPersonId"`
	BlockedPersonID       string                   `json:"blockedPersonId,omitempty"`
	BlockedOrganizationID string                   `json:"blockedOrganizationId,omitempty"`
	Controller            STRIDEControllerRevision `json:"controller"`
	State                 string                   `json:"state"`
	StateChangedAt        time.Time                `json:"stateChangedAt"`
}

func (v NetworkBlock) Validate() error {
	if v.Header.Validate(STRIDEContractNetworkBlock) != nil || v.Header.TenantID != STRIDEGlobalPersonTenant || !strideIdentifier(v.BlockerPersonID) ||
		(v.BlockedPersonID == "") == (v.BlockedOrganizationID == "") || !validOptionalSTRIDEID(v.BlockedPersonID) || !validOptionalSTRIDEID(v.BlockedOrganizationID) ||
		v.BlockedPersonID == v.BlockerPersonID || v.Controller.Validate() != nil || v.Controller.PrincipalID != v.BlockerPersonID || !oneOf(v.State, "active", "withdrawn") || v.StateChangedAt.IsZero() {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type PurgeStoreResult struct {
	Store        string     `json:"store"`
	State        string     `json:"state"`
	AttemptCount int        `json:"attemptCount"`
	CompletedAt  *time.Time `json:"completedAt,omitempty"`
}

var contributionPurgeStores = []string{"projection", "lexical_index", "vector_index", "reranker_cache", "application_cache", "cdn", "push_queue", "job_queue", "analytics", "audit_log", "test_fixture", "export", "backup_manifest"}

func (v PurgeStoreResult) Validate() error {
	if !oneOf(v.Store, "projection", "lexical_index", "vector_index", "reranker_cache", "application_cache", "cdn", "push_queue", "job_queue", "analytics", "audit_log", "test_fixture", "export", "backup_manifest") ||
		!oneOf(v.State, "queued", "completed", "failed_escalated") || v.AttemptCount < 1 || (v.State == "completed") != (v.CompletedAt != nil) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type DerivedPurgeReceipt struct {
	Header               STRIDEContractHeader `json:"header"`
	SubjectPersonID      string               `json:"subjectPersonId"`
	Trigger              STRIDEReference      `json:"trigger"`
	PurgeGeneration      int64                `json:"purgeGeneration"`
	AffectedFieldsDigest string               `json:"affectedFieldsDigest"`
	Stores               []PurgeStoreResult   `json:"stores"`
	EligibilityFencedAt  time.Time            `json:"eligibilityFencedAt"`
	RecordedAt           time.Time            `json:"recordedAt"`
	State                string               `json:"state"`
}

func (v DerivedPurgeReceipt) Validate() error {
	if v.Header.Validate(STRIDEContractDerivedPurgeReceipt) != nil || !strideIdentifier(v.SubjectPersonID) || v.Trigger.Validate() != nil || v.PurgeGeneration < 1 ||
		!isHexDigest(v.AffectedFieldsDigest) || len(v.Stores) == 0 || v.EligibilityFencedAt.IsZero() || v.RecordedAt.Before(v.EligibilityFencedAt) ||
		!oneOf(v.State, "queued", "completed", "failed_escalated") {
		return ErrSTRIDEContractInvalid
	}
	seen := map[string]bool{}
	allComplete := true
	anyFailed := false
	for _, store := range v.Stores {
		if store.Validate() != nil || seen[store.Store] {
			return ErrSTRIDEContractInvalid
		}
		seen[store.Store] = true
		allComplete = allComplete && store.State == "completed"
		anyFailed = anyFailed || store.State == "failed_escalated"
	}
	for _, requiredStore := range contributionPurgeStores {
		if !seen[requiredStore] {
			return ErrSTRIDEContractInvalid
		}
	}
	if len(seen) != len(contributionPurgeStores) {
		return ErrSTRIDEContractInvalid
	}
	if v.State == "completed" && !allComplete || v.State == "failed_escalated" && !anyFailed {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

func allowedReleasedField(value string) bool {
	return oneOf(value, "category", "contribution_role", "coarse_date", "issuer", "customer", "collaborator", "project", "artifact", "excerpt", "metric", "outcome")
}

func allowedContributionEvidenceType(value STRIDEContractType) bool {
	return oneOf(string(value), string(STRIDEContractConversationEvent), string(STRIDEContractTranscriptRevision), string(STRIDEContractAnalysisProjection),
		string(STRIDEContractKnowledgeAssertion), string(STRIDEContractWorkRun), string(STRIDEContractOutcome), string(STRIDEContractRichMessagePart),
		string(STRIDEContractMeetingAgentContribution), string(STRIDEContractArtifactDisposition))
}

func allowedNetworkField(value string) bool {
	return oneOf(value, "display_name", "avatar", "pronouns", "bio", "work_mode", "open_to", "problem_class", "outcome_class", "contribution_role", "coarse_date", "issuer", "artifact", "outcome")
}

func uniqueReleasedFields(values []ReleasedContributionField) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if value.Validate() != nil || seen[value.FieldKey] {
			return false
		}
		seen[value.FieldKey] = true
	}
	return true
}

func uniqueNetworkFields(values []NetworkPublishedField) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if value.Validate() != nil || seen[value.FieldKey] {
			return false
		}
		seen[value.FieldKey] = true
	}
	return true
}

func hasIdentifyingReleasedField(values []ReleasedContributionField) bool {
	for _, value := range values {
		if oneOf(value.FieldKey, "customer", "collaborator", "project", "artifact", "excerpt", "metric", "outcome") {
			return true
		}
	}
	return false
}

func containsSTRIDEString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsProhibitedSearchTerm(value string) bool {
	normalized := strings.ToLower(value)
	for _, forbidden := range []string{"race", "ethnic", "relig", "politic", "pregnan", "family", "disab", "health", "medical", "gender", "sexual", "citizen", "national origin", "graduation year", "salary history", "compensation history", "culture fit", "personality", "loyal", "promotion", "termination", "productivity", "message volume", "meeting volume", "response time", "token", "followers", "prestige"} {
		if strings.Contains(normalized, forbidden) {
			return true
		}
	}
	return false
}

func validVisibleReasonList(values []string) bool {
	if len(values) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 240 || containsProhibitedSearchTerm(value) || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func validSearchFilters(values []NetworkSearchFilter) bool {
	for _, value := range values {
		if value.Validate() != nil {
			return false
		}
	}
	return true
}
func validSearchResults(values []NetworkSearchResultReason) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if value.Validate() != nil || seen[value.Projection.ID] {
			return false
		}
		seen[value.Projection.ID] = true
	}
	return true
}
func validOrdering(values []string) bool {
	if len(values) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !oneOf(value, "declared_query_match", "evidence_coverage", "freshness_bucket", "privacy_shuffle") || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}
