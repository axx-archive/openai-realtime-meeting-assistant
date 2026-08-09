package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOrganizationAuthorityConcurrentCapacityAndAtomicCreate(t *testing.T) {
	service := NewOrganizationAuthorityService()
	now := time.Now().UTC().Truncate(time.Microsecond)
	person := organizationTestPerson("person_aj", 'a', now)
	if err := service.RegisterPerson(person); err != nil {
		t.Fatalf("register person: %v", err)
	}
	for index := 1; index <= 2; index++ {
		organization, owner, event := organizationTestCreate(person.Header.ID, index, now.Add(time.Duration(index)*time.Minute))
		if err := service.CreateOrganization(person.Header.ID, organization, owner, event); err != nil {
			t.Fatalf("create organization %d: %v", index, err)
		}
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for index := 3; index <= 4; index++ {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			organization, owner, event := organizationTestCreate(person.Header.ID, index, now.Add(time.Duration(index)*time.Minute))
			<-start
			results <- service.CreateOrganization(person.Header.ID, organization, owner, event)
		}()
	}
	close(start)
	group.Wait()
	close(results)
	successes := 0
	capacityFailures := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrOrganizationCapacity):
			capacityFailures++
		default:
			t.Fatalf("unexpected concurrent create result: %v", err)
		}
	}
	if successes != 1 || capacityFailures != 1 || service.ActiveMembershipCount(person.Header.ID) != 3 {
		t.Fatalf("capacity result success=%d capacity=%d memberships=%d", successes, capacityFailures, service.ActiveMembershipCount(person.Header.ID))
	}
	if total := len(service.Audit("organization_3")) + len(service.Audit("organization_4")); total != 1 {
		t.Fatalf("failed create left partial audit/organization state: concurrent audit count=%d", total)
	}
}

func TestOrganizationAuthorityJoinApprovalAndPendingZeroAccess(t *testing.T) {
	service := NewOrganizationAuthorityService()
	now := time.Now().UTC().Truncate(time.Microsecond)
	aj := organizationTestPerson("person_aj", 'a', now)
	tim := organizationTestPerson("person_tim", 'b', now)
	for _, person := range []PersonPrincipal{aj, tim} {
		if err := service.RegisterPerson(person); err != nil {
			t.Fatalf("register %s: %v", person.Header.ID, err)
		}
	}
	organization, owner, createEvent := organizationTestCreate(aj.Header.ID, 1, now.Add(time.Minute))
	if err := service.CreateOrganization(aj.Header.ID, organization, owner, createEvent); err != nil {
		t.Fatalf("create: %v", err)
	}
	request := organizationTestJoinRequest(tim.Header.ID, organization.Header.ID, 1, "pending", now.Add(2*time.Minute), "")
	requestEvent := organizationTestAudit(organization.Header.ID, tim.Header.ID, "", 0, tim.Header.ID, "request", 0, 1, request.Header.ID, 'c', now.Add(2*time.Minute))
	if err := service.RequestJoin(request, requestEvent); err != nil {
		t.Fatalf("request join: %v", err)
	}
	if service.ActiveMembershipCount(tim.Header.ID) != 0 {
		t.Fatal("pending request granted membership authority")
	}
	if _, err := service.Membership("membership_bonfire_tim"); !errors.Is(err, ErrOrganizationAuthorityNotFound) {
		t.Fatalf("pending membership lookup err=%v", err)
	}

	approvedAt := now.Add(3 * time.Minute)
	decision := organizationTestJoinRequest(tim.Header.ID, organization.Header.ID, 2, "approved", request.RequestedAt, owner.Header.ID)
	decision.Header.ID = request.Header.ID
	decision.Header.CreatedAt = request.Header.CreatedAt
	decision.DecidedAt = &approvedAt
	member := organizationTestMembership("membership_bonfire_tim", tim.Header.ID, organization.Header.ID, "member", 1, approvedAt, owner.Header.ID)
	decisionEvent := organizationTestAudit(organization.Header.ID, aj.Header.ID, owner.Header.ID, 1, tim.Header.ID, "approve", 1, 2, decision.Header.ID, 'd', approvedAt)
	if err := service.DecideJoin(owner.Header.ID, 1, 1, decision, &member, decisionEvent); err != nil {
		t.Fatalf("approve join: %v", err)
	}
	if service.ActiveMembershipCount(tim.Header.ID) != 1 {
		t.Fatal("approved membership missing")
	}
	if err := service.DecideJoin(owner.Header.ID, 1, 1, decision, &member, decisionEvent); err != nil {
		t.Fatalf("idempotent approval replay: %v", err)
	}
	changedSideEffect := member
	changedSideEffect.Header.ContentDigest = strings.Repeat("9", 64)
	if err := service.DecideJoin(owner.Header.ID, 1, 1, decision, &changedSideEffect, decisionEvent); !errors.Is(err, ErrOrganizationAuthorityConflict) {
		t.Fatalf("idempotency key accepted a different membership side effect: %v", err)
	}
	stored, err := service.JoinRequest(request.Header.ID)
	if err != nil || stored.Status != "approved" || stored.Header.Revision != 2 {
		t.Fatalf("stored decision=%#v err=%v", stored, err)
	}
}

func TestOrganizationAuthorityFinalOwnerTransferThenDeparture(t *testing.T) {
	service := NewOrganizationAuthorityService()
	now := time.Now().UTC().Truncate(time.Microsecond)
	aj := organizationTestPerson("person_aj", 'a', now)
	tim := organizationTestPerson("person_tim", 'b', now)
	for _, person := range []PersonPrincipal{aj, tim} {
		if err := service.RegisterPerson(person); err != nil {
			t.Fatal(err)
		}
	}
	organization, owner, createEvent := organizationTestCreate(aj.Header.ID, 1, now.Add(time.Minute))
	if err := service.CreateOrganization(aj.Header.ID, organization, owner, createEvent); err != nil {
		t.Fatal(err)
	}
	member := organizationTestMembership("membership_bonfire_tim", tim.Header.ID, organization.Header.ID, "member", 1, now.Add(2*time.Minute), owner.Header.ID)
	request := organizationTestJoinRequest(tim.Header.ID, organization.Header.ID, 1, "pending", now.Add(2*time.Minute), "")
	requestEvent := organizationTestAudit(organization.Header.ID, tim.Header.ID, "", 0, tim.Header.ID, "request", 0, 1, request.Header.ID, 'c', now.Add(2*time.Minute))
	if err := service.RequestJoin(request, requestEvent); err != nil {
		t.Fatal(err)
	}
	approvedAt := now.Add(3 * time.Minute)
	decision := organizationTestJoinRequest(tim.Header.ID, organization.Header.ID, 2, "approved", request.RequestedAt, owner.Header.ID)
	decision.Header.ID = request.Header.ID
	decision.Header.CreatedAt = request.Header.CreatedAt
	decision.DecidedAt = &approvedAt
	if err := service.DecideJoin(owner.Header.ID, 1, 1, decision, &member, organizationTestAudit(organization.Header.ID, aj.Header.ID, owner.Header.ID, 1, tim.Header.ID, "approve", 1, 2, request.Header.ID, 'd', approvedAt)); err != nil {
		t.Fatal(err)
	}

	departedAt := now.Add(4 * time.Minute)
	ownerDeparted := owner
	ownerDeparted.Header.Revision = 2
	ownerDeparted.Header.ContentDigest = strings.Repeat("e", 64)
	ownerDeparted.Status = "departed"
	ownerDeparted.EndedAt = &departedAt
	leaveEvent := organizationTestAudit(organization.Header.ID, aj.Header.ID, owner.Header.ID, 1, aj.Header.ID, "leave", 1, 2, owner.Header.ID, 'e', departedAt)
	if err := service.EndMembership(owner.Header.ID, 1, 1, ownerDeparted, leaveEvent); !errors.Is(err, ErrOrganizationFinalOwner) {
		t.Fatalf("final owner departure err=%v", err)
	}

	transferAt := now.Add(5 * time.Minute)
	priorOwnerNext := owner
	priorOwnerNext.Header.Revision = 2
	priorOwnerNext.Header.ContentDigest = strings.Repeat("f", 64)
	priorOwnerNext.Role = "admin"
	newOwnerNext := member
	newOwnerNext.Header.Revision = 2
	newOwnerNext.Header.ContentDigest = strings.Repeat("1", 64)
	newOwnerNext.Role = "owner"
	transferEvent := organizationTestAudit(organization.Header.ID, aj.Header.ID, owner.Header.ID, 1, tim.Header.ID, "transfer", 1, 2, member.Header.ID, '2', transferAt)
	if err := service.TransferOwnership(owner.Header.ID, 1, priorOwnerNext, newOwnerNext, transferEvent); err != nil {
		t.Fatalf("transfer ownership: %v", err)
	}

	departedAt = now.Add(6 * time.Minute)
	priorOwnerDeparted := priorOwnerNext
	priorOwnerDeparted.Header.Revision = 3
	priorOwnerDeparted.Header.ContentDigest = strings.Repeat("3", 64)
	priorOwnerDeparted.Status = "departed"
	priorOwnerDeparted.EndedAt = &departedAt
	leaveAfterTransfer := organizationTestAudit(organization.Header.ID, aj.Header.ID, owner.Header.ID, 2, aj.Header.ID, "leave", 2, 3, owner.Header.ID, '4', departedAt)
	if err := service.EndMembership(owner.Header.ID, 2, 2, priorOwnerDeparted, leaveAfterTransfer); err != nil {
		t.Fatalf("departure after transfer: %v", err)
	}
	storedNewOwner, err := service.Membership(member.Header.ID)
	if err != nil || storedNewOwner.Role != "owner" || storedNewOwner.Status != "active" {
		t.Fatalf("new owner invalid: %#v err=%v", storedNewOwner, err)
	}
}

func TestOrganizationAuthorityRejectsCrossOrganizationTransferAndMismatchedSession(t *testing.T) {
	service := NewOrganizationAuthorityService()
	now := time.Now().UTC().Truncate(time.Microsecond)
	aj := organizationTestPerson("person_aj", 'a', now)
	tim := organizationTestPerson("person_tim", 'b', now)
	pat := organizationTestPerson("person_pat", 'c', now)
	for _, person := range []PersonPrincipal{aj, tim, pat} {
		if err := service.RegisterPerson(person); err != nil {
			t.Fatal(err)
		}
	}
	organizationA, ownerA, createA := organizationTestCreate(aj.Header.ID, 1, now.Add(time.Minute))
	organizationB, ownerB, createB := organizationTestCreate(pat.Header.ID, 2, now.Add(2*time.Minute))
	if err := service.CreateOrganization(aj.Header.ID, organizationA, ownerA, createA); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateOrganization(pat.Header.ID, organizationB, ownerB, createB); err != nil {
		t.Fatal(err)
	}

	// An owner of B cannot rewrite memberships from A as though they belonged
	// to B, even when every proposed contract is individually well formed.
	forgedPrior := ownerA
	forgedPrior.Header.TenantID = organizationB.Header.ID
	forgedPrior.Header.Revision = 2
	forgedPrior.Header.ContentDigest = strings.Repeat("d", 64)
	forgedPrior.OrganizationID = organizationB.Header.ID
	forgedPrior.Role = "admin"
	forgedTarget := organizationTestMembership("membership_a_tim", tim.Header.ID, organizationB.Header.ID, "owner", 2, now.Add(3*time.Minute), ownerB.Header.ID)
	forgedEvent := organizationTestAudit(organizationB.Header.ID, pat.Header.ID, ownerB.Header.ID, 1, tim.Header.ID, "transfer", 1, 2, forgedTarget.Header.ID, 'e', now.Add(3*time.Minute))
	if err := service.TransferOwnership(ownerB.Header.ID, 1, forgedPrior, forgedTarget, forgedEvent); err == nil {
		t.Fatal("cross-organization membership rewrite unexpectedly succeeded")
	}
	storedOwnerA, err := service.Membership(ownerA.Header.ID)
	if err != nil || storedOwnerA.OrganizationID != organizationA.Header.ID || storedOwnerA.Role != "owner" {
		t.Fatalf("org A owner mutated by rejected transfer: %#v err=%v", storedOwnerA, err)
	}

	// A syntactically valid binding cannot borrow another person's active
	// membership as its authority.
	boundAt := now.Add(4 * time.Minute)
	session := ActiveOrganizationSession{
		Header:               organizationTestHeader(STRIDEGlobalPersonTenant, "active_session_tim", 1, STRIDEContractActiveOrganizationSession, 'f', boundAt),
		SessionSubjectDigest: strings.Repeat("1", 64),
		PersonID:             tim.Header.ID,
		OrganizationID:       organizationA.Header.ID,
		MembershipID:         ownerA.Header.ID,
		MembershipRevision:   1,
		SessionRevision:      1,
		Status:               "active",
		BoundAt:              boundAt,
		ExpiresAt:            boundAt.Add(time.Hour),
	}
	switchEvent := organizationTestAudit(organizationA.Header.ID, tim.Header.ID, ownerA.Header.ID, 1, tim.Header.ID, "switch", 0, 1, session.Header.ID, '2', boundAt)
	if err := service.BindActiveSession(0, session, switchEvent); err == nil {
		t.Fatal("session borrowed another person's membership")
	}
}

func organizationTestPerson(id string, digestRune rune, at time.Time) PersonPrincipal {
	return PersonPrincipal{
		Header:               organizationTestHeader(STRIDEGlobalPersonTenant, id, 1, STRIDEContractPersonPrincipal, digestRune, at),
		AccountSubjectDigest: strings.Repeat(string(digestRune), 64),
		Status:               "active",
		RecoveryRevision:     1,
		CustodyRevision:      1,
	}
}

func organizationTestCreate(personID string, index int, at time.Time) (Organization, OrganizationMembership, OrganizationAuditEvent) {
	organizationID := fmt.Sprintf("organization_%d", index)
	organization := Organization{
		Header:          organizationTestHeader(STRIDEGlobalPersonTenant, organizationID, 1, STRIDEContractOrganization, rune('a'+index), at),
		Name:            fmt.Sprintf("Organization %d", index),
		Slug:            fmt.Sprintf("organization-%d", index),
		Status:          "active",
		Discoverability: "private",
		CreatorPersonID: personID,
		PolicyRevision:  1,
		CreatedAt:       at,
		UpdatedAt:       at,
	}
	owner := organizationTestMembership(fmt.Sprintf("membership_%d_owner", index), personID, organizationID, "owner", 1, at, "")
	event := organizationTestAudit(organizationID, personID, "", 0, "", "create", 0, 1, organizationID, rune('5'+index), at)
	return organization, owner, event
}

func organizationTestMembership(id, personID, organizationID, role string, revision int64, at time.Time, grantedBy string) OrganizationMembership {
	return OrganizationMembership{
		Header:                organizationTestHeader(organizationID, id, revision, STRIDEContractOrganizationMembership, 'b', at),
		PersonID:              personID,
		OrganizationID:        organizationID,
		Role:                  role,
		Status:                "active",
		GrantedAt:             at,
		GrantedByMembershipID: grantedBy,
	}
}

func organizationTestJoinRequest(personID, organizationID string, revision int64, status string, requestedAt time.Time, decidedBy string) OrganizationJoinRequest {
	return OrganizationJoinRequest{
		Header:                organizationTestHeader(organizationID, "join_"+organizationID+"_"+personID, revision, STRIDEContractOrganizationJoinRequest, 'c', requestedAt),
		PersonID:              personID,
		OrganizationID:        organizationID,
		Status:                status,
		RequestedAt:           requestedAt,
		ExpiresAt:             requestedAt.Add(24 * time.Hour),
		DecidedByMembershipID: decidedBy,
	}
}

func organizationTestAudit(organizationID, actorPersonID, actorMembershipID string, actorMembershipRevision int64, subjectPersonID, action string, priorRevision, newRevision int64, resultID string, digestRune rune, at time.Time) OrganizationAuditEvent {
	return OrganizationAuditEvent{
		Header:                  organizationTestHeader(organizationID, "audit_"+action+"_"+resultID+fmt.Sprint(newRevision), 1, STRIDEContractOrganizationAuditEvent, digestRune, at),
		OrganizationID:          organizationID,
		ActorPersonID:           actorPersonID,
		ActorMembershipID:       actorMembershipID,
		ActorMembershipRevision: actorMembershipRevision,
		SubjectPersonID:         subjectPersonID,
		Action:                  action,
		PriorRevision:           priorRevision,
		NewRevision:             newRevision,
		CorrelationID:           "correlation_" + action + "_" + resultID,
		IdempotencyKeyDigest:    strings.Repeat(string(digestRune), 64),
		OccurredAt:              at,
	}
}

func organizationTestHeader(tenantID, id string, revision int64, contractType STRIDEContractType, digestRune rune, at time.Time) STRIDEContractHeader {
	return STRIDEContractHeader{
		TenantID:      tenantID,
		ID:            id,
		Revision:      revision,
		SchemaVersion: STRIDEContractSchemaVersion,
		ContractType:  contractType,
		ContentDigest: strings.Repeat(string(digestRune), 64),
		CreatedAt:     at,
	}
}
