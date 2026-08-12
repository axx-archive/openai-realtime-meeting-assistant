package main

import (
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	ErrSTRIDERegistryInvalid     = errors.New("invalid STRIDE registry entry")
	ErrSTRIDERegistryUnknown     = errors.New("unknown STRIDE registry entry")
	ErrSTRIDERegistryQuarantined = errors.New("STRIDE registry entry is quarantined")
	ErrSTRIDEFeatureDisabled     = errors.New("STRIDE feature is disabled")
	ErrSTRIDEActivationFenced    = errors.New("STRIDE runtime activation is fenced in E1")
)

// STRIDEFeature names independently disabled authority seams.  The registry
// does not activate them: E1 may only record, inspect, and further disable a
// seam.  A later reviewed wave must replace the activation fence explicitly.
type STRIDEFeature string

const (
	STRIDEFeaturePublicProjection               STRIDEFeature = "public_projection"
	STRIDEFeatureCrossSurfaceRetrieval          STRIDEFeature = "cross_surface_retrieval"
	STRIDEFeatureScoutFileSearch                STRIDEFeature = "scout_file_search"
	STRIDEFeatureScoutFileActions               STRIDEFeature = "scout_file_actions"
	STRIDEFeatureAgentGIFActions                STRIDEFeature = "agent_gif_actions"
	STRIDEFeatureSuggestedWorkDetection         STRIDEFeature = "suggested_work_detection"
	STRIDEFeatureSuggestedWorkExecution         STRIDEFeature = "suggested_work_execution"
	STRIDEFeatureInsightsWorkflow               STRIDEFeature = "insights_opportunities_v1"
	STRIDEFeatureMarketplaceDiscovery           STRIDEFeature = "marketplace_discovery"
	STRIDEFeatureMarketplaceTrial               STRIDEFeature = "marketplace_trial"
	STRIDEFeatureMarketplaceUpdate              STRIDEFeature = "marketplace_update"
	STRIDEFeatureRoomVoiceInvocation            STRIDEFeature = "room_voice_invocation"
	STRIDEFeatureEnrichedScoutRouting           STRIDEFeature = "enriched_scout_routing"
	STRIDEFeatureRelationshipMemory             STRIDEFeature = "relationship_memory"
	STRIDEFeatureRichResponseModes              STRIDEFeature = "rich_response_modes"
	STRIDEFeatureModelRouteCanary               STRIDEFeature = "model_route_canary"
	STRIDEFeatureCoworkerContext                STRIDEFeature = "coworker_context"
	STRIDEFeatureCoworkerPersonality            STRIDEFeature = "coworker_personality"
	STRIDEFeatureCoworkerLearning               STRIDEFeature = "coworker_learning"
	STRIDEFeatureTeamAgentTrial                 STRIDEFeature = "team_agent_trial"
	STRIDEFeatureTeamAgentHire                  STRIDEFeature = "team_agent_hire"
	STRIDEFeatureTeamAgentUpdate                STRIDEFeature = "team_agent_update"
	STRIDEFeatureTeamAgentAssignment            STRIDEFeature = "team_agent_assignment"
	STRIDEFeatureSpecialistTokenMinting         STRIDEFeature = "specialist_token_minting"
	STRIDEFeatureMeetingInvitation              STRIDEFeature = "meeting_specialist_invitation"
	STRIDEFeatureSpecialistContext              STRIDEFeature = "specialist_context_assembly"
	STRIDEFeatureSpecialistTools                STRIDEFeature = "specialist_tools"
	STRIDEFeatureSpecialistAudio                STRIDEFeature = "specialist_audio_publication"
	STRIDEFeatureVisibleSpecialist              STRIDEFeature = "visible_specialist_profile"
	STRIDEFeaturePersonMyMindContext            STRIDEFeature = "person_mymind_context"
	STRIDEFeatureArtifactDisposition            STRIDEFeature = "artifact_disposition"
	STRIDEFeatureAmbientMindShadow              STRIDEFeature = "ambient_mind_projection_shadow"
	STRIDEFeaturePersonProfileAuthority         STRIDEFeature = "person_profile_authority"
	STRIDEFeatureOrganizationAuthorityWrite     STRIDEFeature = "organization_authority_write"
	STRIDEFeatureOrganizationAuthorityRead      STRIDEFeature = "organization_authority_read"
	STRIDEFeatureActiveOrganizationSession      STRIDEFeature = "active_organization_session"
	STRIDEFeatureContributionCandidateDetection STRIDEFeature = "contribution_candidate_detection"
	STRIDEFeatureContributionReview             STRIDEFeature = "contribution_review"
	STRIDEFeatureWorkRecordPrivate              STRIDEFeature = "work_record_private"
	STRIDEFeatureNetworkProfilePublication      STRIDEFeature = "network_profile_publication"
	STRIDEFeatureNetworkProjectionShadow        STRIDEFeature = "network_projection_shadow"
	STRIDEFeatureNetworkSearch                  STRIDEFeature = "network_search"
	STRIDEFeatureNetworkContact                 STRIDEFeature = "network_contact"
	STRIDEFeatureNetworkQueryParserProvider     STRIDEFeature = "network_query_parser_provider"
	STRIDEFeatureNetworkSemanticReranker        STRIDEFeature = "network_semantic_reranker"
	STRIDEFeatureProjectAuthorityRead           STRIDEFeature = "project_authority_read"
	STRIDEFeatureProjectAuthorityWrite          STRIDEFeature = "project_authority_write"
	STRIDEFeatureProjectSmartLink               STRIDEFeature = "project_smart_link"
	STRIDEFeatureProjectRecordProjection        STRIDEFeature = "project_record_projection"
)

var allSTRIDEFeatures = []STRIDEFeature{
	STRIDEFeaturePublicProjection, STRIDEFeatureCrossSurfaceRetrieval, STRIDEFeatureScoutFileSearch,
	STRIDEFeatureScoutFileActions, STRIDEFeatureAgentGIFActions,
	STRIDEFeatureSuggestedWorkDetection, STRIDEFeatureSuggestedWorkExecution, STRIDEFeatureInsightsWorkflow,
	STRIDEFeatureMarketplaceDiscovery, STRIDEFeatureMarketplaceTrial, STRIDEFeatureMarketplaceUpdate,
	STRIDEFeatureRoomVoiceInvocation, STRIDEFeatureEnrichedScoutRouting, STRIDEFeatureRelationshipMemory,
	STRIDEFeatureRichResponseModes, STRIDEFeatureModelRouteCanary,
	STRIDEFeatureCoworkerContext, STRIDEFeatureCoworkerPersonality, STRIDEFeatureCoworkerLearning,
	STRIDEFeatureTeamAgentTrial, STRIDEFeatureTeamAgentHire, STRIDEFeatureTeamAgentUpdate, STRIDEFeatureTeamAgentAssignment,
	STRIDEFeatureSpecialistTokenMinting, STRIDEFeatureMeetingInvitation, STRIDEFeatureSpecialistContext,
	STRIDEFeatureSpecialistTools, STRIDEFeatureSpecialistAudio, STRIDEFeatureVisibleSpecialist,
	STRIDEFeaturePersonMyMindContext,
	STRIDEFeatureArtifactDisposition,
	STRIDEFeatureAmbientMindShadow,
	STRIDEFeaturePersonProfileAuthority,
	STRIDEFeatureOrganizationAuthorityWrite, STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureActiveOrganizationSession,
	STRIDEFeatureContributionCandidateDetection, STRIDEFeatureContributionReview, STRIDEFeatureWorkRecordPrivate,
	STRIDEFeatureNetworkProfilePublication, STRIDEFeatureNetworkProjectionShadow, STRIDEFeatureNetworkSearch,
	STRIDEFeatureNetworkContact, STRIDEFeatureNetworkQueryParserProvider, STRIDEFeatureNetworkSemanticReranker,
	STRIDEFeatureProjectAuthorityRead, STRIDEFeatureProjectAuthorityWrite, STRIDEFeatureProjectSmartLink, STRIDEFeatureProjectRecordProjection,
}

type STRIDERegistryKind string

const (
	STRIDERegistryModelRoute STRIDERegistryKind = "model_route"
	STRIDERegistrySeat       STRIDERegistryKind = "seat"
	STRIDERegistryWorkflow   STRIDERegistryKind = "workflow"
	STRIDERegistryCoworker   STRIDERegistryKind = "coworker"
	STRIDERegistryPackage    STRIDERegistryKind = "package"
	STRIDERegistryListing    STRIDERegistryKind = "listing"
	STRIDERegistryProfile    STRIDERegistryKind = "profile"
	STRIDERegistryCapability STRIDERegistryKind = "capability"
)

type STRIDERegistryStatus string

const (
	STRIDERegistryDraft       STRIDERegistryStatus = "draft"
	STRIDERegistryQuarantined STRIDERegistryStatus = "quarantined"
	STRIDERegistryUnavailable STRIDERegistryStatus = "unavailable"
	STRIDERegistryRevoked     STRIDERegistryStatus = "revoked"
)

// STRIDERuntimeCapability is descriptive only.  It records compatibility
// claims for a later canary; it must not be used as provider configuration.
type STRIDERuntimeCapability struct {
	SingleCompletion       bool     `json:"singleCompletion"`
	ToolLoop               bool     `json:"toolLoop"`
	ParallelAgentEligible  bool     `json:"parallelAgentEligible"`
	Vision                 bool     `json:"vision"`
	Audio                  bool     `json:"audio"`
	StrictSchema           bool     `json:"strictSchema"`
	Streaming              bool     `json:"streaming"`
	Resumable              bool     `json:"resumable"`
	SideEffectClasses      []string `json:"sideEffectClasses"`
	MinimumReasoningEffort string   `json:"minimumReasoningEffort"`
	MaximumReasoningEffort string   `json:"maximumReasoningEffort"`
}

func (c STRIDERuntimeCapability) Validate() error {
	if !uniqueSTRIDEIDs(c.SideEffectClasses) || !oneOf(c.MinimumReasoningEffort, "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra") ||
		!oneOf(c.MaximumReasoningEffort, "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra") ||
		reasoningRank(c.MinimumReasoningEffort) > reasoningRank(c.MaximumReasoningEffort) {
		return ErrSTRIDERegistryInvalid
	}
	return nil
}

type STRIDERegistryEntry struct {
	TenantID         string                  `json:"tenantId"`
	Kind             STRIDERegistryKind      `json:"kind"`
	Key              string                  `json:"key"`
	Revision         int64                   `json:"revision"`
	Contract         STRIDEReference         `json:"contract"`
	Capability       STRIDERuntimeCapability `json:"capability"`
	Feature          STRIDEFeature           `json:"feature"`
	Status           STRIDERegistryStatus    `json:"status"`
	SchemaDigest     string                  `json:"schemaDigest"`
	CreatedAt        time.Time               `json:"createdAt"`
	QuarantineReason string                  `json:"quarantineReason,omitempty"`
}

func (entry STRIDERegistryEntry) Validate() error {
	if !strideIdentifier(entry.TenantID) || !validSTRIDERegistryKind(entry.Kind) || !strideIdentifier(entry.Key) || entry.Revision < 1 ||
		entry.Contract.Validate() != nil || entry.Capability.Validate() != nil || !validSTRIDEFeature(entry.Feature) ||
		!oneOf(string(entry.Status), string(STRIDERegistryDraft), string(STRIDERegistryQuarantined), string(STRIDERegistryUnavailable), string(STRIDERegistryRevoked)) ||
		!isHexDigest(entry.SchemaDigest) || entry.CreatedAt.IsZero() {
		return ErrSTRIDERegistryInvalid
	}
	if entry.Status == STRIDERegistryQuarantined && !strideIdentifier(entry.QuarantineReason) {
		return ErrSTRIDERegistryInvalid
	}
	if entry.Status != STRIDERegistryQuarantined && entry.QuarantineReason != "" {
		return ErrSTRIDERegistryInvalid
	}
	return nil
}

type STRIDERegistrySnapshot struct {
	Entries  []STRIDERegistryEntry `json:"entries"`
	Features []STRIDEFeatureState  `json:"features"`
	Digest   string                `json:"digest"`
}

type STRIDEFeatureState struct {
	Feature STRIDEFeature `json:"feature"`
	Enabled bool          `json:"enabled"`
}

// STRIDERegistry is intentionally in-memory in E1. Persistence is a later
// canonical writer concern; this object provides the exact closed validation,
// deterministic snapshot, quarantine, and default-off behavior that writer
// must preserve. It has no provider or runtime handles.
type STRIDERegistry struct {
	mu       sync.RWMutex
	entries  map[string]STRIDERegistryEntry
	features map[STRIDEFeature]bool
}

func NewSTRIDERegistry() *STRIDERegistry {
	registry := &STRIDERegistry{entries: make(map[string]STRIDERegistryEntry), features: make(map[STRIDEFeature]bool, len(allSTRIDEFeatures))}
	for _, feature := range allSTRIDEFeatures {
		registry.features[feature] = false
	}
	return registry
}

func (registry *STRIDERegistry) Register(entry STRIDERegistryEntry) error {
	if registry == nil || entry.Validate() != nil {
		return ErrSTRIDERegistryInvalid
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	key := strideRegistryEntryKey(entry.TenantID, entry.Kind, entry.Key)
	if previous, ok := registry.entries[key]; ok && entry.Revision <= previous.Revision {
		return ErrSTRIDERegistryInvalid
	}
	registry.entries[key] = cloneSTRIDERegistryEntry(entry)
	return nil
}

func (registry *STRIDERegistry) Quarantine(tenantID string, kind STRIDERegistryKind, key string, revision int64, reason string) error {
	if registry == nil || !strideIdentifier(tenantID) || !validSTRIDERegistryKind(kind) || !strideIdentifier(key) || revision < 1 || !strideIdentifier(reason) {
		return ErrSTRIDERegistryInvalid
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry, ok := registry.entries[strideRegistryEntryKey(tenantID, kind, key)]
	if !ok || revision <= entry.Revision {
		return ErrSTRIDERegistryUnknown
	}
	entry.Revision = revision
	entry.Status = STRIDERegistryQuarantined
	entry.QuarantineReason = reason
	registry.entries[strideRegistryEntryKey(tenantID, kind, key)] = entry
	return nil
}

func (registry *STRIDERegistry) Resolve(tenantID string, kind STRIDERegistryKind, key string) (STRIDERegistryEntry, error) {
	if registry == nil {
		return STRIDERegistryEntry{}, ErrSTRIDERegistryUnknown
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	entry, ok := registry.entries[strideRegistryEntryKey(tenantID, kind, key)]
	if !ok {
		return STRIDERegistryEntry{}, ErrSTRIDERegistryUnknown
	}
	if entry.Status == STRIDERegistryQuarantined {
		return STRIDERegistryEntry{}, ErrSTRIDERegistryQuarantined
	}
	if !registry.features[entry.Feature] {
		return STRIDERegistryEntry{}, ErrSTRIDEFeatureDisabled
	}
	return STRIDERegistryEntry{}, ErrSTRIDEActivationFenced
}

// SetFeatureEnabled deliberately permits only disabling during E1. A future
// reviewed activation path must be explicit and cannot accidentally reuse this
// default-off registry as a runtime control plane.
func (registry *STRIDERegistry) SetFeatureEnabled(feature STRIDEFeature, enabled bool) error {
	if registry == nil || !validSTRIDEFeature(feature) {
		return ErrSTRIDERegistryInvalid
	}
	if enabled {
		return ErrSTRIDEActivationFenced
	}
	registry.mu.Lock()
	registry.features[feature] = false
	registry.mu.Unlock()
	return nil
}

func (registry *STRIDERegistry) Snapshot() (STRIDERegistrySnapshot, error) {
	if registry == nil {
		return STRIDERegistrySnapshot{}, ErrSTRIDERegistryInvalid
	}
	registry.mu.RLock()
	entries := make([]STRIDERegistryEntry, 0, len(registry.entries))
	for _, entry := range registry.entries {
		entries = append(entries, cloneSTRIDERegistryEntry(entry))
	}
	features := make([]STRIDEFeatureState, 0, len(allSTRIDEFeatures))
	for _, feature := range allSTRIDEFeatures {
		features = append(features, STRIDEFeatureState{Feature: feature, Enabled: registry.features[feature]})
	}
	registry.mu.RUnlock()
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].TenantID != entries[j].TenantID {
			return entries[i].TenantID < entries[j].TenantID
		}
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind < entries[j].Kind
		}
		return entries[i].Key < entries[j].Key
	})
	sort.Slice(features, func(i, j int) bool { return features[i].Feature < features[j].Feature })
	snapshot := STRIDERegistrySnapshot{Entries: entries, Features: features}
	digest, err := STRIDEContractDigest(struct {
		Entries  []STRIDERegistryEntry `json:"entries"`
		Features []STRIDEFeatureState  `json:"features"`
	}{entries, features})
	if err != nil {
		return STRIDERegistrySnapshot{}, err
	}
	snapshot.Digest = digest
	return snapshot, nil
}

func strideRegistryEntryKey(tenantID string, kind STRIDERegistryKind, key string) string {
	return tenantID + "\x00" + string(kind) + "\x00" + key
}
func validSTRIDERegistryKind(kind STRIDERegistryKind) bool {
	switch kind {
	case STRIDERegistryModelRoute, STRIDERegistrySeat, STRIDERegistryWorkflow, STRIDERegistryCoworker, STRIDERegistryPackage, STRIDERegistryListing, STRIDERegistryProfile, STRIDERegistryCapability:
		return true
	}
	return false
}
func validSTRIDEFeature(feature STRIDEFeature) bool {
	for _, candidate := range allSTRIDEFeatures {
		if feature == candidate {
			return true
		}
	}
	return false
}
func reasoningRank(value string) int {
	for index, candidate := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"} {
		if value == candidate {
			return index
		}
	}
	return -1
}
func cloneSTRIDERegistryEntry(entry STRIDERegistryEntry) STRIDERegistryEntry {
	entry.Capability.SideEffectClasses = append([]string(nil), entry.Capability.SideEffectClasses...)
	return entry
}
