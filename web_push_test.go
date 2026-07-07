package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// setupPushTestEnv isolates the push store + VAPID data dir to a temp dir so
// tests never touch the repo's data/ (mirrors setupAuthTestEnv / the
// NOTIFICATIONS_PATH pattern).
func setupPushTestEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("PUSH_SUBSCRIPTIONS_PATH", filepath.Join(dir, "push-subscriptions.json"))
	// Reset the path-keyed VAPID cache so a prior test's keypair never leaks in.
	vapidMu.Lock()
	vapidCache = nil
	vapidCacheKey = ""
	vapidMu.Unlock()
}

func testPushSubscription(email, endpoint string) pushSubscriptionRecord {
	record := pushSubscriptionRecord{
		UserEmail: normalizeAccountEmail(email),
		Endpoint:  endpoint,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	// Keys are opaque here — sendWebPush is stubbed in every delivery test.
	record.Keys.P256dh = "test-p256dh-" + endpoint
	record.Keys.Auth = "test-auth-" + endpoint
	return record
}

type capturedPush struct {
	endpoint string
	payload  map[string]any
	opts     *webpush.Options
}

// stubWebPush installs a capturing sendWebPush that answers with the given
// status, restoring the real sender on cleanup. Returns a locked accessor.
func stubWebPush(t *testing.T, statusFor func(endpoint string) int) func() []capturedPush {
	t.Helper()
	var mu sync.Mutex
	var captured []capturedPush
	previous := sendWebPush
	sendWebPush = func(payload []byte, sub *webpush.Subscription, opts *webpush.Options) (*http.Response, error) {
		var decoded map[string]any
		_ = json.Unmarshal(payload, &decoded)
		mu.Lock()
		captured = append(captured, capturedPush{endpoint: sub.Endpoint, payload: decoded, opts: opts})
		mu.Unlock()
		status := http.StatusCreated
		if statusFor != nil {
			status = statusFor(sub.Endpoint)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	t.Cleanup(func() { sendWebPush = previous })
	return func() []capturedPush {
		mu.Lock()
		defer mu.Unlock()
		return append([]capturedPush(nil), captured...)
	}
}

// withLiveKanbanSocket injects an office connection for email so the
// only-when-away rule sees the account as present, restoring the pool after.
func withLiveKanbanSocket(t *testing.T, email string) {
	t.Helper()
	key := "test-live-" + email
	listLock.Lock()
	previous := officeConnections
	replaced := make(map[string]officeConnectionState, len(officeConnections)+1)
	for k, v := range officeConnections {
		replaced[k] = v
	}
	replaced[key] = officeConnectionState{websocket: &threadSafeWriter{}, sessionEmail: normalizeAccountEmail(email)}
	officeConnections = replaced
	listLock.Unlock()
	t.Cleanup(func() {
		listLock.Lock()
		officeConnections = previous
		listLock.Unlock()
	})
}

// The push HTTP handlers reject unauthenticated and cross-origin callers before
// touching the store, and scope every read/write to the session account.
func TestPushEndpointsRequireAuthAndScopeToViewer(t *testing.T) {
	setupAuthTestEnv(t)
	setupPushTestEnv(t)
	t.Setenv("WEB_PUSH_VAPID_PUBLIC_KEY", "test-public-key")
	t.Setenv("WEB_PUSH_VAPID_PRIVATE_KEY", "test-private-key")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	// Unauthenticated: every handler answers 401 before mutating anything.
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		method  string
		path    string
		body    string
	}{
		{"config", assistantPushConfigHandler, http.MethodGet, "/assistant/push/config", ""},
		{"subscribe", assistantPushSubscribeHandler, http.MethodPost, "/assistant/push/subscribe", `{"endpoint":"e","keys":{"p256dh":"a","auth":"b"}}`},
		{"unsubscribe", assistantPushUnsubscribeHandler, http.MethodPost, "/assistant/push/unsubscribe", `{"endpoint":"e"}`},
		{"prefs", assistantPushPrefsHandler, http.MethodPost, "/assistant/push/prefs", `{"kinds":{"chat":true},"onlyWhenAway":true}`},
	} {
		recorder := httptest.NewRecorder()
		tc.handler(recorder, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s unauth status=%d, want %d", tc.name, recorder.Code, http.StatusUnauthorized)
		}
	}

	// Cross-origin is rejected (403) even with a valid session cookie.
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	crossReq := httptest.NewRequest(http.MethodGet, "http://bonfire.test/assistant/push/config", nil)
	crossReq.Header.Set("Origin", "https://evil.example")
	for _, cookie := range cookies {
		crossReq.AddCookie(cookie)
	}
	crossRec := httptest.NewRecorder()
	assistantPushConfigHandler(crossRec, crossReq)
	if crossRec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status=%d, want %d", crossRec.Code, http.StatusForbidden)
	}

	authed := func(method, path, body string, who []*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		for _, cookie := range who {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		switch {
		case strings.HasSuffix(path, "/config"):
			assistantPushConfigHandler(recorder, req)
		case strings.HasSuffix(path, "/subscribe"):
			assistantPushSubscribeHandler(recorder, req)
		case strings.HasSuffix(path, "/unsubscribe"):
			assistantPushUnsubscribeHandler(recorder, req)
		case strings.HasSuffix(path, "/prefs"):
			assistantPushPrefsHandler(recorder, req)
		}
		return recorder
	}

	// AJ subscribes a device; the store binds it to AJ's account.
	subRec := authed(http.MethodPost, "/assistant/push/subscribe", `{"endpoint":"https://push.example/aj","keys":{"p256dh":"ajp","auth":"aja"}}`, cookies)
	if subRec.Code != http.StatusOK {
		t.Fatalf("subscribe status=%d body=%s", subRec.Code, subRec.Body.String())
	}
	state := snapshotPushStore()
	if len(state.Subscriptions) != 1 || state.Subscriptions[0].UserEmail != "aj@shareability.com" {
		t.Fatalf("store=%#v, want one subscription bound to aj", state.Subscriptions)
	}

	// Tom's config sees vapidPublicKey but reports subscribed=false — AJ's
	// subscription never leaks into Tom's view.
	tomCookies := loginAs(t, "tom@shareability.com", "B0NFIRE!")
	tomConfig := authed(http.MethodGet, "/assistant/push/config", "", tomCookies)
	if tomConfig.Code != http.StatusOK {
		t.Fatalf("tom config status=%d body=%s", tomConfig.Code, tomConfig.Body.String())
	}
	var tomPayload struct {
		VapidPublicKey string `json:"vapidPublicKey"`
		Subscribed     bool   `json:"subscribed"`
		Prefs          struct {
			Kinds        map[string]bool `json:"kinds"`
			OnlyWhenAway bool            `json:"onlyWhenAway"`
		} `json:"prefs"`
	}
	if err := json.Unmarshal(tomConfig.Body.Bytes(), &tomPayload); err != nil {
		t.Fatalf("decode tom config: %v", err)
	}
	if tomPayload.VapidPublicKey != "test-public-key" {
		t.Fatalf("vapidPublicKey=%q, want the env key", tomPayload.VapidPublicKey)
	}
	if tomPayload.Subscribed {
		t.Fatal("tom must not read as subscribed off aj's device")
	}
	if !tomPayload.Prefs.OnlyWhenAway || !tomPayload.Prefs.Kinds["chat"] {
		t.Fatalf("tom default prefs=%#v, want only-when-away on and all kinds on", tomPayload.Prefs)
	}

	// Tom cannot unsubscribe AJ's endpoint (scoped to the session account).
	tomUnsub := authed(http.MethodPost, "/assistant/push/unsubscribe", `{"endpoint":"https://push.example/aj"}`, tomCookies)
	if tomUnsub.Code != http.StatusOK {
		t.Fatalf("tom unsubscribe status=%d", tomUnsub.Code)
	}
	if after := snapshotPushStore(); len(after.Subscriptions) != 1 {
		t.Fatalf("aj subscription removed by tom: %#v", after.Subscriptions)
	}

	// AJ saves prefs; the store records them under AJ's account only.
	prefsRec := authed(http.MethodPost, "/assistant/push/prefs", `{"kinds":{"chat":false,"task":true,"agent":true,"alert":true,"info":true},"onlyWhenAway":false}`, cookies)
	if prefsRec.Code != http.StatusOK {
		t.Fatalf("prefs status=%d body=%s", prefsRec.Code, prefsRec.Body.String())
	}
	saved := resolvePushPrefs(snapshotPushStore(), "aj@shareability.com")
	if saved.Kinds["chat"] || !saved.Kinds["task"] || saved.OnlyWhenAway {
		t.Fatalf("saved prefs=%#v, want chat off, task on, only-when-away off", saved)
	}
}

// Subscriptions persist to disk and reload; upsert-by-endpoint rebinds rather
// than duplicating; and the VAPID keypair is generated once and stable across a
// cache reload.
func TestPushStorePersistsAndVAPIDStable(t *testing.T) {
	setupPushTestEnv(t)

	first := testPushSubscription("aj@shareability.com", "https://push.example/device-1")
	if err := upsertPushSubscription(first); err != nil {
		t.Fatalf("upsert first: %v", err)
	}
	// Same endpoint, new owner: rebinds in place (no duplicate row).
	rebind := testPushSubscription("tom@shareability.com", "https://push.example/device-1")
	if err := upsertPushSubscription(rebind); err != nil {
		t.Fatalf("upsert rebind: %v", err)
	}
	second := testPushSubscription("aj@shareability.com", "https://push.example/device-2")
	if err := upsertPushSubscription(second); err != nil {
		t.Fatalf("upsert second: %v", err)
	}

	reloaded := snapshotPushStore()
	if len(reloaded.Subscriptions) != 2 {
		t.Fatalf("subscriptions=%d, want 2 (endpoint upsert deduped)", len(reloaded.Subscriptions))
	}
	var rebound pushSubscriptionRecord
	for _, sub := range reloaded.Subscriptions {
		if sub.Endpoint == "https://push.example/device-1" {
			rebound = sub
		}
	}
	if rebound.UserEmail != "tom@shareability.com" {
		t.Fatalf("device-1 owner=%q, want the rebound tom", rebound.UserEmail)
	}
	if _, err := os.Stat(pushSubscriptionsPath()); err != nil {
		t.Fatalf("push store file missing: %v", err)
	}

	keys1, err := vapidKeys()
	if err != nil {
		t.Fatalf("vapidKeys: %v", err)
	}
	if keys1.Public == "" || keys1.Private == "" {
		t.Fatal("generated VAPID keypair is empty")
	}
	// Drop the cache and reload from disk — the persisted pair must be identical
	// (a rotated key would invalidate every live subscription).
	vapidMu.Lock()
	vapidCache = nil
	vapidCacheKey = ""
	vapidMu.Unlock()
	keys2, err := vapidKeys()
	if err != nil {
		t.Fatalf("vapidKeys reload: %v", err)
	}
	if keys1 != keys2 {
		t.Fatalf("VAPID keys changed across reload: %#v vs %#v", keys1, keys2)
	}
}

// Delivery resolves recipients, honours the per-kind + only-when-away prefs,
// carries the bell deep-link payload, and prunes on a 410.
func TestDeliverWebPushRoutingAndFilters(t *testing.T) {
	setupPushTestEnv(t)
	t.Setenv("WEB_PUSH_VAPID_PUBLIC_KEY", "test-public-key")
	t.Setenv("WEB_PUSH_VAPID_PRIVATE_KEY", "test-private-key")

	ajSub := testPushSubscription("aj@shareability.com", "https://push.example/aj")
	tomSub := testPushSubscription("tom@shareability.com", "https://push.example/tom")
	if err := upsertPushSubscription(ajSub); err != nil {
		t.Fatalf("upsert aj: %v", err)
	}
	if err := upsertPushSubscription(tomSub); err != nil {
		t.Fatalf("upsert tom: %v", err)
	}

	// Broadcast fans out to every subscriber, and the payload is the bell
	// deep-link contract.
	captured := stubWebPush(t, nil)
	broadcast := notificationRecord{ID: "notification-1", Kind: notificationKindChat, Text: "AJ posted in #warroom"}
	deliverWebPushForRecord(broadcast)
	sends := captured()
	if len(sends) != 2 {
		t.Fatalf("broadcast sends=%d, want 2 (aj + tom)", len(sends))
	}
	got := sends[0]
	if got.payload["tag"] != "notification-1" {
		t.Fatalf("tag=%v, want the record id", got.payload["tag"])
	}
	if got.payload["url"] != "/?bell=notification-1" {
		t.Fatalf("url=%v, want /?bell=<id>", got.payload["url"])
	}
	if got.payload["title"] != osNotificationEventTitle(broadcast) {
		t.Fatalf("title=%v, want the body-free kind title %q", got.payload["title"], osNotificationEventTitle(broadcast))
	}
	if got.payload["body"] != broadcast.Text {
		t.Fatalf("body=%v, want the record text", got.payload["body"])
	}

	// Targeted record reaches only its recipient.
	captured = stubWebPush(t, nil)
	deliverWebPushForRecord(notificationRecord{ID: "notification-2", Kind: notificationKindTask, Text: "just for tom", UserEmail: "tom@shareability.com"})
	sends = captured()
	if len(sends) != 1 || sends[0].endpoint != tomSub.Endpoint {
		t.Fatalf("targeted sends=%#v, want only tom", sends)
	}

	// A kind toggled off suppresses that kind for that user.
	if err := setPushPrefs("tom@shareability.com", prefsFromRequest(map[string]bool{"chat": false}, true)); err != nil {
		t.Fatalf("setPushPrefs: %v", err)
	}
	captured = stubWebPush(t, nil)
	deliverWebPushForRecord(notificationRecord{ID: "notification-3", Kind: notificationKindChat, Text: "chat again"})
	sends = captured()
	if len(sends) != 1 || sends[0].endpoint != ajSub.Endpoint {
		t.Fatalf("kind-filtered sends=%#v, want only aj (tom muted chat)", sends)
	}

	// only-when-away: a present account (live socket) is skipped.
	withLiveKanbanSocket(t, "aj@shareability.com")
	captured = stubWebPush(t, nil)
	deliverWebPushForRecord(notificationRecord{ID: "notification-4", Kind: notificationKindAlert, Text: "alert"})
	sends = captured()
	for _, send := range sends {
		if send.endpoint == ajSub.Endpoint {
			t.Fatalf("aj is present (only-when-away) but still got a push: %#v", sends)
		}
	}

	// A 410 Gone answer prunes the dead subscription.
	captured = stubWebPush(t, func(endpoint string) int {
		if endpoint == tomSub.Endpoint {
			return http.StatusGone
		}
		return http.StatusCreated
	})
	deliverWebPushForRecord(notificationRecord{ID: "notification-5", Kind: notificationKindInfo, Text: "info"})
	_ = captured()
	after := snapshotPushStore()
	for _, sub := range after.Subscriptions {
		if sub.Endpoint == tomSub.Endpoint {
			t.Fatalf("410 subscription was not pruned: %#v", after.Subscriptions)
		}
	}
}

// The deferred flush is the delivery moment: a queued notification must NOT
// push when created, and must push when flushed.
func TestDeferredFlushTriggersPush(t *testing.T) {
	setupPushTestEnv(t)
	t.Setenv("WEB_PUSH_VAPID_PUBLIC_KEY", "test-public-key")
	t.Setenv("WEB_PUSH_VAPID_PRIVATE_KEY", "test-private-key")
	app := newIsolatedKanbanBoardApp(t)

	if err := upsertPushSubscription(testPushSubscription("aj@shareability.com", "https://push.example/aj")); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	sends := make(chan map[string]any, 4)
	previous := sendWebPush
	sendWebPush = func(payload []byte, _ *webpush.Subscription, _ *webpush.Options) (*http.Response, error) {
		var decoded map[string]any
		_ = json.Unmarshal(payload, &decoded)
		sends <- decoded
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	t.Cleanup(func() { sendWebPush = previous })

	if _, err := app.createNotification("aj@shareability.com", "task", "after the meeting", "", "", "", true); err != nil {
		t.Fatalf("create deferred: %v", err)
	}
	// Queued: no push yet.
	select {
	case got := <-sends:
		t.Fatalf("deferred notification pushed on create, not on flush: %#v", got)
	case <-time.After(150 * time.Millisecond):
	}

	if flushed := app.flushDeferredNotifications("test"); flushed != 1 {
		t.Fatalf("flushed=%d, want 1", flushed)
	}
	select {
	case got := <-sends:
		if got["body"] != "after the meeting" {
			t.Fatalf("flushed push body=%v, want the queued text", got["body"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected a push when the deferred queue flushed")
	}
}

// The service worker is served from the origin root as no-store JavaScript.
func TestServiceWorkerRouteServesNoStoreJavaScript(t *testing.T) {
	recorder := httptest.NewRecorder()
	serviceWorkerHandler(recorder, httptest.NewRequest(http.MethodGet, "/sw.js", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("/sw.js status=%d, want 200", recorder.Code)
	}
	if ct := recorder.Header().Get("Content-Type"); !strings.Contains(ct, "text/javascript") {
		t.Fatalf("/sw.js content-type=%q, want text/javascript", ct)
	}
	if cc := recorder.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("/sw.js cache-control=%q, want no-store", cc)
	}
	if !strings.Contains(recorder.Body.String(), "showNotification") {
		t.Fatal("/sw.js body did not serve the worker (no showNotification)")
	}

	// The '/' catch-all still 404s unknown API paths (unaffected by /sw.js).
	if shouldServeIndexHTML(httptest.NewRequest(http.MethodGet, "/assistant/push/does-not-exist", nil)) {
		t.Fatal("unknown /assistant/* path must not fall through to index.html")
	}
}
