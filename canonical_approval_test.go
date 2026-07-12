package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type approvalAuthorizerStub struct{ allowed bool }

func (s approvalAuthorizerStub) MayApprove(context.Context, ACLPrincipal, ApprovalBinding) (bool, error) {
	return s.allowed, nil
}

type approvalEligibilityStub struct {
	mu     sync.Mutex
	roles  map[string]ApprovalEndorserRole
	active map[string]bool
}

func (s *approvalEligibilityStub) EndorserRole(_ context.Context, principal ACLPrincipal) (ApprovalEndorserRole, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := approvalPrincipalKey(principal)
	return s.roles[key], s.active[key], nil
}

type approvalTargetStub struct{ object ACLObject }

func (s *approvalTargetStub) CurrentApprovalTarget(context.Context, ACLObjectRef) (ACLObject, error) {
	return s.object, nil
}

func approvalTestFixture(t *testing.T) (ApprovalService, ApprovalBinding, ACLPrincipal, *approvalEligibilityStub, *approvalTargetStub, time.Time) {
	t.Helper()
	now := time.Date(2026, 7, 12, 20, 0, 0, 0, time.UTC)
	ref := ACLObjectRef{TenantID: "bonfire", Type: "artifact", ID: "artifact-1", ACLVersion: 4}
	contentDigest := strings.Repeat("a", 64)
	parametersDigest := strings.Repeat("b", 64)
	binding := ApprovalBinding{
		Target: ref, Revision: ACLRevisionRef{ContentRevision: 8, ContentDigest: contentDigest}, Action: ACLExecute,
		Plan: ApprovalActionPlan{PolicyVersion: "external-write-v1", Recipient: "investor-1", Destination: "email", Command: "send approved report", ParametersDigest: parametersDigest, ACLVersion: ref.ACLVersion},
	}
	requester := ACLPrincipal{TenantID: "bonfire", ID: "requester", Kind: ACLPrincipalUser}
	eligibility := &approvalEligibilityStub{roles: map[string]ApprovalEndorserRole{}, active: map[string]bool{}}
	target := &approvalTargetStub{object: ACLObject{Ref: ref, CurrentContentRevision: 8, CurrentContentDigest: contentDigest}}
	service := ApprovalService{
		Repo: NewMemoryApprovalRepository(), Authorizer: approvalAuthorizerStub{allowed: true}, Eligibility: eligibility, Targets: target,
		Now: func() time.Time { return now },
	}
	return service, binding, requester, eligibility, target, now
}

func setApprovalRole(stub *approvalEligibilityStub, principal ACLPrincipal, role ApprovalEndorserRole, active bool) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.roles[approvalPrincipalKey(principal)] = role
	stub.active[approvalPrincipalKey(principal)] = active
}

func TestApprovalNeedsOneOwnerOrTwoDistinctActiveMembers(t *testing.T) {
	service, binding, requester, eligibility, _, now := approvalTestFixture(t)
	created, err := service.Create(context.Background(), "", binding, requester, now.Add(time.Hour))
	if err != nil || created.Status != ApprovalPending {
		t.Fatalf("create=%+v err=%v", created, err)
	}
	if _, err := uuid.Parse(created.ID); err != nil {
		t.Fatalf("approval id %q is not PostgreSQL UUID compatible: %v", created.ID, err)
	}
	if _, err := service.Create(context.Background(), "not-a-uuid", binding, requester, now.Add(time.Hour)); !errors.Is(err, ErrApprovalInvalid) {
		t.Fatalf("non-UUID approval id error = %v", err)
	}
	serviceCanonical, bindingCanonical, requesterCanonical, _, _, nowCanonical := approvalTestFixture(t)
	canonicalID := uuid.New()
	normalized, err := serviceCanonical.Create(context.Background(), "urn:uuid:"+canonicalID.String(), bindingCanonical, requesterCanonical, nowCanonical.Add(time.Hour))
	if err != nil || normalized.ID != canonicalID.String() {
		t.Fatalf("alternate UUID spelling normalized to %q, err=%v", normalized.ID, err)
	}
	alice := ACLPrincipal{TenantID: "bonfire", ID: "alice", Kind: ACLPrincipalUser}
	bob := ACLPrincipal{TenantID: "bonfire", ID: "bob", Kind: ACLPrincipalUser}
	setApprovalRole(eligibility, alice, ApprovalRoleMember, true)
	setApprovalRole(eligibility, bob, ApprovalRoleMember, true)
	if got, err := service.Endorse(context.Background(), created.ID, alice); err != nil || got.Status != ApprovalPending || len(got.Endorsements) != 1 {
		t.Fatalf("first endorsement=%+v err=%v", got, err)
	}
	if got, err := service.Endorse(context.Background(), created.ID, alice); err != nil || got.Status != ApprovalPending || len(got.Endorsements) != 1 {
		t.Fatalf("duplicate endorsement=%+v err=%v", got, err)
	}
	if got, err := service.Endorse(context.Background(), created.ID, bob); err != nil || got.Status != ApprovalApproved || len(got.Endorsements) != 2 {
		t.Fatalf("second distinct endorsement=%+v err=%v", got, err)
	}

	service2, binding2, requester2, eligibility2, _, now2 := approvalTestFixture(t)
	owner := ACLPrincipal{TenantID: "bonfire", ID: "owner", Kind: ACLPrincipalUser}
	setApprovalRole(eligibility2, owner, ApprovalRoleOwner, true)
	created2, _ := service2.Create(context.Background(), "", binding2, requester2, now2.Add(time.Hour))
	if got, err := service2.Endorse(context.Background(), created2.ID, owner); err != nil || got.Status != ApprovalApproved {
		t.Fatalf("owner endorsement=%+v err=%v", got, err)
	}
}

func TestApprovalConsumeBindsPlanRevisionAndDispatchExactlyOnce(t *testing.T) {
	service, binding, requester, eligibility, target, now := approvalTestFixture(t)
	owner := ACLPrincipal{TenantID: "bonfire", ID: "owner", Kind: ACLPrincipalUser}
	setApprovalRole(eligibility, owner, ApprovalRoleAdmin, true)
	created, err := service.Create(context.Background(), "", binding, requester, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Endorse(context.Background(), created.ID, owner); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*ApprovalBinding){
		"revision":    func(value *ApprovalBinding) { value.Revision.ContentRevision++ },
		"digest":      func(value *ApprovalBinding) { value.Revision.ContentDigest = strings.Repeat("c", 64) },
		"action":      func(value *ApprovalBinding) { value.Action = ACLShare },
		"recipient":   func(value *ApprovalBinding) { value.Plan.Recipient = "investor-2" },
		"destination": func(value *ApprovalBinding) { value.Plan.Destination = "drive" },
		"command":     func(value *ApprovalBinding) { value.Plan.Command = "upload approved report" },
		"acl": func(value *ApprovalBinding) {
			value.Target.ACLVersion++
			value.Plan.ACLVersion++
		},
	}
	for name, mutate := range mutations {
		changed := binding
		mutate(&changed)
		if _, _, err := service.Consume(context.Background(), created.ID, changed); !errors.Is(err, ErrApprovalBindingChanged) {
			t.Fatalf("changed %s error=%v, want binding changed", name, err)
		}
	}

	dispatch, fresh, err := service.Consume(context.Background(), created.ID, binding)
	if err != nil || !fresh || dispatch.Status != DispatchPrepared || dispatch.ExecutionKey == "" {
		t.Fatalf("consume dispatch=%+v fresh=%v err=%v", dispatch, fresh, err)
	}
	retry, fresh, err := service.Consume(context.Background(), created.ID, binding)
	if err != nil || fresh || retry.ExecutionKey != dispatch.ExecutionKey {
		t.Fatalf("retry dispatch=%+v fresh=%v err=%v", retry, fresh, err)
	}

	// A content edit after endorsement invalidates the bound approval.
	service2, binding2, requester2, eligibility2, target2, now2 := approvalTestFixture(t)
	setApprovalRole(eligibility2, owner, ApprovalRoleAdmin, true)
	created2, _ := service2.Create(context.Background(), "", binding2, requester2, now2.Add(time.Hour))
	_, _ = service2.Endorse(context.Background(), created2.ID, owner)
	target2.object.CurrentContentRevision++
	target2.object.CurrentContentDigest = strings.Repeat("d", 64)
	if _, _, err := service2.Consume(context.Background(), created2.ID, binding2); !errors.Is(err, ErrApprovalBindingChanged) {
		t.Fatalf("stale revision error=%v", err)
	}
	_ = target
}

func TestApprovalConcurrentConsumeCreatesOneDispatch(t *testing.T) {
	service, binding, requester, eligibility, _, now := approvalTestFixture(t)
	owner := ACLPrincipal{TenantID: "bonfire", ID: "owner", Kind: ACLPrincipalUser}
	setApprovalRole(eligibility, owner, ApprovalRoleOwner, true)
	created, _ := service.Create(context.Background(), "", binding, requester, now.Add(time.Hour))
	_, _ = service.Endorse(context.Background(), created.ID, owner)

	type outcome struct {
		dispatch ApprovalDispatch
		fresh    bool
		err      error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			dispatch, fresh, err := service.Consume(context.Background(), created.ID, binding)
			outcomes <- outcome{dispatch, fresh, err}
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)
	freshCount := 0
	key := ""
	for result := range outcomes {
		if result.err != nil {
			t.Fatalf("concurrent consume: %v", result.err)
		}
		if result.fresh {
			freshCount++
		}
		if key == "" {
			key = result.dispatch.ExecutionKey
		} else if result.dispatch.ExecutionKey != key {
			t.Fatalf("execution keys diverged: %q != %q", result.dispatch.ExecutionKey, key)
		}
	}
	if freshCount != 1 {
		t.Fatalf("fresh dispatches=%d, want 1", freshCount)
	}
}

func TestAmbiguousDispatchCannotBeResentAndOnlyReconcilesTerminal(t *testing.T) {
	service, binding, requester, eligibility, _, now := approvalTestFixture(t)
	owner := ACLPrincipal{TenantID: "bonfire", ID: "owner", Kind: ACLPrincipalUser}
	setApprovalRole(eligibility, owner, ApprovalRoleOwner, true)
	created, _ := service.Create(context.Background(), "", binding, requester, now.Add(time.Hour))
	_, _ = service.Endorse(context.Background(), created.ID, owner)
	_, _, _ = service.Consume(context.Background(), created.ID, binding)
	if _, err := service.TransitionDispatch(context.Background(), created.ID, DispatchSending, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := service.TransitionDispatch(context.Background(), created.ID, DispatchAmbiguous, "", "lost lease after send"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Consume(context.Background(), created.ID, binding); !errors.Is(err, ErrDispatchAmbiguous) {
		t.Fatalf("ambiguous retry error=%v", err)
	}
	if _, err := service.TransitionDispatch(context.Background(), created.ID, DispatchSending, "", ""); !errors.Is(err, ErrApprovalState) {
		t.Fatalf("ambiguous->sending error=%v", err)
	}
	if settled, err := service.TransitionDispatch(context.Background(), created.ID, DispatchConfirmed, "provider-receipt-1", ""); err != nil || settled.Status != DispatchConfirmed {
		t.Fatalf("reconcile settled=%+v err=%v", settled, err)
	}
}
