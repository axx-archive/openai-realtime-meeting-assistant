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

func (app *kanbanBoardApp) meetingRecap(args map[string]any, requesterEmail string, roomID string) (map[string]any, bool, error) {
	if app == nil || app.memory == nil {
		return nil, false, fmt.Errorf("meeting memory is unavailable")
	}
	roomID = normalizeRoomID(roomID)

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

	// Force THE ROOM's brain pass — exactly the archive-flush machinery. The
	// per-(agent, room) run lock serializes against the 5-minute ticker and
	// close flushes; another room's window is never touched (W4 §7.4). A pass
	// error is logged, not fatal: fall back to the room's last brain entry.
	agent := meetingBrainAgent()
	app.ensureAmbientAgentRoomBaseline(agent, roomID)
	ctx, cancel := context.WithTimeout(context.Background(), meetingRecapRequestTimeout)
	defer cancel()
	if _, err := app.runAmbientAgentOnceForRoom(agent, ctx, apiKey, nil, 1, roomID); err != nil {
		log.Errorf("meeting recap brain pass failed: %v", err)
	}

	meetingID := app.memory.currentMeetingID(roomID)
	recapText := ""
	for _, entry := range app.memory.snapshotForMeeting(meetingID, 0) {
		if entry.Kind == meetingMemoryKindBrain {
			recapText = strings.TrimSpace(entry.Text)
		}
	}
	if recapText == "" {
		return nil, false, fmt.Errorf("nothing has been captured this meeting yet")
	}
	headline := meetingRecapHeadline(recapText)
	// Coverage honesty (kanban-card-107): if capture began well after the
	// sitting started or had a long gap, lead the recap with a short caveat so
	// it never implies it covers a meeting it only partly captured.
	if prefix := app.meetingCapturePrefix(meetingID); prefix != "" {
		recapText = prefix + recapText
		headline = prefix + headline
	}

	if audience == notificationAudienceMe {
		// Catch-me-up: the recap lands in the requester's bell only.
		if _, err := app.createNotification(requesterEmail, notificationKindInfo, trimForStorage(headline, 500), "room", "", "", false); err != nil {
			return nil, false, err
		}
	} else {
		// Room delivery: the recap re-enters the transcript stream (typed-chat
		// path), consistent with Scout's spoken output being transcribed too.
		// The OFFICE keeps its signed-in union (office ∪ room, like every other
		// office room_chat writer — the unread pip stays live and roomChatSeenIds
		// dedupe makes dual-socket delivery harmless); a NAMED room's recap
		// fans out room-scoped ONLY (W4 §7.4 — never the signed-in union, which
		// leaked one room's recap office-wide).
		if payload, ok := app.recordRoomChatMessage(roomID, scoutParticipantName, "Meeting recap:\n"+recapText); ok {
			if roomID == officeRoomID {
				broadcastSignedInKanbanEvent("room_chat", payload)
			} else {
				broadcastRoomKanbanEvent(roomID, "room_chat", payload)
			}
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
func (app *kanbanBoardApp) catchMeUp(args map[string]any, requesterEmail string, roomID string) (map[string]any, bool, error) {
	forced := map[string]any{"audience": notificationAudienceMe}
	for key, value := range args {
		if key == "audience" {
			continue
		}
		forced[key] = value
	}
	return app.meetingRecap(forced, requesterEmail, roomID)
}

// meetingCapturePrefix returns a short honesty note to lead a recap when the
// captured transcript began well after the room-occupancy sitting started
// (coverageStartTolerance) or had a long stretch with no captured lines
// (coverageGapThreshold) — so a recap never implies it covers a meeting it only
// partly captured. Empty when coverage is clean or unknowable. This is a LIVE
// recompute rather than a stamp read, and that is correct here: the recap always
// targets the CURRENT sitting (currentMeetingID), whose transcripts are minutes
// old and cannot yet have aged out — so it never suffers the aged-transcript
// drift that meetingCoverageDetail guards against for PAST meetings. A gap can be
// a quiet spell as easily as a capture failure, so the note says so plainly and
// never asserts capture broke. The stamped time is the moment CAPTURE began,
// never a fabricated real-world meeting start.
func (app *kanbanBoardApp) meetingCapturePrefix(meetingID string) string {
	if app == nil || app.memory == nil || strings.TrimSpace(meetingID) == "" {
		return ""
	}
	coverage := app.memory.transcriptCoverageForMeeting(meetingID)
	if coverage.Count == 0 {
		return ""
	}
	lateStart := false
	if record, ok := app.meetingDirectoryRecord(meetingID); ok && !isLegacyMeetingKey(meetingID) {
		if start, err := time.Parse(time.RFC3339, strings.TrimSpace(record.StartedAt)); err == nil {
			lateStart = coverage.FirstAt.Sub(start) > coverageStartTolerance
		}
	}
	gapped := coverage.MaxInternalGap > coverageGapThreshold
	if !lateStart && !gapped {
		return ""
	}
	began := coverage.FirstAt.In(meetingTimeLocation()).Format("15:04")
	if lateStart {
		return fmt.Sprintf("(Capture began %s — this recap covers the captured portion only.)\n\n", began)
	}
	gapMin := int(coverage.MaxInternalGap.Minutes())
	return fmt.Sprintf("(There was a %d-minute stretch with no captured transcript — a quiet spell or a capture gap; this recap covers the captured portion only.)\n\n", gapMin)
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
