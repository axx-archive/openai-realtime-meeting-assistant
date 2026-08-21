package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

const goalRouteContractConversationV1 = "stride.goal-route.conversation.v1"

const (
	goalChildActivationReserved = "reserved"
	goalChildActivationStarted  = "started"
)

// goalRouteReceipt is the durable server-owned provenance for a goal route.
// Its digest binds both the selected contract and the exact conversation turn
// that selected it. routeVerified remains transient on goalPlan, so decoding a
// receipt never makes it trusted without re-reading the source conversation.
type goalRouteReceipt struct {
	Contract              string `json:"contract"`
	Requester             string `json:"requester"`
	OriginKind            string `json:"originKind"`
	OriginID              string `json:"originId"`
	SourceMessageID       string `json:"sourceMessageId"`
	SourceMessageDigest   string `json:"sourceMessageDigest"`
	SourceWindowDigest    string `json:"sourceWindowDigest"`
	OperationID           string `json:"operationId"`
	OperationBodyDigest   string `json:"operationBodyDigest"`
	ApprovedProposalID    string `json:"approvedProposalId,omitempty"`
	ApprovedEffectClass   string `json:"approvedEffectClass,omitempty"`
	ObjectiveDigest       string `json:"objectiveDigest"`
	ToolTemplate          string `json:"toolTemplate,omitempty"`
	ProcessID             string `json:"processId,omitempty"`
	Authority             string `json:"authority"`
	PackageID             string `json:"packageId,omitempty"`
	ContextRefsDigest     string `json:"contextRefsDigest,omitempty"`
	SourceSelectionDigest string `json:"sourceSelectionDigest,omitempty"`
	Digest                string `json:"digest"`
}

func (receipt goalRouteReceipt) contractDigest() (string, error) {
	receipt.Digest = ""
	return STRIDEContractDigest(receipt)
}

func goalRouteChildBindingMetadata(plan *goalPlan) map[string]string {
	if plan == nil || !plan.routeVerified || plan.RouteReceipt == nil {
		return nil
	}
	receipt := plan.RouteReceipt
	return map[string]string{
		"goalRouteDigest":     receipt.Digest,
		"requestedBy":         receipt.Requester,
		"originKind":          receipt.OriginKind,
		"originId":            receipt.OriginID,
		"sourceMessageId":     receipt.SourceMessageID,
		"sourceMessageDigest": receipt.SourceMessageDigest,
		"sourceWindowDigest":  receipt.SourceWindowDigest,
		"operationId":         receipt.OperationID,
		"operationBodyDigest": receipt.OperationBodyDigest,
		"approvedProposalId":  receipt.ApprovedProposalID,
		"approvedEffectClass": receipt.ApprovedEffectClass,
	}
}

func (app *kanbanBoardApp) mintGoalRouteReceipt(plan *goalPlan, origin map[string]string) (goalRouteReceipt, error) {
	if app == nil || plan == nil {
		return goalRouteReceipt{}, fmt.Errorf("server-owned goal route is unavailable")
	}
	receipt := goalRouteReceipt{
		Contract:            goalRouteContractConversationV1,
		Requester:           normalizeAccountEmail(firstNonEmptyString(origin["requestedBy"], plan.RequestedBy)),
		OriginKind:          strings.TrimSpace(origin["originKind"]),
		OriginID:            strings.TrimSpace(origin["originId"]),
		SourceMessageID:     strings.TrimSpace(origin["sourceMessageId"]),
		SourceMessageDigest: strings.TrimSpace(origin["sourceMessageDigest"]),
		SourceWindowDigest:  strings.TrimSpace(origin["sourceWindowDigest"]),
		OperationID:         strings.TrimSpace(origin["operationId"]),
		OperationBodyDigest: strings.TrimSpace(origin["operationBodyDigest"]),
		ApprovedProposalID:  strings.TrimSpace(origin["approvedProposalId"]),
		ApprovedEffectClass: strings.TrimSpace(origin["approvedEffectClass"]),
		ObjectiveDigest:     sha256Hex([]byte(canonicalizeBoardText(plan.Objective))),
		ToolTemplate:        strings.TrimSpace(plan.ToolTemplate),
		ProcessID:           strings.TrimSpace(plan.ProcessID),
		Authority:           normalizeCodexJobAuthority(plan.Authority),
		PackageID:           strings.TrimSpace(plan.PackageID),
		ContextRefsDigest:   goalContextRefsDigest(plan.ContextRefs),
	}
	selection, selectionErr := app.goalRouteSourceSelection(receipt)
	if selectionErr != nil {
		return goalRouteReceipt{}, selectionErr
	}
	receipt.SourceSelectionDigest = selection.Digest
	digest, err := receipt.contractDigest()
	if err != nil {
		return goalRouteReceipt{}, fmt.Errorf("goal route receipt could not be encoded")
	}
	receipt.Digest = digest
	if err := app.verifyGoalRouteReceipt(plan, receipt); err != nil {
		return goalRouteReceipt{}, err
	}
	return receipt, nil
}

func (e *goalEngine) prepareGoalRoute(plan *goalPlan, parentID string) error {
	if plan == nil {
		return fmt.Errorf("goal plan is unavailable")
	}
	plan.routeVerified = false
	// Plain historical goals are allowed to remain visible but use only the
	// unavailable stub. They carry no persisted tool/process authority.
	if strings.TrimSpace(plan.ToolTemplate) == "" && strings.TrimSpace(plan.ProcessID) == "" && plan.RouteReceipt == nil {
		return nil
	}
	if e == nil || e.app == nil || plan.RouteReceipt == nil {
		return fmt.Errorf("legacy tool or process assignment has no verified conversation route")
	}
	if plan.RouteReceipt.SourceSelectionDigest == "" {
		legacy := *plan.RouteReceipt
		oldDigest, oldDigestErr := legacy.contractDigest()
		if oldDigestErr != nil || legacy.Digest != oldDigest ||
			legacy.ObjectiveDigest != sha256Hex([]byte(canonicalizeBoardText(plan.Objective))) ||
			legacy.ToolTemplate != strings.TrimSpace(plan.ToolTemplate) || legacy.ProcessID != strings.TrimSpace(plan.ProcessID) ||
			legacy.Authority != normalizeCodexJobAuthority(plan.Authority) || legacy.PackageID != strings.TrimSpace(plan.PackageID) ||
			legacy.ContextRefsDigest != goalContextRefsDigest(plan.ContextRefs) || legacy.Requester != normalizeAccountEmail(goalPlanRequestedBy(*plan)) {
			return fmt.Errorf("legacy goal route authentication failed")
		}
		if parentID = strings.TrimSpace(parentID); parentID != "" {
			parent, ok := e.app.osArtifactByID(parentID)
			if !ok {
				return fmt.Errorf("legacy goal route parent is unavailable")
			}
			if strings.TrimSpace(parent.Metadata["goalRouteDigest"]) != legacy.Digest || strings.TrimSpace(parent.Metadata["sourceSelectionDigest"]) != "" {
				return fmt.Errorf("legacy goal route parent binding changed")
			}
			for key, want := range map[string]string{
				"requestedBy": legacy.Requester, "originKind": legacy.OriginKind, "originId": legacy.OriginID,
				"sourceMessageId": legacy.SourceMessageID, "sourceMessageDigest": legacy.SourceMessageDigest,
				"sourceWindowDigest": legacy.SourceWindowDigest, "operationId": legacy.OperationID,
				"operationBodyDigest": legacy.OperationBodyDigest, "approvedProposalId": legacy.ApprovedProposalID,
				"approvedEffectClass": legacy.ApprovedEffectClass,
			} {
				if strings.TrimSpace(parent.Metadata[key]) != want {
					return fmt.Errorf("legacy goal route parent binding changed")
				}
			}
		}
		selection, err := e.app.goalRouteSourceSelection(*plan.RouteReceipt)
		if err != nil || !e.app.legacyGoalRouteSelectionMatchesApprovedProposal(*plan.RouteReceipt, selection) {
			return fmt.Errorf("legacy goal source selection cannot be authenticated; launch a new run")
		}
		upgraded := *plan.RouteReceipt
		upgraded.SourceSelectionDigest = selection.Digest
		upgraded.Digest, err = upgraded.contractDigest()
		if err != nil {
			return fmt.Errorf("legacy goal source selection could not be bound")
		}
		// Authenticate the complete current conversation operation and the new
		// exact selection binding before persisting the compatibility upgrade.
		// A changed proposal/body therefore cannot leave behind a newly blessed
		// receipt even when the final route check correctly rejects it.
		if err := e.app.verifyGoalRouteReceipt(plan, upgraded); err != nil {
			return fmt.Errorf("legacy goal source selection cannot be authenticated; launch a new run")
		}
		plan.RouteReceipt = &upgraded
		if parentID = strings.TrimSpace(parentID); parentID != "" {
			raw, marshalErr := json.Marshal(plan)
			if marshalErr != nil {
				return fmt.Errorf("legacy goal source selection could not be saved")
			}
			parent, ok := e.app.osArtifactByID(parentID)
			if !ok {
				return fmt.Errorf("legacy goal source selection could not be saved")
			}
			if _, _, persistErr := e.app.updateOSArtifactWithMetadata(parentID, "", parent.Text, scoutParticipantName, map[string]string{
				"goalPlan": string(raw), "goalRouteDigest": upgraded.Digest,
				"sourceSelectionDigest": upgraded.SourceSelectionDigest,
			}); persistErr != nil {
				return fmt.Errorf("legacy goal source selection could not be saved")
			}
		}
	}
	if err := e.app.verifyGoalRouteReceipt(plan, *plan.RouteReceipt); err != nil {
		return err
	}
	if parentID = strings.TrimSpace(parentID); parentID != "" {
		parent, ok := e.app.osArtifactByID(parentID)
		if !ok {
			return fmt.Errorf("goal route parent is unavailable")
		}
		metadata := parent.Metadata
		receipt := plan.RouteReceipt
		if normalizeAccountEmail(metadata["requestedBy"]) != receipt.Requester ||
			strings.TrimSpace(metadata["originKind"]) != receipt.OriginKind || strings.TrimSpace(metadata["originId"]) != receipt.OriginID ||
			strings.TrimSpace(metadata["sourceMessageId"]) != receipt.SourceMessageID || strings.TrimSpace(metadata["sourceMessageDigest"]) != receipt.SourceMessageDigest ||
			strings.TrimSpace(metadata["sourceWindowDigest"]) != receipt.SourceWindowDigest || strings.TrimSpace(metadata["operationId"]) != receipt.OperationID ||
			strings.TrimSpace(metadata["operationBodyDigest"]) != receipt.OperationBodyDigest || strings.TrimSpace(metadata["approvedProposalId"]) != receipt.ApprovedProposalID ||
			strings.TrimSpace(metadata["approvedEffectClass"]) != receipt.ApprovedEffectClass {
			return fmt.Errorf("goal route parent metadata changed")
		}
	}
	plan.routeVerified = true
	return nil
}

func (app *kanbanBoardApp) verifyGoalRouteReceipt(plan *goalPlan, receipt goalRouteReceipt) error {
	if app == nil || plan == nil || receipt.Contract != goalRouteContractConversationV1 {
		return fmt.Errorf("goal route receipt is missing or unsupported")
	}
	if receipt.Requester == "" || !oneOf(receipt.OriginKind, agentThreadOriginPrivateThread, agentThreadOriginChannel) || receipt.OriginID == "" || receipt.SourceMessageID == "" ||
		!isHexDigest(receipt.SourceMessageDigest) || !isHexDigest(receipt.SourceWindowDigest) || receipt.OperationID == "" || !isHexDigest(receipt.OperationBodyDigest) {
		return fmt.Errorf("goal route receipt is incomplete")
	}
	if receipt.ObjectiveDigest != sha256Hex([]byte(canonicalizeBoardText(plan.Objective))) ||
		receipt.ToolTemplate != strings.TrimSpace(plan.ToolTemplate) || receipt.ProcessID != strings.TrimSpace(plan.ProcessID) ||
		receipt.Authority != normalizeCodexJobAuthority(plan.Authority) || receipt.PackageID != strings.TrimSpace(plan.PackageID) ||
		receipt.ContextRefsDigest != goalContextRefsDigest(plan.ContextRefs) ||
		receipt.Requester != normalizeAccountEmail(goalPlanRequestedBy(*plan)) {
		return fmt.Errorf("goal route receipt no longer matches the saved plan")
	}
	wantDigest, err := receipt.contractDigest()
	if err != nil || !isHexDigest(receipt.Digest) || receipt.Digest != wantDigest {
		return fmt.Errorf("goal route receipt authentication failed")
	}
	if !isHexDigest(receipt.SourceSelectionDigest) {
		return fmt.Errorf("goal source selection binding is missing")
	}

	lock := app.scoutChatThreadLock(receipt.OriginID)
	lock.Lock()
	thread, _, threadErr := app.scoutChatThreadByID(receipt.Requester, receipt.OriginID)
	wantVisibility := scoutChatVisibilityPrivate
	if receipt.OriginKind == agentThreadOriginChannel {
		wantVisibility = scoutChatVisibilityPublic
	}
	if threadErr != nil || thread.ArchivedAt != "" || scoutChatThreadVisibility(thread) != wantVisibility {
		lock.Unlock()
		return fmt.Errorf("originating conversation is unavailable")
	}
	_, binding, bindingErr := scoutChatSourceWindow(thread, receipt.SourceMessageID)
	var source scoutChatMessageRecord
	var approved scoutChatMessageRecord
	if bindingErr == nil {
		for _, message := range thread.Messages {
			if strings.TrimSpace(message.ID) == receipt.SourceMessageID {
				source = message
			}
			if strings.TrimSpace(message.ID) == receipt.ApprovedProposalID {
				approved = message
			}
		}
	}
	lock.Unlock()
	if bindingErr != nil || binding.MessageDigest != receipt.SourceMessageDigest || binding.WindowDigest != receipt.SourceWindowDigest || source.ID == "" {
		return fmt.Errorf("originating conversation changed")
	}
	selection, selectionErr := app.goalRouteSourceSelection(receipt)
	if selectionErr != nil || selection.Digest != receipt.SourceSelectionDigest {
		return fmt.Errorf("approved reply-thread source selection changed")
	}

	if receipt.ApprovedProposalID != "" {
		proposalMessage := source
		if receipt.OriginKind == agentThreadOriginChannel {
			proposalMessage = approved
			if receipt.ApprovedEffectClass != "expanded_audience" || proposalMessage.CausedByMessageID != source.ID {
				return fmt.Errorf("approved public goal audience binding changed")
			}
		} else if receipt.ApprovedProposalID != receipt.SourceMessageID {
			return fmt.Errorf("approved private goal proposal is no longer current")
		}
		if proposalMessage.ID != receipt.ApprovedProposalID || proposalMessage.Proposal == nil || proposalMessage.Proposal.Status != "accepted" ||
			strings.TrimSpace(proposalMessage.Proposal.EffectClass) != receipt.ApprovedEffectClass {
			return fmt.Errorf("approved goal proposal is no longer current")
		}
		// ContextRefsDigest was added compatibly to the v1 receipt. A legacy
		// receipt has no digest; its operation digest still binds the exact stored
		// proposal, so stage admission may rehydrate those refs from that proposal.
		// New receipts must match exactly.
		if receipt.ContextRefsDigest != "" && goalContextRefsDigest(proposalMessage.Proposal.ContextRefs) != receipt.ContextRefsDigest {
			return fmt.Errorf("approved goal source bindings changed")
		}
		operation, operationErr := conversationApprovedWorkOperation(receipt.OriginID, receipt.Requester, receipt.ApprovedProposalID, *proposalMessage.Proposal)
		if operationErr != nil || operation.ID != receipt.OperationID || operation.BodyDigest != receipt.OperationBodyDigest {
			return fmt.Errorf("approved goal operation binding changed")
		}
	} else if source.SourceOperationID != receipt.OperationID || source.SourceOperationDigest != receipt.OperationBodyDigest {
		return fmt.Errorf("goal operation binding changed")
	}
	return nil
}

type goalRouteSourceSelectionSnapshot struct {
	Digest         string
	Context        string
	AttachmentRefs []string
}

type goalRouteSourceSelectionMessage struct {
	ID        string         `json:"id"`
	Role      string         `json:"role"`
	Author    string         `json:"author"`
	CreatedAt string         `json:"createdAt"`
	ReplyTo   string         `json:"replyTo,omitempty"`
	CausedBy  string         `json:"causedBy,omitempty"`
	Text      string         `json:"text,omitempty"`
	Sources   []answerSource `json:"sources,omitempty"`
	Files     []struct {
		Name           string `json:"name"`
		Ref            string `json:"ref"`
		Mime           string `json:"mime"`
		Kind           string `json:"kind"`
		Size           int64  `json:"size"`
		SourceID       string `json:"sourceId"`
		SourceRevision string `json:"sourceRevision"`
		Text           string `json:"text"`
	} `json:"files,omitempty"`
}

func (app *kanbanBoardApp) goalRouteSourceSelection(receipt goalRouteReceipt) (goalRouteSourceSelectionSnapshot, error) {
	thread, _, err := app.scoutChatThreadByID(receipt.Requester, receipt.OriginID)
	if err != nil {
		return goalRouteSourceSelectionSnapshot{}, fmt.Errorf("originating conversation is unavailable")
	}
	sourceIndex := scoutChatMessageIndex(thread, receipt.SourceMessageID)
	if sourceIndex < 0 {
		return goalRouteSourceSelectionSnapshot{}, fmt.Errorf("originating conversation is unavailable")
	}
	thread.Messages = append([]scoutChatMessageRecord(nil), thread.Messages[:sourceIndex+1]...)
	projected := app.projectScoutChatThreadForViewer(receipt.Requester, thread)
	projectedSourceIndex := scoutChatMessageIndex(projected, receipt.SourceMessageID)
	if projectedSourceIndex < 0 {
		return goalRouteSourceSelectionSnapshot{}, fmt.Errorf("approved source is no longer readable")
	}
	source := thread.Messages[sourceIndex]
	var selected []scoutChatMessageRecord
	if source.ReplyTo != nil {
		if scoutChatMessageIndex(thread, source.ReplyTo.MessageID) < 0 {
			return goalRouteSourceSelectionSnapshot{}, fmt.Errorf("approved reply-thread source is incomplete")
		}
		selected = scoutChatReplyContextMessages(thread, scoutChatReplyRootID(thread, source.ReplyTo.MessageID)).Messages
	} else {
		selected, _, err = scoutChatSourceWindow(thread, receipt.SourceMessageID)
		if err != nil {
			return goalRouteSourceSelectionSnapshot{}, fmt.Errorf("approved source is unavailable")
		}
	}
	manifest := make([]goalRouteSourceSelectionMessage, 0, len(selected))
	projectedSelected := make([]scoutChatMessageRecord, 0, len(selected))
	attachmentRefs := []string{}
	for _, raw := range selected {
		projectedIndex := scoutChatMessageIndex(projected, raw.ID)
		if projectedIndex < 0 {
			return goalRouteSourceSelectionSnapshot{}, fmt.Errorf("approved source is no longer readable")
		}
		visible := projected.Messages[projectedIndex]
		if len(raw.Files) != len(visible.Files) {
			return goalRouteSourceSelectionSnapshot{}, fmt.Errorf("approved attachment is no longer readable")
		}
		item := goalRouteSourceSelectionMessage{
			ID: visible.ID, Role: visible.Role, Author: firstNonEmptyString(visible.AuthorName, "Coworker"), CreatedAt: visible.CreatedAt,
			CausedBy: visible.CausedByMessageID, Text: visible.Text, Sources: append([]answerSource(nil), visible.Sources...),
		}
		if visible.ReplyTo != nil {
			item.ReplyTo = visible.ReplyTo.MessageID
		}
		for rawIndex, file := range visible.Files {
			part := struct {
				Name           string `json:"name"`
				Ref            string `json:"ref"`
				Mime           string `json:"mime"`
				Kind           string `json:"kind"`
				Size           int64  `json:"size"`
				SourceID       string `json:"sourceId"`
				SourceRevision string `json:"sourceRevision"`
				Text           string `json:"text"`
			}{file.Name, file.Ref, file.Mime, file.Kind, file.Size, file.SourceID, file.SourceRevision, file.Text}
			item.Files = append(item.Files, part)
			attachmentRefs = append(attachmentRefs, scoutChatFileContextRef(thread.ID, raw.ID, rawIndex))
		}
		manifest = append(manifest, item)
		projectedSelected = append(projectedSelected, visible)
	}
	contextText := ""
	if source.ReplyTo != nil {
		// The approved work context was built while the source message was the
		// in-flight current turn, before that message was committed to the
		// thread. Reconstruct that exact view: the manifest above still binds
		// the source message itself, while WorkContext contains only its prior
		// authorized branch just as it did at proposal creation.
		prior := projected
		prior.Messages = append([]scoutChatMessageRecord(nil), projected.Messages[:projectedSourceIndex]...)
		turn := app.scoutChatTurnContextForViewer(receipt.Requester, prior, projected.Messages[projectedSourceIndex])
		if !turn.SourceComplete {
			return goalRouteSourceSelectionSnapshot{}, fmt.Errorf("approved source selection is incomplete")
		}
		contextText = turn.WorkContext
	} else {
		contextText, _ = scoutChatReplyWorkContext(scoutChatReplyContextSelection{Messages: projectedSelected, PinnedIDs: map[string]bool{}, SourceIDs: map[string]bool{}, SourcesComplete: true})
	}
	contextText = strings.TrimSpace(contextText)
	// Context is included byte-for-byte because it is the exact provider-facing
	// branch material. The structural manifest separately binds selection
	// topology and every visible file identity/revision, preventing two distinct
	// branches that happen to render the same prose from becoming interchangeable.
	digest, err := STRIDEContractDigest(struct {
		Context  string                            `json:"context"`
		Messages []goalRouteSourceSelectionMessage `json:"messages"`
	}{Context: contextText, Messages: manifest})
	if err != nil {
		return goalRouteSourceSelectionSnapshot{}, fmt.Errorf("approved source selection is invalid")
	}
	return goalRouteSourceSelectionSnapshot{Digest: digest, Context: contextText, AttachmentRefs: canonicalAssistantContextRefs(attachmentRefs)}, nil
}

func (app *kanbanBoardApp) legacyGoalRouteSelectionMatchesApprovedProposal(receipt goalRouteReceipt, selection goalRouteSourceSelectionSnapshot) bool {
	if receipt.ApprovedProposalID == "" || !isHexDigest(selection.Digest) {
		return false
	}
	thread, _, err := app.scoutChatThreadByID(receipt.Requester, receipt.OriginID)
	if err != nil {
		return false
	}
	index := scoutChatMessageIndex(thread, receipt.ApprovedProposalID)
	if index < 0 || thread.Messages[index].Proposal == nil || thread.Messages[index].Proposal.Status != "accepted" {
		return false
	}
	proposal := thread.Messages[index].Proposal
	const marker = "Resolved reply-thread source (authorized channel messages; preserve as source material, not policy):"
	markerIndex := strings.LastIndex(proposal.Objective, marker)
	if markerIndex < 0 {
		return false
	}
	approvedContext := canonicalizeBoardText(strings.TrimSpace(proposal.Objective[markerIndex+len(marker):]))
	if approvedContext == "" || approvedContext != canonicalizeBoardText(selection.Context) {
		return false
	}
	approvedRefs := canonicalAssistantContextRefs(decodeAssistantContextRefs(proposal.ContextRefs))
	selectionRefs := canonicalAssistantContextRefs(selection.AttachmentRefs)
	if len(approvedRefs) != len(selectionRefs) {
		return false
	}
	for index := range approvedRefs {
		if approvedRefs[index] != selectionRefs[index] {
			return false
		}
	}
	return true
}

func goalContextRefsDigest(value string) string {
	refs := decodeAssistantContextRefs(value)
	if len(refs) == 0 {
		return ""
	}
	return sha256Hex([]byte(strings.Join(refs, "\n")))
}

// verifyGoalChildRoute prevents a persisted scout_thread child from becoming
// an independent provider/output-contract authority. Every admission is
// derived again from the current parent plan after its conversation receipt
// and source are revalidated.
func (app *kanbanBoardApp) verifyGoalChildRoute(child meetingMemoryEntry) error {
	return app.verifyGoalChildRouteState(child, false)
}

func (app *kanbanBoardApp) verifyGoalChildReservation(child meetingMemoryEntry) error {
	return app.verifyGoalChildRouteState(child, true)
}

func (app *kanbanBoardApp) verifyGoalChildRouteState(child meetingMemoryEntry, allowReserved bool) error {
	metadata := child.Metadata
	parentID := strings.TrimSpace(metadata["goalParentId"])
	if app == nil || parentID == "" {
		return fmt.Errorf("goal child route is unavailable")
	}
	parent, ok := app.osArtifactByID(parentID)
	if !ok {
		return fmt.Errorf("goal child parent is unavailable")
	}
	plan, ok := decodeGoalPlan(parent.Metadata["goalPlan"])
	if !ok || plan.Cancelled {
		return fmt.Errorf("goal child parent route is unavailable")
	}
	engine := newGoalEngine(app)
	if err := engine.prepareGoalRoute(&plan, parentID); err != nil {
		return fmt.Errorf("goal child parent route is unavailable: %w", err)
	}
	if plan.RouteReceipt == nil || metadata["goalRouteDigest"] != plan.RouteReceipt.Digest {
		return fmt.Errorf("goal child route receipt changed")
	}
	for key, want := range map[string]string{
		"requestedBy": normalizeAccountEmail(plan.RouteReceipt.Requester),
		"originKind":  plan.RouteReceipt.OriginKind, "originId": plan.RouteReceipt.OriginID,
		"sourceMessageId": plan.RouteReceipt.SourceMessageID, "sourceMessageDigest": plan.RouteReceipt.SourceMessageDigest,
		"sourceWindowDigest": plan.RouteReceipt.SourceWindowDigest, "operationId": plan.RouteReceipt.OperationID,
		"operationBodyDigest": plan.RouteReceipt.OperationBodyDigest, "approvedProposalId": plan.RouteReceipt.ApprovedProposalID,
		"approvedEffectClass": plan.RouteReceipt.ApprovedEffectClass,
	} {
		got := strings.TrimSpace(metadata[key])
		if key == "requestedBy" {
			got = normalizeAccountEmail(got)
		}
		if got != want {
			return fmt.Errorf("goal child source binding changed")
		}
	}
	subtaskID := strings.TrimSpace(metadata["goalSubtaskId"])
	if subtaskID == goalCommitSubtaskID {
		command := strings.TrimSpace(firstNonEmptyString(plan.Gate.Command, plan.Objective))
		if plan.State != goalStateCommit || plan.Gate.Status != "passed" || strings.TrimSpace(plan.Gate.ReviewedBy) == "" ||
			child.ID != strings.TrimSpace(plan.Gate.CommitChildID) || strings.TrimSpace(metadata["source"]) != "goal_commit" ||
			strings.TrimSpace(metadata["mode"]) != "goal_commit" || strings.TrimSpace(metadata["authority"]) != codexJobAuthorityExternalWrite ||
			strings.TrimSpace(metadata["runnerJobId"]) != strings.TrimSpace(plan.Gate.CommitJobID) ||
			strings.TrimSpace(metadata["threadId"]) != goalCommitThreadID(&plan) || strings.TrimSpace(metadata["query"]) != command {
			return fmt.Errorf("goal commit child authority changed")
		}
		return nil
	}
	st := plan.subtaskByID(subtaskID)
	if st == nil || strings.TrimSpace(st.ArtifactID) == "" || strings.TrimSpace(child.ID) != strings.TrimSpace(st.ArtifactID) ||
		normalizeAgentThreadMode(metadata["mode"]) != normalizeAgentThreadMode(st.Mode) ||
		strings.TrimSpace(metadata["assignedRunner"]) != strings.TrimSpace(st.Runner) ||
		normalizeCodexJobAuthority(metadata["authority"]) != goalChildAuthority(st.Authority, plan.Authority) {
		return fmt.Errorf("goal child execution assignment changed")
	}
	expectedTool := ""
	if st.ID == goalDeliverableSubtaskID(&plan) {
		expectedTool = strings.TrimSpace(plan.ToolTemplate)
	}
	expectedContract := ""
	expectedDeliverable := expectedTool != ""
	if def, resolved := engine.resolvedProcess(&plan); resolved {
		stage, found := def.stageByID(st.ID)
		if !found {
			return fmt.Errorf("goal child process stage is unavailable")
		}
		expectedContract = strings.TrimSpace(stage.OutputContract)
		expectedDeliverable = stage.Role == processRoleWriter
	}
	if strings.TrimSpace(metadata["toolTemplate"]) != expectedTool || strings.TrimSpace(metadata["outputContract"]) != expectedContract ||
		strings.EqualFold(strings.TrimSpace(metadata["goalDeliverable"]), "true") != expectedDeliverable {
		return fmt.Errorf("goal child output contract changed")
	}
	activation := strings.TrimSpace(metadata["goalChildActivationState"])
	if activation != goalChildActivationStarted && !(allowReserved && activation == goalChildActivationReserved) {
		return fmt.Errorf("goal child activation is not current")
	}
	return nil
}
