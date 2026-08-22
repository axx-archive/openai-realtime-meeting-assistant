package main

import (
	"context"
	"strings"
	"testing"
)

func openChatThreadRouteJSON(t *testing.T, title, visibility string) string {
	t.Helper()
	return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
		Outcome:   string(conversationIntentStartPrivateWork),
		Route:     "app_action",
		ToolID:    "open_chat_thread",
		Objective: "Open the exact authorized chat destination",
		Fields: []openAIScoutRouterField{
			{Key: "title", Value: title},
			{Key: "visibility", Value: visibility},
		},
	})
}

func navigationActionFromResponse(t *testing.T, response map[string]any) map[string]any {
	t.Helper()
	actions, ok := response["actions"].([]map[string]any)
	if !ok || len(actions) != 1 {
		t.Fatalf("navigation response actions=%#v", response["actions"])
	}
	return actions[0]
}

func TestScoutOpenChatThreadIsTheOnlyAdmittedReadOnlyNativeAction(t *testing.T) {
	decision, err := scoutConversationIntentFromOpenAI(openAIScoutRouterOutput{
		Outcome: string(conversationIntentApprovalRequired), Route: "app_action", ToolID: "open_chat_thread",
		Objective: "Open Like A Farmer", EffectClass: "governed_effect",
		Fields: []openAIScoutRouterField{{Key: "title", Value: "Like A Farmer"}, {Key: "visibility", Value: "public"}},
	}, "open Like A Farmer")
	if err != nil || decision.Outcome != conversationIntentStartPrivateWork || decision.Work == nil || decision.Work.ToolID != "open_chat_thread" {
		t.Fatalf("read-only navigation was not admitted without approval: decision=%+v err=%v", decision, err)
	}
	verdict, err := scoutRouterVerdictFromConversationIntent(decision, "open Like A Farmer")
	if err != nil || verdict == nil || verdict.action == nil || verdict.action.ToolID != "open_chat_thread" {
		t.Fatalf("navigation verdict=%+v err=%v", verdict, err)
	}

	blocked, err := scoutConversationIntentFromOpenAI(openAIScoutRouterOutput{
		Outcome: string(conversationIntentStartPrivateWork), Route: "app_action", ToolID: "archive_channel",
		Objective: "Archive Like A Farmer", Fields: []openAIScoutRouterField{{Key: "channel", Value: "Like A Farmer"}},
	}, "archive Like A Farmer")
	if err != nil || blocked.Outcome != conversationIntentUnavailable || blocked.Unavailable == nil || blocked.Unavailable.Code != "tool_unadmitted" {
		t.Fatalf("legacy mutation escaped admission fence: decision=%+v err=%v", blocked, err)
	}
}

func TestScoutOpenChatThreadResolutionFailsClosedOnACLArchiveAndAmbiguity(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	aj := accountStore().findUser("aj@shareability.com")
	erick := accountStore().findUser("e@shareability.com")
	if aj == nil || erick == nil {
		t.Fatal("seed users unavailable")
	}
	public, err := app.createScoutChatThread(aj.Email, aj.Name, "Same destination", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	private, err := app.createScoutChatThread(aj.Email, aj.Name, "Same destination", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exactVisibleChatThread(app, aj.Email, "Same destination", ""); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("same-title destination did not fail closed: %v", err)
	}
	if got, err := exactVisibleChatThread(app, aj.Email, "#Same destination", "public"); err != nil || got.ID != public.ID {
		t.Fatalf("public hint got=%+v err=%v", got, err)
	}
	if got, err := exactVisibleChatThread(app, aj.Email, "Same destination", "private"); err != nil || got.ID != private.ID {
		t.Fatalf("private hint got=%+v err=%v", got, err)
	}

	hidden, err := app.createScoutChatThread(erick.Email, erick.Name, "Hidden destination", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exactVisibleChatThread(app, aj.Email, hidden.Title, "private"); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("unauthorized private destination resolved: %v", err)
	}
	archived, err := app.createScoutChatThread(aj.Email, aj.Name, "Archived destination", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.setScoutChatThreadArchived(aj.Email, archived.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := exactVisibleChatThread(app, aj.Email, archived.Title, "public"); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("archived destination resolved: %v", err)
	}
}

func TestTypedScoutOpenChatThreadReturnsExactNavigationWithoutPostingToTarget(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "typed-chat-navigation"
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user unavailable")
	}
	source, err := app.createScoutChatThread(user.Email, user.Name, "Private Scout navigation", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	target, err := app.createScoutChatThread(user.Email, user.Name, "Like A Farmer", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	before := len(target.Messages)
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_route" {
			t.Fatalf("navigation reached unexpected workflow %q", request.Workflow)
		}
		return openChatThreadRouteJSON(t, target.Title, scoutChatVisibilityPublic), nil
	})

	response, err := app.appendScoutChatThreadMessage(context.Background(), user, source.ID, "Open the Like A Farmer channel", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	action := navigationActionFromResponse(t, response)
	if action["type"] != "open_chat_thread" || action["threadId"] != target.ID || action["title"] != target.Title ||
		action["visibility"] != scoutChatVisibilityPublic {
		t.Fatalf("navigation action=%#v", action)
	}
	if _, leaked := action["voiceContext"]; leaked {
		t.Fatalf("generic typed navigation leaked a voice-only context claim: %#v", action)
	}
	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || answer.IntentOutcome != string(conversationIntentStartPrivateWork) || answer.Text != "Opening #Like A Farmer. Nothing was posted there." {
		t.Fatalf("typed navigation answer=%+v", answer)
	}
	after, _, err := app.scoutChatThreadByID(user.Email, target.ID)
	if err != nil || len(after.Messages) != before {
		t.Fatalf("navigation mutated target: before=%d after=%d err=%v", before, len(after.Messages), err)
	}
}
