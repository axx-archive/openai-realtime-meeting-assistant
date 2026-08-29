package main

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestFixedAddressableRolesReachGovernedWorkAdmission(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	table, err := fixture.app.ensureTable(fixture.user.Email)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		request    string
		agentID    string
		mode       string
		capability string
	}{
		{name: "researcher", request: "@Researcher research the market opportunity and prepare a source-bound report", agentID: "agent_researcher", mode: "research", capability: "deep_research"},
		{name: "presenter", request: "@Presenter create a presentation about the market opportunity", agentID: "agent_presenter", mode: "design", capability: "presentation_deck"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile, mode, ok := fixture.app.strideTargetedAgentWorkRequest(table, tc.request, nil, nil)
			if !ok || mode != tc.mode || profile.AgentID != tc.agentID || !containsSTRIDEID(profile.Capabilities, tc.capability) {
				t.Fatalf("targeted role did not reach governed work routing: profile=%#v mode=%q ok=%v", profile, mode, ok)
			}
			if profile.ProviderExecutionFenced {
				t.Fatal("fixed specialist remained fenced like a legacy marketplace seat")
			}

			metadata := agentThreadGoalSpecForProfile(profile, "").metadata()
			metadata["requestedBy"] = fixture.user.Email
			admitted, err := fixture.app.reauthorizeAgentThreadProfile(scoutAgentThread{
				ID: "fixed-role-admission-" + tc.name, Mode: mode,
				Artifact: meetingMemoryEntry{Metadata: metadata},
			})
			if err != nil {
				t.Fatalf("fixed role was display-only at the provider admission fence: %v", err)
			}
			if admitted.Artifact.Metadata["agentId"] != tc.agentID || admitted.Artifact.Metadata["agentReauthorizedAt"] == "" {
				t.Fatalf("provider admission lost fixed identity: %v", admitted.Artifact.Metadata)
			}
		})
	}
}

func TestFixedPresenterLaunchesThroughConfirmedPublicWork(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	table, err := fixture.app.ensureTable(fixture.user.Email)
	if err != nil {
		t.Fatal(err)
	}
	response, err := fixture.app.appendScoutChatThreadMessage(context.Background(), fixture.user, table.ID, "@Presenter create a presentation about the market opportunity", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	proposal, ok := response["proposal"].(*scoutRouterProposal)
	if !ok || proposal.AgentID != "agent_presenter" || proposal.Mode != "design" || response["approvalRequired"] != true {
		t.Fatalf("Presenter did not produce a governed confirmation card: %#v", response)
	}
	thread := response["thread"].(scoutChatThreadRecord)
	cardID := thread.Messages[len(thread.Messages)-1].ID
	previousRunner := startAgentThreadAsync
	var launches atomic.Int64
	var launched scoutAgentThread
	startAgentThreadAsync = func(_ *kanbanBoardApp, run scoutAgentThread) {
		launches.Add(1)
		launched = run
	}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	if _, err := fixture.app.resolveScoutChatProposal(context.Background(), fixture.user, table.ID, scoutChatProposalAction{Action: "accepted", MessageID: cardID}); err != nil {
		t.Fatal(err)
	}
	if launches.Load() != 1 || launched.Mode != "design" || launched.Artifact.Metadata["agentId"] != "agent_presenter" || launched.Artifact.Metadata["agentName"] != "Presenter" {
		t.Fatalf("confirmed Presenter work did not reach the bounded runner: launches=%d run=%+v metadata=%v", launches.Load(), launched, launched.Artifact.Metadata)
	}
}
