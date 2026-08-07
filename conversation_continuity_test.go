package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestConversationContinuityIsRevisionedBodyFreeAndRebuiltOnSourceChange(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	now := time.Date(2026, 8, 6, 22, 0, 0, 0, time.UTC)
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "continuity", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	thread.UpdatedAt = now.Format(time.RFC3339Nano)
	thread.Messages = []scoutChatMessageRecord{
		{ID: "source-question", Role: "user", AuthorEmail: "aj@shareability.com", Text: "Should we test the narrow launch first?", CreatedAt: now.Format(time.RFC3339Nano)},
		{ID: "source-reply", Role: "scout", Text: "I disagree with the broad rollout; test the narrow launch first.", CreatedAt: now.Add(time.Minute).Format(time.RFC3339Nano), ReplyTo: &scoutChatReplyRef{MessageID: "source-question"}},
	}
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	first, appended, err := app.rebuildConversationContinuity(thread, "message")
	if err != nil || !appended {
		t.Fatalf("first continuity=%+v appended=%v err=%v", first, appended, err)
	}
	if first.Revision != 1 || first.CurrentIntentClass != "question" || len(first.ResolvedReferenceIDs) != 1 || len(first.OpenLoopSourceIDs) != 2 || len(first.DisagreementSourceIDs) != 1 {
		t.Fatalf("first checkpoint=%+v", first)
	}
	entries := app.memory.entriesOfKind(meetingMemoryKindConversationContinuity, 0)
	if len(entries) != 1 || strings.Contains(entries[0].Text, "Should we test the narrow launch first?") || strings.Contains(entries[0].Text, "I disagree") {
		t.Fatalf("continuity record contains body or wrong count: %+v", entries)
	}
	if got, ok := app.conversationContinuityForViewer("aj@shareability.com", thread); !ok || got.Revision != 1 {
		t.Fatalf("owner continuity=%+v ok=%v", got, ok)
	}
	if got, ok := app.conversationContinuityForViewer("other@shareability.com", thread); !ok || got.Revision != 1 {
		t.Fatalf("organization-public viewer could not read continuity=%+v ok=%v", got, ok)
	}
	if got := app.prepareConversationContinuityModelQuery("aj@shareability.com", thread, "base"); !strings.Contains(got, first.SourceDigest) || strings.Contains(got, "Should we test") {
		t.Fatalf("continuity model envelope=%q", got)
	}

	unchanged, appended, err := app.rebuildConversationContinuity(thread, "message")
	if err != nil || appended || unchanged.Revision != 1 {
		t.Fatalf("idempotent rebuild=%+v appended=%v err=%v", unchanged, appended, err)
	}

	thread.Messages[0].Text = "The launch scope changed; should we ship the smallest safe test?"
	thread.Messages[0].EditedAt = now.Add(2 * time.Minute).Format(time.RFC3339Nano)
	thread.Messages = thread.Messages[:1]
	thread.UpdatedAt = now.Add(2 * time.Minute).Format(time.RFC3339Nano)
	second, appended, err := app.rebuildConversationContinuity(thread, "delete")
	if err != nil || !appended || second.Revision != 2 {
		t.Fatalf("rebuilt continuity=%+v appended=%v err=%v", second, appended, err)
	}
	if !containsContinuityString(second.KnownGaps, "rebuild:delete") || containsContinuityString(second.SourceMessageIDs, "source-reply") || !containsContinuityString(second.CorrectionSourceIDs, "source-question") {
		t.Fatalf("rebuilt checkpoint=%+v", second)
	}
	if got := app.latestConversationContinuity(thread.ID); got.Revision != 2 || got.Status != conversationContinuityStatusActive {
		t.Fatalf("latest continuity=%+v", got)
	}
	if len(app.memory.entriesOfKind(meetingMemoryKindConversationContinuity, 0)) != 3 {
		t.Fatalf("continuity history=%d, want invalidation plus two active revisions", len(app.memory.entriesOfKind(meetingMemoryKindConversationContinuity, 0)))
	}
}

func TestConversationContinuityRespectsPrivateThreadACL(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "private continuity", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	privateMessage := scoutChatMessageRecord{
		ID: "private-source", Role: "user", AuthorEmail: "aj@shareability.com",
		Text: "Keep this private.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	thread, err = app.commitScoutChatThreadMessages("aj@shareability.com", thread.ID, privateMessage)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := app.conversationContinuityForViewer("other@shareability.com", thread); ok {
		t.Fatal("private continuity crossed the thread ACL")
	}
	if _, ok := app.conversationContinuityForViewer("aj@shareability.com", thread); !ok {
		t.Fatal("private owner could not read continuity")
	}
}

func TestConversationContinuityValidationAllowsEmptyOptionalListsButRequiresSourcesWhenActive(t *testing.T) {
	now := time.Now().UTC()
	base := conversationContinuityCheckpoint{
		ID: "continuity-1", ThreadID: "thread-1", Revision: 1, Status: conversationContinuityStatusActive,
		AudienceDigest: strings.Repeat("a", 64), SourceDigest: strings.Repeat("b", 64), SourceMessageIDs: []string{"message-1"},
		CurrentIntentClass: "discussion", LastSourceAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := base.validate(); err != nil {
		t.Fatal(err)
	}
	base.SourceMessageIDs = nil
	if err := base.validate(); err == nil {
		t.Fatal("active continuity without source was accepted")
	}
}

func TestConversationContinuityStartupReconcilesPersistedPublicAndPrivateThreads(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for index, visibility := range []string{scoutChatVisibilityPublic, scoutChatVisibilityPrivate} {
		thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "restart continuity", visibility)
		if err != nil {
			t.Fatal(err)
		}
		thread.Messages = []scoutChatMessageRecord{{ID: "restart-source-" + fmt.Sprint(index), Role: "user", AuthorEmail: "aj@shareability.com", Text: "Persist this before the checkpoint.", CreatedAt: now}}
		thread.UpdatedAt = now
		if err := app.saveScoutChatThread(thread); err != nil {
			t.Fatal(err)
		}
		if got := app.latestConversationContinuity(thread.ID); got.ID != "" {
			t.Fatalf("continuity existed before reconciliation: %+v", got)
		}
	}

	app.reconcileConversationContinuityAtStartup()
	for _, entry := range app.memory.snapshot(0) {
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok || len(thread.Messages) == 0 {
			continue
		}
		if got := app.latestConversationContinuity(thread.ID); got.Status != conversationContinuityStatusActive || got.Revision != 1 {
			t.Fatalf("startup continuity for %s=%+v", thread.ID, got)
		}
	}
}

func TestConversationContinuityInvalidatesWhenFinalSourceIsDeleted(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "empty continuity", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	thread.Messages = []scoutChatMessageRecord{{ID: "only-source", Role: "user", AuthorEmail: "aj@shareability.com", Text: "Delete me.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	if _, appended, err := app.rebuildConversationContinuity(thread, "message"); err != nil || !appended {
		t.Fatalf("initial continuity appended=%v err=%v", appended, err)
	}
	thread.Messages = nil
	invalidated, appended, err := app.rebuildConversationContinuity(thread, "delete")
	if err != nil || !appended || invalidated.Status != conversationContinuityStatusInvalidated {
		t.Fatalf("empty-source invalidation=%+v appended=%v err=%v", invalidated, appended, err)
	}
	if _, ok := app.conversationContinuityForViewer("aj@shareability.com", thread); ok {
		t.Fatal("empty thread retained active continuity")
	}
}

func TestConversationContinuityFailsClosedOnUnobservedSourceChange(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "stale continuity", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	thread.Messages = []scoutChatMessageRecord{{ID: "stale-source", Role: "user", AuthorEmail: "aj@shareability.com", Text: "Original body.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	if _, appended, err := app.rebuildConversationContinuity(thread, "message"); err != nil || !appended {
		t.Fatalf("initial continuity appended=%v err=%v", appended, err)
	}
	thread.Messages[0].Text = "Edited without the observer running."
	thread.Messages[0].EditedAt = time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano)
	if _, ok := app.conversationContinuityForViewer("aj@shareability.com", thread); ok {
		t.Fatal("stale checkpoint remained eligible after source digest changed")
	}
}

func TestConversationContinuityObserverSurvivesSTRIDERuntimeOutage(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.strideRuntime = nil
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "degraded continuity", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{ID: "degraded-source", Role: "user", AuthorEmail: "aj@shareability.com", Text: "Keep continuity current.", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	thread.Messages = []scoutChatMessageRecord{message}
	app.observeSTRIDETeamChatMessage(thread, message, "message", message.AuthorEmail)
	if checkpoint, ok := app.conversationContinuityForViewer(message.AuthorEmail, thread); !ok || checkpoint.SourceDigest == "" {
		t.Fatalf("continuity unavailable during STRIDE outage: %+v ok=%v", checkpoint, ok)
	}
}

func containsContinuityString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
