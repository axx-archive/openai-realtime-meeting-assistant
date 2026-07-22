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
)

// AdmissionAnchor is the durable first-admission boundary for one principal
// in one sitting. Principal is identity, never the mutable room display name:
// members key on their normalized account email and guests on their hashed
// guest-session key.
type AdmissionAnchor struct {
	AnchorID              string                `json:"anchorId"`
	TenantID              string                `json:"tenantId"`
	RoomID                string                `json:"roomId"`
	SittingID             string                `json:"sittingId"`
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
	for _, entry := range store.entries {
		if entry.Kind != meetingMemoryKindTranscript || normalizeRoomID(entry.Metadata["roomId"]) != roomID || strings.TrimSpace(entry.Metadata["meetingId"]) != sittingID {
			continue
		}
		if entry.CreatedAt.After(watermark) {
			watermark = entry.CreatedAt.UTC()
		}
	}
	cutoff, err := currentDurableCaptureSequence(store.path, maxPersistedCaptureSequence(store.entries))
	if err != nil {
		return admittedAt, 0, time.Time{}, fmt.Errorf("capture sequence high-water: %w", err)
	}
	return admittedAt, cutoff, watermark, nil
}

func (app *kanbanBoardApp) persistAdmissionAnchor(ctx context.Context, roomID, sittingID string, principal CanonicalPrincipalRef) (AdmissionAnchor, error) {
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
		TenantID: canonicalTenantID(), RoomID: roomID, SittingID: sittingID, Principal: principal,
		AdmittedAt: admittedAt, CaptureSequenceCutoff: cutoff, CaptureWatermark: watermark,
	})
	if err != nil {
		app.latchAdmissionAnchorFailure(err)
		return AdmissionAnchor{}, err
	}
	return anchor, nil
}

// admitParticipantWithAnchor keeps unanchored presence invisible: capacity
// validation, durable anchor persistence, and the live-map commit are ordered
// under app.mu. A snapshot reader therefore observes either no new endpoint or
// a fully anchored endpoint, never the compensating-rollback interval.
func (app *kanbanBoardApp) admitParticipantWithAnchor(ctx context.Context, roomID, name, sessionID, endpointID, sittingID string, principal CanonicalPrincipalRef) (string, bool, error) {
	name = canonicalRoomParticipantName(name)
	if name == "" {
		return "", false, fmt.Errorf("choose a listed participant and enter the room password")
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)
	if err := app.validateParticipantAdmissionLocked(state, name, endpointID); err != nil {
		return "", false, err
	}
	if _, err := app.persistAdmissionAnchor(ctx, roomID, sittingID, principal); err != nil {
		return "", false, fmt.Errorf("%w: %v", ErrAdmissionAnchorStore, err)
	}
	return app.admitParticipantSessionEndpointInRoomLocked(state, name, sessionID, endpointID)
}

func (app *kanbanBoardApp) admitGuestWithAnchor(ctx context.Context, roomID, sessionKey, requestedName, participantSessionID, sittingID string) (string, bool, error) {
	roomID = normalizeRoomID(roomID)
	base := strings.TrimSpace(requestedName)
	if base == "" {
		base = "Guest"
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)
	display, seated := state.guestSeats[sessionKey]
	if !seated {
		if len(state.guestSeats) >= maxGuestsPerRoom() {
			return "", false, errGuestRoomFull
		}
		display = dedupeGuestDisplayNameLocked(state, guestNamePrefix+base)
	}
	if err := app.validateParticipantAdmissionLocked(state, display, participantSessionID); err != nil {
		return "", false, err
	}
	if _, err := app.persistAdmissionAnchor(ctx, roomID, sittingID, guestAdmissionPrincipal(sessionKey)); err != nil {
		return "", false, fmt.Errorf("%w: %v", ErrAdmissionAnchorStore, err)
	}
	if !seated {
		state.guestSeats[sessionKey] = display
	}
	admitted, firstEndpoint, err := app.admitParticipantSessionEndpointInRoomLocked(state, display, participantSessionID, participantSessionID)
	if err != nil && !seated {
		delete(state.guestSeats, sessionKey)
	}
	return admitted, firstEndpoint, err
}

func normalizeAdmissionAnchor(anchor AdmissionAnchor) AdmissionAnchor {
	anchor.AnchorID = strings.TrimSpace(anchor.AnchorID)
	anchor.TenantID = strings.TrimSpace(anchor.TenantID)
	anchor.RoomID = normalizeRoomID(anchor.RoomID)
	anchor.SittingID = strings.TrimSpace(anchor.SittingID)
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
