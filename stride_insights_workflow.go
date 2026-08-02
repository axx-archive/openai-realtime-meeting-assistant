package main

// This file is the token-free E7 qualification path for
// insights_opportunities_v1. It is intentionally separate from the existing
// provider-backed executor: no interface here can call a provider, reach the
// network, or publish outside the workflow's in-memory internal artifact map.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

const (
	StrideInsightsWorkflowVersion   = 1
	StrideInsightsRequestSchema     = "stride_insights_request_v1"
	StrideInsightsEvidenceSchema    = "stride_insights_evidence_v1"
	StrideInsightsOpportunitySchema = "stride_insights_opportunity_v1"
	StrideInsightsReportSchema      = "stride_insights_report_v1"
	StrideInsightsCriticSchema      = "stride_insights_critic_v1"
	StrideInsightsFeedbackSchema    = "stride_insights_feedback_v1"
	StrideInsightsArtifactSchema    = "stride_insights_artifact_v1"
	StrideInsightsOutcomeSchema     = "stride_insights_outcome_v1"
	StrideInsightsPilotSchema       = "stride_insights_pilot_v1"

	StrideInsightsStatusRunning  = "running"
	StrideInsightsStatusBlocked  = "blocked"
	StrideInsightsStatusRejected = "rejected"
	StrideInsightsStatusAccepted = "accepted"
)

var (
	ErrStrideInsightsInvalid       = errors.New("stride insights workflow record is invalid")
	ErrStrideInsightsUnauthorized  = errors.New("stride insights workflow is unauthorized")
	ErrStrideInsightsConflict      = errors.New("stride insights workflow conflicts with durable state")
	ErrStrideInsightsInjectedCrash = errors.New("stride insights deterministic crash")
	ErrStrideInsightsCriticLimit   = errors.New("stride insights critic revision limit reached")
	ErrStrideInsightsPackageGate   = errors.New("stride insights package gate is not satisfied")
)

type StrideInsightsBinding struct {
	Profile    STRIDEReference `json:"profile"`
	Capability STRIDEReference `json:"capability"`
	Runtime    STRIDEReference `json:"runtime"`
	WorkRun    STRIDEReference `json:"workRun"`
}

func (binding StrideInsightsBinding) Validate() error {
	if binding.Profile.Validate() != nil || binding.Profile.ContractType != STRIDEContractAgentCoreProfile ||
		binding.Capability.Validate() != nil || binding.Capability.ContractType != STRIDEContractAgentCapabilityManifest ||
		binding.Runtime.Validate() != nil || binding.WorkRun.Validate() != nil || binding.WorkRun.ContractType != STRIDEContractWorkRun {
		return ErrStrideInsightsInvalid
	}
	return nil
}

type StrideInsightsAnalystIdentity struct {
	Profile       AgentCoreProfile `json:"profile"`
	Capability    STRIDEReference  `json:"capability"`
	Runtime       STRIDEReference  `json:"runtime"`
	HumanAuthorID string           `json:"humanAuthorId"`
}

func (identity StrideInsightsAnalystIdentity) Validate() error {
	if identity.Profile.Validate() != nil || identity.Profile.DisplayName != "Insights Analyst" || identity.Profile.Role != "insights_analyst" ||
		!strideIdentifier(identity.HumanAuthorID) || identity.Capability.Validate() != nil || identity.Capability.ContractType != STRIDEContractAgentCapabilityManifest || identity.Runtime.Validate() != nil {
		return ErrStrideInsightsInvalid
	}
	return nil
}

func NewStrideInsightsAnalystIdentity(tenantID, humanAuthorID string, capability, runtime STRIDEReference, at time.Time) (StrideInsightsAnalystIdentity, error) {
	identity := StrideInsightsAnalystIdentity{Capability: capability, Runtime: runtime, HumanAuthorID: humanAuthorID}
	profile := AgentCoreProfile{
		Header: STRIDEContractHeader{TenantID: tenantID, ID: "insights-analyst-v1", Revision: 1, SchemaVersion: STRIDEContractSchemaVersion,
			ContractType: STRIDEContractAgentCoreProfile, ContentDigest: temporalDigest("insights-analyst-v1:" + humanAuthorID), CreatedAt: at.UTC()},
		AgentID: "insights-analyst", DisplayName: "Insights Analyst", Pronunciation: "insights-analyst", Role: "insights_analyst",
		MissionDigest: temporalDigest("surface evidence-backed opportunities"), StyleDigest: temporalDigest("clear skeptical decision-ready"),
		Traits: []string{"evidence_first"}, HumorRange: "none", Values: []string{"accuracy"}, Boundaries: []string{"internal_only"},
		Prohibited: []string{"external_write"}, EscalationPolicy: "human_review", Owner: humanAuthorID, Status: "active",
	}
	identity.Profile = profile
	if identity.Validate() != nil {
		return StrideInsightsAnalystIdentity{}, ErrStrideInsightsInvalid
	}
	return identity, nil
}

func (identity StrideInsightsAnalystIdentity) Binding(workRun STRIDEReference) StrideInsightsBinding {
	return StrideInsightsBinding{Profile: referenceFromHeader(identity.Profile.Header), Capability: identity.Capability, Runtime: identity.Runtime, WorkRun: workRun}
}

type StrideInsightsEvidenceSnapshot struct {
	Schema         string                    `json:"schema"`
	Snapshot       RetrievalSnapshot         `json:"snapshot"`
	Sources        []RetrievalSnapshotSource `json:"sources"`
	ManifestDigest string                    `json:"manifestDigest"`
}

func NewStrideInsightsEvidenceSnapshot(snapshot RetrievalSnapshot) (StrideInsightsEvidenceSnapshot, error) {
	result := StrideInsightsEvidenceSnapshot{Schema: StrideInsightsEvidenceSchema, Snapshot: snapshot, Sources: append([]RetrievalSnapshotSource(nil), snapshot.Sources...)}
	result.ManifestDigest = ""
	raw, err := canonicalJSON(struct {
		SnapshotID string                    `json:"snapshotId"`
		Sources    []RetrievalSnapshotSource `json:"sources"`
	}{snapshot.SnapshotID, result.Sources})
	if err != nil {
		return result, err
	}
	result.ManifestDigest = temporalDigestBytes(raw)
	if result.Validate() != nil {
		return StrideInsightsEvidenceSnapshot{}, ErrStrideInsightsInvalid
	}
	return result, nil
}

func DecodeStrideInsightsEvidenceSnapshot(raw []byte) (StrideInsightsEvidenceSnapshot, error) {
	var result StrideInsightsEvidenceSnapshot
	if strideInsightsDecode(raw, &result) != nil || result.Validate() != nil {
		return StrideInsightsEvidenceSnapshot{}, ErrStrideInsightsInvalid
	}
	return result, nil
}

func (evidence StrideInsightsEvidenceSnapshot) Validate() error {
	if evidence.Schema != StrideInsightsEvidenceSchema || evidence.Snapshot.Validate() != nil || !reflectSnapshotSources(evidence.Sources, evidence.Snapshot.Sources) || !isHexDigest(evidence.ManifestDigest) {
		return ErrStrideInsightsInvalid
	}
	copy := evidence
	copy.ManifestDigest = ""
	raw, err := canonicalJSON(struct {
		SnapshotID string                    `json:"snapshotId"`
		Sources    []RetrievalSnapshotSource `json:"sources"`
	}{copy.Snapshot.SnapshotID, copy.Sources})
	if err != nil || temporalDigestBytes(raw) != evidence.ManifestDigest {
		return ErrStrideInsightsInvalid
	}
	return nil
}

type StrideInsightsRequest struct {
	Schema              string                         `json:"schema"`
	WorkflowVersion     int                            `json:"workflowVersion"`
	RequestID           string                         `json:"requestId"`
	RequestRevision     int                            `json:"requestRevision"`
	RunID               string                         `json:"runId"`
	TenantID            string                         `json:"tenantId"`
	PrincipalID         string                         `json:"principalId"`
	Goal                string                         `json:"goal"`
	InternalDestination string                         `json:"internalDestination"`
	Binding             StrideInsightsBinding          `json:"binding"`
	Evidence            StrideInsightsEvidenceSnapshot `json:"evidence"`
	ManifestDigest      string                         `json:"manifestDigest"`
	ParentRunID         string                         `json:"parentRunId,omitempty"`
	ParentReportDigest  string                         `json:"parentReportDigest,omitempty"`
	RequestDigest       string                         `json:"requestDigest"`
}

func (request StrideInsightsRequest) Validate() error {
	if request.Schema != StrideInsightsRequestSchema || request.WorkflowVersion != StrideInsightsWorkflowVersion || !strideIdentifier(request.RequestID) || request.RequestRevision < 1 ||
		!strideIdentifier(request.RunID) || !strideIdentifier(request.TenantID) || !strideIdentifier(request.PrincipalID) || strings.TrimSpace(request.Goal) == "" ||
		!strings.HasPrefix(request.InternalDestination, "workspace:") || strings.Contains(strings.ToLower(request.InternalDestination), "external") || request.Binding.Validate() != nil ||
		request.Evidence.Validate() != nil || request.Evidence.Snapshot.TenantID != request.TenantID || request.Evidence.Snapshot.PrincipalID != request.PrincipalID ||
		!isHexDigest(request.ManifestDigest) || !validOptionalSTRIDEID(request.ParentRunID) || !validOptionalDigest(request.ParentReportDigest) || !isHexDigest(request.RequestDigest) {
		return ErrStrideInsightsInvalid
	}
	if (request.ParentRunID == "") != (request.ParentReportDigest == "") || (request.RequestRevision == 1) != (request.ParentRunID == "") {
		return ErrStrideInsightsInvalid
	}
	want, err := strideInsightsRequestDigest(request)
	if err != nil || want != request.RequestDigest {
		return ErrStrideInsightsInvalid
	}
	return nil
}

func DecodeStrideInsightsRequest(raw []byte) (StrideInsightsRequest, error) {
	var result StrideInsightsRequest
	if err := strideInsightsDecode(raw, &result); err != nil || result.Validate() != nil {
		return StrideInsightsRequest{}, ErrStrideInsightsInvalid
	}
	return result, nil
}

func strideInsightsRequestDigest(request StrideInsightsRequest) (string, error) {
	request.RequestDigest = ""
	raw, err := canonicalJSON(request)
	if err != nil {
		return "", err
	}
	return temporalDigestBytes(raw), nil
}

type StrideInsightsClaim struct {
	ClaimID            string   `json:"claimId"`
	Statement          string   `json:"statement"`
	EvidenceIDs        []string `json:"evidenceIds"`
	CounterevidenceIDs []string `json:"counterevidenceIds,omitempty"`
	Confidence         float64  `json:"confidence"`
	Impact             string   `json:"impact"`
	NextAction         string   `json:"nextAction"`
	Owner              string   `json:"owner"`
	DecisionStatus     string   `json:"decisionStatus"`
}

func (claim StrideInsightsClaim) Validate(evidence map[string]bool) error {
	if !strideIdentifier(claim.ClaimID) || strings.TrimSpace(claim.Statement) == "" || len(claim.EvidenceIDs) == 0 || claim.Confidence < 0 || claim.Confidence > 1 ||
		strings.TrimSpace(claim.Impact) == "" || strings.TrimSpace(claim.NextAction) == "" || !strideIdentifier(claim.Owner) ||
		!oneOf(claim.DecisionStatus, insightsDecisionProposed, insightsDecisionAccepted, insightsDecisionRejected, insightsDecisionDeferred) || !uniqueTextIDs(claim.EvidenceIDs) || !validOptionalTextIDs(claim.CounterevidenceIDs) {
		return ErrStrideInsightsInvalid
	}
	for _, id := range append(append([]string(nil), claim.EvidenceIDs...), claim.CounterevidenceIDs...) {
		if !evidence[id] {
			return fmt.Errorf("%w: claim %s invented evidence %s", ErrStrideInsightsInvalid, claim.ClaimID, id)
		}
	}
	return nil
}

type StrideInsightsOpportunity struct {
	Schema         string   `json:"schema"`
	OpportunityID  string   `json:"opportunityId"`
	Title          string   `json:"title"`
	ClaimIDs       []string `json:"claimIds"`
	Impact         string   `json:"impact"`
	NextAction     string   `json:"nextAction"`
	Owner          string   `json:"owner"`
	DecisionStatus string   `json:"decisionStatus"`
}

type StrideInsightsReport struct {
	Schema                 string                      `json:"schema"`
	ReportID               string                      `json:"reportId"`
	Revision               int                         `json:"revision"`
	ParentReportDigest     string                      `json:"parentReportDigest,omitempty"`
	RunID                  string                      `json:"runId"`
	RequestDigest          string                      `json:"requestDigest"`
	EvidenceManifestDigest string                      `json:"evidenceManifestDigest"`
	ManifestDigest         string                      `json:"manifestDigest"`
	Binding                StrideInsightsBinding       `json:"binding"`
	Summary                string                      `json:"summary"`
	Claims                 []StrideInsightsClaim       `json:"claims"`
	Opportunities          []StrideInsightsOpportunity `json:"opportunities"`
	GeneratedAt            time.Time                   `json:"generatedAt"`
	ReportDigest           string                      `json:"reportDigest"`
}

func (report StrideInsightsReport) Validate(request StrideInsightsRequest) error {
	evidence := map[string]bool{}
	for _, source := range request.Evidence.Sources {
		evidence[source.EvidenceID] = true
	}
	if report.Schema != StrideInsightsReportSchema || !strideIdentifier(report.ReportID) || report.Revision < 1 || report.RunID != request.RunID ||
		report.RequestDigest != request.RequestDigest || report.EvidenceManifestDigest != request.Evidence.ManifestDigest || report.ManifestDigest != request.ManifestDigest ||
		report.Binding != request.Binding || strings.TrimSpace(report.Summary) == "" || len(report.Claims) == 0 || len(report.Opportunities) == 0 || report.GeneratedAt.IsZero() || !isHexDigest(report.ReportDigest) {
		return ErrStrideInsightsInvalid
	}
	if (report.Revision == 1 && report.ParentReportDigest != "") || (report.Revision > 1 && !isHexDigest(report.ParentReportDigest)) {
		return ErrStrideInsightsInvalid
	}
	claimIDs := map[string]bool{}
	for _, claim := range report.Claims {
		if claim.Validate(evidence) != nil || claimIDs[claim.ClaimID] {
			return ErrStrideInsightsInvalid
		}
		claimIDs[claim.ClaimID] = true
	}
	seenOpportunities := map[string]bool{}
	for _, opportunity := range report.Opportunities {
		if opportunity.Schema != StrideInsightsOpportunitySchema || !strideIdentifier(opportunity.OpportunityID) || seenOpportunities[opportunity.OpportunityID] || strings.TrimSpace(opportunity.Title) == "" || len(opportunity.ClaimIDs) == 0 ||
			!uniqueTextIDs(opportunity.ClaimIDs) || strings.TrimSpace(opportunity.Impact) == "" || strings.TrimSpace(opportunity.NextAction) == "" || !strideIdentifier(opportunity.Owner) ||
			!oneOf(opportunity.DecisionStatus, insightsDecisionProposed, insightsDecisionAccepted, insightsDecisionRejected, insightsDecisionDeferred) {
			return ErrStrideInsightsInvalid
		}
		for _, id := range opportunity.ClaimIDs {
			if !claimIDs[id] {
				return ErrStrideInsightsInvalid
			}
		}
		seenOpportunities[opportunity.OpportunityID] = true
	}
	want, err := strideInsightsReportDigest(report)
	if err != nil || want != report.ReportDigest {
		return ErrStrideInsightsInvalid
	}
	return nil
}

func strideInsightsReportDigest(report StrideInsightsReport) (string, error) {
	report.ReportDigest = ""
	raw, err := canonicalJSON(report)
	if err != nil {
		return "", err
	}
	return temporalDigestBytes(raw), nil
}

func DecodeStrideInsightsReport(raw []byte, request StrideInsightsRequest) (StrideInsightsReport, error) {
	var result StrideInsightsReport
	if strideInsightsDecode(raw, &result) != nil || result.Validate(request) != nil {
		return StrideInsightsReport{}, ErrStrideInsightsInvalid
	}
	return result, nil
}

type StrideInsightsCriticFinding struct {
	Criterion      string   `json:"criterion"`
	ClaimID        string   `json:"claimId"`
	Verdict        string   `json:"verdict"`
	EvidenceIDs    []string `json:"evidenceIds,omitempty"`
	RequiredAction string   `json:"requiredAction,omitempty"`
}

type StrideInsightsCriticVerdict struct {
	Schema        string                        `json:"schema"`
	VerdictID     string                        `json:"verdictId"`
	RunID         string                        `json:"runId"`
	ReportDigest  string                        `json:"reportDigest"`
	Round         int                           `json:"round"`
	MaxRounds     int                           `json:"maxRounds"`
	Outcome       string                        `json:"outcome"`
	Findings      []StrideInsightsCriticFinding `json:"findings"`
	Binding       StrideInsightsBinding         `json:"binding"`
	VerdictDigest string                        `json:"verdictDigest"`
}

func (verdict StrideInsightsCriticVerdict) Validate(report StrideInsightsReport) error {
	if verdict.Schema != StrideInsightsCriticSchema || !strideIdentifier(verdict.VerdictID) || verdict.RunID != report.RunID || verdict.ReportDigest != report.ReportDigest ||
		verdict.Round < 1 || verdict.MaxRounds < 1 || verdict.Round > verdict.MaxRounds || verdict.MaxRounds > 2 || !oneOf(verdict.Outcome, insightsCriticAccept, insightsCriticRevise, insightsCriticReject) ||
		len(verdict.Findings) == 0 || verdict.Binding != report.Binding || !isHexDigest(verdict.VerdictDigest) {
		return ErrStrideInsightsInvalid
	}
	claims := map[string]map[string]bool{}
	for _, claim := range report.Claims {
		claims[claim.ClaimID] = map[string]bool{}
		for _, id := range append(append([]string(nil), claim.EvidenceIDs...), claim.CounterevidenceIDs...) {
			claims[claim.ClaimID][id] = true
		}
	}
	coveredClaims := map[string]bool{}
	for _, finding := range verdict.Findings {
		knownEvidence, knownClaim := claims[finding.ClaimID]
		if !strideIdentifier(finding.Criterion) || !knownClaim || !oneOf(finding.Verdict, insightsCriticAccept, insightsCriticRevise, insightsCriticReject) || !validOptionalTextIDs(finding.EvidenceIDs) ||
			(finding.Verdict != insightsCriticAccept && strings.TrimSpace(finding.RequiredAction) == "") {
			return ErrStrideInsightsInvalid
		}
		if finding.Verdict == insightsCriticAccept && len(finding.EvidenceIDs) == 0 {
			return ErrStrideInsightsInvalid
		}
		for _, id := range finding.EvidenceIDs {
			if !knownEvidence[id] {
				return ErrStrideInsightsInvalid
			}
		}
		coveredClaims[finding.ClaimID] = true
	}
	if len(coveredClaims) != len(claims) {
		return ErrStrideInsightsInvalid
	}
	copy := verdict
	copy.VerdictDigest = ""
	raw, _ := canonicalJSON(copy)
	if temporalDigestBytes(raw) != verdict.VerdictDigest {
		return ErrStrideInsightsInvalid
	}
	return nil
}

func DecodeStrideInsightsCriticVerdict(raw []byte, report StrideInsightsReport) (StrideInsightsCriticVerdict, error) {
	var result StrideInsightsCriticVerdict
	if strideInsightsDecode(raw, &result) != nil || result.Validate(report) != nil {
		return StrideInsightsCriticVerdict{}, ErrStrideInsightsInvalid
	}
	return result, nil
}

type StrideInsightsFeedback struct {
	Schema             string                `json:"schema"`
	FeedbackID         string                `json:"feedbackId"`
	RunID              string                `json:"runId"`
	ReportDigest       string                `json:"reportDigest"`
	Action             string                `json:"action"`
	Correction         string                `json:"correction,omitempty"`
	ActorID            string                `json:"actorId"`
	Binding            StrideInsightsBinding `json:"binding"`
	NewRequestRevision int                   `json:"newRequestRevision,omitempty"`
	NewRunID           string                `json:"newRunId,omitempty"`
	At                 time.Time             `json:"at"`
	FeedbackDigest     string                `json:"feedbackDigest"`
}

func (feedback StrideInsightsFeedback) Validate(run StrideInsightsRun) error {
	if feedback.Schema != StrideInsightsFeedbackSchema || !strideIdentifier(feedback.FeedbackID) || feedback.RunID != run.RunID || !isHexDigest(feedback.ReportDigest) ||
		!oneOf(feedback.Action, insightsFeedbackAccept, insightsFeedbackReject, insightsFeedbackCorrect, insightsFeedbackRequestRevision) || !strideIdentifier(feedback.ActorID) ||
		feedback.Binding != run.Request.Binding || feedback.At.IsZero() || !isHexDigest(feedback.FeedbackDigest) {
		return ErrStrideInsightsInvalid
	}
	if feedback.Action == insightsFeedbackCorrect && strings.TrimSpace(feedback.Correction) == "" {
		return ErrStrideInsightsInvalid
	}
	if feedback.Action == insightsFeedbackRequestRevision && (feedback.NewRequestRevision != run.Request.RequestRevision+1 || !strideIdentifier(feedback.NewRunID)) {
		return ErrStrideInsightsInvalid
	}
	foundReport := false
	for _, report := range run.Reports {
		if report.ReportDigest == feedback.ReportDigest {
			foundReport = true
			break
		}
	}
	if !foundReport {
		return ErrStrideInsightsInvalid
	}
	copy := feedback
	copy.FeedbackDigest = ""
	raw, _ := canonicalJSON(copy)
	if temporalDigestBytes(raw) != feedback.FeedbackDigest {
		return ErrStrideInsightsInvalid
	}
	return nil
}

func DecodeStrideInsightsFeedback(raw []byte, run StrideInsightsRun) (StrideInsightsFeedback, error) {
	var result StrideInsightsFeedback
	if strideInsightsDecode(raw, &result) != nil || result.Validate(run) != nil {
		return StrideInsightsFeedback{}, ErrStrideInsightsInvalid
	}
	return result, nil
}

type StrideInsightsArtifact struct {
	Schema         string                `json:"schema"`
	ArtifactID     string                `json:"artifactId"`
	RunID          string                `json:"runId"`
	ReportDigest   string                `json:"reportDigest"`
	Destination    string                `json:"destination"`
	Binding        StrideInsightsBinding `json:"binding"`
	IdempotencyKey string                `json:"idempotencyKey"`
	PublishedAt    time.Time             `json:"publishedAt"`
	ArtifactDigest string                `json:"artifactDigest"`
}

func (artifact StrideInsightsArtifact) Validate(binding StrideInsightsBinding) error {
	if artifact.Schema != StrideInsightsArtifactSchema || !strideIdentifier(artifact.ArtifactID) || !strideIdentifier(artifact.RunID) || !isHexDigest(artifact.ReportDigest) ||
		!strings.HasPrefix(artifact.Destination, "workspace:") || artifact.Binding != binding || !isHexDigest(artifact.IdempotencyKey) || artifact.PublishedAt.IsZero() || !isHexDigest(artifact.ArtifactDigest) {
		return ErrStrideInsightsInvalid
	}
	copy := artifact
	copy.ArtifactDigest = ""
	raw, _ := canonicalJSON(copy)
	if temporalDigestBytes(raw) != artifact.ArtifactDigest {
		return ErrStrideInsightsInvalid
	}
	return nil
}

func DecodeStrideInsightsArtifact(raw []byte, binding StrideInsightsBinding) (StrideInsightsArtifact, error) {
	var result StrideInsightsArtifact
	if strideInsightsDecode(raw, &result) != nil || result.Validate(binding) != nil {
		return StrideInsightsArtifact{}, ErrStrideInsightsInvalid
	}
	return result, nil
}

type StrideInsightsOutcome struct {
	Schema        string                  `json:"schema"`
	RunID         string                  `json:"runId"`
	Status        string                  `json:"status"`
	ReportDigest  string                  `json:"reportDigest,omitempty"`
	Artifact      *StrideInsightsArtifact `json:"artifact,omitempty"`
	Reason        string                  `json:"reason"`
	Binding       StrideInsightsBinding   `json:"binding"`
	CompletedAt   time.Time               `json:"completedAt"`
	OutcomeDigest string                  `json:"outcomeDigest"`
}

func (outcome StrideInsightsOutcome) Validate(binding StrideInsightsBinding) error {
	if outcome.Schema != StrideInsightsOutcomeSchema || !strideIdentifier(outcome.RunID) || !oneOf(outcome.Status, StrideInsightsStatusAccepted, StrideInsightsStatusRejected) ||
		!isHexDigest(outcome.ReportDigest) || strings.TrimSpace(outcome.Reason) == "" || outcome.Binding != binding || outcome.CompletedAt.IsZero() || !isHexDigest(outcome.OutcomeDigest) ||
		(outcome.Status == StrideInsightsStatusAccepted && outcome.Artifact == nil) || (outcome.Status == StrideInsightsStatusRejected && outcome.Artifact != nil) {
		return ErrStrideInsightsInvalid
	}
	if outcome.Artifact != nil && outcome.Artifact.Validate(binding) != nil {
		return ErrStrideInsightsInvalid
	}
	copy := outcome
	copy.OutcomeDigest = ""
	raw, _ := canonicalJSON(copy)
	if temporalDigestBytes(raw) != outcome.OutcomeDigest {
		return ErrStrideInsightsInvalid
	}
	return nil
}

func DecodeStrideInsightsOutcome(raw []byte, binding StrideInsightsBinding) (StrideInsightsOutcome, error) {
	var result StrideInsightsOutcome
	if strideInsightsDecode(raw, &result) != nil || result.Validate(binding) != nil {
		return StrideInsightsOutcome{}, ErrStrideInsightsInvalid
	}
	return result, nil
}

type StrideInsightsStageManifest struct {
	StageID       string `json:"stageId"`
	Owner         string `json:"owner"`
	RouteAlias    string `json:"routeAlias"`
	PromptVersion string `json:"promptVersion"`
	Schema        string `json:"schema"`
}

type StrideInsightsWorkflowManifest struct {
	WorkflowID       string                        `json:"workflowId"`
	Version          int                           `json:"version"`
	Stages           []StrideInsightsStageManifest `json:"stages"`
	MaxCriticRounds  int                           `json:"maxCriticRounds"`
	ExternalWrites   bool                          `json:"externalWrites"`
	StageGraphDigest string                        `json:"stageGraphDigest"`
	ManifestDigest   string                        `json:"manifestDigest"`
}

func FixedStrideInsightsWorkflowManifest() StrideInsightsWorkflowManifest {
	manifest := StrideInsightsWorkflowManifest{WorkflowID: insightsOpportunitiesProcessID, Version: StrideInsightsWorkflowVersion, MaxCriticRounds: 2, ExternalWrites: false,
		Stages: []StrideInsightsStageManifest{
			{StageID: "goal_ownership", Owner: "goal_owner", RouteAlias: "insights.goal-owner", PromptVersion: "goal-v1", Schema: "goal_frame_v1"},
			{StageID: "strategic_framing", Owner: "strategist", RouteAlias: "insights.strategist", PromptVersion: "strategy-v1", Schema: "strategy_frame_v1"},
			{StageID: "research_extraction", Owner: "researcher", RouteAlias: "insights.researcher", PromptVersion: "research-v1", Schema: StrideInsightsEvidenceSchema},
			{StageID: "writer", Owner: "writer", RouteAlias: "insights.writer", PromptVersion: "writer-v1", Schema: StrideInsightsReportSchema},
			{StageID: "criterion_claim_critic", Owner: "critic", RouteAlias: "insights.critic", PromptVersion: "critic-v1", Schema: StrideInsightsCriticSchema},
			{StageID: "verifier", Owner: "verifier", RouteAlias: "insights.verifier", PromptVersion: "verify-v1", Schema: StrideInsightsOutcomeSchema},
		},
	}
	graph, _ := canonicalJSON(manifest.Stages)
	manifest.StageGraphDigest = temporalDigestBytes(graph)
	copy := manifest
	copy.ManifestDigest = ""
	raw, _ := canonicalJSON(copy)
	manifest.ManifestDigest = temporalDigestBytes(raw)
	return manifest
}

func (manifest StrideInsightsWorkflowManifest) Validate() error {
	want := FixedStrideInsightsWorkflowManifest()
	if manifest.WorkflowID != want.WorkflowID || manifest.Version != want.Version || manifest.MaxCriticRounds != want.MaxCriticRounds || manifest.ExternalWrites ||
		manifest.StageGraphDigest != want.StageGraphDigest || manifest.ManifestDigest != want.ManifestDigest || !equalStageManifests(manifest.Stages, want.Stages) {
		return ErrStrideInsightsInvalid
	}
	return nil
}

type StrideInsightsStageReceipt struct {
	StageID       string                `json:"stageId"`
	Round         int                   `json:"round"`
	InputDigest   string                `json:"inputDigest"`
	OutputDigest  string                `json:"outputDigest"`
	Binding       StrideInsightsBinding `json:"binding"`
	ExternalCalls int                   `json:"externalCalls"`
	InputTokens   int                   `json:"inputTokens"`
	OutputTokens  int                   `json:"outputTokens"`
	Synthetic     bool                  `json:"synthetic"`
	CompletedAt   time.Time             `json:"completedAt"`
}

func (receipt StrideInsightsStageReceipt) tokenFree() bool {
	return strideIdentifier(receipt.StageID) && receipt.Round >= 1 && isHexDigest(receipt.InputDigest) && isHexDigest(receipt.OutputDigest) && receipt.Binding.Validate() == nil &&
		receipt.ExternalCalls == 0 && receipt.InputTokens == 0 && receipt.OutputTokens == 0 && receipt.Synthetic && !receipt.CompletedAt.IsZero()
}

type StrideInsightsContribution struct {
	StageID string                `json:"stageId"`
	Round   int                   `json:"round"`
	Digest  string                `json:"digest"`
	Binding StrideInsightsBinding `json:"binding"`
}

type StrideInsightsStageResult struct {
	Digest        string                       `json:"digest"`
	Report        *StrideInsightsReport        `json:"report,omitempty"`
	Verdict       *StrideInsightsCriticVerdict `json:"verdict,omitempty"`
	ExternalCalls int                          `json:"externalCalls"`
	InputTokens   int                          `json:"inputTokens"`
	OutputTokens  int                          `json:"outputTokens"`
	Synthetic     bool                         `json:"synthetic"`
}

type StrideInsightsDeterministicStageExecutor interface {
	ExecuteStrideInsightsStage(stage StrideInsightsStageManifest, round int, request StrideInsightsRequest, prior *StrideInsightsReport) (StrideInsightsStageResult, error)
}

type StrideInsightsRun struct {
	RunID         string                        `json:"runId"`
	Status        string                        `json:"status"`
	Request       StrideInsightsRequest         `json:"request"`
	NextStage     int                           `json:"nextStage"`
	CriticRound   int                           `json:"criticRound"`
	Reports       []StrideInsightsReport        `json:"reports"`
	Verdicts      []StrideInsightsCriticVerdict `json:"verdicts"`
	Receipts      []StrideInsightsStageReceipt  `json:"receipts"`
	Contributions []StrideInsightsContribution  `json:"contributions"`
	Feedback      []StrideInsightsFeedback      `json:"feedback"`
	Artifact      *StrideInsightsArtifact       `json:"artifact,omitempty"`
	Outcome       *StrideInsightsOutcome        `json:"outcome,omitempty"`
	BlockedReason string                        `json:"blockedReason,omitempty"`
	CreatedAt     time.Time                     `json:"createdAt"`
	UpdatedAt     time.Time                     `json:"updatedAt"`
}

type strideInsightsWorkflowSnapshot struct {
	Format      int                            `json:"format"`
	Manifest    StrideInsightsWorkflowManifest `json:"manifest"`
	Runs        []StrideInsightsRun            `json:"runs"`
	Artifacts   []StrideInsightsArtifact       `json:"artifacts"`
	StateDigest string                         `json:"stateDigest"`
}

type StrideInsightsWorkflow struct {
	manifest   StrideInsightsWorkflowManifest
	runs       map[string]StrideInsightsRun
	artifacts  map[string]StrideInsightsArtifact
	now        func() time.Time
	crashAfter map[string]bool
}

func NewStrideInsightsWorkflow(now func() time.Time) *StrideInsightsWorkflow {
	return &StrideInsightsWorkflow{manifest: FixedStrideInsightsWorkflowManifest(), runs: map[string]StrideInsightsRun{}, artifacts: map[string]StrideInsightsArtifact{}, now: now, crashAfter: map[string]bool{}}
}

func (workflow *StrideInsightsWorkflow) InjectCrashAfter(stageID string) {
	workflow.crashAfter[stageID] = true
}

func (workflow *StrideInsightsWorkflow) Launch(principal ACLPrincipal, request StrideInsightsRequest, executor StrideInsightsDeterministicStageExecutor) (StrideInsightsRun, error) {
	if workflow == nil || workflow.manifest.Validate() != nil || request.Validate() != nil || request.ManifestDigest != workflow.manifest.ManifestDigest || executor == nil ||
		principal.Kind != ACLPrincipalUser || principal.TenantID != request.TenantID || principal.ID != request.PrincipalID {
		return StrideInsightsRun{}, ErrStrideInsightsUnauthorized
	}
	if existing, ok := workflow.runs[request.RunID]; ok {
		if existing.Request.RequestDigest != request.RequestDigest {
			return StrideInsightsRun{}, ErrStrideInsightsConflict
		}
		if existing.Status == StrideInsightsStatusAccepted || existing.Status == StrideInsightsStatusRejected {
			return existing, nil
		}
	}
	if request.ParentRunID != "" {
		parent, ok := workflow.runs[request.ParentRunID]
		if !ok || len(parent.Reports) == 0 || parent.Reports[len(parent.Reports)-1].ReportDigest != request.ParentReportDigest || request.RequestRevision != parent.Request.RequestRevision+1 {
			return StrideInsightsRun{}, ErrStrideInsightsConflict
		}
	}
	run, ok := workflow.runs[request.RunID]
	if !ok {
		now := workflow.time()
		run = StrideInsightsRun{RunID: request.RunID, Status: StrideInsightsStatusRunning, Request: request, CriticRound: 1, CreatedAt: now, UpdatedAt: now}
	}
	if run.Status == StrideInsightsStatusBlocked {
		run.Status, run.BlockedReason = StrideInsightsStatusRunning, ""
	}
	for run.NextStage < len(workflow.manifest.Stages) {
		stage := workflow.manifest.Stages[run.NextStage]
		var prior *StrideInsightsReport
		if len(run.Reports) > 0 {
			copy := run.Reports[len(run.Reports)-1]
			prior = &copy
		}
		inputRaw, _ := canonicalJSON(struct {
			Request string
			Stage   string
			Round   int
			Prior   string
		}{request.RequestDigest, stage.StageID, run.CriticRound, reportDigestOf(prior)})
		result, err := executor.ExecuteStrideInsightsStage(stage, run.CriticRound, request, prior)
		if err != nil {
			run.Status, run.BlockedReason, run.UpdatedAt = StrideInsightsStatusBlocked, err.Error(), workflow.time()
			workflow.runs[run.RunID] = cloneStrideInsightsRun(run)
			return run, err
		}
		if !isHexDigest(result.Digest) || result.ExternalCalls != 0 || result.InputTokens != 0 || result.OutputTokens != 0 || !result.Synthetic {
			return StrideInsightsRun{}, ErrStrideInsightsInvalid
		}
		receipt := StrideInsightsStageReceipt{StageID: stage.StageID, Round: run.CriticRound, InputDigest: temporalDigestBytes(inputRaw), OutputDigest: result.Digest,
			Binding: request.Binding, ExternalCalls: result.ExternalCalls, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens, Synthetic: result.Synthetic, CompletedAt: workflow.time()}
		run.Receipts = append(run.Receipts, receipt)
		run.Contributions = append(run.Contributions, StrideInsightsContribution{StageID: stage.StageID, Round: run.CriticRound, Digest: result.Digest, Binding: request.Binding})
		switch stage.StageID {
		case "writer":
			if result.Report == nil {
				return StrideInsightsRun{}, ErrStrideInsightsInvalid
			}
			report := *result.Report
			report.Schema, report.RunID, report.RequestDigest, report.EvidenceManifestDigest, report.ManifestDigest, report.Binding = StrideInsightsReportSchema, run.RunID, request.RequestDigest, request.Evidence.ManifestDigest, request.ManifestDigest, request.Binding
			report.Revision, report.GeneratedAt = len(run.Reports)+1, workflow.time()
			for index := range report.Opportunities {
				report.Opportunities[index].Schema = StrideInsightsOpportunitySchema
			}
			if len(run.Reports) > 0 {
				report.ParentReportDigest = run.Reports[len(run.Reports)-1].ReportDigest
			} else {
				report.ParentReportDigest = ""
			}
			report.ReportDigest = ""
			report.ReportDigest, _ = strideInsightsReportDigest(report)
			if report.Validate(request) != nil {
				return StrideInsightsRun{}, fmt.Errorf("%w: invalid or invented report claim", ErrStrideInsightsInvalid)
			}
			run.Reports = append(run.Reports, report)
		case "criterion_claim_critic":
			if result.Verdict == nil || len(run.Reports) == 0 {
				return StrideInsightsRun{}, ErrStrideInsightsInvalid
			}
			verdict := *result.Verdict
			report := run.Reports[len(run.Reports)-1]
			verdict.Schema, verdict.RunID, verdict.ReportDigest, verdict.Round, verdict.MaxRounds, verdict.Binding = StrideInsightsCriticSchema, run.RunID, report.ReportDigest, run.CriticRound, workflow.manifest.MaxCriticRounds, request.Binding
			verdict.VerdictDigest = ""
			raw, _ := canonicalJSON(verdict)
			verdict.VerdictDigest = temporalDigestBytes(raw)
			if verdict.Validate(report) != nil {
				return StrideInsightsRun{}, ErrStrideInsightsInvalid
			}
			run.Verdicts = append(run.Verdicts, verdict)
			if verdict.Outcome == insightsCriticReject {
				run.Status = StrideInsightsStatusRejected
				run.NextStage = len(workflow.manifest.Stages)
				workflow.finishOutcome(&run, "critic_rejected")
				workflow.runs[run.RunID] = cloneStrideInsightsRun(run)
				return run, nil
			}
			if verdict.Outcome == insightsCriticRevise {
				if run.CriticRound >= workflow.manifest.MaxCriticRounds {
					run.Status = StrideInsightsStatusRejected
					run.NextStage = len(workflow.manifest.Stages)
					workflow.finishOutcome(&run, "critic_round_limit")
					workflow.runs[run.RunID] = cloneStrideInsightsRun(run)
					return run, ErrStrideInsightsCriticLimit
				}
				run.CriticRound++
				run.NextStage = stageIndex(workflow.manifest.Stages, "writer")
				run.UpdatedAt = workflow.time()
				workflow.runs[run.RunID] = cloneStrideInsightsRun(run)
				continue
			}
		case "verifier":
			if len(run.Verdicts) == 0 || run.Verdicts[len(run.Verdicts)-1].Outcome != insightsCriticAccept {
				return StrideInsightsRun{}, ErrStrideInsightsInvalid
			}
			workflow.publishExactlyOnce(&run)
			run.Status = StrideInsightsStatusAccepted
			workflow.finishOutcome(&run, "verified")
		}
		run.NextStage++
		run.UpdatedAt = workflow.time()
		workflow.runs[run.RunID] = cloneStrideInsightsRun(run)
		if workflow.crashAfter[stage.StageID] {
			delete(workflow.crashAfter, stage.StageID)
			return run, ErrStrideInsightsInjectedCrash
		}
	}
	workflow.runs[run.RunID] = cloneStrideInsightsRun(run)
	return run, nil
}

func (workflow *StrideInsightsWorkflow) SubmitFeedback(principal ACLPrincipal, runID string, feedback StrideInsightsFeedback) (StrideInsightsRun, error) {
	run, ok := workflow.runs[runID]
	if !ok || principal.Kind != ACLPrincipalUser || principal.TenantID != run.Request.TenantID || principal.ID != feedback.ActorID || feedback.Validate(run) != nil {
		return StrideInsightsRun{}, ErrStrideInsightsUnauthorized
	}
	for _, prior := range run.Feedback {
		if prior.FeedbackID == feedback.FeedbackID {
			if prior.FeedbackDigest == feedback.FeedbackDigest {
				return run, nil
			}
			return StrideInsightsRun{}, ErrStrideInsightsConflict
		}
	}
	run.Feedback = append(run.Feedback, feedback)
	run.UpdatedAt = workflow.time()
	workflow.runs[runID] = cloneStrideInsightsRun(run)
	return run, nil
}

func (workflow *StrideInsightsWorkflow) publishExactlyOnce(run *StrideInsightsRun) {
	report := run.Reports[len(run.Reports)-1]
	key := temporalDigest(run.RunID + "\x00" + report.ReportDigest + "\x00" + run.Request.InternalDestination)
	if prior, ok := workflow.artifacts[key]; ok {
		copy := prior
		run.Artifact = &copy
		return
	}
	artifact := StrideInsightsArtifact{Schema: StrideInsightsArtifactSchema, ArtifactID: "insights-artifact-" + run.RunID, RunID: run.RunID, ReportDigest: report.ReportDigest,
		Destination: run.Request.InternalDestination, Binding: run.Request.Binding, IdempotencyKey: key, PublishedAt: workflow.time()}
	copy := artifact
	copy.ArtifactDigest = ""
	raw, _ := canonicalJSON(copy)
	artifact.ArtifactDigest = temporalDigestBytes(raw)
	workflow.artifacts[key] = artifact
	run.Artifact = &artifact
}

func (workflow *StrideInsightsWorkflow) finishOutcome(run *StrideInsightsRun, reason string) {
	outcome := StrideInsightsOutcome{Schema: StrideInsightsOutcomeSchema, RunID: run.RunID, Status: run.Status, Reason: reason, Binding: run.Request.Binding, CompletedAt: workflow.time(), Artifact: run.Artifact}
	if len(run.Reports) > 0 {
		outcome.ReportDigest = run.Reports[len(run.Reports)-1].ReportDigest
	}
	copy := outcome
	copy.OutcomeDigest = ""
	raw, _ := canonicalJSON(copy)
	outcome.OutcomeDigest = temporalDigestBytes(raw)
	run.Outcome = &outcome
}

func (workflow *StrideInsightsWorkflow) Run(runID string) (StrideInsightsRun, bool) {
	run, ok := workflow.runs[runID]
	return cloneStrideInsightsRun(run), ok
}

func (workflow *StrideInsightsWorkflow) Snapshot() ([]byte, error) {
	snapshot := strideInsightsWorkflowSnapshot{Format: 1, Manifest: workflow.manifest}
	for _, run := range workflow.runs {
		snapshot.Runs = append(snapshot.Runs, cloneStrideInsightsRun(run))
	}
	for _, artifact := range workflow.artifacts {
		snapshot.Artifacts = append(snapshot.Artifacts, artifact)
	}
	sort.Slice(snapshot.Runs, func(i, j int) bool { return snapshot.Runs[i].RunID < snapshot.Runs[j].RunID })
	sort.Slice(snapshot.Artifacts, func(i, j int) bool {
		return snapshot.Artifacts[i].IdempotencyKey < snapshot.Artifacts[j].IdempotencyKey
	})
	copy := snapshot
	copy.StateDigest = ""
	raw, err := canonicalJSON(copy)
	if err != nil {
		return nil, err
	}
	snapshot.StateDigest = temporalDigestBytes(raw)
	return canonicalJSON(snapshot)
}

func RestoreStrideInsightsWorkflow(raw []byte, now func() time.Time) (*StrideInsightsWorkflow, error) {
	var snapshot strideInsightsWorkflowSnapshot
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&snapshot) != nil {
		return nil, ErrStrideInsightsInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrStrideInsightsInvalid
	}
	copy := snapshot
	copy.StateDigest = ""
	canonical, _ := canonicalJSON(copy)
	if snapshot.Format != 1 || snapshot.Manifest.Validate() != nil || !isHexDigest(snapshot.StateDigest) || snapshot.StateDigest != temporalDigestBytes(canonical) {
		return nil, ErrStrideInsightsInvalid
	}
	workflow := NewStrideInsightsWorkflow(now)
	seen := map[string]bool{}
	for _, run := range snapshot.Runs {
		if seen[run.RunID] || run.Request.Validate() != nil {
			return nil, ErrStrideInsightsInvalid
		}
		seen[run.RunID] = true
		workflow.runs[run.RunID] = cloneStrideInsightsRun(run)
	}
	for _, artifact := range snapshot.Artifacts {
		if artifact.IdempotencyKey == "" {
			return nil, ErrStrideInsightsInvalid
		}
		workflow.artifacts[artifact.IdempotencyKey] = artifact
	}
	return workflow, nil
}

type StrideInsightsPilotFixture struct {
	Schema, FixtureID, InputDigest, ExpectedDigest string
	Synthetic                                      bool
}
type StrideInsightsPilotRubric struct {
	ReviewerIDs []string `json:"reviewerIds"`
	Criteria    []string `json:"criteria"`
}
type StrideInsightsPilotResult struct {
	Schema                   string   `json:"schema"`
	FixtureDigests           []string `json:"fixtureDigests"`
	ReviewDigests            []string `json:"reviewDigests"`
	SyntheticOnly            bool     `json:"syntheticOnly"`
	E10HumanProviderAccepted bool     `json:"e10HumanProviderAccepted"`
	ResultDigest             string   `json:"resultDigest"`
}

func ImmutableStrideInsightsPilotFixtures() []StrideInsightsPilotFixture {
	result := make([]StrideInsightsPilotFixture, 10)
	for i := range result {
		id := fmt.Sprintf("pilot-%02d", i+1)
		result[i] = StrideInsightsPilotFixture{Schema: StrideInsightsPilotSchema, FixtureID: id, InputDigest: temporalDigest(id + ":input"), ExpectedDigest: temporalDigest(id + ":expected"), Synthetic: true}
	}
	return result
}

func EvaluateStrideInsightsSyntheticPilot(fixtures []StrideInsightsPilotFixture, rubric StrideInsightsPilotRubric, reviewDigests []string) (StrideInsightsPilotResult, error) {
	if len(fixtures) != 10 || len(rubric.ReviewerIDs) != 2 || rubric.ReviewerIDs[0] == rubric.ReviewerIDs[1] || len(rubric.Criteria) == 0 || len(reviewDigests) != 20 {
		return StrideInsightsPilotResult{}, ErrStrideInsightsInvalid
	}
	result := StrideInsightsPilotResult{Schema: StrideInsightsPilotSchema, SyntheticOnly: true, E10HumanProviderAccepted: false, ReviewDigests: append([]string(nil), reviewDigests...)}
	for _, digest := range reviewDigests {
		if !isHexDigest(digest) {
			return StrideInsightsPilotResult{}, ErrStrideInsightsInvalid
		}
	}
	for _, fixture := range fixtures {
		if fixture.Schema != StrideInsightsPilotSchema || !strideIdentifier(fixture.FixtureID) || !isHexDigest(fixture.InputDigest) || !isHexDigest(fixture.ExpectedDigest) || !fixture.Synthetic {
			return StrideInsightsPilotResult{}, ErrStrideInsightsInvalid
		}
		result.FixtureDigests = append(result.FixtureDigests, temporalDigest(fixture.FixtureID+fixture.InputDigest+fixture.ExpectedDigest))
	}
	copy := result
	copy.ResultDigest = ""
	raw, _ := canonicalJSON(copy)
	result.ResultDigest = temporalDigestBytes(raw)
	return result, nil
}

type StrideInsightsPackageSeal struct {
	Manifest                                             AgentPackageManifest `json:"manifest"`
	Listing                                              MarketplaceListing   `json:"listing"`
	Availability                                         string               `json:"availability"`
	ProfileRef, CapabilityRef                            STRIDEReference
	RuntimeRef, WorkRunRef                               STRIDEReference
	EvalDigest, SampleDigest, CostDigest, RollbackDigest string
}

func SealStrideInsightsPackage(tenantID string, identity StrideInsightsAnalystIdentity, run StrideInsightsRun, pilot StrideInsightsPilotResult, at time.Time) (StrideInsightsPackageSeal, error) {
	if identity.Validate() != nil || identity.Profile.Header.TenantID != tenantID || run.Status != StrideInsightsStatusAccepted || run.Artifact == nil || run.Outcome == nil ||
		!pilot.SyntheticOnly || pilot.E10HumanProviderAccepted || !isHexDigest(pilot.ResultDigest) {
		return StrideInsightsPackageSeal{}, ErrStrideInsightsPackageGate
	}
	seenStages := map[string]bool{}
	for _, receipt := range run.Receipts {
		if !receipt.tokenFree() || receipt.Binding != run.Request.Binding {
			return StrideInsightsPackageSeal{}, ErrStrideInsightsPackageGate
		}
		seenStages[receipt.StageID] = true
	}
	for _, stage := range FixedStrideInsightsWorkflowManifest().Stages {
		if !seenStages[stage.StageID] {
			return StrideInsightsPackageSeal{}, ErrStrideInsightsPackageGate
		}
	}
	profileRef := referenceFromHeader(identity.Profile.Header)
	evalRef := STRIDEReference{ContractType: STRIDEContractAgentPerformanceReceipt, ID: "insights-token-free-eval", Revision: 1, Digest: pilot.ResultDigest}
	sampleDigest := temporalDigest("ten-immutable-pilot-fixtures")
	costDigest := temporalDigest("zero-token-zero-external-call")
	rollbackDigest := temporalDigest("disable-and-revert-package-v1")
	metadataRef := STRIDEReference{ContractType: STRIDEContractOutcome, ID: "insights-sample-cost-rollback", Revision: 1, Digest: temporalDigest(sampleDigest + costDigest + rollbackDigest)}
	manifest := AgentPackageManifest{Header: STRIDEContractHeader{TenantID: tenantID, ID: "insights-opportunities-package-v1", Revision: 1, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractAgentPackageManifest, ContentDigest: temporalDigest(pilot.ResultDigest + sampleDigest + costDigest + rollbackDigest), CreatedAt: at.UTC()}, PackageID: "insights-opportunities-v1", PublisherID: "stride", PublisherAttestationDigest: temporalDigest("stride-token-free-attestation"), Version: "1.0.0", Provenance: "stride_authored", PersonaSeedDigest: identity.Profile.Header.ContentDigest, AssetRefs: []STRIDEReference{profileRef, identity.Capability, identity.Runtime}, RequestedCapabilities: []string{"internal_workspace_report"}, RuntimeClasses: []string{"deterministic_checkpointed"}, ModelClasses: []string{"descriptive_alias_only"}, VoiceClasses: []string{"none"}, DataClassifications: []string{"authorized_snapshot_only"}, EvalBundleRefs: []STRIDEReference{evalRef}, DependencySBOMRefs: []STRIDEReference{run.Request.Binding.WorkRun, metadataRef}, LicenseID: "internal", UpdatePolicy: "human_approval", MigrationCompatibility: "v1", Status: "verified"}
	if manifest.Validate() != nil {
		return StrideInsightsPackageSeal{}, ErrStrideInsightsPackageGate
	}
	packageRef := referenceFromHeader(manifest.Header)
	listing := MarketplaceListing{Header: STRIDEContractHeader{TenantID: tenantID, ID: "insights-opportunities-listing-v1", Revision: 1, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractMarketplaceListing, ContentDigest: temporalDigest("draft-unavailable:" + manifest.Header.ContentDigest), CreatedAt: at.UTC()}, Package: packageRef, Category: "insights", OutcomeDigest: run.Outcome.OutcomeDigest, Evidence: []STRIDEReference{evalRef}, PermissionSummaryDigest: temporalDigest("internal-only-no-external-write"), Surfaces: []string{"workspace"}, CostBand: "zero_token_fixture", Audience: STRIDEAudience{Visibility: "organization", Principals: []string{run.Request.PrincipalID}}, PublisherStatus: "active", UpdateChannel: "manual", Reviewer: run.Request.PrincipalID, Status: "draft"}
	if listing.Validate() != nil {
		return StrideInsightsPackageSeal{}, ErrStrideInsightsPackageGate
	}
	return StrideInsightsPackageSeal{Manifest: manifest, Listing: listing, Availability: "unavailable", ProfileRef: profileRef, CapabilityRef: identity.Capability,
		RuntimeRef: identity.Runtime, WorkRunRef: run.Request.Binding.WorkRun, EvalDigest: pilot.ResultDigest, SampleDigest: sampleDigest, CostDigest: costDigest, RollbackDigest: rollbackDigest}, nil
}

func (workflow *StrideInsightsWorkflow) time() time.Time {
	if workflow.now != nil {
		return workflow.now().UTC()
	}
	return time.Now().UTC()
}
func stageIndex(stages []StrideInsightsStageManifest, id string) int {
	for i, s := range stages {
		if s.StageID == id {
			return i
		}
	}
	return -1
}
func reportDigestOf(report *StrideInsightsReport) string {
	if report == nil {
		return ""
	}
	return report.ReportDigest
}
func cloneStrideInsightsRun(run StrideInsightsRun) StrideInsightsRun {
	raw, _ := json.Marshal(run)
	var clone StrideInsightsRun
	_ = json.Unmarshal(raw, &clone)
	return clone
}
func equalStageManifests(a, b []StrideInsightsStageManifest) bool {
	rawA, _ := canonicalJSON(a)
	rawB, _ := canonicalJSON(b)
	return bytes.Equal(rawA, rawB)
}
func reflectSnapshotSources(a, b []RetrievalSnapshotSource) bool {
	rawA, _ := canonicalJSON(a)
	rawB, _ := canonicalJSON(b)
	return bytes.Equal(rawA, rawB)
}
func uniqueTextIDs(values []string) bool {
	if len(values) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, v := range values {
		if !strideIdentifier(v) || seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}
func validOptionalTextIDs(values []string) bool { return len(values) == 0 || uniqueTextIDs(values) }
func strideInsightsDecode(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrStrideInsightsInvalid
	}
	return nil
}
