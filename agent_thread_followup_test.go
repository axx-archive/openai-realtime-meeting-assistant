package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func seedCompleteGrillArtifact(t *testing.T, app *kanbanBoardApp) meetingMemoryEntry {
	t.Helper()
	artifact, _, err := app.createOSArtifactWithMetadata("grill", "grill: the nimbus pitch", "# Nimbus pressure test\n\nREADINESS: 6.2/10\n\nStrongest objections: pricing is unproven.", "AJ", map[string]string{
		"source":         "scout_thread",
		"threadId":       "agent-thread-grill-1",
		"threadQuery":    "grill: the nimbus pitch",
		"status":         "complete",
		"threadStatus":   "complete",
		"threadVersion":  "1",
		"completedAt":    time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano),
		"readinessScore": "6.2",
	})
	if err != nil {
		t.Fatalf("seed grill artifact: %v", err)
	}
	return artifact
}

func TestStrideE10ScoutFollowUpCutoverFailsBeforePrivateSourcesAndWrite(t *testing.T) {
	converter, _, _, _, _ := strideE10TenantEnvelopeTestSetup(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	t.Setenv("ANTHROPIC_API_KEY", "")
	artifact := seedCompleteGrillArtifact(t, app)
	mode, query := "grill", artifact.Metadata["threadQuery"]
	runID := "agent-thread-grill-tenant-followup"
	envelope, err := MintStrideE10TenantAuthorityEnvelopeForSurface(context.Background(), converter, strings.Repeat("a", 64), StrideE10TenantSurfaceScout, StrideE10TenantAuthorityPurposeForScoutThread(runID, mode, query), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	asyncCalls := 0
	priorAsync := startAgentThreadFollowUpAsync
	startAgentThreadFollowUpAsync = func(_ *kanbanBoardApp, run agentThreadFollowUpRun) { asyncCalls++ }
	t.Cleanup(func() { startAgentThreadFollowUpAsync = priorAsync })
	before, _ := app.osArtifactByID(artifact.ID)
	if _, err := app.launchAgentThreadFollowUpWithTenantAuthority(artifact, "recheck the bounded evidence", "person-attacker", []scoutChatMessageRecord{{AuthorName: "Other Org", Text: "private body"}}, &agentThreadFollowUpAttachmentScope{destinationID: "private-other-org", files: []scoutChatFileAttachment{{SourceID: "cross-org-private"}}}, runID, &envelope); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("cutover follow-up used legacy sources: %v", err)
	}
	if asyncCalls != 0 {
		t.Fatalf("cutover follow-up reached async provider %d times", asyncCalls)
	}
	stored, ok := app.osArtifactByID(artifact.ID)
	if !ok || stored.Text != before.Text || stored.Metadata["threadVersion"] != before.Metadata["threadVersion"] {
		t.Fatalf("cutover follow-up changed destination controller: before=%+v after=%+v", before, stored)
	}
}

func TestScoutFollowUpUsesReceiverAppWhenGlobalAppIsPoisoned(t *testing.T) {
	receiver := newIsolatedKanbanBoardApp(t)
	receiver.apiKey = "test-key"
	t.Setenv("ANTHROPIC_API_KEY", "")
	source, created, err := receiver.ensureScoutChatThread("followup-receiver-scoped-source", "aj@shareability.com", "AJ", "Receiver source", scoutChatVisibilityPublic, []string{"e@shareability.com"})
	if err != nil || !created {
		t.Fatalf("create receiver source: created=%t err=%v", created, err)
	}
	artifact, _, err := receiver.createOSArtifactWithMetadata("grill", "grill: receiver authority", "# Receiver authority proof", "AJ", map[string]string{
		"source": "scout_thread", "threadId": "agent-thread-receiver-authority", "threadQuery": "grill: receiver authority",
		"originKind": agentThreadOriginChannel, "originId": source.ID, "originSurface": "chat:" + source.ID,
		"requestedBy": "aj@shareability.com", "status": "complete", "threadStatus": "complete", "threadVersion": "1",
	})
	if err != nil {
		t.Fatalf("create receiver artifact: %v", err)
	}

	poison := newIsolatedKanbanBoardApp(t)
	if _, created, err := poison.ensureScoutChatThread(source.ID, "e@shareability.com", "E", "Unrelated private source", scoutChatVisibilityPrivate, nil); err != nil || !created {
		t.Fatalf("create poisoned global source: created=%t err=%v", created, err)
	}
	receiverHeader := receiver.resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact))
	poisonHeader := poison.resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact))
	if receiverHeader.Visibility != scoutChatVisibilityPublic || poisonHeader.Visibility != scoutChatVisibilityPrivate || poisonHeader.OwnerEmail != "e@shareability.com" {
		t.Fatalf("fixture did not create conflicting receiver/global authority: receiver=%+v poison=%+v", receiverHeader, poisonHeader)
	}

	previousApp, previousAuthorizer := kanbanApp, artifactObjectAuthorizer
	kanbanApp = poison
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{TenantID: canonicalArtifactTenantID()}
	t.Cleanup(func() {
		kanbanApp = previousApp
		artifactObjectAuthorizer = previousAuthorizer
	})
	user := accountStore().findUser("aj@shareability.com")
	authorized, ok := receiver.authorizedArtifactForActions(context.Background(), user, artifact.ID, ACLReadContent, ACLExecute, ACLWrite)
	if !ok {
		t.Fatal("Scout follow-up admission consulted the poisoned process-global app instead of the receiver app")
	}
	asyncCalls := 0
	previousAsync := startAgentThreadFollowUpAsync
	startAgentThreadFollowUpAsync = func(runApp *kanbanBoardApp, _ agentThreadFollowUpRun) {
		if runApp != receiver {
			t.Fatalf("follow-up launched on app %p, want receiver %p", runApp, receiver)
		}
		asyncCalls++
	}
	t.Cleanup(func() { startAgentThreadFollowUpAsync = previousAsync })
	thread, err := receiver.dispatchAuthorizedArtifactFollowUpWithAttachments(context.Background(), user, authorized, "tighten the receiver proof", user.Name, nil, source, nil, "")
	if err != nil {
		t.Fatalf("dispatch receiver-scoped follow-up: %v", err)
	}
	if asyncCalls != 1 || thread.Artifact.ID != artifact.ID || thread.Artifact.Metadata["threadVersion"] != "2" {
		t.Fatalf("receiver follow-up async=%d thread=%+v", asyncCalls, thread)
	}
	stored, ok := receiver.osArtifactByID(artifact.ID)
	if !ok || stored.Metadata["threadStatus"] != "running" || stored.Metadata["threadVersion"] != "2" {
		t.Fatalf("receiver follow-up did not persist running v2: %+v", stored)
	}
}

// A follow-up run versions the SAME artifact in place: stable id, threadVersion
// bump, archived prior body, readiness delta, run log, chat ref flip, and dual
// notifications (creator + distinct requester).
func TestLaunchAgentThreadFollowUpVersionsArtifactInPlace(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	// Keyless-Anthropic pins the gpt-5.5 path (worker=openai_text_response).
	t.Setenv("ANTHROPIC_API_KEY", "")

	var captured openAITextRequest
	previousAsync := startAgentThreadFollowUpAsync
	startAgentThreadFollowUpAsync = func(runApp *kanbanBoardApp, run agentThreadFollowUpRun) {
		runApp.runAgentThreadFollowUpWithResponder(run, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
			captured = request
			return "READINESS: 7.1/10\n\nWhat changed in v2: pricing objection resolved by Tim's reply.", nil
		})
	}
	t.Cleanup(func() { startAgentThreadFollowUpAsync = previousAsync })

	artifact := seedCompleteGrillArtifact(t, app)

	// The chat card whose persisted ref must flip with the follow-up.
	chatThread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create chat thread: %v", err)
	}
	if _, err := app.commitScoutChatThreadMessages(chatThread.OwnerEmail, chatThread.ID, scoutChatMessageRecord{
		ID:        "scout-chat-message-card",
		Kind:      "thread",
		Role:      "scout",
		Text:      "pressure test thread launched",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Thread:    &scoutChatThreadRef{ID: "agent-thread-grill-1", Mode: "grill", Query: "grill: the nimbus pitch", Status: "queued", ArtifactID: artifact.ID},
	}); err != nil {
		t.Fatalf("seed chat card: %v", err)
	}

	teamReplies := []scoutChatMessageRecord{{
		ID:         "scout-chat-message-reply",
		Kind:       "message",
		Role:       "user",
		Text:       "we locked pricing at $99/mo with two design partners",
		AuthorName: "Tim",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}}
	thread, err := app.launchAgentThreadFollowUp(artifact.ID, "re-grill with the new pricing answers", "Tim", teamReplies)
	if err != nil {
		t.Fatalf("launchAgentThreadFollowUp: %v", err)
	}
	if thread.ID != "agent-thread-grill-1" {
		t.Fatalf("thread.ID=%q, want the ORIGINAL threadId so existing cards flip", thread.ID)
	}
	if thread.Mode != "grill" {
		t.Fatalf("thread.Mode=%q, want grill from artifact metadata", thread.Mode)
	}

	for _, want := range []string{"Prior artifact (v1) body:", "Nimbus pressure test", "we locked pricing at $99/mo", "Follow-up request: re-grill with the new pricing answers", "Run: follow-up v2"} {
		if !strings.Contains(captured.Input, want) {
			t.Fatalf("follow-up input missing %q:\n%s", want, captured.Input)
		}
	}
	for _, want := range []string{"What changed in v2", "Re-score honestly", "READINESS"} {
		if !strings.Contains(captured.Instructions, want) {
			t.Fatalf("follow-up instructions missing %q", want)
		}
	}

	stored, ok := app.osArtifactByID(artifact.ID)
	if !ok {
		t.Fatalf("artifact %s disappeared", artifact.ID)
	}
	if stored.Metadata["threadVersion"] != "2" {
		t.Fatalf("threadVersion=%q, want 2", stored.Metadata["threadVersion"])
	}
	if !strings.HasPrefix(stored.Text, "READINESS: 7.1/10") {
		t.Fatalf("text=%q, want the new version on top", stored.Text)
	}
	if got := strings.Count(stored.Text, "## Previous run · v1 ·"); got != 1 {
		t.Fatalf("previous-run v1 sections=%d, want exactly 1:\n%s", got, stored.Text)
	}
	if !strings.Contains(stored.Text, "Strongest objections: pricing is unproven.") {
		t.Fatal("prior body must survive in the archive section")
	}
	if stored.Metadata["readinessScore"] != "7.1" || stored.Metadata["readinessPrevScore"] != "6.2" || stored.Metadata["readinessDelta"] != "+0.9" {
		t.Fatalf("readiness metadata=%#v, want 7.1 / prev 6.2 / delta +0.9", stored.Metadata)
	}
	if stored.Metadata["worker"] != "openai_text_response" {
		t.Fatalf("worker=%q, want openai_text_response", stored.Metadata["worker"])
	}
	var runs []agentThreadRunLogEntry
	if err := json.Unmarshal([]byte(stored.Metadata["threadRuns"]), &runs); err != nil {
		t.Fatalf("decode threadRuns %q: %v", stored.Metadata["threadRuns"], err)
	}
	if len(runs) != 2 || runs[0].Version != 1 || runs[1].Version != 2 || runs[1].Score != "7.1" || runs[1].By != "Tim" {
		t.Fatalf("threadRuns=%#v, want backfilled v1 + this run", runs)
	}

	// The persisted chat ref flipped to complete with the artifact id.
	savedThread, _, err := app.scoutChatThreadByID(chatThread.OwnerEmail, chatThread.ID)
	if err != nil {
		t.Fatalf("scoutChatThreadByID: %v", err)
	}
	ref := savedThread.Messages[0].Thread
	if ref == nil || ref.Status != "complete" || ref.ArtifactID != artifact.ID {
		t.Fatalf("chat ref=%#v, want complete + artifact id after the follow-up", ref)
	}

	// Creator AND the distinct follow-up requester both get the milestone,
	// with the readiness dial in the text.
	emails := map[string]string{}
	for _, record := range app.notifications {
		emails[record.UserEmail] = record.Text
	}
	creatorText, hasCreator := emails["aj@shareability.com"]
	requesterText, hasRequester := emails["tim@shareability.com"]
	if !hasCreator || !hasRequester {
		t.Fatalf("notification recipients=%v, want creator and follow-up requester", emails)
	}
	for _, text := range []string{creatorText, requesterText} {
		if !strings.Contains(text, "follow-up complete") || !strings.Contains(text, "readiness 6.2 → 7.1") {
			t.Fatalf("notification text=%q, want completion + readiness dial", text)
		}
	}
}

// Follow-up success files the deliverable and closes the conversation loop
// without mutating an archived Board card.
func TestFollowUpSuccessPreservesBoardHistoryAttachesPackageAndDeliversOrigin(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	t.Setenv("ANTHROPIC_API_KEY", "")

	previousAsync := startAgentThreadFollowUpAsync
	startAgentThreadFollowUpAsync = func(runApp *kanbanBoardApp, run agentThreadFollowUpRun) {
		runApp.runAgentThreadFollowUpWithResponder(run, func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
			return completeResearchArtifactForTest() + "\n\nWhat changed in v2: the worker error was resolved.", nil
		})
	}
	t.Cleanup(func() { startAgentThreadFollowUpAsync = previousAsync })

	card := createLinkageTestCard(t, app, "Nimbus market scan")
	pkg, err := app.createVenturePackage("Nimbus creator platform", "", "AJ")
	if err != nil {
		t.Fatalf("createVenturePackage: %v", err)
	}
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "growth", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	// The artifact may retain a historical boardCardId, but that archive is no
	// longer a work-state authority.
	artifact, _, err := app.createOSArtifactWithMetadata("research", "Nimbus market scan", "scaffold body", "AJ", map[string]string{
		"source":        "scout_thread",
		"threadId":      "agent-thread-research-err",
		"threadQuery":   "Nimbus market scan",
		"title":         "Nimbus market scan",
		"status":        "error",
		"threadStatus":  "error",
		"threadVersion": "1",
		"boardCardId":   card.ID,
		"packageId":     pkg.ID,
		"originKind":    agentThreadOriginChannel,
		"originId":      channel.ID,
	})
	if err != nil {
		t.Fatalf("seed errored artifact: %v", err)
	}
	if status := linkageCardStatus(t, app, card.ID); status != kanbanStatusBacklog {
		t.Fatalf("status=%q before the follow-up, want archived Backlog", status)
	}

	if _, err := app.launchAgentThreadFollowUp(artifact.ID, "try again with the fix", "Tim", nil); err != nil {
		t.Fatalf("launchAgentThreadFollowUp: %v", err)
	}

	if status := linkageCardStatus(t, app, card.ID); status != kanbanStatusBacklog {
		t.Fatalf("status=%q after the follow-up success, want archived Backlog", status)
	}
	// package auto-attach: the completed deliverable files into its binder.
	attached, _ := app.venturePackageByID(pkg.ID)
	if len(attached.ArtifactIDs) != 1 || attached.ArtifactIDs[0] != artifact.ID {
		t.Fatalf("artifactIds=%v, want the recovered artifact attached", attached.ArtifactIDs)
	}
	// close the loop: the origin channel receives exactly one completion card
	// and deliveredAt is stamped.
	saved, _, err := app.scoutChatThreadByID(channel.OwnerEmail, channel.ID)
	if err != nil {
		t.Fatalf("scoutChatThreadByID: %v", err)
	}
	cards := 0
	for _, message := range saved.Messages {
		if message.Thread != nil && message.Thread.ArtifactID == artifact.ID && message.Thread.Status == "complete" {
			cards++
		}
	}
	if cards != 1 {
		t.Fatalf("completion cards=%d in the origin channel, want exactly one", cards)
	}
	stored, _ := app.osArtifactByID(artifact.ID)
	if stored.Metadata["deliveredAt"] == "" {
		t.Fatal("deliveredAt must be stamped by the follow-up delivery")
	}
}

// A reply armed at a running artifact is rejected — but the typed answer must
// survive as a plain channel message (and feed the NEXT run's team-reply
// context) instead of being silently dropped.
func TestFollowUpReplyWhileRunningCommitsUserMessage(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("grill", "grill: the nimbus pitch", "scaffold", "AJ", map[string]string{
		"source":       "scout_thread",
		"threadId":     "agent-thread-grill-running",
		"status":       "running",
		"threadStatus": "running",
	})
	if err != nil {
		t.Fatalf("seed running artifact: %v", err)
	}
	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "launch plan", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages(channel.OwnerEmail, channel.ID, scoutChatMessageRecord{
		ID:        "scout-chat-message-card",
		Kind:      "thread",
		Role:      "scout",
		Text:      "pressure test thread launched",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Thread:    &scoutChatThreadRef{ID: "agent-thread-grill-running", Mode: "grill", Query: "grill: the nimbus pitch", Status: "running", ArtifactID: artifact.ID},
	}); err != nil {
		t.Fatalf("seed channel card: %v", err)
	}

	user := accountStore().findUser("tim@shareability.com")
	if user == nil {
		t.Fatal("seed user tim@shareability.com missing")
	}
	_, err = kanbanApp.appendScoutChatThreadMessage(context.Background(), user, channel.ID, "we locked pricing at $99/mo", nil, artifact.ID)
	if err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("err=%v, want the still-running rejection", err)
	}

	saved, _, err := kanbanApp.scoutChatThreadByID(channel.OwnerEmail, channel.ID)
	if err != nil {
		t.Fatalf("scoutChatThreadByID: %v", err)
	}
	last := saved.Messages[len(saved.Messages)-1]
	if last.Role != "user" || last.Text != "we locked pricing at $99/mo" || last.AuthorEmail != "tim@shareability.com" {
		t.Fatalf("last message=%#v, want the rejected reply committed with author identity", last)
	}
	// the committed reply feeds the next follow-up run's team-reply context.
	replies := scoutChatRepliesSince(saved, "")
	found := false
	for _, reply := range replies {
		if reply.Text == "we locked pricing at $99/mo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("team replies=%#v, want the committed reply available as worker context", replies)
	}
}

func TestFollowUpRejectedWhileRunning(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	artifact, _, err := app.createOSArtifactWithMetadata("grill", "grill: the nimbus pitch", "scaffold", "AJ", map[string]string{
		"source":       "scout_thread",
		"threadId":     "agent-thread-grill-2",
		"status":       "running",
		"threadStatus": "running",
	})
	if err != nil {
		t.Fatalf("seed artifact: %v", err)
	}
	if _, err := app.launchAgentThreadFollowUp(artifact.ID, "again", "AJ", nil); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("err=%v, want still-running rejection", err)
	}
}

// Cost control: follow-ups ALWAYS use the bounded text worker, even when the
// codex agent-thread worker is configured.
func TestFollowUpUsesTextWorkerWithCodexEnvSet(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("BONFIRE_CODEX_AGENT_THREADS", "1")

	responderCalls := 0
	previousAsync := startAgentThreadFollowUpAsync
	startAgentThreadFollowUpAsync = func(runApp *kanbanBoardApp, run agentThreadFollowUpRun) {
		runApp.runAgentThreadFollowUpWithResponder(run, func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
			responderCalls++
			return "READINESS: 6.2/10\n\nWhat changed in v2: nothing landed.", nil
		})
	}
	t.Cleanup(func() { startAgentThreadFollowUpAsync = previousAsync })

	artifact := seedCompleteGrillArtifact(t, app)
	if _, err := app.launchAgentThreadFollowUp(artifact.ID, "run again", "AJ", nil); err != nil {
		t.Fatalf("launchAgentThreadFollowUp: %v", err)
	}
	if responderCalls != 1 {
		t.Fatalf("responderCalls=%d, want the injected text responder to run", responderCalls)
	}
	stored, _ := app.osArtifactByID(artifact.ID)
	if stored.Metadata["worker"] != "openai_text_response" {
		t.Fatalf("worker=%q, want openai_text_response despite BONFIRE_CODEX_AGENT_THREADS", stored.Metadata["worker"])
	}
	if stored.Metadata["runnerJobId"] != "" {
		t.Fatalf("runnerJobId=%q, want no codex job enqueued", stored.Metadata["runnerJobId"])
	}
}

// A failed follow-up never clobbers a good artifact: text untouched, prior
// terminal status restored verbatim, error stamped in metadata only.
func TestFollowUpErrorPreservesBodyAndStatus(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	t.Setenv("ANTHROPIC_API_KEY", "")

	previousAsync := startAgentThreadFollowUpAsync
	startAgentThreadFollowUpAsync = func(runApp *kanbanBoardApp, run agentThreadFollowUpRun) {
		runApp.runAgentThreadFollowUpWithResponder(run, func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
			return "", fmt.Errorf("worker exploded")
		})
	}
	t.Cleanup(func() { startAgentThreadFollowUpAsync = previousAsync })

	artifact := seedCompleteGrillArtifact(t, app)
	if _, err := app.launchAgentThreadFollowUp(artifact.ID, "run again", "Tim", nil); err != nil {
		t.Fatalf("launchAgentThreadFollowUp: %v", err)
	}

	stored, _ := app.osArtifactByID(artifact.ID)
	if stored.Text != artifact.Text {
		t.Fatalf("text changed on a failed follow-up:\n%q\nwant\n%q", stored.Text, artifact.Text)
	}
	if stored.Metadata["status"] != "complete" || stored.Metadata["threadStatus"] != "complete" {
		t.Fatalf("status=%q/%q, want the prior terminal state restored", stored.Metadata["status"], stored.Metadata["threadStatus"])
	}
	if stored.Metadata["threadVersion"] != "1" {
		t.Fatalf("threadVersion=%q, want restored to 1", stored.Metadata["threadVersion"])
	}
	if !strings.Contains(stored.Metadata["followUpError"], "worker exploded") {
		t.Fatalf("followUpError=%q, want the worker error", stored.Metadata["followUpError"])
	}
	if stored.Metadata["followUpStatus"] != "needs_attention" || !strings.Contains(stored.Metadata["progressNote"], "revision needs attention") {
		t.Fatalf("failed revision was not projected honestly: status=%q note=%q", stored.Metadata["followUpStatus"], stored.Metadata["progressNote"])
	}
	if stored.Metadata["readinessScore"] != "6.2" {
		t.Fatalf("readinessScore=%q, want the prior score untouched", stored.Metadata["readinessScore"])
	}

	startAgentThreadFollowUpAsync = func(runApp *kanbanBoardApp, run agentThreadFollowUpRun) {
		runApp.runAgentThreadFollowUpWithResponder(run, func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
			return "# Nimbus pressure test\n\nREADINESS: 7.1/10\n\nThe revision recovered successfully.", nil
		})
	}
	if _, err := app.launchAgentThreadFollowUp(artifact.ID, "retry after recovery", "Tim", nil); err != nil {
		t.Fatalf("launch successful retry: %v", err)
	}
	stored, _ = app.osArtifactByID(artifact.ID)
	if stored.Metadata["followUpStatus"] != "complete" || stored.Metadata["progressNote"] != "" || stored.Metadata["followUpError"] != "" {
		t.Fatalf("successful retry retained failed revision state: status=%q note=%q error=%q", stored.Metadata["followUpStatus"], stored.Metadata["progressNote"], stored.Metadata["followUpError"])
	}
}

// An installed Anthropic key cannot displace the founder-selected OpenAI
// follow-up route or alter its provenance.
func TestFollowUpIgnoresAnthropicKeyAndUsesOpenAI(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-test-key"
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")

	swapAnthropicTextResponder(t, func(_ context.Context, apiKey string, request anthropicTextRequest) (string, error) {
		t.Fatal("Anthropic responder must not run")
		return "", nil
	})

	var got openAITextRequest
	previousAsync := startAgentThreadFollowUpAsync
	startAgentThreadFollowUpAsync = func(runApp *kanbanBoardApp, run agentThreadFollowUpRun) {
		runApp.runAgentThreadFollowUpWithResponder(run, func(_ context.Context, apiKey string, request openAITextRequest) (string, error) {
			if apiKey != "openai-test-key" {
				t.Fatalf("apiKey=%q, want OpenAI key", apiKey)
			}
			got = request
			return "READINESS: 7.1/10\n\nWhat changed in v2: pricing objection resolved.", nil
		})
	}
	t.Cleanup(func() { startAgentThreadFollowUpAsync = previousAsync })

	artifact := seedCompleteGrillArtifact(t, app)
	if _, err := app.launchAgentThreadFollowUp(artifact.ID, "re-grill with the new pricing answers", "Tim", nil); err != nil {
		t.Fatalf("launchAgentThreadFollowUp: %v", err)
	}

	if got.Model != agentThreadTextModel(scoutAgentThread{Mode: "grill"}) {
		t.Fatalf("model=%q, want the OpenAI thread model", got.Model)
	}
	if got.Seat != seatFollowup || got.Workflow != "agent_thread_followup_grill" {
		t.Fatalf("follow-up provenance seat/workflow=%q/%q", got.Seat, got.Workflow)
	}
	if !strings.Contains(got.Instructions, "What changed in v2") || !strings.Contains(got.Instructions, "READINESS") {
		t.Fatalf("OpenAI instructions missing the follow-up contract: %q", got.Instructions)
	}
	if !strings.Contains(got.Input, "Follow-up request: re-grill with the new pricing answers") {
		t.Fatalf("OpenAI input missing the follow-up request: %q", got.Input)
	}

	stored, ok := app.osArtifactByID(artifact.ID)
	if !ok {
		t.Fatalf("artifact %s disappeared", artifact.ID)
	}
	if stored.Metadata["worker"] != agentThreadWorkerOpenAI {
		t.Fatalf("worker=%q, want %q", stored.Metadata["worker"], agentThreadWorkerOpenAI)
	}
	if stored.Metadata["workerBoundary"] != "responses_artifact_writer" {
		t.Fatalf("workerBoundary=%q, want responses_artifact_writer", stored.Metadata["workerBoundary"])
	}
	if !strings.HasPrefix(stored.Text, "READINESS: 7.1/10") {
		t.Fatalf("text=%q, want the OpenAI output merged on top", stored.Text)
	}
	if stored.Metadata["threadVersion"] != "2" || stored.Metadata["readinessScore"] != "7.1" {
		t.Fatalf("metadata=%#v, want v2 with the new readiness score", stored.Metadata)
	}
}

func TestGovernedIdentityPanelChildKeepsBoundGeneralRoute(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-test-key"
	previousGoalStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousGoalStart })
	parent, err := launchConversationOwnedGoalForTest(t, app, goalLaunchSpec{
		Objective: "Build the governed Aurora investor package", CreatedBy: "aj@shareability.com",
		ToolTemplate: packagingStudioProcessID,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := mustGoalPlan(t, app, parent.Artifact.ID)
	engine := newGoalEngine(app)
	if err := engine.prepareGoalRoute(&plan, parent.Artifact.ID); err != nil {
		t.Fatal(err)
	}
	definition, ok := engine.resolvedProcess(&plan)
	if !ok {
		t.Fatal("packaging process did not resolve")
	}
	stage, ok := definition.stageByID("identity")
	if !ok {
		t.Fatal("identity direction stage missing")
	}
	plan.Subtasks = []goalSubtask{{
		ID: stage.ID, Title: stage.Title, Mode: processStageThreadMode(stage), Authority: codexJobAuthorityReadOnly,
		Runner: agentRunnerOpenAIText, Role: stage.Role, Status: subtaskPending,
	}}
	plan.State = goalStateExecute
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.updateOSArtifactWithMetadata(parent.Artifact.ID, "", parent.Artifact.Text, scoutParticipantName, map[string]string{"goalPlan": string(encoded)}); err != nil {
		t.Fatal(err)
	}
	receipt := plan.RouteReceipt
	origin := goalRouteChildBindingMetadata(&plan)
	previousAgentStart := startAgentThreadAsync
	startAgentThreadAsync = func(*kanbanBoardApp, scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousAgentStart })
	childSpec := agentThreadGoalSpec{
		Objective: "Create the visual identity and imagery direction for the investor deck", RequestedBy: receipt.Requester,
		Authority: codexJobAuthorityReadOnly, ParentGoalID: parent.Artifact.ID, SubtaskID: stage.ID,
		AssignedRunner: agentRunnerOpenAIText, OutputContract: stage.OutputContract, Deliverable: false,
		SourceMessageID: receipt.SourceMessageID, SourceMessageDigest: receipt.SourceMessageDigest,
		SourceWindowDigest: receipt.SourceWindowDigest, OperationID: receipt.OperationID,
		OperationBodyDigest: receipt.OperationBodyDigest, ParentGoalRouteDigest: receipt.Digest,
	}
	thread, err := app.launchGoalAgentThreadScaffold(processStageThreadMode(stage), "Create the visual identity and imagery direction for the investor deck", "AJ", origin, childSpec)
	if err != nil {
		t.Fatal(err)
	}
	plan.Subtasks[0].Status = subtaskRunning
	plan.Subtasks[0].ThreadID = thread.ID
	plan.Subtasks[0].ArtifactID = thread.Artifact.ID
	encoded, err = json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.updateOSArtifactWithMetadata(parent.Artifact.ID, "", parent.Artifact.Text, scoutParticipantName, map[string]string{"goalPlan": string(encoded)}); err != nil {
		t.Fatal(err)
	}
	if err := app.activateReservedGoalAgentThread(thread, childSpec, "AJ"); err != nil {
		t.Fatal(err)
	}
	thread.Artifact, _ = app.osArtifactByID(thread.Artifact.ID)
	if _, err := app.agentThreadProviderContext(context.Background(), thread); err != nil {
		t.Fatalf("governed image-direction child failed provider admission: %v", err)
	}
	if gotModel, gotEffort := agentThreadTextModel(thread), agentThreadTextReasoningEffort(thread); gotModel != defaultMeetingBrainModel || gotEffort != defaultMeetingBrainReasoningEffort {
		t.Fatalf("identity-panel route=%s/%s, want %s/%s", gotModel, gotEffort, defaultMeetingBrainModel, defaultMeetingBrainReasoningEffort)
	}
}

// An OpenAI follow-up failure rides the same restore path:
// body untouched, prior terminal status back verbatim, error stamped.
func TestFollowUpOpenAIErrorPreservesBodyAndStatus(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-test-key"
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	previousAsync := startAgentThreadFollowUpAsync
	startAgentThreadFollowUpAsync = func(runApp *kanbanBoardApp, run agentThreadFollowUpRun) {
		runApp.runAgentThreadFollowUpWithResponder(run, func(context.Context, string, openAITextRequest) (string, error) {
			return "", fmt.Errorf("OpenAI response was declined by safety classifiers")
		})
	}
	t.Cleanup(func() { startAgentThreadFollowUpAsync = previousAsync })

	artifact := seedCompleteGrillArtifact(t, app)
	if _, err := app.launchAgentThreadFollowUp(artifact.ID, "run again", "Tim", nil); err != nil {
		t.Fatalf("launchAgentThreadFollowUp: %v", err)
	}

	stored, _ := app.osArtifactByID(artifact.ID)
	if stored.Text != artifact.Text {
		t.Fatalf("text changed on a failed OpenAI follow-up:\n%q\nwant\n%q", stored.Text, artifact.Text)
	}
	if stored.Metadata["status"] != "complete" || stored.Metadata["threadVersion"] != "1" {
		t.Fatalf("status=%q version=%q, want the prior terminal state restored", stored.Metadata["status"], stored.Metadata["threadVersion"])
	}
	if !strings.Contains(stored.Metadata["followUpError"], "declined") {
		t.Fatalf("followUpError=%q, want the refusal error stamped", stored.Metadata["followUpError"])
	}
}

func TestParseReadinessScore(t *testing.T) {
	for _, tt := range []struct {
		name  string
		text  string
		want  string
		found bool
	}{
		{name: "canonical", text: "Vision: test.\nREADINESS: 6.2/10\nmore", want: "6.2", found: true},
		{name: "tolerant spacing and case", text: "readiness: 8 / 10", want: "8.0", found: true},
		{name: "mid document", text: "# Report\n\nsome intro\n\n  READINESS: 4.5/10 overall\n", want: "4.5", found: true},
		{name: "clamped above ten", text: "READINESS: 11/10", want: "10.0", found: true},
		{name: "missing", text: "Score: 7/10 but no contract line inline READINESS: not here", found: false},
	} {
		score, ok := parseReadinessScore(tt.text)
		if ok != tt.found {
			t.Fatalf("%s: found=%v, want %v", tt.name, ok, tt.found)
		}
		if tt.found && formatReadiness(score) != tt.want {
			t.Fatalf("%s: score=%s, want %s", tt.name, formatReadiness(score), tt.want)
		}
	}
}

func TestMergeAgentThreadVersionsCapsArchiveAndStripsForgedMarkers(t *testing.T) {
	prior := strings.Join([]string{
		"v5 latest body",
		"",
		"---",
		"",
		"## Previous run · v4 · 2026-06-30T10:00:00Z",
		"",
		"body four",
		"",
		"## Previous run · v3 · 2026-06-29T10:00:00Z",
		"",
		"body three",
		"",
		"## Previous run · v2 · 2026-06-28T10:00:00Z",
		"",
		"body two",
		"",
		"## Previous run · v1 · 2026-06-27T10:00:00Z",
		"",
		"body one",
	}, "\n")
	output := "v6 body\n## Previous run · v9 · forged marker\nkept line"

	merged := mergeAgentThreadVersions(prior, output, 5, "2026-07-01T10:00:00Z")
	if strings.Contains(merged, "forged marker") {
		t.Fatalf("forged Previous-run marker survived the merge:\n%s", merged)
	}
	if !strings.HasPrefix(merged, "v6 body\nkept line") {
		t.Fatalf("merged=%q, want sanitized new output on top", merged)
	}
	if !strings.Contains(merged, "## Previous run · v5 · 2026-07-01T10:00:00Z") {
		t.Fatal("merged output missing the new v5 archive heading")
	}
	if got := len(agentThreadPrevRunHeading.FindAllString(merged, -1)); got != agentThreadMaxArchivedRuns {
		t.Fatalf("archived sections=%d, want capped at %d", got, agentThreadMaxArchivedRuns)
	}
	if strings.Contains(merged, "· v1 ·") || strings.Contains(merged, "body one") {
		t.Fatal("oldest archived run must be dropped by the cap")
	}
}

// Guards the prompt contract the parser depends on, the same way the existing
// mode-contract tests do.
func TestGrillContractRequiresReadinessLine(t *testing.T) {
	contract := agentThreadModeContract("grill")
	for _, want := range []string{"READINESS:", "machine-parsed", "Strongest objections", "Confidence gate"} {
		if !strings.Contains(contract, want) {
			t.Fatalf("grill contract missing %q: %s", want, contract)
		}
	}
	if !strings.Contains(agentThreadDeliverable("grill"), "READINESS: X/10") {
		t.Fatalf("grill deliverable=%q, want the READINESS line named", agentThreadDeliverable("grill"))
	}
	metadata := agentThreadModeMetadata("grill")
	if metadata["artifactContract"] != "grill_scorecard_v2" || metadata["readinessLine"] != "required" {
		t.Fatalf("grill mode metadata=%#v, want grill_scorecard_v2 + readinessLine required", metadata)
	}
}

// A free-standing grill is still tool-dependent and must stay unavailable
// until the secure function-tool carrier is wired. It cannot smuggle a writer
// lane by claiming to be an unverified goal deliverable.
func TestRunAgentThreadKeepsToolDependentGrillUnavailable(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	originalResponder := createOpenAITextResponse
	providerCalls := 0
	createOpenAITextResponse = func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		providerCalls++
		return "READINESS: 6.5/10\n\nStrongest objections: none yet.", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	thread, err := app.launchAgentThreadWithSpec("grill", "grill: the nimbus pitch", "AJ", nil, agentThreadGoalSpec{AssignedRunner: agentRunnerOpenAIText})
	if err != nil {
		t.Fatalf("launchAgentThread: %v", err)
	}
	app.runAgentThread(thread)

	stored, _ := app.osArtifactByID(thread.Artifact.ID)
	if providerCalls != 0 || stored.Metadata["readinessScore"] != "" || agentThreadStatusValue(stored) != "error" {
		t.Fatalf("tool-dependent grill widened admission: providerCalls=%d status=%q readiness=%q", providerCalls, agentThreadStatusValue(stored), stored.Metadata["readinessScore"])
	}
}

// followUpArtifactId is explicit Scout engagement: it launches in a PUBLIC
// channel without @scout, persists the reply with author identity, and
// rejects artifacts that are not referenced in the thread.
func TestChannelFollowUpMessageLaunchesWithoutMention(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.mu.Lock()
	kanbanApp.apiKey = "test-key"
	kanbanApp.mu.Unlock()
	t.Cleanup(func() { kanbanApp = previousApp })

	previousAsync := startAgentThreadFollowUpAsync
	startAgentThreadFollowUpAsync = func(_ *kanbanBoardApp, _ agentThreadFollowUpRun) {}
	t.Cleanup(func() { startAgentThreadFollowUpAsync = previousAsync })

	artifact := seedCompleteGrillArtifact(t, kanbanApp)
	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "launch plan", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages(channel.OwnerEmail, channel.ID, scoutChatMessageRecord{
		ID:        "scout-chat-message-card",
		Kind:      "thread",
		Role:      "scout",
		Text:      "pressure test thread launched",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Thread:    &scoutChatThreadRef{ID: "agent-thread-grill-1", Mode: "grill", Query: "grill: the nimbus pitch", Status: "complete", ArtifactID: artifact.ID},
	}); err != nil {
		t.Fatalf("seed channel card: %v", err)
	}

	user := accountStore().findUser("tim@shareability.com")
	if user == nil {
		t.Fatal("seed user tim@shareability.com missing")
	}

	// An artifact id that resolves to nothing is rejected outright — Gate A
	// only adds cards for real deliverables.
	if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, channel.ID, "answers inline", nil, "os-artifact-grill-unknown"); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("err=%v, want unknown-artifact rejection", err)
	}

	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, channel.ID, "pricing landed at $99 with two design partners", nil, artifact.ID)
	if err != nil {
		t.Fatalf("append follow-up message: %v", err)
	}
	agentThread, ok := response["agentThread"].(scoutAgentThread)
	if !ok {
		t.Fatalf("response keys=%v, want agentThread launched without @scout", responseKeys(response))
	}
	if agentThread.Artifact.ID != artifact.ID {
		t.Fatalf("agentThread artifact=%q, want the SAME artifact %q", agentThread.Artifact.ID, artifact.ID)
	}
	if agentThread.Artifact.Metadata["threadVersion"] != "2" {
		t.Fatalf("threadVersion=%q, want 2", agentThread.Artifact.Metadata["threadVersion"])
	}

	saved := response["thread"].(scoutChatThreadRecord)
	var userMessage, statusMessage *scoutChatMessageRecord
	for index := range saved.Messages {
		message := saved.Messages[index]
		if message.Role == "user" {
			userMessage = &saved.Messages[index]
		}
		if message.Role == "scout" && strings.Contains(message.Text, "follow-up v2 running") {
			statusMessage = &saved.Messages[index]
		}
	}
	if userMessage == nil || userMessage.AuthorEmail != "tim@shareability.com" || userMessage.AuthorName == "" {
		t.Fatalf("user message=%#v, want author identity persisted", userMessage)
	}
	if statusMessage == nil || statusMessage.Kind != "message" {
		t.Fatalf("status message=%#v, want a plain scout status line (no second thread card)", statusMessage)
	}
}

func TestConversationFollowUpLostResponseReconcilesExactlyOnce(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	previousAsync := startAgentThreadFollowUpAsync
	launches := 0
	startAgentThreadFollowUpAsync = func(_ *kanbanBoardApp, _ agentThreadFollowUpRun) { launches++ }
	t.Cleanup(func() { startAgentThreadFollowUpAsync = previousAsync })

	artifact := seedCompleteGrillArtifact(t, app)
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Deck feedback", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.commitScoutChatThreadMessages(thread.OwnerEmail, thread.ID, scoutChatMessageRecord{
		ID: "followup-origin-card", Kind: "thread", Role: "scout", Text: "Presentation ready",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Thread:    &scoutChatThreadRef{ID: "agent-thread-grill-1", Mode: "grill", Query: "grill: the nimbus pitch", Status: "complete", ArtifactID: artifact.ID},
	}); err != nil {
		t.Fatal(err)
	}
	user := accountStore().findUser("tim@shareability.com")
	operation := conversationTurnOperation{ID: "conversation-followup-lost-response-0001", BodyDigest: sha256Hex([]byte("exact follow-up request"))}
	ctx := withConversationTurnOperation(context.Background(), operation)

	conversationFollowUpBeforeCardCommitProbe = func(_ scoutAgentThread) error {
		conversationFollowUpBeforeCardCommitProbe = nil
		return errors.New("simulated lost response")
	}
	t.Cleanup(func() { conversationFollowUpBeforeCardCommitProbe = nil })
	if _, err := app.appendScoutChatThreadMessage(ctx, user, thread.ID, "make the closing slide more visual", nil, artifact.ID); err == nil || !strings.Contains(err.Error(), "projection needs reconciliation") {
		t.Fatalf("lost-response boundary err=%v", err)
	}
	if launches != 1 {
		t.Fatalf("provider launches=%d, want 1", launches)
	}
	stored, ok := app.osArtifactByID(artifact.ID)
	if !ok {
		t.Fatal("follow-up artifact disappeared")
	}
	receipts, err := conversationFollowUpReceipts(stored.Metadata)
	if err != nil || len(receipts) != 1 || receipts[0].OperationID != operation.ID || receipts[0].TargetArtifactID != artifact.ID {
		t.Fatalf("durable receipts=%+v err=%v", receipts, err)
	}

	changed := operation
	changed.BodyDigest = sha256Hex([]byte("changed follow-up request"))
	if _, err := app.appendScoutChatThreadMessage(withConversationTurnOperation(context.Background(), changed), user, thread.ID, "make the closing slide more visual", nil, artifact.ID); !errors.Is(err, ErrSTRIDEConversationConflict) {
		t.Fatalf("changed-body retry err=%v, want conversation conflict", err)
	}
	if launches != 1 {
		t.Fatalf("changed-body retry launched providers=%d", launches)
	}

	restarted, err := newMeetingMemoryStore(app.memory.path)
	if err != nil {
		t.Fatalf("restart memory: %v", err)
	}
	app.memory = restarted
	reconciled, err := app.appendScoutChatThreadMessage(ctx, user, thread.ID, "make the closing slide more visual", nil, artifact.ID)
	if err != nil {
		t.Fatalf("exact restart retry: %v", err)
	}
	if reconciled["reconciled"] != true || launches != 1 {
		t.Fatalf("reconciled=%v launches=%d", reconciled["reconciled"], launches)
	}
	replayed, err := app.appendScoutChatThreadMessage(ctx, user, thread.ID, "make the closing slide more visual", nil, artifact.ID)
	if err != nil || replayed["replayed"] != true || launches != 1 {
		t.Fatalf("second exact replay response=%v err=%v launches=%d", replayed, err, launches)
	}
	saved := replayed["thread"].(scoutChatThreadRecord)
	statusLines := 0
	for _, message := range saved.Messages {
		if message.CausedByMessageID == "scout-chat-message-"+sha256Hex([]byte("conversation-turn/v1\x00" + normalizeAccountEmail(user.Email) + "\x00" + thread.ID + "\x00" + operation.ID))[:24] {
			statusLines++
		}
	}
	if statusLines != 1 {
		t.Fatalf("follow-up status lines=%d, want exactly one", statusLines)
	}
}

// The headless endpoint (package binder / library) launches a follow-up for
// any signed-in user with the same origin+session gates as /assistant/threads.
func TestAssistantThreadFollowUpEndpoint(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.mu.Lock()
	kanbanApp.apiKey = "test-key"
	kanbanApp.mu.Unlock()
	t.Cleanup(func() { kanbanApp = previousApp })

	previousAsync := startAgentThreadFollowUpAsync
	startAgentThreadFollowUpAsync = func(_ *kanbanBoardApp, _ agentThreadFollowUpRun) {}
	t.Cleanup(func() { startAgentThreadFollowUpAsync = previousAsync })

	artifact := seedCompleteGrillArtifact(t, kanbanApp)

	// No session: rejected before touching the artifact.
	anonRecorder := httptest.NewRecorder()
	assistantThreadFollowUpHandler(anonRecorder, httptest.NewRequest(http.MethodPost, "/assistant/threads/follow-up", strings.NewReader(`{"artifactId":"x","text":"y"}`)))
	if anonRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("anon status=%d, want %d", anonRecorder.Code, http.StatusUnauthorized)
	}

	request := httptest.NewRequest(http.MethodPost, "/assistant/threads/follow-up", strings.NewReader(fmt.Sprintf(`{"artifactId":%q,"text":"re-run with the new numbers"}`, artifact.ID)))
	for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantThreadFollowUpHandler(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusAccepted)
	}
	var payload struct {
		OK       bool               `json:"ok"`
		Artifact meetingMemoryEntry `json:"artifact"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.OK || payload.Artifact.ID != artifact.ID {
		t.Fatalf("payload=%+v, want ok + the same artifact id", payload)
	}
	stored, _ := kanbanApp.osArtifactByID(artifact.ID)
	if stored.Metadata["threadStatus"] != "running" || stored.Metadata["threadVersion"] != "2" {
		t.Fatalf("metadata=%#v, want running v2 after the endpoint launch", stored.Metadata)
	}
}

func TestSharedFollowUpRejectsOriginalRequestersPrivateFileForCurrentCoworker(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-openai-key"
	t.Setenv("ANTHROPIC_API_KEY", "")
	aj := accountStore().findUser("aj@shareability.com")
	tim := accountStore().findUser("tim@shareability.com")
	if aj == nil || tim == nil {
		t.Fatal("seed users missing")
	}
	channel, _, err := app.ensureScoutChatThread("followup-current-human-channel", aj.Email, aj.Name, "Current human", scoutChatVisibilityPublic, []string{tim.Email})
	if err != nil {
		t.Fatal(err)
	}
	private, _, err := app.memory.appendEntry(meetingMemoryKindFile, "followup-aj-private-file", "AJ-ONLY-SOURCE-CANARY", map[string]string{
		"name": "AJ private source.txt", "origin": "files", "brainStatus": fileBrainStatusIngested,
		"visibility": "private", "ownerEmail": aj.Email, "tenantId": canonicalArtifactTenantID(),
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact, _, err := app.createOSArtifactWithMetadata("research", "Shared research", "# Initial report", aj.Name, map[string]string{
		"source": "scout_thread", "threadId": "agent-thread-current-human-source", "threadQuery": "Shared research",
		"requestedBy": aj.Email, "createdBy": aj.Email, "originKind": agentThreadOriginChannel, "originId": channel.ID,
		"contextRefs": encodeAssistantContextRefs([]string{assistantFileContextRef(private.ID)}),
		"status":      "complete", "threadStatus": "complete", "goalStatus": "verified", "threadVersion": "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	var capturedRun agentThreadFollowUpRun
	previousAsync := startAgentThreadFollowUpAsync
	startAgentThreadFollowUpAsync = func(_ *kanbanBoardApp, run agentThreadFollowUpRun) { capturedRun = run }
	t.Cleanup(func() { startAgentThreadFollowUpAsync = previousAsync })

	if _, err := app.dispatchAuthorizedArtifactFollowUpWithAttachments(context.Background(), tim, artifact, "tighten this", tim.Name, nil, channel, nil, ""); err != nil {
		t.Fatal(err)
	}
	providerCalls := 0
	app.runAgentThreadFollowUpWithResponder(capturedRun, func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		providerCalls++
		return "provider must not run", nil
	})
	if providerCalls != 0 {
		t.Fatalf("provider calls=%d, want zero after current-human source reauthorization", providerCalls)
	}
	stored, ok := app.osArtifactByID(artifact.ID)
	if !ok || !strings.Contains(stored.Metadata["followUpError"], "referenced File") || strings.Contains(stored.Text, "provider must not run") {
		t.Fatalf("source-denied follow-up=%+v", stored)
	}
}

func TestFollowUpAttachmentRevokedBeforeProviderMakesZeroCalls(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-openai-key"
	t.Setenv("ANTHROPIC_API_KEY", "")
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	destination, err := app.createScoutChatThread(user.Email, user.Name, "Revoked follow-up", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := putBlob(tinyPNG(t), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	const reservationID = "followup-provider-admission-revocation"
	file := reserveTestAttachment(t, app, user, destination, scoutChatFileAttachment{Name: "decision.png", Kind: "png", Ref: ref}, reservationID)
	artifact, _, err := app.createOSArtifactWithMetadata("research", "Review the decision image", "# Initial report", user.Name, map[string]string{
		"source": "scout_thread", "threadId": "agent-thread-revoked-attachment", "threadQuery": "Review the decision image",
		"requestedBy": user.Email, "createdBy": user.Email, "originKind": agentThreadOriginPrivateThread, "originId": destination.ID,
		"status": "complete", "threadStatus": "complete", "goalStatus": "verified", "threadVersion": "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	var capturedRun agentThreadFollowUpRun
	previousAsync := startAgentThreadFollowUpAsync
	startAgentThreadFollowUpAsync = func(_ *kanbanBoardApp, run agentThreadFollowUpRun) { capturedRun = run }
	t.Cleanup(func() { startAgentThreadFollowUpAsync = previousAsync })

	if _, err := app.dispatchAuthorizedArtifactFollowUpWithAttachments(context.Background(), user, artifact, "use this image", user.Name, nil, destination, []scoutChatFileAttachment{file}, reservationID); err != nil {
		t.Fatal(err)
	}
	if err := app.revokeAttachmentSource(file.SourceID); err != nil {
		t.Fatal(err)
	}
	providerCalls := 0
	app.runAgentThreadFollowUpWithResponder(capturedRun, func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		providerCalls++
		return "provider must not run", nil
	})
	if providerCalls != 0 {
		t.Fatalf("provider calls=%d, want zero after source revocation", providerCalls)
	}
	stored, ok := app.osArtifactByID(artifact.ID)
	if !ok || !strings.Contains(stored.Metadata["followUpError"], "attachment authorization changed") || stored.Text != artifact.Text {
		t.Fatalf("revoked attachment follow-up=%+v", stored)
	}
}

func TestFollowUpAttachmentCommittedBeforeAsyncWorkerStillAuthorizes(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	app.apiKey = "test-openai-key"
	t.Setenv("ANTHROPIC_API_KEY", "")
	user := accountStore().findUser("aj@shareability.com")
	destination, err := app.createScoutChatThread(user.Email, user.Name, "Committed follow-up", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := putBlob(tinyPNG(t), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	file := grantTestPendingAttachment(t, app, user, destination, ref)
	file.Name, file.Kind = "decision.png", "png"
	artifact, _, err := app.createOSArtifactWithMetadata("research", "Review the decision image", "# Initial report", user.Name, map[string]string{
		"source": "scout_thread", "threadId": "agent-thread-committed-attachment", "threadQuery": "Review the decision image",
		"requestedBy": user.Email, "createdBy": user.Email, "originKind": agentThreadOriginPrivateThread, "originId": destination.ID,
		"status": "complete", "threadStatus": "complete", "goalStatus": "verified", "threadVersion": "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	var capturedRun agentThreadFollowUpRun
	previousAsync := startAgentThreadFollowUpAsync
	startAgentThreadFollowUpAsync = func(_ *kanbanBoardApp, run agentThreadFollowUpRun) { capturedRun = run }
	t.Cleanup(func() { startAgentThreadFollowUpAsync = previousAsync })
	if _, err := app.appendScoutChatThreadMessage(context.Background(), user, destination.ID, "use this image", []scoutChatFileAttachment{file}, artifact.ID); err != nil {
		t.Fatal(err)
	}
	app.pendingAttachmentUploadsMu.Lock()
	grant := app.pendingAttachmentUploads[file.SourceID]
	app.pendingAttachmentUploadsMu.Unlock()
	if grant.State != attachmentSourceCommitted || grant.CommittedMessageID == "" {
		t.Fatalf("follow-up source was not committed before worker: %+v", grant)
	}
	providerCalls := 0
	var capturedRequest openAITextRequest
	app.runAgentThreadFollowUpWithResponder(capturedRun, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		providerCalls++
		capturedRequest = request
		return completeResearchArtifactForTest() + "\n\nI used the authorized attachment.", nil
	})
	stored, ok := app.osArtifactByID(artifact.ID)
	if providerCalls != 1 || !ok || stored.Metadata["status"] != "complete" || !strings.Contains(stored.Text, "authorized attachment") {
		t.Fatalf("committed attachment follow-up calls=%d stored=%+v", providerCalls, stored)
	}
	if len(capturedRequest.Attachments) != 1 || capturedRequest.Attachments[0].Type != "input_image" ||
		!strings.HasPrefix(capturedRequest.Attachments[0].ImageURL, "data:image/png;base64,") {
		t.Fatalf("OpenAI follow-up attachments=%+v, want one authorized input_image", capturedRequest.Attachments)
	}
}

// Wave 6 Gate B: a follow-up on a GOAL deliverable (here the goal card itself,
// dropped into a fresh public channel while the goal is parked at its
// checkpoint) routes to the goal engine as a feedback-driven send-back — the
// worker re-runs carrying the note — instead of the old hard reject. The
// added chat ref carries Mode "goal" keyed by the goal's thread id.
func TestFollowUpOnGoalDeliverableResumesGoalWithFeedback(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	installFakeResponder(t, goalResponderRoutes{})
	launched := installFakeChildRunner(t)
	registerReviseProbeForTest(t, "process_feedback_probe")

	// The feedback drive (real model calls in production) runs off the chat
	// request goroutine; capture it and run it synchronously below.
	var pendingDrive func()
	previousResume := startGoalFeedbackResumeAsync
	startGoalFeedbackResumeAsync = func(run func()) { pendingDrive = run }
	t.Cleanup(func() { startGoalFeedbackResumeAsync = previousResume })

	thread, err := launchConversationOwnedGoalForTest(t, kanbanApp, goalLaunchSpec{
		Objective:    "Probe the feedback door",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "process_feedback_probe",
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	kanbanApp.runGoalThread(thread.Artifact.ID)
	parked := waitForGoalStage(t, kanbanApp, thread.Artifact.ID, goalStateApproval)
	// The approval projection is persisted before the folding child releases the
	// goal mutex. Join that exact drive before exercising the request-path
	// TryLock: a genuinely parked goal is idle, while racing the tail of its
	// terminal fold would only test scheduler timing.
	goalLock := goalEngineLock(thread.Artifact.ID)
	goalLock.Lock()
	goalLock.Unlock()
	parent, _ := kanbanApp.osArtifactByID(thread.Artifact.ID)
	writer := parked.subtaskByID("w1")
	if writer == nil || writer.ArtifactID == "" {
		t.Fatalf("parked goal has no governed writer child: %+v", writer)
	}

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "feedback drop zone", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user tim@shareability.com missing")
	}

	followUpOperation := conversationTurnOperation{ID: "goal-child-followup-0001", BodyDigest: sha256Hex([]byte("tighten the close"))}
	response, err := kanbanApp.appendScoutChatThreadMessage(withConversationTurnOperation(context.Background(), followUpOperation), user, channel.ID, "tighten the close", nil, writer.ArtifactID)
	if err != nil {
		t.Fatalf("drop goal deliverable: %v", err)
	}
	agentThread, ok := response["agentThread"].(scoutAgentThread)
	if !ok || agentThread.Mode != "goal" || agentThread.Artifact.ID != parent.ID {
		t.Fatalf("response agentThread=%#v, want a goal-mode resume on the parent", response["agentThread"])
	}
	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || !strings.Contains(answer.Text, "feedback") {
		t.Fatalf("answer=%#v, want a goal-flavored feedback status line", response["answer"])
	}

	// Gate A added the goal card to the fresh channel, keyed for goal flips.
	saved, _, err := kanbanApp.scoutChatThreadByID(channel.OwnerEmail, channel.ID)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	var ref *scoutChatThreadRef
	for index := range saved.Messages {
		if saved.Messages[index].Kind == "thread" && saved.Messages[index].Thread != nil {
			ref = saved.Messages[index].Thread
		}
	}
	if ref == nil || ref.ArtifactID != parent.ID {
		t.Fatalf("ref=%#v, want the goal card added for the dropped deliverable", ref)
	}
	if ref.Mode != "goal" {
		t.Fatalf("ref.Mode=%q, want goal so the client mounts the live goalcard", ref.Mode)
	}
	if ref.ID != parent.Metadata["threadId"] {
		t.Fatalf("ref.ID=%q, want the goal thread id %q", ref.ID, parent.Metadata["threadId"])
	}

	// The captured drive is the send-back: the writer re-runs with the note.
	if pendingDrive == nil {
		t.Fatal("feedback resume never scheduled its drive")
	}
	before := len(*launched)
	pendingDrive()
	plan := waitForGoalStage(t, kanbanApp, thread.Artifact.ID, goalStateApproval)
	if pass := plan.subtaskByID("pass"); pass == nil || pass.Revisions != 1 {
		t.Fatalf("checkpoint stage=%+v, want one send-back round spent and re-parked", plan.subtaskByID("pass"))
	}
	if len(*launched) <= before {
		t.Fatalf("feedback did not re-dispatch the writer (launched %d -> %d)", before, len(*launched))
	}
	redo := (*launched)[len(*launched)-1]
	if redo.subtaskID != "w1" || !strings.Contains(redo.query, "tighten the close") {
		t.Fatalf("redo child=%+v, want w1 re-run carrying the feedback note", redo)
	}
}

// Wave 6 Gate B guard-rails: deliverables with no goal linkage (an imagery
// board, a plain hand-saved artifact) reject with an honest error instead of
// falling into the agent-thread path's misleading "agent thread reports"
// message — and the legacy no-source reject stays intact.
func TestFollowUpDispatchUnlinkedArtifactRejected(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	imagery, _, err := app.createOSArtifactWithMetadata("design", "imagery board", "four hero frames", "AJ", map[string]string{
		"source":       "imagery_board",
		"status":       "complete",
		"threadStatus": "complete",
	})
	if err != nil {
		t.Fatalf("seed imagery artifact: %v", err)
	}
	if _, err := app.dispatchArtifactFollowUp(imagery.ID, "make it warmer", "Tim", nil); err == nil || !strings.Contains(err.Error(), "feedback") {
		t.Fatalf("err=%v, want an unlinked-deliverable rejection naming feedback", err)
	}

	plain, _, err := app.createOSArtifactWithMetadata("artifacts", "meeting notes", "notes body", "AJ", map[string]string{
		"status": "complete",
	})
	if err != nil {
		t.Fatalf("seed plain artifact: %v", err)
	}
	if _, err := app.dispatchArtifactFollowUp(plain.ID, "expand these", "Tim", nil); err == nil {
		t.Fatal("plain no-source artifact must reject, not route")
	}
}
