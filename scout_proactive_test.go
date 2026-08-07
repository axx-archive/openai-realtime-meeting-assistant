package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDecodeScoutProactiveDecisionRejectsUnsafeOrIncompleteActions(t *testing.T) {
	valid, err := decodeScoutProactiveDecision(`{"decision":"reply","confidence":0.91,"reason":"adds a concrete counterpoint","reply":"Try the narrow launch first.","reaction":"","consultAgentId":"","consultQuery":"","interruptionCost":"low"}`)
	if err != nil || valid.Decision != "reply" {
		t.Fatalf("valid decision=%+v err=%v", valid, err)
	}
	for _, raw := range []string{
		`{"decision":"reply","confidence":0.91,"reason":"missing reply","reply":"","reaction":"","consultAgentId":"","consultQuery":"","interruptionCost":"low"}`,
		`{"decision":"react","confidence":0.91,"reason":"unknown emoji","reply":"","reaction":"not-allowed","consultAgentId":"","consultQuery":"","interruptionCost":"low"}`,
		`{"decision":"reply","confidence":0.91,"reason":"mixed action","reply":"ok","reaction":"👍","consultAgentId":"","consultQuery":"","interruptionCost":"low"}`,
		`{"decision":"react","confidence":0.91,"reason":"mixed action","reply":"also reply","reaction":"👍","consultAgentId":"","consultQuery":"","interruptionCost":"low"}`,
		`{"decision":"reply","confidence":0.91,"reason":"consult cannot ride a reply","reply":"I will ask Colton.","reaction":"","consultAgentId":"colton-research","consultQuery":"Research the launch.","interruptionCost":"low"}`,
		`{"decision":"no_action","confidence":0.91,"reason":"orphaned query","reply":"","reaction":"","consultAgentId":"","consultQuery":"Research the launch.","interruptionCost":"low"}`,
		`{"decision":"reply","confidence":0.91,"reason":"extra field","reply":"ok","reaction":"","consultAgentId":"","consultQuery":"","extra":true}`,
	} {
		if _, err := decodeScoutProactiveDecision(raw); err == nil {
			t.Fatalf("unsafe decision accepted: %s", raw)
		}
	}
	noAction, err := decodeScoutProactiveDecision(`{"decision":"no_action","confidence":0.95,"reason":"no material value","reply":"should be cleared","reaction":"👍","consultAgentId":"","consultQuery":"","interruptionCost":"low"}`)
	if err != nil || noAction.Reply != "" || noAction.Reaction != "" {
		t.Fatalf("no_action retained side effect fields: %+v err=%v", noAction, err)
	}
	consult, err := decodeScoutProactiveDecision(`{"decision":"no_action","confidence":0.95,"reason":"research could help","reply":"","reaction":"","consultAgentId":"colton-research","consultQuery":"Compare the narrow launch evidence.","interruptionCost":"low"}`)
	if err != nil || consult.ConsultAgentID != "colton-research" || consult.ConsultQuery == "" || consult.Reply != "" || consult.Reaction != "" {
		t.Fatalf("valid non-visible consult=%+v err=%v", consult, err)
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
	message.Text = "An edited, materially different update"
	message.EditedAt = "2026-08-06T22:00:00Z"
	app.nudgeScoutProactiveAttention(thread, message, "event-2")
	if got := len(app.scoutProactiveQueue); got != 1 {
		t.Fatalf("edited source queued events=%d, want a distinct event", got)
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
		return `{"decision":"reply","confidence":0.95,"reason":"adds a concrete next step","reply":"Test the narrow launch first.","reaction":"","consultAgentId":"","consultQuery":"","interruptionCost":"low"}`, nil
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

func TestScoutProactiveActiveModeNeverPostsUninvokedReply(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("SCOUT_PROACTIVE_MODE", scoutProactiveModeActive)
	candidate := scoutProactiveCandidate{
		Thread:       scoutChatThreadRecord{ID: "team", Title: "Team", Visibility: scoutChatVisibilityPublic},
		Message:      scoutChatMessageRecord{ID: "message-active-reply", Role: "user", AuthorName: "AJ", Text: "What do humans here think?"},
		SourceDigest: strings.Repeat("e", 64),
		EventRef:     "event-active-reply",
	}
	responder := func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		return `{"decision":"reply","confidence":0.96,"reason":"could add a view","reply":"Here is Scout's take.","reaction":"","consultAgentId":"","consultQuery":"","interruptionCost":"low"}`, nil
	}
	if evaluated, err := app.runScoutProactiveCandidates(context.Background(), "", responder, []scoutProactiveCandidate{candidate}); err != nil || evaluated != 1 {
		t.Fatalf("evaluated=%d err=%v", evaluated, err)
	}
	entries := app.memory.entriesOfKind(meetingMemoryKindScoutAttention, 0)
	if len(entries) != 1 {
		t.Fatalf("attention entries=%d, want one suggestion receipt", len(entries))
	}
	var record scoutProactiveAttentionRecord
	if err := json.Unmarshal([]byte(entries[0].Text), &record); err != nil {
		t.Fatal(err)
	}
	if record.Decision != "reply" || record.Status != "suggested" {
		t.Fatalf("active reply record=%+v, want non-visible suggestion", record)
	}
	if len(candidate.Thread.Messages) != 0 {
		t.Fatalf("candidate thread mutated by background reply: %+v", candidate.Thread.Messages)
	}
}

func TestScoutProactiveConfidenceDemotionClearsEveryActionField(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("SCOUT_PROACTIVE_MODE", scoutProactiveModeQuiet)
	candidate := scoutProactiveCandidate{
		Thread:       scoutChatThreadRecord{ID: "team", Title: "Team"},
		Message:      scoutChatMessageRecord{ID: "message-low-confidence", Role: "user", Text: "Should Scout add anything?"},
		SourceDigest: strings.Repeat("c", 64),
		EventRef:     "event-low-confidence",
	}
	responder := func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		return `{"decision":"reply","confidence":0.4,"reason":"uncertain","reply":"Maybe.","reaction":"","consultAgentId":"","consultQuery":"","interruptionCost":"low"}`, nil
	}
	if evaluated, err := app.runScoutProactiveCandidates(context.Background(), "", responder, []scoutProactiveCandidate{candidate}); err != nil || evaluated != 1 {
		t.Fatalf("evaluated=%d err=%v", evaluated, err)
	}
	entries := app.memory.entriesOfKind(meetingMemoryKindScoutAttention, 0)
	if len(entries) != 1 {
		t.Fatalf("attention entries=%d", len(entries))
	}
	var record scoutProactiveAttentionRecord
	if err := json.Unmarshal([]byte(entries[0].Text), &record); err != nil {
		t.Fatal(err)
	}
	if record.Decision != "no_action" || record.Reply != "" || record.Reaction != "" || record.ConsultAgentID != "" || record.ConsultQuery != "" {
		t.Fatalf("confidence-demoted no_action retained action fields: %+v", record)
	}
}

func TestScoutProactiveConsultIsOneNonVisibleReceiptAndNeverLaunches(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	t.Setenv("SCOUT_PROACTIVE_MODE", scoutProactiveModeActive)
	candidate := scoutProactiveCandidate{
		Thread:       scoutChatThreadRecord{ID: "team", Title: "Team", OwnerEmail: "aj@example.com"},
		Message:      scoutChatMessageRecord{ID: "message-consult", Role: "user", AuthorName: "AJ", Text: "What does the market evidence say?"},
		SourceDigest: strings.Repeat("e", 64),
		EventRef:     "event-consult",
	}
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if !strings.Contains(request.Instructions, "never launches Colton, posts, reacts") {
			t.Fatalf("proactive prompt does not bind consultation to a non-visible receipt: %q", request.Instructions)
		}
		return `{"decision":"no_action","confidence":0.96,"reason":"bounded research would improve the answer","reply":"","reaction":"","consultAgentId":"colton-research","consultQuery":"Compare primary-source market evidence.","interruptionCost":"low"}`, nil
	}
	if evaluated, err := app.runScoutProactiveCandidates(context.Background(), "", responder, []scoutProactiveCandidate{candidate}); err != nil || evaluated != 1 {
		t.Fatalf("evaluated=%d err=%v", evaluated, err)
	}
	entries := app.memory.entriesOfKind(meetingMemoryKindScoutAttention, 0)
	if len(entries) != 1 {
		t.Fatalf("attention entries=%d, want exactly one consult receipt", len(entries))
	}
	var record scoutProactiveAttentionRecord
	if err := json.Unmarshal([]byte(entries[0].Text), &record); err != nil {
		t.Fatal(err)
	}
	if record.Status != "consult_suggested" || record.Decision != "no_action" || record.ConsultAgentID != "colton-research" || record.ConsultQuery == "" || record.Reply != "" || record.Reaction != "" {
		t.Fatalf("consult receipt=%+v", record)
	}
	if artifacts := app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0); len(artifacts) != 0 {
		t.Fatalf("proactive consultation launched %d agent artifact(s)", len(artifacts))
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
		return `{"decision":"no_action","confidence":0.95,"reason":"No material value","reply":"","reaction":"","consultAgentId":"","consultQuery":"","interruptionCost":"low"}`, nil
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

func TestScoutProactiveInterruptionBudgetLeavesRoomForHumans(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	now := time.Date(2026, 8, 7, 5, 0, 0, 0, time.UTC)
	candidate := scoutProactiveCandidate{Thread: scoutChatThreadRecord{ID: "ball-dogs"}, Message: scoutChatMessageRecord{ID: "message-2"}}
	mediumReply := scoutProactiveDecision{Decision: "reply", InterruptionCost: "medium"}
	if allowed, _ := app.scoutProactiveInterruptionAllowed(candidate, mediumReply, now); allowed {
		t.Fatal("medium-cost unsolicited reply was allowed")
	}
	lowReply := scoutProactiveDecision{Decision: "reply", InterruptionCost: "low"}
	if allowed, reason := app.scoutProactiveInterruptionAllowed(candidate, lowReply, now); !allowed {
		t.Fatalf("first low-cost reply denied: %s", reason)
	}
	record := scoutProactiveAttentionRecord{ID: "attention-prior", ThreadID: candidate.Thread.ID, MessageID: "message-1", SourceDigest: strings.Repeat("d", 64), Decision: "reply", Mode: scoutProactiveModeActive, Status: "posted", Confidence: .95, CreatedAt: now.Add(-time.Minute)}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.memory.appendAmbientEntry(meetingMemoryKindScoutAttention, record.ID, string(raw), nil); err != nil {
		t.Fatal(err)
	}
	if allowed, _ := app.scoutProactiveInterruptionAllowed(candidate, lowReply, now); allowed {
		t.Fatal("reply ignored recent visible Scout participation")
	}
	lowReaction := scoutProactiveDecision{Decision: "react", InterruptionCost: "low"}
	if allowed, _ := app.scoutProactiveInterruptionAllowed(candidate, lowReaction, now.Add(3*time.Minute)); !allowed {
		t.Fatal("lightweight reaction remained blocked beyond its spacing window")
	}
}

func TestScoutProactiveDoesNotReenterAfterItsOwnLastMessageWithoutInvocation(t *testing.T) {
	thread := scoutChatThreadRecord{ID: "ball-dogs", Messages: []scoutChatMessageRecord{
		{ID: "scout-last", Role: "scout", Text: "The narrow version is more attainable."},
		{ID: "human-followup", Role: "user", Text: "I agree, but curious what everyone else thinks."},
	}}
	if !scoutProactiveMessageImmediatelyFollowsScout(thread, "human-followup") {
		t.Fatal("human follow-up after Scout was not reserved for the room")
	}
	thread.Messages = append(thread.Messages, scoutChatMessageRecord{ID: "another-human", Role: "user", Text: "Here is another angle."})
	if scoutProactiveMessageImmediatelyFollowsScout(thread, "another-human") {
		t.Fatal("a later human-to-human turn was incorrectly treated as Scout's automatic follow-up")
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
