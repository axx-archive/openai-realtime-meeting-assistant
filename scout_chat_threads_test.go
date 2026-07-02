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
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, channel.ID, "let's research the market together at 3pm", nil)
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
	response, err = kanbanApp.appendScoutChatThreadMessage(context.Background(), user, channel.ID, "@Scout what did we decide yesterday?", nil)
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

	// Agent-mode keyword launches in channels also require the mention.
	response, err = kanbanApp.appendScoutChatThreadMessage(context.Background(), user, channel.ID, "@scout research the rodeo creator market", nil)
	if err != nil {
		t.Fatalf("append mention launch message: %v", err)
	}
	if _, ok := response["agentThread"]; !ok {
		t.Fatalf("response keys=%v, want agent thread launch on @scout research", responseKeys(response))
	}

	// Private threads keep always-answer behavior with no mention.
	private, err := kanbanApp.createScoutChatThread("tim@shareability.com", "Tim", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	modelCalls = 0
	if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, private.ID, "what did we decide yesterday?", nil); err != nil {
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
			if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, thread.ID, text, nil); err != nil {
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

func responseKeys(response map[string]any) []string {
	keys := make([]string, 0, len(response))
	for key := range response {
		keys = append(keys, key)
	}
	return keys
}
