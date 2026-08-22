package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	defaultOpenAIResponsesBaseURL = "https://api.openai.com/v1"
	// Company brain, memory extraction, digests, and ordinary provider-backed
	// artifact work share the founder-approved Terra/high lane. These are
	// server-owned constants: deployment environment drift cannot change the
	// provider/model/reasoning contract.
	defaultMeetingBrainModel           = "gpt-5.6-terra"
	defaultMeetingBrainReasoningEffort = "high"
)

// openAIResponsesURL resolves the only admitted Responses API endpoint.
// Provider and route selection are server-owned; a deployment environment
// value must never redirect the OpenAI key or request body to another host.
func openAIResponsesURL() string {
	return defaultOpenAIResponsesBaseURL + "/responses"
}

type openAITextRequest struct {
	Model        string
	Instructions string
	Input        string
	// IdempotencyKey is reserved for server-owned durable operations. Ordinary
	// chat/model calls leave it empty; crash-recoverable artifact work derives
	// it from the immutable operation/run identity.
	IdempotencyKey string
	// Attachments are Responses-native input_image/input_file content items.
	// They are kept separate from Input so ordinary text-only callers preserve
	// the compact string input wire shape byte-for-byte.
	Attachments     []openAIInputContent
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
	// EnableWebSearch gives an explicitly research-scoped request the hosted
	// Responses web-search tool. It is false by default so ordinary chat,
	// meeting, routing, and structured-output calls retain their existing
	// authority and wire contract.
	EnableWebSearch bool
	// MaxToolCalls is a server-owned hosted-tool budget. It is set only by the
	// external_evidence_v2 configurator and frozen in the durable provider
	// request; ordinary web-search calls leave it zero and preserve their wire
	// shape.
	MaxToolCalls int
	// LongRunning is a server-owned transport hint for durable, contract-bearing
	// artifact generation. It changes only the HTTP deadline; it is never sent
	// to the provider and ordinary chat/routing calls leave it false.
	LongRunning bool
	// ValidateOutput runs before a wire response is accepted. It is deliberately
	// request-local so strict lanes can book wire success separately from a
	// parse/schema rejection while ordinary text callers remain unchanged.
	// NormalizeOutput is the matching server-owned presentation seam. It may
	// turn a strict structured response into a canonical artifact, but it must
	// preserve and revalidate any provider evidence receipt before returning.
	NormalizeOutput func(string) (string, error)
	ValidateOutput  func(string) error
}

type openAIInputContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Filename string `json:"filename,omitempty"`
	FileData string `json:"file_data,omitempty"`
}

type openAIInputMessage struct {
	Role    string               `json:"role"`
	Content []openAIInputContent `json:"content"`
}

type openAIJSONSchema struct {
	Name        string
	Description string
	Schema      map[string]any
}

type openAIResponsesPayload struct {
	Model           string                `json:"model"`
	Instructions    string                `json:"instructions,omitempty"`
	Input           any                   `json:"input"`
	Reasoning       map[string]any        `json:"reasoning,omitempty"`
	Text            map[string]any        `json:"text,omitempty"`
	MaxOutputTokens int                   `json:"max_output_tokens,omitempty"`
	Store           *bool                 `json:"store,omitempty"`
	ServiceTier     string                `json:"service_tier,omitempty"`
	Tools           []openAIResponsesTool `json:"tools,omitempty"`
	ToolChoice      string                `json:"tool_choice,omitempty"`
	Include         []string              `json:"include,omitempty"`
	MaxToolCalls    int                   `json:"max_tool_calls,omitempty"`
}

type openAIResponsesTool struct {
	Type string `json:"type"`
}

type openAIResponsesBody struct {
	ID                string `json:"id,omitempty"`
	Model             string `json:"model,omitempty"`
	Status            string `json:"status,omitempty"`
	ServiceTier       string `json:"service_tier,omitempty"`
	IncompleteDetails *struct {
		Reason string `json:"reason,omitempty"`
	} `json:"incomplete_details,omitempty"`
	Output []struct {
		Type   string `json:"type,omitempty"`
		Action *struct {
			Sources []struct {
				Type  string `json:"type,omitempty"`
				Title string `json:"title,omitempty"`
				URL   string `json:"url,omitempty"`
			} `json:"sources,omitempty"`
		} `json:"action,omitempty"`
		Content []struct {
			Type        string                     `json:"type,omitempty"`
			Text        string                     `json:"text,omitempty"`
			Annotations []openAIResponseAnnotation `json:"annotations,omitempty"`
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

type openAIResponseAnnotation struct {
	Type  string `json:"type,omitempty"`
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
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

// openAIResponseReceiptCapture is an optional, request-scoped side channel for
// callers that must durably bind the provider response ID and exact token
// usage beside a validated structured result. It never changes the responder
// signature or becomes a routing input.
type openAIResponseReceiptCapture struct {
	mu       sync.Mutex
	ID       string
	Model    string
	Usage    openAIResponsesUsage
	Observed bool
}

type openAIResponseReceiptCaptureContextKey struct{}

func withOpenAIResponseReceiptCapture(ctx context.Context, capture *openAIResponseReceiptCapture) context.Context {
	if capture == nil {
		return ctx
	}
	return context.WithValue(ctx, openAIResponseReceiptCaptureContextKey{}, capture)
}

func captureOpenAIResponseReceipt(ctx context.Context, id string, model string, usage *openAIResponsesUsage) {
	if ctx == nil || usage == nil || strings.TrimSpace(id) == "" {
		return
	}
	capture, _ := ctx.Value(openAIResponseReceiptCaptureContextKey{}).(*openAIResponseReceiptCapture)
	if capture == nil {
		return
	}
	capture.mu.Lock()
	capture.ID = strings.TrimSpace(id)
	capture.Model = strings.TrimSpace(model)
	capture.Usage = *usage
	capture.Observed = true
	capture.mu.Unlock()
}

func (capture *openAIResponseReceiptCapture) snapshot() (string, string, openAIResponsesUsage, bool) {
	if capture == nil {
		return "", "", openAIResponsesUsage{}, false
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.ID, capture.Model, capture.Usage, capture.Observed
}

// openAIOutputRejection is a successful HTTP exchange whose model output is
// unusable. Callers use this distinction to avoid treating truncation/invalid
// structured output like a transport outage.
type openAIOutputRejection struct {
	reason string
	cause  error
}

func (failure *openAIOutputRejection) Error() string {
	return "OpenAI output rejected: " + failure.reason
}

func (failure *openAIOutputRejection) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.cause
}

func (failure *openAIOutputRejection) providerOutputRejection() {}

// providerOutputRejectionMarker is the provider-neutral counterpart to
// providerInvocationFailureMarker: a successful wire exchange whose output is
// incomplete, empty, refused, or structurally unusable. Durable workers must
// hold their source cursor and open a bounded circuit for these errors; they
// are never evidence that the raw input itself is poison.
type providerOutputRejectionMarker interface {
	error
	providerOutputRejection()
}

func isProviderOutputRejection(err error) bool {
	var failure providerOutputRejectionMarker
	return errors.As(err, &failure)
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

func (failure *openAIProviderFailure) Error() string              { return failure.err.Error() }
func (failure *openAIProviderFailure) Unwrap() error              { return failure.err }
func (failure *openAIProviderFailure) providerInvocationFailure() {}

// providerInvocationFailureMarker is provider-neutral so ambient workers do
// not accidentally dead-letter durable input merely because their current
// route changed from OpenAI to Anthropic.
type providerInvocationFailureMarker interface {
	error
	providerInvocationFailure()
}

func isProviderInvocationFailure(err error) bool {
	var failure providerInvocationFailureMarker
	if errors.As(err, &failure) {
		return true
	}
	// Anthropic non-2xx responses already use the shared status-only wrapper.
	// Classify that existing wire receipt without relying on a provider name.
	var requestFailure *apiRequestFailure
	return errors.As(err, &requestFailure)
}

var createOpenAITextResponse openAITextResponder = createOpenAITextResponseHTTP

func meetingBrainModel() string {
	return defaultMeetingBrainModel
}

func meetingBrainReasoningEffort() string {
	return defaultMeetingBrainReasoningEffort
}

func validOpenAIReasoningEffort(effort string) bool {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "none", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
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
	effort := strings.ToLower(strings.TrimSpace(request.ReasoningEffort))
	if effort != "" && !validOpenAIReasoningEffort(effort) {
		return "", fmt.Errorf("OpenAI reasoning effort %q is not admitted", effort)
	}

	store := false
	input := any(strings.TrimSpace(request.Input))
	if len(request.Attachments) > 0 {
		content := make([]openAIInputContent, 0, len(request.Attachments)+1)
		content = append(content, request.Attachments...)
		content = append(content, openAIInputContent{Type: "input_text", Text: strings.TrimSpace(request.Input)})
		input = []openAIInputMessage{{Role: "user", Content: content}}
	}
	payload := openAIResponsesPayload{
		Model:        model,
		Instructions: strings.TrimSpace(request.Instructions),
		Input:        input,
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
	if effort != "" {
		payload.Reasoning = map[string]any{"effort": effort}
	}
	if verbosity := strings.ToLower(strings.TrimSpace(request.Verbosity)); verbosity != "" {
		payload.Text["verbosity"] = verbosity
	}
	if request.MaxOutputTokens > 0 {
		payload.MaxOutputTokens = request.MaxOutputTokens
	}
	if request.EnableWebSearch {
		payload.Tools = []openAIResponsesTool{{Type: "web_search"}}
		// A research request that may silently skip its only evidence tool cannot
		// satisfy the product's source-bound contract. Responses owns hosted-tool
		// execution, so requiring one call still produces one final model answer.
		payload.ToolChoice = "required"
		// Structured JSON does not reliably carry URL annotations on its literal
		// URL fields. Ask Responses for the provider-owned search source list and
		// bind those URLs server-side instead of trusting model-authored receipts.
		payload.Include = []string{"web_search_call.action.sources"}
	}
	if request.EnableWebSearch && request.JSONSchema != nil && request.JSONSchema.Name == packagingStudioExternalEvidenceContract {
		// This is intentionally not caller-tunable: the bounded evidence lane
		// owns its hosted-search budget at the final wire seam as well as in the
		// durable request snapshot.
		payload.MaxToolCalls = externalEvidenceMaxToolCalls
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
	if idempotencyKey := strings.TrimSpace(request.IdempotencyKey); idempotencyKey != "" {
		httpRequest.Header.Set("Idempotency-Key", idempotencyKey)
	}

	// W0 item 4: exactly ONE ledger entry per wire attempt — success or
	// failure — recorded here at the seam so every consumer (ambient fleet +
	// keyless-Anthropic twins) is metered without touching its own code path.
	// Test-swapped responders never reach this seam, so tests stay silent.
	started := time.Now()
	recordWire := func(usage *openAIResponsesUsage, wireSuccess bool, accepted bool, reason string, serviceTier string, callErr error) {
		captureAmbientReplayUsage(ctx, usage, accepted)
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

	// Long-form artifact writers legitimately need more wall time than chat or
	// routing responses. Hosted research may perform several searches and source
	// reads inside one Responses call, so it receives a separate bounded window;
	// compact calls retain the existing 45-second failure boundary.
	timeout := openAIResponsesRequestTimeout(request)
	response, err := aiProviderHTTPClient(timeout).Do(httpRequest)
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
	providerModel := strings.TrimSpace(body.Model)
	if providerModel == "" || providerModel != model {
		modelErr := &openAIOutputRejection{reason: "provider_model_mismatch"}
		recordWire(body.Usage, true, false, "provider_model_mismatch", body.ServiceTier, modelErr)
		return "", modelErr
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
	if request.EnableWebSearch {
		text = appendOpenAIResponseWebSources(text, extractOpenAIResponseWebEvidence(body))
	}
	if request.NormalizeOutput != nil {
		normalized, err := request.NormalizeOutput(text)
		if err != nil {
			validationErr := &openAIOutputRejection{reason: "output_validation_error: " + err.Error(), cause: err}
			recordWire(body.Usage, true, false, "output_validation_error", body.ServiceTier, validationErr)
			return "", validationErr
		}
		text = strings.TrimSpace(normalized)
	}
	if text == "" {
		emptyErr := &openAIOutputRejection{reason: "empty_output"}
		recordWire(body.Usage, true, false, "empty_output", body.ServiceTier, emptyErr)
		return "", emptyErr
	}
	if request.ValidateOutput != nil {
		if err := request.ValidateOutput(text); err != nil {
			validationErr := &openAIOutputRejection{reason: "output_validation_error: " + err.Error(), cause: err}
			recordWire(body.Usage, true, false, "output_validation_error", body.ServiceTier, validationErr)
			return "", validationErr
		}
	}

	captureOpenAIResponseReceipt(ctx, body.ID, providerModel, body.Usage)
	recordWire(body.Usage, true, true, "", body.ServiceTier, nil)
	return text, nil
}

func openAIResponsesRequestTimeout(request openAITextRequest) time.Duration {
	if request.EnableWebSearch {
		// Hosted research may fan through multiple searches and source reads.
		// Its durable thread context owns cancellation; a client-wide deadline
		// would manufacture failures based on wall time rather than work state.
		return 0
	}
	if request.LongRunning {
		return orchestratorTimeout()
	}
	if request.MaxOutputTokens > 4000 {
		return 120 * time.Second
	}
	return 45 * time.Second
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

type openAIResponseWebCitation struct {
	Title string
	URL   string
}

type openAIResponseWebEvidence struct {
	ResponseID  string
	SearchCalls int
	Citations   []openAIResponseWebCitation
}

func extractOpenAIResponseWebEvidence(body openAIResponsesBody) openAIResponseWebEvidence {
	seen := map[string]bool{}
	citations := make([]openAIResponseWebCitation, 0)
	appendCitation := func(rawURL, rawTitle string) {
		if _, ok := parseBareHTTPSURL(rawURL); !ok || seen[rawURL] {
			return
		}
		seen[rawURL] = true
		title := strings.Join(strings.Fields(rawTitle), " ")
		title = truncateAgentThreadText(title, 180)
		citations = append(citations, openAIResponseWebCitation{Title: title, URL: rawURL})
	}
	searchCalls := 0
	for _, output := range body.Output {
		if output.Type == "web_search_call" {
			searchCalls++
			if output.Action != nil {
				for _, source := range output.Action.Sources {
					appendCitation(source.URL, source.Title)
				}
			}
			continue
		}
		if output.Type != "" && output.Type != "message" {
			continue
		}
		for _, content := range output.Content {
			for _, annotation := range content.Annotations {
				if annotation.Type != "" && annotation.Type != "url_citation" {
					continue
				}
				appendCitation(annotation.URL, annotation.Title)
			}
		}
	}
	return openAIResponseWebEvidence{ResponseID: strings.TrimSpace(body.ID), SearchCalls: searchCalls, Citations: citations}
}

// appendOpenAIResponseWebSources makes the provider's URL-citation receipt
// durable in the saved artifact even when the model rendered citation markers
// rather than literal URLs in its prose. The model cannot self-mint this
// receipt: any claimed block is stripped and the server rebuilds it only from
// the provider response ID, hosted-search calls, and provider-owned source
// records/URL annotations.
func appendOpenAIResponseWebSources(text string, evidence openAIResponseWebEvidence) string {
	text = stripOpenAIWebCitationReceipt(text)
	lines := make([]string, 0, len(evidence.Citations))
	urls := make([]string, 0, len(evidence.Citations))
	domains := map[string]bool{}
	seen := map[string]bool{}
	for _, citation := range evidence.Citations {
		rawURL := citation.URL
		parsed, ok := parseBareHTTPSURL(rawURL)
		if !ok || seen[rawURL] {
			continue
		}
		seen[rawURL] = true
		title := strings.Join(strings.Fields(citation.Title), " ")
		title = truncateAgentThreadText(title, 180)
		line := "- " + rawURL
		if title != "" {
			line = "- " + title + " — " + rawURL
		}
		lines = append(lines, line)
		urls = append(urls, rawURL)
		domains[strings.ToLower(parsed.Hostname())] = true
	}
	if len(lines) == 0 || evidence.SearchCalls < 1 || evidence.ResponseID == "" {
		return text
	}
	receipt := fmt.Sprintf("<!-- stride-web-citation-receipt:v1 count=%d domains=%d searches=%d response=%s digest=%s -->", len(urls), len(domains), evidence.SearchCalls, sha256Hex([]byte(evidence.ResponseID)), sha256Hex([]byte(strings.Join(urls, "\n"))))
	return strings.TrimSpace(text + "\n\n## Scout source receipt\n" + strings.Join(lines, "\n") + "\n" + receipt)
}

var openAIWebCitationReceiptBlockPattern = regexp.MustCompile(`(?is)\n*##\s+Scout source receipt\s*\n.*?(?:<!--\s*stride-web-citation-receipt:v1[^>]*-->|\z)`)

func stripOpenAIWebCitationReceipt(text string) string {
	return strings.TrimSpace(openAIWebCitationReceiptBlockPattern.ReplaceAllString(text, ""))
}
