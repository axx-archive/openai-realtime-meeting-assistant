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
	openAIResponsesURL       = "https://api.openai.com/v1/responses"
	defaultMeetingBrainModel = "gpt-5.5"
)

type openAITextRequest struct {
	Model           string
	Instructions    string
	Input           string
	ReasoningEffort string
	Verbosity       string
	MaxOutputTokens int
}

type openAIResponsesPayload struct {
	Model           string         `json:"model"`
	Instructions    string         `json:"instructions,omitempty"`
	Input           string         `json:"input"`
	Reasoning       map[string]any `json:"reasoning,omitempty"`
	Text            map[string]any `json:"text,omitempty"`
	MaxOutputTokens int            `json:"max_output_tokens,omitempty"`
	Store           *bool          `json:"store,omitempty"`
}

type openAIResponsesBody struct {
	Output []struct {
		Type    string `json:"type,omitempty"`
		Content []struct {
			Type string `json:"type,omitempty"`
			Text string `json:"text,omitempty"`
		} `json:"content,omitempty"`
	} `json:"output,omitempty"`
	Error *struct {
		Message string `json:"message,omitempty"`
	} `json:"error,omitempty"`
}

type openAITextResponder func(context.Context, string, openAITextRequest) (string, error)

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
		Text: map[string]any{
			"format": map[string]any{"type": "text"},
		},
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

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIResponsesURL, bytes.NewReader(rawPayload))
	if err != nil {
		return "", fmt.Errorf("create OpenAI response request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")

	response, err := (&http.Client{Timeout: 45 * time.Second}).Do(httpRequest)
	if err != nil {
		return "", fmt.Errorf("create OpenAI response: %w", err)
	}
	defer response.Body.Close()

	rawBody, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024))
	if err != nil {
		return "", fmt.Errorf("read OpenAI response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", apiRequestFailedError("OpenAI response failed", response.Status, rawBody)
	}

	var body openAIResponsesBody
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return "", fmt.Errorf("decode OpenAI response: %w", err)
	}
	if body.Error != nil && strings.TrimSpace(body.Error.Message) != "" {
		return "", fmt.Errorf("OpenAI response error: %s", strings.TrimSpace(body.Error.Message))
	}

	text := extractOpenAIResponseText(body)
	if text == "" {
		return "", fmt.Errorf("OpenAI response did not include output text")
	}

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
