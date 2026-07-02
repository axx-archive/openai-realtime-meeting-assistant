package main

// meeting_recap / catch_me_up voice tools: force one meeting-brain pass over
// whatever transcripts the ticker has not consumed yet (minBatch=1 — the
// archive-flush machinery), then deliver the freshest brain write-up of the
// current meeting. Audience "room" posts the recap to room chat through the
// transcript-entering path and Scout speaks the headline; audience "me" lands
// it in the requester's notification bell instead.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// meetingRecapRequestTimeout bounds the forced brain pass inside the realtime
// tool call. Blocking inline is precedented (archiveRealtimeMeeting flushes
// inline) and the "# Preambles" instruction mandates a spoken acknowledgement
// before slow tools.
const meetingRecapRequestTimeout = 60 * time.Second

func (app *kanbanBoardApp) meetingRecap(args map[string]any, requesterEmail string) (map[string]any, bool, error) {
	if app == nil || app.memory == nil {
		return nil, false, fmt.Errorf("meeting memory is unavailable")
	}

	audience := strings.ToLower(strings.TrimSpace(asString(args["audience"])))
	switch audience {
	case "", "room":
		audience = "room"
	case notificationAudienceMe:
	default:
		return nil, false, fmt.Errorf("audience must be %q or %q", "room", notificationAudienceMe)
	}
	requesterEmail = normalizeAccountEmail(requesterEmail)
	if audience == notificationAudienceMe && requesterEmail == "" {
		// Same fallback convention as send_notification: the shared room has
		// no single requester.
		audience = "room"
	}

	app.mu.Lock()
	apiKey := app.apiKey
	app.mu.Unlock()
	if strings.TrimSpace(apiKey) == "" {
		return nil, false, fmt.Errorf("Scout needs an OpenAI API key to build a recap")
	}

	// Force the brain pass — exactly the archive-flush machinery. The
	// per-agent run lock serializes against the 5-minute ticker and archive
	// flushes. A pass error is logged, not fatal: fall back to the last brain
	// entry.
	agent := meetingBrainAgent()
	app.ensureAmbientAgentBaseline(agent)
	ctx, cancel := context.WithTimeout(context.Background(), meetingRecapRequestTimeout)
	defer cancel()
	if _, err := app.runAmbientAgentOnce(agent, ctx, apiKey, nil, 1); err != nil {
		log.Errorf("meeting recap brain pass failed: %v", err)
	}

	recapText := ""
	for _, entry := range app.memory.snapshotForMeeting(app.memory.currentMeetingID(), 0) {
		if entry.Kind == meetingMemoryKindBrain {
			recapText = strings.TrimSpace(entry.Text)
		}
	}
	if recapText == "" {
		return nil, false, fmt.Errorf("nothing has been captured this meeting yet")
	}
	headline := meetingRecapHeadline(recapText)

	if audience == notificationAudienceMe {
		// Catch-me-up: the recap lands in the requester's bell only.
		if _, err := app.createNotification(requesterEmail, notificationKindInfo, trimForStorage(headline, 500), "room", "", "", false); err != nil {
			return nil, false, err
		}
	} else {
		// Room delivery: the recap re-enters the transcript stream (typed-chat
		// path), consistent with Scout's spoken output being transcribed too.
		// Fan-out is the signed-in union (office ∪ room) like every other
		// room_chat writer — office tabs get the recap line and unread pip
		// live; roomChatSeenIds dedupe makes dual-socket delivery harmless.
		if payload, ok := app.recordRoomChatMessage(scoutParticipantName, "Meeting recap:\n"+recapText); ok {
			broadcastSignedInKanbanEvent("room_chat", payload)
		}
	}

	return map[string]any{
		"ok":       true,
		"recap":    recapText,
		"headline": headline,
		"audience": audience,
	}, false, nil
}

// catchMeUp is the catch_me_up tool: meeting_recap with audience forced to
// "me" (which still falls back to a room post when there is no requester).
func (app *kanbanBoardApp) catchMeUp(args map[string]any, requesterEmail string) (map[string]any, bool, error) {
	forced := map[string]any{"audience": notificationAudienceMe}
	for key, value := range args {
		if key == "audience" {
			continue
		}
		forced[key] = value
	}
	return app.meetingRecap(forced, requesterEmail)
}

// meetingRecapHeadline extracts the first substantive paragraph of a brain
// write-up (the Overview section body), skipping markdown headings.
func meetingRecapHeadline(text string) string {
	paragraph := []string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		paragraph = append(paragraph, line)
	}
	if len(paragraph) == 0 {
		return trimForStorage(text, 500)
	}
	return strings.Join(paragraph, " ")
}
