package main

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestSTRIDEStaffingLabeledCorpusSelectsOnlyExactEligibleSeatAndAbstains(t *testing.T) {
	now := time.Date(2026, 7, 31, 18, 0, 0, 0, time.UTC)
	for index := 0; index < 200; index++ {
		product := NewSTRIDEProductState()
		workforce := NewSTRIDEWorkforceRuntime()
		agentID := fmt.Sprintf("agent_staff_%03d", index)
		listingID := "mary-marketing"
		projectID := fmt.Sprintf("project_%03d", index)
		destination := fmt.Sprintf("thread_%03d", index)
		assignment := STRIDEProductAgentAssignment{ID: fmt.Sprintf("assignment_%03d", index), ProjectOrChannel: projectID, Role: "marketing_partner", Responsibility: "Prepare the approved project brief.", Destination: destination, Status: "active_fenced", CreatedAt: now}
		agent := STRIDEProductTeamAgent{ID: agentID, ListingID: listingID, DisplayName: "Mary", Category: "marketing", Status: "hired_fenced", OwnerID: "member_aj", DirectThreadID: "thread_agent_" + agentID, Revision: 2,
			Config: STRIDEProductAgentConfig{Memberships: []string{projectID}, PerRunBudgetCents: 25, DailyBudgetCents: 100, Proactivity: "disabled"}, Assignments: []STRIDEProductAgentAssignment{assignment}, ProviderExecutionFenced: true, Lifecycle: []string{"human_approved_hire"}, CreatedAt: now, UpdatedAt: now}
		product.agents[agentID] = agent
		capability := STRIDEReference{ContractType: STRIDEContractAgentCapabilityManifest, ID: "capability_" + agentID, Revision: 1, Digest: temporalDigest("capability:" + agentID)}
		workforce.seats[agentID] = STRIDEWorkforceSeat{ID: agentID, OrgIdentity: "org_agent:" + agentID, DirectThread: agent.DirectThreadID, Package: STRIDEReference{ContractType: STRIDEContractAgentPackageManifest, ID: "package_" + agentID, Revision: 1, Digest: temporalDigest("package:" + agentID)}, Listing: STRIDEReference{ContractType: STRIDEContractMarketplaceListing, ID: "listing_" + agentID, Revision: 1, Digest: temporalDigest("listing:" + agentID)}, Capability: capability, Route: STRIDEReference{ContractType: STRIDEContractOutcome, ID: "route_" + agentID, Revision: 1, Digest: temporalDigest("route:" + agentID)}, Owner: agent.OwnerID, Memberships: []string{projectID}, PerRunBudgetCents: 25, DailyBudgetCents: 100, Concurrency: 1, Proactivity: "disabled", Status: "active", ActivationStage: "complete", CreatedAt: now, UpdatedAt: now}

		request := STRIDEStaffingRequest{RequestID: fmt.Sprintf("staffing_%03d", index), ProjectOrChannel: projectID, Destination: destination, RequiredCategory: "marketing", RequiredCapabilityID: capability.ID, MaxPerRunBudgetCents: 25, CreatedAt: now}
		expectSelection := true
		switch index % 10 {
		case 1:
			request.ProjectOrChannel = "wrong_project"
			expectSelection = false
		case 2:
			request.Destination = "wrong_destination"
			expectSelection = false
		case 3:
			request.RequiredCategory = "design"
			expectSelection = false
		case 4:
			request.RequiredCapabilityID = "capability_wrong"
			expectSelection = false
		case 5:
			request.MaxPerRunBudgetCents = 24
			expectSelection = false
		case 6:
			agent.Status, agent.AccessRevoked = "paused", true
			product.agents[agentID] = agent
			expectSelection = false
		case 7:
			seat := workforce.seats[agentID]
			seat.Status, seat.AccessRevoked = "offboarded", true
			workforce.seats[agentID] = seat
			expectSelection = false
		case 8:
			request.AllowedAgentIDs = []string{"another_agent"}
			expectSelection = false
		case 9:
			agent.Assignments = append(agent.Assignments, STRIDEProductAgentAssignment{ID: "duplicate_assignment", ProjectOrChannel: projectID, Role: "marketing_partner", Responsibility: "Duplicate should fail closed.", Destination: destination, Status: "active_fenced", CreatedAt: now.Add(time.Second)})
			product.agents[agentID] = agent
			expectSelection = false
		}

		recommendation, err := RecommendSTRIDEStaffing(product, workforce, request)
		if err != nil {
			t.Fatalf("case %d: recommend: %v", index, err)
		}
		if recommendation.ProviderExecutionFenced != true || recommendation.Digest == "" {
			t.Fatalf("case %d: unsafe recommendation=%#v", index, recommendation)
		}
		if expectSelection {
			if recommendation.Abstained || recommendation.SelectedAgentID != agentID || len(recommendation.Eligible) != 1 || recommendation.Assignment == nil || recommendation.Capability == nil {
				t.Fatalf("case %d: expected exact selection, got %#v", index, recommendation)
			}
		} else if !recommendation.Abstained || recommendation.SelectedAgentID != "" || len(recommendation.Eligible) != 0 {
			t.Fatalf("case %d: expected abstention, got %#v", index, recommendation)
		}
	}
}

func TestSTRIDEStaffingRecommendationIsStableAndCannotGrantRuntime(t *testing.T) {
	now := time.Date(2026, 7, 31, 18, 0, 0, 0, time.UTC)
	product := NewSTRIDEProductState()
	workforce := NewSTRIDEWorkforceRuntime()
	for _, agentID := range []string{"agent_zeta", "agent_alpha"} {
		agent := STRIDEProductTeamAgent{ID: agentID, ListingID: "mary-marketing", DisplayName: agentID, Category: "marketing", Status: "hired_fenced", OwnerID: "member_aj", DirectThreadID: "thread_" + agentID, Revision: 2, Config: STRIDEProductAgentConfig{Memberships: []string{"dog_perfect"}, PerRunBudgetCents: 10, DailyBudgetCents: 100, Proactivity: "disabled"}, Assignments: []STRIDEProductAgentAssignment{{ID: "assignment_" + agentID, ProjectOrChannel: "dog_perfect", Role: "marketing_partner", Responsibility: "Prepare a brief.", Destination: "thread_dog_perfect", Status: "active_fenced", CreatedAt: now}}, ProviderExecutionFenced: true, Lifecycle: []string{"human_approved_hire"}, CreatedAt: now, UpdatedAt: now}
		product.agents[agentID] = agent
		workforce.seats[agentID] = STRIDEWorkforceSeat{ID: agentID, OrgIdentity: "org_agent:" + agentID, DirectThread: agent.DirectThreadID, Package: STRIDEReference{ContractType: STRIDEContractAgentPackageManifest, ID: "package_" + agentID, Revision: 1, Digest: temporalDigest("package:" + agentID)}, Listing: STRIDEReference{ContractType: STRIDEContractMarketplaceListing, ID: "listing_" + agentID, Revision: 1, Digest: temporalDigest("listing:" + agentID)}, Capability: STRIDEReference{ContractType: STRIDEContractAgentCapabilityManifest, ID: "capability_" + agentID, Revision: 1, Digest: temporalDigest("capability:" + agentID)}, Route: STRIDEReference{ContractType: STRIDEContractOutcome, ID: "route_" + agentID, Revision: 1, Digest: temporalDigest("route:" + agentID)}, Owner: "member_aj", Memberships: []string{"dog_perfect"}, PerRunBudgetCents: 10, DailyBudgetCents: 100, Concurrency: 1, Proactivity: "disabled", Status: "active", ActivationStage: "complete", CreatedAt: now, UpdatedAt: now}
	}
	request := STRIDEStaffingRequest{RequestID: "staffing_stable", ProjectOrChannel: "dog_perfect", Destination: "thread_dog_perfect", RequiredCategory: "marketing", MaxPerRunBudgetCents: 10, CreatedAt: now}
	first, err := RecommendSTRIDEStaffing(product, workforce, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RecommendSTRIDEStaffing(product, workforce, request)
	if err != nil || first.Digest != second.Digest || first.SelectedAgentID != "agent_alpha" {
		t.Fatalf("unstable first=%#v second=%#v err=%v", first, second, err)
	}
	if grant, err := workforce.IssueRuntimeGrant(STRIDEWorkforceActor{ID: "member_aj", IsAdmin: true}, first.SelectedAgentID, now); !errors.Is(err, ErrSTRIDEWorkforceFenced) || !grant.Fenced {
		t.Fatalf("recommendation escaped provider fence grant=%#v err=%v", grant, err)
	}
}
