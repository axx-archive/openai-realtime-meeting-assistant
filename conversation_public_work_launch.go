package main

import (
	"context"
	"fmt"
	"strings"
)

// startAcceptedPublicScoutWorkstream is the single audience-bound adapter for
// an accepted public-channel workstream proposal. The persisted proposal owns
// mode, objective, sources, agent assignment, and authority. Its deterministic
// operation binding is stamped on the artifact before the async worker starts,
// so a crash or lost response can reconstruct the one card instead of starting
// a second provider run.
func (app *kanbanBoardApp) startAcceptedPublicScoutWorkstream(
	ctx context.Context,
	user *userAccount,
	thread scoutChatThreadRecord,
	proposalMessageID string,
	proposal scoutRouterProposal,
	replyTo *scoutChatReplyRef,
	source scoutChatSourceBinding,
) (map[string]any, error) {
	if app == nil || user == nil || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
		return nil, fmt.Errorf("public workstream is unavailable")
	}
	mode := strings.ToLower(strings.TrimSpace(proposal.Mode))
	switch mode {
	case "research", "design", "grill", "workflow":
	default:
		return nil, fmt.Errorf("workstream mode must be research, design, grill, or workflow")
	}
	objective := strings.TrimSpace(proposal.Objective)
	if objective == "" {
		return nil, fmt.Errorf("workstream objective is required")
	}
	if strings.TrimSpace(source.MessageID) == "" {
		return nil, fmt.Errorf("proposal source message is unavailable")
	}
	if !app.assistantContextRefsReadable(ctx, user, proposal.ContextRefs) {
		return nil, fmt.Errorf("a source file changed or is no longer readable; attach it again before launching")
	}
	operation, err := conversationApprovedWorkOperation(thread.ID, user.Email, proposalMessageID, proposal)
	if err != nil {
		return nil, err
	}

	launched, found, err := app.conversationWorkForOperation(user.Email, thread.ID, operation)
	if err != nil {
		return nil, err
	}
	delegatedProfile := STRIDEProductAgentContextProfile{}
	delegated := false
	directlyTargeted := strings.TrimSpace(proposal.AgentID) != ""
	if directlyTargeted {
		delegatedProfile, delegated = app.strideAgentContextForChatWork(proposal.AgentID, thread, mode)
		if !delegated {
			return nil, fmt.Errorf("the selected agent is no longer eligible for this channel or work; update the assignment before confirming")
		}
	} else if mode == "research" {
		delegatedProfile, delegated = app.stridePreferredResearchAgentContext()
	}

	if !found {
		spec := agentThreadGoalSpec{
			Objective: objective, ContextRefs: proposal.ContextRefs,
			SourceMessageID: source.MessageID, SourceMessageDigest: source.MessageDigest, SourceWindowDigest: source.WindowDigest,
			OperationID: operation.ID, OperationBodyDigest: operation.BodyDigest,
			OriginSurface: "chat:" + thread.ID, RequestedBy: normalizeAccountEmail(user.Email),
			Visibility: scoutChatVisibilityPublic,
			Launch: launchFunnelLineage{
				Source: proposalSourceChatRouter, ProposalID: proposalMessageID,
				Path: "chat_workstream", Proposer: normalizeAccountEmail(user.Email),
			},
		}
		if delegated {
			coordinator := ""
			if !directlyTargeted {
				coordinator = scoutParticipantName
			}
			spec = conversationWorkerSpec(agentThreadGoalSpecForProfile(delegatedProfile, coordinator), spec)
		}
		origin := map[string]string{
			"originKind": agentThreadOriginChannel, "originId": thread.ID,
			"originSurface": "chat:" + thread.ID, "requestedBy": normalizeAccountEmail(user.Email),
			"sourceMessageId": source.MessageID,
			"operationId":     operation.ID, "operationBodyDigest": operation.BodyDigest,
			"approvedProposalId": proposalMessageID,
		}
		launched, err = app.launchAgentThreadWithSpec(mode, objective, user.Name, origin, spec)
		if err != nil {
			return nil, err
		}
	}

	proposalMessage := scoutChatMessageRecord{ID: proposalMessageID}
	assistantMessage := conversationWorkReplayCard(proposalMessage, launched)
	assistantMessage.ReplyTo = replyTo
	label := conversationWorkVisibleLabel(conversationWorkDecision{Kind: conversationWorkWorkstream, Mode: mode}, assistantToolLabel(mode))
	assistantMessage.Text = firstNonEmptyString(strings.TrimSpace(label), "Work") + " started — progress and the finished result will stay in this channel"
	if delegated {
		assistantMessage.AuthorName = delegatedProfile.DisplayName
		assistantMessage.Thread = scoutChatThreadRefForAgent(launched, delegatedProfile, "")
		if directlyTargeted {
			assistantMessage.Text = "I’m on it — the work is running now and I’ll bring the finished result back here"
		} else {
			assistantMessage.Text = "I tapped " + delegatedProfile.DisplayName + " for this — research is running now and the finished brief will land here"
			assistantMessage.Thread = scoutChatThreadRefForAgent(launched, delegatedProfile, scoutParticipantName)
		}
	}

	saved, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, assistantMessage)
	if err != nil {
		return nil, fmt.Errorf("work launched but its chat projection needs reconciliation: %w", err)
	}
	current, err := app.reconcileScoutChatThreadRefAfterCommit(user.Email, thread.ID, launched.ID, launched.Artifact.ID)
	if err != nil {
		return nil, fmt.Errorf("work launched but its chat projection needs reconciliation: %w", err)
	}
	if current.ID != "" {
		saved = current
		for _, message := range current.Messages {
			if message.ID == assistantMessage.ID {
				assistantMessage = message
				break
			}
		}
	}
	return map[string]any{
		"ok": true, "answer": assistantMessage, "thread": saved,
		"agentThread": launched, "artifact": launched.Artifact, "actions": launched.Actions,
		"intentOutcome": string(conversationIntentStartPrivateWork),
	}, nil
}
