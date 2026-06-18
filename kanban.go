package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const (
	realtimeCallsURL          = "https://api.openai.com/v1/realtime/calls"
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
	ArchivedAt   time.Time            `json:"archivedAt"`
	ArchivedBy   string               `json:"archivedBy,omitempty"`
	Board        kanbanBoardState     `json:"board"`
	Memory       []meetingMemoryEntry `json:"memory"`
	Participants []string             `json:"participants,omitempty"`
	Notes        meetingNotes         `json:"notes"`
	Email        meetingEmailStatus   `json:"email"`
}

type meetingArchiveResult struct {
	ID          string              `json:"id"`
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
	Item     *kanbanRealtimeOutputItem `json:"item,omitempty"`
	Response *struct {
		Status        string `json:"status,omitempty"`
		StatusDetails *struct {
			Type   string `json:"type,omitempty"`
			Reason string `json:"reason,omitempty"`
		} `json:"status_details,omitempty"`
		Output []kanbanRealtimeOutputItem `json:"output,omitempty"`
	} `json:"response,omitempty"`
}

type kanbanRealtimeOutputItem struct {
	Type      string `json:"type,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	CallID    string `json:"call_id,omitempty"`
}

type kanbanBoardApp struct {
	mu                           sync.Mutex
	cards                        []kanbanCard
	nextCreatedIndex             int
	updatedAt                    time.Time
	handledCalls                 map[string]struct{}
	memory                       *meetingMemoryStore
	participants                 map[string]time.Time
	participantCounts            map[string]int
	participantSessions          map[string]string
	participantMedia             map[string]participantMediaState
	transcriptRecordingEnabled   bool
	transcriptRecordingUpdatedAt time.Time
	transcriptRecordingUpdatedBy string
	lastDeletedCard              *kanbanCard
	apiKey                       string
	restarting                   bool
	assistantStatus              string

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
	audioActivity            []participantAudioFrame
	currentSpeechStartedAt   time.Time
	currentSpeechStoppedAt   time.Time
	proactiveReconnectCancel chan struct{}
	agentCancels             map[string]chan struct{}
	agentDones               map[string]chan struct{}
	agentBaselineIDs         map[string]string
	agentRunLocks            map[string]*sync.Mutex
	closeOnce                sync.Once
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
		Owner:  "Jake",
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
		cards:                        cards,
		nextCreatedIndex:             nextKanbanCardIndex(cards),
		updatedAt:                    updatedAt,
		handledCalls:                 map[string]struct{}{},
		memory:                       memory,
		participants:                 map[string]time.Time{},
		participantCounts:            map[string]int{},
		participantSessions:          map[string]string{},
		participantMedia:             map[string]participantMediaState{},
		transcriptRecordingEnabled:   true,
		transcriptRecordingUpdatedAt: updatedAt,
	}
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s directory: %w", description, err)
	}

	rawJSON, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", description, err)
	}
	rawJSON = append(rawJSON, '\n')

	tmpPath := fmt.Sprintf("%s.tmp-%d", path, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, rawJSON, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", description, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace %s: %w", description, err)
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
	app.startTranscriptionLane(apiKey)
	app.startMeetingBrainWorker(apiKey)
	app.startMeetingBoardWorker(apiKey)

	if err := app.startRealtimePeer(apiKey, realtimeModel()); err != nil {
		return err
	}

	return nil
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
		log.Infof("OpenAI Realtime peer state changed: %s", state.String())
		broadcastKanbanEvent("status", "OpenAI Realtime: "+state.String())
		broadcastAssistantEvent("status", "OpenAI Realtime: "+state.String(), nil)
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

func (app *kanbanBoardApp) restartRealtimePeer(reason string) {
	app.mu.Lock()
	if app.restarting {
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
	transcriptQueued := false
	if !isSyntheticSilence && recordingEnabled {
		transcriptQueued = app.enqueueTranscriptionLaneAudio(roomPCM)
	}

	if (isSyntheticSilence || !recordingEnabled) && !app.realtimeAudioInputAvailable() {
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

	trackLocal, err := addTrack(t, scoutParticipantName, "scout")
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
	contentType, body, err := buildRealtimeCallRequest(offerSDP, app.sessionConfig(model))
	if err != nil {
		return "", err
	}

	request, err := http.NewRequest(http.MethodPost, realtimeCallsURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create Realtime request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", contentType)

	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
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

	return string(answerSDP), nil
}

func buildRealtimeCallRequest(offerSDP string, session map[string]any) (string, []byte, error) {
	sessionJSON, err := json.Marshal(session)
	if err != nil {
		return "", nil, fmt.Errorf("marshal Realtime session: %w", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("sdp", offerSDP); err != nil {
		return "", nil, fmt.Errorf("write SDP offer: %w", err)
	}
	if err := writer.WriteField("session", string(sessionJSON)); err != nil {
		return "", nil, fmt.Errorf("write session config: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", nil, fmt.Errorf("finalize multipart request: %w", err)
	}

	return writer.FormDataContentType(), body.Bytes(), nil
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
	session := map[string]any{
		"type":              "realtime",
		"model":             model,
		"output_modalities": []string{"audio"},
		"audio": map[string]any{
			"input": map[string]any{
				"noise_reduction": map[string]any{
					"type": "near_field",
				},
				"transcription": map[string]any{
					"model":    realtimeTranscriptionModel(),
					"language": "en",
					"prompt":   realtimeTranscriptionPrompt(),
				},
				"turn_detection": realtimeTurnDetectionConfig(),
			},
			"output": map[string]any{
				"voice": realtimeVoice(),
			},
		},
		"instructions": app.sessionInstructions(),
		"tools":        app.kanbanTools(),
		"tool_choice":  app.realtimeToolChoice(),
	}

	if usesAdvancedCommandProfile(model) {
		session["reasoning"] = map[string]any{
			"effort": realtimeReasoningEffort(),
		}
	}

	return session
}

func (app *kanbanBoardApp) realtimeToolChoice() string {
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

func usesAdvancedCommandProfile(model string) bool {
	normalizedModel := strings.ToLower(strings.TrimSpace(model))
	return normalizedModel == "gpt-realtime-2"
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
	case "answer_memory_question":
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
	voiceControlState := "inactive: only clear utterances that start with Hey Scout are addressed to you."
	if app.voiceControlEnabled() {
		voiceControlState = "active: every clear user request is addressed to you until the user turns the floating voice island off."
	}
	return strings.Join([]string{
		"# Role and Objective\nYou are Scout, the Bonfire OS voice operator for live meetings, app navigation, durable artifacts, meeting memory, and the Kanban board. Keep the app useful with minimal chatter.",
		fmt.Sprintf("# Board\nCurrent Kanban board JSON: %s\nAvailable columns: Backlog, In Progress, Blocked, Done.\nKnown meeting participants: %s.", app.boardContextJSON(), strings.Join(meetingParticipantNames, ", ")),
		fmt.Sprintf("# Domain vocabulary\nUse these exact spellings for names, brands, acronyms, and technical terms: %s. Boot Barn is a known brand; do not write Suit Barn when the user says Boot Barn.", strings.Join(domainVocabulary(), ", ")),
		"# Language\nUsers may say ticket, card, task, issue, or sticky note; treat those as Kanban cards. If a transcript includes a speaker label such as Sean:, do not include the label in the title; use it only as context for owner, notes, or tags.",
		"# Reasoning\nFor direct board operations and simple recall requests, act quickly. For multi-step updates, ambiguous references, or memory questions, reason before choosing tools. Do not spend extra reasoning on unclear audio; ask for clarification through do_nothing.",
		"# Voice control mode\n" + voiceControlState + " When active, the waveform island is the user's vocal button for an instant two-way Realtime 2 conversation: answer simple capability, help, navigation, and status questions directly unless a listed tool is needed. When inactive, preserve the shared-room wake phrase behavior. In both modes, ignore background noise, side talk, silence, and filler with do_nothing.",
		"# Preambles\nDo not speak preambles for routine app or board updates. If an addressed request needs memory recall or another tool call that may take noticeable time, say one short acknowledgement immediately before the tool call. Only speak to the room after a tool result when the current voice-control mode says the clear user turn is addressed to you. Otherwise stay silent and use tools.",
		"# Field writing\nWrite card fields as direct project facts, not narration about the user request. Never start titles or notes with phrases like User said, User asked, User requested, or The user wants. Put due dates, key dates, milestone dates, and deadlines in due_date/key_dates or add_key_date; do not put a requested date only in notes. If the user says add Impossible Moments to the board because it is blocked waiting on Erick, use title Impossible Moments, status Blocked, owner Erick, and notes Waiting on Erick.",
		"# Unclear audio\nOnly operate on clear audio or clear typed text. Do not guess proper nouns, brand names, project names, acronyms, owners, or card titles. If the exact entity is unclear, call do_nothing with a concise clarification question instead of creating or updating a card.",
		"# Entity capture\nPreserve exact names, brands, owners, card titles, dates, and project terms. For high-precision identifiers or ambiguous names, normalize only what is clear. If multiple interpretations are plausible, call do_nothing with one clarification question.",
		"# Matching\nUse existing card ids exactly as provided. Match by meaning across title, notes, owner, and tags. Update an existing related card instead of creating a duplicate when the work is already represented. If you are not sure which existing card the user means, call do_nothing with a concise clarification question.",
		"# Status rules\nConcrete first-person status updates are implicit board operations. Started, began, picked up, or working on means In Progress. Shipped, fixed, completed, closed, finished, or resolved means Done. Blocked, waiting, dependent, needs another team, might slip, or at risk means Blocked and should preserve blocker details in notes with blocked, dependency, or risk tags. Park, punt, defer, or move back means Backlog.",
		"# Owner rules\nWhen the speaker names a responsible person, set owner to that exact participant name. Use Unassigned when responsibility is unclear.",
		"# App control\nUse control_app when the user asks you to open or show a Bonfire OS surface. Available surfaces are office, room, chat, artifacts, research, design, grill, board, and memory. If the user asks to open the chat app, start a chat, begin a thread, start a thread, or talk to Scout privately, call control_app with tool chat. The Chat app currently has one private Scout thread; opening Chat starts or focuses that thread. Do not say you cannot start a thread unless the user specifically asks to create multiple named/persistent threads beyond the current Scout thread. If the user asks for a saved artifact, select it by artifact_id when you know the id; otherwise open artifacts.",
		"# Room controls\nUse set_voice_control with enabled=false when the user asks you to stop listening, turn off voice, end the vocal conversation, close the waveform island, or stop Realtime. Use set_recording when the user asks to pause, resume, turn on, turn off, start, or stop transcript recording, meeting notes capture, or shared room recording. Use archive_meeting when the user asks to send notes, generate meeting notes, archive the meeting, or save the meeting artifact. Browser-local controls such as muting or unmuting the user's microphone, turning their camera on/off, sharing their screen, switching stage layout, pinning a speaker, copying a link, signing in/out, changing passwords, or adding passkeys require that user's browser and device permissions; open the relevant surface with control_app and explain the local action instead of claiming direct control.",
		"# Artifacts and prior meetings\nMeeting transcripts, brain summaries, archives, and OS artifacts are durable memory. If the user asks about prior meetings, artifacts, archives, decisions, transcripts, what was said, what was saved, or any recall question, call answer_memory_question with the user's full question as the query. If the user asks to make or save an output, call create_artifact with mode artifacts, research, design, grill, or workflow. If the user asks to update, rename, revise, or overwrite a saved artifact and you know its artifact_id, call update_artifact; if you do not know the artifact_id, open artifacts or ask which artifact rather than creating a duplicate. Use workflow when the user asks for a Codex goal, reusable goal workflow, multi-agent loop, research/design execution plan, or gated shipping loop. Workflow mode saves the goal workflow scaffold inside Bonfire OS; a Codex runner or external research job is not connected yet, so do not claim that you started a Codex goal, browser research, SSH work, or external job.",
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
		"description": "Bonfire OS surface to open. Use chat when the user asks to open the chat app, start a chat, start a thread, begin a thread, or talk to Scout privately.",
		"enum":        []string{"office", "room", "chat", "artifacts", "research", "design", "grill", "board", "memory"},
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
			"description": "Open or focus a Bonfire OS surface such as artifacts, memory, chat, research, design, grill, board, room, or office. For requests to open chat, start a chat, start a thread, begin a thread, or talk privately to Scout, open chat; the current Chat app has one private Scout thread. Use artifact_id when selecting a known saved artifact.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tool":        appToolProperty,
					"artifact_id": map[string]any{"type": "string", "description": "Optional saved artifact id to select after opening artifacts."},
				},
				"required":             []string{"tool"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "set_voice_control",
			"description": "Turn the floating Realtime 2 voice island on or off. Use enabled=false for requests like stop listening, turn off voice, end the vocal conversation, close the waveform island, or stop Realtime.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"enabled": map[string]any{"type": "boolean", "description": "false to stop Realtime voice listening and close the voice island; true to keep voice control active."},
				},
				"required":             []string{"enabled"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "set_recording",
			"description": "Pause or resume the shared room transcript recording and meeting notes capture. Use this for requests like pause recording, resume recording, turn notes capture on, or stop the transcript. This is not local mic, camera, or screen-share control.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"enabled": map[string]any{"type": "boolean", "description": "true to resume or turn on transcript recording; false to pause or turn it off."},
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
			"description": "Create a durable Bonfire OS artifact, research brief, design kickoff, grill scorecard, or Codex goal workflow from a clear user request. If content is omitted, the app will scaffold the artifact from board and memory context.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"mode":    artifactModeProperty,
					"query":   map[string]any{"type": "string", "description": "The user's artifact, research, design, grill, or workflow request."},
					"content": map[string]any{"type": "string", "description": "Optional final artifact content to save. Omit when the app should scaffold it from current board and memory context."},
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
		broadcastAssistantEvent("status", "OpenAI Realtime session configured", map[string]any{"eventType": event.Type})
	case "error":
		if event.Error != nil {
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
		app.armScoutVoiceResponse(event.Transcript)
		if !app.transcriptionLaneConnected() {
			app.rememberTranscript(event, "scout_realtime", app.currentRealtimeModel())
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
	case "answer_memory_question", "create_artifact", "archive_meeting":
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
	}
	app.markScoutSpokenResponsePending(outputItem.Name, result, changed, armedAtStart)

	if err := app.SendEvent(map[string]any{
		"type": "conversation.item.create",
		"item": map[string]any{
			"type":    "function_call_output",
			"call_id": outputItem.CallID,
			"output":  mustMarshalJSON(result),
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

	broadcastKanbanEvent("board", app.snapshotState())
	broadcastKanbanEvent("undo_available", app.canUndoDelete())
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
	case "update_artifact":
		return app.updateRealtimeArtifact(args)
	case "answer_memory_question":
		return app.answerMemoryQuestion(args)
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

func (app *kanbanBoardApp) controlApp(args map[string]any) (map[string]any, bool, error) {
	tool := normalizeOSControlTool(asString(args["tool"]))
	if tool == "" {
		return nil, false, fmt.Errorf("tool is required")
	}
	artifactID := firstNonEmptyString(asString(args["artifact_id"]), asString(args["artifactId"]))
	actions := osAssistantActionsForTool(tool, artifactID)
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
	message := "Transcript recording resumed"
	if !enabled {
		message = "Transcript recording paused"
	}
	broadcastKanbanEvent("participants", snapshot)
	broadcastAssistantEvent("action", message, map[string]any{
		"tool":       "set_recording",
		"recording":  recording,
		"voiceState": "listening",
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

	broadcastKanbanEvent("meeting_archived", result)
	broadcastKanbanEvent("memory", app.memorySnapshotForClients(20))
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
	broadcastKanbanEvent("memory", app.memorySnapshotForClients(20))
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
	case "office", "room", "chat", "artifacts", "research", "design", "grill", "board", "memory":
		return strings.ToLower(strings.TrimSpace(tool))
	case "artifact":
		return "artifacts"
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
		result, err := app.resolveAssistantQueryContext(ctx, query, nil)
		if err != nil {
			return nil, false, err
		}
		result = buildOSAssistantModeAnswer(mode, result, app.snapshotState(), app.memorySnapshotForClients(12))
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

	broadcastKanbanEvent("memory", app.memorySnapshotForClients(20))
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

func (app *kanbanBoardApp) rememberTranscript(event kanbanRealtimeEvent, source string, model string) {
	if app == nil || app.memory == nil {
		log.Errorf("Meeting memory unavailable; transcript was not saved")
		return
	}
	if !app.transcriptRecordingActive() {
		log.Infof("Transcript recording disabled; transcript was not saved")
		return
	}

	speaker, confidence := app.speakerForCompletedTranscript(time.Now().UTC())
	entry, appended, err := app.memory.appendAttributedTranscriptWithMetadata(event.EventID, event.ItemID, speaker, confidence, event.Transcript, map[string]string{
		"source": source,
		"model":  model,
	})
	if err != nil {
		log.Errorf("Failed to write meeting memory: %v", err)
		return
	}
	if !appended {
		return
	}

	broadcastAssistantEvent("transcript", "heard: "+entry.Text, nil)
	broadcastKanbanEvent("memory_transcript", entry)
}

func (app *kanbanBoardApp) memorySnapshot(limit int) []meetingMemoryEntry {
	if app == nil || app.memory == nil {
		return nil
	}

	return app.memory.snapshot(limit)
}

// memorySnapshotForClients decorates archive entries with a keyed download
// URL at serve time so archive links keep working behind the archives auth
// gate without persisting the room password into the store.
func (app *kanbanBoardApp) memorySnapshotForClients(limit int) []meetingMemoryEntry {
	entries := app.memorySnapshot(limit)
	for index := range entries {
		entries[index] = decorateArchiveDownloadURLForClient(entries[index])
	}

	return entries
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
	query := canonicalizeBoardText(asString(args["query"]))
	if query == "" {
		return nil, false, fmt.Errorf("query is required")
	}
	if app == nil || app.memory == nil {
		return nil, false, fmt.Errorf("meeting memory is unavailable")
	}

	matches, contextEntries := app.memoryMatchesAndContext(query)
	answer, modelErr := app.answerMemoryQuestionWithModel(query, contextEntries)
	if modelErr != nil {
		log.Errorf("Failed to answer memory question with model: %v", modelErr)
	}
	if strings.TrimSpace(answer) == "" {
		answer = buildMemoryAnswer(query, matches)
	}
	response := map[string]any{
		"query":  query,
		"answer": answer,
	}

	broadcastKanbanEvent("memory_answer", response)
	broadcastAssistantEvent("answer", answer, map[string]any{"query": query})

	return map[string]any{
		"ok":      true,
		"query":   query,
		"answer":  answer,
		"matches": len(matches),
		"context": len(contextEntries),
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
		parsedStatus, err := parseKanbanStatus(rawStatus)
		if err != nil {
			return nil, false, err
		}
		status = parsedStatus
	}

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

func (app *kanbanBoardApp) moveTicket(args map[string]any) (map[string]any, bool, error) {
	cardID := asString(args["card_id"])
	if cardID == "" {
		return nil, false, fmt.Errorf("card_id is required")
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
	cardID := asString(args["card_id"])
	if cardID == "" {
		return nil, false, fmt.Errorf("card_id is required")
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
	cardID := asString(args["card_id"])
	if cardID == "" {
		return nil, false, fmt.Errorf("card_id is required")
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

func (app *kanbanBoardApp) admitParticipant(name string) (string, error) {
	return app.admitParticipantSession(name, "")
}

func (app *kanbanBoardApp) admitParticipantSession(name string, sessionID string) (string, error) {
	name = canonicalParticipantName(name)
	if name == "" {
		return "", fmt.Errorf("choose a listed participant and enter the room password")
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	capacity := configuredMeetingRoomCapacity()
	if active := app.activeParticipantCountLocked(); app.participantCounts[name] <= 0 && active >= capacity {
		return "", fmt.Errorf("the room is full. this room supports %d people with video on", capacity)
	}

	now := time.Now().UTC()
	app.participants[name] = now
	app.participantCounts[name] = 1
	app.participantSessions[name] = sessionID
	app.participantMedia[name] = participantMediaState{
		UpdatedAt: now.Format(time.RFC3339Nano),
	}

	return name, nil
}

func (app *kanbanBoardApp) forgetParticipant(name string) {
	app.forgetParticipantSession(name, "")
}

func (app *kanbanBoardApp) forgetParticipantSession(name string, sessionID string) bool {
	name = canonicalParticipantName(name)
	if name == "" {
		return false
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	if sessionID != "" {
		currentSessionID, ok := app.participantSessions[name]
		if !ok || currentSessionID != sessionID {
			return false
		}
	}

	delete(app.participantCounts, name)
	delete(app.participants, name)
	delete(app.participantSessions, name)
	delete(app.participantMedia, name)

	return true
}

func (app *kanbanBoardApp) participantSessionCurrent(name string, sessionID string) bool {
	name = canonicalParticipantName(name)
	if name == "" {
		return false
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	if app.participantCounts[name] <= 0 {
		return false
	}
	if sessionID == "" {
		return true
	}

	return app.participantSessions[name] == sessionID
}

func (app *kanbanBoardApp) activeParticipantCount() int {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.activeParticipantCountLocked()
}

func (app *kanbanBoardApp) activeParticipantCountLocked() int {
	active := 0
	for _, count := range app.participantCounts {
		if count > 0 {
			active++
		}
	}

	return active
}

func (app *kanbanBoardApp) participantSnapshot() []string {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.participantSnapshotLocked()
}

func (app *kanbanBoardApp) participantSnapshotLocked() []string {
	participants := make([]string, 0, len(app.participants))
	for _, candidate := range meetingParticipantNames {
		if app.participantCounts[candidate] > 0 {
			participants = append(participants, candidate)
		}
	}

	return participants
}

func (app *kanbanBoardApp) roomSnapshot() map[string]any {
	capacity := configuredMeetingRoomCapacity()

	app.mu.Lock()
	defer app.mu.Unlock()

	return app.roomSnapshotLocked(capacity)
}

func (app *kanbanBoardApp) setParticipantMediaState(name string, state participantMediaState) (map[string]any, error) {
	name = canonicalParticipantName(name)
	if name == "" {
		return nil, fmt.Errorf("unknown participant")
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	if app.participantCounts[name] <= 0 {
		return nil, fmt.Errorf("%s is not in the room", name)
	}

	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	app.participantMedia[name] = state

	return app.roomSnapshotLocked(configuredMeetingRoomCapacity()), nil
}

func (app *kanbanBoardApp) setParticipantScreenSharing(name string, screenSharing bool) map[string]any {
	name = canonicalParticipantName(name)
	if name == "" {
		return app.roomSnapshot()
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	if app.participantCounts[name] <= 0 {
		return app.roomSnapshotLocked(configuredMeetingRoomCapacity())
	}

	state := app.participantMedia[name]
	state.ScreenSharing = screenSharing
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	app.participantMedia[name] = state

	return app.roomSnapshotLocked(configuredMeetingRoomCapacity())
}

func (app *kanbanBoardApp) transcriptRecordingActive() bool {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.transcriptRecordingEnabled
}

func (app *kanbanBoardApp) setTranscriptRecording(enabled bool, updatedBy string) map[string]any {
	updatedBy = canonicalRoomActorName(updatedBy)

	app.mu.Lock()
	defer app.mu.Unlock()

	if app.transcriptRecordingEnabled != enabled || app.transcriptRecordingUpdatedAt.IsZero() {
		app.transcriptRecordingEnabled = enabled
		app.transcriptRecordingUpdatedAt = time.Now().UTC()
		app.transcriptRecordingUpdatedBy = updatedBy
	}

	return app.roomSnapshotLocked(configuredMeetingRoomCapacity())
}

func (app *kanbanBoardApp) roomRecordingStateLocked() roomRecordingState {
	state := roomRecordingState{
		Enabled:   app.transcriptRecordingEnabled,
		UpdatedBy: app.transcriptRecordingUpdatedBy,
	}
	if !app.transcriptRecordingUpdatedAt.IsZero() {
		state.UpdatedAt = app.transcriptRecordingUpdatedAt.UTC().Format(time.RFC3339Nano)
	}

	return state
}

func (app *kanbanBoardApp) roomSnapshotLocked(capacity int) map[string]any {
	participants := app.participantSnapshotLocked()
	occupiedSeats := app.activeParticipantCountLocked()

	availableSeats := capacity - occupiedSeats
	if availableSeats < 0 {
		availableSeats = 0
	}
	mediaStates := make(map[string]participantMediaState, len(participants))
	for _, participant := range participants {
		mediaStates[participant] = app.participantMedia[participant]
	}

	return map[string]any{
		"participants":   participants,
		"capacity":       capacity,
		"occupiedSeats":  occupiedSeats,
		"availableSeats": availableSeats,
		"mediaStates":    mediaStates,
		"recording":      app.roomRecordingStateLocked(),
	}
}

func (app *kanbanBoardApp) archiveMeeting(archivedBy string) (meetingArchiveResult, error) {
	// flush ambient agents first so the final minutes of the meeting are
	// summarized and applied to the board before the snapshot is taken.
	app.flushAmbientAgentsForArchive()

	archivedBy = canonicalRoomActorName(archivedBy)
	archivedAt := time.Now().UTC()
	archiveID := fmt.Sprintf("meeting-%s", archivedAt.Format("20060102-150405-000000000"))
	board := app.snapshotState()
	memory := app.memorySnapshot(2000)
	participants := app.participantSnapshot()
	if len(participants) == 0 && archivedBy != "" {
		participants = []string{archivedBy}
	}
	notes := buildMeetingNotes(archiveID, archivedAt, archivedBy, board, memory, participants)
	email := meetingEmailStatus{
		Recipients: participantEmails(participants),
		Skipped:    true,
		Reason:     "Email delivery has not run yet.",
	}
	archive := meetingArchive{
		ID:           archiveID,
		ArchivedAt:   archivedAt,
		ArchivedBy:   archivedBy,
		Board:        board,
		Memory:       memory,
		Participants: participants,
		Notes:        notes,
		Email:        email,
	}

	archivePath, err := meetingArchivePath(archiveID)
	if err != nil {
		return meetingArchiveResult{}, err
	}

	if err := writeMeetingArchive(archivePath, archive); err != nil {
		return meetingArchiveResult{}, fmt.Errorf("write meeting archive: %w", err)
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
		})
		if err != nil {
			return meetingArchiveResult{}, fmt.Errorf("remember meeting artifact: %w", err)
		}
		clientArtifact := decorateArchiveDownloadURLForClient(artifactEntry)
		artifact = &clientArtifact
		// the archive closes the current meeting; the next entry starts a new one.
		app.memory.rotateMeetingID()
	}

	return meetingArchiveResult{
		ID:          archiveID,
		ArchivedAt:  archivedAt.Format(time.RFC3339Nano),
		ArchivedBy:  archivedBy,
		DownloadURL: meetingArchiveDownloadURLWithKey(archiveID),
		Summary:     summary,
		Notes:       notes,
		Email:       email,
		Artifact:    artifact,
	}, nil
}

func meetingArchiveArtifactTitle(archive meetingArchive) string {
	title := strings.TrimSpace(archive.Notes.Subject)
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

	return writeKanbanBoardState(kanbanBoardPath(), state)
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
		ID:       card.ID,
		Status:   card.Status,
		Title:    card.Title,
		Notes:    card.Notes,
		Owner:    card.Owner,
		Tags:     append([]string(nil), card.Tags...),
		DueDate:  card.DueDate,
		KeyDates: cloneKanbanKeyDates(card.KeyDates),
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

func parseKanbanStatus(value any) (kanbanStatus, error) {
	status := kanbanStatus(asString(value))
	for _, candidate := range kanbanStatuses {
		if candidate == status {
			return status, nil
		}
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

func broadcastKanbanEvent(event string, data any) {
	raw, err := json.Marshal(map[string]any{
		"event": event,
		"data":  data,
	})
	if err != nil {
		log.Errorf("Failed to encode Kanban event: %v", err)
		return
	}

	listLock.RLock()
	websockets := make([]*threadSafeWriter, 0, len(peerConnections))
	for _, state := range peerConnections {
		if state.websocket != nil {
			websockets = append(websockets, state.websocket)
		}
	}
	listLock.RUnlock()

	for _, websocket := range websockets {
		if err := websocket.WriteJSON(&websocketMessage{
			Event: "kanban",
			Data:  string(raw),
		}); err != nil {
			if isExpectedKanbanBroadcastClose(err) {
				continue
			}
			log.Errorf("Failed to send Kanban event: %v", err)
		}
	}
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
