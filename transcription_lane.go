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

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	realtimeWebSocketURL = "wss://api.openai.com/v1/realtime"
	// A4/E2: the authoritative persisted transcript rides this lane, so its model
	// must accept the domain-vocabulary prompt. GPT Transcribe is the accurate
	// final-transcript model for committed Realtime turns and supports the
	// modern prompt, keyword, and plural-language hints.
	defaultTranscriptionLaneModel    = "gpt-transcribe"
	transcriptionLaneInputSampleRate = 24000
	transcriptionLaneQueueSize       = 256
	transcriptionLaneWriteTimeout    = 5 * time.Second
	transcriptionLaneCommitSilence   = 800 * time.Millisecond
	// transcriptionLaneMaxSegment caps a single provider segment. A
	// source-bound lane's only other commit trigger is the 800ms silence timer,
	// re-armed by every accepted frame, so an unbroken monologue used to arrive
	// as one block — 212.6 audio-seconds in production on 2026-09-02, 308.1s on
	// 2026-09-01. A mid-sentence cut costs far less than a five-minute dark
	// stretch, and it also keeps the capture-stall watchdog honest: without a
	// ceiling, a long monologue looks exactly like a stall.
	transcriptionLaneMaxSegment        = 15 * time.Second
	transcriptionLanePCMBytesPerSample = 2
	transcriptionLaneMinCommitSamples  = transcriptionLaneInputSampleRate / 10
	transcriptionLaneReconnectInitial  = 1 * time.Second
	transcriptionLaneReconnectMax      = 30 * time.Second
	transcriptionLaneSessionRefresh    = 55 * time.Minute
)

var (
	errTranscriptionLaneSessionExpired = errors.New("transcription session expired")
	errTranscriptionLaneSessionRefresh = errors.New("transcription session refresh")
	errTranscriptionLaneRepair         = errors.New("transcription lane repair")

	// transcriptionLaneMaxSegmentDuration is the live value of the segment cap.
	// Production never changes it; tests scale it down so a "monologue" is
	// seconds of wall clock instead of a minute. The behaviour under test is
	// the cap firing on continuous audio, which is timescale-independent.
	transcriptionLaneMaxSegmentDuration = transcriptionLaneMaxSegment
)

// meetingMemoryKindTranscriptCoverage is the durable, recall-hidden row that
// records a hole in a meeting's transcript. It is written with
// relevance=expired so it never grounds an answer, but the Meeting Record can
// still read it and refuse to present a partial transcript as complete.
const meetingMemoryKindTranscriptCoverage = "transcript_coverage"

// meetingMemoryKindRecordingAudit is the durable audit row for a recording
// toggle (Fix 5). Before it, recordingEpoch stamped on transcript rows was the
// only durable trace of a toggle: enough to prove two toggles happened during
// the 2026-09-02 blackout, not enough to say when or by whom.
const meetingMemoryKindRecordingAudit = "recording_audit"

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
	segmentBindings *transcriptionSegmentBindings
	// sourceManager owns one provider lane per admitted WebRTC audio
	// publication. Child lanes carry fixedSource, making speaker identity a
	// capture property rather than an inference from the mixed waveform.
	sourceManager          bool
	fixedSource            *sourceTranscriptIdentity
	sourceBindings         *sourceTranscriptBindings
	sourceMu               sync.Mutex
	sourceLanes            map[string]*meetingTranscriptionLane
	sourceRetireTimers     map[string]*time.Timer
	sourceRetireGeneration map[string]uint64
	sourceAcceptedForTurn  bool
	recordingEpoch         uint64
	discardOnClose         bool

	input        chan []int16
	consentInput chan consentAudioFrame
	withdrawals  chan ConsentWithdrawalNotice
	stop         chan struct{}
	done         chan struct{}
	// repair is the capture-stall recovery ladder's provider-reconnect lever.
	// It is deliberately NOT the recording epoch: the epoch is attribution
	// ordering, not a repair tool, and bumping it to fix a stall would fence
	// off audio that is still legitimately in flight.
	repair    chan struct{}
	closeOnce sync.Once

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

type sourceTranscriptIdentity struct {
	TrackKey       string
	Speaker        string
	Fence          ConsentFence
	RecordingEpoch uint64
}

type sourceTranscriptRecord struct {
	sourceTranscriptIdentity
	Capture *transcriptCaptureStamp
}

type sourceTranscriptBindings struct {
	mu      sync.Mutex
	records map[string]sourceTranscriptRecord
}

func newSourceTranscriptBindings() *sourceTranscriptBindings {
	return &sourceTranscriptBindings{records: make(map[string]sourceTranscriptRecord)}
}

func (bindings *sourceTranscriptBindings) Put(segmentID string, record sourceTranscriptRecord) {
	if bindings == nil || strings.TrimSpace(segmentID) == "" {
		return
	}
	bindings.mu.Lock()
	bindings.records[strings.TrimSpace(segmentID)] = record
	bindings.mu.Unlock()
}

func (bindings *sourceTranscriptBindings) Consume(segmentID string) (sourceTranscriptRecord, bool) {
	if bindings == nil {
		return sourceTranscriptRecord{}, false
	}
	segmentID = strings.TrimSpace(segmentID)
	bindings.mu.Lock()
	record, ok := bindings.records[segmentID]
	delete(bindings.records, segmentID)
	bindings.mu.Unlock()
	return record, ok
}

func (bindings *sourceTranscriptBindings) Reset() {
	if bindings == nil {
		return
	}
	bindings.mu.Lock()
	bindings.records = make(map[string]sourceTranscriptRecord)
	bindings.mu.Unlock()
}

func (bindings *sourceTranscriptBindings) Len() int {
	if bindings == nil {
		return 0
	}
	bindings.mu.Lock()
	defer bindings.mu.Unlock()
	return len(bindings.records)
}

func (app *kanbanBoardApp) startTranscriptionLane(apiKey string, mediaGeneration uint64, startToken uint64) {
	if app == nil || strings.TrimSpace(apiKey) == "" || !transcriptionLaneEnabled() {
		return
	}

	lane := newMeetingTranscriptionSourceManagerForRoomGeneration(app, apiKey, transcriptionLaneModel(), officeRoomID, mediaGeneration)
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
		segmentBindings:    newTranscriptionSegmentBindings(),
		input:              make(chan []int16, transcriptionLaneQueueSize),
		consentInput:       make(chan consentAudioFrame, transcriptionLaneQueueSize),
		withdrawals:        make(chan ConsentWithdrawalNotice, 8),
		stop:               make(chan struct{}),
		done:               make(chan struct{}),
		repair:             make(chan struct{}, 1),
	}
}

func newMeetingTranscriptionSourceManagerForRoomGeneration(app *kanbanBoardApp, apiKey string, transcriptionModel string, roomID string, mediaGeneration uint64) *meetingTranscriptionLane {
	lane := newMeetingTranscriptionLaneForRoomGeneration(app, apiKey, transcriptionModel, roomID, mediaGeneration)
	lane.sourceManager = true
	lane.sourceLanes = make(map[string]*meetingTranscriptionLane)
	lane.sourceRetireTimers = make(map[string]*time.Timer)
	lane.sourceRetireGeneration = make(map[string]uint64)
	return lane
}

func (lane *meetingTranscriptionLane) start() {
	if lane.sourceManager {
		go func() {
			<-lane.stop
			close(lane.done)
		}()
		return
	}
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
		if lane.sourceManager {
			lane.closeSourceLanes(true)
		}
		if lane.unsubscribeWithdrawal != nil {
			lane.unsubscribeWithdrawal()
		}
	})
}

func (lane *meetingTranscriptionLane) closeSourceLanes(commitPending bool) {
	if lane == nil || !lane.sourceManager {
		return
	}
	lane.sourceMu.Lock()
	children := make([]*meetingTranscriptionLane, 0, len(lane.sourceLanes))
	for _, timer := range lane.sourceRetireTimers {
		timer.Stop()
	}
	for _, child := range lane.sourceLanes {
		child.discardOnClose = !commitPending
		children = append(children, child)
	}
	lane.sourceLanes = make(map[string]*meetingTranscriptionLane)
	lane.sourceRetireTimers = make(map[string]*time.Timer)
	lane.sourceRetireGeneration = make(map[string]uint64)
	lane.sourceAcceptedForTurn = false
	lane.sourceMu.Unlock()
	for _, child := range children {
		child.close()
	}
}

// repairProviderConnection is recovery-ladder step 2. For a source manager it
// discards and rebuilds the per-publication child lanes (each child owns its
// own provider websocket, so the rebuild IS the reconnect); the next accepted
// frame recreates them. For a plain lane it asks the run loop to drop and
// redial. The recording epoch is untouched on both paths.
func (lane *meetingTranscriptionLane) repairProviderConnection() {
	if lane == nil {
		return
	}
	if lane.sourceManager {
		// discardOnClose: a stalled child's pending buffer never reached the
		// provider, and committing it now would attribute stale audio to the
		// wrong moment.
		lane.closeSourceLanes(false)
		return
	}
	select {
	case lane.repair <- struct{}{}:
	default:
	}
}

func (lane *meetingTranscriptionLane) resetSourcesForRecordingEpoch(epoch uint64) {
	if lane == nil || !lane.sourceManager || epoch == 0 {
		return
	}
	lane.sourceMu.Lock()
	// Recording epochs only advance. A frame that captured the room's prior
	// epoch before Record toggled must never roll the manager backwards after
	// the synchronous reset has fenced it.
	if lane.recordingEpoch >= epoch {
		lane.sourceMu.Unlock()
		return
	}
	lane.recordingEpoch = epoch
	children := make([]*meetingTranscriptionLane, 0, len(lane.sourceLanes))
	for _, timer := range lane.sourceRetireTimers {
		timer.Stop()
	}
	for _, child := range lane.sourceLanes {
		child.discardOnClose = true
		children = append(children, child)
	}
	lane.sourceLanes = make(map[string]*meetingTranscriptionLane)
	lane.sourceRetireTimers = make(map[string]*time.Timer)
	lane.sourceRetireGeneration = make(map[string]uint64)
	lane.sourceAcceptedForTurn = false
	lane.sourceMu.Unlock()
	for _, child := range children {
		child.close()
	}
}

func (lane *meetingTranscriptionLane) enqueueSourceWithConsent(trackKey, participantName string, roomPCM []int16, fence ConsentFence, epoch uint64) bool {
	if lane == nil || !lane.sourceManager || strings.TrimSpace(trackKey) == "" || strings.TrimSpace(participantName) == "" || len(roomPCM) == 0 || epoch == 0 {
		return false
	}
	trackKey = strings.TrimSpace(trackKey)
	participantName = canonicalRoomParticipantName(participantName)
	if participantName == "" {
		return false
	}
	lane.sourceMu.Lock()
	if lane.recordingEpoch > epoch {
		lane.sourceMu.Unlock()
		return false
	}
	if lane.recordingEpoch < epoch {
		lane.sourceMu.Unlock()
		lane.resetSourcesForRecordingEpoch(epoch)
		lane.sourceMu.Lock()
		if lane.recordingEpoch != epoch {
			lane.sourceMu.Unlock()
			return false
		}
	}
	lane.recordingEpoch = epoch
	if timer := lane.sourceRetireTimers[trackKey]; timer != nil {
		timer.Stop()
		delete(lane.sourceRetireTimers, trackKey)
	}
	lane.sourceRetireGeneration[trackKey]++
	child := lane.sourceLanes[trackKey]
	if child != nil && (child.fixedSource == nil || child.fixedSource.Speaker != participantName || !sameConsentFenceVersion(child.fixedSource.Fence, fence) || child.recordingEpoch != epoch) {
		delete(lane.sourceLanes, trackKey)
		child.discardOnClose = true
		lane.sourceMu.Unlock()
		child.close()
		lane.sourceMu.Lock()
		child = nil
	}
	if child == nil {
		child = newMeetingTranscriptionLaneForRoomGeneration(lane.app, lane.apiKey, lane.transcriptionModel, lane.roomID, lane.mediaGeneration)
		child.sittingID = lane.sittingID
		child.recordingEpoch = epoch
		child.fixedSource = &sourceTranscriptIdentity{TrackKey: trackKey, Speaker: participantName, Fence: fence, RecordingEpoch: epoch}
		child.sourceBindings = newSourceTranscriptBindings()
		child.start()
		lane.sourceLanes[trackKey] = child
	}
	lane.sourceMu.Unlock()
	accepted := child.enqueueWithConsent(roomPCM, []ConsentFence{fence})
	if accepted {
		lane.sourceMu.Lock()
		if lane.recordingEpoch == epoch && lane.sourceLanes[trackKey] == child {
			lane.sourceAcceptedForTurn = true
		}
		lane.sourceMu.Unlock()
	}
	return accepted
}

func (lane *meetingTranscriptionLane) removeSource(trackKey string) {
	if lane == nil || !lane.sourceManager || strings.TrimSpace(trackKey) == "" {
		return
	}
	trackKey = strings.TrimSpace(trackKey)
	lane.sourceMu.Lock()
	child := lane.sourceLanes[trackKey]
	if child == nil {
		lane.sourceMu.Unlock()
		return
	}
	if timer := lane.sourceRetireTimers[trackKey]; timer != nil {
		timer.Stop()
	}
	lane.sourceRetireGeneration[trackKey]++
	generation := lane.sourceRetireGeneration[trackKey]
	deadline := time.Now().Add(transcriptionSourceRetireMax)
	lane.sourceRetireTimers[trackKey] = time.AfterFunc(transcriptionSourceRetireInitial, func() {
		lane.checkSourceRetirement(trackKey, child, generation, deadline)
	})
	lane.sourceMu.Unlock()
}

const (
	transcriptionSourceRetireInitial = 2 * time.Second
	transcriptionSourceRetirePoll    = 500 * time.Millisecond
	transcriptionSourceRetireMax     = 30 * time.Second
)

// claimSourceTurnOwnership binds one mixed Realtime input turn to the
// identity-preserving source lanes if at least one exact source frame was
// accepted since the prior provider commit. This is intentionally independent
// of transient websocket connection state: a turn has one persistence owner,
// so the mixed fallback can never race a later source-bound completion.
func (lane *meetingTranscriptionLane) claimSourceTurnOwnership() bool {
	if lane == nil || !lane.sourceManager {
		return false
	}
	lane.sourceMu.Lock()
	owned := lane.sourceAcceptedForTurn
	lane.sourceAcceptedForTurn = false
	lane.sourceMu.Unlock()
	return owned
}

func (lane *meetingTranscriptionLane) checkSourceRetirement(trackKey string, child *meetingTranscriptionLane, generation uint64, deadline time.Time) {
	if lane == nil || child == nil {
		return
	}
	lane.sourceMu.Lock()
	if lane.sourceLanes[trackKey] != child || lane.sourceRetireGeneration[trackKey] != generation {
		lane.sourceMu.Unlock()
		return
	}
	drained := child.isConnected() && len(child.consentInput) == 0 && child.sourceBindings.Len() == 0
	expired := !time.Now().Before(deadline)
	if !drained && !expired {
		lane.sourceRetireTimers[trackKey] = time.AfterFunc(transcriptionSourceRetirePoll, func() {
			lane.checkSourceRetirement(trackKey, child, generation, deadline)
		})
		lane.sourceMu.Unlock()
		return
	}
	delete(lane.sourceLanes, trackKey)
	delete(lane.sourceRetireTimers, trackKey)
	delete(lane.sourceRetireGeneration, trackKey)
	child.discardOnClose = expired && !drained
	lane.sourceMu.Unlock()
	child.close()
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
	if lane.sourceManager {
		lane.sourceMu.Lock()
		children := make([]*meetingTranscriptionLane, 0, len(lane.sourceLanes))
		for _, child := range lane.sourceLanes {
			children = append(children, child)
		}
		lane.sourceMu.Unlock()
		for _, child := range children {
			if child.isConnected() {
				return true
			}
		}
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
	} else {
		broadcastAssistantEvent("status", "Transcript lane disconnected", map[string]any{"model": lane.transcriptionModel})
	}
	// The room status pill is server-authoritative. Publish the connection
	// edge through the same versioned participant snapshot as recording
	// toggles so clients never keep a green "Live" claim during provider
	// reconnects. Source managers project true while any admitted source lane
	// remains connected.
	if lane.app != nil {
		broadcastRoomKanbanEvent(lane.roomID, "participants", lane.app.roomSnapshotForTranscriptionConnectionEdge(lane.roomID))
	}
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
			} else if errors.Is(err, errTranscriptionLaneRepair) {
				log.Warnf("Transcript lane reconnecting for capture-stall repair room=%s", lane.roomID)
				broadcastAssistantEvent("status", "Transcript lane reconnecting", map[string]any{"reason": "capture stall repair"})
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
		if err != nil && !errors.Is(err, errTranscriptionLaneSessionRefresh) && !errors.Is(err, errTranscriptionLaneSessionExpired) && !errors.Is(err, errTranscriptionLaneRepair) && backoff < transcriptionLaneReconnectMax {
			backoff *= 2
			if backoff > transcriptionLaneReconnectMax {
				backoff = transcriptionLaneReconnectMax
			}
		}
	}
}

func (lane *meetingTranscriptionLane) runOnce() error {
	conn, _, err := aiProviderRealtimeWebSocketDialer().Dial(transcriptionLaneWebSocketURL(), http.Header{
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
	lane.segmentBindings.Reset()
	if lane.sourceBindings != nil {
		lane.sourceBindings.Reset()
	}
	lane.setConnected(true)

	readErr := make(chan error, 1)
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				readErr <- err
				return
			}
			if lane.app.handleTranscriptionLaneEventForScopeWithSourceBindings(lane.scope(), raw, lane.transcriptionModel, lane.segmentBindings, lane.sourceBindings) {
				readErr <- errTranscriptionLaneSessionExpired
				return
			}
		}
	}()

	commitTimer := time.NewTimer(time.Hour)
	stopTranscriptionTimer(commitTimer)
	defer commitTimer.Stop()
	// Fix 6: the ceiling on one segment. Armed on the transition INTO
	// pendingAudio and re-armed after every commit, never re-armed per frame —
	// that is the difference between a cap and the silence timer.
	segmentTimer := time.NewTimer(time.Hour)
	stopTranscriptionTimer(segmentTimer)
	defer segmentTimer.Stop()
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
		stopTranscriptionTimer(segmentTimer)
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
		segmentID := uuid.NewString()
		pendingAudio = false
		pendingAudioSamples = 0
		pendingFences = map[string]ConsentFence{}
		segmentCapture = nil
		stopTranscriptionTimer(segmentTimer)
		scope := RoomScoutScope{RoomID: lane.roomID, SittingID: lane.sittingID, MediaGeneration: lane.mediaGeneration}
		lane.app.noteRealtimeSpeechStoppedForScope(scope)
		if lane.fixedSource != nil && lane.sourceBindings != nil {
			lane.sourceBindings.Put(segmentID, sourceTranscriptRecord{
				sourceTranscriptIdentity: *lane.fixedSource,
				Capture:                  capture,
			})
		} else {
			lane.app.freezeAttributionWindowAtCommitForScopeWithSegmentAndConsent(scope, segmentID, capture, contributorFences)
		}
		return lane.commitPendingTranscriptionAudio(conn, samples, segmentID)
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
		if lane.fixedSource == nil && pendingAudio && pendingAudioSamples >= transcriptionLaneMinCommitSamples {
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
			// Arm the ceiling exactly once per segment. Re-arming it per frame
			// (the way the silence timer is re-armed) would recreate the
			// unbounded-monologue bug it exists to close.
			stopTranscriptionTimer(segmentTimer)
			segmentTimer.Reset(transcriptionLaneMaxSegmentDuration)
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
			if pendingAudio && !lane.discardOnClose {
				_ = commitPending()
			} else if pendingAudio {
				clearPending(true)
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
		case <-lane.repair:
			// Capture-stall recovery ladder step 2. Drop the uncommitted
			// segment (it never reached the provider) and redial.
			if pendingAudio {
				clearPending(true)
			}
			return errTranscriptionLaneRepair
		case <-segmentTimer.C:
			if !pendingAudio {
				continue
			}
			// A monologue commits on the ceiling. commitPendingTranscriptionAudio
			// pads short buffers and the provider handles a mid-sentence cut, so
			// no audio is lost — only the sentence boundary moves.
			if err := commitPending(); err != nil {
				return err
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

func (lane *meetingTranscriptionLane) commitPendingTranscriptionAudio(conn *websocket.Conn, pendingSamples int, segmentID string) error {
	paddingSamples := transcriptionLaneCommitPaddingSamples(pendingSamples)
	if paddingSamples > 0 {
		if err := lane.writeJSON(conn, map[string]any{
			"type":  "input_audio_buffer.append",
			"audio": base64.StdEncoding.EncodeToString(make([]byte, paddingSamples*transcriptionLanePCMBytesPerSample)),
		}); err != nil {
			return fmt.Errorf("pad transcription audio: %w", err)
		}
	}

	committedSamples := pendingSamples + paddingSamples
	seconds := float64(committedSamples) / float64(transcriptionLaneInputSampleRate)
	if err := lane.segmentBindings.Commit(segmentID, seconds); err != nil {
		return fmt.Errorf("register transcription segment: %w", err)
	}
	if err := lane.writeJSON(conn, map[string]any{"type": "input_audio_buffer.commit", "event_id": "segment-commit-" + segmentID}); err != nil {
		return fmt.Errorf("commit transcription audio: %w", err)
	}
	// W0-5: the commit is the billing moment on this duration-billed lane —
	// meter it here (padding included: the API transcribes those samples too),
	// whether or not the transcription later completes.
	lane.noteCommittedSegmentUsage(committedSamples)
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
	lane.noteCommittedSegmentUsage(committedSamples)
	if lane == nil || committedSamples <= 0 {
		return
	}
	seconds := float64(committedSamples) / float64(transcriptionLaneInputSampleRate)
	// Legacy/test callers have no provider-item binding. The live lane uses
	// segmentBindings and therefore never consumes this FIFO.
	lane.app.pushTranscriptionSegmentSecondsForLaneScope(lane.scope(), seconds)
}

func (lane *meetingTranscriptionLane) noteCommittedSegmentUsage(committedSamples int) {
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
	return app.handleTranscriptionLaneEventForScopeWithBindings(scope, raw, model, nil)
}

func (app *kanbanBoardApp) handleTranscriptionLaneEventForScopeWithBindings(scope RoomScoutScope, raw []byte, model string, bindings *transcriptionSegmentBindings) bool {
	return app.handleTranscriptionLaneEventForScopeWithSourceBindings(scope, raw, model, bindings, nil)
}

func (app *kanbanBoardApp) handleTranscriptionLaneEventForScopeWithSourceBindings(scope RoomScoutScope, raw []byte, model string, bindings *transcriptionSegmentBindings, sourceBindings *sourceTranscriptBindings) bool {
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
		recordCapabilitySuccess(capabilityMeetingSTT, time.Now().UTC())
		broadcastAssistantEvent("status", "OpenAI transcription session configured", map[string]any{"eventType": event.Type})
	case "error":
		if event.Error != nil {
			recordCapabilityFailure(capabilitySTT, time.Now().UTC(), fmt.Errorf("%s", firstNonEmptyString(event.Error.Code, "transcription_error")))
			recordCapabilityFailure(capabilityMeetingSTT, time.Now().UTC(), fmt.Errorf("%s", firstNonEmptyString(event.Error.Code, "transcription_error")))
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
	case "input_audio_buffer.committed":
		if bindings == nil {
			break
		}
		records, err := bindings.BindCommitted(event.ItemID, event.PreviousItemID)
		if err != nil {
			if errors.Is(err, errTranscriptionSegmentBindingDeferred) {
				recordEvalEvent(seatTranscriptionLane, evalKindTranscriptSegment, map[string]any{
					"status":           "binding_deferred",
					"room_id":          roomID,
					"item_id":          strings.TrimSpace(event.ItemID),
					"previous_item_id": strings.TrimSpace(event.PreviousItemID),
				})
				break
			}
			recordCapabilityFailure(capabilitySTT, time.Now().UTC(), fmt.Errorf("bind committed transcription item: %w", err))
			recordCapabilityFailure(capabilityMeetingSTT, time.Now().UTC(), fmt.Errorf("bind committed transcription item: %w", err))
			recordEvalEvent(seatTranscriptionLane, evalKindTranscriptSegment, map[string]any{
				"status":           "binding_failed",
				"room_id":          roomID,
				"item_id":          strings.TrimSpace(event.ItemID),
				"previous_item_id": strings.TrimSpace(event.PreviousItemID),
			})
			// A conflicting/branched provider chain cannot be safely repaired in
			// place. Fence the connection so its orphaned attribution and metering
			// state cannot leak into the next session.
			return true
		}
		for _, record := range records {
			recordEvalEvent(seatTranscriptionLane, evalKindTranscriptSegment, map[string]any{
				"status":        "bound",
				"room_id":       roomID,
				"segment_id":    record.SegmentID,
				"item_id":       record.ProviderItem,
				"audio_seconds": record.AudioSeconds,
			})
		}
	case "conversation.item.input_audio_transcription.completed":
		persistedBefore := app.transcriptionEventPersisted(roomID, scope.SittingID, event.EventID, event.ItemID)
		segmentID := ""
		audioSeconds := float64(0)
		if bindings != nil {
			record, err := bindings.Consume(event.ItemID)
			if err != nil {
				recordCapabilityFailure(capabilitySTT, time.Now().UTC(), fmt.Errorf("resolve completed transcription item: %w", err))
				recordCapabilityFailure(capabilityMeetingSTT, time.Now().UTC(), fmt.Errorf("resolve completed transcription item: %w", err))
				recordEvalEvent(seatTranscriptionLane, evalKindTranscriptSegment, map[string]any{
					"status":  "unbound_completed",
					"room_id": roomID,
					"item_id": strings.TrimSpace(event.ItemID),
				})
				break
			}
			segmentID, audioSeconds = record.SegmentID, record.AudioSeconds
		} else {
			audioSeconds = app.popTranscriptionSegmentSecondsForLaneScope(scope)
		}
		// This is provider success, but it is not yet meeting-transcript success.
		// Source identity, sitting, consent, and the durable append all sit below;
		// the 2026-09-03 incident had a fresh provider-success stamp alongside no
		// transcript artifact. Clearing the watchdog here would recreate that
		// false green whenever any of those downstream gates refuses the row.
		recordCapabilitySuccess(capabilitySTT, time.Now().UTC())
		recordEvalEvent(seatTranscriptionLane, evalKindTranscriptSegment, map[string]any{
			"status":        "completed",
			"room_id":       roomID,
			"segment_id":    segmentID,
			"item_id":       strings.TrimSpace(event.ItemID),
			"audio_seconds": audioSeconds,
		})
		if sourceBindings != nil {
			sourceRecord, ok := sourceBindings.Consume(segmentID)
			if !ok {
				recordCapabilityFailure(capabilityMeetingSTT, time.Now().UTC(), fmt.Errorf("source-bound transcription identity unavailable"))
				recordEvalEvent(seatTranscriptionLane, evalKindTranscriptSegment, map[string]any{
					"status": "source_identity_unavailable", "room_id": roomID, "segment_id": segmentID,
				})
				break
			}
			app.rememberTranscriptForKnownSource(scope, segmentID, event, "transcript_lane", model, sourceRecord)
		} else if mediaGeneration > 0 {
			app.rememberTranscriptForMediaScopeSegment(scope, segmentID, event, "transcript_lane", model)
		} else {
			app.rememberTranscriptForSegment(roomID, segmentID, event, "transcript_lane", model)
		}
		if !persistedBefore && app.transcriptionEventPersisted(roomID, scope.SittingID, event.EventID, event.ItemID) {
			recordCapabilitySuccess(capabilityMeetingSTT, time.Now().UTC())
			// The capture-stall watchdog's recovery clock advances only after the
			// exact provider completion is durable. A connected websocket, a moving
			// audio queue, and even a provider completion can all coexist with a
			// transcript row rejected by a stale sitting or consent fence.
			app.noteTranscriptCommit(roomID)
		} else if !persistedBefore {
			recordCapabilityFailure(capabilityMeetingSTT, time.Now().UTC(), fmt.Errorf("completed transcription was not persisted"))
		}
	case "conversation.item.input_audio_transcription.failed":
		segmentID := ""
		audioSeconds := float64(0)
		if bindings != nil {
			record, err := bindings.Consume(event.ItemID)
			if err != nil {
				recordCapabilityFailure(capabilitySTT, time.Now().UTC(), fmt.Errorf("resolve failed transcription item: %w", err))
				recordCapabilityFailure(capabilityMeetingSTT, time.Now().UTC(), fmt.Errorf("resolve failed transcription item: %w", err))
				recordEvalEvent(seatTranscriptionLane, evalKindTranscriptSegment, map[string]any{
					"status":  "unbound_failed",
					"room_id": roomID,
					"item_id": strings.TrimSpace(event.ItemID),
				})
				break
			}
			segmentID, audioSeconds = record.SegmentID, record.AudioSeconds
		} else {
			audioSeconds = app.popTranscriptionSegmentSecondsForLaneScope(scope)
		}
		recordCapabilityFailure(capabilitySTT, time.Now().UTC(), fmt.Errorf("transcription segment failed"))
		recordCapabilityFailure(capabilityMeetingSTT, time.Now().UTC(), fmt.Errorf("transcription segment failed"))
		// W0-5: a failed segment is speech the brain never heard — this event
		// series is the raw feed for the >2% drop-off alarm.
		recordEvalEvent(seatTranscriptionLane, evalKindTranscriptSegment, map[string]any{
			"status":        "failed",
			"room_id":       roomID,
			"segment_id":    segmentID,
			"item_id":       strings.TrimSpace(event.ItemID),
			"audio_seconds": audioSeconds,
		})
		// A6: a failed segment yields no transcript to persist, but it still had a
		// window frozen at its commit. Pop it (discard) so the FIFO stays aligned;
		// otherwise the next .completed inherits this dead turn's boundaries and every
		// later transcript is attributed one turn late for the rest of the sitting.
		if sourceBindings != nil {
			_, _ = sourceBindings.Consume(segmentID)
		} else if mediaGeneration > 0 {
			app.popPendingAttributionWindowForScopeSegment(scope, segmentID)
		} else {
			app.popPendingAttributionWindowForRoomSegment(roomID, segmentID)
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

// transcriptionEventPersisted is the recovery proof for one provider
// completion. The provider event id (or item id when the event id is absent)
// is the same durable identity appendAttributedTranscriptEntryWithCapture
// stores. The caller snapshots this before and after persistence so an old
// duplicate cannot be borrowed as proof that a new completion advanced the
// transcript, while a newly durable row still clears the stall.
func (app *kanbanBoardApp) transcriptionEventPersisted(roomID, sittingID, eventID, itemID string) bool {
	if app == nil || app.memory == nil {
		return false
	}
	id := strings.TrimSpace(eventID)
	if id == "" {
		id = strings.TrimSpace(itemID)
	}
	if id == "" {
		// Realtime transcription completions are item-bound. An identity-less
		// event may still be de-duplicated by the memory store's content hash,
		// but it cannot prove WHICH provider completion landed, so recovery must
		// fail closed instead of borrowing an unrelated transcript row.
		return false
	}
	roomID = normalizeRoomID(roomID)
	sittingID = strings.TrimSpace(sittingID)
	app.memory.mu.RLock()
	defer app.memory.mu.RUnlock()
	for index := len(app.memory.entries) - 1; index >= 0; index-- {
		entry := app.memory.entries[index]
		if entry.Kind != meetingMemoryKindTranscript || entry.ID != id || normalizeRoomID(entry.Metadata["roomId"]) != roomID {
			continue
		}
		return sittingID == "" || strings.TrimSpace(entry.Metadata["meetingId"]) == sittingID
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
	return defaultTranscriptionLaneModel
}

func transcriptionLaneWebSocketURL() string {
	values := url.Values{}
	values.Set("intent", "transcription")
	return realtimeWebSocketURL + "?" + values.Encode()
}

// transcriptionModelAcceptsPrompt reports whether transcription configuration
// may carry modern context fields for this model. The gpt-4o transcription
// family and the current gpt-transcribe families accept a prompt; the realtime
// whisper model does NOT — sending `prompt` there is rejected live with
// "The 'prompt' parameter is not supported for this model" and would break the
// session. So the prompt/near-field config is gated by model rather than sent
// unconditionally. The committed lane is server-pinned to gpt-transcribe.
func transcriptionModelAcceptsPrompt(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(model, "gpt-4o") || model == "gpt-transcribe" || model == "gpt-live-transcribe"
}

func transcriptionModelUsesModernHints(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return model == "gpt-transcribe" || model == "gpt-live-transcribe"
}

func transcriptionModelAcceptsNearField(model string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(model)), "gpt-4o")
}

func transcriptionLaneSessionConfig(model string) map[string]any {
	model = strings.TrimSpace(model)
	transcription := map[string]any{"model": model}
	if transcriptionModelUsesModernHints(model) {
		transcription["languages"] = []string{"en"}
		transcription["keywords"] = domainVocabulary()
	} else {
		transcription["language"] = "en"
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
	if transcriptionModelAcceptsNearField(model) {
		input["noise_reduction"] = map[string]any{"type": "near_field"}
	}
	if transcriptionModelAcceptsPrompt(model) {
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
