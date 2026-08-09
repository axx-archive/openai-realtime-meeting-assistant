package main

import (
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var contributionAuthorityTime = time.Date(2026, 8, 8, 18, 0, 0, 0, time.UTC)

func authorityDigest(seed string) string { return sha256Hex([]byte(seed)) }
func authorityAssertion(grant ContributionAuthorityGrant, revision int64, key string, at time.Time) ContributionAuthorityAssertion {
	return ContributionAuthorityAssertion{GrantID: grant.ID, Controller: grant.Controller, ExpectedRevision: revision, IdempotencyKeyDigest: authorityDigest(key), At: at}
}

func authorityGrant(id, role, org, person, party, principal string) ContributionAuthorityGrant {
	return ContributionAuthorityGrant{ID: id, Role: role, OrganizationID: org, PersonID: person, PartyID: party,
		Controller: STRIDEControllerRevision{PrincipalID: principal, AuthorityID: "authority_" + id, AuthorityRevision: 1, PolicyRevision: 1}}
}

type contributionAuthorityFixture struct {
	service                                                *ContributionAuthorityService
	org, subject, publisher, party, issuer, outcome, drift ContributionAuthorityGrant
}

func newContributionAuthorityFixture(t *testing.T) contributionAuthorityFixture {
	t.Helper()
	fixture := contributionAuthorityFixture{
		org:       authorityGrant("grant_org", "organization_reviewer", "org_1", "", "", "reviewer_1"),
		subject:   authorityGrant("grant_subject", "subject", "", "person_1", "", "person_1"),
		publisher: authorityGrant("grant_publish", "person_publisher", "", "person_1", "", "person_1"),
		party:     authorityGrant("grant_party", "named_party", "", "", "customer_1", "customer_1"),
		issuer:    authorityGrant("grant_issuer", "signing_issuer", "org_1", "", "", "issuer_1"),
		outcome:   authorityGrant("grant_outcome", "outcome_reviewer", "org_1", "", "", "outcome_reviewer_1"),
		drift:     authorityGrant("grant_drift", "drift_controller", "org_1", "", "", "privacy_worker_1"),
	}
	service, err := NewContributionAuthorityService([]ContributionAuthorityGrant{fixture.org, fixture.subject, fixture.publisher, fixture.party, fixture.issuer, fixture.outcome, fixture.drift})
	if err != nil {
		t.Fatal(err)
	}
	fixture.service = service
	return fixture
}

func candidateClaim() ContributionClaim {
	return ContributionClaim{Header: contributionNetworkHeader(STRIDEContractContributionClaim, "claim_authority", "org_1"), OrganizationID: "org_1", SubjectPersonID: "person_1", ContributionKind: "delivered", ProblemClass: "commerce", OutcomeClass: "reliability",
		SourceRefs: []STRIDEReference{contributionNetworkRef(STRIDEContractOutcome, "source_outcome")}, EvidenceManifestDigest: authorityDigest("manifest"), AttributionMethod: "source_observed", ACLRevision: 1, ConsentRevision: 1, PurgeGeneration: 1, PolicyRevision: 1, State: "candidate", StateChangedAt: contributionAuthorityTime}
}

func createVerifiedClaim(t *testing.T, f contributionAuthorityFixture) ContributionClaim {
	t.Helper()
	claim := candidateClaim()
	created, err := f.service.CreateClaim(claim, authorityAssertion(f.org, 0, "create", contributionAuthorityTime))
	if err != nil {
		t.Fatal(err)
	}
	reviewed, err := f.service.SubjectReview(claim.Header.ID, false, authorityAssertion(f.subject, created.Header.Revision, "subject_review", contributionAuthorityTime.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	verified, err := f.service.VerifyClaim(claim.Header.ID, authorityAssertion(f.org, reviewed.Header.Revision, "verify", contributionAuthorityTime.Add(2*time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	return verified
}

func TestContributionAuthorityClaimCASControllerAndIdempotency(t *testing.T) {
	f := newContributionAuthorityFixture(t)
	claim := candidateClaim()
	assertion := authorityAssertion(f.org, 0, "create", contributionAuthorityTime)
	created, err := f.service.CreateClaim(claim, assertion)
	if err != nil {
		t.Fatal(err)
	}
	claim.SourceRefs[0].ID = "mutated_input"
	created.SourceRefs[0].ID = "mutated_return"
	replayed, err := f.service.CreateClaim(claim, assertion)
	if !errorsIs(err, ErrContributionAuthorityConflict) {
		t.Fatalf("mutated request reused idempotency key: %v", err)
	}
	claim = candidateClaim()
	replayed, err = f.service.CreateClaim(claim, assertion)
	if err != nil || replayed.SourceRefs[0].ID != "source_outcome" {
		t.Fatalf("create replay mismatch: %#v %v", replayed, err)
	}
	wrong := authorityAssertion(f.drift, created.Header.Revision, "wrong", contributionAuthorityTime.Add(time.Minute))
	if _, err := f.service.SubjectReview(claim.Header.ID, false, wrong); !errorsIs(err, ErrContributionAuthorityDenied) {
		t.Fatalf("wrong controller: %v", err)
	}
	reviewAssertion := authorityAssertion(f.subject, created.Header.Revision, "review", contributionAuthorityTime.Add(time.Minute))
	reviewed, err := f.service.SubjectReview(claim.Header.ID, false, reviewAssertion)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := f.service.SubjectReview(claim.Header.ID, false, reviewAssertion); err != nil || !reflect.DeepEqual(replay, reviewed) {
		t.Fatalf("subject review replay failed: %v", err)
	}
	stale := authorityAssertion(f.org, created.Header.Revision, "stale_verify", contributionAuthorityTime.Add(2*time.Minute))
	if _, err := f.service.VerifyClaim(claim.Header.ID, stale); !errorsIs(err, ErrContributionAuthorityConflict) {
		t.Fatalf("stale CAS accepted: %v", err)
	}
	verified, err := f.service.VerifyClaim(claim.Header.ID, authorityAssertion(f.org, reviewed.Header.Revision, "verify", contributionAuthorityTime.Add(2*time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if verified.State != "verified" || verified.SubjectReview == nil || verified.OrganizationReview == nil {
		t.Fatal("verified claim lost controllers")
	}
	if got := f.service.claimHistory[authorityHistoryKey(claim.Header.ID, 1)]; got.State != "candidate" || got.Header.Revision != 1 || verified.Supersedes == nil || verified.Supersedes.Revision != 2 {
		t.Fatalf("immutable history/current pointer broken: first=%#v current=%#v", got, verified)
	}
}

func TestContributionAuthorityRejectsIncompleteApprovalSetAndForgedSupersession(t *testing.T) {
	f := newContributionAuthorityFixture(t)
	claim := createVerifiedClaim(t, f)
	attHeader := contributionNetworkHeader(STRIDEContractContributionAttestation, "attestation_incomplete", "org_1")
	field := ReleasedContributionField{FieldKey: "outcome", ValueDigest: authorityDigest("value"), ApprovalRefs: []STRIDEReference{contributionNetworkRef(STRIDEContractFieldReleaseApproval, "missing_approval")}}
	att := ContributionAttestation{Header: attHeader, OrganizationID: "org_1", SubjectPersonID: "person_1", Claim: refForHeader(claim.Header), EvidenceManifestDigest: claim.EvidenceManifestDigest, ReleasedFields: []ReleasedContributionField{field}, VerificationTier: "organization_verified_redacted", Issuer: f.issuer.Controller, SigningKeyID: "key_1", SigningKeyRevision: 1, SignatureDigest: authorityDigest("sig"), State: "active"}
	att.ReleasedFieldsDigest, _ = STRIDEContractDigest(att.ReleasedFields)
	if _, err := f.service.IssueAttestation(att, authorityAssertion(f.issuer, 0, "incomplete", contributionAuthorityTime.Add(4*time.Minute))); !errorsIs(err, ErrContributionAuthorityDenied) {
		t.Fatalf("incomplete subject/org/named-party approval set admitted: %v", err)
	}
	forged := claim
	forged.Header = contributionNetworkHeader(STRIDEContractContributionClaim, "claim_forged", "org_1")
	forged.Supersedes = nil
	forged.OrganizationReview = &f.drift.Controller
	if _, err := f.service.SupersedeClaim(claim.Header.ID, forged, authorityAssertion(f.org, claim.Header.Revision, "forged", contributionAuthorityTime.Add(5*time.Minute))); !errorsIs(err, ErrContributionAuthorityDenied) {
		t.Fatalf("forged replacement controller admitted: %v", err)
	}
}

func issuePublishedContribution(t *testing.T, f contributionAuthorityFixture, claim ContributionClaim) (ContributionAttestation, PublishedContributionClaim) {
	t.Helper()
	attHeader := contributionNetworkHeader(STRIDEContractContributionAttestation, "attestation_authority", "org_1")
	attRef := refForHeader(attHeader)
	approval := FieldReleaseApproval{Header: contributionNetworkHeader(STRIDEContractFieldReleaseApproval, "approval_authority", "org_1"), OrganizationID: "org_1", SubjectPersonID: "person_1", Attestation: attRef,
		FieldKey: "outcome", FieldValueDigest: sha256Hex([]byte(`"redacted outcome"`)), Source: claim.SourceRefs[0], SourceConsentRevision: claim.ConsentRevision, SourceACLRevision: claim.ACLRevision, SourcePurgeGeneration: claim.PurgeGeneration,
		Visibility: "signed_in_network", RequiredPartyIDs: []string{"customer_1"}, ApproverRole: "named_party", ApproverPartyID: "customer_1", Controller: f.party.Controller, State: "pending", StateChangedAt: contributionAuthorityTime.Add(3 * time.Minute)}
	subjectApproval := approval
	subjectApproval.Header = contributionNetworkHeader(STRIDEContractFieldReleaseApproval, "approval_subject", "org_1")
	subjectApproval.RequiredPartyIDs, subjectApproval.ApproverRole, subjectApproval.ApproverPartyID, subjectApproval.Controller = nil, "subject", "", f.subject.Controller
	orgApproval := approval
	orgApproval.Header = contributionNetworkHeader(STRIDEContractFieldReleaseApproval, "approval_org", "org_1")
	orgApproval.RequiredPartyIDs, orgApproval.ApproverRole, orgApproval.ApproverPartyID, orgApproval.Controller = nil, "organization", "", f.org.Controller
	approvals := []FieldReleaseApproval{approval, subjectApproval, orgApproval}
	controllers := []ContributionAuthorityGrant{f.party, f.subject, f.org}
	refs := make([]STRIDEReference, 0, len(approvals))
	for i, pending := range approvals {
		if _, err := f.service.PutFieldApproval(pending, authorityAssertion(f.org, 0, "put_approval_"+pending.Header.ID, contributionAuthorityTime.Add(3*time.Minute))); err != nil {
			t.Fatal(err)
		}
		approved, effects, err := f.service.DecideFieldApproval(pending.Header.ID, "approved", authorityAssertion(controllers[i], pending.Header.Revision, "approve_"+pending.Header.ID, contributionAuthorityTime.Add(4*time.Minute)))
		if err != nil || len(effects) != 0 {
			t.Fatalf("approve: %v effects=%d", err, len(effects))
		}
		refs = append(refs, refForHeader(approved.Header))
	}
	field := ReleasedContributionField{FieldKey: "outcome", ValueDigest: approval.FieldValueDigest, ApprovalRefs: refs}
	attestation := ContributionAttestation{Header: attHeader, OrganizationID: "org_1", SubjectPersonID: "person_1", Claim: refForHeader(claim.Header), EvidenceManifestDigest: claim.EvidenceManifestDigest, ReleasedFields: []ReleasedContributionField{field}, VerificationTier: "organization_verified_redacted", Issuer: f.issuer.Controller, SigningKeyID: "key_1", SigningKeyRevision: 1, SignatureDigest: authorityDigest("signature"), State: "active"}
	attestation.ReleasedFieldsDigest, _ = STRIDEContractDigest(attestation.ReleasedFields)
	issued, err := f.service.IssueAttestation(attestation, authorityAssertion(f.issuer, 0, "issue", contributionAuthorityTime.Add(5*time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	publication := PublishedContributionClaim{Header: contributionNetworkHeader(STRIDEContractPublishedContributionClaim, "publication_authority", STRIDEGlobalPersonTenant), SubjectPersonID: "person_1", NarrativeDigest: authorityDigest("public narrative"), Attestations: []STRIDEReference{refForHeader(issued.Header)}, ReleasedFieldsDigest: issued.ReleasedFieldsDigest, Visibility: "signed_in_network", Controller: f.publisher.Controller, State: "published", StateChangedAt: contributionAuthorityTime.Add(6 * time.Minute)}
	published, err := f.service.Publish(publication, authorityAssertion(f.publisher, 0, "publish", contributionAuthorityTime.Add(6*time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	return issued, published
}

func TestContributionAuthorityApprovalAttestationPublicationAndDriftFence(t *testing.T) {
	f := newContributionAuthorityFixture(t)
	claim := createVerifiedClaim(t, f)
	attestation, publication := issuePublishedContribution(t, f, claim)
	if !f.service.FieldEligible(publication.Header.ID, "outcome") {
		t.Fatal("published field unexpectedly fenced")
	}
	staleGeneration := ContributionAuthorityDrift{Kind: "consent", OrganizationID: "org_1", Source: &claim.SourceRefs[0], NewConsentRevision: 2, NewPurgeGeneration: claim.PurgeGeneration, ReasonDigest: authorityDigest("stale generation")}
	if _, err := f.service.FenceDrift(staleGeneration, authorityAssertion(f.drift, 0, "stale_generation", contributionAuthorityTime.Add(7*time.Minute))); !errorsIs(err, ErrContributionAuthorityConflict) {
		t.Fatalf("non-monotonic drift purge generation accepted: %v", err)
	}
	drift := ContributionAuthorityDrift{Kind: "consent", OrganizationID: "org_1", Source: &claim.SourceRefs[0], NewConsentRevision: 2, NewPurgeGeneration: 2, ReasonDigest: authorityDigest("consent withdrawn")}
	effects, err := f.service.FenceDrift(drift, authorityAssertion(f.drift, 0, "drift", contributionAuthorityTime.Add(7*time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 1 || len(effects[0].AffectedFields) != 1 || effects[0].AffectedFields[0] != "outcome" {
		t.Fatalf("unexpected fence effects: %#v", effects)
	}
	if f.service.FieldEligible(publication.Header.ID, "outcome") {
		t.Fatal("drift acknowledgement returned before synchronous fence")
	}
	republish := publication
	republish.Header = contributionNetworkHeader(STRIDEContractPublishedContributionClaim, "publication_after_drift", STRIDEGlobalPersonTenant)
	republish.Attestations = []STRIDEReference{refForHeader(attestation.Header)}
	if _, err := f.service.Publish(republish, authorityAssertion(f.publisher, 0, "republish_after_drift", contributionAuthorityTime.Add(8*time.Minute))); !errorsIs(err, ErrContributionAuthorityDenied) {
		t.Fatalf("publication re-admitted after claim drift: %v", err)
	}
	effects[0].PurgeReceipt.Stores[0].State = "completed"
	queue := f.service.PurgeQueue()
	if len(queue) != 1 || queue[0].Validate() != nil || queue[0].State != "queued" || queue[0].AffectedFieldsDigest != effects[0].AffectedFieldsDigest {
		t.Fatalf("invalid purge queue: %#v", queue)
	}
}

func TestContributionAuthorityNamedPartyWithdrawalFencesExactField(t *testing.T) {
	f := newContributionAuthorityFixture(t)
	claim := createVerifiedClaim(t, f)
	_, publication := issuePublishedContribution(t, f, claim)
	approval := f.service.approvals["approval_authority"]
	_, effects, err := f.service.DecideFieldApproval(approval.Header.ID, "withdrawn", authorityAssertion(f.party, approval.Header.Revision, "withdraw", contributionAuthorityTime.Add(8*time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 1 || effects[0].AffectedFields[0] != "outcome" || f.service.FieldEligible(publication.Header.ID, "outcome") {
		t.Fatalf("withdrawal did not fence exact field: %#v", effects)
	}
}

func TestContributionAuthorityPublicationWithdrawalAndAgentInfluence(t *testing.T) {
	f := newContributionAuthorityFixture(t)
	claim := createVerifiedClaim(t, f)
	_, publication := issuePublishedContribution(t, f, claim)
	withdrawn, effects, err := f.service.WithdrawPublication(publication.Header.ID, authorityAssertion(f.publisher, publication.Header.Revision, "unpublish", contributionAuthorityTime.Add(9*time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if withdrawn.State != "withdrawn" || withdrawn.Visibility != "private" || len(effects) != 1 || f.service.FieldEligible(publication.Header.ID, "outcome") {
		t.Fatal("person withdrawal failed closed")
	}
	receipt := AgentInfluenceReceipt{Header: contributionNetworkHeader(STRIDEContractAgentInfluenceReceipt, "influence_authority", "org_1"), OrganizationID: "org_1", SubjectPersonID: "person_1",
		AgentProfile: contributionNetworkRef(STRIDEContractAgentCoreProfile, "agent_profile"), RuntimeRevision: contributionNetworkRef(STRIDEContractAgentCapabilityManifest, "runtime"), ModelRevision: contributionNetworkRef(STRIDEContractKnowledgeAssertion, "model"), AgentRun: contributionNetworkRef(STRIDEContractWorkRun, "run"), AgentOutput: contributionNetworkRef(STRIDEContractOutcome, "agent_output"), HumanInteraction: contributionNetworkRef(STRIDEContractConversationEvent, "human_interaction"), HumanAdoption: contributionNetworkRef(STRIDEContractKnowledgeAssertion, "human_adoption"), Outcome: contributionNetworkRef(STRIDEContractOutcome, "work_outcome"), Reviewer: f.outcome.Controller, State: "verified"}
	if _, err := f.service.AdmitAgentInfluence(receipt, authorityAssertion(f.outcome, 0, "influence", contributionAuthorityTime.Add(10*time.Minute))); err != nil {
		t.Fatal(err)
	}
	missing := receipt
	missing.Header.ID = "influence_missing"
	missing.Header.ContentDigest = authorityDigest("missing")
	missing.HumanAdoption = STRIDEReference{}
	if _, err := f.service.AdmitAgentInfluence(missing, authorityAssertion(f.outcome, 0, "missing", contributionAuthorityTime.Add(10*time.Minute))); !errorsIs(err, ErrContributionAuthorityInvalid) {
		t.Fatalf("missing adoption admitted: %v", err)
	}
}

func TestContributionAuthorityConcurrentCASAllowsOneReview(t *testing.T) {
	f := newContributionAuthorityFixture(t)
	claim := candidateClaim()
	created, err := f.service.CreateClaim(claim, authorityAssertion(f.org, 0, "create_race", contributionAuthorityTime))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := f.service.SubjectReview(claim.Header.ID, false, authorityAssertion(f.subject, created.Header.Revision, "race_"+string(rune('a'+i)), contributionAuthorityTime.Add(time.Minute)))
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errorsIs(err, ErrContributionAuthorityConflict) {
			conflicts++
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("CAS outcomes success=%d conflicts=%d", success, conflicts)
	}
}

func errorsIs(err, target error) bool {
	return err != nil && (err == target || strings.Contains(err.Error(), target.Error()))
}
