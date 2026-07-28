package main

import (
	"path/filepath"
	"strings"
	"testing"
)

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
	tokens := []string{"good", "dead", "ratelimited"}
	tickets := []expoPushTicket{
		{Status: "ok", ID: "x"},
		{Status: "error", Details: expoPushTicketDetails{Error: "DeviceNotRegistered"}},
		{Status: "error", Details: expoPushTicketDetails{Error: "MessageRateExceeded"}},
	}
	prune := applyExpoPushTickets(tokens, tickets)
	if len(prune) != 1 || prune[0] != "dead" {
		t.Fatalf("prune = %v, want [dead]", prune)
	}
}

// Expo returns one ticket per message, but a truncated or malformed response is
// a network reality. Mis-attributing an error to the wrong token would prune a
// live device, so a short array must simply stop rather than index past.
func TestApplyExpoPushTicketsToleratesShortResponses(t *testing.T) {
	if prune := applyExpoPushTickets([]string{"a", "b"}, []expoPushTicket{{Status: "ok"}}); len(prune) != 0 {
		t.Fatalf("prune = %v, want empty", prune)
	}
	if prune := applyExpoPushTickets(nil, []expoPushTicket{{Status: "error"}}); len(prune) != 0 {
		t.Fatalf("prune = %v, want empty", prune)
	}
}

func TestDeviceTokensRoundTripAndDeduplicate(t *testing.T) {
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))

	record := deviceTokenRecord{UserEmail: "AJ@X.com", Token: "ExponentPushToken[a]", Platform: "ios"}
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
	if tokens[0].UserEmail != "aj@x.com" {
		t.Fatalf("email = %q, want normalized", tokens[0].UserEmail)
	}
}

// A token left registered after logout pushes one account's messages to
// whoever signs in next on that device.
func TestRemoveDeviceTokenUnregistersOnLogout(t *testing.T) {
	t.Setenv("DEVICE_PUSH_TOKENS_PATH", filepath.Join(t.TempDir(), "devices.json"))

	if err := upsertDeviceToken(deviceTokenRecord{UserEmail: "aj@x.com", Token: "tok"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := removeDeviceToken("", "aj@x.com", "tok"); err != nil {
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
