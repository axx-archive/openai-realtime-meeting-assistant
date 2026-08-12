package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestProjectRecordProjectionListsConfirmedAndReauthorizesEveryRead(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	authority, fence := projectAuthorityFixture("bonfire", "person_aj", "member_aj", now)
	resolver := projectSourceResolverFixture()
	domain := NewProjectAuthorityService(fence, resolver)
	projection := NewProjectProjectionService(domain)
	project, binding := projectAuthorityProject(authority, "project_one", "Roadmap", "thread_one", now.Add(2*time.Minute))
	if err := domain.CreateProject(authority, project, binding, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	proposed, proposeEvent := projectAuthorityAssociation(authority, project, "association_one", now.Add(3*time.Minute), 'b')
	if err := domain.ProposeAssociation(authority, proposed, proposeEvent); err != nil {
		t.Fatal(err)
	}
	if err := projection.ApplyCurrentAssociation(authority, proposed.Header.ID); err != nil {
		t.Fatal(err)
	}
	record, err := projection.ReadProjectRecord(authority, project.ProjectID)
	if err != nil || len(record.Nodes) != 0 {
		t.Fatalf("proposal leaked into Project Record: %#v %v", record, err)
	}
	confirmed := proposed
	confirmed.Header = organizationTestHeader("bonfire", proposed.Header.ID, 2, STRIDEContractProjectAssociation, 'c', now.Add(4*time.Minute))
	confirmed.State, confirmed.ExpiresAt, confirmed.RecordedAt = "confirmed", nil, confirmed.Header.CreatedAt
	confirmed.IdempotencyKeyDigest = strings.Repeat("c", 64)
	confirmed.Supersedes = &STRIDEReference{ContractType: STRIDEContractProjectAssociation, ID: proposed.Header.ID, Revision: 1, Digest: proposed.Header.ContentDigest}
	event := ProjectAssociationEvent{Header: organizationTestHeader("bonfire", "event_confirm", 1, STRIDEContractProjectAssociationEvent, 'c', confirmed.RecordedAt), Association: projectAssociationRef(confirmed), Action: "confirm", ResultingState: "confirmed", PriorRevision: 1, NewRevision: 2, ActorPersonID: authority.Person.Header.ID, ActorMembershipID: authority.Membership.Header.ID, ActorMembershipRevision: 1, SessionSubjectDigest: authority.ActiveSession.SessionSubjectDigest, SessionRevision: 1, AuthorityGeneration: 1, IdempotencyKeyDigest: confirmed.IdempotencyKeyDigest, OccurredAt: confirmed.RecordedAt}
	authority.At = now.Add(4 * time.Minute)
	if err := domain.TransitionAssociation(authority, 1, confirmed, event); err != nil {
		t.Fatal(err)
	}
	if err := projection.ApplyCurrentAssociation(authority, confirmed.Header.ID); err != nil {
		t.Fatal(err)
	}
	record, err = projection.ReadProjectRecord(authority, project.ProjectID)
	if err != nil || len(record.Nodes) != 1 || record.Nodes[0].Subject != confirmed.Subject {
		t.Fatalf("confirmed node missing: %#v %v", record, err)
	}
	resolver.snapshot.PurgeGeneration++
	record, err = projection.ReadProjectRecord(authority, project.ProjectID)
	if err != nil || len(record.Nodes) != 0 {
		t.Fatalf("stale source node metadata/count leaked: %#v %v", record, err)
	}
}

func TestProjectRecordProjectionCorrectionUnlistsOldAndListsReplacement(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	authority, fence := projectAuthorityFixture("bonfire", "person_aj", "member_aj", now)
	resolver := projectSourceResolverFixture()
	domain := NewProjectAuthorityService(fence, resolver)
	projection := NewProjectProjectionService(domain)
	first, firstBinding := projectAuthorityProject(authority, "project_one", "Roadmap", "thread_one", now.Add(2*time.Minute))
	second, secondBinding := projectAuthorityProject(authority, "project_two", "Launch", "thread_two", now.Add(3*time.Minute))
	if err := domain.CreateProject(authority, first, firstBinding, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := domain.CreateProject(authority, second, secondBinding, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	proposed, proposedEvent := projectAuthorityAssociation(authority, first, "association_one", now.Add(4*time.Minute), 'c')
	if err := domain.ProposeAssociation(authority, proposed, proposedEvent); err != nil {
		t.Fatal(err)
	}
	if err := projection.ApplyCurrentAssociation(authority, proposed.Header.ID); err != nil {
		t.Fatal(err)
	}
	confirmed := proposed
	confirmed.Header = organizationTestHeader("bonfire", proposed.Header.ID, 2, STRIDEContractProjectAssociation, 'd', now.Add(5*time.Minute))
	confirmed.State, confirmed.ExpiresAt, confirmed.RecordedAt = "confirmed", nil, confirmed.Header.CreatedAt
	confirmed.IdempotencyKeyDigest = strings.Repeat("d", 64)
	confirmed.Supersedes = &STRIDEReference{ContractType: STRIDEContractProjectAssociation, ID: proposed.Header.ID, Revision: 1, Digest: proposed.Header.ContentDigest}
	confirmEvent := ProjectAssociationEvent{Header: organizationTestHeader("bonfire", "event_confirm", 1, STRIDEContractProjectAssociationEvent, 'd', confirmed.RecordedAt), Association: projectAssociationRef(confirmed), Action: "confirm", ResultingState: "confirmed", PriorRevision: 1, NewRevision: 2, ActorPersonID: authority.Person.Header.ID, ActorMembershipID: authority.Membership.Header.ID, ActorMembershipRevision: 1, SessionSubjectDigest: authority.ActiveSession.SessionSubjectDigest, SessionRevision: 1, AuthorityGeneration: 1, IdempotencyKeyDigest: confirmed.IdempotencyKeyDigest, OccurredAt: confirmed.RecordedAt}
	authority.At = now.Add(5 * time.Minute)
	if err := domain.TransitionAssociation(authority, 1, confirmed, confirmEvent); err != nil {
		t.Fatal(err)
	}
	if err := projection.ApplyCurrentAssociation(authority, confirmed.Header.ID); err != nil {
		t.Fatal(err)
	}
	replacement, _ := projectAuthorityAssociation(authority, second, "association_two", now.Add(6*time.Minute), 'e')
	replacement.State, replacement.Basis, replacement.ExpiresAt = "confirmed", "selected", nil
	corrected := confirmed
	corrected.Header = organizationTestHeader("bonfire", confirmed.Header.ID, 3, STRIDEContractProjectAssociation, 'f', now.Add(6*time.Minute))
	corrected.State, corrected.RecordedAt, corrected.IdempotencyKeyDigest = "corrected", corrected.Header.CreatedAt, strings.Repeat("f", 64)
	corrected.Supersedes = &STRIDEReference{ContractType: STRIDEContractProjectAssociation, ID: confirmed.Header.ID, Revision: 2, Digest: confirmed.Header.ContentDigest}
	replacement.IdempotencyKeyDigest, replacement.RecordedAt = corrected.IdempotencyKeyDigest, corrected.RecordedAt
	corrected.Replacement = func() *STRIDEReference { ref := projectAssociationRef(replacement); return &ref }()
	correctEvent := ProjectAssociationEvent{Header: organizationTestHeader("bonfire", "event_correct", 1, STRIDEContractProjectAssociationEvent, 'f', corrected.RecordedAt), Association: projectAssociationRef(corrected), Action: "correct", ResultingState: "corrected", PriorRevision: 2, NewRevision: 3, Replacement: corrected.Replacement, ActorPersonID: authority.Person.Header.ID, ActorMembershipID: authority.Membership.Header.ID, ActorMembershipRevision: 1, SessionSubjectDigest: authority.ActiveSession.SessionSubjectDigest, SessionRevision: 1, AuthorityGeneration: 1, IdempotencyKeyDigest: corrected.IdempotencyKeyDigest, OccurredAt: corrected.RecordedAt}
	authority.At = now.Add(6 * time.Minute)
	if err := domain.CorrectAssociation(authority, 2, corrected, replacement, correctEvent); err != nil {
		t.Fatal(err)
	}
	if err := projection.ApplyCurrentAssociation(authority, corrected.Header.ID); err != nil {
		t.Fatal(err)
	}
	if err := projection.ApplyCurrentAssociation(authority, replacement.Header.ID); err != nil {
		t.Fatal(err)
	}
	oldRecord, _ := projection.ReadProjectRecord(authority, first.ProjectID)
	newRecord, _ := projection.ReadProjectRecord(authority, second.ProjectID)
	if len(oldRecord.Nodes) != 0 || len(newRecord.Nodes) != 1 || newRecord.Nodes[0].Association.ID != replacement.Header.ID {
		t.Fatalf("correction projection mismatch: old=%#v new=%#v", oldRecord, newRecord)
	}
}

func TestProjectRecordProjectionRejectsOutOfOrderAndCrossOrganizationReads(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	authority, fence := projectAuthorityFixture("bonfire", "person_aj", "member_aj", now)
	domain := NewProjectAuthorityService(fence, projectSourceResolverFixture())
	projection := NewProjectProjectionService(domain)
	project, binding := projectAuthorityProject(authority, "project_one", "Roadmap", "thread_one", now.Add(2*time.Minute))
	if err := domain.CreateProject(authority, project, binding, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	proposed, event := projectAuthorityAssociation(authority, project, "association_one", now.Add(3*time.Minute), 'b')
	if err := domain.ProposeAssociation(authority, proposed, event); err != nil {
		t.Fatal(err)
	}
	confirmed := proposed
	confirmed.Header = organizationTestHeader("bonfire", proposed.Header.ID, 2, STRIDEContractProjectAssociation, 'c', now.Add(4*time.Minute))
	confirmed.State, confirmed.ExpiresAt, confirmed.RecordedAt = "confirmed", nil, confirmed.Header.CreatedAt
	confirmed.IdempotencyKeyDigest = strings.Repeat("c", 64)
	confirmed.Supersedes = &STRIDEReference{ContractType: STRIDEContractProjectAssociation, ID: proposed.Header.ID, Revision: 1, Digest: proposed.Header.ContentDigest}
	confirmEvent := ProjectAssociationEvent{Header: organizationTestHeader("bonfire", "event_confirm", 1, STRIDEContractProjectAssociationEvent, 'c', confirmed.RecordedAt), Association: projectAssociationRef(confirmed), Action: "confirm", ResultingState: "confirmed", PriorRevision: 1, NewRevision: 2, ActorPersonID: authority.Person.Header.ID, ActorMembershipID: authority.Membership.Header.ID, ActorMembershipRevision: 1, SessionSubjectDigest: authority.ActiveSession.SessionSubjectDigest, SessionRevision: 1, AuthorityGeneration: 1, IdempotencyKeyDigest: confirmed.IdempotencyKeyDigest, OccurredAt: confirmed.RecordedAt}
	authority.At = now.Add(4 * time.Minute)
	if err := domain.TransitionAssociation(authority, 1, confirmed, confirmEvent); err != nil {
		t.Fatal(err)
	}
	if err := projection.ApplyCurrentAssociation(authority, confirmed.Header.ID); !errors.Is(err, ErrProjectProjectionConflict) {
		t.Fatalf("out-of-order current revision projected: %v", err)
	}
	other, _ := projectAuthorityFixture("other-org", "person_tyler", "member_tyler", now)
	if record, err := projection.ReadProjectRecord(other, project.ProjectID); !errors.Is(err, ErrProjectAuthorityDenied) && !errors.Is(err, ErrProjectAuthorityNotFound) || len(record.Nodes) != 0 {
		t.Fatalf("cross-org Project Record leaked: %#v %v", record, err)
	}
}

func TestProjectRecordProjectionRebuildsAfterRestartFromCurrentRevision(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	authority, fence := projectAuthorityFixture("bonfire", "person_aj", "member_aj", now)
	domain := NewProjectAuthorityService(fence, projectSourceResolverFixture())
	project, binding := projectAuthorityProject(authority, "project_one", "Roadmap", "thread_one", now.Add(2*time.Minute))
	if err := domain.CreateProject(authority, project, binding, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	proposed, event := projectAuthorityAssociation(authority, project, "association_one", now.Add(3*time.Minute), 'b')
	if err := domain.ProposeAssociation(authority, proposed, event); err != nil {
		t.Fatal(err)
	}
	confirmed := proposed
	confirmed.Header = organizationTestHeader("bonfire", proposed.Header.ID, 2, STRIDEContractProjectAssociation, 'c', now.Add(4*time.Minute))
	confirmed.State, confirmed.ExpiresAt, confirmed.RecordedAt = "confirmed", nil, confirmed.Header.CreatedAt
	confirmed.IdempotencyKeyDigest = strings.Repeat("c", 64)
	confirmed.Supersedes = &STRIDEReference{ContractType: STRIDEContractProjectAssociation, ID: proposed.Header.ID, Revision: 1, Digest: proposed.Header.ContentDigest}
	confirm := ProjectAssociationEvent{Header: organizationTestHeader("bonfire", "event_confirm", 1, STRIDEContractProjectAssociationEvent, 'c', confirmed.RecordedAt), Association: projectAssociationRef(confirmed), Action: "confirm", ResultingState: "confirmed", PriorRevision: 1, NewRevision: 2, ActorPersonID: authority.Person.Header.ID, ActorMembershipID: authority.Membership.Header.ID, ActorMembershipRevision: 1, SessionSubjectDigest: authority.ActiveSession.SessionSubjectDigest, SessionRevision: 1, AuthorityGeneration: 1, IdempotencyKeyDigest: confirmed.IdempotencyKeyDigest, OccurredAt: confirmed.RecordedAt}
	authority.At = now.Add(4 * time.Minute)
	if err := domain.TransitionAssociation(authority, 1, confirmed, confirm); err != nil {
		t.Fatal(err)
	}
	restarted := NewProjectProjectionService(domain)
	if err := restarted.RebuildProjectRecord(authority, project.ProjectID); err != nil {
		t.Fatal(err)
	}
	record, err := restarted.ReadProjectRecord(authority, project.ProjectID)
	if err != nil || len(record.Nodes) != 1 || record.Nodes[0].Association.Revision != 2 {
		t.Fatalf("restart rebuild failed: %#v %v", record, err)
	}
	// Rebuild is idempotent and does not duplicate nodes.
	if err := restarted.RebuildProjectRecord(authority, project.ProjectID); err != nil {
		t.Fatal(err)
	}
	record, _ = restarted.ReadProjectRecord(authority, project.ProjectID)
	if len(record.Nodes) != 1 {
		t.Fatalf("restart replay duplicated nodes: %#v", record.Nodes)
	}
}

func TestProjectRecordRebuildRetriesAcrossConcurrentProjectRevision(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	authority, fence := projectAuthorityFixture("bonfire", "person_aj", "member_aj", now)
	resolver := projectSourceResolverFixture()
	domain := NewProjectAuthorityService(fence, resolver)
	project, binding := projectAuthorityProject(authority, "project_one", "Roadmap", "thread_one", now.Add(2*time.Minute))
	if err := domain.CreateProject(authority, project, binding, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	proposed, event := projectAuthorityAssociation(authority, project, "association_one", now.Add(3*time.Minute), 'b')
	if err := domain.ProposeAssociation(authority, proposed, event); err != nil {
		t.Fatal(err)
	}
	resolver.batchEntered = make(chan struct{})
	resolver.batchRelease = make(chan struct{})
	projection := NewProjectProjectionService(domain)
	rebuilt := make(chan error, 1)
	go func() { rebuilt <- projection.RebuildProjectRecord(authority, project.ProjectID) }()
	select {
	case <-resolver.batchEntered:
	case <-time.After(time.Second):
		t.Fatal("rebuild never entered source snapshot")
	}
	revised := project
	revised.Header = organizationTestHeader("bonfire", project.ProjectID, 2, STRIDEContractProject, 'c', now.Add(4*time.Minute))
	revised.Title, revised.UpdatedAt = "Roadmap v2", revised.Header.CreatedAt
	revised.Supersedes = &STRIDEReference{ContractType: STRIDEContractProject, ID: project.ProjectID, Revision: 1, Digest: project.Header.ContentDigest}
	authority.At = now.Add(4 * time.Minute)
	if err := domain.ReviseProject(authority, 1, revised, strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	close(resolver.batchRelease)
	if err := <-rebuilt; err != nil {
		t.Fatalf("rebuild failed instead of retrying on high-water drift: %v", err)
	}
	record, err := projection.ReadProjectRecord(authority, project.ProjectID)
	if err != nil || record.Title != "Roadmap v2" {
		t.Fatalf("rebuild returned stale Project metadata: %#v %v", record, err)
	}
}

func TestProjectRecordReadLinearizesBeforeConcurrentSourceRevocation(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	authority, fence := projectAuthorityFixture("bonfire", "person_aj", "member_aj", now)
	resolver := projectSourceResolverFixture()
	domain := NewProjectAuthorityService(fence, resolver)
	project, binding := projectAuthorityProject(authority, "project_one", "Roadmap", "thread_one", now.Add(2*time.Minute))
	if err := domain.CreateProject(authority, project, binding, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	proposed, event := projectAuthorityAssociation(authority, project, "association_one", now.Add(3*time.Minute), 'b')
	if err := domain.ProposeAssociation(authority, proposed, event); err != nil {
		t.Fatal(err)
	}
	confirmed := proposed
	confirmed.Header = organizationTestHeader("bonfire", proposed.Header.ID, 2, STRIDEContractProjectAssociation, 'c', now.Add(4*time.Minute))
	confirmed.State, confirmed.ExpiresAt, confirmed.RecordedAt = "confirmed", nil, confirmed.Header.CreatedAt
	confirmed.IdempotencyKeyDigest = strings.Repeat("c", 64)
	confirmed.Supersedes = &STRIDEReference{ContractType: STRIDEContractProjectAssociation, ID: proposed.Header.ID, Revision: 1, Digest: proposed.Header.ContentDigest}
	confirm := ProjectAssociationEvent{Header: organizationTestHeader("bonfire", "event_confirm", 1, STRIDEContractProjectAssociationEvent, 'c', confirmed.RecordedAt), Association: projectAssociationRef(confirmed), Action: "confirm", ResultingState: "confirmed", PriorRevision: 1, NewRevision: 2, ActorPersonID: authority.Person.Header.ID, ActorMembershipID: authority.Membership.Header.ID, ActorMembershipRevision: 1, SessionSubjectDigest: authority.ActiveSession.SessionSubjectDigest, SessionRevision: 1, AuthorityGeneration: 1, IdempotencyKeyDigest: confirmed.IdempotencyKeyDigest, OccurredAt: confirmed.RecordedAt}
	authority.At = now.Add(4 * time.Minute)
	if err := domain.TransitionAssociation(authority, 1, confirmed, confirm); err != nil {
		t.Fatal(err)
	}
	projection := NewProjectProjectionService(domain)
	if err := projection.RebuildProjectRecord(authority, project.ProjectID); err != nil {
		t.Fatal(err)
	}
	resolver.batchEntered = make(chan struct{})
	resolver.batchRelease = make(chan struct{})
	type readResult struct {
		record ProjectRecordProjection
		err    error
	}
	read := make(chan readResult, 1)
	go func() {
		record, err := projection.ReadProjectRecord(authority, project.ProjectID)
		read <- readResult{record: record, err: err}
	}()
	select {
	case <-resolver.batchEntered:
	case <-time.After(time.Second):
		t.Fatal("read never entered held source snapshot")
	}
	revoked := make(chan struct{})
	go func() {
		resolver.setAllowed(false)
		close(revoked)
	}()
	select {
	case <-revoked:
		t.Fatal("source revocation interleaved inside aggregate read snapshot")
	case <-time.After(20 * time.Millisecond):
	}
	close(resolver.batchRelease)
	result := <-read
	if result.err != nil || len(result.record.Nodes) != 1 {
		t.Fatalf("linearized pre-revocation read failed: %#v %v", result.record, result.err)
	}
	<-revoked
	if record, err := projection.ReadProjectRecord(authority, project.ProjectID); !errors.Is(err, ErrProjectAuthorityDenied) || len(record.Nodes) != 0 {
		t.Fatalf("post-revocation Project Record leaked: %#v %v", record, err)
	}
}
