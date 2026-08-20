package main

import (
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
	Contract            string `json:"contract"`
	Requester           string `json:"requester"`
	OriginKind          string `json:"originKind"`
	OriginID            string `json:"originId"`
	SourceMessageID     string `json:"sourceMessageId"`
	SourceMessageDigest string `json:"sourceMessageDigest"`
	SourceWindowDigest  string `json:"sourceWindowDigest"`
	OperationID         string `json:"operationId"`
	OperationBodyDigest string `json:"operationBodyDigest"`
	ApprovedProposalID  string `json:"approvedProposalId,omitempty"`
	ApprovedEffectClass string `json:"approvedEffectClass,omitempty"`
	ObjectiveDigest     string `json:"objectiveDigest"`
	ToolTemplate        string `json:"toolTemplate,omitempty"`
	ProcessID           string `json:"processId,omitempty"`
	Authority           string `json:"authority"`
	PackageID           string `json:"packageId,omitempty"`
	Digest              string `json:"digest"`
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
	}
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
		receipt.Requester != normalizeAccountEmail(goalPlanRequestedBy(*plan)) {
		return fmt.Errorf("goal route receipt no longer matches the saved plan")
	}
	wantDigest, err := receipt.contractDigest()
	if err != nil || !isHexDigest(receipt.Digest) || receipt.Digest != wantDigest {
		return fmt.Errorf("goal route receipt authentication failed")
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
		operation, operationErr := conversationApprovedWorkOperation(receipt.OriginID, receipt.Requester, receipt.ApprovedProposalID, *proposalMessage.Proposal)
		if operationErr != nil || operation.ID != receipt.OperationID || operation.BodyDigest != receipt.OperationBodyDigest {
			return fmt.Errorf("approved goal operation binding changed")
		}
	} else if source.SourceOperationID != receipt.OperationID || source.SourceOperationDigest != receipt.OperationBodyDigest {
		return fmt.Errorf("goal operation binding changed")
	}
	return nil
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
