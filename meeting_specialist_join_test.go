package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeMeetingSpecialistQualificationAuthority struct {
	mu      sync.Mutex
	trusted bool
	status  MeetingSpecialistQualificationStatus
	err     error
	calls   int
}

func (authority *fakeMeetingSpecialistQualificationAuthority) Trusted() bool {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	return authority.trusted
}

func (authority *fakeMeetingSpecialistQualificationAuthority) Current(_ context.Context, request MeetingSpecialistQualificationRequest) (MeetingSpecialistQualificationStatus, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.calls++
	return authority.status, authority.err
}

func meetingSpecialistQualificationFixture(now time.Time) (MeetingSpecialistQualificationRequest, *fakeMeetingSpecialistQualificationAuthority) {
	candidate := specialistCandidateFixture("mary", "dog-perfect")
	binding := MeetingSpecialistQualificationRequest{
		TenantID: "bonfire", ResultID: "specialist-e10-result", ResultDigest: strideTestDigest("1"),
		TargetID: "specialist-realtime-target", TargetDigest: strideTestDigest("2"), CandidateRelease: strings.Repeat("a", 40),
		CandidateTreeDigest: strideTestDigest("3"), CandidateImageDigest: strideTestDigest("4"), CandidateConfigDigest: strideTestDigest("5"), CandidateRouteDigest: strideTestDigest("6"),
		Provider: "openai", ProviderModel: meetingSpecialistRealtimeModel, ProviderRoute: "openai-realtime-websocket", ProviderRouteDigest: strideTestDigest("7"),
		AccountingMode: MeetingSpecialistRealtimeInputDirectPCM, SpecialistProfile: candidate.Profile, SpecialistCapability: candidate.Capability,
	}
	subjectDigest, _ := MeetingSpecialistQualificationSubjectDigest(binding)
	authority := &fakeMeetingSpecialistQualificationAuthority{trusted: true, status: MeetingSpecialistQualificationStatus{SubjectDigest: subjectDigest, Qualified: true, QualifiedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}}
	return binding, authority
}

func productionJoinFixture(now time.Time, provider *fakeMeetingSpecialistProvider, factoryCalls *atomic.Int64) (*MeetingSpecialistProductionJoiner, *fakeMeetingSpecialistAuthority) {
	authority := newFakeMeetingSpecialistAuthority()
	qualificationTarget, qualification := meetingSpecialistQualificationFixture(now)
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
		PublishAudio:  func(MeetingAgentFloorScope, uint64, []int16) error { return nil },
		Qualification: qualification, QualificationTarget: qualificationTarget,
	})
	return joiner, authority
}

func productionJoinRequestFixture(t *testing.T, now time.Time) MeetingSpecialistJoinRequest {
	t.Helper()
	product, authority, user := specialistProductFixture(t)
	requested, err := product.Request(context.Background(), user, "dog-perfect", "mary", "Review positioning", "production-request-fixture", time.Minute)
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
	return MeetingSpecialistJoinRequest{Invitation: record.Invitation, Candidate: record.Agent, Scope: authority.scope, Limits: record.Limits}
}

func TestMeetingSpecialistProductionJoinRequiresExactCurrentExternalQualificationBeforeAssembly(t *testing.T) {
	now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
	request := productionJoinRequestFixture(t, now)
	for _, scenario := range []struct {
		name   string
		mutate func(*MeetingSpecialistProductionJoiner, *fakeMeetingSpecialistQualificationAuthority)
	}{
		{name: "missing authority", mutate: func(joiner *MeetingSpecialistProductionJoiner, _ *fakeMeetingSpecialistQualificationAuthority) {
			joiner.qualification = nil
		}},
		{name: "untrusted authority", mutate: func(_ *MeetingSpecialistProductionJoiner, authority *fakeMeetingSpecialistQualificationAuthority) {
			authority.trusted = false
		}},
		{name: "unqualified", mutate: func(_ *MeetingSpecialistProductionJoiner, authority *fakeMeetingSpecialistQualificationAuthority) {
			authority.status.Qualified = false
		}},
		{name: "stale", mutate: func(_ *MeetingSpecialistProductionJoiner, authority *fakeMeetingSpecialistQualificationAuthority) {
			authority.status.ExpiresAt = now
		}},
		{name: "cross result id", mutate: func(joiner *MeetingSpecialistProductionJoiner, _ *fakeMeetingSpecialistQualificationAuthority) {
			joiner.qualificationTarget.ResultID = "other-result"
		}},
		{name: "cross result digest", mutate: func(joiner *MeetingSpecialistProductionJoiner, _ *fakeMeetingSpecialistQualificationAuthority) {
			joiner.qualificationTarget.ResultDigest = strideTestDigest("8")
		}},
		{name: "cross target id", mutate: func(joiner *MeetingSpecialistProductionJoiner, _ *fakeMeetingSpecialistQualificationAuthority) {
			joiner.qualificationTarget.TargetID = "other-target"
		}},
		{name: "cross target digest", mutate: func(joiner *MeetingSpecialistProductionJoiner, _ *fakeMeetingSpecialistQualificationAuthority) {
			joiner.qualificationTarget.TargetDigest = strideTestDigest("9")
		}},
		{name: "cross release", mutate: func(joiner *MeetingSpecialistProductionJoiner, _ *fakeMeetingSpecialistQualificationAuthority) {
			joiner.qualificationTarget.CandidateRelease = strings.Repeat("b", 40)
		}},
		{name: "cross tree", mutate: func(joiner *MeetingSpecialistProductionJoiner, _ *fakeMeetingSpecialistQualificationAuthority) {
			joiner.qualificationTarget.CandidateTreeDigest = strideTestDigest("a")
		}},
		{name: "cross image", mutate: func(joiner *MeetingSpecialistProductionJoiner, _ *fakeMeetingSpecialistQualificationAuthority) {
			joiner.qualificationTarget.CandidateImageDigest = strideTestDigest("b")
		}},
		{name: "cross config", mutate: func(joiner *MeetingSpecialistProductionJoiner, _ *fakeMeetingSpecialistQualificationAuthority) {
			joiner.qualificationTarget.CandidateConfigDigest = strideTestDigest("c")
		}},
		{name: "cross candidate route", mutate: func(joiner *MeetingSpecialistProductionJoiner, _ *fakeMeetingSpecialistQualificationAuthority) {
			joiner.qualificationTarget.CandidateRouteDigest = strideTestDigest("d")
		}},
		{name: "cross provider", mutate: func(joiner *MeetingSpecialistProductionJoiner, _ *fakeMeetingSpecialistQualificationAuthority) {
			joiner.qualificationTarget.Provider = "other-provider"
		}},
		{name: "cross model", mutate: func(joiner *MeetingSpecialistProductionJoiner, _ *fakeMeetingSpecialistQualificationAuthority) {
			joiner.qualificationTarget.ProviderModel = "other-model"
		}},
		{name: "cross provider route", mutate: func(joiner *MeetingSpecialistProductionJoiner, _ *fakeMeetingSpecialistQualificationAuthority) {
			joiner.qualificationTarget.ProviderRoute = "other-route"
		}},
		{name: "cross provider route digest", mutate: func(joiner *MeetingSpecialistProductionJoiner, _ *fakeMeetingSpecialistQualificationAuthority) {
			joiner.qualificationTarget.ProviderRouteDigest = strideTestDigest("e")
		}},
		{name: "cross accounting", mutate: func(joiner *MeetingSpecialistProductionJoiner, _ *fakeMeetingSpecialistQualificationAuthority) {
			joiner.qualificationTarget.AccountingMode = MeetingSpecialistRealtimeInputBoundedTranscript
		}},
		{name: "cross specialist profile", mutate: func(joiner *MeetingSpecialistProductionJoiner, _ *fakeMeetingSpecialistQualificationAuthority) {
			joiner.qualificationTarget.SpecialistProfile = strideTestRef(STRIDEContractAgentCoreProfile, "other-profile")
		}},
		{name: "cross specialist capability", mutate: func(joiner *MeetingSpecialistProductionJoiner, _ *fakeMeetingSpecialistQualificationAuthority) {
			joiner.qualificationTarget.SpecialistCapability = strideTestRef(STRIDEContractAgentCapabilityManifest, "other-capability")
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var providerCalls atomic.Int64
			joiner, _ := productionJoinFixture(now, &fakeMeetingSpecialistProvider{}, &providerCalls)
			qualification := joiner.qualification.(*fakeMeetingSpecialistQualificationAuthority)
			var resolverCalls, minterCalls atomic.Int64
			resolver := joiner.resolveCurrent
			joiner.resolveCurrent = func(ctx context.Context, request MeetingSpecialistJoinRequest) (MeetingSpecialistJoinAssembly, error) {
				resolverCalls.Add(1)
				return resolver(ctx, request)
			}
			minter := joiner.mintCapability
			joiner.mintCapability = func(ctx context.Context, request MeetingSpecialistCapabilityRequest) (string, error) {
				minterCalls.Add(1)
				return minter(ctx, request)
			}
			scenario.mutate(joiner, qualification)
			if joiner.Ready() {
				t.Fatal("product availability remained true without exact current qualification")
			}
			if _, err := joiner.Join(context.Background(), request); !errors.Is(err, ErrMeetingSpecialistJoinQualification) {
				t.Fatalf("join err=%v", err)
			}
			if resolverCalls.Load() != 0 || minterCalls.Load() != 0 || providerCalls.Load() != 0 {
				t.Fatalf("qualification failure crossed startup boundary: resolver=%d minter=%d provider=%d", resolverCalls.Load(), minterCalls.Load(), providerCalls.Load())
			}
		})
	}
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

func TestMeetingSpecialistProductionHTTPApprovalHandsRuntimeLifetimeToProduct(t *testing.T) {
	setupAuthTestEnv(t)
	now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
	product, _, _ := specialistProductFixture(t)
	provider := &fakeMeetingSpecialistProvider{}
	var factoryCalls atomic.Int64
	joiner, _ := productionJoinFixture(now, provider, &factoryCalls)
	product.productionJoin = joiner
	previous := kanbanApp
	kanbanApp = &kanbanBoardApp{meetingSpecialists: product}
	t.Cleanup(func() { kanbanApp = previous })
	cookies := loginAs(t, "aj@shareability.com", defaultMeetingRoomPassword)

	post := func(ctx context.Context, path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)).WithContext(ctx)
		request.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		meetingSpecialistProductInvitationHandler(recorder, request)
		return recorder
	}
	requested := post(context.Background(), "/api/stride/v1/meeting-specialists/invitations", `{"roomId":"dog-perfect","agentId":"mary","purpose":"review positioning","idempotencyKey":"production-http-lifetime"}`)
	if requested.Code != http.StatusOK {
		t.Fatalf("request status=%d body=%s", requested.Code, requested.Body.String())
	}
	var requestPayload struct {
		Invitation meetingSpecialistInvitationView `json:"invitation"`
	}
	if err := json.Unmarshal(requested.Body.Bytes(), &requestPayload); err != nil {
		t.Fatal(err)
	}

	approvalContext, finishApprovalRequest := context.WithCancel(context.Background())
	approved := post(approvalContext, "/api/stride/v1/meeting-specialists/invitations/"+requestPayload.Invitation.ID, `{"roomId":"dog-perfect","revision":1,"decision":"approved"}`)
	if approved.Code != http.StatusOK {
		t.Fatalf("approval status=%d body=%s", approved.Code, approved.Body.String())
	}
	var approvedPayload struct {
		Invitation meetingSpecialistInvitationView `json:"invitation"`
	}
	if err := json.Unmarshal(approved.Body.Bytes(), &approvedPayload); err != nil {
		t.Fatal(err)
	}
	if approvedPayload.Invitation.Status != "joined_session" || factoryCalls.Load() != 1 {
		t.Fatalf("approval=%+v factoryCalls=%d", approvedPayload, factoryCalls.Load())
	}
	product.mu.Lock()
	record := product.invitations[requestPayload.Invitation.ID]
	product.mu.Unlock()
	if record.Runtime == nil || record.Runtime.Snapshot().Session == nil {
		t.Fatalf("joined runtime absent: %+v", record)
	}

	// net/http cancels a request context after ServeHTTP returns. The joined
	// runtime must already belong to the product/session authority by then.
	finishApprovalRequest()
	record.Runtime.mu.Lock()
	runtimeContextErr := record.Runtime.ctx.Err()
	record.Runtime.mu.Unlock()
	provider.mu.Lock()
	closedAfterResponse := provider.closed
	provider.mu.Unlock()
	if runtimeContextErr != nil || record.Runtime.Snapshot().Session == nil || closedAfterResponse != 0 {
		t.Fatalf("HTTP request lifetime escaped handoff: ctxErr=%v snapshot=%+v providerClosed=%d", runtimeContextErr, record.Runtime.Snapshot(), closedAfterResponse)
	}

	dismissed := post(context.Background(), "/api/stride/v1/meeting-specialists/invitations/"+requestPayload.Invitation.ID, `{"roomId":"dog-perfect","revision":2,"decision":"dismissed"}`)
	if dismissed.Code != http.StatusOK {
		t.Fatalf("dismissal status=%d body=%s", dismissed.Code, dismissed.Body.String())
	}
	product.mu.Lock()
	dismissedRecord := product.invitations[requestPayload.Invitation.ID]
	product.mu.Unlock()
	provider.mu.Lock()
	closedAfterDismissal := provider.closed
	provider.mu.Unlock()
	record.Runtime.mu.Lock()
	dismissedContextErr := record.Runtime.ctx.Err()
	record.Runtime.mu.Unlock()
	if dismissedRecord.Status != "dismissed" || dismissedRecord.Runtime != nil || record.Runtime.Snapshot().Session != nil || dismissedContextErr == nil || closedAfterDismissal != 1 {
		t.Fatalf("product dismissal did not own terminal cleanup: record=%+v snapshot=%+v providerClosed=%d", dismissedRecord, record.Runtime.Snapshot(), closedAfterDismissal)
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

	startupContext, cancelStartup := context.WithCancel(context.Background())
	originalMinter := joiner.mintCapability
	joiner.mintCapability = func(ctx context.Context, capabilityRequest MeetingSpecialistCapabilityRequest) (string, error) {
		receipt, err := originalMinter(ctx, capabilityRequest)
		cancelStartup()
		return receipt, err
	}
	if _, err := joiner.Join(startupContext, request); !errors.Is(err, context.Canceled) || factoryCalls.Load() != 0 {
		t.Fatalf("post-mint cancellation dialed provider: err=%v factoryCalls=%d", err, factoryCalls.Load())
	}
	joiner.mintCapability = originalMinter

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
