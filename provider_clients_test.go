package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This inventory is intentionally explicit. Adding or moving a direct AI or
// model-adjacent provider endpoint requires updating this table and routing its
// HTTP/WebSocket client through provider_clients.go. That makes a new provider
// a review-visible act instead of an accidental test-network escape.
func TestAIProviderSourceInventoryUsesSharedNetworkBoundary(t *testing.T) {
	inventory := map[string][]string{
		"kanban.go":                              {"realtimeHTTPClient = aiProviderHTTPClient(30 * time.Second)", "realtimeHTTPClient.Do(request)"},
		"room_scout_transport.go":                {"realtimeHTTPClient.Do(request)"},
		"transcription_lane.go":                  {"aiProviderRealtimeWebSocketDialer().Dial("},
		"meeting_specialist_realtime_config.go":  {"meetingSpecialistRealtimeEndpoint"},
		"meeting_specialist_realtime_adapter.go": {"aiProviderRealtimeWebSocketDialer()"},
		"transcribe_dictation.go":                {"dictationHTTPClient = aiProviderHTTPClient(90 * time.Second)"},
		"openai_responses.go":                    {"timeout := openAIResponsesRequestTimeout(request)", "aiProviderHTTPClient(timeout).Do(httpRequest)"},
		"openai_tool_responses.go":               {"aiProviderHTTPClient(openAIToolResponsesRequestTimeout)", "client.httpClient.Do(request)"},
		"openai_images.go":                       {"aiProviderHTTPClient(openAIImageProviderTimeout).Do(httpRequest)"},
		"embeddings.go":                          {"aiProviderHTTPClient(embeddingRequestTimeout).Do(httpRequest)"},
		"agent_runner_anthropic.go":              {"aiProviderHTTPClient(0).Do(httpRequest)"},
		"fiscal_client.go":                       {"client := aiProviderHTTPClient(fiscalRequestTimeout)"},
		"stride_lead_responses.go":               {"httpClient: aiProviderHTTPClient(strideLeadResponsesTimeout)", "client.httpClient.Do(request)"},
	}
	for path, required := range inventory {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read provider source %s: %v", path, err)
		}
		source := string(raw)
		for _, marker := range required {
			if !strings.Contains(source, marker) {
				t.Errorf("%s no longer carries shared AI-provider network marker %q", path, marker)
			}
		}
		for _, forbidden := range []string{
			"http.Client{",
			"http.Transport{",
			"http.DefaultClient",
			"http.Get(",
			"http.Post(",
			"http.PostForm(",
			"websocket.Dialer{",
			"websocket.DefaultDialer",
			"net.Dial(",
			"tls.Dial(",
		} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s constructs/bypasses the AI-provider network boundary via %q", path, forbidden)
			}
		}
	}

	// Every current hard-coded AI/model-data provider hostname must be owned by
	// the inventory above. Link-preview, GIPHY, Resend, SMTP, push, callbacks,
	// and browser-facing downloads are deliberately outside this AI boundary.
	providerHosts := []string{"api.openai.com", "api.anthropic.com", "api.fiscal.ai"}
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") || path == "provider_clients.go" {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, host := range providerHosts {
			if strings.Contains(string(raw), host) {
				if _, ok := inventory[path]; !ok {
					t.Errorf("AI-provider host %q appears in unreviewed source %s; inventory it and use provider_clients.go", host, path)
				}
			}
		}
	}
}
