package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// computeDirectWorkSourceBinding computes a source binding for a direct work
// launch. When the user message is not yet committed to the thread (the committer
// runs after the work is launched), we need to include it in the window manually.
func computeDirectWorkSourceBinding(thread scoutChatThreadRecord, userMessage scoutChatMessageRecord) (scoutChatSourceBinding, error) {
	sourceMessageID := strings.TrimSpace(userMessage.ID)
	if sourceMessageID == "" {
		return scoutChatSourceBinding{}, fmt.Errorf("source message id is required")
	}
	// Check if the message is already in the thread
	_, source, sourceErr := scoutChatSourceWindow(thread, sourceMessageID)
	if sourceErr == nil {
		return source, nil
	}
	// Message is not yet in thread - compute the binding including it
	digests := make([]string, 0, agentThreadSourceConversationWindow)
	for _, message := range thread.Messages {
		digest, err := scoutChatSourceMessageDigest(thread, message)
		if err != nil {
			return scoutChatSourceBinding{}, err
		}
		digests = append(digests, message.ID+":"+digest)
		if len(digests) > agentThreadSourceConversationWindow-1 {
			digests = digests[1:]
		}
	}
	// Now add the uncommitted user message
	messageDigest, err := scoutChatSourceMessageDigest(thread, userMessage)
	if err != nil {
		return scoutChatSourceBinding{}, err
	}
	digests = append(digests, userMessage.ID+":"+messageDigest)
	if len(digests) > agentThreadSourceConversationWindow {
		digests = digests[1:]
	}
	return scoutChatSourceBinding{
		MessageID:     sourceMessageID,
		MessageDigest: messageDigest,
		WindowDigest:  sha256Hex([]byte(strings.Join(digests, "\n"))),
	}, nil
}

// conversationDirectWorkOperation creates an idempotent operation binding for a
// direct workstream launch (no proposal card). The binding is deterministic so
// crash recovery can find the already-launched work without re-launching.
func conversationDirectWorkOperation(threadID string, sourceMessageID string, requesterEmail string, mode string, objective string, contextRefs string, agentID string) (conversationTurnOperation, error) {
	body, err := canonicalJSON(map[string]any{
		"version":         "conversation-direct-work/v1",
		"threadId":        strings.TrimSpace(threadID),
		"sourceMessageId": strings.TrimSpace(sourceMessageID),
		"requester":       normalizeAccountEmail(requesterEmail),
		"mode":            strings.TrimSpace(mode),
		"objective":       strings.TrimSpace(objective),
		"contextRefs":     strings.TrimSpace(contextRefs),
		"agentId":         strings.TrimSpace(agentID),
	})
	if err != nil {
		return conversationTurnOperation{}, err
	}
	sourceMessageID = strings.TrimSpace(sourceMessageID)
	if sourceMessageID == "" {
		return conversationTurnOperation{}, fmt.Errorf("direct work source message id is required")
	}
	return conversationTurnOperation{
		ID:         "conversation-direct-" + sha256Hex([]byte(sourceMessageID))[:24],
		BodyDigest: sha256Hex(body),
	}, nil
}

// startDirectPublicScoutWorkstream launches a channel workstream immediately
// without requiring a proposal card. This is used when the user's request is
// complete (mode detected, objective clear, no missing inputs) so Scout can
// start work without an approval tap.
func (app *kanbanBoardApp) startDirectPublicScoutWorkstream(
	ctx context.Context,
	user *userAccount,
	thread scoutChatThreadRecord,
	userMessage scoutChatMessageRecord,
	mode string,
	objective string,
	contextRefs string,
	agentID string,
	agentName string,
	replyTo *scoutChatReplyRef,
	commit scoutChatMessageCommitter,
) (map[string]any, error) {
	if app == nil || user == nil || commit == nil || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
		return nil, fmt.Errorf("public workstream is unavailable")
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "research", "design", "grill", "workflow":
	default:
		return nil, fmt.Errorf("workstream mode must be research, design, grill, or workflow")
	}
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return nil, fmt.Errorf("workstream objective is required")
	}
	sourceMessageID := strings.TrimSpace(userMessage.ID)
	if sourceMessageID == "" {
		return nil, fmt.Errorf("source message is unavailable")
	}

	operation, err := conversationDirectWorkOperation(thread.ID, sourceMessageID, user.Email, mode, objective, contextRefs, agentID)
	if err != nil {
		return nil, err
	}

	launched, found, err := app.conversationWorkForOperation(user.Email, thread.ID, operation)
	if err != nil {
		return nil, err
	}

	delegatedProfile := STRIDEProductAgentContextProfile{}
	delegated := false
	directlyTargeted := strings.TrimSpace(agentID) != ""
	if directlyTargeted {
		delegatedProfile, delegated = app.strideAgentContextForChatWork(agentID, thread, mode)
		if !delegated {
			return nil, fmt.Errorf("the selected agent is no longer eligible for this channel or work")
		}
	} else if mode == "research" {
		delegatedProfile, delegated = app.stridePreferredResearchAgentContext()
	}

	// The userMessage may not be committed to the thread yet (the committer runs
	// after launch), so compute the source binding including it if necessary.
	source, sourceErr := computeDirectWorkSourceBinding(thread, userMessage)
	if sourceErr != nil {
		source = scoutChatSourceBinding{MessageID: sourceMessageID}
	}

	if !found {
		spec := agentThreadGoalSpec{
			Objective:           objective,
			ContextRefs:         contextRefs,
			SourceMessageID:     source.MessageID,
			SourceMessageDigest: source.MessageDigest,
			SourceWindowDigest:  source.WindowDigest,
			OperationID:         operation.ID,
			OperationBodyDigest: operation.BodyDigest,
			OriginSurface:       "chat:" + thread.ID,
			RequestedBy:         normalizeAccountEmail(user.Email),
			Visibility:          scoutChatVisibilityPublic,
			Launch: launchFunnelLineage{
				Source:   proposalSourceDeterministicGuard,
				Path:     "direct_channel_workstream",
				Proposer: normalizeAccountEmail(user.Email),
			},
		}
		sourceIndex := scoutChatMessageIndex(thread, sourceMessageID)
		if sourceIndex >= 0 {
			sourceMessage := thread.Messages[sourceIndex]
			if affinity, affinityFound := app.resolveWorkstreamAffinityWithContext(ctx, user, thread, sourceMessage, objective, time.Now().UTC()); affinityFound {
				encodedAffinity, affinityErr := encodeWorkstreamAffinity(affinity)
				if affinityErr != nil {
					return nil, affinityErr
				}
				spec.WorkstreamAffinity = encodedAffinity
				spec.ProjectWorkID = affinity.ProjectThreadID
				spec.ProjectWorkTitle = affinity.ProjectTitle
			}
		}
		if delegated {
			coordinator := ""
			if !directlyTargeted {
				coordinator = scoutParticipantName
			}
			spec = conversationWorkerSpec(agentThreadGoalSpecForProfile(delegatedProfile, coordinator), spec)
		}
		origin := map[string]string{
			"originKind":        agentThreadOriginChannel,
			"originId":          thread.ID,
			"originSurface":     "chat:" + thread.ID,
			"requestedBy":       normalizeAccountEmail(user.Email),
			"sourceMessageId":   source.MessageID,
			"operationId":       operation.ID,
			"operationBodyDigest": operation.BodyDigest,
		}
		launched, err = app.launchAgentThreadWithSpec(mode, objective, user.Name, origin, spec)
		if err != nil {
			return nil, err
		}
	}

	label := conversationWorkVisibleLabel(conversationWorkDecision{Kind: conversationWorkWorkstream, Mode: mode}, assistantToolLabel(mode))
	assistantMessage := scoutChatMessageRecord{
		ID:                fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()),
		Kind:              "thread",
		Role:              "scout",
		AuthorName:        scoutParticipantName,
		IntentOutcome:     string(conversationIntentStartPrivateWork),
		CausedByMessageID: userMessage.ID,
		Text:              firstNonEmptyString(strings.TrimSpace(label), "Work") + " started — progress and the finished result will stay in this channel",
		CreatedAt:         time.Now().UTC().Format(time.RFC3339Nano),
		ReplyTo:           replyTo,
		Thread: &scoutChatThreadRef{
			ID:         launched.ID,
			Mode:       launched.Mode,
			Query:      launched.Query,
			Status:     launched.Status,
			ArtifactID: launched.Artifact.ID,
		},
	}
	if delegated {
		assistantMessage.AuthorName = delegatedProfile.DisplayName
		assistantMessage.Thread = scoutChatThreadRefForAgent(launched, delegatedProfile, "")
		if directlyTargeted {
			assistantMessage.Text = "I'm on it — the work is running now and I'll bring the finished result back here"
		} else {
			assistantMessage.Text = "I tapped " + delegatedProfile.DisplayName + " for this — " + mode + " is running now and the finished result will land here"
			assistantMessage.Thread = scoutChatThreadRefForAgent(launched, delegatedProfile, scoutParticipantName)
		}
	}

	saved, err := commit(userMessage, assistantMessage)
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
		"ok":            true,
		"answer":        assistantMessage,
		"thread":        saved,
		"agentThread":   launched,
		"artifact":      launched.Artifact,
		"actions":       launched.Actions,
		"intentOutcome": string(conversationIntentStartPrivateWork),
	}, nil
}

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
	sourceIndex := scoutChatMessageIndex(thread, source.MessageID)
	if sourceIndex < 0 {
		return nil, fmt.Errorf("proposal source message is unavailable")
	}
	sourceMessage := thread.Messages[sourceIndex]
	_, currentSource, sourceErr := scoutChatSourceWindow(thread, source.MessageID)
	if sourceErr != nil || currentSource.MessageDigest != source.MessageDigest || currentSource.WindowDigest != source.WindowDigest {
		return nil, fmt.Errorf("proposal source changed before work could start")
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
		if affinity, affinityFound := app.resolveWorkstreamAffinityWithContext(ctx, user, thread, sourceMessage, objective, time.Now().UTC()); affinityFound {
			encodedAffinity, affinityErr := encodeWorkstreamAffinity(affinity)
			if affinityErr != nil {
				return nil, affinityErr
			}
			spec.WorkstreamAffinity = encodedAffinity
			spec.ProjectWorkID = affinity.ProjectThreadID
			spec.ProjectWorkTitle = affinity.ProjectTitle
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
