package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const (
	roomScoutProactiveRestartAfter = 55 * time.Minute
	roomScoutDisconnectedGrace     = 5 * time.Second
	roomScoutRestartRetryAfter     = 2 * time.Second
	roomScoutRestartRetryMaxAfter  = 30 * time.Second
	roomScoutRestartMaxAttempts    = 4
)

var errRoomScoutProviderUnavailable = errors.New("room Scout provider session is unavailable")

// roomScoutProviderSession is the replaceable provider-session half of the
// transport. Tests replace the dialer, while production uses one independent
// Pion PeerConnection and OpenAI Realtime call for every RoomScoutScope.
type roomScoutProviderSession interface {
	WriteMixedPCM(context.Context, []int16) error
	SendEvent(any) error
	Close() error
}

type roomScoutProviderDialer func(context.Context, *openAIRoomScoutTransport, uint64) (roomScoutProviderSession, error)

type roomScoutRestartRequest struct {
	generation uint64
	reason     string
}

// mediaSoakProviderFaults is a default-empty, exact-scope fault latch used only
// by the authenticated W2A observer. It lives in the provider adapter so the
// soak exercises the real provider-failure isolation/restart path while the
// room's separately-owned Pion media plane continues untouched.
var mediaSoakProviderFaults = struct {
	sync.Mutex
	active map[string]bool
	hits   map[string]uint64
}{active: map[string]bool{}, hits: map[string]uint64{}}

func mediaSoakProviderFaultKey(scope RoomScoutScope) string {
	return fmt.Sprintf("%s|%s|%d", normalizeRoomID(scope.RoomID), scope.SittingID, scope.MediaGeneration)
}

func setMediaSoakProviderFault(scope RoomScoutScope, active bool) {
	key := mediaSoakProviderFaultKey(scope)
	mediaSoakProviderFaults.Lock()
	if active {
		mediaSoakProviderFaults.active[key] = true
	} else {
		delete(mediaSoakProviderFaults.active, key)
		delete(mediaSoakProviderFaults.hits, key)
	}
	mediaSoakProviderFaults.Unlock()
}

func consumeMediaSoakProviderFault(scope RoomScoutScope) bool {
	key := mediaSoakProviderFaultKey(scope)
	mediaSoakProviderFaults.Lock()
	defer mediaSoakProviderFaults.Unlock()
	if !mediaSoakProviderFaults.active[key] {
		return false
	}
	mediaSoakProviderFaults.hits[key]++
	return true
}

func mediaSoakProviderFaultHits(scope RoomScoutScope) uint64 {
	mediaSoakProviderFaults.Lock()
	defer mediaSoakProviderFaults.Unlock()
	return mediaSoakProviderFaults.hits[mediaSoakProviderFaultKey(scope)]
}

// openAIRoomScoutTransport owns exactly one active provider session at a time.
// Provider generation is separate from media generation: it fences callbacks
// from an expired/replaced OpenAI call inside the same room sitting.
type openAIRoomScoutTransport struct {
	app       *kanbanBoardApp
	scope     RoomScoutScope
	callbacks RoomScoutCallbacks
	apiKey    string
	model     string
	dial      roomScoutProviderDialer

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu                 sync.Mutex
	session            roomScoutProviderSession
	generation         uint64
	closed             bool
	restarts           chan roomScoutRestartRequest
	restartFailures    int
	restartCircuitOpen bool
	// outputTrack is one stable SFU publication for the full room invitation.
	// Provider response tracks are intentionally short-lived; publishing each
	// one directly made subscribers renegotiate after Scout had already begun
	// speaking and removed the receiver again at the end of every answer.
	outputTrack *webrtc.TrackLocalStaticRTP
	outputMu    sync.Mutex
	outputSeq   uint16
	outputTS    uint32
	outputCount uint64
	lastSpeech  time.Time

	voiceMu                sync.Mutex
	armedUntil             time.Time
	responseActive         bool
	responseRecordingEpoch uint64
	pendingSpeech          bool
	pendingSession         roomScoutProviderSession
	pendingGeneration      uint64
	transcriptionOwnership map[string]bool
	transcriptionFIFO      []bool
}

type roomScoutProviderCircuitSnapshot struct {
	Failures int
	Open     bool
}

func (transport *openAIRoomScoutTransport) providerCircuitSnapshot() roomScoutProviderCircuitSnapshot {
	if transport == nil {
		return roomScoutProviderCircuitSnapshot{}
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return roomScoutProviderCircuitSnapshot{Failures: transport.restartFailures, Open: transport.restartCircuitOpen}
}

func newOpenAIRoomScoutTransport(ctx context.Context, app *kanbanBoardApp, apiKey string, scope RoomScoutScope, callbacks RoomScoutCallbacks, dial roomScoutProviderDialer) (*openAIRoomScoutTransport, error) {
	if app == nil || !scope.valid() {
		return nil, ErrRoomScoutFence
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("%w: OPENAI_API_KEY is not configured", errRoomScoutProviderUnavailable)
	}
	if dial == nil {
		dial = dialPionRoomScoutProviderSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	transportCtx, cancel := context.WithCancel(ctx)
	transport := &openAIRoomScoutTransport{
		app: app, scope: scope, callbacks: callbacks, apiKey: apiKey, model: realtimeModel(), dial: dial,
		ctx: transportCtx, cancel: cancel, done: make(chan struct{}), restarts: make(chan roomScoutRestartRequest, 1),
		generation: 1, transcriptionOwnership: make(map[string]bool),
	}
	outputTrack, err := addPersistentRoomScoutOutputTrack(scope)
	if err != nil {
		cancel()
		return nil, err
	}
	transport.outputTrack = outputTrack
	broadcastRoomKanbanEvent(scope.RoomID, "participant_track", map[string]any{
		"name": scoutParticipantName, "kind": "audio", "trackId": outputTrack.ID(),
		"sourceTrackId": outputTrack.ID(), "streamId": outputTrack.StreamID(), "roomId": scope.RoomID,
	})
	requestRoomMediaCommandForGeneration(scope.RoomID, scope.MediaGeneration, roomMediaCommandTrack)

	session, err := dial(transportCtx, transport, 1)
	if err != nil {
		cancel()
		removeTrack(outputTrack)
		return nil, err
	}
	if session == nil {
		cancel()
		removeTrack(outputTrack)
		return nil, fmt.Errorf("%w: dialer returned no session", errRoomScoutProviderUnavailable)
	}
	transport.mu.Lock()
	transport.session = session
	transport.mu.Unlock()
	go transport.runOutputKeepalive()
	go transport.runRestartSupervisor()
	transport.publish(1, "status", map[string]any{"text": "Scout connected", "voiceState": "listening"})
	return transport, nil
}

func (app *kanbanBoardApp) productionRoomScoutTransportFactory(apiKey string) RoomScoutTransportFactory {
	return func(ctx context.Context, scope RoomScoutScope, callbacks RoomScoutCallbacks) (RoomScoutTransport, error) {
		return newOpenAIRoomScoutTransport(ctx, app, apiKey, scope, callbacks, nil)
	}
}

func (transport *openAIRoomScoutTransport) WriteMixedPCM(ctx context.Context, samples []int16) error {
	if transport == nil || len(samples) == 0 {
		return nil
	}
	transport.mu.Lock()
	if transport.closed {
		transport.mu.Unlock()
		return ErrRoomScoutClosed
	}
	session, generation := transport.session, transport.generation
	transport.mu.Unlock()
	if session == nil || !transport.accepts(generation) {
		// During a provider-only restart, shed Scout audio. Returning nil keeps
		// the room mixer and the separately-owned transcription lane healthy.
		return nil
	}
	if consumeMediaSoakProviderFault(transport.scope) {
		err := errors.New("media-soak injected AI provider write failure")
		transport.setStatus(generation, RoomScoutDegraded, err)
		transport.publish(generation, "error", map[string]any{"text": "Scout audio degraded", "error": err.Error()})
		transport.requestRestart(generation, "media-soak provider failure")
		return nil
	}
	if ctx == nil {
		ctx = transport.ctx
	}
	if err := session.WriteMixedPCM(ctx, samples); err != nil {
		transport.setStatus(generation, RoomScoutDegraded, err)
		transport.publish(generation, "error", map[string]any{"text": "Scout audio degraded", "error": trimForStorage(err.Error(), 300)})
		transport.requestRestart(generation, "audio input failed")
	}
	return nil
}

// CancelBufferedAudio implements RoomScoutBufferedAudioCanceler. The Realtime
// input buffer and any active response are provider-owned, so withdrawal must
// explicitly clear both; the containing bundle has already stopped/drained its
// local queue before this is called.
func (transport *openAIRoomScoutTransport) CancelBufferedAudio(ctx context.Context) error {
	if transport == nil {
		return ErrRoomScoutClosed
	}
	transport.mu.Lock()
	if transport.closed {
		transport.mu.Unlock()
		return ErrRoomScoutClosed
	}
	session, generation := transport.session, transport.generation
	transport.mu.Unlock()
	if session == nil || !transport.accepts(generation) {
		return errRoomScoutProviderUnavailable
	}
	if err := session.SendEvent(map[string]any{"type": "input_audio_buffer.clear"}); err != nil {
		transport.requestRestart(generation, "input buffer clear failed")
		return err
	}
	// response.cancel can legitimately race an idle response. Sending it is
	// still required: when one is active, no withdrawn buffered speech may keep
	// producing output. Provider errors are handled by the normal event loop.
	if err := session.SendEvent(map[string]any{"type": "response.cancel"}); err != nil {
		transport.requestRestart(generation, "response cancel failed")
		return err
	}
	return nil
}

func (transport *openAIRoomScoutTransport) Close() error {
	if transport == nil {
		return nil
	}
	transport.mu.Lock()
	if transport.closed {
		done := transport.done
		transport.mu.Unlock()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		return nil
	}
	transport.closed = true
	transport.generation++ // synchronously fence every old provider callback
	session := transport.session
	transport.session = nil
	outputTrack := transport.outputTrack
	transport.outputTrack = nil
	transport.cancel()
	transport.mu.Unlock()
	transport.resetVoiceState()
	var err error
	if session != nil {
		err = session.Close()
	}
	if outputTrack != nil {
		removeTrack(outputTrack)
	}
	select {
	case <-transport.done:
	case <-time.After(2 * time.Second):
	}
	return err
}

func (transport *openAIRoomScoutTransport) accepts(generation uint64) bool {
	if transport == nil || !transport.app.roomScoutScopeCurrent(transport.scope) {
		return false
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return !transport.closed && transport.generation == generation
}

func (transport *openAIRoomScoutTransport) publish(generation uint64, event string, payload any) bool {
	if !transport.accepts(generation) || transport.callbacks.Publish == nil {
		return false
	}
	return transport.callbacks.Publish(transport.scope, event, payload)
}

func (transport *openAIRoomScoutTransport) setStatus(generation uint64, status RoomScoutStatus, err error) bool {
	if !transport.accepts(generation) || transport.callbacks.Status == nil {
		return false
	}
	return transport.callbacks.Status(transport.scope, status, err)
}

func (transport *openAIRoomScoutTransport) requestRestart(generation uint64, reason string) {
	if transport == nil || !transport.app.roomScoutScopeCurrent(transport.scope) {
		return
	}
	transport.mu.Lock()
	accepted := !transport.closed && transport.generation == generation && !transport.restartCircuitOpen
	transport.mu.Unlock()
	if !accepted {
		return
	}
	request := roomScoutRestartRequest{generation: generation, reason: strings.TrimSpace(reason)}
	select {
	case transport.restarts <- request:
	default:
	}
}

func (transport *openAIRoomScoutTransport) runRestartSupervisor() {
	defer close(transport.done)
	timer := time.NewTimer(roomScoutProactiveRestartAfter)
	defer timer.Stop()
	for {
		select {
		case <-transport.ctx.Done():
			return
		case <-timer.C:
			transport.mu.Lock()
			generation := transport.generation
			transport.mu.Unlock()
			transport.replaceSession(roomScoutRestartRequest{generation: generation, reason: "scheduled refresh before session expiration"})
			timer.Reset(roomScoutProactiveRestartAfter)
		case request := <-transport.restarts:
			transport.replaceSession(request)
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(roomScoutProactiveRestartAfter)
		}
	}
}

func (transport *openAIRoomScoutTransport) replaceSession(request roomScoutRestartRequest) {
	transport.mu.Lock()
	if transport.closed || transport.generation != request.generation || transport.restartCircuitOpen {
		transport.mu.Unlock()
		return
	}
	old := transport.session
	transport.session = nil
	transport.generation++
	nextGeneration := transport.generation
	transport.mu.Unlock()
	transport.resetVoiceState()
	if old != nil {
		_ = old.Close()
	}
	transport.setStatus(nextGeneration, RoomScoutDegraded, fmt.Errorf("%s", firstNonEmptyString(request.reason, "provider restart")))
	transport.publish(nextGeneration, "status", map[string]any{"text": "Scout reconnecting", "reason": request.reason, "voiceState": "thinking"})

	session, err := transport.dial(transport.ctx, transport, nextGeneration)
	if err == nil && session == nil {
		err = fmt.Errorf("%w: dialer returned no replacement session", errRoomScoutProviderUnavailable)
	}
	if err != nil {
		transport.publish(nextGeneration, "error", map[string]any{"text": "Scout is temporarily unavailable", "error": trimForStorage(err.Error(), 300)})
		transport.mu.Lock()
		if transport.closed || transport.generation != nextGeneration {
			transport.mu.Unlock()
			return
		}
		transport.restartFailures++
		attempts := transport.restartFailures
		if attempts >= roomScoutRestartMaxAttempts {
			transport.restartCircuitOpen = true
			transport.mu.Unlock()
			circuitErr := fmt.Errorf("room Scout provider circuit opened after %d failed reconnects", attempts)
			transport.setStatus(nextGeneration, RoomScoutDegraded, circuitErr)
			transport.publish(nextGeneration, "error", map[string]any{"text": "Scout voice is unavailable until this room session is restarted", "error": circuitErr.Error()})
			return
		}
		transport.mu.Unlock()
		delay := roomScoutRestartRetryAfter << (attempts - 1)
		if delay > roomScoutRestartRetryMaxAfter {
			delay = roomScoutRestartRetryMaxAfter
		}
		time.AfterFunc(delay, func() {
			transport.requestRestart(nextGeneration, "retry after provider restart failure")
		})
		return
	}
	transport.mu.Lock()
	if transport.closed || transport.generation != nextGeneration {
		transport.mu.Unlock()
		if session != nil {
			_ = session.Close()
		}
		return
	}
	transport.session = session
	transport.restartFailures = 0
	transport.restartCircuitOpen = false
	transport.mu.Unlock()
	transport.setStatus(nextGeneration, RoomScoutReady, nil)
	transport.publish(nextGeneration, "status", map[string]any{"text": "Scout reconnected", "voiceState": "listening"})
}

func (app *kanbanBoardApp) roomScoutScopeCurrent(scope RoomScoutScope) bool {
	if app == nil || !scope.valid() {
		return false
	}
	app.mu.Lock()
	current := app.roomScoutScopeCurrentLocked(scope)
	app.mu.Unlock()
	if !current || app.memory == nil {
		return false
	}
	return strings.TrimSpace(app.memory.currentMeetingID(scope.RoomID)) == strings.TrimSpace(scope.SittingID)
}

type pionRoomScoutProviderSession struct {
	transport  *openAIRoomScoutTransport
	generation uint64
	pc         *webrtc.PeerConnection
	events     *webrtc.DataChannel
	inputTrack *webrtc.TrackLocalStaticSample
	inputEnc   *opusEncoder

	writeMu sync.Mutex
	sendMu  sync.Mutex
	close   sync.Once
}

func dialPionRoomScoutProviderSession(ctx context.Context, transport *openAIRoomScoutTransport, generation uint64) (roomScoutProviderSession, error) {
	peer, err := newPeerConnection()
	if err != nil {
		return nil, fmt.Errorf("create named-room Realtime peer: %w", err)
	}
	closeOnError := func(err error) (roomScoutProviderSession, error) {
		_ = peer.Close()
		return nil, err
	}
	inputTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeOpus, ClockRate: roomAudioSampleRate, Channels: realtimeAudioChannels,
	}, realtimeInputTrackID, realtimeInputStreamID)
	if err != nil {
		return closeOnError(fmt.Errorf("create named-room Realtime input track: %w", err))
	}
	encoder, err := newOpusEncoder(roomAudioSampleRate, realtimeAudioChannels)
	if err != nil {
		return closeOnError(fmt.Errorf("create named-room Realtime encoder: %w", err))
	}
	sender, err := peer.AddTrack(inputTrack)
	if err != nil {
		return closeOnError(fmt.Errorf("attach named-room Realtime input track: %w", err))
	}
	go drainRTCP(sender)
	events, err := peer.CreateDataChannel(realtimeEventChannelLabel, nil)
	if err != nil {
		return closeOnError(fmt.Errorf("create named-room Realtime event channel: %w", err))
	}
	session := &pionRoomScoutProviderSession{
		transport: transport, generation: generation, pc: peer, events: events, inputTrack: inputTrack, inputEnc: encoder,
	}

	peer.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if !transport.accepts(generation) {
			return
		}
		switch state {
		case webrtc.PeerConnectionStateFailed:
			transport.requestRestart(generation, "Realtime peer connection failed")
		case webrtc.PeerConnectionStateDisconnected:
			time.AfterFunc(roomScoutDisconnectedGrace, func() {
				if transport.accepts(generation) && peer.ConnectionState() == webrtc.PeerConnectionStateDisconnected {
					transport.requestRestart(generation, "Realtime peer connection stayed disconnected")
				}
			})
		}
	})
	events.OnOpen(func() {
		if !transport.accepts(generation) {
			return
		}
		_ = session.SendEvent(map[string]any{"type": "session.update", "session": transport.app.roomScoutSessionConfig(transport.scope, transport.model)})
		transport.publish(generation, "status", map[string]any{"text": "Scout is listening", "voiceState": "listening"})
	})
	events.OnMessage(func(message webrtc.DataChannelMessage) {
		if transport.accepts(generation) {
			transport.handleProviderEvent(session, generation, message.Data)
		}
	})
	peer.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		transport.forwardOutputTrack(ctx, generation, track)
	})

	offer, err := peer.CreateOffer(nil)
	if err != nil {
		return closeOnError(fmt.Errorf("create named-room Realtime offer: %w", err))
	}
	gatherComplete := webrtc.GatheringCompletePromise(peer)
	if err := peer.SetLocalDescription(offer); err != nil {
		return closeOnError(fmt.Errorf("set named-room Realtime local description: %w", err))
	}
	select {
	case <-ctx.Done():
		return closeOnError(ctx.Err())
	case <-gatherComplete:
	}
	local := peer.LocalDescription()
	if local == nil || strings.TrimSpace(local.SDP) == "" {
		return closeOnError(fmt.Errorf("named-room Realtime local description is unavailable"))
	}
	answer, err := transport.app.createRealtimeCallWithSessionContext(ctx, transport.apiKey, local.SDP, transport.app.roomScoutSessionConfig(transport.scope, transport.model))
	if err != nil {
		return closeOnError(err)
	}
	if err := peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer}); err != nil {
		return closeOnError(fmt.Errorf("set named-room Realtime remote description: %w", err))
	}
	return session, nil
}

func (session *pionRoomScoutProviderSession) WriteMixedPCM(ctx context.Context, roomPCM []int16) error {
	if session == nil || session.inputTrack == nil || session.inputEnc == nil {
		return errRoomScoutProviderUnavailable
	}
	if len(roomPCM)%roomAudioMixFrameSize != 0 {
		return fmt.Errorf("mixed PCM length %d must be a multiple of %d samples", len(roomPCM), roomAudioMixFrameSize)
	}
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	for offset := 0; offset < len(roomPCM); offset += roomAudioMixFrameSize {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		encoded, err := session.inputEnc.Encode(roomPCMForRealtime(roomPCM[offset : offset+roomAudioMixFrameSize]))
		if err != nil {
			return fmt.Errorf("encode named-room Scout audio: %w", err)
		}
		if err := session.inputTrack.WriteSample(media.Sample{Data: encoded, Duration: roomAudioMixInterval}); err != nil {
			return fmt.Errorf("write named-room Scout audio: %w", err)
		}
	}
	return nil
}

func (session *pionRoomScoutProviderSession) SendEvent(payload any) error {
	if session == nil || session.events == nil || session.events.ReadyState() != webrtc.DataChannelStateOpen {
		return errRoomScoutProviderUnavailable
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	session.sendMu.Lock()
	defer session.sendMu.Unlock()
	return session.events.SendText(string(raw))
}

func (session *pionRoomScoutProviderSession) Close() error {
	if session == nil {
		return nil
	}
	var err error
	session.close.Do(func() { err = session.pc.Close() })
	return err
}

func (app *kanbanBoardApp) createRealtimeCallWithSessionContext(ctx context.Context, apiKey, offerSDP string, config map[string]any) (string, error) {
	contentType, body, err := buildRealtimeCallRequest(offerSDP, config)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, realtimeCallsURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create named-room Realtime request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", contentType)
	response, err := realtimeHTTPClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("create named-room Realtime session: %w", err)
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(response.Body)
	if err != nil {
		return "", fmt.Errorf("read named-room Realtime answer: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", apiRequestFailedError("named-room Realtime session failed", response.Status, answer)
	}
	normalized, err := normalizeRealtimeSDP(string(answer))
	if err != nil {
		return "", fmt.Errorf("named-room Realtime session returned an invalid answer")
	}
	return normalized, nil
}

func (app *kanbanBoardApp) roomScoutSessionConfig(scope RoomScoutScope, model string) map[string]any {
	config := app.sessionConfig(model)
	config["instructions"] = app.roomScoutSessionInstructions(scope)
	config["tools"] = app.roomScoutTools()
	// Every ambient turn first resolves through a tool. The model classifies
	// natural participant turns with answer_room_question and side conversation
	// with do_nothing; the server only requests speech for the former (or another
	// deliberate room-scoped tool). No magic wake-prefix is required.
	config["tool_choice"] = "required"
	return config
}

// roomScoutAllowedTools is closed by construction. A newly-added office or
// private tool never appears in a named room until it receives an explicit
// scope-aware dispatch case below and is intentionally added here.
var roomScoutAllowedTools = map[string]bool{
	"set_recording":          true,
	"answer_memory_question": true,
	"portfolio_health":       true, "company_financial_snapshot": true,
	"financial_comps": true, "meeting_recap": true, "meeting_interval_recall": true,
	"cross_meeting_briefing": true, "get_meeting_detail": true,
	"answer_room_question": true, "do_nothing": true,
}

func (app *kanbanBoardApp) roomScoutTools() []map[string]any {
	all := app.realtimeRoomVoiceTools()
	tools := make([]map[string]any, 0, len(all))
	for _, tool := range all {
		if roomScoutAllowedTools[asString(tool["name"])] {
			tools = append(tools, tool)
		}
	}
	tools = append(tools, map[string]any{
		"type": "function", "name": "answer_room_question",
		"description": "Use when a participant clearly asks Scout a conversational question or request, asks for Scout's opinion, or continues a conversation with Scout. The request may use Scout's name anywhere or omit it in a natural follow-up; no exact wake phrase is required. Never use for side conversation between people, background speech, silence, or filler; use do_nothing for those.",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"request": map[string]any{"type": "string", "description": "A concise statement of the question or request Scout should answer aloud."},
			},
			"required": []string{"request"}, "additionalProperties": false,
		},
	})
	return tools
}

func (app *kanbanBoardApp) roomScoutSessionInstructions(scope RoomScoutScope) string {
	roster := app.participantSnapshotForRoom(scope.RoomID)
	people := "No human participant roster is currently available."
	if len(roster) > 0 {
		identities := make([]string, 0, len(roster))
		for _, name := range roster {
			identity := canonicalRoomActorName(name)
			if seed, ok := seededAccountForName(identity); ok {
				identity += " (" + strideRuntimePrincipalForEmail(seed.Email) + ")"
			}
			identities = append(identities, identity)
		}
		people = "Human roster for this sitting: " + strings.Join(identities, ", ") + "."
	}
	return app.sessionInstructions() + "\n\n" + strings.Join([]string{
		"# Named-room authority",
		fmt.Sprintf("This provider session is bound by the server to room %q, sitting %q, media generation %d.", scope.RoomID, scope.SittingID, scope.MediaGeneration),
		"Never infer, select, or change that room from user speech or tool arguments. Recall, recap, recording, proposals, artifacts, and launched work are server-scoped to this sitting.",
		people,
		"Treat each human as a separate coworker. Use speaker attribution from the shared meeting transcript and company-visible evidence to understand who said what; never merge one person's statements, preferences, or history into another's.",
		"A shared room never receives private chats, Settings imports, or private user-profile memory. Do not claim or imply otherwise.",
		"Tools omitted from this session are intentionally unavailable because their current implementation is office-global or user-private. Do not claim those actions completed.",
		"# Invited-participant turn taking",
		"You are a visible participant, not a wake-word utility. For a clear question or request directed to Scout, a request for your perspective, or a natural follow-up after you spoke, call answer_room_question unless a more specific listed tool is needed. Scout or Scott may appear anywhere in the turn and no exact phrase is required. For side conversation between people, background speech, silence, filler, or a turn not directed to you, call do_nothing and remain silent. Prefer staying quiet over interrupting.",
		"Never promise future work unless the tool result contains a durable receipt for it. If a requested future action is unavailable, say no work is scheduled and direct the participant to @Scout in room chat with the exact destination channel.",
	}, " ")
}

func (transport *openAIRoomScoutTransport) handleProviderEvent(session roomScoutProviderSession, generation uint64, raw []byte) {
	if !transport.accepts(generation) {
		return
	}
	var event kanbanRealtimeEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return
	}
	switch event.Type {
	case "session.created", "session.updated":
		transport.publish(generation, "status", map[string]any{"text": "Scout session configured", "eventType": event.Type})
	case "error":
		message, code := "Realtime provider error", ""
		if event.Error != nil {
			message, code = firstNonEmptyString(event.Error.Message, message), event.Error.Code
		}
		transport.publish(generation, "error", map[string]any{"text": message, "code": code})
		if code == "session_expired" {
			transport.requestRestart(generation, message)
		}
	case "conversation.item.input_audio_transcription.completed":
		transport.app.recordRoomScoutTranscriptionUsage(transport.scope.RoomID, event)
		transport.noteVoiceTranscript(event.Transcript)
		if sourceOwned, found := transport.takeTranscriptionOwnership(event.ItemID); roomScoutFallbackOwnsTranscription(sourceOwned, found) {
			transport.app.rememberRoomScoutTranscript(transport.scope, event, "scout_realtime", transport.model)
		}
	case "conversation.item.input_audio_transcription.failed":
		if sourceOwned, found := transport.takeTranscriptionOwnership(event.ItemID); roomScoutFallbackOwnsTranscription(sourceOwned, found) {
			transport.app.popRoomScoutAttribution(transport.scope)
		}
	case "conversation.item.input_audio_transcription.delta":
		if text := canonicalizeBoardText(event.Delta); text != "" {
			transport.publish(generation, "transcript", map[string]any{"text": "hearing: " + text, "eventType": event.Type})
		}
	case "input_audio_buffer.speech_started":
		transport.app.noteRoomScoutSpeechStarted(transport.scope)
		transport.publish(generation, "audio", map[string]any{"text": "Scout detected speech", "voiceState": "hearing"})
	case "input_audio_buffer.speech_stopped":
		transport.app.noteRoomScoutSpeechStopped(transport.scope)
		transport.publish(generation, "audio", map[string]any{"text": "Scout is thinking", "voiceState": "thinking"})
	case "input_audio_buffer.committed":
		contributorFences := transport.app.takeRoomScoutContributorFences(transport.scope)
		sourceOwned := transport.app.claimTranscriptionSourceTurnForRoom(transport.scope.RoomID)
		transport.rememberTranscriptionOwnership(event.ItemID, sourceOwned)
		if sourceOwned {
			transport.app.discardRoomScoutCurrentAttribution(transport.scope)
		} else {
			transport.app.freezeRoomScoutAttributionWithConsent(transport.scope, contributorFences)
		}
	case "response.created":
		transport.voiceMu.Lock()
		transport.responseActive = true
		transport.responseRecordingEpoch = transport.app.recordingEpochForRoom(transport.scope.RoomID)
		transport.voiceMu.Unlock()
		transport.publish(generation, "audio", map[string]any{"text": "Scout is thinking", "voiceState": "thinking"})
	case "response.output_audio_transcript.delta":
		transport.publish(generation, "audio", map[string]any{"text": "Scout is speaking", "voiceState": "talking"})
	case "response.output_audio_transcript.done", "response.output_text.done":
		if text := canonicalizeBoardText(firstNonEmptyString(event.Transcript, event.Text)); text != "" {
			transport.voiceMu.Lock()
			epoch := transport.responseRecordingEpoch
			transport.voiceMu.Unlock()
			transport.app.rememberRoomAgentTranscriptForEpoch(transport.scope, event, "agent_voice", transport.model, "scout", scoutParticipantName, epoch)
			transport.publish(generation, "answer", map[string]any{"text": text, "voiceState": "talking"})
		}
	case "response.output_item.done":
		if event.Item != nil && event.Item.Type == "function_call" {
			transport.handleProviderToolCall(session, generation, *event.Item, true)
		}
	case "response.function_call_arguments.done":
		transport.handleProviderToolCall(session, generation, realtimeFunctionCallFromArgumentsDone(event), true)
	case "response.done":
		transport.finishProviderResponse()
		transport.app.recordRoomScoutResponseUsage(transport.scope.RoomID, transport.model, event)
		if event.Response != nil {
			interrupted := isInterruptedRealtimeResponseStatus(event.Response.Status)
			for _, output := range event.Response.Output {
				if output.Type == "function_call" && !interrupted {
					transport.handleProviderToolCall(session, generation, output, false)
				}
			}
		}
		transport.publish(generation, "audio", map[string]any{"text": "Scout is listening", "voiceState": "listening"})
	}
}

func (transport *openAIRoomScoutTransport) handleProviderToolCall(session roomScoutProviderSession, generation uint64, output kanbanRealtimeOutputItem, allowIncomplete bool) {
	if strings.TrimSpace(output.CallID) == "" || !transport.accepts(generation) {
		return
	}
	args, parseErr := parseToolCallArguments(output)
	if parseErr != nil && classifyToolArgParse(parseErr, allowIncomplete) == toolArgsAwaitingMore {
		return
	}
	principal := ACLPrincipal{
		TenantID: canonicalTenantID(), ID: "scout-room:" + transport.scope.RoomID, Kind: ACLPrincipalService,
		RoomID: transport.scope.RoomID, SittingID: transport.scope.SittingID,
	}
	if transport.callbacks.RunTool == nil {
		return
	}
	armedAtStart := transport.voiceArmed()
	go func() {
		err := transport.callbacks.RunTool(transport.ctx, transport.scope, output.CallID, principal, func(ctx context.Context) error {
			if !transport.accepts(generation) {
				return ErrRoomScoutFence
			}
			var result map[string]any
			var changed bool
			var err error
			if parseErr != nil {
				err = parseErr
			} else {
				result, changed, err = transport.app.applyRoomScoutToolArgs(ctx, transport.scope, output.Name, args)
			}
			if err != nil {
				result = map[string]any{"ok": false, "error": err.Error()}
			}
			if !transport.accepts(generation) {
				return ErrRoomScoutFence
			}
			if err := session.SendEvent(map[string]any{
				"type": "conversation.item.create",
				"item": map[string]any{"type": "function_call_output", "call_id": output.CallID, "output": capVoiceToolResultContent(mustMarshalJSON(result))},
			}); err != nil {
				return err
			}
			if transport.shouldSpeakAfterTool(output.Name, result, changed, armedAtStart) {
				if err := transport.requestSpokenResponse(session, generation); err != nil {
					return err
				}
			}
			if changed {
				// A named-room tool result is delivered only to its owning room.
				// Company-global side effects are not exposed by this transport;
				// this remains defense-in-depth if a scoped adapter later reports a
				// room-local state mutation.
				broadcastRoomKanbanEvent(transport.scope.RoomID, "board", transport.app.snapshotState())
				broadcastRoomKanbanEvent(transport.scope.RoomID, "undo_available", transport.app.canUndoDelete())
			}
			transport.publish(generation, "action", map[string]any{"text": humanizeToolName(output.Name) + " complete", "tool": output.Name})
			return nil
		})
		if err != nil && !errors.Is(err, ErrRoomScoutClosed) && !errors.Is(err, ErrRoomScoutFence) {
			transport.publish(generation, "error", map[string]any{"text": "Scout tool failed", "tool": output.Name, "error": trimForStorage(err.Error(), 300)})
		}
	}()
}

// applyRoomScoutToolArgs is a closed room-scoped side-effect adapter. It never
// falls through to applyToolCallArgs: that dispatcher carries office/global
// defaults and global notification/broadcast behavior. A new named-room tool
// must receive an explicit case here and an owning-room delivery test.
func (app *kanbanBoardApp) applyRoomScoutToolArgs(ctx context.Context, scope RoomScoutScope, toolName string, args map[string]any) (map[string]any, bool, error) {
	if !app.roomScoutScopeCurrent(scope) {
		return nil, false, ErrRoomScoutFence
	}
	toolName = strings.TrimSpace(toolName)
	if !roomScoutAllowedTools[toolName] {
		return nil, false, fmt.Errorf("named-room Scout cannot use %q", toolName)
	}
	if args == nil {
		args = map[string]any{}
	}
	principal := sharedRoomRecallPrincipal(scope.RoomID, scope.SittingID)
	switch toolName {
	case "answer_memory_question", "cross_meeting_briefing", "get_meeting_detail":
		return app.applyToolCallArgsForPrincipal(toolName, args, principal)
	case "answer_room_question":
		request := canonicalizeBoardText(asString(args["request"]))
		if request == "" {
			return nil, false, fmt.Errorf("request is required")
		}
		requester := app.roomScoutCurrentSpeaker(scope)
		return map[string]any{
			"ok": true, "request": trimForStorage(request, 1000),
			"requester":      firstNonEmptyString(requester, "speaker attribution unavailable"),
			"audience":       app.participantSnapshotForRoom(scope.RoomID),
			"memoryBoundary": "shared speaker-attributed meeting and ACL-authorized company context only; no private chats, Settings imports, or private user profiles",
		}, false, nil
	case "meeting_recap":
		return app.meetingRecap(args, "", scope.RoomID)
	case "meeting_interval_recall":
		kind, err := parseSTRIDETemporalWindow(asString(args["window"]))
		if err != nil {
			return nil, false, err
		}
		result, err := app.answerSTRIDETemporalForRoom(ctx, scope, kind)
		if err != nil {
			return nil, false, err
		}
		return result.toolResult(), false, nil
	case "set_recording":
		raw, ok := args["enabled"]
		if !ok {
			return nil, false, fmt.Errorf("enabled is required")
		}
		enabled, ok := raw.(bool)
		if !ok {
			return nil, false, fmt.Errorf("enabled must be a boolean")
		}
		snapshot := app.setTranscriptRecordingInRoom(scope.RoomID, enabled, scoutParticipantName)
		broadcastRoomKanbanEvent(scope.RoomID, "participants", snapshot)
		return map[string]any{"ok": true, "enabled": enabled, "room": snapshot}, false, nil
	case "portfolio_health":
		return app.portfolioHealthTool()
	case "company_financial_snapshot":
		return app.companyFinancialSnapshotTool(args)
	case "financial_comps":
		return app.financialCompsTool(args)
	case "do_nothing":
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		default:
		}
		reason := asString(args["reason"])
		if reason == "" {
			reason = "No room update requested."
		}
		return map[string]any{"ok": true, "reason": reason}, false, nil
	default:
		// The allowlist and dispatch switch are intentionally redundant so a
		// registry edit cannot accidentally fall through to an office-global tool.
		return nil, false, fmt.Errorf("named-room Scout has no scoped handler for %q", toolName)
	}
}

// roomScoutCurrentSpeaker reads the stable server-side active-speaker state
// without consuming the transcription attribution FIFO. It lets the Realtime
// model address the human who invoked Scout while the persisted transcript
// continues to own final speaker attribution independently.
func (app *kanbanBoardApp) roomScoutCurrentSpeaker(scope RoomScoutScope) string {
	if app == nil || !scope.valid() {
		return ""
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	state := app.roomLiveLocked(scope.RoomID)
	if state.mediaGen != scope.MediaGeneration || state.mediaSittingID != strings.TrimSpace(scope.SittingID) {
		return ""
	}
	name := canonicalRoomActorName(state.activeSpeakerName)
	if name == "" || state.participantCounts[name] < 1 {
		return ""
	}
	return name
}

func (transport *openAIRoomScoutTransport) noteVoiceTranscript(transcript string) {
	if transport == nil {
		return
	}
	now := time.Now()
	transport.voiceMu.Lock()
	if transcriptStartsWithScoutWakePhrase(transcript) {
		transport.armedUntil = now.Add(scoutVoiceArmDuration)
	} else if strings.TrimSpace(transcript) != "" {
		transport.armedUntil = time.Time{}
	}
	transport.voiceMu.Unlock()
}

func (transport *openAIRoomScoutTransport) resetVoiceState() {
	if transport == nil {
		return
	}
	transport.voiceMu.Lock()
	transport.armedUntil = time.Time{}
	transport.responseActive = false
	transport.pendingSpeech = false
	transport.pendingSession = nil
	transport.pendingGeneration = 0
	transport.transcriptionOwnership = make(map[string]bool)
	transport.transcriptionFIFO = nil
	transport.voiceMu.Unlock()
}

func (transport *openAIRoomScoutTransport) rememberTranscriptionOwnership(itemID string, sourceOwned bool) {
	if transport == nil {
		return
	}
	transport.voiceMu.Lock()
	defer transport.voiceMu.Unlock()
	if itemID = strings.TrimSpace(itemID); itemID != "" {
		if transport.transcriptionOwnership == nil {
			transport.transcriptionOwnership = make(map[string]bool)
		}
		transport.transcriptionOwnership[itemID] = sourceOwned
		return
	}
	transport.transcriptionFIFO = append(transport.transcriptionFIFO, sourceOwned)
}

func (transport *openAIRoomScoutTransport) takeTranscriptionOwnership(itemID string) (bool, bool) {
	if transport == nil {
		return false, false
	}
	transport.voiceMu.Lock()
	defer transport.voiceMu.Unlock()
	if itemID = strings.TrimSpace(itemID); itemID != "" {
		owned, ok := transport.transcriptionOwnership[itemID]
		delete(transport.transcriptionOwnership, itemID)
		return owned, ok
	}
	if len(transport.transcriptionFIFO) == 0 {
		return false, false
	}
	owned := transport.transcriptionFIFO[0]
	transport.transcriptionFIFO = append([]bool(nil), transport.transcriptionFIFO[1:]...)
	return owned, true
}

func roomScoutFallbackOwnsTranscription(sourceOwned, found bool) bool {
	return found && !sourceOwned
}

func (transport *openAIRoomScoutTransport) voiceArmed() bool {
	if transport == nil {
		return false
	}
	transport.voiceMu.Lock()
	defer transport.voiceMu.Unlock()
	return !transport.armedUntil.IsZero() && !time.Now().After(transport.armedUntil)
}

func (transport *openAIRoomScoutTransport) shouldSpeakAfterTool(toolName string, result map[string]any, changed, armedAtStart bool) bool {
	if transport == nil {
		return false
	}
	// Tool selection is the semantic participation gate. do_nothing is the
	// model's explicit decision that the room was not talking to Scout; every
	// other closed, room-scoped tool represents a deliberate Scout turn.
	transport.voiceMu.Lock()
	shouldSpeak := toolName != "do_nothing" && scoutToolShouldSpeak(toolName, result, changed, true)
	if shouldSpeak {
		transport.armedUntil = time.Time{}
	}
	transport.voiceMu.Unlock()
	return shouldSpeak
}

func (transport *openAIRoomScoutTransport) requestSpokenResponse(session roomScoutProviderSession, generation uint64) error {
	if transport == nil || !transport.accepts(generation) {
		return ErrRoomScoutFence
	}
	transport.voiceMu.Lock()
	if transport.responseActive {
		transport.pendingSpeech = true
		transport.pendingSession = session
		transport.pendingGeneration = generation
		transport.voiceMu.Unlock()
		return nil
	}
	transport.voiceMu.Unlock()
	return sendRoomScoutSpokenResponse(session)
}

func (transport *openAIRoomScoutTransport) finishProviderResponse() {
	if transport == nil {
		return
	}
	transport.voiceMu.Lock()
	transport.responseActive = false
	pending := transport.pendingSpeech
	session := transport.pendingSession
	generation := transport.pendingGeneration
	transport.pendingSpeech = false
	transport.pendingSession = nil
	transport.pendingGeneration = 0
	transport.voiceMu.Unlock()
	if pending && transport.accepts(generation) {
		if err := sendRoomScoutSpokenResponse(session); err != nil {
			transport.publish(generation, "error", map[string]any{"text": "Scout could not answer aloud", "error": trimForStorage(err.Error(), 300)})
		}
	}
}

func sendRoomScoutSpokenResponse(session roomScoutProviderSession) error {
	if session == nil {
		return errRoomScoutProviderUnavailable
	}
	return session.SendEvent(map[string]any{
		"type": "response.create",
		"response": map[string]any{
			"output_modalities": []string{"audio"}, "tool_choice": "none",
			"instructions": roomScoutSpokenResponseInstructions(),
		},
	})
}

func roomScoutSpokenResponseInstructions() string {
	return strings.Join([]string{
		"Speak to the room as Scout, a visible invited participant.",
		"Answer the participant's current question or request naturally and directly using the conversation and tool result.",
		"No wake phrase is required and you must not mention one.",
		"Never promise future work unless the tool result contains a durable receipt. If no receipt exists, say no work is scheduled.",
		"Do not call another tool in this continuation.",
		"Keep routine answers concise; ask one clear follow-up only when essential.",
	}, " ")
}

func (app *kanbanBoardApp) transcriptionLaneConnectedForRoom(roomID string) bool {
	roomID = normalizeRoomID(roomID)
	if roomID == officeRoomID {
		return app.transcriptionLaneConnected()
	}
	app.mu.Lock()
	lane := app.roomLiveLocked(roomID).lane
	app.mu.Unlock()
	return lane != nil && lane.isConnected()
}

func (app *kanbanBoardApp) claimTranscriptionSourceTurnForRoom(roomID string) bool {
	roomID = normalizeRoomID(roomID)
	app.mu.Lock()
	var lane *meetingTranscriptionLane
	if roomID == officeRoomID {
		lane = app.transcriptLane
	} else {
		lane = app.roomLiveLocked(roomID).lane
	}
	app.mu.Unlock()
	return lane != nil && lane.claimSourceTurnOwnership()
}

func (app *kanbanBoardApp) recordRoomScoutResponseUsage(roomID, model string, event kanbanRealtimeEvent) {
	if event.Response == nil {
		return
	}
	entry := llmUsageEntry{Provider: providerOpenAI, Model: model, Seat: seatVoiceRoom, RoomID: normalizeRoomID(roomID)}
	if realtimeUsageTokens(event.Response.Usage, &entry) {
		recordLLMUsage(entry)
	}
}

func (app *kanbanBoardApp) recordRoomScoutTranscriptionUsage(roomID string, event kanbanRealtimeEvent) {
	entry := llmUsageEntry{Provider: providerOpenAI, Model: realtimeTranscriptionModel(), Seat: seatTranscriptionSession, RoomID: normalizeRoomID(roomID)}
	if realtimeUsageTokens(event.Usage, &entry) {
		recordLLMUsage(entry)
	}
}

func (transport *openAIRoomScoutTransport) forwardOutputTrack(ctx context.Context, generation uint64, track *webrtc.TrackRemote) {
	if track == nil || track.Kind() != webrtc.RTPCodecTypeAudio || !transport.accepts(generation) {
		return
	}
	transport.mu.Lock()
	trackLocal := transport.outputTrack
	transport.mu.Unlock()
	if trackLocal == nil {
		return
	}
	transport.publish(generation, "audio", map[string]any{"text": "Scout voice connected", "trackId": trackLocal.ID()})
	for {
		packet, _, err := track.ReadRTP()
		if err != nil {
			return
		}
		// ReadRTP is a blocking seam. Revalidate both provider and sitting
		// generations after it returns, immediately before registry publication.
		if !transport.accepts(generation) {
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := transport.writeOutputPacket(packet.Payload, true); err != nil {
			return
		}
	}
}

// addPersistentRoomScoutOutputTrack registers one Opus publication for the
// entire invitation. A bounded silence keepalive binds it into every current
// subscriber before the first provider response and keeps the same receiver
// alive across answers and provider restarts.
func addPersistentRoomScoutOutputTrack(scope RoomScoutScope) (*webrtc.TrackLocalStaticRTP, error) {
	if !scope.valid() {
		return nil, ErrRoomScoutFence
	}
	trackID := fmt.Sprintf("scout:%s:%d:audio", scope.RoomID, scope.MediaGeneration)
	streamID := fmt.Sprintf("scout:%s", scope.RoomID)
	trackLocal, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeOpus, ClockRate: roomAudioSampleRate, Channels: realtimeAudioChannels,
	}, trackID, streamID)
	if err != nil {
		return nil, err
	}
	listLock.Lock()
	if trackLocals == nil {
		trackLocals = map[string]*webrtc.TrackLocalStaticRTP{}
	}
	if trackParticipants == nil {
		trackParticipants = map[string]string{}
	}
	if trackParticipantSessions == nil {
		trackParticipantSessions = map[string]string{}
	}
	if trackRooms == nil {
		trackRooms = map[string]string{}
	}
	if trackSourceIDs == nil {
		trackSourceIDs = map[string]string{}
	}
	if trackLayerRIDs == nil {
		trackLayerRIDs = map[string]string{}
	}
	if trackLayerGroups == nil {
		trackLayerGroups = map[string]string{}
	}
	if trackMediaOwners == nil {
		trackMediaOwners = map[string]trackMediaOwner{}
	}
	trackLocals[trackID] = trackLocal
	trackParticipants[trackID] = scoutParticipantName
	trackParticipantSessions[trackID] = fmt.Sprintf("scout:%s", scope.SittingID)
	trackRooms[trackID] = scope.RoomID
	trackSourceIDs[trackID] = trackID
	trackLayerRIDs[trackID] = ""
	trackLayerGroups[trackID] = fmt.Sprintf("scout:%s:%s", scope.RoomID, scope.SittingID)
	trackMediaOwners[trackID] = trackMediaOwner{track: trackLocal, generation: scope.MediaGeneration, sittingID: scope.SittingID}
	totalTracks, audioTracks, videoTracks := forwardedTrackCountsLocked()
	listLock.Unlock()
	log.Infof("room_scout_track_added room=%s sitting=%s media_gen=%d track_id=%s persistent=true total_tracks=%d audio_tracks=%d video_tracks=%d",
		scope.RoomID, scope.SittingID, scope.MediaGeneration, trackID, totalTracks, audioTracks, videoTracks)
	return trackLocal, nil
}

func (transport *openAIRoomScoutTransport) writeOutputPacket(payload []byte, speech bool) error {
	if transport == nil || len(payload) == 0 {
		return nil
	}
	transport.outputMu.Lock()
	defer transport.outputMu.Unlock()
	transport.mu.Lock()
	track := transport.outputTrack
	closed := transport.closed
	transport.mu.Unlock()
	if closed || track == nil {
		return ErrRoomScoutClosed
	}
	transport.outputSeq++
	transport.outputTS += uint32(roomAudioMixFrameSize)
	packet := &rtp.Packet{Header: rtp.Header{
		Version: 2, SequenceNumber: transport.outputSeq, Timestamp: transport.outputTS,
	}, Payload: append([]byte(nil), payload...)}
	if err := track.WriteRTP(packet); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		return err
	}
	transport.outputCount++
	if speech {
		transport.lastSpeech = time.Now()
	}
	return nil
}

func (transport *openAIRoomScoutTransport) runOutputKeepalive() {
	// RFC 6716's canonical 20 ms Opus silence packet. Sending it only when no
	// provider packet arrived in the preceding two frames pre-binds the stable
	// receiver without doubling packet rate during speech.
	ticker := time.NewTicker(roomAudioMixInterval)
	defer ticker.Stop()
	for {
		select {
		case <-transport.ctx.Done():
			return
		case now := <-ticker.C:
			transport.outputMu.Lock()
			quiet := transport.lastSpeech.IsZero() || now.Sub(transport.lastSpeech) >= 2*roomAudioMixInterval
			transport.outputMu.Unlock()
			if quiet {
				_ = transport.writeOutputPacket([]byte{0xF8, 0xFF, 0xFE}, false)
			}
		}
	}
}
