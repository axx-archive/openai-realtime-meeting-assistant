package main

// Card 073 — delete affordance for misplaced messages, server half. A user
// may remove THEIR OWN message from a scout thread (private or public
// channel) and from the room chat transcript; identity comes from the
// session, so nobody can delete someone else's words. An ordinary generated
// Scout answer may be causally removed with its source message, while durable
// work and unrelated Scout messages survive.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedScoutChatUserMessage(t *testing.T, threadID string, viewerEmail string, id string, authorEmail string, text string) {
	t.Helper()
	_, err := kanbanApp.commitScoutChatThreadMessages(viewerEmail, threadID, scoutChatMessageRecord{
		ID:          id,
		Kind:        "message",
		Role:        "user",
		Text:        text,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		AuthorEmail: normalizeAccountEmail(authorEmail),
	})
	if err != nil {
		t.Fatalf("seed message %s: %v", id, err)
	}
}

func TestScoutChatThreadMessageDeleteOwnMessagesOnly(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "launch plan", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	seedScoutChatUserMessage(t, channel.ID, "aj@shareability.com", "msg-aj", "aj@shareability.com", "wrong channel, meant this for ops")
	seedScoutChatUserMessage(t, channel.ID, "tim@shareability.com", "msg-tim", "tim@shareability.com", "keeping this one")
	if _, err := kanbanApp.commitScoutChatThreadMessages("aj@shareability.com", channel.ID, scoutChatMessageRecord{
		ID:        "msg-scout",
		Kind:      "message",
		Role:      "scout",
		Text:      "noted",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("seed scout reply: %v", err)
	}

	// Authz: another signed-in user cannot delete aj's message.
	if _, err := kanbanApp.deleteScoutChatThreadMessage("tim@shareability.com", channel.ID, "msg-aj"); err == nil || !strings.Contains(err.Error(), "your own") {
		t.Fatalf("cross-user delete err=%v, want the own-messages refusal", err)
	}
	// Scout's committed reply is nobody's to delete.
	if _, err := kanbanApp.deleteScoutChatThreadMessage("aj@shareability.com", channel.ID, "msg-scout"); err == nil || !strings.Contains(err.Error(), "your own") {
		t.Fatalf("scout-reply delete err=%v, want the own-messages refusal", err)
	}
	if thread, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", channel.ID); err != nil || len(thread.Messages) != 3 {
		t.Fatalf("messages=%d err=%v after refused deletes, want all 3 intact", len(thread.Messages), err)
	}

	// The author removes their own message; the persisted record loses it and
	// the preview recomputes from what remains.
	thread, err := kanbanApp.deleteScoutChatThreadMessage("aj@shareability.com", channel.ID, "msg-aj")
	if err != nil {
		t.Fatalf("own delete: %v", err)
	}
	if len(thread.Messages) != 2 {
		t.Fatalf("messages=%d after delete, want 2", len(thread.Messages))
	}
	for _, message := range thread.Messages {
		if message.ID == "msg-aj" {
			t.Fatal("deleted message still present in the returned thread")
		}
	}
	if thread.Preview != "noted" {
		t.Fatalf("preview=%q, want it recomputed from the surviving newest text", thread.Preview)
	}
	persisted, _, err := kanbanApp.scoutChatThreadByID("tim@shareability.com", channel.ID)
	if err != nil {
		t.Fatalf("re-read channel: %v", err)
	}
	if len(persisted.Messages) != 2 {
		t.Fatalf("persisted messages=%d, want the deletion durable", len(persisted.Messages))
	}

	// A miss is a 404-shaped error, not a silent success.
	if _, err := kanbanApp.deleteScoutChatThreadMessage("aj@shareability.com", channel.ID, "msg-aj"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("re-delete err=%v, want not found", err)
	}
}

func TestScoutChatAdminModeratesOnlyOrdinaryPublicAgentReplies(t *testing.T) {
	setupAuthTestEnv(t)
	_ = loginAs(t, "aj@shareability.com", "B0NFIRE!")
	_ = loginAs(t, "tim@shareability.com", "B0NFIRE!")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("tim@shareability.com", "Tim", "incident channel", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	config := strideIntegratedRuntimeConfig(t.TempDir())
	config.RecallThreadIDs = []string{channel.ID}
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatalf("create STRIDE runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	kanbanApp.strideRuntime = runtime
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := kanbanApp.commitScoutChatThreadMessages("tim@shareability.com", channel.ID,
		scoutChatMessageRecord{ID: "agent-answer", Kind: "message", Role: "scout", Text: "incorrect answer", CreatedAt: createdAt},
		scoutChatMessageRecord{ID: "human-message", Kind: "message", Role: "user", Text: "keep my words", CreatedAt: createdAt, AuthorEmail: "tim@shareability.com"},
		scoutChatMessageRecord{ID: "agent-work", Kind: "message", Role: "scout", Text: "research running", CreatedAt: createdAt, Thread: &scoutChatThreadRef{ID: "work-1"}},
	); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	member := accountStore().findUser("tim@shareability.com")
	if _, _, err := kanbanApp.moderateScoutChatThreadMessage(member, channel.ID, "agent-answer", "bad routing incident"); err == nil || !strings.Contains(err.Error(), "admin-only") {
		t.Fatalf("member moderation err=%v, want admin-only", err)
	}
	admin := accountStore().findUser("aj@shareability.com")
	if _, _, err := kanbanApp.moderateScoutChatThreadMessage(admin, channel.ID, "human-message", "bad routing incident"); err == nil || !strings.Contains(err.Error(), "public agent replies") {
		t.Fatalf("human moderation err=%v, want narrow agent-only refusal", err)
	}
	if _, _, err := kanbanApp.moderateScoutChatThreadMessage(admin, channel.ID, "agent-work", "bad routing incident"); err == nil || !strings.Contains(err.Error(), "public agent replies") {
		t.Fatalf("durable work moderation err=%v, want narrow ordinary-reply refusal", err)
	}

	thread, receipt, err := kanbanApp.moderateScoutChatThreadMessage(admin, channel.ID, "agent-answer", "bad routing incident")
	if err != nil {
		t.Fatalf("admin moderation: %v", err)
	}
	if scoutChatMessageIndex(thread, "agent-answer") >= 0 || scoutChatMessageIndex(thread, "human-message") < 0 || scoutChatMessageIndex(thread, "agent-work") < 0 {
		t.Fatalf("moderation removed the wrong records: %+v", thread.Messages)
	}
	if receipt.ThreadID != channel.ID || receipt.MessageID != "agent-answer" || receipt.ActorEmail != "aj@shareability.com" || len(receipt.ReasonDigest) != 64 || receipt.DeletedAt == "" {
		t.Fatalf("moderation receipt=%+v", receipt)
	}
	if err := runtime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
		projection, projectErr := domains.ConversationLedger.ProjectForTenantPrincipal(canonicalTenantID(), strideRuntimePrincipalForEmail("tim@shareability.com"))
		if projectErr != nil {
			return projectErr
		}
		for _, row := range projection {
			if row.SourceID == "agent-answer" {
				t.Fatalf("moderated agent answer survived canonical projection: %+v", row)
			}
		}
		snapshot, snapshotErr := domains.ConversationLedger.Snapshot()
		if snapshotErr != nil {
			return snapshotErr
		}
		var latest ConversationEvent
		for _, record := range snapshot.Events {
			if record.Append.Event.SourceID == "agent-answer" {
				latest = record.Append.Event
			}
		}
		if latest.EventType != "delete" || latest.AuthorPrincipal != strideRuntimePrincipalForEmail("aj@shareability.com") {
			t.Fatalf("moderation audit event=%+v", latest)
		}
		return nil
	}); err != nil {
		t.Fatalf("verify canonical moderation projection: %v", err)
	}

	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "private", "private")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages("aj@shareability.com", private.ID, scoutChatMessageRecord{ID: "private-agent", Kind: "message", Role: "scout", Text: "private", CreatedAt: createdAt}); err != nil {
		t.Fatalf("seed private thread: %v", err)
	}
	if _, _, err := kanbanApp.moderateScoutChatThreadMessage(admin, private.ID, "private-agent", "bad routing incident"); err == nil || !strings.Contains(err.Error(), "public agent replies") {
		t.Fatalf("private moderation err=%v, want public-only refusal", err)
	}
}

func TestScoutChatAdminModerationRouteRequiresAdminAndReason(t *testing.T) {
	setupAuthTestEnv(t)
	_ = loginAs(t, "aj@shareability.com", "B0NFIRE!")
	_ = loginAs(t, "tim@shareability.com", "B0NFIRE!")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("tim@shareability.com", "Tim", "incident channel", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	config := strideIntegratedRuntimeConfig(t.TempDir())
	config.RecallThreadIDs = []string{channel.ID}
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatalf("create STRIDE runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	kanbanApp.strideRuntime = runtime
	if _, err := kanbanApp.commitScoutChatThreadMessages("tim@shareability.com", channel.ID, scoutChatMessageRecord{
		ID: "agent-answer", Kind: "message", Role: "scout", Text: "incorrect answer", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("seed agent answer: %v", err)
	}
	moderateAs := func(email string, messageID string, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/assistant/chat-threads/"+channel.ID+"/messages/"+messageID+"/moderate-delete", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range loginAs(t, email, "B0NFIRE!") {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantChatThreadHandler(recorder, req)
		return recorder
	}

	if recorder := moderateAs("tim@shareability.com", "agent-answer", `{"reason":"bad routing incident"}`); recorder.Code != http.StatusForbidden {
		t.Fatalf("member moderation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := moderateAs("aj@shareability.com", "agent-answer", `{"reason":""}`); recorder.Code != http.StatusBadRequest {
		t.Fatalf("empty reason status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder := moderateAs("aj@shareability.com", "agent-answer", `{"reason":"bad routing incident"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("admin moderation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		OK      bool                       `json:"ok"`
		Receipt scoutChatModerationReceipt `json:"receipt"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode moderation response: %v", err)
	}
	if !payload.OK || payload.Receipt.MessageID != "agent-answer" || payload.Receipt.ActorEmail != "aj@shareability.com" {
		t.Fatalf("moderation response=%+v", payload)
	}

	if _, err := kanbanApp.commitScoutChatThreadMessages("tim@shareability.com", channel.ID, scoutChatMessageRecord{
		ID: "agent-answer-pending", Kind: "message", Role: "scout", Text: "another incorrect answer", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("seed pending agent answer: %v", err)
	}
	runtime.config.SnapshotPath = t.TempDir()
	recorder = moderateAs("aj@shareability.com", "agent-answer-pending", `{"reason":"bad routing incident"}`)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("pending moderation status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusAccepted)
	}
	var pendingPayload struct {
		OK       bool                           `json:"ok"`
		Accepted bool                           `json:"accepted"`
		Receipt  scoutChatModerationReceiptView `json:"receipt"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &pendingPayload); err != nil {
		t.Fatalf("decode pending moderation response: %v", err)
	}
	if pendingPayload.OK || !pendingPayload.Accepted || pendingPayload.Receipt.ProjectionState != scoutChatModerationPending {
		t.Fatalf("pending moderation falsely reported complete: %+v", pendingPayload)
	}
}

func TestScoutChatAdminModerationRuntimeUnavailableLeavesSourceIntact(t *testing.T) {
	setupAuthTestEnv(t)
	_ = loginAs(t, "aj@shareability.com", "B0NFIRE!")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "incident channel", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages("aj@shareability.com", channel.ID, scoutChatMessageRecord{
		ID: "agent-answer", Kind: "message", Role: "scout", Text: "incorrect answer", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("seed agent answer: %v", err)
	}
	kanbanApp.strideRuntime = nil
	admin := accountStore().findUser("aj@shareability.com")
	if _, _, err := kanbanApp.moderateScoutChatThreadMessage(admin, channel.ID, "agent-answer", "bad routing incident"); !errors.Is(err, ErrSTRIDERuntimeUnavailable) {
		t.Fatalf("runtime-unavailable moderation err=%v", err)
	}
	thread, _, err := kanbanApp.scoutChatThreadByID(admin.Email, channel.ID)
	if err != nil || scoutChatMessageIndex(thread, "agent-answer") < 0 || len(thread.ModerationReceipts) != 0 {
		t.Fatalf("unavailable preflight mutated source/outbox: messageIndex=%d receipts=%+v err=%v", scoutChatMessageIndex(thread, "agent-answer"), thread.ModerationReceipts, err)
	}
}

func TestScoutChatAdminModerationHumanRetryOutboxSurvivesSaveFailureResponseLossAndRestart(t *testing.T) {
	setupAuthTestEnv(t)
	_ = loginAs(t, "aj@shareability.com", "B0NFIRE!")
	_ = loginAs(t, "tim@shareability.com", "B0NFIRE!")
	dataDir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dataDir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dataDir, "board.json"))
	app := newKanbanBoardApp()

	channel, err := app.createScoutChatThread("tim@shareability.com", "Tim", "incident channel", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	runtimeConfig := strideIntegratedRuntimeConfig(t.TempDir())
	runtimeConfig.RecallThreadIDs = []string{channel.ID}
	runtime, err := NewSTRIDERuntime(runtimeConfig)
	if err != nil {
		t.Fatalf("create STRIDE runtime: %v", err)
	}
	app.strideRuntime = runtime
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", channel.ID, scoutChatMessageRecord{
		ID: "aj-retry", Kind: "message", Role: "user", Text: "compare the two pitches", AuthorEmail: "aj@shareability.com", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("seed projected AJ retry: %v", err)
	}

	validSnapshotPath := runtime.config.SnapshotPath
	runtime.config.SnapshotPath = t.TempDir() // snapshot write fails after the canonical delete append
	admin := accountStore().findUser("aj@shareability.com")
	thread, receipt, err := app.moderateScoutChatThreadMessage(admin, channel.ID, "aj-retry", "bad routing incident")
	if err != nil {
		t.Fatalf("moderation with projection save failure should persist a pending outbox: %v", err)
	}
	if receipt.ProjectionState != scoutChatModerationPending || receipt.AttemptCount != 1 || scoutChatMessageIndex(thread, "aj-retry") >= 0 {
		t.Fatalf("pending moderation=%+v messages=%+v", receipt, thread.Messages)
	}

	// A lost HTTP response followed by the same exact retry resolves the durable
	// operation instead of returning message-not-found or creating a new one.
	thread, retried, err := app.moderateScoutChatThreadMessage(admin, channel.ID, "aj-retry", "bad routing incident")
	if err != nil || retried.OperationID != receipt.OperationID || retried.ProjectionState != scoutChatModerationPending || retried.AttemptCount != 2 {
		t.Fatalf("same-process idempotent retry receipt=%+v err=%v", retried, err)
	}
	if len(thread.ModerationReceipts) != 1 {
		t.Fatalf("response-loss retry created duplicate receipts: %+v", thread.ModerationReceipts)
	}

	// Restore the last valid runtime snapshot in a fresh process. The thread's
	// pending receipt is the source of truth and finishes the one canonical
	// delete, then marks itself complete durably.
	runtimeConfig.SnapshotPath = validSnapshotPath
	runtimeConfig.BootstrapEmpty = false
	restoredRuntime, err := NewSTRIDERuntime(runtimeConfig)
	if err != nil {
		t.Fatalf("restore prior runtime snapshot: %v", err)
	}
	t.Cleanup(func() { _ = restoredRuntime.Close() })
	restarted := newKanbanBoardApp()
	restarted.strideRuntime = restoredRuntime
	restarted.recoverScoutChatModerations()
	persisted, _, err := restarted.scoutChatThreadByID(admin.Email, channel.ID)
	if err != nil || len(persisted.ModerationReceipts) != 1 || persisted.ModerationReceipts[0].ProjectionState != scoutChatModerationComplete || persisted.ModerationReceipts[0].CompletedAt == "" {
		t.Fatalf("restart moderation receipt=%+v err=%v", persisted.ModerationReceipts, err)
	}
	if scoutChatMessageIndex(persisted, "aj-retry") >= 0 {
		t.Fatal("restart reconciliation resurrected the deleted chat source")
	}
	if err := restoredRuntime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
		projection, projectErr := domains.ConversationLedger.ProjectForTenantPrincipal(canonicalTenantID(), strideRuntimePrincipalForEmail("tim@shareability.com"))
		if projectErr != nil {
			return projectErr
		}
		for _, row := range projection {
			if row.SourceID == "aj-retry" {
				t.Fatalf("restart reconciliation left AJ retry recallable: %+v", row)
			}
		}
		snapshot, snapshotErr := domains.ConversationLedger.Snapshot()
		if snapshotErr != nil {
			return snapshotErr
		}
		deleteCount := 0
		for _, record := range snapshot.Events {
			if record.Append.Event.SourceID == "aj-retry" && record.Append.Event.EventType == "delete" {
				deleteCount++
			}
		}
		if deleteCount != 1 {
			t.Fatalf("canonical delete count=%d, want exactly one", deleteCount)
		}
		return nil
	}); err != nil {
		t.Fatalf("verify restarted canonical projection: %v", err)
	}

	completedAttempts := persisted.ModerationReceipts[0].AttemptCount
	_, replayed, err := restarted.moderateScoutChatThreadMessage(admin, channel.ID, "aj-retry", "bad routing incident")
	if err != nil || replayed.ProjectionState != scoutChatModerationComplete || replayed.AttemptCount != completedAttempts {
		t.Fatalf("completed response-loss retry receipt=%+v err=%v", replayed, err)
	}
}

func TestScoutChatThreadMessageDeleteCascadesOnlyToOrdinaryCausedAnswer(t *testing.T) {
	setupAuthTestEnv(t)
	_ = loginAs(t, "aj@shareability.com", "B0NFIRE!")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "strategy", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	config := strideIntegratedRuntimeConfig(t.TempDir())
	config.RecallThreadIDs = []string{channel.ID}
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatalf("create STRIDE runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	kanbanApp.strideRuntime = runtime
	projectedSourceIDs := func() map[string]bool {
		t.Helper()
		result := map[string]bool{}
		if err := runtime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
			rows, err := domains.ConversationLedger.ProjectForTenantPrincipal(canonicalTenantID(), strideRuntimePrincipalForEmail("aj@shareability.com"))
			if err != nil {
				return err
			}
			for _, row := range rows {
				result[row.SourceID] = true
			}
			return nil
		}); err != nil {
			t.Fatalf("project conversation ledger: %v", err)
		}
		return result
	}
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := kanbanApp.commitScoutChatThreadMessages("aj@shareability.com", channel.ID,
		scoutChatMessageRecord{
			ID: "source", Kind: "message", Role: "user", Text: "Compare the two pitches.", CreatedAt: createdAt,
			AuthorEmail: "aj@shareability.com",
		},
		scoutChatMessageRecord{
			ID: "ordinary-answer", Kind: "message", Role: "scout", Text: "The first pitch is stronger.", CreatedAt: createdAt,
			CausedByMessageID: "source",
		},
		scoutChatMessageRecord{
			ID: "durable-work", Kind: "message", Role: "scout", Text: "Research is running.", CreatedAt: createdAt,
			CausedByMessageID: "source", Thread: &scoutChatThreadRef{ID: "work-1"},
		},
		scoutChatMessageRecord{
			ID: "unrelated-answer", Kind: "message", Role: "scout", Text: "Unrelated answer.", CreatedAt: createdAt,
		},
	); err != nil {
		t.Fatalf("seed causal messages: %v", err)
	}
	beforeDelete := projectedSourceIDs()
	if !beforeDelete["source"] || !beforeDelete["ordinary-answer"] {
		t.Fatalf("causal pair did not enter canonical projection: %+v", beforeDelete)
	}

	thread, err := kanbanApp.deleteScoutChatThreadMessage("aj@shareability.com", channel.ID, "source")
	if err != nil {
		t.Fatalf("delete source: %v", err)
	}
	remaining := make(map[string]bool, len(thread.Messages))
	for _, message := range thread.Messages {
		remaining[message.ID] = true
	}
	if remaining["source"] || remaining["ordinary-answer"] {
		t.Fatalf("source or ordinary caused answer survived: %+v", remaining)
	}
	if !remaining["durable-work"] || !remaining["unrelated-answer"] || len(remaining) != 2 {
		t.Fatalf("durable or unrelated message was removed: %+v", remaining)
	}
	afterDelete := projectedSourceIDs()
	if afterDelete["source"] || afterDelete["ordinary-answer"] || !afterDelete["durable-work"] || !afterDelete["unrelated-answer"] {
		t.Fatalf("canonical projection did not retract exactly the causal pair: %+v", afterDelete)
	}

	persisted, _, err := kanbanApp.scoutChatThreadByID("tim@shareability.com", channel.ID)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if len(persisted.Messages) != 2 || scoutChatMessageIndex(persisted, "ordinary-answer") >= 0 {
		t.Fatalf("causal cascade was not durable: %+v", persisted.Messages)
	}
}

func TestScoutChatThreadMessageDeletePrivateThreadStaysOwnerOnly(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Private notes", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	// Pre-stamp message (no authorEmail): owner-only visibility already
	// proves authorship in a private thread, so the owner can still delete it.
	seedScoutChatUserMessage(t, private.ID, "aj@shareability.com", "msg-legacy", "", "posted before the stamp existed")

	if _, err := kanbanApp.deleteScoutChatThreadMessage("tim@shareability.com", private.ID, "msg-legacy"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("outsider delete err=%v, want the thread hidden entirely", err)
	}
	thread, err := kanbanApp.deleteScoutChatThreadMessage("aj@shareability.com", private.ID, "msg-legacy")
	if err != nil {
		t.Fatalf("owner delete of pre-stamp message: %v", err)
	}
	if len(thread.Messages) != 0 {
		t.Fatalf("messages=%d, want 0", len(thread.Messages))
	}
}

// A pre-stamp (authorEmail-less) user message in a PUBLIC channel has no
// provable author — nobody may delete it.
func TestScoutChatThreadMessageDeleteUnstampedChannelMessageRefused(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "launch plan", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	seedScoutChatUserMessage(t, channel.ID, "aj@shareability.com", "msg-unstamped", "", "who wrote this?")
	if _, err := kanbanApp.deleteScoutChatThreadMessage("aj@shareability.com", channel.ID, "msg-unstamped"); err == nil || !strings.Contains(err.Error(), "your own") {
		t.Fatalf("unstamped channel delete err=%v, want refusal", err)
	}
}

// The wire contract the delete control's fetch relies on: DELETE
// /assistant/chat-threads/{id}/messages/{messageId}, session-identified,
// 403 on someone else's message, 200 + the updated thread on your own.
func TestAssistantChatThreadMessageDeleteRoute(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "launch plan", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	seedScoutChatUserMessage(t, channel.ID, "aj@shareability.com", "msg-aj", "aj@shareability.com", "wrong channel")

	deleteAs := func(email string, messageID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, "/assistant/chat-threads/"+channel.ID+"/messages/"+messageID, nil)
		for _, cookie := range loginAs(t, email, "B0NFIRE!") {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantChatThreadHandler(recorder, req)
		return recorder
	}

	if recorder := deleteAs("tim@shareability.com", "msg-aj"); recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-user delete status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusForbidden)
	}
	recorder := deleteAs("aj@shareability.com", "msg-aj")
	if recorder.Code != http.StatusOK {
		t.Fatalf("own delete status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}
	var payload struct {
		OK     bool                  `json:"ok"`
		Thread scoutChatThreadRecord `json:"thread"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if !payload.OK || len(payload.Thread.Messages) != 0 {
		t.Fatalf("response=%s, want ok with the message gone", recorder.Body.String())
	}
	if recorder := deleteAs("aj@shareability.com", "msg-aj"); recorder.Code != http.StatusNotFound {
		t.Fatalf("re-delete status=%d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestRoomChatDeleteEnforcesAuthorshipFromSession(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	payload, ok := app.recordRoomChatMessageWithMetadata(officeRoomID, "Tom", "oops, wrong room", map[string]string{
		"authorEmail": "tom@shareability.com",
	})
	if !ok {
		t.Fatal("seed room chat message failed")
	}
	id, _ := payload["id"].(string)
	if authorEmail, _ := payload["authorEmail"].(string); authorEmail != "tom@shareability.com" {
		t.Fatalf("payload authorEmail=%q, want the session stamp on the wire", authorEmail)
	}

	// Someone else — even sharing the display name — cannot delete it.
	if _, ok := app.deleteRoomChatMessage(id, "tim@shareability.com", "Tom"); ok {
		t.Fatal("cross-user room chat delete succeeded, want authz refusal")
	}
	if history := app.roomChatHistory(roomChatHistoryLimit); len(history) != 1 {
		t.Fatalf("history=%d after refused delete, want the message intact", len(history))
	}

	// The author deletes it: gone from the persisted record and from history.
	deleted, ok := app.deleteRoomChatMessage(id, "TOM@shareability.com", "Tom")
	if !ok {
		t.Fatal("author room chat delete failed")
	}
	if deleted["id"] != id {
		t.Fatalf("delete payload id=%v, want %q", deleted["id"], id)
	}
	if history := app.roomChatHistory(roomChatHistoryLimit); len(history) != 0 {
		t.Fatalf("history=%d after delete, want 0", len(history))
	}
	if entries := app.memory.snapshot(0); len(entries) != 0 {
		t.Fatalf("memory entries=%d after delete, want the transcript entry hard-removed", len(entries))
	}
}

func TestRoomChatDeleteLegacyEntriesFallBackToSpeakerName(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	payload, ok := app.recordRoomChatMessage(officeRoomID, "Tyler", "pre-stamp message")
	if !ok {
		t.Fatal("seed legacy room chat message failed")
	}
	id, _ := payload["id"].(string)

	if _, ok := app.deleteRoomChatMessage(id, "tim@shareability.com", "Tim"); ok {
		t.Fatal("name-mismatched delete of a legacy entry succeeded, want refusal")
	}
	if _, ok := app.deleteRoomChatMessage(id, "tyler@shareability.com", "tyler"); !ok {
		t.Fatal("case-insensitive speaker-name fallback delete failed")
	}
}

func TestRoomChatDeleteOnlyTouchesRoomChatEntries(t *testing.T) {
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(t.TempDir(), "board.json"))

	app := newKanbanBoardApp()
	entry, appended, err := app.memory.appendAttributedTranscript("event-spoken", "item-spoken", "Tyler", "dominant", "Boot Barn spoken update.")
	if err != nil || !appended {
		t.Fatalf("append spoken transcript: appended=%v err=%v", appended, err)
	}
	if _, ok := app.deleteRoomChatMessage(entry.ID, "tyler@shareability.com", "Tyler"); ok {
		t.Fatal("spoken transcript deleted through the room chat path, want refusal")
	}
	if entries := app.memory.snapshot(0); len(entries) != 1 {
		t.Fatalf("memory entries=%d, want the spoken transcript untouched", len(entries))
	}
}
