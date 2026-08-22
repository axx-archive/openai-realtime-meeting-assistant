package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type openAIResponsesTestTransport struct {
	target *url.URL
}

func (transport openAIResponsesTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil || request.URL.String() != defaultOpenAIResponsesBaseURL+"/responses" {
		return nil, fmt.Errorf("unexpected OpenAI Responses test target")
	}
	clone := request.Clone(request.Context())
	redirected := *request.URL
	redirected.Scheme = transport.target.Scheme
	redirected.Host = transport.target.Host
	clone.URL = &redirected
	return http.DefaultTransport.RoundTrip(clone)
}

func routeOpenAIResponsesToTestServer(t *testing.T, rawURL string) {
	t.Helper()
	target, err := url.Parse(rawURL)
	if err != nil || target == nil || !testLoopbackHost(target.Hostname()) {
		t.Fatalf("invalid OpenAI Responses test server %q: %v", rawURL, err)
	}
	restore := sharedAIProviderHTTPTransport.swap(openAIResponsesTestTransport{target: target})
	t.Cleanup(restore)
}

// openAIResponsesLedgerDir points the ledger at a temp dir and freezes its
// clock on 2026-07-11 so every entry lands in a deterministic daily file.
func openAIResponsesLedgerDir(t *testing.T) string {
	t.Helper()
	dir := ledgerTestDir(t)
	fixed := time.Date(2026, time.July, 11, 10, 0, 0, 0, time.UTC)
	prevNow := usageLedgerNow
	usageLedgerNow = func() time.Time { return fixed }
	t.Cleanup(func() { usageLedgerNow = prevNow })
	return dir
}

func TestCreateOpenAITextResponseSendsIdempotencyKey(t *testing.T) {
	openAIResponsesLedgerDir(t)
	const operationKey = "public-work-deterministic-operation"
	var gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("Idempotency-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model":"gpt-5.6-terra",
			"status":"completed",
			"output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}],
			"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
		}`))
	}))
	defer server.Close()
	routeOpenAIResponsesToTestServer(t, server.URL)

	text, err := createOpenAITextResponseHTTP(context.Background(), "test-key", openAITextRequest{
		Model:          "gpt-5.6-terra",
		Input:          "do the durable work",
		IdempotencyKey: operationKey,
	})
	if err != nil {
		t.Fatalf("createOpenAITextResponseHTTP: %v", err)
	}
	if text != "done" || gotKey != operationKey {
		t.Fatalf("text=%q idempotency key=%q, want done/%q", text, gotKey, operationKey)
	}
}

func TestCreateOpenAITextResponseSendsStrictSchemaAndRejectsTruncation(t *testing.T) {
	dir := openAIResponsesLedgerDir(t)
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Write([]byte(`{"model":"gpt-5.5","status":"incomplete","service_tier":"priority","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","content":[{"type":"output_text","text":"{\"meetingId\":\"cut"}]}],"usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	defer server.Close()
	routeOpenAIResponsesToTestServer(t, server.URL)

	_, err := createOpenAITextResponseHTTP(context.Background(), "test-key", openAITextRequest{
		Model:       "gpt-5.5",
		Seat:        seatMeetingDigest,
		Workflow:    "meeting_digest",
		ServiceTier: "priority",
		Input:       "digest",
		JSONSchema:  meetingDigestJSONSchema(),
	})
	if reason, ok := openAIOutputRejectionReason(err); !ok || reason != "max_output_truncation" {
		t.Fatalf("error=%v reason=%q ok=%v, want max-output rejection", err, reason, ok)
	}
	text, _ := payload["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	if format["type"] != "json_schema" || format["strict"] != true || format["name"] != "meeting_digest" {
		t.Fatalf("strict format missing: %#v", format)
	}
	if payload["service_tier"] != "priority" {
		t.Fatalf("requested service tier=%v, want priority", payload["service_tier"])
	}

	rows := readLedgerLines(t, filepath.Join(dir, "usage-2026-07-11.jsonl"))
	if len(rows) != 1 || rows[0]["wire_success"] != true || rows[0]["output_failure_reason"] != "max_output_truncation" {
		t.Fatalf("truncation ledger row=%v", rows)
	}
	if _, accepted := rows[0]["accepted_output"]; accepted {
		t.Fatalf("truncated output was marked accepted: %v", rows[0])
	}
	if rows[0]["service_tier"] != "priority" || rows[0]["requested_service_tier"] != "priority" {
		t.Fatalf("service-tier provenance missing: %v", rows[0])
	}
}

func TestCreateOpenAITextResponseBooksWireSuccessBeforeOutputValidation(t *testing.T) {
	dir := openAIResponsesLedgerDir(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"model":"gpt-5.5","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"not json"}]}],"usage":{"input_tokens":7,"output_tokens":2}}`))
	}))
	defer server.Close()
	routeOpenAIResponsesToTestServer(t, server.URL)

	_, err := createOpenAITextResponseHTTP(context.Background(), "test-key", openAITextRequest{
		Model: "gpt-5.5", Seat: seatMeetingDigest, Input: "digest",
		ValidateOutput: func(text string) error {
			var value map[string]any
			return json.Unmarshal([]byte(text), &value)
		},
	})
	if _, ok := openAIOutputRejectionReason(err); !ok {
		t.Fatalf("error=%v, want accepted-wire output rejection", err)
	}
	rows := readLedgerLines(t, filepath.Join(dir, "usage-2026-07-11.jsonl"))
	if len(rows) != 1 || rows[0]["wire_success"] != true || rows[0]["output_failure_reason"] != "output_validation_error" {
		t.Fatalf("validation ledger row=%v", rows)
	}
}

func TestOpenAIResponsesURLIgnoresLegacyOverride(t *testing.T) {
	t.Setenv("OPENAI_RESPONSES_BASE_URL", "https://attacker.example/v1")
	if got := openAIResponsesURL(); got != "https://api.openai.com/v1/responses" {
		t.Fatalf("responses URL=%q, want the fixed api.openai.com endpoint", got)
	}
}

func TestCreateOpenAITextResponseRejectsMissingOrMismatchedProviderModel(t *testing.T) {
	dir := openAIResponsesLedgerDir(t)
	responses := []string{
		`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"missing"}]}],"usage":{"input_tokens":3,"output_tokens":1}}`,
		`{"model":"gpt-5.6-sol","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"wrong"}]}],"usage":{"input_tokens":3,"output_tokens":1}}`,
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(responses[calls]))
		calls++
	}))
	defer server.Close()
	routeOpenAIResponsesToTestServer(t, server.URL)

	for range responses {
		capture := &openAIResponseReceiptCapture{}
		_, err := createOpenAITextResponseHTTP(withOpenAIResponseReceiptCapture(context.Background(), capture), "test-key", openAITextRequest{Model: "gpt-5.6-terra", Input: "hello"})
		if reason, ok := openAIOutputRejectionReason(err); !ok || reason != "provider_model_mismatch" {
			t.Fatalf("error=%v reason=%q ok=%v, want provider-model mismatch", err, reason, ok)
		}
		if _, _, _, observed := capture.snapshot(); observed {
			t.Fatal("mismatched provider model produced an accepted receipt")
		}
	}
	if calls != 2 {
		t.Fatalf("provider calls=%d, want 2", calls)
	}
	rows := readLedgerLines(t, filepath.Join(dir, "usage-2026-07-11.jsonl"))
	if len(rows) != 2 {
		t.Fatalf("usage rows=%d, want 2", len(rows))
	}
	for _, row := range rows {
		if row["wire_success"] != true || row["output_failure_reason"] != "provider_model_mismatch" {
			t.Fatalf("model-mismatch ledger row=%v", row)
		}
		if _, accepted := row["accepted_output"]; accepted {
			t.Fatalf("model mismatch was marked accepted: %v", row)
		}
	}
}

func TestCreateOpenAITextResponseRejectsLegacyGPT56EffortsBeforeNetwork(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	routeOpenAIResponsesToTestServer(t, server.URL)
	for _, effort := range []string{"minimal", "ultra", "turbo"} {
		_, err := createOpenAITextResponseHTTP(context.Background(), "test-key", openAITextRequest{Model: "gpt-5.6-terra", Input: "hello", ReasoningEffort: effort})
		if err == nil || !strings.Contains(err.Error(), "not admitted") {
			t.Fatalf("effort=%q err=%v, want fail-closed admission error", effort, err)
		}
	}
	for _, effort := range []string{"none", "low", "medium", "high", "xhigh", "max"} {
		if !validOpenAIReasoningEffort(effort) {
			t.Fatalf("approved effort %q rejected", effort)
		}
	}
	if calls != 0 {
		t.Fatalf("provider calls=%d, want zero for rejected efforts", calls)
	}
}

func TestCreateOpenAITextResponsePlacesResponsesAttachmentsBeforeText(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Write([]byte(`{"model":"gpt-5.6-terra","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"read it"}]}]}`))
	}))
	defer server.Close()
	routeOpenAIResponsesToTestServer(t, server.URL)

	_, err := createOpenAITextResponseHTTP(context.Background(), "test-key", openAITextRequest{
		Model: "gpt-5.6-terra",
		Input: "what does this say?",
		Attachments: []openAIInputContent{
			{Type: "input_image", ImageURL: "data:image/png;base64,cmFzdGVy"},
			{Type: "input_file", Filename: "brief.pdf", FileData: "data:application/pdf;base64,JVBERg=="},
		},
	})
	if err != nil {
		t.Fatalf("create response: %v", err)
	}
	input, ok := payload["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("input=%#v, want one user message", payload["input"])
	}
	message, _ := input[0].(map[string]any)
	content, _ := message["content"].([]any)
	if message["role"] != "user" || len(content) != 3 {
		t.Fatalf("message=%#v, want attachment + attachment + text", message)
	}
	image, _ := content[0].(map[string]any)
	file, _ := content[1].(map[string]any)
	textBlock, _ := content[2].(map[string]any)
	if image["type"] != "input_image" || image["image_url"] != "data:image/png;base64,cmFzdGVy" {
		t.Fatalf("image content=%#v", image)
	}
	if file["type"] != "input_file" || file["filename"] != "brief.pdf" || file["file_data"] != "data:application/pdf;base64,JVBERg==" {
		t.Fatalf("file content=%#v", file)
	}
	if textBlock["type"] != "input_text" || textBlock["text"] != "what does this say?" {
		t.Fatalf("text content=%#v", textBlock)
	}
}

func TestCreateOpenAITextResponseDecodesUsageAndRecordsSeatEntry(t *testing.T) {
	dir := openAIResponsesLedgerDir(t)

	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		// Real Responses API wire shape: input_tokens INCLUSIVE of the cached
		// reads under input_tokens_details.cached_tokens.
		w.Write([]byte(`{
			"model": "gpt-5.5",
			"output": [{"type": "message", "content": [{"type": "output_text", "text": "hello books"}]}],
			"usage": {
				"input_tokens": 100,
				"input_tokens_details": {"cached_tokens": 40},
				"output_tokens": 25,
				"output_tokens_details": {"reasoning_tokens": 5},
				"total_tokens": 125
			}
		}`))
	}))
	defer server.Close()
	routeOpenAIResponsesToTestServer(t, server.URL)

	text, err := createOpenAITextResponseHTTP(context.Background(), "test-key", openAITextRequest{
		Model: "gpt-5.5",
		Seat:  seatBrain,
		Input: "summarize",
	})
	if err != nil {
		t.Fatalf("createOpenAITextResponseHTTP: %v", err)
	}
	if text != "hello books" {
		t.Fatalf("text=%q, want hello books", text)
	}
	if gotPath != "/v1/responses" {
		t.Fatalf("wire path=%q, want the fixed /v1/responses endpoint", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("auth header=%q, want Bearer test-key", gotAuth)
	}

	rows := readLedgerLines(t, filepath.Join(dir, "usage-2026-07-11.jsonl"))
	if len(rows) != 1 {
		t.Fatalf("usage rows=%d, want exactly one entry per wire call", len(rows))
	}
	row := rows[0]
	if row["provider"] != providerOpenAI || row["model"] != "gpt-5.5" || row["seat"] != seatBrain {
		t.Fatalf("entry provenance wrong: %v", row)
	}
	// The cached split: ledger input_tokens is the UNCACHED portion.
	if row["input_tokens"].(float64) != 60 {
		t.Fatalf("input_tokens=%v, want 60 (100 wire minus 40 cached)", row["input_tokens"])
	}
	if row["cached_input_tokens"].(float64) != 40 {
		t.Fatalf("cached_input_tokens=%v, want 40", row["cached_input_tokens"])
	}
	if row["output_tokens"].(float64) != 25 {
		t.Fatalf("output_tokens=%v, want 25", row["output_tokens"])
	}
	if cost, ok := row["est_cost_usd"].(float64); !ok || cost <= 0 {
		t.Fatalf("est_cost_usd=%v, want a positive computed cost for a priced model", row["est_cost_usd"])
	}
	if _, flagged := row["price_missing"]; flagged {
		t.Fatalf("price_missing stamped for gpt-5.5: %v", row)
	}
	if _, hasErr := row["error"]; hasErr {
		t.Fatalf("error stamped on a successful call: %v", row)
	}
}

func TestCreateOpenAITextResponseRecordsUntaggedSeat(t *testing.T) {
	dir := openAIResponsesLedgerDir(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"model":"gpt-5.5","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":10,"output_tokens":2}}`))
	}))
	defer server.Close()
	routeOpenAIResponsesToTestServer(t, server.URL)

	if _, err := createOpenAITextResponseHTTP(context.Background(), "test-key", openAITextRequest{Model: "gpt-5.5", Input: "hi"}); err != nil {
		t.Fatalf("createOpenAITextResponseHTTP: %v", err)
	}

	rows := readLedgerLines(t, filepath.Join(dir, "usage-2026-07-11.jsonl"))
	if len(rows) != 1 {
		t.Fatalf("usage rows=%d, want 1", len(rows))
	}
	if rows[0]["seat"] != seatUntagged {
		t.Fatalf("seat=%v, want %q — untagged calls must be VISIBLY untagged", rows[0]["seat"], seatUntagged)
	}
}

func TestCreateOpenAITextResponseRecordsErrorEntryOnFailure(t *testing.T) {
	dir := openAIResponsesLedgerDir(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error": {"message": "rate_limit reached"}}`))
	}))
	defer server.Close()
	routeOpenAIResponsesToTestServer(t, server.URL)

	_, err := createOpenAITextResponseHTTP(context.Background(), "test-key", openAITextRequest{
		Model: "gpt-5.5",
		Seat:  seatBoard,
		Input: "hi",
	})
	if err == nil {
		t.Fatal("expected an error from a 429 response")
	}

	rows := readLedgerLines(t, filepath.Join(dir, "usage-2026-07-11.jsonl"))
	if len(rows) != 1 {
		t.Fatalf("usage rows=%d, want 1 (failed calls still cost latency and must be booked)", len(rows))
	}
	row := rows[0]
	if row["seat"] != seatBoard || row["provider"] != providerOpenAI {
		t.Fatalf("failed-call provenance wrong: %v", row)
	}
	message, _ := row["error"].(string)
	if strings.TrimSpace(message) == "" {
		t.Fatalf("error field empty on a failed call: %v", row)
	}
	if _, hasTokens := row["input_tokens"]; hasTokens {
		t.Fatalf("token fields stamped on a failed call with no usage: %v", row)
	}
}

func TestCreateOpenAITextResponseEnablesWebSearchAndPreservesCitationURLs(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Write([]byte(`{
			"id":"resp_web_search_test",
			"model":"gpt-5.5",
			"status":"completed",
			"output":[{"type":"web_search_call"},{"type":"message","content":[{"type":"output_text","text":"The official release confirms the feature.","annotations":[
				{"type":"url_citation","url":"https://docs.example.com/releases/2026","title":"Official 2026 release"},
				{"type":"url_citation","url":"javascript:alert(1)","title":"unsafe"}
			]}]}]
		}`))
	}))
	defer server.Close()
	routeOpenAIResponsesToTestServer(t, server.URL)

	text, err := createOpenAITextResponseHTTP(context.Background(), "test-key", openAITextRequest{
		Model: "gpt-5.5", Input: "verify the current release", EnableWebSearch: true, MaxToolCalls: 99,
	})
	if err != nil {
		t.Fatalf("create response: %v", err)
	}
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools=%#v, want one hosted search tool", payload["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != "web_search" {
		t.Fatalf("tool=%#v, want current Responses web_search contract", tool)
	}
	if payload["tool_choice"] != "required" {
		t.Fatalf("tool_choice=%#v, want required for a source-bound research run", payload["tool_choice"])
	}
	include, _ := payload["include"].([]any)
	if len(include) != 1 || include[0] != "web_search_call.action.sources" {
		t.Fatalf("include=%#v, want provider-owned hosted-search sources", payload["include"])
	}
	if _, found := payload["max_tool_calls"]; found {
		t.Fatalf("ordinary hosted search unexpectedly received external-evidence tool budget: %#v", payload["max_tool_calls"])
	}
	if !strings.Contains(text, "Official 2026 release — https://docs.example.com/releases/2026") {
		t.Fatalf("text=%q, want durable exact citation URL", text)
	}
	if !strings.Contains(text, "stride-web-citation-receipt:v1") {
		t.Fatalf("text=%q, want server-owned citation receipt", text)
	}
	if strings.Contains(text, "javascript:") {
		t.Fatalf("text=%q, unsafe citation URL was preserved", text)
	}
}

func TestCreateOpenAITextResponseRetainsWebReceiptForStructuredEvidence(t *testing.T) {
	var payload map[string]any
	exactURL := "https://example.org/creator_(program)."
	exactEvidence := strings.ReplaceAll(focusedExternalEvidenceJSONForTest(), "https://example.org/creator-program", exactURL)
	rawEvidence := strings.ReplaceAll(exactEvidence, `"`, `\"`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = fmt.Fprintf(w, `{
			"id":"resp_structured_web_evidence",
			"model":"gpt-5.5",
			"status":"completed",
			"output":[{"type":"web_search_call","action":{"sources":[
				{"type":"url","url":"https://example.org/creator_(program).","title":"Official creator program"},
				{"type":"url","url":"https://example.org/creator_(program).","title":"duplicate"},
				{"type":"url","url":"https://example.org/engagement-methodology","title":"Official engagement methodology"},
				{"type":"url","url":" https://example.org/padded ","title":"padded"},
				{"type":"url","url":"http://example.org/insecure","title":"insecure"},
				{"type":"url","url":"javascript:alert(1)","title":"unsafe"}
			]}},{"type":"message","content":[{"type":"output_text","text":"%s","annotations":[]}]}]
		}`, rawEvidence)
	}))
	defer server.Close()
	routeOpenAIResponsesToTestServer(t, server.URL)

	text, err := createOpenAITextResponseHTTP(context.Background(), "test-key", openAITextRequest{
		Model: "gpt-5.5", Input: "verify the proof points", EnableWebSearch: true,
		MaxToolCalls: 99,
		JSONSchema:   externalEvidenceJSONSchema(), NormalizeOutput: normalizeExternalEvidenceArtifact,
		ValidateOutput: func(text string) error { return validateExternalEvidenceArtifact(text) },
	})
	if err != nil {
		t.Fatalf("create structured evidence response: %v", err)
	}
	format, _ := payload["text"].(map[string]any)
	strict, _ := format["format"].(map[string]any)
	if strict["type"] != "json_schema" || strict["name"] != packagingStudioExternalEvidenceContract {
		t.Fatalf("structured format=%#v", strict)
	}
	if payload["tool_choice"] != "required" {
		t.Fatalf("tool_choice=%#v, want required", payload["tool_choice"])
	}
	include, _ := payload["include"].([]any)
	if len(include) != 1 || include[0] != "web_search_call.action.sources" {
		t.Fatalf("include=%#v, want hosted-search action sources", payload["include"])
	}
	if payload["max_tool_calls"] != float64(6) {
		t.Fatalf("max_tool_calls=%#v, want server-owned external evidence budget", payload["max_tool_calls"])
	}
	if !strings.Contains(text, "## Provider-fetched evidence ledger") || !strings.Contains(text, "stride-web-citation-receipt:v1") || strings.HasPrefix(strings.TrimSpace(text), "{") {
		t.Fatalf("structured evidence was not normalized with its provider receipt:\n%s", text)
	}
	if strings.Contains(text, "javascript:") {
		t.Fatalf("unsafe action source reached receipt:\n%s", text)
	}
	if strings.Contains(text, "http://example.org/insecure") {
		t.Fatalf("non-HTTPS action source reached receipt:\n%s", text)
	}
	if strings.Contains(text, "example.org/padded") {
		t.Fatalf("non-bare padded action source reached receipt:\n%s", text)
	}
	if got := strings.Count(text, exactURL); got != 2 {
		t.Fatalf("exact provider URL occurrences=%d, want one ledger row and one receipt row:\n%s", got, text)
	}
	if rows, rowErr := externalEvidenceLedgerRows(stripOpenAIWebCitationReceipt(text)); rowErr != nil || len(rows) != 2 || len(rows[0]) != 8 {
		t.Fatalf("canonical rows=%#v err=%v", rows, rowErr)
	}
}

func TestExtractOpenAIResponseWebEvidenceRetainsLargeCompleteProviderSet(t *testing.T) {
	_, _, providerURLs := largeExternalEvidenceFixtureForTest(t)
	outputs := make([]map[string]any, 13)
	for index := range outputs {
		outputs[index] = map[string]any{
			"type":   "web_search_call",
			"action": map[string]any{"sources": []map[string]any{}},
		}
	}
	for index, sourceURL := range providerURLs {
		call := outputs[0]
		action := call["action"].(map[string]any)
		sources := action["sources"].([]map[string]any)
		action["sources"] = append(sources, map[string]any{"type": "url", "title": fmt.Sprintf("Primary source %03d", index), "url": sourceURL})
	}
	raw, err := json.Marshal(map[string]any{"id": "resp_large_extract", "output": outputs})
	if err != nil {
		t.Fatalf("marshal large provider response: %v", err)
	}
	var body openAIResponsesBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal large provider response: %v", err)
	}
	evidence := extractOpenAIResponseWebEvidence(body)
	last := openAIResponseWebCitation{}
	if len(evidence.Citations) > 0 {
		last = evidence.Citations[len(evidence.Citations)-1]
	}
	if evidence.SearchCalls != 13 || len(evidence.Citations) != 166 || last.URL != providerURLs[165] {
		t.Fatalf("extracted evidence calls=%d sources=%d last=%#v, want 13/166/%s", evidence.SearchCalls, len(evidence.Citations), last, providerURLs[165])
	}
}

func TestOpenAIResponsesRequestTimeoutIsScopedToWorkShape(t *testing.T) {
	if got := openAIResponsesRequestTimeout(openAITextRequest{}); got != 45*time.Second {
		t.Fatalf("compact timeout=%s, want 45s", got)
	}
	if got := openAIResponsesRequestTimeout(openAITextRequest{MaxOutputTokens: 8000}); got != 120*time.Second {
		t.Fatalf("long-form timeout=%s, want 120s", got)
	}
	t.Setenv("BONFIRE_ORCHESTRATOR_TIMEOUT", "7m")
	if got := openAIResponsesRequestTimeout(openAITextRequest{MaxOutputTokens: 32000, LongRunning: true}); got != 7*time.Minute {
		t.Fatalf("grounded deliverable timeout=%s, want 7m", got)
	}
	if got := openAIResponsesRequestTimeout(openAITextRequest{MaxOutputTokens: 8000, EnableWebSearch: true}); got != 0 {
		t.Fatalf("hosted-search timeout=%s, want cancellation-owned work with no artificial deadline", got)
	}
}
