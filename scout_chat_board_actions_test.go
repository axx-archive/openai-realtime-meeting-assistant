package main

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestScoutChatBoardIntentIsNarrow(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"open the board", scoutChatBoardIntentOpen},
		{"Scout, show me the Kanban board", scoutChatBoardIntentOpen},
		{"clear the kanban board", scoutChatBoardIntentClear},
		{"can you clear out everything on the kanban board", scoutChatBoardIntentClear},
		{"empty the board", scoutChatBoardIntentClear},
		{"delete all cards from the Board", scoutChatBoardIntentClear},
		{"clear this board card", ""},
		{"research better board governance", ""},
		{"show me the launch plan", ""},
	}
	for _, tc := range cases {
		if got := scoutChatBoardIntent(tc.text); got != tc.want {
			t.Errorf("scoutChatBoardIntent(%q)=%q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestScoutChatClearBoardIsReadOnlyAndProviderFenced(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "board-router-test")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = "board-router-test"
	t.Cleanup(func() { kanbanApp = previousApp })

	providerCalls := 0
	previousResponder := createOpenAITextResponse
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		providerCalls++
		return "", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = previousResponder })

	before := kanbanApp.snapshotState()
	beforeBytes, err := os.ReadFile(kanbanBoardPath())
	if err != nil {
		t.Fatalf("read board before command: %v", err)
	}
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	thread, err := kanbanApp.createScoutChatThread(user.Email, user.Name, "Scout", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "can you clear out everything on the kanban board", nil, "")
	if err != nil {
		t.Fatalf("append clear command: %v", err)
	}
	if providerCalls != 0 || response["providerCalls"] != 0 || response["providerExecutionFenced"] != true {
		t.Fatalf("provider calls=%d response=%v, want a fully fenced deterministic turn", providerCalls, response)
	}
	if _, proposed := response["proposal"]; proposed {
		t.Fatalf("clear Board must not become generic work: keys=%v", responseKeys(response))
	}
	action, ok := response["boardAction"].(*scoutChatBoardAction)
	if !ok {
		t.Fatalf("boardAction type=%T, want *scoutChatBoardAction", response["boardAction"])
	}
	if action.Surface != "artifacts" || action.Action != "open" || action.RequestedIntent != scoutChatBoardIntentClear || action.ActiveCardCount != 0 || !action.ReadOnly || action.MutationExecuted || action.Reason != "board_retired" {
		t.Fatalf("boardAction=%#v, want retired compatibility redirect to Work", action)
	}
	after := kanbanApp.snapshotState()
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("Board mutated: before=%#v after=%#v", before, after)
	}
	afterBytes, err := os.ReadFile(kanbanBoardPath())
	if err != nil {
		t.Fatalf("read board after command: %v", err)
	}
	if string(afterBytes) != string(beforeBytes) {
		t.Fatal("persisted Board bytes changed during a read-only clear request")
	}
	saved := response["thread"].(scoutChatThreadRecord)
	if len(saved.Messages) != 2 || saved.Messages[1].Role != "scout" || !strings.Contains(saved.Messages[1].Text, "Board is retired") || !strings.Contains(saved.Messages[1].Text, "Nothing was changed") {
		t.Fatalf("persisted conversation=%#v, want user + retirement no-mutation reply", saved.Messages)
	}
}

func TestScoutChatOpenBoardReturnsReadOnlyNavigation(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "board-router-test")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = "board-router-test"
	t.Cleanup(func() { kanbanApp = previousApp })

	providerCalls := 0
	previousResponder := createOpenAITextResponse
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		providerCalls++
		return "", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = previousResponder })

	user := accountStore().findUser("aj@shareability.com")
	thread, err := kanbanApp.createScoutChatThread(user.Email, user.Name, "Scout", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	before := kanbanApp.snapshotState()
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, thread.ID, "Scout, show me the Kanban board", nil, "")
	if err != nil {
		t.Fatalf("append open command: %v", err)
	}
	if providerCalls != 0 || response["providerCalls"] != 0 {
		t.Fatalf("provider calls=%d response=%v, want deterministic read", providerCalls, response)
	}
	action, ok := response["boardAction"].(*scoutChatBoardAction)
	if !ok || action.Surface != "artifacts" || action.RequestedIntent != scoutChatBoardIntentOpen || action.ActiveCardCount != 0 || !action.ReadOnly || action.MutationExecuted || action.Reason != "board_retired" {
		t.Fatalf("boardAction=%#v, want retired compatibility redirect to Work", action)
	}
	if after := kanbanApp.snapshotState(); !reflect.DeepEqual(after, before) {
		t.Fatalf("open Board mutated state: before=%#v after=%#v", before, after)
	}
	saved := response["thread"].(scoutChatThreadRecord)
	if len(saved.Messages) != 2 || !strings.Contains(saved.Messages[1].Text, "Board is retired") || !strings.Contains(saved.Messages[1].Text, "open Work") {
		t.Fatalf("persisted conversation=%#v, want retirement redirect", saved.Messages)
	}
}
