package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func productionJoinFixture(now time.Time, provider *fakeMeetingSpecialistProvider, factoryCalls *atomic.Int64) (*MeetingSpecialistProductionJoiner, *fakeMeetingSpecialistAuthority) {
	authority := newFakeMeetingSpecialistAuthority()
	gates := enabledSpecialistHarnessGates()
	gates.Tools = true
	joiner := NewMeetingSpecialistProductionJoiner(MeetingSpecialistProductionJoinConfig{
		Enabled: true, Now: func() time.Time { return now }, Gates: gates, CapabilityAuthority: authority,
		ResolveCurrent: func(_ context.Context, request MeetingSpecialistJoinRequest) (MeetingSpecialistJoinAssembly, error) {
			launch := specialistRuntimeLaunchFixture(now)
			launch.Context.Invitation = referenceFromHeader(request.Invitation.Header)
			launch.Context.AgentProfile = request.Candidate.Profile
			launch.Context.Audience = request.Invitation.Audience
			launch.Context.TimeBudgetSeconds = request.Limits.TimeBudgetSeconds
			launch.Context.TurnBudget = request.Limits.TurnBudget
			launch.Context.AudioBudgetSeconds = request.Limits.AudioBudgetSeconds
			launch.Context.TokenBudget = request.Limits.TokenBudget
			launch.Context.CostBudgetCents = request.Limits.CostBudgetCents
			launch.Policy.SessionTTL = time.Duration(request.Limits.TimeBudgetSeconds) * time.Second
			launch.Policy.MaxFloorLease = time.Duration(request.Limits.MaxFloorLeaseSeconds) * time.Second
			launch.Policy.TurnBudget = request.Limits.TurnBudget
			launch.Policy.AudioBudgetSecond = request.Limits.AudioBudgetSeconds
			launch.Policy.CostBudgetCents = request.Limits.CostBudgetCents
			return MeetingSpecialistJoinAssembly{
				SessionID: "production-session-mary", RuntimePrincipal: "production-runtime-mary", AudioTrackID: "production-track-mary",
				Context: launch.Context, Policy: launch.Policy,
			}, nil
		},
		MintCapability: func(_ context.Context, request MeetingSpecialistCapabilityRequest) (string, error) {
			request.Receipt = ""
			token := "specialist-capability-" + workDigest(request)[:24]
			authority.mu.Lock()
			authority.issued[token] = workDigest(request)
			authority.generation = request.MediaGeneration
			authority.mu.Unlock()
			return token, nil
		},
		ProviderFactory: func(_ context.Context, launch MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
			factoryCalls.Add(1)
			if launch.Scope.RuntimePrincipal != "production-runtime-mary" || launch.Context.ToolIDs[0] != "meeting-context-read" || launch.Policy.CostBudgetCents != launch.ApprovalLimits.CostBudgetCents {
				return nil, ErrMeetingSpecialistJoinAssembly
			}
			return provider, nil
		},
		PublishAudio: func(MeetingAgentFloorScope, uint64, []int16) error { return nil },
	})
	return joiner, authority
}

func TestMeetingSpecialistProductionJoinRequiresApprovalAndBindsServerAuthority(t *testing.T) {
	now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
	product, _, user := specialistProductFixture(t)
	provider := &fakeMeetingSpecialistProvider{}
	var factoryCalls atomic.Int64
	joiner, _ := productionJoinFixture(now, provider, &factoryCalls)
	product.productionJoin = joiner

	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review positioning", "production-join-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if factoryCalls.Load() != 0 {
		t.Fatal("request opened provider before explicit approval")
	}
	approved, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved")
	if err != nil || approved.Status != "joined_session" || !approved.ProviderSessionStarted || factoryCalls.Load() != 1 {
		t.Fatalf("approved=%+v factoryCalls=%d err=%v", approved, factoryCalls.Load(), err)
	}
	provider.mu.Lock()
	briefs := provider.briefs
	provider.mu.Unlock()
	if briefs != 1 {
		t.Fatalf("server-authorized context briefs=%d", briefs)
	}
	if _, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, approved.Revision, "dismissed"); err != nil {
		t.Fatal(err)
	}
}

func TestMeetingSpecialistProductionJoinFencesCancellationToolsAndBudgetsBeforeProvider(t *testing.T) {
	now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
	product, authority, user := specialistProductFixture(t)
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review positioning", "production-fence-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	product.mu.Lock()
	record := product.invitations[requested.ID]
	product.mu.Unlock()
	record.Invitation.Header.Revision++
	record.Invitation.Decision = "approved"
	record.Invitation.DecisionPrincipal = authority.scope.RequesterPrincipal
	decisionAt := now
	record.Invitation.DecisionAt = &decisionAt
	record.Invitation.Header.CreatedAt = now
	record.Invitation.Header.ContentDigest, _ = meetingSpecialistInvitationDigest(record.Invitation)
	request := MeetingSpecialistJoinRequest{Invitation: record.Invitation, Candidate: record.Agent, Scope: authority.scope, Limits: record.Limits}

	var factoryCalls atomic.Int64
	joiner, _ := productionJoinFixture(now, &fakeMeetingSpecialistProvider{}, &factoryCalls)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := joiner.Join(cancelled, request); !errors.Is(err, context.Canceled) || factoryCalls.Load() != 0 {
		t.Fatalf("cancelled join err=%v factoryCalls=%d", err, factoryCalls.Load())
	}

	joiner.gates.Tools = false
	if _, err := joiner.Join(context.Background(), request); !errors.Is(err, ErrMeetingSpecialistJoinAssembly) || factoryCalls.Load() != 0 {
		t.Fatalf("tool-authority join err=%v factoryCalls=%d", err, factoryCalls.Load())
	}

	joiner.gates.Tools = true
	originalResolver := joiner.resolveCurrent
	joiner.resolveCurrent = func(ctx context.Context, request MeetingSpecialistJoinRequest) (MeetingSpecialistJoinAssembly, error) {
		assembly, err := originalResolver(ctx, request)
		assembly.Context.TokenBudget = request.Limits.TokenBudget + 1
		return assembly, err
	}
	if _, err := joiner.Join(context.Background(), request); !errors.Is(err, ErrMeetingSpecialistJoinAssembly) || factoryCalls.Load() != 0 {
		t.Fatalf("over-budget join err=%v factoryCalls=%d", err, factoryCalls.Load())
	}

	disabled := NewMeetingSpecialistProductionJoiner(MeetingSpecialistProductionJoinConfig{ProviderFactory: joiner.providerFactory})
	if _, err := disabled.Join(context.Background(), request); !errors.Is(err, ErrMeetingSpecialistJoinDisabled) || factoryCalls.Load() != 0 {
		t.Fatalf("default-off join err=%v factoryCalls=%d", err, factoryCalls.Load())
	}
}

func TestMeetingSpecialistProductionJoinRestartRequiresFreshApproval(t *testing.T) {
	_, authority, user := specialistProductFixture(t)
	now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	persistence := &MeetingSpecialistProductPersistence{
		SnapshotPath: filepath.Join(dir, "specialists.snapshot.json"), GenerationPath: filepath.Join(dir, "specialists.generation.json"),
		Authority: STRIDESnapshotMACAuthority{KeyID: "production_join_restart_key", Key: []byte("0123456789abcdef0123456789abcdef")}, MinimumGeneration: 1, BootstrapEmpty: true,
	}
	provider := &fakeMeetingSpecialistProvider{}
	var factoryCalls atomic.Int64
	joiner, _ := productionJoinFixture(now, provider, &factoryCalls)
	product := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: persistence, ProductionJoin: joiner})
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review campaign", "production-restart-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := product.Resolve(context.Background(), user, "dog-perfect", requested.ID, requested.Revision, "approved")
	if err != nil || approved.Status != "joined_session" || factoryCalls.Load() != 1 {
		t.Fatalf("approved=%+v calls=%d err=%v", approved, factoryCalls.Load(), err)
	}

	restoreConfig := *persistence
	restoreConfig.BootstrapEmpty = false
	restarted := NewMeetingSpecialistProduct(MeetingSpecialistProductConfig{Enabled: true, TenantID: "bonfire", Now: func() time.Time { return now }, Authority: authority, Persistence: &restoreConfig, ProductionJoin: joiner})
	status := restarted.Status(context.Background(), user, "dog-perfect")
	if len(status.Invitations) != 1 || status.Invitations[0].Status != "approved_reauthorization_required" || status.Invitations[0].ProviderSessionStarted || factoryCalls.Load() != 1 {
		t.Fatalf("restart status=%+v calls=%d", status, factoryCalls.Load())
	}
	product.Close("test_cleanup")
	restarted.Close("test_cleanup")
}
