package main

import (
	"strings"
	"testing"
)

func completeResearchArtifactForTest() string {
	body := strings.Join([]string{
		"# Bonfire comparable-company map and positioning",
		"Search tags: country culture, experiential IP, brand partnerships, creator economy",
		"## Executive Summary\nBonfire is best framed as a country-culture experience network and IP engine. " + strings.Repeat("This sentence provides source-grounded decision context with dated evidence and clear implications. ", 80),
		"## Thesis\nThe category is defined by repeatable cultural IP, participant identity, and native brand integration.",
		"## Comparable Companies\n| Company | Model | 2026 relevance |\n|---|---|---|\n| Red Bull | Owned cultural IP | Benchmark |\n| Overtime | Community-led sports media | Adjacent |\n| Complex | Culture and commerce | Adjacent |",
		"## Evidence\n" + strings.Repeat("The comparison separates sourced fact from inference and records units, dates, and DERIVED implications. ", 35),
		"## Elevator Pitch\nBonfire is the country-culture experience network where original events, media, and communities give fans a place to belong and brands a natural role in the culture.",
		"## Sources\n- Grade A: https://www.redbull.com/us-en/energydrink/company-profile\n- Grade A: https://www.overtime.tv/about\n- Grade A: https://complex.com/about\n- Grade A: https://www.livenationentertainment.com/about/\n- Grade A: https://corporate.wwe.com/company/overview",
		"## Counterarguments\nThe concept can fragment without a clear flagship and repeatable operating model.",
		"## Recommendation\nLead with the network and owned-IP thesis. Change our mind if repeat attendance falls below 30% or fewer than three anchor brands renew.",
		"## Open Questions\n1. Which flagship creates the strongest repeat identity?",
		"## Next Checks\n1. Measure repeat intent and sponsor renewal against the stated thresholds.",
		"## Worker Evidence\nFive cited HTTPS sources across five domains were fetched and used in this run.",
	}, "\n\n")
	return appendOpenAIResponseWebSources(body, openAIResponseWebEvidence{
		ResponseID:  "resp_test_research_evidence",
		SearchCalls: 3,
		Citations: []openAIResponseWebCitation{
			{Title: "Red Bull", URL: "https://www.redbull.com/us-en/energydrink/company-profile"},
			{Title: "Overtime", URL: "https://www.overtime.tv/about"},
			{Title: "Complex", URL: "https://complex.com/about"},
			{Title: "Live Nation", URL: "https://www.livenationentertainment.com/about/"},
			{Title: "WWE", URL: "https://corporate.wwe.com/company/overview"},
		},
	})
}

func TestResearchTerminalQualityGateRejectsTheProductionFailureShape(t *testing.T) {
	thread := scoutAgentThread{Mode: "research", Query: "compare the company and write an elevator pitch"}
	body := "# Vision\n\nThe supplied thread context does not identify the company.\n\n## Executive Summary\nBLOCKED — target company is undefined.\n\n## Next Checks\nTell me the company name."
	err := validateAgentThreadTerminalArtifact(thread, body)
	if err == nil {
		t.Fatal("source-free blocked research was accepted as complete")
	}
	for _, want := range []string{"shorter than", "missing comparable companies", "fewer than five", "blocked plan", "elevator pitch"} {
		if !strings.Contains(strings.ToLower(err.Error()), want) {
			t.Fatalf("quality error missing %q: %v", want, err)
		}
	}
}

func TestResearchTerminalQualityGateRejectsFabricatedMarkdownSourcesWithoutProviderReceipt(t *testing.T) {
	thread := scoutAgentThread{Mode: "research", Query: "compare Bonfire and write an elevator pitch"}
	body := stripOpenAIWebCitationReceipt(completeResearchArtifactForTest())
	if err := validateAgentThreadTerminalArtifact(thread, body); err == nil || !strings.Contains(err.Error(), "provider web-search citation receipt") {
		t.Fatalf("fabricated Markdown sources were accepted: %v", err)
	}
}

func TestResearchTerminalQualityGateRejectsTamperedProviderReceipt(t *testing.T) {
	thread := scoutAgentThread{Mode: "research", Query: "compare Bonfire and write an elevator pitch"}
	body := completeResearchArtifactForTest()
	marker := strings.LastIndex(body, "https://www.overtime.tv/about")
	if marker < 0 {
		t.Fatal("test receipt source missing")
	}
	body = body[:marker] + "https://invented.example/about" + body[marker+len("https://www.overtime.tv/about"):]
	if err := validateAgentThreadTerminalArtifact(thread, body); err == nil || !strings.Contains(err.Error(), "receipt is invalid") {
		t.Fatalf("tampered provider receipt was accepted: %v", err)
	}
}

func TestResearchTerminalQualityGateAcceptsEvidenceGradeReport(t *testing.T) {
	thread := scoutAgentThread{Mode: "research", Query: "compare Bonfire and write an elevator pitch", Artifact: meetingMemoryEntry{Metadata: map[string]string{"sourceWindowDigest": strings.Repeat("a", 64)}}}
	body := completeResearchArtifactForTest()
	if err := validateAgentThreadTerminalArtifact(thread, body); err != nil {
		t.Fatalf("evidence-grade research rejected: %v", err)
	}
	metadata := researchArtifactEvidenceMetadata(thread, body)
	if metadata["researchQualityGate"] != "passed" || metadata["researchCitationCount"] != "5" || metadata["researchSourceDomainCount"] != "5" || metadata["researchSourceWindowDigest"] != strings.Repeat("a", 64) {
		t.Fatalf("research evidence metadata=%v", metadata)
	}
}
