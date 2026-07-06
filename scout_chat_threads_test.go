package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestScoutChatChannelVisibilityAccessControl(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Private notes", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "launch plan", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if channel.Visibility != scoutChatVisibilityPublic {
		t.Fatalf("channel visibility=%q, want public", channel.Visibility)
	}

	// Owner sees both; another signed-in user sees only the public channel.
	ownerThreads := kanbanApp.scoutChatThreadsSnapshot("aj@shareability.com", false, 100)
	if len(ownerThreads) != 2 {
		t.Fatalf("owner threads=%d, want 2", len(ownerThreads))
	}
	otherThreads := kanbanApp.scoutChatThreadsSnapshot("tim@shareability.com", false, 100)
	if len(otherThreads) != 1 || otherThreads[0].ID != channel.ID {
		t.Fatalf("other user threads=%#v, want only the public channel", otherThreads)
	}

	if _, _, err := kanbanApp.scoutChatThreadByID("tim@shareability.com", private.ID); err == nil {
		t.Fatal("expected private thread to be hidden from another user")
	}
	if _, _, err := kanbanApp.scoutChatThreadByID("tim@shareability.com", channel.ID); err != nil {
		t.Fatalf("public channel should be readable by any signed-in user: %v", err)
	}

	// The GET handler exposes the channel (grouped by visibility field) too.
	listReq := httptest.NewRequest(http.MethodGet, "/assistant/chat-threads", nil)
	for _, cookie := range loginAs(t, "tim@shareability.com", "B0NFIRE!") {
		listReq.AddCookie(cookie)
	}
	listRecorder := httptest.NewRecorder()
	assistantChatThreadsHandler(listRecorder, listReq)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s, want %d", listRecorder.Code, listRecorder.Body.String(), http.StatusOK)
	}
	var listPayload struct {
		Threads []scoutChatThreadRecord `json:"threads"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listPayload.Threads) != 1 || listPayload.Threads[0].ID != channel.ID || listPayload.Threads[0].Visibility != scoutChatVisibilityPublic {
		t.Fatalf("threads=%#v, want the public channel with visibility field", listPayload.Threads)
	}

	// Archive stays creator-only for public channels.
	if _, err := kanbanApp.setScoutChatThreadArchived("tim@shareability.com", channel.ID, true); err == nil {
		t.Fatal("expected non-creator archive of a channel to fail")
	}
	archived, err := kanbanApp.setScoutChatThreadArchived("aj@shareability.com", channel.ID, true)
	if err != nil {
		t.Fatalf("creator archive: %v", err)
	}
	if archived.ArchivedAt == "" {
		t.Fatalf("archived=%#v, want archivedAt stamp", archived)
	}
}

func TestScoutChatChannelScoutAnswersOnlyWhenMentioned(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.mu.Lock()
	kanbanApp.apiKey = "test-key"
	kanbanApp.mu.Unlock()
	t.Cleanup(func() { kanbanApp = previousApp })

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	modelCalls := 0
	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		modelCalls++
		return "Scout answer from the channel.", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "launch plan", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	user := accountStore().findUser("tim@shareability.com")
	if user == nil {
		t.Fatal("seed user tim@shareability.com missing")
	}

	// A plain human message — even one carrying agent-mode keywords — must not
	// summon Scout or launch an agent thread.
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, channel.ID, "let's research the market together at 3pm", nil, "")
	if err != nil {
		t.Fatalf("append channel message: %v", err)
	}
	if modelCalls != 0 {
		t.Fatalf("modelCalls=%d, want 0 for unmentioned channel message", modelCalls)
	}
	if _, ok := response["answer"]; ok {
		t.Fatalf("response=%#v, want no scout answer without @scout", response)
	}
	if _, ok := response["agentThread"]; ok {
		t.Fatalf("response=%#v, want no agent launch without @scout", response)
	}
	saved := response["thread"].(scoutChatThreadRecord)
	if len(saved.Messages) != 1 {
		t.Fatalf("messages=%d, want just the human message", len(saved.Messages))
	}
	if saved.Messages[0].AuthorEmail != "tim@shareability.com" || saved.Messages[0].AuthorName == "" {
		t.Fatalf("message=%#v, want author identity stamped server-side", saved.Messages[0])
	}

	// An @scout mention (case-insensitive) gets an answer.
	response, err = kanbanApp.appendScoutChatThreadMessage(context.Background(), user, channel.ID, "@Scout what did we decide yesterday?", nil, "")
	if err != nil {
		t.Fatalf("append mention message: %v", err)
	}
	if modelCalls != 1 {
		t.Fatalf("modelCalls=%d, want 1 after @scout mention", modelCalls)
	}
	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || answer.Role != "scout" || !strings.Contains(answer.Text, "Scout answer") {
		t.Fatalf("answer=%#v, want scout reply", response["answer"])
	}

	// D5: an @scout mention plus a bare workstream keyword routes to that
	// workstream — the mention is itself the explicit invocation.
	response, err = kanbanApp.appendScoutChatThreadMessage(context.Background(), user, channel.ID, "@scout research the rodeo creator market", nil, "")
	if err != nil {
		t.Fatalf("append mention keyword message: %v", err)
	}
	if _, ok := response["agentThread"]; !ok {
		t.Fatalf("response keys=%v, want @scout + research keyword to launch the workstream (D5)", responseKeys(response))
	}
	if modelCalls != 1 {
		t.Fatalf("modelCalls=%d, want no conversational model call for a workstream launch", modelCalls)
	}
	launchReply, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || !strings.Contains(launchReply.Text, "research workstream kicked off") {
		t.Fatalf("answer=%#v, want the design workstream reply copy", response["answer"])
	}

	// The explicit prefix after the mention launches an agent run.
	response, err = kanbanApp.appendScoutChatThreadMessage(context.Background(), user, channel.ID, "@scout research: the rodeo creator market", nil, "")
	if err != nil {
		t.Fatalf("append mention launch message: %v", err)
	}
	if _, ok := response["agentThread"]; !ok {
		t.Fatalf("response keys=%v, want agent thread launch on @scout research: prefix", responseKeys(response))
	}

	// Private threads keep always-answer behavior with no mention.
	private, err := kanbanApp.createScoutChatThread("tim@shareability.com", "Tim", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	modelCalls = 0
	if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, private.ID, "what did we decide yesterday?", nil, ""); err != nil {
		t.Fatalf("append private message: %v", err)
	}
	if modelCalls != 1 {
		t.Fatalf("modelCalls=%d, want 1 for private thread without mention", modelCalls)
	}
}

func TestScoutChatConcurrentAppendsBothSurvive(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.mu.Lock()
	kanbanApp.apiKey = "test-key"
	kanbanApp.mu.Unlock()
	t.Cleanup(func() { kanbanApp = previousApp })

	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		// Hold the read -> model -> save window open so both writers overlap.
		time.Sleep(80 * time.Millisecond)
		return "overlapping answer", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, text := range []string{"first concurrent question", "second concurrent question"} {
		wg.Add(1)
		go func(text string) {
			defer wg.Done()
			if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, thread.ID, text, nil, ""); err != nil {
				errs <- err
			}
		}(text)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent append: %v", err)
	}

	saved, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", thread.ID)
	if err != nil {
		t.Fatalf("reload thread: %v", err)
	}
	if len(saved.Messages) != 4 {
		t.Fatalf("messages=%d, want both user turns and both answers to survive", len(saved.Messages))
	}
	texts := make([]string, 0, len(saved.Messages))
	for _, message := range saved.Messages {
		texts = append(texts, message.Text)
	}
	joined := strings.Join(texts, "\n")
	if !strings.Contains(joined, "first concurrent question") || !strings.Contains(joined, "second concurrent question") {
		t.Fatalf("messages=%q, want both concurrent user messages persisted", joined)
	}
}

func TestDecodeScoutChatThreadEntryDefaultsVisibilityPrivate(t *testing.T) {
	entry := meetingMemoryEntry{
		ID:   "scout-chat-1",
		Kind: meetingMemoryKindScoutChat,
		Text: `{"id":"scout-chat-1","title":"Old thread","ownerEmail":"aj@shareability.com","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}`,
	}
	thread, ok := decodeScoutChatThreadEntry(entry)
	if !ok {
		t.Fatal("decode failed")
	}
	if thread.Visibility != scoutChatVisibilityPrivate {
		t.Fatalf("visibility=%q, want pre-channel entries to default private", thread.Visibility)
	}

	entry.Metadata = map[string]string{"visibility": "PUBLIC"}
	thread, ok = decodeScoutChatThreadEntry(entry)
	if !ok {
		t.Fatal("decode with metadata failed")
	}
	if thread.Visibility != scoutChatVisibilityPublic {
		t.Fatalf("visibility=%q, want metadata fallback to normalize public", thread.Visibility)
	}
}

func TestScoutChatChannelModePrefixDetection(t *testing.T) {
	for _, tt := range []struct {
		text string
		want string
	}{
		{text: "@scout grill: pressure-test the EMBERS pitch", want: "grill"},
		{text: "grill: pressure-test the pitch @scout", want: "grill"},
		{text: "@scout research: the rodeo creator market", want: "research"},
		{text: "@scout Design: onboarding flow for the package", want: "design"},
		{text: "@scout workflow: ship the EMBERS package", want: "workflow"},
		// D5: an @scout mention plus a bare workstream keyword launches that
		// workstream — the mention is the explicit invocation.
		{text: "@scout can you research the market for us?", want: "research"},
		{text: "@scout what's in the design doc?", want: "design"},
		{text: "@scout the grill run finished but I can't open the scorecard from here", want: "grill"},
		// Non-keyword @scout chatter stays conversational.
		{text: "@scout from the pressure-test scorecard artifact, list the three hardest questions", want: ""},
		{text: "let's discuss the pitch brief at 3pm @scout thoughts?", want: ""},
		{text: "@scout who owns the packetizer card?", want: ""},
		// Bare keywords WITHOUT @scout never trigger anything (D5 guard),
		// and workflow stays prefix-only.
		{text: "let's research the market together at 3pm", want: ""},
		{text: "the design review moved to friday", want: ""},
		{text: "@scout how does the goal workflow behave?", want: ""},
	} {
		if got := scoutChatThreadModeForChannelText(tt.text); got != tt.want {
			t.Fatalf("scoutChatThreadModeForChannelText(%q)=%q, want %q", tt.text, got, tt.want)
		}
	}
}

func TestAgentThreadCompletionUpdatesPersistedChatThreadRef(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.mu.Lock()
	kanbanApp.apiKey = "test-key"
	kanbanApp.mu.Unlock()
	t.Cleanup(func() { kanbanApp = previousApp })

	// Capture the launched thread instead of running it async so the test can
	// drive the worker to completion deterministically.
	var launched scoutAgentThread
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, thread scoutAgentThread) { launched = thread }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		return "Vision: rodeo creator market.\n\nGoal: research complete.\n\nVerification: artifact complete.", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	channel, err := kanbanApp.createScoutChatThread("tim@shareability.com", "Tim", "embers", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	user := accountStore().findUser("tim@shareability.com")
	if user == nil {
		t.Fatal("seed user tim@shareability.com missing")
	}
	if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, channel.ID, "@scout research: the rodeo creator market", nil, ""); err != nil {
		t.Fatalf("append launch message: %v", err)
	}
	if launched.ID == "" {
		t.Fatal("expected an agent thread launch")
	}

	ref := persistedAgentThreadRef(t, channel.ID, launched.ID)
	if ref.Status != "running" {
		t.Fatalf("ref status=%q, want running before the worker lands", ref.Status)
	}

	// The worker lands while the requester is outside the room: the persisted
	// ref must flip so the 12s chat poll completes the card.
	kanbanApp.runAgentThread(launched)

	ref = persistedAgentThreadRef(t, channel.ID, launched.ID)
	if ref.Status != "complete" {
		t.Fatalf("ref status=%q, want complete after the worker lands", ref.Status)
	}
	if ref.ArtifactID == "" {
		t.Fatal("completed ref should carry the artifact id")
	}
}

func persistedAgentThreadRef(t *testing.T, chatThreadID string, agentThreadID string) scoutChatThreadRef {
	t.Helper()
	saved, _, err := kanbanApp.scoutChatThreadByID("tim@shareability.com", chatThreadID)
	if err != nil {
		t.Fatalf("reload chat thread: %v", err)
	}
	for _, message := range saved.Messages {
		if message.Thread != nil && message.Thread.ID == agentThreadID {
			return *message.Thread
		}
	}
	t.Fatalf("chat thread %s has no persisted ref for agent thread %s", chatThreadID, agentThreadID)
	return scoutChatThreadRef{}
}

func TestScoutChatChannelAttributionSurvivesDisplayNameChange(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "embers", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	seeded := accountStore().findUser("aj@shareability.com")
	if seeded == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	renamed := *seeded
	renamed.Name = "AJ (Founder)"

	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), &renamed, channel.ID, "the package lives in 4 tools", nil, "")
	if err != nil {
		t.Fatalf("append channel message: %v", err)
	}
	saved := response["thread"].(scoutChatThreadRecord)
	message := saved.Messages[len(saved.Messages)-1]
	if message.AuthorName != "AJ (Founder)" {
		t.Fatalf("authorName=%q, want the changed display name instead of a blank author", message.AuthorName)
	}
	if message.AuthorEmail != "aj@shareability.com" {
		t.Fatalf("authorEmail=%q, want the session email", message.AuthorEmail)
	}

	// The seeded roster names still canonicalize.
	rosterUser := *seeded
	rosterUser.Name = "aj"
	if got := scoutChatAuthorName(&rosterUser); got != "AJ" {
		t.Fatalf("scoutChatAuthorName roster=%q, want canonical AJ", got)
	}
	// A blank display name still resolves through the account email.
	blankName := *seeded
	blankName.Name = "   "
	if got := scoutChatAuthorName(&blankName); got != "AJ" {
		t.Fatalf("scoutChatAuthorName blank=%q, want roster name by email", got)
	}
}

func responseKeys(response map[string]any) []string {
	keys := make([]string, 0, len(response))
	for key := range response {
		keys = append(keys, key)
	}
	return keys
}

// post_to_channel relays user words into a public channel through the normal
// commit path: room voice attributes to Scout, private voice to the real
// author (Via scout_voice), everyone gets a deep-linked bell entry, and the
// tool NEVER summons Scout's answer loop even when the text says @scout.
func TestPostToChannelPersistsAndNotifiesWithoutInvokingScout(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	// A model call anywhere in this path is a bug: the mention gate lives in
	// appendScoutChatThreadMessage, which post_to_channel bypasses.
	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("post_to_channel must never invoke the model")
		return "", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "warroom", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	// Room voice: no requester, so the relay attributes to Scout. '#' and
	// case differences are tolerated, @scout stays inert.
	result, changed, err := app.applyToolCallArgs("post_to_channel", map[string]any{
		"channel": "#Warroom",
		"text":    "@scout we agreed to ship the pilot Friday",
	})
	if err != nil {
		t.Fatalf("post_to_channel room voice: %v", err)
	}
	if changed {
		t.Fatal("post_to_channel must not report a board change")
	}
	if result["ok"] != true || result["channel"] != "warroom" || result["threadId"] != channel.ID || asString(result["messageId"]) == "" {
		t.Fatalf("result=%#v, want ok/channel/threadId/messageId", result)
	}
	if _, hasActions := result["actions"]; hasActions {
		t.Fatalf("post_to_channel must not auto-navigate anyone: %#v", result)
	}

	// Private voice: the post carries the requester's identity + mention flag.
	if _, _, err := app.applyPrivateRealtimeVoiceTool("aj@shareability.com", "post_to_channel", map[string]any{
		"channel": "warroom",
		"text":    "Tim, can you own the vendor call?",
		"mention": "Tim",
	}); err != nil {
		t.Fatalf("post_to_channel private voice: %v", err)
	}

	saved, _, err := app.scoutChatThreadByID("aj@shareability.com", channel.ID)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if len(saved.Messages) != 2 {
		t.Fatalf("channel messages=%d, want exactly the two posts (no Scout reply)", len(saved.Messages))
	}
	roomPost := saved.Messages[0]
	if roomPost.Role != "scout" || roomPost.AuthorName != scoutParticipantName || roomPost.Via != "" {
		t.Fatalf("room post=%#v, want Scout-attributed relay", roomPost)
	}
	privatePost := saved.Messages[1]
	if privatePost.Role != "user" || privatePost.AuthorEmail != "aj@shareability.com" || privatePost.Via != "scout_voice" {
		t.Fatalf("private post=%#v, want requester-attributed via scout_voice", privatePost)
	}

	// Bell: two everyone-posts plus one targeted mention, all deep-linked.
	timUnread := app.unreadNotificationsFor("tim@shareability.com", notificationListLimit)
	if len(timUnread) != 3 {
		t.Fatalf("tim unread=%#v, want 2 channel posts + 1 mention flag", timUnread)
	}
	mentionFound := false
	for _, item := range timUnread {
		if item["threadId"] != channel.ID {
			t.Fatalf("notification=%#v, want threadId deep link to the channel", item)
		}
		if strings.Contains(asString(item["text"]), "flagged you in #warroom") && item["userEmail"] == "tim@shareability.com" {
			mentionFound = true
		}
	}
	if !mentionFound {
		t.Fatal("mention must create a targeted notification for Tim")
	}
	// The targeted mention never reaches other accounts.
	for _, item := range app.unreadNotificationsFor("tyler@shareability.com", notificationListLimit) {
		if strings.Contains(asString(item["text"]), "flagged you") {
			t.Fatalf("mention notification leaked to tyler: %#v", item)
		}
	}
}

// Unknown channels error with the available names so the voice model can
// self-correct aloud; private threads are never postable.
func TestPostToChannelUnknownAndPrivateThreadsRejected(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, err := app.createScoutChatThread("aj@shareability.com", "AJ", "warroom", "public"); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if _, err := app.createScoutChatThread("aj@shareability.com", "AJ", "diary", "private"); err != nil {
		t.Fatalf("create private thread: %v", err)
	}

	_, _, err := app.applyToolCallArgs("post_to_channel", map[string]any{
		"channel": "nonexistent",
		"text":    "hello",
	})
	if err == nil || !strings.Contains(err.Error(), `no channel named "nonexistent"`) || !strings.Contains(err.Error(), "warroom") {
		t.Fatalf("unknown channel error=%v, want the available channel names listed", err)
	}

	// The private thread's title must not resolve — private threads are not
	// channels.
	if _, _, err := app.applyToolCallArgs("post_to_channel", map[string]any{
		"channel": "diary",
		"text":    "hello",
	}); err == nil {
		t.Fatal("posting to a private thread by title must be rejected")
	}
}

// create_channel needs an owner identity: the shared room voice is rejected,
// the private dashboard voice creates a public channel and notifies everyone
// with a deep link.
func TestCreateChannelByVoiceRequiresPrivateRequester(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	if _, _, err := app.applyToolCallArgs("create_channel", map[string]any{"name": "growth"}); err == nil || !strings.Contains(err.Error(), "private Scout") {
		t.Fatalf("room-voice create_channel error=%v, want private-Scout redirect", err)
	}

	result, changed, err := app.applyPrivateRealtimeVoiceTool("tim@shareability.com", "create_channel", map[string]any{"name": "#growth"})
	if err != nil {
		t.Fatalf("create_channel private voice: %v", err)
	}
	if changed {
		t.Fatal("create_channel must not report a board change")
	}
	threadID := asString(result["threadId"])
	if result["ok"] != true || result["channel"] != "growth" || threadID == "" {
		t.Fatalf("result=%#v, want ok/channel/threadId", result)
	}

	thread, _, err := app.scoutChatThreadByID("aj@shareability.com", threadID)
	if err != nil {
		t.Fatalf("channel not readable by other signed-in users: %v", err)
	}
	if scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic || normalizeAccountEmail(thread.OwnerEmail) != "tim@shareability.com" {
		t.Fatalf("thread=%#v, want a public channel owned by tim", thread)
	}

	unread := app.unreadNotificationsFor("aj@shareability.com", notificationListLimit)
	if len(unread) != 1 || unread[0]["threadId"] != threadID || !strings.Contains(asString(unread[0]["text"]), "created channel #growth") {
		t.Fatalf("unread=%#v, want one deep-linked channel-created notification", unread)
	}

	// The new channel resolves for posts immediately.
	if _, _, err := app.applyPrivateRealtimeVoiceTool("tim@shareability.com", "post_to_channel", map[string]any{
		"channel": "growth",
		"text":    "kicking this off",
	}); err != nil {
		t.Fatalf("post to freshly created channel: %v", err)
	}
}

// D7: PATCH accepts a title — owner-renamable private threads, any signed-in
// user for public channels — while the legacy archived payloads keep working.
func TestScoutChatThreadRename(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "launch plan", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	renamed, err := kanbanApp.renameScoutChatThread("aj@shareability.com", private.ID, "  simulcast recap  ")
	if err != nil {
		t.Fatalf("rename private thread: %v", err)
	}
	if renamed.Title != "simulcast recap" {
		t.Fatalf("title=%q, want trimmed rename", renamed.Title)
	}
	if renamed.UpdatedAt == private.UpdatedAt {
		t.Fatal("rename must stamp UpdatedAt")
	}
	saved, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", private.ID)
	if err != nil || saved.Title != "simulcast recap" {
		t.Fatalf("saved=%#v err=%v, want persisted rename", saved, err)
	}

	// A private thread is invisible to anyone but its owner.
	if _, err := kanbanApp.renameScoutChatThread("tim@shareability.com", private.ID, "hijack"); err == nil {
		t.Fatal("non-owner rename of a private thread must fail")
	}

	// Public channels are renamable by any signed-in user (D7).
	if _, err := kanbanApp.renameScoutChatThread("tim@shareability.com", channel.ID, "launch plan v2"); err != nil {
		t.Fatalf("channel rename by non-creator: %v", err)
	}

	// Empty titles are rejected; archived threads refuse renames.
	if _, err := kanbanApp.renameScoutChatThread("aj@shareability.com", private.ID, "   "); err == nil {
		t.Fatal("empty title must be rejected")
	}
	if _, err := kanbanApp.setScoutChatThreadArchived("aj@shareability.com", private.ID, true); err != nil {
		t.Fatalf("archive thread: %v", err)
	}
	if _, err := kanbanApp.renameScoutChatThread("aj@shareability.com", private.ID, "after archive"); err == nil {
		t.Fatal("archived thread rename must be rejected")
	}
}

// The PATCH route dispatches title payloads to rename and keeps the legacy
// archived semantics (default true on an empty body) intact.
func TestAssistantChatThreadPatchTitleRoute(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")

	renameReq := httptest.NewRequest(http.MethodPatch, "/assistant/chat-threads/"+thread.ID, strings.NewReader(`{"title":"simulcast recap"}`))
	for _, cookie := range cookies {
		renameReq.AddCookie(cookie)
	}
	renameRec := httptest.NewRecorder()
	assistantChatThreadHandler(renameRec, renameReq)
	if renameRec.Code != http.StatusOK {
		t.Fatalf("rename status=%d body=%s", renameRec.Code, renameRec.Body.String())
	}
	var renamePayload struct {
		Thread scoutChatThreadRecord `json:"thread"`
	}
	if err := json.Unmarshal(renameRec.Body.Bytes(), &renamePayload); err != nil {
		t.Fatalf("decode rename response: %v", err)
	}
	if renamePayload.Thread.Title != "simulcast recap" || renamePayload.Thread.ArchivedAt != "" {
		t.Fatalf("thread=%#v, want renamed and unarchived", renamePayload.Thread)
	}

	archiveReq := httptest.NewRequest(http.MethodPatch, "/assistant/chat-threads/"+thread.ID, strings.NewReader(`{"archived":true}`))
	for _, cookie := range cookies {
		archiveReq.AddCookie(cookie)
	}
	archiveRec := httptest.NewRecorder()
	assistantChatThreadHandler(archiveRec, archiveReq)
	if archiveRec.Code != http.StatusOK {
		t.Fatalf("archive status=%d body=%s", archiveRec.Code, archiveRec.Body.String())
	}
	var archivePayload struct {
		Thread scoutChatThreadRecord `json:"thread"`
	}
	if err := json.Unmarshal(archiveRec.Body.Bytes(), &archivePayload); err != nil {
		t.Fatalf("decode archive response: %v", err)
	}
	if archivePayload.Thread.ArchivedAt == "" || archivePayload.Thread.Title != "simulcast recap" {
		t.Fatalf("thread=%#v, want archived with title intact", archivePayload.Thread)
	}
}

// --- The propose-confirm router (spec §2, Wave 2 item 8) ---------------------

// A deliverable-shaped ask in a private thread earns a PROPOSAL — data on the
// reply and a persisted card, never a launch and never a Q&A model call. The
// routing turn itself must ride the registry: Haiku by default, tool schemas
// injected from tool_registry.go, the trust-asymmetry line in the system
// prompt.
func TestScoutChatRouterProposesToolRunNeverLaunches(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-router-test")
	t.Setenv("BONFIRE_ROUTER_MODEL", "")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {
		t.Fatal("a proposal must never launch an agent thread")
	}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	// A proposal replaces the inline answer — neither chat seam may fire.
	swapAnthropicTextResponder(t, func(context.Context, string, anthropicTextRequest) (string, error) {
		t.Fatal("a proposing turn must not also run the Q&A path")
		return "", nil
	})
	swapOpenAITextResponder(t, func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("a proposing turn must not also run the Q&A path")
		return "", nil
	})

	var routed anthropicMessagesRequest
	swapAnthropicMessagesResponder(t, func(_ context.Context, apiKey string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		if apiKey != "sk-ant-router-test" {
			t.Fatalf("router apiKey=%q, want the env key", apiKey)
		}
		routed = request
		return anthropicMessagesResponse{
			StopReason: "tool_use",
			Content: []json.RawMessage{
				mockAnthropicToolUseBlock("toolu_router", "propose_tool_run", map[string]any{
					"tool_id":   "comps_precedent",
					"objective": "comps for the rodeo doc against streaming buyers",
					"fields":    map[string]any{"thesis": "rodeo doc", "format": "film", "bogus": "dropped"},
				}),
			},
		}, nil
	})

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}

	text := "pull comps for the rodeo doc so we can price it"
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, private.ID, text, nil, "")
	if err != nil {
		t.Fatalf("append routed message: %v", err)
	}

	// The routing turn: Haiku default, registry-injected schemas, muted-agent
	// system prompt.
	if routed.Model != "claude-haiku-4-5" {
		t.Fatalf("router model=%q, want the Haiku default", routed.Model)
	}
	// No effort dial on the routing turn: claude-haiku-4-5 rejects
	// output_config.effort with a 400, which would silently degrade EVERY
	// proposal to an inline answer. Empty Effort keeps output_config off the
	// wire (see TestBuildAnthropicMessagesPayloadOmitsOutputConfigWithoutEffort).
	if routed.Effort != "" {
		t.Fatalf("router effort=%q, want empty — Haiku 4.5 rejects output_config.effort", routed.Effort)
	}
	if !strings.Contains(routed.System, "under-routes is trusted") || !strings.Contains(routed.System, "over-launches is muted") {
		t.Fatalf("router system prompt missing the trust asymmetry: %s", routed.System)
	}
	if len(routed.Tools) != 3 || routed.Tools[0].Name != "propose_tool_run" || routed.Tools[1].Name != "propose_workstream" || routed.Tools[2].Name != "offer_choices" {
		t.Fatalf("router tools=%#v, want propose_tool_run + propose_workstream + offer_choices", routed.Tools)
	}
	for _, tool := range packagingTools() {
		if !strings.Contains(routed.Tools[0].Description, tool.ID) {
			t.Errorf("router tool description missing registry tool %q — the registry must stay the single taxonomy source", tool.ID)
		}
	}

	if _, launched := response["agentThread"]; launched {
		t.Fatalf("response keys=%v — NEVER silent-launch", responseKeys(response))
	}
	proposal, ok := response["proposal"].(*scoutRouterProposal)
	if !ok {
		t.Fatalf("proposal type=%T, want *scoutRouterProposal", response["proposal"])
	}
	if proposal.Kind != scoutRouterProposalKindToolRun || proposal.ToolID != "comps_precedent" {
		t.Fatalf("proposal=%#v, want a comps_precedent tool_run", proposal)
	}
	if proposal.ToolName != "Comps & Precedent" || proposal.GroupLabel != "Ideate" {
		t.Fatalf("proposal name/group=%q/%q, want registry values", proposal.ToolName, proposal.GroupLabel)
	}
	if proposal.Authority != toolAuthorityReadOnly {
		t.Fatalf("proposal authority=%q, want the tool's registry authority", proposal.Authority)
	}
	if proposal.WeightLabel != scoutProposalWeightGoalLoop {
		t.Fatalf("weightLabel=%q, want %q", proposal.WeightLabel, scoutProposalWeightGoalLoop)
	}
	if proposal.Query != text {
		t.Fatalf("proposal query=%q, want the originating message for the Tier-0 escape", proposal.Query)
	}
	if proposal.Fields["thesis"] != "rodeo doc" || proposal.Fields["format"] != "film" {
		t.Fatalf("fields=%#v, want the model's pre-fills", proposal.Fields)
	}
	if _, leaked := proposal.Fields["bogus"]; leaked {
		t.Fatalf("fields=%#v — keys outside the tool's form definition must be dropped", proposal.Fields)
	}
	if !strings.Contains(proposal.Summary, "Comps & Precedent") || !strings.Contains(proposal.Summary, "kill condition") {
		t.Fatalf("summary=%q, want the legible tool + kill-condition sentence", proposal.Summary)
	}

	// The card is a persisted message, so a reload re-renders it.
	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || answer.Kind != scoutChatMessageKindProposal || answer.Proposal == nil {
		t.Fatalf("answer=%#v, want a persisted Kind=proposal message", response["answer"])
	}
	saved := response["thread"].(scoutChatThreadRecord)
	if len(saved.Messages) != 2 || saved.Messages[1].Proposal == nil {
		t.Fatalf("persisted messages=%#v, want user turn + proposal card", saved.Messages)
	}
}

// The heavily-biased default: a router turn that calls no tool leaves the
// existing Q&A path as the answer — the router's own text is never the reply.
func TestScoutChatRouterDefaultsToInlineAnswer(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-router-test")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	swapAnthropicMessagesResponder(t, func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		return anthropicMessagesResponse{
			StopReason: "end_turn",
			Content:    []json.RawMessage{mockAnthropicTextBlock("Tier 0 — answering inline.")},
		}, nil
	})
	swapAnthropicTextResponder(t, func(context.Context, string, anthropicTextRequest) (string, error) {
		return "we decided the market is buyers-first.", nil
	})

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}

	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, private.ID, "what did we decide about the market for this?", nil, "")
	if err != nil {
		t.Fatalf("append question: %v", err)
	}
	if _, proposed := response["proposal"]; proposed {
		t.Fatalf("response keys=%v, want no proposal for a Tier 0 verdict", responseKeys(response))
	}
	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || answer.Kind != "message" || answer.Text != "we decided the market is buyers-first." {
		t.Fatalf("answer=%#v, want the Q&A path's inline answer, not router text", response["answer"])
	}
}

// Keyless: no Anthropic key means no router turn — plain Q&A, never a
// proposal, never an error (the launchGoalThread 503 posture).
func TestScoutChatRouterKeylessSkipsRouterTurn(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.mu.Lock()
	kanbanApp.apiKey = "test-key"
	kanbanApp.mu.Unlock()
	t.Cleanup(func() { kanbanApp = previousApp })

	swapAnthropicMessagesResponder(t, func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		t.Fatal("keyless deploys must never attempt a router turn")
		return anthropicMessagesResponse{}, nil
	})
	swapOpenAITextResponder(t, func(context.Context, string, openAITextRequest) (string, error) {
		return "keyless answer.", nil
	})

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}

	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, private.ID, "research the market and draft a one-pager for the buyer", nil, "")
	if err != nil {
		t.Fatalf("keyless append must degrade to plain Q&A, got error: %v", err)
	}
	if _, proposed := response["proposal"]; proposed {
		t.Fatalf("response keys=%v, want no proposal keyless", responseKeys(response))
	}
	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || answer.Text != "keyless answer." {
		t.Fatalf("answer=%#v, want the plain Q&A answer", response["answer"])
	}
}

// A failed router turn is an optional refinement lost, not an error: the
// message still gets its inline answer.
func TestScoutChatRouterErrorDegradesToInlineAnswer(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-router-test")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	swapAnthropicMessagesResponder(t, func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		return anthropicMessagesResponse{}, fmt.Errorf("router upstream 500")
	})
	swapAnthropicTextResponder(t, func(context.Context, string, anthropicTextRequest) (string, error) {
		return "still answering.", nil
	})

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, private.ID, "grill the neon pitch for me", nil, "")
	if err != nil {
		t.Fatalf("router failure must not fail the message: %v", err)
	}
	if _, proposed := response["proposal"]; proposed {
		t.Fatalf("response keys=%v, want no proposal after a router error", responseKeys(response))
	}
	if answer, ok := response["answer"].(scoutChatMessageRecord); !ok || answer.Text != "still answering." {
		t.Fatalf("answer=%#v, want the inline answer", response["answer"])
	}
}

// Channels keep their explicit-invocation semantics: the router never runs
// there (the @scout mention + prefix/keyword rules are unchanged).
func TestScoutChatRouterSkipsPublicChannels(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-router-test")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	swapAnthropicMessagesResponder(t, func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		t.Fatal("the router must not run for channel messages")
		return anthropicMessagesResponse{}, nil
	})
	swapAnthropicTextResponder(t, func(context.Context, string, anthropicTextRequest) (string, error) {
		return "channel answer.", nil
	})

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "warroom", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	user := accountStore().findUser("tim@shareability.com")
	if user == nil {
		t.Fatal("seed user tim@shareability.com missing")
	}
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, channel.ID, "@scout what did we decide yesterday?", nil, "")
	if err != nil {
		t.Fatalf("append channel mention: %v", err)
	}
	if _, proposed := response["proposal"]; proposed {
		t.Fatalf("response keys=%v, want no proposal in channels", responseKeys(response))
	}
}

// The proposal route: a dismissal records the negative signal (toolId +
// objective payload), flips the persisted card inert, and re-asks the original
// message as Tier 0 — committing only the scout answer.
func TestScoutChatProposalDismissRecordsSignalAndAnswersTier0(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-router-test")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	var askedTier0 string
	swapAnthropicTextResponder(t, func(_ context.Context, _ string, request anthropicTextRequest) (string, error) {
		askedTier0 = request.Input
		return "the market splits buyers-first.", nil
	})

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	proposalMessage := scoutChatMessageRecord{
		ID:        "scout-chat-message-proposal-1",
		Kind:      scoutChatMessageKindProposal,
		Role:      "scout",
		Text:      "this is a Market Map run",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Proposal: &scoutRouterProposal{
			Kind:        scoutRouterProposalKindToolRun,
			ToolID:      "market_map",
			Objective:   "map the rodeo landscape",
			Query:       "how does the rodeo market break down?",
			WeightLabel: scoutProposalWeightGoalLoop,
			Summary:     "this is a Market Map run",
		},
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages("aj@shareability.com", private.ID, proposalMessage); err != nil {
		t.Fatalf("seed proposal card: %v", err)
	}

	body := `{"action":"dismissed","kind":"tool_run","toolId":"market_map","objective":"map the rodeo landscape","query":"how does the rodeo market break down?","messageId":"scout-chat-message-proposal-1"}`
	req := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+private.ID+"/proposal", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	assistantChatThreadHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		OK     bool                   `json:"ok"`
		Answer scoutChatMessageRecord `json:"answer"`
		Thread scoutChatThreadRecord  `json:"thread"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !payload.OK || payload.Answer.Text != "the market splits buyers-first." {
		t.Fatalf("payload=%+v, want the Tier-0 answer", payload)
	}
	if !strings.Contains(askedTier0, "how does the rodeo market break down?") {
		t.Fatalf("Tier-0 input=%q, want the original query re-asked", askedTier0)
	}

	// The signal (misfire economics fuel) with toolId+objective payload.
	assertRouterSignal(t, signalEventRouterProposalDismissed, signalValenceNegative, "market_map", "map the rodeo landscape")

	// The persisted card flipped inert; only the scout answer was added (no
	// duplicate user bubble).
	saved, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", private.ID)
	if err != nil {
		t.Fatalf("reload thread: %v", err)
	}
	if len(saved.Messages) != 2 {
		t.Fatalf("messages=%d, want the card + the answer only", len(saved.Messages))
	}
	if saved.Messages[0].Proposal == nil || saved.Messages[0].Proposal.Status != "dismissed" {
		t.Fatalf("card=%#v, want status dismissed persisted", saved.Messages[0].Proposal)
	}
}

// seedScoutChatProposalCard persists one proposal card in the given thread and
// returns its message id — the accept/dismiss route resolves against this
// stored record, never against request-body fields.
func seedScoutChatProposalCard(t *testing.T, threadID string, ownerEmail string, proposal scoutRouterProposal) string {
	t.Helper()
	messageID := fmt.Sprintf("scout-chat-message-proposal-%d", time.Now().UTC().UnixNano())
	card := scoutChatMessageRecord{
		ID:        messageID,
		Kind:      scoutChatMessageKindProposal,
		Role:      "scout",
		Text:      proposal.Summary,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Proposal:  &proposal,
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages(ownerEmail, threadID, card); err != nil {
		t.Fatalf("seed proposal card: %v", err)
	}
	return messageID
}

// Accepting a tool_run records the positive signal and nothing else — the
// launch rides POST /assistant/goal with the identical palette spec, so this
// route must never fork a second launch door for Tier 2. The signal payload
// comes from the STORED proposal, not the request body.
func TestScoutChatProposalAcceptToolRunRecordsSignalOnly(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {
		t.Fatal("a tool_run accept must not launch here — /assistant/goal is the only door")
	}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	messageID := seedScoutChatProposalCard(t, private.ID, "aj@shareability.com", scoutRouterProposal{
		Kind:      scoutRouterProposalKindToolRun,
		ToolID:    "comps_precedent",
		Objective: "comps for the rodeo doc",
		Query:     "pull comps for the rodeo doc",
	})

	// The request body claims a DIFFERENT tool — the stored record must win.
	response, err := kanbanApp.resolveScoutChatProposal(context.Background(), user, private.ID, scoutChatProposalAction{
		Action:    "accepted",
		Kind:      scoutRouterProposalKindWorkstream,
		ToolID:    "market_map",
		Mode:      "research",
		MessageID: messageID,
	})
	if err != nil {
		t.Fatalf("accept tool_run: %v", err)
	}
	if response["ok"] != true {
		t.Fatalf("response=%#v, want ok", response)
	}
	if _, launched := response["agentThread"]; launched {
		t.Fatalf("response keys=%v, want no launch from the proposal route for tool runs", responseKeys(response))
	}
	assertRouterSignal(t, signalEventRouterProposalAccepted, signalValencePositive, "comps_precedent", "comps for the rodeo doc")

	// A replayed accept is rejected: the card was already claimed.
	if _, err := kanbanApp.resolveScoutChatProposal(context.Background(), user, private.ID, scoutChatProposalAction{
		Action:    "accepted",
		MessageID: messageID,
	}); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("replayed accept err=%v, want already-resolved rejection", err)
	}
}

// Accepting a workstream IS the explicit confirm: the route launches the
// single-shot thread, commits the run card, and records the signal.
func TestScoutChatProposalAcceptWorkstreamLaunches(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}

	messageID := seedScoutChatProposalCard(t, private.ID, "aj@shareability.com", scoutRouterProposal{
		Kind:      scoutRouterProposalKindWorkstream,
		Mode:      "research",
		Objective: "the rodeo creator market",
		Query:     "what does the rodeo creator market look like?",
	})

	// The request body claims a DIFFERENT mode — the stored record must win.
	response, err := kanbanApp.resolveScoutChatProposal(context.Background(), user, private.ID, scoutChatProposalAction{
		Action:    "accepted",
		Kind:      scoutRouterProposalKindWorkstream,
		Mode:      "grill",
		MessageID: messageID,
	})
	if err != nil {
		t.Fatalf("accept workstream: %v", err)
	}
	agentThread, ok := response["agentThread"].(scoutAgentThread)
	if !ok || agentThread.Mode != "research" {
		t.Fatalf("response=%#v, want a running research workstream (the STORED mode)", response["agentThread"])
	}
	if agentThread.Query != "the rodeo creator market" {
		t.Fatalf("agent thread query=%q, want the stored objective", agentThread.Query)
	}
	saved := response["thread"].(scoutChatThreadRecord)
	if !scoutChatThreadHasAgentRef(saved, agentThread.ID) {
		t.Fatalf("persisted thread carries no ref to agent thread %s — status flips cannot land", agentThread.ID)
	}
	assertRouterSignal(t, signalEventRouterProposalAccepted, signalValencePositive, "research", "the rodeo creator market")

	// A replayed accept never launches a duplicate workstream.
	launches := 0
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { launches++ }
	if _, err := kanbanApp.resolveScoutChatProposal(context.Background(), user, private.ID, scoutChatProposalAction{
		Action:    "accepted",
		MessageID: messageID,
	}); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("replayed accept err=%v, want already-resolved rejection", err)
	}
	if launches != 0 {
		t.Fatalf("replayed accept launched %d workstreams, want 0", launches)
	}

	// A fabricated accept for a proposal that never existed is rejected — the
	// acceptance-rate signal only counts real router proposals.
	if _, err := kanbanApp.resolveScoutChatProposal(context.Background(), user, private.ID, scoutChatProposalAction{
		Action:    "accepted",
		Kind:      scoutRouterProposalKindWorkstream,
		Mode:      "research",
		Objective: "fabricated",
		MessageID: "scout-chat-message-never-existed",
	}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("fabricated accept err=%v, want proposal-not-found rejection", err)
	}
	// A missing message id is rejected outright.
	if _, err := kanbanApp.resolveScoutChatProposal(context.Background(), user, private.ID, scoutChatProposalAction{
		Action: "accepted",
		Kind:   scoutRouterProposalKindWorkstream,
		Mode:   "research",
	}); err == nil {
		t.Fatal("an accept without a message id must be rejected")
	}
	// And a junk action is rejected before any signal is written.
	if _, err := kanbanApp.resolveScoutChatProposal(context.Background(), user, private.ID, scoutChatProposalAction{
		Action: "maybe",
	}); err == nil {
		t.Fatal("unknown proposal action must be rejected")
	}
}

// assertRouterSignal finds exactly the newest router signal and checks its
// event, valence, and toolId/objective payload (recordSignal is the seam the
// task pins: dismissals and accepts are Q5 fuel).
func assertRouterSignal(t *testing.T, event string, valence string, toolID string, objective string) {
	t.Helper()
	for _, entry := range kanbanApp.memory.entriesOfKind(meetingMemoryKindSignal, 0) {
		record, ok := decodeSignalEntry(entry)
		if !ok || record.Event != event {
			continue
		}
		if record.Valence != valence {
			t.Fatalf("signal valence=%q, want %q", record.Valence, valence)
		}
		if record.Payload["toolId"] != toolID || record.Payload["objective"] != objective {
			t.Fatalf("signal payload=%#v, want toolId=%q objective=%q", record.Payload, toolID, objective)
		}
		return
	}
	t.Fatalf("no %s signal recorded", event)
}

// --- Palette conversational handoff carries the tool contract (§2 fidelity fix)

// A conversational palette tile (deep_research, grill_pressure_test) hands off
// to the composer; the send must carry tool.id so the launched workstream is
// contract-gated — the same tool must never produce rubric'd output from the
// Run door and generic output from the talk-it-out door.
func TestScoutChatToolTemplateHandoffCarriesToolContract(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	modelCalls := 0
	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		modelCalls++
		return "conversational answer", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}

	response, err := kanbanApp.appendScoutChatThreadMessageWithTool(context.Background(), user, private.ID, "the rodeo creator market", nil, "", "deep_research")
	if err != nil {
		t.Fatalf("append with tool template: %v", err)
	}
	if modelCalls != 0 {
		t.Fatalf("modelCalls=%d, want 0 — a tool-template send launches a workstream, not a conversational answer", modelCalls)
	}
	agentThread, ok := response["agentThread"].(scoutAgentThread)
	if !ok {
		t.Fatalf("response keys=%v, want an agentThread launch for a tool-template send", responseKeys(response))
	}
	if agentThread.Mode != "research" {
		t.Fatalf("mode=%q, want the deep_research tool's base mode research", agentThread.Mode)
	}
	meta := agentThread.Artifact.Metadata
	if meta["toolTemplate"] != "deep_research" {
		t.Fatalf("artifact toolTemplate=%q, want deep_research — without it toolPromptForThread falls back to the generic contract", meta["toolTemplate"])
	}
	if meta["objective"] != "the rodeo creator market" {
		t.Fatalf("artifact objective=%q, want the composer text", meta["objective"])
	}
	if meta["originSurface"] != "chat:"+private.ID {
		t.Fatalf("artifact originSurface=%q, want chat:%s (the return card routes on it)", meta["originSurface"], private.ID)
	}
	if meta["requestedBy"] != "aj@shareability.com" {
		t.Fatalf("artifact requestedBy=%q, want the requester email", meta["requestedBy"])
	}
	if meta["authority"] != toolAuthorityReadOnly {
		t.Fatalf("artifact authority=%q, want the tool's registry authority %q", meta["authority"], toolAuthorityReadOnly)
	}

	// The stamped template resolves through the SAME prompt machinery a goal
	// deliverable uses: the generation prompt is the assembled tool wrapper
	// (contract headings + gate rubric), not the generic per-mode scaffold.
	prompt, ok := kanbanApp.toolPromptForThread(agentThread)
	if !ok {
		t.Fatal("toolPromptForThread=false for the handoff thread — the tool contract is not riding the launch")
	}
	for _, want := range []string{"research_brief_gate_v1", "Executive Summary", "the rodeo creator market"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("assembled tool prompt missing %q", want)
		}
	}

	// The reply is the standard thread card wired to the launched artifact.
	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || answer.Kind != "thread" || answer.Thread == nil || answer.Thread.ArtifactID != agentThread.Artifact.ID {
		t.Fatalf("answer=%#v, want a Kind=thread card referencing the launched artifact", response["answer"])
	}
	saved := response["thread"].(scoutChatThreadRecord)
	if !scoutChatThreadHasAgentRef(saved, agentThread.ID) {
		t.Fatalf("persisted thread carries no ref to agent thread %s — status flips cannot land", agentThread.ID)
	}

	// An unknown template is rejected outright — never silently degraded to a
	// generic run (that silent quality fork is the bug this fixes).
	if _, err := kanbanApp.appendScoutChatThreadMessageWithTool(context.Background(), user, private.ID, "whatever", nil, "", "not_a_tool"); err == nil || !strings.Contains(err.Error(), "unknown tool template") {
		t.Fatalf("err=%v, want unknown tool template rejection", err)
	}
}

// The handoff is an explicit palette invocation, so in a public channel it
// launches without an @scout mention — exactly like an armed follow-up target.
func TestScoutChatToolTemplateLaunchesInChannelWithoutMention(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	user := accountStore().findUser("tim@shareability.com")
	if user == nil {
		t.Fatal("seed user tim@shareability.com missing")
	}
	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "launch plan", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	response, err := kanbanApp.appendScoutChatThreadMessageWithTool(context.Background(), user, channel.ID, "grill the neon signal pitch", nil, "", "grill_pressure_test")
	if err != nil {
		t.Fatalf("append with tool template: %v", err)
	}
	agentThread, ok := response["agentThread"].(scoutAgentThread)
	if !ok {
		t.Fatalf("response keys=%v, want a launch without @scout — the palette tap is the invocation", responseKeys(response))
	}
	if agentThread.Mode != "grill" || agentThread.Artifact.Metadata["toolTemplate"] != "grill_pressure_test" {
		t.Fatalf("mode=%q toolTemplate=%q, want grill/grill_pressure_test", agentThread.Mode, agentThread.Artifact.Metadata["toolTemplate"])
	}
	if agentThread.Artifact.Metadata["originKind"] != agentThreadOriginChannel {
		t.Fatalf("originKind=%q, want %q for a channel handoff", agentThread.Artifact.Metadata["originKind"], agentThreadOriginChannel)
	}
}

// The HTTP messages route decodes toolTemplate and hands it to the launch path
// — this is the wire contract the composer's fetch relies on.
func TestAssistantChatThreadMessagesRouteCarriesToolTemplate(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+thread.ID+"/messages", strings.NewReader(`{"text":"map the fintech landscape","toolTemplate":"deep_research"}`))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	assistantChatThreadHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		AgentThread scoutAgentThread   `json:"agentThread"`
		Artifact    meetingMemoryEntry `json:"artifact"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Artifact.Metadata["toolTemplate"] != "deep_research" {
		t.Fatalf("artifact toolTemplate=%q, want deep_research — the route dropped the template", payload.Artifact.Metadata["toolTemplate"])
	}
	if payload.AgentThread.ID == "" {
		t.Fatalf("body=%s, want an agentThread in the response", rec.Body.String())
	}
}

// A PROCESS id armed by the palette's conversational handoff must launch the
// goal pipeline (the identical spec the palette Run posts), never a single
// agent thread — and never the "unknown tool template" refusal that blocked
// the first live packaging_studio run (2026-07-05).
func TestScoutChatProcessTemplateHandoffLaunchesGoalPipeline(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	installFakeResponder(t, goalResponderRoutes{})

	previousRunner := startGoalThreadAsync
	startGoalThreadAsync = func(_ *kanbanBoardApp, _ string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousRunner })

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}

	response, err := kanbanApp.appendScoutChatThreadMessageWithTool(context.Background(), user, private.ID, "package Station Tenn for talent and partners", nil, "", "packaging_studio")
	if err != nil {
		t.Fatalf("append with process template: %v", err)
	}
	goalThread, ok := response["agentThread"].(scoutAgentThread)
	if !ok {
		t.Fatalf("response keys=%v, want a goal launch for a process-template send", responseKeys(response))
	}
	meta := goalThread.Artifact.Metadata
	if meta["mode"] != "goal" {
		t.Fatalf("artifact mode=%q, want goal — a process launches the pipeline, not a workstream", meta["mode"])
	}
	if meta["processId"] != "packaging_studio" {
		t.Fatalf("artifact processId=%q, want packaging_studio (processes stamp processId, not toolTemplate)", meta["processId"])
	}
	if meta["originSurface"] != "chat:"+private.ID {
		t.Fatalf("artifact originSurface=%q, want chat:%s", meta["originSurface"], private.ID)
	}
	plan, ok := decodeGoalPlan(meta["goalPlan"])
	if !ok || plan.ProcessID != "packaging_studio" {
		t.Fatalf("goal plan ProcessID=%q ok=%v, want the process instantiated", plan.ProcessID, ok)
	}
}
