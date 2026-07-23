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

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const (
	roomScoutProactiveRestartAfter = 55 * time.Minute
	roomScoutDisconnectedGrace     = 5 * time.Second
	roomScoutRestartRetryAfter     = 2 * time.Second
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

	mu         sync.Mutex
	session    roomScoutProviderSession
	generation uint64
	closed     bool
	restarts   chan roomScoutRestartRequest

	voiceMu           sync.Mutex
	armedUntil        time.Time
	responseActive    bool
	pendingSpeech     bool
	pendingSession    roomScoutProviderSession
	pendingGeneration uint64
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
		generation: 1,
	}

	session, err := dial(transportCtx, transport, 1)
	if err != nil {
		cancel()
		return nil, err
	}
	if session == nil {
		cancel()
		return nil, fmt.Errorf("%w: dialer returned no session", errRoomScoutProviderUnavailable)
	}
	transport.mu.Lock()
	transport.session = session
	transport.mu.Unlock()
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
	transport.cancel()
	transport.mu.Unlock()
	transport.resetVoiceState()
	var err error
	if session != nil {
		err = session.Close()
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
	if !transport.accepts(generation) {
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
	if transport.closed || transport.generation != request.generation {
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
		time.AfterFunc(roomScoutRestartRetryAfter, func() {
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
	// Match the proven office wake discipline: every ambient turn must first
	// resolve through a tool (usually do_nothing). The transport requests a
	// tool_choice=none spoken continuation only for a server-observed wake turn.
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
	"financial_comps": true, "meeting_recap": true,
	"cross_meeting_briefing": true, "get_meeting_detail": true,
	"do_nothing": true,
}

func (app *kanbanBoardApp) roomScoutTools() []map[string]any {
	all := app.realtimeRoomVoiceTools()
	tools := make([]map[string]any, 0, len(all))
	for _, tool := range all {
		if roomScoutAllowedTools[asString(tool["name"])] {
			tools = append(tools, tool)
		}
	}
	return tools
}

func (app *kanbanBoardApp) roomScoutSessionInstructions(scope RoomScoutScope) string {
	return app.sessionInstructions() + "\n\n" + strings.Join([]string{
		"# Named-room authority",
		fmt.Sprintf("This provider session is bound by the server to room %q, sitting %q, media generation %d.", scope.RoomID, scope.SittingID, scope.MediaGeneration),
		"Never infer, select, or change that room from user speech or tool arguments. Recall, recap, recording, proposals, artifacts, and launched work are server-scoped to this sitting.",
		"Tools omitted from this session are intentionally unavailable because their current implementation is office-global or user-private. Do not claim those actions completed.",
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
		if !transport.app.transcriptionLaneConnectedForRoom(transport.scope.RoomID) {
			transport.app.rememberRoomScoutTranscript(transport.scope, event, "scout_realtime", transport.model)
		}
	case "conversation.item.input_audio_transcription.failed":
		if !transport.app.transcriptionLaneConnectedForRoom(transport.scope.RoomID) {
			transport.app.popRoomScoutAttribution(transport.scope)
		}
	case "conversation.item.input_audio_transcription.delta":
		if text := canonicalizeBoardText(event.Delta); text != "" {
			transport.publish(generation, "transcript", map[string]any{"text": "hearing: " + text, "eventType": event.Type})
		}
	case "input_audio_buffer.speech_started":
		if !transport.app.transcriptionLaneConnectedForRoom(transport.scope.RoomID) {
			transport.app.noteRoomScoutSpeechStarted(transport.scope)
		}
		transport.publish(generation, "audio", map[string]any{"text": "Scout detected speech", "voiceState": "hearing"})
	case "input_audio_buffer.speech_stopped":
		if !transport.app.transcriptionLaneConnectedForRoom(transport.scope.RoomID) {
			transport.app.noteRoomScoutSpeechStopped(transport.scope)
		}
	case "input_audio_buffer.committed":
		contributorFences := transport.app.takeRoomScoutContributorFences(transport.scope)
		if !transport.app.transcriptionLaneConnectedForRoom(transport.scope.RoomID) {
			transport.app.freezeRoomScoutAttributionWithConsent(transport.scope, contributorFences)
		}
	case "response.created":
		transport.voiceMu.Lock()
		transport.responseActive = true
		transport.voiceMu.Unlock()
	case "response.output_audio_transcript.done", "response.output_text.done":
		if text := canonicalizeBoardText(firstNonEmptyString(event.Transcript, event.Text)); text != "" {
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
	case "meeting_recap":
		return app.meetingRecap(args, "", scope.RoomID)
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
	transport.voiceMu.Unlock()
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
	transport.voiceMu.Lock()
	armed := armedAtStart || (!transport.armedUntil.IsZero() && !time.Now().After(transport.armedUntil))
	if !armed {
		transport.voiceMu.Unlock()
		return false
	}
	shouldSpeak := toolName == "do_nothing" || scoutToolShouldSpeak(toolName, result, changed, true)
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
			"instructions": scoutSpokenResponseInstructions(),
		},
	})
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
	trackLocal, err := addRoomScoutOutputTrack(transport.scope, generation, track)
	if err != nil {
		transport.publish(generation, "error", map[string]any{"text": "Scout output audio unavailable"})
		return
	}
	defer removeTrack(trackLocal)
	// Registration and logging are intentionally outside the transport lock.
	// Revalidate before publishing the track: a room teardown or provider
	// replacement racing registration must leave no stale participant event.
	if !transport.accepts(generation) {
		return
	}
	broadcastRoomKanbanEvent(transport.scope.RoomID, "participant_track", map[string]any{
		"name": scoutParticipantName, "kind": track.Kind().String(), "trackId": trackLocal.ID(),
		"sourceTrackId": track.ID(), "streamId": track.StreamID(), "roomId": transport.scope.RoomID,
	})
	requestRoomMediaCommandForGeneration(transport.scope.RoomID, transport.scope.MediaGeneration, roomMediaCommandTrack)
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
		packet.Extension = false
		packet.Extensions = nil
		if err := trackLocal.WriteRTP(packet); err != nil {
			return
		}
	}
}

func addRoomScoutOutputTrack(scope RoomScoutScope, providerGeneration uint64, track *webrtc.TrackRemote) (*webrtc.TrackLocalStaticRTP, error) {
	if track == nil || !scope.valid() {
		return nil, ErrRoomScoutFence
	}
	trackID := fmt.Sprintf("scout:%s:%d:%d:%s", scope.RoomID, scope.MediaGeneration, providerGeneration, forwardedRemoteTrackID(track))
	trackLocal, err := webrtc.NewTrackLocalStaticRTP(track.Codec().RTPCodecCapability, trackID, track.StreamID())
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
	trackParticipantSessions[trackID] = fmt.Sprintf("scout:%s:%d", scope.SittingID, providerGeneration)
	trackRooms[trackID] = scope.RoomID
	trackSourceIDs[trackID] = track.ID()
	trackLayerRIDs[trackID] = track.RID()
	trackLayerGroups[trackID] = fmt.Sprintf("scout:%s:%s:%d", scope.RoomID, scope.SittingID, providerGeneration)
	trackMediaOwners[trackID] = trackMediaOwner{track: trackLocal, generation: scope.MediaGeneration, sittingID: scope.SittingID}
	totalTracks, audioTracks, videoTracks := forwardedTrackCountsLocked()
	listLock.Unlock()
	log.Infof("room_scout_track_added room=%s sitting=%s media_gen=%d provider_gen=%d track_id=%s total_tracks=%d audio_tracks=%d video_tracks=%d",
		scope.RoomID, scope.SittingID, scope.MediaGeneration, providerGeneration, trackID, totalTracks, audioTracks, videoTracks)
	return trackLocal, nil
}
