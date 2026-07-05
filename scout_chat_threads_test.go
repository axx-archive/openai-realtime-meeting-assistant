package main

import (
	"context"
	"encoding/json"
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
