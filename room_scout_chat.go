package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const roomScoutTextResponseStyle = "Answer as Scout inside a shared live meeting room. Respond directly to the message that mentioned you, addressing the room rather than a private user. Use only the authorized shared-room and organization context supplied to you. Never imply access to private chats or private user profiles. Be concise unless the room asks for depth, and say when evidence is insufficient."

// roomScoutTextLock returns one stable FIFO mutex for an exact room sitting.
// The map is intentionally process-lifetime: removing a lock while a queued
// waiter still holds its pointer could let a later turn overtake it. Room
// sittings are bounded operational records, so retaining these tiny locks is
// safer than clever eviction.
func (app *kanbanBoardApp) roomScoutTextLock(scope RoomScoutScope) *sync.Mutex {
	key := fmt.Sprintf("%s|%s|%d", normalizeRoomID(scope.RoomID), strings.TrimSpace(scope.SittingID), scope.MediaGeneration)
	app.roomScoutTextMu.Lock()
	defer app.roomScoutTextMu.Unlock()
	if app.roomScoutTextLocks == nil {
		app.roomScoutTextLocks = map[string]*sync.Mutex{}
	}
	lock := app.roomScoutTextLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		app.roomScoutTextLocks[key] = lock
	}
	return lock
}

// roomScoutTextScopeCurrent is the publication fence for text Scout. It does
// not require the Realtime participant to be invited: @Scout chat and invited
// voice are deliberately independent lanes. It does require the exact live
// room, sitting and media generation that accepted the authored message.
func (app *kanbanBoardApp) roomScoutTextScopeCurrent(scope RoomScoutScope) bool {
	if app == nil || !scope.valid() {
		return false
	}
	current, ok := app.roomPublicationScope(scope.RoomID, scope.SittingID)
	return ok && current.same(scope)
}

// submitRoomScoutTextMention starts one server-owned room answer after the
// human message has durably landed and been broadcast. The websocket read loop
// never waits on a model call, and ordinary room chat never touches this path.
func (app *kanbanBoardApp) submitRoomScoutTextMention(scope RoomScoutScope, question, replyTo string) {
	question = normalizeRoomChatText(question)
	if app == nil || !scope.valid() || question == "" || !scoutChatMentionsScout(question) {
		return
	}
	go app.runRoomScoutTextMention(scope, question, strings.TrimSpace(replyTo))
}

func (app *kanbanBoardApp) runRoomScoutTextMention(scope RoomScoutScope, question, replyTo string) {
	lock := app.roomScoutTextLock(scope)
	lock.Lock()
	defer lock.Unlock()

	if !app.roomScoutTextScopeCurrent(scope) {
		return
	}
	principal := sharedRoomRecallPrincipal(scope.RoomID, scope.SittingID)
	principal.MediaGeneration = scope.MediaGeneration
	ctx := withAssistantModelSuccessRequired(context.Background())
	ctx = withAssistantBoardShortcutDisabled(ctx)
	ctx = withAssistantResponseStyle(ctx, roomScoutTextResponseStyle)
	result, err := app.resolveAssistantQueryContextForPrincipalWithAttachments(ctx, principal, "", question, nil, nil)
	if err != nil {
		log.Errorf("Room @Scout answer failed room=%s sitting=%s: %v", scope.RoomID, scope.SittingID, err)
		app.publishRoomScoutTextFailure(scope, replyTo)
		return
	}
	answer := strings.TrimSpace(result.answer)
	if answer == "" {
		log.Errorf("Room @Scout answer was empty room=%s sitting=%s", scope.RoomID, scope.SittingID)
		app.publishRoomScoutTextFailure(scope, replyTo)
		return
	}
	if !app.roomScoutTextScopeCurrent(scope) {
		return
	}
	payload, ok := app.recordRoomChatMessageForMeeting(scope.RoomID, scoutParticipantName, answer, map[string]string{
		"speaker":  scoutParticipantName,
		"agentId":  "scout",
		"replyTo":  replyTo,
		"provider": "openai",
		"model":    scoutChatModel(),
	}, scope.SittingID)
	if !ok || !app.roomScoutTextScopeCurrent(scope) {
		return
	}
	payload["roomId"] = scope.RoomID
	broadcastScopedRoomKanbanEvent(scope, "room_chat", payload)
}

// Provider failure is visible but not durable: every current participant sees
// one attributed Scout bubble, while reconnect/history replay contains only
// authored chat and successful answers. Human media and transcription are not
// touched.
func (app *kanbanBoardApp) publishRoomScoutTextFailure(scope RoomScoutScope, replyTo string) {
	if !app.roomScoutTextScopeCurrent(scope) {
		return
	}
	now := time.Now().UTC()
	payload := map[string]any{
		"id":        durableTimestampID("room-scout-error", now),
		"name":      scoutParticipantName,
		"text":      "I couldn’t answer that just now. The meeting is still connected—try @Scout again in a moment.",
		"createdAt": now.Format(time.RFC3339Nano),
		"roomId":    scope.RoomID,
		"agentId":   "scout",
		"transient": true,
		"error":     true,
	}
	if replyTo = strings.TrimSpace(replyTo); replyTo != "" {
		payload["replyTo"] = replyTo
	}
	broadcastScopedRoomKanbanEvent(scope, "room_chat", payload)
}
