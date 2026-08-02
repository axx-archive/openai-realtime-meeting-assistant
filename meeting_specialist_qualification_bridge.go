package main

import (
	"context"
	"errors"
	"strings"

	"github.com/openai/openai-realtime-meeting-assistant/internal/e10evidence"
)

// MeetingSpecialistQualifiedProviderFactory is sealed application wiring. It
// captures both the actual provider factory and the exact signed qualification
// subject derived from that factory's server-owned configuration. The public
// join configuration accepts this concrete type, never a caller-implemented
// Trusted/Current interface or a boolean claim.
type MeetingSpecialistQualifiedProviderFactory struct {
	request       MeetingSpecialistQualificationRequest
	subjectDigest string
	create        MeetingSpecialistProviderFactory
}

// NewMeetingSpecialistQualifiedRealtimeFactory maps the actual normalized
// Realtime configuration, canonical route, accounting contract, and specialist
// references to the signed E10 binding. It does not dial or call a provider.
func NewMeetingSpecialistQualifiedRealtimeFactory(config MeetingSpecialistRealtimeConfig, deployment MeetingSpecialistQualificationDeployment) (*MeetingSpecialistQualifiedProviderFactory, error) {
	config = config.normalized()
	if !config.Enabled || config.APIKey == "" || config.ResolveBrief == nil || config.Model != meetingSpecialistRealtimeModel || config.Voice == "" ||
		!oneOf(config.ReasoningEffort, "low", "medium", "high") || config.MaxOutputTokens <= 0 || config.MaxOutputTokens > 4096 ||
		config.MaxContextBytes <= 0 || config.MaxContextBytes > meetingSpecialistRealtimeMaxContextBytes || config.MaxEventBytes <= 0 || config.MaxEventBytes > meetingSpecialistRealtimeMaxEventBytes ||
		config.MaxEvents <= 0 || config.MaxEvents > meetingSpecialistRealtimeMaxEvents || config.MaxAudioBytes <= 0 || config.MaxAudioBytes > meetingSpecialistRealtimeMaxAudioBytes ||
		deployment.AccountingMode != MeetingSpecialistRealtimeInputDirectPCM || config.InputMode != deployment.AccountingMode || !validMeetingSpecialistRealtimeInputAccounting(config) {
		return nil, ErrMeetingSpecialistJoinQualification
	}
	binding := meetingSpecialistQualificationBinding(config, deployment.AccountingMode, deployment.SpecialistProfile, deployment.SpecialistCapability)
	subjectDigest := e10evidence.MeetingSpecialistQualificationFixtureDigest(binding)
	request := MeetingSpecialistQualificationRequest{
		TenantID: deployment.TenantID, ResultID: deployment.ResultID, TargetID: deployment.TargetID,
		EvaluatorConfigDigest: deployment.EvaluatorConfigDigest, EvaluatorResultDigest: deployment.EvaluatorResultDigest,
		FixtureDigest: deployment.FixtureDigest, QualificationSubjectDigest: subjectDigest, Candidate: deployment.Candidate, Binding: binding,
		SpecialistProfile: deployment.SpecialistProfile, SpecialistCapability: deployment.SpecialistCapability,
	}
	if request.validate() != nil || deployment.FixtureDigest != subjectDigest {
		return nil, ErrMeetingSpecialistJoinQualification
	}
	create := NewMeetingSpecialistRealtimeProviderFactory(config)
	if create == nil {
		return nil, ErrMeetingSpecialistJoinQualification
	}
	return &MeetingSpecialistQualifiedProviderFactory{request: request, subjectDigest: subjectDigest, create: create}, nil
}

func (factory *MeetingSpecialistQualifiedProviderFactory) provider(ctx context.Context, launch MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
	if factory == nil || factory.create == nil || factory.request.validate() != nil || factory.subjectDigest != factory.request.QualificationSubjectDigest {
		return nil, ErrMeetingSpecialistJoinQualification
	}
	provider, err := factory.create(ctx, launch)
	if err != nil || provider == nil {
		return provider, err
	}
	return &qualifiedMeetingSpecialistProvider{provider: provider, subjectDigest: factory.subjectDigest}, nil
}

// meetingSpecialistQualificationBinding is the only mapping from a Realtime
// config to signed E10 provider/voice evidence. Secrets, clocks, functions,
// and dialers are intentionally excluded; their behavior is instead bound by
// the signed release tree/image/config digests.
func meetingSpecialistQualificationBinding(config MeetingSpecialistRealtimeConfig, accountingMode MeetingSpecialistRealtimeInputMode, profile, capability STRIDEReference) e10evidence.MeetingSpecialistQualificationBinding {
	config = config.normalized()
	return e10evidence.MeetingSpecialistQualificationBinding{
		Provider: meetingSpecialistProviderName, Model: config.Model, Voice: config.Voice,
		RouteDigest: workDigest(struct {
			Schema, Provider, Route, Endpoint, Model, InputFormat, OutputFormat string
		}{"stride.meeting-specialist-provider-route/v1", meetingSpecialistProviderName, meetingSpecialistProviderRoute, meetingSpecialistRealtimeEndpoint, config.Model, "audio/pcm@24000", "audio/pcm@24000"}),
		AccountingProfileDigest: workDigest(struct {
			Schema             string
			InputMode          MeetingSpecialistRealtimeInputMode
			Model              string
			ContextReservation int64
			MaxOutputTokens    int64
		}{"stride.meeting-specialist-accounting-profile/v1", accountingMode, config.Model, meetingSpecialistRealtimeContextWindowTokens, config.MaxOutputTokens}),
		RuntimeProfileDigest: workDigest(struct {
			Schema                  string
			Model                   string
			ReasoningEffort         string
			Voice                   string
			MaxOutputTokens         int64
			MaxContextBytes         int
			MaxEventBytes           int64
			MaxEvents               int
			MaxAudioBytes           int64
			SafetyIdentifierEnabled bool
		}{"stride.meeting-specialist-runtime-profile/v1", config.Model, config.ReasoningEffort, config.Voice, config.MaxOutputTokens, config.MaxContextBytes, config.MaxEventBytes, config.MaxEvents, config.MaxAudioBytes, strings.TrimSpace(config.SafetyIdentifier) != ""}),
		CapabilityPolicyDigest: meetingSpecialistCapabilityPolicyDigest(profile, capability),
	}
}

func meetingSpecialistCapabilityPolicyDigest(profile, capability STRIDEReference) string {
	return workDigest(struct {
		Schema     string
		Profile    STRIDEReference
		Capability STRIDEReference
	}{"stride.meeting-specialist-capability-policy/v1", profile, capability})
}

// qualifiedMeetingSpecialistProvider is created only by the sealed factory.
// The runtime requires this exact concrete wrapper and subject before Brief.
type qualifiedMeetingSpecialistProvider struct {
	provider      MeetingSpecialistProvider
	subjectDigest string
}

func (provider *qualifiedMeetingSpecialistProvider) Brief(ctx context.Context, envelope MeetingSpecialistContextEnvelope) error {
	return provider.provider.Brief(ctx, envelope)
}
func (provider *qualifiedMeetingSpecialistProvider) WriteHumanPCM(ctx context.Context, generation uint64, samples []int16) error {
	return provider.provider.WriteHumanPCM(ctx, generation, samples)
}
func (provider *qualifiedMeetingSpecialistProvider) BeginResponse(ctx context.Context, lease MeetingAgentFloorLease) error {
	return provider.provider.BeginResponse(ctx, lease)
}
func (provider *qualifiedMeetingSpecialistProvider) CancelResponse(ctx context.Context, generation uint64, reason string) error {
	return provider.provider.CancelResponse(ctx, generation, reason)
}
func (provider *qualifiedMeetingSpecialistProvider) Close(ctx context.Context, reason string) error {
	return provider.provider.Close(ctx, reason)
}
func (provider *qualifiedMeetingSpecialistProvider) BindMeetingSpecialistProviderHooks(hooks MeetingSpecialistProviderHooks) error {
	binder, ok := provider.provider.(meetingSpecialistProviderHookBinder)
	if !ok {
		return nil
	}
	return binder.BindMeetingSpecialistProviderHooks(hooks)
}
func (provider *qualifiedMeetingSpecialistProvider) MeetingSpecialistProviderReceipt() MeetingSpecialistProviderReceipt {
	receipt := MeetingSpecialistProviderReceipt{}
	if source, ok := provider.provider.(meetingSpecialistProviderReceiptSource); ok {
		receipt = source.MeetingSpecialistProviderReceipt()
	}
	if receipt == (MeetingSpecialistProviderReceipt{}) {
		return receipt
	}
	receipt.QualificationSubjectDigest = provider.subjectDigest
	return receipt
}

func validateQualifiedMeetingSpecialistProvider(provider MeetingSpecialistProvider, subjectDigest string) error {
	qualified, ok := provider.(*qualifiedMeetingSpecialistProvider)
	if !ok || qualified.provider == nil || !isHexDigest(subjectDigest) || qualified.subjectDigest != subjectDigest {
		return errors.New("meeting specialist provider is not bound to the qualified subject")
	}
	return nil
}
