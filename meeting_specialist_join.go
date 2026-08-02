package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/openai/openai-realtime-meeting-assistant/internal/e10evidence"
)

var (
	ErrMeetingSpecialistJoinDisabled      = errors.New("meeting specialist production join is disabled")
	ErrMeetingSpecialistJoinAssembly      = errors.New("meeting specialist production join assembly is invalid")
	ErrMeetingSpecialistJoinQualification = errors.New("meeting specialist production qualification is not current")
)

const (
	meetingSpecialistQualificationTargetID = "meeting-specialist-provider-voice-evaluation"
	meetingSpecialistQualificationMaxAge   = 7 * 24 * time.Hour
	meetingSpecialistProviderName          = "openai"
	meetingSpecialistProviderRoute         = "openai-realtime-websocket"
)

// MeetingSpecialistQualificationRequest is the exact immutable candidate that
// an independently administered E10 authority must have qualified. The local
// application can bind and compare this request, but cannot qualify itself.
type MeetingSpecialistQualificationRequest struct {
	TenantID                   string
	ResultID                   string
	TargetID                   string
	EvaluatorConfigDigest      string
	EvaluatorResultDigest      string
	FixtureDigest              string
	QualificationSubjectDigest string
	Candidate                  e10evidence.CandidateBinding
	Binding                    e10evidence.MeetingSpecialistQualificationBinding
	SpecialistProfile          STRIDEReference
	SpecialistCapability       STRIDEReference
}

func (request MeetingSpecialistQualificationRequest) validate() error {
	if !strideIdentifier(request.TenantID) || !strideIdentifier(request.ResultID) || request.TargetID != meetingSpecialistQualificationTargetID ||
		!isHexDigest(request.EvaluatorConfigDigest) || !isHexDigest(request.EvaluatorResultDigest) || !isHexDigest(request.FixtureDigest) || !isHexDigest(request.QualificationSubjectDigest) ||
		!releaseCommitPattern.MatchString(request.Candidate.ReleaseCommit) || !isHexDigest(request.Candidate.GitTreeDigest) || !isHexDigest(request.Candidate.ImageDigest) || !isHexDigest(request.Candidate.ConfigDigest) || !isHexDigest(request.Candidate.RouteMapDigest) ||
		!strideIdentifier(request.Binding.Provider) || strings.TrimSpace(request.Binding.Model) == "" || strings.TrimSpace(request.Binding.Model) != request.Binding.Model || strings.TrimSpace(request.Binding.Voice) == "" || strings.TrimSpace(request.Binding.Voice) != request.Binding.Voice ||
		!isHexDigest(request.Binding.RouteDigest) || !isHexDigest(request.Binding.AccountingProfileDigest) || !isHexDigest(request.Binding.RuntimeProfileDigest) || !isHexDigest(request.Binding.CapabilityPolicyDigest) ||
		request.SpecialistProfile.Validate() != nil || !oneOf(string(request.SpecialistProfile.ContractType), string(STRIDEContractAgentCoreProfile), string(STRIDEContractAgentProfileOverlay)) ||
		request.SpecialistCapability.Validate() != nil || request.SpecialistCapability.ContractType != STRIDEContractAgentCapabilityManifest {
		return ErrMeetingSpecialistJoinQualification
	}
	subject := e10evidence.MeetingSpecialistQualificationFixtureDigest(request.Binding)
	if request.FixtureDigest != subject || request.QualificationSubjectDigest != subject || request.Candidate.RouteMapDigest != request.Binding.RouteDigest {
		return ErrMeetingSpecialistJoinQualification
	}
	if request.Binding.CapabilityPolicyDigest != meetingSpecialistCapabilityPolicyDigest(request.SpecialistProfile, request.SpecialistCapability) {
		return ErrMeetingSpecialistJoinQualification
	}
	return nil
}

// MeetingSpecialistQualificationSubjectDigest is the canonical public binding
// an external verifier signs and an evidence-store adapter compares. It is
// derived from every launch-relevant qualification field; callers must never
// accept a digest supplied alongside an echoed local configuration as proof.
func MeetingSpecialistQualificationSubjectDigest(request MeetingSpecialistQualificationRequest) (string, error) {
	if err := request.validate(); err != nil {
		return "", err
	}
	return e10evidence.MeetingSpecialistQualificationFixtureDigest(request.Binding), nil
}

// MeetingSpecialistQualificationDeployment supplies release-ledger identities
// that cannot be derived from process state. Provider, model, voice, route,
// accounting, runtime, and specialist-policy digests are deliberately absent:
// NewMeetingSpecialistQualifiedRealtimeFactory derives those from the actual
// normalized server configuration and exact specialist references.
type MeetingSpecialistQualificationDeployment struct {
	TenantID              string
	ResultID              string
	TargetID              string
	EvaluatorConfigDigest string
	EvaluatorResultDigest string
	FixtureDigest         string
	Candidate             e10evidence.CandidateBinding
	AccountingMode        MeetingSpecialistRealtimeInputMode
	SpecialistProfile     STRIDEReference
	SpecialistCapability  STRIDEReference
}

// MeetingSpecialistJoinRequest contains the durable human approval and the
// current server-authorized meeting/workforce facts. It is never decoded from
// an HTTP body. The product refreshes scope and roster authority immediately
// before constructing it and again after a potentially slow join.
type MeetingSpecialistJoinRequest struct {
	Invitation MeetingAgentInvitation
	Candidate  MeetingSpecialistCandidate
	Scope      meetingSpecialistProductScope
	Limits     MeetingSpecialistApprovalLimits
}

// MeetingSpecialistJoinAssembly is resolved by the server after approval.
// Session/principal/track identity, context/tool authority and floor/metering
// policy therefore cannot be supplied by the approving client. The joiner
// derives the remaining scope from MeetingSpecialistJoinRequest and validates
// the complete launch before a one-use capability is minted.
type MeetingSpecialistJoinAssembly struct {
	SessionID        string
	RuntimePrincipal string
	AudioTrackID     string
	Context          MeetingSpecialistContextEnvelope
	Policy           MeetingAgentFloorPolicy
}

type MeetingSpecialistJoinResolver func(context.Context, MeetingSpecialistJoinRequest) (MeetingSpecialistJoinAssembly, error)
type MeetingSpecialistCapabilityMinter func(context.Context, MeetingSpecialistCapabilityRequest) (string, error)

type MeetingSpecialistProductionJoinConfig struct {
	Enabled             bool
	Now                 func() time.Time
	Gates               MeetingSpecialistGates
	ResolveCurrent      MeetingSpecialistJoinResolver
	MintCapability      MeetingSpecialistCapabilityMinter
	CapabilityAuthority MeetingSpecialistCapabilityAuthority
	QualifiedProvider   *MeetingSpecialistQualifiedProviderFactory
	PublishAudio        MeetingSpecialistAudioPublisher
	QualificationStore  *QualificationEvidenceStore
}

// MeetingSpecialistProductionJoiner is the only product-to-runtime assembly
// path. Its zero value and the application default are hard off. Enabling it
// still requires all independently revocable runtime gates and every
// authority-bearing server dependency.
type MeetingSpecialistProductionJoiner struct {
	enabled             bool
	now                 func() time.Time
	gates               MeetingSpecialistGates
	resolveCurrent      MeetingSpecialistJoinResolver
	mintCapability      MeetingSpecialistCapabilityMinter
	capabilityAuthority MeetingSpecialistCapabilityAuthority
	qualifiedProvider   *MeetingSpecialistQualifiedProviderFactory
	publishAudio        MeetingSpecialistAudioPublisher
	qualificationStore  *QualificationEvidenceStore
}

func NewMeetingSpecialistProductionJoiner(config MeetingSpecialistProductionJoinConfig) *MeetingSpecialistProductionJoiner {
	if config.Now == nil {
		config.Now = time.Now
	}
	return &MeetingSpecialistProductionJoiner{
		enabled: config.Enabled, now: config.Now, gates: config.Gates,
		resolveCurrent: config.ResolveCurrent, mintCapability: config.MintCapability,
		capabilityAuthority: config.CapabilityAuthority, qualifiedProvider: config.QualifiedProvider,
		publishAudio: config.PublishAudio, qualificationStore: config.QualificationStore,
	}
}

func (joiner *MeetingSpecialistProductionJoiner) Enabled() bool {
	return joiner != nil && joiner.enabled
}

func (joiner *MeetingSpecialistProductionJoiner) Ready() bool {
	if joiner == nil || !joiner.Enabled() || !joiner.assemblyReady() {
		return false
	}
	_, err := joiner.qualificationCurrent(joiner.qualifiedProvider.request)
	return err == nil
}

func (joiner *MeetingSpecialistProductionJoiner) assemblyReady() bool {
	return joiner != nil && joiner.gates.launchEnabled() && joiner.resolveCurrent != nil && joiner.mintCapability != nil && joiner.capabilityAuthority != nil && joiner.qualifiedProvider != nil && joiner.publishAudio != nil
}

func (joiner *MeetingSpecialistProductionJoiner) qualificationCurrent(request MeetingSpecialistQualificationRequest) (StoredTrustedQualificationResult, error) {
	if joiner == nil || joiner.qualificationStore == nil || joiner.qualifiedProvider == nil || request != joiner.qualifiedProvider.request || request.validate() != nil {
		return StoredTrustedQualificationResult{}, ErrMeetingSpecialistJoinQualification
	}
	stored, err := joiner.qualificationStore.currentMeetingSpecialistQualification(request, joiner.now)
	if err != nil {
		return StoredTrustedQualificationResult{}, ErrMeetingSpecialistJoinQualification
	}
	return stored, nil
}

func (joiner *MeetingSpecialistProductionJoiner) Join(ctx context.Context, request MeetingSpecialistJoinRequest) (*MeetingSpecialistRuntime, error) {
	if !joiner.Enabled() {
		return nil, ErrMeetingSpecialistJoinDisabled
	}
	if !joiner.assemblyReady() {
		return nil, ErrMeetingSpecialistJoinAssembly
	}
	if joiner.qualificationStore == nil || joiner.qualifiedProvider == nil || joiner.qualifiedProvider.request.validate() != nil {
		return nil, ErrMeetingSpecialistJoinQualification
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := joiner.now().UTC()
	if request.Invitation.Validate() != nil || request.Invitation.Decision != "approved" || request.Invitation.DecisionAt == nil || !now.Before(request.Invitation.ExpiresAt) ||
		request.Limits.validate(request.Invitation) != nil || !meetingSpecialistMembersOnlyScope(request.Scope) ||
		request.Scope.RoomID != request.Invitation.RoomID || request.Scope.SittingID != request.Invitation.SittingID || request.Scope.RequesterPrincipal != request.Invitation.Requester ||
		request.Scope.ConsentPolicyRevision != request.Invitation.ConsentPolicyRevision || !sameMeetingSpecialistAudience(request.Scope.Audience, request.Invitation.Audience) ||
		!validMeetingSpecialistCandidateForRoom(request.Candidate, request.Scope.RoomID) ||
		request.Candidate.AgentID == "" || request.Candidate.Profile != request.Invitation.SpecialistProfile || request.Candidate.Capability != request.Invitation.Capability || request.Candidate.Eligibility == nil || request.Invitation.Eligibility == nil || *request.Candidate.Eligibility != *request.Invitation.Eligibility {
		return nil, ErrMeetingSpecialistJoinAssembly
	}
	qualificationRequest := joiner.qualifiedProvider.request
	if qualificationRequest.TenantID != request.Scope.TenantID || qualificationRequest.SpecialistProfile != request.Candidate.Profile || qualificationRequest.SpecialistCapability != request.Candidate.Capability {
		return nil, ErrMeetingSpecialistJoinQualification
	}
	if _, err := joiner.qualificationCurrent(qualificationRequest); err != nil {
		return nil, ErrMeetingSpecialistJoinQualification
	}
	assembly, err := joiner.resolveCurrent(ctx, request)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !strideIdentifier(strings.TrimSpace(assembly.SessionID)) || !strideIdentifier(strings.TrimSpace(assembly.RuntimePrincipal)) || !strideIdentifier(strings.TrimSpace(assembly.AudioTrackID)) {
		return nil, ErrMeetingSpecialistJoinAssembly
	}
	launch := MeetingSpecialistLaunch{
		Scope: MeetingAgentFloorScope{
			RoomID: request.Scope.RoomID, SittingID: request.Scope.SittingID, MediaGeneration: request.Scope.MediaGeneration,
			InvitationID: request.Invitation.Header.ID, SessionID: assembly.SessionID, AgentID: request.Candidate.AgentID,
			RuntimePrincipal: assembly.RuntimePrincipal, AudioTrackID: assembly.AudioTrackID,
		},
		Invitation: request.Invitation, Context: assembly.Context, Policy: assembly.Policy, ApprovalLimits: request.Limits,
	}
	// Tool authority is explicit. Even a read-only tool list cannot ride through
	// a launch whose independently revocable Tools gate is off.
	if len(launch.Context.ToolIDs) > 0 && !joiner.gates.Tools || launch.Validate(now) != nil {
		return nil, ErrMeetingSpecialistJoinAssembly
	}
	capabilityRequest := meetingSpecialistCapabilityRequest(launch)
	receipt, err := joiner.mintCapability(ctx, capabilityRequest)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !strideIdentifier(strings.TrimSpace(receipt)) {
		return nil, ErrMeetingSpecialistJoinAssembly
	}
	launch.CapabilityReceipt = receipt
	qualificationResult, err := joiner.qualificationCurrent(qualificationRequest)
	if err != nil {
		return nil, err
	}
	qualificationCurrent := func() error {
		_, currentErr := joiner.qualificationCurrent(qualificationRequest)
		return currentErr
	}
	runtime := newQualifiedMeetingSpecialistRuntime(joiner.now, joiner.gates, joiner.capabilityAuthority, joiner.qualifiedProvider, qualificationResult, qualificationCurrent, joiner.publishAudio)
	if _, err := runtime.Start(ctx, launch); err != nil {
		return runtime, err
	}
	return runtime, nil
}
