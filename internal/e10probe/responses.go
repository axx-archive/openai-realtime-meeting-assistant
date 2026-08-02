package e10probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The Responses probe is intentionally a separate contract from access and
// transcription probes. It admits one fixed, non-sensitive synthetic task and
// never exposes a caller-controlled prompt, schema, tool, or reasoning level.
const (
	responsesEndpoint             = "/responses"
	responsesTimeout              = 30 * time.Second
	responsesMaxBodyBytes   int64 = 256 << 10
	responsesMaxOutputBytes       = 4 << 10
	responsesMaxInputTokens int64 = 1024

	responsesPriceSourceURL      = "https://developers.openai.com/api/docs/pricing"
	responsesPriceRevision       = "2026-08-01"
	responsesPromptSourceURL     = "https://developers.openai.com/api/docs/guides/model-guidance?model=gpt-5.6-sol"
	responsesSchemaSourceURL     = "https://developers.openai.com/api/docs/guides/structured-outputs"
	responsesAPISourceURL        = "https://developers.openai.com/api/reference/resources/responses/methods/create"
	responsesModelSourceRevision = "2026-08-01"

	responsesFixedInstructions = "Return exactly one JSON object matching the supplied schema. Use only the stated synthetic facts."
	responsesFixedInput        = "Synthetic STRIDE E10 check: add 17 and 25. Use label probe-ok and mark accepted true."
	responsesSafetyIdentifier  = "stride-e10-synthetic-responses-probe-v1"
	responsesSchemaName        = "stride_e10_contract_probe"
	responsesSchemaJSON        = `{"type":"object","properties":{"label":{"type":"string","enum":["probe-ok"]},"sum":{"type":"integer","enum":[42]},"accepted":{"type":"boolean","enum":[true]}},"required":["label","sum","accepted"],"additionalProperties":false}`
)

type responsesProfile struct {
	Effort                  string
	MaxOutputTokens         int64
	HardMaxUSD              float64
	InputUSDPerMillion      float64
	CachedUSDPerMillion     float64
	CacheWriteUSDPerMillion float64
	OutputUSDPerMillion     float64
}

var responsesProfiles = map[string]responsesProfile{
	"gpt-5.6-luna": {
		Effort: "low", MaxOutputTokens: 256, HardMaxUSD: 0.005,
		InputUSDPerMillion: 0.20, CachedUSDPerMillion: 0.02, CacheWriteUSDPerMillion: 0.25, OutputUSDPerMillion: 1.20,
	},
	"gpt-5.6-terra": {
		Effort: "low", MaxOutputTokens: 384, HardMaxUSD: 0.010,
		InputUSDPerMillion: 2.00, CachedUSDPerMillion: 0.20, CacheWriteUSDPerMillion: 2.50, OutputUSDPerMillion: 12.00,
	},
	"gpt-5.6-sol": {
		Effort: "medium", MaxOutputTokens: 512, HardMaxUSD: 0.025,
		InputUSDPerMillion: 5.00, CachedUSDPerMillion: 0.50, CacheWriteUSDPerMillion: 6.25, OutputUSDPerMillion: 30.00,
	},
}

// ResponsesReceipt is a body-free, private evidence record for one bounded
// Responses API contract attempt. It deliberately contains no raw prompt,
// schema, output, provider identifier, credential, or project value.
type ResponsesReceipt struct {
	Schema                     string  `json:"schema"`
	Classification             string  `json:"classification"`
	Success                    bool    `json:"success"`
	AcceptedOutput             bool    `json:"acceptedOutput"`
	Outcome                    string  `json:"outcome"`
	FailureClass               string  `json:"failureClass,omitempty"`
	Endpoint                   string  `json:"endpoint"`
	Method                     string  `json:"method"`
	Model                      string  `json:"model"`
	ReasoningEffort            string  `json:"reasoningEffort"`
	ResponseModelSHA256        string  `json:"responseModelSha256,omitempty"`
	CandidateManifestSHA256    string  `json:"candidateManifestSha256"`
	AcknowledgementSHA256      string  `json:"acknowledgementSha256"`
	PromptSHA256               string  `json:"promptSha256"`
	OutputSchemaSHA256         string  `json:"outputSchemaSha256"`
	SafetyIdentifierSHA256     string  `json:"safetyIdentifierSha256"`
	RequestShapeSHA256         string  `json:"requestShapeSha256"`
	PriceRowSHA256             string  `json:"priceRowSha256"`
	PriceSourceURL             string  `json:"priceSourceUrl"`
	PriceSourceRevision        string  `json:"priceSourceRevision"`
	ModelSourceURL             string  `json:"modelSourceUrl"`
	ModelSourceRevision        string  `json:"modelSourceRevision"`
	PromptSourceURL            string  `json:"promptSourceUrl"`
	SchemaSourceURL            string  `json:"schemaSourceUrl"`
	APISourceURL               string  `json:"apiSourceUrl"`
	ResponseContractSHA256     string  `json:"responseContractSha256"`
	CredentialScope            string  `json:"credentialScope"`
	RequestProjectSHA256       string  `json:"requestProjectSha256,omitempty"`
	ExpectedProjectSHA256      string  `json:"expectedProjectSha256,omitempty"`
	ResponseProjectSHA256      string  `json:"responseProjectSha256,omitempty"`
	ResponseOrganizationSHA256 string  `json:"responseOrganizationSha256,omitempty"`
	AttributionVerified        bool    `json:"attributionVerified"`
	AttributionState           string  `json:"attributionState"`
	HTTPStatus                 int     `json:"httpStatus"`
	LatencyMS                  int64   `json:"latencyMs"`
	RequestIDSHA256            string  `json:"requestIdSha256,omitempty"`
	ResponseIDSHA256           string  `json:"responseIdSha256,omitempty"`
	OutputSHA256               string  `json:"outputSha256,omitempty"`
	OutputByteCount            int     `json:"outputByteCount,omitempty"`
	InputTokens                int64   `json:"inputTokens,omitempty"`
	CachedInputTokens          int64   `json:"cachedInputTokens,omitempty"`
	CacheWriteTokens           int64   `json:"cacheWriteTokens,omitempty"`
	OutputTokens               int64   `json:"outputTokens,omitempty"`
	ReasoningTokens            int64   `json:"reasoningTokens,omitempty"`
	TotalTokens                int64   `json:"totalTokens,omitempty"`
	MaxInputTokens             int64   `json:"maxInputTokens"`
	MaxOutputTokens            int64   `json:"maxOutputTokens"`
	InputUSDPerMillion         float64 `json:"inputUsdPerMillion"`
	CachedUSDPerMillion        float64 `json:"cachedUsdPerMillion"`
	CacheWriteUSDPerMillion    float64 `json:"cacheWriteUsdPerMillion"`
	OutputUSDPerMillion        float64 `json:"outputUsdPerMillion"`
	WorstCaseEstimatedUSD      float64 `json:"worstCaseEstimatedUsd"`
	ComputedCostUSD            float64 `json:"computedCostUsd"`
	RequestedMaxUSD            float64 `json:"requestedMaxUsd"`
	HardMaxUSD                 float64 `json:"hardMaxUsd"`
	RequestBodyBytes           int     `json:"requestBodyBytes"`
	CreatedAt                  string  `json:"createdAt"`
}

type responsesRequest struct {
	Model              string                      `json:"model"`
	Instructions       string                      `json:"instructions"`
	Input              string                      `json:"input"`
	Store              bool                        `json:"store"`
	Background         bool                        `json:"background"`
	Stream             bool                        `json:"stream"`
	Tools              []any                       `json:"tools"`
	ToolChoice         string                      `json:"tool_choice"`
	ParallelToolCalls  bool                        `json:"parallel_tool_calls"`
	MaxOutputTokens    int64                       `json:"max_output_tokens"`
	Reasoning          responsesReasoningRequest   `json:"reasoning"`
	Text               responsesTextRequest        `json:"text"`
	SafetyIdentifier   string                      `json:"safety_identifier"`
	ServiceTier        string                      `json:"service_tier"`
	Truncation         string                      `json:"truncation"`
	PromptCacheOptions responsesPromptCacheOptions `json:"prompt_cache_options"`
}

type responsesReasoningRequest struct {
	Effort  string `json:"effort"`
	Context string `json:"context"`
}

type responsesTextRequest struct {
	Format responsesTextFormat `json:"format"`
}

type responsesTextFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict bool            `json:"strict"`
}

type responsesPromptCacheOptions struct {
	Mode string `json:"mode"`
}

type responsesAPIResponse struct {
	ID                string                  `json:"id"`
	Object            string                  `json:"object"`
	Status            string                  `json:"status"`
	Error             json.RawMessage         `json:"error"`
	IncompleteDetails json.RawMessage         `json:"incomplete_details"`
	Model             string                  `json:"model"`
	Store             *bool                   `json:"store"`
	Background        *bool                   `json:"background"`
	MaxOutputTokens   *int64                  `json:"max_output_tokens"`
	Reasoning         *responsesReasoningEcho `json:"reasoning"`
	ServiceTier       string                  `json:"service_tier"`
	Tools             []json.RawMessage       `json:"tools"`
	ToolChoice        string                  `json:"tool_choice"`
	ParallelToolCalls *bool                   `json:"parallel_tool_calls"`
	Truncation        string                  `json:"truncation"`
	Output            []responsesOutputItem   `json:"output"`
	Usage             *responsesProviderUsage `json:"usage"`
}

type responsesReasoningEcho struct {
	Effort  string `json:"effort"`
	Context string `json:"context"`
}

type responsesOutputItem struct {
	ID      string                   `json:"id"`
	Type    string                   `json:"type"`
	Status  string                   `json:"status"`
	Role    string                   `json:"role"`
	Content []responsesOutputContent `json:"content"`
}

type responsesOutputContent struct {
	Type        string            `json:"type"`
	Text        string            `json:"text"`
	Refusal     string            `json:"refusal"`
	Annotations []json.RawMessage `json:"annotations"`
}

type responsesProviderUsage struct {
	InputTokens        *int64                       `json:"input_tokens"`
	InputTokenDetails  *responsesInputTokenDetails  `json:"input_tokens_details"`
	OutputTokens       *int64                       `json:"output_tokens"`
	OutputTokenDetails *responsesOutputTokenDetails `json:"output_tokens_details"`
	TotalTokens        *int64                       `json:"total_tokens"`
}

type responsesInputTokenDetails struct {
	CachedTokens     *int64 `json:"cached_tokens"`
	CacheWriteTokens *int64 `json:"cache_write_tokens"`
}

type responsesOutputTokenDetails struct {
	ReasoningTokens *int64 `json:"reasoning_tokens"`
}

// RunResponses runs one fixed GPT-5.6 Responses contract attempt. cfg.Model
// selects one of the three fixed profiles; cfg.MaxUSD must admit the worst-case
// bounded request without exceeding the profile's immutable hard ceiling.
func RunResponses(ctx context.Context, cfg Config) (ResponsesReceipt, error) {
	profile, ok := responsesProfiles[cfg.Model]
	if !ok {
		return ResponsesReceipt{}, fmt.Errorf("model %q is not allowlisted for the Responses contract probe", cfg.Model)
	}
	if err := validateCommon(cfg); err != nil {
		return ResponsesReceipt{}, err
	}
	if cfg.Project != strings.TrimSpace(cfg.Project) {
		return ResponsesReceipt{}, errors.New("OPENAI_PROJECT_ID must be canonical without leading or trailing whitespace")
	}
	if cfg.AllowProjectScopedKey || strings.TrimSpace(cfg.Project) == "" {
		return ResponsesReceipt{}, errors.New("paid Responses probes require OPENAI_PROJECT_ID and --expected-project-sha256; project-scoped-key fallback is not permitted")
	}
	if err := validateProjectBinding(cfg); err != nil {
		return ResponsesReceipt{}, err
	}
	if err := validateResponsesBaseURL(cfg.BaseURL); err != nil {
		return ResponsesReceipt{}, err
	}
	if cfg.Client != nil && !responsesIsLoopbackBase(cfg.BaseURL) {
		return ResponsesReceipt{}, errors.New("custom HTTP clients are permitted only with the loopback test override")
	}
	if math.IsNaN(cfg.MaxUSD) || math.IsInf(cfg.MaxUSD, 0) || cfg.MaxUSD <= 0 || cfg.MaxUSD > profile.HardMaxUSD {
		return ResponsesReceipt{}, fmt.Errorf("--max-usd must be greater than 0 and no more than %.3f for %s", profile.HardMaxUSD, cfg.Model)
	}
	worstCase := responsesWorstCaseCost(profile)
	if worstCase > profile.HardMaxUSD {
		return ResponsesReceipt{}, errors.New("frozen Responses profile exceeds its immutable hard cost ceiling")
	}
	if cfg.MaxUSD < worstCase {
		return ResponsesReceipt{}, fmt.Errorf("--max-usd %.6f is below worst-case pre-admission estimate %.6f", cfg.MaxUSD, worstCase)
	}

	requestBody, err := json.Marshal(responsesRequestFor(cfg.Model, profile))
	if err != nil {
		return ResponsesReceipt{}, err
	}
	if len(requestBody) == 0 || int64(len(requestBody)) > responsesMaxBodyBytes {
		return ResponsesReceipt{}, errors.New("frozen Responses request exceeded its bounded body limit")
	}
	dir, err := newPrivateDir(cfg.ReceiptDir)
	if err != nil {
		return ResponsesReceipt{}, err
	}

	now := cfg.now
	if now == nil {
		now = time.Now
	}
	receipt := newResponsesReceipt(cfg, profile, requestBody, worstCase, now())
	base := strings.TrimRight(responsesResolvedBase(cfg.BaseURL), "/")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+responsesEndpoint, bytes.NewReader(requestBody))
	if err != nil {
		receipt.FailureClass = "request_construction"
		return receipt, writeResponsesReceiptThenReturn(dir, receipt, err)
	}
	request.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if strings.TrimSpace(cfg.Project) != "" {
		request.Header.Set("OpenAI-Project", cfg.Project)
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{}
	} else {
		clone := *client
		client = &clone
	}
	client.Timeout = responsesTimeout
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("redirects are not permitted for provider probes")
	}
	boundedContext, cancel := context.WithTimeout(request.Context(), responsesTimeout)
	defer cancel()
	request = request.WithContext(boundedContext)

	started := now()
	response, requestErr := client.Do(request)
	receipt.LatencyMS = now().Sub(started).Milliseconds()
	if requestErr != nil {
		receipt.FailureClass = "transport"
		return receipt, writeResponsesReceiptThenReturn(dir, receipt, errors.New("Responses provider transport failed"))
	}
	defer response.Body.Close()
	receipt.HTTPStatus = response.StatusCode
	if requestID := response.Header.Get("X-Request-ID"); requestID != "" {
		receipt.RequestIDSHA256 = digest(requestID)
	}
	if project := response.Header.Get("OpenAI-Project"); project != "" {
		receipt.ResponseProjectSHA256 = digest(project)
	}
	if organization := response.Header.Get("OpenAI-Organization"); organization != "" {
		receipt.ResponseOrganizationSHA256 = digest(organization)
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, responsesMaxBodyBytes))
		receipt.Outcome = "provider_error"
		receipt.FailureClass = failureClass(response.StatusCode)
		return receipt, writeResponsesReceiptThenReturn(dir, receipt, fmt.Errorf("provider returned HTTP %d (%s)", response.StatusCode, receipt.FailureClass))
	}
	if err := verifyResponsesAttribution(&receipt); err != nil {
		receipt.Outcome = "attribution_mismatch"
		receipt.FailureClass = "project_attribution"
		return receipt, writeResponsesReceiptThenReturn(dir, receipt, err)
	}
	if receipt.RequestIDSHA256 == "" {
		receipt.Outcome = "schema_mismatch"
		receipt.FailureClass = "request_id"
		return receipt, writeResponsesReceiptThenReturn(dir, receipt, errors.New("provider response omitted X-Request-ID"))
	}

	var providerResponse responsesAPIResponse
	if err := decodeResponsesDocument(response.Body, &providerResponse); err != nil {
		receipt.Outcome = "schema_mismatch"
		receipt.FailureClass = "response_schema"
		return receipt, writeResponsesReceiptThenReturn(dir, receipt, err)
	}
	if providerResponse.ID != "" && len(providerResponse.ID) <= 512 && strings.IndexByte(providerResponse.ID, 0) < 0 {
		receipt.ResponseIDSHA256 = digest(providerResponse.ID)
	}
	if providerResponse.Model != "" && len(providerResponse.Model) <= 128 && strings.IndexByte(providerResponse.Model, 0) < 0 {
		receipt.ResponseModelSHA256 = digest(providerResponse.Model)
	}
	outputText, usage, err := validateResponsesProviderResponse(providerResponse, cfg.Model, profile)
	if err != nil {
		receipt.Outcome = "schema_mismatch"
		receipt.FailureClass = "response_contract"
		return receipt, writeResponsesReceiptThenReturn(dir, receipt, err)
	}
	receipt.OutputSHA256 = digest(outputText)
	receipt.OutputByteCount = len(outputText)
	receipt.InputTokens = usage.InputTokens
	receipt.CachedInputTokens = usage.CachedInputTokens
	receipt.CacheWriteTokens = usage.CacheWriteTokens
	receipt.OutputTokens = usage.OutputTokens
	receipt.ReasoningTokens = usage.ReasoningTokens
	receipt.TotalTokens = usage.TotalTokens
	receipt.ComputedCostUSD = responsesUsageCost(profile, usage)
	if receipt.ComputedCostUSD > cfg.MaxUSD || receipt.ComputedCostUSD > profile.HardMaxUSD {
		receipt.Outcome = "cost_cap_exceeded"
		receipt.FailureClass = "post_call_cost_cap"
		return receipt, writeResponsesReceiptThenReturn(dir, receipt, errors.New("provider-reported usage exceeded the admitted Responses cost cap"))
	}
	receipt.AcceptedOutput = true
	receipt.Success = true
	receipt.Outcome = "pass"
	return receipt, writeResponsesReceiptThenReturn(dir, receipt, nil)
}

type validatedResponsesUsage struct {
	InputTokens, CachedInputTokens, CacheWriteTokens int64
	OutputTokens, ReasoningTokens, TotalTokens       int64
}

func responsesRequestFor(model string, profile responsesProfile) responsesRequest {
	return responsesRequest{
		Model: model, Instructions: responsesFixedInstructions, Input: responsesFixedInput,
		Store: false, Background: false, Stream: false, Tools: []any{}, ToolChoice: "none", ParallelToolCalls: false,
		MaxOutputTokens:  profile.MaxOutputTokens,
		Reasoning:        responsesReasoningRequest{Effort: profile.Effort, Context: "current_turn"},
		Text:             responsesTextRequest{Format: responsesTextFormat{Type: "json_schema", Name: responsesSchemaName, Schema: json.RawMessage(responsesSchemaJSON), Strict: true}},
		SafetyIdentifier: responsesSafetyIdentifier, ServiceTier: "default", Truncation: "disabled",
		PromptCacheOptions: responsesPromptCacheOptions{Mode: "explicit"},
	}
}

func newResponsesReceipt(cfg Config, profile responsesProfile, requestBody []byte, worstCase float64, created time.Time) ResponsesReceipt {
	return ResponsesReceipt{
		Schema: "stride.e10.openai-responses-receipt/v1", Classification: "provider_contract_attempt", Outcome: "transport_error",
		Endpoint: responsesEndpoint, Method: http.MethodPost, Model: cfg.Model, ReasoningEffort: profile.Effort,
		CandidateManifestSHA256: strings.ToLower(cfg.CandidateDigest), AcknowledgementSHA256: digest(cfg.Acknowledgement),
		PromptSHA256: digest(responsesFixedInstructions + "\x00" + responsesFixedInput), OutputSchemaSHA256: digest(responsesSchemaJSON),
		SafetyIdentifierSHA256: digest(responsesSafetyIdentifier), RequestShapeSHA256: digestBytes(requestBody),
		PriceRowSHA256: digest(responsesPriceRow(cfg.Model, profile)), PriceSourceURL: responsesPriceSourceURL,
		PriceSourceRevision: responsesPriceRevision, ModelSourceRevision: responsesModelSourceRevision,
		ModelSourceURL:  "https://developers.openai.com/api/docs/models/" + cfg.Model,
		PromptSourceURL: responsesPromptSourceURL, SchemaSourceURL: responsesSchemaSourceURL, APISourceURL: responsesAPISourceURL,
		ResponseContractSHA256: digest(responsesResponseContract(profile)),
		CredentialScope:        credentialScope(cfg), RequestProjectSHA256: optionalDigest(cfg.Project),
		ExpectedProjectSHA256: strings.ToLower(strings.TrimSpace(cfg.ExpectedProjectSHA256)), AttributionState: initialAttributionState(cfg),
		MaxInputTokens: responsesMaxInputTokens, MaxOutputTokens: profile.MaxOutputTokens,
		InputUSDPerMillion: profile.InputUSDPerMillion, CachedUSDPerMillion: profile.CachedUSDPerMillion,
		CacheWriteUSDPerMillion: profile.CacheWriteUSDPerMillion, OutputUSDPerMillion: profile.OutputUSDPerMillion,
		WorstCaseEstimatedUSD: worstCase, RequestedMaxUSD: cfg.MaxUSD, HardMaxUSD: profile.HardMaxUSD,
		RequestBodyBytes: len(requestBody), CreatedAt: created.UTC().Format(time.RFC3339Nano),
	}
}

func validateResponsesProviderResponse(got responsesAPIResponse, requestedModel string, profile responsesProfile) (string, validatedResponsesUsage, error) {
	if got.ID == "" || len(got.ID) > 512 || strings.IndexByte(got.ID, 0) >= 0 {
		return "", validatedResponsesUsage{}, errors.New("provider response ID was missing or outside the bounded contract")
	}
	if got.Object != "response" || got.Status != "completed" || !responsesJSONNull(got.Error) || !responsesJSONNull(got.IncompleteDetails) {
		return "", validatedResponsesUsage{}, errors.New("provider response was not a completed response object")
	}
	if !responsesCompatibleModel(requestedModel, got.Model) {
		return "", validatedResponsesUsage{}, errors.New("provider response model was not compatible with the requested GPT-5.6 family")
	}
	if got.Store == nil || *got.Store || got.Background == nil || *got.Background {
		return "", validatedResponsesUsage{}, errors.New("provider response did not preserve stateless foreground execution")
	}
	if got.MaxOutputTokens == nil || *got.MaxOutputTokens != profile.MaxOutputTokens {
		return "", validatedResponsesUsage{}, errors.New("provider response did not preserve the output-token cap")
	}
	if got.Reasoning == nil || got.Reasoning.Effort != profile.Effort || got.Reasoning.Context != "current_turn" {
		return "", validatedResponsesUsage{}, errors.New("provider response did not preserve the frozen reasoning contract")
	}
	if got.ServiceTier != "default" || got.Tools == nil || len(got.Tools) != 0 || got.ToolChoice != "none" || got.ParallelToolCalls == nil || *got.ParallelToolCalls || got.Truncation != "disabled" {
		return "", validatedResponsesUsage{}, errors.New("provider response did not preserve the no-tools standard request shape")
	}

	var outputText string
	messageCount := 0
	for _, item := range got.Output {
		switch item.Type {
		case "reasoning":
			if item.ID == "" || len(item.ID) > 512 || len(item.Content) != 0 {
				return "", validatedResponsesUsage{}, errors.New("provider returned a malformed reasoning output item")
			}
		case "message":
			messageCount++
			if item.ID == "" || len(item.ID) > 512 || item.Status != "completed" || item.Role != "assistant" || len(item.Content) != 1 {
				return "", validatedResponsesUsage{}, errors.New("provider did not return exactly one completed assistant message")
			}
			content := item.Content[0]
			if content.Type != "output_text" || content.Refusal != "" || len(content.Annotations) != 0 || content.Text == "" || len(content.Text) > responsesMaxOutputBytes {
				return "", validatedResponsesUsage{}, errors.New("provider assistant message was not one bounded unannotated output_text")
			}
			outputText = content.Text
		default:
			return "", validatedResponsesUsage{}, errors.New("provider returned an output item outside the no-tools contract")
		}
	}
	if messageCount != 1 {
		return "", validatedResponsesUsage{}, errors.New("provider did not return exactly one assistant output_text")
	}
	if err := validateResponsesOutput(outputText); err != nil {
		return "", validatedResponsesUsage{}, err
	}
	usage, err := validateResponsesUsage(got.Usage, profile)
	if err != nil {
		return "", validatedResponsesUsage{}, err
	}
	return outputText, usage, nil
}

func validateResponsesOutput(raw string) error {
	if len(raw) == 0 || len(raw) > responsesMaxOutputBytes {
		return errors.New("provider output_text was outside the bounded contract")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return errors.New("provider output_text was not exactly one JSON object")
	}
	fields := make(map[string]json.RawMessage, 3)
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok {
			return errors.New("provider output_text contained an invalid JSON object key")
		}
		if _, duplicate := fields[key]; duplicate {
			return errors.New("provider output_text contained a duplicate JSON key")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return errors.New("provider output_text contained invalid JSON")
		}
		fields[key] = value
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return errors.New("provider output_text was not a complete JSON object")
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) || token != nil {
		return errors.New("provider output_text contained trailing JSON data")
	}
	if len(fields) != 3 {
		return errors.New("provider output_text did not match the fixed schema")
	}
	var label string
	var sum int64
	var accepted bool
	if err := json.Unmarshal(fields["label"], &label); err != nil || label != "probe-ok" {
		return errors.New("provider output_text label did not match the expected result")
	}
	if err := json.Unmarshal(fields["sum"], &sum); err != nil || sum != 42 {
		return errors.New("provider output_text sum did not match the expected result")
	}
	if err := json.Unmarshal(fields["accepted"], &accepted); err != nil || !accepted {
		return errors.New("provider output_text acceptance did not match the expected result")
	}
	return nil
}

func validateResponsesUsage(raw *responsesProviderUsage, profile responsesProfile) (validatedResponsesUsage, error) {
	if raw == nil || raw.InputTokens == nil || raw.InputTokenDetails == nil || raw.InputTokenDetails.CachedTokens == nil || raw.InputTokenDetails.CacheWriteTokens == nil || raw.OutputTokens == nil || raw.OutputTokenDetails == nil || raw.OutputTokenDetails.ReasoningTokens == nil || raw.TotalTokens == nil {
		return validatedResponsesUsage{}, errors.New("provider response omitted required token usage fields")
	}
	usage := validatedResponsesUsage{
		InputTokens: *raw.InputTokens, CachedInputTokens: *raw.InputTokenDetails.CachedTokens,
		CacheWriteTokens: *raw.InputTokenDetails.CacheWriteTokens, OutputTokens: *raw.OutputTokens,
		ReasoningTokens: *raw.OutputTokenDetails.ReasoningTokens, TotalTokens: *raw.TotalTokens,
	}
	counts := []int64{usage.InputTokens, usage.CachedInputTokens, usage.CacheWriteTokens, usage.OutputTokens, usage.ReasoningTokens, usage.TotalTokens}
	for _, count := range counts {
		if count < 0 {
			return validatedResponsesUsage{}, errors.New("provider response reported negative token usage")
		}
	}
	if usage.InputTokens == 0 || usage.OutputTokens == 0 || usage.TotalTokens == 0 {
		return validatedResponsesUsage{}, errors.New("provider response reported zero usage for non-empty input or output")
	}
	if usage.InputTokens > responsesMaxInputTokens || usage.OutputTokens > profile.MaxOutputTokens || usage.ReasoningTokens > usage.OutputTokens {
		return validatedResponsesUsage{}, errors.New("provider token usage exceeded the frozen request caps")
	}
	if usage.CachedInputTokens != 0 || usage.CacheWriteTokens != 0 {
		return validatedResponsesUsage{}, errors.New("provider reported cache usage despite explicit no-cache request policy")
	}
	if usage.TotalTokens != usage.InputTokens+usage.OutputTokens {
		return validatedResponsesUsage{}, errors.New("provider token usage totals were incoherent")
	}
	return usage, nil
}

func decodeResponsesDocument(body io.Reader, destination *responsesAPIResponse) error {
	raw, err := io.ReadAll(io.LimitReader(body, responsesMaxBodyBytes+1))
	if err != nil {
		return errors.New("provider response body could not be read")
	}
	if int64(len(raw)) > responsesMaxBodyBytes {
		return errors.New("provider response exceeded the bounded body limit")
	}
	if err := validateResponsesJSONDocument(raw); err != nil {
		return err
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		return errors.New("provider response was not exactly one JSON document")
	}
	return nil
}

func validateResponsesJSONDocument(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := validateResponsesJSONValue(decoder, 0); err != nil {
		return err
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) || token != nil {
		return errors.New("provider response was not exactly one JSON document")
	}
	return nil
}

func validateResponsesJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("provider response JSON exceeded the nesting limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return errors.New("provider response was not exactly one JSON document")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok {
				return errors.New("provider response contained an invalid JSON object key")
			}
			if _, duplicate := keys[key]; duplicate {
				return errors.New("provider response contained a duplicate JSON key")
			}
			keys[key] = struct{}{}
			if err := validateResponsesJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("provider response contained an incomplete JSON object")
		}
	case '[':
		for decoder.More() {
			if err := validateResponsesJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("provider response contained an incomplete JSON array")
		}
	default:
		return errors.New("provider response contained an invalid JSON delimiter")
	}
	return nil
}

func validateResponsesBaseURL(base string) error {
	if base == "" {
		return nil
	}
	u, err := url.Parse(base)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("Responses API base must be the exact production base or a loopback test URL")
	}
	cleanPath := strings.TrimRight(u.EscapedPath(), "/")
	if u.Scheme == "https" && u.Host == "api.openai.com" && cleanPath == "/v1" {
		return nil
	}
	host := u.Hostname()
	if (host == "localhost" || net.ParseIP(host).IsLoopback()) && (u.Scheme == "http" || u.Scheme == "https") && cleanPath == "" {
		return nil
	}
	return errors.New("Responses API base must be the exact production base or a loopback test URL")
}

func responsesIsLoopbackBase(base string) bool {
	if base == "" {
		return false
	}
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || net.ParseIP(host).IsLoopback()
}

func responsesResolvedBase(base string) string {
	if base == "" {
		return DefaultAPIBase
	}
	return base
}

func responsesCompatibleModel(requested, got string) bool {
	// The 2026-08-01 model pages list only these aliases as their current
	// snapshots. A dated or otherwise rewritten model is not accepted until an
	// official model page proves that exact snapshot and its pricing contract.
	return got == requested
}

func responsesJSONNull(raw json.RawMessage) bool {
	return len(raw) != 0 && string(bytes.TrimSpace(raw)) == "null"
}

func verifyResponsesAttribution(receipt *ResponsesReceipt) error {
	if receipt.ResponseProjectSHA256 == "" {
		return nil
	}
	if receipt.RequestProjectSHA256 == "" || receipt.ExpectedProjectSHA256 == "" {
		return nil
	}
	if receipt.ResponseProjectSHA256 != receipt.RequestProjectSHA256 || receipt.ResponseProjectSHA256 != receipt.ExpectedProjectSHA256 {
		return errors.New("provider project attribution echo did not match request-bound project")
	}
	receipt.AttributionVerified = true
	receipt.AttributionState = "provider_verified"
	return nil
}

func responsesWorstCaseCost(profile responsesProfile) float64 {
	return (float64(responsesMaxInputTokens)*profile.InputUSDPerMillion + float64(profile.MaxOutputTokens)*profile.OutputUSDPerMillion) / 1_000_000
}

func responsesUsageCost(profile responsesProfile, usage validatedResponsesUsage) float64 {
	uncached := usage.InputTokens - usage.CachedInputTokens - usage.CacheWriteTokens
	return (float64(uncached)*profile.InputUSDPerMillion +
		float64(usage.CachedInputTokens)*profile.CachedUSDPerMillion +
		float64(usage.CacheWriteTokens)*profile.CacheWriteUSDPerMillion +
		float64(usage.OutputTokens)*profile.OutputUSDPerMillion) / 1_000_000
}

func responsesPriceRow(model string, profile responsesProfile) string {
	return fmt.Sprintf("official-standard-short-context-v1\nsource=%s\nrevision=%s\nmodel=%s\ninput_usd_per_million=%.2f\ncached_input_usd_per_million=%.2f\ncache_write_usd_per_million=%.2f\noutput_usd_per_million=%.2f\nlong_context_threshold=272000",
		responsesPriceSourceURL, responsesPriceRevision, model, profile.InputUSDPerMillion, profile.CachedUSDPerMillion, profile.CacheWriteUSDPerMillion, profile.OutputUSDPerMillion)
}

func responsesResponseContract(profile responsesProfile) string {
	return fmt.Sprintf("responses-provider-contract-v1\nobject=response\nstatus=completed\nerror=null\nincomplete_details=null\nstore=false\nbackground=false\nservice_tier=default\ntools=empty\ntool_choice=none\nparallel_tool_calls=false\ntruncation=disabled\nreasoning_effort=%s\nreasoning_context=current_turn\nmax_output_tokens=%d\noutputs=reasoning-items-plus-one-completed-assistant-output_text\nusage=input,cached,cache-write,output,reasoning,total\nexpected_output_sha256=%s",
		profile.Effort, profile.MaxOutputTokens, digest(`{"label":"probe-ok","sum":42,"accepted":true}`))
}

func writeResponsesReceiptThenReturn(dir string, receipt ResponsesReceipt, runErr error) error {
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "receipt.json")
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	return runErr
}
