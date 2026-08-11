package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type openAIToolHTTPDoerFunc func(*http.Request) (*http.Response, error)

func (function openAIToolHTTPDoerFunc) Do(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestOpenAIToolResponsesTransportStoreFalseExactReplay(t *testing.T) {
	manifest, err := buildOpenAIToolManifest()
	if err != nil {
		t.Fatal(err)
	}
	callItem := `{"type":"function_call","status":"completed","id":"fc_1","call_id":"call-1","name":"answer_memory_question","arguments":"{\"query\":\"What changed?\"}"}`
	body := `{"id":"resp-1","model":"gpt-5.6-terra","status":"completed","incomplete_details":null,"output":[{"type":"reasoning","status":"completed","id":"rs_1","summary":[]},` + callItem + `]}`
	client := &openAIToolResponsesClient{Endpoint: openAIToolResponsesEndpoint, APIKey: "secret", ProjectID: "project-1"}
	client.httpClient = openAIToolHTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
		requestBody, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if request.Header.Get("OpenAI-Project") != "project-1" || request.Header.Get("Authorization") != "Bearer secret" || strings.Contains(string(requestBody), "previous_response_id") || !strings.Contains(string(requestBody), `"store":false`) || !strings.Contains(string(requestBody), `"parallel_tool_calls":false`) {
			t.Fatalf("unsafe Responses request headers/body: %s", requestBody)
		}
		var decoded map[string]any
		if err := json.Unmarshal(requestBody, &decoded); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	result, err := client.RespondWithOpenAITools(context.Background(), openAIResponsesToolRequest{
		Model: openAIToolRunnerModel, Reasoning: map[string]string{"effort": openAIToolRunnerReasoningEffort}, Instructions: "server",
		Input: []openAIResponsesToolInputItem{{Role: "user", Content: "What changed?"}}, Tools: manifest.responsesTools(), ManifestDigest: manifest.DigestSHA256, ToolChoice: "auto", Store: false, ParallelToolCalls: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ResponseID != "resp-1" || result.Model != openAIToolRunnerModel || len(result.FunctionCalls) != 1 || len(result.ExactOutputItems) != 2 || string(result.ExactOutputItems[1]) != callItem || string(result.FunctionCalls[0].Arguments) != `{"query":"What changed?"}` {
		t.Fatalf("exact Responses parse mismatch: %+v", result)
	}
}

func TestOpenAIToolResponsesTransportSecondRequestUsesStringOutputAndManualHistory(t *testing.T) {
	manifest, err := buildOpenAIToolManifest()
	if err != nil {
		t.Fatal(err)
	}
	callItem := `{"type":"function_call","status":"completed","id":"fc_1","call_id":"call-1","name":"answer_memory_question","arguments":"{\"query\":\"What changed?\"}"}`
	responses := []string{
		`{"id":"resp-1","model":"gpt-5.6-terra","status":"completed","incomplete_details":null,"output":[{"type":"reasoning","status":"completed","id":"rs_1","summary":[]},` + callItem + `]}`,
		`{"id":"resp-2","model":"gpt-5.6-terra","status":"completed","incomplete_details":null,"output":[{"type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Completed"}]}]}`,
	}
	var requestBodies [][]byte
	client := &openAIToolResponsesClient{Endpoint: openAIToolResponsesEndpoint, APIKey: "secret", ProjectID: "project-1"}
	client.httpClient = openAIToolHTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
		raw, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		requestBodies = append(requestBodies, append([]byte(nil), raw...))
		index := len(requestBodies) - 1
		if index >= len(responses) {
			t.Fatalf("unexpected Responses request %d", index)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(responses[index])), Header: make(http.Header)}, nil
	})
	journal, err := openOpenAIToolJournal(context.Background(), openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	authority := &openAIToolTestAuthority{preimage: "preimage"}
	executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
	finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
	carrier := &openAIToolLoopCarrier{Enabled: true, Manifest: manifest, Journal: journal, Authority: authority, Provider: client, Executor: openAIToolEffectAdapter{Backend: executor}, Finalizer: finalizer}
	result, err := carrier.Run(context.Background(), openAIToolLoopRequest{Instructions: "server", UserTurn: "What changed?", Expectation: openAIToolTestExpectation()})
	if err != nil || result.Text != "Completed" || len(requestBodies) != 2 {
		t.Fatalf("transport loop result=%+v requests=%d err=%v", result, len(requestBodies), err)
	}
	var second struct {
		Input []json.RawMessage `json:"input"`
		Store bool              `json:"store"`
	}
	if err := json.Unmarshal(requestBodies[1], &second); err != nil {
		t.Fatal(err)
	}
	if second.Store || len(second.Input) != 4 || !strings.Contains(string(second.Input[1]), `"type":"reasoning"`) || !strings.Contains(string(second.Input[2]), `"call_id":"call-1"`) {
		t.Fatalf("second request did not replay exact output history: %s", requestBodies[1])
	}
	var functionOutput struct {
		Type   string `json:"type"`
		CallID string `json:"call_id"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal(second.Input[3], &functionOutput); err != nil || functionOutput.Type != "function_call_output" || functionOutput.CallID != "call-1" || !json.Valid([]byte(functionOutput.Output)) {
		t.Fatalf("function_call_output was not a JSON string linked to the exact call: item=%s err=%v", second.Input[3], err)
	}
}

func TestOpenAIToolResponsesParserFailsClosed(t *testing.T) {
	for name, body := range map[string]string{
		"duplicate_key":      `{"id":"one","id":"two","model":"gpt-5.6-terra","status":"completed","output":[{"type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`,
		"multiple_calls":     `{"id":"resp","model":"gpt-5.6-terra","status":"completed","output":[{"type":"function_call","status":"completed","call_id":"a","name":"create_artifact","arguments":"{}"},{"type":"function_call","status":"completed","call_id":"b","name":"create_artifact","arguments":"{}"}]}`,
		"mixed_message_call": `{"id":"resp","model":"gpt-5.6-terra","status":"completed","output":[{"type":"function_call","status":"completed","call_id":"a","name":"create_artifact","arguments":"{}"},{"type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`,
		"refusal":            `{"id":"resp","model":"gpt-5.6-terra","status":"completed","output":[{"type":"message","status":"completed","role":"assistant","content":[{"type":"refusal","refusal":"no"}]}]}`,
		"incomplete":         `{"id":"resp","model":"gpt-5.6-terra","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"partial"}]}]}`,
		"unknown_output":     `{"id":"resp","model":"gpt-5.6-terra","status":"completed","output":[{"type":"computer_call"}]}`,
		"trailing":           `{"id":"resp","model":"gpt-5.6-terra","status":"completed","output":[]} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			result, err := parseOpenAIToolResponsesBody([]byte(body))
			if name == "multiple_calls" || name == "refusal" {
				// These states are parsed exactly and rejected by the carrier before
				// any journal reservation or effect.
				if err != nil {
					t.Fatal(err)
				}
				if name == "multiple_calls" && len(result.FunctionCalls) != 2 || name == "refusal" && result.Refusal == "" {
					t.Fatalf("negative state was not preserved: %+v", result)
				}
				return
			}
			if err == nil {
				t.Fatalf("unsafe Responses body was accepted: %+v", result)
			}
		})
	}
}

func TestOpenAIToolResponsesTransportRejectsEndpointAndRouteSubstitution(t *testing.T) {
	manifest, err := buildOpenAIToolManifest()
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	doer := openAIToolHTTPDoerFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("must not be called")
	})
	payload := openAIResponsesToolRequest{
		Model: openAIToolRunnerModel, Reasoning: map[string]string{"effort": openAIToolRunnerReasoningEffort}, Instructions: "server",
		Input: []openAIResponsesToolInputItem{{Role: "user", Content: "turn"}}, Tools: manifest.responsesTools(), ManifestDigest: manifest.DigestSHA256,
		ToolChoice: "auto", Store: false, ParallelToolCalls: false,
	}
	client := &openAIToolResponsesClient{httpClient: doer, Endpoint: "https://attacker.invalid/v1/responses", APIKey: "secret", ProjectID: "project-1"}
	if _, err := client.RespondWithOpenAITools(context.Background(), payload); err == nil || calls != 0 {
		t.Fatalf("substituted endpoint reached transport: calls=%d err=%v", calls, err)
	}
	client.Endpoint = openAIToolResponsesEndpoint
	payload.Reasoning["mode"] = "pro"
	if _, err := client.RespondWithOpenAITools(context.Background(), payload); err == nil || calls != 0 {
		t.Fatalf("substituted reasoning route reached transport: calls=%d err=%v", calls, err)
	}
}

func TestOpenAIToolResponsesProductionConstructorUsesSharedProviderBoundary(t *testing.T) {
	client := newOpenAIToolResponsesClient("secret", "project-1")
	httpClient, ok := client.httpClient.(*http.Client)
	if !ok || httpClient.Transport != sharedAIProviderHTTPTransport || httpClient.Timeout != openAIToolResponsesRequestTimeout || client.Endpoint != openAIToolResponsesEndpoint {
		t.Fatalf("production Responses client escaped shared provider boundary: client=%+v http=%+v", client, httpClient)
	}
}
