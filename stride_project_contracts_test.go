package main

import (
	"testing"
	"time"
)

func projectContractFixture() Project {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	header := strideTestHeader(STRIDEContractProject, "project_stride")
	header.CreatedAt = now
	return Project{
		Header: header, ProjectID: header.ID, OrganizationID: header.TenantID,
		Title: "STRIDE", Aliases: []string{"Network where work happens"}, Lifecycle: "active",
		RetentionPolicy: "organization_default", ControllerMemberships: []STRIDEReference{strideTestRef(STRIDEContractOrganizationMembership, "member_aj")},
		Audience:    STRIDEAudience{Visibility: "project", Principals: []string{"person_aj"}},
		ACLRevision: 1, CreatorPersonID: "person_aj", CreatedAt: now, UpdatedAt: now,
	}
}

func projectAssociationFixture() ProjectAssociation {
	now := time.Date(2026, 8, 12, 18, 1, 0, 0, time.UTC)
	header := strideTestHeader(STRIDEContractProjectAssociation, "association_1")
	header.CreatedAt = now
	return ProjectAssociation{
		Header: header, Project: strideTestRef(STRIDEContractProject, "project_stride"),
		Subject: strideTestRef(STRIDEContractConversationEvent, "turn_1"), SourceRefs: []STRIDEReference{strideTestRef(STRIDEContractConversationEvent, "turn_1")}, SourceAuthorityReceiptID: "source_authority_receipt_1",
		EvidenceCoverageDigest: strideTestDigest("c"), State: "proposed", Basis: "suggested", ClassifierRevision: "project_linker_v1", Confidence: .92,
		ActorPersonID: "person_aj", ActorMembershipID: "member_aj", ActorMembershipRevision: 1,
		SessionSubjectDigest: strideTestDigest("d"), SessionRevision: 1, AuthorityGeneration: 1,
		SourceAudience: STRIDEAudience{Visibility: "private", Principals: []string{"person_aj"}}, SourceACLRevision: 1, SourceACLDigest: strideTestDigest("e"),
		ConsentRevision: 1, PurgeGeneration: 1, IdempotencyKeyDigest: strideTestDigest("f"),
		ExpiresAt: func() *time.Time { value := now.Add(15 * time.Minute); return &value }(), RecordedAt: now,
	}
}

func TestProjectContractPreservesIdentityAcrossRenameAndAllowsSameName(t *testing.T) {
	first := projectContractFixture()
	if err := first.Validate(); err != nil {
		t.Fatalf("valid Project rejected: %v", err)
	}
	second := first
	second.Header.Revision = 2
	second.Header.ContentDigest = strideTestDigest("2")
	second.Header.CreatedAt = first.UpdatedAt.Add(time.Minute)
	second.Title = "STRIDE Evolution"
	second.UpdatedAt = second.Header.CreatedAt
	second.Supersedes = &STRIDEReference{ContractType: STRIDEContractProject, ID: first.ProjectID, Revision: 1, Digest: first.Header.ContentDigest}
	if err := second.Validate(); err != nil || second.ProjectID != first.ProjectID {
		t.Fatalf("valid identity-preserving rename rejected: %v", err)
	}
	sameName := first
	sameName.Header.ID = "project_stride_2"
	sameName.ProjectID = sameName.Header.ID
	if err := sameName.Validate(); err != nil {
		t.Fatalf("same-name distinct Project rejected: %v", err)
	}
}

func TestProjectContractRejectsTitleIdentityAndIllegalRevision(t *testing.T) {
	project := projectContractFixture()
	project.ProjectID = "stride-title-slug"
	if err := project.Validate(); err == nil {
		t.Fatal("Project ID diverging from immutable header identity was accepted")
	}
	project = projectContractFixture()
	project.Header.Revision = 2
	if err := project.Validate(); err == nil {
		t.Fatal("Project revision without exact supersedes reference was accepted")
	}
}

func TestProjectAssociationContractPinsAuthorityAndCorrectionLineage(t *testing.T) {
	proposed := projectAssociationFixture()
	if err := proposed.Validate(); err != nil {
		t.Fatalf("valid proposal rejected: %v", err)
	}
	confirmed := proposed
	confirmed.Header.Revision = 2
	confirmed.Header.ContentDigest = strideTestDigest("3")
	confirmed.Header.CreatedAt = proposed.RecordedAt.Add(time.Minute)
	confirmed.State = "confirmed"
	confirmed.ExpiresAt = nil
	confirmed.RecordedAt = confirmed.Header.CreatedAt
	confirmed.Supersedes = &STRIDEReference{ContractType: STRIDEContractProjectAssociation, ID: proposed.Header.ID, Revision: 1, Digest: proposed.Header.ContentDigest}
	if err := confirmed.Validate(); err != nil {
		t.Fatalf("valid confirmation rejected: %v", err)
	}
	corrected := confirmed
	corrected.Header.Revision = 3
	corrected.Header.ContentDigest = strideTestDigest("4")
	corrected.Header.CreatedAt = confirmed.RecordedAt.Add(time.Minute)
	corrected.State = "corrected"
	corrected.RecordedAt = corrected.Header.CreatedAt
	corrected.Supersedes = &STRIDEReference{ContractType: STRIDEContractProjectAssociation, ID: confirmed.Header.ID, Revision: 2, Digest: confirmed.Header.ContentDigest}
	corrected.Replacement = &STRIDEReference{ContractType: STRIDEContractProjectAssociation, ID: "association_2", Revision: 1, Digest: strideTestDigest("5")}
	if err := corrected.Validate(); err != nil {
		t.Fatalf("valid correction lineage rejected: %v", err)
	}
	corrected.Replacement = nil
	if err := corrected.Validate(); err == nil {
		t.Fatal("corrected association without replacement edge was accepted")
	}
}

func TestProjectAssociationRejectsMissingAuthorityAndExpiredProposalShape(t *testing.T) {
	association := projectAssociationFixture()
	association.SessionSubjectDigest = ""
	if err := association.Validate(); err == nil {
		t.Fatal("association without session binding was accepted")
	}
	association = projectAssociationFixture()
	association.ExpiresAt = nil
	if err := association.Validate(); err == nil {
		t.Fatal("unbounded proposed association was accepted")
	}
}

func TestProjectAssociationEventRevisionMustEqualReferencedAssociation(t *testing.T) {
	association := projectAssociationFixture()
	event := ProjectAssociationEvent{
		Header:                  strideTestHeader(STRIDEContractProjectAssociationEvent, "event_revision_mismatch"),
		Association:             STRIDEReference{ContractType: STRIDEContractProjectAssociation, ID: association.Header.ID, Revision: 1, Digest: association.Header.ContentDigest},
		Action:                  "confirm",
		ResultingState:          "confirmed",
		PriorRevision:           1,
		NewRevision:             2,
		ActorPersonID:           association.ActorPersonID,
		ActorMembershipID:       association.ActorMembershipID,
		ActorMembershipRevision: association.ActorMembershipRevision,
		SessionSubjectDigest:    association.SessionSubjectDigest,
		SessionRevision:         association.SessionRevision,
		AuthorityGeneration:     association.AuthorityGeneration,
		IdempotencyKeyDigest:    association.IdempotencyKeyDigest,
		OccurredAt:              association.RecordedAt.Add(time.Minute),
	}
	event.Header.CreatedAt = event.OccurredAt
	if err := event.Validate(); err == nil {
		t.Fatal("event new revision diverging from referenced association revision was accepted")
	}
}
