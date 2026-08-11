package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func mutationTestUser(email, name string) *userAccount {
	return &userAccount{Email: email, Name: name}
}

func mutationTestString(value string) *string {
	return &value
}

func mutationTestFiles(files []scoutChatFileAttachment) *[]scoutChatFileAttachment {
	return &files
}

func setupScoutChatMutationTest(t *testing.T) *kanbanBoardApp {
	t.Helper()
	setupAuthTestEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	return app
}

func mutationRoute(t *testing.T, method, path, body, email string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if email != "" {
		for _, cookie := range loginAs(t, email, "B0NFIRE!") {
			request.AddCookie(cookie)
		}
	}
	recorder := httptest.NewRecorder()
	assistantChatThreadHandler(recorder, request)
	return recorder
}

func TestEditScoutChatThreadMessagePreservesIdentityPositionReactionsAndAttachments(t *testing.T) {
	app := setupScoutChatMutationTest(t)
	aj := mutationTestUser("aj@shareability.com", "AJ")
	channel, err := app.createScoutChatThread(aj.Email, aj.Name, "team", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	createdAt := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	originalFiles := []scoutChatFileAttachment{{Name: "brief.txt", Kind: "text/plain", Size: 12, Text: "source facts"}}
	messages := []scoutChatMessageRecord{
		{ID: "before", Kind: "message", Role: "user", Text: "before", CreatedAt: createdAt, AuthorName: "Tim", AuthorEmail: "tim@shareability.com"},
		{ID: "edit-me", Kind: "message", Role: "user", Text: "draft", CreatedAt: createdAt, AuthorName: "AJ", AuthorEmail: aj.Email, Files: originalFiles},
		{ID: "after", Kind: "message", Role: "user", Text: "after", CreatedAt: createdAt, AuthorName: "Tim", AuthorEmail: "tim@shareability.com"},
	}
	if _, err := app.commitScoutChatThreadMessages(aj.Email, channel.ID, messages...); err != nil {
		t.Fatalf("seed messages: %v", err)
	}
	if _, _, err := app.updateScoutChatMessageReaction(mutationTestUser("tim@shareability.com", "Tim"), channel.ID, "edit-me", "👍", true); err != nil {
		t.Fatalf("seed reaction: %v", err)
	}

	thread, edited, err := app.editScoutChatThreadMessage(t.Context(), aj, channel.ID, "edit-me", mutationTestString("  final copy  "), nil)
	if err != nil {
		t.Fatalf("edit message: %v", err)
	}
	if edited.ID != "edit-me" || edited.CreatedAt != createdAt || edited.AuthorEmail != aj.Email || edited.AuthorName != "AJ" {
		t.Fatalf("edited identity drifted: %+v", edited)
	}
	if edited.Text != "final copy" || edited.EditedAt == "" {
		t.Fatalf("edited content=%+v, want trimmed text + editedAt", edited)
	}
	if _, err := time.Parse(time.RFC3339Nano, edited.EditedAt); err != nil {
		t.Fatalf("editedAt=%q is not RFC3339Nano: %v", edited.EditedAt, err)
	}
	if len(edited.Files) != 1 || edited.Files[0] != originalFiles[0] {
		t.Fatalf("omitted files changed: %+v", edited.Files)
	}
	if len(edited.Reactions) != 1 || edited.Reactions[0].Emoji != "👍" {
		t.Fatalf("edit dropped reactions: %+v", edited.Reactions)
	}
	if got := []string{thread.Messages[0].ID, thread.Messages[1].ID, thread.Messages[2].ID}; strings.Join(got, ",") != "before,edit-me,after" {
		t.Fatalf("timeline order=%v, want unchanged", got)
	}

	// An explicit empty files array clears attachments; omission above preserved
	// them. The message remains valid because it still has text.
	thread, edited, err = app.editScoutChatThreadMessage(t.Context(), aj, channel.ID, "edit-me", nil, mutationTestFiles([]scoutChatFileAttachment{}))
	if err != nil {
		t.Fatalf("clear attachments: %v", err)
	}
	if edited.Files != nil && len(edited.Files) != 0 {
		t.Fatalf("explicit attachment clear left files: %+v", edited.Files)
	}
	if edited.ID != "edit-me" || len(edited.Reactions) != 1 || thread.Messages[1].ID != "edit-me" {
		t.Fatalf("attachment clear changed stable fields: %+v", edited)
	}

	// Explicit replacement permits an attachment-only message and sanitizes the
	// replacement through the normal chat-file contract.
	replacement := []scoutChatFileAttachment{{Name: "  revised.md  ", Kind: "text/markdown", Size: 9, Text: "new facts"}}
	thread, edited, err = app.editScoutChatThreadMessage(t.Context(), aj, channel.ID, "edit-me", mutationTestString(""), mutationTestFiles(replacement))
	if err != nil {
		t.Fatalf("replace attachments: %v", err)
	}
	if edited.Text != "" || len(edited.Files) != 1 || edited.Files[0].Name != "revised.md" || edited.Files[0].Text != "new facts" {
		t.Fatalf("replacement=%+v, want sanitized attachment-only message", edited)
	}

	// Clearing both is rejected and leaves the persisted message intact.
	if _, _, err := app.editScoutChatThreadMessage(t.Context(), aj, channel.ID, "edit-me", mutationTestString(""), mutationTestFiles([]scoutChatFileAttachment{})); err == nil || !strings.Contains(err.Error(), "text or attachment") {
		t.Fatalf("empty edit err=%v, want refusal", err)
	}
	persisted, _, err := app.scoutChatThreadByID(aj.Email, channel.ID)
	if err != nil {
		t.Fatalf("re-read thread: %v", err)
	}
	if persisted.Messages[1].ID != "edit-me" || len(persisted.Messages[1].Files) != 1 {
		t.Fatalf("rejected edit changed persistence: %+v", persisted.Messages[1])
	}

	// Editing the newest text message recomputes the thread preview.
	thread, _, err = app.editScoutChatThreadMessage(t.Context(), mutationTestUser("tim@shareability.com", "Tim"), channel.ID, "after", mutationTestString("newest corrected"), nil)
	if err != nil {
		t.Fatalf("edit newest: %v", err)
	}
	if thread.Preview != "newest corrected" {
		t.Fatalf("preview=%q, want edited newest text", thread.Preview)
	}
}

func TestEditScoutChatMessageRouteEnforcesOwnershipAndStatuses(t *testing.T) {
	app := setupScoutChatMutationTest(t)
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "team", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	seedScoutChatUserMessage(t, channel.ID, "aj@shareability.com", "msg-aj", "aj@shareability.com", "draft")
	path := "/assistant/chat-threads/" + channel.ID + "/messages/msg-aj"

	if recorder := mutationRoute(t, http.MethodPatch, path, `{"text":"edited"}`, ""); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out status=%d, want 401", recorder.Code)
	}
	if recorder := mutationRoute(t, http.MethodPatch, path, `{"text":"stolen"}`, "tim@shareability.com"); recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-user status=%d body=%s, want 403", recorder.Code, recorder.Body.String())
	}
	if recorder := mutationRoute(t, http.MethodPatch, strings.TrimSuffix(path, "msg-aj")+"missing", `{"text":"edited"}`, "aj@shareability.com"); recorder.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d, want 404", recorder.Code)
	}
	if recorder := mutationRoute(t, http.MethodPatch, path, `{`, "aj@shareability.com"); recorder.Code != http.StatusBadRequest {
		t.Fatalf("malformed status=%d, want 400", recorder.Code)
	}
	recorder := mutationRoute(t, http.MethodPatch, path, `{"text":"edited"}`, "aj@shareability.com")
	if recorder.Code != http.StatusOK {
		t.Fatalf("own edit status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	var response struct {
		OK      bool                   `json:"ok"`
		Message scoutChatMessageRecord `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode edit response: %v", err)
	}
	if !response.OK || response.Message.ID != "msg-aj" || response.Message.Text != "edited" || response.Message.EditedAt == "" {
		t.Fatalf("edit response=%s", recorder.Body.String())
	}
	if _, err := app.setScoutChatThreadArchived("aj@shareability.com", channel.ID, true); err != nil {
		t.Fatalf("archive thread: %v", err)
	}
	if recorder := mutationRoute(t, http.MethodPatch, path, `{"text":"too late"}`, "aj@shareability.com"); recorder.Code != http.StatusConflict {
		t.Fatalf("archived status=%d body=%s, want 409", recorder.Code, recorder.Body.String())
	}
}

func TestEditScoutChatMessageLegacyAuthorshipFallbackStaysPrivateOnly(t *testing.T) {
	app := setupScoutChatMutationTest(t)
	aj := mutationTestUser("aj@shareability.com", "AJ")
	private, err := app.createScoutChatThread(aj.Email, aj.Name, "private", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create private: %v", err)
	}
	seedScoutChatUserMessage(t, private.ID, aj.Email, "legacy-private", "", "old private copy")
	if _, edited, err := app.editScoutChatThreadMessage(t.Context(), aj, private.ID, "legacy-private", mutationTestString("corrected private copy"), nil); err != nil || edited.Text != "corrected private copy" {
		t.Fatalf("private legacy edit=%+v err=%v", edited, err)
	}
	if _, _, err := app.editScoutChatThreadMessage(t.Context(), mutationTestUser("tim@shareability.com", "Tim"), private.ID, "legacy-private", mutationTestString("stolen"), nil); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("private outsider err=%v, want hidden thread", err)
	}

	public, err := app.createScoutChatThread(aj.Email, aj.Name, "public", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create public: %v", err)
	}
	seedScoutChatUserMessage(t, public.ID, aj.Email, "legacy-public", "", "unknown author")
	if _, _, err := app.editScoutChatThreadMessage(t.Context(), aj, public.ID, "legacy-public", mutationTestString("claim"), nil); err == nil || !strings.Contains(err.Error(), "your own") {
		t.Fatalf("unstamped public edit err=%v, want authorship refusal", err)
	}
}

func TestScoutChatMessageReactionsAreStampedIdempotentAndPersistent(t *testing.T) {
	app := setupScoutChatMutationTest(t)
	allowed := []string{"❤️", "👍", "👎", "😂", "‼️", "❓", "🔥"}
	if len(scoutChatReactionEmojis) != len(allowed) {
		t.Fatalf("reaction vocabulary=%v, want exactly %v", scoutChatReactionEmojis, allowed)
	}
	for _, emoji := range allowed {
		if normalized, err := normalizeScoutChatReactionEmoji(emoji); err != nil || normalized != emoji {
			t.Fatalf("allowed reaction %q normalized=%q err=%v", emoji, normalized, err)
		}
	}
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "team", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", channel.ID, scoutChatMessageRecord{
		ID: "scout-reply", Kind: "message", Role: "scout", Text: "shipped", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("seed scout response: %v", err)
	}
	tim := mutationTestUser("tim@shareability.com", "Tim")
	aj := mutationTestUser("aj@shareability.com", "AJ")

	_, first, err := app.updateScoutChatMessageReaction(tim, channel.ID, "scout-reply", "👍", true)
	if err != nil {
		t.Fatalf("first reaction: %v", err)
	}
	if len(first.Reactions) != 1 {
		t.Fatalf("first reactions=%+v, want one", first.Reactions)
	}
	stamp := first.Reactions[0]
	if stamp.ActorEmail != tim.Email || stamp.ActorName != "Tim" || stamp.CreatedAt == "" {
		t.Fatalf("reaction identity not server stamped: %+v", stamp)
	}
	if _, err := time.Parse(time.RFC3339Nano, stamp.CreatedAt); err != nil {
		t.Fatalf("reaction createdAt=%q: %v", stamp.CreatedAt, err)
	}

	// Retrying the same PUT is a no-op: no duplicate and no timestamp churn.
	_, second, err := app.updateScoutChatMessageReaction(tim, channel.ID, "scout-reply", "👍", true)
	if err != nil {
		t.Fatalf("idempotent set: %v", err)
	}
	if len(second.Reactions) != 1 || second.Reactions[0] != stamp {
		t.Fatalf("repeat set changed reaction: before=%+v after=%+v", stamp, second.Reactions)
	}

	// Other actors and other allowed emoji coexist.
	if _, _, err := app.updateScoutChatMessageReaction(aj, channel.ID, "scout-reply", "👍", true); err != nil {
		t.Fatalf("second actor reaction: %v", err)
	}
	_, withFire, err := app.updateScoutChatMessageReaction(tim, channel.ID, "scout-reply", "🔥", true)
	if err != nil {
		t.Fatalf("second emoji: %v", err)
	}
	if len(withFire.Reactions) != 3 {
		t.Fatalf("reactions=%+v, want three actor+emoji entries", withFire.Reactions)
	}
	if _, _, err := app.updateScoutChatMessageReaction(tim, channel.ID, "scout-reply", "🚀", true); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported reaction err=%v, want refusal", err)
	}

	// DELETE clears only the authenticated actor's selected emoji and retries
	// idempotently; AJ's thumbs-up and Tim's fire remain.
	_, cleared, err := app.updateScoutChatMessageReaction(tim, channel.ID, "scout-reply", "👍", false)
	if err != nil {
		t.Fatalf("clear reaction: %v", err)
	}
	if len(cleared.Reactions) != 2 {
		t.Fatalf("clear reactions=%+v, want two survivors", cleared.Reactions)
	}
	_, clearedAgain, err := app.updateScoutChatMessageReaction(tim, channel.ID, "scout-reply", "👍", false)
	if err != nil || len(clearedAgain.Reactions) != 2 {
		t.Fatalf("idempotent clear reactions=%+v err=%v", clearedAgain.Reactions, err)
	}
	persisted, _, err := app.scoutChatThreadByID(aj.Email, channel.ID)
	if err != nil {
		t.Fatalf("re-read reactions: %v", err)
	}
	if len(persisted.Messages) != 1 || len(persisted.Messages[0].Reactions) != 2 {
		t.Fatalf("persisted reactions=%+v", persisted.Messages)
	}
}

func TestScoutChatMessageReplyPersistsImmutableOriginalContext(t *testing.T) {
	app := setupScoutChatMutationTest(t)
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "team", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", channel.ID, scoutChatMessageRecord{
		ID: "original", Kind: "message", Role: "user", Text: "The launch is Thursday at nine.",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: "Tim", AuthorEmail: "tim@shareability.com",
	}); err != nil {
		t.Fatalf("seed original: %v", err)
	}
	path := "/assistant/chat-threads/" + channel.ID + "/messages"
	recorder := mutationRoute(t, http.MethodPost, path, `{"text":"I can own the run of show.","replyToMessageId":"original","operationId":"reply-context-operation-0001"}`, "aj@shareability.com")
	if recorder.Code != http.StatusOK {
		t.Fatalf("reply status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Message scoutChatMessageRecord `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if response.Message.ReplyTo == nil || response.Message.ReplyTo.MessageID != "original" || response.Message.ReplyTo.AuthorName != "Tim" || response.Message.ReplyTo.Text != "The launch is Thursday at nine." {
		t.Fatalf("reply context=%+v", response.Message.ReplyTo)
	}

	// Editing the original never rewrites the reply's historical snapshot.
	if _, _, err := app.editScoutChatThreadMessage(t.Context(), mutationTestUser("tim@shareability.com", "Tim"), channel.ID, "original", mutationTestString("The launch moved."), nil); err != nil {
		t.Fatalf("edit original: %v", err)
	}
	persisted, _, err := app.scoutChatThreadByID("aj@shareability.com", channel.ID)
	if err != nil {
		t.Fatalf("reload thread: %v", err)
	}
	reply := persisted.Messages[len(persisted.Messages)-1]
	if reply.ReplyTo == nil || reply.ReplyTo.Text != "The launch is Thursday at nine." {
		t.Fatalf("persisted reply snapshot=%+v", reply.ReplyTo)
	}

	missing := mutationRoute(t, http.MethodPost, path, `{"text":"orphan","replyToMessageId":"missing","operationId":"missing-reply-operation-0001"}`, "aj@shareability.com")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing reply target status=%d body=%s", missing.Code, missing.Body.String())
	}
}

func TestScoutChatReplyUsesTheSameOwnerEditAndDeleteContract(t *testing.T) {
	app := setupScoutChatMutationTest(t)
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "team", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", channel.ID,
		scoutChatMessageRecord{ID: "reply-root", Kind: "message", Role: "user", Text: "Root", CreatedAt: createdAt, AuthorName: "Tim", AuthorEmail: "tim@shareability.com"},
		scoutChatMessageRecord{ID: "owned-reply", Kind: "message", Role: "user", Text: "Draft reply", CreatedAt: createdAt, AuthorName: "AJ", AuthorEmail: "aj@shareability.com", ReplyTo: &scoutChatReplyRef{MessageID: "reply-root", AuthorName: "Tim", Text: "Root"}},
	); err != nil {
		t.Fatalf("seed reply thread: %v", err)
	}

	path := "/assistant/chat-threads/" + channel.ID + "/messages/owned-reply"
	if recorder := mutationRoute(t, http.MethodPatch, path, `{"text":"stolen reply"}`, "tim@shareability.com"); recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-user reply edit status=%d body=%s, want 403", recorder.Code, recorder.Body.String())
	}
	if recorder := mutationRoute(t, http.MethodDelete, path, "", "tim@shareability.com"); recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-user reply delete status=%d body=%s, want 403", recorder.Code, recorder.Body.String())
	}

	edited := mutationRoute(t, http.MethodPatch, path, `{"text":"Final reply"}`, "aj@shareability.com")
	if edited.Code != http.StatusOK {
		t.Fatalf("own reply edit status=%d body=%s", edited.Code, edited.Body.String())
	}
	var editedResponse struct {
		Message scoutChatMessageRecord `json:"message"`
	}
	if err := json.Unmarshal(edited.Body.Bytes(), &editedResponse); err != nil {
		t.Fatalf("decode reply edit: %v", err)
	}
	if editedResponse.Message.Text != "Final reply" || editedResponse.Message.EditedAt == "" || editedResponse.Message.ReplyTo == nil || editedResponse.Message.ReplyTo.MessageID != "reply-root" {
		t.Fatalf("edited reply lost content or topology: %+v", editedResponse.Message)
	}

	deleted := mutationRoute(t, http.MethodDelete, path, "", "aj@shareability.com")
	if deleted.Code != http.StatusOK {
		t.Fatalf("own reply delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	persisted, _, err := app.scoutChatThreadByID("aj@shareability.com", channel.ID)
	if err != nil {
		t.Fatalf("reload after reply delete: %v", err)
	}
	if len(persisted.Messages) != 1 || persisted.Messages[0].ID != "reply-root" {
		t.Fatalf("reply delete changed the root conversation: %+v", persisted.Messages)
	}
}

func TestScoutChatReactionRouteEnforcesACLIdentityAndArchivedState(t *testing.T) {
	app := setupScoutChatMutationTest(t)
	private, err := app.createScoutChatThread("aj@shareability.com", "AJ", "private", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create private: %v", err)
	}
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", private.ID, scoutChatMessageRecord{
		ID: "private-reply", Kind: "message", Role: "scout", Text: "answer", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("seed private response: %v", err)
	}
	privatePath := "/assistant/chat-threads/" + private.ID + "/messages/private-reply/reaction"
	if recorder := mutationRoute(t, http.MethodPut, privatePath, `{"emoji":"❤️"}`, "tim@shareability.com"); recorder.Code != http.StatusNotFound {
		t.Fatalf("private outsider status=%d body=%s, want 404", recorder.Code, recorder.Body.String())
	}

	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "team", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", channel.ID, scoutChatMessageRecord{
		ID: "public-reply", Kind: "message", Role: "scout", Text: "answer", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("seed public response: %v", err)
	}
	path := "/assistant/chat-threads/" + channel.ID + "/messages/public-reply/reaction"
	if recorder := mutationRoute(t, http.MethodPut, path, `{"emoji":"🚀"}`, "tim@shareability.com"); recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid emoji status=%d body=%s, want 400", recorder.Code, recorder.Body.String())
	}
	// Extra spoofed identity fields are ignored; the authenticated Tim session
	// owns the persisted entry.
	recorder := mutationRoute(t, http.MethodPut, path, `{"emoji":"‼️","actorEmail":"aj@shareability.com","actorName":"AJ"}`, "tim@shareability.com")
	if recorder.Code != http.StatusOK {
		t.Fatalf("reaction put status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Message scoutChatMessageRecord `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode reaction response: %v", err)
	}
	if response.Message.ID != "public-reply" || len(response.Message.Reactions) != 1 || response.Message.Reactions[0].ActorEmail != "tim@shareability.com" {
		t.Fatalf("reaction response=%s", recorder.Body.String())
	}
	deletePath := path + "?emoji=" + url.QueryEscape("‼️")
	if recorder := mutationRoute(t, http.MethodDelete, deletePath, "", "tim@shareability.com"); recorder.Code != http.StatusOK {
		t.Fatalf("reaction delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := mutationRoute(t, http.MethodDelete, deletePath, "", "tim@shareability.com"); recorder.Code != http.StatusOK {
		t.Fatalf("reaction re-delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if _, err := app.setScoutChatThreadArchived("aj@shareability.com", channel.ID, true); err != nil {
		t.Fatalf("archive channel: %v", err)
	}
	if recorder := mutationRoute(t, http.MethodPut, path, `{"emoji":"🔥"}`, "aj@shareability.com"); recorder.Code != http.StatusConflict {
		t.Fatalf("archived reaction status=%d body=%s, want 409", recorder.Code, recorder.Body.String())
	}
}
