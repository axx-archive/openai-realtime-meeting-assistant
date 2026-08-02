package main

// E5 context assembly is deliberately a body-free server boundary. It accepts
// canonical references and caller-supplied authorization interfaces, never
// message bodies, raw brain inventories, provider endpoints, or credentials.

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrSTRIDEAgentContextInvalid = errors.New("invalid STRIDE agent context")
	ErrSTRIDEAgentContextDenied  = errors.New("STRIDE agent context denied")
	ErrSTRIDEScoutDisabled       = errors.New("Scout participant is disabled")
)

type STRIDEAgentContextSurface string

const (
	STRIDEContextPrivate      STRIDEAgentContextSurface = "private"
	STRIDEContextTeam         STRIDEAgentContextSurface = "team"
	STRIDEContextProject      STRIDEAgentContextSurface = "project"
	STRIDEContextMeetingText  STRIDEAgentContextSurface = "meeting_text"
	STRIDEContextMeetingVoice STRIDEAgentContextSurface = "meeting_voice"
)

type STRIDEContextTurn struct {
	Event           STRIDEReference  `json:"event"`
	AuthorPrincipal string           `json:"authorPrincipal"`
	AuthorName      string           `json:"authorName"`
	ReplyTo         *STRIDEReference `json:"replyTo,omitempty"`
	ReactionActors  []string         `json:"reactionActors,omitempty"`
}

func (turn STRIDEContextTurn) Validate() error {
	if turn.Event.Validate() != nil || turn.Event.ContractType != STRIDEContractConversationEvent || !strideIdentifier(turn.AuthorPrincipal) || strings.TrimSpace(turn.AuthorName) == "" ||
		(turn.ReplyTo != nil && (turn.ReplyTo.Validate() != nil || turn.ReplyTo.ContractType != STRIDEContractConversationEvent)) || (len(turn.ReactionActors) > 0 && !uniqueSTRIDEIDs(turn.ReactionActors)) {
		return ErrSTRIDEAgentContextInvalid
	}
	return nil
}

type STRIDEContextWorkRef struct {
	Reference STRIDEReference `json:"reference"`
	Status    string          `json:"status"`
}

func (work STRIDEContextWorkRef) Validate() error {
	if work.Reference.Validate() != nil || work.Reference.ContractType != STRIDEContractWorkProposal && work.Reference.ContractType != STRIDEContractWorkRun || work.Status != "approved" {
		return ErrSTRIDEAgentContextInvalid
	}
	return nil
}

// STRIDEContextAuthorizer must reauthorize every reference before it enters a
// model context. It receives only a body-free reference and principals.
type STRIDEContextAuthorizer interface {
	AuthorizeAgentContext(reference STRIDEReference, requester string, audience STRIDEAudience) bool
}

type STRIDEContextConsentAuthorizer interface {
	AuthorizeRelationshipMemory(reference STRIDEReference, requester string, audience STRIDEAudience) bool
}

type STRIDEAgentContextRequest struct {
	TenantID            string                    `json:"tenantId"`
	ContextID           string                    `json:"contextId"`
	Revision            int64                     `json:"revision"`
	CreatedAt           time.Time                 `json:"createdAt"`
	Surface             STRIDEAgentContextSurface `json:"surface"`
	Invocation          string                    `json:"invocation"`
	Requester           string                    `json:"requester"`
	Recipients          []string                  `json:"recipients"`
	CoreProfile         STRIDEReference           `json:"coreProfile"`
	Overlay             *STRIDEReference          `json:"overlay,omitempty"`
	Capability          STRIDEReference           `json:"capability"`
	ChannelPolicy       STRIDEReference           `json:"channelPolicy"`
	CurrentTurn         STRIDEContextTurn         `json:"currentTurn"`
	RecentTurns         []STRIDEContextTurn       `json:"recentTurns"`
	Evidence            []STRIDEReference         `json:"evidence"`
	Relationships       []STRIDEReference         `json:"relationships,omitempty"`
	ActiveWork          []STRIDEContextWorkRef    `json:"activeWork,omitempty"`
	AllowedTools        []string                  `json:"allowedTools"`
	ResponseModes       []string                  `json:"responseModes"`
	Audience            STRIDEAudience            `json:"audience"`
	ACLVersion          int64                     `json:"aclVersion"`
	PurgeGeneration     int64                     `json:"purgeGeneration"`
	TranscriptHighWater uint64                    `json:"transcriptHighWater"`
	AnalysisHighWater   uint64                    `json:"analysisHighWater"`
	BrainHighWater      uint64                    `json:"brainHighWater"`
	FreshnessDigest     string                    `json:"freshnessDigest"`
	GapsDigest          string                    `json:"gapsDigest"`
}

func (request STRIDEAgentContextRequest) Validate() error {
	if !strideIdentifier(request.TenantID) || !strideIdentifier(request.ContextID) || request.Revision < 1 || request.CreatedAt.IsZero() ||
		!validSTRIDEContextSurface(request.Surface) || !strideIdentifier(request.Invocation) || !strideIdentifier(request.Requester) || !uniqueSTRIDEIDs(request.Recipients) ||
		request.CoreProfile.Validate() != nil || request.CoreProfile.ContractType != STRIDEContractAgentCoreProfile ||
		(request.Overlay != nil && (request.Overlay.Validate() != nil || request.Overlay.ContractType != STRIDEContractAgentProfileOverlay)) ||
		request.Capability.Validate() != nil || request.Capability.ContractType != STRIDEContractAgentCapabilityManifest ||
		request.ChannelPolicy.Validate() != nil || request.ChannelPolicy.ContractType != STRIDEContractChannelNormProfile ||
		request.CurrentTurn.Validate() != nil || len(request.RecentTurns) == 0 || len(request.Evidence) == 0 || !validateSTRIDERefs(request.Evidence) ||
		!uniqueSTRIDEIDs(request.AllowedTools) || !uniqueSTRIDEIDs(request.ResponseModes) || request.Audience.Validate() != nil || request.ACLVersion < 1 || request.PurgeGeneration < 0 ||
		!isHexDigest(request.FreshnessDigest) || !isHexDigest(request.GapsDigest) {
		return ErrSTRIDEAgentContextInvalid
	}
	if !containsSTRIDEID(request.Audience.Principals, request.Requester) {
		return ErrSTRIDEAgentContextInvalid
	}
	for _, recipient := range request.Recipients {
		if !containsSTRIDEID(request.Audience.Principals, recipient) {
			return ErrSTRIDEAgentContextInvalid
		}
	}
	for _, turn := range request.RecentTurns {
		if turn.Validate() != nil {
			return ErrSTRIDEAgentContextInvalid
		}
	}
	if !validateOptionalSTRIDERefs(request.Relationships) {
		return ErrSTRIDEAgentContextInvalid
	}
	for _, relationship := range request.Relationships {
		if relationship.ContractType != STRIDEContractAgentRelationshipMemory {
			return ErrSTRIDEAgentContextInvalid
		}
	}
	for _, work := range request.ActiveWork {
		if work.Validate() != nil {
			return ErrSTRIDEAgentContextInvalid
		}
	}
	for _, tool := range request.AllowedTools {
		if !safeSTRIDEContextTool(tool) {
			return ErrSTRIDEAgentContextInvalid
		}
	}
	for _, mode := range request.ResponseModes {
		if !oneOf(mode, "text", "text_gif", "gif_only", "file_card", "artifact_card", "safe_refusal") {
			return ErrSTRIDEAgentContextInvalid
		}
	}
	return nil
}

type STRIDEAssembledAgentContext struct {
	Envelope            AgentContextEnvelope `json:"envelope"`
	CoreProfile         STRIDEReference      `json:"coreProfile"`
	Overlay             *STRIDEReference     `json:"overlay,omitempty"`
	Turns               []STRIDEContextTurn  `json:"turns"`
	ACLVersion          int64                `json:"aclVersion"`
	PurgeGeneration     int64                `json:"purgeGeneration"`
	TranscriptHighWater uint64               `json:"transcriptHighWater"`
	AnalysisHighWater   uint64               `json:"analysisHighWater"`
	BrainHighWater      uint64               `json:"brainHighWater"`
	FreshnessDigest     string               `json:"freshnessDigest"`
	GapsDigest          string               `json:"gapsDigest"`
	Digest              string               `json:"digest"`
	// CollaborationPreferences contains only the human-consented, currently
	// reauthorized values whose immutable relationship references are already
	// bound into Envelope.Preferences and Digest. Conversation bodies remain
	// absent from this boundary.
	CollaborationPreferences []STRIDECollaborationContextPreference `json:"collaborationPreferences,omitempty"`
	CollaborationRevision    int64                                  `json:"collaborationRevision,omitempty"`
}

type STRIDEAgentContextAssembler struct {
	Authorizer STRIDEContextAuthorizer
	Consent    STRIDEContextConsentAuthorizer
}

func (assembler STRIDEAgentContextAssembler) Assemble(request STRIDEAgentContextRequest) (STRIDEAssembledAgentContext, error) {
	if request.Validate() != nil || assembler.Authorizer == nil || assembler.Consent == nil {
		return STRIDEAssembledAgentContext{}, ErrSTRIDEAgentContextInvalid
	}
	references := []STRIDEReference{request.CoreProfile, request.Capability, request.ChannelPolicy, request.CurrentTurn.Event}
	if request.Overlay != nil {
		references = append(references, *request.Overlay)
	}
	for _, turn := range request.RecentTurns {
		references = append(references, turn.Event)
		if turn.ReplyTo != nil {
			references = append(references, *turn.ReplyTo)
		}
	}
	references = append(references, request.Evidence...)
	for _, work := range request.ActiveWork {
		references = append(references, work.Reference)
	}
	for _, reference := range references {
		if !assembler.Authorizer.AuthorizeAgentContext(reference, request.Requester, request.Audience) {
			return STRIDEAssembledAgentContext{}, ErrSTRIDEAgentContextDenied
		}
	}
	for _, reference := range request.Relationships {
		if !assembler.Authorizer.AuthorizeAgentContext(reference, request.Requester, request.Audience) || !assembler.Consent.AuthorizeRelationshipMemory(reference, request.Requester, request.Audience) {
			return STRIDEAssembledAgentContext{}, ErrSTRIDEAgentContextDenied
		}
	}
	activeWork := make([]STRIDEReference, 0, len(request.ActiveWork))
	for _, work := range request.ActiveWork {
		activeWork = append(activeWork, work.Reference)
	}
	profile := request.CoreProfile
	if request.Overlay != nil {
		profile = *request.Overlay
	}
	material := struct {
		TenantID, ContextID           string
		Revision                      int64
		CreatedAt                     time.Time
		Surface                       STRIDEAgentContextSurface
		Invocation, Requester         string
		Recipients                    []string
		Core                          STRIDEReference
		Overlay                       *STRIDEReference
		Capability, Channel           STRIDEReference
		Current                       STRIDEContextTurn
		Turns                         []STRIDEContextTurn
		Evidence, Relationships, Work []STRIDEReference
		Tools, Modes                  []string
		Audience                      STRIDEAudience
		ACL, Purge                    int64
		Transcript, Analysis, Brain   uint64
		Freshness, Gaps               string
	}{request.TenantID, request.ContextID, request.Revision, request.CreatedAt, request.Surface, request.Invocation, request.Requester, append([]string(nil), request.Recipients...), request.CoreProfile, request.Overlay, request.Capability, request.ChannelPolicy, request.CurrentTurn, cloneSTRIDEContextTurns(request.RecentTurns), SortedSTRIDEReferences(request.Evidence), SortedSTRIDEReferences(request.Relationships), SortedSTRIDEReferences(activeWork), sortedUniqueSTRIDEIDs(request.AllowedTools), sortedUniqueSTRIDEIDs(request.ResponseModes), request.Audience, request.ACLVersion, request.PurgeGeneration, request.TranscriptHighWater, request.AnalysisHighWater, request.BrainHighWater, request.FreshnessDigest, request.GapsDigest}
	digest, err := STRIDEContractDigest(material)
	if err != nil {
		return STRIDEAssembledAgentContext{}, err
	}
	header := STRIDEContractHeader{TenantID: request.TenantID, ID: request.ContextID, Revision: request.Revision, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractAgentContextEnvelope, ContentDigest: digest, CreatedAt: request.CreatedAt}
	envelope := AgentContextEnvelope{Header: header, AgentProfile: profile, Capability: request.Capability, ChannelPolicy: request.ChannelPolicy, InvocationSurface: string(request.Surface), InvocationReason: request.Invocation, Requester: request.Requester, Recipients: append([]string(nil), request.Recipients...), CurrentTurn: request.CurrentTurn.Event, RecentTurns: contextTurnRefs(request.RecentTurns), Evidence: SortedSTRIDEReferences(request.Evidence), Preferences: SortedSTRIDEReferences(request.Relationships), ActiveWork: SortedSTRIDEReferences(activeWork), ResponseModes: sortedUniqueSTRIDEIDs(request.ResponseModes), PermittedTools: sortedUniqueSTRIDEIDs(request.AllowedTools), Audience: request.Audience, CoverageDigest: request.FreshnessDigest, ContextDigest: digest}
	// AgentContextEnvelope's initial E1 validator requires a nonempty
	// preferences slice even though relationship memory is legitimately absent.
	// This assembler preserves the correct empty value and validates every
	// non-optional contract field directly instead of inventing a preference.
	if header.Validate(STRIDEContractAgentContextEnvelope) != nil || envelope.AgentProfile.Validate() != nil || envelope.Capability.Validate() != nil || envelope.ChannelPolicy.Validate() != nil || envelope.CurrentTurn.Validate() != nil || !validateSTRIDERefs(envelope.RecentTurns) || !validateSTRIDERefs(envelope.Evidence) || !validateOptionalSTRIDERefs(envelope.Preferences) || !validateOptionalSTRIDERefs(envelope.ActiveWork) || envelope.Audience.Validate() != nil {
		return STRIDEAssembledAgentContext{}, ErrSTRIDEAgentContextInvalid
	}
	return STRIDEAssembledAgentContext{Envelope: envelope, CoreProfile: request.CoreProfile, Overlay: cloneSTRIDEReference(request.Overlay), Turns: cloneSTRIDEContextTurns(request.RecentTurns), ACLVersion: request.ACLVersion, PurgeGeneration: request.PurgeGeneration, TranscriptHighWater: request.TranscriptHighWater, AnalysisHighWater: request.AnalysisHighWater, BrainHighWater: request.BrainHighWater, FreshnessDigest: request.FreshnessDigest, GapsDigest: request.GapsDigest, Digest: digest}, nil
}

// STRIDEScoutParticipantState keeps the human-authored identity and
// correction surface separate from invocation and model routing. It starts
// disabled and can be admitted only by the signed deterministic-local coworker
// activation boundary; enabling this state never grants a provider route.
type STRIDEScoutParticipantState struct {
	mu            sync.RWMutex
	core          AgentCoreProfile
	relationships map[string]AgentRelationshipMemory
	disabled      bool
}

func NewSTRIDEScoutParticipantState(core AgentCoreProfile) (*STRIDEScoutParticipantState, error) {
	if core.Validate() != nil {
		return nil, ErrSTRIDEAgentContextInvalid
	}
	return &STRIDEScoutParticipantState{core: core, relationships: map[string]AgentRelationshipMemory{}, disabled: true}, nil
}
func (state *STRIDEScoutParticipantState) CoreProfile() (AgentCoreProfile, error) {
	if state == nil {
		return AgentCoreProfile{}, ErrSTRIDEAgentContextInvalid
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.core, nil
}
func (state *STRIDEScoutParticipantState) Disabled() bool {
	if state == nil {
		return true
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.disabled
}
func (state *STRIDEScoutParticipantState) Disable() {
	if state == nil {
		return
	}
	state.mu.Lock()
	state.disabled = true
	state.mu.Unlock()
}
func (state *STRIDEScoutParticipantState) Enable() error { return ErrSTRIDEActivationFenced }

func (state *STRIDEScoutParticipantState) EnableWithAuthority(config STRIDERuntimeConfig, receipt STRIDEProductActivationReceipt, now time.Time) error {
	if state == nil || now.IsZero() || !config.RelationshipMemoryEnabled || !verifySTRIDEProductActivationReceipt(config, receipt, STRIDEProductScopeCoworker, now.UTC()) {
		return ErrSTRIDEActivationFenced
	}
	state.mu.Lock()
	state.disabled = false
	state.mu.Unlock()
	return nil
}
func (state *STRIDEScoutParticipantState) InspectRelationship(id string) (AgentRelationshipMemory, error) {
	if state == nil || !strideIdentifier(id) {
		return AgentRelationshipMemory{}, ErrSTRIDEAgentContextInvalid
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	value, ok := state.relationships[id]
	if !ok {
		return AgentRelationshipMemory{}, ErrSTRIDEConversationUnknown
	}
	return value, nil
}
func (state *STRIDEScoutParticipantState) CorrectRelationship(value AgentRelationshipMemory) error {
	if state == nil || value.Validate() != nil {
		return ErrSTRIDEAgentContextInvalid
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.relationships[value.Header.ID] = value
	return nil
}

// ForgetRelationship removes a relationship-memory projection at the user or
// policy boundary. It is deliberately idempotent: callers need not learn
// whether a prior memory existed in order to make it unavailable to Scout.
func (state *STRIDEScoutParticipantState) ForgetRelationship(id string) error {
	if state == nil || !strideIdentifier(id) {
		return ErrSTRIDEAgentContextInvalid
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	delete(state.relationships, id)
	return nil
}

func validSTRIDEContextSurface(surface STRIDEAgentContextSurface) bool {
	return surface == STRIDEContextPrivate || surface == STRIDEContextTeam || surface == STRIDEContextProject || surface == STRIDEContextMeetingText || surface == STRIDEContextMeetingVoice
}
func safeSTRIDEContextTool(tool string) bool {
	if !strideIdentifier(tool) {
		return false
	}
	lowered := strings.ToLower(tool)
	for _, forbidden := range []string{"raw_brain", "raw_audio", "credential", "secret", "provider_url", "endpoint", "fetch_url"} {
		if strings.Contains(lowered, forbidden) {
			return false
		}
	}
	return true
}
func contextTurnRefs(turns []STRIDEContextTurn) []STRIDEReference {
	refs := make([]STRIDEReference, 0, len(turns))
	for _, turn := range turns {
		refs = append(refs, turn.Event)
	}
	return SortedSTRIDEReferences(refs)
}
func cloneSTRIDEContextTurns(turns []STRIDEContextTurn) []STRIDEContextTurn {
	clone := append([]STRIDEContextTurn(nil), turns...)
	for index := range clone {
		clone[index].ReactionActors = append([]string(nil), clone[index].ReactionActors...)
	}
	sort.Slice(clone, func(i, j int) bool { return clone[i].Event.ID < clone[j].Event.ID })
	return clone
}
func cloneSTRIDEReference(reference *STRIDEReference) *STRIDEReference {
	if reference == nil {
		return nil
	}
	clone := *reference
	return &clone
}
