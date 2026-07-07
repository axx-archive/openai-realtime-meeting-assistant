package main

import (
	"strings"
	"testing"
)

// The four research-grade tool bodies must route public-company financials
// through the fiscal tools, demand per-datapoint filing citations, and carry
// the keyless-honesty clause so agents degrade to labeled recall instead of
// invented sources.
func TestFiscalGroundingSectionInResearchToolBodies(t *testing.T) {
	for _, id := range []string{"deep_research", "comps_precedent", "market_map", "economics_waterfall"} {
		body := toolPromptBody(id)
		for _, want := range []string{
			"DATA SOURCES (fiscal.ai)",
			"financial_comps",
			"company_financial_snapshot",
			"fiscal_api_docs",
			"fiscal_data_query",
			"company_peers",
			"auditUrl",
			"terminalUrl",
			"never invent a source",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s body missing %q", id, want)
			}
		}
	}
}
