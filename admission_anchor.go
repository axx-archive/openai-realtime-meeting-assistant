package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const admissionAnchorFileFormat = 1

var (
	ErrAdmissionAnchorInvalid = errors.New("invalid admission anchor")
	ErrAdmissionAnchorStore   = errors.New("admission anchor store unavailable")
	// Test-only seam used to hold manual rollover at its durable successor
	// boundary while competing leave/media/chat operations probe ordering.
	manualRolloverBeforeSuccessorPersist func()
)

// AdmissionAnchor is the durable first-admission boundary for one principal
// in one sitting. Principal is identity, never the mutable room display name:
// members key on their normalized account email and guests on their hashed
// guest-session key.
type AdmissionAnchor struct {
	AnchorID  string `json:"anchorId"`
	TenantID  string `json:"tenantId"`
	RoomID    string `json:"roomId"`
	SittingID string `json:"sittingId"`
	// PendingRolloverFrom marks anchors staged for an occupied manual archive.
	// SittingStarts ignores them until the meetings store atomically closes the
	// predecessor and opens SittingID. This prevents a failed cross-file write
	// from being promoted into a successor during boot recovery.
	PendingRolloverFrom   string                `json:"pendingRolloverFrom,omitempty"`
	Principal             CanonicalPrincipalRef `json:"principal"`
	AdmittedAt            time.Time             `json:"admittedAt"`
	CaptureSequenceCutoff uint64                `json:"captureSequenceCutoff"`
	CaptureWatermark      time.Time             `json:"captureWatermark"`
}

type admissionAnchorFile struct {
	Format   int               `json:"format"`
	Records  []AdmissionAnchor `json:"records"`
	Checksum string            `json:"checksum"`
}

// AdmissionAnchorStore is a deliberately small restart-safe authority. Every
// upsert reloads while holding a process-shared file lock, applies MIN on the
// admitted timestamp, and durably replaces the checksummed snapshot before
// returning. This gives independent app instances the same uniqueness and
// first-writer semantics without introducing a second migration boundary.
type AdmissionAnchorStore struct {
	mu      sync.Mutex
	path    string
	records []AdmissionAnchor
}

func admissionAnchorsPath() string {
	if path := strings.TrimSpace(os.Getenv("ADMISSION_ANCHORS_PATH")); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "admission-anchors.json")
}

func OpenAdmissionAnchorStore(path string) (*AdmissionAnchorStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("%w: path required", ErrAdmissionAnchorStore)
	}
	lock, err := acquireAdmissionAnchorFileLock(path)
	if err != nil {
		return nil, fmt.Errorf("%w: lock writable store: %v", ErrAdmissionAnchorStore, err)
	}
	defer releaseAdmissionAnchorFileLock(lock)
	records, err := loadAdmissionAnchors(path)
	if err != nil {
		return nil, fmt.Errorf("%w: load: %w", ErrAdmissionAnchorStore, err)
	}
	// Rewrite the validated snapshot (including an empty first snapshot) to
	// prove the exact atomic-replace path is writable before readiness passes.
	if err := persistAdmissionAnchors(path, records); err != nil {
		return nil, fmt.Errorf("%w: writable probe: %v", ErrAdmissionAnchorStore, err)
	}
	return &AdmissionAnchorStore{path: path, records: records}, nil
}

func (app *kanbanBoardApp) initializeAdmissionAnchorStore(path string) error {
	if app == nil {
		return ErrAdmissionAnchorStore
	}
	app.admissionAnchorMu.Lock()
	defer app.admissionAnchorMu.Unlock()
	store, err := OpenAdmissionAnchorStore(path)
	if err != nil {
		app.admissionAnchors = nil
		app.admissionAnchorErr = err
		return err
	}
	app.admissionAnchors = store
	app.admissionAnchorErr = nil
	return nil
}

// admissionAnchorHealthError is the readiness seam for this fail-closed
// admission dependency. Startup may continue to serve non-room surfaces, but
// an unavailable/corrupt store remains explicit and every room admission is
// denied until a clean restart reopens it.
func (app *kanbanBoardApp) admissionAnchorHealthError() error {
	if app == nil {
		return ErrAdmissionAnchorStore
	}
	app.admissionAnchorMu.RLock()
	defer app.admissionAnchorMu.RUnlock()
	if app.admissionAnchorErr != nil {
		return app.admissionAnchorErr
	}
	if app.admissionAnchors == nil {
		return ErrAdmissionAnchorStore
	}
	return nil
}

func (app *kanbanBoardApp) latchAdmissionAnchorFailure(err error) {
	if app == nil || err == nil {
		return
	}
	app.admissionAnchorMu.Lock()
	defer app.admissionAnchorMu.Unlock()
	if app.admissionAnchorErr == nil {
		app.admissionAnchorErr = fmt.Errorf("%w: runtime persistence failure", ErrAdmissionAnchorStore)
	}
}

// RecordFirst persists candidate before returning it. A reconnect/second
// device with an equal or later admission time is a read of the existing row;
// only an earlier concurrently-observed admission can move the row backward.
// The cutoff and watermark always travel with the winning admitted_at value.
func (store *AdmissionAnchorStore) RecordFirst(ctx context.Context, candidate AdmissionAnchor) (AdmissionAnchor, error) {
	if store == nil {
		return AdmissionAnchor{}, ErrAdmissionAnchorStore
	}
	candidate = normalizeAdmissionAnchor(candidate)
	if err := validateAdmissionAnchor(candidate); err != nil {
		return AdmissionAnchor{}, err
	}
	select {
	case <-ctx.Done():
		return AdmissionAnchor{}, ctx.Err()
	default:
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	lock, err := acquireAdmissionAnchorFileLock(store.path)
	if err != nil {
		return AdmissionAnchor{}, fmt.Errorf("%w: lock: %v", ErrAdmissionAnchorStore, err)
	}
	defer releaseAdmissionAnchorFileLock(lock)

	records, err := loadAdmissionAnchors(store.path)
	if err != nil {
		return AdmissionAnchor{}, fmt.Errorf("%w: reload: %v", ErrAdmissionAnchorStore, err)
	}
	for index := range records {
		if !sameAdmissionAnchorKey(records[index], candidate) {
			continue
		}
		if !candidate.AdmittedAt.Before(records[index].AdmittedAt) {
			store.records = cloneAdmissionAnchors(records)
			return records[index], nil
		}
		records[index] = candidate
		if err := persistAdmissionAnchors(store.path, records); err != nil {
			return AdmissionAnchor{}, fmt.Errorf("%w: persist earlier admission: %v", ErrAdmissionAnchorStore, err)
		}
		store.records = cloneAdmissionAnchors(records)
		return candidate, nil
	}

	records = append(records, candidate)
	sortAdmissionAnchors(records)
	if err := persistAdmissionAnchors(store.path, records); err != nil {
		return AdmissionAnchor{}, fmt.Errorf("%w: persist first admission: %v", ErrAdmissionAnchorStore, err)
	}
	store.records = cloneAdmissionAnchors(records)
	return candidate, nil
}

func (store *AdmissionAnchorStore) Lookup(ctx context.Context, tenantID, roomID, sittingID string, principal CanonicalPrincipalRef) (AdmissionAnchor, bool, error) {
	if store == nil {
		return AdmissionAnchor{}, false, ErrAdmissionAnchorStore
	}
	key := normalizeAdmissionAnchor(AdmissionAnchor{TenantID: tenantID, RoomID: roomID, SittingID: sittingID, Principal: principal, AdmittedAt: time.Unix(0, 1).UTC()})
	if err := validateAdmissionAnchor(key); err != nil {
		return AdmissionAnchor{}, false, err
	}
	select {
	case <-ctx.Done():
		return AdmissionAnchor{}, false, ctx.Err()
	default:
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	lock, err := acquireAdmissionAnchorFileLock(store.path)
	if err != nil {
		return AdmissionAnchor{}, false, fmt.Errorf("%w: lock: %v", ErrAdmissionAnchorStore, err)
	}
	defer releaseAdmissionAnchorFileLock(lock)
	records, err := loadAdmissionAnchors(store.path)
	if err != nil {
		return AdmissionAnchor{}, false, fmt.Errorf("%w: reload: %v", ErrAdmissionAnchorStore, err)
	}
	store.records = cloneAdmissionAnchors(records)
	for _, record := range records {
		if sameAdmissionAnchorKey(record, key) {
			return record, true, nil
		}
	}
	return AdmissionAnchor{}, false, nil
}

// SittingStarts returns one authoritative earliest admission per durable
// sitting. The anchor file remains the authority: every read reloads and
// validates the checksummed snapshot under the process-shared lock. Callers use
// this only to repair a meeting-directory commit that was interrupted after an
// admission anchor became durable; it can therefore never invent a sitting.
func (store *AdmissionAnchorStore) SittingStarts(ctx context.Context) ([]AdmissionAnchor, error) {
	if store == nil {
		return nil, ErrAdmissionAnchorStore
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	lock, err := acquireAdmissionAnchorFileLock(store.path)
	if err != nil {
		return nil, fmt.Errorf("%w: lock: %v", ErrAdmissionAnchorStore, err)
	}
	defer releaseAdmissionAnchorFileLock(lock)
	records, err := loadAdmissionAnchors(store.path)
	if err != nil {
		return nil, fmt.Errorf("%w: reload: %v", ErrAdmissionAnchorStore, err)
	}
	store.records = cloneAdmissionAnchors(records)
	type sittingKey struct{ tenantID, roomID, sittingID string }
	earliest := make(map[sittingKey]AdmissionAnchor)
	for _, record := range records {
		if strings.TrimSpace(record.PendingRolloverFrom) != "" {
			continue
		}
		key := sittingKey{record.TenantID, normalizeRoomID(record.RoomID), strings.TrimSpace(record.SittingID)}
		if prior, ok := earliest[key]; !ok || record.AdmittedAt.Before(prior.AdmittedAt) {
			earliest[key] = record
		}
	}
	starts := make([]AdmissionAnchor, 0, len(earliest))
	for _, record := range earliest {
		starts = append(starts, record)
	}
	sort.Slice(starts, func(i, j int) bool {
		if starts[i].AdmittedAt.Equal(starts[j].AdmittedAt) {
			if starts[i].RoomID == starts[j].RoomID {
				return starts[i].SittingID < starts[j].SittingID
			}
			return starts[i].RoomID < starts[j].RoomID
		}
		return starts[i].AdmittedAt.Before(starts[j].AdmittedAt)
	})
	return starts, nil
}

type pendingAdmissionRollover struct {
	RoomID      string
	SittingID   string
	Predecessor string
}

// PendingRollovers returns the staged cross-file rollover identities. Boot
// uses this journal to finish only a rollover whose successor meeting record
// is already durable; otherwise it discards the staging rows and preserves the
// still-open predecessor.
func (store *AdmissionAnchorStore) PendingRollovers(ctx context.Context) ([]pendingAdmissionRollover, error) {
	if store == nil {
		return nil, ErrAdmissionAnchorStore
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	lock, err := acquireAdmissionAnchorFileLock(store.path)
	if err != nil {
		return nil, fmt.Errorf("%w: lock: %v", ErrAdmissionAnchorStore, err)
	}
	defer releaseAdmissionAnchorFileLock(lock)
	records, err := loadAdmissionAnchors(store.path)
	if err != nil {
		return nil, fmt.Errorf("%w: reload: %v", ErrAdmissionAnchorStore, err)
	}
	store.records = cloneAdmissionAnchors(records)
	seen := map[string]bool{}
	result := make([]pendingAdmissionRollover, 0)
	for _, record := range records {
		predecessor := strings.TrimSpace(record.PendingRolloverFrom)
		if predecessor == "" {
			continue
		}
		roomID := normalizeRoomID(record.RoomID)
		key := roomID + "\x00" + record.SittingID + "\x00" + predecessor
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, pendingAdmissionRollover{RoomID: roomID, SittingID: record.SittingID, Predecessor: predecessor})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].RoomID == result[j].RoomID {
			return result[i].SittingID < result[j].SittingID
		}
		return result[i].RoomID < result[j].RoomID
	})
	return result, nil
}

// ResolvePendingRollover either commits staged anchors after the successor
// meeting and memory identity are durable, or removes them after a failed
// meeting-store transaction. The update is one checksummed atomic replace.
func (store *AdmissionAnchorStore) ResolvePendingRollover(ctx context.Context, pending pendingAdmissionRollover, commit bool) error {
	if store == nil {
		return ErrAdmissionAnchorStore
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	lock, err := acquireAdmissionAnchorFileLock(store.path)
	if err != nil {
		return fmt.Errorf("%w: lock: %v", ErrAdmissionAnchorStore, err)
	}
	defer releaseAdmissionAnchorFileLock(lock)
	records, err := loadAdmissionAnchors(store.path)
	if err != nil {
		return fmt.Errorf("%w: reload: %v", ErrAdmissionAnchorStore, err)
	}
	changed := false
	resolved := make([]AdmissionAnchor, 0, len(records))
	for _, record := range records {
		matches := normalizeRoomID(record.RoomID) == normalizeRoomID(pending.RoomID) &&
			strings.TrimSpace(record.SittingID) == strings.TrimSpace(pending.SittingID) &&
			strings.TrimSpace(record.PendingRolloverFrom) == strings.TrimSpace(pending.Predecessor)
		if !matches {
			resolved = append(resolved, record)
			continue
		}
		changed = true
		if commit {
			record.PendingRolloverFrom = ""
			resolved = append(resolved, record)
		}
	}
	if !changed {
		store.records = cloneAdmissionAnchors(records)
		return nil
	}
	sortAdmissionAnchors(resolved)
	if err := persistAdmissionAnchors(store.path, resolved); err != nil {
		return fmt.Errorf("%w: persist pending rollover resolution: %v", ErrAdmissionAnchorStore, err)
	}
	store.records = cloneAdmissionAnchors(resolved)
	return nil
}

func memberAdmissionPrincipal(email string) CanonicalPrincipalRef {
	return CanonicalPrincipalRef{Kind: "user", ID: normalizeAccountEmail(email)}
}

func guestAdmissionPrincipal(sessionKey string) CanonicalPrincipalRef {
	return CanonicalPrincipalRef{Kind: "guest", ID: strings.TrimSpace(sessionKey)}
}

// captureAdmissionObservation linearizes the join boundary against raw source
// appends: append paths use the same memory mutex and durably advance the
// separate monotonic capture counter before appending transcript bytes. A
// crash may leave a harmless sequence gap but can never reuse a pre-admission
// sequence for post-admission content. The watermark remains room/sitting
// specific; a zero watermark honestly means this sitting has captured no source.
func (store *meetingMemoryStore) captureAdmissionObservation(roomID, sittingID string, now func() time.Time) (time.Time, uint64, time.Time, error) {
	if now == nil {
		now = time.Now
	}
	if store == nil {
		return now().UTC(), 0, time.Time{}, ErrAdmissionAnchorStore
	}
	roomID = normalizeRoomID(roomID)
	sittingID = strings.TrimSpace(sittingID)
	store.mu.Lock()
	defer store.mu.Unlock()
	admittedAt := now().UTC()
	var watermark time.Time
	if store.meetingEntryIndexes == nil || store.indexedEntryCount != len(store.entries) {
		store.rebuildMeetingEntryIndexesLocked()
	}
	// Admission is a live-meeting operation. Inspect only the selected sitting's
	// indexed rows rather than walking every historical transcript and artifact.
	for _, index := range store.meetingEntryIndexes[sittingID] {
		if index < 0 || index >= len(store.entries) {
			continue
		}
		if store.meetingEntryVisitHook != nil {
			store.meetingEntryVisitHook()
		}
		entry := store.entries[index]
		if entry.Kind != meetingMemoryKindTranscript || normalizeRoomID(entry.Metadata["roomId"]) != roomID || strings.TrimSpace(entry.Metadata["meetingId"]) != sittingID {
			continue
		}
		if entry.CreatedAt.After(watermark) {
			watermark = entry.CreatedAt.UTC()
		}
	}
	cutoff, err := currentDurableCaptureSequence(store.path, store.maxPersistedCaptureSequenceLocked())
	if err != nil {
		return admittedAt, 0, time.Time{}, fmt.Errorf("capture sequence high-water: %w", err)
	}
	return admittedAt, cutoff, watermark, nil
}

func (app *kanbanBoardApp) persistAdmissionAnchor(ctx context.Context, roomID, sittingID string, principal CanonicalPrincipalRef) (AdmissionAnchor, error) {
	return app.persistAdmissionAnchorWithRollover(ctx, roomID, sittingID, principal, "")
}

func (app *kanbanBoardApp) persistAdmissionAnchorWithRollover(ctx context.Context, roomID, sittingID string, principal CanonicalPrincipalRef, pendingRolloverFrom string) (AdmissionAnchor, error) {
	if err := app.admissionAnchorHealthError(); err != nil {
		return AdmissionAnchor{}, err
	}
	if app.memory == nil {
		return AdmissionAnchor{}, fmt.Errorf("%w: meeting memory unavailable", ErrAdmissionAnchorStore)
	}
	admittedAt, cutoff, watermark, err := app.memory.captureAdmissionObservation(roomID, sittingID, time.Now)
	if err != nil {
		app.latchAdmissionAnchorFailure(err)
		return AdmissionAnchor{}, err
	}
	anchor, err := app.admissionAnchors.RecordFirst(ctx, AdmissionAnchor{
		TenantID: canonicalTenantID(), RoomID: roomID, SittingID: sittingID, PendingRolloverFrom: pendingRolloverFrom, Principal: principal,
		AdmittedAt: admittedAt, CaptureSequenceCutoff: cutoff, CaptureWatermark: watermark,
	})
	if err != nil {
		app.latchAdmissionAnchorFailure(err)
		return AdmissionAnchor{}, err
	}
	return anchor, nil
}

type stagedManualRolloverSitting struct {
	pending      pendingAdmissionRollover
	startedAt    time.Time
	participants []string
}

// stageManualRolloverSittingLocked carries the exact currently admitted
// occupants across an explicit archive boundary. The caller holds app.mu, so a
// leave/join cannot split the occupant snapshot from its durable staged
// anchors. The staged marker keeps boot recovery from treating those anchors
// as a successor unless the matching atomic meetings-store rollover lands.
func (app *kanbanBoardApp) stageManualRolloverSittingLocked(ctx context.Context, roomID, predecessorID, sittingID string) (stagedManualRolloverSitting, error) {
	if app == nil || app.meetings == nil || app.memory == nil || strings.TrimSpace(sittingID) == "" {
		return stagedManualRolloverSitting{}, ErrMeetingRecordStore
	}
	roomID = normalizeRoomID(roomID)
	type carriedAdmission struct {
		name      string
		principal CanonicalPrincipalRef
	}
	state := app.roomLiveLocked(roomID)
	guestPrincipalByName := map[string]CanonicalPrincipalRef{}
	for sessionKey, display := range state.guestSeats {
		guestPrincipalByName[canonicalRoomParticipantName(display)] = guestAdmissionPrincipal(sessionKey)
	}
	carried := make([]carriedAdmission, 0, len(state.participants))
	for name, count := range state.participantCounts {
		name = canonicalRoomParticipantName(name)
		if name == "" || count <= 0 {
			continue
		}
		principal, guest := guestPrincipalByName[name]
		if !guest {
			principal = memberAdmissionPrincipal(participantEmail(name))
		}
		carried = append(carried, carriedAdmission{name: name, principal: principal})
	}
	if len(carried) == 0 {
		return stagedManualRolloverSitting{}, nil
	}
	sort.Slice(carried, func(i, j int) bool { return carried[i].name < carried[j].name })
	if manualRolloverBeforeSuccessorPersist != nil {
		manualRolloverBeforeSuccessorPersist()
	}
	participants := make([]string, 0, len(carried))
	var startedAt time.Time
	pending := pendingAdmissionRollover{RoomID: roomID, SittingID: strings.TrimSpace(sittingID), Predecessor: strings.TrimSpace(predecessorID)}
	for _, admission := range carried {
		anchor, err := app.persistAdmissionAnchorWithRollover(ctx, roomID, sittingID, admission.principal, predecessorID)
		if err != nil {
			if resolveErr := app.admissionAnchors.ResolvePendingRollover(context.Background(), pending, false); resolveErr != nil {
				app.latchAdmissionAnchorFailure(resolveErr)
			}
			return stagedManualRolloverSitting{}, err
		}
		participants = append(participants, admission.name)
		if startedAt.IsZero() || anchor.AdmittedAt.Before(startedAt) {
			startedAt = anchor.AdmittedAt
		}
	}
	return stagedManualRolloverSitting{pending: pending, startedAt: startedAt, participants: participants}, nil
}

// admitParticipantWithAnchor keeps unanchored presence invisible: capacity
// validation, durable anchor persistence, and the live-map commit are ordered
// under app.mu. A snapshot reader therefore observes either no new endpoint or
// a fully anchored endpoint, never the compensating-rollback interval.
func (app *kanbanBoardApp) admitParticipantWithAnchor(ctx context.Context, roomID, name, sessionID, endpointID, sittingID string, principal CanonicalPrincipalRef) (string, bool, error) {
	result, err := app.admitParticipantWithAnchorResult(ctx, roomID, name, sessionID, endpointID, sittingID, principal, false)
	return result.name, result.firstEndpoint, err
}

// admitParticipantWithAnchorResult is the linearization point for every member
// room join. Anchor persistence, the target-room seat, the new session lease,
// same-endpoint/transfer retirement, and one-account-one-room eviction all
// commit under the same app.mu hold. Socket and media closure are intentionally
// returned to the caller so no websocket I/O happens while app.mu is held.
func (app *kanbanBoardApp) admitParticipantWithAnchorResult(ctx context.Context, roomID, name, sessionID, endpointID, sittingID string, principal CanonicalPrincipalRef, transferExisting bool) (participantAdmissionResult, error) {
	app.meetingLifecycleMu.RLock()
	defer app.meetingLifecycleMu.RUnlock()
	roomID = normalizeRoomID(roomID)
	if room, ok := appRoomStore().byID(roomID); !ok {
		return participantAdmissionResult{}, errRoomNotFound
	} else if room.Archived {
		return participantAdmissionResult{}, errRoomArchived
	}
	name = canonicalRoomParticipantName(name)
	if name == "" {
		return participantAdmissionResult{}, fmt.Errorf("choose a listed participant and enter the room password")
	}
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	// A member rejoin of the exact participant session the host closed this
	// sitting is refused (members are otherwise free to come back).
	if err := roomEjectionRefusalLocked(state, "", name, sessionID); err != nil {
		app.mu.Unlock()
		return participantAdmissionResult{}, err
	}
	if transferExisting {
		if err := app.validateParticipantTransferAdmissionLocked(state, name); err != nil {
			app.mu.Unlock()
			return participantAdmissionResult{}, err
		}
	} else if err := app.validateParticipantAdmissionLocked(state, name, endpointID); err != nil {
		app.mu.Unlock()
		return participantAdmissionResult{}, err
	}
	anchor, err := app.persistAdmissionAnchor(ctx, roomID, sittingID, principal)
	if err != nil {
		app.mu.Unlock()
		return participantAdmissionResult{}, fmt.Errorf("%w: %v", ErrAdmissionAnchorStore, err)
	}
	if app.meetings == nil {
		app.mu.Unlock()
		return participantAdmissionResult{}, ErrMeetingRecordStore
	}
	priorMeeting, hadPriorMeeting := app.meetings.activeRecord(roomID)
	app.meetings.cancelIdleEnd(roomID)
	meeting, meetingChanged, err := app.meetings.startMeetingDurable(roomID, sittingID, anchor.AdmittedAt, []string{name})
	if err != nil {
		app.rearmMeetingIdleAfterFailedAdmissionLocked(roomID, state)
		app.mu.Unlock()
		return participantAdmissionResult{}, fmt.Errorf("%w: %v", ErrMeetingRecordStore, err)
	}
	closedPriorMeetingID := app.closedPriorMeetingID(priorMeeting, hadPriorMeeting, meeting)
	if meeting.ID != strings.TrimSpace(sittingID) {
		app.rearmMeetingIdleAfterFailedAdmissionLocked(roomID, state)
		app.mu.Unlock()
		app.scheduleMeetingCoreFinalization(closedPriorMeetingID)
		return participantAdmissionResult{}, fmt.Errorf("%w: admitted sitting mismatch", ErrMeetingRecordStore)
	}
	var result participantAdmissionResult
	if transferExisting {
		result, err = app.transferParticipantSessionEndpointInRoomWithLeaseLocked(state, name, sessionID, endpointID)
	} else {
		result, err = app.admitParticipantSessionEndpointInRoomWithLeaseLocked(state, name, sessionID, endpointID)
	}
	if err != nil {
		app.rearmMeetingIdleAfterFailedAdmissionLocked(roomID, state)
		app.mu.Unlock()
		app.scheduleMeetingCoreFinalization(closedPriorMeetingID)
		return participantAdmissionResult{}, err
	}
	// A new session of a host-muted name was never asked to mute and its
	// audio is not dropped: the roster must not keep claiming the mute.
	clearHostMuteForNewSessionLocked(state, name)
	result.meeting = meeting
	result.meetingChanged = meetingChanged
	result.retired = append(result.retired, app.retireParticipantSeatsOutsideRoomLocked(name, roomID)...)
	app.mu.Unlock()
	app.scheduleMeetingCoreFinalization(closedPriorMeetingID)
	// Retirement becomes visible atomically under app.mu, then drains any
	// already-running lease-gated install/grant after app.mu is released. This
	// preserves linearization without holding the global room lock across a
	// websocket write.
	drainParticipantAdmissionRetirements(result.retired)
	changedRooms := map[string]bool{}
	if result.firstEndpoint {
		changedRooms[normalizeRoomID(roomID)] = true
	}
	for _, retirement := range result.retired {
		if retirement.roomID != normalizeRoomID(roomID) {
			changedRooms[retirement.roomID] = true
		}
	}
	for changedRoomID := range changedRooms {
		app.revokeMeetingSpecialistParticipantAuthority(changedRoomID, "participant_authority_changed")
	}
	return result, nil
}

// admitParticipantTransferWithAnchor preserves the admission-anchor ordering
// while atomically replacing every older same-account endpoint in this room's
// live state. The returned session ids are already retired from the seat and
// can be removed from the media registry/closed after app.mu is released.
func (app *kanbanBoardApp) admitParticipantTransferWithAnchor(ctx context.Context, roomID, name, sessionID, endpointID, sittingID string, principal CanonicalPrincipalRef) (string, bool, []string, error) {
	result, err := app.admitParticipantWithAnchorResult(ctx, roomID, name, sessionID, endpointID, sittingID, principal, true)
	if err != nil {
		return "", false, nil, err
	}
	retiredSessionIDs := make([]string, 0, len(result.retired))
	for _, retired := range result.retired {
		retiredSessionIDs = append(retiredSessionIDs, retired.sessionID)
	}
	return result.name, result.firstEndpoint, retiredSessionIDs, nil
}

func (app *kanbanBoardApp) admitGuestWithAnchorResult(ctx context.Context, roomID, sessionKey, requestedName, participantSessionID, sittingID string) (participantAdmissionResult, error) {
	app.meetingLifecycleMu.RLock()
	defer app.meetingLifecycleMu.RUnlock()
	roomID = normalizeRoomID(roomID)
	if room, ok := appRoomStore().byID(roomID); !ok {
		return participantAdmissionResult{}, errRoomNotFound
	} else if room.Archived {
		return participantAdmissionResult{}, errRoomArchived
	}
	base := strings.TrimSpace(requestedName)
	if base == "" {
		base = "Guest"
	}
	app.mu.Lock()
	state := app.roomLiveLocked(roomID)
	display, seated := state.guestSeats[sessionKey]
	if !seated {
		if len(state.guestSeats) >= maxGuestsPerRoom() {
			app.mu.Unlock()
			return participantAdmissionResult{}, errGuestRoomFull
		}
		display = dedupeGuestDisplayNameLocked(state, guestNamePrefix+base)
	}
	// A seat the host removed this sitting (same guest link, or the same
	// display name) is refused before any anchor or meeting side effect.
	if err := roomEjectionRefusalLocked(state, sessionKey, display, participantSessionID); err != nil {
		app.mu.Unlock()
		return participantAdmissionResult{}, err
	}
	if err := app.validateParticipantAdmissionLocked(state, display, participantSessionID); err != nil {
		app.mu.Unlock()
		return participantAdmissionResult{}, err
	}
	anchor, err := app.persistAdmissionAnchor(ctx, roomID, sittingID, guestAdmissionPrincipal(sessionKey))
	if err != nil {
		app.mu.Unlock()
		return participantAdmissionResult{}, fmt.Errorf("%w: %v", ErrAdmissionAnchorStore, err)
	}
	if app.meetings == nil {
		app.mu.Unlock()
		return participantAdmissionResult{}, ErrMeetingRecordStore
	}
	priorMeeting, hadPriorMeeting := app.meetings.activeRecord(roomID)
	app.meetings.cancelIdleEnd(roomID)
	meeting, meetingChanged, err := app.meetings.startMeetingDurable(roomID, sittingID, anchor.AdmittedAt, []string{display})
	if err != nil {
		app.rearmMeetingIdleAfterFailedAdmissionLocked(roomID, state)
		app.mu.Unlock()
		return participantAdmissionResult{}, fmt.Errorf("%w: %v", ErrMeetingRecordStore, err)
	}
	closedPriorMeetingID := app.closedPriorMeetingID(priorMeeting, hadPriorMeeting, meeting)
	if meeting.ID != strings.TrimSpace(sittingID) {
		app.rearmMeetingIdleAfterFailedAdmissionLocked(roomID, state)
		app.mu.Unlock()
		app.scheduleMeetingCoreFinalization(closedPriorMeetingID)
		return participantAdmissionResult{}, fmt.Errorf("%w: admitted sitting mismatch", ErrMeetingRecordStore)
	}
	if !seated {
		state.guestSeats[sessionKey] = display
	}
	result, err := app.admitParticipantSessionEndpointInRoomWithLeaseLocked(state, display, participantSessionID, participantSessionID)
	if err != nil && !seated {
		delete(state.guestSeats, sessionKey)
	}
	if err != nil {
		app.rearmMeetingIdleAfterFailedAdmissionLocked(roomID, state)
	} else {
		// A new session of a host-muted name was never asked to mute and its
		// audio is not dropped: the roster must not keep claiming the mute.
		clearHostMuteForNewSessionLocked(state, display)
	}
	app.mu.Unlock()
	app.scheduleMeetingCoreFinalization(closedPriorMeetingID)
	result.meeting = meeting
	result.meetingChanged = meetingChanged
	drainParticipantAdmissionRetirements(result.retired)
	if err == nil && result.firstEndpoint {
		app.revokeMeetingSpecialistParticipantAuthority(roomID, "participant_authority_changed")
	}
	return result, err
}

// rearmMeetingIdleAfterFailedAdmissionLocked restores the empty-room close
// guard after anchored admission canceled it but could not publish a live
// seat. The durable anchor/meeting may legitimately remain for crash recovery;
// it must not remain open forever merely because the admission failed after
// cancelIdleEnd. The caller holds app.mu, so an unobserved seat cannot race the
// emptiness decision.
func (app *kanbanBoardApp) rearmMeetingIdleAfterFailedAdmissionLocked(roomID string, state *roomLiveState) {
	if app == nil || app.meetings == nil || state == nil || app.activeParticipantCountInRoomLocked(state) > 0 {
		return
	}
	record, open := app.meetings.activeRecord(roomID)
	if !open {
		return
	}
	if _, _, err := app.meetings.armIdleEndDurable(roomID, record.ID, time.Now().UTC(), func(generation uint64) { app.endMeetingForIdle(roomID, generation) }); err != nil {
		log.Errorf("Could not restore empty-room deadline for meeting %s after failed admission: %v", record.ID, err)
	}
}

// closedPriorMeetingID identifies the defensive same-room restart performed by
// startMeetingDurable. Scheduling happens after app.mu is released, so the old
// sitting receives its core receipts in-process without putting provider work
// on the admission critical path.
func (app *kanbanBoardApp) closedPriorMeetingID(prior meetingRecord, hadPrior bool, current meetingRecord) string {
	if app == nil || app.meetings == nil || !hadPrior || strings.TrimSpace(prior.ID) == "" || prior.ID == current.ID {
		return ""
	}
	closed, found := app.meetings.recordByID(prior.ID)
	if !found || strings.TrimSpace(closed.EndedAt) == "" || closed.Finalization == nil {
		return ""
	}
	return closed.ID
}

func (app *kanbanBoardApp) admitGuestWithAnchor(ctx context.Context, roomID, sessionKey, requestedName, participantSessionID, sittingID string) (string, bool, error) {
	result, err := app.admitGuestWithAnchorResult(ctx, roomID, sessionKey, requestedName, participantSessionID, sittingID)
	return result.name, result.firstEndpoint, err
}

func normalizeAdmissionAnchor(anchor AdmissionAnchor) AdmissionAnchor {
	anchor.AnchorID = strings.TrimSpace(anchor.AnchorID)
	anchor.TenantID = strings.TrimSpace(anchor.TenantID)
	anchor.RoomID = normalizeRoomID(anchor.RoomID)
	anchor.SittingID = strings.TrimSpace(anchor.SittingID)
	anchor.PendingRolloverFrom = strings.TrimSpace(anchor.PendingRolloverFrom)
	anchor.Principal.Kind = strings.ToLower(strings.TrimSpace(anchor.Principal.Kind))
	anchor.Principal.ID = strings.TrimSpace(anchor.Principal.ID)
	if anchor.Principal.Kind == "user" {
		anchor.Principal.ID = normalizeAccountEmail(anchor.Principal.ID)
	}
	anchor.AdmittedAt = anchor.AdmittedAt.UTC()
	if !anchor.CaptureWatermark.IsZero() {
		anchor.CaptureWatermark = anchor.CaptureWatermark.UTC()
	}
	if anchor.AnchorID == "" {
		anchor.AnchorID = deterministicAdmissionAnchorID(anchor)
	}
	return anchor
}

func validateAdmissionAnchor(anchor AdmissionAnchor) error {
	if anchor.AnchorID == "" || anchor.TenantID == "" || anchor.RoomID == "" || anchor.SittingID == "" || anchor.Principal.ID == "" || anchor.AdmittedAt.IsZero() {
		return ErrAdmissionAnchorInvalid
	}
	if anchor.Principal.Kind != "user" && anchor.Principal.Kind != "guest" {
		return fmt.Errorf("%w: principal kind %q", ErrAdmissionAnchorInvalid, anchor.Principal.Kind)
	}
	if anchor.Principal.Kind == "guest" && !isHexDigest(anchor.Principal.ID) {
		return fmt.Errorf("%w: guest principal must be a one-way session digest", ErrAdmissionAnchorInvalid)
	}
	if expected := deterministicAdmissionAnchorID(anchor); anchor.AnchorID != expected {
		return fmt.Errorf("%w: anchor id does not match identity", ErrAdmissionAnchorInvalid)
	}
	return nil
}

func deterministicAdmissionAnchorID(anchor AdmissionAnchor) string {
	identity := struct {
		TenantID  string                `json:"tenantId"`
		RoomID    string                `json:"roomId"`
		SittingID string                `json:"sittingId"`
		Principal CanonicalPrincipalRef `json:"principal"`
	}{
		TenantID: anchor.TenantID, RoomID: anchor.RoomID, SittingID: anchor.SittingID, Principal: anchor.Principal,
	}
	raw, _ := json.Marshal(identity) // fixed string-only struct cannot fail
	sum := sha256.Sum256(raw)
	return "admission-anchor-" + hex.EncodeToString(sum[:])
}

func sameAdmissionAnchorKey(left, right AdmissionAnchor) bool {
	return left.TenantID == right.TenantID && left.RoomID == right.RoomID && left.SittingID == right.SittingID &&
		left.Principal.Kind == right.Principal.Kind && left.Principal.ID == right.Principal.ID
}

func loadAdmissionAnchors(path string) ([]AdmissionAnchor, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var disk admissionAnchorFile
	if err := decoder.Decode(&disk); err != nil {
		return nil, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	if disk.Format != admissionAnchorFileFormat {
		return nil, fmt.Errorf("unsupported format %d", disk.Format)
	}
	for index := range disk.Records {
		disk.Records[index] = normalizeAdmissionAnchor(disk.Records[index])
		if err := validateAdmissionAnchor(disk.Records[index]); err != nil {
			return nil, err
		}
		if index > 0 && sameAdmissionAnchorKey(disk.Records[index-1], disk.Records[index]) {
			return nil, errors.New("duplicate admission anchor key")
		}
	}
	want, err := admissionAnchorChecksum(disk.Records)
	if err != nil {
		return nil, err
	}
	if disk.Checksum != want {
		return nil, errors.New("admission anchor checksum mismatch")
	}
	return cloneAdmissionAnchors(disk.Records), nil
}

func persistAdmissionAnchors(path string, records []AdmissionAnchor) error {
	records = cloneAdmissionAnchors(records)
	for index := range records {
		records[index] = normalizeAdmissionAnchor(records[index])
		if err := validateAdmissionAnchor(records[index]); err != nil {
			return err
		}
	}
	sortAdmissionAnchors(records)
	checksum, err := admissionAnchorChecksum(records)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(admissionAnchorFile{Format: admissionAnchorFileFormat, Records: records, Checksum: checksum})
	if err != nil {
		return err
	}
	// Use the shared W1 durable-file seam. Admission anchors are not a legacy
	// canonical import family today, so this is an ordinary durable replace;
	// if the family is registered later the same call site acquires the
	// canonical mutation fence instead of silently bypassing it.
	return writeFileAtomicallyDurable(path, raw, 0o600)
}

func admissionAnchorChecksum(records []AdmissionAnchor) (string, error) {
	if records == nil {
		records = []AdmissionAnchor{}
	}
	raw, err := canonicalJSON(records)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func sortAdmissionAnchors(records []AdmissionAnchor) {
	sort.Slice(records, func(i, j int) bool {
		left, right := records[i], records[j]
		if left.TenantID != right.TenantID {
			return left.TenantID < right.TenantID
		}
		if left.RoomID != right.RoomID {
			return left.RoomID < right.RoomID
		}
		if left.SittingID != right.SittingID {
			return left.SittingID < right.SittingID
		}
		if left.Principal.Kind != right.Principal.Kind {
			return left.Principal.Kind < right.Principal.Kind
		}
		return left.Principal.ID < right.Principal.ID
	})
}

func cloneAdmissionAnchors(records []AdmissionAnchor) []AdmissionAnchor {
	return append([]AdmissionAnchor(nil), records...)
}

func acquireAdmissionAnchorFileLock(path string) (*os.File, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	lockPath := path + ".lock"
	_, statErr := os.Stat(lockPath)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return nil, statErr
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if created {
		if err := syncCanonicalParentDir(lockPath); err != nil {
			lock.Close()
			return nil, err
		}
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		lock.Close()
		return nil, err
	}
	return lock, nil
}

func releaseAdmissionAnchorFileLock(lock *os.File) {
	if lock == nil {
		return
	}
	_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	_ = lock.Close()
}
