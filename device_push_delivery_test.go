package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// This is the actual product gate up to Expo: an ordinary committed Table
// post—not a synthetic notification helper call—must buzz a teammate while
// excluding the author from both the durable bell and native delivery.
func TestOrdinaryTablePostReachesTeammateDeviceWithoutBuzzingAuthor(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))
	t.Setenv("THREAD_MUTES_PATH", filepath.Join(t.TempDir(), "mutes.json"))
	t.Setenv("PUSH_SUBSCRIPTIONS_PATH", filepath.Join(t.TempDir(), "push.json"))
	t.Setenv("NOTIFICATIONS_PATH", filepath.Join(t.TempDir(), "notifications.json"))

	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	for _, device := range []struct{ email, token string }{
		{email: "tim@shareability.com", token: "ExponentPushToken[tim-phone]"},
		{email: "aj@shareability.com", token: "ExponentPushToken[aj-phone]"},
	} {
		upsertLiveDeviceTokenForTest(t, device.email, device.token)
	}
	table, err := kanbanApp.ensureTable("tim@shareability.com")
	if err != nil {
		t.Fatalf("ensure Table: %v", err)
	}
	author := accountStore().findUser("tim@shareability.com")
	if author == nil {
		t.Fatal("seed user tim@shareability.com missing")
	}
	captured, wait := captureExpoPush(t, `{"data":[{"status":"ok","id":"x"}]}`)

	if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), author, table.ID, "pricing memo is ready for review", nil, ""); err != nil {
		t.Fatalf("append Table message: %v", err)
	}
	waitFor(t, wait, "ordinary Table post Expo delivery")

	if len(*captured) != 1 || (*captured)[0].To != "ExponentPushToken[aj-phone]" {
		t.Fatalf("captured=%+v, want exactly AJ's device and never the author's", *captured)
	}
	if (*captured)[0].Badge == nil || *(*captured)[0].Badge != 1 {
		t.Fatalf("badge=%v, want the recipient's one unread notification", (*captured)[0].Badge)
	}
	if got := kanbanApp.unreadNotificationsFor("tim@shareability.com", notificationListLimit); len(got) != 0 {
		t.Fatalf("author bell=%#v, want no self-notification", got)
	}
	if got := kanbanApp.unreadNotificationsFor("aj@shareability.com", notificationListLimit); len(got) != 1 {
		t.Fatalf("teammate bell=%#v, want one ambient Table notification", got)
	}
}

// End-to-end delivery pins for the native push lane.
//
// The Wave A gate is "a locked iPhone buzzes when a teammate posts". A physical
// device is the only way to prove the final APNs hop, but everything up to the
// bytes on the wire IS verifiable here: that a teammate's message reaches the
// device lane, survives the recipient/pref/mute filters, and produces a
// correctly-shaped Expo request carrying the body and the deep-link target.
//
// These tests exist because a green unit test on `expoPushMessagesFor` proves
// the formatter works, not that the wiring calls it.

// captureExpoPush stands in for exp.host and returns the decoded request.
func captureExpoPush(t *testing.T, reply string, expectedRequests ...int) (*[]expoPushMessage, *sync.WaitGroup) {
	t.Helper()
	captured := &[]expoPushMessage{}
	var mu sync.Mutex
	wait := &sync.WaitGroup{}
	expected := 1
	if len(expectedRequests) > 0 {
		expected = expectedRequests[0]
	}
	wait.Add(expected)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var batch []expoPushMessage
		_ = json.Unmarshal(body, &batch)
		mu.Lock()
		*captured = append(*captured, batch...)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reply))
		wait.Done()
	}))
	t.Cleanup(server.Close)

	previous := expoPushSendURL
	expoPushSendURL = server.URL
	t.Cleanup(func() { expoPushSendURL = previous })
	return captured, wait
}

func TestTableMentionDeliversOncePerTeammateAndHonorsAmbientMute(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))
	t.Setenv("THREAD_MUTES_PATH", filepath.Join(t.TempDir(), "mutes.json"))
	t.Setenv("PUSH_SUBSCRIPTIONS_PATH", filepath.Join(t.TempDir(), "push.json"))
	t.Setenv("NOTIFICATIONS_PATH", filepath.Join(t.TempDir(), "notifications.json"))

	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	for _, device := range []struct{ email, token string }{
		{email: "tim@shareability.com", token: "ExponentPushToken[tim]"},
		{email: "aj@shareability.com", token: "ExponentPushToken[aj]"},
		{email: "e@shareability.com", token: "ExponentPushToken[erick]"},
	} {
		upsertLiveDeviceTokenForTest(t, device.email, device.token)
	}
	table, err := kanbanApp.ensureTable("tim@shareability.com")
	if err != nil {
		t.Fatal(err)
	}
	author := accountStore().findUser("tim@shareability.com")
	captured, wait := captureExpoPush(t, `{"data":[{"status":"ok","id":"x"}]}`, 2)
	if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), author, table.ID, "@AJ please review this", nil, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, wait, "ambient and targeted Table pushes")

	counts := map[string]int{}
	for _, message := range *captured {
		counts[message.To]++
	}
	if counts["ExponentPushToken[aj]"] != 1 || counts["ExponentPushToken[erick]"] != 1 || counts["ExponentPushToken[tim]"] != 0 || len(*captured) != 2 {
		t.Fatalf("push counts=%v messages=%+v", counts, *captured)
	}
	if got := kanbanApp.unreadNotificationsFor("aj@shareability.com", notificationListLimit); len(got) != 1 || got[0]["userEmail"] != "aj@shareability.com" {
		t.Fatalf("AJ bell=%#v, want one targeted mention", got)
	}
	if got := kanbanApp.unreadNotificationsFor("e@shareability.com", notificationListLimit); len(got) != 1 || got[0]["userEmail"] != nil {
		t.Fatalf("Erick bell=%#v, want one ambient notification", got)
	}
	if got := kanbanApp.unreadNotificationsFor("tim@shareability.com", notificationListLimit); len(got) != 0 {
		t.Fatalf("author bell=%#v, want none", got)
	}

	if err := setThreadMuted("", "e@shareability.com", table.ID, true); err != nil {
		t.Fatal(err)
	}
	capturedMuted, waitMuted := captureExpoPush(t, `{"data":[{"status":"ok","id":"y"}]}`)
	if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), author, table.ID, "@AJ second review", nil, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, waitMuted, "targeted mention through teammate ambient mute")
	if len(*capturedMuted) != 1 || (*capturedMuted)[0].To != "ExponentPushToken[aj]" {
		t.Fatalf("muted delivery=%+v, want only AJ's targeted mention", *capturedMuted)
	}
}

func waitFor(t *testing.T, wait *sync.WaitGroup, why string) {
	t.Helper()
	done := make(chan struct{})
	go func() { wait.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", why)
	}
}

// The gate, minus the APNs hop: a teammate's message produces a correctly
// formed Expo push to the registered device, carrying the readable body and the
// thread to open.
func TestTeammateMessageReachesTheDeviceLane(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))
	t.Setenv("THREAD_MUTES_PATH", filepath.Join(t.TempDir(), "mutes.json"))
	t.Setenv("PUSH_SUBSCRIPTIONS_PATH", filepath.Join(t.TempDir(), "push.json"))

	upsertLiveDeviceTokenForTest(t, "aj@shareability.com", "ExponentPushToken[aj-phone]")

	captured, wait := captureExpoPush(t, `{"data":[{"status":"ok","id":"x"}]}`)

	deliverDevicePushForRecord(notificationRecord{
		ID:       "n1",
		Kind:     notificationKindChat,
		Text:     "Tim: pushed the pricing memo, needs eyes before 2",
		ThreadID: "table-1",
	})
	waitFor(t, wait, "the Expo push request")

	if len(*captured) != 1 {
		t.Fatalf("messages sent = %d, want 1", len(*captured))
	}
	message := (*captured)[0]
	if message.To != "ExponentPushToken[aj-phone]" {
		t.Fatalf("to = %q", message.To)
	}
	// The body is what makes a chat notification useful. A banner reading
	// "New message" is worse than no banner.
	if message.Body != "Tim: pushed the pricing memo, needs eyes before 2" {
		t.Fatalf("body = %q, want the message text", message.Body)
	}
	// Without the thread the tap lands on the canvas and the user has to
	// navigate to the thing they were just told about.
	if message.Data["threadId"] != "table-1" {
		t.Fatalf("data = %v, want threadId table-1", message.Data)
	}
	if message.Sound != "default" {
		t.Fatalf("sound = %q, want default — a silent push is not a buzz", message.Sound)
	}
}

// A muted thread must not buzz. This is the valve that keeps someone from
// disabling notifications at the OS level, which is unrecoverable.
func TestMutedThreadProducesNoPushAtAll(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))
	t.Setenv("THREAD_MUTES_PATH", filepath.Join(t.TempDir(), "mutes.json"))
	t.Setenv("PUSH_SUBSCRIPTIONS_PATH", filepath.Join(t.TempDir(), "push.json"))

	upsertLiveDeviceTokenForTest(t, "aj@shareability.com", "ExponentPushToken[aj]")
	if err := setThreadMuted("", "aj@shareability.com", "table-1", true); err != nil {
		t.Fatalf("mute: %v", err)
	}

	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	previous := expoPushSendURL
	expoPushSendURL = server.URL
	defer func() { expoPushSendURL = previous }()

	deliverDevicePushForRecord(notificationRecord{
		Kind: notificationKindChat, Text: "ambient chatter", ThreadID: "table-1",
	})
	time.Sleep(300 * time.Millisecond)
	if hits != 0 {
		t.Fatalf("muted thread sent %d push requests, want 0", hits)
	}
}

// Mute silences volume, never a page. A direct mention carries a recipient and
// must get through regardless.
func TestMentionPushesThroughAMutedThread(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))
	t.Setenv("THREAD_MUTES_PATH", filepath.Join(t.TempDir(), "mutes.json"))
	t.Setenv("PUSH_SUBSCRIPTIONS_PATH", filepath.Join(t.TempDir(), "push.json"))

	upsertLiveDeviceTokenForTest(t, "aj@shareability.com", "ExponentPushToken[aj]")
	if err := setThreadMuted("", "aj@shareability.com", "table-1", true); err != nil {
		t.Fatalf("mute: %v", err)
	}

	captured, wait := captureExpoPush(t, `{"data":[{"status":"ok","id":"x"}]}`)
	deliverDevicePushForRecord(notificationRecord{
		Kind:      notificationKindChat,
		Text:      "Tim mentioned you",
		ThreadID:  "table-1",
		UserEmail: "aj@shareability.com",
	})
	waitFor(t, wait, "the mention push")
	if len(*captured) != 1 {
		t.Fatalf("mention was swallowed by a thread mute")
	}
}

// A token another account owns must never receive this account's messages.
func TestPushNeverLeaksToAnotherAccountsDevice(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))
	t.Setenv("THREAD_MUTES_PATH", filepath.Join(t.TempDir(), "mutes.json"))
	t.Setenv("PUSH_SUBSCRIPTIONS_PATH", filepath.Join(t.TempDir(), "push.json"))

	for _, row := range []struct{ email, token string }{
		{email: "aj@shareability.com", token: "ExponentPushToken[aj]"},
		{email: "tim@shareability.com", token: "ExponentPushToken[tim]"},
	} {
		upsertLiveDeviceTokenForTest(t, row.email, row.token)
	}

	captured, wait := captureExpoPush(t, `{"data":[{"status":"ok","id":"x"}]}`)
	deliverDevicePushForRecord(notificationRecord{
		Kind:      notificationKindChat,
		Text:      "private to AJ",
		ThreadID:  "t1",
		UserEmail: "aj@shareability.com",
	})
	waitFor(t, wait, "the targeted push")

	for _, message := range *captured {
		if message.To != "ExponentPushToken[aj]" {
			t.Fatalf("targeted push reached %q — leak", message.To)
		}
	}
}

// DeviceNotRegistered must actually prune, not just be classified. A dead token
// that survives is retried forever.
func TestDeadTokenIsPrunedAfterDelivery(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))
	t.Setenv("THREAD_MUTES_PATH", filepath.Join(t.TempDir(), "mutes.json"))
	t.Setenv("PUSH_SUBSCRIPTIONS_PATH", filepath.Join(t.TempDir(), "push.json"))

	upsertLiveDeviceTokenForTest(t, "aj@shareability.com", "ExponentPushToken[dead]")

	_, wait := captureExpoPush(t,
		`{"data":[{"status":"error","details":{"error":"DeviceNotRegistered"}}]}`)
	deliverDevicePushForRecord(notificationRecord{
		Kind: notificationKindChat, Text: "hello", ThreadID: "t1",
	})
	waitFor(t, wait, "the push request")

	// The prune happens after the response is handled.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(snapshotDeviceTokenStore().Tokens) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("dead token survived: %+v", snapshotDeviceTokenStore().Tokens)
}
