package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Per-thread read state — design §15.5 of docs/plans/the-table-design.md.
//
// Nothing in this system tracked what a user had read. The canvas live line
// derived "unread" from the NOTIFICATIONS store, which is a different thing
// entirely: notifications are created per mention and per event, not per
// message. The Table's unread boundary, its counts, and the Deck's per-channel
// dots all need a real marker, so this is it.

// threadReadMarker is one row per (tenant, user, thread). It stores the moment
// the user last read AND the message id they read through: the timestamp drives
// the count (immune to deletion) and the id anchors the client's "new messages"
// divider.
type threadReadMarker struct {
	TenantID          string `json:"tenantId"`
	UserEmail         string `json:"userEmail"`
	ThreadID          string `json:"threadId"`
	LastReadMessageID string `json:"lastReadMessageId,omitempty"`
	ReadAt            string `json:"readAt"`
}

type threadReadStoreData struct {
	Markers   []threadReadMarker `json:"markers"`
	UpdatedAt string             `json:"updatedAt,omitempty"`
}

var threadReadStoreMu sync.Mutex

func threadReadMarkersPath() string {
	if path := strings.TrimSpace(os.Getenv("THREAD_READ_MARKERS_PATH")); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "thread-read-markers.json")
}

// loadThreadReadStoreFile reads the store off disk. A missing/empty/corrupt file
// is a clean empty store — read state is best-effort and must never wedge the
// thread list, which calls into this on every load.
// Callers hold threadReadStoreMu (or accept a point-in-time snapshot).
func loadThreadReadStoreFile() threadReadStoreData {
	raw, err := os.ReadFile(threadReadMarkersPath())
	if err != nil {
		return threadReadStoreData{}
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return threadReadStoreData{}
	}
	var state threadReadStoreData
	if err := json.Unmarshal(raw, &state); err != nil {
		log.Errorf("Failed to decode thread read markers: %v", err)
		return threadReadStoreData{}
	}
	for index := range state.Markers {
		state.Markers[index].TenantID = strings.TrimSpace(state.Markers[index].TenantID)
		state.Markers[index].UserEmail = normalizeAccountEmail(state.Markers[index].UserEmail)
		state.Markers[index].ThreadID = strings.TrimSpace(state.Markers[index].ThreadID)
	}
	return state
}

// mutateThreadReadStore is the read-modify-write seam: load fresh, apply fn,
// persist atomically — all under the lock so concurrent reads from two devices
// don't clobber each other.
func mutateThreadReadStore(fn func(*threadReadStoreData)) error {
	threadReadStoreMu.Lock()
	defer threadReadStoreMu.Unlock()
	state := loadThreadReadStoreFile()
	fn(&state)
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return writeJSONFileAtomically(threadReadMarkersPath(), "thread read markers", state)
}

// snapshotThreadReadStore returns a point-in-time copy for read paths.
func snapshotThreadReadStore() threadReadStoreData {
	threadReadStoreMu.Lock()
	defer threadReadStoreMu.Unlock()
	return loadThreadReadStoreFile()
}

func sameReadMarkerKey(a, b threadReadMarker) bool {
	return strings.EqualFold(strings.TrimSpace(a.TenantID), strings.TrimSpace(b.TenantID)) &&
		normalizeAccountEmail(a.UserEmail) == normalizeAccountEmail(b.UserEmail) &&
		strings.TrimSpace(a.ThreadID) == strings.TrimSpace(b.ThreadID)
}

// readMarkerAdvances reports whether `next` is strictly newer than `current`.
// An unparseable current marker is treated as "anything is newer" so a corrupt
// row heals on the next read rather than pinning the thread unread forever; an
// unparseable next marker is refused so garbage cannot overwrite good state.
func readMarkerAdvances(current, next string) bool {
	nextAt, err := time.Parse(time.RFC3339, strings.TrimSpace(next))
	if err != nil {
		return false
	}
	currentAt, err := time.Parse(time.RFC3339, strings.TrimSpace(current))
	if err != nil {
		return true
	}
	return nextAt.After(currentAt)
}

// upsertThreadReadMarker writes a marker, advancing only.
//
// Markers never move backwards. A retry, or an out-of-order request from a
// device that was offline, must not resurrect messages the user has already
// read — that failure looks like the app losing track of you, which is exactly
// the trust the Table has to earn.
func upsertThreadReadMarker(marker threadReadMarker) error {
	marker.TenantID = strings.TrimSpace(marker.TenantID)
	marker.UserEmail = normalizeAccountEmail(marker.UserEmail)
	marker.ThreadID = strings.TrimSpace(marker.ThreadID)
	if marker.ThreadID == "" || marker.UserEmail == "" {
		return nil
	}

	return mutateThreadReadStore(func(state *threadReadStoreData) {
		for index, existing := range state.Markers {
			if !sameReadMarkerKey(existing, marker) {
				continue
			}
			if !readMarkerAdvances(existing.ReadAt, marker.ReadAt) {
				return
			}
			state.Markers[index] = marker
			return
		}
		state.Markers = append(state.Markers, marker)
	})
}

// lookupThreadReadMarker returns the marker for one viewer and thread, or the
// zero marker when they have never read it.
func lookupThreadReadMarker(tenantID, userEmail, threadID string) threadReadMarker {
	want := threadReadMarker{TenantID: tenantID, UserEmail: userEmail, ThreadID: threadID}
	for _, marker := range snapshotThreadReadStore().Markers {
		if sameReadMarkerKey(marker, want) {
			return marker
		}
	}
	return threadReadMarker{}
}

// scoutChatThreadsView renders the thread list for one viewer.
//
// unreadCount is PER-VIEWER and must never become a field on
// scoutChatThreadRecord: that struct is what gets persisted, so a field there
// would write one user's read state onto a record every other user shares.
// Rendering through a map keeps viewer state in the response only.
//
// The round-trip through JSON is deliberate — it means this view cannot drift
// out of sync with the record's own field set or its omitempty rules when a
// future field is added.
func (app *kanbanBoardApp) scoutChatThreadsView(viewerEmail string, includeArchived bool, limit int) []map[string]any {
	threads := app.scoutChatThreadsSnapshot(viewerEmail, includeArchived, limit)
	if len(threads) == 0 {
		return []map[string]any{}
	}

	// One store read for the whole list. Calling lookupThreadReadMarker per
	// thread would re-read and re-decode the file once per row.
	markers := snapshotThreadReadStore().Markers
	markerFor := func(threadID string) threadReadMarker {
		want := threadReadMarker{UserEmail: viewerEmail, ThreadID: threadID}
		for _, marker := range markers {
			if sameReadMarkerKey(marker, want) {
				return marker
			}
		}
		return threadReadMarker{}
	}

	view := make([]map[string]any, 0, len(threads))
	for _, thread := range threads {
		thread = app.projectScoutChatThreadForViewer(viewerEmail, thread)
		encoded, err := json.Marshal(thread)
		if err != nil {
			log.Errorf("Failed to encode thread %s for view: %v", thread.ID, err)
			continue
		}
		row := map[string]any{}
		if err := json.Unmarshal(encoded, &row); err != nil {
			log.Errorf("Failed to decode thread %s for view: %v", thread.ID, err)
			continue
		}
		marker := markerFor(thread.ID)
		row["unreadCount"] = threadUnreadCount(thread.Messages, marker.ReadAt, viewerEmail)
		// The client anchors its "new messages" divider on this.
		row["lastReadMessageId"] = marker.LastReadMessageID
		view = append(view, row)
	}
	return view
}

// assistantThreadReadHandler advances the caller's read marker for one thread.
//
// The route is flat with the thread id in the body, matching
// /assistant/threads/follow-up, because this server registers plain
// http.HandleFunc paths and has no path-parameter router.
func assistantThreadReadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
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
		writeAuthError(w, http.StatusServiceUnavailable, "chat threads are unavailable")
		return
	}

	payload := struct {
		ThreadID          string `json:"threadId"`
		LastReadMessageID string `json:"lastReadMessageId"`
	}{}
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload)
	}
	threadID := strings.TrimSpace(payload.ThreadID)
	if threadID == "" {
		writeAuthError(w, http.StatusBadRequest, "threadId is required")
		return
	}

	// Authorize before writing: reading a thread you cannot see must not
	// silently create a marker for it. The marker is anchored to the exact
	// server-authored message timestamp, not request arrival time; otherwise a
	// concurrent message committed between render and this POST is erased from
	// unread state even though the viewer never saw it.
	thread, _, err := kanbanApp.scoutChatThreadByID(user.Email, threadID)
	if err != nil {
		writeAuthError(w, http.StatusNotFound, "chat thread not found")
		return
	}
	lastReadMessageID := strings.TrimSpace(payload.LastReadMessageID)
	readAt := ""
	readMessageIDs := map[string]struct{}{}
	for _, message := range thread.Messages {
		if message.ID != "" {
			readMessageIDs[message.ID] = struct{}{}
		}
		if message.ID == lastReadMessageID {
			readAt = strings.TrimSpace(message.CreatedAt)
			break
		}
	}
	if lastReadMessageID == "" || readAt == "" {
		writeAuthError(w, http.StatusBadRequest, "lastReadMessageId is not in this thread")
		return
	}

	// The clock is ours, never the client's. A device with a fast clock could
	// otherwise plant a far-future marker and permanently mark the thread read
	// — and markers only advance, so that would be unrecoverable.
	marker := threadReadMarker{
		UserEmail:         user.Email,
		ThreadID:          threadID,
		LastReadMessageID: lastReadMessageID,
		ReadAt:            readAt,
	}
	if err := upsertThreadReadMarker(marker); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not save read state")
		return
	}
	if _, err := kanbanApp.markThreadNotificationsRead(user.Email, threadID, readMessageIDs); err != nil {
		log.Errorf("Failed to settle thread notifications for %s: %v", user.Email, err)
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "readAt": marker.ReadAt})
}

// threadUnreadCount counts messages newer than the marker, excluding the
// viewer's own.
//
// Counting from a TIMESTAMP rather than storing a count is load-bearing:
// scout_chat_delete.go removes messages, and a stored count drifts the moment
// one goes. Scanning forward from a marked message id has the same problem in
// reverse — deleting the message the user last read would strand the scan and
// report the entire thread unread.
//
// The two error directions are deliberately asymmetric, because their costs
// are: an unparseable MESSAGE timestamp counts as read (a message we cannot
// place in time cannot honestly be called new, and guessing "new" pins a
// permanent badge on a thread you have already seen), while an unparseable
// MARKER counts as never-read (hiding messages is the unrecoverable direction).
func threadUnreadCount(messages []scoutChatMessageRecord, readAt string, viewerEmail string) int {
	viewer := normalizeAccountEmail(viewerEmail)

	var since time.Time
	if trimmed := strings.TrimSpace(readAt); trimmed != "" {
		if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
			since = parsed
		}
	}

	count := 0
	for _, message := range messages {
		if viewer != "" && normalizeAccountEmail(message.AuthorEmail) == viewer {
			continue
		}
		created, err := time.Parse(time.RFC3339, strings.TrimSpace(message.CreatedAt))
		if err != nil {
			continue
		}
		if created.After(since) {
			count++
		}
	}
	return count
}
