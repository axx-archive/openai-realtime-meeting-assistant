package main

import (
	"fmt"
	"strings"
)

// Internal routing is host admission, not empirical model qualification. The
// qualified Dissent compiler and its productionEligible gate are unchanged.
type dissentInternalDocumentPlan struct {
	Version                   string   `json:"version"`
	Policy                    string   `json:"policy"`
	Task                      string   `json:"task"`
	WorkClasses               []string `json:"workClasses"`
	Provider                  string   `json:"provider"`
	Model                     string   `json:"model"`
	ReasoningEffort           string   `json:"reasoningEffort"`
	MaxOutputTokens           int      `json:"maxOutputTokens"`
	DeadlineMS                int64    `json:"deadlineMs"`
	RequestDigest             string   `json:"requestDigest"`
	Tools                     string   `json:"tools"`
	SideEffects               string   `json:"sideEffects"`
	Qualification             string   `json:"qualification"`
	JudgmentRequired          bool     `json:"judgmentRequired"`
	IndependentReviewRequired bool     `json:"independentReviewRequired"`
	IndependentReviewStatus   string   `json:"independentReviewStatus"`
	Digest                    string   `json:"digest"`
}

func planInternalDocumentWork(thread scoutAgentThread, request openAITextRequest) (*dissentInternalDocumentPlan, error) {
	if strings.TrimSpace(thread.Artifact.Metadata["outputContract"]) != documentReportOutputContract {
		return nil, nil
	}
	if request.EnableWebSearch || request.MaxToolCalls != 0 || len(request.Attachments) != 0 {
		return nil, fmt.Errorf("internal document route requires bounded text without tools")
	}
	// These are existing supported runtime profiles, selected by the host's
	// durable stage contract. Neither prompt text nor a client model name grants
	// a route. A grounded finished document gets the existing stronger writer.
	model, effort, task := meetingBrainModel(), meetingBrainReasoningEffort(), "document_draft"
	if agentThreadUsesGroundedDeliverableContract(thread) {
		model, effort, task = researchModel(), researchReasoningEffort(), "grounded_document"
	}
	if request.Model != model || request.ReasoningEffort != effort {
		return nil, fmt.Errorf("internal document request differs from the host-selected profile")
	}
	if request.MaxOutputTokens < 1 || request.MaxOutputTokens > agentThreadMaxOutputTokensForThread(thread) {
		return nil, fmt.Errorf("internal document request exceeds its stage token budget")
	}
	requestDigest, err := dissentReceiptDigest(durableOpenAIRequest(request))
	if err != nil {
		return nil, err
	}
	// Only a server-admitted authority fact can require consequential judgment.
	// Business/security words classify the task but never authorize an action or
	// turn an ordinary private brief into an external publication request.
	consequential := normalizeCodexJobAuthority(thread.Artifact.Metadata["authority"]) == codexJobAuthorityExternalWrite
	plan := &dissentInternalDocumentPlan{
		Version: "stride.dissent.internal-document-plan.v1", Policy: "experimental_internal",
		Task: task, WorkClasses: dissentClassifyWorkClasses(dissentCheckInput{Objective: thread.Query, ArtifactKind: "document"}),
		Provider: providerOpenAI, Model: model, ReasoningEffort: effort, MaxOutputTokens: request.MaxOutputTokens,
		DeadlineMS: agentThreadRequestTimeout(thread).Milliseconds(), RequestDigest: requestDigest,
		Tools: "none", SideEffects: "none", Qualification: "not_evaluated",
		JudgmentRequired: consequential, IndependentReviewRequired: consequential, IndependentReviewStatus: "not_required",
	}
	if consequential {
		plan.IndependentReviewStatus = "independent_review_unavailable"
	}
	plan.Digest, err = dissentReceiptDigest(plan)
	return plan, err
}

func validateInternalDocumentPlan(plan *dissentInternalDocumentPlan, thread scoutAgentThread, request openAITextRequest) error {
	if plan == nil {
		return nil
	} // Older frozen operations remain readable.
	expected, err := planInternalDocumentWork(thread, request)
	if err != nil {
		return err
	}
	actualDigest, err := dissentReceiptDigest(plan)
	if err != nil {
		return err
	}
	expectedDigest, err := dissentReceiptDigest(expected)
	if err != nil || expected == nil || actualDigest != expectedDigest {
		return fmt.Errorf("frozen internal document plan changed")
	}
	return nil
}
