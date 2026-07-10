package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNotificationStorePersistsAndCapsNewest(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	total := notificationStoreCap + 5
	for index := 0; index < total; index++ {
		if _, err := app.createNotification("", "info", fmt.Sprintf("notification %d", index), "", "", "", false); err != nil {
			t.Fatalf("createNotification %d: %v", index, err)
		}
	}

	app.mu.Lock()
	stored := len(app.notifications)
	oldest := app.notifications[0].Text
	newest := app.notifications[stored-1].Text
	newestID := app.notifications[stored-1].ID
	app.mu.Unlock()
	if stored != notificationStoreCap {
		t.Fatalf("stored=%d, want cap %d", stored, notificationStoreCap)
	}
	if oldest != "notification 5" {
		t.Fatalf("oldest survivor=%q, want notification 5 (cap drops oldest)", oldest)
	}
	if newest != fmt.Sprintf("notification %d", total-1) {
		t.Fatalf("newest=%q, want notification %d", newest, total-1)
	}

	if _, err := app.markNotificationsRead("aj@shareability.com", []string{newestID}); err != nil {
		t.Fatalf("markNotificationsRead: %v", err)
	}

	// A fresh app on the same data dir reloads the capped store and the
	// read receipts from data/notifications.json.
	reloaded := newKanbanBoardApp()
	reloaded.mu.Lock()
	reloadedCount := len(reloaded.notifications)
	reloadedNewest := reloaded.notifications[reloadedCount-1]
	reloaded.mu.Unlock()
	if reloadedCount != notificationStoreCap {
		t.Fatalf("reloaded=%d, want %d persisted notifications", reloadedCount, notificationStoreCap)
	}
	if reloadedNewest.ID != newestID {
		t.Fatalf("reloaded newest id=%q, want %q", reloadedNewest.ID, newestID)
	}
	if !notificationReadBy(reloadedNewest, "aj@shareability.com") {
		t.Fatal("read receipt did not survive reload")
	}

	if _, err := os.Stat(notificationsPath()); err != nil {
		t.Fatalf("notifications file missing: %v", err)
	}
}

func TestNotificationEndpointsRequireAuthAndScopeToViewer(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	// Unauthenticated requests are rejected before touching the store.
	recorder := httptest.NewRecorder()
	assistantNotificationsHandler(recorder, httptest.NewRequest(http.MethodGet, "/assistant/notifications", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauth GET status=%d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	recorder = httptest.NewRecorder()
	assistantNotificationsReadHandler(recorder, httptest.NewRequest(http.MethodPost, "/assistant/notifications/read", strings.NewReader(`{"ids":["x"]}`)))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauth read status=%d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	broadcast, err := kanbanApp.createNotification("", "alert", "all hands update", "board", "", "", false)
	if err != nil {
		t.Fatalf("create broadcast notification: %v", err)
	}
	targeted, err := kanbanApp.createNotification("tom@shareability.com", "task", "just for Tom", "", "", "", false)
	if err != nil {
		t.Fatalf("create targeted notification: %v", err)
	}

	fetchNotifications := func(email string) []map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/assistant/notifications", nil)
		for _, cookie := range loginAs(t, email, "B0NFIRE!") {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantNotificationsHandler(recorder, req)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
		}
		var payload struct {
			Notifications []map[string]any `json:"notifications"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode notifications: %v", err)
		}
		return payload.Notifications
	}

	// AJ sees only the broadcast; Tom's targeted record never leaks.
	ajList := fetchNotifications("aj@shareability.com")
	if len(ajList) != 1 || ajList[0]["id"] != broadcast.ID {
		t.Fatalf("aj notifications=%#v, want only the broadcast", ajList)
	}
	if _, leaked := ajList[0]["readBy"]; leaked {
		t.Fatal("viewer projection must not expose the readBy roster")
	}

	// Tom sees both, newest first, all unread.
	tomList := fetchNotifications("tom@shareability.com")
	if len(tomList) != 2 || tomList[0]["id"] != targeted.ID || tomList[1]["id"] != broadcast.ID {
		t.Fatalf("tom notifications=%#v, want targeted then broadcast (newest first)", tomList)
	}
	for _, item := range tomList {
		if item["read"] != false {
			t.Fatalf("item=%#v, want read=false before acknowledgement", item)
		}
	}

	// Tom marks both read; AJ cannot mark Tom's targeted record.
	readBody := fmt.Sprintf(`{"ids":[%q,%q]}`, targeted.ID, broadcast.ID)
	readReq := httptest.NewRequest(http.MethodPost, "/assistant/notifications/read", strings.NewReader(readBody))
	for _, cookie := range loginAs(t, "tom@shareability.com", "B0NFIRE!") {
		readReq.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	assistantNotificationsReadHandler(recorder, readReq)
	if recorder.Code != http.StatusOK {
		t.Fatalf("read status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}
	var readPayload struct {
		Marked int `json:"marked"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &readPayload); err != nil {
		t.Fatalf("decode read response: %v", err)
	}
	if readPayload.Marked != 2 {
		t.Fatalf("marked=%d, want 2", readPayload.Marked)
	}

	marked, err := kanbanApp.markNotificationsRead("aj@shareability.com", []string{targeted.ID})
	if err != nil {
		t.Fatalf("markNotificationsRead as non-recipient: %v", err)
	}
	if marked != 0 {
		t.Fatalf("marked=%d, want 0 for a notification aj cannot see", marked)
	}

	for _, item := range fetchNotifications("tom@shareability.com") {
		if item["read"] != true {
			t.Fatalf("item=%#v, want read=true after acknowledgement", item)
		}
	}
	// Tom's read receipts do not mark AJ's copy of the broadcast.
	if ajAfter := fetchNotifications("aj@shareability.com"); ajAfter[0]["read"] != false {
		t.Fatalf("aj broadcast=%#v, want still unread for aj", ajAfter[0])
	}
	if unread := kanbanApp.unreadNotificationsFor("tom@shareability.com", notificationListLimit); len(unread) != 0 {
		t.Fatalf("tom unread=%#v, want empty backlog after read", unread)
	}
}

func TestRealtimeSendNotificationToolDispatch(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	// Room path: audience everyone with an OS tool route.
	result, changed, err := app.applyToolCallArgs("send_notification", map[string]any{
		"text":     "standup notes are ready",
		"kind":     "task",
		"audience": "everyone",
		"tool":     "board",
	})
	if err != nil {
		t.Fatalf("send_notification everyone: %v", err)
	}
	if changed {
		t.Fatal("send_notification must not report a board change")
	}
	if result["ok"] != true || result["audience"] != "everyone" {
		t.Fatalf("result=%#v, want ok everyone", result)
	}
	actions, ok := result["actions"].([]osAssistantAction)
	if !ok || len(actions) != 1 || actions[0].Type != "notify" || actions[0].Label != "standup notes are ready" || actions[0].ID == "" {
		t.Fatalf("actions=%#v, want one notify action carrying id and text", result["actions"])
	}

	// Private path: audience me targets the requesting account.
	if _, _, err := app.applyPrivateRealtimeVoiceTool("aj@shareability.com", "send_notification", map[string]any{
		"text":     "remember the deck review",
		"kind":     "info",
		"audience": "me",
	}); err != nil {
		t.Fatalf("send_notification me: %v", err)
	}

	// Room path audience me has no requester and falls back to everyone.
	roomMe, _, err := app.applyToolCallArgs("send_notification", map[string]any{
		"text":     "wrap in five",
		"kind":     "alert",
		"audience": "me",
	})
	if err != nil {
		t.Fatalf("send_notification room me: %v", err)
	}
	if roomMe["audience"] != "everyone" {
		t.Fatalf("room audience=%v, want everyone fallback without a requester", roomMe["audience"])
	}

	app.mu.Lock()
	records := append([]notificationRecord(nil), app.notifications...)
	app.mu.Unlock()
	if len(records) != 3 {
		t.Fatalf("stored notifications=%d, want 3", len(records))
	}
	if records[0].UserEmail != "" || records[0].Tool != "board" || records[0].Kind != "task" {
		t.Fatalf("records[0]=%#v, want broadcast task routed to board", records[0])
	}
	if records[1].UserEmail != "aj@shareability.com" {
		t.Fatalf("records[1]=%#v, want targeted at aj", records[1])
	}
	if records[2].UserEmail != "" {
		t.Fatalf("records[2]=%#v, want everyone fallback", records[2])
	}

	// Errors surface through err so the tool endpoint can fold them into the
	// always-200 result map.
	for name, args := range map[string]map[string]any{
		"empty text":   {"text": "   ", "kind": "info", "audience": "everyone"},
		"bad audience": {"text": "hi", "kind": "info", "audience": "them"},
		"bad tool":     {"text": "hi", "kind": "info", "audience": "everyone", "tool": "sidebar"},
	} {
		if _, _, err := app.applyToolCallArgs("send_notification", args); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
}

// Contract test alongside TestRealtimeToolsExposeOSControlAndArtifacts: the
// schema, both allowlists, and both instruction strings must expose
// send_notification.
func TestRealtimeSendNotificationToolContract(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	rawTools, err := json.Marshal(app.kanbanTools())
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	toolsJSON := string(rawTools)
	for _, want := range []string{`"name":"send_notification"`, `"everyone"`, `"me"`, "notification bell"} {
		if !strings.Contains(toolsJSON, want) {
			t.Fatalf("tools JSON missing %s", want)
		}
	}

	if !privateRealtimeVoiceToolAllowed("send_notification") {
		t.Fatal("private realtime voice must allow send_notification")
	}
	foundPrivate := false
	for _, tool := range app.privateRealtimeVoiceTools() {
		if asString(tool["name"]) == "send_notification" {
			foundPrivate = true
		}
	}
	if !foundPrivate {
		t.Fatal("privateRealtimeVoiceTools must expose the send_notification schema")
	}

	if !strings.Contains(app.sessionInstructions(), "send_notification") {
		t.Fatal("room session instructions must mention send_notification")
	}
	if !strings.Contains(app.privateRealtimeVoiceSessionInstructions(), "send_notification") {
		t.Fatal("private voice instructions must mention send_notification")
	}
}

// A newly admitted participant receives their unread notification backlog as
// a direct send inside the accept block, scoped to the session account.
func TestWebsocketNotificationBacklogReplayOnAdmission(t *testing.T) {
	server := newIsolatedWebsocketServer(t)

	broadcast, err := kanbanApp.createNotification("", "info", "team broadcast", "", "", "", false)
	if err != nil {
		t.Fatalf("create broadcast: %v", err)
	}
	targeted, err := kanbanApp.createNotification("tom@shareability.com", "agent", "your thread finished", "", "os-artifact-research-1", "", false)
	if err != nil {
		t.Fatalf("create targeted: %v", err)
	}
	if _, err := kanbanApp.createNotification("tyler@shareability.com", "task", "tyler only", "", "", "", false); err != nil {
		t.Fatalf("create other-user notification: %v", err)
	}

	conn := dialIsolatedWebsocket(t, server, "tom@shareability.com")
	writeNativeWebsocketEvent(t, conn, "participant", map[string]any{})

	raw := waitForKanbanEvent(t, conn, "notification_backlog", 5*time.Second)
	var backlog []struct {
		ID         string `json:"id"`
		Kind       string `json:"kind"`
		Text       string `json:"text"`
		ArtifactID string `json:"artifactId"`
		Read       bool   `json:"read"`
		CreatedAt  string `json:"createdAt"`
	}
	if err := json.Unmarshal(raw, &backlog); err != nil {
		t.Fatalf("decode notification backlog: %v", err)
	}
	if len(backlog) != 2 {
		t.Fatalf("backlog length=%d (%#v), want tom's targeted + broadcast only", len(backlog), backlog)
	}
	if backlog[0].ID != targeted.ID || backlog[0].ArtifactID != "os-artifact-research-1" {
		t.Fatalf("backlog[0]=%+v, want the targeted agent notification newest-first", backlog[0])
	}
	if backlog[1].ID != broadcast.ID {
		t.Fatalf("backlog[1]=%+v, want the broadcast", backlog[1])
	}
	for _, item := range backlog {
		if item.Read {
			t.Fatalf("item=%+v, want only unread records in the backlog", item)
		}
		if _, err := time.Parse(time.RFC3339Nano, item.CreatedAt); err != nil {
			t.Fatalf("createdAt=%q is not RFC3339Nano: %v", item.CreatedAt, err)
		}
	}

	// Once read, the record drops out of the next admission replay.
	if _, err := kanbanApp.markNotificationsRead("tom@shareability.com", []string{targeted.ID, broadcast.ID}); err != nil {
		t.Fatalf("markNotificationsRead: %v", err)
	}
	rejoined := dialIsolatedWebsocket(t, server, "tom@shareability.com")
	writeNativeWebsocketEvent(t, rejoined, "participant", map[string]any{})
	raw = waitForKanbanEvent(t, rejoined, "notification_backlog", 5*time.Second)
	var emptied []map[string]any
	if err := json.Unmarshal(raw, &emptied); err != nil {
		t.Fatalf("decode second backlog: %v", err)
	}
	if len(emptied) != 0 {
		t.Fatalf("backlog after read=%#v, want empty", emptied)
	}
}

// Targeted notifications are scoped server-side: the live push goes only to
// sockets whose authenticated session email matches the recipient. A
// differently-authenticated connection never receives the payload, even
// though it sits in the broadcast fan-out pool.
func TestWebsocketTargetedNotificationScopedToRecipientSession(t *testing.T) {
	server := newIsolatedWebsocketServer(t)

	// Tom is admitted but has NOT joined media: targeted delivery must still
	// reach him through the admission-time registry.
	tomConn := dialIsolatedWebsocket(t, server, "tom@shareability.com")
	writeNativeWebsocketEvent(t, tomConn, "participant", map[string]any{})
	waitForKanbanEvent(t, tomConn, "access_granted", 5*time.Second)

	// Tyler joins media so he is in the broadcast pool — the strongest
	// position from which a leaked targeted record would reach him.
	tylerConn := dialIsolatedWebsocket(t, server, "tyler@shareability.com")
	writeNativeWebsocketEvent(t, tylerConn, "participant", map[string]any{})
	waitForKanbanEvent(t, tylerConn, "access_granted", 5*time.Second)
	writeNativeWebsocketEvent(t, tylerConn, "media_ready", map[string]any{})

	targeted, err := kanbanApp.createNotification("tom@shareability.com", "task", "just for Tom", "", "", "", false)
	if err != nil {
		t.Fatalf("create targeted notification: %v", err)
	}

	raw := waitForKanbanEvent(t, tomConn, "notification", 5*time.Second)
	var received struct {
		ID        string `json:"id"`
		UserEmail string `json:"userEmail"`
		Text      string `json:"text"`
		Read      bool   `json:"read"`
	}
	if err := json.Unmarshal(raw, &received); err != nil {
		t.Fatalf("decode targeted notification: %v", err)
	}
	if received.ID != targeted.ID || received.Text != "just for Tom" {
		t.Fatalf("tom received=%+v, want the targeted record %s", received, targeted.ID)
	}
	if received.UserEmail != "tom@shareability.com" {
		t.Fatalf("userEmail=%q, want the recipient", received.UserEmail)
	}
	if received.Read {
		t.Fatal("freshly created targeted notification must arrive unread")
	}

	// Sentinel: the next notification event on Tyler's ordered socket must be
	// this broadcast. If the targeted record had leaked to him, it would be
	// read first and fail the assertion below.
	broadcast, err := kanbanApp.createNotification("", "info", "for everyone", "", "", "", false)
	if err != nil {
		t.Fatalf("create broadcast notification: %v", err)
	}
	raw = waitForKanbanEvent(t, tylerConn, "notification", 5*time.Second)
	var tylerFirst struct {
		ID        string `json:"id"`
		UserEmail string `json:"userEmail"`
	}
	if err := json.Unmarshal(raw, &tylerFirst); err != nil {
		t.Fatalf("decode tyler notification: %v", err)
	}
	if tylerFirst.ID == targeted.ID {
		t.Fatal("targeted notification leaked to a differently-authenticated connection")
	}
	if tylerFirst.ID != broadcast.ID || tylerFirst.UserEmail != "" {
		t.Fatalf("tyler first notification=%+v, want the broadcast %s", tylerFirst, broadcast.ID)
	}
}

// The system emitter: a finished agent thread notifies its creator durably,
// carrying the artifact id so the client can route straight to the result.
func TestAgentThreadCompletionNotifiesCreator(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()

	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(_ context.Context, _ string, _ openAITextRequest) (string, error) {
		return "Vision: done.\n\nGoal: complete.\n\nVerification: verified.", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	thread, err := app.launchAgentThread("research", "map the rodeo creator landscape", "AJ")
	if err != nil {
		t.Fatalf("launchAgentThread: %v", err)
	}

	app.runAgentThread(thread)

	unread := app.unreadNotificationsFor("aj@shareability.com", notificationListLimit)
	if len(unread) != 1 {
		t.Fatalf("creator unread=%#v, want exactly the completion notification", unread)
	}
	record := unread[0]
	if record["kind"] != notificationKindAgent {
		t.Fatalf("kind=%v, want agent", record["kind"])
	}
	if record["artifactId"] != thread.Artifact.ID {
		t.Fatalf("artifactId=%v, want %s", record["artifactId"], thread.Artifact.ID)
	}
	if text := asString(record["text"]); !strings.Contains(text, "complete") {
		t.Fatalf("text=%q, want a completion message", text)
	}
	if record["userEmail"] != "aj@shareability.com" {
		t.Fatalf("userEmail=%v, want the creator only", record["userEmail"])
	}
	// The milestone is targeted: other accounts see nothing.
	if other := app.unreadNotificationsFor("tom@shareability.com", notificationListLimit); len(other) != 0 {
		t.Fatalf("other user unread=%#v, want empty", other)
	}
}

// Frontend wiring guard, following the repo's index.html grep-test pattern:
// the bell, badge, panel, websocket routes, notify action, and reduced-motion
// coverage must stay wired together.
func TestIndexNotificationCenterWiring(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	for _, want := range []string{
		`id="notificationBell"`,
		`id="notificationBadge"`,
		`id="notificationPanel"`,
		`id="notificationList"`,
		`id="notificationMarkAll"`,
		`id="notificationClose"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing notification anchor %s", want)
		}
	}

	kanbanSwitch := functionBody(html, "function handleKanbanMessage(message)")
	if !strings.Contains(kanbanSwitch, "case 'notification':") {
		t.Fatal("handleKanbanMessage must route the notification event")
	}
	if !strings.Contains(kanbanSwitch, "case 'notification_backlog':") {
		t.Fatal("handleKanbanMessage must route the notification_backlog replay")
	}

	actionsBody := functionBody(html, "function handleOSAssistantActions(actions)")
	if !strings.Contains(actionsBody, "'notify'") || !strings.Contains(actionsBody, "showToast") {
		t.Fatal("handleOSAssistantActions must handle the notify action with a toast + store append")
	}

	// CARD 075: a repeat id is a server rewrite — the entry updates in place
	// and read never flips back to unread from a viewerless broadcast; the
	// proposal linkage rides the normalized entry for the bell rewrite.
	// (functionBody cannot extract appendNotificationEntry — its signature's
	// `options = {}` closes the brace walk — so grep the merge line directly.)
	if !strings.Contains(html, "existing.read = existing.read || entry.read") {
		t.Fatal("appendNotificationEntry must merge repeat ids without flipping read back to unread")
	}
	if !strings.Contains(functionBody(html, "function normalizeNotificationEntry(payload)"), "proposalId: String(payload.proposalId || '')") {
		t.Fatal("normalizeNotificationEntry must carry the proposalId linkage")
	}

	// CARDS 062+063: proposal nudges persist until acted on — the panel-open
	// sweep skips them, a row click never marks them read, and the click
	// routes to the actionable deck card instead of a dead end.
	if !strings.Contains(functionBody(html, "function markAllNotificationsRead()"), "!entry.read && !entry.proposalId") {
		t.Fatal("markAllNotificationsRead must skip proposal nudges (they settle on proposal action, not on view)")
	}
	openBody := functionBody(html, "function openNotificationEntry(entry)")
	if !strings.Contains(openBody, "const awaitsProposalAction = Boolean(entry.proposalId) && !entry.read") {
		t.Fatal("openNotificationEntry must detect a nudge still awaiting its proposal action")
	}
	if !strings.Contains(openBody, "focusCodexProposalCard(entry.proposalId)") {
		t.Fatal("openNotificationEntry must route a pending nudge to the room deck card")
	}

	if !strings.Contains(html, `class="chat-thread-item__unread notification-bell__unread"`) {
		t.Fatal("bell unread badge must reuse the chat-thread-item__unread ink pill")
	}

	// chrome-glass panel at the decided z slot with reduced-motion coverage
	if !strings.Contains(html, "z-index: 1150;") {
		t.Fatal("notification panel must sit at z-index 1150 (between toast 1100 and card-detail 1200)")
	}
	reduced := html[strings.LastIndex(html, "@media (prefers-reduced-motion: reduce)"):]
	if !strings.Contains(reduced, ".notification-panel") {
		t.Fatal("notification panel animation must be covered by the reduced-motion block")
	}
}

// Deferred notifications (deliver "after_meeting") queue invisibly and flush
// exactly once when the meeting ends: hidden from every list while queued,
// restamped to the delivery moment on flush, and idempotent across the
// meeting-end seam + archiveMeeting double invocation.
func TestDeferredNotificationsQueueAndFlushIdempotently(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	queuedBroadcast, err := app.createNotification("", "info", "post-meeting broadcast reminder", "", "", "", true)
	if err != nil {
		t.Fatalf("create deferred broadcast: %v", err)
	}
	queuedTargeted, err := app.createNotification("aj@shareability.com", "task", "post-meeting reminder for aj", "", "", "", true)
	if err != nil {
		t.Fatalf("create deferred targeted: %v", err)
	}
	if queuedBroadcast.DeliverAfter != notificationDeliverAfterMeetingMarker || queuedTargeted.DeliverAfter != notificationDeliverAfterMeetingMarker {
		t.Fatalf("deferred records missing queue marker: %q/%q", queuedBroadcast.DeliverAfter, queuedTargeted.DeliverAfter)
	}

	// Queued records are invisible everywhere until the flush.
	if list := app.notificationsForUser("aj@shareability.com", notificationListLimit); len(list) != 0 {
		t.Fatalf("queued deferred notifications leaked into the list: %#v", list)
	}
	if unread := app.unreadNotificationsFor("aj@shareability.com", notificationListLimit); len(unread) != 0 {
		t.Fatalf("queued deferred notifications leaked into the unread backlog: %#v", unread)
	}

	queuedAt := queuedBroadcast.CreatedAt
	time.Sleep(5 * time.Millisecond)
	if flushed := app.flushDeferredNotifications("meeting_end"); flushed != 2 {
		t.Fatalf("flushed=%d, want 2", flushed)
	}

	unread := app.unreadNotificationsFor("aj@shareability.com", notificationListLimit)
	if len(unread) != 2 {
		t.Fatalf("unread after flush=%#v, want both records delivered", unread)
	}
	for _, item := range unread {
		createdAt := asString(item["createdAt"])
		if createdAt <= queuedAt {
			t.Fatalf("createdAt=%q not restamped past queue time %q (bell orders by delivery moment)", createdAt, queuedAt)
		}
	}
	app.mu.Lock()
	for _, record := range app.notifications {
		if record.DeliverAfter != "" {
			t.Fatalf("record %s still queued after flush: %q", record.ID, record.DeliverAfter)
		}
	}
	app.mu.Unlock()

	// Idempotent: the second seam invocation finds nothing.
	if flushed := app.flushDeferredNotifications("archive"); flushed != 0 {
		t.Fatalf("second flush=%d, want 0", flushed)
	}
}

// The send_notification tool contract for deliver "after_meeting": the result
// carries no actions (no toast now), the record queues, and bad deliver
// values error through the normal path.
func TestSendNotificationDeliverAfterMeeting(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	result, changed, err := app.applyToolCallArgs("send_notification", map[string]any{
		"text":     "after the call, remind everyone about the deck",
		"kind":     "task",
		"audience": "everyone",
		"deliver":  "after_meeting",
	})
	if err != nil {
		t.Fatalf("send_notification after_meeting: %v", err)
	}
	if changed {
		t.Fatal("send_notification must not report a board change")
	}
	if result["deliver"] != notificationDeliverAfterMeeting {
		t.Fatalf("result deliver=%v, want after_meeting", result["deliver"])
	}
	if _, hasActions := result["actions"]; hasActions {
		t.Fatalf("deferred send_notification must not return actions (no toast now): %#v", result)
	}

	app.mu.Lock()
	queued := 0
	for _, record := range app.notifications {
		if record.DeliverAfter == notificationDeliverAfterMeetingMarker {
			queued++
		}
	}
	app.mu.Unlock()
	if queued != 1 {
		t.Fatalf("queued=%d, want 1 deferred record", queued)
	}

	if _, _, err := app.applyToolCallArgs("send_notification", map[string]any{
		"text":     "hi",
		"kind":     "info",
		"audience": "everyone",
		"deliver":  "later",
	}); err == nil {
		t.Fatal("invalid deliver value must error")
	}

	// Contract: schema + both instruction builders teach the deferral.
	rawTools, err := json.Marshal(app.kanbanTools())
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	if !strings.Contains(string(rawTools), `"after_meeting"`) {
		t.Fatal("send_notification schema missing the deliver after_meeting enum")
	}
	if !strings.Contains(app.sessionInstructions(), "after_meeting") {
		t.Fatal("room session instructions must teach deliver after_meeting")
	}
	if !strings.Contains(app.privateRealtimeVoiceSessionInstructions(), "after_meeting") {
		t.Fatal("private voice instructions must teach deliver after_meeting")
	}
}

// archiveMeeting is the second flush seam: queued records deliver before the
// meeting id rotates, so an archive with no idle-end still empties the queue.
func TestArchiveMeetingFlushesDeferredNotifications(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	if _, err := app.createNotification("tom@shareability.com", "task", "after-meeting reminder for tom", "", "", "", true); err != nil {
		t.Fatalf("create deferred notification: %v", err)
	}
	if unread := app.unreadNotificationsFor("tom@shareability.com", notificationListLimit); len(unread) != 0 {
		t.Fatalf("deferred record visible before archive: %#v", unread)
	}

	if _, err := app.archiveMeeting("AJ"); err != nil {
		t.Fatalf("archiveMeeting: %v", err)
	}

	unread := app.unreadNotificationsFor("tom@shareability.com", notificationListLimit)
	if len(unread) != 1 || !strings.Contains(asString(unread[0]["text"]), "after-meeting reminder") {
		t.Fatalf("unread after archive=%#v, want the flushed deferred record", unread)
	}
}

// The idle meeting-end seam (endMeetingForIdle) flushes the same queue before
// rotating the memory meeting id.
func TestIdleMeetingEndFlushesDeferredNotifications(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	app.noteMeetingAdmission(officeRoomID, "AJ")
	if _, err := app.createNotification("", "info", "reminder queued during the meeting", "", "", "", true); err != nil {
		t.Fatalf("create deferred notification: %v", err)
	}

	fireIdleEndNow(app)

	unread := app.unreadNotificationsFor("aj@shareability.com", notificationListLimit)
	if len(unread) != 1 || !strings.Contains(asString(unread[0]["text"]), "reminder queued") {
		t.Fatalf("unread after idle end=%#v, want the flushed deferred record", unread)
	}
	if flushed := app.flushDeferredNotifications("archive"); flushed != 0 {
		t.Fatalf("second flush=%d, want 0 (idempotent across both seams)", flushed)
	}
}

// Channel notifications deep-link: threadId survives the viewer projection so
// the client bell can route straight to the channel.
func TestNotificationForViewerCarriesThreadID(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	record, err := app.createNotification("", notificationKindChat, "AJ posted in #warroom: ship it", "chat", "", "scout-chat-123", false)
	if err != nil {
		t.Fatalf("create channel notification: %v", err)
	}
	payload := notificationForViewer(record, "")
	if payload["threadId"] != "scout-chat-123" {
		t.Fatalf("viewer payload threadId=%v, want scout-chat-123", payload["threadId"])
	}
}

// clearNotifications is per-viewer: clearing dismisses records from the
// clearer's bell (list + unread backlog) without deleting the shared record,
// so a broadcast one account cleared still reaches everyone else. The stamp
// survives a reload, an ids-subset clear scopes to the named ids, and the
// viewer projection never leaks the clearedBy roster.
func TestClearNotificationsScopedToViewer(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	broadcast, err := app.createNotification("", "info", "all-hands broadcast", "", "", "", false)
	if err != nil {
		t.Fatalf("create broadcast: %v", err)
	}
	if _, err := app.createNotification("tom@shareability.com", "task", "just for Tom", "", "", "", false); err != nil {
		t.Fatalf("create targeted: %v", err)
	}

	// Tom sees both before clearing.
	if list := app.notificationsForUser("tom@shareability.com", notificationListLimit); len(list) != 2 {
		t.Fatalf("tom pre-clear list=%#v, want both records", list)
	}

	// Tom clears all (nil ids): both his visible records get stamped.
	cleared, err := app.clearNotifications("tom@shareability.com", nil)
	if err != nil {
		t.Fatalf("clearNotifications: %v", err)
	}
	if cleared != 2 {
		t.Fatalf("cleared=%d, want 2", cleared)
	}

	// Tom's GET list AND unread backlog are empty after the clear — the one
	// filter covers both callers.
	if list := app.notificationsForUser("tom@shareability.com", notificationListLimit); len(list) != 0 {
		t.Fatalf("tom list after clear=%#v, want empty", list)
	}
	if unread := app.unreadNotificationsFor("tom@shareability.com", notificationListLimit); len(unread) != 0 {
		t.Fatalf("tom unread after clear=%#v, want empty", unread)
	}

	// AJ is unaffected: the shared broadcast survives for him.
	ajList := app.notificationsForUser("aj@shareability.com", notificationListLimit)
	if len(ajList) != 1 || ajList[0]["id"] != broadcast.ID {
		t.Fatalf("aj list=%#v, want the broadcast still visible", ajList)
	}
	if _, leaked := ajList[0]["clearedBy"]; leaked {
		t.Fatal("viewer projection must not expose the clearedBy roster")
	}

	// Idempotent: a second clear finds nothing new.
	if again, err := app.clearNotifications("tom@shareability.com", nil); err != nil || again != 0 {
		t.Fatalf("second clear=(%d,%v), want (0,nil)", again, err)
	}

	// ids-subset clear scopes to the named ids only.
	ajFirst, err := app.createNotification("aj@shareability.com", "info", "first for aj", "", "", "", false)
	if err != nil {
		t.Fatalf("create ajFirst: %v", err)
	}
	ajSecond, err := app.createNotification("aj@shareability.com", "info", "second for aj", "", "", "", false)
	if err != nil {
		t.Fatalf("create ajSecond: %v", err)
	}
	subset, err := app.clearNotifications("aj@shareability.com", []string{ajFirst.ID})
	if err != nil {
		t.Fatalf("subset clear: %v", err)
	}
	if subset != 1 {
		t.Fatalf("subset cleared=%d, want only ajFirst", subset)
	}
	afterSubset := app.notificationsForUser("aj@shareability.com", notificationListLimit)
	if len(afterSubset) != 2 || afterSubset[0]["id"] != ajSecond.ID || afterSubset[1]["id"] != broadcast.ID {
		t.Fatalf("aj list after subset=%#v, want ajSecond then broadcast (ajFirst cleared)", afterSubset)
	}

	// Cleared state survives a reload from data/notifications.json.
	reloaded := newKanbanBoardApp()
	if list := reloaded.notificationsForUser("tom@shareability.com", notificationListLimit); len(list) != 0 {
		t.Fatalf("tom list after reload=%#v, want still cleared", list)
	}
	reloadedAJ := reloaded.notificationsForUser("aj@shareability.com", notificationListLimit)
	if len(reloadedAJ) != 2 || reloadedAJ[0]["id"] != ajSecond.ID || reloadedAJ[1]["id"] != broadcast.ID {
		t.Fatalf("aj list after reload=%#v, want ajSecond then broadcast (ajFirst still cleared)", reloadedAJ)
	}
}

// A pending proposal nudge is a live call to action: clearNotifications reuses
// the mark-read sticky guard so a clear sweep skips it, yet it still settles on
// proposal action, and a record cleared AFTER it settles stays cleared through
// the settle re-broadcast (TASK 1.5 — the in-place rewrite rides ClearedBy).
func TestClearNotificationsSkipsStickyNudgeAndPreservesSettleClear(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	result, _, err := app.applyToolCallArgs("propose_codex_task", map[string]any{
		"title": "Sticky clear audit",
		"mode":  "research",
		"query": "Research the sticky clear and draft a brief.",
	})
	if err != nil {
		t.Fatalf("propose_codex_task: %v", err)
	}
	proposalID := asString(result["proposal"].(map[string]any)["id"])

	// The pending nudge is sticky: Tom's clear-all skips it and it survives.
	if cleared, err := app.clearNotifications("tom@shareability.com", nil); err != nil || cleared != 0 {
		t.Fatalf("clear over a pending nudge=(%d,%v), want (0,nil) — it must survive", cleared, err)
	}
	tomList := app.notificationsForUser("tom@shareability.com", notificationListLimit)
	if len(tomList) != 1 || tomList[0]["proposalId"] != proposalID {
		t.Fatalf("tom list=%#v, want the surviving pending nudge", tomList)
	}

	// It still settles on proposal action, then reads as acknowledged for all.
	if _, _, err := app.resolveCodexProposal(proposalID, "dismiss", "AJ", "aj@shareability.com"); err != nil {
		t.Fatalf("dismiss proposal: %v", err)
	}
	settled := app.notificationsForUser("tom@shareability.com", notificationListLimit)
	if len(settled) != 1 || settled[0]["proposalId"] != proposalID || settled[0]["read"] != true {
		t.Fatalf("tom settled list=%#v, want the settled nudge read for every account", settled)
	}

	// Now settled (not sticky), Tom can clear it — and it stays cleared.
	if cleared, err := app.clearNotifications("tom@shareability.com", nil); err != nil || cleared != 1 {
		t.Fatalf("clear over the settled nudge=(%d,%v), want (1,nil)", cleared, err)
	}
	if list := app.notificationsForUser("tom@shareability.com", notificationListLimit); len(list) != 0 {
		t.Fatalf("tom list after clearing the settled nudge=%#v, want empty", list)
	}

	// TASK 1.5 direct pin: a non-sticky linked record (its proposal is not
	// pending) is clearable BEFORE a settle rewrite. Clearing it then settling
	// it must keep the stamp — settleProposalNotification rewrites in place and
	// never resurrects the record for the account that cleared it.
	linked, err := app.createLinkedNotification("", "agent", "linked run nudge", "", "", "", "prop-detached", false)
	if err != nil {
		t.Fatalf("create linked nudge: %v", err)
	}
	if n, err := app.clearNotifications("tom@shareability.com", []string{linked.ID}); err != nil || n != 1 {
		t.Fatalf("clear detached linked nudge=(%d,%v), want (1,nil)", n, err)
	}
	app.settleProposalNotification("prop-detached", "linked run finished", "aj@shareability.com", "os-artifact-linked-1")
	if list := app.notificationsForUser("tom@shareability.com", notificationListLimit); len(list) != 0 {
		t.Fatalf("tom list after settle=%#v, want the cleared record to stay hidden through the rewrite", list)
	}
	if unread := app.unreadNotificationsFor("tom@shareability.com", notificationListLimit); len(unread) != 0 {
		t.Fatalf("tom unread after settle=%#v, want empty", unread)
	}
	// AJ never cleared it, so he still sees the settled outcome.
	found := false
	for _, item := range app.notificationsForUser("aj@shareability.com", notificationListLimit) {
		if item["id"] == linked.ID {
			found = true
			if !strings.Contains(asString(item["text"]), "linked run finished") {
				t.Fatalf("aj settled text=%q, want the rewritten outcome", asString(item["text"]))
			}
		}
	}
	if !found {
		t.Fatal("aj list must still carry the settled linked record he never cleared")
	}
}

// The clear endpoint clones the read handler's gate stack: method-first (GET is
// 405), origin, then session (unauth is 401). An authenticated POST with NO
// body clears everything the viewer can see (absent body = clear all).
func TestNotificationClearEndpointGates(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	// Unauthenticated POST is rejected before touching the store.
	rec := httptest.NewRecorder()
	assistantNotificationsClearHandler(rec, httptest.NewRequest(http.MethodPost, "/assistant/notifications/clear", strings.NewReader(`{"ids":["x"]}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth clear status=%d, want %d", rec.Code, http.StatusUnauthorized)
	}

	// GET is not allowed on the clear route (method check runs first).
	rec = httptest.NewRecorder()
	assistantNotificationsClearHandler(rec, httptest.NewRequest(http.MethodGet, "/assistant/notifications/clear", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET clear status=%d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}

	if _, err := kanbanApp.createNotification("", "info", "team-wide", "", "", "", false); err != nil {
		t.Fatalf("create broadcast: %v", err)
	}
	// Absent body: the decoder hits io.EOF, which the handler treats as
	// "clear all visible" rather than a bad request.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/assistant/notifications/clear", nil)
	for _, cookie := range loginAs(t, "tom@shareability.com", "B0NFIRE!") {
		req.AddCookie(cookie)
	}
	assistantNotificationsClearHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("no-body clear status=%d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}
	var out struct {
		Cleared int `json:"cleared"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode clear response: %v", err)
	}
	if out.Cleared != 1 {
		t.Fatalf("cleared=%d, want 1 (the broadcast)", out.Cleared)
	}
	if unread := kanbanApp.unreadNotificationsFor("tom@shareability.com", notificationListLimit); len(unread) != 0 {
		t.Fatalf("tom unread after endpoint clear=%#v, want empty", unread)
	}
}

// Frontend wiring guard for the clear-all control: the button anchor sits
// beside the pinned mark-all-read anchor and is wired to clearAllNotifications,
// which POSTs the clear endpoint, skips sticky pending-proposal nudges, and
// re-syncs the authoritative list on a failed clear — including a non-OK
// response (401/500) that resolves but cleared nothing server-side.
func TestIndexNotificationClearAll(t *testing.T) {
	rawHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(rawHTML)

	if !strings.Contains(html, `id="notificationClearAll"`) {
		t.Fatal("index.html missing the clear-all button anchor")
	}
	// mark-all-read stays a pinned anchor beside it.
	if !strings.Contains(html, `id="notificationMarkAll"`) {
		t.Fatal("clear-all must not displace the mark-all-read anchor")
	}
	if !strings.Contains(html, "notificationClearAll.addEventListener('click', clearAllNotifications)") {
		t.Fatal("clear-all button must be wired to clearAllNotifications")
	}

	clearBody := functionBody(html, "function clearAllNotifications()")
	if clearBody == "" {
		t.Fatal("clearAllNotifications must be defined")
	}
	if !strings.Contains(clearBody, "/assistant/notifications/clear") {
		t.Fatal("clearAllNotifications must POST the clear endpoint")
	}
	if !strings.Contains(clearBody, "entry.proposalId && !entry.read") {
		t.Fatal("clearAllNotifications must skip sticky pending-proposal nudges")
	}
	if !strings.Contains(clearBody, "loadNotifications()") {
		t.Fatal("clearAllNotifications must re-sync the authoritative list on a failed clear")
	}
	// a non-OK response resolves (not rejects), so the .then must inspect res.ok
	// and re-sync too — otherwise the bell sits falsely empty on a 401/500.
	if !strings.Contains(clearBody, "res.ok") {
		t.Fatal("clearAllNotifications must re-sync on a non-OK clear response, not only on a rejected fetch")
	}
}
