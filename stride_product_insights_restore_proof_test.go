package main

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type strideProductRestoreProofStages struct {
	source            STRIDEProductWorkRecord
	reviseFirstCritic bool
}

func (stages strideProductRestoreProofStages) ExecuteStrideInsightsStage(stage StrideInsightsStageManifest, round int, request StrideInsightsRequest, prior *StrideInsightsReport) (StrideInsightsStageResult, error) {
	result, err := (strideProductInsightsStages{source: stages.source}).ExecuteStrideInsightsStage(stage, round, request, prior)
	if err != nil || !stages.reviseFirstCritic || stage.StageID != "criterion_claim_critic" || round != 1 || result.Verdict == nil {
		return result, err
	}
	result.Verdict.Outcome = insightsCriticRevise
	for index := range result.Verdict.Findings {
		result.Verdict.Findings[index].Verdict = insightsCriticRevise
		result.Verdict.Findings[index].RequiredAction = "Revise the claim before acceptance."
	}
	return result, nil
}

func strideProductRestoreProofFixture(t *testing.T, reviseFirstCritic bool) (STRIDEProductWorkRecord, STRIDEProductInsightsState) {
	t.Helper()
	request, _, principal := strideInsightsFixture(t, "run-restore-proof")
	source := STRIDEProductWorkRecord{Title: "Insights & Opportunities report", Outcome: "Create a grounded decision-ready report.", OwnerID: request.PrincipalID}
	workflow := NewStrideInsightsWorkflow(strideInsightsNow())
	run, err := workflow.Launch(principal, request, strideProductRestoreProofStages{source: source, reviseFirstCritic: reviseFirstCritic})
	if err != nil || run.Status != StrideInsightsStatusAccepted || run.Artifact == nil || run.Outcome == nil {
		t.Fatalf("launch accepted proof fixture: run=%+v err=%v", run, err)
	}
	payload, err := workflow.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	work := STRIDEProductWorkRecord{
		ID: "work-restore-proof", OwnerID: request.PrincipalID, DestinationThreadID: "insights", Status: "completed",
		RunID: run.RunID, ArtifactID: run.Artifact.ArtifactID,
	}
	state := STRIDEProductInsightsState{
		TenantID: request.TenantID, WorkID: work.ID, Revision: 1, WorkflowPayload: payload, WorkflowDigest: temporalDigestBytes(payload),
		CurrentRunID: run.RunID, CurrentReportDigest: run.Reports[len(run.Reports)-1].ReportDigest, UpdatedAt: time.Date(2026, 7, 30, 23, 0, 0, 0, time.UTC),
	}
	if err := validateSTRIDEProductInsightsState(work, state); err != nil {
		t.Fatalf("valid proof fixture rejected: %v", err)
	}
	return work, state
}

func tamperSTRIDEProductRestoreProof(t *testing.T, state STRIDEProductInsightsState, mutate func(*StrideInsightsRun)) STRIDEProductInsightsState {
	t.Helper()
	var snapshot strideInsightsWorkflowSnapshot
	if err := json.Unmarshal(state.WorkflowPayload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Runs) != 1 {
		t.Fatalf("fixture runs=%d want=1", len(snapshot.Runs))
	}
	mutate(&snapshot.Runs[0])
	snapshot.StateDigest = ""
	digestPayload, err := canonicalJSON(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.StateDigest = temporalDigestBytes(digestPayload)
	state.WorkflowPayload, err = canonicalJSON(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	state.WorkflowDigest = temporalDigestBytes(state.WorkflowPayload)
	return state
}

func TestSTRIDEProductInsightsRestoreRequiresCompleteAcceptedExecutionProof(t *testing.T) {
	work, baseline := strideProductRestoreProofFixture(t, false)
	tests := []struct {
		name   string
		mutate func(*StrideInsightsRun)
	}{
		{name: "missing receipt", mutate: func(run *StrideInsightsRun) { run.Receipts = run.Receipts[:len(run.Receipts)-1] }},
		{name: "reordered stages", mutate: func(run *StrideInsightsRun) {
			run.Receipts[0], run.Receipts[1] = run.Receipts[1], run.Receipts[0]
			run.Contributions[0], run.Contributions[1] = run.Contributions[1], run.Contributions[0]
		}},
		{name: "nonzero usage", mutate: func(run *StrideInsightsRun) { run.Receipts[0].InputTokens = 1 }},
		{name: "forged input digest", mutate: func(run *StrideInsightsRun) { run.Receipts[0].InputDigest = temporalDigest("forged-input") }},
		{name: "forged output digest", mutate: func(run *StrideInsightsRun) {
			run.Receipts[0].OutputDigest = temporalDigest("forged-output")
			run.Contributions[0].Digest = run.Receipts[0].OutputDigest
		}},
		{name: "missing contribution", mutate: func(run *StrideInsightsRun) { run.Contributions = run.Contributions[:len(run.Contributions)-1] }},
		{name: "mismatched contribution", mutate: func(run *StrideInsightsRun) { run.Contributions[0].Digest = temporalDigest("wrong-contribution") }},
		{name: "missing critic verdict", mutate: func(run *StrideInsightsRun) { run.Verdicts = nil }},
		{name: "verdict report mismatch", mutate: func(run *StrideInsightsRun) {
			run.Verdicts[0].ReportDigest = temporalDigest("wrong-report")
			copy := run.Verdicts[0]
			copy.VerdictDigest = ""
			raw, _ := canonicalJSON(copy)
			run.Verdicts[0].VerdictDigest = temporalDigestBytes(raw)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tampered := tamperSTRIDEProductRestoreProof(t, baseline, test.mutate)
			if err := validateSTRIDEProductInsightsState(work, tampered); !errors.Is(err, ErrSTRIDEProductInvalid) {
				t.Fatalf("tampered accepted proof err=%v, want %v", err, ErrSTRIDEProductInvalid)
			}
		})
	}
}

func TestSTRIDEProductInsightsRestoreAcceptsCompleteBoundedCriticRevisionProof(t *testing.T) {
	work, state := strideProductRestoreProofFixture(t, true)
	_, run, _, err := restoreSTRIDEProductInsightsState(work, state)
	if err != nil {
		t.Fatal(err)
	}
	if run.CriticRound != 2 || len(run.Reports) != 2 || len(run.Verdicts) != 2 || len(run.Receipts) != 8 || len(run.Contributions) != 8 ||
		run.Verdicts[0].Outcome != insightsCriticRevise || run.Verdicts[1].Outcome != insightsCriticAccept {
		t.Fatalf("bounded critic proof=%+v", run)
	}
}
