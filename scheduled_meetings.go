package main

// Wave 7 D1 — scheduled meetings. A scheduledMeeting is a durable, room-bound
// appointment (start, duration, attendees) kept in a mutex-guarded JSON
// side-store next to rooms.json (the file_folders.go / rooms.go idiom:
// tmp+rename persistence, cache keyed by path so tests isolate by env).
// Founder decision #5: scheduling ships as scheduled rooms, never Google
// OAuth — the calendar seam stays the stateless .ics download, extended here
// with TIMED VEVENTs that carry the room's join URL (calendar.go).
//
// ACL: every signed-in member may list and create (the lobby is a member
// surface); only the organizer or the room's manager may edit or cancel.
// Attendees must be registered, non-disabled accounts; validation answers one
// generic error so the route never confirms which addresses exist.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	scheduledMeetingTitleMaxRunes          = 120
	scheduledMeetingMinDurationMinutes     = 5
	scheduledMeetingMaxDurationMinutes     = 12 * 60
	scheduledMeetingDefaultDurationMinutes = 30
	scheduledMeetingMaxAttendees           = 50
	scheduledMeetingStoreLimit             = 2000
	scheduledMeetingUpcomingWindow         = 14 * 24 * time.Hour
	scheduledMeetingRequestBodyLimit       = 16 << 10
	scheduledMeetingCancelledRetention     = 90 * 24 * time.Hour
	scheduledMeetingListLimit              = 200
)

var (
	errScheduledMeetingNotFound  = errors.New("scheduled meeting not found")
	errScheduledMeetingCancelled = errors.New("scheduled meeting is cancelled")
	errScheduledMeetingForbidden = errors.New("only the organizer or a room manager can change this meeting")
	errScheduledMeetingLimit     = errors.New("scheduled meeting limit reached")
	errScheduledMeetingAttendees = errors.New("attendees must be registered accounts")
	errScheduledMeetingRoom      = errors.New("room not found")
	errScheduledMeetingTitle     = errors.New("title must be 1-120 characters")
	errScheduledMeetingStart     = errors.New("startsAt must be an RFC 3339 timestamp")
	errScheduledMeetingDuration  = errors.New("durationMinutes must be between 5 and 720")
	errScheduledMeetingRoomMove  = errors.New("a scheduled meeting cannot move rooms; cancel and create it again")
)

// scheduledMeetingRecord is the stored shape; the wire shape adds derived
// fields (endsAt, joinPath, roomName) in scheduledMeetingPayload.
type scheduledMeetingRecord struct {
	ID              string   `json:"id"`
	RoomID          string   `json:"roomId"`
	Title           string   `json:"title"`
	StartsAt        string   `json:"startsAt"` // RFC 3339, UTC
	DurationMinutes int      `json:"durationMinutes"`
	Attendees       []string `json:"attendees"` // normalized account emails, organizer first
	CreatedBy       string   `json:"createdBy"`
	CreatedAt       string   `json:"createdAt"`
	UpdatedAt       string   `json:"updatedAt"`
	CancelledAt     string   `json:"cancelledAt,omitempty"`
	// Revision counts durable edits (0 at creation, +1 per update including
	// the cancel) and is the record's ICS SEQUENCE, so calendars re-import an
	// edited or cancelled appointment instead of ignoring it.
	Revision int `json:"revision"`
}

type scheduledMeetingStoreState struct {
	Meetings []scheduledMeetingRecord `json:"meetings"`
}

type scheduledMeetingStore struct {
	mu       sync.Mutex
	path     string
	meetings []scheduledMeetingRecord
	loadErr  error
}

func scheduledMeetingsPath() string {
	if path := strings.TrimSpace(os.Getenv("SCHEDULED_MEETINGS_PATH")); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(meetingsPath()), "scheduled-meetings.json")
}

var (
	scheduledMeetingStoreMu    sync.Mutex
	scheduledMeetingStoreCache = map[string]*scheduledMeetingStore{}
)

func appScheduledMeetingStore() *scheduledMeetingStore {
	path := scheduledMeetingsPath()
	scheduledMeetingStoreMu.Lock()
	defer scheduledMeetingStoreMu.Unlock()
	if store, ok := scheduledMeetingStoreCache[path]; ok {
		return store
	}
	store := newScheduledMeetingStore(path)
	scheduledMeetingStoreCache[path] = store
	return store
}

func newScheduledMeetingStore(path string) *scheduledMeetingStore {
	store := &scheduledMeetingStore{path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			store.loadErr = fmt.Errorf("scheduled meeting store is unavailable")
		}
		return store
	}
	var state scheduledMeetingStoreState
	if err := json.Unmarshal(raw, &state); err != nil {
		store.loadErr = fmt.Errorf("scheduled meeting store is malformed")
		return store
	}
	store.meetings = state.Meetings
	return store
}

func (s *scheduledMeetingStore) persistLocked() error {
	if s.loadErr != nil {
		return s.loadErr
	}
	meetings := s.meetings
	if meetings == nil {
		meetings = []scheduledMeetingRecord{}
	}
	return writeJSONFileAtomically(s.path, "scheduled meeting store", scheduledMeetingStoreState{Meetings: meetings})
}

func cloneScheduledMeeting(record scheduledMeetingRecord) scheduledMeetingRecord {
	record.Attendees = append([]string(nil), record.Attendees...)
	return record
}

func newScheduledMeetingID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "sched-" + hex.EncodeToString(raw), nil
}

func scheduledMeetingStart(record scheduledMeetingRecord) (time.Time, bool) {
	start, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(record.StartsAt))
	if err != nil {
		return time.Time{}, false
	}
	return start.UTC(), true
}

func scheduledMeetingEnd(record scheduledMeetingRecord) (time.Time, bool) {
	start, ok := scheduledMeetingStart(record)
	if !ok {
		return time.Time{}, false
	}
	minutes := record.DurationMinutes
	if minutes <= 0 {
		minutes = scheduledMeetingDefaultDurationMinutes
	}
	return start.Add(time.Duration(minutes) * time.Minute), true
}

func sortScheduledMeetings(records []scheduledMeetingRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].StartsAt != records[j].StartsAt {
			return records[i].StartsAt < records[j].StartsAt
		}
		return records[i].ID < records[j].ID
	})
}

// list returns non-cancelled meetings (optionally one room's), sorted by start.
// upcoming narrows to meetings that have not ended yet and start within the
// 14-day window from now — the lobby rail's exact contract.
func (s *scheduledMeetingStore) list(roomID string, now time.Time, upcoming bool, includeCancelled bool) []scheduledMeetingRecord {
	if s == nil {
		return []scheduledMeetingRecord{}
	}
	roomID = strings.TrimSpace(roomID)
	horizon := now.Add(scheduledMeetingUpcomingWindow)
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]scheduledMeetingRecord, 0, len(s.meetings))
	for _, record := range s.meetings {
		if roomID != "" && normalizeRoomID(record.RoomID) != normalizeRoomID(roomID) {
			continue
		}
		if record.CancelledAt != "" && (upcoming || !includeCancelled) {
			continue
		}
		if upcoming {
			start, ok := scheduledMeetingStart(record)
			end, endOK := scheduledMeetingEnd(record)
			if !ok || !endOK || end.Before(now) || start.After(horizon) {
				continue
			}
		}
		result = append(result, cloneScheduledMeeting(record))
	}
	sortScheduledMeetings(result)
	if len(result) > scheduledMeetingListLimit {
		result = result[:scheduledMeetingListLimit]
	}
	return result
}

func (s *scheduledMeetingStore) byID(id string) (scheduledMeetingRecord, bool) {
	if s == nil {
		return scheduledMeetingRecord{}, false
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.meetings {
		if record.ID == id {
			return cloneScheduledMeeting(record), true
		}
	}
	return scheduledMeetingRecord{}, false
}

// pruneLocked drops cancelled meetings past the retention window so the file
// stays bounded without ever deleting a live appointment.
func (s *scheduledMeetingStore) pruneLocked(now time.Time) {
	kept := s.meetings[:0]
	for _, record := range s.meetings {
		if record.CancelledAt != "" {
			if cancelled, err := time.Parse(time.RFC3339Nano, record.CancelledAt); err == nil && now.Sub(cancelled) > scheduledMeetingCancelledRetention {
				continue
			}
		}
		kept = append(kept, record)
	}
	s.meetings = kept
}

func (s *scheduledMeetingStore) create(record scheduledMeetingRecord, now time.Time) (scheduledMeetingRecord, error) {
	if s.loadErr != nil {
		return scheduledMeetingRecord{}, s.loadErr
	}
	id, err := newScheduledMeetingID()
	if err != nil {
		return scheduledMeetingRecord{}, err
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	record.ID = id
	record.CreatedAt = stamp
	record.UpdatedAt = stamp
	record.CancelledAt = ""
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	if len(s.meetings) >= scheduledMeetingStoreLimit {
		return scheduledMeetingRecord{}, errScheduledMeetingLimit
	}
	prior := s.meetings
	s.meetings = append(append([]scheduledMeetingRecord(nil), s.meetings...), cloneScheduledMeeting(record))
	if err := s.persistLocked(); err != nil {
		s.meetings = prior
		return scheduledMeetingRecord{}, err
	}
	return cloneScheduledMeeting(record), nil
}

// update applies mutate to one live record under the store lock and persists
// atomically; a persist failure rolls the in-memory record back.
func (s *scheduledMeetingStore) update(id string, now time.Time, mutate func(*scheduledMeetingRecord) error) (scheduledMeetingRecord, error) {
	if s.loadErr != nil {
		return scheduledMeetingRecord{}, s.loadErr
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.meetings {
		if s.meetings[index].ID != id {
			continue
		}
		prior := cloneScheduledMeeting(s.meetings[index])
		working := cloneScheduledMeeting(s.meetings[index])
		if err := mutate(&working); err != nil {
			return prior, err
		}
		working.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
		working.Revision = prior.Revision + 1
		s.meetings[index] = working
		if err := s.persistLocked(); err != nil {
			s.meetings[index] = prior
			return scheduledMeetingRecord{}, err
		}
		return cloneScheduledMeeting(working), nil
	}
	return scheduledMeetingRecord{}, errScheduledMeetingNotFound
}

/* ---------- validation ---------- */

func normalizeScheduledMeetingTitle(raw string) (string, error) {
	title := strings.Join(strings.Fields(raw), " ")
	if title == "" || utf8.RuneCountInString(title) > scheduledMeetingTitleMaxRunes {
		return "", errScheduledMeetingTitle
	}
	return title, nil
}

func parseScheduledMeetingStart(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, errScheduledMeetingStart
	}
	start, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		start, err = time.Parse(time.RFC3339, raw)
	}
	if err != nil {
		return time.Time{}, errScheduledMeetingStart
	}
	return start.UTC(), nil
}

func normalizeScheduledMeetingDuration(minutes int) (int, error) {
	if minutes == 0 {
		return scheduledMeetingDefaultDurationMinutes, nil
	}
	if minutes < scheduledMeetingMinDurationMinutes || minutes > scheduledMeetingMaxDurationMinutes {
		return 0, errScheduledMeetingDuration
	}
	return minutes, nil
}

// validateScheduledMeetingAttendees canonicalizes the attendee list: the
// organizer always leads, duplicates collapse, and every address must resolve
// to a registered non-disabled account. One generic error covers every
// failure so the route never enumerates the roster.
func validateScheduledMeetingAttendees(organizer string, raw []string) ([]string, error) {
	organizer = normalizeAccountEmail(organizer)
	if organizer == "" {
		return nil, errScheduledMeetingAttendees
	}
	attendees := []string{organizer}
	seen := map[string]struct{}{organizer: {}}
	for _, value := range raw {
		email := normalizeAccountEmail(value)
		if email == "" {
			return nil, errScheduledMeetingAttendees
		}
		if _, dup := seen[email]; dup {
			continue
		}
		if len(attendees) >= scheduledMeetingMaxAttendees {
			return nil, errScheduledMeetingAttendees
		}
		if accountStore().findUser(email) == nil || accountIsDisabled(email) {
			return nil, errScheduledMeetingAttendees
		}
		seen[email] = struct{}{}
		attendees = append(attendees, email)
	}
	return attendees, nil
}

func scheduledMeetingRoom(roomID string) (roomRecord, error) {
	room, ok := appRoomStore().byID(normalizeRoomID(roomID))
	if !ok {
		return roomRecord{}, errScheduledMeetingRoom
	}
	if room.Archived {
		return roomRecord{}, errRoomArchived
	}
	return room, nil
}

// scheduledMeetingManagedBy is the edit/cancel ACL: organizer or room manager.
func scheduledMeetingManagedBy(record scheduledMeetingRecord, user *userAccount) bool {
	if user == nil {
		return false
	}
	if creator := normalizeAccountEmail(record.CreatedBy); creator != "" && creator == normalizeAccountEmail(user.Email) {
		return true
	}
	return roomManagedByUser(normalizeRoomID(record.RoomID), user)
}

/* ---------- wire shape ---------- */

func scheduledMeetingJoinPath(roomID string) string {
	return "/?room=" + url.QueryEscape(normalizeRoomID(roomID))
}

// scheduledMeetingJoinURL prefers the configured public origin so a calendar
// entry opened on another device still lands on the room; without one the
// relative boot-param form is emitted.
func scheduledMeetingJoinURL(r *http.Request, roomID string) string {
	path := scheduledMeetingJoinPath(roomID)
	if r != nil {
		if base, err := publicBaseURL(r); err == nil && base != "" {
			return base + path
		}
	}
	return path
}

func scheduledMeetingPayload(record scheduledMeetingRecord, roomName string, now time.Time) map[string]any {
	attendees := record.Attendees
	if attendees == nil {
		attendees = []string{}
	}
	payload := map[string]any{
		"id":              record.ID,
		"roomId":          normalizeRoomID(record.RoomID),
		"roomName":        roomName,
		"title":           record.Title,
		"startsAt":        record.StartsAt,
		"durationMinutes": record.DurationMinutes,
		"attendees":       attendees,
		"createdBy":       record.CreatedBy,
		"createdAt":       record.CreatedAt,
		"updatedAt":       record.UpdatedAt,
		"revision":        record.Revision,
		"cancelled":       record.CancelledAt != "",
		"joinPath":        scheduledMeetingJoinPath(record.RoomID),
		"icsPath":         "/calendar/meetings.ics?id=" + url.QueryEscape(record.ID),
	}
	if record.CancelledAt != "" {
		payload["cancelledAt"] = record.CancelledAt
	}
	if end, ok := scheduledMeetingEnd(record); ok {
		payload["endsAt"] = end.Format(time.RFC3339)
		if start, ok := scheduledMeetingStart(record); ok {
			payload["inProgress"] = !now.Before(start) && now.Before(end) && record.CancelledAt == ""
		}
	}
	return payload
}

func scheduledMeetingRoomName(roomID string) string {
	if store := appRoomStoreIfOpen(); store != nil {
		if room, ok := store.byID(normalizeRoomID(roomID)); ok {
			return room.Name
		}
	}
	return ""
}

/* ---------- HTTP ---------- */

type scheduledMeetingRequest struct {
	RoomID          *string   `json:"roomId"`
	Title           *string   `json:"title"`
	StartsAt        *string   `json:"startsAt"`
	DurationMinutes *int      `json:"durationMinutes"`
	Attendees       *[]string `json:"attendees"`
}

func decodeScheduledMeetingRequest(w http.ResponseWriter, r *http.Request) (scheduledMeetingRequest, error) {
	var request scheduledMeetingRequest
	if r.Body == nil {
		return request, errors.New("could not read request body")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, scheduledMeetingRequestBodyLimit))
	if err := decoder.Decode(&request); err != nil {
		return request, errors.New("could not read request body")
	}
	return request, nil
}

func writeScheduledMeetingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errScheduledMeetingNotFound), errors.Is(err, errScheduledMeetingRoom):
		writeAuthError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, errScheduledMeetingForbidden):
		writeAuthError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, errScheduledMeetingCancelled), errors.Is(err, errScheduledMeetingLimit):
		writeAuthError(w, http.StatusConflict, err.Error())
	case errors.Is(err, errScheduledMeetingAttendees), errors.Is(err, errScheduledMeetingTitle), errors.Is(err, errScheduledMeetingStart), errors.Is(err, errScheduledMeetingDuration), errors.Is(err, errScheduledMeetingRoomMove), errors.Is(err, errRoomArchived):
		writeAuthError(w, http.StatusBadRequest, err.Error())
	default:
		writeAuthError(w, http.StatusServiceUnavailable, "scheduled meetings are unavailable")
	}
}

// scheduledMeetingsHandler serves GET/POST /assistant/meetings/scheduled and
// GET/PATCH/DELETE /assistant/meetings/scheduled/{id}. Member session + origin
// gated like every /assistant route; DELETE is a soft cancel (cancelledAt) so
// the .ics UID stays stable and calendars can drop the event on re-import.
func scheduledMeetingsHandler(w http.ResponseWriter, r *http.Request) {
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
		writeAuthError(w, http.StatusServiceUnavailable, "scheduled meetings are unavailable")
		return
	}
	id := ""
	if strings.HasPrefix(r.URL.Path, "/assistant/meetings/scheduled/") {
		id = strings.Trim(strings.TrimPrefix(r.URL.Path, "/assistant/meetings/scheduled/"), "/")
		if id == "" || strings.Contains(id, "/") {
			writeAuthError(w, http.StatusNotFound, errScheduledMeetingNotFound.Error())
			return
		}
	}
	store := appScheduledMeetingStore()
	now := time.Now().UTC()

	switch {
	case id == "" && r.Method == http.MethodGet:
		query := r.URL.Query()
		upcoming := query.Get("upcoming") == "1" || strings.EqualFold(query.Get("upcoming"), "true")
		includeCancelled := query.Get("includeCancelled") == "1" || strings.EqualFold(query.Get("includeCancelled"), "true")
		records := store.list(query.Get("roomId"), now, upcoming, includeCancelled)
		meetings := make([]map[string]any, 0, len(records))
		for _, record := range records {
			meetings = append(meetings, scheduledMeetingPayload(record, scheduledMeetingRoomName(record.RoomID), now))
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "meetings": meetings, "upcoming": upcoming, "serverNow": now.Format(time.RFC3339Nano)})
	case id == "" && r.Method == http.MethodPost:
		request, err := decodeScheduledMeetingRequest(w, r)
		if err != nil {
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		record := scheduledMeetingRecord{CreatedBy: normalizeAccountEmail(user.Email)}
		roomID := officeRoomID
		if request.RoomID != nil {
			roomID = normalizeRoomID(*request.RoomID)
		}
		room, err := scheduledMeetingRoom(roomID)
		if err != nil {
			writeScheduledMeetingError(w, err)
			return
		}
		record.RoomID = room.ID
		title := ""
		if request.Title != nil {
			title = *request.Title
		}
		if record.Title, err = normalizeScheduledMeetingTitle(title); err != nil {
			writeScheduledMeetingError(w, err)
			return
		}
		startsAt := ""
		if request.StartsAt != nil {
			startsAt = *request.StartsAt
		}
		start, err := parseScheduledMeetingStart(startsAt)
		if err != nil {
			writeScheduledMeetingError(w, err)
			return
		}
		record.StartsAt = start.Format(time.RFC3339)
		minutes := 0
		if request.DurationMinutes != nil {
			minutes = *request.DurationMinutes
		}
		if record.DurationMinutes, err = normalizeScheduledMeetingDuration(minutes); err != nil {
			writeScheduledMeetingError(w, err)
			return
		}
		var attendees []string
		if request.Attendees != nil {
			attendees = *request.Attendees
		}
		if record.Attendees, err = validateScheduledMeetingAttendees(user.Email, attendees); err != nil {
			writeScheduledMeetingError(w, err)
			return
		}
		created, err := store.create(record, now)
		if err != nil {
			writeScheduledMeetingError(w, err)
			return
		}
		writeAuthJSON(w, http.StatusCreated, map[string]any{"ok": true, "meeting": scheduledMeetingPayload(created, room.Name, now)})
	case id != "" && r.Method == http.MethodGet:
		record, ok := store.byID(id)
		if !ok {
			writeAuthError(w, http.StatusNotFound, errScheduledMeetingNotFound.Error())
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "meeting": scheduledMeetingPayload(record, scheduledMeetingRoomName(record.RoomID), now)})
	case id != "" && r.Method == http.MethodPatch:
		request, err := decodeScheduledMeetingRequest(w, r)
		if err != nil {
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		existing, ok := store.byID(id)
		if !ok {
			writeAuthError(w, http.StatusNotFound, errScheduledMeetingNotFound.Error())
			return
		}
		if !scheduledMeetingManagedBy(existing, user) {
			writeAuthError(w, http.StatusForbidden, errScheduledMeetingForbidden.Error())
			return
		}
		updated, err := store.update(id, now, func(record *scheduledMeetingRecord) error {
			if record.CancelledAt != "" {
				return errScheduledMeetingCancelled
			}
			if request.RoomID != nil && normalizeRoomID(*request.RoomID) != normalizeRoomID(record.RoomID) {
				return errScheduledMeetingRoomMove
			}
			if request.Title != nil {
				title, err := normalizeScheduledMeetingTitle(*request.Title)
				if err != nil {
					return err
				}
				record.Title = title
			}
			if request.StartsAt != nil {
				start, err := parseScheduledMeetingStart(*request.StartsAt)
				if err != nil {
					return err
				}
				record.StartsAt = start.Format(time.RFC3339)
			}
			if request.DurationMinutes != nil {
				minutes, err := normalizeScheduledMeetingDuration(*request.DurationMinutes)
				if err != nil {
					return err
				}
				record.DurationMinutes = minutes
			}
			if request.Attendees != nil {
				attendees, err := validateScheduledMeetingAttendees(record.CreatedBy, *request.Attendees)
				if err != nil {
					return err
				}
				record.Attendees = attendees
			}
			return nil
		})
		if err != nil {
			writeScheduledMeetingError(w, err)
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "meeting": scheduledMeetingPayload(updated, scheduledMeetingRoomName(updated.RoomID), now)})
	case id != "" && r.Method == http.MethodDelete:
		existing, ok := store.byID(id)
		if !ok {
			writeAuthError(w, http.StatusNotFound, errScheduledMeetingNotFound.Error())
			return
		}
		if !scheduledMeetingManagedBy(existing, user) {
			writeAuthError(w, http.StatusForbidden, errScheduledMeetingForbidden.Error())
			return
		}
		cancelled, err := store.update(id, now, func(record *scheduledMeetingRecord) error {
			if record.CancelledAt == "" {
				record.CancelledAt = now.Format(time.RFC3339Nano)
			}
			return nil
		})
		if err != nil {
			writeScheduledMeetingError(w, err)
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "meeting": scheduledMeetingPayload(cancelled, scheduledMeetingRoomName(cancelled.RoomID), now)})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

/* ---------- ICS ---------- */

// scheduledMeetingICSEvent renders one appointment as a TIMED icsEvent: UTC
// DTSTART/DTEND, the room's join URL in DESCRIPTION/URL/LOCATION, one ATTENDEE
// per registered email, and a UID pinned to the record id so edits re-import
// as the same calendar entry.
func scheduledMeetingICSEvent(record scheduledMeetingRecord, roomName, joinURL string) (icsEvent, bool) {
	start, ok := scheduledMeetingStart(record)
	end, endOK := scheduledMeetingEnd(record)
	if !ok || !endOK {
		return icsEvent{}, false
	}
	lines := []string{"Join the room: " + joinURL}
	if roomName != "" {
		lines = append(lines, "Room: "+roomName)
	}
	lines = append(lines, "Scheduled in Bonfire (thebonfire.xyz).")
	organizer := normalizeAccountEmail(record.CreatedBy)
	organizerName := organizer
	if user := accountStore().findUser(organizer); user != nil {
		if name := accountDisplayName(user); name != "" {
			organizerName = name
		}
	}
	return icsEvent{
		Title:         record.Title,
		Start:         start,
		End:           end,
		Description:   strings.Join(lines, "\n"),
		Attendees:     append([]string(nil), record.Attendees...),
		UID:           "scheduled-" + record.ID + "@thebonfire.xyz",
		URL:           joinURL,
		Sequence:      record.Revision,
		Organizer:     organizer,
		OrganizerName: organizerName,
		Cancelled:     record.CancelledAt != "",
	}, true
}

// calendarMeetingsICSHandler serves GET /calendar/meetings.ics[?id=] — one
// scheduled meeting, or every upcoming one, as timed VEVENTs. Guarded exactly
// like calendarICSHandler (method, origin, signed-in member).
func calendarMeetingsICSHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if userFromRequest(r) == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	store := appScheduledMeetingStore()
	now := time.Now().UTC()
	var records []scheduledMeetingRecord
	filename := "bonfire-meetings.ics"
	method := icsMethodPublish
	if id := strings.TrimSpace(r.URL.Query().Get("id")); id != "" {
		record, ok := store.byID(id)
		if !ok {
			writeAuthError(w, http.StatusNotFound, errScheduledMeetingNotFound.Error())
			return
		}
		if record.CancelledAt != "" {
			// A cancelled meeting is served as an iTIP cancel (same UID,
			// STATUS:CANCELLED, bumped SEQUENCE) so a calendar that imported
			// the appointment retracts it instead of keeping a ghost entry.
			method = icsMethodCancel
		}
		records = []scheduledMeetingRecord{record}
		filename = calendarICSFilename("meeting", record.Title)
	} else {
		// The feed keeps excluding cancellations.
		records = store.list(r.URL.Query().Get("roomId"), now, true, false)
	}
	events := make([]icsEvent, 0, len(records))
	for _, record := range records {
		if event, ok := scheduledMeetingICSEvent(record, scheduledMeetingRoomName(record.RoomID), scheduledMeetingJoinURL(r, record.RoomID)); ok {
			events = append(events, event)
		}
	}
	body := buildICSCalendarWithMethod(events, now, method)
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", blobDownloadFilename(filename, "meetings.ics")))
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	if _, err := w.Write(body); err != nil {
		log.Errorf("Failed to serve scheduled meetings calendar: %v", err)
	}
}
