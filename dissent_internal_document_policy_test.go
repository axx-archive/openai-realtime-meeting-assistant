package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDissentInternalPolicySelectsOnlyExistingHostProfiles(t *testing.T) {
	thread, request := dissentReceiptTestThread(), dissentReceiptTestRequest()
	plan, err := planInternalDocumentWork(thread, request)
	if err != nil || plan.Model != meetingBrainModel() || plan.Qualification != "not_evaluated" || plan.Policy != "experimental_internal" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	thread.Artifact.Metadata["goalDeliverable"], thread.Artifact.Metadata["goalParentId"], thread.Artifact.Metadata["goalSubtaskId"] = "true", "parent", "write"
	if _, err := planInternalDocumentWork(thread, request); err == nil {
		t.Fatal("lighter unbound profile entered grounded writer route")
	}
	request.Model, request.ReasoningEffort = researchModel(), researchReasoningEffort()
	plan, err = planInternalDocumentWork(thread, request)
	if err != nil || plan.Task != "grounded_document" || plan.Model != researchModel() {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	if err := validateInternalDocumentPlan(plan, thread, request); err != nil {
		t.Fatal(err)
	}
	plan.MaxOutputTokens++
	if validateInternalDocumentPlan(plan, thread, request) == nil {
		t.Fatal("tampered frozen budget admitted")
	}
	request.MaxOutputTokens = agentThreadMaxOutputTokensForThread(thread) + 1
	if _, err := planInternalDocumentWork(thread, request); err == nil {
		t.Fatal("oversized budget admitted")
	}
}

func TestDissentInternalPolicyPrivateBusinessDraftDoesNotAcquireActionAuthority(t *testing.T) {
	thread, request := dissentReceiptTestThread(), dissentReceiptTestRequest()
	thread.Query = "Draft an internal business memo about production, money movement and acquisitions"
	thread.Artifact.Metadata["authority"] = codexJobAuthorityWorkspaceWrite
	plan, err := planInternalDocumentWork(thread, request)
	if err != nil || plan.JudgmentRequired || plan.IndependentReviewRequired || plan.SideEffects != "none" {
		t.Fatalf("private draft acquired action authority: %+v %v", plan, err)
	}
	thread.Artifact.Metadata["authority"] = codexJobAuthorityExternalWrite
	plan, err = planInternalDocumentWork(thread, request)
	if err != nil || !plan.JudgmentRequired || plan.IndependentReviewStatus != "independent_review_unavailable" || plan.SideEffects != "none" {
		t.Fatalf("consequential route misstated: %+v %v", plan, err)
	}
}

func TestDissentDocumentPublicationRecordsActualRenderedReview(t *testing.T) {
	fixture := seedDocumentReportQualityFixture(t, 2)
	fixture.fileJury(t, 9.4, documentReportMinimumJurySeats, "KEEP")
	review := fileAdmittedPublishedDocument(t, &fixture)
	publish := fixture.plan.subtaskByID(documentReportPublishStageID)
	record := mustArtifact(t, fixture.app, publish.ArtifactID)
	var evidence dissentDocumentReviewEvidence
	if err := json.Unmarshal([]byte(record.Metadata["dissentDocumentReview"]), &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Independent || evidence.ReviewType != "same_provider_rendered_review" || evidence.ArtifactID != review.ArtifactID || evidence.PagesDigest != review.PagesDigest || evidence.JuryDigest != review.JuryDigest || len(evidence.SeatIDs) < documentReportMinimumJurySeats {
		t.Fatalf("review evidence=%+v", evidence)
	}
	if evidence.ExecutionDigest != "" {
		t.Fatal("legacy fixture invented executor evidence")
	}
	if _, changed, err := fixture.app.memory.updateOSArtifactWithMetadata(fixture.report.ID, "", fixture.report.Text+"\nA changed conclusion.", "tester", nil); err != nil || !changed {
		t.Fatal(err)
	}
	if _, err := resolvePublishedDocumentReportQuality(fixture.app, fixture.plan, fixture.parentID); err == nil {
		t.Fatal("changed artifact retained published review")
	}
}

func TestDissentProjectEvidenceRejectsDifferentResultTuple(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	viewer := &userAccount{Email: "aj@shareability.com"}
	if execution, assurance := app.studioDissentEvidenceForViewer(context.Background(), viewer, studioProjectResultRef{ArtifactID: "missing", Version: 1, Digest: "missing"}); execution != nil || assurance != nil {
		t.Fatal("missing artifact leaked evidence")
	}
}

func TestDissentProjectEvidenceIsViewerSafeAndExpiresOnEdit(t *testing.T) {
	fixture := setupWorkFeedbackFixture(t)
	thread := scoutAgentThread{ID: "feedback-fixture-run", Artifact: fixture.root}
	thread.Artifact.Metadata["outputContract"] = documentReportOutputContract
	ctx, collector := withDissentDocumentReceipt(context.Background(), thread)
	generatedBody := fixture.root.Text + "\n\nA bounded generated recommendation."
	_, err := callDocumentWorkWithReceipt(ctx, thread, "private-provider-key", dissentReceiptTestRequest(), func(ctx context.Context, _ string, request openAITextRequest) (string, error) {
		usage := dissentReceiptTestUsage()
		captureOpenAIResponseReceipt(ctx, "resp_private_identity", request.Model, &usage)
		return generatedBody, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ := collector.mergeMetadata(nil)
	if _, changed, err := kanbanApp.memory.updateOSArtifactWithMetadata(fixture.root.ID, "", generatedBody, "Scout", metadata); err != nil || !changed {
		t.Fatal(err)
	}
	detail := readWorkFeedbackDetail(t, fixture)
	if detail.Execution == nil || detail.Execution.Status != "observed" || detail.Execution.ActualModel != meetingBrainModel() || detail.Assurance == nil || detail.Assurance.Independent || detail.Assurance.Type != "not_performed" {
		t.Fatalf("execution=%+v assurance=%+v", detail.Execution, detail.Assurance)
	}
	public, _ := json.Marshal(struct {
		Execution *studioDissentExecutionView
		Assurance *studioDissentAssuranceView
	}{detail.Execution, detail.Assurance})
	if strings.Contains(string(public), "resp_private_identity") || strings.Contains(string(public), "private-provider-key") || strings.Contains(string(public), "Private source text") {
		t.Fatal("project evidence leaked provider or source internals")
	}
	viewer := &userAccount{Email: "aj@shareability.com"}
	wrong := *detail.Result
	wrong.Version++
	if execution, assurance := kanbanApp.studioDissentEvidenceForViewer(context.Background(), viewer, wrong); execution != nil || assurance != nil {
		t.Fatal("different result version received evidence")
	}
	if execution, assurance := kanbanApp.studioDissentEvidenceForViewer(context.Background(), &userAccount{Email: "other@example.com"}, *detail.Result); execution != nil || assurance != nil {
		t.Fatal("private result evidence leaked")
	}
	if _, changed, err := kanbanApp.memory.updateOSArtifactWithMetadata(fixture.root.ID, "", fixture.root.Text+"\nA human revision.", "AJ", nil); err != nil || !changed {
		t.Fatal(err)
	}
	after := readWorkFeedbackDetail(t, fixture)
	if after.Execution == nil || after.Execution.Status != "unavailable" || after.Execution.ActualModel != "" || after.Execution.ReceiptDigest != "" {
		t.Fatalf("edited artifact retained execution attribution: %+v", after.Execution)
	}
	if _, changed, err := kanbanApp.memory.updateOSArtifactWithMetadata(fixture.root.ID, "", generatedBody, "AJ", nil); err != nil || !changed {
		t.Fatal(err)
	}
	if reverted := readWorkFeedbackDetail(t, fixture); reverted.Execution == nil || reverted.Execution.Status != "unavailable" {
		t.Fatal("a later human revision inherited old execution identity merely by restoring its text")
	}
}
