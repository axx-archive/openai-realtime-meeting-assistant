package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
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
	// roomID scopes everything the lane commits — transcripts, attribution
	// freezes/pops, segmentation lookups — to ONE room (multi-room W3). The
	// boot-started office lane carries officeRoomID; named rooms get a lane
	// per sitting from ensureRoomMedia.
	roomID          string
	sittingID       string
	mediaGeneration uint64

	input        chan []int16
	consentInput chan consentAudioFrame
	withdrawals  chan ConsentWithdrawalNotice
	stop         chan struct{}
	done         chan struct{}
	closeOnce    sync.Once

	mu                    sync.Mutex
	connected             bool
	forwardedAudioNotice  bool
	droppedAudioNotice    bool
	unsubscribeWithdrawal func()
}

func (lane *meetingTranscriptionLane) scope() RoomScoutScope {
	if lane == nil {
		return RoomScoutScope{}
	}
	return RoomScoutScope{RoomID: lane.roomID, SittingID: lane.sittingID, MediaGeneration: lane.mediaGeneration}
}

type consentAudioFrame struct {
	pcm    []int16
	fences []ConsentFence
}

func (app *kanbanBoardApp) startTranscriptionLane(apiKey string, mediaGeneration uint64, startToken uint64) {
	if app == nil || strings.TrimSpace(apiKey) == "" || !transcriptionLaneEnabled() {
		return
	}

	lane := newMeetingTranscriptionLaneForRoomGeneration(app, apiKey, transcriptionLaneModel(), officeRoomID, mediaGeneration)
	if officeTranscriptionBeforePublishProbe != nil {
		officeTranscriptionBeforePublishProbe()
	}

	app.mu.Lock()
	state := app.roomLiveLocked(officeRoomID)
	if state.mediaGen != mediaGeneration || state.mediaActor == nil || !app.transcriptionStarting || app.transcriptionStartToken != startToken {
		app.mu.Unlock()
		return
	}
	oldLane := app.transcriptLane
	app.transcriptLane = lane
	app.mu.Unlock()

	if oldLane != nil {
		oldLane.close()
	}

	app.ensureRoomMixerSink()
	lane.start()
}

var officeTranscriptionBeforePublishProbe func()

func newMeetingTranscriptionLane(app *kanbanBoardApp, apiKey string, transcriptionModel string) *meetingTranscriptionLane {
	return newMeetingTranscriptionLaneForRoom(app, apiKey, transcriptionModel, officeRoomID)
}

func newMeetingTranscriptionLaneForRoom(app *kanbanBoardApp, apiKey string, transcriptionModel string, roomID string) *meetingTranscriptionLane {
	return newMeetingTranscriptionLaneForRoomGeneration(app, apiKey, transcriptionModel, roomID, 0)
}

func newMeetingTranscriptionLaneForRoomGeneration(app *kanbanBoardApp, apiKey string, transcriptionModel string, roomID string, mediaGeneration uint64) *meetingTranscriptionLane {
	sittingID := ""
	if app != nil && app.memory != nil {
		sittingID = app.memory.currentMeetingID(roomID)
	}
	return &meetingTranscriptionLane{
		app:                app,
		apiKey:             strings.TrimSpace(apiKey),
		transcriptionModel: strings.TrimSpace(transcriptionModel),
		roomID:             normalizeRoomID(roomID),
		sittingID:          sittingID,
		mediaGeneration:    mediaGeneration,
		input:              make(chan []int16, transcriptionLaneQueueSize),
		consentInput:       make(chan consentAudioFrame, transcriptionLaneQueueSize),
		withdrawals:        make(chan ConsentWithdrawalNotice, 8),
		stop:               make(chan struct{}),
		done:               make(chan struct{}),
	}
}

func (lane *meetingTranscriptionLane) start() {
	lane.unsubscribeWithdrawal = subscribeConsentWithdrawals(lane.noteWithdrawal)
	go lane.run()
}

func (lane *meetingTranscriptionLane) close() {
	if lane == nil {
		return
	}

	lane.closeOnce.Do(func() {
		close(lane.stop)
		<-lane.done
		if lane.unsubscribeWithdrawal != nil {
			lane.unsubscribeWithdrawal()
		}
	})
}

func (lane *meetingTranscriptionLane) enqueueWithConsent(roomPCM []int16, fences []ConsentFence) bool {
	if lane == nil || len(roomPCM) == 0 || len(fences) == 0 {
		return false
	}
	frame := consentAudioFrame{pcm: append([]int16(nil), roomPCM...), fences: append([]ConsentFence(nil), fences...)}
	select {
	case lane.consentInput <- frame:
		lane.noteForwardedAudio()
		return true
	default:
		lane.noteDroppedAudio()
		return false
	}
}

func (lane *meetingTranscriptionLane) noteWithdrawal(notice ConsentWithdrawalNotice) {
	if lane == nil || normalizeRoomID(notice.Binding.RoomID) != normalizeRoomID(lane.roomID) {
		return
	}
	if notice.Scope != ConsentAudioCapture && notice.Scope != ConsentTranscription {
		return
	}
	select {
	case <-lane.stop:
		return
	case lane.withdrawals <- notice:
	}
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
	lane.app.resetPendingAttributionWindowsForScope(lane.scope())
	// W0-5: same discipline for the metering FIFO — a committed duration whose
	// terminal event never arrived must not stamp itself onto this session's
	// first segment.
	lane.app.resetTranscriptionSegmentSecondsForLaneScope(lane.scope())
	lane.setConnected(true)

	readErr := make(chan error, 1)
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				readErr <- err
				return
			}
			if lane.app.handleTranscriptionLaneEventForScope(lane.scope(), raw, lane.transcriptionModel) {
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
	pendingFences := map[string]ConsentFence{}
	var segmentCapture *transcriptCaptureStamp
	// A6: the stable active speaker at the moment this segment opened. A change
	// mid-segment commits the pending audio early so an interjection lands as its
	// own attributed turn instead of folding under the opening speaker.
	segmentSpeaker := ""

	clearPending := func(clearProvider bool) {
		if clearProvider {
			_ = lane.writeJSON(conn, map[string]any{"type": "input_audio_buffer.clear"})
		}
		pendingAudio = false
		pendingAudioSamples = 0
		pendingFences = map[string]ConsentFence{}
		segmentCapture = nil
		segmentSpeaker = ""
		stopTranscriptionTimer(commitTimer)
		lane.app.discardRealtimeSpeechForScope(lane.scope())
	}
	commitPending := func() error {
		if !pendingAudio {
			return nil
		}
		for _, fence := range pendingFences {
			if err := currentConsentLaneAuthority().ValidateFence(context.Background(), fence); err != nil {
				clearPending(true)
				return nil
			}
		}
		samples := pendingAudioSamples
		capture := segmentCapture
		contributorFences := make([]ConsentFence, 0, len(pendingFences))
		for _, fence := range pendingFences {
			contributorFences = append(contributorFences, fence)
		}
		sort.Slice(contributorFences, func(i, j int) bool {
			return consentBindingKey(contributorFences[i].binding) < consentBindingKey(contributorFences[j].binding)
		})
		if capture == nil {
			clearPending(true)
			return nil
		}
		capture.OccurredEnd = time.Now().UTC()
		pendingAudio = false
		pendingAudioSamples = 0
		pendingFences = map[string]ConsentFence{}
		segmentCapture = nil
		scope := RoomScoutScope{RoomID: lane.roomID, SittingID: lane.sittingID, MediaGeneration: lane.mediaGeneration}
		lane.app.noteRealtimeSpeechStoppedForScope(scope)
		lane.app.freezeAttributionWindowAtCommitForScopeWithCaptureAndConsent(scope, capture, contributorFences)
		return lane.commitPendingTranscriptionAudio(conn, samples)
	}
	acceptFrame := func(frame consentAudioFrame) error {
		if len(frame.fences) == 0 {
			return nil
		}
		authority := currentConsentLaneAuthority()
		for _, fence := range frame.fences {
			if fence.lane != ConsentLaneTranscription || authority.ValidateIngressFence(fence) != nil {
				return nil
			}
		}
		// A refreshed fence for the same contributor may reflect a remote
		// withdrawal/re-grant that did not originate in this process. Never let
		// the newer record digest overwrite and thereby launder audio already
		// buffered under the older authority; clear the old segment first.
		if pendingAudio {
			for _, fence := range frame.fences {
				if prior, ok := pendingFences[consentBindingKey(fence.binding)]; ok &&
					(prior.generation != fence.generation || prior.recordDigest != fence.recordDigest || prior.policy != fence.policy) {
					clearPending(true)
					break
				}
			}
		}
		audio := roomPCMForTranscription(frame.pcm)
		if len(audio) == 0 {
			return nil
		}
		if pendingAudio && pendingAudioSamples >= transcriptionLaneMinCommitSamples {
			if speaker := lane.app.activeSpeakerNameForSegmentationForRoom(lane.roomID); speaker != "" && segmentSpeaker != "" && speaker != segmentSpeaker {
				stopTranscriptionTimer(commitTimer)
				if err := commitPending(); err != nil {
					return err
				}
			}
		}
		if !pendingAudio {
			if lane.app == nil || lane.app.memory == nil {
				return nil
			}
			capture, err := lane.app.memory.reserveTranscriptCapture(time.Now().UTC())
			if err != nil {
				return nil
			}
			pendingAudio = true
			segmentCapture = capture
			segmentSpeaker = lane.app.activeSpeakerNameForSegmentationForRoom(lane.roomID)
			lane.app.noteRealtimeSpeechStartedForScope(RoomScoutScope{RoomID: lane.roomID, SittingID: lane.sittingID, MediaGeneration: lane.mediaGeneration})
		}
		for _, fence := range frame.fences {
			pendingFences[consentBindingKey(fence.binding)] = fence
		}
		if err := lane.writeJSON(conn, map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(audio)}); err != nil {
			return fmt.Errorf("write transcription audio: %w", err)
		}
		pendingAudioSamples += transcriptionLaneAudioSamples(audio)
		resetTranscriptionTimer(commitTimer)
		return nil
	}

	for {
		select {
		case <-lane.stop:
			if pendingAudio {
				_ = commitPending()
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
		case <-lane.input:
			// Legacy/plain PCM is intentionally not authority. The channel remains
			// for compatibility probes, but production provider ingress requires
			// enqueueWithConsent.
		case frame := <-lane.consentInput:
			if err := acceptFrame(frame); err != nil {
				return err
			}
		case notice := <-lane.withdrawals:
			matches := false
			for _, fence := range pendingFences {
				if consentBindingKey(fence.binding) == consentBindingKey(notice.Binding) {
					matches = true
					break
				}
			}
			if matches {
				clearPending(true)
			}
			// Every queued frame is uncommitted room work. Dropping the bounded
			// queue is conservative and prevents a withdrawn contributor's mixed
			// samples from surviving behind other participants' audio.
			for len(lane.consentInput) > 0 {
				<-lane.consentInput
			}
		case <-commitTimer.C:
			if !pendingAudio {
				continue
			}
			if err := commitPending(); err != nil {
				return err
			}
		}
	}
}

func (lane *meetingTranscriptionLane) commitPendingTranscriptionAudio(conn *websocket.Conn, pendingSamples int) error {
	paddingSamples := transcriptionLaneCommitPaddingSamples(pendingSamples)
	if paddingSamples > 0 {
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
	// W0-5: the commit is the billing moment on this duration-billed lane —
	// meter it here (padding included: the API transcribes those samples too),
	// whether or not the transcription later completes.
	lane.noteCommittedSegment(pendingSamples + paddingSamples)
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

// ---------------------------------------------------------------------------
// W0-5 lane metering (seat transcription_lane): the lane is duration-billed
// (gpt-4o-transcribe / gpt-realtime-whisper price per audio minute), so every
// committed segment records its AudioSeconds at commit time — the audio is
// billed whether or not the transcription later succeeds. Committed durations
// also queue in a small per-room FIFO so the .completed/.failed terminal event
// (which arrives in commit order on the same socket) can stamp audio_seconds
// onto its transcript_segment eval event. A reconnect resets the room's queue
// alongside the attribution windows.
// ---------------------------------------------------------------------------

// transcriptionSegmentSecondsCap bounds each room's pending queue: if terminal
// events ever stop arriving, the oldest committed durations fall off instead of
// growing without bound (usage rows are unaffected — written at commit time).
const transcriptionSegmentSecondsCap = 64

var (
	transcriptionSegmentSecondsMu sync.Mutex
	transcriptionSegmentSeconds   = map[string][]float64{}
)

func pushTranscriptionSegmentSeconds(roomID string, seconds float64) {
	roomID = normalizeRoomID(roomID)
	transcriptionSegmentSecondsMu.Lock()
	defer transcriptionSegmentSecondsMu.Unlock()
	queue := append(transcriptionSegmentSeconds[roomID], seconds)
	if len(queue) > transcriptionSegmentSecondsCap {
		queue = queue[len(queue)-transcriptionSegmentSecondsCap:]
	}
	transcriptionSegmentSeconds[roomID] = queue
}

func transcriptionScopeMeterKey(scope RoomScoutScope) string {
	return normalizeRoomID(scope.RoomID) + "\x00" + strings.TrimSpace(scope.SittingID) + "\x00" + strconv.FormatUint(scope.MediaGeneration, 10)
}

func pushTranscriptionSegmentSecondsForScope(scope RoomScoutScope, seconds float64) {
	transcriptionSegmentSecondsMu.Lock()
	defer transcriptionSegmentSecondsMu.Unlock()
	key := transcriptionScopeMeterKey(scope)
	queue := append(transcriptionSegmentSeconds[key], seconds)
	if len(queue) > transcriptionSegmentSecondsCap {
		queue = queue[len(queue)-transcriptionSegmentSecondsCap:]
	}
	transcriptionSegmentSeconds[key] = queue
}

// popTranscriptionSegmentSeconds returns the oldest committed segment duration
// for the room, or 0 when nothing is queued (a terminal event for a segment
// committed before the last reconnect).
func popTranscriptionSegmentSeconds(roomID string) float64 {
	roomID = normalizeRoomID(roomID)
	transcriptionSegmentSecondsMu.Lock()
	defer transcriptionSegmentSecondsMu.Unlock()
	queue := transcriptionSegmentSeconds[roomID]
	if len(queue) == 0 {
		return 0
	}
	seconds := queue[0]
	if len(queue) == 1 {
		delete(transcriptionSegmentSeconds, roomID)
	} else {
		transcriptionSegmentSeconds[roomID] = queue[1:]
	}
	return seconds
}

func popTranscriptionSegmentSecondsForScope(scope RoomScoutScope) float64 {
	transcriptionSegmentSecondsMu.Lock()
	defer transcriptionSegmentSecondsMu.Unlock()
	key := transcriptionScopeMeterKey(scope)
	queue := transcriptionSegmentSeconds[key]
	if len(queue) == 0 {
		return 0
	}
	seconds := queue[0]
	if len(queue) == 1 {
		delete(transcriptionSegmentSeconds, key)
	} else {
		transcriptionSegmentSeconds[key] = queue[1:]
	}
	return seconds
}

func resetTranscriptionSegmentSecondsForRoom(roomID string) {
	roomID = normalizeRoomID(roomID)
	transcriptionSegmentSecondsMu.Lock()
	delete(transcriptionSegmentSeconds, roomID)
	transcriptionSegmentSecondsMu.Unlock()
}

func resetTranscriptionSegmentSecondsForScope(scope RoomScoutScope) {
	transcriptionSegmentSecondsMu.Lock()
	delete(transcriptionSegmentSeconds, transcriptionScopeMeterKey(scope))
	transcriptionSegmentSecondsMu.Unlock()
}

// The scoped meter helpers linearize the current-owner check with the FIFO
// mutation under app.mu. A teardown therefore cannot advance the sitting
// between validation and push/pop/reset and let stale lane work touch its
// successor's accounting queue.
func (app *kanbanBoardApp) pushTranscriptionSegmentSecondsForLaneScope(scope RoomScoutScope, seconds float64) {
	if app == nil {
		return
	}
	if scope.MediaGeneration == 0 {
		pushTranscriptionSegmentSeconds(scope.RoomID, seconds)
		return
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(scope.RoomID)
	if state.mediaGen == scope.MediaGeneration && state.mediaActor != nil && state.mediaSittingID == strings.TrimSpace(scope.SittingID) {
		pushTranscriptionSegmentSecondsForScope(scope, seconds)
	}
}

func (app *kanbanBoardApp) popTranscriptionSegmentSecondsForLaneScope(scope RoomScoutScope) float64 {
	if app == nil {
		return 0
	}
	if scope.MediaGeneration == 0 {
		return popTranscriptionSegmentSeconds(scope.RoomID)
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(scope.RoomID)
	if state.mediaGen != scope.MediaGeneration || state.mediaActor == nil || state.mediaSittingID != strings.TrimSpace(scope.SittingID) {
		return 0
	}
	return popTranscriptionSegmentSecondsForScope(scope)
}

func (app *kanbanBoardApp) resetTranscriptionSegmentSecondsForLaneScope(scope RoomScoutScope) {
	if app == nil {
		return
	}
	if scope.MediaGeneration == 0 {
		resetTranscriptionSegmentSecondsForRoom(scope.RoomID)
		return
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(scope.RoomID)
	if state.mediaGen == scope.MediaGeneration && state.mediaActor != nil && state.mediaSittingID == strings.TrimSpace(scope.SittingID) {
		resetTranscriptionSegmentSecondsForScope(scope)
	}
}

// noteCommittedSegment writes the ledger row for one committed segment and
// queues its duration for the terminal transcript_segment eval event.
func (lane *meetingTranscriptionLane) noteCommittedSegment(committedSamples int) {
	if lane == nil || committedSamples <= 0 {
		return
	}
	seconds := float64(committedSamples) / float64(transcriptionLaneInputSampleRate)
	recordLLMUsage(llmUsageEntry{
		Provider:     providerOpenAI,
		Model:        lane.transcriptionModel,
		Seat:         seatTranscriptionLane,
		RoomID:       lane.roomID,
		AudioSeconds: seconds,
	})
	lane.app.pushTranscriptionSegmentSecondsForLaneScope(lane.scope(), seconds)
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
		authority := currentConsentLaneAuthority()
		roomMixer.setConsentSink(realtimeMixedAudioSinkKey+":transcription:"+officeRoomID, ConsentLaneTranscription, authority, &roomLaneAudioSink{app: app, roomID: officeRoomID, lane: ConsentLaneTranscription})
		roomMixer.setConsentSink(realtimeMixedAudioSinkKey+":model:"+officeRoomID, ConsentLaneModelAnalysis, authority, &roomLaneAudioSink{app: app, roomID: officeRoomID, lane: ConsentLaneModelAnalysis})
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
		roomMixer.removeSink(realtimeMixedAudioSinkKey + ":transcription:" + officeRoomID)
		roomMixer.removeSink(realtimeMixedAudioSinkKey + ":model:" + officeRoomID)
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

func (app *kanbanBoardApp) currentRealtimeMediaGeneration() uint64 {
	app.mu.Lock()
	defer app.mu.Unlock()
	return app.realtimeMediaGen
}

func (app *kanbanBoardApp) handleTranscriptionLaneEvent(raw []byte) bool {
	return app.handleTranscriptionLaneEventForRoom(officeRoomID, raw, app.currentTranscriptionLaneModel())
}

func (app *kanbanBoardApp) handleTranscriptionLaneEventForRoom(roomID string, raw []byte, model string) bool {
	return app.handleTranscriptionLaneEventForRoomGeneration(roomID, 0, raw, model)
}

func (app *kanbanBoardApp) handleTranscriptionLaneEventForRoomGeneration(roomID string, mediaGeneration uint64, raw []byte, model string) bool {
	sittingID := ""
	app.mu.Lock()
	if mediaGeneration > 0 {
		sittingID = app.roomLiveLocked(roomID).mediaSittingID
	}
	app.mu.Unlock()
	return app.handleTranscriptionLaneEventForScope(RoomScoutScope{RoomID: roomID, SittingID: sittingID, MediaGeneration: mediaGeneration}, raw, model)
}

func (app *kanbanBoardApp) handleTranscriptionLaneEventForScope(scope RoomScoutScope, raw []byte, model string) bool {
	var event kanbanRealtimeEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		log.Errorf("Failed to parse OpenAI transcription event: %v", err)
		return false
	}
	roomID, mediaGeneration := normalizeRoomID(scope.RoomID), scope.MediaGeneration
	if mediaGeneration > 0 {
		if strings.TrimSpace(scope.SittingID) != "" {
			if !app.roomMediaScopeCurrent(scope) {
				return false
			}
		} else if !app.roomMediaGenerationCurrent(roomID, mediaGeneration) {
			return false
		}
	}
	if transcriptionEventAfterScopeProbe != nil {
		transcriptionEventAfterScopeProbe()
	}

	switch event.Type {
	case "session.created", "session.updated":
		recordCapabilitySuccess(capabilitySTT, time.Now().UTC())
		broadcastAssistantEvent("status", "OpenAI transcription session configured", map[string]any{"eventType": event.Type})
	case "error":
		if event.Error != nil {
			recordCapabilityFailure(capabilitySTT, time.Now().UTC(), fmt.Errorf("%s", firstNonEmptyString(event.Error.Code, "transcription_error")))
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
		recordCapabilitySuccess(capabilitySTT, time.Now().UTC())
		recordEvalEvent(seatTranscriptionLane, evalKindTranscriptSegment, map[string]any{
			"status":        "completed",
			"room_id":       roomID,
			"audio_seconds": app.popTranscriptionSegmentSecondsForLaneScope(scope),
		})
		if mediaGeneration > 0 {
			app.rememberTranscriptForMediaScope(scope, event, "transcript_lane", model)
		} else {
			app.rememberTranscript(roomID, event, "transcript_lane", model)
		}
	case "conversation.item.input_audio_transcription.failed":
		recordCapabilityFailure(capabilitySTT, time.Now().UTC(), fmt.Errorf("transcription segment failed"))
		// W0-5: a failed segment is speech the brain never heard — this event
		// series is the raw feed for the >2% drop-off alarm.
		recordEvalEvent(seatTranscriptionLane, evalKindTranscriptSegment, map[string]any{
			"status":        "failed",
			"room_id":       roomID,
			"audio_seconds": app.popTranscriptionSegmentSecondsForLaneScope(scope),
		})
		// A6: a failed segment yields no transcript to persist, but it still had a
		// window frozen at its commit. Pop it (discard) so the FIFO stays aligned;
		// otherwise the next .completed inherits this dead turn's boundaries and every
		// later transcript is attributed one turn late for the rest of the sitting.
		if mediaGeneration > 0 {
			app.popPendingAttributionWindowForScope(scope)
		} else {
			app.popPendingAttributionWindowForRoom(roomID)
		}
	case "input_audio_buffer.speech_started":
		if mediaGeneration > 0 {
			app.noteRealtimeSpeechStartedForScope(scope)
		} else {
			app.noteRealtimeSpeechStartedForRoom(roomID)
		}
		if roomID == officeRoomID {
			app.clearScoutVoiceArmForNewSpeech()
		}
		broadcastAssistantEvent("audio", "transcript lane detected speech", map[string]any{"eventType": event.Type})
	case "input_audio_buffer.speech_stopped":
		if mediaGeneration > 0 {
			app.noteRealtimeSpeechStoppedForScope(scope)
		} else {
			app.noteRealtimeSpeechStoppedForRoom(roomID)
		}
		broadcastAssistantEvent("audio", "transcript lane detected silence", map[string]any{"eventType": event.Type})
	}

	return false
}

var transcriptionEventAfterScopeProbe func()

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
