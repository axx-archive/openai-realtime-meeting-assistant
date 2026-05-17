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
	defaultReasoningEffort    = "medium"
	defaultRealtimeVADType    = "semantic_vad"
	defaultVADEagerness       = "low"
	defaultKanbanBoardPath    = "data/kanban-board.json"
	realtimeEventChannelLabel = "oai-events"
	realtimeInputTrackID      = "kanban-realtime:mixed-audio"
	realtimeInputStreamID     = "kanban-realtime-input"
	realtimeMixedAudioSinkKey = "kanban-realtime"
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

type kanbanCard struct {
	ID     string       `json:"id"`
	Status kanbanStatus `json:"status"`
	Title  string       `json:"title"`
	Notes  string       `json:"notes"`
	Owner  string       `json:"owner,omitempty"`
	Tags   []string     `json:"tags"`
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
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	CallID    string `json:"call_id,omitempty"`
}

type kanbanBoardApp struct {
	mu                sync.Mutex
	cards             []kanbanCard
	nextCreatedIndex  int
	updatedAt         time.Time
	handledCalls      map[string]struct{}
	memory            *meetingMemoryStore
	participants      map[string]time.Time
	participantCounts map[string]int
	participantMedia  map[string]participantMediaState
	lastDeletedCard   *kanbanCard
	apiKey            string
	restarting        bool
	assistantStatus   string

	model                    string
	pc                       *webrtc.PeerConnection
	events                   *webrtc.DataChannel
	inputTrack               *webrtc.TrackLocalStaticSample
	inputEnc                 *opusEncoder
	connected                bool
	forwardedAudioNotice     bool
	proactiveReconnectCancel chan struct{}
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
		cards:             cards,
		nextCreatedIndex:  nextKanbanCardIndex(cards),
		updatedAt:         updatedAt,
		handledCalls:      map[string]struct{}{},
		memory:            memory,
		participants:      map[string]time.Time{},
		participantCounts: map[string]int{},
		participantMedia:  map[string]participantMediaState{},
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

	return app.startRealtimePeer(apiKey, realtimeModel())
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
			}
			app.mu.Unlock()
			return
		}
		if roomMixer != nil {
			roomMixer.setSink(realtimeMixedAudioSinkKey, app)
		}
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
	cancelProactiveRestart := app.proactiveReconnectCancel
	app.proactiveReconnectCancel = nil
	app.mu.Unlock()

	defer func() {
		app.mu.Lock()
		app.restarting = false
		app.mu.Unlock()
	}()

	if roomMixer != nil {
		roomMixer.removeSink(realtimeMixedAudioSinkKey)
	}
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
		cancelProactiveRestart := app.proactiveReconnectCancel
		app.proactiveReconnectCancel = nil
		app.mu.Unlock()
		if cancelProactiveRestart != nil {
			close(cancelProactiveRestart)
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
		"output_modalities": []string{"text"},
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

func realtimeModel() string {
	if model := strings.TrimSpace(os.Getenv("OPENAI_REALTIME_MODEL")); model != "" {
		return model
	}

	return defaultRealtimeModel
}

func realtimeReasoningEffort() string {
	effort := strings.ToLower(strings.TrimSpace(os.Getenv("OPENAI_REALTIME_REASONING_EFFORT")))
	switch effort {
	case "low", "medium", "high":
		return effort
	default:
		return defaultReasoningEffort
	}
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

func (app *kanbanBoardApp) sessionInstructions() string {
	return strings.Join([]string{
		"# Role\nYou are a voice-operated Kanban board operator for live standups and project meetings. Keep the board accurate, compact, and useful with minimal chatter.",
		fmt.Sprintf("# Board\nCurrent Kanban board JSON: %s\nAvailable columns: Backlog, In Progress, Blocked, Done.\nKnown meeting participants: %s.", app.boardContextJSON(), strings.Join(meetingParticipantNames, ", ")),
		fmt.Sprintf("# Domain vocabulary\nUse these exact spellings for names, brands, acronyms, and technical terms: %s. Boot Barn is a known brand; do not write Suit Barn when the user says Boot Barn.", strings.Join(domainVocabulary(), ", ")),
		"# Language\nUsers may say ticket, card, task, issue, or sticky note; treat those as Kanban cards. If a transcript includes a speaker label such as Sean:, do not include the label in the title; use it only as context for owner, notes, or tags.",
		"# Field writing\nWrite card fields as direct project facts, not narration about the user request. Never start titles or notes with phrases like User said, User asked, User requested, or The user wants. If the user says add Impossible Moments to the board because it is blocked waiting on Erick, use title Impossible Moments, status Blocked, owner Erick, and notes Waiting on Erick.",
		"# Unclear audio\nOnly operate on clear audio or clear typed text. Do not guess proper nouns, brand names, project names, acronyms, owners, or card titles. If the exact entity is unclear, call do_nothing with a concise clarification question instead of creating or updating a card.",
		"# Matching\nUse existing card ids exactly as provided. Match by meaning across title, notes, owner, and tags. Update an existing related card instead of creating a duplicate when the work is already represented. If you are not sure which existing card the user means, call do_nothing with a concise clarification question.",
		"# Status rules\nConcrete first-person status updates are implicit board operations. Started, began, picked up, or working on means In Progress. Shipped, fixed, completed, closed, finished, or resolved means Done. Blocked, waiting, dependent, needs another team, might slip, or at risk means Blocked and should preserve blocker details in notes with blocked, dependency, or risk tags. Park, punt, defer, or move back means Backlog.",
		"# Owner rules\nWhen the speaker names a responsible person, set owner to that exact participant name. Use Unassigned when responsibility is unclear.",
		"# Tool policy\nIf one utterance changes status, notes, owner, and tags for the same existing card, prefer one update_ticket call with all changed fields. Use move_ticket only for a pure status move. Use add_tags only for a pure tag addition. Use create_ticket only when no existing card captures the work. If one transcript contains multiple unrelated operations, call one tool for each operation.",
		"# Memory policy\nMeeting transcripts are saved as durable memory. If the user asks what was said, decided, discussed, remembered, mentioned earlier, or asks any recall question, call answer_memory_question with the user's full question as the query.",
		"# No-op policy\nIf the user is wrapping up, handing off, giving filler, or not giving a concrete board operation or recall request, call do_nothing with a short user-visible reason.",
		"# Response policy\nPrefer tools over text replies. Do not narrate board operations aloud.",
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
					"title":  map[string]any{"type": "string", "description": "Concise title for the work, without speaker prefixes such as Sean:. Preserve exact proper nouns and domain spellings; if unsure, use do_nothing instead."},
					"notes":  map[string]any{"type": "string", "description": "Direct project facts only. Include blocker, dependency, or schedule-risk details, but do not narrate the command or write phrases like User requested, User said, or asked to add this to the board. Preserve exact proper nouns and domain spellings; if unsure, use do_nothing instead."},
					"owner":  ownerProperty,
					"tags":   tagsProperty,
					"status": statusProperty,
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
			"name":        "update_ticket",
			"description": "Update one existing Kanban ticket/card atomically. Prefer this when one utterance changes status, owner, notes, title, or tags for the same card.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"card_id": map[string]any{"type": "string", "description": "Existing board card id."},
					"title":   map[string]any{"type": "string", "description": "Replacement title, when the existing title should be made clearer. Preserve exact proper nouns and domain spellings; if unsure, use do_nothing instead."},
					"notes":   map[string]any{"type": "string", "description": "Full replacement notes as direct project facts. Preserve useful existing notes while adding the new context, but do not narrate the command or write phrases like User requested, User said, or asked to update this card. Preserve exact proper nouns and domain spellings; if unsure, use do_nothing instead."},
					"owner":   ownerProperty,
					"tags":    tagsToAddProperty,
					"status":  statusProperty,
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
			"description": "Answer a user question by recalling the saved meeting transcript and memory.",
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
				broadcastAssistantEvent("status", "Scout is still finishing the last turn.", map[string]any{"code": event.Error.Code})
				return
			}
			broadcastKanbanEvent("status", event.Error.Message)
			broadcastAssistantEvent("error", event.Error.Message, map[string]any{"code": event.Error.Code})
		}
	case "conversation.item.input_audio_transcription.completed":
		app.rememberTranscript(event)
	case "conversation.item.input_audio_transcription.delta":
		if text := canonicalizeBoardText(event.Delta); text != "" {
			broadcastAssistantEvent("transcript", "hearing: "+text, map[string]any{"eventType": event.Type})
		}
	case "input_audio_buffer.speech_started":
		broadcastAssistantEvent("audio", "assistant detected speech", map[string]any{"eventType": event.Type})
	case "input_audio_buffer.speech_stopped":
		broadcastAssistantEvent("audio", "assistant detected silence", map[string]any{"eventType": event.Type})
	case "input_audio_buffer.committed":
		broadcastAssistantEvent("audio", "assistant committed a speech turn", map[string]any{"eventType": event.Type})
	case "response.output_item.done":
		if event.Item != nil && event.Item.Type == "function_call" {
			app.handleToolCall(*event.Item)
		}
	case "response.function_call_arguments.done":
		app.handleToolCall(kanbanRealtimeOutputItem{
			Type:      "function_call",
			Name:      event.Name,
			Arguments: event.Arguments,
			CallID:    event.CallID,
		})
	case "response.done":
		if event.Response == nil {
			return
		}
		for _, outputItem := range event.Response.Output {
			if outputItem.Type == "function_call" {
				app.handleToolCall(outputItem)
			}
		}
	default:
		if text := strings.TrimSpace(event.Text); text != "" && strings.Contains(event.Type, "text") {
			broadcastAssistantEvent("answer", text, map[string]any{"eventType": event.Type})
		}
	}
}

func (app *kanbanBoardApp) handleToolCall(outputItem kanbanRealtimeOutputItem) {
	if strings.TrimSpace(outputItem.CallID) == "" {
		log.Errorf("Ignoring Kanban tool call %q without call_id", outputItem.Name)
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

	result, changed, err := app.applyToolCall(outputItem)
	if err != nil {
		result = map[string]any{
			"ok":    false,
			"error": err.Error(),
		}
		broadcastAssistantEvent("error", err.Error(), map[string]any{"tool": outputItem.Name})
	}

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
	args := map[string]any{}
	if rawArgs := strings.TrimSpace(outputItem.Arguments); rawArgs != "" {
		if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
			return nil, false, fmt.Errorf("parse %s arguments: %w", outputItem.Name, err)
		}
	}

	switch outputItem.Name {
	case "create_ticket":
		return app.createTicket(args)
	case "move_ticket":
		return app.moveTicket(args)
	case "add_tags":
		return app.addTags(args)
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
		return nil, false, fmt.Errorf("unsupported function %q", outputItem.Name)
	}
}

func (app *kanbanBoardApp) rememberTranscript(event kanbanRealtimeEvent) {
	entry, appended, err := app.memory.appendTranscript(event.EventID, event.ItemID, event.Transcript)
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
	return app.memory.snapshot(limit)
}

func (app *kanbanBoardApp) answerMemoryQuestion(args map[string]any) (map[string]any, bool, error) {
	query := canonicalizeBoardText(asString(args["query"]))
	if query == "" {
		return nil, false, fmt.Errorf("query is required")
	}

	matches := app.memory.search(query, 5)
	answer := buildMemoryAnswer(query, matches)
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
		ID:     app.createCardIDLocked(),
		Status: status,
		Title:  title,
		Notes:  notes,
		Owner:  owner,
		Tags:   tags,
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

func (app *kanbanBoardApp) updateTicket(args map[string]any) (map[string]any, bool, error) {
	cardID := asString(args["card_id"])
	if cardID == "" {
		return nil, false, fmt.Errorf("card_id is required")
	}

	title := canonicalizeBoardText(asString(args["title"]))
	notes := cleanBoardNotes(asString(args["notes"]))
	owner := normalizeCardOwner(args["owner"])
	tags := canonicalizeBoardTags(asStringSlice(args["tags"]))
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

	app.mu.Lock()
	defer app.mu.Unlock()

	card, ok := app.findCardLocked(cardID)
	if !ok {
		return nil, false, fmt.Errorf("unknown card_id: %s", cardID)
	}
	if card.Title == title &&
		card.Status == status &&
		card.Owner == owner &&
		card.Notes == notes &&
		stringSlicesEqual(card.Tags, tags) {
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
	name = canonicalParticipantName(name)
	if name == "" {
		return "", fmt.Errorf("choose a listed participant and enter the room password")
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	capacity := configuredMeetingRoomCapacity()
	if active := app.activeParticipantCountLocked(); active >= capacity {
		return "", fmt.Errorf("the room is full. this room supports %d people with video on", capacity)
	}

	app.participants[name] = time.Now().UTC()
	app.participantCounts[name]++
	if _, ok := app.participantMedia[name]; !ok {
		app.participantMedia[name] = participantMediaState{
			UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
	}

	return name, nil
}

func (app *kanbanBoardApp) forgetParticipant(name string) {
	name = canonicalParticipantName(name)
	if name == "" {
		return
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	if app.participantCounts[name] <= 1 {
		delete(app.participantCounts, name)
		delete(app.participants, name)
		delete(app.participantMedia, name)
		return
	}
	app.participantCounts[name]--
	app.participants[name] = time.Now().UTC()
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
			active += count
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
		ID:     card.ID,
		Status: card.Status,
		Title:  card.Title,
		Notes:  card.Notes,
		Owner:  card.Owner,
		Tags:   append([]string(nil), card.Tags...),
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
