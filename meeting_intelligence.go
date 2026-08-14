package main

// meeting_intelligence.go is the read-only, room-scoped projection shared by
// live meeting clients. It does not run a model and it does not invent a new
// work lifecycle: raw transcript capture, the current cumulative digest, and
// live Scout/STT state remain the source records. The projection only binds
// those records to one active meeting identity and one monotonic capture
// high-water so clients can say what is current, what is catching up, and what
// is unavailable without guessing.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const meetingIntelligenceContractVersion = "meeting-intelligence-v1"
const meetingTranscriptContractVersion = "meeting-transcript-v1"
const meetingTranscriptSnapshotLimit = 200

type meetingTranscriptSnapshot struct {
	Contract  string               `json:"contract"`
	RoomID    string               `json:"roomId"`
	MeetingID string               `json:"meetingId"`
	Entries   []meetingMemoryEntry `json:"entries"`
}

type meetingIntelligenceTranscript struct {
	State            string `json:"state"`
	CaptureHighWater uint64 `json:"captureHighWater"`
	SequenceComplete bool   `json:"sequenceComplete"`
	SegmentCount     int    `json:"segmentCount"`
	LastSegmentID    string `json:"lastSegmentId,omitempty"`
	LastCapturedAt   string `json:"lastCapturedAt,omitempty"`
}

type meetingIntelligenceNotes struct {
	State                    string `json:"state"`
	Revision                 string `json:"revision,omitempty"`
	UpdatedAt                string `json:"updatedAt,omitempty"`
	GroundedThrough          string `json:"groundedThrough,omitempty"`
	AnalysisCaptureHighWater uint64 `json:"analysisCaptureHighWater"`
	Coverage                 string `json:"coverage"`
}

type meetingIntelligenceScout struct {
	State           string `json:"state"`
	GroundedThrough string `json:"groundedThrough,omitempty"`
	SourceCount     int    `json:"sourceCount"`
}

type meetingIntelligenceFact struct {
	Text     string `json:"text"`
	Owner    string `json:"owner,omitempty"`
	Status   string `json:"status,omitempty"`
	SourceID string `json:"sourceId,omitempty"`
	At       string `json:"at,omitempty"`
}

type meetingIntelligenceRecap struct {
	Title         string                    `json:"title,omitempty"`
	Topics        []meetingIntelligenceFact `json:"topics"`
	Decisions     []meetingIntelligenceFact `json:"decisions"`
	Actions       []meetingIntelligenceFact `json:"actions"`
	OpenQuestions []meetingIntelligenceFact `json:"openQuestions"`
	Risks         []meetingIntelligenceFact `json:"risks"`
	Themes        []string                  `json:"themes"`
	SourceCount   int                       `json:"sourceCount"`
}

type meetingIntelligenceSnapshot struct {
	Contract    string                        `json:"contract"`
	RoomID      string                        `json:"roomId"`
	MeetingID   string                        `json:"meetingId"`
	Revision    string                        `json:"revision"`
	GeneratedAt string                        `json:"generatedAt"`
	Transcript  meetingIntelligenceTranscript `json:"transcript"`
	Notes       meetingIntelligenceNotes      `json:"notes"`
	Scout       meetingIntelligenceScout      `json:"scout"`
	Recap       *meetingIntelligenceRecap     `json:"recap,omitempty"`
}

type meetingIntelligenceRuntime struct {
	recording bool
	stt       *meetingTranscriptionLane
	scout     *roomRealtimeBundle
}

// currentMeetingTranscriptSnapshot is a member-authorized reconnect replay for
// one active sitting. It is deliberately separate from the intelligence
// projection: transcript entries remain source records, while the recap is a
// revisioned summary over those records. The caller supplies the same scoped
// recall principal used for the room's ordinary memory admission; guests never
// receive this member-memory replay.
func (app *kanbanBoardApp) currentMeetingTranscriptSnapshot(ctx context.Context, principal RecallPrincipal, roomID string) *meetingTranscriptSnapshot {
	if app == nil || app.meetings == nil || app.memory == nil {
		return nil
	}
	roomID = normalizeRoomID(roomID)
	record, ok := app.meetings.activeRecord(roomID)
	if !ok || strings.TrimSpace(record.ID) == "" {
		return nil
	}
	scoped := app.scopedRecallApp(ctx, principal)
	if scoped == nil || scoped.memory == nil {
		return nil
	}
	return currentMeetingTranscriptSnapshotFromStore(scoped.memory, record, roomID)
}

func currentMeetingTranscriptSnapshotFromStore(store *meetingMemoryStore, record meetingRecord, roomID string) *meetingTranscriptSnapshot {
	if store == nil || strings.TrimSpace(record.ID) == "" {
		return nil
	}
	roomID = normalizeRoomID(roomID)
	entries := store.snapshotForMeeting(record.ID, 0)
	transcript := make([]meetingMemoryEntry, 0, len(entries))
	seenSequences := make(map[uint64]struct{}, len(entries))
	for _, entry := range entries {
		if entry.Kind != meetingMemoryKindTranscript || memoryEntryIsMediaSoakCanary(entry) || normalizeRoomID(entry.Metadata["roomId"]) != roomID {
			continue
		}
		sequence, ok := entryCaptureSequence(entry)
		if !ok {
			return nil
		}
		if _, duplicate := seenSequences[sequence]; duplicate {
			return nil
		}
		seenSequences[sequence] = struct{}{}
		transcript = append(transcript, entry)
	}
	sort.SliceStable(transcript, func(left, right int) bool {
		leftSequence, _ := entryCaptureSequence(transcript[left])
		rightSequence, _ := entryCaptureSequence(transcript[right])
		return leftSequence < rightSequence
	})
	transcript = tailMemoryEntries(transcript, meetingTranscriptSnapshotLimit)
	return &meetingTranscriptSnapshot{
		Contract: meetingTranscriptContractVersion,
		RoomID:   roomID, MeetingID: record.ID, Entries: transcript,
	}
}

func (app *kanbanBoardApp) currentMeetingIntelligenceRuntime(roomID string) meetingIntelligenceRuntime {
	if app == nil {
		return meetingIntelligenceRuntime{}
	}
	roomID = normalizeRoomID(roomID)
	app.mu.Lock()
	state := app.roomLive[roomID]
	runtime := meetingIntelligenceRuntime{}
	if state != nil {
		runtime.recording = state.recordingEnabled
		runtime.stt = state.lane
		runtime.scout = state.realtime
	}
	if roomID == officeRoomID && app.transcriptLane != nil {
		runtime.stt = app.transcriptLane
	}
	app.mu.Unlock()
	return runtime
}

func meetingIntelligenceTranscriptCursor(store *meetingMemoryStore, meetingID string) meetingIntelligenceTranscript {
	cursor := meetingIntelligenceTranscript{State: "not_listening", SequenceComplete: true}
	meetingID = strings.TrimSpace(meetingID)
	if store == nil || meetingID == "" {
		return cursor
	}
	store.mu.Lock()
	entries := cloneMemoryEntries(store.entries)
	boundComplete, hasBoundCompleteness := store.meetingTranscriptSequenceComplete[meetingID]
	store.mu.Unlock()
	segments := meetingRecordSegments(entries, meetingID)
	var lastAt time.Time
	var firstMeetingSequence uint64
	seenMeetingSequences := map[uint64]struct{}{}
	sequenceCounts := make(map[uint64]int)
	for _, entry := range entries {
		if entry.Kind != meetingMemoryKindTranscript {
			continue
		}
		if sequence, ok := entryCaptureSequence(entry); ok {
			sequenceCounts[sequence]++
		}
	}
	for _, segment := range segments {
		cursor.SegmentCount++
		sequence := segment.CaptureSequence
		if sequence == 0 {
			cursor.SequenceComplete = false
		} else {
			if _, duplicate := seenMeetingSequences[sequence]; duplicate {
				cursor.SequenceComplete = false
			}
			seenMeetingSequences[sequence] = struct{}{}
			if firstMeetingSequence == 0 || sequence < firstMeetingSequence {
				firstMeetingSequence = sequence
			}
		}
		if sequence > cursor.CaptureHighWater {
			cursor.CaptureHighWater = sequence
			cursor.LastSegmentID = segment.ID
		}
		capturedAt, _ := time.Parse(time.RFC3339Nano, segment.At)
		if capturedAt.After(lastAt) {
			lastAt = capturedAt.UTC()
			if cursor.LastSegmentID == "" {
				cursor.LastSegmentID = segment.ID
			}
		}
	}
	if firstMeetingSequence > 0 && cursor.CaptureHighWater >= firstMeetingSequence {
		if hasBoundCompleteness {
			cursor.SequenceComplete = cursor.SequenceComplete && boundComplete
		} else {
			for sequence := firstMeetingSequence; ; sequence++ {
				if sequenceCounts[sequence] != 1 {
					cursor.SequenceComplete = false
					break
				}
				if sequence == cursor.CaptureHighWater {
					break
				}
			}
		}
	}
	if !lastAt.IsZero() {
		cursor.LastCapturedAt = lastAt.Format(time.RFC3339Nano)
	}
	return cursor
}

// immutableMeetingIntelligenceStoreSnapshot captures only one meeting's
// transcript rows and digest/brain successors in one store generation. All
// later cursor, fallback, and recap reads operate on this immutable clone, so a
// live append cannot land between the transcript high-water and digest
// selection. The maintained meeting directory keeps this read proportional to
// the selected sitting rather than the lifetime memory ledger.
func immutableMeetingIntelligenceStoreSnapshot(store *meetingMemoryStore, meetingID string) *meetingMemoryStore {
	meetingID = strings.TrimSpace(meetingID)
	if store == nil || meetingID == "" {
		return nil
	}
	store.mu.Lock()
	if store.meetingEntryIndexes == nil {
		store.rebuildMeetingEntryIndexesLocked()
	}
	indexes := store.meetingEntryIndexes[meetingID]
	entries := make([]meetingMemoryEntry, 0, len(indexes))
	for _, index := range indexes {
		if index < 0 || index >= len(store.entries) {
			continue
		}
		if store.meetingEntryVisitHook != nil {
			store.meetingEntryVisitHook()
		}
		entry := store.entries[index]
		if strings.TrimSpace(entry.Metadata["meetingId"]) != meetingID || memoryEntryHiddenFromRecall(entry) {
			continue
		}
		entries = append(entries, cloneMemoryEntry(entry))
	}
	sequenceComplete := true
	var firstSequence, lastSequence uint64
	for _, segment := range meetingRecordSegments(entries, meetingID) {
		if segment.CaptureSequence == 0 {
			sequenceComplete = false
			continue
		}
		if firstSequence == 0 || segment.CaptureSequence < firstSequence {
			firstSequence = segment.CaptureSequence
		}
		if segment.CaptureSequence > lastSequence {
			lastSequence = segment.CaptureSequence
		}
	}
	if firstSequence > 0 {
		sequenceComplete = sequenceComplete && store.captureSequenceRangeCompleteLocked(firstSequence, lastSequence)
	}
	store.mu.Unlock()
	snapshot := &meetingMemoryStore{
		entries: entries, meetingIDs: map[string]string{}, seen: map[string]struct{}{},
		meetingTranscriptSequenceComplete: map[string]bool{meetingID: sequenceComplete},
	}
	// The clone is a standalone read generation. Rebuild the same body-free
	// navigation indexes that a restarted store derives from JSONL; otherwise
	// current digests and exact meeting rows vanish only inside the supposedly
	// immutable snapshot, making healthy analysis look perpetually behind.
	snapshot.rebuildMeetingEntryIndexesLocked()
	return snapshot
}

func bindMeetingTranscriptSequenceCompleteness(filtered, source *meetingMemoryStore, meetingID string) {
	meetingID = strings.TrimSpace(meetingID)
	if filtered == nil || source == nil || meetingID == "" {
		return
	}
	filtered.mu.Lock()
	entries := cloneMemoryEntries(filtered.entries)
	filtered.mu.Unlock()
	sequenceComplete := true
	var firstSequence, lastSequence uint64
	for _, segment := range meetingRecordSegments(entries, meetingID) {
		if segment.CaptureSequence == 0 {
			sequenceComplete = false
			continue
		}
		if firstSequence == 0 || segment.CaptureSequence < firstSequence {
			firstSequence = segment.CaptureSequence
		}
		if segment.CaptureSequence > lastSequence {
			lastSequence = segment.CaptureSequence
		}
	}
	if firstSequence > 0 {
		source.mu.Lock()
		if source.meetingEntryIndexes == nil {
			source.rebuildMeetingEntryIndexesLocked()
		}
		sequenceComplete = sequenceComplete && source.captureSequenceRangeCompleteLocked(firstSequence, lastSequence)
		source.mu.Unlock()
	}
	filtered.mu.Lock()
	if filtered.meetingTranscriptSequenceComplete == nil {
		filtered.meetingTranscriptSequenceComplete = map[string]bool{}
	}
	filtered.meetingTranscriptSequenceComplete[meetingID] = sequenceComplete
	filtered.mu.Unlock()
}

func meetingIntelligenceTranscriptCaptureHighWater(store *meetingMemoryStore, transcriptID string) uint64 {
	if store == nil || strings.TrimSpace(transcriptID) == "" {
		return 0
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := len(store.entries) - 1; index >= 0; index-- {
		entry := store.entries[index]
		if entry.Kind == meetingMemoryKindTranscript && entry.ID == strings.TrimSpace(transcriptID) {
			sequence, _ := entryCaptureSequence(entry)
			return sequence
		}
	}
	return 0
}

func meetingIntelligenceBrainCaptureHighWater(store *meetingMemoryStore, brain meetingMemoryEntry) uint64 {
	if sequence, err := strconv.ParseUint(strings.TrimSpace(brain.Metadata[meetingBrainCaptureMetadataKey]), 10, 64); err == nil && sequence > 0 {
		return sequence
	}
	return meetingIntelligenceTranscriptCaptureHighWater(store, brain.Metadata[meetingBrainCursorMetadataKey])
}

func meetingIntelligenceBrainGroupCaptureHighWater(store *meetingMemoryStore, brains []meetingMemoryEntry) uint64 {
	var highWater uint64
	for _, brain := range brains {
		if candidate := meetingIntelligenceBrainCaptureHighWater(store, brain); candidate > highWater {
			highWater = candidate
		}
	}
	return highWater
}

func meetingIntelligenceDigestCaptureHighWater(store *meetingMemoryStore, digest meetingMemoryEntry, meetingID string) uint64 {
	if sequence, err := strconv.ParseUint(strings.TrimSpace(digest.Metadata[meetingDigestCaptureMetadataKey]), 10, 64); err == nil && sequence > 0 {
		return sequence
	}
	if store == nil {
		return 0
	}
	throughBrainID := strings.TrimSpace(digest.Metadata[meetingDigestCursorMetadataKey])
	if throughBrainID == "" {
		return 0
	}
	store.mu.Lock()
	var exact meetingMemoryEntry
	for _, entry := range store.entries {
		if entry.Kind == meetingMemoryKindBrain && entry.ID == throughBrainID && strings.TrimSpace(entry.Metadata["meetingId"]) == strings.TrimSpace(meetingID) && !entry.CreatedAt.After(digest.CreatedAt) {
			exact = cloneMemoryEntry(entry)
			break
		}
	}
	store.mu.Unlock()
	return meetingIntelligenceBrainCaptureHighWater(store, exact)
}

func meetingIntelligenceFactSourceCount(payload meetingDigestPayload) int {
	sources := map[string]struct{}{}
	for _, topic := range payload.Topics {
		if anchor := strings.TrimSpace(topic.Anchor); anchor != "" {
			sources[anchor] = struct{}{}
		}
	}
	for _, decision := range payload.Decisions {
		if anchor := strings.TrimSpace(decision.Anchor); anchor != "" {
			sources[anchor] = struct{}{}
		}
	}
	for _, action := range payload.ActionItems {
		if anchor := strings.TrimSpace(action.Anchor); anchor != "" {
			sources[anchor] = struct{}{}
		}
	}
	for _, question := range payload.OpenQuestions {
		if anchor := strings.TrimSpace(question.Anchor); anchor != "" {
			sources[anchor] = struct{}{}
		}
	}
	return len(sources)
}

func meetingIntelligenceRecapFromProjection(projection *meetingRecordProjection) *meetingIntelligenceRecap {
	if projection == nil || !projection.hasDigest {
		return nil
	}
	payload := projection.payload
	recap := &meetingIntelligenceRecap{
		Title:         "",
		Topics:        make([]meetingIntelligenceFact, 0, len(payload.Topics)),
		Decisions:     make([]meetingIntelligenceFact, 0, len(payload.Decisions)),
		Actions:       make([]meetingIntelligenceFact, 0, len(payload.ActionItems)),
		OpenQuestions: make([]meetingIntelligenceFact, 0, len(payload.OpenQuestions)),
		Risks:         []meetingIntelligenceFact{},
		// Digest themes have no individual source edge in the current schema.
		// Do not present them as current live facts until that contract exists.
		Themes: []string{},
	}
	sources := map[string]struct{}{}
	ground := func(anchor string) (meetingRecordSourceRef, bool) {
		refs, ok := projection.groundedSource(anchor)
		if !ok || len(refs) != 1 {
			return meetingRecordSourceRef{}, false
		}
		sources[refs[0].SegmentID] = struct{}{}
		return refs[0], true
	}
	for _, topic := range payload.Topics {
		if text := strings.TrimSpace(topic.T); text != "" {
			if source, ok := ground(topic.Anchor); ok {
				recap.Topics = append(recap.Topics, meetingIntelligenceFact{Text: text, SourceID: source.SegmentID, At: strings.TrimSpace(topic.At)})
			}
		}
	}
	for _, decision := range payload.Decisions {
		if text := strings.TrimSpace(decision.D); text != "" {
			if source, ok := ground(decision.Anchor); ok {
				recap.Decisions = append(recap.Decisions, meetingIntelligenceFact{Text: text, Owner: strings.TrimSpace(decision.By), Status: strings.TrimSpace(decision.Status), SourceID: source.SegmentID, At: strings.TrimSpace(decision.At)})
			}
		}
	}
	for _, action := range payload.ActionItems {
		if text := strings.TrimSpace(action.A); text != "" {
			if source, ok := ground(action.Anchor); ok {
				recap.Actions = append(recap.Actions, meetingIntelligenceFact{Text: text, Owner: strings.TrimSpace(action.Owner), Status: strings.TrimSpace(action.Status), SourceID: source.SegmentID, At: strings.TrimSpace(action.At)})
			}
		}
	}
	for _, question := range payload.OpenQuestions {
		if text := strings.TrimSpace(question.Q); text != "" {
			if source, ok := ground(question.Anchor); ok {
				recap.OpenQuestions = append(recap.OpenQuestions, meetingIntelligenceFact{Text: text, SourceID: source.SegmentID, At: strings.TrimSpace(question.At)})
			}
		}
	}
	recap.SourceCount = len(sources)
	if recap.SourceCount > 0 {
		recap.Title = strings.TrimSpace(payload.Title)
	}
	return recap
}

func (app *kanbanBoardApp) meetingIntelligenceSnapshot(roomID string, now time.Time) *meetingIntelligenceSnapshot {
	if app == nil || app.meetings == nil || app.memory == nil {
		return nil
	}
	roomID = normalizeRoomID(roomID)
	record, ok := app.meetings.activeRecord(roomID)
	if !ok || strings.TrimSpace(record.ID) == "" {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	return app.meetingIntelligenceSnapshotFromStore(record, roomID, now, immutableMeetingIntelligenceStoreSnapshot(app.memory, record.ID))
}

func (app *kanbanBoardApp) meetingIntelligenceSnapshotFromStore(record meetingRecord, roomID string, now time.Time, store *meetingMemoryStore) *meetingIntelligenceSnapshot {
	if app == nil || store == nil || strings.TrimSpace(record.ID) == "" {
		return nil
	}
	roomID = normalizeRoomID(roomID)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	runtime := app.currentMeetingIntelligenceRuntime(roomID)
	transcript := meetingIntelligenceTranscriptCursor(store, record.ID)
	if runtime.recording && runtime.stt != nil && runtime.stt.isConnected() {
		transcript.State = "listening"
	} else if runtime.recording {
		transcript.State = "transcript_paused"
	}

	notes := meetingIntelligenceNotes{State: "catching_up", Coverage: coverageLabelUnknown}
	var recap *meetingIntelligenceRecap
	digestID := ""
	digestContentRevision := ""
	if digest, found := store.currentDigest(meetingMemoryKindMeetingDigest, record.ID); found {
		if _, parsed := parseMeetingDigest(digest.Text); parsed {
			digestID = digest.ID
			digestContentRevision = temporalDigest(digest.Text)
			projection := newMeetingRecordProjection(record, store.entries, nil, now)
			recap = meetingIntelligenceRecapFromProjection(projection)
			notes.Revision = digest.ID
			notes.UpdatedAt = digest.CreatedAt.UTC().Format(time.RFC3339Nano)
			notes.GroundedThrough = strings.TrimSpace(digest.Metadata[digestSpanEndMetadataKey])
			notes.AnalysisCaptureHighWater = meetingIntelligenceDigestCaptureHighWater(store, digest, record.ID)
			notes.Coverage = strings.TrimSpace(digest.Metadata[digestCoverageMetadataKey])
			if notes.Coverage == "" {
				notes.Coverage = coverageLabelUnknown
			}
			if transcript.SegmentCount > 0 && transcript.SequenceComplete && transcript.CaptureHighWater > 0 && notes.AnalysisCaptureHighWater == transcript.CaptureHighWater {
				notes.State = "current"
			}
		}
	}

	scout := meetingIntelligenceScout{State: "unavailable"}
	if runtime.scout != nil {
		status := runtime.scout.snapshot()
		if status.Status == RoomScoutReady {
			scout.State = "ready"
			if notes.State != "current" {
				scout.State = "not_caught_up"
			}
		}
	}
	if recap != nil {
		scout.GroundedThrough = notes.GroundedThrough
		scout.SourceCount = recap.SourceCount
	}

	revision := temporalDigest(fmt.Sprintf(
		"%s\x00%s\x00%s\x00%d\x00%d\x00%t\x00%s\x00%s\x00%s\x00%s",
		record.ID,
		digestID,
		digestContentRevision,
		transcript.CaptureHighWater,
		notes.AnalysisCaptureHighWater,
		transcript.SequenceComplete,
		transcript.State,
		notes.State,
		notes.Coverage,
		scout.State,
	))
	return &meetingIntelligenceSnapshot{
		Contract: meetingIntelligenceContractVersion, RoomID: roomID, MeetingID: record.ID,
		Revision: revision, GeneratedAt: now.Format(time.RFC3339Nano), Transcript: transcript,
		Notes: notes, Scout: scout, Recap: recap,
	}
}

// memberMeetingIntelligenceSnapshots takes the member-authorized memory clone
// once, then derives transcript replay and intelligence from that same frozen
// generation. The active sitting is rechecked after construction so a meeting
// rollover cannot pair old memory with a successor record.
func (app *kanbanBoardApp) memberMeetingIntelligenceSnapshots(ctx context.Context, principal RecallPrincipal, roomID string, now time.Time) (*meetingTranscriptSnapshot, *meetingIntelligenceSnapshot) {
	if app == nil || app.meetings == nil {
		return nil, nil
	}
	roomID = normalizeRoomID(roomID)
	record, ok := app.meetings.activeRecord(roomID)
	if !ok || strings.TrimSpace(record.ID) == "" || strings.TrimSpace(principal.SittingID) != record.ID || principal.MediaGeneration != app.roomMediaGeneration(roomID) {
		return nil, nil
	}
	scoped := app.scopedRecallApp(ctx, principal)
	if scoped == nil || scoped.memory == nil {
		return nil, nil
	}
	bindMeetingTranscriptSequenceCompleteness(scoped.memory, app.memory, record.ID)
	transcript := currentMeetingTranscriptSnapshotFromStore(scoped.memory, record, roomID)
	intelligence := app.meetingIntelligenceSnapshotFromStore(record, roomID, now, scoped.memory)
	current, stillActive := app.meetings.activeRecord(roomID)
	if !stillActive || current.ID != record.ID || principal.MediaGeneration != app.roomMediaGeneration(roomID) {
		return nil, nil
	}
	return transcript, intelligence
}

var meetingIntelligenceBeforeScopedFanoutProbe func()

// meetingIntelligenceSnapshotForScope binds recap construction to the exact
// sitting/generation that will authorize its fan-out. A meeting rollover may
// occur while the immutable memory generation is being summarized; the final
// active-record and publication-scope reads make that whole candidate stale
// instead of pairing a successor recap with predecessor sockets.
func (app *kanbanBoardApp) meetingIntelligenceSnapshotForScope(scope RoomScoutScope, now time.Time) *meetingIntelligenceSnapshot {
	if app == nil || app.meetings == nil || app.memory == nil || !roomPublicationScopeValid(scope) {
		return nil
	}
	record, active := app.meetings.activeRecord(scope.RoomID)
	if !active || record.ID != scope.SittingID || app.roomMediaGeneration(scope.RoomID) != scope.MediaGeneration {
		return nil
	}
	snapshot := app.meetingIntelligenceSnapshotFromStore(record, scope.RoomID, now, immutableMeetingIntelligenceStoreSnapshot(app.memory, record.ID))
	if snapshot == nil || snapshot.MeetingID != scope.SittingID {
		return nil
	}
	if meetingIntelligenceBeforeScopedFanoutProbe != nil {
		meetingIntelligenceBeforeScopedFanoutProbe()
	}
	currentRecord, stillActive := app.meetings.activeRecord(scope.RoomID)
	currentScope, stillCurrent := app.roomPublicationScope(scope.RoomID, scope.SittingID)
	if !stillActive || currentRecord.ID != scope.SittingID || !stillCurrent || !currentScope.same(scope) {
		return nil
	}
	return snapshot
}

func (app *kanbanBoardApp) broadcastMeetingIntelligence(roomID, meetingID string) bool {
	if app == nil {
		return false
	}
	scope, current := app.roomPublicationScope(roomID, meetingID)
	if !current {
		return false
	}
	snapshot := app.meetingIntelligenceSnapshotForScope(scope, time.Now().UTC())
	if snapshot == nil {
		return false
	}
	// Recheck immediately before the scoped fan-out. The delivery seam also
	// matches recipients against this exact room+sitting+generation tuple.
	currentScope, stillCurrent := app.roomPublicationScope(scope.RoomID, scope.SittingID)
	if !stillCurrent || !currentScope.same(scope) {
		return false
	}
	broadcastScopedRoomKanbanEvent(scope, "meeting_intelligence_snapshot", snapshot)
	return true
}
