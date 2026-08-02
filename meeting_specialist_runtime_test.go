package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeMeetingSpecialistProvider struct {
	mu          sync.Mutex
	briefs      int
	humanWrites int
	responses   []uint64
	cancels     []uint64
	closed      int
	failHuman   bool
	onBrief     func()
	onHuman     func()
	onResponse  func()
	onClose     func()
	hooks       MeetingSpecialistProviderHooks
	receipt     MeetingSpecialistProviderReceipt
}

type fakeMeetingSpecialistAuthority struct {
	mu         sync.Mutex
	issued     map[string]string
	used       map[string]bool
	current    bool
	revoked    map[string]bool
	generation uint64
	onCurrent  func()
}

func newFakeMeetingSpecialistAuthority() *fakeMeetingSpecialistAuthority {
	return &fakeMeetingSpecialistAuthority{issued: map[string]string{}, used: map[string]bool{}, current: true, revoked: map[string]bool{}}
}

func (authority *fakeMeetingSpecialistAuthority) issue(launch MeetingSpecialistLaunch) string {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	request := meetingSpecialistCapabilityRequest(launch)
	request.Receipt = ""
	token := "specialist-capability-" + workDigest(request)[:24]
	authority.issued[token] = workDigest(request)
	authority.generation = launch.Scope.MediaGeneration
	return token
}

func (authority *fakeMeetingSpecialistAuthority) ConsumeLaunch(_ context.Context, request MeetingSpecialistCapabilityRequest) (MeetingSpecialistAuthorization, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	token := request.Receipt
	request.Receipt = ""
	binding := workDigest(request)
	if !authority.current || authority.revoked[token] || authority.used[token] || authority.issued[token] != binding || request.MediaGeneration != authority.generation {
		return MeetingSpecialistAuthorization{}, errors.New("capability denied")
	}
	authority.used[token] = true
	return MeetingSpecialistAuthorization{ID: "specialist-authorization-1", BindingDigest: binding, ExpiresAt: request.ExpiresAt}, nil
}

func (authority *fakeMeetingSpecialistAuthority) Current(_ context.Context, authorization MeetingSpecialistAuthorization, request MeetingSpecialistCapabilityRequest) error {
	authority.mu.Lock()
	valid := authority.current && request.MediaGeneration == authority.generation && authorization.BindingDigest == workDigest(request)
	onCurrent := authority.onCurrent
	authority.mu.Unlock()
	if onCurrent != nil {
		onCurrent()
	}
	if !valid {
		return errors.New("authorization stale")
	}
	return nil
}

func (provider *fakeMeetingSpecialistProvider) Brief(_ context.Context, _ MeetingSpecialistContextEnvelope) error {
	provider.mu.Lock()
	provider.briefs++
	onBrief := provider.onBrief
	provider.mu.Unlock()
	if onBrief != nil {
		onBrief()
	}
	return nil
}
func (provider *fakeMeetingSpecialistProvider) WriteHumanPCM(_ context.Context, _ uint64, _ []int16) error {
	provider.mu.Lock()
	provider.humanWrites++
	fail, onHuman := provider.failHuman, provider.onHuman
	provider.mu.Unlock()
	if onHuman != nil {
		onHuman()
	}
	if fail {
		return errors.New("provider failed")
	}
	return nil
}
func (provider *fakeMeetingSpecialistProvider) BeginResponse(_ context.Context, floor MeetingAgentFloorLease) error {
	provider.mu.Lock()
	provider.responses = append(provider.responses, floor.Generation)
	onResponse := provider.onResponse
	provider.mu.Unlock()
	if onResponse != nil {
		onResponse()
	}
	return nil
}
func (provider *fakeMeetingSpecialistProvider) CancelResponse(_ context.Context, generation uint64, _ string) error {
	provider.mu.Lock()
	provider.cancels = append(provider.cancels, generation)
	provider.mu.Unlock()
	return nil
}
func (provider *fakeMeetingSpecialistProvider) Close(_ context.Context, _ string) error {
	provider.mu.Lock()
	provider.closed++
	onClose := provider.onClose
	provider.mu.Unlock()
	if onClose != nil {
		onClose()
	}
	return nil
}

func (provider *fakeMeetingSpecialistProvider) BindMeetingSpecialistProviderHooks(hooks MeetingSpecialistProviderHooks) error {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.hooks = hooks
	return nil
}

func (provider *fakeMeetingSpecialistProvider) MeetingSpecialistProviderReceipt() MeetingSpecialistProviderReceipt {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.receipt
}

func specialistRuntimeLaunchFixture(now time.Time) MeetingSpecialistLaunch {
	invitationRef := strideTestRef(STRIDEContractMeetingAgentInvitation, "invite-1")
	invitationRef.Digest = strideTestDigest("a")
	decisionAt := now.Add(-time.Second)
	eligibility := strideTestRef(STRIDEContractAgentAssignment, "meeting_eligibility_"+temporalDigest("mary\x00dog-perfect")[:20])
	invitation := MeetingAgentInvitation{
		Header: strideTestHeader(STRIDEContractMeetingAgentInvitation, "invite-1"), RoomID: "dog-perfect", SittingID: "sitting-1",
		SpecialistProfile: strideTestRef(STRIDEContractAgentCoreProfile, "profile-mary"), Capability: strideTestRef(STRIDEContractAgentCapabilityManifest, "capability-marketing"),
		Eligibility: &eligibility,
		Requester:   "user:0123456789abcdef01234567", EligibleConfirmer: "user:abcdef0123456789abcdef01", PurposeDigest: strideTestDigest("c"), ContextClasses: []string{"meeting-analysis"},
		SourceIntervalDigest: strideTestDigest("d"), Audience: STRIDEAudience{Visibility: "meeting", Principals: []string{"user:0123456789abcdef01234567", "user:abcdef0123456789abcdef01"}},
		ConsentPolicyRevision: strideTestRef(STRIDEContractKnowledgeAssertion, "consent-policy-1"), ExpectedTimeSeconds: 180, ExpectedCostCents: 50,
		ExpiresAt: now.Add(10 * time.Minute), Decision: "approved", DecisionPrincipal: "user:abcdef0123456789abcdef01", DecisionAt: &decisionAt, IdempotencyKeyDigest: strideTestDigest("e"),
	}
	contextEnvelope := MeetingSpecialistContextEnvelope{
		Header: strideTestHeader(STRIDEContractMeetingSpecialistContext, "specialist-context-1"), Invitation: invitationRef,
		AgentProfile: invitation.SpecialistProfile, RuntimeRevision: strideTestRef(STRIDEContractDelegationRun, "runtime-1"), ModelRevision: strideTestRef(STRIDEContractAgentCapabilityManifest, "model-route-1"),
		TranscriptRefs: []STRIDEReference{strideTestRef(STRIDEContractTranscriptRevision, "transcript-1")}, AnalysisRefs: []STRIDEReference{strideTestRef(STRIDEContractAnalysisProjection, "analysis-1")},
		BrainRefs: []STRIDEReference{strideTestRef(STRIDEContractKnowledgeAssertion, "brain-1")}, WorkRefs: []STRIDEReference{strideTestRef(STRIDEContractWorkRun, "work-1")},
		Audience: invitation.Audience, RetentionDigest: strideTestDigest("f"), TranscriptHighWater: 8, AnalysisHighWater: 7, BrainHighWater: 6,
		GapsDigest: strideTestDigest("1"), CoverageDigest: strideTestDigest("2"), ToolIDs: []string{"meeting-context-read"}, ResponseContract: "one-bounded-contribution", FloorPolicy: "human-priority-v1",
		TimeBudgetSeconds: 120, TurnBudget: 3, AudioBudgetSeconds: 45, TokenBudget: 1500, CostBudgetCents: 25, ContextDigest: strideTestDigest("3"),
	}
	return MeetingSpecialistLaunch{
		Scope:      MeetingAgentFloorScope{RoomID: "dog-perfect", SittingID: "sitting-1", MediaGeneration: 4, InvitationID: "invite-1", SessionID: "session-mary-1", AgentID: "mary", RuntimePrincipal: "runtime-mary-1", AudioTrackID: "verified-mary-track"},
		Invitation: invitation, Context: contextEnvelope,
		Policy: MeetingAgentFloorPolicy{SessionTTL: 2 * time.Minute, MaxFloorLease: 20 * time.Second, TurnBudget: 3, AudioBudgetSecond: 45, CostBudgetCents: 25}, ApprovalLimits: defaultMeetingSpecialistApprovalLimits(),
	}
}

func enabledSpecialistHarnessGates() MeetingSpecialistGates {
	return MeetingSpecialistGates{TokenMinting: true, Invitation: true, ContextAssembly: true, ProviderSession: true, AudioPublication: true, VisibleProfile: true}
}

func TestMeetingSpecialistRuntimeDefaultOffDoesNotCallFactory(t *testing.T) {
	now := time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC)
	called := false
	runtime := NewMeetingSpecialistRuntime(func() time.Time { return now }, MeetingSpecialistGates{}, nil, func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
		called = true
		return &fakeMeetingSpecialistProvider{}, nil
	}, nil)
	if _, err := runtime.Start(context.Background(), specialistRuntimeLaunchFixture(now)); !errors.Is(err, ErrMeetingSpecialistDisabled) || called {
		t.Fatalf("default-off start err=%v called=%v", err, called)
	}
}

func TestMeetingSpecialistRuntimeShortestLifetimeAndAutonomousTerminalEvidence(t *testing.T) {
	base := time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC)
	start := func(t *testing.T, now func() time.Time, parent context.Context, mutate func(*MeetingSpecialistLaunch), provider *fakeMeetingSpecialistProvider) (*MeetingSpecialistRuntime, MeetingAgentSessionLease, <-chan MeetingSpecialistTerminalEvidence) {
		t.Helper()
		authority := newFakeMeetingSpecialistAuthority()
		launch := specialistRuntimeLaunchFixture(now().UTC())
		if mutate != nil {
			mutate(&launch)
		}
		launch.CapabilityReceipt = authority.issue(launch)
		runtime := NewMeetingSpecialistRuntime(now, enabledSpecialistHarnessGates(), authority, func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
			return provider, nil
		}, func(MeetingAgentFloorScope, uint64, []int16) error { return nil })
		session, err := runtime.Start(parent, launch)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		terminal := make(chan MeetingSpecialistTerminalEvidence, 1)
		if !runtime.BindTerminalObserver(func(evidence MeetingSpecialistTerminalEvidence) { terminal <- evidence }) {
			t.Fatal("bind terminal observer")
		}
		return runtime, session, terminal
	}
	await := func(t *testing.T, terminal <-chan MeetingSpecialistTerminalEvidence) MeetingSpecialistTerminalEvidence {
		t.Helper()
		select {
		case evidence := <-terminal:
			return evidence
		case <-time.After(2 * time.Second):
			t.Fatal("terminal evidence was not delivered")
			return MeetingSpecialistTerminalEvidence{}
		}
	}

	t.Run("policy ttl is a hard autonomous expiry", func(t *testing.T) {
		provider := &fakeMeetingSpecialistProvider{receipt: MeetingSpecialistProviderReceipt{BindingDigest: strideTestDigest("7"), TerminalStatus: "completed"}}
		runtime, _, terminal := start(t, func() time.Time { return base }, context.Background(), func(launch *MeetingSpecialistLaunch) {
			launch.Policy.SessionTTL = 25 * time.Millisecond
			launch.Policy.MaxFloorLease = 10 * time.Millisecond
		}, provider)
		evidence := await(t, terminal)
		if evidence.TerminalReason != "expired" || evidence.Cause != "session_ttl_expired" || evidence.EndedAt.IsZero() || !isHexDigest(evidence.TeardownReceiptDigest) || evidence.ProviderReceipt != provider.receipt {
			t.Fatalf("policy expiry evidence=%+v", evidence)
		}
		if snapshot := runtime.Snapshot(); snapshot.Session != nil || snapshot.TeardownReceiptDigest != evidence.TeardownReceiptDigest {
			t.Fatalf("policy expiry snapshot=%+v evidence=%+v", snapshot, evidence)
		}
	})

	t.Run("parent deadline is a shorter hard expiry", func(t *testing.T) {
		parent, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()
		runtime, _, terminal := start(t, func() time.Time { return base }, parent, nil, &fakeMeetingSpecialistProvider{})
		evidence := await(t, terminal)
		if evidence.TerminalReason != "expired" || evidence.Cause != "parent_context_deadline" || !isHexDigest(evidence.TeardownReceiptDigest) {
			t.Fatalf("parent deadline evidence=%+v", evidence)
		}
		if runtime.Snapshot().Session != nil {
			t.Fatal("parent deadline left floor authority active")
		}
	})

	t.Run("clock-discovered expiry denies tool authority and tears down", func(t *testing.T) {
		var clockMu sync.Mutex
		now := base
		nowValue := func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return now
		}
		provider := &fakeMeetingSpecialistProvider{}
		runtime, session, terminal := start(t, nowValue, context.Background(), func(launch *MeetingSpecialistLaunch) {
			launch.Policy.SessionTTL = time.Minute
			launch.Policy.MaxFloorLease = 20 * time.Second
		}, provider)
		runtime.mu.Lock()
		runtime.gates.Tools = true
		runtime.mu.Unlock()
		clockMu.Lock()
		now = base.Add(time.Minute)
		clockMu.Unlock()
		if err := runtime.AuthorizeTool(session, "meeting-context-read"); !errors.Is(err, ErrMeetingSpecialistUnauthorized) {
			t.Fatalf("expired tool authority err=%v", err)
		}
		evidence := await(t, terminal)
		if evidence.TerminalReason != "expired" || evidence.Cause != "session_ttl_expired" || runtime.Snapshot().Session != nil {
			t.Fatalf("tool expiry evidence=%+v snapshot=%+v", evidence, runtime.Snapshot())
		}
	})

	t.Run("provider failure preserves exact internal cause", func(t *testing.T) {
		provider := &fakeMeetingSpecialistProvider{receipt: MeetingSpecialistProviderReceipt{BindingDigest: strideTestDigest("8"), TerminalStatus: "failed", UsageStatus: "usage_unreconciled"}}
		_, _, terminal := start(t, func() time.Time { return base }, context.Background(), nil, provider)
		provider.mu.Lock()
		failSession := provider.hooks.FailSession
		provider.mu.Unlock()
		if failSession == nil {
			t.Fatal("provider failure hook was not bound")
		}
		failSession("provider_usage_unreconciled")
		evidence := await(t, terminal)
		if evidence.TerminalReason != "failed" || evidence.Cause != "provider_usage_unreconciled" || evidence.ProviderReceipt != provider.receipt || !isHexDigest(evidence.TeardownReceiptDigest) {
			t.Fatalf("provider terminal evidence=%+v", evidence)
		}
	})
}

func TestMeetingSpecialistRuntimeBindsContextHumanInputFloorAndBargeIn(t *testing.T) {
	now := time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC)
	provider := &fakeMeetingSpecialistProvider{}
	authority := newFakeMeetingSpecialistAuthority()
	var published int
	runtime := NewMeetingSpecialistRuntime(func() time.Time { return now }, enabledSpecialistHarnessGates(), authority, func(_ context.Context, launch MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
		if launch.Context.ContextDigest == "" {
			t.Fatal("context not bound")
		}
		return provider, nil
	}, func(scope MeetingAgentFloorScope, _ uint64, pcm []int16) error {
		if scope.AudioTrackID != "verified-mary-track" || len(pcm) == 0 {
			t.Fatal("bad audio publication")
		}
		published++
		return nil
	})
	launch := specialistRuntimeLaunchFixture(now)
	launch.CapabilityReceipt = authority.issue(launch)
	session, err := runtime.Start(context.Background(), launch)
	if err != nil {
		t.Fatal(err)
	}
	if provider.briefs != 1 {
		t.Fatalf("briefs=%d", provider.briefs)
	}
	if err := runtime.SendHumanAudio(session, "human-aj-track", []int16{1, 2}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SendHumanAudio(session, session.Scope.AudioTrackID, []int16{1}); !errors.Is(err, ErrMeetingAgentFloorFeedback) {
		t.Fatalf("feedback err=%v", err)
	}
	floor, err := runtime.RequestTurn(session, "approved_scout_handoff", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.PublishProviderAudio(floor, []int16{4, 5}, 2, 1); err != nil {
		t.Fatal(err)
	}
	if published != 1 {
		t.Fatalf("published=%d", published)
	}
	interruption, ok := runtime.HumanBargeIn(launch.Scope.RoomID, launch.Scope.SittingID, launch.Scope.MediaGeneration)
	if !ok || !interruption.CancelProvider {
		t.Fatalf("interruption=%+v ok=%v", interruption, ok)
	}
	if err := runtime.PublishProviderAudio(floor, []int16{6}, 1, 0); !errors.Is(err, ErrMeetingAgentFloorFence) {
		t.Fatalf("stale output err=%v", err)
	}
	if len(provider.cancels) != 1 || provider.cancels[0] != floor.Generation {
		t.Fatalf("cancels=%v", provider.cancels)
	}
}

func TestMeetingSpecialistRuntimeReauthorizesAfterFactoryAndBrief(t *testing.T) {
	now := time.Date(2026, 7, 31, 18, 0, 0, 0, time.UTC)
	for _, stage := range []string{"factory", "brief"} {
		t.Run(stage, func(t *testing.T) {
			authority := newFakeMeetingSpecialistAuthority()
			provider := &fakeMeetingSpecialistProvider{}
			if stage == "brief" {
				provider.onBrief = func() {
					authority.mu.Lock()
					authority.current = false
					authority.mu.Unlock()
				}
			}
			runtime := NewMeetingSpecialistRuntime(func() time.Time { return now }, enabledSpecialistHarnessGates(), authority, func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
				if stage == "factory" {
					authority.mu.Lock()
					authority.current = false
					authority.mu.Unlock()
				}
				return provider, nil
			}, func(MeetingAgentFloorScope, uint64, []int16) error {
				t.Fatal("revoked launch published audio")
				return nil
			})
			launch := specialistRuntimeLaunchFixture(now)
			launch.CapabilityReceipt = authority.issue(launch)
			if _, err := runtime.Start(context.Background(), launch); !errors.Is(err, ErrMeetingSpecialistUnauthorized) {
				t.Fatalf("%s revocation error=%v", stage, err)
			}
			provider.mu.Lock()
			briefs, closes := provider.briefs, provider.closed
			provider.mu.Unlock()
			wantBriefs := 0
			if stage == "brief" {
				wantBriefs = 1
			}
			if briefs != wantBriefs || closes != 1 {
				t.Fatalf("%s provider lifecycle briefs=%d closes=%d", stage, briefs, closes)
			}
			if snapshot := runtime.Snapshot(); snapshot.Session != nil || snapshot.TeardownReceiptDigest == "" {
				t.Fatalf("%s revoked launch remained active: %+v", stage, snapshot)
			}
		})
	}
}

func TestMeetingSpecialistRuntimeApprovalGuestAndContextBoundsFailClosed(t *testing.T) {
	now := time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*MeetingSpecialistLaunch){
		"unapproved": func(launch *MeetingSpecialistLaunch) {
			launch.Invitation.Decision = "requested"
			launch.Invitation.DecisionPrincipal = ""
			launch.Invitation.DecisionAt = nil
		},
		"guest":         func(launch *MeetingSpecialistLaunch) { launch.Invitation.Requester = "guest-sam" },
		"wrong context": func(launch *MeetingSpecialistLaunch) { launch.Context.Invitation.ID = "invite-other" },
		"missing eligibility": func(launch *MeetingSpecialistLaunch) {
			launch.Invitation.Eligibility = nil
		},
		"budget widen": func(launch *MeetingSpecialistLaunch) {
			launch.Context.CostBudgetCents = launch.Invitation.ExpectedCostCents + 1
		},
		"approval limit widen": func(launch *MeetingSpecialistLaunch) {
			launch.Context.TokenBudget = launch.ApprovalLimits.TokenBudget + 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			launch := specialistRuntimeLaunchFixture(now)
			mutate(&launch)
			authority := newFakeMeetingSpecialistAuthority()
			launch.CapabilityReceipt = authority.issue(launch)
			called := false
			runtime := NewMeetingSpecialistRuntime(func() time.Time { return now }, enabledSpecialistHarnessGates(), authority, func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
				called = true
				return &fakeMeetingSpecialistProvider{}, nil
			}, nil)
			if _, err := runtime.Start(context.Background(), launch); !errors.Is(err, ErrMeetingSpecialistUnauthorized) || called {
				t.Fatalf("err=%v called=%v", err, called)
			}
		})
	}
}

func TestMeetingSpecialistRuntimeToolsAreIndependentlyDisabledAndKillSwitchTearsDown(t *testing.T) {
	now := time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC)
	provider := &fakeMeetingSpecialistProvider{}
	authority := newFakeMeetingSpecialistAuthority()
	runtime := NewMeetingSpecialistRuntime(func() time.Time { return now }, enabledSpecialistHarnessGates(), authority, func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
		return provider, nil
	}, func(MeetingAgentFloorScope, uint64, []int16) error { return nil })
	launch := specialistRuntimeLaunchFixture(now)
	launch.CapabilityReceipt = authority.issue(launch)
	session, err := runtime.Start(context.Background(), launch)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.AuthorizeTool(session, "meeting-context-read"); !errors.Is(err, ErrMeetingSpecialistToolDenied) {
		t.Fatalf("tool err=%v", err)
	}
	runtime.RevokeGates("kill_switch")
	if snapshot := runtime.Snapshot(); snapshot.Session != nil || snapshot.TeardownReceiptDigest == "" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if provider.closed != 1 {
		t.Fatalf("provider closes=%d", provider.closed)
	}
	if err := runtime.SendHumanAudio(session, "human-track", []int16{1}); !errors.Is(err, ErrMeetingSpecialistClosed) {
		t.Fatalf("post-kill input err=%v", err)
	}
}

func TestMeetingSpecialistProviderFailureDoesNotOwnHumanMedia(t *testing.T) {
	now := time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC)
	provider := &fakeMeetingSpecialistProvider{failHuman: true}
	authority := newFakeMeetingSpecialistAuthority()
	humanTrackOpen := true
	runtime := NewMeetingSpecialistRuntime(func() time.Time { return now }, enabledSpecialistHarnessGates(), authority, func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
		return provider, nil
	}, func(MeetingAgentFloorScope, uint64, []int16) error { return nil })
	launch := specialistRuntimeLaunchFixture(now)
	launch.CapabilityReceipt = authority.issue(launch)
	session, err := runtime.Start(context.Background(), launch)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.SendHumanAudio(session, "human-track", []int16{1}); err == nil {
		t.Fatal("expected provider failure")
	}
	if !humanTrackOpen {
		t.Fatal("specialist failure touched human media")
	}
	if provider.closed != 1 {
		t.Fatalf("provider closes=%d", provider.closed)
	}
}

func TestMeetingSpecialistCapabilityRejectsForgedReplayRevokedStaleAndMutatedLaunch(t *testing.T) {
	now := time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC)
	authority := newFakeMeetingSpecialistAuthority()
	launch := specialistRuntimeLaunchFixture(now)
	validReceipt := authority.issue(launch)
	called := 0
	newRuntime := func() *MeetingSpecialistRuntime {
		return NewMeetingSpecialistRuntime(func() time.Time { return now }, enabledSpecialistHarnessGates(), authority, func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
			called++
			return &fakeMeetingSpecialistProvider{}, nil
		}, func(MeetingAgentFloorScope, uint64, []int16) error { return nil })
	}
	for name, mutate := range map[string]func(*MeetingSpecialistLaunch){
		"forged": func(value *MeetingSpecialistLaunch) { value.CapabilityReceipt = "forged" },
		"mutated context": func(value *MeetingSpecialistLaunch) {
			value.CapabilityReceipt = validReceipt
			value.Context.ContextDigest = strideTestDigest("9")
		},
		"mutated eligibility": func(value *MeetingSpecialistLaunch) {
			value.CapabilityReceipt = validReceipt
			changed := *value.Invitation.Eligibility
			changed.Digest = strideTestDigest("8")
			value.Invitation.Eligibility = &changed
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := launch
			mutate(&candidate)
			if _, err := newRuntime().Start(context.Background(), candidate); !errors.Is(err, ErrMeetingSpecialistUnauthorized) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	if called != 0 {
		t.Fatalf("provider created for unauthorized launch: %d", called)
	}

	launch.CapabilityReceipt = validReceipt
	runtime := newRuntime()
	session, err := runtime.Start(context.Background(), launch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newRuntime().Start(context.Background(), launch); !errors.Is(err, ErrMeetingSpecialistUnauthorized) {
		t.Fatalf("replayed capability err=%v", err)
	}
	authority.current = false // consent/invitation/membership revocation
	if err := runtime.SendHumanAudio(session, "human-track", []int16{1}); !errors.Is(err, ErrMeetingSpecialistUnauthorized) {
		t.Fatalf("revoked audio err=%v", err)
	}
	authority.current = true
	baseNow := now
	now = launch.Invitation.ExpiresAt
	if _, err := runtime.RequestTurn(session, "approved_scout_handoff", time.Second); !errors.Is(err, ErrMeetingSpecialistUnauthorized) {
		t.Fatalf("expired capability err=%v", err)
	}
	now = baseNow
	staleAuthority := newFakeMeetingSpecialistAuthority()
	staleLaunch := specialistRuntimeLaunchFixture(now)
	staleLaunch.CapabilityReceipt = staleAuthority.issue(staleLaunch)
	staleRuntime := NewMeetingSpecialistRuntime(func() time.Time { return now }, enabledSpecialistHarnessGates(), staleAuthority, func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
		return &fakeMeetingSpecialistProvider{}, nil
	}, func(MeetingAgentFloorScope, uint64, []int16) error { return nil })
	staleSession, err := staleRuntime.Start(context.Background(), staleLaunch)
	if err != nil {
		t.Fatal(err)
	}
	staleAuthority.generation++ // room was replaced/rejoined
	if _, err := staleRuntime.RequestTurn(staleSession, "approved_scout_handoff", time.Second); !errors.Is(err, ErrMeetingSpecialistUnauthorized) {
		t.Fatalf("stale room generation err=%v", err)
	}
	_ = staleRuntime.RevokeGates("test_cleanup")
}

func waitMeetingSpecialistRuntimeClosed(t *testing.T, specialist *MeetingSpecialistRuntime) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		specialist.mu.Lock()
		closed := specialist.closed
		specialist.mu.Unlock()
		if closed {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("runtime did not enter revoked state")
}

func assertMeetingSpecialistRevokeWaits(t *testing.T, specialist *MeetingSpecialistRuntime, release chan struct{}) {
	t.Helper()
	done := make(chan struct{})
	go func() { specialist.RevokeGates("concurrent_revoke"); close(done) }()
	waitMeetingSpecialistRuntimeClosed(t, specialist)
	select {
	case <-done:
		t.Fatal("revoke returned while an admitted side effect was blocked")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("revoke did not drain after side effect returned")
	}
}

func TestMeetingSpecialistRuntimeRevokeLinearizesBlockedLaunchProviderPublisherAndTool(t *testing.T) {
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

	t.Run("launch factory", func(t *testing.T) {
		authority := newFakeMeetingSpecialistAuthority()
		provider := &fakeMeetingSpecialistProvider{}
		entered, release := make(chan struct{}), make(chan struct{})
		specialist := NewMeetingSpecialistRuntime(func() time.Time { return now }, enabledSpecialistHarnessGates(), authority, func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
			close(entered)
			<-release
			return provider, nil
		}, nil)
		launch := specialistRuntimeLaunchFixture(now)
		launch.CapabilityReceipt = authority.issue(launch)
		startResult := make(chan error, 1)
		go func() { _, err := specialist.Start(context.Background(), launch); startResult <- err }()
		<-entered
		assertMeetingSpecialistRevokeWaits(t, specialist, release)
		if err := <-startResult; !errors.Is(err, ErrMeetingSpecialistFence) && !errors.Is(err, ErrMeetingSpecialistUnauthorized) {
			t.Fatalf("start after revoke err=%v", err)
		}
		provider.mu.Lock()
		closes := provider.closed
		provider.mu.Unlock()
		if closes != 1 {
			t.Fatalf("provider closes=%d", closes)
		}
		if snapshot := specialist.Snapshot(); snapshot.Session != nil {
			t.Fatalf("revoked launch survived: %+v", snapshot)
		}
	})

	t.Run("provider input", func(t *testing.T) {
		authority := newFakeMeetingSpecialistAuthority()
		entered, release := make(chan struct{}), make(chan struct{})
		provider := &fakeMeetingSpecialistProvider{onHuman: func() { close(entered); <-release }}
		specialist := NewMeetingSpecialistRuntime(func() time.Time { return now }, enabledSpecialistHarnessGates(), authority, func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
			return provider, nil
		}, nil)
		launch := specialistRuntimeLaunchFixture(now)
		launch.CapabilityReceipt = authority.issue(launch)
		session, err := specialist.Start(context.Background(), launch)
		if err != nil {
			t.Fatal(err)
		}
		inputResult := make(chan error, 1)
		go func() { inputResult <- specialist.SendHumanAudio(session, "human-track", []int16{1}) }()
		<-entered
		assertMeetingSpecialistRevokeWaits(t, specialist, release)
		if err = <-inputResult; err != nil {
			t.Fatalf("admitted input err=%v", err)
		}
		if err = specialist.SendHumanAudio(session, "human-track", []int16{2}); !errors.Is(err, ErrMeetingSpecialistClosed) {
			t.Fatalf("post-revoke input err=%v", err)
		}
		provider.mu.Lock()
		writes := provider.humanWrites
		provider.mu.Unlock()
		if writes != 1 {
			t.Fatalf("provider writes=%d", writes)
		}
	})

	t.Run("audio publisher", func(t *testing.T) {
		authority := newFakeMeetingSpecialistAuthority()
		provider := &fakeMeetingSpecialistProvider{}
		entered, release := make(chan struct{}), make(chan struct{})
		var publishMu sync.Mutex
		published := 0
		specialist := NewMeetingSpecialistRuntime(func() time.Time { return now }, enabledSpecialistHarnessGates(), authority, func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
			return provider, nil
		}, func(MeetingAgentFloorScope, uint64, []int16) error {
			close(entered)
			<-release
			publishMu.Lock()
			published++
			publishMu.Unlock()
			return nil
		})
		launch := specialistRuntimeLaunchFixture(now)
		launch.CapabilityReceipt = authority.issue(launch)
		session, err := specialist.Start(context.Background(), launch)
		if err != nil {
			t.Fatal(err)
		}
		floor, err := specialist.RequestTurn(session, "approved_scout_handoff", time.Second)
		if err != nil {
			t.Fatal(err)
		}
		publishResult := make(chan error, 1)
		go func() { publishResult <- specialist.PublishProviderAudio(floor, []int16{1}, 1, 0) }()
		<-entered
		assertMeetingSpecialistRevokeWaits(t, specialist, release)
		if err = <-publishResult; err != nil {
			t.Fatalf("admitted publish err=%v", err)
		}
		if err = specialist.PublishProviderAudio(floor, []int16{2}, 1, 0); !errors.Is(err, ErrMeetingSpecialistClosed) {
			t.Fatalf("post-revoke publish err=%v", err)
		}
		publishMu.Lock()
		count := published
		publishMu.Unlock()
		if count != 1 {
			t.Fatalf("published=%d", count)
		}
	})

	t.Run("tool grant", func(t *testing.T) {
		authority := newFakeMeetingSpecialistAuthority()
		gates := enabledSpecialistHarnessGates()
		gates.Tools = true
		specialist := NewMeetingSpecialistRuntime(func() time.Time { return now }, gates, authority, func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
			return &fakeMeetingSpecialistProvider{}, nil
		}, nil)
		launch := specialistRuntimeLaunchFixture(now)
		launch.CapabilityReceipt = authority.issue(launch)
		session, err := specialist.Start(context.Background(), launch)
		if err != nil {
			t.Fatal(err)
		}
		entered, release := make(chan struct{}), make(chan struct{})
		authority.mu.Lock()
		authority.onCurrent = func() { close(entered); <-release }
		authority.mu.Unlock()
		toolResult := make(chan error, 1)
		go func() { toolResult <- specialist.AuthorizeTool(session, "meeting-context-read") }()
		<-entered
		assertMeetingSpecialistRevokeWaits(t, specialist, release)
		if err = <-toolResult; !errors.Is(err, ErrMeetingSpecialistUnauthorized) {
			t.Fatalf("stale tool grant err=%v", err)
		}
		if err = specialist.AuthorizeTool(session, "meeting-context-read"); !errors.Is(err, ErrMeetingSpecialistClosed) {
			t.Fatalf("post-revoke tool err=%v", err)
		}
	})
}

func TestMeetingSpecialistRuntimeStopDrainsBlockedProviderTurnBeforeReturning(t *testing.T) {
	now := time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)
	authority := newFakeMeetingSpecialistAuthority()
	entered, release := make(chan struct{}), make(chan struct{})
	provider := &fakeMeetingSpecialistProvider{onResponse: func() { close(entered); <-release }}
	specialist := NewMeetingSpecialistRuntime(func() time.Time { return now }, enabledSpecialistHarnessGates(), authority, func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
		return provider, nil
	}, nil)
	launch := specialistRuntimeLaunchFixture(now)
	launch.CapabilityReceipt = authority.issue(launch)
	session, err := specialist.Start(context.Background(), launch)
	if err != nil {
		t.Fatal(err)
	}
	turnResult := make(chan error, 1)
	go func() {
		_, turnErr := specialist.RequestTurn(session, "approved_scout_handoff", time.Second)
		turnResult <- turnErr
	}()
	<-entered
	stopResult := make(chan error, 1)
	go func() { stopResult <- specialist.Stop(session, "room_closed") }()
	waitMeetingSpecialistRuntimeClosed(t, specialist)
	select {
	case err = <-stopResult:
		t.Fatalf("stop returned before admitted provider turn drained: %v", err)
	default:
	}
	close(release)
	if err = <-turnResult; err != nil {
		t.Fatalf("admitted turn err=%v", err)
	}
	select {
	case err = <-stopResult:
		if err != nil {
			t.Fatalf("stop err=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not finish after provider turn drained")
	}
	if _, err = specialist.RequestTurn(session, "approved_scout_handoff", time.Second); !errors.Is(err, ErrMeetingSpecialistClosed) {
		t.Fatalf("post-stop turn err=%v", err)
	}
	provider.mu.Lock()
	responses, closes := len(provider.responses), provider.closed
	provider.mu.Unlock()
	if responses != 1 || closes != 1 {
		t.Fatalf("responses=%d closes=%d", responses, closes)
	}
}

func TestMeetingSpecialistRuntimeRevocationBoundsPermanentlyBlockedWork(t *testing.T) {
	now := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	assertRevoked := func(t *testing.T, runtime *MeetingSpecialistRuntime, release chan struct{}) {
		t.Helper()
		runtime.shutdownTimeout = 25 * time.Millisecond
		started := time.Now()
		err := runtime.RevokeGates("concurrent_revoke")
		if !errors.Is(err, ErrMeetingSpecialistDrainTimeout) {
			t.Fatalf("bounded revoke err=%v", err)
		}
		if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
			t.Fatalf("bounded revoke took %s", elapsed)
		}
		snapshot := runtime.Snapshot()
		if snapshot.Session != nil || snapshot.TeardownReceiptDigest == "" || snapshot.TerminalReason == "" {
			t.Fatalf("bounded revoke omitted terminal receipt: %+v", snapshot)
		}
		close(release)
	}

	t.Run("factory", func(t *testing.T) {
		authority := newFakeMeetingSpecialistAuthority()
		entered, release := make(chan struct{}), make(chan struct{})
		runtime := NewMeetingSpecialistRuntime(func() time.Time { return now }, enabledSpecialistHarnessGates(), authority, func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
			close(entered)
			<-release
			return &fakeMeetingSpecialistProvider{}, nil
		}, nil)
		launch := specialistRuntimeLaunchFixture(now)
		launch.CapabilityReceipt = authority.issue(launch)
		result := make(chan error, 1)
		go func() { _, err := runtime.Start(context.Background(), launch); result <- err }()
		<-entered
		assertRevoked(t, runtime, release)
		if err := <-result; !errors.Is(err, ErrMeetingSpecialistFence) && !errors.Is(err, ErrMeetingSpecialistUnauthorized) {
			t.Fatalf("factory completion after revoke err=%v", err)
		}
	})

	for _, fixture := range []struct {
		name  string
		start func(*MeetingSpecialistRuntime, MeetingAgentSessionLease, chan struct{}, chan struct{})
	}{
		{name: "provider write", start: func(runtime *MeetingSpecialistRuntime, session MeetingAgentSessionLease, entered, release chan struct{}) {
			provider := runtime.provider.(*fakeMeetingSpecialistProvider)
			provider.mu.Lock()
			provider.onHuman = func() { close(entered); <-release }
			provider.mu.Unlock()
			go func() { _ = runtime.SendHumanAudio(session, "human-track", []int16{1}) }()
		}},
		{name: "publisher", start: func(runtime *MeetingSpecialistRuntime, session MeetingAgentSessionLease, entered, release chan struct{}) {
			floor, err := runtime.RequestTurn(session, "approved_scout_handoff", time.Second)
			if err != nil {
				close(entered)
				return
			}
			runtime.mu.Lock()
			runtime.publish = func(MeetingAgentFloorScope, uint64, []int16) error { close(entered); <-release; return nil }
			runtime.mu.Unlock()
			go func() { _ = runtime.PublishProviderAudio(floor, []int16{1}, 1, 0) }()
		}},
		{name: "authority", start: func(runtime *MeetingSpecialistRuntime, session MeetingAgentSessionLease, entered, release chan struct{}) {
			authority := runtime.authority.(*fakeMeetingSpecialistAuthority)
			authority.mu.Lock()
			authority.onCurrent = func() { close(entered); <-release }
			authority.mu.Unlock()
			go func() { _, _ = runtime.RequestTurn(session, "approved_scout_handoff", time.Second) }()
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			authority := newFakeMeetingSpecialistAuthority()
			provider := &fakeMeetingSpecialistProvider{}
			runtime := NewMeetingSpecialistRuntime(func() time.Time { return now }, enabledSpecialistHarnessGates(), authority, func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
				return provider, nil
			}, func(MeetingAgentFloorScope, uint64, []int16) error { return nil })
			launch := specialistRuntimeLaunchFixture(now)
			launch.CapabilityReceipt = authority.issue(launch)
			session, err := runtime.Start(context.Background(), launch)
			if err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			fixture.start(runtime, session, entered, release)
			<-entered
			assertRevoked(t, runtime, release)
		})
	}
}
