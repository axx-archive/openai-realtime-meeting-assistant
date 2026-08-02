package main

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	ErrMeetingSpecialistJoinDisabled = errors.New("meeting specialist production join is disabled")
	ErrMeetingSpecialistJoinAssembly = errors.New("meeting specialist production join assembly is invalid")
)

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
	ProviderFactory     MeetingSpecialistProviderFactory
	PublishAudio        MeetingSpecialistAudioPublisher
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
	providerFactory     MeetingSpecialistProviderFactory
	publishAudio        MeetingSpecialistAudioPublisher
}

func NewMeetingSpecialistProductionJoiner(config MeetingSpecialistProductionJoinConfig) *MeetingSpecialistProductionJoiner {
	if config.Now == nil {
		config.Now = time.Now
	}
	return &MeetingSpecialistProductionJoiner{
		enabled: config.Enabled, now: config.Now, gates: config.Gates,
		resolveCurrent: config.ResolveCurrent, mintCapability: config.MintCapability,
		capabilityAuthority: config.CapabilityAuthority, providerFactory: config.ProviderFactory,
		publishAudio: config.PublishAudio,
	}
}

func (joiner *MeetingSpecialistProductionJoiner) Enabled() bool {
	return joiner != nil && joiner.enabled
}

func (joiner *MeetingSpecialistProductionJoiner) Ready() bool {
	if !joiner.Enabled() || !joiner.gates.launchEnabled() || joiner.resolveCurrent == nil || joiner.mintCapability == nil || joiner.capabilityAuthority == nil || joiner.providerFactory == nil || joiner.publishAudio == nil {
		return false
	}
	return true
}

func (joiner *MeetingSpecialistProductionJoiner) Join(ctx context.Context, request MeetingSpecialistJoinRequest) (*MeetingSpecialistRuntime, error) {
	if !joiner.Enabled() {
		return nil, ErrMeetingSpecialistJoinDisabled
	}
	if !joiner.Ready() {
		return nil, ErrMeetingSpecialistJoinAssembly
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
	runtime := NewMeetingSpecialistRuntime(joiner.now, joiner.gates, joiner.capabilityAuthority, joiner.providerFactory, joiner.publishAudio)
	if _, err := runtime.Start(ctx, launch); err != nil {
		return runtime, err
	}
	return runtime, nil
}
