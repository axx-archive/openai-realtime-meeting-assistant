package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
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
	_, canExport, stable := app.authoredResultFinalExportState(result)
	if !stable {
		return fmt.Errorf("the artifact review changed; retry from the current revision")
	}
	if !canExport {
		return fmt.Errorf("this authored draft must pass review before final export")
	}
	return nil
}

// authoredResultFinalExportState evaluates publication against one exact
// artifact revision. Goal-managed results share the goal engine's mutation
// fence so a concurrent drive cannot move the review plan while the admission
// tuple is being resolved. Callers fail closed when the fence is busy or the
// artifact header changes; no read path is allowed to wait behind a long run.
func (app *kanbanBoardApp) authoredResultFinalExportState(result meetingMemoryEntry) (qualityState string, canExport bool, stable bool) {
	if app == nil || app.memory == nil || strings.TrimSpace(result.ID) == "" {
		return "", false, false
	}
	parentID := strings.TrimSpace(firstNonEmptyString(result.Metadata["goalId"], result.Metadata["goalParentId"]))
	// A goal-owned result is managed even while its goal mutation fence is
	// busy. Return the conservative managed state on every unstable exit so a
	// caller can never mistake contention for a legacy standalone artifact.
	if parentID != "" {
		qualityState = authoredResultQualityDraftNeedsAttention
	}
	if parentID != "" {
		lock := goalEngineLock(parentID)
		if !lock.TryLock() {
			return qualityState, false, false
		}
		defer lock.Unlock()
	}
	return app.authoredResultFinalExportStateUnderGoalFence(result)
}

var authoredAdmissionAfterQualityProbe func()
var authoredCopyAfterAdmissionProbe func()

var authoredAdmissionReferenceMetadataKeys = []string{
	"deckArtifactId",
	"reviewedDeckArtifactId",
	"slideJuryArtifactId",
	"documentArtifactId",
	"reviewedDocumentArtifactId",
	"documentJuryArtifactId",
	"imageryBoardArtifactId",
}

func authoredAdmissionReferencedArtifactIDs(entry meetingMemoryEntry) []string {
	ids := make([]string, 0, len(authoredAdmissionReferenceMetadataKeys)+2)
	for _, key := range authoredAdmissionReferenceMetadataKeys {
		if id := strings.TrimSpace(entry.Metadata[key]); id != "" {
			ids = append(ids, id)
		}
	}
	for _, id := range strings.Split(entry.Metadata["shipArtifactIds"], ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return uniqueSortedStrings(ids)
}

// authoredAdmissionWitness captures every artifact record that can influence
// the rendered admission decision. The plan's stage records are the roots;
// their server-owned artifact references add the jury/scoreboard and exact
// deliverable records. A final recheck of this witness turns independent
// store reads inside the quality resolver into one optimistic snapshot.
type authoredAdmissionWitnessRecord struct {
	Header       ArtifactAuthorizationHeader
	RecordDigest string
}

// authoredAdmissionRecordDigest deliberately binds the complete persisted
// record, not only the authorization header. Quality resolvers read
// server-owned metadata (jury verdicts, thresholds, artifact references, and
// render receipts) that is not capability-bearing on its own. Hashing the
// full snapshot makes a metadata-only edit during evaluation invalidate the
// optimistic admission decision just as a body or ACL edit would.
func authoredAdmissionRecordDigest(entry meetingMemoryEntry) string {
	raw, err := json.Marshal(struct {
		ID       string            `json:"id"`
		Kind     string            `json:"kind"`
		Text     string            `json:"text"`
		Metadata map[string]string `json:"metadata"`
	}{
		ID:       strings.TrimSpace(entry.ID),
		Kind:     entry.Kind,
		Text:     entry.Text,
		Metadata: entry.Metadata,
	})
	if err != nil {
		return ""
	}
	return sha256Hex(raw)
}

func (app *kanbanBoardApp) authoredAdmissionWitness(plan goalPlan, parentID string, result meetingMemoryEntry) (map[string]authoredAdmissionWitnessRecord, bool) {
	if app == nil || app.memory == nil {
		return nil, false
	}
	queued := []string{strings.TrimSpace(parentID), strings.TrimSpace(result.ID)}
	for _, subtask := range plan.Subtasks {
		if id := strings.TrimSpace(subtask.ArtifactID); id != "" {
			queued = append(queued, id)
		}
	}
	witness := map[string]authoredAdmissionWitnessRecord{}
	for len(queued) > 0 {
		id := strings.TrimSpace(queued[0])
		queued = queued[1:]
		if id == "" {
			continue
		}
		if _, seen := witness[id]; seen {
			continue
		}
		if len(witness) >= 256 {
			return nil, false
		}
		header, found := app.memory.artifactAuthorizationHeaderByID(id)
		if !found {
			// A missing stage/evidence record cannot contribute to admission.
			// Leave it out; the deterministic resolver will return draft.
			continue
		}
		entry, exact := app.memory.artifactSnapshotIfHeaderMatches(id, header)
		if !exact {
			return nil, false
		}
		recordDigest := authoredAdmissionRecordDigest(entry)
		if recordDigest == "" {
			return nil, false
		}
		witness[id] = authoredAdmissionWitnessRecord{Header: header, RecordDigest: recordDigest}
		queued = append(queued, authoredAdmissionReferencedArtifactIDs(entry)...)
	}
	if _, parentFound := witness[strings.TrimSpace(parentID)]; !parentFound {
		return nil, false
	}
	if _, resultFound := witness[strings.TrimSpace(result.ID)]; !resultFound {
		return nil, false
	}
	return witness, true
}

func (app *kanbanBoardApp) authoredAdmissionWitnessCurrent(witness map[string]authoredAdmissionWitnessRecord) bool {
	if app == nil || app.memory == nil || len(witness) == 0 {
		return false
	}
	ids := make([]string, 0, len(witness))
	for id := range witness {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		expected := witness[id]
		current, found := app.memory.artifactAuthorizationHeaderByID(id)
		if !found || !artifactAuthorizationHeaderEqual(expected.Header, current) {
			return false
		}
		entry, exact := app.memory.artifactSnapshotIfHeaderMatches(id, expected.Header)
		if !exact || expected.RecordDigest == "" || !strings.EqualFold(expected.RecordDigest, authoredAdmissionRecordDigest(entry)) {
			return false
		}
	}
	return true
}

// authoredResultFinalExportStateUnderGoalFence evaluates one result while the
// caller owns goalEngineLock(parentID) for managed artifacts. It also binds
// every independent evidence record read by the quality resolver, so a jury
// or publication-record edit cannot race through as an admitted snapshot.
func (app *kanbanBoardApp) authoredResultFinalExportStateUnderGoalFence(result meetingMemoryEntry) (qualityState string, canExport bool, stable bool) {
	if app == nil || app.memory == nil || strings.TrimSpace(result.ID) == "" {
		return "", false, false
	}
	expectedHeader := app.resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(result))
	parentID := strings.TrimSpace(firstNonEmptyString(result.Metadata["goalId"], result.Metadata["goalParentId"]))
	if parentID != "" {
		qualityState = authoredResultQualityDraftNeedsAttention
	}

	current, ok := app.memory.artifactSnapshotIfHeaderMatches(result.ID, expectedHeader)
	if !ok || strings.TrimSpace(firstNonEmptyString(current.Metadata["goalId"], current.Metadata["goalParentId"])) != parentID {
		return qualityState, false, false
	}
	if parentID == "" {
		if _, exact := app.memory.artifactSnapshotIfHeaderMatches(result.ID, expectedHeader); !exact {
			return "", false, false
		}
		return "", true, true
	}
	parentHeader, found := app.memory.artifactAuthorizationHeaderByID(parentID)
	if !found {
		return qualityState, false, true
	}
	parent, exact := app.memory.artifactSnapshotIfHeaderMatches(parentID, parentHeader)
	if !exact {
		return qualityState, false, false
	}
	plan, decoded := decodeGoalPlan(parent.Metadata["goalPlan"])
	if !decoded {
		return qualityState, false, true
	}
	witness, witnessed := app.authoredAdmissionWitness(plan, parentID, current)
	if !witnessed {
		return qualityState, false, false
	}
	qualityState = app.authoredGoalResultQuality(plan, parentID, current)
	if authoredAdmissionAfterQualityProbe != nil {
		authoredAdmissionAfterQualityProbe()
	}
	if !app.authoredAdmissionWitnessCurrent(witness) {
		return authoredResultQualityDraftNeedsAttention, false, false
	}
	return qualityState, qualityState == "" || qualityState == authoredResultQualityAdmitted, true
}

func (app *kanbanBoardApp) authoredResultPublicationReady(result meetingMemoryEntry) bool {
	_, canExport, stable := app.authoredResultFinalExportState(result)
	return stable && canExport
}

// withFinalExportAdmissionOperation keeps the authored goal fence across an
// operation that would create an independent publication capability (for
// example Save a Copy). It evaluates admission immediately before and after
// the operation. Callers that create durable state must roll it back when the
// post-check fails; ordinary authored mutations use the same goal fence.
func (app *kanbanBoardApp) withFinalExportAdmissionOperation(result meetingMemoryEntry, operation func(meetingMemoryEntry) error) error {
	if app == nil || app.memory == nil || operation == nil {
		return fmt.Errorf("artifacts are unavailable")
	}
	parentID := strings.TrimSpace(firstNonEmptyString(result.Metadata["goalId"], result.Metadata["goalParentId"]))
	var unlock func()
	if parentID != "" {
		lock := goalEngineLock(parentID)
		if !lock.TryLock() {
			return fmt.Errorf("the artifact review is busy; try again from the current revision")
		}
		unlock = lock.Unlock
		defer unlock()
	}
	header := app.resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(result))
	current, exact := app.memory.artifactSnapshotIfHeaderMatches(result.ID, header)
	if !exact {
		return fmt.Errorf("the artifact changed; reopen the current revision")
	}
	quality, allowed, stable := app.authoredResultFinalExportStateUnderGoalFence(current)
	if !stable || !allowed || (parentID != "" && quality != authoredResultQualityAdmitted) {
		return fmt.Errorf("review the current authored deliverable before creating an independent copy")
	}
	if authoredCopyAfterAdmissionProbe != nil {
		authoredCopyAfterAdmissionProbe()
	}
	if err := operation(current); err != nil {
		return err
	}
	current, exact = app.memory.artifactSnapshotIfHeaderMatches(result.ID, header)
	if !exact {
		return fmt.Errorf("the artifact changed while the copy was being created")
	}
	quality, allowed, stable = app.authoredResultFinalExportStateUnderGoalFence(current)
	if !stable || !allowed || (parentID != "" && quality != authoredResultQualityAdmitted) {
		return fmt.Errorf("the artifact review changed while the copy was being created")
	}
	return nil
}

func rollbackAuthoredIndependentCopy(app *kanbanBoardApp, artifactID string) {
	artifactID = strings.TrimSpace(artifactID)
	if app == nil || app.memory == nil || artifactID == "" {
		return
	}
	if err := moveFileToFolder(artifactID, ""); err != nil {
		log.Errorf("Rollback copy folder assignment %s failed: %v", artifactID, err)
	}
	_, projection, deleted, err := app.memory.deleteOSArtifactWithProjection(artifactID)
	if projection.token != nil {
		revokeArtifactDeletionProjection(projection)
	}
	if err != nil || !deleted {
		log.Errorf("Rollback independent copy %s failed: deleted=%t err=%v", artifactID, deleted, err)
	}
}

func (app *kanbanBoardApp) authoredArtifactShareEligible(result meetingMemoryEntry) bool {
	return artifactShareEligible(result) && app.authoredResultPublicationReady(result)
}

// authoredResultFinalExportCapabilityHandler is the small, revision-bound
// publication seam used by generic Intelligence views. Those views can expose
// assets without opening a Studio, so they must not infer publication rights
// from persisted artifact metadata or from a stale channel message. Presenting
// is a read experience; downloading is a separate export authority. Unmanaged
// artifacts retain their historical behavior; authored goal results require
// the exact current rendered admission for either action.
func authoredResultFinalExportCapabilityHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil || kanbanApp.memory == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "artifacts are unavailable")
		return
	}
	artifactID := strings.TrimSpace(r.URL.Query().Get("id"))
	artifact, ok := authorizedArtifactForActions(r.Context(), user, artifactID, ACLReadContent)
	if !ok {
		writeAuthError(w, http.StatusNotFound, "artifact not found")
		return
	}
	_, canExportAuthority := authorizedArtifactForActions(r.Context(), user, artifactID, ACLReadContent, ACLExport)
	qualityState, publicationReady, stable := kanbanApp.authoredResultFinalExportState(artifact)
	if !stable {
		writeAuthError(w, http.StatusConflict, "artifact revision changed")
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"artifactId":      artifact.ID,
		"artifactVersion": artifactVersion(artifact),
		"qualityState":    qualityState,
		"managed":         qualityState != "",
		"canPresent":      publicationReady,
		"canExport":       publicationReady && canExportAuthority,
	})
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

	expectedParentHeader := app.resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(parentSnapshot))
	parent, ok := app.memory.artifactSnapshotIfHeaderMatches(parentID, expectedParentHeader)
	if !ok {
		return scoutAgentThread{}, fmt.Errorf("goal artifact not found")
	}
	result, ok := app.osArtifactByID(resultID)
	if !ok || artifactVersion(result) != artifactVersion(resultSnapshot) || !strings.EqualFold(artifactCapabilityDigest(result), artifactCapabilityDigest(resultSnapshot)) ||
		!artifactAuthorizationHeaderEqual(app.resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(resultSnapshot)), app.resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(result))) {
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
	if err := packagingStudioHistoricalRunError(&plan); err != nil {
		return scoutAgentThread{}, err
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
