package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const roomScoutTextResponseStyle = "Answer as Scout inside a shared live meeting room. Respond directly to the message that mentioned you, addressing the room rather than a private user. Use only the authorized shared-room and organization context supplied to you. Never imply access to private chats or private user profiles. Be concise unless the room asks for depth, and say when evidence is insufficient. This answer lane cannot schedule or perform future actions. Never say you will do, post, send, schedule, or finish something later; state that no work is running and ask for an immediately answerable request instead."

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
func (app *kanbanBoardApp) submitRoomScoutTextMention(scope RoomScoutScope, question, replyTo, requesterEmail, requesterName string) {
	question = normalizeRoomChatText(question)
	if app == nil || !scope.valid() || question == "" || !scoutChatMentionsScout(question) {
		return
	}
	replyTo = strings.TrimSpace(replyTo)
	requesterEmail = normalizeAccountEmail(requesterEmail)
	requesterName = canonicalRoomActorName(requesterName)
	if _, requested := parseRoomRecapFollowThrough(question); requested {
		// The room message is already durable at this seam. Persist the job and
		// deterministic delivery id before acknowledging it to the room; unlike a
		// model answer this bounded ledger write must not be lost in a goroutine
		// crash window.
		app.runRoomScoutTextMention(scope, question, replyTo, requesterEmail, requesterName)
		return
	}
	go app.runRoomScoutTextMention(scope, question, replyTo, requesterEmail, requesterName)
}

func (app *kanbanBoardApp) runRoomScoutTextMention(scope RoomScoutScope, question, replyTo, requesterEmail, requesterName string) {
	lock := app.roomScoutTextLock(scope)
	lock.Lock()
	defer lock.Unlock()

	if !app.roomScoutTextScopeCurrent(scope) {
		return
	}
	if destination, requested := parseRoomRecapFollowThrough(question); requested {
		record, err := app.scheduleRoomRecapFollowThrough(scope, replyTo, question, requesterEmail, requesterName, destination)
		if err != nil {
			answer := "I couldn't schedule that, so nothing is running. " + trimForStorage(err.Error(), 220) + "."
			if destination == "" || roomFollowThroughIsMissingInput(err) && strings.Contains(strings.ToLower(err.Error()), "name") {
				answer = "Which exact channel should receive the recap? Nothing is scheduled until you name it."
			}
			app.publishRoomScoutTextAnswer(scope, replyTo, answer, nil)
			return
		}
		app.publishRoomScoutTextAnswer(scope, replyTo,
			fmt.Sprintf("Scheduled. I'll post this meeting's recap in #%s after this sitting ends. Receipt: %s.", record.DestinationTitle, record.ID),
			map[string]string{"followThroughId": record.ID, "followThroughStatus": record.Status, "destinationThreadId": record.DestinationThreadID})
		return
	}
	if roomScoutLateJoinCatchUpRequested(question) {
		ctx, cancel := context.WithTimeout(context.Background(), meetingRecapRequestTimeout)
		defer cancel()
		response, err := app.exactCatchUpRecapWithComposer(ctx, requesterEmail, scope.RoomID, "", nil)
		if err != nil {
			log.Errorf("Room @Scout catch-up failed room=%s sitting=%s: %v", scope.RoomID, scope.SittingID, err)
			app.publishRoomScoutTextFailure(scope, replyTo)
			return
		}
		answer := strings.TrimSpace(response.Headline + "\n\n" + response.Recap)
		app.publishRoomScoutTextAnswer(scope, replyTo, answer, map[string]string{
			"meetingCatchUp": "true",
			"coverage":       string(response.Coverage.Status),
			"provider":       "stride",
			"model":          "deterministic-extractive-catch-up/v1",
		})
		return
	}
	principal := sharedRoomRecallPrincipal(scope.RoomID, scope.SittingID)
	principal.MediaGeneration = scope.MediaGeneration
	question = app.prepareSTRIDERoomRequesterModelQuery(scope, requesterEmail, requesterName, question)
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
	app.publishRoomScoutTextAnswer(scope, replyTo, answer, nil)
}

func roomScoutLateJoinCatchUpRequested(question string) bool {
	if !scoutChatMentionsScout(question) {
		return false
	}
	normalized := strings.ToLower(normalizeRoomChatText(question))
	normalized = strings.ReplaceAll(normalized, "@scout", " ")
	normalized = strings.Join(strings.Fields(normalized), " ")
	for _, phrase := range []string{
		"what did i miss",
		"what have i missed",
		"catch me up",
		"what happened before i joined",
		"i just joined what happened",
	} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

// prepareSTRIDERoomRequesterModelQuery preserves who asked without turning a
// shared meeting into a private-profile lookup. The room's ACL-scoped recall
// lane already supplies attributed transcripts and company-visible memory;
// this suffix tells the model how to keep those human identities separate.
func (app *kanbanBoardApp) prepareSTRIDERoomRequesterModelQuery(scope RoomScoutScope, requesterEmail, requesterName, query string) string {
	requesterEmail = normalizeAccountEmail(requesterEmail)
	requesterName = firstNonEmptyString(canonicalRoomActorName(requesterName), participantNameForEmail(requesterEmail), "a room participant")
	requesterIdentity := requesterName
	if principal := strideRuntimePrincipalForEmail(requesterEmail); principal != "" {
		requesterIdentity += " (" + principal + ")"
	}
	roster := []string(nil)
	if app != nil {
		roster = app.participantSnapshotForRoom(scope.RoomID)
	}
	return query + "\n\n[STRIDE shared meeting context: requester=" + requesterIdentity + "; room=" + scope.RoomID + "; sitting=" + scope.SittingID + "; participants=" + strings.Join(roster, ", ") + ". Keep every participant's statements and speaker-attributed meeting history distinct. Use only this sitting's shared transcript plus ACL-authorized company context. Never access, infer from, or reveal private chats, Settings imports, or private user profiles.]"
}

func (app *kanbanBoardApp) publishRoomScoutTextAnswer(scope RoomScoutScope, replyTo, answer string, extraMetadata map[string]string) {
	if !app.roomScoutTextScopeCurrent(scope) {
		return
	}
	metadata := map[string]string{
		"speaker":  scoutParticipantName,
		"agentId":  "scout",
		"replyTo":  replyTo,
		"provider": "openai",
		"model":    scoutChatModel(),
	}
	for key, value := range extraMetadata {
		metadata[key] = value
	}
	payload, ok := app.recordRoomChatMessageForScope(scope, scoutParticipantName, answer, metadata)
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
