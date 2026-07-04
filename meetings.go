package main

// Meetings as first-class objects. A meetingRecord is the durable identity of
// one meeting: it adopts the SAME id the memory store stamps onto entries
// (metadata.meetingId), is opened eagerly at room admission, and is closed on
// archive, on last-leave + idle grace, or on boot when a stale open record no
// longer matches the resumed memory meeting id. The alignment invariant other
// designs rely on: the record closes exactly when the memory meeting id
// rotates.
//
// Persistence is a sidecar JSON store (data/meetings.json, notifications.json
// pattern) — records mutate continuously (endedAt, auto-title, participants
// union), so they must never live in the append-only meeting-memory.jsonl.
//
// Lock-ordering rule: store methods only take store.mu, never app.mu, and
// never touch websockets; callers broadcast after every lock is released.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// meetingStoreCap keeps data/meetings.json bounded: only the newest 200
	// records survive a write.
	meetingStoreCap = 200
	// meetingListLimit is the default newest-first page size for
	// GET /assistant/meetings.
	meetingListLimit = 20
)

const (
	meetingEndedReasonArchive = "archive"
	meetingEndedReasonIdle    = "idle"
	meetingEndedReasonRestart = "restart"
)

const meetingTitleSourceAuto = "auto"

// meetingRecord is one first-class meeting. Timestamps are RFC3339Nano UTC
// strings (same convention as notificationRecord.CreatedAt).
type meetingRecord struct {
	ID           string   `json:"id"`                    // == memory meetingId ("meeting-YYYYMMDD-HHMMSS-nnnnnnnnn")
	Title        string   `json:"title,omitempty"`       // empty until auto-titled
	TitleSource  string   `json:"titleSource,omitempty"` // "auto" (manual reserved for later)
	StartedAt    string   `json:"startedAt"`
	EndedAt      string   `json:"endedAt,omitempty"`     // empty == active
	EndedReason  string   `json:"endedReason,omitempty"` // archive | idle | restart
	ArchiveID    string   `json:"archiveId,omitempty"`   // stamps at archive time
	Participants []string `json:"participants"`          // union of admitted canonical names, meetingParticipantNames order
}

type meetingStoreState struct {
	Meetings  []meetingRecord `json:"meetings"`
	UpdatedAt string          `json:"updatedAt,omitempty"`
}

type meetingStore struct {
	mu        sync.Mutex
	path      string
	records   []meetingRecord // oldest-first, capped
	idleTimer *time.Timer
	// idleGeneration invalidates an in-flight idle fire: every admission's
	// cancelIdleEnd (and every re-arm) bumps it, and endMeetingForIdle only
	// closes the record when the generation captured at arm time still
	// matches — validated under mu in the SAME critical section that stamps
	// EndedAt, so a join landing after the fire's occupancy check can never
	// have its meeting closed underneath it.
	idleGeneration uint64
}

func meetingsPath() string {
	if path := strings.TrimSpace(os.Getenv("MEETINGS_PATH")); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "meetings.json")
}

// meetingIdleEndGrace is how long an empty room stays "in the meeting" before
// the record closes and the memory meeting id rotates.
func meetingIdleEndGrace() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("MEETING_IDLE_END_GRACE")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 10 * time.Minute
}

func loadMeetingStore(path string) (*meetingStore, error) {
	records, err := loadMeetingStoreState(path)
	if err != nil {
		return nil, err
	}
	return &meetingStore{path: path, records: records}, nil
}

func loadMeetingStoreState(path string) ([]meetingRecord, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read meetings: %w", err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}

	var state meetingStoreState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("decode meetings: %w", err)
	}
	records := make([]meetingRecord, 0, len(state.Meetings))
	for _, record := range state.Meetings {
		if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.StartedAt) == "" {
			continue
		}
		record.Participants, _ = unionMeetingParticipants(nil, record.Participants)
		records = append(records, record)
	}
	if len(records) > meetingStoreCap {
		records = records[len(records)-meetingStoreCap:]
	}
	return records, nil
}

func (store *meetingStore) persistLocked() {
	state := meetingStoreState{
		Meetings:  append([]meetingRecord(nil), store.records...),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeJSONFileAtomically(store.path, "meetings", state); err != nil {
		log.Errorf("Failed to persist meetings: %v", err)
	}
}

// unionMeetingParticipants unions canonical participant names into
// meetingParticipantNames roster order; changed reports whether added
// contributed anyone new.
func unionMeetingParticipants(existing []string, added []string) ([]string, bool) {
	member := map[string]struct{}{}
	for _, name := range existing {
		if canonical := canonicalParticipantName(name); canonical != "" {
			member[canonical] = struct{}{}
		}
	}
	changed := false
	for _, name := range added {
		canonical := canonicalParticipantName(name)
		if canonical == "" {
			continue
		}
		if _, ok := member[canonical]; ok {
			continue
		}
		member[canonical] = struct{}{}
		changed = true
	}
	union := make([]string, 0, len(member))
	for _, candidate := range meetingParticipantNames {
		if _, ok := member[candidate]; ok {
			union = append(union, candidate)
		}
	}
	return union, changed
}

func cloneMeetingRecord(record meetingRecord) meetingRecord {
	record.Participants = append([]string(nil), record.Participants...)
	return record
}

// openRecordIndexLocked returns the index of the newest open record, or -1.
func (store *meetingStore) openRecordIndexLocked() int {
	for index := len(store.records) - 1; index >= 0; index-- {
		if store.records[index].EndedAt == "" {
			return index
		}
	}
	return -1
}

// activeRecord returns the newest open record.
func (store *meetingStore) activeRecord() (meetingRecord, bool) {
	if store == nil {
		return meetingRecord{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	if index := store.openRecordIndexLocked(); index >= 0 {
		return cloneMeetingRecord(store.records[index]), true
	}
	return meetingRecord{}, false
}

// startMeeting opens (or extends) the record for id. If the open record
// already carries the SAME id the start is a no-op that unions participants;
// an open record with a DIFFERENT id (should not happen; defensive against
// the idle-timer race) is closed with reason restart first.
func (store *meetingStore) startMeeting(id string, startedAt time.Time, participants []string) (meetingRecord, bool) {
	if store == nil || strings.TrimSpace(id) == "" {
		return meetingRecord{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	if index := store.openRecordIndexLocked(); index >= 0 {
		if store.records[index].ID == id {
			union, changed := unionMeetingParticipants(store.records[index].Participants, participants)
			if changed {
				store.records[index].Participants = union
				store.persistLocked()
			}
			return cloneMeetingRecord(store.records[index]), changed
		}
		store.records[index].EndedAt = startedAt.UTC().Format(time.RFC3339Nano)
		store.records[index].EndedReason = meetingEndedReasonRestart
	}

	union, _ := unionMeetingParticipants(nil, participants)
	record := meetingRecord{
		ID:           id,
		StartedAt:    startedAt.UTC().Format(time.RFC3339Nano),
		Participants: union,
	}
	store.records = append(store.records, record)
	if len(store.records) > meetingStoreCap {
		store.records = store.records[len(store.records)-meetingStoreCap:]
	}
	store.persistLocked()
	return cloneMeetingRecord(record), true
}

// addParticipant union-adds a canonical name to the open record with this id.
func (store *meetingStore) addParticipant(id string, name string) (meetingRecord, bool) {
	if store == nil || strings.TrimSpace(id) == "" {
		return meetingRecord{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	index := store.openRecordIndexLocked()
	if index < 0 || store.records[index].ID != id {
		return meetingRecord{}, false
	}
	union, changed := unionMeetingParticipants(store.records[index].Participants, []string{name})
	if changed {
		store.records[index].Participants = union
		store.persistLocked()
	}
	return cloneMeetingRecord(store.records[index]), changed
}

// endMeeting stamps EndedAt/EndedReason/ArchiveID on the open record with
// this id; idempotent (already-ended or unknown id → changed=false).
func (store *meetingStore) endMeeting(id string, endedAt time.Time, reason string, archiveID string) (meetingRecord, bool) {
	if store == nil || strings.TrimSpace(id) == "" {
		return meetingRecord{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	return store.endMeetingLocked(id, endedAt, reason, archiveID)
}

// endMeetingIfIdleGeneration is endMeeting for the idle-end seam: the close
// only lands when generation still matches idleGeneration, checked under mu
// atomically with the EndedAt stamp. A rejoin whose cancelIdleEnd bumped the
// generation after the timer fired makes the in-flight close a no-op.
func (store *meetingStore) endMeetingIfIdleGeneration(id string, endedAt time.Time, generation uint64) (meetingRecord, bool) {
	if store == nil || strings.TrimSpace(id) == "" {
		return meetingRecord{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	if generation != store.idleGeneration {
		return meetingRecord{}, false
	}
	return store.endMeetingLocked(id, endedAt, meetingEndedReasonIdle, "")
}

func (store *meetingStore) endMeetingLocked(id string, endedAt time.Time, reason string, archiveID string) (meetingRecord, bool) {
	for index := len(store.records) - 1; index >= 0; index-- {
		if store.records[index].ID != id {
			continue
		}
		if store.records[index].EndedAt != "" {
			return cloneMeetingRecord(store.records[index]), false
		}
		store.records[index].EndedAt = endedAt.UTC().Format(time.RFC3339Nano)
		store.records[index].EndedReason = reason
		store.records[index].ArchiveID = strings.TrimSpace(archiveID)
		store.persistLocked()
		return cloneMeetingRecord(store.records[index]), true
	}
	return meetingRecord{}, false
}

// setAutoTitle lands the server-derived title on the record with this id
// (open or recently ended — mission passes lag the meeting). A future manual
// title always wins over auto.
func (store *meetingStore) setAutoTitle(id string, title string) (meetingRecord, bool) {
	title = trimForStorage(strings.TrimSpace(title), 120)
	if store == nil || strings.TrimSpace(id) == "" || title == "" {
		return meetingRecord{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	for index := len(store.records) - 1; index >= 0; index-- {
		if store.records[index].ID != id {
			continue
		}
		if store.records[index].TitleSource != "" && store.records[index].TitleSource != meetingTitleSourceAuto {
			return cloneMeetingRecord(store.records[index]), false
		}
		if store.records[index].Title == title {
			return cloneMeetingRecord(store.records[index]), false
		}
		store.records[index].Title = title
		store.records[index].TitleSource = meetingTitleSourceAuto
		store.persistLocked()
		return cloneMeetingRecord(store.records[index]), true
	}
	return meetingRecord{}, false
}

// hasEndedRecord reports whether any record with this id has already ended —
// the guard that keeps an ended meeting's id from ever being re-minted onto a
// second record (boot reconciliation and the admission path both consult it).
func (store *meetingStore) hasEndedRecord(id string) bool {
	if store == nil || strings.TrimSpace(id) == "" {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	for index := len(store.records) - 1; index >= 0; index-- {
		if store.records[index].ID == id && store.records[index].EndedAt != "" {
			return true
		}
	}
	return false
}

// recent returns up to limit records, newest first.
func (store *meetingStore) recent(limit int) []meetingRecord {
	records := []meetingRecord{}
	if store == nil {
		return records
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	for index := len(store.records) - 1; index >= 0; index-- {
		records = append(records, cloneMeetingRecord(store.records[index]))
		if limit > 0 && len(records) >= limit {
			break
		}
	}
	return records
}

// countStartedSince reports how many records started today and within the
// last 7 days (meetingTimeLocation wall-clock), for the intel stat tiles.
func (store *meetingStore) countStartedSince(now time.Time) (int, int) {
	if store == nil {
		return 0, 0
	}
	location := meetingTimeLocation()
	local := now.In(location)
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	weekStart := now.Add(-7 * 24 * time.Hour)

	store.mu.Lock()
	defer store.mu.Unlock()

	today, week := 0, 0
	for _, record := range store.records {
		startedAt, err := time.Parse(time.RFC3339Nano, record.StartedAt)
		if err != nil {
			continue
		}
		if !startedAt.Before(dayStart) {
			today++
		}
		if !startedAt.Before(weekStart) {
			week++
		}
	}
	return today, week
}

// armIdleEnd schedules fire after the idle grace; arming replaces any pending
// timer (which bumps the generation so the replaced fire cannot land). fire
// receives the generation captured at arm time and must hand it back to
// endMeetingIfIdleGeneration for validation.
func (store *meetingStore) armIdleEnd(fire func(generation uint64)) {
	if store == nil {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	if store.idleTimer != nil {
		store.idleTimer.Stop()
		store.idleGeneration++
	}
	generation := store.idleGeneration
	store.idleTimer = time.AfterFunc(meetingIdleEndGrace(), func() { fire(generation) })
}

// cancelIdleEnd stops any pending idle end AND bumps the generation: a timer
// whose callback already fired (Stop returned false) is invalidated before it
// can stamp EndedAt.
func (store *meetingStore) cancelIdleEnd() {
	if store == nil {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	store.idleGeneration++
	if store.idleTimer != nil {
		store.idleTimer.Stop()
		store.idleTimer = nil
	}
}

// meetingRecordPayload is the wire shape for the `meeting` kanban event and
// GET /assistant/meetings items. serverNow is the client's clock-skew anchor:
// sharedElapsed = (Date.parse(startedAt) + (Date.now() - Date.parse(serverNow))).
func meetingRecordPayload(record meetingRecord, now time.Time) map[string]any {
	active := record.EndedAt == ""
	var durationSeconds int64
	if startedAt, err := time.Parse(time.RFC3339Nano, record.StartedAt); err == nil {
		end := now
		if !active {
			if endedAt, endErr := time.Parse(time.RFC3339Nano, record.EndedAt); endErr == nil {
				end = endedAt
			}
		}
		if elapsed := end.Sub(startedAt); elapsed > 0 {
			durationSeconds = int64(elapsed / time.Second)
		}
	}
	participants := record.Participants
	if participants == nil {
		participants = []string{}
	}
	return map[string]any{
		"id":              record.ID,
		"title":           record.Title,
		"titleSource":     record.TitleSource,
		"startedAt":       record.StartedAt,
		"endedAt":         record.EndedAt,
		"endedReason":     record.EndedReason,
		"archiveId":       record.ArchiveID,
		"participants":    participants,
		"active":          active,
		"durationSeconds": durationSeconds,
		"serverNow":       now.UTC().Format(time.RFC3339Nano),
	}
}

/* ---------- app lifecycle hooks ---------- */

// noteMeetingAdmission opens/extends the meeting record for an admitted
// participant. Called from the websocket accept path AFTER
// admitParticipantSession succeeds. Cancels any pending idle end FIRST so a
// rejoin inside the grace window keeps the meeting open. Broadcasts `meeting`
// on change.
func (app *kanbanBoardApp) noteMeetingAdmission(name string) {
	if app == nil || app.meetings == nil || app.memory == nil {
		return
	}
	app.meetings.cancelIdleEnd()
	id := app.memory.ensureMeetingID()
	if app.meetings.hasEndedRecord(id) {
		// The idle-end fire (or an archive) closed this id after the memory
		// store handed it out but before its rotation landed. An ended id must
		// never be re-minted onto a second record: rotate and start fresh. The
		// closer's own rotation is conditional (rotateMeetingIDIfCurrent), so
		// the fresh id below can never be clobbered by the racing seam.
		app.memory.rotateMeetingID()
		id = app.memory.ensureMeetingID()
	}
	record, changed := app.meetings.startMeeting(id, time.Now().UTC(), []string{name})
	if changed {
		app.broadcastMeetingRecord(record)
	}
}

// noteMeetingOccupancy arms the idle-end timer when the room empties. Called
// after forgetParticipantSession in the websocket cleanup path.
func (app *kanbanBoardApp) noteMeetingOccupancy() {
	if app == nil || app.meetings == nil {
		return
	}
	if app.activeParticipantCount() > 0 {
		return
	}
	if _, ok := app.meetings.activeRecord(); !ok {
		return
	}
	app.meetings.armIdleEnd(app.endMeetingForIdle)
}

// endMeetingForIdle fires from the grace timer: re-check emptiness, close the
// record, and rotate the memory meeting id so record lifecycle and entry
// keying stay aligned (the invariant other designs rely on). The locks never
// overlap, so the close itself validates the arm-time generation against
// cancelIdleEnd (see endMeetingIfIdleGeneration) — an admission racing the
// fired timer keeps its meeting open, and the rotation is conditional so a
// racing admission's freshly minted id is never cleared.
func (app *kanbanBoardApp) endMeetingForIdle(generation uint64) {
	if app == nil || app.meetings == nil {
		return
	}
	if app.activeParticipantCount() > 0 {
		// someone rejoined during the race; the meeting stays open.
		return
	}
	record, ok := app.meetings.activeRecord()
	if !ok {
		return
	}
	closed, changed := app.meetings.endMeetingIfIdleGeneration(record.ID, time.Now().UTC(), generation)
	if !changed {
		return
	}
	// The meeting is over: deliver anything queued with deliver
	// "after_meeting" before the id rotates (idempotent — archiveMeeting may
	// flush the same queue).
	app.flushDeferredNotifications("meeting_end")
	if app.memory != nil {
		app.memory.rotateMeetingIDIfCurrent(closed.ID)
	}
	app.broadcastMeetingRecord(closed)
}

// reconcileMeetingRecordsAtBoot runs once from newKanbanBoardApp: a stale open
// record whose id no longer matches the resumed memory meeting id closes with
// reason restart; a matching open record (memory resumed the same in-flight
// meeting) stays open with the idle timer armed — occupancy is zero at boot,
// and a join inside the grace window cancels it. With NO open record, a
// resumed memory id that matches an ENDED record is rotated away: idle end
// rotates only in-process, so after a restart newMeetingMemoryStore resumes
// the ended meeting's id (the last JSONL entry is not an archive) and the
// next admission would otherwise re-mint it onto a duplicate record.
func (app *kanbanBoardApp) reconcileMeetingRecordsAtBoot() {
	if app == nil || app.meetings == nil {
		return
	}
	record, ok := app.meetings.activeRecord()
	if !ok {
		if resumed := app.memory.currentMeetingID(); resumed != "" && app.meetings.hasEndedRecord(resumed) {
			app.memory.rotateMeetingID()
		}
		return
	}
	if record.ID != app.memory.currentMeetingID() {
		app.meetings.endMeeting(record.ID, time.Now().UTC(), meetingEndedReasonRestart, "")
		return
	}
	app.meetings.armIdleEnd(app.endMeetingForIdle)
}

func (app *kanbanBoardApp) broadcastMeetingRecord(record meetingRecord) {
	broadcastSignedInKanbanEvent("meeting", meetingRecordPayload(record, time.Now().UTC()))
}

// meetingSnapshot returns the active record payload for direct sends / HTTP,
// or nil when no meeting is active (the client clears its state on null).
func (app *kanbanBoardApp) meetingSnapshot() map[string]any {
	if app == nil || app.meetings == nil {
		return nil
	}
	record, ok := app.meetings.activeRecord()
	if !ok {
		return nil
	}
	return meetingRecordPayload(record, time.Now().UTC())
}

/* ---------- memory enrichment (Memory tool, D15) ---------- */

// Per-meeting caps for the enriched GET /assistant/meetings payload.
const (
	meetingDetailDecisionLimit = 12
	meetingDetailLogLimit      = 8
	meetingDetailLinkLimit     = 6
	meetingDetailSummaryLimit  = 480
	meetingDetailLogLineLimit  = 160
)

// meetingMemoryDetail is what the Memory tool's expanded meeting card shows:
// a summary, the decided checklist, capped log rows, linked board cards, and
// the visible entry count. All of it is derived from data the store already
// holds — nothing here is synthesized for display (D2/D15).
type meetingMemoryDetail struct {
	Summary        string
	archiveSummary string
	Decisions      []string
	Log            []map[string]string
	CardIDs        []string
	EntryCount     int
}

// meetingSummaryFromWriteUp lifts the Overview section (or the first prose
// paragraph) out of a meeting-brain markdown write-up.
func meetingSummaryFromWriteUp(text string) string {
	lines := strings.Split(text, "\n")
	inOverview := false
	collected := []string{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			heading := strings.ToLower(strings.Trim(trimmed, "# "))
			inOverview = strings.Contains(heading, "overview")
			continue
		}
		if inOverview && trimmed != "" {
			collected = append(collected, trimmed)
		}
	}
	if len(collected) == 0 {
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			collected = append(collected, trimmed)
			break
		}
	}
	return trimForStorage(strings.Join(collected, " "), meetingDetailSummaryLimit)
}

// meetingDetailLogLine flattens an entry's text to one bounded log-row line:
// the first prose line, skipping markdown headings (brain/board write-ups
// open with "## Summary"-style section markers).
func meetingDetailLogLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return trimForStorage(line, meetingDetailLogLineLimit)
	}
	return ""
}

// meetingMemoryDetails walks the memory store once and groups the Memory
// tool's expanded-card data by meeting id (only ids present in wanted).
func (app *kanbanBoardApp) meetingMemoryDetails(wanted map[string]struct{}) map[string]*meetingMemoryDetail {
	details := map[string]*meetingMemoryDetail{}
	if app == nil || app.memory == nil || len(wanted) == 0 {
		return details
	}
	for _, entry := range app.memory.snapshot(0) {
		meetingID := strings.TrimSpace(entry.Metadata["meetingId"])
		if meetingID == "" {
			continue
		}
		if _, ok := wanted[meetingID]; !ok {
			continue
		}
		detail := details[meetingID]
		if detail == nil {
			detail = &meetingMemoryDetail{}
			details[meetingID] = detail
		}
		switch entry.Kind {
		case meetingMemoryKindDecision:
			status := strings.TrimSpace(entry.Metadata["status"])
			if status == "" || status == decisionStatusActive {
				if len(detail.Decisions) < meetingDetailDecisionLimit {
					detail.Decisions = append(detail.Decisions, entry.Text)
				}
			}
			continue
		case meetingMemoryKindCodexProposal:
			if strings.TrimSpace(entry.Metadata["confirmedBy"]) != "" {
				detail.addCardID(entry.Metadata["cardId"])
			}
			continue
		case meetingMemoryKindScoutChat, meetingMemoryKindMissionInsight, meetingMemoryKindDecisionPass, meetingMemoryKindPackage, meetingMemoryKindDealRoom:
			// UI-state kinds never surface as meeting log rows.
			continue
		}

		// The remaining kinds are the visible-timeline family: they count
		// toward the entry total and feed the log rows.
		detail.EntryCount++
		kind := entry.Kind
		switch entry.Kind {
		case meetingMemoryKindTranscript:
			if entry.Metadata["source"] == transcriptSourceRoomChat {
				kind = "chat"
			}
		case meetingMemoryKindBrain:
			// the freshest brain write-up narrates the meeting
			detail.Summary = meetingSummaryFromWriteUp(entry.Text)
		case meetingMemoryKindBoardUpdate:
			for _, cardID := range strings.Split(entry.Metadata["cardIds"], ",") {
				detail.addCardID(cardID)
			}
		case meetingMemoryKindOSArtifact:
			detail.addCardID(entry.Metadata["boardCardId"])
		case meetingMemoryKindArchive:
			detail.archiveSummary = trimForStorage(strings.TrimSpace(entry.Text), meetingDetailSummaryLimit)
		}
		detail.Log = append(detail.Log, map[string]string{
			"kind": kind,
			"at":   entry.CreatedAt.UTC().Format(time.RFC3339Nano),
			"text": meetingDetailLogLine(entry.Text),
		})
	}
	for _, detail := range details {
		if detail.Summary == "" {
			detail.Summary = detail.archiveSummary
		}
		if overflow := len(detail.Log) - meetingDetailLogLimit; overflow > 0 {
			detail.Log = detail.Log[overflow:]
		}
	}
	return details
}

func (detail *meetingMemoryDetail) addCardID(cardID string) {
	cardID = strings.TrimSpace(cardID)
	if cardID == "" || len(detail.CardIDs) >= meetingDetailLinkLimit {
		return
	}
	for _, existing := range detail.CardIDs {
		if existing == cardID {
			return
		}
	}
	detail.CardIDs = append(detail.CardIDs, cardID)
}

// meetingDetailFields shapes a detail for the wire; link chips resolve card
// titles against the CURRENT board, so a deleted card never renders a dead
// jump target.
func meetingDetailFields(detail *meetingMemoryDetail, cardTitles map[string]string) map[string]any {
	if detail == nil {
		detail = &meetingMemoryDetail{}
	}
	decisions := detail.Decisions
	if decisions == nil {
		decisions = []string{}
	}
	logRows := detail.Log
	if logRows == nil {
		logRows = []map[string]string{}
	}
	links := make([]map[string]string, 0, len(detail.CardIDs))
	for _, cardID := range detail.CardIDs {
		title, ok := cardTitles[cardID]
		if !ok || strings.TrimSpace(title) == "" {
			continue
		}
		links = append(links, map[string]string{"cardId": cardID, "title": title})
	}
	return map[string]any{
		"summary":    detail.Summary,
		"decisions":  decisions,
		"log":        logRows,
		"links":      links,
		"entryCount": detail.EntryCount,
	}
}

/* ---------- HTTP ---------- */

// assistantMeetingsHandler serves GET /assistant/meetings to any signed-in
// user (same origin + session guards as the board handler): newest-first
// meeting records plus one top-level serverNow skew anchor.
func assistantMeetingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "meetings are unavailable")
		return
	}

	limit := meetingListLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}

	now := time.Now().UTC()
	records := kanbanApp.meetings.recent(limit)
	wanted := make(map[string]struct{}, len(records))
	for _, record := range records {
		wanted[record.ID] = struct{}{}
	}
	details := kanbanApp.meetingMemoryDetails(wanted)
	cardTitles := map[string]string{}
	for _, card := range kanbanApp.snapshotState().Cards {
		cardTitles[card.ID] = card.Title
	}
	meetings := make([]map[string]any, 0, len(records))
	for _, record := range records {
		item := meetingRecordPayload(record, now)
		// one top-level anchor instead of a per-item serverNow.
		delete(item, "serverNow")
		// Memory-tool enrichment (D15): summary, decided checklist, log
		// rows, and board-card links per meeting.
		for key, value := range meetingDetailFields(details[record.ID], cardTitles) {
			item[key] = value
		}
		meetings = append(meetings, item)
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"meetings":  meetings,
		"serverNow": now.Format(time.RFC3339Nano),
	})
}
