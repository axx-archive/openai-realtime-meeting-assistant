package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	studioProjectSchemaVersion = 1
	studioProjectTitleMaxRunes = 160
	studioProjectDefaultLimit  = 100
	studioProjectMaxLimit      = 200

	studioProjectKindPresentation = "presentation"
	studioProjectKindDocument     = "document"

	studioProjectStatusQueued         = "queued"
	studioProjectStatusRunning        = "running"
	studioProjectStatusNeedsInput     = "needs_input"
	studioProjectStatusNeedsAttention = "needs_attention"
	studioProjectStatusReady          = "ready"
	studioProjectStatusStopped        = "stopped"
)

// studioProjectView is a read projection over one existing goal root. It is
// intentionally not another Project record or work-state store: the root goal
// remains the sole lifecycle authority, while the exact viewer-authorized
// deliverable tuple remains the sole editor/presenter/export doorway.
type studioProjectView struct {
	SchemaVersion   int                         `json:"schemaVersion"`
	ID              string                      `json:"id"`
	Kind            string                      `json:"kind"`
	Title           string                      `json:"title"`
	Revision        int                         `json:"revision"`
	Status          string                      `json:"status"`
	ProgressPercent int                         `json:"progressPercent"`
	Phase           string                      `json:"phase"`
	Phases          []studioProjectPhaseView    `json:"phases"`
	CreatedAt       string                      `json:"createdAt"`
	UpdatedAt       string                      `json:"updatedAt"`
	RootRunID       string                      `json:"rootRunId"`
	RootArtifactID  string                      `json:"rootArtifactId"`
	Href            string                      `json:"href"`
	Source          *studioProjectSourceRef     `json:"source,omitempty"`
	CompanyProject  *studioCompanyProjectRef    `json:"companyProject,omitempty"`
	Result          *studioProjectResultRef     `json:"result,omitempty"`
	Checkpoint      *scoutChatWorkCheckpointRef `json:"checkpoint,omitempty"`
	Attention       *studioProjectAttentionView `json:"attention,omitempty"`
	CanRename       bool                        `json:"canRename"`
}

type studioProjectPhaseView struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
}

// studioProjectAttentionView is intentionally viewer-safe. Raw goal blockers
// can contain provider diagnostics, source titles, or implementation details;
// the Studio only needs to explain what kind of recovery stopped and what the
// viewer can do next.
type studioProjectAttentionView struct {
	Title       string `json:"title"`
	Body        string `json:"body,omitempty"`
	ActionLabel string `json:"actionLabel,omitempty"`
}

type studioProjectSourceRef struct {
	ThreadID string `json:"threadId"`
	Kind     string `json:"kind,omitempty"`
}

type studioCompanyProjectRef struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

type studioProjectSourceAccessCache struct {
	checked map[string]bool
	refs    map[string]*studioProjectSourceRef
}

var studioProjectSourceAuthorizationProbe func(string)

type studioProjectCompanyAccessCache struct {
	checked map[string]bool
	refs    map[string]*studioCompanyProjectRef
}

var studioProjectCompanyAuthorizationProbe func(string)

func newStudioProjectSourceAccessCache() *studioProjectSourceAccessCache {
	return &studioProjectSourceAccessCache{checked: map[string]bool{}, refs: map[string]*studioProjectSourceRef{}}
}

func newStudioProjectCompanyAccessCache() *studioProjectCompanyAccessCache {
	return &studioProjectCompanyAccessCache{checked: map[string]bool{}, refs: map[string]*studioCompanyProjectRef{}}
}

func studioProjectSourceForViewer(app *kanbanBoardApp, viewer *userAccount, sourceID, sourceKind string, cache *studioProjectSourceAccessCache) *studioProjectSourceRef {
	sourceID, sourceKind = strings.TrimSpace(sourceID), strings.TrimSpace(sourceKind)
	if app == nil || viewer == nil || sourceID == "" || !oneOf(sourceKind, agentThreadOriginPrivateThread, agentThreadOriginChannel) {
		return nil
	}
	key := sourceKind + ":" + sourceID
	if cache != nil && cache.checked[key] {
		if ref := cache.refs[key]; ref != nil {
			copy := *ref
			return &copy
		}
		return nil
	}
	if cache != nil {
		cache.checked[key] = true
	}
	if studioProjectSourceAuthorizationProbe != nil {
		studioProjectSourceAuthorizationProbe(key)
	}
	thread, _, err := app.scoutChatThreadByID(viewer.Email, sourceID)
	if err != nil {
		return nil
	}
	actualKind := agentThreadOriginPrivateThread
	if thread.Visibility == scoutChatVisibilityPublic {
		actualKind = agentThreadOriginChannel
	}
	if actualKind != sourceKind {
		return nil
	}
	ref := &studioProjectSourceRef{ThreadID: sourceID, Kind: sourceKind}
	if cache != nil {
		cache.refs[key] = ref
	}
	copy := *ref
	return &copy
}

// studioCompanyProjectForViewer treats the Company Project as a separate
// authorization domain. A readable Studio root is only a hint: its affinity
// must still be current, and the referenced project thread must be an exact,
// eligible destination that this viewer can read right now.
func studioCompanyProjectForViewer(ctx context.Context, app *kanbanBoardApp, viewer *userAccount, entry meetingMemoryEntry, cache *studioProjectCompanyAccessCache) *studioCompanyProjectRef {
	projectID := strings.TrimSpace(entry.Metadata["projectWorkId"])
	projectTitle := strings.TrimSpace(entry.Metadata["projectWorkTitle"])
	requester := accountStore().findUser(normalizeAccountEmail(entry.Metadata["requestedBy"]))
	if app == nil || viewer == nil || requester == nil || projectID == "" || projectTitle == "" {
		return nil
	}
	currentID, currentTitle, _, current := app.currentWorkstreamSupport(ctx, requester, entry, 0)
	if !current || currentID != projectID || currentTitle != projectTitle {
		return nil
	}
	key := projectID + "\x00" + projectTitle
	if cache != nil && cache.checked[key] {
		if ref := cache.refs[key]; ref != nil {
			copy := *ref
			return &copy
		}
		return nil
	}
	if cache != nil {
		cache.checked[key] = true
	}
	if studioProjectCompanyAuthorizationProbe != nil {
		studioProjectCompanyAuthorizationProbe(key)
	}
	project, _, err := app.scoutChatThreadByID(viewer.Email, projectID)
	if err != nil || project.ArchivedAt != "" || !strideProductProjectDestinationEligible(project) ||
		!scoutChatThreadAllowsViewer(project, viewer.Email) || strings.TrimSpace(project.Title) != projectTitle {
		return nil
	}
	ref := &studioCompanyProjectRef{ID: project.ID, Title: strings.TrimSpace(project.Title)}
	if cache != nil {
		cache.refs[key] = ref
	}
	copy := *ref
	return &copy
}

type studioProjectResultRef struct {
	ArtifactID    string                      `json:"artifactId"`
	Type          string                      `json:"type"`
	Version       int                         `json:"version"`
	Digest        string                      `json:"digest"`
	Title         string                      `json:"title"`
	Preview       string                      `json:"preview,omitempty"`
	Assets        []scoutChatResultAssetRef   `json:"assets,omitempty"`
	Table         *scoutChatResultTableRef    `json:"table,omitempty"`
	Workbook      *scoutChatResultWorkbookRef `json:"workbook,omitempty"`
	ApprovalState string                      `json:"approvalState,omitempty"`
	QualityState  string                      `json:"qualityState,omitempty"`
	ReviewManaged bool                        `json:"reviewManaged"`
	CanEdit       bool                        `json:"canEdit"`
	CanContinue   bool                        `json:"canContinue"`
	CanPresent    bool                        `json:"canPresent"`
	CanExport     bool                        `json:"canExport"`
}

// scoutChatStudioProjectRef is the quiet conversation receipt. It is
// synthesized at the viewer seam and never persisted as a second lifecycle.
type scoutChatStudioProjectRef struct {
	ID              string                      `json:"id"`
	Kind            string                      `json:"kind"`
	Title           string                      `json:"title"`
	Status          string                      `json:"status"`
	ProgressPercent int                         `json:"progressPercent"`
	Phase           string                      `json:"phase"`
	Href            string                      `json:"href"`
	Checkpoint      *scoutChatWorkCheckpointRef `json:"checkpoint,omitempty"`
	Attention       *studioProjectAttentionView `json:"attention,omitempty"`
}

func studioProjectKindForProcessID(processID string) string {
	switch strings.TrimSpace(processID) {
	case packagingStudioProcessID:
		return studioProjectKindPresentation
	case documentReportProcessID:
		return studioProjectKindDocument
	default:
		return ""
	}
}

// studioProjectCandidate recognizes only a canonical root goal. Titles,
// query prose, filenames and child artifact types are deliberately excluded
// from classification so a stage output can never masquerade as a project.
func studioProjectCandidate(entry meetingMemoryEntry) (string, goalPlan, bool) {
	metadata := entry.Metadata
	if entry.Kind != meetingMemoryKindOSArtifact || strings.TrimSpace(metadata["source"]) != "goal_thread" ||
		strings.TrimSpace(metadata["mode"]) != "goal" || strings.TrimSpace(metadata["goalParentId"]) != "" {
		return "", goalPlan{}, false
	}
	plan, ok := decodeGoalPlan(metadata["goalPlan"])
	if !ok || strings.TrimSpace(plan.GoalID) == "" || strings.TrimSpace(plan.GoalID) != strings.TrimSpace(metadata["threadId"]) ||
		strings.TrimSpace(plan.ProcessID) != strings.TrimSpace(metadata["processId"]) {
		return "", goalPlan{}, false
	}
	kind := studioProjectKindForProcessID(plan.ProcessID)
	return kind, plan, kind != ""
}

// studioLegacyProjectCandidate keeps pre-Studio authored work reachable
// without rewriting production history or inventing a second lifecycle store.
// The migration is deliberately typed and fail closed: old Design, Grill,
// Workflow, generic Artifacts, process children, and ordinary Files stay out
// of the Research library even when their persisted body happens to be
// Markdown. A native named deck copy is the one threadless exception because
// its scene/copy metadata is a server-owned presentation identity.
func studioLegacyProjectCandidate(entry meetingMemoryEntry) (string, bool) {
	if _, _, canonical := studioProjectCandidate(entry); canonical {
		return "", false
	}
	metadata := entry.Metadata
	if entry.Kind != meetingMemoryKindOSArtifact || strings.TrimSpace(metadata["goalParentId"]) != "" ||
		strings.TrimSpace(metadata["processStage"]) != "" || strings.TrimSpace(metadata["source"]) != "scout_thread" {
		return "", false
	}
	mode := strings.ToLower(strings.TrimSpace(metadata["mode"]))
	threadID := strings.TrimSpace(metadata["threadId"])
	switch artifactType(entry) {
	case artifactTypeHTMLDeck:
		threadedPresentation := threadID != "" && oneOf(mode, "artifacts", "presentation", "deck", "slides")
		nativeNamedCopy := threadID == "" && mode == "artifacts" && validBlobRef(strings.TrimSpace(metadata[deckSceneRefMetadataKey])) &&
			strings.TrimSpace(metadata["copiedFromArtifactId"]) != ""
		return studioProjectKindPresentation, threadedPresentation || nativeNamedCopy
	case artifactTypeMarkdown:
		return studioProjectKindDocument, threadID != "" && oneOf(mode, "research", "document", "report")
	default:
		return "", false
	}
}

func studioProjectClassification(entry meetingMemoryEntry) (kind string, plan goalPlan, canonical bool, ok bool) {
	if kind, plan, canonical = studioProjectCandidate(entry); canonical {
		return kind, plan, true, true
	}
	kind, legacy := studioLegacyProjectCandidate(entry)
	return kind, goalPlan{}, false, legacy
}

func studioProjectProjectionRelevantEntry(entry meetingMemoryEntry) bool {
	if entry.Kind != meetingMemoryKindOSArtifact {
		return false
	}
	if _, _, ok := studioProjectCandidate(entry); ok {
		return true
	}
	if _, ok := studioLegacyProjectCandidate(entry); ok {
		return true
	}
	metadata := entry.Metadata
	if strings.TrimSpace(metadata["processStage"]) == "ship_compile" && strings.TrimSpace(metadata["goalParentId"]) != "" {
		return true
	}
	typeName := artifactType(entry)
	if typeName == artifactTypeHTMLDeck {
		return (strings.TrimSpace(metadata["source"]) == "packaging_studio_ship" && strings.TrimSpace(metadata["goalId"]) != "") ||
			(strings.TrimSpace(metadata["source"]) == "scout_thread" && strings.TrimSpace(metadata["goalParentId"]) != "")
	}
	return typeName == artifactTypeMarkdown && strings.TrimSpace(metadata["source"]) == "scout_thread" &&
		strings.TrimSpace(metadata["goalParentId"]) != "" && strings.EqualFold(strings.TrimSpace(metadata["goalDeliverable"]), "true")
}

func boundedStudioProjectTitle(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	runes := []rune(value)
	if len(runes) > studioProjectTitleMaxRunes {
		value = strings.TrimSpace(string(runes[:studioProjectTitleMaxRunes-1])) + "…"
	}
	return value
}

func studioProjectTitle(entry meetingMemoryEntry, plan goalPlan) string {
	return firstNonEmptyString(
		boundedStudioProjectTitle(entry.Metadata["studioTitle"]),
		boundedStudioProjectTitle(entry.Metadata["title"]),
		boundedStudioProjectTitle(entry.Metadata["threadQuery"]),
		boundedStudioProjectTitle(plan.Objective),
		"Untitled work",
	)
}

func studioProjectRevision(entry meetingMemoryEntry) int {
	revision, _ := strconv.Atoi(strings.TrimSpace(entry.Metadata["studioRevision"]))
	if revision < 1 {
		return 1
	}
	return revision
}

func studioProjectArchived(entry meetingMemoryEntry) bool {
	return strings.TrimSpace(entry.Metadata["studioArchivedAt"]) != ""
}

func studioProjectCheckpointActionable(checkpoint *scoutChatWorkCheckpointRef) bool {
	if checkpoint == nil || !validGoalCheckpointChoiceID(strings.TrimSpace(checkpoint.ID), "goal-checkpoint-") || strings.TrimSpace(checkpoint.Question) == "" || len(checkpoint.Options) == 0 {
		return false
	}
	for _, option := range checkpoint.Options {
		if !validGoalCheckpointChoiceID(strings.TrimSpace(option.ID), "checkpoint-option-") || strings.TrimSpace(option.Label) == "" || !oneOf(strings.TrimSpace(option.Action), processCheckpointActionProceed, processCheckpointActionRevise, processCheckpointActionHold) {
			return false
		}
	}
	return true
}

func studioProjectCheckpointForViewer(entry meetingMemoryEntry, viewer *userAccount) *scoutChatWorkCheckpointRef {
	checkpoint := scoutChatCheckpointRefForArtifact(entry)
	if viewer == nil || !isArtifactApprovalAdmin(viewer) || !studioProjectCheckpointActionable(checkpoint) {
		return nil
	}
	return checkpoint
}

func studioProjectStatus(entry meetingMemoryEntry, plan goalPlan, hasActionableCheckpoint bool) string {
	if plan.Cancelled {
		return studioProjectStatusStopped
	}
	switch strings.TrimSpace(plan.State) {
	case goalStateApproval:
		if hasActionableCheckpoint {
			return studioProjectStatusNeedsInput
		}
		return studioProjectStatusNeedsAttention
	case goalStateBlocked:
		return studioProjectStatusNeedsAttention
	case goalStateVerified:
		return studioProjectStatusReady
	}
	switch strings.ToLower(strings.TrimSpace(firstNonEmptyString(entry.Metadata["threadStatus"], entry.Metadata["status"]))) {
	case "queued", "pending":
		return studioProjectStatusQueued
	case codexJobStatusApprovalRequired:
		if hasActionableCheckpoint {
			return studioProjectStatusNeedsInput
		}
		return studioProjectStatusNeedsAttention
	case "error", codexJobStatusFailed, "needs_attention":
		return studioProjectStatusNeedsAttention
	case "cancelled", "canceled", "stopped":
		return studioProjectStatusStopped
	case codexJobStatusComplete, "completed", "verified":
		return studioProjectStatusReady
	default:
		return studioProjectStatusRunning
	}
}

func studioProjectProgress(entry meetingMemoryEntry, plan goalPlan) int {
	progress, err := strconv.Atoi(strings.TrimSpace(entry.Metadata["progressPercent"]))
	if err != nil {
		_, _, progress = goalStateDisplay(&plan)
	}
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
}

func studioLegacyProjectProgress(entry meetingMemoryEntry, status string) int {
	if raw := strings.TrimSpace(entry.Metadata["progressPercent"]); raw != "" {
		if progress, err := strconv.Atoi(raw); err == nil {
			if progress < 0 {
				return 0
			}
			if progress > 100 {
				return 100
			}
			return progress
		}
	}
	if status == studioProjectStatusReady {
		return 100
	}
	// Older direct work did not always persist fractional progress. Zero is the
	// only truthful fallback; the Studio must not invent a goal-stage percent.
	return 0
}

func studioProjectPhase(status string, progress int) string {
	if status == studioProjectStatusReady {
		return "ready"
	}
	if status == studioProjectStatusStopped {
		return "stopped"
	}
	switch {
	case progress < 20:
		return "brief"
	case progress < 75:
		return "build"
	default:
		return "polish"
	}
}

func studioProjectPhases(status, current string) []studioProjectPhaseView {
	definitions := []studioProjectPhaseView{
		{ID: "brief", Label: "Brief"},
		{ID: "build", Label: "Build"},
		{ID: "polish", Label: "Polish"},
		{ID: "ready", Label: "Ready"},
	}
	currentIndex := 0
	for index := range definitions {
		if definitions[index].ID == current {
			currentIndex = index
			break
		}
	}
	for index := range definitions {
		switch {
		case status == studioProjectStatusReady:
			definitions[index].Status = "complete"
		case status == studioProjectStatusStopped && index == currentIndex:
			definitions[index].Status = studioProjectStatusStopped
		case index < currentIndex:
			definitions[index].Status = "complete"
		case index > currentIndex:
			definitions[index].Status = "upcoming"
		case status == studioProjectStatusNeedsInput || status == studioProjectStatusNeedsAttention:
			definitions[index].Status = status
		default:
			definitions[index].Status = "active"
		}
	}
	return definitions
}

func studioProjectUpdatedAt(entry meetingMemoryEntry) time.Time {
	for _, raw := range []string{entry.Metadata["studioTitleUpdatedAt"], entry.Metadata["updatedAt"]} {
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw)); err == nil {
			return parsed
		}
	}
	return entry.CreatedAt
}

func studioProjectHref(kind, id string) string {
	return "/work?project=" + url.QueryEscape(strings.TrimSpace(id))
}

func studioProjectAttention(status string, plan goalPlan, resultReady bool) *studioProjectAttentionView {
	if status != studioProjectStatusNeedsAttention {
		return nil
	}
	if strings.TrimSpace(plan.State) == goalStateVerified && !resultReady {
		return &studioProjectAttentionView{
			Title:       "Final file needs reconnecting",
			Body:        "Scout finished the work, but the exact verified file is not available yet.",
			ActionLabel: "Open conversation",
		}
	}
	blocker := strings.ToLower(strings.TrimSpace(plan.Blocker))
	switch {
	case strings.Contains(blocker, "restart"), strings.Contains(blocker, "execution state"), strings.Contains(blocker, "child authority"), strings.Contains(blocker, "saved goal"):
		return &studioProjectAttentionView{
			Title:       "Automatic recovery stopped",
			Body:        "The request was interrupted before Scout could safely resume it.",
			ActionLabel: "Open conversation",
		}
	case strings.Contains(blocker, "source"), strings.Contains(blocker, "authorization"), strings.Contains(blocker, "revoked"), strings.Contains(blocker, "access"):
		return &studioProjectAttentionView{
			Title:       "Source access changed",
			Body:        "Scout can’t verify one of the sources needed to finish this work.",
			ActionLabel: "Review sources",
		}
	case strings.Contains(blocker, "gate"), strings.Contains(blocker, "revision"), strings.Contains(blocker, "evidence"), strings.Contains(blocker, "format"), strings.Contains(blocker, "quality"):
		return &studioProjectAttentionView{
			Title:       "Quality checks didn’t pass",
			Body:        "Scout stopped instead of publishing a result it could not verify.",
			ActionLabel: "Open conversation",
		}
	default:
		return &studioProjectAttentionView{
			Title:       "Scout couldn’t finish automatically",
			Body:        "The last automated attempt stopped before a verified final file was ready.",
			ActionLabel: "Open conversation",
		}
	}
}

func studioProjectStatusCanRename(status string) bool {
	return oneOf(status, studioProjectStatusReady, studioProjectStatusNeedsAttention, studioProjectStatusStopped)
}

func studioProjectCanRename(ctx context.Context, viewer *userAccount, candidate artifactListAuthorizationCandidate, status string) bool {
	return studioProjectStatusCanRename(status) && artifactHeaderAuthorized(ctx, viewer, ACLWrite, candidate.Header)
}

func studioProjectResultReady(result *studioProjectResultRef) bool {
	if result == nil || result.CanContinue {
		return false
	}
	if result.ReviewManaged {
		return result.QualityState == authoredResultQualityAdmitted
	}
	return result.QualityState == ""
}

func studioProjectAuthorizationCandidateCurrent(ctx context.Context, app *kanbanBoardApp, candidate artifactListAuthorizationCandidate) bool {
	if app == nil || app.memory == nil {
		return false
	}
	if artifactAuthorizationAfterCheckProbe != nil {
		artifactAuthorizationAfterCheckProbe()
	}
	current, found := app.memory.artifactAuthorizationHeaderByID(candidate.Header.ObjectID)
	return found && artifactAuthorizationHeaderEqual(candidate.Header, current) && app.projectBoundArtifactCurrent(ctx, candidate.Entry)
}

func studioProjectViewForCandidate(ctx context.Context, viewer *userAccount, candidate artifactListAuthorizationCandidate, resultIndex scoutChatResultProjectionIndex) (studioProjectView, bool) {
	return studioProjectViewForCandidateProjectionWithApp(kanbanApp, ctx, viewer, candidate, resultIndex, nil, nil, true, true)
}

func studioProjectViewForCandidateWithAccessCache(ctx context.Context, viewer *userAccount, candidate artifactListAuthorizationCandidate, resultIndex scoutChatResultProjectionIndex, sourceCache *studioProjectSourceAccessCache, companyCache *studioProjectCompanyAccessCache) (studioProjectView, bool) {
	return studioProjectViewForCandidateProjectionWithApp(kanbanApp, ctx, viewer, candidate, resultIndex, sourceCache, companyCache, true, false)
}

func studioProjectViewForReceipt(app *kanbanBoardApp, ctx context.Context, viewer *userAccount, candidate artifactListAuthorizationCandidate, resultIndex scoutChatResultProjectionIndex) (studioProjectView, bool) {
	return studioProjectViewForCandidateProjectionWithApp(app, ctx, viewer, candidate, resultIndex, nil, nil, false, false)
}

func studioProjectViewForCandidateProjectionWithApp(app *kanbanBoardApp, ctx context.Context, viewer *userAccount, candidate artifactListAuthorizationCandidate, resultIndex scoutChatResultProjectionIndex, sourceCache *studioProjectSourceAccessCache, companyCache *studioProjectCompanyAccessCache, includeContext bool, includeResultPreview bool) (studioProjectView, bool) {
	if app == nil || viewer == nil || !studioProjectAuthorizationCandidateCurrent(ctx, app, candidate) {
		return studioProjectView{}, false
	}
	entry := candidate.Entry
	kind, plan, canonical, ok := studioProjectClassification(entry)
	if !ok {
		return studioProjectView{}, false
	}
	progress := 100
	checkpoint := (*scoutChatWorkCheckpointRef)(nil)
	status := studioProjectStatusReady
	if canonical {
		progress = studioProjectProgress(entry, plan)
		checkpoint = studioProjectCheckpointForViewer(entry, viewer)
		status = studioProjectStatus(entry, plan, checkpoint != nil)
	} else {
		status = studioProjectStatus(entry, goalPlan{}, false)
		progress = studioLegacyProjectProgress(entry, status)
	}
	threadRunID := strings.TrimSpace(entry.Metadata["threadId"])
	rootRunID := firstNonEmptyString(threadRunID, entry.ID)
	view := studioProjectView{
		SchemaVersion: studioProjectSchemaVersion,
		ID:            entry.ID, Kind: kind, Title: studioProjectTitle(entry, plan), Revision: studioProjectRevision(entry),
		Status: status, ProgressPercent: progress,
		CreatedAt: entry.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: studioProjectUpdatedAt(entry).UTC().Format(time.RFC3339Nano),
		RootRunID: rootRunID, RootArtifactID: entry.ID, Href: studioProjectHref(kind, entry.ID),
		Checkpoint: checkpoint,
	}
	// Root-artifact access does not imply source-chat access. The per-request
	// cache also prevents one large shared channel from being decoded N times for
	// N projects in the same Studio list.
	if includeContext {
		view.Source = studioProjectSourceForViewer(app, viewer, entry.Metadata["originId"], entry.Metadata["originKind"], sourceCache)
		view.CompanyProject = studioCompanyProjectForViewer(ctx, app, viewer, entry, companyCache)
	}
	mode := "goal"
	query := plan.Objective
	if !canonical {
		mode = strings.TrimSpace(entry.Metadata["mode"])
		query = strings.TrimSpace(entry.Metadata["threadQuery"])
	}
	message := scoutChatMessageRecord{Thread: &scoutChatThreadRef{
		ID: threadRunID, RootRunID: rootRunID, Mode: mode, ProcessID: plan.ProcessID,
		Query: query, Status: firstNonEmptyString(entry.Metadata["threadStatus"], entry.Metadata["status"]),
		ArtifactID: entry.ID, ProgressPercent: float64(progress), Checkpoint: view.Checkpoint,
		OutputFamily: scoutChatOutputFamilyForArtifact(entry),
	}}
	app.projectScoutChatResultRefWithPreview(ctx, viewer, &message, resultIndex, includeResultPreview)
	if ref := message.Thread; ref != nil && ref.ResultArtifactID != "" {
		view.Result = &studioProjectResultRef{
			ArtifactID: ref.ResultArtifactID, Type: ref.ResultArtifactType, Version: ref.ResultArtifactVersion,
			Digest: ref.ResultArtifactDigest, Title: ref.ResultTitle, Preview: ref.ResultPreview,
			Assets: ref.ResultAssets, Table: ref.ResultTable, Workbook: ref.ResultWorkbook,
			ApprovalState: ref.ResultApprovalState, QualityState: ref.ResultQualityState,
			ReviewManaged: canonical,
			CanEdit:       ref.ResultCanEdit, CanContinue: ref.ResultCanContinue,
			CanPresent: ref.ResultCanPresent, CanExport: ref.ResultCanExport,
		}
	}
	// Completion is a claim about an exact, current, viewer-authorized file, not
	// merely a terminal plan state. If that file is missing, revoked, or drifted,
	// the Studio must ask for attention instead of presenting a false Ready state.
	resultReady := studioProjectResultReady(view.Result)
	if !canonical {
		resultReady = resultReady && view.Result.ArtifactID == entry.ID
	}
	if status == studioProjectStatusReady && !resultReady {
		status = studioProjectStatusNeedsAttention
	}
	if status != studioProjectStatusNeedsInput {
		view.Checkpoint = nil
	}
	phase := studioProjectPhase(status, progress)
	view.Status = status
	view.Phase = phase
	view.Phases = studioProjectPhases(status, phase)
	view.Attention = studioProjectAttention(status, plan, resultReady)
	view.CanRename = studioProjectCanRename(ctx, viewer, candidate, status)
	return view, true
}

func studioProjectProjectionDirectory() ([]artifactListAuthorizationCandidate, scoutChatResultProjectionIndex) {
	if kanbanApp == nil || kanbanApp.memory == nil {
		return nil, scoutChatResultProjectionIndex{}
	}
	candidates := kanbanApp.memory.studioProjectProjectionSnapshot()
	artifacts := make([]meetingMemoryEntry, 0, len(candidates))
	for _, candidate := range candidates {
		artifacts = append(artifacts, candidate.Entry)
	}
	return candidates, kanbanApp.scoutChatResultIndexFromArtifacts(artifacts)
}

func authorizedStudioProjectCandidates(ctx context.Context, viewer *userAccount, candidates []artifactListAuthorizationCandidate) []artifactListAuthorizationCandidate {
	projects := make([]artifactListAuthorizationCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if _, _, _, ok := studioProjectClassification(candidate.Entry); !ok || !artifactHeaderAuthorized(ctx, viewer, ACLReadContent, candidate.Header) || !kanbanApp.projectBoundArtifactCurrent(ctx, candidate.Entry) {
			continue
		}
		projects = append(projects, candidate)
	}
	return projects
}

func studioProjectList(ctx context.Context, viewer *userAccount, kind string) []studioProjectView {
	candidates, index := studioProjectProjectionDirectory()
	sourceCache := newStudioProjectSourceAccessCache()
	companyCache := newStudioProjectCompanyAccessCache()
	views := make([]studioProjectView, 0)
	for _, candidate := range authorizedStudioProjectCandidates(ctx, viewer, candidates) {
		if studioProjectArchived(candidate.Entry) {
			continue
		}
		view, ok := studioProjectViewForCandidateWithAccessCache(ctx, viewer, candidate, index, sourceCache, companyCache)
		if !ok || (kind != "" && view.Kind != kind) {
			continue
		}
		views = append(views, view)
	}
	sort.SliceStable(views, func(left, right int) bool {
		leftTime, _ := time.Parse(time.RFC3339Nano, views[left].UpdatedAt)
		rightTime, _ := time.Parse(time.RFC3339Nano, views[right].UpdatedAt)
		if leftTime.Equal(rightTime) {
			return views[left].ID > views[right].ID
		}
		return leftTime.After(rightTime)
	})
	return views
}

func studioProjectListETag(projects []studioProjectView) string {
	raw, err := json.Marshal(projects)
	if err != nil {
		return ""
	}
	return `"` + sha256Hex(raw) + `"`
}

func studioProjectsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPatch {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	viewer := userFromRequest(r)
	if viewer == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil || kanbanApp.memory == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "work studios are unavailable")
		return
	}

	if r.Method == http.MethodPatch {
		var payload struct {
			ID               string `json:"id"`
			Title            string `json:"title"`
			Archived         *bool  `json:"archived"`
			ExpectedRevision int    `json:"expectedRevision"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&payload); err != nil {
			writeAuthError(w, http.StatusBadRequest, "could not read project update")
			return
		}
		payload.ID = strings.TrimSpace(payload.ID)
		payload.Title = boundedStudioProjectTitle(payload.Title)
		updateCount := 0
		if payload.Title != "" {
			updateCount++
		}
		if payload.Archived != nil {
			updateCount++
		}
		if payload.ID == "" || payload.ExpectedRevision < 1 || updateCount != 1 {
			writeAuthError(w, http.StatusBadRequest, "project id, expected revision, and exactly one title or archived update are required")
			return
		}
		prior, found := authorizedArtifactByID(r.Context(), viewer, ACLWrite, payload.ID)
		kind, plan, canonical, recognized := studioProjectClassification(prior)
		if !found || !recognized || kind == "" {
			writeAuthError(w, http.StatusNotFound, "studio project not found")
			return
		}
		if studioProjectRevision(prior) != payload.ExpectedRevision {
			writeAuthError(w, http.StatusConflict, "project changed; reload and try again")
			return
		}
		status := studioProjectStatus(prior, plan, canonical && studioProjectCheckpointForViewer(prior, viewer) != nil)
		if payload.Title != "" && !studioProjectStatusCanRename(status) {
			writeAuthError(w, http.StatusConflict, "project can be renamed after Scout finishes or stops")
			return
		}
		archiveEligible := status == studioProjectStatusNeedsAttention
		if canonical {
			// Archive authority comes from the durable lifecycle, never from the
			// viewer-specific checkpoint projection. An approval root without an
			// actionable/admin-visible checkpoint may look like needs_attention to
			// this viewer, but it is still genuine approval work and must survive.
			archiveEligible = strings.TrimSpace(plan.State) == goalStateBlocked
		}
		if payload.Archived != nil && *payload.Archived && !archiveEligible {
			writeAuthError(w, http.StatusConflict, "only failed work that needs attention can be archived")
			return
		}
		archiveChanged := payload.Archived != nil && studioProjectArchived(prior) != *payload.Archived
		titleChanged := payload.Title != "" && studioProjectTitle(prior, plan) != payload.Title
		if titleChanged || archiveChanged {
			header := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(prior))
			expectedRawRevision := strings.TrimSpace(prior.Metadata["studioRevision"])
			updates := map[string]string{
				"studioSchemaVersion": strconv.Itoa(studioProjectSchemaVersion),
				"studioRevision":      strconv.Itoa(payload.ExpectedRevision + 1),
			}
			if titleChanged {
				updates["studioTitle"] = payload.Title
				updates["studioTitleUpdatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
				updates["studioTitleUpdatedBy"] = viewer.Name
			}
			if archiveChanged {
				updates["studioArchivedAt"] = ""
				updates["studioArchivedBy"] = ""
				if *payload.Archived {
					updates["studioArchivedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
					updates["studioArchivedBy"] = viewer.Name
				}
			}
			var changed bool
			updateProject := func() error {
				var updateErr error
				_, changed, updateErr = kanbanApp.memory.updateOSArtifactMetadataIfHeaderAndMetadataMatch(
					header, map[string]string{"studioRevision": expectedRawRevision}, prior.ID, updates,
				)
				if updateErr != nil {
					return updateErr
				}
				if !changed {
					return errors.New("studio project changed")
				}
				return nil
			}
			// Renaming changes user-visible authored identity, so it remains
			// fenced by the exact originating conversation. Archiving/restoring is
			// owner bookkeeping over an already-failed root: old legacy work often
			// points at a conversation revision that no longer exists, and that
			// stale source must not make the failure impossible to clear. ACLWrite,
			// the authorization header, and the studio-revision CAS still bind the
			// exact project mutation.
			var err error
			if titleChanged {
				err = kanbanApp.withCurrentAgentThreadSource(scoutAgentThread{Artifact: prior}, updateProject)
			} else {
				err = updateProject()
			}
			if err != nil {
				writeAuthError(w, http.StatusConflict, "project changed; reload and try again")
				return
			}
		}
		candidates, resultIndex := studioProjectProjectionDirectory()
		for _, candidate := range authorizedStudioProjectCandidates(r.Context(), viewer, candidates) {
			if candidate.Entry.ID != payload.ID {
				continue
			}
			if view, ok := studioProjectViewForCandidate(r.Context(), viewer, candidate, resultIndex); ok {
				writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "project": view})
				return
			}
		}
		writeAuthError(w, http.StatusConflict, "project changed; reload and try again")
		return
	}

	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	if kind != "" && !oneOf(kind, studioProjectKindPresentation, studioProjectKindDocument) {
		writeAuthError(w, http.StatusBadRequest, "kind must be presentation or document")
		return
	}
	if wantID := strings.TrimSpace(r.URL.Query().Get("id")); wantID != "" {
		candidates, resultIndex := studioProjectProjectionDirectory()
		for _, candidate := range authorizedStudioProjectCandidates(r.Context(), viewer, candidates) {
			if candidate.Entry.ID != wantID {
				continue
			}
			if project, ok := studioProjectViewForCandidate(r.Context(), viewer, candidate, resultIndex); ok && (kind == "" || project.Kind == kind) {
				writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "project": project})
				return
			}
		}
		writeAuthError(w, http.StatusNotFound, "studio project not found")
		return
	}
	projects := studioProjectList(r.Context(), viewer, kind)
	limit := studioProjectDefaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			writeAuthError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		if parsed > studioProjectMaxLimit {
			parsed = studioProjectMaxLimit
		}
		limit = parsed
	}
	start := 0
	if before := strings.TrimSpace(r.URL.Query().Get("before")); before != "" {
		start = -1
		for index := range projects {
			if projects[index].ID == before {
				start = index + 1
				break
			}
		}
		if start < 0 {
			writeAuthError(w, http.StatusNotFound, "project cursor not found")
			return
		}
	}
	end := start + limit
	if end > len(projects) {
		end = len(projects)
	}
	window := projects[start:end]
	etag := studioProjectListETag(window)
	if etag != "" && requestETagMatches(r.Header.Get("If-None-Match"), etag) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if etag != "" {
		w.Header().Set("ETag", etag)
	}
	payload := map[string]any{"ok": true, "projects": window, "hasMore": end < len(projects)}
	if end < len(projects) && len(window) > 0 {
		payload["nextBefore"] = window[len(window)-1].ID
	}
	writeAuthJSON(w, http.StatusOK, payload)
}

func studioProjectReceiptFromIndexedRoot(ctx context.Context, app *kanbanBoardApp, viewer *userAccount, indexed meetingMemoryEntry, resultIndex scoutChatResultProjectionIndex) (*scoutChatStudioProjectRef, bool) {
	if app == nil || app.memory == nil || viewer == nil {
		return nil, false
	}
	_, _, ok := studioProjectCandidate(indexed)
	if !ok {
		return nil, false
	}
	header, found := app.memory.artifactAuthorizationHeaderByID(indexed.ID)
	if !found || !artifactHeaderAuthorized(ctx, viewer, ACLReadContent, header) || !app.projectBoundArtifactCurrent(ctx, indexed) {
		return nil, false
	}
	expected := artifactAuthorizationHeaderFromEntry(indexed)
	if header.ContentRevision != expected.ContentRevision || !strings.EqualFold(header.ContentDigest, expected.ContentDigest) {
		return nil, false
	}
	view, ok := studioProjectViewForReceipt(app, ctx, viewer, artifactListAuthorizationCandidate{Entry: indexed, Header: header}, resultIndex)
	if !ok {
		return nil, false
	}
	return &scoutChatStudioProjectRef{
		ID: view.ID, Kind: view.Kind, Title: view.Title, Status: view.Status,
		ProgressPercent: view.ProgressPercent, Phase: view.Phase, Href: view.Href,
		Checkpoint: view.Checkpoint, Attention: view.Attention,
	}, true
}

func (app *kanbanBoardApp) projectScoutChatStudioProjectRef(ctx context.Context, viewer *userAccount, message *scoutChatMessageRecord, index scoutChatResultProjectionIndex) {
	if message == nil {
		return
	}
	message.StudioProject = nil
	rootID := ""
	if message.Thread != nil {
		rootID = strings.TrimSpace(message.Thread.ArtifactID)
	} else if message.Manifest != nil {
		rootID = strings.TrimSpace(message.Manifest.GoalID)
	}
	root, found := index.byID[rootID]
	if !found {
		return
	}
	if _, _, recognized := studioProjectCandidate(root); recognized && message.Thread != nil {
		// Raw goal-thread checkpoints are not viewer-bound. Studio decisions only
		// leave the server through the gated receipt projection below.
		thread := *message.Thread
		thread.Checkpoint = nil
		message.Thread = &thread
	}
	if ref, ok := studioProjectReceiptFromIndexedRoot(ctx, app, viewer, root, index); ok {
		message.StudioProject = ref
	}
}

func studioProjectLaunchCopy(processID, label string) string {
	switch strings.TrimSpace(processID) {
	case packagingStudioProcessID:
		return "I’m building your presentation now. I’ll post the finished file here."
	case documentReportProcessID:
		return "I’m researching and writing your report now. I’ll post the finished file here."
	default:
		label = firstNonEmptyString(strings.TrimSpace(label), "Work")
		return label + " started — progress and the finished result will stay in this conversation"
	}
}
