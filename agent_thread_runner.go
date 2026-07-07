package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultAgentThreadRequestTimeout = 60 * time.Second

// Agent-thread origin kinds, stamped at launch so completion can deliver the
// finished artifact card back to the surface that requested the work.
const (
	agentThreadOriginRoom          = "room"
	agentThreadOriginChannel       = "channel"
	agentThreadOriginPrivateThread = "private_thread"
	agentThreadOriginTool          = "tool"
)

// agentThreadOriginMetadataKeys are the only origin keys a launch call site
// may stamp; everything else in the origin map is dropped. routeNote is the
// card 068 delivery-routing disclosure (best match / #general fallback) the
// workflow ticker stamps so completion delivery can surface WHY the finished
// work landed in a given channel.
var agentThreadOriginMetadataKeys = []string{"originKind", "originId", "originMeetingId", "routeNote"}

// broadcastNavigationActions decides whether a room-wide assistant_event may
// carry navigation actions (open_tool: chat, etc). A channel-origin launch is
// background, fire-and-forget work in a public thread — approving a Scout
// proposal must not yank the approver OR anyone else in the room to the chat
// tab. So channel-origin broadcasts drop their navigation actions; the room
// learns via the live thread card + the completion notification instead. Room
// and tool origins keep today's behavior (the initiator's own navigation still
// rides its direct HTTP/tool response, a separate channel from this broadcast).
func broadcastNavigationActions(originKind string, actions []osAssistantAction) []osAssistantAction {
	if strings.TrimSpace(originKind) == agentThreadOriginChannel {
		return nil
	}
	return actions
}

type scoutAgentThread struct {
	ID       string              `json:"id"`
	Mode     string              `json:"mode"`
	Query    string              `json:"query"`
	Status   string              `json:"status"`
	Artifact meetingMemoryEntry  `json:"artifact"`
	Actions  []osAssistantAction `json:"actions,omitempty"`
}

// startAgentThreadAsync is assigned in init (not at declaration) to break the
// package-initialization cycle runAgentThread → syncLinkedCardForArtifact →
// applyToolCallArgs → launchAgentThreadWithOrigin → startAgentThreadAsync.
var startAgentThreadAsync func(app *kanbanBoardApp, thread scoutAgentThread)

func init() {
	startAgentThreadAsync = func(app *kanbanBoardApp, thread scoutAgentThread) {
		go app.runAgentThread(thread)
	}
}

func (app *kanbanBoardApp) launchAgentThread(mode string, query string, createdBy string) (scoutAgentThread, error) {
	return app.launchAgentThreadWithOrigin(mode, query, createdBy, nil)
}

// agentThreadGoalSpec carries the additive goal-spec fields a caller may stamp
// on a launch. Every field is optional: an empty spec stamps nothing and
// reproduces today's behavior exactly. Present fields become additive artifact
// metadata the AgentRunner layer (and Wave 2's /goal engine) can read back.
type agentThreadGoalSpec struct {
	Objective     string
	ToolTemplate  string
	ContextRefs   string
	OriginSurface string
	RequestedBy   string
	Authority     string
	Visibility    string
	PackageID     string
	// Goal-engine linkage (Wave 2): a subtask launched by the /goal engine
	// stamps its parent goal + subtask id so the child's terminal seam folds
	// the result back into the parent plan, and the assigned runner so
	// selectAgentRunner can honor the per-subtask capability match.
	ParentGoalID   string
	SubtaskID      string
	AssignedRunner string
	// Deliverable marks the terminal, contract-bearing subtask so the runner
	// gives its generation a heavier effort + token budget (agent_runner_iface.go
	// reads the goalDeliverable flag). Only the /goal engine sets it.
	Deliverable bool
	// OutputContract is the process stage's declared contract, stamped so the
	// worker's instruction layer can honor raw-document contracts (a
	// packaging_deck_v1 child's response IS the HTML file, not a workflow
	// report). Only process writer stages set it.
	OutputContract string
}

func (spec agentThreadGoalSpec) metadata() map[string]string {
	metadata := map[string]string{}
	for key, value := range map[string]string{
		"objective":      spec.Objective,
		"toolTemplate":   spec.ToolTemplate,
		"contextRefs":    spec.ContextRefs,
		"originSurface":  spec.OriginSurface,
		"requestedBy":    spec.RequestedBy,
		"authority":      spec.Authority,
		"visibility":     spec.Visibility,
		"packageId":      spec.PackageID,
		"goalParentId":   spec.ParentGoalID,
		"goalSubtaskId":  spec.SubtaskID,
		"assignedRunner": spec.AssignedRunner,
		"outputContract": spec.OutputContract,
	} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			metadata[key] = trimmed
		}
	}
	if spec.Deliverable {
		metadata["goalDeliverable"] = "true"
	}
	return metadata
}

// launchAgentThreadWithOrigin launches an agent thread with origin metadata
// (originKind/originId/originMeetingId) stamped on the artifact so
// deliverArtifactToOrigin can close the loop when the worker completes.
func (app *kanbanBoardApp) launchAgentThreadWithOrigin(mode string, query string, createdBy string, origin map[string]string) (scoutAgentThread, error) {
	return app.launchAgentThreadWithSpec(mode, query, createdBy, origin, agentThreadGoalSpec{})
}

// launchAgentThreadWithSpec is launchAgentThreadWithOrigin plus additive
// goal-spec metadata. Existing callers route through the thin wrapper above with
// an empty spec, so their behavior is unchanged.
func (app *kanbanBoardApp) launchAgentThreadWithSpec(mode string, query string, createdBy string, origin map[string]string, spec agentThreadGoalSpec) (scoutAgentThread, error) {
	mode = normalizeAgentThreadMode(mode)
	if mode == "" {
		return scoutAgentThread{}, fmt.Errorf("thread mode is required")
	}
	query = canonicalizeBoardText(query)
	if query == "" {
		return scoutAgentThread{}, fmt.Errorf("thread query is required")
	}

	threadID := fmt.Sprintf("agent-thread-%s-%d", mode, time.Now().UnixNano())
	worker := configuredAgentThreadWorkerName()
	content := buildAgentThreadScaffold(mode, query, app.snapshotState(), app.memorySnapshotForClients(12))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	metadata := map[string]string{
		"source":          "scout_thread",
		"threadId":        threadID,
		"threadQuery":     query,
		"agentLoop":       "realtime_controlled_workforce",
		"status":          "running",
		"threadStatus":    "running",
		"goalStatus":      "running",
		"currentStage":    "execute_in_order",
		"progressPercent": "35",
		"workflowStages":  goalWorkflowStageMetadata,
		"reviewGate":      "pending",
		"queuedAt":        now,
		"startedAt":       now,
		"published":       "false",
		"worker":          worker,
		"workerBoundary":  agentThreadWorkerBoundary(worker),
		"latestThreadRun": threadID,
	}
	for key, value := range agentThreadModeMetadata(mode) {
		metadata[key] = value
	}
	for _, key := range agentThreadOriginMetadataKeys {
		if value := strings.TrimSpace(origin[key]); value != "" {
			metadata[key] = value
		}
	}
	// Additive goal-spec metadata: absent fields stamp nothing, so callers that
	// pass an empty spec keep today's behavior.
	for key, value := range spec.metadata() {
		metadata[key] = value
	}
	// Card 069 governance stamp: every launch carries its approval lane so the
	// ticker and auto-select read enforcement, not vibes. A goal child rides
	// its parent's standard lane (the loop already collected its one-member
	// approval); otherwise the lane derives from the same dimensions the gates
	// enforce, with a blank spec authority falling back to the phrase-derived
	// class the codex sidecar will apply at enqueue.
	if strings.TrimSpace(spec.ParentGoalID) != "" {
		metadata["approvalLane"] = approvalLaneStandard
	} else {
		laneAuthority := strings.TrimSpace(spec.Authority)
		if laneAuthority == "" {
			laneAuthority = codexJobAuthorityForThread(scoutAgentThread{Mode: mode, Query: query})
		}
		metadata["approvalLane"] = approvalLaneFor(mode, spec.ToolTemplate, laneAuthority, false)
	}
	artifact, _, err := app.createOSArtifactWithMetadata(mode, query, content, createdBy, metadata)
	if err != nil {
		return scoutAgentThread{}, err
	}
	if strings.TrimSpace(artifact.ID) == "" {
		return scoutAgentThread{}, fmt.Errorf("thread artifact was not saved")
	}

	actions := app.osAssistantActions(query, mode, artifact)
	thread := scoutAgentThread{
		ID:       threadID,
		Mode:     mode,
		Query:    query,
		Status:   "running",
		Artifact: artifact,
		Actions:  actions,
	}

	broadcastSignedInKanbanEvent("memory", app.memorySnapshotForClients(20))
	// A channel-origin launch drops navigation actions BOTH at the top level and
	// inside the nested thread, so no client — present or future — can read a
	// navigation action off this room-wide broadcast and yank the tab.
	broadcastThread := thread
	broadcastThread.Actions = broadcastNavigationActions(metadata["originKind"], actions)
	broadcastAssistantEvent("action", assistantToolLabel(mode)+" thread launched", map[string]any{
		"tool":       "launch_agent_thread",
		"thread":     broadcastThread,
		"artifact":   artifact,
		"actions":    broadcastThread.Actions,
		"voiceState": "listening",
	})

	startAgentThreadAsync(app, thread)
	return thread, nil
}

func normalizeAgentThreadMode(mode string) string {
	switch normalizeRealtimeArtifactMode(mode) {
	case "artifacts", "research", "design", "grill", "workflow":
		return normalizeRealtimeArtifactMode(mode)
	default:
		return ""
	}
}

func (app *kanbanBoardApp) runAgentThread(thread scoutAgentThread) {
	ctx, cancel := context.WithTimeout(context.Background(), agentThreadRequestTimeout())
	defer cancel()

	workerResult, err := app.produceAgentThreadArtifactWithWorker(ctx, thread, createOpenAITextResponse)
	output := workerResult.Text
	if err == nil && !workerResult.Terminal {
		app.updateQueuedAgentThread(thread, workerResult)
		return
	}

	status := "complete"
	message := assistantToolLabel(thread.Mode) + " thread complete"
	metadata := map[string]string{
		"status":          "complete",
		"threadStatus":    "complete",
		"goalStatus":      "verified",
		"currentStage":    "verify_goal_completed",
		"progressPercent": "100",
		"reviewGate":      "passed",
		"completedAt":     time.Now().UTC().Format(time.RFC3339Nano),
		"latestThreadRun": thread.ID,
	}
	for key, value := range workerResult.Metadata {
		if strings.TrimSpace(value) != "" {
			metadata[key] = value
		}
	}
	// "" keeps the stored title (safe under concurrent edits); only a real
	// derivation from the finished body replaces it.
	title := ""
	scaffoldTitle := thread.Artifact.Metadata["title"]
	if err != nil {
		status = "error"
		message = assistantToolLabel(thread.Mode) + " thread needs attention"
		output = buildAgentThreadError(thread, err)
		metadata["status"] = "error"
		metadata["threadStatus"] = "error"
		metadata["goalStatus"] = "needs_attention"
		metadata["currentStage"] = "gate_before_shipping"
		metadata["progressPercent"] = "72"
		metadata["reviewGate"] = "blocked"
		metadata["error"] = err.Error()
	} else if derived := artifactTitleFromBody(output, scaffoldTitle); derived != "" && derived != scaffoldTitle {
		// Completed work gets a real display title from the body's first
		// heading; the launch prompt survives in metadata["threadQuery"].
		title = derived
		metadata["titleSource"] = "derived"
	}
	if err == nil {
		// Terminal seam contract: grill runs get their READINESS score parsed
		// and stamped, and every completed run lands in the threadRuns log the
		// package binder charts (agent_thread_followup.go).
		stampReadinessMetadata(thread.Artifact, thread.Mode, output, metadata)
		version := 1
		if parsed, versionErr := strconv.Atoi(strings.TrimSpace(thread.Artifact.Metadata["threadVersion"])); versionErr == nil && parsed > 0 {
			version = parsed
		}
		appendThreadRunLog(thread.Artifact, metadata, thread.ID, version, thread.Artifact.Metadata["createdBy"])
	}

	artifact, _, updateErr := app.updateOSArtifactWithMetadata(thread.Artifact.ID, title, output, scoutParticipantName, metadata)
	if updateErr != nil {
		log.Errorf("Failed to update Scout thread artifact %s: %v", thread.ID, updateErr)
		broadcastAssistantEvent("error", "Scout thread could not update its artifact", map[string]any{
			"tool":     "launch_agent_thread",
			"threadId": thread.ID,
			"artifact": thread.Artifact,
		})
		return
	}

	actions := app.osAssistantActions(thread.Query, thread.Mode, artifact)
	broadcastActions := broadcastNavigationActions(artifact.Metadata["originKind"], actions)
	broadcastSignedInKanbanEvent("memory", app.memorySnapshotForClients(20))
	broadcastAssistantEvent("action", message, map[string]any{
		"tool":       "launch_agent_thread",
		"thread":     scoutAgentThread{ID: thread.ID, Mode: thread.Mode, Query: thread.Query, Status: status, Artifact: artifact, Actions: broadcastActions},
		"artifact":   artifact,
		"actions":    broadcastActions,
		"voiceState": "listening",
	})
	// Terminal status must reach requesters who launched from chat: the ref
	// commit pushes a chat_thread event over the office socket (channel
	// broadcast or owner-targeted for private threads); the 12s chat poll
	// re-renders the persisted ref when the socket is down.
	app.updateScoutChatThreadRefs(thread.ID, status, artifact.ID)
	// Board auto-advance: a finished deliverable moves its linked card
	// (complete → Done, error → Blocked) so the board stops lying about
	// launched work.
	app.syncLinkedCardForArtifact(artifact, status)
	// Close the loop: a successful completion posts the compact artifact card
	// back to the surface that requested the work (room chat or channel).
	if status == "complete" {
		app.deliverArtifactToOrigin(artifact, thread.ID)
	}
	// Durable milestone: the creator learns the thread finished (or failed)
	// even if they are outside the room when the worker lands. A /goal subtask
	// child is suppressed here — the parent goal engine notifies the creator
	// exactly once on the goal's terminal state, so one goal never fires a
	// notification per subtask AND per revision (the v1/v2/v3 flood).
	if shouldNotifyAgentThreadCreator(artifact) {
		app.notifyAgentThreadCreator(artifact, notificationKindAgent, agentThreadNotificationText(message, artifact))
	}
	// Goal-engine linkage: a subtask child folds its terminal result back into
	// the parent plan, which re-drives the state machine (goal_engine.go). No-op
	// for non-goal threads (goalParentId absent).
	if parentID := strings.TrimSpace(artifact.Metadata["goalParentId"]); parentID != "" {
		app.foldGoalChildCompletion(parentID, artifact.Metadata["goalSubtaskId"], artifact, status)
	}
}

// deliverArtifactToOrigin posts a compact completion card back to the surface
// that requested the work. Complete-only: errors keep the existing creator
// notification. Idempotence: metadata["deliveredAt"] is stamped before the
// post, so a retried codex callback (or a rerun of the same artifact id)
// never delivers twice.
func (app *kanbanBoardApp) deliverArtifactToOrigin(artifact meetingMemoryEntry, agentThreadID string) {
	if app == nil || app.memory == nil || strings.TrimSpace(artifact.ID) == "" {
		return
	}
	originKind := strings.TrimSpace(artifact.Metadata["originKind"])
	if originKind != agentThreadOriginRoom && originKind != agentThreadOriginChannel {
		// private_thread delivery IS the persisted ref rewrite
		// (updateScoutChatThreadRefs); tool/absent keep the existing
		// notification-only behavior.
		return
	}
	if strings.TrimSpace(artifact.Metadata["deliveredAt"]) != "" {
		return
	}

	mode := firstNonEmptyString(artifact.Metadata["mode"], artifact.Kind)
	title := firstNonEmptyString(strings.TrimSpace(artifact.Metadata["title"]), assistantToolLabel(mode)+" artifact")
	text := fmt.Sprintf("finished %s — %s", assistantToolLabel(mode), title)
	// Card 068: a workflow-ticker launch stamps a routeNote disclosing WHY the
	// work landed in this channel (best match / #general fallback). Surface it on
	// the completion card so a fuzzy or fallback route is honest, not silent.
	if note := strings.TrimSpace(artifact.Metadata["routeNote"]); note != "" {
		text += " · " + note
	}
	stampDelivered := func() bool {
		if _, _, err := app.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "", map[string]string{
			"deliveredAt": time.Now().UTC().Format(time.RFC3339Nano),
		}); err != nil {
			log.Errorf("Failed to stamp deliveredAt on artifact %s: %v", artifact.ID, err)
			return false
		}
		return true
	}

	switch originKind {
	case agentThreadOriginRoom:
		// Guard: appendEntry lazily mints a NEW meeting id, so posting after
		// the origin meeting archived/rotated would fabricate a phantom
		// meeting. The creator notification already covers that case. The
		// check here is only a cheap early-out — the append itself re-checks
		// the id under the store lock (appendEntryForMeeting), so a rotation
		// racing the stampDelivered write below can never slip a card into a
		// phantom or successor meeting.
		originMeetingID := strings.TrimSpace(artifact.Metadata["originMeetingId"])
		if originMeetingID == "" || originMeetingID != app.memory.currentMeetingID() {
			return
		}
		if !stampDelivered() {
			return
		}
		payload, ok := app.recordRoomChatMessageWithArtifact(scoutParticipantName, text, artifact.ID, originMeetingID)
		if !ok {
			return
		}
		broadcastSignedInKanbanEvent("room_chat", payload)
	case agentThreadOriginChannel:
		channelID := strings.TrimSpace(artifact.Metadata["originId"])
		if channelID == "" {
			return
		}
		entry, ok := app.memory.entryByKindAndID(meetingMemoryKindScoutChat, channelID)
		if !ok {
			return
		}
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok {
			return
		}
		if thread.ArchivedAt != "" || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
			// An archived channel (creator-only action) or a non-public thread
			// never accepts delivery writes — commitScoutChatThreadMessages runs
			// as the owner and would bypass the archived-thread guard every
			// user-facing writer enforces. Fall back to the creator
			// notification, which the terminal seam always sends.
			return
		}
		// Alert the whole team the work is done BEFORE the duplicate-card guard —
		// launchApprovedProposal already posted the live launch card for this
		// thread, so scoutChatThreadHasAgentRef is true on the common path and the
		// dedup return below would otherwise swallow the completion notification.
		app.broadcastChannelCompletion(artifact, thread)
		if agentThreadID != "" && scoutChatThreadHasAgentRef(thread, agentThreadID) {
			// The in-channel launch card already exists and
			// updateScoutChatThreadRefs flips it to complete — no duplicate.
			return
		}
		if !stampDelivered() {
			return
		}
		message := scoutChatMessageRecord{
			ID:        fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
			Kind:      "thread",
			Role:      "scout",
			Text:      text,
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Thread: &scoutChatThreadRef{
				ID:         firstNonEmptyString(agentThreadID, artifact.Metadata["threadId"]),
				Mode:       mode,
				Query:      firstNonEmptyString(artifact.Metadata["threadQuery"], artifact.Metadata["query"]),
				Status:     "complete",
				ArtifactID: artifact.ID,
			},
		}
		// The public-visibility branch inside commit broadcasts chat_thread
		// over the office channel to every signed-in client.
		if _, err := app.commitScoutChatThreadMessages(thread.OwnerEmail, thread.ID, message); err != nil {
			log.Errorf("Failed to deliver artifact %s to channel %s: %v", artifact.ID, thread.ID, err)
		}
	}
}

// broadcastChannelCompletion fires the company-wide "the report is ready" bell
// for a channel-delivered thread. userEmail "" makes it a broadcast to every
// signed-in user (pushNotificationRecord fans an empty-recipient record to all),
// so approving a proposal never MOVES anyone — the whole team is simply told the
// work finished and where to read it. It is called BEFORE the duplicate-card
// dedup guard so it fires on every completion, including the common case where
// launchApprovedProposal already posted the live launch card into the channel
// (which trips scoutChatThreadHasAgentRef and would otherwise skip the whole
// delivery block, swallowing the alert).
func (app *kanbanBoardApp) broadcastChannelCompletion(artifact meetingMemoryEntry, channel scoutChatThreadRecord) {
	title := firstNonEmptyString(strings.TrimSpace(artifact.Metadata["title"]), strings.TrimSpace(artifact.Metadata["threadQuery"]), "the report")
	notifyText := fmt.Sprintf("Scout finished %q — ready in #%s", compactAssistantLine(title), channel.Title)
	if _, err := app.createNotification("", notificationKindChat, notifyText, "chat", artifact.ID, channel.ID, false); err != nil {
		log.Errorf("Failed to broadcast channel completion notification for artifact %s: %v", artifact.ID, err)
	}
}

// shouldNotifyAgentThreadCreator gates the per-thread terminal notification. A
// /goal subtask child (goalParentId set) is suppressed because the parent goal
// engine notifies the creator once on the goal's terminal state; without this
// gate a single goal with a revised subtask fires one notification per subtask
// attempt (v1/v2/v3), flooding "Finished Recently". Standalone threads
// (no goalParentId) always notify.
func shouldNotifyAgentThreadCreator(artifact meetingMemoryEntry) bool {
	return strings.TrimSpace(artifact.Metadata["goalParentId"]) == ""
}

func agentThreadNotificationText(message string, artifact meetingMemoryEntry) string {
	if title := strings.TrimSpace(artifact.Metadata["title"]); title != "" {
		return message + ": " + title
	}
	return message
}

func (app *kanbanBoardApp) updateQueuedAgentThread(thread scoutAgentThread, workerResult agentThreadWorkerResult) {
	output := strings.TrimSpace(workerResult.Text)
	if output == "" {
		output = thread.Artifact.Text
	}

	status := firstNonEmptyString(workerResult.Metadata["threadStatus"], workerResult.Metadata["status"], "queued")
	message := assistantToolLabel(thread.Mode) + " thread queued"
	switch status {
	case codexJobStatusApprovalRequired:
		message = assistantToolLabel(thread.Mode) + " thread needs approval"
	case codexJobStatusRunning:
		message = assistantToolLabel(thread.Mode) + " thread running"
	}

	metadata := map[string]string{
		"latestThreadRun": thread.ID,
	}
	for key, value := range workerResult.Metadata {
		if strings.TrimSpace(value) != "" {
			metadata[key] = value
		}
	}

	// Only terminal results with real text earn a derived title; queued /
	// running / approval status updates pass "" so updateOSArtifactWithMetadata
	// keeps whatever title the artifact carries — never the stale scaffold
	// prompt from this thread's launch snapshot.
	title := ""
	if status == codexJobStatusComplete && strings.TrimSpace(workerResult.Text) != "" {
		scaffoldTitle := thread.Artifact.Metadata["title"]
		if derived := artifactTitleFromBody(output, scaffoldTitle); derived != "" && derived != scaffoldTitle {
			title = derived
			metadata["titleSource"] = "derived"
		}
	}

	artifact, _, updateErr := app.updateOSArtifactWithMetadata(thread.Artifact.ID, title, output, scoutParticipantName, metadata)
	if updateErr != nil {
		log.Errorf("Failed to update queued Scout thread artifact %s: %v", thread.ID, updateErr)
		broadcastAssistantEvent("error", "Scout thread could not update its queued artifact", map[string]any{
			"tool":     "launch_agent_thread",
			"threadId": thread.ID,
			"artifact": thread.Artifact,
		})
		return
	}

	actions := app.osAssistantActions(thread.Query, thread.Mode, artifact)
	broadcastActions := broadcastNavigationActions(artifact.Metadata["originKind"], actions)
	broadcastSignedInKanbanEvent("memory", app.memorySnapshotForClients(20))
	broadcastAssistantEvent("action", message, map[string]any{
		"tool":       "launch_agent_thread",
		"thread":     scoutAgentThread{ID: thread.ID, Mode: thread.Mode, Query: thread.Query, Status: status, Artifact: artifact, Actions: broadcastActions},
		"artifact":   artifact,
		"actions":    broadcastActions,
		"voiceState": "listening",
	})
	// Keep chat-side thread cards in step with queued/approval states too.
	app.updateScoutChatThreadRefs(thread.ID, status, artifact.ID)
	// Approval gates stall silently otherwise: the creator gets a durable
	// nudge that the worker is waiting on them.
	if status == codexJobStatusApprovalRequired {
		app.notifyAgentThreadCreator(artifact, notificationKindAgent, agentThreadNotificationText(message, artifact))
	}
}

func agentThreadRequestTimeout() time.Duration {
	switch selectedAgentRunnerName() {
	case agentRunnerCodexSidecar, agentRunnerCodexLocal:
		return codexExecConfigFromEnv().Timeout
	case agentRunnerAnthropicFable:
		// The in-process tool loop runs many turns; give it room beyond the
		// single-completion default. Only applies when the orchestrator is
		// selected, so the codex/openai timeouts are unchanged.
		return orchestratorTimeout()
	default:
		return defaultAgentThreadRequestTimeout
	}
}

type agentThreadWorkerResult struct {
	Text     string
	Metadata map[string]string
	Terminal bool
}

// produceAgentThreadArtifactWithWorker is the single seam the AgentRunner
// interface replaces. It selects a runner (anthropic_fable when
// ANTHROPIC_API_KEY is set, else today's codex/openai worker per env), runs the
// job, and drains the async progress channel into the synchronous
// agentThreadWorkerResult the terminal seam in runAgentThread expects. The
// wrapper providers emit their underlying result verbatim, so codex/openai
// paths are byte-for-byte unchanged; only the anthropic path is new.
func (app *kanbanBoardApp) produceAgentThreadArtifactWithWorker(ctx context.Context, thread scoutAgentThread, responder openAITextResponder) (agentThreadWorkerResult, error) {
	job := app.newAgentJob(thread)
	runner := app.selectAgentRunner(job, responder)
	progress, err := runner.RunJob(ctx, job)
	if err != nil {
		return agentThreadWorkerResult{}, err
	}
	// onProgress persists each non-terminal turn onto the running artifact so the
	// progress card advances mid-run; the terminal update is left to the seam in
	// runAgentThread (folding that write here would race it). The office-socket
	// broadcast of these updates is Wave 3's job.
	return drainAgentProgress(progress, func(update AgentProgress) {
		app.persistAgentThreadProgress(thread, update)
	})
}

// persistAgentThreadProgress stamps a runner's per-turn progress (currentStage,
// goalStatus, progressPercent, reviewGate) onto the running thread's artifact.
// Non-terminal only and additive metadata; the current body is preserved.
func (app *kanbanBoardApp) persistAgentThreadProgress(thread scoutAgentThread, update AgentProgress) {
	if app == nil || update.Terminal || strings.TrimSpace(thread.Artifact.ID) == "" {
		return
	}
	metadata := agentProgressMetadata(update)
	if len(metadata) == 0 {
		return
	}
	if _, _, err := app.updateOSArtifactWithMetadata(thread.Artifact.ID, "", thread.Artifact.Text, scoutParticipantName, metadata); err != nil {
		log.Errorf("Failed to persist thread %s progress: %v", thread.ID, err)
	}
}

func (app *kanbanBoardApp) produceAgentThreadArtifact(ctx context.Context, thread scoutAgentThread, responder openAITextResponder) (string, error) {
	if app == nil {
		return "", fmt.Errorf("assistant is unavailable")
	}
	if responder == nil {
		responder = createOpenAITextResponse
	}
	apiKey := app.currentOpenAIAPIKey()
	if strings.TrimSpace(apiKey) == "" {
		return "", fmt.Errorf("OPENAI_API_KEY is not configured")
	}

	output, err := responder(ctx, apiKey, openAITextRequest{
		Model:           meetingBrainModel(),
		Instructions:    app.agentThreadInstructionsForThread(thread),
		Input:           buildAgentThreadInput(thread, app.snapshotState(), app.memorySnapshotForClients(20), time.Now()),
		ReasoningEffort: "low",
		Verbosity:       "medium",
		MaxOutputTokens: 2600,
	})
	if err != nil {
		return "", err
	}
	output = strings.TrimSpace(output)
	if output == "" {
		return "", fmt.Errorf("Scout thread produced no artifact text")
	}

	return output, nil
}

func (app *kanbanBoardApp) currentOpenAIAPIKey() string {
	if app == nil {
		return ""
	}
	app.mu.Lock()
	apiKey := strings.TrimSpace(app.apiKey)
	app.mu.Unlock()
	if apiKey != "" {
		return apiKey
	}
	return strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
}

func buildAgentThreadScaffold(mode string, query string, board kanbanBoardState, memory []meetingMemoryEntry) string {
	contextLine := boardAndMemoryContextLine(board, memory)
	lines := []string{
		"Scout work thread",
		"",
		"Vision: " + compactAssistantLine(query),
		"Status: running",
		"Thread mode: " + assistantToolLabel(mode),
		"Workspace context: " + contextLine,
		"",
		"Execution log",
		"- Scout created the artifact and queued a server-side thread.",
		"- The Realtime 2 voice loop stays free while the worker runs.",
		"- The artifact will update when the worker completes or hits an error.",
	}
	return strings.Join(appendGoalWorkflow(lines, mode, query, contextLine, agentThreadDeliverable(mode), contextLine), "\n")
}

func buildAgentThreadError(thread scoutAgentThread, err error) string {
	lines := []string{
		"Scout work thread",
		"",
		"Vision: " + compactAssistantLine(thread.Query),
		"Status: needs attention",
		"Thread mode: " + assistantToolLabel(thread.Mode),
		"",
		"Execution log",
		"- Scout created the artifact and ran the agent orchestrator.",
		"- Worker error: " + strings.TrimSpace(err.Error()),
		"",
		"Next action: retry the run — the agent orchestrator hit an error, not a missing worker. If it recurs, check the worker logs. This thread does not require reconnecting an external Codex worker.",
	}
	return strings.Join(appendGoalWorkflow(lines, thread.Mode, thread.Query, err.Error(), agentThreadDeliverable(thread.Mode), "worker error recorded on artifact"), "\n")
}

// agentThreadInstructionsForThread is agentThreadInstructions plus the Wave-10
// generation hop: when the thread carries a resolvable tool template (the
// deliverable subtask of a tool-templated goal), the model that writes the
// artifact receives the tool's full A++ prompt with its exact output contract
// taking primacy over the generic workflow headings. Every other thread keeps
// today's per-mode contract unchanged.
func (app *kanbanBoardApp) agentThreadInstructionsForThread(thread scoutAgentThread) string {
	if toolPrompt, ok := app.toolPromptForThread(thread); ok {
		return strings.Join([]string{
			toolPrompt,
			"",
			"Emit ONLY the tool's OUTPUT CONTRACT above, using its exact headings — do not add the generic workflow headings.",
			"Do not claim you performed browser, SSH, repository, or external Codex work unless the input explicitly includes that evidence.",
			"Write in a practical operator voice. Keep it useful as a saved artifact, not a chat reply.",
		}, "\n")
	}
	// A raw-document contract REPLACES the generic workflow instructions: the
	// child's response is the deliverable file itself, and "start with a
	// one-line Vision, then Markdown sections" is exactly the instruction that
	// looped the first live ship_deck into its law-sweep block.
	if raw, ok := rawDocumentContractInstructions(thread.Artifact.Metadata["outputContract"]); ok {
		return raw
	}
	return agentThreadInstructions(thread.Mode)
}

func agentThreadInstructions(mode string) string {
	return strings.Join([]string{
		"You are Scout's server-side work-thread writer for Bonfire OS.",
		"Create the artifact requested by the user while preserving the structured goal workflow.",
		"Start with a one-line Vision, then provide concise Markdown sections for Goal, Context used, Work decomposition, Agent assignment, Dependency coordination, Ordered execution, Review against the original goal, Gate, What worked, Report, Next moves, and Verification.",
		"Use stable headings and short paragraphs or bullets so the artifact viewer can turn the output into a readable brief.",
		agentThreadModeContract(mode),
		"Do not claim you performed browser, SSH, repository, or external Codex work unless the input explicitly includes that evidence.",
		"Write in a practical operator voice. Keep it useful as a saved artifact, not a chat reply.",
		"Mode: " + assistantToolLabel(mode) + ".",
	}, "\n")
}

func buildAgentThreadInput(thread scoutAgentThread, board kanbanBoardState, memory []meetingMemoryEntry, now time.Time) string {
	var builder strings.Builder
	builder.WriteString("Now: ")
	builder.WriteString(now.Format(time.RFC3339))
	builder.WriteString("\nThread id: ")
	builder.WriteString(thread.ID)
	builder.WriteString("\nMode: ")
	builder.WriteString(thread.Mode)
	builder.WriteString("\nUser request: ")
	builder.WriteString(thread.Query)
	builder.WriteString("\n\nBoard and memory context: ")
	builder.WriteString(boardAndMemoryContextLine(board, memory))
	builder.WriteString("\n\nRecent durable memory:\n")
	for _, entry := range memory {
		builder.WriteString("- ")
		builder.WriteString(entry.Kind)
		if title := strings.TrimSpace(entry.Metadata["title"]); title != "" {
			builder.WriteString(" / ")
			builder.WriteString(title)
		}
		builder.WriteString(": ")
		builder.WriteString(compactAssistantLine(entry.Text))
		builder.WriteByte('\n')
	}
	return builder.String()
}

func agentThreadDeliverable(mode string) string {
	switch normalizeAgentThreadMode(mode) {
	case "research":
		return "research brief with thesis, source trail, evidence table, counterarguments, recommendation, and next checks"
	case "design":
		return "design brief with intent, context links, screens, interaction states, responsive plan, handoff notes, and build risks"
	case "grill":
		return "pressure-test scorecard opening with a machine-parseable READINESS: X/10 line, then objections, hard questions, and improved ask"
	case "workflow":
		return "goal-tracked multi-agent workflow artifact with review and shipping gates"
	default:
		return "durable operating artifact with workflow, evidence, and verification notes"
	}
}

func agentThreadModeContract(mode string) string {
	switch normalizeAgentThreadMode(mode) {
	case "research":
		return "For research mode, use these exact readable headings when evidence exists: Executive Summary, Thesis, Evidence, Sources, Counterarguments, Recommendation, Open questions, Next checks, and Worker evidence. Add a short Search tags line near the top. Cite only sources or tool evidence actually used."
	case "design":
		return "For design mode, include these readable sections: Design intent, Context and research used, Core screens, Interaction states, Responsive behavior, Implementation handoff, Risks, and Next checks. If a relevant research brief appears in memory, explicitly say how it shaped the design."
	case "grill":
		return "For grill mode, the first line after the Vision must be exactly 'READINESS: <score>/10' with one decimal (example: 'READINESS: 6.5/10') — this line is machine-parsed, never omit or reformat it. Then include Strongest objections, Tough questions, Revised ask, and Confidence gate."
	case "workflow":
		return "For workflow mode, keep the ten-step goal loop explicit and make the gate status unambiguous."
	default:
		return "For artifact mode, name the decision, evidence, risks, owner, and next move."
	}
}

func agentThreadModeMetadata(mode string) map[string]string {
	switch normalizeAgentThreadMode(mode) {
	case "research":
		return map[string]string{
			"artifactContract": "research_brief_v2",
			"artifactHeadings": "executive summary thesis evidence sources counterarguments recommendation open questions next checks worker evidence",
			"searchTags":       "required",
		}
	case "grill":
		return map[string]string{
			"artifactContract": "grill_scorecard_v2",
			"readinessLine":    "required",
		}
	default:
		return nil
	}
}
