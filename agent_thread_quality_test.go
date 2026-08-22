package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
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

func focusedExternalEvidenceThreadForTest() scoutAgentThread {
	return scoutAgentThread{
		Mode:  "research",
		Query: "Verify the one credibility-critical audience benchmark in the approved deck brief.",
		Artifact: meetingMemoryEntry{Metadata: map[string]string{
			"goalDeliverable": "true",
			"goalParentId":    "goal-private-deck",
			"goalSubtaskId":   "external_research",
			"outputContract":  packagingStudioExternalEvidenceContract,
			"originKind":      agentThreadOriginPrivateThread,
			"originId":        "private-aj",
			"originSurface":   "chat:private-aj",
			"visibility":      scoutChatVisibilityPrivate,
			"ownerEmail":      "aj@shareability.com",
			"requestedBy":     "aj@shareability.com",
		}},
	}
}

func focusedExternalEvidenceArtifactForTest() string {
	body := strings.Join([]string{
		"## Research questions",
		"- What current official figure best establishes the reachable creator audience?",
		"## Verified evidence ledger",
		"| Research question | Source fact | Source title | URL | Published / updated | Units | Confidence | Deck implication |",
		"|---|---|---|---|---|---|---|---|",
		"| Reachable creator audience | The official program reports 4,200 opted-in creators. | Official creator program | https://example.org/creator-program | 2026-08-20 | creators | High | Use 4,200 as the sourced ceiling, not as a forecast. |",
		"## Excluded or unverified",
		"- Excluded an unsourced social post claiming 10,000 creators.",
	}, "\n\n")
	return appendOpenAIResponseWebSources(body, openAIResponseWebEvidence{
		ResponseID:  "resp_focused_external_evidence",
		SearchCalls: 1,
		Citations: []openAIResponseWebCitation{
			{Title: "Official creator program", URL: "https://example.org/creator-program"},
		},
	})
}

func TestExternalEvidenceContractAcceptsFocusedProviderBackedLedger(t *testing.T) {
	thread := focusedExternalEvidenceThreadForTest()
	body := focusedExternalEvidenceArtifactForTest()
	if len(strings.Fields(body)) >= minimumResearchArtifactWords {
		t.Fatalf("focused fixture unexpectedly reached the generic %d-word floor", minimumResearchArtifactWords)
	}
	if err := validateAgentThreadTerminalArtifact(thread, body); err != nil {
		t.Fatalf("focused external evidence was rejected: %v", err)
	}

	// A client cannot self-assert the contract to weaken an ordinary research
	// thread; the parent/subtask/deliverable binding is the authority boundary.
	spoofed := thread
	spoofed.Artifact.Metadata = map[string]string{"outputContract": packagingStudioExternalEvidenceContract}
	if err := validateAgentThreadTerminalArtifact(spoofed, body); err == nil || !strings.Contains(err.Error(), "shorter than") {
		t.Fatalf("unbound outputContract bypassed the generic research gate: %v", err)
	}
}

func TestExternalEvidenceContractRejectsUnreceiptedOrUnboundSources(t *testing.T) {
	thread := focusedExternalEvidenceThreadForTest()
	body := focusedExternalEvidenceArtifactForTest()
	if err := validateAgentThreadTerminalArtifact(thread, stripOpenAIWebCitationReceipt(body)); err == nil || !strings.Contains(err.Error(), "provider web-search citation receipt") {
		t.Fatalf("unreceipted evidence passed: %v", err)
	}

	tampered := strings.Replace(body, "https://example.org/creator-program | 2026", "https://unfetched.example/claim | 2026", 1)
	if err := validateAgentThreadTerminalArtifact(thread, tampered); err == nil || !strings.Contains(err.Error(), "absent from the provider citation receipt") {
		t.Fatalf("ledger URL absent from receipt passed: %v", err)
	}
}

func TestProviderWebReceiptRoundTripsExactBareHTTPSURLs(t *testing.T) {
	exactURL := "https://example.org/creator_(program)."
	body := appendOpenAIResponseWebSources("candidate", openAIResponseWebEvidence{
		ResponseID:  "resp_exact_url_round_trip",
		SearchCalls: 1,
		Citations: []openAIResponseWebCitation{
			{Title: "Official creator program", URL: exactURL},
			{Title: "duplicate", URL: exactURL},
			{Title: "padded", URL: " " + exactURL + " "},
			{Title: "insecure", URL: "http://example.org/insecure"},
			{Title: "unsafe", URL: "javascript:alert(1)"},
		},
	})
	if got := strings.Count(body, exactURL); got != 1 {
		t.Fatalf("receipt exact URL occurrences=%d, want one deduplicated row:\n%s", got, body)
	}
	if strings.Contains(body, "http://example.org/insecure") || strings.Contains(body, "javascript:") || strings.Contains(body, "padded") {
		t.Fatalf("receipt admitted a non-bare or non-HTTPS provider source:\n%s", body)
	}
	receipt, err := verifiedResearchCitationReceipt(body)
	if err != nil {
		t.Fatalf("verify exact provider receipt: %v\n%s", err, body)
	}
	if receipt.CitationCount != 1 || receipt.DomainCount != 1 || !receipt.CitationURLs[exactURL] {
		t.Fatalf("receipt=%#v, want one exact provider URL", receipt)
	}

	tampered := strings.Replace(body, exactURL, strings.TrimSuffix(exactURL, "."), 1)
	if _, err := verifiedResearchCitationReceipt(tampered); err == nil {
		t.Fatalf("punctuation-trimmed URL unexpectedly preserved the exact provider digest:\n%s", tampered)
	}
}

func focusedExternalEvidenceJSONForTest() string {
	raw, _ := json.Marshal(externalEvidenceEnvelope{
		ResearchQuestions: []string{"What current official figure best establishes the reachable creator audience?"},
		Evidence: []externalEvidenceEnvelopeRow{{
			ResearchQuestion:   "What current official figure best establishes the reachable creator audience?",
			SourceFact:         "The official program reports 4,200 opted-in creators | all currently active.",
			SourceTitle:        "Official creator program",
			URL:                "https://example.org/creator-program",
			PublishedOrUpdated: "2026-08-20",
			Units:              "creators",
			Confidence:         "High",
			DeckImplication:    "Use 4,200 as the sourced ceiling, not as a forecast.",
		}},
		ExcludedOrUnverified: []string{"An unsourced social post claiming 10,000 creators."},
	})
	return string(raw)
}

func TestExternalEvidenceV2NormalizesStrictJSONIntoEscapedCanonicalMarkdown(t *testing.T) {
	body := appendOpenAIResponseWebSources(focusedExternalEvidenceJSONForTest(), openAIResponseWebEvidence{
		ResponseID:  "resp_external_evidence_v2",
		SearchCalls: 1,
		Citations:   []openAIResponseWebCitation{{Title: "Official creator program", URL: "https://example.org/creator-program"}},
	})
	normalized, err := normalizeExternalEvidenceArtifact(body)
	if err != nil {
		t.Fatalf("normalize external evidence: %v", err)
	}
	if strings.HasPrefix(strings.TrimSpace(normalized), "{") || !strings.Contains(normalized, `creators \| all currently active`) {
		t.Fatalf("normalized artifact was not canonical escaped Markdown:\n%s", normalized)
	}
	rows, err := externalEvidenceLedgerRows(stripOpenAIWebCitationReceipt(normalized))
	if err != nil || len(rows) != 1 || len(rows[0]) != 8 || rows[0][1] != "The official program reports 4,200 opted-in creators | all currently active." {
		t.Fatalf("canonical rows=%#v err=%v", rows, err)
	}
	if err := validateExternalEvidenceArtifact(normalized); err != nil {
		t.Fatalf("canonical normalized artifact failed final gate: %v", err)
	}
}

func TestExternalEvidenceV2RejectsMissingFactsAndUnreceiptedURLs(t *testing.T) {
	receiptBody := appendOpenAIResponseWebSources("candidate", openAIResponseWebEvidence{
		ResponseID: "resp_external_evidence_v2_rejection", SearchCalls: 1,
		Citations: []openAIResponseWebCitation{{URL: "https://example.org/creator-program"}},
	})
	receipt, err := verifiedResearchCitationReceipt(receiptBody)
	if err != nil {
		t.Fatalf("fixture receipt: %v", err)
	}
	envelope, err := decodeExternalEvidenceEnvelope(focusedExternalEvidenceJSONForTest())
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	envelope.Evidence[0].Units = ""
	if err := validateExternalEvidenceEnvelope(envelope, receipt); err == nil || !strings.Contains(err.Error(), "units") || isExternalEvidenceSyntaxFailure(err) {
		t.Fatalf("missing fact error=%v, want semantic rejection", err)
	}
	envelope.Evidence[0].Units = "creators"
	envelope.Evidence[0].URL = "https://unfetched.example/claim"
	if err := validateExternalEvidenceEnvelope(envelope, receipt); err == nil || !strings.Contains(err.Error(), "absent from the provider citation receipt") {
		t.Fatalf("unreceipted URL error=%v", err)
	}
	envelope.Evidence[0].URL = "https://example.org/creator-program"
	row := envelope.Evidence[0]
	for len(envelope.Evidence) < 13 {
		envelope.Evidence = append(envelope.Evidence, row)
	}
	if err := validateExternalEvidenceEnvelope(envelope, receipt); err == nil || !strings.Contains(err.Error(), "1 to 12 decision-useful rows") {
		t.Fatalf("oversized evidence ledger error=%v", err)
	}
}

func TestExternalEvidenceLegacyColumnDriftIsSyntaxClassAndNeverAccepted(t *testing.T) {
	body := focusedExternalEvidenceArtifactForTest()
	seven := strings.Replace(body, " | creators | High |", " | High |", 1)
	if err := validateExternalEvidenceArtifact(seven); err == nil || !isExternalEvidenceSyntaxFailure(err) || !strings.Contains(err.Error(), "7 columns") {
		t.Fatalf("seven-column error=%v, want typed syntax rejection", err)
	} else {
		wrapped := &openAIOutputRejection{reason: "output_validation_error: " + err.Error(), cause: err}
		if got := agentThreadFailureClass(wrapped); got != agentThreadFailureClassExternalEvidenceSyntax {
			t.Fatalf("wrapped syntax failure class=%q", got)
		}
	}
	nine := strings.Replace(body, "Official creator program", "Official creator | program", 1)
	if err := validateExternalEvidenceArtifact(nine); err == nil || !isExternalEvidenceSyntaxFailure(err) || !strings.Contains(err.Error(), "9 columns") {
		t.Fatalf("nine-column error=%v, want typed syntax rejection", err)
	}
}

func TestAgentThreadFailureClassCannotBeAssertedByWorkerMetadata(t *testing.T) {
	metadata := map[string]string{"failureClass": agentThreadFailureClassExternalEvidenceSyntax}
	stampAgentThreadFailureClass(metadata, errors.New("ordinary provider failure"))
	if got := metadata["failureClass"]; got != "" {
		t.Fatalf("ordinary error retained worker-asserted failure class %q", got)
	}
	syntaxErr := &externalEvidenceSyntaxError{err: errors.New("bad evidence envelope")}
	stampAgentThreadFailureClass(metadata, syntaxErr)
	if got := metadata["failureClass"]; got != agentThreadFailureClassExternalEvidenceSyntax {
		t.Fatalf("server-derived syntax class=%q", got)
	}
	stampAgentThreadFailureClass(metadata, nil)
	if got := metadata["failureClass"]; got != "" {
		t.Fatalf("successful result retained failure class %q", got)
	}
}

func TestExternalEvidenceV2RequestUsesBoundedStrictSchemaAndNormalizer(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread := focusedExternalEvidenceThreadForTest()
	request := app.buildAgentThreadOpenAIRequest(thread, app.newAgentJob(thread), time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC))
	if request.JSONSchema == nil || request.JSONSchema.Name != packagingStudioExternalEvidenceContract || request.NormalizeOutput == nil || !request.EnableWebSearch {
		t.Fatalf("external evidence request missing v2 schema/normalizer/search: %#v", request)
	}
	properties, _ := request.JSONSchema.Schema["properties"].(map[string]any)
	evidence, _ := properties["evidence"].(map[string]any)
	if evidence["minItems"] != 1 || evidence["maxItems"] != 12 {
		t.Fatalf("evidence bounds=%#v, want 1..12", evidence)
	}
	if !strings.Contains(request.Instructions, "at most 12 decision-useful evidence items") {
		t.Fatalf("v2 instructions lost synthesis cap:\n%s", request.Instructions)
	}
	restored := durableOpenAIRequest(request).request(thread)
	if restored.JSONSchema == nil || restored.NormalizeOutput == nil {
		t.Fatal("durable request replay lost v2 schema or server normalizer")
	}
}

func TestExternalEvidenceContractInstructionsStayFocusedAndPrivateBound(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread := focusedExternalEvidenceThreadForTest()
	instructions := app.agentThreadInstructionsForThread(thread)
	for _, want := range []string{"focused external-evidence contract", "Verified evidence ledger", "One decisive source", "server appends"} {
		if !strings.Contains(instructions, want) {
			t.Errorf("focused instructions missing %q:\n%s", want, instructions)
		}
	}
	for _, forbidden := range []string{"Comparable Companies", "at least five actually used sources", "1,000-word"} {
		if strings.Contains(instructions, forbidden) {
			t.Errorf("focused instructions inherited generic requirement %q:\n%s", forbidden, instructions)
		}
	}
	if thread.Artifact.Metadata["visibility"] != scoutChatVisibilityPrivate || thread.Artifact.Metadata["ownerEmail"] != "aj@shareability.com" || thread.Artifact.Metadata["originSurface"] != "chat:private-aj" {
		t.Fatalf("contract fixture lost private ACL binding: %+v", thread.Artifact.Metadata)
	}
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
