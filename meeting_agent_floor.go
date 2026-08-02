package main

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrMeetingAgentFloorInvalid    = errors.New("invalid meeting-agent floor request")
	ErrMeetingAgentFloorOccupied   = errors.New("meeting-agent seat or floor is occupied")
	ErrMeetingAgentFloorFence      = errors.New("meeting-agent floor fence mismatch")
	ErrMeetingAgentFloorExpired    = errors.New("meeting-agent floor lease expired")
	ErrMeetingAgentFloorBudget     = errors.New("meeting-agent floor budget exhausted")
	ErrMeetingAgentFloorAgentLoop  = errors.New("agent-originated turn chains are forbidden")
	ErrMeetingAgentFloorFeedback   = errors.New("agent audio cannot re-enter specialist input")
	ErrMeetingAgentFloorTerminated = errors.New("meeting-agent session is terminated")
)

// MeetingAgentFloorScope binds a specialist to one approved invitation and
// one room media generation. It deliberately contains no provider credential
// or raw context body.
type MeetingAgentFloorScope struct {
	RoomID           string `json:"roomId"`
	SittingID        string `json:"sittingId"`
	MediaGeneration  uint64 `json:"mediaGeneration"`
	InvitationID     string `json:"invitationId"`
	SessionID        string `json:"sessionId"`
	AgentID          string `json:"agentId"`
	RuntimePrincipal string `json:"runtimePrincipal"`
	AudioTrackID     string `json:"audioTrackId"`
}

func (scope MeetingAgentFloorScope) Validate() error {
	if !strideIdentifier(scope.RoomID) || !strideIdentifier(scope.SittingID) || scope.MediaGeneration == 0 ||
		!strideIdentifier(scope.InvitationID) || !strideIdentifier(scope.SessionID) || !strideIdentifier(scope.AgentID) ||
		!strideIdentifier(scope.RuntimePrincipal) || !strideIdentifier(scope.AudioTrackID) {
		return ErrMeetingAgentFloorInvalid
	}
	return nil
}

type MeetingAgentFloorPolicy struct {
	SessionTTL        time.Duration `json:"sessionTtl"`
	MaxFloorLease     time.Duration `json:"maxFloorLease"`
	TurnBudget        int           `json:"turnBudget"`
	AudioBudgetSecond int64         `json:"audioBudgetSeconds"`
	CostBudgetCents   int64         `json:"costBudgetCents"`
}

func (policy MeetingAgentFloorPolicy) Validate() error {
	if policy.SessionTTL <= 0 || policy.MaxFloorLease <= 0 || policy.MaxFloorLease > policy.SessionTTL ||
		policy.TurnBudget <= 0 || policy.AudioBudgetSecond <= 0 || policy.CostBudgetCents < 0 {
		return ErrMeetingAgentFloorInvalid
	}
	return nil
}

type MeetingAgentSessionLease struct {
	Scope      MeetingAgentFloorScope `json:"scope"`
	Generation uint64                 `json:"generation"`
	GrantedAt  time.Time              `json:"grantedAt"`
	ExpiresAt  time.Time              `json:"expiresAt"`
}

type MeetingAgentFloorLease struct {
	Session    MeetingAgentSessionLease `json:"session"`
	Generation uint64                   `json:"generation"`
	GrantedAt  time.Time                `json:"grantedAt"`
	ExpiresAt  time.Time                `json:"expiresAt"`
	Trigger    string                   `json:"trigger"`
}

type MeetingAgentFloorInterruption struct {
	SessionID       string    `json:"sessionId"`
	FloorGeneration uint64    `json:"floorGeneration"`
	Reason          string    `json:"reason"`
	InterruptedAt   time.Time `json:"interruptedAt"`
	CancelProvider  bool      `json:"cancelProvider"`
}

type MeetingAgentFloorSnapshot struct {
	Session               *MeetingAgentSessionLease `json:"session,omitempty"`
	Floor                 *MeetingAgentFloorLease   `json:"floor,omitempty"`
	TurnsRemaining        int                       `json:"turnsRemaining"`
	AudioSecondsRemaining int64                     `json:"audioSecondsRemaining"`
	CostCentsRemaining    int64                     `json:"costCentsRemaining"`
	InputHighWater        uint64                    `json:"inputHighWater"`
	OutputHighWater       uint64                    `json:"outputHighWater"`
	TerminalReason        string                    `json:"terminalReason,omitempty"`
	TeardownReceiptDigest string                    `json:"teardownReceiptDigest,omitempty"`
	HumanTrackIDs         []string                  `json:"humanTrackIds,omitempty"`
}

// MeetingAgentFloorController owns the meeting-agent seat and the much
// shorter speaking lease separately. A human interruption revokes only the
// speaking lease; dismissal/expiry tears down the entire specialist session.
// Human media is never owned or closed by this controller.
type MeetingAgentFloorController struct {
	mu  sync.Mutex
	now func() time.Time

	sessionGeneration uint64
	floorGeneration   uint64
	session           *meetingAgentSessionState
	lastTerminal      string
	lastReceipt       string
}

type meetingAgentSessionState struct {
	lease                 MeetingAgentSessionLease
	policy                MeetingAgentFloorPolicy
	floor                 *MeetingAgentFloorLease
	turnsRemaining        int
	audioSecondsRemaining int64
	costCentsRemaining    int64
	inputHighWater        uint64
	outputHighWater       uint64
	humanTracks           map[string]struct{}
}

func NewMeetingAgentFloorController(now func() time.Time) *MeetingAgentFloorController {
	if now == nil {
		now = time.Now
	}
	return &MeetingAgentFloorController{now: now}
}

func (controller *MeetingAgentFloorController) Admit(scope MeetingAgentFloorScope, policy MeetingAgentFloorPolicy) (MeetingAgentSessionLease, error) {
	if controller == nil || scope.Validate() != nil || policy.Validate() != nil {
		return MeetingAgentSessionLease{}, ErrMeetingAgentFloorInvalid
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	now := controller.now().UTC()
	controller.expireLocked(now)
	if controller.session != nil {
		return MeetingAgentSessionLease{}, ErrMeetingAgentFloorOccupied
	}
	controller.sessionGeneration++
	lease := MeetingAgentSessionLease{Scope: scope, Generation: controller.sessionGeneration, GrantedAt: now, ExpiresAt: now.Add(policy.SessionTTL)}
	controller.session = &meetingAgentSessionState{
		lease: lease, policy: policy, turnsRemaining: policy.TurnBudget,
		audioSecondsRemaining: policy.AudioBudgetSecond, costCentsRemaining: policy.CostBudgetCents,
		humanTracks: make(map[string]struct{}),
	}
	controller.lastTerminal, controller.lastReceipt = "", ""
	return lease, nil
}

func (controller *MeetingAgentFloorController) RequestFloor(session MeetingAgentSessionLease, trigger string, requested time.Duration) (MeetingAgentFloorLease, error) {
	if controller == nil || !allowedMeetingAgentFloorTrigger(trigger) || requested <= 0 {
		if strings.Contains(strings.ToLower(trigger), "agent") || strings.Contains(strings.ToLower(trigger), "specialist") {
			return MeetingAgentFloorLease{}, ErrMeetingAgentFloorAgentLoop
		}
		return MeetingAgentFloorLease{}, ErrMeetingAgentFloorInvalid
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	now := controller.now().UTC()
	controller.expireLocked(now)
	state, err := controller.sessionLocked(session, now)
	if err != nil {
		return MeetingAgentFloorLease{}, err
	}
	if state.floor != nil {
		return MeetingAgentFloorLease{}, ErrMeetingAgentFloorOccupied
	}
	if state.turnsRemaining <= 0 || state.audioSecondsRemaining <= 0 {
		return MeetingAgentFloorLease{}, ErrMeetingAgentFloorBudget
	}
	if requested > state.policy.MaxFloorLease {
		requested = state.policy.MaxFloorLease
	}
	if untilSessionExpiry := session.ExpiresAt.Sub(now); requested > untilSessionExpiry {
		requested = untilSessionExpiry
	}
	if requested <= 0 {
		return MeetingAgentFloorLease{}, ErrMeetingAgentFloorExpired
	}
	controller.floorGeneration++
	floor := MeetingAgentFloorLease{Session: session, Generation: controller.floorGeneration, GrantedAt: now, ExpiresAt: now.Add(requested), Trigger: trigger}
	state.floor = &floor
	state.turnsRemaining--
	return floor, nil
}

// AcceptHumanInput authorizes only a real human track. In particular the
// specialist's own verified output track, any other agent track, and mixed
// synthetic audio are rejected before they can reach a provider input buffer.
func (controller *MeetingAgentFloorController) AcceptHumanInput(session MeetingAgentSessionLease, sourceTrackID, sourceKind string) (uint64, error) {
	if controller == nil || !strideIdentifier(sourceTrackID) {
		return 0, ErrMeetingAgentFloorInvalid
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	now := controller.now().UTC()
	controller.expireLocked(now)
	state, err := controller.sessionLocked(session, now)
	if err != nil {
		return 0, err
	}
	if sourceTrackID == state.lease.Scope.AudioTrackID || sourceKind != "human" {
		return 0, ErrMeetingAgentFloorFeedback
	}
	state.humanTracks[sourceTrackID] = struct{}{}
	state.inputHighWater++
	return state.inputHighWater, nil
}

// AcceptProviderOutput fences every audio chunk by the active floor
// generation and designated verified track. costCents is the already-metered
// incremental charge; this controller never estimates provider prices.
func (controller *MeetingAgentFloorController) AcceptProviderOutput(floor MeetingAgentFloorLease, outputTrackID string, audioSeconds, costCents int64) (uint64, error) {
	if controller == nil || audioSeconds <= 0 || costCents < 0 || !strideIdentifier(outputTrackID) {
		return 0, ErrMeetingAgentFloorInvalid
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	now := controller.now().UTC()
	controller.expireLocked(now)
	state, err := controller.floorLocked(floor, now)
	if err != nil {
		return 0, err
	}
	if outputTrackID != state.lease.Scope.AudioTrackID {
		return 0, ErrMeetingAgentFloorFence
	}
	if audioSeconds > state.audioSecondsRemaining || costCents > state.costCentsRemaining {
		state.floor = nil
		controller.floorGeneration++
		return 0, ErrMeetingAgentFloorBudget
	}
	state.audioSecondsRemaining -= audioSeconds
	state.costCentsRemaining -= costCents
	state.outputHighWater++
	return state.outputHighWater, nil
}

// HumanBargeIn synchronously fences provider output before the caller sends a
// provider cancel. It never mutates, mutes, or closes any human media track.
func (controller *MeetingAgentFloorController) HumanBargeIn(roomID, sittingID string, mediaGeneration uint64) (MeetingAgentFloorInterruption, bool) {
	if controller == nil {
		return MeetingAgentFloorInterruption{}, false
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	now := controller.now().UTC()
	controller.expireLocked(now)
	state := controller.session
	if state == nil || state.floor == nil || state.lease.Scope.RoomID != roomID || state.lease.Scope.SittingID != sittingID || state.lease.Scope.MediaGeneration != mediaGeneration {
		return MeetingAgentFloorInterruption{}, false
	}
	interruption := MeetingAgentFloorInterruption{SessionID: state.lease.Scope.SessionID, FloorGeneration: state.floor.Generation, Reason: "human_barge_in", InterruptedAt: now, CancelProvider: true}
	state.floor = nil
	controller.floorGeneration++
	return interruption, true
}

// ReleaseFloor ends an ordinary specialist turn but retains the admitted
// session for a later explicit human follow-up.
func (controller *MeetingAgentFloorController) ReleaseFloor(floor MeetingAgentFloorLease) error {
	if controller == nil {
		return ErrMeetingAgentFloorInvalid
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	now := controller.now().UTC()
	controller.expireLocked(now)
	state, err := controller.floorLocked(floor, now)
	if err != nil {
		return err
	}
	state.floor = nil
	controller.floorGeneration++
	return nil
}

// Terminate is idempotent for the same session lease. The receipt is a
// body-free digest suitable for MeetingAgentSession.TeardownReceiptDigest.
func (controller *MeetingAgentFloorController) Terminate(session MeetingAgentSessionLease, reason string) (string, error) {
	if controller == nil || !allowedMeetingAgentTerminalReason(reason) {
		return "", ErrMeetingAgentFloorInvalid
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.session == nil {
		if controller.lastReceipt != "" && controller.lastTerminal == session.Scope.SessionID+"\x00"+reason {
			return controller.lastReceipt, nil
		}
		return "", ErrMeetingAgentFloorTerminated
	}
	state, err := controller.sessionLocked(session, controller.now().UTC())
	if err != nil && !errors.Is(err, ErrMeetingAgentFloorExpired) {
		return "", err
	}
	if state == nil {
		return "", ErrMeetingAgentFloorTerminated
	}
	receipt, digestErr := meetingAgentTerminalReceipt(state, reason)
	if digestErr != nil {
		return "", digestErr
	}
	controller.session = nil
	controller.sessionGeneration++
	controller.floorGeneration++
	controller.lastTerminal = session.Scope.SessionID + "\x00" + reason
	controller.lastReceipt = receipt
	return receipt, nil
}

func meetingAgentTerminalReceipt(state *meetingAgentSessionState, reason string) (string, error) {
	if state == nil || !allowedMeetingAgentTerminalReason(reason) {
		return "", ErrMeetingAgentFloorInvalid
	}
	return STRIDEContractDigest(struct {
		SessionID        string `json:"sessionId"`
		Generation       uint64 `json:"generation"`
		Reason           string `json:"reason"`
		InputHighWater   uint64 `json:"inputHighWater"`
		OutputHighWater  uint64 `json:"outputHighWater"`
		TurnsUsed        int    `json:"turnsUsed"`
		AudioSecondsUsed int64  `json:"audioSecondsUsed"`
		CostCentsUsed    int64  `json:"costCentsUsed"`
	}{
		SessionID: state.lease.Scope.SessionID, Generation: state.lease.Generation, Reason: reason,
		InputHighWater: state.inputHighWater, OutputHighWater: state.outputHighWater,
		TurnsUsed:        state.policy.TurnBudget - state.turnsRemaining,
		AudioSecondsUsed: state.policy.AudioBudgetSecond - state.audioSecondsRemaining,
		CostCentsUsed:    state.policy.CostBudgetCents - state.costCentsRemaining,
	})
}

func (controller *MeetingAgentFloorController) Snapshot() MeetingAgentFloorSnapshot {
	if controller == nil {
		return MeetingAgentFloorSnapshot{}
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.expireLocked(controller.now().UTC())
	if controller.session == nil {
		return MeetingAgentFloorSnapshot{TerminalReason: controller.lastTerminal, TeardownReceiptDigest: controller.lastReceipt}
	}
	state := controller.session
	session := state.lease
	var floor *MeetingAgentFloorLease
	if state.floor != nil {
		copy := *state.floor
		floor = &copy
	}
	tracks := make([]string, 0, len(state.humanTracks))
	for track := range state.humanTracks {
		tracks = append(tracks, track)
	}
	sort.Strings(tracks)
	return MeetingAgentFloorSnapshot{
		Session: &session, Floor: floor, TurnsRemaining: state.turnsRemaining,
		AudioSecondsRemaining: state.audioSecondsRemaining, CostCentsRemaining: state.costCentsRemaining,
		InputHighWater: state.inputHighWater, OutputHighWater: state.outputHighWater, HumanTrackIDs: tracks,
	}
}

func (controller *MeetingAgentFloorController) sessionLocked(lease MeetingAgentSessionLease, now time.Time) (*meetingAgentSessionState, error) {
	state := controller.session
	if state == nil {
		return nil, ErrMeetingAgentFloorTerminated
	}
	if !sameMeetingAgentSessionLease(state.lease, lease) {
		return nil, ErrMeetingAgentFloorFence
	}
	if !now.Before(state.lease.ExpiresAt) {
		return nil, ErrMeetingAgentFloorExpired
	}
	return state, nil
}

func (controller *MeetingAgentFloorController) floorLocked(lease MeetingAgentFloorLease, now time.Time) (*meetingAgentSessionState, error) {
	state, err := controller.sessionLocked(lease.Session, now)
	if err != nil {
		return nil, err
	}
	if state.floor == nil || state.floor.Generation != lease.Generation || !sameMeetingAgentSessionLease(state.floor.Session, lease.Session) {
		return nil, ErrMeetingAgentFloorFence
	}
	if !now.Before(state.floor.ExpiresAt) {
		state.floor = nil
		controller.floorGeneration++
		return nil, ErrMeetingAgentFloorExpired
	}
	return state, nil
}

func (controller *MeetingAgentFloorController) expireLocked(now time.Time) {
	if controller.session == nil {
		return
	}
	if !now.Before(controller.session.lease.ExpiresAt) {
		state := controller.session
		controller.lastTerminal = state.lease.Scope.SessionID + "\x00expired"
		controller.lastReceipt, _ = meetingAgentTerminalReceipt(state, "expired")
		controller.session = nil
		controller.sessionGeneration++
		controller.floorGeneration++
		return
	}
	if controller.session.floor != nil && !now.Before(controller.session.floor.ExpiresAt) {
		controller.session.floor = nil
		controller.floorGeneration++
	}
}

func sameMeetingAgentSessionLease(left, right MeetingAgentSessionLease) bool {
	return left.Generation == right.Generation && left.Scope == right.Scope && left.GrantedAt.Equal(right.GrantedAt) && left.ExpiresAt.Equal(right.ExpiresAt)
}

func allowedMeetingAgentFloorTrigger(trigger string) bool {
	switch trigger {
	case "explicit_human_request", "approved_scout_handoff", "human_followup":
		return true
	}
	return false
}

func allowedMeetingAgentTerminalReason(reason string) bool {
	switch reason {
	case "dismissed", "expired", "failed", "budget_exhausted", "consent_withdrawn", "invitation_revoked", "kill_switch", "room_closed",
		"agent_authority_changed", "meeting_authority_changed", "participant_authority_changed", "consent_authority_changed", "control_authority_expired",
		"eligibility_revoked", "guest_participant", "post_launch_persistence_failed", "provider_response_failed", "test_cleanup", "test_complete", "concurrent_revoke":
		return true
	}
	return false
}
