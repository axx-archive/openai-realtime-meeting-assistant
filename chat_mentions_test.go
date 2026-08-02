package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestChatMentionNames(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"roster hit", "hey @Tyler can you own this?", []string{"Tyler"}},
		{"case-insensitive canonicalizes", "ping @tyler and @CAITLYN", []string{"Tyler", "Caitlyn"}},
		{"dedupes repeat mentions", "@aj @AJ @aj thoughts?", []string{"AJ"}},
		{"email address is not a mention", "mail aj@shareability.com today", nil},
		{"email local punctuation is not a mention", "mail first.last+alerts@Tyler.com", nil},
		{"prefix of a longer word is not a mention", "the @Tylerish account", nil},
		{"longer handle punctuation is not a mention", "the @Tyler_team and @AJ.dev accounts", nil},
		{"punctuation ends the name", "@tom, @tim: sync?", []string{"Tom", "Tim"}},
		{"scout is not a user mention", "@scout summarize this", nil},
		{"escaped mention is historical text", `don't notify \@Tyler but do notify @AJ`, []string{"AJ"}},
		{"quoted mention is historical text", `Tim wrote "@Tyler can you look" but @AJ owns it`, []string{"AJ"}},
		{"curly quoted mention is historical text", "Tim wrote “@Tyler can you look” but @AJ owns it", []string{"AJ"}},
		{"markdown quote is historical text", "> @Tyler can you look\n@AJ owns it", []string{"AJ"}},
		{"inline code is not a mention", "Use `@Tyler` in the fixture, then ask @AJ", []string{"AJ"}},
		{"fenced code is not a mention", "```\n@Tyler fixture\n```\n@AJ owns it", []string{"AJ"}},
		{"non-roster names skipped", "@zelda and @Joel", []string{"Joel"}},
		{"bare at sign", "meet @ 3pm", nil},
		{"start and end of text", "@Erick ping @Tim", []string{"Erick", "Tim"}},
		{"no mentions", "plain channel banter", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := chatMentionNames(tc.text)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("chatMentionNames(%q)=%v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestScoutChatMentionParserAdversarialBoundaries(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"direct lowercase", "@scout catch me up", true},
		{"direct mixed case", "hey @ScOuT, catch me up", true},
		{"email domain", "send it to aj@scout.com", false},
		{"email local punctuation", "send it to ops+alerts@scout.io", false},
		{"longer underscore handle", "ask @scout_bot", false},
		{"longer dot handle", "ask @scout.dev", false},
		{"escaped historical mention", `the literal is \@scout`, false},
		{"quoted historical mention", `Tim said "@scout catch us up" yesterday`, false},
		{"curly historical mention", "Tim said “@scout catch us up” yesterday", false},
		{"markdown historical quote", "> @scout catch us up\nI agree", false},
		{"inline code", "type `@scout` in the demo", false},
		{"fenced code", "```text\n@scout\n```", false},
		{"quoted then direct", `Tim said "@scout old request"; @scout new request`, true},
		{"quote line then direct", "> @scout old request\n@scout new request", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scoutChatMentionsScout(tc.text); got != tc.want {
				t.Fatalf("scoutChatMentionsScout(%q)=%v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestScoutChatMentionReadsAuthoredTextOnly(t *testing.T) {
	message := scoutChatMessageRecord{
		Text: "plain human message",
		Files: []scoutChatFileAttachment{{
			Name: "@scout-plan.pdf",
			Kind: "gif",
			Text: "derived attachment says @scout launch research",
			Ref:  "sha256:@scout",
		}},
		ReplyTo: &scoutChatReplyRef{Text: "historical @scout request"},
		Sources: []answerSource{{Quote: "@scout was cited"}},
	}
	if scoutChatMessageMentionsScout(message) {
		t.Fatal("attachment, GIF, reply, and source metadata must not summon Scout")
	}
	message.Text = "@scout please inspect the attached plan"
	if !scoutChatMessageMentionsScout(message) {
		t.Fatal("a direct authored-text mention should still summon Scout")
	}
}

// A public-channel message that @-mentions roster members creates one targeted,
// thread-deep-linked notification per mentioned user — never the author, never
// unmentioned accounts — and the whole path is model-free (keyless-safe).
func TestScoutChatChannelMentionNotifiesMentionedUsers(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("mention notifications must never invoke the model")
		return "", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "warroom", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	user := accountStore().findUser("tim@shareability.com")
	if user == nil {
		t.Fatal("seed user tim@shareability.com missing")
	}

	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, channel.ID, "@AJ @tyler can you review the pilot cut? cc @Tim", nil, "")
	if err != nil {
		t.Fatalf("append channel message: %v", err)
	}
	if _, ok := response["answer"]; ok {
		t.Fatalf("response=%#v, want no scout answer for user-only mentions", response)
	}

	for _, email := range []string{"aj@shareability.com", "tyler@shareability.com"} {
		unread := kanbanApp.unreadNotificationsFor(email, notificationListLimit)
		if len(unread) != 1 {
			t.Fatalf("%s unread=%#v, want exactly one mention notification", email, unread)
		}
		entry := unread[0]
		if entry["userEmail"] != email {
			t.Fatalf("notification=%#v, want targeted to %s", entry, email)
		}
		if entry["kind"] != notificationKindChat || entry["tool"] != "chat" || entry["threadId"] != channel.ID {
			t.Fatalf("notification=%#v, want chat kind + thread deep link", entry)
		}
		text := asString(entry["text"])
		if !strings.Contains(text, "Tim mentioned you in #warroom") || !strings.Contains(text, "review the pilot cut") {
			t.Fatalf("notification text=%q, want author, channel title, and excerpt", text)
		}
	}

	// The author's self-mention (@Tim) never rings their own bell, and an
	// unmentioned roster member hears nothing.
	for _, email := range []string{"tim@shareability.com", "joel@shareability.com"} {
		if unread := kanbanApp.unreadNotificationsFor(email, notificationListLimit); len(unread) != 0 {
			t.Fatalf("%s unread=%#v, want none", email, unread)
		}
	}

	// A second plain message without mentions rings nobody.
	if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, channel.ID, "sending the notes now", nil, ""); err != nil {
		t.Fatalf("append plain message: %v", err)
	}
	if unread := kanbanApp.unreadNotificationsFor("tyler@shareability.com", notificationListLimit); len(unread) != 1 {
		t.Fatalf("tyler unread=%#v, want still exactly the one mention", unread)
	}
}

func TestScoutChatChannelReplyNotifiesOriginalAuthorOnce(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "warroom", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	seedScoutChatUserMessage(t, channel.ID, "aj@shareability.com", "tim-original", "tim@shareability.com", "I can own the first pass.")
	aj := accountStore().findUser("aj@shareability.com")
	if aj == nil {
		t.Fatal("seed user AJ missing")
	}
	// The explicit @Tim and reply target compose into one notification, not two.
	if _, err := kanbanApp.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), aj, channel.ID, "@Tim perfect, thank you.", nil, "", "tim-original", ""); err != nil {
		t.Fatalf("append reply: %v", err)
	}
	unread := kanbanApp.unreadNotificationsFor("tim@shareability.com", notificationListLimit)
	if len(unread) != 1 {
		t.Fatalf("Tim unread=%#v, want one deduped reply notification", unread)
	}
	if text := asString(unread[0]["text"]); !strings.Contains(text, "AJ replied to you in #warroom") || !strings.Contains(text, "perfect") {
		t.Fatalf("reply notification text=%q", text)
	}
}

// @scout keeps its answer-gate role in channels, and user mentions in the same
// message still notify: the two systems compose instead of colliding.
func TestScoutChatChannelScoutMentionAnswersAndUserMentionNotifies(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.mu.Lock()
	kanbanApp.apiKey = "test-key"
	kanbanApp.mu.Unlock()
	t.Cleanup(func() { kanbanApp = previousApp })

	modelCalls := 0
	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		modelCalls++
		return "Scout answer from the channel.", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "warroom", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	user := accountStore().findUser("tim@shareability.com")
	if user == nil {
		t.Fatal("seed user tim@shareability.com missing")
	}

	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, channel.ID, "@scout what did we decide? @Tyler you were there too", nil, "")
	if err != nil {
		t.Fatalf("append mention message: %v", err)
	}
	if modelCalls != 1 {
		t.Fatalf("modelCalls=%d, want 1 for the @scout mention", modelCalls)
	}
	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || answer.Role != "scout" || !strings.Contains(answer.Text, "Scout answer") {
		t.Fatalf("answer=%#v, want scout reply alongside the mention notification", response["answer"])
	}

	tylerUnread := kanbanApp.unreadNotificationsFor("tyler@shareability.com", notificationListLimit)
	if len(tylerUnread) != 1 || tylerUnread[0]["threadId"] != channel.ID {
		t.Fatalf("tyler unread=%#v, want one deep-linked mention notification", tylerUnread)
	}
	// Scout is a gate, not a recipient: no notification records a scout target.
	kanbanApp.mu.Lock()
	total := len(kanbanApp.notifications)
	kanbanApp.mu.Unlock()
	if total != 1 {
		t.Fatalf("notifications=%d, want only Tyler's mention", total)
	}
}

func TestScoutChatResponseStyleIsExclusiveToTheTable(t *testing.T) {
	table := scoutChatThreadRecord{Title: "team", Visibility: scoutChatVisibilityPublic, Table: true}
	if got := scoutChatResponseStyle(table); got != tableScoutChatResponseStyle || !strings.Contains(got, "casual group chat") {
		t.Fatalf("Table response style=%q, want the casual #team contract", got)
	}
	for name, thread := range map[string]scoutChatThreadRecord{
		"same title without durable Table identity": {Title: "team", Visibility: scoutChatVisibilityPublic},
		"another public channel":                    {Title: "warroom", Visibility: scoutChatVisibilityPublic},
		"private Scout chat":                        {Title: "Scout", Visibility: scoutChatVisibilityPrivate, Table: true},
	} {
		if got := scoutChatResponseStyle(thread); got != "" {
			t.Fatalf("%s style=%q, want no #team voice outside the flagged public Table", name, got)
		}
	}
}

func TestTableScoutMentionAddsCasualStyleToModelInstructions(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.mu.Lock()
	kanbanApp.apiKey = "test-key"
	kanbanApp.mu.Unlock()
	t.Cleanup(func() { kanbanApp = previousApp })

	var captured openAITextRequest
	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow == "scout_route" {
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Route: "inline"}), nil
		}
		captured = request
		return "yeah — that tracks. i’d keep the first pass small.", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	table, err := kanbanApp.ensureTable("aj@shareability.com")
	if err != nil {
		t.Fatalf("ensure Table: %v", err)
	}
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user AJ missing")
	}
	if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, table.ID, "@scout quick gut check on cobalt banana?", nil, ""); err != nil {
		t.Fatalf("append Table mention: %v", err)
	}
	if !strings.Contains(captured.Instructions, tableScoutChatResponseStyle) {
		t.Fatalf("instructions=%q, want the #team-only casual style", captured.Instructions)
	}
}

// Private threads are a 1:1 with Scout: mentions there page nobody, and Scout
// still answers without any mention.
func TestScoutChatPrivateThreadMentionsDoNotNotify(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.mu.Lock()
	kanbanApp.apiKey = "test-key"
	kanbanApp.mu.Unlock()
	t.Cleanup(func() { kanbanApp = previousApp })

	modelCalls := 0
	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow == "scout_route" {
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Route: "inline"}), nil
		}
		modelCalls++
		return "Scout private answer.", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	private, err := kanbanApp.createScoutChatThread("tim@shareability.com", "Tim", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	user := accountStore().findUser("tim@shareability.com")
	if user == nil {
		t.Fatal("seed user tim@shareability.com missing")
	}

	if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, private.ID, "draft a note to @Tyler and @AJ about the pilot", nil, ""); err != nil {
		t.Fatalf("append private message: %v", err)
	}
	if modelCalls != 1 {
		t.Fatalf("modelCalls=%d, want the private always-answer behavior intact", modelCalls)
	}
	for _, email := range []string{"tyler@shareability.com", "aj@shareability.com"} {
		if unread := kanbanApp.unreadNotificationsFor(email, notificationListLimit); len(unread) != 0 {
			t.Fatalf("%s unread=%#v, want no notifications from a private thread", email, unread)
		}
	}
}
