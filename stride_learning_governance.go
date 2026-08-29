package main

// Reviewed learning governance. Candidates contain digests and exact durable
// references only; they cannot carry raw source bodies or mutate a provider.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"time"
)

const strideLearningAuditMaxEventBytes = 1 << 20

var (
	ErrSTRIDELearningInvalid     = errors.New("STRIDE learning governance record is invalid")
	ErrSTRIDELearningPrivacy     = errors.New("STRIDE learning candidate would widen source authority")
	ErrSTRIDELearningGate        = errors.New("STRIDE learning activation gate failed")
	ErrSTRIDELearningConflict    = errors.New("STRIDE learning audit conflicts with durable history")
	ErrSTRIDELearningAbsent      = errors.New("STRIDE learning candidate is absent")
	ErrSTRIDELearningUnavailable = errors.New("STRIDE learning audit repository is unavailable")
)

type STRIDELearningScope string

const (
	STRIDELearningCompany STRIDELearningScope = "company"
	STRIDELearningProject STRIDELearningScope = "project"
	STRIDELearningPerson  STRIDELearningScope = "person"
	STRIDELearningAgent   STRIDELearningScope = "agent"
)

type STRIDELearningAuthorityFence struct {
	SourceScope           STRIDELearningScope `json:"sourceScope"`
	SourceScopeID         string              `json:"sourceScopeId"`
	SourceAudience        STRIDEAudience      `json:"sourceAudience"`
	CandidateAudience     STRIDEAudience      `json:"candidateAudience"`
	ACLRevision           int64               `json:"aclRevision"`
	ConsentRevision       int64               `json:"consentRevision"`
	PurgeGeneration       int64               `json:"purgeGeneration"`
	SourceAuthorityDigest string              `json:"sourceAuthorityDigest"`
	ObservedAt            time.Time           `json:"observedAt"`
}

func (fence STRIDELearningAuthorityFence) Validate() error {
	if !validSTRIDELearningScope(fence.SourceScope) || !strideIdentifier(fence.SourceScopeID) || fence.SourceAudience.Validate() != nil ||
		fence.CandidateAudience.Validate() != nil || fence.ACLRevision < 1 || fence.ConsentRevision < 1 || fence.PurgeGeneration < 0 ||
		!isHexDigest(fence.SourceAuthorityDigest) || fence.ObservedAt.IsZero() {
		return ErrSTRIDELearningInvalid
	}
	if !strideLearningAudienceSubset(fence.CandidateAudience, fence.SourceAudience) {
		return ErrSTRIDELearningPrivacy
	}
	if fence.SourceAudience.Visibility == "private" && fence.CandidateAudience.Visibility != "private" {
		return ErrSTRIDELearningPrivacy
	}
	return nil
}

type STRIDEReviewedLearningCandidate struct {
	ID                    string                       `json:"id"`
	TenantID              string                       `json:"tenantId"`
	IdempotencyKeyDigest  string                       `json:"idempotencyKeyDigest"`
	Agent                 string                       `json:"agent"`
	Scope                 STRIDELearningScope          `json:"scope"`
	ScopeID               string                       `json:"scopeId"`
	Impact                string                       `json:"impact"`
	Learning              AgentLearningRecord          `json:"learning"`
	SourceEpisodes        []STRIDEReference            `json:"sourceEpisodes"`
	WorkRuns              []STRIDEReference            `json:"workRuns"`
	Outcomes              []STRIDEReference            `json:"outcomes"`
	Authority             STRIDELearningAuthorityFence `json:"authority"`
	AuthorityFenceDigest  string                       `json:"authorityFenceDigest"`
	SupersedesCandidateID string                       `json:"supersedesCandidateId,omitempty"`
	ProposedBy            string                       `json:"proposedBy"`
	ProposedAt            time.Time                    `json:"proposedAt"`
}

func (candidate STRIDEReviewedLearningCandidate) Validate() error {
	wantFence, fenceErr := candidate.FenceDigest()
	if !strideIdentifier(candidate.ID) || !strideIdentifier(candidate.TenantID) || !isHexDigest(candidate.IdempotencyKeyDigest) ||
		!validSTRIDEWorkAgent(candidate.Agent) || !validSTRIDELearningScope(candidate.Scope) || !strideIdentifier(candidate.ScopeID) ||
		!oneOf(candidate.Impact, "reference", "procedure", "behavior", "profile") || candidate.Learning.Validate() != nil ||
		candidate.Learning.Header.TenantID != candidate.TenantID || candidate.Learning.AgentID != candidate.Agent || STRIDELearningScope(candidate.Learning.Scope) != candidate.Scope || candidate.Learning.Subject != candidate.ScopeID ||
		candidate.Learning.Status != "candidate" || !exactSTRIDELearningRefs(candidate.SourceEpisodes, STRIDEContractSourceEpisode) ||
		!exactSTRIDELearningRefs(candidate.WorkRuns, STRIDEContractWorkRun) || !exactSTRIDELearningRefs(candidate.Outcomes, STRIDEContractOutcome) ||
		!sameSTRIDELearningRefs(candidate.Learning.Evidence, candidate.SourceEpisodes) || !sameSTRIDELearningAudience(candidate.Learning.Audience, candidate.Authority.CandidateAudience) ||
		fenceErr != nil || candidate.AuthorityFenceDigest != wantFence || !validOptionalSTRIDEID(candidate.SupersedesCandidateID) ||
		!validSTRIDELearningProposer(candidate.ProposedBy) || candidate.ProposedAt.IsZero() || candidate.ProposedAt.Before(candidate.Authority.ObservedAt) || candidate.Learning.LastObserved.After(candidate.ProposedAt) {
		return ErrSTRIDELearningInvalid
	}
	if strideLearningScopeRank(candidate.Scope) > strideLearningScopeRank(candidate.Authority.SourceScope) {
		return ErrSTRIDELearningPrivacy
	}
	if candidate.Scope == STRIDELearningAgent && candidate.ScopeID != candidate.Agent {
		return ErrSTRIDELearningInvalid
	}
	if candidate.Scope == STRIDELearningCompany && candidate.ScopeID != candidate.TenantID {
		return ErrSTRIDELearningInvalid
	}
	return nil
}

func (candidate STRIDEReviewedLearningCandidate) FenceDigest() (string, error) {
	if candidate.Authority.Validate() != nil || !strideIdentifier(candidate.ID) || !strideIdentifier(candidate.TenantID) ||
		!validSTRIDEWorkAgent(candidate.Agent) || !validSTRIDELearningScope(candidate.Scope) || !strideIdentifier(candidate.ScopeID) ||
		!isHexDigest(candidate.Learning.LessonDigest) || !exactSTRIDELearningRefs(candidate.SourceEpisodes, STRIDEContractSourceEpisode) ||
		!exactSTRIDELearningRefs(candidate.WorkRuns, STRIDEContractWorkRun) || !exactSTRIDELearningRefs(candidate.Outcomes, STRIDEContractOutcome) {
		return "", ErrSTRIDELearningInvalid
	}
	return STRIDEContractDigest(struct {
		ID             string                       `json:"id"`
		TenantID       string                       `json:"tenantId"`
		Agent          string                       `json:"agent"`
		Scope          STRIDELearningScope          `json:"scope"`
		ScopeID        string                       `json:"scopeId"`
		Impact         string                       `json:"impact"`
		LessonDigest   string                       `json:"lessonDigest"`
		SourceEpisodes []STRIDEReference            `json:"sourceEpisodes"`
		WorkRuns       []STRIDEReference            `json:"workRuns"`
		Outcomes       []STRIDEReference            `json:"outcomes"`
		Authority      STRIDELearningAuthorityFence `json:"authority"`
	}{candidate.ID, candidate.TenantID, candidate.Agent, candidate.Scope, candidate.ScopeID, candidate.Impact, candidate.Learning.LessonDigest,
		candidate.SourceEpisodes, candidate.WorkRuns, candidate.Outcomes, candidate.Authority})
}

func (candidate STRIDEReviewedLearningCandidate) Digest() (string, error) {
	if candidate.Validate() != nil {
		return "", ErrSTRIDELearningInvalid
	}
	return STRIDEContractDigest(candidate)
}

type STRIDELearningEvalMetrics struct {
	Quality    float64 `json:"quality"`
	Citation   float64 `json:"citation"`
	Grounding  float64 `json:"grounding"`
	Privacy    float64 `json:"privacy"`
	CostMicros int64   `json:"costMicros"`
	LatencyMS  int64   `json:"latencyMs"`
}

func (metrics STRIDELearningEvalMetrics) Validate() error {
	if !finiteSTRIDELearningNumber(metrics.Quality) || !finiteSTRIDELearningNumber(metrics.Citation) || !finiteSTRIDELearningNumber(metrics.Grounding) || !finiteSTRIDELearningNumber(metrics.Privacy) ||
		metrics.Quality < 0 || metrics.Quality > 1 || metrics.Citation < 0 || metrics.Citation > 1 || metrics.Grounding < 0 || metrics.Grounding > 1 ||
		metrics.Privacy < 0 || metrics.Privacy > 1 || metrics.CostMicros < 0 || metrics.LatencyMS < 0 {
		return ErrSTRIDELearningInvalid
	}
	return nil
}

type STRIDELearningEvaluationReceipt struct {
	ID              string                    `json:"id"`
	TenantID        string                    `json:"tenantId"`
	CandidateID     string                    `json:"candidateId"`
	CandidateDigest string                    `json:"candidateDigest"`
	Agent           string                    `json:"agent"`
	DatasetDigest   string                    `json:"datasetDigest"`
	HeldOut         bool                      `json:"heldOut"`
	SampleCount     int                       `json:"sampleCount"`
	Before          STRIDELearningEvalMetrics `json:"before"`
	After           STRIDELearningEvalMetrics `json:"after"`
	Evaluator       string                    `json:"evaluator"`
	EvaluatedAt     time.Time                 `json:"evaluatedAt"`
	ReceiptDigest   string                    `json:"receiptDigest"`
}

func (receipt STRIDELearningEvaluationReceipt) Validate() error {
	want, digestErr := receipt.Digest()
	if !strideIdentifier(receipt.ID) || !strideIdentifier(receipt.TenantID) || !strideIdentifier(receipt.CandidateID) || !isHexDigest(receipt.CandidateDigest) ||
		!validSTRIDEWorkAgent(receipt.Agent) || !isHexDigest(receipt.DatasetDigest) || !receipt.HeldOut || receipt.SampleCount < 1 ||
		receipt.Before.Validate() != nil || receipt.After.Validate() != nil || !validSTRIDELearningEvaluator(receipt.Evaluator) || receipt.EvaluatedAt.IsZero() ||
		digestErr != nil || receipt.ReceiptDigest != want {
		return ErrSTRIDELearningInvalid
	}
	return nil
}

func (receipt STRIDELearningEvaluationReceipt) Digest() (string, error) {
	receipt.ReceiptDigest = ""
	return STRIDEContractDigest(receipt)
}

type STRIDELearningActivationPolicy struct {
	MinQualityDelta         float64 `json:"minQualityDelta"`
	MinCitation             float64 `json:"minCitation"`
	MinGrounding            float64 `json:"minGrounding"`
	MinPrivacy              float64 `json:"minPrivacy"`
	MaxCostIncreaseRatio    float64 `json:"maxCostIncreaseRatio"`
	MaxLatencyIncreaseRatio float64 `json:"maxLatencyIncreaseRatio"`
}

func (policy STRIDELearningActivationPolicy) Validate() error {
	if !finiteSTRIDELearningNumber(policy.MinQualityDelta) || !finiteSTRIDELearningNumber(policy.MinCitation) || !finiteSTRIDELearningNumber(policy.MinGrounding) ||
		!finiteSTRIDELearningNumber(policy.MinPrivacy) || !finiteSTRIDELearningNumber(policy.MaxCostIncreaseRatio) || !finiteSTRIDELearningNumber(policy.MaxLatencyIncreaseRatio) ||
		policy.MinQualityDelta <= 0 || policy.MinQualityDelta > 1 || policy.MinCitation < 0 || policy.MinCitation > 1 ||
		policy.MinGrounding < 0 || policy.MinGrounding > 1 || policy.MinPrivacy < 0 || policy.MinPrivacy > 1 ||
		policy.MaxCostIncreaseRatio < 0 || policy.MaxLatencyIncreaseRatio < 0 {
		return ErrSTRIDELearningInvalid
	}
	return nil
}

type STRIDELearningActivationQualification struct {
	ID                  string                         `json:"id"`
	CandidateID         string                         `json:"candidateId"`
	CandidateDigest     string                         `json:"candidateDigest"`
	EvaluationID        string                         `json:"evaluationId"`
	EvaluationDigest    string                         `json:"evaluationDigest"`
	Policy              STRIDELearningActivationPolicy `json:"policy"`
	PolicyDigest        string                         `json:"policyDigest"`
	Eligible            bool                           `json:"eligible"`
	EvaluatedAt         time.Time                      `json:"evaluatedAt"`
	QualificationDigest string                         `json:"qualificationDigest"`
}

func QualifySTRIDELearningActivation(candidate STRIDEReviewedLearningCandidate, receipt STRIDELearningEvaluationReceipt, policy STRIDELearningActivationPolicy, at time.Time) (STRIDELearningActivationQualification, error) {
	candidateDigest, candidateErr := candidate.Digest()
	policyDigest, policyErr := STRIDEContractDigest(policy)
	if candidateErr != nil || receipt.Validate() != nil || policy.Validate() != nil || policyErr != nil || at.IsZero() ||
		receipt.CandidateID != candidate.ID || receipt.CandidateDigest != candidateDigest || receipt.Agent != candidate.Agent || receipt.TenantID != candidate.TenantID {
		return STRIDELearningActivationQualification{}, ErrSTRIDELearningInvalid
	}
	eligible := strideLearningEligible(receipt, policy)
	qualification := STRIDELearningActivationQualification{
		ID: "learning-qualification-" + candidate.ID, CandidateID: candidate.ID, CandidateDigest: candidateDigest,
		EvaluationID: receipt.ID, EvaluationDigest: receipt.ReceiptDigest, Policy: policy, PolicyDigest: policyDigest, Eligible: eligible, EvaluatedAt: at.UTC(),
	}
	qualification.QualificationDigest, _ = qualification.Digest()
	if !eligible {
		return qualification, ErrSTRIDELearningGate
	}
	return qualification, nil
}

func (qualification STRIDELearningActivationQualification) Digest() (string, error) {
	qualification.QualificationDigest = ""
	return STRIDEContractDigest(qualification)
}

func (qualification STRIDELearningActivationQualification) Validate() error {
	want, err := qualification.Digest()
	wantPolicy, policyErr := STRIDEContractDigest(qualification.Policy)
	if !strideIdentifier(qualification.ID) || !strideIdentifier(qualification.CandidateID) || !isHexDigest(qualification.CandidateDigest) ||
		!strideIdentifier(qualification.EvaluationID) || !isHexDigest(qualification.EvaluationDigest) || !isHexDigest(qualification.PolicyDigest) ||
		qualification.Policy.Validate() != nil || policyErr != nil || qualification.PolicyDigest != wantPolicy ||
		!qualification.Eligible || qualification.EvaluatedAt.IsZero() || err != nil || qualification.QualificationDigest != want {
		return ErrSTRIDELearningInvalid
	}
	return nil
}

type STRIDELearningHumanRatification struct {
	ID                  string    `json:"id"`
	CandidateID         string    `json:"candidateId"`
	CandidateDigest     string    `json:"candidateDigest"`
	QualificationID     string    `json:"qualificationId"`
	QualificationDigest string    `json:"qualificationDigest"`
	Decision            string    `json:"decision"`
	RatifiedBy          string    `json:"ratifiedBy"`
	RatifiedAt          time.Time `json:"ratifiedAt"`
}

func (ratification STRIDELearningHumanRatification) Validate() error {
	if !strideIdentifier(ratification.ID) || !strideIdentifier(ratification.CandidateID) || !isHexDigest(ratification.CandidateDigest) ||
		!strideIdentifier(ratification.QualificationID) || !isHexDigest(ratification.QualificationDigest) ||
		!oneOf(ratification.Decision, "approved", "rejected") || !humanSTRIDELearningActor(ratification.RatifiedBy) || ratification.RatifiedAt.IsZero() {
		return ErrSTRIDELearningInvalid
	}
	return nil
}

type STRIDELearningAuditEventType string

const (
	STRIDELearningCandidateProposed   STRIDELearningAuditEventType = "candidate_proposed"
	STRIDELearningEvaluationRecorded  STRIDELearningAuditEventType = "evaluation_recorded"
	STRIDELearningActivationQualified STRIDELearningAuditEventType = "activation_qualified"
	STRIDELearningCandidateReviewed   STRIDELearningAuditEventType = "candidate_reviewed"
	STRIDELearningActivationRatified  STRIDELearningAuditEventType = "activation_ratified"
	STRIDELearningCandidateRejected   STRIDELearningAuditEventType = "candidate_rejected"
	STRIDELearningCandidateCorrected  STRIDELearningAuditEventType = "candidate_corrected"
	STRIDELearningCandidateForgotten  STRIDELearningAuditEventType = "candidate_forgotten"
	STRIDELearningCandidateSuperseded STRIDELearningAuditEventType = "candidate_superseded"
)

type STRIDELearningAuditEvent struct {
	ID                     string                                 `json:"id"`
	TenantID               string                                 `json:"tenantId"`
	Sequence               uint64                                 `json:"sequence"`
	IdempotencyKeyDigest   string                                 `json:"idempotencyKeyDigest"`
	PreviousEventDigest    string                                 `json:"previousEventDigest,omitempty"`
	EventDigest            string                                 `json:"eventDigest"`
	Type                   STRIDELearningAuditEventType           `json:"type"`
	CandidateID            string                                 `json:"candidateId"`
	TargetCandidateID      string                                 `json:"targetCandidateId,omitempty"`
	ReplacementCandidateID string                                 `json:"replacementCandidateId,omitempty"`
	ActorKind              string                                 `json:"actorKind"`
	Actor                  string                                 `json:"actor"`
	Summary                string                                 `json:"summary"`
	Candidate              *STRIDEReviewedLearningCandidate       `json:"candidate,omitempty"`
	Evaluation             *STRIDELearningEvaluationReceipt       `json:"evaluation,omitempty"`
	Qualification          *STRIDELearningActivationQualification `json:"qualification,omitempty"`
	Ratification           *STRIDELearningHumanRatification       `json:"ratification,omitempty"`
	OccurredAt             time.Time                              `json:"occurredAt"`
}

func (event STRIDELearningAuditEvent) validateCommand() error {
	if !strideIdentifier(event.ID) || !strideIdentifier(event.TenantID) || !isHexDigest(event.IdempotencyKeyDigest) || !validSTRIDELearningAuditType(event.Type) ||
		!strideIdentifier(event.CandidateID) || !oneOf(event.ActorKind, "human", "system") || !strideIdentifier(event.Actor) ||
		!humanActivitySummary(event.Summary) || event.OccurredAt.IsZero() || !validOptionalSTRIDEID(event.TargetCandidateID) || !validOptionalSTRIDEID(event.ReplacementCandidateID) {
		return ErrSTRIDELearningInvalid
	}
	if event.ActorKind == "human" && !humanSTRIDELearningActor(event.Actor) || event.ActorKind == "system" && !strings.HasPrefix(event.Actor, "system-") {
		return ErrSTRIDELearningInvalid
	}
	switch event.Type {
	case STRIDELearningCandidateProposed:
		if event.Candidate == nil || event.Candidate.Validate() != nil || event.Candidate.ID != event.CandidateID || event.Candidate.TenantID != event.TenantID {
			return ErrSTRIDELearningInvalid
		}
	case STRIDELearningEvaluationRecorded:
		if event.Evaluation == nil || event.Evaluation.Validate() != nil || event.Evaluation.CandidateID != event.CandidateID || event.Evaluation.TenantID != event.TenantID {
			return ErrSTRIDELearningInvalid
		}
	case STRIDELearningActivationQualified:
		if event.Qualification == nil || event.Qualification.Validate() != nil || event.Qualification.CandidateID != event.CandidateID {
			return ErrSTRIDELearningInvalid
		}
	case STRIDELearningActivationRatified:
		if event.ActorKind != "human" || event.Ratification == nil || event.Ratification.Validate() != nil || event.Ratification.CandidateID != event.CandidateID || event.Ratification.RatifiedBy != event.Actor {
			return ErrSTRIDELearningInvalid
		}
	case STRIDELearningCandidateReviewed, STRIDELearningCandidateRejected, STRIDELearningCandidateForgotten, STRIDELearningCandidateSuperseded:
		if event.ActorKind != "human" {
			return ErrSTRIDELearningInvalid
		}
	case STRIDELearningCandidateCorrected:
		if event.ActorKind != "human" || event.Candidate == nil || event.Candidate.Validate() != nil || event.TargetCandidateID == "" || event.Candidate.ID != event.CandidateID || event.Candidate.SupersedesCandidateID != event.TargetCandidateID {
			return ErrSTRIDELearningInvalid
		}
	}
	if event.Type != STRIDELearningCandidateProposed && event.Type != STRIDELearningCandidateCorrected && event.Candidate != nil ||
		event.Type != STRIDELearningEvaluationRecorded && event.Evaluation != nil || event.Type != STRIDELearningActivationQualified && event.Qualification != nil ||
		event.Type != STRIDELearningActivationRatified && event.Ratification != nil {
		return ErrSTRIDELearningInvalid
	}
	return nil
}

func (event STRIDELearningAuditEvent) digest() (string, error) {
	event.EventDigest = ""
	return STRIDEContractDigest(event)
}

type STRIDELearningCandidateState struct {
	Candidate     STRIDEReviewedLearningCandidate        `json:"candidate"`
	Status        string                                 `json:"status"`
	Evaluation    *STRIDELearningEvaluationReceipt       `json:"evaluation,omitempty"`
	Qualification *STRIDELearningActivationQualification `json:"qualification,omitempty"`
	Ratification  *STRIDELearningHumanRatification       `json:"ratification,omitempty"`
	Audit         []STRIDELearningAuditEvent             `json:"audit"`
}

type STRIDELearningAuditRepository struct {
	mu          sync.Mutex
	path        string
	events      []STRIDELearningAuditEvent
	idempotency map[string]STRIDELearningAuditEvent
	write       func(string, []byte) error
	poisoned    error
}

func NewSTRIDELearningAuditRepository(path string) (*STRIDELearningAuditRepository, error) {
	repository := &STRIDELearningAuditRepository{path: strings.TrimSpace(path), idempotency: map[string]STRIDELearningAuditEvent{}, write: func(path string, raw []byte) error { return appendFileDurably(path, raw, 0o600) }}
	if repository.path != "" {
		events, idempotency, err := loadSTRIDELearningAudit(repository.path)
		if err != nil {
			return nil, err
		}
		repository.events, repository.idempotency = events, idempotency
	}
	return repository, nil
}

func (repository *STRIDELearningAuditRepository) Append(command STRIDELearningAuditEvent) (STRIDELearningAuditEvent, bool, error) {
	if repository == nil || command.Sequence != 0 || command.EventDigest != "" || command.PreviousEventDigest != "" || command.validateCommand() != nil {
		return STRIDELearningAuditEvent{}, false, ErrSTRIDELearningInvalid
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.poisoned != nil {
		return STRIDELearningAuditEvent{}, false, ErrSTRIDELearningUnavailable
	}
	if existing, found := repository.idempotency[command.IdempotencyKeyDigest]; found {
		if sameSTRIDELearningCommand(existing, command) {
			return existing, false, nil
		}
		return STRIDELearningAuditEvent{}, false, ErrSTRIDELearningConflict
	}
	states, err := reduceSTRIDELearningAudit(repository.events)
	if err != nil {
		return STRIDELearningAuditEvent{}, false, err
	}
	if err := applySTRIDELearningAudit(states, command); err != nil {
		return STRIDELearningAuditEvent{}, false, err
	}
	command.Sequence = uint64(len(repository.events) + 1)
	if len(repository.events) > 0 {
		command.PreviousEventDigest = repository.events[len(repository.events)-1].EventDigest
	}
	command.EventDigest, _ = command.digest()
	if repository.path != "" {
		raw, _ := json.Marshal(command)
		if err := repository.write(repository.path, append(raw, '\n')); err != nil {
			repository.poisoned = err
			return STRIDELearningAuditEvent{}, false, err
		}
	}
	repository.events = append(repository.events, command)
	repository.idempotency[command.IdempotencyKeyDigest] = command
	return command, true, nil
}

func (repository *STRIDELearningAuditRepository) State(candidateID string) (STRIDELearningCandidateState, error) {
	if repository == nil || !strideIdentifier(candidateID) {
		return STRIDELearningCandidateState{}, ErrSTRIDELearningInvalid
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.poisoned != nil {
		return STRIDELearningCandidateState{}, ErrSTRIDELearningUnavailable
	}
	states, err := reduceSTRIDELearningAudit(repository.events)
	if err != nil {
		return STRIDELearningCandidateState{}, err
	}
	state, found := states[candidateID]
	if !found {
		return STRIDELearningCandidateState{}, ErrSTRIDELearningAbsent
	}
	return state, nil
}

func reduceSTRIDELearningAudit(events []STRIDELearningAuditEvent) (map[string]STRIDELearningCandidateState, error) {
	states := map[string]STRIDELearningCandidateState{}
	for _, event := range events {
		if err := applySTRIDELearningAudit(states, event); err != nil {
			return nil, err
		}
	}
	return states, nil
}

func applySTRIDELearningAudit(states map[string]STRIDELearningCandidateState, event STRIDELearningAuditEvent) error {
	state, found := states[event.CandidateID]
	switch event.Type {
	case STRIDELearningCandidateProposed:
		if found {
			return ErrSTRIDELearningConflict
		}
		state = STRIDELearningCandidateState{Candidate: *event.Candidate, Status: "candidate"}
	case STRIDELearningCandidateCorrected:
		target, targetFound := states[event.TargetCandidateID]
		if found || !targetFound || oneOf(target.Status, "forgotten", "superseded") || target.Candidate.Agent != event.Candidate.Agent ||
			strideLearningScopeRank(event.Candidate.Scope) > strideLearningScopeRank(target.Candidate.Scope) ||
			!strideLearningAudienceSubset(event.Candidate.Authority.CandidateAudience, target.Candidate.Authority.CandidateAudience) {
			return ErrSTRIDELearningConflict
		}
		target.Status = "corrected"
		target.Audit = append(target.Audit, event)
		states[event.TargetCandidateID] = target
		state = STRIDELearningCandidateState{Candidate: *event.Candidate, Status: "candidate"}
	case STRIDELearningEvaluationRecorded:
		if !found || state.Status != "candidate" || state.Evaluation != nil || state.Qualification != nil {
			return ErrSTRIDELearningConflict
		}
		candidateDigest, _ := state.Candidate.Digest()
		if event.Evaluation.CandidateDigest != candidateDigest || event.Evaluation.Agent != state.Candidate.Agent {
			return ErrSTRIDELearningConflict
		}
		copy := *event.Evaluation
		state.Evaluation = &copy
	case STRIDELearningActivationQualified:
		candidateDigest, _ := state.Candidate.Digest()
		if !found || state.Evaluation == nil || state.Qualification != nil || event.Qualification.CandidateDigest != candidateDigest ||
			event.Qualification.EvaluationID != state.Evaluation.ID || event.Qualification.EvaluationDigest != state.Evaluation.ReceiptDigest ||
			event.Qualification.EvaluatedAt.Before(state.Evaluation.EvaluatedAt) || !strideLearningEligible(*state.Evaluation, event.Qualification.Policy) {
			return ErrSTRIDELearningConflict
		}
		copy := *event.Qualification
		state.Qualification = &copy
	case STRIDELearningCandidateReviewed:
		if !found || state.Qualification == nil || state.Status != "candidate" {
			return ErrSTRIDELearningConflict
		}
		state.Status = "reviewed"
	case STRIDELearningActivationRatified:
		candidateDigest, _ := state.Candidate.Digest()
		if !found || state.Status != "reviewed" || state.Qualification == nil || event.Ratification.QualificationID != state.Qualification.ID ||
			event.Ratification.QualificationDigest != state.Qualification.QualificationDigest || event.Ratification.CandidateDigest != candidateDigest {
			return ErrSTRIDELearningConflict
		}
		copy := *event.Ratification
		state.Ratification = &copy
		if copy.Decision == "approved" {
			state.Status = "active"
		} else {
			state.Status = "rejected"
		}
	case STRIDELearningCandidateRejected:
		if !found || oneOf(state.Status, "forgotten", "superseded") {
			return ErrSTRIDELearningConflict
		}
		state.Status = "rejected"
	case STRIDELearningCandidateForgotten:
		if !found || state.Status == "forgotten" {
			return ErrSTRIDELearningConflict
		}
		state.Status, state.Ratification = "forgotten", nil
	case STRIDELearningCandidateSuperseded:
		replacement, replacementFound := states[event.ReplacementCandidateID]
		if !found || !replacementFound || state.Status == "forgotten" || replacement.Candidate.SupersedesCandidateID != event.CandidateID {
			return ErrSTRIDELearningConflict
		}
		state.Status = "superseded"
	}
	state.Audit = append(state.Audit, event)
	states[event.CandidateID] = state
	return nil
}

func loadSTRIDELearningAudit(path string) ([]STRIDELearningAuditEvent, map[string]STRIDELearningAuditEvent, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, map[string]STRIDELearningAuditEvent{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var events []STRIDELearningAuditEvent
	idempotency := map[string]STRIDELearningAuditEvent{}
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			if errors.Is(readErr, io.EOF) || len(line) > strideLearningAuditMaxEventBytes {
				return nil, nil, ErrSTRIDELearningInvalid
			}
			decoder := json.NewDecoder(bytes.NewReader(line))
			decoder.DisallowUnknownFields()
			var event STRIDELearningAuditEvent
			if decoder.Decode(&event) != nil || ensureJSONEOF(decoder) != nil || event.validateCommand() != nil || event.Sequence != uint64(len(events)+1) ||
				!isHexDigest(event.EventDigest) || len(events) == 0 && event.PreviousEventDigest != "" || len(events) > 0 && event.PreviousEventDigest != events[len(events)-1].EventDigest {
				return nil, nil, ErrSTRIDELearningInvalid
			}
			want, _ := event.digest()
			if want != event.EventDigest {
				return nil, nil, ErrSTRIDELearningInvalid
			}
			if _, duplicate := idempotency[event.IdempotencyKeyDigest]; duplicate {
				return nil, nil, ErrSTRIDELearningConflict
			}
			candidate := append(append([]STRIDELearningAuditEvent(nil), events...), event)
			if _, err := reduceSTRIDELearningAudit(candidate); err != nil {
				return nil, nil, err
			}
			events = candidate
			idempotency[event.IdempotencyKeyDigest] = event
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, nil, readErr
		}
	}
	return events, idempotency, nil
}

func sameSTRIDELearningCommand(left, right STRIDELearningAuditEvent) bool {
	left.Sequence, right.Sequence = 0, 0
	left.PreviousEventDigest, right.PreviousEventDigest = "", ""
	left.EventDigest, right.EventDigest = "", ""
	leftRaw, leftErr := canonicalJSON(left)
	rightRaw, rightErr := canonicalJSON(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftRaw, rightRaw)
}

func exactSTRIDELearningRefs(refs []STRIDEReference, kind STRIDEContractType) bool {
	if len(refs) == 0 || !validateSTRIDERefs(refs) {
		return false
	}
	for _, ref := range refs {
		if ref.ContractType != kind {
			return false
		}
	}
	return true
}

func sameSTRIDELearningRefs(left, right []STRIDEReference) bool {
	leftRaw, leftErr := canonicalJSON(left)
	rightRaw, rightErr := canonicalJSON(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftRaw, rightRaw)
}

func sameSTRIDELearningAudience(left, right STRIDEAudience) bool {
	leftRaw, leftErr := canonicalJSON(left)
	rightRaw, rightErr := canonicalJSON(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftRaw, rightRaw)
}

func validSTRIDELearningScope(scope STRIDELearningScope) bool {
	return scope == STRIDELearningCompany || scope == STRIDELearningProject || scope == STRIDELearningPerson || scope == STRIDELearningAgent
}

func strideLearningScopeRank(scope STRIDELearningScope) int {
	switch scope {
	case STRIDELearningPerson, STRIDELearningAgent:
		return 0
	case STRIDELearningProject:
		return 1
	case STRIDELearningCompany:
		return 2
	}
	return 99
}

func strideLearningAudienceSubset(candidate, source STRIDEAudience) bool {
	if candidate.Visibility != source.Visibility {
		return false
	}
	sourcePrincipals := map[string]bool{}
	for _, principal := range source.Principals {
		sourcePrincipals[principal] = true
	}
	for _, principal := range candidate.Principals {
		if !sourcePrincipals[principal] {
			return false
		}
	}
	return true
}

func validSTRIDELearningProposer(actor string) bool {
	return humanSTRIDELearningActor(actor) || strings.HasPrefix(actor, "system-")
}

func validSTRIDELearningEvaluator(actor string) bool {
	return humanSTRIDELearningActor(actor) || strings.HasPrefix(actor, "system-eval-")
}

func humanSTRIDELearningActor(actor string) bool {
	if !strideIdentifier(actor) || validSTRIDEWorkAgent(actor) {
		return false
	}
	for _, prefix := range []string{"system-", "provider-", "model-", "runtime-", "agent-"} {
		if strings.HasPrefix(actor, prefix) {
			return false
		}
	}
	return true
}

func withinSTRIDELearningIncrease(before, after int64, allowed float64) bool {
	if before == 0 {
		return after == 0
	}
	return float64(after) <= float64(before)*(1+allowed)
}

func finiteSTRIDELearningNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func strideLearningEligible(receipt STRIDELearningEvaluationReceipt, policy STRIDELearningActivationPolicy) bool {
	return receipt.Validate() == nil && policy.Validate() == nil && receipt.After.Privacy >= receipt.Before.Privacy && receipt.After.Grounding >= receipt.Before.Grounding &&
		receipt.After.Privacy >= policy.MinPrivacy && receipt.After.Grounding >= policy.MinGrounding && receipt.After.Citation >= policy.MinCitation &&
		receipt.After.Quality-receipt.Before.Quality >= policy.MinQualityDelta &&
		withinSTRIDELearningIncrease(receipt.Before.CostMicros, receipt.After.CostMicros, policy.MaxCostIncreaseRatio) &&
		withinSTRIDELearningIncrease(receipt.Before.LatencyMS, receipt.After.LatencyMS, policy.MaxLatencyIncreaseRatio)
}

func validSTRIDELearningAuditType(eventType STRIDELearningAuditEventType) bool {
	switch eventType {
	case STRIDELearningCandidateProposed, STRIDELearningEvaluationRecorded, STRIDELearningActivationQualified,
		STRIDELearningCandidateReviewed, STRIDELearningActivationRatified, STRIDELearningCandidateRejected,
		STRIDELearningCandidateCorrected, STRIDELearningCandidateForgotten, STRIDELearningCandidateSuperseded:
		return true
	}
	return false
}
