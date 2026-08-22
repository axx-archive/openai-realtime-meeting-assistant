package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

type documentReportQualityFixture struct {
	app      *kanbanBoardApp
	plan     *goalPlan
	parentID string
	report   meetingMemoryEntry
	binding  documentReportRenderBinding
}

func copyDocumentReportMetadata(source map[string]string) map[string]string {
	copy := make(map[string]string, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}

func seedRenderedDocumentReport(t *testing.T, declaredPages int, actualPages int) (*kanbanBoardApp, meetingMemoryEntry) {
	t.Helper()
	app := newIsolatedKanbanBoardApp(t)
	return app, seedRenderedDocumentReportForParent(t, app, "goal-document-rendered", declaredPages, actualPages)
}

func seedRenderedDocumentReportForParent(t *testing.T, app *kanbanBoardApp, parentID string, declaredPages int, actualPages int) meetingMemoryEntry {
	t.Helper()
	report, appended, err := app.createOSArtifactWithMetadata("artifacts", "Western creator opportunity", "# Western Creator Opportunity\n\nA decision-ready report with [evidence](https://example.com).", "tester", map[string]string{
		"type":            artifactTypeMarkdown,
		"title":           "Western creator opportunity",
		"goalParentId":    parentID,
		"goalSubtaskId":   "write",
		"outputContract":  documentReportOutputContract,
		"goalDeliverable": "true",
		"status":          "complete",
		"threadStatus":    "complete",
	})
	if err != nil || !appended {
		t.Fatalf("seed document: appended=%t err=%v", appended, err)
	}
	sourceVersion := artifactVersion(report)
	assets := make([]artifactAsset, 0, actualPages+1)
	pdfRef, err := putBlob([]byte("%PDF-1.7 rendered document"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	assets = append(assets, artifactAsset{Ref: pdfRef, Mime: "application/pdf", Name: "report.pdf", Kind: "pdf", SourceArtifactVersion: sourceVersion})
	for page := 1; page <= actualPages; page++ {
		ref, blobErr := putBlob([]byte(fmt.Sprintf("jpeg document page %d of %d", page, actualPages)), "image/jpeg")
		if blobErr != nil {
			t.Fatal(blobErr)
		}
		assets = append(assets, artifactAsset{Ref: ref, Mime: "image/jpeg", Name: fmt.Sprintf("page-%02d.jpg", page), Kind: "page_image", SourceArtifactVersion: sourceVersion})
	}
	assetsRaw, _ := json.Marshal(assets)
	printDigest := renderPDFContentDigest(renderJobKindPaper, renderResearchReportPrintHTML(report))
	updated, changed, err := app.memory.updateOSArtifactMetadata(report.ID, map[string]string{
		artifactAssetsMetadataKey:              string(assetsRaw),
		"renderStatus":                         renderJobStatusComplete,
		"renderKind":                           renderJobKindPaper,
		"renderPageCount":                      strconv.Itoa(declaredPages),
		"renderPageImages":                     strconv.Itoa(declaredPages),
		renderSourceArtifactVersionMetadataKey: strconv.Itoa(sourceVersion),
		renderSourceContentDigestMetadataKey:   printDigest,
		renderPDFSourceVersionMetadataKey:      strconv.Itoa(sourceVersion),
		renderPDFArtifactVersionMetadataKey:    strconv.Itoa(sourceVersion + 1),
		renderPDFAssetRefMetadataKey:           pdfRef,
	})
	if err != nil || !changed {
		t.Fatalf("attach rendered document: changed=%t err=%v", changed, err)
	}
	return updated
}

func seedDocumentReportQualityFixture(t *testing.T, pages int) documentReportQualityFixture {
	t.Helper()
	app, report := seedRenderedDocumentReport(t, pages, pages)
	binding, err := validateDocumentReportCompletedRender(report)
	if err != nil {
		t.Fatalf("validate seeded render: %v", err)
	}
	fixture := documentReportQualityFixture{
		app: app, parentID: "goal-document-rendered", report: report, binding: binding,
		plan: &goalPlan{ProcessID: documentReportProcessID, Subtasks: []goalSubtask{
			{ID: "write", Status: subtaskComplete, ArtifactID: report.ID},
			{ID: documentReportDraftRenderStageID, Status: subtaskComplete},
			{ID: documentReportJuryStageID, Status: subtaskComplete},
		}},
	}
	fixture.fileRenderRecord(t)
	return fixture
}

func (fixture *documentReportQualityFixture) fileRenderRecord(t *testing.T) {
	t.Helper()
	body, metadata, err := documentReportRenderRecord(fixture.binding)
	if err != nil {
		t.Fatal(err)
	}
	metadata["source"] = "process_stage"
	metadata["processStage"] = documentReportDraftRenderStageID
	metadata["goalParentId"] = fixture.parentID
	record, appended, err := fixture.app.createOSArtifactWithMetadata("workflow", "Exact document render", body, "tester", metadata)
	if err != nil || !appended {
		t.Fatalf("file render record: appended=%t err=%v", appended, err)
	}
	fixture.plan.subtaskByID(documentReportDraftRenderStageID).ArtifactID = record.ID
}

func scorecardForDocumentPages(pages int, score float64, fix string) documentReportSeatScorecard {
	card := documentReportSeatScorecard{Pages: make([]documentReportPageScore, 0, pages)}
	for page := 1; page <= pages; page++ {
		scores := map[string]float64{}
		for _, dimension := range documentReportJuryDimensions {
			scores[dimension] = score
		}
		card.Pages = append(card.Pages, documentReportPageScore{Page: page, Scores: scores, Fixes: []string{fix}, Blockers: []string{}})
	}
	return card
}

func documentJuryVoicesForScore(t *testing.T, pages int, score float64, seats int, fix string) []goalPanelVoice {
	t.Helper()
	personas := documentReportJuryPersonas()
	voices := make([]goalPanelVoice, 0, seats)
	for index := 0; index < seats && index < len(personas); index++ {
		raw, err := json.Marshal(scorecardForDocumentPages(pages, score, fix))
		if err != nil {
			t.Fatal(err)
		}
		voices = append(voices, goalPanelVoice{Persona: personas[index].Name, Text: string(raw)})
	}
	return voices
}

func (fixture *documentReportQualityFixture) fileJury(t *testing.T, score float64, seats int, fix string) documentReportJuryReadiness {
	t.Helper()
	voices := documentJuryVoicesForScore(t, fixture.binding.PageCount, score, seats, fix)
	readiness := evaluateDocumentReportJury(voices, fixture.binding.PageCount)
	var body strings.Builder
	body.WriteString("# Rendered document jury\n\n## Exact seat scorecards\n")
	for _, voice := range voices {
		body.WriteString("\n### " + voice.Persona + "\n" + strings.TrimSpace(voice.Text) + "\n")
	}
	repairs := readiness.Repairs
	if repairs == nil {
		repairs = []documentReportRepair{}
	}
	repairsRaw, _ := json.Marshal(repairs)
	metadata := map[string]string{
		"artifactContract":         documentReportJuryContract,
		"type":                     artifactTypeMarkdown,
		"source":                   documentReportJurySource,
		"goalId":                   fixture.parentID,
		"documentArtifactId":       fixture.binding.ArtifactID,
		"documentSourceVersion":    strconv.Itoa(fixture.binding.SourceVersion),
		"documentArtifactVersion":  strconv.Itoa(fixture.binding.ArtifactVersion),
		"documentContentDigest":    fixture.binding.ContentDigest,
		"documentCapabilityDigest": fixture.binding.CapabilityDigest,
		"renderPdfAssetRef":        fixture.binding.PDFAssetRef,
		"renderPageCount":          strconv.Itoa(fixture.binding.PageCount),
		"renderPagesDigest":        fixture.binding.PagesDigest,
		"reviewVerdict":            readiness.Verdict,
		"blockingPages":            slideJuryPageList(readiness.BlockingPages),
		"minimumAverage":           strconv.FormatFloat(readiness.MinimumAverage, 'f', 2, 64),
		"parsedSeats":              strconv.Itoa(readiness.ParsedSeats),
		"jurySeatIds":              strings.Join(readiness.SeatIDs, ","),
		"repairFixes":              string(repairsRaw),
	}
	metadata["jurySeatsDigest"] = sha256Hex([]byte(strings.TrimSpace(body.String())))
	jury, appended, err := fixture.app.createOSArtifactWithMetadata("workflow", "Rendered document jury", strings.TrimSpace(body.String()), "tester", metadata)
	if err != nil || !appended {
		t.Fatalf("file jury: appended=%t err=%v", appended, err)
	}
	jury = mustArtifact(t, fixture.app, jury.ID)
	if jury.Metadata["jurySeatsDigest"] != sha256Hex([]byte(jury.Text)) {
		if _, _, err := fixture.app.memory.updateOSArtifactMetadata(jury.ID, map[string]string{"jurySeatsDigest": sha256Hex([]byte(jury.Text))}); err != nil {
			t.Fatal(err)
		}
		jury = mustArtifact(t, fixture.app, jury.ID)
	}
	stageMetadata := map[string]string{
		"source": "process_stage", "processStage": documentReportJuryStageID, "goalParentId": fixture.parentID,
		"documentJuryArtifactId": jury.ID, "documentJuryArtifactDigest": artifactCapabilityDigest(jury),
	}
	for _, key := range []string{"reviewVerdict", "blockingPages", "minimumAverage", "parsedSeats", "jurySeatIds", "jurySeatsDigest", "repairFixes", "documentArtifactId", "documentSourceVersion", "documentArtifactVersion", "documentContentDigest", "documentCapabilityDigest", "renderPdfAssetRef", "renderPageCount", "renderPagesDigest"} {
		stageMetadata[key] = jury.Metadata[key]
	}
	stage, appended, err := fixture.app.createOSArtifactWithMetadata("workflow", "Rendered document jury stage", "Rendered jury completed", "tester", stageMetadata)
	if err != nil || !appended {
		t.Fatalf("file jury stage: appended=%t err=%v", appended, err)
	}
	fixture.plan.subtaskByID(documentReportJuryStageID).ArtifactID = stage.ID
	return readiness
}

func TestDocumentReportJuryWeakButValidProducesExecutableRepairs(t *testing.T) {
	voices := documentJuryVoicesForScore(t, 2, 8.2, 2, "Reduce the body copy to four short paragraphs")
	readiness := evaluateDocumentReportJury(voices, 2)
	if readiness.Verdict != "needs_changes" || readiness.ParsedSeats != 2 || readiness.MinimumAverage != 8.2 || len(readiness.BlockingPages) != 2 || len(readiness.Repairs) != 2 {
		t.Fatalf("weak valid readiness=%+v", readiness)
	}
	for _, repair := range readiness.Repairs {
		if len(repair.Fixes) == 0 || !validDocumentReportRepair(repair.Fixes[0]) {
			t.Fatalf("repair is not executable: %+v", repair)
		}
	}

	fixture := seedDocumentReportQualityFixture(t, 2)
	fixture.fileJury(t, 8.2, 2, "Reduce the body copy to four short paragraphs")
	review, err := resolveDocumentReportQualityReview(fixture.app, fixture.plan, fixture.parentID)
	if err != nil || review.Verdict != "needs_changes" || len(review.Repairs) != 2 {
		t.Fatalf("resolved weak jury=%+v err=%v", review, err)
	}
}

func TestDocumentReportJuryOneSeatNeedsAttention(t *testing.T) {
	readiness := evaluateDocumentReportJury(documentJuryVoicesForScore(t, 1, 9.5, 1, "KEEP"), 1)
	if readiness.Verdict != "needs_attention" || readiness.ParsedSeats != 1 || readiness.MinimumAverage != 0 {
		t.Fatalf("one-seat readiness=%+v", readiness)
	}
	decision := documentReportRenderedGateDecision(documentReportQualityReview{Verdict: "needs_attention"}, nil, ProcessGateSpec{HoldOnFailure: true}, 0)
	if decision.Outcome != goalGateOutcomeBlocked || decision.Verdict != goalReviewFail {
		t.Fatalf("needs_attention gate decision=%+v", decision)
	}
}

func TestDocumentReportRenderMissingPageNeedsAttention(t *testing.T) {
	_, report := seedRenderedDocumentReport(t, 2, 1)
	if _, err := validateDocumentReportCompletedRender(report); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("missing rendered page was admitted: %v", err)
	}
	_, metadata, err := documentReportAttentionRecord("the render is missing one page", report, documentReportRenderBinding{})
	if err != nil || metadata["reviewVerdict"] != "needs_attention" {
		t.Fatalf("missing-page attention metadata=%v err=%v", metadata, err)
	}
}

func TestDocumentReportPageAssetsOrderAndBindOneHundredPagesNumerically(t *testing.T) {
	_, report := seedRenderedDocumentReport(t, renderPageImageAssetCap, renderPageImageAssetCap)
	binding, err := validateDocumentReportCompletedRender(report)
	if err != nil {
		t.Fatalf("100-page render was rejected by lexical filename ordering: %v", err)
	}
	pages, err := documentReportPageAssets(report)
	if err != nil {
		t.Fatalf("order 100 rendered pages: %v", err)
	}
	if len(pages) != renderPageImageAssetCap || pages[9].Name != "page-10.jpg" || pages[98].Name != "page-99.jpg" || pages[99].Name != "page-100.jpg" {
		t.Fatalf("100-page numeric order is wrong around digit boundaries: page10=%q page99=%q page100=%q", pages[9].Name, pages[98].Name, pages[99].Name)
	}
	if binding.PageCount != renderPageImageAssetCap || binding.PagesDigest != documentReportPagesDigest(pages) {
		t.Fatalf("100-page binding lost exact coverage: %+v", binding)
	}

	assets := artifactAssets(report)
	for left, right := 0, len(assets)-1; left < right; left, right = left+1, right-1 {
		assets[left], assets[right] = assets[right], assets[left]
	}
	reversed := report
	reversed.Metadata = copyDocumentReportMetadata(report.Metadata)
	reversedAssets, _ := json.Marshal(assets)
	reversed.Metadata[artifactAssetsMetadataKey] = string(reversedAssets)
	reordered, err := documentReportPageAssets(reversed)
	if err != nil {
		t.Fatalf("numeric page identity should be independent of attachment order: %v", err)
	}
	if digest := documentReportPagesDigest(reordered); digest != binding.PagesDigest {
		t.Fatalf("canonical 100-page digest changed with attachment order: got %s want %s", digest, binding.PagesDigest)
	}

	fixture := seedDocumentReportQualityFixture(t, renderPageImageAssetCap)
	fixture.fileJury(t, 9.2, documentReportMinimumJurySeats, "KEEP")
	review, err := resolveDocumentReportQualityReview(fixture.app, fixture.plan, fixture.parentID)
	if err != nil || review.Verdict != "ready" || review.PageCount != renderPageImageAssetCap || review.PagesDigest != fixture.binding.PagesDigest {
		t.Fatalf("100-page exact rendered admission failed: review=%+v err=%v", review, err)
	}
}

func TestDocumentReportPageAssetsFailClosedOnDuplicateGapAndAlias(t *testing.T) {
	_, report := seedRenderedDocumentReport(t, 3, 3)
	cases := []struct {
		name   string
		mutate func([]artifactAsset)
	}{
		{name: "duplicate numeric identity", mutate: func(assets []artifactAsset) {
			assets[len(assets)-1].Name = "page-02.jpg"
		}},
		{name: "missing numeric identity", mutate: func(assets []artifactAsset) {
			assets[len(assets)-1].Name = "page-04.jpg"
		}},
		{name: "noncanonical alias", mutate: func(assets []artifactAsset) {
			assets[len(assets)-1].Name = "page-003.jpg"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mutated := report
			mutated.Metadata = copyDocumentReportMetadata(report.Metadata)
			assets := artifactAssets(report)
			tc.mutate(assets)
			raw, _ := json.Marshal(assets)
			mutated.Metadata[artifactAssetsMetadataKey] = string(raw)
			if _, err := documentReportPageAssets(mutated); err == nil {
				t.Fatalf("%s page set was admitted", tc.name)
			}
		})
	}
}

func TestDocumentReportAdmissionRejectsStaleDraft(t *testing.T) {
	fixture := seedDocumentReportQualityFixture(t, 2)
	fixture.fileJury(t, 9.2, 2, "KEEP")
	if _, err := resolveDocumentReportQualityReview(fixture.app, fixture.plan, fixture.parentID); err != nil {
		t.Fatalf("fresh rendered review did not resolve: %v", err)
	}
	if _, changed, err := fixture.app.memory.updateOSArtifactWithMetadata(fixture.report.ID, "", fixture.report.Text+"\n\nA repaired conclusion.", "tester", nil); err != nil || !changed {
		t.Fatalf("revise document: changed=%t err=%v", changed, err)
	}
	if _, err := resolveDocumentReportQualityReview(fixture.app, fixture.plan, fixture.parentID); err == nil {
		t.Fatal("stale rendered jury admitted a changed document")
	}
}

func attachFreshDocumentRender(t *testing.T, fixture *documentReportQualityFixture) {
	t.Helper()
	report := mustArtifact(t, fixture.app, fixture.report.ID)
	sourceVersion := artifactVersion(report)
	pdfRef, err := putBlob([]byte(fmt.Sprintf("%%PDF repaired document v%d", sourceVersion)), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	assets := []artifactAsset{{Ref: pdfRef, Mime: "application/pdf", Name: "report.pdf", Kind: "pdf", SourceArtifactVersion: sourceVersion}}
	for page := 1; page <= fixture.binding.PageCount; page++ {
		ref, blobErr := putBlob([]byte(fmt.Sprintf("repaired page %d source v%d", page, sourceVersion)), "image/jpeg")
		if blobErr != nil {
			t.Fatal(blobErr)
		}
		assets = append(assets, artifactAsset{Ref: ref, Mime: "image/jpeg", Name: fmt.Sprintf("page-%02d.jpg", page), Kind: "page_image", SourceArtifactVersion: sourceVersion})
	}
	assetsRaw, _ := json.Marshal(assets)
	printDigest := renderPDFContentDigest(renderJobKindPaper, renderResearchReportPrintHTML(report))
	updated, changed, err := fixture.app.memory.updateOSArtifactMetadata(report.ID, map[string]string{
		artifactAssetsMetadataKey: string(assetsRaw), "renderStatus": renderJobStatusComplete, "renderKind": renderJobKindPaper,
		"renderPageCount": strconv.Itoa(fixture.binding.PageCount), "renderPageImages": strconv.Itoa(fixture.binding.PageCount),
		renderSourceArtifactVersionMetadataKey: strconv.Itoa(sourceVersion), renderSourceContentDigestMetadataKey: printDigest,
		renderPDFSourceVersionMetadataKey: strconv.Itoa(sourceVersion), renderPDFArtifactVersionMetadataKey: strconv.Itoa(sourceVersion + 1),
		renderPDFAssetRefMetadataKey: pdfRef,
	})
	if err != nil || !changed {
		t.Fatalf("attach repaired render: changed=%t err=%v", changed, err)
	}
	fixture.report = updated
	fixture.binding, err = validateDocumentReportCompletedRender(updated)
	if err != nil {
		t.Fatalf("validate repaired render: %v", err)
	}
	fixture.fileRenderRecord(t)
}

func TestDocumentReportAdmissionAcceptsRepairedDraftOnlyAfterFreshJury(t *testing.T) {
	fixture := seedDocumentReportQualityFixture(t, 2)
	fixture.fileJury(t, 8.0, 2, "Reduce the body copy to four short paragraphs")
	weak, err := resolveDocumentReportQualityReview(fixture.app, fixture.plan, fixture.parentID)
	if err != nil || weak.Verdict != "needs_changes" {
		t.Fatalf("weak draft review=%+v err=%v", weak, err)
	}
	decision := documentReportRenderedGateDecision(weak, nil, ProcessGateSpec{MaxRounds: 2}, 0)
	if decision.Outcome != goalGateOutcomeRevise || len(decision.Gaps) == 0 {
		t.Fatalf("weak draft decision=%+v", decision)
	}
	if _, changed, err := fixture.app.memory.updateOSArtifactWithMetadata(fixture.report.ID, "", fixture.report.Text+"\n\n## Clear next decision\n\nChoose the bounded pilot.", "tester", nil); err != nil || !changed {
		t.Fatalf("repair document: changed=%t err=%v", changed, err)
	}
	if _, err := resolveDocumentReportQualityReview(fixture.app, fixture.plan, fixture.parentID); err == nil {
		t.Fatal("the repaired text reused the old page jury")
	}
	attachFreshDocumentRender(t, &fixture)
	fixture.fileJury(t, 9.3, 2, "KEEP")
	fresh, err := resolveDocumentReportQualityReview(fixture.app, fixture.plan, fixture.parentID)
	if err != nil || fresh.Verdict != "ready" || fresh.MinimumAverage < documentReportReadyAverageFloor {
		t.Fatalf("fresh repaired review=%+v err=%v", fresh, err)
	}
	accepted := documentReportRenderedGateDecision(fresh, nil, ProcessGateSpec{MaxRounds: 2}, 1)
	if accepted.Outcome != goalGateOutcomeAccept || accepted.Verdict != goalReviewPass {
		t.Fatalf("fresh repaired decision=%+v", accepted)
	}
}

func fileAdmittedPublishedDocument(t *testing.T, fixture *documentReportQualityFixture) documentReportQualityReview {
	t.Helper()
	review, err := resolveDocumentReportQualityReview(fixture.app, fixture.plan, fixture.parentID)
	if err != nil || review.Verdict != "ready" {
		t.Fatalf("resolve ready review: %+v err=%v", review, err)
	}
	admissionMetadata := review.gateMetadata()
	admissionMetadata["source"] = "process_stage"
	admissionMetadata["processStage"] = documentReportRenderedAdmissionID
	admissionMetadata["goalParentId"] = fixture.parentID
	admission, appended, err := fixture.app.createOSArtifactWithMetadata("workflow", "Rendered admission", "Exact rendered document admitted", "tester", admissionMetadata)
	if err != nil || !appended {
		t.Fatalf("file admission: appended=%t err=%v", appended, err)
	}
	admissionStage := fixture.plan.subtaskByID(documentReportRenderedAdmissionID)
	if admissionStage == nil {
		fixture.plan.Subtasks = append(fixture.plan.Subtasks, goalSubtask{ID: documentReportRenderedAdmissionID})
		admissionStage = fixture.plan.subtaskByID(documentReportRenderedAdmissionID)
	}
	admissionStage.Status, admissionStage.ArtifactID = subtaskComplete, admission.ID
	admissionStage.Review = &goalSubtaskReview{Verdict: goalReviewPass, By: "process_stage"}
	publishBody, publishMetadata, err := compileDocumentReportPublish(fixture.app, fixture.plan, fixture.parentID, ProcessStage{ID: documentReportPublishStageID})
	if err != nil {
		t.Fatalf("compile publish: %v", err)
	}
	publishMetadata["source"] = "process_stage"
	publishMetadata["processStage"] = documentReportPublishStageID
	publishMetadata["goalParentId"] = fixture.parentID
	publish, appended, err := fixture.app.createOSArtifactWithMetadata("workflow", "Publish document", publishBody, "tester", publishMetadata)
	if err != nil || !appended {
		t.Fatalf("file publish: appended=%t err=%v", appended, err)
	}
	publishStage := fixture.plan.subtaskByID(documentReportPublishStageID)
	if publishStage == nil {
		fixture.plan.Subtasks = append(fixture.plan.Subtasks, goalSubtask{ID: documentReportPublishStageID})
		publishStage = fixture.plan.subtaskByID(documentReportPublishStageID)
	}
	publishStage.Status, publishStage.ArtifactID = subtaskComplete, publish.ID
	publishStage.Review = &goalSubtaskReview{Verdict: goalReviewPass}
	if strings.TrimSpace(fixture.plan.GoalID) == "" {
		fixture.plan.GoalID = "agent-thread-goal-document-fixture"
	}
	fixture.plan.PlanVersion = goalPlanVersion
	fixture.plan.State = goalStateVerified
	fixture.plan.Authority = codexJobAuthorityWorkspaceWrite
	rawPlan, err := json.Marshal(fixture.plan)
	if err != nil {
		t.Fatal(err)
	}
	parentMetadata := map[string]string{
		"source": "goal_thread", "mode": "goal", "threadId": fixture.plan.GoalID,
		"processId": fixture.plan.ProcessID, "goalPlan": string(rawPlan),
	}
	if parent, ok := fixture.app.osArtifactByID(fixture.parentID); ok {
		if _, changed, err := fixture.app.updateOSArtifactWithMetadata(parent.ID, "", parent.Text, "tester", parentMetadata); err != nil || !changed {
			t.Fatalf("refresh exact document parent: changed=%t err=%v", changed, err)
		}
	} else if _, appended, err := fixture.app.memory.appendOSArtifact(fixture.parentID, "Document goal fixture", parentMetadata); err != nil || !appended {
		t.Fatalf("seed exact document parent: appended=%t err=%v", appended, err)
	}
	return review
}

func TestDocumentReportPostJuryGateAndVerifierAreDeterministic(t *testing.T) {
	fixture := seedDocumentReportQualityFixture(t, 2)
	fixture.fileJury(t, 9.4, 2, "KEEP")
	review := fileAdmittedPublishedDocument(t, &fixture)
	if fixture.plan.GoalID == fixture.parentID {
		t.Fatal("fixture did not reproduce the normal thread-id versus parent-artifact-id split")
	}
	if got := documentReportPlanParentID(fixture.app, fixture.plan); got != fixture.parentID {
		t.Fatalf("document parent resolver=%q, want %q", got, fixture.parentID)
	}
	engine := newGoalEngine(fixture.app)
	modelCalls := 0
	engine.openAIResponder = func(context.Context, string, openAITextRequest) (string, error) {
		modelCalls++
		return "", fmt.Errorf("text scorer must not run after rendered admission")
	}
	engine.gate(context.Background(), fixture.plan)
	if fixture.plan.Gate.Status != "passed" || fixture.plan.Gate.ReviewedBy != "document_rendered_admission" || modelCalls != 0 {
		t.Fatalf("deterministic ship gate=%+v modelCalls=%d", fixture.plan.Gate, modelCalls)
	}
	if !engine.verify(context.Background(), fixture.plan) || modelCalls != 0 || !strings.Contains(fixture.plan.Verification.Reasons, fmt.Sprintf("v%d", review.ArtifactVersion)) {
		t.Fatalf("deterministic verify=%+v modelCalls=%d", fixture.plan.Verification, modelCalls)
	}
}

func TestNormalLaunchDocumentTerminalResolvesParentArtifactNotThreadID(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "document-parent-binding-test")
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "document-parent-binding-test"
	previousStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousStart })

	run, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Write the western creator market opportunity report",
		CreatedBy: "aj@shareability.com", ToolTemplate: documentReportProcessID,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := mustGoalPlan(t, app, run.Artifact.ID)
	engine := newGoalEngine(app)
	if err := engine.prepareGoalRoute(&plan, run.Artifact.ID); err != nil {
		t.Fatal(err)
	}
	if err := instantiateProcessPlan(documentReportDefinition(), &plan); err != nil {
		t.Fatal(err)
	}
	report := seedRenderedDocumentReportForParent(t, app, run.Artifact.ID, 2, 2)
	binding, err := validateDocumentReportCompletedRender(report)
	if err != nil {
		t.Fatal(err)
	}
	fixture := documentReportQualityFixture{app: app, plan: &plan, parentID: run.Artifact.ID, report: report, binding: binding}
	write := plan.subtaskByID("write")
	render := plan.subtaskByID(documentReportDraftRenderStageID)
	jury := plan.subtaskByID(documentReportJuryStageID)
	if write == nil || render == nil || jury == nil {
		t.Fatal("normal document process did not instantiate its rendered-admission stages")
	}
	write.Status, write.ArtifactID = subtaskComplete, report.ID
	render.Status = subtaskComplete
	jury.Status = subtaskComplete
	fixture.fileRenderRecord(t)
	fixture.fileJury(t, 9.4, documentReportMinimumJurySeats, "KEEP")
	review := fileAdmittedPublishedDocument(t, &fixture)

	if plan.GoalID == run.Artifact.ID || documentReportPlanParentID(app, &plan) != run.Artifact.ID {
		t.Fatalf("normal identity split was not resolved: thread=%q parent=%q resolved=%q", plan.GoalID, run.Artifact.ID, documentReportPlanParentID(app, &plan))
	}
	modelCalls := 0
	engine.openAIResponder = func(context.Context, string, openAITextRequest) (string, error) {
		modelCalls++
		return "", fmt.Errorf("post-jury text scorer must not run")
	}
	engine.gate(context.Background(), &plan)
	if plan.Gate.Status != "passed" || !engine.verify(context.Background(), &plan) || modelCalls != 0 || !strings.Contains(plan.Verification.Reasons, fmt.Sprintf("v%d", review.ArtifactVersion)) {
		t.Fatalf("normal document terminal gate=%+v verify=%+v modelCalls=%d", plan.Gate, plan.Verification, modelCalls)
	}
}
