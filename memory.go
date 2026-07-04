package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	meetingMemoryKindTranscript  = "transcript"
	meetingMemoryKindBrain       = "brain"
	meetingMemoryKindBoardUpdate = "board_update"
	meetingMemoryKindArchive     = "archive"
	meetingMemoryKindOSArtifact  = "os_artifact"
	meetingMemoryKindScoutChat   = "scout_chat_thread"
	// meetingMemoryKindCodexProposal is a board-worker-proposed agent task
	// awaiting a human confirm/dismiss. Proposals are UI state, not knowledge:
	// like scout_chat_thread they are excluded from Scout search context and
	// from the generic client memory timeline.
	meetingMemoryKindCodexProposal = "codex_proposal"
	// meetingMemoryKindMissionInsight is the mission-intelligence agent's
	// synthesized themes/consensus JSON. Like scout_chat_thread and
	// codex_proposal it is UI state (served via /assistant/mission), not
	// knowledge prose: excluded from Scout search context and from the
	// generic client memory timeline.
	meetingMemoryKindMissionInsight = "mission_insight"
	// meetingMemoryKindDecision is one explicit team decision per entry.
	// entry.Text = the decision statement, so store.search matches decisions
	// for free — deliberately NOT a UI-state kind: decisions ground Scout's
	// answers. They are still excluded from the client memory timeline via
	// visibleMeetingMemoryEntries (the ledger renders in the intel canvas).
	meetingMemoryKindDecision = "decision"
	// meetingMemoryKindDecisionPass is the decision-ledger agent's per-pass
	// cursor artifact (mirrors board_update's role for the board worker). Pure
	// bookkeeping: UI state, never knowledge.
	meetingMemoryKindDecisionPass = "decision_pass"
	// meetingMemoryKindPackage is a venture package — the per-IP mission
	// binder. entry.Text = the full venturePackageRecord JSON (the
	// scout_chat_thread precedent), so it is UI state: raw record JSON must
	// never pollute Scout search; packages reach Scout through a structured
	// "# Venture packages" context section instead. Excluded from the client
	// memory timeline too (the binder renders in the intel canvas).
	meetingMemoryKindPackage = "package"
	// meetingMemoryKindSlopPass is the slop-classifier's per-pass cursor +
	// audit-stub artifact (mirrors decision_pass for the ledger). Pure
	// bookkeeping: UI state, never knowledge — carries the consumed-through
	// transcript cursor and, for an expiry deletion, the deleted id + reason so
	// the FACT of a hard delete survives even though the content does not.
	meetingMemoryKindSlopPass = "slop_pass"
	defaultMeetingMemoryPath  = "data/meeting-memory.jsonl"
	// transcriptSourceRoomChat marks transcript entries injected from the
	// in-meeting text chat rather than the audio transcription lanes.
	transcriptSourceRoomChat = "room_chat"
)

// relevance lifecycle (Wave 7). Stored on meetingMemoryEntry.Metadata under
// key "relevance"; absent == active. active/archived stay searchable (archived
// is down-ranked in store.search); quarantined/expired are excluded from recall,
// model context, and the client timeline. quarantined hard-deletes 30 visible
// days after quarantinedAt, leaving a slop_pass audit stub.
const (
	relevanceMetadataKey = "relevance"
	relevanceActive      = "active"
	relevanceArchived    = "archived"
	relevanceQuarantined = "quarantined"
	relevanceExpired     = "expired"
	// archivedSearchPenalty subtracts from an archived entry's match score so it
	// ranks below active material yet stays findable (design §6).
	archivedSearchPenalty = 6
)

// memoryEntryRelevance returns the lifecycle state of an entry; absent == active.
func memoryEntryRelevance(entry meetingMemoryEntry) string {
	relevance := strings.TrimSpace(strings.ToLower(entry.Metadata[relevanceMetadataKey]))
	if relevance == "" {
		return relevanceActive
	}
	return relevance
}

// memoryEntryHiddenFromRecall reports entries excluded from search, model
// context, and the client timeline: quarantined or expired.
func memoryEntryHiddenFromRecall(entry meetingMemoryEntry) bool {
	switch memoryEntryRelevance(entry) {
	case relevanceQuarantined, relevanceExpired:
		return true
	}
	return false
}

var memoryTokenPattern = regexp.MustCompile(`[a-z0-9]+`)

var lowQualityTranscriptPhrases = map[string]struct{}{
	"ah":        {},
	"er":        {},
	"hm":        {},
	"hmm":       {},
	"mm":        {},
	"oh":        {},
	"ok":        {},
	"okay":      {},
	"so":        {},
	"test":      {},
	"testing":   {},
	"the":       {},
	"uh":        {},
	"um":        {},
	"yeah":      {},
	"yep":       {},
	"assistant": {},
	"thank you": {},
	"thanks":    {},
	"that's":    {},
	"thats":     {},
}

type meetingMemoryStore struct {
	mu        sync.Mutex
	path      string
	entries   []meetingMemoryEntry
	seen      map[string]struct{}
	meetingID string
	// bootLatestIDs maps entry kind to the newest entry ID already persisted
	// when the store was loaded — the baseline an ambient agent loop registers
	// at startup so it never backfills pre-boot history.
	bootLatestIDs map[string]string
}

type meetingMemoryEntry struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	Text      string            `json:"text"`
	CreatedAt time.Time         `json:"createdAt"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type meetingMemoryMatch struct {
	Entry meetingMemoryEntry
	Score int
}

func newMeetingMemoryStore(path string) (*meetingMemoryStore, error) {
	store := &meetingMemoryStore{
		path:          path,
		seen:          map[string]struct{}{},
		bootLatestIDs: map[string]string{},
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create memory directory: %w", err)
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, fmt.Errorf("open memory file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var entry meetingMemoryEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			log.Warnf("Skipping malformed memory entry: %v", err)
			continue
		}
		if strings.TrimSpace(entry.ID) == "" || strings.TrimSpace(entry.Text) == "" {
			continue
		}
		store.entries = append(store.entries, entry)
		store.seen[entry.ID] = struct{}{}
		store.bootLatestIDs[entry.Kind] = entry.ID
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read memory file: %w", err)
	}

	// resume the in-flight meeting after a restart: if the newest entry was not
	// an archive, the meeting it belongs to is still open.
	if count := len(store.entries); count > 0 {
		last := store.entries[count-1]
		if last.Kind != meetingMemoryKindArchive {
			store.meetingID = strings.TrimSpace(last.Metadata["meetingId"])
		}
	}

	return store, nil
}

// bootBaselineIDOfKind returns the ID of the newest entry of kind that was
// already persisted when the store was loaded: the cursor an ambient agent
// loop would have registered had it started at boot.
func (store *meetingMemoryStore) bootBaselineIDOfKind(kind string) string {
	if store == nil {
		return ""
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	return store.bootLatestIDs[kind]
}

// currentMeetingID returns the active meeting id, empty until the first entry
// of a meeting is appended.
func (store *meetingMemoryStore) currentMeetingID() string {
	if store == nil {
		return ""
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	return store.meetingID
}

// rotateMeetingID closes the current meeting; the next appended entry lazily
// starts a new meeting id. Called when archive_meeting completes.
func (store *meetingMemoryStore) rotateMeetingID() {
	if store == nil {
		return
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	store.meetingID = ""
}

// rotateMeetingIDIfCurrent rotates only while id is still the active meeting
// id; reports whether the rotation landed. The closing seams (idle end,
// archive) use it so a concurrent admission's freshly minted successor id is
// never clobbered by a stale close.
func (store *meetingMemoryStore) rotateMeetingIDIfCurrent(id string) bool {
	if store == nil || strings.TrimSpace(id) == "" {
		return false
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	if store.meetingID != id {
		return false
	}
	store.meetingID = ""
	return true
}

// ensureMeetingID mints (or returns) the active meeting id eagerly, so a
// meeting record can be opened at room admission before any entry appends.
func (store *meetingMemoryStore) ensureMeetingID() string {
	if store == nil {
		return ""
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	return store.currentMeetingIDLocked()
}

func (store *meetingMemoryStore) currentMeetingIDLocked() string {
	if store.meetingID == "" {
		now := time.Now().UTC()
		// nanosecond suffix keeps back-to-back meetings distinct.
		store.meetingID = fmt.Sprintf("meeting-%s-%09d", now.Format("20060102-150405"), now.Nanosecond())
	}

	return store.meetingID
}

func meetingMemoryPath() string {
	if path := strings.TrimSpace(os.Getenv("MEETING_MEMORY_PATH")); path != "" {
		return path
	}

	return defaultMeetingMemoryPath
}

func (store *meetingMemoryStore) appendTranscript(eventID string, itemID string, transcript string) (meetingMemoryEntry, bool, error) {
	return store.appendAttributedTranscript(eventID, itemID, "", "", transcript)
}

func (store *meetingMemoryStore) appendAttributedTranscript(eventID string, itemID string, speaker string, speakerConfidence string, transcript string) (meetingMemoryEntry, bool, error) {
	return store.appendAttributedTranscriptWithMetadata(eventID, itemID, speaker, speakerConfidence, transcript, nil)
}

func (store *meetingMemoryStore) appendAttributedTranscriptWithMetadata(eventID string, itemID string, speaker string, speakerConfidence string, transcript string, extraMetadata map[string]string) (meetingMemoryEntry, bool, error) {
	return store.appendAttributedTranscriptEntry(eventID, itemID, speaker, speakerConfidence, transcript, extraMetadata, false, "")
}

// appendRoomChatTranscript injects a typed room-chat message into the
// transcript stream. Typed text is deliberate, so it bypasses the
// transcriptLooksUseful filler filter that guards spoken transcripts.
func (store *meetingMemoryStore) appendRoomChatTranscript(eventID string, speaker string, text string) (meetingMemoryEntry, bool, error) {
	return store.appendRoomChatTranscriptWithMetadata(eventID, speaker, text, nil)
}

// appendRoomChatTranscriptWithMetadata is appendRoomChatTranscript with extra
// metadata (e.g. artifactId on close-the-loop delivery messages); the
// room_chat source marker always wins.
func (store *meetingMemoryStore) appendRoomChatTranscriptWithMetadata(eventID string, speaker string, text string, extraMetadata map[string]string) (meetingMemoryEntry, bool, error) {
	return store.appendRoomChatTranscriptForMeeting(eventID, speaker, text, extraMetadata, "")
}

// appendRoomChatTranscriptForMeeting is appendRoomChatTranscriptWithMetadata
// gated on the meeting id: a non-empty expectedMeetingID must still be the
// active meeting id — validated under the store lock, atomically with the
// meetingId stamp — or the append is skipped (appended=false). This is the
// close-the-loop delivery guard: an archive/idle rotation racing the delivery
// can never lazily mint a phantom meeting or leak the card into the successor
// meeting's transcript stream.
func (store *meetingMemoryStore) appendRoomChatTranscriptForMeeting(eventID string, speaker string, text string, extraMetadata map[string]string, expectedMeetingID string) (meetingMemoryEntry, bool, error) {
	metadata := make(map[string]string, len(extraMetadata)+1)
	for key, value := range extraMetadata {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		metadata[key] = value
	}
	metadata["source"] = transcriptSourceRoomChat
	return store.appendAttributedTranscriptEntry(eventID, "", speaker, "", text, metadata, true, expectedMeetingID)
}

func (store *meetingMemoryStore) appendAttributedTranscriptEntry(eventID string, itemID string, speaker string, speakerConfidence string, transcript string, extraMetadata map[string]string, bypassUsefulnessFilter bool, expectedMeetingID string) (meetingMemoryEntry, bool, error) {
	transcript = normalizeMemoryText(canonicalizeDomainTerms(transcript))
	if store == nil || transcript == "" {
		return meetingMemoryEntry{}, false, nil
	}
	if !bypassUsefulnessFilter && !transcriptLooksUseful(transcript) {
		return meetingMemoryEntry{}, false, nil
	}

	id := strings.TrimSpace(eventID)
	if id == "" {
		id = strings.TrimSpace(itemID)
	}
	if id == "" {
		id = fmt.Sprintf("transcript-%d", time.Now().UnixNano())
	}

	metadata := map[string]string{
		"itemId": itemID,
	}
	for key, value := range extraMetadata {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		metadata[key] = value
	}
	if speaker = normalizeTranscriptSpeaker(speaker); speaker != "" {
		transcript = formatSpeakerTranscript(speaker, transcript)
		metadata["speaker"] = speaker
		if speakerConfidence != "" {
			metadata["speakerConfidence"] = speakerConfidence
		}
	}

	return store.appendEntryForMeeting(meetingMemoryKindTranscript, id, transcript, metadata, expectedMeetingID)
}

func (store *meetingMemoryStore) appendBrainWriteUp(id string, text string, metadata map[string]string) (meetingMemoryEntry, bool, error) {
	return store.appendEntry(meetingMemoryKindBrain, id, text, metadata)
}

func (store *meetingMemoryStore) appendBoardUpdate(id string, text string, metadata map[string]string) (meetingMemoryEntry, bool, error) {
	return store.appendEntry(meetingMemoryKindBoardUpdate, id, text, metadata)
}

func (store *meetingMemoryStore) appendArchive(id string, text string, metadata map[string]string) (meetingMemoryEntry, bool, error) {
	return store.appendEntry(meetingMemoryKindArchive, id, text, metadata)
}

func (store *meetingMemoryStore) appendOSArtifact(id string, text string, metadata map[string]string) (meetingMemoryEntry, bool, error) {
	return store.appendEntry(meetingMemoryKindOSArtifact, id, text, metadata)
}

func (store *meetingMemoryStore) appendScoutChatThread(id string, text string, metadata map[string]string) (meetingMemoryEntry, bool, error) {
	return store.appendEntry(meetingMemoryKindScoutChat, id, text, metadata)
}

func (store *meetingMemoryStore) appendCodexProposal(id string, text string, metadata map[string]string) (meetingMemoryEntry, bool, error) {
	return store.appendEntry(meetingMemoryKindCodexProposal, id, text, metadata)
}

func (store *meetingMemoryStore) appendMissionInsight(id string, text string, metadata map[string]string) (meetingMemoryEntry, bool, error) {
	return store.appendEntry(meetingMemoryKindMissionInsight, id, text, metadata)
}

func (store *meetingMemoryStore) appendDecision(id string, text string, metadata map[string]string) (meetingMemoryEntry, bool, error) {
	return store.appendEntry(meetingMemoryKindDecision, id, text, metadata)
}

func (store *meetingMemoryStore) appendDecisionPass(id string, text string, metadata map[string]string) (meetingMemoryEntry, bool, error) {
	return store.appendEntry(meetingMemoryKindDecisionPass, id, text, metadata)
}

func (store *meetingMemoryStore) appendVenturePackage(id string, text string, metadata map[string]string) (meetingMemoryEntry, bool, error) {
	return store.appendEntry(meetingMemoryKindPackage, id, text, metadata)
}

func (store *meetingMemoryStore) appendSlopPass(id string, text string, metadata map[string]string) (meetingMemoryEntry, bool, error) {
	return store.appendEntry(meetingMemoryKindSlopPass, id, text, metadata)
}

func (store *meetingMemoryStore) updateScoutChatThread(id string, text string, metadataUpdates map[string]string) (meetingMemoryEntry, bool, error) {
	return store.updateEntryWithMetadata(meetingMemoryKindScoutChat, id, text, metadataUpdates)
}

func (store *meetingMemoryStore) updateOSArtifact(id string, title string, text string, updatedBy string) (meetingMemoryEntry, bool, error) {
	return store.updateOSArtifactWithMetadata(id, title, text, updatedBy, nil)
}

func (store *meetingMemoryStore) updateOSArtifactWithMetadata(id string, title string, text string, updatedBy string, metadataUpdates map[string]string) (meetingMemoryEntry, bool, error) {
	if store == nil {
		return meetingMemoryEntry{}, false, fmt.Errorf("memory store is unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return meetingMemoryEntry{}, false, fmt.Errorf("artifact id is required")
	}
	text = normalizeMemoryEntryText(meetingMemoryKindOSArtifact, text)
	if text == "" {
		return meetingMemoryEntry{}, false, fmt.Errorf("artifact text is required")
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	index := -1
	for candidateIndex, entry := range store.entries {
		if entry.ID == id && entry.Kind == meetingMemoryKindOSArtifact {
			index = candidateIndex
			break
		}
	}
	if index < 0 {
		return meetingMemoryEntry{}, false, fmt.Errorf("artifact not found")
	}

	previousEntry := store.entries[index]
	entry := cloneMemoryEntry(previousEntry)
	if entry.Metadata == nil {
		entry.Metadata = map[string]string{}
	}
	nextTitle := strings.TrimSpace(title)
	if nextTitle == "" {
		nextTitle = entry.Metadata["title"]
	}
	nextUpdatedBy := strings.TrimSpace(updatedBy)
	changed := entry.Text != text || entry.Metadata["title"] != nextTitle
	normalizedMetadataUpdates := make(map[string]string, len(metadataUpdates))
	for key, value := range metadataUpdates {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		value = strings.TrimSpace(value)
		normalizedMetadataUpdates[key] = value
		if entry.Metadata[key] != value {
			changed = true
		}
	}
	if !changed {
		return cloneMemoryEntry(entry), false, nil
	}
	entry.Metadata["title"] = nextTitle
	for key, value := range normalizedMetadataUpdates {
		entry.Metadata[key] = value
	}
	if nextUpdatedBy != "" {
		entry.Metadata["updatedBy"] = nextUpdatedBy
	}
	entry.Metadata["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	entry.Text = text

	store.entries[index] = entry
	if err := store.rewriteLocked(); err != nil {
		store.entries[index] = previousEntry
		return meetingMemoryEntry{}, false, err
	}

	return cloneMemoryEntry(entry), changed, nil
}

func (store *meetingMemoryStore) updateEntryWithMetadata(kind string, id string, text string, metadataUpdates map[string]string) (meetingMemoryEntry, bool, error) {
	if store == nil {
		return meetingMemoryEntry{}, false, fmt.Errorf("memory store is unavailable")
	}
	kind = strings.TrimSpace(kind)
	id = strings.TrimSpace(id)
	if kind == "" || id == "" {
		return meetingMemoryEntry{}, false, fmt.Errorf("memory entry id and kind are required")
	}
	text = normalizeMemoryEntryText(kind, text)
	if text == "" {
		return meetingMemoryEntry{}, false, fmt.Errorf("memory entry text is required")
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	index := -1
	for candidateIndex, entry := range store.entries {
		if entry.ID == id && entry.Kind == kind {
			index = candidateIndex
			break
		}
	}
	if index < 0 {
		return meetingMemoryEntry{}, false, fmt.Errorf("memory entry not found")
	}

	previousEntry := store.entries[index]
	entry := cloneMemoryEntry(previousEntry)
	if entry.Metadata == nil {
		entry.Metadata = map[string]string{}
	}
	changed := entry.Text != text
	for key, value := range metadataUpdates {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		value = strings.TrimSpace(value)
		if entry.Metadata[key] != value {
			changed = true
			entry.Metadata[key] = value
		}
	}
	if !changed {
		return cloneMemoryEntry(entry), false, nil
	}
	entry.Text = text

	store.entries[index] = entry
	if err := store.rewriteLocked(); err != nil {
		store.entries[index] = previousEntry
		return meetingMemoryEntry{}, false, err
	}

	return cloneMemoryEntry(entry), true, nil
}

func (store *meetingMemoryStore) appendEntry(kind string, id string, text string, metadata map[string]string) (meetingMemoryEntry, bool, error) {
	return store.appendEntryForMeeting(kind, id, text, metadata, "")
}

// appendEntryForMeeting is appendEntry with an optional meeting-id gate: a
// non-empty expectedMeetingID that no longer matches the active meeting id
// (checked under the lock, atomically with the meetingId stamp) skips the
// append with appended=false and no error — the caller's origin meeting is
// simply over.
func (store *meetingMemoryStore) appendEntryForMeeting(kind string, id string, text string, metadata map[string]string, expectedMeetingID string) (meetingMemoryEntry, bool, error) {
	if strings.TrimSpace(kind) == "" {
		kind = meetingMemoryKindTranscript
	}
	kind = strings.TrimSpace(kind)
	text = normalizeMemoryEntryText(kind, text)
	if store == nil || text == "" {
		return meetingMemoryEntry{}, false, nil
	}
	if strings.TrimSpace(id) == "" {
		id = fmt.Sprintf("%s-%d", kind, time.Now().UnixNano())
	}

	entry := meetingMemoryEntry{
		ID:        strings.TrimSpace(id),
		Kind:      kind,
		Text:      text,
		CreatedAt: time.Now().UTC(),
		Metadata:  metadata,
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	if _, ok := store.seen[entry.ID]; ok {
		return entry, false, nil
	}
	if expectedMeetingID = strings.TrimSpace(expectedMeetingID); expectedMeetingID != "" && store.meetingID != expectedMeetingID {
		return meetingMemoryEntry{}, false, nil
	}

	// stamp every entry with the current meeting id (created lazily at the
	// first entry of a meeting). entries without one stay readable.
	stamped := make(map[string]string, len(metadata)+1)
	for key, value := range metadata {
		stamped[key] = value
	}
	if strings.TrimSpace(stamped["meetingId"]) == "" {
		stamped["meetingId"] = store.currentMeetingIDLocked()
	}
	entry.Metadata = stamped

	file, err := os.OpenFile(store.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return meetingMemoryEntry{}, false, fmt.Errorf("open memory file: %w", err)
	}
	defer file.Close()

	raw, err := json.Marshal(entry)
	if err != nil {
		return meetingMemoryEntry{}, false, fmt.Errorf("encode memory entry: %w", err)
	}
	if _, err := file.Write(append(raw, '\n')); err != nil {
		return meetingMemoryEntry{}, false, fmt.Errorf("write memory entry: %w", err)
	}

	store.entries = append(store.entries, entry)
	store.seen[entry.ID] = struct{}{}

	return entry, true, nil
}

func (store *meetingMemoryStore) rewriteLocked() error {
	dir := filepath.Dir(store.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create memory directory: %w", err)
	}

	file, err := os.CreateTemp(dir, ".meeting-memory-*.jsonl")
	if err != nil {
		return fmt.Errorf("create memory temp file: %w", err)
	}
	tempPath := file.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()

	encoder := json.NewEncoder(file)
	for _, entry := range store.entries {
		if err := encoder.Encode(entry); err != nil {
			_ = file.Close()
			return fmt.Errorf("encode memory entry: %w", err)
		}
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("chmod memory temp file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close memory temp file: %w", err)
	}
	if err := os.Rename(tempPath, store.path); err != nil {
		return fmt.Errorf("replace memory file: %w", err)
	}
	cleanup = false

	return nil
}

func (store *meetingMemoryStore) snapshot(limit int) []meetingMemoryEntry {
	if store == nil {
		return nil
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	return cloneMemoryEntries(tailMemoryEntries(store.visibleEntriesLocked(), limit))
}

// visibleEntriesLocked returns store.entries minus the entries that must never
// reach the client memory timeline or the snapshot-fed model-context lanes:
// quarantined/expired material (forgotten) and slop_pass cursor/audit stubs
// (pure bookkeeping — visibleMeetingMemoryEntries filters the other UI-state
// kinds by name but predates slop_pass, so it is dropped here). Callers that
// must SEE quarantined entries (the quarantine tray, the classifier, expiry)
// read store.entries via entriesByRelevance/entriesOfKind, not snapshot.
func (store *meetingMemoryStore) visibleEntriesLocked() []meetingMemoryEntry {
	visible := make([]meetingMemoryEntry, 0, len(store.entries))
	for _, entry := range store.entries {
		if entry.Kind == meetingMemoryKindSlopPass || memoryEntryHiddenFromRecall(entry) {
			continue
		}
		visible = append(visible, entry)
	}
	return visible
}

func (store *meetingMemoryStore) snapshotForMeeting(meetingID string, limit int) []meetingMemoryEntry {
	if store == nil {
		return nil
	}
	meetingID = strings.TrimSpace(meetingID)
	if meetingID == "" {
		return nil
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	entries := make([]meetingMemoryEntry, 0, len(store.entries))
	for _, entry := range store.visibleEntriesLocked() {
		if strings.TrimSpace(entry.Metadata["meetingId"]) != meetingID {
			continue
		}
		entries = append(entries, entry)
	}

	return cloneMemoryEntries(tailMemoryEntries(entries, limit))
}

// entriesOfKind returns the newest entries of one kind, oldest first.
func (store *meetingMemoryStore) entriesOfKind(kind string, limit int) []meetingMemoryEntry {
	if store == nil {
		return nil
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	matched := make([]meetingMemoryEntry, 0)
	for _, entry := range store.entries {
		if entry.Kind == kind {
			matched = append(matched, entry)
		}
	}

	return cloneMemoryEntries(tailMemoryEntries(matched, limit))
}

// entryByKindAndID looks up a single entry; newest wins if ids ever collide
// across rewrites.
func (store *meetingMemoryStore) entryByKindAndID(kind string, id string) (meetingMemoryEntry, bool) {
	if store == nil {
		return meetingMemoryEntry{}, false
	}
	kind = strings.TrimSpace(kind)
	id = strings.TrimSpace(id)
	if kind == "" || id == "" {
		return meetingMemoryEntry{}, false
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	for index := len(store.entries) - 1; index >= 0; index-- {
		entry := store.entries[index]
		if entry.ID == id && entry.Kind == kind {
			return cloneMemoryEntry(entry), true
		}
	}

	return meetingMemoryEntry{}, false
}

// entryByID looks up a single entry by id across all kinds (newest wins on an
// id collision). Used by the quarantine restore/delete paths, which key on the
// entry id alone and infer the kind from the found entry.
func (store *meetingMemoryStore) entryByID(id string) (meetingMemoryEntry, bool) {
	if store == nil {
		return meetingMemoryEntry{}, false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return meetingMemoryEntry{}, false
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	for index := len(store.entries) - 1; index >= 0; index-- {
		if store.entries[index].ID == id {
			return cloneMemoryEntry(store.entries[index]), true
		}
	}

	return meetingMemoryEntry{}, false
}

// entriesByRelevance returns every entry in the given lifecycle state, newest
// first. The classifier and the quarantine tray read the store directly this
// way (never through snapshot, which HIDES quarantined/expired material).
func (store *meetingMemoryStore) entriesByRelevance(relevance string) []meetingMemoryEntry {
	if store == nil {
		return nil
	}
	relevance = strings.TrimSpace(strings.ToLower(relevance))
	if relevance == "" {
		return nil
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	matched := make([]meetingMemoryEntry, 0)
	for index := len(store.entries) - 1; index >= 0; index-- {
		if memoryEntryRelevance(store.entries[index]) == relevance {
			matched = append(matched, cloneMemoryEntry(store.entries[index]))
		}
	}

	return matched
}

// deleteEntryByID hard-deletes one entry (any kind) and rewrites the log. This
// is the ONLY hard delete in the system — the expiry job's terminal step, always
// paired with a slop_pass audit stub so the fact of deletion survives. Reports
// whether an entry was removed.
func (store *meetingMemoryStore) deleteEntryByID(id string) (meetingMemoryEntry, bool, error) {
	if store == nil {
		return meetingMemoryEntry{}, false, fmt.Errorf("memory store is unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return meetingMemoryEntry{}, false, fmt.Errorf("entry id is required")
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	index := -1
	for candidate := len(store.entries) - 1; candidate >= 0; candidate-- {
		if store.entries[candidate].ID == id {
			index = candidate
			break
		}
	}
	if index < 0 {
		return meetingMemoryEntry{}, false, nil
	}

	removed := cloneMemoryEntry(store.entries[index])
	store.entries = append(store.entries[:index], store.entries[index+1:]...)
	if err := store.rewriteLocked(); err != nil {
		// restore the in-memory slice so a failed rewrite is not silently lossy.
		store.entries = append(store.entries[:index], append([]meetingMemoryEntry{removed}, store.entries[index:]...)...)
		return meetingMemoryEntry{}, false, err
	}
	delete(store.seen, id)

	return removed, true, nil
}

// isUIStateMemoryKind reports the entry kinds that are workspace/UI state
// rather than meeting knowledge — chat threads, codex proposals, mission
// insights, decision-pass cursors, and venture-package records never enter
// Scout's search results or model context. Kind "decision" is deliberately
// absent: decision statements ARE knowledge and must ground Scout's answers.
func isUIStateMemoryKind(kind string) bool {
	return kind == meetingMemoryKindScoutChat || kind == meetingMemoryKindCodexProposal || kind == meetingMemoryKindMissionInsight || kind == meetingMemoryKindDecisionPass || kind == meetingMemoryKindPackage || kind == meetingMemoryKindDealRoom || kind == meetingMemoryKindSlopPass
}

func (store *meetingMemoryStore) search(query string, limit int) []meetingMemoryMatch {
	if store == nil || limit <= 0 {
		return nil
	}

	query = normalizeMemoryText(canonicalizeDomainTerms(query))
	if query == "" {
		return nil
	}

	queryTokens := uniqueMemoryTokens(query)
	if len(queryTokens) == 0 {
		return nil
	}
	// Query expansion (Wave 7): OR a curated set of synonyms into token
	// matching, at a slightly lower weight than the raw tokens, so a
	// vocabulary mismatch ("runway" vs "cash-out") still surfaces the entry.
	// Pure static map (domain_terms.go) — no model call in the search path.
	synonymTokens := expandRecallSynonyms(queryTokens)

	store.mu.Lock()
	entries := cloneMemoryEntries(store.entries)
	store.mu.Unlock()

	matches := make([]meetingMemoryMatch, 0, len(entries))
	lowerQuery := strings.ToLower(query)
	for _, entry := range entries {
		if isUIStateMemoryKind(entry.Kind) {
			continue
		}
		// quarantined/expired material is forgotten: never a recall candidate.
		if memoryEntryHiddenFromRecall(entry) {
			continue
		}
		lowerText := strings.ToLower(entry.Text)
		score := 0
		if strings.Contains(lowerText, lowerQuery) {
			score += 10
		}
		for _, token := range queryTokens {
			if strings.Contains(lowerText, token) {
				score += 3
			}
		}
		for _, token := range synonymTokens {
			if strings.Contains(lowerText, token) {
				score += 2
			}
		}
		if score == 0 {
			continue
		}
		// archived material stays searchable but ranks below active (design §6).
		if memoryEntryRelevance(entry) == relevanceArchived {
			if score -= archivedSearchPenalty; score < 1 {
				score = 1
			}
		}
		matches = append(matches, meetingMemoryMatch{Entry: entry, Score: score})
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Entry.CreatedAt.After(matches[j].Entry.CreatedAt)
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}

	return matches
}

func buildMemoryAnswer(query string, matches []meetingMemoryMatch) string {
	query = normalizeMemoryText(canonicalizeDomainTerms(query))
	if len(matches) == 0 {
		if query == "" {
			return "I do not have enough meeting memory yet."
		}
		return fmt.Sprintf("I could not find anything in meeting memory for %q.", query)
	}

	parts := make([]string, 0, len(matches)+1)
	parts = append(parts, fmt.Sprintf("I found %d relevant memory item(s) for %q:", len(matches), query))
	for _, match := range matches {
		parts = append(parts, fmt.Sprintf("- %s", match.Entry.Text))
	}

	return strings.Join(parts, "\n")
}

func normalizeTranscriptSpeaker(speaker string) string {
	speaker = normalizeMemoryText(speaker)
	if speaker == "" {
		return ""
	}

	parts := strings.Split(speaker, "+")
	normalizedParts := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if canonical := canonicalParticipantName(part); canonical != "" {
			normalizedParts = append(normalizedParts, canonical)
		}
	}
	if len(normalizedParts) > 0 {
		return strings.Join(uniqueStrings(normalizedParts), " + ")
	}

	if canonical := canonicalParticipantName(speaker); canonical != "" {
		return canonical
	}

	return ""
}

func formatSpeakerTranscript(speaker string, transcript string) string {
	speaker = normalizeTranscriptSpeaker(speaker)
	transcript = normalizeMemoryText(transcript)
	if speaker == "" || transcript == "" {
		return transcript
	}
	if transcriptHasSpeakerPrefix(transcript, speaker) {
		return transcript
	}

	return speaker + ": " + transcript
}

func transcriptHasSpeakerPrefix(transcript string, speaker string) bool {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return false
	}
	for _, part := range strings.Split(speaker, "+") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(transcript), strings.ToLower(part)+":") {
			return true
		}
	}

	return false
}

func normalizeMemoryText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func normalizeMemoryEntryText(kind string, value string) string {
	if kind != meetingMemoryKindBrain && kind != meetingMemoryKindBoardUpdate && kind != meetingMemoryKindOSArtifact && kind != meetingMemoryKindScoutChat && kind != meetingMemoryKindMissionInsight && kind != meetingMemoryKindPackage && kind != meetingMemoryKindDealRoom {
		return normalizeMemoryText(value)
	}

	value = strings.ReplaceAll(strings.TrimSpace(value), "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	lines := strings.Split(value, "\n")
	normalizedLines := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimRight(strings.TrimSpace(line), " ")
		if line == "" {
			if blank {
				continue
			}
			blank = true
			normalizedLines = append(normalizedLines, "")
			continue
		}
		blank = false
		normalizedLines = append(normalizedLines, line)
	}

	return strings.TrimSpace(strings.Join(normalizedLines, "\n"))
}

func transcriptLooksUseful(value string) bool {
	normalized := strings.ToLower(strings.Trim(value, " \t\r\n.,!?;:'\"()[]{}"))
	if normalized == "" {
		return false
	}
	if _, ok := lowQualityTranscriptPhrases[normalized]; ok {
		return false
	}

	tokens := memoryTokenPattern.FindAllString(normalized, -1)
	if len(tokens) == 0 {
		return false
	}
	meaningfulTokens := 0
	for _, token := range tokens {
		if _, ok := lowQualityTranscriptPhrases[token]; ok {
			continue
		}
		if len(token) >= 3 {
			meaningfulTokens++
		}
	}

	return meaningfulTokens > 0
}

func uniqueMemoryTokens(value string) []string {
	rawTokens := memoryTokenPattern.FindAllString(strings.ToLower(value), -1)
	seen := map[string]struct{}{}
	tokens := make([]string, 0, len(rawTokens))
	for _, token := range rawTokens {
		if len(token) < 3 {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}

	return tokens
}

func tailMemoryEntries(entries []meetingMemoryEntry, limit int) []meetingMemoryEntry {
	if limit <= 0 || len(entries) <= limit {
		return entries
	}

	return entries[len(entries)-limit:]
}

func cloneMemoryEntries(entries []meetingMemoryEntry) []meetingMemoryEntry {
	cloned := make([]meetingMemoryEntry, 0, len(entries))
	for _, entry := range entries {
		cloned = append(cloned, cloneMemoryEntry(entry))
	}

	return cloned
}

func cloneMemoryEntry(entry meetingMemoryEntry) meetingMemoryEntry {
	cloned := entry
	if entry.Metadata != nil {
		cloned.Metadata = make(map[string]string, len(entry.Metadata))
		for key, value := range entry.Metadata {
			cloned.Metadata[key] = value
		}
	}

	return cloned
}
