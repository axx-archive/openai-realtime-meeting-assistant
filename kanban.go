package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const (
	defaultRealtimeModel      = "gpt-realtime-2"
	defaultReasoningEffort    = "high"
	defaultRealtimeVADType    = "server_vad"
	defaultVADEagerness       = "high"
	defaultRealtimeVoice      = "marin"
	defaultKanbanBoardPath    = "data/kanban-board.json"
	realtimeEventChannelLabel = "oai-events"
	realtimeInputTrackID      = "kanban-realtime:mixed-audio"
	realtimeInputStreamID     = "kanban-realtime-input"
	realtimeMixedAudioSinkKey = "kanban-realtime"
	scoutParticipantName      = "Scout"
	scoutWakePhraseFirstWord  = "hey"
	scoutWakePhraseSecondWord = "scout"
	scoutVoiceArmDuration     = 12 * time.Second
	// scoutVoiceRecentToolGrace lets a late-arriving wake transcript still claim
	// a tool result that completed moments earlier: the async ASR transcript
	// routinely lands after response.done.
	scoutVoiceRecentToolGrace = 6 * time.Second
)

var (
	realtimeCallsURL   = "https://api.openai.com/v1/realtime/calls"
	realtimeHTTPClient = &http.Client{Timeout: 30 * time.Second}
)

func durableTimestampID(prefix string, at time.Time) string {
	at = at.UTC()
	return fmt.Sprintf("%s-%s-%09d", strings.TrimSpace(prefix), at.Format("20060102-150405"), at.Nanosecond())
}

type kanbanStatus string

const (
	kanbanStatusBacklog    kanbanStatus = "Backlog"
	kanbanStatusInProgress kanbanStatus = "In Progress"
	kanbanStatusBlocked    kanbanStatus = "Blocked"
	kanbanStatusDone       kanbanStatus = "Done"
)

var kanbanStatuses = []kanbanStatus{
	kanbanStatusBacklog,
	kanbanStatusInProgress,
	kanbanStatusBlocked,
	kanbanStatusDone,
}

const maxKanbanKeyDates = 8

type kanbanKeyDate struct {
	Label string `json:"label"`
	Date  string `json:"date"`
}

type kanbanCard struct {
	ID       string          `json:"id"`
	Status   kanbanStatus    `json:"status"`
	Title    string          `json:"title"`
	Notes    string          `json:"notes"`
	Owner    string          `json:"owner,omitempty"`
	Tags     []string        `json:"tags"`
	DueDate  string          `json:"dueDate,omitempty"`
	KeyDates []kanbanKeyDate `json:"keyDates,omitempty"`
	// Draft marks a Scout-proposed card awaiting a human accept/dismiss
	// (D4). Human-created cards are never drafts. Boards persisted before
	// the field existed decode as non-drafts (zero value).
	Draft     bool   `json:"draft,omitempty"`
	DraftedAt string `json:"draftedAt,omitempty"`
}

type kanbanBoardState struct {
	Cards     []kanbanCard `json:"cards"`
	UpdatedAt string       `json:"updatedAt,omitempty"`
}

type participantMediaState struct {
	MicMuted      bool   `json:"micMuted"`
	CameraOff     bool   `json:"cameraOff"`
	ScreenSharing bool   `json:"screenSharing"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
}

type roomRecordingState struct {
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	UpdatedBy string `json:"updatedBy,omitempty"`
}

type meetingArchive struct {
	ID           string               `json:"id"`
	MeetingID    string               `json:"meetingId,omitempty"`
	ArchivedAt   time.Time            `json:"archivedAt"`
	ArchivedBy   string               `json:"archivedBy,omitempty"`
	Board        kanbanBoardState     `json:"board"`
	Memory       []meetingMemoryEntry `json:"memory"`
	Participants []string             `json:"participants,omitempty"`
	Notes        meetingNotes         `json:"notes"`
	Email        meetingEmailStatus   `json:"email"`
	// Meeting embeds the closed first-class meeting record (title, real
	// startedAt, participants union) so an archive references its meeting
	// self-containedly.
	Meeting *meetingRecord `json:"meeting,omitempty"`
}

type meetingArchiveResult struct {
	ID          string              `json:"id"`
	MeetingID   string              `json:"meetingId,omitempty"`
	ArchivedAt  string              `json:"archivedAt"`
	ArchivedBy  string              `json:"archivedBy,omitempty"`
	DownloadURL string              `json:"downloadUrl"`
	Summary     string              `json:"summary"`
	Notes       meetingNotes        `json:"notes"`
	Email       meetingEmailStatus  `json:"email"`
	Artifact    *meetingMemoryEntry `json:"artifact,omitempty"`
}

type kanbanRealtimeEvent struct {
	EventID    string `json:"event_id,omitempty"`
	Type       string `json:"type,omitempty"`
	Transcript string `json:"transcript,omitempty"`
	Text       string `json:"text,omitempty"`
	Delta      string `json:"delta,omitempty"`
	ItemID     string `json:"item_id,omitempty"`
	Name       string `json:"name,omitempty"`
	Arguments  string `json:"arguments,omitempty"`
	CallID     string `json:"call_id,omitempty"`
	Error      *struct {
		Code    string `json:"code,omitempty"`
		Message string `json:"message,omitempty"`
	} `json:"error,omitempty"`
	Item *kanbanRealtimeOutputItem `json:"item,omitempty"`
	// Usage rides transcription events
	// (conversation.item.input_audio_transcription.completed): the voice
	// peer's own STT billing, metered per segment (W0-5).
	Usage    *kanbanRealtimeUsage `json:"usage,omitempty"`
	Response *struct {
		Status        string `json:"status,omitempty"`
		StatusDetails *struct {
			Type   string `json:"type,omitempty"`
			Reason string `json:"reason,omitempty"`
		} `json:"status_details,omitempty"`
		Output []kanbanRealtimeOutputItem `json:"output,omitempty"`
		// Usage is the voice turn's wire-reported audio+text token splits
		// on response.done (W0-5 room-voice metering).
		Usage *kanbanRealtimeUsage `json:"usage,omitempty"`
	} `json:"response,omitempty"`
}

type kanbanRealtimeOutputItem struct {
	Type      string `json:"type,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	CallID    string `json:"call_id,omitempty"`
}

// kanbanRealtimeUsage decodes the usage object the Realtime API attaches to
// response.done (voice turns: per-modality token splits with cached shares)
// and to input_audio_transcription.completed (transcription billing, token-
// or duration-based). Absent fields decode to zero; unknown fields are
// ignored, so the decode is inert on events that carry no usage.
type kanbanRealtimeUsage struct {
	Type              string  `json:"type,omitempty"` // transcription events: "tokens" | "duration"
	Seconds           float64 `json:"seconds,omitempty"`
	TotalTokens       int64   `json:"total_tokens,omitempty"`
	InputTokens       int64   `json:"input_tokens,omitempty"`
	OutputTokens      int64   `json:"output_tokens,omitempty"`
	InputTokenDetails *struct {
		TextTokens          int64 `json:"text_tokens,omitempty"`
		AudioTokens         int64 `json:"audio_tokens,omitempty"`
		CachedTokens        int64 `json:"cached_tokens,omitempty"`
		CachedTokensDetails *struct {
			TextTokens  int64 `json:"text_tokens,omitempty"`
			AudioTokens int64 `json:"audio_tokens,omitempty"`
		} `json:"cached_tokens_details,omitempty"`
	} `json:"input_token_details,omitempty"`
	OutputTokenDetails *struct {
		TextTokens  int64 `json:"text_tokens,omitempty"`
		AudioTokens int64 `json:"audio_tokens,omitempty"`
	} `json:"output_token_details,omitempty"`
}

// realtimeUsageTokens maps a Realtime usage payload onto the ledger entry's
// token splits. Cached tokens are subtracted out of the per-modality totals so
// Input/AudioInput carry only the full-price share (the pricing table bills
// cached reads separately). Reports false when the payload carries no billable
// signal at all, so callers skip zero rows.
func realtimeUsageTokens(usage *kanbanRealtimeUsage, entry *llmUsageEntry) bool {
	if usage == nil {
		return false
	}
	textIn := usage.InputTokens
	var audioIn, cachedText, cachedAudio int64
	if details := usage.InputTokenDetails; details != nil {
		textIn, audioIn = details.TextTokens, details.AudioTokens
		if cached := details.CachedTokensDetails; cached != nil {
			cachedText, cachedAudio = cached.TextTokens, cached.AudioTokens
		} else if details.CachedTokens > 0 {
			// No per-modality cached split: attribute the cached share to
			// text. Cached text and cached audio bill identically on the
			// realtime family, so the cost estimate is unaffected.
			cachedText = min(details.CachedTokens, textIn)
		}
	}
	entry.InputTokens = max(textIn-cachedText, 0)
	entry.CachedInputTokens = cachedText
	entry.AudioInputTokens = max(audioIn-cachedAudio, 0)
	entry.CachedAudioInputTokens = cachedAudio
	if details := usage.OutputTokenDetails; details != nil {
		entry.OutputTokens = details.TextTokens
		entry.AudioOutputTokens = details.AudioTokens
	} else {
		entry.OutputTokens = usage.OutputTokens
	}
	entry.AudioSeconds = usage.Seconds
	return entry.InputTokens > 0 || entry.CachedInputTokens > 0 ||
		entry.AudioInputTokens > 0 || entry.CachedAudioInputTokens > 0 ||
		entry.OutputTokens > 0 || entry.AudioOutputTokens > 0 ||
		entry.AudioSeconds > 0
}

type kanbanBoardApp struct {
	mu               sync.Mutex
	cards            []kanbanCard
	nextCreatedIndex int
	updatedAt        time.Time
	handledCalls     map[string]struct{}
	memory           *meetingMemoryStore
	// roomLive is the per-room live plane (multi-room W3, room_live.go): each
	// room's presence maps (participant name -> liveness stamp, endpoint
	// sessions, media state — the laptop+phone endpoint contract unchanged),
	// recording toggle, speaker-attribution state, and lazy mixer/lane. The
	// office room is seeded at construction; a room's state is created lazily
	// on first touch. Guarded by mu, exactly like the fields it replaced.
	roomLive        map[string]*roomLiveState
	lastDeletedCard *kanbanCard
	apiKey          string
	restarting      bool
	assistantStatus string

	model                    string
	pc                       *webrtc.PeerConnection
	events                   *webrtc.DataChannel
	inputTrack               *webrtc.TrackLocalStaticSample
	inputEnc                 *opusEncoder
	connected                bool
	forwardedAudioNotice     bool
	realtimeResponseActive   bool
	voiceControlActive       bool
	voiceControlUpdatedAt    time.Time
	voiceControlUpdatedBy    string
	scoutVoiceArmedAt        time.Time
	scoutVoiceArmedUntil     time.Time
	scoutSpokenResponse      bool
	scoutSpokenResponseSent  bool
	scoutLastToolResultAt    time.Time
	scoutLastToolResultName  string
	scoutToolCallsInFlight   int
	transcriptLane           *meetingTranscriptionLane
	proactiveReconnectCancel chan struct{}
	// realtimeStarting serializes lazy admission-time peer creation (W4 §4.4);
	// realtimeMediaGen captures the office roomLive mediaGen the current peer
	// was created under so restart loops abort after an idle teardown bumps it
	// (the teardown-vs-restart fence).
	realtimeStarting bool
	realtimeMediaGen uint64
	agentCancels     map[string]chan struct{}
	agentDones       map[string]chan struct{}
	agentBaselineIDs map[string]string
	agentRunLocks    map[string]*sync.Mutex
	// agentNudges holds the A3 per-agent buffered(1) wake channels (agent_runner.go):
	// a transcript append, or the brain-append cascade, signals one so the runner
	// re-checks its window immediately instead of waiting for the safety-floor tick.
	agentNudges map[string]chan struct{}
	// agentPendingRooms records WHICH rooms nudged each agent since its last
	// wake (multi-room W4 §7.4): the wake channel stays buffered(1) so bursts
	// coalesce, and the pending set guarantees no room's nudge is lost.
	agentPendingRooms map[string]map[string]struct{}
	// agentFailures holds the A8 per-agent same-window retry state (attempts +
	// backoff, agent_runner.go) so a permanently-failing window backs off and is
	// finally skipped instead of re-sent every tick forever.
	agentFailures   map[string]*ambientAgentFailure
	chatThreadLocks map[string]*sync.Mutex
	// agentThreadRunLocks serializes follow-up validate+mark-running per
	// artifact (agent_thread_followup.go); model calls stay outside.
	agentThreadRunLocks map[string]*sync.Mutex
	notifications       []notificationRecord
	meetings            *meetingStore
	// Grill session state ("Scout, grill us") — all under mu. While
	// grillActive, sessionInstructions() swaps to the persona instruction set
	// and realtimeToolChoice() returns "auto" so the persona can speak.
	grillActive               bool
	grillTopic                string
	grillPersona              string
	grillStartedBy            string
	grillStartedAt            time.Time
	grillBaselineTranscriptID string
	grillTimer                *time.Timer
	// missionIntelRefreshAt is the last accepted on-demand mission refresh;
	// the refresh endpoint allows one attempt per cooldown window.
	missionIntelRefreshAt time.Time
	// proposalMu serializes codex-proposal confirm/dismiss transitions so a
	// double confirm can never launch two agent threads.
	proposalMu sync.Mutex
	// packageMu serializes ALL venture-package mutations (the proposal-lock
	// precedent): whole-record last-write-wins inside the lock.
	packageMu sync.Mutex
	// dealRoomMu serializes ALL Deal Room mutations (request/approve/reject/
	// revoke) the same way packageMu guards packages: whole-record
	// last-write-wins inside the lock, broadcasts only after it is released.
	dealRoomMu sync.Mutex
	// boardWorkerRetriedThroughID bounds the board worker's total-failure retry
	// (A2): when a pass changes nothing but errors on every op, the runner
	// declines to advance its cursor so the same brain-summary window is
	// re-attempted next pass — but only once per window boundary, tracked by the
	// through-brain id here, so a permanently-rejected op (a doctrine gate the
	// model keeps re-emitting) can never wedge the cursor behind a growing
	// backlog. Guarded by mu.
	boardWorkerRetriedThroughID string
	closeOnce                   sync.Once
}

var initialKanbanBoardCards = []kanbanCard{
	{
		ID:     "card-002",
		Status: kanbanStatusBacklog,
		Title:  "Add RTP Retransmission Buffer",
		Notes:  "Keep recent RTP packets available for NACK-driven retransmission without unbounded memory growth.",
		Owner:  "Tim",
		Tags:   []string{"webrtc", "rtp", "nack"},
	},
	{
		ID:     "card-003",
		Status: kanbanStatusBacklog,
		Title:  "Implement ICE Restart Handling",
		Notes:  "Support renegotiation paths that refresh ICE credentials and reconnect peers after network changes.",
		Owner:  "Tyler",
		Tags:   []string{"webrtc", "ice", "signaling"},
	},
	{
		ID:     "card-004",
		Status: kanbanStatusBacklog,
		Title:  "Harden DTLS/SRTP Cleanup",
		Notes:  "Ensure failed and closed peer connections release transports, tracks, and SRTP state promptly.",
		Owner:  "Erick",
		Tags:   []string{"webrtc", "dtls", "srtp"},
	},
	{
		ID:     "card-005",
		Status: kanbanStatusBacklog,
		Title:  "Add Simulcast Forwarding Controls",
		Notes:  "Choose forwarded RTP layers per subscriber so the server can adapt streams to bandwidth and viewport size.",
		Owner:  "Caitlyn",
		Tags:   []string{"webrtc", "simulcast", "bandwidth"},
	},
	{
		ID:     "card-001",
		Status: kanbanStatusBacklog,
		Title:  "Finish RTP HEVC Packetizer",
		Notes:  "Complete HEVC payload fragmentation, aggregation, and marker-bit handling for outbound RTP streams.",
		Owner:  "AJ",
		Tags:   []string{"webrtc", "rtp", "hevc"},
	},
}

func newKanbanBoardApp() *kanbanBoardApp {
	memory, err := newMeetingMemoryStore(meetingMemoryPath())
	if err != nil {
		log.Errorf("Meeting memory disabled: %v", err)
	}

	cards := normalizeKanbanCards(initialKanbanBoardCards)
	updatedAt := time.Now().UTC()
	loadedBoard := false
	boardPersistenceHealthy := true
	if board, ok, err := loadKanbanBoardState(kanbanBoardPath()); err != nil {
		log.Errorf("Kanban board persistence disabled: %v", err)
		boardPersistenceHealthy = false
	} else if ok {
		cards = cloneKanbanCards(board.Cards)
		if parsedUpdatedAt, err := time.Parse(time.RFC3339Nano, board.UpdatedAt); err == nil {
			updatedAt = parsedUpdatedAt.UTC()
		}
		loadedBoard = true
	}

	app := &kanbanBoardApp{
		cards:            cards,
		nextCreatedIndex: nextKanbanCardIndex(cards),
		updatedAt:        updatedAt,
		handledCalls:     map[string]struct{}{},
		memory:           memory,
		roomLive: map[string]*roomLiveState{
			officeRoomID: newRoomLiveState(officeRoomID, updatedAt),
		},
	}
	app.projectLegacyPackageOwnerMetadataAtBoot()
	if notifications, err := loadNotificationStoreState(notificationsPath()); err != nil {
		log.Errorf("Notification persistence disabled: %v", err)
	} else {
		app.notifications = notifications
	}
	if meetings, err := loadMeetingStore(meetingsPath()); err != nil {
		log.Errorf("Meeting record persistence disabled: %v", err)
	} else {
		app.meetings = meetings
	}
	// boot reconciliation: close a stale open record (its meeting id no longer
	// matches the memory store's resumed id), or re-arm the idle timer when
	// the same in-flight meeting survived the restart.
	app.reconcileMeetingRecordsAtBoot()
	// One-time, idempotent Files backfill (kanban-card-110): stamp every
	// pre-gate-qualifying deliverable savedToFiles=true so introducing the
	// explicit-save gate never disappears content the team already relies on.
	app.grandfatherSavedToFilesAtBoot()
	if !loadedBoard && boardPersistenceHealthy {
		if err := app.persistBoard(); err != nil {
			log.Errorf("Could not persist initial Kanban board: %v", err)
		}
	} else if loadedBoard && boardPersistenceHealthy {
		if err := app.persistBoard(); err != nil {
			log.Errorf("Could not persist normalized Kanban board: %v", err)
		}
	}

	return app
}

func kanbanBoardPath() string {
	if path := strings.TrimSpace(os.Getenv("KANBAN_BOARD_PATH")); path != "" {
		return path
	}
	if memoryPath := meetingMemoryPath(); strings.TrimSpace(memoryPath) != "" {
		return filepath.Join(filepath.Dir(memoryPath), "kanban-board.json")
	}

	return defaultKanbanBoardPath
}

func loadKanbanBoardState(path string) (kanbanBoardState, bool, error) {
	rawBoard, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return kanbanBoardState{}, false, nil
		}
		return kanbanBoardState{}, false, fmt.Errorf("read Kanban board: %w", err)
	}
	if len(bytes.TrimSpace(rawBoard)) == 0 {
		return kanbanBoardState{}, false, nil
	}

	var state kanbanBoardState
	if err := json.Unmarshal(rawBoard, &state); err != nil {
		return kanbanBoardState{}, false, fmt.Errorf("decode Kanban board: %w", err)
	}
	state.Cards = normalizeKanbanCards(state.Cards)

	return state, true, nil
}

func writeKanbanBoardState(path string, state kanbanBoardState) error {
	return writeJSONFileAtomically(path, "Kanban board", state)
}

func writeJSONFileAtomically(path string, description string, value any) error {
	rawJSON, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", description, err)
	}
	rawJSON = append(rawJSON, '\n')

	if err := writeFileAtomicallyForCanonicalMode(path, rawJSON, 0o600); err != nil {
		return fmt.Errorf("persist %s: %w", description, err)
	}

	return nil
}

func normalizeKanbanCards(cards []kanbanCard) []kanbanCard {
	normalized := make([]kanbanCard, 0, len(cards))
	seenIDs := map[string]struct{}{}
	for index, card := range cards {
		card.ID = strings.TrimSpace(card.ID)
		if card.ID == "" {
			card.ID = fmt.Sprintf("persisted-card-%03d", index+1)
		}
		if _, exists := seenIDs[card.ID]; exists {
			card.ID = fmt.Sprintf("%s-%d", card.ID, index+1)
		}
		seenIDs[card.ID] = struct{}{}

		if !knownKanbanStatus(card.Status) {
			card.Status = kanbanStatusBacklog
		}
		card.Title = strings.TrimSpace(card.Title)
		if card.Title == "" {
			card.Title = "Untitled card"
		}
		card.Notes = cleanBoardNotes(card.Notes)
		card.Owner = normalizePersistedCardOwner(card.Owner)
		card.Tags = canonicalizeBoardTags(card.Tags)
		card.DueDate = normalizeKeyDateText(card.DueDate)
		card.KeyDates = normalizeKanbanKeyDates(card.KeyDates)
		if card.DueDate == "" {
			card.DueDate = dueDateFromKeyDates(card.KeyDates)
		}
		card.DraftedAt = strings.TrimSpace(card.DraftedAt)
		if !card.Draft {
			card.DraftedAt = ""
		}
		normalized = append(normalized, cloneKanbanCard(card))
	}

	return normalized
}

func normalizePersistedCardOwner(owner string) string {
	owner = strings.TrimSpace(owner)
	if owner == "" || strings.EqualFold(owner, "Unassigned") {
		return "Unassigned"
	}
	if canonicalOwner := canonicalParticipantName(owner); canonicalOwner != "" {
		return canonicalOwner
	}

	return owner
}

func knownKanbanStatus(status kanbanStatus) bool {
	for _, candidate := range kanbanStatuses {
		if status == candidate {
			return true
		}
	}

	return false
}

func nextKanbanCardIndex(cards []kanbanCard) int {
	nextIndex := 1
	for _, card := range cards {
		var cardIndex int
		if _, err := fmt.Sscanf(card.ID, "kanban-card-%d", &cardIndex); err == nil && cardIndex >= nextIndex {
			nextIndex = cardIndex + 1
		}
	}

	return nextIndex
}

func (app *kanbanBoardApp) JoinConferenceRoom() error {
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return fmt.Errorf("OPENAI_API_KEY is not configured")
	}
	app.mu.Lock()
	app.apiKey = apiKey
	app.mu.Unlock()
	// W4 (§4.4/§9.10, the plan's ONE deliberate behavior change): the office
	// transcription lane and the Scout Realtime peer are no longer boot-started
	// — they are created lazily at the first admission (ensureRoomMedia →
	// ensureOfficeMedia) and torn down after the idle-end close chain, ending
	// the always-on OpenAI spend. Scout connects concurrently with the join
	// handshake, so the first-join delay is invisible.
	app.startMeetingBrainWorker(apiKey)
	app.startMeetingBoardWorker(apiKey)
	// Item B: the ambient research-suggestion worker rides the SAME brain stream
	// as the board worker but with a looser, confirm-first bar — it volunteers a
	// research proposal (codex_proposals.go) when the room DISCUSSES a workstream
	// without ever addressing Scout, and launches nothing on its own
	// (suggestion_agent.go).
	app.startResearchSuggestionWorker(apiKey)
	app.startMissionIntelligenceWorker(apiKey)
	app.startNarrativeMaintainerWorker(apiKey)
	app.startDecisionLedgerWorker(apiKey)
	// Track-2 digest tiers (meeting_digest.go): brains → per-meeting digests →
	// per-day rollups, plus the end-of-day reflection riding the day tick.
	// Backfill is OFF by default (MEETING_DIGEST_BACKFILL/DAY_DIGEST_BACKFILL)
	// so a first deploy never token-spikes over weeks of stored brains.
	app.startMeetingDigestWorker(apiKey)
	app.startDayDigestWorker(apiKey)
	// Track-2 Wave 3 (amendment A1): the entity ledger consolidates each landed
	// meeting_digest's facts — plus new decision-ledger rows — into the
	// canonical cross-meeting registry of decisions / action items / topics /
	// open questions (entity_ledger.go: deterministic match first, one batched
	// LLM adjudication call only for ambiguity, ADD/UPDATE/SUPERSEDE/CLOSE
	// events folded into a rebuildable read-model). Backfill OFF by default
	// (ENTITY_LEDGER_BACKFILL).
	app.startEntityLedgerWorker(apiKey)
	// Track-2 Wave 4 (amendment A2): T4 company digest = the ledger's CURRENT
	// state view (deterministic Go projection) + a THIN narrative refreshed
	// from ledger deltas — never a recursive re-summary of day digests
	// (company_digest.go). Wakes only when the ledger changed (inputKind =
	// ledger_event). Backfill OFF by default (COMPANY_DIGEST_BACKFILL).
	app.startCompanyDigestWorker(apiKey)
	// Card 067: the ~5-minute status re-scan that relaunches approved-but-stuck
	// proposals and any auto_run-lane standing-approved work. Model-free, so it
	// starts independent of the API key gate above.
	app.startWorkflowTicker()
	app.reconcileGoalThreadsAtBoot()

	return nil
}

// ensureOfficeMedia lazily creates the office's media plane on admission (W4
// §4.4): the transcription lane on first seat, and the Scout Realtime peer
// ONLY while the sitting is not listen-only (§7.3's never-started layer).
// Idempotent per sitting; keyless boots no-op exactly like before.
func (app *kanbanBoardApp) ensureOfficeMedia() {
	app.mu.Lock()
	apiKey := app.apiKey
	laneRunning := app.transcriptLane != nil
	app.mu.Unlock()
	if strings.TrimSpace(apiKey) == "" {
		return
	}
	if !laneRunning {
		app.startTranscriptionLane(apiKey)
	}
	if !app.sittingListenOnly(officeRoomID) {
		app.ensureOfficeRealtimePeer(apiKey)
	}
}

// ensureOfficeRealtimePeer starts the office Scout peer when none is live.
// The office roomLive mediaGen is captured onto realtimeMediaGen so the
// restart loops are fenced against a teardown (the sitting ended) racing a
// reconnect — a moved gen aborts the restart instead of resurrecting a peer
// for an empty room.
func (app *kanbanBoardApp) ensureOfficeRealtimePeer(apiKey string) {
	app.mu.Lock()
	if app.pc != nil || app.restarting || app.realtimeStarting {
		app.mu.Unlock()
		return
	}
	app.realtimeStarting = true
	app.realtimeMediaGen = app.roomLiveLocked(officeRoomID).mediaGen
	app.mu.Unlock()

	err := app.startRealtimePeer(apiKey, realtimeModel())
	app.mu.Lock()
	app.realtimeStarting = false
	app.mu.Unlock()
	if err != nil {
		log.Errorf("Failed to start OpenAI Realtime peer on admission: %v", err)
		broadcastAssistantEvent("error", "OpenAI Realtime disabled: "+err.Error(), nil)
	}
}

// teardownRealtimePeerForIdle closes the office Scout peer at the tail of the
// office idle-end close chain (W4 §4.4) WITHOUT scheduling a restart: the
// proactive-restart cancel is closed, every session field resets, and the
// next admission recreates the peer via ensureOfficeMedia.
func (app *kanbanBoardApp) teardownRealtimePeerForIdle() {
	app.mu.Lock()
	peerConnection := app.pc
	app.pc = nil
	app.events = nil
	app.inputTrack = nil
	app.inputEnc = nil
	app.connected = false
	app.forwardedAudioNotice = false
	app.realtimeResponseActive = false
	app.scoutVoiceArmedAt = time.Time{}
	app.scoutVoiceArmedUntil = time.Time{}
	app.scoutSpokenResponse = false
	app.scoutSpokenResponseSent = false
	app.scoutLastToolResultAt = time.Time{}
	app.scoutLastToolResultName = ""
	cancelProactiveRestart := app.proactiveReconnectCancel
	app.proactiveReconnectCancel = nil
	app.mu.Unlock()

	if cancelProactiveRestart != nil {
		close(cancelProactiveRestart)
	}
	if peerConnection != nil {
		_ = peerConnection.Close()
	}
}

func (app *kanbanBoardApp) startRealtimePeer(apiKey string, model string) error {
	peerConnection, err := newPeerConnection()
	if err != nil {
		return fmt.Errorf("create Realtime peer connection: %w", err)
	}

	inputTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeOpus,
			ClockRate: roomAudioSampleRate,
			Channels:  realtimeAudioChannels,
		},
		realtimeInputTrackID,
		realtimeInputStreamID,
	)
	if err != nil {
		_ = peerConnection.Close()
		return fmt.Errorf("create Realtime mixed audio input track: %w", err)
	}

	inputEnc, err := newOpusEncoder(roomAudioSampleRate, realtimeAudioChannels)
	if err != nil {
		_ = peerConnection.Close()
		return fmt.Errorf("create Realtime mixed audio encoder: %w", err)
	}

	inputSender, err := peerConnection.AddTrack(inputTrack)
	if err != nil {
		_ = peerConnection.Close()
		return fmt.Errorf("attach Realtime mixed audio input track: %w", err)
	}
	go drainRTCP(inputSender)

	events, err := peerConnection.CreateDataChannel(realtimeEventChannelLabel, nil)
	if err != nil {
		_ = peerConnection.Close()
		return fmt.Errorf("create Realtime event data channel: %w", err)
	}

	app.mu.Lock()
	app.model = model
	app.pc = peerConnection
	app.events = events
	app.inputTrack = inputTrack
	app.inputEnc = inputEnc
	app.forwardedAudioNotice = false
	app.realtimeResponseActive = false
	app.scoutVoiceArmedAt = time.Time{}
	app.scoutVoiceArmedUntil = time.Time{}
	app.scoutSpokenResponse = false
	app.scoutSpokenResponseSent = false
	app.scoutLastToolResultAt = time.Time{}
	app.scoutLastToolResultName = ""
	app.mu.Unlock()

	peerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		app.handleRealtimePeerConnectionState(peerConnection, state)
	})
	events.OnOpen(func() {
		log.Infof("OpenAI Realtime event channel opened")
		_ = app.SendEvent(app.sessionUpdateEvent())
		broadcastKanbanEvent("status", "Kanban assistant is listening")
		broadcastAssistantEvent("status", "Kanban assistant is listening", nil)
	})
	events.OnMessage(func(message webrtc.DataChannelMessage) {
		app.handleRealtimeEvent(message.Data)
	})
	peerConnection.OnTrack(func(t *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		app.forwardRealtimeOutputTrack(t)
	})

	go func() {
		if err := app.connectRealtimePeer(apiKey, model); err != nil {
			log.Errorf("Failed to connect OpenAI Realtime peer: %v", err)
			broadcastKanbanEvent("status", "OpenAI Realtime disabled: "+err.Error())
			broadcastAssistantEvent("error", "OpenAI Realtime disabled: "+err.Error(), nil)
			_ = peerConnection.Close()
			app.mu.Lock()
			if app.pc == peerConnection {
				app.pc = nil
				app.events = nil
				app.inputTrack = nil
				app.inputEnc = nil
				app.connected = false
				app.forwardedAudioNotice = false
				app.realtimeResponseActive = false
				app.scoutVoiceArmedAt = time.Time{}
				app.scoutVoiceArmedUntil = time.Time{}
				app.scoutSpokenResponse = false
				app.scoutSpokenResponseSent = false
				app.scoutLastToolResultAt = time.Time{}
				app.scoutLastToolResultName = ""
			}
			app.mu.Unlock()
			return
		}
		app.ensureRoomMixerSink()
		app.startProactiveRealtimeRestart(peerConnection)
	}()

	return nil
}

func (app *kanbanBoardApp) handleRealtimePeerConnectionState(peerConnection *webrtc.PeerConnection, state webrtc.PeerConnectionState) {
	log.Infof("OpenAI Realtime peer state changed: %s", state.String())
	broadcastKanbanEvent("status", "OpenAI Realtime: "+state.String())
	broadcastAssistantEvent("status", "OpenAI Realtime: "+state.String(), nil)

	switch state {
	case webrtc.PeerConnectionStateFailed:
		go app.restartRealtimePeerIfStill(peerConnection, state, 0, "Realtime peer connection failed")
	case webrtc.PeerConnectionStateDisconnected:
		go app.restartRealtimePeerIfStill(peerConnection, state, 5*time.Second, "Realtime peer connection stayed disconnected")
	}
}

func (app *kanbanBoardApp) restartRealtimePeerIfStill(peerConnection *webrtc.PeerConnection, state webrtc.PeerConnectionState, delay time.Duration, reason string) {
	if delay > 0 {
		time.Sleep(delay)
	}
	if peerConnection == nil {
		return
	}

	app.mu.Lock()
	isCurrent := app.pc == peerConnection && !app.restarting
	app.mu.Unlock()
	if !isCurrent || peerConnection.ConnectionState() != state {
		return
	}

	app.restartRealtimePeer(reason)
}

func (app *kanbanBoardApp) restartRealtimePeer(reason string) {
	app.mu.Lock()
	if app.restarting {
		app.mu.Unlock()
		return
	}
	// W4 mediaGen fence: after an idle teardown (pc nil, or the office media
	// generation moved) there is nothing to restart — the next admission
	// recreates the peer lazily. Without this, a queued reconnect could
	// resurrect a Scout session for an empty room.
	if app.pc == nil || app.roomLiveLocked(officeRoomID).mediaGen != app.realtimeMediaGen {
		app.mu.Unlock()
		return
	}
	app.restarting = true
	apiKey := app.apiKey
	peerConnection := app.pc
	app.pc = nil
	app.events = nil
	app.inputTrack = nil
	app.inputEnc = nil
	app.connected = false
	app.forwardedAudioNotice = false
	app.realtimeResponseActive = false
	app.scoutVoiceArmedAt = time.Time{}
	app.scoutVoiceArmedUntil = time.Time{}
	app.scoutSpokenResponse = false
	app.scoutSpokenResponseSent = false
	app.scoutLastToolResultAt = time.Time{}
	app.scoutLastToolResultName = ""
	cancelProactiveRestart := app.proactiveReconnectCancel
	app.proactiveReconnectCancel = nil
	app.mu.Unlock()

	defer func() {
		app.mu.Lock()
		app.restarting = false
		app.mu.Unlock()
	}()

	app.removeRoomMixerSinkIfIdle()
	if cancelProactiveRestart != nil {
		close(cancelProactiveRestart)
	}
	if peerConnection != nil {
		_ = peerConnection.Close()
	}
	if strings.TrimSpace(apiKey) == "" {
		broadcastKanbanEvent("status", "OpenAI Realtime disabled: OPENAI_API_KEY is not configured")
		broadcastAssistantEvent("error", "OpenAI Realtime disabled: OPENAI_API_KEY is not configured", nil)
		return
	}

	if reason != "" {
		log.Infof("Restarting OpenAI Realtime peer: %s", reason)
		broadcastKanbanEvent("status", "OpenAI Realtime reconnecting: "+reason)
		broadcastAssistantEvent("status", "OpenAI Realtime reconnecting: "+reason, nil)
	}

	if err := app.startRealtimePeer(apiKey, realtimeModel()); err != nil {
		log.Errorf("Failed to restart OpenAI Realtime peer: %v", err)
		broadcastKanbanEvent("status", "OpenAI Realtime disabled: "+err.Error())
		broadcastAssistantEvent("error", "OpenAI Realtime disabled: "+err.Error(), nil)
	}
}

func (app *kanbanBoardApp) Close() error {
	var closeErr error
	app.closeOnce.Do(func() {
		if roomMixer != nil {
			roomMixer.removeSink(realtimeMixedAudioSinkKey)
		}

		app.mu.Lock()
		peerConnection := app.pc
		app.pc = nil
		app.events = nil
		app.inputTrack = nil
		app.inputEnc = nil
		app.connected = false
		app.forwardedAudioNotice = false
		app.realtimeResponseActive = false
		app.scoutVoiceArmedAt = time.Time{}
		app.scoutVoiceArmedUntil = time.Time{}
		app.scoutSpokenResponse = false
		app.scoutSpokenResponseSent = false
		app.scoutLastToolResultAt = time.Time{}
		app.scoutLastToolResultName = ""
		cancelProactiveRestart := app.proactiveReconnectCancel
		app.proactiveReconnectCancel = nil
		agentCancels := app.agentCancels
		agentDones := app.agentDones
		transcriptLane := app.transcriptLane
		app.transcriptLane = nil
		app.agentCancels = nil
		app.agentDones = nil
		app.agentBaselineIDs = nil
		app.agentNudges = nil
		app.agentPendingRooms = nil
		app.agentFailures = nil
		app.mu.Unlock()
		if transcriptLane != nil {
			transcriptLane.close()
		}
		if cancelProactiveRestart != nil {
			close(cancelProactiveRestart)
		}
		for name, cancel := range agentCancels {
			if cancel == nil {
				continue
			}
			close(cancel)
			if done := agentDones[name]; done != nil {
				<-done
			}
		}
		if peerConnection != nil {
			closeErr = peerConnection.Close()
		}
	})

	return closeErr
}

func (app *kanbanBoardApp) connectRealtimePeer(apiKey string, model string) error {
	app.mu.Lock()
	if app.connected {
		app.mu.Unlock()
		return nil
	}
	peerConnection := app.pc
	app.mu.Unlock()

	if peerConnection == nil {
		return fmt.Errorf("Realtime peer connection is unavailable")
	}

	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("create Realtime offer: %w", err)
	}

	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)
	if err := peerConnection.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("set Realtime local description: %w", err)
	}
	<-gatherComplete

	localDescription := peerConnection.LocalDescription()
	if localDescription == nil || strings.TrimSpace(localDescription.SDP) == "" {
		return fmt.Errorf("Realtime peer connection did not produce a local description")
	}

	answerSDP, err := app.createRealtimeCall(apiKey, model, localDescription.SDP)
	if err != nil {
		return err
	}

	if err := peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  answerSDP,
	}); err != nil {
		return fmt.Errorf("set Realtime remote description: %w", err)
	}

	app.mu.Lock()
	app.connected = true
	app.mu.Unlock()

	return nil
}

func (app *kanbanBoardApp) startProactiveRealtimeRestart(peerConnection *webrtc.PeerConnection) {
	cancel := make(chan struct{})

	app.mu.Lock()
	if app.pc != peerConnection {
		app.mu.Unlock()
		close(cancel)
		return
	}
	if app.proactiveReconnectCancel != nil {
		close(app.proactiveReconnectCancel)
	}
	app.proactiveReconnectCancel = cancel
	app.mu.Unlock()

	go func() {
		select {
		case <-time.After(55 * time.Minute):
			app.mu.Lock()
			isCurrent := app.pc == peerConnection
			app.mu.Unlock()
			if isCurrent {
				app.restartRealtimePeer("scheduled refresh before session expiration")
			}
		case <-cancel:
		}
	}()
}

func (app *kanbanBoardApp) WriteMixedPCM(roomPCM []int16) error {
	if len(roomPCM) == 0 {
		return nil
	}
	if len(roomPCM)%roomAudioMixFrameSize != 0 {
		return fmt.Errorf("mixed PCM length %d must be a multiple of %d samples", len(roomPCM), roomAudioMixFrameSize)
	}

	isSyntheticSilence := pcmIsZero(roomPCM)
	recordingEnabled := app.transcriptRecordingActive()
	if !recordingEnabled {
		return nil
	}

	transcriptQueued := false
	if !isSyntheticSilence {
		transcriptQueued = app.enqueueTranscriptionLaneAudio(roomPCM)
	}

	if isSyntheticSilence && !app.realtimeAudioInputAvailable() {
		return nil
	}

	if err := app.writeRealtimeMixedPCM(roomPCM); err != nil {
		if transcriptQueued {
			return nil
		}
		return err
	}

	return nil
}

func pcmIsZero(pcm []int16) bool {
	if len(pcm) == 0 {
		return false
	}
	for _, sample := range pcm {
		if sample != 0 {
			return false
		}
	}
	return true
}

func (app *kanbanBoardApp) realtimeAudioInputAvailable() bool {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.inputTrack != nil && app.inputEnc != nil
}

func (app *kanbanBoardApp) writeRealtimeMixedPCM(roomPCM []int16) error {
	app.mu.Lock()
	inputTrack := app.inputTrack
	inputEnc := app.inputEnc
	app.mu.Unlock()

	if inputTrack == nil || inputEnc == nil {
		return fmt.Errorf("Realtime mixed audio input is unavailable")
	}

	for offset := 0; offset < len(roomPCM); offset += roomAudioMixFrameSize {
		frame := roomPCM[offset : offset+roomAudioMixFrameSize]

		opusFrame, err := inputEnc.Encode(roomPCMForRealtime(frame))
		if err != nil {
			return fmt.Errorf("encode mixed room audio: %w", err)
		}

		if err := inputTrack.WriteSample(media.Sample{
			Data:     opusFrame,
			Duration: roomAudioMixInterval,
		}); err != nil {
			return fmt.Errorf("write mixed room audio sample: %w", err)
		}
	}

	app.noteRealtimeAudioForwarded()
	return nil
}

func (app *kanbanBoardApp) noteRealtimeAudioForwarded() {
	app.mu.Lock()
	if app.forwardedAudioNotice {
		app.mu.Unlock()
		return
	}
	app.forwardedAudioNotice = true
	app.mu.Unlock()

	broadcastAssistantEvent("audio", "mixed room audio is reaching the assistant", nil)
}

func (app *kanbanBoardApp) forwardRealtimeOutputTrack(t *webrtc.TrackRemote) {
	if t == nil {
		return
	}

	log.Infof("Got OpenAI Realtime output track: Kind=%s, ID=%s, PayloadType=%d", t.Kind(), t.ID(), t.PayloadType())
	if t.Kind() != webrtc.RTPCodecTypeAudio {
		return
	}

	forwardedTrackID := forwardedRemoteTrackID(t)
	broadcastAssistantEvent("audio", "Scout voice connected", map[string]any{
		"trackId":       forwardedTrackID,
		"sourceTrackId": t.ID(),
		"streamId":      t.StreamID(),
		"payloadType":   t.PayloadType(),
	})

	// Scout's realtime output voice registers ONLY into its room's track table
	// (the office until the W4 per-room realtime extraction), so a second
	// room's subscribers are never offered it.
	trackLocal, err := addTrack(officeRoomID, t, scoutParticipantName, "scout")
	if err != nil {
		log.Errorf("Failed to create local track for Scout voice=%s: %v", t.ID(), err)
		return
	}
	broadcastKanbanEvent("participant_track", participantTrackPayload(scoutParticipantName, t))
	signalPeerConnections()
	defer removeTrack(trackLocal)

	for {
		packet, _, err := t.ReadRTP()
		if err != nil {
			return
		}

		packet.Extension = false
		packet.Extensions = nil

		if err := trackLocal.WriteRTP(packet); err != nil {
			return
		}
	}
}

func drainRTCP(sender *webrtc.RTPSender) {
	buffer := make([]byte, 1500)
	for {
		if _, _, err := sender.Read(buffer); err != nil {
			return
		}
	}
}

func (app *kanbanBoardApp) createRealtimeCall(apiKey string, model string, offerSDP string) (string, error) {
	warnRealtimeVoiceSessionNoVocab("room")
	return app.createRealtimeCallWithSession(apiKey, offerSDP, app.sessionConfig(model))
}

func (app *kanbanBoardApp) createPrivateRealtimeVoiceCall(apiKey string, model string, offerSDP string) (string, error) {
	warnRealtimeVoiceSessionNoVocab("private")
	return app.createRealtimeCallWithSession(apiKey, offerSDP, app.privateRealtimeVoiceSessionConfig(model))
}

// warnRealtimeVoiceSessionNoVocab makes a degraded voice-session transcription
// config LOUD (W1-12): when the configured transcription model rejects the
// domain-vocabulary prompt (whisper family), the voice peer's fallback
// transcription lane loses vocabulary biasing entirely — proper nouns start
// mangling silently. Logged + funneled once per session CREATE, deliberately
// not inside sessionConfig itself, which is re-sent on every board-context
// session.update.
func warnRealtimeVoiceSessionNoVocab(surface string) {
	model := realtimeTranscriptionModel()
	if transcriptionModelAcceptsPrompt(model) {
		return
	}
	log.Warnf("Realtime %s voice session transcription model %q rejects the domain-vocabulary prompt — transcription fidelity is degraded (set OPENAI_REALTIME_TRANSCRIPTION_MODEL to a gpt-4o transcription model to restore vocabulary biasing)", surface, model)
	recordEvalEvent(seatTranscriptionSession, evalKindNoVocabWarning, map[string]any{
		"model":   model,
		"surface": surface,
	})
}

func (app *kanbanBoardApp) createRealtimeCallWithSession(apiKey string, offerSDP string, session map[string]any) (string, error) {
	contentType, body, err := buildRealtimeCallRequest(offerSDP, session)
	if err != nil {
		return "", err
	}

	request, err := http.NewRequest(http.MethodPost, realtimeCallsURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create Realtime request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", contentType)

	response, err := realtimeHTTPClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("create Realtime session: %w", err)
	}
	defer response.Body.Close()

	answerSDP, err := io.ReadAll(response.Body)
	if err != nil {
		return "", fmt.Errorf("read Realtime answer: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", apiRequestFailedError("Realtime session failed", response.Status, answerSDP)
	}

	normalizedAnswer, err := normalizeRealtimeSDP(string(answerSDP))
	if err != nil {
		log.Errorf("Realtime session returned invalid SDP answer: %v", err)
		return "", fmt.Errorf("Realtime session returned an invalid answer")
	}
	return normalizedAnswer, nil
}

func (app *kanbanBoardApp) privateRealtimeVoiceSessionConfig(model string) map[string]any {
	session := app.sessionConfig(model)
	session["instructions"] = app.privateRealtimeVoiceSessionInstructions()
	session["tools"] = app.privateRealtimeVoiceTools()
	session["tool_choice"] = "auto"
	return session
}

func (app *kanbanBoardApp) privateRealtimeVoiceSessionInstructions() string {
	return strings.Join([]string{
		"# Role and Objective\nYou are Scout, the private Bonfire OS voice assistant on the dashboard. This is a one-user Realtime 2 conversation outside the video room. You can act across the whole OS on this user's behalf: navigate, recall, run the board, edit and publish artifacts, notify the team, post as the user, and launch goals.",
		"# Boundary\nYou act on this one user's behalf — you are NOT the room's shared voice. Do not describe yourself as the shared room Scout, do not say the room can hear you, and do not treat the user as a meeting participant. You MAY update the shared Kanban board on the user's behalf (create, move, update, tag, date, delete, or undo cards) — announce what you changed. External writes (commit, push, deploy, production side effects) stay gated: you never perform them directly, and initiate_goal cannot request them. When you post as the user with start_chat_as_user, the message is always stamped and shown as posted via Scout — disclosure is mandatory and automatic. If the user asks for the live room, use control_app to open the Room surface; do not claim you joined as the shared room voice operator.",
		"# OS actions\nUse control_app to open office, room, chat, artifacts, research, design, grill, board, memory, or files; pass also_open to open several surfaces at once. Use the board tools (create_ticket, move_ticket, update_ticket, add_tags, add_key_date, remove_key_dates, delete_ticket, undo_delete_ticket) to run the board for the user. Use update_artifact / publish_artifact to edit or publish a saved artifact the user owns. Use launch_agent_thread for a single research, investigate, source, design, grill, pressure-test, or plan request so Chat becomes the live work surface and the finished Markdown is saved as an artifact. Use initiate_goal for a multi-step objective the user wants Scout to plan and drive end to end (\"package the Aurora IP\", \"take this from idea to investor-ready\"). Use create_artifact only when the user asks to save a quick, explicit piece of already-known content. Use answer_memory_question for recall across saved meetings and artifacts. Use read_thread_aloud to fetch and then speak the recent messages of a channel or private thread, an artifact, or the user's notifications. Use organize_files when the user asks to file, sort, or group files or deliverables into a folder on the Files surface; name the folder and pass the file-name fragments to match. Use save_to_files when the user asks to save, keep, or add a finished deliverable (a research report, deck, or goal output) to the Files surface — deliverables no longer appear there automatically, so pass the title fragments to match and optionally a folder to file them under. Use note_for_the_record when the user explicitly wants something remembered in company memory (\"note for the record…\", \"remember that…\", \"put on record that…\"); pass kind=decision for an explicit decision or stated position and it lands as a proposed decision the team can ratify. Use send_notification when the user asks to notify the team, post an alert, or leave a reminder in the notification bell; audience everyone reaches all signed-in users, audience me notifies only this user, and deliver \"after_meeting\" queues it until the meeting is archived when the user says after this meeting, remind. Use propose_codex_task when the user asks to queue, delegate, or staff agent work for later; it only posts a proposal card that a human must confirm before any agent thread launches. Use create_package / attach_to_package / advance_package_stage to manage venture packages — the per-IP mission binders shown in Mission Intelligence. Use do_nothing for unclear speech or requests that require shared-room controls.",
		"# Channels and posting as the user\nUse post_to_channel when the user says put/post/share that in #channel or tell the team; quote their content faithfully, never embellish. Use start_chat_as_user to START a new channel or private thread and post the user's message into it on their behalf — the post is always disclosed as via Scout. Before posting as the user, read the draft back and get a yes. Use mention to flag one person by name. Use create_channel to make a new public team channel when asked.",
		"# Meeting recap\nUse meeting_recap with audience \"me\" (or catch_me_up) for catch-me-up requests about the live meeting; it lands in the user's bell and you read the headline aloud. For catch-up SPANNING meetings or days — what did I miss this week, what happened yesterday, catch me up on the last few days — use cross_meeting_briefing instead: it returns a day-by-day briefing of decisions, action items, topics, and open questions across every meeting in the range; read the top decisions and blockers, not every line. Use get_meeting_detail with a meeting_id from a briefing for one past meeting's digest, and pass an anchor id to quote the verbatim exchange.",
		"# Private grill\nWhen the user says grill me, pressure-test me, or play investor with me, call start_private_grill (optionally naming a package to ground the question bank) and follow the returned instructions to run the three-act ritual privately — this is one-on-one, never the shared room. Call end_private_grill after you deliver the spoken readiness report; it files the graded scorecard and restores your normal behavior.",
		fmt.Sprintf("# Board context\nCurrent Kanban board JSON for lightweight recall: %s.", app.boardContextJSON()),
		fmt.Sprintf("# Domain vocabulary\nUse these exact spellings for names, brands, acronyms, and technical terms: %s.", strings.Join(domainVocabulary(), ", ")),
		"# Behavior\nAnswer directly and briefly. Prefer the available OS tools when the user asks to navigate, save an artifact, start research/design/grill/workflow, or recall memory. Use board context only when the user explicitly asks about board, card, task, status, owner, or due-date information. Ask one concise clarifying question when the request is ambiguous; do not volunteer board status for unclear follow-ups like \"what?\" just because board context is present.",
	}, "\n\n")
}

func (app *kanbanBoardApp) privateRealtimeVoiceTools() []map[string]any {
	tools := []map[string]any{}
	for _, tool := range app.kanbanTools() {
		if privateRealtimeVoiceToolAllowed(asString(tool["name"])) {
			tools = append(tools, tool)
		}
	}
	return tools
}

func buildRealtimeCallRequest(offerSDP string, session map[string]any) (string, []byte, error) {
	normalizedOffer, err := normalizeRealtimeSDP(offerSDP)
	if err != nil {
		return "", nil, fmt.Errorf("invalid Realtime SDP offer: %w", err)
	}

	sessionJSON, err := json.Marshal(session)
	if err != nil {
		return "", nil, fmt.Errorf("marshal Realtime session: %w", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writeMultipartField(writer, "sdp", "application/sdp", normalizedOffer); err != nil {
		return "", nil, fmt.Errorf("write SDP offer: %w", err)
	}
	if err := writeMultipartField(writer, "session", "application/json", string(sessionJSON)); err != nil {
		return "", nil, fmt.Errorf("write session config: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", nil, fmt.Errorf("finalize multipart request: %w", err)
	}

	return writer.FormDataContentType(), body.Bytes(), nil
}

func normalizeRealtimeSDP(sdp string) (string, error) {
	normalized := strings.TrimSpace(sdp)
	if normalized == "" {
		return "", fmt.Errorf("sdp is required")
	}
	normalized = strings.ReplaceAll(normalized, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")

	lines := strings.Split(normalized, "\n")
	for index, line := range lines {
		line = strings.TrimRight(line, " \t")
		if len(line) < 3 || line[1] != '=' || !isSDPFieldName(line[0]) {
			return "", fmt.Errorf("invalid SDP line %d", index+1)
		}
		lines[index] = line
	}
	if lines[0] != "v=0" {
		return "", fmt.Errorf("sdp must start with v=0")
	}

	return strings.Join(lines, "\r\n") + "\r\n", nil
}

func isSDPFieldName(field byte) bool {
	return strings.ContainsRune("vosiuepcbtrzkam", rune(field))
}

func writeMultipartField(writer *multipart.Writer, name string, contentType string, value string) error {
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"`, name))
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	_, err = io.WriteString(part, value)
	return err
}

func (app *kanbanBoardApp) SendEvent(payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal Realtime event: %w", err)
	}

	app.mu.Lock()
	events := app.events
	app.mu.Unlock()
	if events == nil || events.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("Realtime event channel is unavailable")
	}

	return events.SendText(string(raw))
}

func (app *kanbanBoardApp) sessionConfig(model string) map[string]any {
	transcriptionModel := realtimeTranscriptionModel()
	transcription := map[string]any{
		"model":    transcriptionModel,
		"language": "en",
	}
	input := map[string]any{
		"transcription":  transcription,
		"turn_detection": realtimeTurnDetectionConfig(),
	}
	// W1-12: the domain-vocabulary prompt + near-field noise reduction are
	// gated by transcription model exactly like transcription_lane.go — the
	// realtime whisper family rejects both fields live ("The 'prompt'
	// parameter is not supported for this model"), so sending them
	// unconditionally would break the whole voice session the way prod broke
	// on 2026-07-08. A model that rejects the prompt still transcribes, just
	// without vocabulary biasing — warnRealtimeVoiceSessionNoVocab makes that
	// degraded state loud at session create.
	if transcriptionModelAcceptsPrompt(transcriptionModel) {
		input["noise_reduction"] = map[string]any{"type": "near_field"}
		transcription["prompt"] = realtimeTranscriptionPrompt()
	}
	session := map[string]any{
		"type":              "realtime",
		"model":             model,
		"output_modalities": []string{"audio"},
		"audio": map[string]any{
			"input": input,
			"output": map[string]any{
				"voice": realtimeVoice(),
			},
		},
		"instructions": app.sessionInstructions(),
		"tools":        app.realtimeRoomVoiceTools(),
		"tool_choice":  app.realtimeToolChoice(),
	}

	if usesAdvancedCommandProfile(model) {
		session["reasoning"] = map[string]any{
			"effort": realtimeReasoningEffort(),
		}
	}

	return session
}

// realtimeRoomVoiceExcluded lists tools kept OUT of the shared room voice
// session. fiscal_api_docs and fiscal_data_query return payloads (typed docs,
// raw sandbox output) too heavy for a spoken turn — they stay orchestrator-only
// and match the private-voice exclusion (privateRealtimeVoiceToolAllowed).
var realtimeRoomVoiceExcluded = map[string]bool{
	"fiscal_api_docs":   true,
	"fiscal_data_query": true,
}

// realtimeRoomVoiceTools is kanbanTools() minus the heavy-payload tools that
// have no place in a voice turn. The full set still reaches the orchestrator
// tool loop and Scout chat proposals.
func (app *kanbanBoardApp) realtimeRoomVoiceTools() []map[string]any {
	all := app.kanbanTools()
	filtered := make([]map[string]any, 0, len(all))
	for _, tool := range all {
		if name, _ := tool["name"].(string); realtimeRoomVoiceExcluded[name] {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func (app *kanbanBoardApp) realtimeToolChoice() string {
	// The grill persona must speak freely without voice-control being on.
	if app.grillSessionActive() {
		return "auto"
	}
	if app.voiceControlEnabled() {
		return "auto"
	}

	return "required"
}

func (app *kanbanBoardApp) sessionUpdateEvent() map[string]any {
	return map[string]any{
		"type":    "session.update",
		"session": app.sessionConfig(app.model),
	}
}

func (app *kanbanBoardApp) setVoiceControlActive(active bool, updatedBy string) {
	if app == nil {
		return
	}
	updatedBy = canonicalRoomActorName(updatedBy)
	app.mu.Lock()
	changed := app.voiceControlActive != active || app.voiceControlUpdatedBy != updatedBy
	app.voiceControlActive = active
	app.voiceControlUpdatedAt = time.Now().UTC()
	app.voiceControlUpdatedBy = updatedBy
	app.mu.Unlock()

	state := "listening"
	text := "Realtime 2 voice is listening."
	if !active {
		state = "idle"
		text = "Realtime 2 voice is off."
	}
	broadcastAssistantEvent("status", text, map[string]any{
		"voiceControl": active,
		"voiceState":   state,
		"updatedBy":    updatedBy,
	})
	if changed {
		app.refreshRealtimeBoardContext("voice control")
	}
}

func (app *kanbanBoardApp) voiceControlEnabled() bool {
	if app == nil {
		return false
	}
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.voiceControlActive
}

func (app *kanbanBoardApp) refreshRealtimeBoardContext(reason string) {
	if app == nil {
		return
	}
	if err := app.SendEvent(app.sessionUpdateEvent()); err != nil {
		if !strings.Contains(err.Error(), "Realtime event channel is unavailable") {
			log.Errorf("Failed to refresh Realtime board context after %s: %v", reason, err)
		}
	}
}

func realtimeModel() string {
	if model := strings.TrimSpace(os.Getenv("OPENAI_REALTIME_MODEL")); model != "" {
		return model
	}

	return defaultRealtimeModel
}

func realtimeReasoningEffort() string {
	effort := strings.ToLower(strings.TrimSpace(os.Getenv("OPENAI_REALTIME_REASONING_EFFORT")))
	switch effort {
	case "minimal", "low", "medium", "high", "xhigh":
		return effort
	default:
		return defaultReasoningEffort
	}
}

func realtimeVoice() string {
	if voice := strings.TrimSpace(os.Getenv("OPENAI_REALTIME_VOICE")); voice != "" {
		return voice
	}

	return defaultRealtimeVoice
}

func realtimeTurnDetectionConfig() map[string]any {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OPENAI_REALTIME_VAD_TYPE"))) {
	case "server_vad":
		return map[string]any{
			"type":                "server_vad",
			"threshold":           0.5,
			"prefix_padding_ms":   300,
			"silence_duration_ms": 300,
			"create_response":     true,
			"interrupt_response":  true,
		}
	case "semantic_vad":
		return map[string]any{
			"type":               "semantic_vad",
			"eagerness":          realtimeVADEagerness(),
			"create_response":    true,
			"interrupt_response": true,
		}
	case "":
		return realtimeTurnDetectionConfigWithDefaults()
	default:
		return realtimeTurnDetectionConfigWithDefaults()
	}
}

func realtimeTurnDetectionConfigWithDefaults() map[string]any {
	return map[string]any{
		"type":                defaultRealtimeVADType,
		"threshold":           0.5,
		"prefix_padding_ms":   300,
		"silence_duration_ms": 300,
		"create_response":     true,
		"interrupt_response":  true,
	}
}

func realtimeVADEagerness() string {
	eagerness := strings.ToLower(strings.TrimSpace(os.Getenv("OPENAI_REALTIME_VAD_EAGERNESS")))
	switch eagerness {
	case "low", "medium", "high", "auto":
		return eagerness
	default:
		return defaultVADEagerness
	}
}

func isRealtimeActiveResponseError(event kanbanRealtimeEvent) bool {
	if event.Error == nil {
		return false
	}
	message := strings.ToLower(event.Error.Message)
	return strings.Contains(message, "active response in progress") &&
		strings.Contains(message, "wait until the response is finished")
}

// usesAdvancedCommandProfile reports whether the realtime model accepts the
// session "reasoning" effort block. W1-13: match the gpt-realtime-2 FAMILY
// (gpt-realtime-2, gpt-realtime-2.1, future point releases) instead of the
// old exact string, so an env model bump no longer silently drops the effort
// pin — but EXCLUDE -mini variants until their session.reasoning support is
// live-verified (the official model page is silent on it).
func usesAdvancedCommandProfile(model string) bool {
	normalizedModel := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(normalizedModel, "mini") {
		return false
	}
	if normalizedModel == "gpt-realtime-2" {
		return true
	}
	// Point releases and dashed variants only — a hypothetical
	// "gpt-realtime-20" is a different family and must not match.
	return strings.HasPrefix(normalizedModel, "gpt-realtime-2.") ||
		strings.HasPrefix(normalizedModel, "gpt-realtime-2-")
}

// telemetryLaneSnapshot reports the effective realtime + transcription lane
// configuration for healthz surfacing (W0-9). Pure config read: env-backed
// accessors only, no app state, no wire calls. Both voice surfaces share
// OPENAI_REALTIME_MODEL today (the W5 OPENAI_PRIVATE_REALTIME_MODEL dial does
// not exist yet), so private_voice_model mirrors realtime_model.
func telemetryLaneSnapshot() map[string]any {
	laneModel := transcriptionLaneModel()
	sessionModel := realtimeTranscriptionModel()
	return map[string]any{
		"realtime_model":            realtimeModel(),
		"realtime_reasoning_effort": realtimeReasoningEffort(),
		"transcription_lane_model":  laneModel,
		"transcription_lane_vocab":  transcriptionModelAcceptsPrompt(laneModel),
		"voice_transcription_model": sessionModel,
		"voice_transcription_vocab": transcriptionModelAcceptsPrompt(sessionModel),
		"private_voice_model":       realtimeModel(),
	}
}

// validateRealtimeConfig checks the realtime/transcription dials at boot
// (W1-12 tail) and logs one warning per problem: a whisper-family
// transcription model silently loses vocabulary biasing, a model id missing
// from the pricing table is usually an env typo (typo'd dials fail
// per-request in prod while mocked tests stay green), and an out-of-enum
// effort value silently falls back to the default. Returns the warning list
// so the boot caller and tests can assert on it; an empty slice means the
// config is clean.
func validateRealtimeConfig() []string {
	var warnings []string
	now := time.Now().UTC()
	for _, lane := range []struct{ label, model string }{
		{"transcript lane (OPENAI_TRANSCRIPT_MODEL)", transcriptionLaneModel()},
		{"voice-session transcription (OPENAI_REALTIME_TRANSCRIPTION_MODEL)", realtimeTranscriptionModel()},
	} {
		if !transcriptionModelAcceptsPrompt(lane.model) {
			warnings = append(warnings, fmt.Sprintf("%s model %q rejects the domain-vocabulary prompt — transcription fidelity is degraded", lane.label, lane.model))
		}
		if _, priced := priceForModel(lane.model, now); !priced {
			warnings = append(warnings, fmt.Sprintf("%s model %q has no pricing-table row — likely an env typo", lane.label, lane.model))
		}
	}
	if _, priced := priceForModel(realtimeModel(), now); !priced {
		warnings = append(warnings, fmt.Sprintf("realtime voice (OPENAI_REALTIME_MODEL) model %q has no pricing-table row — likely an env typo", realtimeModel()))
	}
	if raw := strings.ToLower(strings.TrimSpace(os.Getenv("OPENAI_REALTIME_REASONING_EFFORT"))); raw != "" {
		switch raw {
		case "minimal", "low", "medium", "high", "xhigh":
		default:
			warnings = append(warnings, fmt.Sprintf("OPENAI_REALTIME_REASONING_EFFORT=%q is not one of minimal/low/medium/high/xhigh — falling back to %q", raw, defaultReasoningEffort))
		}
	}
	for _, warning := range warnings {
		log.Warnf("Realtime config: %s", warning)
	}
	return warnings
}

// scoutWakeFillerWords are leading throwaway tokens ASR often prepends to an
// addressed turn; one is tolerated before the wake phrase.
var scoutWakeFillerWords = map[string]struct{}{
	"um": {}, "uh": {}, "uhm": {}, "ok": {}, "okay": {}, "so": {}, "well": {},
	"yeah": {}, "oh": {}, "hi": {}, "hello": {}, "alright": {}, "hey": {},
}

// scoutWakeWords cover the scout token plus common ASR confusions
// (scout's tokenizes to scout + s, so it matches via "scout").
var scoutWakeWords = map[string]struct{}{
	scoutWakePhraseSecondWord: {}, "scouts": {}, "scott": {},
}

// transcriptStartsWithScoutWakePhrase reports whether a transcript is addressed
// to Scout. ASR output is messy, so tolerate one leading filler word, a bare
// scout token without "hey", and common mishearings of the scout token. Speech
// with no scout token in the first two meaningful words stays rejected.
func transcriptStartsWithScoutWakePhrase(text string) bool {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	if len(words) == 0 {
		return false
	}
	if _, filler := scoutWakeFillerWords[words[0]]; filler && len(words) > 1 {
		words = words[1:]
		if words[0] == "there" && len(words) > 1 {
			words = words[1:]
		}
	}
	if _, wake := scoutWakeWords[words[0]]; wake {
		return true
	}
	if len(words) < 2 || words[0] != scoutWakePhraseFirstWord {
		return false
	}
	_, wake := scoutWakeWords[words[1]]

	return wake
}

func (app *kanbanBoardApp) armScoutVoiceResponse(transcript string) {
	transcript = strings.TrimSpace(transcript)
	wakePhrase := transcriptStartsWithScoutWakePhrase(transcript)
	voiceControl := app.voiceControlEnabled()
	if !wakePhrase && !(voiceControl && transcript != "") {
		// a completed non-wake turn means any armed wake turn is over — unless
		// the wake turn's own response or tool call is still in flight: on the
		// single mixed room stream another speaker's segment (or the user's
		// continuation after a pause) must not silence the armed answer.
		if transcript != "" && !app.scoutTurnInFlight() {
			app.clearScoutVoiceArm()
		}
		return
	}

	now := time.Now()
	app.mu.Lock()
	// the wake transcript often arrives after the tool result it triggered;
	// if a speakable tool result just completed, speak now instead of arming.
	// do_nothing never qualifies (defensive: it is never recorded either) —
	// a stale ambient no-op must not make Scout speak about nothing.
	speakNow := !app.scoutLastToolResultAt.IsZero() &&
		now.Sub(app.scoutLastToolResultAt) <= scoutVoiceRecentToolGrace &&
		app.scoutLastToolResultName != "do_nothing"
	lastToolName := app.scoutLastToolResultName
	if speakNow {
		app.scoutLastToolResultAt = time.Time{}
		app.scoutLastToolResultName = ""
		app.scoutVoiceArmedAt = time.Time{}
		app.scoutVoiceArmedUntil = time.Time{}
		app.scoutSpokenResponse = true
		app.scoutSpokenResponseSent = false
	} else {
		app.scoutVoiceArmedAt = now
		app.scoutVoiceArmedUntil = now.Add(scoutVoiceArmDuration)
	}
	app.mu.Unlock()

	metadata := map[string]any{
		"voiceControl": voiceControl,
		"voiceState":   "hearing",
	}
	statusText := "Scout heard the voice request."
	if wakePhrase {
		metadata["wakePhrase"] = "Hey Scout"
		statusText = "Scout heard the wake phrase."
	}
	broadcastAssistantEvent("status", statusText, metadata)
	if speakNow {
		log.Infof("Scout wake transcript arrived after %s tool result; speaking now", lastToolName)
		app.flushScoutSpokenResponseIfPending()
	}
}

// clearScoutVoiceArmForNewSpeech is intentionally a no-op (the transcript lane
// still calls it on speech_started): speech_started fires for any participant
// in the single mixed room stream, so crosstalk used to disarm nearly every
// armed wake turn. The arm window now clears when a completed non-wake
// transcript arrives or when it expires.
func (app *kanbanBoardApp) clearScoutVoiceArmForNewSpeech() {}

func (app *kanbanBoardApp) clearScoutVoiceArm() {
	app.mu.Lock()
	app.scoutVoiceArmedAt = time.Time{}
	app.scoutVoiceArmedUntil = time.Time{}
	app.mu.Unlock()
}

func (app *kanbanBoardApp) scoutVoiceArmed() bool {
	now := time.Now()
	app.mu.Lock()
	defer app.mu.Unlock()

	return !app.scoutVoiceArmedUntil.IsZero() && !now.After(app.scoutVoiceArmedUntil)
}

// scoutTurnInFlight reports whether the realtime model is mid-response or a
// tool call is still executing — a window in which a completed non-wake
// transcript must not disarm the wake window.
func (app *kanbanBoardApp) scoutTurnInFlight() bool {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.realtimeResponseActive || app.scoutToolCallsInFlight > 0
}

func (app *kanbanBoardApp) beginScoutToolCall() {
	app.mu.Lock()
	app.scoutToolCallsInFlight++
	app.mu.Unlock()
}

func (app *kanbanBoardApp) endScoutToolCall() {
	app.mu.Lock()
	if app.scoutToolCallsInFlight > 0 {
		app.scoutToolCallsInFlight--
	}
	app.mu.Unlock()
}

// markScoutSpokenResponsePending queues a spoken reply for an armed wake turn.
// armedAtStart is the arm state snapshotted when the tool call started, so a
// slow tool (memory answers can take tens of seconds) still speaks after the
// window expires mid-call.
func (app *kanbanBoardApp) markScoutSpokenResponsePending(toolName string, result map[string]any, changed bool, armedAtStart bool) {
	now := time.Now()
	app.mu.Lock()
	defer app.mu.Unlock()

	armed := armedAtStart || (!app.scoutVoiceArmedUntil.IsZero() && !now.After(app.scoutVoiceArmedUntil))
	if !armed {
		// the wake transcript may still be in flight; remember this result so
		// a late arm within scoutVoiceRecentToolGrace can still speak it.
		// only results that would speak on their own merits qualify — and
		// never do_nothing: tool_choice "required" makes it constant ambient
		// churn that would otherwise contaminate the grace buffer and have a
		// wake turn speak about nothing.
		if toolName != "do_nothing" && scoutToolShouldSpeak(toolName, result, changed, false) {
			app.scoutLastToolResultAt = now
			app.scoutLastToolResultName = toolName
		}
		return
	}
	if !scoutToolShouldSpeak(toolName, result, changed, armed) {
		return
	}

	app.scoutVoiceArmedAt = time.Time{}
	app.scoutVoiceArmedUntil = time.Time{}
	app.scoutLastToolResultAt = time.Time{}
	app.scoutLastToolResultName = ""
	app.scoutSpokenResponse = true
	app.scoutSpokenResponseSent = false
}

func scoutToolShouldSpeak(toolName string, result map[string]any, changed bool, armed bool) bool {
	if ok, exists := result["ok"].(bool); exists && !ok {
		return true
	}
	if toolName == "do_nothing" {
		return armed && doNothingReasonShouldSpeak(asString(result["reason"]))
	}
	if armed {
		// an armed hey-scout turn gets a confirmation even for ok no-ops,
		// e.g. moving a card that is already in the requested column
		return true
	}

	// unarmed merits: memory answers and errors (handled above) speak; board
	// tools speak only when they changed something. do_nothing never speaks
	// on its own — it is the marker that nothing scout-addressed happened.
	switch toolName {
	case "answer_memory_question", "cross_meeting_briefing", "get_meeting_detail":
		return true
	case "do_nothing":
		return false
	default:
		return changed
	}
}

func doNothingReasonShouldSpeak(reason string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	if reason == "" {
		return false
	}
	if strings.Contains(reason, "?") {
		return true
	}
	for _, phrase := range []string{"clarify", "which ", "what ", "who ", "say it again", "repeat", "unclear", "not sure", "could not tell", "couldn't tell"} {
		if strings.Contains(reason, phrase) {
			return true
		}
	}
	return false
}

func (app *kanbanBoardApp) flushScoutSpokenResponseIfPending() {
	app.mu.Lock()
	if !app.scoutSpokenResponse || app.scoutSpokenResponseSent {
		app.mu.Unlock()
		return
	}
	if app.realtimeResponseActive {
		app.mu.Unlock()
		return
	}
	app.scoutSpokenResponse = false
	app.scoutSpokenResponseSent = true
	app.mu.Unlock()

	if err := app.SendEvent(map[string]any{
		"type": "response.create",
		"response": map[string]any{
			"output_modalities": []string{"audio"},
			"tool_choice":       "none",
			"instructions":      scoutSpokenResponseInstructions(),
		},
	}); err != nil {
		app.mu.Lock()
		app.scoutSpokenResponse = true
		app.scoutSpokenResponseSent = false
		app.mu.Unlock()
		log.Errorf("Failed to request Scout spoken response: %v", err)
		broadcastAssistantEvent("error", "could not ask Scout to speak", nil)
	}
}

func (app *kanbanBoardApp) retryScoutSpokenResponseAfterActiveResponseError() bool {
	app.mu.Lock()
	defer app.mu.Unlock()

	if !app.scoutSpokenResponseSent {
		return false
	}
	app.scoutSpokenResponse = true
	app.scoutSpokenResponseSent = false
	return true
}

func (app *kanbanBoardApp) markScoutSpokenResponseDelivered() {
	app.mu.Lock()
	app.scoutSpokenResponseSent = false
	app.mu.Unlock()
}

func (app *kanbanBoardApp) markRealtimeResponseActive(active bool) {
	app.mu.Lock()
	app.realtimeResponseActive = active
	app.mu.Unlock()
}

func (app *kanbanBoardApp) isRealtimeResponseActive() bool {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.realtimeResponseActive
}

func scoutSpokenResponseInstructions() string {
	return strings.Join([]string{
		"Speak to the room as Scout.",
		"The user already started this turn with Hey Scout, so answer aloud now.",
		"Do not call tools.",
		"Do not repeat or mention the wake phrase.",
		"If the tool result contains an answer or reason, say it plainly.",
		"If the tool result completed a board update, acknowledge it in one short sentence.",
	}, " ")
}

func (app *kanbanBoardApp) sessionInstructions() string {
	// An active grill replaces the whole operator instruction set: the
	// session.update issued by refreshRealtimeBoardContext("grill start")
	// swaps Scout into the persona, and "grill end" swaps it back.
	if app.grillSessionActive() {
		return app.grillSessionInstructions()
	}
	voiceControlState := "inactive: only clear utterances that start with Hey Scout are addressed to you."
	if app.voiceControlEnabled() {
		voiceControlState = "active: every clear room request is addressed to you until the user turns the shared room voice island off."
	}
	return strings.Join([]string{
		"# Role and Objective\nYou are Scout, the Bonfire OS voice operator for live meetings, app navigation, durable artifacts, meeting memory, and the Kanban board. Keep the app useful with minimal chatter.",
		fmt.Sprintf("# Board\nCurrent Kanban board JSON: %s\nAvailable columns: Backlog, In Progress, Blocked, Done.\nKnown meeting participants: %s.", app.boardContextJSON(), strings.Join(meetingParticipantNames, ", ")),
		fmt.Sprintf("# Domain vocabulary\nUse these exact spellings for names, brands, acronyms, and technical terms: %s. Boot Barn is a known brand; do not write Suit Barn when the user says Boot Barn.", strings.Join(domainVocabulary(), ", ")),
		"# Language\nUsers may say ticket, card, task, issue, or sticky note; treat those as Kanban cards. If a transcript includes a speaker label such as Sean:, do not include the label in the title; use it only as context for owner, notes, or tags.",
		"# Reasoning\nFor direct board operations and simple recall requests, act quickly. For multi-step updates, ambiguous references, or memory questions, reason before choosing tools. Do not spend extra reasoning on unclear audio; ask for clarification through do_nothing.",
		"# Voice control mode\n" + voiceControlState + " This is the shared room Realtime 2 Scout, fed by room audio and heard by everyone in the room. The private Scout chat outside the room is a separate per-user surface; open chat with control_app instead of joining or controlling the room for private conversation requests. When active, answer simple capability, help, navigation, and status questions directly unless a listed tool is needed. When inactive, preserve the shared-room wake phrase behavior. In both modes, ignore background noise, side talk, silence, and filler with do_nothing.",
		"# Preambles\nDo not speak preambles for routine app or board updates. If an addressed request needs memory recall or another tool call that may take noticeable time, say one short acknowledgement immediately before the tool call. Only speak to the room after a tool result when the current voice-control mode says the clear user turn is addressed to you. Otherwise stay silent and use tools.",
		"# Field writing\nWrite card fields as direct project facts, not narration about the user request. Never start titles or notes with phrases like User said, User asked, User requested, or The user wants. Put due dates, key dates, milestone dates, and deadlines in due_date/key_dates or add_key_date; do not put a requested date only in notes. If the user says add Impossible Moments to the board because it is blocked waiting on Erick, use title Impossible Moments, status Blocked, owner Erick, and notes Waiting on Erick.",
		"# Unclear audio\nOnly operate on clear audio or clear typed text. Do not guess proper nouns, brand names, project names, acronyms, owners, or card titles. If the exact entity is unclear, call do_nothing with a concise clarification question instead of creating or updating a card.",
		"# Entity capture\nPreserve exact names, brands, owners, card titles, dates, and project terms. For high-precision identifiers or ambiguous names, normalize only what is clear. If multiple interpretations are plausible, call do_nothing with one clarification question.",
		"# Matching\nUse existing card ids exactly as provided. Match by meaning across title, notes, owner, and tags. Update an existing related card instead of creating a duplicate when the work is already represented. If you are not sure which existing card the user means, call do_nothing with a concise clarification question.",
		"# Status rules\nConcrete first-person status updates are implicit board operations. Started, began, picked up, or working on means In Progress. Shipped, fixed, completed, closed, finished, or resolved means Done. Blocked, waiting, dependent, needs another team, might slip, or at risk means Blocked and should preserve blocker details in notes with blocked, dependency, or risk tags. Park, punt, defer, or move back means Backlog.",
		"# Owner rules\nWhen the speaker names a responsible person, set owner to that exact participant name. Use Unassigned when responsibility is unclear.",
		"# App control\nUse control_app when the user asks you to open or show a Bonfire OS surface. Available surfaces are office, room, chat, artifacts, research, design, grill, board, memory, and files. Files is the shared drive of every uploaded document, deck, and image — open it when the user asks for the files, the drive, or an uploaded document. If the user asks to open the chat app, start a chat, begin a conversational thread, start a discussion thread, or talk to Scout privately, call control_app with tool chat. Opening Chat focuses the user's current private Scout thread; a new chat thread should reset that private conversation unless the user explicitly asks to resume existing context. Do not say you cannot start a thread unless the user specifically asks to create multiple named/persistent chat threads beyond the current Scout thread. If the user asks for a saved artifact, select it by artifact_id when you know the id; otherwise open artifacts.",
		"# Room controls\nUse set_voice_control with enabled=false when the user asks you to stop listening in the room, turn off shared room voice, end the vocal room conversation, close the room voice island, or stop room Realtime. Use set_recording when the user asks to pause, resume, turn on, turn off, start, or stop transcript recording, meeting notes capture, or shared room recording; this switch is room-wide for every participant, and after it changes you should make one short group announcement that recording is on or off. Use archive_meeting when the user asks to send notes, generate meeting notes, archive the meeting, or save the meeting artifact. Browser-local controls such as muting or unmuting the user's microphone, turning their camera on/off, sharing their screen, switching stage layout, pinning a speaker, copying a link, signing in/out, changing passwords, or adding passkeys require that user's browser and device permissions; open the relevant surface with control_app and explain the local action instead of claiming direct control.",
		"# Artifacts, agent threads, and prior meetings\nMeeting transcripts, brain summaries, archives, and OS artifacts are durable memory. Company-OS work should become an artifact when it has a goal, deliverable, status, review gate, or shareable result. If the user asks about prior meetings, artifacts, archives, decisions, transcripts, what was said, what was saved, or any recall question, call answer_memory_question with the user's full question as the query. If the user asks to make or save a quick output, call create_artifact with mode artifacts, research, design, grill, or workflow. If the user asks to kick off research, design work, grill mode, a Codex-style goal loop, a multi-agent loop, or any longer work thread, first state or ask for the vision, then call launch_agent_thread so the artifact is created immediately and the worker can update progress outside the live voice loop. Research, design, grill, and workflow are first-class agent workforce modes; launch_agent_thread is the preferred tool for those longer modes. If the user asks to update, rename, revise, or overwrite a saved artifact and you know its artifact_id, call update_artifact; if you do not know the artifact_id, open artifacts or ask which artifact rather than creating a duplicate. Use publish_artifact only when the user explicitly asks to publish, unpublish, share to dashboard, or remove from dashboard. Latest published artifacts are surfaced on the Office dashboard. " + agentThreadWorkerInstruction(),
		"# Notifications\nUse send_notification when a user asks you to notify the team, alert everyone, or post a visible reminder to the notification bell. Notifications are durable and reach signed-in users outside the room, so prefer audience everyone from this shared room surface. When the user says \"after this meeting/call, remind…\" or asks for the reminder once the meeting is over, pass deliver \"after_meeting\" so it queues until the meeting is archived. Do not use send_notification for routine acknowledgements or board updates.",
		"# Channels\nUse post_to_channel when a user says put/post/share that in #channel or tell the team in a channel; quote their content faithfully, never embellish. Use mention to flag one person by name. create_channel makes a new public team channel, but only from a user's private Scout — tell room requesters to create channels from their private Scout or the chat surface.",
		"# Meeting recap\nUse meeting_recap when someone asks where are we, recap this meeting, or what did I miss in THIS meeting; speak the headline plus 3-5 bullets in under 30 seconds — the full recap is posted to room chat. Use catch_me_up (or meeting_recap with audience me) when one person wants a private catch-up in their notification bell. When the catch-up spans MULTIPLE meetings or days — what did I miss this week, what happened yesterday, catch me up since last week — use cross_meeting_briefing instead: it returns a day-by-day briefing of decisions, action items, topics, and open questions across every meeting in the range; speak the top decisions and blockers only. Use get_meeting_detail with a meeting_id for one past meeting's digest, and pass an anchor id to quote the verbatim exchange.",
		"# Grill sessions\nUse start_grill_session when a user says grill us, pressure-test us, or play investor on a topic; you will switch into the named persona and question the room. Use end_grill_session when anyone asks to stop grilling or stand down — a graded report thread is filed automatically.",
		"# Proposed agent work\nUse propose_codex_task when a user asks you to have someone or an agent take on research, design, grill, planning, or writing work later, such as have someone research comparable exits. It never auto-runs: it posts a proposal card with title, mode, and query that any signed-in user must confirm before the agent thread launches. A separate background workflow ticker may later launch proposals a human has already approved, but proposing itself starts nothing. Prefer launch_agent_thread when the user wants the work started right now in their own chat. Use create_package / attach_to_package / advance_package_stage to manage venture packages — the per-IP mission binders shown in Mission Intelligence; pass package_id on propose_codex_task when the proposed work belongs to a named package.",
		"# Board tools\nUse only the tools listed in this session. If one utterance changes status, notes, owner, tags, and dates for the same existing card, prefer one update_ticket call with all changed fields. Use undo_delete_ticket when the user asks to undo a deletion or restore the last deleted card. Use add_key_date for a pure date or milestone addition to an existing card. Use remove_key_dates when the user asks to remove, clear, erase, or delete key dates from an existing card; set remove_all=true when they do not name specific date labels. Use update_ticket with replace_key_dates=true when the user gives the exact key dates to keep or asks to replace the whole set. Use move_ticket only for a pure status move. Use add_tags only for a pure tag addition. Use create_ticket only when no existing card captures the work. If one transcript contains multiple unrelated operations, call one tool for each operation. Only say an action completed after the tool result succeeds.",
		"# No-op and background audio\nIf the latest audio is silence, background noise, side conversation, filler, wrap-up, or a handoff with no concrete app action, board operation, artifact request, or recall request, call do_nothing with a short reason. Do not say I'm here, I didn't catch that, or take your time.",
		"# Wake phrase\nWhen voice control mode is inactive, only speak to the room when the user's clear utterance starts with the exact wake phrase Hey Scout. Treat Hey Scout as an address to you, not as content to save on the board. If the utterance does not start with Hey Scout, stay silent after tool calls.",
		"# Verbosity\nPrefer tools over text replies. Keep spoken responses to one short sentence unless the user asks for a memory answer; for memory answers, give the headline first and only the most useful details.",
	}, "\n\n")
}

func (app *kanbanBoardApp) boardContextJSON() string {
	raw, err := json.Marshal(app.snapshotState().Cards)
	if err != nil {
		return "[]"
	}

	return string(raw)
}

type orchestratorToolPolicy struct {
	RequiredAuthority string
	SideEffect        string
}

// orchestratorToolPolicies is the code-level capability registry adjacent to
// the master Kanban tool definitions. Tools absent here are never exposed to
// the orchestrator; every present tool declares both authority and effects.
var orchestratorToolPolicies = map[string]orchestratorToolPolicy{
	"create_ticket": {codexJobAuthorityWorkspaceWrite, "board_write"}, "move_ticket": {codexJobAuthorityWorkspaceWrite, "board_write"},
	"add_tags": {codexJobAuthorityWorkspaceWrite, "board_write"}, "add_key_date": {codexJobAuthorityWorkspaceWrite, "board_write"},
	"remove_key_dates": {codexJobAuthorityWorkspaceWrite, "board_write"}, "update_ticket": {codexJobAuthorityWorkspaceWrite, "board_write"},
	"create_artifact": {codexJobAuthorityWorkspaceWrite, "artifact_write"}, "update_artifact": {codexJobAuthorityWorkspaceWrite, "artifact_write"},
	"answer_memory_question": {codexJobAuthorityReadOnly, "read"}, "note_for_the_record": {codexJobAuthorityWorkspaceWrite, "memory_write"},
	"cross_meeting_briefing": {codexJobAuthorityReadOnly, "read"}, "get_meeting_detail": {codexJobAuthorityReadOnly, "read"},
	"create_package": {codexJobAuthorityWorkspaceWrite, "package_write"}, "attach_to_package": {codexJobAuthorityWorkspaceWrite, "package_write"},
	"advance_package_stage": {codexJobAuthorityWorkspaceWrite, "package_write"}, "send_notification": {codexJobAuthorityWorkspaceWrite, "notification_write"},
	"do_nothing": {codexJobAuthorityReadOnly, "none"}, "company_financial_snapshot": {codexJobAuthorityReadOnly, "external_read"},
	"financial_comps": {codexJobAuthorityReadOnly, "external_read"}, "fiscal_api_docs": {codexJobAuthorityReadOnly, "external_read"},
	"fiscal_data_query": {codexJobAuthorityReadOnly, "external_read"},
}

func (app *kanbanBoardApp) kanbanTools() []map[string]any {
	statusProperty := map[string]any{
		"type":        "string",
		"description": "Kanban column for the ticket.",
		"enum":        []string{"Backlog", "In Progress", "Blocked", "Done"},
	}
	tagsProperty := map[string]any{
		"type":        "array",
		"description": "Short labels that capture people, area, state, or risk. Preserve exact domain spellings for proper nouns and acronyms. Use blocked/dependency/risk tags for blockers when appropriate.",
		"items":       map[string]any{"type": "string"},
	}
	tagsToAddProperty := map[string]any{
		"type":        "array",
		"description": "Tags to add to the existing card. Existing tags are preserved. Preserve exact domain spellings for proper nouns and acronyms.",
		"items":       map[string]any{"type": "string"},
	}
	dueDateProperty := map[string]any{
		"type":        "string",
		"description": "Primary due date or deadline text, such as May 24, tomorrow, or 2026-05-24. Use only when the user explicitly gives a due date or deadline.",
	}
	keyDateProperty := map[string]any{
		"type":        "object",
		"description": "A key date or milestone on the card.",
		"properties": map[string]any{
			"label": map[string]any{"type": "string", "description": "Short milestone label without the date, such as due, investor PDF, launch, review, or kickoff."},
			"date":  map[string]any{"type": "string", "description": "Date text exactly as resolved from the user, such as May 24, tomorrow, or 2026-05-24."},
		},
		"required":             []string{"label", "date"},
		"additionalProperties": false,
	}
	keyDatesProperty := map[string]any{
		"type":        "array",
		"description": "Key dates or milestones to add or update on the card. Preserve useful existing dates unless replace_key_dates is true. Use an empty array with replace_key_dates=true to clear all key dates.",
		"items":       keyDateProperty,
	}
	ownerProperty := map[string]any{
		"type":        "string",
		"description": "Responsible participant when the user names an owner or the work clearly belongs to someone.",
		"enum":        append([]string{"Unassigned"}, meetingParticipantNames...),
	}
	appToolProperty := map[string]any{
		"type":        "string",
		"description": "Bonfire OS surface to open. Use chat when the user asks to open the chat app, start a conversational thread, begin a discussion thread, or talk to Scout privately. Use files for uploaded documents, decks, and images — the shared file drive.",
		"enum":        []string{"office", "room", "chat", "artifacts", "research", "design", "grill", "board", "memory", "files"},
	}
	artifactModeProperty := map[string]any{
		"type":        "string",
		"description": "Durable artifact workspace to use.",
		"enum":        []string{"artifacts", "research", "design", "grill", "workflow"},
	}

	return []map[string]any{
		{
			"type":        "function",
			"name":        "create_ticket",
			"description": "Create a new Kanban ticket/card for explicit requests or implicit meeting status updates such as shipped, started, or blocked work.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":     map[string]any{"type": "string", "description": "Concise title for the work, without speaker prefixes such as Sean:. Preserve exact proper nouns and domain spellings; if unsure, use do_nothing instead."},
					"notes":     map[string]any{"type": "string", "description": "Direct project facts only. Include blocker, dependency, or schedule-risk details, but do not narrate the command or write phrases like User requested, User said, or asked to add this to the board. Do not put due dates or key dates here when they can be represented in due_date/key_dates. Preserve exact proper nouns and domain spellings; if unsure, use do_nothing instead."},
					"owner":     ownerProperty,
					"tags":      tagsProperty,
					"status":    statusProperty,
					"due_date":  dueDateProperty,
					"key_dates": keyDatesProperty,
				},
				"required":             []string{"title", "notes", "owner", "tags", "status"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "move_ticket",
			"description": "Move an existing Kanban ticket/card to another column, including Blocked when work is waiting on a dependency.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"card_id": map[string]any{"type": "string", "description": "Existing board card id."},
					"status":  statusProperty,
				},
				"required":             []string{"card_id", "status"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "add_tags",
			"description": "Add one or more tags to an existing Kanban ticket/card without removing existing tags.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"card_id": map[string]any{"type": "string", "description": "Existing board card id."},
					"tags":    tagsToAddProperty,
				},
				"required":             []string{"card_id", "tags"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "add_key_date",
			"description": "Add or update one key date, milestone, due date, or deadline on an existing Kanban ticket/card without changing notes.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"card_id": map[string]any{"type": "string", "description": "Existing board card id."},
					"label":   map[string]any{"type": "string", "description": "Short label for the milestone, such as due, investor PDF, launch, review, or kickoff. Do not include the date."},
					"date":    map[string]any{"type": "string", "description": "Date text exactly as resolved from the user, such as May 24, tomorrow, or 2026-05-24."},
				},
				"required":             []string{"card_id", "label", "date"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "remove_key_dates",
			"description": "Remove one or more key dates, milestones, due dates, or deadlines from an existing Kanban ticket/card.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"card_id":    map[string]any{"type": "string", "description": "Existing board card id."},
					"labels":     map[string]any{"type": "array", "description": "Specific key date labels to remove, such as due, investor PDF, launch, review, or kickoff. Omit or leave empty when remove_all is true.", "items": map[string]any{"type": "string"}},
					"remove_all": map[string]any{"type": "boolean", "description": "Set true to remove every key date from the card when the user asks to clear, erase, or remove key dates without naming labels."},
				},
				"required":             []string{"card_id"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "update_ticket",
			"description": "Update one existing Kanban ticket/card atomically. Prefer this when one utterance changes status, owner, notes, title, tags, due date, or key dates for the same card.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"card_id":   map[string]any{"type": "string", "description": "Existing board card id."},
					"title":     map[string]any{"type": "string", "description": "Replacement title, when the existing title should be made clearer. Preserve exact proper nouns and domain spellings; if unsure, use do_nothing instead."},
					"notes":     map[string]any{"type": "string", "description": "Full replacement notes as direct project facts. Preserve useful existing notes while adding the new context, but do not narrate the command or write phrases like User requested, User said, or asked to update this card. Do not put due dates or key dates here when they can be represented in due_date/key_dates. Preserve exact proper nouns and domain spellings; if unsure, use do_nothing instead."},
					"owner":     ownerProperty,
					"tags":      tagsToAddProperty,
					"status":    statusProperty,
					"due_date":  dueDateProperty,
					"key_dates": keyDatesProperty,
					"replace_key_dates": map[string]any{
						"type":        "boolean",
						"description": "Set true only when the user asks to replace the whole key-date set or gives the exact key dates to keep. With key_dates=[], this clears all key dates.",
					},
				},
				"required":             []string{"card_id"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "delete_ticket",
			"description": "Delete an existing Kanban ticket/card.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"card_id": map[string]any{"type": "string", "description": "Existing board card id."},
				},
				"required":             []string{"card_id"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "undo_delete_ticket",
			"description": "Restore the most recently deleted Kanban ticket/card. Use when the user asks to undo a deletion or restore the last deleted card.",
			"parameters": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "control_app",
			"description": "Open or focus a Bonfire OS surface such as artifacts, memory, chat, research, design, grill, board, room, or office. For requests to open chat, start a chat, start a conversational thread, begin a discussion thread, or talk privately to Scout, open chat; the current Chat app has one private Scout thread that can be reset for a new conversation. Use artifact_id when selecting a known saved artifact.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tool":        appToolProperty,
					"artifact_id": map[string]any{"type": "string", "description": "Optional saved artifact id to select after opening artifacts."},
					"also_open": map[string]any{
						"type":        "array",
						"description": "Optional extra surfaces to open alongside tool, in order — use when the user asks to open several things at once (e.g. the market map artifact AND the deck).",
						"items":       map[string]any{"type": "string", "enum": []string{"office", "room", "chat", "artifacts", "research", "design", "grill", "board", "memory", "files"}},
					},
				},
				"required":             []string{"tool"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "set_voice_control",
			"description": "Turn the shared room Realtime 2 voice island on or off. Use enabled=false for requests like stop listening in the room, turn off room voice, end the vocal room conversation, close the room waveform island, or stop room Realtime.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"enabled": map[string]any{"type": "boolean", "description": "false to stop shared room Realtime voice listening and close the room voice island; true to keep room voice control active."},
				},
				"required":             []string{"enabled"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "set_recording",
			"description": "Pause or resume the room-wide transcript recording and meeting notes capture for every participant. Use this for requests like pause recording, resume recording, turn notes capture on, or stop the transcript, then announce the on/off state to the group. This is not local mic, camera, or screen-share control.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"enabled": map[string]any{"type": "boolean", "description": "true to resume or turn on room-wide transcript recording; false to pause or turn it off for the room."},
				},
				"required":             []string{"enabled"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "archive_meeting",
			"description": "Generate and save meeting notes plus a meeting artifact, equivalent to the Send notes action. Use when the user asks to send notes, generate notes, archive the meeting, save meeting notes, or create the meeting artifact.",
			"parameters": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "create_artifact",
			"description": "Create a quick durable Bonfire OS artifact from explicit content the user wants saved now. Do not use this to kick off research, design, grill, or workflow work; use launch_agent_thread for those longer worker requests.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"mode":    artifactModeProperty,
					"query":   map[string]any{"type": "string", "description": "The user's quick saved-artifact request or title."},
					"content": map[string]any{"type": "string", "description": "Final artifact content to save. Provide this when the user supplied or approved the content."},
				},
				"required":             []string{"mode", "query"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "launch_agent_thread",
			"description": "Launch a Scout agent-workforce thread for research, investigation, sourcing, design, grill, pressure-test, planning, or workflow requests. This creates a Chat work thread immediately, saves the backing artifact, and lets the worker update progress outside the live Realtime voice loop.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"mode":  artifactModeProperty,
					"query": map[string]any{"type": "string", "description": "The user's vision, goal, research/design request, or Codex-style workflow objective."},
				},
				"required":             []string{"mode", "query"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "update_artifact",
			"description": "Update an existing saved Bonfire OS artifact when the artifact_id is known. Use for requests to rename, revise, edit, or overwrite a specific saved artifact. If the artifact_id is unknown, open artifacts or ask which artifact instead of creating a duplicate.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"artifact_id": map[string]any{"type": "string", "description": "Existing saved artifact id to update."},
					"title":       map[string]any{"type": "string", "description": "Optional replacement artifact title."},
					"content":     map[string]any{"type": "string", "description": "Optional full replacement artifact content. Omit only for title-only renames."},
				},
				"required":             []string{"artifact_id"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "publish_artifact",
			"description": "Publish or unpublish a saved artifact so the latest published artifacts can appear on the Office dashboard. Use only after an explicit publish, unpublish, share to dashboard, or remove from dashboard request.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"artifact_id": map[string]any{"type": "string", "description": "Existing saved artifact id to publish or unpublish."},
					"published":   map[string]any{"type": "boolean", "description": "true to publish to the dashboard; false to remove from published dashboard surfaces."},
				},
				"required":             []string{"artifact_id", "published"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "answer_memory_question",
			"description": "Answer a user question by recalling the saved speaker-attributed transcript, brain write-ups, archives, and meeting memory.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "The user's recall question or memory search query."},
				},
				"required":             []string{"query"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "portfolio_health",
			"description": "Summarize the state of the venture portfolio for the user — every package's stage, readiness, freshness, and open gaps, leading with anything stale. Use when the user asks how the portfolio, the book, or the packages are doing (\"how's the portfolio?\"). Read-only.",
			"parameters": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "company_financial_snapshot",
			"description": "Grounded fundamentals for one public company: identity, latest annual revenue and net income with filing citation links, and valuation multiples. Read-only, fiscal.ai-backed; requires FISCAL_AI_API_KEY.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"company": map[string]any{"type": "string", "description": "Ticker, EXCHANGE_TICKER key such as NASDAQ_NFLX, or company name."},
				},
				"required":             []string{"company"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "financial_comps",
			"description": "Peer comparables for one public company: the peer universe with the latest valuation multiples per peer, shaped for a markdown table. Read-only, fiscal.ai-backed; requires FISCAL_AI_API_KEY.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"company": map[string]any{"type": "string", "description": "Subject company: ticker, EXCHANGE_TICKER key, or name."},
					"ratio_ids": map[string]any{
						"type":        "array",
						"description": "fiscal.ai ratio ids to compare, such as ratio_ev_to_ebitda; omit for the defaults.",
						"items":       map[string]any{"type": "string"},
					},
					"peer_limit": map[string]any{"type": "integer", "description": "Maximum peers to include; omit for 6, capped at 12."},
				},
				"required":             []string{"company"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "fiscal_api_docs",
			"description": "Typed fiscal.ai API docs for planning a custom fiscal_data_query. Read-only, fiscal.ai-backed; requires FISCAL_AI_API_KEY.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"topic": map[string]any{
						"type":        "string",
						"description": "index (the default) is the compact function list; full is the complete typed docs.",
						"enum":        []string{"index", "full"},
					},
				},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "fiscal_data_query",
			"description": "Run custom JS against the fiscal.ai sandbox for financial data the typed tools do not cover. Read-only, fiscal.ai-backed; requires FISCAL_AI_API_KEY.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"code":      map[string]any{"type": "string", "description": "An async arrow function `async () => {...}` calling codemode.<fn>({...}) and emitting results via console.log (the only return channel). Check fiscal_api_docs first."},
					"max_chars": map[string]any{"type": "integer", "description": "Truncate the returned text to this many characters; default 20000."},
				},
				"required":             []string{"code"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "propose_codex_task",
			"description": "Propose a Codex agent task as a confirmable proposal card. Use when the user asks to have someone or an agent research, design, grill, plan, or write something later; this never launches work itself — a human must confirm the proposal card before the agent thread starts.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":      map[string]any{"type": "string", "description": "Short human title for the proposed task."},
					"mode":       artifactModeProperty,
					"query":      map[string]any{"type": "string", "description": "What the agent should produce once a human confirms the proposal."},
					"card_id":    map[string]any{"type": "string", "description": "id of the existing board card this task delivers; omit if none."},
					"package_id": map[string]any{"type": "string", "description": "id or exact name of the venture package this task belongs to; omit if none."},
					"thread_id":  map[string]any{"type": "string", "description": "id of the public channel this proposal originated in; the finished work is delivered back there. Omit if none."},
				},
				"required":             []string{"title", "mode", "query"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "create_package",
			"description": "Create a venture package — a first-class IP mission binder that collects the artifacts, board cards, decisions, and channel moving one piece of IP through the pipeline.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":   map[string]any{"type": "string", "description": "Unique package name, usually the IP or venture name."},
					"thesis": map[string]any{"type": "string", "description": "One-line thesis for why this IP wins."},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "attach_to_package",
			"description": "Attach an existing artifact, board card, channel, or decision to a venture package so the binder stays the one place holding the IP's moving parts.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"package":   map[string]any{"type": "string", "description": "Package name or id."},
					"ref_type":  map[string]any{"type": "string", "description": "What kind of object is being attached.", "enum": []string{"artifact", "card", "channel", "decision"}},
					"ref_id":    map[string]any{"type": "string", "description": "Exact id of the object to attach; preferred when known."},
					"ref_title": map[string]any{"type": "string", "description": "Title to fuzzy-resolve within ref_type when the exact id is unknown."},
				},
				"required":             []string{"package", "ref_type"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "advance_package_stage",
			"description": "Advance a venture package to its next pipeline stage, or set an explicit stage (forward or back).",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"package": map[string]any{"type": "string", "description": "Package name or id."},
					"stage":   map[string]any{"type": "string", "description": "Explicit stage to set; omit to step to the next stage.", "enum": packageStages},
				},
				"required":             []string{"package"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "send_notification",
			"description": "Post a persistent Bonfire OS notification to the notification bell. Use this for deliberate notify, alert, or remind requests such as notify the team, alert everyone, or remind me; do not use it for routine acknowledgements.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{"type": "string", "description": "Short notification text to deliver."},
					"kind": map[string]any{
						"type":        "string",
						"description": "Notification tone.",
						"enum":        []string{"info", "task", "agent", "chat", "alert"},
					},
					"audience": map[string]any{
						"type":        "string",
						"description": "everyone posts to all signed-in users; me notifies only the requesting user.",
						"enum":        []string{"everyone", "me"},
					},
					"tool": map[string]any{
						"type":        "string",
						"description": "Optional Bonfire OS surface to open when the notification is clicked.",
						"enum":        []string{"office", "room", "chat", "artifacts", "research", "design", "grill", "board", "memory", "files"},
					},
					"deliver": map[string]any{
						"type":        "string",
						"description": "after_meeting queues the notification until the meeting is archived; now (the default) delivers immediately.",
						"enum":        []string{"now", "after_meeting"},
					},
				},
				"required":             []string{"text", "kind", "audience"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "post_to_channel",
			"description": "Post a message into an existing public team channel on behalf of the user. Quote the user's content faithfully; never embellish. This posts only — it never summons Scout to answer in the channel.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"channel": map[string]any{"type": "string", "description": "Channel name; a leading '#' is tolerated."},
					"text":    map[string]any{"type": "string", "description": "The message to post, quoting the user's words faithfully."},
					"mention": map[string]any{
						"type":        "string",
						"description": "Optional participant name to flag with a targeted notification.",
						"enum":        append([]string{""}, meetingParticipantNames...),
					},
				},
				"required":             []string{"channel", "text"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "create_channel",
			"description": "Create a new public team channel (a shared chat thread every signed-in user can read). Only available from a user's private Scout — the shared room voice cannot create channels.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string", "description": "Channel name; a leading '#' is tolerated."},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "meeting_recap",
			"description": "Build a fresh recap of the live meeting: forces a meeting-brain pass over the newest transcripts, posts the full recap to room chat (audience room, the default), or lands it in the requesting user's notification bell (audience me for catch-me-up requests). Speak the headline after the result arrives.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"audience": map[string]any{
						"type":        "string",
						"description": "room posts the recap to room chat for everyone; me delivers it privately to the requesting user's bell.",
						"enum":        []string{"room", "me"},
					},
					"focus": map[string]any{"type": "string", "description": "Optional topic to emphasize when speaking the recap."},
				},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "catch_me_up",
			"description": "Catch one person up on the live meeting: same as meeting_recap with audience me — a fresh recap lands in the requesting user's notification bell and you read the headline aloud.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"focus": map[string]any{"type": "string", "description": "Optional topic to emphasize when speaking the recap."},
				},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "cross_meeting_briefing",
			"description": "Comprehensive day-by-day briefing across ALL meetings in a date range — decisions, action items, topics, open questions, and themes, importance-ranked, with decision-ledger entries quoted verbatim. Use for what did I miss this week / yesterday, catch me up on the last few days, or any catch-up spanning more than the live meeting. Composed from stored digests; falls back to summarizing raw meeting memory when digests do not cover the range.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"range":     map[string]any{"type": "string", "description": "Natural range phrase: today, yesterday, this week, last week, or last N hours/minutes. Defaults to this week."},
					"start_day": map[string]any{"type": "string", "description": "Optional explicit start day YYYY-MM-DD (local time); overrides range."},
					"end_day":   map[string]any{"type": "string", "description": "Optional explicit end day YYYY-MM-DD, inclusive; defaults to start_day."},
				},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "get_meeting_detail",
			"description": "Drill into ONE past meeting: returns its digest (decisions, action items, topics, open questions with transcript anchors) plus its newest brain write-ups. Pass anchor with a transcript entry id (from a briefing or digest anchor) to quote the verbatim exchange around that moment.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"meeting_id": map[string]any{"type": "string", "description": "Meeting id, e.g. meeting-20260706-021630 (from a briefing's meeting list)."},
					"anchor":     map[string]any{"type": "string", "description": "Optional transcript entry id to quote verbatim with surrounding lines."},
				},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "start_grill_session",
			"description": "Start a grill session: Scout switches into a pressure-test persona and questions the room on the topic until end_grill_session. Use when a user says grill us, pressure-test us, or play investor.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"topic":   map[string]any{"type": "string", "description": "What the room is being grilled on."},
					"persona": map[string]any{"type": "string", "description": "Optional persona to adopt; defaults to a skeptical seed-stage investor."},
				},
				"required":             []string{"topic"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "end_grill_session",
			"description": "End the active grill session, restore normal Scout behavior, and file the graded grill report as an agent thread. Use immediately when anyone says end the grill, stop grilling, or Scout, stand down.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{"type": "string", "description": "Optional reason the grill ended."},
				},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "start_private_grill",
			"description": "Start a private, one-on-one grill: YOU (Scout) pressure-test the single dashboard user by voice. Use when the user says grill me, pressure-test me, or play investor with me. Returns the persona instructions the browser applies to this private session — this does NOT grill the shared room. Optionally name a package to ground the question bank in its artifacts and decisions.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"package": map[string]any{"type": "string", "description": "Optional package name or id to ground the grill in (its artifacts, rights/economics assumptions, and decisions become the question bank)."},
					"persona": map[string]any{"type": "string", "description": "Optional persona to adopt; defaults to a prepared, skeptical investor who has read the whole package."},
				},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "end_private_grill",
			"description": "End the private grill, restore normal private-voice behavior, and file the graded scorecard as a grill agent thread. Call this after you deliver the spoken readiness report, when the user says end the grill, stop, that's enough, or stand down. Pass the package (if one was named) and a short Q&A transcript so the scorecard carries a valid READINESS line.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"package":    map[string]any{"type": "string", "description": "The package the grill was grounded in, if any — the scorecard attaches to it and updates the readiness dial."},
					"persona":    map[string]any{"type": "string", "description": "The persona you grilled as; recorded on the report."},
					"transcript": map[string]any{"type": "string", "description": "A short Q&A transcript of the grill (questions asked and answers given) so the filed scorecard can grade each answer."},
					"reason":     map[string]any{"type": "string", "description": "Optional reason the grill ended."},
					"readiness":  map[string]any{"type": "number", "description": "The overall readiness score you assigned this pitch out of 10 (one decimal). Powers the live scorecard reveal."},
					"verdict":    map[string]any{"type": "string", "description": "One sharp closing line for the scorecard (e.g. 'Strong on story. Thin on the moat.')."},
					"scores": map[string]any{
						"type":        "array",
						"description": "Per-dimension scores you graded live, in the order you want them shown.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"label": map[string]any{"type": "string", "description": "Dimension name, e.g. Evidence, Clarity, Confidence."},
								"score": map[string]any{"type": "number", "description": "Score for this dimension out of 10."},
							},
							"required":             []string{"label", "score"},
							"additionalProperties": false,
						},
					},
				},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "read_thread_aloud",
			"description": "Fetch the recent text of a channel, a private thread, a saved artifact, or the user's notifications so you can read it aloud in your spoken turn. Use for requests like read me the latest in #dealflow, what did the fintech thread say, or read my notifications. This returns text only; you speak it.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"description": "What to read: channel (a public #channel), private_thread (one of the user's Scout threads), artifact (a saved artifact id), or notifications (the user's bell).",
						"enum":        []string{"channel", "private_thread", "artifact", "notifications"},
					},
					"ref":   map[string]any{"type": "string", "description": "Channel name, private thread id, or artifact id. Ignored for notifications."},
					"limit": map[string]any{"type": "integer", "description": "How many recent messages to read. Default 3."},
				},
				"required":             []string{"target", "ref"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "start_chat_as_user",
			"description": "Start (or address) a channel or a private thread and post a message into it on the user's behalf, quoting them faithfully. The post is always disclosed as via Scout — read the draft back and confirm before posting. Use for requests like start a thread with the team about X and say Y, or post to #dealflow as me.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"audience": map[string]any{
						"type":        "string",
						"description": "channel starts/addresses a public team #channel; thread starts/addresses one of the user's private Scout threads.",
						"enum":        []string{"channel", "thread"},
					},
					"name": map[string]any{"type": "string", "description": "Channel or thread name to create or address; a leading '#' is tolerated."},
					"text": map[string]any{"type": "string", "description": "The message to post, quoting the user's words faithfully."},
					"disclose": map[string]any{
						"type":        "boolean",
						"description": "Always true. Disclosure is stamped server-side regardless of this value; the message is always shown as posted via Scout.",
					},
				},
				"required":             []string{"audience", "name", "text"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "initiate_goal",
			"description": "Launch the multi-step /goal pipeline: Scout decomposes the objective, runs the subtasks, reviews against the goal, gates before shipping, and reports. Use for a real end-to-end objective (\"package the Aurora IP into an investor one-pager and deck\"), not a single research or design ask (use launch_agent_thread for those).",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"objective": map[string]any{"type": "string", "description": "The end-to-end goal, in the user's words."},
					"package":   map[string]any{"type": "string", "description": "Optional package name or id to file the result under."},
					"tool": map[string]any{
						"type":        "string",
						"enum":        packagingRunPresetIDs(),
						"description": "Optional run-type preset id to run the goal against — shapes the output contract and the ship gate. Pick the enum id that best matches the ask; omit for a free-form goal.",
					},
					"authority_hint": map[string]any{
						"type":        "string",
						"description": "read_only for research/analysis goals; workspace_write when the goal produces or edits work. external_write is never available here — it is earned only at the ship gate with human approval.",
						"enum":        []string{"read_only", "workspace_write"},
					},
				},
				"required":             []string{"objective"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "organize_files",
			"description": "File uploaded files and finished deliverables into a named folder on the Files surface. Fuzzy-matches each given name fragment against the visible file list (case-insensitive substring) and creates the folder first when it does not exist yet. Use for requests like put the Samsung reports in a Samsung folder.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"folderName": map[string]any{"type": "string", "description": "Folder to file the matches under; created when missing unless createIfMissing is false."},
					"fileNames": map[string]any{
						"type":        "array",
						"description": "Display-name fragments to match, one per file or group (\"samsung\" matches \"Samsung TV Plus teardown\").",
						"items":       map[string]any{"type": "string"},
					},
					"createIfMissing": map[string]any{"type": "boolean", "description": "Create the folder when it does not exist yet. Default true."},
				},
				"required":             []string{"folderName"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "save_to_files",
			"description": "Save finished deliverables (research reports, decks, goal outputs) onto the Files surface — deliverables no longer appear there automatically, so the team can only find them once saved. Fuzzy-matches each given name fragment against deliverable titles (case-insensitive substring) and can file the saved rows under a folder in the same step. Use for requests like save the Samsung report to Files, or save the teardown into a Diligence folder.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"fileNames": map[string]any{
						"type":        "array",
						"description": "Deliverable title fragments to match and save, one per report or group (\"samsung\" matches \"Samsung TV Plus teardown\").",
						"items":       map[string]any{"type": "string"},
					},
					"folderName": map[string]any{"type": "string", "description": "Optional folder to file the saved deliverables under; created when missing."},
				},
				"required":             []string{"fileNames"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "note_for_the_record",
			"description": "File a durable, author-certain note into the company memory so it grounds future recall — the deliberate \"for the record\" path. Use when the user explicitly wants a statement, stance, or decision remembered (\"note for the record: we never ship Q4 launches\", \"remember that Tim is against the Samsung deal\", \"put on record that we decided to lead with Ball Dogs\"). The note is company-recallable even when filed from a private thread — the explicit request IS the consent. Set kind to decision for an explicit decision or stance (recorded as a proposed decision the team can ratify); leave it as note otherwise.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"note":  map[string]any{"type": "string", "description": "The statement to remember, self-contained and in the user's own framing."},
					"topic": map[string]any{"type": "string", "description": "Optional short subject label the note is about (\"Samsung deal\", \"Q4 launches\")."},
					"kind": map[string]any{
						"type":        "string",
						"description": "note for a plain fact/reminder (default); decision for an explicit decision or stated position, recorded as a proposed decision on the ledger.",
						"enum":        []string{"note", "decision"},
					},
					"owner": map[string]any{"type": "string", "description": "Optional person the note or stance is attributed to when it is about someone other than the speaker (\"Tim\"); used as the decision maker for kind=decision."},
				},
				"required":             []string{"note"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "do_nothing",
			"description": "Use this when the user is not asking to operate on the Kanban board, is only wrapping up, or says a handoff phrase like That's it from me.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{"type": "string"},
				},
				"required":             []string{"reason"},
				"additionalProperties": false,
			},
		},
	}
}

func (app *kanbanBoardApp) handleRealtimeEvent(raw []byte) {
	var event kanbanRealtimeEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		log.Errorf("Failed to parse OpenAI Realtime event: %v", err)
		return
	}

	switch event.Type {
	case "session.created", "session.updated":
		recordCapabilitySuccess(capabilityScout, time.Now().UTC())
		broadcastAssistantEvent("status", "OpenAI Realtime session configured", map[string]any{"eventType": event.Type})
	case "error":
		if event.Error != nil {
			recordCapabilityFailure(capabilityScout, time.Now().UTC(), fmt.Errorf("%s", firstNonEmptyString(event.Error.Code, "realtime_error")))
			log.Errorf("OpenAI Realtime error code=%s message=%s", event.Error.Code, event.Error.Message)
			if event.Error.Code == "session_expired" {
				broadcastAssistantEvent("status", "OpenAI Realtime session expired; reconnecting", nil)
				go app.restartRealtimePeer(event.Error.Message)
				return
			}
			if isRealtimeActiveResponseError(event) {
				shouldRetrySpeech := app.retryScoutSpokenResponseAfterActiveResponseError()
				broadcastAssistantEvent("status", "Scout is still finishing the last turn.", map[string]any{"code": event.Error.Code})
				if shouldRetrySpeech && !app.isRealtimeResponseActive() {
					app.flushScoutSpokenResponseIfPending()
				}
				return
			}
			// Unrecognized server errors stay off the chat feed: only
			// kind=query/answer/error render there, so downgrade to a short
			// status line and keep the raw message in metadata + server logs.
			broadcastKanbanEvent("status", "assistant hit a server error")
			broadcastAssistantEvent("status", "assistant hit a server error", map[string]any{"code": event.Error.Code, "message": event.Error.Message})
		}
	case "conversation.item.input_audio_transcription.completed":
		// W0-5: the voice peer's in-session transcription bills at the
		// transcription model's rates regardless of which lane persists, so
		// meter every segment that reports usage.
		app.recordVoicePeerTranscriptionUsage(event)
		app.armScoutVoiceResponse(event.Transcript)
		if !app.transcriptionLaneConnected() {
			app.recordVoicePeerTranscriptSegment(event, "completed")
			app.rememberTranscript(officeRoomID, event, "scout_realtime", app.currentRealtimeModel())
		}
	case "conversation.item.input_audio_transcription.failed":
		// A6: mirror the .completed gate. When the Scout peer is the persisting
		// session (lane down), a failed segment still froze a window at its commit;
		// pop it (discard) so the FIFO stays aligned and later transcripts keep their
		// correct speaker. When the lane is up it owns the FIFO, so skip here.
		if !app.transcriptionLaneConnected() {
			// Every .failed here is speech the brain never heard — feed the
			// W0 transcript funnel so drop-off is countable per sitting.
			app.recordVoicePeerTranscriptSegment(event, "failed")
			app.popPendingAttributionWindow()
		}
	case "conversation.item.input_audio_transcription.delta":
		if text := canonicalizeBoardText(event.Delta); text != "" {
			broadcastAssistantEvent("transcript", "hearing: "+text, map[string]any{"eventType": event.Type})
		}
	case "input_audio_buffer.speech_started":
		if !app.transcriptionLaneConnected() {
			app.noteRealtimeSpeechStarted()
		}
		broadcastAssistantEvent("audio", "assistant detected speech", map[string]any{"eventType": event.Type, "voiceState": "hearing"})
	case "input_audio_buffer.speech_stopped":
		if !app.transcriptionLaneConnected() {
			app.noteRealtimeSpeechStopped()
		}
		broadcastAssistantEvent("audio", "assistant detected silence", map[string]any{"eventType": event.Type, "voiceState": "listening"})
	case "input_audio_buffer.committed":
		// A6: when the Scout peer is the persisting session (lane down), freeze the
		// attribution window at commit so its transcription.completed resolves the
		// speaker from this turn's boundaries, not whatever the next turn overwrote.
		// When the lane is up it owns persistence + freezing, so skip here.
		if !app.transcriptionLaneConnected() {
			app.freezeAttributionWindowAtCommit()
		}
		broadcastAssistantEvent("audio", "assistant committed a speech turn", map[string]any{"eventType": event.Type, "voiceState": "thinking"})
	case "response.created":
		app.markRealtimeResponseActive(true)
		broadcastAssistantEvent("audio", "Scout is thinking", map[string]any{"eventType": event.Type, "voiceState": "thinking"})
	case "response.output_audio_transcript.done":
		if text := canonicalizeBoardText(firstNonEmptyString(event.Transcript, event.Text)); text != "" {
			app.markScoutSpokenResponseDelivered()
			broadcastAssistantEvent("answer", text, map[string]any{"eventType": event.Type, "voiceState": "talking"})
		}
	case "response.output_text.done":
		if text := canonicalizeBoardText(firstNonEmptyString(event.Text, event.Transcript)); text != "" {
			app.markScoutSpokenResponseDelivered()
			broadcastAssistantEvent("answer", text, map[string]any{"eventType": event.Type, "voiceState": "talking"})
		}
	case "response.output_item.done":
		if event.Item != nil && event.Item.Type == "function_call" {
			app.handleToolCall(*event.Item, true)
		}
	case "response.function_call_arguments.done":
		app.handleToolCall(realtimeFunctionCallFromArgumentsDone(event), true)
	case "response.done":
		app.markRealtimeResponseActive(false)
		hadFunctionCall := false
		if event.Response != nil {
			interrupted := isInterruptedRealtimeResponseStatus(event.Response.Status)
			if interrupted && !strings.EqualFold(strings.TrimSpace(event.Response.Status), "cancelled") {
				recordCapabilityFailure(capabilityScout, time.Now().UTC(), fmt.Errorf("realtime response %s", strings.TrimSpace(event.Response.Status)))
			} else {
				recordCapabilitySuccess(capabilityScout, time.Now().UTC())
			}
			for _, outputItem := range event.Response.Output {
				if outputItem.Type == "function_call" {
					hadFunctionCall = true
					if interrupted {
						// The response was cancelled/incomplete/failed: its tool
						// calls were never meant to complete, so skip them
						// silently instead of executing half-specified calls.
						if app.markCallHandled(outputItem.CallID) {
							log.Infof("Skipping %s tool call from %s response for call_id=%s reason=%s", outputItem.Name, event.Response.Status, outputItem.CallID, realtimeResponseStatusReason(event))
						}
						continue
					}
					app.handleToolCall(outputItem, false)
				}
			}
		}
		if !hadFunctionCall {
			app.markScoutSpokenResponseDelivered()
		}
		// W0-5: response.done over the datachannel is the authoritative
		// server-side metering point for the room voice seat.
		app.recordRealtimeResponseUsage(event)
		app.flushScoutSpokenResponseIfPending()
		broadcastAssistantEvent("audio", "Scout is listening", map[string]any{"eventType": event.Type, "voiceState": "listening"})
	default:
		if text := strings.TrimSpace(event.Text); text != "" && strings.Contains(event.Type, "text") {
			broadcastAssistantEvent("answer", text, map[string]any{"eventType": event.Type})
		}
	}
}

// isInterruptedRealtimeResponseStatus reports whether a response.done status
// means the response never completed (barge-in cancellation, truncation, or a
// server failure) and its tool calls must not be executed.
func isInterruptedRealtimeResponseStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "cancelled", "incomplete", "failed":
		return true
	default:
		return false
	}
}

func realtimeResponseStatusReason(event kanbanRealtimeEvent) string {
	if event.Response == nil || event.Response.StatusDetails == nil {
		return ""
	}

	return firstNonEmptyString(event.Response.StatusDetails.Reason, event.Response.StatusDetails.Type)
}

// recordRealtimeResponseUsage ledgers one voice turn's wire-reported usage
// (W0-5): the room voice session is server-owned, so the response.done usage
// object arriving over the datachannel is the authoritative metering point
// for seat voice_room. Turns whose events carry no usage record nothing —
// visible gaps beat invented numbers.
func (app *kanbanBoardApp) recordRealtimeResponseUsage(event kanbanRealtimeEvent) {
	if event.Response == nil {
		return
	}
	model := app.currentRealtimeModel()
	if model == "" {
		// The peer records its live model at start; before that (or on a
		// bare test app) the configured dial is the honest best guess.
		model = realtimeModel()
	}
	entry := llmUsageEntry{
		Provider: providerOpenAI,
		Model:    model,
		Seat:     seatVoiceRoom,
		RoomID:   officeRoomID,
	}
	if !realtimeUsageTokens(event.Response.Usage, &entry) {
		return
	}
	recordLLMUsage(entry)
}

// recordVoicePeerTranscriptionUsage ledgers the voice peer's own per-segment
// transcription billing when the wire reports it (W0-5). The in-session
// transcription bills at the transcription model's rates whether or not this
// peer is the persisting session, so this runs on every .completed event.
func (app *kanbanBoardApp) recordVoicePeerTranscriptionUsage(event kanbanRealtimeEvent) {
	entry := llmUsageEntry{
		Provider: providerOpenAI,
		Model:    realtimeTranscriptionModel(),
		Seat:     seatTranscriptionSession,
		RoomID:   officeRoomID,
	}
	if !realtimeUsageTokens(event.Usage, &entry) {
		return
	}
	recordLLMUsage(entry)
}

// recordVoicePeerTranscriptSegment feeds the W0 transcript funnel for the
// voice-peer FALLBACK transcription path — callers gate on the transcript
// lane being down, i.e. the Scout peer is the persisting session (the lane
// emits its own segments when it owns persistence).
func (app *kanbanBoardApp) recordVoicePeerTranscriptSegment(event kanbanRealtimeEvent, status string) {
	fields := map[string]any{
		"status":  status,
		"room_id": officeRoomID,
		"source":  "scout_realtime",
	}
	if event.Usage != nil && event.Usage.Seconds > 0 {
		fields["audio_seconds"] = event.Usage.Seconds
	}
	recordEvalEvent(seatTranscriptionSession, evalKindTranscriptSegment, fields)
}

func realtimeFunctionCallFromArgumentsDone(event kanbanRealtimeEvent) kanbanRealtimeOutputItem {
	if event.Item != nil {
		outputItem := *event.Item
		if outputItem.Type == "" {
			outputItem.Type = "function_call"
		}
		if outputItem.Name == "" {
			outputItem.Name = event.Name
		}
		if outputItem.Arguments == "" {
			outputItem.Arguments = event.Arguments
		}
		if outputItem.CallID == "" {
			outputItem.CallID = event.CallID
		}

		return outputItem
	}

	return kanbanRealtimeOutputItem{
		Type:      "function_call",
		Name:      event.Name,
		Arguments: event.Arguments,
		CallID:    event.CallID,
	}
}

func (app *kanbanBoardApp) handleToolCall(outputItem kanbanRealtimeOutputItem, allowIncompleteArguments bool) {
	if strings.TrimSpace(outputItem.CallID) == "" {
		log.Errorf("Ignoring Kanban tool call %q without call_id", outputItem.Name)
		return
	}

	args, parseErr := parseToolCallArguments(outputItem)
	outcome := classifyToolArgParse(parseErr, allowIncompleteArguments)
	if parseErr == nil && strings.TrimSpace(outputItem.Arguments) == "" {
		// No argument bytes streamed at all: a barge-in cancelled the response
		// before the model produced any arguments. Treat it like truncation
		// rather than executing the tool with no args.
		outcome = toolArgsAwaitingMore
		if !allowIncompleteArguments {
			outcome = toolArgsInterrupted
		}
	}
	switch outcome {
	case toolArgsAwaitingMore:
		// Still streaming: the completing event (response.done / output_item.done)
		// will retry with the full arguments.
		log.Infof("Waiting for complete %s arguments for call_id=%s", outputItem.Name, outputItem.CallID)
		return
	case toolArgsInterrupted:
		// The response was interrupted/cancelled before the arguments finished
		// streaming (common mid-meeting on barge-in). The call will never be
		// completed, so skip it: don't run a half-specified board mutation and
		// don't surface a parse error to the meeting chat feed. Mark it handled
		// so a later duplicate event for the same call is ignored too.
		if app.markCallHandled(outputItem.CallID) {
			log.Infof("Skipping interrupted %s tool call with incomplete arguments for call_id=%s", outputItem.Name, outputItem.CallID)
			if app.scoutVoiceArmed() {
				// the user addressed scout directly; don't drop the turn silently
				app.clearScoutVoiceArm()
				broadcastAssistantEvent("status", "Scout missed that — say it again", map[string]any{"tool": outputItem.Name})
			}
		}
		return
	}
	// toolArgsComplete and toolArgsMalformed both fall through: malformed-but-
	// complete arguments are a genuine error and still surface below.

	if !app.markCallHandled(outputItem.CallID) {
		return
	}

	// Snapshot the arm state before execution: a slow tool must not lose its
	// armed turn to window expiry while it runs.
	armedAtStart := app.scoutVoiceArmed()
	broadcastAssistantEvent("action", "using "+humanizeToolName(outputItem.Name), map[string]any{"tool": outputItem.Name})

	// Count the call as in flight until its result lands so a crosstalk or
	// continuation transcript completing meanwhile cannot disarm the turn.
	app.beginScoutToolCall()
	finish := func() {
		defer app.endScoutToolCall()
		app.finishToolCall(outputItem, args, parseErr, armedAtStart)
	}

	if realtimeToolRunsAsync(outputItem.Name) {
		// Memory answers block on a model call for up to 45s; run off the
		// datachannel event loop so realtime event processing keeps flowing.
		// The call id is already marked handled, so it can never run twice.
		go finish()
		return
	}
	finish()
}

func realtimeToolRunsAsync(name string) bool {
	switch name {
	case "answer_memory_question", "create_artifact", "launch_agent_thread", "archive_meeting", "meeting_recap", "catch_me_up", "cross_meeting_briefing", "end_grill_session", "organize_files", "save_to_files":
		// meeting_recap/catch_me_up block on a forced brain pass (up to 60s),
		// cross_meeting_briefing can block on its map-reduce fallback,
		// end_grill_session files a report thread, and organize_files /
		// save_to_files walk the whole file/artifact list plus the folder
		// side-store; run them off the datachannel event loop like the other
		// slow tools.
		return true
	case "company_financial_snapshot", "financial_comps", "fiscal_api_docs", "fiscal_data_query":
		// Every fiscal.ai tool makes a live MCP round-trip (up to 120s); it must
		// run off the datachannel event loop so realtime events keep flowing.
		// Only the typed pair rides room voice (see realtimeRoomVoiceTools), but
		// all four are marked slow so no surface can block the loop.
		return true
	default:
		return false
	}
}

func (app *kanbanBoardApp) finishToolCall(outputItem kanbanRealtimeOutputItem, args map[string]any, parseErr error, armedAtStart bool) {
	var result map[string]any
	var changed bool
	var err error
	if parseErr != nil {
		err = parseErr
	} else {
		result, changed, err = app.applyToolCallArgs(outputItem.Name, args)
	}
	if err != nil {
		result = map[string]any{
			"ok":    false,
			"error": err.Error(),
		}
		broadcastAssistantEvent("error", err.Error(), map[string]any{"tool": outputItem.Name})
	} else {
		// W0-7: finishToolCall is the one seam that is EXCLUSIVELY the room
		// voice datachannel loop (the shared applyToolCallArgs dispatch also
		// serves the board worker), so spoken proposal mints and direct
		// launches stamp their room_voice provenance here.
		app.recordRoomVoiceToolProvenance(outputItem.Name, result)
	}
	app.markScoutSpokenResponsePending(outputItem.Name, result, changed, armedAtStart)

	if err := app.SendEvent(map[string]any{
		"type": "conversation.item.create",
		"item": map[string]any{
			"type":    "function_call_output",
			"call_id": outputItem.CallID,
			// Same defense as the text orchestrator: a body-echoing tool result
			// (a full artifact/package body) must not bloat the Realtime session
			// context. Capped tighter here — the voice window is smaller and audio
			// tokens accrue fast.
			"output": capVoiceToolResultContent(mustMarshalJSON(result)),
		},
	}); err != nil {
		log.Errorf("Failed to send Kanban function output: %v", err)
		broadcastAssistantEvent("error", "could not send tool result to OpenAI Realtime", map[string]any{"tool": outputItem.Name})
	} else {
		app.flushScoutSpokenResponseIfPending()
	}

	if !changed {
		if outputItem.Name == "do_nothing" {
			if reason := asString(result["reason"]); reason != "" {
				broadcastAssistantEvent("status", reason, map[string]any{"tool": outputItem.Name})
			}
		}
		return
	}

	broadcastSignedInKanbanEvent("board", app.snapshotState())
	broadcastSignedInKanbanEvent("undo_available", app.canUndoDelete())
	broadcastAssistantEvent("action", humanizeToolName(outputItem.Name)+" complete", map[string]any{"tool": outputItem.Name})
	if err := app.SendEvent(app.sessionUpdateEvent()); err != nil {
		log.Errorf("Failed to refresh Kanban Realtime session: %v", err)
		broadcastAssistantEvent("error", "could not refresh assistant board context", map[string]any{"tool": outputItem.Name})
	}
}

func (app *kanbanBoardApp) applyToolCall(outputItem kanbanRealtimeOutputItem) (map[string]any, bool, error) {
	args, err := parseToolCallArguments(outputItem)
	if err != nil {
		return nil, false, err
	}

	return app.applyToolCallArgs(outputItem.Name, args)
}

// recordRoomVoiceToolProvenance stamps the W0-7 proposal-chain funnel for
// successful tool calls that arrived over the room-voice datachannel: a
// spoken propose_codex_task mints with source=room_voice. A spoken
// launch_agent_thread is a direct launch, but its funnel lineage now rides the
// spec onto the single launched emitter (see the room-voice dispatch), so no
// second launched event is recorded here.
func (app *kanbanBoardApp) recordRoomVoiceToolProvenance(toolName string, result map[string]any) {
	switch toolName {
	case "propose_codex_task":
		app.recordVoiceProposalMinted(proposalSourceRoomVoice, "", result)
	}
}

// recordVoiceProposalMinted emits the proposal-funnel minted event for a
// voice-surface propose_codex_task. Spoken asks have no brain/transcript
// lineage (they never passed through a brain window), so the available
// lineage is the origin room + active meeting (room voice) and the proposing
// account (private voice).
func (app *kanbanBoardApp) recordVoiceProposalMinted(source string, proposer string, result map[string]any) {
	if result == nil {
		return
	}
	proposal, ok := result["proposal"].(map[string]any)
	if !ok {
		return
	}
	fields := map[string]any{"source": source}
	if proposer != "" {
		fields["proposer"] = proposer
	}
	if source == proposalSourceRoomVoice {
		fields["room_id"] = officeRoomID
		if app.memory != nil {
			if meetingID := app.memory.currentMeetingID(officeRoomID); meetingID != "" {
				fields["meeting_id"] = meetingID
			}
		}
	}
	recordProposalEvent(proposalEventMinted, asString(proposal["id"]), fields)
}

// errTruncatedToolArguments marks a tool-argument parse failure caused by the
// arguments JSON being cut off rather than malformed.
var errTruncatedToolArguments = errors.New("tool arguments truncated")

func parseToolCallArguments(outputItem kanbanRealtimeOutputItem) (map[string]any, error) {
	args := map[string]any{}
	if rawArgs := strings.TrimSpace(outputItem.Arguments); rawArgs != "" {
		if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
			if isTruncatedJSONError(err, len(rawArgs)) {
				err = fmt.Errorf("%w: %v", errTruncatedToolArguments, err)
			}
			return nil, fmt.Errorf("parse %s arguments: %w", outputItem.Name, err)
		}
	}

	return args, nil
}

// isTruncatedJSONError reports whether a JSON parse failure looks like the
// input was cut off rather than malformed. Truncation mid-string yields
// "unexpected end of JSON input", but truncation mid-escape, mid-\u sequence,
// mid-literal, or mid-number yields "invalid character ..." syntax errors whose
// offset sits at/past the end of the input; genuinely malformed JSON fails at
// an earlier offset.
func isTruncatedJSONError(err error, inputLen int) bool {
	var syntaxErr *json.SyntaxError
	return errors.As(err, &syntaxErr) && syntaxErr.Offset >= int64(inputLen)
}

func isIncompleteToolArgumentsError(err error) bool {
	return errors.Is(err, errTruncatedToolArguments) ||
		strings.Contains(err.Error(), "unexpected end of JSON input")
}

// toolArgParseOutcome classifies the result of parsing a tool call's arguments.
type toolArgParseOutcome int

const (
	toolArgsComplete     toolArgParseOutcome = iota // valid arguments, proceed
	toolArgsAwaitingMore                            // truncated, but more is still streaming in
	toolArgsInterrupted                             // truncated on the final event: response was cut off
	toolArgsMalformed                               // complete but invalid JSON: a genuine error
)

// classifyToolArgParse interprets a tool-argument parse result. A truncation-
// shaped failure (see isTruncatedJSONError) means the arguments ended
// prematurely: while the model is still streaming (allowIncomplete) the
// completing event will follow, so we wait; on the final event it means the
// response was interrupted/cancelled mid-call, so
// the call should be skipped rather than executed or reported as an error. Any
// other parse failure is malformed-but-complete JSON and remains a real error.
func classifyToolArgParse(parseErr error, allowIncomplete bool) toolArgParseOutcome {
	if parseErr == nil {
		return toolArgsComplete
	}
	if isIncompleteToolArgumentsError(parseErr) {
		if allowIncomplete {
			return toolArgsAwaitingMore
		}
		return toolArgsInterrupted
	}

	return toolArgsMalformed
}

// markCallHandled records a call_id as handled, returning true only the first
// time so callers can deduplicate the multiple Realtime events that describe the
// same tool call.
func (app *kanbanBoardApp) markCallHandled(callID string) bool {
	app.mu.Lock()
	defer app.mu.Unlock()
	if _, ok := app.handledCalls[callID]; ok {
		return false
	}
	app.handledCalls[callID] = struct{}{}

	return true
}

func (app *kanbanBoardApp) applyToolCallArgs(toolName string, args map[string]any) (map[string]any, bool, error) {
	switch toolName {
	case "create_ticket":
		return app.createTicket(args)
	case "move_ticket":
		return app.moveTicket(args)
	case "add_tags":
		return app.addTags(args)
	case "add_key_date":
		return app.addKeyDate(args)
	case "remove_key_dates":
		return app.removeKeyDates(args)
	case "update_ticket":
		return app.updateTicket(args)
	case "delete_ticket":
		return app.deleteTicket(args)
	case "undo_delete_ticket":
		return app.restoreLastDeletedTicket()
	case "control_app":
		return app.controlApp(args)
	case "set_voice_control":
		return app.setRealtimeVoiceControl(args)
	case "set_recording":
		return app.setRealtimeRecording(args)
	case "archive_meeting":
		return app.archiveRealtimeMeeting(args)
	case "create_artifact":
		return app.createRealtimeArtifact(args)
	case "launch_agent_thread":
		// The shared dispatch serves the room voice loop and the board
		// worker: both act on the live meeting, so completion delivers back
		// to the room. The private dashboard voice path intercepts this tool
		// in applyPrivateRealtimeVoiceTool and passes no origin. Only the room
		// voice loop reaches THIS case (the board worker and orchestrator
		// allowlists both exclude launch_agent_thread), so the direct-launch
		// funnel lineage (source=room_voice, path=launch_agent_thread) rides the
		// spec onto the single launched emitter.
		return app.launchRealtimeAgentThread(args, map[string]string{
			"originKind":      agentThreadOriginRoom,
			"originMeetingId": app.memory.currentMeetingID(officeRoomID),
		}, agentThreadGoalSpec{Launch: launchFunnelLineage{
			Source: proposalSourceRoomVoice,
			Path:   "launch_agent_thread",
		}})
	case "update_artifact":
		return app.updateRealtimeArtifact(args)
	case "publish_artifact":
		return app.publishRealtimeArtifact(args)
	case "answer_memory_question":
		return app.answerMemoryQuestionForPrincipal(args, sharedRoomRecallPrincipal(officeRoomID, app.memory.currentMeetingID(officeRoomID)), true)
	case "organize_files":
		// Shared dispatch (room voice + workers) has no single requester: an
		// empty viewer scopes the file list to team-visible rows only (direct
		// uploads + public channels); the private voice path passes the real
		// requester in applyPrivateRealtimeVoiceTool.
		return app.organizeFilesTool(args, "")
	case "save_to_files":
		// Deliverables are team-wide, so the match set is not requester-scoped;
		// the empty requester only leaves the savedToFilesBy attribution to
		// Scout. The private voice path passes the real requester below.
		return app.saveToFilesTool(args, "")
	case "note_for_the_record":
		// Shared room-voice entry point: no single session author (the room is
		// shared), so the note is filed author-uncertain unless an explicit owner is
		// named. No per-call id exists here, so the scope carries an hour bucket
		// (F14) — an accidental double-fire in the hour dedupes, a later deliberate
		// re-file does not. The private-voice path passes the signed-in requester,
		// and the agent-thread orchestrator passes job.RequestedBy + thread + tool-
		// call id, so those file author-certain with their own idempotency scope.
		return app.noteForTheRecordTool(args, "", hourBucketedNoteScope("room-voice", time.Now()))
	case "portfolio_health":
		// Read-only aggregation over the venture packages; no requester needed,
		// so the shared dispatch serves both the room and private voice paths.
		return app.portfolioHealthTool()
	case "company_financial_snapshot":
		// Read-only fiscal.ai grounding: no requester, no board mutation, so
		// the shared dispatch serves every surface.
		return app.companyFinancialSnapshotTool(args)
	case "financial_comps":
		return app.financialCompsTool(args)
	case "fiscal_api_docs":
		return app.fiscalAPIDocsTool(args)
	case "fiscal_data_query":
		return app.fiscalDataQueryTool(args)
	case "propose_codex_task":
		// Creates a confirmable proposal, never launches an agent thread
		// directly. The shared dispatch (board worker + room voice) has no
		// single requester, so provenance falls back to board_worker.
		return app.proposeCodexTask(args, "")
	case "create_package":
		// Shared dispatch (room voice + workers) has no single requester, so
		// package mutations attribute to Scout inside the tool helpers.
		return app.createPackageTool(args, "")
	case "attach_to_package":
		return app.attachToPackageTool(args, "")
	case "advance_package_stage":
		return app.advancePackageStageTool(args, "")
	case "send_notification":
		// The shared room path has no single requester; audience "me" falls
		// back to everyone there (see sendRealtimeNotification).
		return app.sendRealtimeNotification(args, "")
	case "post_to_channel":
		// Room voice has no single requester: the post attributes to Scout.
		return app.postToChannel(args, "")
	case "create_channel":
		// Rejected without a requester — channels need an owner identity.
		return app.createChannelByVoice(args, "")
	case "meeting_recap":
		// Room voice: audience "me" falls back to a room post (no requester).
		// The shared room voice is the office's (per-room Scout voice is not
		// instantiated for named rooms), so the recap binds to the office.
		return app.meetingRecap(args, "", officeRoomID)
	case "catch_me_up":
		return app.catchMeUp(args, "", officeRoomID)
	case "cross_meeting_briefing":
		return app.crossMeetingBriefingToolForPrincipal(args, sharedRoomRecallPrincipal(officeRoomID, app.memory.currentMeetingID(officeRoomID)))
	case "get_meeting_detail":
		return app.getMeetingDetailForPrincipal(args, sharedRoomRecallPrincipal(officeRoomID, app.memory.currentMeetingID(officeRoomID)))
	case "start_grill_session":
		return app.startGrillSession(args)
	case "end_grill_session":
		return app.endGrillSession(args)
	case "do_nothing":
		reason := asString(args["reason"])
		if reason == "" {
			reason = "No board update requested."
		}
		return map[string]any{
			"ok":     true,
			"reason": reason,
		}, false, nil
	default:
		return nil, false, fmt.Errorf("unsupported function %q", toolName)
	}
}

// applyToolCallArgsForPrincipal is the context-carrying dispatch seam for
// recall-shaped tools. Call sites that know a room/user must use this instead
// of smuggling room ids through model-controlled arguments. Non-recall tools
// retain the legacy dispatcher.
func (app *kanbanBoardApp) applyToolCallArgsForPrincipal(toolName string, args map[string]any, principal RecallPrincipal) (map[string]any, bool, error) {
	switch strings.TrimSpace(toolName) {
	case "answer_memory_question":
		return app.answerMemoryQuestionForPrincipal(args, principal, false)
	case "meeting_recap", "catch_me_up":
		if principal.Audience == "guest" || principal.User == nil {
			return nil, false, fmt.Errorf("meeting recap is unavailable for this principal")
		}
		roomID := normalizeRoomID(principal.RoomID)
		if roomID != officeRoomID && (strings.TrimSpace(principal.SittingID) == "" || app.memory.currentMeetingID(roomID) != strings.TrimSpace(principal.SittingID)) {
			return nil, false, fmt.Errorf("meeting recap room binding is stale")
		}
		requester := ""
		if principal.Audience == "private" {
			requester = normalizeAccountEmail(principal.User.Email)
			privateArgs := make(map[string]any, len(args)+1)
			for key, value := range args {
				privateArgs[key] = value
			}
			privateArgs["audience"] = notificationAudienceMe
			args = privateArgs
		}
		if toolName == "catch_me_up" {
			return app.catchMeUp(args, requester, roomID)
		}
		return app.meetingRecap(args, requester, roomID)
	case "cross_meeting_briefing":
		return app.crossMeetingBriefingToolForPrincipal(args, principal)
	case "get_meeting_detail":
		return app.getMeetingDetailForPrincipal(args, principal)
	default:
		return app.applyToolCallArgs(toolName, args)
	}
}

// --- fiscal.ai tool dispatch --------------------------------------------------
//
// All four tools are read-only and self-contained: no requester, no board
// mutation, no ctx on this seam — the fiscal client carries its own 120s
// timeout, so each dispatch uses context.Background(). Keyless returns a
// clear payload instead of an error (the initiate_goal posture) so keyless
// deploys keep working.

const (
	fiscalDataQueryDefaultMaxChars = 20000
	// fiscalDataQueryMaxCharsCeiling caps a model-supplied max_chars so a large
	// value cannot pour up to the 4MB response bound back into the tool-loop
	// context. ~100K chars is roughly 25K tokens — a generous single-tool cap.
	fiscalDataQueryMaxCharsCeiling = 100000
	// fiscalFullDocsMaxChars is a safety bound on the typed docs, set above the
	// real payload (~66KB) so topic="full" returns the complete docs while still
	// capping a pathological upstream response.
	fiscalFullDocsMaxChars = 262144
)

// fiscalToolNotConfigured is the shared keyless payload for every fiscal tool.
func fiscalToolNotConfigured() (map[string]any, bool, error) {
	return map[string]any{
		"ok":     false,
		"reason": "FISCAL_AI_API_KEY is not configured — fiscal.ai financial grounding is unavailable here.",
	}, false, nil
}

// fiscalTruncate caps tool text with an explicit notice so the model knows
// the cut happened and at what size. The cut backs up to a rune boundary so a
// multi-byte character is never split into invalid UTF-8 (json.Marshal would
// otherwise rewrite the tail to U+FFFD).
func fiscalTruncate(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit] + fmt.Sprintf("\n[truncated at %d chars]", limit)
}

func (app *kanbanBoardApp) companyFinancialSnapshotTool(args map[string]any) (map[string]any, bool, error) {
	if !hasFiscalAPIKey() {
		return fiscalToolNotConfigured()
	}
	company := asString(args["company"])
	if company == "" {
		return nil, false, fmt.Errorf("company is required")
	}
	snapshot, err := fiscalCompanySnapshot(context.Background(), company)
	if err != nil {
		return nil, false, err
	}
	return map[string]any{"ok": true, "snapshot": snapshot}, false, nil
}

func (app *kanbanBoardApp) financialCompsTool(args map[string]any) (map[string]any, bool, error) {
	if !hasFiscalAPIKey() {
		return fiscalToolNotConfigured()
	}
	company := asString(args["company"])
	if company == "" {
		return nil, false, fmt.Errorf("company is required")
	}
	// nil ratio ids and peer_limit 0 take the client defaults (3 ratios, 6 peers).
	comps, err := fiscalComps(context.Background(), company, asStringSlice(args["ratio_ids"]), asInt(args["peer_limit"]))
	if err != nil {
		return nil, false, err
	}
	return map[string]any{"ok": true, "comps": comps}, false, nil
}

func (app *kanbanBoardApp) fiscalAPIDocsTool(args map[string]any) (map[string]any, bool, error) {
	if !hasFiscalAPIKey() {
		return fiscalToolNotConfigured()
	}
	topic := asString(args["topic"])
	var docs string
	var err error
	switch topic {
	case "", "index":
		topic = "index"
		docs, err = fiscalAPIDocsCompact(context.Background())
	case "full":
		docs, err = fiscalAPIDocs(context.Background())
		docs = fiscalTruncate(docs, fiscalFullDocsMaxChars)
	default:
		return nil, false, fmt.Errorf("unsupported topic %q (use index or full)", topic)
	}
	if err != nil {
		return nil, false, err
	}
	return map[string]any{"ok": true, "topic": topic, "docs": docs}, false, nil
}

func (app *kanbanBoardApp) fiscalDataQueryTool(args map[string]any) (map[string]any, bool, error) {
	if !hasFiscalAPIKey() {
		return fiscalToolNotConfigured()
	}
	code := asString(args["code"])
	if code == "" {
		return nil, false, fmt.Errorf("code is required")
	}
	maxChars := asInt(args["max_chars"])
	if maxChars <= 0 {
		maxChars = fiscalDataQueryDefaultMaxChars
	}
	if maxChars > fiscalDataQueryMaxCharsCeiling {
		maxChars = fiscalDataQueryMaxCharsCeiling
	}
	output, err := fiscalExecuteCode(context.Background(), code)
	if err != nil {
		return nil, false, err
	}
	return map[string]any{"ok": true, "output": fiscalTruncate(output, maxChars)}, false, nil
}

// privateRealtimeVoiceToolAllowed is the single source of truth for what
// private Scout ("she can do it all") may call. Room-only tools are excluded by
// construction: set_voice_control / set_recording / archive_meeting mutate the
// shared room session or recording for every participant, and
// start_grill_session / end_grill_session swap the shared room persona — the
// private surface has no room. Everything else the private user owns for
// themselves, including board mutation on their behalf and artifact edits.
func privateRealtimeVoiceToolAllowed(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case
		// Navigation, recall, artifacts (the private user owns editing).
		"control_app", "create_artifact", "update_artifact", "publish_artifact",
		"launch_agent_thread", "answer_memory_question", "propose_codex_task",
		// Board mutation on the user's behalf — private Scout drives the board
		// for you; the mutation path is the same shared applyToolCallArgs.
		"create_ticket", "move_ticket", "update_ticket", "add_tags",
		"add_key_date", "remove_key_dates", "delete_ticket", "undo_delete_ticket",
		// Packages, notifications, channels, recap, cross-meeting recall.
		"create_package", "attach_to_package", "advance_package_stage", "portfolio_health",
		"send_notification", "post_to_channel", "create_channel",
		"meeting_recap", "catch_me_up", "cross_meeting_briefing", "get_meeting_detail",
		// fiscal.ai grounding — only the typed, spoken-ready pair; fiscal_api_docs
		// and fiscal_data_query return payloads too heavy for a voice turn.
		"company_financial_snapshot", "financial_comps",
		// New Realtime-2 parity tools (Wave 6).
		"read_thread_aloud", "start_chat_as_user", "initiate_goal",
		// Files organization — requester-scoped, so the private voice sees the
		// same rows GET /assistant/files serves them.
		"organize_files",
		// Save a finished deliverable onto the Files surface (explicit-save
		// gate); attribution carries the signed-in requester.
		"save_to_files",
		// Deliberate-write path: file an author-certain "for the record" note into
		// company memory. The private user owns filing their own notes.
		"note_for_the_record",
		// Private grill (Wave 12) — client-driven session.update swap, private
		// only. The room grill (start_grill_session/end_grill_session) swaps the
		// SHARED room persona server-side and stays room-only above; this variant
		// grills the single dashboard user and never mutates a server session.
		"start_private_grill", "end_private_grill",
		"do_nothing":
		return true
	default:
		return false
	}
}

func (app *kanbanBoardApp) applyPrivateRealtimeVoiceTool(requesterEmail string, toolName string, args map[string]any) (map[string]any, bool, error) {
	toolName = strings.TrimSpace(toolName)
	if !privateRealtimeVoiceToolAllowed(toolName) {
		return nil, false, fmt.Errorf("private Realtime voice cannot use %q", toolName)
	}
	if args == nil {
		args = map[string]any{}
	}
	roomID := app.memberCurrentRoom(requesterEmail)
	principal := app.recallPrincipalForMemberRoom(requesterEmail, roomID)
	if toolName == "answer_memory_question" || toolName == "meeting_recap" || toolName == "catch_me_up" || toolName == "cross_meeting_briefing" || toolName == "get_meeting_detail" {
		return app.applyToolCallArgsForPrincipal(toolName, args, principal)
	}
	if toolName == "create_artifact" {
		return app.createRealtimeArtifactForPrincipal(args, recallPrincipalForEmail(requesterEmail))
	}
	// send_notification and propose_codex_task depend on who is asking: the
	// private dashboard voice belongs to a single signed-in user, so audience
	// "me" can target that account and proposals carry real provenance.
	if toolName == "send_notification" {
		return app.sendRealtimeNotification(args, requesterEmail)
	}
	if toolName == "propose_codex_task" {
		result, changed, err := app.proposeCodexTask(args, normalizeAccountEmail(requesterEmail))
		if err == nil {
			// W0-7: private-voice mints carry the signed-in proposer as their
			// lineage — this surface has exactly one human behind it.
			app.recordVoiceProposalMinted(proposalSourcePrivateVoice, normalizeAccountEmail(requesterEmail), result)
		}
		return result, changed, err
	}
	// Channel and recap tools carry the signed-in requester so posts attribute
	// to the real author and catch-me-up recaps land in the right bell.
	if toolName == "post_to_channel" {
		return app.postToChannel(args, requesterEmail)
	}
	if toolName == "create_channel" {
		return app.createChannelByVoice(args, requesterEmail)
	}
	// Package mutations carry the signed-in requester's identity from the
	// private dashboard voice; the shared dispatch falls back to Scout.
	if toolName == "create_package" {
		return app.createPackageTool(args, packageToolActor(requesterEmail))
	}
	if toolName == "attach_to_package" {
		return app.attachToPackageTool(args, packageToolActor(requesterEmail))
	}
	if toolName == "advance_package_stage" {
		return app.advancePackageStageTool(args, packageToolActor(requesterEmail))
	}
	// The private dashboard voice is not the room's work: launches carry no
	// room origin, so completion stays with the creator notification. The
	// direct-launch funnel lineage (source=private_voice, proposer) rides the
	// spec onto the single launched emitter — no second launched event here.
	if toolName == "launch_agent_thread" {
		return app.launchRealtimeAgentThread(args, nil, agentThreadGoalSpec{Launch: launchFunnelLineage{
			Source:   proposalSourcePrivateVoice,
			Path:     "launch_agent_thread",
			Proposer: normalizeAccountEmail(requesterEmail),
		}})
	}
	// read_thread_aloud resolves recent text scoped to the signed-in requester
	// (private threads and notifications are theirs); the session speaks it.
	if toolName == "read_thread_aloud" {
		return app.readThreadAloud(args, requesterEmail)
	}
	// organize_files resolves the requester's own file list (their private
	// thread attachments included) before matching names into a folder.
	if toolName == "organize_files" {
		return app.organizeFilesTool(args, requesterEmail)
	}
	// save_to_files stamps matched deliverables savedToFiles=true; the requester
	// is the savedToFilesBy attribution.
	if toolName == "save_to_files" {
		return app.saveToFilesTool(args, requesterEmail)
	}
	// note_for_the_record files an author-certain memory entry; the signed-in
	// requester is the certain author. There is no per-call id on the voice path,
	// so the scope carries an hour bucket (F14): a double-call within the hour
	// files once, a later deliberate re-file lands as its own record.
	if toolName == "note_for_the_record" {
		return app.noteForTheRecordTool(args, requesterEmail, hourBucketedNoteScope("private-voice:"+normalizeAccountEmail(requesterEmail), time.Now()))
	}
	// start_chat_as_user posts on the user's behalf with a mandatory,
	// server-stamped disclosure — the requester identity, never a model arg.
	if toolName == "start_chat_as_user" {
		return app.startChatAsUser(args, requesterEmail)
	}
	// initiate_goal launches the /goal engine as the signed-in requester and can
	// never request external_write (the dispatch clamps it below). Each goal
	// subtask launches through launchAgentThreadWithSpec's choke point and emits
	// its own launched event, so the goal-level direct launch records none of its
	// own — that would double the ProposalsLaunched counter against the subtasks.
	if toolName == "initiate_goal" {
		return app.initiateGoalTool(args, requesterEmail)
	}
	// start_private_grill / end_private_grill return the instruction block the
	// BROWSER applies over its own data channel (the server does not own the
	// private peer, so it cannot push session.update). Neither mutates any
	// server session state; end also files the graded scorecard as the requester.
	if toolName == "start_private_grill" {
		return app.startPrivateGrill(args, requesterEmail)
	}
	if toolName == "end_private_grill" {
		return app.endPrivateGrill(args, requesterEmail)
	}
	// Board mutations and artifact edits fall through to the shared dispatch,
	// which mutates the board but does NOT broadcast. The caller fans the
	// board/undo snapshots out on changed==true — assistantRealtimeToolHandler
	// for private voice (RW1), exactly as the room-voice path does after this
	// same call at ~2861.
	return app.applyToolCallArgs(toolName, args)
}

// initiateGoalTool launches the /goal pipeline by voice as the signed-in
// requester. It is a thin adapter over launchGoalThread: it can NEVER request
// external_write (the schema enum excludes it AND this dispatch clamps any
// smuggled value down to workspace_write), and it degrades gracefully when the
// orchestrator key is absent so keyless deploys keep working.
func (app *kanbanBoardApp) initiateGoalTool(args map[string]any, requesterEmail string) (map[string]any, bool, error) {
	objective := strings.TrimSpace(asString(args["objective"]))
	if objective == "" {
		return nil, false, fmt.Errorf("objective is required")
	}
	// Clamp authority: external_write is earned at the gate, never set here.
	// Anything that is not an explicit read_only hint becomes workspace_write.
	authority := codexJobAuthorityWorkspaceWrite
	if strings.EqualFold(strings.TrimSpace(asString(args["authority_hint"])), "read_only") {
		authority = codexJobAuthorityReadOnly
	}

	spec := goalLaunchSpec{
		Objective:    objective,
		CreatedBy:    normalizeAccountEmail(requesterEmail),
		Authority:    authority,
		PackageID:    strings.TrimSpace(asString(args["package"])),
		ToolTemplate: strings.TrimSpace(asString(args["tool"])),
	}
	thread, err := app.launchGoalThread(spec)
	if err != nil {
		if errors.Is(err, errAgentWorkerNotConfigured) {
			// Keyless / no orchestrator: speak an honest fallback instead of a
			// hard error, and do not pretend a goal is running.
			return map[string]any{
				"ok":       false,
				"launched": false,
				"reason":   "the goal engine needs the orchestrator key, which is not configured here — I can research or draft pieces of it instead.",
			}, false, nil
		}
		return nil, false, err
	}

	return map[string]any{
		"ok":        true,
		"launched":  true,
		"objective": objective,
		"threadId":  thread.ID,
		"thread":    thread,
		"artifact":  thread.Artifact,
		"authority": authority,
	}, false, nil
}

func (app *kanbanBoardApp) controlApp(args map[string]any) (map[string]any, bool, error) {
	tool := normalizeOSControlTool(asString(args["tool"]))
	if tool == "" {
		return nil, false, fmt.Errorf("tool is required")
	}
	artifactID := firstNonEmptyString(asString(args["artifact_id"]), asString(args["artifactId"]))
	actions := osAssistantActionsForTool(tool, artifactID)
	opened := []string{tool}
	// also_open loops extra surfaces into the same action batch so "open the
	// market map AND the deck" is one tool call, not several.
	for _, extra := range asStringSlice(args["also_open"]) {
		normalized := normalizeOSControlTool(extra)
		if normalized == "" || normalized == tool {
			continue
		}
		actions = append(actions, osAssistantActionsForTool(normalized, "")...)
		opened = append(opened, normalized)
	}
	broadcastAssistantEvent("action", "Opened "+assistantToolLabel(tool), map[string]any{
		"tool":       "control_app",
		"actions":    actions,
		"voiceState": "listening",
	})

	return map[string]any{
		"ok":         true,
		"tool":       tool,
		"artifactId": artifactID,
		"actions":    actions,
		"opened":     opened,
	}, false, nil
}

func (app *kanbanBoardApp) setRealtimeVoiceControl(args map[string]any) (map[string]any, bool, error) {
	rawEnabled, exists := args["enabled"]
	if !exists {
		return nil, false, fmt.Errorf("enabled is required")
	}
	enabled, ok := rawEnabled.(bool)
	if !ok {
		return nil, false, fmt.Errorf("enabled must be a boolean")
	}

	app.setVoiceControlActive(enabled, scoutParticipantName)
	message := "Realtime voice is still listening"
	voiceState := "listening"
	if !enabled {
		message = "Realtime voice is off"
		voiceState = "idle"
	}
	actions := []osAssistantAction{{
		Type:    "set_voice_control",
		Enabled: boolPtr(enabled),
		Label:   message,
	}}
	broadcastAssistantEvent("action", message, map[string]any{
		"tool":         "set_voice_control",
		"voiceControl": enabled,
		"voiceState":   voiceState,
		"actions":      actions,
	})

	return map[string]any{
		"ok":           true,
		"enabled":      enabled,
		"voiceControl": enabled,
		"actions":      actions,
		"message":      message,
	}, false, nil
}

func (app *kanbanBoardApp) setRealtimeRecording(args map[string]any) (map[string]any, bool, error) {
	rawEnabled, exists := args["enabled"]
	if !exists {
		return nil, false, fmt.Errorf("enabled is required")
	}
	enabled, ok := rawEnabled.(bool)
	if !ok {
		return nil, false, fmt.Errorf("enabled must be a boolean")
	}

	snapshot := app.setTranscriptRecording(enabled, scoutParticipantName)
	recording, _ := snapshot["recording"].(roomRecordingState)
	message := roomRecordingAnnouncementText(recording)
	broadcastKanbanEvent("participants", snapshot)
	broadcastAssistantEvent("answer", message, map[string]any{
		"tool":       "set_recording",
		"recording":  recording,
		"voiceState": "talking",
	})

	return map[string]any{
		"ok":        true,
		"enabled":   enabled,
		"recording": recording,
		"room":      snapshot,
		"message":   message,
	}, false, nil
}

func (app *kanbanBoardApp) archiveRealtimeMeeting(_ map[string]any) (map[string]any, bool, error) {
	result, err := app.archiveMeeting(scoutParticipantName)
	if err != nil {
		return nil, false, err
	}

	broadcastSignedInKanbanEvent("meeting_archived", result)
	broadcastSignedInKanbanEvent("memory", nil)
	var actions []osAssistantAction
	if result.Artifact != nil {
		actions = app.osAssistantActions(result.Summary, "artifacts", *result.Artifact)
	}
	broadcastAssistantEvent("action", "Meeting notes saved", map[string]any{
		"tool":       "archive_meeting",
		"archive":    result,
		"actions":    actions,
		"voiceState": "listening",
	})

	return map[string]any{
		"ok":      true,
		"archive": result,
		"actions": actions,
		"message": result.Summary,
	}, false, nil
}

func (app *kanbanBoardApp) updateRealtimeArtifact(args map[string]any) (map[string]any, bool, error) {
	artifactID := firstNonEmptyString(asString(args["artifact_id"]), asString(args["artifactId"]))
	if artifactID == "" {
		return nil, false, fmt.Errorf("artifact_id is required")
	}
	title := canonicalizeBoardText(asString(args["title"]))
	content := strings.TrimSpace(firstNonEmptyString(asString(args["content"]), asString(args["text"])))
	if title == "" && content == "" {
		return nil, false, fmt.Errorf("title or content is required")
	}

	existing, exists := app.osArtifactByID(artifactID)
	if !exists {
		return nil, false, fmt.Errorf("artifact not found")
	}
	if content == "" {
		content = existing.Text
	}

	artifact, updated, err := app.updateOSArtifact(artifactID, title, content, scoutParticipantName)
	if err != nil {
		return nil, false, err
	}
	broadcastSignedInKanbanEvent("memory", nil)
	actions := app.osAssistantActions(title, "artifacts", artifact)
	broadcastAssistantEvent("action", "Artifact updated", map[string]any{
		"tool":       "update_artifact",
		"artifact":   artifact,
		"updated":    updated,
		"actions":    actions,
		"voiceState": "listening",
	})

	return map[string]any{
		"ok":       true,
		"artifact": artifact,
		"updated":  updated,
		"actions":  actions,
	}, false, nil
}

func (app *kanbanBoardApp) updateRealtimeArtifactForUser(ctx context.Context, user *userAccount, args map[string]any) (map[string]any, bool, error) {
	artifactID := firstNonEmptyString(asString(args["artifact_id"]), asString(args["artifactId"]))
	if artifactID == "" {
		return nil, false, fmt.Errorf("artifact_id is required")
	}
	header, found := app.memory.artifactAuthorizationHeaderByID(artifactID)
	if !found || !artifactHeaderAuthorized(ctx, user, ACLWrite, header) {
		return nil, false, fmt.Errorf("artifact not found")
	}
	existing, found := app.memory.artifactSnapshotIfHeaderMatches(artifactID, header)
	if !found {
		return nil, false, fmt.Errorf("artifact not found")
	}
	title := canonicalizeBoardText(asString(args["title"]))
	content := strings.TrimSpace(firstNonEmptyString(asString(args["content"]), asString(args["text"])))
	if title == "" && content == "" {
		return nil, false, fmt.Errorf("title or content is required")
	}
	if content == "" {
		content = existing.Text
	}
	artifact, updated, err := app.memory.updateOSArtifactWithMetadataIfHeaderMatches(header, artifactID, title, content, user.Name, nil)
	if err != nil {
		return nil, false, fmt.Errorf("artifact not found")
	}
	broadcastSignedInKanbanEvent("memory", nil)
	actions := app.osAssistantActions(title, "artifacts", artifact)
	return map[string]any{"ok": true, "artifact": artifact, "updated": updated, "actions": actions}, false, nil
}

func (app *kanbanBoardApp) publishRealtimeArtifact(args map[string]any) (map[string]any, bool, error) {
	artifactID := firstNonEmptyString(asString(args["artifact_id"]), asString(args["artifactId"]))
	if artifactID == "" {
		return nil, false, fmt.Errorf("artifact_id is required")
	}
	rawPublished, ok := args["published"]
	if !ok {
		return nil, false, fmt.Errorf("published is required")
	}
	publishedValue, ok := rawPublished.(bool)
	if !ok {
		return nil, false, fmt.Errorf("published must be a boolean")
	}

	artifact, updated, err := app.publishOSArtifact(artifactID, publishedValue, scoutParticipantName)
	if err != nil {
		return nil, false, err
	}
	broadcastSignedInKanbanEvent("memory", nil)
	actions := app.osAssistantActions(artifact.Metadata["title"], "artifacts", artifact)
	message := "Artifact unpublished"
	if publishedValue {
		message = "Artifact published"
	}
	broadcastAssistantEvent("action", message, map[string]any{
		"tool":       "publish_artifact",
		"artifact":   artifact,
		"updated":    updated,
		"actions":    actions,
		"voiceState": "listening",
	})

	return map[string]any{
		"ok":        true,
		"artifact":  artifact,
		"published": publishedValue,
		"updated":   updated,
		"actions":   actions,
	}, false, nil
}

func (app *kanbanBoardApp) osArtifactByID(id string) (meetingMemoryEntry, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return meetingMemoryEntry{}, false
	}
	for _, artifact := range app.osArtifactsSnapshot(0) {
		if artifact.ID == id {
			return artifact, true
		}
	}

	return meetingMemoryEntry{}, false
}

func boolPtr(value bool) *bool {
	return &value
}

func normalizeOSControlTool(tool string) string {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "office", "room", "chat", "artifacts", "research", "design", "grill", "board", "memory", "files":
		return strings.ToLower(strings.TrimSpace(tool))
	case "artifact":
		return "artifacts"
	case "file", "drive":
		return "files"
	default:
		return ""
	}
}

func osAssistantActionsForTool(tool string, artifactID string) []osAssistantAction {
	tool = normalizeOSControlTool(tool)
	artifactID = strings.TrimSpace(artifactID)
	if tool == "" {
		return nil
	}
	actions := []osAssistantAction{{
		Type:       "open_tool",
		Tool:       tool,
		Mode:       normalizeOSAssistantMode(tool),
		ArtifactID: artifactID,
		Label:      "Opened " + assistantToolLabel(tool),
	}}
	if tool == "artifacts" && artifactID != "" {
		actions = append(actions, osAssistantAction{
			Type:       "select_artifact",
			Tool:       "artifacts",
			ArtifactID: artifactID,
			Label:      "Selected artifact",
		})
	}
	return actions
}

func (app *kanbanBoardApp) createRealtimeArtifact(args map[string]any) (map[string]any, bool, error) {
	return app.createRealtimeArtifactForPrincipal(args, sharedRoomRecallPrincipal(officeRoomID, app.memory.currentMeetingID(officeRoomID)))
}

func (app *kanbanBoardApp) createRealtimeArtifactForPrincipal(args map[string]any, principal RecallPrincipal) (map[string]any, bool, error) {
	mode := normalizeRealtimeArtifactMode(asString(args["mode"]))
	if mode == "" {
		return nil, false, fmt.Errorf("mode is required")
	}
	query := canonicalizeBoardText(asString(args["query"]))
	if query == "" {
		return nil, false, fmt.Errorf("query is required")
	}
	content := strings.TrimSpace(asString(args["content"]))
	if content == "" {
		ctx, cancel := context.WithTimeout(context.Background(), assistantQueryRequestTimeout)
		defer cancel()
		requester := ""
		if principal.User != nil {
			requester = principal.User.Email
		}
		result, err := app.resolveAssistantQueryContextForPrincipalWithAttachments(ctx, principal, requester, query, nil, nil)
		if err != nil {
			return nil, false, err
		}
		result = buildOSAssistantModeAnswer(mode, result, app.snapshotState(), app.scopedRecallApp(ctx, principal).memorySnapshotForClients(12))
		content = strings.TrimSpace(result.answer)
	}
	if content == "" {
		return nil, false, fmt.Errorf("artifact content is empty")
	}

	artifact, appended, err := app.createOSArtifact(mode, query, content, scoutParticipantName)
	if err != nil {
		return nil, false, err
	}
	if artifact.ID == "" {
		return nil, false, fmt.Errorf("artifact was not saved")
	}

	broadcastSignedInKanbanEvent("memory", nil)
	actions := app.osAssistantActions(query, mode, artifact)
	broadcastAssistantEvent("action", assistantToolLabel(mode)+" artifact saved", map[string]any{
		"tool":       "create_artifact",
		"mode":       mode,
		"artifact":   artifact,
		"actions":    actions,
		"voiceState": "listening",
	})

	return map[string]any{
		"ok":       true,
		"mode":     mode,
		"query":    query,
		"artifact": artifact,
		"appended": appended,
		"actions":  actions,
	}, false, nil
}

func (app *kanbanBoardApp) launchRealtimeAgentThread(args map[string]any, origin map[string]string, spec agentThreadGoalSpec) (map[string]any, bool, error) {
	mode := normalizeAgentThreadMode(asString(args["mode"]))
	if mode == "" {
		return nil, false, fmt.Errorf("mode is required")
	}
	query := canonicalizeBoardText(asString(args["query"]))
	if query == "" {
		return nil, false, fmt.Errorf("query is required")
	}

	thread, err := app.launchAgentThreadWithSpec(mode, query, scoutParticipantName, origin, spec)
	if err != nil {
		return nil, false, err
	}

	return map[string]any{
		"ok":       true,
		"mode":     mode,
		"query":    query,
		"thread":   thread,
		"artifact": thread.Artifact,
		"actions":  thread.Actions,
	}, false, nil
}

func normalizeRealtimeArtifactMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "artifact", "artifacts":
		return "artifacts"
	case "research", "design", "grill", "workflow":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return ""
	}
}

func (app *kanbanBoardApp) rememberTranscript(roomID string, event kanbanRealtimeEvent, source string, model string) {
	if app == nil || app.memory == nil {
		log.Errorf("Meeting memory unavailable; transcript was not saved")
		return
	}
	if !app.transcriptRecordingActiveInRoom(roomID) {
		log.Infof("Transcript recording disabled; transcript was not saved")
		return
	}

	speaker, confidence := app.speakerForCommittedTranscriptForRoom(roomID, time.Now().UTC())
	entry, appended, err := app.memory.appendAttributedTranscriptEntry(roomID, event.EventID, event.ItemID, speaker, confidence, event.Transcript, map[string]string{
		"source": source,
		"model":  model,
	}, false, "")
	if err != nil {
		log.Errorf("Failed to write meeting memory: %v", err)
		return
	}
	if !appended {
		return
	}

	broadcastAssistantEvent("transcript", "heard: "+entry.Text, nil)
	broadcastRoomKanbanEvent(roomID, "memory_transcript", entry)
	// A3: wake the meeting-brain worker so it re-checks THIS ROOM's window on
	// this fresh transcript instead of waiting up to a full brain interval (W4
	// §7.4: nudges carry the room). The worker debounces (a buffered wake) and,
	// once it appends, cascades the room-carrying nudge to the board / decision
	// / mission / narrative workers that consume brains.
	app.nudgeAmbientAgentForRoom(meetingBrainAgentName, roomID)
	// Wake-word presence cue (VISUAL only — no auto-arming): a transcript
	// naming Scout pulses the brand mark / voice island on room clients.
	// Detection lives only here, so typed room chat never pulses, and the
	// recording toggle gates it (no transcripts = no presence). §7.3: a
	// listen-only sitting has no Scout — the wake pulse is skipped too.
	if scoutWakePattern.MatchString(entry.Text) && !app.sittingListenOnly(roomID) {
		broadcastAssistantEvent("wake", "Scout heard its name", map[string]any{"speaker": speaker})
	}
}

// scoutWakePattern spots Scout's name as a whole word inside a transcript —
// "scouting" and "discount" never match.
var scoutWakePattern = regexp.MustCompile(`(?i)\bscout\b`)

const (
	// maxRoomChatMessageRunes caps a single typed room-chat message.
	maxRoomChatMessageRunes = 4000
	// roomChatHistoryLimit is how many chat messages replay to a newly
	// admitted participant.
	roomChatHistoryLimit = 50
)

// normalizeRoomChatText trims a typed chat message and enforces the server
// size cap (rune-safe so multi-byte text never splits mid-character).
func normalizeRoomChatText(text string) string {
	text = strings.TrimSpace(text)
	if runes := []rune(text); len(runes) > maxRoomChatMessageRunes {
		text = strings.TrimSpace(string(runes[:maxRoomChatMessageRunes]))
	}
	return text
}

// recordRoomChatMessage persists a typed room-chat message into the
// transcript stream so it flows into brain/board analysis and meeting
// archives. Unlike spoken transcripts it ignores the recording toggle —
// typing is an explicit act — and bypasses the filler filter. It mirrors
// rememberTranscript's broadcast pattern and returns the room_chat broadcast
// payload. Speaker is passed explicitly; never speakerForCompletedTranscript,
// which would steal attribution state from the audio pipeline.
func (app *kanbanBoardApp) recordRoomChatMessage(roomID string, senderName string, text string) (map[string]any, bool) {
	return app.recordRoomChatMessageWithMetadata(roomID, senderName, text, nil)
}

// recordRoomChatMessageWithArtifact posts a room-chat message that carries a
// finished artifact reference — the close-the-loop delivery card. It rides
// the same transcript-entering path as typed chat, so the brain/board
// workers and meeting archives see the delivery too. expectedMeetingID gates
// the append atomically on that meeting still being active (empty = ungated):
// the delivery seam passes the origin meeting id so a rotation racing the
// delivery can never mint a phantom meeting or leak into the successor.
func (app *kanbanBoardApp) recordRoomChatMessageWithArtifact(roomID string, senderName string, text string, artifactID string, expectedMeetingID string) (map[string]any, bool) {
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return app.recordRoomChatMessage(roomID, senderName, text)
	}
	// Scout is not a canonical meeting participant, so the transcript path's
	// speaker normalization drops it; the explicit metadata fallback keeps the
	// card attributed (a canonical sender still wins inside the append).
	return app.recordRoomChatMessageForMeeting(roomID, senderName, text, map[string]string{
		"artifactId": artifactID,
		"speaker":    strings.TrimSpace(senderName),
	}, expectedMeetingID)
}

func (app *kanbanBoardApp) recordRoomChatMessageWithMetadata(roomID string, senderName string, text string, extraMetadata map[string]string) (map[string]any, bool) {
	return app.recordRoomChatMessageForMeeting(roomID, senderName, text, extraMetadata, "")
}

func (app *kanbanBoardApp) recordRoomChatMessageForMeeting(roomID string, senderName string, text string, extraMetadata map[string]string, expectedMeetingID string) (map[string]any, bool) {
	if app == nil || app.memory == nil {
		log.Errorf("Meeting memory unavailable; room chat message was not saved")
		return nil, false
	}
	text = normalizeRoomChatText(text)
	if text == "" {
		return nil, false
	}

	id := durableTimestampID("chat", time.Now())
	entry, appended, err := app.memory.appendRoomChatTranscriptForMeeting(roomID, id, senderName, text, extraMetadata, expectedMeetingID)
	if err != nil {
		log.Errorf("Failed to write room chat to meeting memory: %v", err)
		return nil, false
	}
	if !appended {
		return nil, false
	}

	broadcastAssistantEvent("transcript", "heard: "+entry.Text, nil)
	broadcastRoomKanbanEvent(roomID, "memory_transcript", entry)
	// A3: typed room chat is a brain input too — wake the brain worker for THIS
	// ROOM the same way spoken transcripts do so a text-only exchange is not
	// left un-brained until the next floor tick (W4 §7.4: nudges carry the room).
	app.nudgeAmbientAgentForRoom(meetingBrainAgentName, roomID)
	return roomChatEventPayload(entry), true
}

// roomChatEventPayload shapes a persisted chat entry into the room_chat wire
// payload; the stored text carries the "Speaker: " transcript prefix, which
// the payload strips because the author rides in the name field.
func roomChatEventPayload(entry meetingMemoryEntry) map[string]any {
	name := strings.TrimSpace(entry.Metadata["speaker"])
	text := entry.Text
	if name != "" {
		prefix := name + ":"
		if len(text) > len(prefix) && strings.EqualFold(text[:len(prefix)], prefix) {
			text = strings.TrimSpace(text[len(prefix):])
		}
	}
	payload := map[string]any{
		"id":        entry.ID,
		"name":      name,
		"text":      text,
		"createdAt": entry.CreatedAt.UTC().Format(time.RFC3339Nano),
		// W5 §8.2: every room_chat frame carries its room so the client's
		// roomId filter can scope it — a named room's recap delivery
		// (broadcastRoomKanbanEvent, recap.go) has no other stamp.
		"roomId": normalizeRoomID(entry.Metadata["roomId"]),
	}
	// Completion-delivery messages carry the artifact id so the client can
	// render a "view report" chip on the bubble.
	if artifactID := strings.TrimSpace(entry.Metadata["artifactId"]); artifactID != "" {
		payload["artifactId"] = artifactID
	}
	// The session-identity stamp: own-message detection (and the delete
	// affordance) keys on this, never on the mutable display name.
	if authorEmail := normalizeAccountEmail(entry.Metadata["authorEmail"]); authorEmail != "" {
		payload["authorEmail"] = authorEmail
	}
	return payload
}

// deleteRoomChatMessage removes one persisted room-chat transcript entry — the
// misplaced-message escape hatch. Identity comes from the session, never the
// payload: the requester must be the message's author. The authorEmail stamp
// wins; entries persisted before the stamp existed fall back to a
// case-insensitive speaker-name match. Returns the room_chat_delete broadcast
// payload.
func (app *kanbanBoardApp) deleteRoomChatMessage(entryID string, requesterEmail string, requesterName string) (map[string]any, bool) {
	if app == nil || app.memory == nil {
		return nil, false
	}
	entryID = strings.TrimSpace(entryID)
	if entryID == "" {
		return nil, false
	}
	entry, ok := app.memory.entryByID(entryID)
	if !ok || entry.Kind != meetingMemoryKindTranscript || entry.Metadata["source"] != transcriptSourceRoomChat {
		return nil, false
	}
	if !roomChatEntryAuthoredBy(entry, requesterEmail, requesterName) {
		return nil, false
	}
	removed, deleted, err := app.memory.deleteEntryByID(entryID)
	if err != nil || !deleted {
		if err != nil {
			log.Errorf("Failed to delete room chat message %s: %v", entryID, err)
		}
		return nil, false
	}
	// Tombstone the delete (memory study 1.5b, gap #13): the message CONTENT is
	// gone — that is the point of a delete — but a dated, recall-hidden stub
	// records the FACT that the author removed it, so the deletion is never
	// trace-free. Mirrors the slop-quarantine expiry-stub discipline; best-effort,
	// so a stub write failure never blocks the delete itself.
	deleter := firstNonEmptyString(participantNameForEmail(requesterEmail), strings.TrimSpace(requesterName), normalizeAccountEmail(requesterEmail), "a member")
	tombstoneMetadata := map[string]string{
		relevanceMetadataKey: relevanceExpired,
		"deletedId":          entryID,
		"deletedKind":        removed.Kind,
		"deletedBy":          deleter,
		"deletedAt":          time.Now().UTC().Format(time.RFC3339Nano),
		"roomId":             firstNonEmptyString(normalizeRoomID(removed.Metadata["roomId"]), officeRoomID),
	}
	if meetingID := strings.TrimSpace(removed.Metadata["meetingId"]); meetingID != "" {
		tombstoneMetadata["meetingId"] = meetingID
	}
	tombstoneID := fmt.Sprintf("chat-delete-%s", entryID)
	if _, _, tombErr := app.memory.appendChatDeleteTombstone(tombstoneID, "Room chat message deleted by "+deleter, tombstoneMetadata); tombErr != nil {
		log.Errorf("Failed to write chat-delete tombstone for %s: %v", entryID, tombErr)
	}
	return map[string]any{"id": entryID}, true
}

// roomChatEntryAuthoredBy is the room-chat delete authz check.
func roomChatEntryAuthoredBy(entry meetingMemoryEntry, requesterEmail string, requesterName string) bool {
	if authorEmail := normalizeAccountEmail(entry.Metadata["authorEmail"]); authorEmail != "" {
		return authorEmail == normalizeAccountEmail(requesterEmail)
	}
	speaker := strings.TrimSpace(entry.Metadata["speaker"])
	return speaker != "" && strings.EqualFold(speaker, strings.TrimSpace(requesterName))
}

// roomChatHistory returns the newest room-chat messages of the office's
// current meeting, oldest first, shaped like room_chat broadcast payloads.
func (app *kanbanBoardApp) roomChatHistory(limit int) []map[string]any {
	return app.roomChatHistoryForRoom(officeRoomID, limit)
}

// roomChatHistoryForRoom is roomChatHistory scoped to one room's live meeting.
func (app *kanbanBoardApp) roomChatHistoryForRoom(roomID string, limit int) []map[string]any {
	history := []map[string]any{}
	if app == nil || app.memory == nil {
		return history
	}

	entries := app.memory.snapshotForMeeting(app.memory.currentMeetingID(normalizeRoomID(roomID)), 0)
	chats := make([]meetingMemoryEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Kind != meetingMemoryKindTranscript || entry.Metadata["source"] != transcriptSourceRoomChat {
			continue
		}
		chats = append(chats, entry)
	}
	for _, entry := range tailMemoryEntries(chats, limit) {
		history = append(history, roomChatEventPayload(entry))
	}
	return history
}

func (app *kanbanBoardApp) memorySnapshot(limit int) []meetingMemoryEntry {
	if app == nil || app.memory == nil {
		return nil
	}

	return visibleMeetingMemoryEntries(app.memory.snapshot(0), limit)
}

func (app *kanbanBoardApp) memorySnapshotForMeeting(meetingID string, limit int) []meetingMemoryEntry {
	if app == nil || app.memory == nil {
		return nil
	}

	return visibleMeetingMemoryEntries(app.memory.snapshotForMeeting(meetingID, 0), limit)
}

func visibleMeetingMemoryEntries(entries []meetingMemoryEntry, limit int) []meetingMemoryEntry {
	visible := make([]meetingMemoryEntry, 0, len(entries))
	for _, entry := range entries {
		// codex_proposal entries render as dedicated confirm/dismiss cards
		// (codex_proposal events), never as generic memory-timeline noise;
		// mission_insight JSON is UI state served via /assistant/mission;
		// decisions render in the intel canvas ledger (and decision_pass is
		// pure cursor bookkeeping); package records render in the intel
		// canvas binder; narrative dossiers render as storylines on the intel
		// surface and run_log lines as thread cards/notifications — searchable
		// knowledge (the decision precedent). Digest rollups (strict JSON),
		// reflections, and cursor stubs stay recall/bookkeeping material: the
		// briefing surfaces read digests through the range helpers, not this
		// feed. None of them are timeline entries.
		if entry.Kind == meetingMemoryKindScoutChat || entry.Kind == meetingMemoryKindCodexProposal || entry.Kind == meetingMemoryKindMissionInsight || entry.Kind == meetingMemoryKindDecision || entry.Kind == meetingMemoryKindDecisionPass || entry.Kind == meetingMemoryKindPackage || entry.Kind == meetingMemoryKindDealRoom || entry.Kind == meetingMemoryKindFile || entry.Kind == meetingMemoryKindReflection || entry.Kind == meetingMemoryKindDayDigestPass || entry.Kind == meetingMemoryKindLedgerEvent || entry.Kind == meetingMemoryKindLedgerPass || entry.Kind == meetingMemoryKindNarrative || entry.Kind == meetingMemoryKindRunLog || isMeetingDigestKind(entry.Kind) {
			continue
		}
		visible = append(visible, entry)
	}
	return tailMemoryEntries(visible, limit)
}

// memorySnapshotForClients decorates archive entries with a keyed download
// URL at serve time so archive links keep working behind the archives auth
// gate without persisting the room password into the store.
func (app *kanbanBoardApp) memorySnapshotForClients(limit int) []meetingMemoryEntry {
	entries := app.memorySnapshot(limit)
	projected := make([]meetingMemoryEntry, 0, len(entries))
	for index := range entries {
		// Artifact identity, metadata, and bodies are principal-scoped objects.
		// Shared HTTP/websocket memory bootstraps omit them entirely; clients load
		// authorized artifact windows through /artifacts.
		if entries[index].Kind == meetingMemoryKindOSArtifact {
			continue
		}
		// bodies are already prompt-capped at the snapshot boundary
		// (stripOversizeBody in visibleEntriesLocked); re-apply as
		// belt-and-suspenders because this payload is broadcast to every
		// client on each memory event AND rides buildOSAssistantModeAnswer
		// prompts. The artifact stage never reads bodies from this lane —
		// it loads full bodies via /artifacts (osArtifactsSnapshot).
		projected = append(projected, decorateArchiveDownloadURLForClient(stripOversizeBody(entries[index])))
	}

	return projected
}

func decorateArchiveDownloadURLForClient(entry meetingMemoryEntry) meetingMemoryEntry {
	if entry.Metadata == nil {
		return entry
	}
	archiveID := strings.TrimSpace(entry.Metadata["archiveId"])
	if archiveID == "" {
		return entry
	}
	metadata := make(map[string]string, len(entry.Metadata)+1)
	for key, value := range entry.Metadata {
		metadata[key] = value
	}
	metadata["downloadUrl"] = meetingArchiveDownloadURLWithKey(archiveID)
	entry.Metadata = metadata
	return entry
}

func (app *kanbanBoardApp) answerMemoryQuestion(args map[string]any) (map[string]any, bool, error) {
	return app.answerMemoryQuestionForPrincipal(args, sharedRoomRecallPrincipal(officeRoomID, app.memory.currentMeetingID(officeRoomID)), true)
}

var recallBroadcastProbe func(RecallPrincipal, string)

func (app *kanbanBoardApp) answerMemoryQuestionForPrincipal(args map[string]any, principal RecallPrincipal, emit bool) (map[string]any, bool, error) {
	query := canonicalizeBoardText(asString(args["query"]))
	if query == "" {
		return nil, false, fmt.Errorf("query is required")
	}
	if app == nil || app.memory == nil {
		return nil, false, fmt.Errorf("meeting memory is unavailable")
	}

	recallApp := app.scopedRecallApp(context.Background(), principal)
	matches, contextEntries := recallApp.memoryMatchesAndContext(query)
	if recallModelContextProbe != nil {
		recallModelContextProbe(contextEntries)
	}
	answer, modelErr := recallApp.answerMemoryQuestionWithModel(query, contextEntries)
	if modelErr != nil {
		log.Errorf("Failed to answer memory question with model: %v", modelErr)
	}
	if strings.TrimSpace(answer) == "" {
		// Wave 6: a time-ranged briefing question degrades to the composed
		// digest/ledger briefing (then on-demand map-reduce over raw memory);
		// A5 keeps current-state questions on the deterministic ledger fold.
		// Only queries neither lane serves keep the 8-keyword-hit last resort.
		if briefingAnswer, ok := recallApp.rangedBriefingAnswer(query); ok {
			answer = briefingAnswer
		} else if ledgerAnswer, ok := recallApp.ledgerStatusAnswer(query); ok {
			answer = ledgerAnswer
		} else {
			answer = buildMemoryAnswer(query, matches)
		}
	}
	response := map[string]any{
		"query":  query,
		"answer": answer,
	}

	if emit {
		if recallBroadcastProbe != nil {
			recallBroadcastProbe(principal, answer)
		}
		roomID := normalizeRoomID(principal.RoomID)
		broadcastRoomKanbanEvent(roomID, "memory_answer", response)
		broadcastRoomKanbanEvent(roomID, "assistant_event", map[string]any{
			"kind": "answer", "text": answer, "query": query, "createdAt": time.Now().UTC().Format(time.RFC3339Nano),
		})
	}

	return map[string]any{
		"ok":      true,
		"query":   query,
		"answer":  answer,
		"matches": len(matches),
		"context": len(contextEntries),
	}, false, nil
}

// noteForTheRecordTool files the deliberate-write "for the record" entry (memory
// study 2.1, gap #5): an author-certain, recall-eligible note that lets a stance
// argued to Scout reach company memory WITHOUT breaching the private-thread
// privacy contract — the explicit tool call IS the consent surface, so ambient
// thread text stays out while a deliberately-filed note enters.
//
// certainAuthor is the signed-in session user (private-voice requester,
// agent-thread requestedBy); it is empty on the shared room-voice path, where no
// single author exists and the note is filed author-uncertain. idScope
// namespaces the idempotency id (thread/session) so identical notes in different
// threads stay distinct while a double-call in the SAME scope files exactly once
// via the store seen-map — the run-log id discipline.
//
// Per-path idScope semantics (F14 — accidental double-fires dedupe, deliberate
// re-filing later works):
//   - orchestrator: "agent-thread:<threadID>:<toolCallID>" — the tool-call id is
//     stable across a transport retry (same assistant block) but distinct on a
//     genuine re-emission, so it needs no time component.
//   - private-voice / room-voice: no per-call id exists, so hourBucketedNoteScope
//     appends a UTC hour bucket. Two identical notes in the same hour file once;
//     the same note filed a later hour is a fresh, deliberate record — never a
//     permanent forever-dedupe.
//
// kind=decision routes the statement onto the decision ledger as a PROPOSED
// decision (the directional-decision path) the team can ratify, attributed to a
// named owner or the certain author; anything else files a note. Never mutates
// the board, so changed is always false.
// hourBucketedNoteScope namespaces a note idempotency scope that lacks a natural
// per-call id (room voice, private voice) with a UTC hour bucket (F14): accidental
// double-fires within the hour dedupe against the store seen-map, but the same
// note filed a later hour is a deliberate, distinct record. Injectable clock so
// the bucket boundary is testable.
func hourBucketedNoteScope(namespace string, at time.Time) string {
	return namespace + "@" + at.UTC().Format("2006-01-02T15")
}

func (app *kanbanBoardApp) noteForTheRecordTool(args map[string]any, certainAuthor string, idScope string) (map[string]any, bool, error) {
	if app == nil || app.memory == nil {
		return nil, false, fmt.Errorf("meeting memory is unavailable")
	}
	note := normalizeMemoryText(asString(args["note"]))
	if note == "" {
		return nil, false, fmt.Errorf("note is required")
	}
	topic := normalizeMemoryText(asString(args["topic"]))
	ownerRaw := normalizeMemoryText(asString(args["owner"]))
	kind := strings.ToLower(strings.TrimSpace(asString(args["kind"])))
	if kind != "decision" {
		kind = "note"
	}

	// The author is stamped from the SESSION user, never a transcript heuristic:
	// a signed-in requester resolves to a roster display name when possible, else
	// their own email — either way a certain identity. No session user (room
	// voice) files author-uncertain.
	certain := strings.TrimSpace(certainAuthor)
	author := ""
	authorCertain := false
	if certain != "" {
		author = firstNonEmptyString(participantNameForEmail(certain), normalizeAccountEmail(certain), certain)
		authorCertain = true
	}

	// Deterministic id: identical (scope, author, kind, topic, note) files once.
	sum := sha256.Sum256([]byte(strings.Join([]string{idScope, author, kind, topic, note}, "\x00")))
	id := "note-" + hex.EncodeToString(sum[:])[:16]

	// Pre-stamp the meetingId (the live office sitting, or "none" at idle) so a
	// note filed outside a meeting never lazily opens a phantom sitting — the
	// files.go grandfather-marker discipline.
	meetingID := firstNonEmptyString(app.memory.currentMeetingID(officeRoomID), "none")

	if kind == "decision" {
		// Attribution discipline (F12/F27): when an owner is NAMED, attribute the
		// stance ONLY if it grounds to a real participant — a non-roster name is
		// NEVER silently replaced by the filer, which would fabricate a stance in
		// the who-thinks-what position index ("remember Jordan wants X" filed as if
		// the filer holds it). Ungroundable → madeBy stays blank (the decision
		// ledger's own unknown-name discipline; the statement text still names the
		// party). The filer-fallback is reserved for the NO-owner case: filing one's
		// own stance ("for the record, I want us to pass").
		madeBy := ""
		ownerUngrounded := false
		if ownerRaw != "" {
			madeBy = normalizeTranscriptSpeaker(ownerRaw)
			ownerUngrounded = madeBy == ""
		} else {
			madeBy = author
		}
		metadata := map[string]string{
			"madeBy":        madeBy,
			"context":       firstNonEmptyString(topic, "filed via note_for_the_record"),
			"dedupeKey":     decisionDedupeKey(note),
			"status":        decisionStatusProposed,
			"source":        "note_for_the_record",
			"authorCertain": strconv.FormatBool(authorCertain),
			"roomId":        officeRoomID,
			"meetingId":     meetingID,
		}
		if author != "" {
			metadata["filedBy"] = author
		}
		entry, appended, err := app.memory.appendDecision(id, note, metadata)
		if err != nil {
			return nil, false, err
		}
		if appended {
			broadcastOfficeKanbanEvent("decision", decisionPayload(entry))
			broadcastSignedInKanbanEvent("memory", nil)
			broadcastAssistantEvent("action", "Scout recorded a decision for the record.", map[string]any{"kind": "decision"})
		}
		result := map[string]any{
			"ok":       true,
			"kind":     "decision",
			"id":       entry.ID,
			"status":   decisionStatusProposed,
			"madeBy":   madeBy,
			"recorded": appended,
		}
		if ownerUngrounded {
			// Honest tool message: the named owner could not be grounded, so the
			// decision is on record but attributed to nobody rather than to the filer.
			result["message"] = "Recorded as a proposed decision, but \"" + ownerRaw + "\" is not a known team member, so no owner attribution was set (the statement text still names them)."
		}
		return result, false, nil
	}

	metadata := map[string]string{
		"author":        author,
		"authorCertain": strconv.FormatBool(authorCertain),
		"source":        "note_for_the_record",
		"roomId":        officeRoomID,
		"meetingId":     meetingID,
	}
	if topic != "" {
		metadata["topic"] = topic
	}
	if ownerRaw != "" {
		metadata["owner"] = ownerRaw
	}
	entry, appended, err := app.memory.appendNote(id, note, metadata)
	if err != nil {
		return nil, false, err
	}
	if appended {
		broadcastSignedInKanbanEvent("memory", nil)
		broadcastAssistantEvent("action", "Scout filed a note for the record.", map[string]any{"kind": "note"})
	}
	return map[string]any{
		"ok":       true,
		"kind":     "note",
		"id":       entry.ID,
		"author":   author,
		"recorded": appended,
	}, false, nil
}

// organizeFilesTool files visible Files-surface rows into a named folder —
// the Scout seam over the file_folders.go store, the same laws as the HTTP
// doors. requesterEmail scopes row visibility exactly like GET
// /assistant/files (an empty requester — the shared room dispatch — sees
// team-visible rows only). Matching is fuzzy: case-insensitive substring per
// supplied name fragment; the folder is created on demand unless
// createIfMissing is explicitly false.
func (app *kanbanBoardApp) organizeFilesTool(args map[string]any, requesterEmail string) (map[string]any, bool, error) {
	folderName := strings.Join(strings.Fields(asString(args["folderName"])), " ")
	if folderName == "" {
		return nil, false, fmt.Errorf("folderName is required")
	}
	if app == nil || app.memory == nil {
		return nil, false, fmt.Errorf("files are unavailable")
	}
	createIfMissing := true
	if raw, ok := args["createIfMissing"].(bool); ok {
		createIfMissing = raw
	}

	var folder fileFolderRecord
	found := false
	for _, candidate := range listFileFolders() {
		if strings.EqualFold(candidate.Name, folderName) {
			folder = candidate
			found = true
			break
		}
	}
	created := false
	if !found {
		if !createIfMissing {
			return map[string]any{
				"ok":     false,
				"reason": fmt.Sprintf("folder %q does not exist and createIfMissing is false", folderName),
			}, false, nil
		}
		actor := firstNonEmptyString(participantNameForEmail(requesterEmail), scoutParticipantName)
		newFolder, err := createFileFolder(folderName, actor)
		if err != nil {
			return nil, false, fmt.Errorf("create folder %q: %w", folderName, err)
		}
		folder = newFolder
		created = true
	}

	rows := app.assistantFilesForUser(requesterEmail)
	requested := asStringSlice(args["fileNames"])
	moved := make([]string, 0, len(requested))
	unmatched := make([]string, 0)
	filed := map[string]struct{}{}
	for _, fragment := range requested {
		fragment = strings.TrimSpace(fragment)
		if fragment == "" {
			continue
		}
		lowered := strings.ToLower(fragment)
		matchedAny := false
		for _, row := range rows {
			if !strings.Contains(strings.ToLower(row.Name), lowered) {
				continue
			}
			matchedAny = true
			// overlapping fragments ("samsung", "samsung deck") file a row once.
			if _, dup := filed[row.ID]; dup {
				continue
			}
			if err := moveFileToFolder(row.ID, folder.ID); err != nil {
				log.Errorf("organize_files failed to move %s into %s: %v", row.ID, folder.ID, err)
				continue
			}
			filed[row.ID] = struct{}{}
			moved = append(moved, row.Name)
		}
		if !matchedAny {
			unmatched = append(unmatched, fragment)
		}
	}

	summaryParts := make([]string, 0, 3)
	if created {
		summaryParts = append(summaryParts, fmt.Sprintf("Created folder %q.", folder.Name))
	}
	if len(moved) > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("Filed %d file%s into %q: %s.", len(moved), pluralSuffix(len(moved)), folder.Name, strings.Join(moved, ", ")))
	} else {
		summaryParts = append(summaryParts, fmt.Sprintf("No files were moved into %q.", folder.Name))
	}
	if len(unmatched) > 0 {
		summaryParts = append(summaryParts, "No match for: "+strings.Join(unmatched, ", ")+".")
	}
	summary := strings.Join(summaryParts, " ")

	if created || len(moved) > 0 {
		// The Files surface refresh event the folder HTTP doors fan out.
		broadcastSignedInKanbanEvent("file", map[string]any{"kind": "folders"})
	}

	return map[string]any{
		"ok":        true,
		"folder":    folder.Name,
		"folderId":  folder.ID,
		"created":   created,
		"moved":     moved,
		"unmatched": unmatched,
		"summary":   summary,
	}, false, nil
}

// saveToFilesTool is the Scout seam over the explicit-save gate (card-110): it
// matches INTEL deliverables (the whole os_artifact library, NOT just the
// already-visible Files rows — an unsaved deliverable is exactly what this
// exists to surface) by title/threadQuery fragment and stamps each match
// savedToFiles=true through saveDeliverableToFiles, optionally filing them
// under a folder in the same call. requesterEmail supplies the savedToFilesBy
// attribution.
func (app *kanbanBoardApp) saveToFilesTool(args map[string]any, requesterEmail string) (map[string]any, bool, error) {
	if app == nil || app.memory == nil {
		return nil, false, fmt.Errorf("files are unavailable")
	}
	fragments := asStringSlice(args["fileNames"])
	if len(fragments) == 0 {
		return nil, false, fmt.Errorf("fileNames is required")
	}
	actor := firstNonEmptyString(participantNameForEmail(requesterEmail), scoutParticipantName)

	// Optional destination folder — resolved (or created on demand) exactly like
	// organize_files, then passed to every save so the file lands filed.
	folderName := strings.Join(strings.Fields(asString(args["folderName"])), " ")
	folderID := ""
	folderCreated := false
	if folderName != "" {
		found := false
		for _, candidate := range listFileFolders() {
			if strings.EqualFold(candidate.Name, folderName) {
				folderID = candidate.ID
				found = true
				break
			}
		}
		if !found {
			newFolder, err := createFileFolder(folderName, actor)
			if err != nil {
				return nil, false, fmt.Errorf("create folder %q: %w", folderName, err)
			}
			folderID = newFolder.ID
			folderCreated = true
		}
	}

	requester := &userAccount{Email: normalizeAccountEmail(requesterEmail), Name: participantNameForEmail(requesterEmail)}
	deliverables := app.authorizedFileDeliverableCandidates(context.Background(), requester, ACLReadContent, ACLWrite)
	saved := make([]string, 0, len(fragments))
	unmatched := make([]string, 0)
	done := map[string]struct{}{}
	for _, fragment := range fragments {
		fragment = strings.TrimSpace(fragment)
		if fragment == "" {
			continue
		}
		lowered := strings.ToLower(fragment)
		matchedAny := false
		for _, entry := range deliverables {
			if !deliverableRecordQualifies(entry) {
				continue
			}
			title := strings.TrimSpace(entry.Metadata["title"])
			threadQuery := strings.TrimSpace(entry.Metadata["threadQuery"])
			if !strings.Contains(strings.ToLower(title), lowered) && !strings.Contains(strings.ToLower(threadQuery), lowered) {
				continue
			}
			matchedAny = true
			// overlapping fragments ("samsung", "samsung deck") save a row once.
			if _, dup := done[entry.ID]; dup {
				continue
			}
			row, err := app.saveDeliverableSnapshotToFiles(entry, folderID, actor)
			if err != nil {
				log.Errorf("save_to_files failed to save %s: %v", entry.ID, err)
				continue
			}
			done[entry.ID] = struct{}{}
			saved = append(saved, firstNonEmptyString(row.Name, title, threadQuery))
		}
		if !matchedAny {
			unmatched = append(unmatched, fragment)
		}
	}

	summaryParts := make([]string, 0, 3)
	if folderCreated {
		summaryParts = append(summaryParts, fmt.Sprintf("Created folder %q.", folderName))
	}
	if len(saved) > 0 {
		if folderName != "" {
			summaryParts = append(summaryParts, fmt.Sprintf("Saved %d deliverable%s to Files in %q: %s.", len(saved), pluralSuffix(len(saved)), folderName, strings.Join(saved, ", ")))
		} else {
			summaryParts = append(summaryParts, fmt.Sprintf("Saved %d deliverable%s to Files: %s.", len(saved), pluralSuffix(len(saved)), strings.Join(saved, ", ")))
		}
	} else {
		summaryParts = append(summaryParts, "No deliverables were saved to Files.")
	}
	if len(unmatched) > 0 {
		summaryParts = append(summaryParts, "No match for: "+strings.Join(unmatched, ", ")+".")
	}
	summary := strings.Join(summaryParts, " ")

	if len(saved) > 0 || folderCreated {
		// The Files surface refresh event every files mutation fans out.
		broadcastSignedInKanbanEvent("file", map[string]any{"kind": "folders"})
	}

	return map[string]any{
		"ok":        true,
		"saved":     saved,
		"unmatched": unmatched,
		"folder":    folderName,
		"folderId":  folderID,
		"created":   folderCreated,
		"summary":   summary,
	}, false, nil
}

func (app *kanbanBoardApp) createTicket(args map[string]any) (map[string]any, bool, error) {
	title := canonicalizeBoardText(asString(args["title"]))
	if title == "" {
		return nil, false, fmt.Errorf("title is required")
	}

	notes := cleanBoardNotes(asString(args["notes"]))
	owner := normalizeCardOwner(args["owner"])
	if owner == "" {
		owner = "Unassigned"
	}
	tags := canonicalizeBoardTags(asStringSlice(args["tags"]))
	dueDate, _ := dueDateFromArgs(args)
	keyDates, _ := keyDatesFromArgs(args)
	if dueDate != "" {
		keyDates = mergeKanbanKeyDates(keyDates, kanbanKeyDate{Label: "due", Date: dueDate})
	} else {
		dueDate = dueDateFromKeyDates(keyDates)
	}
	status := kanbanStatusBacklog
	if rawStatus, ok := args["status"]; ok {
		// A status outside the canon (even after alias mapping) must never
		// drop the card: the card matters more than its column, so unknown
		// spellings default to Backlog instead of erroring out the create.
		if parsedStatus, err := parseKanbanStatus(rawStatus); err == nil {
			status = parsedStatus
		}
	}

	// Scout-drafted cards (board worker, D4) land as pending drafts a human
	// accepts or dismisses; the manual create path strips this flag so
	// human-created cards are never drafts.
	draft := asBool(args["draft"])

	app.mu.Lock()
	defer app.mu.Unlock()

	card := kanbanCard{
		ID:       app.createCardIDLocked(),
		Status:   status,
		Title:    title,
		Notes:    notes,
		Owner:    owner,
		Tags:     tags,
		DueDate:  dueDate,
		KeyDates: keyDates,
	}
	if draft {
		card.Draft = true
		card.DraftedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	app.cards = append(app.cards, card)
	if err := app.touchLocked(); err != nil {
		return nil, false, err
	}

	return map[string]any{
		"ok":      true,
		"created": true,
		"card":    cloneKanbanCard(card),
	}, true, nil
}

// acceptDraftTicket converts a Scout draft into a normal board card (D4).
// The card keeps its column and owner (Unassigned until someone claims it).
func (app *kanbanBoardApp) acceptDraftTicket(args map[string]any) (map[string]any, bool, error) {
	cardID := asString(args["card_id"])
	if cardID == "" {
		return nil, false, fmt.Errorf("card_id is required")
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	card, ok := app.findCardLocked(cardID)
	if !ok {
		return nil, false, fmt.Errorf("unknown card_id: %s", cardID)
	}
	if !card.Draft {
		return nil, false, fmt.Errorf("card %s is not a draft", cardID)
	}
	card.Draft = false
	card.DraftedAt = ""
	if err := app.touchLocked(); err != nil {
		return nil, false, err
	}

	return map[string]any{
		"ok":       true,
		"accepted": true,
		"card_id":  cardID,
		"card":     cloneKanbanCard(*card),
	}, true, nil
}

// dismissDraftTicket removes a Scout draft from the board (D4). Dismissed
// drafts never counted as board cards, so they do not enter the undo slot.
func (app *kanbanBoardApp) dismissDraftTicket(args map[string]any) (map[string]any, bool, error) {
	cardID := asString(args["card_id"])
	if cardID == "" {
		return nil, false, fmt.Errorf("card_id is required")
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	index := -1
	for candidateIndex, card := range app.cards {
		if card.ID == cardID {
			index = candidateIndex
			break
		}
	}
	if index == -1 {
		return nil, false, fmt.Errorf("unknown card_id: %s", cardID)
	}
	if !app.cards[index].Draft {
		return nil, false, fmt.Errorf("card %s is not a draft", cardID)
	}
	dismissedCard := cloneKanbanCard(app.cards[index])
	app.cards = append(app.cards[:index], app.cards[index+1:]...)
	if err := app.touchLocked(); err != nil {
		return nil, false, err
	}

	return map[string]any{
		"ok":        true,
		"dismissed": true,
		"card_id":   cardID,
		"card":      dismissedCard,
	}, true, nil
}

func (app *kanbanBoardApp) moveTicket(args map[string]any) (map[string]any, bool, error) {
	cardID, err := app.resolveCardIDArg(args)
	if err != nil {
		return nil, false, err
	}

	status, err := parseKanbanStatus(args["status"])
	if err != nil {
		return nil, false, err
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	card, ok := app.findCardLocked(cardID)
	if !ok {
		return nil, false, fmt.Errorf("unknown card_id: %s", cardID)
	}
	if card.Status == status {
		return map[string]any{
			"ok":      true,
			"moved":   false,
			"card_id": cardID,
			"status":  status,
		}, false, nil
	}
	card.Status = status
	if err := app.touchLocked(); err != nil {
		return nil, false, err
	}

	return map[string]any{
		"ok":      true,
		"moved":   true,
		"card_id": cardID,
		"status":  status,
	}, true, nil
}

func (app *kanbanBoardApp) addTags(args map[string]any) (map[string]any, bool, error) {
	cardID, err := app.resolveCardIDArg(args)
	if err != nil {
		return nil, false, err
	}

	tags := canonicalizeBoardTags(asStringSlice(args["tags"]))
	if len(tags) == 0 {
		return nil, false, fmt.Errorf("tags are required")
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	card, ok := app.findCardLocked(cardID)
	if !ok {
		return nil, false, fmt.Errorf("unknown card_id: %s", cardID)
	}
	nextTags := uniqueStrings(append(card.Tags, tags...))
	if stringSlicesEqual(card.Tags, nextTags) {
		return map[string]any{
			"ok":         true,
			"tags_added": false,
			"card_id":    cardID,
			"tags":       append([]string(nil), tags...),
		}, false, nil
	}
	card.Tags = nextTags
	if err := app.touchLocked(); err != nil {
		return nil, false, err
	}

	return map[string]any{
		"ok":         true,
		"tags_added": true,
		"card_id":    cardID,
		"tags":       append([]string(nil), tags...),
	}, true, nil
}

func (app *kanbanBoardApp) addKeyDate(args map[string]any) (map[string]any, bool, error) {
	cardID := asString(args["card_id"])
	if cardID == "" {
		return nil, false, fmt.Errorf("card_id is required")
	}

	keyDate, ok := normalizeKanbanKeyDate(asString(args["label"]), asString(args["date"]))
	if !ok {
		return nil, false, fmt.Errorf("label and date are required")
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	card, ok := app.findCardLocked(cardID)
	if !ok {
		return nil, false, fmt.Errorf("unknown card_id: %s", cardID)
	}

	nextKeyDates := mergeKanbanKeyDates(card.KeyDates, keyDate)
	nextDueDate := card.DueDate
	if keyDateIsDue(keyDate) {
		nextDueDate = keyDate.Date
	}
	if kanbanKeyDatesEqual(card.KeyDates, nextKeyDates) && card.DueDate == nextDueDate {
		return map[string]any{
			"ok":       true,
			"added":    false,
			"card_id":  cardID,
			"key_date": keyDate,
			"card":     cloneKanbanCard(*card),
		}, false, nil
	}

	card.KeyDates = nextKeyDates
	card.DueDate = nextDueDate
	if err := app.touchLocked(); err != nil {
		return nil, false, err
	}

	return map[string]any{
		"ok":       true,
		"added":    true,
		"card_id":  cardID,
		"key_date": keyDate,
		"card":     cloneKanbanCard(*card),
	}, true, nil
}

func (app *kanbanBoardApp) removeKeyDates(args map[string]any) (map[string]any, bool, error) {
	cardID := asString(args["card_id"])
	if cardID == "" {
		return nil, false, fmt.Errorf("card_id is required")
	}

	labels := normalizeKeyDateLabels(asStringSlice(args["labels"]))
	removeAll := asBool(args["remove_all"]) || asBool(args["removeAll"]) || len(labels) == 0

	app.mu.Lock()
	defer app.mu.Unlock()

	card, ok := app.findCardLocked(cardID)
	if !ok {
		return nil, false, fmt.Errorf("unknown card_id: %s", cardID)
	}

	nextKeyDates := filterKanbanKeyDates(card.KeyDates, labels, removeAll)
	nextDueDate := card.DueDate
	if removeAll || keyDateLabelsIncludeDue(labels) {
		nextDueDate = dueDateFromKeyDates(nextKeyDates)
	}
	if kanbanKeyDatesEqual(card.KeyDates, nextKeyDates) && card.DueDate == nextDueDate {
		return map[string]any{
			"ok":         true,
			"removed":    false,
			"card_id":    cardID,
			"remove_all": removeAll,
			"labels":     labels,
			"card":       cloneKanbanCard(*card),
		}, false, nil
	}

	card.KeyDates = nextKeyDates
	card.DueDate = nextDueDate
	if err := app.touchLocked(); err != nil {
		return nil, false, err
	}

	return map[string]any{
		"ok":         true,
		"removed":    true,
		"card_id":    cardID,
		"remove_all": removeAll,
		"labels":     labels,
		"card":       cloneKanbanCard(*card),
	}, true, nil
}

func (app *kanbanBoardApp) updateTicket(args map[string]any) (map[string]any, bool, error) {
	// When card_id is absent the title names the target card (exact,
	// case-insensitive) rather than a rename; the rename path below then
	// leaves the card's title effectively unchanged (casing at most).
	cardID, err := app.resolveCardIDArg(args)
	if err != nil {
		return nil, false, err
	}

	title := canonicalizeBoardText(asString(args["title"]))
	notes := cleanBoardNotes(asString(args["notes"]))
	owner := normalizeCardOwner(args["owner"])
	tags := canonicalizeBoardTags(asStringSlice(args["tags"]))
	dueDate, hasDueDate := dueDateFromArgs(args)
	keyDates, hasKeyDates := keyDatesFromArgs(args)
	replaceKeyDates := asBool(args["replace_key_dates"]) || asBool(args["replaceKeyDates"])
	if replaceKeyDates && !hasKeyDates {
		hasKeyDates = true
	}
	var status kanbanStatus
	if rawStatus, ok := args["status"]; ok && asString(rawStatus) != "" {
		parsedStatus, err := parseKanbanStatus(rawStatus)
		if err != nil {
			return nil, false, err
		}
		status = parsedStatus
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	card, ok := app.findCardLocked(cardID)
	if !ok {
		return nil, false, fmt.Errorf("unknown card_id: %s", cardID)
	}
	changed := false
	if title != "" && card.Title != title {
		card.Title = title
		changed = true
	}
	if notes != "" && card.Notes != notes {
		card.Notes = notes
		changed = true
	}
	if owner != "" && card.Owner != owner {
		card.Owner = owner
		changed = true
	}
	if status != "" && card.Status != status {
		card.Status = status
		changed = true
	}
	if len(tags) > 0 {
		nextTags := uniqueStrings(append(card.Tags, tags...))
		if !stringSlicesEqual(card.Tags, nextTags) {
			card.Tags = nextTags
			changed = true
		}
	}
	if hasKeyDates {
		nextKeyDates := card.KeyDates
		if replaceKeyDates {
			nextKeyDates = keyDates
		} else if len(keyDates) > 0 {
			nextKeyDates = mergeKanbanKeyDates(card.KeyDates, keyDates...)
		}
		if !kanbanKeyDatesEqual(card.KeyDates, nextKeyDates) {
			card.KeyDates = nextKeyDates
			changed = true
		}
		if replaceKeyDates {
			keyDatesDueDate := dueDateFromKeyDates(nextKeyDates)
			if card.DueDate != keyDatesDueDate {
				card.DueDate = keyDatesDueDate
				changed = true
			}
		} else if keyDatesDueDate := dueDateFromKeyDates(keyDates); keyDatesDueDate != "" && card.DueDate != keyDatesDueDate {
			card.DueDate = keyDatesDueDate
			changed = true
		}
	}
	if hasDueDate {
		if dueDate != "" {
			nextKeyDates := mergeKanbanKeyDates(card.KeyDates, kanbanKeyDate{Label: "due", Date: dueDate})
			if !kanbanKeyDatesEqual(card.KeyDates, nextKeyDates) {
				card.KeyDates = nextKeyDates
				changed = true
			}
		}
		if card.DueDate != dueDate {
			card.DueDate = dueDate
			changed = true
		}
	}
	if !changed {
		return map[string]any{
			"ok":      true,
			"updated": false,
			"card_id": cardID,
			"card":    cloneKanbanCard(*card),
		}, false, nil
	}
	if err := app.touchLocked(); err != nil {
		return nil, false, err
	}

	return map[string]any{
		"ok":      true,
		"updated": true,
		"card_id": cardID,
		"card":    cloneKanbanCard(*card),
	}, true, nil
}

func (app *kanbanBoardApp) updateTicketDetails(args map[string]any) (map[string]any, bool, error) {
	cardID := asString(args["card_id"])
	if cardID == "" {
		return nil, false, fmt.Errorf("card_id is required")
	}

	title := canonicalizeBoardText(asString(args["title"]))
	if title == "" {
		return nil, false, fmt.Errorf("title is required")
	}
	status, err := parseKanbanStatus(args["status"])
	if err != nil {
		return nil, false, err
	}
	owner := normalizeCardOwner(args["owner"])
	if owner == "" {
		owner = "Unassigned"
	}
	notes := cleanBoardNotes(asString(args["notes"]))
	tags := canonicalizeBoardTags(asStringSlice(args["tags"]))
	dueDate, hasDueDate := dueDateFromArgs(args)
	keyDates, hasKeyDates := keyDatesFromArgs(args)

	app.mu.Lock()
	defer app.mu.Unlock()

	card, ok := app.findCardLocked(cardID)
	if !ok {
		return nil, false, fmt.Errorf("unknown card_id: %s", cardID)
	}
	nextDueDate := card.DueDate
	nextKeyDates := cloneKanbanKeyDates(card.KeyDates)
	if hasKeyDates || hasDueDate {
		if hasKeyDates {
			nextKeyDates = keyDates
		}
		if hasDueDate {
			nextDueDate = dueDate
			if dueDate != "" {
				nextKeyDates = mergeKanbanKeyDates(nextKeyDates, kanbanKeyDate{Label: "due", Date: dueDate})
			}
		} else {
			nextDueDate = dueDateFromKeyDates(nextKeyDates)
		}
	}
	if card.Title == title &&
		card.Status == status &&
		card.Owner == owner &&
		card.Notes == notes &&
		stringSlicesEqual(card.Tags, tags) &&
		card.DueDate == nextDueDate &&
		kanbanKeyDatesEqual(card.KeyDates, nextKeyDates) {
		return map[string]any{
			"ok":      true,
			"updated": false,
			"card_id": cardID,
		}, false, nil
	}
	card.Title = title
	card.Status = status
	card.Owner = owner
	card.Notes = notes
	card.Tags = tags
	card.DueDate = nextDueDate
	card.KeyDates = nextKeyDates
	if err := app.touchLocked(); err != nil {
		return nil, false, err
	}

	return map[string]any{
		"ok":      true,
		"updated": true,
		"card_id": cardID,
	}, true, nil
}

func (app *kanbanBoardApp) deleteTicket(args map[string]any) (map[string]any, bool, error) {
	cardID := asString(args["card_id"])
	if cardID == "" {
		return nil, false, fmt.Errorf("card_id is required")
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	index := -1
	for candidateIndex, card := range app.cards {
		if card.ID == cardID {
			index = candidateIndex
			break
		}
	}
	if index == -1 {
		return nil, false, fmt.Errorf("unknown card_id: %s", cardID)
	}
	deletedCard := cloneKanbanCard(app.cards[index])
	app.lastDeletedCard = &deletedCard
	app.cards = append(app.cards[:index], app.cards[index+1:]...)
	if err := app.touchLocked(); err != nil {
		return nil, false, err
	}

	return map[string]any{
		"ok":      true,
		"deleted": true,
		"card_id": cardID,
	}, true, nil
}

func (app *kanbanBoardApp) restoreLastDeletedTicket() (map[string]any, bool, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	if app.lastDeletedCard == nil {
		return nil, false, fmt.Errorf("no deleted ticket to restore")
	}

	restoredCard := cloneKanbanCard(*app.lastDeletedCard)
	if _, exists := app.findCardLocked(restoredCard.ID); exists {
		restoredCard.ID = app.createCardIDLocked()
	}
	app.cards = append(app.cards, restoredCard)
	app.lastDeletedCard = nil
	if err := app.touchLocked(); err != nil {
		return nil, false, err
	}

	return map[string]any{
		"ok":       true,
		"restored": true,
		"card_id":  restoredCard.ID,
	}, true, nil
}

func (app *kanbanBoardApp) canUndoDelete() bool {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.lastDeletedCard != nil
}

func (app *kanbanBoardApp) snapshotState() kanbanBoardState {
	app.mu.Lock()
	defer app.mu.Unlock()

	state := kanbanBoardState{
		Cards: cloneKanbanCards(app.cards),
	}
	if !app.updatedAt.IsZero() {
		state.UpdatedAt = app.updatedAt.UTC().Format(time.RFC3339Nano)
	}

	return state
}

// defaultMaxEndpointsPerUser bounds how many concurrent devices one account may
// hold in the room at once. Two lets the mandated laptop+phone case work while
// keeping fan-out on the small VPS predictable.
const defaultMaxEndpointsPerUser = 2

// maxEndpointsPerUser reads BONFIRE_MAX_ENDPOINTS_PER_USER, defaulting to and
// flooring at defaultMaxEndpointsPerUser so a misconfigured value never drops
// below the single-device guarantee.
func maxEndpointsPerUser() int {
	raw := strings.TrimSpace(os.Getenv("BONFIRE_MAX_ENDPOINTS_PER_USER"))
	if raw == "" {
		return defaultMaxEndpointsPerUser
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return defaultMaxEndpointsPerUser
	}
	return value
}

func (app *kanbanBoardApp) admitParticipant(name string) (string, error) {
	return app.admitParticipantSession(name, "")
}

// admitParticipantSession preserves the original one-session-per-name contract
// for callers (and tests) that do not carry a device endpoint id: the empty
// endpoint id collapses to a single shared slot.
func (app *kanbanBoardApp) admitParticipantSession(name string, sessionID string) (string, error) {
	admitted, _, err := app.admitParticipantSessionEndpoint(name, sessionID, "")
	return admitted, err
}

// admitParticipantSessionEndpoint admits (or refreshes) one endpoint of an
// account in the office — the legacy single-room contract every existing
// caller and test relies on. Room-scoped admission (multi-room W3) rides
// admitParticipantSessionEndpointInRoom.
func (app *kanbanBoardApp) admitParticipantSessionEndpoint(name string, sessionID string, endpointID string) (admitted string, firstEndpoint bool, err error) {
	return app.admitParticipantSessionEndpointInRoom(officeRoomID, name, sessionID, endpointID)
}

// admitParticipantSessionEndpointInRoom admits (or refreshes) one endpoint of
// an account in one room. Capacity is counted per distinct name, so a person
// on two devices still consumes a single seat; the number of concurrent
// endpoints one account may hold is bounded by maxEndpointsPerUser so fan-out
// stays affordable. The returned firstEndpoint is true only when this
// admission brought the account from absent to present, so callers can
// announce a genuine "joined" to the room without firing a spurious join
// every time a second device connects. Guest seats arrive here too, with the
// server-minted "Guest " display name (canonicalRoomParticipantName).
func (app *kanbanBoardApp) admitParticipantSessionEndpointInRoom(roomID string, name string, sessionID string, endpointID string) (admitted string, firstEndpoint bool, err error) {
	name = canonicalRoomParticipantName(name)
	if name == "" {
		return "", false, fmt.Errorf("choose a listed participant and enter the room password")
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)

	capacity := configuredMeetingRoomCapacity()
	alreadyPresent := state.participantCounts[name] > 0
	if !alreadyPresent && app.activeParticipantCountInRoomLocked(state) >= capacity {
		return "", false, fmt.Errorf("the room is full. this room supports %d people with video on", capacity)
	}

	endpoints := state.participantEndpoints[name]
	if endpoints == nil {
		endpoints = map[string]string{}
		state.participantEndpoints[name] = endpoints
	}
	previousSessionID, endpointExisted := endpoints[endpointID]
	if !endpointExisted && len(endpoints) >= maxEndpointsPerUser() {
		return "", false, fmt.Errorf("you're already connected from %d devices. leave one to join here", maxEndpointsPerUser())
	}
	endpoints[endpointID] = sessionID

	now := time.Now().UTC()
	if state.participantSessionLiveness == nil {
		state.participantSessionLiveness = map[string]map[string]time.Time{}
	}
	sessionLiveness := state.participantSessionLiveness[name]
	if sessionLiveness == nil {
		sessionLiveness = map[string]time.Time{}
		state.participantSessionLiveness[name] = sessionLiveness
	}
	// Refreshing one endpoint replaces its prior session. Retire the old stamp
	// before installing the new one so a later stale-session cleanup can never
	// match or remove the refreshed endpoint.
	if previousSessionID != "" && previousSessionID != sessionID {
		delete(sessionLiveness, previousSessionID)
	}
	if sessionID != "" {
		sessionLiveness[sessionID] = now
	}
	state.participants[name] = now
	state.participantCounts[name] = len(endpoints)
	// Reset the shared media state on a fresh account or when an endpoint's own
	// session reconnects (a refreshed tab), but NOT when an additional device
	// joins an already-present account — otherwise the first device's mute/
	// camera state would be clobbered by the second device's arrival.
	if !alreadyPresent || endpointExisted {
		state.participantMedia[name] = participantMediaState{
			UpdatedAt: now.Format(time.RFC3339Nano),
		}
	}

	return name, !alreadyPresent, nil
}

func (app *kanbanBoardApp) forgetParticipant(name string) {
	app.forgetParticipantSession(name, "")
}

// forgetParticipantSession drops one session. With an empty sessionID it clears
// the whole account (the forgetParticipant path). With a real sessionID it only
// removes the endpoint that currently holds that session; a stale session that
// has already been replaced is a no-op (returns false), and other endpoints of
// the same account are left untouched.
func (app *kanbanBoardApp) forgetParticipantSession(name string, sessionID string) bool {
	removed, _ := app.forgetParticipantSessionResult(name, sessionID)
	return removed
}

// forgetParticipantSessionResult is forgetParticipantSession with presence
// bookkeeping: stillPresent reports whether the account still holds another live
// endpoint after this removal, so callers announce "left" only when the last
// device is gone — not when one of a person's two devices disconnects.
func (app *kanbanBoardApp) forgetParticipantSessionResult(name string, sessionID string) (removed bool, stillPresent bool) {
	return app.forgetParticipantSessionResultInRoom(officeRoomID, name, sessionID)
}

func (app *kanbanBoardApp) forgetParticipantSessionResultInRoom(roomID string, name string, sessionID string) (removed bool, stillPresent bool) {
	name = canonicalRoomParticipantName(name)
	if name == "" {
		return false, false
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)

	endpoints := state.participantEndpoints[name]

	if sessionID != "" {
		matchedEndpoint := ""
		matched := false
		for endpointID, storedSessionID := range endpoints {
			if storedSessionID == sessionID {
				matchedEndpoint = endpointID
				matched = true
				break
			}
		}
		if !matched {
			return false, state.participantCounts[name] > 0
		}
		delete(endpoints, matchedEndpoint)
		if sessionLiveness := state.participantSessionLiveness[name]; sessionLiveness != nil {
			delete(sessionLiveness, sessionID)
			if len(sessionLiveness) == 0 {
				delete(state.participantSessionLiveness, name)
			}
		}
		if len(endpoints) > 0 {
			state.participantCounts[name] = len(endpoints)
			state.participants[name] = time.Now().UTC()
			return true, true
		}
	}

	delete(state.participantCounts, name)
	delete(state.participants, name)
	delete(state.participantEndpoints, name)
	delete(state.participantSessionLiveness, name)
	delete(state.participantMedia, name)

	return true, false
}

// participantSessionCurrent reports whether the given session is still the live
// session for its endpoint. A session stays current until a newer session
// replaces it on the SAME endpoint (a refreshed tab); a second device with its
// own endpoint id never invalidates the first device's session.
func (app *kanbanBoardApp) participantSessionCurrent(name string, sessionID string) bool {
	return app.participantSessionCurrentInRoom(officeRoomID, name, sessionID)
}

func (app *kanbanBoardApp) participantSessionCurrentInRoom(roomID string, name string, sessionID string) bool {
	name = canonicalRoomParticipantName(name)
	if name == "" {
		return false
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)

	if state.participantCounts[name] <= 0 {
		return false
	}
	if sessionID == "" {
		return true
	}

	for _, storedSessionID := range state.participantEndpoints[name] {
		if storedSessionID == sessionID {
			return true
		}
	}

	return false
}

// touchParticipantLiveness is the legacy account-level wrapper. Callers that
// know the socket session must use touchParticipantSessionLivenessInRoom so one
// healthy endpoint cannot keep another endpoint's zombie transport alive.
func (app *kanbanBoardApp) touchParticipantLiveness(name string) {
	app.touchParticipantLivenessInRoom(officeRoomID, name)
}

func (app *kanbanBoardApp) touchParticipantLivenessInRoom(roomID string, name string) {
	app.touchParticipantSessionLivenessInRoom(roomID, name, "")
}

// touchParticipantSessionLivenessInRoom stamps one admitted socket's
// endpoint-scoped proof of life (an inbound app-level room frame). A late frame
// from a replaced/removed session is ignored, so it cannot resurrect presence
// or keep the replacement's endpoint alive. The account-level stamp remains an
// aggregate and a compatibility fallback for legacy in-memory state.
func (app *kanbanBoardApp) touchParticipantSessionLivenessInRoom(roomID string, name string, sessionID string) {
	name = canonicalRoomParticipantName(name)
	if name == "" {
		return
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)

	if state.participantCounts[name] <= 0 {
		return
	}
	now := time.Now().UTC()
	if sessionID == "" {
		// A legacy caller has no session identity. Preserve its historical
		// account-level semantics by refreshing every current endpoint; the main
		// room read loop uses the session-aware method above.
		state.participants[name] = now
		if state.participantSessionLiveness == nil {
			state.participantSessionLiveness = map[string]map[string]time.Time{}
		}
		sessionLiveness := state.participantSessionLiveness[name]
		if sessionLiveness == nil {
			sessionLiveness = map[string]time.Time{}
			state.participantSessionLiveness[name] = sessionLiveness
		}
		for _, currentSessionID := range state.participantEndpoints[name] {
			if currentSessionID != "" {
				sessionLiveness[currentSessionID] = now
			}
		}
		return
	}

	current := false
	for _, currentSessionID := range state.participantEndpoints[name] {
		if currentSessionID == sessionID {
			current = true
			break
		}
	}
	if !current {
		return
	}
	if state.participantSessionLiveness == nil {
		state.participantSessionLiveness = map[string]map[string]time.Time{}
	}
	sessionLiveness := state.participantSessionLiveness[name]
	if sessionLiveness == nil {
		sessionLiveness = map[string]time.Time{}
		state.participantSessionLiveness[name] = sessionLiveness
	}
	sessionLiveness[sessionID] = now
	state.participants[name] = now
}

// participantLivenessReap describes the stale endpoints removed from one
// account. stillPresent distinguishes a partial endpoint reap from the last
// endpoint leaving, so the caller tears down every stale session but only
// announces participant_left when the account actually leaves the roster.
type participantLivenessReap struct {
	name            string
	sessionIDs      []string
	reapedEndpoints int
	stillPresent    bool
}

// reapStaleParticipantSessionsLocked drops each stale endpoint independently.
// A healthy laptop therefore no longer keeps the same account's frozen phone
// session, PeerConnection, and forwarded tracks alive. Account presence/media
// survives while any endpoint remains; the legacy account-level stamp is used
// when endpoint stamps do not exist. Callers hold app.mu. Deleting from ranged
// maps during iteration is safe in Go.
func (app *kanbanBoardApp) reapStaleParticipantSessionsLocked(now time.Time, timeout time.Duration) map[string][]participantLivenessReap {
	reapedByRoom := map[string][]participantLivenessReap{}
	for roomID, state := range app.roomLive {
		for name, accountLastSeen := range state.participants {
			if state.participantCounts[name] <= 0 {
				continue
			}

			endpoints := state.participantEndpoints[name]
			sessionLiveness := state.participantSessionLiveness[name]
			accountStale := now.Sub(accountLastSeen) > timeout
			type staleEndpoint struct {
				endpointID string
				sessionID  string
			}
			stale := make([]staleEndpoint, 0, len(endpoints))
			if len(endpoints) == 0 {
				// Compatibility for legacy/runtime state that predates endpoint
				// bookkeeping and carries only the account liveness stamp.
				if accountStale {
					stale = append(stale, staleEndpoint{})
				}
			} else {
				for endpointID, sessionID := range endpoints {
					lastSeen, stamped := sessionLiveness[sessionID]
					// An explicit session stamp is authoritative even if a legacy/
					// inconsistent aggregate is stale; otherwise a fresh laptop could
					// still be evicted with a zombie phone. Only endpoints without a
					// session stamp fall back to the aggregate timestamp.
					isStale := accountStale
					if stamped {
						isStale = now.Sub(lastSeen) > timeout
					}
					if !isStale {
						continue
					}
					stale = append(stale, staleEndpoint{endpointID: endpointID, sessionID: sessionID})
				}
			}
			if len(stale) == 0 {
				continue
			}

			sessionIDs := make([]string, 0, len(stale))
			seenSessions := map[string]bool{}
			for _, endpoint := range stale {
				sessionID := endpoint.sessionID
				if endpoint.endpointID != "" || len(endpoints) > 0 {
					delete(endpoints, endpoint.endpointID)
				}
				if sessionID != "" && !seenSessions[sessionID] {
					seenSessions[sessionID] = true
					sessionIDs = append(sessionIDs, sessionID)
				}
				delete(sessionLiveness, sessionID)
				// memberRepairBuckets / memberIceRestartBuckets key on the
				// per-socket participant session id (not the guest seat key), so
				// release each reaped session here — the read-loop defer's
				// dropMember*Bucket calls never ran for a wedged socket, and
				// app.mu is already held so we drop directly rather than
				// re-locking through the helpers.
				delete(state.memberRepairBuckets, sessionID)
				delete(state.memberIceRestartBuckets, sessionID)
			}
			if len(sessionLiveness) == 0 {
				delete(state.participantSessionLiveness, name)
			}
			if len(endpoints) > 0 {
				state.participantCounts[name] = len(endpoints)
				// Keep the aggregate/legacy stamp honest after a partial reap: it
				// is the newest proof of life among surviving stamped sessions. A
				// surviving legacy endpoint has no individual stamp, so preserve a
				// newer aggregate for it rather than manufacturing an older value.
				newestRemaining := time.Time{}
				legacyEndpointRemains := false
				for _, sessionID := range endpoints {
					lastSeen, stamped := sessionLiveness[sessionID]
					if !stamped {
						legacyEndpointRemains = true
						continue
					}
					if newestRemaining.IsZero() || lastSeen.After(newestRemaining) {
						newestRemaining = lastSeen
					}
				}
				if legacyEndpointRemains && accountLastSeen.After(newestRemaining) {
					newestRemaining = accountLastSeen
				}
				if !newestRemaining.IsZero() {
					state.participants[name] = newestRemaining
				}
				reapedByRoom[roomID] = append(reapedByRoom[roomID], participantLivenessReap{
					name:            name,
					sessionIDs:      sessionIDs,
					reapedEndpoints: len(stale),
					stillPresent:    true,
				})
				continue
			}
			delete(state.participantCounts, name)
			delete(state.participants, name)
			delete(state.participantEndpoints, name)
			delete(state.participantSessionLiveness, name)
			delete(state.participantMedia, name)
			if isGuestDisplayName(name) {
				for sessionKey, display := range state.guestSeats {
					if strings.EqualFold(display, name) {
						delete(state.guestSeats, sessionKey)
						delete(state.chatBuckets, sessionKey)
						delete(state.mediaStateBuckets, sessionKey)
						delete(state.telemetryBuckets, sessionKey)
						delete(state.guestIceRestartBuckets, sessionKey)
					}
				}
			}
			reapedByRoom[roomID] = append(reapedByRoom[roomID], participantLivenessReap{
				name:            name,
				sessionIDs:      sessionIDs,
				reapedEndpoints: len(stale),
			})
		}
	}
	return reapedByRoom
}

// sweepStaleParticipantSessions is the periodic backstop to the per-socket
// read-deadline cleanup: it reaps accounts whose room socket has gone silent
// past participantLivenessTimeout, so activeParticipantCount() reaches zero even
// when a wedged read loop never runs its onclose defer or a backgrounded tab
// keeps a half-open socket nominally open. On any reap it tears down the
// half-open peer connections, refreshes the roster, and arms the empty-room idle
// end via noteMeetingOccupancy (a no-op while any live account remains). A brief
// drop that reconnects re-stamps liveness on admission well inside the timeout,
// so a rejoiner is never reaped and the sitting never finalizes underneath them.
func (app *kanbanBoardApp) sweepStaleParticipantSessions() {
	if app == nil {
		return
	}
	app.mu.Lock()
	reapedByRoom := app.reapStaleParticipantSessionsLocked(time.Now().UTC(), participantLivenessTimeout)
	app.mu.Unlock()
	if len(reapedByRoom) == 0 {
		return
	}
	for roomID, reaped := range reapedByRoom {
		for _, entry := range reaped {
			for _, sessionID := range entry.sessionIDs {
				// Close the peer connections + sockets BEFORE clearing the
				// bookkeeping: closeSessionMedia looks the PCs up by session id
				// in peerConnections/activeParticipantConnections, which
				// unregisterParticipantSession is about to delete. pc.Close()
				// releases the ICE/DTLS/SRTP transports and errors the read
				// pumps so their goroutines exit — the teardown a wedged read
				// loop's onclose defer never got to run. (Mirrors the archive
				// path's closeSessionSockets-then-unregister ordering.)
				closeSessionMedia(sessionID)
				unregisterParticipantSession(entry.name, sessionID)
			}
			if entry.stillPresent {
				log.Infof("participant_endpoint_liveness_reap participant=%s room=%s endpoints=%d sessions=%d remaining=true pcpool=%d; endpoint socket silent past %s", entry.name, roomID, entry.reapedEndpoints, len(entry.sessionIDs), peerConnectionPoolSize(), participantLivenessTimeout)
				continue
			}
			log.Infof("participant_liveness_reap participant=%s room=%s endpoints=%d sessions=%d pcpool=%d; last endpoint silent past %s", entry.name, roomID, entry.reapedEndpoints, len(entry.sessionIDs), peerConnectionPoolSize(), participantLivenessTimeout)
			broadcastRoomKanbanEvent(roomID, "participant_left", map[string]any{"name": entry.name, "roomId": roomID})
		}
		broadcastRoomKanbanEvent(roomID, "participants", app.roomSnapshotForRoom(roomID))
		// occupancy may now be zero: arm the empty-room idle end so the sitting
		// finalizes and the next entry mints a fresh meeting id.
		app.noteMeetingOccupancy(roomID)
	}
	broadcastRoomsSnapshot()
}

// startParticipantLivenessSweeper launches the backstop sweep on a ticker for
// the process lifetime. Started once from server boot (main), never from the
// test constructor, so tests drive sweepStaleParticipantSessions directly.
func (app *kanbanBoardApp) startParticipantLivenessSweeper() {
	if app == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(participantLivenessSweepInterval)
		defer ticker.Stop()
		for range ticker.C {
			app.sweepStaleParticipantSessions()
		}
	}()
}

// activeParticipantCount reports the room's live seat count (multi-room W3:
// real per-room presence — a room with no live plane has no occupants).
func (app *kanbanBoardApp) activeParticipantCount(roomID string) int {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.activeParticipantCountInRoomLocked(app.roomLiveLocked(roomID))
}

func (app *kanbanBoardApp) activeParticipantCountLocked() int {
	return app.activeParticipantCountInRoomLocked(app.roomLiveLocked(officeRoomID))
}

func (app *kanbanBoardApp) activeParticipantCountInRoomLocked(state *roomLiveState) int {
	active := 0
	for _, count := range state.participantCounts {
		if count > 0 {
			active++
		}
	}

	return active
}

// participantEndpointCountsLocked reports how many concurrent devices each
// in-room account currently holds, so the roster can render a subtle
// "· 2 devices" affordance for a person on more than one endpoint. Callers must
// hold app.mu.
func (app *kanbanBoardApp) participantEndpointCountsLocked(state *roomLiveState) map[string]int {
	counts := make(map[string]int, len(state.participantEndpoints))
	for name, endpoints := range state.participantEndpoints {
		if count := len(endpoints); count > 0 {
			counts[name] = count
		}
	}
	return counts
}

func (app *kanbanBoardApp) participantSnapshot() []string {
	return app.participantSnapshotForRoom(officeRoomID)
}

// participantSnapshotForRoom lists the room's present participants — the W4
// room-scoped agents feed their prompts THE ROOM's roster, not the office's.
func (app *kanbanBoardApp) participantSnapshotForRoom(roomID string) []string {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.participantSnapshotLocked(app.roomLiveLocked(roomID))
}

// participantSnapshotLocked lists present participants: roster members in
// canonical order first, then guest seats sorted by display name.
func (app *kanbanBoardApp) participantSnapshotLocked(state *roomLiveState) []string {
	participants := make([]string, 0, len(state.participants))
	for _, candidate := range meetingParticipantNames {
		if state.participantCounts[candidate] > 0 {
			participants = append(participants, candidate)
		}
	}
	guests := make([]string, 0, len(state.guestSeats))
	for name := range state.participants {
		if isGuestDisplayName(name) && state.participantCounts[name] > 0 {
			guests = append(guests, name)
		}
	}
	sort.Strings(guests)
	participants = append(participants, guests...)

	return participants
}

func (app *kanbanBoardApp) roomSnapshot() map[string]any {
	return app.roomSnapshotForRoom(officeRoomID)
}

func (app *kanbanBoardApp) roomSnapshotForRoom(roomID string) map[string]any {
	capacity := configuredMeetingRoomCapacity()

	app.mu.Lock()
	defer app.mu.Unlock()

	return app.roomSnapshotLockedForRoom(app.roomLiveLocked(roomID), capacity)
}

func (app *kanbanBoardApp) setParticipantMediaState(name string, state participantMediaState) (map[string]any, error) {
	return app.setParticipantMediaStateInRoom(officeRoomID, name, state)
}

func (app *kanbanBoardApp) setParticipantMediaStateInRoom(roomID string, name string, state participantMediaState) (map[string]any, error) {
	name = canonicalRoomParticipantName(name)
	if name == "" {
		return nil, fmt.Errorf("unknown participant")
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	room := app.roomLiveLocked(roomID)

	if room.participantCounts[name] <= 0 {
		return nil, fmt.Errorf("%s is not in the room", name)
	}

	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	room.participantMedia[name] = state

	return app.roomSnapshotLockedForRoom(room, configuredMeetingRoomCapacity()), nil
}

func (app *kanbanBoardApp) setParticipantScreenSharing(name string, screenSharing bool) map[string]any {
	return app.setParticipantScreenSharingInRoom(officeRoomID, name, screenSharing)
}

func (app *kanbanBoardApp) setParticipantScreenSharingInRoom(roomID string, name string, screenSharing bool) map[string]any {
	name = canonicalRoomParticipantName(name)
	if name == "" {
		return app.roomSnapshotForRoom(roomID)
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	room := app.roomLiveLocked(roomID)

	if room.participantCounts[name] <= 0 {
		return app.roomSnapshotLockedForRoom(room, configuredMeetingRoomCapacity())
	}

	state := room.participantMedia[name]
	state.ScreenSharing = screenSharing
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	room.participantMedia[name] = state

	return app.roomSnapshotLockedForRoom(room, configuredMeetingRoomCapacity())
}

func (app *kanbanBoardApp) transcriptRecordingActive() bool {
	return app.transcriptRecordingActiveInRoom(officeRoomID)
}

func (app *kanbanBoardApp) transcriptRecordingActiveInRoom(roomID string) bool {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.roomLiveLocked(roomID).recordingEnabled
}

func (app *kanbanBoardApp) setTranscriptRecording(enabled bool, updatedBy string) map[string]any {
	return app.setTranscriptRecordingInRoom(officeRoomID, enabled, updatedBy)
}

func (app *kanbanBoardApp) setTranscriptRecordingInRoom(roomID string, enabled bool, updatedBy string) map[string]any {
	// System actors (the §6.5 guest transcription cap) keep their verbatim
	// stamp; everything else normalizes through the roster like before.
	if !strings.HasPrefix(updatedBy, "system:") {
		updatedBy = canonicalRoomActorName(updatedBy)
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(roomID)

	if state.recordingEnabled != enabled || state.recordingUpdatedAt.IsZero() {
		state.recordingEnabled = enabled
		state.recordingUpdatedAt = time.Now().UTC()
		state.recordingUpdatedBy = updatedBy
		// A6: freeze runs at every commit but the pop (rememberTranscript ->
		// speakerForCommittedTranscript) is skipped while recording is off. Clear the
		// FIFO on any recording toggle so a window frozen across the boundary cannot
		// drift attribution once recording resumes. (Already holding app.mu here.)
		state.pendingAttributionWindows = nil
		// §6.5: a member flipping recording back on in a guest-enabled room
		// grants another transcription-cap window for the live sitting.
		if enabled && state.id != officeRoomID && state.mixer != nil {
			if room, ok := appRoomStore().byID(state.id); ok && room.GuestEnabled {
				app.armGuestTranscriptionCapLocked(state, state.mediaGen)
			}
		}
	}
	if !enabled && state.id == officeRoomID {
		app.scoutVoiceArmedAt = time.Time{}
		app.scoutVoiceArmedUntil = time.Time{}
		app.scoutSpokenResponse = false
		app.scoutSpokenResponseSent = false
		app.scoutLastToolResultAt = time.Time{}
		app.scoutLastToolResultName = ""
	}

	return app.roomSnapshotLockedForRoom(state, configuredMeetingRoomCapacity())
}

func roomRecordingAnnouncementText(recording roomRecordingState) string {
	actor := canonicalRoomActorName(recording.UpdatedBy)
	if actor == "" {
		actor = scoutParticipantName
	}
	action := "turned meeting recording on"
	state := "on"
	if !recording.Enabled {
		action = "turned meeting recording off"
		state = "off"
	}
	if actor == scoutParticipantName {
		return fmt.Sprintf("Scout: meeting recording is %s for the room.", state)
	}
	return fmt.Sprintf("Scout: %s %s for the room.", actor, action)
}

func (app *kanbanBoardApp) roomRecordingStateLocked(room *roomLiveState) roomRecordingState {
	state := roomRecordingState{
		Enabled:   room.recordingEnabled,
		UpdatedBy: room.recordingUpdatedBy,
	}
	if !room.recordingUpdatedAt.IsZero() {
		state.UpdatedAt = room.recordingUpdatedAt.UTC().Format(time.RFC3339Nano)
	}

	return state
}

func (app *kanbanBoardApp) roomSnapshotLocked(capacity int) map[string]any {
	return app.roomSnapshotLockedForRoom(app.roomLiveLocked(officeRoomID), capacity)
}

func (app *kanbanBoardApp) roomSnapshotLockedForRoom(room *roomLiveState, capacity int) map[string]any {
	participants := app.participantSnapshotLocked(room)
	occupiedSeats := app.activeParticipantCountInRoomLocked(room)

	availableSeats := capacity - occupiedSeats
	if availableSeats < 0 {
		availableSeats = 0
	}
	mediaStates := make(map[string]participantMediaState, len(participants))
	for _, participant := range participants {
		mediaStates[participant] = room.participantMedia[participant]
	}

	return map[string]any{
		"roomId":         room.id,
		"participants":   participants,
		"capacity":       capacity,
		"occupiedSeats":  occupiedSeats,
		"availableSeats": availableSeats,
		"mediaStates":    mediaStates,
		"endpointCounts": app.participantEndpointCountsLocked(room),
		"recording":      app.roomRecordingStateLocked(room),
	}
}

func (app *kanbanBoardApp) archiveMeeting(archivedBy string) (meetingArchiveResult, error) {
	// force-end an active grill FIRST so the grill Q&A lands in the archive,
	// the report window closes cleanly, and normal instructions are restored.
	app.endGrillSessionForArchive()
	// flush ambient agents first so the final minutes of the meeting are
	// summarized and applied to the board before the snapshot is taken.
	app.flushAmbientAgentsForArchive()

	archivedBy = canonicalRoomActorName(archivedBy)
	archivedAt := time.Now().UTC()
	archiveID := durableTimestampID("meeting", archivedAt)
	meetingID := ""
	if app.memory != nil {
		meetingID = app.memory.currentMeetingID(officeRoomID)
	}
	board := app.snapshotState()
	memory := app.memorySnapshotForMeeting(meetingID, 2000)
	participants := app.participantSnapshot()
	if len(participants) == 0 && archivedBy != "" {
		participants = []string{archivedBy}
	}
	// Snapshot the meeting record for the archive embed (title, real
	// startedAt, participants union) WITHOUT closing it yet: the record only
	// ends after the archive is durably written, so a failed write never
	// strands an ended record whose archiveId 404s while the room keeps
	// talking on an un-rotated memory id. The end stamps below mirror exactly
	// what endMeeting will persist after the first successful write.
	var closedMeeting *meetingRecord
	if record, ok := app.meetings.activeRecord(officeRoomID); ok && record.ID == meetingID {
		pending := record
		pending.EndedAt = archivedAt.Format(time.RFC3339Nano)
		pending.EndedReason = meetingEndedReasonArchive
		pending.ArchiveID = archiveID
		closedMeeting = &pending
	} else if record, _ := app.meetings.endMeeting(meetingID, archivedAt, meetingEndedReasonArchive, archiveID); record.ID != "" {
		// Defensive: the id matches an ALREADY-ENDED record (endMeeting is
		// idempotent, changed=false) — embed it as stored.
		closedRecord := record
		closedMeeting = &closedRecord
	}
	notes := buildMeetingNotes(archiveID, archivedAt, archivedBy, board, memory, participants)
	email := meetingEmailStatus{
		Recipients: participantEmails(participants),
		Skipped:    true,
		Reason:     "Email delivery has not run yet.",
	}
	archive := meetingArchive{
		ID:           archiveID,
		MeetingID:    meetingID,
		ArchivedAt:   archivedAt,
		ArchivedBy:   archivedBy,
		Board:        board,
		Memory:       memory,
		Participants: participants,
		Notes:        notes,
		Email:        email,
		Meeting:      closedMeeting,
	}

	archivePath, err := meetingArchivePath(archiveID)
	if err != nil {
		return meetingArchiveResult{}, err
	}

	if err := writeMeetingArchive(archivePath, archive); err != nil {
		return meetingArchiveResult{}, fmt.Errorf("write meeting archive: %w", err)
	}

	// The archive is durable: NOW close the record (idempotent — a retried
	// archive of an already-ended id reports changed=false and never
	// restamps).
	closedMeetingChanged := false
	if record, changed := app.meetings.endMeeting(meetingID, archivedAt, meetingEndedReasonArchive, archiveID); record.ID != "" {
		closedRecord := record
		closedMeeting = &closedRecord
		closedMeetingChanged = changed
	}

	email = sendMeetingNotesEmail(email.Recipients, notes)
	archive.Email = email
	if err := writeMeetingArchive(archivePath, archive); err != nil {
		return meetingArchiveResult{}, fmt.Errorf("write meeting archive email status: %w", err)
	}

	summary := fmt.Sprintf("Archived meeting %s with %d transcript item(s), %d board card(s), %d participant(s), and %d project status item(s).", archiveID, len(archive.Memory), len(archive.Board.Cards), len(archive.Participants), len(notes.ProjectStatuses))
	if archivedBy != "" {
		summary = fmt.Sprintf("%s archived meeting %s with %d transcript item(s), %d board card(s), %d participant(s), and %d project status item(s).", archivedBy, archiveID, len(archive.Memory), len(archive.Board.Cards), len(archive.Participants), len(notes.ProjectStatuses))
	}
	if email.Sent {
		summary += fmt.Sprintf(" Meeting notes were emailed to %d recipient(s).", len(email.Recipients))
	} else if email.Skipped {
		summary += " Meeting notes were generated but not emailed: " + email.Reason
	} else if email.Error != "" {
		summary += " Meeting notes were generated, but email failed: " + email.Error
	}
	var artifact *meetingMemoryEntry
	if app.memory != nil {
		// the persisted downloadUrl stays unkeyed so the room password never
		// lands in the memory store or in archive snapshots.
		_, _, err = app.memory.appendArchive(archiveID, summary, map[string]string{
			"archiveId":   archiveID,
			"downloadUrl": meetingArchiveDownloadURL(archiveID),
			"archivedBy":  archivedBy,
			"meetingId":   meetingID,
		})
		if err != nil {
			return meetingArchiveResult{}, fmt.Errorf("remember meeting archive: %w", err)
		}
		artifactEntry, _, err := app.memory.appendOSArtifact(archiveID+"-artifact", buildMeetingArchiveArtifactText(archive, summary), map[string]string{
			"mode":        "meeting",
			"title":       meetingArchiveArtifactTitle(archive),
			"archiveId":   archiveID,
			"downloadUrl": meetingArchiveDownloadURL(archiveID),
			"createdBy":   archivedBy,
			"meetingId":   meetingID,
			"status":      "published",
			"published":   "true",
			"publishedAt": archivedAt.Format(time.RFC3339Nano),
			"publishedBy": archivedBy,
		})
		if err != nil {
			return meetingArchiveResult{}, fmt.Errorf("remember meeting artifact: %w", err)
		}
		clientArtifact := decorateArchiveDownloadURLForClient(artifactEntry)
		artifact = &clientArtifact
		// the meeting is over: deliver anything queued with deliver
		// "after_meeting" before the id rotates (idempotent — the idle-end
		// seam may already have flushed).
		app.flushDeferredNotifications("archive")
		// the archive closes the current meeting; the next entry starts a new
		// one. Conditional: a racing admission that already rotated and minted
		// a successor id must not have it cleared by this stale close.
		if meetingID != "" {
			app.memory.rotateMeetingIDIfCurrent(officeRoomID, meetingID)
		} else {
			// nothing was captured before the archive; clear whatever id the
			// archive entries themselves lazily minted (pre-fix behavior).
			app.memory.rotateMeetingID(officeRoomID)
		}
	}

	// push the closed record, then immediately open a successor record when
	// people are still in the room so a mid-occupancy archive never leaves a
	// recordless gap.
	if closedMeetingChanged && closedMeeting != nil {
		app.broadcastMeetingRecord(*closedMeeting)
	}
	if app.meetings != nil && app.memory != nil && app.activeParticipantCount(officeRoomID) > 0 {
		successorID := app.memory.ensureMeetingID(officeRoomID)
		if successor, started := app.meetings.startMeeting(officeRoomID, successorID, time.Now().UTC(), app.participantSnapshot()); started {
			app.broadcastMeetingRecord(successor)
		}
	}

	return meetingArchiveResult{
		ID:          archiveID,
		MeetingID:   meetingID,
		ArchivedAt:  archivedAt.Format(time.RFC3339Nano),
		ArchivedBy:  archivedBy,
		DownloadURL: meetingArchiveDownloadURLWithKey(archiveID),
		Summary:     summary,
		Notes:       notes,
		Email:       email,
		Artifact:    artifact,
	}, nil
}

// autoArchiveIdleMeeting writes the durable archive for a meeting the idle
// timer just closed — the session-end rule (card 078): empty for the grace
// window means the session is over, and a non-empty session is preserved
// silently. It runs AFTER endMeetingForIdle stamped EndedAt and rotated the
// memory meeting id, so it never re-ends the record or re-rotates; the
// archive entries pin the ENDED meeting id explicitly so the append can never
// lazily mint (and stamp) a successor id. Differences from archiveMeeting,
// all deliberate: no email (silent), no successor record (the room is empty),
// no deferred-notification flush (endMeetingForIdle already flushed with
// "meeting_end"), and no ambient-agent flush here — post-rotation model output
// would key to the successor id; endMeetingForIdle already ran the close
// chain (flushAmbientAgentsForClose) BEFORE rotating, so the snapshot below
// embeds the final brain pass and the meeting's digest.
func (app *kanbanBoardApp) autoArchiveIdleMeeting(closed meetingRecord) {
	if app == nil || app.memory == nil || app.meetings == nil || strings.TrimSpace(closed.ID) == "" {
		return
	}
	memory := app.memorySnapshotForMeeting(closed.ID, 2000)
	if len(memory) == 0 {
		// a contentless session leaves no artifact.
		return
	}
	archivedAt := time.Now().UTC()
	archiveID := durableTimestampID("meeting", archivedAt)
	board := app.snapshotState()
	participants := append([]string(nil), closed.Participants...)
	notes := buildMeetingNotes(archiveID, archivedAt, "", board, memory, participants)
	email := meetingEmailStatus{
		Recipients: participantEmails(participants),
		Skipped:    true,
		Reason:     "Idle auto-archive does not email notes.",
	}
	embedded := cloneMeetingRecord(closed)
	embedded.ArchiveID = archiveID
	archive := meetingArchive{
		ID:           archiveID,
		MeetingID:    closed.ID,
		ArchivedAt:   archivedAt,
		Board:        board,
		Memory:       memory,
		Participants: participants,
		Notes:        notes,
		Email:        email,
		Meeting:      &embedded,
	}

	archivePath, err := meetingArchivePath(archiveID)
	if err != nil {
		log.Errorf("Failed to resolve idle auto-archive path: %v", err)
		return
	}
	if err := writeMeetingArchive(archivePath, archive); err != nil {
		log.Errorf("Failed to write idle auto-archive: %v", err)
		return
	}

	// the archive is durable: stamp it onto the already-closed record
	// (refused when a racing fire already stamped one).
	if record, changed := app.meetings.stampArchiveID(closed.ID, archiveID); changed {
		app.broadcastMeetingRecord(record)
	}

	// same summary shape as archiveMeeting so the Memory tool's quiet-log
	// matcher keeps recognizing archive rows.
	summary := fmt.Sprintf("Archived meeting %s with %d transcript item(s), %d board card(s), %d participant(s), and %d project status item(s).", archiveID, len(archive.Memory), len(archive.Board.Cards), len(archive.Participants), len(notes.ProjectStatuses))
	summary += " Meeting notes were generated but not emailed: " + email.Reason

	// the archive entries file under the CLOSING record's room (multi-room W2:
	// a named room's auto-archive must never stamp roomId=office, or that
	// room's boot resume would read the office's tail instead of its own).
	closedRoomID := meetingRoomID(closed)
	if _, _, err := app.memory.appendEntryForMeeting(closedRoomID, meetingMemoryKindArchive, archiveID, summary, map[string]string{
		"archiveId":   archiveID,
		"downloadUrl": meetingArchiveDownloadURL(archiveID),
		"archivedBy":  "",
		"meetingId":   closed.ID,
	}, ""); err != nil {
		log.Errorf("Failed to remember idle auto-archive: %v", err)
		return
	}
	if _, _, err := app.memory.appendEntryForMeeting(closedRoomID, meetingMemoryKindOSArtifact, archiveID+"-artifact", buildMeetingArchiveArtifactText(archive, summary), map[string]string{
		"mode":        "meeting",
		"title":       meetingArchiveArtifactTitle(archive),
		"archiveId":   archiveID,
		"downloadUrl": meetingArchiveDownloadURL(archiveID),
		"createdBy":   "",
		"meetingId":   closed.ID,
		"status":      "published",
		"published":   "true",
		"publishedAt": archivedAt.Format(time.RFC3339Nano),
		"publishedBy": "",
	}, ""); err != nil {
		log.Errorf("Failed to remember idle auto-archive artifact: %v", err)
		return
	}
	// silent by design: refresh memory-fed surfaces, no meeting_archived
	// toast and no assistant announcement.
	broadcastSignedInKanbanEvent("memory", nil)
}

func meetingArchiveArtifactTitle(archive meetingArchive) string {
	title := ""
	// the meeting record's server-derived title is the meeting's real name;
	// the generated notes subject is the fallback.
	if archive.Meeting != nil {
		title = strings.TrimSpace(archive.Meeting.Title)
	}
	if title == "" {
		title = strings.TrimSpace(archive.Notes.Subject)
	}
	if title == "" {
		title = "Meeting artifact"
	}
	if !archive.ArchivedAt.IsZero() {
		title = title + " - " + archive.ArchivedAt.Format("Jan 2")
	}
	return title
}

func buildMeetingArchiveArtifactText(archive meetingArchive, summary string) string {
	var body strings.Builder
	body.WriteString("Meeting artifact\n\n")
	if strings.TrimSpace(summary) != "" {
		body.WriteString("Summary\n")
		body.WriteString(summary)
		body.WriteString("\n\n")
	}
	if archive.ID != "" {
		body.WriteString("Archive ID: ")
		body.WriteString(archive.ID)
		body.WriteString("\n")
	}
	if !archive.ArchivedAt.IsZero() {
		body.WriteString("Archived: ")
		body.WriteString(archive.ArchivedAt.Format(time.RFC1123))
		body.WriteString("\n")
	}
	if archive.ArchivedBy != "" {
		body.WriteString("Archived by: ")
		body.WriteString(archive.ArchivedBy)
		body.WriteString("\n")
	}
	if len(archive.Participants) > 0 {
		body.WriteString("Participants: ")
		body.WriteString(strings.Join(archive.Participants, ", "))
		body.WriteString("\n")
	}

	body.WriteString("\nDecisions\n")
	if len(archive.Notes.Decisions) == 0 {
		body.WriteString("- No explicit decisions were captured in the transcript.\n")
	} else {
		for _, decision := range archive.Notes.Decisions {
			body.WriteString("- ")
			body.WriteString(decision)
			body.WriteByte('\n')
		}
	}

	body.WriteString("\nProject status\n")
	if len(archive.Notes.ProjectStatuses) == 0 {
		body.WriteString("- No active project cards were on the board.\n")
	} else {
		for _, project := range archive.Notes.ProjectStatuses {
			owner := strings.TrimSpace(project.Owner)
			if owner == "" {
				owner = "Unassigned"
			}
			body.WriteString("- ")
			body.WriteString(project.Title)
			body.WriteString(": ")
			body.WriteString(project.Status)
			body.WriteString(" · ")
			body.WriteString(owner)
			body.WriteByte('\n')
		}
	}

	if strings.TrimSpace(archive.Notes.Text) != "" {
		body.WriteString("\nFull notes\n")
		body.WriteString(archive.Notes.Text)
	}

	return strings.TrimSpace(body.String())
}

func writeMeetingArchive(path string, archive meetingArchive) error {
	return writeJSONFileAtomically(path, "meeting archive", archive)
}

func meetingArchiveDownloadURL(archiveID string) string {
	return "/archives/" + archiveID + ".json"
}

// meetingArchiveDownloadURLWithKey appends the archive's derived access token
// so the client can link the archive without the URL ever carrying the room
// password.
func meetingArchiveDownloadURLWithKey(archiveID string) string {
	return meetingArchiveDownloadURL(archiveID) + "?key=" + url.QueryEscape(archiveAccessToken(archiveID))
}

func meetingArchivePath(archiveID string) (string, error) {
	archiveID = strings.TrimSpace(strings.TrimSuffix(archiveID, ".json"))
	if archiveID == "" || strings.Contains(archiveID, "/") || strings.Contains(archiveID, "\\") || strings.Contains(archiveID, "..") {
		return "", fmt.Errorf("invalid archive id")
	}

	return filepath.Join(filepath.Dir(meetingMemoryPath()), "archives", archiveID+".json"), nil
}

func (app *kanbanBoardApp) createCardIDLocked() string {
	for {
		cardID := fmt.Sprintf("kanban-card-%03d", app.nextCreatedIndex)
		app.nextCreatedIndex++
		if _, exists := app.findCardLocked(cardID); exists {
			continue
		}
		return cardID
	}
}

// resolveCardIDArg returns the target card id for a card mutation. When the
// model omits card_id but names the card (title or card_title), the card is
// resolved by exact case-insensitive title match — the board worker omits
// card_id dozens of times per pass and every such op used to hard-fail. An
// absent title or an ambiguous one (multiple matching cards) keeps the
// original card_id-is-required error.
func (app *kanbanBoardApp) resolveCardIDArg(args map[string]any) (string, error) {
	if cardID := strings.TrimSpace(asString(args["card_id"])); cardID != "" {
		return cardID, nil
	}
	title := canonicalizeBoardText(asString(args["title"]))
	if title == "" {
		title = canonicalizeBoardText(asString(args["card_title"]))
	}
	if title == "" {
		return "", fmt.Errorf("card_id is required")
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	matchID := ""
	matches := 0
	for index := range app.cards {
		if strings.EqualFold(app.cards[index].Title, title) {
			matchID = app.cards[index].ID
			matches++
		}
	}
	if matches != 1 {
		return "", fmt.Errorf("card_id is required")
	}

	return matchID, nil
}

func (app *kanbanBoardApp) findCardLocked(cardID string) (*kanbanCard, bool) {
	for index := range app.cards {
		if app.cards[index].ID == cardID {
			return &app.cards[index], true
		}
	}

	return nil, false
}

func (app *kanbanBoardApp) touchLocked() error {
	app.updatedAt = time.Now().UTC()
	return app.persistBoardLocked()
}

func (app *kanbanBoardApp) persistBoard() error {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.persistBoardLocked()
}

func (app *kanbanBoardApp) persistBoardLocked() error {
	state := kanbanBoardState{
		Cards: cloneKanbanCards(app.cards),
	}
	if !app.updatedAt.IsZero() {
		state.UpdatedAt = app.updatedAt.UTC().Format(time.RFC3339Nano)
	}

	err := writeKanbanBoardState(kanbanBoardPath(), state)
	if err == nil {
		return nil
	}
	// A failed/ambiguous durable replace must not leave RAM describing a
	// generation different from the visible legacy source of truth.
	if visible, found, loadErr := loadKanbanBoardState(kanbanBoardPath()); loadErr == nil && found {
		app.cards = cloneKanbanCards(visible.Cards)
		if parsed, parseErr := time.Parse(time.RFC3339Nano, visible.UpdatedAt); parseErr == nil {
			app.updatedAt = parsed.UTC()
		}
	}
	return err
}

func cloneKanbanCards(cards []kanbanCard) []kanbanCard {
	clonedCards := make([]kanbanCard, 0, len(cards))
	for _, card := range cards {
		clonedCards = append(clonedCards, cloneKanbanCard(card))
	}

	return clonedCards
}

func cloneKanbanCard(card kanbanCard) kanbanCard {
	return kanbanCard{
		ID:        card.ID,
		Status:    card.Status,
		Title:     card.Title,
		Notes:     card.Notes,
		Owner:     card.Owner,
		Tags:      append([]string(nil), card.Tags...),
		DueDate:   card.DueDate,
		KeyDates:  cloneKanbanKeyDates(card.KeyDates),
		Draft:     card.Draft,
		DraftedAt: card.DraftedAt,
	}
}

func normalizeCardOwner(value any) string {
	owner := asString(value)
	if strings.EqualFold(owner, "Unassigned") {
		return "Unassigned"
	}
	if canonicalOwner := canonicalParticipantName(owner); canonicalOwner != "" {
		return canonicalOwner
	}

	return ""
}

func canonicalRoomActorName(name string) string {
	if participant := canonicalParticipantName(name); participant != "" {
		return participant
	}
	if strings.EqualFold(strings.TrimSpace(name), scoutParticipantName) {
		return scoutParticipantName
	}

	return ""
}

func asString(value any) string {
	candidate, ok := value.(string)
	if !ok {
		return ""
	}

	return strings.TrimSpace(candidate)
}

func asBool(value any) bool {
	candidate, ok := value.(bool)
	return ok && candidate
}

// asInt reads an integer tool argument. JSON numbers decode to float64, but
// tolerate a numeric string too so a model that quotes the value still works.
func asInt(value any) int {
	switch candidate := value.(type) {
	case float64:
		return int(candidate)
	case int:
		return candidate
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(candidate)); err == nil {
			return parsed
		}
	}
	return 0
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}

	return ""
}

func asStringSlice(value any) []string {
	rawValues, ok := value.([]any)
	if !ok {
		return nil
	}

	values := make([]string, 0, len(rawValues))
	for _, rawValue := range rawValues {
		if value := asString(rawValue); value != "" {
			values = append(values, value)
		}
	}

	return values
}

func dueDateFromArgs(args map[string]any) (string, bool) {
	if value, ok := args["due_date"]; ok {
		return normalizeKeyDateText(asString(value)), true
	}
	if value, ok := args["dueDate"]; ok {
		return normalizeKeyDateText(asString(value)), true
	}

	return "", false
}

func keyDatesFromArgs(args map[string]any) ([]kanbanKeyDate, bool) {
	if value, ok := args["key_dates"]; ok {
		return asKanbanKeyDates(value), true
	}
	if value, ok := args["keyDates"]; ok {
		return asKanbanKeyDates(value), true
	}

	return nil, false
}

func asKanbanKeyDates(value any) []kanbanKeyDate {
	switch rawValues := value.(type) {
	case []any:
		dates := make([]kanbanKeyDate, 0, len(rawValues))
		for _, rawValue := range rawValues {
			if keyDate, ok := keyDateFromAny(rawValue); ok {
				dates = append(dates, keyDate)
			}
		}
		return normalizeKanbanKeyDates(dates)
	case []kanbanKeyDate:
		return normalizeKanbanKeyDates(rawValues)
	case []map[string]any:
		dates := make([]kanbanKeyDate, 0, len(rawValues))
		for _, rawValue := range rawValues {
			if keyDate, ok := keyDateFromAny(rawValue); ok {
				dates = append(dates, keyDate)
			}
		}
		return normalizeKanbanKeyDates(dates)
	default:
		return nil
	}
}

func keyDateFromAny(value any) (kanbanKeyDate, bool) {
	switch rawValue := value.(type) {
	case kanbanKeyDate:
		return normalizeKanbanKeyDate(rawValue.Label, rawValue.Date)
	case map[string]any:
		label := firstNonEmptyString(asString(rawValue["label"]), asString(rawValue["name"]))
		date := firstNonEmptyString(asString(rawValue["date"]), asString(rawValue["value"]))
		return normalizeKanbanKeyDate(label, date)
	case map[string]string:
		label := firstNonEmptyString(rawValue["label"], rawValue["name"])
		date := firstNonEmptyString(rawValue["date"], rawValue["value"])
		return normalizeKanbanKeyDate(label, date)
	default:
		return kanbanKeyDate{}, false
	}
}

func normalizeKanbanKeyDates(dates []kanbanKeyDate) []kanbanKeyDate {
	return mergeKanbanKeyDates(nil, dates...)
}

func normalizeKanbanKeyDate(label string, date string) (kanbanKeyDate, bool) {
	label = normalizeKeyDateText(label)
	date = normalizeKeyDateText(date)
	if label == "" || date == "" {
		return kanbanKeyDate{}, false
	}
	if strings.EqualFold(label, "due") || strings.EqualFold(label, "deadline") {
		label = "due"
	}

	return kanbanKeyDate{Label: label, Date: date}, true
}

func normalizeKeyDateLabels(labels []string) []string {
	normalizedLabels := make([]string, 0, len(labels))
	seenLabels := map[string]struct{}{}
	for _, label := range labels {
		normalizedLabel := normalizeKeyDateLabel(label)
		if normalizedLabel == "" {
			continue
		}
		if _, exists := seenLabels[normalizedLabel]; exists {
			continue
		}
		seenLabels[normalizedLabel] = struct{}{}
		normalizedLabels = append(normalizedLabels, normalizedLabel)
	}

	return normalizedLabels
}

func normalizeKeyDateLabel(label string) string {
	normalizedLabel := strings.ToLower(normalizeKeyDateText(label))
	if normalizedLabel == "deadline" {
		return "due"
	}

	return normalizedLabel
}

func normalizeKeyDateText(value string) string {
	return strings.Trim(canonicalizeBoardText(value), "\"'")
}

func filterKanbanKeyDates(existing []kanbanKeyDate, labels []string, removeAll bool) []kanbanKeyDate {
	if removeAll {
		return nil
	}

	removeLabels := map[string]struct{}{}
	for _, label := range labels {
		if normalizedLabel := normalizeKeyDateLabel(label); normalizedLabel != "" {
			removeLabels[normalizedLabel] = struct{}{}
		}
	}
	if len(removeLabels) == 0 {
		return cloneKanbanKeyDates(existing)
	}

	filtered := make([]kanbanKeyDate, 0, len(existing))
	for _, keyDate := range existing {
		if _, remove := removeLabels[normalizeKeyDateLabel(keyDate.Label)]; remove {
			continue
		}
		filtered = append(filtered, keyDate)
	}

	return normalizeKanbanKeyDates(filtered)
}

func mergeKanbanKeyDates(existing []kanbanKeyDate, additions ...kanbanKeyDate) []kanbanKeyDate {
	merged := make([]kanbanKeyDate, 0, len(existing)+len(additions))
	indexByLabel := map[string]int{}
	for _, keyDate := range append(cloneKanbanKeyDates(existing), additions...) {
		normalizedKeyDate, ok := normalizeKanbanKeyDate(keyDate.Label, keyDate.Date)
		if !ok {
			continue
		}
		labelKey := strings.ToLower(normalizedKeyDate.Label)
		if existingIndex, exists := indexByLabel[labelKey]; exists {
			merged[existingIndex] = normalizedKeyDate
			continue
		}
		if len(merged) >= maxKanbanKeyDates {
			continue
		}
		indexByLabel[labelKey] = len(merged)
		merged = append(merged, normalizedKeyDate)
	}

	return merged
}

func cloneKanbanKeyDates(dates []kanbanKeyDate) []kanbanKeyDate {
	if len(dates) == 0 {
		return nil
	}
	clonedDates := make([]kanbanKeyDate, 0, len(dates))
	for _, date := range dates {
		clonedDates = append(clonedDates, date)
	}

	return clonedDates
}

func kanbanKeyDatesEqual(left []kanbanKeyDate, right []kanbanKeyDate) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}

	return true
}

func dueDateFromKeyDates(dates []kanbanKeyDate) string {
	for _, date := range dates {
		if keyDateIsDue(date) {
			return date.Date
		}
	}

	return ""
}

func keyDateIsDue(date kanbanKeyDate) bool {
	return strings.EqualFold(strings.TrimSpace(date.Label), "due")
}

func keyDateLabelsIncludeDue(labels []string) bool {
	for _, label := range labels {
		if normalizeKeyDateLabel(label) == "due" {
			return true
		}
	}

	return false
}

func formatKanbanKeyDates(dates []kanbanKeyDate) string {
	parts := make([]string, 0, len(dates))
	for _, date := range normalizeKanbanKeyDates(dates) {
		parts = append(parts, fmt.Sprintf("%s %s", date.Label, date.Date))
	}

	return strings.Join(parts, ", ")
}

// kanbanStatusAliases maps normalized (lowercased, whitespace-collapsed)
// status spellings the models actually emit to the four canonical board
// columns. The board worker and realtime Scout both send statuses like
// "To Do", "Todo", or "Draft"; treating spelling as fatal silently dropped
// whole cards, so every known spelling lands on a real column instead.
var kanbanStatusAliases = map[string]kanbanStatus{
	"backlog":     kanbanStatusBacklog,
	"todo":        kanbanStatusBacklog,
	"to do":       kanbanStatusBacklog,
	"to-do":       kanbanStatusBacklog,
	"draft":       kanbanStatusBacklog,
	"new":         kanbanStatusBacklog,
	"in progress": kanbanStatusInProgress,
	"in-progress": kanbanStatusInProgress,
	"doing":       kanbanStatusInProgress,
	"wip":         kanbanStatusInProgress,
	"blocked":     kanbanStatusBlocked,
	"blocker":     kanbanStatusBlocked,
	"done":        kanbanStatusDone,
	"complete":    kanbanStatusDone,
	"completed":   kanbanStatusDone,
	"finished":    kanbanStatusDone,
	"shipped":     kanbanStatusDone,
}

func parseKanbanStatus(value any) (kanbanStatus, error) {
	normalized := strings.ToLower(strings.Join(strings.Fields(asString(value)), " "))
	if canonical, ok := kanbanStatusAliases[normalized]; ok {
		return canonical, nil
	}

	return "", fmt.Errorf("unknown Kanban status: %v", value)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		normalizedValue := strings.TrimSpace(value)
		if normalizedValue == "" {
			continue
		}
		if _, ok := seen[normalizedValue]; ok {
			continue
		}
		seen[normalizedValue] = struct{}{}
		result = append(result, normalizedValue)
	}

	return result
}

func stringSlicesEqual(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}

	return true
}

func mustMarshalJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return `{"ok":false,"error":"Could not encode function output."}`
	}

	return string(raw)
}

func humanizeToolName(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "_", " "))
	if name == "" {
		return "tool"
	}

	return name
}

func (app *kanbanBoardApp) assistantStatusSnapshot() map[string]any {
	if app == nil {
		return nil
	}

	app.mu.Lock()
	status := app.assistantStatus
	app.mu.Unlock()
	if strings.TrimSpace(status) == "" {
		return nil
	}

	return map[string]any{
		"kind":      "status",
		"text":      status,
		"createdAt": time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func broadcastAssistantEvent(kind string, text string, metadata map[string]any) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	if kanbanApp != nil && (kind == "status" || kind == "error") {
		kanbanApp.mu.Lock()
		kanbanApp.assistantStatus = text
		kanbanApp.mu.Unlock()
	}

	payload := map[string]any{
		"kind":      kind,
		"text":      text,
		"createdAt": time.Now().UTC().Format(time.RFC3339Nano),
	}
	for key, value := range metadata {
		payload[key] = value
	}

	broadcastKanbanEvent("assistant_event", payload)
}

func sendKanbanEvent(websocket *threadSafeWriter, event string, data any) error {
	// §6.2 write-time backstop: an event outside the guest allowlist is
	// dropped at the writer, whatever path routed it here.
	if !guestWriterAllowsKanbanEvent(websocket, event) {
		return nil
	}
	raw, err := json.Marshal(map[string]any{
		"event": event,
		"data":  data,
	})
	if err != nil {
		return err
	}

	return websocket.WriteJSON(&websocketMessage{
		Event: "kanban",
		Data:  string(raw),
	})
}

func encodeKanbanEvent(event string, data any) (string, error) {
	raw, err := json.Marshal(map[string]any{
		"event": event,
		"data":  data,
	})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func deliverKanbanEvent(event string, websockets []*threadSafeWriter, raw string) {
	for _, websocket := range websockets {
		// §6.2: broadcasts share this delivery path; guest writers only ever
		// receive allowlisted events, whatever pool routed them here.
		if !guestWriterAllowsKanbanEvent(websocket, event) {
			continue
		}
		if err := websocket.WriteJSON(&websocketMessage{
			Event: "kanban",
			Data:  raw,
		}); err != nil {
			if isExpectedKanbanBroadcastClose(err) {
				continue
			}
			log.Errorf("Failed to send Kanban event: %v", err)
		}
	}
}

// broadcastKanbanEvent is the OFFICE room fan-out — the legacy name every
// office-ambient caller (Scout status, assistant events) keeps. Room-scoped
// paths carry their room through broadcastRoomKanbanEvent.
func broadcastKanbanEvent(event string, data any) {
	broadcastRoomKanbanEvent(officeRoomID, event, data)
}

// broadcastRoomKanbanEvent reaches the media-joined sockets of ONE room —
// members and (allowlist permitting) guests seated there. Room-scoped events
// (signaling companions, participants, transcripts, active speaker) must stay
// on this path so a second live room can never overwrite another tab's state.
func broadcastRoomKanbanEvent(roomID string, event string, data any) {
	roomID = normalizeRoomID(roomID)
	raw, err := encodeKanbanEvent(event, data)
	if err != nil {
		log.Errorf("Failed to encode Kanban event: %v", err)
		return
	}

	listLock.RLock()
	websockets := make([]*threadSafeWriter, 0, len(peerConnections))
	for _, state := range peerConnections {
		if state.websocket != nil && normalizeRoomID(state.roomID) == roomID {
			websockets = append(websockets, state.websocket)
		}
	}
	listLock.RUnlock()

	deliverKanbanEvent(event, websockets, raw)
}

// broadcastRoomAudienceKanbanEvent preserves the legacy office room as an
// organization-wide member surface while making every named room private to
// its admitted sockets. Named-room chat/meeting records must never use the
// signed-in union fan-out, which crosses room boundaries by design.
func broadcastRoomAudienceKanbanEvent(roomID string, event string, data any) {
	roomID = normalizeRoomID(roomID)
	if roomID == officeRoomID {
		broadcastSignedInKanbanEvent(event, data)
		broadcastRoomGuestsKanbanEvent(roomID, event, data)
		return
	}
	broadcastRoomKanbanEvent(roomID, event, data)
}

// broadcastRoomGuestsKanbanEvent reaches only the GUEST sockets of one room —
// the sidecar for signed-in broadcasts whose payload guests are entitled to
// (their room's meeting record, room chat), since guests never appear in the
// office/signed-in pools.
func broadcastRoomGuestsKanbanEvent(roomID string, event string, data any) {
	roomID = normalizeRoomID(roomID)
	raw, err := encodeKanbanEvent(event, data)
	if err != nil {
		log.Errorf("Failed to encode guest Kanban event: %v", err)
		return
	}

	listLock.RLock()
	websockets := make([]*threadSafeWriter, 0, 4)
	for _, state := range peerConnections {
		if state.websocket != nil && state.websocket.guest && normalizeRoomID(state.roomID) == roomID {
			websockets = append(websockets, state.websocket)
		}
	}
	listLock.RUnlock()

	deliverKanbanEvent(event, websockets, raw)
}

// broadcastOfficeKanbanEvent fans an event out to office sockets only —
// authenticated connections that never took a room seat. Clients keep the
// office socket open while in the room too, so office-only routing is the
// exactly-once channel for signed-in-safe events (chat_thread,
// codex_proposal, mission_insight).
func broadcastOfficeKanbanEvent(event string, data any) {
	if event == "memory" && kanbanApp != nil {
		listLock.RLock()
		type recipient struct {
			websocket *threadSafeWriter
			email     string
		}
		recipients := make([]recipient, 0, len(officeConnections))
		for _, state := range officeConnections {
			if state.websocket != nil {
				recipients = append(recipients, recipient{state.websocket, state.sessionEmail})
			}
		}
		listLock.RUnlock()
		for _, recipient := range recipients {
			principal, ok := authenticatedRecallPrincipal(recipient.email)
			if !ok {
				continue
			}
			_ = sendKanbanEvent(recipient.websocket, "memory", kanbanApp.memorySnapshotForPrincipal(context.Background(), principal, 20))
		}
		return
	}
	raw, err := encodeKanbanEvent(event, data)
	if err != nil {
		log.Errorf("Failed to encode office Kanban event: %v", err)
		return
	}

	listLock.RLock()
	websockets := make([]*threadSafeWriter, 0, len(officeConnections))
	for _, state := range officeConnections {
		if state.websocket != nil {
			websockets = append(websockets, state.websocket)
		}
	}
	listLock.RUnlock()

	deliverKanbanEvent(event, websockets, raw)
}

// broadcastSignedInKanbanEvent reaches the union of office sockets and
// media-joined MEMBER room sockets, deduped by writer pointer. Guests are
// excluded at selection (they are never signed in) on top of the §6.2
// write-time allowlist. Reserved for idempotent, snapshot-shaped payloads
// (board, undo_available, memory, meeting, meeting_archived, server_shutdown)
// and id-deduped entries (notification, room_chat) where a double delivery is
// a harmless re-render.
func broadcastSignedInKanbanEvent(event string, data any) {
	if event == "memory" && kanbanApp != nil {
		type recipient struct {
			websocket *threadSafeWriter
			email     string
			roomID    string
		}
		listLock.RLock()
		seen := make(map[*threadSafeWriter]bool, len(officeConnections)+len(peerConnections))
		recipients := make([]recipient, 0, len(officeConnections)+len(peerConnections))
		for _, state := range officeConnections {
			if state.websocket != nil && !seen[state.websocket] {
				seen[state.websocket] = true
				recipients = append(recipients, recipient{state.websocket, state.sessionEmail, officeRoomID})
			}
		}
		for _, state := range peerConnections {
			if state.websocket != nil && !state.websocket.guest && !seen[state.websocket] {
				seen[state.websocket] = true
				recipients = append(recipients, recipient{state.websocket, state.sessionEmail, state.roomID})
			}
		}
		listLock.RUnlock()
		for _, recipient := range recipients {
			if _, ok := authenticatedRecallPrincipal(recipient.email); !ok {
				continue
			}
			principal := kanbanApp.recallPrincipalForMemberRoom(recipient.email, recipient.roomID)
			_ = sendKanbanEvent(recipient.websocket, "memory", kanbanApp.memorySnapshotForPrincipal(context.Background(), principal, 20))
		}
		return
	}
	raw, err := encodeKanbanEvent(event, data)
	if err != nil {
		log.Errorf("Failed to encode signed-in Kanban event: %v", err)
		return
	}

	listLock.RLock()
	seen := make(map[*threadSafeWriter]bool, len(officeConnections)+len(peerConnections))
	websockets := make([]*threadSafeWriter, 0, len(officeConnections)+len(peerConnections))
	for _, state := range officeConnections {
		if state.websocket != nil && !seen[state.websocket] {
			seen[state.websocket] = true
			websockets = append(websockets, state.websocket)
		}
	}
	for _, state := range peerConnections {
		if state.websocket != nil && !state.websocket.guest && !seen[state.websocket] {
			seen[state.websocket] = true
			websockets = append(websockets, state.websocket)
		}
	}
	listLock.RUnlock()

	deliverKanbanEvent(event, websockets, raw)
}

// sendKanbanEventToUser delivers an event only to live connections whose
// server-side authenticated session email matches. It iterates
// officeConnections plus activeParticipantConnections (populated at
// admission, unlike the media-gated peerConnections fan-out pool), deduped by
// writer pointer, so office tabs and admitted-but-not-media-joined sockets
// are reached too. Targeted payloads must never go through
// broadcastKanbanEvent and rely on client-side redaction.
func sendKanbanEventToUser(email string, event string, data any) {
	email = normalizeAccountEmail(email)
	if email == "" {
		return
	}

	raw, err := encodeKanbanEvent(event, data)
	if err != nil {
		log.Errorf("Failed to encode targeted Kanban event: %v", err)
		return
	}

	listLock.RLock()
	seen := make(map[*threadSafeWriter]bool, 2)
	websockets := make([]*threadSafeWriter, 0, 2)
	for _, state := range officeConnections {
		if state.websocket != nil && state.sessionEmail == email && !seen[state.websocket] {
			seen[state.websocket] = true
			websockets = append(websockets, state.websocket)
		}
	}
	for _, state := range activeParticipantConnections {
		if state.websocket != nil && state.sessionEmail == email && !seen[state.websocket] {
			seen[state.websocket] = true
			websockets = append(websockets, state.websocket)
		}
	}
	listLock.RUnlock()

	for _, websocket := range websockets {
		if err := websocket.WriteJSON(&websocketMessage{
			Event: "kanban",
			Data:  raw,
		}); err != nil {
			if isExpectedKanbanBroadcastClose(err) {
				continue
			}
			log.Errorf("Failed to send targeted Kanban event: %v", err)
		}
	}
}

// userHasLiveKanbanSocket reports whether the account currently holds an
// office or admitted-participant websocket — i.e. is "present" at the desk.
// Powers the only-when-away web-push rule (card 089): a phone stays quiet for
// what an open session already surfaced. Scans the same two pools as
// sendKanbanEventToUser under the shared read lock.
func userHasLiveKanbanSocket(email string) bool {
	email = normalizeAccountEmail(email)
	if email == "" {
		return false
	}

	listLock.RLock()
	defer listLock.RUnlock()
	for _, state := range officeConnections {
		if state.websocket != nil && state.sessionEmail == email {
			return true
		}
	}
	for _, state := range activeParticipantConnections {
		if state.websocket != nil && state.sessionEmail == email {
			return true
		}
	}
	return false
}

func isExpectedKanbanBroadcastClose(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "websocket: close sent") ||
		strings.Contains(message, "use of closed network connection") ||
		strings.Contains(message, "broken pipe")
}
