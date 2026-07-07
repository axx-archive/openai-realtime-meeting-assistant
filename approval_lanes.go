package main

// Approval lanes — card 069's DEFAULT approval-governance decision as
// enforceable taxonomy. The three lanes RATIFY what the code already does
// rather than inventing new ceremony:
//
//   auto     — a human asked for a quick single pass (research, design,
//              artifacts, grill, workflow) at read_only/workspace_write
//              authority. Launches immediately: POST /assistant/threads
//              behavior today. The requester's tap IS the approval.
//   standard — multi-agent goal loops and tool-templated processes
//              (/assistant/goal, launchGoalThread) and ALL system-proposed
//              work (proposeCodexTask, the private-chat router). Exactly one
//              signed-in member approval: the requester's own tap or the
//              proposal confirm (resolveCodexProposal /
//              resolveScoutChatProposal).
//   heavy    — external_write work (deploys, pushes, email, production
//              mutations — the codexJobAuthorityForThread phrase class).
//              Parks at approval_required and ships only with the approval
//              admin (isArtifactApprovalAdmin) OR two distinct member
//              endorsements (the consensus door in
//              artifactRunnerActionHandler).
//
// There is NO token-cost accounting in this repo, so the honest cost proxy is
// the existing weight-class pair (quick single pass vs goal loop) plus the
// authority ladder — the lane payload carries rule STRINGS, never invented
// dollar figures. The 067 ticker reads approvalLanesPayload() via GET
// /assistant/tools; the 088 auto-select reads approvalLaneFor(). The decision
// itself rides the ledger as status=proposed (seedProposedGovernanceDecision)
// until the team ratifies it through POST /assistant/decisions/ratify.

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	approvalLaneAuto     = "auto"
	approvalLaneStandard = "standard"
	approvalLaneHeavy    = "heavy"

	// approvalConsensusRequired is how many distinct non-admin member
	// endorsements carry the same weight as one admin approval on the heavy
	// lane.
	approvalConsensusRequired = 2

	// approvalEndorsementsKey stores the JSON array of normalized member
	// emails that endorsed a parked heavy-lane artifact; approvalConsensusAtKey
	// marks the consensus as CONSUMED — stamped atomically with the flipping
	// endorsement so a concurrent third approve can never double-launch.
	approvalEndorsementsKey = "approvalEndorsements"
	approvalConsensusAtKey  = "approvalConsensusAt"
)

// approvalLaneFor classifies a launch or proposal into its governance lane
// from the dimensions the gates already enforce: mode ("goal" = multi-agent
// loop), toolTemplate (a tool/process id routes through the goal engine),
// authority (the read_only/workspace_write/external_write ladder), and
// whether the system — not a human — proposed the work. System-proposed work
// is NEVER auto: the proposal card exists to collect its one-member confirm.
func approvalLaneFor(mode string, toolTemplate string, authority string, systemProposed bool) string {
	if normalizeCodexJobAuthority(authority) == codexJobAuthorityExternalWrite {
		return approvalLaneHeavy
	}
	if systemProposed {
		return approvalLaneStandard
	}
	if strings.EqualFold(strings.TrimSpace(mode), "goal") || strings.TrimSpace(toolTemplate) != "" {
		return approvalLaneStandard
	}
	return approvalLaneAuto
}

// approvalLanesPayload is the single lane taxonomy served on GET
// /assistant/tools (the door the ticker and the palette already read). Rules
// are honest prose over the enforced dimensions — no dollar figures, because
// the repo has no usage meter to back them.
func approvalLanesPayload() []map[string]any {
	return []map[string]any{
		{
			"id":        approvalLaneAuto,
			"label":     "Auto",
			"rule":      "Human-requested quick single passes (research, design, artifacts, grill, workflow) at read_only or workspace_write authority launch immediately.",
			"approvers": "none — the requester's tap is the approval",
		},
		{
			"id":        approvalLaneStandard,
			"label":     "Standard",
			"rule":      "Multi-agent goal loops, tool-templated processes, and anything Scout proposes need exactly one signed-in member approval before launch.",
			"approvers": "any one signed-in member",
		},
		{
			"id":        approvalLaneHeavy,
			"label":     "Heavy",
			"rule":      "external_write work (deploys, pushes, email, production mutations) parks at approval_required and ships only with the approval admin or two distinct member endorsements.",
			"approvers": "the approval admin, or 2 distinct members",
		},
	}
}

// approvalEndorsementMu serializes the heavy-lane consensus read-modify-write:
// the endorsement list, the two-email check, and the consensus-consumed stamp
// all happen under one lock so exactly ONE request ever observes the flip to
// reached=true (the confirm-before-launch discipline from
// resolveCodexProposal).
var approvalEndorsementMu sync.Mutex

func decodeApprovalEndorsements(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var emails []string
	if json.Unmarshal([]byte(raw), &emails) != nil {
		return nil
	}
	cleaned := make([]string, 0, len(emails))
	for _, email := range emails {
		if email = normalizeAccountEmail(email); email != "" {
			cleaned = append(cleaned, email)
		}
	}
	return cleaned
}

// recordApprovalEndorsement records a non-admin member's approve on a parked
// heavy-lane artifact. Returns the endorsement list after recording and
// whether THIS call completed the consensus. The consensus-consumed stamp
// lands in the same artifact update as the flipping endorsement, so only one
// caller ever gets reached=true; later endorsements on a consumed consensus
// report reached=false (the launch is, or was, in flight). A retry by an
// existing endorser after clearApprovalConsensusStamp re-completes the pair.
func (app *kanbanBoardApp) recordApprovalEndorsement(artifactID string, endorserEmail string) ([]string, bool, error) {
	if app == nil || app.memory == nil {
		return nil, false, fmt.Errorf("artifacts are unavailable")
	}
	email := normalizeAccountEmail(endorserEmail)
	if email == "" {
		return nil, false, fmt.Errorf("endorser email is required")
	}

	approvalEndorsementMu.Lock()
	defer approvalEndorsementMu.Unlock()

	artifact, exists := app.osArtifactByID(artifactID)
	if !exists {
		return nil, false, fmt.Errorf("artifact not found")
	}
	if !artifactAwaitingApproval(artifact.Metadata) {
		return nil, false, fmt.Errorf("artifact is not waiting for external-write approval")
	}
	endorsements := decodeApprovalEndorsements(artifact.Metadata[approvalEndorsementsKey])
	if strings.TrimSpace(artifact.Metadata[approvalConsensusAtKey]) != "" {
		// Consensus already consumed — a goal checkpoint that re-parked after a
		// consensus-executed hold/revise stays admin-territory from here.
		return endorsements, false, nil
	}
	already := false
	for _, existing := range endorsements {
		if existing == email {
			already = true
			break
		}
	}
	if !already {
		endorsements = append(endorsements, email)
	}
	reached := len(endorsements) >= approvalConsensusRequired
	if already && !reached {
		// The same member twice stays 1/2 — nothing new to persist.
		return endorsements, false, nil
	}
	encoded, err := json.Marshal(endorsements)
	if err != nil {
		return nil, false, fmt.Errorf("encode endorsements: %w", err)
	}
	stamp := map[string]string{approvalEndorsementsKey: string(encoded)}
	if reached {
		stamp[approvalConsensusAtKey] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if _, _, err := app.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "", stamp); err != nil {
		return nil, false, err
	}
	return endorsements, reached, nil
}

// clearApprovalConsensusStamp reverts the consensus-consumed marker after a
// FAILED approve execution, so a retry by either endorser can complete the
// interrupted launch (mirrors the revert-to-proposed discipline in
// resolveCodexProposal).
func (app *kanbanBoardApp) clearApprovalConsensusStamp(artifactID string) {
	if app == nil || app.memory == nil {
		return
	}
	approvalEndorsementMu.Lock()
	defer approvalEndorsementMu.Unlock()
	artifact, exists := app.osArtifactByID(artifactID)
	if !exists {
		return
	}
	if _, _, err := app.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "", map[string]string{approvalConsensusAtKey: ""}); err != nil {
		log.Errorf("Failed to clear consensus stamp on artifact %s: %v", artifactID, err)
	}
}
