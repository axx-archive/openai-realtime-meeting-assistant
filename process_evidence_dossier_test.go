package main

import (
	"strings"
	"testing"
)

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
