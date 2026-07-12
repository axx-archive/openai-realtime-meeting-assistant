package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultOpenAIResponsesBaseURL = "https://api.openai.com/v1"
	defaultMeetingBrainModel      = "gpt-5.5"
)

// openAIResponsesURL resolves the Responses API endpoint. W1 item 14 of
// docs/model-routing-master-plan-2026-07-11.md: OPENAI_RESPONSES_BASE_URL
// overrides the wire base (gateway/Venice shelf-readiness) with the default
// byte-identical to the old hardcoded const. The override is a BASE url —
// "https://gateway.example/v1" — and "/responses" is appended here, matching
// the OpenAI SDK base-url convention.
func openAIResponsesURL() string {
	base := strings.TrimSpace(os.Getenv("OPENAI_RESPONSES_BASE_URL"))
	if base == "" {
		base = defaultOpenAIResponsesBaseURL
	}
	return strings.TrimRight(base, "/") + "/responses"
}

type openAITextRequest struct {
	Model           string
	Instructions    string
	Input           string
	ReasoningEffort string
	Verbosity       string
	MaxOutputTokens int
	// Seat tags the caller for the usage ledger (a seat* constant from
	// usage_ledger.go). Threaded through the request struct so the responder
	// signature — swapped as a test seam across the whole suite — is untouched.
	// An empty Seat records as seatUntagged: visible gaps beat invisible ones.
	Seat string
	// Workflow and ServiceTier make routing provenance explicit in the usage
	// book. JSONSchema enables Responses strict structured output without
	// changing the responder signature used by existing text callers.
	Workflow    string
	ServiceTier string
	JSONSchema  *openAIJSONSchema
	// ValidateOutput runs before a wire response is accepted. It is deliberately
	// request-local so strict lanes can book wire success separately from a
	// parse/schema rejection while ordinary text callers remain unchanged.
	ValidateOutput func(string) error
}

type openAIJSONSchema struct {
	Name        string
	Description string
	Schema      map[string]any
}

type openAIResponsesPayload struct {
	Model           string         `json:"model"`
	Instructions    string         `json:"instructions,omitempty"`
	Input           string         `json:"input"`
	Reasoning       map[string]any `json:"reasoning,omitempty"`
	Text            map[string]any `json:"text,omitempty"`
	MaxOutputTokens int            `json:"max_output_tokens,omitempty"`
	Store           *bool          `json:"store,omitempty"`
	ServiceTier     string         `json:"service_tier,omitempty"`
}

type openAIResponsesBody struct {
	Status            string `json:"status,omitempty"`
	ServiceTier       string `json:"service_tier,omitempty"`
	IncompleteDetails *struct {
		Reason string `json:"reason,omitempty"`
	} `json:"incomplete_details,omitempty"`
	Output []struct {
		Type    string `json:"type,omitempty"`
		Content []struct {
			Type string `json:"type,omitempty"`
			Text string `json:"text,omitempty"`
		} `json:"content,omitempty"`
	} `json:"output,omitempty"`
	// Usage is the Responses API usage object (W0 item 4: the ambient fleet's
	// books). input_tokens is INCLUSIVE of the cached reads reported under
	// input_tokens_details.cached_tokens — the ledger split happens at the
	// recording seam, never here.
	Usage *openAIResponsesUsage `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message,omitempty"`
	} `json:"error,omitempty"`
}

type openAIResponsesUsage struct {
	InputTokens        int64 `json:"input_tokens,omitempty"`
	InputTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens,omitempty"`
	} `json:"input_tokens_details,omitempty"`
	OutputTokens        int64 `json:"output_tokens,omitempty"`
	OutputTokensDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens,omitempty"`
	} `json:"output_tokens_details,omitempty"`
	TotalTokens int64 `json:"total_tokens,omitempty"`
}

type openAITextResponder func(context.Context, string, openAITextRequest) (string, error)

// openAIOutputRejection is a successful HTTP exchange whose model output is
// unusable. Callers use this distinction to avoid treating truncation/invalid
// structured output like a transport outage.
type openAIOutputRejection struct {
	reason string
}

func (failure *openAIOutputRejection) Error() string {
	return "OpenAI output rejected: " + failure.reason
}

func openAIOutputRejectionReason(err error) (string, bool) {
	var failure *openAIOutputRejection
	if !errors.As(err, &failure) {
		return "", false
	}
	return failure.reason, true
}

// openAIProviderFailure marks failures outside the model output itself: HTTP
// errors, transport timeouts, unreadable bodies, or malformed provider
// envelopes. Digest callers must hold their cursor on these failures; quota or
// network outages are not poison input and must never consume a dead-letter
// budget.
type openAIProviderFailure struct {
	err error
}

func (failure *openAIProviderFailure) Error() string { return failure.err.Error() }
func (failure *openAIProviderFailure) Unwrap() error { return failure.err }

func isOpenAIProviderFailure(err error) bool {
	var failure *openAIProviderFailure
	return errors.As(err, &failure)
}

var createOpenAITextResponse openAITextResponder = createOpenAITextResponseHTTP

func meetingBrainModel() string {
	if model := strings.TrimSpace(os.Getenv("OPENAI_BRAIN_MODEL")); model != "" {
		return model
	}

	return defaultMeetingBrainModel
}

func createOpenAITextResponseHTTP(ctx context.Context, apiKey string, request openAITextRequest) (string, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY is not configured")
	}
	model := strings.TrimSpace(request.Model)
	if model == "" {
		model = meetingBrainModel()
	}

	store := false
	payload := openAIResponsesPayload{
		Model:        model,
		Instructions: strings.TrimSpace(request.Instructions),
		Input:        strings.TrimSpace(request.Input),
		Store:        &store,
		ServiceTier:  strings.TrimSpace(request.ServiceTier),
		Text: map[string]any{
			"format": map[string]any{"type": "text"},
		},
	}
	if request.JSONSchema != nil {
		payload.Text["format"] = map[string]any{
			"type":        "json_schema",
			"name":        request.JSONSchema.Name,
			"description": request.JSONSchema.Description,
			"strict":      true,
			"schema":      request.JSONSchema.Schema,
		}
	}
	if effort := strings.ToLower(strings.TrimSpace(request.ReasoningEffort)); effort != "" {
		payload.Reasoning = map[string]any{"effort": effort}
	}
	if verbosity := strings.ToLower(strings.TrimSpace(request.Verbosity)); verbosity != "" {
		payload.Text["verbosity"] = verbosity
	}
	if request.MaxOutputTokens > 0 {
		payload.MaxOutputTokens = request.MaxOutputTokens
	}

	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode OpenAI response request: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIResponsesURL(), bytes.NewReader(rawPayload))
	if err != nil {
		return "", fmt.Errorf("create OpenAI response request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")

	// W0 item 4: exactly ONE ledger entry per wire attempt — success or
	// failure — recorded here at the seam so every consumer (ambient fleet +
	// keyless-Anthropic twins) is metered without touching its own code path.
	// Test-swapped responders never reach this seam, so tests stay silent.
	started := time.Now()
	recordWire := func(usage *openAIResponsesUsage, wireSuccess bool, accepted bool, reason string, serviceTier string, callErr error) {
		entry := llmUsageEntry{
			Provider:             providerOpenAI,
			Model:                model,
			Seat:                 strings.TrimSpace(request.Seat),
			DurationMS:           time.Since(started).Milliseconds(),
			Workflow:             strings.TrimSpace(request.Workflow),
			RequestedServiceTier: strings.TrimSpace(request.ServiceTier),
			ServiceTier:          strings.TrimSpace(serviceTier),
			WireSuccess:          wireSuccess,
			AcceptedOutput:       accepted,
			OutputFailureReason:  strings.TrimSpace(reason),
		}
		if usage != nil {
			cached := usage.InputTokensDetails.CachedTokens
			// The Responses API reports input_tokens inclusive of cached reads;
			// the ledger bills InputTokens and CachedInputTokens at separate
			// rates (models_pricing.go), so split them here.
			entry.InputTokens = usage.InputTokens - cached
			if entry.InputTokens < 0 {
				entry.InputTokens = 0
			}
			entry.CachedInputTokens = cached
			entry.OutputTokens = usage.OutputTokens
		}
		if callErr != nil {
			entry.Error = callErr.Error()
		}
		recordLLMUsage(entry)
	}

	response, err := (&http.Client{Timeout: 45 * time.Second}).Do(httpRequest)
	if err != nil {
		wireErr := &openAIProviderFailure{err: fmt.Errorf("create OpenAI response: %w", err)}
		recordWire(nil, false, false, "transport_error", "", wireErr)
		return "", wireErr
	}
	defer response.Body.Close()

	rawBody, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024))
	if err != nil {
		readErr := &openAIProviderFailure{err: fmt.Errorf("read OpenAI response: %w", err)}
		recordWire(nil, false, false, "body_read_error", "", readErr)
		return "", readErr
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		failure := &openAIProviderFailure{err: apiRequestFailedError("OpenAI response failed", response.Status, rawBody)}
		recordWire(nil, false, false, "http_error", "", failure)
		return "", failure
	}

	var body openAIResponsesBody
	if err := json.Unmarshal(rawBody, &body); err != nil {
		decodeErr := &openAIProviderFailure{err: fmt.Errorf("decode OpenAI response: %w", err)}
		recordWire(nil, true, false, "response_decode_error", "", decodeErr)
		return "", decodeErr
	}
	if body.Error != nil && strings.TrimSpace(body.Error.Message) != "" {
		bodyErr := &openAIProviderFailure{err: fmt.Errorf("OpenAI response error: %s", strings.TrimSpace(body.Error.Message))}
		recordWire(body.Usage, true, false, "response_error", body.ServiceTier, bodyErr)
		return "", bodyErr
	}
	if status := strings.ToLower(strings.TrimSpace(body.Status)); status == "incomplete" || status == "failed" || status == "cancelled" {
		reason := "response_" + status
		if body.IncompleteDetails != nil && strings.TrimSpace(body.IncompleteDetails.Reason) != "" {
			reason = strings.TrimSpace(body.IncompleteDetails.Reason)
		}
		if reason == "max_output_tokens" || reason == "max_tokens" {
			reason = "max_output_truncation"
		}
		incompleteErr := &openAIOutputRejection{reason: reason}
		recordWire(body.Usage, true, false, reason, body.ServiceTier, incompleteErr)
		return "", incompleteErr
	}

	text := extractOpenAIResponseText(body)
	if text == "" {
		emptyErr := &openAIOutputRejection{reason: "empty_output"}
		recordWire(body.Usage, true, false, "empty_output", body.ServiceTier, emptyErr)
		return "", emptyErr
	}
	if request.ValidateOutput != nil {
		if err := request.ValidateOutput(text); err != nil {
			validationErr := &openAIOutputRejection{reason: "output_validation_error: " + err.Error()}
			recordWire(body.Usage, true, false, "output_validation_error", body.ServiceTier, validationErr)
			return "", validationErr
		}
	}

	recordWire(body.Usage, true, true, "", body.ServiceTier, nil)
	return text, nil
}

type apiRequestFailure struct {
	status string
	body   string
}

func (failure *apiRequestFailure) Error() string {
	return fmt.Sprintf("api request failed (%s)", failure.status)
}

// apiRequestFailedError logs the full upstream error body server-side and
// returns a short status-only error safe to surface to users.
func apiRequestFailedError(context string, status string, body []byte) error {
	log.Errorf("%s: status=%s body=%s", context, status, strings.TrimSpace(string(body)))
	return &apiRequestFailure{
		status: status,
		body:   strings.TrimSpace(string(body)),
	}
}

func openAIAPIRequestUserMessage(err error) (string, int, bool) {
	var failure *apiRequestFailure
	if !errors.As(err, &failure) {
		return "", 0, false
	}

	body := strings.ToLower(failure.body)
	if strings.Contains(body, "insufficient_quota") || strings.Contains(body, "current quota") || strings.Contains(body, "billing quota") {
		return "OpenAI API quota is exhausted", http.StatusTooManyRequests, true
	}
	if strings.Contains(body, "rate_limit") || strings.Contains(body, "rate limit") || strings.Contains(body, "requests per minute") {
		return "OpenAI API rate limit reached; try again shortly", http.StatusTooManyRequests, true
	}

	return "", 0, false
}

func extractOpenAIResponseText(body openAIResponsesBody) string {
	var parts []string
	for _, output := range body.Output {
		if output.Type != "" && output.Type != "message" {
			continue
		}
		for _, content := range output.Content {
			if content.Type != "" && content.Type != "output_text" {
				continue
			}
			if text := strings.TrimSpace(content.Text); text != "" {
				parts = append(parts, text)
			}
		}
	}

	return strings.TrimSpace(strings.Join(parts, "\n"))
}
