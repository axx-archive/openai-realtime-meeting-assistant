package main

import (
	"context"
	"encoding/json"
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

// The follow-up success seam runs the SAME terminal contract as the primary
// seam: on the error→follow-up-success path the linked board card advances to
// Done, the completed artifact attaches to its venture package, and the
// completion card is delivered back to the origin channel.
func TestFollowUpSuccessAdvancesCardAttachesPackageAndDeliversOrigin(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	t.Setenv("ANTHROPIC_API_KEY", "")

	previousAsync := startAgentThreadFollowUpAsync
	startAgentThreadFollowUpAsync = func(runApp *kanbanBoardApp, run agentThreadFollowUpRun) {
		runApp.runAgentThreadFollowUpWithResponder(run, func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
			return "Vision: recovered.\n\nWhat changed in v2: the worker error was resolved.", nil
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

	// v1 errored: the card sits in Blocked, nothing attached, nothing
	// delivered — exactly the state the primary error seam leaves behind.
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
	app.advanceLinkedCard(card.ID, kanbanStatusBlocked, "v1 worker error")
	if status := linkageCardStatus(t, app, card.ID); status != kanbanStatusBlocked {
		t.Fatalf("status=%q before the follow-up, want Blocked", status)
	}

	if _, err := app.launchAgentThreadFollowUp(artifact.ID, "try again with the fix", "Tim", nil); err != nil {
		t.Fatalf("launchAgentThreadFollowUp: %v", err)
	}

	// board auto-advance: the board stops lying about the recovered work.
	if status := linkageCardStatus(t, app, card.ID); status != kanbanStatusDone {
		t.Fatalf("status=%q after the follow-up success, want Done", status)
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
	if stored.Metadata["readinessScore"] != "6.2" {
		t.Fatalf("readinessScore=%q, want the prior score untouched", stored.Metadata["readinessScore"])
	}
}

// With an Anthropic key present the follow-up rides Sonnet 5 — re-baselined
// 3000-token budget at effort low, same instructions/input as the OpenAI
// path, honest worker stamp — and needs NO OpenAI key at all. The injected
// gpt-5.5 responder must never run.
func TestFollowUpRoutesToSonnetWithAnthropicKey(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("BONFIRE_CHAT_MODEL", "")

	var got anthropicTextRequest
	swapAnthropicTextResponder(t, func(_ context.Context, apiKey string, request anthropicTextRequest) (string, error) {
		if apiKey != "sk-ant-test" {
			t.Fatalf("apiKey=%q, want the Anthropic key", apiKey)
		}
		got = request
		return "READINESS: 7.1/10\n\nWhat changed in v2: pricing objection resolved.", nil
	})

	previousAsync := startAgentThreadFollowUpAsync
	startAgentThreadFollowUpAsync = func(runApp *kanbanBoardApp, run agentThreadFollowUpRun) {
		runApp.runAgentThreadFollowUpWithResponder(run, func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
			t.Fatal("OpenAI responder must not run when an Anthropic key is present")
			return "", nil
		})
	}
	t.Cleanup(func() { startAgentThreadFollowUpAsync = previousAsync })

	artifact := seedCompleteGrillArtifact(t, app)
	if _, err := app.launchAgentThreadFollowUp(artifact.ID, "re-grill with the new pricing answers", "Tim", nil); err != nil {
		t.Fatalf("launchAgentThreadFollowUp: %v", err)
	}

	if got.Model != "claude-sonnet-5" {
		t.Fatalf("model=%q, want claude-sonnet-5", got.Model)
	}
	if got.MaxTokens != 3000 || got.Effort != "low" {
		t.Fatalf("follow-up budget=%d/%q, want 3000/low", got.MaxTokens, got.Effort)
	}
	if !strings.Contains(got.Instructions, "What changed in v2") || !strings.Contains(got.Instructions, "READINESS") {
		t.Fatalf("Sonnet instructions missing the follow-up contract: %q", got.Instructions)
	}
	if !strings.Contains(got.Input, "Follow-up request: re-grill with the new pricing answers") {
		t.Fatalf("Sonnet input missing the follow-up request: %q", got.Input)
	}

	stored, ok := app.osArtifactByID(artifact.ID)
	if !ok {
		t.Fatalf("artifact %s disappeared", artifact.ID)
	}
	if stored.Metadata["worker"] != agentThreadWorkerAnthropic {
		t.Fatalf("worker=%q, want %q (the evidence stamp stays honest)", stored.Metadata["worker"], agentThreadWorkerAnthropic)
	}
	if stored.Metadata["workerBoundary"] != "anthropic_messages_artifact_writer" {
		t.Fatalf("workerBoundary=%q, want anthropic_messages_artifact_writer", stored.Metadata["workerBoundary"])
	}
	if !strings.HasPrefix(stored.Text, "READINESS: 7.1/10") {
		t.Fatalf("text=%q, want the Sonnet output merged on top", stored.Text)
	}
	if stored.Metadata["threadVersion"] != "2" || stored.Metadata["readinessScore"] != "7.1" {
		t.Fatalf("metadata=%#v, want v2 with the new readiness score", stored.Metadata)
	}
}

// A Sonnet follow-up failure rides the same restore path as an OpenAI one:
// body untouched, prior terminal status back verbatim, error stamped.
func TestFollowUpSonnetErrorPreservesBodyAndStatus(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")

	swapAnthropicTextResponder(t, func(context.Context, string, anthropicTextRequest) (string, error) {
		return "", fmt.Errorf("Anthropic chat request was declined by safety classifiers")
	})
	previousAsync := startAgentThreadFollowUpAsync
	startAgentThreadFollowUpAsync = func(runApp *kanbanBoardApp, run agentThreadFollowUpRun) {
		runApp.runAgentThreadFollowUpWithResponder(run, nil)
	}
	t.Cleanup(func() { startAgentThreadFollowUpAsync = previousAsync })

	artifact := seedCompleteGrillArtifact(t, app)
	if _, err := app.launchAgentThreadFollowUp(artifact.ID, "run again", "Tim", nil); err != nil {
		t.Fatalf("launchAgentThreadFollowUp: %v", err)
	}

	stored, _ := app.osArtifactByID(artifact.ID)
	if stored.Text != artifact.Text {
		t.Fatalf("text changed on a failed Sonnet follow-up:\n%q\nwant\n%q", stored.Text, artifact.Text)
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

// v1 grill completions through the text worker stamp their initial score so
// the first re-grill has a baseline to diff against.
func TestRunAgentThreadStampsInitialGrillReadiness(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		return "READINESS: 6.5/10\n\nStrongest objections: none yet.", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	thread, err := app.launchAgentThread("grill", "grill: the nimbus pitch", "AJ")
	if err != nil {
		t.Fatalf("launchAgentThread: %v", err)
	}
	app.runAgentThread(thread)

	stored, _ := app.osArtifactByID(thread.Artifact.ID)
	if stored.Metadata["readinessScore"] != "6.5" {
		t.Fatalf("readinessScore=%q, want 6.5 from the v1 run", stored.Metadata["readinessScore"])
	}
	if stored.Metadata["readinessDelta"] != "" {
		t.Fatalf("readinessDelta=%q, want no delta on the first run", stored.Metadata["readinessDelta"])
	}
	var runs []agentThreadRunLogEntry
	if err := json.Unmarshal([]byte(stored.Metadata["threadRuns"]), &runs); err != nil || len(runs) != 1 || runs[0].Version != 1 || runs[0].Score != "6.5" {
		t.Fatalf("threadRuns=%q (err=%v), want a single v1 entry with the score", stored.Metadata["threadRuns"], err)
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

	// An artifact with no card in this thread is rejected.
	if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, channel.ID, "answers inline", nil, "os-artifact-grill-unknown"); err == nil || !strings.Contains(err.Error(), "not in this thread") {
		t.Fatalf("err=%v, want not-in-this-thread rejection", err)
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
