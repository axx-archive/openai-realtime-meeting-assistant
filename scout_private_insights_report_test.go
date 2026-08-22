package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
)

func TestScoutInsightsReportDecisionUsesProportionateDocumentProcess(t *testing.T) {
	request := "Create an Insights & Opportunities report about the market opportunity for a western-culture engagement army of thousands of on-demand creators."
	decision, ok := scoutInsightsReportDecision(request)
	if !ok || decision.validate() != nil || decision.Outcome != conversationIntentStartPrivateWork || decision.Work == nil {
		t.Fatalf("decision=%+v ok=%t", decision, ok)
	}
	work := decision.Work
	if work.Kind != conversationWorkRegistryTool || work.ToolID != documentReportProcessID || work.Authority != toolAuthorityWorkspaceWrite {
		t.Fatalf("work=%+v", work)
	}
	for _, want := range []string{
		"private, editable native Markdown document",
		"Company Brain context",
		"do not research by default",
		"coherent human argument",
		"evidence, counterevidence, opportunities, risks, and executable tests",
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

func TestScoutDocumentReportGuardKeepsLightweightAnswersImmediate(t *testing.T) {
	for _, request := range []string{
		"What are 10 ways to recruit western creators?",
		"Give me a list of campaign ideas",
		"What is a market opportunity report?",
		"I want to know what an Insights & Opportunities report is",
		"Should we create an Insights & Opportunities report?",
		"Do I need a strategy memo?",
		"How should we write the market report?",
		"Would a report help this decision?",
		"I need to know whether we should prepare a strategy document",
		"Analyze this paragraph",
		"Edit the report we already made",
		"Don't write a report; answer here.",
		"I never asked you to prepare a memo.",
		"Can you explain how to write a report?",
		"Could someone write a report?",
	} {
		if scoutDocumentReportRequestDetected(request) {
			t.Errorf("lightweight ask was routed to Document Studio: %q", request)
		}
	}
	for _, request := range []string{
		"Write a report on the western creator market",
		"Write a market report for the western creator opportunity",
		"Please prepare a strategy document for the launch decision",
		"Put together a memo about the creator pilot",
		"I need a strategy memo written for Friday",
		"Can you write a report on the western creator market?",
		"Could you please prepare a strategy document?",
	} {
		if !scoutDocumentReportRequestDetected(request) {
			t.Errorf("durable document ask stayed on the immediate path: %q", request)
		}
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
	previousRunner := startGoalThreadAsync
	var launches atomic.Int64
	startGoalThreadAsync = func(_ *kanbanBoardApp, _ string) {
		launches.Add(1)
	}
	t.Cleanup(func() { startGoalThreadAsync = previousRunner })

	initialOperation := conversationTurnOperation{ID: "private-insights-report-initial", BodyDigest: sha256Hex([]byte(request))}
	response, err := fixture.app.appendScoutChatThreadMessage(withConversationTurnOperation(context.Background(), initialOperation), fixture.user, thread.ID, request, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if launches.Load() != 1 || response["intentOutcome"] != string(conversationIntentStartPrivateWork) || response["proposal"] != nil {
		t.Fatalf("launches=%d response=%#v", launches.Load(), response)
	}
	launched, ok := response["agentThread"].(scoutAgentThread)
	if !ok {
		t.Fatalf("agentThread=%#v", response["agentThread"])
	}
	metadata := launched.Artifact.Metadata
	if launched.Mode != "goal" || metadata["visibility"] != scoutChatVisibilityPrivate || normalizeAccountEmail(metadata["ownerEmail"]) != normalizeAccountEmail(fixture.user.Email) ||
		normalizeAccountEmail(metadata["requestedBy"]) != normalizeAccountEmail(fixture.user.Email) || metadata["originKind"] != agentThreadOriginPrivateThread || metadata["originSurface"] != "chat:"+thread.ID {
		t.Fatalf("launched=%+v metadata=%v", launched, metadata)
	}
	if !strings.Contains(launched.Query, "private, editable native Markdown document") {
		t.Fatalf("worker lost report contract: %q", launched.Query)
	}
	var plan goalPlan
	if err := json.Unmarshal([]byte(metadata["goalPlan"]), &plan); err != nil || plan.ProcessID != documentReportProcessID {
		t.Fatalf("goal plan=%+v err=%v", plan, err)
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
		t.Fatalf("replay launched duplicate process goals: total launches=%d, want 2 across two distinct threads", launches.Load())
	}
}
