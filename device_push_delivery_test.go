package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

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
func captureExpoPush(t *testing.T, reply string) (*[]expoPushMessage, *sync.WaitGroup) {
	t.Helper()
	captured := &[]expoPushMessage{}
	var mu sync.Mutex
	wait := &sync.WaitGroup{}
	wait.Add(1)

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
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))
	t.Setenv("THREAD_MUTES_PATH", filepath.Join(t.TempDir(), "mutes.json"))
	t.Setenv("PUSH_SUBSCRIPTIONS_PATH", filepath.Join(t.TempDir(), "push.json"))

	if err := upsertDeviceToken(deviceTokenRecord{
		UserEmail: "aj@shareability.com",
		Token:     "ExponentPushToken[aj-phone]",
		Platform:  "ios",
	}); err != nil {
		t.Fatalf("register device: %v", err)
	}

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
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))
	t.Setenv("THREAD_MUTES_PATH", filepath.Join(t.TempDir(), "mutes.json"))
	t.Setenv("PUSH_SUBSCRIPTIONS_PATH", filepath.Join(t.TempDir(), "push.json"))

	if err := upsertDeviceToken(deviceTokenRecord{
		UserEmail: "aj@shareability.com", Token: "ExponentPushToken[aj]",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
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
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))
	t.Setenv("THREAD_MUTES_PATH", filepath.Join(t.TempDir(), "mutes.json"))
	t.Setenv("PUSH_SUBSCRIPTIONS_PATH", filepath.Join(t.TempDir(), "push.json"))

	if err := upsertDeviceToken(deviceTokenRecord{
		UserEmail: "aj@shareability.com", Token: "ExponentPushToken[aj]",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
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
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))
	t.Setenv("THREAD_MUTES_PATH", filepath.Join(t.TempDir(), "mutes.json"))
	t.Setenv("PUSH_SUBSCRIPTIONS_PATH", filepath.Join(t.TempDir(), "push.json"))

	for _, row := range []deviceTokenRecord{
		{UserEmail: "aj@shareability.com", Token: "ExponentPushToken[aj]"},
		{UserEmail: "tim@shareability.com", Token: "ExponentPushToken[tim]"},
	} {
		if err := upsertDeviceToken(row); err != nil {
			t.Fatalf("register: %v", err)
		}
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
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))
	t.Setenv("THREAD_MUTES_PATH", filepath.Join(t.TempDir(), "mutes.json"))
	t.Setenv("PUSH_SUBSCRIPTIONS_PATH", filepath.Join(t.TempDir(), "push.json"))

	if err := upsertDeviceToken(deviceTokenRecord{
		UserEmail: "aj@shareability.com", Token: "ExponentPushToken[dead]",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

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
