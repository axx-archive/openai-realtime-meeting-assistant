package main

import (
	"fmt"
	"strings"
)

// scoutChatTypingEventPayload authorizes an ephemeral typing signal against
// the same thread visibility lookup used by durable chat reads. Typing is
// intentionally public-channel-only: private Scout threads reveal nothing to
// other accounts, archived channels cannot accumulate stale presence, and no
// signal is written to the thread record or meeting memory.
func scoutChatTypingEventPayload(app *kanbanBoardApp, sessionUser *userAccount, threadID string, typing bool) (map[string]any, error) {
	if app == nil || sessionUser == nil || normalizeAccountEmail(sessionUser.Email) == "" {
		return nil, fmt.Errorf("typing is unavailable")
	}
	thread, _, err := app.scoutChatThreadByID(sessionUser.Email, strings.TrimSpace(threadID))
	if err != nil || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic || thread.ArchivedAt != "" {
		return nil, fmt.Errorf("chat thread is unavailable")
	}
	identity := accountStore().findUser(sessionUser.Email)
	if identity == nil {
		identity = sessionUser
	}
	return map[string]any{
		"threadId":      thread.ID,
		"email":         normalizeAccountEmail(identity.Email),
		"name":          accountDisplayName(identity),
		"avatarDataURL": identity.AvatarDataURL,
		"typing":        typing,
	}, nil
}

func deliverScoutChatTypingEvent(app *kanbanBoardApp, sessionUser *userAccount, threadID string, payload map[string]any) error {
	if app == nil || sessionUser == nil || payload == nil {
		return fmt.Errorf("typing is unavailable")
	}
	thread, _, err := app.scoutChatThreadByID(sessionUser.Email, strings.TrimSpace(threadID))
	if err != nil || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic || thread.ArchivedAt != "" {
		return fmt.Errorf("chat thread is unavailable")
	}
	if scoutChatThreadIsOrganizationPublic(thread) {
		broadcastSignedInKanbanEvent("chat_typing", payload)
		return nil
	}
	for _, member := range scoutChatThreadMemberEmails(thread) {
		sendKanbanEventToUser(member, "chat_typing", payload)
	}
	return nil
}
