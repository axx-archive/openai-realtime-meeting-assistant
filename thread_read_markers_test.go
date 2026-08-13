package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadHandlerAnchorsToExactMessageInsteadOfRequestTime(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("THREAD_READ_MARKERS_PATH", filepath.Join(t.TempDir(), "markers.json"))
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	thread, err := kanbanApp.createScoutChatThread("tim@shareability.com", "Tim", "team", "public")
	if err != nil {
		t.Fatal(err)
	}
	messages := []scoutChatMessageRecord{
		{ID: "seen", Role: "user", Text: "first", AuthorEmail: "tim@shareability.com", CreatedAt: "2026-07-28T10:00:00Z"},
		{ID: "concurrent", Role: "user", Text: "second", AuthorEmail: "tim@shareability.com", CreatedAt: "2026-07-28T10:01:00Z"},
	}
	saved, err := kanbanApp.commitScoutChatThreadMessages("tim@shareability.com", thread.ID, messages...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kanbanApp.createChatNotification("aj@shareability.com", nil, "Tim mentioned you", saved, messages[0]); err != nil {
		t.Fatal(err)
	}
	if got := kanbanApp.unreadNotificationsFor("aj@shareability.com", notificationListLimit); len(got) != 1 {
		t.Fatalf("precondition mention notifications=%#v", got)
	}

	req := httptest.NewRequest(http.MethodPost, "/assistant/threads/read", strings.NewReader(`{"threadId":"`+thread.ID+`","lastReadMessageId":"seen"}`))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginAs(t, "aj@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	assistantThreadReadHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark read status=%d body=%s", rec.Code, rec.Body.String())
	}
	marker := lookupThreadReadMarker("", "aj@shareability.com", thread.ID)
	if marker.ReadAt != messages[0].CreatedAt || marker.LastReadMessageID != "seen" {
		t.Fatalf("marker=%+v, want exact seen message anchor", marker)
	}
	if got := threadUnreadCount(messages, marker.ReadAt, "aj@shareability.com"); got != 1 {
		t.Fatalf("concurrent unseen messages=%d, want 1", got)
	}
	if got := kanbanApp.unreadNotificationsFor("aj@shareability.com", notificationListLimit); len(got) != 0 {
		t.Fatalf("read thread left its mention dot lit: %#v", got)
	}
}

func readMarkerMessage(id, author, createdAt string) scoutChatMessageRecord {
	return scoutChatMessageRecord{ID: id, AuthorEmail: author, CreatedAt: createdAt}
}

func TestThreadUnreadCountExcludesOwnMessages(t *testing.T) {
	messages := []scoutChatMessageRecord{
		readMarkerMessage("1", "dana@x.com", "2026-07-28T10:00:00Z"),
		readMarkerMessage("2", "aj@x.com", "2026-07-28T10:01:00Z"),
		readMarkerMessage("3", "dana@x.com", "2026-07-28T10:02:00Z"),
	}
	// Read through 10:00:30. Message 2 is the viewer's own and never counts.
	if got := threadUnreadCount(messages, "2026-07-28T10:00:30Z", "aj@x.com"); got != 1 {
		t.Fatalf("unread = %d, want 1", got)
	}
}

func TestThreadUnreadCountWithNoMarkerCountsEverythingButOwn(t *testing.T) {
	messages := []scoutChatMessageRecord{
		readMarkerMessage("1", "dana@x.com", "2026-07-28T10:00:00Z"),
		readMarkerMessage("2", "aj@x.com", "2026-07-28T10:01:00Z"),
	}
	if got := threadUnreadCount(messages, "", "aj@x.com"); got != 1 {
		t.Fatalf("unread = %d, want 1", got)
	}
}

// The marker is a TIMESTAMP, so deleting the message the user last read cannot
// strand the count. Scanning for a marked message id would report the whole
// thread unread the moment that message was deleted, and this repo deletes
// messages (scout_chat_delete.go).
func TestThreadUnreadCountSurvivesDeletionOfTheMarkedMessage(t *testing.T) {
	messages := []scoutChatMessageRecord{
		// The 10:00 message the marker was set from has been deleted.
		readMarkerMessage("3", "dana@x.com", "2026-07-28T10:02:00Z"),
	}
	if got := threadUnreadCount(messages, "2026-07-28T10:00:30Z", "aj@x.com"); got != 1 {
		t.Fatalf("unread = %d, want 1", got)
	}
}

// A message we cannot place in time cannot honestly be called new. Guessing
// "new" would pin a permanent unread badge on an already-read thread.
func TestThreadUnreadCountIgnoresUnparseableMessageTimestamps(t *testing.T) {
	messages := []scoutChatMessageRecord{readMarkerMessage("1", "dana@x.com", "not-a-time")}
	if got := threadUnreadCount(messages, "2026-07-28T10:00:00Z", "aj@x.com"); got != 0 {
		t.Fatalf("unread = %d, want 0", got)
	}
}

// A corrupt marker must fail LOUD (everything unread), never silent (everything
// read) — hiding messages is the unrecoverable direction of this error.
func TestThreadUnreadCountTreatsACorruptMarkerAsNeverRead(t *testing.T) {
	messages := []scoutChatMessageRecord{
		readMarkerMessage("1", "dana@x.com", "2026-07-28T10:00:00Z"),
	}
	if got := threadUnreadCount(messages, "garbage", "aj@x.com"); got != 1 {
		t.Fatalf("unread = %d, want 1", got)
	}
}

func TestThreadUnreadCountMatchesEmailCaseInsensitively(t *testing.T) {
	messages := []scoutChatMessageRecord{
		readMarkerMessage("1", "AJ@X.com", "2026-07-28T10:00:00Z"),
	}
	if got := threadUnreadCount(messages, "", "aj@x.com"); got != 0 {
		t.Fatalf("unread = %d, want 0 (own message, different case)", got)
	}
}

func TestThreadReadMarkersRoundTripThroughTheStore(t *testing.T) {
	t.Setenv("THREAD_READ_MARKERS_PATH", filepath.Join(t.TempDir(), "markers.json"))

	if err := upsertThreadReadMarker(threadReadMarker{
		TenantID:          "tenant-a",
		UserEmail:         "AJ@X.com",
		ThreadID:          "table-1",
		LastReadMessageID: "m5",
		ReadAt:            "2026-07-28T10:00:00Z",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Stored normalized, so a lookup with different case still finds it.
	found := lookupThreadReadMarker("tenant-a", "aj@x.com", "table-1")
	if found.ReadAt != "2026-07-28T10:00:00Z" || found.LastReadMessageID != "m5" {
		t.Fatalf("marker = %+v, want the one just written", found)
	}
}

// Markers only advance. A retry or an out-of-order request from a stale client
// must never move the marker backwards and resurrect messages already read.
func TestThreadReadMarkersOnlyAdvance(t *testing.T) {
	t.Setenv("THREAD_READ_MARKERS_PATH", filepath.Join(t.TempDir(), "markers.json"))

	newer := threadReadMarker{
		TenantID:  "tenant-a",
		UserEmail: "aj@x.com",
		ThreadID:  "table-1",
		ReadAt:    "2026-07-28T12:00:00Z",
	}
	older := threadReadMarker{
		TenantID:  "tenant-a",
		UserEmail: "aj@x.com",
		ThreadID:  "table-1",
		ReadAt:    "2026-07-28T09:00:00Z",
	}
	if err := upsertThreadReadMarker(newer); err != nil {
		t.Fatalf("upsert newer: %v", err)
	}
	if err := upsertThreadReadMarker(older); err != nil {
		t.Fatalf("upsert older: %v", err)
	}

	if got := lookupThreadReadMarker("tenant-a", "aj@x.com", "table-1").ReadAt; got != newer.ReadAt {
		t.Fatalf("marker went backwards: %s, want %s", got, newer.ReadAt)
	}
}

// Two users reading the same thread must not overwrite each other, and neither
// must two threads for one user. The key is all three parts.
func TestThreadReadMarkersAreKeyedByAllThreeParts(t *testing.T) {
	t.Setenv("THREAD_READ_MARKERS_PATH", filepath.Join(t.TempDir(), "markers.json"))

	base := threadReadMarker{TenantID: "tenant-a", ReadAt: "2026-07-28T10:00:00Z"}

	aj := base
	aj.UserEmail, aj.ThreadID, aj.LastReadMessageID = "aj@x.com", "table-1", "m1"
	dana := base
	dana.UserEmail, dana.ThreadID, dana.LastReadMessageID = "dana@x.com", "table-1", "m2"
	other := base
	other.UserEmail, other.ThreadID, other.LastReadMessageID = "aj@x.com", "pricing", "m3"

	for _, marker := range []threadReadMarker{aj, dana, other} {
		if err := upsertThreadReadMarker(marker); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	if got := lookupThreadReadMarker("tenant-a", "aj@x.com", "table-1").LastReadMessageID; got != "m1" {
		t.Fatalf("aj/table-1 = %q, want m1", got)
	}
	if got := lookupThreadReadMarker("tenant-a", "dana@x.com", "table-1").LastReadMessageID; got != "m2" {
		t.Fatalf("dana/table-1 = %q, want m2", got)
	}
	if got := lookupThreadReadMarker("tenant-a", "aj@x.com", "pricing").LastReadMessageID; got != "m3" {
		t.Fatalf("aj/pricing = %q, want m3", got)
	}
}

func TestScoutChatThreadsIndexViewIsBodyFreeAndViewerAuthorized(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	private, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Private strategy", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	private.Messages = []scoutChatMessageRecord{{ID: "secret-message", Role: "user", Text: strings.Repeat("private body ", 500), AuthorEmail: "aj@shareability.com"}}
	if err := app.saveScoutChatThread(private); err != nil {
		t.Fatal(err)
	}
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Country Golf", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	channel.Messages = []scoutChatMessageRecord{
		{ID: "public-message", Role: "user", Text: strings.Repeat("public body ", 500), AuthorEmail: "aj@shareability.com", CreatedAt: "2026-08-12T19:00:00Z"},
		{ID: "viewer-message", Role: "user", Text: "own reply", AuthorEmail: "tim@shareability.com", CreatedAt: "2026-08-12T19:01:00Z"},
	}
	if err := app.saveScoutChatThread(channel); err != nil {
		t.Fatal(err)
	}

	rows := app.scoutChatThreadsIndexView("tim@shareability.com", false, 100)
	if len(rows) != 1 || rows[0]["id"] != channel.ID || rows[0]["messagesLoaded"] != false {
		t.Fatalf("index rows=%#v, want only the body-free public channel", rows)
	}
	if rows[0]["unreadCount"] != 1 || rows[0]["messageCount"] != 2 {
		t.Fatalf("index viewer/count projection=%#v, want one unread of two", rows[0])
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("public body")) || bytes.Contains(encoded, []byte("secret-message")) || bytes.Contains(encoded, []byte(`"messages":`)) {
		t.Fatalf("index leaked conversation body/shape: %s", encoded)
	}
}

func TestScoutChatThreadsIndexSelectionNeverEvaluatesTheFullProjection(t *testing.T) {
	indexCalls := 0
	got := selectScoutChatThreadsListProjection(
		true,
		func() []map[string]any {
			indexCalls++
			return []map[string]any{{"id": "fast-index"}}
		},
		func() []map[string]any {
			t.Fatal("body-free index evaluated the full conversation projection")
			return nil
		},
	)
	if indexCalls != 1 || len(got) != 1 || got[0]["id"] != "fast-index" {
		t.Fatalf("index calls=%d projection=%#v", indexCalls, got)
	}
}

func TestScoutChatThreadsIndexClearsTerminalWorkAndDeletedActivity(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Lifecycle", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	thread.Messages = []scoutChatMessageRecord{{ID: "work", Role: "scout", CreatedAt: "2026-08-12T19:00:00Z", Thread: &scoutChatThreadRef{ID: "run", Status: "running", Query: "private request body"}}}
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	rows := app.scoutChatThreadsIndexView("aj@shareability.com", false, 100)
	if len(rows) != 1 || rows[0]["activeWork"] == nil {
		t.Fatalf("running work missing from body-free index: %#v", rows)
	}
	encoded, _ := json.Marshal(rows[0]["activeWork"])
	if bytes.Contains(encoded, []byte("private request body")) || bytes.Contains(encoded, []byte(`"query"`)) {
		t.Fatalf("active-work projection leaked request body: %s", encoded)
	}
	thread.Messages[0].Thread.Status = "complete"
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	rows = app.scoutChatThreadsIndexView("aj@shareability.com", false, 100)
	if rows[0]["activeWork"] != nil {
		t.Fatalf("terminal work remained active: %#v", rows[0])
	}
	thread.Messages = nil
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	rows = app.scoutChatThreadsIndexView("aj@shareability.com", false, 100)
	if rows[0]["messageCount"] != 0 || rows[0]["unreadCount"] != 0 {
		t.Fatalf("deleted activity remained in index: %#v", rows[0])
	}
}

func TestScoutChatThreadMutationViewNeverReturnsConversationBodies(t *testing.T) {
	view := scoutChatThreadMutationView(scoutChatThreadRecord{ID: "thread", Title: "Renamed", Messages: []scoutChatMessageRecord{{ID: "secret", Text: strings.Repeat("body", 1000)}}})
	encoded, _ := json.Marshal(view)
	if bytes.Contains(encoded, []byte("secret")) || bytes.Contains(encoded, []byte(`"messages"`)) {
		t.Fatalf("mutation response leaked conversation: %s", encoded)
	}
}

// The view must carry the record's own fields through untouched — the client
// reads title, visibility, updatedAt and the rest from these same rows.
func TestThreadsViewPreservesRecordFieldsAndAddsViewerState(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("THREAD_READ_MARKERS_PATH", filepath.Join(t.TempDir(), "markers.json"))
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "pricing", "public")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	rows := kanbanApp.scoutChatThreadsView("aj@shareability.com", false, 100)
	if len(rows) == 0 {
		t.Fatal("view returned no rows")
	}
	var row map[string]any
	for _, candidate := range rows {
		if candidate["id"] == thread.ID {
			row = candidate
		}
	}
	if row == nil {
		t.Fatalf("view is missing thread %s", thread.ID)
	}
	if row["title"] != thread.Title {
		t.Fatalf("title = %v, want %q", row["title"], thread.Title)
	}
	if row["visibility"] != scoutChatVisibilityPublic {
		t.Fatalf("visibility = %v, want public", row["visibility"])
	}
	if _, ok := row["unreadCount"]; !ok {
		t.Fatal("view did not add unreadCount")
	}
}

// The invariant that justifies the view existing at all: unreadCount is
// per-viewer, so it must never be written onto the record every other user
// shares. If this ever fails, one person reading a channel would mark it read
// for the whole team.
func TestViewerStateNeverReachesThePersistedRecord(t *testing.T) {
	encoded, err := json.Marshal(scoutChatThreadRecord{ID: "t1", Title: "pricing"})
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"unreadCount", "lastReadMessageId", "readAt"} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("persisted record carries per-viewer field %q: %s", leaked, encoded)
		}
	}
}

// Read state is best-effort: a missing or corrupt file must read as "nothing
// read yet", never wedge the thread list that calls it on every load.
func TestThreadReadMarkerLookupToleratesAMissingStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "markers.json")
	t.Setenv("THREAD_READ_MARKERS_PATH", path)

	if got := lookupThreadReadMarker("tenant-a", "aj@x.com", "table-1"); got.ReadAt != "" {
		t.Fatalf("missing store returned %+v, want zero marker", got)
	}

	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := lookupThreadReadMarker("tenant-a", "aj@x.com", "table-1"); got.ReadAt != "" {
		t.Fatalf("corrupt store returned %+v, want zero marker", got)
	}
}
