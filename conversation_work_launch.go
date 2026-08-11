package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type scoutChatMessageCommitter func(...scoutChatMessageRecord) (scoutChatThreadRecord, error)

type conversationWorkProjectionPendingError struct{ err error }

// conversationWorkBeforeCardCommitProbe is test-only crash injection at the
// exact boundary where a WorkRun already exists but its conversation card does
// not. Production leaves it nil.
var conversationWorkBeforeCardCommitProbe func(scoutAgentThread) error

// conversationWorkAfterCardCommitProbe is test-only crash injection after the
// exact work card is durable and before the reserved secure carrier is claimed.
var conversationWorkAfterCardCommitProbe func(scoutAgentThread) error

func conversationApprovedWorkOperation(threadID string, requesterEmail string, proposalMessageID string, proposal scoutRouterProposal) (conversationTurnOperation, error) {
	body, err := canonicalJSON(map[string]any{
		"version":  "conversation-approved-work/v1",
		"threadId": strings.TrimSpace(threadID), "requester": normalizeAccountEmail(requesterEmail),
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
	if strings.TrimSpace(work.AgentID) != "" && work.Kind != conversationWorkWorkstream {
		return nil, fmt.Errorf("the addressed agent is not admitted for this work; ask Scout to coordinate it or choose an eligible agent capability")
	}
	if strings.TrimSpace(work.ApprovedProposalID) != "" {
		if work.ApprovedProposalID != userMessage.ID || !conversationApprovedEffectMatches(work, work.ApprovedEffectClass) {
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
	_, sourceBinding, sourceErr := scoutChatSourceWindow(currentSourceThread, userMessage.ID)
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
			launched, err = app.launchGoalThread(goalLaunchSpec{
				Objective: work.Objective, CreatedBy: user.Name, Authority: process.Authority,
				PackageID: work.PackageID, ToolTemplate: process.ID,
				Origin: origin,
			})
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
			PackageID: work.PackageID, ToolTemplate: tool.ID,
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
			PackageID: work.PackageID,
			Origin:    origin,
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

	now := time.Now().UTC()
	assistantMessage := scoutChatMessageRecord{
		ID: "scout-chat-message-work-" + sha256Hex([]byte(userMessage.ID + "\x00" + launched.ID))[:24], Kind: "thread", Role: "scout",
		AuthorName: scoutParticipantName, IntentOutcome: string(conversationIntentStartPrivateWork),
		Text:      firstNonEmptyString(strings.TrimSpace(label), "Private work") + " started — progress and the finished result will stay in this conversation",
		CreatedAt: now.Format(time.RFC3339Nano), CausedByMessageID: userMessage.ID,
		Thread: &scoutChatThreadRef{ID: launched.ID, Mode: launched.Mode, Query: launched.Query, Status: launched.Status, ArtifactID: launched.Artifact.ID},
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

func conversationWorkVisibleLabel(work conversationWorkDecision, fallback string) string {
	switch strings.TrimSpace(work.ToolID) {
	case packagingStudioProcessID:
		return "Presentation"
	case "deck_outline":
		return "Presentation outline"
	case ventureWorkbookToolID:
		return "Financial model"
	case "brand_design_brief":
		return "Design direction"
	}
	if work.Kind == conversationWorkWorkstream {
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
	identity.PackageID = request.PackageID
	identity.Launch = request.Launch
	return identity
}
