package main

// Wave 7 D2 — recording to blob, DEFAULT OFF (founder decision #4). The
// client's MediaRecorder streams webm chunks to POST
// /assistant/meetings/recording/upload; chunks append into a temp part file
// and the `final` chunk assembles the recording into the content-addressed
// blob store, then attaches it to the Meeting Record as the `recording`
// output stage. Three gates guard every chunk, all fail-closed:
//
//  1. the room's manager-only `recordingEnabled` setting (a JSON side-store
//     beside meetings.json — roomRecord itself is untouched);
//  2. the uploader is a signed-in participant of that exact meeting;
//  3. every currently-admitted participant holds the `recording` consent lane
//     (consent_lanes.go). Members ride the company's rules of the road like
//     transcription; an external guest must grant it explicitly — recording
//     never rides the guest default-allow baseline. A later denial or
//     withdrawal on the sitting refuses further chunks and drops the parts.
//
// Recordings are as sensitive as transcripts: playback goes through the
// session-gated /artifacts/blob route and is authorized exactly like the
// Meeting Record that owns the ref (meetingRecordingBlobAuthorized).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	meetingRecordingDefaultMaxBytes    = 100 << 20
	meetingRecordingMultipartMemory    = 8 << 20
	meetingRecordingFramingHeadroom    = 1 << 20
	meetingRecordingMimeVideo          = "video/webm"
	meetingRecordingMimeAudio          = "audio/webm"
	meetingRecordingStateUploading     = "uploading"
	meetingRecordingStateStored        = "stored"
	meetingRecordingStateRefused       = "refused"
	meetingRecordingMaxDurationSeconds = 24 * 60 * 60
	meetingRecordingStageDisposition   = "media_recording"
	// meetingRecordingDefaultUploadTTL bounds an abandoned chunk stream: an
	// `uploading` record whose last chunk is older than this is refused and its
	// raw part file removed by the liveness sweeper (env MEETING_RECORDING_UPLOAD_TTL).
	meetingRecordingDefaultUploadTTL = 2 * time.Hour
)

var (
	errMeetingRecordingPartOrder      = errors.New("recording part is out of order")
	errMeetingRecordingTooLarge       = errors.New("recording exceeds the size cap")
	errMeetingRecordingStored         = errors.New("recording is already stored")
	errMeetingRecordingRefused        = errors.New("recording was refused")
	errMeetingRecordingConsentRevoked = errors.New("recording consent was withdrawn")
	errMeetingRecordingEmpty          = errors.New("recording is empty")
	errMeetingRecordingMime           = errors.New("recording mime must be video/webm or audio/webm")
	errMeetingRecordingAbandoned      = errors.New("recording upload was abandoned")
	errMeetingRecordingDisabled       = errors.New("recording was turned off for this room")
)

// meetingRecordingMaxBytes is the per-recording cap (env
// MEETING_RECORDING_MAX_BYTES, default 100MB). Exceeding it answers 413 and
// marks the recording refused.
func meetingRecordingMaxBytes() int {
	if raw := strings.TrimSpace(os.Getenv("MEETING_RECORDING_MAX_BYTES")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 && parsed <= 1<<31-1 {
			return int(parsed)
		}
	}
	return meetingRecordingDefaultMaxBytes
}

// meetingRecordingUploadTTL is how long an in-flight upload may sit without a
// new chunk before the sweeper reaps it.
func meetingRecordingUploadTTL() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("MEETING_RECORDING_UPLOAD_TTL")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return meetingRecordingDefaultUploadTTL
}

func normalizeMeetingRecordingMime(raw string) (string, error) {
	mime := strings.ToLower(strings.TrimSpace(raw))
	if index := strings.Index(mime, ";"); index >= 0 {
		mime = strings.TrimSpace(mime[:index])
	}
	switch mime {
	case "":
		return meetingRecordingMimeVideo, nil
	case meetingRecordingMimeVideo, meetingRecordingMimeAudio:
		return mime, nil
	default:
		return "", errMeetingRecordingMime
	}
}

/* ---------- durable side-store ---------- */

// roomRecordingSetting is the manager-controlled per-room switch. Absent
// means false: shipping this code is not activation.
type roomRecordingSetting struct {
	RecordingEnabled bool   `json:"recordingEnabled"`
	UpdatedBy        string `json:"updatedBy,omitempty"`
	UpdatedAt        string `json:"updatedAt,omitempty"`
}

// meetingRecordingRecord tracks one meeting's upload from first chunk to
// stored blob (or refusal). Keyed by meeting id == sitting id.
type meetingRecordingRecord struct {
	MeetingID        string `json:"meetingId"`
	RoomID           string `json:"roomId"`
	State            string `json:"state"`
	Mime             string `json:"mime"`
	UploadedBy       string `json:"uploadedBy"`
	StartedAt        string `json:"startedAt"`
	UpdatedAt        string `json:"updatedAt"`
	PartCount        int    `json:"partCount"`
	Bytes            int64  `json:"bytes"`
	ConsentCheckedAt string `json:"consentCheckedAt,omitempty"`
	ConsentRevokedAt string `json:"consentRevokedAt,omitempty"`
	Ref              string `json:"ref,omitempty"`
	Size             int64  `json:"size,omitempty"`
	DurationSeconds  int    `json:"durationSeconds,omitempty"`
	StoredAt         string `json:"storedAt,omitempty"`
	Error            string `json:"error,omitempty"`
}

type meetingRecordingStoreState struct {
	RoomSettings map[string]roomRecordingSetting   `json:"roomSettings"`
	Recordings   map[string]meetingRecordingRecord `json:"recordings"`
}

type meetingRecordingStore struct {
	mu       sync.Mutex
	path     string
	partsDir string
	state    meetingRecordingStoreState
	loadErr  error
}

func meetingRecordingsPath() string {
	if path := strings.TrimSpace(os.Getenv("MEETING_RECORDINGS_PATH")); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(meetingsPath()), "meeting-recordings.json")
}

var (
	meetingRecordingStoreMu    sync.Mutex
	meetingRecordingStoreCache = map[string]*meetingRecordingStore{}
)

func appMeetingRecordingStore() *meetingRecordingStore {
	path := meetingRecordingsPath()
	meetingRecordingStoreMu.Lock()
	defer meetingRecordingStoreMu.Unlock()
	if store, ok := meetingRecordingStoreCache[path]; ok {
		return store
	}
	store := newMeetingRecordingStore(path)
	meetingRecordingStoreCache[path] = store
	return store
}

// appMeetingRecordingStoreIfOpen never creates a store: the consent listener
// and read-only projections use it so a notice cannot conjure the file.
func appMeetingRecordingStoreIfOpen() *meetingRecordingStore {
	meetingRecordingStoreMu.Lock()
	defer meetingRecordingStoreMu.Unlock()
	return meetingRecordingStoreCache[meetingRecordingsPath()]
}

func newMeetingRecordingStore(path string) *meetingRecordingStore {
	store := &meetingRecordingStore{
		path:     path,
		partsDir: filepath.Join(filepath.Dir(path), "meeting-recording-parts"),
		state:    meetingRecordingStoreState{RoomSettings: map[string]roomRecordingSetting{}, Recordings: map[string]meetingRecordingRecord{}},
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			store.loadErr = fmt.Errorf("meeting recording store is unavailable")
		}
		return store
	}
	var state meetingRecordingStoreState
	if err := json.Unmarshal(raw, &state); err != nil {
		store.loadErr = fmt.Errorf("meeting recording store is malformed")
		return store
	}
	if state.RoomSettings == nil {
		state.RoomSettings = map[string]roomRecordingSetting{}
	}
	if state.Recordings == nil {
		state.Recordings = map[string]meetingRecordingRecord{}
	}
	store.state = state
	return store
}

func (s *meetingRecordingStore) persistLocked() error {
	if s.loadErr != nil {
		return s.loadErr
	}
	return writeJSONFileAtomically(s.path, "meeting recording store", s.state)
}

var meetingRecordingPartNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,120}$`)

func (s *meetingRecordingStore) partPath(meetingID string) string {
	name := strings.TrimSpace(meetingID)
	if !meetingRecordingPartNamePattern.MatchString(name) {
		name = sha256Hex([]byte(name))
	}
	return filepath.Join(s.partsDir, name+".part")
}

func (s *meetingRecordingStore) setting(roomID string) roomRecordingSetting {
	if s == nil {
		return roomRecordingSetting{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.RoomSettings[normalizeRoomID(roomID)]
}

func (s *meetingRecordingStore) setRecordingEnabled(roomID string, enabled bool, updatedBy string, now time.Time) (roomRecordingSetting, error) {
	if s.loadErr != nil {
		return roomRecordingSetting{}, s.loadErr
	}
	roomID = normalizeRoomID(roomID)
	s.mu.Lock()
	defer s.mu.Unlock()
	prior, hadPrior := s.state.RoomSettings[roomID]
	stamp := now.UTC().Format(time.RFC3339Nano)
	setting := roomRecordingSetting{RecordingEnabled: enabled, UpdatedBy: normalizeAccountEmail(updatedBy), UpdatedAt: stamp}
	s.state.RoomSettings[roomID] = setting
	// Turning recording off ends every in-flight upload in the room: the
	// consent banner is gone, so no further chunk may land and the raw parts
	// already received are dropped. Persist FIRST: the refused records ride
	// the same write as the setting, and the part files are only removed once
	// that write has landed. A persist failure rolls back the setting AND the
	// records, leaving every in-flight upload exactly as it was.
	var reaped []string
	priorRecords := map[string]meetingRecordingRecord{}
	if !enabled {
		for meetingID, record := range s.state.Recordings {
			if normalizeRoomID(record.RoomID) != roomID || record.State != meetingRecordingStateUploading {
				continue
			}
			priorRecords[meetingID] = record
			record.State = meetingRecordingStateRefused
			record.Error = errMeetingRecordingDisabled.Error()
			record.UpdatedAt = stamp
			s.state.Recordings[meetingID] = record
			reaped = append(reaped, meetingID)
		}
	}
	if err := s.persistLocked(); err != nil {
		if hadPrior {
			s.state.RoomSettings[roomID] = prior
		} else {
			delete(s.state.RoomSettings, roomID)
		}
		for meetingID, record := range priorRecords {
			s.state.Recordings[meetingID] = record
		}
		return roomRecordingSetting{}, err
	}
	for _, meetingID := range reaped {
		_ = os.Remove(s.partPath(meetingID))
		log.Infof("Recording upload for meeting %s refused: recording turned off for room %s", meetingID, roomID)
	}
	return setting, nil
}

// refuseLocked flips one in-flight record to refused with reason and drops its
// raw part file. Callers hold s.mu and persist afterwards.
func (s *meetingRecordingStore) refuseLocked(meetingID string, reason error, now time.Time) {
	record, ok := s.state.Recordings[meetingID]
	if !ok || record.State != meetingRecordingStateUploading {
		return
	}
	record.State = meetingRecordingStateRefused
	record.Error = reason.Error()
	record.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	s.state.Recordings[meetingID] = record
	_ = os.Remove(s.partPath(meetingID))
}

// sweepStaleUploads reaps `uploading` records whose last chunk is older than
// ttl: the tab closed, the network dropped, or the client never sent `final`.
// Returns the refused meeting ids. Fresh uploads are untouched.
func (s *meetingRecordingStore) sweepStaleUploads(now time.Time, ttl time.Duration) []string {
	if s == nil || s.loadErr != nil || ttl <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var reaped []string
	for meetingID, record := range s.state.Recordings {
		if record.State != meetingRecordingStateUploading {
			continue
		}
		last, err := time.Parse(time.RFC3339Nano, firstNonEmptyString(record.UpdatedAt, record.StartedAt))
		if err != nil || now.Sub(last) <= ttl {
			continue
		}
		s.refuseLocked(meetingID, errMeetingRecordingAbandoned, now)
		reaped = append(reaped, meetingID)
	}
	if len(reaped) == 0 {
		return nil
	}
	sort.Strings(reaped)
	if err := s.persistLocked(); err != nil {
		log.Errorf("Could not persist abandoned recording sweep: %v", err)
	}
	return reaped
}

// sweepAbandonedMeetingRecordingUploads rides the participant liveness ticker
// (kanban.go startParticipantLivenessSweeper) — no ticker of its own. Only an
// already-open store is swept, so the sweep never creates the file.
func sweepAbandonedMeetingRecordingUploads() {
	store := appMeetingRecordingStoreIfOpen()
	if store == nil {
		return
	}
	for _, meetingID := range store.sweepStaleUploads(time.Now().UTC(), meetingRecordingUploadTTL()) {
		log.Infof("Recording upload for meeting %s abandoned past %s; parts removed", meetingID, meetingRecordingUploadTTL())
	}
}

func (s *meetingRecordingStore) recording(meetingID string) (meetingRecordingRecord, bool) {
	if s == nil {
		return meetingRecordingRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.state.Recordings[strings.TrimSpace(meetingID)]
	return record, ok
}

// appendPart accepts chunk partIndex for meetingID. Parts must arrive in
// order (a re-delivered earlier part is acknowledged without appending);
// crossing maxBytes refuses the whole recording and drops its parts.
func (s *meetingRecordingStore) appendPart(meetingID, roomID, uploadedBy, mime string, partIndex int, chunk []byte, maxBytes int, now time.Time) (meetingRecordingRecord, error) {
	if s.loadErr != nil {
		return meetingRecordingRecord{}, s.loadErr
	}
	meetingID = strings.TrimSpace(meetingID)
	s.mu.Lock()
	defer s.mu.Unlock()
	stamp := now.UTC().Format(time.RFC3339Nano)
	record, exists := s.state.Recordings[meetingID]
	if exists {
		switch {
		case record.ConsentRevokedAt != "":
			return record, errMeetingRecordingConsentRevoked
		case record.State == meetingRecordingStateStored:
			return record, errMeetingRecordingStored
		case record.State == meetingRecordingStateRefused:
			return record, errMeetingRecordingRefused
		}
		if partIndex < record.PartCount {
			return record, nil
		}
		if partIndex > record.PartCount {
			return record, errMeetingRecordingPartOrder
		}
	} else {
		if partIndex != 0 {
			return meetingRecordingRecord{}, errMeetingRecordingPartOrder
		}
		record = meetingRecordingRecord{
			MeetingID: meetingID, RoomID: normalizeRoomID(roomID), State: meetingRecordingStateUploading,
			Mime: mime, UploadedBy: normalizeAccountEmail(uploadedBy), StartedAt: stamp, ConsentCheckedAt: stamp,
		}
	}
	if record.Bytes+int64(len(chunk)) > int64(maxBytes) {
		record.State = meetingRecordingStateRefused
		record.Error = errMeetingRecordingTooLarge.Error()
		record.UpdatedAt = stamp
		s.state.Recordings[meetingID] = record
		_ = os.Remove(s.partPath(meetingID))
		if err := s.persistLocked(); err != nil {
			log.Errorf("Could not persist refused recording %s: %v", meetingID, err)
		}
		return record, errMeetingRecordingTooLarge
	}
	partPath := s.partPath(meetingID)
	if len(chunk) > 0 {
		if err := os.MkdirAll(s.partsDir, 0o700); err != nil {
			return record, fmt.Errorf("recording parts directory: %w", err)
		}
		file, err := os.OpenFile(partPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return record, fmt.Errorf("open recording parts: %w", err)
		}
		if _, err := file.Write(chunk); err != nil {
			_ = file.Close()
			_ = os.Truncate(partPath, record.Bytes)
			return record, fmt.Errorf("append recording part: %w", err)
		}
		if err := file.Close(); err != nil {
			_ = os.Truncate(partPath, record.Bytes)
			return record, fmt.Errorf("close recording part: %w", err)
		}
	}
	prior, hadPrior := s.state.Recordings[meetingID]
	priorBytes := record.Bytes
	record.PartCount++
	record.Bytes += int64(len(chunk))
	record.UpdatedAt = stamp
	s.state.Recordings[meetingID] = record
	if err := s.persistLocked(); err != nil {
		if hadPrior {
			s.state.Recordings[meetingID] = prior
		} else {
			delete(s.state.Recordings, meetingID)
		}
		if len(chunk) > 0 {
			_ = os.Truncate(partPath, priorBytes)
		}
		return record, err
	}
	return record, nil
}

// finish assembles the parts into one blob via put and marks the recording
// stored. Idempotent: an already-stored recording returns itself.
func (s *meetingRecordingStore) finish(meetingID string, durationSeconds int, maxBytes int, now time.Time, put func([]byte, string) (string, error)) (meetingRecordingRecord, error) {
	if s.loadErr != nil {
		return meetingRecordingRecord{}, s.loadErr
	}
	meetingID = strings.TrimSpace(meetingID)
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.state.Recordings[meetingID]
	if !ok {
		return meetingRecordingRecord{}, errMeetingRecordingEmpty
	}
	switch {
	case record.State == meetingRecordingStateStored:
		return record, nil
	case record.ConsentRevokedAt != "":
		return record, errMeetingRecordingConsentRevoked
	case record.State == meetingRecordingStateRefused:
		return record, errMeetingRecordingRefused
	}
	partPath := s.partPath(meetingID)
	data, err := os.ReadFile(partPath)
	if err != nil {
		if os.IsNotExist(err) {
			return record, errMeetingRecordingEmpty
		}
		return record, fmt.Errorf("read recording parts: %w", err)
	}
	if len(data) == 0 {
		return record, errMeetingRecordingEmpty
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	if len(data) > maxBytes {
		record.State = meetingRecordingStateRefused
		record.Error = errMeetingRecordingTooLarge.Error()
		record.UpdatedAt = stamp
		s.state.Recordings[meetingID] = record
		_ = os.Remove(partPath)
		_ = s.persistLocked()
		return record, errMeetingRecordingTooLarge
	}
	ref, err := put(data, record.Mime)
	if err != nil {
		return record, err
	}
	prior := record
	record.State = meetingRecordingStateStored
	record.Ref = ref
	record.Size = int64(len(data))
	record.Bytes = int64(len(data))
	record.StoredAt = stamp
	record.UpdatedAt = stamp
	record.Error = ""
	if durationSeconds > 0 && durationSeconds <= meetingRecordingMaxDurationSeconds {
		record.DurationSeconds = durationSeconds
	}
	s.state.Recordings[meetingID] = record
	if err := s.persistLocked(); err != nil {
		s.state.Recordings[meetingID] = prior
		return record, err
	}
	_ = os.Remove(partPath)
	return record, nil
}

// markConsentRevoked refuses every later chunk for the sitting and drops the
// parts already received; an already-stored recording is not unwound here
// (deletion is a Meeting Record action, not a consent side effect).
func (s *meetingRecordingStore) markConsentRevoked(meetingID string, now time.Time) bool {
	if s == nil || s.loadErr != nil {
		return false
	}
	meetingID = strings.TrimSpace(meetingID)
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.state.Recordings[meetingID]
	if !ok || record.State == meetingRecordingStateStored || record.ConsentRevokedAt != "" {
		return false
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	record.ConsentRevokedAt = stamp
	record.State = meetingRecordingStateRefused
	record.Error = errMeetingRecordingConsentRevoked.Error()
	record.UpdatedAt = stamp
	s.state.Recordings[meetingID] = record
	_ = os.Remove(s.partPath(meetingID))
	if err := s.persistLocked(); err != nil {
		log.Errorf("Could not persist recording consent withdrawal for %s: %v", meetingID, err)
	}
	return true
}

func (s *meetingRecordingStore) storedRefs() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	refs := make([]string, 0, len(s.state.Recordings))
	for _, record := range s.state.Recordings {
		if record.State == meetingRecordingStateStored && validBlobRef(record.Ref) {
			refs = append(refs, record.Ref)
		}
	}
	sort.Strings(refs)
	return refs
}

// roomMediaRecordingEnabled is the one read seam rooms.go's list payload and
// the upload route share. Default false.
func roomMediaRecordingEnabled(roomID string) bool {
	return appMeetingRecordingStore().setting(roomID).RecordingEnabled
}

// meetingRecordingBlobRefs keeps stored recordings alive across the blob GC
// sweep (registered through registerBlobReferenceWalker in init).
func meetingRecordingBlobRefs(app *kanbanBoardApp) []string {
	seen := map[string]struct{}{}
	refs := make([]string, 0)
	add := func(ref string) {
		if !validBlobRef(ref) {
			return
		}
		if _, dup := seen[ref]; dup {
			return
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	for _, ref := range appMeetingRecordingStore().storedRefs() {
		add(ref)
	}
	if app != nil && app.meetings != nil {
		app.meetings.mu.Lock()
		for _, record := range app.meetings.records {
			if record.Recording != nil {
				add(record.Recording.Ref)
			}
		}
		app.meetings.mu.Unlock()
	}
	sort.Strings(refs)
	return refs
}

// meetingRecordingConsentScopeRevokes reports whether a consent decision
// notice ends recording for its sitting: any non-grant on the recording scope
// or on audio_capture, which recording depends on.
func meetingRecordingConsentScopeRevokes(scope ConsentScope, disposition ConsentDisposition) bool {
	return (scope == ConsentRecording || scope == ConsentAudioCapture) && disposition != ConsentGranted
}

func init() {
	registerBlobReferenceWalker(meetingRecordingBlobRefs)
	subscribeConsentDecisions(func(notice ConsentDecisionNotice) {
		if !meetingRecordingConsentScopeRevokes(notice.Scope, notice.Disposition) {
			return
		}
		if store := appMeetingRecordingStoreIfOpen(); store != nil {
			store.markConsentRevoked(notice.Binding.SittingID, time.Now().UTC())
		}
	})
}

/* ---------- Meeting Record output stage ---------- */

// meetingRecordingOutput is the `recording` output stage on a Meeting Record:
// the immutable blob ref, its mime and size, the client-reported duration,
// and the session-gated playback path.
type meetingRecordingOutput struct {
	Ref             string `json:"ref"`
	Mime            string `json:"mime"`
	Size            int64  `json:"size"`
	DurationSeconds int    `json:"durationSeconds,omitempty"`
	StoredAt        string `json:"storedAt"`
	UploadedBy      string `json:"uploadedBy,omitempty"`
	PlaybackPath    string `json:"playbackPath,omitempty"`
}

func cloneMeetingRecordingOutput(output *meetingRecordingOutput) *meetingRecordingOutput {
	if output == nil {
		return nil
	}
	cloned := *output
	return &cloned
}

func meetingRecordingPlaybackPath(ref, meetingID, mime string) string {
	name := "meeting-recording"
	if meetingRecordingPartNamePattern.MatchString(strings.TrimSpace(meetingID)) {
		name = strings.TrimSpace(meetingID)
	}
	extension := ".webm"
	if mime == meetingRecordingMimeAudio {
		extension = ".audio.webm"
	}
	return "/artifacts/blob?ref=" + url.QueryEscape(ref) + "&name=" + url.QueryEscape(name+extension)
}

// meetingRecordingStageReceipt projects the stored recording as a finalization
// stage receipt for read surfaces. Never part of readiness: a recording is an
// optional output, not a source the close depends on.
func meetingRecordingStageReceipt(output *meetingRecordingOutput) *meetingFinalizationStageReceipt {
	if output == nil {
		return nil
	}
	return &meetingFinalizationStageReceipt{
		State: meetingFinalizationStageComplete, OutputID: output.Ref, CompletedAt: output.StoredAt,
		ItemCount: 1, Disposition: meetingRecordingStageDisposition,
	}
}

func (store *meetingStore) attachRecording(id string, output meetingRecordingOutput, now time.Time) (meetingRecord, error) {
	if store == nil || strings.TrimSpace(id) == "" || !validBlobRef(output.Ref) {
		return meetingRecord{}, ErrMeetingRecordStore
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.recordIndexes == nil {
		store.rebuildDirectoryCursorIndexesLocked()
	}
	index, ok := store.recordIndexes[strings.TrimSpace(id)]
	if !ok || index < 0 || index >= len(store.records) {
		return meetingRecord{}, fmt.Errorf("%w: meeting %s is absent", ErrMeetingRecordStore, id)
	}
	prior := cloneMeetingRecord(store.records[index])
	if output.StoredAt == "" {
		output.StoredAt = now.UTC().Format(time.RFC3339Nano)
	}
	if output.PlaybackPath == "" {
		output.PlaybackPath = meetingRecordingPlaybackPath(output.Ref, store.records[index].ID, output.Mime)
	}
	store.records[index].Recording = &output
	if err := store.persistLocked(); err != nil {
		store.resolvePersistFailureLocked(err, func() { store.records[index] = prior })
		return cloneMeetingRecord(store.records[index]), fmt.Errorf("%w: %v", ErrMeetingRecordStore, err)
	}
	return cloneMeetingRecord(store.records[index]), nil
}

func (store *meetingStore) recordByRecordingRef(ref string) (meetingRecord, bool) {
	if store == nil || !validBlobRef(ref) {
		return meetingRecord{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := len(store.records) - 1; index >= 0; index-- {
		if recording := store.records[index].Recording; recording != nil && recording.Ref == ref {
			return cloneMeetingRecord(store.records[index]), true
		}
	}
	return meetingRecord{}, false
}

// meetingRecordingBlobAuthorized answers the blob route for recording refs: the
// viewer must currently read the owning Meeting Record through the same
// principal-filtered projection the Records surface uses. A learned ref is
// never authority.
func (app *kanbanBoardApp) meetingRecordingBlobAuthorized(ctx context.Context, user *userAccount, ref string) bool {
	if app == nil || user == nil || app.meetings == nil || !validBlobRef(ref) {
		return false
	}
	record, found := app.meetings.recordByRecordingRef(ref)
	if !found {
		return false
	}
	projections, _, _, _ := app.meetingRecordPageProjectionsForPrincipal(ctx, recallPrincipalForUser(user), 1, record.ID, "", false)
	for _, projection := range projections {
		if projection.record.ID == record.ID && projection.record.Recording != nil && projection.record.Recording.Ref == ref {
			return true
		}
	}
	return false
}

/* ---------- consent ---------- */

func meetingHasParticipant(record meetingRecord, name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	for _, existing := range record.Participants {
		if sameParticipantName(existing, name) {
			return true
		}
	}
	return false
}

// meetingRecordingConsentDecision is the room's recording consent state for
// one meeting: every participant the live room can still attribute must hold
// the recording lane, and no denial/withdrawal has landed on the sitting.
// Departed guests are no longer being captured and are not re-evaluated; a
// withdrawal they made while present was already recorded by the listener.
// A participant who is STILL SEATED but cannot be mapped to a consent
// principal (a deleted member account, an ambiguous guest seat) fails closed:
// they are being captured and their consent cannot be verified.
//
// The RECORDER's own seat consents by the act of recording: its principal is
// the uploader's authenticated session (never the roster name) and the durable
// admission anchor proves the seat — the lane authority is not consulted for
// it. That is what lets a member alone in a room record on a deployment whose
// consent authority has no durable store (every lane check errors there);
// every OTHER seated participant still has to verify, and fails closed.
func (app *kanbanBoardApp) meetingRecordingConsentDecision(ctx context.Context, record meetingRecord, recorder *userAccount) (bool, string) {
	if app == nil {
		return false, "room state is unavailable"
	}
	roomID := meetingRoomID(record)
	sittingID := strings.TrimSpace(record.ID)
	if sittingID == "" {
		return false, "meeting is unavailable"
	}
	if recording, ok := appMeetingRecordingStore().recording(sittingID); ok && recording.ConsentRevokedAt != "" {
		return false, "recording consent was withdrawn for this meeting"
	}
	const unverifiable = "recording consent could not be verified"
	const notConsented = "a participant has not consented to recording"
	recorderName, recorderEmail := "", ""
	if recorder != nil {
		recorderName = participantNameForAccount(recorder)
		recorderEmail = normalizeAccountEmail(recorder.Email)
	}
	laneAllowed := func(principal CanonicalPrincipalRef) (bool, string) {
		decision, err := app.effectiveConsentLane(ctx, principal, roomID, sittingID, ConsentLaneRecording)
		if err != nil {
			return false, unverifiable
		}
		if !decision.Allowed {
			return false, notConsented
		}
		return true, ""
	}
	checked := 0
	for _, name := range record.Participants {
		if recorderName != "" && recorderEmail != "" && sameParticipantName(name, recorderName) {
			if _, err := app.consentBindingForPrincipal(ctx, memberAdmissionPrincipal(recorderEmail), roomID, sittingID); err != nil {
				return false, unverifiable
			}
			checked++
			continue
		}
		// Other seats: the account(s) actually admitted behind the seat's
		// live sockets first, then the roster map for a seat without one.
		if emails := seatedMemberAccountEmailsInRoom(roomID, name); len(emails) > 0 {
			for _, email := range emails {
				if ok, reason := laneAllowed(memberAdmissionPrincipal(email)); !ok {
					return false, reason
				}
			}
			checked++
			continue
		}
		principal, ok := app.consentPrincipalForTranscriptSpeaker(roomID, name)
		if !ok {
			if app.participantSeatedInRoom(roomID, name) {
				return false, notConsented
			}
			continue
		}
		if ok, reason := laneAllowed(principal); !ok {
			return false, reason
		}
		checked++
	}
	if checked == 0 {
		return false, "no admitted participant carries recording consent"
	}
	return true, ""
}

/* ---------- HTTP ---------- */

// meetingRecordingSettingsHandler serves GET /assistant/meetings/recording/settings?roomId=
// (any member) and PATCH/POST {roomId, recordingEnabled} (room manager only;
// a non-manager gets the same opaque 404 roomActionHandler answers).
func meetingRecordingSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPatch && r.Method != http.MethodPost {
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
		writeAuthError(w, http.StatusServiceUnavailable, "rooms are unavailable")
		return
	}
	store := appMeetingRecordingStore()
	if r.Method == http.MethodGet {
		roomID := normalizeRoomID(r.URL.Query().Get("roomId"))
		if _, ok := appRoomStore().byID(roomID); !ok {
			writeAuthError(w, http.StatusNotFound, "room not found")
			return
		}
		setting := store.setting(roomID)
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok": true, "roomId": roomID, "recordingEnabled": setting.RecordingEnabled,
			"updatedAt": setting.UpdatedAt, "maxBytes": meetingRecordingMaxBytes(),
			"manageable": roomManagedByUser(roomID, user),
		})
		return
	}
	payload := struct {
		RoomID           string `json:"roomId"`
		RecordingEnabled bool   `json:"recordingEnabled"`
	}{}
	if err := decodeAuthBody(r, &payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	roomID := normalizeRoomID(payload.RoomID)
	if !roomManagedByUser(roomID, user) {
		writeAuthError(w, http.StatusNotFound, "room not found")
		return
	}
	room, ok := appRoomStore().byID(roomID)
	if !ok {
		writeAuthError(w, http.StatusNotFound, "room not found")
		return
	}
	if room.Archived {
		writeAuthError(w, http.StatusBadRequest, errRoomArchived.Error())
		return
	}
	setting, err := store.setRecordingEnabled(roomID, payload.RecordingEnabled, user.Email, time.Now().UTC())
	if err != nil {
		writeAuthError(w, http.StatusServiceUnavailable, "recording setting could not be saved")
		return
	}
	broadcastRoomsSnapshot()
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok": true, "roomId": roomID, "recordingEnabled": setting.RecordingEnabled, "updatedAt": setting.UpdatedAt, "maxBytes": meetingRecordingMaxBytes(),
	})
}

// writeMeetingRecordingUploadError maps a store error onto the upload
// response. partCount is the record's current part count: an out-of-order 409
// carries it so the client resyncs its next partIndex instead of stalling.
func writeMeetingRecordingUploadError(w http.ResponseWriter, err error, maxBytes int, partCount int) {
	switch {
	case errors.Is(err, errMeetingRecordingTooLarge):
		writeAuthError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("recording exceeds the %dMB cap", maxBytes>>20))
	case errors.Is(err, errMeetingRecordingPartOrder):
		if partCount < 0 {
			partCount = 0
		}
		writeAuthJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "partCount": partCount})
	case errors.Is(err, errMeetingRecordingStored):
		writeAuthError(w, http.StatusConflict, err.Error())
	case errors.Is(err, errMeetingRecordingConsentRevoked), errors.Is(err, errMeetingRecordingRefused):
		writeAuthError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, errMeetingRecordingEmpty), errors.Is(err, errMeetingRecordingMime):
		writeAuthError(w, http.StatusBadRequest, err.Error())
	default:
		writeAuthError(w, http.StatusServiceUnavailable, "recording could not be saved")
	}
}

// meetingRecordingUploadHandler serves POST /assistant/meetings/recording/upload
// as multipart/form-data: meetingId, partIndex, final ("1"|"true"), optional
// mime (video/webm | audio/webm) and durationSeconds, plus the `chunk` file
// part. Every chunk re-checks the room setting, participant membership and
// the recording consent lane; `final` assembles and attaches the output stage.
func meetingRecordingUploadHandler(w http.ResponseWriter, r *http.Request) {
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
	if kanbanApp == nil || kanbanApp.meetings == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "meetings are unavailable")
		return
	}
	maxBytes := meetingRecordingMaxBytes()
	r.Body = http.MaxBytesReader(w, r.Body, int64(maxBytes)+meetingRecordingFramingHeadroom)
	if err := r.ParseMultipartForm(meetingRecordingMultipartMemory); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeAuthError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("recording exceeds the %dMB cap", maxBytes>>20))
			return
		}
		writeAuthError(w, http.StatusBadRequest, "could not read recording chunk")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	meetingID := strings.TrimSpace(r.FormValue("meetingId"))
	partIndex, err := strconv.Atoi(strings.TrimSpace(r.FormValue("partIndex")))
	if meetingID == "" || err != nil || partIndex < 0 {
		writeAuthError(w, http.StatusBadRequest, "meetingId and a non-negative partIndex are required")
		return
	}
	finalRaw := strings.ToLower(strings.TrimSpace(r.FormValue("final")))
	final := finalRaw == "1" || finalRaw == "true"
	mime, err := normalizeMeetingRecordingMime(r.FormValue("mime"))
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	durationSeconds := 0
	if raw := strings.TrimSpace(r.FormValue("durationSeconds")); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed >= 0 {
			durationSeconds = parsed
		}
	}

	record, found := kanbanApp.meetings.recordByID(meetingID)
	if !found || !meetingHasParticipant(record, participantNameForAccount(user)) {
		// Missing and unauthorized are deliberately indistinguishable.
		writeAuthError(w, http.StatusNotFound, "meeting is unavailable")
		return
	}
	roomID := meetingRoomID(record)
	if !roomMediaRecordingEnabled(roomID) {
		writeAuthError(w, http.StatusForbidden, "recording is off for this room")
		return
	}
	if allowed, reason := kanbanApp.meetingRecordingConsentDecision(r.Context(), record, user); !allowed {
		writeAuthError(w, http.StatusForbidden, "recording consent is missing: "+reason)
		return
	}

	var chunk []byte
	part, _, partErr := r.FormFile("chunk")
	if errors.Is(partErr, http.ErrMissingFile) {
		part, _, partErr = r.FormFile("file")
	}
	switch {
	case partErr == nil:
		data, readErr := io.ReadAll(io.LimitReader(part, int64(maxBytes)+1))
		_ = part.Close()
		if readErr != nil {
			writeAuthError(w, http.StatusBadRequest, "could not read recording chunk")
			return
		}
		chunk = data
	case errors.Is(partErr, http.ErrMissingFile) && final:
		chunk = nil
	default:
		writeAuthError(w, http.StatusBadRequest, "recording chunk is required")
		return
	}

	store := appMeetingRecordingStore()
	now := time.Now().UTC()
	recording, err := store.appendPart(meetingID, roomID, user.Email, mime, partIndex, chunk, maxBytes, now)
	if err != nil {
		// appendPart returns the current record alongside an ordering error
		// (an empty record — partCount 0 — when no upload is open yet).
		writeMeetingRecordingUploadError(w, err, maxBytes, recording.PartCount)
		return
	}
	response := map[string]any{
		"ok": true, "meetingId": meetingID, "state": recording.State, "partIndex": partIndex,
		"partCount": recording.PartCount, "bytes": recording.Bytes, "maxBytes": maxBytes,
	}
	if !final {
		writeAuthJSON(w, http.StatusOK, response)
		return
	}
	recording, err = store.finish(meetingID, durationSeconds, maxBytes, now, func(data []byte, mime string) (string, error) {
		return putBlobWithCap(data, mime, maxBytes)
	})
	if err != nil {
		writeMeetingRecordingUploadError(w, err, maxBytes, recording.PartCount)
		return
	}
	output := meetingRecordingOutput{
		Ref: recording.Ref, Mime: recording.Mime, Size: recording.Size, DurationSeconds: recording.DurationSeconds,
		StoredAt: recording.StoredAt, UploadedBy: recording.UploadedBy,
		PlaybackPath: meetingRecordingPlaybackPath(recording.Ref, meetingID, recording.Mime),
	}
	updated, err := kanbanApp.meetings.attachRecording(meetingID, output, now)
	if err != nil {
		log.Errorf("Recording %s stored as %s but the Meeting Record stage could not be written: %v", meetingID, recording.Ref, err)
		writeAuthError(w, http.StatusServiceUnavailable, "recording stored but the Meeting Record could not be updated")
		return
	}
	kanbanApp.broadcastMeetingRecord(updated)
	response["state"] = recording.State
	response["recording"] = output
	writeAuthJSON(w, http.StatusOK, response)
}
