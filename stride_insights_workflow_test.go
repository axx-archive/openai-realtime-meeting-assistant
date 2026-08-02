package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type strideInsightsFakeStages struct {
	calls        map[string]int
	outcomes     []string
	invented     bool
	failStage    string
	failOnce     bool
	externalCall bool
}

func (fake *strideInsightsFakeStages) ExecuteStrideInsightsStage(stage StrideInsightsStageManifest, round int, request StrideInsightsRequest, prior *StrideInsightsReport) (StrideInsightsStageResult, error) {
	if fake.calls == nil {
		fake.calls = map[string]int{}
	}
	fake.calls[stage.StageID]++
	if fake.failStage == stage.StageID && (!fake.failOnce || fake.calls[stage.StageID] == 1) {
		return StrideInsightsStageResult{}, errors.New("deterministic stage blocked")
	}
	result := StrideInsightsStageResult{Digest: temporalDigest(stage.StageID + ":" + string(rune('0'+round))), Synthetic: true}
	if fake.externalCall {
		result.ExternalCalls = 1
	}
	if stage.StageID == "writer" {
		evidenceID := request.Evidence.Sources[0].EvidenceID
		if fake.invented {
			evidenceID = "invented-evidence"
		}
		result.Report = &StrideInsightsReport{ReportID: "report-" + string(rune('0'+round)), Summary: "Decision-ready internal opportunities.",
			Claims: []StrideInsightsClaim{{ClaimID: "claim-1", Statement: "Customers need a shorter review loop.", EvidenceIDs: []string{evidenceID}, Confidence: .8,
				Impact: "Faster decisions", NextAction: "Run a bounded trial", Owner: request.PrincipalID, DecisionStatus: insightsDecisionProposed}},
			Opportunities: []StrideInsightsOpportunity{{OpportunityID: "opportunity-1", Title: "Shorten review loop", ClaimIDs: []string{"claim-1"}, Impact: "Faster decisions",
				NextAction: "Run a bounded trial", Owner: request.PrincipalID, DecisionStatus: insightsDecisionProposed}}}
	}
	if stage.StageID == "criterion_claim_critic" {
		outcome := insightsCriticAccept
		if len(fake.outcomes) >= round {
			outcome = fake.outcomes[round-1]
		}
		finding := StrideInsightsCriticFinding{Criterion: "grounding", ClaimID: "claim-1", Verdict: outcome, EvidenceIDs: []string{request.Evidence.Sources[0].EvidenceID}}
		if outcome != insightsCriticAccept {
			finding.RequiredAction = "Ground or remove the claim."
		}
		result.Verdict = &StrideInsightsCriticVerdict{VerdictID: "verdict-" + string(rune('0'+round)), Outcome: outcome, Findings: []StrideInsightsCriticFinding{finding}}
	}
	_ = prior
	_ = fake.externalCall
	return result, nil
}

func strideInsightsFixture(t *testing.T, runID string) (StrideInsightsRequest, StrideInsightsAnalystIdentity, ACLPrincipal) {
	t.Helper()
	existing := validInsightsRequest(t)
	evidence, err := NewStrideInsightsEvidenceSnapshot(existing.EvidenceSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 30, 20, 0, 0, 0, time.UTC)
	capability := temporalTestRef(STRIDEContractAgentCapabilityManifest, "insights-capability")
	runtime := temporalTestRef(STRIDEContractAgentCapabilityManifest, "insights-runtime")
	identity, err := NewStrideInsightsAnalystIdentity("tenant-1", "human-author", capability, runtime, at)
	if err != nil {
		t.Fatal(err)
	}
	request := StrideInsightsRequest{Schema: StrideInsightsRequestSchema, WorkflowVersion: StrideInsightsWorkflowVersion, RequestID: "request-" + runID,
		RequestRevision: 1, RunID: runID, TenantID: "tenant-1", PrincipalID: "member-1", Goal: "Find grounded decision-ready opportunities.",
		InternalDestination: "workspace:insights", Binding: identity.Binding(temporalTestRef(STRIDEContractWorkRun, "work-"+runID)), Evidence: evidence,
		ManifestDigest: FixedStrideInsightsWorkflowManifest().ManifestDigest}
	request.RequestDigest, err = strideInsightsRequestDigest(request)
	if err != nil || request.Validate() != nil {
		t.Fatalf("fixture request: %v %v", err, request.Validate())
	}
	return request, identity, ACLPrincipal{TenantID: "tenant-1", ID: "member-1", Kind: ACLPrincipalUser}
}

func strideInsightsNow() func() time.Time {
	value := time.Date(2026, 7, 30, 21, 0, 0, 0, time.UTC)
	return func() time.Time { value = value.Add(time.Second); return value }
}

func TestStrideInsightsClosedSchemasAndAuthorizationPreventLeakage(t *testing.T) {
	request, _, principal := strideInsightsFixture(t, "run-auth")
	raw, _ := json.Marshal(request)
	raw = append(raw[:len(raw)-1], []byte(`,"unexpected":true}`)...)
	if _, err := DecodeStrideInsightsRequest(raw); !errors.Is(err, ErrStrideInsightsInvalid) {
		t.Fatalf("unknown field accepted: %v", err)
	}
	workflow := NewStrideInsightsWorkflow(strideInsightsNow())
	fake := &strideInsightsFakeStages{}
	wrong := principal
	wrong.ID = "other-member"
	if _, err := workflow.Launch(wrong, request, fake); !errors.Is(err, ErrStrideInsightsUnauthorized) {
		t.Fatalf("wrong principal err=%v", err)
	}
	if _, found := workflow.Run(request.RunID); found || len(fake.calls) != 0 {
		t.Fatal("unauthorized request leaked into durable or stage state")
	}
	tampered := request
	tampered.Evidence.Snapshot.PrincipalID = "other-member"
	if _, err := workflow.Launch(principal, tampered, fake); !errors.Is(err, ErrStrideInsightsUnauthorized) {
		t.Fatalf("tampered snapshot err=%v", err)
	}
	external := request
	external.RunID, external.RequestID, external.InternalDestination = "run-external", "request-external", "external:https://example.com"
	external.RequestDigest, _ = strideInsightsRequestDigest(external)
	if _, err := workflow.Launch(principal, external, fake); !errors.Is(err, ErrStrideInsightsUnauthorized) {
		t.Fatalf("external destination err=%v", err)
	}
	if _, err := workflow.Launch(principal, request, &strideInsightsFakeStages{externalCall: true}); !errors.Is(err, ErrStrideInsightsInvalid) {
		t.Fatalf("external stage execution err=%v", err)
	}
	if run, _ := workflow.Run(request.RunID); run.Artifact != nil {
		t.Fatal("external stage execution published an artifact")
	}
}

func TestStrideInsightsRejectsInventedClaimsAndHasNoArtifact(t *testing.T) {
	request, _, principal := strideInsightsFixture(t, "run-invented")
	workflow := NewStrideInsightsWorkflow(strideInsightsNow())
	_, err := workflow.Launch(principal, request, &strideInsightsFakeStages{invented: true})
	if err == nil || !strings.Contains(err.Error(), "invented") {
		t.Fatalf("invented claim err=%v", err)
	}
	run, found := workflow.Run(request.RunID)
	if !found || run.Artifact != nil || len(workflow.artifacts) != 0 {
		t.Fatalf("invented claim published: %+v", run)
	}
}

func TestStrideInsightsDuplicateLaunchAndPublicationAreExactlyOnce(t *testing.T) {
	request, _, principal := strideInsightsFixture(t, "run-once")
	workflow := NewStrideInsightsWorkflow(strideInsightsNow())
	fake := &strideInsightsFakeStages{}
	first, err := workflow.Launch(principal, request, fake)
	if err != nil || first.Status != StrideInsightsStatusAccepted || first.Artifact == nil {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	wantCalls := 0
	for _, count := range fake.calls {
		wantCalls += count
	}
	second, err := workflow.Launch(principal, request, fake)
	gotCalls := 0
	for _, count := range fake.calls {
		gotCalls += count
	}
	if err != nil || second.Artifact == nil || second.Artifact.ArtifactDigest != first.Artifact.ArtifactDigest || wantCalls != gotCalls || len(workflow.artifacts) != 1 {
		t.Fatalf("duplicate launch mutated: %+v %d/%d %v", second, wantCalls, gotCalls, err)
	}
	first.Reports[0].Summary = "caller mutation"
	stored, _ := workflow.Run(request.RunID)
	if stored.Reports[0].Summary == "caller mutation" || stored.Outcome.Validate(request.Binding) != nil {
		t.Fatal("returned report was mutable or outcome was invalid")
	}
	for _, contribution := range stored.Contributions {
		if contribution.Binding != request.Binding {
			t.Fatal("visible contribution lost analyst binding")
		}
	}
}

func TestStrideInsightsCrashSnapshotRestoreResumesAfterCheckpoint(t *testing.T) {
	request, _, principal := strideInsightsFixture(t, "run-resume")
	workflow := NewStrideInsightsWorkflow(strideInsightsNow())
	workflow.InjectCrashAfter("research_extraction")
	fake := &strideInsightsFakeStages{}
	if _, err := workflow.Launch(principal, request, fake); !errors.Is(err, ErrStrideInsightsInjectedCrash) {
		t.Fatalf("crash err=%v", err)
	}
	if fake.calls["goal_ownership"] != 1 || fake.calls["research_extraction"] != 1 {
		t.Fatalf("precrash calls=%v", fake.calls)
	}
	snapshot, err := workflow.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreStrideInsightsWorkflow(snapshot, strideInsightsNow())
	if err != nil {
		t.Fatal(err)
	}
	run, err := restored.Launch(principal, request, fake)
	if err != nil || run.Status != StrideInsightsStatusAccepted || fake.calls["goal_ownership"] != 1 || fake.calls["research_extraction"] != 1 || fake.calls["writer"] != 1 {
		t.Fatalf("resume reran checkpoint: run=%+v calls=%v err=%v", run, fake.calls, err)
	}
}

func TestStrideInsightsCriticBoundRejectsAndPreservesVisibleOutcome(t *testing.T) {
	request, _, principal := strideInsightsFixture(t, "run-critic")
	workflow := NewStrideInsightsWorkflow(strideInsightsNow())
	fake := &strideInsightsFakeStages{outcomes: []string{insightsCriticRevise, insightsCriticRevise}}
	run, err := workflow.Launch(principal, request, fake)
	if !errors.Is(err, ErrStrideInsightsCriticLimit) || run.Status != StrideInsightsStatusRejected || run.Artifact != nil || run.Outcome == nil || run.Outcome.Reason != "critic_round_limit" || len(run.Reports) != 2 || len(run.Verdicts) != 2 {
		t.Fatalf("critic bound run=%+v err=%v", run, err)
	}
	if fake.calls["writer"] != 2 || fake.calls["criterion_claim_critic"] != 2 || fake.calls["verifier"] != 0 {
		t.Fatalf("critic calls=%v", fake.calls)
	}
}

func TestStrideInsightsBlockedRunResumesAndFeedbackBindsCorrectionRerunLineage(t *testing.T) {
	request, _, principal := strideInsightsFixture(t, "run-feedback")
	workflow := NewStrideInsightsWorkflow(strideInsightsNow())
	fake := &strideInsightsFakeStages{failStage: "strategic_framing", failOnce: true}
	run, err := workflow.Launch(principal, request, fake)
	if err == nil || run.Status != StrideInsightsStatusBlocked || run.BlockedReason == "" || run.Artifact != nil {
		t.Fatalf("blocked run=%+v err=%v", run, err)
	}
	run, err = workflow.Launch(principal, request, fake)
	if err != nil || run.Status != StrideInsightsStatusAccepted {
		t.Fatalf("resumed run=%+v err=%v", run, err)
	}
	feedback := StrideInsightsFeedback{Schema: StrideInsightsFeedbackSchema, FeedbackID: "feedback-1", RunID: run.RunID, ReportDigest: run.Reports[len(run.Reports)-1].ReportDigest,
		Action: insightsFeedbackRequestRevision, ActorID: principal.ID, Binding: request.Binding, NewRequestRevision: 2, NewRunID: "run-feedback-rerun", At: time.Date(2026, 7, 30, 22, 0, 0, 0, time.UTC)}
	copy := feedback
	raw, _ := canonicalJSON(copy)
	feedback.FeedbackDigest = temporalDigestBytes(raw)
	updated, err := workflow.SubmitFeedback(principal, run.RunID, feedback)
	if err != nil || len(updated.Feedback) != 1 || updated.Feedback[0].Binding != request.Binding {
		t.Fatalf("feedback=%+v err=%v", updated, err)
	}
	correction := StrideInsightsFeedback{Schema: StrideInsightsFeedbackSchema, FeedbackID: "feedback-correction", RunID: run.RunID, ReportDigest: feedback.ReportDigest,
		Action: insightsFeedbackCorrect, Correction: "Owner should be the product lead.", ActorID: principal.ID, Binding: request.Binding, At: time.Date(2026, 7, 30, 22, 1, 0, 0, time.UTC)}
	raw, _ = canonicalJSON(correction)
	correction.FeedbackDigest = temporalDigestBytes(raw)
	updated, err = workflow.SubmitFeedback(principal, run.RunID, correction)
	if err != nil || len(updated.Feedback) != 2 || updated.Feedback[1].Correction == "" {
		t.Fatalf("correction=%+v err=%v", updated, err)
	}
	rerun := request
	rerun.RunID, rerun.RequestID, rerun.RequestRevision, rerun.ParentRunID, rerun.ParentReportDigest = feedback.NewRunID, "request-rerun", 2, run.RunID, feedback.ReportDigest
	rerun.Binding.WorkRun = temporalTestRef(STRIDEContractWorkRun, "work-rerun")
	rerun.RequestDigest, _ = strideInsightsRequestDigest(rerun)
	rerunResult, err := workflow.Launch(principal, rerun, &strideInsightsFakeStages{})
	if err != nil || rerunResult.Status != StrideInsightsStatusAccepted || rerunResult.Request.ParentReportDigest != feedback.ReportDigest {
		t.Fatalf("rerun=%+v err=%v", rerunResult, err)
	}
}

func TestStrideInsightsPackageGateSealsTokenFreeMetadataAndSyntheticPilotCannotPassE10(t *testing.T) {
	request, identity, principal := strideInsightsFixture(t, "run-package")
	workflow := NewStrideInsightsWorkflow(strideInsightsNow())
	run, err := workflow.Launch(principal, request, &strideInsightsFakeStages{})
	if err != nil {
		t.Fatal(err)
	}
	fixtures := ImmutableStrideInsightsPilotFixtures()
	reviews := make([]string, 20)
	for index := range reviews {
		reviews[index] = temporalDigest("review-" + string(rune('a'+index)))
	}
	pilot, err := EvaluateStrideInsightsSyntheticPilot(fixtures, StrideInsightsPilotRubric{ReviewerIDs: []string{"reviewer-a", "reviewer-b"}, Criteria: []string{"grounding", "usefulness"}}, reviews)
	if err != nil || !pilot.SyntheticOnly || pilot.E10HumanProviderAccepted {
		t.Fatalf("pilot=%+v err=%v", pilot, err)
	}
	seal, err := SealStrideInsightsPackage("tenant-1", identity, run, pilot, time.Date(2026, 7, 30, 23, 0, 0, 0, time.UTC))
	if err != nil || seal.Manifest.Status != "verified" || seal.Listing.Status != "draft" || seal.Availability != "unavailable" || seal.Manifest.Validate() != nil || seal.Listing.Validate() != nil ||
		!isHexDigest(seal.EvalDigest) || !isHexDigest(seal.SampleDigest) || !isHexDigest(seal.CostDigest) || !isHexDigest(seal.RollbackDigest) {
		t.Fatalf("seal=%+v err=%v", seal, err)
	}
	if seal.ProfileRef != request.Binding.Profile || seal.CapabilityRef != request.Binding.Capability || seal.RuntimeRef != request.Binding.Runtime || seal.WorkRunRef != request.Binding.WorkRun || FixedStrideInsightsWorkflowManifest().ExternalWrites {
		t.Fatal("package seal lost profile, capability, runtime, WorkRun, or write boundary")
	}
	tampered := run
	tampered.Receipts[0].ExternalCalls = 1
	if _, err := SealStrideInsightsPackage("tenant-1", identity, tampered, pilot, time.Now().UTC()); !errors.Is(err, ErrStrideInsightsPackageGate) {
		t.Fatalf("external-call receipt passed package gate: %v", err)
	}
	badPilot := pilot
	badPilot.E10HumanProviderAccepted = true
	if _, err := SealStrideInsightsPackage("tenant-1", identity, run, badPilot, time.Now().UTC()); !errors.Is(err, ErrStrideInsightsPackageGate) {
		t.Fatalf("synthetic E10 claim passed package gate: %v", err)
	}
}
