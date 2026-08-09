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

func TestPublicAgentMentionRequiresExplicitWorkAndConfirmation(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	hired := hireResearchAgentForBridgeTest(t, fixture, "colton-research", strideProductAgentDirectThreadPrefix+"public_mention")
	table, err := fixture.app.ensureTable(fixture.user.Email)
	if err != nil {
		t.Fatal(err)
	}
	previousRunner := startAgentThreadAsync
	var launches atomic.Int64
	var launched scoutAgentThread
	startAgentThreadAsync = func(_ *kanbanBoardApp, thread scoutAgentThread) {
		launches.Add(1)
		launched = thread
	}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	social, err := fixture.app.appendScoutChatThreadMessage(context.Background(), fixture.user, table.ID, "@Colton loved your note on that article", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if social["proposal"] != nil || social["answer"] != nil || launches.Load() != 0 {
		t.Fatalf("social mention became work: response=%v launches=%d", social, launches.Load())
	}

	root := scoutChatMessageRecord{
		ID: "article-root", Kind: "message", Role: "user", Text: "https://example.com/disney-tiktok",
		CreatedAt: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), AuthorName: "Tyler", AuthorEmail: "tyler@shareability.com",
	}
	if _, err := fixture.app.commitScoutChatThreadMessages(fixture.user.Email, table.ID, root); err != nil {
		t.Fatal(err)
	}
	response, err := fixture.app.appendScoutChatThreadMessageWithReplyAndTool(
		context.Background(), fixture.user, table.ID,
		"@Colton can you dig into that article, search the web, and analyze the market implications?",
		nil, "", root.ID, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	proposal, ok := response["proposal"].(*scoutRouterProposal)
	if !ok || proposal.AgentID != hired.ID || proposal.AgentName != hired.DisplayName || proposal.Mode != "research" || proposal.Status != "" {
		t.Fatalf("targeted proposal=%#v", response["proposal"])
	}
	if !strings.Contains(proposal.Objective, root.Text) || strings.Contains(proposal.Query, root.Text) || strings.Contains(proposal.Summary, root.Text) {
		t.Fatalf("reply context was not bound only to the worker objective: objective=%q query=%q summary=%q", proposal.Objective, proposal.Query, proposal.Summary)
	}
	if response["approvalRequired"] != true || response["providerCalls"] != 0 || launches.Load() != 0 {
		t.Fatalf("proposal crossed confirmation gate: response=%v launches=%d", response, launches.Load())
	}
	saved := response["thread"].(scoutChatThreadRecord)
	card := saved.Messages[len(saved.Messages)-1]
	if card.Proposal == nil || card.Proposal.AgentID != hired.ID || card.ReplyTo == nil || card.ReplyTo.MessageID != root.ID {
		t.Fatalf("persisted targeted card=%+v", card)
	}

	accepted, err := fixture.app.resolveScoutChatProposal(context.Background(), fixture.user, table.ID, scoutChatProposalAction{Action: "accepted", MessageID: card.ID})
	if err != nil {
		t.Fatal(err)
	}
	if launches.Load() != 1 || !strings.Contains(launched.Query, root.Text) || launched.Artifact.Metadata["agentId"] != hired.ID || launched.Artifact.Metadata["agentName"] != hired.DisplayName || launched.Artifact.Metadata["delegatedBy"] != "" {
		t.Fatalf("targeted launch=%+v metadata=%v launches=%d", launched, launched.Artifact.Metadata, launches.Load())
	}
	answer := accepted["answer"].(scoutChatMessageRecord)
	if answer.AuthorName != hired.DisplayName || answer.ReplyTo == nil || answer.ReplyTo.MessageID != root.ID || answer.Thread == nil || answer.Thread.AgentID != hired.ID || answer.Thread.AgentName != hired.DisplayName || answer.Thread.DelegatedBy != "" {
		t.Fatalf("targeted attribution=%+v", answer)
	}
	fixture.app.updateScoutChatThreadRefs(launched.ID, "complete", launched.Artifact.ID)
	reloaded, _, err := fixture.app.scoutChatThreadByID(fixture.user.Email, table.ID)
	if err != nil {
		t.Fatal(err)
	}
	workCards := 0
	for _, message := range reloaded.Messages {
		if message.Thread == nil || message.Thread.ID != launched.ID {
			continue
		}
		workCards++
		if message.ID != answer.ID || message.ReplyTo == nil || message.ReplyTo.MessageID != root.ID || message.Thread.Status != "complete" {
			t.Fatalf("reloaded reply-local work card=%+v", message)
		}
	}
	if workCards != 1 {
		t.Fatalf("reply-local completion produced %d work cards, want exactly one", workCards)
	}
	if _, err := fixture.app.resolveScoutChatProposal(context.Background(), fixture.user, table.ID, scoutChatProposalAction{Action: "accepted", MessageID: card.ID}); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("duplicate accept err=%v", err)
	}
	if launches.Load() != 1 {
		t.Fatalf("duplicate accept launched %d workstreams, want exactly one", launches.Load())
	}
}

func TestTargetedAgentProposalRechecksEligibilityBeforeClaim(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	hired := hireResearchAgentForBridgeTest(t, fixture, "colton-research", strideProductAgentDirectThreadPrefix+"eligibility_recheck")
	table, err := fixture.app.ensureTable(fixture.user.Email)
	if err != nil {
		t.Fatal(err)
	}
	response, err := fixture.app.appendScoutChatThreadMessage(context.Background(), fixture.user, table.ID, "@Colton research the Disney and TikTok market implications", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	proposal := response["proposal"].(*scoutRouterProposal)
	saved := response["thread"].(scoutChatThreadRecord)
	cardID := saved.Messages[len(saved.Messages)-1].ID
	if proposal.AgentID != hired.ID {
		t.Fatalf("proposal target=%+v", proposal)
	}

	err = fixture.runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		_, err := ctx.Product.mutateAgent(hired.ID, hired.Revision, func(agent *STRIDEProductTeamAgent) error {
			agent.Status = "paused"
			agent.AccessRevoked = true
			return nil
		}, fixture.config.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	previousRunner := startAgentThreadAsync
	var launches atomic.Int64
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { launches.Add(1) }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	if _, err := fixture.app.resolveScoutChatProposal(context.Background(), fixture.user, table.ID, scoutChatProposalAction{Action: "accepted", MessageID: cardID}); err == nil || !strings.Contains(err.Error(), "no longer eligible") {
		t.Fatalf("eligibility recheck err=%v", err)
	}
	if launches.Load() != 0 {
		t.Fatalf("ineligible agent launched %d workstreams", launches.Load())
	}
	pending, err := fixture.app.pendingScoutChatProposal(table.ID, fixture.user.Email, cardID)
	if err != nil || pending.Status != "" {
		t.Fatalf("failed preclaim changed proposal: pending=%+v err=%v", pending, err)
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

func TestDirectCoworkerThreadReplyPersistsWithoutLaunchingWork(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	directThreadID := strideProductAgentDirectThreadPrefix + "colton_plain_reply_test"
	hireResearchAgentForBridgeTest(t, fixture, "colton-research", directThreadID)
	thread, _, err := fixture.app.ensureScoutChatThread(directThreadID, fixture.user.Email, fixture.user.Name, "Colton · agent", scoutChatVisibilityPrivate, nil)
	if err != nil {
		t.Fatal(err)
	}
	root := scoutChatMessageRecord{
		ID: "colton-reply-root", Kind: "message", Role: "user", Text: "Research the Country+Golf membership strategy.",
		CreatedAt: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), AuthorName: fixture.user.Name, AuthorEmail: fixture.user.Email,
	}
	if _, err := fixture.app.commitScoutChatThreadMessages(fixture.user.Email, thread.ID, root); err != nil {
		t.Fatal(err)
	}
	previousRunner := startAgentThreadAsync
	var launches atomic.Int64
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { launches.Add(1) }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	response, err := fixture.app.appendScoutChatThreadMessageWithReplyAndTool(
		context.Background(), fixture.user, thread.ID, "That framing works for me.", nil, "", root.ID, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if launches.Load() != 0 || response["providerCalls"] != 0 || response["routing"] != "thread_reply_only" {
		t.Fatalf("plain nested reply launched coworker work: launches=%d response=%v", launches.Load(), response)
	}
	if response["agentThread"] != nil || response["artifact"] != nil || response["answer"] != nil {
		t.Fatalf("plain nested reply invented work output: %v", response)
	}
	saved, ok := response["thread"].(scoutChatThreadRecord)
	if !ok || len(saved.Messages) != 2 {
		t.Fatalf("saved thread=%#v", response["thread"])
	}
	reply := saved.Messages[1]
	if reply.ReplyTo == nil || reply.ReplyTo.MessageID != root.ID || reply.ReplyTo.Text != root.Text || reply.Text != "That framing works for me." {
		t.Fatalf("plain nested reply=%+v", reply)
	}
}

func TestDirectResearchRequestNeedsMaterialTopicAndScope(t *testing.T) {
	for _, vague := range []string{
		"Hey Colton",
		"Research the best launch partner and deliver the decision brief.",
		"Please investigate this and recommend what to do.",
	} {
		if !directResearchRequestNeedsInput(vague, nil, nil) {
			t.Errorf("vague request %q launched without a material topic", vague)
		}
	}
	for _, ready := range []string{
		"Research the Country+Golf membership growth strategy using primary sources",
		"Compare Otter and Granola for a 50-person company",
	} {
		if directResearchRequestNeedsInput(ready, nil, nil) {
			t.Errorf("bounded request %q was incorrectly blocked", ready)
		}
	}
	if directResearchRequestNeedsInput("Research this", []scoutChatFileAttachment{{Name: "venture-brief.pdf"}}, nil) {
		t.Error("an attached source should satisfy the material-input gate")
	}
	if directResearchRequestNeedsInput("Research this", nil, []string{"chat-file:brief"}) {
		t.Error("an authorized source ref should satisfy the material-input gate")
	}
}

func TestDirectColtonFollowUpKeepsIdentityRelationshipLaneAndLearningLineage(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	directThreadID := strideProductAgentDirectThreadPrefix + "colton_followup_contract_test"
	hired := hireResearchAgentForBridgeTest(t, fixture, "colton-research", directThreadID)
	profile, ok := fixture.app.strideAgentDirectThreadContext(directThreadID)
	if !ok {
		t.Fatal("missing current Colton profile")
	}
	metadata := agentThreadGoalSpecForProfile(profile, "").metadata()
	for key, value := range map[string]string{
		"source":        "scout_thread",
		"threadId":      "agent-thread-research-colton-followup",
		"threadQuery":   "Research Country+Golf membership growth",
		"requestedBy":   fixture.user.Email,
		"createdBy":     fixture.user.Name,
		"originKind":    agentThreadOriginPrivateThread,
		"originId":      directThreadID,
		"status":        "complete",
		"threadStatus":  "complete",
		"goalStatus":    "verified",
		"threadVersion": "1",
		"completedAt":   time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano),
	} {
		metadata[key] = value
	}
	artifact, _, err := fixture.app.createOSArtifactWithMetadata("research", "Country+Golf membership growth", "# Research brief\n\nInitial result.", hired.DisplayName, metadata)
	if err != nil {
		t.Fatal(err)
	}
	fixture.app.apiKey = "test-openai-key"
	t.Setenv("ANTHROPIC_API_KEY", "")

	var captured openAITextRequest
	var followUpRunID string
	previousAsync := startAgentThreadFollowUpAsync
	startAgentThreadFollowUpAsync = func(runApp *kanbanBoardApp, run agentThreadFollowUpRun) {
		followUpRunID = run.runID
		runApp.runAgentThreadFollowUpWithResponder(run, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
			captured = request
			return completeResearchArtifactForTest() + "\n\nWhat changed in v2: I tightened the recommendation around AJ's decision.\n\nVerified follow-up.", nil
		})
	}
	t.Cleanup(func() { startAgentThreadFollowUpAsync = previousAsync })

	if _, err := fixture.app.launchAgentThreadFollowUp(artifact.ID, "tighten the recommendation for my decision", fixture.user.Name, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"delivering this work as Colton",
		"Speak in first person",
		"Authenticated requester:",
		"private one-to-one work surface",
	} {
		if !strings.Contains(captured.Instructions, want) {
			t.Fatalf("follow-up prompt missing %q:\n%s", want, captured.Instructions)
		}
	}
	if strings.Contains(captured.Instructions, "You are Scout") {
		t.Fatalf("follow-up prompt restored Scout as a second speaking identity:\n%s", captured.Instructions)
	}
	stored, ok := fixture.app.osArtifactByID(artifact.ID)
	if !ok || stored.Metadata["updatedBy"] != "Colton" || stored.Metadata["threadVersion"] != "2" || stored.Metadata["status"] != "complete" {
		t.Fatalf("stored follow-up=%+v", stored)
	}
	if followUpRunID == "" || stored.Metadata["latestThreadRun"] != followUpRunID {
		t.Fatalf("follow-up run lineage=%q metadata=%q", followUpRunID, stored.Metadata["latestThreadRun"])
	}
	runLogged := false
	for _, entry := range fixture.app.memory.entriesOfKind(meetingMemoryKindRunLog, 50) {
		if entry.Kind == "run_log" && entry.ID == "run-log-"+followUpRunID && entry.Metadata["artifactId"] == artifact.ID {
			runLogged = true
		}
	}
	if !runLogged {
		t.Fatalf("follow-up run %q has no durable run ledger", followUpRunID)
	}
	var current STRIDEProductTeamAgent
	err = fixture.runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		var found bool
		current, found = ctx.Product.agentRecord(hired.ID)
		if !found {
			return ErrSTRIDEProductUnknown
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Learning) != 1 {
		t.Fatalf("learning=%+v, want one pending follow-up candidate", current.Learning)
	}
	learning := current.Learning[0]
	if learning.Status != "pending" || learning.Origin != "completed_work" || learning.RunID != followUpRunID || learning.ArtifactID != artifact.ID || learning.SourceThreadID != directThreadID {
		t.Fatalf("follow-up learning lineage=%+v", learning)
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
		return completeResearchArtifactForTest(), nil
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
