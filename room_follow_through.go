package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const roomFollowThroughStoreCap = 500

const (
	roomFollowThroughRetryBase = 5 * time.Second
	roomFollowThroughRetryMax  = 5 * time.Minute
)

const (
	roomFollowThroughQueued        = "queued"
	roomFollowThroughDelivering    = "delivering"
	roomFollowThroughDelivered     = "delivered"
	roomFollowThroughAwaitingInput = "awaiting_input"
	roomFollowThroughRecapChannel  = "recap_to_channel"
)

var (
	roomFollowThroughIntentPattern = regexp.MustCompile(`(?i)\b(?:post|put|send|share|publish)\b[\s\S]{0,100}\b(?:recap|notes?|summary)\b|\b(?:recap|notes?|summary)\b[\s\S]{0,100}\b(?:post|put|send|share|publish)\b`)
	roomFollowThroughTargetPattern = regexp.MustCompile(`(?i)\b(?:in|into|to)\s+(?:the\s+)?#?([^\n,.!?]+?)\s+(?:thread|channel)\b`)
	roomFollowThroughHashPattern   = regexp.MustCompile(`(?i)(?:^|\s)#([a-z0-9][a-z0-9_-]{0,79})(?:\s|$|[,.!?])`)
)

type roomFollowThroughRecord struct {
	ID                  string `json:"id"`
	Kind                string `json:"kind"`
	Status              string `json:"status"`
	RoomID              string `json:"roomId"`
	SittingID           string `json:"sittingId"`
	MediaGeneration     uint64 `json:"mediaGeneration"`
	SourceMessageID     string `json:"sourceMessageId"`
	SourceTextDigest    string `json:"sourceTextDigest"`
	RequesterEmail      string `json:"requesterEmail"`
	RequesterName       string `json:"requesterName,omitempty"`
	DestinationThreadID string `json:"destinationThreadId"`
	DestinationTitle    string `json:"destinationTitle"`
	DestinationRevision string `json:"destinationRevision"`
	DeliveryMessageID   string `json:"deliveryMessageId"`
	CreatedAt           string `json:"createdAt"`
	UpdatedAt           string `json:"updatedAt"`
	DeliveredAt         string `json:"deliveredAt,omitempty"`
	Attempts            int    `json:"attempts,omitempty"`
	LastError           string `json:"lastError,omitempty"`
	ReceiptDigest       string `json:"receiptDigest,omitempty"`
	NextAttemptAt       string `json:"nextAttemptAt,omitempty"`
}

type roomFollowThroughStoreState struct {
	Records   []roomFollowThroughRecord `json:"records"`
	UpdatedAt string                    `json:"updatedAt,omitempty"`
}

func roomFollowThroughPath() string {
	if path := strings.TrimSpace(os.Getenv("ROOM_FOLLOW_THROUGH_PATH")); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "room-follow-through.json")
}

func loadRoomFollowThroughStore(path string) ([]roomFollowThroughRecord, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read room follow-through: %w", err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	var state roomFollowThroughStoreState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("decode room follow-through: %w", err)
	}
	records := make([]roomFollowThroughRecord, 0, len(state.Records))
	for _, record := range state.Records {
		if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.SittingID) == "" || strings.TrimSpace(record.DestinationThreadID) == "" {
			continue
		}
		if record.Status == roomFollowThroughDelivering {
			// A crash after the lease stamp but before destination commit is a
			// retry, not a terminal state. The deterministic message id makes the
			// replay exact-once.
			record.Status = roomFollowThroughQueued
		}
		records = append(records, record)
	}
	return compactRoomFollowThroughRecords(records), nil
}

// compactRoomFollowThroughRecords bounds completed history without ever
// deleting work that still needs delivery or human input. If more than the
// nominal cap is unfinished, durability wins and the store may temporarily
// exceed the display/history bound.
func compactRoomFollowThroughRecords(records []roomFollowThroughRecord) []roomFollowThroughRecord {
	if len(records) <= roomFollowThroughStoreCap {
		return records
	}
	keep := make([]bool, len(records))
	kept := 0
	for index, record := range records {
		if record.Status != roomFollowThroughDelivered {
			keep[index] = true
			kept++
		}
	}
	for index := len(records) - 1; index >= 0 && kept < roomFollowThroughStoreCap; index-- {
		if keep[index] || records[index].Status != roomFollowThroughDelivered {
			continue
		}
		keep[index] = true
		kept++
	}
	compacted := make([]roomFollowThroughRecord, 0, kept)
	for index, record := range records {
		if keep[index] {
			compacted = append(compacted, record)
		}
	}
	return compacted
}

func roomFollowThroughDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func parseRoomRecapFollowThrough(text string) (string, bool) {
	text = normalizeRoomChatText(text)
	if text == "" || !roomFollowThroughIntentPattern.MatchString(text) {
		return "", false
	}
	match := roomFollowThroughTargetPattern.FindStringSubmatch(text)
	if len(match) < 2 {
		if hash := roomFollowThroughHashPattern.FindStringSubmatch(text); len(hash) >= 2 {
			return strings.TrimSpace(hash[1]), true
		}
		return "", true
	}
	title := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(match[1]), "#"))
	if oneOf(strings.ToLower(title), "a", "an", "the", "this", "that") {
		return "", true
	}
	return title, true
}

func (app *kanbanBoardApp) roomFollowThroughDestination(requesterEmail, title string) (scoutChatThreadRecord, error) {
	title = strings.TrimSpace(strings.TrimPrefix(title, "#"))
	requesterEmail = normalizeAccountEmail(requesterEmail)
	if title == "" {
		return scoutChatThreadRecord{}, fmt.Errorf("name the destination channel")
	}
	if app == nil || app.memory == nil {
		return scoutChatThreadRecord{}, fmt.Errorf("channels are unavailable")
	}
	var matches []scoutChatThreadRecord
	for _, entry := range app.memory.snapshot(0) {
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok || thread.ArchivedAt != "" || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic || !strings.EqualFold(strings.TrimSpace(thread.Title), title) {
			continue
		}
		if requesterEmail != "" {
			if !scoutChatThreadAllowsViewer(thread, requesterEmail) {
				continue
			}
		} else if !scoutChatThreadIsOrganizationPublic(thread) {
			// A shared voice turn has no authenticated single requester. It may
			// schedule only into an organization-public channel.
			continue
		}
		matches = append(matches, thread)
	}
	if len(matches) != 1 {
		return scoutChatThreadRecord{}, fmt.Errorf("no single authorized channel named %q", title)
	}
	return matches[0], nil
}

func (app *kanbanBoardApp) scheduleRoomRecapFollowThrough(scope RoomScoutScope, sourceMessageID, sourceText, requesterEmail, requesterName, destinationTitle string) (roomFollowThroughRecord, error) {
	if app == nil || !app.roomScoutTextScopeCurrent(scope) {
		return roomFollowThroughRecord{}, ErrRoomScoutFence
	}
	if app.roomFollowThroughStoreErr != nil {
		return roomFollowThroughRecord{}, fmt.Errorf("durable follow-through is unavailable")
	}
	destination, err := app.roomFollowThroughDestination(requesterEmail, destinationTitle)
	if err != nil {
		return roomFollowThroughRecord{}, err
	}
	requesterEmail = normalizeAccountEmail(requesterEmail)
	sourceMessageID = strings.TrimSpace(sourceMessageID)
	if sourceMessageID == "" {
		return roomFollowThroughRecord{}, fmt.Errorf("source message is unavailable")
	}
	key := strings.Join([]string{scope.RoomID, scope.SittingID, sourceMessageID, roomFollowThroughRecapChannel, destination.ID}, "\x00")
	digest := roomFollowThroughDigest(key)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	record := roomFollowThroughRecord{
		ID: "room-follow-through-" + digest[:24], Kind: roomFollowThroughRecapChannel, Status: roomFollowThroughQueued,
		RoomID: scope.RoomID, SittingID: scope.SittingID, MediaGeneration: scope.MediaGeneration,
		SourceMessageID: sourceMessageID, SourceTextDigest: "sha256:" + roomFollowThroughDigest(normalizeRoomChatText(sourceText)),
		RequesterEmail: requesterEmail, RequesterName: canonicalRoomActorName(requesterName),
		DestinationThreadID: destination.ID, DestinationTitle: destination.Title,
		DestinationRevision: scoutChatAttachmentDestinationRevision(destination),
		DeliveryMessageID:   "room-recap-delivery-" + digest[:24], CreatedAt: now, UpdatedAt: now,
	}

	app.roomFollowThroughMu.Lock()
	defer app.roomFollowThroughMu.Unlock()
	for _, existing := range app.roomFollowThrough {
		if existing.ID == record.ID {
			return existing, nil
		}
	}
	previous := append([]roomFollowThroughRecord(nil), app.roomFollowThrough...)
	app.roomFollowThrough = append(app.roomFollowThrough, record)
	app.roomFollowThrough = compactRoomFollowThroughRecords(app.roomFollowThrough)
	if err := app.persistRoomFollowThroughLocked(); err != nil {
		app.roomFollowThrough = previous
		return roomFollowThroughRecord{}, err
	}
	return record, nil
}

func (app *kanbanBoardApp) persistRoomFollowThroughLocked() error {
	return writeJSONFileAtomically(roomFollowThroughPath(), "room follow-through", roomFollowThroughStoreState{
		Records:   append([]roomFollowThroughRecord(nil), app.roomFollowThrough...),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (app *kanbanBoardApp) resolveRoomFollowThroughRecap(ctx context.Context, record roomFollowThroughRecord) (ACLPrincipal, BrainRetrievalResult, string, error) {
	var retrieval BrainRetrievalResult
	requester := accountStore().findUser(record.RequesterEmail)
	if app == nil || app.meetings == nil || app.catchUpRecapResolver == nil || requester == nil {
		return ACLPrincipal{}, retrieval, "", fmt.Errorf("source authorization is unavailable")
	}
	meeting, ok := app.meetings.recordByID(record.SittingID)
	if !ok || meetingRoomID(meeting) != normalizeRoomID(record.RoomID) {
		return ACLPrincipal{}, retrieval, "", fmt.Errorf("source sitting is unavailable")
	}
	startedAt, startErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(meeting.StartedAt))
	endedAt := time.Now().UTC()
	if strings.TrimSpace(meeting.EndedAt) != "" {
		var endErr error
		endedAt, endErr = time.Parse(time.RFC3339Nano, strings.TrimSpace(meeting.EndedAt))
		if endErr != nil {
			return ACLPrincipal{}, retrieval, "", fmt.Errorf("source sitting end is invalid")
		}
	}
	if startErr != nil || !startedAt.Before(endedAt) {
		return ACLPrincipal{}, retrieval, "", fmt.Errorf("source sitting bounds are invalid")
	}
	temporal, err := NewBoundedTemporalQuery(TemporalExplicitRange, startedAt, endedAt, meetingTimeLocation().String(), record.RoomID, record.SittingID, "durable post-meeting recap")
	if err != nil {
		return ACLPrincipal{}, retrieval, "", fmt.Errorf("source temporal boundary is invalid")
	}
	principal := ACLPrincipal{
		TenantID: canonicalTenantID(), ID: normalizeAccountEmail(record.RequesterEmail), Kind: ACLPrincipalUser,
		TeamIDs: []string{"organization"}, RoomID: normalizeRoomID(record.RoomID), SittingID: record.SittingID,
	}
	retrieval, err = app.catchUpRecapResolver.ResolveCatchUp(ctx, BrainRetrievalRequest{
		Principal: principal, Query: "Create the completed meeting recap with decisions, blockers, and next actions.", Temporal: temporal,
	})
	if err != nil {
		return ACLPrincipal{}, BrainRetrievalResult{}, "", fmt.Errorf("source retrieval is unavailable: %w", err)
	}
	if retrieval.Snapshot.Validate() != nil || retrieval.Coverage.Validate() != nil || retrieval.Coverage.SnapshotID != retrieval.Snapshot.SnapshotID ||
		retrieval.Snapshot.PrincipalKind != principal.Kind || retrieval.Snapshot.PrincipalID != principal.ID || retrieval.Snapshot.TenantID != principal.TenantID {
		return ACLPrincipal{}, BrainRetrievalResult{}, "", fmt.Errorf("source authorization proof is invalid")
	}
	left, leftErr := canonicalJSON(retrieval.Snapshot.Temporal)
	right, rightErr := canonicalJSON(temporal)
	if leftErr != nil || rightErr != nil || string(left) != string(right) {
		return ACLPrincipal{}, BrainRetrievalResult{}, "", fmt.Errorf("source authorization crossed the sitting boundary")
	}
	recap, _, _, err := composeCatchUpWithOptionalProvider(ctx, retrieval, app.configuredCatchUpComposer())
	if err != nil {
		return ACLPrincipal{}, BrainRetrievalResult{}, "", fmt.Errorf("authorized recap composition is unavailable: %w", err)
	}
	prefix := app.meetingCapturePrefix(record.SittingID)
	roomName := record.RoomID
	if room, ok := appRoomStore().byID(record.RoomID); ok && strings.TrimSpace(room.Name) != "" {
		roomName = room.Name
	}
	text := trimForStorage(fmt.Sprintf("Meeting recap — %s\n\n%s%s", roomName, prefix, recap), 12000)
	return principal, retrieval, text, nil
}

func (app *kanbanBoardApp) commitRoomFollowThroughMessage(record roomFollowThroughRecord, text string) (scoutChatThreadRecord, error) {
	lock := app.scoutChatThreadLock(record.DestinationThreadID)
	lock.Lock()
	thread, err := app.roomFollowThroughDestinationByID(record)
	if err != nil {
		lock.Unlock()
		return scoutChatThreadRecord{}, err
	}
	if scoutChatAttachmentDestinationRevision(thread) != record.DestinationRevision {
		lock.Unlock()
		return scoutChatThreadRecord{}, fmt.Errorf("destination audience changed")
	}
	if index := scoutChatMessageIndex(thread, record.DeliveryMessageID); index >= 0 {
		lock.Unlock()
		return thread, nil
	}
	message := scoutChatMessageRecord{
		ID: record.DeliveryMessageID, Kind: "message", Role: "scout", Text: text,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: scoutParticipantName,
	}
	thread.Messages = append(thread.Messages, message)
	updateScoutChatThreadSummary(&thread, scoutChatMessageRecord{}, message)
	if err := app.saveScoutChatThread(thread); err != nil {
		lock.Unlock()
		return scoutChatThreadRecord{}, err
	}
	lock.Unlock()
	app.observeSTRIDETeamChatMessage(thread, message, "message", "")
	deliverScoutChatThreadUpdate(thread, message)
	return thread, nil
}

func (app *kanbanBoardApp) roomFollowThroughDestinationByID(record roomFollowThroughRecord) (scoutChatThreadRecord, error) {
	if app == nil || app.memory == nil {
		return scoutChatThreadRecord{}, fmt.Errorf("destination is unavailable")
	}
	for _, entry := range app.memory.snapshot(0) {
		if entry.ID != record.DestinationThreadID {
			continue
		}
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok || thread.ArchivedAt != "" || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic {
			break
		}
		if record.RequesterEmail != "" {
			if !scoutChatThreadAllowsViewer(thread, record.RequesterEmail) {
				break
			}
		} else if !scoutChatThreadIsOrganizationPublic(thread) {
			break
		}
		return thread, nil
	}
	return scoutChatThreadRecord{}, fmt.Errorf("destination is no longer authorized")
}

func (app *kanbanBoardApp) flushRoomFollowThroughForMeeting(roomID, sittingID, trigger string) int {
	if app == nil || app.memory == nil || strings.TrimSpace(sittingID) == "" {
		return 0
	}
	roomID = normalizeRoomID(roomID)
	app.roomFollowThroughMu.Lock()
	ids := make([]string, 0, 2)
	for index := range app.roomFollowThrough {
		record := &app.roomFollowThrough[index]
		if normalizeRoomID(record.RoomID) != roomID || record.SittingID != strings.TrimSpace(sittingID) || record.Status != roomFollowThroughQueued {
			continue
		}
		record.Status = roomFollowThroughDelivering
		record.Attempts++
		record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		ids = append(ids, record.ID)
	}
	if len(ids) > 0 {
		_ = app.persistRoomFollowThroughLocked()
	}
	app.roomFollowThroughMu.Unlock()

	delivered := 0
	for _, id := range ids {
		app.roomFollowThroughMu.Lock()
		index := -1
		var record roomFollowThroughRecord
		for candidate := range app.roomFollowThrough {
			if app.roomFollowThrough[candidate].ID == id {
				index = candidate
				record = app.roomFollowThrough[candidate]
				break
			}
		}
		app.roomFollowThroughMu.Unlock()
		if index < 0 {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), meetingBrainRequestTimeout)
		principal, retrieval, text, err := app.resolveRoomFollowThroughRecap(ctx, record)
		var thread scoutChatThreadRecord
		if err == nil {
			err = app.catchUpRecapResolver.CommitCatchUpPublication(ctx, principal, retrieval.Snapshot, func() error {
				var commitErr error
				thread, commitErr = app.commitRoomFollowThroughMessage(record, text)
				return commitErr
			})
		}
		cancel()
		now := time.Now().UTC().Format(time.RFC3339Nano)
		retry := false
		awaitingInput := false
		attempts := record.Attempts
		app.roomFollowThroughMu.Lock()
		for candidate := range app.roomFollowThrough {
			if app.roomFollowThrough[candidate].ID != id {
				continue
			}
			current := &app.roomFollowThrough[candidate]
			current.UpdatedAt = now
			if err != nil {
				current.Status = roomFollowThroughQueued
				current.LastError = trimForStorage(err.Error(), 300)
				if roomFollowThroughAuthorizationFailure(err) {
					current.Status = roomFollowThroughAwaitingInput
					current.NextAttemptAt = ""
					awaitingInput = true
				} else {
					delay := roomFollowThroughRetryDelay(current.Attempts)
					current.NextAttemptAt = time.Now().UTC().Add(delay).Format(time.RFC3339Nano)
					retry = true
				}
			} else {
				current.Status = roomFollowThroughDelivered
				current.DeliveredAt = now
				current.LastError = ""
				current.NextAttemptAt = ""
				current.ReceiptDigest = "sha256:" + roomFollowThroughDigest(strings.Join([]string{current.ID, current.DeliveryMessageID, thread.ID, now}, "\x00"))
				delivered++
			}
			break
		}
		_ = app.persistRoomFollowThroughLocked()
		app.roomFollowThroughMu.Unlock()
		if err != nil {
			log.Errorf("Room follow-through delivery failed id=%s trigger=%s: %v", id, trigger, err)
			if awaitingInput && record.RequesterEmail != "" {
				_, _ = app.createNotificationRecord(record.RequesterEmail, nil, notificationKindAgent,
					"Scout couldn't post the meeting recap because #"+record.DestinationTitle+" is no longer an authorized destination. Ask again with an exact accessible channel.",
					"chat", "", record.DestinationThreadID, "", record.DeliveryMessageID, record.DestinationTitle, scoutParticipantName, "", false)
			}
			if retry {
				delay := roomFollowThroughRetryDelay(attempts)
				time.AfterFunc(delay, func() {
					app.flushRoomFollowThroughForMeeting(record.RoomID, record.SittingID, "retry")
				})
			}
			continue
		}
		if record.RequesterEmail != "" {
			_, _ = app.createNotificationRecord(record.RequesterEmail, nil, notificationKindAgent,
				"Scout posted the meeting recap in #"+thread.Title+".", "chat", "", thread.ID, "",
				record.DeliveryMessageID, thread.Title, scoutParticipantName, trimForStorage(text, 500), false)
		}
	}
	return delivered
}

func roomFollowThroughAuthorizationFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrRetrievalSnapshotStale) || errors.Is(err, ErrBrainSourceConsentAbsent) ||
		errors.Is(err, ErrCatchUpUnauthorized) || errors.Is(err, ErrBrainEvidenceInvalid) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "authoriz") || strings.Contains(message, "audience changed")
}

func roomFollowThroughRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := roomFollowThroughRetryBase
	for index := 1; index < attempt && delay < roomFollowThroughRetryMax; index++ {
		delay *= 2
		if delay > roomFollowThroughRetryMax {
			delay = roomFollowThroughRetryMax
		}
	}
	return delay
}

func (app *kanbanBoardApp) resumeRoomFollowThroughAtBoot() {
	if app == nil || app.meetings == nil || app.roomFollowThroughStoreErr != nil {
		return
	}
	type scope struct{ roomID, sittingID string }
	seen := map[scope]bool{}
	app.roomFollowThroughMu.Lock()
	for _, record := range app.roomFollowThrough {
		if !oneOf(record.Status, roomFollowThroughQueued, roomFollowThroughDelivering) {
			continue
		}
		if meeting, active := app.meetings.activeRecord(record.RoomID); active && meeting.ID == record.SittingID {
			continue
		}
		seen[scope{record.RoomID, record.SittingID}] = true
	}
	app.roomFollowThroughMu.Unlock()
	for candidate := range seen {
		app.flushRoomFollowThroughForMeeting(candidate.roomID, candidate.sittingID, "boot_recovery")
	}
}

func roomFollowThroughIsMissingInput(err error) bool {
	return err != nil && !errors.Is(err, ErrRoomScoutFence)
}
