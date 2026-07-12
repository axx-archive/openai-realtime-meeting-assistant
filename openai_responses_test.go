package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

func TestCreateOpenAITextResponseSendsStrictSchemaAndRejectsTruncation(t *testing.T) {
	dir := openAIResponsesLedgerDir(t)
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Write([]byte(`{"status":"incomplete","service_tier":"priority","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","content":[{"type":"output_text","text":"{\"meetingId\":\"cut"}]}],"usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	defer server.Close()
	t.Setenv("OPENAI_RESPONSES_BASE_URL", server.URL)

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
		w.Write([]byte(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"not json"}]}],"usage":{"input_tokens":7,"output_tokens":2}}`))
	}))
	defer server.Close()
	t.Setenv("OPENAI_RESPONSES_BASE_URL", server.URL)

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

func TestOpenAIResponsesURLDefaultAndOverride(t *testing.T) {
	t.Setenv("OPENAI_RESPONSES_BASE_URL", "")
	if got := openAIResponsesURL(); got != "https://api.openai.com/v1/responses" {
		t.Fatalf("default responses URL=%q, want the unchanged api.openai.com wire endpoint", got)
	}

	// The override is a BASE url; the /responses path is appended, trailing
	// slashes tolerated (W1 item 14: gateway/Venice shelf-readiness).
	t.Setenv("OPENAI_RESPONSES_BASE_URL", "https://gateway.example/v1")
	if got := openAIResponsesURL(); got != "https://gateway.example/v1/responses" {
		t.Fatalf("override responses URL=%q, want https://gateway.example/v1/responses", got)
	}
	t.Setenv("OPENAI_RESPONSES_BASE_URL", "https://gateway.example/v1/")
	if got := openAIResponsesURL(); got != "https://gateway.example/v1/responses" {
		t.Fatalf("trailing-slash override URL=%q, want https://gateway.example/v1/responses", got)
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
	t.Setenv("OPENAI_RESPONSES_BASE_URL", server.URL)

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
	if gotPath != "/responses" {
		t.Fatalf("wire path=%q, want /responses appended to the base override", gotPath)
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
		w.Write([]byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":10,"output_tokens":2}}`))
	}))
	defer server.Close()
	t.Setenv("OPENAI_RESPONSES_BASE_URL", server.URL)

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
	t.Setenv("OPENAI_RESPONSES_BASE_URL", server.URL)

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
