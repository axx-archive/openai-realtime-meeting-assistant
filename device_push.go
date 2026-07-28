package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Native push — design §8 of docs/plans/the-table-design.md.
//
// The mobile app had NO push of any kind: no expo-notifications dependency, no
// APNs path, nothing. Delivery was a websocket that only lived while the app
// was open, which makes "replaces the team's iPhone group thread" false on day
// one — iMessage's defining property is that it reaches you.
//
// This is a SIBLING to web_push.go, not a rebuild. Per-recipient filtering is
// transport-agnostic and already correct; duplicating it is how two lanes drift
// apart and start disagreeing about who gets what.

// deviceLaneHonorsOnlyWhenAway is false, deliberately, and is a constant so the
// decision is testable rather than implied by an absent line of code.
//
// The OnlyWhenAway pref exists so a phone does not double-buzz what an open
// session already surfaced. That is right for WEB push, where the subscription
// and the open session are the same device. A native phone is a different
// device in a different place: an open browser tab on a laptop must not silence
// a locked iPhone, or the product's central promise fails exactly when you are
// away from the desk that tab is on.
const deviceLaneHonorsOnlyWhenAway = false

// Expo's documented per-request ceiling. Exceeding it fails the WHOLE request
// with PUSH_TOO_MANY_NOTIFICATIONS — not a partial send — so batching is a
// correctness requirement, not an optimization.
const expoPushMaxBatch = 100

// expoPushSendURL is a var, not a const, so the delivery path can be exercised
// end-to-end against a stub in tests. Everything between "a teammate posts" and
// "bytes on the wire to Expo" is then verifiable without a device; only APNs
// itself is not.
var expoPushSendURL = "https://exp.host/--/api/v2/push/send"

type deviceTokenRecord struct {
	TenantID  string `json:"tenantId,omitempty"`
	UserEmail string `json:"userEmail"`
	Token     string `json:"token"`
	Platform  string `json:"platform,omitempty"`
	CreatedAt string `json:"createdAt"`
}

type deviceTokenStoreData struct {
	Tokens    []deviceTokenRecord `json:"tokens"`
	UpdatedAt string              `json:"updatedAt,omitempty"`
}

type expoPushMessage struct {
	To    string         `json:"to"`
	Title string         `json:"title,omitempty"`
	Body  string         `json:"body"`
	Data  map[string]any `json:"data,omitempty"`
	Sound string         `json:"sound,omitempty"`
	Badge *int           `json:"badge,omitempty"`
}

type expoPushTicketDetails struct {
	Error string `json:"error"`
}

type expoPushTicket struct {
	Status  string                `json:"status"`
	ID      string                `json:"id"`
	Message string                `json:"message"`
	Details expoPushTicketDetails `json:"details"`
}

type expoPushResponse struct {
	Data []expoPushTicket `json:"data"`
}

var deviceTokenStoreMu sync.Mutex

func deviceTokensPath() string {
	if path := strings.TrimSpace(os.Getenv("DEVICE_PUSH_TOKENS_PATH")); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "device-push-tokens.json")
}

// loadDeviceTokenStoreFile reads the store off disk. A missing/empty/corrupt
// file is a clean empty store — push is best-effort and must never wedge a
// notification. Callers hold deviceTokenStoreMu (or accept a snapshot).
func loadDeviceTokenStoreFile() deviceTokenStoreData {
	raw, err := os.ReadFile(deviceTokensPath())
	if err != nil {
		return deviceTokenStoreData{}
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return deviceTokenStoreData{}
	}
	var state deviceTokenStoreData
	if err := json.Unmarshal(raw, &state); err != nil {
		log.Errorf("Failed to decode device push tokens: %v", err)
		return deviceTokenStoreData{}
	}
	for index := range state.Tokens {
		state.Tokens[index].TenantID = strings.TrimSpace(state.Tokens[index].TenantID)
		state.Tokens[index].UserEmail = normalizeAccountEmail(state.Tokens[index].UserEmail)
		state.Tokens[index].Token = strings.TrimSpace(state.Tokens[index].Token)
	}
	return state
}

func mutateDeviceTokenStore(fn func(*deviceTokenStoreData)) error {
	deviceTokenStoreMu.Lock()
	defer deviceTokenStoreMu.Unlock()
	state := loadDeviceTokenStoreFile()
	fn(&state)
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return writeJSONFileAtomically(deviceTokensPath(), "device push tokens", state)
}

func snapshotDeviceTokenStore() deviceTokenStoreData {
	deviceTokenStoreMu.Lock()
	defer deviceTokenStoreMu.Unlock()
	return loadDeviceTokenStoreFile()
}

// upsertDeviceToken binds a token to an account, keyed by token the way
// pushSubscriptionRecord is keyed by endpoint.
//
// Keying on the token (not the account) is what makes re-registration safe:
// the client registers on every cold start, and appending blindly would fan one
// message out N times to a single phone.
func upsertDeviceToken(record deviceTokenRecord) error {
	record.TenantID = strings.TrimSpace(record.TenantID)
	record.UserEmail = normalizeAccountEmail(record.UserEmail)
	record.Token = strings.TrimSpace(record.Token)
	if record.Token == "" || record.UserEmail == "" {
		return nil
	}
	if record.CreatedAt == "" {
		record.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	return mutateDeviceTokenStore(func(state *deviceTokenStoreData) {
		for index, existing := range state.Tokens {
			if existing.Token != record.Token {
				continue
			}
			// A token that moved to a different account replaces the old
			// binding outright — otherwise the previous user's messages keep
			// arriving on a phone someone else has signed into.
			state.Tokens[index] = record
			return
		}
		state.Tokens = append(state.Tokens, record)
	})
}

func removeDeviceToken(tenantID, userEmail, token string) error {
	tenantID = strings.TrimSpace(tenantID)
	userEmail = normalizeAccountEmail(userEmail)
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	return mutateDeviceTokenStore(func(state *deviceTokenStoreData) {
		kept := state.Tokens[:0]
		for _, existing := range state.Tokens {
			ownerMatches := userEmail == "" || normalizeAccountEmail(existing.UserEmail) == userEmail
			tenantMatches := tenantID == "" || strings.TrimSpace(existing.TenantID) == tenantID
			if existing.Token == token && ownerMatches && tenantMatches {
				continue
			}
			kept = append(kept, existing)
		}
		state.Tokens = kept
	})
}

// pruneDeviceTokens drops tokens Expo has reported as permanently gone.
func pruneDeviceTokens(tokens []string) {
	if len(tokens) == 0 {
		return
	}
	dead := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		dead[strings.TrimSpace(token)] = struct{}{}
	}
	err := mutateDeviceTokenStore(func(state *deviceTokenStoreData) {
		kept := state.Tokens[:0]
		for _, existing := range state.Tokens {
			if _, gone := dead[existing.Token]; gone {
				continue
			}
			kept = append(kept, existing)
		}
		state.Tokens = kept
	})
	if err != nil {
		// Log-and-continue, like prunePushSubscriptions: a failed prune just
		// means we retry a dead token next time, which is harmless.
		log.Errorf("Failed to prune device push tokens: %v", err)
	}
}

// deviceRecipientMatches mirrors pushRecipientMatches for a device token:
// broadcast reaches every subscriber, a targeted record reaches only its
// recipient, and tenant binding is respected.
func deviceRecipientMatches(record notificationRecord, token deviceTokenRecord) bool {
	if !notificationTenantMatches(record, token.TenantID) {
		return false
	}
	userEmail := normalizeAccountEmail(token.UserEmail)
	return !notificationExcludedForUser(record, userEmail) &&
		(record.UserEmail == "" || record.UserEmail == userEmail)
}

// chunkExpoPushMessages splits tokens into Expo-sized batches.
func chunkExpoPushMessages(tokens []string, size int) [][]string {
	if size <= 0 || len(tokens) == 0 {
		return nil
	}
	batches := make([][]string, 0, (len(tokens)+size-1)/size)
	for start := 0; start < len(tokens); start += size {
		end := start + size
		if end > len(tokens) {
			end = len(tokens)
		}
		batches = append(batches, tokens[start:end])
	}
	return batches
}

// expoPushMessagesFor builds one message per token.
//
// The body carries the message text. A chat banner reading "New message" is
// worse than no banner — the whole reason a phone buzzes is so you can decide
// whether to look. Note this is NOT the "titles only" boundary that
// pushNotificationRecordOS enforces: that guards the in-app OS event stream, a
// different surface with a different threat model.
func expoPushMessagesFor(record notificationRecord, tokens []string) []expoPushMessage {
	body := strings.TrimSpace(record.Text)
	if body == "" || len(tokens) == 0 {
		return nil
	}

	data := map[string]any{"notificationId": record.ID}
	if threadID := strings.TrimSpace(record.ThreadID); threadID != "" {
		// A notification is a request to see ONE thing. Without a thread the
		// tap lands on the canvas and the user has to navigate twice.
		data["threadId"] = threadID
	}
	if messageID := strings.TrimSpace(record.MessageID); messageID != "" {
		data["messageId"] = messageID
	}
	if threadName := strings.TrimSpace(record.ThreadName); threadName != "" {
		data["threadName"] = threadName
	}
	if tool := strings.TrimSpace(record.Tool); tool != "" {
		data["tool"] = tool
	}

	messages := make([]expoPushMessage, 0, len(tokens))
	for _, token := range tokens {
		messages = append(messages, expoPushMessage{
			To:    token,
			Body:  body,
			Data:  data,
			Sound: "default",
		})
	}
	return messages
}

// applyExpoPushTickets returns the tokens Expo says are permanently gone.
//
// DeviceNotRegistered is the APNs equivalent of a VAPID 410. Every other error
// is transient: pruning on a rate limit would silently unsubscribe a live
// device, which is a far worse failure than one retried message.
//
// A short or malformed ticket array simply stops. Mis-attributing an error to
// the wrong token would prune the wrong phone.
func applyExpoPushTickets(tokens []string, tickets []expoPushTicket) []string {
	prune := []string{}
	for index, ticket := range tickets {
		if index >= len(tokens) {
			break
		}
		if !strings.EqualFold(ticket.Status, "error") {
			continue
		}
		if ticket.Details.Error == "DeviceNotRegistered" {
			prune = append(prune, tokens[index])
		}
	}
	return prune
}

// deviceTargetsForRecord resolves which tokens should receive a record.
func deviceTargetsForRecord(record notificationRecord) []string {
	state := snapshotDeviceTokenStore()
	if len(state.Tokens) == 0 {
		return nil
	}
	pushState := snapshotPushStore()

	tokens := make([]string, 0, len(state.Tokens))
	for _, token := range state.Tokens {
		if !deviceRecipientMatches(record, token) {
			continue
		}
		// Kind preferences ARE shared with web push — "mute agent chatter"
		// should mean the same thing on both surfaces.
		prefs := resolvePushPrefs(pushState, token.UserEmail)
		if !prefs.Kinds[record.Kind] {
			continue
		}
		if deviceLaneHonorsOnlyWhenAway && prefs.OnlyWhenAway && userHasLiveKanbanSocket(token.UserEmail) {
			continue
		}
		if threadMutedForUser(token.UserEmail, record) {
			continue
		}
		tokens = append(tokens, token.Token)
	}
	return tokens
}

// sendExpoPushBatch posts one batch and returns its tickets.
func sendExpoPushBatch(ctx context.Context, messages []expoPushMessage) ([]expoPushTicket, error) {
	payload, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, expoPushSendURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if token := strings.TrimSpace(os.Getenv("EXPO_ACCESS_TOKEN")); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("Expo push returned HTTP %d", response.StatusCode)
	}

	var decoded expoPushResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	return decoded.Data, nil
}

// deliverDevicePushForRecord fans a notification out to native devices.
func deliverDevicePushForRecord(record notificationRecord) {
	if strings.TrimSpace(record.Text) == "" {
		return
	}
	tokens := deviceTargetsForRecord(record)
	if len(tokens) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dead := []string{}
	for _, batch := range chunkExpoPushMessages(tokens, expoPushMaxBatch) {
		messages := expoPushMessagesFor(record, batch)
		if len(messages) == 0 {
			continue
		}
		tickets, err := sendExpoPushBatch(ctx, messages)
		if err != nil {
			log.Errorf("Expo push batch failed: %v", err)
			continue
		}
		dead = append(dead, applyExpoPushTickets(batch, tickets)...)
	}
	pruneDeviceTokens(dead)
}

// pushNotificationRecordDevice is the fourth sibling in the notifications.go
// fan-out, alongside local, websocket, and web push.
func pushNotificationRecordDevice(record notificationRecord) {
	go deliverDevicePushForRecord(record)
}

// pushDevicesHandler registers and unregisters this device's Expo push token.
func pushDevicesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
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

	payload := struct {
		Token    string `json:"token"`
		Platform string `json:"platform"`
	}{}
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload)
	}
	token := strings.TrimSpace(payload.Token)
	if token == "" {
		writeAuthError(w, http.StatusBadRequest, "token is required")
		return
	}

	if r.Method == http.MethodDelete {
		// Unregistering on logout is not housekeeping: a token left bound to
		// the previous account pushes their messages to whoever signs in next
		// on this phone.
		if err := removeDeviceToken("", user.Email, token); err != nil {
			writeAuthError(w, http.StatusInternalServerError, "could not unregister device")
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	if err := upsertDeviceToken(deviceTokenRecord{
		UserEmail: user.Email,
		Token:     token,
		Platform:  strings.TrimSpace(payload.Platform),
	}); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not register device")
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true})
}
