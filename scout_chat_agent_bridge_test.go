package main

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func hireResearchAgentForBridgeTest(t *testing.T, fixture strideProjectAuthorityFixture, listingID, directThreadID string) STRIDEProductTeamAgent {
	t.Helper()
	var hired STRIDEProductTeamAgent
	err := fixture.runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		trial, err := ctx.Product.beginTrial(listingID, strideRuntimePrincipalForEmail(fixture.user.Email), time.Now().UTC())
		if err != nil {
			return err
		}
		hired, err = ctx.Product.mutateAgent(trial.ID, trial.Revision, func(agent *STRIDEProductTeamAgent) error {
			agent.Status = "hired_fenced"
			agent.DirectThreadID = directThreadID
			agent.AccessRevoked = false
			agent.Lifecycle = append(agent.Lifecycle, "human_approved_hire", "provider_runtime_remains_fenced")
			return nil
		}, time.Now().UTC())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return hired
}

func TestDirectColtonThreadUsesApprovedResearchBridgeWithoutUnfencingSeat(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	directThreadID := strideProductAgentDirectThreadPrefix + "colton_bridge_test"
	hired := hireResearchAgentForBridgeTest(t, fixture, "colton-research", directThreadID)
	thread, _, err := fixture.app.ensureScoutChatThread(directThreadID, fixture.user.Email, fixture.user.Name, "Colton · agent", scoutChatVisibilityPrivate, nil)
	if err != nil {
		t.Fatal(err)
	}
	previousRunner := startAgentThreadAsync
	var launched scoutAgentThread
	startAgentThreadAsync = func(_ *kanbanBoardApp, thread scoutAgentThread) { launched = thread }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	response, err := fixture.app.appendScoutChatThreadMessage(context.Background(), fixture.user, thread.ID, "Research the Country+Golf competitive landscape with primary sources", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if launched.ID == "" || launched.Artifact.Metadata["agentId"] != hired.ID || launched.Artifact.Metadata["agentName"] != "Colton" ||
		launched.Artifact.Metadata["toolTemplate"] != "deep_research" || launched.Artifact.Metadata["authority"] != toolAuthorityReadOnly {
		t.Fatalf("launched=%+v metadata=%v", launched, launched.Artifact.Metadata)
	}
	if response["providerExecutionFenced"] != true || response["executionBridge"] != "scout_read_only_research_runner" || response["providerCalls"] != 0 {
		t.Fatalf("bridge disclosure=%v", response)
	}
	saved, ok := response["thread"].(scoutChatThreadRecord)
	if !ok || len(saved.Messages) < 2 {
		t.Fatalf("saved thread=%#v", response["thread"])
	}
	ref := saved.Messages[len(saved.Messages)-1].Thread
	if ref == nil || ref.AgentID != hired.ID || ref.AgentName != "Colton" || ref.DelegatedBy != "" || ref.ArtifactID != launched.Artifact.ID {
		t.Fatalf("work ref=%+v", ref)
	}
	if got := saved.Messages[len(saved.Messages)-1].Text; got != "I’m on it — I’m starting the research now, and I’ll bring the finished brief back here." || strings.Contains(got, "Colton picked") {
		t.Fatalf("direct Colton handoff is not first-person and conversational: %q", got)
	}
}

func TestScoutDeepResearchDelegatesToHiredColtonWithDurableAttribution(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	directThreadID := strideProductAgentDirectThreadPrefix + "colton_delegate_test"
	hired := hireResearchAgentForBridgeTest(t, fixture, "colton-research", directThreadID)
	thread, err := fixture.app.createScoutChatThread(fixture.user.Email, fixture.user.Name, "Scout research", "")
	if err != nil {
		t.Fatal(err)
	}
	previousRunner := startAgentThreadAsync
	var launched scoutAgentThread
	startAgentThreadAsync = func(_ *kanbanBoardApp, thread scoutAgentThread) { launched = thread }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	response, err := fixture.app.appendScoutChatThreadMessageWithTool(context.Background(), fixture.user, thread.ID, "Research how country clubs are modernizing member media", nil, "", "deep_research")
	if err != nil {
		t.Fatal(err)
	}
	if launched.Artifact.Metadata["agentId"] != hired.ID || launched.Artifact.Metadata["agentName"] != "Colton" || launched.Artifact.Metadata["delegatedBy"] != scoutParticipantName {
		t.Fatalf("delegated metadata=%v", launched.Artifact.Metadata)
	}
	saved, ok := response["thread"].(scoutChatThreadRecord)
	if !ok || len(saved.Messages) < 2 {
		t.Fatalf("saved thread=%#v", response["thread"])
	}
	message := saved.Messages[len(saved.Messages)-1]
	if message.Thread == nil || message.Thread.AgentName != "Colton" || message.Thread.DelegatedBy != scoutParticipantName {
		t.Fatalf("delegated work message=%+v", message)
	}
	if message.Text != "I tapped Colton for this — running against the research contract and gate rubric" {
		t.Fatalf("delegation copy=%q", message.Text)
	}
}

func TestDirectResearchCoworkerRequestsMissingInputWithoutLaunchingProvider(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	directThreadID := strideProductAgentDirectThreadPrefix + "colton_missing_input_test"
	hired := hireResearchAgentForBridgeTest(t, fixture, "colton-research", directThreadID)
	thread, _, err := fixture.app.ensureScoutChatThread(directThreadID, fixture.user.Email, fixture.user.Name, "Colton · agent", scoutChatVisibilityPrivate, nil)
	if err != nil {
		t.Fatal(err)
	}
	previousRunner := startAgentThreadAsync
	var launches atomic.Int64
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { launches.Add(1) }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	response, err := fixture.app.appendScoutChatThreadMessage(context.Background(), fixture.user, thread.ID, "Hey Colton", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if launches.Load() != 0 || response["providerCalls"] != 0 || response["missingInput"] != true || response["dependencyRequired"] != true {
		t.Fatalf("missing-input admission escaped provider fence: launches=%d response=%v", launches.Load(), response)
	}
	saved, ok := response["thread"].(scoutChatThreadRecord)
	if !ok || len(saved.Messages) != 2 {
		t.Fatalf("saved thread=%#v", response["thread"])
	}
	request := saved.Messages[1]
	if request.AuthorName != hired.DisplayName || request.Role != "scout" || request.Thread != nil ||
		!strings.Contains(request.Text, "topic or question") || !strings.Contains(request.Text, "decision or scope") || !strings.Contains(request.Text, "sources or Files") {
		t.Fatalf("named missing-input request=%+v", request)
	}
}

func TestAgentProfileIsReauthorizedAndCorrectedLearningReachesProviderPrompt(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	directThreadID := strideProductAgentDirectThreadPrefix + "colton_reauthorize_test"
	hired := hireResearchAgentForBridgeTest(t, fixture, "colton-research", directThreadID)
	thread, _, err := fixture.app.ensureScoutChatThread(directThreadID, fixture.user.Email, fixture.user.Name, "Colton · agent", scoutChatVisibilityPrivate, nil)
	if err != nil {
		t.Fatal(err)
	}
	previousRunner := startAgentThreadAsync
	var launched scoutAgentThread
	startAgentThreadAsync = func(_ *kanbanBoardApp, thread scoutAgentThread) { launched = thread }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	if _, err := fixture.app.appendScoutChatThreadMessage(context.Background(), fixture.user, thread.ID, "Research Country+Golf membership growth using primary sources", nil, ""); err != nil {
		t.Fatal(err)
	}

	var corrected STRIDEProductTeamAgent
	err = fixture.runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		agent, ok := ctx.Product.agentRecord(hired.ID)
		if !ok {
			return ErrSTRIDEProductUnknown
		}
		agent, err = ctx.Product.recordAgentLearning(agent.ID, agent.Revision, "delivery", "team", "Always bury the recommendation after the appendix.", time.Now().UTC())
		if err != nil {
			return err
		}
		corrected, err = ctx.Product.resolveAgentLearning(agent.ID, agent.Revision, agent.Learning[0].ID, "correct", "Lead with the recommendation, then show the evidence map.", time.Now().UTC())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	currentProfile, ok := fixture.app.strideAgentDirectThreadContext(directThreadID)
	if !ok || currentProfile.Digest == launched.Artifact.Metadata["agentDigest"] || currentProfile.Digest == "" {
		t.Fatalf("learning correction did not advance profile digest: old=%q current=%q", launched.Artifact.Metadata["agentDigest"], currentProfile.Digest)
	}

	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerOpenAIText)
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	providerCalls := 0
	var instructions string
	result, err := fixture.app.produceAgentThreadArtifactWithWorker(context.Background(), launched, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		providerCalls++
		instructions = request.Instructions
		return "# Research brief\n\nVerified.", nil
	})
	if err != nil || !result.Terminal || providerCalls != 1 {
		t.Fatalf("reauthorized run result=%+v calls=%d err=%v", result, providerCalls, err)
	}
	if result.Metadata["agentDigest"] != currentProfile.Digest || result.Metadata["agentReauthorizedAt"] == "" {
		t.Fatalf("provider result did not preserve current reauthorization evidence: metadata=%v current=%q", result.Metadata, currentProfile.Digest)
	}
	for _, want := range []string{
		"Approved capabilities: deep_research, evidence_map, research_brief, source_synthesis",
		"Package-authored operating principles (not observed facts about a person)",
		"The useful answer is often one source beyond the obvious search result.",
		"Current human-reviewed team learning (reviewed or corrected records only)",
		corrected.Learning[0].Summary,
	} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("provider prompt missing %q:\n%s", want, instructions)
		}
	}
	if strings.Contains(instructions, "Always bury the recommendation") {
		t.Fatalf("stale reviewed learning reached provider after correction:\n%s", instructions)
	}
}

func TestAgentProfileReauthorizationStopsPausedSeatBeforeProvider(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	directThreadID := strideProductAgentDirectThreadPrefix + "colton_pause_test"
	hired := hireResearchAgentForBridgeTest(t, fixture, "colton-research", directThreadID)
	thread, _, err := fixture.app.ensureScoutChatThread(directThreadID, fixture.user.Email, fixture.user.Name, "Colton · agent", scoutChatVisibilityPrivate, nil)
	if err != nil {
		t.Fatal(err)
	}
	previousRunner := startAgentThreadAsync
	var launched scoutAgentThread
	startAgentThreadAsync = func(_ *kanbanBoardApp, thread scoutAgentThread) { launched = thread }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	if _, err := fixture.app.appendScoutChatThreadMessage(context.Background(), fixture.user, thread.ID, "Research Country+Golf creator partnerships with current sources", nil, ""); err != nil {
		t.Fatal(err)
	}
	err = fixture.runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		_, err := ctx.Product.mutateAgent(hired.ID, hired.Revision, func(agent *STRIDEProductTeamAgent) error {
			agent.Status = "paused"
			agent.AccessRevoked = true
			agent.Lifecycle = append(agent.Lifecycle, "paused_by_human")
			return nil
		}, time.Now().UTC())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerOpenAIText)
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	providerCalls := 0
	_, err = fixture.app.produceAgentThreadArtifactWithWorker(context.Background(), launched, func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		providerCalls++
		return "must not run", nil
	})
	if err == nil || !strings.Contains(err.Error(), "assigned agent is unavailable") || providerCalls != 0 {
		t.Fatalf("paused seat reauthorization err=%v providerCalls=%d", err, providerCalls)
	}
}
