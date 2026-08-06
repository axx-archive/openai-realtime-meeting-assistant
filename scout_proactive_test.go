package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestDecodeScoutProactiveDecisionRejectsUnsafeOrIncompleteActions(t *testing.T) {
	valid, err := decodeScoutProactiveDecision(`{"decision":"reply","confidence":0.91,"reason":"adds a concrete counterpoint","reply":"Try the narrow launch first.","reaction":"","consultAgentId":"","consultQuery":""}`)
	if err != nil || valid.Decision != "reply" {
		t.Fatalf("valid decision=%+v err=%v", valid, err)
	}
	for _, raw := range []string{
		`{"decision":"reply","confidence":0.91,"reason":"missing reply","reply":"","reaction":"","consultAgentId":"","consultQuery":""}`,
		`{"decision":"react","confidence":0.91,"reason":"unknown emoji","reply":"","reaction":"not-allowed","consultAgentId":"","consultQuery":""}`,
		`{"decision":"reply","confidence":0.91,"reason":"extra field","reply":"ok","reaction":"","consultAgentId":"","consultQuery":"","extra":true}`,
	} {
		if _, err := decodeScoutProactiveDecision(raw); err == nil {
			t.Fatalf("unsafe decision accepted: %s", raw)
		}
	}
	noAction, err := decodeScoutProactiveDecision(`{"decision":"no_action","confidence":0.95,"reason":"no material value","reply":"should be cleared","reaction":"👍","consultAgentId":"","consultQuery":""}`)
	if err != nil || noAction.Reply != "" || noAction.Reaction != "" {
		t.Fatalf("no_action retained side effect fields: %+v err=%v", noAction, err)
	}
}

func TestScoutProactiveNudgeIsEventDrivenAndCoalescesPendingMessages(t *testing.T) {
	app := &kanbanBoardApp{scoutProactiveQueue: make(chan scoutProactiveEvent, 2), scoutProactivePending: map[string]struct{}{}}
	app.scoutProactiveMu = sync.Mutex{}
	thread := scoutChatThreadRecord{ID: "team", Visibility: scoutChatVisibilityPublic}
	message := scoutChatMessageRecord{ID: "message-1", Role: "user", Text: "A useful human update"}
	t.Setenv("SCOUT_PROACTIVE_MODE", scoutProactiveModeQuiet)
	app.nudgeScoutProactiveAttention(thread, message, "event-1")
	app.nudgeScoutProactiveAttention(thread, message, "event-1")
	if got := len(app.scoutProactiveQueue); got != 1 {
		t.Fatalf("queued events=%d, want one coalesced event", got)
	}
	event := <-app.scoutProactiveQueue
	if event.ThreadID != thread.ID || event.MessageID != message.ID || event.EventRef != "event-1" {
		t.Fatalf("event=%+v", event)
	}
}

func TestScoutProactiveQuietModeClassifiesWithoutVisibleAction(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("SCOUT_PROACTIVE_MODE", scoutProactiveModeQuiet)
	candidate := scoutProactiveCandidate{
		Thread:       scoutChatThreadRecord{ID: "team", Title: "Team"},
		Message:      scoutChatMessageRecord{ID: "message-1", Role: "user", AuthorName: "AJ", Text: "Should we test the narrow launch first?"},
		SourceDigest: strings.Repeat("b", 64),
		EventRef:     "event-1",
	}
	called := 0
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		called++
		return `{"decision":"reply","confidence":0.95,"reason":"adds a concrete next step","reply":"Test the narrow launch first.","reaction":"","consultAgentId":"","consultQuery":""}`, nil
	}
	evaluated, err := app.runScoutProactiveCandidates(context.Background(), "", responder, []scoutProactiveCandidate{candidate})
	if err != nil || evaluated != 1 || called != 1 {
		t.Fatalf("evaluated=%d called=%d err=%v", evaluated, called, err)
	}
	entries := app.memory.entriesOfKind(meetingMemoryKindScoutAttention, 0)
	if len(entries) != 1 {
		t.Fatalf("attention entries=%d", len(entries))
	}
	var record scoutProactiveAttentionRecord
	if err := json.Unmarshal([]byte(entries[0].Text), &record); err != nil {
		t.Fatal(err)
	}
	if record.Status != "suggested" || record.Decision != "reply" {
		t.Fatalf("quiet record=%+v", record)
	}
}

func TestScoutProactiveClassifierUsesLunaMaxAndConstitution(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	candidate := scoutProactiveCandidate{
		Thread:  scoutChatThreadRecord{ID: "ball-dogs", Title: "Ball Dogs"},
		Message: scoutChatMessageRecord{ID: "message-1", Role: "user", AuthorName: "AJ", Text: "Which version is more attainable?"},
	}
	var captured openAITextRequest
	decision, err := app.classifyScoutProactiveCandidate(context.Background(), "openai-key", candidate, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		captured = request
		return `{"decision":"no_action","confidence":0.95,"reason":"No material value","reply":"","reaction":"","consultAgentId":"","consultQuery":""}`, nil
	})
	if err != nil || decision.Decision != "no_action" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	if captured.Model != scoutChatModel() || captured.ReasoningEffort != scoutReasoningEffort() || captured.Seat != seatProactiveAttention || captured.Workflow != "scout_proactive_attention" || captured.JSONSchema == nil {
		t.Fatalf("proactive request=%+v, want Luna/max strict proactive seat", captured)
	}
	if !strings.Contains(captured.Instructions, "Be a brilliant coworker") || !strings.Contains(captured.Instructions, "Use no_action often") {
		t.Fatalf("proactive instructions=%q, missing coworker/no-action contract", captured.Instructions)
	}
}

func TestScoutProactiveAttentionReceiptIsIdempotent(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	candidate := scoutProactiveCandidate{
		Thread:       scoutChatThreadRecord{ID: "ball-dogs", Title: "Ball Dogs"},
		Message:      scoutChatMessageRecord{ID: "message-1", Role: "user", Text: "What is the wedge?"},
		SourceDigest: "source-digest-1",
		EventRef:     "event-1",
	}
	decision := scoutProactiveDecision{Decision: "no_action", Confidence: 0.99, Reason: "No material value"}
	if err := app.appendScoutProactiveAttention(candidate, decision, scoutProactiveModeQuiet, "suggested"); err != nil {
		t.Fatalf("first attention append: %v", err)
	}
	if err := app.appendScoutProactiveAttention(candidate, decision, scoutProactiveModeQuiet, "suggested"); err != nil {
		t.Fatalf("second attention append: %v", err)
	}
	if got := len(app.memory.entriesOfKind(meetingMemoryKindScoutAttention, 0)); got != 1 {
		t.Fatalf("attention entries=%d, want one idempotent receipt", got)
	}
}

func TestScoutProactiveWorkerStopsAndClearsLifecycleState(t *testing.T) {
	done := make(chan struct{})
	app := &kanbanBoardApp{
		scoutProactiveDone:    done,
		scoutProactiveQueue:   make(chan scoutProactiveEvent, 1),
		scoutProactivePending: map[string]struct{}{"thread\x00message": {}},
	}
	closed := false
	app.scoutProactiveCancel = func() {
		closed = true
		close(done)
	}

	app.stopScoutProactiveWorker()
	if !closed || app.scoutProactiveDone != nil || app.scoutProactiveQueue != nil || app.scoutProactivePending != nil {
		t.Fatalf("proactive lifecycle not stopped and cleared: closed=%t done=%v queue=%v pending=%v", closed, app.scoutProactiveDone, app.scoutProactiveQueue, app.scoutProactivePending)
	}
}
