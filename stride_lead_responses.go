package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	strideLeadResponsesEndpoint     = "https://api.openai.com/v1/responses"
	strideLeadResponsesMaxBodyBytes = 4 << 20
	strideLeadResponsesTimeout      = 2 * time.Minute
)

type strideLeadResponsesHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type strideLeadResponsesClient struct {
	httpClient strideLeadResponsesHTTPDoer
	Endpoint   string
	APIKey     string
	ProjectID  string
}

func newSTRIDELeadResponsesClient(apiKey, projectID string) *strideLeadResponsesClient {
	return &strideLeadResponsesClient{
		httpClient: aiProviderHTTPClient(strideLeadResponsesTimeout), Endpoint: strideLeadResponsesEndpoint,
		APIKey: strings.TrimSpace(apiKey), ProjectID: strings.TrimSpace(projectID),
	}
}

type strideLeadConversationWire struct {
	ID string `json:"id"`
}

type strideLeadResponsesWireRequest struct {
	Model              string                      `json:"model"`
	Instructions       string                      `json:"instructions"`
	Input              string                      `json:"input"`
	Store              bool                        `json:"store"`
	Background         bool                        `json:"background"`
	PreviousResponseID string                      `json:"previous_response_id,omitempty"`
	Conversation       *strideLeadConversationWire `json:"conversation,omitempty"`
	Tools              []map[string]any            `json:"tools,omitempty"`
	ToolChoice         string                      `json:"tool_choice,omitempty"`
	ParallelToolCalls  bool                        `json:"parallel_tool_calls"`
	Metadata           map[string]string           `json:"metadata"`
	Reasoning          map[string]string           `json:"reasoning"`
}

func (client *strideLeadResponsesClient) CreateSTRIDELeadResponse(ctx context.Context, request STRIDELeadResponsesRequest) (STRIDELeadResponsesResult, error) {
	if err := client.validate(); err != nil {
		return STRIDELeadResponsesResult{}, err
	}
	if request.Model != defaultSTRIDELeadHarnessModel || request.ReasoningEffort != defaultSTRIDELeadHarnessReasoningEffort || strings.TrimSpace(request.Instructions) == "" || strings.TrimSpace(request.Input) == "" ||
		!isHexDigest(request.IdempotencyKey) || request.PreviousResponseID != "" && request.ConversationID != "" ||
		!validOptionalSTRIDEID(request.PreviousResponseID) || !validOptionalSTRIDEID(request.ConversationID) || len(request.Metadata) == 0 {
		return STRIDELeadResponsesResult{}, ErrSTRIDELeadHarnessInvalid
	}
	if err := validateSTRIDELeadToolAdmission(request); err != nil {
		return STRIDELeadResponsesResult{}, err
	}
	wire := strideLeadResponsesWireRequest{
		Model: strings.TrimSpace(request.Model), Instructions: strings.TrimSpace(request.Instructions), Input: strings.TrimSpace(request.Input),
		Store: true, Background: true, PreviousResponseID: strings.TrimSpace(request.PreviousResponseID), Tools: request.Tools,
		ToolChoice: "auto", ParallelToolCalls: false, Metadata: request.Metadata,
		Reasoning: map[string]string{"effort": defaultSTRIDELeadHarnessReasoningEffort},
	}
	if request.ConversationID != "" {
		wire.Conversation = &strideLeadConversationWire{ID: strings.TrimSpace(request.ConversationID)}
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		return STRIDELeadResponsesResult{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.Endpoint, bytes.NewReader(raw))
	if err != nil {
		return STRIDELeadResponsesResult{}, err
	}
	client.authorize(httpRequest)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Idempotency-Key", request.IdempotencyKey)
	return client.do(httpRequest)
}

func (client *strideLeadResponsesClient) RetrieveSTRIDELeadResponse(ctx context.Context, responseID string) (STRIDELeadResponsesResult, error) {
	if err := client.validate(); err != nil || !strideIdentifier(responseID) {
		return STRIDELeadResponsesResult{}, ErrSTRIDELeadHarnessInvalid
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, client.Endpoint+"/"+url.PathEscape(responseID), nil)
	if err != nil {
		return STRIDELeadResponsesResult{}, err
	}
	client.authorize(httpRequest)
	return client.do(httpRequest)
}

func (client *strideLeadResponsesClient) validate() error {
	if client == nil || client.httpClient == nil || client.Endpoint != strideLeadResponsesEndpoint || strings.TrimSpace(client.APIKey) == "" || strings.TrimSpace(client.ProjectID) == "" {
		return ErrSTRIDELeadHarnessFenced
	}
	return nil
}

func (client *strideLeadResponsesClient) authorize(request *http.Request) {
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(client.APIKey))
	request.Header.Set("OpenAI-Project", strings.TrimSpace(client.ProjectID))
}

func (client *strideLeadResponsesClient) do(request *http.Request) (STRIDELeadResponsesResult, error) {
	response, err := client.httpClient.Do(request)
	if err != nil {
		return STRIDELeadResponsesResult{}, errors.Join(ErrSTRIDELeadHarnessRecoverable, err)
	}
	if response == nil || response.Body == nil {
		return STRIDELeadResponsesResult{}, errors.Join(ErrSTRIDELeadHarnessRecoverable, errors.New("Responses returned no body"))
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, strideLeadResponsesMaxBodyBytes+1))
	if err != nil || len(raw) > strideLeadResponsesMaxBodyBytes {
		return STRIDELeadResponsesResult{}, errors.Join(ErrSTRIDELeadHarnessRecoverable, errors.New("Responses body is unavailable"))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return STRIDELeadResponsesResult{}, errors.Join(ErrSTRIDELeadHarnessRecoverable, fmt.Errorf("Responses HTTP %d", response.StatusCode))
	}
	return parseSTRIDELeadResponsesEnvelope(raw)
}

func validateSTRIDELeadToolAdmission(request STRIDELeadResponsesRequest) error {
	if !validSTRIDEWorkAgent(request.ToolAgent) || request.ToolAgent == STRIDEWorkAgentScout || !allDigests(request.ToolManifestDigest, request.ToolAdmissionDigest) {
		return ErrSTRIDELeadHarnessInvalid
	}
	outputKind := "research"
	if request.ToolAgent == STRIDEWorkAgentPresenter {
		outputKind = "presentation"
	}
	want, err := admitSTRIDELeadTools(outputKind)
	if err != nil || want.Agent != request.ToolAgent || want.ManifestDigest != request.ToolManifestDigest || want.AdmissionDigest != request.ToolAdmissionDigest {
		return ErrSTRIDELeadHarnessInvalid
	}
	left, leftErr := canonicalJSON(want.Tools)
	right, rightErr := canonicalJSON(request.Tools)
	if leftErr != nil || rightErr != nil || !bytes.Equal(left, right) {
		return ErrSTRIDELeadHarnessInvalid
	}
	return nil
}

func parseSTRIDELeadResponsesEnvelope(raw []byte) (STRIDELeadResponsesResult, error) {
	unique := json.NewDecoder(bytes.NewReader(raw))
	unique.UseNumber()
	if _, err := decodeUniqueJSONValue(unique); err != nil {
		return STRIDELeadResponsesResult{}, errors.Join(ErrSTRIDELeadHarnessRecoverable, errors.New("invalid Responses envelope"))
	}
	if token, err := unique.Token(); err != io.EOF {
		if err == nil {
			return STRIDELeadResponsesResult{}, errors.Join(ErrSTRIDELeadHarnessRecoverable, fmt.Errorf("unexpected trailing Responses token %v", token))
		}
		return STRIDELeadResponsesResult{}, errors.Join(ErrSTRIDELeadHarnessRecoverable, err)
	}
	var wire struct {
		ID                 string                      `json:"id"`
		Model              string                      `json:"model"`
		Status             string                      `json:"status"`
		PreviousResponseID string                      `json:"previous_response_id"`
		Conversation       *strideLeadConversationWire `json:"conversation"`
		Output             []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Error             json.RawMessage `json:"error"`
		IncompleteDetails json.RawMessage `json:"incomplete_details"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&wire); err != nil || ensureJSONEOF(decoder) != nil {
		return STRIDELeadResponsesResult{}, errors.Join(ErrSTRIDELeadHarnessRecoverable, errors.New("invalid Responses envelope"))
	}
	result := STRIDELeadResponsesResult{
		ResponseID: strings.TrimSpace(wire.ID), PreviousResponseID: strings.TrimSpace(wire.PreviousResponseID),
		Model: strings.TrimSpace(wire.Model), Status: strings.ToLower(strings.TrimSpace(wire.Status)), EnvelopeDigest: sha256Hex(raw),
	}
	if wire.Conversation != nil {
		result.ConversationID = strings.TrimSpace(wire.Conversation.ID)
	}
	if !strideIdentifier(result.ResponseID) || !strideIdentifier(result.Model) || !oneOf(result.Status, "queued", "in_progress", "completed", "incomplete", "failed", "cancelled") ||
		!validOptionalSTRIDEID(result.PreviousResponseID) || !validOptionalSTRIDEID(result.ConversationID) {
		return STRIDELeadResponsesResult{}, ErrSTRIDELeadHarnessInvalid
	}
	for _, item := range wire.Output {
		if item.Type != "message" || item.Role != "assistant" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "output_text" {
				result.OutputText += content.Text
			}
		}
	}
	if result.Status == "completed" && strings.TrimSpace(result.OutputText) == "" {
		return STRIDELeadResponsesResult{}, errors.Join(ErrSTRIDELeadHarnessRecoverable, errors.New("completed Responses envelope has no output text"))
	}
	return result, nil
}

var _ STRIDELeadResponsesProvider = (*strideLeadResponsesClient)(nil)
