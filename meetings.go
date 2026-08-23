package main

// Meetings as first-class objects. A meetingRecord is the durable identity of
// one meeting: it adopts the SAME id the memory store stamps onto entries
// (metadata.meetingId), is opened eagerly at room admission, and is closed on
// archive, on last-leave + idle grace, or on boot when a stale open record no
// longer matches the resumed memory meeting id. The alignment invariant other
// designs rely on: the record closes exactly when the memory meeting id
// rotates.
//
// Session-end rule (founder decision 2026-07-08, card 078): a session is one
// SITTING — an empty room for five minutes (meetingIdleEndGrace default 5m,
// env MEETING_IDLE_END_GRACE) finalizes the meeting; the next entry mints a
// fresh id. The idle end closes the record, rotates the memory meeting id, and
// silently auto-archives a non-empty meeting (no email); the next join always
// starts a fresh meeting context. Emptiness is judged by
// activeParticipantCount(), which a liveness sweep drives to zero even when a
// zombie/backgrounded socket lingers (see sweepStaleParticipantSessions).
//
// Persistence is a sidecar JSON store (data/meetings.json, notifications.json
// pattern) — records mutate continuously (endedAt, auto-title, participants
// union), so they must never live in the append-only meeting-memory.jsonl.
//
// Lock-ordering rule: store methods only take store.mu, never app.mu, and
// never touch websockets; callers broadcast after every lock is released.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// meetingListLimit is the default newest-first page size for
	// GET /assistant/meetings.
	meetingListLimit = 20
	// meetingDirectoryScanLimit bounds one permanent-library page without
	// deleting older identities. Authorization can make a page sparse, but the
	// returned cursor always advances through this bounded directory window.
	meetingDirectoryScanLimit = 200
	meetingIdleCloseRetryBase = time.Second
	meetingIdleCloseRetryMax  = 30 * time.Second
)

const (
	meetingEndedReasonArchive = "archive"
	meetingEndedReasonIdle    = "idle"
	meetingEndedReasonRestart = "restart"
	// room_closed: the room itself was archived out from under the sitting
	// (rooms UX §3.7) — occupants were told and disconnected server-side.
	meetingEndedReasonRoomClosed = "room_closed"
)

const meetingTitleSourceAuto = "auto"

var ErrMeetingRecordStore = errors.New("meeting record store unavailable")

// meetingRecord is one first-class meeting. Timestamps are RFC3339Nano UTC
// strings (same convention as notificationRecord.CreatedAt).
type meetingRecord struct {
	ID             string                      `json:"id"`                    // == memory meetingId ("meeting-YYYYMMDD-HHMMSS-nnnnnnnnn")
	RoomID         string                      `json:"roomId,omitempty"`      // empty on read == office (§9 migration rule)
	ListenOnly     bool                        `json:"listenOnly,omitempty"`  // per-sitting latch (§7.1) — set/enforced in W4
	Title          string                      `json:"title,omitempty"`       // empty until auto-titled
	TitleSource    string                      `json:"titleSource,omitempty"` // "auto" (manual reserved for later)
	StartedAt      string                      `json:"startedAt"`
	EndedAt        string                      `json:"endedAt,omitempty"`        // empty == active
	EndedReason    string                      `json:"endedReason,omitempty"`    // archive | idle | restart
	ArchiveID      string                      `json:"archiveId,omitempty"`      // stamps at archive time
	IdleDeadlineAt string                      `json:"idleDeadlineAt,omitempty"` // durable all-empty grace boundary; cleared by admission/close
	Participants   []string                    `json:"participants"`             // union of admitted canonical names, meetingParticipantNames order
	Finalization   *meetingFinalizationReceipt `json:"finalization,omitempty"`
}

// meetingRoomID resolves a record's room under the migration invariant:
// records written before rooms existed carry no RoomID and are the office's.
func meetingRoomID(record meetingRecord) string {
	return normalizeRoomID(record.RoomID)
}

// storedMeetingRoomID is the write-side convention: office records persist
// with an EMPTY RoomID (omitempty), so meetings.json stays byte-compatible
// with the pre-room shape and a rolled-back binary reads them unchanged.
func storedMeetingRoomID(roomID string) string {
	if normalizeRoomID(roomID) == officeRoomID {
		return ""
	}
	return strings.TrimSpace(roomID)
}

type meetingStoreState struct {
	Meetings  []meetingRecord `json:"meetings"`
	UpdatedAt string          `json:"updatedAt,omitempty"`
}

type meetingStore struct {
	mu      sync.Mutex
	path    string
	records []meetingRecord // oldest-first; permanent identities are never evicted
	// persistState is an instance-local durability seam. Production leaves it
	// nil and uses writeJSONFileAtomically; focused recovery tests inject exact
	// definite/ambiguous outcomes without racing package globals.
	persistState           func(meetingStoreState) error
	directoryCursorIndexes map[string]int
	recordIndexes          map[string]int
	// idleTimers holds each room's pending idle-end timer (multi-room W2:
	// every sitting seam is keyed by normalized room id; office aliases the
	// pre-room behavior exactly).
	idleTimers map[string]*time.Timer
	// idleGenerations invalidates an in-flight idle fire PER ROOM: every
	// admission's cancelIdleEnd (and every re-arm) bumps the room's
	// generation, and endMeetingForIdle only closes the record when the
	// generation captured at arm time still matches — validated under mu in
	// the SAME critical section that stamps EndedAt, so a join landing after
	// the fire's occupancy check can never have its meeting closed underneath
	// it, and room A's fire can never validate against room B's counter.
	idleGenerations map[string]uint64
	// idleTimerDones and idleTimerCallbacks make timer teardown joinable.
	// time.Timer.Stop only tells us whether the callback was prevented; when
	// it returns false the callback may still be reading app/package state.
	// Tests that replace those globals must therefore wait for every callback
	// registered here, rather than treating Stop as a goroutine join.
	idleTimerDones     map[string]chan struct{}
	idleTimerCallbacks map[chan struct{}]struct{}
	idleRetryAttempts  map[string]int
	idleTimersStopped  bool
}

func meetingsPath() string {
	if path := strings.TrimSpace(os.Getenv("MEETINGS_PATH")); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "meetings.json")
}

// meetingIdleEndGrace is how long an empty room stays "in the meeting" before
// the record closes and the memory meeting id rotates. The five-minute default
// is the deliberate reconnection boundary: it absorbs refreshes, device swaps,
// and short network handoffs without fragmenting one sitting, while still
// closing promptly enough for final analysis and the next sitting's fresh id.
func meetingIdleEndGrace() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("MEETING_IDLE_END_GRACE")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 5 * time.Minute
}

func loadMeetingStore(path string) (*meetingStore, error) {
	records, err := loadMeetingStoreState(path)
	if err != nil {
		return nil, err
	}
	store := &meetingStore{path: path, records: records}
	store.rebuildDirectoryCursorIndexesLocked()
	return store, nil
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
	seenIDs := make(map[string]struct{}, len(state.Meetings))
	for _, record := range state.Meetings {
		record.ID = strings.TrimSpace(record.ID)
		if record.ID == "" || strings.TrimSpace(record.StartedAt) == "" {
			continue
		}
		if _, duplicate := seenIDs[record.ID]; duplicate {
			return nil, fmt.Errorf("decode meetings: duplicate meeting id %q", record.ID)
		}
		seenIDs[record.ID] = struct{}{}
		record.Participants, _ = unionMeetingParticipants(nil, record.Participants)
		records = append(records, record)
	}
	return records, nil
}

func (store *meetingStore) persistLocked() error {
	state := meetingStoreState{
		Meetings:  append([]meetingRecord(nil), store.records...),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	var err error
	if store.persistState != nil {
		err = store.persistState(state)
	} else {
		err = writeJSONFileAtomically(store.path, "meetings", state)
	}
	if err == nil {
		return nil
	}
	log.Errorf("Failed to persist meetings: %v", err)
	return err
}

func (store *meetingStore) resolvePersistFailureLocked(err error, rollback func()) {
	if errors.Is(err, ErrDurableReplaceAmbiguous) {
		if persisted, loadErr := loadMeetingStoreState(store.path); loadErr == nil {
			store.records = persisted
			store.rebuildDirectoryCursorIndexesLocked()
			return
		}
	}
	rollback()
	store.rebuildDirectoryCursorIndexesLocked()
}

func meetingDirectoryCursorForID(id string) string {
	mac := hmac.New(sha256.New, archiveTokenSecret())
	_, _ = mac.Write([]byte("meeting-directory-page/v1\x00" + strings.TrimSpace(id)))
	return hex.EncodeToString(mac.Sum(nil))
}

func (store *meetingStore) rebuildDirectoryCursorIndexesLocked() {
	store.directoryCursorIndexes = make(map[string]int, len(store.records))
	store.recordIndexes = make(map[string]int, len(store.records))
	for index, record := range store.records {
		store.directoryCursorIndexes[meetingDirectoryCursorForID(record.ID)] = index
		store.recordIndexes[record.ID] = index
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
	if record.Finalization != nil {
		finalization := cloneMeetingFinalizationReceipt(*record.Finalization)
		record.Finalization = &finalization
	}
	return record
}

// openRecordIndexLocked returns the index of the room's newest open record,
// or -1. Room identity follows meetingRoomID (absent RoomID == office).
func (store *meetingStore) openRecordIndexLocked(roomID string) int {
	roomID = normalizeRoomID(roomID)
	for index := len(store.records) - 1; index >= 0; index-- {
		if store.records[index].EndedAt == "" && meetingRoomID(store.records[index]) == roomID {
			return index
		}
	}
	return -1
}

// activeRecord returns the room's newest open record.
func (store *meetingStore) activeRecord(roomID string) (meetingRecord, bool) {
	if store == nil {
		return meetingRecord{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	if index := store.openRecordIndexLocked(roomID); index >= 0 {
		return cloneMeetingRecord(store.records[index]), true
	}
	return meetingRecord{}, false
}

// openRoomIDs lists the rooms that currently hold an open record — the boot
// reconciliation walks these alongside the memory store's resumed rooms.
func (store *meetingStore) openRoomIDs() []string {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	seen := map[string]struct{}{}
	roomIDs := []string{}
	for _, record := range store.records {
		if record.EndedAt != "" {
			continue
		}
		roomID := meetingRoomID(record)
		if _, ok := seen[roomID]; ok {
			continue
		}
		seen[roomID] = struct{}{}
		roomIDs = append(roomIDs, roomID)
	}
	return roomIDs
}

// recoverAnchoredMeetings repairs the only admissible missing-directory case:
// a checksummed admission anchor became durable before the meeting-store
// replace completed. It never mints from memory ids, timestamps, or room state;
// every created record is backed by a valid earliest anchor. All repairs land
// in one atomic meetings.json replace, and chronological successors close older
// orphan sittings so each room still has at most one open record.
func (store *meetingStore) recoverAnchoredMeetings(starts []AdmissionAnchor) ([]meetingRecord, error) {
	if store == nil {
		return nil, ErrMeetingRecordStore
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	priorRecords := make([]meetingRecord, len(store.records))
	for index := range store.records {
		priorRecords[index] = cloneMeetingRecord(store.records[index])
	}
	if store.recordIndexes == nil {
		store.rebuildDirectoryCursorIndexesLocked()
	}
	touched := map[string]struct{}{}
	changed := false
	for _, anchor := range starts {
		id := strings.TrimSpace(anchor.SittingID)
		roomID := normalizeRoomID(anchor.RoomID)
		if anchor.TenantID != canonicalTenantID() || id == "" || anchor.AdmittedAt.IsZero() {
			continue
		}
		if index, found := store.recordIndexes[id]; found {
			if index < 0 || index >= len(store.records) || meetingRoomID(store.records[index]) != roomID {
				store.records = priorRecords
				store.rebuildDirectoryCursorIndexesLocked()
				return nil, fmt.Errorf("%w: anchored sitting %s conflicts with its meeting record", ErrMeetingRecordStore, id)
			}
			startedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(store.records[index].StartedAt))
			if err != nil || anchor.AdmittedAt.UTC().Before(startedAt.UTC()) {
				store.records[index].StartedAt = anchor.AdmittedAt.UTC().Format(time.RFC3339Nano)
				changed = true
				touched[id] = struct{}{}
			}
			continue
		}
		store.records = append(store.records, meetingRecord{
			ID: id, RoomID: storedMeetingRoomID(roomID), StartedAt: anchor.AdmittedAt.UTC().Format(time.RFC3339Nano), Participants: []string{},
		})
		changed = true
		touched[id] = struct{}{}
		store.rebuildDirectoryCursorIndexesLocked()
	}
	if !changed {
		return nil, nil
	}

	// Keep the permanent directory chronological and close every non-latest open
	// sitting at the next admitted sitting boundary. Existing ended records retain
	// their stronger archive/idle reason and timestamp.
	sort.SliceStable(store.records, func(i, j int) bool {
		left, leftErr := time.Parse(time.RFC3339Nano, store.records[i].StartedAt)
		right, rightErr := time.Parse(time.RFC3339Nano, store.records[j].StartedAt)
		if leftErr == nil && rightErr == nil && !left.Equal(right) {
			return left.Before(right)
		}
		return store.records[i].ID < store.records[j].ID
	})
	byRoom := map[string][]int{}
	for index, record := range store.records {
		byRoom[meetingRoomID(record)] = append(byRoom[meetingRoomID(record)], index)
	}
	for _, indexes := range byRoom {
		for position := 0; position+1 < len(indexes); position++ {
			index := indexes[position]
			if store.records[index].EndedAt != "" {
				continue
			}
			next := store.records[indexes[position+1]]
			store.records[index].EndedAt = next.StartedAt
			store.records[index].EndedReason = meetingEndedReasonRestart
			store.records[index].IdleDeadlineAt = ""
			if store.records[index].Finalization == nil {
				closedAt, _ := time.Parse(time.RFC3339Nano, next.StartedAt)
				receipt := newMeetingFinalizationReceipt(meetingFinalizationSourceHighWater{}, closedAt)
				store.records[index].Finalization = &receipt
			}
			changed = true
			touched[store.records[index].ID] = struct{}{}
		}
	}
	store.rebuildDirectoryCursorIndexesLocked()
	if err := store.persistLocked(); err != nil {
		store.resolvePersistFailureLocked(err, func() { store.records = priorRecords })
		return nil, fmt.Errorf("%w: %v", ErrMeetingRecordStore, err)
	}
	recovered := make([]meetingRecord, 0, len(touched))
	for _, record := range store.records {
		if _, ok := touched[record.ID]; ok {
			recovered = append(recovered, cloneMeetingRecord(record))
		}
	}
	return recovered, nil
}

// startMeeting opens (or extends) the room's record for id. If the room's
// open record already carries the SAME id the start is a no-op that unions
// participants; an open record with a DIFFERENT id (should not happen;
// defensive against the idle-timer race) is closed with reason restart first.
// The defensive close is room-scoped by construction: it can only ever close
// a record belonging to the SAME room (openRecordIndexLocked filters by
// room), so one room starting a sitting never restarts another's.
func (store *meetingStore) startMeeting(roomID string, id string, startedAt time.Time, participants []string) (meetingRecord, bool) {
	record, changed, _ := store.startMeetingDurable(roomID, id, startedAt, participants)
	return record, changed
}

// startMeetingDurable is the admission-authority variant of startMeeting. It
// surfaces persistence failure so a durable admission anchor can never publish
// a live participant while its matching meeting record is absent. When a retry
// presents the winning earlier anchor timestamp, the record start moves back to
// that authority rather than preserving the later process-recovery time.
func (store *meetingStore) startMeetingDurable(roomID string, id string, startedAt time.Time, participants []string) (meetingRecord, bool, error) {
	id = strings.TrimSpace(id)
	if store == nil || id == "" {
		return meetingRecord{}, false, fmt.Errorf("meeting store is unavailable")
	}
	if startedAt.IsZero() {
		return meetingRecord{}, false, fmt.Errorf("meeting start authority is missing")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.recordIndexes == nil {
		store.rebuildDirectoryCursorIndexesLocked()
	}
	if existingIndex, exists := store.recordIndexes[id]; exists {
		if existingIndex < 0 || existingIndex >= len(store.records) {
			return meetingRecord{}, false, fmt.Errorf("meeting record index is invalid")
		}
		existing := &store.records[existingIndex]
		if existing.RoomID != storedMeetingRoomID(roomID) || existing.EndedAt != "" {
			return cloneMeetingRecord(*existing), false, fmt.Errorf("meeting sitting is not open in this room")
		}
		union, changed := unionMeetingParticipants(existing.Participants, participants)
		priorStart, startErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(existing.StartedAt))
		startChanged := startErr != nil || startedAt.UTC().Before(priorStart.UTC())
		idleChanged := strings.TrimSpace(existing.IdleDeadlineAt) != ""
		if changed || startChanged || idleChanged {
			prior := cloneMeetingRecord(*existing)
			existing.Participants = union
			if startChanged {
				existing.StartedAt = startedAt.UTC().Format(time.RFC3339Nano)
			}
			existing.IdleDeadlineAt = ""
			if err := store.persistLocked(); err != nil {
				store.resolvePersistFailureLocked(err, func() { store.records[existingIndex] = prior })
				return cloneMeetingRecord(store.records[existingIndex]), false, err
			}
		}
		return cloneMeetingRecord(store.records[existingIndex]), changed || startChanged || idleChanged, nil
	}

	priorRecords := make([]meetingRecord, len(store.records))
	for index := range store.records {
		priorRecords[index] = cloneMeetingRecord(store.records[index])
	}
	if index := store.openRecordIndexLocked(roomID); index >= 0 {
		store.records[index].EndedAt = startedAt.UTC().Format(time.RFC3339Nano)
		store.records[index].EndedReason = meetingEndedReasonRestart
		store.records[index].IdleDeadlineAt = ""
		if store.records[index].Finalization == nil {
			receipt := newMeetingFinalizationReceipt(meetingFinalizationSourceHighWater{}, startedAt)
			store.records[index].Finalization = &receipt
		}
	}

	union, _ := unionMeetingParticipants(nil, participants)
	record := meetingRecord{
		ID:           id,
		RoomID:       storedMeetingRoomID(roomID),
		StartedAt:    startedAt.UTC().Format(time.RFC3339Nano),
		Participants: union,
	}
	store.records = append(store.records, record)
	if store.directoryCursorIndexes == nil || store.recordIndexes == nil {
		store.rebuildDirectoryCursorIndexesLocked()
	} else {
		store.directoryCursorIndexes[meetingDirectoryCursorForID(record.ID)] = len(store.records) - 1
		store.recordIndexes[record.ID] = len(store.records) - 1
	}
	if err := store.persistLocked(); err != nil {
		store.resolvePersistFailureLocked(err, func() { store.records = priorRecords })
		return meetingRecord{}, false, err
	}
	return cloneMeetingRecord(record), true, nil
}

// recordByID returns the record (open or ended) carrying id — the
// meetingListenOnly lookup workers use over historical windows.
func (store *meetingStore) recordByID(id string) (meetingRecord, bool) {
	if store == nil || strings.TrimSpace(id) == "" {
		return meetingRecord{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.recordIndexes == nil {
		store.rebuildDirectoryCursorIndexesLocked()
	}

	index, found := store.recordIndexes[id]
	if found && index >= 0 && index < len(store.records) && store.records[index].ID == id {
		return cloneMeetingRecord(store.records[index]), true
	}
	return meetingRecord{}, false
}

// latchListenOnly sets the §7.1 per-sitting listen-only latch on the OPEN
// record carrying id. One-way by construction: nothing ever writes false, so
// the latch persists after the last guest leaves and only the next sitting's
// fresh record returns to full mode.
func (store *meetingStore) latchListenOnly(id string) (meetingRecord, bool) {
	if store == nil || strings.TrimSpace(id) == "" {
		return meetingRecord{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	for index := len(store.records) - 1; index >= 0; index-- {
		if store.records[index].ID != id || store.records[index].EndedAt != "" {
			continue
		}
		if store.records[index].ListenOnly {
			return cloneMeetingRecord(store.records[index]), false
		}
		store.records[index].ListenOnly = true
		if err := store.persistLocked(); err != nil {
			store.resolvePersistFailureLocked(err, func() { store.records[index].ListenOnly = false })
			return cloneMeetingRecord(store.records[index]), false
		}
		return cloneMeetingRecord(store.records[index]), true
	}
	return meetingRecord{}, false
}

// addParticipant union-adds a canonical name to the open record with this id.
func (store *meetingStore) addParticipant(id string, name string) (meetingRecord, bool) {
	if store == nil || strings.TrimSpace(id) == "" {
		return meetingRecord{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	// look the OPEN record up by id directly (ids are globally unique): with
	// per-room records the newest open record may belong to another room.
	index := -1
	for candidate := len(store.records) - 1; candidate >= 0; candidate-- {
		if store.records[candidate].ID == id && store.records[candidate].EndedAt == "" {
			index = candidate
			break
		}
	}
	if index < 0 {
		return meetingRecord{}, false
	}
	union, changed := unionMeetingParticipants(store.records[index].Participants, []string{name})
	if changed {
		prior := append([]string(nil), store.records[index].Participants...)
		store.records[index].Participants = union
		if err := store.persistLocked(); err != nil {
			store.resolvePersistFailureLocked(err, func() { store.records[index].Participants = prior })
			return cloneMeetingRecord(store.records[index]), false
		}
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
// only lands when generation still matches the ROOM's idle generation,
// checked under mu atomically with the EndedAt stamp. A rejoin whose
// cancelIdleEnd bumped the room's generation after the timer fired makes the
// in-flight close a no-op — and another room's fire can never validate here.
func (store *meetingStore) endMeetingIfIdleGeneration(roomID string, id string, endedAt time.Time, generation uint64) (meetingRecord, bool) {
	if store == nil || strings.TrimSpace(id) == "" {
		return meetingRecord{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	if generation != store.idleGenerations[normalizeRoomID(roomID)] {
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
		prior := cloneMeetingRecord(store.records[index])
		store.records[index].EndedAt = endedAt.UTC().Format(time.RFC3339Nano)
		store.records[index].EndedReason = reason
		store.records[index].ArchiveID = strings.TrimSpace(archiveID)
		store.records[index].IdleDeadlineAt = ""
		if err := store.persistLocked(); err != nil {
			store.resolvePersistFailureLocked(err, func() { store.records[index] = prior })
			return cloneMeetingRecord(prior), false
		}
		return cloneMeetingRecord(store.records[index]), true
	}
	return meetingRecord{}, false
}

// stampArchiveID lands an archive id on an ENDED record that has none yet —
// the idle auto-archive seam: endMeetingForIdle closes the record first and
// the archive file is written after, so the stamp is a separate step. Open
// records are refused (archiveMeeting stamps those atomically with the
// close), and a stamped record never restamps, so a duplicate idle fire can
// never point the record at a second archive.
func (store *meetingStore) stampArchiveID(id string, archiveID string) (meetingRecord, bool) {
	archiveID = strings.TrimSpace(archiveID)
	if store == nil || strings.TrimSpace(id) == "" || archiveID == "" {
		return meetingRecord{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	for index := len(store.records) - 1; index >= 0; index-- {
		if store.records[index].ID != id {
			continue
		}
		if store.records[index].EndedAt == "" || store.records[index].ArchiveID != "" {
			return cloneMeetingRecord(store.records[index]), false
		}
		prior := store.records[index].ArchiveID
		store.records[index].ArchiveID = archiveID
		if err := store.persistLocked(); err != nil {
			store.resolvePersistFailureLocked(err, func() { store.records[index].ArchiveID = prior })
			return cloneMeetingRecord(store.records[index]), false
		}
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
		prior := cloneMeetingRecord(store.records[index])
		store.records[index].Title = title
		store.records[index].TitleSource = meetingTitleSourceAuto
		if err := store.persistLocked(); err != nil {
			store.resolvePersistFailureLocked(err, func() { store.records[index] = prior })
			return cloneMeetingRecord(prior), false
		}
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

// recentPage returns a bounded newest-first directory window after cursor.
// The cursor is an opaque managed commitment to the last directory identity
// observed by the prior page. Permanent identities make it stable across
// restart and later appends without exposing an unauthorized meeting id.
func (store *meetingStore) recentPage(limit int, cursor string) ([]meetingRecord, string, bool) {
	records := []meetingRecord{}
	if store == nil || limit < 1 {
		return records, "", false
	}
	cursor = strings.TrimSpace(cursor)
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.directoryCursorIndexes == nil || store.recordIndexes == nil {
		store.rebuildDirectoryCursorIndexesLocked()
	}
	start := len(store.records) - 1
	if cursor != "" {
		index, found := store.directoryCursorIndexes[cursor]
		if !found || index < 1 {
			return records, "", false
		}
		start = index - 1
	}
	for index := start; index >= 0 && len(records) < limit; index-- {
		records = append(records, cloneMeetingRecord(store.records[index]))
	}
	if len(records) == 0 {
		return records, "", false
	}
	nextCursor := meetingDirectoryCursorForID(records[len(records)-1].ID)
	hasMore := start-len(records) >= 0
	return records, nextCursor, hasMore
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

// armIdleEnd schedules a process-local timer for callers that do not own an
// open meeting record. Meeting lifecycle callers use armIdleEndDurable below,
// which first journals the absolute deadline so a restart cannot grant a
// second grace window.
func (store *meetingStore) armIdleEnd(roomID string, fire func(generation uint64)) {
	if store == nil {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.armIdleEndLocked(roomID, meetingIdleEndGrace(), fire)
}

// armIdleEndDurable records the first all-empty deadline for the exact open
// sitting and schedules only the remaining duration. Repeated empty signals
// and boot reconciliation preserve that timestamp; a successful admission
// clears it in startMeetingDurable's same durable write.
func (store *meetingStore) armIdleEndDurable(roomID, meetingID string, emptyAt time.Time, fire func(generation uint64)) (meetingRecord, bool, error) {
	if store == nil || strings.TrimSpace(meetingID) == "" {
		return meetingRecord{}, false, ErrMeetingRecordStore
	}
	if emptyAt.IsZero() {
		emptyAt = time.Now().UTC()
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.idleTimersStopped {
		return meetingRecord{}, false, nil
	}
	index := store.openRecordIndexLocked(roomID)
	if index < 0 || store.records[index].ID != strings.TrimSpace(meetingID) {
		return meetingRecord{}, false, nil
	}
	record := &store.records[index]
	deadline, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(record.IdleDeadlineAt))
	changed := strings.TrimSpace(record.IdleDeadlineAt) == "" || parseErr != nil
	if changed {
		if store.idleRetryAttempts != nil {
			delete(store.idleRetryAttempts, normalizeRoomID(roomID))
		}
		prior := cloneMeetingRecord(*record)
		deadline = emptyAt.UTC().Add(meetingIdleEndGrace())
		record.IdleDeadlineAt = deadline.Format(time.RFC3339Nano)
		if err := store.persistLocked(); err != nil {
			store.resolvePersistFailureLocked(err, func() { store.records[index] = prior })
			// Admission cancels the prior process timer before it attempts the
			// participant-union write. If that write (or this deadline write)
			// fails, retaining no close guard would strand the empty sitting for
			// the rest of this process. Prefer the deadline recovered from an
			// ambiguous durable replacement; otherwise keep a best-effort local
			// guard at the proposed absolute deadline while returning the error
			// so callers still fail the admission closed.
			if recovered, parseRecoveredErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(store.records[index].IdleDeadlineAt)); parseRecoveredErr == nil {
				deadline = recovered
			}
			delay := deadline.Sub(time.Now().UTC())
			if delay < 0 {
				delay = 0
			}
			store.armIdleEndLocked(roomID, delay, fire)
			return cloneMeetingRecord(store.records[index]), false, fmt.Errorf("%w: %v", ErrMeetingRecordStore, err)
		}
	}
	delay := deadline.Sub(time.Now().UTC())
	if delay < 0 {
		delay = 0
	}
	store.armIdleEndLocked(roomID, delay, fire)
	return cloneMeetingRecord(store.records[index]), changed, nil
}

// rearmIdleCloseAfterFailure keeps the original durable deadline authoritative
// after a close write definitely failed. Since that deadline is already due,
// retrying at delay zero would create a disk-failure busy loop; instead a
// bounded process-local backoff re-enters the same generation-fenced close.
// A restart reads the unchanged past-due IdleDeadlineAt and retries as well.
func (store *meetingStore) rearmIdleCloseAfterFailure(roomID, meetingID string, generation uint64, fire func(generation uint64)) bool {
	if store == nil || strings.TrimSpace(meetingID) == "" {
		return false
	}
	roomID = normalizeRoomID(roomID)
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.idleTimersStopped || store.idleGenerations[roomID] != generation {
		return false
	}
	index := store.openRecordIndexLocked(roomID)
	if index < 0 || store.records[index].ID != strings.TrimSpace(meetingID) || strings.TrimSpace(store.records[index].IdleDeadlineAt) == "" {
		return false
	}
	if store.idleRetryAttempts == nil {
		store.idleRetryAttempts = map[string]int{}
	}
	store.idleRetryAttempts[roomID]++
	delay := meetingIdleCloseRetryBase
	for attempt := 1; attempt < store.idleRetryAttempts[roomID] && delay < meetingIdleCloseRetryMax; attempt++ {
		delay *= 2
		if delay > meetingIdleCloseRetryMax {
			delay = meetingIdleCloseRetryMax
		}
	}
	store.armIdleEndLocked(roomID, delay, fire)
	return true
}

func (store *meetingStore) clearIdleCloseRetry(roomID string) {
	if store == nil {
		return
	}
	store.mu.Lock()
	if store.idleRetryAttempts != nil {
		delete(store.idleRetryAttempts, normalizeRoomID(roomID))
	}
	store.mu.Unlock()
}

// armIdleEndLocked replaces the room's pending callback at an explicit delay.
// The caller holds store.mu. Generation fencing remains process-local; the
// absolute deadline above is the restart authority.
func (store *meetingStore) armIdleEndLocked(roomID string, delay time.Duration, fire func(generation uint64)) {

	if store.idleTimersStopped {
		return
	}
	roomID = normalizeRoomID(roomID)
	if store.idleTimers == nil {
		store.idleTimers = map[string]*time.Timer{}
	}
	if store.idleGenerations == nil {
		store.idleGenerations = map[string]uint64{}
	}
	if store.idleTimerDones == nil {
		store.idleTimerDones = map[string]chan struct{}{}
	}
	if store.idleTimerCallbacks == nil {
		store.idleTimerCallbacks = map[chan struct{}]struct{}{}
	}
	if store.idleTimers[roomID] != nil {
		if store.idleTimers[roomID].Stop() {
			store.finishIdleTimerCallbackLocked(store.idleTimerDones[roomID])
		}
		store.idleGenerations[roomID]++
	}
	generation := store.idleGenerations[roomID]
	done := make(chan struct{})
	store.idleTimerDones[roomID] = done
	store.idleTimerCallbacks[done] = struct{}{}
	store.idleTimers[roomID] = time.AfterFunc(delay, func() {
		defer func() {
			store.mu.Lock()
			store.finishIdleTimerCallbackLocked(done)
			store.mu.Unlock()
		}()
		fire(generation)
	})
}

// cancelIdleEnd stops the room's pending idle end AND bumps the room's
// generation: a timer whose callback already fired (Stop returned false) is
// invalidated before it can stamp EndedAt.
func (store *meetingStore) cancelIdleEnd(roomID string) {
	if store == nil {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	roomID = normalizeRoomID(roomID)
	if store.idleRetryAttempts != nil {
		delete(store.idleRetryAttempts, roomID)
	}
	if store.idleGenerations == nil {
		store.idleGenerations = map[string]uint64{}
	}
	store.idleGenerations[roomID]++
	if store.idleTimers[roomID] != nil {
		if store.idleTimers[roomID].Stop() {
			store.finishIdleTimerCallbackLocked(store.idleTimerDones[roomID])
		}
		delete(store.idleTimers, roomID)
		delete(store.idleTimerDones, roomID)
	}
}

// finishIdleTimerCallbackLocked marks one timer callback complete exactly
// once. The caller holds store.mu. A successfully stopped timer is completed
// by its stopper; a timer whose callback started is completed by the callback.
func (store *meetingStore) finishIdleTimerCallbackLocked(done chan struct{}) {
	if done == nil || store.idleTimerCallbacks == nil {
		return
	}
	if _, ok := store.idleTimerCallbacks[done]; !ok {
		return
	}
	delete(store.idleTimerCallbacks, done)
	close(done)
}

// stopIdleEndsAndWait permanently stops this store's idle timers and joins
// any callback already in flight. Production does not normally close the
// process in-place, but isolated tests replace package globals that callbacks
// consult; those replacements are only race-safe after this join returns.
func (store *meetingStore) stopIdleEndsAndWait() {
	if store == nil {
		return
	}

	store.mu.Lock()
	store.idleTimersStopped = true
	for roomID, timer := range store.idleTimers {
		store.idleGenerations[roomID]++
		if timer != nil && timer.Stop() {
			store.finishIdleTimerCallbackLocked(store.idleTimerDones[roomID])
		}
		delete(store.idleTimers, roomID)
		delete(store.idleTimerDones, roomID)
	}
	pending := make([]chan struct{}, 0, len(store.idleTimerCallbacks))
	for done := range store.idleTimerCallbacks {
		pending = append(pending, done)
	}
	store.mu.Unlock()

	for _, done := range pending {
		<-done
	}
}

// meetingRecordPayload is the wire shape for the `meeting` kanban event and
// GET /assistant/meetings items. serverNow is the client's clock-skew anchor:
// sharedElapsed = (Date.parse(startedAt) + (Date.now() - Date.parse(serverNow))).
func meetingRecordPayload(record meetingRecord, now time.Time) map[string]any {
	active := record.EndedAt == ""
	finalizationState := "active"
	analysisReady := false
	if !active {
		finalizationState = "untracked"
		if record.Finalization != nil {
			finalizationState = record.Finalization.State
			analysisReady = meetingFinalizationReceiptReady(record.Finalization)
			if finalizationState != meetingFinalizationClosing && finalizationState != meetingFinalizationFinalized && finalizationState != meetingFinalizationDegraded {
				finalizationState = meetingFinalizationDegraded
			}
		}
	}
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
	payload := map[string]any{
		"id":                record.ID,
		"roomId":            normalizeRoomID(record.RoomID),
		"title":             record.Title,
		"titleSource":       record.TitleSource,
		"startedAt":         record.StartedAt,
		"endedAt":           record.EndedAt,
		"endedReason":       record.EndedReason,
		"archiveId":         record.ArchiveID,
		"participants":      participants,
		"active":            active,
		"finalizationState": finalizationState,
		"analysisReady":     analysisReady,
		"durationSeconds":   durationSeconds,
		"serverNow":         now.UTC().Format(time.RFC3339Nano),
	}
	if record.Finalization != nil {
		receipt := cloneMeetingFinalizationReceipt(*record.Finalization)
		payload["finalization"] = receipt
	}
	return payload
}

// meetingRecordPayload applies the app's durable-output validation to the
// pure wire-shape helper above. A finalized receipt is necessary but not
// sufficient for analysis readiness: its exact source-stamped Brain/digest
// outputs must still be present and current. Production reads and broadcasts
// use this method so a deleted or corrupt output cannot look ready during the
// interval before boot recovery reopens the receipt.
func (app *kanbanBoardApp) meetingRecordPayload(record meetingRecord, now time.Time) map[string]any {
	payload := meetingRecordPayload(record, now)
	payload["analysisReady"] = app.meetingFinalizationOutputsReady(record)
	return payload
}

/* ---------- app lifecycle hooks ---------- */

// prepareMeetingSittingID mints or resolves the sitting identity without
// opening a meeting record or broadcasting participant state. The websocket
// path uses this identity to persist its admission anchor first.
func (app *kanbanBoardApp) prepareMeetingSittingID(roomID string) string {
	if app == nil || app.meetings == nil || app.memory == nil {
		return ""
	}
	roomID = normalizeRoomID(roomID)
	id := app.memory.ensureMeetingID(roomID)
	if app.meetings.hasEndedRecord(id) {
		// The idle-end fire (or an archive) closed this id after the memory
		// store handed it out but before its rotation landed. An ended id must
		// never be re-minted onto a second record: rotate and start fresh. The
		// closer's own rotation is conditional (rotateMeetingIDIfCurrent), so
		// the fresh id below can never be clobbered by the racing seam.
		app.memory.rotateMeetingID(roomID)
		id = app.memory.ensureMeetingID(roomID)
	}
	return id
}

// noteMeetingAdmission opens/extends the room's meeting record for an
// admitted participant. Legacy/internal callers prepare and commit together;
// the websocket path calls noteMeetingAdmissionForSitting only after its
// durable admission anchor succeeds.
func (app *kanbanBoardApp) noteMeetingAdmission(roomID string, name string) string {
	return app.noteMeetingAdmissionForSitting(roomID, name, app.prepareMeetingSittingID(roomID))
}

func (app *kanbanBoardApp) noteMeetingAdmissionForSitting(roomID string, name string, id string) string {
	if app == nil || app.meetings == nil || app.memory == nil || strings.TrimSpace(id) == "" {
		return ""
	}
	roomID = normalizeRoomID(roomID)
	// A close/rotation racing between preparation and durable anchor write must
	// not attach the participant to a different sitting than the anchor.
	if app.memory.currentMeetingID(roomID) != id || app.meetings.hasEndedRecord(id) {
		return ""
	}
	app.meetings.cancelIdleEnd(roomID)
	record, changed := app.meetings.startMeeting(roomID, id, time.Now().UTC(), []string{name})
	// §7.1 listen-only latch: guest-enabled at the sitting's start OR a guest
	// admitted mid-sitting (guest admissions land here too) latches the record.
	// One-way — latchListenOnly never writes false — so a guest leaving
	// mid-meeting cannot return the sitting to full mode.
	if !record.ListenOnly && app.roomListenOnly(roomID) {
		if latched, flipped := app.meetings.latchListenOnly(id); flipped {
			record = latched
			changed = true
		}
	}
	if changed {
		app.broadcastMeetingRecord(record)
	}
	return record.ID
}

// publishAnchoredMeetingAdmission completes the non-durable presentation side
// of an anchored admission. The meeting record itself was already persisted
// before the live seat committed; this step only applies the per-sitting guest
// latch and broadcasts the resulting record after app.mu is released.
func (app *kanbanBoardApp) publishAnchoredMeetingAdmission(admission participantAdmissionResult) string {
	record := admission.meeting
	if app == nil || app.meetings == nil || strings.TrimSpace(record.ID) == "" {
		return ""
	}
	changed := admission.meetingChanged
	roomID := meetingRoomID(record)
	if !record.ListenOnly && app.roomListenOnly(roomID) {
		if latched, flipped := app.meetings.latchListenOnly(record.ID); flipped {
			record = latched
			changed = true
		}
	}
	if changed {
		app.broadcastMeetingRecord(record)
	}
	return record.ID
}

// noteMeetingOccupancy arms the room's idle-end timer when that room empties.
// Called after forgetParticipantSession in the websocket cleanup path.
func (app *kanbanBoardApp) noteMeetingOccupancy(roomID string) {
	if app == nil || app.meetings == nil {
		return
	}
	app.meetingLifecycleMu.RLock()
	defer app.meetingLifecycleMu.RUnlock()
	roomID = normalizeRoomID(roomID)
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)
	if app.activeParticipantCountInRoomLocked(state) > 0 {
		return
	}
	record, ok := app.meetings.activeRecord(roomID)
	if !ok {
		return
	}
	if _, _, err := app.meetings.armIdleEndDurable(roomID, record.ID, time.Now().UTC(), func(generation uint64) { app.endMeetingForIdle(roomID, generation) }); err != nil {
		log.Errorf("Could not persist empty-room deadline for meeting %s: %v", record.ID, err)
	}
}

// endMeetingForIdle fires from a room's grace timer: re-check that room's
// emptiness, close its record, and rotate its memory meeting id so record
// lifecycle and entry keying stay aligned (the invariant other designs rely
// on). The locks never overlap, so the close itself validates the arm-time
// generation against the room's cancelIdleEnd counter (see
// endMeetingIfIdleGeneration) — an admission racing the fired timer keeps its
// meeting open, and the rotation is conditional AND room-scoped, so a racing
// admission's freshly minted id — or another room's live sitting — is never
// cleared.
func (app *kanbanBoardApp) endMeetingForIdle(roomID string, generation uint64) {
	if app == nil || app.meetings == nil {
		return
	}
	app.meetingLifecycleMu.Lock()
	defer app.meetingLifecycleMu.Unlock()
	roomID = normalizeRoomID(roomID)
	if app.activeParticipantCount(roomID) > 0 {
		// someone rejoined during the race; the meeting stays open.
		return
	}
	record, ok := app.meetings.activeRecord(roomID)
	if !ok {
		return
	}
	source := app.meetingFinalizationSource(record.ID)
	closed, changed, closeErr := app.meetings.endMeetingWithFinalizationIfIdleGeneration(roomID, record.ID, time.Now().UTC(), generation, source)
	if closeErr != nil {
		log.Errorf("Could not durably begin meeting %s finalization: %v", record.ID, closeErr)
		app.meetings.rearmIdleCloseAfterFailure(roomID, record.ID, generation, func(nextGeneration uint64) {
			app.endMeetingForIdle(roomID, nextGeneration)
		})
		return
	}
	if !changed {
		return
	}
	app.meetings.clearIdleCloseRetry(roomID)
	if app.meetingSpecialists != nil {
		app.meetingSpecialists.CloseScope(roomID, closed.ID, "room_closed")
	}
	if roomID == officeRoomID {
		app.cancelOfficeScoutWorkForSitting(closed.ID)
	}
	// The meeting is over: deliver anything queued with deliver
	// "after_meeting" before the id rotates (idempotent — archiveMeeting may
	// flush the same queue).
	app.flushDeferredNotifications("meeting_end")
	// The durable receipt already says closing. Rotate and release media before
	// any model latency: a post-grace rejoin must mint a successor immediately,
	// never inherit the ended sitting while its Brain/digest are being finalized.
	if app.memory != nil {
		app.memory.rotateMeetingIDIfCurrent(roomID, closed.ID)
	}
	app.broadcastMeetingRecord(closed)
	app.teardownRoomMediaAfterIdle(roomID)
	app.flushRoomFollowThroughForMeeting(roomID, closed.ID, "meeting_end")
	// The session is over for good (empty past the grace): silently archive
	// what the meeting captured so the next join starts a fresh context with
	// the prior one preserved. The archive embeds `closing`; the asynchronous
	// core runner refreshes it to finalized/degraded from its durable receipt.
	app.autoArchiveIdleMeeting(closed)
	app.scheduleMeetingCoreFinalization(closed.ID)
	broadcastRoomsSnapshot()
}

// reconcileMeetingRecordsAtBoot first repairs the admission-anchor -> meeting
// directory crash seam, then runs once PER ROOM (the union of rooms holding an
// open record and rooms whose memory meeting id resumed). A stale open record
// whose id no longer matches the room's resumed memory meeting id closes with
// reason restart; a matching open record stays open with its idle timer armed.
// With NO open record, a resumed memory id that matches an ENDED record rotates
// away. A recovered anchor-backed record may restore an otherwise-empty memory
// sitting id; no unanchored memory id is ever allowed to create a record.
func (app *kanbanBoardApp) reconcileMeetingRecordsAtBoot() {
	if app == nil || app.meetings == nil {
		return
	}
	app.admissionAnchorMu.RLock()
	anchors := app.admissionAnchors
	anchorErr := app.admissionAnchorErr
	app.admissionAnchorMu.RUnlock()
	if anchors != nil && anchorErr == nil {
		// Complete or abort the explicit manual-rollover cross-file journal
		// before ordinary anchor recovery. A durable successor meeting proves
		// the atomic close/open landed; otherwise staged anchors are discarded
		// so they can never manufacture a successor after a failed archive.
		if pending, err := anchors.PendingRollovers(context.Background()); err != nil {
			app.latchAdmissionAnchorFailure(err)
			log.Errorf("Could not read pending admission rollovers: %v", err)
		} else {
			for _, rollover := range pending {
				record, committed := app.meetings.recordByID(rollover.SittingID)
				if committed && app.memory != nil {
					currentID := app.memory.currentMeetingID(rollover.RoomID)
					if currentID == rollover.Predecessor {
						app.memory.transitionMeetingIDIfCurrent(rollover.RoomID, rollover.Predecessor, rollover.SittingID)
					} else if currentID == "" && record.EndedAt == "" {
						app.memory.resumeMeetingIDIfEmpty(rollover.RoomID, rollover.SittingID)
					}
				}
				if err := anchors.ResolvePendingRollover(context.Background(), rollover, committed); err != nil {
					app.latchAdmissionAnchorFailure(err)
					log.Errorf("Could not resolve pending admission rollover %s: %v", rollover.SittingID, err)
				}
			}
		}
		starts, err := anchors.SittingStarts(context.Background())
		if err != nil {
			app.latchAdmissionAnchorFailure(err)
			log.Errorf("Could not read admission anchors for meeting recovery: %v", err)
		} else if recovered, err := app.meetings.recoverAnchoredMeetings(starts); err != nil {
			log.Errorf("Could not recover anchored meeting records: %v", err)
		} else if app.memory != nil {
			for _, record := range recovered {
				if record.EndedAt == "" {
					app.memory.resumeMeetingIDIfEmpty(meetingRoomID(record), record.ID)
				}
			}
		}
	}
	roomIDs := map[string]struct{}{officeRoomID: {}}
	for _, roomID := range app.meetings.openRoomIDs() {
		roomIDs[roomID] = struct{}{}
	}
	if app.memory != nil {
		for _, roomID := range app.memory.meetingRoomIDs() {
			roomIDs[roomID] = struct{}{}
		}
	}
	for roomID := range roomIDs {
		app.reconcileMeetingRecordsAtBootForRoom(roomID)
	}
}

func (app *kanbanBoardApp) reconcileMeetingRecordsAtBootForRoom(roomID string) {
	roomID = normalizeRoomID(roomID)
	if roomID != officeRoomID {
		if room, found := appRoomStore().byID(roomID); found && room.Archived {
			// The archived flag is the durable cross-store close intent. A crash
			// after rooms.json committed but before meetings.json closed resumes
			// this fenced chain instead of granting a new five-minute idle window.
			go app.closeRoomForArchive(roomID)
			return
		}
	}
	record, ok := app.meetings.activeRecord(roomID)
	if !ok {
		if resumed := app.memory.currentMeetingID(roomID); resumed != "" && app.meetings.hasEndedRecord(resumed) {
			app.memory.rotateMeetingID(roomID)
		}
		return
	}
	if record.ID != app.memory.currentMeetingID(roomID) {
		source := app.meetingFinalizationSource(record.ID)
		if _, _, err := app.meetings.endMeetingWithFinalization(record.ID, time.Now().UTC(), meetingEndedReasonRestart, "", source); err != nil {
			log.Errorf("Could not close stale meeting %s with a durable finalization receipt: %v", record.ID, err)
		}
		return
	}
	if _, _, err := app.meetings.armIdleEndDurable(roomID, record.ID, time.Now().UTC(), func(generation uint64) { app.endMeetingForIdle(roomID, generation) }); err != nil {
		log.Errorf("Could not restore empty-room deadline for meeting %s: %v", record.ID, err)
	}
}

func (app *kanbanBoardApp) broadcastMeetingRecord(record meetingRecord) {
	payload := app.meetingRecordPayload(record, time.Now().UTC())
	broadcastRoomAudienceKanbanEvent(record.RoomID, "meeting", payload)
}

// meetingSnapshot returns the room's active record payload for direct sends /
// HTTP, or nil when no meeting is active (the client clears its state on null).
func (app *kanbanBoardApp) meetingSnapshot(roomID string) map[string]any {
	if app == nil || app.meetings == nil {
		return nil
	}
	record, ok := app.meetings.activeRecord(roomID)
	if !ok {
		return nil
	}
	return app.meetingRecordPayload(record, time.Now().UTC())
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
	ClaimCardIDs   map[string][]string
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
	return meetingMemoryDetailsFromStore(app.memory, wanted)
}

func (app *kanbanBoardApp) meetingMemoryDetailsForPrincipal(ctx context.Context, principal RecallPrincipal, wanted map[string]struct{}) map[string]*meetingMemoryDetail {
	if app == nil {
		return map[string]*meetingMemoryDetail{}
	}
	return meetingMemoryDetailsFromStore(app.recallStoreForPrincipal(ctx, principal), wanted)
}

func meetingMemoryDetailsFromStore(store *meetingMemoryStore, wanted map[string]struct{}) map[string]*meetingMemoryDetail {
	details := map[string]*meetingMemoryDetail{}
	if store == nil || len(wanted) == 0 {
		return details
	}
	for _, entry := range store.snapshot(0) {
		meetingID := strings.TrimSpace(entry.Metadata["meetingId"])
		if meetingID == "" {
			continue
		}
		if _, ok := wanted[meetingID]; !ok {
			continue
		}
		detail := details[meetingID]
		if detail == nil {
			detail = &meetingMemoryDetail{ClaimCardIDs: map[string][]string{}}
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
		if isMeetingDigestKind(entry.Kind) {
			// digest rollups are recall material (strict JSON), not meeting
			// log rows; the card summary keeps coming from the freshest brain
			// until a later wave prefers the digest deliberately.
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
			detail.addClaimCardLinks(entry.Metadata["claimCardLinks"])
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

func (detail *meetingMemoryDetail) addClaimCardLinks(encoded string) {
	if detail == nil || strings.TrimSpace(encoded) == "" {
		return
	}
	links := []meetingBoardClaimCardLink{}
	if json.Unmarshal([]byte(encoded), &links) != nil {
		return
	}
	if detail.ClaimCardIDs == nil {
		detail.ClaimCardIDs = map[string][]string{}
	}
	for _, link := range links {
		segmentID, cardID := strings.TrimSpace(link.SegmentID), strings.TrimSpace(link.CardID)
		if segmentID == "" || cardID == "" {
			continue
		}
		found := false
		for _, existing := range detail.ClaimCardIDs[segmentID] {
			found = found || existing == cardID
		}
		if !found {
			detail.ClaimCardIDs[segmentID] = append(detail.ClaimCardIDs[segmentID], cardID)
		}
	}
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

// assistantMeetingsHandler serves the permanent Meeting Record collection and
// exact detail route to a signed-in principal. The directory is never a grant:
// rows are joined only after the requester's principal-filtered recall store
// proves that the sitting still has readable source material.
func assistantMeetingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
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
	principal := recallPrincipalForUser(user)
	projectionLimit := limit
	detailID := ""
	conversationRequest := false
	if strings.HasPrefix(r.URL.Path, "/assistant/meetings/") {
		suffix := strings.Trim(strings.TrimPrefix(r.URL.Path, "/assistant/meetings/"), "/")
		parts := strings.Split(suffix, "/")
		detailID = strings.TrimSpace(parts[0])
		conversationRequest = len(parts) == 2 && parts[1] == "conversation"
		if detailID == "" || len(parts) > 2 || (len(parts) == 2 && !conversationRequest) {
			writeAuthError(w, http.StatusNotFound, "meeting record is unavailable")
			return
		}
		projectionLimit = 1
	}
	if r.Method == http.MethodPost && !conversationRequest {
		writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	indexOnly := detailID == "" && strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("view")), "index")
	projections, _, nextMeetingCursor, hasMoreMeetings := kanbanApp.meetingRecordPageProjectionsForPrincipal(r.Context(), principal, projectionLimit, detailID, r.URL.Query().Get("meetingCursor"), !indexOnly)
	cardTitles := map[string]string{}
	for _, card := range kanbanApp.snapshotState().Cards {
		cardTitles[card.ID] = card.Title
	}
	if detailID != "" {
		for _, projection := range projections {
			if projection.index.ID != detailID {
				continue
			}
			if conversationRequest {
				if r.Method != http.MethodPost {
					writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
					return
				}
				payload := struct {
					RecordRevision string `json:"recordRevision"`
				}{}
				decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
				decoder.DisallowUnknownFields()
				if r.Body == nil || decoder.Decode(&payload) != nil || ensureJSONEOF(decoder) != nil {
					writeAuthError(w, http.StatusBadRequest, "could not read Meeting Record conversation request")
					return
				}
				if strings.TrimSpace(payload.RecordRevision) == "" || payload.RecordRevision != projection.index.RecordRevision {
					writeAuthError(w, http.StatusConflict, "Meeting Record revision changed")
					return
				}
				thread, created, createErr := kanbanApp.ensureMeetingRecordConversation(user, projection)
				if createErr != nil {
					writeAuthError(w, http.StatusConflict, createErr.Error())
					return
				}
				if created {
					deliverScoutChatThreadMetadata(thread)
				}
				writeAuthJSON(w, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created], map[string]any{
					"ok": true, "created": created, "thread": kanbanApp.projectScoutChatThreadForViewer(user.Email, thread, r.Context()),
				})
				return
			}
			if r.Method != http.MethodGet {
				writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			writeAuthJSON(w, http.StatusOK, map[string]any{
				"ok":        true,
				"meeting":   projection.detail(kanbanApp.meetingRecordReferencesForViewer(r.Context(), user, projection.legacyDetail), r.URL.Query().Get("cursor"), r.URL.Query().Get("q"), r.URL.Query().Get("segmentId"), parseMeetingRecordTranscriptLimit(r.URL.Query().Get("transcriptLimit"))),
				"serverNow": now.Format(time.RFC3339Nano),
			})
			return
		}
		// Missing and unauthorized are deliberately indistinguishable.
		writeAuthError(w, http.StatusNotFound, "meeting record is unavailable")
		return
	}
	if indexOnly {
		items := make([]meetingRecordIndexItem, 0, len(projections))
		for _, projection := range projections {
			items = append(items, projection.index)
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok":         true,
			"contract":   meetingRecordContractVersion,
			"meetings":   items,
			"nextCursor": nextMeetingCursor,
			"hasMore":    hasMoreMeetings,
			"serverNow":  now.Format(time.RFC3339Nano),
		})
		return
	}

	// Compatibility shape for already-released clients. It is now subject to
	// the same current-source authorization as the closed index/detail routes.
	meetings := make([]map[string]any, 0, len(projections))
	for _, projection := range projections {
		item := kanbanApp.meetingRecordPayload(projection.record, now)
		// one top-level anchor instead of a per-item serverNow.
		delete(item, "serverNow")
		// Memory-tool enrichment (D15): summary, decided checklist, log
		// rows, and board-card links per meeting.
		for key, value := range meetingDetailFields(projection.legacyDetail, cardTitles) {
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
