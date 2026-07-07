package main

// Tests for the fiscal.ai MCP client (fiscal_client.go): the 3-step handshake
// against a fake MCP server (Bearer auth, session-id capture, protocol
// sequence), SSE and plain-JSON body parsing, the keyless clear-error
// contract (the fake must never be hit), upstream error bodies never
// surfacing, the 4MB response bound, isError text carried into the error,
// injection-safe JSON embedding of user args, and the compact docs index
// with its success-only cache.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const fiscalTestSessionID = "fake-session-0123abcd"

// fiscalFakeMCP is the fake vendor server: it enforces the auth + session
// protocol on every request and records the method sequence and every
// execute_code payload.
type fiscalFakeMCP struct {
	mu        sync.Mutex
	sequence  []string
	codes     []string
	toolCalls int

	// replyText builds the tools/call content text; nil replies "ok".
	replyText func(tool, code string) string
	plainJSON bool
	isError   bool
}

func (fake *fiscalFakeMCP) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var request struct {
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Errorf("fake MCP got a non-JSON body: %v", err)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-fiscal-key" {
			t.Errorf("authorization=%q, want the FISCAL_AI_API_KEY bearer", got)
		}
		if accept := r.Header.Get("Accept"); !strings.Contains(accept, "text/event-stream") {
			t.Errorf("accept=%q does not offer text/event-stream", accept)
		}
		fake.mu.Lock()
		fake.sequence = append(fake.sequence, request.Method)
		fake.mu.Unlock()

		switch request.Method {
		case "initialize":
			if r.Header.Get("Mcp-Session-Id") != "" {
				t.Error("initialize must not carry a session id")
			}
			w.Header().Set("Mcp-Session-Id", fiscalTestSessionID)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: message\ndata: {\"result\":{\"serverInfo\":{\"name\":\"fake\"}},\"jsonrpc\":\"2.0\",\"id\":1}\n\n")
		case "notifications/initialized":
			if got := r.Header.Get("Mcp-Session-Id"); got != fiscalTestSessionID {
				t.Errorf("initialized session id=%q, want %q", got, fiscalTestSessionID)
			}
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			if got := r.Header.Get("Mcp-Session-Id"); got != fiscalTestSessionID {
				t.Errorf("tools/call session id=%q, want %q", got, fiscalTestSessionID)
			}
			code, _ := request.Params.Arguments["code"].(string)
			fake.mu.Lock()
			fake.codes = append(fake.codes, code)
			fake.toolCalls++
			fake.mu.Unlock()
			text := "ok"
			if fake.replyText != nil {
				text = fake.replyText(request.Params.Name, code)
			}
			payload, _ := json.Marshal(map[string]any{
				"result":  map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": fake.isError},
				"jsonrpc": "2.0",
				"id":      2,
			})
			if fake.plainJSON {
				w.Header().Set("Content-Type", "application/json")
				w.Write(payload)
			} else {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "event: message\ndata: %s\n\n", payload)
			}
		default:
			t.Errorf("fake MCP got unexpected method %q", request.Method)
		}
	}
}

func (fake *fiscalFakeMCP) lastCode(t *testing.T) string {
	t.Helper()
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.codes) == 0 {
		t.Fatal("no execute_code payload reached the fake MCP")
	}
	return fake.codes[len(fake.codes)-1]
}

func (fake *fiscalFakeMCP) toolCallCount() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.toolCalls
}

// withFakeFiscalMCP points fiscalMCPURL at a fake server for one test.
func withFakeFiscalMCP(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	original := fiscalMCPURL
	fiscalMCPURL = server.URL
	t.Cleanup(func() {
		fiscalMCPURL = original
		server.Close()
	})
}

// resetFiscalDocsCache empties the docs cache for one test and restores it.
func resetFiscalDocsCache(t *testing.T) {
	t.Helper()
	fiscalDocsMu.Lock()
	original := fiscalDocsCached
	fiscalDocsCached = ""
	fiscalDocsMu.Unlock()
	t.Cleanup(func() {
		fiscalDocsMu.Lock()
		fiscalDocsCached = original
		fiscalDocsMu.Unlock()
	})
}

// The core protocol contract: initialize → notifications/initialized →
// tools/call, each with the Bearer key, the captured session id riding the
// post-initialize requests, and the SSE data line parsed into the text.
func TestFiscalExecuteCodeHandshakeAndSSEParse(t *testing.T) {
	t.Setenv("FISCAL_AI_API_KEY", "test-fiscal-key")
	fake := &fiscalFakeMCP{replyText: func(tool, code string) string {
		if tool != "execute_code" {
			t.Errorf("tool=%q, want execute_code", tool)
		}
		if !strings.Contains(code, "console.log") {
			t.Errorf("code payload did not arrive: %q", code)
		}
		return `{"answer":42}`
	}}
	withFakeFiscalMCP(t, fake.handler(t))

	text, err := fiscalExecuteCode(context.Background(), `async () => { console.log("hi"); }`)
	if err != nil {
		t.Fatalf("fiscalExecuteCode: %v", err)
	}
	if text != `{"answer":42}` {
		t.Fatalf("text=%q, want the content text from the SSE data line", text)
	}
	want := []string{"initialize", "notifications/initialized", "tools/call"}
	if strings.Join(fake.sequence, ",") != strings.Join(want, ",") {
		t.Fatalf("protocol sequence=%v, want %v", fake.sequence, want)
	}
}

// A plain JSON body (no SSE framing) must parse too — defensive on transport.
func TestFiscalExecuteCodePlainJSONBody(t *testing.T) {
	t.Setenv("FISCAL_AI_API_KEY", "test-fiscal-key")
	fake := &fiscalFakeMCP{plainJSON: true, replyText: func(string, string) string { return "plain-body-result" }}
	withFakeFiscalMCP(t, fake.handler(t))

	text, err := fiscalExecuteCode(context.Background(), "async () => {}")
	if err != nil {
		t.Fatalf("fiscalExecuteCode on plain JSON body: %v", err)
	}
	if text != "plain-body-result" {
		t.Fatalf("text=%q, want plain-body-result", text)
	}
}

// Keyless: an immediate clear error naming FISCAL_AI_API_KEY, no HTTP ever.
func TestFiscalKeylessClearErrorNoHTTP(t *testing.T) {
	t.Setenv("FISCAL_AI_API_KEY", "")
	resetFiscalDocsCache(t)
	withFakeFiscalMCP(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("keyless run must never reach the MCP server")
	})

	if hasFiscalAPIKey() {
		t.Fatal("hasFiscalAPIKey()=true with an empty env")
	}
	for name, call := range map[string]func() error{
		"fiscalExecuteCode": func() error { _, err := fiscalExecuteCode(context.Background(), "async () => {}"); return err },
		"fiscalAPIDocs":     func() error { _, err := fiscalAPIDocs(context.Background()); return err },
		"snapshot":          func() error { _, err := fiscalCompanySnapshot(context.Background(), "NFLX"); return err },
		"comps":             func() error { _, err := fiscalComps(context.Background(), "NFLX", nil, 0); return err },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "FISCAL_AI_API_KEY") {
			t.Fatalf("%s keyless err=%v, want a clear FISCAL_AI_API_KEY error", name, err)
		}
	}
}

// An upstream 401 body (which can carry vendor internals) must never appear
// in the returned error string — only the status survives.
func TestFiscalUpstreamErrorBodyNeverSurfaces(t *testing.T) {
	t.Setenv("FISCAL_AI_API_KEY", "test-fiscal-key")
	const secret = "super-secret-upstream-diagnostic-4711"
	withFakeFiscalMCP(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, secret)
	})

	_, err := fiscalExecuteCode(context.Background(), "async () => {}")
	if err == nil {
		t.Fatal("want an error on upstream 401")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("upstream body leaked into the error: %v", err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("err=%v, want the status to survive", err)
	}
}

// result.isError carries useful vendor text (e.g. the valid-ratioId hint) —
// the error must include it.
func TestFiscalExecuteCodeIsErrorCarriesContentText(t *testing.T) {
	t.Setenv("FISCAL_AI_API_KEY", "test-fiscal-key")
	const detail = "Execution error: Invalid ratioId, see /v1/ratios-list"
	fake := &fiscalFakeMCP{isError: true, replyText: func(string, string) string { return detail }}
	withFakeFiscalMCP(t, fake.handler(t))

	_, err := fiscalExecuteCode(context.Background(), "async () => {}")
	if err == nil || !strings.Contains(err.Error(), detail) {
		t.Fatalf("err=%v, want the isError content text carried through", err)
	}
}

// A JSON-RPC error object becomes an error.
func TestFiscalExecuteCodeJSONRPCError(t *testing.T) {
	t.Setenv("FISCAL_AI_API_KEY", "test-fiscal-key")
	var step atomic.Int32
	withFakeFiscalMCP(t, func(w http.ResponseWriter, r *http.Request) {
		switch step.Add(1) {
		case 1:
			w.Header().Set("Mcp-Session-Id", fiscalTestSessionID)
			fmt.Fprint(w, "event: message\ndata: {\"result\":{},\"jsonrpc\":\"2.0\",\"id\":1}\n\n")
		case 2:
			w.WriteHeader(http.StatusAccepted)
		default:
			fmt.Fprint(w, "event: message\ndata: {\"error\":{\"code\":-32600,\"message\":\"bad request shape\"},\"jsonrpc\":\"2.0\",\"id\":2}\n\n")
		}
	})

	_, err := fiscalExecuteCode(context.Background(), "async () => {}")
	if err == nil || !strings.Contains(err.Error(), "bad request shape") || !strings.Contains(err.Error(), "-32600") {
		t.Fatalf("err=%v, want the JSON-RPC error surfaced with its code", err)
	}
}

// An oversized response is bounded by the 4MB LimitReader: the call returns
// (an error), it does not hang or balloon.
func TestFiscalOversizedResponseBounded(t *testing.T) {
	t.Setenv("FISCAL_AI_API_KEY", "test-fiscal-key")
	var step atomic.Int32
	withFakeFiscalMCP(t, func(w http.ResponseWriter, r *http.Request) {
		switch step.Add(1) {
		case 1:
			w.Header().Set("Mcp-Session-Id", fiscalTestSessionID)
			fmt.Fprint(w, "event: message\ndata: {\"result\":{},\"jsonrpc\":\"2.0\",\"id\":1}\n\n")
		case 2:
			w.WriteHeader(http.StatusAccepted)
		default:
			// A data line whose JSON extends past the 4MB cap: the truncated
			// read must yield a decode error, never an unbounded buffer.
			fmt.Fprint(w, "event: message\ndata: {\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"")
			filler := strings.Repeat("a", 1<<20)
			for i := 0; i < 6; i++ {
				fmt.Fprint(w, filler)
			}
			fmt.Fprint(w, "\"}]},\"jsonrpc\":\"2.0\",\"id\":2}\n\n")
		}
	})

	text, err := fiscalExecuteCode(context.Background(), "async () => {}")
	if err == nil {
		t.Fatalf("want an error on a truncated oversized response, got %d bytes of text", len(text))
	}
	if len(text) > fiscalMaxResponseBytes {
		t.Fatalf("returned text exceeds the response bound: %d bytes", len(text))
	}
}

// Injection safety: a company "name" full of quotes and backticks reaches the
// code payload only in its JSON-escaped form, inside the const declaration —
// it can never terminate the string literal or the arrow function.
func TestFiscalCompanySnapshotInjectionSafeArgs(t *testing.T) {
	t.Setenv("FISCAL_AI_API_KEY", "test-fiscal-key")
	hostile := "Ne\"tflix`); console.log(\"pwned\"); (`"
	fake := &fiscalFakeMCP{replyText: func(tool, code string) string {
		return `{"name":"Netflix, Inc.","companyKey":"NASDAQ_NFLX","terminalUrl":"https://fiscal.ai/company/NasdaqGS-NFLX"}`
	}}
	withFakeFiscalMCP(t, fake.handler(t))

	snapshot, err := fiscalCompanySnapshot(context.Background(), hostile)
	if err != nil {
		t.Fatalf("fiscalCompanySnapshot: %v", err)
	}
	code := fake.lastCode(t)
	if want := "const query = " + fiscalJSONArg(hostile) + ";"; !strings.Contains(code, want) {
		t.Fatalf("code payload does not embed the JSON-escaped arg %q:\n%s", want, code)
	}
	// The raw (unescaped) quote sequences must not appear — json.Marshal
	// turned every interior quote into \", so neither the string-terminating
	// quote nor the injected console.log survives verbatim.
	if strings.Contains(code, `Ne"tflix`) || strings.Contains(code, `console.log("pwned")`) {
		t.Fatalf("raw unescaped injection sequence leaked into the code payload:\n%s", code)
	}
	if snapshot["companyKey"] != "NASDAQ_NFLX" {
		t.Fatalf("snapshot=%v, want the parsed console.log JSON", snapshot)
	}
}

// fiscalComps defaults: nil ratio ids become the three defaults, peerLimit 0
// becomes 6, and the parsed rows come back shaped for a table.
func TestFiscalCompsDefaultsAndRowShape(t *testing.T) {
	t.Setenv("FISCAL_AI_API_KEY", "test-fiscal-key")
	fake := &fiscalFakeMCP{replyText: func(tool, code string) string {
		return `{"subject":{"companyKey":"NASDAQ_NFLX","name":"Netflix, Inc.","terminalUrl":"https://fiscal.ai/company/NasdaqGS-NFLX"},` +
			`"ratioIds":["ratio_ev_to_ebitda"],` +
			`"rows":[{"companyKey":"NASDAQ_NFLX","displayName":"Netflix, Inc.","relationship":"subject","terminalUrl":"https://fiscal.ai/company/NasdaqGS-NFLX","ratios":{"ratio_ev_to_ebitda":{"value":22.5,"date":"2026-07-06"}}}]}`
	}}
	withFakeFiscalMCP(t, fake.handler(t))

	comps, err := fiscalComps(context.Background(), "NASDAQ_NFLX", nil, 0)
	if err != nil {
		t.Fatalf("fiscalComps: %v", err)
	}
	code := fake.lastCode(t)
	for _, ratioID := range fiscalDefaultRatioIDs {
		if !strings.Contains(code, `"`+ratioID+`"`) {
			t.Fatalf("code payload missing default ratio id %s:\n%s", ratioID, code)
		}
	}
	if !strings.Contains(code, "const peerLimit = 6;") {
		t.Fatalf("code payload missing the default peer limit:\n%s", code)
	}
	rows, ok := comps["rows"].([]any)
	if !ok || len(rows) == 0 {
		t.Fatalf("comps rows=%v, want a parsed row slice", comps["rows"])
	}
	row, ok := rows[0].(map[string]any)
	if !ok || row["relationship"] != "subject" || row["terminalUrl"] == "" {
		t.Fatalf("row[0]=%v, want the subject row with a terminalUrl", rows[0])
	}
}

// The compact docs index: every codemode function gets one line with the
// first sentence of its doc comment (multi-line comments included), the
// Usage notes and Data patterns sections ride along, later sections do not —
// and success is cached while failures retry.
func TestFiscalAPIDocsCompactAndCache(t *testing.T) {
	t.Setenv("FISCAL_AI_API_KEY", "test-fiscal-key")
	resetFiscalDocsCache(t)

	const fakeDocs = `# Fiscal.ai API

Company identifiers use the format ` + "`<EXCHANGE>_<TICKER>`" + `.

## Available functions

declare const codemode: {
  /** Get all available companies with their exchange, ticker, and basic info. */
  companies_list: (input: { pageNumber?: number }) => Promise<{}>;
  /** Get detailed company profile including key identifiers.

Long elaboration paragraph that must NOT ride the compact index. */
  company_profile: (input: { companyKey: string }) => Promise<{}>;
  /** Get daily time-series data for a specific financial ratio, useful for charting trends. */
  company_daily_ratios: (input: { ratioId: string; companyKey: string }) => Promise<Array<{}>>;
};

## Usage notes
- Code MUST be an async arrow function
- Use console.log() to output results

## Data patterns
- Financials endpoints return { metrics, data }

## Artifact guidelines
- This section must not ride the compact index.
`
	// First: a failing fetch must not poison the cache (the retry below works).
	withFakeFiscalMCP(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if _, err := fiscalAPIDocsCompact(context.Background()); err == nil {
		t.Fatal("want an error from a failing docs fetch")
	}

	// Now the real fake: the retry succeeds, parses, and caches.
	fake := &fiscalFakeMCP{replyText: func(tool, code string) string {
		if tool != "api_docs" {
			t.Errorf("tool=%q, want api_docs", tool)
		}
		return fakeDocs
	}}
	withFakeFiscalMCP(t, fake.handler(t))

	compact, err := fiscalAPIDocsCompact(context.Background())
	if err != nil {
		t.Fatalf("fiscalAPIDocsCompact: %v", err)
	}
	for _, want := range []string{
		"- companies_list: Get all available companies with their exchange, ticker, and basic info.",
		"- company_profile: Get detailed company profile including key identifiers.",
		"- company_daily_ratios: Get daily time-series data for a specific financial ratio, useful for charting trends.",
		"## Usage notes",
		"## Data patterns",
	} {
		if !strings.Contains(compact, want) {
			t.Fatalf("compact index missing %q:\n%s", want, compact)
		}
	}
	if strings.Contains(compact, "Long elaboration paragraph") {
		t.Fatalf("compact index carries a doc comment's elaboration:\n%s", compact)
	}
	if strings.Contains(compact, "Artifact guidelines") {
		t.Fatalf("compact index carries sections past Data patterns:\n%s", compact)
	}

	// Cached: a second call must not hit the server again.
	before := fake.toolCallCount()
	if _, err := fiscalAPIDocsCompact(context.Background()); err != nil {
		t.Fatalf("cached fiscalAPIDocsCompact: %v", err)
	}
	if after := fake.toolCallCount(); after != before {
		t.Fatalf("docs fetched again despite the cache (calls %d -> %d)", before, after)
	}
}
