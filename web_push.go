package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// Card 089 — real phone push. Every durable notification already funnels
// through pushNotificationRecord (notifications.go); this file adds the Web
// Push (VAPID) fan-out so a subscribed phone buzzes even with the app closed.
// The store is disk-backed and read-modify-write under a mutex so tests that
// swap PUSH_SUBSCRIPTIONS_PATH stay isolated (no long-lived in-memory cache to
// leak across them), mirroring notificationsPath()'s env-override pattern.

// webPushTTLSeconds keeps an undelivered push queued on the push service for
// five minutes; a phone that comes back online inside that window still buzzes.
const webPushTTLSeconds = 300

// pushSubscriptionRecord is one browser Push API subscription bound to an
// account. Endpoint is the natural key (upsert-by-endpoint); the same phone
// re-subscribing rebinds rather than duplicates.
type pushSubscriptionRecord struct {
	UserEmail string `json:"userEmail"`
	Endpoint  string `json:"endpoint"`
	Keys      struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	UserAgent string `json:"userAgent,omitempty"`
	CreatedAt string `json:"createdAt"`
}

func (r pushSubscriptionRecord) toWebpush() *webpush.Subscription {
	return &webpush.Subscription{
		Endpoint: r.Endpoint,
		Keys: webpush.Keys{
			P256dh: r.Keys.P256dh,
			Auth:   r.Keys.Auth,
		},
	}
}

// pushPrefs is a per-user policy: which notification kinds may buzz a phone,
// and whether to suppress the push while the account has a live socket open
// (only-when-away, default on so a phone never double-buzzes what the desktop
// already showed).
type pushPrefs struct {
	Kinds        map[string]bool `json:"kinds"`
	OnlyWhenAway bool            `json:"onlyWhenAway"`
}

// pushStoreData is the whole persisted push store: subscriptions plus the
// per-user preference map.
type pushStoreData struct {
	Subscriptions []pushSubscriptionRecord `json:"subscriptions"`
	Prefs         map[string]pushPrefs     `json:"prefs,omitempty"`
	UpdatedAt     string                   `json:"updatedAt,omitempty"`
}

// pushNotificationKinds is the closed set of kinds a per-kind toggle covers —
// the same kinds normalizeNotificationKind emits.
var pushNotificationKinds = []string{
	notificationKindInfo,
	notificationKindTask,
	notificationKindAgent,
	notificationKindChat,
	notificationKindAlert,
}

var pushStoreMu sync.Mutex

func pushSubscriptionsPath() string {
	if path := strings.TrimSpace(os.Getenv("PUSH_SUBSCRIPTIONS_PATH")); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "push-subscriptions.json")
}

// loadPushStoreFile reads the store off disk. A missing/empty/corrupt file is a
// clean empty store — push is best-effort and must never wedge a notification.
// Callers hold pushStoreMu (or accept a point-in-time snapshot).
func loadPushStoreFile() pushStoreData {
	empty := pushStoreData{Prefs: map[string]pushPrefs{}}
	raw, err := os.ReadFile(pushSubscriptionsPath())
	if err != nil {
		return empty
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return empty
	}
	var state pushStoreData
	if err := json.Unmarshal(raw, &state); err != nil {
		log.Errorf("Failed to decode push subscriptions: %v", err)
		return empty
	}
	if state.Prefs == nil {
		state.Prefs = map[string]pushPrefs{}
	}
	return state
}

// mutatePushStore is the read-modify-write seam: load fresh, apply fn, persist
// atomically — all under the lock so concurrent subscribe/prune don't clobber.
func mutatePushStore(fn func(*pushStoreData)) error {
	pushStoreMu.Lock()
	defer pushStoreMu.Unlock()
	state := loadPushStoreFile()
	fn(&state)
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return writeJSONFileAtomically(pushSubscriptionsPath(), "push subscriptions", state)
}

// snapshotPushStore returns a point-in-time copy for the delivery path.
func snapshotPushStore() pushStoreData {
	pushStoreMu.Lock()
	defer pushStoreMu.Unlock()
	return loadPushStoreFile()
}

// upsertPushSubscription binds a subscription to an account, keyed by endpoint:
// the same endpoint re-subscribing (or moving to a different signed-in account)
// rebinds in place rather than duplicating.
func upsertPushSubscription(sub pushSubscriptionRecord) error {
	return mutatePushStore(func(state *pushStoreData) {
		for index := range state.Subscriptions {
			if state.Subscriptions[index].Endpoint == sub.Endpoint {
				state.Subscriptions[index] = sub
				return
			}
		}
		state.Subscriptions = append(state.Subscriptions, sub)
	})
}

// removePushSubscription drops one endpoint, scoped to the owning account so a
// caller can only unsubscribe its own device.
func removePushSubscription(userEmail, endpoint string) error {
	userEmail = normalizeAccountEmail(userEmail)
	endpoint = strings.TrimSpace(endpoint)
	return mutatePushStore(func(state *pushStoreData) {
		kept := make([]pushSubscriptionRecord, 0, len(state.Subscriptions))
		for _, sub := range state.Subscriptions {
			if sub.Endpoint == endpoint && (userEmail == "" || sub.UserEmail == userEmail) {
				continue
			}
			kept = append(kept, sub)
		}
		state.Subscriptions = kept
	})
}

// prunePushSubscriptions drops dead endpoints (a push service answered 404/410
// — the subscription is gone for good). Log-and-continue; a failed prune just
// retries on the next dead delivery.
func prunePushSubscriptions(endpoints []string) {
	if len(endpoints) == 0 {
		return
	}
	drop := make(map[string]bool, len(endpoints))
	for _, endpoint := range endpoints {
		drop[endpoint] = true
	}
	if err := mutatePushStore(func(state *pushStoreData) {
		kept := make([]pushSubscriptionRecord, 0, len(state.Subscriptions))
		for _, sub := range state.Subscriptions {
			if drop[sub.Endpoint] {
				continue
			}
			kept = append(kept, sub)
		}
		state.Subscriptions = kept
	}); err != nil {
		log.Errorf("Failed to prune stale push subscriptions: %v", err)
	}
}

// setPushPrefs persists one account's push policy.
func setPushPrefs(userEmail string, prefs pushPrefs) error {
	userEmail = normalizeAccountEmail(userEmail)
	if userEmail == "" {
		return fmt.Errorf("account is required")
	}
	return mutatePushStore(func(state *pushStoreData) {
		if state.Prefs == nil {
			state.Prefs = map[string]pushPrefs{}
		}
		state.Prefs[userEmail] = prefs
	})
}

// defaultPushPrefs is the policy for an account that has never saved one: every
// kind may buzz, and only-when-away is on so a phone never double-buzzes what
// an open session already surfaced.
func defaultPushPrefs() pushPrefs {
	kinds := make(map[string]bool, len(pushNotificationKinds))
	for _, kind := range pushNotificationKinds {
		kinds[kind] = true
	}
	return pushPrefs{Kinds: kinds, OnlyWhenAway: true}
}

// resolvePushPrefs merges stored prefs onto the defaults so a partial stored
// map (missing a newly-added kind) still resolves that kind to its default.
func resolvePushPrefs(state pushStoreData, userEmail string) pushPrefs {
	prefs := defaultPushPrefs()
	stored, ok := state.Prefs[normalizeAccountEmail(userEmail)]
	if !ok {
		return prefs
	}
	if stored.Kinds != nil {
		for _, kind := range pushNotificationKinds {
			if value, present := stored.Kinds[kind]; present {
				prefs.Kinds[kind] = value
			}
		}
	}
	prefs.OnlyWhenAway = stored.OnlyWhenAway
	return prefs
}

// prefsFromRequest builds a full policy from a POST body, defaulting any absent
// kind to on (the client posts the whole object, but a partial payload stays
// permissive rather than silently muting).
func prefsFromRequest(kinds map[string]bool, onlyWhenAway bool) pushPrefs {
	prefs := defaultPushPrefs()
	if kinds != nil {
		for _, kind := range pushNotificationKinds {
			if value, present := kinds[kind]; present {
				prefs.Kinds[kind] = value
			}
		}
	}
	prefs.OnlyWhenAway = onlyWhenAway
	return prefs
}

func pushRecipientMatches(record notificationRecord, userEmail string) bool {
	// Broadcast (UserEmail == "") reaches every subscriber; a targeted record
	// reaches only its recipient.
	return record.UserEmail == "" || record.UserEmail == normalizeAccountEmail(userEmail)
}

// --- VAPID keys -------------------------------------------------------------

type vapidKeyPair struct {
	Public  string `json:"publicKey"`
	Private string `json:"privateKey"`
}

func vapidKeysPath() string {
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "vapid-keys.json")
}

var (
	vapidMu       sync.Mutex
	vapidCache    *vapidKeyPair
	vapidCacheKey string
)

// vapidKeys resolves the server's VAPID keypair: an explicit env override wins;
// otherwise the pair is read from (or generated once and persisted to)
// data/vapid-keys.json, which survives deploys like users.json. Generating once
// is guaranteed by the persisted file (not the cache) so subscriptions minted
// against a key stay valid across restarts. The cache is keyed by path so a
// test that repoints the data dir reloads instead of leaking the prior pair.
func vapidKeys() (vapidKeyPair, error) {
	pub := strings.TrimSpace(os.Getenv("WEB_PUSH_VAPID_PUBLIC_KEY"))
	priv := strings.TrimSpace(os.Getenv("WEB_PUSH_VAPID_PRIVATE_KEY"))
	if pub != "" && priv != "" {
		return vapidKeyPair{Public: pub, Private: priv}, nil
	}

	vapidMu.Lock()
	defer vapidMu.Unlock()
	path := vapidKeysPath()
	if vapidCache != nil && vapidCacheKey == path {
		return *vapidCache, nil
	}

	if raw, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(raw))) > 0 {
		var pair vapidKeyPair
		if err := json.Unmarshal(raw, &pair); err == nil && pair.Public != "" && pair.Private != "" {
			vapidCache = &pair
			vapidCacheKey = path
			return pair, nil
		}
	}

	generatedPrivate, generatedPublic, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return vapidKeyPair{}, fmt.Errorf("generate VAPID keys: %w", err)
	}
	pair := vapidKeyPair{Public: generatedPublic, Private: generatedPrivate}
	if err := writeJSONFileAtomically(path, "VAPID keys", pair); err != nil {
		log.Errorf("Failed to persist VAPID keys: %v", err)
	}
	vapidCache = &pair
	vapidCacheKey = path
	return pair, nil
}

// loadOrCreateVAPIDKeys warms (and, on first boot, mints) the keypair so the
// public key is ready the instant a client asks to subscribe.
func loadOrCreateVAPIDKeys() {
	if _, err := vapidKeys(); err != nil {
		log.Errorf("Web push disabled: %v", err)
	}
}

func webPushSubscriber() string {
	if subscriber := strings.TrimSpace(os.Getenv("WEB_PUSH_SUBSCRIBER")); subscriber != "" {
		return subscriber
	}
	return "mailto:noreply@thebonfire.xyz"
}

// sendWebPush is the outbound seam, a var so tests stub delivery (the
// sendAccountEmail pattern).
var sendWebPush = func(payload []byte, sub *webpush.Subscription, opts *webpush.Options) (*http.Response, error) {
	return webpush.SendNotification(payload, sub, opts)
}

// deliverWebPushForRecord fans one durable notification out to subscribed
// phones. Runs in its own goroutine off pushNotificationRecord so the websocket
// fan-out never blocks on a network call. Short-circuits before touching VAPID
// keys when nothing is subscribed (the common case for every non-push test).
func deliverWebPushForRecord(record notificationRecord) {
	if strings.TrimSpace(record.Text) == "" {
		return
	}
	state := snapshotPushStore()
	if len(state.Subscriptions) == 0 {
		return
	}

	type target struct {
		sub pushSubscriptionRecord
	}
	targets := make([]target, 0, len(state.Subscriptions))
	for _, sub := range state.Subscriptions {
		if !pushRecipientMatches(record, sub.UserEmail) {
			continue
		}
		prefs := resolvePushPrefs(state, sub.UserEmail)
		if !prefs.Kinds[record.Kind] {
			continue
		}
		if prefs.OnlyWhenAway && userHasLiveKanbanSocket(sub.UserEmail) {
			continue
		}
		targets = append(targets, target{sub: sub})
	}
	if len(targets) == 0 {
		return
	}

	keys, err := vapidKeys()
	if err != nil {
		log.Errorf("Web push send skipped, no VAPID keys: %v", err)
		return
	}

	payload, err := json.Marshal(map[string]any{
		"title": osNotificationEventTitle(record),
		"body":  record.Text,
		"tag":   record.ID,
		"url":   "/?bell=" + record.ID,
	})
	if err != nil {
		log.Errorf("Failed to encode web push payload: %v", err)
		return
	}

	subscriber := webPushSubscriber()
	var stale []string
	for _, t := range targets {
		response, err := sendWebPush(payload, t.sub.toWebpush(), &webpush.Options{
			Subscriber:      subscriber,
			VAPIDPublicKey:  keys.Public,
			VAPIDPrivateKey: keys.Private,
			TTL:             webPushTTLSeconds,
		})
		if err != nil {
			log.Errorf("Web push send failed for %s: %v", t.sub.UserEmail, err)
			continue
		}
		if response != nil {
			if response.Body != nil {
				response.Body.Close()
			}
			// 404/410 means the subscription is permanently gone; prune it.
			if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
				stale = append(stale, t.sub.Endpoint)
			}
		}
	}
	prunePushSubscriptions(stale)
}

// --- HTTP handlers ----------------------------------------------------------
//
// The guard stack (method, origin, session, app-ready) mirrors
// assistantNotificationsHandler exactly.

func assistantPushConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
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
		writeAuthError(w, http.StatusServiceUnavailable, "push is unavailable")
		return
	}

	keys, err := vapidKeys()
	if err != nil {
		writeAuthError(w, http.StatusServiceUnavailable, "push is unavailable")
		return
	}
	state := snapshotPushStore()
	prefs := resolvePushPrefs(state, user.Email)
	subscribed := false
	for _, sub := range state.Subscriptions {
		if sub.UserEmail == normalizeAccountEmail(user.Email) {
			subscribed = true
			break
		}
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"vapidPublicKey": keys.Public,
		"subscribed":     subscribed,
		"prefs": map[string]any{
			"kinds":        prefs.Kinds,
			"onlyWhenAway": prefs.OnlyWhenAway,
		},
	})
}

func assistantPushSubscribeHandler(w http.ResponseWriter, r *http.Request) {
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
		writeAuthError(w, http.StatusServiceUnavailable, "push is unavailable")
		return
	}

	var body struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read subscription")
		return
	}
	endpoint := strings.TrimSpace(body.Endpoint)
	if endpoint == "" || strings.TrimSpace(body.Keys.P256dh) == "" || strings.TrimSpace(body.Keys.Auth) == "" {
		writeAuthError(w, http.StatusBadRequest, "subscription is incomplete")
		return
	}

	record := pushSubscriptionRecord{
		UserEmail: normalizeAccountEmail(user.Email),
		Endpoint:  endpoint,
		UserAgent: trimForStorage(r.Header.Get("User-Agent"), 200),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	record.Keys.P256dh = strings.TrimSpace(body.Keys.P256dh)
	record.Keys.Auth = strings.TrimSpace(body.Keys.Auth)
	if err := upsertPushSubscription(record); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not save subscription")
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func assistantPushUnsubscribeHandler(w http.ResponseWriter, r *http.Request) {
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
		writeAuthError(w, http.StatusServiceUnavailable, "push is unavailable")
		return
	}

	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read subscription")
		return
	}
	if strings.TrimSpace(body.Endpoint) == "" {
		writeAuthError(w, http.StatusBadRequest, "endpoint is required")
		return
	}
	if err := removePushSubscription(user.Email, body.Endpoint); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not remove subscription")
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func assistantPushPrefsHandler(w http.ResponseWriter, r *http.Request) {
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
		writeAuthError(w, http.StatusServiceUnavailable, "push is unavailable")
		return
	}

	var body struct {
		Kinds        map[string]bool `json:"kinds"`
		OnlyWhenAway bool            `json:"onlyWhenAway"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read preferences")
		return
	}
	prefs := prefsFromRequest(body.Kinds, body.OnlyWhenAway)
	if err := setPushPrefs(user.Email, prefs); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not save preferences")
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"prefs": map[string]any{
			"kinds":        prefs.Kinds,
			"onlyWhenAway": prefs.OnlyWhenAway,
		},
	})
}
