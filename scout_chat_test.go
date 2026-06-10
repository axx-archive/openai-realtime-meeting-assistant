package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type capturedChatEvent struct {
	event   string
	payload map[string]any
}

func newCapturedChatSession(events *[]capturedChatEvent) *scoutChatSession {
	return &scoutChatSession{
		send: func(event string, data any) error {
			payload, _ := data.(map[string]any)
			*events = append(*events, capturedChatEvent{event: event, payload: payload})
			return nil
		},
	}
}

func chatEventKinds(events []capturedChatEvent) []string {
	kinds := make([]string, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, asString(event.payload["kind"]))
	}

	return kinds
}

func TestScoutChatAnswersOnSessionAndThreadsHistory(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	var inputs []string
	originalResponder := createOpenAITextResponse
	defer func() { createOpenAITextResponse = originalResponder }()
	createOpenAITextResponse = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		inputs = append(inputs, request.Input)
		if len(inputs) == 1 {
			return "the boot barn shoot is friday.", nil
		}
		return "it starts at 9am.", nil
	}

	var events []capturedChatEvent
	session := newCapturedChatSession(&events)

	session.handle(app, "when is the boot barn shoot?")
	if got, want := strings.Join(chatEventKinds(events), ","), "query,status,answer"; got != want {
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

	session.handle(app, "what time does it start?")
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

	var events []capturedChatEvent
	session := newCapturedChatSession(&events)
	for index := 0; index < scoutChatMaxHistoryTurns; index++ {
		session.handle(app, fmt.Sprintf("question %d about the boot barn shoot", index))
	}

	if len(session.turns) > scoutChatMaxHistoryTurns {
		t.Fatalf("history turns=%d, want at most %d", len(session.turns), scoutChatMaxHistoryTurns)
	}
	newest := session.turns[len(session.turns)-2]
	if !strings.Contains(newest.text, fmt.Sprintf("question %d", scoutChatMaxHistoryTurns-1)) {
		t.Fatalf("newest retained turn=%q, want the most recent question", newest.text)
	}
}

func TestScoutChatRejectsEmptyMessage(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	var events []capturedChatEvent
	session := newCapturedChatSession(&events)
	session.handle(app, "   ")

	if got, want := strings.Join(chatEventKinds(events), ","), "error"; got != want {
		t.Fatalf("event kinds=%q, want %q", got, want)
	}
	if len(session.turns) != 0 {
		t.Fatalf("history turns=%d, want 0 after an empty message", len(session.turns))
	}
}
