package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type w6TestShadowHealth struct {
	healthy   bool
	beforeUse func()
	results   []STRIDENetworkShadowSearchResult
	authority *NetworkAuthority
}

func (v *w6TestShadowHealth) WithHealthyCurrentW6Shadow(_ context.Context, expectation W6ShadowHealthExpectation, use func(W6ShadowHealthSnapshot) error) error {
	if !v.healthy {
		return ErrSTRIDENetworkShadowAuthority
	}
	if v.beforeUse != nil {
		v.beforeUse()
	}
	return use(W6ShadowHealthSnapshot{OrganizationID: expectation.OrganizationID, PolicyRevision: expectation.PolicyRevision, Generation: 1, SnapshotRevision: 1, IndexedRevision: 1, PurgeWorkerHealthy: true})
}

func (v *w6TestShadowHealth) Search(STRIDENetworkShadowSearchRequest) ([]STRIDENetworkShadowSearchResult, error) {
	if !v.healthy {
		return nil, ErrSTRIDENetworkShadowAuthority
	}
	return cloneContract(v.results), nil
}

func (v *w6TestShadowHealth) WithCurrentSearchAdmission(_ context.Context, request STRIDENetworkShadowSearchRequest, use func([]STRIDENetworkShadowSearchResult) error) error {
	if !v.healthy || !validStrideE10SessionHash(request.SessionHash) || !strideIdentifier(request.ActiveOrganizationSessionID) {
		return ErrSTRIDENetworkShadowAuthority
	}
	if v.beforeUse != nil {
		v.beforeUse()
	}
	if v.authority != nil {
		v.authority.mu.Lock()
		defer v.authority.mu.Unlock()
	}
	return use(cloneContract(v.results))
}

func (v *w6TestShadowHealth) WithCurrentSearchDisclosures(_ context.Context, request STRIDENetworkShadowDisclosureRequest, use func([]STRIDENetworkShadowSearchResult) error) error {
	if !v.healthy || len(request.Results) == 0 {
		return ErrSTRIDENetworkShadowAuthority
	}
	if v.authority != nil {
		v.authority.mu.Lock()
		defer v.authority.mu.Unlock()
		for _, result := range request.Results {
			for _, field := range result.Fields {
				if field.Claim == nil {
					continue
				}
				publication, ok := v.authority.publications[field.Claim.ID]
				if !ok || referenceFromHeader(publication.Header) != *field.Claim || publication.State != "published" {
					return ErrSTRIDENetworkShadowAuthority
				}
			}
		}
	}
	return use(cloneContract(request.Results))
}

func (v *w6TestShadowHealth) WithCurrentContactAuthority(_ context.Context, expectation STRIDENetworkShadowContactAuthorityExpectation, use func() error) error {
	if !v.healthy || !validStrideE10SessionHash(expectation.SessionHash) || !strideIdentifier(expectation.ActiveOrganizationSessionID) {
		return ErrSTRIDENetworkShadowAuthority
	}
	if v.beforeUse != nil {
		v.beforeUse()
	}
	if v.authority != nil {
		v.authority.mu.Lock()
		defer v.authority.mu.Unlock()
	}
	return use()
}

func (v *w6TestShadowHealth) WithCurrentExactLinkProjection(reference STRIDEReference, use func(STRIDENetworkShadowSearchResult) error) error {
	for _, result := range v.results {
		if result.Projection == reference {
			return use(cloneContract(result))
		}
	}
	return ErrSTRIDENetworkShadowAuthority
}

func w6TestShadowForFixture(f networkAuthorityFixture) *w6TestShadowHealth {
	return &w6TestShadowHealth{healthy: true, authority: f.service, results: []STRIDENetworkShadowSearchResult{{Projection: referenceFromHeader(f.profile.Header), Fields: networkVisiblePublishedFields(f.profile.Fields)}}}
}

type networkAuthorityFixture struct {
	service             *NetworkAuthority
	now                 time.Time
	personController    STRIDEControllerRevision
	capabilityAuthority TalentSearchCapabilityAuthority
	capabilityAssertion TalentSearchCapabilityAssertion
	profile             NetworkProfileProjection
	grant               TalentSearchGrant
	publication         PublishedContributionClaim
	attestation         ContributionAttestation
	claim               ContributionClaim
	approval            FieldReleaseApproval
}

func assertExactNetworkPurgeReceipt(t *testing.T, receipt *DerivedPurgeReceipt) {
	t.Helper()
	if receipt == nil || receipt.Validate() != nil || len(receipt.Stores) != len(contributionPurgeStores) {
		t.Fatalf("invalid full-store network purge receipt: %+v", receipt)
	}
	for index, store := range contributionPurgeStores {
		if receipt.Stores[index].Store != store || receipt.Stores[index].State != "queued" {
			t.Fatalf("network purge store %d=%+v want=%q queued", index, receipt.Stores[index], store)
		}
	}
}

func newNetworkAuthorityFixture(t *testing.T) networkAuthorityFixture {
	t.Helper()
	now := time.Date(2026, 8, 8, 18, 0, 0, 0, time.UTC)
	service := NewNetworkAuthority(func() time.Time { return now })
	personController := STRIDEControllerRevision{PrincipalID: "person_candidate", AuthorityID: "person_profile_control", AuthorityRevision: 1, PolicyRevision: 1}
	claim := strideTestRef(STRIDEContractPublishedContributionClaim, "published_claim_growth")
	released := []ReleasedContributionField{{FieldKey: "category", ValueDigest: sha256Hex([]byte(`"growth"`)), ApprovalRefs: []STRIDEReference{strideTestRef(STRIDEContractFieldReleaseApproval, "approval_growth")}}}
	releasedDigest, err := STRIDEContractDigest(released)
	if err != nil {
		t.Fatal(err)
	}
	attestation := ContributionAttestation{
		Header:         STRIDEContractHeader{TenantID: "org_evidence", ID: "attestation_growth", Revision: 1, SchemaVersion: 1, ContractType: STRIDEContractContributionAttestation, ContentDigest: strideTestDigest("a"), CreatedAt: now},
		OrganizationID: "org_evidence", SubjectPersonID: "person_candidate", Claim: strideTestRef(STRIDEContractContributionClaim, "claim_growth"), EvidenceManifestDigest: strideTestDigest("b"), ReleasedFieldsDigest: releasedDigest, ReleasedFields: released,
		VerificationTier: "organization_verified_opaque", Issuer: STRIDEControllerRevision{PrincipalID: "person_evidence_admin", AuthorityID: "evidence_authority", AuthorityRevision: 1, PolicyRevision: 1}, SigningKeyID: "signing_key", SigningKeyRevision: 1, SignatureDigest: strideTestDigest("c"), State: "active",
	}
	claimAuthority := ContributionClaim{Header: STRIDEContractHeader{TenantID: "org_evidence", ID: attestation.Claim.ID, Revision: attestation.Claim.Revision, SchemaVersion: 1, ContractType: STRIDEContractContributionClaim, ContentDigest: attestation.Claim.Digest, CreatedAt: now}, OrganizationID: "org_evidence", SubjectPersonID: "person_candidate", ContributionKind: "delivered", ProblemClass: "growth", OutcomeClass: "retention", SourceRefs: []STRIDEReference{strideTestRef(STRIDEContractOutcome, "source_growth")}, EvidenceManifestDigest: strideTestDigest("d"), AttributionMethod: "source_observed", ACLRevision: 1, ConsentRevision: 1, PurgeGeneration: 1, PolicyRevision: 1, State: "verified", SubjectReview: &personController, OrganizationReview: &attestation.Issuer, StateChangedAt: now}
	approvedAt := now
	approval := FieldReleaseApproval{Header: STRIDEContractHeader{TenantID: "org_evidence", ID: released[0].ApprovalRefs[0].ID, Revision: released[0].ApprovalRefs[0].Revision, SchemaVersion: 1, ContractType: STRIDEContractFieldReleaseApproval, ContentDigest: released[0].ApprovalRefs[0].Digest, CreatedAt: now}, OrganizationID: "org_evidence", SubjectPersonID: "person_candidate", Attestation: referenceFromHeader(attestation.Header), FieldKey: released[0].FieldKey, FieldValueDigest: released[0].ValueDigest, Source: strideTestRef(STRIDEContractOutcome, "source_growth"), SourceConsentRevision: 1, SourceACLRevision: 1, SourcePurgeGeneration: 1, Visibility: "signed_in_network", ApproverRole: "organization", Controller: attestation.Issuer, State: "approved", ApprovedAt: &approvedAt, StateChangedAt: now}
	publication := PublishedContributionClaim{
		Header:          STRIDEContractHeader{TenantID: STRIDEGlobalPersonTenant, ID: claim.ID, Revision: claim.Revision, SchemaVersion: 1, ContractType: STRIDEContractPublishedContributionClaim, ContentDigest: claim.Digest, CreatedAt: now},
		SubjectPersonID: "person_candidate", NarrativeDigest: strideTestDigest("d"), Attestations: []STRIDEReference{referenceFromHeader(attestation.Header)}, ReleasedFieldsDigest: releasedDigest, Visibility: "signed_in_network", Controller: personController, State: "published", StateChangedAt: now,
	}
	if err := service.InstallClaimAuthority(claimAuthority); err != nil {
		t.Fatalf("install claim: %v", err)
	}
	if err := service.InstallFieldApprovalAuthority(approval); err != nil {
		t.Fatalf("install approval: %v", err)
	}
	if err := service.InstallAttestationAuthority(attestation); err != nil {
		t.Fatalf("install attestation: %v", err)
	}
	if err := service.InstallPublicationAuthority(publication, []ContributionAttestation{attestation}); err != nil {
		t.Fatalf("install publication: %v", err)
	}
	fields := []NetworkPublishedField{
		{FieldKey: "display_name", ValueDigest: sha256Hex([]byte(`"Candidate"`)), VisibleValue: json.RawMessage(`"Candidate"`), EvidenceLabel: "self_described"},
		{FieldKey: "problem_class", ValueDigest: sha256Hex([]byte(`"growth"`)), VisibleValue: json.RawMessage(`"growth"`), EvidenceLabel: "organization_verified_opaque", Claim: &claim},
		{FieldKey: "work_mode", ValueDigest: sha256Hex([]byte(`["async"]`)), VisibleValue: json.RawMessage(`["async"]`), EvidenceLabel: "self_described"},
	}
	fieldsDigest, err := STRIDEContractDigest(fields)
	if err != nil {
		t.Fatal(err)
	}
	profile := NetworkProfileProjection{
		Header:          STRIDEContractHeader{TenantID: STRIDEGlobalPersonTenant, ID: "network_profile_candidate", Revision: 1, SchemaVersion: 1, ContractType: STRIDEContractNetworkProfileProjection, ContentDigest: strideTestDigest("3"), CreatedAt: now},
		SubjectPersonID: "person_candidate", Publication: claim, Fields: fields, FieldsDigest: fieldsDigest, Discoverability: "unlisted",
		Controller: personController, State: "draft", StateChangedAt: now,
	}
	if _, _, _, err := service.PutProfile(personController, profile, 0, strideTestDigest("4")); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	profile.Header.Revision = 2
	profile.Header.ContentDigest = strideTestDigest("5")
	profile.Header.CreatedAt = now.Add(time.Minute)
	profile.State = "published"
	profile.Discoverability = "signed_in_network"
	profile.StateChangedAt = now.Add(time.Minute)
	if _, _, _, err := service.PutProfile(personController, profile, 1, strideTestDigest("6")); err != nil {
		t.Fatalf("publish profile: %v", err)
	}

	authority := TalentSearchCapabilityAuthority{ID: "talent_capability_admin", Revision: 1, OrganizationID: "org_recruiter", ControllerPersonID: "person_privacy_admin", MembershipID: "membership_privacy_admin", MembershipRevision: 3, PolicyRevision: 1, Active: true}
	if err := service.InstallTalentSearchCapabilityAuthority(authority); err != nil {
		t.Fatal(err)
	}
	assertion := TalentSearchCapabilityAssertion{AuthorityID: authority.ID, AuthorityRevision: authority.Revision, ControllerPersonID: authority.ControllerPersonID}
	admin := STRIDEControllerRevision{PrincipalID: authority.ControllerPersonID, AuthorityID: authority.ID, AuthorityRevision: authority.Revision, PolicyRevision: authority.PolicyRevision}
	grant := TalentSearchGrant{
		Header:         STRIDEContractHeader{TenantID: "org_recruiter", ID: "talent_grant_recruiter", Revision: 1, SchemaVersion: 1, ContractType: STRIDEContractTalentSearchGrant, ContentDigest: strideTestDigest("7"), CreatedAt: now},
		OrganizationID: "org_recruiter", MembershipID: "membership_recruiter", MembershipRevision: 2, SearcherPersonID: "person_recruiter",
		CapabilityAdministrator: admin, PolicyRevision: 1, State: "active", GrantedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}
	if err := service.InstallMembershipAuthority(NetworkMembershipAuthority{MembershipID: grant.MembershipID, OrganizationID: grant.OrganizationID, PersonID: grant.SearcherPersonID, Revision: grant.MembershipRevision, Active: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := service.PutTalentSearchGrant(assertion, grant, 0, strideTestDigest("8")); err != nil {
		t.Fatalf("create talent grant: %v", err)
	}
	return networkAuthorityFixture{service: service, now: now, personController: personController, capabilityAuthority: authority, capabilityAssertion: assertion, profile: profile, grant: grant, publication: publication, attestation: attestation, claim: claimAuthority, approval: approval}
}

func (f networkAuthorityFixture) searchRequest(seed, query string, filters ...NetworkSearchFilter) NetworkSearchRequest {
	return NetworkSearchRequest{
		GrantRef: referenceFromHeader(f.grant.Header), SearcherPersonID: f.grant.SearcherPersonID, OrganizationID: f.grant.OrganizationID,
		MembershipID: f.grant.MembershipID, MembershipRevision: f.grant.MembershipRevision, SessionHash: sha256Hex([]byte("current recruiter session")), ActiveSessionID: "active_session_recruiter", HumanQuery: query,
		OriginalQueryDigest: sha256Hex([]byte(query)), StructuredFilters: filters, InterpretationConfirmed: true, Limit: 10,
		IdempotencyKeyDigest: sha256Hex([]byte("search-" + seed)), At: f.now.Add(2 * time.Minute),
	}
}

func networkFilter(field, value string) NetworkSearchFilter {
	return NetworkSearchFilter{Field: field, Operation: "equals", VisibleValue: value, ValueDigest: sha256Hex([]byte(value))}
}

func TestNetworkAuthorityPublishedOnlyPolicyAndIdempotency(t *testing.T) {
	fixture := newNetworkAuthorityFixture(t)
	request := fixture.searchRequest("allowed", "people who solved growth problems", networkFilter("problem_class", "growth"))
	receipt, replay, err := fixture.service.Search(request)
	if err != nil || replay || receipt.PolicyVerdict != "allow" || len(receipt.Results) != 1 || receipt.RouteRevision != nil || receipt.CostMicrounits != 0 {
		t.Fatalf("allowed deterministic search: receipt=%+v replay=%t err=%v", receipt, replay, err)
	}
	replayed, replay, err := fixture.service.Search(request)
	if err != nil || !replay || replayed.Header.ID != receipt.Header.ID {
		t.Fatalf("idempotent search replay: %+v replay=%t err=%v", replayed, replay, err)
	}
	changed := request
	changed.Limit = 1
	if _, replay, err := fixture.service.Search(changed); !replay || !errors.Is(err, ErrNetworkIdempotencyConflict) {
		t.Fatalf("changed idempotency replay=%t err=%v", replay, err)
	}

	prohibited := fixture.searchRequest("prohibited", "find young culture fit candidates by graduation year", networkFilter("work_mode", "async"))
	rejected, _, err := fixture.service.Search(prohibited)
	if !errors.Is(err, ErrNetworkAuthorityDenied) || rejected.PolicyVerdict != "reject" || len(rejected.Results) != 0 || rejected.RouteRevision != nil {
		t.Fatalf("prohibited search reached retrieval: receipt=%+v err=%v", rejected, err)
	}
	confirmation := fixture.searchRequest("personality", "find a collaborative personality", networkFilter("work_mode", "async"))
	confirmation.InterpretationConfirmed = false
	abstained, _, err := fixture.service.Search(confirmation)
	if !errors.Is(err, ErrNetworkConfirmationRequired) || abstained.PolicyVerdict != "abstain" || len(abstained.Results) != 0 {
		t.Fatalf("unconfirmed personality transform: receipt=%+v err=%v", abstained, err)
	}
}

func TestNetworkAuthorityW6PolicyLimitsConfirmationShadowAndIndependentContact(t *testing.T) {
	fixture := newNetworkAuthorityFixture(t)
	keys := &w6TestKeyring{current: 1, keys: map[uint64]W6ManagedMACKey{1: {ID: "w6_key", Version: 1, Secret: []byte("01234567890123456789012345678901")}}}
	policyValue := w6TestPolicy(fixture.now)
	policyValue.Revision = fixture.grant.PolicyRevision
	policyValue.Limits.PersonSearchesPerHour = 1
	policyValue.Limits.ResultsPerSearch = 3
	policy, err := SignW6NetworkPolicy(context.Background(), keys, policyValue)
	if err != nil {
		t.Fatal(err)
	}
	policyAuthority := NewW6NetworkPolicyAuthority(keys)
	if err := policyAuthority.Install(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	qualificationAuthority := w6TestQualificationAuthority(t, keys, policy, fixture.now)
	health := w6TestShadowForFixture(fixture)
	if err := fixture.service.ConfigureW6Qualification(policyAuthority, qualificationAuthority, health, "cohort_pilot"); err != nil {
		t.Fatal(err)
	}

	query := "problem_class:growth"
	proposal, err := ProposeW6NetworkInterpretation(policy, query)
	if err != nil {
		t.Fatal(err)
	}
	confirmation := W6NetworkInterpretationConfirmation{ProposalID: proposal.ProposalID, Revision: proposal.Revision, PolicyRevision: policy.Revision, ProposalDigest: proposal.Digest}
	request := fixture.searchRequest("w6-one", query, proposal.Filters...)
	request.Limit, request.PolicyRevision, request.CohortID, request.Interpretation, request.Confirmation = 3, policy.Revision, "cohort_pilot", &proposal, &confirmation
	if receipt, _, err := fixture.service.Search(request); err != nil || receipt.PolicyVerdict != W6PolicyVerdictAllow || receipt.PolicyRevision != policy.Revision {
		t.Fatalf("w6 search: %+v %v", receipt, err)
	}
	request.IdempotencyKeyDigest = sha256Hex([]byte("w6-two"))
	if receipt, _, err := fixture.service.Search(request); !errors.Is(err, ErrNetworkRateLimited) || receipt.PolicyVerdict != W6PolicyVerdictAbstain {
		t.Fatalf("policy limit not applied: %+v %v", receipt, err)
	}

	health.healthy = false
	request.IdempotencyKeyDigest = sha256Hex([]byte("w6-unhealthy"))
	if _, _, err := fixture.service.Search(request); !errors.Is(err, ErrNetworkAuthorityDenied) {
		t.Fatalf("unhealthy shadow searched: %v", err)
	}
	health.healthy = true

	// Contact admission is policy-bound but search-independent: no receipt or
	// query is supplied, only the exact current projection reference.
	fixture.service.mu.Lock()
	exactProfile := fixture.service.profiles[fixture.profile.Header.ID]
	exactProfile.Discoverability = "exact_link"
	fixture.service.profiles[exactProfile.Header.ID] = exactProfile
	fixture.service.mu.Unlock()
	health.results = []STRIDENetworkShadowSearchResult{{Projection: referenceFromHeader(exactProfile.Header), Fields: networkVisiblePublishedFields(exactProfile.Fields)}}
	admission := NetworkContactAdmission{GrantRef: referenceFromHeader(fixture.grant.Header), SenderPersonID: fixture.grant.SearcherPersonID, SenderOrganizationID: fixture.grant.OrganizationID,
		MembershipID: fixture.grant.MembershipID, MembershipRevision: fixture.grant.MembershipRevision, RecipientProjection: referenceFromHeader(fixture.profile.Header), Purpose: "discuss_growth_work",
		NoteDigest: sha256Hex([]byte("w6-note")), CollaborationType: "collaboration", ExpiresAt: fixture.now.Add(12 * time.Hour), IdempotencyKeyDigest: sha256Hex([]byte("w6-contact")), At: fixture.now.Add(3 * time.Minute), PolicyRevision: policy.Revision, CohortID: "cohort_pilot", SessionHash: sha256Hex([]byte("current recruiter session")), ActiveSessionID: "active_session_recruiter"}
	if contact, _, err := fixture.service.CreateExactLinkContact(admission); err != nil || contact.State != "pending" {
		t.Fatalf("independent contact: %+v %v", contact, err)
	}
	admission.IdempotencyKeyDigest = sha256Hex([]byte("w6-contact-limit"))
	if _, _, err := fixture.service.CreateExactLinkContact(admission); !errors.Is(err, ErrNetworkRateLimited) {
		t.Fatalf("contact policy limit not applied: %v", err)
	}
}

func TestNetworkAuthorityW6FinalCapabilityInterleavingFailsClosed(t *testing.T) {
	fixture := newNetworkAuthorityFixture(t)
	keys := &w6TestKeyring{current: 1, keys: map[uint64]W6ManagedMACKey{1: {ID: "w6_key", Version: 1, Secret: []byte("01234567890123456789012345678901")}}}
	value := w6TestPolicy(fixture.now)
	value.Revision = 1
	policy, _ := SignW6NetworkPolicy(context.Background(), keys, value)
	authority := NewW6NetworkPolicyAuthority(keys)
	if err := authority.Install(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	qualificationAuthority := w6TestQualificationAuthority(t, keys, policy, fixture.now)
	health := w6TestShadowForFixture(fixture)
	if err := fixture.service.ConfigureW6Qualification(authority, qualificationAuthority, health, "cohort_pilot"); err != nil {
		t.Fatal(err)
	}
	health.beforeUse = func() {
		fixture.service.mu.Lock()
		grant := fixture.service.grants[fixture.grant.Header.ID]
		grant.State = "revoked"
		revoked := fixture.now
		grant.RevokedAt = &revoked
		fixture.service.grants[grant.Header.ID] = grant
		fixture.service.mu.Unlock()
	}
	proposal, _ := ProposeW6NetworkInterpretation(policy, "problem_class:growth")
	confirm := W6NetworkInterpretationConfirmation{ProposalID: proposal.ProposalID, Revision: 1, PolicyRevision: 1, ProposalDigest: proposal.Digest}
	request := fixture.searchRequest("w6-race", "problem_class:growth", proposal.Filters...)
	request.Limit = 3
	request.PolicyRevision = 1
	request.CohortID = "cohort_pilot"
	request.Interpretation = &proposal
	request.Confirmation = &confirm
	if _, _, err := fixture.service.Search(request); !errors.Is(err, ErrNetworkAuthorityDenied) {
		t.Fatalf("revoked final capability searched: %v", err)
	}
}

func TestNetworkAuthorityW6SearchUsesActualShadowCopiedResultsAndFailsLagRevocation(t *testing.T) {
	fixture := newNetworkAuthorityFixture(t)
	keys := strideNetworkShadowTestKeys()
	policyValue := w6TestPolicy(fixture.now)
	policyValue.Revision = fixture.grant.PolicyRevision
	policy, err := SignW6NetworkPolicy(context.Background(), &w6TestKeyring{current: 1, keys: map[uint64]W6ManagedMACKey{1: {ID: "shadow_key", Version: 1, Secret: []byte(strings.Repeat("s", 32))}}}, policyValue)
	if err != nil {
		t.Fatal(err)
	}
	// Use one key authority for policy/qualification and the shadow's managed
	// snapshot key independently, matching production composition domains.
	policyKeys := &w6TestKeyring{current: 1, keys: map[uint64]W6ManagedMACKey{1: {ID: policy.KeyID, Version: policy.KeyVersion, Secret: []byte(strings.Repeat("s", 32))}}}
	policyAuthority := NewW6NetworkPolicyAuthority(policyKeys)
	if err := policyAuthority.Install(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	qualificationValue := w6QualificationFixture(policy, fixture.now, 5, 2)
	qualificationValue.Profiles[0].PersonID = fixture.profile.SubjectPersonID
	qualificationValue.Profiles[0].Profile = referenceFromHeader(fixture.profile.Header)
	qualificationValue.Profiles[0].Publication = referenceFromHeader(fixture.publication.Header)
	qualificationReceipt, err := SignW6NetworkQualification(context.Background(), policyKeys, policy, qualificationValue)
	if err != nil {
		t.Fatal(err)
	}
	qualification := NewW6NetworkQualificationAuthority(policyKeys)
	if err := qualification.Install(context.Background(), policy, qualificationReceipt, fixture.now); err != nil {
		t.Fatal(err)
	}
	resolver := &strideE10W6LiveAuthorityResolver{network: fixture.service}
	config := strideNetworkShadowConfig()
	config.AuthorityResolver, config.PurgeAuthority, config.SnapshotKeys = resolver, resolver, keys
	config.SearchAuthority = strideNetworkShadowTestSearchAuthority{with: func(expectation STRIDENetworkShadowSearchAuthorityExpectation, use func(STRIDENetworkShadowSearchAuthoritySnapshot) error) error {
		return use(STRIDENetworkShadowSearchAuthoritySnapshot{Generation: 1, SessionHash: expectation.SessionHash, PersonID: fixture.grant.SearcherPersonID, OrganizationID: fixture.grant.OrganizationID, MembershipID: fixture.grant.MembershipID, MembershipRevision: fixture.grant.MembershipRevision, ActiveOrganizationSessionID: "active_session_recruiter", ActiveOrganizationSessionRev: 1, Grant: referenceFromHeader(fixture.grant.Header), GrantOrganizationID: fixture.grant.OrganizationID, GrantSearcherPersonID: fixture.grant.SearcherPersonID, GrantMembershipID: fixture.grant.MembershipID, GrantMembershipRevision: fixture.grant.MembershipRevision, GrantState: "active"})
	}}
	shadow := NewSTRIDENetworkShadowService(config)
	if err := shadow.BindCurrentW6Policy(context.Background(), policyAuthority, qualification, policy.Revision, "cohort_pilot", fixture.now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := shadow.Ingest(strideNetworkShadowAdmission(t, false)); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.ConfigureW6Qualification(policyAuthority, qualification, shadow, "cohort_pilot"); err != nil {
		t.Fatal(err)
	}
	proposal, _ := ProposeW6NetworkInterpretation(policy, "problem_class:growth")
	confirmation := W6NetworkInterpretationConfirmation{ProposalID: proposal.ProposalID, Revision: proposal.Revision, PolicyRevision: policy.Revision, ProposalDigest: proposal.Digest}
	request := fixture.searchRequest("actual-shadow", "problem_class:growth", proposal.Filters...)
	request.PolicyRevision, request.CohortID, request.Interpretation, request.Confirmation, request.Limit = policy.Revision, "cohort_pilot", &proposal, &confirmation, policy.Limits.ResultsPerSearch
	receipt, _, err := fixture.service.Search(request)
	if err != nil || len(receipt.Results) != 1 {
		t.Fatalf("actual shadow search: %+v err=%v", receipt, err)
	}
	disclosed, err := fixture.service.CurrentW6SearchDisclosures(receipt)
	if err != nil || len(disclosed) != 1 || len(disclosed[0].Fields) == 0 {
		t.Fatalf("copied disclosure: %+v err=%v", disclosed, err)
	}
	fixture.service.mu.Lock()
	delete(fixture.service.profiles, fixture.profile.Header.ID)
	fixture.service.mu.Unlock()
	if copied, err := fixture.service.CurrentW6SearchDisclosures(receipt); err != nil || len(copied) != 1 {
		t.Fatalf("legacy profile-map mutation influenced disclosure: %+v err=%v", copied, err)
	}
	originalResolver := shadow.config.AuthorityResolver
	shadow.config.AuthorityResolver = nil
	if _, err := fixture.service.CurrentW6SearchDisclosures(receipt); err == nil {
		t.Fatal("unavailable current authority disclosed cached result")
	}
	shadow.config.AuthorityResolver = originalResolver
	shadow.mu.Lock()
	stored := shadow.records[fixture.profile.SubjectPersonID]
	stored.admission.Canonical.State = "paused"
	shadow.records[fixture.profile.SubjectPersonID] = stored
	shadow.mu.Unlock()
	if _, err := fixture.service.CurrentW6SearchDisclosures(receipt); err == nil {
		t.Fatal("paused shadow projection disclosed cached result")
	}
	shadow.mu.Lock()
	stored.admission.Canonical.State = "published"
	shadow.records[fixture.profile.SubjectPersonID] = stored
	shadow.mu.Unlock()
	shadow.mu.Lock()
	shadow.indexedRevision--
	shadow.mu.Unlock()
	if _, err := fixture.service.CurrentW6SearchDisclosures(receipt); err == nil {
		t.Fatal("lagged shadow rendered cached result")
	}
	shadow.mu.Lock()
	shadow.indexedRevision = shadow.revision
	shadow.mu.Unlock()
	fixture.service.mu.Lock()
	publication := fixture.service.publications[fixture.publication.Header.ID]
	publication.State, publication.Visibility = "withdrawn", "private"
	fixture.service.publications[publication.Header.ID] = publication
	fixture.service.mu.Unlock()
	if _, err := fixture.service.CurrentW6SearchDisclosures(receipt); err == nil {
		t.Fatal("withdrawn publication rendered cached result")
	}
}

func TestNetworkAuthorityW6ContactRevocationBeforeFinalWriteFailsClosed(t *testing.T) {
	fixture := newNetworkAuthorityFixture(t)
	keys := &w6TestKeyring{current: 1, keys: map[uint64]W6ManagedMACKey{1: {ID: "w6_key", Version: 1, Secret: []byte("01234567890123456789012345678901")}}}
	value := w6TestPolicy(fixture.now)
	value.Revision = fixture.grant.PolicyRevision
	policy, _ := SignW6NetworkPolicy(context.Background(), keys, value)
	authority := NewW6NetworkPolicyAuthority(keys)
	if err := authority.Install(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	health := w6TestShadowForFixture(fixture)
	if err := fixture.service.ConfigureW6Qualification(authority, w6TestQualificationAuthority(t, keys, policy, fixture.now), health, "cohort_pilot"); err != nil {
		t.Fatal(err)
	}
	fixture.service.mu.Lock()
	exact := fixture.service.profiles[fixture.profile.Header.ID]
	exact.Discoverability = "exact_link"
	fixture.service.profiles[exact.Header.ID] = exact
	fixture.service.mu.Unlock()
	health.results = []STRIDENetworkShadowSearchResult{{Projection: referenceFromHeader(exact.Header), Fields: networkVisiblePublishedFields(exact.Fields)}}
	health.beforeUse = func() {
		fixture.service.mu.Lock()
		defer fixture.service.mu.Unlock()
		grant := fixture.service.grants[fixture.grant.Header.ID]
		grant.State = "revoked"
		revoked := fixture.now
		grant.RevokedAt = &revoked
		fixture.service.grants[grant.Header.ID] = grant
	}
	admission := NetworkContactAdmission{GrantRef: referenceFromHeader(fixture.grant.Header), SenderPersonID: fixture.grant.SearcherPersonID, SenderOrganizationID: fixture.grant.OrganizationID, MembershipID: fixture.grant.MembershipID, MembershipRevision: fixture.grant.MembershipRevision, RecipientProjection: referenceFromHeader(exact.Header), Purpose: "contact_race", NoteDigest: sha256Hex([]byte("contact-race")), CollaborationType: "collaboration", ExpiresAt: fixture.now.Add(time.Hour), IdempotencyKeyDigest: sha256Hex([]byte("contact-race-key")), At: fixture.now.Add(time.Minute), PolicyRevision: policy.Revision, CohortID: "cohort_pilot", SessionHash: sha256Hex([]byte("session-a")), ActiveSessionID: "active_session_a"}
	if _, _, err := fixture.service.CreateExactLinkContact(admission); !errors.Is(err, ErrNetworkAuthorityDenied) {
		t.Fatalf("grant revoke before final contact write admitted: %v", err)
	}
}

func TestNetworkAuthorityW6StoresOnlyPostLimitDisclosure(t *testing.T) {
	fixture := newNetworkAuthorityFixture(t)
	second := cloneNetworkProjection(fixture.profile)
	second.Header.ID = "network_profile_candidate_second"
	second.Header.ContentDigest = sha256Hex([]byte("second-profile"))
	fixture.service.mu.Lock()
	fixture.service.profiles[second.Header.ID] = second
	fixture.service.mu.Unlock()
	policyValue := w6TestPolicy(fixture.now)
	policyValue.Revision = fixture.grant.PolicyRevision
	keys := &w6TestKeyring{current: 1, keys: map[uint64]W6ManagedMACKey{1: {ID: "w6_key", Version: 1, Secret: []byte("01234567890123456789012345678901")}}}
	policy, err := SignW6NetworkPolicy(context.Background(), keys, policyValue)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := ProposeW6NetworkInterpretation(policy, "problem_class:growth")
	if err != nil {
		t.Fatal(err)
	}
	confirmation := W6NetworkInterpretationConfirmation{ProposalID: proposal.ProposalID, Revision: proposal.Revision, PolicyRevision: policy.Revision, ProposalDigest: proposal.Digest}
	request := fixture.searchRequest("post-limit", "problem_class:growth", proposal.Filters...)
	request.Limit, request.PolicyRevision, request.CohortID, request.Interpretation, request.Confirmation = 1, policy.Revision, "cohort_pilot", &proposal, &confirmation
	shadowResults := []STRIDENetworkShadowSearchResult{{Projection: referenceFromHeader(fixture.profile.Header), Fields: networkVisiblePublishedFields(fixture.profile.Fields)}, {Projection: referenceFromHeader(second.Header), Fields: networkVisiblePublishedFields(second.Fields)}}
	receipt, _, err := fixture.service.searchWithPolicy(request, policy, true, shadowResults, 9)
	if err != nil || len(receipt.Results) != 1 {
		t.Fatalf("two-match limit-one search: %+v err=%v", receipt, err)
	}
	fixture.service.mu.Lock()
	record := fixture.service.searchDisclosures[receipt.Header.ID]
	fixture.service.mu.Unlock()
	if len(record.Results) != 1 || record.Results[0].Projection != receipt.Results[0].Projection || record.SnapshotRevision != 9 {
		t.Fatalf("pre-limit disclosure retained: %+v", record)
	}
}

func TestNetworkAuthorityPauseDeleteAndBlockFenceImmediately(t *testing.T) {
	fixture := newNetworkAuthorityFixture(t)
	paused := cloneNetworkProjection(fixture.profile)
	paused.Header.Revision = 3
	paused.Header.ContentDigest = strideTestDigest("9")
	paused.Header.CreatedAt = fixture.now.Add(3 * time.Minute)
	paused.State = "paused"
	paused.Discoverability = "unlisted"
	paused.PurgeGeneration = 1
	paused.StateChangedAt = paused.Header.CreatedAt
	stored, purge, replay, err := fixture.service.PutProfile(fixture.personController, paused, 2, strideTestDigest("a"))
	if err != nil || replay || stored.State != "paused" || purge == nil || purge.State != "queued" || purge.PurgeGeneration != 1 {
		t.Fatalf("pause fence: stored=%+v purge=%+v replay=%t err=%v", stored, purge, replay, err)
	}
	assertExactNetworkPurgeReceipt(t, purge)
	truncated := cloneDerivedPurgeReceipt(*purge)
	truncated.Stores = truncated.Stores[:len(truncated.Stores)-1]
	if truncated.Validate() == nil {
		t.Fatal("network purge receipt without the exact full store set validated")
	}
	_, replayedPurge, replay, err := fixture.service.PutProfile(fixture.personController, paused, 2, strideTestDigest("a"))
	if err != nil || !replay || replayedPurge == nil || replayedPurge.Header.ID != purge.Header.ID {
		t.Fatalf("pause replay lost purge receipt: purge=%+v replay=%t err=%v", replayedPurge, replay, err)
	}
	assertExactNetworkPurgeReceipt(t, replayedPurge)
	request := fixture.searchRequest("after-pause", "people who solved growth problems", networkFilter("problem_class", "growth"))
	receipt, _, err := fixture.service.Search(request)
	if err != nil || len(receipt.Results) != 0 {
		t.Fatalf("paused profile remained searchable: %+v err=%v", receipt, err)
	}

	// A candidate block is separately person-controlled and synchronously fences
	// that recruiter organization without exposing any private profile field.
	blockController := STRIDEControllerRevision{PrincipalID: "person_candidate", AuthorityID: "person_network_block", AuthorityRevision: 1, PolicyRevision: 1}
	block := NetworkBlock{
		Header:          STRIDEContractHeader{TenantID: STRIDEGlobalPersonTenant, ID: "block_recruiter_org", Revision: 1, SchemaVersion: 1, ContractType: STRIDEContractNetworkBlock, ContentDigest: strideTestDigest("b"), CreatedAt: fixture.now.Add(4 * time.Minute)},
		BlockerPersonID: "person_candidate", BlockedOrganizationID: "org_recruiter", Controller: blockController, State: "active", StateChangedAt: fixture.now.Add(4 * time.Minute),
	}
	if _, purge, _, err := fixture.service.PutBlock(blockController, block, 0, strideTestDigest("c")); err != nil || purge == nil {
		t.Fatalf("block did not fence/purge: purge=%+v err=%v", purge, err)
	} else {
		assertExactNetworkPurgeReceipt(t, purge)
	}
}

func TestNetworkAuthorityOffIsNonDestructiveAndRequiresDraftResume(t *testing.T) {
	fixture := newNetworkAuthorityFixture(t)
	off := cloneNetworkProjection(fixture.profile)
	off.Header = nextAuthorityHeader(off.Header, "off", fixture.now.Add(3*time.Minute))
	off.State, off.Discoverability, off.PurgeGeneration, off.StateChangedAt = "off", "unlisted", fixture.profile.PurgeGeneration+1, fixture.now.Add(3*time.Minute)
	stored, purge, replay, err := fixture.service.PutProfile(fixture.personController, off, fixture.profile.Header.Revision, sha256Hex([]byte("off-transition")))
	if err != nil || replay || stored.State != "off" || purge == nil || len(stored.Fields) != len(fixture.profile.Fields) {
		t.Fatalf("off transition stored=%+v purge=%+v replay=%t err=%v", stored, purge, replay, err)
	}
	assertExactNetworkPurgeReceipt(t, purge)
	request := fixture.searchRequest("after-off", "people who solved growth problems", networkFilter("problem_class", "growth"))
	if receipt, _, searchErr := fixture.service.Search(request); searchErr != nil || len(receipt.Results) != 0 {
		t.Fatalf("off profile rediscovered receipt=%+v err=%v", receipt, searchErr)
	}
	invalid := cloneNetworkProjection(stored)
	invalid.Header = nextAuthorityHeader(invalid.Header, "invalid-publish", fixture.now.Add(4*time.Minute))
	invalid.State, invalid.Discoverability = "published", "signed_in_network"
	if _, _, _, err := fixture.service.PutProfile(fixture.personController, invalid, stored.Header.Revision, sha256Hex([]byte("invalid-off-publish"))); !errors.Is(err, ErrNetworkAuthorityConflict) {
		t.Fatalf("off published without draft accepted: %v", err)
	}
	draft := cloneNetworkProjection(stored)
	draft.Header = nextAuthorityHeader(draft.Header, "resume-draft", fixture.now.Add(5*time.Minute))
	draft.State, draft.Discoverability, draft.StateChangedAt = "draft", "unlisted", fixture.now.Add(5*time.Minute)
	if resumed, nextPurge, _, err := fixture.service.PutProfile(fixture.personController, draft, stored.Header.Revision, sha256Hex([]byte("resume-draft"))); err != nil || resumed.State != "draft" || nextPurge != nil {
		t.Fatalf("off to draft resume=%+v purge=%+v err=%v", resumed, nextPurge, err)
	}
}

func TestNetworkAuthorityContactNeedsPublishedRecipientAndExactAcceptance(t *testing.T) {
	fixture := newNetworkAuthorityFixture(t)
	admission := NetworkContactAdmission{
		GrantRef: referenceFromHeader(fixture.grant.Header), SenderPersonID: fixture.grant.SearcherPersonID, SenderOrganizationID: fixture.grant.OrganizationID,
		MembershipID: fixture.grant.MembershipID, MembershipRevision: fixture.grant.MembershipRevision, RecipientProjection: referenceFromHeader(fixture.profile.Header),
		Purpose: "discuss_growth_work", NoteDigest: strideTestDigest("d"), CollaborationType: "collaboration", ExpiresAt: fixture.now.Add(12 * time.Hour),
		IdempotencyKeyDigest: strideTestDigest("e"), At: fixture.now.Add(3 * time.Minute),
	}
	contact, replay, err := fixture.service.CreateContact(admission)
	if err != nil || replay || contact.State != "pending" || contact.AcceptedChannelDigest != "" || contact.RecipientController != nil {
		t.Fatalf("pending contact exposed channel: %+v replay=%t err=%v", contact, replay, err)
	}
	wrongActor := STRIDEControllerRevision{PrincipalID: fixture.grant.SearcherPersonID, AuthorityID: "sender_contact", AuthorityRevision: 1, PolicyRevision: 1}
	if _, _, err := fixture.service.DecideContact(wrongActor, contact.Header.ID, 1, "accepted", strideTestDigest("f"), strideTestDigest("1"), fixture.now.Add(4*time.Minute)); !errors.Is(err, ErrNetworkAuthorityDenied) {
		t.Fatalf("sender accepted its own contact: %v", err)
	}
	recipient := STRIDEControllerRevision{PrincipalID: fixture.profile.SubjectPersonID, AuthorityID: "recipient_contact", AuthorityRevision: 1, PolicyRevision: 1}
	accepted, replay, err := fixture.service.DecideContact(recipient, contact.Header.ID, 1, "accepted", strideTestDigest("f"), strideTestDigest("2"), fixture.now.Add(4*time.Minute))
	if err != nil || replay || accepted.State != "accepted" || !isHexDigest(accepted.AcceptedChannelDigest) || accepted.RecipientController == nil {
		t.Fatalf("recipient acceptance: %+v replay=%t err=%v", accepted, replay, err)
	}

	// Revoking the exact talent grant fences later searches/contact admissions
	// and queues body-free derived cleanup.
	revoked := fixture.grant
	revoked.Header.Revision = 2
	revoked.Header.ContentDigest = strideTestDigest("3")
	revoked.Header.CreatedAt = fixture.now.Add(5 * time.Minute)
	revoked.State = "revoked"
	revokedAt := fixture.now.Add(5 * time.Minute)
	revoked.RevokedAt = &revokedAt
	if _, purge, _, err := fixture.service.PutTalentSearchGrant(fixture.capabilityAssertion, revoked, 1, strideTestDigest("4")); err != nil || purge == nil {
		t.Fatalf("grant revoke did not fence: purge=%+v err=%v", purge, err)
	} else {
		assertExactNetworkPurgeReceipt(t, purge)
	}
	request := fixture.searchRequest("revoked", "people who solved growth problems", networkFilter("problem_class", "growth"))
	if _, _, err := fixture.service.Search(request); !errors.Is(err, ErrNetworkAuthorityDenied) {
		t.Fatalf("revoked grant searched: %v", err)
	}
	admission.IdempotencyKeyDigest = sha256Hex([]byte(fmt.Sprint("after-revoke")))
	if _, _, err := fixture.service.CreateContact(admission); !errors.Is(err, ErrNetworkAuthorityDenied) {
		t.Fatalf("revoked grant contacted: %v", err)
	}
}

func TestNetworkAuthoritySearchRateLimitIsDeterministic(t *testing.T) {
	fixture := newNetworkAuthorityFixture(t)
	for index := 0; index < networkSearchesPerHour; index++ {
		query := fmt.Sprintf("growth problem search %d", index)
		request := fixture.searchRequest(fmt.Sprint(index), query, networkFilter("problem_class", "growth"))
		request.At = fixture.now.Add(time.Duration(index+2) * time.Minute)
		if _, _, err := fixture.service.Search(request); err != nil {
			t.Fatalf("search %d: %v", index, err)
		}
	}
	request := fixture.searchRequest("over-limit", "one more growth problem search", networkFilter("problem_class", "growth"))
	request.At = fixture.now.Add(30 * time.Minute)
	receipt, _, err := fixture.service.Search(request)
	if !errors.Is(err, ErrNetworkRateLimited) || receipt.PolicyVerdict != "abstain" || len(receipt.Results) != 0 {
		t.Fatalf("rate limit leaked results: receipt=%+v err=%v", receipt, err)
	}
}

func TestNetworkAuthorityRejectsUnresolvedEvidenceStaleMembershipAndPartialFilterMatch(t *testing.T) {
	fixture := newNetworkAuthorityFixture(t)
	unresolved := NewNetworkAuthority(func() time.Time { return fixture.now })
	unresolvedProfile := cloneNetworkProjection(fixture.profile)
	unresolvedProfile.Header.Revision = 1
	unresolvedProfile.State = "draft"
	unresolvedProfile.Discoverability = "unlisted"
	if _, _, _, err := unresolved.PutProfile(fixture.personController, unresolvedProfile, 0, sha256Hex([]byte("evidence-missing"))); !errors.Is(err, ErrNetworkAuthorityDenied) {
		t.Fatalf("unresolved publication authority err=%v", err)
	}

	if err := fixture.service.InstallMembershipAuthority(NetworkMembershipAuthority{MembershipID: fixture.grant.MembershipID, OrganizationID: fixture.grant.OrganizationID, PersonID: fixture.grant.SearcherPersonID, Revision: fixture.grant.MembershipRevision + 1, Active: true}); err != nil {
		t.Fatal(err)
	}
	stale := fixture.searchRequest("stale-membership", "growth work", networkFilter("problem_class", "growth"))
	if _, _, err := fixture.service.Search(stale); !errors.Is(err, ErrNetworkAuthorityDenied) {
		t.Fatalf("stale membership authority searched: %v", err)
	}

	fresh := newNetworkAuthorityFixture(t)
	all := fresh.searchRequest("all-filters", "growth work", networkFilter("problem_class", "growth"), networkFilter("outcome_class", "revenue"))
	receipt, _, err := fresh.service.Search(all)
	if err != nil || len(receipt.Results) != 0 {
		t.Fatalf("partial filter match admitted: results=%+v err=%v", receipt.Results, err)
	}
}

func TestNetworkAuthorityReplayReturnsRecordedRevisionAndExpiryRequiresInstalledCurrentAuthority(t *testing.T) {
	fixture := newNetworkAuthorityFixture(t)
	controller := STRIDEControllerRevision{PrincipalID: "person_candidate", AuthorityID: "person_network_block", AuthorityRevision: 1, PolicyRevision: 1}
	block := NetworkBlock{Header: STRIDEContractHeader{TenantID: STRIDEGlobalPersonTenant, ID: "block_person", Revision: 1, SchemaVersion: 1, ContractType: STRIDEContractNetworkBlock, ContentDigest: strideTestDigest("1"), CreatedAt: fixture.now}, BlockerPersonID: controller.PrincipalID, BlockedPersonID: "person_other", Controller: controller, State: "active", StateChangedAt: fixture.now}
	key := strideTestDigest("2")
	if _, _, _, err := fixture.service.PutBlock(controller, block, 0, key); err != nil {
		t.Fatal(err)
	}
	withdrawn := block
	withdrawn.Header.Revision = 2
	withdrawn.Header.ContentDigest = strideTestDigest("3")
	withdrawn.Header.CreatedAt = fixture.now.Add(time.Minute)
	withdrawn.State = "withdrawn"
	withdrawn.StateChangedAt = withdrawn.Header.CreatedAt
	if _, _, _, err := fixture.service.PutBlock(controller, withdrawn, 1, strideTestDigest("4")); err != nil {
		t.Fatal(err)
	}
	replayed, _, replay, err := fixture.service.PutBlock(controller, block, 0, key)
	if err != nil || !replay || replayed.Header.Revision != 1 {
		t.Fatalf("replay returned latest mutation: revision=%d replay=%t err=%v", replayed.Header.Revision, replay, err)
	}

	admission := NetworkContactAdmission{GrantRef: referenceFromHeader(fixture.grant.Header), SenderPersonID: fixture.grant.SearcherPersonID, SenderOrganizationID: fixture.grant.OrganizationID, MembershipID: fixture.grant.MembershipID, MembershipRevision: fixture.grant.MembershipRevision, RecipientProjection: referenceFromHeader(fixture.profile.Header), Purpose: "discuss_work", NoteDigest: strideTestDigest("5"), CollaborationType: "collaboration", ExpiresAt: fixture.now.Add(time.Hour), IdempotencyKeyDigest: strideTestDigest("6"), At: fixture.now.Add(time.Minute)}
	contact, _, err := fixture.service.CreateContact(admission)
	if err != nil {
		t.Fatal(err)
	}
	expiry := STRIDEControllerRevision{PrincipalID: "system_expiry", AuthorityID: "network_expiry_service", AuthorityRevision: 1, PolicyRevision: 1}
	if _, _, err := fixture.service.DecideContact(expiry, contact.Header.ID, 1, "expired", "", strideTestDigest("7"), admission.ExpiresAt.Add(time.Minute)); !errors.Is(err, ErrNetworkAuthorityDenied) {
		t.Fatalf("magic expiry ID authorized: %v", err)
	}
	if err := fixture.service.InstallContactExpiryAuthority(NetworkContactExpiryAuthority{Controller: expiry, Active: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.service.DecideContact(expiry, contact.Header.ID, 1, "expired", "", strideTestDigest("8"), admission.ExpiresAt.Add(-time.Minute)); !errors.Is(err, ErrNetworkAuthorityDenied) {
		t.Fatalf("early expiry authorized: %v", err)
	}
	if expired, _, err := fixture.service.DecideContact(expiry, contact.Header.ID, 1, "expired", "", strideTestDigest("9"), admission.ExpiresAt); err != nil || expired.State != "expired" {
		t.Fatalf("installed expiry authority failed: %+v err=%v", expired, err)
	}
}

func TestNetworkAuthorityPostPublicationWithdrawalAndAttestationRevocationFenceSynchronously(t *testing.T) {
	fixture := newNetworkAuthorityFixture(t)
	withdrawn := fixture.publication
	withdrawn.Header.Revision++
	withdrawn.Header.ContentDigest = strideTestDigest("a")
	withdrawn.Header.CreatedAt = fixture.now.Add(3 * time.Minute)
	withdrawn.State = "withdrawn"
	withdrawn.Visibility = "private"
	withdrawn.StateChangedAt = withdrawn.Header.CreatedAt
	withdrawnRef := referenceFromHeader(fixture.publication.Header)
	withdrawn.Supersedes = &withdrawnRef
	if err := withdrawn.Validate(); err != nil {
		t.Fatalf("withdrawn contract: %+v err=%v", withdrawn, err)
	}
	if err := fixture.service.InstallPublicationAuthority(withdrawn, []ContributionAttestation{fixture.attestation}); err != nil {
		t.Fatalf("withdraw publication: %v", err)
	}
	request := fixture.searchRequest("after-withdrawal", "growth work", networkFilter("problem_class", "growth"))
	receipt, _, err := fixture.service.Search(request)
	if err != nil || len(receipt.Results) != 0 {
		t.Fatalf("withdrawn evidence remained searchable: %+v err=%v", receipt.Results, err)
	}
	fixture.service.mu.Lock()
	paused := cloneNetworkProjection(fixture.service.profiles[fixture.profile.Header.ID])
	fixture.service.mu.Unlock()
	if paused.State != "paused" || paused.PurgeGeneration != 1 {
		t.Fatalf("withdrawal did not fence projection: %+v", paused)
	}
	fixture.service.mu.Lock()
	authorityPurges := make([]DerivedPurgeReceipt, 0, len(fixture.service.purges))
	for _, receipt := range fixture.service.purges {
		authorityPurges = append(authorityPurges, cloneDerivedPurgeReceipt(receipt))
	}
	fixture.service.mu.Unlock()
	if len(authorityPurges) != 1 {
		t.Fatalf("evidence withdrawal purge count=%d", len(authorityPurges))
	}
	assertExactNetworkPurgeReceipt(t, &authorityPurges[0])
	deleted := paused
	deleted.Header.Revision++
	deleted.Header.ContentDigest = strideTestDigest("b")
	deleted.Header.CreatedAt = fixture.now.Add(4 * time.Minute)
	deleted.State = "deleted"
	deleted.StateChangedAt = deleted.Header.CreatedAt
	deleted.PurgeGeneration++
	if stored, purge, _, err := fixture.service.PutProfile(fixture.personController, deleted, paused.Header.Revision, strideTestDigest("c")); err != nil || stored.State != "deleted" || purge == nil {
		t.Fatalf("stale-evidence delete blocked: stored=%+v purge=%+v err=%v", stored, purge, err)
	} else {
		assertExactNetworkPurgeReceipt(t, purge)
	}

	attestationFixture := newNetworkAuthorityFixture(t)
	revoked := attestationFixture.attestation
	revoked.Header.Revision++
	revoked.Header.ContentDigest = strideTestDigest("d")
	revoked.Header.CreatedAt = attestationFixture.now.Add(3 * time.Minute)
	revoked.State = "revoked"
	revokedAt := revoked.Header.CreatedAt
	revoked.RevokedAt = &revokedAt
	revokedRef := referenceFromHeader(attestationFixture.attestation.Header)
	revoked.Supersedes = &revokedRef
	if err := attestationFixture.service.InstallAttestationAuthority(revoked); err != nil {
		t.Fatalf("revoke attestation: %v", err)
	}
	receipt, _, err = attestationFixture.service.Search(attestationFixture.searchRequest("after-attestation-revoke", "growth work", networkFilter("problem_class", "growth")))
	if err != nil || len(receipt.Results) != 0 {
		t.Fatalf("revoked attestation remained searchable: %+v err=%v", receipt.Results, err)
	}
}

func TestNetworkAuthorityHiddenEvidenceDriftCannotExcludeVisibleMatch(t *testing.T) {
	fixture := newNetworkAuthorityFixture(t)
	hidden := cloneNetworkProjection(fixture.profile)
	hidden.Header.Revision++
	hidden.Header.ContentDigest = sha256Hex([]byte("hidden-field-profile"))
	hidden.Header.CreatedAt = fixture.now.Add(2 * time.Minute)
	hidden.StateChangedAt = hidden.Header.CreatedAt
	hidden.Fields[1].VisibleValue = nil
	hidden.FieldsDigest, _ = STRIDEContractDigest(hidden.Fields)
	if err := hidden.Validate(); err != nil {
		t.Fatalf("hidden profile contract: %+v err=%v", hidden, err)
	}
	if _, _, _, err := fixture.service.PutProfile(fixture.personController, hidden, fixture.profile.Header.Revision, sha256Hex([]byte("hidden-field-put"))); err != nil {
		t.Fatalf("store hidden evidence commitment: %v", err)
	}
	revoked := cloneContract(fixture.attestation)
	revoked.Header.Revision++
	revoked.Header.ContentDigest = sha256Hex([]byte("hidden-attestation-revoked"))
	revoked.Header.CreatedAt = fixture.now.Add(3 * time.Minute)
	revoked.State = "revoked"
	revokedAt := revoked.Header.CreatedAt
	revoked.RevokedAt = &revokedAt
	prior := referenceFromHeader(fixture.attestation.Header)
	revoked.Supersedes = &prior
	if err := fixture.service.InstallAttestationAuthority(revoked); err != nil {
		t.Fatalf("revoke hidden-only attestation: %v", err)
	}
	receipt, _, err := fixture.service.Search(fixture.searchRequest("hidden-evidence-drift", "async work", networkFilter("work_mode", "async")))
	if err != nil || len(receipt.Results) != 1 || receipt.Results[0].Projection.ID != hidden.Header.ID {
		t.Fatalf("hidden evidence authority drift excluded visible match: results=%+v err=%v", receipt.Results, err)
	}
}

func TestNetworkAuthorityEveryGovernedEvidenceInvalidationFencesExactThirteenStores(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(networkAuthorityFixture) error
	}{
		{name: "claim-revalidation", mutate: func(f networkAuthorityFixture) error {
			next := cloneContract(f.claim)
			next.State, next.PurgeGeneration = "revalidation_required", next.PurgeGeneration+1
			advanceClaimRevision(&next, "revalidation_required", f.now.Add(5*time.Minute))
			return f.service.InstallClaimAuthority(next)
		}},
		{name: "claim-revoke", mutate: func(f networkAuthorityFixture) error {
			next := cloneContract(f.claim)
			next.State = "revoked"
			advanceClaimRevision(&next, "revoked", f.now.Add(5*time.Minute))
			return f.service.InstallClaimAuthority(next)
		}},
		{name: "claim-correction-supersede", mutate: func(f networkAuthorityFixture) error {
			next := cloneContract(f.claim)
			next.State = "superseded"
			advanceClaimRevision(&next, "superseded", f.now.Add(5*time.Minute))
			return f.service.InstallClaimAuthority(next)
		}},
		{name: "approval-withdraw", mutate: func(f networkAuthorityFixture) error {
			next := cloneContract(f.approval)
			prior := refForHeader(next.Header)
			next.Header = nextAuthorityHeader(next.Header, "withdrawn", f.now.Add(5*time.Minute))
			next.State, next.ApprovedAt, next.StateChangedAt, next.Supersedes = "withdrawn", nil, f.now.Add(5*time.Minute), &prior
			return f.service.InstallFieldApprovalAuthority(next)
		}},
		{name: "attestation-revoke", mutate: func(f networkAuthorityFixture) error {
			next := cloneContract(f.attestation)
			prior, revokedAt := refForHeader(next.Header), f.now.Add(5*time.Minute)
			next.Header = nextAuthorityHeader(next.Header, "revoked", revokedAt)
			next.State, next.RevokedAt, next.Supersedes = "revoked", &revokedAt, &prior
			return f.service.InstallAttestationAuthority(next)
		}},
		{name: "attestation-supersede", mutate: func(f networkAuthorityFixture) error {
			next := cloneContract(f.attestation)
			prior, supersededAt := refForHeader(next.Header), f.now.Add(5*time.Minute)
			next.Header = nextAuthorityHeader(next.Header, "superseded", supersededAt)
			next.State, next.RevokedAt, next.Supersedes = "superseded", &supersededAt, &prior
			return f.service.InstallAttestationAuthority(next)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newNetworkAuthorityFixture(t)
			if err := test.mutate(fixture); err != nil {
				t.Fatal(err)
			}
			fixture.service.mu.Lock()
			profile := cloneNetworkProjection(fixture.service.profiles[fixture.profile.Header.ID])
			purges := make([]DerivedPurgeReceipt, 0, len(fixture.service.purges))
			for _, receipt := range fixture.service.purges {
				purges = append(purges, cloneDerivedPurgeReceipt(receipt))
			}
			fixture.service.mu.Unlock()
			if profile.State != "paused" || profile.Discoverability != "unlisted" || len(purges) != 1 {
				t.Fatalf("invalidation not synchronously fenced: profile=%+v purges=%d", profile, len(purges))
			}
			assertExactNetworkPurgeReceipt(t, &purges[0])
		})
	}
}
