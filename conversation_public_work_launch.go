package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	publicConversationWorkActivationState   = "publicConversationWorkActivationState"
	publicConversationWorkActivationOwner   = "publicConversationWorkActivationOwner"
	publicConversationWorkActivationTime    = "publicConversationWorkActivatedAt"
	publicConversationWorkReserved          = "reserved"
	publicConversationWorkStarted           = "started"
	publicConversationWorkComplete          = "complete"
	publicConversationWorkNeedsAttention    = "needs_attention"
	publicConversationProviderRequestKey    = "publicConversationProviderRequestBlobRef"
	publicConversationProviderRequestHash   = "publicConversationProviderRequestDigest"
	publicConversationProviderBlobPrefix    = "private-provider-request:"
	publicConversationProviderBlobMax       = 4 << 20
	publicConversationProviderStoreMax      = 64 << 20
	goalChildProviderReplayClassKey         = "goalChildProviderReplayClass"
	goalChildProviderReplayProcessIDKey     = "goalChildProviderReplayProcessId"
	goalChildProviderReplayProcessDigestKey = "goalChildProviderReplayProcessDigest"
	goalChildProviderReplayStudioWriterV1   = "studio_writer_v1"
)

var publicConversationProviderBlobMu sync.Mutex

// publicConversationWorkAfterActivationPersistProbe simulates a process loss
// after the durable dispatch claim but before the provider handoff. Production
// leaves it nil.
var publicConversationWorkAfterActivationPersistProbe func(scoutAgentThread) error

// publicConversationWorkAfterProviderAcceptedProbe simulates losing the local
// acknowledgement after the provider accepted and returned the deterministic
// operation, but before its terminal artifact/chat effect committed.
var publicConversationWorkAfterProviderAcceptedProbe func(scoutAgentThread, agentThreadWorkerResult) error
var publicConversationWorkAfterTerminalCommitProbe func(meetingMemoryEntry) error

func publicConversationProviderOperationKey(thread scoutAgentThread) string {
	metadata := thread.Artifact.Metadata
	if strings.TrimSpace(metadata[publicConversationWorkActivationState]) == "" {
		if (!agentThreadUsesExternalEvidenceV2Contract(thread) && !goalChildUsesStudioWriterProviderReplay(thread)) ||
			strings.TrimSpace(metadata["goalChildActivationState"]) != goalChildActivationStarted ||
			strings.TrimSpace(metadata["goalParentId"]) == "" || strings.TrimSpace(metadata["goalSubtaskId"]) == "" {
			return ""
		}
		if goalChildUsesStudioWriterProviderReplay(thread) && !agentThreadUsesExternalEvidenceV2Contract(thread) {
			return "goal-studio-writer-" + sha256Hex([]byte(strings.Join([]string{
				"goal-studio-writer-provider-operation/v1", metadata[goalChildProviderReplayProcessIDKey], metadata[goalChildProviderReplayProcessDigestKey],
				metadata["operationId"], metadata["operationBodyDigest"],
				metadata["goalParentId"], metadata["goalSubtaskId"], metadata["goalRouteDigest"], metadata["outputContract"],
				thread.ID, thread.Artifact.ID,
			}, "\x00")))
		}
		if strings.TrimSpace(metadata["operationId"]) == "" || strings.TrimSpace(metadata["operationBodyDigest"]) == "" {
			return ""
		}
		return "goal-child-work-" + sha256Hex([]byte(strings.Join([]string{
			"goal-child-provider-operation/v1", metadata["operationId"], metadata["operationBodyDigest"],
			metadata["goalParentId"], metadata["goalSubtaskId"], metadata["goalRouteDigest"], thread.ID, thread.Artifact.ID,
		}, "\x00")))
	}
	if strings.TrimSpace(metadata["operationId"]) == "" || strings.TrimSpace(metadata["operationBodyDigest"]) == "" {
		return ""
	}
	return "public-work-" + sha256Hex([]byte(strings.Join([]string{
		"public-conversation-provider-operation/v1", metadata["operationId"], metadata["operationBodyDigest"], thread.ID, thread.Artifact.ID,
	}, "\x00")))
}

func goalChildUsesStudioWriterProviderReplay(thread scoutAgentThread) bool {
	metadata := thread.Artifact.Metadata
	return strings.TrimSpace(metadata[goalChildProviderReplayClassKey]) == goalChildProviderReplayStudioWriterV1 &&
		strings.TrimSpace(metadata["goalParentId"]) != "" && strings.TrimSpace(metadata["goalSubtaskId"]) != "" &&
		strings.TrimSpace(metadata["outputContract"]) != "" && strings.TrimSpace(metadata["assignedRunner"]) == agentRunnerOpenAIText &&
		oneOf(strings.TrimSpace(metadata[goalChildProviderReplayProcessIDKey]), packagingStudioProcessID, documentReportProcessID) &&
		isHexDigest(strings.TrimSpace(metadata[goalChildProviderReplayProcessDigestKey])) &&
		normalizeCodexJobAuthority(metadata["authority"]) != codexJobAuthorityExternalWrite
}

func goalChildUsesDurableProviderReplay(thread scoutAgentThread) bool {
	return agentThreadUsesExternalEvidenceV2Contract(thread) || goalChildUsesStudioWriterProviderReplay(thread)
}

func providerRequestReservationExpectedMetadata(artifact meetingMemoryEntry) (map[string]string, error) {
	metadata := artifact.Metadata
	if strings.TrimSpace(metadata[publicConversationWorkActivationState]) != "" {
		if strings.TrimSpace(metadata[publicConversationWorkActivationState]) != publicConversationWorkStarted {
			return nil, fmt.Errorf("public conversation provider request activation is not current")
		}
		return map[string]string{
			publicConversationWorkActivationState: publicConversationWorkStarted,
			publicConversationWorkActivationOwner: metadata[publicConversationWorkActivationOwner],
		}, nil
	}
	if strings.TrimSpace(metadata["goalChildActivationState"]) == goalChildActivationStarted {
		return map[string]string{
			"goalChildActivationState": goalChildActivationStarted,
			"goalParentId":             metadata["goalParentId"],
			"goalSubtaskId":            metadata["goalSubtaskId"],
			"threadId":                 metadata["threadId"],
		}, nil
	}
	return nil, fmt.Errorf("provider request activation is unavailable")
}

func artifactRetainsPrivateProviderRequest(artifact meetingMemoryEntry) bool {
	metadata := artifact.Metadata
	return oneOf(strings.TrimSpace(metadata[publicConversationWorkActivationState]), publicConversationWorkReserved, publicConversationWorkStarted) ||
		(strings.TrimSpace(metadata["goalChildActivationState"]) == goalChildActivationStarted &&
			goalChildUsesDurableProviderReplay(scoutAgentThread{ID: metadata["threadId"], Artifact: artifact}))
}

func validPrivateProviderDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func privateProviderRequestRetentionAllowed(retained, candidate int64) bool {
	return retained >= 0 && candidate > 0 && candidate <= publicConversationProviderBlobMax && retained <= publicConversationProviderStoreMax-candidate
}

func securePrivateProviderRequestDir() (string, error) {
	dir := filepath.Join(filepath.Dir(meetingMemoryPath()), "private-operation-blobs")
	if info, err := os.Lstat(dir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("private provider request store is not a directory")
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect private provider request store: %w", err)
	} else if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create private provider request store: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("secure private provider request store: %w", err)
	}
	return dir, nil
}

func storePrivatePublicConversationProviderRequest(raw []byte) (string, string, error) {
	publicConversationProviderBlobMu.Lock()
	defer publicConversationProviderBlobMu.Unlock()
	return storePrivatePublicConversationProviderRequestLocked(raw)
}

func storePrivatePublicConversationProviderRequestLocked(raw []byte) (string, string, error) {
	if len(raw) == 0 {
		return "", "", fmt.Errorf("public conversation provider request is empty")
	}
	if len(raw) > publicConversationProviderBlobMax {
		return "", "", fmt.Errorf("public conversation provider request exceeds the private snapshot limit")
	}
	digest := sha256Hex(raw)
	if !validPrivateProviderDigest(digest) {
		return "", "", fmt.Errorf("private provider request digest is invalid")
	}
	dir, err := securePrivateProviderRequestDir()
	if err != nil {
		return "", "", err
	}
	path := filepath.Join(dir, digest+".json")
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", "", fmt.Errorf("private provider request blob is not a regular file")
		}
		existing, readErr := readPrivateProviderRequestFile(path)
		if readErr != nil {
			return "", "", readErr
		}
		if sha256Hex(existing) != digest {
			return "", "", fmt.Errorf("private provider request blob is corrupt")
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return "", "", fmt.Errorf("secure private provider request blob: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", "", fmt.Errorf("read private provider request blob: %w", err)
	} else if err := writeFileAtomicallyForCanonicalMode(path, raw, 0o600); err != nil {
		return "", "", fmt.Errorf("write private provider request blob: %w", err)
	} else if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Errorf("private provider request blob did not finalize as a regular file")
	} else if err := os.Chmod(path, 0o600); err != nil {
		return "", "", fmt.Errorf("secure private provider request blob: %w", err)
	}
	return publicConversationProviderBlobPrefix + digest, digest, nil
}

func readPrivateProviderRequestFile(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open private provider request blob: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("private provider request blob is not a regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, publicConversationProviderBlobMax+1))
	if err != nil {
		return nil, fmt.Errorf("read private provider request blob: %w", err)
	}
	if len(raw) == 0 || len(raw) > publicConversationProviderBlobMax {
		return nil, fmt.Errorf("public conversation provider request blob size is invalid")
	}
	return raw, nil
}

func loadPrivatePublicConversationProviderRequest(ref, expectedDigest string) ([]byte, error) {
	publicConversationProviderBlobMu.Lock()
	defer publicConversationProviderBlobMu.Unlock()
	ref = strings.TrimSpace(ref)
	digest := strings.TrimPrefix(ref, publicConversationProviderBlobPrefix)
	if digest == ref || !validPrivateProviderDigest(digest) || !validPrivateProviderDigest(strings.TrimSpace(expectedDigest)) || digest != strings.TrimSpace(expectedDigest) {
		return nil, fmt.Errorf("public conversation provider request blob binding changed")
	}
	dir, err := securePrivateProviderRequestDir()
	if err != nil {
		return nil, err
	}
	raw, err := readPrivateProviderRequestFile(filepath.Join(dir, digest+".json"))
	if err != nil {
		return nil, err
	}
	if sha256Hex(raw) != digest {
		return nil, fmt.Errorf("public conversation provider request blob digest changed")
	}
	return raw, nil
}

func (app *kanbanBoardApp) gcPrivatePublicConversationProviderRequests() error {
	publicConversationProviderBlobMu.Lock()
	defer publicConversationProviderBlobMu.Unlock()
	_, err := app.gcPrivatePublicConversationProviderRequestsLocked()
	return err
}

func (app *kanbanBoardApp) gcPrivatePublicConversationProviderRequestsLocked() (int64, error) {
	dir, err := securePrivateProviderRequestDir()
	if err != nil {
		return 0, err
	}
	referenced := map[string]struct{}{}
	if app != nil && app.memory != nil {
		for _, artifact := range app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0) {
			if !artifactRetainsPrivateProviderRequest(artifact) {
				continue
			}
			ref := strings.TrimSpace(artifact.Metadata[publicConversationProviderRequestKey])
			digest := strings.TrimPrefix(ref, publicConversationProviderBlobPrefix)
			if digest != ref && validPrivateProviderDigest(digest) && digest == strings.TrimSpace(artifact.Metadata[publicConversationProviderRequestHash]) {
				referenced[digest] = struct{}{}
			}
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("scan private provider request store: %w", err)
	}
	var retained int64
	for _, entry := range entries {
		name := entry.Name()
		digest := strings.TrimSuffix(name, ".json")
		path := filepath.Join(dir, name)
		info, statErr := os.Lstat(path)
		_, keep := referenced[digest]
		if statErr != nil || !strings.HasSuffix(name, ".json") || !validPrivateProviderDigest(digest) || !keep ||
			info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
				return retained, fmt.Errorf("remove orphan private provider request blob: %w", removeErr)
			}
			continue
		}
		if info.Size() <= 0 || info.Size() > publicConversationProviderBlobMax {
			return retained, fmt.Errorf("referenced private provider request blob size is invalid")
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return retained, fmt.Errorf("secure retained private provider request blob: %w", err)
		}
		retained += info.Size()
	}
	if retained > publicConversationProviderStoreMax {
		return retained, fmt.Errorf("private provider request retention cap is exceeded")
	}
	return retained, nil
}

func cleanupPrivatePublicConversationProviderRequest(ref string) {
	digest := strings.TrimPrefix(strings.TrimSpace(ref), publicConversationProviderBlobPrefix)
	if !validPrivateProviderDigest(digest) {
		return
	}
	publicConversationProviderBlobMu.Lock()
	defer publicConversationProviderBlobMu.Unlock()
	dir, err := securePrivateProviderRequestDir()
	if err != nil {
		return
	}
	_ = os.Remove(filepath.Join(dir, digest+".json"))
}

// startAcceptedPublicScoutWork is the one audience-bound adapter for accepted
// workstream, tool, and goal proposals in a public channel. The current channel
// is both the authorized destination and the visible job ledger. A deterministic
// root card is durable before provider activation; reply lineage remains on the
// source/artifact binding instead of hiding progress inside the reply branch.
func (app *kanbanBoardApp) startAcceptedPublicScoutWork(
	ctx context.Context,
	user *userAccount,
	thread scoutChatThreadRecord,
	proposalMessageID string,
	proposal scoutRouterProposal,
	replyTo *scoutChatReplyRef,
	source scoutChatSourceBinding,
) (map[string]any, error) {
	if app == nil || user == nil || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
		return nil, fmt.Errorf("public work is unavailable")
	}
	work, err := conversationWorkFromScoutProposal(&proposal)
	if err != nil {
		return nil, err
	}
	objective := strings.TrimSpace(proposal.Objective)
	if objective == "" {
		return nil, fmt.Errorf("public work objective is required")
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
	destinationRevision := scoutChatAttachmentDestinationRevision(thread)
	if destinationRevision == "" {
		return nil, fmt.Errorf("public work destination is unavailable")
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
	if directlyTargeted && work.Kind == conversationWorkWorkstream {
		delegatedProfile, delegated = app.strideAgentContextForChatWork(proposal.AgentID, thread, work.Mode)
		if !delegated {
			return nil, fmt.Errorf("the selected agent is no longer eligible for this channel or work; update the assignment before confirming")
		}
	} else if work.Kind == conversationWorkWorkstream && work.Mode == "research" {
		delegatedProfile, delegated = app.stridePreferredResearchAgentContext()
	}

	rootCardID := "scout-chat-message-public-work-" + sha256Hex([]byte(operation.ID + "\x00" + operation.BodyDigest))[:24]
	label := conversationWorkVisibleLabel(work, assistantToolLabel(firstNonEmptyString(work.Mode, "workflow")))
	rootCard := scoutChatMessageRecord{
		ID: rootCardID, Kind: "thread", Role: "scout", AuthorName: scoutParticipantName,
		IntentOutcome: string(conversationIntentStartPrivateWork), CausedByMessageID: proposalMessageID,
		Text:      firstNonEmptyString(strings.TrimSpace(label), "Work") + " queued — generating in this channel",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	// A goal launcher starts internally, so its audience-visible reservation has
	// to exist first. Workstreams can persist their artifact without activation,
	// then put the exact ref on this same card before starting the provider.
	if work.Kind != conversationWorkWorkstream && !found {
		if _, _, err := app.upsertAcceptedPublicWorkCard(user.Email, thread.ID, rootCard); err != nil {
			return nil, err
		}
	}

	if !found {
		origin := map[string]string{
			"originKind": agentThreadOriginChannel, "originId": thread.ID,
			"originSurface": "chat:" + thread.ID, "requestedBy": normalizeAccountEmail(user.Email),
			"sourceMessageId": source.MessageID, "sourceMessageDigest": source.MessageDigest, "sourceWindowDigest": source.WindowDigest,
			"operationId": operation.ID, "operationBodyDigest": operation.BodyDigest,
			"approvedProposalId":  proposalMessageID,
			"approvedEffectClass": proposal.EffectClass,
		}
		switch work.Kind {
		case conversationWorkWorkstream:
			mode := strings.ToLower(strings.TrimSpace(work.Mode))
			switch mode {
			case "research", "design", "grill", "workflow":
			default:
				return nil, fmt.Errorf("workstream mode must be research, design, grill, or workflow")
			}
			spec := agentThreadGoalSpec{
				Objective: objective, ContextRefs: proposal.ContextRefs,
				SourceMessageID: source.MessageID, SourceMessageDigest: source.MessageDigest, SourceWindowDigest: source.WindowDigest,
				OperationID: operation.ID, OperationBodyDigest: operation.BodyDigest,
				OriginSurface: "chat:" + thread.ID, RequestedBy: normalizeAccountEmail(user.Email),
				Visibility: scoutChatVisibilityPublic,
				Launch:     launchFunnelLineage{Source: proposalSourceChatRouter, ProposalID: proposalMessageID, Path: "chat_workstream", Proposer: normalizeAccountEmail(user.Email)},
			}
			if affinity, affinityFound := app.resolveWorkstreamAffinityWithContext(ctx, user, thread, sourceMessage, objective, time.Now().UTC()); affinityFound {
				encodedAffinity, affinityErr := encodeWorkstreamAffinity(affinity)
				if affinityErr != nil {
					return nil, affinityErr
				}
				spec.WorkstreamAffinity, spec.ProjectWorkID, spec.ProjectWorkTitle = encodedAffinity, affinity.ProjectThreadID, affinity.ProjectTitle
			}
			if delegated {
				coordinator := ""
				if !directlyTargeted {
					coordinator = scoutParticipantName
				}
				spec = conversationWorkerSpec(agentThreadGoalSpecForProfile(delegatedProfile, coordinator), spec)
			}
			runID := "agent-thread-public-" + sha256Hex([]byte(operation.ID + "\x00" + operation.BodyDigest))[:24]
			launched, err = app.launchAgentThreadWithSpecBound(mode, objective, user.Name, origin, spec, runID, nil, false)
			if err == nil {
				_, _, err = app.updateOSArtifactWithMetadata(launched.Artifact.ID, "", launched.Artifact.Text, "", map[string]string{
					publicConversationWorkActivationState: publicConversationWorkReserved, "destinationRevision": destinationRevision,
				})
				if current, ok := app.osArtifactByID(launched.Artifact.ID); ok {
					launched.Artifact = current
				}
			}
		case conversationWorkRegistryTool:
			if process, ok := processByID(work.ToolID); ok && !process.Hidden {
				launched, err = app.launchGoalThread(goalLaunchSpec{Objective: objective, CreatedBy: user.Name, Authority: process.Authority, PackageID: work.PackageID, ToolTemplate: process.ID, ContextRefs: proposal.ContextRefs, Origin: origin})
			} else if tool, ok := toolByID(work.ToolID); ok && tool.ID != ventureWorkbookToolID {
				launched, err = app.launchGoalThread(goalLaunchSpec{Objective: objective, CreatedBy: user.Name, Authority: tool.Authority, PackageID: work.PackageID, ToolTemplate: tool.ID, ContextRefs: proposal.ContextRefs, Origin: origin})
			} else {
				err = fmt.Errorf("the requested public output contract is unavailable")
			}
		case conversationWorkGoal:
			launched, err = app.launchGoalThread(goalLaunchSpec{Objective: objective, CreatedBy: user.Name, Authority: work.Authority, PackageID: work.PackageID, ContextRefs: proposal.ContextRefs, Origin: origin})
		default:
			err = fmt.Errorf("public work route %q is unavailable", work.Kind)
		}
		if err != nil {
			rootCard.Text = firstNonEmptyString(strings.TrimSpace(label), "Work") + " failed to start"
			_, _, _ = app.upsertAcceptedPublicWorkCard(user.Email, thread.ID, rootCard)
			return nil, err
		}
	}

	rootCard.Thread = &scoutChatThreadRef{ID: launched.ID, Mode: launched.Mode, ProcessID: launched.Artifact.Metadata["processId"], Query: launched.Query, Status: launched.Status, ArtifactID: launched.Artifact.ID,
		OutputFamily: firstNonEmptyString(scoutChatOutputFamilyForArtifact(launched.Artifact), scoutChatOutputFamilyForMode(launched.Mode)),
		ProjectID:    launched.Artifact.Metadata["projectWorkId"], ProjectTitle: launched.Artifact.Metadata["projectWorkTitle"]}
	rootCard.Text = firstNonEmptyString(strings.TrimSpace(label), "Work") + " in progress"
	if studioProjectKindForProcessID(launched.Artifact.Metadata["processId"]) != "" {
		rootCard.Text = studioProjectLaunchCopy(launched.Artifact.Metadata["processId"], firstNonEmptyString(strings.TrimSpace(label), "Work"))
	}
	if delegated {
		rootCard.AuthorName = delegatedProfile.DisplayName
		rootCard.Thread = scoutChatThreadRefForAgent(launched, delegatedProfile, "")
		if directlyTargeted {
			rootCard.Text = delegatedProfile.DisplayName + " is working on it"
		} else {
			rootCard.Text = "Research in progress"
			rootCard.Thread = scoutChatThreadRefForAgent(launched, delegatedProfile, scoutParticipantName)
		}
	}
	saved, assistantMessage, err := app.upsertAcceptedPublicWorkCard(user.Email, thread.ID, rootCard)
	if err != nil {
		return nil, fmt.Errorf("work reserved but its channel projection needs reconciliation: %w", err)
	}
	if work.Kind == conversationWorkWorkstream && oneOf(launched.Artifact.Metadata[publicConversationWorkActivationState], publicConversationWorkReserved, publicConversationWorkStarted) {
		activationSpec := agentThreadGoalSpec{
			RequestedBy: normalizeAccountEmail(user.Email),
			Launch:      launchFunnelLineage{Source: proposalSourceChatRouter, ProposalID: proposalMessageID, Path: "chat_workstream", Proposer: normalizeAccountEmail(user.Email)},
		}
		launched, err = app.activateAcceptedPublicConversationWork(launched, activationSpec, user.Name)
		if err != nil {
			return nil, fmt.Errorf("work card is visible but activation needs reconciliation: %w", err)
		}
	}
	current, err := app.reconcileScoutChatThreadRefAfterCommit(user.Email, thread.ID, launched.ID, launched.Artifact.ID)
	if err != nil {
		return nil, fmt.Errorf("work launched but its chat projection needs reconciliation: %w", err)
	}
	if current.ID != "" {
		saved = current
		for _, message := range current.Messages {
			if message.ID == rootCardID {
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

// activateAcceptedPublicConversationWork is the durable dispatch outbox for a
// channel workstream. The persisted owner is boot-specific: retries in this
// process observe the in-memory active claim, while a restarted process can
// reclaim an ambiguous started row and dispatch the same deterministic run ID
// once. Provider activation remains strictly after the exact root card check.
func (app *kanbanBoardApp) activateAcceptedPublicConversationWork(thread scoutAgentThread, spec agentThreadGoalSpec, createdBy string) (scoutAgentThread, error) {
	if app == nil || app.memory == nil || strings.TrimSpace(thread.Artifact.ID) == "" {
		return thread, fmt.Errorf("public conversation work reservation is unavailable")
	}
	app.openAIToolActivationMu.Lock()
	current, ok := app.osArtifactByID(thread.Artifact.ID)
	if !ok || current.Metadata["originKind"] != agentThreadOriginChannel || current.Metadata["threadId"] != thread.ID {
		app.openAIToolActivationMu.Unlock()
		return thread, fmt.Errorf("public conversation work reservation changed")
	}
	status := strings.ToLower(strings.TrimSpace(firstNonEmptyString(current.Metadata["threadStatus"], current.Metadata["status"])))
	if oneOf(status, "complete", "published", "error", "failed", "cancelled", "canceled") {
		thread.Artifact = current
		app.openAIToolActivationMu.Unlock()
		return thread, nil
	}
	state := strings.TrimSpace(current.Metadata[publicConversationWorkActivationState])
	priorOwner := strings.TrimSpace(current.Metadata[publicConversationWorkActivationOwner])
	owner := app.currentOpenAIToolActivationOwnerLocked()
	if state != publicConversationWorkReserved && state != publicConversationWorkStarted {
		app.openAIToolActivationMu.Unlock()
		return thread, fmt.Errorf("public conversation work reservation state is invalid")
	}
	if state == publicConversationWorkStarted && priorOwner == owner {
		if _, active := app.openAIToolActiveRuns[current.ID]; active {
			thread.Artifact = current
			app.openAIToolActivationMu.Unlock()
			return thread, nil
		}
	} else {
		header := artifactAuthorizationHeaderFromEntry(current)
		activated, changed, err := app.memory.updateOSArtifactMetadataIfHeaderAndMetadataMatch(header, map[string]string{
			publicConversationWorkActivationState: state, publicConversationWorkActivationOwner: priorOwner,
		}, current.ID, map[string]string{
			publicConversationWorkActivationState: publicConversationWorkStarted,
			publicConversationWorkActivationOwner: owner,
			publicConversationWorkActivationTime:  time.Now().UTC().Format(time.RFC3339Nano),
		})
		if err != nil || !changed {
			app.openAIToolActivationMu.Unlock()
			return thread, fmt.Errorf("public conversation work activation claim changed")
		}
		current = activated
	}
	thread.Artifact = current
	app.openAIToolActiveRuns[current.ID] = struct{}{}
	app.openAIToolActivationMu.Unlock()

	if err := app.verifyAcceptedPublicConversationWorkDispatch(thread); err != nil {
		app.failAcceptedPublicConversationWorkPermanently(thread, err)
		return thread, err
	}
	if strings.TrimSpace(current.Metadata["worker"]) == agentThreadWorkerOpenAI {
		prepared, err := app.preparePublicConversationProviderRequest(thread)
		if err != nil {
			app.failAcceptedPublicConversationWorkPermanently(thread, err)
			return thread, err
		}
		thread = prepared
		current = prepared.Artifact
	}
	provenanceKey := sha256Hex([]byte("public-conversation-work-activation/v1\x00" + current.ID + "\x00" + thread.ID + "\x00" + current.Metadata["operationId"]))
	if err := recordWorkflowRunOnce(workflowRunEntry{
		IdempotencyKey: provenanceKey,
		WorkflowID:     firstNonEmptyString(spec.ToolTemplate, "agent_thread_"+thread.Mode), TriggerSurface: triggerSurfaceChatRouter,
		Proposer: firstNonEmptyString(spec.RequestedBy, current.Metadata["requestedBy"], createdBy), Lane: current.Metadata["approvalLane"],
		Outcome: workflowOutcomeLaunched, ThreadID: thread.ID,
	}); err != nil {
		app.forgetOpenAIToolActiveRun(current.ID)
		return thread, fmt.Errorf("public conversation work launch provenance is unavailable: %w", err)
	}
	if publicConversationWorkAfterActivationPersistProbe != nil {
		if err := publicConversationWorkAfterActivationPersistProbe(thread); err != nil {
			app.forgetOpenAIToolActiveRun(current.ID)
			return thread, err
		}
	}
	broadcastSignedInKanbanEvent("memory", nil)
	broadcastAssistantEvent("action", assistantToolLabel(thread.Mode)+" thread launched", agentThreadBroadcastMetadata("launch_agent_thread", thread.ID, thread.Status, "listening"))
	startAgentThreadAsync(app, thread)
	return thread, nil
}

func (app *kanbanBoardApp) failAcceptedPublicConversationWorkPermanently(thread scoutAgentThread, cause error) {
	if app == nil || app.memory == nil || strings.TrimSpace(thread.Artifact.ID) == "" {
		return
	}
	defer app.forgetOpenAIToolActiveRun(thread.Artifact.ID)
	const publicMessage = "This work stopped because its approved source or durable provider request is no longer valid. Review the source and launch it again."
	metadata := map[string]string{
		"status": "error", "threadStatus": "error", "goalStatus": "needs_attention", "currentStage": "gate_before_shipping",
		"progressPercent": "72", "reviewGate": "blocked", "error": "approved source or provider request is no longer valid",
		"progressNote": publicMessage, "completedAt": time.Now().UTC().Format(time.RFC3339Nano),
		publicConversationWorkActivationState: publicConversationWorkNeedsAttention,
		publicConversationProviderRequestKey:  "", publicConversationProviderRequestHash: "",
	}
	artifact, changed, err := app.memory.updateOSArtifactWithMetadataIfHeaderAndMetadataMatch(
		artifactAuthorizationHeaderFromEntry(thread.Artifact),
		map[string]string{
			publicConversationWorkActivationState: publicConversationWorkStarted,
			publicConversationWorkActivationOwner: thread.Artifact.Metadata[publicConversationWorkActivationOwner],
		},
		thread.Artifact.ID, "", publicMessage, scoutParticipantName, metadata,
	)
	if err != nil || !changed {
		log.Errorf("Public conversation work permanent failure could not claim %s: %v", thread.ID, err)
		return
	}
	cleanupPrivatePublicConversationProviderRequest(thread.Artifact.Metadata[publicConversationProviderRequestKey])
	app.updateScoutChatThreadRefs(thread.ID, "error", artifact.ID)
	app.syncLinkedCardForArtifact(artifact, "error")
	if shouldNotifyAgentThreadCreator(artifact) {
		app.notifyAgentThreadCreator(artifact, notificationKindAgent, agentThreadNotificationText(assistantToolLabel(thread.Mode)+" thread needs attention", artifact))
	}
	broadcastSignedInKanbanEvent("memory", nil)
	broadcastAssistantEvent("error", assistantToolLabel(thread.Mode)+" thread needs attention", agentThreadBroadcastMetadata("launch_agent_thread", thread.ID, "error", ""))
}

func (app *kanbanBoardApp) verifyAcceptedPublicConversationWorkDispatch(work scoutAgentThread) error {
	metadata := work.Artifact.Metadata
	requester := normalizeAccountEmail(metadata["requestedBy"])
	channelID := strings.TrimSpace(metadata["originId"])
	proposalID := strings.TrimSpace(metadata["approvedProposalId"])
	sourceID := strings.TrimSpace(metadata["sourceMessageId"])
	thread, _, err := app.scoutChatThreadByID(requester, channelID)
	if err != nil || thread.ArchivedAt != "" || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic ||
		scoutChatAttachmentDestinationRevision(thread) != metadata["destinationRevision"] {
		return fmt.Errorf("public conversation work destination changed")
	}
	_, source, err := scoutChatSourceWindow(thread, sourceID)
	if err != nil || source.MessageDigest != metadata["sourceMessageDigest"] || source.WindowDigest != metadata["sourceWindowDigest"] {
		return fmt.Errorf("public conversation work source changed")
	}
	var proposalMessage, rootCard scoutChatMessageRecord
	for _, message := range thread.Messages {
		if message.ID == proposalID {
			proposalMessage = message
		}
		if message.Thread != nil && message.Thread.ID == work.ID {
			rootCard = message
		}
	}
	if proposalMessage.Proposal == nil || proposalMessage.Proposal.Status != "accepted" || proposalMessage.CausedByMessageID != sourceID ||
		proposalMessage.Proposal.EffectClass != "expanded_audience" {
		return fmt.Errorf("public conversation work approval changed")
	}
	operation, err := conversationApprovedWorkOperation(channelID, requester, proposalID, *proposalMessage.Proposal)
	if err != nil || operation.ID != metadata["operationId"] || operation.BodyDigest != metadata["operationBodyDigest"] {
		return fmt.Errorf("public conversation work operation changed")
	}
	if rootCard.ID == "" || rootCard.Kind != "thread" || rootCard.ReplyTo != nil || rootCard.CausedByMessageID != proposalID ||
		rootCard.Thread == nil || rootCard.Thread.ArtifactID != work.Artifact.ID {
		return fmt.Errorf("public conversation work card is not durable")
	}
	return nil
}

// reconcilePublicConversationWorkAtBoot reclaims public workstream dispatches
// whose process died after the durable started claim but before (or during)
// provider handoff. Terminal rows are left untouched; malformed/stale rows
// fail closed and remain visible for a later governed reconciliation.
func (app *kanbanBoardApp) reconcilePublicConversationWorkAtBoot() {
	if err := app.gcPrivatePublicConversationProviderRequests(); err != nil {
		log.Errorf("Private public-work provider snapshot GC deferred: %v", err)
	}
	if app == nil || app.memory == nil {
		return
	}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0) {
		state := strings.TrimSpace(entry.Metadata[publicConversationWorkActivationState])
		if entry.Metadata["originKind"] != agentThreadOriginChannel {
			continue
		}
		if oneOf(state, publicConversationWorkComplete, publicConversationWorkNeedsAttention) {
			status := strings.ToLower(strings.TrimSpace(firstNonEmptyString(entry.Metadata["threadStatus"], entry.Metadata["status"])))
			if oneOf(status, "complete", "error", "failed") {
				app.updateScoutChatThreadRefs(entry.Metadata["threadId"], status, entry.ID)
			}
			continue
		}
		if !oneOf(state, publicConversationWorkReserved, publicConversationWorkStarted) {
			continue
		}
		thread := scoutAgentThread{
			ID: entry.Metadata["threadId"], Mode: entry.Metadata["mode"], Query: firstNonEmptyString(entry.Metadata["threadQuery"], entry.Metadata["query"]),
			Status: firstNonEmptyString(entry.Metadata["threadStatus"], entry.Metadata["status"], "running"), Artifact: entry,
		}
		spec := agentThreadGoalSpec{RequestedBy: entry.Metadata["requestedBy"], Launch: launchFunnelLineage{
			Source: proposalSourceChatRouter, ProposalID: entry.Metadata["approvedProposalId"], Path: "chat_workstream", Proposer: entry.Metadata["requestedBy"],
		}}
		if _, err := app.activateAcceptedPublicConversationWork(thread, spec, firstNonEmptyString(entry.Metadata["createdBy"], scoutParticipantName)); err != nil {
			if current, ok := app.osArtifactByID(entry.ID); ok && current.Metadata[publicConversationWorkActivationState] == publicConversationWorkNeedsAttention {
				log.Errorf("Public conversation work recovery ended needs-attention for %s: %v", thread.ID, err)
			} else {
				log.Errorf("Public conversation work recovery deferred for %s: %v", thread.ID, err)
			}
		}
	}
}

// upsertAcceptedPublicWorkCard appends or replaces the one deterministic root
// card for an accepted proposal. It never carries ReplyTo: the reply source is
// already bound in the operation/artifact, while progress belongs in the main
// channel where the whole destination audience can see it.
func (app *kanbanBoardApp) upsertAcceptedPublicWorkCard(viewerEmail, threadID string, message scoutChatMessageRecord) (scoutChatThreadRecord, scoutChatMessageRecord, error) {
	message.ReplyTo = nil
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()
	thread, _, err := app.scoutChatThreadByID(viewerEmail, threadID)
	if err != nil {
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, err
	}
	if thread.ArchivedAt != "" || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, fmt.Errorf("public work destination changed")
	}
	index := scoutChatMessageIndex(thread, message.ID)
	if index < 0 {
		thread.Messages = append(thread.Messages, message)
	} else {
		createdAt := thread.Messages[index].CreatedAt
		thread.Messages[index] = message
		if createdAt != "" {
			thread.Messages[index].CreatedAt = createdAt
		}
		message = thread.Messages[index]
	}
	updateScoutChatThreadSummary(&thread, scoutChatMessageRecord{}, message)
	if err := app.saveScoutChatThread(thread); err != nil {
		return scoutChatThreadRecord{}, scoutChatMessageRecord{}, err
	}
	deliverScoutChatThreadUpdate(thread, message)
	return thread, message, nil
}

// Compatibility for focused callers while the public accept route migrates to
// the generalized output-contract adapter.
func (app *kanbanBoardApp) startAcceptedPublicScoutWorkstream(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, proposalMessageID string, proposal scoutRouterProposal, replyTo *scoutChatReplyRef, source scoutChatSourceBinding) (map[string]any, error) {
	return app.startAcceptedPublicScoutWork(ctx, user, thread, proposalMessageID, proposal, replyTo, source)
}
