package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	openAIToolActivationStateMetadataKey = "openAIToolActivationState"
	openAIToolActivationOwnerMetadataKey = "openAIToolActivationOwner"
	openAIToolActivationReserved         = "reserved"
	openAIToolActivationStarted          = "started"
	openAIToolActivationFinalizing       = "finalizing"
	openAIToolActivationComplete         = "complete"
	openAIToolActivationNeedsAttention   = "needs_attention"
)

// openAIToolAfterActivationCommitProbe is a test-only crash seam after the
// durable started claim and before any process-local worker exists.
var openAIToolAfterActivationCommitProbe func(scoutAgentThread) error
var openAIToolAfterFailureArtifactCommitProbe func(meetingMemoryEntry) error
var openAIToolAfterWorkflowProvenanceProbe func(scoutAgentThread) error

func (app *kanbanBoardApp) currentOpenAIToolActivationOwnerLocked() string {
	if app.openAIToolActivationOwner == "" {
		app.openAIToolActivationOwner = uuid.NewString()
	}
	if app.openAIToolActiveRuns == nil {
		app.openAIToolActiveRuns = map[string]struct{}{}
	}
	return app.openAIToolActivationOwner
}

func (app *kanbanBoardApp) forgetOpenAIToolActiveRun(artifactID string) {
	if app == nil {
		return
	}
	app.openAIToolActivationMu.Lock()
	delete(app.openAIToolActiveRuns, strings.TrimSpace(artifactID))
	app.openAIToolActivationMu.Unlock()
}

func openAIToolSpecFromArtifact(artifact meetingMemoryEntry) agentThreadGoalSpec {
	metadata := artifact.Metadata
	return agentThreadGoalSpec{
		Objective: metadata["objective"], ToolTemplate: metadata["toolTemplate"], ContextRefs: metadata["contextRefs"],
		SourceMessageID: metadata["sourceMessageId"], SourceMessageDigest: metadata["sourceMessageDigest"], SourceWindowDigest: metadata["sourceWindowDigest"],
		OperationID: metadata["operationId"], OperationBodyDigest: metadata["operationBodyDigest"], OriginSurface: metadata["originSurface"],
		RequestedBy: metadata["requestedBy"], Authority: metadata["authority"], Visibility: metadata["visibility"], PackageID: metadata["packageId"],
		AgentID: metadata["agentId"], AgentName: metadata["agentName"], AgentRole: metadata["agentRole"], AgentOutcome: metadata["agentOutcome"],
		AgentPersona: metadata["agentPersona"], AgentVoice: metadata["agentVoice"], AgentStyle: metadata["agentStyle"], AgentTraits: metadata["agentTraits"],
		AgentCapabilities: metadata["agentCapabilities"], AgentMemoryPolicy: metadata["agentMemoryPolicy"], AgentCoreMemories: metadata["agentCoreMemories"],
		AgentActiveLearning: metadata["agentActiveLearning"], AgentDigest: metadata["agentDigest"], DelegatedBy: metadata["delegatedBy"],
		AssignedRunner: metadata["assignedRunner"], OutputContract: metadata["outputContract"], ParentGoalRouteDigest: metadata["goalRouteDigest"],
		Deliverable: strings.EqualFold(strings.TrimSpace(metadata["goalDeliverable"]), "true"),
	}
}

func (app *kanbanBoardApp) openAIToolThreadFromArtifact(artifact meetingMemoryEntry) (scoutAgentThread, error) {
	if app == nil || strings.TrimSpace(artifact.ID) == "" || strings.TrimSpace(artifact.Metadata["source"]) != "scout_thread" {
		return scoutAgentThread{}, ErrStrideE10TenantAuthorityStale
	}
	thread := scoutAgentThread{
		ID: strings.TrimSpace(artifact.Metadata["threadId"]), Mode: normalizeAgentThreadMode(artifact.Metadata["mode"]),
		Query: canonicalizeBoardText(artifact.Metadata["threadQuery"]), Status: firstNonEmptyString(strings.TrimSpace(artifact.Metadata["threadStatus"]), "running"), Artifact: artifact,
	}
	if thread.ID == "" || thread.Mode == "" || thread.Query == "" || strings.TrimSpace(artifact.Metadata["tenantAuthorityRef"]) != thread.ID {
		return scoutAgentThread{}, ErrStrideE10TenantAuthorityStale
	}
	envelope, err := app.strideE10ScoutThreadEnvelope(thread)
	if err != nil || envelope.Purpose != StrideE10TenantAuthorityPurposeForScoutThread(thread.ID, thread.Mode, thread.Query) || envelope.SessionSubjectDigest != strings.TrimSpace(artifact.Metadata[openAIToolSessionDigestMetadataKey]) {
		return scoutAgentThread{}, ErrStrideE10TenantAuthorityStale
	}
	thread.TenantAuthority = envelope
	thread.Actions = app.osAssistantActions(thread.Query, thread.Mode, artifact)
	return thread, nil
}

func (app *kanbanBoardApp) openAIToolConversationSource(artifact meetingMemoryEntry) (scoutChatThreadRecord, scoutChatMessageRecord, error) {
	owner := normalizeAccountEmail(artifact.Metadata["requestedBy"])
	threadID := strings.TrimSpace(artifact.Metadata["originId"])
	sourceID := strings.TrimSpace(artifact.Metadata["sourceMessageId"])
	if owner == "" || threadID == "" || sourceID == "" || artifact.Metadata["originKind"] != agentThreadOriginPrivateThread {
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, ErrStrideE10TenantAuthorityStale
	}
	thread, _, err := app.scoutChatThreadByID(owner, threadID)
	if err != nil || thread.ArchivedAt != "" || scoutChatThreadVisibility(thread) != scoutChatVisibilityPrivate {
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, ErrStrideE10TenantAuthorityStale
	}
	var source scoutChatMessageRecord
	for _, message := range thread.Messages {
		if message.ID == sourceID {
			source = message
		}
	}
	_, binding, bindingErr := scoutChatSourceWindow(thread, sourceID)
	if source.ID == "" || source.Role != "user" || source.SourceOperationID != artifact.Metadata["operationId"] || source.SourceOperationDigest != artifact.Metadata["operationBodyDigest"] || bindingErr != nil ||
		binding.MessageDigest != artifact.Metadata["sourceMessageDigest"] || binding.WindowDigest != artifact.Metadata["sourceWindowDigest"] {
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, ErrStrideE10TenantAuthorityStale
	}
	return thread, source, nil
}

func (app *kanbanBoardApp) verifyOpenAIToolConversationCard(artifact meetingMemoryEntry) (scoutChatThreadRecord, scoutChatMessageRecord, scoutChatMessageRecord, error) {
	thread, source, err := app.openAIToolConversationSource(artifact)
	if err != nil {
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, scoutChatMessageRecord{}, err
	}
	var card scoutChatMessageRecord
	cardID := "scout-chat-message-work-" + sha256Hex([]byte(source.ID + "\x00" + artifact.Metadata["threadId"]))[:24]
	for _, message := range thread.Messages {
		if message.ID == cardID {
			card = message
			break
		}
	}
	if card.ID == "" || card.CausedByMessageID != source.ID || card.IntentOutcome != string(conversationIntentStartPrivateWork) || card.Thread == nil ||
		card.Thread.ID != artifact.Metadata["threadId"] || card.Thread.ArtifactID != artifact.ID || card.Thread.Mode != normalizeAgentThreadMode(artifact.Metadata["mode"]) || card.Thread.Query != canonicalizeBoardText(artifact.Metadata["threadQuery"]) {
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, scoutChatMessageRecord{}, ErrStrideE10TenantAuthorityStale
	}
	return thread, source, card, nil
}

func (app *kanbanBoardApp) activateReservedOpenAIToolAgentThread(ctx context.Context, thread scoutAgentThread, spec agentThreadGoalSpec, createdBy string) error {
	if app == nil || app.openAIToolRuntime == nil || !app.openAIToolRuntime.Enabled || app.openAIToolRuntime.Carrier == nil || !app.openAIToolRuntime.Carrier.Enabled || thread.TenantAuthority == nil || strings.TrimSpace(thread.Artifact.ID) == "" {
		return ErrStrideE10TenantAuthorityStale
	}
	return withStrideE10TenantEnvelopeAuthorityContext(ctx, thread.TenantAuthority, StrideE10TenantSurfaceScout, time.Now().UTC(), func(bound context.Context, _ StrideE10TenantPrincipal) error {
		current, ok := app.osArtifactByID(thread.Artifact.ID)
		if !ok || strings.TrimSpace(current.Metadata["threadId"]) != thread.ID {
			return ErrStrideE10TenantAuthorityStale
		}
		thread.Artifact = current
		job := app.newAgentJob(thread)
		if _, _, err := newOpenAIToolProductBackend(bound, app, job, app.openAIToolRuntime.Carrier.Journal); err != nil {
			return err
		}
		if _, _, _, err := app.verifyOpenAIToolConversationCard(current); err != nil {
			return err
		}

		app.openAIToolActivationMu.Lock()
		current, ok = app.osArtifactByID(thread.Artifact.ID)
		if !ok {
			app.openAIToolActivationMu.Unlock()
			return ErrStrideE10TenantAuthorityStale
		}
		state := strings.TrimSpace(current.Metadata[openAIToolActivationStateMetadataKey])
		priorOwner := strings.TrimSpace(current.Metadata[openAIToolActivationOwnerMetadataKey])
		owner := app.currentOpenAIToolActivationOwnerLocked()
		if state == openAIToolActivationComplete || state == openAIToolActivationNeedsAttention {
			app.openAIToolActivationMu.Unlock()
			return nil
		}
		if state == openAIToolActivationStarted && priorOwner == owner {
			if _, active := app.openAIToolActiveRuns[current.ID]; active {
				app.openAIToolActivationMu.Unlock()
				return nil
			}
		} else {
			if state != openAIToolActivationReserved && state != openAIToolActivationStarted && state != openAIToolActivationFinalizing {
				app.openAIToolActivationMu.Unlock()
				return ErrStrideE10TenantAuthorityStale
			}
			nextState := openAIToolActivationStarted
			if state == openAIToolActivationFinalizing {
				nextState = openAIToolActivationFinalizing
			}
			header := artifactAuthorizationHeaderFromEntry(current)
			activated, changed, err := app.memory.updateOSArtifactMetadataIfHeaderAndMetadataMatch(header, map[string]string{
				openAIToolActivationStateMetadataKey: state, openAIToolActivationOwnerMetadataKey: priorOwner,
			}, current.ID, map[string]string{
				openAIToolActivationStateMetadataKey: nextState, openAIToolActivationOwnerMetadataKey: owner,
				"openAIToolActivatedAt": time.Now().UTC().Format(time.RFC3339Nano),
			})
			if err != nil || !changed {
				app.openAIToolActivationMu.Unlock()
				return errors.New("OpenAI tool conversation activation claim changed")
			}
			current = activated
		}
		thread.Artifact = current
		app.openAIToolActiveRuns[current.ID] = struct{}{}
		app.openAIToolActivationMu.Unlock()
		if openAIToolAfterActivationCommitProbe != nil {
			if err := openAIToolAfterActivationCommitProbe(thread); err != nil {
				app.forgetOpenAIToolActiveRun(current.ID)
				return err
			}
		}
		provenanceKey := sha256Hex([]byte("stride-openai-tool-activation-provenance-v1\x00" + current.ID + "\x00" + thread.ID + "\x00" + current.Metadata["operationId"]))
		if err := recordWorkflowRunOnce(workflowRunEntry{
			IdempotencyKey: provenanceKey,
			WorkflowID:     firstNonEmptyString(spec.ToolTemplate, "agent_thread_"+thread.Mode), TriggerSurface: triggerSurfaceChatRouter,
			Proposer: firstNonEmptyString(spec.RequestedBy, createdBy), Lane: current.Metadata["approvalLane"], Outcome: workflowOutcomeLaunched, ThreadID: thread.ID,
		}); err != nil {
			app.forgetOpenAIToolActiveRun(current.ID)
			return err
		}
		if openAIToolAfterWorkflowProvenanceProbe != nil {
			if err := openAIToolAfterWorkflowProvenanceProbe(thread); err != nil {
				app.forgetOpenAIToolActiveRun(current.ID)
				return err
			}
		}
		startAgentThreadAsync(app, thread)
		return nil
	})
}

func (app *kanbanBoardApp) reconcileOpenAIToolConversationWork(ctx context.Context, artifact meetingMemoryEntry) error {
	if app == nil || app.openAIToolRuntime == nil || !app.openAIToolRuntime.Enabled || app.openAIToolRuntime.Carrier == nil || !app.openAIToolRuntime.Carrier.Enabled {
		return nil
	}
	state := strings.TrimSpace(artifact.Metadata[openAIToolActivationStateMetadataKey])
	if state == "" {
		return nil
	}
	thread, err := app.openAIToolThreadFromArtifact(artifact)
	if err != nil {
		return err
	}
	return withStrideE10TenantEnvelopeAuthorityContext(ctx, thread.TenantAuthority, StrideE10TenantSurfaceScout, time.Now().UTC(), func(bound context.Context, _ StrideE10TenantPrincipal) error {
		_, source, _, cardErr := app.verifyOpenAIToolConversationCard(artifact)
		if cardErr != nil {
			chat, currentSource, sourceErr := app.openAIToolConversationSource(artifact)
			if sourceErr != nil {
				return cardErr
			}
			source = currentSource
			card := conversationWorkReplayCard(source, thread)
			for _, message := range chat.Messages {
				if message.ID == card.ID {
					return cardErr
				}
			}
			if _, commitErr := app.commitScoutChatThreadMessagesWithContext(bound, normalizeAccountEmail(artifact.Metadata["requestedBy"]), chat.ID, card); commitErr != nil {
				return commitErr
			}
			if _, _, _, verifyErr := app.verifyOpenAIToolConversationCard(artifact); verifyErr != nil {
				return verifyErr
			}
		}
		switch state {
		case openAIToolActivationComplete, openAIToolActivationNeedsAttention:
			status := "error"
			if state == openAIToolActivationComplete {
				status = "complete"
				job := app.newAgentJob(thread)
				backend, _, backendErr := newOpenAIToolProductBackend(bound, app, job, app.openAIToolRuntime.Carrier.Journal)
				finalReceipt, receiptErr := openAIToolProductFinalReceiptFromArtifact(artifact)
				postimage, postimageErr := openAIToolProductSemanticPostimageDigest(artifact, "")
				if backendErr != nil || receiptErr != nil || postimageErr != nil || finalReceipt.PostimageDigest != postimage || backend.verifyOpenAIToolProductFinalReceipt(bound, finalReceipt) != nil {
					return ErrStrideE10TenantAuthorityStale
				}
			}
			if err := app.commitScoutChatThreadRefStatusWithContext(bound, strings.TrimSpace(artifact.Metadata["originId"]), normalizeAccountEmail(artifact.Metadata["requestedBy"]), thread.ID, status, artifact.ID); err != nil {
				return err
			}
			currentThread, _, err := app.scoutChatThreadByID(normalizeAccountEmail(artifact.Metadata["requestedBy"]), strings.TrimSpace(artifact.Metadata["originId"]))
			if err != nil {
				return err
			}
			for _, message := range currentThread.Messages {
				if message.Thread != nil && message.Thread.ID == thread.ID && message.Thread.ArtifactID == artifact.ID && message.Thread.Status == status {
					return nil
				}
			}
			return errors.New("OpenAI tool terminal conversation projection did not reconcile")
		default:
			return app.activateReservedOpenAIToolAgentThread(bound, thread, openAIToolSpecFromArtifact(artifact), artifact.Metadata["createdBy"])
		}
	})
}

func (app *kanbanBoardApp) installOpenAIToolProductRuntime(ctx context.Context, runtime *openAIToolProductRuntime) error {
	if app == nil {
		return errOpenAIToolCarrierUnavailable
	}
	app.openAIToolRuntime = runtime
	if runtime == nil || !runtime.Enabled || runtime.Carrier == nil || !runtime.Carrier.Enabled {
		return nil
	}
	var firstErr error
	for _, entry := range app.memory.snapshot(0) {
		if entry.Kind != meetingMemoryKindOSArtifact || strings.TrimSpace(entry.Metadata[openAIToolActivationStateMetadataKey]) == "" {
			continue
		}
		if err := app.reconcileOpenAIToolConversationWork(ctx, entry); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("recover OpenAI tool work %s: %w", entry.ID, err)
		}
	}
	return firstErr
}

func (app *kanbanBoardApp) completeOpenAIToolAgentThread(ctx context.Context, thread scoutAgentThread) error {
	current, ok := app.osArtifactByID(thread.Artifact.ID)
	if !ok || strings.TrimSpace(current.Metadata[openAIToolActivationStateMetadataKey]) != openAIToolActivationFinalizing {
		return ErrStrideE10TenantAuthorityStale
	}
	thread.Artifact = current
	if app.openAIToolRuntime == nil || app.openAIToolRuntime.Carrier == nil || app.openAIToolRuntime.Carrier.Journal == nil {
		return errOpenAIToolCarrierUnavailable
	}
	backend, _, err := newOpenAIToolProductBackend(ctx, app, app.newAgentJob(thread), app.openAIToolRuntime.Carrier.Journal)
	if err != nil {
		return err
	}
	finalReceipt, err := openAIToolProductFinalReceiptFromArtifact(current)
	if err != nil {
		return err
	}
	semanticPostimage, semanticErr := openAIToolProductSemanticPostimageDigest(current, "")
	fullPreimage, fullErr := openAIToolArtifactPostimageDigest(current)
	if semanticErr != nil || fullErr != nil || finalReceipt.PostimageDigest != semanticPostimage || backend.verifyOpenAIToolProductFinalReceipt(ctx, finalReceipt) != nil {
		return ErrStrideE10TenantAuthorityStale
	}
	threadRecord, _, err := app.scoutChatThreadByID(normalizeAccountEmail(current.Metadata["requestedBy"]), strings.TrimSpace(current.Metadata["originId"]))
	if err != nil {
		return err
	}
	projection := false
	for _, message := range threadRecord.Messages {
		if message.Thread != nil && message.Thread.ID == thread.ID && message.Thread.ArtifactID == current.ID && message.Thread.Status == "complete" {
			projection = true
			break
		}
	}
	if !projection {
		return errors.New("OpenAI tool completion card is not durable")
	}
	header := artifactAuthorizationHeaderFromEntry(current)
	updated, changed, err := app.memory.updateOSArtifactWithMetadataIfHeaderAndToolPreimagesMatch(header, semanticPostimage, fullPreimage, current.ID, "", current.Text, scoutParticipantName, map[string]string{
		openAIToolActivationStateMetadataKey: openAIToolActivationComplete,
	})
	if err != nil || !changed || strings.TrimSpace(updated.Metadata[openAIToolActivationStateMetadataKey]) != openAIToolActivationComplete {
		return errors.New("OpenAI tool completion state changed before its exact commit")
	}
	return nil
}

func (app *kanbanBoardApp) failOpenAIToolAgentThread(ctx context.Context, thread scoutAgentThread, cause error) {
	defer app.forgetOpenAIToolActiveRun(thread.Artifact.ID)
	current, ok := app.osArtifactByID(thread.Artifact.ID)
	state := strings.TrimSpace(current.Metadata[openAIToolActivationStateMetadataKey])
	if !ok || state == openAIToolActivationComplete || state == openAIToolActivationFinalizing {
		return
	}
	thread.Artifact = current
	if app.openAIToolRuntime == nil || app.openAIToolRuntime.Carrier == nil || app.openAIToolRuntime.Carrier.Journal == nil {
		return
	}
	if _, _, err := newOpenAIToolProductBackend(ctx, app, app.newAgentJob(thread), app.openAIToolRuntime.Carrier.Journal); err != nil {
		return
	}
	header := artifactAuthorizationHeaderFromEntry(current)
	semanticPostimage, semanticErr := openAIToolProductSemanticPostimageDigest(current, "")
	fullPreimage, fullErr := openAIToolArtifactPostimageDigest(current)
	if semanticErr != nil || fullErr != nil {
		return
	}
	updated, changed, err := app.memory.updateOSArtifactWithMetadataIfHeaderAndToolPreimagesMatch(header, semanticPostimage, fullPreimage, current.ID, "", current.Text, scoutParticipantName, map[string]string{
		openAIToolActivationStateMetadataKey: openAIToolActivationNeedsAttention,
		"status":                             "error", "threadStatus": "error", "goalStatus": "needs_attention", "currentStage": "secure_work_unavailable",
		"progressPercent": "0", "reviewGate": "blocked", "error": "Secure OpenAI work stopped before completion.", "completedAt": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil || !changed {
		return
	}
	if openAIToolAfterFailureArtifactCommitProbe != nil {
		if probeErr := openAIToolAfterFailureArtifactCommitProbe(updated); probeErr != nil {
			return
		}
	}
	_ = app.commitScoutChatThreadRefStatusWithContext(ctx, strings.TrimSpace(updated.Metadata["originId"]), normalizeAccountEmail(updated.Metadata["requestedBy"]), thread.ID, "error", updated.ID)
	_ = cause
}
