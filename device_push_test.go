package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func liveDeviceTokenRecordForTest(t *testing.T, email, token string) (string, deviceTokenRecord) {
	t.Helper()
	sessionToken, err := userSessionStore().create(email)
	if err != nil {
		t.Fatalf("create member session: %v", err)
	}
	return sessionToken, deviceTokenRecord{
		TenantID: canonicalTenantID(), UserEmail: email, Token: token, Platform: "ios",
		SessionHash: hashResetToken(sessionToken),
	}
}

func upsertLiveDeviceTokenForTest(t *testing.T, email, token string) string {
	t.Helper()
	sessionToken, record := liveDeviceTokenRecordForTest(t, email, token)
	if err := upsertDeviceToken(record); err != nil {
		t.Fatalf("register live device: %v", err)
	}
	return sessionToken
}

func assertNoDevicePushDeliveryForTest(t *testing.T, record notificationRecord) {
	t.Helper()
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	previous := expoPushSendURL
	expoPushSendURL = server.URL
	defer func() { expoPushSendURL = previous }()
	deliverDevicePushForRecord(record)
	if hits != 0 {
		t.Fatalf("ineligible device authority emitted %d Expo requests", hits)
	}
}

// Expo rejects >100 messages per request with PUSH_TOO_MANY_NOTIFICATIONS —
// the whole request fails, not just the overflow, so batching is not optional.
func TestExpoPushBatchesAtOneHundred(t *testing.T) {
	tokens := make([]string, 250)
	for index := range tokens {
		tokens[index] = "ExponentPushToken[t]"
	}
	batches := chunkExpoPushMessages(tokens, expoPushMaxBatch)
	if len(batches) != 3 {
		t.Fatalf("batches = %d, want 3", len(batches))
	}
	if len(batches[0]) != 100 || len(batches[1]) != 100 || len(batches[2]) != 50 {
		t.Fatalf("bad batch sizes: %d, %d, %d", len(batches[0]), len(batches[1]), len(batches[2]))
	}
}

func TestExpoPushBatchingHandlesExactMultiplesAndEmpty(t *testing.T) {
	if got := len(chunkExpoPushMessages(nil, 100)); got != 0 {
		t.Fatalf("empty batches = %d, want 0", got)
	}
	tokens := make([]string, 200)
	if got := len(chunkExpoPushMessages(tokens, 100)); got != 2 {
		t.Fatalf("exact multiple batches = %d, want 2", got)
	}
}

// DeviceNotRegistered is the APNs equivalent of a VAPID 410: the token is gone
// for good and must be pruned, exactly as prunePushSubscriptions treats a dead
// endpoint. Every other error is transient and must NOT prune — pruning on a
// rate limit would silently unsubscribe a live device.
func TestDeviceNotRegisteredTokensArePruned(t *testing.T) {
	targets := []devicePushTarget{{Token: "good"}, {Token: "dead"}, {Token: "ratelimited"}}
	tickets := []expoPushTicket{
		{Status: "ok", ID: "x"},
		{Status: "error", Details: expoPushTicketDetails{Error: "DeviceNotRegistered"}},
		{Status: "error", Details: expoPushTicketDetails{Error: "MessageRateExceeded"}},
	}
	prune := applyExpoPushTickets(targets, tickets)
	if len(prune) != 1 || prune[0].Token != "dead" {
		t.Fatalf("prune = %v, want [dead]", prune)
	}
}

// Expo returns one ticket per message, but a truncated or malformed response is
// a network reality. Mis-attributing an error to the wrong token would prune a
// live device, so a short array must simply stop rather than index past.
func TestApplyExpoPushTicketsToleratesShortResponses(t *testing.T) {
	if prune := applyExpoPushTickets([]devicePushTarget{{Token: "a"}, {Token: "b"}}, []expoPushTicket{{Status: "ok"}}); len(prune) != 0 {
		t.Fatalf("prune = %v, want empty", prune)
	}
	if prune := applyExpoPushTickets(nil, []expoPushTicket{{Status: "error"}}); len(prune) != 0 {
		t.Fatalf("prune = %v, want empty", prune)
	}
}

func TestDeviceTokensRoundTripAndDeduplicate(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))

	_, record := liveDeviceTokenRecordForTest(t, "aj@shareability.com", "ExponentPushToken[a]")
	if err := upsertDeviceToken(record); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Re-registering the same token (every cold start does this) must not
	// accumulate duplicates, or one message fans out N times to one phone.
	if err := upsertDeviceToken(record); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}

	tokens := snapshotDeviceTokenStore().Tokens
	if len(tokens) != 1 {
		t.Fatalf("tokens = %d, want 1", len(tokens))
	}
	if tokens[0].UserEmail != "aj@shareability.com" {
		t.Fatalf("email = %q, want normalized", tokens[0].UserEmail)
	}
}

// A token left registered after logout pushes one account's messages to
// whoever signs in next on that device.
func TestRemoveDeviceTokenUnregistersOnLogout(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))

	_, record := liveDeviceTokenRecordForTest(t, "aj@shareability.com", "ExponentPushToken[remove]")
	if err := upsertDeviceToken(record); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := removeDeviceTokenBinding(record.TenantID, record.UserEmail, record.Token, record.SessionHash); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := len(snapshotDeviceTokenStore().Tokens); got != 0 {
		t.Fatalf("tokens = %d, want 0", got)
	}
}

func TestDeviceRecipientMatchesBroadcastAndTargeted(t *testing.T) {
	token := deviceTokenRecord{UserEmail: "aj@x.com", Token: "tok"}

	broadcast := notificationRecord{Kind: notificationKindChat}
	if !deviceRecipientMatches(broadcast, token) {
		t.Fatal("broadcast did not reach a subscriber")
	}

	mine := notificationRecord{Kind: notificationKindChat, UserEmail: "aj@x.com"}
	if !deviceRecipientMatches(mine, token) {
		t.Fatal("targeted record did not reach its recipient")
	}

	theirs := notificationRecord{Kind: notificationKindChat, UserEmail: "dana@x.com"}
	if deviceRecipientMatches(theirs, token) {
		t.Fatal("targeted record leaked to the wrong recipient")
	}
}

// The device lane deliberately does NOT honour OnlyWhenAway.
//
// That pref exists so a phone doesn't double-buzz what an open session already
// surfaced — correct for WEB push, where the subscription and the open session
// are the same device. A native phone is a different device in a different
// place: an open browser tab on a laptop must not silence a locked iPhone, or
// the product's central promise ("your team reaches you") fails exactly when
// you are away from the desk the tab is on.
func TestDeviceLaneIgnoresOnlyWhenAway(t *testing.T) {
	if deviceLaneHonorsOnlyWhenAway {
		t.Fatal("the device lane must not suppress on an open web session")
	}
}

// Body text is what makes a chat notification useful — a banner reading
// "New message" is worse than no banner. This is NOT the "titles only"
// boundary that pushNotificationRecordOS enforces: that guards the in-app OS
// event stream, a different surface with a different threat model.
func TestExpoPushMessagesCarryTheBody(t *testing.T) {
	record := notificationRecord{
		ID:       "notification-1",
		Kind:     notificationKindChat,
		Text:     "Dana: pushed the pricing memo",
		ThreadID: "table-1",
	}
	messages := expoPushMessagesFor(record, []string{"tok"})
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if !strings.Contains(messages[0].Body, "pricing memo") {
		t.Fatalf("body = %q, want the message text", messages[0].Body)
	}
	if messages[0].Data["notificationId"] != "notification-1" {
		t.Fatalf("data = %v, want notificationId", messages[0].Data)
	}
	// The deep link target: a notification is a request to see ONE thing, so
	// the payload must name the thread or the tap lands on the canvas.
	if messages[0].Data["threadId"] != "table-1" {
		t.Fatalf("data = %v, want threadId", messages[0].Data)
	}
}

func TestExpoPushMessagesSkipEmptyText(t *testing.T) {
	record := notificationRecord{Kind: notificationKindChat, Text: "   "}
	if got := len(expoPushMessagesFor(record, []string{"tok"})); got != 0 {
		t.Fatalf("messages = %d, want 0 for an empty body", got)
	}
}

func TestPushDeviceRegistrationPersistsOnlyExactSessionHashAndDeliversWhileLive(t *testing.T) {
	setupAuthTestEnv(t)
	storePath := filepath.Join(t.TempDir(), "devices.json")
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", storePath)
	sessionToken, err := userSessionStore().create("aj@shareability.com")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/assistant/push/devices", strings.NewReader(`{"token":"ExponentPushToken[live]","platform":"ios"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+sessionToken)
	recorder := httptest.NewRecorder()
	pushDevicesHandler(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("register status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	state := snapshotDeviceTokenStore()
	if len(state.Tokens) != 1 {
		t.Fatalf("registrations=%+v, want one", state.Tokens)
	}
	registered := state.Tokens[0]
	if registered.TenantID != canonicalTenantID() || registered.UserEmail != "aj@shareability.com" || registered.SessionHash != hashResetToken(sessionToken) {
		t.Fatalf("registration=%+v, want exact tenant/account/session authority", registered)
	}
	raw, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(sessionToken)) {
		t.Fatal("raw bearer session token was persisted in the device store")
	}
	rawTokenReq := httptest.NewRequest(http.MethodPost, "/assistant/push/devices",
		strings.NewReader(`{"token":"`+sessionToken+`","platform":"ios"}`))
	rawTokenReq.Header.Set("Content-Type", "application/json")
	rawTokenReq.Header.Set("Authorization", "Bearer "+sessionToken)
	rawTokenRecorder := httptest.NewRecorder()
	pushDevicesHandler(rawTokenRecorder, rawTokenReq)
	if rawTokenRecorder.Code != http.StatusBadRequest {
		t.Fatalf("raw session as device token status=%d, want 400", rawTokenRecorder.Code)
	}
	raw, err = os.ReadFile(storePath)
	if err != nil || bytes.Contains(raw, []byte(sessionToken)) {
		t.Fatalf("raw bearer reached device store after rejected registration: err=%v", err)
	}
	targets := deviceTargetsForRecord(notificationRecord{Kind: notificationKindChat, Text: "live"})
	if len(targets) != 1 || targets[0].Token != registered.Token || targets[0].SessionHash != registered.SessionHash {
		t.Fatalf("targets=%+v, want the live exact binding", targets)
	}
}

func TestNativePushRegistrationPrefersBearerOverConflictingCookie(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))

	bearerToken, err := userSessionStore().create("aj@shareability.com")
	if err != nil {
		t.Fatal(err)
	}
	cookieToken, err := userSessionStore().create("tim@shareability.com")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/assistant/push/devices",
		strings.NewReader(`{"token":"ExponentPushToken[native-authority]","platform":"ios"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Bonfire-Client", "expo")
	request.Header.Set("Authorization", "Bearer "+bearerToken)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookieToken})
	recorder := httptest.NewRecorder()
	pushDevicesHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("registration status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	state := snapshotDeviceTokenStore()
	if len(state.Tokens) != 1 {
		t.Fatalf("registrations=%+v, want one", state.Tokens)
	}
	record := state.Tokens[0]
	if record.UserEmail != "aj@shareability.com" || record.SessionHash != hashResetToken(bearerToken) {
		t.Fatalf("native registration=%+v, want exact bearer account/session", record)
	}
	if record.UserEmail == "tim@shareability.com" || record.SessionHash == hashResetToken(cookieToken) {
		t.Fatalf("ambient cookie incorrectly became push authority: %+v", record)
	}
}

func TestPushTargetResolutionRejectsExpiredDestroyedLegacyAndWrongAuthority(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, sessionToken string, record deviceTokenRecord)
	}{
		{name: "expired", mutate: func(t *testing.T, sessionToken string, _ deviceTokenRecord) {
			store := userSessionStore()
			store.mu.Lock()
			record := store.sessions[hashResetToken(sessionToken)]
			record.Expires = time.Now().UTC().Add(-time.Second)
			store.sessions[hashResetToken(sessionToken)] = record
			store.mu.Unlock()
		}},
		{name: "destroyed", mutate: func(_ *testing.T, sessionToken string, _ deviceTokenRecord) {
			userSessionStore().destroy(sessionToken)
		}},
		{name: "legacy unbound", mutate: func(t *testing.T, _ string, record deviceTokenRecord) {
			if err := mutateDeviceTokenStore(func(state *deviceTokenStoreData) {
				state.Tokens[0].SessionHash = ""
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "wrong account", mutate: func(t *testing.T, _ string, _ deviceTokenRecord) {
			if err := mutateDeviceTokenStore(func(state *deviceTokenStoreData) {
				state.Tokens[0].UserEmail = "tim@shareability.com"
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "wrong tenant", mutate: func(t *testing.T, _ string, _ deviceTokenRecord) {
			if err := mutateDeviceTokenStore(func(state *deviceTokenStoreData) {
				state.Tokens[0].TenantID = "other-tenant"
			}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			setupAuthTestEnv(t)
			t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))
			sessionToken, record := liveDeviceTokenRecordForTest(t, "aj@shareability.com", "ExponentPushToken[authority]")
			if err := upsertDeviceToken(record); err != nil {
				t.Fatal(err)
			}
			test.mutate(t, sessionToken, record)
			if targets := deviceTargetsForRecord(notificationRecord{Kind: notificationKindChat, Text: "private"}); len(targets) != 0 {
				t.Fatalf("ineligible authority resolved targets=%+v", targets)
			}
			assertNoDevicePushDeliveryForTest(t, notificationRecord{Kind: notificationKindChat, Text: "private"})
		})
	}
}

func TestPushBindingBecomesIneligibleWhenDeleteReturnsUnauthorized(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))
	sessionToken := upsertLiveDeviceTokenForTest(t, "aj@shareability.com", "ExponentPushToken[logout-race]")
	userSessionStore().destroy(sessionToken)
	req := httptest.NewRequest(http.MethodDelete, "/assistant/push/devices", strings.NewReader(`{"token":"ExponentPushToken[logout-race]"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+sessionToken)
	recorder := httptest.NewRecorder()
	pushDevicesHandler(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("delete status=%d, want 401 for destroyed session", recorder.Code)
	}
	if len(snapshotDeviceTokenStore().Tokens) != 1 {
		t.Fatal("test precondition lost: failed DELETE should leave the stale record")
	}
	if targets := deviceTargetsForRecord(notificationRecord{Kind: notificationKindChat, Text: "must not leak"}); len(targets) != 0 {
		t.Fatalf("destroyed binding remained eligible after failed DELETE: %+v", targets)
	}
	assertNoDevicePushDeliveryForTest(t, notificationRecord{Kind: notificationKindChat, Text: "must not leak"})
}

func TestPasswordRotationImmediatelyInvalidatesExistingPushBinding(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	var sessionToken string
	for _, cookie := range cookies {
		if cookie.Name == sessionCookieName {
			sessionToken = cookie.Value
		}
	}
	if sessionToken == "" {
		t.Fatal("member session cookie missing")
	}
	if err := upsertDeviceToken(deviceTokenRecord{TenantID: canonicalTenantID(), UserEmail: "aj@shareability.com",
		Token: "ExponentPushToken[password]", Platform: "ios", SessionHash: hashResetToken(sessionToken)}); err != nil {
		t.Fatal(err)
	}
	rotated := postAuthJSON(t, "/auth/change-password", `{"currentPassword":"B0NFIRE!","newPassword":"New-Pass-123!"}`, cookies)
	if rotated.Code != http.StatusOK {
		t.Fatalf("password rotation status=%d body=%s", rotated.Code, rotated.Body.String())
	}
	if len(snapshotDeviceTokenStore().Tokens) != 1 {
		t.Fatal("rotation should preserve the inert record for fail-closed re-registration")
	}
	if targets := deviceTargetsForRecord(notificationRecord{Kind: notificationKindChat, Text: "old session"}); len(targets) != 0 {
		t.Fatalf("password-rotated binding remained eligible: %+v", targets)
	}
	assertNoDevicePushDeliveryForTest(t, notificationRecord{Kind: notificationKindChat, Text: "old session"})
}

func TestLegacyDeviceStoreIsPreservedButInertUntilFailClosedReregistration(t *testing.T) {
	setupAuthTestEnv(t)
	path := filepath.Join(t.TempDir(), "devices.json")
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", path)
	legacy := `{"tokens":[{"userEmail":"aj@shareability.com","token":"ExponentPushToken[legacy]","platform":"ios","createdAt":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	state := snapshotDeviceTokenStore()
	if len(state.Tokens) != 1 || state.Tokens[0].SessionHash != "" {
		t.Fatalf("legacy store was not loaded compatibly: %+v", state.Tokens)
	}
	assertNoDevicePushDeliveryForTest(t, notificationRecord{Kind: notificationKindChat, Text: "legacy must not receive"})
	upsertLiveDeviceTokenForTest(t, "aj@shareability.com", "ExponentPushToken[legacy]")
	state = snapshotDeviceTokenStore()
	if len(state.Tokens) != 1 || state.Tokens[0].SessionHash == "" || state.Tokens[0].TenantID != canonicalTenantID() {
		t.Fatalf("re-registration did not replace the legacy token binding: %+v", state.Tokens)
	}
}

func TestDelayedPruneAndOldSessionDeleteCannotRemoveReboundToken(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))
	_, oldRecord := liveDeviceTokenRecordForTest(t, "aj@shareability.com", "ExponentPushToken[shared]")
	if err := upsertDeviceToken(oldRecord); err != nil {
		t.Fatal(err)
	}
	oldTargets := deviceTargetsForRecord(notificationRecord{Kind: notificationKindChat, Text: "AJ only", UserEmail: oldRecord.UserEmail})
	if len(oldTargets) != 1 {
		t.Fatalf("old targets=%+v", oldTargets)
	}
	_, rebound := liveDeviceTokenRecordForTest(t, "tim@shareability.com", oldRecord.Token)
	if err := upsertDeviceToken(rebound); err != nil {
		t.Fatal(err)
	}
	pruneDeviceTokenBindings(oldTargets)
	if err := removeDeviceTokenBinding(oldRecord.TenantID, oldRecord.UserEmail, oldRecord.Token, oldRecord.SessionHash); err != nil {
		t.Fatal(err)
	}
	state := snapshotDeviceTokenStore()
	if len(state.Tokens) != 1 || state.Tokens[0].UserEmail != rebound.UserEmail || state.Tokens[0].SessionHash != rebound.SessionHash {
		t.Fatalf("new binding was pruned by stale authority: %+v", state.Tokens)
	}
}

func TestSessionDestroyLinearizesAgainstInFlightPushDelivery(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))
	sessionToken := upsertLiveDeviceTokenForTest(t, "aj@shareability.com", "ExponentPushToken[linearized]")
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte(`{"data":[{"status":"ok","id":"sent"}]}`))
	}))
	defer server.Close()
	previous := expoPushSendURL
	expoPushSendURL = server.URL
	defer func() { expoPushSendURL = previous }()

	deliveryDone := make(chan struct{})
	go func() {
		deliverDevicePushForRecord(notificationRecord{Kind: notificationKindChat, Text: "authorized before logout"})
		close(deliveryDone)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("push delivery never reached the authority-linearized send")
	}
	destroyDone := make(chan struct{})
	go func() {
		userSessionStore().destroy(sessionToken)
		close(destroyDone)
	}()
	select {
	case <-destroyDone:
		t.Fatal("session destroy returned while an authority-approved send was still in flight")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	for name, done := range map[string]<-chan struct{}{"delivery": deliveryDone, "destroy": destroyDone} {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for %s completion", name)
		}
	}
	if _, ok := userSessionStore().lookup(sessionToken); ok {
		t.Fatal("session survived linearized destroy")
	}

	// After destroy returns the same binding is immediately inert.
	assertNoDevicePushDeliveryForTest(t, notificationRecord{Kind: notificationKindChat, Text: "after logout"})
}

func TestDelayedAuthenticatedRegistrationCannotOverwriteCurrentBindingAfterRevocation(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))
	deviceToken := "ExponentPushToken[stale-register]"
	sessionA, staleRecord := liveDeviceTokenRecordForTest(t, "aj@shareability.com", deviceToken)

	// Request A resolves authentication while its session is still live, then
	// pauses before the store call.
	requestA := httptest.NewRequest(http.MethodPost, "/assistant/push/devices", nil)
	requestA.Header.Set("Authorization", "Bearer "+sessionA)
	resolvedA := userFromRequest(requestA)
	if resolvedA == nil || resolvedA.Email != staleRecord.UserEmail {
		t.Fatal("request A did not resolve its live member before the race")
	}

	// Logout/revocation linearizes first, then the same physical token becomes
	// current under B. Delayed A must not be able to replace B using its stale
	// pre-logout identity result.
	userSessionStore().destroy(sessionA)
	_, currentRecord := liveDeviceTokenRecordForTest(t, "tim@shareability.com", deviceToken)
	if err := upsertDeviceToken(currentRecord); err != nil {
		t.Fatal(err)
	}
	if err := upsertDeviceToken(staleRecord); err == nil {
		t.Fatal("delayed request A inserted after its session was revoked")
	}
	state := snapshotDeviceTokenStore()
	if len(state.Tokens) != 1 || state.Tokens[0].UserEmail != currentRecord.UserEmail || state.Tokens[0].SessionHash != currentRecord.SessionHash {
		t.Fatalf("stale request A displaced current binding B: %+v", state.Tokens)
	}
	if targets := deviceTargetsForRecord(notificationRecord{Kind: notificationKindChat, Text: "private A", UserEmail: staleRecord.UserEmail}); len(targets) != 0 {
		t.Fatalf("revoked A became a delivery target: %+v", targets)
	}
	targets := deviceTargetsForRecord(notificationRecord{Kind: notificationKindChat, Text: "private B", UserEmail: currentRecord.UserEmail})
	if len(targets) != 1 || targets[0].SessionHash != currentRecord.SessionHash {
		t.Fatalf("current binding B was not preserved: %+v", targets)
	}
}
