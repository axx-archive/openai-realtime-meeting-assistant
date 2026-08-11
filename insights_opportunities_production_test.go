package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIInsightsProviderPinsStrictRouteAndIgnoresAnthropicKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "installed-but-never-used")
	swapAnthropicTextResponder(t, func(context.Context, string, anthropicTextRequest) (string, error) {
		t.Fatal("Anthropic text route received Insights traffic")
		return "", nil
	})
	provider := &openAIInsightsOpportunitiesProvider{
		APIKey: func() string { return "openai-key" },
		Responder: func(_ context.Context, key string, request openAITextRequest) (string, error) {
			seat := insightsOpportunitiesStaticRoute().Orchestration
			if key != "openai-key" || request.Model != seat.Model || request.ReasoningEffort != seat.Effort || request.JSONSchema == nil || request.JSONSchema.Name != "insights_opportunities_orchestration" || request.Workflow != "insights_opportunities_orchestration" {
				t.Fatalf("request=%+v key=%q, want pinned OpenAI orchestration route", request, key)
			}
			return `{"focus":["grounded work"],"constraints":["cite supplied evidence"]}`, nil
		},
	}
	var plan InsightsOpportunitiesOrchestrationPlan
	execution, err := provider.callJSON(context.Background(), insightsOpportunitiesStaticRoute().Orchestration, seatOrchestrator, "system", map[string]any{"input": "x"}, &plan)
	if err != nil {
		t.Fatalf("callJSON: %v", err)
	}
	if execution.Provider != providerOpenAI || execution.Model != "gpt-5.6-sol" || execution.Effort != "high" || execution.Request == "" || len(plan.Focus) != 1 {
		t.Fatalf("execution=%+v plan=%+v", execution, plan)
	}
}

func TestOpenAIInsightsProviderRequiresWireReceiptInProduction(t *testing.T) {
	openAIResponsesLedgerDir(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload["model"] != "gpt-5.6-sol" {
			t.Fatalf("model=%v, want pinned Sol", payload["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_insights_1","model":"gpt-5.6-sol","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"focus\":[\"grounded work\"],\"constraints\":[\"cite supplied evidence\"]}"}]}],"usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":20},"output_tokens":30}}`))
	}))
	defer server.Close()
	routeOpenAIResponsesToTestServer(t, server.URL)
	provider := &openAIInsightsOpportunitiesProvider{APIKey: func() string { return "openai-key" }, Responder: createOpenAITextResponseHTTP, RequireReceipt: true}
	var plan InsightsOpportunitiesOrchestrationPlan
	execution, err := provider.callJSON(context.Background(), insightsOpportunitiesStaticRoute().Orchestration, seatOrchestrator, "system", map[string]any{"input": "x"}, &plan)
	if err != nil {
		t.Fatalf("callJSON: %v", err)
	}
	if execution.Request != "resp_insights_1" || execution.Provider != providerOpenAI || execution.Usage.InputTokens != 80 || execution.Usage.CachedInputTokens != 20 || execution.Usage.OutputTokens != 30 {
		t.Fatalf("execution=%+v, want exact Responses receipt", execution)
	}
}

func TestOpenAIInsightsProviderRejectsMissingProductionReceipt(t *testing.T) {
	provider := &openAIInsightsOpportunitiesProvider{
		APIKey:         func() string { return "openai-key" },
		RequireReceipt: true,
		Responder: func(context.Context, string, openAITextRequest) (string, error) {
			return `{"focus":["x"],"constraints":[]}`, nil
		},
	}
	var plan InsightsOpportunitiesOrchestrationPlan
	if _, err := provider.callJSON(context.Background(), insightsOpportunitiesStaticRoute().Orchestration, seatOrchestrator, "system", map[string]any{"input": "x"}, &plan); err == nil {
		t.Fatal("missing provider receipt was accepted")
	}
}
