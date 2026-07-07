package main

// Tests for the fiscal.ai tool wiring (kanban.go + agent_runner_anthropic.go):
// the four tool definitions ride kanbanTools() with the right required params,
// allowlist membership (all four orchestrator, exactly the two typed tools on
// private voice), keyless dispatch returning the clear not-configured payload
// with NO HTTP, and happy-path dispatches proving args flow through
// applyToolCallArgs into the client's execute_code payload. The fake MCP
// server + fiscalMCPURL swap come from fiscal_client_test.go.

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

var fiscalToolNames = []string{
	"company_financial_snapshot",
	"financial_comps",
	"fiscal_api_docs",
	"fiscal_data_query",
}

func kanbanToolByName(t *testing.T, app *kanbanBoardApp, name string) map[string]any {
	t.Helper()
	for _, tool := range app.kanbanTools() {
		if asString(tool["name"]) == name {
			return tool
		}
	}
	t.Fatalf("tool %q is not registered in kanbanTools()", name)
	return nil
}

func toolSchema(t *testing.T, tool map[string]any) (properties map[string]any, required []string) {
	t.Helper()
	schema, ok := tool["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("tool %q has no parameters schema", asString(tool["name"]))
	}
	properties, _ = schema["properties"].(map[string]any)
	required, _ = schema["required"].([]string)
	return properties, required
}

// The four definitions: present, correct required params, and every
// description declaring the read-only + FISCAL_AI_API_KEY contract (they ride
// every realtime voice session, so the contract must be in the text).
func TestFiscalToolDefinitionsRegistered(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	for _, name := range fiscalToolNames {
		description := asString(kanbanToolByName(t, app, name)["description"])
		if !strings.Contains(description, "Read-only") || !strings.Contains(description, "FISCAL_AI_API_KEY") {
			t.Fatalf("%s description does not declare read-only + FISCAL_AI_API_KEY: %q", name, description)
		}
	}

	properties, required := toolSchema(t, kanbanToolByName(t, app, "company_financial_snapshot"))
	if len(required) != 1 || required[0] != "company" {
		t.Fatalf("company_financial_snapshot required=%v, want [company]", required)
	}
	if _, ok := properties["company"]; !ok {
		t.Fatal("company_financial_snapshot has no company property")
	}

	properties, required = toolSchema(t, kanbanToolByName(t, app, "financial_comps"))
	if len(required) != 1 || required[0] != "company" {
		t.Fatalf("financial_comps required=%v, want [company]", required)
	}
	for _, key := range []string{"company", "ratio_ids", "peer_limit"} {
		if _, ok := properties[key]; !ok {
			t.Fatalf("financial_comps has no %s property", key)
		}
	}

	properties, required = toolSchema(t, kanbanToolByName(t, app, "fiscal_api_docs"))
	if len(required) != 0 {
		t.Fatalf("fiscal_api_docs required=%v, want none (topic defaults to index)", required)
	}
	topic, _ := properties["topic"].(map[string]any)
	if enum, _ := topic["enum"].([]string); len(enum) != 2 || enum[0] != "index" || enum[1] != "full" {
		t.Fatalf("fiscal_api_docs topic enum=%v, want [index full]", topic["enum"])
	}

	properties, required = toolSchema(t, kanbanToolByName(t, app, "fiscal_data_query"))
	if len(required) != 1 || required[0] != "code" {
		t.Fatalf("fiscal_data_query required=%v, want [code]", required)
	}
	for _, key := range []string{"code", "max_chars"} {
		if _, ok := properties[key]; !ok {
			t.Fatalf("fiscal_data_query has no %s property", key)
		}
	}
}

// Allowlists: the orchestrator gets all four; private voice gets exactly the
// two typed tools — docs and raw query are too heavy for a voice turn.
func TestFiscalToolAllowlists(t *testing.T) {
	for _, name := range fiscalToolNames {
		if !orchestratorToolAllowlist[name] {
			t.Fatalf("%s is not in orchestratorToolAllowlist", name)
		}
	}
	voiceAllowed := map[string]bool{
		"company_financial_snapshot": true,
		"financial_comps":            true,
		"fiscal_api_docs":            false,
		"fiscal_data_query":          false,
	}
	for _, name := range fiscalToolNames {
		if got := privateRealtimeVoiceToolAllowed(name); got != voiceAllowed[name] {
			t.Fatalf("privateRealtimeVoiceToolAllowed(%s)=%v, want %v", name, got, voiceAllowed[name])
		}
	}
}

// Keyless: every fiscal dispatch returns the clear not-configured payload —
// ok=false with FISCAL_AI_API_KEY in the reason, no error, no mutation — and
// the MCP server is NEVER contacted.
func TestFiscalToolsKeylessDispatchNotConfiguredNoHTTP(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("FISCAL_AI_API_KEY", "")
	withFakeFiscalMCP(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("keyless dispatch must never reach the MCP server")
	})

	argsFor := map[string]map[string]any{
		"company_financial_snapshot": {"company": "NFLX"},
		"financial_comps":            {"company": "NFLX"},
		"fiscal_api_docs":            {},
		"fiscal_data_query":          {"code": "async () => {}"},
	}
	for _, name := range fiscalToolNames {
		result, changed, err := app.applyToolCallArgs(name, argsFor[name])
		if err != nil {
			t.Fatalf("%s keyless dispatch errored: %v", name, err)
		}
		if changed {
			t.Fatalf("%s keyless dispatch reported a change", name)
		}
		if ok, _ := result["ok"].(bool); ok {
			t.Fatalf("%s keyless result=%v, want ok=false", name, result)
		}
		if reason := asString(result["reason"]); !strings.Contains(reason, "FISCAL_AI_API_KEY") {
			t.Fatalf("%s keyless reason=%q, want it to name FISCAL_AI_API_KEY", name, reason)
		}
	}
}

// Happy path: a company_financial_snapshot dispatch flows the company arg
// through applyToolCallArgs into the client's execute_code payload and returns
// the parsed snapshot under "snapshot".
func TestFiscalSnapshotDispatchArgsFlowThrough(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("FISCAL_AI_API_KEY", "test-fiscal-key")
	fake := &fiscalFakeMCP{replyText: func(tool, code string) string {
		return `{"name":"Netflix, Inc.","companyKey":"NASDAQ_NFLX","terminalUrl":"https://fiscal.ai/company/NasdaqGS-NFLX"}`
	}}
	withFakeFiscalMCP(t, fake.handler(t))

	result, changed, err := app.applyToolCallArgs("company_financial_snapshot", map[string]any{"company": "NFLX"})
	if err != nil {
		t.Fatalf("company_financial_snapshot dispatch: %v", err)
	}
	if changed {
		t.Fatal("read-only snapshot dispatch reported a change")
	}
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("result=%v, want ok=true", result)
	}
	if !strings.Contains(fake.lastCode(t), `const query = "NFLX";`) {
		t.Fatalf("company arg did not reach the execute_code payload:\n%s", fake.lastCode(t))
	}
	snapshot, ok := result["snapshot"].(map[string]any)
	if !ok || snapshot["companyKey"] != "NASDAQ_NFLX" {
		t.Fatalf("snapshot=%v, want the parsed client payload", result["snapshot"])
	}
}

// financial_comps coerces JSON-typed args ([]any ratio ids, float64 peer
// limit) into the client call — both must land in the code payload.
func TestFiscalCompsDispatchCoercesArgs(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("FISCAL_AI_API_KEY", "test-fiscal-key")
	fake := &fiscalFakeMCP{replyText: func(tool, code string) string {
		return `{"subject":{"companyKey":"NASDAQ_NFLX"},"ratioIds":["ratio_ev_to_sales"],"rows":[]}`
	}}
	withFakeFiscalMCP(t, fake.handler(t))

	result, _, err := app.applyToolCallArgs("financial_comps", map[string]any{
		"company":    "NASDAQ_NFLX",
		"ratio_ids":  []any{"ratio_ev_to_sales"},
		"peer_limit": float64(3),
	})
	if err != nil {
		t.Fatalf("financial_comps dispatch: %v", err)
	}
	code := fake.lastCode(t)
	if !strings.Contains(code, `const ratioIds = ["ratio_ev_to_sales"];`) {
		t.Fatalf("ratio_ids did not reach the execute_code payload:\n%s", code)
	}
	if !strings.Contains(code, "const peerLimit = 3;") {
		t.Fatalf("peer_limit did not reach the execute_code payload:\n%s", code)
	}
	if _, ok := result["comps"].(map[string]any); !ok {
		t.Fatalf("result=%v, want the parsed comps under \"comps\"", result)
	}
}

// fiscal_data_query truncates at max_chars with the explicit suffix; the raw
// code arrives at the sandbox verbatim.
func TestFiscalDataQueryDispatchTruncation(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("FISCAL_AI_API_KEY", "test-fiscal-key")
	long := strings.Repeat("x", 500)
	fake := &fiscalFakeMCP{replyText: func(tool, code string) string { return long }}
	withFakeFiscalMCP(t, fake.handler(t))

	const query = `async () => { console.log(JSON.stringify(await codemode.ratios_list({}))); }`
	result, _, err := app.applyToolCallArgs("fiscal_data_query", map[string]any{
		"code":      query,
		"max_chars": float64(100),
	})
	if err != nil {
		t.Fatalf("fiscal_data_query dispatch: %v", err)
	}
	if fake.lastCode(t) != query {
		t.Fatalf("code payload arrived altered: %q", fake.lastCode(t))
	}
	output := asString(result["output"])
	if !strings.HasSuffix(output, "[truncated at 100 chars]") {
		t.Fatalf("output=%q, want the explicit truncation suffix", output)
	}
	if !strings.HasPrefix(output, strings.Repeat("x", 100)) || strings.HasPrefix(output, strings.Repeat("x", 101)) {
		t.Fatalf("output kept %d chars before the suffix, want exactly 100", strings.Index(output, "\n"))
	}
}

// Every fiscal tool makes a live MCP round-trip, so all four must run off the
// datachannel event loop (realtimeToolRunsAsync) — a synchronous fiscal call
// would freeze realtime event processing for the length of the round-trip.
func TestFiscalToolsRunAsyncOnRealtime(t *testing.T) {
	for _, name := range fiscalToolNames {
		if !realtimeToolRunsAsync(name) {
			t.Fatalf("%s makes a network round-trip and must run async on the datachannel", name)
		}
	}
}

// The shared room voice session drops the heavy fiscal pair (fiscal_api_docs /
// fiscal_data_query) while keeping the spoken-ready typed pair and every other
// tool. Excluding them keeps a room turn from pulling back typed docs or raw
// sandbox output.
func TestRealtimeRoomVoiceToolsExcludeHeavyFiscal(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	roomNames := map[string]bool{}
	for _, tool := range app.realtimeRoomVoiceTools() {
		roomNames[asString(tool["name"])] = true
	}
	for _, name := range []string{"fiscal_api_docs", "fiscal_data_query"} {
		if roomNames[name] {
			t.Fatalf("%s must not ride the room voice session (too heavy for a spoken turn)", name)
		}
	}
	for _, name := range []string{"company_financial_snapshot", "financial_comps"} {
		if !roomNames[name] {
			t.Fatalf("%s should ride the room voice session (spoken-ready)", name)
		}
	}
	// The filter removes exactly the two heavy tools, nothing else.
	if got, want := len(app.realtimeRoomVoiceTools()), len(app.kanbanTools())-2; got != want {
		t.Fatalf("room voice tools=%d, want kanbanTools-2=%d", got, want)
	}
}

// fiscalTruncate must never split a multi-byte rune: a cut landing inside a
// UTF-8 sequence would otherwise json.Marshal to U+FFFD. The em dash "—" is
// three bytes, so cutting at any interior byte must back up to its start.
func TestFiscalTruncateIsRuneSafe(t *testing.T) {
	text := strings.Repeat("—", 50) // 150 bytes, 50 runes
	for _, limit := range []int{50, 100, 149, 151} {
		got := fiscalTruncate(text, limit)
		body := got
		if i := strings.Index(got, "\n[truncated"); i >= 0 {
			body = got[:i]
		}
		if !utf8.ValidString(body) {
			t.Fatalf("fiscalTruncate(limit=%d) produced invalid UTF-8", limit)
		}
	}
}

// A model-supplied max_chars above the ceiling is clamped so a large value
// cannot pour the full (up to 4MB) response back into the tool-loop context.
func TestFiscalDataQueryMaxCharsCeiling(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("FISCAL_AI_API_KEY", "test-fiscal-key")
	long := strings.Repeat("y", fiscalDataQueryMaxCharsCeiling+5000)
	fake := &fiscalFakeMCP{replyText: func(tool, code string) string { return long }}
	withFakeFiscalMCP(t, fake.handler(t))

	result, _, err := app.applyToolCallArgs("fiscal_data_query", map[string]any{
		"code":      `async () => { console.log("big"); }`,
		"max_chars": float64(10_000_000),
	})
	if err != nil {
		t.Fatalf("fiscal_data_query dispatch: %v", err)
	}
	output := asString(result["output"])
	// The suffix must report the ceiling, not the requested 10M, and the kept
	// body must be exactly the ceiling length.
	wantSuffix := "[truncated at " + strconv.Itoa(fiscalDataQueryMaxCharsCeiling) + " chars]"
	if !strings.HasSuffix(output, wantSuffix) {
		t.Fatalf("output must be clamped to the ceiling; want suffix %q, got tail %q", wantSuffix, output[len(output)-40:])
	}
	if body := strings.TrimSuffix(output, "\n"+wantSuffix); len(body) != fiscalDataQueryMaxCharsCeiling {
		t.Fatalf("clamped body=%d chars, want the ceiling %d", len(body), fiscalDataQueryMaxCharsCeiling)
	}
}
