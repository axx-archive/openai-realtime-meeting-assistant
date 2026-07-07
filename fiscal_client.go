package main

// fiscal_client.go — fiscal.ai financial-data grounding over the vendor's MCP
// endpoint (streamable HTTP). Two tools exist upstream: api_docs (the full
// typed docs, ~66KB) and execute_code (an async JS arrow function where
// codemode.<fn>() reaches 35 REST functions and console.log is the ONLY
// return channel). Every helper here is ONE execute_code call — the vendor's
// own guidance is to chain calls server-side.
//
// Protocol facts (verified live 2026-07-06):
//   - Auth on /mcp is `Authorization: Bearer`; the session id arrives as the
//     `Mcp-Session-Id` RESPONSE header on initialize and must ride every
//     later request. One JSON-RPC request per POST (batches are rejected).
//   - Responses are SSE (`data: {...}` lines) but are parsed defensively as
//     plain JSON too.
//   - result.isError carries useful text (e.g. bad-ratioId messages) — it is
//     surfaced in the error, unlike raw HTTP failures which go through
//     apiRequestFailedError so upstream bodies never reach the browser.
//   - company_profile REJECTS plain tickers ("Data is not yet available…"),
//     so resolution scans companies_list; daily ratio series ordering is NOT
//     guaranteed, so every series is sorted by date before taking "latest".
//
// User-supplied strings are embedded into the JS via json.Marshal, so quotes
// and backticks cannot break out of the code payload. Keyless degrades to a
// clear FISCAL_AI_API_KEY error before any HTTP, the openai_images posture.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// fiscalMCPURL is a package VAR (the openAIImagesURL precedent): tests swap
// in a fake MCP server to exercise the real handshake encoding.
var fiscalMCPURL = "https://api.fiscal.ai/mcp"

const (
	fiscalMCPProtocolVersion = "2025-03-26"
	// fiscalMaxResponseBytes bounds one MCP response body; the largest
	// legitimate payload (full api_docs) is ~66KB, so 4MB is generous.
	fiscalMaxResponseBytes = 4 << 20
	// Chained execute_code calls fan out to many REST calls server-side;
	// 120s is the same ceiling the slowest OpenAI seam uses.
	fiscalRequestTimeout = 120 * time.Second
)

// currentFiscalAPIKey mirrors currentAnthropicAPIKey: env-only, trimmed,
// never logged or persisted.
func currentFiscalAPIKey() string {
	return strings.TrimSpace(os.Getenv("FISCAL_AI_API_KEY"))
}

func hasFiscalAPIKey() bool {
	return currentFiscalAPIKey() != ""
}

// --- MCP transport -------------------------------------------------------------

type fiscalRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type fiscalRPCResponse struct {
	Result *struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// fiscalMCPPost sends one JSON-RPC request and returns the response headers
// plus the (bounded) body. Non-2xx goes through apiRequestFailedError so the
// upstream body is logged server-side but never surfaced.
func fiscalMCPPost(ctx context.Context, client *http.Client, apiKey, sessionID string, payload fiscalRPCRequest) (http.Header, []byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, fmt.Errorf("encode fiscal.ai MCP request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, fiscalMCPURL, bytes.NewReader(raw))
	if err != nil {
		return nil, nil, fmt.Errorf("create fiscal.ai MCP request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		request.Header.Set("Mcp-Session-Id", sessionID)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, nil, fmt.Errorf("fiscal.ai MCP request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, fiscalMaxResponseBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("read fiscal.ai MCP response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, nil, apiRequestFailedError("fiscal.ai MCP request failed", response.Status, body)
	}
	return response.Header, body, nil
}

// fiscalParseRPCBody extracts the first JSON-RPC message from an SSE stream
// (`data: {...}` lines) or, defensively, from a plain JSON body.
func fiscalParseRPCBody(raw []byte) (fiscalRPCResponse, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return fiscalRPCResponse{}, fmt.Errorf("fiscal.ai MCP response body is empty")
	}
	var candidates [][]byte
	for _, line := range bytes.Split(trimmed, []byte("\n")) {
		if data, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("data:")); ok {
			candidates = append(candidates, bytes.TrimSpace(data))
		}
	}
	if len(candidates) == 0 {
		candidates = append(candidates, trimmed)
	}
	var firstErr error
	for _, candidate := range candidates {
		var rpc fiscalRPCResponse
		if err := json.Unmarshal(candidate, &rpc); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if rpc.Result != nil || rpc.Error != nil {
			return rpc, nil
		}
	}
	if firstErr != nil {
		return fiscalRPCResponse{}, fmt.Errorf("decode fiscal.ai MCP response: %w", firstErr)
	}
	return fiscalRPCResponse{}, fmt.Errorf("fiscal.ai MCP response carried no result")
}

// fiscalToolCall runs the full handshake (initialize → capture session id →
// notifications/initialized → tools/call) with a fresh session per call, and
// returns the joined text content. result.isError becomes an error CARRYING
// that text — vendor execution errors name the fix (e.g. valid ratio ids).
func fiscalToolCall(ctx context.Context, tool string, arguments map[string]any) (string, error) {
	apiKey := currentFiscalAPIKey()
	if apiKey == "" {
		return "", fmt.Errorf("FISCAL_AI_API_KEY is not configured")
	}
	client := &http.Client{Timeout: fiscalRequestTimeout}

	initHeader, initBody, err := fiscalMCPPost(ctx, client, apiKey, "", fiscalRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": fiscalMCPProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "bonfire", "version": "1.0"},
		},
	})
	if err != nil {
		return "", err
	}
	initRPC, err := fiscalParseRPCBody(initBody)
	if err != nil {
		return "", err
	}
	if initRPC.Error != nil {
		return "", fmt.Errorf("fiscal.ai MCP initialize error %d: %s", initRPC.Error.Code, initRPC.Error.Message)
	}
	sessionID := strings.TrimSpace(initHeader.Get("Mcp-Session-Id"))
	if sessionID == "" {
		return "", fmt.Errorf("fiscal.ai MCP initialize returned no session id")
	}

	// The initialized notification has no id and a 202/empty-body response.
	if _, _, err := fiscalMCPPost(ctx, client, apiKey, sessionID, fiscalRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}); err != nil {
		return "", err
	}

	_, callBody, err := fiscalMCPPost(ctx, client, apiKey, sessionID, fiscalRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params:  map[string]any{"name": tool, "arguments": arguments},
	})
	if err != nil {
		return "", err
	}
	rpc, err := fiscalParseRPCBody(callBody)
	if err != nil {
		return "", err
	}
	if rpc.Error != nil {
		return "", fmt.Errorf("fiscal.ai MCP error %d: %s", rpc.Error.Code, rpc.Error.Message)
	}
	if rpc.Result == nil {
		return "", fmt.Errorf("fiscal.ai MCP response carried no result")
	}
	var parts []string
	for _, content := range rpc.Result.Content {
		if content.Type != "" && content.Type != "text" {
			continue
		}
		if text := strings.TrimSpace(content.Text); text != "" {
			parts = append(parts, text)
		}
	}
	text := strings.Join(parts, "\n")
	if rpc.Result.IsError {
		return "", fmt.Errorf("fiscal.ai %s failed: %s", tool, firstNonEmptyString(text, "no error detail"))
	}
	if text == "" {
		return "", fmt.Errorf("fiscal.ai %s returned no text content", tool)
	}
	return text, nil
}

// fiscalExecuteCode runs one JS payload (an `async () => {...}` arrow — the
// vendor contract) through execute_code on a fresh MCP session.
func fiscalExecuteCode(ctx context.Context, code string) (string, error) {
	return fiscalToolCall(ctx, "execute_code", map[string]any{"code": code})
}

// --- api_docs + the compact index ----------------------------------------------

var (
	fiscalDocsMu     sync.Mutex
	fiscalDocsCached string
)

// fiscalAPIDocs fetches the full typed docs once and caches success; a failed
// fetch leaves the cache empty so the next call retries. The lock is held only
// to read/write the cache, never across the network fetch — concurrent callers
// on a cold cache may both fetch (the result is identical and idempotent)
// rather than serialize behind one slow upstream round-trip.
func fiscalAPIDocs(ctx context.Context) (string, error) {
	fiscalDocsMu.Lock()
	cached := fiscalDocsCached
	fiscalDocsMu.Unlock()
	if cached != "" {
		return cached, nil
	}
	docs, err := fiscalToolCall(ctx, "api_docs", map[string]any{})
	if err != nil {
		return "", err
	}
	fiscalDocsMu.Lock()
	fiscalDocsCached = docs
	fiscalDocsMu.Unlock()
	return docs, nil
}

// fiscalDocsFunctionDecl matches a `name: (input:` declaration line inside
// the docs' `declare const codemode: {` block.
var fiscalDocsFunctionDecl = regexp.MustCompile(`^([a-z][a-z0-9_]*): \(input:`)

// fiscalAPIDocsCompact derives a small index from the full docs: every
// codemode function with the first sentence of its doc comment, plus the
// vendor's Usage notes and Data patterns sections. Target: under 6KB, small
// enough to ride a system prompt where the 66KB original cannot.
func fiscalAPIDocsCompact(ctx context.Context) (string, error) {
	full, err := fiscalAPIDocs(ctx)
	if err != nil {
		return "", err
	}
	var entries []string
	pending := ""
	inBlock := false
	for _, line := range strings.Split(full, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "declare const codemode"):
			inBlock = true
		case !inBlock:
		case trimmed == "};":
			inBlock = false
		case strings.HasPrefix(trimmed, "/**"):
			pending = fiscalDocsFirstSentence(trimmed)
		default:
			if match := fiscalDocsFunctionDecl.FindStringSubmatch(trimmed); match != nil {
				entries = append(entries, "- "+match[1]+": "+firstNonEmptyString(pending, "(no description)"))
				pending = ""
			}
		}
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("fiscal.ai docs parse found no codemode functions")
	}
	sections := []string{
		"# fiscal.ai API (compact index)",
		"",
		"Company identifiers use the format `<EXCHANGE>_<TICKER>`, e.g. `NASDAQ_MSFT`.",
		"Call via `codemode.<name>({...})` inside the execute_code tool.",
		"",
		"## Functions",
		strings.Join(entries, "\n"),
	}
	for _, heading := range []string{"## Usage notes", "## Data patterns"} {
		if section := fiscalDocsSection(full, heading); section != "" {
			sections = append(sections, "", section)
		}
	}
	return strings.Join(sections, "\n"), nil
}

// fiscalDocsFirstSentence trims a `/** ... */` opener line to its first
// sentence — the docs put the summary first and elaborate after.
func fiscalDocsFirstSentence(line string) string {
	text := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(line), "/**"), "*/"))
	if cut := strings.Index(text, ". "); cut >= 0 {
		return text[:cut+1]
	}
	return text
}

// fiscalDocsSection returns one `## heading` section up to the next heading.
func fiscalDocsSection(full, heading string) string {
	start := strings.Index(full, heading)
	if start < 0 {
		return ""
	}
	rest := full[start:]
	if end := strings.Index(rest[len(heading):], "\n## "); end >= 0 {
		rest = rest[:len(heading)+end]
	}
	return strings.TrimSpace(rest)
}

// --- Typed helpers (each is ONE chained execute_code call) -----------------------

// fiscalDefaultRatioIDs are the comparison ratios both helpers report by
// default; all three verified live against company_daily_ratios.
var fiscalDefaultRatioIDs = []string{"ratio_ev_to_ebitda", "ratio_ev_to_sales", "ratio_earnings_yield"}

const fiscalDefaultPeerLimit = 6

// fiscalJSONArg marshals a user-supplied value into a JS literal. Quotes,
// backticks, and newlines all arrive JSON-escaped, so an argument can never
// break out of the code payload.
func fiscalJSONArg(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(raw)
}

// fiscalResolveJS resolves a company query to a company_profile. Verified
// live: company_profile REJECTS plain tickers, so anything that is not an
// accepted `EXCHANGE_TICKER` key falls back to scanning companies_list —
// exact key, then exact ticker, then exact name, then name substring, Active
// listings preferred; the scan stops early on an Active key/ticker hit.
const fiscalResolveJS = `  const resolveProfile = async (raw) => {
    const q = String(raw == null ? "" : raw).trim();
    if (!q) { throw new Error("empty company query"); }
    if (/^[A-Za-z0-9.\-]+_[A-Za-z0-9.\-]+$/.test(q)) {
      try { return await codemode.company_profile({ companyKey: q.toUpperCase() }); } catch (e) {}
    }
    const qU = q.toUpperCase();
    const qL = q.toLowerCase();
    let best = null;
    let bestRank = 99;
    for (let page = 1; page <= 20; page++) {
      const res = await codemode.companies_list({ pageNumber: page, pageSize: 1000 });
      for (const c of ((res && res.data) || [])) {
        let rank = 99;
        if (String(c.companyKey || "").toUpperCase() === qU) rank = 0;
        else if (String(c.ticker || "").toUpperCase() === qU) rank = 1;
        else if (String(c.name || "").toLowerCase() === qL) rank = 2;
        else if (qL.length >= 3 && String(c.name || "").toLowerCase().indexOf(qL) !== -1) rank = 3;
        if (rank < 99 && c.tradingStatus !== "Active") rank += 0.5;
        if (rank < bestRank) { bestRank = rank; best = c; }
      }
      if (bestRank <= 1) break;
      if (!res || !res.pagination || !res.pagination.hasNextPage) break;
    }
    if (!best) { throw new Error("fiscal.ai: no company matched " + JSON.stringify(q)); }
    return await codemode.company_profile({ companyKey: best.companyKey });
  };
  const sortByField = (rows, field) => (rows || []).filter((r) => r && r[field]).sort((a, b) => String(a[field]).localeCompare(String(b[field])));
  const latestRatio = async (key, rid) => {
    try {
      const series = await codemode.company_daily_ratios({ companyKey: key, ratioId: rid });
      const pts = sortByField(Array.isArray(series) ? series : [], "date");
      const last = pts[pts.length - 1];
      return last ? { value: last.ratio, date: last.date } : null;
    } catch (e) { return { error: String((e && e.message) || e) }; }
  };
`

// fiscalParseJSONObject decodes the console.log JSON an execute_code helper
// emits, tolerating stray log lines around the object.
func fiscalParseJSONObject(text string) (map[string]any, error) {
	trimmed := strings.TrimSpace(text)
	var out map[string]any
	if err := json.Unmarshal([]byte(trimmed), &out); err == nil {
		return out, nil
	}
	if start, end := strings.Index(trimmed, "{"), strings.LastIndex(trimmed, "}"); start >= 0 && end > start {
		if err := json.Unmarshal([]byte(trimmed[start:end+1]), &out); err == nil {
			return out, nil
		}
	}
	snippet := trimmed
	if len(snippet) > 200 {
		snippet = snippet[:200] + "..."
	}
	return nil, fmt.Errorf("fiscal.ai returned non-JSON output: %s", snippet)
}

// fiscalCompanySnapshot resolves one company (ticker, companyKey, or name)
// and returns its identity (name, companyKey, terminalUrl, sector/industry,
// reporting currency), the latest ANNUAL total revenue + net income with
// their auditUrl source links, and the latest default ratios — every figure
// dated. Daily series and financial rows are sorted by date server-side
// because the API guarantees no ordering.
func fiscalCompanySnapshot(ctx context.Context, company string) (map[string]any, error) {
	if strings.TrimSpace(company) == "" {
		return nil, fmt.Errorf("fiscal.ai snapshot needs a company (ticker, companyKey, or name)")
	}
	code := "async () => {\n" +
		"  const query = " + fiscalJSONArg(company) + ";\n" +
		"  const ratioIds = " + fiscalJSONArg(fiscalDefaultRatioIDs) + ";\n" +
		fiscalResolveJS +
		`  const profile = await resolveProfile(query);
  const key = profile.companyKey;
  const out = {
    name: profile.name || null,
    companyKey: key,
    terminalUrl: profile.terminalUrl || null,
    sector: profile.sector || null,
    industry: profile.industry || null,
    reportingCurrency: profile.reportingCurrency || null
  };
  const figure = (row, ids) => {
    for (const id of ids) {
      const v = row.metricsValues && row.metricsValues[id];
      if (!v || v.value === undefined || v.value === null) continue;
      const links = [];
      for (const arv of (v.asReportedValues || [])) {
        for (const s of (arv.sources || [])) {
          const u = s.auditUrl || s.originalSourceUrl;
          if (u && links.indexOf(u) === -1) links.push(u);
        }
      }
      return { metricId: id, value: v.value, unit: v.unit || null, currency: v.currency || null, reportDate: row.reportDate, fiscalYear: (row.fiscalYear === undefined ? null : row.fiscalYear), auditUrls: links.slice(0, 3) };
    }
    return null;
  };
  try {
    const fin = await codemode.company_financials_standardized({ statementType: "income-statement", companyKey: key, periodType: "annual" });
    const rows = sortByField((fin && fin.data) || [], "reportDate");
    const latest = rows[rows.length - 1];
    if (latest) {
      out.latestAnnual = {
        reportDate: latest.reportDate,
        totalRevenue: figure(latest, ["income_statement_total_revenues"]),
        netIncome: figure(latest, ["income_statement_consolidated_net_income", "income_statement_net_income_attributable_to_common_shareholders", "income_statement_net_income"])
      };
    }
  } catch (e) { out.financialsError = String((e && e.message) || e); }
  out.ratios = {};
  for (const rid of ratioIds) { out.ratios[rid] = await latestRatio(key, rid); }
  console.log(JSON.stringify(out));
}`
	text, err := fiscalExecuteCode(ctx, code)
	if err != nil {
		return nil, err
	}
	return fiscalParseJSONObject(text)
}

// fiscalComps resolves the subject, picks up to peerLimit peers
// (direct_competitor first, then business_model_peer, then the rest), and
// returns one row per company — displayName, relationship, peerReasoning,
// terminalUrl, and the latest value of every requested ratio — shaped so a
// model can trivially emit a markdown table (rows[0] is the subject).
func fiscalComps(ctx context.Context, company string, ratioIDs []string, peerLimit int) (map[string]any, error) {
	if strings.TrimSpace(company) == "" {
		return nil, fmt.Errorf("fiscal.ai comps needs a company (ticker, companyKey, or name)")
	}
	cleaned := make([]string, 0, len(ratioIDs))
	for _, id := range ratioIDs {
		if id = strings.TrimSpace(id); id != "" {
			cleaned = append(cleaned, id)
		}
	}
	if len(cleaned) == 0 {
		cleaned = fiscalDefaultRatioIDs
	}
	if peerLimit <= 0 {
		peerLimit = fiscalDefaultPeerLimit
	}
	if peerLimit > 12 {
		peerLimit = 12
	}
	code := "async () => {\n" +
		"  const query = " + fiscalJSONArg(company) + ";\n" +
		"  const ratioIds = " + fiscalJSONArg(cleaned) + ";\n" +
		"  const peerLimit = " + fiscalJSONArg(peerLimit) + ";\n" +
		fiscalResolveJS +
		`  const subject = await resolveProfile(query);
  const peersRes = await codemode.company_peers({ companyKey: subject.companyKey });
  const all = (peersRes && peersRes.peers) || [];
  const byRelationship = (rel) => all.filter((p) => p && p.peerRelationship === rel);
  const rest = all.filter((p) => p && p.peerRelationship !== "direct_competitor" && p.peerRelationship !== "business_model_peer");
  const chosen = byRelationship("direct_competitor").concat(byRelationship("business_model_peer"), rest).slice(0, peerLimit);
  const buildRow = async (key, displayName, relationship, reasoning, profile) => {
    if (!profile) { try { profile = await codemode.company_profile({ companyKey: key }); } catch (e) { profile = null; } }
    const row = {
      companyKey: key,
      displayName: (profile && profile.name) || displayName || key,
      relationship: relationship,
      terminalUrl: (profile && profile.terminalUrl) || null,
      ratios: {}
    };
    if (reasoning) { row.peerReasoning = reasoning; }
    const values = await Promise.all(ratioIds.map((rid) => latestRatio(key, rid)));
    ratioIds.forEach((rid, i) => { row.ratios[rid] = values[i]; });
    return row;
  };
  const rows = await Promise.all([
    buildRow(subject.companyKey, subject.name, "subject", null, subject)
  ].concat(chosen.map((p) => buildRow(p.companyKey, p.displayNameEnglish, p.peerRelationship, p.peerReasoning, null))));
  console.log(JSON.stringify({
    subject: { companyKey: subject.companyKey, name: subject.name || null, terminalUrl: subject.terminalUrl || null },
    ratioIds: ratioIds,
    rows: rows
  }));
}`
	text, err := fiscalExecuteCode(ctx, code)
	if err != nil {
		return nil, err
	}
	return fiscalParseJSONObject(text)
}
