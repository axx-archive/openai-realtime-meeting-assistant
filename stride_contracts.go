package main

// STRIDE's E1 contracts are intentionally data-only.  They describe what may
// be persisted, projected, or authorized; no contract grants a model, tool,
// network route, runtime principal, or provider credential by itself.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

var ErrSTRIDEContractInvalid = errors.New("invalid STRIDE contract")

const STRIDEContractSchemaVersion = 1

var strideID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,191}$`)

type STRIDEContractType string

const (
	STRIDEContractConversationEvent          STRIDEContractType = "conversation_event"
	STRIDEContractTranscriptSegment          STRIDEContractType = "transcript_segment"
	STRIDEContractTranscriptRevision         STRIDEContractType = "transcript_revision"
	STRIDEContractMeetingSourceEpisode       STRIDEContractType = "meeting_source_episode"
	STRIDEContractSourceEpisode              STRIDEContractType = "source_episode"
	STRIDEContractAnalysisProjection         STRIDEContractType = "analysis_projection"
	STRIDEContractKnowledgeAssertion         STRIDEContractType = "knowledge_assertion"
	STRIDEContractCollaborationPreference    STRIDEContractType = "collaboration_preference"
	STRIDEContractWorkIntent                 STRIDEContractType = "work_intent"
	STRIDEContractWorkProposal               STRIDEContractType = "work_proposal"
	STRIDEContractWorkRun                    STRIDEContractType = "work_run"
	STRIDEContractOutcome                    STRIDEContractType = "outcome"
	STRIDEContractMeetingAnswer              STRIDEContractType = "meeting_answer"
	STRIDEContractCompanyAnswer              STRIDEContractType = "company_answer"
	STRIDEContractAgentCoreProfile           STRIDEContractType = "agent_core_profile"
	STRIDEContractAgentProfileOverlay        STRIDEContractType = "agent_profile_overlay"
	STRIDEContractAgentCapabilityManifest    STRIDEContractType = "agent_capability_manifest"
	STRIDEContractChannelNormProfile         STRIDEContractType = "channel_norm_profile"
	STRIDEContractAgentRelationshipMemory    STRIDEContractType = "agent_relationship_memory"
	STRIDEContractAgentContextEnvelope       STRIDEContractType = "agent_context_envelope"
	STRIDEContractDelegationRun              STRIDEContractType = "delegation_run"
	STRIDEContractRichMessagePart            STRIDEContractType = "rich_message_part"
	STRIDEContractMeetingAgentInvitation     STRIDEContractType = "meeting_agent_invitation"
	STRIDEContractMeetingSpecialistContext   STRIDEContractType = "meeting_specialist_context"
	STRIDEContractMeetingAgentSession        STRIDEContractType = "meeting_agent_session"
	STRIDEContractMeetingAgentContribution   STRIDEContractType = "meeting_agent_contribution"
	STRIDEContractAgentPackageManifest       STRIDEContractType = "agent_package_manifest"
	STRIDEContractMarketplaceListing         STRIDEContractType = "marketplace_listing"
	STRIDEContractTeamAgent                  STRIDEContractType = "team_agent"
	STRIDEContractAgentAssignment            STRIDEContractType = "agent_assignment"
	STRIDEContractAgentLearningRecord        STRIDEContractType = "agent_learning_record"
	STRIDEContractAgentPerformanceReceipt    STRIDEContractType = "agent_performance_receipt"
	STRIDEContractAgentUpdateProposal        STRIDEContractType = "agent_update_proposal"
	STRIDEContractWorkforcePolicy            STRIDEContractType = "workforce_policy"
	STRIDEContractPersonPrincipal            STRIDEContractType = "person_principal"
	STRIDEContractPersonProfile              STRIDEContractType = "person_profile"
	STRIDEContractOrganizationMemberProfile  STRIDEContractType = "organization_member_profile"
	STRIDEContractOrganization               STRIDEContractType = "organization"
	STRIDEContractOrganizationMembership     STRIDEContractType = "organization_membership"
	STRIDEContractOrganizationJoinRequest    STRIDEContractType = "organization_join_request"
	STRIDEContractActiveOrganizationSession  STRIDEContractType = "active_organization_session"
	STRIDEContractOrganizationAuditEvent     STRIDEContractType = "organization_audit_event"
	STRIDEContractContributionClaim          STRIDEContractType = "contribution_claim"
	STRIDEContractContributionAttestation    STRIDEContractType = "contribution_attestation"
	STRIDEContractPublishedContributionClaim STRIDEContractType = "published_contribution_claim"
	STRIDEContractAgentInfluenceReceipt      STRIDEContractType = "agent_influence_receipt"
	STRIDEContractFieldReleaseApproval       STRIDEContractType = "field_release_approval"
	STRIDEContractNetworkProfileProjection   STRIDEContractType = "network_profile_projection"
	STRIDEContractTalentSearchGrant          STRIDEContractType = "talent_search_grant"
	STRIDEContractNetworkSearchReceipt       STRIDEContractType = "network_search_receipt"
	STRIDEContractContactRequest             STRIDEContractType = "contact_request"
	STRIDEContractNetworkBlock               STRIDEContractType = "network_block"
	STRIDEContractDerivedPurgeReceipt        STRIDEContractType = "derived_purge_receipt"
	STRIDEContractWorkspaceMembership        STRIDEContractType = "workspace_membership"
	STRIDEContractMyMindSource               STRIDEContractType = "mymind_source"
	STRIDEContractMyMindDisclosureGrant      STRIDEContractType = "mymind_disclosure_grant"
	STRIDEContractMyMindCustodyDeletion      STRIDEContractType = "mymind_custody_deletion_receipt"
	STRIDEContractArtifactDisposition        STRIDEContractType = "artifact_disposition"
	STRIDEContractProject                    STRIDEContractType = "project"
	STRIDEContractProjectThreadBinding       STRIDEContractType = "project_thread_binding"
	STRIDEContractProjectAssociation         STRIDEContractType = "project_association"
	STRIDEContractProjectAssociationEvent    STRIDEContractType = "project_association_event"
)

// STRIDEContractHeader is the immutable, body-free identity shared by every
// durable STRIDE contract revision.  Content bodies live behind ACL-governed
// revision references; only their digest belongs in an operational audit row.
type STRIDEContractHeader struct {
	TenantID      string             `json:"tenantId"`
	ID            string             `json:"id"`
	Revision      int64              `json:"revision"`
	SchemaVersion int                `json:"schemaVersion"`
	ContractType  STRIDEContractType `json:"contractType"`
	ContentDigest string             `json:"contentDigest"`
	CreatedAt     time.Time          `json:"createdAt"`
}

func (h STRIDEContractHeader) Validate(want STRIDEContractType) error {
	if !strideIdentifier(h.TenantID) || !strideIdentifier(h.ID) || h.Revision < 1 || h.SchemaVersion != STRIDEContractSchemaVersion ||
		h.ContractType != want || !isHexDigest(h.ContentDigest) || h.CreatedAt.IsZero() {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type STRIDEReference struct {
	ContractType STRIDEContractType `json:"contractType"`
	ID           string             `json:"id"`
	Revision     int64              `json:"revision"`
	Digest       string             `json:"digest"`
}

func (r STRIDEReference) Validate() error {
	if !validSTRIDEContractType(r.ContractType) || !strideIdentifier(r.ID) || r.Revision < 1 || !isHexDigest(r.Digest) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type STRIDEAudience struct {
	Visibility string   `json:"visibility"`
	Principals []string `json:"principals"`
}

func (a STRIDEAudience) Validate() error {
	if !oneOf(a.Visibility, "private", "project", "channel", "organization", "meeting") || len(a.Principals) == 0 || !uniqueSTRIDEIDs(a.Principals) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type ConversationEvent struct {
	Header            STRIDEContractHeader `json:"header"`
	SourceType        string               `json:"sourceType"`
	SourceID          string               `json:"sourceId"`
	RoomID            string               `json:"roomId,omitempty"`
	SittingID         string               `json:"sittingId,omitempty"`
	ThreadID          string               `json:"threadId,omitempty"`
	AuthorPrincipal   string               `json:"authorPrincipal"`
	AuthorName        string               `json:"authorName"`
	OccurredAt        time.Time            `json:"occurredAt"`
	IngestedAt        time.Time            `json:"ingestedAt"`
	EventType         string               `json:"eventType"`
	ContentRevision   int64                `json:"contentRevision"`
	ContentDigest     string               `json:"contentDigest"`
	SupersedesEventID string               `json:"supersedesEventId,omitempty"`
	ReplyToEventID    string               `json:"replyToEventId,omitempty"`
	Audience          STRIDEAudience       `json:"audience"`
	ACLVersion        int64                `json:"aclVersion"`
	RetentionPolicy   string               `json:"retentionPolicy"`
	PurgeGeneration   int64                `json:"purgeGeneration"`
	StructuredRefs    []STRIDEReference    `json:"structuredRefs,omitempty"`
	// AttachmentRefs and LinkRefs keep the body-free event useful to an
	// authorized projector without making it guess which rich-message parts
	// are files versus links. ReactionActors is the complete current set for a
	// reaction mutation, so clearing a reaction is representable instead of
	// becoming an append-only false positive.
	AttachmentRefs []STRIDEReference `json:"attachmentRefs,omitempty"`
	LinkRefs       []STRIDEReference `json:"linkRefs,omitempty"`
	// Do not omit an empty reactionActors array: reaction mutations carry the
	// complete current set, so [] is the durable representation of clearing the
	// last reaction. A missing/null field remains reserved for legacy snapshots
	// that recorded only the mutating actor.
	ReactionActors     []string `json:"reactionActors"`
	BodyRef            string   `json:"bodyRef,omitempty"`
	Provenance         string   `json:"provenance"`
	OnBehalfOf         string   `json:"onBehalfOf,omitempty"`
	ProviderItemIDHash string   `json:"providerItemIdHash,omitempty"`
}

func (v ConversationEvent) Validate() error {
	if v.Header.Validate(STRIDEContractConversationEvent) != nil || !strideIdentifier(v.SourceType) || !strideIdentifier(v.SourceID) ||
		!strideIdentifier(v.AuthorPrincipal) || strings.TrimSpace(v.AuthorName) == "" || v.OccurredAt.IsZero() || v.IngestedAt.IsZero() ||
		!oneOf(v.EventType, "message", "edit", "delete", "reply", "reaction", "file", "link", "transcript_turn", "consent_change", "agent_session_status", "agent_contribution") ||
		v.ContentRevision < 1 || !isHexDigest(v.ContentDigest) || v.Audience.Validate() != nil || v.ACLVersion < 1 || !strideIdentifier(v.RetentionPolicy) ||
		v.PurgeGeneration < 0 || !oneOf(v.Provenance, "client", "server", "tool", "provider") || !validOptionalSTRIDEID(v.SupersedesEventID) || !validOptionalSTRIDEID(v.ReplyToEventID) ||
		!validOptionalSTRIDEID(v.BodyRef) || !validOptionalDigest(v.ProviderItemIDHash) || !validateOptionalSTRIDERefs(v.StructuredRefs) ||
		!validateOptionalSTRIDERefs(v.AttachmentRefs) || !validateOptionalSTRIDERefs(v.LinkRefs) || (len(v.ReactionActors) > 0 && !uniqueSTRIDEIDs(v.ReactionActors)) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type TranscriptSegment struct {
	Header          STRIDEContractHeader `json:"header"`
	ConversationRef STRIDEReference      `json:"conversationRef"`
	RoomID          string               `json:"roomId"`
	SittingID       string               `json:"sittingId"`
	MediaGeneration uint64               `json:"mediaGeneration"`
	CaptureSequence uint64               `json:"captureSequence"`
	SourceStart     time.Time            `json:"sourceStart"`
	SourceEnd       time.Time            `json:"sourceEnd"`
	ProviderItemID  string               `json:"providerItemIdHash,omitempty"`
	Status          string               `json:"status"`
	Speaker         string               `json:"speaker"`
	Attribution     string               `json:"attribution"`
	ConsentScopes   []string             `json:"consentScopes"`
	ModelDigest     string               `json:"modelDigest"`
	ConfigDigest    string               `json:"configDigest"`
	ContextDigest   string               `json:"contextDigest"`
	LanguageHints   []string             `json:"languageHints,omitempty"`
	SupersedesID    string               `json:"supersedesId,omitempty"`
	CreatedAt       time.Time            `json:"createdAt"`
}

func (v TranscriptSegment) Validate() error {
	if v.Header.Validate(STRIDEContractTranscriptSegment) != nil || v.ConversationRef.Validate() != nil || !strideIdentifier(v.RoomID) || !strideIdentifier(v.SittingID) ||
		v.MediaGeneration == 0 || v.CaptureSequence == 0 || v.SourceStart.IsZero() || v.SourceEnd.IsZero() || !v.SourceStart.Before(v.SourceEnd) ||
		!oneOf(v.Status, "capturing", "live_partial", "live_final", "authoritative_final", "degraded_final", "failed", "corrected", "superseded", "retracted") ||
		!strideIdentifier(v.Speaker) || !strideIdentifier(v.Attribution) || len(v.ConsentScopes) == 0 || !uniqueSTRIDEIDs(v.ConsentScopes) ||
		!isHexDigest(v.ModelDigest) || !isHexDigest(v.ConfigDigest) || !isHexDigest(v.ContextDigest) || !validOptionalDigest(v.ProviderItemID) || !validOptionalSTRIDEID(v.SupersedesID) || v.CreatedAt.IsZero() {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type TranscriptRevision struct {
	Header       STRIDEContractHeader `json:"header"`
	SegmentID    string               `json:"segmentId"`
	Revision     int64                `json:"revision"`
	TextDigest   string               `json:"textDigest"`
	Status       string               `json:"status"`
	SupersedesID string               `json:"supersedesId,omitempty"`
	Evidence     []STRIDEReference    `json:"evidence"`
}

func (v TranscriptRevision) Validate() error {
	return combineContract(validateHeaderAndRefs(v.Header, STRIDEContractTranscriptRevision, v.Evidence), !strideIdentifier(v.SegmentID) || v.Revision < 1 || !isHexDigest(v.TextDigest) || !oneOf(v.Status, "authoritative_final", "degraded_final", "corrected", "superseded", "retracted") || !validOptionalSTRIDEID(v.SupersedesID))
}

type AnalysisProjection struct {
	Header              STRIDEContractHeader `json:"header"`
	Kind                string               `json:"kind"`
	SourceRefs          []STRIDEReference    `json:"sourceRefs"`
	WindowStart         time.Time            `json:"windowStart"`
	WindowEnd           time.Time            `json:"windowEnd"`
	ThroughSegmentID    string               `json:"throughSegmentId"`
	SourceHighWater     uint64               `json:"sourceHighWater"`
	ProjectionHighWater uint64               `json:"projectionHighWater"`
	ModelDigest         string               `json:"modelDigest"`
	PromptDigest        string               `json:"promptDigest"`
	EvidenceDigest      string               `json:"evidenceDigest"`
	Confidence          float64              `json:"confidence"`
	Audience            STRIDEAudience       `json:"audience"`
	SupersedesID        string               `json:"supersedesId,omitempty"`
	FreshThrough        time.Time            `json:"freshThrough"`
}

func (v AnalysisProjection) Validate() error {
	if v.Header.Validate(STRIDEContractAnalysisProjection) != nil || !oneOf(v.Kind, "decision", "commitment", "blocker", "storyline", "alignment", "divergence", "position", "open_question", "entity", "project", "topic", "link", "file", "artifact", "vocabulary", "alias", "work_intent_candidate") ||
		!validateSTRIDERefs(v.SourceRefs) || v.WindowStart.IsZero() || v.WindowEnd.IsZero() || !v.WindowStart.Before(v.WindowEnd) || !strideIdentifier(v.ThroughSegmentID) ||
		!isHexDigest(v.ModelDigest) || !isHexDigest(v.PromptDigest) || !isHexDigest(v.EvidenceDigest) || v.Confidence < 0 || v.Confidence > 1 || v.Audience.Validate() != nil || !validOptionalSTRIDEID(v.SupersedesID) || v.FreshThrough.IsZero() {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type KnowledgeAssertion struct {
	Header     STRIDEContractHeader `json:"header"`
	Status     string               `json:"status"`
	Confidence float64              `json:"confidence"`
	Evidence   []STRIDEReference    `json:"evidence"`
	Audience   STRIDEAudience       `json:"audience"`
	Supersedes string               `json:"supersedes,omitempty"`
}

func (v KnowledgeAssertion) Validate() error {
	if v.Header.Validate(STRIDEContractKnowledgeAssertion) != nil || !oneOf(v.Status, "asserted", "inferred", "unsupported", "superseded", "retracted") || v.Confidence < 0 || v.Confidence > 1 ||
		!validateSTRIDERefs(v.Evidence) || (v.Status == "asserted" && len(v.Evidence) == 0) || v.Audience.Validate() != nil || !validOptionalSTRIDEID(v.Supersedes) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type CollaborationPreference struct {
	Header             STRIDEContractHeader `json:"header"`
	SubjectPrincipal   string               `json:"subjectPrincipal"`
	Scope              string               `json:"scope"`
	PreferenceType     string               `json:"preferenceType"`
	ValueDigest        string               `json:"valueDigest"`
	Origin             string               `json:"origin"`
	Evidence           []STRIDEReference    `json:"evidence"`
	Confidence         float64              `json:"confidence"`
	FirstObserved      time.Time            `json:"firstObserved"`
	LastObserved       time.Time            `json:"lastObserved"`
	ReinforcementCount int                  `json:"reinforcementCount"`
	ExpiresAt          *time.Time           `json:"expiresAt,omitempty"`
	Audience           STRIDEAudience       `json:"audience"`
	Status             string               `json:"status"`
	CorrectionHistory  []STRIDEReference    `json:"correctionHistory,omitempty"`
}

func (v CollaborationPreference) Validate() error {
	if v.Header.Validate(STRIDEContractCollaborationPreference) != nil || !strideIdentifier(v.SubjectPrincipal) || !strideIdentifier(v.Scope) || !strideIdentifier(v.PreferenceType) ||
		!isHexDigest(v.ValueDigest) || !oneOf(v.Origin, "explicit", "inferred") || !validateSTRIDERefs(v.Evidence) || v.Confidence < 0 || v.Confidence > 1 || v.FirstObserved.IsZero() || v.LastObserved.Before(v.FirstObserved) || v.ReinforcementCount < 0 ||
		(v.ExpiresAt != nil && !v.ExpiresAt.After(v.LastObserved)) || v.Audience.Validate() != nil || !oneOf(v.Status, "active", "corrected", "expired", "forgotten", "superseded") || !validateOptionalSTRIDERefs(v.CorrectionHistory) || sensitivePreference(v.PreferenceType) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type WorkIntent struct {
	Header          STRIDEContractHeader `json:"header"`
	OutcomeDigest   string               `json:"outcomeDigest"`
	Sources         []STRIDEReference    `json:"sources"`
	People          []string             `json:"people"`
	Projects        []string             `json:"projects"`
	Confidence      float64              `json:"confidence"`
	Counterevidence []STRIDEReference    `json:"counterevidence,omitempty"`
	Status          string               `json:"status"`
}

func (v WorkIntent) Validate() error {
	return combineContract(validateHeaderAndRefs(v.Header, STRIDEContractWorkIntent, v.Sources), !isHexDigest(v.OutcomeDigest) || !uniqueSTRIDEIDs(v.People) || !uniqueSTRIDEIDs(v.Projects) || v.Confidence < 0 || v.Confidence > 1 || !validateOptionalSTRIDERefs(v.Counterevidence) || !oneOf(v.Status, "candidate", "proposed", "dismissed", "expired", "superseded"))
}

type ApprovalPolicy struct {
	ID                       string    `json:"id"`
	Revision                 int64     `json:"revision"`
	EligiblePrincipals       []string  `json:"eligiblePrincipals"`
	Quorum                   int       `json:"quorum"`
	SourceReadRequired       bool      `json:"sourceReadRequired"`
	DestinationWriteRequired bool      `json:"destinationWriteRequired"`
	SeparationOfDuties       bool      `json:"separationOfDuties"`
	SideEffectClass          string    `json:"sideEffectClass"`
	ExpiresAt                time.Time `json:"expiresAt"`
	InvalidationDigest       string    `json:"invalidationDigest"`
}

func (v ApprovalPolicy) Validate() error {
	if !strideIdentifier(v.ID) || v.Revision < 1 || !uniqueSTRIDEIDs(v.EligiblePrincipals) || v.Quorum < 1 || v.Quorum > len(v.EligiblePrincipals) || !oneOf(v.SideEffectClass, "read_only", "internal_write", "external_write", "production", "financial", "credential", "destructive") || v.ExpiresAt.IsZero() || !isHexDigest(v.InvalidationDigest) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type WorkProposal struct {
	Header                  STRIDEContractHeader `json:"header"`
	EvidenceSnapshot        STRIDEReference      `json:"evidenceSnapshot"`
	OutcomeDigest           string               `json:"outcomeDigest"`
	WorkflowProfile         string               `json:"workflowProfile"`
	Destination             string               `json:"destination"`
	Participants            []string             `json:"participants"`
	Owner                   string               `json:"owner"`
	Reviewer                string               `json:"reviewer"`
	Authority               string               `json:"authority"`
	BudgetCents             int64                `json:"budgetCents"`
	TimeEstimateSeconds     int64                `json:"timeEstimateSeconds"`
	GeneratedArtifactPolicy string               `json:"generatedArtifactPolicy"`
	ExpiresAt               time.Time            `json:"expiresAt"`
	Approval                ApprovalPolicy       `json:"approval"`
}

func (v WorkProposal) Validate() error {
	return combineContract(validateHeader(v.Header, STRIDEContractWorkProposal), v.EvidenceSnapshot.Validate() != nil || !isHexDigest(v.OutcomeDigest) || !strideIdentifier(v.WorkflowProfile) || !strideIdentifier(v.Destination) || !uniqueSTRIDEIDs(v.Participants) || !strideIdentifier(v.Owner) || !strideIdentifier(v.Reviewer) || !oneOf(v.Authority, "read_only", "internal_write", "external_write", "production", "financial", "credential", "destructive") || v.BudgetCents < 0 || v.TimeEstimateSeconds < 0 || !strideIdentifier(v.GeneratedArtifactPolicy) || v.ExpiresAt.IsZero() || v.Approval.Validate() != nil)
}

type WorkRun struct {
	Header               STRIDEContractHeader `json:"header"`
	Proposal             STRIDEReference      `json:"proposal"`
	IdempotencyKeyDigest string               `json:"idempotencyKeyDigest"`
	StageGraphDigest     string               `json:"stageGraphDigest"`
	RouteRefs            []STRIDEReference    `json:"routeRefs"`
	CheckpointRefs       []STRIDEReference    `json:"checkpointRefs,omitempty"`
	Attempts             int                  `json:"attempts"`
	UsageDigest          string               `json:"usageDigest"`
	Status               string               `json:"status"`
	Artifacts            []STRIDEReference    `json:"artifacts,omitempty"`
	CriticVerdicts       []STRIDEReference    `json:"criticVerdicts,omitempty"`
	ApprovalRefs         []STRIDEReference    `json:"approvalRefs"`
	TerminalEvidence     []STRIDEReference    `json:"terminalEvidence,omitempty"`
}

func (v WorkRun) Validate() error {
	return combineContract(validateHeaderAndRefs(v.Header, STRIDEContractWorkRun, v.RouteRefs), v.Proposal.Validate() != nil || !isHexDigest(v.IdempotencyKeyDigest) || !isHexDigest(v.StageGraphDigest) || !validateOptionalSTRIDERefs(v.CheckpointRefs) || v.Attempts < 0 || !isHexDigest(v.UsageDigest) || !oneOf(v.Status, "queued", "running", "awaiting_input", "awaiting_review", "blocked", "completed", "failed", "cancelled") || !validateOptionalSTRIDERefs(v.Artifacts) || !validateOptionalSTRIDERefs(v.CriticVerdicts) || !validateSTRIDERefs(v.ApprovalRefs) || !validateOptionalSTRIDERefs(v.TerminalEvidence))
}

type OutcomeRecord struct {
	Header            STRIDEContractHeader `json:"header"`
	WorkRun           STRIDEReference      `json:"workRun"`
	CriteriaDigest    string               `json:"criteriaDigest"`
	Verdict           string               `json:"verdict"`
	Artifacts         []STRIDEReference    `json:"artifacts"`
	DestinationThread string               `json:"destinationThread"`
	CompletedAt       time.Time            `json:"completedAt"`
	Reviewer          string               `json:"reviewer"`
	CaveatDigest      string               `json:"caveatDigest,omitempty"`
}

func (v OutcomeRecord) Validate() error {
	return combineContract(validateHeaderAndRefs(v.Header, STRIDEContractOutcome, v.Artifacts), v.WorkRun.Validate() != nil || !isHexDigest(v.CriteriaDigest) || !oneOf(v.Verdict, "accepted", "rejected", "partial") || !strideIdentifier(v.DestinationThread) || v.CompletedAt.IsZero() || !strideIdentifier(v.Reviewer) || !validOptionalDigest(v.CaveatDigest))
}

type AnswerEnvelope struct {
	Header              STRIDEContractHeader `json:"header"`
	AnswerDigest        string               `json:"answerDigest"`
	Evidence            []STRIDEReference    `json:"evidence"`
	Temporal            TemporalQuery        `json:"temporal"`
	TranscriptHighWater uint64               `json:"transcriptHighWater"`
	AnalysisHighWater   uint64               `json:"analysisHighWater"`
	BrainHighWater      uint64               `json:"brainHighWater"`
	Coverage            RecallCoverage       `json:"coverage"`
	Publication         string               `json:"publication"`
	AudienceAuthorized  bool                 `json:"audienceAuthorized"`
}

func (v AnswerEnvelope) validate(kind STRIDEContractType) error {
	if v.Header.Validate(kind) != nil || !isHexDigest(v.AnswerDigest) || !validateSTRIDERefs(v.Evidence) || v.Temporal.Validate() != nil || v.Coverage.Validate() != nil || !oneOf(v.Publication, "speak_and_post", "post_only", "private_only", "refuse") || !v.AudienceAuthorized {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type MeetingAnswerEnvelope AnswerEnvelope

func (v MeetingAnswerEnvelope) Validate() error {
	return AnswerEnvelope(v).validate(STRIDEContractMeetingAnswer)
}

type CompanyAnswerEnvelope AnswerEnvelope

func (v CompanyAnswerEnvelope) Validate() error {
	return AnswerEnvelope(v).validate(STRIDEContractCompanyAnswer)
}

type AgentCoreProfile struct {
	Header           STRIDEContractHeader `json:"header"`
	AgentID          string               `json:"agentId"`
	DisplayName      string               `json:"displayName"`
	Pronunciation    string               `json:"pronunciation"`
	AvatarRef        string               `json:"avatarRef"`
	Role             string               `json:"role"`
	MissionDigest    string               `json:"missionDigest"`
	StyleDigest      string               `json:"styleDigest"`
	Traits           []string             `json:"traits"`
	HumorRange       string               `json:"humorRange"`
	Values           []string             `json:"values"`
	Boundaries       []string             `json:"boundaries"`
	Prohibited       []string             `json:"prohibited"`
	EscalationPolicy string               `json:"escalationPolicy"`
	Owner            string               `json:"owner"`
	Status           string               `json:"status"`
}

func (v AgentCoreProfile) Validate() error {
	return combineContract(validateAgentProfileHeader(v.Header, STRIDEContractAgentCoreProfile, v.AgentID, v.DisplayName, v.Role, v.Owner), !strideIdentifier(v.Pronunciation) || !validOptionalSTRIDEID(v.AvatarRef) || !isHexDigest(v.MissionDigest) || !isHexDigest(v.StyleDigest) || !uniqueSTRIDEIDs(v.Traits) || !oneOf(v.HumorRange, "none", "low", "medium", "high") || !uniqueSTRIDEIDs(v.Values) || !uniqueSTRIDEIDs(v.Boundaries) || !uniqueSTRIDEIDs(v.Prohibited) || !strideIdentifier(v.EscalationPolicy) || !oneOf(v.Status, "draft", "active", "paused", "retired"))
}

type AgentProfileOverlay struct {
	Header            STRIDEContractHeader `json:"header"`
	TeamAgentID       string               `json:"teamAgentId"`
	BasePackage       STRIDEReference      `json:"basePackage"`
	Author            string               `json:"author"`
	IdentityDigest    string               `json:"identityDigest"`
	PersonalityDigest string               `json:"personalityDigest"`
	VoiceDigest       string               `json:"voiceDigest"`
	MissionDigest     string               `json:"missionDigest"`
	BoundariesDigest  string               `json:"boundariesDigest"`
	DiffDigest        string               `json:"diffDigest"`
	ContinuityEvals   []STRIDEReference    `json:"continuityEvals"`
	Status            string               `json:"status"`
	RollbackRef       *STRIDEReference     `json:"rollbackRef,omitempty"`
}

func (v AgentProfileOverlay) Validate() error {
	return combineContract(validateHeaderAndRefs(v.Header, STRIDEContractAgentProfileOverlay, v.ContinuityEvals), !strideIdentifier(v.TeamAgentID) || v.BasePackage.Validate() != nil || !strideIdentifier(v.Author) || !allDigests(v.IdentityDigest, v.PersonalityDigest, v.VoiceDigest, v.MissionDigest, v.BoundariesDigest, v.DiffDigest) || !oneOf(v.Status, "draft", "active", "superseded", "reverted") || (v.RollbackRef != nil && v.RollbackRef.Validate() != nil))
}

type AgentCapabilityManifest struct {
	Header              STRIDEContractHeader `json:"header"`
	AgentID             string               `json:"agentId"`
	Profile             STRIDEReference      `json:"profile"`
	Surfaces            []string             `json:"surfaces"`
	Memberships         []string             `json:"memberships"`
	ToolRoles           []string             `json:"toolRoles"`
	WorkflowRoles       []string             `json:"workflowRoles"`
	RoutePolicy         string               `json:"routePolicy"`
	MemoryScopes        []string             `json:"memoryScopes"`
	DataClassifications []string             `json:"dataClassifications"`
	SideEffectClasses   []string             `json:"sideEffectClasses"`
	PerCallBudgetCents  int64                `json:"perCallBudgetCents"`
	PerRunBudgetCents   int64                `json:"perRunBudgetCents"`
	MaxHops             int                  `json:"maxHops"`
	Concurrency         int                  `json:"concurrency"`
	ExpiresAt           time.Time            `json:"expiresAt"`
	KillSwitches        []string             `json:"killSwitches"`
}

func (v AgentCapabilityManifest) Validate() error {
	return combineContract(validateHeader(v.Header, STRIDEContractAgentCapabilityManifest), !strideIdentifier(v.AgentID) || v.Profile.Validate() != nil || !uniqueSTRIDEIDs(v.Surfaces) || !uniqueSTRIDEIDs(v.Memberships) || !uniqueSTRIDEIDs(v.ToolRoles) || !uniqueSTRIDEIDs(v.WorkflowRoles) || !strideIdentifier(v.RoutePolicy) || !uniqueSTRIDEIDs(v.MemoryScopes) || !uniqueSTRIDEIDs(v.DataClassifications) || !uniqueSTRIDEIDs(v.SideEffectClasses) || v.PerCallBudgetCents < 0 || v.PerRunBudgetCents < 0 || v.MaxHops < 0 || v.MaxHops > 1 || v.Concurrency < 1 || v.ExpiresAt.IsZero() || !uniqueSTRIDEIDs(v.KillSwitches))
}

type ChannelNormProfile struct {
	Header                STRIDEContractHeader `json:"header"`
	ChannelID             string               `json:"channelId"`
	Purpose               string               `json:"purpose"`
	Audience              STRIDEAudience       `json:"audience"`
	MemoryDisclosure      bool                 `json:"memoryDisclosure"`
	WorkSensingDisclosure bool                 `json:"workSensingDisclosure"`
	Tone                  string               `json:"tone"`
	ResponseLength        string               `json:"responseLength"`
	HumorPolicy           string               `json:"humorPolicy"`
	GIFPolicy             string               `json:"gifPolicy"`
	ProactivePolicy       string               `json:"proactivePolicy"`
	RetentionPolicy       string               `json:"retentionPolicy"`
}

func (v ChannelNormProfile) Validate() error {
	return combineContract(validateHeader(v.Header, STRIDEContractChannelNormProfile), !strideIdentifier(v.ChannelID) || !strideIdentifier(v.Purpose) || v.Audience.Validate() != nil || !strideIdentifier(v.Tone) || !oneOf(v.ResponseLength, "brief", "normal", "detailed") || !oneOf(v.HumorPolicy, "disabled", "restrained", "enabled") || !oneOf(v.GIFPolicy, "disabled", "low_risk_only") || !oneOf(v.ProactivePolicy, "disabled", "quiet") || !strideIdentifier(v.RetentionPolicy))
}

type AgentRelationshipMemory struct {
	Header             STRIDEContractHeader `json:"header"`
	AgentID            string               `json:"agentId"`
	Subject            string               `json:"subject"`
	Scope              string               `json:"scope"`
	ObservationDigest  string               `json:"observationDigest"`
	Evidence           []STRIDEReference    `json:"evidence"`
	Confidence         float64              `json:"confidence"`
	FirstObserved      time.Time            `json:"firstObserved"`
	LastObserved       time.Time            `json:"lastObserved"`
	ReinforcementCount int                  `json:"reinforcementCount"`
	Audience           STRIDEAudience       `json:"audience"`
	ExpiresAt          *time.Time           `json:"expiresAt,omitempty"`
	Status             string               `json:"status"`
	CorrectionHistory  []STRIDEReference    `json:"correctionHistory,omitempty"`
}

func (v AgentRelationshipMemory) Validate() error {
	return combineContract(validateHeaderAndRefs(v.Header, STRIDEContractAgentRelationshipMemory, v.Evidence), !strideIdentifier(v.AgentID) || !strideIdentifier(v.Subject) || !strideIdentifier(v.Scope) || !isHexDigest(v.ObservationDigest) || v.Confidence < 0 || v.Confidence > 1 || v.FirstObserved.IsZero() || v.LastObserved.Before(v.FirstObserved) || v.ReinforcementCount < 0 || v.Audience.Validate() != nil || (v.ExpiresAt != nil && !v.ExpiresAt.After(v.LastObserved)) || !oneOf(v.Status, "present", "absent", "unavailable", "denied", "superseded", "forgotten") || !validateOptionalSTRIDERefs(v.CorrectionHistory))
}

type AgentContextEnvelope struct {
	Header            STRIDEContractHeader `json:"header"`
	AgentProfile      STRIDEReference      `json:"agentProfile"`
	Capability        STRIDEReference      `json:"capability"`
	ChannelPolicy     STRIDEReference      `json:"channelPolicy"`
	InvocationSurface string               `json:"invocationSurface"`
	InvocationReason  string               `json:"invocationReason"`
	Requester         string               `json:"requester"`
	Recipients        []string             `json:"recipients"`
	CurrentTurn       STRIDEReference      `json:"currentTurn"`
	RecentTurns       []STRIDEReference    `json:"recentTurns"`
	Evidence          []STRIDEReference    `json:"evidence"`
	Preferences       []STRIDEReference    `json:"preferences"`
	ActiveWork        []STRIDEReference    `json:"activeWork"`
	ResponseModes     []string             `json:"responseModes"`
	PermittedTools    []string             `json:"permittedTools"`
	Audience          STRIDEAudience       `json:"audience"`
	CoverageDigest    string               `json:"coverageDigest"`
	ContextDigest     string               `json:"contextDigest"`
}

func (v AgentContextEnvelope) Validate() error {
	return combineContract(validateHeaderAndRefs(v.Header, STRIDEContractAgentContextEnvelope, v.RecentTurns), v.AgentProfile.Validate() != nil || v.Capability.Validate() != nil || v.ChannelPolicy.Validate() != nil || !strideIdentifier(v.InvocationSurface) || !strideIdentifier(v.InvocationReason) || !strideIdentifier(v.Requester) || !uniqueSTRIDEIDs(v.Recipients) || v.CurrentTurn.Validate() != nil || !validateSTRIDERefs(v.Evidence) || !validateOptionalSTRIDERefs(v.Preferences) || !validateOptionalSTRIDERefs(v.ActiveWork) || !uniqueSTRIDEIDs(v.ResponseModes) || !uniqueSTRIDEIDs(v.PermittedTools) || v.Audience.Validate() != nil || !isHexDigest(v.CoverageDigest) || !isHexDigest(v.ContextDigest))
}

type DelegationRun struct {
	Header            STRIDEContractHeader `json:"header"`
	ParentWork        *STRIDEReference     `json:"parentWork,omitempty"`
	SourceAgent       string               `json:"sourceAgent"`
	DestinationAgent  string               `json:"destinationAgent"`
	StageID           string               `json:"stageId"`
	InputScope        []STRIDEReference    `json:"inputScope"`
	DestinationThread string               `json:"destinationThread"`
	OutputContract    string               `json:"outputContract"`
	Tools             []string             `json:"tools"`
	Authority         string               `json:"authority"`
	MaxHops           int                  `json:"maxHops"`
	TimeBudgetSeconds int64                `json:"timeBudgetSeconds"`
	TokenBudget       int64                `json:"tokenBudget"`
	CostBudgetCents   int64                `json:"costBudgetCents"`
	FenceDigest       string               `json:"fenceDigest"`
	Status            string               `json:"status"`
}

func (v DelegationRun) Validate() error {
	return combineContract(validateHeaderAndRefs(v.Header, STRIDEContractDelegationRun, v.InputScope), (v.ParentWork != nil && v.ParentWork.Validate() != nil) || !strideIdentifier(v.SourceAgent) || !strideIdentifier(v.DestinationAgent) || !strideIdentifier(v.StageID) || !strideIdentifier(v.DestinationThread) || !strideIdentifier(v.OutputContract) || !uniqueSTRIDEIDs(v.Tools) || !oneOf(v.Authority, "read_only", "internal_write") || v.MaxHops != 0 || v.TimeBudgetSeconds < 0 || v.TokenBudget < 0 || v.CostBudgetCents < 0 || !isHexDigest(v.FenceDigest) || !oneOf(v.Status, "queued", "running", "completed", "failed", "cancelled"))
}

type RichMessagePart struct {
	Header                STRIDEContractHeader `json:"header"`
	Kind                  string               `json:"kind"`
	TextDigest            string               `json:"textDigest,omitempty"`
	Evidence              *STRIDEReference     `json:"evidence,omitempty"`
	Asset                 *STRIDEReference     `json:"asset,omitempty"`
	MIME                  string               `json:"mime,omitempty"`
	SizeBytes             int64                `json:"sizeBytes,omitempty"`
	ProviderIDHash        string               `json:"providerIdHash,omitempty"`
	SafeQueryClass        string               `json:"safeQueryClass,omitempty"`
	Rating                string               `json:"rating,omitempty"`
	AltTextDigest         string               `json:"altTextDigest,omitempty"`
	SelectionReasonDigest string               `json:"selectionReasonDigest,omitempty"`
	ProfileRevision       *STRIDEReference     `json:"profileRevision,omitempty"`
}

func (v RichMessagePart) Validate() error {
	if v.Header.Validate(STRIDEContractRichMessagePart) != nil || !oneOf(v.Kind, "text", "evidence_chip", "link_card", "artifact", "file", "image", "gif") || v.SizeBytes < 0 || (v.Evidence != nil && v.Evidence.Validate() != nil) || (v.Asset != nil && v.Asset.Validate() != nil) || (v.ProfileRevision != nil && v.ProfileRevision.Validate() != nil) {
		return ErrSTRIDEContractInvalid
	}
	switch v.Kind {
	case "text":
		if !isHexDigest(v.TextDigest) || v.Evidence != nil || v.Asset != nil {
			return ErrSTRIDEContractInvalid
		}
	case "evidence_chip":
		if v.Evidence == nil || v.Asset != nil {
			return ErrSTRIDEContractInvalid
		}
	case "link_card", "artifact", "file", "image":
		if v.Asset == nil || !validOptionalDigest(v.TextDigest) {
			return ErrSTRIDEContractInvalid
		}
	case "gif":
		if v.Asset == nil || !isHexDigest(v.ProviderIDHash) || !strideIdentifier(v.SafeQueryClass) || !oneOf(v.Rating, "g", "pg") || !isHexDigest(v.AltTextDigest) || !isHexDigest(v.SelectionReasonDigest) || v.ProfileRevision == nil {
			return ErrSTRIDEContractInvalid
		}
	}
	return nil
}

type MeetingAgentInvitation struct {
	Header            STRIDEContractHeader `json:"header"`
	RoomID            string               `json:"roomId"`
	SittingID         string               `json:"sittingId"`
	SpecialistProfile STRIDEReference      `json:"specialistProfile"`
	Capability        STRIDEReference      `json:"capability"`
	// Eligibility binds the human decision to the exact current agent,
	// assignment, room, and workforce capability state. It is optional only so
	// pre-field signed snapshots can be restored and terminally reauthorized;
	// every new specialist request and every provider launch requires it.
	Eligibility           *STRIDEReference `json:"eligibility,omitempty"`
	Requester             string           `json:"requester"`
	EligibleConfirmer     string           `json:"eligibleConfirmer"`
	PurposeDigest         string           `json:"purposeDigest"`
	ContextClasses        []string         `json:"contextClasses"`
	SourceIntervalDigest  string           `json:"sourceIntervalDigest"`
	Audience              STRIDEAudience   `json:"audience"`
	ConsentPolicyRevision STRIDEReference  `json:"consentPolicyRevision"`
	ExpectedTimeSeconds   int64            `json:"expectedTimeSeconds"`
	ExpectedCostCents     int64            `json:"expectedCostCents"`
	ExpiresAt             time.Time        `json:"expiresAt"`
	Decision              string           `json:"decision"`
	DecisionPrincipal     string           `json:"decisionPrincipal,omitempty"`
	DecisionAt            *time.Time       `json:"decisionAt,omitempty"`
	IdempotencyKeyDigest  string           `json:"idempotencyKeyDigest"`
}

func (v MeetingAgentInvitation) Validate() error {
	return combineContract(validateHeader(v.Header, STRIDEContractMeetingAgentInvitation), !strideIdentifier(v.RoomID) || !strideIdentifier(v.SittingID) || v.SpecialistProfile.Validate() != nil || v.Capability.Validate() != nil || (v.Eligibility != nil && (v.Eligibility.Validate() != nil || v.Eligibility.ContractType != STRIDEContractAgentAssignment)) || !strideIdentifier(v.Requester) || !strideIdentifier(v.EligibleConfirmer) || !isHexDigest(v.PurposeDigest) || !uniqueSTRIDEIDs(v.ContextClasses) || !isHexDigest(v.SourceIntervalDigest) || v.Audience.Validate() != nil || v.ConsentPolicyRevision.Validate() != nil || v.ExpectedTimeSeconds < 0 || v.ExpectedCostCents < 0 || v.ExpiresAt.IsZero() || !oneOf(v.Decision, "requested", "approved", "declined", "expired", "dismissed") || !validOptionalSTRIDEID(v.DecisionPrincipal) || (v.DecisionAt != nil && v.DecisionAt.IsZero()) || !isHexDigest(v.IdempotencyKeyDigest))
}

type MeetingSpecialistContextEnvelope struct {
	Header              STRIDEContractHeader `json:"header"`
	Invitation          STRIDEReference      `json:"invitation"`
	AgentProfile        STRIDEReference      `json:"agentProfile"`
	RuntimeRevision     STRIDEReference      `json:"runtimeRevision"`
	ModelRevision       STRIDEReference      `json:"modelRevision"`
	TranscriptRefs      []STRIDEReference    `json:"transcriptRefs"`
	AnalysisRefs        []STRIDEReference    `json:"analysisRefs"`
	BrainRefs           []STRIDEReference    `json:"brainRefs"`
	WorkRefs            []STRIDEReference    `json:"workRefs"`
	Audience            STRIDEAudience       `json:"audience"`
	RetentionDigest     string               `json:"retentionDigest"`
	PurgeGeneration     int64                `json:"purgeGeneration"`
	TranscriptHighWater uint64               `json:"transcriptHighWater"`
	AnalysisHighWater   uint64               `json:"analysisHighWater"`
	BrainHighWater      uint64               `json:"brainHighWater"`
	GapsDigest          string               `json:"gapsDigest"`
	CoverageDigest      string               `json:"coverageDigest"`
	ToolIDs             []string             `json:"toolIds"`
	ResponseContract    string               `json:"responseContract"`
	FloorPolicy         string               `json:"floorPolicy"`
	TimeBudgetSeconds   int64                `json:"timeBudgetSeconds"`
	TurnBudget          int                  `json:"turnBudget"`
	AudioBudgetSeconds  int64                `json:"audioBudgetSeconds"`
	TokenBudget         int64                `json:"tokenBudget"`
	CostBudgetCents     int64                `json:"costBudgetCents"`
	ContextDigest       string               `json:"contextDigest"`
}

func (v MeetingSpecialistContextEnvelope) Validate() error {
	return combineContract(validateHeaderAndRefs(v.Header, STRIDEContractMeetingSpecialistContext, v.TranscriptRefs), v.Invitation.Validate() != nil || v.AgentProfile.Validate() != nil || v.RuntimeRevision.Validate() != nil || v.ModelRevision.Validate() != nil || !validateSTRIDERefs(v.AnalysisRefs) || !validateSTRIDERefs(v.BrainRefs) || !validateSTRIDERefs(v.WorkRefs) || v.Audience.Validate() != nil || !isHexDigest(v.RetentionDigest) || v.PurgeGeneration < 0 || !isHexDigest(v.GapsDigest) || !isHexDigest(v.CoverageDigest) || !uniqueSTRIDEIDs(v.ToolIDs) || !strideIdentifier(v.ResponseContract) || !strideIdentifier(v.FloorPolicy) || v.TimeBudgetSeconds < 0 || v.TurnBudget < 0 || v.AudioBudgetSeconds < 0 || v.TokenBudget < 0 || v.CostBudgetCents < 0 || !isHexDigest(v.ContextDigest))
}

type MeetingAgentSession struct {
	Header                    STRIDEContractHeader `json:"header"`
	Invitation                STRIDEReference      `json:"invitation"`
	ContextDigest             string               `json:"contextDigest"`
	RuntimePrincipal          string               `json:"runtimePrincipal"`
	ProviderSessionIDHash     string               `json:"providerSessionIdHash"`
	AudioTrackID              string               `json:"audioTrackId"`
	FloorGeneration           uint64               `json:"floorGeneration"`
	Status                    string               `json:"status"`
	InputHighWater            uint64               `json:"inputHighWater"`
	OutputHighWater           uint64               `json:"outputHighWater"`
	ToolCalls                 []STRIDEReference    `json:"toolCalls,omitempty"`
	UsageDigest               string               `json:"usageDigest"`
	InterruptionReason        string               `json:"interruptionReason,omitempty"`
	TerminalProviderEventHash string               `json:"terminalProviderEventHash,omitempty"`
	TeardownReceiptDigest     string               `json:"teardownReceiptDigest,omitempty"`
	StartedAt                 time.Time            `json:"startedAt"`
	EndedAt                   *time.Time           `json:"endedAt,omitempty"`
}

func (v MeetingAgentSession) Validate() error {
	return combineContract(validateHeader(v.Header, STRIDEContractMeetingAgentSession), v.Invitation.Validate() != nil || !isHexDigest(v.ContextDigest) || !strideIdentifier(v.RuntimePrincipal) || !isHexDigest(v.ProviderSessionIDHash) || !strideIdentifier(v.AudioTrackID) || v.FloorGeneration == 0 || !oneOf(v.Status, "requested", "approved", "joining", "briefed", "listening", "speaking", "dismissed", "expired", "failed") || !validateOptionalSTRIDERefs(v.ToolCalls) || !isHexDigest(v.UsageDigest) || !validOptionalSTRIDEID(v.InterruptionReason) || !validOptionalDigest(v.TerminalProviderEventHash) || !validOptionalDigest(v.TeardownReceiptDigest) || v.StartedAt.IsZero() || (v.EndedAt != nil && v.EndedAt.Before(v.StartedAt)))
}

type MeetingAgentContribution struct {
	Header           STRIDEContractHeader `json:"header"`
	Session          STRIDEReference      `json:"session"`
	AgentID          string               `json:"agentId"`
	RuntimePrincipal string               `json:"runtimePrincipal"`
	TranscriptRefs   []STRIDEReference    `json:"transcriptRefs"`
	PurposeDigest    string               `json:"purposeDigest"`
	ContextDigest    string               `json:"contextDigest"`
	CoverageDigest   string               `json:"coverageDigest"`
	Evidence         []STRIDEReference    `json:"evidence"`
	Audience         STRIDEAudience       `json:"audience"`
	RetentionDigest  string               `json:"retentionDigest"`
	PurgeGeneration  int64                `json:"purgeGeneration"`
	AnalysisRefs     []STRIDEReference    `json:"analysisRefs"`
	WorkIntent       *STRIDEReference     `json:"workIntent,omitempty"`
}

func (v MeetingAgentContribution) Validate() error {
	return combineContract(validateHeaderAndRefs(v.Header, STRIDEContractMeetingAgentContribution, v.TranscriptRefs), v.Session.Validate() != nil || !strideIdentifier(v.AgentID) || !strideIdentifier(v.RuntimePrincipal) || !isHexDigest(v.PurposeDigest) || !isHexDigest(v.ContextDigest) || !isHexDigest(v.CoverageDigest) || !validateSTRIDERefs(v.Evidence) || v.Audience.Validate() != nil || !isHexDigest(v.RetentionDigest) || v.PurgeGeneration < 0 || !validateSTRIDERefs(v.AnalysisRefs) || (v.WorkIntent != nil && v.WorkIntent.Validate() != nil))
}

type AgentPackageManifest struct {
	Header                     STRIDEContractHeader `json:"header"`
	PackageID                  string               `json:"packageId"`
	PublisherID                string               `json:"publisherId"`
	PublisherAttestationDigest string               `json:"publisherAttestationDigest"`
	Version                    string               `json:"version"`
	Provenance                 string               `json:"provenance"`
	PersonaSeedDigest          string               `json:"personaSeedDigest"`
	AssetRefs                  []STRIDEReference    `json:"assetRefs"`
	RequestedCapabilities      []string             `json:"requestedCapabilities"`
	RuntimeClasses             []string             `json:"runtimeClasses"`
	ModelClasses               []string             `json:"modelClasses"`
	VoiceClasses               []string             `json:"voiceClasses"`
	DataClassifications        []string             `json:"dataClassifications"`
	EvalBundleRefs             []STRIDEReference    `json:"evalBundleRefs"`
	DependencySBOMRefs         []STRIDEReference    `json:"dependencySbomRefs"`
	LicenseID                  string               `json:"licenseId"`
	UpdatePolicy               string               `json:"updatePolicy"`
	MigrationCompatibility     string               `json:"migrationCompatibility"`
	Status                     string               `json:"status"`
}

func (v AgentPackageManifest) Validate() error {
	return combineContract(validateHeaderAndRefs(v.Header, STRIDEContractAgentPackageManifest, v.AssetRefs), !strideIdentifier(v.PackageID) || !strideIdentifier(v.PublisherID) || !isHexDigest(v.PublisherAttestationDigest) || !strideIdentifier(v.Version) || !oneOf(v.Provenance, "stride_authored", "organization_authored") || !isHexDigest(v.PersonaSeedDigest) || !uniqueSTRIDEIDs(v.RequestedCapabilities) || !uniqueSTRIDEIDs(v.RuntimeClasses) || !uniqueSTRIDEIDs(v.ModelClasses) || !uniqueSTRIDEIDs(v.VoiceClasses) || !uniqueSTRIDEIDs(v.DataClassifications) || !validateSTRIDERefs(v.EvalBundleRefs) || !validateSTRIDERefs(v.DependencySBOMRefs) || !strideIdentifier(v.LicenseID) || !strideIdentifier(v.UpdatePolicy) || !strideIdentifier(v.MigrationCompatibility) || !oneOf(v.Status, "draft", "verified", "revoked", "superseded"))
}

type MarketplaceListing struct {
	Header                  STRIDEContractHeader `json:"header"`
	Package                 STRIDEReference      `json:"package"`
	Category                string               `json:"category"`
	OutcomeDigest           string               `json:"outcomeDigest"`
	Evidence                []STRIDEReference    `json:"evidence"`
	PermissionSummaryDigest string               `json:"permissionSummaryDigest"`
	Surfaces                []string             `json:"surfaces"`
	CostBand                string               `json:"costBand"`
	QualityReceipt          *STRIDEReference     `json:"qualityReceipt,omitempty"`
	SafetyReceipt           *STRIDEReference     `json:"safetyReceipt,omitempty"`
	VoiceReceipt            *STRIDEReference     `json:"voiceReceipt,omitempty"`
	Audience                STRIDEAudience       `json:"audience"`
	PublisherStatus         string               `json:"publisherStatus"`
	UpdateChannel           string               `json:"updateChannel"`
	Reviewer                string               `json:"reviewer"`
	Status                  string               `json:"status"`
}

func (v MarketplaceListing) Validate() error {
	return combineContract(validateHeaderAndRefs(v.Header, STRIDEContractMarketplaceListing, v.Evidence), v.Package.Validate() != nil || !strideIdentifier(v.Category) || !isHexDigest(v.OutcomeDigest) || !isHexDigest(v.PermissionSummaryDigest) || !uniqueSTRIDEIDs(v.Surfaces) || !strideIdentifier(v.CostBand) || (v.QualityReceipt != nil && v.QualityReceipt.Validate() != nil) || (v.SafetyReceipt != nil && v.SafetyReceipt.Validate() != nil) || (v.VoiceReceipt != nil && v.VoiceReceipt.Validate() != nil) || v.Audience.Validate() != nil || !oneOf(v.PublisherStatus, "active", "suspended", "revoked") || !strideIdentifier(v.UpdateChannel) || !strideIdentifier(v.Reviewer) || !oneOf(v.Status, "draft", "under_review", "verified", "available", "suspended", "revoked", "superseded"))
}

type TeamAgent struct {
	Header             STRIDEContractHeader `json:"header"`
	TeamAgentID        string               `json:"teamAgentId"`
	Package            STRIDEReference      `json:"package"`
	Listing            STRIDEReference      `json:"listing"`
	Overlay            *STRIDEReference     `json:"overlay,omitempty"`
	Owner              string               `json:"owner"`
	EscalationOwner    string               `json:"escalationOwner"`
	HireReceipt        STRIDEReference      `json:"hireReceipt"`
	Status             string               `json:"status"`
	DirectThread       string               `json:"directThread"`
	Memberships        []string             `json:"memberships"`
	Assignments        []STRIDEReference    `json:"assignments"`
	Capability         STRIDEReference      `json:"capability"`
	MemoryPolicy       string               `json:"memoryPolicy"`
	RouteRevision      STRIDEReference      `json:"routeRevision"`
	PerRunBudgetCents  int64                `json:"perRunBudgetCents"`
	DailyBudgetCents   int64                `json:"dailyBudgetCents"`
	MonthlyBudgetCents int64                `json:"monthlyBudgetCents"`
	Health             string               `json:"health"`
	CurrentWork        *STRIDEReference     `json:"currentWork,omitempty"`
	CurrentSession     *STRIDEReference     `json:"currentSession,omitempty"`
	PausedAt           *time.Time           `json:"pausedAt,omitempty"`
	OffboardedAt       *time.Time           `json:"offboardedAt,omitempty"`
	TerminalReason     string               `json:"terminalReason,omitempty"`
}

func (v TeamAgent) Validate() error {
	return combineContract(validateHeader(v.Header, STRIDEContractTeamAgent), !strideIdentifier(v.TeamAgentID) || v.Package.Validate() != nil || v.Listing.Validate() != nil || (v.Overlay != nil && v.Overlay.Validate() != nil) || !strideIdentifier(v.Owner) || !strideIdentifier(v.EscalationOwner) || v.HireReceipt.Validate() != nil || !oneOf(v.Status, "draft_hire", "trial_pending", "trial_active", "review_required", "active", "paused", "declined", "expired", "offboarding", "offboarded", "quarantined") || !strideIdentifier(v.DirectThread) || !uniqueSTRIDEIDs(v.Memberships) || !validateOptionalSTRIDERefs(v.Assignments) || v.Capability.Validate() != nil || !strideIdentifier(v.MemoryPolicy) || v.RouteRevision.Validate() != nil || v.PerRunBudgetCents < 0 || v.DailyBudgetCents < 0 || v.MonthlyBudgetCents < 0 || !oneOf(v.Health, "healthy", "degraded", "unavailable") || (v.CurrentWork != nil && v.CurrentWork.Validate() != nil) || (v.CurrentSession != nil && v.CurrentSession.Validate() != nil) || (v.PausedAt != nil && v.PausedAt.IsZero()) || (v.OffboardedAt != nil && v.OffboardedAt.IsZero()) || !validOptionalSTRIDEID(v.TerminalReason))
}

type AgentAssignment struct {
	Header               STRIDEContractHeader `json:"header"`
	TeamAgent            STRIDEReference      `json:"teamAgent"`
	ProjectOrChannel     string               `json:"projectOrChannel"`
	WorkflowRole         string               `json:"workflowRole"`
	ResponsibilityDigest string               `json:"responsibilityDigest"`
	SourceScope          []STRIDEReference    `json:"sourceScope"`
	Destination          string               `json:"destination"`
	Authority            string               `json:"authority"`
	Owner                string               `json:"owner"`
	StartsAt             time.Time            `json:"startsAt"`
	EndsAt               *time.Time           `json:"endsAt,omitempty"`
	BudgetCents          int64                `json:"budgetCents"`
	Status               string               `json:"status"`
}

func (v AgentAssignment) Validate() error {
	return combineContract(validateHeaderAndRefs(v.Header, STRIDEContractAgentAssignment, v.SourceScope), v.TeamAgent.Validate() != nil || !strideIdentifier(v.ProjectOrChannel) || !strideIdentifier(v.WorkflowRole) || !isHexDigest(v.ResponsibilityDigest) || !strideIdentifier(v.Destination) || !oneOf(v.Authority, "read_only", "internal_write") || !strideIdentifier(v.Owner) || v.StartsAt.IsZero() || (v.EndsAt != nil && !v.EndsAt.After(v.StartsAt)) || v.BudgetCents < 0 || !oneOf(v.Status, "draft", "active", "paused", "completed", "cancelled"))
}

type AgentLearningRecord struct {
	Header             STRIDEContractHeader `json:"header"`
	AgentID            string               `json:"agentId"`
	Kind               string               `json:"kind"`
	Subject            string               `json:"subject"`
	Scope              string               `json:"scope"`
	LessonDigest       string               `json:"lessonDigest"`
	Evidence           []STRIDEReference    `json:"evidence"`
	Confidence         float64              `json:"confidence"`
	FirstObserved      time.Time            `json:"firstObserved"`
	LastObserved       time.Time            `json:"lastObserved"`
	ReinforcementCount int                  `json:"reinforcementCount"`
	Counterevidence    []STRIDEReference    `json:"counterevidence,omitempty"`
	Audience           STRIDEAudience       `json:"audience"`
	ExpiresAt          *time.Time           `json:"expiresAt,omitempty"`
	Status             string               `json:"status"`
	CorrectionHistory  []STRIDEReference    `json:"correctionHistory,omitempty"`
	PurgeLineage       []STRIDEReference    `json:"purgeLineage,omitempty"`
}

func (v AgentLearningRecord) Validate() error {
	return combineContract(validateHeaderAndRefs(v.Header, STRIDEContractAgentLearningRecord, v.Evidence), !strideIdentifier(v.AgentID) || !oneOf(v.Kind, "relationship", "domain", "competency_candidate") || !strideIdentifier(v.Subject) || !strideIdentifier(v.Scope) || !isHexDigest(v.LessonDigest) || v.Confidence < 0 || v.Confidence > 1 || v.FirstObserved.IsZero() || v.LastObserved.Before(v.FirstObserved) || v.ReinforcementCount < 0 || !validateOptionalSTRIDERefs(v.Counterevidence) || v.Audience.Validate() != nil || (v.ExpiresAt != nil && !v.ExpiresAt.After(v.LastObserved)) || !oneOf(v.Status, "candidate", "reviewed", "rejected", "corrected", "forgotten", "superseded") || !validateOptionalSTRIDERefs(v.CorrectionHistory) || !validateOptionalSTRIDERefs(v.PurgeLineage))
}

type AgentPerformanceReceipt struct {
	Header         STRIDEContractHeader `json:"header"`
	Assignment     STRIDEReference      `json:"assignment"`
	WorkRun        STRIDEReference      `json:"workRun"`
	Output         STRIDEReference      `json:"output"`
	CriteriaDigest string               `json:"criteriaDigest"`
	Evidence       []STRIDEReference    `json:"evidence"`
	Reviewer       string               `json:"reviewer"`
	FeedbackDigest string               `json:"feedbackDigest"`
	Verdict        string               `json:"verdict"`
	Route          STRIDEReference      `json:"route"`
	Profile        STRIDEReference      `json:"profile"`
	Package        STRIDEReference      `json:"package"`
	CostCents      int64                `json:"costCents"`
	LatencyMS      int64                `json:"latencyMs"`
	EligibleClaims []string             `json:"eligibleClaims"`
}

func (v AgentPerformanceReceipt) Validate() error {
	return combineContract(validateHeaderAndRefs(v.Header, STRIDEContractAgentPerformanceReceipt, v.Evidence), v.Assignment.Validate() != nil || v.WorkRun.Validate() != nil || v.Output.Validate() != nil || !isHexDigest(v.CriteriaDigest) || !strideIdentifier(v.Reviewer) || !isHexDigest(v.FeedbackDigest) || !oneOf(v.Verdict, "accepted", "rejected") || v.Route.Validate() != nil || v.Profile.Validate() != nil || v.Package.Validate() != nil || v.CostCents < 0 || v.LatencyMS < 0 || !uniqueSTRIDEIDs(v.EligibleClaims))
}

type AgentUpdateProposal struct {
	Header               STRIDEContractHeader `json:"header"`
	TeamAgent            STRIDEReference      `json:"teamAgent"`
	CurrentPackage       STRIDEReference      `json:"currentPackage"`
	CandidatePackage     STRIDEReference      `json:"candidatePackage"`
	CurrentProfile       STRIDEReference      `json:"currentProfile"`
	CandidateProfile     STRIDEReference      `json:"candidateProfile"`
	CurrentCapability    STRIDEReference      `json:"currentCapability"`
	CandidateCapability  STRIDEReference      `json:"candidateCapability"`
	CurrentRoute         STRIDEReference      `json:"currentRoute"`
	CandidateRoute       STRIDEReference      `json:"candidateRoute"`
	SemanticDiffDigest   string               `json:"semanticDiffDigest"`
	PermissionDiffDigest string               `json:"permissionDiffDigest"`
	MigrationDigest      string               `json:"migrationDigest"`
	AffectedAssignments  []STRIDEReference    `json:"affectedAssignments"`
	EvalReceipts         []STRIDEReference    `json:"evalReceipts"`
	CostDeltaCents       int64                `json:"costDeltaCents"`
	RolloutCohort        string               `json:"rolloutCohort"`
	RollbackRef          STRIDEReference      `json:"rollbackRef"`
	Approvers            []string             `json:"approvers"`
	ExpiresAt            time.Time            `json:"expiresAt"`
	Decision             string               `json:"decision"`
}

func (v AgentUpdateProposal) Validate() error {
	return combineContract(validateHeaderAndRefs(v.Header, STRIDEContractAgentUpdateProposal, v.AffectedAssignments), v.TeamAgent.Validate() != nil || v.CurrentPackage.Validate() != nil || v.CandidatePackage.Validate() != nil || v.CurrentProfile.Validate() != nil || v.CandidateProfile.Validate() != nil || v.CurrentCapability.Validate() != nil || v.CandidateCapability.Validate() != nil || v.CurrentRoute.Validate() != nil || v.CandidateRoute.Validate() != nil || !allDigests(v.SemanticDiffDigest, v.PermissionDiffDigest, v.MigrationDigest) || !validateSTRIDERefs(v.EvalReceipts) || !strideIdentifier(v.RolloutCohort) || v.RollbackRef.Validate() != nil || !uniqueSTRIDEIDs(v.Approvers) || v.ExpiresAt.IsZero() || !oneOf(v.Decision, "pending", "approved", "rejected", "expired"))
}

type WorkforcePolicy struct {
	Header             STRIDEContractHeader `json:"header"`
	Roles              map[string][]string  `json:"roles"`
	RosterCap          int                  `json:"rosterCap"`
	ConcurrencyCap     int                  `json:"concurrencyCap"`
	DailyBudgetCents   int64                `json:"dailyBudgetCents"`
	MonthlyBudgetCents int64                `json:"monthlyBudgetCents"`
	MaxAgentHops       int                  `json:"maxAgentHops"`
	ProactivePolicy    string               `json:"proactivePolicy"`
	RetentionPolicy    string               `json:"retentionPolicy"`
}

func (v WorkforcePolicy) Validate() error {
	if v.Header.Validate(STRIDEContractWorkforcePolicy) != nil || len(v.Roles) == 0 || v.RosterCap < 0 || v.ConcurrencyCap < 0 || v.DailyBudgetCents < 0 || v.MonthlyBudgetCents < 0 || v.MaxAgentHops != 0 || !oneOf(v.ProactivePolicy, "disabled", "quiet") || !strideIdentifier(v.RetentionPolicy) {
		return ErrSTRIDEContractInvalid
	}
	for role, actions := range v.Roles {
		if !strideIdentifier(role) || !uniqueSTRIDEIDs(actions) {
			return ErrSTRIDEContractInvalid
		}
	}
	return nil
}

func STRIDEContractDigest(value any) (string, error) {
	raw, err := canonicalJSON(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func validSTRIDEContractType(value STRIDEContractType) bool {
	switch value {
	case STRIDEContractConversationEvent, STRIDEContractTranscriptSegment, STRIDEContractTranscriptRevision, STRIDEContractMeetingSourceEpisode, STRIDEContractSourceEpisode, STRIDEContractAnalysisProjection, STRIDEContractKnowledgeAssertion, STRIDEContractCollaborationPreference, STRIDEContractWorkIntent, STRIDEContractWorkProposal, STRIDEContractWorkRun, STRIDEContractOutcome, STRIDEContractMeetingAnswer, STRIDEContractCompanyAnswer, STRIDEContractAgentCoreProfile, STRIDEContractAgentProfileOverlay, STRIDEContractAgentCapabilityManifest, STRIDEContractChannelNormProfile, STRIDEContractAgentRelationshipMemory, STRIDEContractAgentContextEnvelope, STRIDEContractDelegationRun, STRIDEContractRichMessagePart, STRIDEContractMeetingAgentInvitation, STRIDEContractMeetingSpecialistContext, STRIDEContractMeetingAgentSession, STRIDEContractMeetingAgentContribution, STRIDEContractAgentPackageManifest, STRIDEContractMarketplaceListing, STRIDEContractTeamAgent, STRIDEContractAgentAssignment, STRIDEContractAgentLearningRecord, STRIDEContractAgentPerformanceReceipt, STRIDEContractAgentUpdateProposal, STRIDEContractWorkforcePolicy, STRIDEContractPersonPrincipal, STRIDEContractPersonProfile, STRIDEContractOrganizationMemberProfile, STRIDEContractOrganization, STRIDEContractOrganizationMembership, STRIDEContractOrganizationJoinRequest, STRIDEContractActiveOrganizationSession, STRIDEContractOrganizationAuditEvent, STRIDEContractContributionClaim, STRIDEContractContributionAttestation, STRIDEContractPublishedContributionClaim, STRIDEContractAgentInfluenceReceipt, STRIDEContractFieldReleaseApproval, STRIDEContractNetworkProfileProjection, STRIDEContractTalentSearchGrant, STRIDEContractNetworkSearchReceipt, STRIDEContractContactRequest, STRIDEContractNetworkBlock, STRIDEContractDerivedPurgeReceipt, STRIDEContractWorkspaceMembership, STRIDEContractMyMindSource, STRIDEContractMyMindDisclosureGrant, STRIDEContractMyMindCustodyDeletion, STRIDEContractArtifactDisposition, STRIDEContractProject, STRIDEContractProjectThreadBinding, STRIDEContractProjectAssociation, STRIDEContractProjectAssociationEvent:
		return true
	}
	return false
}
func strideIdentifier(value string) bool      { return strideID.MatchString(value) }
func validOptionalSTRIDEID(value string) bool { return value == "" || strideIdentifier(value) }
func validOptionalDigest(value string) bool   { return value == "" || isHexDigest(value) }
func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
func uniqueSTRIDEIDs(values []string) bool {
	if len(values) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !strideIdentifier(value) || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}
func validateSTRIDERefs(values []STRIDEReference) bool {
	if len(values) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if value.Validate() != nil {
			return false
		}
		key := string(value.ContractType) + "\x00" + value.ID + fmt.Sprint(value.Revision)
		if seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}
func validateOptionalSTRIDERefs(values []STRIDEReference) bool {
	return len(values) == 0 || validateSTRIDERefs(values)
}
func validateHeader(h STRIDEContractHeader, kind STRIDEContractType) error { return h.Validate(kind) }
func validateHeaderAndRefs(h STRIDEContractHeader, kind STRIDEContractType, refs []STRIDEReference) error {
	if h.Validate(kind) != nil || !validateSTRIDERefs(refs) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}
func validateAgentProfileHeader(h STRIDEContractHeader, kind STRIDEContractType, agentID, displayName, role, owner string) error {
	if h.Validate(kind) != nil || !strideIdentifier(agentID) || strings.TrimSpace(displayName) == "" || !strideIdentifier(role) || !strideIdentifier(owner) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}
func allDigests(values ...string) bool {
	for _, value := range values {
		if !isHexDigest(value) {
			return false
		}
	}
	return true
}
func sensitivePreference(kind string) bool {
	normalized := strings.ToLower(kind)
	for _, forbidden := range []string{"health", "medical", "race", "religion", "politic", "sexual", "union", "disability", "biometric"} {
		if strings.Contains(normalized, forbidden) {
			return true
		}
	}
	return false
}

// combineContract preserves the first structured validation error and turns
// additional scalar failures into the shared closed-schema error.
func combineContract(prior error, invalid bool) error {
	if prior != nil || invalid {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

// SortedSTRIDEReferences makes evidence serialization stable before a digest
// is taken. It never mutates the caller's slice.
func SortedSTRIDEReferences(values []STRIDEReference) []STRIDEReference {
	clone := append([]STRIDEReference(nil), values...)
	sort.Slice(clone, func(i, j int) bool {
		if clone[i].ContractType != clone[j].ContractType {
			return clone[i].ContractType < clone[j].ContractType
		}
		if clone[i].ID != clone[j].ID {
			return clone[i].ID < clone[j].ID
		}
		return clone[i].Revision < clone[j].Revision
	})
	return clone
}
