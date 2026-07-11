package main

import (
	"context"
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
