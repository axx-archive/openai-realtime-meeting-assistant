package main

// This is execution evidence for the existing Document Studio route, not a
// qualified Dissent plan. It adds no provider, tools, or model calls. The host
// persists this metadata beside the terminal artifact through its existing
// authorization boundary. Rendered review remains a separate document gate.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

const dissentDocumentReceiptKey = "dissentExecutionReceipt"
const dissentDocumentReceiptVersion = "stride.dissent.document-execution.v1"

type dissentDocumentExecutionReceipt struct {
	Version         string                       `json:"version"`
	ArtifactID      string                       `json:"artifactId"`
	RunID           string                       `json:"runId"`
	OutputVersion   int                          `json:"outputVersion"`
	OutputContract  string                       `json:"outputContract"`
	RequestDigest   string                       `json:"requestDigest"`
	OutputDigest    string                       `json:"outputDigest"`
	Provider        string                       `json:"provider"`
	RequestedModel  string                       `json:"requestedModel"`
	ActualModel     string                       `json:"actualModel,omitempty"`
	ReasoningEffort string                       `json:"reasoningEffort"`
	ResponseID      string                       `json:"responseId,omitempty"`
	Usage           *openAIResponsesUsage        `json:"usage,omitempty"`
	Plan            *dissentInternalDocumentPlan `json:"plan,omitempty"`
	FallbackUsed    bool                         `json:"fallbackUsed"`
	EvidenceStatus  string                       `json:"evidenceStatus"`
	Qualification   string                       `json:"qualification"`
	Assurance       string                       `json:"assurance"`
	Integrity       string                       `json:"integrity"`
	Digest          string                       `json:"digest"`
}

type dissentDocumentReceiptCollector struct {
	mu      sync.Mutex
	receipt *dissentDocumentExecutionReceipt
}
type dissentDocumentReceiptContextKey struct{}

func withDissentDocumentReceipt(ctx context.Context, thread scoutAgentThread) (context.Context, *dissentDocumentReceiptCollector) {
	if strings.TrimSpace(thread.Artifact.Metadata["outputContract"]) != documentReportOutputContract {
		return ctx, nil
	}
	collector := &dissentDocumentReceiptCollector{}
	return context.WithValue(ctx, dissentDocumentReceiptContextKey{}, collector), collector
}

func dissentReceiptDigest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	var decoded any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		return "", err
	}
	canonical, err := dissentCanonicalJSON(decoded)
	if err != nil {
		return "", err
	}
	return dissentSha256Hex(canonical), nil
}

// The collector is request-scoped. Metadata joins the runner's terminal result,
// so there is no competing write against the in-flight artifact or its ACL.
func (collector *dissentDocumentReceiptCollector) mergeMetadata(metadata map[string]string) (map[string]string, error) {
	if collector == nil {
		return metadata, nil
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	if collector.receipt == nil {
		return metadata, nil
	}
	raw, err := json.Marshal(collector.receipt)
	if err != nil {
		return metadata, err
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata[dissentDocumentReceiptKey] = string(raw)
	metadata["dissentExecutionDigest"] = collector.receipt.Digest
	metadata["dissentExecutionEvidence"] = collector.receipt.EvidenceStatus
	metadata["dissentQualification"] = collector.receipt.Qualification
	metadata["dissentAssurance"] = collector.receipt.Assurance
	return metadata, nil
}

func callDocumentWorkWithReceipt(ctx context.Context, thread scoutAgentThread, apiKey string, request openAITextRequest, responder openAITextResponder) (string, error) {
	collector, _ := ctx.Value(dissentDocumentReceiptContextKey{}).(*dissentDocumentReceiptCollector)
	if collector == nil || strings.TrimSpace(thread.Artifact.Metadata["outputContract"]) != documentReportOutputContract {
		return callOpenAITextWithBoundedInvocationRetry(ctx, apiKey, request, responder)
	}
	if request.EnableWebSearch || request.MaxToolCalls != 0 || len(request.Attachments) != 0 {
		return "", fmt.Errorf("document execution evidence accepts only the existing no-tools text route")
	}
	requestDigest, err := dissentReceiptDigest(durableOpenAIRequest(request))
	if err != nil {
		return "", err
	}
	plan, err := planInternalDocumentWork(thread, request)
	if err != nil {
		return "", err
	}
	capture := &openAIResponseReceiptCapture{}
	provenance := &providerCallProvenanceCapture{}
	callCtx := withProviderCallProvenanceCapture(withOpenAIResponseReceiptCapture(ctx, capture), provenance)
	output, err := callOpenAITextWithBoundedInvocationRetry(callCtx, apiKey, request, responder)
	if err != nil {
		return "", err
	}
	output = strings.TrimSpace(output)
	if output == "" {
		return "", fmt.Errorf("document execution returned no text")
	}
	receipt := dissentDocumentExecutionReceipt{
		Version: dissentDocumentReceiptVersion, ArtifactID: thread.Artifact.ID, RunID: thread.ID, OutputVersion: artifactVersion(thread.Artifact) + 1,
		OutputContract: documentReportOutputContract, RequestDigest: requestDigest,
		OutputDigest: dissentSha256Hex(output), Provider: providerOpenAI,
		RequestedModel: request.Model, ReasoningEffort: request.ReasoningEffort,
		EvidenceStatus: "unavailable", Qualification: "not_evaluated",
		Assurance: "not_performed", Integrity: "content_checksum",
		Plan: plan,
	}
	id, model, usage, observed := capture.snapshot()
	if observed {
		actual, hasProvenance := provenance.snapshot()
		if model != request.Model {
			if !hasProvenance || !actual.FallbackUsed || actual.Provider != providerOpenAI || actual.PrimaryModel != request.Model || actual.Model != model {
				return "", fmt.Errorf("document executor does not match the requested route or an observed admitted fallback")
			}
			receipt.FallbackUsed = true
		}
		if id == "" || model == "" || usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.InputTokensDetails.CachedTokens < 0 || usage.InputTokensDetails.CachedTokens > usage.InputTokens || usage.OutputTokensDetails.ReasoningTokens < 0 || usage.OutputTokensDetails.ReasoningTokens > usage.OutputTokens || usage.TotalTokens < 0 {
			return "", fmt.Errorf("document executor returned invalid usage evidence")
		}
		receipt.ResponseID, receipt.ActualModel, receipt.Usage = id, model, &usage
		receipt.EvidenceStatus = "observed"
	}
	receipt.Digest, err = dissentReceiptDigest(receipt)
	if err != nil {
		return "", err
	}
	collector.mu.Lock()
	collector.receipt = &receipt
	collector.mu.Unlock()
	return output, nil
}

// A checksum detects changed content; it is deliberately not called a signed
// receipt or independent verification. Artifact custody remains STRIDE-owned.
func verifyDissentDocumentReceipt(raw, artifactID, output string) error {
	var receipt dissentDocumentExecutionReceipt
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("document execution receipt has trailing data")
	}
	if receipt.Version != dissentDocumentReceiptVersion || receipt.ArtifactID != artifactID || receipt.OutputContract != documentReportOutputContract || receipt.OutputDigest != dissentSha256Hex(strings.TrimSpace(output)) || receipt.Qualification != "not_evaluated" || receipt.Assurance != "not_performed" || receipt.Integrity != "content_checksum" {
		return fmt.Errorf("document execution receipt binding is invalid")
	}
	expected := receipt.Digest
	receipt.Digest = ""
	digest, err := dissentReceiptDigest(receipt)
	if err != nil || expected == "" || digest != expected {
		return fmt.Errorf("document execution receipt checksum changed")
	}
	if receipt.EvidenceStatus != "observed" || receipt.ResponseID == "" || receipt.ActualModel == "" || receipt.Usage == nil {
		return fmt.Errorf("document execution provider evidence is unavailable")
	}
	return nil
}
