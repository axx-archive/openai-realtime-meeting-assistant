package main

import (
	"context"
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const strideLeadHarnessShadowEnvironment = "BONFIRE_STRIDE_LEAD_HARNESS_SHADOW"

const (
	defaultSTRIDELeadHarnessModel           = "gpt-5.6-sol"
	defaultSTRIDELeadHarnessReasoningEffort = "max"
)

var (
	ErrSTRIDELeadHarnessFenced      = errors.New("STRIDE lead harness is default-off")
	ErrSTRIDELeadHarnessInvalid     = errors.New("STRIDE lead harness request is invalid")
	ErrSTRIDELeadHarnessNeedsHuman  = errors.New("STRIDE lead harness needs a consequential human decision")
	ErrSTRIDELeadHarnessRecoverable = errors.New("STRIDE lead harness has a recoverable system failure")
)

func strideLeadHarnessShadowEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(strideLeadHarnessShadowEnvironment))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

type STRIDELeadProviderReceipt struct {
	RunID                  string    `json:"runId"`
	AssignmentID           string    `json:"assignmentId"`
	Provider               string    `json:"provider"`
	Model                  string    `json:"model"`
	ResponseID             string    `json:"responseId"`
	PreviousResponseID     string    `json:"previousResponseId,omitempty"`
	ConversationID         string    `json:"conversationId,omitempty"`
	Status                 string    `json:"status"`
	Recovery               string    `json:"recovery"`
	Attempt                int       `json:"attempt"`
	RequestDigest          string    `json:"requestDigest"`
	SpendBoundaryDigest    string    `json:"spendBoundaryDigest"`
	SourceManifestDigest   string    `json:"sourceManifestDigest"`
	AuthorityFenceDigest   string    `json:"authorityFenceDigest"`
	ToolAdmissionDigest    string    `json:"toolAdmissionDigest"`
	ProviderEnvelopeDigest string    `json:"providerEnvelopeDigest"`
	ObservedAt             time.Time `json:"observedAt"`
}

func (receipt STRIDELeadProviderReceipt) Validate() error {
	if !strideIdentifier(receipt.RunID) || !strideIdentifier(receipt.AssignmentID) || receipt.Provider != providerOpenAI ||
		receipt.Model != defaultSTRIDELeadHarnessModel || !strideIdentifier(receipt.ResponseID) || !validOptionalSTRIDEID(receipt.PreviousResponseID) ||
		!validOptionalSTRIDEID(receipt.ConversationID) || !oneOf(receipt.Status, "queued", "in_progress", "completed", "incomplete", "failed", "cancelled") ||
		!oneOf(receipt.Recovery, "created", "retrieved", "resumed") || receipt.Attempt < 1 || !allDigests(receipt.RequestDigest, receipt.SpendBoundaryDigest, receipt.SourceManifestDigest,
		receipt.AuthorityFenceDigest, receipt.ToolAdmissionDigest, receipt.ProviderEnvelopeDigest) || receipt.ObservedAt.IsZero() || receipt.ObservedAt.Location() != time.UTC {
		return ErrSTRIDELeadHarnessInvalid
	}
	return nil
}

type STRIDELeadMilestone struct {
	RunID        string             `json:"runId"`
	AssignmentID string             `json:"assignmentId"`
	Kind         string             `json:"kind"`
	Phase        STRIDEWorkRunPhase `json:"phase"`
	Summary      string             `json:"summary"`
	Evidence     []STRIDEReference  `json:"evidence,omitempty"`
	CommittedAt  time.Time          `json:"committedAt"`
}

func (milestone STRIDELeadMilestone) Validate() error {
	if !strideIdentifier(milestone.RunID) || !strideIdentifier(milestone.AssignmentID) ||
		!oneOf(milestone.Kind, "provider_started", "provider_completed", "artifact_committed", "delivery_committed") ||
		!validSTRIDEWorkRunPhase(milestone.Phase) || !humanActivitySummary(milestone.Summary) ||
		!validateOptionalSTRIDERefs(milestone.Evidence) || milestone.CommittedAt.IsZero() || milestone.CommittedAt.Location() != time.UTC {
		return ErrSTRIDELeadHarnessInvalid
	}
	return nil
}

type STRIDELeadSourceManifest struct {
	RunID              string                            `json:"runId"`
	AssignmentID       string                            `json:"assignmentId"`
	Authority          STRIDEAssignmentAuthoritySnapshot `json:"authority"`
	AuthorityFence     string                            `json:"authorityFence"`
	SourceManifestHash string                            `json:"sourceManifestHash"`
}

func newSTRIDELeadSourceManifest(run STRIDECanonicalWorkRun, assignment STRIDECanonicalAgentAssignment) (STRIDELeadSourceManifest, error) {
	manifest := STRIDELeadSourceManifest{
		RunID: run.ID, AssignmentID: assignment.ID, Authority: assignment.AuthoritySnapshot, AuthorityFence: assignment.AuthorityFenceDigest,
	}
	digest, err := STRIDEContractDigest(struct {
		RunID          string
		AssignmentID   string
		Authority      STRIDEAssignmentAuthoritySnapshot
		AuthorityFence string
	}{manifest.RunID, manifest.AssignmentID, manifest.Authority, manifest.AuthorityFence})
	if err != nil {
		return STRIDELeadSourceManifest{}, err
	}
	manifest.SourceManifestHash = digest
	if run.Validate() != nil || assignment.Validate() != nil || assignment.RunID != run.ID || !allDigests(manifest.AuthorityFence, manifest.SourceManifestHash) {
		return STRIDELeadSourceManifest{}, ErrSTRIDELeadHarnessInvalid
	}
	return manifest, nil
}

type STRIDELeadSpendBoundary struct {
	Approval            STRIDEReference `json:"approval"`
	MaximumSpendCents   int64           `json:"maximumSpendCents"`
	ExpectedSpendCents  int64           `json:"expectedSpendCents"`
	ApprovalFenceDigest string          `json:"approvalFenceDigest"`
}

func (boundary STRIDELeadSpendBoundary) Validate() error {
	if boundary.Approval.Validate() != nil || boundary.MaximumSpendCents < 1 || boundary.ExpectedSpendCents < 0 ||
		boundary.ExpectedSpendCents > boundary.MaximumSpendCents || !isHexDigest(boundary.ApprovalFenceDigest) {
		return ErrSTRIDELeadHarnessInvalid
	}
	return nil
}

type STRIDELeadToolAdmission struct {
	Agent           string           `json:"agent"`
	ManifestDigest  string           `json:"manifestDigest"`
	AdmissionDigest string           `json:"admissionDigest"`
	Tools           []map[string]any `json:"tools"`
}

func admitSTRIDELeadTools(outputKind string) (STRIDELeadToolAdmission, error) {
	manifest, err := buildOpenAIToolManifest()
	if err != nil {
		return STRIDELeadToolAdmission{}, err
	}
	agent, _ := strideWorkRunSpecialist(outputKind)
	var names []string
	switch outputKind {
	case "research":
		names = []string{controlToolReportGoalState, "answer_memory_question"}
	case "presentation":
		names = []string{controlToolReportGoalState, "answer_memory_question", "create_artifact", "update_artifact"}
	default:
		return STRIDELeadToolAdmission{}, ErrSTRIDELeadHarnessInvalid
	}
	selected := make([]map[string]any, 0, len(names))
	for _, name := range names {
		entry, admitted := manifest.admitted(name)
		if !admitted || !entry.Admitted || !isHexDigest(entry.SchemaSHA256) || strings.TrimSpace(entry.PolicyRevision) == "" {
			return STRIDELeadToolAdmission{}, ErrSTRIDELeadHarnessInvalid
		}
		selected = append(selected, map[string]any{
			"type": "function", "name": entry.Name, "description": entry.Description, "parameters": entry.Parameters, "strict": true,
		})
	}
	sort.Slice(selected, func(i, j int) bool { return asString(selected[i]["name"]) < asString(selected[j]["name"]) })
	digest, err := STRIDEContractDigest(struct {
		Manifest string
		Agent    string
		Tools    []map[string]any
	}{manifest.DigestSHA256, agent, selected})
	if err != nil {
		return STRIDELeadToolAdmission{}, err
	}
	return STRIDELeadToolAdmission{Agent: agent, ManifestDigest: manifest.DigestSHA256, AdmissionDigest: digest, Tools: selected}, nil
}

type STRIDELeadResponsesRequest struct {
	Model               string
	ReasoningEffort     string
	Instructions        string
	Input               string
	IdempotencyKey      string
	PreviousResponseID  string
	ConversationID      string
	Metadata            map[string]string
	Tools               []map[string]any
	ToolAgent           string
	ToolManifestDigest  string
	ToolAdmissionDigest string
}

type STRIDELeadResponsesResult struct {
	ResponseID         string
	PreviousResponseID string
	ConversationID     string
	Model              string
	Status             string
	OutputText         string
	EnvelopeDigest     string
}

type STRIDELeadResponsesProvider interface {
	CreateSTRIDELeadResponse(context.Context, STRIDELeadResponsesRequest) (STRIDELeadResponsesResult, error)
	RetrieveSTRIDELeadResponse(context.Context, string) (STRIDELeadResponsesResult, error)
}

type STRIDELeadHarnessRequest struct {
	RunID              string
	Instructions       string
	Input              string
	PreviousResponseID string
	ConversationID     string
	Spend              STRIDELeadSpendBoundary
	Now                time.Time
}

type STRIDELeadHarness struct {
	Enabled  bool
	WorkRuns *STRIDEWorkRunRepository
	Provider STRIDELeadResponsesProvider
	Now      func() time.Time
}

func (harness *STRIDELeadHarness) Run(ctx context.Context, request STRIDELeadHarnessRequest) (STRIDELeadProviderReceipt, error) {
	if harness == nil || !harness.Enabled || !strideLeadHarnessShadowEnabled() {
		return STRIDELeadProviderReceipt{}, ErrSTRIDELeadHarnessFenced
	}
	if harness.WorkRuns == nil || harness.Provider == nil || !strideIdentifier(request.RunID) || request.Spend.Validate() != nil ||
		strings.TrimSpace(request.Instructions) == "" || strings.TrimSpace(request.Input) == "" ||
		(request.PreviousResponseID != "" && request.ConversationID != "") {
		return STRIDELeadProviderReceipt{}, ErrSTRIDELeadHarnessInvalid
	}
	card, err := harness.WorkRuns.SideCard(request.RunID)
	if err != nil || card.Run.AccountableAgent != STRIDEWorkAgentScout || oneOf(card.Status, "failed", "cancelled") {
		return STRIDELeadProviderReceipt{}, ErrSTRIDELeadHarnessInvalid
	}
	scout, specialist, ok := strideLeadHarnessAssignments(card)
	if !ok {
		return STRIDELeadProviderReceipt{}, ErrSTRIDELeadHarnessInvalid
	}
	sourceManifest, err := newSTRIDELeadSourceManifest(card.Run, scout)
	if err != nil {
		return STRIDELeadProviderReceipt{}, err
	}
	tools, err := admitSTRIDELeadTools(card.Run.OutputKind)
	if err != nil || tools.Agent != specialist.Agent {
		return STRIDELeadProviderReceipt{}, ErrSTRIDELeadHarnessInvalid
	}
	now := request.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
		if harness.Now != nil {
			now = harness.Now().UTC()
		}
	}
	requestDigest, err := STRIDEContractDigest(struct {
		RunID, Instructions, Input, PreviousResponseID, ConversationID string
		Spend                                                          STRIDELeadSpendBoundary
		SourceManifest, ToolAdmission                                  string
	}{request.RunID, strings.TrimSpace(request.Instructions), strings.TrimSpace(request.Input), request.PreviousResponseID, request.ConversationID, request.Spend, sourceManifest.SourceManifestHash, tools.AdmissionDigest})
	if err != nil {
		return STRIDELeadProviderReceipt{}, err
	}
	spendBoundaryDigest, err := STRIDEContractDigest(request.Spend)
	if err != nil {
		return STRIDELeadProviderReceipt{}, err
	}
	var result STRIDELeadResponsesResult
	recovery := "created"
	attempt := 1
	if card.Provider != nil {
		attempt = card.Provider.Attempt
	}
	if card.Provider != nil && card.Provider.Status == "completed" && card.Provider.RequestDigest == requestDigest {
		if err := harness.ensureProviderMilestone(card.Run, scout, *card.Provider, card.Phase); err != nil {
			return STRIDELeadProviderReceipt{}, err
		}
		return *card.Provider, nil
	}
	if card.Provider != nil && !leadProviderTerminal(card.Provider.Status) {
		result, err = harness.Provider.RetrieveSTRIDELeadResponse(ctx, card.Provider.ResponseID)
		recovery = "retrieved"
	} else {
		if card.Provider != nil {
			attempt++
			recovery = "resumed"
		}
		result, err = harness.Provider.CreateSTRIDELeadResponse(ctx, STRIDELeadResponsesRequest{
			Model: defaultSTRIDELeadHarnessModel, ReasoningEffort: defaultSTRIDELeadHarnessReasoningEffort, Instructions: strings.TrimSpace(request.Instructions),
			Input: strings.TrimSpace(request.Input), IdempotencyKey: sha256Hex([]byte(requestDigest + "\x00" + strconv.Itoa(attempt))), PreviousResponseID: strings.TrimSpace(request.PreviousResponseID),
			ConversationID: strings.TrimSpace(request.ConversationID), ToolAdmissionDigest: tools.AdmissionDigest, Tools: tools.Tools,
			ToolAgent: tools.Agent, ToolManifestDigest: tools.ManifestDigest,
			Metadata: map[string]string{"run_id": card.Run.ID, "assignment_id": scout.ID, "specialist_assignment_id": specialist.ID, "source_manifest_digest": sourceManifest.SourceManifestHash, "authority_fence_digest": scout.AuthorityFenceDigest, "spend_boundary_digest": spendBoundaryDigest},
		})
	}
	if err != nil {
		return STRIDELeadProviderReceipt{}, errors.Join(ErrSTRIDELeadHarnessRecoverable, err)
	}
	if result.Model != defaultSTRIDELeadHarnessModel || !strideIdentifier(result.ResponseID) || !isHexDigest(result.EnvelopeDigest) ||
		(recovery == "retrieved" && (card.Provider == nil || result.ResponseID != card.Provider.ResponseID)) {
		return STRIDELeadProviderReceipt{}, ErrSTRIDELeadHarnessRecoverable
	}
	receipt := STRIDELeadProviderReceipt{
		RunID: card.Run.ID, AssignmentID: scout.ID, Provider: providerOpenAI, Model: result.Model, ResponseID: result.ResponseID,
		PreviousResponseID: result.PreviousResponseID, ConversationID: result.ConversationID, Status: result.Status, Recovery: recovery,
		Attempt:       attempt,
		RequestDigest: requestDigest, SpendBoundaryDigest: spendBoundaryDigest, SourceManifestDigest: sourceManifest.SourceManifestHash, AuthorityFenceDigest: scout.AuthorityFenceDigest,
		ToolAdmissionDigest: tools.AdmissionDigest, ProviderEnvelopeDigest: result.EnvelopeDigest, ObservedAt: now,
	}
	if receipt.Validate() != nil {
		return STRIDELeadProviderReceipt{}, ErrSTRIDELeadHarnessInvalid
	}
	if card.Provider != nil && sameSTRIDELeadProviderState(*card.Provider, receipt) {
		if err := harness.ensureProviderMilestone(card.Run, scout, *card.Provider, card.Phase); err != nil {
			return STRIDELeadProviderReceipt{}, err
		}
		return *card.Provider, nil
	}
	event := strideWorkRunEvent(card.Run, "provider-"+receipt.ResponseID+"-"+receipt.Status, STRIDEProviderResponseRecorded, STRIDEWorkAgentScout,
		"Scout recorded the durable provider response as "+strings.ReplaceAll(receipt.Status, "_", " "), now)
	event.Agent, event.AssignmentID, event.ProviderReceipt = scout.Agent, scout.ID, &receipt
	if _, _, err := harness.WorkRuns.Append(event); err != nil {
		return STRIDELeadProviderReceipt{}, err
	}
	if err := harness.ensureProviderMilestone(card.Run, scout, receipt, card.Phase); err != nil {
		return STRIDELeadProviderReceipt{}, err
	}
	return receipt, nil
}

func (harness *STRIDELeadHarness) ensureProviderMilestone(run STRIDECanonicalWorkRun, assignment STRIDECanonicalAgentAssignment, receipt STRIDELeadProviderReceipt, phase STRIDEWorkRunPhase) error {
	switch receipt.Status {
	case "queued", "in_progress":
		return harness.appendMilestone(run, assignment, "provider_started", phase, "Scout started the durable background response", receipt.ObservedAt)
	case "completed":
		return harness.appendMilestone(run, assignment, "provider_completed", phase, "Scout recovered the completed provider response", receipt.ObservedAt)
	default:
		return nil
	}
}

func (harness *STRIDELeadHarness) appendMilestone(run STRIDECanonicalWorkRun, assignment STRIDECanonicalAgentAssignment, kind string, phase STRIDEWorkRunPhase, summary string, at time.Time) error {
	if card, err := harness.WorkRuns.SideCard(run.ID); err == nil {
		for _, existing := range card.Milestones {
			if existing.Kind == kind {
				return nil
			}
		}
	}
	milestone := STRIDELeadMilestone{RunID: run.ID, AssignmentID: assignment.ID, Kind: kind, Phase: phase, Summary: summary, CommittedAt: at.UTC()}
	if milestone.Validate() != nil {
		return ErrSTRIDELeadHarnessInvalid
	}
	event := strideWorkRunEvent(run, "milestone-"+kind, STRIDEMilestoneRecorded, STRIDEWorkAgentScout, summary, at)
	event.Agent, event.AssignmentID, event.Milestone = assignment.Agent, assignment.ID, &milestone
	_, _, err := harness.WorkRuns.Append(event)
	return err
}

// CompletedOutput deliberately re-reads a stored response instead of placing
// provider-authored content in the WorkRun event log. This is the restart seam
// for a process that durably recorded completion before staging its private
// shadow candidate.
func (harness *STRIDELeadHarness) CompletedOutput(ctx context.Context, receipt STRIDELeadProviderReceipt) (string, error) {
	if harness == nil || harness.Provider == nil || receipt.Validate() != nil || receipt.Status != "completed" {
		return "", ErrSTRIDELeadHarnessInvalid
	}
	result, err := harness.Provider.RetrieveSTRIDELeadResponse(ctx, receipt.ResponseID)
	if err != nil {
		return "", errors.Join(ErrSTRIDELeadHarnessRecoverable, err)
	}
	if result.ResponseID != receipt.ResponseID || result.Model != receipt.Model || result.Status != "completed" ||
		(receipt.ConversationID != "" && result.ConversationID != receipt.ConversationID) || strings.TrimSpace(result.OutputText) == "" {
		return "", ErrSTRIDELeadHarnessRecoverable
	}
	return result.OutputText, nil
}

func strideLeadHarnessAssignments(card STRIDEWorkRunSideCard) (STRIDECanonicalAgentAssignment, STRIDECanonicalAgentAssignment, bool) {
	want, _ := strideWorkRunSpecialist(card.Run.OutputKind)
	var scout, specialist STRIDECanonicalAgentAssignment
	for _, state := range card.Assignments {
		if !oneOf(state.Status, "assigned", "running", "completed") {
			continue
		}
		if state.Assignment.Agent == STRIDEWorkAgentScout && state.Assignment.OutputContract == "orchestration" {
			scout = state.Assignment
		}
		if state.Assignment.Agent == want {
			specialist = state.Assignment
		}
	}
	return scout, specialist, scout.ID != "" && specialist.ID != ""
}

func leadProviderTerminal(status string) bool {
	return oneOf(status, "completed", "incomplete", "failed", "cancelled")
}

func sameSTRIDELeadProviderState(left, right STRIDELeadProviderReceipt) bool {
	return left.ResponseID == right.ResponseID && left.Attempt == right.Attempt && left.Status == right.Status && left.ProviderEnvelopeDigest == right.ProviderEnvelopeDigest
}

type STRIDELeadFailureCause string

const (
	STRIDELeadFailureMissingAuthority STRIDELeadFailureCause = "missing_authority"
	STRIDELeadFailureHumanDecision    STRIDELeadFailureCause = "consequential_human_decision"
	STRIDELeadFailureProvider         STRIDELeadFailureCause = "provider_failure"
	STRIDELeadFailureParser           STRIDELeadFailureCause = "parser_failure"
	STRIDELeadFailureCritic           STRIDELeadFailureCause = "critic_failure"
	STRIDELeadFailureInternal         STRIDELeadFailureCause = "internal_failure"
)

func classifySTRIDELeadFailure(cause STRIDELeadFailureCause) string {
	if cause == STRIDELeadFailureMissingAuthority || cause == STRIDELeadFailureHumanDecision {
		return "needs_you"
	}
	return "retrying"
}
