package main

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type projectTestFence struct {
	mu              sync.Mutex
	personID        string
	organizationID  string
	membershipID    string
	membershipRev   int64
	sessionDigest   string
	sessionRevision int64
	generation      uint64
	allow           bool
	callCount       int
	blockOnCall     int
	entered         chan struct{}
	release         chan struct{}
}

type projectTestSourceResolver struct {
	mu           sync.Mutex
	snapshot     ProjectSourceAuthoritySnapshot
	allow        bool
	batchEntered chan struct{}
	batchRelease chan struct{}
	batchOnce    sync.Once
}

func (r *projectTestSourceResolver) WithCurrentProjectSource(_ ProjectAuthorityContext, subject STRIDEReference, refs []STRIDEReference, effect func(ProjectSourceAuthoritySnapshot) error) error {
	if r == nil || effect == nil {
		return ErrProjectAuthorityDenied
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.allow || r.snapshot.Subject != subject || !sameSTRIDEReferences(r.snapshot.SourceRefs, refs) {
		return ErrProjectAuthorityDenied
	}
	return effect(r.snapshot)
}

func (r *projectTestSourceResolver) WithCurrentProjectSources(_ ProjectAuthorityContext, requests []ProjectSourceAuthorityRequest, effect func([]ProjectSourceAuthoritySnapshot) error) error {
	if r == nil || effect == nil {
		return ErrProjectAuthorityDenied
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.allow {
		return ErrProjectAuthorityDenied
	}
	if r.batchEntered != nil && r.batchRelease != nil {
		r.batchOnce.Do(func() { close(r.batchEntered) })
		<-r.batchRelease
	}
	snapshots := make([]ProjectSourceAuthoritySnapshot, len(requests))
	for index, request := range requests {
		if r.snapshot.Subject != request.Subject || !sameSTRIDEReferences(r.snapshot.SourceRefs, request.SourceRefs) {
			return ErrProjectAuthorityDenied
		}
		snapshots[index] = r.snapshot
	}
	return effect(snapshots)
}

func (r *projectTestSourceResolver) setAllowed(allowed bool) {
	r.mu.Lock()
	r.allow = allowed
	r.mu.Unlock()
}

func projectSourceResolverFixture() *projectTestSourceResolver {
	return &projectTestSourceResolver{allow: true, snapshot: ProjectSourceAuthoritySnapshot{ReceiptID: "source_authority_receipt_1",
		Subject: strideTestRef(STRIDEContractConversationEvent, "turn_1"), SourceRefs: []STRIDEReference{strideTestRef(STRIDEContractConversationEvent, "turn_1")},
		EvidenceCoverageDigest: strings.Repeat("1", 64), Audience: STRIDEAudience{Visibility: "private", Principals: []string{"person_aj"}},
		ACLRevision: 1, ACLDigest: strings.Repeat("2", 64), ConsentRevision: 1, PurgeGeneration: 1, ExpiresAt: time.Date(2026, 8, 13, 18, 0, 0, 0, time.UTC),
	}}
}

func (f *projectTestFence) WithCurrentProjectAuthority(value ProjectAuthorityContext, effect func() error) error {
	if f == nil || effect == nil {
		return ErrProjectAuthorityDenied
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++
	if !f.allow || value.Person.Header.ID != f.personID || value.Organization.Header.ID != f.organizationID ||
		value.Membership.Header.ID != f.membershipID || value.Membership.Header.Revision != f.membershipRev ||
		value.ActiveSession.SessionSubjectDigest != f.sessionDigest || value.ActiveSession.SessionRevision != f.sessionRevision ||
		value.Generation != f.generation {
		return ErrProjectAuthorityDenied
	}
	if f.blockOnCall > 0 && f.callCount == f.blockOnCall {
		close(f.entered)
		<-f.release
	}
	return effect()
}

func (f *projectTestFence) setAllowed(allowed bool) {
	f.mu.Lock()
	f.allow = allowed
	f.mu.Unlock()
}

func projectAuthorityFixture(orgID, personID, membershipID string, at time.Time) (ProjectAuthorityContext, *projectTestFence) {
	person := PersonPrincipal{Header: organizationTestHeader(STRIDEGlobalPersonTenant, personID, 1, STRIDEContractPersonPrincipal, '1', at), AccountSubjectDigest: strings.Repeat("2", 64), Status: "active", RecoveryRevision: 1, CustodyRevision: 1}
	organization := Organization{Header: organizationTestHeader(STRIDEGlobalPersonTenant, orgID, 1, STRIDEContractOrganization, '3', at), Name: "Bonfire", Slug: strings.ToLower(orgID), Status: "active", Discoverability: "private", CreatorPersonID: personID, PolicyRevision: 1, CreatedAt: at, UpdatedAt: at}
	membership := OrganizationMembership{Header: organizationTestHeader(orgID, membershipID, 1, STRIDEContractOrganizationMembership, '4', at), PersonID: personID, OrganizationID: orgID, Role: "owner", Status: "active", GrantedAt: at}
	expires := at.Add(8 * time.Hour)
	session := ActiveOrganizationSession{Header: organizationTestHeader(STRIDEGlobalPersonTenant, "active_"+membershipID, 1, STRIDEContractActiveOrganizationSession, '5', at), SessionSubjectDigest: strings.Repeat("6", 64), PersonID: personID, OrganizationID: orgID, MembershipID: membershipID, MembershipRevision: 1, SessionRevision: 1, Status: "active", BoundAt: at, ExpiresAt: expires}
	authority := ProjectAuthorityContext{Person: person, Organization: organization, Membership: membership, ActiveSession: session, Generation: 1, At: at.Add(time.Minute)}
	fence := &projectTestFence{personID: personID, organizationID: orgID, membershipID: membershipID, membershipRev: 1, sessionDigest: session.SessionSubjectDigest, sessionRevision: 1, generation: 1, allow: true}
	return authority, fence
}

func projectAuthorityProject(authority ProjectAuthorityContext, id, title, threadID string, at time.Time) (Project, ProjectThreadBinding) {
	project := Project{
		Header: organizationTestHeader(authority.Organization.Header.ID, id, 1, STRIDEContractProject, '7', at), ProjectID: id, OrganizationID: authority.Organization.Header.ID,
		Title: title, Lifecycle: "active", RetentionPolicy: "organization_default",
		ControllerMemberships: []STRIDEReference{{ContractType: STRIDEContractOrganizationMembership, ID: authority.Membership.Header.ID, Revision: authority.Membership.Header.Revision, Digest: authority.Membership.Header.ContentDigest}},
		Audience:              STRIDEAudience{Visibility: "project", Principals: []string{authority.Person.Header.ID}}, ACLRevision: 1,
		CreatorPersonID: authority.Person.Header.ID, CreatedAt: at, UpdatedAt: at,
	}
	binding := ProjectThreadBinding{
		Header:   organizationTestHeader(authority.Organization.Header.ID, "binding_"+id, 1, STRIDEContractProjectThreadBinding, '8', at),
		Project:  STRIDEReference{ContractType: STRIDEContractProject, ID: id, Revision: 1, Digest: project.Header.ContentDigest},
		ThreadID: threadID, Kind: "primary", State: "active", ThreadAudienceRevision: 1, ThreadACLDigest: strings.Repeat("9", 64),
		ActorPersonID: authority.Person.Header.ID, ActorMembershipID: authority.Membership.Header.ID, ActorMembershipRevision: authority.Membership.Header.Revision, BoundAt: at,
	}
	return project, binding
}

func TestProjectAuthorityStableIdentitySameNameAndLifecycle(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	authority, fence := projectAuthorityFixture("bonfire", "person_aj", "member_aj", now)
	service := NewProjectAuthorityService(fence, projectSourceResolverFixture())
	first, firstBinding := projectAuthorityProject(authority, "project_one", "STRIDE", "thread_one", now.Add(2*time.Minute))
	second, secondBinding := projectAuthorityProject(authority, "project_two", "STRIDE", "thread_two", now.Add(3*time.Minute))
	if err := service.CreateProject(authority, first, firstBinding, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateProject(authority, second, secondBinding, strings.Repeat("b", 64)); err != nil {
		t.Fatalf("same-name distinct Project rejected: %v", err)
	}
	renamed := first
	renamed.Header = organizationTestHeader("bonfire", first.ProjectID, 2, STRIDEContractProject, 'c', now.Add(4*time.Minute))
	renamed.Title = "STRIDE Evolution"
	renamed.UpdatedAt = renamed.Header.CreatedAt
	renamed.Supersedes = &STRIDEReference{ContractType: STRIDEContractProject, ID: first.ProjectID, Revision: 1, Digest: first.Header.ContentDigest}
	if err := service.ReviseProject(authority, 1, renamed, strings.Repeat("d", 64)); err != nil {
		t.Fatalf("rename rejected: %v", err)
	}
	visible, err := service.VisibleProjects(authority)
	if err != nil || len(visible) != 2 {
		t.Fatalf("visible Projects = %#v, %v", visible, err)
	}
	sort.Slice(visible, func(i, j int) bool { return visible[i].ProjectID < visible[j].ProjectID })
	if visible[0].ProjectID != first.ProjectID || visible[0].Title != "STRIDE Evolution" {
		t.Fatalf("rename changed identity or failed to project: %#v", visible[0])
	}
	archived := renamed
	archived.Header = organizationTestHeader("bonfire", first.ProjectID, 3, STRIDEContractProject, 'e', now.Add(5*time.Minute))
	archived.Lifecycle = "archived"
	archived.UpdatedAt = archived.Header.CreatedAt
	archived.Supersedes = &STRIDEReference{ContractType: STRIDEContractProject, ID: first.ProjectID, Revision: 2, Digest: renamed.Header.ContentDigest}
	if err := service.ReviseProject(authority, 2, archived, strings.Repeat("f", 64)); err != nil {
		t.Fatal(err)
	}
	visible, _ = service.VisibleProjects(authority)
	if len(visible) != 1 || visible[0].ProjectID != second.ProjectID {
		t.Fatalf("archived Project remained suggestible: %#v", visible)
	}
}

func TestProjectAuthorityFailsClosedOnSessionAndTenantDrift(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	authority, fence := projectAuthorityFixture("bonfire", "person_aj", "member_aj", now)
	service := NewProjectAuthorityService(fence, projectSourceResolverFixture())
	project, binding := projectAuthorityProject(authority, "project_one", "Private roadmap", "thread_one", now.Add(2*time.Minute))
	if err := service.CreateProject(authority, project, binding, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	stale := authority
	stale.Generation++
	if projects, err := service.VisibleProjects(stale); !errors.Is(err, ErrProjectAuthorityDenied) || projects != nil {
		t.Fatalf("stale generation leaked Projects: %#v %v", projects, err)
	}
	other, otherFence := projectAuthorityFixture("other-org", "person_tyler", "member_tyler", now)
	otherService := NewProjectAuthorityService(otherFence, projectSourceResolverFixture())
	// Copying storage is deliberately unavailable; another service/tenant has no
	// name, count, or timing shape from the first organization.
	if projects, err := otherService.VisibleProjects(other); err != nil || len(projects) != 0 {
		t.Fatalf("cross-org empty projection failed closed: %#v %v", projects, err)
	}
	fence.allow = false
	if projects, err := service.VisibleProjects(authority); !errors.Is(err, ErrProjectAuthorityDenied) || projects != nil {
		t.Fatalf("revoked session leaked Projects: %#v %v", projects, err)
	}
}

func TestProjectAuthorityIdempotencyAndPrimaryThreadUniqueness(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	authority, fence := projectAuthorityFixture("bonfire", "person_aj", "member_aj", now)
	service := NewProjectAuthorityService(fence, projectSourceResolverFixture())
	project, binding := projectAuthorityProject(authority, "project_one", "Roadmap", "thread_one", now.Add(2*time.Minute))
	key := strings.Repeat("a", 64)
	if err := service.CreateProject(authority, project, binding, key); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateProject(authority, project, binding, key); err != nil {
		t.Fatalf("exact retry was not idempotent: %v", err)
	}
	conflict := project
	conflict.Title = "Changed under same key"
	conflict.Header.ContentDigest = strings.Repeat("b", 64)
	conflictBinding := binding
	conflictBinding.Project.Digest = conflict.Header.ContentDigest
	if err := service.CreateProject(authority, conflict, conflictBinding, key); !errors.Is(err, ErrProjectAuthorityConflict) {
		t.Fatalf("same key/different request did not conflict: %v", err)
	}
	second, secondBinding := projectAuthorityProject(authority, "project_two", "Other", "thread_one", now.Add(3*time.Minute))
	if err := service.CreateProject(authority, second, secondBinding, strings.Repeat("c", 64)); !errors.Is(err, ErrProjectAuthorityConflict) {
		t.Fatalf("one active primary thread was bound to two Projects: %v", err)
	}
}

func projectAuthorityAssociation(authority ProjectAuthorityContext, project Project, id string, at time.Time, keyRune rune) (ProjectAssociation, ProjectAssociationEvent) {
	expires := at.Add(15 * time.Minute)
	association := ProjectAssociation{
		Header:  organizationTestHeader(authority.Organization.Header.ID, id, 1, STRIDEContractProjectAssociation, keyRune, at),
		Project: STRIDEReference{ContractType: STRIDEContractProject, ID: project.ProjectID, Revision: project.Header.Revision, Digest: project.Header.ContentDigest},
		Subject: strideTestRef(STRIDEContractConversationEvent, "turn_1"), SourceRefs: []STRIDEReference{strideTestRef(STRIDEContractConversationEvent, "turn_1")}, SourceAuthorityReceiptID: "source_authority_receipt_1",
		EvidenceCoverageDigest: strings.Repeat("1", 64), State: "proposed", Basis: "suggested", ClassifierRevision: "project_linker_v1", Confidence: .9,
		ActorPersonID: authority.Person.Header.ID, ActorMembershipID: authority.Membership.Header.ID, ActorMembershipRevision: authority.Membership.Header.Revision,
		SessionSubjectDigest: authority.ActiveSession.SessionSubjectDigest, SessionRevision: authority.ActiveSession.SessionRevision, AuthorityGeneration: authority.Generation,
		SourceAudience: STRIDEAudience{Visibility: "private", Principals: []string{authority.Person.Header.ID}}, SourceACLRevision: 1, SourceACLDigest: strings.Repeat("2", 64),
		ConsentRevision: 1, PurgeGeneration: 1, IdempotencyKeyDigest: strings.Repeat(string(keyRune), 64), ExpiresAt: &expires, RecordedAt: at,
	}
	event := ProjectAssociationEvent{
		Header:      organizationTestHeader(authority.Organization.Header.ID, "event_"+id, 1, STRIDEContractProjectAssociationEvent, keyRune, at),
		Association: projectAssociationRef(association), Action: "propose", ResultingState: "proposed", PriorRevision: 0, NewRevision: 1,
		ActorPersonID: authority.Person.Header.ID, ActorMembershipID: authority.Membership.Header.ID, ActorMembershipRevision: authority.Membership.Header.Revision,
		SessionSubjectDigest: authority.ActiveSession.SessionSubjectDigest, SessionRevision: authority.ActiveSession.SessionRevision, AuthorityGeneration: authority.Generation,
		IdempotencyKeyDigest: association.IdempotencyKeyDigest, OccurredAt: at,
	}
	return association, event
}

func TestProjectAssociationProposalConfirmationCorrectionAndNoResurrection(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	authority, fence := projectAuthorityFixture("bonfire", "person_aj", "member_aj", now)
	service := NewProjectAuthorityService(fence, projectSourceResolverFixture())
	first, firstBinding := projectAuthorityProject(authority, "project_one", "Roadmap", "thread_one", now.Add(2*time.Minute))
	second, secondBinding := projectAuthorityProject(authority, "project_two", "Launch", "thread_two", now.Add(3*time.Minute))
	if err := service.CreateProject(authority, first, firstBinding, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateProject(authority, second, secondBinding, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	proposed, proposeEvent := projectAuthorityAssociation(authority, first, "association_one", now.Add(4*time.Minute), 'c')
	if err := service.ProposeAssociation(authority, proposed, proposeEvent); err != nil {
		t.Fatal(err)
	}
	confirmed := proposed
	confirmed.Header = organizationTestHeader("bonfire", proposed.Header.ID, 2, STRIDEContractProjectAssociation, 'd', now.Add(5*time.Minute))
	confirmed.State, confirmed.ExpiresAt, confirmed.RecordedAt = "confirmed", nil, confirmed.Header.CreatedAt
	confirmed.IdempotencyKeyDigest = strings.Repeat("d", 64)
	confirmed.Supersedes = &STRIDEReference{ContractType: STRIDEContractProjectAssociation, ID: proposed.Header.ID, Revision: 1, Digest: proposed.Header.ContentDigest}
	confirmEvent := ProjectAssociationEvent{Header: organizationTestHeader("bonfire", "event_confirm", 1, STRIDEContractProjectAssociationEvent, 'd', confirmed.RecordedAt), Association: projectAssociationRef(confirmed), Action: "confirm", ResultingState: "confirmed", PriorRevision: 1, NewRevision: 2, ActorPersonID: authority.Person.Header.ID, ActorMembershipID: authority.Membership.Header.ID, ActorMembershipRevision: 1, SessionSubjectDigest: authority.ActiveSession.SessionSubjectDigest, SessionRevision: 1, AuthorityGeneration: 1, IdempotencyKeyDigest: confirmed.IdempotencyKeyDigest, OccurredAt: confirmed.RecordedAt}
	authority.At = now.Add(5 * time.Minute)
	if err := service.TransitionAssociation(authority, 1, confirmed, confirmEvent); err != nil {
		t.Fatal(err)
	}
	replacement, _ := projectAuthorityAssociation(authority, second, "association_two", now.Add(6*time.Minute), 'e')
	replacement.State, replacement.Basis, replacement.ExpiresAt = "confirmed", "selected", nil
	corrected := confirmed
	corrected.Header = organizationTestHeader("bonfire", confirmed.Header.ID, 3, STRIDEContractProjectAssociation, 'f', now.Add(6*time.Minute))
	corrected.State, corrected.RecordedAt = "corrected", corrected.Header.CreatedAt
	corrected.IdempotencyKeyDigest = strings.Repeat("f", 64)
	corrected.Supersedes = &STRIDEReference{ContractType: STRIDEContractProjectAssociation, ID: confirmed.Header.ID, Revision: 2, Digest: confirmed.Header.ContentDigest}
	replacement.IdempotencyKeyDigest = corrected.IdempotencyKeyDigest
	replacement.Replacement = nil
	replacement.RecordedAt = corrected.RecordedAt
	corrected.Replacement = func() *STRIDEReference { value := projectAssociationRef(replacement); return &value }()
	correctEvent := ProjectAssociationEvent{Header: organizationTestHeader("bonfire", "event_correct", 1, STRIDEContractProjectAssociationEvent, 'f', corrected.RecordedAt), Association: projectAssociationRef(corrected), Action: "correct", ResultingState: "corrected", PriorRevision: 2, NewRevision: 3, Replacement: corrected.Replacement, ActorPersonID: authority.Person.Header.ID, ActorMembershipID: authority.Membership.Header.ID, ActorMembershipRevision: 1, SessionSubjectDigest: authority.ActiveSession.SessionSubjectDigest, SessionRevision: 1, AuthorityGeneration: 1, IdempotencyKeyDigest: corrected.IdempotencyKeyDigest, OccurredAt: corrected.RecordedAt}
	authority.At = now.Add(6 * time.Minute)
	if err := service.CorrectAssociation(authority, 2, corrected, replacement, correctEvent); err != nil {
		t.Fatal(err)
	}
	current, err := service.CurrentAssociation(authority, corrected.Header.ID)
	if err != nil || current.State != "corrected" || current.Replacement == nil || current.Replacement.ID != replacement.Header.ID {
		t.Fatalf("old edge not terminalized: %#v %v", current, err)
	}
	newCurrent, err := service.CurrentAssociation(authority, replacement.Header.ID)
	if err != nil || newCurrent.State != "confirmed" || newCurrent.Project.ID != second.ProjectID {
		t.Fatalf("replacement edge not current: %#v %v", newCurrent, err)
	}
	resurrect := corrected
	resurrect.Header = organizationTestHeader("bonfire", corrected.Header.ID, 4, STRIDEContractProjectAssociation, '7', now.Add(7*time.Minute))
	resurrect.State, resurrect.Replacement = "confirmed", nil
	resurrect.RecordedAt = resurrect.Header.CreatedAt
	resurrect.IdempotencyKeyDigest = strings.Repeat("7", 64)
	resurrect.Supersedes = &STRIDEReference{ContractType: STRIDEContractProjectAssociation, ID: corrected.Header.ID, Revision: 3, Digest: corrected.Header.ContentDigest}
	resurrectEvent := ProjectAssociationEvent{Header: organizationTestHeader("bonfire", "event_resurrect", 1, STRIDEContractProjectAssociationEvent, '7', resurrect.RecordedAt), Association: projectAssociationRef(resurrect), Action: "confirm", ResultingState: "confirmed", PriorRevision: 3, NewRevision: 4, ActorPersonID: authority.Person.Header.ID, ActorMembershipID: authority.Membership.Header.ID, ActorMembershipRevision: 1, SessionSubjectDigest: authority.ActiveSession.SessionSubjectDigest, SessionRevision: 1, AuthorityGeneration: 1, IdempotencyKeyDigest: resurrect.IdempotencyKeyDigest, OccurredAt: resurrect.RecordedAt}
	authority.At = now.Add(7 * time.Minute)
	if err := service.TransitionAssociation(authority, 3, resurrect, resurrectEvent); !errors.Is(err, ErrProjectAuthorityConflict) {
		t.Fatalf("terminal association resurrected: %v", err)
	}
}

func TestProjectAssociationRequiresServerResolvedCurrentSourceAuthority(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	authority, fence := projectAuthorityFixture("bonfire", "person_aj", "member_aj", now)
	resolver := projectSourceResolverFixture()
	service := NewProjectAuthorityService(fence, resolver)
	project, binding := projectAuthorityProject(authority, "project_one", "Roadmap", "thread_one", now.Add(2*time.Minute))
	if err := service.CreateProject(authority, project, binding, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	proposed, event := projectAuthorityAssociation(authority, project, "association_source", now.Add(3*time.Minute), 'b')
	proposed.SourceACLDigest = strings.Repeat("f", 64)
	if err := service.ProposeAssociation(authority, proposed, event); !errors.Is(err, ErrProjectAuthorityDenied) {
		t.Fatalf("caller-minted source ACL was accepted: %v", err)
	}
	proposed.SourceACLDigest = resolver.snapshot.ACLDigest
	if err := service.ProposeAssociation(authority, proposed, event); err != nil {
		t.Fatal(err)
	}
	resolver.snapshot.ACLRevision++
	resolver.snapshot.ACLDigest = strings.Repeat("e", 64)
	if _, err := service.CurrentAssociation(authority, proposed.Header.ID); !errors.Is(err, ErrProjectAuthorityNotFound) {
		t.Fatalf("read exposed association after source authority drift: %v", err)
	}
}

func TestProjectAssociationRenewsEphemeralSourceReceiptWithoutRewritingEdge(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	authority, fence := projectAuthorityFixture("bonfire", "person_aj", "member_aj", now)
	resolver := projectSourceResolverFixture()
	service := NewProjectAuthorityService(fence, resolver)
	project, binding := projectAuthorityProject(authority, "project_one", "Roadmap", "thread_one", now.Add(2*time.Minute))
	if err := service.CreateProject(authority, project, binding, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	proposed, event := projectAuthorityAssociation(authority, project, "association_renew", now.Add(3*time.Minute), 'b')
	if err := service.ProposeAssociation(authority, proposed, event); err != nil {
		t.Fatal(err)
	}
	resolver.snapshot.ReceiptID = "source_authority_receipt_renewed"
	resolver.snapshot.ExpiresAt = now.Add(24 * time.Hour)
	if current, err := service.CurrentAssociation(authority, proposed.Header.ID); err != nil || current.Header.ID != proposed.Header.ID {
		t.Fatalf("renewed exact source receipt rewrote or hid durable edge: %#v %v", current, err)
	}
	resolver.snapshot.ExpiresAt = authority.At
	if _, err := service.CurrentAssociation(authority, proposed.Header.ID); !errors.Is(err, ErrProjectAuthorityDenied) {
		t.Fatalf("expired ephemeral resolver snapshot authorized read: %v", err)
	}
}

func TestProjectIdempotencyIsScopedByOrganizationAndOperation(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	authority, fence := projectAuthorityFixture("bonfire", "person_aj", "member_aj", now)
	service := NewProjectAuthorityService(fence, projectSourceResolverFixture())
	project, binding := projectAuthorityProject(authority, "project_one", "Roadmap", "thread_one", now.Add(2*time.Minute))
	key := strings.Repeat("a", 64)
	if err := service.CreateProject(authority, project, binding, key); err != nil {
		t.Fatal(err)
	}
	// The same digest used for a different operation is a distinct idempotency
	// scope; it must reach that operation's own validation/CAS boundary.
	revised := project
	revised.Header = organizationTestHeader("bonfire", project.ProjectID, 2, STRIDEContractProject, 'b', now.Add(3*time.Minute))
	revised.Title, revised.UpdatedAt = "Roadmap v2", revised.Header.CreatedAt
	revised.Supersedes = &STRIDEReference{ContractType: STRIDEContractProject, ID: project.ProjectID, Revision: 1, Digest: project.Header.ContentDigest}
	if err := service.ReviseProject(authority, 1, revised, key); err != nil {
		t.Fatalf("cross-operation idempotency collision: %v", err)
	}
}

func TestProjectMutationLinearizesBeforeConcurrentAuthorityRevocation(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	authority, fence := projectAuthorityFixture("bonfire", "person_aj", "member_aj", now)
	// CreateProject first performs a bounded current-authority probe and then
	// enters the callback that holds authority through the locked commit.
	fence.blockOnCall = 2
	fence.entered = make(chan struct{})
	fence.release = make(chan struct{})
	service := NewProjectAuthorityService(fence, projectSourceResolverFixture())
	project, binding := projectAuthorityProject(authority, "project_linear", "Linear", "thread_linear", now.Add(2*time.Minute))
	mutationDone := make(chan error, 1)
	go func() { mutationDone <- service.CreateProject(authority, project, binding, strings.Repeat("a", 64)) }()
	select {
	case <-fence.entered:
	case <-time.After(time.Second):
		t.Fatal("mutation never entered held authority callback")
	}
	revoked := make(chan struct{})
	go func() {
		fence.setAllowed(false)
		close(revoked)
	}()
	select {
	case <-revoked:
		t.Fatal("authority revocation interleaved inside held mutation callback")
	case <-time.After(20 * time.Millisecond):
	}
	close(fence.release)
	if err := <-mutationDone; err != nil {
		t.Fatalf("linearized mutation failed: %v", err)
	}
	select {
	case <-revoked:
	case <-time.After(time.Second):
		t.Fatal("authority revocation did not complete after mutation callback")
	}
	if projects, err := service.VisibleProjects(authority); !errors.Is(err, ErrProjectAuthorityDenied) || projects != nil {
		t.Fatalf("post-revocation read escaped fence: %#v %v", projects, err)
	}
}
