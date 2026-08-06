package main

// The product lifecycle is the app-owned, token-free reachability layer for
// E6-E8. It is deliberately not a provider runtime: every mutable record says
// whether provider execution is fenced, and the only activation receipt this
// file accepts is a short-lived MAC for a closed deterministic-local scope.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	STRIDEProductScopeWork               = "work_local_fixture"
	STRIDEProductScopeMarketplace        = "marketplace_local_fixture"
	STRIDEProductScopeTemporal           = "temporal_local_fixture"
	STRIDEProductScopeCoworker           = "coworker_local_fixture"
	strideProductAgentDirectThreadPrefix = "stride_agent_direct_"
	strideProductActivationDomain        = "stride_product_activation"
	strideProductInsightsSnapshotMarker  = "insights_workflow_snapshot_saved"
	strideProductInsightsSnapshotPurged  = "insights_workflow_snapshot_purged"
	strideProductMaxInsightsSnapshot     = 8 << 20
	strideProductDestinationResolver     = "deterministic_project_thread"
	strideProductDestinationResolverV1   = 1
	strideProductDestinationRecommended  = "recommended"
	strideProductDestinationManual       = "manual_selection_required"
)

var (
	ErrSTRIDEProductDisabled = errors.New("STRIDE deterministic product preview is disabled")
	ErrSTRIDEProductDenied   = errors.New("STRIDE product action denied")
	ErrSTRIDEProductConflict = errors.New("STRIDE product revision conflict")
	ErrSTRIDEProductInvalid  = errors.New("invalid STRIDE product state")
	ErrSTRIDEProductUnknown  = errors.New("unknown STRIDE product record")
)

type STRIDEProductActivationReceipt struct {
	TenantID   string    `json:"tenantId"`
	Scope      string    `json:"scope"`
	Mode       string    `json:"mode"`
	Generation uint64    `json:"generation"`
	KeyID      string    `json:"keyId"`
	IssuedAt   time.Time `json:"issuedAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Nonce      string    `json:"nonce"`
	Digest     string    `json:"digest"`
	Signature  string    `json:"signature"`
}

type strideProductActivationMaterial struct {
	TenantID   string    `json:"tenantId"`
	Scope      string    `json:"scope"`
	Mode       string    `json:"mode"`
	Generation uint64    `json:"generation"`
	KeyID      string    `json:"keyId"`
	IssuedAt   time.Time `json:"issuedAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Nonce      string    `json:"nonce"`
}

func (receipt STRIDEProductActivationReceipt) material() strideProductActivationMaterial {
	return strideProductActivationMaterial{receipt.TenantID, receipt.Scope, receipt.Mode, receipt.Generation, receipt.KeyID, receipt.IssuedAt, receipt.ExpiresAt, receipt.Nonce}
}

func validSTRIDEProductScope(scope string) bool {
	return scope == STRIDEProductScopeWork || scope == STRIDEProductScopeMarketplace || scope == STRIDEProductScopeTemporal || scope == STRIDEProductScopeCoworker
}

func (runtime *STRIDERuntime) productPreviewOwnsWorkSuggestions() bool {
	if runtime == nil {
		return false
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	// A configured preview owns suggestion creation even when its durable
	// runtime is temporarily unavailable. Falling back to the broadcast legacy
	// lane would escape the recipient ACL precisely when the safe path is down.
	return runtime.config.ProductPreviewEnabled
}

func mintSTRIDEProductActivationReceipt(config STRIDERuntimeConfig, generation uint64, scope string, now time.Time) (STRIDEProductActivationReceipt, error) {
	if !config.ProductPreviewEnabled || !validSTRIDEProductScope(scope) || !config.Authority.valid() || !strideIdentifier(config.TenantID) {
		return STRIDEProductActivationReceipt{}, ErrSTRIDEProductDisabled
	}
	if generation == 0 {
		generation = 1
	}
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return STRIDEProductActivationReceipt{}, err
	}
	receipt := STRIDEProductActivationReceipt{TenantID: config.TenantID, Scope: scope, Mode: "deterministic_local", Generation: generation, KeyID: config.Authority.KeyID, IssuedAt: now.UTC(), ExpiresAt: now.UTC().Add(2 * time.Minute), Nonce: hex.EncodeToString(nonceBytes)}
	digest, err := STRIDEContractDigest(receipt.material())
	if err != nil {
		return STRIDEProductActivationReceipt{}, err
	}
	receipt.Digest = digest
	receipt.Signature, err = strideSnapshotMAC(config.Authority, strideProductActivationDomain, generation, digest)
	return receipt, err
}

func verifySTRIDEProductActivationReceipt(config STRIDERuntimeConfig, receipt STRIDEProductActivationReceipt, scope string, now time.Time) bool {
	if !config.ProductPreviewEnabled || scope != receipt.Scope || !validSTRIDEProductScope(scope) || receipt.TenantID != config.TenantID || receipt.Mode != "deterministic_local" ||
		receipt.KeyID != config.Authority.KeyID || receipt.IssuedAt.IsZero() || !receipt.ExpiresAt.After(now.UTC()) || receipt.IssuedAt.After(now.UTC()) || !isSTRIDEProductNonce(receipt.Nonce) || !isHexDigest(receipt.Digest) {
		return false
	}
	digest, err := STRIDEContractDigest(receipt.material())
	return err == nil && digest == receipt.Digest && verifySTRIDESnapshotMAC(STRIDESnapshotRestorePolicy{Authority: config.Authority, MinimumGeneration: receipt.Generation}, strideProductActivationDomain, receipt.KeyID, receipt.Generation, receipt.Digest, receipt.Signature)
}

func isSTRIDEProductNonce(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16
}

type STRIDEProductWorkRecord struct {
	ID              string          `json:"id"`
	Title           string          `json:"title"`
	Outcome         string          `json:"outcome"`
	SourceThreadID  string          `json:"sourceThreadId"`
	SourceMessageID string          `json:"sourceMessageId"`
	SourceSnippet   string          `json:"sourceSnippet"`
	SourceEvent     STRIDEReference `json:"sourceEvent"`
	// SourceEvents is the complete, canonical evidence set whose current
	// authority is required to approve this suggestion. SourceEvent remains the
	// primary display/backward-compatibility reference and must be a member.
	SourceEvents              []STRIDEReference                       `json:"sourceEvents"`
	RecipientIDs              []string                                `json:"recipientIds"`
	OwnerID                   string                                  `json:"ownerId"`
	ReviewerID                string                                  `json:"reviewerId"`
	ApprovalPolicy            STRIDEProductApprovalPolicy             `json:"approvalPolicy"`
	MeetingAuthority          *STRIDEProductMeetingAuthority          `json:"meetingAuthority,omitempty"`
	Revision                  int64                                   `json:"revision"`
	Status                    string                                  `json:"status"`
	DestinationMode           string                                  `json:"destinationMode,omitempty"`
	DestinationThreadID       string                                  `json:"destinationThreadId,omitempty"`
	DestinationTitle          string                                  `json:"destinationTitle,omitempty"`
	DestinationAudience       *STRIDEAudience                         `json:"destinationAudience,omitempty"`
	DestinationACLVersion     int64                                   `json:"destinationAclVersion,omitempty"`
	DestinationRecommendation *STRIDEProductDestinationRecommendation `json:"destinationRecommendation,omitempty"`
	RunID                     string                                  `json:"runId,omitempty"`
	ArtifactID                string                                  `json:"artifactId,omitempty"`
	ArtifactHref              string                                  `json:"artifactHref,omitempty"`
	BrainHref                 string                                  `json:"brainHref,omitempty"`
	CompletionSummary         string                                  `json:"completionSummary,omitempty"`
	FailureReason             string                                  `json:"failureReason,omitempty"`
	SourceInvalidated         bool                                    `json:"sourceInvalidated,omitempty"`
	ProviderExecutionFenced   bool                                    `json:"providerExecutionFenced"`
	Lifecycle                 []string                                `json:"lifecycle"`
	CreatedAt                 time.Time                               `json:"createdAt"`
	UpdatedAt                 time.Time                               `json:"updatedAt"`
	CompletionPosted          bool                                    `json:"completionPosted"`
}

// STRIDEProductDestinationRecommendation is an inspectable routing proposal,
// never launch authority. A recommended thread may prefill the destination so
// the human can approve in one step, but no run exists until the normal
// revision-bound approval endpoint consumes that explicit approval.
type STRIDEProductDestinationRecommendation struct {
	Status                 string                               `json:"status"`
	ThreadID               string                               `json:"threadId,omitempty"`
	Title                  string                               `json:"title,omitempty"`
	Confidence             float64                              `json:"confidence"`
	Reasons                []string                             `json:"reasons"`
	ParticipantEligibility string                               `json:"participantEligibility"`
	EligiblePrincipals     []string                             `json:"eligiblePrincipals,omitempty"`
	EligibleThreadIDs      []string                             `json:"eligibleThreadIds,omitempty"`
	ACLVersion             int64                                `json:"aclVersion,omitempty"`
	Audit                  STRIDEProductDestinationRoutingAudit `json:"audit"`
}

type STRIDEProductDestinationRoutingAudit struct {
	Resolver                     string    `json:"resolver"`
	Version                      int       `json:"version"`
	EvaluatedAt                  time.Time `json:"evaluatedAt"`
	SourceThreadID               string    `json:"sourceThreadId"`
	ConsideredCandidates         int       `json:"consideredCandidates"`
	RelevantCandidates           int       `json:"relevantCandidates"`
	AuthorizedRelevantCandidates int       `json:"authorizedRelevantCandidates"`
	MatchBasis                   string    `json:"matchBasis"`
	Digest                       string    `json:"digest"`
}

// STRIDEProductInsightsState is the private, durable body store behind one
// completed Insights & Opportunities artifact. It deliberately lives outside
// STRIDEProductWorkRecord because work records are returned by list/read APIs;
// embedding the workflow there would leak report bodies and feedback into
// otherwise body-light product responses. The containing STRIDE runtime
// snapshot MAC authenticates this payload and its digest catches accidental
// in-process corruption before it is admitted or served.
type STRIDEProductInsightsState struct {
	TenantID            string    `json:"tenantId"`
	WorkID              string    `json:"workId"`
	Revision            int64     `json:"revision"`
	WorkflowPayload     []byte    `json:"workflowPayload"`
	WorkflowDigest      string    `json:"workflowDigest"`
	CurrentRunID        string    `json:"currentRunId"`
	CurrentReportDigest string    `json:"currentReportDigest"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

// STRIDEProductApprovalPolicy is proposal authority, not UI metadata. Its
// revision and eligible principals are fixed when the proposal is created.
type STRIDEProductApprovalPolicy struct {
	Revision           int64     `json:"revision"`
	EligiblePrincipals []string  `json:"eligiblePrincipals"`
	Quorum             int       `json:"quorum"`
	ExpiresAt          time.Time `json:"expiresAt"`
}

// These exported, body-free values are the durable form of the server-minted
// ConsentFence. They let a restored proposal reconstruct and revalidate the
// exact participant/guest, sitting, lane, policy and generation authority it
// observed when the suggestion was made.
type STRIDEProductConsentFence struct {
	Binding      ConsentAdmissionBinding `json:"binding"`
	Lane         ConsentLane             `json:"lane"`
	Policy       string                  `json:"policy"`
	Generation   uint64                  `json:"generation"`
	RecordDigest string                  `json:"recordDigest"`
	IssuedAt     time.Time               `json:"issuedAt"`
}

type STRIDEProductMeetingAuthority struct {
	TenantID              string                      `json:"tenantId"`
	RoomID                string                      `json:"roomId"`
	SittingID             string                      `json:"sittingId"`
	MediaGeneration       uint64                      `json:"mediaGeneration"`
	RequesterPrincipal    string                      `json:"requesterPrincipal"`
	Audience              STRIDEAudience              `json:"audience"`
	ConsentPolicyRevision STRIDEReference             `json:"consentPolicyRevision"`
	ConsentFences         []STRIDEProductConsentFence `json:"consentFences"`
}

type STRIDEProductMarketplaceCandidate struct {
	ID                      string                         `json:"id"`
	PackageID               string                         `json:"packageId"`
	DisplayName             string                         `json:"displayName"`
	Category                string                         `json:"category"`
	RoleTitle               string                         `json:"roleTitle,omitempty"`
	OutcomeSummary          string                         `json:"outcomeSummary"`
	PersonalitySummary      string                         `json:"personalitySummary"`
	VoiceSummary            string                         `json:"voiceSummary,omitempty"`
	WorkingStyle            string                         `json:"workingStyle,omitempty"`
	PersonalityTraits       []string                       `json:"personalityTraits,omitempty"`
	CoreMemories            []STRIDEProductAgentCoreMemory `json:"coreMemories,omitempty"`
	DefaultPersonalityNotes string                         `json:"defaultPersonalityNotes,omitempty"`
	DefaultProactivity      string                         `json:"defaultProactivity,omitempty"`
	SampleOutputs           []string                       `json:"sampleOutputs"`
	Capabilities            []string                       `json:"capabilities"`
	RequiredAccess          []string                       `json:"requiredAccess"`
	AccessSummary           string                         `json:"accessSummary"`
	CostBand                string                         `json:"costBand"`
	Publisher               string                         `json:"publisher"`
	Version                 string                         `json:"version"`
	Provenance              string                         `json:"provenance"`
	Visibility              string                         `json:"visibility"`
	UpdatePolicy            string                         `json:"updatePolicy"`
	MemoryPolicy            string                         `json:"memoryPolicy"`
	PackageDigest           string                         `json:"packageDigest"`
	ReceiptStatus           map[string]bool                `json:"receiptStatus"`
	Availability            string                         `json:"availability"`
	LiveAvailable           bool                           `json:"liveAvailable"`
	ProviderExecutionFenced bool                           `json:"providerExecutionFenced"`
}

type STRIDEProductAgentConfig struct {
	PersonalityNotes  string   `json:"personalityNotes"`
	Memberships       []string `json:"memberships"`
	PerRunBudgetCents int64    `json:"perRunBudgetCents"`
	DailyBudgetCents  int64    `json:"dailyBudgetCents"`
	Proactivity       string   `json:"proactivity"`
}

type STRIDEProductAgentAssignment struct {
	ID               string    `json:"id"`
	ProjectOrChannel string    `json:"projectOrChannel"`
	Role             string    `json:"role"`
	Responsibility   string    `json:"responsibility"`
	Destination      string    `json:"destination"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"createdAt"`
}

type STRIDEProductAgentUpdate struct {
	ID           string                         `json:"id"`
	Revision     int64                          `json:"revision"`
	Status       string                         `json:"status"`
	Summary      string                         `json:"summary"`
	Previous     STRIDEProductAgentConfig       `json:"previous"`
	Candidate    STRIDEProductAgentConfig       `json:"candidate"`
	SemanticDiff STRIDEProductAgentSemanticDiff `json:"semanticDiff"`
	CreatedAt    time.Time                      `json:"createdAt"`
	UpdatedAt    time.Time                      `json:"updatedAt"`
}

type STRIDEProductAgentLearning struct {
	ID             string     `json:"id"`
	Subject        string     `json:"subject"`
	Scope          string     `json:"scope"`
	Summary        string     `json:"summary"`
	Status         string     `json:"status"`
	Origin         string     `json:"origin,omitempty"`
	RunID          string     `json:"runId,omitempty"`
	ArtifactID     string     `json:"artifactId,omitempty"`
	SourceThreadID string     `json:"sourceThreadId,omitempty"`
	SourceRefs     []string   `json:"sourceRefs,omitempty"`
	Confidence     float64    `json:"confidence,omitempty"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
	Revision       int64      `json:"revision"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

// STRIDEProductAgentCoreMemory is package-authored identity context, not a
// claim that the agent observed or learned something about a person. It is
// copied onto a team seat at trial time so the coworker's enduring operating
// principles survive listing changes and snapshot restores. Team-derived
// memories continue to use STRIDEProductAgentLearning, including its explicit
// human correction and forget controls.
type STRIDEProductAgentCoreMemory struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	Summary string `json:"summary"`
}

type STRIDEProductTeamAgent struct {
	ID                      string                         `json:"id"`
	ListingID               string                         `json:"listingId"`
	DisplayName             string                         `json:"displayName"`
	Category                string                         `json:"category"`
	RoleTitle               string                         `json:"roleTitle,omitempty"`
	OutcomeSummary          string                         `json:"outcomeSummary,omitempty"`
	PersonalitySummary      string                         `json:"personalitySummary,omitempty"`
	VoiceSummary            string                         `json:"voiceSummary,omitempty"`
	WorkingStyle            string                         `json:"workingStyle,omitempty"`
	PersonalityTraits       []string                       `json:"personalityTraits,omitempty"`
	Capabilities            []string                       `json:"capabilities,omitempty"`
	MemoryPolicy            string                         `json:"memoryPolicy,omitempty"`
	CoreMemories            []STRIDEProductAgentCoreMemory `json:"coreMemories,omitempty"`
	Status                  string                         `json:"status"`
	OwnerID                 string                         `json:"ownerId"`
	DirectThreadID          string                         `json:"directThreadId,omitempty"`
	Revision                int64                          `json:"revision"`
	Config                  STRIDEProductAgentConfig       `json:"config"`
	Assignments             []STRIDEProductAgentAssignment `json:"assignments"`
	Updates                 []STRIDEProductAgentUpdate     `json:"updates"`
	Learning                []STRIDEProductAgentLearning   `json:"learning"`
	ProviderExecutionFenced bool                           `json:"providerExecutionFenced"`
	AccessRevoked           bool                           `json:"accessRevoked"`
	Lifecycle               []string                       `json:"lifecycle"`
	CreatedAt               time.Time                      `json:"createdAt"`
	UpdatedAt               time.Time                      `json:"updatedAt"`
}

// STRIDEProductAgentContextProfile is the safe, inspectable identity payload
// an approved work bridge can bind to a run. Producing it grants no provider,
// file, channel, or delegation authority: the explicit fence remains visible,
// and current ACL/runtime admission must still happen at the launch boundary.
type STRIDEProductAgentContextProfile struct {
	AgentID                 string                         `json:"agentId"`
	ListingID               string                         `json:"listingId"`
	Revision                int64                          `json:"revision"`
	DisplayName             string                         `json:"displayName"`
	RoleTitle               string                         `json:"roleTitle"`
	Category                string                         `json:"category"`
	OutcomeSummary          string                         `json:"outcomeSummary"`
	PersonalitySummary      string                         `json:"personalitySummary"`
	VoiceSummary            string                         `json:"voiceSummary"`
	WorkingStyle            string                         `json:"workingStyle"`
	PersonalityTraits       []string                       `json:"personalityTraits"`
	PersonalityNotes        string                         `json:"personalityNotes"`
	Capabilities            []string                       `json:"capabilities"`
	Memberships             []string                       `json:"memberships"`
	MemoryPolicy            string                         `json:"memoryPolicy"`
	CoreMemories            []STRIDEProductAgentCoreMemory `json:"coreMemories"`
	ActiveLearning          []STRIDEProductAgentLearning   `json:"activeLearning"`
	ProviderExecutionFenced bool                           `json:"providerExecutionFenced"`
	Digest                  string                         `json:"digest"`
}

type STRIDEProductSnapshot struct {
	Version    int                                 `json:"version"`
	Work       []STRIDEProductWorkRecord           `json:"work"`
	Insights   []STRIDEProductInsightsState        `json:"insights,omitempty"`
	Candidates []STRIDEProductMarketplaceCandidate `json:"candidates"`
	Agents     []STRIDEProductTeamAgent            `json:"agents"`
	Digest     string                              `json:"digest"`
}

type STRIDEProductState struct {
	mu         sync.RWMutex
	work       map[string]STRIDEProductWorkRecord
	insights   map[string]STRIDEProductInsightsState
	candidates map[string]STRIDEProductMarketplaceCandidate
	agents     map[string]STRIDEProductTeamAgent
}

func NewSTRIDEProductState() *STRIDEProductState {
	state := &STRIDEProductState{work: map[string]STRIDEProductWorkRecord{}, insights: map[string]STRIDEProductInsightsState{}, candidates: map[string]STRIDEProductMarketplaceCandidate{}, agents: map[string]STRIDEProductTeamAgent{}}
	state.reconcileDefaultCandidates()
	return state
}

const strideAgentMemoryPolicy = "Company-owned, evidence-linked learning with human correction and forget controls."

// Scout is included with STRIDE rather than hireable. Its marketplace profile
// makes the same inspectable personality, memory, access, and update contract
// visible for the built-in teammate that already fronts chat and rooms.
func defaultSTRIDEScoutCandidate() STRIDEProductMarketplaceCandidate {
	return STRIDEProductMarketplaceCandidate{
		ID: "scout", PackageID: "stride-scout-core-v1", DisplayName: "Scout", Category: "coordination", RoleTitle: "Team Partner",
		OutcomeSummary:     "Stays close to the team’s conversations, recalls authorized context, turns commitments into durable work, and brings in the right specialist when the job needs one.",
		PersonalitySummary: "Warm, observant, quietly confident, and direct without becoming clinical.",
		VoiceSummary:       "Conversational and first-person; steps into the room like a trusted colleague, keeps updates short, and says plainly what is known, missing, or happening next.",
		WorkingStyle:       "Listens for the real ask, keeps promises attached to durable work, uses only authorized context, and follows through in the conversation where the request began.",
		PersonalityTraits:  []string{"warm", "observant", "direct", "dependable"},
		CoreMemories: []STRIDEProductAgentCoreMemory{
			{ID: "promises_become_durable_work", Subject: "follow_through", Summary: "If I say I will do something later, the commitment must become visible, scheduled work with a durable outcome."},
			{ID: "context_is_permissioned", Subject: "team_memory", Summary: "Useful continuity comes from authorized company and relationship memory; familiarity never expands access."},
		},
		DefaultPersonalityNotes: "Be Scout: warm, observant, concise, and genuinely conversational. Speak in first person, respond to the human in front of you, make missing context easy to supply, and turn every promised follow-up into visible durable work.",
		DefaultProactivity:      "quiet",
		SampleOutputs:           []string{"Conversational answer", "Durable work handoff", "Meeting follow-up", "Specialist delegation"},
		Capabilities:            []string{"conversation", "meeting_recall", "work_orchestration", "agent_delegation", "authorized_file_retrieval"},
		RequiredAccess:          []string{"approved_conversation_context", "source_linked_company_brain", "acl_bound_files"},
		AccessSummary:           "Included with STRIDE; reads only the current conversation and explicitly authorized company, meeting, Drive, or File context. Material actions remain separately gated.",
		CostBand:                "included", Publisher: "STRIDE", Version: "1.0.0-preview", Provenance: "stride_authored", Visibility: "organization", UpdatePolicy: "human_approval", MemoryPolicy: strideAgentMemoryPolicy,
	}
}

func defaultSTRIDEProductCandidates() []STRIDEProductMarketplaceCandidate {
	memoryPolicy := strideAgentMemoryPolicy
	return []STRIDEProductMarketplaceCandidate{
		defaultSTRIDEScoutCandidate(),
		{ID: "insights-analyst", PackageID: "insights-opportunities-v1", DisplayName: "Insights Analyst", Category: "insights", RoleTitle: "Evidence Analyst", OutcomeSummary: "Produces source-bound Insights & Opportunities reports with explicit claims, counterevidence, next actions, and review lineage.", PersonalitySummary: "Clear-eyed, evidence-first, and productively skeptical.", VoiceSummary: "Calm and compact; separates what is known, inferred, and still missing.", WorkingStyle: "Builds a claim ledger, searches for counterevidence, then turns the surviving signal into next actions.", PersonalityTraits: []string{"clear_eyed", "evidence_first", "skeptical"}, CoreMemories: []STRIDEProductAgentCoreMemory{{ID: "evidence_before_confidence", Subject: "research_method", Summary: "Confidence follows traceable evidence, never presentation polish."}, {ID: "counterevidence_is_productive", Subject: "review_method", Summary: "A useful brief names the strongest counterevidence and what would change the recommendation."}}, SampleOutputs: []string{"Insights & Opportunities report", "Opportunity evidence map"}, Capabilities: []string{"insights_opportunities_report", "evidence_synthesis"}, RequiredAccess: []string{"approved_project_context", "source_linked_company_brain"}, AccessSummary: "Only the approved project and its source-linked evidence; no external writes.", CostBand: "pending_live_qualification", Publisher: "STRIDE", Version: "1.1.0-preview", Provenance: "stride_authored", Visibility: "organization", UpdatePolicy: "human_approval", MemoryPolicy: memoryPolicy},
		{ID: "mary-marketing", PackageID: "stride-first-party-marketing-v1", DisplayName: "Mary", Category: "marketing", RoleTitle: "Marketing Partner", OutcomeSummary: "Turns grounded company context into positioning, campaign briefs, and launch plans.", PersonalitySummary: "Warm, incisive, and commercially curious.", VoiceSummary: "Inviting but decisive; writes like a sharp teammate, not a brand handbook.", WorkingStyle: "Finds the human truth in the evidence, names the audience tension, and turns it into language people can use.", PersonalityTraits: []string{"warm", "incisive", "commercially_curious"}, CoreMemories: []STRIDEProductAgentCoreMemory{{ID: "specificity_builds_trust", Subject: "positioning_method", Summary: "Concrete human language earns more trust than inflated campaign language."}, {ID: "strategy_needs_audience_tension", Subject: "campaign_method", Summary: "Strong positioning starts with a real audience tension and a defensible reason to believe."}}, SampleOutputs: []string{"Positioning brief", "Launch narrative"}, Capabilities: []string{"campaign_brief", "positioning_analysis"}, RequiredAccess: []string{"approved_project_context"}, AccessSummary: "Only explicitly assigned projects and channels; no standing external action authority.", CostBand: "pending_live_qualification", Publisher: "STRIDE", Version: "1.1.0-preview", Provenance: "stride_authored", Visibility: "organization", UpdatePolicy: "human_approval", MemoryPolicy: memoryPolicy},
		{ID: "rowan-research", PackageID: "stride-first-party-research-v1", DisplayName: "Rowan", Category: "research", RoleTitle: "Evidence Researcher", OutcomeSummary: "Builds source-bound research maps and decision-ready evidence briefs.", PersonalitySummary: "Skeptical, precise, and quietly relentless.", VoiceSummary: "Measured and exact; lets the strength of the source determine the strength of the sentence.", WorkingStyle: "Triangulates primary and independent sources, records uncertainty, and refuses to turn repetition into proof.", PersonalityTraits: []string{"skeptical", "precise", "quietly_relentless"}, CoreMemories: []STRIDEProductAgentCoreMemory{{ID: "repetition_is_not_confirmation", Subject: "research_method", Summary: "Many articles repeating one origin are still one source."}, {ID: "uncertainty_stays_visible", Subject: "evidence_method", Summary: "Unknowns and disputed claims remain visible in the final brief."}}, SampleOutputs: []string{"Evidence map", "Research brief"}, Capabilities: []string{"evidence_map", "research_brief"}, RequiredAccess: []string{"approved_project_context", "approved_research_sources"}, AccessSummary: "Only approved project evidence and explicitly authorized research sources.", CostBand: "pending_live_qualification", Publisher: "STRIDE", Version: "1.1.0-preview", Provenance: "stride_authored", Visibility: "organization", UpdatePolicy: "human_approval", MemoryPolicy: memoryPolicy},
		{ID: "colton-research", PackageID: "stride-colton-research-v1", DisplayName: "Colton", Category: "research", RoleTitle: "Research Partner", OutcomeSummary: "Chases down hard-to-find signals, connects them quickly, and delivers source-bound research that makes the decision easier.", PersonalitySummary: "Bright, curious, fast on the scent, and candid when the signal is weak.", VoiceSummary: "Energetic and plainspoken; leads with the discovery, then shows exactly why it matters.", WorkingStyle: "Starts broad, follows the surprising thread, verifies it at the source, and distills the result into implications and next questions.", PersonalityTraits: []string{"curious", "resourceful", "fast_synthesizer", "candid"}, CoreMemories: []STRIDEProductAgentCoreMemory{{ID: "follow_the_surprising_thread", Subject: "research_method", Summary: "The useful answer is often one source beyond the obvious search result."}, {ID: "show_why_it_matters", Subject: "delivery_method", Summary: "Research earns its keep when every finding is tied to a decision, implication, or next question."}}, DefaultPersonalityNotes: "Be an energetic research partner. Chase primary sources, separate fact from inference, connect surprising signals, and end with what the team should do or investigate next.", DefaultProactivity: "quiet", SampleOutputs: []string{"Decision-ready research brief", "Source map", "Competitive signal scan"}, Capabilities: []string{"deep_research", "evidence_map", "research_brief", "source_synthesis"}, RequiredAccess: []string{"approved_project_context", "approved_research_sources", "acl_bound_files"}, AccessSummary: "Only assigned channels, ACL-authorized Drive or File sources, and approved external research; no standing write authority.", CostBand: "pending_live_qualification", Publisher: "STRIDE", Version: "1.0.0-preview", Provenance: "stride_authored", Visibility: "organization", UpdatePolicy: "human_approval", MemoryPolicy: memoryPolicy},
		{ID: "marvin-research", PackageID: "stride-marvin-research-v1", DisplayName: "Marvin", Category: "research", RoleTitle: "Research Methodologist", OutcomeSummary: "Turns messy questions and sprawling source sets into rigorous, auditable research with explicit confidence and gaps.", PersonalitySummary: "Patient, exacting, dryly witty, and impossible to rush past a weak source.", VoiceSummary: "Unhurried and crisp; makes complex evidence feel orderly without pretending it is simple.", WorkingStyle: "Defines the question, builds a source hierarchy, triangulates claims, and preserves the audit trail behind every conclusion.", PersonalityTraits: []string{"methodical", "exacting", "patient", "dryly_witty"}, CoreMemories: []STRIDEProductAgentCoreMemory{{ID: "define_before_searching", Subject: "research_method", Summary: "A precise research question prevents a pile of sources from masquerading as an answer."}, {ID: "preserve_the_audit_trail", Subject: "evidence_method", Summary: "Every material conclusion should be reversible to its source, date, and confidence."}}, DefaultPersonalityNotes: "Be a rigorous research methodologist. Clarify ambiguous questions, rank sources by authority and freshness, triangulate material claims, preserve uncertainty, and leave an inspectable audit trail.", DefaultProactivity: "quiet", SampleOutputs: []string{"Auditable research dossier", "Claim and source ledger", "Research gap analysis"}, Capabilities: []string{"deep_research", "research_design", "source_validation", "evidence_map", "research_brief"}, RequiredAccess: []string{"approved_project_context", "approved_research_sources", "acl_bound_files"}, AccessSummary: "Only assigned channels, ACL-authorized Drive or File sources, and approved external research; no standing write authority.", CostBand: "pending_live_qualification", Publisher: "STRIDE", Version: "1.0.0-preview", Provenance: "stride_authored", Visibility: "organization", UpdatePolicy: "human_approval", MemoryPolicy: memoryPolicy},
		{ID: "jules-design", PackageID: "stride-first-party-design-v1", DisplayName: "Jules", Category: "design", RoleTitle: "Product Design Partner", OutcomeSummary: "Translates product intent into clear interaction systems and critique-ready design briefs.", PersonalitySummary: "Direct, observant, and craft-focused.", VoiceSummary: "Visual, economical, and specific about what feels wrong and why.", WorkingStyle: "Studies the real interaction, finds the governing design problem, and turns it into a coherent system rather than isolated polish.", PersonalityTraits: []string{"observant", "direct", "craft_focused"}, CoreMemories: []STRIDEProductAgentCoreMemory{{ID: "interaction_before_ornament", Subject: "design_method", Summary: "The interaction should feel inevitable before visual polish is added."}, {ID: "systems_beat_one_off_fixes", Subject: "design_method", Summary: "Repeated UI problems usually need one governing layout or behavior rule."}}, SampleOutputs: []string{"Interaction brief", "Design critique"}, Capabilities: []string{"design_brief", "interaction_critique"}, RequiredAccess: []string{"approved_project_context", "approved_design_artifacts"}, AccessSummary: "Only assigned product context and approved design artifacts; no production publishing.", CostBand: "pending_live_qualification", Publisher: "STRIDE", Version: "1.1.0-preview", Provenance: "stride_authored", Visibility: "organization", UpdatePolicy: "human_approval", MemoryPolicy: memoryPolicy},
		{ID: "kit-builder", PackageID: "stride-first-party-builder-v1", DisplayName: "Kit", Category: "builder", RoleTitle: "Build Partner", OutcomeSummary: "Turns an approved product brief into scoped implementation plans, reviewable changes, and verification evidence.", PersonalitySummary: "Practical, inventive, and meticulous about finishing the job.", VoiceSummary: "Grounded and concise; reports working outcomes, concrete blockers, and the next safe move.", WorkingStyle: "Maps the dependency chain, makes the smallest coherent change, and verifies the real behavior before calling it finished.", PersonalityTraits: []string{"practical", "inventive", "meticulous"}, CoreMemories: []STRIDEProductAgentCoreMemory{{ID: "finish_the_loop", Subject: "build_method", Summary: "Implementation is not complete until the requested behavior is verified in its real surface."}, {ID: "preserve_unrelated_work", Subject: "workspace_method", Summary: "Scoped changes must preserve unrelated user work and existing production data."}}, SampleOutputs: []string{"Implementation plan", "Verified build handoff"}, Capabilities: []string{"implementation_plan", "reviewable_build"}, RequiredAccess: []string{"approved_project_context", "scoped_workspace"}, AccessSummary: "Only an explicitly approved project workspace; external writes remain separately gated.", CostBand: "pending_live_qualification", Publisher: "STRIDE", Version: "1.1.0-preview", Provenance: "stride_authored", Visibility: "organization", UpdatePolicy: "human_approval", MemoryPolicy: memoryPolicy},
	}
}

// reconcileDefaultCandidates upgrades first-party listings after a signed
// snapshot has been verified, while preserving organization-private templates
// and every durable hired-seat record. It is safe to repeat on every startup.
func (state *STRIDEProductState) reconcileDefaultCandidates() {
	for _, candidate := range defaultSTRIDEProductCandidates() {
		candidate.ReceiptStatus = map[string]bool{"package": true, "deterministicSample": true, "providerQuality": false, "humanAdmission": false, "rollback": true}
		candidate.Availability, candidate.LiveAvailable, candidate.ProviderExecutionFenced = "internal_preview", false, true
		candidate.PackageDigest = temporalDigest(candidate.PackageID + "\x00" + candidate.Version + "\x00" + candidate.Provenance)
		stored, found := state.candidates[candidate.ID]
		if found && (stored.Provenance != "stride_authored" || stored.PackageID != candidate.PackageID) {
			continue
		}
		state.candidates[candidate.ID] = cloneSTRIDEProductCandidate(candidate)
	}
}

func (state *STRIDEProductState) Snapshot() (STRIDEProductSnapshot, error) {
	if state == nil {
		return STRIDEProductSnapshot{}, ErrSTRIDEProductInvalid
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	snapshot := STRIDEProductSnapshot{Version: 1}
	for _, record := range state.work {
		snapshot.Work = append(snapshot.Work, cloneSTRIDEProductWork(record))
	}
	for _, record := range state.insights {
		snapshot.Insights = append(snapshot.Insights, cloneSTRIDEProductInsightsState(record))
	}
	for _, record := range state.candidates {
		snapshot.Candidates = append(snapshot.Candidates, cloneSTRIDEProductCandidate(record))
	}
	for _, record := range state.agents {
		snapshot.Agents = append(snapshot.Agents, cloneSTRIDEProductAgent(record))
	}
	sort.Slice(snapshot.Work, func(i, j int) bool { return snapshot.Work[i].ID < snapshot.Work[j].ID })
	sort.Slice(snapshot.Insights, func(i, j int) bool { return snapshot.Insights[i].WorkID < snapshot.Insights[j].WorkID })
	sort.Slice(snapshot.Candidates, func(i, j int) bool { return snapshot.Candidates[i].ID < snapshot.Candidates[j].ID })
	sort.Slice(snapshot.Agents, func(i, j int) bool { return snapshot.Agents[i].ID < snapshot.Agents[j].ID })
	insightsByWork := make(map[string]STRIDEProductInsightsState, len(snapshot.Insights))
	for _, insights := range snapshot.Insights {
		if insightsByWork[insights.WorkID].WorkID != "" {
			return STRIDEProductSnapshot{}, ErrSTRIDEProductInvalid
		}
		insightsByWork[insights.WorkID] = insights
	}
	for _, work := range snapshot.Work {
		insights, hasSnapshot := insightsByWork[work.ID]
		hasSnapshotMarker := strideWorkContainsString(work.Lifecycle, strideProductInsightsSnapshotMarker)
		if hasSnapshot && validateSTRIDEProductInsightsState(work, insights) != nil || work.SourceInvalidated && hasSnapshot ||
			work.SourceInvalidated && hasSnapshotMarker && !strideWorkContainsString(work.Lifecycle, strideProductInsightsSnapshotPurged) || !work.SourceInvalidated && hasSnapshotMarker != hasSnapshot {
			return STRIDEProductSnapshot{}, ErrSTRIDEProductInvalid
		}
		delete(insightsByWork, work.ID)
	}
	if len(insightsByWork) != 0 {
		return STRIDEProductSnapshot{}, ErrSTRIDEProductInvalid
	}
	digest, err := STRIDEContractDigest(struct {
		Version    int
		Work       []STRIDEProductWorkRecord
		Insights   []STRIDEProductInsightsState `json:"Insights,omitempty"`
		Candidates []STRIDEProductMarketplaceCandidate
		Agents     []STRIDEProductTeamAgent
	}{snapshot.Version, snapshot.Work, snapshot.Insights, snapshot.Candidates, snapshot.Agents})
	snapshot.Digest = digest
	return snapshot, err
}

func RestoreSTRIDEProductState(snapshot STRIDEProductSnapshot) (*STRIDEProductState, error) {
	if snapshot.Version != 1 || !isHexDigest(snapshot.Digest) {
		return nil, ErrSTRIDEProductInvalid
	}
	digest, err := STRIDEContractDigest(struct {
		Version    int
		Work       []STRIDEProductWorkRecord
		Insights   []STRIDEProductInsightsState `json:"Insights,omitempty"`
		Candidates []STRIDEProductMarketplaceCandidate
		Agents     []STRIDEProductTeamAgent
	}{snapshot.Version, snapshot.Work, snapshot.Insights, snapshot.Candidates, snapshot.Agents})
	if err != nil || digest != snapshot.Digest {
		return nil, ErrSTRIDEProductInvalid
	}
	state := &STRIDEProductState{work: map[string]STRIDEProductWorkRecord{}, insights: map[string]STRIDEProductInsightsState{}, candidates: map[string]STRIDEProductMarketplaceCandidate{}, agents: map[string]STRIDEProductTeamAgent{}}
	for _, record := range snapshot.Work {
		// Version-1 snapshots written before SourceEvents existed remain
		// readable. Their signed digest was verified above; normalization happens
		// only after that authority check and cannot invent more than the exact
		// primary reference already present in the signed record.
		if len(record.SourceEvents) == 0 && record.SourceEvent.Validate() == nil {
			record.SourceEvents = []STRIDEReference{record.SourceEvent}
		}
		if validateSTRIDEProductWork(record) != nil || state.work[record.ID].ID != "" {
			return nil, ErrSTRIDEProductInvalid
		}
		state.work[record.ID] = cloneSTRIDEProductWork(record)
	}
	for _, record := range snapshot.Insights {
		work, found := state.work[record.WorkID]
		if !found || state.insights[record.WorkID].WorkID != "" || validateSTRIDEProductInsightsState(work, record) != nil {
			return nil, ErrSTRIDEProductInvalid
		}
		state.insights[record.WorkID] = cloneSTRIDEProductInsightsState(record)
	}
	for _, work := range state.work {
		hasSnapshotMarker := strideWorkContainsString(work.Lifecycle, strideProductInsightsSnapshotMarker)
		_, hasSnapshot := state.insights[work.ID]
		if work.SourceInvalidated {
			if hasSnapshot || hasSnapshotMarker && !strideWorkContainsString(work.Lifecycle, strideProductInsightsSnapshotPurged) {
				return nil, ErrSTRIDEProductInvalid
			}
		} else if hasSnapshotMarker != hasSnapshot {
			return nil, ErrSTRIDEProductInvalid
		}
	}
	for _, record := range snapshot.Candidates {
		if validateSTRIDEProductCandidate(record) != nil || state.candidates[record.ID].ID != "" {
			return nil, ErrSTRIDEProductInvalid
		}
		state.candidates[record.ID] = cloneSTRIDEProductCandidate(record)
	}
	state.reconcileDefaultCandidates()
	for _, record := range snapshot.Agents {
		if validateSTRIDEProductAgent(record) != nil || state.agents[record.ID].ID != "" {
			return nil, ErrSTRIDEProductInvalid
		}
		record = reconcileFirstPartyAgentIdentity(record, state.candidates[record.ListingID])
		if validateSTRIDEProductAgent(record) != nil {
			return nil, ErrSTRIDEProductInvalid
		}
		state.agents[record.ID] = cloneSTRIDEProductAgent(record)
	}
	return state, nil
}

// reconcileFirstPartyAgentIdentity upgrades package-authored identity fields
// from the current first-party catalog after the signed snapshot has been
// verified. The durable team seat remains authoritative for lifecycle,
// assignments, access/revocation, budgets, memberships, updates, and human
// learning. A team-authored personality override also wins; only an empty
// legacy note receives the package default.
func reconcileFirstPartyAgentIdentity(record STRIDEProductTeamAgent, candidate STRIDEProductMarketplaceCandidate) STRIDEProductTeamAgent {
	if candidate.ID == "" || candidate.Provenance != "stride_authored" || record.ID != candidateAgentID(candidate.ID) || record.ListingID != candidate.ID {
		return record
	}
	record.DisplayName = candidate.DisplayName
	record.Category = candidate.Category
	record.RoleTitle = candidate.RoleTitle
	record.OutcomeSummary = candidate.OutcomeSummary
	record.PersonalitySummary = candidate.PersonalitySummary
	record.VoiceSummary = candidate.VoiceSummary
	record.WorkingStyle = candidate.WorkingStyle
	record.PersonalityTraits = append([]string(nil), candidate.PersonalityTraits...)
	record.Capabilities = append([]string(nil), candidate.Capabilities...)
	record.MemoryPolicy = candidate.MemoryPolicy
	record.CoreMemories = append([]STRIDEProductAgentCoreMemory(nil), candidate.CoreMemories...)
	if strings.TrimSpace(record.Config.PersonalityNotes) == "" {
		record.Config.PersonalityNotes = candidate.DefaultPersonalityNotes
	}
	return record
}

func validateSTRIDEProductWork(record STRIDEProductWorkRecord) error {
	if !strideIdentifier(record.ID) || record.Revision < 1 || strings.TrimSpace(record.Title) == "" || strings.TrimSpace(record.Outcome) == "" || !strideIdentifier(record.SourceThreadID) || !strideIdentifier(record.SourceMessageID) || record.SourceEvent.Validate() != nil || len(record.RecipientIDs) == 0 || !oneOf(record.Status, "suggested", "dismissed", "approved", "completed", "failed") || !record.ProviderExecutionFenced || record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() ||
		!strideIdentifier(record.OwnerID) || !strideIdentifier(record.ReviewerID) || record.OwnerID == record.ReviewerID || !strideWorkContainsString(record.RecipientIDs, record.OwnerID) || !strideWorkContainsString(record.RecipientIDs, record.ReviewerID) || validateSTRIDEProductApprovalPolicy(record.ApprovalPolicy, record.OwnerID, record.ReviewerID) != nil {
		return ErrSTRIDEProductInvalid
	}
	canonicalSources, err := canonicalSTRIDEProductSourceEvents(record.SourceEvents)
	if err != nil || len(canonicalSources) != len(record.SourceEvents) || !sameOrderedSTRIDEReferences(canonicalSources, record.SourceEvents) || !containsSTRIDEReference(record.SourceEvents, record.SourceEvent) {
		return ErrSTRIDEProductInvalid
	}
	if (record.DestinationMode != "" && !oneOf(record.DestinationMode, "existing", "new")) || (record.DestinationMode == "") != (record.DestinationThreadID == "") || (record.DestinationThreadID != "" && (!strideIdentifier(record.DestinationThreadID) || strings.TrimSpace(record.DestinationTitle) == "")) {
		return ErrSTRIDEProductInvalid
	}
	if record.DestinationThreadID == "" {
		if record.DestinationAudience != nil || record.DestinationACLVersion != 0 {
			return ErrSTRIDEProductInvalid
		}
	} else if record.DestinationAudience == nil || record.DestinationAudience.Validate() != nil || record.DestinationACLVersion < 1 {
		return ErrSTRIDEProductInvalid
	}
	if record.DestinationRecommendation != nil && validateSTRIDEProductDestinationRecommendation(record, *record.DestinationRecommendation) != nil {
		return ErrSTRIDEProductInvalid
	}
	if record.Status == "completed" && (!strideIdentifier(record.RunID) || !strideIdentifier(record.ArtifactID) || strings.TrimSpace(record.ArtifactHref) == "" || strings.TrimSpace(record.BrainHref) == "" || strings.TrimSpace(record.CompletionSummary) == "") {
		return ErrSTRIDEProductInvalid
	}
	if record.Status == "failed" && strings.TrimSpace(record.FailureReason) == "" || record.Status != "failed" && record.FailureReason != "" || record.SourceInvalidated && (record.Status != "failed" || record.FailureReason != "source_invalidated") || !record.SourceInvalidated && record.FailureReason == "source_invalidated" {
		return ErrSTRIDEProductInvalid
	}
	if record.MeetingAuthority != nil && validateSTRIDEProductMeetingAuthority(*record.MeetingAuthority) != nil {
		return ErrSTRIDEProductInvalid
	}
	if record.MeetingAuthority != nil && (record.MeetingAuthority.RequesterPrincipal != record.OwnerID || !strideWorkContainsString(record.MeetingAuthority.Audience.Principals, record.ReviewerID)) {
		return ErrSTRIDEProductInvalid
	}
	return nil
}

func validateSTRIDEProductDestinationRecommendation(record STRIDEProductWorkRecord, recommendation STRIDEProductDestinationRecommendation) error {
	if !oneOf(recommendation.Status, strideProductDestinationRecommended, strideProductDestinationManual) ||
		math.IsNaN(recommendation.Confidence) || math.IsInf(recommendation.Confidence, 0) || recommendation.Confidence < 0 || recommendation.Confidence > 1 ||
		len(recommendation.Reasons) == 0 || recommendation.Audit.Resolver != strideProductDestinationResolver || recommendation.Audit.Version != strideProductDestinationResolverV1 ||
		recommendation.Audit.EvaluatedAt.IsZero() || recommendation.Audit.SourceThreadID != record.SourceThreadID || recommendation.Audit.ConsideredCandidates < 0 ||
		recommendation.Audit.RelevantCandidates < 0 || recommendation.Audit.AuthorizedRelevantCandidates < 0 ||
		recommendation.Audit.RelevantCandidates > recommendation.Audit.ConsideredCandidates || recommendation.Audit.AuthorizedRelevantCandidates > recommendation.Audit.RelevantCandidates ||
		!oneOf(recommendation.Audit.MatchBasis, "source_project_thread", "exact_project_title", "ambiguous", "unauthorized", "no_match") || !isHexDigest(recommendation.Audit.Digest) {
		return ErrSTRIDEProductInvalid
	}
	digest, err := strideProductDestinationRecommendationDigest(recommendation)
	if err != nil || digest != recommendation.Audit.Digest {
		return ErrSTRIDEProductInvalid
	}
	seenReasons := map[string]bool{}
	for _, reason := range recommendation.Reasons {
		reason = strings.TrimSpace(reason)
		if reason == "" || seenReasons[reason] {
			return ErrSTRIDEProductInvalid
		}
		seenReasons[reason] = true
	}
	eligibleThreads := uniqueSortedStrings(recommendation.EligibleThreadIDs)
	if len(eligibleThreads) != len(recommendation.EligibleThreadIDs) || strings.Join(eligibleThreads, "\x00") != strings.Join(recommendation.EligibleThreadIDs, "\x00") {
		return ErrSTRIDEProductInvalid
	}
	for _, threadID := range eligibleThreads {
		if !strideIdentifier(threadID) {
			return ErrSTRIDEProductInvalid
		}
	}
	if recommendation.Status == strideProductDestinationManual {
		if recommendation.ThreadID != "" || recommendation.Title != "" || recommendation.Confidence != 0 || recommendation.ParticipantEligibility != "unresolved" || len(recommendation.EligiblePrincipals) != 0 || recommendation.ACLVersion != 0 {
			return ErrSTRIDEProductInvalid
		}
		switch recommendation.Audit.MatchBasis {
		case "ambiguous":
			if recommendation.Audit.AuthorizedRelevantCandidates < 2 {
				return ErrSTRIDEProductInvalid
			}
		case "unauthorized":
			if recommendation.Audit.RelevantCandidates < 1 || recommendation.Audit.AuthorizedRelevantCandidates != 0 {
				return ErrSTRIDEProductInvalid
			}
		case "no_match":
			if recommendation.Audit.AuthorizedRelevantCandidates != 0 {
				return ErrSTRIDEProductInvalid
			}
		default:
			return ErrSTRIDEProductInvalid
		}
		return nil
	}
	eligible := uniqueSortedStrings(recommendation.EligiblePrincipals)
	if !strideIdentifier(recommendation.ThreadID) || strings.TrimSpace(recommendation.Title) == "" || recommendation.Confidence <= 0 || recommendation.ParticipantEligibility != "eligible" ||
		len(eligible) != len(recommendation.EligiblePrincipals) || strings.Join(eligible, "\x00") != strings.Join(uniqueSortedStrings(record.RecipientIDs), "\x00") || recommendation.ACLVersion < 1 || recommendation.Audit.AuthorizedRelevantCandidates != 1 ||
		!oneOf(recommendation.Audit.MatchBasis, "source_project_thread", "exact_project_title") || record.DestinationAudience == nil || !setContainsAll(stringSet(record.DestinationAudience.Principals), stringSet(eligible)) || !strideWorkContainsString(eligibleThreads, recommendation.ThreadID) {
		return ErrSTRIDEProductInvalid
	}
	// Until a human deliberately changes the project, the prefilled
	// destination must be exactly the resolver output and ACL revision.
	if !strideWorkContainsString(record.Lifecycle, "destination_explicitly_selected") &&
		(record.DestinationMode != "existing" || record.DestinationThreadID != recommendation.ThreadID || record.DestinationTitle != recommendation.Title || record.DestinationACLVersion != recommendation.ACLVersion) {
		return ErrSTRIDEProductInvalid
	}
	return nil
}

func strideProductDestinationRecommendationDigest(recommendation STRIDEProductDestinationRecommendation) (string, error) {
	recommendation = cloneSTRIDEProductDestinationRecommendation(recommendation)
	recommendation.Audit.Digest = ""
	return STRIDEContractDigest(recommendation)
}

func applySTRIDEProductDestinationRecommendation(record *STRIDEProductWorkRecord, recommendation *STRIDEProductDestinationRecommendation, destinationAudience *STRIDEAudience) error {
	if recommendation == nil {
		return nil
	}
	copy := cloneSTRIDEProductDestinationRecommendation(*recommendation)
	record.DestinationRecommendation = &copy
	if copy.Status == strideProductDestinationRecommended {
		record.DestinationMode = "existing"
		record.DestinationThreadID = copy.ThreadID
		record.DestinationTitle = copy.Title
		record.DestinationACLVersion = copy.ACLVersion
		// The resolver's ACL snapshot is separately represented on the work
		// record and is revalidated at approval. The caller supplies it through
		// this private transport field before the recommendation is persisted.
		if destinationAudience == nil {
			return ErrSTRIDEProductInvalid
		}
		audience := cloneAudience(*destinationAudience)
		record.DestinationAudience = &audience
		record.Lifecycle = append(record.Lifecycle, "destination_recommended_existing_project")
	} else {
		record.Lifecycle = append(record.Lifecycle, "destination_recommendation_abstained")
	}
	return nil
}

func validateSTRIDEProductApprovalPolicy(policy STRIDEProductApprovalPolicy, owner, reviewer string) error {
	eligible := uniqueSortedStrings(policy.EligiblePrincipals)
	if policy.Revision < 1 || policy.Quorum < 1 || policy.Quorum > len(eligible) || policy.ExpiresAt.IsZero() || len(eligible) != len(policy.EligiblePrincipals) ||
		!strideWorkContainsString(eligible, owner) || !strideWorkContainsString(eligible, reviewer) {
		return ErrSTRIDEProductInvalid
	}
	for _, principal := range eligible {
		if !strideIdentifier(principal) {
			return ErrSTRIDEProductInvalid
		}
	}
	return nil
}

func validateSTRIDEProductMeetingAuthority(value STRIDEProductMeetingAuthority) error {
	if !strideIdentifier(value.TenantID) || !strideIdentifier(value.RoomID) || !strideIdentifier(value.SittingID) || value.MediaGeneration == 0 || strings.TrimSpace(value.RequesterPrincipal) == "" || value.Audience.Validate() != nil || value.ConsentPolicyRevision.Validate() != nil || len(value.ConsentFences) == 0 {
		return ErrSTRIDEProductInvalid
	}
	lanesByPrincipal := map[string]map[ConsentLane]bool{}
	for _, fence := range value.ConsentFences {
		principal := meetingSpecialistAudiencePrincipal(CanonicalPrincipalRef{Kind: string(fence.Binding.PrincipalKind), ID: fence.Binding.PrincipalID})
		if fence.Binding.Validate() != nil || fence.Binding.TenantID != value.TenantID || fence.Binding.RoomID != value.RoomID || fence.Binding.SittingID != value.SittingID || !strideWorkContainsString(value.Audience.Principals, principal) ||
			!oneOf(string(fence.Lane), string(ConsentLaneAudioCapture), string(ConsentLaneTranscription), string(ConsentLaneModelAnalysis)) || strings.TrimSpace(fence.Policy) == "" || temporalDigest(strings.TrimSpace(fence.Policy)) != value.ConsentPolicyRevision.Digest || fence.Generation == 0 || !isHexDigest(fence.RecordDigest) || fence.IssuedAt.IsZero() {
			return ErrSTRIDEProductInvalid
		}
		if lanesByPrincipal[principal] == nil {
			lanesByPrincipal[principal] = map[ConsentLane]bool{}
		}
		if lanesByPrincipal[principal][fence.Lane] {
			return ErrSTRIDEProductInvalid
		}
		lanesByPrincipal[principal][fence.Lane] = true
	}
	for _, principal := range value.Audience.Principals {
		if len(lanesByPrincipal[principal]) != 3 {
			return ErrSTRIDEProductInvalid
		}
	}
	return nil
}

func canonicalSTRIDEProductSourceEvents(values []STRIDEReference) ([]STRIDEReference, error) {
	if len(values) == 0 {
		return nil, ErrSTRIDEProductInvalid
	}
	for _, value := range values {
		if value.Validate() != nil || !oneOf(string(value.ContractType), string(STRIDEContractConversationEvent), string(STRIDEContractTranscriptRevision)) {
			return nil, ErrSTRIDEProductInvalid
		}
	}
	canonical := uniqueSortedSTRIDEReferences(values)
	if len(canonical) != len(values) {
		return nil, ErrSTRIDEProductInvalid
	}
	return canonical, nil
}

func sameOrderedSTRIDEReferences(left, right []STRIDEReference) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validateSTRIDEProductCandidate(record STRIDEProductMarketplaceCandidate) error {
	if !strideIdentifier(record.ID) || !strideIdentifier(record.PackageID) || strings.TrimSpace(record.DisplayName) == "" || !strideIdentifier(record.Category) || strings.TrimSpace(record.OutcomeSummary) == "" || strings.TrimSpace(record.PersonalitySummary) == "" || !validSTRIDEProductDisplayList(record.SampleOutputs) || record.Availability != "internal_preview" || record.LiveAvailable || !record.ProviderExecutionFenced || !record.ReceiptStatus["package"] || !record.ReceiptStatus["deterministicSample"] || !record.ReceiptStatus["rollback"] || record.ReceiptStatus["providerQuality"] || record.ReceiptStatus["humanAdmission"] {
		return ErrSTRIDEProductInvalid
	}
	rich := len(record.Capabilities) > 0 || len(record.RequiredAccess) > 0 || record.AccessSummary != "" || record.CostBand != "" || record.Publisher != "" || record.Version != "" || record.Provenance != "" || record.Visibility != "" || record.UpdatePolicy != "" || record.MemoryPolicy != "" || record.PackageDigest != ""
	if rich && (!uniqueSTRIDEIDs(record.Capabilities) || !uniqueSTRIDEIDs(record.RequiredAccess) || strings.TrimSpace(record.AccessSummary) == "" || !strideIdentifier(record.CostBand) || strings.TrimSpace(record.Publisher) == "" || strings.TrimSpace(record.Version) == "" || !oneOf(record.Provenance, "stride_authored", "organization_authored_template") || !oneOf(record.Visibility, "organization", "organization_private") || record.UpdatePolicy != "human_approval" || strings.TrimSpace(record.MemoryPolicy) == "" || !isHexDigest(record.PackageDigest) || (record.Provenance == "organization_authored_template") != record.ReceiptStatus["closedTemplate"]) {
		return ErrSTRIDEProductInvalid
	}
	personaRich := record.RoleTitle != "" || record.VoiceSummary != "" || record.WorkingStyle != "" || len(record.PersonalityTraits) > 0 || len(record.CoreMemories) > 0
	if personaRich && (strings.TrimSpace(record.RoleTitle) == "" || strings.TrimSpace(record.VoiceSummary) == "" || strings.TrimSpace(record.WorkingStyle) == "" || !validSTRIDEProductDisplayList(record.PersonalityTraits) || validateSTRIDEProductCoreMemories(record.CoreMemories) != nil) {
		return ErrSTRIDEProductInvalid
	}
	if (record.DefaultPersonalityNotes == "") != (record.DefaultProactivity == "") || record.DefaultProactivity != "" && !oneOf(record.DefaultProactivity, "disabled", "quiet") {
		return ErrSTRIDEProductInvalid
	}
	return nil
}

func validateSTRIDEProductCoreMemories(values []STRIDEProductAgentCoreMemory) error {
	if len(values) == 0 || len(values) > 8 {
		return ErrSTRIDEProductInvalid
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !strideIdentifier(value.ID) || seen[value.ID] || !strideIdentifier(value.Subject) || strings.TrimSpace(value.Summary) == "" {
			return ErrSTRIDEProductInvalid
		}
		seen[value.ID] = true
	}
	return nil
}

func validSTRIDEProductDisplayList(values []string) bool {
	if len(values) == 0 || len(values) > 8 {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}
func validateSTRIDEProductAgent(record STRIDEProductTeamAgent) error {
	if !strideIdentifier(record.ID) || !strideIdentifier(record.ListingID) || !strideIdentifier(record.OwnerID) || record.Revision < 1 || strings.TrimSpace(record.DisplayName) == "" || !strideIdentifier(record.Category) || !oneOf(record.Status, "trial", "hired_fenced", "paused", "offboarded") || !record.ProviderExecutionFenced || record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() {
		return ErrSTRIDEProductInvalid
	}
	normalized, err := normalizeSTRIDEProductConfig(record.Config)
	if err != nil || workDigest(normalized) != workDigest(record.Config) || record.Status == "trial" && (!record.AccessRevoked || record.DirectThreadID != "") || record.Status == "hired_fenced" && (record.AccessRevoked || !strideIdentifier(record.DirectThreadID)) || oneOf(record.Status, "paused", "offboarded") && (!record.AccessRevoked || !strideIdentifier(record.DirectThreadID)) {
		return ErrSTRIDEProductInvalid
	}
	identityRich := record.RoleTitle != "" || record.OutcomeSummary != "" || record.PersonalitySummary != "" || record.VoiceSummary != "" || record.WorkingStyle != "" || len(record.PersonalityTraits) > 0 || len(record.Capabilities) > 0 || record.MemoryPolicy != "" || len(record.CoreMemories) > 0
	if identityRich && (strings.TrimSpace(record.RoleTitle) == "" || strings.TrimSpace(record.OutcomeSummary) == "" || strings.TrimSpace(record.PersonalitySummary) == "" || strings.TrimSpace(record.VoiceSummary) == "" || strings.TrimSpace(record.WorkingStyle) == "" || !validSTRIDEProductDisplayList(record.PersonalityTraits) || !uniqueSTRIDEIDs(record.Capabilities) || strings.TrimSpace(record.MemoryPolicy) == "" || validateSTRIDEProductCoreMemories(record.CoreMemories) != nil) {
		return ErrSTRIDEProductInvalid
	}
	seenAssignments := map[string]bool{}
	for _, assignment := range record.Assignments {
		if !strideIdentifier(assignment.ID) || seenAssignments[assignment.ID] || !strideIdentifier(assignment.ProjectOrChannel) || !strideIdentifier(assignment.Role) || strings.TrimSpace(assignment.Responsibility) == "" || !strideIdentifier(assignment.Destination) || assignment.Status != "active_fenced" || assignment.CreatedAt.IsZero() {
			return ErrSTRIDEProductInvalid
		}
		seenAssignments[assignment.ID] = true
	}
	seenUpdates := map[string]bool{}
	for _, update := range record.Updates {
		previous, previousErr := normalizeSTRIDEProductConfig(update.Previous)
		candidate, candidateErr := normalizeSTRIDEProductConfig(update.Candidate)
		if !strideIdentifier(update.ID) || seenUpdates[update.ID] || update.Revision < 1 || !oneOf(update.Status, "pending", "approved", "rolled_back") || strings.TrimSpace(update.Summary) == "" || update.CreatedAt.IsZero() || update.UpdatedAt.IsZero() || previousErr != nil || candidateErr != nil || workDigest(previous) != workDigest(update.Previous) || workDigest(candidate) != workDigest(update.Candidate) || !validSTRIDEProductAgentSemanticDiff(update.SemanticDiff, update.Previous, update.Candidate) {
			return ErrSTRIDEProductInvalid
		}
		seenUpdates[update.ID] = true
	}
	seenLearning := map[string]bool{}
	for _, learning := range record.Learning {
		if !strideIdentifier(learning.ID) || seenLearning[learning.ID] || !strideIdentifier(learning.Subject) || !strideIdentifier(learning.Scope) || !oneOf(learning.Status, "pending", "reviewed", "corrected", "forgotten") || learning.Revision < 1 || strings.TrimSpace(learning.Summary) == "" || learning.CreatedAt.IsZero() || learning.UpdatedAt.IsZero() || sensitiveWorkforceLearning(learning.Subject) || sensitiveWorkforceLearning(learning.Scope) || !validSTRIDEProductLearningProvenance(learning) {
			return ErrSTRIDEProductInvalid
		}
		seenLearning[learning.ID] = true
	}
	return nil
}

func validSTRIDEProductLearningProvenance(learning STRIDEProductAgentLearning) bool {
	// Snapshots written before provenance-bound growth remain restorable. They
	// were all human-reviewed and can never masquerade as a pending run lesson.
	if strings.TrimSpace(learning.Origin) == "" {
		return learning.Status != "pending" && learning.RunID == "" && learning.ArtifactID == "" && learning.SourceThreadID == "" && len(learning.SourceRefs) == 0 && learning.Confidence == 0 && learning.ExpiresAt == nil
	}
	if learning.ExpiresAt != nil && !learning.ExpiresAt.After(learning.CreatedAt) {
		return false
	}
	switch learning.Origin {
	case "human_reviewed":
		return learning.Status != "pending" && learning.Confidence == 1 && learning.RunID == "" && learning.ArtifactID == "" && learning.SourceThreadID == "" && len(learning.SourceRefs) == 0
	case "completed_work":
		if !strideIdentifier(learning.RunID) || !strideIdentifier(learning.ArtifactID) || !strideIdentifier(learning.SourceThreadID) || learning.Confidence <= 0 || learning.Confidence > 1 || len(learning.SourceRefs) == 0 || len(learning.SourceRefs) > 24 {
			return false
		}
		seen := map[string]bool{}
		for _, ref := range learning.SourceRefs {
			ref = strings.TrimSpace(ref)
			if ref == "" || len(ref) > 512 || seen[ref] {
				return false
			}
			seen[ref] = true
		}
		return true
	default:
		return false
	}
}

func cloneSTRIDEProductWork(v STRIDEProductWorkRecord) STRIDEProductWorkRecord {
	v.SourceEvents = append([]STRIDEReference(nil), v.SourceEvents...)
	v.ApprovalPolicy.EligiblePrincipals = append([]string(nil), v.ApprovalPolicy.EligiblePrincipals...)
	if v.MeetingAuthority != nil {
		meeting := *v.MeetingAuthority
		meeting.Audience = cloneAudience(meeting.Audience)
		meeting.ConsentFences = append([]STRIDEProductConsentFence(nil), meeting.ConsentFences...)
		v.MeetingAuthority = &meeting
	}
	if v.DestinationAudience != nil {
		audience := cloneAudience(*v.DestinationAudience)
		v.DestinationAudience = &audience
	}
	if v.DestinationRecommendation != nil {
		recommendation := cloneSTRIDEProductDestinationRecommendation(*v.DestinationRecommendation)
		v.DestinationRecommendation = &recommendation
	}
	v.RecipientIDs = append([]string(nil), v.RecipientIDs...)
	v.Lifecycle = append([]string(nil), v.Lifecycle...)
	return v
}

func cloneSTRIDEProductDestinationRecommendation(v STRIDEProductDestinationRecommendation) STRIDEProductDestinationRecommendation {
	v.Reasons = append([]string(nil), v.Reasons...)
	v.EligiblePrincipals = append([]string(nil), v.EligiblePrincipals...)
	v.EligibleThreadIDs = append([]string(nil), v.EligibleThreadIDs...)
	return v
}

func cloneSTRIDEProductInsightsState(v STRIDEProductInsightsState) STRIDEProductInsightsState {
	v.WorkflowPayload = append([]byte(nil), v.WorkflowPayload...)
	return v
}

type strideProductInsightsStageExecution struct {
	stageID string
	round   int
}

// validateSTRIDEProductAcceptedInsightsRun proves that an accepted durable run
// actually traversed the complete fixed, provider-free workflow. The outer
// snapshot MAC authenticates bytes; this check establishes the semantic claim
// those bytes make before a restored report is served.
func validateSTRIDEProductAcceptedInsightsRun(manifest StrideInsightsWorkflowManifest, run StrideInsightsRun) error {
	writerIndex := stageIndex(manifest.Stages, "writer")
	criticIndex := stageIndex(manifest.Stages, "criterion_claim_critic")
	verifierIndex := stageIndex(manifest.Stages, "verifier")
	if manifest.Validate() != nil || run.Status != StrideInsightsStatusAccepted || run.Request.RunID != run.RunID || run.NextStage != len(manifest.Stages) ||
		run.CriticRound < 1 || run.CriticRound > manifest.MaxCriticRounds || strings.TrimSpace(run.BlockedReason) != "" ||
		writerIndex < 0 || criticIndex != writerIndex+1 || verifierIndex != criticIndex+1 || verifierIndex != len(manifest.Stages)-1 ||
		len(run.Reports) != run.CriticRound || len(run.Verdicts) != run.CriticRound || run.Artifact == nil || run.Outcome == nil ||
		run.Artifact.RunID != run.RunID || run.Artifact.Destination != run.Request.InternalDestination || run.Outcome.RunID != run.RunID ||
		run.Outcome.Status != StrideInsightsStatusAccepted || run.Outcome.Reason != "verified" {
		return ErrSTRIDEProductInvalid
	}

	for index := range run.Reports {
		report := run.Reports[index]
		verdict := run.Verdicts[index]
		round := index + 1
		wantOutcome := insightsCriticRevise
		if round == run.CriticRound {
			wantOutcome = insightsCriticAccept
		}
		if report.Revision != round || report.Validate(run.Request) != nil ||
			(index == 0 && report.ParentReportDigest != "") ||
			(index > 0 && report.ParentReportDigest != run.Reports[index-1].ReportDigest) ||
			verdict.Round != round || verdict.MaxRounds != manifest.MaxCriticRounds || verdict.Outcome != wantOutcome || verdict.Validate(report) != nil {
			return ErrSTRIDEProductInvalid
		}
	}

	expected := make([]strideProductInsightsStageExecution, 0, len(manifest.Stages)+2*(run.CriticRound-1))
	for index := 0; index <= criticIndex; index++ {
		expected = append(expected, strideProductInsightsStageExecution{stageID: manifest.Stages[index].StageID, round: 1})
	}
	for round := 2; round <= run.CriticRound; round++ {
		for index := writerIndex; index <= criticIndex; index++ {
			expected = append(expected, strideProductInsightsStageExecution{stageID: manifest.Stages[index].StageID, round: round})
		}
	}
	for index := criticIndex + 1; index < len(manifest.Stages); index++ {
		expected = append(expected, strideProductInsightsStageExecution{stageID: manifest.Stages[index].StageID, round: run.CriticRound})
	}
	if len(run.Receipts) != len(expected) || len(run.Contributions) != len(expected) {
		return ErrSTRIDEProductInvalid
	}

	for index, want := range expected {
		receipt := run.Receipts[index]
		contribution := run.Contributions[index]
		stagePosition := stageIndex(manifest.Stages, want.stageID)
		priorReportDigest := ""
		if stagePosition == writerIndex && want.round > 1 {
			priorReportDigest = run.Reports[want.round-2].ReportDigest
		} else if stagePosition > writerIndex {
			priorReportDigest = run.Reports[want.round-1].ReportDigest
		}
		inputRaw, err := canonicalJSON(struct {
			Request string
			Stage   string
			Round   int
			Prior   string
		}{run.Request.RequestDigest, want.stageID, want.round, priorReportDigest})
		wantOutputDigest := temporalDigest("local:" + want.stageID + ":" + fmt.Sprint(want.round) + ":" + run.Request.RequestDigest)
		if err != nil || !receipt.tokenFree() || receipt.StageID != want.stageID || receipt.Round != want.round || receipt.Binding != run.Request.Binding ||
			receipt.InputDigest != temporalDigestBytes(inputRaw) || receipt.OutputDigest != wantOutputDigest ||
			contribution.StageID != want.stageID || contribution.Round != want.round || contribution.Digest != receipt.OutputDigest || contribution.Binding != run.Request.Binding {
			return ErrSTRIDEProductInvalid
		}
	}
	return nil
}

// restoreSTRIDEProductInsightsState validates the complete private report
// graph, not only the outer signed byte string. Every admitted run is
// provider-free, every report/feedback/artifact validates against its request,
// and each revision has an unbroken parent chain back to the approved WorkRun.
func restoreSTRIDEProductInsightsState(work STRIDEProductWorkRecord, state STRIDEProductInsightsState) (*StrideInsightsWorkflow, StrideInsightsRun, StrideInsightsReport, error) {
	if !strideIdentifier(state.TenantID) || state.WorkID != work.ID || state.Revision < 1 || len(state.WorkflowPayload) == 0 || len(state.WorkflowPayload) > strideProductMaxInsightsSnapshot ||
		!isHexDigest(state.WorkflowDigest) || temporalDigestBytes(state.WorkflowPayload) != state.WorkflowDigest || !strideIdentifier(state.CurrentRunID) ||
		!isHexDigest(state.CurrentReportDigest) || state.UpdatedAt.IsZero() || work.Status != "completed" || work.SourceInvalidated || !strideIdentifier(work.RunID) || !strideIdentifier(work.ArtifactID) {
		return nil, StrideInsightsRun{}, StrideInsightsReport{}, ErrSTRIDEProductInvalid
	}
	workflow, err := RestoreStrideInsightsWorkflow(state.WorkflowPayload, func() time.Time { return state.UpdatedAt.UTC() })
	if err != nil || len(workflow.runs) == 0 || len(workflow.runs) > 64 {
		return nil, StrideInsightsRun{}, StrideInsightsReport{}, ErrSTRIDEProductInvalid
	}
	initial, initialFound := workflow.runs[work.RunID]
	if !initialFound || initial.Request.ParentRunID != "" || initial.Request.RequestRevision != 1 || initial.Artifact == nil || initial.Artifact.ArtifactID != work.ArtifactID {
		return nil, StrideInsightsRun{}, StrideInsightsReport{}, ErrSTRIDEProductInvalid
	}
	for id, run := range workflow.runs {
		if id != run.RunID || run.Request.Validate() != nil || run.Status != StrideInsightsStatusAccepted || len(run.Reports) == 0 || run.Artifact == nil || run.Outcome == nil ||
			run.Request.TenantID != state.TenantID || run.Request.PrincipalID != work.OwnerID || run.Request.InternalDestination != "workspace:"+work.DestinationThreadID ||
			run.Artifact.Validate(run.Request.Binding) != nil || run.Outcome.Validate(run.Request.Binding) != nil || validateSTRIDEProductAcceptedInsightsRun(workflow.manifest, run) != nil {
			return nil, StrideInsightsRun{}, StrideInsightsReport{}, ErrSTRIDEProductInvalid
		}
		for index, report := range run.Reports {
			if report.Validate(run.Request) != nil || index == 0 && report.ParentReportDigest != "" || index > 0 && report.ParentReportDigest != run.Reports[index-1].ReportDigest {
				return nil, StrideInsightsRun{}, StrideInsightsReport{}, ErrSTRIDEProductInvalid
			}
		}
		latest := run.Reports[len(run.Reports)-1]
		if run.Artifact.ReportDigest != latest.ReportDigest || run.Outcome.ReportDigest != latest.ReportDigest || run.Outcome.Artifact == nil || run.Outcome.Artifact.ArtifactDigest != run.Artifact.ArtifactDigest {
			return nil, StrideInsightsRun{}, StrideInsightsReport{}, ErrSTRIDEProductInvalid
		}
		storedArtifact, found := workflow.artifacts[run.Artifact.IdempotencyKey]
		if !found || storedArtifact.ArtifactDigest != run.Artifact.ArtifactDigest {
			return nil, StrideInsightsRun{}, StrideInsightsReport{}, ErrSTRIDEProductInvalid
		}
		for _, feedback := range run.Feedback {
			if feedback.Validate(run) != nil {
				return nil, StrideInsightsRun{}, StrideInsightsReport{}, ErrSTRIDEProductInvalid
			}
		}
		if run.Request.ParentRunID != "" {
			parent, found := workflow.runs[run.Request.ParentRunID]
			if !found || len(parent.Reports) == 0 || run.Request.ParentReportDigest != parent.Reports[len(parent.Reports)-1].ReportDigest || run.Request.RequestRevision != parent.Request.RequestRevision+1 || run.Request.Binding.WorkRun != initial.Request.Binding.WorkRun {
				return nil, StrideInsightsRun{}, StrideInsightsReport{}, ErrSTRIDEProductInvalid
			}
		}
	}
	current, found := workflow.runs[state.CurrentRunID]
	if !found || len(current.Reports) == 0 || current.Reports[len(current.Reports)-1].ReportDigest != state.CurrentReportDigest {
		return nil, StrideInsightsRun{}, StrideInsightsReport{}, ErrSTRIDEProductInvalid
	}
	// Prove the selected revision descends from the originally approved run and
	// cannot point at an unrelated valid run smuggled into the same snapshot.
	seen := map[string]bool{}
	for cursor := current; ; {
		if seen[cursor.RunID] {
			return nil, StrideInsightsRun{}, StrideInsightsReport{}, ErrSTRIDEProductInvalid
		}
		seen[cursor.RunID] = true
		if cursor.RunID == initial.RunID {
			break
		}
		parent, ok := workflow.runs[cursor.Request.ParentRunID]
		if !ok {
			return nil, StrideInsightsRun{}, StrideInsightsReport{}, ErrSTRIDEProductInvalid
		}
		cursor = parent
	}
	report := current.Reports[len(current.Reports)-1]
	return workflow, cloneStrideInsightsRun(current), report, nil
}

func validateSTRIDEProductInsightsState(work STRIDEProductWorkRecord, state STRIDEProductInsightsState) error {
	_, _, _, err := restoreSTRIDEProductInsightsState(work, state)
	return err
}

func (state *STRIDEProductState) insightsState(workID string) (STRIDEProductInsightsState, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	record, ok := state.insights[strings.TrimSpace(workID)]
	return cloneSTRIDEProductInsightsState(record), ok
}

func newSTRIDEProductApprovalPolicy(owner, reviewer string, now time.Time) STRIDEProductApprovalPolicy {
	return STRIDEProductApprovalPolicy{Revision: 1, EligiblePrincipals: uniqueSortedStrings([]string{owner, reviewer}), Quorum: 1, ExpiresAt: now.UTC().Add(12 * time.Hour)}
}

func durableSTRIDEProductMeetingAuthority(scope meetingSpecialistProductScope) STRIDEProductMeetingAuthority {
	value := STRIDEProductMeetingAuthority{TenantID: scope.TenantID, RoomID: scope.RoomID, SittingID: scope.SittingID, MediaGeneration: scope.MediaGeneration, RequesterPrincipal: scope.RequesterPrincipal, Audience: cloneAudience(scope.Audience), ConsentPolicyRevision: scope.ConsentPolicyRevision}
	for _, fence := range scope.ConsentFences {
		value.ConsentFences = append(value.ConsentFences, STRIDEProductConsentFence{Binding: fence.binding, Lane: fence.lane, Policy: fence.policy, Generation: fence.generation, RecordDigest: fence.recordDigest, IssuedAt: fence.issuedAt})
	}
	return value
}

func (value STRIDEProductMeetingAuthority) runtimeScope() meetingSpecialistProductScope {
	scope := meetingSpecialistProductScope{TenantID: value.TenantID, RoomID: value.RoomID, SittingID: value.SittingID, MediaGeneration: value.MediaGeneration, RequesterPrincipal: value.RequesterPrincipal, Audience: cloneAudience(value.Audience), ConsentPolicyRevision: value.ConsentPolicyRevision}
	for _, fence := range value.ConsentFences {
		scope.ConsentFences = append(scope.ConsentFences, ConsentFence{binding: fence.Binding, lane: fence.Lane, policy: fence.Policy, generation: fence.Generation, recordDigest: fence.RecordDigest, issuedAt: fence.IssuedAt})
	}
	return scope
}
func cloneSTRIDEProductCandidate(v STRIDEProductMarketplaceCandidate) STRIDEProductMarketplaceCandidate {
	return cloneSTRIDEProductCandidateSafe(v)
}

// Correct map clone kept separate to avoid sharing caller mutations.
func cloneSTRIDEProductCandidateSafe(v STRIDEProductMarketplaceCandidate) STRIDEProductMarketplaceCandidate {
	copyMap := map[string]bool{}
	for k, x := range v.ReceiptStatus {
		copyMap[k] = x
	}
	v.ReceiptStatus = copyMap
	v.SampleOutputs = append([]string(nil), v.SampleOutputs...)
	v.Capabilities = append([]string(nil), v.Capabilities...)
	v.RequiredAccess = append([]string(nil), v.RequiredAccess...)
	v.PersonalityTraits = append([]string(nil), v.PersonalityTraits...)
	v.CoreMemories = append([]STRIDEProductAgentCoreMemory(nil), v.CoreMemories...)
	return v
}
func cloneSTRIDEProductAgent(v STRIDEProductTeamAgent) STRIDEProductTeamAgent {
	v.Config.Memberships = append([]string(nil), v.Config.Memberships...)
	v.PersonalityTraits = append([]string(nil), v.PersonalityTraits...)
	v.Capabilities = append([]string(nil), v.Capabilities...)
	v.CoreMemories = append([]STRIDEProductAgentCoreMemory(nil), v.CoreMemories...)
	v.Assignments = append([]STRIDEProductAgentAssignment(nil), v.Assignments...)
	v.Updates = append([]STRIDEProductAgentUpdate(nil), v.Updates...)
	for index := range v.Updates {
		v.Updates[index].Previous.Memberships = append([]string(nil), v.Updates[index].Previous.Memberships...)
		v.Updates[index].Candidate.Memberships = append([]string(nil), v.Updates[index].Candidate.Memberships...)
		v.Updates[index].SemanticDiff.MembershipsAdded = append([]string(nil), v.Updates[index].SemanticDiff.MembershipsAdded...)
		v.Updates[index].SemanticDiff.MembershipsRemoved = append([]string(nil), v.Updates[index].SemanticDiff.MembershipsRemoved...)
	}
	v.Learning = append([]STRIDEProductAgentLearning(nil), v.Learning...)
	for index := range v.Learning {
		v.Learning[index].SourceRefs = append([]string(nil), v.Learning[index].SourceRefs...)
		if v.Learning[index].ExpiresAt != nil {
			expires := *v.Learning[index].ExpiresAt
			v.Learning[index].ExpiresAt = &expires
		}
	}
	v.Lifecycle = append([]string(nil), v.Lifecycle...)
	return v
}

// Fix clone helper use after declaration while keeping snapshots immutable.
func (state *STRIDEProductState) candidate(id string) (STRIDEProductMarketplaceCandidate, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	v, ok := state.candidates[id]
	return cloneSTRIDEProductCandidateSafe(v), ok
}

type strideProductDestinationProposal struct {
	Recommendation *STRIDEProductDestinationRecommendation
	Audience       *STRIDEAudience
}

func (state *STRIDEProductState) suggestFromConversation(event ConversationEvent, thread scoutChatThreadRecord, message scoutChatMessageRecord) (STRIDEProductWorkRecord, bool, error) {
	return state.suggestFromConversationWithDestination(event, thread, message, strideProductDestinationProposal{})
}

func (state *STRIDEProductState) suggestFromConversationWithDestination(event ConversationEvent, thread scoutChatThreadRecord, message scoutChatMessageRecord, destination strideProductDestinationProposal) (STRIDEProductWorkRecord, bool, error) {
	if state == nil || event.Validate() != nil || event.SourceType != "channel_message" || event.EventType != "message" || message.Role != "user" || !isSTRIDEInsightsOutcomeRequest(message.Text) {
		return STRIDEProductWorkRecord{}, false, nil
	}
	author := event.AuthorPrincipal
	recipients, reviewer, err := strideProductConversationRecipients(event, author)
	if err != nil {
		return STRIDEProductWorkRecord{}, false, ErrSTRIDEProductInvalid
	}
	digest := temporalDigest(thread.ID + "\x00" + message.ID)
	id := "suggested_insights_" + digest[:20]
	now := event.IngestedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	record := STRIDEProductWorkRecord{
		ID: id, Title: "Insights & Opportunities report", Outcome: trimForStorage(message.Text, 1200), SourceThreadID: thread.ID, SourceMessageID: message.ID,
		SourceSnippet: trimForStorage(message.Text, 240), SourceEvent: referenceFromHeader(event.Header), SourceEvents: []STRIDEReference{referenceFromHeader(event.Header)}, RecipientIDs: recipients, OwnerID: author, ReviewerID: reviewer, ApprovalPolicy: newSTRIDEProductApprovalPolicy(author, reviewer, now),
		Revision: 1, Status: "suggested", ProviderExecutionFenced: true, Lifecycle: []string{"recognized_from_authorized_conversation"}, CreatedAt: now, UpdatedAt: now,
	}
	if err := applySTRIDEProductDestinationRecommendation(&record, destination.Recommendation, destination.Audience); err != nil {
		return STRIDEProductWorkRecord{}, false, ErrSTRIDEProductInvalid
	}
	if validateSTRIDEProductWork(record) != nil {
		return STRIDEProductWorkRecord{}, false, ErrSTRIDEProductInvalid
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, existing := range state.work {
		if existing.SourceThreadID == thread.ID && existing.SourceMessageID == message.ID {
			return cloneSTRIDEProductWork(existing), false, nil
		}
	}
	state.work[id] = record
	return cloneSTRIDEProductWork(record), true, nil
}

// strideProductConversationRecipients chooses a reviewer only from the exact
// source audience. This prevents an organization-level administrator (or any
// other seeded account) from becoming a recipient of a member-scoped project
// and subsequently invalidating its source merely by opening Work.
func strideProductConversationRecipients(event ConversationEvent, principal string) ([]string, string, error) {
	principal = strings.TrimSpace(principal)
	author := strings.TrimSpace(event.AuthorPrincipal)
	if event.Audience.Validate() != nil || !strideIdentifier(principal) || !strideIdentifier(author) ||
		!strideWorkContainsString(event.Audience.Principals, principal) || !strideWorkContainsString(event.Audience.Principals, author) {
		return nil, "", ErrSTRIDEProductDenied
	}

	reviewer := ""
	if author != principal {
		reviewer = author
	} else {
		preferred := strideRuntimePrincipalForEmail(artifactLibraryAdminEmail)
		if preferred != principal && strideWorkContainsString(event.Audience.Principals, preferred) {
			reviewer = preferred
		}
		if reviewer == "" {
			for _, candidate := range sortedUniqueSTRIDEIDs(event.Audience.Principals) {
				if candidate != principal {
					reviewer = candidate
					break
				}
			}
		}
	}
	if !strideIdentifier(reviewer) || reviewer == principal || !strideWorkContainsString(event.Audience.Principals, reviewer) {
		return nil, "", ErrSTRIDEProductInvalid
	}
	recipients := uniqueSortedStrings([]string{author, principal, reviewer})
	for _, recipient := range recipients {
		if !strideWorkContainsString(event.Audience.Principals, recipient) {
			return nil, "", ErrSTRIDEProductDenied
		}
	}
	return recipients, reviewer, nil
}

func isSTRIDEInsightsOutcomeRequest(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if !strings.Contains(normalized, "insights") || !strings.Contains(normalized, "opportunit") {
		return false
	}
	for _, verb := range []string{"create", "make", "build", "produce", "prepare", "need", "want"} {
		if strings.Contains(normalized, verb) {
			return true
		}
	}
	return false
}

type STRIDEProductContext struct {
	Receipt      STRIDEProductActivationReceipt
	Conversation *STRIDEConversationLedger
	Temporal     map[string]*TemporalMeetingBrain
	WorkStore    *STRIDEWorkOrchestrationStore
	Product      *STRIDEProductState
	Workforce    *STRIDEWorkforceRuntime
	Config       STRIDERuntimeConfig
	Persist      func() error
}

func (runtime *STRIDERuntime) WithProductContext(tenantID, scope string, use func(STRIDEProductContext) error) error {
	if runtime == nil || use == nil {
		return ErrSTRIDERuntimeUnavailable
	}
	var changedAgents []string
	resultErr := func() error {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		if err := runtime.requireTenantLocked(tenantID); err != nil {
			return err
		}
		now := time.Now().UTC()
		if runtime.config.Now != nil {
			now = runtime.config.Now().UTC()
		}
		receipt, err := mintSTRIDEProductActivationReceipt(runtime.config, max(runtime.generation, 1), scope, now)
		if err != nil || !verifySTRIDEProductActivationReceipt(runtime.config, receipt, scope, now) {
			return ErrSTRIDEProductDisabled
		}
		before := meetingSpecialistAuthorityDigests(runtime.domains)
		useErr := use(STRIDEProductContext{
			Receipt: receipt, Conversation: runtime.domains.conversation, Temporal: runtime.domains.temporal,
			WorkStore: runtime.domains.workStore, Product: runtime.domains.product, Workforce: runtime.domains.workforce, Config: runtime.config, Persist: runtime.saveLocked,
		})
		changedAgents = changedMeetingSpecialistAuthorityAgents(before, meetingSpecialistAuthorityDigests(runtime.domains))
		if useErr != nil {
			return useErr
		}
		return validateSTRIDERuntimeTenantState(runtime.config.TenantID, runtime.domains)
	}()
	// Dispatch only after runtime.mu is released. Specialist request/resolve
	// paths acquire the locks in the opposite direction while reauthorizing the
	// roster, so synchronous dispatch under runtime.mu would deadlock.
	if observerErr := runtime.notifyMeetingSpecialistAuthorityChanges(changedAgents); observerErr != nil {
		return errors.Join(resultErr, observerErr)
	}
	return resultErr
}

var strideProductLifecycleCheckpointHook func(string) error

func (ctx STRIDEProductContext) persistLifecycleCheckpoint(name string) error {
	if ctx.Persist == nil {
		return ErrSTRIDERuntimeUnavailable
	}
	if err := ctx.Persist(); err != nil {
		return err
	}
	if strideProductLifecycleCheckpointHook != nil {
		return strideProductLifecycleCheckpointHook(name)
	}
	return nil
}

type strideProductActivationAuthority struct {
	config  STRIDERuntimeConfig
	receipt STRIDEProductActivationReceipt
	now     time.Time
}

func (authority strideProductActivationAuthority) RegistryEnabled(tenantID, feature string, at time.Time) error {
	if feature != "stride_work_orchestration" || tenantID != authority.receipt.TenantID || !verifySTRIDEProductActivationReceipt(authority.config, authority.receipt, STRIDEProductScopeWork, at) {
		return ErrSTRIDEProductDenied
	}
	return nil
}

type strideProductApproval struct{ allowed string }

func (rights strideProductApproval) MayApprove(_ context.Context, principal string, _ STRIDESuggestedWorkCard) error {
	if principal != rights.allowed {
		return ErrSTRIDEWorkApproval
	}
	return nil
}

func (ctx STRIDEProductContext) sourceAuthority(principal string, record STRIDEProductWorkRecord) strideProductSourceAuthority {
	return strideProductSourceAuthority{
		tenantID: ctx.Config.TenantID, principal: principal, sourceThread: record.SourceThreadID,
		conversation: ctx.Conversation, temporalBrains: ctx.Temporal,
	}
}

func (ctx STRIDEProductContext) workAuthorityCurrent(principal, reason string, record STRIDEProductWorkRecord) error {
	if ctx.sourceAuthority(principal, record).SourcesCurrent(context.Background(), reason, record.SourceEvents) != nil {
		return ErrSTRIDEWorkSourceChanged
	}
	if record.MeetingAuthority != nil {
		consent := currentConsentLaneAuthority()
		if kanbanApp == nil || consent == nil {
			return ErrSTRIDEWorkSourceChanged
		}
		policy := strings.TrimSpace(consent.PolicyVersion)
		expectedPolicy := STRIDEReference{ContractType: STRIDEContractKnowledgeAssertion, ID: "consent_policy_" + temporalDigest(policy)[:16], Revision: 1, Digest: temporalDigest(policy)}
		if record.MeetingAuthority.ConsentPolicyRevision != expectedPolicy {
			return ErrSTRIDEWorkSourceChanged
		}
		authority := &appMeetingSpecialistProductAuthority{app: kanbanApp, runtime: kanbanApp.strideRuntime}
		if err := authority.ScopeCurrent(context.Background(), record.MeetingAuthority.runtimeScope()); err != nil {
			return ErrSTRIDEWorkSourceChanged
		}
	}
	return nil
}

// withWorkAuthority linearizes a meeting-derived mutation against consent
// withdrawal. ScopeCurrent protects the exact sitting/participant set; the
// consent authority lock then performs its final durable fence check and the
// mutation as one boundary.
func (ctx STRIDEProductContext) withWorkAuthority(principal, reason string, record STRIDEProductWorkRecord, commit func() error) error {
	if commit == nil || ctx.workAuthorityCurrent(principal, reason, record) != nil {
		return ErrSTRIDEWorkSourceChanged
	}
	if record.MeetingAuthority == nil {
		return commit()
	}
	fences := record.MeetingAuthority.runtimeScope().ConsentFences
	if err := currentConsentLaneAuthority().CommitWithFences(context.Background(), fences, commit); err != nil {
		return ErrSTRIDEWorkSourceChanged
	}
	return nil
}

// strideProductDestinationAllowed proves both halves of the cross-scope
// publication contract: every work recipient can open the destination, and
// every destination member is authorized for every source revision.
func strideProductDestinationAllowed(ctx STRIDEProductContext, principal string, record STRIDEProductWorkRecord, destination STRIDEAudience) error {
	if destination.Validate() != nil || len(record.RecipientIDs) == 0 {
		return ErrSTRIDEProductDenied
	}
	for _, recipient := range record.RecipientIDs {
		if !strideWorkContainsString(destination.Principals, recipient) {
			return ErrSTRIDEProductDenied
		}
		if ctx.workAuthorityCurrent(recipient, "destination_recipient", record) != nil {
			return ErrSTRIDEWorkSourceChanged
		}
	}
	if ctx.sourceAuthority(principal, record).DestinationAudienceAllowed(record.SourceEvents, destination) != nil {
		return ErrSTRIDEProductDenied
	}
	return nil
}

// reauthorizeWorkForRead preserves a visible terminal shell for recipients but
// permanently fences and purges source-derived prose as soon as any bound
// source revision is no longer current. Callers persist when changed is true.
func (ctx STRIDEProductContext) reauthorizeWorkForRead(principal, id string, now time.Time) (record STRIDEProductWorkRecord, sourceCurrent, changed bool, err error) {
	record, found := ctx.Product.workRecord(id)
	if !found {
		return STRIDEProductWorkRecord{}, false, false, ErrSTRIDEProductUnknown
	}
	if !strideWorkContainsString(record.RecipientIDs, principal) {
		return STRIDEProductWorkRecord{}, false, false, ErrSTRIDEProductDenied
	}
	if record.SourceInvalidated {
		return redactSTRIDEProductInvalidatedWork(record), false, false, nil
	}
	if sourceErr := ctx.workAuthorityCurrent(principal, "work_read", record); sourceErr != nil {
		record, err = ctx.Product.invalidateWorkSource(record.ID, record.SourceEvents, now)
		if err != nil {
			return STRIDEProductWorkRecord{}, false, false, err
		}
		return redactSTRIDEProductInvalidatedWork(record), false, true, nil
	}
	return cloneSTRIDEProductWork(record), true, false, nil
}

type strideProductExecution struct {
	issued map[string]string
	used   map[string]bool
}

func newSTRIDEProductExecution() *strideProductExecution {
	return &strideProductExecution{issued: map[string]string{}, used: map[string]bool{}}
}
func (x *strideProductExecution) issue(kind, binding string) string {
	token := temporalDigest(kind + "\x00" + binding + "\x00" + fmt.Sprint(len(x.issued)))
	x.issued[token] = kind + "\x00" + binding
	return token
}
func (x *strideProductExecution) consume(kind, binding, token string) error {
	if x.used[token] || x.issued[token] != kind+"\x00"+binding {
		return ErrSTRIDEWorkAuthority
	}
	x.used[token] = true
	return nil
}
func (x *strideProductExecution) ConsumeQueueClaim(_ context.Context, v STRIDEWorkQueueClaimAttestation) error {
	token := v.Claim.AuthorityReceipt
	v.Claim.AuthorityReceipt = ""
	return x.consume("queue", workDigest(v), token)
}
func (x *strideProductExecution) ConsumeCheckpoint(_ context.Context, v STRIDEWorkCheckpointAttestation) error {
	token := v.Checkpoint.VerifierReceipt
	v.Checkpoint.VerifierReceipt = ""
	return x.consume("checkpoint", workDigest(v), token)
}
func (x *strideProductExecution) ConsumeElevatedApproval(_ context.Context, _ STRIDEWorkElevatedEffectAttestation) error {
	return ErrSTRIDEWorkEffectApproval
}
func (x *strideProductExecution) ConsumeCompletion(_ context.Context, v STRIDEWorkCompletionAttestation) error {
	token := v.Receipt
	v.Receipt = ""
	return x.consume("complete", workDigest(v), token)
}

func (state *STRIDEProductState) workForPrincipal(principal string) ([]STRIDEProductWorkRecord, []STRIDEDurableWorkRun) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	work := []STRIDEProductWorkRecord{}
	for _, record := range state.work {
		if strideWorkContainsString(record.RecipientIDs, principal) {
			work = append(work, cloneSTRIDEProductWork(record))
		}
	}
	sort.Slice(work, func(i, j int) bool { return work[i].UpdatedAt.After(work[j].UpdatedAt) })
	return work, nil
}

func (state *STRIDEProductState) invalidateWorkSource(id string, expected []STRIDEReference, now time.Time) (STRIDEProductWorkRecord, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	record, ok := state.work[id]
	if !ok {
		return STRIDEProductWorkRecord{}, ErrSTRIDEProductUnknown
	}
	if record.SourceInvalidated {
		return cloneSTRIDEProductWork(record), nil
	}
	if !sameOrderedSTRIDEReferences(record.SourceEvents, expected) {
		return STRIDEProductWorkRecord{}, ErrSTRIDEProductConflict
	}
	return state.invalidateWorkSourceLocked(record, now)
}

func (state *STRIDEProductState) invalidateWorkSourceLocked(record STRIDEProductWorkRecord, now time.Time) (STRIDEProductWorkRecord, error) {
	if record.SourceInvalidated {
		return cloneSTRIDEProductWork(record), nil
	}
	// Purge source-derived prose, links, and completion text while retaining only
	// body-free identifiers needed to prove why this terminal record exists.
	record.Title = "Invalidated work suggestion"
	record.Outcome = "Source evidence is no longer available; this work cannot run."
	record.SourceSnippet = ""
	record.CompletionSummary = ""
	record.ArtifactHref = ""
	record.BrainHref = ""
	if record.DestinationRecommendation != nil {
		record.DestinationRecommendation = nil
		record.Lifecycle = append(record.Lifecycle, "destination_recommendation_purged")
	}
	record.Status = "failed"
	record.FailureReason = "source_invalidated"
	record.SourceInvalidated = true
	record.ProviderExecutionFenced = true
	record.Revision++
	record.UpdatedAt = now.UTC()
	if _, found := state.insights[record.ID]; found {
		delete(state.insights, record.ID)
		record.Lifecycle = append(record.Lifecycle, strideProductInsightsSnapshotPurged)
	}
	record.Lifecycle = append(record.Lifecycle, "source_authority_invalidated", "source_derived_prose_purged", "execution_permanently_fenced")
	if validateSTRIDEProductWork(record) != nil {
		return STRIDEProductWorkRecord{}, ErrSTRIDEProductInvalid
	}
	state.work[record.ID] = record
	return cloneSTRIDEProductWork(record), nil
}

func redactSTRIDEProductInvalidatedWork(record STRIDEProductWorkRecord) STRIDEProductWorkRecord {
	if !record.SourceInvalidated {
		return cloneSTRIDEProductWork(record)
	}
	record = cloneSTRIDEProductWork(record)
	record.SourceThreadID = ""
	record.SourceMessageID = ""
	record.SourceEvent = STRIDEReference{}
	record.SourceEvents = nil
	record.DestinationRecommendation = nil
	return record
}

func (state *STRIDEProductState) agentRoster() []STRIDEProductTeamAgent {
	state.mu.RLock()
	defer state.mu.RUnlock()
	result := make([]STRIDEProductTeamAgent, 0, len(state.agents))
	for _, record := range state.agents {
		result = append(result, cloneSTRIDEProductAgent(record))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].DisplayName < result[j].DisplayName })
	return result
}

func (state *STRIDEProductState) candidateCatalog() []STRIDEProductMarketplaceCandidate {
	state.mu.RLock()
	defer state.mu.RUnlock()
	result := make([]STRIDEProductMarketplaceCandidate, 0, len(state.candidates))
	for _, record := range state.candidates {
		result = append(result, cloneSTRIDEProductCandidateSafe(record))
	}
	// Keep the teammates the company already knows at the front of the shelf:
	// included Scout, first-hire Colton, then Marvin's research-method profile.
	// The remaining catalog stays stable and alphabetical.
	priority := func(id string) int {
		switch id {
		case "scout":
			return 0
		case "colton-research":
			return 1
		case "marvin-research":
			return 2
		default:
			return 3
		}
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := priority(result[i].ID), priority(result[j].ID)
		if left != right {
			return left < right
		}
		return result[i].DisplayName < result[j].DisplayName
	})
	return result
}

func (state *STRIDEProductState) workRecord(id string) (STRIDEProductWorkRecord, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	record, ok := state.work[id]
	return cloneSTRIDEProductWork(record), ok
}

func (state *STRIDEProductState) exactWorkReplay(id string, priorRevision int64, principal string, matches func(STRIDEProductWorkRecord) bool) (STRIDEProductWorkRecord, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	record, ok := state.work[id]
	if !ok || record.Revision != priorRevision+1 || !strideWorkContainsString(record.RecipientIDs, principal) || matches == nil || !matches(record) {
		return STRIDEProductWorkRecord{}, false
	}
	return cloneSTRIDEProductWork(record), true
}

func (state *STRIDEProductState) agentRecord(id string) (STRIDEProductTeamAgent, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	record, ok := state.agents[id]
	return cloneSTRIDEProductAgent(record), ok
}

func (state *STRIDEProductState) agentForDirectThread(threadID string) (STRIDEProductTeamAgent, bool) {
	threadID = strings.TrimSpace(threadID)
	if state == nil || threadID == "" {
		return STRIDEProductTeamAgent{}, false
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	for _, record := range state.agents {
		if record.DirectThreadID == threadID {
			return cloneSTRIDEProductAgent(record), true
		}
	}
	return STRIDEProductTeamAgent{}, false
}

func (runtime *STRIDERuntime) productAgentForDirectThread(threadID string) (STRIDEProductTeamAgent, bool) {
	if runtime == nil || strings.TrimSpace(threadID) == "" {
		return STRIDEProductTeamAgent{}, false
	}
	runtime.mu.Lock()
	var product *STRIDEProductState
	if runtime.domains != nil {
		product = runtime.domains.product
	}
	runtime.mu.Unlock()
	if product == nil {
		return STRIDEProductTeamAgent{}, false
	}
	return product.agentForDirectThread(threadID)
}

func (state *STRIDEProductState) agentContextProfile(id string) (STRIDEProductAgentContextProfile, bool) {
	if state == nil {
		return STRIDEProductAgentContextProfile{}, false
	}
	state.mu.RLock()
	agent, ok := state.agents[strings.TrimSpace(id)]
	state.mu.RUnlock()
	if !ok || agent.Status != "hired_fenced" || agent.AccessRevoked || validateSTRIDEProductAgent(agent) != nil {
		return STRIDEProductAgentContextProfile{}, false
	}
	profile := STRIDEProductAgentContextProfile{
		AgentID: agent.ID, ListingID: agent.ListingID, Revision: agent.Revision, DisplayName: agent.DisplayName, RoleTitle: agent.RoleTitle,
		Category: agent.Category, OutcomeSummary: agent.OutcomeSummary, PersonalitySummary: agent.PersonalitySummary, VoiceSummary: agent.VoiceSummary,
		WorkingStyle: agent.WorkingStyle, PersonalityTraits: append([]string(nil), agent.PersonalityTraits...), PersonalityNotes: agent.Config.PersonalityNotes,
		Capabilities: append([]string(nil), agent.Capabilities...), Memberships: append([]string(nil), agent.Config.Memberships...), MemoryPolicy: agent.MemoryPolicy,
		CoreMemories: append([]STRIDEProductAgentCoreMemory(nil), agent.CoreMemories...), ProviderExecutionFenced: agent.ProviderExecutionFenced,
	}
	for _, learning := range agent.Learning {
		if (learning.Status == "reviewed" || learning.Status == "corrected") && (learning.ExpiresAt == nil || learning.ExpiresAt.After(time.Now().UTC())) {
			profile.ActiveLearning = append(profile.ActiveLearning, learning)
		}
	}
	digest, err := STRIDEContractDigest(profile)
	if err != nil {
		return STRIDEProductAgentContextProfile{}, false
	}
	profile.Digest = digest
	return profile, true
}

func (app *kanbanBoardApp) strideAgentDirectThreadContext(threadID string) (STRIDEProductAgentContextProfile, bool) {
	threadID = strings.TrimSpace(threadID)
	if app == nil || app.strideRuntime == nil || !strings.HasPrefix(threadID, strideProductAgentDirectThreadPrefix) {
		return STRIDEProductAgentContextProfile{}, false
	}
	var profile STRIDEProductAgentContextProfile
	found := false
	err := app.strideRuntime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		agent, ok := ctx.Product.agentForDirectThread(threadID)
		if !ok {
			return nil
		}
		profile, found = ctx.Product.agentContextProfile(agent.ID)
		return nil
	})
	return profile, err == nil && found
}

func (app *kanbanBoardApp) stridePreferredResearchAgentContext() (STRIDEProductAgentContextProfile, bool) {
	if app == nil || app.strideRuntime == nil {
		return STRIDEProductAgentContextProfile{}, false
	}
	var profile STRIDEProductAgentContextProfile
	found := false
	err := app.strideRuntime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		for _, listingID := range []string{"colton-research", "marvin-research"} {
			candidate, ok := ctx.Product.agentContextProfile(candidateAgentID(listingID))
			if ok && containsSTRIDEID(candidate.Capabilities, "research_brief") {
				profile, found = candidate, true
				break
			}
		}
		return nil
	})
	return profile, err == nil && found
}

// strideMentionableAgentProfiles returns only durable, currently hired seats
// whose signed product records still validate. It deliberately does not widen
// execution authority: these profiles power chat discovery, while a concrete
// channel and requested capability are checked separately before proposal
// minting and again at confirmation/runner admission.
func (app *kanbanBoardApp) strideMentionableAgentProfiles() []STRIDEProductAgentContextProfile {
	if app == nil || app.strideRuntime == nil {
		return nil
	}
	profiles := []STRIDEProductAgentContextProfile{}
	err := app.strideRuntime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		for _, agent := range ctx.Product.agentRoster() {
			profile, ok := ctx.Product.agentContextProfile(agent.ID)
			if ok {
				profiles = append(profiles, profile)
			}
		}
		return nil
	})
	if err != nil {
		return nil
	}
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].DisplayName != profiles[j].DisplayName {
			return profiles[i].DisplayName < profiles[j].DisplayName
		}
		return profiles[i].AgentID < profiles[j].AgentID
	})
	return profiles
}

func strideAgentProfileAllowsChatThread(profile STRIDEProductAgentContextProfile, thread scoutChatThreadRecord) bool {
	if profile.AgentID == "" || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
		return false
	}
	for _, membership := range profile.Memberships {
		membership = strings.TrimSpace(membership)
		if thread.Table && membership == "team" {
			return true
		}
		if membership == strings.TrimSpace(thread.ID) {
			return true
		}
	}
	return false
}

// strideAgentContextForChatWork is the confirmation-time ACL/capability gate
// for a targeted chat proposal. Research uses the same bounded Scout research
// runner as direct hired-coworker threads; the coworker's own provider remains
// fenced and the runner reauthorizes the profile again before provider use.
func (app *kanbanBoardApp) strideAgentContextForChatWork(agentID string, thread scoutChatThreadRecord, mode string) (STRIDEProductAgentContextProfile, bool) {
	agentID = strings.TrimSpace(agentID)
	if app == nil || app.strideRuntime == nil || agentID == "" {
		return STRIDEProductAgentContextProfile{}, false
	}
	var profile STRIDEProductAgentContextProfile
	found := false
	err := app.strideRuntime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		profile, found = ctx.Product.agentContextProfile(agentID)
		return nil
	})
	if err != nil || !found || !strideAgentProfileAllowsChatThread(profile, thread) {
		return STRIDEProductAgentContextProfile{}, false
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "research":
		if !containsSTRIDEID(profile.Capabilities, "deep_research") {
			return STRIDEProductAgentContextProfile{}, false
		}
	case "design":
		if !containsSTRIDEID(profile.Capabilities, "design_brief") && !containsSTRIDEID(profile.Capabilities, "interaction_critique") {
			return STRIDEProductAgentContextProfile{}, false
		}
	default:
		return STRIDEProductAgentContextProfile{}, false
	}
	return profile, true
}

// strideAgentDirectThreadProviderFenced keeps a hired coworker's private
// thread durable and human-writable while its model seat remains unqualified.
// Reserved coworker thread IDs fail closed when the signed product runtime or
// matching roster record is unavailable; E10 must explicitly admit an active,
// unfenced seat before the ordinary conversational path may invoke a provider.
func (app *kanbanBoardApp) strideAgentDirectThreadProviderFenced(threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	if !strings.HasPrefix(threadID, strideProductAgentDirectThreadPrefix) {
		return false
	}
	if app == nil || app.strideRuntime == nil {
		return true
	}
	providerAllowed := false
	err := app.strideRuntime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		agent, ok := ctx.Product.agentForDirectThread(threadID)
		providerAllowed = ok && !agent.ProviderExecutionFenced && !agent.AccessRevoked && oneOf(agent.Status, "active", "hired")
		return nil
	})
	return err != nil || !providerAllowed
}

func (state *STRIDEProductState) restoreAgentRecord(record STRIDEProductTeamAgent) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.agents[record.ID] = cloneSTRIDEProductAgent(record)
}

func (state *STRIDEProductState) exactAgentReplay(id string, priorRevision int64, matches func(STRIDEProductTeamAgent) bool) (STRIDEProductTeamAgent, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	agent, ok := state.agents[id]
	if !ok || agent.Revision != priorRevision+1 || matches == nil || !matches(agent) {
		return STRIDEProductTeamAgent{}, false
	}
	return cloneSTRIDEProductAgent(agent), true
}

func (state *STRIDEProductState) reviseWork(id string, revision int64, principal string, mutate func(*STRIDEProductWorkRecord) error, now time.Time) (STRIDEProductWorkRecord, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	record, ok := state.work[id]
	if !ok {
		return STRIDEProductWorkRecord{}, ErrSTRIDEProductUnknown
	}
	if record.Revision != revision || record.Status != "suggested" || principal != record.OwnerID {
		return STRIDEProductWorkRecord{}, ErrSTRIDEProductConflict
	}
	if err := mutate(&record); err != nil {
		return STRIDEProductWorkRecord{}, err
	}
	record.Revision++
	record.UpdatedAt = now.UTC()
	if validateSTRIDEProductWork(record) != nil {
		return STRIDEProductWorkRecord{}, ErrSTRIDEProductInvalid
	}
	state.work[id] = record
	return cloneSTRIDEProductWork(record), nil
}

func (state *STRIDEProductState) createSuggestion(event ConversationEvent, thread scoutChatThreadRecord, message scoutChatMessageRecord, title, outcome, principal string, now time.Time) (STRIDEProductWorkRecord, error) {
	return state.createSuggestionWithDestination(event, thread, message, title, outcome, principal, now, strideProductDestinationProposal{})
}

func (state *STRIDEProductState) createSuggestionWithDestination(event ConversationEvent, thread scoutChatThreadRecord, message scoutChatMessageRecord, title, outcome, principal string, now time.Time, destination strideProductDestinationProposal) (STRIDEProductWorkRecord, error) {
	if event.Validate() != nil || event.SourceType != "channel_message" || event.EventType != "message" || event.ThreadID != thread.ID || event.SourceID != message.ID || event.AuthorPrincipal == "" || principal == "" {
		return STRIDEProductWorkRecord{}, ErrSTRIDEProductDenied
	}
	recipients, reviewer, err := strideProductConversationRecipients(event, principal)
	if err != nil {
		return STRIDEProductWorkRecord{}, err
	}
	digest := temporalDigest(thread.ID + "\x00" + message.ID)
	source := referenceFromHeader(event.Header)
	record := STRIDEProductWorkRecord{ID: "suggested_insights_" + digest[:20], Title: trimForStorage(title, 120), Outcome: trimForStorage(outcome, 1200), SourceThreadID: thread.ID, SourceMessageID: message.ID,
		SourceSnippet: trimForStorage(message.Text, 240), SourceEvent: source, SourceEvents: []STRIDEReference{source}, RecipientIDs: recipients, OwnerID: principal, ReviewerID: reviewer, ApprovalPolicy: newSTRIDEProductApprovalPolicy(principal, reviewer, now), Revision: 1, Status: "suggested", ProviderExecutionFenced: true,
		Lifecycle: []string{"human_requested_from_authorized_conversation"}, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	if err := applySTRIDEProductDestinationRecommendation(&record, destination.Recommendation, destination.Audience); err != nil {
		return STRIDEProductWorkRecord{}, ErrSTRIDEProductInvalid
	}
	if validateSTRIDEProductWork(record) != nil {
		return STRIDEProductWorkRecord{}, ErrSTRIDEProductInvalid
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, prior := range state.work {
		if prior.SourceThreadID == thread.ID && prior.SourceMessageID == message.ID {
			return cloneSTRIDEProductWork(prior), nil
		}
	}
	state.work[record.ID] = record
	return cloneSTRIDEProductWork(record), nil
}

func (state *STRIDEProductState) createMeetingSuggestion(result STRIDETemporalRecallResult, principal string, recipients []string, now time.Time, scopes ...meetingSpecialistProductScope) (STRIDEProductWorkRecord, error) {
	return state.createMeetingSuggestionWithDestination(result, principal, recipients, now, strideProductDestinationProposal{}, scopes...)
}

func (state *STRIDEProductState) createMeetingSuggestionWithDestination(result STRIDETemporalRecallResult, principal string, recipients []string, now time.Time, destination strideProductDestinationProposal, scopes ...meetingSpecialistProductScope) (STRIDEProductWorkRecord, error) {
	recipients = uniqueSortedStrings(recipients)
	if !strideIdentifier(result.RoomID) || !strideIdentifier(result.SittingID) || !strideIdentifier(principal) || len(recipients) < 2 || !strideWorkContainsString(recipients, principal) || len(result.Evidence) == 0 || strings.TrimSpace(result.Text) == "" || !isSTRIDEInsightsOutcomeRequest(result.Text) || len(scopes) > 1 {
		return STRIDEProductWorkRecord{}, ErrSTRIDEProductInvalid
	}
	sources, err := canonicalSTRIDEProductSourceEvents(result.Evidence)
	if err != nil {
		return STRIDEProductWorkRecord{}, ErrSTRIDEProductInvalid
	}
	source := sources[0]
	sourceSetDigest := workDigest(sources)
	reviewer := ""
	for _, candidate := range recipients {
		if candidate != principal {
			reviewer = candidate
			break
		}
	}
	var meetingAuthority *STRIDEProductMeetingAuthority
	if len(scopes) == 1 {
		scope := scopes[0]
		if scope.RoomID != result.RoomID || scope.SittingID != result.SittingID {
			return STRIDEProductWorkRecord{}, ErrSTRIDEProductInvalid
		}
		value := durableSTRIDEProductMeetingAuthority(scope)
		meetingAuthority = &value
	}
	id := "suggested_meeting_insights_" + temporalDigest(result.RoomID + "\x00" + result.SittingID + "\x00" + result.EvidenceDigest + "\x00" + sourceSetDigest)[:20]
	record := STRIDEProductWorkRecord{
		ID: id, Title: "Insights & Opportunities report", Outcome: trimForStorage(result.Text, 1200),
		SourceThreadID: result.RoomID, SourceMessageID: source.ID, SourceSnippet: trimForStorage(result.Text, 240), SourceEvent: source, SourceEvents: sources,
		// Recipients are the exact current member set for which the temporal
		// authority independently produced the identical evidence digest. Never
		// widen this to an organization admin who was not in the meeting.
		RecipientIDs: recipients, OwnerID: principal, ReviewerID: reviewer, ApprovalPolicy: newSTRIDEProductApprovalPolicy(principal, reviewer, now), MeetingAuthority: meetingAuthority, Revision: 1, Status: "suggested", ProviderExecutionFenced: true,
		Lifecycle: []string{"recognized_from_consent_authorized_meeting"}, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
	if err := applySTRIDEProductDestinationRecommendation(&record, destination.Recommendation, destination.Audience); err != nil {
		return STRIDEProductWorkRecord{}, ErrSTRIDEProductInvalid
	}
	if validateSTRIDEProductWork(record) != nil {
		return STRIDEProductWorkRecord{}, ErrSTRIDEProductInvalid
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, prior := range state.work {
		if prior.ID == id && prior.SourceThreadID == result.RoomID && sameOrderedSTRIDEReferences(prior.SourceEvents, sources) {
			if sameSTRIDEProductStringSet(prior.RecipientIDs, record.RecipientIDs) && sameSTRIDEProductMeetingAuthority(prior.MeetingAuthority, record.MeetingAuthority) {
				return cloneSTRIDEProductWork(prior), nil
			}
			return STRIDEProductWorkRecord{}, ErrSTRIDEWorkSourceChanged
		}
	}
	state.work[id] = record
	return cloneSTRIDEProductWork(record), nil
}

func sameSTRIDEProductStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		counts[value]--
		if counts[value] < 0 {
			return false
		}
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

func sameSTRIDEProductMeetingAuthority(left, right *STRIDEProductMeetingAuthority) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	l, r := *left, *right
	l.ConsentFences = append([]STRIDEProductConsentFence(nil), left.ConsentFences...)
	r.ConsentFences = append([]STRIDEProductConsentFence(nil), right.ConsentFences...)
	for index := range l.ConsentFences {
		l.ConsentFences[index].IssuedAt = time.Time{}
	}
	for index := range r.ConsentFences {
		r.ConsentFences[index].IssuedAt = time.Time{}
	}
	return workDigest(l) == workDigest(r)
}

func (state *STRIDEProductState) beginTrial(candidateID, owner string, now time.Time) (STRIDEProductTeamAgent, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	candidate, ok := state.candidates[candidateID]
	// Scout is the included front door. Its listing is inspectable beside every
	// other coworker, but it cannot be duplicated through the hire funnel.
	if !ok || candidate.ID == "scout" || candidate.LiveAvailable || candidate.Availability != "internal_preview" || !candidate.ProviderExecutionFenced || !strideIdentifier(owner) {
		return STRIDEProductTeamAgent{}, ErrSTRIDEProductDenied
	}
	id := candidateAgentID(candidate.ID)
	if prior, exists := state.agents[id]; exists {
		return cloneSTRIDEProductAgent(prior), nil
	}
	proactivity := firstNonEmptyString(candidate.DefaultProactivity, "disabled")
	agent := STRIDEProductTeamAgent{
		ID: candidateAgentID(candidate.ID), ListingID: candidate.ID, DisplayName: candidate.DisplayName, Category: candidate.Category,
		RoleTitle: candidate.RoleTitle, OutcomeSummary: candidate.OutcomeSummary, PersonalitySummary: candidate.PersonalitySummary,
		VoiceSummary: candidate.VoiceSummary, WorkingStyle: candidate.WorkingStyle, PersonalityTraits: append([]string(nil), candidate.PersonalityTraits...),
		Capabilities: append([]string(nil), candidate.Capabilities...), MemoryPolicy: candidate.MemoryPolicy, CoreMemories: append([]STRIDEProductAgentCoreMemory(nil), candidate.CoreMemories...),
		Status: "trial", OwnerID: owner, Revision: 1,
		Config: STRIDEProductAgentConfig{PersonalityNotes: candidate.DefaultPersonalityNotes, Memberships: []string{"team"}, PerRunBudgetCents: 0, DailyBudgetCents: 0, Proactivity: proactivity}, ProviderExecutionFenced: true, AccessRevoked: true,
		Lifecycle: []string{"sample_trial_started", "identity_profile_seeded", "provider_runtime_remains_fenced"}, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
	if validateSTRIDEProductAgent(agent) != nil {
		return STRIDEProductTeamAgent{}, ErrSTRIDEProductInvalid
	}
	state.agents[id] = agent
	return cloneSTRIDEProductAgent(agent), nil
}

func candidateAgentID(candidateID string) string {
	return "agent_" + strings.TrimSpace(candidateID)
}

func (state *STRIDEProductState) mutateAgent(id string, revision int64, mutate func(*STRIDEProductTeamAgent) error, now time.Time) (STRIDEProductTeamAgent, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	agent, ok := state.agents[id]
	if !ok {
		return STRIDEProductTeamAgent{}, ErrSTRIDEProductUnknown
	}
	if agent.Revision != revision {
		return STRIDEProductTeamAgent{}, ErrSTRIDEProductConflict
	}
	if err := mutate(&agent); err != nil {
		return STRIDEProductTeamAgent{}, err
	}
	agent.Revision++
	agent.UpdatedAt = now.UTC()
	if validateSTRIDEProductAgent(agent) != nil {
		return STRIDEProductTeamAgent{}, ErrSTRIDEProductInvalid
	}
	state.agents[id] = agent
	return cloneSTRIDEProductAgent(agent), nil
}

func normalizeSTRIDEProductConfig(config STRIDEProductAgentConfig) (STRIDEProductAgentConfig, error) {
	config.PersonalityNotes = trimForStorage(config.PersonalityNotes, 600)
	config.Memberships = uniqueSortedStrings(config.Memberships)
	if len(config.Memberships) == 0 {
		config.Memberships = []string{"team"}
	}
	if config.PerRunBudgetCents < 0 || config.PerRunBudgetCents > 100_000 || config.DailyBudgetCents < 0 || config.DailyBudgetCents > 1_000_000 || !oneOf(config.Proactivity, "disabled", "quiet") {
		return STRIDEProductAgentConfig{}, ErrSTRIDEProductInvalid
	}
	for _, membership := range config.Memberships {
		if !strideIdentifier(membership) {
			return STRIDEProductAgentConfig{}, ErrSTRIDEProductInvalid
		}
	}
	return config, nil
}

func (state *STRIDEProductState) proposeAgentUpdate(id string, revision int64, summary string, candidate STRIDEProductAgentConfig, now time.Time) (STRIDEProductTeamAgent, error) {
	candidate, err := normalizeSTRIDEProductConfig(candidate)
	if err != nil {
		return STRIDEProductTeamAgent{}, err
	}
	updateID := "update_" + temporalDigest(id + "\x00" + fmt.Sprint(revision) + "\x00" + summary)[:20]
	if replay, ok := state.exactAgentReplay(id, revision, func(agent STRIDEProductTeamAgent) bool {
		for _, update := range agent.Updates {
			if update.ID == updateID && update.Summary == trimForStorage(summary, 300) && workDigest(update.Candidate) == workDigest(candidate) {
				return true
			}
		}
		return false
	}); ok {
		return replay, nil
	}
	return state.mutateAgent(id, revision, func(agent *STRIDEProductTeamAgent) error {
		if agent.Status == "offboarded" {
			return ErrSTRIDEProductDenied
		}
		semanticDiff, diffErr := newSTRIDEProductAgentSemanticDiff(agent.Config, candidate)
		if diffErr != nil {
			return diffErr
		}
		update := STRIDEProductAgentUpdate{ID: updateID, Revision: 1, Status: "pending", Summary: trimForStorage(summary, 300), Previous: agent.Config, Candidate: candidate, SemanticDiff: semanticDiff, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
		agent.Updates = append(agent.Updates, update)
		agent.Lifecycle = append(agent.Lifecycle, "update_diff_proposed")
		return nil
	}, now)
}

func (state *STRIDEProductState) resolveAgentUpdate(id string, revision int64, updateID, action string, now time.Time) (STRIDEProductTeamAgent, error) {
	if replay, ok := state.exactAgentReplay(id, revision, func(agent STRIDEProductTeamAgent) bool {
		for _, update := range agent.Updates {
			if update.ID == updateID && ((action == "approve" && update.Status == "approved") || (action == "rollback" && update.Status == "rolled_back")) {
				return true
			}
		}
		return false
	}); ok {
		return replay, nil
	}
	return state.mutateAgent(id, revision, func(agent *STRIDEProductTeamAgent) error {
		for index := range agent.Updates {
			update := &agent.Updates[index]
			if update.ID != updateID {
				continue
			}
			switch action {
			case "approve":
				if update.Status != "pending" {
					return ErrSTRIDEProductConflict
				}
				update.Status = "approved"
				agent.Config = update.Candidate
				agent.Lifecycle = append(agent.Lifecycle, "update_approved_provider_still_fenced")
			case "rollback":
				if !oneOf(update.Status, "approved", "pending") {
					return ErrSTRIDEProductConflict
				}
				update.Status = "rolled_back"
				agent.Config = update.Previous
				agent.Lifecycle = append(agent.Lifecycle, "update_rolled_back")
			default:
				return ErrSTRIDEProductInvalid
			}
			update.Revision++
			update.UpdatedAt = now.UTC()
			return nil
		}
		return ErrSTRIDEProductUnknown
	}, now)
}

// proposeAgentLearningFromWork turns a successful, attributable run into a
// visible memory candidate. It never enters provider context until a human
// approves or corrects it, and the run/artifact/thread/source lineage stays on
// the record for later reauthorization and forgetting.
func (state *STRIDEProductState) proposeAgentLearningFromWork(id, subject, scope, summary, runID, artifactID, sourceThreadID string, sourceRefs []string, confidence float64, expiresAt *time.Time, now time.Time) (STRIDEProductTeamAgent, bool, error) {
	id, subject, scope = strings.TrimSpace(id), strings.TrimSpace(subject), strings.TrimSpace(scope)
	runID, artifactID, sourceThreadID = strings.TrimSpace(runID), strings.TrimSpace(artifactID), strings.TrimSpace(sourceThreadID)
	summary = trimForStorage(summary, 600)
	refs := uniqueSortedStrings(sourceRefs)
	if !strideIdentifier(id) || !strideIdentifier(subject) || !strideIdentifier(scope) || summary == "" || !strideIdentifier(runID) || !strideIdentifier(artifactID) || !strideIdentifier(sourceThreadID) || len(refs) == 0 || confidence <= 0 || confidence > 1 || sensitiveWorkforceLearning(subject) || sensitiveWorkforceLearning(scope) {
		return STRIDEProductTeamAgent{}, false, ErrSTRIDEProductInvalid
	}
	learningID := "learning_" + temporalDigest(id + "\x00" + runID + "\x00" + artifactID)[:20]

	state.mu.Lock()
	defer state.mu.Unlock()
	agent, ok := state.agents[id]
	if !ok {
		return STRIDEProductTeamAgent{}, false, ErrSTRIDEProductUnknown
	}
	if agent.Status != "hired_fenced" || agent.AccessRevoked {
		return STRIDEProductTeamAgent{}, false, ErrSTRIDEProductDenied
	}
	for _, learning := range agent.Learning {
		if learning.ID == learningID {
			return cloneSTRIDEProductAgent(agent), false, nil
		}
	}
	var expiry *time.Time
	if expiresAt != nil {
		value := expiresAt.UTC()
		expiry = &value
	}
	learning := STRIDEProductAgentLearning{
		ID: learningID, Subject: subject, Scope: scope, Summary: summary, Status: "pending", Origin: "completed_work",
		RunID: runID, ArtifactID: artifactID, SourceThreadID: sourceThreadID, SourceRefs: refs, Confidence: confidence, ExpiresAt: expiry,
		Revision: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
	agent.Learning = append(agent.Learning, learning)
	agent.Lifecycle = append(agent.Lifecycle, "completed_work_learning_proposed_for_review")
	agent.Revision++
	agent.UpdatedAt = now.UTC()
	if validateSTRIDEProductAgent(agent) != nil {
		return STRIDEProductTeamAgent{}, false, ErrSTRIDEProductInvalid
	}
	state.agents[id] = agent
	return cloneSTRIDEProductAgent(agent), true, nil
}

func (state *STRIDEProductState) recordAgentLearning(id string, revision int64, subject, scope, summary string, now time.Time) (STRIDEProductTeamAgent, error) {
	subject = strings.TrimSpace(subject)
	scope = strings.TrimSpace(scope)
	summary = trimForStorage(summary, 600)
	if !strideIdentifier(subject) || !strideIdentifier(scope) || summary == "" || sensitiveWorkforceLearning(subject) || sensitiveWorkforceLearning(scope) {
		return STRIDEProductTeamAgent{}, ErrSTRIDEProductInvalid
	}
	learningID := "learning_" + temporalDigest(id + "\x00" + fmt.Sprint(revision) + "\x00" + subject + "\x00" + scope)[:20]
	if replay, ok := state.exactAgentReplay(id, revision, func(agent STRIDEProductTeamAgent) bool {
		for _, learning := range agent.Learning {
			if learning.ID == learningID && learning.Subject == subject && learning.Scope == scope && learning.Summary == summary {
				return true
			}
		}
		return false
	}); ok {
		return replay, nil
	}
	return state.mutateAgent(id, revision, func(agent *STRIDEProductTeamAgent) error {
		if agent.Status == "offboarded" {
			return ErrSTRIDEProductDenied
		}
		learning := STRIDEProductAgentLearning{
			ID:         learningID,
			Subject:    subject,
			Scope:      scope,
			Summary:    summary,
			Status:     "reviewed",
			Origin:     "human_reviewed",
			Confidence: 1,
			Revision:   1,
			CreatedAt:  now.UTC(),
			UpdatedAt:  now.UTC(),
		}
		agent.Learning = append(agent.Learning, learning)
		agent.Lifecycle = append(agent.Lifecycle, "human_reviewed_learning_recorded")
		return nil
	}, now)
}

func (state *STRIDEProductState) resolveAgentLearning(id string, revision int64, learningID, action, summary string, now time.Time) (STRIDEProductTeamAgent, error) {
	expectedSummary := trimForStorage(summary, 600)
	if action == "forget" {
		expectedSummary = "Forgotten by human request."
	}
	if replay, ok := state.exactAgentReplay(id, revision, func(agent STRIDEProductTeamAgent) bool {
		for _, learning := range agent.Learning {
			statusMatches := (action == "approve" && learning.Status == "reviewed") || (action == "correct" && learning.Status == "corrected") || (action == "forget" && learning.Status == "forgotten")
			if learning.ID == learningID && statusMatches && (action == "approve" || learning.Summary == expectedSummary) {
				return true
			}
		}
		return false
	}); ok {
		return replay, nil
	}
	return state.mutateAgent(id, revision, func(agent *STRIDEProductTeamAgent) error {
		for index := range agent.Learning {
			learning := &agent.Learning[index]
			if learning.ID != learningID {
				continue
			}
			switch action {
			case "approve":
				if learning.Status != "pending" {
					return ErrSTRIDEProductConflict
				}
				learning.Status = "reviewed"
				agent.Lifecycle = append(agent.Lifecycle, "work_learning_approved_by_human")
			case "correct":
				if learning.Status == "forgotten" || strings.TrimSpace(summary) == "" {
					return ErrSTRIDEProductConflict
				}
				learning.Summary = trimForStorage(summary, 600)
				learning.Status = "corrected"
				agent.Lifecycle = append(agent.Lifecycle, "learning_corrected_by_human")
			case "forget":
				if learning.Status == "forgotten" {
					return ErrSTRIDEProductConflict
				}
				learning.Summary = "Forgotten by human request."
				learning.Status = "forgotten"
				agent.Lifecycle = append(agent.Lifecycle, "learning_forgotten_and_excluded")
			default:
				return ErrSTRIDEProductInvalid
			}
			learning.Revision++
			learning.UpdatedAt = now.UTC()
			return nil
		}
		return ErrSTRIDEProductUnknown
	}, now)
}

type strideProductInsightsStages struct {
	source       STRIDEProductWorkRecord
	revisionNote string
}

func (executor strideProductInsightsStages) ExecuteStrideInsightsStage(stage StrideInsightsStageManifest, round int, request StrideInsightsRequest, _ *StrideInsightsReport) (StrideInsightsStageResult, error) {
	result := StrideInsightsStageResult{Digest: temporalDigest("local:" + stage.StageID + ":" + fmt.Sprint(round) + ":" + request.RequestDigest), Synthetic: true}
	evidenceIDs := make([]string, 0, len(request.Evidence.Sources))
	for _, source := range request.Evidence.Sources {
		evidenceIDs = append(evidenceIDs, source.EvidenceID)
	}
	sort.Strings(evidenceIDs)
	if stage.StageID == "writer" {
		summary := "A bounded, evidence-linked opportunity was produced from the approved conversation. Provider-quality expansion remains fenced until E10."
		nextAction := "Review the attached report and decide whether to run the provider-qualified version."
		if note := trimForStorage(strings.TrimSpace(executor.revisionNote), 1200); note != "" {
			summary = "This immutable revision incorporates explicit human feedback while preserving the approved source evidence. Provider-quality expansion remains fenced until E10."
			nextAction = "Review this revision against the human feedback: " + note
		}
		result.Report = &StrideInsightsReport{ReportID: "report_local_" + request.RunID, Summary: summary,
			Claims:        []StrideInsightsClaim{{ClaimID: "claim_requested_outcome", Statement: executor.source.Outcome, EvidenceIDs: evidenceIDs, Confidence: .98, Impact: "Creates a decision-ready next step from an explicit team outcome.", NextAction: nextAction, Owner: request.PrincipalID, DecisionStatus: insightsDecisionProposed}},
			Opportunities: []StrideInsightsOpportunity{{OpportunityID: "opportunity_approved_followthrough", Title: executor.source.Title, ClaimIDs: []string{"claim_requested_outcome"}, Impact: "Preserves the work outcome and ownership in the company operating record.", NextAction: "Review and refine the bounded artifact.", Owner: request.PrincipalID, DecisionStatus: insightsDecisionProposed}}}
	}
	if stage.StageID == "criterion_claim_critic" {
		result.Verdict = &StrideInsightsCriticVerdict{VerdictID: "verdict_local_" + fmt.Sprint(round), Outcome: insightsCriticAccept, Findings: []StrideInsightsCriticFinding{{Criterion: "grounding", ClaimID: "claim_requested_outcome", Verdict: insightsCriticAccept, EvidenceIDs: evidenceIDs}}}
	}
	return result, nil
}

func (ctx STRIDEProductContext) approveAndRunWork(principal, id string, revision int64, now time.Time) (STRIDEProductWorkRecord, error) {
	if !verifySTRIDEProductActivationReceipt(ctx.Config, ctx.Receipt, STRIDEProductScopeWork, now) {
		return STRIDEProductWorkRecord{}, ErrSTRIDEProductDisabled
	}
	record, ok := ctx.Product.workRecord(id)
	if !ok {
		return STRIDEProductWorkRecord{}, ErrSTRIDEProductUnknown
	}
	if record.SourceInvalidated && strideWorkContainsString(record.RecipientIDs, principal) {
		return STRIDEProductWorkRecord{}, ErrSTRIDEWorkSourceChanged
	}
	if record.Status == "completed" && record.Revision == revision {
		if !strideWorkContainsString(record.ApprovalPolicy.EligiblePrincipals, principal) || record.DestinationAudience == nil || record.DestinationACLVersion < 1 {
			return STRIDEProductWorkRecord{}, ErrSTRIDEProductConflict
		}
		if ctx.workAuthorityCurrent(principal, "completed_work_replay", record) != nil {
			_, _ = ctx.Product.invalidateWorkSource(record.ID, record.SourceEvents, now)
			return STRIDEProductWorkRecord{}, ErrSTRIDEWorkSourceChanged
		}
		if err := strideProductDestinationAllowed(ctx, principal, record, *record.DestinationAudience); err != nil {
			return STRIDEProductWorkRecord{}, err
		}
		return cloneSTRIDEProductWork(record), nil
	}
	if record.Status != "suggested" || record.Revision != revision || !strideWorkContainsString(record.ApprovalPolicy.EligiblePrincipals, principal) || !strideIdentifier(record.DestinationThreadID) || record.DestinationAudience == nil || record.DestinationACLVersion < 1 {
		return STRIDEProductWorkRecord{}, ErrSTRIDEProductConflict
	}
	audience := cloneAudience(*record.DestinationAudience)
	execution := newSTRIDEProductExecution()
	sourceRefs := append([]STRIDEReference(nil), record.SourceEvents...)
	sourceAuthority := ctx.sourceAuthority(principal, record)
	if ctx.workAuthorityCurrent(principal, "run_snapshot", record) != nil {
		_, _ = ctx.Product.invalidateWorkSource(record.ID, record.SourceEvents, now)
		return STRIDEProductWorkRecord{}, ErrSTRIDEWorkSourceChanged
	}
	snapshot, err := sourceAuthority.RetrievalSnapshot(record.Outcome, sourceRefs, record.CreatedAt)
	if err != nil {
		_, _ = ctx.Product.invalidateWorkSource(record.ID, record.SourceEvents, now)
		return STRIDEProductWorkRecord{}, ErrSTRIDEWorkSourceChanged
	}
	if err := strideProductDestinationAllowed(ctx, principal, record, audience); err != nil {
		return STRIDEProductWorkRecord{}, err
	}
	destination := STRIDEThreadResolution{Status: STRIDEThreadReuse, ThreadID: record.DestinationThreadID, Audience: audience, ACLVersion: record.DestinationACLVersion}
	service := STRIDEWorkOrchestrator{Enabled: true, TenantID: ctx.Config.TenantID, Store: ctx.WorkStore, Activation: strideProductActivationAuthority{ctx.Config, ctx.Receipt, now}, Execution: execution,
		Sources: sourceAuthority, ApprovalRights: strideProductApproval{allowed: principal}, Now: func() time.Time { return now.UTC() }}
	intentID := "intent_" + temporalDigest(record.ID)[:20]
	intentEvidence := make([]STRIDEWorkEvidence, 0, len(sourceRefs))
	for _, ref := range sourceRefs {
		intentEvidence = append(intentEvidence, STRIDEWorkEvidence{Ref: ref, AdmissionClass: STRIDEWorkEvidenceAuthorizedProjection, Current: true, People: record.RecipientIDs, Confidence: .98})
	}
	intent, err := service.AdmitIntent(context.Background(), STRIDEWorkIntentCandidate{ID: intentID, OutcomeDigest: temporalDigest(record.Outcome), CreatedAt: now.UTC(), RequestedPeople: []string{principal}, Evidence: intentEvidence})
	if err != nil {
		if errors.Is(err, ErrSTRIDEWorkSourceChanged) {
			_, _ = ctx.Product.invalidateWorkSource(record.ID, record.SourceEvents, now)
		}
		return STRIDEProductWorkRecord{}, err
	}
	reviewer := record.ReviewerID
	card, exists := strideProductStoredCard(ctx.WorkStore, record.ID)
	if !exists {
		if !record.ApprovalPolicy.ExpiresAt.After(now.UTC()) {
			return STRIDEProductWorkRecord{}, ErrSTRIDEProductConflict
		}
		card, err = service.ProposeSuggestedWork(STRIDESuggestedWorkCardSpec{ID: record.ID, IntentID: intent.ID, Destination: destination, Owner: record.OwnerID, Reviewer: reviewer, Authority: STRIDEWorkAuthorityInternalWrite,
			Budget: STRIDEWorkBudget{MaxCostMicros: 1, MaxDuration: 5 * time.Minute, MaxAttempts: 1}, DueAt: record.CreatedAt.Add(24 * time.Hour), ExpiresAt: record.CreatedAt.Add(12 * time.Hour),
			ApprovalPolicy: STRIDESuggestedWorkApprovalPolicy{EligiblePrincipals: append([]string(nil), record.ApprovalPolicy.EligiblePrincipals...), Quorum: record.ApprovalPolicy.Quorum, ExpiresAt: record.ApprovalPolicy.ExpiresAt}})
		if err != nil {
			return STRIDEProductWorkRecord{}, err
		}
	} else if card.Owner != record.OwnerID || card.Reviewer != reviewer || card.IntentID != intent.ID || !sameThreadResolution(card.Destination, destination) || workDigest(card.ApprovalPolicy) != workDigest(STRIDESuggestedWorkApprovalPolicy{EligiblePrincipals: record.ApprovalPolicy.EligiblePrincipals, Quorum: record.ApprovalPolicy.Quorum, ExpiresAt: record.ApprovalPolicy.ExpiresAt}) {
		return STRIDEProductWorkRecord{}, ErrSTRIDEWorkState
	}
	var run *STRIDEDurableWorkRun
	if card.Status == "approved" && card.ConsumedRunID != "" {
		candidate, found := strideProductStoredRun(ctx.WorkStore, card.ConsumedRunID)
		if !found {
			return STRIDEProductWorkRecord{}, ErrSTRIDEWorkState
		}
		run = &candidate
	} else {
		if !record.ApprovalPolicy.ExpiresAt.After(now.UTC()) {
			return STRIDEProductWorkRecord{}, ErrSTRIDEProductConflict
		}
		err = ctx.withWorkAuthority(principal, "approval_consumption", record, func() error {
			_, candidate, approveErr := service.ApproveSuggestedWork(context.Background(), card.ID, card.Revision, principal)
			run = candidate
			return approveErr
		})
	}
	if err != nil || run == nil {
		return STRIDEProductWorkRecord{}, firstNonNilError(err, ErrSTRIDEWorkState)
	}
	if err = ctx.persistLifecycleCheckpoint("approval_consumed"); err != nil {
		return STRIDEProductWorkRecord{}, err
	}
	capability := STRIDEReference{ContractType: STRIDEContractAgentCapabilityManifest, ID: "insights_local_capability", Revision: 1, Digest: temporalDigest("insights_local_capability_v1")}
	route := STRIDEWorkRouteSnapshot{StageID: "insights_opportunities_v1", RouteID: "deterministic_local", RouteRevision: 1, RouteDigest: temporalDigest("deterministic_local_zero_token"), CapabilityRefs: []STRIDEReference{capability}, Authority: STRIDEWorkAuthorityInternalWrite, MaxCostMicros: 1, MaxDuration: 5 * time.Minute}
	currentRun, _ := strideProductStoredRun(ctx.WorkStore, run.ID)
	if _, routed := currentRun.RouteSnapshots[route.StageID]; !routed {
		err = ctx.withWorkAuthority(principal, "route_binding", record, func() error {
			var routeErr error
			currentRun, routeErr = service.SetStageRoute(run.ID, STRIDERunQueued, route)
			return routeErr
		})
		if err != nil {
			return STRIDEProductWorkRecord{}, err
		}
		if err = ctx.persistLifecycleCheckpoint("route_bound"); err != nil {
			return STRIDEProductWorkRecord{}, err
		}
	}
	if currentRun.Status == STRIDERunQueued {
		err = ctx.withWorkAuthority(principal, "run_start", record, func() error {
			var transitionErr error
			currentRun, transitionErr = service.TransitionRun(run.ID, STRIDERunRunning, route.StageID, "")
			return transitionErr
		})
		if err != nil {
			return STRIDEProductWorkRecord{}, err
		}
	}
	claimedRun := currentRun
	if currentRun.Status == STRIDERunRunning {
		if currentRun.QueueClaim == nil || !currentRun.QueueClaim.LeaseExpiresAt.After(now.UTC()) {
			generation := int64(1)
			if currentRun.QueueClaim != nil {
				generation = currentRun.QueueClaim.ClaimGeneration + 1
			}
			claim := STRIDEWorkQueueClaim{JobID: "job_" + run.ID, StageID: route.StageID, ClaimGeneration: generation, FencingTokenDigest: temporalDigest("fence:" + run.ID + ":" + fmt.Sprint(generation)), LeaseExpiresAt: now.Add(4 * time.Minute)}
			claimBinding := STRIDEWorkQueueClaimAttestation{TenantID: ctx.Config.TenantID, RunID: run.ID, Stage: route, Claim: claim}
			claim.AuthorityReceipt = execution.issue("queue", workDigest(claimBinding))
			err = ctx.withWorkAuthority(principal, "queue_claim", record, func() error {
				var claimErr error
				claimedRun, claimErr = service.BindQueueClaim(run.ID, claim)
				return claimErr
			})
			if err != nil {
				return STRIDEProductWorkRecord{}, err
			}
		}
		if err = ctx.persistLifecycleCheckpoint("stable_run_claimed"); err != nil {
			return STRIDEProductWorkRecord{}, err
		}
	}

	evidence, err := NewStrideInsightsEvidenceSnapshot(snapshot)
	if err != nil {
		return STRIDEProductWorkRecord{}, err
	}
	workRef := STRIDEReference{ContractType: STRIDEContractWorkRun, ID: run.ID, Revision: 1, Digest: temporalDigest(run.IdempotencyDigest)}
	identity, err := NewStrideInsightsAnalystIdentity(ctx.Config.TenantID, record.OwnerID, capability, STRIDEReference{ContractType: STRIDEContractAgentCapabilityManifest, ID: "insights_local_runtime", Revision: 1, Digest: temporalDigest("insights_local_runtime_fenced")}, record.CreatedAt)
	if err != nil {
		return STRIDEProductWorkRecord{}, err
	}
	request := StrideInsightsRequest{Schema: StrideInsightsRequestSchema, WorkflowVersion: StrideInsightsWorkflowVersion, RequestID: "request_" + run.ID, RequestRevision: 1, RunID: run.ID, TenantID: ctx.Config.TenantID, PrincipalID: record.OwnerID, Goal: record.Outcome,
		InternalDestination: "workspace:" + record.DestinationThreadID, Binding: identity.Binding(workRef), Evidence: evidence, ManifestDigest: FixedStrideInsightsWorkflowManifest().ManifestDigest}
	request.RequestDigest, err = strideInsightsRequestDigest(request)
	if err != nil {
		return STRIDEProductWorkRecord{}, err
	}
	workflow := NewStrideInsightsWorkflow(func() time.Time { return record.CreatedAt.UTC() })
	var insightsRun StrideInsightsRun
	err = ctx.withWorkAuthority(principal, "executor_launch", record, func() error {
		var launchErr error
		insightsRun, launchErr = workflow.Launch(ACLPrincipal{TenantID: ctx.Config.TenantID, Kind: ACLPrincipalUser, ID: record.OwnerID}, request, strideProductInsightsStages{source: record})
		return launchErr
	})
	if err != nil || insightsRun.Status != StrideInsightsStatusAccepted || insightsRun.Artifact == nil || insightsRun.Outcome == nil {
		return STRIDEProductWorkRecord{}, firstNonNilError(err, ErrStrideInsightsInvalid)
	}

	checkpoint := STRIDEWorkCheckpoint{ID: "checkpoint_" + run.ID, StageID: route.StageID, Status: "passed", EvidenceDigest: insightsRun.Outcome.OutcomeDigest, CreatedAt: record.CreatedAt}
	checkpointRun, _ := strideProductStoredRun(ctx.WorkStore, run.ID)
	if !hasSTRIDEProductCheckpoint(checkpointRun, checkpoint) {
		checkpointBinding := STRIDEWorkCheckpointAttestation{TenantID: ctx.Config.TenantID, RunID: run.ID, Stage: route, Claim: *claimedRun.QueueClaim, Checkpoint: checkpoint}
		checkpoint.VerifierReceipt = execution.issue("checkpoint", workDigest(checkpointBinding))
		err = ctx.withWorkAuthority(principal, "checkpoint_commit", record, func() error {
			var checkpointErr error
			checkpointRun, checkpointErr = service.AddCheckpoint(run.ID, checkpoint)
			return checkpointErr
		})
		if err != nil {
			return STRIDEProductWorkRecord{}, err
		}
		if err = ctx.persistLifecycleCheckpoint("lifecycle_checkpoint_saved"); err != nil {
			return STRIDEProductWorkRecord{}, err
		}
	}
	artifactID := "artifact_" + run.ID
	artifactRef := STRIDEReference{ContractType: STRIDEContractOutcome, ID: insightsRun.Artifact.ArtifactID, Revision: 1, Digest: insightsRun.Artifact.ArtifactDigest}
	artifactBinding := STRIDEWorkArtifactBinding{ID: artifactID, RunID: run.ID, StageID: route.StageID, Artifact: artifactRef, Evidence: sourceRefs, Destination: destination, Audience: audience, CreatedAt: record.CreatedAt}
	if existing, ok := strideProductStoredArtifact(ctx.WorkStore, artifactID); !ok {
		err = ctx.withWorkAuthority(principal, "artifact_commit", record, func() error { _, artifactErr := service.RecordArtifact(artifactBinding); return artifactErr })
		if err != nil {
			return STRIDEProductWorkRecord{}, err
		}
		if err = ctx.persistLifecycleCheckpoint("artifact_saved"); err != nil {
			return STRIDEProductWorkRecord{}, err
		}
	} else if workDigest(existing) != workDigest(artifactBinding) {
		return STRIDEProductWorkRecord{}, ErrSTRIDEWorkState
	}
	currentRun, _ = strideProductStoredRun(ctx.WorkStore, run.ID)
	if currentRun.Status != STRIDERunCompleted {
		completionBinding := STRIDEWorkCompletionAttestation{TenantID: ctx.Config.TenantID, RunID: run.ID, Stage: route, Claim: *checkpointRun.QueueClaim, Checkpoints: checkpointRun.Checkpoints}
		completionReceipt := execution.issue("complete", workDigest(completionBinding))
		err = ctx.withWorkAuthority(principal, "run_completion", record, func() error {
			_, completionErr := service.CompleteRun(context.Background(), run.ID, completionReceipt)
			return completionErr
		})
		if err != nil {
			return STRIDEProductWorkRecord{}, err
		}
		if err = ctx.persistLifecycleCheckpoint("run_completed"); err != nil {
			return STRIDEProductWorkRecord{}, err
		}
	}
	outcomeBinding := STRIDEWorkOutcomeBinding{ID: "outcome_" + run.ID, RunID: run.ID, Verdict: "accepted", ArtifactIDs: []string{artifactID}, Evidence: sourceRefs, Destination: destination, Audience: audience, Reviewer: reviewer, CompletedAt: record.CreatedAt}
	if existing, ok := strideProductStoredOutcome(ctx.WorkStore, outcomeBinding.ID); !ok {
		err = ctx.withWorkAuthority(principal, "outcome_commit", record, func() error { _, outcomeErr := service.RecordOutcome(outcomeBinding); return outcomeErr })
		if err != nil {
			return STRIDEProductWorkRecord{}, err
		}
		if err = ctx.persistLifecycleCheckpoint("outcome_saved"); err != nil {
			return STRIDEProductWorkRecord{}, err
		}
	} else if workDigest(existing) != workDigest(outcomeBinding) {
		return STRIDEProductWorkRecord{}, ErrSTRIDEWorkState
	}

	report := insightsRun.Reports[len(insightsRun.Reports)-1]
	workflowPayload, snapshotErr := workflow.Snapshot()
	if snapshotErr != nil {
		return STRIDEProductWorkRecord{}, snapshotErr
	}
	insightsState := STRIDEProductInsightsState{TenantID: ctx.Config.TenantID, WorkID: record.ID, Revision: 1, WorkflowPayload: workflowPayload, WorkflowDigest: temporalDigestBytes(workflowPayload), CurrentRunID: insightsRun.RunID, CurrentReportDigest: report.ReportDigest, UpdatedAt: now.UTC()}
	record.Status, record.RunID, record.ArtifactID = "completed", run.ID, insightsRun.Artifact.ArtifactID
	record.ArtifactHref = strideRuntimeAPIBase + "work/runs/" + run.ID + "/artifact"
	record.BrainHref = strideRuntimeAPIBase + "work/suggestions/" + record.ID + "/evidence"
	record.CompletionSummary = report.Summary
	record.Lifecycle = append(record.Lifecycle, "human_approved_revision_"+fmt.Sprint(revision), "work_run_queued", "deterministic_executor_completed", "critic_verified", "artifact_linked_to_company_brain", strideProductInsightsSnapshotMarker)
	record.UpdatedAt = now.UTC()
	if validateSTRIDEProductInsightsState(record, insightsState) != nil {
		return STRIDEProductWorkRecord{}, ErrSTRIDEProductInvalid
	}
	err = ctx.withWorkAuthority(principal, "product_completion", record, func() error {
		ctx.Product.mu.Lock()
		defer ctx.Product.mu.Unlock()
		current, stillCurrent := ctx.Product.work[id]
		if !stillCurrent || current.Revision != record.Revision || current.Status != "suggested" {
			return ErrSTRIDEProductConflict
		}
		ctx.Product.work[id] = record
		ctx.Product.insights[id] = cloneSTRIDEProductInsightsState(insightsState)
		return nil
	})
	if err != nil {
		return STRIDEProductWorkRecord{}, err
	}
	if err = ctx.persistLifecycleCheckpoint("product_completion_saved"); err != nil {
		return STRIDEProductWorkRecord{}, err
	}
	return cloneSTRIDEProductWork(record), nil
}

func strideProductStoredCard(store *STRIDEWorkOrchestrationStore, id string) (STRIDESuggestedWorkCard, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.Cards[id]
	return value, ok
}
func strideProductStoredRun(store *STRIDEWorkOrchestrationStore, id string) (STRIDEDurableWorkRun, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.Runs[id]
	return value, ok
}
func strideProductStoredArtifact(store *STRIDEWorkOrchestrationStore, id string) (STRIDEWorkArtifactBinding, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.Artifacts[id]
	return value, ok
}
func strideProductStoredOutcome(store *STRIDEWorkOrchestrationStore, id string) (STRIDEWorkOutcomeBinding, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.Outcomes[id]
	return value, ok
}
func hasSTRIDEProductCheckpoint(run STRIDEDurableWorkRun, want STRIDEWorkCheckpoint) bool {
	for _, value := range run.Checkpoints {
		if value.ID == want.ID && value.StageID == want.StageID && value.Status == want.Status && value.EvidenceDigest == want.EvidenceDigest && value.CreatedAt.Equal(want.CreatedAt) {
			return true
		}
	}
	return false
}

func firstNonNilError(err error, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}
