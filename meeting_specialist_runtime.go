package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	ErrMeetingSpecialistDisabled     = errors.New("meeting specialist capability is disabled")
	ErrMeetingSpecialistUnauthorized = errors.New("meeting specialist launch is unauthorized")
	ErrMeetingSpecialistFence        = errors.New("meeting specialist session fence mismatch")
	ErrMeetingSpecialistClosed       = errors.New("meeting specialist session is closed")
	ErrMeetingSpecialistToolDenied   = errors.New("meeting specialist tool is denied")
	ErrMeetingSpecialistDrainTimeout = errors.New("meeting specialist shutdown drain timed out")
)

const meetingSpecialistRuntimeShutdownTimeout = 2 * time.Second

// MeetingSpecialistGates mirror the independently revocable E1 switches. A
// zero value is deliberately unusable. The deterministic harness may set them
// without enabling the production STRIDERegistry or opening a provider call.
type MeetingSpecialistGates struct {
	TokenMinting     bool `json:"tokenMinting"`
	Invitation       bool `json:"invitation"`
	ContextAssembly  bool `json:"contextAssembly"`
	ProviderSession  bool `json:"providerSession"`
	AudioPublication bool `json:"audioPublication"`
	Tools            bool `json:"tools"`
	VisibleProfile   bool `json:"visibleProfile"`
}

func (gates MeetingSpecialistGates) launchEnabled() bool {
	return gates.TokenMinting && gates.Invitation && gates.ContextAssembly && gates.ProviderSession && gates.AudioPublication && gates.VisibleProfile
}

type MeetingSpecialistLaunch struct {
	Scope             MeetingAgentFloorScope           `json:"scope"`
	Invitation        MeetingAgentInvitation           `json:"invitation"`
	Context           MeetingSpecialistContextEnvelope `json:"context"`
	Policy            MeetingAgentFloorPolicy          `json:"policy"`
	ApprovalLimits    MeetingSpecialistApprovalLimits  `json:"approvalLimits"`
	CapabilityReceipt string                           `json:"-"`
}

func (launch MeetingSpecialistLaunch) Validate(now time.Time) error {
	if launch.Scope.Validate() != nil || launch.Invitation.Validate() != nil || launch.Context.Validate() != nil || launch.Policy.Validate() != nil ||
		launch.Invitation.Eligibility == nil || launch.Invitation.Eligibility.Validate() != nil || launch.Invitation.Eligibility.ContractType != STRIDEContractAgentAssignment ||
		launch.Invitation.Decision != "approved" || launch.Invitation.DecisionAt == nil || launch.Invitation.DecisionPrincipal != launch.Invitation.EligibleConfirmer ||
		!now.Before(launch.Invitation.ExpiresAt) || strings.HasPrefix(strings.ToLower(launch.Invitation.Requester), "guest") ||
		strings.HasPrefix(strings.ToLower(launch.Invitation.EligibleConfirmer), "guest") || launch.Invitation.Audience.Visibility != "meeting" || !meetingSpecialistMembersOnlyAudience(launch.Invitation.Audience) ||
		launch.Scope.RoomID != launch.Invitation.RoomID || launch.Scope.SittingID != launch.Invitation.SittingID ||
		launch.Scope.InvitationID != launch.Invitation.Header.ID || launch.Context.Invitation.ID != launch.Invitation.Header.ID ||
		launch.Context.Invitation.Revision != launch.Invitation.Header.Revision || launch.Context.Invitation.Digest != launch.Invitation.Header.ContentDigest ||
		launch.Context.AgentProfile != launch.Invitation.SpecialistProfile || !sameMeetingSpecialistAudience(launch.Context.Audience, launch.Invitation.Audience) || launch.Context.ContextDigest == "" ||
		launch.Context.TimeBudgetSeconds > launch.Invitation.ExpectedTimeSeconds || launch.Context.CostBudgetCents > launch.Invitation.ExpectedCostCents ||
		launch.ApprovalLimits.validate(launch.Invitation) != nil || launch.Context.TimeBudgetSeconds > launch.ApprovalLimits.TimeBudgetSeconds ||
		launch.Context.TurnBudget > launch.ApprovalLimits.TurnBudget || launch.Context.AudioBudgetSeconds > launch.ApprovalLimits.AudioBudgetSeconds ||
		launch.Context.TokenBudget > launch.ApprovalLimits.TokenBudget || launch.Context.CostBudgetCents > launch.ApprovalLimits.CostBudgetCents ||
		launch.Policy.SessionTTL > time.Duration(launch.Context.TimeBudgetSeconds)*time.Second || launch.Policy.AudioBudgetSecond > launch.Context.AudioBudgetSeconds ||
		launch.Policy.MaxFloorLease > time.Duration(launch.ApprovalLimits.MaxFloorLeaseSeconds)*time.Second || launch.Policy.TurnBudget > launch.Context.TurnBudget || launch.Policy.CostBudgetCents > launch.Context.CostBudgetCents {
		return ErrMeetingSpecialistUnauthorized
	}
	return nil
}

func meetingSpecialistMembersOnlyAudience(audience STRIDEAudience) bool {
	memberPrefix := string(ACLPrincipalUser) + ":"
	if len(audience.Principals) == 0 {
		return false
	}
	for _, principal := range audience.Principals {
		if !strings.HasPrefix(strings.TrimSpace(principal), memberPrefix) {
			return false
		}
	}
	return true
}

func sameMeetingSpecialistAudience(left, right STRIDEAudience) bool {
	if left.Visibility != right.Visibility || len(left.Principals) != len(right.Principals) {
		return false
	}
	for index := range left.Principals {
		if left.Principals[index] != right.Principals[index] {
			return false
		}
	}
	return true
}

// MeetingSpecialistProvider is implemented only by a server-side adapter. The
// factory normally closes over its credential; no browser/native API and no
// launch payload carries a secret or provider session URL.
type MeetingSpecialistProvider interface {
	Brief(context.Context, MeetingSpecialistContextEnvelope) error
	WriteHumanPCM(context.Context, uint64, []int16) error
	BeginResponse(context.Context, MeetingAgentFloorLease) error
	CancelResponse(context.Context, uint64, string) error
	Close(context.Context, string) error
}

type MeetingSpecialistProviderFactory func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistProvider, error)

type MeetingSpecialistAudioPublisher func(MeetingAgentFloorScope, uint64, []int16) error

// MeetingSpecialistProviderHooks are bound by the runtime after floor
// admission and before the provider receives its context brief. This keeps
// provider callbacks behind the same floor generation, authorization checks,
// audio publisher, and teardown path as direct runtime calls.
type MeetingSpecialistProviderHooks struct {
	PublishAudio func(MeetingAgentFloorLease, []int16, int64, int64) error
	CompleteTurn func(MeetingAgentFloorLease) error
	FailSession  func(string)
}

type meetingSpecialistProviderHookBinder interface {
	BindMeetingSpecialistProviderHooks(MeetingSpecialistProviderHooks) error
}

type meetingSpecialistProviderReceiptSource interface {
	MeetingSpecialistProviderReceipt() MeetingSpecialistProviderReceipt
}

// MeetingSpecialistTerminalEvidence is the immutable runtime handoff used by
// product persistence after the provider and floor have both been fenced. The
// public terminal reason stays within the floor/product vocabulary, while
// Cause preserves the exact autonomous runtime or provider failure.
type MeetingSpecialistTerminalEvidence struct {
	TerminalReason             string                            `json:"terminalReason"`
	Cause                      string                            `json:"cause"`
	EndedAt                    time.Time                         `json:"endedAt"`
	QualificationSubjectDigest string                            `json:"qualificationSubjectDigest,omitempty"`
	QualificationResult        *StoredTrustedQualificationResult `json:"qualificationResult,omitempty"`
	QualificationLegacyUnbound bool                              `json:"qualificationLegacyUnbound,omitempty"`
	TeardownReceiptDigest      string                            `json:"teardownReceiptDigest,omitempty"`
	ProviderReceipt            MeetingSpecialistProviderReceipt  `json:"providerReceipt"`
}

type meetingSpecialistRuntimeLifetimeKey struct{}

func meetingSpecialistRuntimeOwnsLifetime(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	owned, _ := ctx.Value(meetingSpecialistRuntimeLifetimeKey{}).(bool)
	return owned
}

// meetingSpecialistStartupLifetime keeps provider startup subordinate to the
// caller until the runtime has completed every authorization and briefing
// check. The runtime context never inherits caller values, deadlines, or
// cancellation; a narrow bridge forwards cancellation only until handoff,
// after which product/session authority solely owns the accepted lifetime.
type meetingSpecialistStartupLifetime struct {
	mu        sync.Mutex
	handedOff bool
	cancel    context.CancelFunc
	stop      chan struct{}
	stopOnce  sync.Once
}

func newMeetingSpecialistStartupLifetime(parent context.Context) (context.Context, *meetingSpecialistStartupLifetime) {
	// The durable runtime inherits no request values, deadlines, or cancellation.
	// The bridge below forwards cancellation only until successful handoff.
	ownedParent := context.WithValue(context.Background(), meetingSpecialistRuntimeLifetimeKey{}, true)
	ctx, cancel := context.WithCancel(ownedParent)
	lifetime := &meetingSpecialistStartupLifetime{cancel: cancel, stop: make(chan struct{})}
	if parent.Done() != nil {
		go func() {
			select {
			case <-parent.Done():
				lifetime.cancelBeforeHandoff()
			case <-lifetime.stop:
			}
		}()
	}
	return ctx, lifetime
}

func (lifetime *meetingSpecialistStartupLifetime) cancelBeforeHandoff() {
	if lifetime == nil {
		return
	}
	lifetime.mu.Lock()
	if !lifetime.handedOff {
		lifetime.cancel()
	}
	lifetime.mu.Unlock()
}

func (lifetime *meetingSpecialistStartupLifetime) handoff(parent context.Context) error {
	if lifetime == nil {
		return context.Canceled
	}
	lifetime.mu.Lock()
	defer lifetime.mu.Unlock()
	if err := parent.Err(); err != nil {
		lifetime.cancel()
		return err
	}
	lifetime.handedOff = true
	return nil
}

func (lifetime *meetingSpecialistStartupLifetime) stopForwarding() {
	if lifetime != nil {
		lifetime.stopOnce.Do(func() { close(lifetime.stop) })
	}
}

// MeetingSpecialistCapabilityRequest contains only immutable, server-derived
// launch facts. The authority resolves current consent, invitation,
// membership and room-generation state; none of those facts are trusted from
// client booleans.
type MeetingSpecialistCapabilityRequest struct {
	RoomID                string
	SittingID             string
	MediaGeneration       uint64
	InvitationID          string
	InvitationRevision    int64
	InvitationDigest      string
	ConsentPolicyRevision STRIDEReference
	Requester             string
	Confirmer             string
	RuntimePrincipal      string
	AgentProfile          STRIDEReference
	Eligibility           STRIDEReference
	ContextDigest         string
	ApprovalLimits        MeetingSpecialistApprovalLimits
	ToolIDs               []string
	ExpiresAt             time.Time
	Receipt               string
}

type MeetingSpecialistAuthorization struct {
	ID            string
	BindingDigest string
	ExpiresAt     time.Time
}

type MeetingSpecialistCapabilityAuthority interface {
	ConsumeLaunch(context.Context, MeetingSpecialistCapabilityRequest) (MeetingSpecialistAuthorization, error)
	Current(context.Context, MeetingSpecialistAuthorization, MeetingSpecialistCapabilityRequest) error
}

type MeetingSpecialistRuntime struct {
	mu        sync.Mutex
	cond      *sync.Cond
	now       func() time.Time
	gates     MeetingSpecialistGates
	floor     *MeetingAgentFloorController
	factory   MeetingSpecialistProviderFactory
	publish   MeetingSpecialistAudioPublisher
	authority MeetingSpecialistCapabilityAuthority

	ctx             context.Context
	cancel          context.CancelFunc
	launch          MeetingSpecialistLaunch
	lease           MeetingAgentSessionLease
	provider        MeetingSpecialistProvider
	closed          bool
	teardownReceipt string
	teardownErr     error
	authorization   MeetingSpecialistAuthorization
	providerReceipt MeetingSpecialistProviderReceipt
	// qualificationSubjectDigest is set only by the concrete production
	// joiner. When present, Start accepts only the sealed provider produced by
	// the factory that captured this exact signed subject.
	qualificationSubjectDigest string
	qualificationResult        *StoredTrustedQualificationResult
	qualificationCurrent       func() error
	epoch                      uint64
	inflight                   int
	starting                   bool
	stopping                   bool
	shutdownTimeout            time.Duration
	hardExpiresAt              time.Time
	hardExpiryCause            string
	terminalObserver           func(MeetingSpecialistTerminalEvidence)
	terminalEvidence           MeetingSpecialistTerminalEvidence
	terminalOnce               sync.Once
}

func NewMeetingSpecialistRuntime(now func() time.Time, gates MeetingSpecialistGates, authority MeetingSpecialistCapabilityAuthority, factory MeetingSpecialistProviderFactory, publish MeetingSpecialistAudioPublisher) *MeetingSpecialistRuntime {
	if now == nil {
		now = time.Now
	}
	runtime := &MeetingSpecialistRuntime{now: now, gates: gates, floor: NewMeetingAgentFloorController(now), authority: authority, factory: factory, publish: publish, shutdownTimeout: meetingSpecialistRuntimeShutdownTimeout}
	runtime.cond = sync.NewCond(&runtime.mu)
	return runtime
}

func newQualifiedMeetingSpecialistRuntime(now func() time.Time, gates MeetingSpecialistGates, authority MeetingSpecialistCapabilityAuthority, factory *MeetingSpecialistQualifiedProviderFactory, qualification StoredTrustedQualificationResult, qualificationCurrent func() error, publish MeetingSpecialistAudioPublisher) *MeetingSpecialistRuntime {
	if factory == nil {
		return NewMeetingSpecialistRuntime(now, gates, authority, nil, publish)
	}
	runtime := NewMeetingSpecialistRuntime(now, gates, authority, factory.provider, publish)
	runtime.qualificationSubjectDigest = factory.subjectDigest
	runtime.qualificationResult = cloneMeetingSpecialistQualificationResult(&qualification)
	runtime.qualificationCurrent = qualificationCurrent
	return runtime
}

func (runtime *MeetingSpecialistRuntime) BindTerminalObserver(observer func(MeetingSpecialistTerminalEvidence)) bool {
	if runtime == nil || observer == nil {
		return false
	}
	runtime.mu.Lock()
	if runtime.terminalObserver != nil {
		runtime.mu.Unlock()
		return false
	}
	runtime.terminalObserver = observer
	evidence := runtime.terminalEvidence
	runtime.mu.Unlock()
	if evidence.TerminalReason != "" {
		go runtime.dispatchTerminal(evidence)
	}
	return true
}

func (runtime *MeetingSpecialistRuntime) dispatchTerminal(evidence MeetingSpecialistTerminalEvidence) {
	if runtime == nil || evidence.TerminalReason == "" || evidence.Cause == "" || evidence.EndedAt.IsZero() {
		return
	}
	runtime.mu.Lock()
	if runtime.terminalEvidence.TerminalReason == "" {
		runtime.terminalEvidence = evidence
	}
	observer := runtime.terminalObserver
	evidence = runtime.terminalEvidence
	runtime.mu.Unlock()
	if observer != nil {
		runtime.terminalOnce.Do(func() { observer(evidence) })
	}
}

func meetingSpecialistHardLifetime(now time.Time, launch MeetingSpecialistLaunch, authorization MeetingSpecialistAuthorization) (time.Time, string) {
	type candidate struct {
		at    time.Time
		cause string
	}
	candidates := []candidate{
		{at: now.Add(launch.Policy.SessionTTL), cause: "session_ttl_expired"},
		{at: now.Add(time.Duration(launch.Context.TimeBudgetSeconds) * time.Second), cause: "context_time_budget_expired"},
		{at: launch.Invitation.ExpiresAt.UTC(), cause: "invitation_expired"},
		{at: authorization.ExpiresAt.UTC(), cause: "authorization_expired"},
	}
	shortest := candidates[0]
	for _, value := range candidates[1:] {
		if value.at.Before(shortest.at) {
			shortest = value
		}
	}
	return shortest.at, shortest.cause
}

func (runtime *MeetingSpecialistRuntime) watchLifetime(ctx context.Context, lease MeetingAgentSessionLease, duration time.Duration, expiryCause string) {
	if runtime == nil || ctx == nil || duration < 0 || expiryCause == "" {
		return
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		_ = runtime.stopWithCause(lease, "expired", expiryCause)
	case <-ctx.Done():
		runtime.mu.Lock()
		current := runtime.ctx == ctx && sameMeetingAgentSessionLease(runtime.lease, lease) && !runtime.closed
		runtime.mu.Unlock()
		if !current {
			return
		}
		terminalReason, cause := "failed", "parent_context_cancelled"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			terminalReason, cause = "expired", "parent_context_deadline"
		}
		_ = runtime.stopWithCause(lease, terminalReason, cause)
	}
}

func meetingSpecialistProviderTerminal(reason string) (string, string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "provider_failure_unspecified"
	}
	if reason == "session_deadline" {
		return "expired", reason
	}
	return "failed", reason
}

// meetingSpecialistAdmission is a short-lived, generation-bound permit. It is
// acquired under runtime.mu, but provider/authority/publisher callbacks run
// without that mutex. Revoke and Stop close the generation and wait for all
// earlier permits to drain before returning.
type meetingSpecialistAdmission struct {
	runtime       *MeetingSpecialistRuntime
	epoch         uint64
	provider      MeetingSpecialistProvider
	ctx           context.Context
	launch        MeetingSpecialistLaunch
	authorization MeetingSpecialistAuthorization
	authority     MeetingSpecialistCapabilityAuthority
	publish       MeetingSpecialistAudioPublisher
	start         bool
	released      bool
}

func (runtime *MeetingSpecialistRuntime) beginStartAdmission() (*meetingSpecialistAdmission, error) {
	if runtime == nil {
		return nil, ErrMeetingSpecialistDisabled
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if !runtime.gates.launchEnabled() || runtime.authority == nil {
		return nil, ErrMeetingSpecialistDisabled
	}
	if runtime.starting || runtime.provider != nil || (runtime.ctx != nil && !runtime.closed) {
		return nil, ErrMeetingAgentFloorOccupied
	}
	if runtime.closed {
		return nil, ErrMeetingSpecialistClosed
	}
	runtime.starting = true
	runtime.inflight++
	return &meetingSpecialistAdmission{runtime: runtime, epoch: runtime.epoch, authority: runtime.authority, start: true}, nil
}

func (runtime *MeetingSpecialistRuntime) beginAdmission(session MeetingAgentSessionLease) (*meetingSpecialistAdmission, error) {
	if runtime == nil {
		return nil, ErrMeetingSpecialistClosed
	}
	runtime.mu.Lock()
	if runtime.closed || runtime.provider == nil || runtime.ctx == nil {
		runtime.mu.Unlock()
		return nil, ErrMeetingSpecialistClosed
	}
	if !sameMeetingAgentSessionLease(runtime.lease, session) {
		runtime.mu.Unlock()
		return nil, ErrMeetingSpecialistFence
	}
	now := runtime.now().UTC()
	if !runtime.hardExpiresAt.IsZero() && !now.Before(runtime.hardExpiresAt) {
		cause := runtime.hardExpiryCause
		runtime.mu.Unlock()
		_ = runtime.stopWithCause(session, "expired", cause)
		return nil, ErrMeetingSpecialistUnauthorized
	}
	if !runtime.gates.launchEnabled() {
		runtime.mu.Unlock()
		return nil, ErrMeetingSpecialistDisabled
	}
	runtime.inflight++
	admission := &meetingSpecialistAdmission{runtime: runtime, epoch: runtime.epoch, provider: runtime.provider, ctx: runtime.ctx, launch: runtime.launch, authorization: runtime.authorization, authority: runtime.authority, publish: runtime.publish}
	runtime.mu.Unlock()
	return admission, nil
}

func (admission *meetingSpecialistAdmission) release() {
	if admission == nil || admission.runtime == nil || admission.released {
		return
	}
	runtime := admission.runtime
	runtime.mu.Lock()
	if !admission.released {
		admission.released = true
		if admission.start {
			runtime.starting = false
		}
		runtime.inflight--
		if runtime.inflight == 0 {
			runtime.cond.Broadcast()
		}
	}
	runtime.mu.Unlock()
}

func (admission *meetingSpecialistAdmission) current(requireProvider bool) bool {
	if admission == nil || admission.runtime == nil {
		return false
	}
	runtime := admission.runtime
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if admission.released || runtime.epoch != admission.epoch || runtime.closed || !runtime.gates.launchEnabled() {
		return false
	}
	return !requireProvider || (runtime.provider == admission.provider && runtime.ctx == admission.ctx)
}

func (admission *meetingSpecialistAdmission) authorizeCurrent() error {
	if admission == nil || admission.authority == nil || admission.ctx == nil {
		return ErrMeetingSpecialistUnauthorized
	}
	request := meetingSpecialistCapabilityRequest(admission.launch)
	request.Receipt = ""
	now := admission.runtime.now().UTC()
	if !admission.authorization.ExpiresAt.After(now) || !request.ExpiresAt.After(now) || admission.authority.Current(admission.ctx, admission.authorization, request) != nil || !admission.current(true) {
		return ErrMeetingSpecialistUnauthorized
	}
	return nil
}

func (runtime *MeetingSpecialistRuntime) Start(parent context.Context, launch MeetingSpecialistLaunch) (MeetingAgentSessionLease, error) {
	if runtime == nil {
		return MeetingAgentSessionLease{}, ErrMeetingSpecialistDisabled
	}
	if parent == nil {
		parent = context.Background()
	}
	admission, err := runtime.beginStartAdmission()
	if err != nil {
		return MeetingAgentSessionLease{}, err
	}
	defer admission.release()
	if err := launch.Validate(runtime.now().UTC()); err != nil {
		return MeetingAgentSessionLease{}, err
	}
	request := meetingSpecialistCapabilityRequest(launch)
	if strings.TrimSpace(request.Receipt) == "" {
		return MeetingAgentSessionLease{}, ErrMeetingSpecialistUnauthorized
	}
	authorization, err := admission.authority.ConsumeLaunch(parent, request)
	if err != nil || !strideIdentifier(authorization.ID) || !isHexDigest(authorization.BindingDigest) || !authorization.ExpiresAt.After(runtime.now().UTC()) {
		return MeetingAgentSessionLease{}, ErrMeetingSpecialistUnauthorized
	}
	request.Receipt = ""
	if authorization.BindingDigest != workDigest(request) || admission.authority.Current(parent, authorization, request) != nil || !admission.current(false) {
		return MeetingAgentSessionLease{}, ErrMeetingSpecialistUnauthorized
	}
	launch.CapabilityReceipt = ""
	lifetimeNow := runtime.now().UTC()
	hardExpiresAt, hardExpiryCause := meetingSpecialistHardLifetime(lifetimeNow, launch, authorization)
	if runtime.qualificationResult != nil {
		evaluatedAt, qualificationErr := time.Parse(time.RFC3339Nano, runtime.qualificationResult.Record.EvaluatedAt)
		qualificationExpiresAt := evaluatedAt.UTC().Add(meetingSpecialistQualificationMaxAge)
		if qualificationErr != nil || !qualificationExpiresAt.After(lifetimeNow) {
			return MeetingAgentSessionLease{}, ErrMeetingSpecialistUnauthorized
		}
		if qualificationExpiresAt.Before(hardExpiresAt) {
			hardExpiresAt, hardExpiryCause = qualificationExpiresAt, "qualification_expired"
		}
	}
	if !hardExpiresAt.After(lifetimeNow) {
		return MeetingAgentSessionLease{}, ErrMeetingSpecialistUnauthorized
	}
	runtime.mu.Lock()
	if runtime.epoch != admission.epoch || runtime.closed || !runtime.gates.launchEnabled() || runtime.provider != nil || runtime.ctx != nil {
		runtime.mu.Unlock()
		return MeetingAgentSessionLease{}, ErrMeetingSpecialistFence
	}
	ctx, startupLifetime := newMeetingSpecialistStartupLifetime(parent)
	defer startupLifetime.stopForwarding()
	cancel := startupLifetime.cancel
	runtime.ctx, runtime.cancel, runtime.launch, runtime.authorization, runtime.closed = ctx, cancel, launch, authorization, false
	runtime.hardExpiresAt, runtime.hardExpiryCause = hardExpiresAt, hardExpiryCause
	runtime.mu.Unlock()

	lease, err := runtime.floor.Admit(launch.Scope, launch.Policy)
	if err != nil {
		abortErr := runtime.abortStart(ctx, cancel, MeetingAgentSessionLease{}, nil, "floor_admission_failed")
		return MeetingAgentSessionLease{}, errors.Join(err, abortErr)
	}
	runtime.mu.Lock()
	if runtime.ctx != ctx || runtime.closed || runtime.epoch != admission.epoch {
		runtime.mu.Unlock()
		receipt, terminateErr := runtime.floor.Terminate(lease, "kill_switch")
		runtime.mu.Lock()
		runtime.teardownReceipt, runtime.teardownErr = receipt, terminateErr
		runtime.mu.Unlock()
		cancel()
		return MeetingAgentSessionLease{}, errors.Join(ErrMeetingSpecialistFence, terminateErr)
	}
	// Publish the admitted lease before potentially blocking provider work so a
	// concurrent revocation can always terminate the floor and mint its receipt.
	runtime.lease = lease
	runtime.mu.Unlock()
	go runtime.watchLifetime(ctx, lease, hardExpiresAt.Sub(lifetimeNow), hardExpiryCause)
	if runtime.factory == nil {
		abortErr := runtime.abortStart(ctx, cancel, lease, nil, "provider_factory_unavailable")
		return MeetingAgentSessionLease{}, errors.Join(ErrMeetingSpecialistDisabled, abortErr)
	}
	provider, err := runtime.factory(ctx, launch)
	if parent.Err() != nil {
		startupLifetime.cancelBeforeHandoff()
	}
	if contextErr := ctx.Err(); contextErr != nil {
		abortErr := runtime.abortStart(ctx, cancel, lease, provider, "startup_request_cancelled_after_factory")
		return MeetingAgentSessionLease{}, errors.Join(contextErr, abortErr)
	}
	if err != nil || provider == nil {
		abortErr := runtime.abortStart(ctx, cancel, lease, provider, "provider_factory_failed")
		if err != nil {
			return MeetingAgentSessionLease{}, errors.Join(err, abortErr)
		}
		return MeetingAgentSessionLease{}, errors.Join(ErrMeetingSpecialistDisabled, abortErr)
	}
	if runtime.qualificationSubjectDigest != "" && validateQualifiedMeetingSpecialistProvider(provider, runtime.qualificationSubjectDigest) != nil {
		abortErr := runtime.abortStart(ctx, cancel, lease, provider, "provider_qualification_subject_mismatch")
		return MeetingAgentSessionLease{}, errors.Join(ErrMeetingSpecialistUnauthorized, abortErr)
	}
	if runtime.qualificationCurrent != nil && runtime.qualificationCurrent() != nil {
		abortErr := runtime.abortStart(ctx, cancel, lease, provider, "provider_qualification_expired_after_factory")
		return MeetingAgentSessionLease{}, errors.Join(ErrMeetingSpecialistUnauthorized, abortErr)
	}
	if admission.authority.Current(ctx, authorization, request) != nil {
		abortErr := runtime.abortStart(ctx, cancel, lease, provider, "authority_changed_after_factory")
		return MeetingAgentSessionLease{}, errors.Join(ErrMeetingSpecialistUnauthorized, abortErr)
	}
	if !admission.current(false) || !runtime.launchStateCurrent(ctx) {
		abortErr := runtime.abortStart(ctx, cancel, lease, provider, "launch_fenced_after_factory")
		return MeetingAgentSessionLease{}, errors.Join(ErrMeetingSpecialistFence, abortErr)
	}
	if binder, ok := provider.(meetingSpecialistProviderHookBinder); ok {
		hooks := MeetingSpecialistProviderHooks{
			PublishAudio: runtime.PublishProviderAudio,
			CompleteTurn: runtime.CompleteTurn,
			FailSession: func(reason string) {
				terminalReason, cause := meetingSpecialistProviderTerminal(reason)
				_ = runtime.stopWithCause(lease, terminalReason, cause)
			},
		}
		if err := binder.BindMeetingSpecialistProviderHooks(hooks); err != nil {
			abortErr := runtime.abortStart(ctx, cancel, lease, provider, "provider_hook_binding_failed")
			return MeetingAgentSessionLease{}, errors.Join(err, abortErr)
		}
	}
	if parent.Err() != nil {
		startupLifetime.cancelBeforeHandoff()
	}
	if contextErr := ctx.Err(); contextErr != nil {
		abortErr := runtime.abortStart(ctx, cancel, lease, provider, "startup_request_cancelled_before_brief")
		return MeetingAgentSessionLease{}, errors.Join(contextErr, abortErr)
	}
	if err := provider.Brief(ctx, launch.Context); err != nil {
		abortErr := runtime.abortStart(ctx, cancel, lease, provider, "brief_failed")
		return MeetingAgentSessionLease{}, errors.Join(err, abortErr)
	}
	if runtime.qualificationCurrent != nil && runtime.qualificationCurrent() != nil {
		abortErr := runtime.abortStart(ctx, cancel, lease, provider, "provider_qualification_expired_after_brief")
		return MeetingAgentSessionLease{}, errors.Join(ErrMeetingSpecialistUnauthorized, abortErr)
	}
	if admission.authority.Current(ctx, authorization, request) != nil {
		abortErr := runtime.abortStart(ctx, cancel, lease, provider, "authority_changed_after_brief")
		return MeetingAgentSessionLease{}, errors.Join(ErrMeetingSpecialistUnauthorized, abortErr)
	}
	if !admission.current(false) || !runtime.launchStateCurrent(ctx) {
		abortErr := runtime.abortStart(ctx, cancel, lease, provider, "launch_fenced_after_brief")
		return MeetingAgentSessionLease{}, errors.Join(ErrMeetingSpecialistFence, abortErr)
	}
	if err := startupLifetime.handoff(parent); err != nil {
		abortErr := runtime.abortStart(ctx, cancel, lease, provider, "startup_request_cancelled")
		return MeetingAgentSessionLease{}, errors.Join(err, abortErr)
	}
	runtime.mu.Lock()
	if runtime.ctx != ctx || runtime.closed {
		runtime.mu.Unlock()
		providerErr := runtime.closeProviderBounded(provider, ctx, "launch_fenced")
		receipt, terminateErr := runtime.floor.Terminate(lease, "failed")
		runtime.mu.Lock()
		runtime.teardownReceipt, runtime.teardownErr = receipt, terminateErr
		runtime.mu.Unlock()
		cancel()
		return MeetingAgentSessionLease{}, errors.Join(ErrMeetingSpecialistFence, terminateErr, providerErr)
	}
	runtime.lease, runtime.provider = lease, provider
	if source, ok := provider.(meetingSpecialistProviderReceiptSource); ok {
		runtime.providerReceipt = source.MeetingSpecialistProviderReceipt()
	}
	runtime.starting = false
	runtime.mu.Unlock()
	return lease, nil
}

func (runtime *MeetingSpecialistRuntime) launchStateCurrent(ctx context.Context) bool {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.ctx == ctx && !runtime.closed && runtime.gates.launchEnabled()
}

func (runtime *MeetingSpecialistRuntime) abortStart(ctx context.Context, cancel context.CancelFunc, lease MeetingAgentSessionLease, provider MeetingSpecialistProvider, reason string) error {
	if runtime == nil {
		return nil
	}
	receipt := ""
	var terminateErr error
	if lease.Generation > 0 {
		receipt, terminateErr = runtime.floor.Terminate(lease, "failed")
	}
	providerErr := runtime.closeProviderBounded(provider, ctx, reason)
	if cancel != nil {
		cancel()
	}
	runtime.mu.Lock()
	if runtime.ctx == ctx {
		wasClosed := runtime.closed
		runtime.closed = true
		runtime.starting = false
		runtime.provider = nil
		runtime.lease = lease
		if receipt != "" || runtime.teardownReceipt == "" && !wasClosed {
			runtime.teardownReceipt = receipt
		}
		if !wasClosed {
			runtime.teardownErr = errors.Join(terminateErr, providerErr)
		}
	}
	runtime.mu.Unlock()
	return errors.Join(terminateErr, providerErr)
}

func (runtime *MeetingSpecialistRuntime) SendHumanAudio(session MeetingAgentSessionLease, sourceTrackID string, pcm []int16) error {
	if runtime == nil || len(pcm) == 0 {
		return ErrMeetingSpecialistUnauthorized
	}
	admission, err := runtime.beginAdmission(session)
	if err != nil {
		return err
	}
	defer admission.release()
	if err = admission.authorizeCurrent(); err != nil {
		return err
	}
	highWater, err := runtime.floor.AcceptHumanInput(session, sourceTrackID, "human")
	if err != nil {
		return err
	}
	if !admission.current(true) {
		return ErrMeetingSpecialistClosed
	}
	if err := admission.provider.WriteHumanPCM(admission.ctx, highWater, append([]int16(nil), pcm...)); err != nil {
		admission.release()
		_ = runtime.Stop(session, "failed")
		return err
	}
	return nil
}

func (runtime *MeetingSpecialistRuntime) RequestTurn(session MeetingAgentSessionLease, trigger string, duration time.Duration) (MeetingAgentFloorLease, error) {
	admission, err := runtime.beginAdmission(session)
	if err != nil {
		return MeetingAgentFloorLease{}, err
	}
	defer admission.release()
	if err = admission.authorizeCurrent(); err != nil {
		return MeetingAgentFloorLease{}, err
	}
	floor, err := runtime.floor.RequestFloor(session, trigger, duration)
	if err != nil {
		return MeetingAgentFloorLease{}, err
	}
	if !admission.current(true) {
		_ = runtime.floor.ReleaseFloor(floor)
		return MeetingAgentFloorLease{}, ErrMeetingSpecialistClosed
	}
	if err := admission.provider.BeginResponse(admission.ctx, floor); err != nil {
		_ = runtime.floor.ReleaseFloor(floor)
		// A provider may have committed input or created a response before a
		// later WebSocket write failed. The floor alone cannot be rolled back
		// safely, so mirror the human-input path and terminally fence the whole
		// session before another turn can be admitted.
		admission.release()
		stopErr := runtime.Stop(session, "failed")
		return MeetingAgentFloorLease{}, errors.Join(err, stopErr)
	}
	return floor, nil
}

// PublishProviderAudio is the only specialist-audio publication seam. The
// provider generation, output track, audio/cost budgets and current floor are
// checked before the independently-owned human media plane sees anything.
func (runtime *MeetingSpecialistRuntime) PublishProviderAudio(floor MeetingAgentFloorLease, pcm []int16, audioSeconds, costCents int64) error {
	if runtime == nil || len(pcm) == 0 {
		return ErrMeetingSpecialistUnauthorized
	}
	admission, err := runtime.beginAdmission(floor.Session)
	if err != nil {
		return err
	}
	defer admission.release()
	if err = admission.authorizeCurrent(); err != nil {
		return err
	}
	if _, err := runtime.floor.AcceptProviderOutput(floor, floor.Session.Scope.AudioTrackID, audioSeconds, costCents); err != nil {
		return err
	}
	if !admission.current(true) || admission.publish == nil {
		return ErrMeetingSpecialistDisabled
	}
	return admission.publish(floor.Session.Scope, floor.Generation, append([]int16(nil), pcm...))
}

func (runtime *MeetingSpecialistRuntime) CompleteTurn(floor MeetingAgentFloorLease) error {
	if runtime == nil {
		return ErrMeetingSpecialistClosed
	}
	admission, err := runtime.beginAdmission(floor.Session)
	if err != nil {
		return err
	}
	defer admission.release()
	return runtime.floor.ReleaseFloor(floor)
}

func (runtime *MeetingSpecialistRuntime) HumanBargeIn(roomID, sittingID string, mediaGeneration uint64) (MeetingAgentFloorInterruption, bool) {
	if runtime == nil {
		return MeetingAgentFloorInterruption{}, false
	}
	runtime.mu.Lock()
	session := runtime.lease
	runtime.mu.Unlock()
	admission, err := runtime.beginAdmission(session)
	if err != nil {
		return MeetingAgentFloorInterruption{}, false
	}
	defer admission.release()
	interruption, ok := runtime.floor.HumanBargeIn(roomID, sittingID, mediaGeneration)
	if !ok {
		return MeetingAgentFloorInterruption{}, false
	}
	if admission.current(true) {
		_ = admission.provider.CancelResponse(admission.ctx, interruption.FloorGeneration, interruption.Reason)
	}
	return interruption, true
}

func (runtime *MeetingSpecialistRuntime) AuthorizeTool(session MeetingAgentSessionLease, toolID string) error {
	admission, err := runtime.beginAdmission(session)
	if err != nil {
		return err
	}
	defer admission.release()
	if err = admission.authorizeCurrent(); err != nil {
		return err
	}
	runtime.mu.Lock()
	allowed := runtime.epoch == admission.epoch && !runtime.closed && runtime.gates.Tools && containsMeetingSpecialistTool(admission.launch.Context.ToolIDs, toolID)
	runtime.mu.Unlock()
	if !allowed {
		return ErrMeetingSpecialistToolDenied
	}
	return nil
}

func (runtime *MeetingSpecialistRuntime) Stop(session MeetingAgentSessionLease, reason string) error {
	return runtime.stopWithCause(session, reason, reason)
}

func (runtime *MeetingSpecialistRuntime) stopWithCause(session MeetingAgentSessionLease, terminalReason, cause string) error {
	if runtime == nil {
		return ErrMeetingSpecialistClosed
	}
	terminalReason = strings.TrimSpace(terminalReason)
	cause = strings.TrimSpace(cause)
	if !allowedMeetingAgentTerminalReason(terminalReason) || cause == "" {
		return ErrMeetingAgentFloorInvalid
	}
	runtime.mu.Lock()
	if runtime.closed {
		for runtime.stopping {
			runtime.cond.Wait()
		}
		receipt, teardownErr := runtime.teardownReceipt, runtime.teardownErr
		runtime.mu.Unlock()
		if receipt != "" {
			return teardownErr
		}
		return ErrMeetingSpecialistClosed
	}
	if !sameMeetingAgentSessionLease(runtime.lease, session) {
		runtime.mu.Unlock()
		return ErrMeetingSpecialistFence
	}
	runtime.closed = true
	runtime.stopping = true
	runtime.epoch++
	provider, ctx, cancel := runtime.provider, runtime.ctx, runtime.cancel
	runtime.provider = nil
	runtime.mu.Unlock()
	// Fence and receipt the floor immediately. Provider/socket teardown runs
	// outside runtime and product mutexes, before a bounded in-flight drain, so
	// a wedged write, publisher, factory, or authority cannot block revocation.
	receipt, err := runtime.floor.Terminate(session, terminalReason)
	if terminalReason == "expired" && receipt == "" {
		snapshot := runtime.floor.Snapshot()
		if snapshot.Session == nil && snapshot.TeardownReceiptDigest != "" && snapshot.TerminalReason == session.Scope.SessionID+"\x00expired" {
			receipt = snapshot.TeardownReceiptDigest
			if errors.Is(err, ErrMeetingAgentFloorExpired) || errors.Is(err, ErrMeetingAgentFloorTerminated) {
				err = nil
			}
		}
	}
	providerErr := runtime.closeProviderBounded(provider, ctx, cause)
	// Provider Close owns the closed-before-context-cancel ordering. Cancelling
	// first lets a provider deadline watcher misclassify an intentional stop as
	// a timeout and overwrite an authoritative terminal receipt. The bounded
	// close path still interrupts the socket immediately; this cancellation is
	// the generic-provider fallback and is idempotent.
	if cancel != nil {
		cancel()
	}
	drainErr := runtime.waitInflightBounded()
	providerReceipt := MeetingSpecialistProviderReceipt{}
	if source, ok := provider.(meetingSpecialistProviderReceiptSource); ok {
		providerReceipt = source.MeetingSpecialistProviderReceipt()
	}
	teardownErr := errors.Join(err, providerErr, drainErr)
	runtime.mu.Lock()
	if providerReceipt != (MeetingSpecialistProviderReceipt{}) {
		runtime.providerReceipt = providerReceipt
	}
	if receipt != "" || runtime.teardownReceipt == "" {
		runtime.teardownReceipt = receipt
	}
	runtime.stopping = false
	runtime.teardownErr = teardownErr
	runtime.cond.Broadcast()
	evidence := MeetingSpecialistTerminalEvidence{
		TerminalReason: terminalReason, Cause: cause, EndedAt: runtime.now().UTC(),
		QualificationSubjectDigest: runtime.qualificationSubjectDigest, QualificationResult: cloneMeetingSpecialistQualificationResult(runtime.qualificationResult), TeardownReceiptDigest: runtime.teardownReceipt, ProviderReceipt: runtime.providerReceipt,
	}
	runtime.mu.Unlock()
	runtime.dispatchTerminal(evidence)
	return teardownErr
}

func (runtime *MeetingSpecialistRuntime) RevokeGates(reason string) error {
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	runtime.gates = MeetingSpecialistGates{}
	if runtime.closed {
		for runtime.stopping {
			runtime.cond.Wait()
		}
		err := runtime.teardownErr
		runtime.mu.Unlock()
		return err
	}
	runtime.closed = true
	runtime.stopping = true
	runtime.epoch++
	lease, provider, ctx, cancel := runtime.lease, runtime.provider, runtime.ctx, runtime.cancel
	runtime.provider = nil
	runtime.mu.Unlock()
	receipt := ""
	var terminateErr error
	if lease.Generation > 0 {
		receipt, terminateErr = runtime.floor.Terminate(lease, reason)
	}
	if reason == "expired" && receipt == "" && lease.Generation > 0 {
		snapshot := runtime.floor.Snapshot()
		if snapshot.Session == nil && snapshot.TeardownReceiptDigest != "" && snapshot.TerminalReason == lease.Scope.SessionID+"\x00expired" {
			receipt = snapshot.TeardownReceiptDigest
			if errors.Is(terminateErr, ErrMeetingAgentFloorExpired) || errors.Is(terminateErr, ErrMeetingAgentFloorTerminated) {
				terminateErr = nil
			}
		}
	}
	providerErr := runtime.closeProviderBounded(provider, ctx, reason)
	if cancel != nil {
		cancel()
	}
	drainErr := runtime.waitInflightBounded()
	providerReceipt := MeetingSpecialistProviderReceipt{}
	if source, ok := provider.(meetingSpecialistProviderReceiptSource); ok {
		providerReceipt = source.MeetingSpecialistProviderReceipt()
	}
	teardownErr := errors.Join(terminateErr, providerErr, drainErr)
	runtime.mu.Lock()
	if providerReceipt != (MeetingSpecialistProviderReceipt{}) {
		runtime.providerReceipt = providerReceipt
	}
	if receipt != "" || runtime.teardownReceipt == "" {
		runtime.teardownReceipt = receipt
	}
	runtime.stopping = false
	runtime.teardownErr = teardownErr
	runtime.cond.Broadcast()
	evidence := MeetingSpecialistTerminalEvidence{
		TerminalReason: reason, Cause: reason, EndedAt: runtime.now().UTC(),
		QualificationSubjectDigest: runtime.qualificationSubjectDigest, QualificationResult: cloneMeetingSpecialistQualificationResult(runtime.qualificationResult), TeardownReceiptDigest: runtime.teardownReceipt, ProviderReceipt: runtime.providerReceipt,
	}
	runtime.mu.Unlock()
	runtime.dispatchTerminal(evidence)
	return teardownErr
}

// FenceGates is the non-blocking authority half of RevokeGates. Product owns
// its ledger mutex while it detaches a runtime; clearing gates and advancing
// the epoch here makes that revocation effective before any potentially slow
// persistence or provider teardown. RevokeGates still performs and joins the
// bounded socket/floor cleanup after the product mutex is released.
func (runtime *MeetingSpecialistRuntime) FenceGates() {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	if !runtime.closed {
		runtime.gates = MeetingSpecialistGates{}
		runtime.epoch++
	}
	runtime.mu.Unlock()
}

func (runtime *MeetingSpecialistRuntime) waitInflightBounded() error {
	if runtime == nil {
		return nil
	}
	timeout := runtime.shutdownTimeout
	if timeout <= 0 {
		timeout = meetingSpecialistRuntimeShutdownTimeout
	}
	deadline := time.Now().Add(timeout)
	for {
		runtime.mu.Lock()
		inflight := runtime.inflight
		runtime.mu.Unlock()
		if inflight == 0 {
			return nil
		}
		if !time.Now().Before(deadline) {
			return ErrMeetingSpecialistDrainTimeout
		}
		time.Sleep(time.Millisecond)
	}
}

func (runtime *MeetingSpecialistRuntime) closeProviderBounded(provider MeetingSpecialistProvider, ctx context.Context, reason string) error {
	if runtime == nil || provider == nil {
		return nil
	}
	timeout := runtime.shutdownTimeout
	if timeout <= 0 {
		timeout = meetingSpecialistRuntimeShutdownTimeout
	}
	done := make(chan error, 2)
	go func() {
		done <- provider.CancelResponse(ctx, 0, reason)
	}()
	go func() {
		done <- provider.Close(ctx, reason)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var result error
	for completed := 0; completed < 2; completed++ {
		select {
		case err := <-done:
			result = errors.Join(result, err)
		case <-timer.C:
			return errors.Join(result, ErrMeetingSpecialistDrainTimeout)
		}
	}
	return result
}

func (runtime *MeetingSpecialistRuntime) Snapshot() MeetingAgentFloorSnapshot {
	if runtime == nil {
		return MeetingAgentFloorSnapshot{}
	}
	return runtime.floor.Snapshot()
}

// ProviderReceipt returns only immutable hashes, model/profile controls, and
// reconciled aggregate usage. It never returns a credential, provider session
// identifier, context body, transcript, or audio.
func (runtime *MeetingSpecialistRuntime) ProviderReceipt() MeetingSpecialistProviderReceipt {
	if runtime == nil {
		return MeetingSpecialistProviderReceipt{}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if source, ok := runtime.provider.(meetingSpecialistProviderReceiptSource); ok {
		return source.MeetingSpecialistProviderReceipt()
	}
	return runtime.providerReceipt
}

func meetingSpecialistCapabilityRequest(launch MeetingSpecialistLaunch) MeetingSpecialistCapabilityRequest {
	eligibility := STRIDEReference{}
	if launch.Invitation.Eligibility != nil {
		eligibility = *launch.Invitation.Eligibility
	}
	return MeetingSpecialistCapabilityRequest{
		RoomID: launch.Scope.RoomID, SittingID: launch.Scope.SittingID, MediaGeneration: launch.Scope.MediaGeneration,
		InvitationID: launch.Invitation.Header.ID, InvitationRevision: launch.Invitation.Header.Revision, InvitationDigest: launch.Invitation.Header.ContentDigest,
		ConsentPolicyRevision: launch.Invitation.ConsentPolicyRevision, Requester: launch.Invitation.Requester, Confirmer: launch.Invitation.DecisionPrincipal,
		RuntimePrincipal: launch.Scope.RuntimePrincipal, AgentProfile: launch.Invitation.SpecialistProfile, Eligibility: eligibility, ContextDigest: launch.Context.ContextDigest,
		ApprovalLimits: launch.ApprovalLimits, ToolIDs: append([]string(nil), launch.Context.ToolIDs...), ExpiresAt: launch.Invitation.ExpiresAt.UTC(), Receipt: launch.CapabilityReceipt,
	}
}

func containsMeetingSpecialistTool(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
