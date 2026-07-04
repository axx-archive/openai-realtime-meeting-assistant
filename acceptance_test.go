package main

// Spectacular OS — acceptance suite (automated half). This is the single Go
// gate the whole-wave demo (docs/plans/spectacular-os-acceptance.md) rests on.
// It is deliberately two-layered:
//
//   1. DIRECT behavioral assertions for the cheap, pure spec-shapes — the three
//      doors resolving to one stable contract, the READINESS parse contract, and
//      the external-write gate's registry source of truth. These run the real
//      code, no fixtures.
//   2. A coverage MANIFEST (TestAcceptanceSuiteCoverage) that thin-wraps the
//      fixture-heavy pillars which already have first-class per-wave tests:
//      asserting their named tests exist rather than duplicating their setup.
//      If a pillar test is deleted or renamed, this acceptance gate goes red —
//      tying each manual acceptance-doc guarantee (A8–A13, C, E) to concrete
//      automated coverage.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAcceptanceThreeDoorsResolveStableContracts backs pillar A8 ("three doors,
// one pipeline"): the voice door, the /goal text door, and the palette door all
// funnel a tool id through the SAME registry (toolByID / normalizeToolTemplate)
// to a stable output contract. We assert the three exemplar tools resolve to a
// non-empty, stable contract and that an unknown id degrades to "" (a plain
// goal, never an error) — the exact invariant TestGoalDoorsResolveToolTemplate
// exercises end-to-end through the goal engine.
func TestAcceptanceThreeDoorsResolveStableContracts(t *testing.T) {
	exemplars := map[string]string{
		"deep_research":       "the research door",
		"one_pager":           "the one-pager door",
		"grill_pressure_test": "the grill door",
	}
	for id, label := range exemplars {
		tool, ok := toolByID(id)
		if !ok {
			t.Fatalf("%s: toolByID(%q) did not resolve — the palette/goal/voice doors share this registry", label, id)
		}
		if strings.TrimSpace(tool.Contract) == "" {
			t.Fatalf("%s: tool %q has no output contract; the staged card + artifact stamp need a stable contract", label, id)
		}
		if got := normalizeToolTemplate(id); got != id {
			t.Fatalf("%s: normalizeToolTemplate(%q)=%q, want the canonical id back", label, id, got)
		}
	}
	// A stray/unknown template must degrade to a plain goal, never error.
	if got := normalizeToolTemplate("not_a_real_tool_ever"); got != "" {
		t.Fatalf("unknown tool template should degrade to \"\", got %q", got)
	}
}

// TestAcceptanceReadinessContractParse backs A5 + pillar A12 (grill exemplar):
// the private-grill scorecard READINESS line must parse to a clamped 0–10 score
// so the binder readiness dial and its trend are trustworthy. Tested directly
// against the real parser.
func TestAcceptanceReadinessContractParse(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		wantVal float64
		wantOK  bool
	}{
		{"canonical line", "Summary here.\nREADINESS: 6.8/10\nNext steps…", 6.8, true},
		{"integer out of ten", "READINESS: 7/10", 7, true},
		{"clamps above ten", "READINESS: 42/10", 10, true},
		{"bare number without /10 is not the contract", "READINESS: 7", 0, false},
		{"no readiness line", "Just a normal artifact with no verdict.", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseReadinessScore(tc.text)
			if ok != tc.wantOK {
				t.Fatalf("parseReadinessScore ok=%v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.wantVal {
				t.Fatalf("parseReadinessScore=%v, want %v", got, tc.wantVal)
			}
		})
	}
}

// TestAcceptanceExternalWriteGateIsRegistryDriven backs pillar A9 ("external-
// write gate — both runners"): the gate's source of truth is the registry flag,
// not the model's say-so. The memo tool is gated; the three read-only exemplars
// are not. The two runner PATHS honoring this flag are covered by the manifest
// (codex + Fable in-process tests) — here we pin the flag itself.
func TestAcceptanceExternalWriteGateIsRegistryDriven(t *testing.T) {
	memo, ok := toolByID("investor_update_memo")
	if !ok {
		t.Fatal("investor_update_memo tool missing — the external-write exemplar")
	}
	if !memo.ExternalWriteGated {
		t.Fatal("investor_update_memo must be ExternalWriteGated: the gate rides the registry, not the model")
	}
	for _, id := range []string{"deep_research", "one_pager", "grill_pressure_test"} {
		tool, ok := toolByID(id)
		if !ok {
			t.Fatalf("exemplar tool %q missing", id)
		}
		if tool.ExternalWriteGated {
			t.Fatalf("read-only exemplar %q must NOT be external-write gated (evals keep these ungated)", id)
		}
	}
}

// TestAcceptanceSuiteCoverage is the thin-wrap manifest. Each row maps an
// acceptance-doc guarantee to the per-wave test(s) that prove it. The test only
// asserts those functions still exist in the suite; deleting/renaming any pillar
// test turns this acceptance gate red. This is the sanctioned "assert they
// exist rather than duplicate" pattern for the fixture-heavy pillars.
func TestAcceptanceSuiteCoverage(t *testing.T) {
	pillars := []struct {
		guarantee string
		tests     []string
	}{
		{
			"A8 three doors, one pipeline",
			[]string{"TestGoalDoorsResolveToolTemplate", "TestPaletteOpensFromBothDoors"},
		},
		{
			"A9 external-write gate — Fable in-process runner",
			[]string{"TestGoalEngineExternalWriteStopsAtApprovalWithoutLaunchingCommit", "TestExternalWriteGatedToolForcesApproval", "TestInitiateGoalCannotYieldExternalWrite"},
		},
		{
			"A9 external-write gate — codex sidecar runner",
			[]string{"TestAgentThreadBlocksExternalWriteBeforeCodexRun", "TestAgentThreadBlocksExternalWriteBeforeLocalCodexExec"},
		},
		{
			"A10 approval round-trip",
			[]string{"TestApprovalRoundTripFiresEventAndNotifiesRequester", "TestResumeApprovedGoalEnqueuesExternalWriteJobAfterApproval"},
		},
		{
			"A11 disclosure stamp is server-authoritative (spoof-proof)",
			[]string{"TestStartChatAsUserStampsDisclosureRegardlessOfArgs"},
		},
		{
			"A7/D1 quarantine deny-list eligibility",
			[]string{"TestSlopCandidateEligibleDenyList"},
		},
		{
			"A5/A12 READINESS parse contract",
			[]string{"TestParseReadinessScore"},
		},
		{
			"A3 unified push channel — two sessions, off-room delivery",
			[]string{"TestUnifiedPushChannelTwoSessionAcceptance"},
		},
		{
			"A13 BonfireOS rename (shell label + unchanged office data-tool key)",
			[]string{"TestIndexBonfireOSRenameAndAgentToken", "TestScoutChatThreadRename"},
		},
		{
			"E1–E4 Deal Room capstone (approve mints token, binder escaped)",
			[]string{"TestDealRoomApproveMintsTokenAndServesEscapedBinder"},
		},
		{
			"C wake-word + Wave 14 polish/recovery markers",
			[]string{"TestIndexWave14PolishMarkers"},
		},
	}

	suite := loadTestSuiteSource(t)
	for _, pillar := range pillars {
		for _, name := range pillar.tests {
			if !strings.Contains(suite, "func "+name+"(") {
				t.Errorf("acceptance pillar %q: missing coverage test %s — the automated gate for this guarantee is gone", pillar.guarantee, name)
			}
		}
	}
}

// loadTestSuiteSource concatenates every *_test.go file in the package so the
// manifest can look up a test declaration regardless of which file holds it.
func loadTestSuiteSource(t *testing.T) string {
	t.Helper()
	matches, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("glob test files: %v", err)
	}
	var b strings.Builder
	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		t.Fatal("no *_test.go files found for the acceptance manifest")
	}
	return b.String()
}
