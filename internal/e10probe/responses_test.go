package e10probe

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunResponsesAcceptsOnlyFrozenProfilesAndWritesBodyFreeReceipt(t *testing.T) {
	for _, model := range []string{"gpt-5.6-luna", "gpt-5.6-terra", "gpt-5.6-sol"} {
		t.Run(model, func(t *testing.T) {
			profile := responsesProfiles[model]
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != responsesEndpoint {
					t.Errorf("unexpected request target: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("OpenAI-Project") != "test-project" {
					t.Error("missing explicit credential/project binding")
				}
				if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" {
					t.Error("unexpected content negotiation")
				}
				var request responsesRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode request: %v", err)
				}
				assertFrozenResponsesRequest(t, request, model, profile)
				w.Header().Set("X-Request-ID", "req_private")
				w.Header().Set("OpenAI-Project", "test-project")
				w.Header().Set("OpenAI-Organization", "org_private")
				_, _ = w.Write(responsesSuccessPayload(t, model, profile))
			}))
			defer server.Close()

			cfg := responsesTestConfig(t, model, server.URL)
			receipt, err := RunResponses(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !receipt.Success || !receipt.AcceptedOutput || receipt.Outcome != "pass" || receipt.Classification != "provider_contract_attempt" {
				t.Fatalf("unexpected receipt verdict: %+v", receipt)
			}
			if receipt.Model != model || receipt.ReasoningEffort != profile.Effort || receipt.MaxOutputTokens != profile.MaxOutputTokens || receipt.HardMaxUSD != profile.HardMaxUSD {
				t.Fatalf("receipt did not bind profile: %+v", receipt)
			}
			if receipt.CandidateManifestSHA256 != cfg.CandidateDigest || receipt.RequestShapeSHA256 == "" || receipt.PromptSHA256 == "" || receipt.OutputSchemaSHA256 == "" || receipt.SafetyIdentifierSHA256 == "" || receipt.PriceRowSHA256 == "" || receipt.ResponseContractSHA256 == "" || receipt.ModelSourceURL == "" {
				t.Fatalf("receipt omitted immutable evidence binding: %+v", receipt)
			}
			if !receipt.AttributionVerified || receipt.AttributionState != "provider_verified" || receipt.RequestIDSHA256 == "" || receipt.ResponseIDSHA256 == "" || receipt.ResponseModelSHA256 == "" {
				t.Fatalf("receipt omitted hashed provider binding: %+v", receipt)
			}
			if receipt.InputTokens != 100 || receipt.OutputTokens != 20 || receipt.ReasoningTokens != 5 || receipt.TotalTokens != 120 || receipt.CachedInputTokens != 0 || receipt.CacheWriteTokens != 0 || receipt.ComputedCostUSD <= 0 {
				t.Fatalf("receipt omitted strict usage accounting: %+v", receipt)
			}
			if receipt.WorstCaseEstimatedUSD > receipt.RequestedMaxUSD || receipt.RequestedMaxUSD > receipt.HardMaxUSD {
				t.Fatalf("receipt budget binding is incoherent: %+v", receipt)
			}

			dirInfo, err := os.Stat(cfg.ReceiptDir)
			if err != nil || dirInfo.Mode().Perm() != 0o700 {
				t.Fatalf("receipt directory is not private: info=%v err=%v", dirInfo, err)
			}
			receiptPath := filepath.Join(cfg.ReceiptDir, "receipt.json")
			fileInfo, err := os.Stat(receiptPath)
			if err != nil || fileInfo.Mode().Perm() != 0o600 {
				t.Fatalf("receipt file is not private: info=%v err=%v", fileInfo, err)
			}
			raw, err := os.ReadFile(receiptPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{
				"test-key", "test-project", "req_private", "resp_private", "msg_private", "reasoning_private",
				responsesFixedInstructions, responsesFixedInput, responsesSafetyIdentifier, `{"label":"probe-ok","sum":42,"accepted":true}`,
			} {
				if strings.Contains(string(raw), forbidden) {
					t.Fatalf("private receipt retained raw provider/request content %q: %s", forbidden, raw)
				}
			}
		})
	}
}

func TestRunResponsesRejectsInvalidAdmissionBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	t.Run("unsupported model", func(t *testing.T) {
		cfg := responsesTestConfig(t, "gpt-5.6-luna", server.URL)
		cfg.Model = "gpt-5.6"
		if _, err := RunResponses(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "not allowlisted") {
			t.Fatalf("want model rejection, got %v", err)
		}
	})
	t.Run("over hard cap", func(t *testing.T) {
		cfg := responsesTestConfig(t, "gpt-5.6-luna", server.URL)
		cfg.MaxUSD = responsesProfiles[cfg.Model].HardMaxUSD + 0.001
		if _, err := RunResponses(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "no more than") {
			t.Fatalf("want hard cap rejection, got %v", err)
		}
	})
	t.Run("below worst case", func(t *testing.T) {
		cfg := responsesTestConfig(t, "gpt-5.6-sol", server.URL)
		cfg.MaxUSD = responsesWorstCaseCost(responsesProfiles[cfg.Model]) / 2
		if _, err := RunResponses(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "worst-case") {
			t.Fatalf("want pre-admission estimate rejection, got %v", err)
		}
	})
	for name, maxUSD := range map[string]float64{"NaN budget": math.NaN(), "positive infinity budget": math.Inf(1), "negative infinity budget": math.Inf(-1)} {
		t.Run(name, func(t *testing.T) {
			cfg := responsesTestConfig(t, "gpt-5.6-luna", server.URL)
			cfg.MaxUSD = maxUSD
			if _, err := RunResponses(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "--max-usd") {
				t.Fatalf("want non-finite budget rejection, got %v", err)
			}
		})
	}
	t.Run("non-exact base", func(t *testing.T) {
		cfg := responsesTestConfig(t, "gpt-5.6-luna", server.URL)
		cfg.BaseURL = server.URL + "/v1"
		if _, err := RunResponses(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "exact production base") {
			t.Fatalf("want host/path rejection, got %v", err)
		}
	})
	t.Run("query on production base", func(t *testing.T) {
		cfg := responsesTestConfig(t, "gpt-5.6-luna", server.URL)
		cfg.BaseURL = "https://api.openai.com/v1?redirect=elsewhere"
		if _, err := RunResponses(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "exact production base") {
			t.Fatalf("want production query rejection, got %v", err)
		}
	})
	t.Run("custom production client", func(t *testing.T) {
		cfg := responsesTestConfig(t, "gpt-5.6-luna", server.URL)
		cfg.BaseURL = ""
		cfg.Client = &http.Client{}
		if _, err := RunResponses(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "loopback test override") {
			t.Fatalf("want custom production client rejection, got %v", err)
		}
	})
	t.Run("non-canonical project", func(t *testing.T) {
		cfg := responsesTestConfig(t, "gpt-5.6-luna", server.URL)
		cfg.Project = " test-project "
		cfg.ExpectedProjectSHA256 = digest(cfg.Project)
		if _, err := RunResponses(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "must be canonical") {
			t.Fatalf("want project canonicalization rejection, got %v", err)
		}
	})
	if got := calls.Load(); got != 0 {
		t.Fatalf("invalid admissions made %d provider calls", got)
	}
}

func TestRunResponsesRejectsAdversarialProviderContracts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		trail  string
	}{
		{name: "incomplete status", mutate: func(v map[string]any) { v["status"] = "incomplete" }},
		{name: "missing error null", mutate: func(v map[string]any) { delete(v, "error") }},
		{name: "wrong model family", mutate: func(v map[string]any) { v["model"] = "gpt-5.6-terra" }},
		{name: "invalid snapshot suffix", mutate: func(v map[string]any) { v["model"] = "gpt-5.6-luna-latest" }},
		{name: "stored response", mutate: func(v map[string]any) { v["store"] = true }},
		{name: "background response", mutate: func(v map[string]any) { v["background"] = true }},
		{name: "wrong output cap", mutate: func(v map[string]any) { v["max_output_tokens"] = 255 }},
		{name: "wrong reasoning effort", mutate: func(v map[string]any) { v["reasoning"].(map[string]any)["effort"] = "high" }},
		{name: "wrong reasoning context", mutate: func(v map[string]any) { v["reasoning"].(map[string]any)["context"] = "all_turns" }},
		{name: "tool echo", mutate: func(v map[string]any) { v["tools"] = []any{map[string]any{"type": "web_search"}} }},
		{name: "tool output item", mutate: func(v map[string]any) {
			v["output"] = []any{map[string]any{"id": "call_private", "type": "function_call"}}
		}},
		{name: "two assistant messages", mutate: func(v map[string]any) {
			v["output"] = append(v["output"].([]any), responsesTestMessage(`{"label":"probe-ok","sum":42,"accepted":true}`))
		}},
		{name: "refusal", mutate: func(v map[string]any) {
			responsesTestContent(v)["type"] = "refusal"
			responsesTestContent(v)["text"] = ""
			responsesTestContent(v)["refusal"] = "private refusal"
		}},
		{name: "wrong expected result", mutate: func(v map[string]any) {
			responsesTestContent(v)["text"] = `{"label":"probe-ok","sum":41,"accepted":true}`
		}},
		{name: "extra output key", mutate: func(v map[string]any) {
			responsesTestContent(v)["text"] = `{"label":"probe-ok","sum":42,"accepted":true,"extra":"private"}`
		}},
		{name: "duplicate output key", mutate: func(v map[string]any) {
			responsesTestContent(v)["text"] = `{"label":"probe-ok","label":"probe-ok","sum":42,"accepted":true}`
		}},
		{name: "trailing output JSON", mutate: func(v map[string]any) {
			responsesTestContent(v)["text"] = `{"label":"probe-ok","sum":42,"accepted":true} {}`
		}},
		{name: "missing usage", mutate: func(v map[string]any) { delete(v, "usage") }},
		{name: "negative usage", mutate: func(v map[string]any) { responsesTestUsage(v)["output_tokens"] = -1 }},
		{name: "zero input usage", mutate: func(v map[string]any) {
			responsesTestUsage(v)["input_tokens"] = 0
			responsesTestUsage(v)["total_tokens"] = 20
		}},
		{name: "zero output usage", mutate: func(v map[string]any) {
			responsesTestUsage(v)["output_tokens"] = 0
			responsesTestUsage(v)["total_tokens"] = 100
		}},
		{name: "incoherent usage", mutate: func(v map[string]any) { responsesTestUsage(v)["total_tokens"] = 119 }},
		{name: "cached usage despite no-cache", mutate: func(v map[string]any) { responsesTestInputDetails(v)["cached_tokens"] = 1 }},
		{name: "cache write despite no-cache", mutate: func(v map[string]any) { responsesTestInputDetails(v)["cache_write_tokens"] = 1 }},
		{name: "output usage over cap", mutate: func(v map[string]any) {
			responsesTestUsage(v)["output_tokens"] = 257
			responsesTestUsage(v)["total_tokens"] = 357
		}},
		{name: "trailing provider JSON", trail: `{}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := responsesProfiles["gpt-5.6-luna"]
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Request-ID", "req_private")
				w.Header().Set("OpenAI-Project", "test-project")
				payload := responsesSuccessMap("gpt-5.6-luna", profile)
				if test.mutate != nil {
					test.mutate(payload)
				}
				encoded, err := json.Marshal(payload)
				if err != nil {
					t.Errorf("marshal response: %v", err)
				}
				_, _ = w.Write(append(encoded, test.trail...))
			}))
			defer server.Close()
			cfg := responsesTestConfig(t, "gpt-5.6-luna", server.URL)
			receipt, err := RunResponses(context.Background(), cfg)
			if err == nil || receipt.Success || receipt.AcceptedOutput || receipt.Outcome != "schema_mismatch" {
				t.Fatalf("want strict contract rejection, receipt=%+v err=%v", receipt, err)
			}
			raw, readErr := os.ReadFile(filepath.Join(cfg.ReceiptDir, "receipt.json"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, forbidden := range []string{"test-key", "test-project", "req_private", "resp_private", "private refusal", "private"} {
				if strings.Contains(string(raw), forbidden) {
					t.Fatalf("failure receipt leaked raw content %q: %s", forbidden, raw)
				}
			}
		})
	}
}

func TestRunResponsesRequiresRequestIDAndRejectsOversizedBody(t *testing.T) {
	profile := responsesProfiles["gpt-5.6-luna"]
	t.Run("request ID", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("OpenAI-Project", "test-project")
			_, _ = w.Write(responsesSuccessPayload(t, "gpt-5.6-luna", profile))
		}))
		defer server.Close()
		cfg := responsesTestConfig(t, "gpt-5.6-luna", server.URL)
		receipt, err := RunResponses(context.Background(), cfg)
		if err == nil || receipt.FailureClass != "request_id" || receipt.Success {
			t.Fatalf("want request-ID rejection, receipt=%+v err=%v", receipt, err)
		}
	})
	t.Run("bounded response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Request-ID", "req_private")
			w.Header().Set("OpenAI-Project", "test-project")
			_, _ = io.WriteString(w, strings.Repeat("x", int(responsesMaxBodyBytes)+1))
		}))
		defer server.Close()
		cfg := responsesTestConfig(t, "gpt-5.6-luna", server.URL)
		receipt, err := RunResponses(context.Background(), cfg)
		if err == nil || receipt.FailureClass != "response_schema" || !strings.Contains(err.Error(), "bounded body") {
			t.Fatalf("want bounded-body rejection, receipt=%+v err=%v", receipt, err)
		}
	})
}

func TestRunResponsesRejectsDuplicateProviderJSONKeys(t *testing.T) {
	profile := responsesProfiles["gpt-5.6-luna"]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "req_private")
		w.Header().Set("OpenAI-Project", "test-project")
		payload := string(responsesSuccessPayload(t, "gpt-5.6-luna", profile))
		payload = strings.Replace(payload, `"id":"resp_private"`, `"id":"first_private","id":"resp_private"`, 1)
		_, _ = io.WriteString(w, payload)
	}))
	defer server.Close()
	cfg := responsesTestConfig(t, "gpt-5.6-luna", server.URL)
	receipt, err := RunResponses(context.Background(), cfg)
	if err == nil || receipt.Success || receipt.FailureClass != "response_schema" || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("want duplicate-key rejection, receipt=%+v err=%v", receipt, err)
	}
	raw, readErr := os.ReadFile(filepath.Join(cfg.ReceiptDir, "receipt.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(raw), "first_private") || strings.Contains(string(raw), "resp_private") {
		t.Fatalf("duplicate-key failure receipt leaked provider IDs: %s", raw)
	}
}

func TestRunResponsesRejectsProviderErrorAndRedirectWithoutLeakingBodies(t *testing.T) {
	t.Run("provider error body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, "provider-private-error-body")
		}))
		defer server.Close()
		cfg := responsesTestConfig(t, "gpt-5.6-luna", server.URL)
		receipt, err := RunResponses(context.Background(), cfg)
		if err == nil || receipt.Outcome != "provider_error" || receipt.FailureClass != "rate_limit_or_quota" {
			t.Fatalf("want safe provider error, receipt=%+v err=%v", receipt, err)
		}
		raw, readErr := os.ReadFile(filepath.Join(cfg.ReceiptDir, "receipt.json"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Contains(err.Error(), "provider-private") || strings.Contains(string(raw), "provider-private") {
			t.Fatalf("provider error body leaked: err=%v receipt=%s", err, raw)
		}
	})

	t.Run("redirect", func(t *testing.T) {
		var redirected atomic.Int32
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			redirected.Add(1)
		}))
		defer target.Close()
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
		}))
		defer origin.Close()
		cfg := responsesTestConfig(t, "gpt-5.6-luna", origin.URL)
		receipt, err := RunResponses(context.Background(), cfg)
		if err == nil || receipt.FailureClass != "transport" || redirected.Load() != 0 {
			t.Fatalf("redirect was not denied, receipt=%+v err=%v redirected=%d", receipt, err, redirected.Load())
		}
	})
}

func TestRunResponsesProjectBindingModes(t *testing.T) {
	profile := responsesProfiles["gpt-5.6-luna"]
	t.Run("project-scoped credential is rejected before paid call", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
		}))
		defer server.Close()
		cfg := responsesTestConfig(t, "gpt-5.6-luna", server.URL)
		cfg.Project = ""
		cfg.ExpectedProjectSHA256 = ""
		cfg.AllowProjectScopedKey = true
		cfg.APIKey = "sk-proj-private-key"
		if _, err := RunResponses(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "project-scoped-key fallback is not permitted") {
			t.Fatalf("want explicit paid-project binding rejection, got %v", err)
		}
		if calls.Load() != 0 {
			t.Fatalf("project-scoped fallback made %d paid calls", calls.Load())
		}
		if _, err := os.Stat(cfg.ReceiptDir); !os.IsNotExist(err) {
			t.Fatalf("pre-admission rejection unexpectedly created receipt state: %v", err)
		}
	})

	t.Run("mismatched response project", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Request-ID", "req_private")
			w.Header().Set("OpenAI-Project", "wrong-private-project")
			_, _ = w.Write(responsesSuccessPayload(t, "gpt-5.6-luna", profile))
		}))
		defer server.Close()
		cfg := responsesTestConfig(t, "gpt-5.6-luna", server.URL)
		receipt, err := RunResponses(context.Background(), cfg)
		if err == nil || receipt.Outcome != "attribution_mismatch" || receipt.AttributionVerified {
			t.Fatalf("want project mismatch rejection, receipt=%+v err=%v", receipt, err)
		}
		raw, readErr := os.ReadFile(filepath.Join(cfg.ReceiptDir, "receipt.json"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Contains(string(raw), "wrong-private-project") || strings.Contains(string(raw), "test-project") {
			t.Fatalf("receipt retained raw project data: %s", raw)
		}
	})
}

func TestResponsesModelCompatibilityIsFamilyAndDateBounded(t *testing.T) {
	if !responsesCompatibleModel("gpt-5.6-luna", "gpt-5.6-luna") {
		t.Fatal("current documented model snapshot should be compatible")
	}
	for _, invalid := range []string{"gpt-5.6-terra", "gpt-5.6-luna-latest", "gpt-5.6-luna-2026-07-31", "gpt-5.6-luna-2026-7-31", "gpt-5.6-luna-2026-07-31-extra"} {
		if responsesCompatibleModel("gpt-5.6-luna", invalid) {
			t.Fatalf("unexpected compatible model %q", invalid)
		}
	}
}

func TestFrozenResponsesProfilesStayInsideHardCeilings(t *testing.T) {
	wants := map[string]struct {
		effort      string
		output      int64
		hard        float64
		input       float64
		cached      float64
		write       float64
		outputPrice float64
		worstCase   float64
	}{
		"gpt-5.6-luna":  {"max", 256, .005, .20, .02, .25, 1.20, .000512},
		"gpt-5.6-terra": {"low", 384, .010, 2.00, .20, 2.50, 12.00, .006656},
		"gpt-5.6-sol":   {"medium", 512, .025, 5.00, .50, 6.25, 30.00, .02048},
	}
	if responsesMaxInputTokens != 1024 {
		t.Fatalf("pre-admission input-token ceiling drifted: %d", responsesMaxInputTokens)
	}
	if len(responsesProfiles) != len(wants) {
		t.Fatalf("profile allowlist drifted: %+v", responsesProfiles)
	}
	for model, want := range wants {
		got := responsesProfiles[model]
		if got.Effort != want.effort || got.MaxOutputTokens != want.output || got.HardMaxUSD != want.hard || got.InputUSDPerMillion != want.input || got.CachedUSDPerMillion != want.cached || got.CacheWriteUSDPerMillion != want.write || got.OutputUSDPerMillion != want.outputPrice {
			t.Fatalf("profile %s drifted: %+v", model, got)
		}
		if estimate := responsesWorstCaseCost(got); estimate != want.worstCase || estimate <= 0 || estimate > got.HardMaxUSD {
			t.Fatalf("profile %s worst-case %.6f exceeds hard ceiling %.6f", model, estimate, got.HardMaxUSD)
		}
	}
}

func responsesTestConfig(t *testing.T, model, baseURL string) Config {
	t.Helper()
	manifestDir := t.TempDir()
	manifestPath := filepath.Join(manifestDir, "candidate.manifest")
	manifest := []byte("synthetic E10 candidate manifest\n")
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	profile, ok := responsesProfiles[model]
	if !ok {
		profile = responsesProfiles["gpt-5.6-luna"]
	}
	return Config{
		CandidateDigest: digestBytes(manifest), CandidateManifestPath: manifestPath,
		ReceiptDir: filepath.Join(t.TempDir(), "receipt"), Acknowledgement: "synthetic-test-acknowledgement",
		APIKey: "test-key", Project: "test-project", ExpectedProjectSHA256: digest("test-project"),
		Model: model, MaxUSD: profile.HardMaxUSD, BaseURL: baseURL,
	}
}

func assertFrozenResponsesRequest(t *testing.T, request responsesRequest, model string, profile responsesProfile) {
	t.Helper()
	if request.Model != model || request.Instructions != responsesFixedInstructions || request.Input != responsesFixedInput {
		t.Fatalf("fixed request prompt drifted: %+v", request)
	}
	if request.Store || request.Background || request.Stream || request.Tools == nil || len(request.Tools) != 0 || request.ToolChoice != "none" || request.ParallelToolCalls {
		t.Fatalf("request authority shape drifted: %+v", request)
	}
	if request.MaxOutputTokens != profile.MaxOutputTokens || request.Reasoning.Effort != profile.Effort || request.Reasoning.Context != "current_turn" {
		t.Fatalf("request model profile drifted: %+v", request)
	}
	if request.Text.Format.Type != "json_schema" || request.Text.Format.Name != responsesSchemaName || !request.Text.Format.Strict || string(request.Text.Format.Schema) != responsesSchemaJSON {
		t.Fatalf("request schema drifted: %+v", request.Text.Format)
	}
	if request.SafetyIdentifier != responsesSafetyIdentifier || request.ServiceTier != "default" || request.Truncation != "disabled" || request.PromptCacheOptions.Mode != "explicit" {
		t.Fatalf("request safety/cost shape drifted: %+v", request)
	}
}

func responsesSuccessPayload(t *testing.T, model string, profile responsesProfile) []byte {
	t.Helper()
	encoded, err := json.Marshal(responsesSuccessMap(model, profile))
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func responsesSuccessMap(model string, profile responsesProfile) map[string]any {
	return map[string]any{
		"id": "resp_private", "object": "response", "status": "completed", "error": nil, "incomplete_details": nil,
		"model": model, "store": false, "background": false, "max_output_tokens": profile.MaxOutputTokens,
		"reasoning":    map[string]any{"effort": profile.Effort, "context": "current_turn"},
		"service_tier": "default", "tools": []any{}, "tool_choice": "none", "parallel_tool_calls": false, "truncation": "disabled",
		"output": []any{
			map[string]any{"id": "reasoning_private", "type": "reasoning"},
			responsesTestMessage(`{"label":"probe-ok","sum":42,"accepted":true}`),
		},
		"usage": map[string]any{
			"input_tokens": 100, "input_tokens_details": map[string]any{"cached_tokens": 0, "cache_write_tokens": 0},
			"output_tokens": 20, "output_tokens_details": map[string]any{"reasoning_tokens": 5}, "total_tokens": 120,
		},
	}
}

func responsesTestMessage(text string) map[string]any {
	return map[string]any{
		"id": "msg_private", "type": "message", "status": "completed", "role": "assistant",
		"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
	}
}

func responsesTestContent(payload map[string]any) map[string]any {
	message := payload["output"].([]any)[1].(map[string]any)
	return message["content"].([]any)[0].(map[string]any)
}

func responsesTestUsage(payload map[string]any) map[string]any {
	return payload["usage"].(map[string]any)
}

func responsesTestInputDetails(payload map[string]any) map[string]any {
	return responsesTestUsage(payload)["input_tokens_details"].(map[string]any)
}
