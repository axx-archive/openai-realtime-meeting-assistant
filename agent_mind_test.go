package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAgentMindPositionPersistsEvolvesAndCanBeHumanResolved(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread := scoutChatThreadRecord{
		ID:           "ball-dogs",
		OwnerEmail:   "aj@shareability.com",
		Visibility:   scoutChatVisibilityPublic,
		MemberEmails: nil,
	}
	question := scoutChatMessageRecord{ID: "message-question-1", Role: "user", Text: "What do you think is the more attainable Ball Dogs strategy?"}
	firstAnswer := scoutChatMessageRecord{
		ID:   "message-answer-1",
		Role: "scout",
		Text: "My read is that the repeatable sports-pet short-form format is the more attainable wedge because it can prove audience and partner value before the company funds a larger character universe.",
	}
	app.maybeRecordScoutAgentMindPosition(thread, question, firstAnswer)

	positions := app.agentMindPositions(agentMindScoutID, "Ball Dogs")
	if len(positions) != 1 {
		t.Fatalf("positions=%#v, want one durable Scout position", positions)
	}
	first := positions[0]
	if first.Revision != 1 || first.Status != agentMindPositionStatusActive || first.SourceMessage != question.ID {
		t.Fatalf("first position=%+v, want active revision 1 sourced to the question", first)
	}
	if !strings.Contains(app.agentMindPositionPrompt(agentMindScoutID, "ball"), first.SourceThread) {
		t.Fatal("AgentMind prompt must retain source-linked context")
	}

	secondQuestion := question
	secondQuestion.ID = "message-question-2"
	secondAnswer := firstAnswer
	secondAnswer.ID = "message-answer-2"
	secondAnswer.Text = "My read has changed: start with the athlete-led game-day format, because a named first talent path is a cheaper proof point than asking a partner to underwrite a whole franchise thesis."
	secondThread := thread
	secondThread.ID = "ball-dogs-follow-up"
	app.maybeRecordScoutAgentMindPosition(secondThread, secondQuestion, secondAnswer)

	positions = app.agentMindPositions(agentMindScoutID, "strategy")
	if len(positions) != 1 || positions[0].Revision != 2 || positions[0].SourceThread != secondThread.ID || !strings.Contains(positions[0].Summary, "changed") {
		t.Fatalf("evolved positions=%#v, want one revision-2 position", positions)
	}
	prior := positions[0]
	resolved, appended, err := app.resolveAgentMindPosition(prior, agentMindPositionStatusCorrected, "Human review favors the athlete-led proof point, with the franchise claim still unproven.", "AJ", "message-review-1")
	if err != nil || !appended {
		t.Fatalf("resolveAgentMindPosition: record=%+v appended=%v err=%v", resolved, appended, err)
	}
	if resolved.Revision != 3 || resolved.Status != agentMindPositionStatusCorrected || resolved.Origin != agentMindPositionOriginReview {
		t.Fatalf("resolved position=%+v, want corrected human-review revision 3", resolved)
	}
	positions = app.agentMindPositions(agentMindScoutID, "strategy")
	if len(positions) != 1 || positions[0].Revision != 3 || positions[0].Status != agentMindPositionStatusCorrected {
		t.Fatalf("latest positions=%#v, want corrected revision 3", positions)
	}

	// The source-linked judgment survives a store reload rather than living only
	// in the process-local prompt assembly.
	reloaded, err := newMeetingMemoryStore(os.Getenv("MEETING_MEMORY_PATH"))
	if err != nil {
		t.Fatalf("reload AgentMind store: %v", err)
	}
	reloadedApp := &kanbanBoardApp{memory: reloaded}
	if got := reloadedApp.agentMindPositions(agentMindScoutID, "strategy"); len(got) != 1 || got[0].Revision != 3 {
		t.Fatalf("reloaded positions=%#v, want corrected revision 3", got)
	}
}

func TestAgentMindIgnoresNonJudgmentTurns(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread := scoutChatThreadRecord{ID: "general", OwnerEmail: "aj@shareability.com", Visibility: scoutChatVisibilityPublic}
	app.maybeRecordScoutAgentMindPosition(thread,
		scoutChatMessageRecord{ID: "message-1", Role: "user", Text: "Can you summarize the latest update?"},
		scoutChatMessageRecord{ID: "message-2", Role: "scout", Text: "The latest update is in the public channel."})
	if got := app.agentMindPositions(agentMindScoutID, ""); len(got) != 0 {
		t.Fatalf("positions=%#v, want no position for a summary turn", got)
	}
}

func TestAgentMindPositionExpiryIsNotPrompted(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	expires := time.Now().UTC().Add(-time.Minute)
	record := agentMindPositionRecord{
		ID:            "agent-mind-expired",
		PositionKey:   "scout:topic-ball-dogs:ball-dogs",
		AgentID:       agentMindScoutID,
		Subject:       "topic-ball-dogs",
		Scope:         "ball-dogs",
		Summary:       "Expired judgment",
		Status:        agentMindPositionStatusActive,
		Origin:        agentMindPositionOriginConversation,
		SourceThread:  "ball-dogs",
		SourceMessage: "message-1",
		SourceRefs:    []string{"thread:ball-dogs"},
		Confidence:    0.7,
		Revision:      1,
		ExpiresAt:     &expires,
		CreatedAt:     time.Now().UTC().Add(-2 * time.Minute),
		UpdatedAt:     time.Now().UTC().Add(-2 * time.Minute),
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal expired position: %v", err)
	}
	if _, appended, err := app.memory.appendAmbientEntry(meetingMemoryKindAgentMindPosition, record.ID, string(raw), map[string]string{"agentId": agentMindScoutID}); err != nil || !appended {
		t.Fatalf("record expired position: appended=%v err=%v", appended, err)
	}
	if got := app.agentMindPositions(agentMindScoutID, "ball"); len(got) != 0 || app.agentMindPositionPrompt(agentMindScoutID, "ball") != "" {
		t.Fatalf("expired position leaked into projection: %#v", got)
	}
}
