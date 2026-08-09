package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var contributionNetworkTestTime = time.Date(2026, 8, 8, 16, 0, 0, 0, time.UTC)

func contributionNetworkHeader(kind STRIDEContractType, id, tenant string) STRIDEContractHeader {
	return STRIDEContractHeader{TenantID: tenant, ID: id, Revision: 1, SchemaVersion: STRIDEContractSchemaVersion, ContractType: kind, ContentDigest: strings.Repeat("a", 64), CreatedAt: contributionNetworkTestTime}
}

func contributionNetworkRef(kind STRIDEContractType, id string) STRIDEReference {
	return STRIDEReference{ContractType: kind, ID: id, Revision: 1, Digest: strings.Repeat("b", 64)}
}

func contributionNetworkController(person string) STRIDEControllerRevision {
	return STRIDEControllerRevision{PrincipalID: person, AuthorityID: "authority_" + person, AuthorityRevision: 1, PolicyRevision: 1}
}

func validContributionClaim() ContributionClaim {
	return ContributionClaim{
		Header: contributionNetworkHeader(STRIDEContractContributionClaim, "claim_1", "org_1"), OrganizationID: "org_1", SubjectPersonID: "person_1",
		ContributionKind: "delivered", ProblemClass: "commerce", OutcomeClass: "reliability", SourceRefs: []STRIDEReference{contributionNetworkRef(STRIDEContractOutcome, "outcome_1")},
		EvidenceManifestDigest: strings.Repeat("c", 64), AttributionMethod: "source_observed", ACLRevision: 1, ConsentRevision: 1, PurgeGeneration: 1,
		PolicyRevision: 1, State: "verified", SubjectReview: ptrController(contributionNetworkController("person_1")), OrganizationReview: ptrController(contributionNetworkController("reviewer_1")), StateChangedAt: contributionNetworkTestTime,
	}
}

func ptrController(value STRIDEControllerRevision) *STRIDEControllerRevision { return &value }

func TestContributionClaimValidationAndLifecycleFailClosed(t *testing.T) {
	claim := validContributionClaim()
	if err := claim.Validate(); err != nil {
		t.Fatalf("valid claim rejected: %v", err)
	}
	if !ContributionClaimTransitionAllowed("verified", "revalidation_required") || ContributionClaimTransitionAllowed("verified", "candidate") || ContributionClaimTransitionAllowed("revoked", "verified") {
		t.Fatal("claim transition graph is not fail closed")
	}
	mutations := []func(*ContributionClaim){
		func(v *ContributionClaim) { v.Header.TenantID = "org_2" },
		func(v *ContributionClaim) { v.SubjectReview = nil },
		func(v *ContributionClaim) { v.SourceRefs = nil },
		func(v *ContributionClaim) { v.ConsentRevision = 0 },
		func(v *ContributionClaim) { v.State = "ranked" },
	}
	for i, mutate := range mutations {
		candidate := claim
		mutate(&candidate)
		if candidate.Validate() == nil {
			t.Fatalf("invalid claim mutation %d accepted", i)
		}
	}
}

func validFieldApproval() FieldReleaseApproval {
	approved := contributionNetworkTestTime
	return FieldReleaseApproval{Header: contributionNetworkHeader(STRIDEContractFieldReleaseApproval, "approval_1", "org_1"), OrganizationID: "org_1", SubjectPersonID: "person_1",
		Attestation: contributionNetworkRef(STRIDEContractContributionAttestation, "attestation_1"), FieldKey: "outcome", FieldValueDigest: strings.Repeat("c", 64), Source: contributionNetworkRef(STRIDEContractOutcome, "outcome_1"),
		SourceConsentRevision: 1, SourceACLRevision: 1, SourcePurgeGeneration: 1, Visibility: "signed_in_network", RequiredPartyIDs: []string{"customer_1"}, ApproverRole: "named_party", ApproverPartyID: "customer_1",
		Controller: contributionNetworkController("customer_1"), State: "approved", ApprovedAt: &approved, StateChangedAt: approved}
}

func TestNamedPartyAndAttestationRequireExactApprovals(t *testing.T) {
	approval := validFieldApproval()
	if err := approval.Validate(); err != nil {
		t.Fatalf("valid approval rejected: %v", err)
	}
	stale := approval
	stale.FieldValueDigest = "bad"
	if stale.Validate() == nil {
		t.Fatal("non-digest field value accepted")
	}
	wrong := approval
	wrong.ApproverPartyID = "stranger"
	if wrong.Validate() == nil {
		t.Fatal("non-required approver accepted")
	}

	attestation := ContributionAttestation{Header: contributionNetworkHeader(STRIDEContractContributionAttestation, "attestation_1", "org_1"), OrganizationID: "org_1", SubjectPersonID: "person_1",
		Claim: contributionNetworkRef(STRIDEContractContributionClaim, "claim_1"), EvidenceManifestDigest: strings.Repeat("c", 64),
		ReleasedFields:   []ReleasedContributionField{{FieldKey: "outcome", ValueDigest: strings.Repeat("e", 64), ApprovalRefs: []STRIDEReference{contributionNetworkRef(STRIDEContractFieldReleaseApproval, "approval_1")}}},
		VerificationTier: "organization_verified_redacted", Issuer: contributionNetworkController("reviewer_1"), SigningKeyID: "signing_key_1", SigningKeyRevision: 1, SignatureDigest: strings.Repeat("f", 64), State: "active"}
	attestation.ReleasedFieldsDigest, _ = STRIDEContractDigest(attestation.ReleasedFields)
	if err := attestation.Validate(); err != nil {
		t.Fatalf("valid attestation rejected: %v", err)
	}
	opaque := attestation
	opaque.VerificationTier = "organization_verified_opaque"
	if opaque.Validate() == nil {
		t.Fatal("identifying outcome field accepted in opaque tier")
	}
	noApproval := attestation
	noApproval.ReleasedFields[0].ApprovalRefs = nil
	if noApproval.Validate() == nil {
		t.Fatal("released field without exact approval accepted")
	}
}

func TestPublishedClaimAndNetworkProjectionStayPersonControlled(t *testing.T) {
	publication := PublishedContributionClaim{Header: contributionNetworkHeader(STRIDEContractPublishedContributionClaim, "published_1", STRIDEGlobalPersonTenant), SubjectPersonID: "person_1",
		NarrativeDigest: strings.Repeat("c", 64), Attestations: []STRIDEReference{contributionNetworkRef(STRIDEContractContributionAttestation, "attestation_1")}, ReleasedFieldsDigest: strings.Repeat("d", 64),
		Visibility: "signed_in_network", Controller: contributionNetworkController("person_1"), State: "published", StateChangedAt: contributionNetworkTestTime}
	if err := publication.Validate(); err != nil {
		t.Fatalf("valid publication rejected: %v", err)
	}
	if !PublishedContributionTransitionAllowed("published", "withdrawn") || PublishedContributionTransitionAllowed("withdrawn", "published") {
		t.Fatal("publication can be revived without new approval")
	}
	draft := publication
	draft.State = "draft"
	if draft.Validate() == nil {
		t.Fatal("discoverable draft accepted")
	}

	claimRef := contributionNetworkRef(STRIDEContractPublishedContributionClaim, "published_1")
	projection := NetworkProfileProjection{Header: contributionNetworkHeader(STRIDEContractNetworkProfileProjection, "projection_1", STRIDEGlobalPersonTenant), SubjectPersonID: "person_1", Publication: claimRef,
		Fields:          []NetworkPublishedField{{FieldKey: "contribution_role", ValueDigest: strings.Repeat("e", 64), EvidenceLabel: "organization_verified_redacted", Claim: &claimRef}},
		Discoverability: "signed_in_network", PurgeGeneration: 1, Controller: contributionNetworkController("person_1"), State: "published", StateChangedAt: contributionNetworkTestTime}
	projection.FieldsDigest, _ = STRIDEContractDigest(projection.Fields)
	if err := projection.Validate(); err != nil {
		t.Fatalf("valid projection rejected: %v", err)
	}
	private := projection
	private.Fields = append([]NetworkPublishedField(nil), projection.Fields...)
	private.Fields[0].FieldKey = "mymind"
	if private.Validate() == nil {
		t.Fatal("MyMind field accepted in network projection")
	}
	scored := projection
	scored.Fields = append([]NetworkPublishedField(nil), projection.Fields...)
	scored.Fields[0].FieldKey = "productivity_score"
	if scored.Validate() == nil {
		t.Fatal("score field accepted in network projection")
	}
	paused := projection
	paused.State = "paused"
	if paused.Validate() == nil {
		t.Fatal("discoverable paused projection accepted")
	}
	visible := projection
	visible.Fields = []NetworkPublishedField{{FieldKey: "contribution_role", ValueDigest: sha256Hex([]byte(`"operator"`)), VisibleValue: json.RawMessage(`"operator"`), EvidenceLabel: "organization_verified_redacted", Claim: &claimRef}}
	visible.FieldsDigest, _ = STRIDEContractDigest(visible.Fields)
	if visible.Validate() != nil {
		t.Fatal("bounded public visible field rejected")
	}
	mismatch := visible
	mismatch.Fields = append([]NetworkPublishedField(nil), visible.Fields...)
	mismatch.Fields[0].VisibleValue = json.RawMessage(`"forged"`)
	mismatch.FieldsDigest, _ = STRIDEContractDigest(mismatch.Fields)
	if mismatch.Validate() == nil {
		t.Fatal("visible field digest mismatch accepted")
	}
	nested := visible
	nested.Fields = append([]NetworkPublishedField(nil), visible.Fields...)
	nested.Fields[0].VisibleValue = json.RawMessage(`{"email":"private@example.com"}`)
	nested.Fields[0].ValueDigest = sha256Hex(nested.Fields[0].VisibleValue)
	nested.FieldsDigest, _ = STRIDEContractDigest(nested.Fields)
	if nested.Validate() == nil {
		t.Fatal("nested private visible field accepted")
	}
	off := projection
	off.State, off.Discoverability = "off", "unlisted"
	if off.Validate() != nil {
		t.Fatal("private non-destructive off state rejected")
	}
}

func TestAgentInfluenceRequiresAdoptionReviewAndOutcome(t *testing.T) {
	receipt := AgentInfluenceReceipt{Header: contributionNetworkHeader(STRIDEContractAgentInfluenceReceipt, "influence_1", "org_1"), OrganizationID: "org_1", SubjectPersonID: "person_1",
		AgentProfile: contributionNetworkRef(STRIDEContractAgentCoreProfile, "profile_1"), RuntimeRevision: contributionNetworkRef(STRIDEContractAgentCapabilityManifest, "runtime_1"), ModelRevision: contributionNetworkRef(STRIDEContractKnowledgeAssertion, "model_1"),
		AgentRun: contributionNetworkRef(STRIDEContractWorkRun, "run_1"), AgentOutput: contributionNetworkRef(STRIDEContractOutcome, "agent_output_1"), HumanInteraction: contributionNetworkRef(STRIDEContractConversationEvent, "interaction_1"),
		HumanAdoption: contributionNetworkRef(STRIDEContractKnowledgeAssertion, "adoption_1"), Outcome: contributionNetworkRef(STRIDEContractOutcome, "outcome_1"), Reviewer: contributionNetworkController("reviewer_1"), State: "verified"}
	if err := receipt.Validate(); err != nil {
		t.Fatalf("valid influence receipt rejected: %v", err)
	}
	receipt.HumanAdoption = STRIDEReference{}
	if receipt.Validate() == nil {
		t.Fatal("agent self-output accepted without human adoption")
	}
}

func validSearchGrant() TalentSearchGrant {
	return TalentSearchGrant{Header: contributionNetworkHeader(STRIDEContractTalentSearchGrant, "grant_1", "org_1"), OrganizationID: "org_1", MembershipID: "membership_1", MembershipRevision: 1,
		SearcherPersonID: "searcher_1", CapabilityAdministrator: contributionNetworkController("admin_1"), PolicyRevision: 1, State: "active", GrantedAt: contributionNetworkTestTime, ExpiresAt: contributionNetworkTestTime.Add(time.Hour)}
}

func TestTalentSearchIsVisibleExplainableAndPolicyChecked(t *testing.T) {
	grant := validSearchGrant()
	if err := grant.Validate(); err != nil {
		t.Fatalf("valid grant rejected: %v", err)
	}
	receipt := NetworkSearchReceipt{Header: contributionNetworkHeader(STRIDEContractNetworkSearchReceipt, "search_1", "org_1"), OrganizationID: "org_1", Grant: contributionNetworkRef(STRIDEContractTalentSearchGrant, "grant_1"),
		OriginalQueryDigest: strings.Repeat("c", 64), PolicyRevision: 1, PolicyVerdict: "allow", PolicyReasonCodes: []string{"allowed_work_evidence"},
		StructuredFilters: []NetworkSearchFilter{{Field: "contribution_role", Operation: "equals", VisibleValue: "builder", ValueDigest: sha256Hex([]byte("builder"))}}, InterpretationConfirmed: true,
		Ordering: []string{"declared_query_match", "evidence_coverage", "freshness_bucket", "privacy_shuffle"}, Results: []NetworkSearchResultReason{{Projection: contributionNetworkRef(STRIDEContractNetworkProfileProjection, "projection_1"), Why: []string{"Matched published builder contribution"}, Unknown: []string{"Private source details are unavailable"}}}, SearchedAt: contributionNetworkTestTime}
	if err := receipt.Validate(); err != nil {
		t.Fatalf("valid search receipt rejected: %v", err)
	}
	hiddenRank := receipt
	hiddenRank.Ordering = []string{"quality_score"}
	if hiddenRank.Validate() == nil {
		t.Fatal("hidden quality ordering accepted")
	}
	proxy := receipt
	proxy.StructuredFilters = append([]NetworkSearchFilter(nil), receipt.StructuredFilters...)
	proxy.StructuredFilters[0].VisibleValue = "young culture fit"
	if proxy.Validate() == nil {
		t.Fatal("prohibited proxy query accepted")
	}
	noExplanation := receipt
	noExplanation.Results = append([]NetworkSearchResultReason(nil), receipt.Results...)
	noExplanation.Results[0].Unknown = nil
	if noExplanation.Validate() == nil {
		t.Fatal("result without unknown disclosure accepted")
	}
	rejected := receipt
	rejected.PolicyVerdict = "reject"
	if rejected.Validate() == nil {
		t.Fatal("rejected query retrieved results")
	}
	rejected.StructuredFilters = nil
	rejected.Results = nil
	rejected.Ordering = []string{"privacy_shuffle"}
	if err := rejected.Validate(); err != nil {
		t.Fatalf("body-minimized rejected receipt rejected: %v", err)
	}
}

func TestContactBlockAndPurgeRemainFailClosed(t *testing.T) {
	contact := ContactRequest{Header: contributionNetworkHeader(STRIDEContractContactRequest, "contact_1", "org_1"), SenderOrganizationID: "org_1", SenderPersonID: "sender_1", RecipientPersonID: "person_1",
		RecipientProjection: contributionNetworkRef(STRIDEContractNetworkProfileProjection, "projection_1"), Purpose: "recruiting", NoteDigest: strings.Repeat("c", 64), CollaborationType: "recruiting", State: "pending", ExpiresAt: contributionNetworkTestTime.Add(time.Hour), StateChangedAt: contributionNetworkTestTime}
	if err := contact.Validate(); err != nil {
		t.Fatalf("valid pending contact rejected: %v", err)
	}
	leaky := contact
	leaky.AcceptedChannelDigest = strings.Repeat("d", 64)
	if leaky.Validate() == nil {
		t.Fatal("contact channel disclosed before acceptance")
	}

	block := NetworkBlock{Header: contributionNetworkHeader(STRIDEContractNetworkBlock, "block_1", STRIDEGlobalPersonTenant), BlockerPersonID: "person_1", BlockedOrganizationID: "org_1", Controller: contributionNetworkController("person_1"), State: "active", StateChangedAt: contributionNetworkTestTime}
	if err := block.Validate(); err != nil {
		t.Fatalf("valid block rejected: %v", err)
	}
	broad := block
	broad.BlockedPersonID = "sender_1"
	if broad.Validate() == nil {
		t.Fatal("ambiguous person and organization block accepted")
	}

	completed := contributionNetworkTestTime.Add(time.Minute)
	stores := make([]PurgeStoreResult, 0, len(contributionPurgeStores))
	for _, store := range contributionPurgeStores {
		stores = append(stores, PurgeStoreResult{Store: store, State: "completed", AttemptCount: 1, CompletedAt: &completed})
	}
	purge := DerivedPurgeReceipt{Header: contributionNetworkHeader(STRIDEContractDerivedPurgeReceipt, "purge_1", STRIDEGlobalPersonTenant), SubjectPersonID: "person_1", Trigger: contributionNetworkRef(STRIDEContractPublishedContributionClaim, "published_1"),
		PurgeGeneration: 2, AffectedFieldsDigest: strings.Repeat("e", 64), Stores: stores, EligibilityFencedAt: contributionNetworkTestTime, RecordedAt: completed, State: "completed"}
	if err := purge.Validate(); err != nil {
		t.Fatalf("valid purge rejected: %v", err)
	}
	incomplete := purge
	incomplete.Stores[0].State = "queued"
	incomplete.Stores[0].CompletedAt = nil
	if incomplete.Validate() == nil {
		t.Fatal("completed purge accepted with queued store")
	}
	missingStore := purge
	missingStore.Stores = missingStore.Stores[:len(missingStore.Stores)-1]
	if missingStore.Validate() == nil {
		t.Fatal("purge accepted without every required derived store")
	}
}
