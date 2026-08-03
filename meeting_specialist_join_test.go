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
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-realtime-meeting-assistant/internal/e10evidence"
)

func meetingSpecialistQualificationFixture() MeetingSpecialistQualificationRequest {
	candidate := specialistCandidateFixture("mary", "dog-perfect")
	binding := e10evidence.MeetingSpecialistQualificationBinding{
		Provider: meetingSpecialistProviderName, Model: meetingSpecialistRealtimeModel, Voice: defaultRealtimeVoice, RouteDigest: strideTestDigest("6"),
		AccountingProfileDigest: strideTestDigest("7"), RuntimeProfileDigest: strideTestDigest("8"), CapabilityPolicyDigest: meetingSpecialistCapabilityPolicyDigest(candidate.Profile, candidate.Capability),
	}
	subjectDigest := e10evidence.MeetingSpecialistQualificationFixtureDigest(binding)
	return MeetingSpecialistQualificationRequest{
		TenantID: "bonfire", ResultID: "specialist-e10-result", TargetID: meetingSpecialistQualificationTargetID,
		EvaluatorConfigDigest: strideTestDigest("1"), EvaluatorResultDigest: strideTestDigest("2"), FixtureDigest: subjectDigest, QualificationSubjectDigest: subjectDigest,
		Candidate: e10evidence.CandidateBinding{ReleaseCommit: strings.Repeat("a", 40), GitTreeDigest: strideTestDigest("3"), ImageDigest: strideTestDigest("4"), ConfigDigest: strideTestDigest("5"), RouteMapDigest: binding.RouteDigest},
		Binding:   binding, SpecialistProfile: candidate.Profile, SpecialistCapability: candidate.Capability,
	}
}

func meetingSpecialistQualificationFixtureForCandidate(tenantID string, candidate MeetingSpecialistCandidate) MeetingSpecialistQualificationRequest {
	request := meetingSpecialistQualificationFixture()
	request.TenantID = tenantID
	request.SpecialistProfile = candidate.Profile
	request.SpecialistCapability = candidate.Capability
	request.Binding.CapabilityPolicyDigest = meetingSpecialistCapabilityPolicyDigest(candidate.Profile, candidate.Capability)
	subject := e10evidence.MeetingSpecialistQualificationFixtureDigest(request.Binding)
	request.FixtureDigest = subject
	request.QualificationSubjectDigest = subject
	return request
}

func meetingSpecialistQualificationStore(t *testing.T, request MeetingSpecialistQualificationRequest, now, evaluatedAt time.Time) *QualificationEvidenceStore {
	t.Helper()
	fixture := verifiedMeetingSpecialistQualificationBundle(t, request, evaluatedAt)
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	anchorAuthority := newTestQualificationAnchorAuthority(t, request.TenantID, fixture.rootsRaw)
	store, err := OpenTrustedQualificationEvidenceStore(filepath.Join(directory, "specialist-qualification.jsonl"), QualificationEvidenceSeed{}, request.TenantID, func() time.Time { return now }, QualificationEvidenceTrustConfig{AnchorAuthority: anchorAuthority})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ImportQualificationBundle(fixture.bundleRaw); err != nil {
		t.Fatal(err)
	}
	return store
}

func testMeetingSpecialistQualifiedProviderFactory(t *testing.T, request MeetingSpecialistQualificationRequest, create MeetingSpecialistProviderFactory) *MeetingSpecialistQualifiedProviderFactory {
	t.Helper()
	subjectDigest, err := MeetingSpecialistQualificationSubjectDigest(request)
	if err != nil || create == nil || subjectDigest != request.QualificationSubjectDigest {
		t.Fatalf("invalid deterministic qualified-provider fixture: subject=%s err=%v", subjectDigest, err)
	}
	return &MeetingSpecialistQualifiedProviderFactory{request: request, subjectDigest: subjectDigest, create: create}
}

func productionJoinFixtureAt(t *testing.T, now, evaluatedAt time.Time, provider *fakeMeetingSpecialistProvider, factoryCalls *atomic.Int64) (*MeetingSpecialistProductionJoiner, *fakeMeetingSpecialistAuthority) {
	t.Helper()
	return productionJoinFixtureForQualification(t, now, evaluatedAt, meetingSpecialistQualificationFixture(), provider, factoryCalls)
}

func productionJoinFixtureForQualification(t *testing.T, now, evaluatedAt time.Time, qualificationTarget MeetingSpecialistQualificationRequest, provider *fakeMeetingSpecialistProvider, factoryCalls *atomic.Int64) (*MeetingSpecialistProductionJoiner, *fakeMeetingSpecialistAuthority) {
	t.Helper()
	authority := newFakeMeetingSpecialistAuthority()
	qualificationStore := meetingSpecialistQualificationStore(t, qualificationTarget, now, evaluatedAt)
	qualifiedProvider := testMeetingSpecialistQualifiedProviderFactory(t, qualificationTarget, func(_ context.Context, launch MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
		factoryCalls.Add(1)
		if launch.Scope.RuntimePrincipal != "production-runtime-mary" || launch.Context.ToolIDs[0] != "meeting-context-read" || launch.Policy.CostBudgetCents != launch.ApprovalLimits.CostBudgetCents {
			return nil, ErrMeetingSpecialistJoinAssembly
		}
		return provider, nil
	})
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
		QualifiedProvider: qualifiedProvider, QualificationStore: qualificationStore,
		PublishAudio: func(MeetingAgentFloorScope, uint64, []int16) error { return nil },
	})
	return joiner, authority
}

func productionJoinFixture(t *testing.T, now time.Time, provider *fakeMeetingSpecialistProvider, factoryCalls *atomic.Int64) (*MeetingSpecialistProductionJoiner, *fakeMeetingSpecialistAuthority) {
	t.Helper()
	return productionJoinFixtureAt(t, now, now.Add(-time.Minute), provider, factoryCalls)
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

func TestNewMeetingSpecialistQualifiedRealtimeFactoryDerivesExactProductionBinding(t *testing.T) {
	candidate := specialistCandidateFixture("mary", "dog-perfect")
	config := defaultOffMeetingSpecialistRealtimeConfig()
	config.Enabled = true
	config.APIKey = "test-key-never-dialed"
	config.ReasoningEffort = "low"
	config.ResolveBrief = func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistRealtimeBrief, error) {
		return MeetingSpecialistRealtimeBrief{}, nil
	}
	binding := meetingSpecialistQualificationBinding(config, MeetingSpecialistRealtimeInputDirectPCM, candidate.Profile, candidate.Capability)
	legacyRelativeRouteDigest := workDigest(struct {
		Schema, Provider, Route, Endpoint, Model, InputFormat, OutputFormat string
	}{"stride.meeting-specialist-provider-route/v1", meetingSpecialistProviderName, meetingSpecialistProviderRoute, "/v1/realtime?model=" + config.Model, config.Model, "audio/pcm@24000", "audio/pcm@24000"})
	if binding.RouteDigest == legacyRelativeRouteDigest {
		t.Fatal("production route binding omitted the dialed endpoint origin")
	}
	subject := e10evidence.MeetingSpecialistQualificationFixtureDigest(binding)
	deployment := MeetingSpecialistQualificationDeployment{
		TenantID: "bonfire", ResultID: "specialist-e10-result", TargetID: meetingSpecialistQualificationTargetID,
		EvaluatorConfigDigest: strideTestDigest("1"), EvaluatorResultDigest: strideTestDigest("2"), FixtureDigest: subject,
		Candidate:      e10evidence.CandidateBinding{ReleaseCommit: strings.Repeat("a", 40), GitTreeDigest: strideTestDigest("3"), ImageDigest: strideTestDigest("4"), ConfigDigest: strideTestDigest("5"), RouteMapDigest: binding.RouteDigest},
		AccountingMode: MeetingSpecialistRealtimeInputDirectPCM, SpecialistProfile: candidate.Profile, SpecialistCapability: candidate.Capability,
	}
	factory, err := NewMeetingSpecialistQualifiedRealtimeFactory(config, deployment)
	if err != nil || factory == nil || factory.create == nil || factory.subjectDigest != subject || factory.request.Binding != binding || factory.request.Candidate != deployment.Candidate {
		t.Fatalf("production factory binding=%+v subject=%s err=%v", factory, subject, err)
	}
	tampered := deployment
	tampered.Candidate.RouteMapDigest = strideTestDigest("6")
	if _, err := NewMeetingSpecialistQualifiedRealtimeFactory(config, tampered); !errors.Is(err, ErrMeetingSpecialistJoinQualification) {
		t.Fatalf("candidate-to-route mismatch was accepted: %v", err)
	}
	config.Model = "other-model"
	if _, err := NewMeetingSpecialistQualifiedRealtimeFactory(config, deployment); !errors.Is(err, ErrMeetingSpecialistJoinQualification) {
		t.Fatalf("noncanonical production model was accepted: %v", err)
	}
	config.Model = meetingSpecialistRealtimeModel
	config.InputMode = MeetingSpecialistRealtimeInputBoundedTranscript
	config.MaxInputTokensPerTurn = 128
	if _, err := NewMeetingSpecialistQualifiedRealtimeFactory(config, deployment); !errors.Is(err, ErrMeetingSpecialistJoinQualification) {
		t.Fatalf("accounting mode divergent from the actual config was accepted: %v", err)
	}
	config.InputMode = MeetingSpecialistRealtimeInputDirectPCM
	config.MaxInputTokensPerTurn = 1
	if _, err := NewMeetingSpecialistQualifiedRealtimeFactory(config, deployment); !errors.Is(err, ErrMeetingSpecialistJoinQualification) {
		t.Fatalf("invalid direct-PCM accounting was accepted: %v", err)
	}
}

func TestMeetingSpecialistProductionJoinRequiresExactCurrentExternalQualificationBeforeAssembly(t *testing.T) {
	now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
	request := productionJoinRequestFixture(t, now)
	scenarios := []struct {
		name   string
		want   error
		mutate func(*MeetingSpecialistProductionJoiner)
	}{
		{name: "missing evidence store", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualificationStore = nil
		}},
		{name: "missing sealed provider", want: ErrMeetingSpecialistJoinAssembly, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider = nil
		}},
		{name: "cross result id", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.ResultID = "other-result"
		}},
		{name: "cross target id", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.TargetID = "other-target"
		}},
		{name: "cross evaluator config", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.EvaluatorConfigDigest = strideTestDigest("a")
		}},
		{name: "cross evaluator result", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.EvaluatorResultDigest = strideTestDigest("b")
		}},
		{name: "cross fixture", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.FixtureDigest = strideTestDigest("c")
		}},
		{name: "cross subject", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.QualificationSubjectDigest = strideTestDigest("d")
		}},
		{name: "cross release", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.Candidate.ReleaseCommit = strings.Repeat("b", 40)
		}},
		{name: "cross tree", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.Candidate.GitTreeDigest = strideTestDigest("e")
		}},
		{name: "cross image", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.Candidate.ImageDigest = strideTestDigest("f")
		}},
		{name: "cross config", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.Candidate.ConfigDigest = strideTestDigest("0")
		}},
		{name: "cross route map", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.Candidate.RouteMapDigest = strideTestDigest("1")
		}},
		{name: "cross provider", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.Binding.Provider = "anthropic"
		}},
		{name: "cross model", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.Binding.Model = "other-model"
		}},
		{name: "cross voice", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.Binding.Voice = "other-voice"
		}},
		{name: "cross provider route", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.Binding.RouteDigest = strideTestDigest("2")
		}},
		{name: "cross accounting", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.Binding.AccountingProfileDigest = strideTestDigest("3")
		}},
		{name: "cross runtime", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.Binding.RuntimeProfileDigest = strideTestDigest("4")
		}},
		{name: "cross capability policy", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.Binding.CapabilityPolicyDigest = strideTestDigest("5")
		}},
		{name: "cross specialist profile", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.SpecialistProfile = strideTestRef(STRIDEContractAgentCoreProfile, "other-profile")
		}},
		{name: "cross specialist capability", want: ErrMeetingSpecialistJoinQualification, mutate: func(joiner *MeetingSpecialistProductionJoiner) {
			joiner.qualifiedProvider.request.SpecialistCapability = strideTestRef(STRIDEContractAgentCapabilityManifest, "other-capability")
		}},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			var providerCalls atomic.Int64
			joiner, _ := productionJoinFixture(t, now, &fakeMeetingSpecialistProvider{}, &providerCalls)
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
			scenario.mutate(joiner)
			if joiner.Ready() {
				t.Fatal("product availability remained true without exact current qualification")
			}
			if _, err := joiner.Join(context.Background(), request); !errors.Is(err, scenario.want) {
				t.Fatalf("join err=%v, want %v", err, scenario.want)
			}
			if resolverCalls.Load() != 0 || minterCalls.Load() != 0 || providerCalls.Load() != 0 {
				t.Fatalf("qualification failure crossed startup boundary: resolver=%d minter=%d provider=%d", resolverCalls.Load(), minterCalls.Load(), providerCalls.Load())
			}
		})
	}

	t.Run("fixed seven day freshness boundary", func(t *testing.T) {
		var staleCalls atomic.Int64
		stale, _ := productionJoinFixtureAt(t, now, now.Add(-meetingSpecialistQualificationMaxAge), &fakeMeetingSpecialistProvider{}, &staleCalls)
		if stale.Ready() {
			t.Fatal("qualification at the fixed expiry instant remained ready")
		}
		if _, err := stale.Join(context.Background(), request); !errors.Is(err, ErrMeetingSpecialistJoinQualification) || staleCalls.Load() != 0 {
			t.Fatalf("stale join err=%v providerCalls=%d", err, staleCalls.Load())
		}

		var validCalls atomic.Int64
		valid, _ := productionJoinFixtureAt(t, now, now.Add(-meetingSpecialistQualificationMaxAge+10*time.Second), &fakeMeetingSpecialistProvider{}, &validCalls)
		if !valid.Ready() {
			t.Fatal("qualification immediately before the fixed expiry boundary was rejected")
		}
		runtime, err := valid.Join(context.Background(), request)
		if err != nil || runtime == nil || validCalls.Load() != 1 {
			t.Fatalf("pre-boundary join runtime=%v calls=%d err=%v", runtime, validCalls.Load(), err)
		}
		_ = runtime.Stop(runtime.lease, "test_cleanup")
	})

	t.Run("live qualification expiry preserves its exact terminal cause", func(t *testing.T) {
		provider := &fakeMeetingSpecialistProvider{receipt: MeetingSpecialistProviderReceipt{
			BindingDigest: strideTestDigest("e"), TerminalStatus: "completed",
		}}
		var factoryCalls atomic.Int64
		joiner, _ := productionJoinFixtureAt(
			t,
			now,
			now.Add(-meetingSpecialistQualificationMaxAge+250*time.Millisecond),
			provider,
			&factoryCalls,
		)
		runtime, err := joiner.Join(context.Background(), request)
		if err != nil || runtime == nil || factoryCalls.Load() != 1 {
			t.Fatalf("short-lived qualified join runtime=%v calls=%d err=%v", runtime, factoryCalls.Load(), err)
		}
		terminal := make(chan MeetingSpecialistTerminalEvidence, 1)
		if !runtime.BindTerminalObserver(func(evidence MeetingSpecialistTerminalEvidence) { terminal <- evidence }) {
			t.Fatal("bind qualification-expiry terminal observer")
		}
		select {
		case evidence := <-terminal:
			if evidence.TerminalReason != "expired" || evidence.Cause != "qualification_expired" ||
				!isHexDigest(evidence.TeardownReceiptDigest) || evidence.ProviderReceipt.BindingDigest != provider.receipt.BindingDigest ||
				evidence.ProviderReceipt.QualificationSubjectDigest != evidence.QualificationSubjectDigest || runtime.Snapshot().Session != nil {
				t.Fatalf("qualification expiry evidence=%+v snapshot=%+v", evidence, runtime.Snapshot())
			}
		case <-time.After(2 * time.Second):
			t.Fatal("live qualification expiry did not emit terminal evidence")
		}
	})

	t.Run("qualification is rechecked after factory and brief latency", func(t *testing.T) {
		current := now
		var factoryCalls atomic.Int64
		provider := &fakeMeetingSpecialistProvider{}
		joiner, _ := productionJoinFixtureAt(t, now, now.Add(-meetingSpecialistQualificationMaxAge+time.Second), provider, &factoryCalls)
		joiner.now = func() time.Time { return current }
		create := joiner.qualifiedProvider.create
		joiner.qualifiedProvider.create = func(ctx context.Context, launch MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
			created, createErr := create(ctx, launch)
			current = now.Add(2 * time.Second)
			return created, createErr
		}
		if _, err := joiner.Join(context.Background(), request); !errors.Is(err, ErrMeetingSpecialistUnauthorized) || factoryCalls.Load() != 1 || provider.closed != 1 {
			t.Fatalf("factory-latency expiry escaped: calls=%d closed=%d err=%v", factoryCalls.Load(), provider.closed, err)
		}

		current = now
		factoryCalls.Store(0)
		provider = &fakeMeetingSpecialistProvider{onBrief: func() { current = now.Add(2 * time.Second) }}
		joiner, _ = productionJoinFixtureAt(t, now, now.Add(-meetingSpecialistQualificationMaxAge+time.Second), provider, &factoryCalls)
		joiner.now = func() time.Time { return current }
		if _, err := joiner.Join(context.Background(), request); !errors.Is(err, ErrMeetingSpecialistUnauthorized) || factoryCalls.Load() != 1 || provider.closed != 1 {
			t.Fatalf("brief-latency expiry escaped: calls=%d closed=%d err=%v", factoryCalls.Load(), provider.closed, err)
		}
	})
}
func TestMeetingSpecialistProductionJoinRequiresApprovalAndBindsServerAuthority(t *testing.T) {
	now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
	product, _, user := specialistProductFixture(t)
	provider := &fakeMeetingSpecialistProvider{}
	var factoryCalls atomic.Int64
	joiner, _ := productionJoinFixture(t, now, provider, &factoryCalls)
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
	joiner, _ := productionJoinFixture(t, now, provider, &factoryCalls)
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
	joiner, _ := productionJoinFixture(t, now, &fakeMeetingSpecialistProvider{}, &factoryCalls)
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

	disabled := NewMeetingSpecialistProductionJoiner(MeetingSpecialistProductionJoinConfig{QualifiedProvider: joiner.qualifiedProvider})
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
	joiner, _ := productionJoinFixture(t, now, provider, &factoryCalls)
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
