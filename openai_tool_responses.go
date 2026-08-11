package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const openAIToolResponsesMaxBodyBytes = 4 << 20
const openAIToolResponsesEndpoint = "https://api.openai.com/v1/responses"
const openAIToolResponsesRequestTimeout = 2 * time.Minute

type openAIToolHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// openAIToolResponsesClient is the explicit transport adapter for the isolated
// carrier. It is never constructed from package init or selected by legacy
// runner aliases; installation must provide an exact project, credential and
// reviewed endpoint after the separate spend gate.
type openAIToolResponsesClient struct {
	httpClient openAIToolHTTPDoer
	Endpoint   string
	APIKey     string
	ProjectID  string
}

func newOpenAIToolResponsesClient(apiKey, projectID string) *openAIToolResponsesClient {
	return &openAIToolResponsesClient{
		httpClient: aiProviderHTTPClient(openAIToolResponsesRequestTimeout),
		Endpoint:   openAIToolResponsesEndpoint, APIKey: strings.TrimSpace(apiKey), ProjectID: strings.TrimSpace(projectID),
	}
}

func (client *openAIToolResponsesClient) RespondWithOpenAITools(ctx context.Context, payload openAIResponsesToolRequest) (openAIResponsesToolResponse, error) {
	if client == nil || client.httpClient == nil || strings.TrimSpace(client.Endpoint) != openAIToolResponsesEndpoint || strings.TrimSpace(client.APIKey) == "" || strings.TrimSpace(client.ProjectID) == "" {
		return openAIResponsesToolResponse{}, errOpenAIToolCarrierUnavailable
	}
	authoritativeManifest, manifestErr := buildOpenAIToolManifest()
	providedTools, providedToolsErr := canonicalJSON(payload.Tools)
	authoritativeTools, authoritativeToolsErr := canonicalJSON(authoritativeManifest.responsesTools())
	providedReasoning, providedReasoningErr := canonicalJSON(payload.Reasoning)
	authoritativeReasoning, authoritativeReasoningErr := canonicalJSON(map[string]string{"effort": openAIToolRunnerReasoningEffort})
	if manifestErr != nil || providedToolsErr != nil || authoritativeToolsErr != nil || providedReasoningErr != nil || authoritativeReasoningErr != nil || payload.ManifestDigest != authoritativeManifest.DigestSHA256 || !bytes.Equal(providedTools, authoritativeTools) || !bytes.Equal(providedReasoning, authoritativeReasoning) || payload.Model != openAIToolRunnerModel || strings.TrimSpace(payload.Instructions) == "" || len(payload.Input) == 0 || payload.Store || payload.ParallelToolCalls || payload.ToolChoice != "auto" {
		return openAIResponsesToolResponse{}, errors.New("OpenAI tool Responses payload violates the frozen server route")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return openAIResponsesToolResponse{}, err
	}
	if bytes.Contains(raw, []byte("previous_response_id")) || !bytes.Contains(raw, []byte(`"store":false`)) || !bytes.Contains(raw, []byte(`"parallel_tool_calls":false`)) {
		return openAIResponsesToolResponse{}, errors.New("OpenAI tool Responses payload attempted provider-managed state")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(client.Endpoint), bytes.NewReader(raw))
	if err != nil {
		return openAIResponsesToolResponse{}, err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(client.APIKey))
	request.Header.Set("OpenAI-Project", strings.TrimSpace(client.ProjectID))
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return openAIResponsesToolResponse{}, err
	}
	if response == nil || response.Body == nil {
		return openAIResponsesToolResponse{}, errors.New("OpenAI tool Responses returned no HTTP body")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, openAIToolResponsesMaxBodyBytes+1))
	if err != nil {
		return openAIResponsesToolResponse{}, err
	}
	if len(body) > openAIToolResponsesMaxBodyBytes {
		return openAIResponsesToolResponse{}, errors.New("OpenAI tool Responses body exceeded limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return openAIResponsesToolResponse{}, fmt.Errorf("OpenAI tool Responses HTTP %d", response.StatusCode)
	}
	return parseOpenAIToolResponsesBody(body)
}

func parseOpenAIToolResponsesBody(body []byte) (openAIResponsesToolResponse, error) {
	uniqueDecoder := json.NewDecoder(bytes.NewReader(body))
	uniqueDecoder.UseNumber()
	if _, err := decodeUniqueJSONValue(uniqueDecoder); err != nil {
		return openAIResponsesToolResponse{}, fmt.Errorf("validate unique OpenAI Responses JSON: %w", err)
	}
	if token, err := uniqueDecoder.Token(); err != io.EOF {
		if err == nil {
			return openAIResponsesToolResponse{}, fmt.Errorf("unexpected trailing OpenAI Responses token %v", token)
		}
		return openAIResponsesToolResponse{}, err
	}
	var wire struct {
		ID                string            `json:"id"`
		Model             string            `json:"model"`
		Status            string            `json:"status"`
		IncompleteDetails json.RawMessage   `json:"incomplete_details"`
		Output            []json.RawMessage `json:"output"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&wire); err != nil {
		return openAIResponsesToolResponse{}, fmt.Errorf("decode OpenAI tool Responses body: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return openAIResponsesToolResponse{}, err
	}
	result := openAIResponsesToolResponse{
		ResponseID: strings.TrimSpace(wire.ID), Model: strings.TrimSpace(wire.Model),
		ExactOutputItems: cloneOpenAIToolRawItems(wire.Output), Incomplete: strings.EqualFold(strings.TrimSpace(wire.Status), "incomplete") || len(bytes.TrimSpace(wire.IncompleteDetails)) > 0 && string(bytes.TrimSpace(wire.IncompleteDetails)) != "null",
	}
	if result.ResponseID == "" || result.Model == "" || len(result.ExactOutputItems) == 0 || strings.TrimSpace(wire.Status) != "completed" {
		return openAIResponsesToolResponse{}, errors.New("OpenAI tool Responses body lacks id, model, or output")
	}
	var messages int
	for _, rawItem := range wire.Output {
		var header struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(rawItem, &header); err != nil {
			return openAIResponsesToolResponse{}, errors.New("OpenAI tool Responses output item is invalid")
		}
		switch strings.TrimSpace(header.Type) {
		case "reasoning":
			if header.Status != "completed" {
				return openAIResponsesToolResponse{}, errors.New("OpenAI tool Responses reasoning item is incomplete")
			}
			// Preserved byte-for-byte for manual replay; not interpreted locally.
		case "function_call":
			var call struct {
				Type      string `json:"type"`
				Name      string `json:"name"`
				CallID    string `json:"call_id"`
				Arguments string `json:"arguments"`
			}
			if err := json.Unmarshal(rawItem, &call); err != nil || header.Status != "completed" || strings.TrimSpace(call.Name) == "" || strings.TrimSpace(call.CallID) == "" || !json.Valid([]byte(call.Arguments)) {
				return openAIResponsesToolResponse{}, errors.New("OpenAI tool Responses function call is invalid")
			}
			result.FunctionCalls = append(result.FunctionCalls, openAIResponsesFunctionCall{Name: call.Name, CallID: call.CallID, Arguments: json.RawMessage(call.Arguments)})
		case "message":
			messages++
			var message struct {
				Role    string `json:"role"`
				Content []struct {
					Type    string `json:"type"`
					Text    string `json:"text"`
					Refusal string `json:"refusal"`
				} `json:"content"`
			}
			if err := json.Unmarshal(rawItem, &message); err != nil || header.Status != "completed" || message.Role != "assistant" {
				return openAIResponsesToolResponse{}, errors.New("OpenAI tool Responses message is invalid")
			}
			for _, content := range message.Content {
				switch content.Type {
				case "output_text":
					result.Text += content.Text
				case "refusal":
					result.Refusal += content.Refusal
				default:
					return openAIResponsesToolResponse{}, fmt.Errorf("OpenAI tool Responses message content %q is unavailable", content.Type)
				}
			}
		default:
			return openAIResponsesToolResponse{}, fmt.Errorf("OpenAI tool Responses output type %q is unavailable", header.Type)
		}
	}
	if len(result.FunctionCalls) > 0 && messages > 0 || messages > 1 {
		return openAIResponsesToolResponse{}, errors.New("OpenAI tool Responses mixed or multiple terminal messages with function calls")
	}
	return result, nil
}
