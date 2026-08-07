package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const agentRunnerACLFileBody = "Exact authorized source fact: Country+Golf launches with a 24-player creator invitational."

func agentRunnerACLThreadFixture(t *testing.T) (*kanbanBoardApp, scoutAgentThread, func()) {
	t.Helper()
	app, user := withIsolatedScoutFileApp(t)
	entry, _, err := app.memory.appendEntry(meetingMemoryKindFile, "file-agent-runner-acl",
		agentRunnerACLFileBody,
		map[string]string{
			"name":        "Country Golf Brief.pdf",
			"origin":      "files",
			"brainStatus": fileBrainStatusIngested,
			"visibility":  "organization",
			"tenantId":    canonicalArtifactTenantID(),
		})
	if err != nil {
		t.Fatalf("seed authorized File: %v", err)
	}
	thread := scoutAgentThread{
		ID:    "agent-thread-research-acl",
		Mode:  "design",
		Query: "Synthesize the attached Country+Golf brief into a positioning concept",
		Artifact: meetingMemoryEntry{
			ID: "os-artifact-agent-runner-acl",
			Metadata: map[string]string{
				"requestedBy": user.Email,
				"createdBy":   user.Email,
				"contextRefs": encodeAssistantContextRefs([]string{assistantFileContextRef(entry.ID)}),
				"agentName":   "Colton",
				"agentRole":   "Research Partner",
				"delegatedBy": "Scout",
			},
		},
	}
	revoke := func() {
		if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindFile, entry.ID, agentRunnerACLFileBody, map[string]string{
			"visibility": "private",
			"ownerEmail": "someone-else@shareability.com",
		}); err != nil {
			t.Fatalf("revoke File ACL: %v", err)
		}
	}
	return app, thread, revoke
}

func assertAgentRunnerACLRevocation(t *testing.T, run func() error, providerCalls *int) {
	t.Helper()
	before := *providerCalls
	err := run()
	if !errors.Is(err, ErrAgentThreadSourceChanged) {
		t.Fatalf("revoked source err=%v, want ErrAgentThreadSourceChanged", err)
	}
	if *providerCalls != before {
		t.Fatalf("provider calls advanced after revocation: before=%d after=%d", before, *providerCalls)
	}
	card := buildAgentThreadError(scoutAgentThread{Mode: "research", Query: "review it"}, err)
	for _, want := range []string{"Status: needs attention", "source changed", "Nothing was sent to a provider"} {
		if !strings.Contains(card, want) {
			t.Fatalf("source-changed card missing %q:\n%s", want, card)
		}
	}
}

func TestOpenAIRunnerReauthorizesExactFileContextAtProviderAdmission(t *testing.T) {
	clearAgentRunnerEnv(t)
	app, thread, revoke := agentRunnerACLThreadFixture(t)
	app.apiKey = "sk-openai-test"
	t.Setenv("OPENAI_API_KEY", "sk-openai-test")
	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerOpenAIText)
	providerCalls := 0
	run := func() error {
		_, err := app.produceAgentThreadArtifactWithWorker(context.Background(), thread, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
			providerCalls++
			if !strings.Contains(request.Input, agentRunnerACLFileBody) || !strings.Contains(request.Input, "Country Golf Brief.pdf") {
				t.Fatalf("OpenAI input did not receive authorized exact File context:\n%s", request.Input)
			}
			return "# Country+Golf research\n\nVerified.", nil
		})
		return err
	}
	if err := run(); err != nil || providerCalls != 1 {
		t.Fatalf("authorized OpenAI run calls=%d err=%v", providerCalls, err)
	}
	revoke()
	assertAgentRunnerACLRevocation(t, run, &providerCalls)
}

func TestAnthropicRunnerReauthorizesExactFileContextAtProviderAdmission(t *testing.T) {
	clearAgentRunnerEnv(t)
	app, thread, revoke := agentRunnerACLThreadFixture(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerAnthropicFable)
	previous := createAnthropicMessagesResponse
	providerCalls := 0
	createAnthropicMessagesResponse = func(_ context.Context, _ string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		providerCalls++
		encoded, _ := json.Marshal(request.Messages)
		prompt := string(encoded)
		if !strings.Contains(prompt, agentRunnerACLFileBody) || !strings.Contains(prompt, "Country Golf Brief.pdf") {
			t.Fatalf("Anthropic prompt did not receive authorized exact File context:\n%s", prompt)
		}
		return anthropicMessagesResponse{StopReason: "end_turn", Content: []json.RawMessage{mockAnthropicTextBlock("# Country+Golf research\n\nVerified.")}}, nil
	}
	t.Cleanup(func() { createAnthropicMessagesResponse = previous })
	run := func() error {
		_, err := app.produceAgentThreadArtifactWithWorker(context.Background(), thread, nil)
		return err
	}
	if err := run(); err != nil || providerCalls != 1 {
		t.Fatalf("authorized Anthropic run calls=%d err=%v", providerCalls, err)
	}
	revoke()
	assertAgentRunnerACLRevocation(t, run, &providerCalls)
}

func TestCodexRunnerReauthorizesExactFileContextAtProviderAdmission(t *testing.T) {
	clearAgentRunnerEnv(t)
	enableCodexExecutionForTest(t)
	app, thread, revoke := agentRunnerACLThreadFixture(t)
	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerCodexLocal)
	previous := runCodexExecCommand
	providerCalls := 0
	runCodexExecCommand = func(_ context.Context, _ codexExecConfig, prompt string) (codexExecResult, error) {
		providerCalls++
		if !strings.Contains(prompt, agentRunnerACLFileBody) || !strings.Contains(prompt, "Country Golf Brief.pdf") {
			t.Fatalf("Codex prompt did not receive authorized exact File context:\n%s", prompt)
		}
		return codexExecResult{FinalMessage: "# Country+Golf research\n\nVerified."}, nil
	}
	t.Cleanup(func() { runCodexExecCommand = previous })
	run := func() error {
		_, err := app.produceAgentThreadArtifactWithWorker(context.Background(), thread, nil)
		return err
	}
	if err := run(); err != nil || providerCalls != 1 {
		t.Fatalf("authorized Codex run calls=%d err=%v", providerCalls, err)
	}
	revoke()
	assertAgentRunnerACLRevocation(t, run, &providerCalls)
}

func TestCodexSidecarDefersFileBodyAndReauthorizesAgainAtClaim(t *testing.T) {
	clearAgentRunnerEnv(t)
	enableCodexExecutionForTest(t)
	app, thread, revoke := agentRunnerACLThreadFixture(t)
	queueDir := t.TempDir()
	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerCodexSidecar)
	t.Setenv("BONFIRE_CODEX_QUEUE_PATH", queueDir)
	t.Setenv("BONFIRE_RUNNER_TOKEN", "") // callback fails locally without network after the persisted stop

	providerCalls := 0
	previous := runCodexExecCommand
	runCodexExecCommand = func(_ context.Context, _ codexExecConfig, _ string) (codexExecResult, error) {
		providerCalls++
		return codexExecResult{FinalMessage: "must not run after revocation"}, nil
	}
	t.Cleanup(func() { runCodexExecCommand = previous })

	result, err := app.produceAgentThreadArtifactWithWorker(context.Background(), thread, nil)
	if err != nil || result.Terminal {
		t.Fatalf("enqueue source-bound sidecar result=%+v err=%v", result, err)
	}
	jobID := result.Metadata["runnerJobId"]
	store := newCodexRunnerJobStore(queueDir)
	queued, err := store.read(filepath.Base(store.jobPath(jobID)))
	if err != nil {
		t.Fatalf("read queued sidecar job: %v", err)
	}
	if strings.TrimSpace(queued.Prompt) != "" || queued.ThreadMetadata["contextRefs"] == "" {
		t.Fatalf("source-bound queue serialized provider prompt or lost refs: %+v", queued)
	}
	authorizedPrompt, _, err := codexRunnerPromptAtProviderAdmission(context.Background(), *queued)
	if err != nil || !strings.Contains(authorizedPrompt, agentRunnerACLFileBody) {
		t.Fatalf("authorized sidecar claim prompt err=%v prompt=%q", err, authorizedPrompt)
	}

	revoke()
	if _, _, err := codexRunnerPromptAtProviderAdmission(context.Background(), *queued); !errors.Is(err, ErrAgentThreadSourceChanged) {
		t.Fatalf("revoked sidecar claim err=%v, want ErrAgentThreadSourceChanged", err)
	}
	claimed, err := store.claimNext("runner-acl-test")
	if err != nil || claimed == nil {
		t.Fatalf("claim queued sidecar job=%+v err=%v", claimed, err)
	}
	processCodexRunnerJob(context.Background(), store, *claimed)
	if providerCalls != 0 {
		t.Fatalf("sidecar invoked Codex after claim-time revocation: calls=%d", providerCalls)
	}
	failed, err := store.read(filepath.Base(store.jobPath(jobID)))
	if err != nil || failed.Status != codexJobStatusFailed || failed.Metadata["sourceChanged"] != "true" {
		t.Fatalf("sidecar source-change failure not durable: job=%+v err=%v", failed, err)
	}
}

func TestSharedChannelAgentRejectsRequesterPrivateFileBeforeProviderAdmission(t *testing.T) {
	clearAgentRunnerEnv(t)
	app, user := withIsolatedScoutFileApp(t)
	channel, err := app.createScoutChatThread(user.Email, user.Name, "shared research", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	private, _, err := app.memory.appendEntry(meetingMemoryKindFile, "file-private-channel-denial", "AJ private source body", map[string]string{
		"name":        "AJ private notes.txt",
		"origin":      "files",
		"brainStatus": fileBrainStatusIngested,
		"visibility":  "private",
		"ownerEmail":  user.Email,
		"tenantId":    canonicalArtifactTenantID(),
	})
	if err != nil {
		t.Fatal(err)
	}
	thread := scoutAgentThread{
		ID: "agent-thread-private-channel-denial", Mode: "research", Query: "Research this privately owned source",
		Artifact: meetingMemoryEntry{ID: "artifact-private-channel-denial", Metadata: map[string]string{
			"requestedBy": user.Email,
			"createdBy":   user.Email,
			"originKind":  agentThreadOriginChannel,
			"originId":    channel.ID,
			"contextRefs": encodeAssistantContextRefs([]string{assistantFileContextRef(private.ID)}),
		}},
	}
	if _, err := app.agentThreadProviderContext(context.Background(), thread); !errors.Is(err, ErrAgentThreadSourceChanged) || !strings.Contains(err.Error(), "referenced File") {
		t.Fatalf("shared channel accepted requester-private source: %v", err)
	}
}

func queuedColtonSidecarFixture(t *testing.T) (strideProjectAuthorityFixture, STRIDEProductTeamAgent, *codexRunnerJobStore, *codexRunnerJob) {
	t.Helper()
	fixture := newSTRIDEProjectAuthorityFixture(t)
	directThreadID := strideProductAgentDirectThreadPrefix + "colton_sidecar_claim_test"
	hired := hireResearchAgentForBridgeTest(t, fixture, "colton-research", directThreadID)
	profile, ok := fixture.app.strideAgentDirectThreadContext(directThreadID)
	if !ok {
		t.Fatal("hired Colton profile unavailable")
	}
	entry, _, err := fixture.app.memory.appendEntry(meetingMemoryKindFile, "file-colton-sidecar-claim",
		agentRunnerACLFileBody,
		map[string]string{
			"name": "Country Golf Brief.pdf", "origin": "files", "brainStatus": fileBrainStatusIngested,
			"visibility": "organization", "tenantId": canonicalArtifactTenantID(),
		})
	if err != nil {
		t.Fatal(err)
	}
	metadata := agentThreadGoalSpecForProfile(profile, scoutParticipantName).metadata()
	metadata["requestedBy"] = fixture.user.Email
	metadata["createdBy"] = fixture.user.Email
	metadata["contextRefs"] = encodeAssistantContextRefs([]string{assistantFileContextRef(entry.ID)})
	thread := scoutAgentThread{
		ID: "agent-thread-colton-sidecar-claim", Mode: "research", Query: "Research the source as Colton",
		Artifact: meetingMemoryEntry{ID: "artifact-colton-sidecar-claim", Metadata: metadata},
	}
	providerContext, err := fixture.app.agentThreadProviderContext(context.Background(), thread)
	if err != nil {
		t.Fatal(err)
	}
	job := fixture.app.newAgentJob(thread)
	job.Context = providerContext
	queueDir := t.TempDir()
	t.Setenv("BONFIRE_CODEX_QUEUE_PATH", queueDir)
	result, err := fixture.app.enqueueCodexAgentThreadArtifactForJob(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	store := newCodexRunnerJobStore(queueDir)
	queued, err := store.read(filepath.Base(store.jobPath(result.Metadata["runnerJobId"])))
	if err != nil {
		t.Fatal(err)
	}
	return fixture, hired, store, queued
}

func TestCodexSidecarReauthorizesCoworkerPauseAtClaim(t *testing.T) {
	clearAgentRunnerEnv(t)
	fixture, hired, store, _ := queuedColtonSidecarFixture(t)
	err := fixture.runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
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
	providerCalls := 0
	previous := runCodexExecCommand
	runCodexExecCommand = func(_ context.Context, _ codexExecConfig, _ string) (codexExecResult, error) {
		providerCalls++
		return codexExecResult{}, nil
	}
	t.Cleanup(func() { runCodexExecCommand = previous })
	claimed, err := store.claimNext("runner-paused-seat")
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	processCodexRunnerJob(context.Background(), store, *claimed)
	if providerCalls != 0 {
		t.Fatalf("paused Colton reached sidecar provider: calls=%d", providerCalls)
	}
	failed, err := store.read(filepath.Base(store.jobPath(claimed.ID)))
	if err != nil || failed.Status != codexJobStatusFailed || !strings.Contains(failed.Error, "assigned agent is unavailable") {
		t.Fatalf("paused-seat claim was not durably stopped: job=%+v err=%v", failed, err)
	}
}

func TestCodexSidecarRefreshesCorrectedCoworkerLearningAtClaim(t *testing.T) {
	clearAgentRunnerEnv(t)
	fixture, hired, store, queued := queuedColtonSidecarFixture(t)
	staleDigest := queued.ThreadMetadata["agentDigest"]
	const staleLearning = "Put the recommendation after a long appendix."
	const correctedLearning = "Lead with the recommendation, then show the evidence map."
	err := fixture.runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		agent, ok := ctx.Product.agentRecord(hired.ID)
		if !ok {
			return ErrSTRIDEProductUnknown
		}
		learned, err := ctx.Product.recordAgentLearning(agent.ID, agent.Revision, "delivery", "team", staleLearning, time.Now().UTC())
		if err != nil {
			return err
		}
		_, err = ctx.Product.resolveAgentLearning(learned.ID, learned.Revision, learned.Learning[0].ID, "correct", correctedLearning, time.Now().UTC())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	prompt, currentMetadata, err := codexRunnerPromptAtProviderAdmission(context.Background(), *queued)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, correctedLearning) || strings.Contains(prompt, staleLearning) {
		t.Fatalf("claim prompt did not refresh corrected learning:\n%s", prompt)
	}
	if currentMetadata["agentDigest"] == "" || currentMetadata["agentDigest"] == staleDigest || currentMetadata["agentReauthorizedAt"] == "" {
		t.Fatalf("claim metadata did not refresh coworker identity: stale=%q current=%v", staleDigest, currentMetadata)
	}

	providerCalls := 0
	previous := runCodexExecCommand
	runCodexExecCommand = func(_ context.Context, _ codexExecConfig, providerPrompt string) (codexExecResult, error) {
		providerCalls++
		if !strings.Contains(providerPrompt, correctedLearning) || strings.Contains(providerPrompt, staleLearning) {
			t.Fatalf("Codex received stale coworker learning:\n%s", providerPrompt)
		}
		return codexExecResult{FinalMessage: "# Current Colton research\n\nVerified."}, nil
	}
	t.Cleanup(func() { runCodexExecCommand = previous })
	claimed, err := store.claimNext("runner-corrected-seat")
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	processCodexRunnerJob(context.Background(), store, *claimed)
	completed, err := store.read(filepath.Base(store.jobPath(claimed.ID)))
	if err != nil || completed.Status != codexJobStatusComplete || providerCalls != 1 || completed.Metadata["agentDigest"] != currentMetadata["agentDigest"] || completed.ThreadMetadata["agentDigest"] != currentMetadata["agentDigest"] {
		t.Fatalf("corrected claim identity not preserved: calls=%d job=%+v err=%v", providerCalls, completed, err)
	}
}
