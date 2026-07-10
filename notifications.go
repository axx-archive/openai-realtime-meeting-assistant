package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// notificationStoreCap keeps data/notifications.json bounded: only the
	// newest 500 records survive a write.
	notificationStoreCap = 500
	// notificationListLimit is the newest-first page size for GET and for the
	// unread backlog replayed on websocket admission.
	notificationListLimit = 100
)

const (
	notificationKindInfo  = "info"
	notificationKindTask  = "task"
	notificationKindAgent = "agent"
	notificationKindChat  = "chat"
	notificationKindAlert = "alert"
)

const (
	notificationAudienceEveryone = "everyone"
	notificationAudienceMe       = "me"
)

const (
	// send_notification deliver argument values.
	notificationDeliverNow          = "now"
	notificationDeliverAfterMeeting = "after_meeting"
	// notificationDeliverAfterMeetingMarker is the stored DeliverAfter value
	// while an after_meeting record waits for the meeting to end.
	notificationDeliverAfterMeetingMarker = "meeting"
)

// notificationRecord is one durable notification. UserEmail == "" means the
// notification is addressed to everyone; ReadBy tracks which accounts have
// acknowledged it.
type notificationRecord struct {
	ID         string `json:"id"`
	UserEmail  string `json:"userEmail,omitempty"`
	Kind       string `json:"kind"`
	Text       string `json:"text"`
	Tool       string `json:"tool,omitempty"`
	ArtifactID string `json:"artifactId,omitempty"`
	// ThreadID deep-links the bell entry to a chat thread/channel.
	ThreadID string `json:"threadId,omitempty"`
	// ProposalID links a codex-proposal nudge back to its proposal so
	// settleProposalNotification can find and settle this record when the
	// proposal resolves.
	ProposalID string `json:"proposalId,omitempty"`
	// ResolvedAt stamps the moment the linked proposal settled. While the
	// proposal is pending the nudge is sticky — generic read receipts skip
	// it — and once stamped the record reads as acknowledged for every
	// account (the call to action is gone for everyone).
	ResolvedAt string `json:"resolvedAt,omitempty"`
	// DeliverAfter marks a queued deferred notification ("meeting" while
	// waiting for the meeting to end); flushDeferredNotifications clears it,
	// restamps CreatedAt, and pushes the record. Queued records are hidden
	// from every viewer list until then.
	DeliverAfter string   `json:"deliverAfter,omitempty"`
	CreatedAt    string   `json:"createdAt"`
	ReadBy       []string `json:"readBy,omitempty"`
	// ClearedBy tracks which accounts have dismissed this notification from
	// their bell. Cleared records are filtered out of every viewer list (the
	// GET page and the websocket admission backlog) without being deleted —
	// the store stays a single shared log, so a broadcast one account cleared
	// still reaches everyone else. The roster is bounded by the 500-record cap.
	ClearedBy []string `json:"clearedBy,omitempty"`
}

type notificationStoreState struct {
	Notifications []notificationRecord `json:"notifications"`
	UpdatedAt     string               `json:"updatedAt,omitempty"`
}

func notificationsPath() string {
	if path := strings.TrimSpace(os.Getenv("NOTIFICATIONS_PATH")); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "notifications.json")
}

func loadNotificationStoreState(path string) ([]notificationRecord, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read notifications: %w", err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}

	var state notificationStoreState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("decode notifications: %w", err)
	}
	records := make([]notificationRecord, 0, len(state.Notifications))
	for _, record := range state.Notifications {
		if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.Text) == "" {
			continue
		}
		record.Kind = normalizeNotificationKind(record.Kind)
		record.UserEmail = normalizeAccountEmail(record.UserEmail)
		records = append(records, record)
	}
	if len(records) > notificationStoreCap {
		records = records[len(records)-notificationStoreCap:]
	}
	return records, nil
}

func normalizeNotificationKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case notificationKindTask:
		return notificationKindTask
	case notificationKindAgent:
		return notificationKindAgent
	case notificationKindChat:
		return notificationKindChat
	case notificationKindAlert:
		return notificationKindAlert
	default:
		return notificationKindInfo
	}
}

// createNotification appends a durable notification, persists the capped
// store, and pushes the record over the kanban websocket. Broadcast records
// (UserEmail == "") fan out to everyone; targeted records go only to sockets
// whose server-side authenticated session email matches the recipient — a
// non-recipient never receives the payload, so client-side filtering is
// defense-in-depth only. threadID deep-links the bell entry to a chat
// thread/channel. deferred queues the record with DeliverAfter="meeting" and
// skips the push entirely — flushDeferredNotifications delivers it when the
// meeting ends.
func (app *kanbanBoardApp) createNotification(userEmail string, kind string, text string, tool string, artifactID string, threadID string, deferred bool) (notificationRecord, error) {
	return app.createLinkedNotification(userEmail, kind, text, tool, artifactID, threadID, "", deferred)
}

// createLinkedNotification is createNotification plus the proposal linkage:
// proposalID stamps the record so settleProposalNotification can rewrite it
// when the proposal resolves.
func (app *kanbanBoardApp) createLinkedNotification(userEmail string, kind string, text string, tool string, artifactID string, threadID string, proposalID string, deferred bool) (notificationRecord, error) {
	if app == nil {
		return notificationRecord{}, fmt.Errorf("notifications are unavailable")
	}
	text = trimForStorage(text, 500)
	if text == "" {
		return notificationRecord{}, fmt.Errorf("notification text is required")
	}

	record := notificationRecord{
		UserEmail:  normalizeAccountEmail(userEmail),
		Kind:       normalizeNotificationKind(kind),
		Text:       text,
		Tool:       strings.TrimSpace(tool),
		ArtifactID: strings.TrimSpace(artifactID),
		ThreadID:   strings.TrimSpace(threadID),
		ProposalID: strings.TrimSpace(proposalID),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	if deferred {
		record.DeliverAfter = notificationDeliverAfterMeetingMarker
	}

	app.mu.Lock()
	record.ID = app.nextNotificationIDLocked()
	app.notifications = append(app.notifications, record)
	if len(app.notifications) > notificationStoreCap {
		app.notifications = app.notifications[len(app.notifications)-notificationStoreCap:]
	}
	persistErr := app.persistNotificationsLocked()
	app.mu.Unlock()
	if persistErr != nil {
		log.Errorf("Failed to persist notifications: %v", persistErr)
	}

	if deferred {
		// Queued: no broadcast, no targeted send — the flush pushes it.
		return record, nil
	}
	pushNotificationRecord(record)
	return record, nil
}

// pushNotificationRecord fans one persisted record out over the websocket:
// broadcast to everyone, or targeted to the recipient's own sockets only.
func pushNotificationRecord(record notificationRecord) {
	if record.UserEmail == "" {
		broadcastSignedInKanbanEvent("notification", notificationForViewer(record, ""))
	} else {
		sendKanbanEventToUser(record.UserEmail, "notification", notificationForViewer(record, record.UserEmail))
	}
	// Unified push channel: the same record on the typed stream so brief
	// counters and other light consumers can react by kind. The bell itself
	// stays driven by the full 'notification' event above; this carries a
	// body-free, kind-derived label only — record.Text can embed message body
	// (chat notifications) which must never cross the "titles only" boundary.
	osEvt := osEvent{
		Kind:          osEventNotification,
		Ref:           record.ID,
		Title:         osNotificationEventTitle(record),
		OriginSurface: firstNonEmptyString(record.Tool, "room"),
	}
	if record.UserEmail == "" {
		broadcastOSEvent(osEvt)
	} else {
		sendOSEventToUser(record.UserEmail, osEvt)
	}
	// Card 089: the same record buzzes subscribed phones over Web Push. This
	// is the single fan-out seam every durable notification (live + deferred
	// flush) already funnels through, so one hook covers every producer. Run
	// it off the fan-out path — a per-subscription network call must never
	// block the websocket sends above. settleProposalNotification rewrites its
	// nudge without calling here on purpose (no re-buzz on settle).
	go deliverWebPushForRecord(record)
}

// flushDeferredNotifications delivers every notification queued with
// deliver "after_meeting": clears the queue marker, restamps CreatedAt to the
// delivery moment (the bell orders by it), persists once, then pushes each
// record. Idempotent — a second call finds nothing queued, so the meeting-end
// seam and archiveMeeting may both invoke it safely.
func (app *kanbanBoardApp) flushDeferredNotifications(trigger string) int {
	if app == nil {
		return 0
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	app.mu.Lock()
	flushed := make([]notificationRecord, 0, 2)
	for index := range app.notifications {
		if app.notifications[index].DeliverAfter != notificationDeliverAfterMeetingMarker {
			continue
		}
		app.notifications[index].DeliverAfter = ""
		app.notifications[index].CreatedAt = now
		flushed = append(flushed, app.notifications[index])
	}
	var persistErr error
	if len(flushed) > 0 {
		persistErr = app.persistNotificationsLocked()
	}
	app.mu.Unlock()
	if persistErr != nil {
		log.Errorf("Failed to persist flushed deferred notifications: %v", persistErr)
	}
	if len(flushed) == 0 {
		return 0
	}

	log.Infof("Delivering %d deferred notification(s) on %s", len(flushed), trigger)
	for _, record := range flushed {
		pushNotificationRecord(record)
	}
	return len(flushed)
}

func (app *kanbanBoardApp) nextNotificationIDLocked() string {
	for {
		id := fmt.Sprintf("notification-%d", time.Now().UnixNano())
		taken := false
		for index := len(app.notifications) - 1; index >= 0; index-- {
			if app.notifications[index].ID == id {
				taken = true
				break
			}
		}
		if !taken {
			return id
		}
	}
}

func (app *kanbanBoardApp) persistNotificationsLocked() error {
	state := notificationStoreState{
		Notifications: append([]notificationRecord(nil), app.notifications...),
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	return writeJSONFileAtomically(notificationsPath(), "notifications", state)
}

func notificationVisibleTo(record notificationRecord, viewerEmail string) bool {
	return record.UserEmail == "" || record.UserEmail == viewerEmail
}

func notificationReadBy(record notificationRecord, viewerEmail string) bool {
	if viewerEmail == "" {
		return false
	}
	for _, reader := range record.ReadBy {
		if normalizeAccountEmail(reader) == viewerEmail {
			return true
		}
	}
	return false
}

// notificationClearedBy reports whether the viewer has dismissed this record
// from their bell. Cleared records stay in the shared store but drop out of the
// viewer's list and unread backlog.
func notificationClearedBy(record notificationRecord, viewerEmail string) bool {
	if viewerEmail == "" {
		return false
	}
	for _, clearer := range record.ClearedBy {
		if normalizeAccountEmail(clearer) == viewerEmail {
			return true
		}
	}
	return false
}

// notificationReadFor is the viewer-facing read state: a settled proposal
// nudge reads as acknowledged for every account — once someone acted, the
// call to action is moot for everyone — otherwise the ReadBy roster decides.
func notificationReadFor(record notificationRecord, viewerEmail string) bool {
	return record.ResolvedAt != "" || notificationReadBy(record, viewerEmail)
}

// notificationForViewer projects a record for one account: the ReadBy roster
// stays server-side (clients only learn their own read state).
func notificationForViewer(record notificationRecord, viewerEmail string) map[string]any {
	payload := map[string]any{
		"id":        record.ID,
		"kind":      record.Kind,
		"text":      record.Text,
		"createdAt": record.CreatedAt,
		"read":      notificationReadFor(record, viewerEmail),
	}
	if record.UserEmail != "" {
		payload["userEmail"] = record.UserEmail
	}
	if record.Tool != "" {
		payload["tool"] = record.Tool
	}
	if record.ArtifactID != "" {
		payload["artifactId"] = record.ArtifactID
	}
	if record.ThreadID != "" {
		payload["threadId"] = record.ThreadID
	}
	if record.ProposalID != "" {
		payload["proposalId"] = record.ProposalID
	}
	return payload
}

// notificationsForUser returns the viewer's own plus broadcast notifications,
// newest first.
func (app *kanbanBoardApp) notificationsForUser(viewerEmail string, limit int) []map[string]any {
	return app.notificationsForUserFiltered(viewerEmail, limit, false)
}

// unreadNotificationsFor is the websocket admission backlog: only records the
// viewer has not read yet, newest first.
func (app *kanbanBoardApp) unreadNotificationsFor(viewerEmail string, limit int) []map[string]any {
	return app.notificationsForUserFiltered(viewerEmail, limit, true)
}

func (app *kanbanBoardApp) notificationsForUserFiltered(viewerEmail string, limit int, unreadOnly bool) []map[string]any {
	if app == nil {
		return []map[string]any{}
	}
	viewerEmail = normalizeAccountEmail(viewerEmail)
	if viewerEmail == "" {
		return []map[string]any{}
	}

	app.mu.Lock()
	records := append([]notificationRecord(nil), app.notifications...)
	app.mu.Unlock()

	visible := make([]map[string]any, 0, len(records))
	for index := len(records) - 1; index >= 0; index-- {
		record := records[index]
		// Queued deferred records stay invisible until the flush delivers them.
		if record.DeliverAfter != "" {
			continue
		}
		if !notificationVisibleTo(record, viewerEmail) {
			continue
		}
		// A record the viewer cleared is hidden from both the GET page and the
		// websocket admission backlog — this one filter covers both callers.
		if notificationClearedBy(record, viewerEmail) {
			continue
		}
		if unreadOnly && notificationReadFor(record, viewerEmail) {
			continue
		}
		visible = append(visible, notificationForViewer(record, viewerEmail))
		if limit > 0 && len(visible) >= limit {
			break
		}
	}
	return visible
}

// settleProposalNotification rewrites the propose-time broadcast nudge when
// its codex proposal resolves: the stale "confirm to launch" text becomes the
// outcome, the ResolvedAt stamp settles the record for every account (any
// signed-in user could have acted, so the call to action is moot for all of
// them and the record never replays into anyone's unread backlog), a
// confirmed launch stamps the run artifact so the bell entry routes to the
// resulting workflow, and the updated record re-broadcasts so live bells
// rewrite in place. The resolver still joins ReadBy as the explicit actor
// receipt. The os_event mirror is skipped on purpose —
// notifyProposalResolution already covers the typed stream.
func (app *kanbanBoardApp) settleProposalNotification(proposalID string, text string, resolverEmail string, artifactID string) {
	if app == nil {
		return
	}
	proposalID = strings.TrimSpace(proposalID)
	text = trimForStorage(text, 500)
	if proposalID == "" || text == "" {
		return
	}
	resolverEmail = normalizeAccountEmail(resolverEmail)
	artifactID = strings.TrimSpace(artifactID)

	app.mu.Lock()
	var settled notificationRecord
	found := false
	for index := len(app.notifications) - 1; index >= 0; index-- {
		record := &app.notifications[index]
		if record.ProposalID != proposalID {
			continue
		}
		// Rewrite in place: only the settled fields change. The existing
		// ClearedBy roster rides through untouched, so a record an account
		// cleared after it settled stays cleared for them — the re-broadcast
		// below never resurrects it in their bell on the next list/backlog sync.
		record.Text = text
		record.ResolvedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if artifactID != "" {
			record.ArtifactID = artifactID
		}
		if resolverEmail != "" && !notificationReadBy(*record, resolverEmail) {
			record.ReadBy = append(record.ReadBy, resolverEmail)
		}
		settled = *record
		found = true
		break
	}
	var persistErr error
	if found {
		persistErr = app.persistNotificationsLocked()
	}
	app.mu.Unlock()
	if persistErr != nil {
		log.Errorf("Failed to persist settled proposal notification for %s: %v", proposalID, persistErr)
	}
	if !found {
		return
	}
	broadcastSignedInKanbanEvent("notification", notificationForViewer(settled, ""))
}

// markNotificationsRead stamps the viewer onto ReadBy for every listed id the
// viewer can see, persists once, and returns how many records changed. A
// proposal nudge whose proposal is still pending is sticky: generic
// view/mark-all-read receipts skip it, and only resolveCodexProposal (via
// settleProposalNotification) settles it.
func (app *kanbanBoardApp) markNotificationsRead(viewerEmail string, ids []string) (int, error) {
	if app == nil {
		return 0, fmt.Errorf("notifications are unavailable")
	}
	viewerEmail = normalizeAccountEmail(viewerEmail)
	if viewerEmail == "" || len(ids) == 0 {
		return 0, nil
	}
	wanted := map[string]struct{}{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			wanted[id] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return 0, nil
	}

	app.mu.Lock()
	marked := 0
	for index := range app.notifications {
		record := &app.notifications[index]
		if _, ok := wanted[record.ID]; !ok {
			continue
		}
		if !notificationVisibleTo(*record, viewerEmail) || notificationReadFor(*record, viewerEmail) {
			continue
		}
		if record.ProposalID != "" && record.ResolvedAt == "" && app.proposalAwaitingAction(record.ProposalID) {
			// Sticky until acted on: the nudge only settles when someone
			// confirms or dismisses the proposal itself.
			continue
		}
		record.ReadBy = append(record.ReadBy, viewerEmail)
		marked++
	}
	var persistErr error
	if marked > 0 {
		persistErr = app.persistNotificationsLocked()
	}
	app.mu.Unlock()
	if persistErr != nil {
		return marked, persistErr
	}
	return marked, nil
}

// clearNotifications dismisses notifications from the viewer's bell by stamping
// them onto ClearedBy, persists once, and returns how many records changed. A
// nil/empty ids slice clears everything the viewer can currently see; a
// populated slice clears only those ids. Like markNotificationsRead it stamps
// only the viewer (the shared record survives for everyone else) and refuses a
// sticky pending-proposal nudge — that call to action only leaves the bell when
// the proposal itself is acted on, never on a generic clear.
func (app *kanbanBoardApp) clearNotifications(viewerEmail string, ids []string) (int, error) {
	if app == nil {
		return 0, fmt.Errorf("notifications are unavailable")
	}
	viewerEmail = normalizeAccountEmail(viewerEmail)
	if viewerEmail == "" {
		return 0, nil
	}
	// nil/empty ids means "clear all visible"; a populated set scopes the clear.
	var wanted map[string]struct{}
	if len(ids) > 0 {
		wanted = map[string]struct{}{}
		for _, id := range ids {
			if id = strings.TrimSpace(id); id != "" {
				wanted[id] = struct{}{}
			}
		}
		if len(wanted) == 0 {
			return 0, nil
		}
	}

	app.mu.Lock()
	cleared := 0
	for index := range app.notifications {
		record := &app.notifications[index]
		if wanted != nil {
			if _, ok := wanted[record.ID]; !ok {
				continue
			}
		}
		// Only records the viewer can actually see are clearable, and a queued
		// deferred record has not been delivered yet — clearing it before the
		// flush would swallow it silently.
		if !notificationVisibleTo(*record, viewerEmail) || record.DeliverAfter != "" {
			continue
		}
		if notificationClearedBy(*record, viewerEmail) {
			continue
		}
		if record.ProposalID != "" && record.ResolvedAt == "" && app.proposalAwaitingAction(record.ProposalID) {
			// Sticky until acted on: the nudge only leaves the bell when the
			// proposal is confirmed or dismissed, never on a clear sweep.
			continue
		}
		record.ClearedBy = append(record.ClearedBy, viewerEmail)
		cleared++
	}
	var persistErr error
	if cleared > 0 {
		persistErr = app.persistNotificationsLocked()
	}
	app.mu.Unlock()
	if persistErr != nil {
		return cleared, persistErr
	}
	return cleared, nil
}

// notifyAgentThreadCreator posts a durable notification for agent-thread
// milestones (completion, needs attention, approval required). The creator is
// resolved from the artifact's createdBy roster name; threads without an
// identifiable human creator (e.g. Scout-launched from the shared room)
// broadcast to everyone so the milestone is never lost.
func (app *kanbanBoardApp) notifyAgentThreadCreator(artifact meetingMemoryEntry, kind string, text string) {
	creatorEmail := participantEmail(artifact.Metadata["createdBy"])
	if _, err := app.createNotification(creatorEmail, kind, text, "", artifact.ID, "", false); err != nil {
		log.Errorf("Failed to create agent thread notification: %v", err)
	}
}

// sendRealtimeNotification executes the send_notification realtime tool.
// requesterEmail identifies the private-voice user for audience "me"; the
// shared room Scout has no single requester, so "me" falls back to everyone
// there. Errors return through the normal (result, changed, err) path — the
// tool endpoint folds them into the 200 result map.
func (app *kanbanBoardApp) sendRealtimeNotification(args map[string]any, requesterEmail string) (map[string]any, bool, error) {
	text := strings.TrimSpace(asString(args["text"]))
	if text == "" {
		return nil, false, fmt.Errorf("text is required")
	}

	audience := strings.ToLower(strings.TrimSpace(asString(args["audience"])))
	switch audience {
	case "", notificationAudienceEveryone:
		audience = notificationAudienceEveryone
	case notificationAudienceMe:
	default:
		return nil, false, fmt.Errorf("audience must be %q or %q", notificationAudienceEveryone, notificationAudienceMe)
	}
	userEmail := ""
	if audience == notificationAudienceMe {
		userEmail = normalizeAccountEmail(requesterEmail)
		if userEmail == "" {
			audience = notificationAudienceEveryone
		}
	}

	tool := ""
	if rawTool := strings.TrimSpace(asString(args["tool"])); rawTool != "" {
		tool = normalizeOSControlTool(rawTool)
		if tool == "" {
			return nil, false, fmt.Errorf("unknown tool %q", rawTool)
		}
	}

	deliver := strings.ToLower(strings.TrimSpace(asString(args["deliver"])))
	switch deliver {
	case "", notificationDeliverNow:
		deliver = notificationDeliverNow
	case notificationDeliverAfterMeeting:
	default:
		return nil, false, fmt.Errorf("deliver must be %q or %q", notificationDeliverNow, notificationDeliverAfterMeeting)
	}
	deferred := deliver == notificationDeliverAfterMeeting

	record, err := app.createNotification(userEmail, asString(args["kind"]), text, tool, "", "", deferred)
	if err != nil {
		return nil, false, err
	}

	if deferred {
		// Queued until the meeting ends: no toast now, so no actions.
		return map[string]any{
			"ok":           true,
			"audience":     audience,
			"deliver":      notificationDeliverAfterMeeting,
			"notification": notificationForViewer(record, userEmail),
		}, false, nil
	}

	// The invoking private-voice client applies these actions directly; the
	// websocket push reaches the rest of the audience (everyone for
	// broadcasts, only the recipient's own sockets for audience "me") and
	// clients dedupe by id.
	actions := []osAssistantAction{{
		Type:  "notify",
		ID:    record.ID,
		Kind:  record.Kind,
		Tool:  record.Tool,
		Label: record.Text,
	}}

	return map[string]any{
		"ok":           true,
		"audience":     audience,
		"notification": notificationForViewer(record, userEmail),
		"actions":      actions,
	}, false, nil
}

// assistantNotificationsHandler serves GET /assistant/notifications with the
// same origin + session guards as the chat-threads handlers.
func assistantNotificationsHandler(w http.ResponseWriter, r *http.Request) {
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
		writeAuthError(w, http.StatusServiceUnavailable, "notifications are unavailable")
		return
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"notifications": kanbanApp.notificationsForUser(user.Email, notificationListLimit),
	})
}

// assistantNotificationsReadHandler serves POST /assistant/notifications/read.
func assistantNotificationsReadHandler(w http.ResponseWriter, r *http.Request) {
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
		writeAuthError(w, http.StatusServiceUnavailable, "notifications are unavailable")
		return
	}

	payload := struct {
		IDs []string `json:"ids"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read notification ids")
		return
	}
	marked, err := kanbanApp.markNotificationsRead(user.Email, payload.IDs)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "marked": marked})
}

// assistantNotificationsClearHandler serves POST /assistant/notifications/clear
// with the same gate stack as the read handler. An absent or empty ids array
// clears everything the viewer can currently see; a populated array scopes the
// clear to those ids.
func assistantNotificationsClearHandler(w http.ResponseWriter, r *http.Request) {
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
		writeAuthError(w, http.StatusServiceUnavailable, "notifications are unavailable")
		return
	}

	payload := struct {
		IDs []string `json:"ids"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&payload); err != nil && err != io.EOF {
		writeAuthError(w, http.StatusBadRequest, "could not read notification ids")
		return
	}
	cleared, err := kanbanApp.clearNotifications(user.Email, payload.IDs)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared": cleared})
}
