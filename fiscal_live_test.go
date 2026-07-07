package main

// Live smoke against the real fiscal.ai MCP endpoint. Opt-in twice over:
// FISCAL_AI_API_KEY must be set AND FISCAL_LIVE_SMOKE=1, so CI and keyless
// laptops always skip. Run with:
//
//	FISCAL_LIVE_SMOKE=1 go test -count=1 -run TestFiscalLiveSmoke .

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFiscalLiveSmoke(t *testing.T) {
	if !hasFiscalAPIKey() || os.Getenv("FISCAL_LIVE_SMOKE") != "1" {
		t.Skip("live smoke needs FISCAL_AI_API_KEY and FISCAL_LIVE_SMOKE=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Snapshot from a PLAIN ticker — exercises the companies_list resolution
	// path (company_profile rejects bare tickers, verified live).
	snapshot, err := fiscalCompanySnapshot(ctx, "NFLX")
	if err != nil {
		t.Fatalf("fiscalCompanySnapshot(NFLX): %v", err)
	}
	if key, _ := snapshot["companyKey"].(string); key != "NASDAQ_NFLX" {
		t.Fatalf("companyKey=%v, want NASDAQ_NFLX", snapshot["companyKey"])
	}
	terminalURL, _ := snapshot["terminalUrl"].(string)
	if !strings.HasPrefix(terminalURL, "https://fiscal.ai/") {
		t.Fatalf("terminalUrl=%q, want a fiscal.ai terminal link", terminalURL)
	}
	latest, _ := snapshot["latestAnnual"].(map[string]any)
	if latest == nil {
		t.Fatalf("snapshot carries no latestAnnual block: %v", snapshot)
	}
	revenue, _ := latest["totalRevenue"].(map[string]any)
	if revenue == nil {
		t.Fatalf("latestAnnual carries no totalRevenue: %v", latest)
	}
	if value, _ := revenue["value"].(float64); value <= 0 {
		t.Fatalf("totalRevenue.value=%v, want a positive revenue figure", revenue["value"])
	}
	if date, _ := revenue["reportDate"].(string); date == "" {
		t.Fatalf("totalRevenue carries no reportDate: %v", revenue)
	}

	comps, err := fiscalComps(ctx, "NASDAQ_NFLX", nil, 4)
	if err != nil {
		t.Fatalf("fiscalComps(NASDAQ_NFLX): %v", err)
	}
	rows, _ := comps["rows"].([]any)
	// rows[0] is the subject; at least 2 peers behind it.
	if len(rows) < 3 {
		t.Fatalf("comps returned %d rows, want the subject plus >=2 peers", len(rows))
	}
	for index, entry := range rows {
		row, _ := entry.(map[string]any)
		if row == nil {
			t.Fatalf("row %d is not an object: %v", index, entry)
		}
		if url, _ := row["terminalUrl"].(string); !strings.HasPrefix(url, "https://fiscal.ai/") {
			t.Fatalf("row %d (%v) terminalUrl=%q, want a fiscal.ai terminal link", index, row["companyKey"], url)
		}
	}

	// The compact docs index parses the real 66KB docs and stays prompt-sized.
	compact, err := fiscalAPIDocsCompact(ctx)
	if err != nil {
		t.Fatalf("fiscalAPIDocsCompact: %v", err)
	}
	if len(compact) >= 6144 {
		t.Fatalf("compact docs index is %d bytes, want under 6KB", len(compact))
	}
	for _, want := range []string{"- company_profile:", "- company_daily_ratios:", "## Usage notes", "## Data patterns"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("compact docs index missing %q", want)
		}
	}
}

// TestFiscalLiveDispatch drives the real fiscal.ai API through the agent
// dispatch seam (applyToolCallArgs) — the exact path Scout voice and the Fable
// orchestrator take — proving grounded data reaches a tool result end to end,
// including the per-datapoint auditUrl citations the research prompts require.
func TestFiscalLiveDispatch(t *testing.T) {
	if !hasFiscalAPIKey() || os.Getenv("FISCAL_LIVE_SMOKE") != "1" {
		t.Skip("live dispatch smoke needs FISCAL_AI_API_KEY and FISCAL_LIVE_SMOKE=1")
	}
	app := newIsolatedKanbanBoardApp(t)

	// financial_comps from a plain ticker, through dispatch.
	compsResult, changed, err := app.applyToolCallArgs("financial_comps", map[string]any{"company": "NFLX"})
	if err != nil {
		t.Fatalf("dispatch financial_comps(NFLX): %v", err)
	}
	if changed {
		t.Fatal("financial_comps is read-only but reported a change")
	}
	if ok, _ := compsResult["ok"].(bool); !ok {
		t.Fatalf("financial_comps result=%v, want ok=true", compsResult)
	}
	comps, _ := compsResult["comps"].(map[string]any)
	rows, _ := comps["rows"].([]any)
	if len(rows) < 3 {
		t.Fatalf("financial_comps returned %d rows, want subject plus >=2 peers", len(rows))
	}
	t.Logf("financial_comps(NFLX): %d rows, subject=%v", len(rows), comps["subject"])

	// company_financial_snapshot from an EXCHANGE_TICKER key, through dispatch.
	snapResult, _, err := app.applyToolCallArgs("company_financial_snapshot", map[string]any{"company": "NASDAQ_MSFT"})
	if err != nil {
		t.Fatalf("dispatch company_financial_snapshot(NASDAQ_MSFT): %v", err)
	}
	snapshot, _ := snapResult["snapshot"].(map[string]any)
	if snapshot == nil {
		t.Fatalf("snapshot result=%v, want a snapshot object", snapResult)
	}
	terminalURL, _ := snapshot["terminalUrl"].(string)
	if !strings.HasPrefix(terminalURL, "https://fiscal.ai/") {
		t.Fatalf("snapshot terminalUrl=%q, want a fiscal.ai terminal link", terminalURL)
	}
	// Revenue must carry an auditUrl deep link — the citation the comps gate
	// demands. (Asserted best-effort: log if the vendor omits sources.)
	latest, _ := snapshot["latestAnnual"].(map[string]any)
	revenue, _ := latest["totalRevenue"].(map[string]any)
	auditURLs, _ := revenue["auditUrls"].([]any)
	t.Logf("snapshot(MSFT): terminalUrl=%s revenue=%v auditUrls=%d",
		terminalURL, revenue["value"], len(auditURLs))
	if len(auditURLs) == 0 {
		t.Errorf("snapshot revenue carries no auditUrls — per-datapoint citation missing")
	}
}
