package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newAcceptedPublicWorkFixture(t *testing.T) (*kanbanBoardApp, *userAccount, scoutChatThreadRecord, scoutChatMessageRecord, scoutChatSourceBinding) {
	t.Helper()
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-public-work-test"
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Like A Farmer", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	source := scoutChatMessageRecord{
		ID: "public-work-source", Kind: "message", Role: "user", Text: "Use Tyler's ask and build the real deck.",
		AuthorName: user.Name, AuthorEmail: user.Email, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	thread, err = app.commitScoutChatThreadMessages(user.Email, thread.ID, source)
	if err != nil {
		t.Fatal(err)
	}
	_, binding, err := scoutChatSourceWindow(thread, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	return app, user, thread, source, binding
}

func launchLostAckPublicResearchForFailureTest(t *testing.T, proposalID string) (*kanbanBoardApp, *userAccount, scoutChatThreadRecord, scoutChatMessageRecord, scoutAgentThread, *int) {
	t.Helper()
	app, user, thread, source, binding := newAcceptedPublicWorkFixture(t)
	proposal := scoutRouterProposal{
		Kind: scoutRouterProposalKindWorkstream, IntentOutcome: string(conversationIntentApprovalRequired), EffectClass: "expanded_audience",
		Mode: "research", Objective: "Research the launch plan with exact cited evidence", Query: source.Text,
		Lane: approvalLaneStandard, WeightLabel: scoutProposalWeightQuickPass, Summary: "Research prepared", Status: "accepted",
	}
	var err error
	thread, err = app.commitScoutChatThreadMessages(user.Email, thread.ID, scoutChatMessageRecord{
		ID: proposalID, Kind: scoutChatMessageKindProposal, Role: "scout", Proposal: &proposal,
		CausedByMessageID: source.ID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	previousStarter := startAgentThreadAsync
	previousProbe := publicConversationWorkAfterProviderAcceptedProbe
	t.Cleanup(func() {
		startAgentThreadAsync = previousStarter
		publicConversationWorkAfterProviderAcceptedProbe = previousProbe
	})
	startAgentThreadAsync = func(runApp *kanbanBoardApp, work scoutAgentThread) { runApp.runAgentThread(work) }
	providerCalls := new(int)
	swapOpenAITextResponder(t, func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		*providerCalls++
		return completeResearchArtifactForTest(), nil
	})
	publicConversationWorkAfterProviderAcceptedProbe = func(_ scoutAgentThread, _ agentThreadWorkerResult) error {
		return errors.New("simulated lost local acknowledgement")
	}
	response, err := app.startAcceptedPublicScoutWork(context.Background(), user, thread, proposalID, proposal, nil, binding)
	if err != nil {
		t.Fatal(err)
	}
	publicConversationWorkAfterProviderAcceptedProbe = nil
	return app, user, thread, source, response["agentThread"].(scoutAgentThread), providerCalls
}

func assertPermanentPublicWorkFailure(t *testing.T, app *kanbanBoardApp, user *userAccount, thread scoutChatThreadRecord, work scoutAgentThread, providerCalls *int) {
	t.Helper()
	artifact, ok := app.osArtifactByID(work.Artifact.ID)
	if !ok || artifact.Metadata[publicConversationWorkActivationState] != publicConversationWorkNeedsAttention || artifact.Metadata["threadStatus"] != "error" ||
		artifact.Metadata[publicConversationProviderRequestKey] != "" || artifact.Metadata[publicConversationProviderRequestHash] != "" {
		t.Fatalf("permanent failure artifact is not terminal/clean: %+v", artifact)
	}
	digest := strings.TrimPrefix(work.Artifact.Metadata[publicConversationProviderRequestKey], publicConversationProviderBlobPrefix)
	if _, err := os.Stat(filepath.Join(filepath.Dir(meetingMemoryPath()), "private-operation-blobs", digest+".json")); !os.IsNotExist(err) {
		t.Fatalf("permanent failure retained private provider snapshot: %v", err)
	}
	reloaded, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	visibleTerminal := false
	for _, message := range reloaded.Messages {
		if message.Thread != nil && message.Thread.ID == work.ID {
			visibleTerminal = message.ReplyTo == nil && message.Thread.Status == "error" && strings.Contains(strings.ToLower(message.Text), "needs attention")
		}
	}
	if !visibleTerminal {
		t.Fatalf("permanent failure root card remained non-terminal: %+v", reloaded.Messages)
	}
	before := *providerCalls
	app.reconcilePublicConversationWorkAtBoot()
	if *providerCalls != before {
		t.Fatalf("terminal permanent failure retried provider: before=%d after=%d", before, *providerCalls)
	}
}

func TestAcceptedPublicWorkstreamPersistsRootCardBeforeActivation(t *testing.T) {
	app, user, thread, source, binding := newAcceptedPublicWorkFixture(t)
	previousStarter := startAgentThreadAsync
	t.Cleanup(func() { startAgentThreadAsync = previousStarter })
	activated := 0
	startAgentThreadAsync = func(runApp *kanbanBoardApp, work scoutAgentThread) {
		activated++
		current, _, err := runApp.scoutChatThreadByID(user.Email, thread.ID)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, message := range current.Messages {
			if message.Thread != nil && message.Thread.ID == work.ID {
				found = true
				if message.ReplyTo != nil || message.CausedByMessageID != "proposal-public-workstream" {
					t.Fatalf("activation saw non-root or unbound card: %+v", message)
				}
			}
		}
		if !found {
			t.Fatal("provider activation happened before the root work card was durable")
		}
	}
	proposal := scoutRouterProposal{
		Kind: scoutRouterProposalKindWorkstream, IntentOutcome: string(conversationIntentApprovalRequired), EffectClass: "expanded_audience",
		Mode: "research", Objective: "Research Tyler's ask and return a decision-ready brief", Query: source.Text,
		Lane: approvalLaneStandard, WeightLabel: scoutProposalWeightQuickPass, Summary: "Research prepared", Status: "accepted",
	}
	thread, err := app.commitScoutChatThreadMessages(user.Email, thread.ID, scoutChatMessageRecord{
		ID: "proposal-public-workstream", Kind: scoutChatMessageKindProposal, Role: "scout", Proposal: &proposal,
		CausedByMessageID: source.ID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	reply := &scoutChatReplyRef{MessageID: source.ID, Text: source.Text, AuthorName: user.Name, AuthorEmail: user.Email}
	response, err := app.startAcceptedPublicScoutWork(context.Background(), user, thread, "proposal-public-workstream", proposal, reply, binding)
	if err != nil {
		t.Fatal(err)
	}
	if activated != 1 {
		t.Fatalf("activations=%d, want 1", activated)
	}
	answer := response["answer"].(scoutChatMessageRecord)
	work := response["agentThread"].(scoutAgentThread)
	if answer.ReplyTo != nil || answer.Thread == nil || answer.Thread.ID != work.ID || answer.CausedByMessageID != "proposal-public-workstream" {
		t.Fatalf("root answer=%+v", answer)
	}
	metadata := work.Artifact.Metadata
	if metadata["originId"] != thread.ID || metadata["sourceMessageId"] != source.ID || metadata[publicConversationWorkActivationState] != "started" {
		t.Fatalf("work metadata=%v", metadata)
	}
	for _, terminal := range []struct {
		status string
		text   string
	}{
		{status: "complete", text: "Research delivered"},
		{status: "error", text: "Research needs attention"},
	} {
		updated, _, updateErr := app.updateOSArtifactWithMetadata(work.Artifact.ID, "", work.Artifact.Text, "", map[string]string{
			"status": terminal.status, "threadStatus": terminal.status,
		})
		if updateErr != nil {
			t.Fatal(updateErr)
		}
		app.updateScoutChatThreadRefs(work.ID, terminal.status, updated.ID)
		current, _, loadErr := app.scoutChatThreadByID(user.Email, thread.ID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		matches := 0
		for _, message := range current.Messages {
			if message.ID != answer.ID {
				continue
			}
			matches++
			if message.ReplyTo != nil || message.Thread == nil || message.Thread.Status != terminal.status || message.Text != terminal.text {
				t.Fatalf("%s root status card=%+v", terminal.status, message)
			}
		}
		if matches != 1 {
			t.Fatalf("%s root cards=%d, want 1", terminal.status, matches)
		}
	}
}

func TestAcceptedPublicWorkstreamRecoversActivationClaimAfterRestartExactlyOnce(t *testing.T) {
	app, user, thread, source, binding := newAcceptedPublicWorkFixture(t)
	proposal := scoutRouterProposal{
		Kind: scoutRouterProposalKindWorkstream, IntentOutcome: string(conversationIntentApprovalRequired), EffectClass: "expanded_audience",
		Mode: "research", Objective: "Research the exact launch request", Query: source.Text,
		Lane: approvalLaneStandard, WeightLabel: scoutProposalWeightQuickPass, Summary: "Research prepared", Status: "accepted",
	}
	var err error
	thread, err = app.commitScoutChatThreadMessages(user.Email, thread.ID, scoutChatMessageRecord{
		ID: "proposal-public-restart", Kind: scoutChatMessageKindProposal, Role: "scout", Proposal: &proposal,
		CausedByMessageID: source.ID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}

	previousStarter := startAgentThreadAsync
	previousProbe := publicConversationWorkAfterActivationPersistProbe
	t.Cleanup(func() {
		startAgentThreadAsync = previousStarter
		publicConversationWorkAfterActivationPersistProbe = previousProbe
	})
	starts := 0
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) { starts++ }
	publicConversationWorkAfterActivationPersistProbe = func(scoutAgentThread) error {
		return errors.New("simulated process loss after activation claim")
	}
	if _, err := app.startAcceptedPublicScoutWork(context.Background(), user, thread, "proposal-public-restart", proposal, nil, binding); err == nil || !strings.Contains(err.Error(), "simulated process loss") {
		t.Fatalf("crash-boundary error=%v", err)
	}
	if starts != 0 {
		t.Fatalf("provider starts before simulated crash=%d, want 0", starts)
	}
	operation, err := conversationApprovedWorkOperation(thread.ID, user.Email, "proposal-public-restart", proposal)
	if err != nil {
		t.Fatal(err)
	}
	reserved, found, err := app.conversationWorkForOperation(user.Email, thread.ID, operation)
	if err != nil || !found || reserved.Artifact.Metadata[publicConversationWorkActivationState] != publicConversationWorkStarted ||
		reserved.Artifact.Metadata[publicConversationWorkActivationOwner] == "" {
		t.Fatalf("durable crash claim=%+v found=%t err=%v", reserved.Artifact.Metadata, found, err)
	}
	current, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	rootCards := 0
	for _, message := range current.Messages {
		if message.Thread != nil && message.Thread.ID == reserved.ID {
			rootCards++
			if message.ReplyTo != nil || message.CausedByMessageID != "proposal-public-restart" {
				t.Fatalf("crash reservation card=%+v", message)
			}
		}
	}
	if rootCards != 1 {
		t.Fatalf("crash reservation root cards=%d, want 1", rootCards)
	}

	publicConversationWorkAfterActivationPersistProbe = nil
	restarted := newKanbanBoardApp()
	restarted.apiKey = app.apiKey
	restarted.reconcilePublicConversationWorkAtBoot()
	if starts != 1 {
		t.Fatalf("restart provider starts=%d, want 1", starts)
	}
	restarted.reconcilePublicConversationWorkAtBoot()
	if starts != 1 {
		t.Fatalf("same-boot reconciliation duplicated provider starts=%d", starts)
	}
	reloaded, _, err := restarted.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, restartedBinding, err := scoutChatSourceWindow(reloaded, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.startAcceptedPublicScoutWork(context.Background(), user, reloaded, "proposal-public-restart", proposal, nil, restartedBinding); err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Fatalf("accepted retry duplicated provider starts=%d", starts)
	}
	artifacts := 0
	for _, entry := range restarted.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0) {
		if entry.Metadata["operationId"] == operation.ID && entry.Metadata["operationBodyDigest"] == operation.BodyDigest {
			artifacts++
		}
	}
	if artifacts != 1 {
		t.Fatalf("restart artifacts=%d, want one deterministic operation", artifacts)
	}
}

func TestAcceptedPublicWorkstreamReusesProviderOperationAfterLostLocalAck(t *testing.T) {
	app, user, thread, source, binding := newAcceptedPublicWorkFixture(t)
	const privateSourceSentinel = "SOURCE-ONLY-PROVIDER-SNAPSHOT-7f4c1e"
	source = scoutChatMessageRecord{ID: "public-work-provider-snapshot-source", Kind: "message", Role: "user", AuthorName: user.Name,
		AuthorEmail: user.Email, Text: "Authorized source detail: " + privateSourceSentinel, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	var err error
	thread, err = app.commitScoutChatThreadMessages(user.Email, thread.ID, source)
	if err != nil {
		t.Fatal(err)
	}
	_, binding, err = scoutChatSourceWindow(thread, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	proposal := scoutRouterProposal{
		Kind: scoutRouterProposalKindWorkstream, IntentOutcome: string(conversationIntentApprovalRequired), EffectClass: "expanded_audience",
		Mode: "research", Objective: "Research the launch plan with exact cited evidence", Query: source.Text,
		Lane: approvalLaneStandard, WeightLabel: scoutProposalWeightQuickPass, Summary: "Research prepared", Status: "accepted",
	}
	thread, err = app.commitScoutChatThreadMessages(user.Email, thread.ID, scoutChatMessageRecord{
		ID: "proposal-public-provider-ack", Kind: scoutChatMessageKindProposal, Role: "scout", Proposal: &proposal,
		CausedByMessageID: source.ID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}

	previousStarter := startAgentThreadAsync
	previousProbe := publicConversationWorkAfterProviderAcceptedProbe
	t.Cleanup(func() {
		startAgentThreadAsync = previousStarter
		publicConversationWorkAfterProviderAcceptedProbe = previousProbe
	})
	startAgentThreadAsync = func(runApp *kanbanBoardApp, work scoutAgentThread) { runApp.runAgentThread(work) }
	providerCalls, providerEffects := 0, 0
	accepted := map[string]string{}
	var providerRequests [][]byte
	providerBody := completeResearchArtifactForTest()
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		providerCalls++
		wireRequest, marshalErr := json.Marshal(durableOpenAIRequest(request))
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		providerRequests = append(providerRequests, wireRequest)
		if strings.TrimSpace(request.IdempotencyKey) == "" {
			t.Fatal("public provider request has no deterministic idempotency key")
		}
		if output, ok := accepted[request.IdempotencyKey]; ok {
			return output, nil
		}
		providerEffects++
		output := providerBody
		accepted[request.IdempotencyKey] = output
		return output, nil
	})
	lostAck := true
	publicConversationWorkAfterProviderAcceptedProbe = func(_ scoutAgentThread, _ agentThreadWorkerResult) error {
		if lostAck {
			lostAck = false
			return errors.New("simulated lost local provider acknowledgement")
		}
		return nil
	}
	response, err := app.startAcceptedPublicScoutWork(context.Background(), user, thread, "proposal-public-provider-ack", proposal, nil, binding)
	if err != nil {
		t.Fatal(err)
	}
	work := response["agentThread"].(scoutAgentThread)
	reservedMetadata, _ := json.Marshal(work.Artifact.Metadata)
	if strings.Contains(string(reservedMetadata), privateSourceSentinel) || validBlobRef(work.Artifact.Metadata[publicConversationProviderRequestKey]) {
		t.Fatalf("private provider request leaked into artifact metadata or a public blob ref: %s", reservedMetadata)
	}
	privateDir := filepath.Join(filepath.Dir(meetingMemoryPath()), "private-operation-blobs")
	privateRef := strings.TrimPrefix(work.Artifact.Metadata[publicConversationProviderRequestKey], publicConversationProviderBlobPrefix)
	if info, err := os.Stat(privateDir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("private provider request directory permissions=%v err=%v, want 0700", info, err)
	}
	if info, err := os.Stat(filepath.Join(privateDir, privateRef+".json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private provider request blob permissions=%v err=%v, want 0600", info, err)
	}
	frozenRaw, err := loadPrivatePublicConversationProviderRequest(work.Artifact.Metadata[publicConversationProviderRequestKey], work.Artifact.Metadata[publicConversationProviderRequestHash])
	if err != nil {
		t.Fatal(err)
	}
	var frozen durablePublicConversationProviderRequest
	if err := json.Unmarshal(frozenRaw, &frozen); err != nil {
		t.Fatal(err)
	}
	for _, authorityEntry := range frozen.Authority.Entries {
		if authorityEntry.ID == work.Artifact.ID {
			t.Fatal("provider authority manifest included the mutable output artifact itself")
		}
	}
	if len(providerRequests) != 1 || !strings.Contains(string(providerRequests[0]), privateSourceSentinel) {
		t.Fatalf("provider request did not include the authorized private source sentinel: %s", providerRequests[0])
	}
	beforeRestart, ok := app.osArtifactByID(work.Artifact.ID)
	if !ok || beforeRestart.Metadata[publicConversationWorkActivationState] != publicConversationWorkStarted ||
		oneOf(beforeRestart.Metadata["threadStatus"], "complete", "error") {
		t.Fatalf("lost-ack artifact committed prematurely: %+v", beforeRestart.Metadata)
	}
	if providerCalls != 1 || providerEffects != 1 {
		t.Fatalf("pre-restart provider calls=%d effects=%d, want 1/1", providerCalls, providerEffects)
	}
	app.openAIToolActivationMu.Lock()
	_, stillActive := app.openAIToolActiveRuns[work.Artifact.ID]
	app.openAIToolActivationMu.Unlock()
	if stillActive {
		t.Fatal("lost-ack worker leaked its in-process active claim")
	}

	publicConversationWorkAfterProviderAcceptedProbe = nil
	restarted := newKanbanBoardApp()
	restarted.apiKey = app.apiKey
	restarted.reconcilePublicConversationWorkAtBoot()
	if providerCalls != 2 || providerEffects != 1 {
		t.Fatalf("restart provider calls=%d committed effects=%d, want retry 2/1", providerCalls, providerEffects)
	}
	if len(providerRequests) != 2 || string(providerRequests[0]) != string(providerRequests[1]) {
		t.Fatalf("restart changed the provider request under one idempotency key:\nfirst=%s\nsecond=%s", providerRequests[0], providerRequests[1])
	}
	completed, ok := restarted.osArtifactByID(work.Artifact.ID)
	if !ok || completed.Metadata[publicConversationWorkActivationState] != publicConversationWorkComplete || completed.Metadata["threadStatus"] != "complete" ||
		!strings.Contains(completed.Text, "# Bonfire comparable-company map and positioning") ||
		!strings.Contains(completed.Text, "stride-web-citation-receipt:v1") {
		t.Fatalf("recovered terminal artifact ok=%t state=%q status=%q bodyLen=%d", ok,
			completed.Metadata[publicConversationWorkActivationState], completed.Metadata["threadStatus"], len(completed.Text))
	}
	if completed.Metadata[publicConversationProviderRequestKey] != "" || completed.Metadata[publicConversationProviderRequestHash] != "" {
		t.Fatalf("terminal artifact retained private provider snapshot metadata: %v", completed.Metadata)
	}
	if _, err := os.Stat(filepath.Join(privateDir, privateRef+".json")); !os.IsNotExist(err) {
		t.Fatalf("terminal provider snapshot was not cleaned up: %v", err)
	}
	restarted.openAIToolActivationMu.Lock()
	_, stillActive = restarted.openAIToolActiveRuns[work.Artifact.ID]
	restarted.openAIToolActivationMu.Unlock()
	if stillActive {
		t.Fatal("terminal worker leaked its in-process active claim")
	}
	restarted.reconcilePublicConversationWorkAtBoot()
	if providerCalls != 2 || providerEffects != 1 {
		t.Fatalf("terminal reconciliation duplicated provider calls=%d effects=%d", providerCalls, providerEffects)
	}
	reloaded, _, err := restarted.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	rootCards := 0
	for _, message := range reloaded.Messages {
		if message.Thread != nil && message.Thread.ID == work.ID {
			rootCards++
			if message.ReplyTo != nil || message.Thread.Status != "complete" || !strings.HasPrefix(message.Text, "Research delivered") {
				t.Fatalf("recovered terminal root card=%+v", message)
			}
		}
	}
	if rootCards != 1 {
		t.Fatalf("recovered terminal root cards=%d, want exactly one", rootCards)
	}
}

func TestAcceptedPublicWorkstreamRejectsRetryAfterEmbeddedAuthorityRevoked(t *testing.T) {
	app, user, thread, source, binding := newAcceptedPublicWorkFixture(t)
	proposal := scoutRouterProposal{
		Kind: scoutRouterProposalKindWorkstream, IntentOutcome: string(conversationIntentApprovalRequired), EffectClass: "expanded_audience",
		Mode: "research", Objective: "Research the launch plan with exact cited evidence", Query: source.Text,
		Lane: approvalLaneStandard, WeightLabel: scoutProposalWeightQuickPass, Summary: "Research prepared", Status: "accepted",
	}
	var err error
	thread, err = app.commitScoutChatThreadMessages(user.Email, thread.ID, scoutChatMessageRecord{
		ID: "proposal-public-provider-revoke", Kind: scoutChatMessageKindProposal, Role: "scout", Proposal: &proposal,
		CausedByMessageID: source.ID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	previousStarter := startAgentThreadAsync
	previousProbe := publicConversationWorkAfterProviderAcceptedProbe
	t.Cleanup(func() {
		startAgentThreadAsync = previousStarter
		publicConversationWorkAfterProviderAcceptedProbe = previousProbe
	})
	startAgentThreadAsync = func(runApp *kanbanBoardApp, work scoutAgentThread) { runApp.runAgentThread(work) }
	providerCalls := 0
	swapOpenAITextResponder(t, func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		providerCalls++
		return completeResearchArtifactForTest(), nil
	})
	publicConversationWorkAfterProviderAcceptedProbe = func(_ scoutAgentThread, _ agentThreadWorkerResult) error {
		return errors.New("simulated lost local acknowledgement")
	}
	response, err := app.startAcceptedPublicScoutWork(context.Background(), user, thread, "proposal-public-provider-revoke", proposal, nil, binding)
	if err != nil {
		t.Fatal(err)
	}
	work := response["agentThread"].(scoutAgentThread)
	raw, err := loadPrivatePublicConversationProviderRequest(work.Artifact.Metadata[publicConversationProviderRequestKey], work.Artifact.Metadata[publicConversationProviderRequestHash])
	if err != nil {
		t.Fatal(err)
	}
	var snapshot durablePublicConversationProviderRequest
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	revokedID := ""
	for _, entry := range snapshot.Authority.Entries {
		if entry.Kind == meetingMemoryKindConversationContinuity {
			revokedID = entry.ID
			break
		}
	}
	if revokedID == "" {
		t.Fatal("fixture did not embed an ambient continuity source")
	}
	app.memory.mu.Lock()
	for index := range app.memory.entries {
		if app.memory.entries[index].ID == revokedID {
			metadata := cloneCodexThreadMetadata(app.memory.entries[index].Metadata)
			metadata["visibility"] = "private"
			metadata["ownerEmail"] = "revoked@example.com"
			metadata["aclVersion"] = "999"
			app.memory.entries[index].Metadata = metadata
		}
	}
	app.memory.mu.Unlock()
	publicConversationWorkAfterProviderAcceptedProbe = nil
	current, _ := app.osArtifactByID(work.Artifact.ID)
	work.Artifact = current
	_, retryErr := app.activateAcceptedPublicConversationWork(work, agentThreadGoalSpec{RequestedBy: user.Email}, user.Name)
	if retryErr == nil || !strings.Contains(retryErr.Error(), "authority manifest changed") {
		t.Fatalf("revoked embedded authority retry err=%v", retryErr)
	}
	if strings.Contains(retryErr.Error(), revokedID) || strings.Contains(retryErr.Error(), "revoked@example.com") {
		t.Fatalf("authority failure leaked private manifest detail: %v", retryErr)
	}
	if providerCalls != 1 {
		t.Fatalf("revoked retry reached provider: calls=%d, want initial call only", providerCalls)
	}
	failedArtifact, _ := app.osArtifactByID(work.Artifact.ID)
	if failedArtifact.Metadata[publicConversationWorkActivationState] != publicConversationWorkNeedsAttention || failedArtifact.Metadata["threadStatus"] != "error" ||
		failedArtifact.Metadata[publicConversationProviderRequestKey] != "" || failedArtifact.Metadata[publicConversationProviderRequestHash] != "" {
		t.Fatalf("revoked retry did not become terminal needs-attention: %v", failedArtifact.Metadata)
	}
	privateDigest := strings.TrimPrefix(work.Artifact.Metadata[publicConversationProviderRequestKey], publicConversationProviderBlobPrefix)
	if _, err := os.Stat(filepath.Join(filepath.Dir(meetingMemoryPath()), "private-operation-blobs", privateDigest+".json")); !os.IsNotExist(err) {
		t.Fatalf("revoked retry retained its private snapshot: %v", err)
	}
	failedMetadata, _ := json.Marshal(failedArtifact.Metadata)
	if strings.Contains(string(failedMetadata), revokedID) || strings.Contains(string(failedMetadata), "revoked@example.com") {
		t.Fatalf("authority failure persisted private manifest detail: %s", failedMetadata)
	}
	reloaded, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	visibleTerminal := false
	for _, message := range reloaded.Messages {
		if message.Thread != nil && message.Thread.ID == work.ID {
			visibleTerminal = message.ReplyTo == nil && message.Thread.Status == "error" && strings.Contains(strings.ToLower(message.Text), "needs attention")
		}
	}
	if !visibleTerminal {
		t.Fatalf("revoked retry root card remained non-terminal: %+v", reloaded.Messages)
	}
	app.reconcilePublicConversationWorkAtBoot()
	if providerCalls != 1 {
		t.Fatalf("terminal revoked work retried endlessly: calls=%d", providerCalls)
	}
}

func TestAcceptedPublicWorkstreamDeletedSourceBecomesVisibleTerminalFailure(t *testing.T) {
	app, user, thread, source, work, providerCalls := launchLostAckPublicResearchForFailureTest(t, "proposal-public-deleted-source")
	currentThread, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	kept := currentThread.Messages[:0]
	for _, message := range currentThread.Messages {
		if message.ID != source.ID {
			kept = append(kept, message)
		}
	}
	currentThread.Messages = kept
	if err := app.saveScoutChatThread(currentThread); err != nil {
		t.Fatal(err)
	}
	current, _ := app.osArtifactByID(work.Artifact.ID)
	work.Artifact = current
	if _, err := app.activateAcceptedPublicConversationWork(work, agentThreadGoalSpec{RequestedBy: user.Email}, user.Name); err == nil {
		t.Fatal("deleted source retry was accepted")
	}
	if *providerCalls != 1 {
		t.Fatalf("deleted source retry reached provider: calls=%d", *providerCalls)
	}
	assertPermanentPublicWorkFailure(t, app, user, thread, work, providerCalls)
}

func TestAcceptedPublicWorkstreamCorruptSnapshotRestartBecomesVisibleTerminalFailure(t *testing.T) {
	app, user, thread, _, work, providerCalls := launchLostAckPublicResearchForFailureTest(t, "proposal-public-corrupt-snapshot")
	_ = app
	digest := strings.TrimPrefix(work.Artifact.Metadata[publicConversationProviderRequestKey], publicConversationProviderBlobPrefix)
	path := filepath.Join(filepath.Dir(meetingMemoryPath()), "private-operation-blobs", digest+".json")
	if err := os.WriteFile(path, []byte(`{"corrupt":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	restarted := newKanbanBoardApp()
	restarted.apiKey = "openai-public-work-test"
	restarted.reconcilePublicConversationWorkAtBoot()
	if *providerCalls != 1 {
		t.Fatalf("corrupt snapshot restart reached provider: calls=%d", *providerCalls)
	}
	assertPermanentPublicWorkFailure(t, restarted, user, thread, work, providerCalls)
}

func TestPrivatePublicWorkProviderRequestStoreRejectsUnsafeFiles(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	dir := filepath.Join(filepath.Dir(meetingMemoryPath()), "private-operation-blobs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ref, digest, err := storePrivatePublicConversationProviderRequest([]byte(`{"safe":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("store directory mode=%v err=%v, want 0700", info, err)
	}
	path := filepath.Join(dir, digest+".json")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPrivatePublicConversationProviderRequest(ref, digest); err == nil {
		t.Fatal("widened 0644 private snapshot was accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []struct{ ref, digest string }{
		{publicConversationProviderBlobPrefix + strings.ToUpper(digest), strings.ToUpper(digest)},
		{publicConversationProviderBlobPrefix + "../" + digest, digest},
		{publicConversationProviderBlobPrefix + strings.Repeat("g", 64), strings.Repeat("g", 64)},
		{publicConversationProviderBlobPrefix + digest, strings.Repeat("0", 64)},
	} {
		if _, err := loadPrivatePublicConversationProviderRequest(invalid.ref, invalid.digest); err == nil {
			t.Fatalf("unsafe private snapshot binding was accepted: %+v", invalid)
		}
	}
	symlinkRaw := []byte(`{"symlink":true}`)
	symlinkDigest := sha256Hex(symlinkRaw)
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, symlinkRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, symlinkDigest+".json")); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPrivatePublicConversationProviderRequest(publicConversationProviderBlobPrefix+symlinkDigest, symlinkDigest); err == nil {
		t.Fatal("symlink private snapshot was accepted")
	}
	oversized := make([]byte, publicConversationProviderBlobMax+1)
	oversizedDigest := sha256Hex(oversized)
	if err := os.WriteFile(filepath.Join(dir, oversizedDigest+".json"), oversized, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPrivatePublicConversationProviderRequest(publicConversationProviderBlobPrefix+oversizedDigest, oversizedDigest); err == nil {
		t.Fatal("oversized private snapshot was accepted")
	}
	if _, _, err := storePrivatePublicConversationProviderRequest(oversized); err == nil {
		t.Fatal("oversized private snapshot was stored")
	}
	if privateProviderRequestRetentionAllowed(publicConversationProviderStoreMax-1, 2) ||
		!privateProviderRequestRetentionAllowed(publicConversationProviderStoreMax-publicConversationProviderBlobMax, publicConversationProviderBlobMax) {
		t.Fatal("aggregate private snapshot retention cap boundary is incorrect")
	}
	_ = app
}

func TestPrivatePublicWorkProviderRequestGCCleansTerminalCommitOrphan(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	ref, digest, err := storePrivatePublicConversationProviderRequest([]byte(`{"terminal":"committed-before-cleanup"}`))
	if err != nil {
		t.Fatal(err)
	}
	artifact, _, err := app.createOSArtifactWithMetadata("research", "terminal orphan", "done", "Scout", map[string]string{
		publicConversationWorkActivationState: publicConversationWorkComplete,
		publicConversationProviderRequestKey:  ref,
		publicConversationProviderRequestHash: digest,
		"status":                              "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = artifact
	path := filepath.Join(filepath.Dir(meetingMemoryPath()), "private-operation-blobs", digest+".json")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if err := app.gcPrivatePublicConversationProviderRequests(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("terminal commit orphan survived boot GC: %v", err)
	}
}

func TestPrivatePublicWorkProviderRequestRejectsSymlinkStoreDirectory(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	_ = app
	dir := filepath.Join(filepath.Dir(meetingMemoryPath()), "private-operation-blobs")
	target := t.TempDir()
	if err := os.Symlink(target, dir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := storePrivatePublicConversationProviderRequest([]byte(`{"unsafe":"directory-symlink"}`)); err == nil {
		t.Fatal("symlink private snapshot directory was accepted")
	}
}

func TestAcceptedPublicCodexWorkstreamReplaysTerminalJobAfterLostCallback(t *testing.T) {
	enableCodexExecutionForTest(t)
	t.Setenv("BONFIRE_AGENT_THREAD_WORKER", "codex_exec")
	t.Setenv("BONFIRE_CODEX_RUNNER_MODE", "sidecar")
	queueDir := t.TempDir()
	t.Setenv("BONFIRE_CODEX_QUEUE_PATH", queueDir)
	app, user, thread, source, binding := newAcceptedPublicWorkFixture(t)
	proposal := scoutRouterProposal{
		Kind: scoutRouterProposalKindWorkstream, IntentOutcome: string(conversationIntentApprovalRequired), EffectClass: "expanded_audience",
		Mode: "workflow", Objective: "Build the approved launch workflow artifact", Query: source.Text,
		Lane: approvalLaneStandard, WeightLabel: scoutProposalWeightQuickPass, Summary: "Workflow prepared", Status: "accepted",
	}
	var err error
	thread, err = app.commitScoutChatThreadMessages(user.Email, thread.ID, scoutChatMessageRecord{
		ID: "proposal-public-codex-lost-callback", Kind: scoutChatMessageKindProposal, Role: "scout", Proposal: &proposal,
		CausedByMessageID: source.ID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	previousStarter := startAgentThreadAsync
	t.Cleanup(func() { startAgentThreadAsync = previousStarter })
	startAgentThreadAsync = func(runApp *kanbanBoardApp, work scoutAgentThread) { runApp.runAgentThread(work) }
	response, err := app.startAcceptedPublicScoutWork(context.Background(), user, thread, "proposal-public-codex-lost-callback", proposal, nil, binding)
	if err != nil {
		t.Fatal(err)
	}
	work := response["agentThread"].(scoutAgentThread)
	queuedArtifact, ok := app.osArtifactByID(work.Artifact.ID)
	if !ok || queuedArtifact.Metadata["threadStatus"] != codexJobStatusQueued || queuedArtifact.Metadata["runnerJobId"] == "" {
		t.Fatalf("initial Codex artifact was not durably queued: %+v", queuedArtifact.Metadata)
	}
	store := newCodexRunnerJobStore(queueDir)
	claimed, err := store.claimNext("lost-callback-runner")
	if err != nil || claimed == nil {
		t.Fatalf("claim Codex job: job=%+v err=%v", claimed, err)
	}
	const terminalBody = "# Approved launch workflow\n\nThe durable workflow is complete."
	claimed.Status = codexJobStatusComplete
	claimed.CompletedAt = time.Now().UTC()
	claimed.ResultText = terminalBody
	claimed.RunnerEvidence = "lost callback fixture"
	claimed.Metadata = mergeStringMaps(claimed.Metadata, map[string]string{
		"status": codexJobStatusComplete, "threadStatus": codexJobStatusComplete, "goalStatus": "verified",
		"progressPercent": "100", "reviewGate": "passed", "completedAt": claimed.CompletedAt.Format(time.RFC3339Nano),
	})
	if err := store.update(*claimed); err != nil {
		t.Fatalf("persist terminal Codex result before lost callback: %v", err)
	}

	restarted := newKanbanBoardApp()
	restarted.reconcilePublicConversationWorkAtBoot()
	completed, ok := restarted.osArtifactByID(work.Artifact.ID)
	if !ok || completed.Metadata["threadStatus"] != codexJobStatusComplete ||
		completed.Metadata[publicConversationWorkActivationState] != publicConversationWorkComplete || strings.TrimSpace(completed.Text) != terminalBody {
		t.Fatalf("terminal Codex replay did not finalize the artifact: %+v", completed)
	}
	terminalJob, err := store.read(filepath.Base(store.jobPath(claimed.ID)))
	if err != nil || terminalJob.Status != codexJobStatusComplete || terminalJob.Attempts != 1 || terminalJob.RunnerID != "lost-callback-runner" {
		t.Fatalf("terminal replay reset or replaced the queue job: job=%+v err=%v", terminalJob, err)
	}
	restarted.reconcilePublicConversationWorkAtBoot()
	terminalAgain, err := store.read(filepath.Base(store.jobPath(claimed.ID)))
	if err != nil || terminalAgain.Attempts != 1 {
		t.Fatalf("second reconciliation re-enqueued terminal job: job=%+v err=%v", terminalAgain, err)
	}
	reloaded, _, err := restarted.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	rootCards := 0
	for _, message := range reloaded.Messages {
		if message.Thread != nil && message.Thread.ID == work.ID {
			rootCards++
			if message.ReplyTo != nil || message.Thread.Status != codexJobStatusComplete {
				t.Fatalf("terminal Codex replay card=%+v", message)
			}
		}
	}
	if rootCards != 1 {
		t.Fatalf("terminal Codex replay root cards=%d, want one", rootCards)
	}
}

func TestAcceptedPublicPackagingStudioHasVisibleReservationBeforeGoalStarts(t *testing.T) {
	app, user, thread, source, binding := newAcceptedPublicWorkFixture(t)
	previousStarter := startGoalThreadAsync
	t.Cleanup(func() { startGoalThreadAsync = previousStarter })
	starts := 0
	startGoalThreadAsync = func(runApp *kanbanBoardApp, _ string) {
		starts++
		current, _, err := runApp.scoutChatThreadByID(user.Email, thread.ID)
		if err != nil {
			t.Fatal(err)
		}
		visible := false
		for _, message := range current.Messages {
			if message.CausedByMessageID == "proposal-public-deck" && message.Kind == "thread" {
				visible = message.ReplyTo == nil && strings.Contains(message.Text, "queued")
			}
		}
		if !visible {
			t.Fatal("goal provider started before a visible root reservation")
		}
	}
	proposal := scoutRouterProposalForToolID(packagingStudioProcessID, "Build a first-class deck from Tyler's ask", source.Text)
	if proposal == nil {
		t.Fatal("packaging studio proposal unavailable")
	}
	proposal.IntentOutcome = string(conversationIntentApprovalRequired)
	proposal.EffectClass = "expanded_audience"
	proposal.Status = "accepted"
	updatedThread, commitErr := app.commitScoutChatThreadMessages(user.Email, thread.ID, scoutChatMessageRecord{
		ID: "proposal-public-deck", Kind: scoutChatMessageKindProposal, Role: "scout", Proposal: proposal,
		CausedByMessageID: source.ID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if commitErr != nil {
		t.Fatal(commitErr)
	}
	thread = updatedThread
	response, err := app.startAcceptedPublicScoutWork(context.Background(), user, thread, "proposal-public-deck", *proposal, nil, binding)
	if err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Fatalf("goal starts=%d, want 1", starts)
	}
	work := response["agentThread"].(scoutAgentThread)
	answer := response["answer"].(scoutChatMessageRecord)
	if work.Mode != "goal" || work.Artifact.Metadata["processId"] != packagingStudioProcessID {
		t.Fatalf("packaging work=%+v metadata=%v", work, work.Artifact.Metadata)
	}
	if answer.ReplyTo != nil || answer.Thread == nil || answer.Thread.ID != work.ID || !strings.Contains(answer.Text, "progress") {
		t.Fatalf("deck root card=%+v", answer)
	}
	plan, ok := decodeGoalPlan(work.Artifact.Metadata["goalPlan"])
	if !ok || plan.RouteReceipt == nil {
		t.Fatalf("public packaging goal has no route receipt: %+v", work.Artifact.Metadata)
	}
	if err := app.verifyGoalRouteReceipt(&plan, *plan.RouteReceipt); err != nil {
		t.Fatalf("fresh public route receipt rejected: %v", err)
	}

	current, _, err := app.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	original := current
	for _, tc := range []struct {
		name   string
		mutate func(*scoutChatMessageRecord)
	}{
		{name: "acceptance", mutate: func(message *scoutChatMessageRecord) { message.Proposal.Status = "pending" }},
		{name: "audience effect", mutate: func(message *scoutChatMessageRecord) { message.Proposal.EffectClass = "private_only" }},
		{name: "source lineage", mutate: func(message *scoutChatMessageRecord) { message.CausedByMessageID = "different-source" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := original
			mutated.Messages = append([]scoutChatMessageRecord(nil), original.Messages...)
			found := false
			for i := range mutated.Messages {
				if mutated.Messages[i].ID != "proposal-public-deck" {
					continue
				}
				proposalCopy := *mutated.Messages[i].Proposal
				mutated.Messages[i].Proposal = &proposalCopy
				tc.mutate(&mutated.Messages[i])
				found = true
				break
			}
			if !found {
				t.Fatal("accepted proposal missing from public thread")
			}
			if err := app.saveScoutChatThread(mutated); err != nil {
				t.Fatal(err)
			}
			if err := app.verifyGoalRouteReceipt(&plan, *plan.RouteReceipt); err == nil {
				t.Fatalf("public route admitted changed %s", tc.name)
			}
			if err := app.saveScoutChatThread(original); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConversationApprovedWorkOperationBindsDestinationAndOutput(t *testing.T) {
	base := scoutRouterProposal{Kind: scoutRouterProposalKindToolRun, ToolID: packagingStudioProcessID, Objective: "Build the deck"}
	first, err := conversationApprovedWorkOperation("like-a-farmer", "aj@shareability.com", "proposal-1", base)
	if err != nil {
		t.Fatal(err)
	}
	otherDestination, _ := conversationApprovedWorkOperation("general", "aj@shareability.com", "proposal-1", base)
	otherOutput := base
	otherOutput.ToolID = "deck_outline"
	changedOutput, _ := conversationApprovedWorkOperation("like-a-farmer", "aj@shareability.com", "proposal-1", otherOutput)
	if first.BodyDigest == otherDestination.BodyDigest || first.BodyDigest == changedOutput.BodyDigest {
		t.Fatalf("operation did not bind destination/output: first=%s destination=%s output=%s", first.BodyDigest, otherDestination.BodyDigest, changedOutput.BodyDigest)
	}
}

func TestScoutConversationalAnswerCannotPersistFutureWorkPromise(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "openai-core-test")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = "openai-core-test"
	t.Cleanup(func() { kanbanApp = previousApp })
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow == "scout_route" {
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Route: "inline"}), nil
		}
		return "I'll review it and come back with recommendations.", nil
	})
	user := accountStore().findUser("aj@shareability.com")
	thread, err := kanbanApp.createScoutChatThread(user.Email, user.Name, "Scout", "")
	if err != nil {
		t.Fatal(err)
	}
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "Can you remind me what a deck includes?", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	answer := response["answer"].(scoutChatMessageRecord)
	if scoutConversationalAnswerPromisesFutureWork(answer.Text) || !strings.Contains(answer.Text, "Nothing was scheduled") || answer.Thread != nil || answer.ImageGeneration != nil {
		t.Fatalf("dishonest answer persisted: %+v", answer)
	}
	reloaded, _, err := kanbanApp.scoutChatThreadByID(user.Email, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Messages[len(reloaded.Messages)-1].Text; got != answer.Text {
		t.Fatalf("stored answer=%q response=%q", got, answer.Text)
	}
}

func TestScoutConversationalFutureWorkPromiseLanguage(t *testing.T) {
	for _, answer := range []string{
		"I'll review it and come back with recommendations.",
		"I will carefully prepare the deck and send it shortly.",
		"I'm going to research the market and follow up.",
		"Let me build that presentation for you.",
		"We'll post the finished analysis here.",
		"I plan to investigate this and report back.",
		"I'll take a look and get back to you.",
		"I'll look into it and let you know what I find.",
		"I'll get that finished and have it ready shortly.",
		"I'll review the deck tomorrow.",
		"I'll ask Tyler and share his answer here.",
		"I'll handle the research and get back to you tomorrow.",
		"I'll start now and report back.",
		"I'll take this from here.",
		"I'm on it.",
	} {
		if !scoutConversationalAnswerPromisesFutureWork(answer) {
			t.Errorf("future-work promise was not detected: %q", answer)
		}
	}
	for _, answer := range []string{
		"The phrase \"I'll review it and come back with recommendations\" is a future-work promise.",
		"The phrase \"I'm on it\" is not a durable work receipt.",
		"I won't create or post anything without your approval.",
		"I can explain how to create the deck yourself.",
		"I'll explain the distinction between a deck and an outline here.",
		"I'll run through the terminology below so the distinction is clear.",
		"I'll review the assumptions below and explain each issue here.",
		"If I were doing this manually, I would review the source first.",
		"Here is my review and the three recommendations.",
	} {
		if scoutConversationalAnswerPromisesFutureWork(answer) {
			t.Errorf("ordinary explanatory text was treated as a promise: %q", answer)
		}
	}
}

func TestPrivateDeckConfirmationRoutesEffectiveAskToPackagingStudio(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-deck-confirmation-test"
	previousStarter := startGoalThreadAsync
	t.Cleanup(func() { startGoalThreadAsync = previousStarter })
	starts := 0
	startGoalThreadAsync = func(*kanbanBoardApp, string) { starts++ }
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		t.Fatalf("deck confirmation unexpectedly called model workflow %q", request.Workflow)
		return "", nil
	})

	user := accountStore().findUser("aj@shareability.com")
	thread, err := app.createScoutChatThread(user.Email, user.Name, "Deck confirmation", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	original := scoutChatMessageRecord{
		ID: "deck-confirmation-source", Kind: "message", Role: "user",
		Text:       "Make a 5-slide deck about Tyler's Like A Farmer launch plan for the team.",
		AuthorName: user.Name, AuthorEmail: user.Email, CreatedAt: now.Format(time.RFC3339Nano),
	}
	direction := scoutChatMessageRecord{
		ID: "deck-confirmation-direction", Kind: "message", Role: "scout",
		Text:          "Should it feel polished and credibility-first, or more cinematic and culture-led? Do you want bold full-bleed imagery or a clean typographic system?",
		IntentOutcome: string(conversationIntentClarifyOnce), CausedByMessageID: original.ID,
		CreatedAt: now.Add(time.Millisecond).Format(time.RFC3339Nano),
	}
	thread, err = app.commitScoutChatThreadMessages(user.Email, thread.ID, original, direction)
	if err != nil {
		t.Fatal(err)
	}
	operation := conversationTurnOperation{
		ID: "deck-confirmation-operation-0001", BodyDigest: sha256Hex([]byte("deck confirmation operation body")),
	}
	response, err := app.appendScoutChatThreadMessage(withConversationTurnOperation(context.Background(), operation), user, thread.ID, "yes", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Fatalf("packaging starts=%d, want 1; response=%+v", starts, response)
	}
	work, ok := response["agentThread"].(scoutAgentThread)
	if !ok || work.Mode != "goal" || work.Artifact.Metadata["processId"] != packagingStudioProcessID {
		t.Fatalf("deck confirmation work=%+v response=%+v", work, response)
	}
	if !strings.Contains(strings.ToLower(work.Query), "tyler") || strings.TrimSpace(work.Query) == "yes" {
		t.Fatalf("confirmation did not preserve effective deck ask: %q", work.Query)
	}
	projected := app.projectScoutChatMutationResponseForViewer(user.Email, thread.ID, response)
	answer := projected["answer"].(scoutChatMessageRecord)
	if answer.Kind != "thread" || answer.Thread == nil || answer.Text != "I’m building your presentation now. I’ll post the finished file here." || answer.StudioProject == nil || answer.StudioProject.Kind != studioProjectKindPresentation || !strings.HasPrefix(answer.StudioProject.Href, "/work?project=") || strings.Contains(strings.ToLower(answer.Text), "<!doctype html") || strings.Contains(strings.ToLower(answer.Text), "open it in presentations") {
		t.Fatalf("deck confirmation answer=%+v", answer)
	}
}
