package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
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
	defaultReasoningEffort    = "low"
	defaultRealtimeVADType    = "semantic_vad"
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
	ID          string             `json:"id"`
	ArchivedAt  string             `json:"archivedAt"`
	ArchivedBy  string             `json:"archivedBy,omitempty"`
	DownloadURL string             `json:"downloadUrl"`
	Summary     string             `json:"summary"`
	Notes       meetingNotes       `json:"notes"`
	Email       meetingEmailStatus `json:"email"`
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
	mu                  sync.Mutex
	cards               []kanbanCard
	nextCreatedIndex    int
	updatedAt           time.Time
	handledCalls        map[string]struct{}
	memory              *meetingMemoryStore
	participants        map[string]time.Time
	participantCounts   map[string]int
	participantSessions map[string]string
	participantMedia    map[string]participantMediaState
	lastDeletedCard     *kanbanCard
	apiKey              string
	restarting          bool
	assistantStatus     string

	model                    string
	pc                       *webrtc.PeerConnection
	events                   *webrtc.DataChannel
	inputTrack               *webrtc.TrackLocalStaticSample
	inputEnc                 *opusEncoder
	connected                bool
	forwardedAudioNotice     bool
	realtimeResponseActive   bool
	scoutVoiceArmedAt        time.Time
	scoutVoiceArmedUntil     time.Time
	scoutSpokenResponse      bool
	scoutSpokenResponseSent  bool
	transcriptLane           *meetingTranscriptionLane
	audioActivity            []participantAudioFrame
	currentSpeechStartedAt   time.Time
	currentSpeechStoppedAt   time.Time
	proactiveReconnectCancel chan struct{}
	brainWorkerCancel        chan struct{}
	brainWorkerDone          chan struct{}
	brainWorkerBaselineID    string
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
		cards:               cards,
		nextCreatedIndex:    nextKanbanCardIndex(cards),
		updatedAt:           updatedAt,
		handledCalls:        map[string]struct{}{},
		memory:              memory,
		participants:        map[string]time.Time{},
		participantCounts:   map[string]int{},
		participantSessions: map[string]string{},
		participantMedia:    map[string]participantMediaState{},
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
		cancelProactiveRestart := app.proactiveReconnectCancel
		app.proactiveReconnectCancel = nil
		brainWorkerCancel := app.brainWorkerCancel
		brainWorkerDone := app.brainWorkerDone
		transcriptLane := app.transcriptLane
		app.transcriptLane = nil
		app.brainWorkerCancel = nil
		app.brainWorkerDone = nil
		app.brainWorkerBaselineID = ""
		app.mu.Unlock()
		if transcriptLane != nil {
			transcriptLane.close()
		}
		if cancelProactiveRestart != nil {
			close(cancelProactiveRestart)
		}
		if brainWorkerCancel != nil {
			close(brainWorkerCancel)
			if brainWorkerDone != nil {
				<-brainWorkerDone
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
	broadcastKanbanEvent("participant_track", map[string]any{
		"name":          scoutParticipantName,
		"kind":          t.Kind().String(),
		"trackId":       forwardedTrackID,
		"sourceTrackId": t.ID(),
		"streamId":      t.StreamID(),
	})

	trackLocal, err := addTrack(t, scoutParticipantName, "scout")
	if err != nil {
		log.Errorf("Failed to create local track for Scout voice=%s: %v", t.ID(), err)
		return
	}
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
		return "", fmt.Errorf("Realtime session failed: status=%s body=%s", response.Status, strings.TrimSpace(string(answerSDP)))
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
		"tool_choice":  "required",
	}

	if usesAdvancedCommandProfile(model) {
		session["reasoning"] = map[string]any{
			"effort": realtimeReasoningEffort(),
		}
	}

	return session
}

func (app *kanbanBoardApp) sessionUpdateEvent() map[string]any {
	return map[string]any{
		"type":    "session.update",
		"session": app.sessionConfig(app.model),
	}
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
	case "semantic_vad", "":
		return map[string]any{
			"type":               "semantic_vad",
			"eagerness":          realtimeVADEagerness(),
			"create_response":    true,
			"interrupt_response": true,
		}
	default:
		return realtimeTurnDetectionConfigWithDefaults()
	}
}

func realtimeTurnDetectionConfigWithDefaults() map[string]any {
	return map[string]any{
		"type":               defaultRealtimeVADType,
		"eagerness":          realtimeVADEagerness(),
		"create_response":    true,
		"interrupt_response": true,
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

func transcriptStartsWithScoutWakePhrase(text string) bool {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})

	return len(words) >= 2 &&
		words[0] == scoutWakePhraseFirstWord &&
		words[1] == scoutWakePhraseSecondWord
}

func (app *kanbanBoardApp) armScoutVoiceResponse(transcript string) {
	if !transcriptStartsWithScoutWakePhrase(transcript) {
		return
	}

	now := time.Now()
	app.mu.Lock()
	app.scoutVoiceArmedAt = now
	app.scoutVoiceArmedUntil = now.Add(scoutVoiceArmDuration)
	app.mu.Unlock()

	broadcastAssistantEvent("status", "Scout heard the wake phrase.", map[string]any{"wakePhrase": "Hey Scout"})
}

func (app *kanbanBoardApp) clearScoutVoiceArmForNewSpeech() {
	now := time.Now()

	app.mu.Lock()
	if !app.scoutVoiceArmedAt.IsZero() && now.After(app.scoutVoiceArmedAt) {
		app.scoutVoiceArmedAt = time.Time{}
		app.scoutVoiceArmedUntil = time.Time{}
	}
	app.mu.Unlock()
}

func (app *kanbanBoardApp) markScoutSpokenResponsePending(toolName string, result map[string]any, changed bool) {
	if !scoutToolShouldSpeak(toolName, result, changed) {
		return
	}

	now := time.Now()
	app.mu.Lock()
	defer app.mu.Unlock()

	if app.scoutVoiceArmedUntil.IsZero() || now.After(app.scoutVoiceArmedUntil) {
		return
	}

	app.scoutVoiceArmedAt = time.Time{}
	app.scoutVoiceArmedUntil = time.Time{}
	app.scoutSpokenResponse = true
	app.scoutSpokenResponseSent = false
}

func scoutToolShouldSpeak(toolName string, result map[string]any, changed bool) bool {
	if ok, exists := result["ok"].(bool); exists && !ok {
		return true
	}

	switch toolName {
	case "answer_memory_question", "do_nothing":
		return true
	default:
		return changed
	}
}

func (app *kanbanBoardApp) flushScoutSpokenResponseIfPending() {
	app.mu.Lock()
	if !app.scoutSpokenResponse || app.scoutSpokenResponseSent {
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
	return strings.Join([]string{
		"# Role and Objective\nYou are Scout, a voice-operated Kanban board operator for live standups and project meetings. Keep the board accurate, compact, and useful with minimal chatter.",
		fmt.Sprintf("# Board\nCurrent Kanban board JSON: %s\nAvailable columns: Backlog, In Progress, Blocked, Done.\nKnown meeting participants: %s.", app.boardContextJSON(), strings.Join(meetingParticipantNames, ", ")),
		fmt.Sprintf("# Domain vocabulary\nUse these exact spellings for names, brands, acronyms, and technical terms: %s. Boot Barn is a known brand; do not write Suit Barn when the user says Boot Barn.", strings.Join(domainVocabulary(), ", ")),
		"# Language\nUsers may say ticket, card, task, issue, or sticky note; treat those as Kanban cards. If a transcript includes a speaker label such as Sean:, do not include the label in the title; use it only as context for owner, notes, or tags.",
		"# Reasoning\nFor direct board operations and simple recall requests, act quickly. For multi-step updates, ambiguous references, or memory questions, reason before choosing tools. Do not spend extra reasoning on unclear audio; ask for clarification through do_nothing.",
		"# Preambles\nDo not speak preambles for routine board updates. Only speak to the room after a tool result when the clear user turn started with Hey Scout. Otherwise stay silent and use tools.",
		"# Field writing\nWrite card fields as direct project facts, not narration about the user request. Never start titles or notes with phrases like User said, User asked, User requested, or The user wants. Put due dates, key dates, milestone dates, and deadlines in due_date/key_dates or add_key_date; do not put a requested date only in notes. If the user says add Impossible Moments to the board because it is blocked waiting on Erick, use title Impossible Moments, status Blocked, owner Erick, and notes Waiting on Erick.",
		"# Unclear audio\nOnly operate on clear audio or clear typed text. Do not guess proper nouns, brand names, project names, acronyms, owners, or card titles. If the exact entity is unclear, call do_nothing with a concise clarification question instead of creating or updating a card.",
		"# Entity capture\nPreserve exact names, brands, owners, card titles, dates, and project terms. For high-precision identifiers or ambiguous names, normalize only what is clear. If multiple interpretations are plausible, call do_nothing with one clarification question.",
		"# Matching\nUse existing card ids exactly as provided. Match by meaning across title, notes, owner, and tags. Update an existing related card instead of creating a duplicate when the work is already represented. If you are not sure which existing card the user means, call do_nothing with a concise clarification question.",
		"# Status rules\nConcrete first-person status updates are implicit board operations. Started, began, picked up, or working on means In Progress. Shipped, fixed, completed, closed, finished, or resolved means Done. Blocked, waiting, dependent, needs another team, might slip, or at risk means Blocked and should preserve blocker details in notes with blocked, dependency, or risk tags. Park, punt, defer, or move back means Backlog.",
		"# Owner rules\nWhen the speaker names a responsible person, set owner to that exact participant name. Use Unassigned when responsibility is unclear.",
		"# Tools\nUse only the tools listed in this session. If one utterance changes status, notes, owner, tags, and dates for the same existing card, prefer one update_ticket call with all changed fields. Use add_key_date for a pure date or milestone addition to an existing card. Use move_ticket only for a pure status move. Use add_tags only for a pure tag addition. Use create_ticket only when no existing card captures the work. If one transcript contains multiple unrelated operations, call one tool for each operation. Only say an action completed after the tool result succeeds.",
		"# Memory\nMeeting transcripts are saved as durable memory with speaker labels when Scout can attribute the speaker. A scheduled brain worker also writes durable summaries with transcript references. If the user asks what was said, decided, discussed, remembered, mentioned earlier, how a meeting went, or asks any recall question, call answer_memory_question with the user's full question as the query.",
		"# No-op and background audio\nIf the latest audio is silence, background noise, side conversation, filler, wrap-up, or a handoff with no concrete board operation or recall request, call do_nothing with a short reason. Do not say I'm here, I didn't catch that, or take your time.",
		"# Wake phrase\nOnly speak to the room when the user's clear utterance starts with the exact wake phrase Hey Scout. Treat Hey Scout as an address to you, not as content to save on the board. If the utterance does not start with Hey Scout, stay silent after tool calls.",
		"# Verbosity\nPrefer tools over text replies. Do not narrate board operations aloud unless the utterance started with Hey Scout; then keep any spoken response to one short sentence. For memory answers, give the headline first and only the most useful details.",
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
		"description": "Key dates or milestones to add or update on the card. Preserve useful existing dates unless the user asks to replace them.",
		"items":       keyDateProperty,
	}
	ownerProperty := map[string]any{
		"type":        "string",
		"description": "Responsible participant when the user names an owner or the work clearly belongs to someone.",
		"enum":        append([]string{"Unassigned"}, meetingParticipantNames...),
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
			broadcastKanbanEvent("status", event.Error.Message)
			broadcastAssistantEvent("error", event.Error.Message, map[string]any{"code": event.Error.Code})
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
		app.clearScoutVoiceArmForNewSpeech()
		broadcastAssistantEvent("audio", "assistant detected speech", map[string]any{"eventType": event.Type})
	case "input_audio_buffer.speech_stopped":
		if !app.transcriptionLaneConnected() {
			app.noteRealtimeSpeechStopped()
		}
		broadcastAssistantEvent("audio", "assistant detected silence", map[string]any{"eventType": event.Type})
	case "input_audio_buffer.committed":
		broadcastAssistantEvent("audio", "assistant committed a speech turn", map[string]any{"eventType": event.Type})
	case "response.created":
		app.markRealtimeResponseActive(true)
	case "response.output_audio_transcript.done":
		if text := canonicalizeBoardText(firstNonEmptyString(event.Transcript, event.Text)); text != "" {
			app.markScoutSpokenResponseDelivered()
			broadcastAssistantEvent("answer", text, map[string]any{"eventType": event.Type})
		}
	case "response.output_text.done":
		if text := canonicalizeBoardText(firstNonEmptyString(event.Text, event.Transcript)); text != "" {
			app.markScoutSpokenResponseDelivered()
			broadcastAssistantEvent("answer", text, map[string]any{"eventType": event.Type})
		}
	case "response.output_item.done":
		if event.Item != nil && event.Item.Type == "function_call" {
			app.handleToolCall(*event.Item, false)
		}
	case "response.function_call_arguments.done":
		app.handleToolCall(realtimeFunctionCallFromArgumentsDone(event), true)
	case "response.done":
		app.markRealtimeResponseActive(false)
		hadFunctionCall := false
		if event.Response != nil {
			for _, outputItem := range event.Response.Output {
				if outputItem.Type == "function_call" {
					hadFunctionCall = true
					app.handleToolCall(outputItem, false)
				}
			}
		}
		if !hadFunctionCall {
			app.markScoutSpokenResponseDelivered()
		}
		app.flushScoutSpokenResponseIfPending()
	default:
		if text := strings.TrimSpace(event.Text); text != "" && strings.Contains(event.Type, "text") {
			broadcastAssistantEvent("answer", text, map[string]any{"eventType": event.Type})
		}
	}
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
	if parseErr != nil && allowIncompleteArguments && isIncompleteToolArgumentsError(parseErr) {
		log.Infof("Waiting for complete %s arguments for call_id=%s", outputItem.Name, outputItem.CallID)
		return
	}

	app.mu.Lock()
	if _, ok := app.handledCalls[outputItem.CallID]; ok {
		app.mu.Unlock()
		return
	}
	app.handledCalls[outputItem.CallID] = struct{}{}
	app.mu.Unlock()

	broadcastAssistantEvent("action", "using "+humanizeToolName(outputItem.Name), map[string]any{"tool": outputItem.Name})

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
	app.markScoutSpokenResponsePending(outputItem.Name, result, changed)

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

func parseToolCallArguments(outputItem kanbanRealtimeOutputItem) (map[string]any, error) {
	args := map[string]any{}
	if rawArgs := strings.TrimSpace(outputItem.Arguments); rawArgs != "" {
		if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
			return nil, fmt.Errorf("parse %s arguments: %w", outputItem.Name, err)
		}
	}

	return args, nil
}

func isIncompleteToolArgumentsError(err error) bool {
	return strings.Contains(err.Error(), "unexpected end of JSON input")
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
	case "update_ticket":
		return app.updateTicket(args)
	case "delete_ticket":
		return app.deleteTicket(args)
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

func (app *kanbanBoardApp) rememberTranscript(event kanbanRealtimeEvent, source string, model string) {
	if app == nil || app.memory == nil {
		log.Errorf("Meeting memory unavailable; transcript was not saved")
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
	if hasKeyDates && len(keyDates) > 0 {
		nextKeyDates := mergeKanbanKeyDates(card.KeyDates, keyDates...)
		if !kanbanKeyDatesEqual(card.KeyDates, nextKeyDates) {
			card.KeyDates = nextKeyDates
			changed = true
		}
		if keyDatesDueDate := dueDateFromKeyDates(keyDates); keyDatesDueDate != "" && card.DueDate != keyDatesDueDate {
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
	}
}

func (app *kanbanBoardApp) archiveMeeting(archivedBy string) (meetingArchiveResult, error) {
	archivedBy = canonicalParticipantName(archivedBy)
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
	if app.memory != nil {
		_, _, err = app.memory.appendArchive(archiveID, summary, map[string]string{
			"archiveId":   archiveID,
			"downloadUrl": meetingArchiveDownloadURL(archiveID),
			"archivedBy":  archivedBy,
		})
		if err != nil {
			return meetingArchiveResult{}, fmt.Errorf("remember meeting archive: %w", err)
		}
	}

	return meetingArchiveResult{
		ID:          archiveID,
		ArchivedAt:  archivedAt.Format(time.RFC3339Nano),
		ArchivedBy:  archivedBy,
		DownloadURL: meetingArchiveDownloadURL(archiveID),
		Summary:     summary,
		Notes:       notes,
		Email:       email,
	}, nil
}

func writeMeetingArchive(path string, archive meetingArchive) error {
	return writeJSONFileAtomically(path, "meeting archive", archive)
}

func meetingArchiveDownloadURL(archiveID string) string {
	return "/archives/" + archiveID + ".json"
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

func asString(value any) string {
	candidate, ok := value.(string)
	if !ok {
		return ""
	}

	return strings.TrimSpace(candidate)
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

func normalizeKeyDateText(value string) string {
	return strings.Trim(canonicalizeBoardText(value), "\"'")
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
			log.Errorf("Failed to send Kanban event: %v", err)
		}
	}
}
