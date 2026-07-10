package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
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
	// Digest tiers (Track-2 Wave 1). One compact rollup entry per scope,
	// written ONLY through upsertDigest (supersede-in-place, exactly one
	// current entry per (kind, digestKey)). Deliberately NOT UI-state kinds:
	// unlike mission_insight they must be recall-eligible — their bounded
	// ~4KB bodies (producer MaxOutputTokens-capped, Wave 2) make that
	// token-safe, and isPromptBodyCapExemptKind exempts them from the
	// prompt-body cap so a long rollup is never stubbed.
	//   meeting_digest — one anchored-JSON digest per meetingId (digestKey =
	//   meetingId; span metadata = the meeting's covered window).
	meetingMemoryKindMeetingDigest = "meeting_digest"
	//   day_digest — one rollup per local calendar day (digestKey = the
	//   dayBucket key, bucketed on entry CreatedAt in meetingTimeLocation so a
	//   marathon meeting still splits into per-day slices).
	meetingMemoryKindDayDigest = "day_digest"
	//   company_digest — the latest-only running fold across recent days
	//   (digestKey = companyDigestKey).
	meetingMemoryKindCompanyDigest = "company_digest"
	// meetingMemoryKindDayDigestPass is the day-digest agent's per-pass cursor
	// artifact (Track-2 Wave 2, mirrors decision_pass): the durable
	// throughMeetingDigestId cursor must advance even on a pass that rebuilt
	// nothing, or a window of superseded/unparseable inputs would re-feed
	// every tick and, once it exceeded the batch cap, starve newer digests
	// forever. Pure bookkeeping: UI state, never knowledge. Written through
	// appendAmbientEntry, so it carries NO meetingId and never mints one.
	meetingMemoryKindDayDigestPass = "day_digest_pass"
	// meetingMemoryKindReflection is the end-of-day synthesis entry (Track-2
	// amendment A3) riding the day-digest tick: recurring blockers, consensus
	// forming/diverging, decisions circled without closure, ownership drift —
	// synthesis ACROSS recent digests + decision-ledger deltas, not another
	// aggregation tier. Deliberately recall-eligible knowledge prose (NOT UI
	// state — it grounds Scout search and query context like kind=decision),
	// append-only, at most one per local calendar day, anchored to its
	// supporting digests via metadata. Written through appendAmbientEntry: a
	// reflection describes a PAST day, so it must neither join the live
	// meeting's snapshotForMeeting nor lazily mint a meeting id at idle.
	meetingMemoryKindReflection = "reflection"
	// meetingMemoryKindLedgerEvent is one entity-ledger consolidation op
	// (Track-2 Wave 3, amendment A1): entry.Text = a ledgerEventPayload JSON
	// carrying the op (add/update/supersede/close) plus the FULL post-state of
	// one canonical ledger record. The append-only event log is the ledger's
	// source of truth; the entity-keyed read-model is derived by folding these
	// entries in log order (ledgerState, entity_ledger.go) and is therefore
	// rebuildable from scratch. Record JSON, never prose: UI state (excluded
	// from Scout search/context and the client timeline) — recall reads the
	// FOLDED state view, never raw events. Written through appendLedgerEvents
	// (mint-free: a ledger record spans meetings, so no meetingId is stamped).
	meetingMemoryKindLedgerEvent = "ledger_event"
	// meetingMemoryKindLedgerPass is the entity-ledger agent's per-pass cursor
	// artifact (the decision_pass pattern): carries throughMeetingDigestId and
	// throughDecisionId so a zero-event pass still advances consumption. Pure
	// bookkeeping: UI state, never knowledge. Written through
	// appendAmbientEntry, so it carries NO meetingId and never mints one.
	meetingMemoryKindLedgerPass = "ledger_pass"
	// meetingMemoryKindRunLog is one compact line per terminal agent run
	// (complete or error): who requested what, how it ended, and which artifact
	// holds the deliverable — never the deliverable body itself. Deliberately
	// NOT a UI-state kind (the decision precedent): run history is knowledge
	// ("did we already research Samsung?"), so store.search must surface it.
	// Excluded from the client memory timeline via visibleMeetingMemoryEntries
	// (runs render as thread cards and completion notifications already).
	meetingMemoryKindRunLog = "run_log"
	// meetingMemoryKindNarrative is one living storyline dossier — an
	// opportunity, client, or project narrative the narrative maintainer
	// (narrative_maintainer.go) keeps current. Deliberately NOT a UI-state
	// kind: "fill me in on the history of the Samsung opportunity" must ground
	// on these bodies through store.search. Exactly ONE active entry exists
	// per storyline slug — an update appends the new version and expires the
	// predecessor via the relevance lifecycle, so recall only ever sees the
	// latest. Excluded from the client memory timeline (the intel surface
	// renders storylines from the mission snapshot instead).
	meetingMemoryKindNarrative = "narrative"
	defaultMeetingMemoryPath   = "data/meeting-memory.jsonl"
	// transcriptSourceRoomChat marks transcript entries injected from the
	// in-meeting text chat rather than the audio transcription lanes.
	transcriptSourceRoomChat = "room_chat"
)

// relevance lifecycle (Wave 7). Stored on meetingMemoryEntry.Metadata under
// key "relevance"; absent == active. active/archived stay searchable (archived
// is down-ranked in store.search); quarantined/expired are excluded from recall,
// model context, and the client timeline. quarantined hard-deletes 30 visible
// days after quarantinedAt, leaving a slop_pass audit stub.
//
// Digest-kind exception (Track-2 Wave 1): a relevanceArchived meeting/day/
// company digest is a SUPERSEDED rollup — dead state, not down-ranked
// knowledge — so memoryEntryHiddenFromRecall hides it entirely; the
// replacement digest carries the same facts.
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
// context, and the client timeline: quarantined or expired material, plus
// SUPERSEDED digests (a digest-kind entry upsertDigest marked archived) —
// exactly one current digest per (kind, digestKey) may ever ground recall,
// otherwise a stale rollup could contradict its replacement.
func memoryEntryHiddenFromRecall(entry meetingMemoryEntry) bool {
	switch memoryEntryRelevance(entry) {
	case relevanceQuarantined, relevanceExpired:
		return true
	case relevanceArchived:
		return isMeetingDigestKind(entry.Kind)
	}
	return false
}

// --- First-class Artifact model: version lineage (packaging OS §4, Wave 3
// item 13). A formalization over the existing metadata map, NOT a storage
// migration: the JSONL shape is unchanged and artifacts written before these
// keys existed read back identically (version 1, no parent, no history).
const (
	// artifactVersionMetadataKey holds the current lineage counter as a
	// decimal string; absent == 1.
	artifactVersionMetadataKey = "artifactVersion"
	// artifactParentVersionIDMetadataKey points at the version this body
	// superseded ("<artifactId>@v<N-1>"); absent on a never-edited artifact.
	artifactParentVersionIDMetadataKey = "parentVersionId"
	// artifactVersionsMetadataKey holds the JSON journal of superseded
	// versions ([]artifactVersionRecord).
	artifactVersionsMetadataKey = "artifactVersions"
)

// artifactVersionRecord is one line of the lineage journal kept under the
// artifactVersions metadata key: the snapshot of a PRIOR version at the moment
// an edit superseded it. BodyBlobRef points at the superseded body in the
// content-addressed blob store when the blob seam is wired (blobs.go, same
// wave); lineage stays intact without it — the ref is simply absent.
type artifactVersionRecord struct {
	V           int    `json:"v"`
	EditedBy    string `json:"editedBy,omitempty"`
	At          string `json:"at,omitempty"`
	BodyBlobRef string `json:"bodyBlobRef,omitempty"`
}

// artifactVersionBlobStore is the seam blobs.go assigns (at init) to persist a
// superseded body as a content-addressed blob and return its ref. nil — or an
// error — degrades gracefully to a version record without a bodyBlobRef,
// exactly how an absent codex sidecar degrades: the feature narrows, nothing
// breaks.
var artifactVersionBlobStore func(artifactID string, version int, body string) (string, error)

// artifactVersion reads the lineage counter; artifacts written before the
// model was formalized carry no key and read back as version 1.
func artifactVersion(entry meetingMemoryEntry) int {
	if version, err := strconv.Atoi(strings.TrimSpace(entry.Metadata[artifactVersionMetadataKey])); err == nil && version >= 1 {
		return version
	}
	return 1
}

// artifactParentVersionID is the pointer to the superseded version; empty for
// a never-edited artifact.
func artifactParentVersionID(entry meetingMemoryEntry) string {
	return strings.TrimSpace(entry.Metadata[artifactParentVersionIDMetadataKey])
}

// artifactVersionID names one version of one artifact.
func artifactVersionID(artifactID string, version int) string {
	return fmt.Sprintf("%s@v%d", strings.TrimSpace(artifactID), version)
}

// artifactVersionHistory decodes the lineage journal, oldest first. Malformed
// or absent metadata reads back as no history — old artifacts stay readable.
func artifactVersionHistory(entry meetingMemoryEntry) []artifactVersionRecord {
	raw := strings.TrimSpace(entry.Metadata[artifactVersionsMetadataKey])
	if raw == "" {
		return nil
	}
	var records []artifactVersionRecord
	if err := json.Unmarshal([]byte(raw), &records); err != nil {
		return nil
	}
	return records
}

// appendArtifactVersionRecord appends one record to the encoded journal,
// tolerating a malformed existing value (the journal restarts rather than
// blocking the edit).
func appendArtifactVersionRecord(existing string, record artifactVersionRecord) string {
	records := artifactVersionHistory(meetingMemoryEntry{Metadata: map[string]string{artifactVersionsMetadataKey: existing}})
	records = append(records, record)
	raw, err := json.Marshal(records)
	if err != nil {
		return existing
	}
	return string(raw)
}

// bumpArtifactVersionLocked mints version+1 on entry — called under store.mu
// just before a body edit lands, while entry still carries the superseded
// body's editor/timestamp metadata. It journals the superseded version (with a
// body blob ref when the seam is wired) and repoints parentVersionId at it.
// Every writer funnels through updateOSArtifactWithMetadata, so gate revisions
// and human PATCH edits inherit the same lineage for free.
func bumpArtifactVersionLocked(entry *meetingMemoryEntry, previousBody string) {
	prior := artifactVersion(*entry)
	record := artifactVersionRecord{
		V:        prior,
		EditedBy: firstNonEmptyString(entry.Metadata["updatedBy"], entry.Metadata["createdBy"]),
		At:       firstNonEmptyString(entry.Metadata["updatedAt"], entry.CreatedAt.UTC().Format(time.RFC3339Nano)),
	}
	if artifactVersionBlobStore != nil {
		if ref, err := artifactVersionBlobStore(entry.ID, prior, previousBody); err == nil {
			record.BodyBlobRef = strings.TrimSpace(ref)
		} else {
			log.Warnf("artifact %s v%d body snapshot failed (lineage kept without ref): %v", entry.ID, prior, err)
		}
	}
	entry.Metadata[artifactVersionMetadataKey] = strconv.Itoa(prior + 1)
	entry.Metadata[artifactParentVersionIDMetadataKey] = artifactVersionID(entry.ID, prior)
	entry.Metadata[artifactVersionsMetadataKey] = appendArtifactVersionRecord(entry.Metadata[artifactVersionsMetadataKey], record)
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
	mu      sync.Mutex
	path    string
	entries []meetingMemoryEntry
	seen    map[string]struct{}
	// meetingIDs is the per-room active meeting id (multi-room W2): keyed by
	// normalized room id (normalizeRoomID — absent metadata.roomId == office),
	// one sitting id per room, minted lazily and rotated independently so one
	// room's close can never clear or redirect another room's sitting.
	meetingIDs map[string]string
	// bootLatestIDs maps entry kind to the newest entry ID already persisted
	// when the store was loaded — the baseline an ambient agent loop registers
	// at startup so it never backfills pre-boot history.
	bootLatestIDs map[string]string
	// bootLatestRoomIDs is the same boot baseline PER ROOM (kind → normalized
	// roomId → newest pre-boot id): a room-scoped agent first touching a room
	// with pre-boot history baselines at that room's newest input instead of
	// backfilling it (multi-room W4 §7.4 — a brand-new room baselines at now).
	bootLatestRoomIDs map[string]map[string]string
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
		path:              path,
		seen:              map[string]struct{}{},
		meetingIDs:        map[string]string{},
		bootLatestIDs:     map[string]string{},
		bootLatestRoomIDs: map[string]map[string]string{},
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

	// bufio.Reader, not bufio.Scanner: a shipped packaging deck (print chassis +
	// base64-inlined imagery) is a multi-megabyte artifact filed as ONE JSONL
	// line, and Scanner both caps a token (bufio.ErrTooLong) and hard-fails the
	// WHOLE load on the first over-cap line — so one deck disabled all meeting
	// memory on the next restart. ReadString grows without a fixed ceiling, and
	// a per-line json error only skips that line (matching the existing
	// malformed-entry resilience below).
	reader := bufio.NewReaderSize(file, 1024*1024)
	for {
		line, readErr := reader.ReadString('\n')
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			var entry meetingMemoryEntry
			if err := json.Unmarshal([]byte(trimmed), &entry); err != nil {
				log.Warnf("Skipping malformed memory entry: %v", err)
			} else if strings.TrimSpace(entry.ID) != "" && strings.TrimSpace(entry.Text) != "" {
				store.entries = append(store.entries, entry)
				store.seen[entry.ID] = struct{}{}
				store.bootLatestIDs[entry.Kind] = entry.ID
				if store.bootLatestRoomIDs[entry.Kind] == nil {
					store.bootLatestRoomIDs[entry.Kind] = map[string]string{}
				}
				store.bootLatestRoomIDs[entry.Kind][normalizeRoomID(entry.Metadata["roomId"])] = entry.ID
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return nil, fmt.Errorf("read memory file: %w", readErr)
		}
	}

	// resume the in-flight meetings after a restart, PER ROOM (multi-room W2):
	// for each room (absent metadata.roomId == office), if that room's newest
	// entry was not an archive, the meeting it belongs to is still open. Digest
	// rollups, reflections, ledger events, and pass artifacts are skipped: they
	// are ambient cross-meeting bookkeeping — a meeting_digest may describe a
	// PAST meeting (its meetingId stamp = the digested meeting, not the live
	// one) and the day/company digests, reflections, ledger events/passes, and
	// day-digest pass artifacts carry no meetingId at all — so one of them as
	// a room's newest line must neither clear nor redirect that room's
	// in-flight meeting id.
	decided := map[string]struct{}{}
	for index := len(store.entries) - 1; index >= 0; index-- {
		last := store.entries[index]
		if isAmbientBookkeepingMemoryKind(last.Kind) {
			continue
		}
		roomID := normalizeRoomID(last.Metadata["roomId"])
		if _, ok := decided[roomID]; ok {
			continue
		}
		decided[roomID] = struct{}{}
		if last.Kind != meetingMemoryKindArchive {
			if id := strings.TrimSpace(last.Metadata["meetingId"]); id != "" {
				store.meetingIDs[roomID] = id
			}
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

// bootBaselineIDOfKindForRoom is bootBaselineIDOfKind with the W4 room
// dimension: the newest pre-boot entry of kind whose roomId (absent == office)
// matches — the baseline a room-scoped agent registers when it first touches
// a room, so it resumes instead of backfilling. A room born after boot has no
// pre-boot entries and baselines at "" (i.e. at now).
func (store *meetingMemoryStore) bootBaselineIDOfKindForRoom(kind string, roomID string) string {
	if store == nil {
		return ""
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	if rooms := store.bootLatestRoomIDs[kind]; rooms != nil {
		return rooms[normalizeRoomID(roomID)]
	}
	return ""
}

// currentMeetingID returns the room's active meeting id, empty until the
// first entry of a meeting in that room is appended.
func (store *meetingMemoryStore) currentMeetingID(roomID string) string {
	if store == nil {
		return ""
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	return store.meetingIDs[normalizeRoomID(roomID)]
}

// rotateMeetingID closes the room's current meeting; the room's next appended
// entry lazily starts a new meeting id. Called when archive_meeting completes.
func (store *meetingMemoryStore) rotateMeetingID(roomID string) {
	if store == nil {
		return
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	delete(store.meetingIDs, normalizeRoomID(roomID))
}

// rotateMeetingIDIfCurrent rotates only while id is still the room's active
// meeting id; reports whether the rotation landed. The closing seams (idle
// end, archive) use it so a concurrent admission's freshly minted successor id
// is never clobbered by a stale close.
func (store *meetingMemoryStore) rotateMeetingIDIfCurrent(roomID string, id string) bool {
	if store == nil || strings.TrimSpace(id) == "" {
		return false
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	roomID = normalizeRoomID(roomID)
	if store.meetingIDs[roomID] != id {
		return false
	}
	delete(store.meetingIDs, roomID)
	return true
}

// ensureMeetingID mints (or returns) the room's active meeting id eagerly, so
// a meeting record can be opened at room admission before any entry appends.
func (store *meetingMemoryStore) ensureMeetingID(roomID string) string {
	if store == nil {
		return ""
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	return store.currentMeetingIDLocked(roomID)
}

func (store *meetingMemoryStore) currentMeetingIDLocked(roomID string) string {
	roomID = normalizeRoomID(roomID)
	if store.meetingIDs == nil {
		store.meetingIDs = map[string]string{}
	}
	if store.meetingIDs[roomID] == "" {
		now := time.Now().UTC()
		// nanosecond suffix keeps back-to-back meetings distinct; the bump
		// loop keeps CONCURRENT rooms distinct — two rooms minting inside the
		// same wall-clock tick (coarse clocks resolve microseconds) would
		// otherwise share one id and blend their sittings.
		nanos := now.Nanosecond()
		id := fmt.Sprintf("meeting-%s-%09d", now.Format("20060102-150405"), nanos)
		for store.meetingIDHeldByAnotherRoomLocked(roomID, id) {
			nanos = (nanos + 1) % 1_000_000_000
			id = fmt.Sprintf("meeting-%s-%09d", now.Format("20060102-150405"), nanos)
		}
		store.meetingIDs[roomID] = id
	}

	return store.meetingIDs[roomID]
}

// meetingIDHeldByAnotherRoomLocked reports whether id is already some OTHER
// room's active meeting id — the cross-room uniqueness fence for the mint.
func (store *meetingMemoryStore) meetingIDHeldByAnotherRoomLocked(roomID string, id string) bool {
	for otherRoom, otherID := range store.meetingIDs {
		if otherRoom != roomID && otherID == id {
			return true
		}
	}
	return false
}

// meetingRoomIDs lists the rooms with a live (resumed or minted) meeting id —
// the boot reconciliation walks these alongside the open meeting records.
func (store *meetingMemoryStore) meetingRoomIDs() []string {
	if store == nil {
		return nil
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	roomIDs := make([]string, 0, len(store.meetingIDs))
	for roomID := range store.meetingIDs {
		roomIDs = append(roomIDs, roomID)
	}
	sort.Strings(roomIDs)
	return roomIDs
}

func meetingMemoryPath() string {
	if path := strings.TrimSpace(os.Getenv("MEETING_MEMORY_PATH")); path != "" {
		return path
	}

	return defaultMeetingMemoryPath
}

// The office-defaulting transcript wrappers: everything below funnels into
// appendAttributedTranscriptEntry, which carries the room dimension; callers
// without a room in hand (tests, legacy seams) write office entries.
func (store *meetingMemoryStore) appendTranscript(eventID string, itemID string, transcript string) (meetingMemoryEntry, bool, error) {
	return store.appendAttributedTranscript(eventID, itemID, "", "", transcript)
}

func (store *meetingMemoryStore) appendAttributedTranscript(eventID string, itemID string, speaker string, speakerConfidence string, transcript string) (meetingMemoryEntry, bool, error) {
	return store.appendAttributedTranscriptWithMetadata(eventID, itemID, speaker, speakerConfidence, transcript, nil)
}

func (store *meetingMemoryStore) appendAttributedTranscriptWithMetadata(eventID string, itemID string, speaker string, speakerConfidence string, transcript string, extraMetadata map[string]string) (meetingMemoryEntry, bool, error) {
	return store.appendAttributedTranscriptEntry(officeRoomID, eventID, itemID, speaker, speakerConfidence, transcript, extraMetadata, false, "")
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
	return store.appendRoomChatTranscriptForMeeting(officeRoomID, eventID, speaker, text, extraMetadata, "")
}

// appendRoomChatTranscriptForMeeting is appendRoomChatTranscriptWithMetadata
// gated on the meeting id: a non-empty expectedMeetingID must still be the
// active meeting id — validated under the store lock, atomically with the
// meetingId stamp — or the append is skipped (appended=false). This is the
// close-the-loop delivery guard: an archive/idle rotation racing the delivery
// can never lazily mint a phantom meeting or leak the card into the successor
// meeting's transcript stream.
func (store *meetingMemoryStore) appendRoomChatTranscriptForMeeting(roomID string, eventID string, speaker string, text string, extraMetadata map[string]string, expectedMeetingID string) (meetingMemoryEntry, bool, error) {
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
	return store.appendAttributedTranscriptEntry(roomID, eventID, "", speaker, "", text, metadata, true, expectedMeetingID)
}

func (store *meetingMemoryStore) appendAttributedTranscriptEntry(roomID string, eventID string, itemID string, speaker string, speakerConfidence string, transcript string, extraMetadata map[string]string, bypassUsefulnessFilter bool, expectedMeetingID string) (meetingMemoryEntry, bool, error) {
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

	return store.appendEntryForMeeting(roomID, meetingMemoryKindTranscript, id, transcript, metadata, expectedMeetingID)
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

func (store *meetingMemoryStore) appendRunLog(id string, text string, metadata map[string]string) (meetingMemoryEntry, bool, error) {
	return store.appendEntry(meetingMemoryKindRunLog, id, text, metadata)
}

func (store *meetingMemoryStore) appendNarrative(id string, text string, metadata map[string]string) (meetingMemoryEntry, bool, error) {
	return store.appendEntry(meetingMemoryKindNarrative, id, text, metadata)
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
	// Version lineage (packaging OS §4): a BODY edit — engine gate revision or
	// human PATCH alike — mints version+1 and journals the superseded version
	// before the new body lands. Runs after the metadata merge so the
	// store-owned lineage keys always win, and before the updatedBy/updatedAt
	// stamps so the journal credits the superseded version's actual editor.
	// Title- or metadata-only rewrites never mint versions.
	if entry.Text != text {
		bumpArtifactVersionLocked(&entry, entry.Text)
	}
	if nextUpdatedBy != "" {
		entry.Metadata["updatedBy"] = nextUpdatedBy
	}
	entry.Metadata["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	entry.Text = text

	store.entries[index] = entry
	if err := store.rewriteLocked(false); err != nil {
		store.entries[index] = previousEntry
		return meetingMemoryEntry{}, false, err
	}

	return cloneMemoryEntry(entry), changed, nil
}

// updateOSArtifactMetadata merges ONLY metadataUpdates onto the artifact,
// re-reading the current entry under store.mu so a caller's earlier text
// snapshot can never ride along and clobber a concurrent body update (the
// openedAt-stamp race: opens happen exactly while a thread runner is writing
// the final body, and updateOSArtifactWithMetadata overwrites text with
// whatever is passed). Text, title, updatedBy, and updatedAt are untouched —
// this is a bookkeeping stamp, not an edit.
func (store *meetingMemoryStore) updateOSArtifactMetadata(id string, metadataUpdates map[string]string) (meetingMemoryEntry, bool, error) {
	if store == nil {
		return meetingMemoryEntry{}, false, fmt.Errorf("memory store is unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return meetingMemoryEntry{}, false, fmt.Errorf("artifact id is required")
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
	changed := false
	for key, value := range metadataUpdates {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		value = strings.TrimSpace(value)
		if entry.Metadata[key] != value {
			entry.Metadata[key] = value
			changed = true
		}
	}
	if !changed {
		return cloneMemoryEntry(entry), false, nil
	}

	store.entries[index] = entry
	if err := store.rewriteLocked(false); err != nil {
		store.entries[index] = previousEntry
		return meetingMemoryEntry{}, false, err
	}

	return cloneMemoryEntry(entry), true, nil
}

// updateOSArtifactsMetadataBatch merges the SAME metadataUpdates onto many
// artifacts under ONE lock with a SINGLE rewrite, the boot-migration variant of
// updateOSArtifactMetadata (which rewrites the whole JSONL per call — N stamps
// were N full re-encodes on the 2-vCPU box). It follows updateOSArtifactMetadata
// exactly: only os_artifact entries, a bookkeeping stamp that never touches
// Text/title/updatedBy, a no-op update skipped per entry, and a rollback of the
// in-memory entries if the rewrite fails. The rewrite is fsync'd (syncToDisk),
// the digest-supersession precedent for a routine full-file rewrite whose loss
// on a crash-after-rename would truncate the log. Returns the count changed.
func (store *meetingMemoryStore) updateOSArtifactsMetadataBatch(ids []string, metadataUpdates map[string]string) (int, error) {
	if store == nil {
		return 0, fmt.Errorf("memory store is unavailable")
	}
	if len(ids) == 0 || len(metadataUpdates) == 0 {
		return 0, nil
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	indexByID := make(map[string]int, len(store.entries))
	for candidateIndex, entry := range store.entries {
		if entry.Kind == meetingMemoryKindOSArtifact {
			indexByID[entry.ID] = candidateIndex
		}
	}

	type rollback struct {
		index int
		prior meetingMemoryEntry
	}
	applied := make([]rollback, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		index, ok := indexByID[id]
		if !ok {
			continue
		}
		previousEntry := store.entries[index]
		entry := cloneMemoryEntry(previousEntry)
		if entry.Metadata == nil {
			entry.Metadata = map[string]string{}
		}
		changed := false
		for key, value := range metadataUpdates {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			value = strings.TrimSpace(value)
			if entry.Metadata[key] != value {
				entry.Metadata[key] = value
				changed = true
			}
		}
		if !changed {
			continue
		}
		store.entries[index] = entry
		applied = append(applied, rollback{index: index, prior: previousEntry})
	}
	if len(applied) == 0 {
		return 0, nil
	}
	if err := store.rewriteLocked(true); err != nil {
		for _, stale := range applied {
			store.entries[stale.index] = stale.prior
		}
		return 0, err
	}
	return len(applied), nil
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
	if err := store.rewriteLocked(false); err != nil {
		store.entries[index] = previousEntry
		return meetingMemoryEntry{}, false, err
	}

	return cloneMemoryEntry(entry), true, nil
}

// appendEntry writes an office entry — the pre-multi-room default every
// ambient/agent appender still uses until W4 rooms the agent layer.
func (store *meetingMemoryStore) appendEntry(kind string, id string, text string, metadata map[string]string) (meetingMemoryEntry, bool, error) {
	// W4: a producer that pre-stamps metadata.roomId (a room-scoped ambient
	// agent writing another room's artifact) routes to THAT room's meeting id;
	// everything else keeps the office default exactly as before.
	roomID := officeRoomID
	if metadata != nil {
		roomID = normalizeRoomID(metadata["roomId"])
	}
	return store.appendEntryForMeeting(roomID, kind, id, text, metadata, "")
}

// roomIDsOfKind lists the distinct rooms (absent roomId == office) that hold
// entries of kind — the rooms a room-scoped ambient agent's safety-floor tick
// walks (multi-room W4 §7.4).
func (store *meetingMemoryStore) roomIDsOfKind(kind string) []string {
	if store == nil {
		return nil
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	seen := map[string]struct{}{}
	roomIDs := []string{}
	for _, entry := range store.entries {
		if entry.Kind != kind {
			continue
		}
		roomID := normalizeRoomID(entry.Metadata["roomId"])
		if _, ok := seen[roomID]; ok {
			continue
		}
		seen[roomID] = struct{}{}
		roomIDs = append(roomIDs, roomID)
	}
	return roomIDs
}

// appendEntryForMeeting is appendEntry with a room dimension and an optional
// meeting-id gate: a non-empty expectedMeetingID that no longer matches the
// ROOM's active meeting id (checked under the lock, atomically with the
// meetingId stamp) skips the append with appended=false and no error — the
// caller's origin meeting is simply over. Every new entry is stamped with
// metadata.roomId (§3.4) alongside the room's meetingId; readers keep treating
// absent roomId as office, so the JSONL is never rewritten.
func (store *meetingMemoryStore) appendEntryForMeeting(roomID string, kind string, id string, text string, metadata map[string]string, expectedMeetingID string) (meetingMemoryEntry, bool, error) {
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

	roomID = normalizeRoomID(roomID)

	store.mu.Lock()
	defer store.mu.Unlock()

	if _, ok := store.seen[entry.ID]; ok {
		return entry, false, nil
	}
	if expectedMeetingID = strings.TrimSpace(expectedMeetingID); expectedMeetingID != "" && store.meetingIDs[roomID] != expectedMeetingID {
		return meetingMemoryEntry{}, false, nil
	}

	// stamp every entry with the room's current meeting id (created lazily at
	// the first entry of a meeting) plus the room id itself (§3.4). entries
	// without either stay readable and read back as office.
	stamped := make(map[string]string, len(metadata)+2)
	for key, value := range metadata {
		stamped[key] = value
	}
	if strings.TrimSpace(stamped["meetingId"]) == "" {
		stamped["meetingId"] = store.currentMeetingIDLocked(roomID)
	}
	if strings.TrimSpace(stamped["roomId"]) == "" {
		stamped["roomId"] = roomID
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

func (store *meetingMemoryStore) rewriteLocked(syncToDisk bool) error {
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
	// Flush to disk before the rename when asked: digest upserts make
	// full-file rewrites routine (~every few minutes), and a crash after
	// rename with unflushed data could truncate the entire memory file.
	// Per-message rewrites skip the fsync — it multiplies suite latency
	// (the -race pass times out) and async reporters outrun assertions;
	// their crash window is unchanged from the pre-digest design.
	if syncToDisk {
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return fmt.Errorf("sync memory temp file: %w", err)
		}
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

// maxPromptBodyBytes is the store-layer cap on how many bytes of one entry's
// Text may ride a visible snapshot into a prompt-feeding lane (Track-2 Wave 0
// root-cause token fix). 8KB clears every real transcript (max observed ~7.2KB)
// while catching the multi-MB base64 os_artifact class that blew a model call
// past the 1M-token ceiling (observed 2,505,990 > 1,000,000 400). This is the
// SOURCE cap; memoryAnswerExcerpt (answer side) and
// orchestratorToolResultBudgetChars (tool-result conduit) remain layered on top.
const maxPromptBodyBytes = 8192

// promptBodyOmittedMetadataKey marks a snapshot COPY whose Text was stubbed or
// truncated by stripOversizeBody; the value is the original byte length. It is
// stamped on serve-time copies only — never persisted to the JSONL — so tests
// and clients can tell a capped body from a naturally short one. Distinct from
// main's artifact-list "bodyTrimmed" excerpt marker.
const promptBodyOmittedMetadataKey = "promptBodyOmitted"

// isPromptBodyCapExemptKind reports kinds whose Text must never be stubbed by
// the prompt-body cap:
//   - brain write-ups, the digest tiers (meeting_digest/day_digest/
//     company_digest), and end-of-day reflections are model-written summaries
//     already bounded by their producers' MaxOutputTokens — the summary layer
//     must survive whole so future long roll-ups aren't stubbed;
//   - UI-state record kinds (isUIStateMemoryKind: scout chat threads, packages,
//     deal rooms, …) carry decoded-verbatim JSON that feature code reads through
//     snapshot(0) (thread lists, channel linkage, the blob-sweep reference scan)
//     — stubbing them would corrupt records, and they are already excluded from
//     every prompt lane by the isUIStateMemoryKind guard in
//     contextEntriesForQuery/search and from the client timeline by
//     visibleMeetingMemoryEntries.
func isPromptBodyCapExemptKind(kind string) bool {
	if kind == meetingMemoryKindBrain || kind == meetingMemoryKindReflection || isMeetingDigestKind(kind) {
		return true
	}
	return isUIStateMemoryKind(kind)
}

// stripOversizeBody returns entry with its Text bounded for prompt/snapshot
// use: exempt kinds and bodies at or under maxPromptBodyBytes pass through
// unchanged; a body carrying a base64 payload is replaced whole with a stub
// (a base64 prefix is pure token noise — the full body stays in the store and
// is reachable by id via entriesOfKind/osArtifactByID); any other oversize
// body keeps a rune-safe prefix plus an omission marker naming the id so
// drill-down still works. The input entry is never mutated — Text is replaced
// on the value copy and Metadata is cloned before the omission stamp.
func stripOversizeBody(entry meetingMemoryEntry) meetingMemoryEntry {
	if isPromptBodyCapExemptKind(entry.Kind) {
		return entry
	}
	size := len(entry.Text)
	if size <= maxPromptBodyBytes {
		return entry
	}
	metadata := make(map[string]string, len(entry.Metadata)+1)
	for key, value := range entry.Metadata {
		metadata[key] = value
	}
	metadata[promptBodyOmittedMetadataKey] = strconv.Itoa(size)
	entry.Metadata = metadata
	if strings.Contains(entry.Text, ";base64,") {
		entry.Text = fmt.Sprintf("[artifact id=%s — %d bytes omitted]", entry.ID, size)
		return entry
	}
	cut := maxPromptBodyBytes
	for cut > 0 && !utf8.RuneStart(entry.Text[cut]) {
		cut--
	}
	entry.Text = strings.TrimSpace(entry.Text[:cut]) +
		fmt.Sprintf("\n[truncated — full entry id=%s — %d bytes omitted]", entry.ID, size)
	return entry
}

// visibleEntriesLocked returns store.entries minus the entries that must never
// reach the client memory timeline or the snapshot-fed model-context lanes:
// quarantined/expired material (forgotten), slop_pass cursor/audit stubs
// (pure bookkeeping — visibleMeetingMemoryEntries filters the other UI-state
// kinds by name but predates slop_pass, so it is dropped here), and raw
// feedback signals (signals.go: distillation-only input that may quote private
// thread text — it must never ride memorySnapshotForClients broadcasts or the
// snapshot-fed worker prompts). Callers that must SEE quarantined entries (the
// quarantine tray, the classifier, expiry) read store.entries via
// entriesByRelevance/entriesOfKind, not snapshot — future signal distillers
// read the same way.
//
// Bodies are prompt-capped here (stripOversizeBody), so EVERY snapshot lane —
// snapshot, snapshotForMeeting, memorySnapshotForClients, the archive embeds,
// grill/recap/mission context builders — inherits the cap. Callers that need
// FULL bodies (the artifact library/render/share/PDF path, thread records,
// the blob sweep) read via entriesOfKind, the same convention quarantine
// readers already follow; search() keeps matching against full bodies because
// it scans store.entries directly.
func (store *meetingMemoryStore) visibleEntriesLocked() []meetingMemoryEntry {
	visible := make([]meetingMemoryEntry, 0, len(store.entries))
	for _, entry := range store.entries {
		if entry.Kind == meetingMemoryKindSlopPass || entry.Kind == meetingMemoryKindSignal || memoryEntryHiddenFromRecall(entry) {
			continue
		}
		visible = append(visible, stripOversizeBody(entry))
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

// transcriptCoverage is the deterministic read of how continuously ONE
// meeting's raw transcript was captured: when the first/last captured line
// landed, how many there were, and the largest silent stretch between two
// consecutive lines. Every field derives from stored kind=transcript entries,
// so the model can never fabricate it. Zero values (Count == 0) mean nothing
// was captured for the meeting.
//
// This is THE shared coverage primitive: the meeting-digest producer stamps a
// coverage label from it (meetingCoverageLabel), the recap prefix reads it, and
// a future "recording degraded" badge is expected to read it too — keep the
// signature stable and side-effect free.
type transcriptCoverage struct {
	FirstAt        time.Time     // earliest captured transcript line
	LastAt         time.Time     // latest captured transcript line
	Count          int           // number of captured transcript lines
	MaxInternalGap time.Duration // largest gap between two consecutive lines
}

// transcriptCoverageForMeeting computes the transcriptCoverage read for a
// meeting id over the store's kind=transcript entries. It scans raw entries
// (not visibleEntriesLocked) on purpose: coverage is about what the recorder
// captured, independent of recall visibility.
func (store *meetingMemoryStore) transcriptCoverageForMeeting(meetingID string) transcriptCoverage {
	var coverage transcriptCoverage
	if store == nil {
		return coverage
	}
	meetingID = strings.TrimSpace(meetingID)
	if meetingID == "" {
		return coverage
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	times := make([]time.Time, 0, 64)
	for _, entry := range store.entries {
		if entry.Kind != meetingMemoryKindTranscript {
			continue
		}
		if strings.TrimSpace(entry.Metadata["meetingId"]) != meetingID {
			continue
		}
		times = append(times, entry.CreatedAt)
	}
	if len(times) == 0 {
		return coverage
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	coverage.Count = len(times)
	coverage.FirstAt = times[0]
	coverage.LastAt = times[len(times)-1]
	for index := 1; index < len(times); index++ {
		if gap := times[index].Sub(times[index-1]); gap > coverage.MaxInternalGap {
			coverage.MaxInternalGap = gap
		}
	}
	return coverage
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

// --- Digest tiers (Track-2 Wave 1): store primitives for the rolling rollup
// hierarchy brain (T1) → meeting_digest (T2) → day_digest (T3) →
// company_digest (T4). This wave is pure store plumbing — no model calls; the
// producers land in Wave 2 and the recall wiring in Waves 4/5.

const (
	// digestKeyMetadataKey scopes a digest: the meetingId for meeting_digest,
	// the dayBucket key for day_digest, companyDigestKey for company_digest.
	// upsertDigest ALWAYS stamps it (digestsInRange/latestDigestPerMeeting
	// silently match nothing on a missing key, so stamping is load-bearing).
	digestKeyMetadataKey = "digestKey"
	// digestCurrentMetadataKey is "true" on the single live digest per
	// (kind, digestKey) and "false" once superseded. Written only by
	// upsertDigest under the store mutex.
	digestCurrentMetadataKey = "current"
	digestCurrentTrue        = "true"
	digestCurrentFalse       = "false"
	// digestSupersededByMetadataKey names the replacing entry id on a
	// superseded digest, for audit/drill-down.
	digestSupersededByMetadataKey = "supersededBy"
	// digestDayMetadataKey holds the local-calendar-day bucket ("2006-01-02"
	// in meetingTimeLocation) a digest belongs to. Producers stamp it via
	// dayBucket on entry CreatedAt so a marathon meeting files per-day.
	digestDayMetadataKey = "day"
	// digestSpanStartMetadataKey/digestSpanEndMetadataKey bound the window a
	// meeting_digest covers (RFC3339); digestsInRange overlap-matches on them.
	digestSpanStartMetadataKey = "spanStart"
	digestSpanEndMetadataKey   = "spanEnd"
	// digestSittingStartedAtMetadataKey/digestSittingEndedAtMetadataKey stamp the
	// room-occupancy sitting bounds (the meetings-directory StartedAt/EndedAt)
	// onto a meeting_digest so a reader can compare the CAPTURED span to the
	// sitting it belongs to (kanban-card-107). NEVER a real-world calendar time.
	digestSittingStartedAtMetadataKey = "sittingStartedAt"
	digestSittingEndedAtMetadataKey   = "sittingEndedAt"
	// digestCoverageMetadataKey holds the Go-computed coverage classification
	// (one of the coverageLabel* values). Server-authored — the model never sees
	// or writes it, so it can never inflate a partial capture to "full".
	digestCoverageMetadataKey = "coverage"
	// externalMayPredateCaptureMetadataKey flags a listen-only sitting whose real
	// external meeting may have already been underway before Bonfire began
	// capturing — the honest caveat for a guest/listen-only digest.
	externalMayPredateCaptureMetadataKey = "externalMayPredateCapture"
	// companyDigestKey is the fixed digestKey of the latest-only company fold.
	companyDigestKey = "company"
	// dayBucketLayout is the canonical day-key format shared by dayBucket,
	// the day-digest producer, and digestsInRange.
	dayBucketLayout = "2006-01-02"
)

// Coverage classification (kanban-card-107): how completely a captured
// meeting_digest window covers the room-occupancy sitting it belongs to.
// Server-computed in Go so the model can never overstate visibility.
const (
	coverageLabelFull             = "full"               // covers the sitting start and has no large gap
	coverageLabelPartialLateStart = "partial_late_start" // capture began well after the sitting started
	coverageLabelPartialGaps      = "partial_gaps"       // a long stretch mid-sitting with no captured transcript (quiet OR a capture gap)
	coverageLabelUnknown          = "unknown"            // legacy synthetic key, no directory record, or no captured span

	// coverageStartTolerance: a digest whose captured window opens within this
	// of the sitting start still counts as covering the start — it absorbs the
	// first-brain-batch latency between admission and the first summarized line.
	coverageStartTolerance = 2 * time.Minute
	// coverageGapThreshold: the largest stretch between two consecutive captured
	// transcript lines tolerated before coverage reads as gapped. Transcripts land
	// only on speech or room-chat, so a gap this long means EITHER a genuinely
	// quiet stretch OR a capture failure — the two are indistinguishable from the
	// timeline alone. partial_gaps is therefore a conservative flag ("something
	// here may be missing"), never proof of an outage, and every reader phrases it
	// that way. Kept generously wide (10m) so an ordinary lull — a demo, a silent
	// read-through, a side conversation off-mic — does not get flagged.
	coverageGapThreshold = 10 * time.Minute
)

// isLegacyMeetingKey reports the synthetic per-day digest keys minted for the
// pre-scoping null-meetingId history (digestKeyForBrain). Those have no
// directory record, so their coverage is always unknown.
func isLegacyMeetingKey(key string) bool {
	return strings.HasPrefix(strings.TrimSpace(key), "meeting-legacy-")
}

// meetingCoverageLabel classifies how completely a captured digest window
// covers its meeting's room-occupancy sitting, computed entirely in Go.
// resolvable is false for legacy synthetic keys and meetings with no directory
// record (or an unparseable start); those — and any meeting with no captured
// span — are always coverageLabelUnknown, never a fabricated "full". Late start
// is reported before gaps: a missing opening is the more fundamental hole.
func meetingCoverageLabel(resolvable bool, sittingStart, spanStart time.Time, maxInternalGap time.Duration) string {
	if !resolvable || sittingStart.IsZero() || spanStart.IsZero() {
		return coverageLabelUnknown
	}
	if spanStart.Sub(sittingStart) > coverageStartTolerance {
		return coverageLabelPartialLateStart
	}
	if maxInternalGap > coverageGapThreshold {
		return coverageLabelPartialGaps
	}
	return coverageLabelFull
}

// meetingCoverageSummary is the read-side coverage view for one meeting shared
// by the cross-meeting briefing, get_meeting_detail, and the digest context
// headers. Label is one of the coverageLabel* values; SpanStart/SpanEnd are the
// captured window (zero when no digest span is stamped).
type meetingCoverageSummary struct {
	Label      string
	SpanStart  time.Time
	SpanEnd    time.Time
	ListenOnly bool
}

// partial reports a coverage summary the reader should caveat (partial or
// unknown, or listen-only) — anything but a clean, fully-covered capture.
func (summary meetingCoverageSummary) partial() bool {
	return summary.Label != coverageLabelFull || summary.ListenOnly
}

// parseDigestSpanMetadata reads a digest's stamped [spanStart, spanEnd] window,
// reporting ok=false when either bound is absent/unparseable (a legacy digest).
func parseDigestSpanMetadata(entry meetingMemoryEntry) (time.Time, time.Time, bool) {
	start, startErr := time.Parse(time.RFC3339, strings.TrimSpace(entry.Metadata[digestSpanStartMetadataKey]))
	end, endErr := time.Parse(time.RFC3339, strings.TrimSpace(entry.Metadata[digestSpanEndMetadataKey]))
	if startErr != nil || endErr != nil {
		return time.Time{}, time.Time{}, false
	}
	return start, end, true
}

// meetingCoverageFromDigest reads the coverage summary a producer already
// stamped onto a meeting_digest entry (the sub-wave-1 stamps). A legacy digest
// missing the coverage stamp degrades to coverageLabelUnknown — never a
// fabricated read.
func meetingCoverageFromDigest(entry meetingMemoryEntry) meetingCoverageSummary {
	summary := meetingCoverageSummary{
		Label:      strings.TrimSpace(entry.Metadata[digestCoverageMetadataKey]),
		ListenOnly: strings.EqualFold(strings.TrimSpace(entry.Metadata[listenOnlyMetadataKey]), "true"),
	}
	if summary.Label == "" {
		summary.Label = coverageLabelUnknown
	}
	if start, end, ok := parseDigestSpanMetadata(entry); ok {
		summary.SpanStart, summary.SpanEnd = start, end
	}
	return summary
}

// isMeetingDigestKind reports the three digest tiers. They are recall-eligible
// knowledge (never UI-state), exempt from the prompt-body cap, and hidden from
// recall once superseded (memoryEntryHiddenFromRecall).
func isMeetingDigestKind(kind string) bool {
	switch kind {
	case meetingMemoryKindMeetingDigest, meetingMemoryKindDayDigest, meetingMemoryKindCompanyDigest:
		return true
	}
	return false
}

// isAmbientBookkeepingMemoryKind reports the cross-meeting ambient kinds the
// boot meeting-resume scan must skip: they describe PAST meetings/days (or no
// meeting at all — the mint-free appendAmbientEntry/appendLedgerEvents lanes),
// so one of them as the newest JSONL line must neither clear nor redirect the
// in-flight meeting id across a restart.
func isAmbientBookkeepingMemoryKind(kind string) bool {
	switch kind {
	case meetingMemoryKindReflection, meetingMemoryKindDayDigestPass, meetingMemoryKindLedgerEvent, meetingMemoryKindLedgerPass:
		return true
	}
	return isMeetingDigestKind(kind)
}

// dayBucket returns the canonical calendar-day key for t in the pinned meeting
// timezone (MEETING_TIME_ZONE, default America/Los_Angeles). Both the digest
// producers and digestsInRange must bucket through this one helper so a
// late-night entry files to — and is queried under — the same local day.
func dayBucket(t time.Time) string {
	return t.In(meetingTimeLocation()).Format(dayBucketLayout)
}

func digestEntryKey(entry meetingMemoryEntry) string {
	return strings.TrimSpace(entry.Metadata[digestKeyMetadataKey])
}

// digestEntryCurrent reports whether a digest entry is the live one for its
// (kind, digestKey). Only upsertDigest writes digests, and it always stamps
// current="true", so an explicit match is the complete check.
func digestEntryCurrent(entry meetingMemoryEntry) bool {
	return strings.TrimSpace(entry.Metadata[digestCurrentMetadataKey]) == digestCurrentTrue
}

// upsertDigest writes the new current digest for (kind, key) and supersedes
// the prior one in place: every still-current digest for that scope is marked
// relevanceArchived + current="false" + supersededBy — which drops it from
// recall via memoryEntryHiddenFromRecall (snapshot lanes, search, context)
// while the raw JSONL keeps it for audit. Mark-stale + append run under ONE
// critical section so a concurrent double-run can never leave two current
// digests, and both land in a single atomic temp+rename rewrite when a prior
// digest existed (plain O_APPEND on first write). Metadata should carry the
// producer's day/span/cursor stamps; digestKey and current are stamped here.
// No meetingId is auto-stamped: a digest usually describes a PAST meeting or
// day, and stamping the live meeting id would leak it into an unrelated
// snapshotForMeeting (the meeting-digest producer passes meetingId == key).
func (store *meetingMemoryStore) upsertDigest(kind string, key string, text string, metadata map[string]string) (meetingMemoryEntry, error) {
	if store == nil {
		return meetingMemoryEntry{}, fmt.Errorf("memory store is unavailable")
	}
	kind = strings.TrimSpace(kind)
	if !isMeetingDigestKind(kind) {
		return meetingMemoryEntry{}, fmt.Errorf("upsertDigest: %q is not a digest kind", kind)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return meetingMemoryEntry{}, fmt.Errorf("upsertDigest: digest key is required")
	}
	text = normalizeMemoryEntryText(kind, text)
	if text == "" {
		return meetingMemoryEntry{}, fmt.Errorf("upsertDigest: digest text is required")
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	// Mint a unique id under the lock so the seen-map guard is race-free.
	id := fmt.Sprintf("%s-%s-%d", kind, key, time.Now().UnixNano())
	for suffix := 1; ; suffix++ {
		if _, ok := store.seen[id]; !ok {
			break
		}
		id = fmt.Sprintf("%s-%s-%d-%d", kind, key, time.Now().UnixNano(), suffix)
	}

	stamped := make(map[string]string, len(metadata)+2)
	for metaKey, metaValue := range metadata {
		metaKey = strings.TrimSpace(metaKey)
		if metaKey == "" {
			continue
		}
		stamped[metaKey] = strings.TrimSpace(metaValue)
	}
	stamped[digestKeyMetadataKey] = key
	stamped[digestCurrentMetadataKey] = digestCurrentTrue

	entry := meetingMemoryEntry{
		ID:        id,
		Kind:      kind,
		Text:      text,
		CreatedAt: time.Now().UTC(),
		Metadata:  stamped,
	}

	// Supersede in place. The loop archives EVERY current match (not just the
	// newest) so a crash that once left two current digests self-heals on the
	// next upsert.
	type supersededDigest struct {
		index int
		prior meetingMemoryEntry
	}
	superseded := make([]supersededDigest, 0, 1)
	for index := range store.entries {
		prior := store.entries[index]
		if prior.Kind != kind || digestEntryKey(prior) != key || !digestEntryCurrent(prior) {
			continue
		}
		updated := cloneMemoryEntry(prior)
		if updated.Metadata == nil {
			updated.Metadata = map[string]string{}
		}
		updated.Metadata[relevanceMetadataKey] = relevanceArchived
		updated.Metadata[digestCurrentMetadataKey] = digestCurrentFalse
		updated.Metadata[digestSupersededByMetadataKey] = id
		superseded = append(superseded, supersededDigest{index: index, prior: prior})
		store.entries[index] = updated
	}

	store.entries = append(store.entries, entry)
	store.seen[id] = struct{}{}

	var err error
	if len(superseded) == 0 {
		err = store.appendEntryLineLocked(entry)
	} else {
		err = store.rewriteLocked(true)
	}
	if err != nil {
		// roll back the in-RAM mutation so RAM matches the file.
		store.entries = store.entries[:len(store.entries)-1]
		delete(store.seen, id)
		for _, stale := range superseded {
			store.entries[stale.index] = stale.prior
		}
		return meetingMemoryEntry{}, err
	}

	return cloneMemoryEntry(entry), nil
}

// appendEntryLineLocked O_APPEND-writes one already-validated entry line.
// Caller holds store.mu and owns the entries/seen bookkeeping (including
// rollback on error).
func (store *meetingMemoryStore) appendEntryLineLocked(entry meetingMemoryEntry) error {
	file, err := os.OpenFile(store.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open memory file: %w", err)
	}
	defer file.Close()

	raw, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode memory entry: %w", err)
	}
	if _, err := file.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("write memory entry: %w", err)
	}

	return nil
}

// appendAmbientEntry appends a cross-meeting ambient entry WITHOUT the
// meetingId stamp appendEntryForMeeting applies: reflections and day-digest
// pass artifacts describe PAST days, so they must neither adopt the live
// meeting id (leaking into an unrelated snapshotForMeeting) nor lazily mint a
// fresh one at idle (the upsertDigest precedent). Same seen-map dedupe and
// RAM-matches-file rollback contract as the other append paths.
func (store *meetingMemoryStore) appendAmbientEntry(kind string, id string, text string, metadata map[string]string) (meetingMemoryEntry, bool, error) {
	if store == nil {
		return meetingMemoryEntry{}, false, fmt.Errorf("memory store is unavailable")
	}
	kind = strings.TrimSpace(kind)
	text = normalizeMemoryEntryText(kind, text)
	if kind == "" || text == "" {
		return meetingMemoryEntry{}, false, nil
	}
	if strings.TrimSpace(id) == "" {
		id = fmt.Sprintf("%s-%d", kind, time.Now().UnixNano())
	}

	stamped := make(map[string]string, len(metadata))
	for key, value := range metadata {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		stamped[key] = strings.TrimSpace(value)
	}
	entry := meetingMemoryEntry{
		ID:        strings.TrimSpace(id),
		Kind:      kind,
		Text:      text,
		CreatedAt: time.Now().UTC(),
		Metadata:  stamped,
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	if _, ok := store.seen[entry.ID]; ok {
		return entry, false, nil
	}
	if err := store.appendEntryLineLocked(entry); err != nil {
		return meetingMemoryEntry{}, false, err
	}
	store.entries = append(store.entries, entry)
	store.seen[entry.ID] = struct{}{}

	return cloneMemoryEntry(entry), true, nil
}

// hasReflectionForDay reports whether an end-of-day reflection was already
// written for the local calendar day (dayBucket key) — the at-most-one-per-day
// guard the day-digest tick checks before spending a reflection model call.
func (store *meetingMemoryStore) hasReflectionForDay(day string) bool {
	day = strings.TrimSpace(day)
	if store == nil || day == "" {
		return false
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	for index := len(store.entries) - 1; index >= 0; index-- {
		entry := store.entries[index]
		if entry.Kind != meetingMemoryKindReflection {
			continue
		}
		if strings.TrimSpace(entry.Metadata[digestDayMetadataKey]) == day {
			return true
		}
	}

	return false
}

// latestDigestPerMeeting returns the current meeting_digest per meetingId
// (digestKey), cloned. Exactly one current digest per key is the upsertDigest
// invariant; newest-CreatedAt wins here as belt-and-suspenders for the crash
// window that could briefly leave two.
func (store *meetingMemoryStore) latestDigestPerMeeting() map[string]meetingMemoryEntry {
	if store == nil {
		return nil
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	latest := make(map[string]meetingMemoryEntry)
	for _, entry := range store.entries {
		if entry.Kind != meetingMemoryKindMeetingDigest {
			continue
		}
		if !digestEntryCurrent(entry) || memoryEntryHiddenFromRecall(entry) {
			continue
		}
		key := digestEntryKey(entry)
		if key == "" {
			continue
		}
		if prior, ok := latest[key]; ok && prior.CreatedAt.After(entry.CreatedAt) {
			continue
		}
		latest[key] = entry
	}
	for key, entry := range latest {
		latest[key] = cloneMemoryEntry(entry)
	}

	return latest
}

// digestSpan resolves the window a digest covers, for range matching:
//   - day_digest: its local calendar day [00:00, 24:00) from the day stamp;
//   - meeting_digest: [spanStart, spanEnd] when stamped, else its day stamp,
//     else the point CreatedAt.
//
// The reported end is inclusive (a day ends at 23:59:59.999…), so two
// adjacent days never both match a boundary instant.
func digestSpan(entry meetingMemoryEntry, location *time.Location) (time.Time, time.Time) {
	switch entry.Kind {
	case meetingMemoryKindDayDigest:
		day := strings.TrimSpace(entry.Metadata[digestDayMetadataKey])
		if day == "" {
			// the digestKey IS the day bucket for a day_digest.
			day = digestEntryKey(entry)
		}
		if start, err := time.ParseInLocation(dayBucketLayout, day, location); err == nil {
			return start, start.Add(24*time.Hour - time.Nanosecond)
		}
	case meetingMemoryKindMeetingDigest:
		start, startErr := time.Parse(time.RFC3339, strings.TrimSpace(entry.Metadata[digestSpanStartMetadataKey]))
		end, endErr := time.Parse(time.RFC3339, strings.TrimSpace(entry.Metadata[digestSpanEndMetadataKey]))
		if startErr == nil && endErr == nil {
			if end.Before(start) {
				start, end = end, start
			}
			return start, end
		}
		if day := strings.TrimSpace(entry.Metadata[digestDayMetadataKey]); day != "" {
			if dayStart, err := time.ParseInLocation(dayBucketLayout, day, location); err == nil {
				return dayStart, dayStart.Add(24*time.Hour - time.Nanosecond)
			}
		}
	}
	return entry.CreatedAt, entry.CreatedAt
}

// digestsInRange returns the current day_digests whose local calendar day
// falls in [start, end] plus the current meeting_digests whose covered span
// overlaps it, oldest-first by covered window. Both bounds are inclusive.
// Superseded digests never match (memoryEntryHiddenFromRecall).
func (store *meetingMemoryStore) digestsInRange(start time.Time, end time.Time) []meetingMemoryEntry {
	if store == nil || start.IsZero() || end.IsZero() || end.Before(start) {
		return nil
	}
	location := meetingTimeLocation()

	store.mu.Lock()
	defer store.mu.Unlock()

	type rangedDigest struct {
		entry meetingMemoryEntry
		start time.Time
	}
	matched := make([]rangedDigest, 0, 16)
	for _, entry := range store.entries {
		switch entry.Kind {
		case meetingMemoryKindDayDigest, meetingMemoryKindMeetingDigest:
		default:
			continue
		}
		if !digestEntryCurrent(entry) || memoryEntryHiddenFromRecall(entry) {
			continue
		}
		spanStart, spanEnd := digestSpan(entry, location)
		// end is exclusive (relativeQueryTimeRange semantics): a digest whose
		// window STARTS exactly at end (e.g. today's day_digest on a
		// "yesterday" query) must not ride into the range.
		if !spanStart.Before(end) || spanEnd.Before(start) {
			continue
		}
		matched = append(matched, rangedDigest{entry: entry, start: spanStart})
	}

	sort.SliceStable(matched, func(i, j int) bool {
		if !matched[i].start.Equal(matched[j].start) {
			return matched[i].start.Before(matched[j].start)
		}
		// same window start: the day rollup leads its meetings.
		return matched[i].entry.Kind == meetingMemoryKindDayDigest && matched[j].entry.Kind != meetingMemoryKindDayDigest
	})

	digests := make([]meetingMemoryEntry, 0, len(matched))
	for _, ranged := range matched {
		digests = append(digests, cloneMemoryEntry(ranged.entry))
	}

	return digests
}

// latestCompanyDigest returns the newest current company_digest (mirrors
// latestMissionInsight, but store-level and supersede-aware).
func (store *meetingMemoryStore) latestCompanyDigest() (meetingMemoryEntry, bool) {
	if store == nil {
		return meetingMemoryEntry{}, false
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	for index := len(store.entries) - 1; index >= 0; index-- {
		entry := store.entries[index]
		if entry.Kind != meetingMemoryKindCompanyDigest {
			continue
		}
		if !digestEntryCurrent(entry) || memoryEntryHiddenFromRecall(entry) {
			continue
		}
		return cloneMemoryEntry(entry), true
	}

	return meetingMemoryEntry{}, false
}

// transcriptWindowAround resolves a digest anchor to its verbatim exchange:
// the transcript entries of the anchor's meeting ordered by CreatedAt, radius
// entries either side of the anchor, never crossing a meetingId boundary.
// A non-transcript (or hidden) anchor centers on its CreatedAt position among
// the meeting's transcripts. Hidden (quarantined/expired) transcripts never
// resurface, and bodies ride the prompt cap since this feeds drill-down
// prompts (real transcripts are far under the cap).
func (store *meetingMemoryStore) transcriptWindowAround(entryID string, radius int) []meetingMemoryEntry {
	entryID = strings.TrimSpace(entryID)
	if store == nil || entryID == "" || radius < 0 {
		return nil
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	var anchor meetingMemoryEntry
	found := false
	for index := len(store.entries) - 1; index >= 0; index-- {
		if store.entries[index].ID == entryID {
			anchor = store.entries[index]
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	meetingID := strings.TrimSpace(anchor.Metadata["meetingId"])

	transcripts := make([]meetingMemoryEntry, 0, 64)
	for _, entry := range store.entries {
		if entry.Kind != meetingMemoryKindTranscript {
			continue
		}
		if strings.TrimSpace(entry.Metadata["meetingId"]) != meetingID {
			continue
		}
		if memoryEntryHiddenFromRecall(entry) {
			continue
		}
		transcripts = append(transcripts, entry)
	}
	if len(transcripts) == 0 {
		return nil
	}
	sort.SliceStable(transcripts, func(i, j int) bool {
		return transcripts[i].CreatedAt.Before(transcripts[j].CreatedAt)
	})

	anchorIndex := -1
	for index, entry := range transcripts {
		if entry.ID == anchor.ID {
			anchorIndex = index
			break
		}
	}
	if anchorIndex < 0 {
		// non-transcript anchor: center on where it falls in time.
		anchorIndex = len(transcripts) - 1
		for index, entry := range transcripts {
			if !entry.CreatedAt.Before(anchor.CreatedAt) {
				anchorIndex = index
				break
			}
		}
	}

	low := anchorIndex - radius
	if low < 0 {
		low = 0
	}
	high := anchorIndex + radius
	if high > len(transcripts)-1 {
		high = len(transcripts) - 1
	}

	window := cloneMemoryEntries(transcripts[low : high+1])
	for index := range window {
		window[index] = stripOversizeBody(window[index])
	}

	return window
}

// deleteEntryByID hard-deletes one entry (any kind) and rewrites the log. Two
// callers only: the expiry job's terminal step (always paired with a slop_pass
// audit stub so the fact of deletion survives) and a user deleting their own
// misplaced room-chat message. Reports whether an entry was removed.
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
	if err := store.rewriteLocked(false); err != nil {
		// restore the in-memory slice so a failed rewrite is not silently lossy.
		store.entries = append(store.entries[:index], append([]meetingMemoryEntry{removed}, store.entries[index:]...)...)
		return meetingMemoryEntry{}, false, err
	}
	delete(store.seen, id)

	return removed, true, nil
}

// isUIStateMemoryKind reports the entry kinds that are workspace/UI state
// rather than meeting knowledge — chat threads, codex proposals, mission
// insights, decision-pass cursors, venture-package records, and raw feedback
// signals (signals.go: distillation-only input, never recall material) never
// enter Scout's search results or model context. Kind "decision" is
// deliberately absent: decision statements ARE knowledge and must ground
// Scout's answers.
func isUIStateMemoryKind(kind string) bool {
	return kind == meetingMemoryKindScoutChat || kind == meetingMemoryKindCodexProposal || kind == meetingMemoryKindMissionInsight || kind == meetingMemoryKindDecisionPass || kind == meetingMemoryKindPackage || kind == meetingMemoryKindDealRoom || kind == meetingMemoryKindSlopPass || kind == meetingMemoryKindSignal || kind == meetingMemoryKindDayDigestPass || kind == meetingMemoryKindLedgerEvent || kind == meetingMemoryKindLedgerPass
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
		parts = append(parts, "- "+memoryAnswerExcerpt(match.Entry))
	}

	return strings.Join(parts, "\n")
}

// memoryAnswerExcerpt renders one recall match as a compact, single-line
// excerpt. A recall answer summarizes memory; it must NEVER inline a full
// artifact body. Full-text search once surfaced a stale 2.6MB packaging deck
// (HTML + base64 imagery) for a "Samsung TV audience" query; dumping its body
// verbatim here produced a 2.65M-char answer that, fed back as an
// answer_memory_question tool result, pushed the Fable research orchestrator to
// ~2.55M tokens > the 1M model-context ceiling (400, every Samsung run). Titling
// the excerpt keeps the recall useful without the body.
func memoryAnswerExcerpt(entry meetingMemoryEntry) string {
	excerpt := compactAssistantLine(entry.Text)
	// The title is model-supplied and unbounded; cap it too so the excerpt's size
	// guarantee holds regardless of which field carries the bulk.
	if title := strings.TrimSpace(entry.Metadata["title"]); title != "" {
		return compactAssistantLine(title) + " — " + excerpt
	}
	return excerpt
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
		// Guest display names ("Guest Sam") pass through verbatim — the
		// server-enforced prefix is exactly what the record must carry so a
		// guest can never be attributed as a roster member (multi-room §5.2).
		if canonical := canonicalRoomParticipantName(part); canonical != "" {
			normalizedParts = append(normalizedParts, canonical)
		}
	}
	if len(normalizedParts) > 0 {
		return strings.Join(uniqueStrings(normalizedParts), " + ")
	}

	if canonical := canonicalRoomParticipantName(speaker); canonical != "" {
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
	if kind != meetingMemoryKindBrain && kind != meetingMemoryKindBoardUpdate && kind != meetingMemoryKindOSArtifact && kind != meetingMemoryKindScoutChat && kind != meetingMemoryKindMissionInsight && kind != meetingMemoryKindPackage && kind != meetingMemoryKindDealRoom && kind != meetingMemoryKindNarrative && kind != meetingMemoryKindReflection && kind != meetingMemoryKindLedgerEvent && !isMeetingDigestKind(kind) {
		// digest kinds and ledger events take the structure-preserving branch
		// below: their bodies are strict JSON (like mission_insight) and the
		// whitespace collapse would mutate content inside JSON string values;
		// reflection bodies are sectioned markdown (like brain write-ups);
		// narrative dossiers (axx/main) likewise keep their structure.
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
