package main

// Wave 7 D3 — the post-call recap card. When a meeting finalizes with a
// digest, ONE Scout-authored message lands in the meeting's channel. Compact
// (AJ 2026-09-02): title, a mono meta line (room · duration · N people), the
// top THREE decisions in the digest's own order, and a mono footer counting what
// the card leaves out (+N decisions · M action items · K open).
//
// AJ 2026-09-03: the posted text no longer carries a "Meeting Record: …" tail.
// It read as a raw URL in a conversation and sent readers to a surface that
// means nothing to them; the web client now expands the card in place instead
// (index.html meetingRecapCardMessageNode). The meeting id still reaches every
// client through the deterministic message id — meeting-recap-card-<meetingID>,
// which is what gates the card render in the first place — so nothing needed a
// new metadata field. Idempotent per meeting:
// the record stamps recapCardPostedAt, the message id is deterministic, and
// the commit re-reads the thread under its lock, so finalization retries and
// crash-replays can never post twice. Rooms without a channel are skipped (no
// stamp, so a channel created later still gets nothing retroactively — cards
// are live announcements, not history backfill).
//
// Channel mapping: room chat itself is sitting-scoped memory, not a thread,
// so a named room's card goes to the organization-public channel whose title
// matches the room's name; the office maps to #meetings (meetings_channel.go),
// provisioned on the first recap if boot did not already create it. Recap
// cards that already sit in Bonfire Chat stay where they are.

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	meetingRecapCardMaxDecisions  = 3
	meetingRecapCardMessagePrefix = "meeting-recap-card-"
)

// meetingRecapCard is the compact card payload the channel message text is
// rendered from (and the shape the web client parses back, index.html
// parseMeetingRecapCardText).
type meetingRecapCard struct {
	Title         string
	Meta          string
	Decisions     []string
	MoreDecisions int
	ActionItems   int
	OpenQuestions int
	// RecordPath is the canonical `?record=<id>` deep link for the meeting.
	// It is NOT written into the posted message any more (AJ 2026-09-03 — see
	// the file comment); it stays on the payload as the one place that shape is
	// spelled, for the boot param and any non-chat consumer that needs a link.
	RecordPath string
}

func pluralizeCount(count int, singular, plural string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
}

// Footer is the mono overflow line; empty when the card already shows
// everything the digest captured.
func (card meetingRecapCard) Footer() string {
	parts := make([]string, 0, 3)
	if card.MoreDecisions > 0 {
		parts = append(parts, "+"+pluralizeCount(card.MoreDecisions, "decision", "decisions"))
	}
	if card.ActionItems > 0 {
		parts = append(parts, pluralizeCount(card.ActionItems, "action item", "action items"))
	}
	if card.OpenQuestions > 0 {
		parts = append(parts, fmt.Sprintf("%d open", card.OpenQuestions))
	}
	return strings.Join(parts, " · ")
}

// Text renders the durable message body. Self-contained: no trailing link out
// of the conversation (AJ 2026-09-03).
func (card meetingRecapCard) Text() string {
	var builder strings.Builder
	builder.WriteString("**Meeting recap — " + card.Title + "**\n")
	if card.Meta != "" {
		builder.WriteString(card.Meta + "\n")
	}
	if len(card.Decisions) > 0 {
		builder.WriteString("\nDecisions\n")
		for _, decision := range card.Decisions {
			builder.WriteString("• " + decision + "\n")
		}
	} else {
		builder.WriteString("\nNo grounded decisions were captured.\n")
	}
	if footer := card.Footer(); footer != "" {
		builder.WriteString("\n" + footer + "\n")
	}
	return strings.TrimSpace(builder.String())
}

// meetingRecapCardTopDecisions picks the card's three decisions: the SERVER
// truncates, and the digest's own order is the authority (AJ 2026-09-02 — "the
// top THREE decisions, by the digest's own order"). Deliberately not re-ranked
// by decision.Importance: that field is optional and model-emitted, so ranking
// on it would reorder the card away from the order the decisions were made in
// and leave "+N decisions" ambiguous about which ones the reader is missing.
// Blank entries never count — a digest that emitted three decisions and two
// empties reads "3 decisions", not "+2".
// Returns the picks and how many grounded decisions were left off.
func meetingRecapCardTopDecisions(payload meetingDigestPayload) ([]string, int) {
	picks := make([]string, 0, meetingRecapCardMaxDecisions)
	dropped := 0
	for _, decision := range payload.Decisions {
		text := strings.TrimSpace(decision.D)
		if text == "" {
			continue
		}
		if len(picks) >= meetingRecapCardMaxDecisions {
			dropped++
			continue
		}
		picks = append(picks, trimForStorage(text, 240))
	}
	return picks, dropped
}

func meetingRecapCardMessageID(meetingID string) string {
	return meetingRecapCardMessagePrefix + strings.TrimSpace(meetingID)
}

// meetingRecordCardPath is the canonical Meeting Record deep link. The web
// client owns the `?record=` boot param (frontend half of Wave 7); since
// 2026-09-03 the recap card message no longer prints it.
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
		// AJ 2026-09-02: office recaps go to #meetings, created on first recap
		// when boot has not already provisioned it.
		meetings, err := app.ensureMeetingsChannel(artifactLibraryAdminEmail)
		if err != nil || meetings.ArchivedAt != "" || !scoutChatThreadIsOrganizationPublic(meetings) {
			return scoutChatThreadRecord{}, false
		}
		return meetings, true
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

func buildMeetingRecapCard(record meetingRecord, payload meetingDigestPayload, roomName string, now time.Time) meetingRecapCard {
	card := meetingRecapCard{
		Title:      firstNonEmptyString(strings.TrimSpace(record.Title), strings.TrimSpace(payload.Title), "Meeting"),
		RecordPath: meetingRecordCardPath(record.ID),
	}
	meta := make([]string, 0, 3)
	if roomName = strings.TrimSpace(roomName); roomName != "" {
		meta = append(meta, roomName)
	}
	if duration := formatMeetingRecapDuration(meetingRecordDuration(record, now)); duration != "" {
		meta = append(meta, duration)
	}
	if count := len(record.Participants); count > 0 {
		meta = append(meta, pluralizeCount(count, "person", "people"))
	}
	card.Meta = strings.Join(meta, " · ")
	card.Decisions, card.MoreDecisions = meetingRecapCardTopDecisions(payload)
	for _, action := range payload.ActionItems {
		if strings.TrimSpace(action.A) != "" {
			card.ActionItems++
		}
	}
	for _, question := range payload.OpenQuestions {
		if strings.TrimSpace(question.Q) != "" {
			card.OpenQuestions++
		}
	}
	return card
}

func buildMeetingRecapCardText(record meetingRecord, payload meetingDigestPayload, roomName string, now time.Time) string {
	return buildMeetingRecapCard(record, payload, roomName, now).Text()
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

	// Office recaps used to land in Bonfire Chat. The stamp write is fail-soft
	// (stampMeetingRecapCard only logs), so a meeting finalized before the move
	// can have its card sitting in the Table with no RecapCardThreadID. The
	// dedupe below re-reads the TARGET thread only, so without this probe the
	// next finalization retry would post a second copy of the same card into
	// #meetings. One extra read, on a path that already re-reads under a lock.
	if strings.TrimSpace(record.RecapCardThreadID) == "" && normalizeRoomID(meetingRoomID(record)) == officeRoomID {
		if table, found := app.findTableThread(); found && table.ID != thread.ID && scoutChatMessageIndex(table, messageID) >= 0 {
			app.stampMeetingRecapCard(record.ID, table.ID, messageID, now)
			return false, nil
		}
	}

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
