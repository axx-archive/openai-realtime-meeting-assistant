package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type canonicalMeetingSpecialistTestAuthority struct {
	mu      sync.Mutex
	scope   meetingSpecialistProductScope
	runtime *STRIDERuntime
}

func (authority *canonicalMeetingSpecialistTestAuthority) ResolveScope(_ context.Context, user *userAccount, roomID string) (meetingSpecialistProductScope, error) {
	if user == nil {
		return meetingSpecialistProductScope{}, ErrMeetingSpecialistProductScope
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if normalizeRoomID(roomID) != authority.scope.RoomID {
		return meetingSpecialistProductScope{}, ErrMeetingSpecialistProductScope
	}
	return authority.scope, nil
}

func (authority *canonicalMeetingSpecialistTestAuthority) ResolveControlScope(ctx context.Context, user *userAccount, roomID string) (meetingSpecialistProductScope, error) {
	return authority.ResolveScope(ctx, user, roomID)
}

func (authority *canonicalMeetingSpecialistTestAuthority) EligibleRoster(ctx context.Context, scope meetingSpecialistProductScope) ([]MeetingSpecialistCandidate, error) {
	authority.mu.Lock()
	runtime := authority.runtime
	authority.mu.Unlock()
	return (&appMeetingSpecialistProductAuthority{runtime: runtime}).EligibleRoster(ctx, scope)
}

func (authority *canonicalMeetingSpecialistTestAuthority) ScopeCurrent(_ context.Context, scope meetingSpecialistProductScope) error {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if !sameMeetingSpecialistProductScope(authority.scope, scope) {
		return ErrMeetingSpecialistProductScope
	}
	return nil
}

func (authority *canonicalMeetingSpecialistTestAuthority) setRuntime(runtime *STRIDERuntime) {
	authority.mu.Lock()
	authority.runtime = runtime
	authority.mu.Unlock()
}

func (authority *canonicalMeetingSpecialistTestAuthority) addParticipant(principal string) {
	authority.mu.Lock()
	authority.scope.Audience.Principals = uniqueSortedStrings(append(authority.scope.Audience.Principals, principal))
	authority.mu.Unlock()
}

func explicitMeetingSpecialistScope(now time.Time) meetingSpecialistProductScope {
	return meetingSpecialistProductScope{
		TenantID: "bonfire", RoomID: "dog-perfect", SittingID: "sitting-eligibility", MediaGeneration: 11,
		RequesterPrincipal:    "user:0123456789abcdef01234567",
		Audience:              STRIDEAudience{Visibility: "meeting", Principals: []string{"user:0123456789abcdef01234567", "user:abcdef0123456789abcdef01"}},
		ConsentPolicyRevision: strideTestRef(STRIDEContractKnowledgeAssertion, "consent-policy-eligibility"),
		ConsentFences: []ConsentFence{{
			binding: ConsentAdmissionBinding{TenantID: "bonfire", PrincipalKind: ACLPrincipalUser, PrincipalID: "aj@shareability.com", RoomID: "dog-perfect", SittingID: "sitting-eligibility", AnchorID: "admission-eligibility"},
			lane:    ConsentLaneAudioCapture, policy: "consent-policy-v1", generation: 1, recordDigest: strideTestDigest("9"), issuedAt: now,
		}},
	}
}

func TestMeetingSpecialistEligibilityRequiresExplicitRevisionBoundAssignmentAndSurvivesRevocationRestart(t *testing.T) {
	for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GOOGLE_API_KEY", "GEMINI_API_KEY"} {
		t.Setenv(key, "")
	}
	dir := t.TempDir()
	now := time.Date(2026, 8, 1, 16, 0, 0, 0, time.UTC)
	config := strideIntegratedRuntimeConfig(dir)
	config.ProductPreviewEnabled = true
	config.Now = func() time.Time { return now }
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	admin := STRIDEWorkforceActor{ID: "member_aj", IsAdmin: true}
	agentID := "agent_mary-marketing"
	var agent STRIDEProductTeamAgent
	if err := runtime.WithProductContext("bonfire", STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		var mutationErr error
		agent, mutationErr = ctx.Product.beginTrial("mary-marketing", "member_aj", now)
		if mutationErr != nil {
			return mutationErr
		}
		agent, mutationErr = ctx.Product.mutateAgent(agent.ID, agent.Revision, func(value *STRIDEProductTeamAgent) error {
			value.Status = "hired_fenced"
			value.DirectThreadID = "thread_mary_eligibility"
			value.AccessRevoked = false
			value.Lifecycle = append(value.Lifecycle, "human_approved_hire", "provider_runtime_remains_fenced")
			return nil
		}, now)
		if mutationErr != nil {
			return mutationErr
		}
		seat, installErr := ctx.Workforce.installFencedInternalPreviewSeat(admin, ctx.Receipt, agent, now)
		if installErr != nil {
			return installErr
		}
		if meetingSpecialistContainsString(seat.Memberships, "organization") {
			t.Fatal("hire implicitly granted organization membership")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	scope := explicitMeetingSpecialistScope(now)
	authority := &canonicalMeetingSpecialistTestAuthority{scope: scope, runtime: runtime}
	roster := func(candidateScope meetingSpecialistProductScope) []MeetingSpecialistCandidate {
		t.Helper()
		candidates, rosterErr := authority.EligibleRoster(context.Background(), candidateScope)
		if rosterErr != nil {
			t.Fatalf("eligible roster: %v", rosterErr)
		}
		return candidates
	}
	if candidates := roster(scope); len(candidates) != 0 {
		t.Fatalf("hire alone granted meeting access: %+v", candidates)
	}

	// An exact membership without an explicit assignment is still insufficient.
	if err := runtime.WithProductContext("bonfire", STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		var mutationErr error
		agent, mutationErr = ctx.Product.mutateAgent(agentID, agent.Revision, func(value *STRIDEProductTeamAgent) error {
			value.Config.Memberships = []string{"dog-perfect", "organization"}
			value.Lifecycle = append(value.Lifecycle, "explicit_room_membership_configured")
			return nil
		}, now.Add(time.Minute))
		return mutationErr
	}); err != nil {
		t.Fatal(err)
	}
	if candidates := roster(scope); len(candidates) != 0 {
		t.Fatalf("membership without assignment granted meeting access: %+v", candidates)
	}

	if err := runtime.WithProductContext("bonfire", STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		var mutationErr error
		agent, mutationErr = ctx.Product.mutateAgent(agentID, agent.Revision, func(value *STRIDEProductTeamAgent) error {
			value.Assignments = append(value.Assignments, STRIDEProductAgentAssignment{
				ID: "assignment_mary_dog_perfect", ProjectOrChannel: "dog-perfect", Role: meetingSpecialistAssignmentRole,
				Responsibility: "Join only the Dog Perfect meeting when explicitly invited.", Destination: "dog-perfect", Status: "active_fenced", CreatedAt: now.Add(2 * time.Minute),
			})
			value.Lifecycle = append(value.Lifecycle, "explicit_meeting_assignment_recorded")
			return nil
		}, now.Add(2*time.Minute))
		return mutationErr
	}); err != nil {
		t.Fatal(err)
	}
	eligible := roster(scope)
	if len(eligible) != 1 || !validMeetingSpecialistCandidateForRoom(eligible[0], scope.RoomID) || eligible[0].ProductAgentRevision != agent.Revision {
		t.Fatalf("exact assignment did not create a revision-bound candidate: %+v", eligible)
	}
	wrongRoom := scope
	wrongRoom.RoomID = "other-room"
	if candidates := roster(wrongRoom); len(candidates) != 0 {
		t.Fatalf("wrong room inherited eligibility: %+v", candidates)
	}

	persistence := &MeetingSpecialistProductPersistence{
		SnapshotPath: filepath.Join(dir, "specialists.snapshot.json"), GenerationPath: filepath.Join(dir, "specialists.generation.json"),
		Authority: config.Authority, MinimumGeneration: 1, BootstrapEmpty: true,
	}
	product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: persistence})
	bindMeetingSpecialistAuthorityObserver(runtime, product)
	user := &userAccount{Email: "aj@shareability.com", Name: "AJ"}
	assignmentProvider := installMeetingSpecialistProductionJoin(t, product, now)
	first, err := product.Request(context.Background(), user, scope.RoomID, agentID, "Pressure-test the launch positioning", "eligibility-first", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	bindMeetingSpecialistProductionQualification(t, product, first.ID)
	firstApproved, err := product.Resolve(context.Background(), user, scope.RoomID, first.ID, first.Revision, "approved")
	if err != nil || firstApproved.Status != "joined_session" || !firstApproved.ProviderSessionStarted {
		t.Fatalf("initial joined specialist=%+v err=%v", firstApproved, err)
	}
	product.mu.Lock()
	firstRecord := product.invitations[first.ID]
	product.mu.Unlock()
	if firstRecord.Invitation.Eligibility == nil || firstRecord.Agent.Eligibility == nil || *firstRecord.Invitation.Eligibility != *firstRecord.Agent.Eligibility {
		t.Fatalf("invitation omitted exact eligibility binding: %+v", firstRecord)
	}

	// An assignment change creates a new Product authority revision even when
	// the room remains the same; the older approval cannot follow it.
	if err := runtime.WithProductContext("bonfire", STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		var mutationErr error
		agent, mutationErr = ctx.Product.mutateAgent(agentID, agent.Revision, func(value *STRIDEProductTeamAgent) error {
			value.Assignments = append(value.Assignments, STRIDEProductAgentAssignment{
				ID: "assignment_mary_dog_perfect_v2", ProjectOrChannel: "dog-perfect", Role: meetingSpecialistAssignmentRole,
				Responsibility: "Join the revised Dog Perfect meeting scope only.", Destination: "dog-perfect", Status: "active_fenced", CreatedAt: now.Add(3 * time.Minute),
			})
			value.Lifecycle = append(value.Lifecycle, "meeting_assignment_revised")
			return nil
		}, now.Add(3*time.Minute))
		return mutationErr
	}); err != nil {
		t.Fatal(err)
	}
	product.mu.Lock()
	assignmentRevoked := product.invitations[first.ID]
	product.mu.Unlock()
	if assignmentRevoked.Status != "eligibility_revoked" || assignmentRevoked.Runtime != nil {
		t.Fatalf("assignment mutation did not synchronously revoke prior authority: %+v", assignmentRevoked)
	}
	assignmentProvider.mu.Lock()
	assignmentProviderCloses := assignmentProvider.closed
	assignmentProvider.mu.Unlock()
	if assignmentProviderCloses != 1 {
		t.Fatalf("assignment mutation returned before joined provider closed: closes=%d", assignmentProviderCloses)
	}
	if _, err := product.Resolve(context.Background(), user, scope.RoomID, first.ID, assignmentRevoked.Invitation.Header.Revision, "approved"); !errors.Is(err, ErrMeetingSpecialistProductAgent) {
		t.Fatalf("stale Product revision approval error=%v", err)
	}
	status := product.Status(context.Background(), user, scope.RoomID)
	if len(status.Invitations) != 1 || status.Invitations[0].Status != "eligibility_revoked" {
		t.Fatalf("stale invitation was not terminally revoked: %+v", status)
	}
	// Removing the exact membership revokes discovery even though generic
	// organization membership remains; restoring it produces another revision.
	if err := runtime.WithProductContext("bonfire", STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		var mutationErr error
		agent, mutationErr = ctx.Product.mutateAgent(agentID, agent.Revision, func(value *STRIDEProductTeamAgent) error {
			value.Config.Memberships = []string{"organization"}
			value.Lifecycle = append(value.Lifecycle, "meeting_membership_removed")
			return nil
		}, now.Add(4*time.Minute))
		return mutationErr
	}); err != nil {
		t.Fatal(err)
	}
	if candidates := roster(scope); len(candidates) != 0 {
		t.Fatalf("generic membership retained meeting eligibility: %+v", candidates)
	}
	if err := runtime.WithProductContext("bonfire", STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		var mutationErr error
		agent, mutationErr = ctx.Product.mutateAgent(agentID, agent.Revision, func(value *STRIDEProductTeamAgent) error {
			value.Config.Memberships = []string{"dog-perfect"}
			value.Lifecycle = append(value.Lifecycle, "meeting_membership_restored")
			return nil
		}, now.Add(5*time.Minute))
		return mutationErr
	}); err != nil {
		t.Fatal(err)
	}
	if candidates := roster(scope); len(candidates) != 1 || candidates[0].ProductAgentRevision != agent.Revision {
		t.Fatalf("restored membership did not mint current binding: %+v", candidates)
	}

	capabilityProvider := installMeetingSpecialistProductionJoin(t, product, now)
	second, err := product.Request(context.Background(), user, scope.RoomID, agentID, "Pressure-test the revised positioning", "eligibility-second", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	bindMeetingSpecialistProductionQualification(t, product, second.ID)
	secondApproved, err := product.Resolve(context.Background(), user, scope.RoomID, second.ID, second.Revision, "approved")
	if err != nil || secondApproved.Status != "joined_session" || !secondApproved.ProviderSessionStarted {
		t.Fatalf("capability-bound joined specialist=%+v err=%v", secondApproved, err)
	}
	// A current Workforce capability revision is part of the same binding. A
	// capability update cannot inherit an approval minted for its predecessor.
	if err := runtime.WithProductContext("bonfire", STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		ctx.Workforce.mu.Lock()
		defer ctx.Workforce.mu.Unlock()
		seat := ctx.Workforce.seats[agentID]
		seat.Capability = strideTestRef(STRIDEContractAgentCapabilityManifest, "capability_mary_meeting_v2")
		ctx.Workforce.seats[agentID] = seat
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	product.mu.Lock()
	capabilityRevoked := product.invitations[second.ID]
	product.mu.Unlock()
	if capabilityRevoked.Status != "eligibility_revoked" || capabilityRevoked.Runtime != nil {
		t.Fatalf("Workforce mutation did not synchronously revoke prior authority: %+v", capabilityRevoked)
	}
	capabilityProvider.mu.Lock()
	capabilityProviderCloses := capabilityProvider.closed
	capabilityProvider.mu.Unlock()
	if capabilityProviderCloses != 1 {
		t.Fatalf("Workforce mutation returned before joined provider closed: closes=%d", capabilityProviderCloses)
	}
	if _, err := product.Resolve(context.Background(), user, scope.RoomID, second.ID, capabilityRevoked.Invitation.Header.Revision, "approved"); !errors.Is(err, ErrMeetingSpecialistProductAgent) {
		t.Fatalf("stale Workforce revision approval error=%v", err)
	}
	status = product.Status(context.Background(), user, scope.RoomID)
	workforceRevoked := false
	for _, invitation := range status.Invitations {
		workforceRevoked = workforceRevoked || invitation.ID == second.ID && invitation.Status == "eligibility_revoked"
	}
	if !workforceRevoked {
		t.Fatalf("stale Workforce-bound invitation was not revoked: %+v", status.Invitations)
	}

	third, err := product.Request(context.Background(), user, scope.RoomID, agentID, "Review the current capability positioning", "eligibility-third", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// Participant churn changes the approved audience and revokes the exact
	// invitation before any provider session can be created.
	authority.addParticipant("user:ffffffffffffffffffffffff")
	status = product.Status(context.Background(), user, scope.RoomID)
	participantRevoked := false
	for _, invitation := range status.Invitations {
		participantRevoked = participantRevoked || invitation.ID == third.ID && invitation.Status == "eligibility_revoked" && !invitation.ProviderSessionStarted
	}
	if len(status.Invitations) != 3 || !participantRevoked {
		t.Fatalf("participant churn did not revoke invitation: %+v", status.Invitations)
	}

	joinedProvider := installMeetingSpecialistProductionJoin(t, product, now)
	fourth, err := product.Request(context.Background(), user, scope.RoomID, agentID, "Review the updated participant discussion", "eligibility-fourth", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	bindMeetingSpecialistProductionQualification(t, product, fourth.ID)
	joined, err := product.Resolve(context.Background(), user, scope.RoomID, fourth.ID, fourth.Revision, "approved")
	if err != nil || joined.Status != "joined_session" || !joined.ProviderSessionStarted {
		t.Fatalf("joined specialist=%+v err=%v", joined, err)
	}
	if err := runtime.WithProductContext("bonfire", STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		_, pauseErr := ctx.Workforce.Pause(admin, agentID, "pause_mary_meeting", now.Add(6*time.Minute))
		return pauseErr
	}); err != nil {
		t.Fatal(err)
	}
	product.mu.Lock()
	pausedRecord := product.invitations[fourth.ID]
	product.mu.Unlock()
	if pausedRecord.Status != "eligibility_revoked" || pausedRecord.Runtime != nil {
		t.Fatalf("pause did not synchronously revoke specialist authority: %+v", pausedRecord)
	}
	joinedProvider.mu.Lock()
	providerCloses := joinedProvider.closed
	joinedProvider.mu.Unlock()
	if providerCloses != 1 {
		t.Fatalf("pause returned before joined provider closed: closes=%d", providerCloses)
	}
	status = product.Status(context.Background(), user, scope.RoomID)
	if len(status.Candidates) != 0 {
		t.Fatalf("paused specialist remained eligible: %+v", status.Candidates)
	}
	for _, invitation := range status.Invitations {
		if invitation.ID == fourth.ID && (invitation.Status != "eligibility_revoked" || invitation.ProviderSessionStarted) {
			t.Fatalf("pause did not revoke active invitation: %+v", invitation)
		}
	}
	if err := runtime.WithProductContext("bonfire", STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		_, offboardErr := ctx.Workforce.Offboard(admin, agentID, "offboard_mary_meeting", now.Add(7*time.Minute))
		return offboardErr
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	config.BootstrapEmpty = false
	restartedRuntime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restartedRuntime.Close() })
	authority.setRuntime(restartedRuntime)
	restoreConfig := *persistence
	restoreConfig.BootstrapEmpty = false
	restartedProduct := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restoreConfig})
	restartedStatus := restartedProduct.Status(context.Background(), user, scope.RoomID)
	if restartedStatus.CanInvite || len(restartedStatus.Candidates) != 0 {
		t.Fatalf("offboarded specialist resurrected after restart: %+v", restartedStatus)
	}
	for _, invitation := range restartedStatus.Invitations {
		if invitation.ProviderSessionStarted || invitation.Status == "awaiting_approval" || invitation.Status == "joined_session" {
			t.Fatalf("restart resurrected specialist authority: %+v", invitation)
		}
	}
}
