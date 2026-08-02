package main

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	ErrMeetingSpecialistJoinDisabled      = errors.New("meeting specialist production join is disabled")
	ErrMeetingSpecialistJoinAssembly      = errors.New("meeting specialist production join assembly is invalid")
	ErrMeetingSpecialistJoinQualification = errors.New("meeting specialist production qualification is not current")
)

const meetingSpecialistQualificationSubjectSchema = "stride.meeting-specialist-qualification-subject.v1"

// MeetingSpecialistQualificationRequest is the exact immutable candidate that
// an independently administered E10 authority must have qualified. The local
// application can bind and compare this request, but cannot qualify itself.
type MeetingSpecialistQualificationRequest struct {
	TenantID              string
	ResultID              string
	ResultDigest          string
	TargetID              string
	TargetDigest          string
	CandidateRelease      string
	CandidateTreeDigest   string
	CandidateImageDigest  string
	CandidateConfigDigest string
	CandidateRouteDigest  string
	Provider              string
	ProviderModel         string
	ProviderRoute         string
	ProviderRouteDigest   string
	AccountingMode        MeetingSpecialistRealtimeInputMode
	SpecialistProfile     STRIDEReference
	SpecialistCapability  STRIDEReference
}

func (request MeetingSpecialistQualificationRequest) validate() error {
	if !strideIdentifier(request.TenantID) || !strideIdentifier(request.ResultID) || !isHexDigest(request.ResultDigest) ||
		!strideIdentifier(request.TargetID) || !isHexDigest(request.TargetDigest) || !releaseCommitPattern.MatchString(request.CandidateRelease) ||
		!isHexDigest(request.CandidateTreeDigest) || !isHexDigest(request.CandidateImageDigest) || !isHexDigest(request.CandidateConfigDigest) || !isHexDigest(request.CandidateRouteDigest) ||
		!strideIdentifier(request.Provider) || !strideIdentifier(request.ProviderModel) || !strideIdentifier(request.ProviderRoute) || !isHexDigest(request.ProviderRouteDigest) ||
		!oneOf(string(request.AccountingMode), string(MeetingSpecialistRealtimeInputDirectPCM), string(MeetingSpecialistRealtimeInputBoundedTranscript)) ||
		request.SpecialistProfile.Validate() != nil || !oneOf(string(request.SpecialistProfile.ContractType), string(STRIDEContractAgentCoreProfile), string(STRIDEContractAgentProfileOverlay)) ||
		request.SpecialistCapability.Validate() != nil || request.SpecialistCapability.ContractType != STRIDEContractAgentCapabilityManifest {
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
	return workDigest(struct {
		Schema  string
		Subject MeetingSpecialistQualificationRequest
	}{Schema: meetingSpecialistQualificationSubjectSchema, Subject: request}), nil
}

// MeetingSpecialistQualificationStatus is returned by the external trust
// root. SubjectDigest is the externally verified digest of the canonical
// request above, not a digest of caller-echoed configuration.
type MeetingSpecialistQualificationStatus struct {
	SubjectDigest string
	Qualified     bool
	QualifiedAt   time.Time
	ExpiresAt     time.Time
}

// MeetingSpecialistQualificationAuthority is intentionally narrower than the
// local QualificationEvidenceStore, which is structure-only and must never be
// treated as a provider qualification trust root. The concrete external store
// adapter is application wiring owned by the release/qualification workstream.
type MeetingSpecialistQualificationAuthority interface {
	Trusted() bool
	Current(context.Context, MeetingSpecialistQualificationRequest) (MeetingSpecialistQualificationStatus, error)
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
	ProviderFactory     MeetingSpecialistProviderFactory
	PublishAudio        MeetingSpecialistAudioPublisher
	Qualification       MeetingSpecialistQualificationAuthority
	QualificationTarget MeetingSpecialistQualificationRequest
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
	qualification       MeetingSpecialistQualificationAuthority
	qualificationTarget MeetingSpecialistQualificationRequest
}

func NewMeetingSpecialistProductionJoiner(config MeetingSpecialistProductionJoinConfig) *MeetingSpecialistProductionJoiner {
	if config.Now == nil {
		config.Now = time.Now
	}
	return &MeetingSpecialistProductionJoiner{
		enabled: config.Enabled, now: config.Now, gates: config.Gates,
		resolveCurrent: config.ResolveCurrent, mintCapability: config.MintCapability,
		capabilityAuthority: config.CapabilityAuthority, providerFactory: config.ProviderFactory,
		publishAudio: config.PublishAudio, qualification: config.Qualification, qualificationTarget: config.QualificationTarget,
	}
}

func (joiner *MeetingSpecialistProductionJoiner) Enabled() bool {
	return joiner != nil && joiner.enabled
}

func (joiner *MeetingSpecialistProductionJoiner) Ready() bool {
	return joiner != nil && joiner.Enabled() && joiner.assemblyReady() && joiner.qualificationCurrent(context.Background(), joiner.qualificationTarget, joiner.now().UTC()) == nil
}

func (joiner *MeetingSpecialistProductionJoiner) assemblyReady() bool {
	return joiner != nil && joiner.gates.launchEnabled() && joiner.resolveCurrent != nil && joiner.mintCapability != nil && joiner.capabilityAuthority != nil && joiner.providerFactory != nil && joiner.publishAudio != nil
}

func (joiner *MeetingSpecialistProductionJoiner) qualificationCurrent(ctx context.Context, request MeetingSpecialistQualificationRequest, now time.Time) error {
	if joiner == nil || joiner.qualification == nil || !joiner.qualification.Trusted() || request.validate() != nil {
		return ErrMeetingSpecialistJoinQualification
	}
	subjectDigest, err := MeetingSpecialistQualificationSubjectDigest(request)
	if err != nil {
		return ErrMeetingSpecialistJoinQualification
	}
	status, err := joiner.qualification.Current(ctx, request)
	if err != nil || status.SubjectDigest != subjectDigest || !isHexDigest(status.SubjectDigest) || !status.Qualified || status.QualifiedAt.IsZero() || status.QualifiedAt.After(now) || !status.ExpiresAt.After(now) {
		return ErrMeetingSpecialistJoinQualification
	}
	return nil
}

func (joiner *MeetingSpecialistProductionJoiner) Join(ctx context.Context, request MeetingSpecialistJoinRequest) (*MeetingSpecialistRuntime, error) {
	if !joiner.Enabled() {
		return nil, ErrMeetingSpecialistJoinDisabled
	}
	if !joiner.assemblyReady() {
		return nil, ErrMeetingSpecialistJoinAssembly
	}
	if joiner.qualification == nil || !joiner.qualification.Trusted() || joiner.qualificationTarget.validate() != nil {
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
	qualificationRequest := joiner.qualificationTarget
	if qualificationRequest.TenantID != request.Scope.TenantID || qualificationRequest.SpecialistProfile != request.Candidate.Profile || qualificationRequest.SpecialistCapability != request.Candidate.Capability ||
		joiner.qualificationCurrent(ctx, qualificationRequest, now) != nil {
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
	if err := joiner.qualificationCurrent(ctx, qualificationRequest, joiner.now().UTC()); err != nil {
		return nil, err
	}
	runtime := NewMeetingSpecialistRuntime(joiner.now, joiner.gates, joiner.capabilityAuthority, joiner.providerFactory, joiner.publishAudio)
	if _, err := runtime.Start(ctx, launch); err != nil {
		return runtime, err
	}
	return runtime, nil
}
