package main

// Wave 7 D3 — the post-call recap card. When a meeting finalizes with a
// digest, ONE Scout-authored message lands in the room's channel: title,
// duration, up to five decisions, up to five action items, and a link to the
// Meeting Record. Idempotent per meeting: the record stamps
// recapCardPostedAt, the message id is deterministic, and the commit re-reads
// the thread under its lock, so finalization retries and crash-replays can
// never post twice. Rooms without a channel are skipped (no stamp, so a
// channel created later still gets nothing retroactively — cards are live
// announcements, not history backfill).
//
// Channel mapping: room chat itself is sitting-scoped memory, not a thread,
// so the card goes to the organization-public channel whose title matches
// the room's name; the office maps to the Table (the team's permanent
// channel).

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	meetingRecapCardMaxItems      = 5
	meetingRecapCardMessagePrefix = "meeting-recap-card-"
)

func meetingRecapCardMessageID(meetingID string) string {
	return meetingRecapCardMessagePrefix + strings.TrimSpace(meetingID)
}

// meetingRecordCardPath is the Meeting Record deep link the card carries.
// The web client owns the `?record=` boot param (frontend half of Wave 7).
func meetingRecordCardPath(meetingID string) string {
	path := "/?record=" + url.QueryEscape(strings.TrimSpace(meetingID))
	if base := strings.TrimRight(strings.TrimSpace(os.Getenv("BONFIRE_PUBLIC_URL")), "/"); base != "" {
		return base + path
	}
	return path
}

func formatMeetingRecapDuration(seconds int64) string {
	if seconds <= 0 {
		return ""
	}
	minutes := (seconds + 30) / 60
	if minutes < 1 {
		return "under a minute"
	}
	hours, minutes := minutes/60, minutes%60
	switch {
	case hours > 0 && minutes > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%d min", minutes)
	}
}

// meetingRecapCardChannel resolves the room's channel thread, or false when
// the room has none.
func (app *kanbanBoardApp) meetingRecapCardChannel(roomID string) (scoutChatThreadRecord, bool) {
	if app == nil || app.memory == nil {
		return scoutChatThreadRecord{}, false
	}
	roomID = normalizeRoomID(roomID)
	if roomID == officeRoomID {
		table, ok := app.findTableThread()
		if !ok || table.ArchivedAt != "" || !scoutChatThreadIsOrganizationPublic(table) {
			return scoutChatThreadRecord{}, false
		}
		return table, true
	}
	store := appRoomStoreIfOpen()
	if store == nil {
		return scoutChatThreadRecord{}, false
	}
	room, ok := store.byID(roomID)
	if !ok || strings.TrimSpace(room.Name) == "" {
		return scoutChatThreadRecord{}, false
	}
	thread, err := app.publicChannelByName(room.Name)
	if err != nil {
		return scoutChatThreadRecord{}, false
	}
	return thread, true
}

func buildMeetingRecapCardText(record meetingRecord, payload meetingDigestPayload, roomName string, now time.Time) string {
	title := firstNonEmptyString(strings.TrimSpace(record.Title), strings.TrimSpace(payload.Title), "Meeting")
	var builder strings.Builder
	builder.WriteString("**Meeting recap — " + title + "**\n")
	meta := make([]string, 0, 3)
	if roomName = strings.TrimSpace(roomName); roomName != "" {
		meta = append(meta, roomName)
	}
	if duration := formatMeetingRecapDuration(meetingRecordDuration(record, now)); duration != "" {
		meta = append(meta, duration)
	}
	if count := len(record.Participants); count == 1 {
		meta = append(meta, "1 person")
	} else if count > 1 {
		meta = append(meta, fmt.Sprintf("%d people", count))
	}
	if len(meta) > 0 {
		builder.WriteString(strings.Join(meta, " · ") + "\n")
	}
	decisions := make([]string, 0, meetingRecapCardMaxItems)
	for _, decision := range payload.Decisions {
		text := strings.TrimSpace(decision.D)
		if text == "" || len(decisions) >= meetingRecapCardMaxItems {
			continue
		}
		decisions = append(decisions, "• "+trimForStorage(text, 240))
	}
	actions := make([]string, 0, meetingRecapCardMaxItems)
	for _, action := range payload.ActionItems {
		text := strings.TrimSpace(action.A)
		if text == "" || len(actions) >= meetingRecapCardMaxItems {
			continue
		}
		line := "• " + trimForStorage(text, 240)
		if owner := strings.TrimSpace(action.Owner); owner != "" {
			line += " — " + owner
		}
		actions = append(actions, line)
	}
	if len(decisions) > 0 {
		builder.WriteString("\nDecisions\n" + strings.Join(decisions, "\n") + "\n")
	}
	if len(actions) > 0 {
		builder.WriteString("\nAction items\n" + strings.Join(actions, "\n") + "\n")
	}
	if len(decisions) == 0 && len(actions) == 0 {
		builder.WriteString("\nNo grounded decisions or action items were captured.\n")
	}
	builder.WriteString("\nMeeting Record: " + meetingRecordCardPath(record.ID) + "\n")
	return strings.TrimSpace(builder.String())
}

// postMeetingRecapCardFailSoft never fails a finalization: the receipt is
// already durable, and the card is a courtesy announcement. It returns the
// record as stamped (re-read from the store) so callers broadcast the truth.
func (app *kanbanBoardApp) postMeetingRecapCardFailSoft(record meetingRecord) meetingRecord {
	if _, err := app.postMeetingRecapCard(record); err != nil {
		log.Errorf("Meeting %s recap card was not posted: %v", record.ID, err)
		return record
	}
	if app != nil && app.meetings != nil {
		if current, ok := app.meetings.recordByID(record.ID); ok {
			return current
		}
	}
	return record
}

// postMeetingRecapCard posts the card once. Returns posted=true only when this
// call committed the message.
func (app *kanbanBoardApp) postMeetingRecapCard(record meetingRecord) (bool, error) {
	if app == nil || app.meetings == nil || app.memory == nil || strings.TrimSpace(record.ID) == "" {
		return false, nil
	}
	if strings.TrimSpace(record.RecapCardPostedAt) != "" {
		return false, nil
	}
	receipt := record.Finalization
	if receipt == nil || receipt.State != meetingFinalizationFinalized || receipt.Source.TranscriptCount == 0 {
		return false, nil
	}
	digest, ok := app.memory.currentDigest(meetingMemoryKindMeetingDigest, record.ID)
	if !ok {
		return false, nil
	}
	payload, ok := parseMeetingDigest(digest.Text)
	if !ok {
		return false, nil
	}
	thread, ok := app.meetingRecapCardChannel(meetingRoomID(record))
	if !ok {
		return false, nil
	}
	now := time.Now().UTC()
	messageID := meetingRecapCardMessageID(record.ID)
	roomName := ""
	if store := appRoomStoreIfOpen(); store != nil {
		if room, found := store.byID(meetingRoomID(record)); found {
			roomName = room.Name
		}
	}
	text := buildMeetingRecapCardText(record, payload, roomName, now)

	lock := app.scoutChatThreadLock(thread.ID)
	lock.Lock()
	current, _, err := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		lock.Unlock()
		return false, err
	}
	if scoutChatMessageIndex(current, messageID) >= 0 {
		lock.Unlock()
		app.stampMeetingRecapCard(record.ID, thread.ID, messageID, now)
		return false, nil
	}
	message := scoutChatMessageRecord{
		ID: messageID, Kind: "message", Role: "scout", Text: text,
		CreatedAt: now.Format(time.RFC3339Nano), AuthorName: scoutParticipantName,
	}
	_, err = app.commitScoutChatThreadMessagesLockedWithContext(context.Background(), thread.OwnerEmail, thread.ID, message)
	lock.Unlock()
	if err != nil {
		return false, err
	}
	app.stampMeetingRecapCard(record.ID, thread.ID, messageID, now)
	broadcastOSEvent(osEvent{
		Kind: osEventChannelPost, Ref: thread.ID, Title: "#" + thread.Title,
		OriginSurface: "chat", Actor: scoutParticipantName,
	})
	return true, nil
}

func (app *kanbanBoardApp) stampMeetingRecapCard(meetingID, threadID, messageID string, now time.Time) {
	if _, err := app.meetings.markRecapCardPosted(meetingID, threadID, messageID, now); err != nil {
		log.Errorf("Meeting %s recap card posted but the stamp could not be written: %v", meetingID, err)
	}
}

// markRecapCardPosted stamps the card on the record; a second call is a no-op.
func (store *meetingStore) markRecapCardPosted(id, threadID, messageID string, now time.Time) (meetingRecord, error) {
	if store == nil || strings.TrimSpace(id) == "" {
		return meetingRecord{}, ErrMeetingRecordStore
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.recordIndexes == nil {
		store.rebuildDirectoryCursorIndexesLocked()
	}
	index, ok := store.recordIndexes[strings.TrimSpace(id)]
	if !ok || index < 0 || index >= len(store.records) {
		return meetingRecord{}, fmt.Errorf("%w: meeting %s is absent", ErrMeetingRecordStore, id)
	}
	if store.records[index].RecapCardPostedAt != "" {
		return cloneMeetingRecord(store.records[index]), nil
	}
	prior := cloneMeetingRecord(store.records[index])
	store.records[index].RecapCardPostedAt = now.UTC().Format(time.RFC3339Nano)
	store.records[index].RecapCardThreadID = strings.TrimSpace(threadID)
	store.records[index].RecapCardMessageID = strings.TrimSpace(messageID)
	if err := store.persistLocked(); err != nil {
		store.resolvePersistFailureLocked(err, func() { store.records[index] = prior })
		return cloneMeetingRecord(store.records[index]), fmt.Errorf("%w: %v", ErrMeetingRecordStore, err)
	}
	return cloneMeetingRecord(store.records[index]), nil
}
