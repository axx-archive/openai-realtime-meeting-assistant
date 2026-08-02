package main

// Token-free E6 Suggested Work and durable orchestration foundations. This
// package deliberately exposes no detector model, provider, HTTP route, queue
// launch, or production adapter. All mutation crosses one serializable store
// boundary so a future PostgreSQL repository can preserve these invariants.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrSTRIDEWorkDisabled        = errors.New("STRIDE work orchestration is disabled")
	ErrSTRIDEWorkAdmissionDenied = errors.New("work intent admission denied")
	ErrSTRIDEWorkState           = errors.New("work orchestration state transition denied")
	ErrSTRIDEWorkApproval        = errors.New("suggested work approval denied")
	ErrSTRIDEWorkSourceChanged   = errors.New("work evidence is no longer current")
	ErrSTRIDEWorkDestination     = errors.New("work destination requires an explicit choice")
	ErrSTRIDEWorkEffectApproval  = errors.New("elevated effect approval required")
	ErrSTRIDEWorkAuthority       = errors.New("work orchestration authority attestation denied")
	ErrSTRIDEDelegationDenied    = errors.New("agent delegation denied")
	ErrSTRIDEWorkSnapshotInvalid = errors.New("work orchestration snapshot is invalid")
)

const (
	STRIDEWorkEvidenceAuthorizedProjection = "authorized_projection"
	STRIDEThreadReuse                      = "reuse"
	STRIDEThreadExplicitChoice             = "explicit_choice"
	STRIDEThreadCreateRequired             = "create_required"

	STRIDEWorkAuthorityReadOnly      = "read_only"
	STRIDEWorkAuthorityInternalWrite = "internal_write"
	STRIDEWorkAuthorityExternalWrite = "external_write"
	STRIDEWorkAuthorityProduction    = "production"
	STRIDEWorkAuthorityDestructive   = "destructive"

	STRIDERunQueued         = "queued"
	STRIDERunRunning        = "running"
	STRIDERunAwaitingInput  = "awaiting_input"
	STRIDERunAwaitingReview = "awaiting_review"
	STRIDERunBlocked        = "blocked"
	STRIDERunCompleted      = "completed"
	STRIDERunFailed         = "failed"
	STRIDERunCancelled      = "cancelled"
)

type STRIDEWorkEvidence struct {
	Ref              STRIDEReference   `json:"ref"`
	AdmissionClass   string            `json:"admissionClass"`
	AttributedSource *STRIDEReference  `json:"attributedSource,omitempty"`
	Current          bool              `json:"current"`
	People           []string          `json:"people"`
	Projects         []string          `json:"projects,omitempty"`
	Confidence       float64           `json:"confidence"`
	Counterevidence  []STRIDEReference `json:"counterevidence,omitempty"`
}

type STRIDEWorkIntentCandidate struct {
	ID              string               `json:"id"`
	OutcomeDigest   string               `json:"outcomeDigest"`
	Evidence        []STRIDEWorkEvidence `json:"evidence"`
	RequestedPeople []string             `json:"requestedPeople,omitempty"`
	CreatedAt       time.Time            `json:"createdAt"`
}

type STRIDEAdmittedWorkIntent struct {
	ID              string            `json:"id"`
	TenantID        string            `json:"tenantId"`
	OutcomeDigest   string            `json:"outcomeDigest"`
	Evidence        []STRIDEReference `json:"evidence"`
	Counterevidence []STRIDEReference `json:"counterevidence,omitempty"`
	RelevantPeople  []string          `json:"relevantPeople"`
	Projects        []string          `json:"projects,omitempty"`
	Confidence      float64           `json:"confidence"`
	DedupeDigest    string            `json:"dedupeDigest"`
	CreatedAt       time.Time         `json:"createdAt"`
	Status          string            `json:"status"`
}

type STRIDEProjectThreadCandidate struct {
	ThreadID       string         `json:"threadId"`
	ProjectIDs     []string       `json:"projectIds"`
	ParticipantIDs []string       `json:"participantIds"`
	Authorized     bool           `json:"authorized"`
	Archived       bool           `json:"archived"`
	Relevant       bool           `json:"relevant"`
	Audience       STRIDEAudience `json:"audience"`
	ACLVersion     int64          `json:"aclVersion"`
}

type STRIDEThreadResolution struct {
	Status     string                         `json:"status"`
	ThreadID   string                         `json:"threadId,omitempty"`
	Candidates []STRIDEProjectThreadCandidate `json:"candidates,omitempty"`
	Audience   STRIDEAudience                 `json:"audience,omitempty"`
	ACLVersion int64                          `json:"aclVersion,omitempty"`
}

func ResolveSTRIDEProjectThread(projectIDs, people []string, candidates []STRIDEProjectThreadCandidate) STRIDEThreadResolution {
	projects := stringSet(projectIDs)
	relevantPeople := stringSet(people)
	matches := make([]STRIDEProjectThreadCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.ThreadID = strings.TrimSpace(candidate.ThreadID)
		lowerID := strings.ToLower(candidate.ThreadID)
		if !strideIdentifier(candidate.ThreadID) || !candidate.Authorized || candidate.Archived || !candidate.Relevant ||
			lowerID == "general" || lowerID == "#general" || lowerID == "public" || candidate.ACLVersion < 1 || candidate.Audience.Validate() != nil {
			continue
		}
		if len(projects) > 0 && !setsIntersect(projects, stringSet(candidate.ProjectIDs)) {
			continue
		}
		// A work destination is eligible only when every relevant human can
		// open it. A single overlapping participant is not sufficient authority
		// to recommend a project thread, even though the final approval boundary
		// independently re-checks the canonical ACL before starting a run.
		if len(relevantPeople) > 0 && !setContainsAll(stringSet(candidate.ParticipantIDs), relevantPeople) {
			continue
		}
		matches = append(matches, candidate)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ThreadID < matches[j].ThreadID })
	switch len(matches) {
	case 0:
		return STRIDEThreadResolution{Status: STRIDEThreadCreateRequired}
	case 1:
		return STRIDEThreadResolution{Status: STRIDEThreadReuse, ThreadID: matches[0].ThreadID, Audience: matches[0].Audience, ACLVersion: matches[0].ACLVersion}
	default:
		return STRIDEThreadResolution{Status: STRIDEThreadExplicitChoice, Candidates: matches}
	}
}

type STRIDEWorkBudget struct {
	MaxCostMicros int64         `json:"maxCostMicros"`
	MaxDuration   time.Duration `json:"maxDuration"`
	MaxAttempts   int           `json:"maxAttempts"`
}

type STRIDESuggestedWorkApprovalPolicy struct {
	EligiblePrincipals []string  `json:"eligiblePrincipals"`
	Quorum             int       `json:"quorum"`
	ExpiresAt          time.Time `json:"expiresAt"`
}

type STRIDEWorkEndorsement struct {
	Principal string    `json:"principal"`
	At        time.Time `json:"at"`
}

type STRIDESuggestedWorkCard struct {
	ID               string                            `json:"id"`
	TenantID         string                            `json:"tenantId"`
	Revision         int64                             `json:"revision"`
	IntentID         string                            `json:"intentId"`
	Evidence         []STRIDEReference                 `json:"evidence"`
	Counterevidence  []STRIDEReference                 `json:"counterevidence,omitempty"`
	Destination      STRIDEThreadResolution            `json:"destination"`
	Owner            string                            `json:"owner"`
	Reviewer         string                            `json:"reviewer"`
	Authority        string                            `json:"authority"`
	Budget           STRIDEWorkBudget                  `json:"budget"`
	DueAt            time.Time                         `json:"dueAt"`
	ExpiresAt        time.Time                         `json:"expiresAt"`
	ApprovalPolicy   STRIDESuggestedWorkApprovalPolicy `json:"approvalPolicy"`
	Endorsements     []STRIDEWorkEndorsement           `json:"endorsements,omitempty"`
	Status           string                            `json:"status"`
	DismissReason    string                            `json:"dismissReason,omitempty"`
	CreatedAt        time.Time                         `json:"createdAt"`
	UpdatedAt        time.Time                         `json:"updatedAt"`
	ConsumedRunID    string                            `json:"consumedRunId,omitempty"`
	ParentRunID      string                            `json:"parentRunId,omitempty"`
	ParentFeedbackID string                            `json:"parentFeedbackId,omitempty"`
}

type STRIDEWorkRouteSnapshot struct {
	StageID        string            `json:"stageId"`
	RouteID        string            `json:"routeId"`
	RouteRevision  int64             `json:"routeRevision"`
	RouteDigest    string            `json:"routeDigest"`
	CapabilityRefs []STRIDEReference `json:"capabilityRefs"`
	Authority      string            `json:"authority"`
	MaxCostMicros  int64             `json:"maxCostMicros"`
	MaxDuration    time.Duration     `json:"maxDuration"`
}

type STRIDEWorkCheckpoint struct {
	ID                    string    `json:"id"`
	StageID               string    `json:"stageId"`
	Status                string    `json:"status"`
	EvidenceDigest        string    `json:"evidenceDigest"`
	CreatedAt             time.Time `json:"createdAt"`
	VerifierReceipt       string    `json:"-"`
	VerifierReceiptDigest string    `json:"verifierReceiptDigest"`
}

// STRIDEWorkQueueClaim is the durable hand-off to the existing leased queue.
// Only the digest of the fencing token is retained; the bearer token itself
// remains in the queue worker's short-lived claim context.
type STRIDEWorkQueueClaim struct {
	JobID                  string    `json:"jobId"`
	StageID                string    `json:"stageId"`
	ClaimGeneration        int64     `json:"claimGeneration"`
	FencingTokenDigest     string    `json:"fencingTokenDigest"`
	LeaseExpiresAt         time.Time `json:"leaseExpiresAt"`
	AuthorityReceipt       string    `json:"-"`
	AuthorityReceiptDigest string    `json:"authorityReceiptDigest"`
}

type STRIDEElevatedEffectApproval struct {
	ID                     string    `json:"id"`
	Revision               int64     `json:"revision"`
	RunID                  string    `json:"runId"`
	StageID                string    `json:"stageId"`
	Authority              string    `json:"authority"`
	BindingDigest          string    `json:"bindingDigest"`
	ExpiresAt              time.Time `json:"expiresAt"`
	Current                bool      `json:"current"`
	Consumed               bool      `json:"consumed"`
	TenantID               string    `json:"tenantId"`
	AuthorityReceipt       string    `json:"-"`
	AuthorityReceiptDigest string    `json:"authorityReceiptDigest"`
}

type STRIDEDurableWorkRun struct {
	ID                      string                             `json:"id"`
	TenantID                string                             `json:"tenantId"`
	IdempotencyDigest       string                             `json:"idempotencyDigest"`
	CardID                  string                             `json:"cardId"`
	CardRevision            int64                              `json:"cardRevision"`
	Evidence                []STRIDEReference                  `json:"evidence"`
	Destination             STRIDEThreadResolution             `json:"destination"`
	Owner                   string                             `json:"owner"`
	Reviewer                string                             `json:"reviewer"`
	Authority               string                             `json:"authority"`
	Budget                  STRIDEWorkBudget                   `json:"budget"`
	Status                  string                             `json:"status"`
	CurrentStage            string                             `json:"currentStage,omitempty"`
	Attempts                int                                `json:"attempts"`
	RouteSnapshots          map[string]STRIDEWorkRouteSnapshot `json:"routeSnapshots"`
	Checkpoints             []STRIDEWorkCheckpoint             `json:"checkpoints,omitempty"`
	QueueClaim              *STRIDEWorkQueueClaim              `json:"queueClaim,omitempty"`
	BlockReason             string                             `json:"blockReason,omitempty"`
	InvalidatedSources      []STRIDEReference                  `json:"invalidatedSources,omitempty"`
	CreatedAt               time.Time                          `json:"createdAt"`
	UpdatedAt               time.Time                          `json:"updatedAt"`
	CompletedAt             time.Time                          `json:"completedAt,omitempty"`
	CompletionReceiptDigest string                             `json:"completionReceiptDigest,omitempty"`
	ParentRunID             string                             `json:"parentRunId,omitempty"`
	ParentFeedbackID        string                             `json:"parentFeedbackId,omitempty"`
}

type STRIDEWorkArtifactBinding struct {
	ID          string                 `json:"id"`
	RunID       string                 `json:"runId"`
	StageID     string                 `json:"stageId"`
	Artifact    STRIDEReference        `json:"artifact"`
	Evidence    []STRIDEReference      `json:"evidence"`
	Destination STRIDEThreadResolution `json:"destination"`
	Audience    STRIDEAudience         `json:"audience"`
	CreatedAt   time.Time              `json:"createdAt"`
}

type STRIDEWorkOutcomeBinding struct {
	ID          string                 `json:"id"`
	RunID       string                 `json:"runId"`
	Verdict     string                 `json:"verdict"`
	ArtifactIDs []string               `json:"artifactIds"`
	Evidence    []STRIDEReference      `json:"evidence"`
	Destination STRIDEThreadResolution `json:"destination"`
	Audience    STRIDEAudience         `json:"audience"`
	Reviewer    string                 `json:"reviewer"`
	CompletedAt time.Time              `json:"completedAt"`
}

type STRIDEWorkFeedback struct {
	ID         string    `json:"id"`
	RunID      string    `json:"runId"`
	Kind       string    `json:"kind"`
	Author     string    `json:"author"`
	BodyDigest string    `json:"bodyDigest"`
	CreatedAt  time.Time `json:"createdAt"`
	Rerun      bool      `json:"rerun"`
}

type STRIDEAgentDelegationGrant struct {
	ID                 string          `json:"id"`
	RunID              string          `json:"runId"`
	StageID            string          `json:"stageId"`
	TeamAgent          STRIDEReference `json:"teamAgent"`
	CapabilityManifest STRIDEReference `json:"capabilityManifest"`
	Assignment         STRIDEReference `json:"assignment"`
	RuntimePrincipal   string          `json:"runtimePrincipal"`
	RuntimeTokenDigest string          `json:"runtimeTokenDigest"`
	AllowedTools       []string        `json:"allowedTools"`
	MaxDuration        time.Duration   `json:"maxDuration"`
	MaxCostMicros      int64           `json:"maxCostMicros"`
	MaxDelegationHops  int             `json:"maxDelegationHops"`
	Trigger            string          `json:"trigger"`
	IssuedAt           time.Time       `json:"issuedAt"`
	ExpiresAt          time.Time       `json:"expiresAt"`
	Revoked            bool            `json:"revoked"`
}

type STRIDEDelegationToken struct {
	Grant STRIDEAgentDelegationGrant `json:"grant"`
	Token string                     `json:"token"`
}

type STRIDEWorkSourceAuthority interface {
	SourcesCurrent(context.Context, string, []STRIDEReference) error
}

type STRIDEWorkApprovalRights interface {
	MayApprove(context.Context, string, STRIDESuggestedWorkCard) error
}

type STRIDEWorkAgentAuthority interface {
	ReferencesCurrent(context.Context, STRIDEReference, STRIDEReference, STRIDEReference) error
}

// STRIDEWorkActivationAuthority is the server-owned registry boundary. The
// local Enabled boolean is only a kill switch; it can never activate work by
// itself.
type STRIDEWorkActivationAuthority interface {
	RegistryEnabled(tenantID, feature string, at time.Time) error
}

type STRIDEWorkQueueClaimAttestation struct {
	TenantID string
	RunID    string
	Stage    STRIDEWorkRouteSnapshot
	Claim    STRIDEWorkQueueClaim
}

type STRIDEWorkCheckpointAttestation struct {
	TenantID   string
	RunID      string
	Stage      STRIDEWorkRouteSnapshot
	Claim      STRIDEWorkQueueClaim
	Checkpoint STRIDEWorkCheckpoint
}

type STRIDEWorkElevatedEffectAttestation struct {
	TenantID string
	RunID    string
	Stage    STRIDEWorkRouteSnapshot
	Approval STRIDEElevatedEffectApproval
}

type STRIDEWorkCompletionAttestation struct {
	TenantID    string
	RunID       string
	Stage       STRIDEWorkRouteSnapshot
	Claim       STRIDEWorkQueueClaim
	Checkpoints []STRIDEWorkCheckpoint
	Receipt     string
}

// STRIDEWorkExecutionAuthority validates one-use attestations against the
// authoritative queue, approval and verifier ledgers. Implementations must
// reject forged, replayed, revoked, stale and cross-tenant receipts.
type STRIDEWorkExecutionAuthority interface {
	ConsumeQueueClaim(context.Context, STRIDEWorkQueueClaimAttestation) error
	ConsumeCheckpoint(context.Context, STRIDEWorkCheckpointAttestation) error
	ConsumeElevatedApproval(context.Context, STRIDEWorkElevatedEffectAttestation) error
	ConsumeCompletion(context.Context, STRIDEWorkCompletionAttestation) error
}

type STRIDEWorkOrchestrationStore struct {
	mu              sync.Mutex
	Version         int                                     `json:"version"`
	Intents         map[string]STRIDEAdmittedWorkIntent     `json:"intents"`
	IntentDedupe    map[string]string                       `json:"intentDedupe"`
	Cards           map[string]STRIDESuggestedWorkCard      `json:"cards"`
	Runs            map[string]STRIDEDurableWorkRun         `json:"runs"`
	Artifacts       map[string]STRIDEWorkArtifactBinding    `json:"artifacts"`
	Outcomes        map[string]STRIDEWorkOutcomeBinding     `json:"outcomes"`
	Feedback        map[string]STRIDEWorkFeedback           `json:"feedback"`
	EffectApprovals map[string]STRIDEElevatedEffectApproval `json:"effectApprovals"`
	Delegations     map[string]STRIDEAgentDelegationGrant   `json:"delegations"`
}

func NewSTRIDEWorkOrchestrationStore() *STRIDEWorkOrchestrationStore {
	return &STRIDEWorkOrchestrationStore{
		Version: 1, Intents: map[string]STRIDEAdmittedWorkIntent{}, IntentDedupe: map[string]string{},
		Cards: map[string]STRIDESuggestedWorkCard{}, Runs: map[string]STRIDEDurableWorkRun{},
		Artifacts: map[string]STRIDEWorkArtifactBinding{}, Outcomes: map[string]STRIDEWorkOutcomeBinding{},
		Feedback: map[string]STRIDEWorkFeedback{}, EffectApprovals: map[string]STRIDEElevatedEffectApproval{}, Delegations: map[string]STRIDEAgentDelegationGrant{},
	}
}

type STRIDEWorkOrchestrator struct {
	Enabled        bool
	TenantID       string
	Store          *STRIDEWorkOrchestrationStore
	Activation     STRIDEWorkActivationAuthority
	Execution      STRIDEWorkExecutionAuthority
	Sources        STRIDEWorkSourceAuthority
	ApprovalRights STRIDEWorkApprovalRights
	Agents         STRIDEWorkAgentAuthority
	MinConfidence  float64
	Now            func() time.Time
	Random         func([]byte) error
}

func (service STRIDEWorkOrchestrator) now() time.Time {
	if service.Now != nil {
		return service.Now().UTC()
	}
	return time.Now().UTC()
}

func (service STRIDEWorkOrchestrator) requireEnabled() error {
	if !service.Enabled || !strideIdentifier(service.TenantID) || service.Activation == nil {
		return ErrSTRIDEWorkDisabled
	}
	if err := service.Activation.RegistryEnabled(service.TenantID, "stride_work_orchestration", service.now()); err != nil {
		return ErrSTRIDEWorkDisabled
	}
	if service.Store == nil {
		return ErrSTRIDEWorkState
	}
	return nil
}

func (service STRIDEWorkOrchestrator) AdmitIntent(ctx context.Context, candidate STRIDEWorkIntentCandidate) (STRIDEAdmittedWorkIntent, error) {
	if err := service.requireEnabled(); err != nil {
		return STRIDEAdmittedWorkIntent{}, err
	}
	if service.Sources == nil || !strideIdentifier(strings.TrimSpace(candidate.ID)) || !isHexDigest(candidate.OutcomeDigest) || candidate.CreatedAt.IsZero() || len(candidate.Evidence) == 0 {
		return STRIDEAdmittedWorkIntent{}, ErrSTRIDEWorkAdmissionDenied
	}
	refs := make([]STRIDEReference, 0, len(candidate.Evidence))
	counter := []STRIDEReference{}
	peopleScores := map[string]float64{}
	projects := map[string]struct{}{}
	totalConfidence := 0.0
	for _, evidence := range candidate.Evidence {
		if evidence.AdmissionClass != STRIDEWorkEvidenceAuthorizedProjection || !evidence.Current || evidence.Ref.Validate() != nil ||
			!allowedWorkProjectionContract(evidence.Ref.ContractType) || evidence.Confidence < 0 || evidence.Confidence > 1 {
			return STRIDEAdmittedWorkIntent{}, ErrSTRIDEWorkAdmissionDenied
		}
		// Specialist contributions are admissible only after an ordinary,
		// authorized projection becomes the source ref. The contribution remains
		// attribution, never launch authority in its own right.
		if evidence.Ref.ContractType == STRIDEContractMeetingAgentContribution {
			return STRIDEAdmittedWorkIntent{}, ErrSTRIDEWorkAdmissionDenied
		}
		if evidence.AttributedSource != nil && evidence.AttributedSource.Validate() != nil {
			return STRIDEAdmittedWorkIntent{}, ErrSTRIDEWorkAdmissionDenied
		}
		refs = append(refs, evidence.Ref)
		counter = append(counter, evidence.Counterevidence...)
		totalConfidence += evidence.Confidence
		for _, person := range evidence.People {
			person = strings.TrimSpace(person)
			if strideIdentifier(person) && !strings.HasPrefix(strings.ToLower(person), "agent-") {
				peopleScores[person] += evidence.Confidence
			}
		}
		for _, project := range evidence.Projects {
			if strideIdentifier(strings.TrimSpace(project)) {
				projects[strings.TrimSpace(project)] = struct{}{}
			}
		}
	}
	rawRefCount, rawCounterCount := len(refs), len(counter)
	refs = uniqueSortedSTRIDEReferences(refs)
	counter = uniqueSortedSTRIDEReferences(counter)
	if len(refs) == 0 || (rawRefCount > 0 && len(refs) == 0) ||
		(rawCounterCount > 0 && len(counter) == 0) || !optionalReferencesValid(counter) {
		return STRIDEAdmittedWorkIntent{}, ErrSTRIDEWorkAdmissionDenied
	}
	if err := service.Sources.SourcesCurrent(ctx, "intent_admission", append(append([]STRIDEReference{}, refs...), counter...)); err != nil {
		return STRIDEAdmittedWorkIntent{}, ErrSTRIDEWorkSourceChanged
	}
	confidence := totalConfidence/float64(len(candidate.Evidence)) - 0.05*float64(len(counter))
	minimum := service.MinConfidence
	if minimum <= 0 {
		minimum = 0.70
	}
	if confidence < minimum || confidence > 1 {
		return STRIDEAdmittedWorkIntent{}, ErrSTRIDEWorkAdmissionDenied
	}
	relevant := []string{}
	for person, score := range peopleScores {
		if score >= minimum {
			relevant = append(relevant, person)
		}
	}
	requested := uniqueSortedStrings(candidate.RequestedPeople)
	for _, person := range requested {
		if peopleScores[person] < minimum {
			return STRIDEAdmittedWorkIntent{}, ErrSTRIDEWorkAdmissionDenied
		}
	}
	relevant = uniqueSortedStrings(append(relevant, requested...))
	if len(relevant) == 0 {
		return STRIDEAdmittedWorkIntent{}, ErrSTRIDEWorkAdmissionDenied
	}
	projectList := mapKeysSorted(projects)
	dedupe := workIntentDedupeDigest(service.TenantID, candidate.OutcomeDigest, refs, relevant, projectList)
	service.Store.mu.Lock()
	defer service.Store.mu.Unlock()
	if existingID := service.Store.IntentDedupe[dedupe]; existingID != "" {
		return service.Store.Intents[existingID], nil
	}
	intent := STRIDEAdmittedWorkIntent{
		ID: candidate.ID, TenantID: service.TenantID, OutcomeDigest: candidate.OutcomeDigest, Evidence: refs, Counterevidence: counter,
		RelevantPeople: relevant, Projects: projectList, Confidence: confidence, DedupeDigest: dedupe,
		CreatedAt: candidate.CreatedAt.UTC(), Status: "admitted",
	}
	if _, exists := service.Store.Intents[intent.ID]; exists {
		return STRIDEAdmittedWorkIntent{}, ErrSTRIDEWorkAdmissionDenied
	}
	service.Store.Intents[intent.ID] = intent
	service.Store.IntentDedupe[dedupe] = intent.ID
	return intent, nil
}

func allowedWorkProjectionContract(kind STRIDEContractType) bool {
	switch kind {
	case STRIDEContractConversationEvent, STRIDEContractTranscriptRevision, STRIDEContractAnalysisProjection, STRIDEContractKnowledgeAssertion:
		// A transcript revision is an admissible primary projection only when the
		// configured Sources authority independently proves that exact revision is
		// still current and readable. The contract allowlist alone grants nothing.
		return true
	default:
		return false
	}
}

type STRIDESuggestedWorkCardSpec struct {
	ID               string
	IntentID         string
	Destination      STRIDEThreadResolution
	Owner            string
	Reviewer         string
	Authority        string
	Budget           STRIDEWorkBudget
	DueAt            time.Time
	ExpiresAt        time.Time
	ApprovalPolicy   STRIDESuggestedWorkApprovalPolicy
	ParentRunID      string
	ParentFeedbackID string
}

func (service STRIDEWorkOrchestrator) ProposeSuggestedWork(spec STRIDESuggestedWorkCardSpec) (STRIDESuggestedWorkCard, error) {
	if err := service.requireEnabled(); err != nil {
		return STRIDESuggestedWorkCard{}, err
	}
	now := service.now()
	service.Store.mu.Lock()
	defer service.Store.mu.Unlock()
	intent, ok := service.Store.Intents[strings.TrimSpace(spec.IntentID)]
	if !ok || intent.TenantID != service.TenantID || intent.Status != "admitted" || !validResolvedDestination(spec.Destination) || !validWorkAuthority(spec.Authority) ||
		!validWorkBudget(spec.Budget) || !spec.DueAt.After(now) || !spec.ExpiresAt.After(now) || spec.ExpiresAt.After(spec.DueAt) || !spec.ApprovalPolicy.ExpiresAt.After(now) ||
		!validSuggestedWorkApprovalPolicy(spec.ApprovalPolicy, spec.Owner, spec.Reviewer) {
		return STRIDESuggestedWorkCard{}, ErrSTRIDEWorkState
	}
	if !strideIdentifier(spec.ID) || !strideIdentifier(spec.Owner) || !strideIdentifier(spec.Reviewer) || spec.Owner == spec.Reviewer {
		return STRIDESuggestedWorkCard{}, ErrSTRIDEWorkState
	}
	for _, principal := range append([]string{spec.Owner, spec.Reviewer}, spec.ApprovalPolicy.EligiblePrincipals...) {
		if !strideWorkContainsString(intent.RelevantPeople, principal) || !strideWorkContainsString(spec.Destination.Audience.Principals, principal) {
			return STRIDESuggestedWorkCard{}, ErrSTRIDEWorkState
		}
	}
	if (spec.ParentRunID == "") != (spec.ParentFeedbackID == "") {
		return STRIDESuggestedWorkCard{}, ErrSTRIDEWorkState
	}
	if spec.ParentRunID != "" {
		parent, parentOK := service.Store.Runs[spec.ParentRunID]
		feedback, feedbackOK := service.Store.Feedback[spec.ParentFeedbackID]
		if !parentOK || parent.TenantID != service.TenantID || !feedbackOK || !feedbackEligibleSTRIDERunStatus(parent.Status) || feedback.RunID != parent.ID || !feedback.Rerun {
			return STRIDESuggestedWorkCard{}, ErrSTRIDEWorkState
		}
	}
	if _, exists := service.Store.Cards[spec.ID]; exists {
		return STRIDESuggestedWorkCard{}, ErrSTRIDEWorkState
	}
	card := STRIDESuggestedWorkCard{
		ID: spec.ID, TenantID: service.TenantID, Revision: 1, IntentID: intent.ID, Evidence: intent.Evidence, Counterevidence: intent.Counterevidence,
		Destination: spec.Destination, Owner: spec.Owner, Reviewer: spec.Reviewer, Authority: spec.Authority,
		Budget: spec.Budget, DueAt: spec.DueAt.UTC(), ExpiresAt: spec.ExpiresAt.UTC(), ApprovalPolicy: spec.ApprovalPolicy,
		Status: "suggested", CreatedAt: now, UpdatedAt: now, ParentRunID: spec.ParentRunID, ParentFeedbackID: spec.ParentFeedbackID,
	}
	service.Store.Cards[card.ID] = card
	return card, nil
}

func (service STRIDEWorkOrchestrator) ReviseSuggestedWork(cardID string, expectedRevision int64, mutate func(*STRIDESuggestedWorkCard) error) (STRIDESuggestedWorkCard, error) {
	if err := service.requireEnabled(); err != nil {
		return STRIDESuggestedWorkCard{}, err
	}
	service.Store.mu.Lock()
	defer service.Store.mu.Unlock()
	card, ok := service.Store.Cards[strings.TrimSpace(cardID)]
	if !ok || card.TenantID != service.TenantID || card.Revision != expectedRevision || card.Status != "suggested" || mutate == nil {
		return STRIDESuggestedWorkCard{}, ErrSTRIDEWorkState
	}
	immutableID, immutableIntentID, immutableTenantID := card.ID, card.IntentID, card.TenantID
	immutableEvidenceDigest := workDigest(uniqueSortedSTRIDEReferences(card.Evidence))
	immutableCounterDigest := workDigest(uniqueSortedSTRIDEReferences(card.Counterevidence))
	immutableCreatedAt := card.CreatedAt
	immutableParentRunID, immutableParentFeedbackID := card.ParentRunID, card.ParentFeedbackID
	if err := mutate(&card); err != nil {
		return STRIDESuggestedWorkCard{}, err
	}
	if card.ID != immutableID || card.IntentID != immutableIntentID || card.TenantID != immutableTenantID ||
		workDigest(uniqueSortedSTRIDEReferences(card.Evidence)) != immutableEvidenceDigest ||
		workDigest(uniqueSortedSTRIDEReferences(card.Counterevidence)) != immutableCounterDigest || !validResolvedDestination(card.Destination) ||
		!validWorkAuthority(card.Authority) || !validWorkBudget(card.Budget) || !card.ExpiresAt.After(service.now()) ||
		!card.ApprovalPolicy.ExpiresAt.After(service.now()) || !validSuggestedWorkApprovalPolicy(card.ApprovalPolicy, card.Owner, card.Reviewer) ||
		card.CreatedAt != immutableCreatedAt || card.ParentRunID != immutableParentRunID || card.ParentFeedbackID != immutableParentFeedbackID ||
		!oneOf(card.Status, "suggested", "dismissed") || card.ConsumedRunID != "" {
		return STRIDESuggestedWorkCard{}, ErrSTRIDEWorkState
	}
	card.Revision++
	card.Endorsements = nil
	card.UpdatedAt = service.now()
	service.Store.Cards[card.ID] = card
	return card, nil
}

func (service STRIDEWorkOrchestrator) DismissSuggestedWork(cardID string, expectedRevision int64, principal, reason string) (STRIDESuggestedWorkCard, error) {
	if strings.TrimSpace(reason) == "" {
		return STRIDESuggestedWorkCard{}, ErrSTRIDEWorkState
	}
	return service.ReviseSuggestedWork(cardID, expectedRevision, func(card *STRIDESuggestedWorkCard) error {
		if principal != card.Owner && principal != card.Reviewer {
			return ErrSTRIDEWorkApproval
		}
		card.Status = "dismissed"
		card.DismissReason = trimForStorage(reason, 500)
		return nil
	})
}

// ApproveSuggestedWork atomically records one endorsement and, at quorum,
// consumes the exact card revision into one deterministic WorkRun.
func (service STRIDEWorkOrchestrator) ApproveSuggestedWork(ctx context.Context, cardID string, expectedRevision int64, principal string) (STRIDESuggestedWorkCard, *STRIDEDurableWorkRun, error) {
	if err := service.requireEnabled(); err != nil {
		return STRIDESuggestedWorkCard{}, nil, err
	}
	if service.Sources == nil || service.ApprovalRights == nil {
		return STRIDESuggestedWorkCard{}, nil, ErrSTRIDEWorkApproval
	}
	now := service.now()
	service.Store.mu.Lock()
	defer service.Store.mu.Unlock()
	card, ok := service.Store.Cards[strings.TrimSpace(cardID)]
	if !ok || card.TenantID != service.TenantID || card.Revision != expectedRevision || !card.ExpiresAt.After(now) || !card.ApprovalPolicy.ExpiresAt.After(now) {
		return STRIDESuggestedWorkCard{}, nil, ErrSTRIDEWorkApproval
	}
	if !strideWorkContainsString(card.ApprovalPolicy.EligiblePrincipals, principal) {
		return STRIDESuggestedWorkCard{}, nil, ErrSTRIDEWorkApproval
	}
	if err := service.ApprovalRights.MayApprove(ctx, principal, card); err != nil {
		return STRIDESuggestedWorkCard{}, nil, ErrSTRIDEWorkApproval
	}
	if card.Status == "approved" && card.ConsumedRunID != "" {
		run, exists := service.Store.Runs[card.ConsumedRunID]
		if !exists {
			return STRIDESuggestedWorkCard{}, nil, ErrSTRIDEWorkState
		}
		return card, &run, nil
	}
	if card.Status != "suggested" {
		return STRIDESuggestedWorkCard{}, nil, ErrSTRIDEWorkApproval
	}
	if err := service.Sources.SourcesCurrent(ctx, "suggested_work_approval", append(append([]STRIDEReference{}, card.Evidence...), card.Counterevidence...)); err != nil {
		return STRIDESuggestedWorkCard{}, nil, ErrSTRIDEWorkSourceChanged
	}
	for _, endorsement := range card.Endorsements {
		if endorsement.Principal == principal {
			return card, nil, nil
		}
	}
	card.Endorsements = append(card.Endorsements, STRIDEWorkEndorsement{Principal: principal, At: now})
	sort.Slice(card.Endorsements, func(i, j int) bool { return card.Endorsements[i].Principal < card.Endorsements[j].Principal })
	card.UpdatedAt = now
	if len(card.Endorsements) >= card.ApprovalPolicy.Quorum {
		runID, idempotency := deterministicWorkRunIdentity(card)
		if existing, exists := service.Store.Runs[runID]; exists {
			card.Status = "approved"
			card.ConsumedRunID = runID
			service.Store.Cards[card.ID] = card
			return card, &existing, nil
		}
		run := STRIDEDurableWorkRun{
			ID: runID, TenantID: service.TenantID, IdempotencyDigest: idempotency, CardID: card.ID, CardRevision: card.Revision,
			Evidence: append([]STRIDEReference(nil), card.Evidence...), Destination: card.Destination,
			Owner: card.Owner, Reviewer: card.Reviewer, Authority: card.Authority, Budget: card.Budget,
			Status: STRIDERunQueued, RouteSnapshots: map[string]STRIDEWorkRouteSnapshot{}, CreatedAt: now, UpdatedAt: now,
			ParentRunID: card.ParentRunID, ParentFeedbackID: card.ParentFeedbackID,
		}
		service.Store.Runs[run.ID] = run
		card.Status = "approved"
		card.ConsumedRunID = run.ID
		service.Store.Cards[card.ID] = card
		return card, &run, nil
	}
	service.Store.Cards[card.ID] = card
	return card, nil, nil
}

func (service STRIDEWorkOrchestrator) SetStageRoute(runID string, expectedStatus string, snapshot STRIDEWorkRouteSnapshot) (STRIDEDurableWorkRun, error) {
	if err := service.requireEnabled(); err != nil {
		return STRIDEDurableWorkRun{}, err
	}
	if !validStageRouteSnapshot(snapshot) {
		return STRIDEDurableWorkRun{}, ErrSTRIDEWorkState
	}
	service.Store.mu.Lock()
	defer service.Store.mu.Unlock()
	run, ok := service.Store.Runs[runID]
	if !ok || run.TenantID != service.TenantID || run.Status != expectedStatus || terminalSTRIDERunStatus(run.Status) || workAuthorityRank(snapshot.Authority) > workAuthorityRank(run.Authority) {
		return STRIDEDurableWorkRun{}, ErrSTRIDEWorkState
	}
	if _, exists := run.RouteSnapshots[snapshot.StageID]; exists {
		return STRIDEDurableWorkRun{}, ErrSTRIDEWorkState
	}
	run.RouteSnapshots[snapshot.StageID] = snapshot
	run.UpdatedAt = service.now()
	service.Store.Runs[run.ID] = run
	return run, nil
}

func (service STRIDEWorkOrchestrator) TransitionRun(runID, to, stageID, reason string) (STRIDEDurableWorkRun, error) {
	if err := service.requireEnabled(); err != nil {
		return STRIDEDurableWorkRun{}, err
	}
	service.Store.mu.Lock()
	defer service.Store.mu.Unlock()
	run, ok := service.Store.Runs[runID]
	// Completion is an authority-bearing transition and must go through
	// CompleteRun, where a current queue lease and verifier receipts are
	// consumed. A caller cannot complete work by choosing a status string.
	if !ok || run.TenantID != service.TenantID || to == STRIDERunCompleted || !legalSTRIDERunTransition(run.Status, to) {
		return STRIDEDurableWorkRun{}, ErrSTRIDEWorkState
	}
	if to == STRIDERunRunning {
		if stageID == "" {
			stageID = run.CurrentStage
		}
		if _, exists := run.RouteSnapshots[stageID]; !exists || len(run.InvalidatedSources) > 0 {
			return STRIDEDurableWorkRun{}, ErrSTRIDEWorkState
		}
		if elevatedWorkAuthority(run.RouteSnapshots[stageID].Authority) && !consumedEffectApprovalForStage(service.Store.EffectApprovals, run, run.RouteSnapshots[stageID], service.now()) {
			return STRIDEDurableWorkRun{}, ErrSTRIDEWorkEffectApproval
		}
		if run.QueueClaim != nil && run.QueueClaim.StageID != stageID {
			run.QueueClaim = nil
		}
		run.CurrentStage = stageID
		if run.Status == STRIDERunQueued || run.Status == STRIDERunFailed || run.Status == STRIDERunBlocked {
			run.Attempts++
			if run.Attempts > run.Budget.MaxAttempts {
				return STRIDEDurableWorkRun{}, ErrSTRIDEWorkState
			}
		}
	}
	if to == STRIDERunBlocked || to == STRIDERunAwaitingInput || to == STRIDERunAwaitingReview || to == STRIDERunFailed || to == STRIDERunCancelled {
		if strings.TrimSpace(reason) == "" {
			return STRIDEDurableWorkRun{}, ErrSTRIDEWorkState
		}
		run.BlockReason = trimForStorage(reason, 500)
	} else {
		run.BlockReason = ""
	}
	run.Status = to
	run.UpdatedAt = service.now()
	if terminalSTRIDERunStatus(to) {
		run.CompletedAt = run.UpdatedAt
	}
	service.Store.Runs[run.ID] = run
	return run, nil
}

// BindQueueClaim persists the leased queue's fencing boundary without taking
// ownership of queue execution. A worker with an older generation can never
// replace a newer claim after a retry or restart.
func (service STRIDEWorkOrchestrator) BindQueueClaim(runID string, claim STRIDEWorkQueueClaim) (STRIDEDurableWorkRun, error) {
	if err := service.requireEnabled(); err != nil {
		return STRIDEDurableWorkRun{}, err
	}
	if service.Execution == nil || !strideIdentifier(claim.JobID) || !strideIdentifier(claim.StageID) || claim.ClaimGeneration < 1 ||
		!isHexDigest(claim.FencingTokenDigest) || strings.TrimSpace(claim.AuthorityReceipt) == "" || !claim.LeaseExpiresAt.After(service.now()) {
		return STRIDEDurableWorkRun{}, ErrSTRIDEWorkState
	}
	service.Store.mu.Lock()
	defer service.Store.mu.Unlock()
	run, ok := service.Store.Runs[runID]
	stage, stageOK := run.RouteSnapshots[claim.StageID]
	if !ok || !stageOK || run.TenantID != service.TenantID || run.Status != STRIDERunRunning || run.CurrentStage != claim.StageID {
		return STRIDEDurableWorkRun{}, ErrSTRIDEWorkState
	}
	if run.QueueClaim != nil {
		if workDigest(*run.QueueClaim) == workDigest(claim) {
			return run, nil
		}
		if claim.ClaimGeneration <= run.QueueClaim.ClaimGeneration {
			return STRIDEDurableWorkRun{}, ErrSTRIDEWorkState
		}
	}
	if err := service.Execution.ConsumeQueueClaim(context.Background(), STRIDEWorkQueueClaimAttestation{TenantID: service.TenantID, RunID: run.ID, Stage: stage, Claim: claim}); err != nil {
		return STRIDEDurableWorkRun{}, ErrSTRIDEWorkAuthority
	}
	copyClaim := claim
	copyClaim.AuthorityReceiptDigest = opaqueReceiptDigest(claim.AuthorityReceipt)
	copyClaim.AuthorityReceipt = ""
	copyClaim.LeaseExpiresAt = copyClaim.LeaseExpiresAt.UTC()
	run.QueueClaim = &copyClaim
	run.UpdatedAt = service.now()
	service.Store.Runs[run.ID] = run
	return run, nil
}

func (service STRIDEWorkOrchestrator) AddCheckpoint(runID string, checkpoint STRIDEWorkCheckpoint) (STRIDEDurableWorkRun, error) {
	if err := service.requireEnabled(); err != nil {
		return STRIDEDurableWorkRun{}, err
	}
	if service.Execution == nil || !strideIdentifier(checkpoint.ID) || !strideIdentifier(checkpoint.StageID) || !oneOf(checkpoint.Status, "passed", "failed", "awaiting_input", "awaiting_review") ||
		!isHexDigest(checkpoint.EvidenceDigest) || strings.TrimSpace(checkpoint.VerifierReceipt) == "" || checkpoint.CreatedAt.IsZero() {
		return STRIDEDurableWorkRun{}, ErrSTRIDEWorkState
	}
	service.Store.mu.Lock()
	defer service.Store.mu.Unlock()
	run, ok := service.Store.Runs[runID]
	stage, stageOK := run.RouteSnapshots[checkpoint.StageID]
	if !ok || !stageOK || run.TenantID != service.TenantID || terminalSTRIDERunStatus(run.Status) || run.CurrentStage != checkpoint.StageID || run.QueueClaim == nil || !run.QueueClaim.LeaseExpiresAt.After(service.now()) {
		return STRIDEDurableWorkRun{}, ErrSTRIDEWorkState
	}
	for _, existing := range run.Checkpoints {
		if existing.ID == checkpoint.ID {
			if existing == checkpoint {
				return run, nil
			}
			return STRIDEDurableWorkRun{}, ErrSTRIDEWorkState
		}
	}
	if err := service.Execution.ConsumeCheckpoint(context.Background(), STRIDEWorkCheckpointAttestation{TenantID: service.TenantID, RunID: run.ID, Stage: stage, Claim: *run.QueueClaim, Checkpoint: checkpoint}); err != nil {
		return STRIDEDurableWorkRun{}, ErrSTRIDEWorkAuthority
	}
	checkpoint.VerifierReceiptDigest = opaqueReceiptDigest(checkpoint.VerifierReceipt)
	checkpoint.VerifierReceipt = ""
	run.Checkpoints = append(run.Checkpoints, checkpoint)
	run.UpdatedAt = service.now()
	service.Store.Runs[run.ID] = run
	return run, nil
}

func (service STRIDEWorkOrchestrator) InvalidateSources(runID, reason string, refs []STRIDEReference) (STRIDEDurableWorkRun, error) {
	if err := service.requireEnabled(); err != nil {
		return STRIDEDurableWorkRun{}, err
	}
	refs = uniqueSortedSTRIDEReferences(refs)
	if len(refs) == 0 || strings.TrimSpace(reason) == "" {
		return STRIDEDurableWorkRun{}, ErrSTRIDEWorkState
	}
	service.Store.mu.Lock()
	defer service.Store.mu.Unlock()
	run, ok := service.Store.Runs[runID]
	if !ok || run.TenantID != service.TenantID || terminalSTRIDERunStatus(run.Status) || !referenceSubset(refs, run.Evidence) {
		return STRIDEDurableWorkRun{}, ErrSTRIDEWorkState
	}
	run.InvalidatedSources = refs
	run.Status = STRIDERunBlocked
	run.BlockReason = trimForStorage(reason, 500)
	run.UpdatedAt = service.now()
	service.Store.Runs[run.ID] = run
	return run, nil
}

func (service STRIDEWorkOrchestrator) ReauthorizeRunSources(ctx context.Context, runID string) (STRIDEDurableWorkRun, error) {
	if err := service.requireEnabled(); err != nil {
		return STRIDEDurableWorkRun{}, err
	}
	if service.Sources == nil {
		return STRIDEDurableWorkRun{}, ErrSTRIDEWorkSourceChanged
	}
	service.Store.mu.Lock()
	defer service.Store.mu.Unlock()
	run, ok := service.Store.Runs[runID]
	if !ok || run.TenantID != service.TenantID || run.Status != STRIDERunBlocked || len(run.InvalidatedSources) == 0 {
		return STRIDEDurableWorkRun{}, ErrSTRIDEWorkState
	}
	if err := service.Sources.SourcesCurrent(ctx, "work_run_reauthorization", run.Evidence); err != nil {
		return STRIDEDurableWorkRun{}, ErrSTRIDEWorkSourceChanged
	}
	run.InvalidatedSources = nil
	run.BlockReason = "source reauthorization recorded; explicit resume required"
	run.UpdatedAt = service.now()
	service.Store.Runs[run.ID] = run
	return run, nil
}

func (service STRIDEWorkOrchestrator) RegisterElevatedEffectApproval(approval STRIDEElevatedEffectApproval) error {
	if err := service.requireEnabled(); err != nil {
		return err
	}
	service.Store.mu.Lock()
	defer service.Store.mu.Unlock()
	run, ok := service.Store.Runs[approval.RunID]
	if !ok || run.TenantID != service.TenantID || terminalSTRIDERunStatus(run.Status) {
		return ErrSTRIDEWorkState
	}
	stage, ok := run.RouteSnapshots[approval.StageID]
	if !ok || service.Execution == nil || !elevatedWorkAuthority(stage.Authority) || !strideIdentifier(approval.ID) || approval.Revision < 1 || !approval.Current || approval.Consumed ||
		approval.TenantID != service.TenantID || strings.TrimSpace(approval.AuthorityReceipt) == "" ||
		approval.Authority != stage.Authority || !approval.ExpiresAt.After(service.now()) || approval.BindingDigest != elevatedEffectBindingDigest(run, stage) {
		return ErrSTRIDEWorkEffectApproval
	}
	if existing, exists := service.Store.EffectApprovals[approval.ID]; exists {
		if workDigest(existing) == workDigest(approval) {
			return nil
		}
		return ErrSTRIDEWorkEffectApproval
	}
	service.Store.EffectApprovals[approval.ID] = approval
	return nil
}

func (service STRIDEWorkOrchestrator) AuthorizeElevatedStage(ctx context.Context, runID, stageID, approvalID string) error {
	if err := service.requireEnabled(); err != nil {
		return err
	}
	service.Store.mu.Lock()
	defer service.Store.mu.Unlock()
	run, ok := service.Store.Runs[runID]
	if !ok || run.TenantID != service.TenantID || terminalSTRIDERunStatus(run.Status) {
		return ErrSTRIDEWorkState
	}
	stage, ok := run.RouteSnapshots[stageID]
	if !ok {
		return ErrSTRIDEWorkState
	}
	if !elevatedWorkAuthority(stage.Authority) {
		return nil
	}
	approval, exists := service.Store.EffectApprovals[approvalID]
	if service.Execution == nil || !exists || !approval.Current || approval.Consumed || approval.RunID != run.ID || approval.StageID != stageID || approval.Authority != stage.Authority ||
		approval.TenantID != service.TenantID || strings.TrimSpace(approval.AuthorityReceipt) == "" ||
		approval.Revision < 1 || !approval.ExpiresAt.After(service.now()) || approval.BindingDigest != elevatedEffectBindingDigest(run, stage) {
		return ErrSTRIDEWorkEffectApproval
	}
	if err := service.Execution.ConsumeElevatedApproval(ctx, STRIDEWorkElevatedEffectAttestation{TenantID: service.TenantID, RunID: run.ID, Stage: stage, Approval: approval}); err != nil {
		return ErrSTRIDEWorkEffectApproval
	}
	approval.Consumed = true
	approval.AuthorityReceiptDigest = opaqueReceiptDigest(approval.AuthorityReceipt)
	approval.AuthorityReceipt = ""
	service.Store.EffectApprovals[approval.ID] = approval
	return nil
}

// CompleteRun consumes a one-use verifier attestation bound to the tenant,
// run, route, current queue generation and exact checkpoint set. This is the
// only path to completed.
func (service STRIDEWorkOrchestrator) CompleteRun(ctx context.Context, runID, receipt string) (STRIDEDurableWorkRun, error) {
	if err := service.requireEnabled(); err != nil {
		return STRIDEDurableWorkRun{}, err
	}
	if service.Execution == nil || strings.TrimSpace(receipt) == "" {
		return STRIDEDurableWorkRun{}, ErrSTRIDEWorkAuthority
	}
	service.Store.mu.Lock()
	defer service.Store.mu.Unlock()
	run, ok := service.Store.Runs[runID]
	stage, stageOK := run.RouteSnapshots[run.CurrentStage]
	if !ok || !stageOK || run.TenantID != service.TenantID || run.Status != STRIDERunRunning || run.QueueClaim == nil ||
		run.QueueClaim.StageID != run.CurrentStage || !run.QueueClaim.LeaseExpiresAt.After(service.now()) || len(run.InvalidatedSources) != 0 || !hasPassedCheckpoint(run.Checkpoints, run.CurrentStage) {
		return STRIDEDurableWorkRun{}, ErrSTRIDEWorkState
	}
	request := STRIDEWorkCompletionAttestation{TenantID: service.TenantID, RunID: run.ID, Stage: stage, Claim: *run.QueueClaim, Checkpoints: append([]STRIDEWorkCheckpoint(nil), run.Checkpoints...), Receipt: receipt}
	if err := service.Execution.ConsumeCompletion(ctx, request); err != nil {
		return STRIDEDurableWorkRun{}, ErrSTRIDEWorkAuthority
	}
	run.Status = STRIDERunCompleted
	run.BlockReason = ""
	run.CompletedAt = service.now()
	run.UpdatedAt = run.CompletedAt
	run.CompletionReceiptDigest = opaqueReceiptDigest(receipt)
	service.Store.Runs[run.ID] = run
	return run, nil
}

func (service STRIDEWorkOrchestrator) RecordArtifact(binding STRIDEWorkArtifactBinding) (STRIDEWorkArtifactBinding, error) {
	if err := service.requireEnabled(); err != nil {
		return STRIDEWorkArtifactBinding{}, err
	}
	service.Store.mu.Lock()
	defer service.Store.mu.Unlock()
	run, ok := service.Store.Runs[binding.RunID]
	if !ok || run.TenantID != service.TenantID || terminalSTRIDERunStatus(run.Status) || binding.Artifact.Validate() != nil || binding.Audience.Validate() != nil ||
		!sameReferenceSet(binding.Evidence, run.Evidence) || !sameThreadResolution(binding.Destination, run.Destination) ||
		!sameAudience(binding.Audience, run.Destination.Audience) || binding.StageID != run.CurrentStage || !strideIdentifier(binding.ID) || binding.CreatedAt.IsZero() {
		return STRIDEWorkArtifactBinding{}, ErrSTRIDEWorkState
	}
	if existing, exists := service.Store.Artifacts[binding.ID]; exists {
		if workDigest(existing) == workDigest(binding) {
			return existing, nil
		}
		return STRIDEWorkArtifactBinding{}, ErrSTRIDEWorkState
	}
	service.Store.Artifacts[binding.ID] = binding
	return binding, nil
}

func (service STRIDEWorkOrchestrator) RecordOutcome(binding STRIDEWorkOutcomeBinding) (STRIDEWorkOutcomeBinding, error) {
	if err := service.requireEnabled(); err != nil {
		return STRIDEWorkOutcomeBinding{}, err
	}
	service.Store.mu.Lock()
	defer service.Store.mu.Unlock()
	run, ok := service.Store.Runs[binding.RunID]
	if !ok || run.TenantID != service.TenantID || run.Status != STRIDERunCompleted || !oneOf(binding.Verdict, "accepted", "partial", "rejected") ||
		!sameReferenceSet(binding.Evidence, run.Evidence) || !sameThreadResolution(binding.Destination, run.Destination) ||
		!sameAudience(binding.Audience, run.Destination.Audience) || binding.Reviewer != run.Reviewer || binding.CompletedAt.IsZero() || !strideIdentifier(binding.ID) {
		return STRIDEWorkOutcomeBinding{}, ErrSTRIDEWorkState
	}
	if binding.Verdict != "rejected" && len(binding.ArtifactIDs) == 0 {
		return STRIDEWorkOutcomeBinding{}, ErrSTRIDEWorkState
	}
	for _, artifactID := range binding.ArtifactIDs {
		artifact, exists := service.Store.Artifacts[artifactID]
		if !exists || artifact.RunID != run.ID {
			return STRIDEWorkOutcomeBinding{}, ErrSTRIDEWorkState
		}
	}
	if existing, exists := service.Store.Outcomes[binding.ID]; exists {
		if workDigest(existing) == workDigest(binding) {
			return existing, nil
		}
		return STRIDEWorkOutcomeBinding{}, ErrSTRIDEWorkState
	}
	service.Store.Outcomes[binding.ID] = binding
	return binding, nil
}

func (service STRIDEWorkOrchestrator) AddFeedback(feedback STRIDEWorkFeedback) (STRIDEWorkFeedback, error) {
	if err := service.requireEnabled(); err != nil {
		return STRIDEWorkFeedback{}, err
	}
	if !strideIdentifier(feedback.ID) || !strideIdentifier(feedback.RunID) || !strideIdentifier(feedback.Author) ||
		!oneOf(feedback.Kind, "correction", "quality", "scope", "rerun_request") || !isHexDigest(feedback.BodyDigest) || feedback.CreatedAt.IsZero() ||
		(feedback.Rerun != (feedback.Kind == "rerun_request")) {
		return STRIDEWorkFeedback{}, ErrSTRIDEWorkState
	}
	service.Store.mu.Lock()
	defer service.Store.mu.Unlock()
	run, ok := service.Store.Runs[feedback.RunID]
	if !ok || run.TenantID != service.TenantID || !feedbackEligibleSTRIDERunStatus(run.Status) {
		return STRIDEWorkFeedback{}, ErrSTRIDEWorkState
	}
	if existing, exists := service.Store.Feedback[feedback.ID]; exists {
		if existing == feedback {
			return existing, nil
		}
		return STRIDEWorkFeedback{}, ErrSTRIDEWorkState
	}
	service.Store.Feedback[feedback.ID] = feedback
	return feedback, nil
}

func (service STRIDEWorkOrchestrator) IssueDelegation(ctx context.Context, runID, stageID, trigger string, teamAgent, capabilities, assignment STRIDEReference, tools []string, maxDuration time.Duration, maxCostMicros int64) (STRIDEDelegationToken, error) {
	if err := service.requireEnabled(); err != nil {
		return STRIDEDelegationToken{}, err
	}
	if service.Agents == nil || trigger != "human_approved_work_run" || teamAgent.ContractType != STRIDEContractTeamAgent || capabilities.ContractType != STRIDEContractAgentCapabilityManifest ||
		assignment.ContractType != STRIDEContractAgentAssignment || teamAgent.Validate() != nil || capabilities.Validate() != nil || assignment.Validate() != nil ||
		maxDuration <= 0 || maxDuration > 15*time.Minute || maxCostMicros <= 0 || len(tools) == 0 {
		return STRIDEDelegationToken{}, ErrSTRIDEDelegationDenied
	}
	tools = uniqueSortedStrings(tools)
	for _, tool := range tools {
		if !strideIdentifier(tool) {
			return STRIDEDelegationToken{}, ErrSTRIDEDelegationDenied
		}
	}
	if err := service.Agents.ReferencesCurrent(ctx, teamAgent, capabilities, assignment); err != nil {
		return STRIDEDelegationToken{}, ErrSTRIDEDelegationDenied
	}
	raw := make([]byte, 64)
	if service.Random != nil {
		if err := service.Random(raw); err != nil {
			return STRIDEDelegationToken{}, ErrSTRIDEDelegationDenied
		}
	} else if _, err := rand.Read(raw); err != nil {
		return STRIDEDelegationToken{}, ErrSTRIDEDelegationDenied
	}
	now := service.now()
	token := base64.RawURLEncoding.EncodeToString(raw[32:])
	tokenDigest := sha256.Sum256([]byte(token))
	grant := STRIDEAgentDelegationGrant{
		ID: "stride-delegation-" + base64.RawURLEncoding.EncodeToString(raw[:32]), RunID: runID, StageID: stageID,
		TeamAgent: teamAgent, CapabilityManifest: capabilities, Assignment: assignment,
		RuntimePrincipal:   "stride-runtime-" + base64.RawURLEncoding.EncodeToString(raw[:24]),
		RuntimeTokenDigest: fmt.Sprintf("%x", tokenDigest[:]), AllowedTools: tools,
		MaxDuration: maxDuration, MaxCostMicros: maxCostMicros, MaxDelegationHops: 0, Trigger: trigger,
		IssuedAt: now, ExpiresAt: now.Add(maxDuration),
	}
	service.Store.mu.Lock()
	defer service.Store.mu.Unlock()
	run, ok := service.Store.Runs[runID]
	stage, stageOK := run.RouteSnapshots[stageID]
	if !ok || !stageOK || run.TenantID != service.TenantID || run.Status != STRIDERunRunning || run.CurrentStage != stageID ||
		!referenceSetContains(stage.CapabilityRefs, capabilities) || maxCostMicros > stage.MaxCostMicros || maxDuration > stage.MaxDuration {
		return STRIDEDelegationToken{}, ErrSTRIDEDelegationDenied
	}
	if _, exists := service.Store.Delegations[grant.ID]; exists {
		return STRIDEDelegationToken{}, ErrSTRIDEDelegationDenied
	}
	service.Store.Delegations[grant.ID] = grant
	return STRIDEDelegationToken{Grant: grant, Token: token}, nil
}

func (store *STRIDEWorkOrchestrationStore) Snapshot() ([]byte, string, error) {
	if store == nil {
		return nil, "", ErrSTRIDEWorkSnapshotInvalid
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	payload, err := json.Marshal(store)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(payload)
	return payload, fmt.Sprintf("%x", digest[:]), nil
}

func RestoreSTRIDEWorkOrchestrationStore(payload []byte, expectedDigest string) (*STRIDEWorkOrchestrationStore, error) {
	digest := sha256.Sum256(payload)
	if fmt.Sprintf("%x", digest[:]) != strings.TrimSpace(expectedDigest) {
		return nil, ErrSTRIDEWorkSnapshotInvalid
	}
	var store STRIDEWorkOrchestrationStore
	if err := json.Unmarshal(payload, &store); err != nil || store.Version != 1 || store.Intents == nil || store.IntentDedupe == nil || store.Cards == nil || store.Runs == nil || store.Artifacts == nil || store.Outcomes == nil || store.Feedback == nil || store.EffectApprovals == nil || store.Delegations == nil {
		return nil, ErrSTRIDEWorkSnapshotInvalid
	}
	for id, run := range store.Runs {
		if id != run.ID || !validRestoredRun(run) {
			return nil, ErrSTRIDEWorkSnapshotInvalid
		}
	}
	for digest, id := range store.IntentDedupe {
		intent, ok := store.Intents[id]
		if !ok || digest != intent.DedupeDigest || !isHexDigest(digest) || !validRestoredIntent(intent) {
			return nil, ErrSTRIDEWorkSnapshotInvalid
		}
	}
	for id, card := range store.Cards {
		if id != card.ID || !validRestoredCard(card) {
			return nil, ErrSTRIDEWorkSnapshotInvalid
		}
		intent, ok := store.Intents[card.IntentID]
		if !ok || card.TenantID != intent.TenantID || !sameReferenceSet(card.Evidence, intent.Evidence) || !sameReferenceSet(card.Counterevidence, intent.Counterevidence) {
			return nil, ErrSTRIDEWorkSnapshotInvalid
		}
		if card.ConsumedRunID != "" {
			run, exists := store.Runs[card.ConsumedRunID]
			if !exists || run.CardID != card.ID || run.CardRevision != card.Revision || card.Status != "approved" {
				return nil, ErrSTRIDEWorkSnapshotInvalid
			}
		}
	}
	for id, artifact := range store.Artifacts {
		run, ok := store.Runs[artifact.RunID]
		if id != artifact.ID || !ok || artifact.Artifact.Validate() != nil || artifact.Audience.Validate() != nil ||
			!sameReferenceSet(artifact.Evidence, run.Evidence) || !sameThreadResolution(artifact.Destination, run.Destination) ||
			!sameAudience(artifact.Audience, run.Destination.Audience) {
			return nil, ErrSTRIDEWorkSnapshotInvalid
		}
	}
	for id, outcome := range store.Outcomes {
		run, ok := store.Runs[outcome.RunID]
		if id != outcome.ID || !ok || !oneOf(outcome.Verdict, "accepted", "partial", "rejected") ||
			!sameReferenceSet(outcome.Evidence, run.Evidence) || !sameThreadResolution(outcome.Destination, run.Destination) ||
			!sameAudience(outcome.Audience, run.Destination.Audience) || outcome.Reviewer != run.Reviewer {
			return nil, ErrSTRIDEWorkSnapshotInvalid
		}
		for _, artifactID := range outcome.ArtifactIDs {
			artifact, exists := store.Artifacts[artifactID]
			if !exists || artifact.RunID != run.ID {
				return nil, ErrSTRIDEWorkSnapshotInvalid
			}
		}
	}
	for id, feedback := range store.Feedback {
		run, ok := store.Runs[feedback.RunID]
		if id != feedback.ID || !ok || !feedbackEligibleSTRIDERunStatus(run.Status) || !validRestoredFeedback(feedback) {
			return nil, ErrSTRIDEWorkSnapshotInvalid
		}
	}
	for id, approval := range store.EffectApprovals {
		run, ok := store.Runs[approval.RunID]
		stage, stageOK := run.RouteSnapshots[approval.StageID]
		if id != approval.ID || !ok || !stageOK || !strideIdentifier(approval.ID) || approval.Revision < 1 || !approval.Current || approval.TenantID != run.TenantID ||
			approval.Authority != stage.Authority || !approval.ExpiresAt.After(run.CreatedAt) || approval.BindingDigest != elevatedEffectBindingDigest(run, stage) ||
			(approval.Consumed && !isHexDigest(approval.AuthorityReceiptDigest)) {
			return nil, ErrSTRIDEWorkSnapshotInvalid
		}
	}
	for id, grant := range store.Delegations {
		run, ok := store.Runs[grant.RunID]
		stage, stageOK := run.RouteSnapshots[grant.StageID]
		if id != grant.ID || !ok || !stageOK || !validRestoredDelegation(grant) || !referenceSetContains(stage.CapabilityRefs, grant.CapabilityManifest) {
			return nil, ErrSTRIDEWorkSnapshotInvalid
		}
	}
	return &store, nil
}

func legalSTRIDERunTransition(from, to string) bool {
	if from == to || terminalSTRIDERunStatus(from) {
		return false
	}
	switch from {
	case STRIDERunQueued:
		return to == STRIDERunRunning || to == STRIDERunCancelled || to == STRIDERunBlocked
	case STRIDERunRunning:
		return to == STRIDERunAwaitingInput || to == STRIDERunAwaitingReview || to == STRIDERunBlocked || to == STRIDERunCompleted || to == STRIDERunFailed || to == STRIDERunCancelled
	case STRIDERunAwaitingInput, STRIDERunAwaitingReview:
		return to == STRIDERunRunning || to == STRIDERunBlocked || to == STRIDERunCancelled
	case STRIDERunBlocked, STRIDERunFailed:
		return to == STRIDERunRunning || to == STRIDERunCancelled
	default:
		return false
	}
}

func terminalSTRIDERunStatus(value string) bool {
	return value == STRIDERunCompleted || value == STRIDERunCancelled
}

func feedbackEligibleSTRIDERunStatus(value string) bool {
	return terminalSTRIDERunStatus(value) || value == STRIDERunFailed
}

func validWorkAuthority(value string) bool {
	return oneOf(value, STRIDEWorkAuthorityReadOnly, STRIDEWorkAuthorityInternalWrite, STRIDEWorkAuthorityExternalWrite, STRIDEWorkAuthorityProduction, STRIDEWorkAuthorityDestructive)
}

func elevatedWorkAuthority(value string) bool {
	return value == STRIDEWorkAuthorityExternalWrite || value == STRIDEWorkAuthorityProduction || value == STRIDEWorkAuthorityDestructive
}

func workAuthorityRank(value string) int {
	switch value {
	case STRIDEWorkAuthorityReadOnly:
		return 1
	case STRIDEWorkAuthorityInternalWrite:
		return 2
	case STRIDEWorkAuthorityExternalWrite:
		return 3
	case STRIDEWorkAuthorityProduction:
		return 4
	case STRIDEWorkAuthorityDestructive:
		return 5
	default:
		return 99
	}
}

func validWorkBudget(value STRIDEWorkBudget) bool {
	return value.MaxCostMicros > 0 && value.MaxDuration > 0 && value.MaxDuration <= 24*time.Hour && value.MaxAttempts > 0 && value.MaxAttempts <= 10
}

func validSuggestedWorkApprovalPolicy(value STRIDESuggestedWorkApprovalPolicy, owner, reviewer string) bool {
	eligible := uniqueSortedStrings(value.EligiblePrincipals)
	return len(eligible) == len(value.EligiblePrincipals) && value.Quorum > 0 && value.Quorum <= len(eligible) && !value.ExpiresAt.IsZero() && strideWorkContainsString(eligible, owner) && strideWorkContainsString(eligible, reviewer)
}

func validResolvedDestination(value STRIDEThreadResolution) bool {
	return value.Status == STRIDEThreadReuse && strideIdentifier(value.ThreadID) && value.ACLVersion > 0 && value.Audience.Validate() == nil && len(value.Candidates) == 0
}

func validStageRouteSnapshot(value STRIDEWorkRouteSnapshot) bool {
	if !strideIdentifier(value.StageID) || !strideIdentifier(value.RouteID) || value.RouteRevision < 1 || !isHexDigest(value.RouteDigest) ||
		!validWorkAuthority(value.Authority) || value.MaxCostMicros <= 0 || value.MaxDuration <= 0 || len(value.CapabilityRefs) == 0 {
		return false
	}
	return allReferencesValid(value.CapabilityRefs)
}

func validRestoredRun(run STRIDEDurableWorkRun) bool {
	if !strideIdentifier(run.ID) || !strideIdentifier(run.TenantID) || !isHexDigest(run.IdempotencyDigest) || !validWorkAuthority(run.Authority) || !validWorkBudget(run.Budget) || !validResolvedDestination(run.Destination) || !allReferencesValid(run.Evidence) {
		return false
	}
	if !oneOf(run.Status, STRIDERunQueued, STRIDERunRunning, STRIDERunAwaitingInput, STRIDERunAwaitingReview, STRIDERunBlocked, STRIDERunCompleted, STRIDERunFailed, STRIDERunCancelled) {
		return false
	}
	for stageID, route := range run.RouteSnapshots {
		if stageID != route.StageID || !validStageRouteSnapshot(route) {
			return false
		}
	}
	if run.QueueClaim != nil && (!strideIdentifier(run.QueueClaim.JobID) || !strideIdentifier(run.QueueClaim.StageID) ||
		run.QueueClaim.ClaimGeneration < 1 || !isHexDigest(run.QueueClaim.FencingTokenDigest) || !isHexDigest(run.QueueClaim.AuthorityReceiptDigest) || run.RouteSnapshots[run.QueueClaim.StageID].StageID == "") {
		return false
	}
	for _, checkpoint := range run.Checkpoints {
		if !strideIdentifier(checkpoint.ID) || !strideIdentifier(checkpoint.StageID) || !oneOf(checkpoint.Status, "passed", "failed", "awaiting_input", "awaiting_review") ||
			!isHexDigest(checkpoint.EvidenceDigest) || !isHexDigest(checkpoint.VerifierReceiptDigest) || checkpoint.CreatedAt.IsZero() || run.RouteSnapshots[checkpoint.StageID].StageID == "" {
			return false
		}
	}
	if run.Status == STRIDERunCompleted && !isHexDigest(run.CompletionReceiptDigest) {
		return false
	}
	return true
}

func validRestoredIntent(intent STRIDEAdmittedWorkIntent) bool {
	return strideIdentifier(intent.ID) && strideIdentifier(intent.TenantID) && isHexDigest(intent.OutcomeDigest) && allReferencesValid(intent.Evidence) &&
		optionalReferencesValid(intent.Counterevidence) && len(intent.RelevantPeople) > 0 && intent.Confidence >= 0 && intent.Confidence <= 1 &&
		isHexDigest(intent.DedupeDigest) && !intent.CreatedAt.IsZero() && intent.Status == "admitted"
}

func validRestoredCard(card STRIDESuggestedWorkCard) bool {
	if !strideIdentifier(card.ID) || !strideIdentifier(card.TenantID) || card.Revision < 1 || !strideIdentifier(card.IntentID) || !allReferencesValid(card.Evidence) ||
		!optionalReferencesValid(card.Counterevidence) || !validResolvedDestination(card.Destination) || !strideIdentifier(card.Owner) ||
		!strideIdentifier(card.Reviewer) || card.Owner == card.Reviewer || !validWorkAuthority(card.Authority) || !validWorkBudget(card.Budget) ||
		!validSuggestedWorkApprovalPolicy(card.ApprovalPolicy, card.Owner, card.Reviewer) || !oneOf(card.Status, "suggested", "dismissed", "approved") ||
		card.CreatedAt.IsZero() || card.UpdatedAt.IsZero() || (card.ParentRunID == "") != (card.ParentFeedbackID == "") {
		return false
	}
	if card.Status == "approved" {
		return strideIdentifier(card.ConsumedRunID)
	}
	return card.ConsumedRunID == ""
}

func validRestoredFeedback(feedback STRIDEWorkFeedback) bool {
	return strideIdentifier(feedback.ID) && strideIdentifier(feedback.RunID) && strideIdentifier(feedback.Author) &&
		oneOf(feedback.Kind, "correction", "quality", "scope", "rerun_request") && isHexDigest(feedback.BodyDigest) &&
		!feedback.CreatedAt.IsZero() && feedback.Rerun == (feedback.Kind == "rerun_request")
}

func validRestoredDelegation(grant STRIDEAgentDelegationGrant) bool {
	return strideIdentifier(grant.ID) && strideIdentifier(grant.RunID) && strideIdentifier(grant.StageID) &&
		grant.TeamAgent.ContractType == STRIDEContractTeamAgent && grant.TeamAgent.Validate() == nil &&
		grant.CapabilityManifest.ContractType == STRIDEContractAgentCapabilityManifest && grant.CapabilityManifest.Validate() == nil &&
		grant.Assignment.ContractType == STRIDEContractAgentAssignment && grant.Assignment.Validate() == nil &&
		strideIdentifier(grant.RuntimePrincipal) && isHexDigest(grant.RuntimeTokenDigest) && len(grant.AllowedTools) > 0 &&
		grant.MaxDuration > 0 && grant.MaxDuration <= 15*time.Minute && grant.MaxCostMicros > 0 && grant.MaxDelegationHops == 0 &&
		grant.Trigger == "human_approved_work_run" && !grant.IssuedAt.IsZero() && grant.ExpiresAt.After(grant.IssuedAt)
}

func deterministicWorkRunIdentity(card STRIDESuggestedWorkCard) (string, string) {
	canonical := strings.Join([]string{"stride-work-run/v2", card.TenantID, card.ID, fmt.Sprint(card.Revision), workDigest(card.Evidence), card.Destination.ThreadID, fmt.Sprint(card.Destination.ACLVersion)}, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	value := fmt.Sprintf("%x", digest[:])
	return "stride-run-" + value[:32], value
}

func workIntentDedupeDigest(tenantID, outcome string, evidence []STRIDEReference, people, projects []string) string {
	canonical := strings.Join([]string{"stride-work-intent/v2", tenantID, outcome, workDigest(evidence), strings.Join(people, ","), strings.Join(projects, ",")}, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("%x", digest[:])
}

func elevatedEffectBindingDigest(run STRIDEDurableWorkRun, stage STRIDEWorkRouteSnapshot) string {
	canonical := strings.Join([]string{"stride-elevated-effect/v2", run.TenantID, run.ID, stage.StageID, stage.Authority, stage.RouteID, fmt.Sprint(stage.RouteRevision), stage.RouteDigest, run.Destination.ThreadID, fmt.Sprint(run.Destination.ACLVersion)}, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("%x", digest[:])
}

func uniqueSortedSTRIDEReferences(values []STRIDEReference) []STRIDEReference {
	values = append([]STRIDEReference(nil), values...)
	sort.Slice(values, func(i, j int) bool {
		if values[i].ContractType != values[j].ContractType {
			return values[i].ContractType < values[j].ContractType
		}
		if values[i].ID != values[j].ID {
			return values[i].ID < values[j].ID
		}
		return values[i].Revision < values[j].Revision
	})
	result := values[:0]
	for _, value := range values {
		if len(result) > 0 && result[len(result)-1].ContractType == value.ContractType && result[len(result)-1].ID == value.ID && result[len(result)-1].Revision == value.Revision {
			if result[len(result)-1].Digest != value.Digest {
				return nil
			}
			continue
		}
		result = append(result, value)
	}
	return result
}

func allReferencesValid(values []STRIDEReference) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if value.Validate() != nil {
			return false
		}
	}
	return true
}
func optionalReferencesValid(values []STRIDEReference) bool {
	return len(values) == 0 || allReferencesValid(values)
}
func consumedEffectApprovalForStage(values map[string]STRIDEElevatedEffectApproval, run STRIDEDurableWorkRun, stage STRIDEWorkRouteSnapshot, now time.Time) bool {
	for _, approval := range values {
		if approval.RunID == run.ID && approval.StageID == stage.StageID && approval.Authority == stage.Authority && approval.Current && approval.Consumed && approval.ExpiresAt.After(now) && approval.BindingDigest == elevatedEffectBindingDigest(run, stage) {
			return true
		}
	}
	return false
}

func sameReferenceSet(left, right []STRIDEReference) bool {
	leftSorted, rightSorted := uniqueSortedSTRIDEReferences(left), uniqueSortedSTRIDEReferences(right)
	if (len(left) > 0 && len(leftSorted) == 0) || (len(right) > 0 && len(rightSorted) == 0) {
		return false
	}
	return workDigest(leftSorted) == workDigest(rightSorted)
}
func referenceSetContains(values []STRIDEReference, wanted STRIDEReference) bool {
	for _, value := range values {
		if value.ContractType == wanted.ContractType && value.ID == wanted.ID && value.Revision == wanted.Revision && value.Digest == wanted.Digest {
			return true
		}
	}
	return false
}
func referenceSubset(subset, full []STRIDEReference) bool {
	set := map[string]struct{}{}
	for _, value := range full {
		set[workDigest(value)] = struct{}{}
	}
	for _, value := range subset {
		if _, ok := set[workDigest(value)]; !ok {
			return false
		}
	}
	return true
}
func workDigest(value any) string {
	raw, _ := json.Marshal(value)
	digest := sha256.Sum256(raw)
	return fmt.Sprintf("%x", digest[:])
}
func opaqueReceiptDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", digest[:])
}
func hasPassedCheckpoint(checkpoints []STRIDEWorkCheckpoint, stageID string) bool {
	for _, checkpoint := range checkpoints {
		if checkpoint.StageID == stageID && checkpoint.Status == "passed" && isHexDigest(checkpoint.VerifierReceiptDigest) {
			return true
		}
	}
	return false
}
func stringSet(values []string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			result[trimmed] = struct{}{}
		}
	}
	return result
}
func setsIntersect(left, right map[string]struct{}) bool {
	for value := range left {
		if _, ok := right[value]; ok {
			return true
		}
	}
	return false
}
func setContainsAll(container, required map[string]struct{}) bool {
	for value := range required {
		if _, ok := container[value]; !ok {
			return false
		}
	}
	return true
}
func mapKeysSorted(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
func strideWorkContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
func sameAudience(left, right STRIDEAudience) bool {
	return left.Visibility == right.Visibility && strings.Join(uniqueSortedStrings(left.Principals), "\x00") == strings.Join(uniqueSortedStrings(right.Principals), "\x00")
}
func sameThreadResolution(left, right STRIDEThreadResolution) bool {
	return left.Status == right.Status && left.ThreadID == right.ThreadID && left.ACLVersion == right.ACLVersion && sameAudience(left.Audience, right.Audience)
}
