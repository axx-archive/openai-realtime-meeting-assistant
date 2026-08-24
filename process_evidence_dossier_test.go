package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestProcessContextDownstreamBriefStripsResearchAuthorityEnvelope(t *testing.T) {
	const question = "What current rules govern the official program?"
	brief := processContextDownstreamBrief(`{
		"direct_ask":"Prepare the decision brief.",
		"research_mode":"external",
		"research_questions":[{
			"question":"`+question+`",
			"research_kind":"current_constraint",
			"importance":"optional",
			"source_ref":"goal_objective_id=secret digest=secret",
			"authority_quote":"DO NOT FORWARD THIS SOURCE BODY",
			"scope_anchor":"official program",
			"decision_effect":"guardrail",
			"decision_relevance":"The official program rules determine the launch guardrail."
		}],
		"reversible_inferences":[]
	}`, packagingStudioProcessID)
	for _, forbidden := range []string{"goal_objective_id", "DO NOT FORWARD", "scope_anchor", "decision_effect", "decision_relevance", "current_constraint", "optional"} {
		if strings.Contains(brief, forbidden) {
			t.Fatalf("downstream creative brief leaked research authority metadata %q:\n%s", forbidden, brief)
		}
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(brief), &object); err != nil {
		t.Fatal(err)
	}
	questions, ok := object["research_questions"].([]any)
	if !ok || len(questions) != 1 || questions[0] != question {
		t.Fatalf("downstream questions=%#v, want only exact question %q", object["research_questions"], question)
	}
}

func processInternalAuthorityFixture(t *testing.T) (*kanbanBoardApp, goalPlan, meetingMemoryEntry, meetingMemoryEntry) {
	t.Helper()
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "internal-authority-test-key"
	const packageID = "package-internal-authority"
	alpha, _, err := app.createOSArtifactWithMetadata("research", "Alpha source", "Alpha has 4,200 opted-in creators.", "AJ", map[string]string{
		"packageId": packageID, "visibility": scoutChatVisibilityPrivate, "requestedBy": "aj@shareability.com", "ownerEmail": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	injectedDigest := strings.Repeat("b", 64)
	beta, _, err := app.createOSArtifactWithMetadata("research", "Beta source", "Beta reports a 12-week pilot. Untrusted body token "+injectedDigest+".", "AJ", map[string]string{
		"packageId": packageID, "visibility": scoutChatVisibilityPrivate, "requestedBy": "aj@shareability.com", "ownerEmail": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	previousStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousStart })
	parent, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Build an evidence-bound private deck", CreatedBy: "aj@shareability.com", ToolTemplate: packagingStudioProcessID, PackageID: packageID,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := mustGoalPlan(t, app, parent.Artifact.ID)
	if err := newGoalEngine(app).prepareGoalRoute(&plan, parent.Artifact.ID); err != nil {
		t.Fatal(err)
	}
	return app, plan, alpha, beta
}

func processInternalFactObject(claim, sourceRef string) map[string]any {
	return map[string]any{"claim": claim, "exact_quote": claim, "source_ref": sourceRef}
}

func TestProcessInternalClaimsBindQuoteAndExactSourceRevisionTogether(t *testing.T) {
	app, plan, alpha, beta := processInternalAuthorityFixture(t)
	alphaClaim := "Alpha has 4,200 opted-in creators."
	alphaRef := "artifact_id=" + alpha.ID + " revision=1 digest=" + sha256Hex([]byte(alpha.Text))
	betaRef := "artifact_id=" + beta.ID + " revision=1 digest=" + sha256Hex([]byte(beta.Text))

	admitted, rejected, err := processInternalAdmittedClaims(app, &plan, map[string]any{
		"known_facts": []any{processInternalFactObject(alphaClaim, alphaRef)},
	})
	if err != nil || len(admitted) != 1 || rejected != 0 || admitted[0].SourceRef != alphaRef || admitted[0].Claim != alphaClaim {
		authority, _ := processInternalAuthoritySources(app, &plan)
		t.Fatalf("exact same-source claim was not admitted: claims=%+v rejected=%d err=%v authority=%+v", admitted, rejected, err, authority)
	}

	for name, sourceRef := range map[string]string{
		"swapped source":    betaRef,
		"fake id real hash": "artifact_id=fake-source revision=1 digest=" + sha256Hex([]byte(alpha.Text)),
		"body token spoof":  "artifact_id=fake-source revision=1 digest=" + strings.Repeat("b", 64),
	} {
		t.Run(name, func(t *testing.T) {
			claims, rejectedCount, claimErr := processInternalAdmittedClaims(app, &plan, map[string]any{
				"known_facts": []any{processInternalFactObject(alphaClaim, sourceRef)},
			})
			if claimErr != nil || len(claims) != 0 || rejectedCount != 1 {
				t.Fatalf("mismatched source authority survived: claims=%+v rejected=%d err=%v", claims, rejectedCount, claimErr)
			}
		})
	}
}

func TestResearchObjectiveAuthorityIsIntentOnly(t *testing.T) {
	app, plan, _, _ := processInternalAuthorityFixture(t)
	objective, ok := processResearchObjectiveAuthoritySource(&plan)
	if !ok {
		t.Fatal("objective intent authority is unavailable")
	}
	factual, err := processInternalAuthoritySources(app, &plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := factual[objective.Ref]; leaked {
		t.Fatal("goal objective leaked into factual evidence authority")
	}
	research, err := processResearchAuthoritySources(app, &plan)
	if err != nil {
		t.Fatal(err)
	}
	if got, exists := research[objective.Ref]; !exists || got.Text != strings.TrimSpace(plan.Objective) {
		t.Fatalf("research intent authority=%+v, want separately addressable objective", got)
	}
}

func TestProcessInternalClaimRequiresOneUnrefutedSourceAssertion(t *testing.T) {
	const claim = "Acme has 4,200 opted-in creators."
	for name, source := range map[string]string{
		"refuting wrapper":    "The claim that Acme has 4,200 opted-in creators. is false.",
		"attributed wrapper":  "A trade group claims Acme has 4,200 opted-in creators.",
		"conditional wrapper": "If true, Acme has 4,200 opted-in creators.",
		"adjacent refutation": claim + " However, the company later retracted it.",
	} {
		t.Run(name, func(t *testing.T) {
			if processInternalSourceEntailsExactClaim(claim, source) {
				t.Fatalf("unsupported inner clause was admitted from %q", source)
			}
		})
	}
	for name, nonAssertion := range map[string]string{
		"whole conditional": "If Acme has 4,200 creators, the pilot will expand.",
		"whole question":    "Did Acme have 4,200 creators in 2026?",
	} {
		t.Run(name, func(t *testing.T) {
			if processInternalSourceEntailsExactClaim(nonAssertion, nonAssertion) {
				t.Fatalf("non-assertion was admitted as an internal fact: %q", nonAssertion)
			}
		})
	}
	for name, source := range map[string]string{
		"exact body":         claim,
		"exact sentence":     "Background context is stable. " + claim + " The pilot starts next week.",
		"no terminal period": "Acme has 4,200 opted-in creators",
	} {
		t.Run(name, func(t *testing.T) {
			candidate := claim
			if name == "no terminal period" {
				candidate = source
			}
			if !processInternalSourceEntailsExactClaim(candidate, source) {
				t.Fatalf("exact source assertion was rejected from %q", source)
			}
		})
	}
}

func TestProcessSourcePacketDisclosesExactMessageAndFileRefs(t *testing.T) {
	app, plan, _, _ := processInternalAuthorityFixture(t)
	selection, err := app.goalRouteSourceSelection(*plan.RouteReceipt)
	if err != nil || len(selection.InternalEvidenceSources) == 0 {
		t.Fatalf("route source selection has no per-source authority: %+v err=%v", selection, err)
	}
	packet, err := newGoalEngine(app).processStageSourcePacket(t.Context(), &plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range selection.InternalEvidenceSources {
		if !strings.Contains(packet, "SOURCE ["+source.Ref+"]") || !strings.Contains(packet, source.Text) {
			t.Fatalf("source ref/body pair is not disclosed to context snapshot: ref=%q\n%s", source.Ref, packet)
		}
	}
	if strings.Contains(packet, "source_selection_digest=") {
		t.Fatalf("aggregate branch digest was exposed as a factual source ref:\n%s", packet)
	}
}

func TestProcessMissingExternalProofKeepsPDFSourceSpecificButNonAuthoritative(t *testing.T) {
	const sourceURL = "https://example.org/research.pdf"
	const candidateFact = "The program has 4,200 active creators."
	fixture := focusedEntailmentThreadForTest(t, candidateFact, sourceURL, "__PDF_EXTRACTION_REQUIRED__")
	body := externalEvidenceEntailmentBodyForTest(t, externalEvidenceEntailmentCheck{
		CandidateID: fixture.candidateID, CandidateFact: candidateFact, URL: sourceURL, SourceWindowDigest: "N/A",
		Verdict: "unclear", Confidence: "High", Reason: "The PDF requires authenticated text extraction before this claim can be checked.",
	}, nil)
	normalized, err := normalizeExternalEvidenceEntailmentArtifact(fixture.app, fixture.thread, body)
	if err != nil {
		t.Fatal(err)
	}
	missing, count, err := canonicalExternalMissingProofManifest(fixture.app, fixture.thread, normalized)
	if err != nil || count != 1 {
		t.Fatalf("source-specific PDF gap was not preserved: count=%d err=%v\n%s", count, err, missing)
	}
	for _, want := range []string{fixture.candidateID, candidateFact, "Official creator program", sourceURL, "extraction_required", externalSourcePDFRerouteContract} {
		if !strings.Contains(missing, want) {
			t.Fatalf("missing-proof table lost %q:\n%s", want, missing)
		}
	}
	if strings.Contains(missing, "entailment_checked") || strings.Contains(missing, "external_source_bound") {
		t.Fatalf("unproved PDF candidate was made authoritative:\n%s", missing)
	}
}

func compileFocusedEvidenceDossierForTest(t *testing.T, verdict, confidence string) (*kanbanBoardApp, goalPlan, meetingMemoryEntry) {
	t.Helper()
	const candidateFact = "In 2026, the official program has 4,200 opted-in creators."
	const sourceURL = "https://example.org/creator-program"
	fixture := focusedEntailmentThreadForTest(t, candidateFact, sourceURL, candidateFact)
	body := externalEvidenceEntailmentBodyForTest(t, externalEvidenceEntailmentCheck{
		CandidateID: fixture.candidateID, CandidateFact: candidateFact, URL: sourceURL,
		SourceWindowDigest: fixture.windowDigest, Verdict: verdict, Confidence: confidence,
		Reason: "The exact authority-bound source window was checked for this candidate claim.",
	}, nil)
	normalized, err := normalizeExternalEvidenceEntailmentArtifact(fixture.app, fixture.thread, body)
	if err != nil {
		t.Fatalf("normalize focused entailment: %v", err)
	}
	writer, _, err := fixture.app.updateOSArtifactWithMetadata(fixture.thread.Artifact.ID, "", normalized, scoutParticipantName, map[string]string{
		"status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	parentID := strings.TrimSpace(writer.Metadata["goalParentId"])
	parent, ok := fixture.app.osArtifactByID(parentID)
	if !ok {
		t.Fatal("focused entailment parent is unavailable")
	}
	plan, ok := decodeGoalPlan(parent.Metadata["goalPlan"])
	if !ok {
		t.Fatal("focused entailment parent plan is unavailable")
	}
	entailment := plan.subtaskByID("evidence_entailment")
	if entailment == nil {
		t.Fatal("focused entailment stage is unavailable")
	}
	entailment.Status, entailment.ArtifactID, entailment.ThreadID = subtaskComplete, writer.ID, writer.Metadata["threadId"]
	encodedPlan, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.app.updateOSArtifactWithMetadata(parentID, "", parent.Text, scoutParticipantName, map[string]string{"goalPlan": string(encodedPlan)}); err != nil {
		t.Fatal(err)
	}
	evidenceBody, evidenceMetadata, err := compileProcessEvidenceDossier(fixture.app, &plan, parentID, ProcessStage{ID: "evidence"})
	if err != nil {
		t.Fatalf("compile focused evidence dossier: %v", err)
	}
	for key, value := range map[string]string{
		"goalParentId": parentID, "goalSubtaskId": "evidence", "processId": plan.ProcessID,
		"processStage": "evidence", "status": "complete", "threadStatus": "complete",
	} {
		evidenceMetadata[key] = value
	}
	evidence, _, err := fixture.app.createOSArtifactWithMetadata("workflow", "Evidence admission dossier", evidenceBody, scoutParticipantName, evidenceMetadata)
	if err != nil {
		t.Fatal(err)
	}
	evidenceStage := plan.subtaskByID("evidence")
	if evidenceStage == nil {
		t.Fatal("focused evidence stage is unavailable")
	}
	evidenceStage.Status, evidenceStage.ArtifactID = subtaskComplete, evidence.ID
	return fixture.app, plan, evidence
}

func cloneEvidenceMetadataForTest(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func TestProcessEvidenceDossierScopesWeakLoadBearingResearchWithoutInventingAnAnswer(t *testing.T) {
	t.Run("high confidence admitted support passes", func(t *testing.T) {
		_, plan, evidence := compileFocusedEvidenceDossierForTest(t, "entailed", "High")
		if evidence.Metadata["evidenceAdequacy"] != processEvidenceAdequacySufficient || evidence.Metadata["researchQuestionsAuthorized"] != "1" || evidence.Metadata["researchQuestionsStrong"] != "1" {
			t.Fatalf("strong coverage metadata=%v", evidence.Metadata)
		}
		if err := validateProcessEvidenceDossier(&plan, evidence); err != nil {
			t.Fatalf("strong exact-question coverage did not pass: %v\n%s", err, evidence.Text)
		}
	})

	t.Run("weak admitted support narrows the story automatically", func(t *testing.T) {
		app, plan, evidence := compileFocusedEvidenceDossierForTest(t, "entailed", "Medium")
		if evidence.Metadata["evidenceAdequacy"] != processEvidenceAdequacyScoped || !strings.Contains(evidence.Text, "| load_bearing |") || !strings.Contains(evidence.Text, "| weak |") || !strings.Contains(evidence.Text, "Automatically narrow the recommendation") {
			t.Fatalf("weak coverage was not recorded honestly: metadata=%v\n%s", evidence.Metadata, evidence.Text)
		}
		if err := validateProcessEvidenceDossier(&plan, evidence); err != nil {
			t.Fatalf("scoped weak coverage did not remain valid authority: %v", err)
		}
		story, ok := packagingStudioDefinition().stageByID("story_architects")
		if !ok {
			t.Fatal("story stage is unavailable")
		}
		if err := newGoalEngine(app).validateProcessStageInputAuthority(&plan, story); err != nil {
			t.Fatalf("scoped dossier could not reach the story stage: %v", err)
		}
	})

	t.Run("unadmitted candidate support is an explicit scoped gap", func(t *testing.T) {
		_, plan, evidence := compileFocusedEvidenceDossierForTest(t, "unclear", "High")
		if evidence.Metadata["evidenceAdequacy"] != processEvidenceAdequacyScoped || !strings.Contains(evidence.Text, "| partial |") {
			t.Fatalf("partial coverage was not recorded honestly:\n%s", evidence.Text)
		}
		if err := validateProcessEvidenceDossier(&plan, evidence); err != nil {
			t.Fatalf("partial coverage did not preserve an uncertainty-first path: %v", err)
		}
	})
}

func TestProcessExternalResearchCoverageTracksMissingPartialAndStrongPerExactQuestion(t *testing.T) {
	authorities := []externalEvidenceResearchQuestionAuthority{
		{Question: "What is the official creator count?", Importance: "load_bearing"},
		{Question: "What is the current participation rule?", Importance: "optional"},
		{Question: "What comparator changes the decision?", Importance: "optional"},
	}
	first := externalEvidenceEnvelopeRow{ResearchQuestion: authorities[0].Question, SourceFact: "The program has 4,200 creators.", SourceTitle: "Official count", URL: "https://example.org/count", PublishedOrUpdated: "Accessed 2026-08-21", Units: "creators", Confidence: "High", DeckImplication: "Use as the ceiling."}
	second := externalEvidenceEnvelopeRow{ResearchQuestion: authorities[1].Question, SourceFact: "The current rule requires opt in.", SourceTitle: "Official rule", URL: "https://example.org/rule", PublishedOrUpdated: "Accessed 2026-08-21", Units: "rule", Confidence: "High", DeckImplication: "Use as a guardrail."}
	firstID, secondID := externalEvidenceCandidateID(first), externalEvidenceCandidateID(second)
	authority := externalEvidenceEntailmentAuthority{
		Candidates: map[string]externalEvidenceEnvelopeRow{firstID: first, secondID: second},
		SourceEnvelope: externalSourceSnapshotEnvelope{Snapshots: []externalSourceSnapshot{
			{CandidateID: firstID, ResearchQuestion: first.ResearchQuestion, CandidateFact: first.SourceFact, URL: first.URL},
			{CandidateID: secondID, ResearchQuestion: second.ResearchQuestion, CandidateFact: second.SourceFact, URL: second.URL},
		}},
	}
	admitted := [][]string{{firstID, first.SourceFact, first.SourceFact, first.SourceTitle, first.URL, "exact window", strings.Repeat("a", 64), "Count", "relevant", "decision_grade", "entailed", "High", "Exact support."}}
	coverage, err := processExternalResearchQuestionCoverage(authorities, authority, admitted)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{processResearchCoverageSupported, processResearchCoveragePartial, processResearchCoverageMissing}
	for index := range want {
		if coverage[index].Question != authorities[index].Question || coverage[index].Coverage != want[index] {
			t.Fatalf("coverage[%d]=%+v, want exact question and %s", index, coverage[index], want[index])
		}
	}
	manifest, adequacy, strong, digest, err := canonicalProcessResearchQuestionCoverageManifest("external", coverage)
	if err != nil || adequacy != processEvidenceAdequacyScoped || strong != 1 || digest == "" || !strings.Contains(manifest, authorities[2].Question) {
		t.Fatalf("coverage manifest adequacy=%q strong=%d digest=%q err=%v\n%s", adequacy, strong, digest, err, manifest)
	}
	adjustment := processEvidenceScopeAdjustment(coverage, adequacy)
	if adjustment != processEvidenceOptionalScopeAdjustment || strings.Contains(adjustment, "decision-critical") {
		t.Fatalf("optional-only gap got misleading scope adjustment %q", adjustment)
	}
}

func TestProcessResearchCoverageNoneAndInternalRemainValid(t *testing.T) {
	for _, mode := range []string{"none", "internal"} {
		t.Run(mode, func(t *testing.T) {
			manifest, adequacy, strong, digest, err := canonicalProcessResearchQuestionCoverageManifest(mode, nil)
			if err != nil || adequacy != processEvidenceAdequacyNotRequired || strong != 0 || digest == "" {
				t.Fatalf("%s coverage manifest adequacy=%q strong=%d digest=%q err=%v", mode, adequacy, strong, digest, err)
			}
			rows, err := processResearchQuestionCoverageRows(manifest, mode)
			if err != nil || len(rows) != 0 || !strings.Contains(manifest, "| None required | not_required | 0 | 0 | 0 | not_required |") {
				t.Fatalf("%s coverage was not a canonical no-research sentinel: rows=%+v err=%v\n%s", mode, rows, err, manifest)
			}
		})
	}
}

func TestProcessEvidenceDossierCoverageTamperingFailsClosed(t *testing.T) {
	_, plan, evidence := compileFocusedEvidenceDossierForTest(t, "entailed", "High")
	index := processEvidenceDossierReceiptPattern.FindStringIndex(evidence.Text)
	if len(index) != 2 {
		t.Fatal("focused evidence receipt is unavailable")
	}
	prefix := strings.TrimSpace(evidence.Text[:index[0]])
	prefix = strings.Replace(prefix, "| supported |", "| weak |", 1)
	digest := sha256Hex([]byte(prefix))
	tampered := evidence
	tampered.Text = prefix + fmt.Sprintf("\n\n<!-- stride-process-evidence-dossier:v1 process=%s external=%s internal=%s digest=%s -->", plan.ProcessID, evidence.Metadata["externalClaimsAdmitted"], evidence.Metadata["internalClaimsAdmitted"], digest)
	tampered.Metadata = cloneEvidenceMetadataForTest(evidence.Metadata)
	tampered.Metadata["evidenceAdmissionDigest"] = digest
	if err := validateProcessEvidenceDossier(&plan, tampered); err == nil || !strings.Contains(err.Error(), "coverage row 1 is malformed") {
		t.Fatalf("resigned inconsistent coverage survived: %v", err)
	}

	tampered = evidence
	tampered.Metadata = cloneEvidenceMetadataForTest(evidence.Metadata)
	tampered.Metadata["researchQuestionsStrong"] = strconv.Itoa(0)
	if err := validateProcessEvidenceDossier(&plan, tampered); err == nil || !strings.Contains(err.Error(), "strong research question count") {
		t.Fatalf("coverage metadata downgrade survived: %v", err)
	}
}
