package main

// This file is the product/control-plane boundary for in-meeting specialists.
// Browser and native clients may only inspect server-derived eligibility and
// record revision-bound human choices. The separately configured production
// joiner owns the post-approval transition; its application default is off.

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrMeetingSpecialistProductDisabled = errors.New("meeting specialist product path is disabled")
	ErrMeetingSpecialistProductScope    = errors.New("meeting specialist room scope is unauthorized")
	ErrMeetingSpecialistProductAgent    = errors.New("meeting specialist is not eligible")
	ErrMeetingSpecialistProductRevision = errors.New("meeting specialist invitation revision mismatch")
	ErrMeetingSpecialistProductDecision = errors.New("meeting specialist invitation decision is invalid")
	ErrMeetingSpecialistProductRestore  = errors.New("meeting specialist state restore failed")
)

type MeetingSpecialistCandidate struct {
	AgentID                 string           `json:"agentId"`
	DisplayName             string           `json:"displayName"`
	Profile                 STRIDEReference  `json:"profile"`
	Capability              STRIDEReference  `json:"capability"`
	Assignment              *STRIDEReference `json:"assignment,omitempty"`
	Eligibility             *STRIDEReference `json:"eligibility,omitempty"`
	RoomID                  string           `json:"roomId,omitempty"`
	ProductAgentRevision    int64            `json:"productAgentRevision,omitempty"`
	WorkforceRevisionDigest string           `json:"workforceRevisionDigest,omitempty"`
}

type meetingSpecialistProductScope struct {
	TenantID              string
	RoomID                string
	SittingID             string
	MediaGeneration       uint64
	RequesterPrincipal    string
	Audience              STRIDEAudience
	ConsentPolicyRevision STRIDEReference
	ConsentFences         []ConsentFence
}

type MeetingSpecialistProductAuthority interface {
	ResolveScope(context.Context, *userAccount, string) (meetingSpecialistProductScope, error)
	ResolveControlScope(context.Context, *userAccount, string) (meetingSpecialistProductScope, error)
	EligibleRoster(context.Context, meetingSpecialistProductScope) ([]MeetingSpecialistCandidate, error)
	ScopeCurrent(context.Context, meetingSpecialistProductScope) error
}

// MeetingSpecialistApprovalLimits are the hard, human-visible ceilings bound
// to one invitation. A provider launch may use less, never more.
type MeetingSpecialistApprovalLimits struct {
	TimeBudgetSeconds    int64 `json:"timeBudgetSeconds"`
	TurnBudget           int   `json:"turnBudget"`
	MaxFloorLeaseSeconds int64 `json:"maxFloorLeaseSeconds"`
	AudioBudgetSeconds   int64 `json:"audioBudgetSeconds"`
	TokenBudget          int64 `json:"tokenBudget"`
	CostBudgetCents      int64 `json:"costBudgetCents"`
}

func defaultMeetingSpecialistApprovalLimits() MeetingSpecialistApprovalLimits {
	return MeetingSpecialistApprovalLimits{TimeBudgetSeconds: 120, TurnBudget: 3, MaxFloorLeaseSeconds: 20, AudioBudgetSeconds: 45, TokenBudget: 1500, CostBudgetCents: 25}
}

func (limits MeetingSpecialistApprovalLimits) validate(invitation MeetingAgentInvitation) error {
	if limits.TimeBudgetSeconds <= 0 || limits.TimeBudgetSeconds > invitation.ExpectedTimeSeconds || limits.TurnBudget <= 0 || limits.MaxFloorLeaseSeconds <= 0 || limits.MaxFloorLeaseSeconds > limits.TimeBudgetSeconds || limits.AudioBudgetSeconds <= 0 || limits.AudioBudgetSeconds > limits.TimeBudgetSeconds || limits.TokenBudget <= 0 || limits.CostBudgetCents < 0 || limits.CostBudgetCents > invitation.ExpectedCostCents {
		return ErrMeetingSpecialistProductDecision
	}
	return nil
}

type MeetingSpecialistProductConfig struct {
	Enabled              bool
	TenantID             string
	Now                  func() time.Time
	ControlCurrent       func() bool
	ControlCheckInterval time.Duration
	Authority            MeetingSpecialistProductAuthority
	Persistence          *MeetingSpecialistProductPersistence
	ProductionJoin       *MeetingSpecialistProductionJoiner
}

type meetingSpecialistProductRecord struct {
	Invitation                 MeetingAgentInvitation             `json:"invitation"`
	Agent                      MeetingSpecialistCandidate         `json:"agent"`
	PurposeSummary             string                             `json:"purposeSummary"`
	Limits                     MeetingSpecialistApprovalLimits    `json:"limits"`
	Status                     string                             `json:"status"`
	UpdatedAt                  time.Time                          `json:"updatedAt"`
	QualificationSubjectDigest string                             `json:"qualificationSubjectDigest,omitempty"`
	QualificationResult        *StoredTrustedQualificationResult  `json:"qualificationResult,omitempty"`
	TerminalEvidence           *MeetingSpecialistTerminalEvidence `json:"terminalEvidence,omitempty"`
	Scope                      meetingSpecialistProductScope      `json:"-"`
	Runtime                    *MeetingSpecialistRuntime          `json:"-"`
}

type MeetingSpecialistProduct struct {
	mu                          sync.Mutex
	enabled                     bool
	now                         func() time.Time
	authority                   MeetingSpecialistProductAuthority
	invitations                 map[string]meetingSpecialistProductRecord
	tenantID                    string
	persistence                 *MeetingSpecialistProductPersistence
	generation                  uint64
	healthErr                   error
	controlCurrent              func() bool
	controlMonitorStop          chan struct{}
	controlMonitorDone          chan struct{}
	controlMonitorStopOnce      sync.Once
	productionJoin              *MeetingSpecialistProductionJoiner
	unsubscribeConsentDecisions func()
}

type meetingSpecialistRuntimeRevocation struct {
	runtime *MeetingSpecialistRuntime
	reason  string
}

func newMeetingSpecialistRuntimeRevocation(runtime *MeetingSpecialistRuntime, reason string) meetingSpecialistRuntimeRevocation {
	if runtime != nil {
		runtime.FenceGates()
	}
	return meetingSpecialistRuntimeRevocation{runtime: runtime, reason: reason}
}

func revokeMeetingSpecialistRuntimes(revocations []meetingSpecialistRuntimeRevocation) error {
	if len(revocations) == 0 {
		return nil
	}
	results := make(chan error, len(revocations))
	var wait sync.WaitGroup
	for _, revocation := range revocations {
		if revocation.runtime == nil {
			continue
		}
		wait.Add(1)
		go func(revocation meetingSpecialistRuntimeRevocation) {
			defer wait.Done()
			results <- revocation.runtime.RevokeGates(revocation.reason)
		}(revocation)
	}
	wait.Wait()
	close(results)
	var result error
	for err := range results {
		result = errors.Join(result, err)
	}
	return result
}

func revokeMeetingSpecialistRuntimeList(runtimes []*MeetingSpecialistRuntime, reason string) error {
	revocations := make([]meetingSpecialistRuntimeRevocation, 0, len(runtimes))
	for _, runtime := range runtimes {
		if runtime != nil {
			revocations = append(revocations, newMeetingSpecialistRuntimeRevocation(runtime, reason))
		}
	}
	return revokeMeetingSpecialistRuntimes(revocations)
}

func NewMeetingSpecialistProduct(config MeetingSpecialistProductConfig) *MeetingSpecialistProduct {
	if config.Now == nil {
		config.Now = time.Now
	}
	if strings.TrimSpace(config.TenantID) == "" {
		config.TenantID = canonicalTenantID()
	}
	product := &MeetingSpecialistProduct{enabled: config.Enabled, now: config.Now, controlCurrent: config.ControlCurrent, authority: config.Authority, tenantID: config.TenantID, invitations: map[string]meetingSpecialistProductRecord{}, productionJoin: config.ProductionJoin}
	product.initializePersistence(config.Persistence)
	if product.enabled && product.controlCurrent != nil {
		interval := config.ControlCheckInterval
		if interval <= 0 {
			interval = time.Second
		}
		product.controlMonitorStop = make(chan struct{})
		product.controlMonitorDone = make(chan struct{})
		go product.monitorControlAuthority(interval)
	}
	return product
}

// monitorControlAuthority bounds how long a joined specialist may continue
// after its short-lived control receipt expires or is removed. Request-time
// readiness checks remain defense in depth; provider/audio teardown no longer
// depends on another HTTP or control-plane call arriving.
func (product *MeetingSpecialistProduct) monitorControlAuthority(interval time.Duration) {
	defer close(product.controlMonitorDone)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-product.controlMonitorStop:
			return
		case <-ticker.C:
			if !product.controlCurrent() {
				product.disableExpiredControl()
				return
			}
		}
	}
}

func (product *MeetingSpecialistProduct) stopControlMonitor() {
	if product == nil || product.controlMonitorStop == nil {
		return
	}
	product.controlMonitorStopOnce.Do(func() { close(product.controlMonitorStop) })
	<-product.controlMonitorDone
}

type MeetingSpecialistProductStatus struct {
	Available   bool                              `json:"available"`
	CanInvite   bool                              `json:"canInvite"`
	Reason      string                            `json:"reason,omitempty"`
	RoomID      string                            `json:"roomId,omitempty"`
	SittingID   string                            `json:"sittingId,omitempty"`
	Candidates  []MeetingSpecialistCandidate      `json:"candidates"`
	Invitations []meetingSpecialistInvitationView `json:"invitations"`
}

type meetingSpecialistInvitationView struct {
	ID                     string                             `json:"id"`
	Revision               int64                              `json:"revision"`
	AgentID                string                             `json:"agentId"`
	DisplayName            string                             `json:"displayName"`
	PurposeSummary         string                             `json:"purposeSummary"`
	ContextClasses         []string                           `json:"contextClasses"`
	Audience               STRIDEAudience                     `json:"audience"`
	ExpectedTimeSeconds    int64                              `json:"expectedTimeSeconds"`
	ExpectedCostCents      int64                              `json:"expectedCostCents"`
	HardLimits             MeetingSpecialistApprovalLimits    `json:"hardLimits"`
	Decision               string                             `json:"decision"`
	Status                 string                             `json:"status"`
	ProviderSessionStarted bool                               `json:"providerSessionStarted"`
	ExpiresAt              time.Time                          `json:"expiresAt"`
	UpdatedAt              time.Time                          `json:"updatedAt"`
	TerminalEvidence       *MeetingSpecialistTerminalEvidence `json:"terminalEvidence,omitempty"`
}

func (product *MeetingSpecialistProduct) Status(ctx context.Context, user *userAccount, roomID string) MeetingSpecialistProductStatus {
	status := MeetingSpecialistProductStatus{Candidates: []MeetingSpecialistCandidate{}, Invitations: []meetingSpecialistInvitationView{}}
	operational, restoreFailed := product.readiness()
	if !operational {
		status.Reason = "feature_disabled"
		if restoreFailed {
			status.Reason = "state_restore_failed"
		}
		return status
	}
	scope, err := product.authority.ResolveScope(ctx, user, roomID)
	if err != nil {
		status.Reason = meetingSpecialistProductReason(err)
		return status
	}
	if !meetingSpecialistMembersOnlyScope(scope) {
		_ = product.RevokeScopeAuthority(scope.RoomID, scope.SittingID, "guest_participant")
		status.Reason = "active_member_room_required"
		return status
	}
	status.RoomID, status.SittingID = scope.RoomID, scope.SittingID
	candidates, err := product.authority.EligibleRoster(ctx, scope)
	if err != nil {
		product.mu.Lock()
		changed := false
		persistFailed := false
		var revocations []meetingSpecialistRuntimeRevocation
		for id, record := range product.invitations {
			if record.Invitation.RoomID == scope.RoomID && record.Invitation.SittingID == scope.SittingID && meetingSpecialistInvitationRequiresEligibility(record) {
				if revocation := product.revokeEligibilityLocked(id, record, product.now().UTC()); revocation.runtime != nil {
					revocations = append(revocations, revocation)
				}
				changed = true
			}
		}
		if changed {
			if persistErr := product.persistLocked(); persistErr != nil {
				revocations = append(revocations, product.failClosedLocked(persistErr)...)
				persistFailed = true
			}
		}
		product.mu.Unlock()
		_ = revokeMeetingSpecialistRuntimes(revocations)
		if persistFailed {
			status.Reason = "state_restore_failed"
			return status
		}
		status.Reason = meetingSpecialistProductReason(err)
		return status
	}
	status.Candidates = append(status.Candidates, candidates...)
	sort.Slice(status.Candidates, func(i, j int) bool { return status.Candidates[i].AgentID < status.Candidates[j].AgentID })
	if operational, latestRestoreFailed := product.readiness(); !operational {
		status.Candidates = []MeetingSpecialistCandidate{}
		status.Reason = "feature_disabled"
		if latestRestoreFailed {
			status.Reason = "state_restore_failed"
		}
		return status
	}
	product.mu.Lock()
	if !product.enabled {
		restoreFailed = product.healthErr != nil
		product.mu.Unlock()
		status.Candidates = []MeetingSpecialistCandidate{}
		status.Reason = "feature_disabled"
		if restoreFailed {
			status.Reason = "state_restore_failed"
		}
		return status
	}
	now := product.now().UTC()
	eligibilityChanged, revocations, expirationErr := product.expireInvitationsLocked(now)
	if expirationErr != nil {
		revocations = append(revocations, product.failClosedLocked(expirationErr)...)
		product.mu.Unlock()
		_ = revokeMeetingSpecialistRuntimes(revocations)
		status.Candidates = []MeetingSpecialistCandidate{}
		status.Invitations = []meetingSpecialistInvitationView{}
		status.Reason = "state_restore_failed"
		return status
	}
	for id, record := range product.invitations {
		if record.Invitation.RoomID != scope.RoomID || record.Invitation.SittingID != scope.SittingID {
			continue
		}
		// A restored approval is deliberately inert: it has no runtime and cannot
		// be reused or approved in place. Participant reconnects and consent-fence
		// remints therefore leave it as reauthorization-required, while any
		// Product/Workforce candidate revision still revokes it terminally.
		scopeChanged := meetingSpecialistInvitationRequiresCurrentScope(record) && !sameMeetingSpecialistSittingScope(record.Scope, scope)
		candidateChanged := meetingSpecialistInvitationRequiresEligibility(record) && !currentMeetingSpecialistCandidate(candidates, record.Agent, record.Invitation)
		if scopeChanged || candidateChanged {
			if revocation := product.revokeEligibilityLocked(id, record, now); revocation.runtime != nil {
				revocations = append(revocations, revocation)
			}
			record = product.invitations[id]
			eligibilityChanged = true
		}
		status.Invitations = append(status.Invitations, meetingSpecialistView(record))
	}
	persistFailed := false
	if eligibilityChanged {
		if err := product.persistLocked(); err != nil {
			revocations = append(revocations, product.failClosedLocked(err)...)
			persistFailed = true
		}
	}
	product.mu.Unlock()
	// revokeEligibilityLocked deliberately detaches live runtimes before the
	// durable write. A persistence failure must not return early and strand
	// those providers outside product ownership; teardown is authority-critical
	// even when the fail-closed state itself cannot be persisted.
	_ = revokeMeetingSpecialistRuntimes(revocations)
	if persistFailed {
		status.Candidates = []MeetingSpecialistCandidate{}
		status.Invitations = []meetingSpecialistInvitationView{}
		status.Reason = "state_restore_failed"
		return status
	}
	sort.Slice(status.Invitations, func(i, j int) bool { return status.Invitations[i].UpdatedAt.Before(status.Invitations[j].UpdatedAt) })
	// Roster discovery and human approval are usable without a model call. The
	// joining transition is available only when every server-side dependency is
	// explicitly configured.
	status.CanInvite = len(status.Candidates) > 0
	status.Available = status.CanInvite && product.productionJoin != nil && product.productionJoin.Ready()
	if !status.CanInvite {
		status.Reason = "no_eligible_specialists"
	} else if !status.Available {
		status.Reason = "provider_qualification_pending"
	}
	return status
}

func (product *MeetingSpecialistProduct) Request(ctx context.Context, user *userAccount, roomID, agentID, purpose, idempotencyKey string, ttl time.Duration) (meetingSpecialistInvitationView, error) {
	if operational, _ := product.readiness(); !operational {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductDisabled
	}
	scope, err := product.authority.ResolveScope(ctx, user, roomID)
	if err != nil {
		return meetingSpecialistInvitationView{}, err
	}
	if !meetingSpecialistMembersOnlyScope(scope) {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductScope
	}
	candidates, err := product.authority.EligibleRoster(ctx, scope)
	if err != nil {
		return meetingSpecialistInvitationView{}, err
	}
	var candidate MeetingSpecialistCandidate
	for _, value := range candidates {
		if value.AgentID == strings.TrimSpace(agentID) {
			candidate = value
			break
		}
	}
	if !validMeetingSpecialistCandidateForRoom(candidate, scope.RoomID) {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductAgent
	}
	purpose, idempotencyKey = strings.TrimSpace(purpose), strings.TrimSpace(idempotencyKey)
	if purpose == "" || !strideIdentifier(idempotencyKey) {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductDecision
	}
	if ttl <= 0 || ttl > 10*time.Minute {
		ttl = 5 * time.Minute
	}
	now := product.now().UTC()
	scopeDigest, err := meetingSpecialistProductScopeDigest(scope)
	if err != nil {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductScope
	}
	idempotencyDigest := sha256Hex([]byte(scope.TenantID + "\x00" + scope.RoomID + "\x00" + scope.SittingID + "\x00" + scope.RequesterPrincipal + "\x00" + scopeDigest + "\x00" + idempotencyKey))
	if operational, _ := product.readiness(); !operational {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductDisabled
	}
	product.mu.Lock()
	var deferredRevocations []meetingSpecialistRuntimeRevocation
	defer func() {
		product.mu.Unlock()
		_ = revokeMeetingSpecialistRuntimes(deferredRevocations)
	}()
	if !product.enabled {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductDisabled
	}
	// Reauthorize after acquiring the invitation ledger lock. Canonical
	// Product/Workforce mutations dispatch their observer only after releasing
	// STRIDERuntime.mu, so this final read is deadlock-free and closes the race
	// where a request resolved an old roster immediately before a mutation.
	if err := product.authority.ScopeCurrent(ctx, scope); err != nil {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductScope
	}
	currentRoster, err := product.authority.EligibleRoster(ctx, scope)
	if err != nil {
		return meetingSpecialistInvitationView{}, err
	}
	var currentCandidateValue MeetingSpecialistCandidate
	for _, value := range currentRoster {
		if value.AgentID == candidate.AgentID {
			currentCandidateValue = value
			break
		}
	}
	if !sameMeetingSpecialistCandidate(candidate, currentCandidateValue) || !validMeetingSpecialistCandidateForRoom(currentCandidateValue, scope.RoomID) {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductAgent
	}
	candidates = currentRoster
	candidate = currentCandidateValue
	now = product.now().UTC()
	if changed, revocations, expirationErr := product.expireInvitationsLocked(now); expirationErr != nil {
		deferredRevocations = append(deferredRevocations, revocations...)
		deferredRevocations = append(deferredRevocations, product.failClosedLocked(expirationErr)...)
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductRestore
	} else if changed {
		deferredRevocations = append(deferredRevocations, revocations...)
		if err := product.persistLocked(); err != nil {
			deferredRevocations = append(deferredRevocations, product.failClosedLocked(err)...)
			return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductRestore
		}
	}
	for _, existing := range product.invitations {
		if existing.Invitation.IdempotencyKeyDigest == idempotencyDigest {
			if !sameMeetingSpecialistCandidate(existing.Agent, candidate) || existing.Invitation.PurposeDigest != sha256Hex([]byte(purpose)) {
				return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductDecision
			}
			return meetingSpecialistView(existing), nil
		}
	}
	for id, existing := range product.invitations {
		if existing.Invitation.RoomID == scope.RoomID && existing.Invitation.SittingID == scope.SittingID && meetingSpecialistInvitationIsActive(existing) {
			if !sameMeetingSpecialistSittingScope(existing.Scope, scope) {
				if existing.Runtime != nil {
					deferredRevocations = append(deferredRevocations, newMeetingSpecialistRuntimeRevocation(existing.Runtime, "meeting_authority_changed"))
					existing.Runtime = nil
				}
				existing.Status, existing.UpdatedAt = "eligibility_revoked", now
				product.invitations[id] = existing
				continue
			}
			if !currentMeetingSpecialistCandidate(candidates, existing.Agent, existing.Invitation) {
				if existing.Runtime != nil {
					deferredRevocations = append(deferredRevocations, newMeetingSpecialistRuntimeRevocation(existing.Runtime, "eligibility_revoked"))
					existing.Runtime = nil
				}
				existing.Status, existing.UpdatedAt = "eligibility_revoked", now
				product.invitations[id] = existing
				continue
			}
			if existing.Agent.AgentID != candidate.AgentID {
				return meetingSpecialistInvitationView{}, ErrMeetingAgentFloorOccupied
			}
			if sameMeetingSpecialistCandidate(existing.Agent, candidate) {
				return meetingSpecialistView(existing), nil
			}
			if existing.Runtime != nil {
				deferredRevocations = append(deferredRevocations, newMeetingSpecialistRuntimeRevocation(existing.Runtime, "eligibility_revoked"))
				existing.Runtime = nil
			}
			existing.Status, existing.UpdatedAt = "eligibility_revoked", now
			product.invitations[id] = existing
		}
	}
	id := "specialist_invitation_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	invitation := MeetingAgentInvitation{
		Header: STRIDEContractHeader{TenantID: scope.TenantID, ID: id, Revision: 1, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractMeetingAgentInvitation, CreatedAt: now},
		RoomID: scope.RoomID, SittingID: scope.SittingID, SpecialistProfile: candidate.Profile, Capability: candidate.Capability, Eligibility: cloneSTRIDEReference(candidate.Eligibility),
		Requester: scope.RequesterPrincipal, EligibleConfirmer: scope.RequesterPrincipal, PurposeDigest: sha256Hex([]byte(purpose)),
		ContextClasses: []string{"meeting_transcript", "meeting_analysis", "company_brain", "active_work"}, SourceIntervalDigest: sha256Hex([]byte(scope.SittingID + ":current")),
		Audience: scope.Audience, ConsentPolicyRevision: scope.ConsentPolicyRevision, ExpectedTimeSeconds: 120, ExpectedCostCents: 25,
		ExpiresAt: now.Add(ttl), Decision: "requested", IdempotencyKeyDigest: idempotencyDigest,
	}
	invitation.Header.ContentDigest, err = meetingSpecialistInvitationDigest(invitation)
	if err != nil || invitation.Validate() != nil {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductDecision
	}
	limits := defaultMeetingSpecialistApprovalLimits()
	if limits.validate(invitation) != nil {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductDecision
	}
	record := meetingSpecialistProductRecord{Invitation: invitation, Agent: candidate, PurposeSummary: trimForStorage(purpose, 240), Limits: limits, Status: "awaiting_approval", UpdatedAt: now, Scope: cloneMeetingSpecialistProductScope(scope)}
	product.invitations[id] = record
	if err := product.persistLocked(); err != nil {
		delete(product.invitations, id)
		deferredRevocations = append(deferredRevocations, product.failClosedLocked(err)...)
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductRestore
	}
	return meetingSpecialistView(record), nil
}

func (product *MeetingSpecialistProduct) Resolve(ctx context.Context, user *userAccount, roomID, invitationID string, revision int64, decision string) (meetingSpecialistInvitationView, error) {
	if operational, _ := product.readiness(); !operational {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductDisabled
	}
	invitationID, decision = strings.TrimSpace(invitationID), strings.TrimSpace(decision)
	if decision != "approved" && decision != "declined" && decision != "dismissed" {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductDecision
	}
	var scope meetingSpecialistProductScope
	var err error
	if decision == "approved" {
		scope, err = product.authority.ResolveScope(ctx, user, roomID)
	} else {
		scope, err = product.authority.ResolveControlScope(ctx, user, roomID)
	}
	if err != nil {
		return meetingSpecialistInvitationView{}, err
	}
	if decision == "approved" && !meetingSpecialistMembersOnlyScope(scope) {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductScope
	}
	if operational, _ := product.readiness(); !operational {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductDisabled
	}
	product.mu.Lock()
	var deferredRevocations []meetingSpecialistRuntimeRevocation
	defer func() {
		product.mu.Unlock()
		_ = revokeMeetingSpecialistRuntimes(deferredRevocations)
	}()
	if !product.enabled {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductDisabled
	}
	now := product.now().UTC()
	if changed, revocations, expirationErr := product.expireInvitationsLocked(now); expirationErr != nil {
		deferredRevocations = append(deferredRevocations, revocations...)
		deferredRevocations = append(deferredRevocations, product.failClosedLocked(expirationErr)...)
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductRestore
	} else if changed {
		deferredRevocations = append(deferredRevocations, revocations...)
		if err := product.persistLocked(); err != nil {
			deferredRevocations = append(deferredRevocations, product.failClosedLocked(err)...)
			return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductRestore
		}
	}
	record, found := product.invitations[invitationID]
	if !found || record.Invitation.RoomID != scope.RoomID || record.Invitation.SittingID != scope.SittingID || record.Invitation.EligibleConfirmer != scope.RequesterPrincipal || (decision == "approved" && !sameMeetingSpecialistProductScope(record.Scope, scope)) {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductScope
	}
	if record.Invitation.Header.Revision != revision {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductRevision
	}
	if record.Status == "expired" {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductDecision
	}
	if decision == "approved" && (!now.Before(record.Invitation.ExpiresAt) || product.authority.ScopeCurrent(ctx, scope) != nil) {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductScope
	}
	if decision == "approved" {
		for id, existing := range product.invitations {
			if id != invitationID && existing.Invitation.RoomID == record.Invitation.RoomID && existing.Invitation.SittingID == record.Invitation.SittingID && meetingSpecialistInvitationIsActive(existing) {
				return meetingSpecialistInvitationView{}, ErrMeetingAgentFloorOccupied
			}
		}
		roster, rosterErr := product.authority.EligibleRoster(ctx, scope)
		if rosterErr != nil || !currentMeetingSpecialistCandidate(roster, record.Agent, record.Invitation) {
			return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductAgent
		}
	}
	requestedDecision := record.Status == "awaiting_approval" && record.Invitation.Decision == "requested"
	approvedDismissal := decision == "dismissed" && record.Invitation.Decision == "approved" && oneOf(record.Status, "approved_waiting_for_provider_qualification", "approved_reauthorization_required", "joined_session")
	if scope.RequesterPrincipal != record.Invitation.EligibleConfirmer || !requestedDecision && !approvedDismissal {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductDecision
	}
	record.Invitation.Header.Revision++
	record.Invitation.Decision = decision
	record.Invitation.DecisionPrincipal = scope.RequesterPrincipal
	record.Invitation.DecisionAt = &now
	record.Invitation.Header.CreatedAt = now
	record.UpdatedAt = now
	switch decision {
	case "approved":
		// Waiting approval state owns no runtime or provider factory. The single
		// production joiner path below may create one only after qualification.
		record.Runtime = nil
		record.Status = "approved_waiting_for_provider_qualification"
	case "declined":
		record.Status = "declined"
	case "dismissed":
		if record.Runtime != nil {
			deferredRevocations = append(deferredRevocations, newMeetingSpecialistRuntimeRevocation(record.Runtime, "dismissed"))
		}
		record.Runtime = nil
		record.Status = "dismissed"
	}
	record.Invitation.Header.ContentDigest, err = meetingSpecialistInvitationDigest(record.Invitation)
	if err != nil || record.Invitation.Validate() != nil {
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductDecision
	}
	product.invitations[invitationID] = record
	// The approved decision must be durable before any session factory or Brief
	// can run. A persistence failure therefore leaves zero launched providers.
	if err := product.persistLocked(); err != nil {
		deferredRevocations = append(deferredRevocations, product.failClosedLocked(err)...)
		return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductRestore
	}
	joinAttempted := false
	var runtime *MeetingSpecialistRuntime
	var joinErr error
	if decision == "approved" && product.productionJoin != nil && product.productionJoin.Enabled() {
		joinAttempted = true
		runtime, joinErr = product.productionJoin.Join(ctx, MeetingSpecialistJoinRequest{Invitation: record.Invitation, Candidate: record.Agent, Scope: scope, Limits: record.Limits})
	}
	if joinAttempted {
		joinFailureReason := "production_join_failed"
		if joinErr == nil && runtime != nil && runtime.Snapshot().Session != nil {
			if !validMeetingSpecialistQualificationResult(runtime.qualificationResult, runtime.qualificationSubjectDigest) {
				joinErr = ErrMeetingSpecialistJoinQualification
			}
			// Product scope is checked again after the potentially slow launch.
			// Runtime capability authority independently performs the same checks
			// around provider creation and briefing.
			if joinErr == nil {
				joinErr = product.authority.ScopeCurrent(ctx, scope)
			}
			if joinErr == nil {
				roster, rosterErr := product.authority.EligibleRoster(ctx, scope)
				if rosterErr != nil || !currentMeetingSpecialistCandidate(roster, record.Agent, record.Invitation) {
					joinErr = ErrMeetingSpecialistProductAgent
				}
			}
		}
		if joinErr != nil || runtime == nil || runtime.Snapshot().Session == nil {
			if runtime != nil {
				deferredRevocations = append(deferredRevocations, newMeetingSpecialistRuntimeRevocation(runtime, joinFailureReason))
			}
			record.Runtime = nil
			record.Status = "approved_session_failed"
		} else {
			if !runtime.BindTerminalObserver(func(evidence MeetingSpecialistTerminalEvidence) {
				product.recordRuntimeTerminal(invitationID, runtime, evidence)
			}) {
				deferredRevocations = append(deferredRevocations, newMeetingSpecialistRuntimeRevocation(runtime, joinFailureReason))
				record.Runtime = nil
				record.Status = "approved_session_failed"
			} else {
				record.Runtime = runtime
				record.QualificationSubjectDigest = runtime.qualificationSubjectDigest
				record.QualificationResult = cloneMeetingSpecialistQualificationResult(runtime.qualificationResult)
				record.Status = "joined_session"
			}
		}
		record.UpdatedAt = product.now().UTC()
		product.invitations[invitationID] = record
		if err := product.persistLocked(); err != nil {
			if record.Runtime != nil {
				deferredRevocations = append(deferredRevocations, newMeetingSpecialistRuntimeRevocation(record.Runtime, "post_launch_persistence_failed"))
				record.Runtime = nil
				record.Status = "approved_session_failed"
				product.invitations[invitationID] = record
			}
			deferredRevocations = append(deferredRevocations, product.failClosedLocked(err)...)
			return meetingSpecialistInvitationView{}, ErrMeetingSpecialistProductRestore
		}
	}
	return meetingSpecialistView(record), nil
}

// recordRuntimeTerminal reconciles an autonomous provider/deadline failure
// back into the product ledger. Product-originated revocations detach the
// runtime first, so their observer callback is an idempotent no-op.
func (product *MeetingSpecialistProduct) recordRuntimeTerminal(invitationID string, runtime *MeetingSpecialistRuntime, evidence MeetingSpecialistTerminalEvidence) {
	if product == nil || runtime == nil {
		return
	}
	if !validMeetingSpecialistTerminalEvidence(&evidence) {
		product.mu.Lock()
		revocations := product.failClosedLocked(ErrMeetingSpecialistProductRestore)
		product.mu.Unlock()
		_ = revokeMeetingSpecialistRuntimes(revocations)
		return
	}
	product.mu.Lock()
	record, found := product.invitations[invitationID]
	ownedRuntime := found && record.Runtime == runtime
	if !found || !ownedRuntime && (record.Runtime != nil || record.TerminalEvidence != nil || meetingSpecialistInvitationIsActive(record)) {
		product.mu.Unlock()
		return
	}
	if !validMeetingSpecialistTerminalEvidenceForLimits(&evidence, record.Limits) || evidence.QualificationSubjectDigest != record.QualificationSubjectDigest || !sameMeetingSpecialistQualificationResult(evidence.QualificationResult, record.QualificationResult) {
		revocations := product.failClosedLocked(ErrMeetingSpecialistProductRestore)
		product.mu.Unlock()
		_ = revokeMeetingSpecialistRuntimes(revocations)
		return
	}
	if ownedRuntime {
		record.Runtime = nil
		switch evidence.TerminalReason {
		case "expired":
			expired, err := expireMeetingSpecialistRecord(record, evidence.EndedAt)
			if err != nil {
				revocations := product.failClosedLocked(err)
				product.mu.Unlock()
				_ = revokeMeetingSpecialistRuntimes(revocations)
				return
			}
			record = expired
		case "failed":
			record.Status = "failed"
			record.UpdatedAt = evidence.EndedAt.UTC()
		default:
			record.Status = "closed"
			record.UpdatedAt = evidence.EndedAt.UTC()
		}
	}
	terminalEvidence := evidence
	record.TerminalEvidence = &terminalEvidence
	if record.UpdatedAt.Before(evidence.EndedAt) {
		record.UpdatedAt = evidence.EndedAt.UTC()
	}
	product.invitations[invitationID] = record
	var revocations []meetingSpecialistRuntimeRevocation
	var persistErr error
	if product.enabled {
		persistErr = product.persistLocked()
	} else if product.healthErr == nil {
		// Close and control-expiry disable admission before provider teardown.
		// Terminal evidence may append to that already-terminal ledger without
		// reopening the product or accepting another invitation.
		persistErr = product.persistTerminalLocked()
	}
	if persistErr != nil {
		revocations = append(revocations, product.failClosedLocked(persistErr)...)
	}
	product.mu.Unlock()
	_ = revokeMeetingSpecialistRuntimes(revocations)
}

func (product *MeetingSpecialistProduct) Close(reason string) {
	if product == nil {
		return
	}
	product.stopControlMonitor()
	product.mu.Lock()
	wasEnabled := product.enabled
	revocations := make([]meetingSpecialistRuntimeRevocation, 0)
	for id, record := range product.invitations {
		if record.Runtime != nil {
			revocations = append(revocations, newMeetingSpecialistRuntimeRevocation(record.Runtime, reason))
			record.Runtime = nil
		}
		if wasEnabled && (record.Invitation.Decision == "approved" || record.Invitation.Decision == "requested") {
			record.Status = "closed"
			record.UpdatedAt = product.now().UTC()
			product.invitations[id] = record
		}
	}
	if wasEnabled {
		if err := product.persistLocked(); err != nil {
			revocations = append(revocations, product.failClosedLocked(err)...)
		}
	}
	// Close is terminal even when persistence succeeded. A stopped control
	// monitor must never be followed by a newly admitted invitation/runtime.
	product.enabled = false
	unsubscribe := product.unsubscribeConsentDecisions
	product.unsubscribeConsentDecisions = nil
	product.mu.Unlock()
	_ = revokeMeetingSpecialistRuntimes(revocations)
	if unsubscribe != nil {
		unsubscribe()
	}
}

func (product *MeetingSpecialistProduct) CloseScope(roomID, sittingID, reason string) {
	if product == nil {
		return
	}
	product.mu.Lock()
	if !product.enabled {
		product.mu.Unlock()
		return
	}
	revocations := make([]meetingSpecialistRuntimeRevocation, 0)
	for id, record := range product.invitations {
		if record.Invitation.RoomID != normalizeRoomID(roomID) || record.Invitation.SittingID != strings.TrimSpace(sittingID) {
			continue
		}
		if record.Runtime != nil {
			revocations = append(revocations, newMeetingSpecialistRuntimeRevocation(record.Runtime, reason))
			record.Runtime = nil
		}
		if record.Invitation.Decision == "approved" || record.Invitation.Decision == "requested" {
			record.Status = "closed"
			record.UpdatedAt = product.now().UTC()
			product.invitations[id] = record
		}
	}
	if err := product.persistLocked(); err != nil {
		revocations = append(revocations, product.failClosedLocked(err)...)
	}
	product.mu.Unlock()
	_ = revokeMeetingSpecialistRuntimes(revocations)
}

// RevokeAgentAuthority is the synchronous fail-closed hook for canonical
// Product/Workforce mutations. The authority owner calls it after releasing
// its own ledger locks, so a joined provider is closed before the mutation is
// reported complete and without introducing a Product<->runtime lock cycle.
func (product *MeetingSpecialistProduct) RevokeAgentAuthority(agentID, reason string) error {
	if product == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	if !strideIdentifier(agentID) {
		return ErrMeetingSpecialistProductAgent
	}
	product.mu.Lock()
	if !product.enabled {
		if product.healthErr != nil {
			product.mu.Unlock()
			return ErrMeetingSpecialistProductRestore
		}
		product.mu.Unlock()
		return nil
	}
	changed := false
	now := product.now().UTC()
	runtimes := make([]*MeetingSpecialistRuntime, 0)
	for id, record := range product.invitations {
		if record.Agent.AgentID != agentID || !meetingSpecialistInvitationRequiresEligibility(record) {
			continue
		}
		if record.Runtime != nil {
			record.Runtime.FenceGates()
			runtimes = append(runtimes, record.Runtime)
			record.Runtime = nil
		}
		record.Status, record.UpdatedAt = "eligibility_revoked", now
		product.invitations[id] = record
		changed = true
	}
	if !changed {
		product.mu.Unlock()
		return nil
	}
	if err := product.persistLocked(); err != nil {
		failClosedRevocations := product.failClosedLocked(err)
		product.mu.Unlock()
		return errors.Join(ErrMeetingSpecialistProductRestore, revokeMeetingSpecialistRuntimeList(runtimes, reason), revokeMeetingSpecialistRuntimes(failClosedRevocations))
	}
	product.mu.Unlock()
	return revokeMeetingSpecialistRuntimeList(runtimes, reason)
}

// RevokeScopeAuthority is the synchronous counterpart for meeting participant
// and consent-generation churn. An empty sittingID intentionally revokes every
// active invitation in the room; callers should pass the exact sitting when it
// is available.
func (product *MeetingSpecialistProduct) RevokeScopeAuthority(roomID, sittingID, reason string) error {
	if product == nil {
		return nil
	}
	roomID, sittingID = normalizeRoomID(roomID), strings.TrimSpace(sittingID)
	if !strideIdentifier(roomID) {
		return ErrMeetingSpecialistProductScope
	}
	product.mu.Lock()
	if !product.enabled {
		if product.healthErr != nil {
			product.mu.Unlock()
			return ErrMeetingSpecialistProductRestore
		}
		product.mu.Unlock()
		return nil
	}
	changed := false
	now := product.now().UTC()
	runtimes := make([]*MeetingSpecialistRuntime, 0)
	for id, record := range product.invitations {
		if record.Invitation.RoomID != roomID || sittingID != "" && record.Invitation.SittingID != sittingID || !meetingSpecialistInvitationRequiresCurrentScope(record) {
			continue
		}
		if record.Runtime != nil {
			record.Runtime.FenceGates()
			runtimes = append(runtimes, record.Runtime)
			record.Runtime = nil
		}
		record.Status, record.UpdatedAt = "eligibility_revoked", now
		product.invitations[id] = record
		changed = true
	}
	if !changed {
		product.mu.Unlock()
		return nil
	}
	if err := product.persistLocked(); err != nil {
		failClosedRevocations := product.failClosedLocked(err)
		product.mu.Unlock()
		return errors.Join(ErrMeetingSpecialistProductRestore, revokeMeetingSpecialistRuntimeList(runtimes, reason), revokeMeetingSpecialistRuntimes(failClosedRevocations))
	}
	product.mu.Unlock()
	return revokeMeetingSpecialistRuntimeList(runtimes, reason)
}

func meetingSpecialistView(record meetingSpecialistProductRecord) meetingSpecialistInvitationView {
	providerStarted := record.Runtime != nil && record.Runtime.Snapshot().Session != nil
	return meetingSpecialistInvitationView{
		ID: record.Invitation.Header.ID, Revision: record.Invitation.Header.Revision, AgentID: record.Agent.AgentID, DisplayName: record.Agent.DisplayName,
		PurposeSummary: record.PurposeSummary, ContextClasses: append([]string(nil), record.Invitation.ContextClasses...), Audience: cloneAudience(record.Invitation.Audience),
		ExpectedTimeSeconds: record.Invitation.ExpectedTimeSeconds, ExpectedCostCents: record.Invitation.ExpectedCostCents, HardLimits: record.Limits,
		Decision: record.Invitation.Decision, Status: record.Status, ProviderSessionStarted: providerStarted, ExpiresAt: record.Invitation.ExpiresAt, UpdatedAt: record.UpdatedAt,
		TerminalEvidence: cloneMeetingSpecialistTerminalEvidence(record.TerminalEvidence),
	}
}

func meetingSpecialistInvitationIsActive(record meetingSpecialistProductRecord) bool {
	switch record.Status {
	case "awaiting_approval":
		return record.Invitation.Decision == "requested"
	case "approved_waiting_for_provider_qualification", "joined_session":
		return record.Invitation.Decision == "approved"
	default:
		return false
	}
}

// expireInvitationsLocked makes invitation time authority durable before a
// stale idempotency replay, a fresh request, or an approval can observe it.
// Runtime gates are fenced synchronously while product.mu is held; provider
// teardown is completed by the caller after releasing that mutex.
func (product *MeetingSpecialistProduct) expireInvitationsLocked(now time.Time) (bool, []meetingSpecialistRuntimeRevocation, error) {
	changed := false
	revocations := make([]meetingSpecialistRuntimeRevocation, 0)
	for id, record := range product.invitations {
		if !meetingSpecialistInvitationIsActive(record) || now.Before(record.Invitation.ExpiresAt) {
			continue
		}
		expired, err := expireMeetingSpecialistRecord(record, now)
		if err != nil {
			return changed, revocations, err
		}
		if record.Runtime != nil {
			revocations = append(revocations, newMeetingSpecialistRuntimeRevocation(record.Runtime, "expired"))
			expired.Runtime = nil
		}
		product.invitations[id] = expired
		changed = true
	}
	return changed, revocations, nil
}

func expireMeetingSpecialistRecord(record meetingSpecialistProductRecord, now time.Time) (meetingSpecialistProductRecord, error) {
	now = now.UTC()
	record.Invitation.Header.Revision++
	record.Invitation.Header.CreatedAt = now
	record.Invitation.Decision = "expired"
	record.Invitation.DecisionPrincipal = ""
	record.Invitation.DecisionAt = &now
	digest, err := meetingSpecialistInvitationDigest(record.Invitation)
	if err != nil {
		return meetingSpecialistProductRecord{}, err
	}
	record.Invitation.Header.ContentDigest = digest
	if err := record.Invitation.Validate(); err != nil {
		return meetingSpecialistProductRecord{}, err
	}
	record.Status = "expired"
	record.UpdatedAt = now
	return record, nil
}

func cloneMeetingSpecialistTerminalEvidence(evidence *MeetingSpecialistTerminalEvidence) *MeetingSpecialistTerminalEvidence {
	if evidence == nil {
		return nil
	}
	cloned := *evidence
	cloned.QualificationResult = cloneMeetingSpecialistQualificationResult(evidence.QualificationResult)
	return &cloned
}

func cloneMeetingSpecialistQualificationResult(result *StoredTrustedQualificationResult) *StoredTrustedQualificationResult {
	if result == nil {
		return nil
	}
	cloned := *result
	return &cloned
}

func sameMeetingSpecialistQualificationResult(left, right *StoredTrustedQualificationResult) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameMeetingSpecialistTerminalEvidence(left, right *MeetingSpecialistTerminalEvidence) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftValue, rightValue := *left, *right
	leftValue.QualificationResult = nil
	rightValue.QualificationResult = nil
	return leftValue == rightValue && sameMeetingSpecialistQualificationResult(left.QualificationResult, right.QualificationResult)
}

func meetingSpecialistInvitationRequiresEligibility(record meetingSpecialistProductRecord) bool {
	return oneOf(record.Status, "awaiting_approval", "approved_waiting_for_provider_qualification", "approved_reauthorization_required", "joined_session")
}

// approved_reauthorization_required is already a zero-authority tombstone for
// a formerly approved session. It remains bound to the candidate revision for
// truthful history, but it holds no live meeting-scope authority to revoke.
// A fresh invitation is required against the current participant and consent
// scope before a specialist can join again.
func meetingSpecialistInvitationRequiresCurrentScope(record meetingSpecialistProductRecord) bool {
	return oneOf(record.Status, "awaiting_approval", "approved_waiting_for_provider_qualification", "joined_session")
}

func (product *MeetingSpecialistProduct) revokeEligibilityLocked(id string, record meetingSpecialistProductRecord, now time.Time) meetingSpecialistRuntimeRevocation {
	return product.revokeEligibilityWithReasonLocked(id, record, now, "eligibility_revoked")
}

func (product *MeetingSpecialistProduct) revokeEligibilityWithReasonLocked(id string, record meetingSpecialistProductRecord, now time.Time, reason string) meetingSpecialistRuntimeRevocation {
	revocation := newMeetingSpecialistRuntimeRevocation(record.Runtime, reason)
	if record.Runtime != nil {
		if strings.TrimSpace(reason) == "" {
			reason = "eligibility_revoked"
			revocation.reason = reason
		}
		record.Runtime = nil
	}
	record.Status = "eligibility_revoked"
	record.UpdatedAt = now.UTC()
	product.invitations[id] = record
	return revocation
}

func (product *MeetingSpecialistProduct) readiness() (operational, restoreFailed bool) {
	if product == nil {
		return false, false
	}
	product.mu.Lock()
	enabled, authorized, restoreFailed, current := product.enabled, product.authority != nil, product.healthErr != nil, product.controlCurrent
	product.mu.Unlock()
	if !enabled || !authorized {
		return false, restoreFailed
	}
	if current != nil && !current() {
		product.disableExpiredControl()
		product.mu.Lock()
		restoreFailed = product.healthErr != nil
		product.mu.Unlock()
		return false, restoreFailed
	}
	return true, restoreFailed
}

func (product *MeetingSpecialistProduct) disableExpiredControl() {
	if product == nil {
		return
	}
	product.mu.Lock()
	if !product.enabled {
		product.mu.Unlock()
		return
	}
	now := product.now().UTC()
	revocations := make([]meetingSpecialistRuntimeRevocation, 0)
	for id, record := range product.invitations {
		if record.Runtime != nil {
			revocations = append(revocations, newMeetingSpecialistRuntimeRevocation(record.Runtime, "control_authority_expired"))
			record.Runtime = nil
		}
		if meetingSpecialistInvitationIsActive(record) {
			record.Status = "closed"
			record.UpdatedAt = now
		}
		product.invitations[id] = record
	}
	if err := product.persistLocked(); err != nil {
		revocations = append(revocations, product.failClosedLocked(err)...)
	} else {
		// Persist the terminal records while the product is still permitted to
		// write, then disable admission. persistLocked intentionally rejects a
		// disabled product, so reversing this order makes every normal receipt
		// expiry look like store corruption and leaves stale durable authority.
		product.enabled = false
	}
	product.mu.Unlock()
	_ = revokeMeetingSpecialistRuntimes(revocations)
}

func meetingSpecialistInvitationDigest(invitation MeetingAgentInvitation) (string, error) {
	invitation.Header.ContentDigest = ""
	return STRIDEContractDigest(invitation)
}

func sameMeetingSpecialistProductScope(left, right meetingSpecialistProductScope) bool {
	if left.TenantID != right.TenantID || left.RoomID != right.RoomID || left.SittingID != right.SittingID || left.MediaGeneration != right.MediaGeneration ||
		left.RequesterPrincipal != right.RequesterPrincipal || left.ConsentPolicyRevision != right.ConsentPolicyRevision || left.Audience.Visibility != right.Audience.Visibility || len(left.Audience.Principals) != len(right.Audience.Principals) || !sameMeetingSpecialistConsentFences(left.ConsentFences, right.ConsentFences) {
		return false
	}
	for index := range left.Audience.Principals {
		if left.Audience.Principals[index] != right.Audience.Principals[index] {
			return false
		}
	}
	return true
}

// sameMeetingSpecialistSittingScope compares shared room authority without
// treating the current viewer/requester as part of that authority revision.
// Requester identity remains exact for invitation confirmation and
// idempotency; another authorized member may inspect or request in the same
// sitting without revoking the existing specialist.
func sameMeetingSpecialistSittingScope(left, right meetingSpecialistProductScope) bool {
	left.RequesterPrincipal = ""
	right.RequesterPrincipal = ""
	return sameMeetingSpecialistProductScope(left, right)
}

func meetingSpecialistMembersOnlyScope(scope meetingSpecialistProductScope) bool {
	memberPrefix := string(ACLPrincipalUser) + ":"
	if !strings.HasPrefix(scope.RequesterPrincipal, memberPrefix) || !meetingSpecialistMembersOnlyAudience(scope.Audience) {
		return false
	}
	for _, fence := range scope.ConsentFences {
		if fence.binding.PrincipalKind != ACLPrincipalUser {
			return false
		}
	}
	return true
}

func meetingSpecialistProductScopeDigest(scope meetingSpecialistProductScope) (string, error) {
	fences, err := meetingSpecialistConsentAuthorityDigests(scope.ConsentFences)
	if err != nil {
		return "", err
	}
	return STRIDEContractDigest(struct {
		TenantID              string          `json:"tenantId"`
		RoomID                string          `json:"roomId"`
		SittingID             string          `json:"sittingId"`
		MediaGeneration       uint64          `json:"mediaGeneration"`
		RequesterPrincipal    string          `json:"requesterPrincipal"`
		Audience              STRIDEAudience  `json:"audience"`
		ConsentPolicyRevision STRIDEReference `json:"consentPolicyRevision"`
		ConsentAuthority      []string        `json:"consentAuthority"`
	}{scope.TenantID, scope.RoomID, scope.SittingID, scope.MediaGeneration, scope.RequesterPrincipal, cloneAudience(scope.Audience), scope.ConsentPolicyRevision, fences})
}

func cloneMeetingSpecialistProductScope(scope meetingSpecialistProductScope) meetingSpecialistProductScope {
	scope.Audience = cloneAudience(scope.Audience)
	scope.ConsentFences = append([]ConsentFence(nil), scope.ConsentFences...)
	return scope
}

func sameMeetingSpecialistConsentFences(left, right []ConsentFence) bool {
	if len(left) != len(right) {
		return false
	}
	leftDigests, leftErr := meetingSpecialistConsentAuthorityDigests(left)
	rightDigests, rightErr := meetingSpecialistConsentAuthorityDigests(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	for index := range leftDigests {
		if leftDigests[index] != rightDigests[index] {
			return false
		}
	}
	return true
}

// meetingSpecialistConsentAuthorityDigests deliberately excludes issuedAt.
// Fence mint time remains in the signed audit snapshot and provider-facing
// fence validation, but it is not a consent authority revision: binding, lane,
// policy, generation, and the durable record digest are.
func meetingSpecialistConsentAuthorityDigests(values []ConsentFence) ([]string, error) {
	result := make([]string, 0, len(values))
	for _, fence := range values {
		digest, err := STRIDEContractDigest(struct {
			Binding      ConsentAdmissionBinding `json:"binding"`
			Lane         ConsentLane             `json:"lane"`
			Policy       string                  `json:"policy"`
			Generation   uint64                  `json:"generation"`
			RecordDigest string                  `json:"recordDigest"`
		}{fence.binding, fence.lane, fence.policy, fence.generation, fence.recordDigest})
		if err != nil {
			return nil, err
		}
		result = append(result, digest)
	}
	sort.Strings(result)
	return result, nil
}

func meetingSpecialistProductReason(err error) string {
	switch {
	case errors.Is(err, ErrMeetingSpecialistProductDisabled):
		return "feature_disabled"
	case errors.Is(err, ErrMeetingSpecialistProductAgent):
		return "no_eligible_specialists"
	case errors.Is(err, ErrConsentAuthorityUnavailable):
		return "consent_unavailable"
	default:
		return "active_member_room_required"
	}
}
