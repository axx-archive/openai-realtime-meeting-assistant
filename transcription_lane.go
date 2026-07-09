package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	realtimeWebSocketURL = "wss://api.openai.com/v1/realtime"
	// A4/E2: the authoritative persisted transcript rides this lane, so its model
	// must accept the domain-vocabulary prompt. gpt-4o-transcribe demonstrably
	// honours a free-text transcription prompt (the Scout realtime peer already
	// uses it that way); the prior gpt-realtime-whisper default carried no
	// vocabulary bias, which is how proper nouns like "Ball Dogs" got mangled.
	defaultTranscriptionLaneModel      = "gpt-4o-transcribe"
	transcriptionLaneInputSampleRate   = 24000
	transcriptionLaneQueueSize         = 256
	transcriptionLaneWriteTimeout      = 5 * time.Second
	transcriptionLaneCommitSilence     = 800 * time.Millisecond
	transcriptionLanePCMBytesPerSample = 2
	transcriptionLaneMinCommitSamples  = transcriptionLaneInputSampleRate / 10
	transcriptionLaneReconnectInitial  = 1 * time.Second
	transcriptionLaneReconnectMax      = 30 * time.Second
	transcriptionLaneSessionRefresh    = 55 * time.Minute
)

var (
	errTranscriptionLaneSessionExpired = errors.New("transcription session expired")
	errTranscriptionLaneSessionRefresh = errors.New("transcription session refresh")
)

type meetingTranscriptionLane struct {
	app                *kanbanBoardApp
	apiKey             string
	transcriptionModel string

	input     chan []int16
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	mu                   sync.Mutex
	connected            bool
	forwardedAudioNotice bool
	droppedAudioNotice   bool
}

func (app *kanbanBoardApp) startTranscriptionLane(apiKey string) {
	if app == nil || strings.TrimSpace(apiKey) == "" || !transcriptionLaneEnabled() {
		return
	}

	lane := newMeetingTranscriptionLane(app, apiKey, transcriptionLaneModel())

	app.mu.Lock()
	oldLane := app.transcriptLane
	app.transcriptLane = lane
	app.mu.Unlock()

	if oldLane != nil {
		oldLane.close()
	}

	app.ensureRoomMixerSink()
	lane.start()
}

func newMeetingTranscriptionLane(app *kanbanBoardApp, apiKey string, transcriptionModel string) *meetingTranscriptionLane {
	return &meetingTranscriptionLane{
		app:                app,
		apiKey:             strings.TrimSpace(apiKey),
		transcriptionModel: strings.TrimSpace(transcriptionModel),
		input:              make(chan []int16, transcriptionLaneQueueSize),
		stop:               make(chan struct{}),
		done:               make(chan struct{}),
	}
}

func (lane *meetingTranscriptionLane) start() {
	go lane.run()
}

func (lane *meetingTranscriptionLane) close() {
	if lane == nil {
		return
	}

	lane.closeOnce.Do(func() {
		close(lane.stop)
		<-lane.done
	})
}

func (lane *meetingTranscriptionLane) enqueue(roomPCM []int16) bool {
	if lane == nil || len(roomPCM) == 0 {
		return false
	}

	copied := append([]int16(nil), roomPCM...)
	select {
	case lane.input <- copied:
		lane.noteForwardedAudio()
		return true
	default:
		lane.noteDroppedAudio()
		return false
	}
}

func (lane *meetingTranscriptionLane) isConnected() bool {
	if lane == nil {
		return false
	}

	lane.mu.Lock()
	defer lane.mu.Unlock()
	return lane.connected
}

func (lane *meetingTranscriptionLane) setConnected(connected bool) {
	if lane == nil {
		return
	}

	lane.mu.Lock()
	changed := lane.connected != connected
	lane.connected = connected
	lane.mu.Unlock()

	if !changed {
		return
	}
	if connected {
		broadcastAssistantEvent("status", "Transcript lane connected", map[string]any{"model": lane.transcriptionModel})
		return
	}
	broadcastAssistantEvent("status", "Transcript lane disconnected", map[string]any{"model": lane.transcriptionModel})
}

func (lane *meetingTranscriptionLane) noteForwardedAudio() {
	lane.mu.Lock()
	if lane.forwardedAudioNotice {
		lane.mu.Unlock()
		return
	}
	lane.forwardedAudioNotice = true
	lane.mu.Unlock()

	broadcastAssistantEvent("audio", "mixed room audio is reaching the transcript lane", nil)
}

func (lane *meetingTranscriptionLane) noteDroppedAudio() {
	lane.mu.Lock()
	if lane.droppedAudioNotice {
		lane.mu.Unlock()
		return
	}
	lane.droppedAudioNotice = true
	lane.mu.Unlock()

	log.Warnf("Dropping mixed audio for transcript lane because its queue is full")
}

func (lane *meetingTranscriptionLane) run() {
	defer close(lane.done)

	backoff := transcriptionLaneReconnectInitial
	for {
		select {
		case <-lane.stop:
			lane.setConnected(false)
			return
		default:
		}

		err := lane.runOnce()
		if err != nil && !lane.stopping() {
			if errors.Is(err, errTranscriptionLaneSessionRefresh) {
				log.Infof("Transcript lane refreshing before session expiration")
				broadcastAssistantEvent("status", "Transcript lane refreshing", map[string]any{"reason": "session refresh"})
				backoff = transcriptionLaneReconnectInitial
			} else if errors.Is(err, errTranscriptionLaneSessionExpired) {
				log.Warnf("Transcript lane session expired; reconnecting")
				broadcastAssistantEvent("status", "Transcript lane reconnecting", map[string]any{"reason": "session expired"})
				backoff = transcriptionLaneReconnectInitial
			} else {
				log.Errorf("Transcript lane failed: %v", err)
				broadcastAssistantEvent("status", "Transcript lane reconnecting", map[string]any{"error": err.Error()})
			}
		} else if err == nil {
			backoff = transcriptionLaneReconnectInitial
		}
		lane.setConnected(false)

		select {
		case <-lane.stop:
			return
		case <-time.After(backoff):
		}
		if err != nil && !errors.Is(err, errTranscriptionLaneSessionRefresh) && !errors.Is(err, errTranscriptionLaneSessionExpired) && backoff < transcriptionLaneReconnectMax {
			backoff *= 2
			if backoff > transcriptionLaneReconnectMax {
				backoff = transcriptionLaneReconnectMax
			}
		}
	}
}

func (lane *meetingTranscriptionLane) runOnce() error {
	conn, _, err := websocket.DefaultDialer.Dial(transcriptionLaneWebSocketURL(), http.Header{
		"Authorization": []string{"Bearer " + lane.apiKey},
	})
	if err != nil {
		return fmt.Errorf("connect OpenAI transcription websocket: %w", err)
	}
	defer conn.Close()

	if err := lane.writeJSON(conn, transcriptionLaneSessionConfig(lane.transcriptionModel)); err != nil {
		return fmt.Errorf("configure transcription session: %w", err)
	}
	// A6: clear any window orphaned by the previous connection (a commit whose
	// .completed never arrived before the socket dropped) so it cannot drift this
	// connection's attribution FIFO.
	lane.app.resetPendingAttributionWindows()
	lane.setConnected(true)

	readErr := make(chan error, 1)
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				readErr <- err
				return
			}
			if lane.app.handleTranscriptionLaneEvent(raw) {
				readErr <- errTranscriptionLaneSessionExpired
				return
			}
		}
	}()

	commitTimer := time.NewTimer(time.Hour)
	stopTranscriptionTimer(commitTimer)
	defer commitTimer.Stop()
	refreshTimer := time.NewTimer(transcriptionLaneSessionRefresh)
	defer refreshTimer.Stop()
	pendingAudio := false
	pendingAudioSamples := 0
	// A6: the stable active speaker at the moment this segment opened. A change
	// mid-segment commits the pending audio early so an interjection lands as its
	// own attributed turn instead of folding under the opening speaker.
	segmentSpeaker := ""

	for {
		select {
		case <-lane.stop:
			if pendingAudio {
				lane.app.noteRealtimeSpeechStopped()
				lane.app.freezeAttributionWindowAtCommit()
				_ = lane.commitPendingTranscriptionAudio(conn, pendingAudioSamples)
			}
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
			return nil
		case err := <-readErr:
			if lane.stopping() || websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			return fmt.Errorf("read transcription websocket: %w", err)
		case <-refreshTimer.C:
			if pendingAudio {
				refreshTimer.Reset(5 * time.Second)
				continue
			}
			return errTranscriptionLaneSessionRefresh
		case roomPCM := <-lane.input:
			audio := roomPCMForTranscription(roomPCM)
			if len(audio) == 0 {
				continue
			}
			// A6: split on speaker change before appending the new speaker's audio,
			// but only once the pending segment holds enough to commit on its own
			// (guards against fragmenting a single word on a flicker).
			if pendingAudio && pendingAudioSamples >= transcriptionLaneMinCommitSamples {
				if speaker := lane.app.activeSpeakerNameForSegmentation(); speaker != "" && segmentSpeaker != "" && speaker != segmentSpeaker {
					stopTranscriptionTimer(commitTimer)
					samples := pendingAudioSamples
					pendingAudio = false
					pendingAudioSamples = 0
					lane.app.noteRealtimeSpeechStopped()
					lane.app.freezeAttributionWindowAtCommit()
					if err := lane.commitPendingTranscriptionAudio(conn, samples); err != nil {
						return err
					}
				}
			}
			if !pendingAudio {
				pendingAudio = true
				pendingAudioSamples = 0
				segmentSpeaker = lane.app.activeSpeakerNameForSegmentation()
				lane.app.noteRealtimeSpeechStarted()
			}
			if err := lane.writeJSON(conn, map[string]any{
				"type":  "input_audio_buffer.append",
				"audio": base64.StdEncoding.EncodeToString(audio),
			}); err != nil {
				return fmt.Errorf("write transcription audio: %w", err)
			}
			pendingAudioSamples += transcriptionLaneAudioSamples(audio)
			resetTranscriptionTimer(commitTimer)
		case <-commitTimer.C:
			if !pendingAudio {
				continue
			}
			pendingAudio = false
			samples := pendingAudioSamples
			pendingAudioSamples = 0
			lane.app.noteRealtimeSpeechStopped()
			lane.app.freezeAttributionWindowAtCommit()
			if err := lane.commitPendingTranscriptionAudio(conn, samples); err != nil {
				return err
			}
		}
	}
}

func (lane *meetingTranscriptionLane) commitPendingTranscriptionAudio(conn *websocket.Conn, pendingSamples int) error {
	if paddingSamples := transcriptionLaneCommitPaddingSamples(pendingSamples); paddingSamples > 0 {
		if err := lane.writeJSON(conn, map[string]any{
			"type":  "input_audio_buffer.append",
			"audio": base64.StdEncoding.EncodeToString(make([]byte, paddingSamples*transcriptionLanePCMBytesPerSample)),
		}); err != nil {
			return fmt.Errorf("pad transcription audio: %w", err)
		}
	}

	if err := lane.writeJSON(conn, map[string]any{"type": "input_audio_buffer.commit"}); err != nil {
		return fmt.Errorf("commit transcription audio: %w", err)
	}
	return nil
}

func (lane *meetingTranscriptionLane) writeJSON(conn *websocket.Conn, payload map[string]any) error {
	if err := conn.SetWriteDeadline(time.Now().Add(transcriptionLaneWriteTimeout)); err != nil {
		return fmt.Errorf("set transcription websocket deadline: %w", err)
	}

	return conn.WriteJSON(payload)
}

func resetTranscriptionTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	stopTranscriptionTimer(timer)
	timer.Reset(transcriptionLaneCommitSilence)
}

func stopTranscriptionTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func transcriptionLaneAudioSamples(audio []byte) int {
	return len(audio) / transcriptionLanePCMBytesPerSample
}

func transcriptionLaneCommitPaddingSamples(pendingSamples int) int {
	if pendingSamples >= transcriptionLaneMinCommitSamples {
		return 0
	}
	return transcriptionLaneMinCommitSamples - pendingSamples
}

func (lane *meetingTranscriptionLane) stopping() bool {
	if lane == nil {
		return true
	}

	select {
	case <-lane.stop:
		return true
	default:
		return false
	}
}

func (app *kanbanBoardApp) ensureRoomMixerSink() {
	if roomMixer != nil {
		roomMixer.setSink(realtimeMixedAudioSinkKey, app)
	}
}

func (app *kanbanBoardApp) removeRoomMixerSinkIfIdle() {
	if roomMixer == nil {
		return
	}

	app.mu.Lock()
	hasTranscriptLane := app.transcriptLane != nil
	hasRealtimeInput := app.inputTrack != nil && app.inputEnc != nil
	app.mu.Unlock()

	if !hasTranscriptLane && !hasRealtimeInput {
		roomMixer.removeSink(realtimeMixedAudioSinkKey)
	}
}

func (app *kanbanBoardApp) enqueueTranscriptionLaneAudio(roomPCM []int16) bool {
	app.mu.Lock()
	lane := app.transcriptLane
	app.mu.Unlock()

	return lane != nil && lane.enqueue(roomPCM)
}

func (app *kanbanBoardApp) transcriptionLaneConnected() bool {
	app.mu.Lock()
	lane := app.transcriptLane
	app.mu.Unlock()

	return lane != nil && lane.isConnected()
}

func (app *kanbanBoardApp) currentTranscriptionLaneModel() string {
	app.mu.Lock()
	lane := app.transcriptLane
	app.mu.Unlock()
	if lane == nil {
		return transcriptionLaneModel()
	}

	return lane.transcriptionModel
}

func (app *kanbanBoardApp) currentRealtimeModel() string {
	app.mu.Lock()
	defer app.mu.Unlock()
	return app.model
}

func (app *kanbanBoardApp) handleTranscriptionLaneEvent(raw []byte) bool {
	var event kanbanRealtimeEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		log.Errorf("Failed to parse OpenAI transcription event: %v", err)
		return false
	}

	switch event.Type {
	case "session.created", "session.updated":
		broadcastAssistantEvent("status", "OpenAI transcription session configured", map[string]any{"eventType": event.Type})
	case "error":
		if event.Error != nil {
			if event.Error.Code == "session_expired" {
				log.Warnf("OpenAI transcription session expired: %s", event.Error.Message)
				broadcastAssistantEvent("status", "Transcript lane session expired; reconnecting", map[string]any{"code": event.Error.Code, "lane": "transcript"})
				return true
			}
			log.Errorf("OpenAI transcription error code=%s message=%s", event.Error.Code, event.Error.Message)
			// Keep raw server errors off the chat feed (only query/answer/error
			// kinds render there); raw message stays in metadata + server logs.
			broadcastAssistantEvent("status", "transcript lane hit a server error", map[string]any{"code": event.Error.Code, "message": event.Error.Message, "lane": "transcript"})
		}
	case "conversation.item.input_audio_transcription.completed":
		app.rememberTranscript(event, "transcript_lane", app.currentTranscriptionLaneModel())
	case "conversation.item.input_audio_transcription.failed":
		// A6: a failed segment yields no transcript to persist, but it still had a
		// window frozen at its commit. Pop it (discard) so the FIFO stays aligned;
		// otherwise the next .completed inherits this dead turn's boundaries and every
		// later transcript is attributed one turn late for the rest of the sitting.
		app.popPendingAttributionWindow()
	case "input_audio_buffer.speech_started":
		app.noteRealtimeSpeechStarted()
		app.clearScoutVoiceArmForNewSpeech()
		broadcastAssistantEvent("audio", "transcript lane detected speech", map[string]any{"eventType": event.Type})
	case "input_audio_buffer.speech_stopped":
		app.noteRealtimeSpeechStopped()
		broadcastAssistantEvent("audio", "transcript lane detected silence", map[string]any{"eventType": event.Type})
	}

	return false
}

func transcriptionLaneEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MEETING_TRANSCRIPT_LANE_ENABLED"))) {
	case "", "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func transcriptionLaneModel() string {
	if model := strings.TrimSpace(os.Getenv("OPENAI_TRANSCRIPT_MODEL")); model != "" {
		return model
	}

	return defaultTranscriptionLaneModel
}

func transcriptionLaneWebSocketURL() string {
	values := url.Values{}
	values.Set("intent", "transcription")
	return realtimeWebSocketURL + "?" + values.Encode()
}

// transcriptionModelAcceptsPrompt reports whether the realtime transcription
// session config may carry a free-text `prompt` for this model. The gpt-4o
// transcription family accepts it (A4 domain-vocabulary biasing); the realtime
// whisper model does NOT — sending `prompt` there is rejected live with
// "The 'prompt' parameter is not supported for this model" and would break the
// session. So the prompt/near-field config is gated by model rather than sent
// unconditionally (prod pins OPENAI_TRANSCRIPT_MODEL=gpt-realtime-whisper).
func transcriptionModelAcceptsPrompt(model string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(model)), "gpt-4o")
}

func transcriptionLaneSessionConfig(model string) map[string]any {
	model = strings.TrimSpace(model)
	transcription := map[string]any{
		"model":    model,
		"language": "en",
	}
	input := map[string]any{
		"format": map[string]any{
			"type": "audio/pcm",
			"rate": transcriptionLaneInputSampleRate,
		},
		"transcription":  transcription,
		"turn_detection": nil,
	}
	// A4: bias the authoritative persisted stream with the same near-field noise
	// reduction + domain-vocabulary prompt the Scout realtime peer uses — but
	// ONLY for models that accept it. Whisper rejects both fields, so it keeps
	// the plain config it always had (domain-vocab requires switching
	// OPENAI_TRANSCRIPT_MODEL to gpt-4o-transcribe).
	if transcriptionModelAcceptsPrompt(model) {
		input["noise_reduction"] = map[string]any{"type": "near_field"}
		transcription["prompt"] = realtimeTranscriptionPrompt()
	}
	return map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"type":  "transcription",
			"audio": map[string]any{"input": input},
		},
	}
}

func roomPCMForTranscription(roomPCM []int16) []byte {
	if len(roomPCM) < 2 {
		return nil
	}

	out := make([]byte, (len(roomPCM)/2)*2)
	for i, j := 0, 0; i+1 < len(roomPCM); i, j = i+2, j+2 {
		sample := int16((int32(roomPCM[i]) + int32(roomPCM[i+1])) / 2)
		binary.LittleEndian.PutUint16(out[j:j+2], uint16(sample))
	}

	return out
}
