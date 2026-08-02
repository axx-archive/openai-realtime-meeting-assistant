package main

// STRIDE staffing is a deterministic recommendation layer, never an authority
// or execution layer. It can narrow an already human-approved roster to an
// exact active assignment, but it cannot create assignments, mint capability
// tokens, start provider work, or widen an audience.

import (
	"errors"
	"sort"
	"time"
)

var ErrSTRIDEStaffingInvalid = errors.New("invalid STRIDE staffing request")

type STRIDEStaffingRequest struct {
	RequestID            string    `json:"requestId"`
	ProjectOrChannel     string    `json:"projectOrChannel"`
	Destination          string    `json:"destination"`
	RequiredCategory     string    `json:"requiredCategory"`
	RequiredCapabilityID string    `json:"requiredCapabilityId,omitempty"`
	AllowedAgentIDs      []string  `json:"allowedAgentIds,omitempty"`
	MaxPerRunBudgetCents int64     `json:"maxPerRunBudgetCents"`
	CreatedAt            time.Time `json:"createdAt"`
}

func (request STRIDEStaffingRequest) Validate() error {
	if !strideIdentifier(request.RequestID) || !strideIdentifier(request.ProjectOrChannel) || !strideIdentifier(request.Destination) ||
		!strideIdentifier(request.RequiredCategory) || request.MaxPerRunBudgetCents < 0 || request.CreatedAt.IsZero() ||
		(request.RequiredCapabilityID != "" && !strideIdentifier(request.RequiredCapabilityID)) ||
		(len(request.AllowedAgentIDs) > 0 && !uniqueSTRIDEIDs(request.AllowedAgentIDs)) {
		return ErrSTRIDEStaffingInvalid
	}
	return nil
}

type STRIDEStaffingCandidate struct {
	AgentID              string          `json:"agentId"`
	DisplayName          string          `json:"displayName"`
	Category             string          `json:"category"`
	ProductRevision      int64           `json:"productRevision"`
	Assignment           STRIDEReference `json:"assignment"`
	Capability           STRIDEReference `json:"capability"`
	PerRunBudgetCents    int64           `json:"perRunBudgetCents"`
	ProductStateDigest   string          `json:"productStateDigest"`
	WorkforceStateDigest string          `json:"workforceStateDigest"`
}

type STRIDEStaffingRecommendation struct {
	RequestID               string                    `json:"requestId"`
	SelectedAgentID         string                    `json:"selectedAgentId,omitempty"`
	SelectedProductRevision int64                     `json:"selectedProductRevision,omitempty"`
	Assignment              *STRIDEReference          `json:"assignment,omitempty"`
	Capability              *STRIDEReference          `json:"capability,omitempty"`
	Eligible                []STRIDEStaffingCandidate `json:"eligible"`
	Abstained               bool                      `json:"abstained"`
	Reason                  string                    `json:"reason"`
	ProviderExecutionFenced bool                      `json:"providerExecutionFenced"`
	Digest                  string                    `json:"digest"`
}

// RecommendSTRIDEStaffing snapshots both durable control planes, applies exact
// eligibility rules, and returns a content-bound recommendation. A caller must
// still revalidate current assignment, capability, audience, and approval at
// the later execution boundary.
func RecommendSTRIDEStaffing(product *STRIDEProductState, workforce *STRIDEWorkforceRuntime, request STRIDEStaffingRequest) (STRIDEStaffingRecommendation, error) {
	if product == nil || workforce == nil || request.Validate() != nil {
		return STRIDEStaffingRecommendation{}, ErrSTRIDEStaffingInvalid
	}
	productSnapshot, err := product.Snapshot()
	if err != nil {
		return STRIDEStaffingRecommendation{}, ErrSTRIDEStaffingInvalid
	}
	workforceSnapshot, err := workforce.Snapshot()
	if err != nil {
		return STRIDEStaffingRecommendation{}, ErrSTRIDEStaffingInvalid
	}
	allowed := make(map[string]bool, len(request.AllowedAgentIDs))
	for _, id := range request.AllowedAgentIDs {
		allowed[id] = true
	}
	seats := make(map[string]STRIDEWorkforceSeat, len(workforceSnapshot.Seats))
	for _, seat := range workforceSnapshot.Seats {
		seats[seat.ID] = seat
	}

	recommendation := STRIDEStaffingRecommendation{RequestID: request.RequestID, Abstained: true, Reason: "no_eligible_agent", ProviderExecutionFenced: true, Eligible: []STRIDEStaffingCandidate{}}
	for _, agent := range productSnapshot.Agents {
		if len(allowed) > 0 && !allowed[agent.ID] || agent.Status != "hired_fenced" || agent.AccessRevoked || !agent.ProviderExecutionFenced || agent.Category != request.RequiredCategory {
			continue
		}
		seat, found := seats[agent.ID]
		if !found || seat.Status != "active" || seat.AccessRevoked || seat.Owner != agent.OwnerID || seat.PerRunBudgetCents < 0 ||
			(request.MaxPerRunBudgetCents > 0 && seat.PerRunBudgetCents > request.MaxPerRunBudgetCents) || seat.Capability.Validate() != nil ||
			seat.Capability.ContractType != STRIDEContractAgentCapabilityManifest ||
			(request.RequiredCapabilityID != "" && seat.Capability.ID != request.RequiredCapabilityID) {
			continue
		}
		assignment, ok := exactSTRIDEStaffingAssignment(agent.Assignments, request.ProjectOrChannel, request.Destination)
		if !ok {
			continue
		}
		assignmentDigest, digestErr := STRIDEContractDigest(struct {
			AgentID       string
			AgentRevision int64
			Assignment    STRIDEProductAgentAssignment
		}{agent.ID, agent.Revision, assignment})
		if digestErr != nil {
			return STRIDEStaffingRecommendation{}, ErrSTRIDEStaffingInvalid
		}
		assignmentRef := STRIDEReference{ContractType: STRIDEContractAgentAssignment, ID: assignment.ID, Revision: agent.Revision, Digest: assignmentDigest}
		if assignmentRef.Validate() != nil {
			continue
		}
		recommendation.Eligible = append(recommendation.Eligible, STRIDEStaffingCandidate{
			AgentID: agent.ID, DisplayName: agent.DisplayName, Category: agent.Category, ProductRevision: agent.Revision,
			Assignment: assignmentRef, Capability: seat.Capability, PerRunBudgetCents: seat.PerRunBudgetCents,
			ProductStateDigest: productSnapshot.Digest, WorkforceStateDigest: workforceSnapshot.Digest,
		})
	}
	sort.Slice(recommendation.Eligible, func(i, j int) bool {
		if recommendation.Eligible[i].PerRunBudgetCents != recommendation.Eligible[j].PerRunBudgetCents {
			return recommendation.Eligible[i].PerRunBudgetCents < recommendation.Eligible[j].PerRunBudgetCents
		}
		return recommendation.Eligible[i].AgentID < recommendation.Eligible[j].AgentID
	})
	if len(recommendation.Eligible) > 0 {
		selected := recommendation.Eligible[0]
		recommendation.SelectedAgentID = selected.AgentID
		recommendation.SelectedProductRevision = selected.ProductRevision
		assignment, capability := selected.Assignment, selected.Capability
		recommendation.Assignment, recommendation.Capability = &assignment, &capability
		recommendation.Abstained = false
		recommendation.Reason = "exact_assignment_and_capability_match"
	}
	recommendation.Digest, err = STRIDEContractDigest(struct {
		Request        STRIDEStaffingRequest
		Recommendation STRIDEStaffingRecommendation
	}{request, recommendation})
	if err != nil {
		return STRIDEStaffingRecommendation{}, ErrSTRIDEStaffingInvalid
	}
	return recommendation, nil
}

func exactSTRIDEStaffingAssignment(assignments []STRIDEProductAgentAssignment, projectOrChannel, destination string) (STRIDEProductAgentAssignment, bool) {
	var selected STRIDEProductAgentAssignment
	found := false
	for _, assignment := range assignments {
		if assignment.Status != "active_fenced" || assignment.ProjectOrChannel != projectOrChannel || assignment.Destination != destination {
			continue
		}
		if found {
			// Ambiguous duplicate authority is never resolved by a ranking model.
			return STRIDEProductAgentAssignment{}, false
		}
		selected, found = assignment, true
	}
	return selected, found
}
