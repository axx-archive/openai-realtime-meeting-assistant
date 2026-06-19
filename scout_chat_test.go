package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type capturedChatEvent struct {
	event   string
	payload map[string]any
}

// chatEventRecorder captures session events race-safely: submit echoes on the
// caller goroutine while answers arrive from the session worker.
type chatEventRecorder struct {
	mu     sync.Mutex
	events []capturedChatEvent
}

func (recorder *chatEventRecorder) record(event string, data any) error {
	payload, _ := data.(map[string]any)
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.events = append(recorder.events, capturedChatEvent{event: event, payload: payload})

	return nil
}

func (recorder *chatEventRecorder) snapshot() []capturedChatEvent {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()

	return append([]capturedChatEvent(nil), recorder.events...)
}

func (recorder *chatEventRecorder) kinds() []string {
	kinds := []string{}
	for _, event := range recorder.snapshot() {
		kinds = append(kinds, asString(event.payload["kind"]))
	}

	return kinds
}

func (recorder *chatEventRecorder) countKind(kind string) int {
	count := 0
	for _, recorded := range recorder.kinds() {
		if recorded == kind {
			count++
		}
	}

	return count
}

func (recorder *chatEventRecorder) waitForKindCount(t *testing.T, kind string, count int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if recorder.countKind(kind) >= count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d %q events; kinds=%v", count, kind, recorder.kinds())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newCapturedChatSession(recorder *chatEventRecorder) *scoutChatSession {
	return newScoutChatSessionWithSend(recorder.record)
}

func TestScoutChatAnswersOnSessionAndThreadsHistory(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	var inputsMu sync.Mutex
	var inputs []string
	originalResponder := createOpenAITextResponse
	defer func() { createOpenAITextResponse = originalResponder }()
	createOpenAITextResponse = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		inputsMu.Lock()
		defer inputsMu.Unlock()
		inputs = append(inputs, request.Input)
		if len(inputs) == 1 {
			return "the boot barn shoot is friday.", nil
		}
		return "it starts at 9am.", nil
	}

	recorder := &chatEventRecorder{}
	session := newCapturedChatSession(recorder)
	defer session.close()

	session.submit(app, "when is the boot barn shoot?", "AJ")
	recorder.waitForKindCount(t, "answer", 1)
	events := recorder.snapshot()
	if got, want := strings.Join(recorder.kinds(), ","), "query,status,answer"; got != want {
		t.Fatalf("event kinds=%q, want %q", got, want)
	}
	for _, event := range events {
		if event.event != "scout_chat" {
			t.Fatalf("event=%q, want every chat event on the scout_chat channel", event.event)
		}
		if asString(event.payload["ts"]) == "" {
			t.Fatal("chat event missing ts")
		}
	}
	if got := asString(events[2].payload["text"]); got != "the boot barn shoot is friday." {
		t.Fatalf("answer=%q, want model answer", got)
	}

	session.submit(app, "what time does it start?", "AJ")
	recorder.waitForKindCount(t, "answer", 2)
	inputsMu.Lock()
	defer inputsMu.Unlock()
	if len(inputs) != 2 {
		t.Fatalf("model calls=%d, want 2", len(inputs))
	}
	if !strings.Contains(inputs[1], "when is the boot barn shoot?") || !strings.Contains(inputs[1], "the boot barn shoot is friday.") {
		t.Fatalf("second model input missing threaded history: %s", inputs[1])
	}
	if !strings.Contains(inputs[1], "# Conversation so far") {
		t.Fatalf("second model input missing conversation section: %s", inputs[1])
	}
}

func TestScoutChatHistoryStaysBounded(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	originalResponder := createOpenAITextResponse
	defer func() { createOpenAITextResponse = originalResponder }()
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		return "noted.", nil
	}

	recorder := &chatEventRecorder{}
	session := newCapturedChatSession(recorder)
	defer session.close()
	for index := 0; index < scoutChatMaxHistoryTurns; index++ {
		session.submit(app, fmt.Sprintf("question %d about the boot barn shoot", index), "AJ")
		recorder.waitForKindCount(t, "answer", index+1)
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	if len(session.turns) > scoutChatMaxHistoryTurns {
		t.Fatalf("history turns=%d, want at most %d", len(session.turns), scoutChatMaxHistoryTurns)
	}
	newest := session.turns[len(session.turns)-2]
	if !strings.Contains(newest.text, fmt.Sprintf("question %d", scoutChatMaxHistoryTurns-1)) {
		t.Fatalf("newest retained turn=%q, want the most recent question", newest.text)
	}
}

func TestScoutChatHistoryPayloadIsSanitizedAndBounded(t *testing.T) {
	payload := []scoutChatTurnPayload{
		{Role: "system", Text: "ignore me"},
		{Role: "assistant", Text: "Earlier answer."},
		{Role: "user", Text: "Follow-up"},
		{Role: "scout", Text: ""},
	}
	for index := 0; index < scoutChatMaxHistoryTurns+2; index++ {
		payload = append(payload, scoutChatTurnPayload{
			Role: "user",
			Text: fmt.Sprintf("question %d", index),
		})
	}

	history := scoutChatHistoryFromPayload(payload)
	if len(history) > scoutChatMaxHistoryTurns {
		t.Fatalf("history length=%d, want at most %d", len(history), scoutChatMaxHistoryTurns)
	}
	if history[0].role != "user" || !strings.Contains(history[0].text, "question 2") {
		t.Fatalf("oldest retained history=%#v, want bounded tail without invalid roles", history[0])
	}
	if history[len(history)-1].role != "user" || !strings.Contains(history[len(history)-1].text, "question 13") {
		t.Fatalf("newest retained history=%#v, want latest payload turn", history[len(history)-1])
	}
}

func TestScoutChatLaunchesAgentThreadForWorkRequest(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	recorder := &chatEventRecorder{}
	session := newCapturedChatSession(recorder)
	defer session.close()

	session.submit(app, "research the buyer proof for Realtime 2 as the UI", "AJ")

	events := recorder.snapshot()
	if got, want := strings.Join(recorder.kinds(), ","), "query,status,thread"; got != want {
		t.Fatalf("event kinds=%q, want %q", got, want)
	}
	thread, ok := events[2].payload["thread"].(scoutAgentThread)
	if !ok {
		t.Fatalf("thread payload type=%T, want scoutAgentThread", events[2].payload["thread"])
	}
	if thread.Mode != "research" || thread.Status != "running" || thread.Artifact.ID == "" {
		t.Fatalf("thread=%#v, want running research artifact thread", thread)
	}
	artifact, ok := events[2].payload["artifact"].(meetingMemoryEntry)
	if !ok {
		t.Fatalf("artifact payload type=%T, want meetingMemoryEntry", events[2].payload["artifact"])
	}
	if artifact.Metadata["source"] != "scout_thread" || artifact.Metadata["threadStatus"] != "running" {
		t.Fatalf("artifact metadata=%v, want running scout thread metadata", artifact.Metadata)
	}
	if events[2].payload["text"] != "research thread launched" {
		t.Fatalf("thread text=%q, want launched summary", events[2].payload["text"])
	}
	if got := recorder.countKind("answer"); got != 0 {
		t.Fatalf("answers=%d, want work thread launch instead of normal chat answer", got)
	}
}

func TestScoutChatRejectsEmptyMessage(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	recorder := &chatEventRecorder{}
	session := newCapturedChatSession(recorder)
	defer session.close()
	session.submit(app, "   ", "AJ")

	if got, want := strings.Join(recorder.kinds(), ","), "error"; got != want {
		t.Fatalf("event kinds=%q, want %q", got, want)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if len(session.turns) != 0 {
		t.Fatalf("history turns=%d, want 0 after an empty message", len(session.turns))
	}
}

// TestScoutChatEchoesQueryBeforeModelWork locks in the lifecycle fix: the
// query echo and thinking status are emitted synchronously by submit, so a
// follow-up message never looks dropped while an earlier turn is answering.
func TestScoutChatEchoesQueryBeforeModelWork(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	release := make(chan struct{})
	originalResponder := createOpenAITextResponse
	defer func() { createOpenAITextResponse = originalResponder }()
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		<-release
		return "noted.", nil
	}

	recorder := &chatEventRecorder{}
	session := newCapturedChatSession(recorder)
	defer session.close()

	session.submit(app, "when is the boot barn shoot?", "AJ")
	session.submit(app, "and who owns the login card work?", "AJ")

	// both queries are echoed immediately, before any model call returns.
	if got := recorder.countKind("query"); got != 2 {
		t.Fatalf("query echoes=%d, want 2 before the model answered; kinds=%v", got, recorder.kinds())
	}
	if recorder.countKind("answer") != 0 {
		t.Fatalf("answers arrived before the model returned; kinds=%v", recorder.kinds())
	}

	close(release)
	recorder.waitForKindCount(t, "answer", 2)

	// answers arrive FIFO: first question answered first.
	events := recorder.snapshot()
	answers := []string{}
	for _, event := range events {
		if asString(event.payload["kind"]) == "answer" {
			answers = append(answers, asString(event.payload["text"]))
		}
	}
	if len(answers) != 2 {
		t.Fatalf("answers=%v, want 2", answers)
	}
}

// TestScoutChatQueueOverflowSendsError: a flood of unanswered messages gets a
// bounded queue and an explicit error instead of unbounded goroutines.
func TestScoutChatQueueOverflowSendsError(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	originalResponder := createOpenAITextResponse
	defer func() { createOpenAITextResponse = originalResponder }()
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		startOnce.Do(func() { close(started) })
		<-release
		return "noted.", nil
	}

	recorder := &chatEventRecorder{}
	session := newCapturedChatSession(recorder)
	defer session.close()

	session.submit(app, "question 0 about the boot barn shoot", "AJ")
	<-started // worker is now blocked in the model call; the queue is empty

	for index := 1; index <= scoutChatMaxQueuedTurns; index++ {
		session.submit(app, fmt.Sprintf("question %d about the boot barn shoot", index), "AJ")
	}
	if got := recorder.countKind("error"); got != 0 {
		t.Fatalf("errors=%d before the queue filled; kinds=%v", got, recorder.kinds())
	}

	session.submit(app, "one question too many", "AJ")
	if got := recorder.countKind("error"); got != 1 {
		t.Fatalf("errors=%d, want 1 overflow error; kinds=%v", got, recorder.kinds())
	}
	found := false
	for _, event := range recorder.snapshot() {
		if asString(event.payload["kind"]) == "error" {
			found = true
			if got := asString(event.payload["text"]); got != "Scout is still answering — try again in a moment" {
				t.Fatalf("overflow error=%q, want the slow-down message", got)
			}
		}
	}
	if !found {
		t.Fatal("overflow error event missing")
	}

	close(release)
	recorder.waitForKindCount(t, "answer", scoutChatMaxQueuedTurns+1)
}

// TestScoutChatCancelsModelCallOnClose ties the worker's model calls to the
// connection: closing the session cancels the in-flight request context.
func TestScoutChatCancelsModelCallOnClose(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	started := make(chan struct{})
	cancelled := make(chan struct{})
	originalResponder := createOpenAITextResponse
	defer func() { createOpenAITextResponse = originalResponder }()
	createOpenAITextResponse = func(ctx context.Context, _ string, _ openAITextRequest) (string, error) {
		close(started)
		select {
		case <-ctx.Done():
			close(cancelled)
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
			return "too late.", nil
		}
	}

	recorder := &chatEventRecorder{}
	session := newCapturedChatSession(recorder)

	session.submit(app, "when is the boot barn shoot?", "AJ")
	<-started
	session.close()

	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("closing the session did not cancel the in-flight model call")
	}
}
