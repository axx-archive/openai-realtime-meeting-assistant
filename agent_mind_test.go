package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAgentMindPositionPersistsEvolvesAndCanBeHumanResolved(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "ball-dogs", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	question := scoutChatMessageRecord{ID: "message-question-1", Role: "user", Text: "What do you think is the more attainable Ball Dogs strategy?"}
	firstAnswer := scoutChatMessageRecord{
		ID:   "message-answer-1",
		Role: "scout",
		Text: "My read is that the repeatable sports-pet short-form format is the more attainable wedge because it can prove audience and partner value before the company funds a larger character universe.",
	}
	thread.Messages = []scoutChatMessageRecord{question, firstAnswer}
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
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
	secondThread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "ball-dogs-follow-up", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	secondThread.Messages = []scoutChatMessageRecord{secondQuestion, secondAnswer}
	if err := app.saveScoutChatThread(secondThread); err != nil {
		t.Fatal(err)
	}
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

func TestAgentMindLatestRevisionCannotBeResurrectedByQueryExpiryOrForgetting(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "agent-mind-revisions", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	question := scoutChatMessageRecord{ID: "question-1", Role: "user", AuthorEmail: "aj@shareability.com", Text: "What do you think is the better launch strategy?"}
	firstAnswer := scoutChatMessageRecord{ID: "answer-1", Role: "scout", Text: "My read is to lead with the old lighthouse concept because it gives the launch one memorable image and a clear proof point."}
	thread.Messages = []scoutChatMessageRecord{question, firstAnswer}
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	app.maybeRecordScoutAgentMindPosition(thread, question, firstAnswer)

	secondAnswer := scoutChatMessageRecord{ID: "answer-2", Role: "scout", Text: "My read has changed: lead with the athlete-led game-day format because it has a named distribution path and cheaper proof."}
	thread.Messages = []scoutChatMessageRecord{question, secondAnswer}
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	app.maybeRecordScoutAgentMindPosition(thread, question, secondAnswer)
	if got := app.agentMindPositions(agentMindScoutID, "lighthouse"); len(got) != 0 {
		t.Fatalf("old summary query resurrected stale revision: %#v", got)
	}
	positions := app.agentMindPositions(agentMindScoutID, "athlete")
	if len(positions) != 1 || positions[0].Revision != 2 {
		t.Fatalf("latest position=%#v, want revision 2", positions)
	}
	thread.Messages[0].Reactions = []scoutChatMessageReaction{{
		Emoji:      "👍",
		ActorEmail: "reviewer@example.com",
		ActorName:  "Reviewer",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}}
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	if got := app.agentMindPositions(agentMindScoutID, "athlete"); len(got) != 1 || got[0].Revision != 2 {
		t.Fatalf("reaction invalidated unchanged source content: %#v", got)
	}
	forgotten, appended, err := app.resolveAgentMindPosition(positions[0], agentMindPositionStatusForgotten, "Human review withdrew this working judgment.", "AJ", "review-1")
	if err != nil || !appended || forgotten.Status != agentMindPositionStatusForgotten {
		t.Fatalf("forget position=%+v appended=%v err=%v", forgotten, appended, err)
	}
	if got := app.agentMindPositions(agentMindScoutID, "lighthouse athlete"); len(got) != 0 {
		t.Fatalf("forgotten latest revision exposed an older position: %#v", got)
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
		SourceAnswer:  "answer-1",
		SourceDigest:  strings.Repeat("c", 64),
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

func TestAgentMindReviewRouteIsAdminRevisionBoundAndSourceAuthorized(t *testing.T) {
	setupAuthTestEnv(t)
	timCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	adminCookies := loginAs(t, artifactLibraryAdminEmail, "B0NFIRE!")
	app := newIsolatedKanbanBoardApp(t)
	previous := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previous })
	thread, err := app.createScoutChatThread(artifactLibraryAdminEmail, "AJ", "agent-mind-review", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	question := scoutChatMessageRecord{ID: "review-question", Role: "user", AuthorEmail: artifactLibraryAdminEmail, Text: "What do you think is the better launch strategy?"}
	answer := scoutChatMessageRecord{ID: "review-answer", Role: "scout", Text: "My read is to start with the narrow pilot because it provides a faster proof point and limits downside."}
	thread.Messages = []scoutChatMessageRecord{question, answer}
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	app.maybeRecordScoutAgentMindPosition(thread, question, answer)
	positions := app.agentMindPositionsForViewer(artifactLibraryAdminEmail, agentMindScoutID, "")
	if len(positions) != 1 {
		t.Fatalf("positions=%#v", positions)
	}
	body := []byte(fmt.Sprintf(`{"id":%q,"revision":%d,"action":"correct","summary":"The pilot remains preferred, but only after the source assumptions are revalidated."}`, positions[0].ID, positions[0].Revision))

	request := httptest.NewRequest(http.MethodPost, "/assistant/agent-mind", bytes.NewReader(body))
	for _, cookie := range timCookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantAgentMindHandler(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("non-admin status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/assistant/agent-mind", bytes.NewReader(body))
	for _, cookie := range adminCookies {
		request.AddCookie(cookie)
	}
	if user := userFromRequest(request); user == nil || normalizeAccountEmail(user.Email) != artifactLibraryAdminEmail {
		t.Fatalf("admin session resolved to %+v", user)
	}
	recorder = httptest.NewRecorder()
	assistantAgentMindHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("admin status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	latest := app.latestAgentMindPosition(positions[0].AgentID, positions[0].Subject, positions[0].Scope)
	if latest.Revision != positions[0].Revision+1 || latest.Status != agentMindPositionStatusCorrected || latest.Origin != agentMindPositionOriginReview {
		t.Fatalf("latest=%+v", latest)
	}

	request = httptest.NewRequest(http.MethodPost, "/assistant/agent-mind", bytes.NewReader(body))
	for _, cookie := range adminCookies {
		request.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	assistantAgentMindHandler(recorder, request)
	if recorder.Code != http.StatusNotFound && recorder.Code != http.StatusConflict {
		t.Fatalf("stale review status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
