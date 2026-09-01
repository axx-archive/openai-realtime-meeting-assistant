package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// registerNonSeedAccountForTest inserts a registered account that is not on
// the seeded roster and removes it again when the test ends — the account
// store is process-global, so a leftover would be a phantom for later tests.
func registerNonSeedAccountForTest(t *testing.T, email, name string) *userAccount {
	t.Helper()
	store := accountStore()
	seed := store.findUser("aj@shareability.com")
	if seed == nil {
		t.Fatal("seed account is unavailable")
	}
	email = normalizeAccountEmail(email)
	if participantNameForEmail(email) != "" {
		t.Fatalf("%s is a seeded account; fixture must be non-seed", email)
	}
	extra := &userAccount{
		Email:             email,
		Name:              name,
		PasswordHash:      append([]byte(nil), seed.PasswordHash...),
		WebAuthnHandle:    []byte("non-seed-handle-" + email),
		PasswordChangedAt: time.Now().UTC(),
	}
	store.mu.Lock()
	store.users[email] = extra
	store.mu.Unlock()
	t.Cleanup(func() {
		store.mu.Lock()
		delete(store.users, email)
		store.mu.Unlock()
	})
	return extra
}

func TestAccountDisplayNamePrefersRosterThenAccountNameThenLocalPart(t *testing.T) {
	setupAuthTestEnv(t)
	if got := accountDisplayName(accountStore().findUser("tim@shareability.com")); got != "Tim" {
		t.Fatalf("seed display name=%q, want roster name", got)
	}
	if got := accountDisplayName(&userAccount{Email: "future-teammate@shareability.com", Name: "  Future   Teammate "}); got != "Future Teammate" {
		t.Fatalf("non-seed display name=%q, want collapsed account name", got)
	}
	if got := accountDisplayName(&userAccount{Email: "Quiet.Person@shareability.com"}); got != "quiet.person" {
		t.Fatalf("nameless display name=%q, want email local-part", got)
	}
	if got := accountDisplayName(nil); got != "" {
		t.Fatalf("nil display name=%q", got)
	}
}

func TestChatMentionNotifiesRegisteredNonSeedAccount(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("THREAD_MUTES_PATH", filepath.Join(t.TempDir(), "mutes.json"))
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("mention notifications must never invoke the model")
		return "", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	extra := registerNonSeedAccountForTest(t, "future-teammate@shareability.com", "Future Teammate")
	if got := chatMentionTargetEmails("@aj @Future-Teammate @nobody-here @AJ"); strings.Join(got, ",") != "aj@shareability.com,"+extra.Email {
		t.Fatalf("mention targets=%v, want roster first, then the directory match, deduped", got)
	}

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "warroom", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	tim := accountStore().findUser("tim@shareability.com")
	if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), tim, channel.ID, "@Future-Teammate can you take the pilot cut?", nil, ""); err != nil {
		t.Fatalf("append channel message: %v", err)
	}
	unread := kanbanApp.unreadNotificationsFor(extra.Email, notificationListLimit)
	if len(unread) != 1 || unread[0]["userEmail"] != extra.Email || unread[0]["threadId"] != channel.ID {
		t.Fatalf("non-seed mention notifications=%#v, want exactly one targeted record", unread)
	}
	if text := asString(unread[0]["text"]); !strings.Contains(text, "Tim mentioned you in #warroom") {
		t.Fatalf("notification text=%q", text)
	}
	for _, email := range []string{"tim@shareability.com", "aj@shareability.com"} {
		if got := kanbanApp.unreadNotificationsFor(email, notificationListLimit); len(got) != 0 {
			t.Fatalf("%s unread=%#v, want none", email, got)
		}
	}

	// At level "mentions" the targeted record still delivers; ambient does not.
	if err := setThreadNotificationLevel("", extra.Email, channel.ID, threadNotificationMentions); err != nil {
		t.Fatal(err)
	}
	targeted := notificationRecord{ThreadID: channel.ID, Kind: notificationKindChat, UserEmail: asString(unread[0]["userEmail"])}
	ambient := notificationRecord{ThreadID: channel.ID, Kind: notificationKindChat}
	if threadMutedForUser(extra.Email, targeted) || !threadMutedForUser(extra.Email, ambient) {
		t.Fatal("mentions level must deliver the non-seed account's targeted mention while muting ambient")
	}
}

func TestScoutChatTypingPayloadUsesAccountNameForNonSeedAccount(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(t.TempDir(), "memory.jsonl"))
	app := newKanbanBoardApp()
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "team typing", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	extra := registerNonSeedAccountForTest(t, "future-teammate@shareability.com", "Future Teammate")
	extra.AvatarDataURL = "data:image/png;base64,ZnV0dXJl"
	payload, err := scoutChatTypingEventPayload(app, &userAccount{Email: "Future-Teammate@shareability.com", Name: "stale session name"}, thread.ID, true)
	if err != nil {
		t.Fatalf("typing payload: %v", err)
	}
	if payload["name"] != "Future Teammate" || payload["email"] != extra.Email || payload["avatarDataURL"] != extra.AvatarDataURL || payload["typing"] != true {
		t.Fatalf("typing payload=%#v, want the registered account's name and identity", payload)
	}
}
