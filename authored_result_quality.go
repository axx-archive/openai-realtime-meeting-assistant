package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Authored-result quality is deliberately separate from the lifecycle status
// on either the goal or its child artifact. A child can be complete while the
// goal is blocked (salvage), and a once-admitted result can be changed later by
// a human editor. Only the exact revision revalidated against its deterministic
// rendered admission may project final-presentation/export capabilities.
const (
	authoredResultQualityAdmitted             = "admitted"
	authoredResultQualityDraftNeedsAttention  = "draft_needs_attention"
	authoredResultQualityEditedAfterAdmission = "edited_after_admission"
)

type authoredResultAdmissionTuple struct {
	ArtifactID       string
	ArtifactVersion  int
	ContentDigest    string
	CapabilityDigest string
}

func (tuple authoredResultAdmissionTuple) identifies(entry meetingMemoryEntry) bool {
	if strings.TrimSpace(tuple.ArtifactID) == "" || tuple.ArtifactVersion < 1 || strings.TrimSpace(entry.ID) != strings.TrimSpace(tuple.ArtifactID) {
		return false
	}
	if artifactVersion(entry) != tuple.ArtifactVersion {
		return false
	}
	if strings.TrimSpace(tuple.ContentDigest) != "" && !strings.EqualFold(strings.TrimSpace(tuple.ContentDigest), documentReportBodyDigest(entry)) {
		return false
	}
	return strings.TrimSpace(tuple.CapabilityDigest) != "" &&
		strings.EqualFold(strings.TrimSpace(tuple.CapabilityDigest), artifactCapabilityDigest(entry))
}

// packagingStudioAdmissionTuple reads only the server-authored quality and
// publication records that were part of the verified plan. It does not itself
// admit anything; resolvePublishedPackagingStudioQuality remains the complete
// revalidation boundary. This tuple exists so a later Studio mutation can be
// represented truthfully as edited_after_admission instead of generic failure.
func packagingStudioAdmissionTuple(app *kanbanBoardApp, plan goalPlan, parentID string) (authoredResultAdmissionTuple, bool) {
	if app == nil || plan.ProcessID != packagingStudioProcessID || plan.State != goalStateVerified {
		return authoredResultAdmissionTuple{}, false
	}
	quality := plan.subtaskByID("quality_gate")
	publish := plan.subtaskByID("ship_compile")
	if quality == nil || publish == nil || quality.Status != subtaskComplete || publish.Status != subtaskComplete || strings.TrimSpace(quality.ArtifactID) == "" || strings.TrimSpace(publish.ArtifactID) == "" {
		return authoredResultAdmissionTuple{}, false
	}
	qualityRecord, qualityOK := app.osArtifactByID(quality.ArtifactID)
	publishRecord, publishOK := app.osArtifactByID(publish.ArtifactID)
	if !qualityOK || !publishOK || qualityRecord.Metadata["processStage"] != "quality_gate" || publishRecord.Metadata["processStage"] != "ship_compile" ||
		strings.TrimSpace(qualityRecord.Metadata["goalParentId"]) != strings.TrimSpace(parentID) || strings.TrimSpace(publishRecord.Metadata["goalParentId"]) != strings.TrimSpace(parentID) {
		return authoredResultAdmissionTuple{}, false
	}
	id := strings.TrimSpace(qualityRecord.Metadata["reviewedDeckArtifactId"])
	version, err := strconv.Atoi(strings.TrimSpace(qualityRecord.Metadata["reviewedDeckArtifactVersion"]))
	digest := strings.TrimSpace(qualityRecord.Metadata["reviewedDeckContentDigest"])
	if err != nil || version < 1 || id == "" || digest == "" || strings.TrimSpace(qualityRecord.Metadata["slideJuryArtifactId"]) == "" ||
		strings.TrimSpace(qualityRecord.Metadata["slideJuryArtifactDigest"]) == "" ||
		strings.TrimSpace(publishRecord.Metadata["shipArtifactIds"]) != id || strings.TrimSpace(publishRecord.Metadata["deckArtifactId"]) != id {
		return authoredResultAdmissionTuple{}, false
	}
	return authoredResultAdmissionTuple{ArtifactID: id, ArtifactVersion: version, CapabilityDigest: digest}, true
}

func documentReportAdmissionTuple(app *kanbanBoardApp, plan goalPlan, parentID string) (authoredResultAdmissionTuple, bool) {
	if app == nil || plan.ProcessID != documentReportProcessID || plan.State != goalStateVerified {
		return authoredResultAdmissionTuple{}, false
	}
	publish := plan.subtaskByID(documentReportPublishStageID)
	if publish == nil || publish.Status != subtaskComplete || strings.TrimSpace(publish.ArtifactID) == "" {
		return authoredResultAdmissionTuple{}, false
	}
	record, ok := app.osArtifactByID(publish.ArtifactID)
	if !ok || record.Metadata["processStage"] != documentReportPublishStageID || record.Metadata["published"] != "true" || strings.TrimSpace(record.Metadata["goalParentId"]) != strings.TrimSpace(parentID) {
		return authoredResultAdmissionTuple{}, false
	}
	version, err := strconv.Atoi(strings.TrimSpace(record.Metadata["documentArtifactVersion"]))
	tuple := authoredResultAdmissionTuple{
		ArtifactID:       strings.TrimSpace(record.Metadata["documentArtifactId"]),
		ArtifactVersion:  version,
		ContentDigest:    strings.TrimSpace(record.Metadata["documentContentDigest"]),
		CapabilityDigest: strings.TrimSpace(record.Metadata["documentCapabilityDigest"]),
	}
	if err != nil || tuple.ArtifactVersion < 1 || tuple.ArtifactID == "" || tuple.ContentDigest == "" || tuple.CapabilityDigest == "" || strings.TrimSpace(record.Metadata["renderPdfAssetRef"]) == "" || strings.TrimSpace(record.Metadata["documentJuryArtifactId"]) == "" {
		return authoredResultAdmissionTuple{}, false
	}
	return tuple, true
}

func (app *kanbanBoardApp) authoredGoalResultQuality(plan goalPlan, parentID string, result meetingMemoryEntry) string {
	if plan.State == goalStateBlocked {
		return authoredResultQualityDraftNeedsAttention
	}
	if plan.State != goalStateVerified {
		return authoredResultQualityDraftNeedsAttention
	}

	if artifactType(result) == artifactTypeHTMLDeck && artifactIsHTMLDocument(result) {
		// Human acceptance is useful workflow context, but it is not rendered
		// admission. Only the deterministic jury/publication binding below may
		// earn Present/final-export capabilities.
		review, err := resolvePublishedPackagingStudioQuality(app, &plan, parentID)
		if err == nil && review.DeckID == result.ID && review.DeckVersion == artifactVersion(result) && strings.EqualFold(review.DeckDigest, artifactCapabilityDigest(result)) {
			return authoredResultQualityAdmitted
		}
		if tuple, ok := packagingStudioAdmissionTuple(app, plan, parentID); ok && strings.TrimSpace(tuple.ArtifactID) == strings.TrimSpace(result.ID) && !tuple.identifies(result) {
			return authoredResultQualityEditedAfterAdmission
		}
		return authoredResultQualityDraftNeedsAttention
	}

	if artifactType(result) == artifactTypeMarkdown {
		review, err := resolvePublishedDocumentReportQuality(app, &plan, parentID)
		if err == nil && review.ArtifactID == result.ID && review.ArtifactVersion == artifactVersion(result) && strings.EqualFold(review.ContentDigest, documentReportBodyDigest(result)) && strings.EqualFold(review.CapabilityDigest, artifactCapabilityDigest(result)) {
			return authoredResultQualityAdmitted
		}
		if tuple, ok := documentReportAdmissionTuple(app, plan, parentID); ok && strings.TrimSpace(tuple.ArtifactID) == strings.TrimSpace(result.ID) && !tuple.identifies(result) {
			return authoredResultQualityEditedAfterAdmission
		}
	}
	return authoredResultQualityDraftNeedsAttention
}

// authoredResultQualityForArtifact is the direct Studio/read-boundary mirror
// of the chat projection. It lets a deep link re-check the current goal and
// exact artifact revision instead of trusting a capability copied into an old
// client message. An empty state means the artifact is not managed by an
// authored goal; those pre-existing standalone files retain their ordinary
// editor behavior instead of being retroactively put behind this admission
// contract.
func (app *kanbanBoardApp) authoredResultQualityForArtifact(result meetingMemoryEntry) string {
	if app == nil || strings.TrimSpace(result.ID) == "" {
		return ""
	}
	parentID := strings.TrimSpace(firstNonEmptyString(result.Metadata["goalId"], result.Metadata["goalParentId"]))
	if parentID == "" {
		return ""
	}
	parent, ok := app.osArtifactByID(parentID)
	if !ok {
		return authoredResultQualityDraftNeedsAttention
	}
	plan, ok := decodeGoalPlan(parent.Metadata["goalPlan"])
	if !ok {
		return authoredResultQualityDraftNeedsAttention
	}
	return app.authoredGoalResultQuality(plan, parentID, result)
}

func (app *kanbanBoardApp) requireFinalExportAdmission(result meetingMemoryEntry) error {
	qualityState := app.authoredResultQualityForArtifact(result)
	if qualityState != "" && qualityState != authoredResultQualityAdmitted {
		return fmt.Errorf("this authored draft must pass review before final export")
	}
	return nil
}

// reviewEditedAuthoredResult reopens only the deterministic render/admission
// tail for a human-edited result. It never sends the request back through the
// writer and therefore cannot overwrite a normal Studio save with an older
// generated draft. The exact edited revision becomes the input to a fresh
// render, jury, and publication decision.
func (app *kanbanBoardApp) reviewEditedAuthoredResult(parentSnapshot, resultSnapshot meetingMemoryEntry, reviewedBy string) (scoutAgentThread, error) {
	if app == nil || app.memory == nil {
		return scoutAgentThread{}, fmt.Errorf("artifacts are unavailable")
	}
	parentID := strings.TrimSpace(parentSnapshot.ID)
	resultID := strings.TrimSpace(resultSnapshot.ID)
	if parentID == "" || resultID == "" || parentSnapshot.Metadata["mode"] != "goal" {
		return scoutAgentThread{}, fmt.Errorf("an authored goal and edited result are required")
	}
	lock := goalEngineLock(parentID)
	if !lock.TryLock() {
		return scoutAgentThread{}, fmt.Errorf("the goal is busy right now — wait for it to park or finish, then review the changes")
	}
	defer lock.Unlock()

	expectedParentHeader := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(parentSnapshot))
	parent, ok := app.memory.artifactSnapshotIfHeaderMatches(parentID, expectedParentHeader)
	if !ok {
		return scoutAgentThread{}, fmt.Errorf("goal artifact not found")
	}
	result, ok := app.osArtifactByID(resultID)
	if !ok || artifactVersion(result) != artifactVersion(resultSnapshot) || !strings.EqualFold(artifactCapabilityDigest(result), artifactCapabilityDigest(resultSnapshot)) ||
		!artifactAuthorizationHeaderEqual(resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(resultSnapshot)), resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(result))) {
		return scoutAgentThread{}, fmt.Errorf("the edited result changed — reopen it before starting review")
	}
	if strings.TrimSpace(firstNonEmptyString(result.Metadata["goalId"], result.Metadata["goalParentId"])) != parentID {
		return scoutAgentThread{}, fmt.Errorf("the edited result does not belong to this goal")
	}
	plan, ok := decodeGoalPlan(parent.Metadata["goalPlan"])
	if !ok {
		return scoutAgentThread{}, fmt.Errorf("goal plan not found")
	}
	if app.authoredGoalResultQuality(plan, parentID, result) != authoredResultQualityEditedAfterAdmission {
		return scoutAgentThread{}, fmt.Errorf("only an edited, previously admitted result can start changes review")
	}
	engine := newGoalEngine(app)
	engine.expectedPersistHeader = &expectedParentHeader
	engine.expectedPersistBody = parent.Text
	if err := engine.prepareGoalRoute(&plan, parentID); err != nil {
		return scoutAgentThread{}, fmt.Errorf("saved goal route is unavailable: %w", err)
	}

	reviewReason := "Studio changes by " + firstNonEmptyString(strings.TrimSpace(reviewedBy), "an authorized editor") + " require fresh rendered admission"
	switch plan.ProcessID {
	case packagingStudioProcessID:
		if artifactType(result) != artifactTypeHTMLDeck || !artifactIsHTMLDocument(result) {
			return scoutAgentThread{}, fmt.Errorf("the edited presentation is unavailable")
		}
		producer := plan.subtaskByID("ship_deck")
		render := plan.subtaskByID("draft_compile")
		if producer == nil || render == nil {
			return scoutAgentThread{}, fmt.Errorf("the presentation review stages are unavailable")
		}
		// The edited canonical deck is now the producer input. draft_compile may
		// persist it in place and enqueue a fresh render without regenerating it.
		producer.Status = subtaskComplete
		producer.ArtifactID = result.ID
		producer.Review = &goalSubtaskReview{Verdict: goalReviewPass, Reasons: reviewReason, By: "studio_review_changes"}
		resetGoalDependentsWithEvidence(&plan, producer.ID, "", "studio_review_changes", reviewReason)
		render.Status = subtaskReady
		render.Review = &goalSubtaskReview{Verdict: goalReviewRevise, Reasons: reviewReason, By: "studio_review_changes"}
	case documentReportProcessID:
		if artifactType(result) != artifactTypeMarkdown {
			return scoutAgentThread{}, fmt.Errorf("the edited document is unavailable")
		}
		write := plan.subtaskByID("write")
		textGate := plan.subtaskByID("quality_gate")
		render := plan.subtaskByID(documentReportDraftRenderStageID)
		if write == nil || textGate == nil || render == nil || textGate.Status != subtaskComplete {
			return scoutAgentThread{}, fmt.Errorf("the document review stages are unavailable")
		}
		write.Status = subtaskComplete
		write.ArtifactID = result.ID
		write.Review = &goalSubtaskReview{Verdict: goalReviewPass, Reasons: reviewReason, By: "studio_review_changes"}
		resetGoalDependentsWithEvidence(&plan, textGate.ID, "", "studio_review_changes", reviewReason)
		render.Status = subtaskReady
		render.Review = &goalSubtaskReview{Verdict: goalReviewRevise, Reasons: reviewReason, By: "studio_review_changes"}
	default:
		return scoutAgentThread{}, fmt.Errorf("this authored result has no deterministic changes-review route")
	}

	plan.Report.AcceptedResultArtifactID = ""
	plan.Report.AcceptedResultArtifactVersion = 0
	plan.Report.AcceptedResultArtifactDigest = ""
	plan.Gate = goalGate{Status: "pending"}
	plan.Verification = goalVerification{Verdict: "pending"}
	plan.Checkpoint = nil
	plan.MaxProgress = 0
	plan.Blocker = ""
	plan.State = goalStateExecute
	engine.applyProcessBudgets(&plan)
	updated := engine.persist(&plan, parentID, composeGoalArtifact(&plan))
	if engine.conditionalPersistFailed || strings.TrimSpace(updated.ID) == "" {
		return scoutAgentThread{}, fmt.Errorf("goal artifact changed — reopen it before starting review")
	}
	query := compactAssistantLine(firstNonEmptyString(plan.Objective, updated.Metadata["title"]))
	thread := scoutAgentThread{ID: firstNonEmptyString(strings.TrimSpace(updated.Metadata["threadId"]), parentID), Mode: "goal", Query: query, Status: "running", Artifact: updated, Actions: app.osAssistantActions(query, "goal", updated)}
	startGoalFeedbackResumeAsync(func() { app.runGoalThread(parentID) })
	return thread, nil
}
