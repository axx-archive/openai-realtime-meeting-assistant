package main

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
)

func TestScoutInsightsReportDecisionIsOneSourceBoundResearchArtifact(t *testing.T) {
	request := "Create an Insights & Opportunities report about the market opportunity for a western-culture engagement army of thousands of on-demand creators."
	decision, ok := scoutInsightsReportDecision(request)
	if !ok || decision.validate() != nil || decision.Outcome != conversationIntentStartPrivateWork || decision.Work == nil {
		t.Fatalf("decision=%+v ok=%t", decision, ok)
	}
	work := decision.Work
	if work.Kind != conversationWorkWorkstream || work.Mode != "research" || work.Authority != toolAuthorityReadOnly {
		t.Fatalf("work=%+v", work)
	}
	for _, want := range []string{
		"private, editable Markdown Insights & Opportunities report",
		"Company Brain context",
		"external research could materially change",
		"evidence and counterevidence",
		"30/60/90-day tests",
		"opt-in ambassador and creator community",
	} {
		if !strings.Contains(work.Objective, want) {
			t.Errorf("objective missing %q:\n%s", want, work.Objective)
		}
	}
	if got := conversationWorkVisibleLabel(*work, "Research"); got != "Insights & Opportunities report" {
		t.Fatalf("visible label=%q", got)
	}
	if scoutInsightsReportRequestDetected("What is an Insights & Opportunities report?") {
		t.Fatal("format question was mistaken for an artifact request")
	}
}

func TestPrivateScoutInsightsReportStartsExactlyOnceAndStaysPrivate(t *testing.T) {
	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerOpenAIText)
	fixture := newSTRIDEProjectAuthorityFixture(t)
	fixture.app.apiKey = "router-not-needed-for-deterministic-report"
	thread, err := fixture.app.createScoutChatThread(fixture.user.Email, fixture.user.Name, "Private report", "")
	if err != nil {
		t.Fatal(err)
	}
	request := "Create an Insights & Opportunities report about the market opportunity for a western culture engagement army of thousands of creators posting on demand."
	previousRunner := startAgentThreadAsync
	var launches atomic.Int64
	var launched scoutAgentThread
	startAgentThreadAsync = func(_ *kanbanBoardApp, candidate scoutAgentThread) {
		launches.Add(1)
		launched = candidate
	}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	response, err := fixture.app.appendScoutChatThreadMessage(context.Background(), fixture.user, thread.ID, request, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if launches.Load() != 1 || response["intentOutcome"] != string(conversationIntentStartPrivateWork) || response["proposal"] != nil {
		t.Fatalf("launches=%d response=%#v", launches.Load(), response)
	}
	metadata := launched.Artifact.Metadata
	if launched.Mode != "research" || metadata["visibility"] != scoutChatVisibilityPrivate || normalizeAccountEmail(metadata["ownerEmail"]) != normalizeAccountEmail(fixture.user.Email) ||
		normalizeAccountEmail(metadata["requestedBy"]) != normalizeAccountEmail(fixture.user.Email) || metadata["originKind"] != agentThreadOriginPrivateThread || metadata["originSurface"] != "chat:"+thread.ID {
		t.Fatalf("launched=%+v metadata=%v", launched, metadata)
	}
	if !strings.Contains(launched.Query, "private, editable Markdown Insights & Opportunities report") {
		t.Fatalf("worker lost report contract: %q", launched.Query)
	}
	saved, ok := response["thread"].(scoutChatThreadRecord)
	if !ok || len(saved.Messages) != 2 || saved.Messages[1].Thread == nil || saved.Messages[1].Thread.ArtifactID != launched.Artifact.ID || saved.Messages[1].Text != "Insights & Opportunities report in progress" {
		t.Fatalf("saved private report card=%#v", response["thread"])
	}

	// Replaying the same accepted turn operation repairs/reuses the durable work
	// root; it must not start a second provider run.
	operation := conversationTurnOperation{ID: "private-insights-report-replay", BodyDigest: sha256Hex([]byte(request))}
	replayContext := withConversationTurnOperation(context.Background(), operation)
	secondThread, err := fixture.app.createScoutChatThread(fixture.user.Email, fixture.user.Name, "Private report replay", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.app.appendScoutChatThreadMessage(replayContext, fixture.user, secondThread.ID, request, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.app.appendScoutChatThreadMessage(replayContext, fixture.user, secondThread.ID, request, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if launches.Load() != 2 {
		t.Fatalf("replay launched duplicate provider work: total launches=%d, want 2 across two distinct threads", launches.Load())
	}
}
