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
		if _, err := app.createNotification("", "info", fmt.Sprintf("notification %d", index), "", ""); err != nil {
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

	broadcast, err := kanbanApp.createNotification("", "alert", "all hands update", "board", "")
	if err != nil {
		t.Fatalf("create broadcast notification: %v", err)
	}
	targeted, err := kanbanApp.createNotification("tom@shareability.com", "task", "just for Tom", "", "")
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

	broadcast, err := kanbanApp.createNotification("", "info", "team broadcast", "", "")
	if err != nil {
		t.Fatalf("create broadcast: %v", err)
	}
	targeted, err := kanbanApp.createNotification("tom@shareability.com", "agent", "your thread finished", "", "os-artifact-research-1")
	if err != nil {
		t.Fatalf("create targeted: %v", err)
	}
	if _, err := kanbanApp.createNotification("tyler@shareability.com", "task", "tyler only", "", ""); err != nil {
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

	targeted, err := kanbanApp.createNotification("tom@shareability.com", "task", "just for Tom", "", "")
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
	broadcast, err := kanbanApp.createNotification("", "info", "for everyone", "", "")
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
