package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeMeetingSpecialistProductAuthority struct {
	mu            sync.Mutex
	scope         meetingSpecialistProductScope
	roster        []MeetingSpecialistCandidate
	current       bool
	control       bool
	eligibleCalls int
	onEligible    func(int)
}

func specialistCandidateFixture(agentID, roomID string) MeetingSpecialistCandidate {
	assignment := strideTestRef(STRIDEContractAgentAssignment, "assignment-"+agentID+"-"+roomID)
	assignment.Revision = 7
	eligibility := strideTestRef(STRIDEContractAgentAssignment, "meeting_eligibility_"+temporalDigest(agentID + "\x00" + roomID)[:20])
	eligibility.Revision = assignment.Revision
	candidate := MeetingSpecialistCandidate{
		AgentID: agentID, DisplayName: "Mary", Profile: strideTestRef(STRIDEContractAgentCoreProfile, "profile-"+agentID),
		Capability: strideTestRef(STRIDEContractAgentCapabilityManifest, "capability-"+agentID), Assignment: &assignment, Eligibility: &eligibility,
		RoomID: roomID, ProductAgentRevision: assignment.Revision, WorkforceRevisionDigest: strideTestDigest("8"),
	}
	material := meetingSpecialistEligibilityMaterial{
		AgentID: candidate.AgentID, RoomID: roomID, ProductAgentRevision: candidate.ProductAgentRevision, WorkforceRevisionDigest: candidate.WorkforceRevisionDigest,
		Assignment: assignment, Profile: candidate.Profile, Capability: candidate.Capability,
	}
	eligibility.Digest, _ = STRIDEContractDigest(material)
	candidate.Eligibility = &eligibility
	return candidate
}

func (authority *fakeMeetingSpecialistProductAuthority) ResolveScope(_ context.Context, user *userAccount, roomID string) (meetingSpecialistProductScope, error) {
	if user == nil || roomID != authority.scope.RoomID {
		return meetingSpecialistProductScope{}, ErrMeetingSpecialistProductScope
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if !authority.current {
		return meetingSpecialistProductScope{}, ErrMeetingSpecialistProductScope
	}
	return authority.scope, nil
}

func (authority *fakeMeetingSpecialistProductAuthority) ResolveControlScope(ctx context.Context, user *userAccount, roomID string) (meetingSpecialistProductScope, error) {
	_ = ctx
	if user == nil || roomID != authority.scope.RoomID {
		return meetingSpecialistProductScope{}, ErrMeetingSpecialistProductScope
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if !authority.control {
		return meetingSpecialistProductScope{}, ErrMeetingSpecialistProductScope
	}
	return authority.scope, nil
}

func (authority *fakeMeetingSpecialistProductAuthority) EligibleRoster(context.Context, meetingSpecialistProductScope) ([]MeetingSpecialistCandidate, error) {
	authority.mu.Lock()
	authority.eligibleCalls++
	call, hook := authority.eligibleCalls, authority.onEligible
	roster := append([]MeetingSpecialistCandidate(nil), authority.roster...)
	authority.mu.Unlock()
	if hook != nil {
		hook(call)
	}
	return roster, nil
}

func (authority *fakeMeetingSpecialistProductAuthority) ScopeCurrent(context.Context, meetingSpecialistProductScope) error {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if !authority.current {
		return ErrMeetingSpecialistProductScope
	}
	return nil
}

func specialistProductFixture(t *testing.T) (*MeetingSpecialistProduct, *fakeMeetingSpecialistProductAuthority, *userAccount) {
	t.Helper()
	now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
	authority := &fakeMeetingSpecialistProductAuthority{
		current: true,
		control: true,
		scope: meetingSpecialistProductScope{
			TenantID: "bonfire", RoomID: "dog-perfect", SittingID: "sitting-1", MediaGeneration: 7,
			RequesterPrincipal:    "user:0123456789abcdef01234567",
			Audience:              STRIDEAudience{Visibility: "meeting", Principals: []string{"user:0123456789abcdef01234567", "user:abcdef0123456789abcdef01"}},
			ConsentPolicyRevision: strideTestRef(STRIDEContractKnowledgeAssertion, "consent-policy-1"),
			ConsentFences: []ConsentFence{{
				binding: ConsentAdmissionBinding{TenantID: "bonfire", PrincipalKind: ACLPrincipalUser, PrincipalID: "aj@shareability.com", RoomID: "dog-perfect", SittingID: "sitting-1", AnchorID: "admission-1"},
				lane:    ConsentLaneAudioCapture, policy: "consent-policy-v1", generation: 1, recordDigest: strideTestDigest("9"), issuedAt: now,
			}},
		},
		roster: []MeetingSpecialistCandidate{specialistCandidateFixture("mary", "dog-perfect")},
	}
	product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, Now: func() time.Time { return now }, Authority: authority})
	return product, authority, &userAccount{Email: "aj@shareability.com", Name: "AJ"}
}

func installMeetingSpecialistProductionJoin(t *testing.T, product *MeetingSpecialistProduct, now time.Time) *fakeMeetingSpecialistProvider {
	t.Helper()
	provider := &fakeMeetingSpecialistProvider{}
	var factoryCalls atomic.Int64
	var scope meetingSpecialistProductScope
	switch authority := product.authority.(type) {
	case *fakeMeetingSpecialistProductAuthority:
		authority.mu.Lock()
		scope = authority.scope
		authority.mu.Unlock()
	case *canonicalMeetingSpecialistTestAuthority:
		authority.mu.Lock()
		scope = authority.scope
		authority.mu.Unlock()
	}
	qualificationTarget := meetingSpecialistQualificationFixture()
	if roster, err := product.authority.EligibleRoster(context.Background(), scope); err == nil && len(roster) > 0 {
		qualificationTarget = meetingSpecialistQualificationFixtureForCandidate(scope.TenantID, roster[0])
	}
	joiner, _ := productionJoinFixtureForQualification(t, now, now.Add(-time.Minute), qualificationTarget, provider, &factoryCalls)
	product.productionJoin = joiner
	return provider
}

func bindMeetingSpecialistProductionQualification(t *testing.T, product *MeetingSpecialistProduct, invitationID string) {
	t.Helper()
	product.mu.Lock()
	record, found := product.invitations[invitationID]
	product.mu.Unlock()
	if !found || product.productionJoin == nil || product.productionJoin.qualifiedProvider == nil {
		t.Fatalf("qualification binding source missing for %s", invitationID)
	}
	request := product.productionJoin.qualifiedProvider.request
	if request.TenantID != record.Scope.TenantID || request.SpecialistProfile != record.Agent.Profile || request.SpecialistCapability != record.Agent.Capability || !product.productionJoin.Ready() {
		t.Fatalf("production qualification is not bound to invitation %s", invitationID)
	}
}
func meetingSpecialistProviderReceiptFixture(subjectDigest string) MeetingSpecialistProviderReceipt {
	return MeetingSpecialistProviderReceipt{
		QualificationSubjectDigest: subjectDigest, BindingDigest: strideTestDigest("1"), RequestDigest: strideTestDigest("2"), SessionIDHash: strideTestDigest("3"),
		Model: "gpt-realtime-2.1", ReasoningEffort: "low", EventDigest: strideTestDigest("4"), EventCount: 3,
		UsageDigest: strideTestDigest("5"), UsageStatus: "reconciled", TerminalEventHash: strideTestDigest("6"), TerminalStatus: "failed", SessionFailureHash: strideTestDigest("7"),
		InputTokens: 10, OutputTokens: 20, OutputAudioTokens: 10, ReconciledCostCent: 2,
		ProtocolSource: meetingSpecialistRealtimeProtocolSource, ModelSource: meetingSpecialistRealtimeModelSource, ContractDigest: strideTestDigest("8"),
	}
}

func TestMeetingSpecialistProductApprovalIsRevisionBoundAndProviderFenced(t *testing.T) {
	product, _, user := specialistProductFixture(t)
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Help us pressure-test positioning", "request-1", 5*time.Minute)
	if err != nil || requested.Revision != 1 || requested.Status != "awaiting_approval" {
		t.Fatalf("requested=%+v err=%v", requested, err)
	}
	approved, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved")
	if err != nil || approved.Revision != 2 || approved.Status != "approved_waiting_for_provider_qualification" {
		t.Fatalf("approved=%+v err=%v", approved, err)
	}
	product.mu.Lock()
	record := product.invitations[requested.ID]
	product.mu.Unlock()
	if record.Runtime != nil {
		t.Fatalf("waiting approval retained a runtime/factory placeholder: %+v", record.Runtime)
	}
	if _, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "dismissed"); !errors.Is(err, ErrMeetingSpecialistProductRevision) {
		t.Fatalf("stale resolution err=%v", err)
	}
	dismissed, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, approved.Revision, "dismissed")
	if err != nil || dismissed.Revision != 3 || dismissed.Status != "dismissed" {
		t.Fatalf("dismissed=%+v err=%v", dismissed, err)
	}
	product.mu.Lock()
	record = product.invitations[requested.ID]
	product.mu.Unlock()
	if record.Runtime != nil {
		t.Fatal("dismissal retained the specialist runtime")
	}
}

func TestMeetingSpecialistProductConsentFenceRemintAndOrderReplayOneInvitation(t *testing.T) {
	product, authority, user := specialistProductFixture(t)
	authority.mu.Lock()
	firstFence := authority.scope.ConsentFences[0]
	secondFence := firstFence
	secondFence.binding.PrincipalID = "tim@shareability.com"
	secondFence.binding.AnchorID = "admission-2"
	secondFence.lane = ConsentLaneTranscription
	secondFence.recordDigest = strideTestDigest("a")
	secondFence.issuedAt = firstFence.issuedAt.Add(time.Millisecond)
	authority.scope.ConsentFences = []ConsentFence{firstFence, secondFence}
	authority.mu.Unlock()

	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "consent-remint", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	authority.mu.Lock()
	remintedFirst, remintedSecond := firstFence, secondFence
	remintedFirst.issuedAt = remintedFirst.issuedAt.Add(10 * time.Second)
	remintedSecond.issuedAt = remintedSecond.issuedAt.Add(20 * time.Second)
	// Provider resolution may return the same authority set in another order.
	authority.scope.ConsentFences = []ConsentFence{remintedSecond, remintedFirst}
	authority.mu.Unlock()

	replayed, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "consent-remint", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	product.mu.Lock()
	record := product.invitations[requested.ID]
	invitationCount := len(product.invitations)
	product.mu.Unlock()
	if replayed.ID != requested.ID || replayed.Revision != requested.Revision || invitationCount != 1 {
		t.Fatalf("same consent authority minted another invitation: first=%+v replay=%+v count=%d", requested, replayed, invitationCount)
	}
	durable := durableMeetingSpecialistScope(record.Scope)
	if len(durable.ConsentFences) != 2 || !durable.ConsentFences[0].IssuedAt.Equal(firstFence.issuedAt) || !durable.ConsentFences[1].IssuedAt.Equal(secondFence.issuedAt) {
		t.Fatalf("audit mint times were not preserved: %+v", durable.ConsentFences)
	}
}

func TestMeetingSpecialistProductConsentAuthorityRevisionRequiresFreshInvitation(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		mutate func(*ConsentFence)
	}{
		{name: "generation", mutate: func(fence *ConsentFence) { fence.generation++ }},
		{name: "record digest", mutate: func(fence *ConsentFence) { fence.recordDigest = strideTestDigest("a") }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			product, authority, user := specialistProductFixture(t)
			requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "consent-regrant", time.Minute)
			if err != nil {
				t.Fatal(err)
			}

			authority.mu.Lock()
			fences := append([]ConsentFence(nil), authority.scope.ConsentFences...)
			fence := fences[0]
			scenario.mutate(&fence)
			fence.issuedAt = fence.issuedAt.Add(time.Second)
			fences[0] = fence
			authority.scope.ConsentFences = fences
			authority.mu.Unlock()

			if _, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved"); !errors.Is(err, ErrMeetingSpecialistProductScope) {
				t.Fatalf("stale consent invitation approval err=%v", err)
			}
			fresh, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "consent-regrant", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if fresh.ID == requested.ID || fresh.Revision != 1 || fresh.Status != "awaiting_approval" {
				t.Fatalf("consent authority revision replayed prior invitation: old=%+v fresh=%+v", requested, fresh)
			}
			product.mu.Lock()
			prior := product.invitations[requested.ID]
			product.mu.Unlock()
			if prior.Status != "eligibility_revoked" || prior.Runtime != nil {
				t.Fatalf("stale consent invitation remained active: %+v", prior)
			}
		})
	}
}

func TestMeetingSpecialistProductRequestReauthorizesAfterInitialRosterRead(t *testing.T) {
	product, authority, user := specialistProductFixture(t)
	firstRosterRead := make(chan struct{})
	continueRequest := make(chan struct{})
	authority.mu.Lock()
	authority.onEligible = func(call int) {
		if call == 1 {
			close(firstRosterRead)
			<-continueRequest
		}
	}
	authority.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "request-roster-race", time.Minute)
		done <- err
	}()
	<-firstRosterRead
	authority.mu.Lock()
	authority.roster = nil
	authority.mu.Unlock()
	if err := product.RevokeAgentAuthority("mary", "agent_authority_changed"); err != nil {
		t.Fatal(err)
	}
	close(continueRequest)
	if err := <-done; !errors.Is(err, ErrMeetingSpecialistProductAgent) {
		t.Fatalf("stale roster request err=%v", err)
	}
	product.mu.Lock()
	invitationCount := len(product.invitations)
	product.mu.Unlock()
	if invitationCount != 0 {
		t.Fatalf("stale roster request persisted %d invitations", invitationCount)
	}
}

func TestMeetingSpecialistProductAuthorityMutationClosesJoinedSessionBeforePoll(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		wantReason string
		revoke     func(*testing.T, *MeetingSpecialistProduct) error
	}{
		{name: "agent revision", wantReason: "agent_authority_changed", revoke: func(_ *testing.T, product *MeetingSpecialistProduct) error {
			return product.RevokeAgentAuthority("mary", "agent_authority_changed")
		}},
		{name: "meeting authority", wantReason: "meeting_authority_changed", revoke: func(_ *testing.T, product *MeetingSpecialistProduct) error {
			return product.RevokeScopeAuthority("dog-perfect", "sitting-1", "meeting_authority_changed")
		}},
		{name: "participant roster", wantReason: "participant_authority_changed", revoke: func(t *testing.T, product *MeetingSpecialistProduct) error {
			app := &kanbanBoardApp{meetingSpecialists: product}
			_, firstEndpoint, err := app.admitParticipantSessionEndpointInRoom("dog-perfect", "Tim", "participant-session", "participant-endpoint")
			if err == nil && !firstEndpoint {
				t.Fatal("participant mutation did not add a new roster principal")
			}
			return err
		}},
		{name: "consent generation", wantReason: "consent_authority_changed", revoke: func(_ *testing.T, product *MeetingSpecialistProduct) error {
			bindMeetingSpecialistConsentObserver(product)
			consent := NewConsentLaneAuthority(NewMemoryConsentStore(), "consent-policy-v1")
			consent.OnDecision = handleConsentDecision
			_, err := consent.RecordDecision(context.Background(), ConsentAdmissionBinding{
				TenantID: "bonfire", PrincipalKind: ACLPrincipalGuest, PrincipalID: strings.Repeat("a", 64), RoomID: "dog-perfect", SittingID: "sitting-1", AnchorID: "admission-1",
			}, ConsentAudioCapture, ConsentGranted)
			return err
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, authority, user := specialistProductFixture(t)
			now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
			dir := t.TempDir()
			persistence := &MeetingSpecialistProductPersistence{
				SnapshotPath: filepath.Join(dir, "specialists.snapshot.json"), GenerationPath: filepath.Join(dir, "specialists.generation.json"),
				Authority: STRIDESnapshotMACAuthority{KeyID: "specialist_immediate_revoke", Key: []byte("0123456789abcdef0123456789abcdef")}, MinimumGeneration: 1, BootstrapEmpty: true,
			}
			provider := &fakeMeetingSpecialistProvider{}
			product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: persistence})
			defer product.Close("test_complete")
			var factoryCalls atomic.Int64
			product.productionJoin, _ = productionJoinFixture(t, now, provider, &factoryCalls)

			requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "immediate-revoke-"+strings.ReplaceAll(scenario.name, " ", "-"), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			approved, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved")
			if err != nil || approved.Status != "joined_session" {
				t.Fatalf("approval=%+v err=%v", approved, err)
			}
			product.mu.Lock()
			joinedRuntime := product.invitations[requested.ID].Runtime
			product.mu.Unlock()
			if joinedRuntime == nil {
				t.Fatal("joined runtime missing before revocation")
			}
			if err := scenario.revoke(t, product); err != nil {
				t.Fatal(err)
			}
			provider.mu.Lock()
			closed := provider.closed
			provider.mu.Unlock()
			product.mu.Lock()
			record := product.invitations[requested.ID]
			product.mu.Unlock()
			if closed != 1 || record.Runtime != nil || record.Status != "eligibility_revoked" {
				t.Fatalf("revocation was not synchronous: closes=%d record=%+v", closed, record)
			}
			snapshot := joinedRuntime.Snapshot()
			if snapshot.Session != nil || snapshot.TeardownReceiptDigest == "" || snapshot.TerminalReason != "production-session-mary\x00"+scenario.wantReason {
				t.Fatalf("revocation receipt did not preserve cause: %+v", snapshot)
			}

			restore := *persistence
			restore.BootstrapEmpty = false
			restarted := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restore})
			restarted.mu.Lock()
			restored := restarted.invitations[requested.ID]
			restarted.mu.Unlock()
			if restored.Runtime != nil || restored.Status != "eligibility_revoked" {
				t.Fatalf("restart resurrected revoked session: %+v", restored)
			}
		})
	}
}

func TestMeetingSpecialistProductStatusPersistenceFailureStillRevokesDetachedRuntime(t *testing.T) {
	_, authority, user := specialistProductFixture(t)
	now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	persistence := &MeetingSpecialistProductPersistence{
		SnapshotPath: filepath.Join(dir, "specialists.snapshot.json"), GenerationPath: filepath.Join(dir, "specialists.generation.json"),
		Authority: STRIDESnapshotMACAuthority{KeyID: "specialist_status_revoke", Key: []byte("0123456789abcdef0123456789abcdef")}, MinimumGeneration: 1, BootstrapEmpty: true,
	}
	product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: persistence})
	provider := installMeetingSpecialistProductionJoin(t, product, now)
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "status-persist-failure", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved")
	if err != nil || approved.Status != "joined_session" {
		t.Fatalf("approval=%+v err=%v", approved, err)
	}
	product.mu.Lock()
	joinedRuntime := product.invitations[requested.ID].Runtime
	product.mu.Unlock()
	if joinedRuntime == nil || joinedRuntime.Snapshot().Session == nil {
		t.Fatal("joined runtime missing before forced persistence failure")
	}

	if err := os.Remove(persistence.GenerationPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(persistence.GenerationPath, 0o700); err != nil {
		t.Fatal(err)
	}
	authority.mu.Lock()
	authority.roster = nil
	authority.mu.Unlock()
	status := product.Status(context.Background(), user, "dog-perfect")
	if status.Reason != "state_restore_failed" || len(status.Candidates) != 0 || len(status.Invitations) != 0 {
		t.Fatalf("fail-closed status=%+v", status)
	}
	provider.mu.Lock()
	closed := provider.closed
	provider.mu.Unlock()
	product.mu.Lock()
	record := product.invitations[requested.ID]
	enabled, healthErr := product.enabled, product.healthErr
	product.mu.Unlock()
	if closed != 1 || joinedRuntime.Snapshot().Session != nil || record.Runtime != nil || enabled || healthErr == nil {
		t.Fatalf("detached runtime survived persistence failure: closes=%d snapshot=%+v record=%+v enabled=%v health=%v", closed, joinedRuntime.Snapshot(), record, enabled, healthErr)
	}
}

func TestMeetingSpecialistProductAllowsAtMostOneActiveSpecialistPerSitting(t *testing.T) {
	product, authority, user := specialistProductFixture(t)
	authority.mu.Lock()
	authority.roster = append(authority.roster, specialistCandidateFixture("researcher", "dog-perfect"))
	authority.mu.Unlock()
	type result struct {
		view meetingSpecialistInvitationView
		err  error
	}
	results := make(chan result, 2)
	for _, agentID := range []string{"mary", "researcher"} {
		agentID := agentID
		go func() {
			view, err := product.Request(context.Background(), user, "dog-perfect", agentID, "Review campaign", "one-specialist-"+agentID, time.Minute)
			results <- result{view: view, err: err}
		}()
	}
	succeeded, conflicted := 0, 0
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			succeeded++
		case errors.Is(result.err, ErrMeetingAgentFloorOccupied):
			conflicted++
		default:
			t.Fatalf("unexpected concurrent request result=%+v", result)
		}
	}
	product.mu.Lock()
	active := 0
	for _, record := range product.invitations {
		if meetingSpecialistInvitationIsActive(record) {
			active++
		}
	}
	product.mu.Unlock()
	if succeeded != 1 || conflicted != 1 || active != 1 {
		t.Fatalf("specialist concurrency successes=%d conflicts=%d active=%d", succeeded, conflicted, active)
	}
}

func TestMeetingSpecialistProductRequesterNeutralSittingAuthority(t *testing.T) {
	product, authority, user := specialistProductFixture(t)
	defer product.Close("test_cleanup")
	now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
	provider := installMeetingSpecialistProductionJoin(t, product, now)
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "requester-neutral-owner", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved"); err != nil {
		t.Fatal(err)
	}
	product.mu.Lock()
	runtime := product.invitations[requested.ID].Runtime
	product.mu.Unlock()
	if runtime == nil {
		t.Fatal("joined runtime missing")
	}

	// The second audience member is fully authorized in the same sitting, but is
	// not the invitation requester/confirmer. Reading the shared status and making
	// a competing request must not be interpreted as an authority revision.
	authority.mu.Lock()
	authority.scope.RequesterPrincipal = "user:abcdef0123456789abcdef01"
	authority.roster = append(authority.roster, specialistCandidateFixture("researcher", "dog-perfect"))
	authority.mu.Unlock()
	otherMember := &userAccount{Email: "erick@example.com", Name: "Erick"}
	status := product.Status(context.Background(), otherMember, "dog-perfect")
	product.mu.Lock()
	record := product.invitations[requested.ID]
	product.mu.Unlock()
	provider.mu.Lock()
	closed := provider.closed
	provider.mu.Unlock()
	if len(status.Invitations) != 1 || record.Runtime != runtime || record.Status != "joined_session" || closed != 0 || runtime.Snapshot().Session == nil {
		t.Fatalf("second member status revoked owner session: status=%+v record=%+v closes=%d snapshot=%+v", status, record, closed, runtime.Snapshot())
	}
	if _, err := product.Request(context.Background(), otherMember, "dog-perfect", "researcher", "Research campaign", "requester-neutral-competitor", time.Minute); !errors.Is(err, ErrMeetingAgentFloorOccupied) {
		t.Fatalf("competing member request err=%v, want occupied", err)
	}
	product.mu.Lock()
	record = product.invitations[requested.ID]
	product.mu.Unlock()
	provider.mu.Lock()
	closed = provider.closed
	provider.mu.Unlock()
	if record.Runtime != runtime || record.Status != "joined_session" || closed != 0 {
		t.Fatalf("competing member request revoked owner session: record=%+v closes=%d", record, closed)
	}
}

func TestMeetingSpecialistProductExpirationIsDurableAndReleasesSeat(t *testing.T) {
	t.Run("live joined invitation", func(t *testing.T) {
		_, authority, user := specialistProductFixture(t)
		now := time.Date(2026, 8, 1, 21, 0, 0, 0, time.UTC)
		dir := t.TempDir()
		persistence := &MeetingSpecialistProductPersistence{
			SnapshotPath: filepath.Join(dir, "specialists.snapshot.json"), GenerationPath: filepath.Join(dir, "specialists.generation.json"),
			Authority: STRIDESnapshotMACAuthority{KeyID: "specialist_expiry_live", Key: []byte("0123456789abcdef0123456789abcdef")}, MinimumGeneration: 1, BootstrapEmpty: true,
		}
		product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: persistence})
		provider := installMeetingSpecialistProductionJoin(t, product, now)
		requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "expiry-live", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved"); err != nil {
			t.Fatal(err)
		}
		product.mu.Lock()
		runtime := product.invitations[requested.ID].Runtime
		product.mu.Unlock()
		now = now.Add(2 * time.Minute)
		status := product.Status(context.Background(), user, "dog-perfect")
		product.mu.Lock()
		expired := product.invitations[requested.ID]
		product.mu.Unlock()
		provider.mu.Lock()
		closed := provider.closed
		provider.mu.Unlock()
		snapshot := runtime.Snapshot()
		if len(status.Invitations) != 1 || expired.Status != "expired" || expired.Invitation.Decision != "expired" || expired.Invitation.Header.Revision != requested.Revision+2 || expired.Invitation.DecisionAt == nil || !expired.Invitation.DecisionAt.Equal(now) || expired.Runtime != nil || meetingSpecialistInvitationIsActive(expired) || closed != 1 || snapshot.Session != nil || snapshot.TeardownReceiptDigest == "" || snapshot.TerminalReason != "production-session-mary\x00expired" || expired.TerminalEvidence == nil || expired.TerminalEvidence.TerminalReason != "expired" || expired.TerminalEvidence.Cause != "expired" || expired.TerminalEvidence.TeardownReceiptDigest != snapshot.TeardownReceiptDigest {
			t.Fatalf("live expiry was not terminal: status=%+v record=%+v closes=%d snapshot=%+v", status, expired, closed, snapshot)
		}
		replayed, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "expiry-live", time.Minute)
		if err != nil || replayed.ID != requested.ID || replayed.Status != "expired" || replayed.Decision != "expired" || replayed.Revision != expired.Invitation.Header.Revision {
			t.Fatalf("expired idempotency replay lost terminal truth: replay=%+v err=%v", replayed, err)
		}
		fresh, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign again", "expiry-live-fresh", time.Minute)
		if err != nil || fresh.ID == requested.ID || fresh.Status != "awaiting_approval" {
			t.Fatalf("expired invitation blocked fresh request: fresh=%+v err=%v", fresh, err)
		}
		restore := *persistence
		restore.BootstrapEmpty = false
		restarted := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restore})
		restarted.mu.Lock()
		restoredExpired, restoredFresh, healthErr := restarted.invitations[requested.ID], restarted.invitations[fresh.ID], restarted.healthErr
		restarted.mu.Unlock()
		if healthErr != nil || restoredExpired.Status != "expired" || restoredExpired.Invitation.Decision != "expired" || restoredExpired.TerminalEvidence == nil || restoredExpired.TerminalEvidence.TerminalReason != "expired" || restoredExpired.TerminalEvidence.TeardownReceiptDigest == "" || restoredFresh.Status != "awaiting_approval" {
			t.Fatalf("durable expiry restart state: expired=%+v fresh=%+v health=%v", restoredExpired, restoredFresh, healthErr)
		}
	})

	t.Run("restart normalizes stale active snapshot once", func(t *testing.T) {
		_, authority, user := specialistProductFixture(t)
		now := time.Date(2026, 8, 1, 22, 0, 0, 0, time.UTC)
		dir := t.TempDir()
		persistence := &MeetingSpecialistProductPersistence{
			SnapshotPath: filepath.Join(dir, "specialists.snapshot.json"), GenerationPath: filepath.Join(dir, "specialists.generation.json"),
			Authority: STRIDESnapshotMACAuthority{KeyID: "specialist_expiry_restore", Key: []byte("0123456789abcdef0123456789abcdef")}, MinimumGeneration: 1, BootstrapEmpty: true,
		}
		product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: persistence})
		requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "expiry-restore", time.Minute)
		if err != nil || product.generation != 1 {
			t.Fatalf("initial request=%+v err=%v generation=%d", requested, err, product.generation)
		}
		now = now.Add(2 * time.Minute)
		restore := *persistence
		restore.BootstrapEmpty = false
		restarted := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restore})
		restarted.mu.Lock()
		record, generation, healthErr := restarted.invitations[requested.ID], restarted.generation, restarted.healthErr
		restarted.mu.Unlock()
		if healthErr != nil || record.Status != "expired" || record.Invitation.Decision != "expired" || record.Invitation.Header.Revision != requested.Revision+1 || record.Invitation.DecisionAt == nil || !record.Invitation.DecisionAt.Equal(now) || generation != 2 {
			t.Fatalf("restore did not durably normalize expiry: record=%+v generation=%d health=%v", record, generation, healthErr)
		}
		secondRestart := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restore})
		secondRestart.mu.Lock()
		record, generation, healthErr = secondRestart.invitations[requested.ID], secondRestart.generation, secondRestart.healthErr
		secondRestart.mu.Unlock()
		if healthErr != nil || record.Status != "expired" || record.Invitation.Decision != "expired" || record.Invitation.Header.Revision != requested.Revision+1 || generation != 2 {
			t.Fatalf("second restore replayed expiry transition: record=%+v generation=%d health=%v", record, generation, healthErr)
		}
		if fresh, err := secondRestart.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign again", "expiry-restore-fresh", time.Minute); err != nil || fresh.ID == requested.ID {
			t.Fatalf("restored expiry blocked fresh request: fresh=%+v err=%v", fresh, err)
		}
	})
}

func TestMeetingSpecialistProductRejectsMultipleActiveRecordsOnResolveAndRestore(t *testing.T) {
	duplicateRecord := func(t *testing.T, record meetingSpecialistProductRecord) (string, meetingSpecialistProductRecord) {
		t.Helper()
		id := "specialist_invitation_duplicate_active"
		record.Invitation.Header.ID = id
		record.Invitation.IdempotencyKeyDigest = sha256Hex([]byte("duplicate-active-idempotency"))
		digest, err := meetingSpecialistInvitationDigest(record.Invitation)
		if err != nil {
			t.Fatal(err)
		}
		record.Invitation.Header.ContentDigest = digest
		record.Runtime = nil
		return id, record
	}

	t.Run("resolve", func(t *testing.T) {
		product, _, user := specialistProductFixture(t)
		requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "duplicate-resolve", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		product.mu.Lock()
		id, duplicate := duplicateRecord(t, product.invitations[requested.ID])
		product.invitations[id] = duplicate
		product.mu.Unlock()
		if _, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved"); !errors.Is(err, ErrMeetingAgentFloorOccupied) {
			t.Fatalf("approval with competing active record err=%v", err)
		}
	})

	t.Run("signed restore", func(t *testing.T) {
		_, authority, user := specialistProductFixture(t)
		now := time.Date(2026, 8, 1, 23, 0, 0, 0, time.UTC)
		dir := t.TempDir()
		persistence := &MeetingSpecialistProductPersistence{
			SnapshotPath: filepath.Join(dir, "specialists.snapshot.json"), GenerationPath: filepath.Join(dir, "specialists.generation.json"),
			Authority: STRIDESnapshotMACAuthority{KeyID: "specialist_duplicate_restore", Key: []byte("0123456789abcdef0123456789abcdef")}, MinimumGeneration: 1, BootstrapEmpty: true,
		}
		product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: persistence})
		requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "duplicate-restore", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		product.mu.Lock()
		id, duplicate := duplicateRecord(t, product.invitations[requested.ID])
		product.invitations[id] = duplicate
		err = product.persistLocked()
		product.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		restore := *persistence
		restore.BootstrapEmpty = false
		restarted := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restore})
		if got := restarted.Status(context.Background(), user, "dog-perfect"); got.Reason != "state_restore_failed" {
			t.Fatalf("duplicate active restore did not fail closed: %+v", got)
		}
	})
}

func TestMeetingSpecialistProductMemberOnlyScopeAndGuestChurn(t *testing.T) {
	t.Run("initial guest blocks request", func(t *testing.T) {
		product, authority, user := specialistProductFixture(t)
		authority.mu.Lock()
		authority.scope.Audience.Principals = append(authority.scope.Audience.Principals, "guest:0123456789abcdef01234567")
		guestFence := authority.scope.ConsentFences[0]
		guestFence.binding.PrincipalKind = ACLPrincipalGuest
		guestFence.binding.PrincipalID = strings.Repeat("a", 64)
		authority.scope.ConsentFences = append(authority.scope.ConsentFences, guestFence)
		authority.mu.Unlock()
		if _, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "guest-block", time.Minute); !errors.Is(err, ErrMeetingSpecialistProductScope) {
			t.Fatalf("guest-present request err=%v", err)
		}
		product.mu.Lock()
		count := len(product.invitations)
		product.mu.Unlock()
		if count != 0 {
			t.Fatalf("guest-present request persisted %d invitations", count)
		}
	})

	t.Run("guest churn revokes joined runtime", func(t *testing.T) {
		product, authority, user := specialistProductFixture(t)
		now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
		provider := installMeetingSpecialistProductionJoin(t, product, now)
		requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "guest-churn", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		approved, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved")
		if err != nil || approved.Status != "joined_session" {
			t.Fatalf("approval=%+v err=%v", approved, err)
		}
		product.mu.Lock()
		runtime := product.invitations[requested.ID].Runtime
		product.mu.Unlock()
		if runtime == nil {
			t.Fatal("joined runtime missing")
		}
		authority.mu.Lock()
		authority.scope.Audience.Principals = append(authority.scope.Audience.Principals, "guest:0123456789abcdef01234567")
		guestFence := authority.scope.ConsentFences[0]
		guestFence.binding.PrincipalKind = ACLPrincipalGuest
		guestFence.binding.PrincipalID = strings.Repeat("b", 64)
		authority.scope.ConsentFences = append(authority.scope.ConsentFences, guestFence)
		authority.mu.Unlock()
		status := product.Status(context.Background(), user, "dog-perfect")
		provider.mu.Lock()
		closed := provider.closed
		provider.mu.Unlock()
		product.mu.Lock()
		record := product.invitations[requested.ID]
		product.mu.Unlock()
		snapshot := runtime.Snapshot()
		if status.Reason != "active_member_room_required" || closed != 1 || record.Runtime != nil || record.Status != "eligibility_revoked" || record.TerminalEvidence == nil || record.TerminalEvidence.TerminalReason != "guest_participant" || record.TerminalEvidence.Cause != "guest_participant" || record.TerminalEvidence.TeardownReceiptDigest != snapshot.TeardownReceiptDigest || snapshot.Session != nil || snapshot.TeardownReceiptDigest == "" || snapshot.TerminalReason != "production-session-mary\x00guest_participant" {
			t.Fatalf("guest churn status=%+v closes=%d record=%+v snapshot=%+v", status, closed, record, snapshot)
		}
	})
}

func TestMeetingSpecialistProductFailClosedRevokesUnrelatedJoinedRuntimeBeforeReturn(t *testing.T) {
	_, authority, user := specialistProductFixture(t)
	now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	persistence := &MeetingSpecialistProductPersistence{
		SnapshotPath: filepath.Join(dir, "specialists.snapshot.json"), GenerationPath: filepath.Join(dir, "specialists.generation.json"),
		Authority: STRIDESnapshotMACAuthority{KeyID: "specialist_fail_closed", Key: []byte("0123456789abcdef0123456789abcdef")}, MinimumGeneration: 1, BootstrapEmpty: true,
	}
	product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: persistence})
	provider := installMeetingSpecialistProductionJoin(t, product, now)
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "unrelated-fail-closed", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(persistence.GenerationPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(persistence.GenerationPath, 0o700); err != nil {
		t.Fatal(err)
	}
	product.CloseScope("another-room", "another-sitting", "unrelated_mutation")
	provider.mu.Lock()
	closed := provider.closed
	provider.mu.Unlock()
	product.mu.Lock()
	record := product.invitations[requested.ID]
	enabled := product.enabled
	product.mu.Unlock()
	if closed != 1 || record.Runtime != nil || enabled {
		t.Fatalf("fail-closed returned before revocation: closes=%d record=%+v enabled=%v", closed, record, enabled)
	}
}

func TestMeetingSpecialistProductCloseIsTerminalAcrossConcurrentRequest(t *testing.T) {
	product, authority, user := specialistProductFixture(t)
	secondRosterRead := make(chan struct{})
	releaseRosterRead := make(chan struct{})
	authority.mu.Lock()
	authority.onEligible = func(call int) {
		if call == 2 {
			close(secondRosterRead)
			<-releaseRosterRead
		}
	}
	authority.mu.Unlock()
	requestDone := make(chan error, 1)
	go func() {
		_, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "close-race", time.Minute)
		requestDone <- err
	}()
	<-secondRosterRead
	closeDone := make(chan struct{})
	go func() { product.Close("test_close"); close(closeDone) }()
	close(releaseRosterRead)
	if err := <-requestDone; err != nil {
		t.Fatalf("request that linearized before close err=%v", err)
	}
	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not complete")
	}
	if _, err := product.Request(context.Background(), user, "dog-perfect", "mary", "After close", "after-close", time.Minute); !errors.Is(err, ErrMeetingSpecialistProductDisabled) {
		t.Fatalf("post-Close request err=%v", err)
	}
	product.mu.Lock()
	enabled := product.enabled
	for _, record := range product.invitations {
		if meetingSpecialistInvitationIsActive(record) {
			product.mu.Unlock()
			t.Fatalf("active invitation survived Close: %+v", record)
		}
	}
	product.mu.Unlock()
	if enabled {
		t.Fatal("Close left product enabled after monitor shutdown")
	}
}

func TestMeetingSpecialistProductReconcilesAutonomousRuntimeTerminal(t *testing.T) {
	_, authority, user := specialistProductFixture(t)
	now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	persistence := &MeetingSpecialistProductPersistence{
		SnapshotPath: filepath.Join(dir, "specialists.snapshot.json"), GenerationPath: filepath.Join(dir, "specialists.generation.json"),
		Authority: STRIDESnapshotMACAuthority{KeyID: "specialist_runtime_terminal", Key: []byte("0123456789abcdef0123456789abcdef")}, MinimumGeneration: 1, BootstrapEmpty: true,
	}
	product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: persistence})
	installMeetingSpecialistProductionJoin(t, product, now)
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "runtime-terminal", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved"); err != nil {
		t.Fatal(err)
	}
	product.mu.Lock()
	runtime := product.invitations[requested.ID].Runtime
	product.mu.Unlock()
	if runtime == nil {
		t.Fatal("joined runtime missing")
	}
	runtime.mu.Lock()
	lease := runtime.lease
	runtime.mu.Unlock()
	if err := runtime.Stop(lease, "failed"); err != nil {
		t.Fatal(err)
	}
	product.mu.Lock()
	record := product.invitations[requested.ID]
	product.mu.Unlock()
	if record.Runtime != nil || record.Status != "failed" || meetingSpecialistInvitationIsActive(record) || !isHexDigest(record.QualificationSubjectDigest) || record.TerminalEvidence == nil || record.TerminalEvidence.QualificationSubjectDigest != record.QualificationSubjectDigest || record.TerminalEvidence.TerminalReason != "failed" || record.TerminalEvidence.Cause != "failed" || record.TerminalEvidence.EndedAt.IsZero() || record.TerminalEvidence.TeardownReceiptDigest == "" {
		t.Fatalf("autonomous runtime terminal was not reconciled: %+v", record)
	}
	restore := *persistence
	restore.BootstrapEmpty = false
	restarted := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restore})
	restarted.mu.Lock()
	restored, restoreHealth := restarted.invitations[requested.ID], restarted.healthErr
	restarted.mu.Unlock()
	if restoreHealth != nil || restored.Status != "failed" || restored.QualificationSubjectDigest != record.QualificationSubjectDigest || restored.TerminalEvidence == nil || restored.TerminalEvidence.QualificationSubjectDigest != restored.QualificationSubjectDigest || restored.TerminalEvidence.TerminalReason != "failed" || restored.TerminalEvidence.TeardownReceiptDigest != record.TerminalEvidence.TeardownReceiptDigest {
		t.Fatalf("autonomous terminal evidence did not survive restart: record=%+v health=%v", restored, restoreHealth)
	}
	if next, err := restarted.Request(context.Background(), user, "dog-perfect", "mary", "Try again", "runtime-terminal-next", time.Minute); err != nil || next.ID == requested.ID {
		t.Fatalf("terminal runtime blocked fresh invitation: next=%+v err=%v", next, err)
	}
}

func TestMeetingSpecialistProductValidatesTerminalEvidenceBeforeDurableWrite(t *testing.T) {
	t.Run("valid nonzero provider receipt survives signed restart", func(t *testing.T) {
		_, authority, user := specialistProductFixture(t)
		now := time.Date(2026, 8, 1, 19, 0, 0, 0, time.UTC)
		dir := t.TempDir()
		persistence := &MeetingSpecialistProductPersistence{
			SnapshotPath: filepath.Join(dir, "specialists.snapshot.json"), GenerationPath: filepath.Join(dir, "specialists.generation.json"),
			Authority: STRIDESnapshotMACAuthority{KeyID: "specialist_terminal_evidence", Key: []byte("0123456789abcdef0123456789abcdef")}, MinimumGeneration: 1, BootstrapEmpty: true,
		}
		product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: persistence})
		installMeetingSpecialistProductionJoin(t, product, now)
		requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "terminal-evidence-valid", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved"); err != nil {
			t.Fatal(err)
		}
		product.mu.Lock()
		runtime := product.invitations[requested.ID].Runtime
		product.mu.Unlock()
		evidence := MeetingSpecialistTerminalEvidence{TerminalReason: "failed", Cause: "provider_usage_unreconciled", EndedAt: now.Add(time.Second), QualificationSubjectDigest: runtime.qualificationSubjectDigest, QualificationResult: cloneMeetingSpecialistQualificationResult(runtime.qualificationResult), TeardownReceiptDigest: strideTestDigest("9"), ProviderReceipt: meetingSpecialistProviderReceiptFixture(runtime.qualificationSubjectDigest)}
		product.recordRuntimeTerminal(requested.ID, runtime, evidence)
		product.mu.Lock()
		record, healthErr := product.invitations[requested.ID], product.healthErr
		product.mu.Unlock()
		if healthErr != nil || record.Status != "failed" || !sameMeetingSpecialistTerminalEvidence(record.TerminalEvidence, &evidence) {
			t.Fatalf("valid terminal evidence was not recorded: record=%+v health=%v", record, healthErr)
		}
		restore := *persistence
		restore.BootstrapEmpty = false
		restarted := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restore})
		restarted.mu.Lock()
		restored, restoreHealth := restarted.invitations[requested.ID], restarted.healthErr
		restarted.mu.Unlock()
		if restoreHealth != nil || !sameMeetingSpecialistTerminalEvidence(restored.TerminalEvidence, &evidence) {
			t.Fatalf("provider receipt did not survive signed restart: record=%+v health=%v", restored, restoreHealth)
		}
		product.mu.Lock()
		tampered := product.invitations[requested.ID]
		tampered.QualificationSubjectDigest = strideTestDigest("f")
		product.invitations[requested.ID] = tampered
		err = product.persistLocked()
		product.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		mismatched := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restore})
		if got := mismatched.Status(context.Background(), user, "dog-perfect"); got.Reason != "state_restore_failed" {
			t.Fatalf("signed session-to-terminal qualification mismatch survived restore: %+v", got)
		}
	})

	t.Run("malformed callback fails closed before presentation", func(t *testing.T) {
		product, _, user := specialistProductFixture(t)
		installMeetingSpecialistProductionJoin(t, product, product.now().UTC())
		requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "terminal-evidence-invalid", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved"); err != nil {
			t.Fatal(err)
		}
		product.mu.Lock()
		runtime := product.invitations[requested.ID].Runtime
		product.mu.Unlock()
		product.recordRuntimeTerminal(requested.ID, runtime, MeetingSpecialistTerminalEvidence{TerminalReason: "failed", Cause: "failed", EndedAt: product.now().UTC(), TeardownReceiptDigest: "not-a-digest"})
		product.mu.Lock()
		record, enabled, healthErr := product.invitations[requested.ID], product.enabled, product.healthErr
		product.mu.Unlock()
		if enabled || healthErr == nil || record.Runtime != nil || record.TerminalEvidence != nil || meetingSpecialistView(record).TerminalEvidence != nil {
			t.Fatalf("malformed terminal evidence escaped fail-closed boundary: record=%+v enabled=%v health=%v", record, enabled, healthErr)
		}
	})

	t.Run("malformed signed provider receipt fails restore", func(t *testing.T) {
		_, authority, user := specialistProductFixture(t)
		now := time.Date(2026, 8, 1, 19, 30, 0, 0, time.UTC)
		dir := t.TempDir()
		persistence := &MeetingSpecialistProductPersistence{
			SnapshotPath: filepath.Join(dir, "specialists.snapshot.json"), GenerationPath: filepath.Join(dir, "specialists.generation.json"),
			Authority: STRIDESnapshotMACAuthority{KeyID: "specialist_terminal_malformed", Key: []byte("0123456789abcdef0123456789abcdef")}, MinimumGeneration: 1, BootstrapEmpty: true,
		}
		product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: persistence})
		requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "terminal-evidence-malformed-snapshot", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved"); err != nil {
			t.Fatal(err)
		}
		product.mu.Lock()
		record := product.invitations[requested.ID]
		malformed := meetingSpecialistProviderReceiptFixture(record.QualificationSubjectDigest)
		malformed.EventCount = -1
		record.Runtime = nil
		record.Status = "failed"
		record.TerminalEvidence = &MeetingSpecialistTerminalEvidence{TerminalReason: "failed", Cause: "failed", EndedAt: now, QualificationSubjectDigest: record.QualificationSubjectDigest, TeardownReceiptDigest: strideTestDigest("a"), ProviderReceipt: malformed}
		product.invitations[requested.ID] = record
		err = product.persistLocked()
		product.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		restore := *persistence
		restore.BootstrapEmpty = false
		restarted := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restore})
		if got := restarted.Status(context.Background(), user, "dog-perfect"); got.Reason != "state_restore_failed" {
			t.Fatalf("malformed signed provider receipt did not fail restore: %+v", got)
		}
	})
}

func TestMeetingSpecialistProductMigratesSignedV1QualificationEvidenceWithoutTrustingIt(t *testing.T) {
	_, authority, user := specialistProductFixture(t)
	now := time.Date(2026, 8, 1, 19, 40, 0, 0, time.UTC)
	dir := t.TempDir()
	persistence := &MeetingSpecialistProductPersistence{
		SnapshotPath: filepath.Join(dir, "specialists.snapshot.json"), GenerationPath: filepath.Join(dir, "specialists.generation.json"),
		Authority: STRIDESnapshotMACAuthority{KeyID: "specialist_v1_migration", Key: []byte("0123456789abcdef0123456789abcdef")}, MinimumGeneration: 1, BootstrapEmpty: true,
	}
	product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: persistence})
	installMeetingSpecialistProductionJoin(t, product, now)
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "v1-qualification-migration", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved"); err != nil {
		t.Fatal(err)
	}
	product.mu.Lock()
	runtime := product.invitations[requested.ID].Runtime
	product.mu.Unlock()
	evidence := MeetingSpecialistTerminalEvidence{TerminalReason: "failed", Cause: "provider_usage_unreconciled", EndedAt: now.Add(time.Second), QualificationSubjectDigest: runtime.qualificationSubjectDigest, QualificationResult: cloneMeetingSpecialistQualificationResult(runtime.qualificationResult), TeardownReceiptDigest: strideTestDigest("9"), ProviderReceipt: meetingSpecialistProviderReceiptFixture(runtime.qualificationSubjectDigest)}
	product.recordRuntimeTerminal(requested.ID, runtime, evidence)

	var snapshot meetingSpecialistSnapshotEnvelope
	if err := readSTRIDERuntimeJSON(persistence.SnapshotPath, &snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Payload.Format = 1
	for index := range snapshot.Payload.Records {
		record := &snapshot.Payload.Records[index]
		record.QualificationSubjectDigest = ""
		record.QualificationResult = nil
		if record.TerminalEvidence != nil {
			record.TerminalEvidence.QualificationSubjectDigest = ""
			record.TerminalEvidence.QualificationResult = nil
			record.TerminalEvidence.QualificationLegacyUnbound = false
			record.TerminalEvidence.ProviderReceipt.QualificationSubjectDigest = ""
		}
	}
	snapshot.Digest, err = STRIDEContractDigest(snapshot.Payload)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Signature, err = strideSnapshotMAC(persistence.Authority, meetingSpecialistProductSnapshotDomain, snapshot.Payload.Generation, snapshot.Digest)
	if err != nil {
		t.Fatal(err)
	}
	var generation meetingSpecialistGenerationEnvelope
	if err := readSTRIDERuntimeJSON(persistence.GenerationPath, &generation); err != nil {
		t.Fatal(err)
	}
	generation.Payload.Format = 1
	generation.Payload.SnapshotDigest = snapshot.Digest
	generation.Digest, err = STRIDEContractDigest(generation.Payload)
	if err != nil {
		t.Fatal(err)
	}
	generation.Signature, err = strideSnapshotMAC(persistence.Authority, meetingSpecialistProductGenerationDomain, generation.Payload.Generation, generation.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFileAtomically(persistence.SnapshotPath, "meeting specialist v1 test snapshot", snapshot); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFileAtomically(persistence.GenerationPath, "meeting specialist v1 test generation", generation); err != nil {
		t.Fatal(err)
	}

	restore := *persistence
	restore.BootstrapEmpty = false
	restarted := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restore})
	restarted.mu.Lock()
	restored, healthErr := restarted.invitations[requested.ID], restarted.healthErr
	restarted.mu.Unlock()
	if healthErr != nil || restored.Status != "failed" || restored.QualificationResult != nil || restored.QualificationSubjectDigest != "" || restored.TerminalEvidence == nil || !restored.TerminalEvidence.QualificationLegacyUnbound || restored.TerminalEvidence.ProviderReceipt.QualificationSubjectDigest != "" {
		t.Fatalf("v1 evidence was not preserved as explicitly unbound history: record=%+v health=%v", restored, healthErr)
	}
	var upgraded meetingSpecialistSnapshotEnvelope
	if err := readSTRIDERuntimeJSON(persistence.SnapshotPath, &upgraded); err != nil || upgraded.Payload.Format != meetingSpecialistProductSnapshotFormat {
		t.Fatalf("v1 snapshot was not durably upgraded: format=%d err=%v", upgraded.Payload.Format, err)
	}
	second := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restore})
	if got := second.Status(context.Background(), user, "dog-perfect"); got.Reason == "state_restore_failed" {
		t.Fatalf("upgraded unbound history failed second restart: %+v", got)
	}
}

func TestMeetingSpecialistProductClosePersistsTerminalEvidenceWhileDisabled(t *testing.T) {
	_, authority, user := specialistProductFixture(t)
	now := time.Date(2026, 8, 1, 19, 45, 0, 0, time.UTC)
	dir := t.TempDir()
	persistence := &MeetingSpecialistProductPersistence{
		SnapshotPath: filepath.Join(dir, "specialists.snapshot.json"), GenerationPath: filepath.Join(dir, "specialists.generation.json"),
		Authority: STRIDESnapshotMACAuthority{KeyID: "specialist_close_terminal", Key: []byte("0123456789abcdef0123456789abcdef")}, MinimumGeneration: 1, BootstrapEmpty: true,
	}
	product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: persistence})
	provider := installMeetingSpecialistProductionJoin(t, product, now)
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "close-terminal-evidence", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved"); err != nil {
		t.Fatal(err)
	}
	product.Close("room_closed")
	product.mu.Lock()
	record, enabled, healthErr := product.invitations[requested.ID], product.enabled, product.healthErr
	product.mu.Unlock()
	provider.mu.Lock()
	closed := provider.closed
	provider.mu.Unlock()
	if enabled || healthErr != nil || closed != 1 || record.Status != "closed" || record.TerminalEvidence == nil || record.TerminalEvidence.TerminalReason != "room_closed" || record.TerminalEvidence.Cause != "room_closed" || record.TerminalEvidence.TeardownReceiptDigest == "" {
		t.Fatalf("Close terminal evidence state: record=%+v enabled=%v health=%v closes=%d", record, enabled, healthErr, closed)
	}
	restore := *persistence
	restore.BootstrapEmpty = false
	restarted := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restore})
	restarted.mu.Lock()
	restored, restoreHealth := restarted.invitations[requested.ID], restarted.healthErr
	restarted.mu.Unlock()
	if restoreHealth != nil || restored.Status != "closed" || restored.TerminalEvidence == nil || restored.TerminalEvidence.TerminalReason != "room_closed" || restored.TerminalEvidence.TeardownReceiptDigest != record.TerminalEvidence.TeardownReceiptDigest {
		t.Fatalf("Close terminal evidence restart: record=%+v health=%v", restored, restoreHealth)
	}
}

func TestMeetingSpecialistProductControlExpiryPersistsClosedLedgerWithoutRestoreFailure(t *testing.T) {
	var current atomic.Bool
	current.Store(true)
	_, authority, user := specialistProductFixture(t)
	now := time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	persistence := &MeetingSpecialistProductPersistence{
		SnapshotPath: filepath.Join(dir, "specialists.snapshot.json"), GenerationPath: filepath.Join(dir, "specialists.generation.json"),
		Authority: STRIDESnapshotMACAuthority{KeyID: "specialist_control_expiry", Key: []byte("0123456789abcdef0123456789abcdef")}, MinimumGeneration: 1, BootstrapEmpty: true,
	}
	product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{
		Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: persistence,
		ControlCurrent: func() bool { return current.Load() }, ControlCheckInterval: time.Millisecond,
	})
	provider := installMeetingSpecialistProductionJoin(t, product, now)
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "persisted-control-expiry", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved"); err != nil {
		t.Fatal(err)
	}
	current.Store(false)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		product.mu.Lock()
		record, enabled, healthErr := product.invitations[requested.ID], product.enabled, product.healthErr
		product.mu.Unlock()
		provider.mu.Lock()
		closed := provider.closed
		provider.mu.Unlock()
		if !enabled && healthErr == nil && record.Runtime == nil && record.Status == "closed" && record.TerminalEvidence != nil && closed == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	provider.mu.Lock()
	closed := provider.closed
	provider.mu.Unlock()
	product.mu.Lock()
	record, enabled, healthErr := product.invitations[requested.ID], product.enabled, product.healthErr
	product.mu.Unlock()
	if enabled || healthErr != nil || record.Runtime != nil || record.Status != "closed" || record.TerminalEvidence == nil || record.TerminalEvidence.TerminalReason != "control_authority_expired" || record.TerminalEvidence.TeardownReceiptDigest == "" || closed != 1 {
		t.Fatalf("control expiry persistence state: enabled=%v health=%v record=%+v closes=%d", enabled, healthErr, record, closed)
	}
	restore := *persistence
	restore.BootstrapEmpty = false
	restarted := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restore})
	restarted.mu.Lock()
	restored, restoreHealth := restarted.invitations[requested.ID], restarted.healthErr
	restarted.mu.Unlock()
	if restoreHealth != nil || restored.Status != "closed" || restored.Runtime != nil || restored.TerminalEvidence == nil || restored.TerminalEvidence.TerminalReason != "control_authority_expired" || restored.TerminalEvidence.TeardownReceiptDigest != record.TerminalEvidence.TeardownReceiptDigest {
		t.Fatalf("control expiry durable state=%+v health=%v", restored, restoreHealth)
	}
}

func TestMeetingSpecialistProductTeardownDoesNotHoldProductMutexWhenProviderCloseBlocks(t *testing.T) {
	now := time.Date(2026, 8, 1, 20, 30, 0, 0, time.UTC)
	closeEntered := make(chan struct{})
	releaseClose := make(chan struct{})
	provider := &fakeMeetingSpecialistProvider{onClose: func() {
		close(closeEntered)
		<-releaseClose
	}}
	capabilityAuthority := newFakeMeetingSpecialistAuthority()
	runtime := NewMeetingSpecialistRuntime(func() time.Time { return now }, enabledSpecialistHarnessGates(), capabilityAuthority, func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
		return provider, nil
	}, func(MeetingAgentFloorScope, uint64, []int16) error { return nil })
	runtime.shutdownTimeout = 500 * time.Millisecond
	launch := specialistRuntimeLaunchFixture(now)
	launch.CapabilityReceipt = capabilityAuthority.issue(launch)
	if _, err := runtime.Start(context.Background(), launch); err != nil {
		t.Fatal(err)
	}

	product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{
		Enabled:   true,
		TenantID:  "bonfire",
		Now:       func() time.Time { return now },
		Authority: &fakeMeetingSpecialistProductAuthority{},
	})
	product.mu.Lock()
	product.invitations[launch.Invitation.Header.ID] = meetingSpecialistProductRecord{
		Invitation: launch.Invitation,
		Agent:      MeetingSpecialistCandidate{AgentID: launch.Scope.AgentID},
		Status:     "joined_session",
		Runtime:    runtime,
		UpdatedAt:  now,
	}
	product.mu.Unlock()

	revokeDone := make(chan error, 1)
	go func() {
		revokeDone <- product.RevokeAgentAuthority(launch.Scope.AgentID, "agent_authority_changed")
	}()
	select {
	case <-closeEntered:
	case <-time.After(time.Second):
		t.Fatal("provider close did not start")
	}

	mutexAvailable := make(chan struct{})
	go func() {
		product.mu.Lock()
		product.mu.Unlock()
		close(mutexAvailable)
	}()
	select {
	case <-mutexAvailable:
	case <-time.After(100 * time.Millisecond):
		close(releaseClose)
		t.Fatal("product mutex remained held while provider close was blocked")
	}
	close(releaseClose)
	if err := <-revokeDone; err != nil {
		t.Fatal(err)
	}

	product.mu.Lock()
	record := product.invitations[launch.Invitation.Header.ID]
	product.mu.Unlock()
	snapshot := runtime.Snapshot()
	if record.Runtime != nil || record.Status != "eligibility_revoked" || snapshot.Session != nil || snapshot.TeardownReceiptDigest == "" || snapshot.TerminalReason != launch.Scope.SessionID+"\x00agent_authority_changed" {
		t.Fatalf("blocked-close revocation was not truthful: record=%+v snapshot=%+v", record, snapshot)
	}
}

func TestMeetingSpecialistProductScopeRevocationAndConcurrentApprovalFailClosed(t *testing.T) {
	product, authority, user := specialistProductFixture(t)
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "request-2", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	authority.mu.Lock()
	authority.current = false
	authority.mu.Unlock()
	if _, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, 1, "approved"); !errors.Is(err, ErrMeetingSpecialistProductScope) {
		t.Fatalf("revoked scope err=%v", err)
	}
	authority.mu.Lock()
	authority.current = true
	authority.mu.Unlock()

	var successes, failures int
	var mu sync.Mutex
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, resolveErr := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, 1, "approved")
			mu.Lock()
			if resolveErr == nil {
				successes++
			} else {
				failures++
			}
			mu.Unlock()
		}()
	}
	wait.Wait()
	if successes != 1 || failures != 1 {
		t.Fatalf("successes=%d failures=%d", successes, failures)
	}
}

func TestMeetingSpecialistProductStatusRevokesApprovedRuntimeWhenEligibilityChanges(t *testing.T) {
	product, authority, user := specialistProductFixture(t)
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "eligibility-status", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved")
	if err != nil || approved.Status != "approved_waiting_for_provider_qualification" {
		t.Fatalf("approved=%+v err=%v", approved, err)
	}
	authority.mu.Lock()
	authority.roster = nil
	authority.mu.Unlock()
	status := product.Status(context.Background(), user, "dog-perfect")
	if len(status.Candidates) != 0 || len(status.Invitations) != 1 || status.Invitations[0].Status != "eligibility_revoked" || status.Invitations[0].ProviderSessionStarted {
		t.Fatalf("eligibility revocation status=%+v", status)
	}
	product.mu.Lock()
	record := product.invitations[requested.ID]
	product.mu.Unlock()
	if record.Runtime != nil {
		t.Fatal("eligibility revocation retained specialist runtime")
	}
}

func TestMeetingSpecialistProductRevokedRosterCannotBeApproved(t *testing.T) {
	product, authority, user := specialistProductFixture(t)
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "roster-revoke-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	authority.roster = nil
	if _, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved"); !errors.Is(err, ErrMeetingSpecialistProductAgent) {
		t.Fatalf("revoked roster approval err=%v", err)
	}
	product.mu.Lock()
	record := product.invitations[requested.ID]
	product.mu.Unlock()
	if record.Invitation.Decision != "requested" || record.Runtime != nil {
		t.Fatalf("revoked roster mutated invitation: %+v", record)
	}
}

func TestMeetingSpecialistProductDeclineRemainsAvailableWhenConsentIsWithdrawn(t *testing.T) {
	product, authority, user := specialistProductFixture(t)
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "consent-withdrawn-decline-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	authority.mu.Lock()
	authority.current = false // full consent-bearing scope is no longer available
	authority.mu.Unlock()
	declined, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "declined")
	if err != nil || declined.Decision != "declined" || declined.Status != "declined" {
		t.Fatalf("decline=%+v err=%v", declined, err)
	}
}

func TestMeetingSpecialistProductRestartDoesNotResurrectTransientApproval(t *testing.T) {
	product, authority, user := specialistProductFixture(t)
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "request-3", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = product.Resolve(context.Background(), user, "dog-perfect", requested.ID, 1, "approved"); err != nil {
		t.Fatal(err)
	}
	product.Close("room_closed")

	restarted := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, Now: product.now, Authority: authority})
	status := restarted.Status(context.Background(), user, "dog-perfect")
	if len(status.Invitations) != 0 {
		t.Fatalf("restart resurrected transient invitations: %+v", status.Invitations)
	}
}

func TestMeetingSpecialistProductSignedRestartRecoveryAndRollbackFence(t *testing.T) {
	_, authority, user := specialistProductFixture(t)
	dir := t.TempDir()
	persistence := &MeetingSpecialistProductPersistence{
		SnapshotPath: filepath.Join(dir, "specialists.snapshot.json"), GenerationPath: filepath.Join(dir, "specialists.generation.json"),
		Authority: STRIDESnapshotMACAuthority{KeyID: "specialist_test_key", Key: []byte("0123456789abcdef0123456789abcdef")}, MinimumGeneration: 1, BootstrapEmpty: true,
	}
	now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
	product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: persistence})
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "durable-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	oldSnapshot, err := os.ReadFile(persistence.SnapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = product.Resolve(context.Background(), user, "dog-perfect", requested.ID, 1, "approved"); err != nil {
		t.Fatal(err)
	}
	if product.generation != 2 {
		t.Fatalf("generation=%d", product.generation)
	}

	restoreConfig := *persistence
	restoreConfig.BootstrapEmpty = false
	restarted := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restoreConfig})
	status := restarted.Status(context.Background(), user, "dog-perfect")
	if len(status.Invitations) != 1 || status.Invitations[0].ID != requested.ID || status.Invitations[0].Status != "approved_reauthorization_required" || restarted.generation != 2 {
		t.Fatalf("restart status=%+v generation=%d", status, restarted.generation)
	}

	// An older authentic snapshot paired with the newer generation ledger is a
	// rollback attempt and must fail closed rather than silently lose approval.
	if err := os.WriteFile(persistence.SnapshotPath, oldSnapshot, 0o600); err != nil {
		t.Fatal(err)
	}
	rolledBack := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restoreConfig})
	if got := rolledBack.Status(context.Background(), user, "dog-perfect"); got.Reason != "state_restore_failed" || got.Available || got.CanInvite {
		t.Fatalf("rollback did not fail closed: %+v", got)
	}
}

func TestMeetingSpecialistProductRestoreNormalizesLegacyTestJoinStates(t *testing.T) {
	for _, scenario := range []struct {
		legacy string
		want   string
	}{
		{legacy: "joined_test_session", want: "approved_reauthorization_required"},
		{legacy: "approved_test_session_failed", want: "approved_session_failed"},
	} {
		t.Run(scenario.legacy, func(t *testing.T) {
			_, authority, user := specialistProductFixture(t)
			now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
			dir := t.TempDir()
			persistence := &MeetingSpecialistProductPersistence{
				SnapshotPath: filepath.Join(dir, "specialists.snapshot.json"), GenerationPath: filepath.Join(dir, "specialists.generation.json"),
				Authority: STRIDESnapshotMACAuthority{KeyID: "specialist_legacy_state_key", Key: []byte("0123456789abcdef0123456789abcdef")}, MinimumGeneration: 1, BootstrapEmpty: true,
			}
			product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: persistence})
			requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "legacy-state-"+scenario.legacy, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved"); err != nil {
				t.Fatal(err)
			}
			product.mu.Lock()
			record := product.invitations[requested.ID]
			record.Status = scenario.legacy
			product.invitations[requested.ID] = record
			err = product.persistLocked()
			product.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}

			restore := *persistence
			restore.BootstrapEmpty = false
			restarted := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restore})
			restarted.mu.Lock()
			restored, healthErr := restarted.invitations[requested.ID], restarted.healthErr
			restarted.mu.Unlock()
			if healthErr != nil || restored.Status != scenario.want || restored.Runtime != nil {
				t.Fatalf("legacy restore poisoned product: record=%+v health=%v", restored, healthErr)
			}
			second := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restore})
			second.mu.Lock()
			secondRecord, secondHealth := second.invitations[requested.ID], second.healthErr
			second.mu.Unlock()
			if secondHealth != nil || secondRecord.Status != scenario.want || secondRecord.Runtime != nil {
				t.Fatalf("normalized state was not durable: record=%+v health=%v", secondRecord, secondHealth)
			}
		})
	}
}

func TestMeetingSpecialistProductRestoredApprovalNeedsFreshScopeButRetainsCandidateRevocation(t *testing.T) {
	_, authority, user := specialistProductFixture(t)
	dir := t.TempDir()
	persistence := &MeetingSpecialistProductPersistence{
		SnapshotPath: filepath.Join(dir, "specialists.snapshot.json"), GenerationPath: filepath.Join(dir, "specialists.generation.json"),
		Authority: STRIDESnapshotMACAuthority{KeyID: "specialist_scope_restart_key", Key: []byte("0123456789abcdef0123456789abcdef")}, MinimumGeneration: 1, BootstrapEmpty: true,
	}
	now := time.Date(2026, 8, 1, 17, 0, 0, 0, time.UTC)
	product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: persistence})
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "restart-scope-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved"); err != nil {
		t.Fatal(err)
	}

	restore := *persistence
	restore.BootstrapEmpty = false
	restarted := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restore})
	authority.mu.Lock()
	authority.scope.MediaGeneration++
	authority.scope.ConsentFences[0].recordDigest = strideTestDigest("a")
	authority.mu.Unlock()

	// Reconnect/reminted scope cannot revive the old approval and therefore has
	// no live authority left to revoke. The UI keeps the truthful restart state
	// and requires a newly scoped invitation.
	if err := restarted.RevokeScopeAuthority("dog-perfect", "sitting-1", "participant_authority_changed"); err != nil {
		t.Fatal(err)
	}
	status := restarted.Status(context.Background(), user, "dog-perfect")
	if len(status.Invitations) != 1 || status.Invitations[0].Status != "approved_reauthorization_required" || status.Invitations[0].ProviderSessionStarted {
		t.Fatalf("scope churn downgraded inert restart approval: %+v", status.Invitations)
	}

	// Candidate authority remains independently revision-bound: a pause,
	// offboard, assignment, profile, or capability mutation must still make the
	// historical approval terminally ineligible.
	if err := restarted.RevokeAgentAuthority("mary", "agent_authority_changed"); err != nil {
		t.Fatal(err)
	}
	status = restarted.Status(context.Background(), user, "dog-perfect")
	if len(status.Invitations) != 1 || status.Invitations[0].Status != "eligibility_revoked" || status.Invitations[0].ProviderSessionStarted {
		t.Fatalf("candidate revision did not revoke restart approval: %+v", status.Invitations)
	}
}

func TestMeetingSpecialistProductTamperedSnapshotFailsClosed(t *testing.T) {
	_, authority, user := specialistProductFixture(t)
	dir := t.TempDir()
	persistence := &MeetingSpecialistProductPersistence{
		SnapshotPath: filepath.Join(dir, "specialists.snapshot.json"), GenerationPath: filepath.Join(dir, "specialists.generation.json"),
		Authority: STRIDESnapshotMACAuthority{KeyID: "specialist_test_key", Key: []byte("0123456789abcdef0123456789abcdef")}, MinimumGeneration: 1, BootstrapEmpty: true,
	}
	product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Authority: authority, Persistence: persistence})
	if _, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "tamper-1", time.Minute); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(persistence.SnapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"displayName": "Mary"`, `"displayName": "Mallory"`, 1))
	if err := os.WriteFile(persistence.SnapshotPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	restoreConfig := *persistence
	restoreConfig.BootstrapEmpty = false
	restarted := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Authority: authority, Persistence: &restoreConfig})
	if got := restarted.Status(context.Background(), user, "dog-perfect"); got.Reason != "state_restore_failed" {
		t.Fatalf("tamper status=%+v", got)
	}
}

func TestMeetingSpecialistProductRoomCloseTearsDownExactSittingOnly(t *testing.T) {
	product, _, user := specialistProductFixture(t)
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "request-close", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = product.Resolve(context.Background(), user, "dog-perfect", requested.ID, 1, "approved"); err != nil {
		t.Fatal(err)
	}
	product.CloseScope("another-room", "sitting-1", "room_closed")
	product.mu.Lock()
	before := product.invitations[requested.ID]
	product.mu.Unlock()
	if before.Runtime != nil || before.Status != "approved_waiting_for_provider_qualification" {
		t.Fatalf("unrelated room closed specialist: %+v", before)
	}
	product.CloseScope("dog-perfect", "sitting-1", "room_closed")
	product.mu.Lock()
	after := product.invitations[requested.ID]
	product.mu.Unlock()
	if after.Runtime != nil || after.Status != "closed" {
		t.Fatalf("exact sitting teardown=%+v", after)
	}
}

func TestMeetingSpecialistHTTPRequiresAuthAndReportsDefaultOffHonestly(t *testing.T) {
	setupAuthTestEnv(t)
	previous := kanbanApp
	kanbanApp = &kanbanBoardApp{meetingSpecialists: NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{})}
	t.Cleanup(func() { kanbanApp = previous })

	request := httptest.NewRequest(http.MethodGet, "/api/stride/v1/meeting-specialists?roomId=office", nil)
	recorder := httptest.NewRecorder()
	meetingSpecialistProductStatusHandler(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/stride/v1/meeting-specialists?roomId=office", nil)
	for _, cookie := range loginAs(t, "aj@shareability.com", defaultMeetingRoomPassword) {
		request.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	meetingSpecialistProductStatusHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Specialists MeetingSpecialistProductStatus `json:"specialists"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Specialists.Available || response.Specialists.Reason != "feature_disabled" || len(response.Specialists.Candidates) != 0 {
		t.Fatalf("default-off response=%+v", response.Specialists)
	}
}

func TestMeetingSpecialistHTTPRejectsUnknownFieldsBeforeAuthority(t *testing.T) {
	setupAuthTestEnv(t)
	product, _, _ := specialistProductFixture(t)
	previous := kanbanApp
	kanbanApp = &kanbanBoardApp{meetingSpecialists: product}
	t.Cleanup(func() { kanbanApp = previous })
	request := httptest.NewRequest(http.MethodPost, "/api/stride/v1/meeting-specialists/invitations", strings.NewReader(`{"roomId":"dog-perfect","agentId":"mary","purpose":"review","idempotencyKey":"key-1","approved":true}`))
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", defaultMeetingRoomPassword) {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	meetingSpecialistProductInvitationHandler(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMeetingSpecialistControlActivationRequiresExactSignedShortLivedReceipt(t *testing.T) {
	t.Setenv("BONFIRE_CANONICAL_TENANT_ID", "bonfire")
	dir := t.TempDir()
	path := filepath.Join(dir, "meeting-specialist-control.json")
	t.Setenv(meetingSpecialistControlActivationEnv, path)
	now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
	authority := STRIDESnapshotMACAuthority{KeyID: "specialist_control_key", Key: []byte("0123456789abcdef0123456789abcdef")}
	runtime := &STRIDERuntime{
		config: STRIDERuntimeConfig{Enabled: true, TenantID: "bonfire", Authority: authority, MinimumGeneration: 4},
		state:  STRIDERuntimeStandby,
	}
	payload := meetingSpecialistControlActivationPayload{
		Format: meetingSpecialistControlActivationFormat, TenantID: "bonfire", Generation: 4, KeyID: authority.KeyID,
		Features: append([]STRIDEFeature(nil), meetingSpecialistControlFeatures...), EvidenceDigest: strideTestDigest("7"), StateMinimumGeneration: 1, BootstrapEmpty: true, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour),
	}
	digest, err := STRIDEContractDigest(payload)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := strideSnapshotMAC(authority, meetingSpecialistControlActivationDomain, payload.Generation, digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFileAtomically(path, "meeting specialist activation test", meetingSpecialistControlActivationEnvelope{Payload: payload, Digest: digest, Signature: signature}); err != nil {
		t.Fatal(err)
	}
	if !meetingSpecialistControlActivationEnabled(runtime, now) {
		t.Fatal("valid control-plane receipt was not accepted")
	}

	// A correctly signed receipt that widens authority to audio is rejected;
	// this ceremony can never cross the provider/audio boundary.
	payload.Features = append(payload.Features, STRIDEFeatureSpecialistAudio)
	digest, _ = STRIDEContractDigest(payload)
	signature, _ = strideSnapshotMAC(authority, meetingSpecialistControlActivationDomain, payload.Generation, digest)
	if err := writeJSONFileAtomically(path, "meeting specialist activation test", meetingSpecialistControlActivationEnvelope{Payload: payload, Digest: digest, Signature: signature}); err != nil {
		t.Fatal(err)
	}
	if meetingSpecialistControlActivationEnabled(runtime, now) {
		t.Fatal("authority-widening receipt was accepted")
	}

	// Restore the exact receipt, then prove expiration and tampering both fail.
	payload.Features = append([]STRIDEFeature(nil), meetingSpecialistControlFeatures...)
	payload.ExpiresAt = now
	digest, _ = STRIDEContractDigest(payload)
	signature, _ = strideSnapshotMAC(authority, meetingSpecialistControlActivationDomain, payload.Generation, digest)
	if err := writeJSONFileAtomically(path, "meeting specialist activation test", meetingSpecialistControlActivationEnvelope{Payload: payload, Digest: digest, Signature: signature}); err != nil {
		t.Fatal(err)
	}
	if meetingSpecialistControlActivationEnabled(runtime, now) {
		t.Fatal("expired receipt was accepted")
	}
	payload.ExpiresAt = now.Add(time.Hour)
	digest, _ = STRIDEContractDigest(payload)
	if err := writeJSONFileAtomically(path, "meeting specialist activation test", meetingSpecialistControlActivationEnvelope{Payload: payload, Digest: digest, Signature: strings.Repeat("0", 64)}); err != nil {
		t.Fatal(err)
	}
	if meetingSpecialistControlActivationEnabled(runtime, now) {
		t.Fatal("forged receipt was accepted")
	}
}

func TestMeetingSpecialistProductRechecksShortLivedControlAuthority(t *testing.T) {
	var current atomic.Bool
	current.Store(true)
	now := time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC)
	product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{
		Enabled:              true,
		Now:                  func() time.Time { return now },
		Authority:            &fakeMeetingSpecialistProductAuthority{},
		ControlCurrent:       func() bool { return current.Load() },
		ControlCheckInterval: time.Millisecond,
	})
	defer product.Close("test_complete")
	if operational, _ := product.readiness(); !operational {
		t.Fatal("current control authority did not admit the product")
	}
	capabilityAuthority := newFakeMeetingSpecialistAuthority()
	runtime := NewMeetingSpecialistRuntime(func() time.Time { return now }, enabledSpecialistHarnessGates(), capabilityAuthority, func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
		return &fakeMeetingSpecialistProvider{}, nil
	}, nil)
	launch := specialistRuntimeLaunchFixture(now)
	launch.CapabilityReceipt = capabilityAuthority.issue(launch)
	if _, err := runtime.Start(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	product.mu.Lock()
	product.invitations[launch.Invitation.Header.ID] = meetingSpecialistProductRecord{Invitation: launch.Invitation, Status: "joined_session", Runtime: runtime, UpdatedAt: now}
	product.mu.Unlock()
	current.Store(false)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		product.mu.Lock()
		enabled := product.enabled
		record := product.invitations[launch.Invitation.Header.ID]
		product.mu.Unlock()
		snapshot := runtime.Snapshot()
		if !enabled && record.Runtime == nil && record.Status == "closed" && snapshot.Session == nil && snapshot.TeardownReceiptDigest != "" && snapshot.TerminalReason == launch.Scope.SessionID+"\x00control_authority_expired" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	product.mu.Lock()
	enabled := product.enabled
	record := product.invitations[launch.Invitation.Header.ID]
	product.mu.Unlock()
	snapshot := runtime.Snapshot()
	if enabled || record.Runtime != nil || record.Status != "closed" || snapshot.Session != nil || snapshot.TeardownReceiptDigest == "" || snapshot.TerminalReason != launch.Scope.SessionID+"\x00control_authority_expired" {
		t.Fatalf("control expiry did not atomically disable and revoke: enabled=%v record=%+v snapshot=%+v", enabled, record, snapshot)
	}
}

func TestMeetingSpecialistHTTPFakeSessionJoinAndFailureStayIsolated(t *testing.T) {
	setupAuthTestEnv(t)
	now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)

	for _, scenario := range []struct {
		name       string
		fail       bool
		wantStatus string
	}{
		{name: "joined", wantStatus: "joined_session"},
		{name: "provider failure isolated", fail: true, wantStatus: "approved_session_failed"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			product, productAuthority, _ := specialistProductFixture(t)
			provider := &fakeMeetingSpecialistProvider{}
			joinShouldFail := scenario.fail
			var factoryCalls atomic.Int64
			joiner, _ := productionJoinFixture(t, now, provider, &factoryCalls)
			providerFactory := joiner.qualifiedProvider.create
			joiner.qualifiedProvider.create = func(ctx context.Context, launch MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
				if joinShouldFail {
					return nil, errors.New("deterministic provider failure")
				}
				return providerFactory(ctx, launch)
			}
			product.productionJoin = joiner

			previous := kanbanApp
			kanbanApp = &kanbanBoardApp{meetingSpecialists: product}
			t.Cleanup(func() { kanbanApp = previous })
			cookies := loginAs(t, "aj@shareability.com", defaultMeetingRoomPassword)
			post := func(path, body string) *httptest.ResponseRecorder {
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				for _, cookie := range cookies {
					request.AddCookie(cookie)
				}
				recorder := httptest.NewRecorder()
				meetingSpecialistProductInvitationHandler(recorder, request)
				return recorder
			}
			requested := post("/api/stride/v1/meeting-specialists/invitations", `{"roomId":"dog-perfect","agentId":"mary","purpose":"review positioning","idempotencyKey":"fake-http-1"}`)
			if requested.Code != http.StatusOK {
				t.Fatalf("request status=%d body=%s", requested.Code, requested.Body.String())
			}
			var requestPayload struct {
				Invitation meetingSpecialistInvitationView `json:"invitation"`
			}
			if err := json.Unmarshal(requested.Body.Bytes(), &requestPayload); err != nil {
				t.Fatal(err)
			}
			if requestPayload.Invitation.PurposeSummary != "review positioning" || len(requestPayload.Invitation.ContextClasses) != 4 || requestPayload.Invitation.Audience.Visibility != "meeting" || requestPayload.Invitation.ExpectedTimeSeconds != 120 || requestPayload.Invitation.ExpectedCostCents != 25 || requestPayload.Invitation.HardLimits != defaultMeetingSpecialistApprovalLimits() || requestPayload.Invitation.ProviderSessionStarted {
				t.Fatalf("approval card omitted informed scope: %+v", requestPayload.Invitation)
			}
			approved := post("/api/stride/v1/meeting-specialists/invitations/"+requestPayload.Invitation.ID, `{"roomId":"dog-perfect","revision":1,"decision":"approved"}`)
			if approved.Code != http.StatusOK {
				t.Fatalf("approval status=%d body=%s", approved.Code, approved.Body.String())
			}
			var approvedPayload struct {
				Invitation             meetingSpecialistInvitationView `json:"invitation"`
				ProviderSessionStarted bool                            `json:"providerSessionStarted"`
			}
			if err := json.Unmarshal(approved.Body.Bytes(), &approvedPayload); err != nil {
				t.Fatal(err)
			}
			if approvedPayload.Invitation.Status != scenario.wantStatus || approvedPayload.ProviderSessionStarted != !scenario.fail || approvedPayload.Invitation.ProviderSessionStarted != !scenario.fail {
				t.Fatalf("approval=%+v", approvedPayload)
			}
			product.mu.Lock()
			record := product.invitations[requestPayload.Invitation.ID]
			product.mu.Unlock()
			if scenario.fail {
				if record.Runtime != nil || provider.briefs != 0 {
					t.Fatalf("failed join escaped isolation: %+v briefs=%d", record.Runtime, provider.briefs)
				}
				joinShouldFail = false
				recoveryRequest := post("/api/stride/v1/meeting-specialists/invitations", `{"roomId":"dog-perfect","agentId":"mary","purpose":"review positioning after recovery","idempotencyKey":"fake-http-recovery"}`)
				if recoveryRequest.Code != http.StatusOK {
					t.Fatalf("recovery request status=%d body=%s", recoveryRequest.Code, recoveryRequest.Body.String())
				}
				var recoveryPayload struct {
					Invitation meetingSpecialistInvitationView `json:"invitation"`
				}
				if err := json.Unmarshal(recoveryRequest.Body.Bytes(), &recoveryPayload); err != nil {
					t.Fatal(err)
				}
				if recoveryPayload.Invitation.ID == requestPayload.Invitation.ID || recoveryPayload.Invitation.Revision != 1 || recoveryPayload.Invitation.Status != "awaiting_approval" {
					t.Fatalf("failed invitation blocked fresh recovery: failed=%+v recovery=%+v", requestPayload.Invitation, recoveryPayload.Invitation)
				}
				recovered := post("/api/stride/v1/meeting-specialists/invitations/"+recoveryPayload.Invitation.ID, `{"roomId":"dog-perfect","revision":1,"decision":"approved"}`)
				if recovered.Code != http.StatusOK {
					t.Fatalf("recovery approval status=%d body=%s", recovered.Code, recovered.Body.String())
				}
				var recoveredPayload struct {
					Invitation meetingSpecialistInvitationView `json:"invitation"`
				}
				if err := json.Unmarshal(recovered.Body.Bytes(), &recoveredPayload); err != nil {
					t.Fatal(err)
				}
				if recoveredPayload.Invitation.Status != "joined_session" || provider.briefs != 1 {
					t.Fatalf("recovery did not join isolated fake session: invitation=%+v briefs=%d", recoveredPayload.Invitation, provider.briefs)
				}
			} else if record.Runtime == nil || record.Runtime.Snapshot().Session == nil || provider.briefs != 1 {
				t.Fatalf("fake join did not reach runtime: runtime=%+v briefs=%d", record.Runtime, provider.briefs)
			} else {
				// Revoking the specialist from the roster must prevent future
				// approvals without trapping an already joined test session.
				productAuthority.roster = nil
				dismissed := post("/api/stride/v1/meeting-specialists/invitations/"+requestPayload.Invitation.ID, `{"roomId":"dog-perfect","revision":2,"decision":"dismissed"}`)
				if dismissed.Code != http.StatusOK || provider.closed != 1 {
					t.Fatalf("dismiss status=%d body=%s provider closes=%d", dismissed.Code, dismissed.Body.String(), provider.closed)
				}
			}
		})
	}
}

func TestMeetingSpecialistProductPersistsApprovalBeforeLaunchAndFencesPersistenceFailure(t *testing.T) {
	for _, stage := range []string{"before_launch", "after_launch"} {
		t.Run(stage, func(t *testing.T) {
			_, productAuthority, user := specialistProductFixture(t)
			dir := t.TempDir()
			persistence := &MeetingSpecialistProductPersistence{
				SnapshotPath: filepath.Join(dir, "specialists.snapshot.json"), GenerationPath: filepath.Join(dir, "specialists.generation.json"),
				Authority: STRIDESnapshotMACAuthority{KeyID: "specialist_persist_order_key", Key: []byte("0123456789abcdef0123456789abcdef")}, MinimumGeneration: 1, BootstrapEmpty: true,
			}
			now := time.Date(2026, 7, 31, 19, 0, 0, 0, time.UTC)
			provider := &fakeMeetingSpecialistProvider{}
			joinCalls := 0
			product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: productAuthority, Persistence: persistence})
			var fixtureFactoryCalls atomic.Int64
			joiner, _ := productionJoinFixture(t, now, provider, &fixtureFactoryCalls)
			providerFactory := joiner.qualifiedProvider.create
			joiner.qualifiedProvider.create = func(ctx context.Context, launch MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
				joinCalls++
				var durable meetingSpecialistSnapshotEnvelope
				if err := readSTRIDERuntimeJSON(persistence.SnapshotPath, &durable); err != nil {
					t.Fatalf("approval was not durable before launch: %v", err)
				}
				foundApproved := false
				for _, stored := range durable.Payload.Records {
					if stored.Invitation.Header.ID == launch.Invitation.Header.ID && stored.Invitation.Decision == "approved" && stored.Status == "approved_waiting_for_provider_qualification" {
						foundApproved = true
					}
				}
				if !foundApproved {
					t.Fatalf("provider launch preceded durable approval: %+v", durable.Payload.Records)
				}
				createdProvider, err := providerFactory(ctx, launch)
				if err != nil {
					return createdProvider, err
				}
				if stage == "after_launch" {
					if err := os.Remove(persistence.GenerationPath); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(persistence.GenerationPath, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				return createdProvider, nil
			}
			product.productionJoin = joiner
			requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review positioning before launch", "persist-order-"+stage, 5*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if stage == "before_launch" {
				if err := os.Remove(persistence.GenerationPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(persistence.GenerationPath, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			_, resolveErr := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved")
			if !errors.Is(resolveErr, ErrMeetingSpecialistProductRestore) {
				t.Fatalf("%s persistence error=%v", stage, resolveErr)
			}
			if stage == "before_launch" && joinCalls != 0 {
				t.Fatalf("approval persistence failure launched provider %d times", joinCalls)
			}
			if stage == "after_launch" && (joinCalls != 1 || provider.briefs != 1 || provider.closed != 1) {
				t.Fatalf("post-launch persistence failure did not revoke: joins=%d briefs=%d closes=%d", joinCalls, provider.briefs, provider.closed)
			}
			product.mu.Lock()
			record := product.invitations[requested.ID]
			enabled, healthErr := product.enabled, product.healthErr
			product.mu.Unlock()
			if enabled || healthErr == nil || record.Runtime != nil {
				t.Fatalf("%s persistence failure did not fail closed: enabled=%t health=%v record=%+v", stage, enabled, healthErr, record)
			}
		})
	}
}
