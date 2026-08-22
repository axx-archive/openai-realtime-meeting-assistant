package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func largeExternalEvidenceFixtureForTest(t *testing.T) (externalEvidenceEnvelope, openAIResponseWebEvidence, []string) {
	t.Helper()
	questions := []string{
		"What official market count establishes the reachable audience?",
		"What primary-source behavior signal establishes participation quality?",
		"What official spend measure establishes commercial relevance?",
		"What primary-source operating constraint most affects feasibility?",
		"What official trend establishes why the decision matters now?",
	}
	providerURLs := make([]string, 166)
	citations := make([]openAIResponseWebCitation, 166)
	for index := range providerURLs {
		providerURLs[index] = fmt.Sprintf("https://source-%03d.example.test/evidence/%03d", index, index)
		citations[index] = openAIResponseWebCitation{Title: fmt.Sprintf("Primary source %03d", index), URL: providerURLs[index]}
	}
	envelope := externalEvidenceEnvelope{ResearchQuestions: questions, ExcludedOrUnverified: []string{"An unsupported market estimate was excluded."}}
	for index := 0; index < 12; index++ {
		sourceIndex := index
		if index == 11 {
			sourceIndex = len(providerURLs) - 1 // prove the complete provider set, including its last source, is admissible
		}
		envelope.Evidence = append(envelope.Evidence, externalEvidenceEnvelopeRow{
			ResearchQuestion:   questions[index%len(questions)],
			SourceFact:         fmt.Sprintf("Primary source %03d reports the decision-critical value %d.", sourceIndex, 1000+sourceIndex),
			SourceTitle:        fmt.Sprintf("Primary source %03d", sourceIndex),
			URL:                providerURLs[sourceIndex],
			PublishedOrUpdated: "Accessed 2026-08-21",
			Units:              "participants",
			Confidence:         "High",
			DeckImplication:    fmt.Sprintf("Use verified value %d only for the matching decision claim.", 1000+sourceIndex),
		})
	}
	return envelope, openAIResponseWebEvidence{ResponseID: "resp_large_provider_set", SearchCalls: 13, Citations: citations}, providerURLs
}

func externalEvidenceJSONForTest(t *testing.T, envelope externalEvidenceEnvelope) string {
	t.Helper()
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal external evidence fixture: %v", err)
	}
	return string(raw)
}

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
		"| Reachable creator audience | The official program has 4,200 opted-in creators. | Official creator program | https://example.org/creator-program | Accessed 2026-08-20 | creators | High | Use 4,200 as the sourced ceiling, not as a forecast. |",
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

type focusedEntailmentFixture struct {
	app                *kanbanBoardApp
	thread             scoutAgentThread
	researchThread     scoutAgentThread
	authorizedQuestion string
	candidateID        string
	windowDigest       string
	sourceURL          string
}

func focusedEntailmentThreadForTest(t *testing.T, candidateFact, sourceURL, fetchedText string) focusedEntailmentFixture {
	t.Helper()
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "entailment-authority-test-key"
	previousStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousStart })
	parent, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Build a private evidence-bound presentation about whether the official program's 2026 opted-in creator count supports proceeding", CreatedBy: "aj@shareability.com", ToolTemplate: packagingStudioProcessID,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := mustGoalPlan(t, app, parent.Artifact.ID)
	engine := newGoalEngine(app)
	if err := engine.prepareGoalRoute(&plan, parent.Artifact.ID); err != nil {
		t.Fatal(err)
	}
	if err := instantiateProcessPlan(packagingStudioDefinition(), &plan); err != nil {
		t.Fatal(err)
	}
	plan.State = goalStateExecute
	const authorizedQuestion = "How many opted-in creators does the official program have in 2026?"
	contextBody := `{"direct_ask":"Decide whether the official program's 2026 opted-in creator count supports proceeding","audience":"decision makers","decision":"whether the program size supports proceeding","desired_response":"make a grounded decision","slide_count":8,"context_used":[],"settled_decisions":[],"taste_signals":[],"brand_assets":[],"research_mode":"external","research_questions":["` + authorizedQuestion + `"],"known_facts":[],"uncertain_claims":[],"reversible_inferences":[]}`
	contextArtifact, _, err := app.createOSArtifactWithMetadata("workflow", "Context snapshot", contextBody, scoutParticipantName, map[string]string{
		"goalParentId": parent.Artifact.ID, "goalSubtaskId": "context_snapshot", "outputContract": "deck_context_snapshot_v2",
		"processId": packagingStudioProcessID, "processStage": "context_snapshot", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	contextStage := plan.subtaskByID("context_snapshot")
	contextStage.Status, contextStage.ArtifactID = subtaskComplete, contextArtifact.ID

	row := externalEvidenceEnvelopeRow{
		ResearchQuestion: authorizedQuestion, SourceFact: candidateFact,
		SourceTitle: "Official creator program", URL: sourceURL, PublishedOrUpdated: "Accessed 2026-08-21",
		Units: "creators", Confidence: "Medium", DeckImplication: "Use only after entailment checking.",
	}
	rawEvidence := externalEvidenceJSONForTest(t, externalEvidenceEnvelope{
		ResearchQuestions: []string{row.ResearchQuestion}, Evidence: []externalEvidenceEnvelopeRow{row}, ExcludedOrUnverified: []string{},
	})
	normalizedEvidence, err := normalizeExternalEvidenceArtifact(appendOpenAIResponseWebSources(rawEvidence, openAIResponseWebEvidence{
		ResponseID: "resp_entailment_fixture", SearchCalls: 1, Citations: []openAIResponseWebCitation{{Title: "Official creator program", URL: sourceURL}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	researchThreadID := "research-fixture-" + sha256Hex([]byte(t.Name()))[:20]
	researchMetadata := map[string]string{
		"goalDeliverable": "true", "originKind": agentThreadOriginPrivateThread, "originId": plan.RouteReceipt.OriginID,
		"originSurface": "chat:" + plan.RouteReceipt.OriginID, "visibility": scoutChatVisibilityPrivate,
		"ownerEmail": "aj@shareability.com", "requestedBy": "aj@shareability.com",
		"goalParentId": parent.Artifact.ID, "goalSubtaskId": "external_research", "outputContract": packagingStudioExternalEvidenceContract,
		"processId": packagingStudioProcessID, "processStage": "external_research", "status": "complete", "threadStatus": "complete",
		"researchAcceptedContentDigest": sha256Hex([]byte(normalizedEvidence)), "researchAcceptedArtifactVersion": "1",
		"threadId": researchThreadID, "threadQuery": "Research only the authorized question: " + authorizedQuestion, "mode": "research",
	}
	for key, value := range researchArtifactEvidenceMetadata(scoutAgentThread{Mode: "research"}, normalizedEvidence) {
		researchMetadata[key] = value
	}
	research, _, err := app.createOSArtifactWithMetadata("research", "External research", normalizedEvidence, scoutParticipantName, researchMetadata)
	if err != nil {
		t.Fatal(err)
	}
	researchStage := plan.subtaskByID("external_research")
	researchStage.Status, researchStage.ArtifactID, researchStage.ThreadID = subtaskComplete, research.ID, researchThreadID

	if strings.TrimSpace(fetchedText) == "" {
		fetchedText = candidateFact
	}
	fetch := func(_ context.Context, rawURL string) (externalSourceDocument, error) {
		if fetchedText == "__PDF_EXTRACTION_REQUIRED__" {
			return externalSourceDocument{}, fmt.Errorf("%w; reroute=%s", errExternalSourcePDFRequiresExtraction, externalSourcePDFRerouteContract)
		}
		return externalSourceDocument{
			RequestedURL: rawURL, FinalURL: rawURL, RedirectChain: []string{rawURL}, ContentType: "text/html", FetchedAt: "2026-08-21T12:00:00Z",
			ContentDigest: sha256Hex([]byte(fetchedText)), Blocks: []externalSourceTextBlock{{Anchor: "Official creator program", Text: fetchedText}},
		}, nil
	}
	sourceBody, sourceMetadata, err := compileExternalEvidenceSourceSnapshotsWithFetcher(app, &plan, parent.Artifact.ID, fetch)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"goalParentId": parent.Artifact.ID, "goalSubtaskId": "source_snapshot", "processId": packagingStudioProcessID,
		"processStage": "source_snapshot", "status": "complete", "threadStatus": "complete",
	} {
		sourceMetadata[key] = value
	}
	source, _, err := app.createOSArtifactWithMetadata("workflow", "Source snapshot", sourceBody, scoutParticipantName, sourceMetadata)
	if err != nil {
		t.Fatal(err)
	}
	sourceStage := plan.subtaskByID("source_snapshot")
	sourceStage.Status, sourceStage.ArtifactID = subtaskComplete, source.ID

	envelope, _, err := externalSourceSnapshotEnvelopeFromText(sourceBody)
	if err != nil || len(envelope.Snapshots) != 1 {
		t.Fatalf("source snapshot fixture: envelope=%+v err=%v", envelope, err)
	}
	windowDigest := "N/A"
	if len(envelope.Snapshots[0].Windows) > 0 {
		windowDigest = envelope.Snapshots[0].Windows[0].Digest
	}
	threadID := "entailment-fixture-" + sha256Hex([]byte(t.Name()))[:20]
	query := "Check the exact authority-bound source windows.\n\n" + sourceBody
	writerMetadata := map[string]string{
		"goalDeliverable": "true", "goalParentId": parent.Artifact.ID, "goalSubtaskId": "evidence_entailment",
		"outputContract": packagingStudioEntailmentContract, "originKind": agentThreadOriginPrivateThread,
		"originId": plan.RouteReceipt.OriginID, "originSurface": "chat:" + plan.RouteReceipt.OriginID, "visibility": scoutChatVisibilityPrivate,
		"ownerEmail": "aj@shareability.com", "requestedBy": "aj@shareability.com", "threadId": threadID, "threadQuery": query,
		"mode": "artifacts", "status": "running", "threadStatus": "running",
	}
	writer, _, err := app.createOSArtifactWithMetadata("artifacts", "Evidence entailment", "Checking source windows.", scoutParticipantName, writerMetadata)
	if err != nil {
		t.Fatal(err)
	}
	writerStage := plan.subtaskByID("evidence_entailment")
	writerStage.Status, writerStage.ArtifactID, writerStage.ThreadID = subtaskRunning, writer.ID, threadID
	encodedPlan, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	parent.Artifact, _, err = app.updateOSArtifactWithMetadata(parent.Artifact.ID, "", parent.Artifact.Text, scoutParticipantName, map[string]string{"goalPlan": string(encodedPlan)})
	if err != nil {
		t.Fatal(err)
	}
	writer, _ = app.osArtifactByID(writer.ID)
	return focusedEntailmentFixture{
		app: app, thread: scoutAgentThread{ID: threadID, Mode: "artifacts", Query: query, Status: "running", Artifact: writer},
		researchThread:     scoutAgentThread{ID: researchThreadID, Mode: "research", Query: researchMetadata["threadQuery"], Status: "complete", Artifact: research},
		authorizedQuestion: authorizedQuestion,
		candidateID:        externalEvidenceCandidateID(row), windowDigest: windowDigest, sourceURL: sourceURL,
	}
}

func externalEvidenceEntailmentBodyForTest(t *testing.T, check externalEvidenceEntailmentCheck, citations []openAIResponseWebCitation) string {
	t.Helper()
	if strings.TrimSpace(check.RelevanceVerdict) == "" {
		check.RelevanceVerdict = "relevant"
	}
	if strings.TrimSpace(check.SourceQuality) == "" {
		check.SourceQuality = "decision_grade"
	}
	raw, err := json.Marshal(externalEvidenceEntailmentEnvelope{Checks: []externalEvidenceEntailmentCheck{check}})
	if err != nil {
		t.Fatal(err)
	}
	_ = citations // Entailment consumes only server-fetched windows; it never starts a second hosted search.
	return string(raw)
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

	tampered := strings.Replace(body, "https://example.org/creator-program | Accessed 2026", "https://unfetched.example/claim | Accessed 2026", 1)
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
	reachQuestion := "What current official figure best establishes the reachable creator audience?"
	qualityQuestion := "What official measure best distinguishes opted-in engagement from raw reach?"
	raw, _ := json.Marshal(externalEvidenceEnvelope{
		ResearchQuestions: []string{reachQuestion, qualityQuestion},
		Evidence: []externalEvidenceEnvelopeRow{
			{
				ResearchQuestion:   reachQuestion,
				SourceFact:         "The official program reports 4,200 opted-in creators | all currently active.",
				SourceTitle:        "Official creator program",
				URL:                "https://example.org/creator-program",
				PublishedOrUpdated: "Accessed 2026-08-20",
				Units:              "creators",
				Confidence:         "High",
				DeckImplication:    "Use 4,200 as the sourced ceiling, not as a forecast.",
			},
			{
				ResearchQuestion:   qualityQuestion,
				SourceFact:         "The official methodology counts only creators who completed an opt-in action.",
				SourceTitle:        "Official engagement methodology",
				URL:                "https://example.org/engagement-methodology",
				PublishedOrUpdated: "Accessed 2026-08-18",
				Units:              "opt-in actions",
				Confidence:         "High",
				DeckImplication:    "Frame the network as activated participation rather than undifferentiated reach.",
			},
		},
		ExcludedOrUnverified: []string{"An unsourced social post claiming 10,000 creators."},
	})
	return string(raw)
}

func TestExternalEvidenceV2NormalizesStrictJSONIntoEscapedCanonicalMarkdown(t *testing.T) {
	body := appendOpenAIResponseWebSources(focusedExternalEvidenceJSONForTest(), openAIResponseWebEvidence{
		ResponseID:  "resp_external_evidence_v2",
		SearchCalls: 1,
		Citations: []openAIResponseWebCitation{
			{Title: "Official creator program", URL: "https://example.org/creator-program"},
			{Title: "Official engagement methodology", URL: "https://example.org/engagement-methodology"},
		},
	})
	normalized, err := normalizeExternalEvidenceArtifact(body)
	if err != nil {
		t.Fatalf("normalize external evidence: %v", err)
	}
	if strings.HasPrefix(strings.TrimSpace(normalized), "{") || !strings.Contains(normalized, `creators \| all currently active`) {
		t.Fatalf("normalized artifact was not canonical escaped Markdown:\n%s", normalized)
	}
	rows, err := externalEvidenceLedgerRows(stripOpenAIWebCitationReceipt(normalized))
	if err != nil || len(rows) != 2 || len(rows[0]) != 8 || rows[0][1] != "The official program reports 4,200 opted-in creators | all currently active." {
		t.Fatalf("canonical rows=%#v err=%v", rows, err)
	}
	if err := validateExternalEvidenceArtifact(normalized); err != nil {
		t.Fatalf("canonical normalized artifact failed final gate: %v", err)
	}
}

func TestExternalEvidenceV2CompactsLargeProviderSetAfterFullValidation(t *testing.T) {
	envelope, providerEvidence, providerURLs := largeExternalEvidenceFixtureForTest(t)
	fullBody := appendOpenAIResponseWebSources(externalEvidenceJSONForTest(t, envelope), providerEvidence)
	fullReceipt, err := verifiedResearchCitationReceipt(fullBody)
	if err != nil {
		t.Fatalf("verify full provider receipt: %v", err)
	}
	if fullReceipt.CitationCount != 166 || fullReceipt.DomainCount != 166 || fullReceipt.SearchCalls != 13 || !fullReceipt.CitationURLs[providerURLs[165]] {
		t.Fatalf("full provider receipt=%#v, want all 166 sources across 13 calls including the last source", fullReceipt)
	}

	normalized, err := normalizeExternalEvidenceArtifact(fullBody)
	if err != nil {
		t.Fatalf("normalize large provider set: %v", err)
	}
	compact, err := verifiedResearchCitationReceipt(normalized)
	if err != nil {
		t.Fatalf("verify compact receipt: %v\n%s", err, normalized)
	}
	if compact.CitationCount != 12 || compact.DomainCount != 12 || compact.SearchCalls != 13 || compact.ProviderCitationCount != 166 || compact.ProviderDomainCount != 166 {
		t.Fatalf("compact receipt=%#v, want 12 used / 166 provider / 13 calls", compact)
	}
	if compact.ProviderCitationDigest != sha256Hex([]byte(strings.Join(providerURLs, "\n"))) {
		t.Fatalf("provider digest=%q, want full 166-source digest", compact.ProviderCitationDigest)
	}
	if !compact.CitationURLs[providerURLs[165]] {
		t.Fatalf("last provider source was not admitted into compact used set: %#v", compact.CitationURLs)
	}
	if strings.Contains(normalized, providerURLs[164]) {
		t.Fatalf("unused provider URL leaked into durable human-readable receipt: %s", providerURLs[164])
	}
	if !strings.Contains(normalized, "provider_count=166 provider_domains=166 provider_digest="+compact.ProviderCitationDigest) {
		t.Fatalf("compact marker lost full provider audit metadata:\n%s", normalized)
	}
	metadata := researchArtifactEvidenceMetadata(focusedExternalEvidenceThreadForTest(), normalized)
	if metadata["researchCitationCount"] != "12" || metadata["researchProviderSourceCount"] != "166" || metadata["researchProviderSourceDomainCount"] != "166" || metadata["researchProviderSourceDigest"] != compact.ProviderCitationDigest || metadata["researchWebSearchCallCount"] != "13" {
		t.Fatalf("research metadata lost used/provider distinction: %#v", metadata)
	}
}

func TestExternalEvidenceV2CompactReceiptUsesProviderTitlesAndPreservesUTF8(t *testing.T) {
	envelope, err := decodeExternalEvidenceEnvelope(focusedExternalEvidenceJSONForTest())
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	envelope.Evidence[0].SourceTitle = "Model-authored spoofed title"
	providerTitle := strings.Repeat("牧", 200)
	body := appendOpenAIResponseWebSources(externalEvidenceJSONForTest(t, envelope), openAIResponseWebEvidence{
		ResponseID: "resp_provider_titles_utf8", SearchCalls: 1,
		Citations: []openAIResponseWebCitation{
			{Title: providerTitle, URL: envelope.Evidence[0].URL},
			{Title: "Provider engagement methodology", URL: envelope.Evidence[1].URL},
		},
	})
	normalized, err := normalizeExternalEvidenceArtifact(body)
	if err != nil {
		t.Fatalf("normalize provider-titled evidence: %v", err)
	}
	if !utf8.ValidString(normalized) {
		t.Fatal("normalized receipt split a multibyte provider title")
	}
	receipt, err := verifiedResearchCitationReceipt(normalized)
	if err != nil {
		t.Fatalf("verify compact provider-titled receipt: %v", err)
	}
	wantTitle := truncateAgentThreadText(providerTitle, 180)
	if got := receipt.CitationTitles[envelope.Evidence[0].URL]; got != wantTitle {
		t.Fatalf("provider title=%q, want exact rune-safe provider title %q", got, wantTitle)
	}
	receiptSection := normalized[strings.LastIndex(normalized, "## Scout source receipt"):]
	if strings.Contains(receiptSection, "Model-authored spoofed title") || !strings.Contains(receiptSection, wantTitle) {
		t.Fatalf("compact receipt did not preserve provider title authority:\n%s", receiptSection)
	}
	if strings.Contains(normalized, "Model-authored spoofed title") || !strings.Contains(normalized, "## Provider-fetched evidence ledger") || !strings.Contains(normalized, wantTitle) {
		t.Fatalf("visible evidence ledger did not replace the model title with provider authority:\n%s", normalized)
	}
}

func TestExternalEvidenceV2CompactReceiptDeduplicatesUsedURLs(t *testing.T) {
	envelope, providerEvidence, _ := largeExternalEvidenceFixtureForTest(t)
	envelope.Evidence[5].URL = envelope.Evidence[0].URL
	envelope.Evidence[5].SourceTitle = envelope.Evidence[0].SourceTitle
	normalized, err := normalizeExternalEvidenceArtifact(appendOpenAIResponseWebSources(externalEvidenceJSONForTest(t, envelope), providerEvidence))
	if err != nil {
		t.Fatalf("normalize duplicated used source: %v", err)
	}
	receipt, err := verifiedResearchCitationReceipt(normalized)
	if err != nil {
		t.Fatalf("verify deduplicated receipt: %v", err)
	}
	if receipt.CitationCount != 11 || len(receipt.CitationURLs) != 11 {
		t.Fatalf("deduplicated receipt=%#v, want 11 exact distinct URLs", receipt)
	}
	if got := strings.Count(normalized, envelope.Evidence[0].URL); got != 3 {
		t.Fatalf("duplicate used URL occurrences=%d, want two ledger rows plus one receipt row", got)
	}
}

func TestExternalEvidenceV2FailsClosedForUnsafeOutOfSetAndNoRows(t *testing.T) {
	base, providerEvidence, _ := largeExternalEvidenceFixtureForTest(t)
	tests := []struct {
		name string
		edit func(*externalEvidenceEnvelope)
		want string
	}{
		{name: "unsafe URL", edit: func(envelope *externalEvidenceEnvelope) { envelope.Evidence[0].URL = "javascript:alert(1)" }, want: "valid bare HTTPS URL"},
		{name: "out of provider set", edit: func(envelope *externalEvidenceEnvelope) {
			envelope.Evidence[0].URL = "https://outside.example.test/unfetched"
		}, want: "absent from the provider citation receipt"},
		{name: "no valid rows and no explanation", edit: func(envelope *externalEvidenceEnvelope) {
			envelope.Evidence = nil
			envelope.ExcludedOrUnverified = nil
		}, want: "empty evidence requires"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := base
			envelope.Evidence = append([]externalEvidenceEnvelopeRow(nil), base.Evidence...)
			test.edit(&envelope)
			_, err := normalizeExternalEvidenceArtifact(appendOpenAIResponseWebSources(externalEvidenceJSONForTest(t, envelope), providerEvidence))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want fail-closed reason containing %q", err, test.want)
			}
		})
	}
}

func TestExternalEvidenceV2RejectsMissingFactsAndUnreceiptedURLs(t *testing.T) {
	receiptBody := appendOpenAIResponseWebSources("candidate", openAIResponseWebEvidence{
		ResponseID: "resp_external_evidence_v2_rejection", SearchCalls: 1,
		Citations: []openAIResponseWebCitation{{URL: "https://example.org/creator-program"}, {URL: "https://example.org/engagement-methodology"}},
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
	if err := validateExternalEvidenceEnvelope(envelope, receipt); err == nil || !strings.Contains(err.Error(), "at most 12 decision-useful rows") {
		t.Fatalf("oversized evidence ledger error=%v", err)
	}
}

func TestExternalEvidenceV2AcceptsOneDecisiveResearchQuestion(t *testing.T) {
	receiptBody := appendOpenAIResponseWebSources("candidate", openAIResponseWebEvidence{
		ResponseID: "resp_external_evidence_v2_proportional", SearchCalls: 1,
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
	envelope.ResearchQuestions = envelope.ResearchQuestions[:1]
	envelope.Evidence = envelope.Evidence[:1]
	if err := validateExternalEvidenceEnvelope(envelope, receipt); err != nil {
		t.Fatalf("one decisive question and one exact primary source should pass: %v", err)
	}
}

func authorizedExternalEvidenceTestContext(t *testing.T) (*kanbanBoardApp, goalPlan, string) {
	t.Helper()
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "external-evidence-context-test-key"
	previousStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousStart })
	parent, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Build a private presentation about whether the official program's 2026 opted-in creator count supports proceeding",
		CreatedBy: "aj@shareability.com", ToolTemplate: packagingStudioProcessID,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := mustGoalPlan(t, app, parent.Artifact.ID)
	engine := newGoalEngine(app)
	if err := engine.prepareGoalRoute(&plan, parent.Artifact.ID); err != nil {
		t.Fatal(err)
	}
	if err := instantiateProcessPlan(packagingStudioDefinition(), &plan); err != nil {
		t.Fatal(err)
	}
	const question = "How many opted-in creators does the official program have in 2026?"
	contextBody := `{"direct_ask":"Decide whether the official program's 2026 opted-in creator count supports proceeding","audience":"decision makers","decision":"whether the program size supports proceeding","desired_response":"make a grounded decision","slide_count":6,"context_used":[],"settled_decisions":[],"taste_signals":[],"brand_assets":[],"research_mode":"external","research_questions":["` + question + `"],"known_facts":[],"uncertain_claims":[],"reversible_inferences":[]}`
	contextArtifact, _, err := app.createOSArtifactWithMetadata("workflow", "Context snapshot", contextBody, scoutParticipantName, map[string]string{
		"goalParentId": parent.Artifact.ID, "goalSubtaskId": "context_snapshot", "outputContract": "deck_context_snapshot_v2",
		"processId": packagingStudioProcessID, "processStage": "context_snapshot", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	contextStage := plan.subtaskByID("context_snapshot")
	contextStage.Status, contextStage.ArtifactID = subtaskComplete, contextArtifact.ID
	encodedPlan, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.updateOSArtifactWithMetadata(parent.Artifact.ID, "", parent.Artifact.Text, scoutParticipantName, map[string]string{"goalPlan": string(encodedPlan)}); err != nil {
		t.Fatal(err)
	}
	return app, plan, parent.Artifact.ID
}

func authorizedExternalEvidenceResearchThreadForTest(t *testing.T, app *kanbanBoardApp, plan goalPlan, parentID, threadID string) scoutAgentThread {
	t.Helper()
	writer := plan.subtaskByID("external_research")
	researchMetadata := map[string]string{
		"goalParentId": parentID, "goalSubtaskId": "external_research", "outputContract": packagingStudioExternalEvidenceContract,
		"processId": packagingStudioProcessID, "processStage": "external_research", "status": "running", "threadStatus": "running",
		"threadId": threadID, "threadQuery": "Research the authorized context snapshot.", "mode": "research",
		"assignedRunner": writer.Runner, "authority": goalChildAuthority(writer.Authority, plan.Authority),
		"goalDeliverable": "true", "goalChildActivationState": goalChildActivationStarted,
		publicConversationWorkActivationState: publicConversationWorkStarted,
		publicConversationWorkActivationOwner: "external-evidence-context-test-worker",
	}
	for key, value := range goalRouteChildBindingMetadata(&plan) {
		researchMetadata[key] = value
	}
	researchArtifact, _, err := app.createOSArtifactWithMetadata("research", "External research", "Research pending.", scoutParticipantName, researchMetadata)
	if err != nil {
		t.Fatal(err)
	}
	writer.Status, writer.ArtifactID, writer.ThreadID = subtaskRunning, researchArtifact.ID, threadID
	encodedPlan, _ := json.Marshal(plan)
	parentArtifact, _ := app.osArtifactByID(parentID)
	if _, _, err := app.updateOSArtifactWithMetadata(parentID, "", parentArtifact.Text, scoutParticipantName, map[string]string{"goalPlan": string(encodedPlan)}); err != nil {
		t.Fatal(err)
	}
	return scoutAgentThread{ID: threadID, Mode: "research", Query: "Research the authorized context snapshot.", Status: "running", Artifact: researchArtifact}
}

func TestAuthorizedExternalEvidenceQuestionsAcceptsProductionObjectShape(t *testing.T) {
	app, plan, parentID := authorizedExternalEvidenceTestContext(t)
	contextStage := plan.subtaskByID("context_snapshot")
	if contextStage == nil {
		t.Fatal("context snapshot stage missing")
	}
	artifact, ok := app.osArtifactByID(contextStage.ArtifactID)
	if !ok {
		t.Fatal("context snapshot artifact missing")
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(artifact.Text), &object); err != nil {
		t.Fatalf("decode context snapshot: %v", err)
	}
	question := object["research_questions"].([]any)[0].(string)
	object["research_questions"] = []any{map[string]any{
		"question":           question,
		"decision_relevance": "This fact could change the recommendation.",
	}}
	body, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("encode production-shaped context snapshot: %v", err)
	}
	if _, _, err := app.updateOSArtifactWithMetadata(artifact.ID, "", string(body), scoutParticipantName, artifact.Metadata); err != nil {
		t.Fatalf("update production-shaped context snapshot: %v", err)
	}

	questions, err := authorizedExternalEvidenceResearchQuestions(app, &plan, parentID)
	if err != nil {
		t.Fatalf("production-shaped research question should remain authorized: %v", err)
	}
	if len(questions) != 1 || questions[0] != question {
		t.Fatalf("questions=%v, want exact production question %q", questions, question)
	}
	thread := authorizedExternalEvidenceResearchThreadForTest(t, app, plan, parentID, "valid-object-research-authority")
	prepared, err := app.preparePublicConversationProviderRequest(thread)
	if err != nil {
		t.Fatalf("valid production-shaped authority should reach provider preflight: %v", err)
	}
	if strings.TrimSpace(prepared.Artifact.Metadata[publicConversationProviderRequestKey]) == "" {
		t.Fatal("valid production-shaped authority did not reserve a durable provider request")
	}
	providerContext, err := app.agentThreadProviderContext(context.Background(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	request, found, err := app.decodeDurablePublicConversationProviderRequest(prepared, providerContext.Memory)
	if err != nil || !found || request.NormalizeOutput == nil || request.JSONSchema == nil || request.MaxToolCalls != externalEvidenceMaxToolCalls {
		t.Fatalf("decoded provider request lost the exact evidence contract: found=%t schema=%t normalize=%t calls=%d err=%v", found, request.JSONSchema != nil, request.NormalizeOutput != nil, request.MaxToolCalls, err)
	}
	retained, err := authorizedExternalEvidenceResearchQuestionsForThread(app, prepared)
	if err != nil || len(retained) != 1 || retained[0] != question {
		t.Fatalf("decoded provider request lost normalized question authority: questions=%v err=%v", retained, err)
	}
}

func TestAuthorizedExternalEvidenceQuestionsRejectsObjectWithoutQuestion(t *testing.T) {
	app, plan, parentID := authorizedExternalEvidenceTestContext(t)
	contextStage := plan.subtaskByID("context_snapshot")
	artifact, _ := app.osArtifactByID(contextStage.ArtifactID)
	var object map[string]any
	if err := json.Unmarshal([]byte(artifact.Text), &object); err != nil {
		t.Fatalf("decode context snapshot: %v", err)
	}
	object["research_questions"] = []any{map[string]any{"decision_relevance": "Missing the actual question."}}
	body, _ := json.Marshal(object)
	if _, _, err := app.updateOSArtifactWithMetadata(artifact.ID, "", string(body), scoutParticipantName, artifact.Metadata); err != nil {
		t.Fatalf("update invalid context snapshot: %v", err)
	}

	_, err := authorizedExternalEvidenceResearchQuestions(app, &plan, parentID)
	if err == nil || !strings.Contains(err.Error(), "research question 1 is empty") {
		t.Fatalf("error=%v, want a specific empty-question rejection", err)
	}

	thread := authorizedExternalEvidenceResearchThreadForTest(t, app, plan, parentID, "invalid-research-authority")
	if _, err := app.preparePublicConversationProviderRequest(thread); err == nil || !strings.Contains(err.Error(), "invalid before provider handoff") {
		t.Fatalf("preflight error=%v, want deterministic authority rejection before provider handoff", err)
	}
	current, _ := app.osArtifactByID(thread.Artifact.ID)
	if strings.TrimSpace(current.Metadata[publicConversationProviderRequestKey]) != "" {
		t.Fatal("invalid external evidence authority must not reserve a provider request")
	}
}

func TestExternalEvidenceV2AllowsHonestZeroUsableEvidenceAfterProviderSearch(t *testing.T) {
	envelope := externalEvidenceEnvelope{
		ResearchQuestions:    []string{"What official count establishes the active creator population?"},
		Evidence:             []externalEvidenceEnvelopeRow{},
		ExcludedOrUnverified: []string{"The hosted search found candidate pages, but none stated a complete active-creator count with a usable source assertion."},
	}
	body := appendOpenAIResponseWebSources(externalEvidenceJSONForTest(t, envelope), openAIResponseWebEvidence{
		ResponseID: "resp_zero_usable_evidence", SearchCalls: 2,
		Citations: []openAIResponseWebCitation{{Title: "Creator program overview", URL: "https://example.org/overview"}},
	})
	normalized, err := normalizeExternalEvidenceArtifact(body)
	if err != nil {
		t.Fatalf("honest zero-evidence search outcome was rejected: %v", err)
	}
	rows, err := externalEvidenceLedgerRows(normalized)
	if err != nil || len(rows) != 0 {
		t.Fatalf("zero-evidence ledger did not remain an exact empty table: rows=%#v err=%v", rows, err)
	}
	receipt, err := verifiedResearchCitationReceipt(normalized)
	if err != nil || receipt.CitationCount != 0 || receipt.ProviderCitationCount != 1 || receipt.SearchCalls != 2 || !receipt.HasProviderAudit {
		t.Fatalf("zero-evidence receipt lost the real provider search audit: receipt=%+v err=%v\n%s", receipt, err, normalized)
	}
	if err := validateExternalEvidenceArtifact(normalized); err != nil {
		t.Fatalf("normalized zero-evidence artifact failed terminal validation: %v", err)
	}
	envelope.ExcludedOrUnverified = nil
	if _, err := normalizeExternalEvidenceArtifact(appendOpenAIResponseWebSources(externalEvidenceJSONForTest(t, envelope), openAIResponseWebEvidence{
		ResponseID: "resp_zero_without_reason", SearchCalls: 1,
		Citations: []openAIResponseWebCitation{{URL: "https://example.org/overview"}},
	})); err == nil || !strings.Contains(err.Error(), "empty evidence requires") {
		t.Fatalf("unexplained empty evidence passed: %v", err)
	}
}

func TestExternalEvidenceV2BindsExactAuthorizedQuestionsAndRejectsLowConfidence(t *testing.T) {
	fixture := focusedEntailmentThreadForTest(t, "The official program has 4,200 opted-in creators in 2026.", "https://example.org/creator-program", "")
	questions, err := authorizedExternalEvidenceResearchQuestionsForThread(fixture.app, fixture.researchThread)
	if err != nil || len(questions) != 1 || questions[0] != fixture.authorizedQuestion {
		t.Fatalf("authorized question binding failed: questions=%#v err=%v", questions, err)
	}
	row := externalEvidenceEnvelopeRow{
		ResearchQuestion: fixture.authorizedQuestion, SourceFact: "The official program has 4,200 opted-in creators in 2026.",
		SourceTitle: "Official creator program", URL: fixture.sourceURL, PublishedOrUpdated: "Accessed 2026-08-21",
		Units: "creators", Confidence: "Medium", DeckImplication: "Use as a bounded proof point.",
	}
	providerEvidence := openAIResponseWebEvidence{ResponseID: "resp_question_binding", SearchCalls: 1, Citations: []openAIResponseWebCitation{{Title: row.SourceTitle, URL: row.URL}}}
	matching := appendOpenAIResponseWebSources(externalEvidenceJSONForTest(t, externalEvidenceEnvelope{
		ResearchQuestions: []string{fixture.authorizedQuestion}, Evidence: []externalEvidenceEnvelopeRow{row},
	}), providerEvidence)
	if _, err := normalizeExternalEvidenceArtifactWithQuestions(matching, questions); err != nil {
		t.Fatalf("exact authorized research question was rejected: %v", err)
	}

	offBriefQuestion := "How many lunar mining licenses exist?"
	row.ResearchQuestion = offBriefQuestion
	offBrief := appendOpenAIResponseWebSources(externalEvidenceJSONForTest(t, externalEvidenceEnvelope{
		ResearchQuestions: []string{offBriefQuestion}, Evidence: []externalEvidenceEnvelopeRow{row},
	}), providerEvidence)
	if _, err := normalizeExternalEvidenceArtifactWithQuestions(offBrief, questions); err == nil || !strings.Contains(err.Error(), "authorized context snapshot") {
		t.Fatalf("off-brief self-consistent evidence passed: %v", err)
	}

	row.ResearchQuestion, row.Confidence = fixture.authorizedQuestion, "Low"
	lowConfidence := appendOpenAIResponseWebSources(externalEvidenceJSONForTest(t, externalEvidenceEnvelope{
		ResearchQuestions: []string{fixture.authorizedQuestion}, Evidence: []externalEvidenceEnvelopeRow{row},
	}), providerEvidence)
	if _, err := normalizeExternalEvidenceArtifactWithQuestions(lowConfidence, questions); err == nil || !strings.Contains(err.Error(), "must remain excluded") {
		t.Fatalf("Low-confidence evidence row passed: %v", err)
	}
}

func TestExternalEvidenceQuestionRelevanceRejectsSingleGenericOverlap(t *testing.T) {
	question := "How many opted-in Acme creators participate in 2026?"
	if externalEvidenceCandidateRelevantToQuestion(question, "Beta has 9,000 creators in 2026.") {
		t.Fatal("one generic creator overlap laundered an unrelated entity into scope")
	}
	if !externalEvidenceCandidateRelevantToQuestion(question, "Acme has 4,200 opted-in creators in 2026.") {
		t.Fatal("same-entity, same-population candidate was rejected")
	}
}

func TestExternalEvidenceQuestionRelevanceBindsEntityPopulationMeasureGeoAndTime(t *testing.T) {
	question := "How many opted-in Acme creators participate in the United States in 2026?"
	for _, offBrief := range []string{
		"Acme spent $12 million on creator ads in the United States in 2026.",
		"Acme has 4,200 opted-in creators in Canada in 2026.",
		"Acme has 4,200 opted-in creators in the United States in 2025.",
		"Beta has 4,200 opted-in creators in the United States in 2026.",
		"Acme has 4,200 customers in the United States in 2026.",
	} {
		if externalEvidenceCandidateRelevantToQuestion(question, offBrief) {
			t.Fatalf("semantically off-brief candidate passed relevance: %q", offBrief)
		}
	}
	if !externalEvidenceCandidateRelevantToQuestion(question, "Acme has 4,200 opted-in creators in the United States in 2026.") {
		t.Fatal("same-entity, population, count, geography, and time candidate was rejected")
	}
}

func TestExternalEvidenceResearchQuestionPreservesAuthorityPredicate(t *testing.T) {
	authority := "Decide whether Acme has enough opted-in creators in 2026."
	if externalEvidenceQuestionBoundToAuthority("How much did Acme spend on creator ads in 2026?", authority) {
		t.Fatal("creator-count authority drifted into an ad-spend research question")
	}
	if !externalEvidenceQuestionBoundToAuthority("How many opted-in creators does Acme have in 2026?", authority) {
		t.Fatal("same-entity, population, measure, and year research question was rejected")
	}
	question := "How many opted-in creators does the official program have in 2026?"
	for _, command := range []string{"Build", "Check", "Decide", "Prepare", "Create", "Assess", "Develop", "Determine", "Recommend", "Verify"} {
		commandAuthority := command + " a grounded answer about whether the official program's 2026 opted-in creator count supports proceeding."
		if !externalEvidenceQuestionBoundToAuthority(question, commandAuthority) {
			t.Errorf("sentence-leading command verb %q was mistaken for a required entity", command)
		}
	}
}

func TestExternalEvidenceAdmissionRejectsInterrogativesAndTimeTriggersRegardlessPunctuation(t *testing.T) {
	for _, nonAssertion := range []string{
		"Did Acme have 4,200 creators in 2026.",
		"Is Acme at 4,200 creators in 2026.",
		"When Acme reaches 4,200 creators, launch the pilot.",
		"Once Acme reaches 4,200 creators, launch the pilot.",
	} {
		if externalEvidenceWindowEntailsCandidate(nonAssertion, sourceWindowForSemanticTest("FAQ", nonAssertion, nonAssertion)) {
			t.Fatalf("non-assertion was admitted as evidence: %q", nonAssertion)
		}
	}
}

func TestExternalEvidenceZeroUsableSearchContinuesThroughSnapshotAndEntailment(t *testing.T) {
	fixture := focusedEntailmentThreadForTest(t, "The program has 4,200 active creators.", "https://example.org/overview", "The program has 4,200 active creators.")
	parentID := fixture.thread.Artifact.Metadata["goalParentId"]
	parent, ok := fixture.app.osArtifactByID(parentID)
	if !ok {
		t.Fatal("fixture parent is unavailable")
	}
	plan, ok := decodeGoalPlan(parent.Metadata["goalPlan"])
	if !ok {
		t.Fatal("fixture plan is unavailable")
	}
	researchStage := plan.subtaskByID("external_research")
	researchArtifact, ok := fixture.app.osArtifactByID(researchStage.ArtifactID)
	if !ok {
		t.Fatal("fixture research artifact is unavailable")
	}
	zeroEnvelope := externalEvidenceEnvelope{
		ResearchQuestions:    []string{fixture.authorizedQuestion},
		Evidence:             []externalEvidenceEnvelopeRow{},
		ExcludedOrUnverified: []string{"The searched source did not state one complete, usable active-creator assertion."},
	}
	zeroResearch, err := normalizeExternalEvidenceArtifact(appendOpenAIResponseWebSources(externalEvidenceJSONForTest(t, zeroEnvelope), openAIResponseWebEvidence{
		ResponseID: "resp_zero_pipeline", SearchCalls: 1,
		Citations: []openAIResponseWebCitation{{Title: "Creator program overview", URL: "https://example.org/overview"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	nextResearchVersion := artifactVersion(researchArtifact) + 1
	researchMetadata := researchArtifactEvidenceMetadataAtVersion(scoutAgentThread{Mode: "research"}, zeroResearch, nextResearchVersion)
	researchMetadata["status"], researchMetadata["threadStatus"] = "complete", "complete"
	researchArtifact, _, err = fixture.app.updateOSArtifactWithMetadata(researchArtifact.ID, "", zeroResearch, scoutParticipantName, researchMetadata)
	if err != nil {
		t.Fatal(err)
	}

	sourceBody, sourceMetadata, err := compileExternalEvidenceSourceSnapshotsWithFetcher(fixture.app, &plan, parentID, func(context.Context, string) (externalSourceDocument, error) {
		t.Fatal("zero usable evidence must not fetch any candidate URL")
		return externalSourceDocument{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if sourceMetadata["sourceSnapshotRows"] != "0" || sourceMetadata["sourceSnapshotFetched"] != "0" || sourceMetadata["sourceEvidenceArtifactId"] != researchArtifact.ID {
		t.Fatalf("zero snapshot metadata lost exact search lineage: %+v", sourceMetadata)
	}
	sourceStage := plan.subtaskByID("source_snapshot")
	sourceArtifact, ok := fixture.app.osArtifactByID(sourceStage.ArtifactID)
	if !ok {
		t.Fatal("fixture source artifact is unavailable")
	}
	for key, value := range map[string]string{
		"goalParentId": parentID, "goalSubtaskId": "source_snapshot", "processId": plan.ProcessID,
		"processStage": "source_snapshot", "status": "complete", "threadStatus": "complete",
	} {
		sourceMetadata[key] = value
	}
	if _, _, err := fixture.app.updateOSArtifactWithMetadata(sourceArtifact.ID, "", sourceBody, scoutParticipantName, sourceMetadata); err != nil {
		t.Fatal(err)
	}
	query := "Check the exact authority-bound source windows.\n\n" + sourceBody
	writerArtifact, _, err := fixture.app.updateOSArtifactWithMetadata(fixture.thread.Artifact.ID, "", fixture.thread.Artifact.Text, scoutParticipantName, map[string]string{"threadQuery": query})
	if err != nil {
		t.Fatal(err)
	}
	fixture.thread.Artifact, fixture.thread.Query = writerArtifact, query

	authority, err := authorizedExternalEvidenceEntailmentAuthority(fixture.app, fixture.thread)
	if err != nil || len(authority.Candidates) != 0 || len(authority.SourceEnvelope.Snapshots) != 0 {
		t.Fatalf("zero-candidate authority did not survive source snapshotting: candidates=%d snapshots=%d err=%v", len(authority.Candidates), len(authority.SourceEnvelope.Snapshots), err)
	}
	rawChecks, _ := json.Marshal(externalEvidenceEntailmentEnvelope{Checks: []externalEvidenceEntailmentCheck{}})
	normalized, err := normalizeExternalEvidenceEntailmentArtifact(fixture.app, fixture.thread, string(rawChecks))
	if err != nil {
		t.Fatalf("zero-candidate entailment did not complete: %v", err)
	}
	admitted, err := externalEvidenceEntailmentAdmittedRows(normalized)
	if err != nil || len(admitted) != 0 || !strings.Contains(normalized, "admitted=0") {
		t.Fatalf("zero-candidate entailment did not produce a canonical no-proof artifact: admitted=%#v err=%v\n%s", admitted, err, normalized)
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
	questions, _ := properties["research_questions"].(map[string]any)
	if questions["minItems"] != 1 || questions["maxItems"] != 5 {
		t.Fatalf("question bounds=%#v, want 1..5", questions)
	}
	if evidence["minItems"] != 0 || evidence["maxItems"] != 12 {
		t.Fatalf("evidence bounds=%#v, want 0..12", evidence)
	}
	if request.MaxToolCalls != 6 || !strings.Contains(request.Instructions, "Copy the 1 to 5 research_questions exactly") || !strings.Contains(request.Instructions, "when the approved snapshot contains one") || !strings.Contains(request.Instructions, "second corroborating source only") {
		t.Fatalf("v2 request lost bounded research contract: max_tool_calls=%d\n%s", request.MaxToolCalls, request.Instructions)
	}
	snapshotRaw, err := json.Marshal(durableOpenAIRequest(request))
	if err != nil {
		t.Fatalf("marshal durable external evidence request: %v", err)
	}
	var snapshot durableOpenAITextRequest
	if err := json.Unmarshal(snapshotRaw, &snapshot); err != nil {
		t.Fatalf("unmarshal durable external evidence request: %v", err)
	}
	if snapshot.MaxToolCalls != externalEvidenceMaxToolCalls {
		t.Fatalf("durable snapshot maxToolCalls=%d, want %d", snapshot.MaxToolCalls, externalEvidenceMaxToolCalls)
	}
	restored := snapshot.request(app, thread)
	if restored.JSONSchema == nil || restored.NormalizeOutput == nil || restored.MaxToolCalls != 6 {
		t.Fatal("durable request replay lost v2 schema, server normalizer, or hosted-tool budget")
	}
}

func TestExternalEvidenceEntailmentRejectsUnrelatedClaimFromReadyEvidence(t *testing.T) {
	const sourceURL = "https://example.org/creator-program"
	const fabricated = "The creator program generated 9.2 million purchases in 2026."
	fixture := focusedEntailmentThreadForTest(t, fabricated, sourceURL, "The program explains how creators apply and complete onboarding.")
	body := externalEvidenceEntailmentBodyForTest(t, externalEvidenceEntailmentCheck{
		CandidateID: fixture.candidateID, CandidateFact: fabricated, URL: sourceURL, SourceWindowDigest: "N/A",
		Verdict: "entailed", Confidence: "High", Reason: "The source was fetched.",
	}, []openAIResponseWebCitation{{Title: "Official creator program", URL: sourceURL}})
	normalized, err := normalizeExternalEvidenceEntailmentArtifact(fixture.app, fixture.thread, body)
	if err != nil {
		t.Fatalf("normalize entailment result: %v", err)
	}
	admitted := strings.Split(normalized, "## Rejected or unclear candidate claims")[0]
	if strings.Contains(admitted, fabricated) || !strings.Contains(normalized, "authority-bound server snapshot") {
		t.Fatalf("unrelated fabricated claim reached ready evidence:\n%s", normalized)
	}
}

func TestExternalEvidenceEntailmentRejectsExactButOffQuestionAssertion(t *testing.T) {
	const sourceURL = "https://example.org/orchard"
	const offQuestionFact = "The orchard shipped 900 apples in 2026."
	fixture := focusedEntailmentThreadForTest(t, offQuestionFact, sourceURL, offQuestionFact)
	body := externalEvidenceEntailmentBodyForTest(t, externalEvidenceEntailmentCheck{
		CandidateID: fixture.candidateID, CandidateFact: offQuestionFact, URL: sourceURL, SourceWindowDigest: fixture.windowDigest,
		RelevanceVerdict: "relevant", Verdict: "entailed", Confidence: "High", Reason: "The exact sentence appears on the fetched page.",
	}, nil)
	normalized, err := normalizeExternalEvidenceEntailmentArtifact(fixture.app, fixture.thread, body)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := externalEvidenceEntailmentAdmittedRows(normalized)
	if err != nil || len(rows) != 0 || !strings.Contains(normalized, "exact research-question relevance") {
		t.Fatalf("exact but off-question assertion became authoritative: rows=%#v err=%v\n%s", rows, err, normalized)
	}
}

func TestExternalEvidenceEntailmentRejectsConditionalAndInterrogativeAssertions(t *testing.T) {
	for name, nonAssertion := range map[string]string{
		"conditional":   "If the official program has 4,200 creators, the pilot will expand.",
		"interrogative": "Did the official program have 4,200 creators in 2026?",
	} {
		t.Run(name, func(t *testing.T) {
			fixture := focusedEntailmentThreadForTest(t, nonAssertion, "https://example.org/creator-program", nonAssertion)
			body := externalEvidenceEntailmentBodyForTest(t, externalEvidenceEntailmentCheck{
				CandidateID: fixture.candidateID, CandidateFact: nonAssertion, URL: fixture.sourceURL, SourceWindowDigest: fixture.windowDigest,
				RelevanceVerdict: "relevant", Verdict: "entailed", Confidence: "High", Reason: "The exact text appears on the page.",
			}, nil)
			normalized, err := normalizeExternalEvidenceEntailmentArtifact(fixture.app, fixture.thread, body)
			if err != nil {
				t.Fatal(err)
			}
			rows, rowErr := externalEvidenceEntailmentAdmittedRows(normalized)
			if rowErr != nil || len(rows) != 0 {
				t.Fatalf("non-assertion became authoritative: rows=%#v err=%v\n%s", rows, rowErr, normalized)
			}
		})
	}
}

func TestExternalEvidenceEntailmentBindsExactCandidateAndIndependentProviderFetch(t *testing.T) {
	const sourceURL = "https://example.org/creator-program"
	const candidate = "The official program has 4,200 opted-in creators in 2026."
	fixture := focusedEntailmentThreadForTest(t, candidate, sourceURL, candidate)
	check := externalEvidenceEntailmentCheck{
		CandidateID: fixture.candidateID, CandidateFact: candidate, URL: sourceURL, SourceWindowDigest: fixture.windowDigest,
		Verdict: "entailed", Confidence: "High", Reason: "Same population, units, and date.",
	}
	body := externalEvidenceEntailmentBodyForTest(t, check, []openAIResponseWebCitation{{Title: "Official creator program", URL: sourceURL}})
	normalized, err := normalizeExternalEvidenceEntailmentArtifact(fixture.app, fixture.thread, body)
	if err != nil || !strings.Contains(strings.Split(normalized, "## Rejected or unclear candidate claims")[0], candidate) || !strings.Contains(normalized, "entailment_checked") {
		t.Fatalf("exact entailed claim was not admitted err=%v:\n%s", err, normalized)
	}

	tampered := check
	tampered.CandidateFact = "A different claim"
	if _, err := normalizeExternalEvidenceEntailmentArtifact(fixture.app, fixture.thread, externalEvidenceEntailmentBodyForTest(t, tampered, []openAIResponseWebCitation{{Title: "Official creator program", URL: sourceURL}})); err == nil || !strings.Contains(err.Error(), "exact candidate") {
		t.Fatalf("changed candidate binding passed: %v", err)
	}
	wrongURL := check
	wrongURL.URL = "https://example.org/other"
	if _, err := normalizeExternalEvidenceEntailmentArtifact(fixture.app, fixture.thread, externalEvidenceEntailmentBodyForTest(t, wrongURL, nil)); err == nil || !strings.Contains(err.Error(), "exact candidate") {
		t.Fatalf("changed candidate URL passed: %v", err)
	}
}

func TestExternalEvidenceEntailmentRequestUsesAuthorityBoundNoSearchContract(t *testing.T) {
	fixture := focusedEntailmentThreadForTest(t, "The official program has 4,200 opted-in creators in 2026.", "https://example.org/creator-program", "")
	request := fixture.app.buildAgentThreadOpenAIRequest(fixture.thread, fixture.app.newAgentJob(fixture.thread), time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC))
	if request.JSONSchema == nil || request.JSONSchema.Name != packagingStudioEntailmentContract || request.NormalizeOutput == nil || request.EnableWebSearch || request.MaxToolCalls != 0 {
		t.Fatalf("entailment request missing strict authority-bound no-search contract: %#v", request)
	}
	if !strings.Contains(request.Instructions, "authority-bound, bounded source windows") || !strings.Contains(request.Instructions, "must not infer support") || !strings.Contains(request.Instructions, "measure/unit fidelity") {
		t.Fatalf("entailment instructions lost exact claim/source safeguards:\n%s", request.Instructions)
	}
}

func TestExternalEvidencePDFSnapshotContinuesToNoAdmissionWithStableReroute(t *testing.T) {
	const sourceURL = "https://example.org/research.pdf"
	const candidate = "The program has 4,200 active creators."
	fixture := focusedEntailmentThreadForTest(t, candidate, sourceURL, "__PDF_EXTRACTION_REQUIRED__")
	authority, err := authorizedExternalEvidenceEntailmentAuthority(fixture.app, fixture.thread)
	if err != nil || len(authority.SourceEnvelope.Snapshots) != 1 {
		t.Fatalf("PDF source snapshot authority failed: %+v err=%v", authority.SourceEnvelope, err)
	}
	snapshot := authority.SourceEnvelope.Snapshots[0]
	if snapshot.Status != "extraction_required" || snapshot.CandidateID != fixture.candidateID || snapshot.URL != sourceURL || snapshot.SourceTitle != "Official creator program" || !strings.Contains(snapshot.Note, externalSourcePDFRerouteContract) || len(snapshot.Windows) != 0 {
		t.Fatalf("PDF snapshot lost source-specific extraction-required identity: %+v", snapshot)
	}
	body := externalEvidenceEntailmentBodyForTest(t, externalEvidenceEntailmentCheck{
		CandidateID: fixture.candidateID, CandidateFact: candidate, URL: sourceURL, SourceWindowDigest: "N/A",
		Verdict: "unclear", Confidence: "High", Reason: "The PDF requires authenticated text extraction before this claim can be checked.",
	}, nil)
	normalized, err := normalizeExternalEvidenceEntailmentArtifact(fixture.app, fixture.thread, body)
	if err != nil {
		t.Fatalf("PDF extraction-required source did not continue through entailment: %v", err)
	}
	admitted, err := externalEvidenceEntailmentAdmittedRows(normalized)
	if err != nil || len(admitted) != 0 || !strings.Contains(normalized, candidate) || !strings.Contains(normalized, sourceURL) || !strings.Contains(normalized, "PDF requires authenticated text extraction") {
		t.Fatalf("PDF no-proof path lost its source-specific missing-proof row: admitted=%#v err=%v\n%s", admitted, err, normalized)
	}
}

func TestExternalEvidenceContractInstructionsStayFocusedAndPrivateBound(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread := focusedExternalEvidenceThreadForTest()
	instructions := app.agentThreadInstructionsForThread(thread)
	for _, want := range []string{"focused external-evidence contract", "governed deliverable", "Copy the 1 to 5 research_questions exactly", "when the approved snapshot contains one", "primary or official source", "second corroborating source only", "schema compatibility", "server appends"} {
		if !strings.Contains(instructions, want) {
			t.Errorf("focused instructions missing %q:\n%s", want, instructions)
		}
	}
	for _, forbidden := range []string{"Packaging Studio", "feeding a presentation", "slide-usable", "Comparable Companies", "at least five actually used sources", "1,000-word"} {
		if strings.Contains(instructions, forbidden) {
			t.Errorf("focused instructions inherited generic requirement %q:\n%s", forbidden, instructions)
		}
	}
	if thread.Artifact.Metadata["visibility"] != scoutChatVisibilityPrivate || thread.Artifact.Metadata["ownerEmail"] != "aj@shareability.com" || thread.Artifact.Metadata["originSurface"] != "chat:private-aj" {
		t.Fatalf("contract fixture lost private ACL binding: %+v", thread.Artifact.Metadata)
	}
}

func TestExternalEvidenceEntailmentInstructionsAreArtifactNeutral(t *testing.T) {
	instructions := externalEvidenceEntailmentContractInstructions()
	for _, want := range []string{"Stride's independent claim-to-source check", "governed deliverable", "source_window_digest"} {
		if !strings.Contains(instructions, want) {
			t.Errorf("entailment instructions missing %q:\n%s", want, instructions)
		}
	}
	for _, forbidden := range []string{"Packaging Studio", "presentation", "slide"} {
		if strings.Contains(instructions, forbidden) {
			t.Errorf("entailment instructions prescribed artifact %q:\n%s", forbidden, instructions)
		}
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
	if metadata["researchQualityGate"] != "passed" || metadata["researchEvidenceBinding"] != "provider_fetched_urls" || metadata["researchCitationCount"] != "5" || metadata["researchSourceDomainCount"] != "5" || metadata["researchSourceWindowDigest"] != strings.Repeat("a", 64) {
		t.Fatalf("research evidence metadata=%v", metadata)
	}
	if _, found := metadata["researchProviderSourceCount"]; found {
		t.Fatalf("ordinary research unexpectedly received external-evidence provider audit fields: %v", metadata)
	}
}
