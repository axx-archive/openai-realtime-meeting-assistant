package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func waitForScoutOpeningReplyState(t *testing.T, app *kanbanBoardApp, threadID string, state string) scoutChatThreadRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		thread, ok := app.scoutOpeningThreadByID(threadID)
		if ok && thread.OpeningOperation != nil {
			index := scoutChatMessageIndex(thread, thread.OpeningOperation.ReplyMessageID)
			if index >= 0 && thread.Messages[index].Reply != nil && thread.Messages[index].Reply.State == state {
				return thread
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("thread %s did not reach reply state %s", threadID, state)
	return scoutChatThreadRecord{}
}

func TestEnsureScoutHomeOpeningIsAtomicAndIdempotent(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { _ = app.Close() })
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}

	thread, created, err := app.ensureScoutHomeOpening(user, "opening-key-1", "Help me think through pricing")
	if err != nil || !created {
		t.Fatalf("ensure opening created=%v err=%v", created, err)
	}
	if scoutChatThreadVisibility(thread) != scoutChatVisibilityPrivate || len(thread.Messages) != 2 || thread.OpeningOperation == nil {
		t.Fatalf("atomic thread=%+v", thread)
	}
	if thread.Messages[0].Role != "user" || thread.Messages[0].Text != "Help me think through pricing" {
		t.Fatalf("opening message=%+v", thread.Messages[0])
	}
	if thread.Messages[1].Reply == nil || thread.Messages[1].Reply.State != scoutReplyStateQueued || thread.Messages[1].Reply.InReplyTo != thread.Messages[0].ID {
		t.Fatalf("reply placeholder=%+v", thread.Messages[1])
	}
	if thread.Title != "Help me think through pricing" {
		t.Fatalf("title=%q", thread.Title)
	}

	replayed, created, err := app.ensureScoutHomeOpening(user, "opening-key-1", "Help me think through pricing")
	if err != nil || created || replayed.ID != thread.ID || len(replayed.Messages) != 2 {
		t.Fatalf("replay created=%v err=%v thread=%+v", created, err, replayed)
	}
	if _, _, err := app.ensureScoutHomeOpening(user, "opening-key-1", "Different text"); !errors.Is(err, errScoutOpeningConflict) {
		t.Fatalf("conflicting replay err=%v", err)
	}

	projected := app.projectScoutChatThreadForViewer(user.Email, thread)
	if projected.OpeningOperation != nil {
		t.Fatal("opening operation leaked through viewer projection")
	}
}

func TestEnsureScoutHomeOpeningConcurrentDuplicatesAndOwnerIsolation(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { _ = app.Close() })
	owner := accountStore().findUser("aj@shareability.com")
	other := accountStore().findUser("tim@shareability.com")
	if owner == nil || other == nil {
		t.Fatal("seed users missing")
	}

	const workers = 24
	var wg sync.WaitGroup
	results := make(chan scoutChatThreadRecord, workers)
	created := make(chan bool, workers)
	errs := make(chan error, workers)
	for index := 0; index < workers; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			thread, wasCreated, err := app.ensureScoutHomeOpening(owner, "concurrent-key", "One durable turn")
			results <- thread
			created <- wasCreated
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(created)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent ensure: %v", err)
		}
	}
	createdCount := 0
	for value := range created {
		if value {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count=%d, want exactly one", createdCount)
	}
	threadID := ""
	for thread := range results {
		if threadID == "" {
			threadID = thread.ID
		}
		if thread.ID != threadID || len(thread.Messages) != 2 {
			t.Fatalf("duplicate result=%+v", thread)
		}
	}
	otherThread, wasCreated, err := app.ensureScoutHomeOpening(other, "concurrent-key", "One durable turn")
	if err != nil || !wasCreated || otherThread.ID == threadID {
		t.Fatalf("owner-isolated thread=%+v created=%v err=%v", otherThread, wasCreated, err)
	}
	if _, _, err := app.scoutChatThreadByID(other.Email, threadID); err == nil {
		t.Fatal("another owner read a private atomic Scout thread")
	}
}

func TestScoutHomeOpeningHandlerReturnsBeforeProviderAndCompletesPlaceholder(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-test-key"
	kanbanApp = app
	t.Cleanup(func() {
		_ = app.Close()
		kanbanApp = previousApp
	})
	t.Setenv("OPENAI_API_KEY", "openai-test-key")

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		once.Do(func() { close(started) })
		<-release
		if request.Workflow == "scout_route" {
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Route: "inline"}), nil
		}
		if request.Workflow == "scout_chat" {
			return "Pricing changed because the pilot scope expanded.", nil
		}
		return "", errors.New("unexpected workflow")
	})

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	request := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads", strings.NewReader(`{"openingMessage":{"text":"Why did pricing change?"}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "home-send-1")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantChatThreadsHandler(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Thread scoutChatThreadRecord `json:"thread"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Thread.ID == "" || len(payload.Thread.Messages) != 2 || payload.Thread.Messages[1].Reply == nil {
		t.Fatalf("accepted payload=%+v", payload.Thread)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("reply worker did not start")
	}
	select {
	case <-time.After(50 * time.Millisecond):
		// The HTTP response already returned while the provider remains blocked.
	case <-release:
		t.Fatal("provider release channel closed unexpectedly")
	}
	close(release)

	completed := waitForScoutOpeningReplyState(t, app, payload.Thread.ID, scoutReplyStateCompleted)
	if len(completed.Messages) != 2 || completed.Messages[1].ID != payload.Thread.Messages[1].ID || !strings.Contains(completed.Messages[1].Text, "pilot scope") {
		t.Fatalf("completed thread=%+v", completed)
	}
}

func TestScoutHomeOpeningTransportAcceptsMaxEscapedText(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() {
		_ = app.Close()
		kanbanApp = previousApp
	})
	text := strings.Repeat("\x01", scoutHomeOpeningMaxRunes)
	body, err := json.Marshal(map[string]any{"openingMessage": map[string]any{"text": text}})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) <= 16<<10 {
		t.Fatalf("fixture body=%d, want proof above the old transport cap", len(body))
	}
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	request := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "escaped-max-opening")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantChatThreadsHandler(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestScoutHomeOpeningFailureIsSafeAndRetryDoesNotDuplicateUserMessage(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-test-key"
	t.Cleanup(func() { _ = app.Close() })
	t.Setenv("OPENAI_API_KEY", "openai-test-key")
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}

	var mu sync.Mutex
	failAnswer := true
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow == "scout_route" {
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Route: "inline"}), nil
		}
		mu.Lock()
		fail := failAnswer
		mu.Unlock()
		if fail {
			return "", &openAIProviderFailure{err: errors.New("temporary network outage")}
		}
		return "Recovered answer.", nil
	})

	thread, _, err := app.ensureScoutHomeOpening(user, "retry-key", "Can you answer this?")
	if err != nil {
		t.Fatalf("ensure opening: %v", err)
	}
	app.queueScoutOpeningReply(thread.ID)
	failed := waitForScoutOpeningReplyState(t, app, thread.ID, scoutReplyStateFailed)
	if len(failed.Messages) != 2 || failed.Messages[1].Text != scoutReplySafeFailureText || !failed.Messages[1].Reply.Retryable {
		t.Fatalf("failed thread=%+v", failed)
	}

	mu.Lock()
	failAnswer = false
	mu.Unlock()
	if _, _, err := app.retryScoutOpeningReply(user.Email, thread.ID, failed.Messages[1].ID); err != nil {
		t.Fatalf("retry reply: %v", err)
	}
	completed := waitForScoutOpeningReplyState(t, app, thread.ID, scoutReplyStateCompleted)
	if len(completed.Messages) != 2 || completed.Messages[0].ID != thread.Messages[0].ID || completed.Messages[1].ID != thread.Messages[1].ID || completed.Messages[1].Text != "Recovered answer." {
		t.Fatalf("retry duplicated or replaced identities: %+v", completed)
	}
}

func TestScoutHomeOpeningQueuedReplyRecoversAfterRestart(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	thread, _, err := app.ensureScoutHomeOpening(user, "restart-key", "Recover this queued answer")
	if err != nil {
		t.Fatalf("ensure opening: %v", err)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("close first app: %v", err)
	}

	restarted := newKanbanBoardApp()
	restarted.apiKey = "openai-test-key"
	t.Cleanup(func() { _ = restarted.Close() })
	t.Setenv("OPENAI_API_KEY", "openai-test-key")
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow == "scout_route" {
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Route: "inline"}), nil
		}
		return "Recovered after restart.", nil
	})
	restarted.startScoutOpeningReplyWorkers()
	completed := waitForScoutOpeningReplyState(t, restarted, thread.ID, scoutReplyStateCompleted)
	if len(completed.Messages) != 2 || completed.Messages[1].Text != "Recovered after restart." {
		t.Fatalf("recovered thread=%+v", completed)
	}
}

func TestScoutHomeOpeningProposalPreservesRouterProvenance(t *testing.T) {
	setupAuthTestEnv(t)
	resetCapabilityRuntimeForTest(t)
	dir := ledgerTestDir(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-test-key"
	t.Cleanup(func() { _ = app.Close() })
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}

	thread, _, err := app.ensureScoutHomeOpening(user, "provenance-key", "package this end to end")
	if err != nil {
		t.Fatalf("ensure opening: %v", err)
	}
	app.queueScoutOpeningReply(thread.ID)
	completed := waitForScoutOpeningReplyState(t, app, thread.ID, scoutReplyStateCompleted)
	if completed.Messages[1].Proposal == nil {
		t.Fatalf("completed reply=%+v, want proposal", completed.Messages[1])
	}

	minted := filterLedgerEvents(readRouterLedgerEvents(t, dir), telemetryTypeProposal, proposalEventMinted)
	if len(minted) != 1 {
		t.Fatalf("minted events=%d, want exactly one", len(minted))
	}
	fields := ledgerEventFields(minted[0])
	if fields["source"] != proposalSourceDeterministicGuard || fields["proposal_id"] != completed.Messages[1].ID {
		t.Fatalf("mint fields=%v, want deterministic provenance on reply id", fields)
	}
	if state := capabilityState(capabilityTypedScoutRouter); !state.LastSuccess.IsZero() {
		t.Fatalf("deterministic guard manufactured provider success: %+v", state)
	}
}

func TestScoutHomeOpeningMutationsFenceQueuedAndRunningReplies(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { _ = app.Close() })
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}

	t.Run("edit cancels running and discards late completion", func(t *testing.T) {
		thread, _, err := app.ensureScoutHomeOpening(user, "mutation-edit", "Original opening")
		if err != nil {
			t.Fatalf("ensure opening: %v", err)
		}
		_, _, leaseID, claimed := app.claimScoutOpeningReply(thread.ID)
		if !claimed {
			t.Fatal("claim running reply")
		}
		changed := "Edited opening"
		edited, _, err := app.editScoutChatThreadMessage(context.Background(), user, thread.ID, thread.OpeningOperation.UserMessageID, &changed, nil)
		if err != nil {
			t.Fatalf("edit opening: %v", err)
		}
		replyIndex := scoutChatMessageIndex(edited, thread.OpeningOperation.ReplyMessageID)
		if replyIndex < 0 || edited.Messages[replyIndex].Reply == nil || edited.Messages[replyIndex].Reply.State != scoutReplyStateCanceled {
			t.Fatalf("edited thread=%+v, want canceled reply", edited)
		}
		app.finishScoutOpeningReply(thread.ID, leaseID, scoutChatMessageRecord{Kind: "message", Role: "scout", Text: "late answer"}, nil)
		current, _ := app.scoutOpeningThreadByID(thread.ID)
		if current.Messages[replyIndex].Reply.State != scoutReplyStateCanceled || strings.Contains(current.Messages[replyIndex].Text, "late answer") {
			t.Fatalf("late completion escaped CAS: %+v", current.Messages[replyIndex])
		}
	})

	t.Run("delete removes opening and placeholder", func(t *testing.T) {
		thread, _, err := app.ensureScoutHomeOpening(user, "mutation-delete", "Delete this opening")
		if err != nil {
			t.Fatalf("ensure opening: %v", err)
		}
		deleted, err := app.deleteScoutChatThreadMessage(user.Email, thread.ID, thread.OpeningOperation.UserMessageID)
		if err != nil {
			t.Fatalf("delete opening: %v", err)
		}
		if deleted.OpeningOperation != nil || len(deleted.Messages) != 0 {
			t.Fatalf("deleted thread=%+v, want opening pair removed", deleted)
		}
	})

	t.Run("archive cancels queued reply", func(t *testing.T) {
		thread, _, err := app.ensureScoutHomeOpening(user, "mutation-archive", "Archive this opening")
		if err != nil {
			t.Fatalf("ensure opening: %v", err)
		}
		archived, err := app.setScoutChatThreadArchived(user.Email, thread.ID, true)
		if err != nil {
			t.Fatalf("archive opening: %v", err)
		}
		replyIndex := scoutChatMessageIndex(archived, thread.OpeningOperation.ReplyMessageID)
		if replyIndex < 0 || archived.Messages[replyIndex].Reply == nil || archived.Messages[replyIndex].Reply.State != scoutReplyStateCanceled {
			t.Fatalf("archived thread=%+v, want canceled reply", archived)
		}
	})
}

func TestRecoveredScoutOpeningReplyCannotConsumeOrClobberLaterTurns(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "openai-test-key"
	t.Cleanup(func() { _ = app.Close() })
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user missing")
	}
	thread, _, err := app.ensureScoutHomeOpening(user, "causal-cutoff", "Answer the opening question")
	if err != nil {
		t.Fatalf("ensure opening: %v", err)
	}

	lock := app.scoutChatThreadLock(thread.ID)
	lock.Lock()
	current, ok := app.scoutOpeningThreadByID(thread.ID)
	if !ok {
		lock.Unlock()
		t.Fatal("opening thread missing")
	}
	now := time.Now().UTC()
	laterUser := scoutChatMessageRecord{ID: "later-user", Kind: "message", Role: "user", Text: "LATER CAUSAL CONTAMINANT", CreatedAt: now.Format(time.RFC3339Nano), AuthorEmail: user.Email}
	laterReply := scoutChatMessageRecord{ID: "later-scout", Kind: "message", Role: "scout", Text: "Newest preview must survive", CreatedAt: now.Add(time.Millisecond).Format(time.RFC3339Nano)}
	current.Messages = append(current.Messages, laterUser, laterReply)
	updateScoutChatThreadSummary(&current, laterUser, laterReply)
	if err := app.saveScoutChatThread(current); err != nil {
		lock.Unlock()
		t.Fatalf("save later turn: %v", err)
	}
	previewBefore, updatedBefore := current.Preview, current.UpdatedAt
	lock.Unlock()

	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if strings.Contains(fmt.Sprint(request.Input), "LATER CAUSAL CONTAMINANT") {
			t.Fatalf("opening provider input consumed a later turn: %v", request.Input)
		}
		switch request.Workflow {
		case "scout_route":
			return openAIScoutRouteJSON(t, openAIScoutRouterOutput{Route: "inline"}), nil
		case "scout_chat":
			return "Opening answer arrived late.", nil
		default:
			return "", fmt.Errorf("unexpected workflow %q", request.Workflow)
		}
	})
	claimedThread, openingMessage, leaseID, claimed := app.claimScoutOpeningReply(thread.ID)
	if !claimed {
		t.Fatal("claim delayed opening reply")
	}
	resolved, err := app.resolveScoutOpeningReply(context.Background(), user, claimedThread, openingMessage)
	if err != nil {
		t.Fatalf("resolve delayed opening: %v", err)
	}
	app.finishScoutOpeningReply(thread.ID, leaseID, resolved, nil)

	finished, _ := app.scoutOpeningThreadByID(thread.ID)
	if finished.Preview != previewBefore || finished.UpdatedAt != updatedBefore {
		t.Fatalf("late opening clobbered newer summary: preview=%q/%q updated=%q/%q", finished.Preview, previewBefore, finished.UpdatedAt, updatedBefore)
	}
	if finished.Messages[len(finished.Messages)-1].Text != laterReply.Text {
		t.Fatalf("newest message moved or changed: %+v", finished.Messages)
	}
}
