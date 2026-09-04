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
	// Wave 3 (Work hub truth): every terminal result kind a conversation can
	// produce is Work. image covers agent-thread image results and chat renders,
	// sheet covers workbooks and data tables, research covers research-mode
	// briefs that carry the research contract, artifact covers pdf/bundle/file
	// results. presentation and document keep their exact prior meaning.
	studioProjectKindImage    = "image"
	studioProjectKindSheet    = "sheet"
	studioProjectKindResearch = "research"
	studioProjectKindArtifact = "artifact"

	// studioProjectResultVersionsMax bounds the prior-version list carried on
	// a result ref; the journal itself is unbounded in metadata.
	studioProjectResultVersionsMax = 20

	// studioProjectOpenActionPresent etc. are the one open/download verb the
	// Work detail renders per kind. Clients never infer the verb from a title,
	// filename, or MIME claim.
	studioProjectOpenActionPresent  = "present"
	studioProjectOpenActionDocument = "document"
	studioProjectOpenActionImage    = "image"
	studioProjectOpenActionDownload = "download"
	studioProjectOpenActionOpen     = "open"

	studioProjectStepQueued  = "queued"
	studioProjectStepRunning = "running"
	studioProjectStepDone    = "done"
	studioProjectStepFailed  = "failed"

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
	// Origin names the non-chat surface that launched the work. Chat origins
	// keep using Source (which re-checks thread access); a room origin has no
	// thread to authorize, so it carries only the room identity and display
	// title the viewer can already see in the rooms list.
	Origin *studioProjectOriginRef `json:"origin,omitempty"`
	// Steps is the per-stage run log of a canonical goal root, in plan order:
	// the same subtasks the chat goalcard renders from the goal plan.
	Steps     []studioProjectStepView `json:"steps,omitempty"`
	CanRename bool                    `json:"canRename"`
	// Wave 11 (Packaging Studio): the structured brief a commission was
	// launched from (provenance, shown on the row), the commission identity
	// (thread + message the brief was posted as), and the project tag the
	// deliverable is filed under (artifact_projects.go).
	Brief      map[string]any              `json:"brief,omitempty"`
	Commission *studioProjectCommissionRef `json:"commission,omitempty"`
	Project    string                      `json:"project,omitempty"`
	// Usage is an observed spending breakdown on authorized detail reads only.
	Usage                 *studioWorkUsageView           `json:"usage,omitempty"`
	Feedback              *studioWorkFeedbackView        `json:"feedback,omitempty"`
	Execution             *studioDissentExecutionView    `json:"execution,omitempty"`
	Assurance             *studioDissentAssuranceView    `json:"assurance,omitempty"`
	PriorFeedbackEvidence []workFeedbackEvidenceCitation `json:"priorFeedbackEvidence,omitempty"`
}

// studioProjectCommissionRef is the row-level receipt of a Packaging Studio
// commission: ids only, the brief itself rides on the view's Brief field.
type studioProjectCommissionRef struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	ThreadID  string `json:"threadId,omitempty"`
	MessageID string `json:"messageId,omitempty"`
	// Chain names the presentation a commissionFirst research launched (or
	// "waiting" until the research result exists).
	ChainState     string `json:"chainState,omitempty"`
	PresentationID string `json:"presentationId,omitempty"`
	// Waiting state from the chat-intake record bound to this commission
	// (viewer-fenced): "waiting on <name> · N questions" survives reload.
	packagingCommissionWaitingState
}

func studioProjectCommissionRefFor(ctx context.Context, app *kanbanBoardApp, viewer *userAccount, entry meetingMemoryEntry) *studioProjectCommissionRef {
	kind, ok := packagingCommissionMetadata(entry)
	if !ok {
		return nil
	}
	ref := &studioProjectCommissionRef{
		ID: entry.ID, Kind: kind,
		// A story outline records its bound thread under storyThreadId (the
		// Story Studio mint stamps only that key), so read both: an empty
		// threadId here is what makes "Open the outline" refuse a thread the
		// viewer can actually read.
		ThreadID: strings.TrimSpace(firstNonEmptyString(
			entry.Metadata[packagingCommissionThreadIDMetadataKey],
			entry.Metadata[packagingStoryThreadIDMetadataKey],
		)),
		MessageID:                       strings.TrimSpace(entry.Metadata[packagingCommissionMessageIDMetadataKey]),
		ChainState:                      strings.TrimSpace(entry.Metadata[packagingChainStateMetadataKey]),
		PresentationID:                  strings.TrimSpace(entry.Metadata[packagingChainPresentationIDMetadataKey]),
		packagingCommissionWaitingState: packagingCommissionWaitingState{BriefComplete: true},
	}
	if app != nil && viewer != nil {
		ref.packagingCommissionWaitingState = app.packagingCommissionWaitingStateFor(ctx, viewer, entry)
	}
	return ref
}

type studioProjectPhaseView struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
}

// studioProjectOriginRef is the viewer-safe launch origin for work that did
// not start in a chat thread. Kind is currently always "room".
type studioProjectOriginRef struct {
	Kind      string `json:"kind"`
	RoomID    string `json:"roomId,omitempty"`
	RoomTitle string `json:"roomTitle,omitempty"`
}

// studioProjectStepView is one goal-plan subtask projected for the Work
// detail. State is the closed queued|running|done|failed vocabulary; At is
// best-effort (the stage artifact's filing time when the directory holds it)
// and omitted rather than invented.
type studioProjectStepView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	State string `json:"state"`
	At    string `json:"at,omitempty"`
}

// studioProjectResultVersionRef is one superseded version of the result
// artifact, projected from the artifactVersions lineage journal memory.go
// keeps on every body edit. Source is who superseded that version (the
// editor recorded on the journal line). BodyRef is the content-addressed
// snapshot of that version's body when the blob seam captured one; the blob
// route re-authorizes the signed-in viewer before serving it.
type studioProjectResultVersionRef struct {
	Version int    `json:"version"`
	At      string `json:"at,omitempty"`
	Digest  string `json:"digest,omitempty"`
	Source  string `json:"source,omitempty"`
	BodyRef string `json:"bodyRef,omitempty"`
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
	// OpenAction is the one verb the Work detail offers for this kind:
	// present (deck), document (document-style open, also research), image
	// (preview PrimaryAsset), download (sheet/artifact bytes), open (artifact
	// stage when no asset is available).
	OpenAction string `json:"openAction,omitempty"`
	// PrimaryAsset is the exact blob the open/download verb targets, chosen
	// server-side by kind; DownloadURL is its authenticated download route.
	PrimaryAsset *scoutChatResultAssetRef `json:"primaryAsset,omitempty"`
	DownloadURL  string                   `json:"downloadUrl,omitempty"`
	// Versions lists superseded versions of this exact result, newest first,
	// capped at studioProjectResultVersionsMax. The current version is the
	// Version field above and is not repeated here.
	Versions []studioProjectResultVersionRef `json:"versions,omitempty"`
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

// studioProjectKinds is the closed kind vocabulary the list filter accepts.
var studioProjectKinds = []string{
	studioProjectKindPresentation, studioProjectKindDocument, studioProjectKindImage,
	studioProjectKindSheet, studioProjectKindResearch, studioProjectKindArtifact,
}

func studioProjectKindValid(kind string) bool {
	return oneOf(strings.TrimSpace(kind), studioProjectKinds...)
}

// studioProjectKindByProcessID maps a registered process to the Work kind of
// its result. Only processes whose result the goal projection can bind
// (a declared deck or a goalDeliverable document) belong here; image, sheet,
// research, and generic artifact results have no authored process today and
// enter Work through studioLegacyProjectCandidate as standalone terminal
// results instead.
var studioProjectKindByProcessID = map[string]string{
	packagingStudioProcessID: studioProjectKindPresentation,
	documentReportProcessID:  studioProjectKindDocument,
}

func studioProjectKindForProcessID(processID string) string {
	return studioProjectKindByProcessID[strings.TrimSpace(processID)]
}

// studioResearchReportArtifact recognizes a research-mode brief by its
// durable contract stamp, never by prose or title. Research-mode Markdown
// without the contract stays a plain document so historical reports keep
// their kind.
func studioResearchReportArtifact(metadata map[string]string) bool {
	contract := strings.ToLower(strings.TrimSpace(firstNonEmptyString(metadata["artifactContract"], metadata["outputContract"])))
	return strings.HasPrefix(contract, "research_")
}

// studioProjectKindForArtifactType maps a concrete non-authored result type
// onto its Work kind. Markdown and decks are classified by their own rules.
func studioProjectKindForArtifactType(typeName string) string {
	switch typeName {
	case artifactTypeImage:
		return studioProjectKindImage
	case artifactTypeWorkbook, artifactTypeTable:
		return studioProjectKindSheet
	case artifactTypePDF, artifactTypeBundle, artifactTypeFile:
		return studioProjectKindArtifact
	default:
		return ""
	}
}

// studioChatImageCandidate recognizes a chat-generated image render
// (scout_chat_images.go): source chat_image, no goal lineage, and a real
// image asset on the record. Its body is Markdown, so the type switch alone
// would misfile it as a document.
func studioChatImageCandidate(entry meetingMemoryEntry) bool {
	metadata := entry.Metadata
	if strings.TrimSpace(metadata["source"]) != "chat_image" || strings.TrimSpace(metadata["goalParentId"]) != "" ||
		strings.TrimSpace(metadata["processStage"]) != "" || strings.TrimSpace(metadata["threadId"]) != "" {
		return false
	}
	return scoutChatResultHasImageAsset(scoutChatResultAssets(entry))
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

// studioLegacyProjectCandidate keeps pre-Studio authored work and every
// standalone terminal result reachable without rewriting production history
// or inventing a second lifecycle store. The classification is deliberately
// typed and fail closed: Markdown bodies of old Design, Grill, Workflow and
// generic Artifacts modes, process children, and ordinary Files stay out even
// when their persisted body happens to be Markdown. Concrete non-Markdown
// results (image, workbook/table, pdf/bundle/file) filed by a conversation
// run are Work by type; a chat image render is Work by its image asset. A
// native named deck copy is the one threadless deck exception because its
// scene/copy metadata is a server-owned presentation identity.
func studioLegacyProjectCandidate(entry meetingMemoryEntry) (string, bool) {
	if _, _, canonical := studioProjectCandidate(entry); canonical {
		return "", false
	}
	metadata := entry.Metadata
	if entry.Kind != meetingMemoryKindOSArtifact || strings.TrimSpace(metadata["goalParentId"]) != "" ||
		strings.TrimSpace(metadata["processStage"]) != "" {
		return "", false
	}
	if studioChatImageCandidate(entry) {
		return studioProjectKindImage, true
	}
	source := strings.TrimSpace(metadata["source"])
	if !oneOf(source, "scout_thread", studioBlankSourceDocument, studioBlankSourceDeck) {
		return "", false
	}
	mode := strings.ToLower(strings.TrimSpace(metadata["mode"]))
	threadID := strings.TrimSpace(metadata["threadId"])
	typeName := artifactType(entry)
	if kind := studioProjectKindForArtifactType(typeName); kind != "" {
		// A concrete result type is its own identity; only a real conversation
		// run (source + run id) may file it as Work, and children/stages were
		// already excluded above.
		return kind, source == "scout_thread" && threadID != ""
	}
	switch typeName {
	case artifactTypeHTMLDeck:
		threadedPresentation := source == "scout_thread" && threadID != "" && oneOf(mode, "artifacts", "presentation", "deck", "slides")
		nativeNamedCopy := source == "scout_thread" && threadID == "" && mode == "artifacts" &&
			validBlobRef(strings.TrimSpace(metadata[deckSceneRefMetadataKey])) &&
			strings.TrimSpace(metadata["copiedFromArtifactId"]) != ""
		// A studio-native blank create is server-owned identity the same way a
		// named copy is: its scene ref was minted by the deck boundary.
		nativeBlank := source == studioBlankSourceDeck && threadID == "" &&
			validBlobRef(strings.TrimSpace(metadata[deckSceneRefMetadataKey]))
		return studioProjectKindPresentation, threadedPresentation || nativeNamedCopy || nativeBlank
	case artifactTypeMarkdown:
		threaded := source == "scout_thread" && threadID != "" && oneOf(mode, "research", "document", "report")
		nativeBlank := source == studioBlankSourceDocument && threadID == ""
		if threaded && mode == "research" && studioResearchReportArtifact(metadata) {
			return studioProjectKindResearch, true
		}
		return studioProjectKindDocument, threaded || nativeBlank
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

// studioProjectOrigin projects a room launch origin. Chat origins are carried
// by Source (which re-checks thread access) and are deliberately not repeated
// here. The room title is the same display name the member rooms list shows;
// the office is the migration-invariant default room.
func studioProjectOrigin(entry meetingMemoryEntry) *studioProjectOriginRef {
	if strings.TrimSpace(entry.Metadata["originKind"]) != agentThreadOriginRoom {
		return nil
	}
	roomID := normalizeRoomID(entry.Metadata["originId"])
	origin := &studioProjectOriginRef{Kind: agentThreadOriginRoom, RoomID: roomID}
	if roomID == officeRoomID {
		origin.RoomTitle = officeRoomName
	} else if store := appRoomStoreIfOpen(); store != nil {
		if room, ok := store.byID(roomID); ok {
			origin.RoomTitle = strings.TrimSpace(room.Name)
		}
	}
	return origin
}

// studioProjectStepState folds the engine's subtask vocabulary onto the
// closed Work step vocabulary. Unknown values read as queued rather than as
// progress that never happened.
func studioProjectStepState(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case subtaskRunning:
		return studioProjectStepRunning
	case subtaskComplete:
		return studioProjectStepDone
	case subtaskFailed, subtaskBlocked:
		return studioProjectStepFailed
	default:
		return studioProjectStepQueued
	}
}

// studioProjectSteps projects the goal plan's subtasks — the exact run log the
// chat goalcard renders — in plan order. At is stamped only when the stage's
// filed artifact is already in the body-free directory; it is never invented
// from the root's timestamps.
func studioProjectSteps(plan goalPlan, index scoutChatResultProjectionIndex) []studioProjectStepView {
	if len(plan.Subtasks) == 0 {
		return nil
	}
	steps := make([]studioProjectStepView, 0, len(plan.Subtasks))
	for _, subtask := range plan.Subtasks {
		id := strings.TrimSpace(subtask.ID)
		if id == "" {
			continue
		}
		step := studioProjectStepView{
			ID:    id,
			Label: boundedStudioProjectTitle(firstNonEmptyString(subtask.Title, id)),
			State: studioProjectStepState(subtask.Status),
		}
		if artifactID := strings.TrimSpace(subtask.ArtifactID); artifactID != "" && step.State == studioProjectStepDone {
			if filed, ok := index.byID[artifactID]; ok && !filed.CreatedAt.IsZero() {
				step.At = filed.CreatedAt.UTC().Format(time.RFC3339Nano)
			}
		}
		steps = append(steps, step)
	}
	return steps
}

// studioProjectResultVersions projects the superseded versions of the exact
// current result from its lineage journal, newest first. The indexed row must
// still describe the current version the viewer was just authorized for;
// otherwise the directory is stale and no history is claimed.
func studioProjectResultVersions(indexed meetingMemoryEntry, currentVersion int) []studioProjectResultVersionRef {
	if currentVersion < 1 || artifactVersion(indexed) != currentVersion {
		return nil
	}
	history := artifactVersionHistory(indexed)
	if len(history) == 0 {
		return nil
	}
	versions := make([]studioProjectResultVersionRef, 0, min(len(history), studioProjectResultVersionsMax))
	for position := len(history) - 1; position >= 0 && len(versions) < studioProjectResultVersionsMax; position-- {
		record := history[position]
		if record.V < 1 || record.V >= currentVersion {
			continue
		}
		ref := studioProjectResultVersionRef{
			Version: record.V,
			At:      strings.TrimSpace(record.At),
			Digest:  strings.ToLower(strings.TrimSpace(record.ContentDigest)),
			Source:  strings.TrimSpace(record.EditedBy),
		}
		if validBlobRef(strings.TrimSpace(record.BodyBlobRef)) {
			ref.BodyRef = strings.TrimSpace(record.BodyBlobRef)
		}
		versions = append(versions, ref)
	}
	return versions
}

// studioProjectPrimaryAsset picks the one blob the kind's open/download verb
// targets: the image for an image, the export (workbook/pdf) for sheets and
// artifacts, otherwise the first bounded asset.
func studioProjectPrimaryAsset(kind string, assets []scoutChatResultAssetRef) *scoutChatResultAssetRef {
	if len(assets) == 0 {
		return nil
	}
	pick := func(match func(scoutChatResultAssetRef) bool) *scoutChatResultAssetRef {
		for index := range assets {
			if match(assets[index]) {
				asset := assets[index]
				return &asset
			}
		}
		return nil
	}
	switch kind {
	case studioProjectKindImage:
		return pick(func(asset scoutChatResultAssetRef) bool {
			return asset.Kind == "image" && strings.HasPrefix(strings.ToLower(asset.Mime), "image/")
		})
	case studioProjectKindSheet, studioProjectKindArtifact:
		if asset := pick(func(asset scoutChatResultAssetRef) bool { return oneOf(asset.Kind, "export", "pdf") }); asset != nil {
			return asset
		}
		asset := assets[0]
		return &asset
	default:
		return nil
	}
}

// studioProjectDecorateResult stamps the kind-specific open verb, the primary
// asset and its authenticated download route, and the prior-version list on
// an already viewer-authorized result ref.
func studioProjectDecorateResult(result *studioProjectResultRef, kind string, index scoutChatResultProjectionIndex) {
	if result == nil {
		return
	}
	switch kind {
	case studioProjectKindPresentation:
		result.OpenAction = studioProjectOpenActionPresent
	case studioProjectKindDocument, studioProjectKindResearch:
		result.OpenAction = studioProjectOpenActionDocument
	case studioProjectKindImage:
		result.OpenAction = studioProjectOpenActionImage
	case studioProjectKindSheet, studioProjectKindArtifact:
		result.OpenAction = studioProjectOpenActionDownload
	default:
		result.OpenAction = studioProjectOpenActionOpen
	}
	result.PrimaryAsset = studioProjectPrimaryAsset(kind, result.Assets)
	if result.PrimaryAsset != nil {
		result.DownloadURL = fileBlobDownloadURL(result.PrimaryAsset.Ref, firstNonEmptyString(result.PrimaryAsset.Name, result.Title))
	} else if result.OpenAction == studioProjectOpenActionImage || result.OpenAction == studioProjectOpenActionDownload {
		// No exact bytes to hand over: fall back to the artifact stage instead
		// of offering a download that would 404.
		result.OpenAction = studioProjectOpenActionOpen
	}
	if indexed, ok := index.byID[result.ArtifactID]; ok {
		result.Versions = studioProjectResultVersions(indexed, result.Version)
	}
}

// studioProjectChatImageResult binds a chat image render as its own result.
// Chat renders sit outside the agent-thread result contract (no run id, a
// Markdown body around the image), so the thread-ref projection cannot bind
// them; this path applies the same rules — current authorized body, exact
// version + capability digest, a real image asset, viewer-scoped capability
// flags — without a goal or run identity.
func studioProjectChatImageResult(ctx context.Context, app *kanbanBoardApp, viewer *userAccount, entry meetingMemoryEntry) *studioProjectResultRef {
	if app == nil || viewer == nil || !studioChatImageCandidate(entry) {
		return nil
	}
	current, ok := app.authorizedScoutChatResultArtifact(ctx, viewer, entry.ID)
	if !ok || current.ID != entry.ID || !studioChatImageCandidate(current) {
		return nil
	}
	if agentThreadStatusValue(current) != artifactStatusComplete {
		return nil
	}
	assets := scoutChatResultAssets(current)
	version := artifactVersion(current)
	digest := strings.ToLower(strings.TrimSpace(artifactCapabilityDigest(current)))
	if version < 1 || !isHexDigest(digest) || !scoutChatResultHasImageAsset(assets) {
		return nil
	}
	return &studioProjectResultRef{
		ArtifactID: current.ID, Type: artifactTypeImage, Version: version, Digest: digest,
		Title:     firstNonEmptyString(strings.TrimSpace(current.Metadata["title"]), "Image"),
		Assets:    assets,
		CanEdit:   app.artifactAuthorized(ctx, viewer, ACLWrite, current),
		CanExport: app.artifactAuthorized(ctx, viewer, ACLExport, current),
	}
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
	} else if !canonical && kind == studioProjectKindImage {
		view.Result = studioProjectChatImageResult(ctx, app, viewer, entry)
	}
	studioProjectDecorateResult(view.Result, kind, resultIndex)
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
	view.Origin = studioProjectOrigin(entry)
	view.Brief = packagingBriefMap(decodePackagingBriefMetadata(entry.Metadata))
	view.Commission = studioProjectCommissionRefFor(ctx, app, viewer, entry)
	view.Project = strings.TrimSpace(entry.Metadata[artifactProjectMetadataKey])
	if canonical {
		view.Steps = studioProjectSteps(plan, resultIndex)
	}
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
	return studioProjectListFiltered(ctx, viewer, kind, "")
}

// studioProjectListFiltered adds the Wave 11 project filter: an exact
// (case-insensitive) project tag name; "" lists every project.
func studioProjectListFiltered(ctx context.Context, viewer *userAccount, kind string, project string) []studioProjectView {
	candidates, index := studioProjectProjectionDirectory()
	sourceCache := newStudioProjectSourceAccessCache()
	companyCache := newStudioProjectCompanyAccessCache()
	views := make([]studioProjectView, 0)
	project = strings.Join(strings.Fields(project), " ")
	for _, candidate := range authorizedStudioProjectCandidates(ctx, viewer, candidates) {
		if studioProjectArchived(candidate.Entry) {
			continue
		}
		if project != "" && !strings.EqualFold(strings.TrimSpace(candidate.Entry.Metadata[artifactProjectMetadataKey]), project) {
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
			ID               string                     `json:"id"`
			Title            string                     `json:"title"`
			Archived         *bool                      `json:"archived"`
			ExpectedRevision int                        `json:"expectedRevision"`
			Feedback         *studioWorkFeedbackRequest `json:"feedback"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&payload); err != nil {
			writeAuthError(w, http.StatusBadRequest, "could not read project update")
			return
		}
		payload.ID = strings.TrimSpace(payload.ID)
		payload.Title = boundedStudioProjectTitle(payload.Title)
		if payload.Feedback != nil {
			if payload.Title != "" || payload.Archived != nil {
				writeAuthError(w, http.StatusBadRequest, "feedback cannot be combined with another update")
				return
			}
			studioWorkFeedbackHandler(w, r, viewer, payload.ID, payload.ExpectedRevision, *payload.Feedback)
			return
		}
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
	if kind != "" && !studioProjectKindValid(kind) {
		writeAuthError(w, http.StatusBadRequest, "kind must be one of "+strings.Join(studioProjectKinds, ", "))
		return
	}
	if wantID := strings.TrimSpace(r.URL.Query().Get("id")); wantID != "" {
		candidates, resultIndex := studioProjectProjectionDirectory()
		for _, candidate := range authorizedStudioProjectCandidates(r.Context(), viewer, candidates) {
			if candidate.Entry.ID != wantID {
				continue
			}
			if project, ok := studioProjectViewForCandidate(r.Context(), viewer, candidate, resultIndex); ok && (kind == "" || project.Kind == kind) {
				project.Usage = studioWorkUsageForViewer(r.Context(), viewer, candidate, resultIndex)
				project.Feedback = studioWorkFeedbackForViewer(r.Context(), viewer, candidate.Entry, project)
				project.PriorFeedbackEvidence = kanbanApp.studioPriorFeedbackEvidenceForViewer(r.Context(), viewer, project.Result)
				if project.Result != nil {
					project.Execution, project.Assurance = kanbanApp.studioDissentEvidenceForViewer(r.Context(), viewer, *project.Result)
				}
				writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "project": project})
				return
			}
		}
		writeAuthError(w, http.StatusNotFound, "studio project not found")
		return
	}
	projectFilter := strings.TrimSpace(r.URL.Query().Get("project"))
	if len([]rune(projectFilter)) > fileFolderNameMaxLen {
		writeAuthError(w, http.StatusBadRequest, "project filter is too long")
		return
	}
	if !kanbanApp.artifactProjectVisibleToViewer(r.Context(), viewer, projectFilter) {
		writeAuthError(w, http.StatusNotFound, "project not found")
		return
	}
	// A commissionFirst chain advances on the requester's next read of the
	// list: the waiting deck launches once its research result is complete.
	kanbanApp.advancePackagingCommissionChainsForViewer(r.Context(), viewer)
	projects := studioProjectListFiltered(r.Context(), viewer, kind, projectFilter)
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
