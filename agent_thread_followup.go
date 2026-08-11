package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Conversational agent threads: an in-thread reply re-runs the SAME artifact
// in place (stable artifact id, threadVersion bumped, prior body archived
// under a canonical "## Previous run" heading). Follow-ups ALWAYS ride the
// bounded OpenAI text worker — one Responses call, 60s, no codex queue or
// sandbox — even when BONFIRE_CODEX_AGENT_THREADS is set. That is the cost
// envelope: a follow-up can never fan out.
const (
	// agentThreadFollowUpMaxPriorBody caps the latest-version body fed back
	// into the follow-up prompt (bytes, rune-boundary safe).
	agentThreadFollowUpMaxPriorBody = 8000
	// agentThreadFollowUpMaxReplies caps team messages since the last run.
	agentThreadFollowUpMaxReplies = 30
	// agentThreadFollowUpMaxReplyLen caps each reply line fed to the worker.
	agentThreadFollowUpMaxReplyLen = 700
	// agentThreadMaxArchivedRuns bounds artifact growth: older Previous-run
	// sections beyond this are dropped at merge time.
	agentThreadMaxArchivedRuns = 4
	// agentThreadRunLogCap bounds the threadRuns trajectory metadata.
	agentThreadRunLogCap = 12
)

// agentThreadPrevRunHeading is the canonical version boundary. Worker output
// is sanitized against it before every merge so a forged marker can never
// corrupt future splits.
var agentThreadPrevRunHeading = regexp.MustCompile(`(?m)^## Previous run · v(\d+) · .*$`)

// readinessLinePattern parses the machine-readable grill contract line
// ("READINESS: 6.5/10"). Tolerant: case-insensitive, optional spaces, first
// match anywhere in the document wins.
var readinessLinePattern = regexp.MustCompile(`(?mi)^\s*READINESS:\s*([0-9]+(?:\.[0-9]+)?)\s*/\s*10\b`)

// agentThreadFollowUpStatusKeys are snapshotted at launch (inside the
// per-artifact lock) and written back verbatim when the follow-up run fails,
// so an error never clobbers a good artifact's terminal state or version.
var agentThreadFollowUpStatusKeys = []string{"status", "threadStatus", "goalStatus", "currentStage", "progressPercent", "reviewGate", "threadVersion", "latestThreadRun"}

// agentThreadFollowUpRun carries one armed follow-up from launch to the async
// worker, including the pre-run metadata snapshots the error path restores.
type agentThreadFollowUpRun struct {
	thread         scoutAgentThread
	artifactID     string
	runID          string
	version        int
	requestedBy    string
	requesterEmail string
	input          string
	prevMeta       map[string]string
	prevStatus     map[string]string
	// attachmentScope carries only typed source identity and destination
	// bindings. The async worker reauthorizes them and reads bytes immediately
	// before the provider call; launch-time raw blocks are never bearer grants.
	attachmentScope *agentThreadFollowUpAttachmentScope
}

type agentThreadFollowUpAttachmentScope struct {
	destinationID string
	reservationID string
	files         []scoutChatFileAttachment
}

// startAgentThreadFollowUpAsync is the test seam mirroring
// startAgentThreadAsync (agent_thread_runner.go).
var startAgentThreadFollowUpAsync = func(app *kanbanBoardApp, run agentThreadFollowUpRun) {
	go app.runAgentThreadFollowUp(run)
}

// agentThreadRunLock returns the per-artifact mutex serializing follow-up
// validate+mark-running (mirrors scoutChatThreadLock). The model call stays
// outside the lock.
func (app *kanbanBoardApp) agentThreadRunLock(artifactID string) *sync.Mutex {
	app.mu.Lock()
	defer app.mu.Unlock()

	if app.agentThreadRunLocks == nil {
		app.agentThreadRunLocks = map[string]*sync.Mutex{}
	}
	lock, ok := app.agentThreadRunLocks[artifactID]
	if !ok {
		lock = &sync.Mutex{}
		app.agentThreadRunLocks[artifactID] = lock
	}
	return lock
}

// agentThreadStatusValue reads the same status keys the client's
// artifactStatusValue reads: threadStatus first, then status.
func agentThreadStatusValue(artifact meetingMemoryEntry) string {
	return strings.ToLower(strings.TrimSpace(firstNonEmptyString(artifact.Metadata["threadStatus"], artifact.Metadata["status"])))
}

// dispatchArtifactFollowUp is the Wave 6 feedback router: every follow-up
// entry (the chat drop, the headless endpoint) lands here, and the
// deliverable's own provenance decides which worker takes the feedback.
// Agent-thread reports (source scout_thread — including goal writer children,
// which re-run in place exactly as before) go to launchAgentThreadFollowUp
// unchanged; goal-engine deliverables — the goal card itself, process stage
// outputs, packaging ship artifacts — resume their goal with the reply as a
// revision note, re-opening a completed goal when needed. The goal route runs
// BEFORE launchAgentThreadFollowUp's per-artifact lock and terminal-status
// check: a goal parked at a checkpoint reads as still-running by agent-thread
// rules. Deliverables with no goal linkage (an imagery board, a taste
// profile, a hand-saved note) refuse honestly. teamReplies feed only the
// agent-thread path — a goal resume carries the explicit note.
func (app *kanbanBoardApp) dispatchArtifactFollowUp(artifactID string, replyText string, requestedBy string, teamReplies []scoutChatMessageRecord) (scoutAgentThread, error) {
	return app.dispatchArtifactFollowUpWithAttachments(artifactID, replyText, requestedBy, teamReplies, nil)
}

// dispatchArtifactFollowUpWithAttachments is the internal typed-source route.
// The goal-resume route carries the text revision note exactly as before.
func (app *kanbanBoardApp) dispatchArtifactFollowUpWithAttachments(artifactID string, replyText string, requestedBy string, teamReplies []scoutChatMessageRecord, attachmentScope *agentThreadFollowUpAttachmentScope) (scoutAgentThread, error) {
	if app == nil || app.memory == nil {
		return scoutAgentThread{}, fmt.Errorf("assistant is unavailable")
	}
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return scoutAgentThread{}, fmt.Errorf("artifact id is required")
	}
	artifact, ok := app.osArtifactByID(artifactID)
	if !ok {
		return scoutAgentThread{}, fmt.Errorf("that report is unavailable")
	}
	if err := app.artifactFollowUpRouteError(artifact); err != nil {
		return scoutAgentThread{}, err
	}
	if parentID := strings.TrimSpace(artifact.Metadata["goalParentId"]); parentID != "" && artifact.Metadata["source"] == "scout_thread" {
		if err := app.verifyGoalChildRoute(artifact); err != nil {
			return scoutAgentThread{}, err
		}
		return app.resumeGoalWithFeedback(parentID, requestedBy, replyText, artifact.ID)
	}
	if artifact.Metadata["source"] == "scout_thread" {
		return app.launchAgentThreadFollowUpWithAuthorizedSnapshot(artifact, replyText, requestedBy, teamReplies, attachmentScope)
	}
	return app.resumeGoalWithFeedback(artifactGoalParentID(artifact), requestedBy, replyText, artifact.ID)
}

func (app *kanbanBoardApp) dispatchAuthorizedArtifactFollowUpWithAttachments(ctx context.Context, user *userAccount, artifact meetingMemoryEntry, replyText string, requestedBy string, teamReplies []scoutChatMessageRecord, destination scoutChatThreadRecord, files []scoutChatFileAttachment, reservationID string) (scoutAgentThread, error) {
	return app.dispatchAuthorizedArtifactFollowUpWithConversationOperation(ctx, user, artifact, replyText, requestedBy, teamReplies, destination, files, reservationID, nil)
}

func (app *kanbanBoardApp) dispatchAuthorizedArtifactFollowUpWithConversationOperation(ctx context.Context, user *userAccount, artifact meetingMemoryEntry, replyText string, requestedBy string, teamReplies []scoutChatMessageRecord, destination scoutChatThreadRecord, files []scoutChatFileAttachment, reservationID string, binding *conversationFollowUpBinding) (scoutAgentThread, error) {
	artifact, ok := app.revalidateArtifactSnapshot(artifact)
	if !ok {
		return scoutAgentThread{}, fmt.Errorf("that report is unavailable")
	}
	if parentID := strings.TrimSpace(artifact.Metadata["goalParentId"]); parentID != "" && artifact.Metadata["source"] == "scout_thread" {
		if err := app.verifyGoalChildRoute(artifact); err != nil {
			return scoutAgentThread{}, err
		}
		parent, parentOK := authorizedArtifactForActions(ctx, user, parentID, ACLReadContent, ACLExecute, ACLWrite)
		if !parentOK {
			return scoutAgentThread{}, fmt.Errorf("that deliverable's workstream is no longer available")
		}
		plan, planOK := decodeGoalPlan(parent.Metadata["goalPlan"])
		if !planOK || plan.Cancelled {
			return scoutAgentThread{}, fmt.Errorf("that deliverable's workstream is no longer available")
		}
		if binding != nil {
			return app.resumeGoalWithFeedbackAuthorizedOperation(parent, requestedBy, replyText, artifact.ID, binding)
		}
		return app.resumeGoalWithFeedbackAuthorized(parent, requestedBy, replyText, artifact.ID)
	}
	if artifact.Metadata["source"] == "scout_thread" {
		if err := app.artifactFollowUpRouteError(artifact); err != nil {
			return scoutAgentThread{}, err
		}
		requesterEmail := ""
		if user != nil {
			requesterEmail = normalizeAccountEmail(user.Email)
		}
		var attachmentScope *agentThreadFollowUpAttachmentScope
		if len(files) > 0 {
			attachmentScope = &agentThreadFollowUpAttachmentScope{
				destinationID: strings.TrimSpace(destination.ID),
				reservationID: strings.TrimSpace(reservationID),
				files:         append([]scoutChatFileAttachment(nil), files...),
			}
		}
		if binding != nil {
			runID := "agent-thread-followup-" + sha256Hex([]byte(binding.OperationID))[:24]
			return app.launchAgentThreadFollowUpWithTenantAuthoritySnapshot(artifact, replyText, firstNonEmptyString(requesterEmail, requestedBy), teamReplies, attachmentScope, runID, nil, binding)
		}
		return app.launchAgentThreadFollowUpWithAuthorizedSnapshot(artifact, replyText, firstNonEmptyString(requesterEmail, requestedBy), teamReplies, attachmentScope)
	}
	parentID := artifactGoalParentID(artifact)
	if parentID == "" {
		return scoutAgentThread{}, fmt.Errorf("that deliverable is not linked to a workstream that can take feedback yet")
	}
	parent, ok := authorizedArtifactForActions(ctx, user, parentID, ACLReadContent, ACLExecute, ACLWrite)
	if !ok {
		return scoutAgentThread{}, fmt.Errorf("that deliverable's workstream is no longer available")
	}
	plan, ok := decodeGoalPlan(parent.Metadata["goalPlan"])
	if !ok || plan.Cancelled {
		return scoutAgentThread{}, fmt.Errorf("that deliverable's workstream is no longer available")
	}
	if binding != nil {
		return app.resumeGoalWithFeedbackAuthorizedOperation(parent, requestedBy, replyText, artifact.ID, binding)
	}
	return app.resumeGoalWithFeedbackAuthorized(parent, requestedBy, replyText, artifact.ID)
}

func (app *kanbanBoardApp) revalidateArtifactSnapshot(expected meetingMemoryEntry) (meetingMemoryEntry, bool) {
	if app == nil || app.memory == nil || strings.TrimSpace(expected.ID) == "" {
		return meetingMemoryEntry{}, false
	}
	header := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(expected))
	return app.memory.artifactSnapshotIfHeaderMatches(expected.ID, header)
}

// artifactFollowUpRouteError decides whether a follow-up on this artifact can
// reach a worker AT ALL — the permanent-provenance check, split from dispatch
// so Gate A can refuse a drop BEFORE planting the deliverable's card in the
// thread (a card whose copy promises "feedback re-runs it" must never appear
// for an artifact no route will ever take). Transient states — a goal mid-
// drive, a parked checkpoint without a send-back door — stay downstream: the
// deliverable is droppable, feedback just cannot land right now.
func (app *kanbanBoardApp) artifactFollowUpRouteError(artifact meetingMemoryEntry) error {
	if artifact.Metadata["source"] == "scout_thread" {
		if strings.TrimSpace(artifact.Metadata["goalParentId"]) != "" {
			return app.verifyGoalChildRoute(artifact)
		}
		return nil
	}
	goalID := artifactGoalParentID(artifact)
	if goalID == "" {
		return fmt.Errorf("that deliverable is not linked to a workstream that can take feedback yet")
	}
	parent, ok := app.osArtifactByID(goalID)
	if !ok {
		return fmt.Errorf("that deliverable's workstream is no longer available")
	}
	plan, ok := decodeGoalPlan(parent.Metadata["goalPlan"])
	if !ok {
		return fmt.Errorf("that deliverable's workstream is no longer available")
	}
	if plan.Cancelled {
		return fmt.Errorf("that deliverable's goal was cancelled — launch a fresh run instead")
	}
	return nil
}

// launchAgentThreadFollowUp validates and marks an existing agent-thread
// artifact as running a new in-place version, then hands the bounded text
// run to the async worker. Any signed-in user may follow up; the run itself
// is server-side.
func (app *kanbanBoardApp) launchAgentThreadFollowUp(artifactID string, replyText string, requestedBy string, teamReplies []scoutChatMessageRecord) (scoutAgentThread, error) {
	return app.launchAgentThreadFollowUpWithAttachments(artifactID, replyText, requestedBy, teamReplies, nil)
}

// launchAgentThreadFollowUpWithAttachments is launchAgentThreadFollowUp plus
// the triggering reply's typed source scope. Existing entrypoints delegate
// with nil.
func (app *kanbanBoardApp) launchAgentThreadFollowUpWithAttachments(artifactID string, replyText string, requestedBy string, teamReplies []scoutChatMessageRecord, attachmentScope *agentThreadFollowUpAttachmentScope) (scoutAgentThread, error) {
	artifact, ok := app.osArtifactByID(strings.TrimSpace(artifactID))
	if !ok {
		return scoutAgentThread{}, fmt.Errorf("that report is unavailable")
	}
	return app.launchAgentThreadFollowUpWithAuthorizedSnapshot(artifact, replyText, requestedBy, teamReplies, attachmentScope)
}

func (app *kanbanBoardApp) launchAgentThreadFollowUpWithAuthorizedSnapshot(expected meetingMemoryEntry, replyText string, requestedBy string, teamReplies []scoutChatMessageRecord, attachmentScope *agentThreadFollowUpAttachmentScope) (scoutAgentThread, error) {
	if strideE10TenantCutoverEnabled() {
		return scoutAgentThread{}, ErrStrideE10TenantAuthorityStale
	}
	return app.launchAgentThreadFollowUpWithTenantAuthoritySnapshot(expected, replyText, requestedBy, teamReplies, attachmentScope, "", nil, nil)
}

func (app *kanbanBoardApp) launchAgentThreadFollowUpWithTenantAuthority(expected meetingMemoryEntry, replyText string, requestedBy string, teamReplies []scoutChatMessageRecord, attachmentScope *agentThreadFollowUpAttachmentScope, runID string, envelope *StrideE10TenantAuthorityEnvelope) (scoutAgentThread, error) {
	mode := normalizeAgentThreadMode(firstNonEmptyString(expected.Metadata["mode"], expected.Kind))
	if mode == "" {
		mode = "workflow"
	}
	query := firstNonEmptyString(expected.Metadata["threadQuery"], expected.Metadata["title"])
	if !strideE10TenantCutoverEnabled() || envelope == nil || envelope.Purpose != StrideE10TenantAuthorityPurposeForScoutThread(runID, mode, query) || strings.Contains(requestedBy, "@") {
		return scoutAgentThread{}, ErrStrideE10TenantAuthorityStale
	}
	var thread scoutAgentThread
	err := withStrideE10TenantEnvelopeAuthority(context.Background(), envelope, StrideE10TenantSurfaceScout, time.Now().UTC(), func(principal StrideE10TenantPrincipal) error {
		return strideE10ScoutCanonicalExecutionUnavailable(principal, envelope)
	})
	return thread, err
}

func (app *kanbanBoardApp) launchAgentThreadFollowUpWithTenantAuthoritySnapshot(expected meetingMemoryEntry, replyText string, requestedBy string, teamReplies []scoutChatMessageRecord, attachmentScope *agentThreadFollowUpAttachmentScope, reservedRunID string, envelope *StrideE10TenantAuthorityEnvelope, binding *conversationFollowUpBinding) (scoutAgentThread, error) {
	if app == nil || app.memory == nil {
		return scoutAgentThread{}, fmt.Errorf("assistant is unavailable")
	}
	artifactID := strings.TrimSpace(expected.ID)
	if artifactID == "" {
		return scoutAgentThread{}, fmt.Errorf("artifact id is required")
	}

	lock := app.agentThreadRunLock(artifactID)
	lock.Lock()
	defer lock.Unlock()

	artifact, ok := app.revalidateArtifactSnapshot(expected)
	if !ok {
		return scoutAgentThread{}, fmt.Errorf("that report is unavailable")
	}
	if artifact.Metadata["source"] != "scout_thread" {
		return scoutAgentThread{}, fmt.Errorf("follow-ups only run on agent thread reports")
	}
	if strings.TrimSpace(artifact.Metadata["goalParentId"]) != "" {
		return scoutAgentThread{}, fmt.Errorf("goal child feedback must resume its governed parent workstream")
	}
	requesterAccount, requesterOK := authenticatedRequester(requestedBy)
	if !requesterOK {
		return scoutAgentThread{}, fmt.Errorf("the authenticated requester is unavailable; sign in again and retry")
	}
	requesterEmail := normalizeAccountEmail(requesterAccount.Email)
	requestedByName := firstNonEmptyString(participantNameForAccount(requesterAccount), canonicalRoomActorName(requestedBy), strings.TrimSpace(requestedBy))
	if attachmentScope != nil && len(attachmentScope.files) > 0 {
		destination, _, destinationErr := app.scoutChatThreadByID(requesterEmail, attachmentScope.destinationID)
		if destinationErr != nil || !app.followUpAttachmentSourcesAuthorized(requesterAccount, destination, attachmentScope.files, attachmentScope.reservationID) {
			return scoutAgentThread{}, fmt.Errorf("attachment authorization changed; attach the file again")
		}
	}
	mode := normalizeAgentThreadMode(firstNonEmptyString(artifact.Metadata["mode"], artifact.Kind))
	if mode == "" {
		// Same fallback as the rerun action (codex_runner_queue.go).
		mode = "workflow"
	}
	switch agentThreadStatusValue(artifact) {
	case "complete", "published", "error", "failed":
	default:
		return scoutAgentThread{}, fmt.Errorf("thread is still running — wait for it to finish")
	}

	version := 1
	if parsed, err := strconv.Atoi(strings.TrimSpace(artifact.Metadata["threadVersion"])); err == nil && parsed > 0 {
		version = parsed
	}
	nextVersion := version + 1
	runID := strings.TrimSpace(reservedRunID)
	if runID == "" {
		runID = fmt.Sprintf("agent-thread-%s-followup-%d", mode, time.Now().UnixNano())
	}
	// The ORIGINAL threadId keeps ref rewrites flipping the existing chat
	// cards; the fresh runID only records this run.
	threadID := firstNonEmptyString(strings.TrimSpace(artifact.Metadata["threadId"]), runID)
	query := firstNonEmptyString(artifact.Metadata["threadQuery"], artifact.Metadata["title"])
	thread := scoutAgentThread{ID: threadID, Mode: mode, Query: query, Status: "running", Artifact: artifact, TenantAuthority: envelope}
	// Follow-ups are new provider admissions, not authority inherited from v1.
	// Resolve the current named seat before the visible running transition; the
	// worker repeats this fence immediately before its provider call.
	reauthorizedThread, err := app.reauthorizeAgentThreadProfile(agentThreadFollowUpThreadForRequester(thread, requesterEmail))
	if err != nil {
		return scoutAgentThread{}, err
	}
	thread = reauthorizedThread
	artifact = thread.Artifact
	writer := agentThreadArtifactWriter(thread, agentThreadWorkerResult{})

	prevMeta := make(map[string]string, len(artifact.Metadata))
	for key, value := range artifact.Metadata {
		prevMeta[key] = value
	}
	prevStatus := make(map[string]string, len(agentThreadFollowUpStatusKeys))
	for _, key := range agentThreadFollowUpStatusKeys {
		prevStatus[key] = artifact.Metadata[key]
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	// Mark running WITHOUT touching text or title: a failed follow-up must be
	// able to restore the prior good state untouched.
	expectedHeader := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact))
	runningMetadata := map[string]string{
		"status":            "running",
		"threadStatus":      "running",
		"goalStatus":        "running",
		"currentStage":      "execute_in_order",
		"progressPercent":   "35",
		"reviewGate":        "pending",
		"latestThreadRun":   runID,
		"threadVersion":     strconv.Itoa(nextVersion),
		"followUpBy":        requestedByName,
		"followUpStartedAt": now,
		"followUpError":     "",
	}
	if binding != nil {
		receipts, receiptErr := appendConversationFollowUpReceipt(artifact.Metadata, *binding)
		if receiptErr != nil {
			return scoutAgentThread{}, receiptErr
		}
		runningMetadata[conversationFollowUpReceiptMetadataKey] = receipts
	}
	if envelope != nil {
		persistedEnvelope, persistErr := app.persistStrideE10ScoutAuthority(runID, envelope)
		if persistErr != nil {
			return scoutAgentThread{}, persistErr
		}
		envelope = persistedEnvelope
		runningMetadata["tenantAuthorityRef"] = runID
	}
	for _, key := range agentThreadProfileMetadataKeys {
		if value := strings.TrimSpace(thread.Artifact.Metadata[key]); value != "" {
			runningMetadata[key] = value
		}
	}
	if value := strings.TrimSpace(thread.Artifact.Metadata["agentReauthorizedAt"]); value != "" {
		runningMetadata["agentReauthorizedAt"] = value
	}
	updated, matched, err := app.memory.updateOSArtifactWithMetadataIfHeaderMatches(expectedHeader, artifact.ID, "", artifact.Text, writer, runningMetadata)
	if err != nil {
		if !matched {
			return scoutAgentThread{}, fmt.Errorf("that report is unavailable")
		}
		return scoutAgentThread{}, err
	}
	if !matched {
		return scoutAgentThread{}, fmt.Errorf("that report is unavailable")
	}

	actions := app.osAssistantActions(query, mode, updated)
	thread.Artifact = updated
	thread.Actions = actions
	workerThread := agentThreadFollowUpThreadForRequester(thread, requesterEmail)
	principal, principalOK := app.agentThreadRecallPrincipal(requesterEmail, workerThread.Artifact.Metadata)
	var memory []meetingMemoryEntry
	if principalOK {
		memory = app.memorySnapshotForPrincipal(context.Background(), principal, 12)
	}
	input := buildAgentThreadFollowUpInput(workerThread, artifact, nextVersion, replyText, teamReplies, app.snapshotState(), memory, time.Now())

	// Signal capture (signals.go): asking for a re-run means v(N) missed — a
	// negative signal whose payload carries WHAT was asked for. Log-and-continue.
	app.recordSignalEvent(requestedByName, signalEventArtifactRerun, signalValenceNegative, artifact.ID, artifact.Metadata["packageId"], map[string]string{
		"instruction": truncateAgentThreadText(replyText, 500),
	})

	broadcastSignedInKanbanEvent("memory", nil)
	broadcastAssistantEvent("action", assistantToolLabel(mode)+" follow-up running", agentThreadBroadcastMetadata("launch_agent_thread", thread.ID, thread.Status, "listening"))
	app.updateScoutChatThreadRefs(thread.ID, "running", updated.ID)

	startAgentThreadFollowUpAsync(app, agentThreadFollowUpRun{
		thread:          workerThread,
		artifactID:      artifact.ID,
		runID:           runID,
		version:         nextVersion,
		requestedBy:     requestedByName,
		requesterEmail:  requesterEmail,
		input:           input,
		prevMeta:        prevMeta,
		prevStatus:      prevStatus,
		attachmentScope: attachmentScope,
	})
	return thread, nil
}

// agentThreadFollowUpThreadForRequester overrides requester identity only on
// the in-memory provider-admission copy. The durable artifact keeps v1's
// requestedBy authorship provenance, while every v2+ run resolves Files,
// relationship memory, and destination audience for the human acting now.
func agentThreadFollowUpThreadForRequester(thread scoutAgentThread, requesterEmail string) scoutAgentThread {
	metadata := make(map[string]string, len(thread.Artifact.Metadata)+1)
	for key, value := range thread.Artifact.Metadata {
		metadata[key] = value
	}
	metadata["requestedBy"] = normalizeAccountEmail(requesterEmail)
	thread.Artifact.Metadata = metadata
	return thread
}

func (app *kanbanBoardApp) runAgentThreadFollowUp(run agentThreadFollowUpRun) {
	app.runAgentThreadFollowUpWithResponder(run, createOpenAITextResponse)
}

func (app *kanbanBoardApp) runAgentThreadFollowUpWithResponder(run agentThreadFollowUpRun, responder openAITextResponder) {
	if !strideE10TenantCutoverEnabled() {
		app.runAgentThreadFollowUpWithResponderAuthorized(run, responder)
		return
	}
	envelope, err := app.strideE10ScoutThreadEnvelope(run.thread)
	if err != nil || envelope.Purpose != StrideE10TenantAuthorityPurposeForScoutThread(run.runID, run.thread.Mode, run.thread.Query) {
		return
	}
	err = withStrideE10TenantEnvelopeAuthority(context.Background(), envelope, StrideE10TenantSurfaceScout, time.Now().UTC(), func(principal StrideE10TenantPrincipal) error {
		return strideE10ScoutCanonicalExecutionUnavailable(principal, envelope)
	})
	_ = err
}

func (app *kanbanBoardApp) runAgentThreadFollowUpWithResponderAuthorized(run agentThreadFollowUpRun, responder openAITextResponder) {
	// Always the default text-worker timeout — never codexExecConfigFromEnv():
	// follow-ups do not consult the configured agent-thread worker mode.
	ctx, cancel := agentThreadRequestContext(context.Background(), run.thread)
	defer cancel()

	if responder == nil {
		responder = createOpenAITextResponse
	}
	// The worker stamp records which model actually wrote this version — the
	// evidence footer must stay honest across the Sonnet migration.
	worker := agentThreadWorkerOpenAI
	workerBoundary := "responses_artifact_writer"
	output, err := func() (string, error) {
		// A v2+ run must re-prove the same identity, source, requester, and
		// destination contracts as v1. This refreshes approved learning and
		// rejects a paused seat or revoked File before either provider sees data.
		freshThread, authErr := app.reauthorizeAgentThreadProfile(run.thread)
		if authErr != nil {
			return "", authErr
		}
		if _, authErr = app.agentThreadProviderContext(ctx, freshThread); authErr != nil {
			return "", authErr
		}
		run.thread = freshThread
		instructions := app.agentThreadFollowUpInstructionsForThread(run.thread, run.version)
		var openAIAttachments []openAIInputContent
		if run.attachmentScope != nil && len(run.attachmentScope.files) > 0 {
			requester := accountStore().findUser(run.requesterEmail)
			if requester == nil {
				return "", fmt.Errorf("attachment authorization changed; sign in again and attach the file")
			}
			destination, _, destinationErr := app.scoutChatThreadByID(run.requesterEmail, run.attachmentScope.destinationID)
			if destinationErr != nil || !app.followUpAttachmentSourcesAuthorized(requester, destination, run.attachmentScope.files, run.attachmentScope.reservationID) {
				return "", fmt.Errorf("attachment authorization changed before the workstream could read it; attach the file again")
			}
			// Construct provider bytes only after the provider-time authority
			// check. The reader rechecks each source around I/O, then the final
			// fence below catches revocation or audience changes during the read.
			openAIAttachments = app.followUpOpenAIAttachmentContentAuthorized(requester, destination, run.attachmentScope.files, run.attachmentScope.reservationID)
			if !app.followUpAttachmentSourcesAuthorized(requester, destination, run.attachmentScope.files, run.attachmentScope.reservationID) {
				return "", fmt.Errorf("attachment authorization changed before the workstream could read it; attach the file again")
			}
		}
		var raw string
		var responderErr error
		apiKey := app.currentOpenAIAPIKey()
		if strings.TrimSpace(apiKey) == "" {
			return "", fmt.Errorf("OPENAI_API_KEY is not configured")
		}
		liveWebSearch := agentThreadUsesLiveWebSearch(run.thread)
		raw, responderErr = responder(ctx, apiKey, openAITextRequest{
			Model:           agentThreadTextModel(run.thread),
			Seat:            seatFollowup,
			Workflow:        firstNonEmptyString(strings.TrimSpace(run.thread.Artifact.Metadata["toolTemplate"]), "agent_thread_followup_"+normalizeAgentThreadMode(run.thread.Mode)),
			Instructions:    instructions,
			Input:           run.input,
			Attachments:     openAIAttachments,
			ReasoningEffort: agentThreadTextReasoningEffort(run.thread),
			Verbosity:       "medium",
			MaxOutputTokens: agentThreadMaxOutputTokensForThread(run.thread),
			EnableWebSearch: liveWebSearch,
			ValidateOutput: func(text string) error {
				return validateAgentThreadTerminalArtifact(run.thread, text)
			},
		})
		if responderErr != nil {
			return "", responderErr
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return "", fmt.Errorf("%s follow-up produced no artifact text", agentThreadArtifactWriter(run.thread, agentThreadWorkerResult{}))
		}
		if _, authErr := app.agentThreadProviderContext(ctx, run.thread); authErr != nil {
			return "", authErr
		}
		if qualityErr := validateAgentThreadTerminalArtifact(run.thread, raw); qualityErr != nil {
			return "", qualityErr
		}
		return raw, nil
	}()

	// Re-read: the title (or text, via manual edits) may have changed while
	// the worker ran; the merge and restore both build on the stored state.
	prev, ok := app.osArtifactByID(run.artifactID)
	if !ok {
		log.Errorf("Follow-up artifact %s disappeared mid-run", run.artifactID)
		return
	}

	if err != nil {
		// Failed follow-ups never clobber a good body: metadata-only restore
		// of the pre-run terminal state plus the error stamp.
		metadata := make(map[string]string, len(run.prevStatus)+1)
		for key, value := range run.prevStatus {
			metadata[key] = value
		}
		metadata["followUpError"] = err.Error()
		metadata["followUpStatus"] = "needs_attention"
		metadata["followUpErrorAt"] = time.Now().UTC().Format(time.RFC3339Nano)
		metadata["progressNote"] = "Latest Scout revision needs attention — the last accepted deliverable is still available."
		artifact, _, updateErr := app.updateOSArtifactWithMetadata(run.artifactID, "", prev.Text, agentThreadArtifactWriter(run.thread, agentThreadWorkerResult{}), metadata)
		if updateErr != nil {
			log.Errorf("Failed to restore follow-up artifact %s: %v", run.artifactID, updateErr)
			return
		}
		message := assistantToolLabel(run.thread.Mode) + " follow-up needs attention"
		prevRefStatus := firstNonEmptyString(run.prevStatus["threadStatus"], run.prevStatus["status"], "complete")
		broadcastSignedInKanbanEvent("memory", nil)
		broadcastAssistantEvent("action", message, agentThreadBroadcastMetadata("launch_agent_thread", run.thread.ID, prevRefStatus, "listening"))
		app.updateScoutChatThreadRefs(run.thread.ID, prevRefStatus, artifact.ID)
		app.notifyAgentThreadFollowUp(artifact, message)
		return
	}

	prevStampedAt := firstNonEmptyString(run.prevMeta["completedAt"], run.prevMeta["updatedAt"])
	newText := mergeAgentThreadVersions(prev.Text, output, run.version-1, prevStampedAt)
	metadata := map[string]string{
		"status":          "complete",
		"threadStatus":    "complete",
		"goalStatus":      "verified",
		"currentStage":    "verify_goal_completed",
		"progressPercent": "100",
		"reviewGate":      "passed",
		"completedAt":     time.Now().UTC().Format(time.RFC3339Nano),
		"latestThreadRun": run.runID,
		"worker":          worker,
		"workerBoundary":  workerBoundary,
		"followUpError":   "",
		"followUpStatus":  "complete",
		"progressNote":    "",
	}
	for _, key := range agentThreadProfileMetadataKeys {
		if value := strings.TrimSpace(run.thread.Artifact.Metadata[key]); value != "" {
			metadata[key] = value
		}
	}
	for key, value := range researchArtifactEvidenceMetadata(run.thread, output) {
		metadata[key] = value
	}
	if value := strings.TrimSpace(run.thread.Artifact.Metadata["agentReauthorizedAt"]); value != "" {
		metadata["agentReauthorizedAt"] = value
	}
	stampReadinessMetadata(prev, run.thread.Mode, output, metadata)
	appendThreadRunLog(prev, metadata, run.runID, run.version, run.requestedBy)

	var artifact meetingMemoryEntry
	updateErr := app.withCurrentAgentThreadSource(run.thread, func() error {
		var innerErr error
		artifact, _, innerErr = app.updateOSArtifactWithMetadata(run.artifactID, "", newText, agentThreadArtifactWriter(run.thread, agentThreadWorkerResult{}), metadata)
		return innerErr
	})
	if updateErr != nil {
		log.Errorf("Failed to update follow-up artifact %s: %v", run.artifactID, updateErr)
		broadcastAssistantEvent("error", "Scout follow-up could not update its artifact", agentThreadBroadcastMetadata("launch_agent_thread", run.thread.ID, "error", ""))
		return
	}
	// A follow-up is its own attributable terminal run. Give the ledger and
	// pending-learning seam the fresh run id while keeping the stable thread id
	// for the UI card and origin delivery below.
	terminalRun := run.thread
	terminalRun.ID = run.runID
	terminalRun.Status = "complete"
	terminalRun.Artifact = artifact
	app.appendAgentRunLogEntry(terminalRun, artifact, "complete", output)

	message := assistantToolLabel(run.thread.Mode) + " follow-up complete"
	broadcastSignedInKanbanEvent("memory", nil)
	broadcastAssistantEvent("action", message, agentThreadBroadcastMetadata("launch_agent_thread", run.thread.ID, "complete", "listening"))
	app.updateScoutChatThreadRefs(run.thread.ID, "complete", artifact.ID)
	// Same terminal contract as the primary seam (runAgentThread): the board
	// card advances (idempotent — moveTicket reports changed=false when the
	// card is already Done) and the completion card closes the loop back to
	// the origin surface (idempotent via the deliveredAt stamp, so artifacts
	// whose v1 already delivered are naturally skipped). This matters most on
	// the error→follow-up-success path, where v1 left the card Blocked and
	// never delivered.
	app.syncLinkedCardForArtifact(artifact, "complete")
	app.deliverArtifactToOrigin(artifact, run.thread.ID)
	app.notifyAgentThreadFollowUp(artifact, message)
}

// notifyAgentThreadFollowUp notifies the artifact creator (as every terminal
// seam does) AND, when a different teammate asked for the follow-up, that
// requester too. Grill completions carry the readiness dial in the text.
func (app *kanbanBoardApp) notifyAgentThreadFollowUp(artifact meetingMemoryEntry, message string) {
	text := agentThreadNotificationText(message, artifact) + readinessNotificationSuffix(artifact.Metadata)
	app.notifyAgentThreadCreator(artifact, notificationKindAgent, text)
	followUpEmail := normalizeAccountEmail(participantEmail(artifact.Metadata["followUpBy"]))
	creatorEmail := normalizeAccountEmail(participantEmail(artifact.Metadata["createdBy"]))
	if followUpEmail == "" || followUpEmail == creatorEmail {
		return
	}
	if _, err := app.createNotification(followUpEmail, notificationKindAgent, text, "", artifact.ID, "", false); err != nil {
		log.Errorf("Failed to create follow-up notification: %v", err)
	}
}

// readinessNotificationSuffix renders the re-grill dial for notification text
// when a delta exists: " (readiness 6.2 → 7.1)".
func readinessNotificationSuffix(metadata map[string]string) string {
	previous := strings.TrimSpace(metadata["readinessPrevScore"])
	next := strings.TrimSpace(metadata["readinessScore"])
	if previous == "" || next == "" {
		return ""
	}
	return fmt.Sprintf(" (readiness %s → %s)", previous, next)
}

// agentThreadFollowUpInstructions extends the base mode instructions with the
// tighter follow-up deliverable contract.
func agentThreadFollowUpInstructions(mode string, version int) string {
	lines := []string{
		agentThreadInstructions(mode),
		fmt.Sprintf("This is follow-up run v%d revising an existing artifact. Rewrite the FULL deliverable, not a diff. Keep prior sections that still hold; update or delete what the team's replies changed.", version),
		fmt.Sprintf("Add a 'What changed in v%d' section immediately after the Vision line (for grill mode: immediately after the READINESS line) listing the specific changes and which team reply drove each.", version),
		"Do not reproduce the 'Previous run' archive sections — output only the new version.",
	}
	if normalizeAgentThreadMode(mode) == "grill" {
		lines = append(lines, "Re-score honestly: the READINESS line must reflect the answers actually given, not effort. If an objection was resolved, name it; if dodged, keep the score flat and say why.")
	}
	return strings.Join(lines, "\n")
}

// agentThreadFollowUpInstructionsForThread preserves the initial-run contract:
// one neutral workflow base, exactly one named coworker identity, and the
// authenticated requester's surface-specific relationship-memory lane.
func (app *kanbanBoardApp) agentThreadFollowUpInstructionsForThread(thread scoutAgentThread, version int) string {
	identityContext := strings.TrimSpace(strings.Join([]string{
		agentThreadPersonaInstruction(thread.Artifact.Metadata),
		app.agentThreadRequesterRelationshipInstruction(thread),
	}, "\n\n"))
	return strings.TrimSpace(agentThreadFollowUpInstructions(thread.Mode, version) + "\n\n" + identityContext)
}

// buildAgentThreadFollowUpInput mirrors buildAgentThreadInput and adds the
// prior body, the team replies that landed since the last run, and the
// explicit follow-up request. Memory context is smaller (12) than the initial
// run's — the artifact body is the primary context now.
func buildAgentThreadFollowUpInput(thread scoutAgentThread, artifact meetingMemoryEntry, version int, replyText string, teamReplies []scoutChatMessageRecord, board kanbanBoardState, memory []meetingMemoryEntry, now time.Time) string {
	latest, _ := splitAgentThreadVersions(artifact.Text)
	var builder strings.Builder
	builder.WriteString("Now: ")
	builder.WriteString(now.Format(time.RFC3339))
	builder.WriteString("\nThread id: ")
	builder.WriteString(thread.ID)
	builder.WriteString("\nMode: ")
	builder.WriteString(thread.Mode)
	builder.WriteString(fmt.Sprintf("\nRun: follow-up v%d", version))
	builder.WriteString("\nUser request: ")
	builder.WriteString(thread.Query)
	builder.WriteString(fmt.Sprintf("\n\nPrior artifact (v%d) body:\n", version-1))
	builder.WriteString(truncateAgentThreadText(latest, agentThreadFollowUpMaxPriorBody))
	builder.WriteString("\n\nTeam replies since the last run (chronological):\n")
	replies := teamReplies
	if len(replies) > agentThreadFollowUpMaxReplies {
		replies = replies[len(replies)-agentThreadFollowUpMaxReplies:]
	}
	if len(replies) == 0 {
		builder.WriteString("- (none)\n")
	}
	for _, message := range replies {
		line := truncateAgentThreadText(compactAssistantLine(scoutChatMessageModelText(message)), agentThreadFollowUpMaxReplyLen)
		builder.WriteString(fmt.Sprintf("- [%s · %s] %s\n", firstNonEmptyString(strings.TrimSpace(message.AuthorName), "teammate"), scoutChatReplyClock(message.CreatedAt), line))
	}
	builder.WriteString("\nFollow-up request: ")
	builder.WriteString(strings.TrimSpace(replyText))
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

func scoutChatReplyClock(createdAt string) string {
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(createdAt)); err == nil {
		return parsed.UTC().Format("15:04")
	}
	return "earlier"
}

// truncateAgentThreadText truncates on a byte budget while backing off to a
// rune boundary (same idiom as sanitizeScoutChatFiles).
func truncateAgentThreadText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) && len(value) > 0 {
		value = value[:len(value)-1]
	}
	return strings.TrimSpace(value)
}

// splitAgentThreadVersions splits an artifact body at the first Previous-run
// heading: latest = the live version, archive = every archived section.
func splitAgentThreadVersions(text string) (string, string) {
	loc := agentThreadPrevRunHeading.FindStringIndex(text)
	if loc == nil {
		return strings.TrimSpace(text), ""
	}
	latest := strings.TrimSpace(text[:loc[0]])
	// Drop the trailing horizontal rule that separates versions.
	latest = strings.TrimSpace(strings.TrimSuffix(latest, "---"))
	return latest, strings.TrimSpace(text[loc[0]:])
}

// stripAgentThreadRunMarkers removes any line in worker output matching the
// version-boundary heading so a forged marker cannot break future splits.
func stripAgentThreadRunMarkers(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if agentThreadPrevRunHeading.MatchString(line) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// mergeAgentThreadVersions assembles the new in-place version: sanitized new
// output on top, prior latest body archived under the canonical heading, and
// the existing archive trimmed to agentThreadMaxArchivedRuns sections.
func mergeAgentThreadVersions(priorText string, newOutput string, prevVersion int, prevStampedAt string) string {
	sanitized := stripAgentThreadRunMarkers(newOutput)
	latest, archive := splitAgentThreadVersions(priorText)
	if prevVersion < 1 {
		prevVersion = 1
	}
	heading := fmt.Sprintf("## Previous run · v%d · %s", prevVersion, firstNonEmptyString(strings.TrimSpace(prevStampedAt), "unknown time"))
	archiveBlock := heading + "\n\n" + latest
	if archive != "" {
		archiveBlock += "\n\n" + archive
	}
	archiveBlock = capAgentThreadArchive(archiveBlock, agentThreadMaxArchivedRuns)
	return sanitized + "\n\n---\n\n" + archiveBlock
}

func capAgentThreadArchive(archive string, maxSections int) string {
	locs := agentThreadPrevRunHeading.FindAllStringIndex(archive, -1)
	if maxSections <= 0 || len(locs) <= maxSections {
		return archive
	}
	return strings.TrimSpace(archive[:locs[maxSections][0]])
}

// parseReadinessScore returns the first READINESS line's score, clamped to
// [0,10] and rounded to one decimal.
func parseReadinessScore(text string) (float64, bool) {
	match := readinessLinePattern.FindStringSubmatch(text)
	if len(match) < 2 {
		return 0, false
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, false
	}
	if value < 0 {
		value = 0
	}
	if value > 10 {
		value = 10
	}
	return math.Round(value*10) / 10, true
}

func formatReadiness(value float64) string {
	return strconv.FormatFloat(value, 'f', 1, 64)
}

// stampReadinessMetadata applies the grill READINESS contract at every
// artifact-finalizing seam. Fail-soft: a missing/reformatted line leaves the
// prior score untouched (stale beats wrong) and flags readinessParse=missing.
func stampReadinessMetadata(prev meetingMemoryEntry, mode string, output string, metadata map[string]string) {
	if normalizeAgentThreadMode(firstNonEmptyString(mode, prev.Metadata["mode"])) != "grill" {
		return
	}
	score, ok := parseReadinessScore(output)
	if !ok {
		metadata["readinessParse"] = "missing"
		return
	}
	metadata["readinessParse"] = ""
	metadata["readinessScore"] = formatReadiness(score)
	prevScore := strings.TrimSpace(prev.Metadata["readinessScore"])
	if prevScore == "" {
		return
	}
	metadata["readinessPrevScore"] = prevScore
	if prevValue, err := strconv.ParseFloat(prevScore, 64); err == nil {
		metadata["readinessDelta"] = fmt.Sprintf("%+.1f", score-prevValue)
	}
}

// agentThreadRunLogEntry is one row of the compact threadRuns trajectory the
// package binder charts (score omitted for non-grill runs).
type agentThreadRunLogEntry struct {
	Version int    `json:"v"`
	At      string `json:"at"`
	Run     string `json:"run,omitempty"`
	Score   string `json:"score,omitempty"`
	By      string `json:"by,omitempty"`
}

// appendThreadRunLog appends this run to metadata["threadRuns"], backfilling
// the prior run for artifacts written before the log existed, capped at
// agentThreadRunLogCap. Decode failures start the log fresh.
func appendThreadRunLog(prev meetingMemoryEntry, metadata map[string]string, runID string, version int, by string) {
	var runs []agentThreadRunLogEntry
	if raw := strings.TrimSpace(prev.Metadata["threadRuns"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &runs); err != nil {
			runs = nil
		}
	}
	if len(runs) == 0 && version > 1 {
		runs = append(runs, agentThreadRunLogEntry{
			Version: version - 1,
			At:      firstNonEmptyString(prev.Metadata["completedAt"], prev.Metadata["updatedAt"]),
			Run:     firstNonEmptyString(prev.Metadata["latestThreadRun"], prev.Metadata["threadId"]),
			Score:   strings.TrimSpace(prev.Metadata["readinessScore"]),
			By:      strings.TrimSpace(prev.Metadata["createdBy"]),
		})
	}
	runs = append(runs, agentThreadRunLogEntry{
		Version: version,
		At:      time.Now().UTC().Format(time.RFC3339Nano),
		Run:     runID,
		Score:   strings.TrimSpace(metadata["readinessScore"]),
		By:      strings.TrimSpace(by),
	})
	if len(runs) > agentThreadRunLogCap {
		runs = runs[len(runs)-agentThreadRunLogCap:]
	}
	if raw, err := json.Marshal(runs); err == nil {
		metadata["threadRuns"] = string(raw)
	}
}
