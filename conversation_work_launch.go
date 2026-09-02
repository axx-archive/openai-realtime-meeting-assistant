package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type scoutChatMessageCommitter func(...scoutChatMessageRecord) (scoutChatThreadRecord, error)

// workRequestContextRef is one client-declared Drive attachment on a work
// request (Wave 5 D8): {ref: "file|<id>", sourceId?, sourceRevision?} — the
// Files-surface ref plus the handle /assistant/attachments/from-file returns.
// It is DATA the launcher re-authorizes; it never grants anything by itself.
type workRequestContextRef struct {
	Ref            string `json:"ref"`
	SourceID       string `json:"sourceId,omitempty"`
	SourceRevision string `json:"sourceRevision,omitempty"`
}

type workRequestContextRefsContextKey struct{}

// errWorkRequestContextRefForbidden is the fail-closed launch refusal for a
// requested Drive ref the requester may not read into this destination. HTTP
// doors map it to 403 (workRequestContextRefStatus); nothing is launched.
var errWorkRequestContextRefForbidden = errors.New("a requested Drive file is not readable by the requester")

const workRequestContextRefsMax = 8

// withWorkRequestContextRefs carries the payload's optional contextRefs from
// the chat/work-request door into startConversationPrivateWork, the one seam
// that admits client-declared refs into a launch.
func withWorkRequestContextRefs(ctx context.Context, refs []workRequestContextRef) context.Context {
	if len(refs) == 0 {
		return ctx
	}
	return context.WithValue(ctx, workRequestContextRefsContextKey{}, append([]workRequestContextRef(nil), refs...))
}

func workRequestContextRefsFromContext(ctx context.Context) []workRequestContextRef {
	if ctx == nil {
		return nil
	}
	refs, _ := ctx.Value(workRequestContextRefsContextKey{}).([]workRequestContextRef)
	return refs
}

// decodeWorkRequestContextRefs parses the optional payload field: a list of
// {ref, sourceId?, sourceRevision?} objects or bare "file|<id>" strings.
func decodeWorkRequestContextRefs(raw any) ([]workRequestContextRef, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("contextRefs must be a list")
	}
	if len(items) > workRequestContextRefsMax {
		return nil, fmt.Errorf("at most %d context refs per request", workRequestContextRefsMax)
	}
	refs := make([]workRequestContextRef, 0, len(items))
	for _, item := range items {
		switch value := item.(type) {
		case string:
			refs = append(refs, workRequestContextRef{Ref: strings.TrimSpace(value)})
		case map[string]any:
			refs = append(refs, workRequestContextRef{
				Ref:            strings.TrimSpace(asString(value["ref"])),
				SourceID:       strings.TrimSpace(asString(value["sourceId"])),
				SourceRevision: strings.TrimSpace(asString(value["sourceRevision"])),
			})
		default:
			return nil, fmt.Errorf("contextRefs entries must be objects")
		}
	}
	return refs, nil
}

func workRequestContextRefStatus(err error) int {
	if errors.Is(err, errWorkRequestContextRefForbidden) {
		return http.StatusForbidden
	}
	return http.StatusInternalServerError
}

// authorizeWorkRequestContextRefs validates every requested ref through the
// same resolution /assistant/attachments/from-file uses
// (assistantFileAttachmentSourceForDestination: per-file ACL, trash, promoted
// source, audience intersection with the destination thread). A supplied
// sourceId must be the requester's own Drive-origin grant for that exact file
// at its current revision. Any failure refuses the whole launch.
func (app *kanbanBoardApp) authorizeWorkRequestContextRefs(ctx context.Context, user *userAccount, destination scoutChatThreadRecord, requested []workRequestContextRef) ([]string, error) {
	if len(requested) == 0 {
		return nil, nil
	}
	if app == nil || app.memory == nil || user == nil {
		return nil, errWorkRequestContextRefForbidden
	}
	refs := make([]string, 0, len(requested))
	for _, item := range requested {
		ref := strings.TrimSpace(item.Ref)
		if !strings.HasPrefix(ref, "file|") {
			return nil, errWorkRequestContextRefForbidden
		}
		fileID := strings.TrimSpace(strings.TrimPrefix(ref, "file|"))
		if fileID == "" {
			return nil, errWorkRequestContextRefForbidden
		}
		_, _, revision, ok := app.assistantFileAttachmentSourceForDestination(ctx, user, fileID, destination)
		if !ok {
			return nil, errWorkRequestContextRefForbidden
		}
		requestedRevision := strings.TrimSpace(item.SourceRevision)
		if sourceID := strings.TrimSpace(item.SourceID); sourceID != "" {
			app.pendingAttachmentUploadsMu.Lock()
			grant, found := app.pendingAttachmentUploads[sourceID]
			app.pendingAttachmentUploadsMu.Unlock()
			if !found || grant.State == attachmentSourceRevoked ||
				normalizeAccountEmail(grant.OwnerEmail) != normalizeAccountEmail(user.Email) ||
				strings.TrimSpace(grant.OriginFileID) != fileID || strings.TrimSpace(grant.OriginRevision) != revision ||
				(requestedRevision != "" && requestedRevision != strings.TrimSpace(grant.SourceRevision)) {
				return nil, errWorkRequestContextRefForbidden
			}
		} else if requestedRevision != "" && requestedRevision != revision {
			return nil, errWorkRequestContextRefForbidden
		}
		refs = append(refs, assistantFileContextRef(fileID))
	}
	return canonicalAssistantContextRefs(refs), nil
}

type conversationWorkProjectionPendingError struct{ err error }

// conversationWorkBeforeCardCommitProbe is test-only crash injection at the
// exact boundary where a WorkRun already exists but its conversation card does
// not. Production leaves it nil.
var conversationWorkBeforeCardCommitProbe func(scoutAgentThread) error

// conversationWorkAfterCardCommitProbe is test-only crash injection after the
// exact work card is durable and before the reserved secure carrier is claimed.
var conversationWorkAfterCardCommitProbe func(scoutAgentThread) error

func conversationApprovedWorkOperation(threadID string, requesterEmail string, proposalMessageID string, proposal scoutRouterProposal) (conversationTurnOperation, error) {
	outputContract := firstNonEmptyString(strings.TrimSpace(proposal.ToolID), strings.TrimSpace(proposal.Mode), strings.TrimSpace(proposal.Kind))
	body, err := canonicalJSON(map[string]any{
		"version":  "conversation-approved-work/v2",
		"threadId": strings.TrimSpace(threadID), "requester": normalizeAccountEmail(requesterEmail),
		// Public conversation work currently delivers to the channel that owns the
		// accepted proposal. Name that fact explicitly in the operation body: the
		// same approval can never be replayed into a different audience or output.
		"destinationKind": "channel", "destinationThreadId": strings.TrimSpace(threadID),
		"outputContract":    outputContract,
		"proposalMessageId": strings.TrimSpace(proposalMessageID), "kind": strings.TrimSpace(proposal.Kind),
		"intentOutcome": strings.TrimSpace(proposal.IntentOutcome), "effectClass": strings.TrimSpace(proposal.EffectClass),
		"toolId": strings.TrimSpace(proposal.ToolID), "mode": strings.TrimSpace(proposal.Mode),
		"agentId": strings.TrimSpace(proposal.AgentID), "objective": strings.TrimSpace(proposal.Objective),
		"contextRefs": strings.TrimSpace(proposal.ContextRefs), "packageId": strings.TrimSpace(proposal.PackageID),
		"authority": strings.TrimSpace(proposal.Authority), "fields": proposal.Fields,
	})
	if err != nil {
		return conversationTurnOperation{}, err
	}
	proposalMessageID = strings.TrimSpace(proposalMessageID)
	if proposalMessageID == "" {
		return conversationTurnOperation{}, fmt.Errorf("approved work proposal id is required")
	}
	return conversationTurnOperation{
		ID:         "conversation-approval-" + sha256Hex([]byte(proposalMessageID))[:24],
		BodyDigest: sha256Hex(body),
	}, nil
}

func (err *conversationWorkProjectionPendingError) Error() string {
	if err == nil || err.err == nil {
		return "private work started and its conversation card is pending reconciliation"
	}
	return "private work started and its conversation card is pending reconciliation: " + err.err.Error()
}

func (err *conversationWorkProjectionPendingError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.err
}

func conversationApprovedEffectMatches(work conversationWorkDecision, approvedEffect string) bool {
	approvedEffect = strings.TrimSpace(approvedEffect)
	if approvedEffect == "" {
		return false
	}
	requiredEffect := conversationWorkRequiredEffectClass(work, "")
	if requiredEffect == "" {
		// A conservative model-only approval may hold otherwise private work at
		// the generic governed boundary. It can never mint a more specific
		// effect or expand the launcher's capability.
		return approvedEffect == "governed_effect"
	}
	return approvedEffect == requiredEffect
}

// startConversationPrivateWork is the sole typed-chat launch adapter for a
// start_private_work decision. It re-resolves every internal registry entry and
// addressed-agent assignment server-side, creates one truthful work card, and
// never accepts tool/model/provider/authority input from a client.
func (app *kanbanBoardApp) startConversationPrivateWork(
	ctx context.Context,
	user *userAccount,
	thread scoutChatThreadRecord,
	userMessage scoutChatMessageRecord,
	work conversationWorkDecision,
	contextRefs string,
	source string,
	commit scoutChatMessageCommitter,
) (map[string]any, error) {
	if app == nil || user == nil || commit == nil {
		return nil, fmt.Errorf("private work is unavailable")
	}
	if scoutChatThreadVisibility(thread) != scoutChatVisibilityPrivate {
		return nil, fmt.Errorf("private work can start only in a private conversation")
	}
	work.ContextRefs = strings.TrimSpace(contextRefs)
	// Wave 5 D8: client-declared Drive refs on the request are admitted only
	// after every one re-authorizes for this requester and destination; a
	// single refusal launches nothing.
	if requested := workRequestContextRefsFromContext(ctx); len(requested) > 0 {
		authorized, refErr := app.authorizeWorkRequestContextRefs(ctx, user, thread, requested)
		if refErr != nil {
			return nil, refErr
		}
		work.ContextRefs = encodeAssistantContextRefs(canonicalAssistantContextRefs(append(decodeAssistantContextRefs(work.ContextRefs), authorized...)))
	}
	if strings.TrimSpace(work.AgentID) != "" && work.Kind != conversationWorkWorkstream {
		return nil, fmt.Errorf("the addressed agent is not admitted for this work; ask Scout to coordinate it or choose an eligible agent capability")
	}
	if strings.TrimSpace(work.ApprovedProposalID) != "" {
		if work.ApprovedProposalID != userMessage.ID {
			return nil, fmt.Errorf("approved work does not match the exact held effect")
		}
		requiredEffect := conversationWorkRequiredEffectClass(work, "")
		if requiredEffect != "" && !conversationApprovedEffectMatches(work, work.ApprovedEffectClass) {
			return nil, fmt.Errorf("approved work does not match the exact held effect")
		}
		if requiredEffect == "" && strings.TrimSpace(work.ApprovedEffectClass) != "" && !conversationApprovedEffectMatches(work, work.ApprovedEffectClass) {
			return nil, fmt.Errorf("approved work does not match the exact held effect")
		}
		if err := work.validateRoute(); err != nil {
			return nil, err
		}
	} else {
		if conversationWorkRequiredEffectClass(work, "") != "" {
			return nil, fmt.Errorf("governed work requires an exact accepted proposal")
		}
		if err := work.validatePrivate(); err != nil {
			return nil, err
		}
	}
	if !app.assistantContextRefsReadable(ctx, user, work.ContextRefs) {
		return nil, fmt.Errorf("a source file changed or is no longer readable; attach it again")
	}
	currentSourceThread, _, sourceErr := app.scoutChatThreadByID(user.Email, thread.ID)
	if sourceErr != nil || currentSourceThread.ArchivedAt != "" || scoutChatThreadVisibility(currentSourceThread) != scoutChatVisibilityPrivate {
		return nil, fmt.Errorf("the source conversation changed or is no longer available")
	}
	_, sourceBinding, sourceErr := goalRouteConversationSourceWindow(currentSourceThread, userMessage.ID)
	if sourceErr != nil {
		return nil, fmt.Errorf("the source conversation could not be bound to this work")
	}

	origin := map[string]string{
		"originKind":          agentThreadOriginPrivateThread,
		"originId":            thread.ID,
		"requestedBy":         normalizeAccountEmail(user.Email),
		"sourceMessageId":     userMessage.ID,
		"sourceMessageDigest": sourceBinding.MessageDigest,
		"sourceWindowDigest":  sourceBinding.WindowDigest,
	}
	if work.ApprovedProposalID != "" {
		origin["approvedProposalId"] = work.ApprovedProposalID
		origin["approvedEffectClass"] = work.ApprovedEffectClass
	}
	turnOperation := conversationTurnOperationFromContext(ctx)
	if turnOperation.ID != "" {
		origin["operationId"] = turnOperation.ID
		origin["operationBodyDigest"] = turnOperation.BodyDigest
	}
	if sessionDigest := strideE10TenantSessionHashFromContext(ctx); validStrideE10SessionHash(sessionDigest) {
		origin[openAIToolSessionDigestMetadataKey] = sessionDigest
	}
	originSurface := "chat:" + thread.ID
	origin["originSurface"] = originSurface
	var launched scoutAgentThread
	var label string
	var err error
	var deferredOpenAIToolActivation bool
	var deferredOpenAIToolSpec agentThreadGoalSpec

	switch work.Kind {
	case conversationWorkWorkstream:
		launchPath := "conversation_start_private_work"
		if work.ApprovedProposalID != "" {
			launchPath = "chat_workstream"
		}
		spec := agentThreadGoalSpec{
			Objective: work.Objective, ContextRefs: work.ContextRefs,
			SourceMessageID: userMessage.ID, SourceMessageDigest: sourceBinding.MessageDigest, SourceWindowDigest: sourceBinding.WindowDigest,
			OperationID: turnOperation.ID, OperationBodyDigest: turnOperation.BodyDigest,
			OriginSurface: originSurface, RequestedBy: normalizeAccountEmail(user.Email),
			Authority: work.Authority, Visibility: scoutChatVisibilityPrivate,
			Launch: launchFunnelLineage{Source: source, ProposalID: work.ApprovedProposalID, Path: launchPath, Proposer: normalizeAccountEmail(user.Email)},
		}
		if conversationWorkVisibleLabel(work, "") == "Insights & Opportunities report" {
			spec.WorkLabel = "Insights & Opportunities report"
		}
		if work.Mode == "research" && userMessage.Project != nil {
			projectBinding, bindingErr := app.projectWorkBindingForLaunch(ctx, currentSourceThread, userMessage)
			if bindingErr != nil {
				return nil, bindingErr
			}
			encodedBinding, bindingErr := encodeProjectWorkBinding(projectBinding)
			if bindingErr != nil {
				return nil, bindingErr
			}
			spec.ProjectWorkBinding = encodedBinding
			spec.ProjectWorkID = projectBinding.ProjectID
			spec.ProjectWorkTitle = projectBinding.ProjectTitle
		} else if affinity, found := app.resolveWorkstreamAffinityWithContext(ctx, user, currentSourceThread, userMessage, work.Objective, time.Now().UTC()); found {
			encodedAffinity, affinityErr := encodeWorkstreamAffinity(affinity)
			if affinityErr != nil {
				return nil, affinityErr
			}
			spec.WorkstreamAffinity = encodedAffinity
			spec.ProjectWorkID = affinity.ProjectThreadID
			spec.ProjectWorkTitle = affinity.ProjectTitle
		}
		delegatedProfile, delegated := STRIDEProductAgentContextProfile{}, false
		if work.AgentID != "" {
			delegatedProfile, delegated = app.strideAgentContextForChatWork(work.AgentID, thread, work.Mode)
			if !delegated {
				return nil, fmt.Errorf("the addressed agent is not eligible for this work")
			}
			spec = conversationWorkerSpec(agentThreadGoalSpecForProfile(delegatedProfile, ""), spec)
		} else if work.Mode == "research" {
			delegatedProfile, delegated = app.stridePreferredResearchAgentContext()
			if delegated {
				spec = conversationWorkerSpec(agentThreadGoalSpecForProfile(delegatedProfile, scoutParticipantName), spec)
			}
		}
		if strideE10TenantCutoverEnabled() && app.openAIToolRuntime != nil && app.openAIToolRuntime.Enabled && app.openAIToolRuntime.Carrier != nil && app.openAIToolRuntime.Carrier.Enabled {
			runID := "agent-thread-openai-tool-" + sha256Hex([]byte("conversation-openai-tool-run-v1\x00" + thread.ID + "\x00" + userMessage.ID + "\x00" + work.Objective))[:24]
			envelope, envelopeErr := mintStrideE10TenantAuthorityEnvelopeFromHeldContext(ctx, StrideE10TenantSurfaceScout, StrideE10TenantAuthorityPurposeForScoutThread(runID, work.Mode, work.Objective), time.Now().UTC().Add(strideE10TenantAuthorityEnvelopeMaxTTL))
			if envelopeErr != nil {
				return nil, envelopeErr
			}
			launched, err = app.launchAgentThreadWithSpecAndTenantAuthorityContext(ctx, work.Mode, work.Objective, user.Name, origin, spec, runID, &envelope)
			deferredOpenAIToolActivation, deferredOpenAIToolSpec = err == nil, spec
		} else {
			launched, err = app.launchAgentThreadWithSpec(work.Mode, work.Objective, user.Name, origin, spec)
		}
		label = assistantToolLabel(work.Mode)
		if err == nil && delegated {
			work.AgentID = delegatedProfile.AgentID
			work.AgentName = delegatedProfile.DisplayName
		}

	case conversationWorkRegistryTool:
		if process, ok := processByID(work.ToolID); ok && !process.Hidden {
			workLabel := ""
			if process.ID == documentReportProcessID && scoutInsightsReportRequestDetected(userMessage.Text) {
				workLabel = "Insights & Opportunities report"
			}
			launchSpec := goalLaunchSpec{
				Objective: work.Objective, CreatedBy: user.Name, Authority: process.Authority,
				PackageID: work.PackageID, ToolTemplate: process.ID, ContextRefs: work.ContextRefs,
				WorkLabel: workLabel, Origin: origin,
			}
			if studioProjectKindForProcessID(process.ID) != "" {
				launched, err = app.launchConversationStudioProcessOnce(ctx, user, thread, work, process, turnOperation, launchSpec)
			} else {
				launched, err = app.launchGoalThread(launchSpec)
			}
			label = process.Title
			break
		}
		tool, ok := toolByID(work.ToolID)
		if !ok {
			return nil, fmt.Errorf("the requested output contract is unavailable")
		}
		if tool.ID == ventureWorkbookToolID {
			launched, err = app.createPrivateVentureWorkbookForOperation(thread.ID, userMessage.ID, work.Objective, user, turnOperation)
			label = tool.Name
			break
		}
		launched, err = app.launchGoalThread(goalLaunchSpec{
			Objective: work.Objective, CreatedBy: user.Name, Authority: tool.Authority,
			PackageID: work.PackageID, ToolTemplate: tool.ID, ContextRefs: work.ContextRefs,
			Origin: origin,
		})
		label = tool.Name

	case conversationWorkGoal:
		authority := strings.TrimSpace(work.Authority)
		if authority != codexJobAuthorityReadOnly {
			authority = codexJobAuthorityWorkspaceWrite
		}
		launched, err = app.launchGoalThread(goalLaunchSpec{
			Objective: work.Objective, CreatedBy: user.Name, Authority: authority,
			PackageID: work.PackageID, ContextRefs: work.ContextRefs,
			Origin: origin,
		})
		label = "Goal"

	default:
		return nil, fmt.Errorf("private work route %q is not admitted by this launcher", work.Kind)
	}
	if err != nil {
		return nil, err
	}
	if work.ApprovedProposalID != "" && work.Kind != conversationWorkWorkstream {
		recordProposalEvent(proposalEventLaunched, work.ApprovedProposalID, map[string]any{
			"path": "conversation_approved_work", "thread_id": launched.ID,
		})
	}
	label = conversationWorkVisibleLabel(work, label)
	// The launch objective may be source-expanded before this point. Preserve the
	// named report's concise product label from the exact initiating message,
	// while generic document runs keep the broader "Document" label.
	if work.Kind == conversationWorkRegistryTool && work.ToolID == documentReportProcessID && scoutInsightsReportRequestDetected(userMessage.Text) {
		label = "Insights & Opportunities report"
	}

	now := time.Now().UTC()
	assistantMessage := scoutChatMessageRecord{
		ID: "scout-chat-message-work-" + sha256Hex([]byte(userMessage.ID + "\x00" + launched.ID))[:24], Kind: "thread", Role: "scout",
		AuthorName: scoutParticipantName, IntentOutcome: string(conversationIntentStartPrivateWork),
		Text:      studioProjectLaunchCopy(launched.Artifact.Metadata["processId"], firstNonEmptyString(strings.TrimSpace(label), "Private work")),
		CreatedAt: now.Format(time.RFC3339Nano), CausedByMessageID: userMessage.ID,
		Thread: &scoutChatThreadRef{ID: launched.ID, Mode: launched.Mode, ProcessID: launched.Artifact.Metadata["processId"], Query: launched.Query, Status: launched.Status, ArtifactID: launched.Artifact.ID,
			OutputFamily: firstNonEmptyString(scoutChatOutputFamilyForArtifact(launched.Artifact), scoutChatOutputFamilyForMode(launched.Mode)),
			ProjectID:    launched.Artifact.Metadata["projectWorkId"], ProjectTitle: launched.Artifact.Metadata["projectWorkTitle"]},
	}
	if work.AgentID != "" && work.AgentName != "" {
		assistantMessage.AuthorName = work.AgentName
		assistantMessage.Thread = scoutChatThreadRefForAgent(launched, STRIDEProductAgentContextProfile{AgentID: work.AgentID, DisplayName: work.AgentName}, "")
	}
	if conversationWorkBeforeCardCommitProbe != nil {
		if probeErr := conversationWorkBeforeCardCommitProbe(launched); probeErr != nil {
			return nil, &conversationWorkProjectionPendingError{err: probeErr}
		}
	}
	saved, err := commit(assistantMessage)
	if err != nil {
		if current, _, readErr := app.scoutChatThreadByID(user.Email, thread.ID); readErr == nil && scoutChatMessageIndex(current, assistantMessage.ID) >= 0 {
			saved = current
		} else {
			return nil, &conversationWorkProjectionPendingError{err: err}
		}
	}
	if current, reconcileErr := app.reconcileScoutChatThreadRefAfterCommitWithContext(ctx, user.Email, thread.ID, launched.ID, launched.Artifact.ID); reconcileErr == nil && current.ID != "" {
		saved = current
		for _, currentMessage := range saved.Messages {
			if currentMessage.ID == assistantMessage.ID {
				assistantMessage = currentMessage
				break
			}
		}
	}
	if deferredOpenAIToolActivation {
		if conversationWorkAfterCardCommitProbe != nil {
			if probeErr := conversationWorkAfterCardCommitProbe(launched); probeErr != nil {
				return nil, &conversationWorkProjectionPendingError{err: probeErr}
			}
		}
		if activationErr := app.activateReservedOpenAIToolAgentThread(ctx, launched, deferredOpenAIToolSpec, user.Name); activationErr != nil {
			return nil, &conversationWorkProjectionPendingError{err: activationErr}
		}
	}
	return map[string]any{
		"ok": true, "answer": assistantMessage, "thread": saved,
		"agentThread": launched, "artifact": launched.Artifact, "actions": launched.Actions,
		"intentOutcome": string(conversationIntentStartPrivateWork),
	}, nil
}

// launchConversationStudioProcessOnce gives the two authored-output processes
// a durable exactly-once seam below every conversation entry point. Ordinary
// private turns already journal the HTTP operation before routing, while Home
// openings own a separate reply lease; both can still die after the canonical
// Work root is appended and before its chat card is committed. On reclaim, the
// exact source-bound root is adopted rather than launching a second process.
func (app *kanbanBoardApp) launchConversationStudioProcessOnce(
	ctx context.Context,
	user *userAccount,
	thread scoutChatThreadRecord,
	work conversationWorkDecision,
	process ProcessDefinition,
	operation conversationTurnOperation,
	spec goalLaunchSpec,
) (scoutAgentThread, error) {
	if app == nil || user == nil || app.memory == nil {
		return scoutAgentThread{}, fmt.Errorf("private work is unavailable")
	}
	if operation.ID == "" && operation.BodyDigest == "" {
		return app.launchGoalThread(spec)
	}
	operationID, operationErr := normalizeScoutIdempotencyKey(operation.ID)
	if operationErr != nil || !isHexDigest(operation.BodyDigest) {
		return scoutAgentThread{}, fmt.Errorf("conversation work operation binding is invalid")
	}
	operation.ID = operationID

	// This lock is deliberately distinct from the outer conversation-operation
	// lock. The ordinary message path may already hold that lock when it reaches
	// this seam; a dedicated root lock serializes scan-then-append without
	// deadlocking route-receipt verification, which locks the source thread.
	lockKey := "conversation-studio-root-operation-" + sha256Hex([]byte(normalizeAccountEmail(user.Email) + "\x00" + thread.ID + "\x00" + operation.ID))[:24]
	rootLock := app.scoutChatThreadLock(lockKey)
	rootLock.Lock()
	defer rootLock.Unlock()

	var match scoutAgentThread
	matches := 0
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0) {
		_, plan, canonical := studioProjectCandidate(entry)
		if !canonical {
			continue
		}
		metadata := entry.Metadata
		if strings.TrimSpace(metadata["operationId"]) != operation.ID || strings.TrimSpace(metadata["originId"]) != strings.TrimSpace(thread.ID) ||
			normalizeAccountEmail(metadata["requestedBy"]) != normalizeAccountEmail(user.Email) {
			continue
		}
		matches++
		if matches > 1 {
			return scoutAgentThread{}, fmt.Errorf("%w: conversation operation owns multiple Studio roots", ErrSTRIDEConversationConflict)
		}
		if strings.TrimSpace(metadata["operationBodyDigest"]) != operation.BodyDigest || plan.ProcessID != process.ID ||
			canonicalizeBoardText(plan.Objective) != canonicalizeBoardText(work.Objective) ||
			strings.TrimSpace(plan.PackageID) != strings.TrimSpace(work.PackageID) ||
			plan.ContextRefs != encodeAssistantContextRefs(decodeAssistantContextRefs(work.ContextRefs)) ||
			normalizeCodexJobAuthority(plan.Authority) != normalizeCodexJobAuthority(process.Authority) || plan.RouteReceipt == nil {
			return scoutAgentThread{}, fmt.Errorf("%w: conversation Studio operation binding changed", ErrSTRIDEConversationConflict)
		}
		header := artifactAuthorizationHeaderFromEntry(entry)
		if !artifactHeaderAuthorized(ctx, user, ACLReadContent, header) || !artifactHeaderAuthorized(ctx, user, ACLExecute, header) {
			return scoutAgentThread{}, fmt.Errorf("conversation Studio work is unavailable")
		}
		if err := app.verifyGoalRouteReceipt(&plan, *plan.RouteReceipt); err != nil {
			return scoutAgentThread{}, fmt.Errorf("%w: conversation Studio route changed", ErrSTRIDEConversationConflict)
		}
		query := firstNonEmptyString(strings.TrimSpace(metadata["threadQuery"]), strings.TrimSpace(metadata["objective"]), plan.Objective)
		match = scoutAgentThread{
			ID: firstNonEmptyString(strings.TrimSpace(metadata["threadId"]), plan.GoalID), Mode: "goal", Query: query,
			Status: firstNonEmptyString(agentThreadStatusValue(entry), "running"), Artifact: entry,
		}
		match.Actions = app.osAssistantActions(match.Query, match.Mode, entry)
	}
	if matches == 1 {
		return match, nil
	}
	return app.launchGoalThread(spec)
}

func conversationWorkVisibleLabel(work conversationWorkDecision, fallback string) string {
	switch strings.TrimSpace(work.ToolID) {
	case packagingStudioProcessID:
		return "Presentation"
	case documentReportProcessID:
		lower := strings.ToLower(work.Objective)
		if strings.Contains(lower, "insights") && strings.Contains(lower, "opportunit") {
			return "Insights & Opportunities report"
		}
		return "Document"
	case "deck_outline":
		return "Presentation outline"
	case ventureWorkbookToolID:
		return "Financial model"
	case "brand_design_brief":
		return "Design direction"
	}
	if work.Kind == conversationWorkWorkstream {
		if strings.Contains(strings.ToLower(work.Objective), "insights & opportunities report") {
			return "Insights & Opportunities report"
		}
		switch strings.ToLower(strings.TrimSpace(work.Mode)) {
		case "research":
			return "Research"
		case "design":
			return "Design"
		}
	}
	return firstNonEmptyString(strings.TrimSpace(fallback), "Private work")
}

// conversationWorkerSpec keeps server-resolved worker identity while retaining
// the immutable request/source fields that own replay and reconciliation. An
// agent assignment may change who is visible; it must never erase the turn's
// authority or operation binding.
func conversationWorkerSpec(identity agentThreadGoalSpec, request agentThreadGoalSpec) agentThreadGoalSpec {
	identity.Objective = request.Objective
	identity.ToolTemplate = request.ToolTemplate
	identity.ContextRefs = request.ContextRefs
	identity.SourceMessageID = request.SourceMessageID
	identity.SourceMessageDigest = request.SourceMessageDigest
	identity.SourceWindowDigest = request.SourceWindowDigest
	identity.OperationID = request.OperationID
	identity.OperationBodyDigest = request.OperationBodyDigest
	identity.OriginSurface = request.OriginSurface
	identity.RequestedBy = request.RequestedBy
	identity.Authority = request.Authority
	identity.Visibility = request.Visibility
	identity.WorkLabel = request.WorkLabel
	identity.PackageID = request.PackageID
	identity.ProjectWorkBinding = request.ProjectWorkBinding
	identity.ProjectWorkID = request.ProjectWorkID
	identity.ProjectWorkTitle = request.ProjectWorkTitle
	identity.WorkstreamAffinity = request.WorkstreamAffinity
	identity.Launch = request.Launch
	return identity
}
