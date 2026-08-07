package main

// E8 workforce runtime is a deterministic, offline lifecycle ledger. An
// "active" seat is an approved roster record, not a running model: issuance
// remains fenced until a separately reviewed runtime integration exists.

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrSTRIDEWorkforceInvalid     = errors.New("invalid STRIDE workforce request")
	ErrSTRIDEWorkforceUnknown     = errors.New("unknown STRIDE workforce agent")
	ErrSTRIDEWorkforceState       = errors.New("invalid STRIDE workforce lifecycle state")
	ErrSTRIDEWorkforceIdempotency = errors.New("conflicting STRIDE workforce idempotency key")
	ErrSTRIDEWorkforceFenced      = errors.New("STRIDE workforce runtime is default-off")
	ErrSTRIDEWorkforceSensitive   = errors.New("sensitive STRIDE workforce learning denied")
	ErrSTRIDEWorkforceAuthority   = errors.New("STRIDE workforce authority denied")
)

type STRIDEWorkforceSeat struct {
	ID                 string
	OrgIdentity        string
	DirectThread       string
	Package            STRIDEReference
	Listing            STRIDEReference
	Overlay            *STRIDEReference
	Capability         STRIDEReference
	Route              STRIDEReference
	Owner              string
	Memberships        []string
	PerRunBudgetCents  int64
	DailyBudgetCents   int64
	MonthlyBudgetCents int64
	Concurrency        int
	Proactivity        string
	Status             string
	ActivationStage    string
	AccessRevoked      bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
	OffboardedAt       *time.Time
}

type STRIDEWorkforceHireRequest struct {
	IdempotencyKey string
	AgentID        string
	Template       STRIDEAgentTemplate
	Listing        STRIDEMarketplaceListingRecord
	Owner          string
	Overlay        *STRIDEReference
	Capability     STRIDEReference
	Route          STRIDEReference
}

type STRIDEWorkforceReceipt struct {
	Action         string
	IdempotencyKey string
	AgentID        string
	RequestDigest  string
	Before         string
	After          string
	At             time.Time
	Digest         string
}

type STRIDERuntimeGrant struct {
	PrincipalID           string
	CapabilityTokenDigest string
	ExpiresAt             time.Time
	Fenced                bool
}

// STRIDEPerformanceApproval binds a human decision to the exact capability
// revision and an evaluation receipt. Neither a learning record nor a model
// may manufacture this approval.
type STRIDEPerformanceApproval struct {
	Capability STRIDEReference
	Evaluation STRIDEReference
	ApprovedBy string
}

func (approval STRIDEPerformanceApproval) Validate() error {
	if approval.Capability.Validate() != nil || approval.Capability.ContractType != STRIDEContractAgentCapabilityManifest || approval.Evaluation.Validate() != nil || !strideIdentifier(approval.ApprovedBy) {
		return ErrSTRIDEWorkforceInvalid
	}
	return nil
}

type STRIDEWorkforceCanaryManifest struct {
	SeatID             string
	RouteDescriptionID string
	Synthetic          bool
	OneSeat            bool
	ProfileChange      bool
	RouteChange        bool
	Status             string
}

type STRIDEUpdateReview struct {
	Proposal              AgentUpdateProposal
	PersonalityDiffDigest string
	ModelDiffDigest       string
	CostDiffDigest        string
	Approver              string
	Status                string
	AppliedStages         []string
}

type STRIDEDraftRecommendation struct {
	ID           string
	AgentID      string
	Kind         string
	ReasonDigest string
	Status       string
}

type STRIDEScoutRosterView struct {
	Seats           []STRIDEWorkforceSeat
	Recommendations []STRIDEDraftRecommendation
}

type STRIDEWorkforceSnapshot struct {
	Seats       []STRIDEWorkforceSeat
	Receipts    []STRIDEWorkforceReceipt
	Learning    []AgentLearningRecord
	Performance []AgentPerformanceReceipt
	Updates     []STRIDEUpdateReview
	Canaries    []STRIDEWorkforceCanaryManifest
	Generation  uint64
	KeyID       string
	Digest      string
	Signature   string
}

type STRIDEWorkforceRuntime struct {
	mu                 sync.RWMutex
	seats              map[string]STRIDEWorkforceSeat
	receipts           map[string]STRIDEWorkforceReceipt
	learning           map[string]AgentLearningRecord
	performance        map[string]AgentPerformanceReceipt
	updates            map[string]STRIDEUpdateReview
	canaries           map[string]STRIDEWorkforceCanaryManifest
	snapshotGeneration uint64
}

func NewSTRIDEWorkforceRuntime() *STRIDEWorkforceRuntime {
	runtime := &STRIDEWorkforceRuntime{seats: map[string]STRIDEWorkforceSeat{}, receipts: map[string]STRIDEWorkforceReceipt{}, learning: map[string]AgentLearningRecord{}, performance: map[string]AgentPerformanceReceipt{}, updates: map[string]STRIDEUpdateReview{}, canaries: map[string]STRIDEWorkforceCanaryManifest{}}
	for _, fixture := range []string{"insights", "mary_marketing", "research", "design", "builder"} {
		runtime.seats["fixture_"+fixture] = STRIDEWorkforceSeat{ID: "fixture_" + fixture, OrgIdentity: "org_agent:fixture_" + fixture, DirectThread: "thread_fixture_" + fixture, Status: "unavailable", AccessRevoked: true}
	}
	return runtime
}

// installFencedInternalPreviewSeat projects a human-approved, first-party
// deterministic preview into the canonical roster. It deliberately cannot be
// called with a marketplace/provider listing and it never changes the hard
// IssueRuntimeGrant fence below. "active" here means the coworker identity is
// visible to product control planes (including meeting invitations), not that
// a model session can start.
func (runtime *STRIDEWorkforceRuntime) installFencedInternalPreviewSeat(actor STRIDEWorkforceActor, receipt STRIDEProductActivationReceipt, agent STRIDEProductTeamAgent, now time.Time) (STRIDEWorkforceSeat, error) {
	if runtime == nil || actor.Validate() != nil || !actor.IsAdmin || receipt.Scope != STRIDEProductScopeMarketplace || receipt.Mode != "deterministic_local" || !isHexDigest(receipt.Digest) || !isHexDigest(receipt.Signature) ||
		validateSTRIDEProductAgent(agent) != nil || agent.Status != "hired_fenced" || !agent.ProviderExecutionFenced || agent.AccessRevoked || now.IsZero() {
		return STRIDEWorkforceSeat{}, ErrSTRIDEWorkforceInvalid
	}
	requestDigest, err := STRIDEContractDigest(struct {
		Activation STRIDEProductActivationReceipt
		Agent      STRIDEProductTeamAgent
		ListingID  string
	}{receipt, agent, agent.ListingID})
	if err != nil {
		return STRIDEWorkforceSeat{}, ErrSTRIDEWorkforceInvalid
	}
	receiptKeyBase := "internal_preview_" + temporalDigest(agent.ID + "\x00" + agent.ListingID)[:24]
	buildSeat := func(at time.Time) STRIDEWorkforceSeat {
		digest := temporalDigest(agent.ID + "\x00" + agent.ListingID)
		overlay := STRIDEReference{ContractType: STRIDEContractAgentProfileOverlay, ID: "profile_" + agent.ID, Revision: 1, Digest: temporalDigest("profile:" + digest)}
		return STRIDEWorkforceSeat{ID: agent.ID, OrgIdentity: "org_agent:" + agent.ID, DirectThread: agent.DirectThreadID,
			Package: STRIDEReference{ContractType: STRIDEContractAgentPackageManifest, ID: "package_" + agent.ListingID, Revision: 1, Digest: temporalDigest("package:" + digest)},
			Listing: STRIDEReference{ContractType: STRIDEContractMarketplaceListing, ID: "listing_" + agent.ListingID, Revision: 1, Digest: temporalDigest("listing:" + digest)}, Overlay: &overlay,
			Capability: STRIDEReference{ContractType: STRIDEContractAgentCapabilityManifest, ID: "capability_" + agent.ID, Revision: 1, Digest: temporalDigest("capability:" + digest)},
			Route:      STRIDEReference{ContractType: STRIDEContractOutcome, ID: "route_" + agent.ID, Revision: 1, Digest: temporalDigest("route:fenced:" + digest)}, Owner: agent.OwnerID,
			// Hiring creates an organization-owned identity, not organization-wide
			// authority. Meeting eligibility is granted only by a separate exact
			// Product assignment and is never inferred from this roster projection.
			Memberships: uniqueSortedStrings(agent.Config.Memberships), PerRunBudgetCents: agent.Config.PerRunBudgetCents, DailyBudgetCents: agent.Config.DailyBudgetCents,
			Concurrency: 1, Proactivity: agent.Config.Proactivity, Status: "active", ActivationStage: "complete", AccessRevoked: false, CreatedAt: at.UTC(), UpdatedAt: at.UTC()}
	}
	buildReceipts := func(at time.Time) []STRIDEWorkforceReceipt {
		transitions := []struct{ action, before, after string }{
			{"create", "", "draft_hire"},
			{"trial", "draft_hire", "trial_pending"},
			{"hire", "trial_pending", "trial_active"},
			{"activate_identity", "trial_active", "trial_active"},
			{"activate_capability", "trial_active", "trial_active"},
			{"activate_profile", "trial_active", "trial_active"},
			{"activate_route", "trial_active", "review_required"},
			{"review", "review_required", "active"},
		}
		result := make([]STRIDEWorkforceReceipt, 0, len(transitions))
		for _, transition := range transitions {
			lifecycleReceipt := newSTRIDEWorkforceReceipt(transition.action, receiptKeyBase+"_"+transition.action, agent.ID, transition.before, transition.after, at)
			if transition.action == "create" {
				lifecycleReceipt.RequestDigest = requestDigest
			}
			result = append(result, lifecycleReceipt)
		}
		return result
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if prior, ok := runtime.seats[agent.ID]; ok {
		createKey := "create:" + receiptKeyBase + "_create"
		createReceipt, found := runtime.receipts[createKey]
		if !found || createReceipt.AgentID != agent.ID || createReceipt.RequestDigest != requestDigest {
			return STRIDEWorkforceSeat{}, ErrSTRIDEWorkforceIdempotency
		}
		expectedSeat := buildSeat(createReceipt.At)
		if prior.Status != "active" || prior.AccessRevoked || mustSTRIDEWorkforceDigest(prior) != mustSTRIDEWorkforceDigest(expectedSeat) {
			return STRIDEWorkforceSeat{}, ErrSTRIDEWorkforceState
		}
		for _, expected := range buildReceipts(createReceipt.At) {
			actual, found := runtime.receipts[expected.Action+":"+expected.IdempotencyKey]
			if !found || actual != expected {
				return STRIDEWorkforceSeat{}, ErrSTRIDEWorkforceState
			}
		}
		return cloneSTRIDEWorkforceSeat(prior), nil
	}
	seat := buildSeat(now)
	if !validSTRIDEWorkforceSeat(seat) {
		return STRIDEWorkforceSeat{}, ErrSTRIDEWorkforceInvalid
	}
	lifecycleReceipts := buildReceipts(now)
	for _, lifecycleReceipt := range lifecycleReceipts {
		key := lifecycleReceipt.Action + ":" + lifecycleReceipt.IdempotencyKey
		if _, exists := runtime.receipts[key]; exists {
			return STRIDEWorkforceSeat{}, ErrSTRIDEWorkforceIdempotency
		}
	}
	runtime.seats[seat.ID] = seat
	for _, lifecycleReceipt := range lifecycleReceipts {
		runtime.receipts[lifecycleReceipt.Action+":"+lifecycleReceipt.IdempotencyKey] = lifecycleReceipt
	}
	return cloneSTRIDEWorkforceSeat(seat), nil
}

func (runtime *STRIDEWorkforceRuntime) CreateFromTemplate(actor STRIDEWorkforceActor, request STRIDEWorkforceHireRequest, now time.Time) (STRIDEWorkforceSeat, STRIDEWorkforceReceipt, error) {
	if runtime == nil || actor.Validate() != nil || !actor.IsAdmin {
		return STRIDEWorkforceSeat{}, STRIDEWorkforceReceipt{}, ErrSTRIDEAdminRequired
	}
	if !strideIdentifier(request.IdempotencyKey) || !strideIdentifier(request.AgentID) || !strideIdentifier(request.Owner) || request.Template.Validate() != nil || request.Listing.State != STRIDEListingAvailable || !request.Listing.Available || request.Listing.Listing.Package != request.Template.Package || request.Capability.Validate() != nil || request.Route.Validate() != nil || now.IsZero() {
		return STRIDEWorkforceSeat{}, STRIDEWorkforceReceipt{}, ErrSTRIDEWorkforceInvalid
	}
	if request.Overlay != nil && request.Overlay.Validate() != nil {
		return STRIDEWorkforceSeat{}, STRIDEWorkforceReceipt{}, ErrSTRIDEWorkforceInvalid
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	requestDigest := mustSTRIDEWorkforceDigest(struct {
		AgentID           string
		Template          STRIDEAgentTemplate
		Listing           STRIDEReference
		Owner             string
		Overlay           *STRIDEReference
		Capability, Route STRIDEReference
	}{request.AgentID, request.Template, strideMarketplaceListingReference(request.Listing.Listing), request.Owner, request.Overlay, request.Capability, request.Route})
	if receipt, found, err := runtime.idempotentLocked("create", request.IdempotencyKey, request.AgentID); found || err != nil {
		if err != nil {
			return STRIDEWorkforceSeat{}, STRIDEWorkforceReceipt{}, err
		}
		if receipt.RequestDigest != requestDigest {
			return STRIDEWorkforceSeat{}, STRIDEWorkforceReceipt{}, ErrSTRIDEWorkforceIdempotency
		}
		seat := runtime.seats[request.AgentID]
		return cloneSTRIDEWorkforceSeat(seat), receipt, nil
	}
	if _, exists := runtime.seats[request.AgentID]; exists {
		return STRIDEWorkforceSeat{}, STRIDEWorkforceReceipt{}, ErrSTRIDEWorkforceState
	}
	seat := STRIDEWorkforceSeat{ID: request.AgentID, OrgIdentity: "org_agent:" + request.AgentID, DirectThread: "thread_agent_" + request.AgentID, Package: request.Template.Package, Listing: strideMarketplaceListingReference(request.Listing.Listing), Overlay: cloneSTRIDEReference(request.Overlay), Capability: request.Capability, Route: request.Route, Owner: request.Owner, Memberships: append([]string(nil), request.Template.Memberships...), PerRunBudgetCents: request.Template.PerRunBudgetCents, DailyBudgetCents: request.Template.DailyBudgetCents, MonthlyBudgetCents: request.Template.MonthlyBudgetCents, Concurrency: request.Template.Concurrency, Proactivity: request.Template.Proactivity, Status: "draft_hire", ActivationStage: "identity", AccessRevoked: true, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	runtime.seats[seat.ID] = seat
	receipt := newSTRIDEWorkforceReceipt("create", request.IdempotencyKey, seat.ID, "", seat.Status, now)
	receipt.RequestDigest = requestDigest
	runtime.receipts[receipt.Action+":"+receipt.IdempotencyKey] = receipt
	return cloneSTRIDEWorkforceSeat(seat), receipt, nil
}

func (runtime *STRIDEWorkforceRuntime) Trial(actor STRIDEWorkforceActor, agentID, idempotencyKey string, now time.Time) (STRIDEWorkforceReceipt, error) {
	return runtime.transition(actor, "trial", agentID, idempotencyKey, now, "draft_hire", "trial_pending")
}
func (runtime *STRIDEWorkforceRuntime) Hire(actor STRIDEWorkforceActor, agentID, idempotencyKey string, now time.Time) (STRIDEWorkforceReceipt, error) {
	return runtime.transition(actor, "hire", agentID, idempotencyKey, now, "trial_pending", "trial_active")
}
func (runtime *STRIDEWorkforceRuntime) Pause(actor STRIDEWorkforceActor, agentID, idempotencyKey string, now time.Time) (STRIDEWorkforceReceipt, error) {
	return runtime.transitionAny(actor, "pause", agentID, idempotencyKey, now, []string{"trial_pending", "trial_active", "review_required", "active", "quarantined"}, "paused")
}
func (runtime *STRIDEWorkforceRuntime) Quarantine(actor STRIDEWorkforceActor, agentID, idempotencyKey string, now time.Time) (STRIDEWorkforceReceipt, error) {
	return runtime.transitionAny(actor, "quarantine", agentID, idempotencyKey, now, []string{"draft_hire", "trial_pending", "trial_active", "review_required", "active", "paused"}, "quarantined")
}

// Activate advances exactly one stage. Profile and route changes can never be
// combined in the same canary or receipt.
func (runtime *STRIDEWorkforceRuntime) Activate(actor STRIDEWorkforceActor, agentID, idempotencyKey, stage string, now time.Time) (STRIDEWorkforceReceipt, error) {
	if runtime == nil || actor.Validate() != nil || !actor.IsAdmin {
		return STRIDEWorkforceReceipt{}, ErrSTRIDEAdminRequired
	}
	if !strideIdentifier(agentID) || !strideIdentifier(idempotencyKey) || !oneOf(stage, "identity", "capability", "profile", "route") || now.IsZero() {
		return STRIDEWorkforceReceipt{}, ErrSTRIDEWorkforceInvalid
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if receipt, found, err := runtime.idempotentLocked("activate_"+stage, idempotencyKey, agentID); found || err != nil {
		if err != nil {
			return STRIDEWorkforceReceipt{}, err
		}
		return receipt, nil
	}
	seat, found := runtime.seats[agentID]
	if !found || seat.Status != "trial_active" || seat.ActivationStage != stage {
		return STRIDEWorkforceReceipt{}, ErrSTRIDEWorkforceState
	}
	before := seat.Status
	switch stage {
	case "identity":
		seat.ActivationStage = "capability"
	case "capability":
		seat.ActivationStage = "profile"
	case "profile":
		seat.ActivationStage = "route"
	case "route":
		seat.ActivationStage = "review"
		seat.Status = "review_required"
	}
	seat.UpdatedAt = now.UTC()
	runtime.seats[agentID] = seat
	receipt := newSTRIDEWorkforceReceipt("activate_"+stage, idempotencyKey, agentID, before, seat.Status, now)
	runtime.receipts[receipt.Action+":"+receipt.IdempotencyKey] = receipt
	return receipt, nil
}

// Review is the explicit human gate between a technically configured seat and
// an active coworker. Route readiness never grants runtime access by itself.
func (runtime *STRIDEWorkforceRuntime) Review(actor STRIDEWorkforceActor, agentID, idempotencyKey string, now time.Time) (STRIDEWorkforceReceipt, error) {
	if runtime == nil || actor.Validate() != nil || !actor.IsAdmin {
		return STRIDEWorkforceReceipt{}, ErrSTRIDEAdminRequired
	}
	if !strideIdentifier(agentID) || !strideIdentifier(idempotencyKey) || now.IsZero() {
		return STRIDEWorkforceReceipt{}, ErrSTRIDEWorkforceInvalid
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if receipt, found, err := runtime.idempotentLocked("review", idempotencyKey, agentID); found || err != nil {
		if err != nil {
			return STRIDEWorkforceReceipt{}, err
		}
		return receipt, nil
	}
	seat, found := runtime.seats[agentID]
	if !found || seat.Status != "review_required" || seat.ActivationStage != "review" || !seat.AccessRevoked {
		return STRIDEWorkforceReceipt{}, ErrSTRIDEWorkforceState
	}
	before := seat.Status
	seat.Status = "active"
	seat.ActivationStage = "complete"
	seat.AccessRevoked = false
	seat.UpdatedAt = now.UTC()
	runtime.seats[agentID] = seat
	receipt := newSTRIDEWorkforceReceipt("review", idempotencyKey, agentID, before, seat.Status, now)
	runtime.receipts[receipt.Action+":"+receipt.IdempotencyKey] = receipt
	return receipt, nil
}

func (runtime *STRIDEWorkforceRuntime) Offboard(actor STRIDEWorkforceActor, agentID, idempotencyKey string, now time.Time) (STRIDEWorkforceReceipt, error) {
	if runtime == nil || actor.Validate() != nil || !actor.IsAdmin {
		return STRIDEWorkforceReceipt{}, ErrSTRIDEAdminRequired
	}
	if !strideIdentifier(agentID) || !strideIdentifier(idempotencyKey) || now.IsZero() {
		return STRIDEWorkforceReceipt{}, ErrSTRIDEWorkforceInvalid
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if receipt, found, err := runtime.idempotentLocked("offboard", idempotencyKey, agentID); found || err != nil {
		if err != nil {
			return STRIDEWorkforceReceipt{}, err
		}
		return receipt, nil
	}
	seat, found := runtime.seats[agentID]
	if !found || seat.Status == "unavailable" || seat.Status == "offboarded" {
		return STRIDEWorkforceReceipt{}, ErrSTRIDEWorkforceState
	}
	before := seat.Status
	at := now.UTC()
	seat.Status = "offboarded"
	seat.AccessRevoked = true
	seat.OffboardedAt = &at
	seat.UpdatedAt = at
	runtime.seats[agentID] = seat
	receipt := newSTRIDEWorkforceReceipt("offboard", idempotencyKey, agentID, before, seat.Status, now)
	runtime.receipts[receipt.Action+":"+receipt.IdempotencyKey] = receipt
	return receipt, nil
}

func (runtime *STRIDEWorkforceRuntime) Export(actor STRIDEWorkforceActor, agentID, idempotencyKey string, now time.Time) (STRIDEWorkforceSeat, STRIDEWorkforceReceipt, error) {
	if runtime == nil || actor.Validate() != nil || !actor.IsAdmin {
		return STRIDEWorkforceSeat{}, STRIDEWorkforceReceipt{}, ErrSTRIDEAdminRequired
	}
	if !strideIdentifier(agentID) || !strideIdentifier(idempotencyKey) || now.IsZero() {
		return STRIDEWorkforceSeat{}, STRIDEWorkforceReceipt{}, ErrSTRIDEWorkforceInvalid
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if receipt, found, err := runtime.idempotentLocked("export", idempotencyKey, agentID); found || err != nil {
		if err != nil {
			return STRIDEWorkforceSeat{}, STRIDEWorkforceReceipt{}, err
		}
		return cloneSTRIDEWorkforceSeat(runtime.seats[agentID]), receipt, nil
	}
	seat, found := runtime.seats[agentID]
	if !found {
		return STRIDEWorkforceSeat{}, STRIDEWorkforceReceipt{}, ErrSTRIDEWorkforceUnknown
	}
	receipt := newSTRIDEWorkforceReceipt("export", idempotencyKey, agentID, seat.Status, seat.Status, now)
	runtime.receipts[receipt.Action+":"+receipt.IdempotencyKey] = receipt
	return cloneSTRIDEWorkforceSeat(seat), receipt, nil
}

func (runtime *STRIDEWorkforceRuntime) IssueRuntimeGrant(actor STRIDEWorkforceActor, agentID string, now time.Time) (STRIDERuntimeGrant, error) {
	if runtime == nil || actor.Validate() != nil || !actor.IsAdmin {
		return STRIDERuntimeGrant{}, ErrSTRIDEAdminRequired
	}
	if !strideIdentifier(agentID) || now.IsZero() {
		return STRIDERuntimeGrant{}, ErrSTRIDEWorkforceInvalid
	}
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	seat, found := runtime.seats[agentID]
	if !found {
		return STRIDERuntimeGrant{}, ErrSTRIDEWorkforceUnknown
	}
	if seat.Status != "active" || seat.AccessRevoked {
		return STRIDERuntimeGrant{}, ErrSTRIDEWorkforceAuthority
	}
	// The digest is an audit placeholder, not a bearer token. A real token
	// cannot be minted until the global default-off fence is removed in a
	// reviewed runtime wave.
	grant := STRIDERuntimeGrant{PrincipalID: "runtime:" + seat.OrgIdentity, CapabilityTokenDigest: mustSTRIDEWorkforceDigest(struct{ Seat, Capability string }{seat.ID, seat.Capability.Digest}), ExpiresAt: now.UTC().Add(15 * time.Minute), Fenced: true}
	return grant, ErrSTRIDEWorkforceFenced
}

func (runtime *STRIDEWorkforceRuntime) RegisterCanary(actor STRIDEWorkforceActor, manifest STRIDEWorkforceCanaryManifest) error {
	if runtime == nil || actor.Validate() != nil || !actor.IsAdmin {
		return ErrSTRIDEAdminRequired
	}
	if !strideIdentifier(manifest.SeatID) || !strideIdentifier(manifest.RouteDescriptionID) || !manifest.OneSeat || manifest.ProfileChange && manifest.RouteChange || !oneOf(manifest.Status, "draft", "evaluated", "qualified") {
		return ErrSTRIDEWorkforceInvalid
	}
	if manifest.Status == "qualified" && manifest.Synthetic {
		return ErrSTRIDEWorkforceFenced
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	seat, found := runtime.seats[manifest.SeatID]
	if !found || seat.Status == "unavailable" {
		return ErrSTRIDEWorkforceUnknown
	}
	runtime.canaries[manifest.SeatID] = manifest
	return nil
}

func (runtime *STRIDEWorkforceRuntime) RecordLearning(actor STRIDEWorkforceActor, record AgentLearningRecord) error {
	if runtime == nil || actor.Validate() != nil || !actor.IsAdmin {
		return ErrSTRIDEAdminRequired
	}
	if record.Validate() != nil {
		return ErrSTRIDEWorkforceInvalid
	}
	if sensitiveWorkforceLearning(record.Subject) || sensitiveWorkforceLearning(record.Scope) {
		return ErrSTRIDEWorkforceSensitive
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	seat, found := runtime.seats[record.AgentID]
	if !found || seat.Status == "offboarded" {
		return ErrSTRIDEWorkforceAuthority
	}
	runtime.learning[record.Header.ID] = record
	return nil
}
func (runtime *STRIDEWorkforceRuntime) CorrectLearning(actor STRIDEWorkforceActor, record AgentLearningRecord) error {
	if record.Status != "corrected" {
		return ErrSTRIDEWorkforceInvalid
	}
	return runtime.RecordLearning(actor, record)
}
func (runtime *STRIDEWorkforceRuntime) ForgetLearning(actor STRIDEWorkforceActor, id string) error {
	if runtime == nil || actor.Validate() != nil || !actor.IsAdmin {
		return ErrSTRIDEAdminRequired
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	record, found := runtime.learning[id]
	if !found {
		return ErrSTRIDEWorkforceUnknown
	}
	record.Status = "forgotten"
	runtime.learning[id] = record
	return nil
}
func (runtime *STRIDEWorkforceRuntime) PurgeLearning(actor STRIDEWorkforceActor, id string) error {
	if runtime == nil || actor.Validate() != nil || !actor.IsAdmin {
		return ErrSTRIDEAdminRequired
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if _, found := runtime.learning[id]; !found {
		return ErrSTRIDEWorkforceUnknown
	}
	delete(runtime.learning, id)
	return nil
}

func (runtime *STRIDEWorkforceRuntime) RecordPerformance(actor STRIDEWorkforceActor, agentID string, receipt AgentPerformanceReceipt, approval STRIDEPerformanceApproval) (bool, error) {
	if runtime == nil || actor.Validate() != nil || !actor.IsAdmin {
		return false, ErrSTRIDEAdminRequired
	}
	if receipt.Validate() != nil || approval.Validate() != nil || !strideIdentifier(agentID) {
		return false, ErrSTRIDEWorkforceInvalid
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	seat, found := runtime.seats[agentID]
	if !found || seat.Status == "offboarded" {
		return false, ErrSTRIDEWorkforceAuthority
	}
	if approval.Capability != seat.Capability || receipt.Package != seat.Package || receipt.Route != seat.Route || !containsSTRIDEReference(receipt.Evidence, approval.Evaluation) {
		return false, ErrSTRIDEWorkforceAuthority
	}
	runtime.performance[receipt.Header.ID] = receipt
	return receipt.Verdict == "accepted", nil
}

func (runtime *STRIDEWorkforceRuntime) ProposeUpdate(actor STRIDEWorkforceActor, review STRIDEUpdateReview) error {
	if runtime == nil || actor.Validate() != nil || !actor.IsAdmin {
		return ErrSTRIDEAdminRequired
	}
	if review.Proposal.Validate() != nil || !allDigests(review.PersonalityDiffDigest, review.ModelDiffDigest, review.CostDiffDigest) || review.Approver != "" || review.Status != "pending" {
		return ErrSTRIDEWorkforceInvalid
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if _, found := runtime.seats[review.Proposal.TeamAgent.ID]; !found {
		return ErrSTRIDEWorkforceUnknown
	}
	runtime.updates[review.Proposal.Header.ID] = cloneSTRIDEUpdateReview(review)
	return nil
}
func (runtime *STRIDEWorkforceRuntime) ApproveUpdate(actor STRIDEWorkforceActor, proposalID string) error {
	if runtime == nil || actor.Validate() != nil || !actor.IsAdmin {
		return ErrSTRIDEAdminRequired
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	review, found := runtime.updates[proposalID]
	if !found {
		return ErrSTRIDEWorkforceUnknown
	}
	if review.Status != "pending" {
		return ErrSTRIDEWorkforceState
	}
	review.Approver = actor.ID
	review.Status = "trial"
	runtime.updates[proposalID] = review
	return nil
}
func (runtime *STRIDEWorkforceRuntime) ApplyUpdateCanary(actor STRIDEWorkforceActor, proposalID, stage string) error {
	if runtime == nil || actor.Validate() != nil || !actor.IsAdmin {
		return ErrSTRIDEAdminRequired
	}
	if !oneOf(stage, "profile", "route") {
		return ErrSTRIDEWorkforceInvalid
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	review, found := runtime.updates[proposalID]
	if !found {
		return ErrSTRIDEWorkforceUnknown
	}
	if review.Status != "trial" || !strideIdentifier(review.Approver) {
		return ErrSTRIDEWorkforceAuthority
	}
	for _, applied := range review.AppliedStages {
		if applied == stage {
			return nil
		}
	}
	if stage == "route" && len(review.AppliedStages) == 0 {
		return ErrSTRIDEWorkforceState
	}
	review.AppliedStages = append(review.AppliedStages, stage)
	if stage == "route" {
		review.Status = "activated"
	}
	runtime.updates[proposalID] = review
	return nil
}

// RollbackUpdate never deletes the organization-owned seat, overlay, or local
// learning. It only fences the candidate rollout and returns its review state
// to the previously approved local identity.
func (runtime *STRIDEWorkforceRuntime) RollbackUpdate(actor STRIDEWorkforceActor, proposalID string) error {
	if runtime == nil || actor.Validate() != nil || !actor.IsAdmin {
		return ErrSTRIDEAdminRequired
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	review, found := runtime.updates[proposalID]
	if !found {
		return ErrSTRIDEWorkforceUnknown
	}
	if !oneOf(review.Status, "trial", "activated") || !strideIdentifier(review.Approver) {
		return ErrSTRIDEWorkforceState
	}
	review.Status = "rolled_back"
	runtime.updates[proposalID] = review
	return nil
}

func (runtime *STRIDEWorkforceRuntime) ScoutRosterView() STRIDEScoutRosterView {
	if runtime == nil {
		return STRIDEScoutRosterView{}
	}
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	view := STRIDEScoutRosterView{}
	for _, seat := range runtime.seats {
		view.Seats = append(view.Seats, cloneSTRIDEWorkforceSeat(seat))
		if seat.Status == "paused" || seat.Status == "quarantined" {
			view.Recommendations = append(view.Recommendations, STRIDEDraftRecommendation{ID: "draft_" + seat.ID, AgentID: seat.ID, Kind: "review_agent", ReasonDigest: mustSTRIDEWorkforceDigest(seat.ID + ":" + seat.Status), Status: "draft"})
		}
	}
	sort.Slice(view.Seats, func(i, j int) bool { return view.Seats[i].ID < view.Seats[j].ID })
	sort.Slice(view.Recommendations, func(i, j int) bool { return view.Recommendations[i].ID < view.Recommendations[j].ID })
	return view
}

func (runtime *STRIDEWorkforceRuntime) Snapshot() (STRIDEWorkforceSnapshot, error) {
	if runtime == nil {
		return STRIDEWorkforceSnapshot{}, ErrSTRIDEWorkforceInvalid
	}
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.snapshotLocked()
}

func (runtime *STRIDEWorkforceRuntime) snapshotLocked() (STRIDEWorkforceSnapshot, error) {
	snapshot := STRIDEWorkforceSnapshot{Generation: runtime.snapshotGeneration}
	for _, v := range runtime.seats {
		snapshot.Seats = append(snapshot.Seats, cloneSTRIDEWorkforceSeat(v))
	}
	for _, v := range runtime.receipts {
		snapshot.Receipts = append(snapshot.Receipts, v)
	}
	for _, v := range runtime.learning {
		snapshot.Learning = append(snapshot.Learning, v)
	}
	for _, v := range runtime.performance {
		snapshot.Performance = append(snapshot.Performance, v)
	}
	for _, v := range runtime.updates {
		snapshot.Updates = append(snapshot.Updates, cloneSTRIDEUpdateReview(v))
	}
	for _, v := range runtime.canaries {
		snapshot.Canaries = append(snapshot.Canaries, v)
	}
	sort.Slice(snapshot.Seats, func(i, j int) bool { return snapshot.Seats[i].ID < snapshot.Seats[j].ID })
	sort.Slice(snapshot.Receipts, func(i, j int) bool { return snapshot.Receipts[i].Digest < snapshot.Receipts[j].Digest })
	sort.Slice(snapshot.Learning, func(i, j int) bool { return snapshot.Learning[i].Header.ID < snapshot.Learning[j].Header.ID })
	sort.Slice(snapshot.Performance, func(i, j int) bool { return snapshot.Performance[i].Header.ID < snapshot.Performance[j].Header.ID })
	sort.Slice(snapshot.Updates, func(i, j int) bool {
		return snapshot.Updates[i].Proposal.Header.ID < snapshot.Updates[j].Proposal.Header.ID
	})
	sort.Slice(snapshot.Canaries, func(i, j int) bool { return snapshot.Canaries[i].SeatID < snapshot.Canaries[j].SeatID })
	digest, err := strideWorkforceSnapshotStateDigest(snapshot)
	if err != nil {
		return STRIDEWorkforceSnapshot{}, err
	}
	snapshot.Digest = digest
	return snapshot, nil
}

func strideWorkforceSnapshotStateDigest(snapshot STRIDEWorkforceSnapshot) (string, error) {
	return STRIDEContractDigest(struct {
		Seats       []STRIDEWorkforceSeat
		Receipts    []STRIDEWorkforceReceipt
		Learning    []AgentLearningRecord
		Performance []AgentPerformanceReceipt
		Updates     []STRIDEUpdateReview
		Canaries    []STRIDEWorkforceCanaryManifest
	}{snapshot.Seats, snapshot.Receipts, snapshot.Learning, snapshot.Performance, snapshot.Updates, snapshot.Canaries})
}

// AuthenticatedSnapshot seals the canonical state with a configured MAC key.
// Generations are monotonic within a runtime. Cross-process rollback defense
// is completed by passing an externally persisted high-water mark to restore.
func (runtime *STRIDEWorkforceRuntime) AuthenticatedSnapshot(authority STRIDESnapshotMACAuthority, generation uint64) (STRIDEWorkforceSnapshot, error) {
	if runtime == nil || !authority.valid() {
		return STRIDEWorkforceSnapshot{}, ErrSTRIDEWorkforceInvalid
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if generation == 0 || generation <= runtime.snapshotGeneration {
		return STRIDEWorkforceSnapshot{}, ErrSTRIDEWorkforceInvalid
	}
	snapshot, err := runtime.snapshotLocked()
	if err != nil {
		return STRIDEWorkforceSnapshot{}, err
	}
	snapshot.Generation = generation
	snapshot.KeyID = authority.KeyID
	snapshot.Signature, err = strideSnapshotMAC(authority, "stride_workforce", generation, snapshot.Digest)
	if err != nil {
		return STRIDEWorkforceSnapshot{}, err
	}
	runtime.snapshotGeneration = generation
	return snapshot, nil
}

// RestoreSTRIDEWorkforceRuntime requires a configured trust policy. An
// unsigned self-consistent digest is never sufficient authority to resurrect
// workforce identities, lifecycle state, or qualification claims.
func RestoreSTRIDEWorkforceRuntime(snapshot STRIDEWorkforceSnapshot, policies ...STRIDESnapshotRestorePolicy) (*STRIDEWorkforceRuntime, error) {
	if len(policies) != 1 || !isHexDigest(snapshot.Digest) || !verifySTRIDESnapshotMAC(policies[0], "stride_workforce", snapshot.KeyID, snapshot.Generation, snapshot.Digest, snapshot.Signature) {
		return nil, ErrSTRIDEWorkforceInvalid
	}
	digest, err := strideWorkforceSnapshotStateDigest(snapshot)
	if err != nil || digest != snapshot.Digest {
		return nil, ErrSTRIDEWorkforceInvalid
	}
	runtime := NewSTRIDEWorkforceRuntime()
	runtime.seats = map[string]STRIDEWorkforceSeat{}
	for _, seat := range snapshot.Seats {
		if !validSTRIDEWorkforceSeat(seat) {
			return nil, ErrSTRIDEWorkforceInvalid
		}
		if _, exists := runtime.seats[seat.ID]; exists {
			return nil, ErrSTRIDEWorkforceInvalid
		}
		runtime.seats[seat.ID] = cloneSTRIDEWorkforceSeat(seat)
	}
	for _, receipt := range snapshot.Receipts {
		if !validSTRIDEWorkforceReceipt(receipt) {
			return nil, ErrSTRIDEWorkforceInvalid
		}
		key := receipt.Action + ":" + receipt.IdempotencyKey
		if _, exists := runtime.receipts[key]; exists {
			return nil, ErrSTRIDEWorkforceInvalid
		}
		runtime.receipts[key] = receipt
	}
	if !replayValidSTRIDEWorkforceLifecycle(runtime.seats, snapshot.Receipts) {
		return nil, ErrSTRIDEWorkforceInvalid
	}
	for _, record := range snapshot.Learning {
		if record.Validate() != nil || sensitiveWorkforceLearning(record.Subject) || sensitiveWorkforceLearning(record.Scope) {
			return nil, ErrSTRIDEWorkforceInvalid
		}
		if _, exists := runtime.learning[record.Header.ID]; exists {
			return nil, ErrSTRIDEWorkforceInvalid
		}
		seat, exists := runtime.seats[record.AgentID]
		if !exists || seat.Status == "offboarded" || seat.Status == "unavailable" {
			return nil, ErrSTRIDEWorkforceInvalid
		}
		runtime.learning[record.Header.ID] = record
	}
	for _, receipt := range snapshot.Performance {
		if receipt.Validate() != nil {
			return nil, ErrSTRIDEWorkforceInvalid
		}
		if _, exists := runtime.performance[receipt.Header.ID]; exists {
			return nil, ErrSTRIDEWorkforceInvalid
		}
		runtime.performance[receipt.Header.ID] = receipt
	}
	for _, review := range snapshot.Updates {
		if review.Proposal.Validate() != nil || !allDigests(review.PersonalityDiffDigest, review.ModelDiffDigest, review.CostDiffDigest) || !oneOf(review.Status, "pending", "trial", "activated", "rolled_back") {
			return nil, ErrSTRIDEWorkforceInvalid
		}
		if _, exists := runtime.updates[review.Proposal.Header.ID]; exists {
			return nil, ErrSTRIDEWorkforceInvalid
		}
		runtime.updates[review.Proposal.Header.ID] = cloneSTRIDEUpdateReview(review)
	}
	for _, canary := range snapshot.Canaries {
		if !validSTRIDEWorkforceCanary(canary) || canary.Status == "qualified" && canary.Synthetic {
			return nil, ErrSTRIDEWorkforceInvalid
		}
		seat, exists := runtime.seats[canary.SeatID]
		if !exists || seat.Status == "unavailable" {
			return nil, ErrSTRIDEWorkforceInvalid
		}
		if _, exists := runtime.canaries[canary.SeatID]; exists {
			return nil, ErrSTRIDEWorkforceInvalid
		}
		runtime.canaries[canary.SeatID] = canary
	}
	runtime.snapshotGeneration = snapshot.Generation
	return runtime, nil
}

func (runtime *STRIDEWorkforceRuntime) transition(actor STRIDEWorkforceActor, action, agentID, key string, now time.Time, before, after string) (STRIDEWorkforceReceipt, error) {
	return runtime.transitionAny(actor, action, agentID, key, now, []string{before}, after)
}
func (runtime *STRIDEWorkforceRuntime) transitionAny(actor STRIDEWorkforceActor, action, agentID, key string, now time.Time, befores []string, after string) (STRIDEWorkforceReceipt, error) {
	if runtime == nil || actor.Validate() != nil || !actor.IsAdmin {
		return STRIDEWorkforceReceipt{}, ErrSTRIDEAdminRequired
	}
	if !strideIdentifier(agentID) || !strideIdentifier(key) || now.IsZero() {
		return STRIDEWorkforceReceipt{}, ErrSTRIDEWorkforceInvalid
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if receipt, found, err := runtime.idempotentLocked(action, key, agentID); found || err != nil {
		if err != nil {
			return STRIDEWorkforceReceipt{}, err
		}
		return receipt, nil
	}
	seat, found := runtime.seats[agentID]
	if !found {
		return STRIDEWorkforceReceipt{}, ErrSTRIDEWorkforceUnknown
	}
	if !oneOf(seat.Status, befores...) {
		return STRIDEWorkforceReceipt{}, ErrSTRIDEWorkforceState
	}
	before := seat.Status
	seat.Status = after
	seat.UpdatedAt = now.UTC()
	if after == "quarantined" || after == "paused" {
		seat.AccessRevoked = true
	}
	runtime.seats[agentID] = seat
	receipt := newSTRIDEWorkforceReceipt(action, key, agentID, before, after, now)
	runtime.receipts[receipt.Action+":"+receipt.IdempotencyKey] = receipt
	return receipt, nil
}
func (runtime *STRIDEWorkforceRuntime) idempotentLocked(action, key, agentID string) (STRIDEWorkforceReceipt, bool, error) {
	receipt, found := runtime.receipts[action+":"+key]
	if !found {
		return STRIDEWorkforceReceipt{}, false, nil
	}
	if receipt.AgentID != agentID {
		return STRIDEWorkforceReceipt{}, true, ErrSTRIDEWorkforceIdempotency
	}
	return receipt, true, nil
}
func newSTRIDEWorkforceReceipt(action, key, agentID, before, after string, at time.Time) STRIDEWorkforceReceipt {
	value := STRIDEWorkforceReceipt{Action: action, IdempotencyKey: key, AgentID: agentID, Before: before, After: after, At: at.UTC()}
	value.Digest = mustSTRIDEWorkforceDigest(action + "|" + key + "|" + agentID + "|" + before + "|" + after + "|" + value.At.Format(time.RFC3339Nano))
	return value
}
func validSTRIDEWorkforceReceipt(receipt STRIDEWorkforceReceipt) bool {
	if !strideIdentifier(receipt.Action) || !strideIdentifier(receipt.IdempotencyKey) || !strideIdentifier(receipt.AgentID) || receipt.At.IsZero() || receipt.At.Location() != time.UTC || !isHexDigest(receipt.Digest) {
		return false
	}
	if receipt.Action == "create" {
		if !isHexDigest(receipt.RequestDigest) || receipt.Before != "" || receipt.After != "draft_hire" {
			return false
		}
	} else if receipt.RequestDigest != "" {
		return false
	}
	want := newSTRIDEWorkforceReceipt(receipt.Action, receipt.IdempotencyKey, receipt.AgentID, receipt.Before, receipt.After, receipt.At)
	return receipt.Digest == want.Digest
}

func validSTRIDEWorkforceCanary(manifest STRIDEWorkforceCanaryManifest) bool {
	return strideIdentifier(manifest.SeatID) && strideIdentifier(manifest.RouteDescriptionID) && manifest.OneSeat && !(manifest.ProfileChange && manifest.RouteChange) && oneOf(manifest.Status, "draft", "evaluated", "qualified")
}
func replayValidSTRIDEWorkforceLifecycle(seats map[string]STRIDEWorkforceSeat, receipts []STRIDEWorkforceReceipt) bool {
	grouped := make(map[string][]STRIDEWorkforceReceipt)
	for _, receipt := range receipts {
		if _, exists := seats[receipt.AgentID]; !exists {
			return false
		}
		grouped[receipt.AgentID] = append(grouped[receipt.AgentID], receipt)
	}
	for id, seat := range seats {
		if seat.Status == "unavailable" {
			if len(grouped[id]) != 0 || !validSTRIDEUnavailableFixture(seat) {
				return false
			}
			continue
		}
		events := append([]STRIDEWorkforceReceipt(nil), grouped[id]...)
		sort.Slice(events, func(i, j int) bool {
			if !events[i].At.Equal(events[j].At) {
				return events[i].At.Before(events[j].At)
			}
			left, right := strideWorkforceActionOrder(events[i].Action), strideWorkforceActionOrder(events[j].Action)
			if left != right {
				return left < right
			}
			return events[i].Digest < events[j].Digest
		})
		status, stage, accessRevoked := "", "", false
		var createdAt, updatedAt time.Time
		var offboardedAt *time.Time
		for _, receipt := range events {
			switch receipt.Action {
			case "create":
				if status != "" || receipt.Before != "" || receipt.After != "draft_hire" {
					return false
				}
				status, stage, accessRevoked = "draft_hire", "identity", true
				createdAt, updatedAt = receipt.At, receipt.At
			case "trial":
				if status != "draft_hire" || receipt.Before != status || receipt.After != "trial_pending" {
					return false
				}
				status, updatedAt = "trial_pending", receipt.At
			case "hire":
				if status != "trial_pending" || receipt.Before != status || receipt.After != "trial_active" {
					return false
				}
				status, updatedAt = "trial_active", receipt.At
			case "activate_identity", "activate_capability", "activate_profile", "activate_route":
				wanted := strings.TrimPrefix(receipt.Action, "activate_")
				if status != "trial_active" || stage != wanted || receipt.Before != "trial_active" {
					return false
				}
				switch wanted {
				case "identity":
					stage = "capability"
				case "capability":
					stage = "profile"
				case "profile":
					stage = "route"
				case "route":
					stage, status = "review", "review_required"
				}
				if receipt.After != status {
					return false
				}
				updatedAt = receipt.At
			case "review":
				if status != "review_required" || stage != "review" || !accessRevoked || receipt.Before != status || receipt.After != "active" {
					return false
				}
				stage, status, accessRevoked, updatedAt = "complete", "active", false, receipt.At
			case "pause":
				if !oneOf(status, "trial_pending", "trial_active", "review_required", "active", "quarantined") || receipt.Before != status || receipt.After != "paused" {
					return false
				}
				status, accessRevoked, updatedAt = "paused", true, receipt.At
			case "quarantine":
				if !oneOf(status, "draft_hire", "trial_pending", "trial_active", "review_required", "active", "paused") || receipt.Before != status || receipt.After != "quarantined" {
					return false
				}
				status, accessRevoked, updatedAt = "quarantined", true, receipt.At
			case "offboard":
				if status == "" || status == "unavailable" || status == "offboarded" || receipt.Before != status || receipt.After != "offboarded" {
					return false
				}
				at := receipt.At
				status, accessRevoked, updatedAt, offboardedAt = "offboarded", true, at, &at
			case "export":
				if status == "" || receipt.Before != status || receipt.After != status {
					return false
				}
			default:
				return false
			}
		}
		if status == "" || seat.Status != status || seat.ActivationStage != stage || seat.AccessRevoked != accessRevoked || !seat.CreatedAt.Equal(createdAt) || !seat.UpdatedAt.Equal(updatedAt) {
			return false
		}
		if (seat.OffboardedAt == nil) != (offboardedAt == nil) || seat.OffboardedAt != nil && !seat.OffboardedAt.Equal(*offboardedAt) {
			return false
		}
	}
	return true
}

func strideWorkforceActionOrder(action string) int {
	switch action {
	case "create":
		return 0
	case "trial":
		return 10
	case "hire":
		return 20
	case "activate_identity":
		return 30
	case "activate_capability":
		return 40
	case "activate_profile":
		return 50
	case "activate_route":
		return 60
	case "review":
		return 65
	case "pause":
		return 70
	case "quarantine":
		return 80
	case "offboard":
		return 90
	case "export":
		return 100
	default:
		return 1000
	}
}

func validSTRIDEUnavailableFixture(seat STRIDEWorkforceSeat) bool {
	if !oneOf(seat.ID, "fixture_insights", "fixture_mary_marketing", "fixture_research", "fixture_design", "fixture_builder") {
		return false
	}
	return seat.OrgIdentity == "org_agent:"+seat.ID && seat.DirectThread == "thread_"+seat.ID && seat.AccessRevoked && seat.ActivationStage == "" && seat.Owner == "" && len(seat.Memberships) == 0 && seat.CreatedAt.IsZero() && seat.UpdatedAt.IsZero() && seat.OffboardedAt == nil
}
func validSTRIDEWorkforceSeat(seat STRIDEWorkforceSeat) bool {
	if !strideIdentifier(seat.ID) || !strideIdentifier(seat.OrgIdentity) || !strideIdentifier(seat.DirectThread) || !oneOf(seat.Status, "draft_hire", "trial_pending", "trial_active", "review_required", "active", "paused", "quarantined", "offboarded", "unavailable") {
		return false
	}
	if seat.Status == "unavailable" {
		return validSTRIDEUnavailableFixture(seat)
	}
	return seat.Package.Validate() == nil && seat.Listing.Validate() == nil && seat.Capability.Validate() == nil && seat.Route.Validate() == nil && strideIdentifier(seat.Owner) && uniqueSTRIDEIDs(seat.Memberships) && seat.Concurrency > 0 && oneOf(seat.Proactivity, "disabled", "quiet") && !seat.CreatedAt.IsZero() && seat.CreatedAt.Location() == time.UTC && !seat.UpdatedAt.IsZero() && seat.UpdatedAt.Location() == time.UTC && !seat.UpdatedAt.Before(seat.CreatedAt)
}
func cloneSTRIDEWorkforceSeat(seat STRIDEWorkforceSeat) STRIDEWorkforceSeat {
	seat.Overlay = cloneSTRIDEReference(seat.Overlay)
	seat.Memberships = append([]string(nil), seat.Memberships...)
	if seat.OffboardedAt != nil {
		at := *seat.OffboardedAt
		seat.OffboardedAt = &at
	}
	return seat
}
func cloneSTRIDEUpdateReview(review STRIDEUpdateReview) STRIDEUpdateReview {
	review.AppliedStages = append([]string(nil), review.AppliedStages...)
	return review
}
func strideMarketplaceListingReference(listing MarketplaceListing) STRIDEReference {
	return STRIDEReference{ContractType: STRIDEContractMarketplaceListing, ID: listing.Header.ID, Revision: listing.Header.Revision, Digest: listing.Header.ContentDigest}
}
func sensitiveWorkforceLearning(value string) bool {
	value = strings.ToLower(value)
	for _, forbidden := range []string{"health", "medical", "race", "religion", "politic", "sexual", "union", "disability", "biometric", "credential", "secret"} {
		if strings.Contains(value, forbidden) {
			return true
		}
	}
	return false
}
func containsSTRIDEReference(values []STRIDEReference, wanted STRIDEReference) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func mustSTRIDEWorkforceDigest(value any) string {
	digest, _ := STRIDEContractDigest(value)
	return digest
}
