package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

type MeetingSpecialistProviderReceipt struct {
	QualificationSubjectDigest string                             `json:"qualificationSubjectDigest,omitempty"`
	BindingDigest              string                             `json:"bindingDigest"`
	RequestDigest              string                             `json:"requestDigest"`
	SessionIDHash              string                             `json:"sessionIdHash,omitempty"`
	Model                      string                             `json:"model"`
	ReasoningEffort            string                             `json:"reasoningEffort"`
	EventDigest                string                             `json:"eventDigest"`
	EventCount                 int                                `json:"eventCount"`
	UsageDigest                string                             `json:"usageDigest,omitempty"`
	UsageStatus                string                             `json:"usageStatus,omitempty"`
	TerminalEventHash          string                             `json:"terminalEventHash,omitempty"`
	TerminalStatus             string                             `json:"terminalStatus,omitempty"`
	SessionFailureHash         string                             `json:"sessionFailureHash,omitempty"`
	InputTokens                int64                              `json:"inputTokens,omitempty"`
	OutputTokens               int64                              `json:"outputTokens,omitempty"`
	OutputAudioTokens          int64                              `json:"outputAudioTokens,omitempty"`
	ReconciledCostCent         int64                              `json:"reconciledCostCents,omitempty"`
	InputAudioSamples          int64                              `json:"inputAudioSamples,omitempty"`
	InputSampleLimit           int64                              `json:"inputSampleLimit,omitempty"`
	InputWorstCaseTokens       int64                              `json:"inputWorstCaseTokensPerTurn,omitempty"`
	InputMode                  MeetingSpecialistRealtimeInputMode `json:"inputMode,omitempty"`
	ProtocolSource             string                             `json:"protocolSource"`
	ModelSource                string                             `json:"modelSource"`
	ContractDigest             string                             `json:"contractDigest"`
}

type openAIMeetingSpecialistProvider struct {
	config  MeetingSpecialistRealtimeConfig
	launch  MeetingSpecialistLaunch
	conn    meetingSpecialistRealtimeConn
	ctx     context.Context
	cancel  context.CancelFunc
	binding string

	writeMu sync.Mutex
	mu      sync.Mutex
	hooks   MeetingSpecialistProviderHooks
	closed  bool
	briefed bool
	done    chan struct{}

	sessionID         string
	activeFloor       *MeetingAgentFloorLease
	activeResponse    string
	activeItem        string
	cancelledResponse string
	cancelledItem     string
	cancelPending     bool
	usageUnreconciled bool
	terminalFenced    bool
	humanPCMSamples   int64
	inputSampleLimit  int64
	activeInputLimit  int64
	activeOutputLimit int64
	activeCostLimit   int64
	audioBuffer       []byte
	audioBytes        int64
	publishedAudioMS  int64
	eventHashes       []string
	usageHashes       []string
	seenEventIDs      map[string]struct{}
	receipt           MeetingSpecialistProviderReceipt
	failureOnce       sync.Once
	readerStarted     bool
}

// NewMeetingSpecialistRealtimeProviderFactory creates a server-side factory.
// A disabled or incomplete config returns a callable fail-closed factory; it
// never silently substitutes another model, endpoint, effort, voice, tool, or
// credential source.
func NewMeetingSpecialistRealtimeProviderFactory(config MeetingSpecialistRealtimeConfig) MeetingSpecialistProviderFactory {
	config = config.normalized()
	return func(parent context.Context, launch MeetingSpecialistLaunch) (MeetingSpecialistProvider, error) {
		if err := config.validate(launch); err != nil {
			return nil, err
		}
		// Only direct PCM has a qualified wire implementation in this adapter.
		// Bounded transcript accounting is intentionally usable by admission
		// planning but cannot silently fall through to an unreviewed transport.
		if config.InputMode != MeetingSpecialistRealtimeInputDirectPCM {
			return nil, ErrMeetingSpecialistProviderConfig
		}
		invocationConfig := config
		if launchAudioBytes := launch.Context.AudioBudgetSeconds * meetingSpecialistRealtimeSampleRate * 2; launchAudioBytes < invocationConfig.MaxAudioBytes {
			invocationConfig.MaxAudioBytes = launchAudioBytes
		}
		if parent == nil {
			parent = context.Background()
		}
		ttl := time.Duration(launch.Context.TimeBudgetSeconds) * time.Second
		if launch.Policy.SessionTTL < ttl {
			ttl = launch.Policy.SessionTTL
		}
		if invitationTTL := launch.Invitation.ExpiresAt.Sub(config.Now().UTC()); invitationTTL < ttl {
			ttl = invitationTTL
		}
		if ttl <= 0 {
			return nil, ErrMeetingSpecialistUnauthorized
		}
		runtimeOwnsLifetime := meetingSpecialistRuntimeOwnsLifetime(parent)
		var ctx context.Context
		var cancel context.CancelFunc
		if runtimeOwnsLifetime {
			ctx, cancel = context.WithCancel(parent)
		} else {
			ctx, cancel = context.WithTimeout(parent, ttl)
		}
		dial := config.dial
		if dial == nil {
			dial = dialOpenAIMeetingSpecialistRealtime
		}
		headers := make(http.Header)
		headers.Set("Authorization", "Bearer "+config.APIKey)
		if config.SafetyIdentifier != "" {
			headers.Set("OpenAI-Safety-Identifier", config.SafetyIdentifier)
		}
		conn, err := dial(ctx, meetingSpecialistRealtimeEndpoint, headers)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("%w: websocket dial", ErrMeetingSpecialistProviderProtocol)
		}
		if conn == nil {
			cancel()
			return nil, ErrMeetingSpecialistProviderProtocol
		}
		conn.SetReadLimit(config.MaxEventBytes + 1)
		launchWithoutReceipt := launch
		launchWithoutReceipt.CapabilityReceipt = ""
		binding := workDigest(struct {
			Launch                 MeetingSpecialistLaunch
			Model                  string
			ReasoningEffort        string
			Voice                  string
			MaxOutputTokens        int64
			InputMode              MeetingSpecialistRealtimeInputMode
			MaxInputTokensPerTurn  int64
			ProviderEndpoint       string
			SafetyIdentifierDigest string
		}{launchWithoutReceipt, config.Model, config.ReasoningEffort, config.Voice, config.MaxOutputTokens, config.InputMode, config.MaxInputTokensPerTurn, "/v1/realtime?model=" + config.Model, optionalMeetingSpecialistDigest(config.SafetyIdentifier)})
		contractDigest := workDigest(meetingSpecialistRealtimeContractDeclaration())
		inputSampleLimit := launch.Policy.AudioBudgetSecond * meetingSpecialistRealtimeSampleRate
		provider := &openAIMeetingSpecialistProvider{
			config: invocationConfig, launch: launchWithoutReceipt, conn: conn, ctx: ctx, cancel: cancel, binding: binding,
			done: make(chan struct{}), seenEventIDs: map[string]struct{}{}, inputSampleLimit: inputSampleLimit,
			receipt: MeetingSpecialistProviderReceipt{
				BindingDigest: binding, Model: config.Model, ReasoningEffort: config.ReasoningEffort, EventDigest: sha256Hex(nil),
				InputSampleLimit: inputSampleLimit, InputWorstCaseTokens: meetingSpecialistRealtimeContextWindowTokens, InputMode: config.InputMode,
				ProtocolSource: meetingSpecialistRealtimeProtocolSource, ModelSource: meetingSpecialistRealtimeModelSource, ContractDigest: contractDigest,
			},
		}
		if !runtimeOwnsLifetime {
			go func() {
				select {
				case <-ctx.Done():
					provider.mu.Lock()
					closed := provider.closed
					provider.mu.Unlock()
					if !closed {
						provider.fail("session_deadline")
					}
				case <-provider.done:
				}
			}()
		}
		return provider, nil
	}
}

func optionalMeetingSpecialistDigest(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return sha256Hex([]byte(value))
}

func meetingSpecialistRealtimeContractDeclaration() any {
	return struct {
		ProtocolSource, ModelSource, Endpoint, Model, InputFormat, OutputFormat, InputAccounting string
		SessionEvents, InputEvents, OutputEvents, CancelEvents                                   []string
	}{
		meetingSpecialistRealtimeProtocolSource, meetingSpecialistRealtimeModelSource,
		"/v1/realtime?model=gpt-realtime-2.1", meetingSpecialistRealtimeModel, "audio/pcm@24000", "audio/pcm@24000", "direct_pcm_reserves_full_128k_context",
		[]string{"session.created", "conversation.created (optional)", "session.updated"},
		[]string{"input_audio_buffer.append", "input_audio_buffer.commit", "response.create"},
		[]string{"conversation.item.added", "conversation.item.done", "response.created", "response.output_item.added", "response.content_part.added", "response.output_audio.delta", "response.output_audio.done", "response.output_audio_transcript.delta", "response.output_audio_transcript.done", "response.content_part.done", "response.output_item.done", "response.done", "rate_limits.updated"},
		[]string{"response.cancel", "conversation.item.truncate", "input_audio_buffer.clear"},
	}
}

func dialOpenAIMeetingSpecialistRealtime(ctx context.Context, endpoint string, headers http.Header) (meetingSpecialistRealtimeConn, error) {
	dialer := *aiProviderRealtimeWebSocketDialer()
	dialer.Proxy = nil
	dialer.HandshakeTimeout = 15 * time.Second
	conn, response, err := dialer.DialContext(ctx, endpoint, headers)
	if response != nil && response.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func (provider *openAIMeetingSpecialistProvider) BindMeetingSpecialistProviderHooks(hooks MeetingSpecialistProviderHooks) error {
	if provider == nil || hooks.PublishAudio == nil || hooks.CompleteTurn == nil || hooks.FailSession == nil {
		return ErrMeetingSpecialistProviderConfig
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.closed || provider.briefed {
		return ErrMeetingSpecialistProviderProtocol
	}
	provider.hooks = hooks
	return nil
}

func (provider *openAIMeetingSpecialistProvider) Brief(ctx context.Context, envelope MeetingSpecialistContextEnvelope) error {
	if provider == nil || envelope.Validate() != nil || envelope.ContextDigest != provider.launch.Context.ContextDigest {
		return ErrMeetingSpecialistProviderProtocol
	}
	provider.mu.Lock()
	if provider.closed || provider.briefed || provider.hooks.PublishAudio == nil {
		provider.mu.Unlock()
		return ErrMeetingSpecialistProviderProtocol
	}
	provider.mu.Unlock()
	brief, err := provider.config.ResolveBrief(ctx, provider.launch)
	if err != nil || validateMeetingSpecialistRealtimeBrief(provider.launch, brief) != nil {
		return ErrMeetingSpecialistProviderProtocol
	}
	contextJSON, err := canonicalJSON(struct {
		Envelope MeetingSpecialistContextEnvelope `json:"envelope"`
		Brief    MeetingSpecialistRealtimeBrief   `json:"brief"`
	}{envelope, brief})
	if err != nil || len(contextJSON) == 0 || len(contextJSON) > provider.config.MaxContextBytes {
		return ErrMeetingSpecialistProviderConfig
	}
	created, err := provider.readBriefEvent("session.created")
	if err != nil {
		return err
	}
	var createdEvent struct {
		Type    string `json:"type"`
		EventID string `json:"event_id"`
		Session struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			Model string `json:"model"`
		} `json:"session"`
	}
	if json.Unmarshal(created, &createdEvent) != nil || createdEvent.Session.ID == "" || createdEvent.Session.Type != "realtime" ||
		(createdEvent.Session.Model != "" && createdEvent.Session.Model != provider.config.Model) {
		return ErrMeetingSpecialistProviderProtocol
	}
	provider.mu.Lock()
	provider.sessionID = createdEvent.Session.ID
	provider.receipt.SessionIDHash = sha256Hex([]byte(createdEvent.Session.ID))
	provider.mu.Unlock()
	request := provider.sessionUpdate(contextJSON)
	requestRaw, _ := canonicalJSON(request)
	provider.mu.Lock()
	provider.receipt.RequestDigest = sha256Hex(requestRaw)
	provider.mu.Unlock()
	if err := provider.write(request); err != nil {
		return err
	}
	updated, err := provider.readSessionUpdatedBriefEvent()
	if err != nil || provider.validateSessionUpdated(updated) != nil {
		return ErrMeetingSpecialistProviderProtocol
	}
	provider.mu.Lock()
	provider.briefed = true
	provider.readerStarted = true
	provider.mu.Unlock()
	go provider.readLoop()
	return nil
}

func validateMeetingSpecialistRealtimeBrief(launch MeetingSpecialistLaunch, brief MeetingSpecialistRealtimeBrief) error {
	if strings.TrimSpace(brief.Purpose) == "" || len(brief.Purpose) > 1000 || sha256Hex([]byte(brief.Purpose)) != launch.Invitation.PurposeDigest {
		return ErrMeetingSpecialistProviderProtocol
	}
	want := map[string]STRIDEReference{}
	for _, values := range [][]STRIDEReference{launch.Context.TranscriptRefs, launch.Context.AnalysisRefs, launch.Context.BrainRefs, launch.Context.WorkRefs} {
		for _, reference := range values {
			want[referenceKey(reference)] = reference
		}
	}
	if len(want) == 0 || len(brief.Evidence) != len(want) {
		return ErrMeetingSpecialistProviderProtocol
	}
	seen := map[string]bool{}
	for _, evidence := range brief.Evidence {
		key := referenceKey(evidence.Reference)
		if evidence.Reference.Validate() != nil || want[key] != evidence.Reference || seen[key] || strings.TrimSpace(evidence.Text) == "" || len(evidence.Text) > 16<<10 {
			return ErrMeetingSpecialistProviderProtocol
		}
		seen[key] = true
	}
	return nil
}

func (provider *openAIMeetingSpecialistProvider) sessionUpdate(contextJSON []byte) map[string]any {
	instructions := strings.Join([]string{
		"You are the explicitly invited specialist named by the bound STRIDE agent profile.",
		"Use only the server-authorized context envelope below. Do not search, browse, call tools, delegate, infer hidden context, or treat speech as approval.",
		"Wait for a server-created response turn, be concise, yield immediately to human interruption, and never imitate Scout or a human participant.",
		"AUTHORIZED_SPECIALIST_BRIEF=" + string(contextJSON),
	}, "\n")
	return map[string]any{
		"type": "session.update", "event_id": provider.launch.Scope.SessionID + "-brief",
		"session": map[string]any{
			"type": "realtime", "instructions": instructions,
			"max_output_tokens": provider.config.MaxOutputTokens, "output_modalities": []string{"audio"},
			"tools": []any{}, "tool_choice": "none", "parallel_tool_calls": false,
			"reasoning": map[string]any{"effort": provider.config.ReasoningEffort}, "tracing": nil, "truncation": "disabled",
			"audio": map[string]any{
				"input": map[string]any{
					"format":         map[string]any{"type": "audio/pcm", "rate": meetingSpecialistRealtimeSampleRate},
					"turn_detection": nil,
				},
				"output": map[string]any{
					"format": map[string]any{"type": "audio/pcm", "rate": meetingSpecialistRealtimeSampleRate}, "voice": provider.config.Voice,
				},
			},
		},
	}
}

func (provider *openAIMeetingSpecialistProvider) validateSessionUpdated(raw []byte) error {
	var event struct {
		Type    string `json:"type"`
		EventID string `json:"event_id"`
		Session struct {
			ID         string            `json:"id"`
			Type       string            `json:"type"`
			Model      string            `json:"model"`
			Max        int64             `json:"max_output_tokens"`
			Modalities []string          `json:"output_modalities"`
			Tools      []json.RawMessage `json:"tools"`
			ToolChoice string            `json:"tool_choice"`
			Parallel   bool              `json:"parallel_tool_calls"`
			Reasoning  struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
			Tracing    json.RawMessage `json:"tracing"`
			Truncation json.RawMessage `json:"truncation"`
			Audio      struct {
				Input struct {
					Format struct {
						Type string `json:"type"`
						Rate int    `json:"rate"`
					} `json:"format"`
					TurnDetection json.RawMessage `json:"turn_detection"`
				} `json:"input"`
				Output struct {
					Voice  string `json:"voice"`
					Format struct {
						Type string `json:"type"`
						Rate int    `json:"rate"`
					} `json:"format"`
				} `json:"output"`
			} `json:"audio"`
		} `json:"session"`
	}
	if json.Unmarshal(raw, &event) != nil || event.Type != "session.updated" || event.EventID == "" || event.Session.ID != provider.sessionID ||
		event.Session.Type != "realtime" || event.Session.Model != provider.config.Model || event.Session.Max != provider.config.MaxOutputTokens ||
		len(event.Session.Modalities) != 1 || event.Session.Modalities[0] != "audio" || len(event.Session.Tools) != 0 || event.Session.ToolChoice != "none" || event.Session.Parallel ||
		event.Session.Reasoning.Effort != provider.config.ReasoningEffort || !jsonNull(event.Session.Tracing) || string(bytes.TrimSpace(event.Session.Truncation)) != `"disabled"` ||
		event.Session.Audio.Input.Format.Type != "audio/pcm" || event.Session.Audio.Input.Format.Rate != meetingSpecialistRealtimeSampleRate || !jsonNull(event.Session.Audio.Input.TurnDetection) ||
		event.Session.Audio.Output.Format.Type != "audio/pcm" || event.Session.Audio.Output.Format.Rate != meetingSpecialistRealtimeSampleRate || event.Session.Audio.Output.Voice != provider.config.Voice {
		return ErrMeetingSpecialistProviderProtocol
	}
	return nil
}

func jsonNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func (provider *openAIMeetingSpecialistProvider) readBriefEvent(want string) ([]byte, error) {
	raw, typ, err := provider.readBriefEventAny()
	if err != nil || typ != want {
		return nil, ErrMeetingSpecialistProviderProtocol
	}
	return raw, nil
}

func (provider *openAIMeetingSpecialistProvider) readSessionUpdatedBriefEvent() ([]byte, error) {
	raw, typ, err := provider.readBriefEventAny()
	if err != nil {
		return nil, err
	}
	if typ == "conversation.created" {
		var event struct {
			Conversation struct {
				ID     string `json:"id"`
				Object string `json:"object"`
			} `json:"conversation"`
		}
		if json.Unmarshal(raw, &event) != nil || event.Conversation.ID == "" || event.Conversation.Object != "realtime.conversation" {
			return nil, ErrMeetingSpecialistProviderProtocol
		}
		raw, typ, err = provider.readBriefEventAny()
		if err != nil {
			return nil, err
		}
	}
	if typ != "session.updated" {
		return nil, ErrMeetingSpecialistProviderProtocol
	}
	return raw, nil
}

func (provider *openAIMeetingSpecialistProvider) readBriefEventAny() ([]byte, string, error) {
	_, raw, err := provider.conn.ReadMessage()
	if err != nil || int64(len(raw)) > provider.config.MaxEventBytes {
		return nil, "", ErrMeetingSpecialistProviderProtocol
	}
	typ, err := provider.recordEvent(raw)
	if err != nil {
		return nil, "", ErrMeetingSpecialistProviderProtocol
	}
	return raw, typ, nil
}

func (provider *openAIMeetingSpecialistProvider) WriteHumanPCM(_ context.Context, highWater uint64, pcm []int16) error {
	if provider == nil || highWater == 0 || len(pcm) == 0 || len(pcm) > meetingSpecialistRealtimeSampleRate*10 {
		return ErrMeetingSpecialistProviderProtocol
	}
	provider.mu.Lock()
	if provider.closed || provider.usageUnreconciled || provider.terminalFenced {
		provider.mu.Unlock()
		return ErrMeetingSpecialistClosed
	}
	if !provider.briefed || provider.activeFloor != nil || provider.cancelPending || provider.cancelledResponse != "" {
		provider.mu.Unlock()
		return ErrMeetingSpecialistProviderProtocol
	}
	if provider.inputSampleLimit <= 0 || provider.humanPCMSamples+int64(len(pcm)) > provider.inputSampleLimit {
		provider.mu.Unlock()
		return ErrMeetingSpecialistProviderBudget
	}
	provider.humanPCMSamples += int64(len(pcm))
	provider.receipt.InputAudioSamples = provider.humanPCMSamples
	provider.mu.Unlock()
	raw := make([]byte, len(pcm)*2)
	for index, sample := range pcm {
		binary.LittleEndian.PutUint16(raw[index*2:], uint16(sample))
	}
	return provider.write(map[string]any{
		"type": "input_audio_buffer.append", "event_id": fmt.Sprintf("%s-input-%d", provider.launch.Scope.SessionID, highWater),
		"audio": base64.StdEncoding.EncodeToString(raw),
	})
}

func (provider *openAIMeetingSpecialistProvider) admitResponseLocked() (int64, int64, error) {
	// Direct PCM has no documented provider-enforced input-token limit. Do not
	// infer one from duration. Reserve the model's entire documented context
	// window as audio input, then shrink the response output to what the exact
	// approved token and worst-case price envelopes can still fund.
	usedTokens := provider.receipt.InputTokens + provider.receipt.OutputTokens
	admittedOutput, admittedCost, ok := meetingSpecialistRealtimeResponseAdmission(provider.config, provider.launch, usedTokens, provider.receipt.ReconciledCostCent)
	if !ok {
		return 0, 0, ErrMeetingSpecialistProviderBudget
	}
	inputTokens, _, ok := meetingSpecialistRealtimeInputReservation(provider.config)
	if !ok {
		return 0, 0, ErrMeetingSpecialistProviderBudget
	}
	provider.activeInputLimit = inputTokens
	provider.activeOutputLimit = admittedOutput
	provider.activeCostLimit = admittedCost
	return admittedOutput, admittedCost, nil
}

func (provider *openAIMeetingSpecialistProvider) clearResponseAdmissionLocked() {
	provider.activeInputLimit = 0
	provider.activeOutputLimit = 0
	provider.activeCostLimit = 0
}

func (provider *openAIMeetingSpecialistProvider) BeginResponse(_ context.Context, floor MeetingAgentFloorLease) error {
	if provider == nil || floor.Generation == 0 || floor.Session.Scope != provider.launch.Scope || floor.Session.ExpiresAt.After(provider.launch.Invitation.ExpiresAt) {
		return ErrMeetingSpecialistProviderProtocol
	}
	provider.mu.Lock()
	if provider.closed || provider.usageUnreconciled || provider.terminalFenced || !provider.briefed || provider.activeFloor != nil || provider.cancelPending || provider.cancelledResponse != "" {
		provider.mu.Unlock()
		return ErrMeetingSpecialistProviderProtocol
	}
	maxOutputTokens, _, err := provider.admitResponseLocked()
	if err != nil {
		provider.mu.Unlock()
		return err
	}
	copyFloor := floor
	provider.activeFloor = &copyFloor
	provider.activeResponse, provider.activeItem = "", ""
	provider.audioBuffer = nil
	provider.audioBytes = 0
	provider.publishedAudioMS = 0
	provider.mu.Unlock()
	if err := provider.write(map[string]any{"type": "input_audio_buffer.commit", "event_id": fmt.Sprintf("%s-floor-%d-commit", provider.launch.Scope.SessionID, floor.Generation)}); err != nil {
		return err
	}
	return provider.write(map[string]any{
		"type": "response.create", "event_id": fmt.Sprintf("%s-floor-%d-response", provider.launch.Scope.SessionID, floor.Generation),
		"response": map[string]any{
			"output_modalities": []string{"audio"}, "max_output_tokens": maxOutputTokens,
			"tools": []any{}, "tool_choice": "none",
		},
	})
}

func (provider *openAIMeetingSpecialistProvider) CancelResponse(_ context.Context, generation uint64, reason string) error {
	if provider == nil {
		return ErrMeetingSpecialistProviderProtocol
	}
	provider.mu.Lock()
	if provider.closed || provider.usageUnreconciled || provider.terminalFenced {
		provider.mu.Unlock()
		return nil
	}
	if generation != 0 && (provider.activeFloor == nil || provider.activeFloor.Generation != generation) {
		provider.mu.Unlock()
		return ErrMeetingSpecialistFence
	}
	itemID, audioEndMS := provider.activeItem, provider.publishedAudioMS
	if provider.activeResponse == "" {
		provider.cancelPending = true
	} else {
		provider.cancelledResponse = provider.activeResponse
	}
	provider.cancelledItem = provider.activeItem
	provider.activeFloor = nil
	provider.activeResponse, provider.activeItem = "", ""
	provider.audioBuffer = nil
	provider.publishedAudioMS = 0
	provider.mu.Unlock()
	if err := provider.write(map[string]any{
		"type": "response.cancel", "event_id": fmt.Sprintf("%s-cancel-%d-%s", provider.launch.Scope.SessionID, generation, safeSpecialistEventID(reason)),
	}); err != nil {
		return err
	}
	if itemID != "" && audioEndMS > 0 {
		return provider.write(map[string]any{
			"type": "conversation.item.truncate", "event_id": fmt.Sprintf("%s-truncate-%d", provider.launch.Scope.SessionID, generation),
			"item_id": itemID, "content_index": 0, "audio_end_ms": audioEndMS,
		})
	}
	return nil
}

func (provider *openAIMeetingSpecialistProvider) Close(_ context.Context, reason string) error {
	if provider == nil {
		return nil
	}
	provider.mu.Lock()
	if provider.closed {
		done := provider.done
		provider.mu.Unlock()
		select {
		case <-done:
		case <-time.After(250 * time.Millisecond):
		}
		return nil
	}
	provider.closed = true
	itemID, audioEndMS := provider.activeItem, provider.publishedAudioMS
	provider.activeFloor = nil
	provider.audioBuffer = nil
	conn := provider.conn
	readerStarted := provider.readerStarted
	provider.mu.Unlock()
	provider.cancel()
	// Never wait behind a wedged provider write during revocation. When the
	// writer is available, send the documented best-effort clear/cancel/truncate
	// sequence under a hard deadline; otherwise close the socket immediately to
	// interrupt the blocked write.
	if conn != nil && provider.writeMu.TryLock() {
		_ = conn.SetWriteDeadline(time.Now().Add(meetingSpecialistRealtimeWriteTimeout))
		_ = conn.WriteJSON(map[string]any{"type": "input_audio_buffer.clear", "event_id": provider.launch.Scope.SessionID + "-close-clear"})
		_ = conn.WriteJSON(map[string]any{"type": "response.cancel", "event_id": provider.launch.Scope.SessionID + "-close-" + safeSpecialistEventID(reason)})
		if itemID != "" && audioEndMS > 0 {
			_ = conn.WriteJSON(map[string]any{
				"type": "conversation.item.truncate", "event_id": provider.launch.Scope.SessionID + "-close-truncate",
				"item_id": itemID, "content_index": 0, "audio_end_ms": audioEndMS,
			})
		}
		provider.writeMu.Unlock()
	}
	var err error
	if conn != nil {
		err = conn.Close()
	}
	if !readerStarted {
		provider.closeDone()
	}
	return err
}

func safeSpecialistEventID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			builder.WriteRune(r)
		}
		if builder.Len() >= 48 {
			break
		}
	}
	if builder.Len() == 0 {
		return "closed"
	}
	return builder.String()
}

func (provider *openAIMeetingSpecialistProvider) write(event any) error {
	provider.writeMu.Lock()
	defer provider.writeMu.Unlock()
	// Close linearizes by setting the provider fence before attempting the
	// write lock. A writer that was queued behind teardown must re-check that
	// fence only after it owns the lock, otherwise it could emit audio or a new
	// response after authority revocation.
	provider.mu.Lock()
	closed := provider.closed || provider.usageUnreconciled || provider.terminalFenced
	provider.mu.Unlock()
	if closed {
		return ErrMeetingSpecialistClosed
	}
	if err := provider.conn.SetWriteDeadline(time.Now().Add(meetingSpecialistRealtimeWriteTimeout)); err != nil {
		return fmt.Errorf("%w: websocket write deadline", ErrMeetingSpecialistProviderProtocol)
	}
	if err := provider.conn.WriteJSON(event); err != nil {
		return fmt.Errorf("%w: websocket write", ErrMeetingSpecialistProviderProtocol)
	}
	return nil
}

func (provider *openAIMeetingSpecialistProvider) readLoop() {
	defer provider.closeDone()
	for {
		_, raw, err := provider.conn.ReadMessage()
		if err != nil {
			provider.mu.Lock()
			closed := provider.closed
			provider.mu.Unlock()
			// A runtime-owned parent cancellation has its own exact terminal
			// classification and evidence callback. Let that lifetime owner close
			// the provider instead of racing it with a generic read failure.
			runtimeLifetimeEnded := meetingSpecialistRuntimeOwnsLifetime(provider.ctx) && provider.ctx.Err() != nil
			if !closed && !runtimeLifetimeEnded {
				provider.fail("provider_read_failed")
			}
			return
		}
		if int64(len(raw)) > provider.config.MaxEventBytes {
			provider.fail("provider_event_too_large")
			return
		}
		typ, err := provider.recordEvent(raw)
		if err != nil || provider.handleEvent(typ, raw) != nil {
			provider.fail("provider_event_invalid")
			return
		}
	}
}

func (provider *openAIMeetingSpecialistProvider) recordEvent(raw []byte) (string, error) {
	var envelope struct {
		Type    string `json:"type"`
		EventID string `json:"event_id"`
	}
	if json.Unmarshal(raw, &envelope) != nil || strings.TrimSpace(envelope.Type) == "" || strings.TrimSpace(envelope.EventID) == "" {
		return "", ErrMeetingSpecialistProviderProtocol
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if _, found := provider.seenEventIDs[envelope.EventID]; found || len(provider.eventHashes) >= provider.config.MaxEvents {
		return "", ErrMeetingSpecialistProviderProtocol
	}
	provider.seenEventIDs[envelope.EventID] = struct{}{}
	provider.eventHashes = append(provider.eventHashes, sha256Hex(raw))
	provider.receipt.EventCount = len(provider.eventHashes)
	provider.receipt.EventDigest = sha256Hex([]byte(strings.Join(provider.eventHashes, "\n")))
	return envelope.Type, nil
}

func (provider *openAIMeetingSpecialistProvider) handleEvent(typ string, raw []byte) error {
	switch typ {
	case "response.created":
		var event struct {
			Response struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"response"`
		}
		if json.Unmarshal(raw, &event) != nil || event.Response.ID == "" || !oneOf(event.Response.Status, "", "in_progress") {
			return ErrMeetingSpecialistProviderProtocol
		}
		provider.mu.Lock()
		defer provider.mu.Unlock()
		if provider.cancelPending && provider.activeFloor == nil && provider.activeResponse == "" {
			provider.cancelPending = false
			provider.cancelledResponse = event.Response.ID
			return nil
		}
		if provider.activeFloor == nil || provider.activeResponse != "" {
			return ErrMeetingSpecialistProviderProtocol
		}
		provider.activeResponse = event.Response.ID
		return nil
	case "response.output_audio.delta":
		return provider.handleAudioDelta(raw)
	case "response.output_audio.done", "response.output_audio_transcript.delta", "response.output_audio_transcript.done":
		return provider.validateOutputStreamEvent(typ, raw)
	case "response.output_item.added", "response.output_item.done":
		return provider.validateOutputItemEvent(typ, raw)
	case "response.content_part.added", "response.content_part.done":
		return provider.validateContentPartEvent(typ, raw)
	case "conversation.item.added", "conversation.item.done":
		return provider.validateConversationItemEvent(typ, raw)
	case "input_audio_buffer.committed":
		var event struct {
			ItemID string `json:"item_id"`
		}
		if json.Unmarshal(raw, &event) != nil || event.ItemID == "" {
			return ErrMeetingSpecialistProviderProtocol
		}
		return nil
	case "rate_limits.updated":
		var event struct {
			RateLimits []json.RawMessage `json:"rate_limits"`
		}
		if json.Unmarshal(raw, &event) != nil || len(event.RateLimits) == 0 {
			return ErrMeetingSpecialistProviderProtocol
		}
		return nil
	case "response.done":
		return provider.handleResponseDone(raw)
	case "error":
		return ErrMeetingSpecialistProviderProtocol
	default:
		// Text output, tool/MCP calls, automatic VAD events, session mutation,
		// and every future unknown event fail closed because this session
		// configured audio-only output, no tools, and explicit floor turns.
		return ErrMeetingSpecialistProviderProtocol
	}
}

func (provider *openAIMeetingSpecialistProvider) handleAudioDelta(raw []byte) error {
	var event struct {
		ResponseID   string `json:"response_id"`
		ItemID       string `json:"item_id"`
		OutputIndex  int    `json:"output_index"`
		ContentIndex int    `json:"content_index"`
		Delta        string `json:"delta"`
	}
	if json.Unmarshal(raw, &event) != nil || event.ResponseID == "" || event.ItemID == "" || event.OutputIndex != 0 || event.ContentIndex != 0 || event.Delta == "" {
		return ErrMeetingSpecialistProviderProtocol
	}
	audio, err := base64.StdEncoding.DecodeString(event.Delta)
	if err != nil || len(audio) == 0 || len(audio)%2 != 0 {
		return ErrMeetingSpecialistProviderProtocol
	}
	provider.mu.Lock()
	if provider.activeFloor == nil && provider.cancelledResponse == event.ResponseID && (provider.cancelledItem == "" || provider.cancelledItem == event.ItemID) {
		provider.cancelledItem = event.ItemID
		provider.mu.Unlock()
		return nil
	}
	if provider.activeFloor == nil || provider.activeResponse != event.ResponseID || provider.activeItem != "" && provider.activeItem != event.ItemID || provider.audioBytes+int64(len(audio)) > provider.config.MaxAudioBytes {
		provider.mu.Unlock()
		return ErrMeetingSpecialistProviderProtocol
	}
	provider.activeItem = event.ItemID
	provider.audioBytes += int64(len(audio))
	provider.audioBuffer = append(provider.audioBuffer, audio...)
	provider.mu.Unlock()
	// Audio is staged until response.done supplies the authoritative terminal
	// state and billable usage. This prevents incomplete or failed output from
	// escaping to the meeting while retaining bounded memory.
	return nil
}

func (provider *openAIMeetingSpecialistProvider) validateOutputStreamEvent(typ string, raw []byte) error {
	var event struct {
		ResponseID   string  `json:"response_id"`
		ItemID       string  `json:"item_id"`
		OutputIndex  int     `json:"output_index"`
		ContentIndex int     `json:"content_index"`
		Delta        *string `json:"delta,omitempty"`
		Transcript   *string `json:"transcript,omitempty"`
	}
	if json.Unmarshal(raw, &event) != nil || event.ResponseID == "" || event.ItemID == "" || event.OutputIndex != 0 || event.ContentIndex != 0 {
		return ErrMeetingSpecialistProviderProtocol
	}
	if strings.HasSuffix(typ, ".delta") && (event.Delta == nil || *event.Delta == "") {
		return ErrMeetingSpecialistProviderProtocol
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.activeFloor == nil && provider.cancelledResponse == event.ResponseID && (provider.cancelledItem == "" || provider.cancelledItem == event.ItemID) {
		provider.cancelledItem = event.ItemID
		return nil
	}
	if provider.activeFloor == nil || provider.activeResponse != event.ResponseID || provider.activeItem != "" && provider.activeItem != event.ItemID {
		return ErrMeetingSpecialistProviderProtocol
	}
	provider.activeItem = event.ItemID
	return nil
}

func (provider *openAIMeetingSpecialistProvider) validateOutputItemEvent(typ string, raw []byte) error {
	var event struct {
		ResponseID  string `json:"response_id"`
		OutputIndex int    `json:"output_index"`
		Item        struct {
			ID     string `json:"id"`
			Type   string `json:"type"`
			Role   string `json:"role"`
			Status string `json:"status"`
		} `json:"item"`
	}
	if json.Unmarshal(raw, &event) != nil || event.ResponseID == "" || event.OutputIndex != 0 || event.Item.ID == "" || event.Item.Type != "message" || event.Item.Role != "assistant" {
		return ErrMeetingSpecialistProviderProtocol
	}
	// Item completion is correlated here, but response.done is the documented
	// authority for the terminal response state. An incomplete item is valid and
	// must remain staged until that terminal event reconciles usage and cost.
	if typ == "response.output_item.done" && !oneOf(event.Item.Status, "completed", "incomplete") {
		return ErrMeetingSpecialistProviderProtocol
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.activeFloor == nil && provider.cancelledResponse == event.ResponseID && (provider.cancelledItem == "" || provider.cancelledItem == event.Item.ID) {
		provider.cancelledItem = event.Item.ID
		return nil
	}
	if provider.activeFloor == nil || provider.activeResponse != event.ResponseID || provider.activeItem != "" && provider.activeItem != event.Item.ID {
		return ErrMeetingSpecialistProviderProtocol
	}
	provider.activeItem = event.Item.ID
	return nil
}

func (provider *openAIMeetingSpecialistProvider) validateContentPartEvent(_ string, raw []byte) error {
	var event struct {
		ResponseID   string `json:"response_id"`
		ItemID       string `json:"item_id"`
		OutputIndex  int    `json:"output_index"`
		ContentIndex int    `json:"content_index"`
		Part         struct {
			Type string `json:"type"`
		} `json:"part"`
	}
	if json.Unmarshal(raw, &event) != nil || event.ResponseID == "" || event.ItemID == "" || event.OutputIndex != 0 || event.ContentIndex != 0 || event.Part.Type != "audio" {
		return ErrMeetingSpecialistProviderProtocol
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.activeFloor == nil && provider.cancelledResponse == event.ResponseID && (provider.cancelledItem == "" || provider.cancelledItem == event.ItemID) {
		provider.cancelledItem = event.ItemID
		return nil
	}
	if provider.activeFloor == nil || provider.activeResponse != event.ResponseID || provider.activeItem != "" && provider.activeItem != event.ItemID {
		return ErrMeetingSpecialistProviderProtocol
	}
	provider.activeItem = event.ItemID
	return nil
}

func (provider *openAIMeetingSpecialistProvider) validateConversationItemEvent(typ string, raw []byte) error {
	var event struct {
		Item struct {
			ID      string            `json:"id"`
			Type    string            `json:"type"`
			Role    string            `json:"role"`
			Status  string            `json:"status"`
			Content []json.RawMessage `json:"content"`
		} `json:"item"`
	}
	if json.Unmarshal(raw, &event) != nil || event.Item.ID == "" || event.Item.Type != "message" || !oneOf(event.Item.Role, "user", "assistant") || len(event.Item.Content) == 0 {
		return ErrMeetingSpecialistProviderProtocol
	}
	if typ == "conversation.item.done" && !oneOf(event.Item.Status, "completed", "incomplete") {
		return ErrMeetingSpecialistProviderProtocol
	}
	return nil
}

type meetingSpecialistRealtimeUsage struct {
	InputText, InputAudio, CachedText, CachedAudio int64
	OutputText, OutputAudio, Input, Output, Total  int64
}

func (provider *openAIMeetingSpecialistProvider) handleResponseDone(raw []byte) error {
	var event struct {
		Response struct {
			ID            string          `json:"id"`
			Status        string          `json:"status"`
			StatusDetails json.RawMessage `json:"status_details"`
			Usage         json.RawMessage `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &event) != nil || event.Response.ID == "" {
		return ErrMeetingSpecialistProviderProtocol
	}
	if event.Response.Status == "cancelled" {
		return provider.handleCancelledResponseDone(raw, event.Response.ID, event.Response.StatusDetails, event.Response.Usage)
	}
	if oneOf(event.Response.Status, "incomplete", "failed") {
		return provider.handleUnacceptedResponseDone(raw, event.Response.ID, event.Response.Status, event.Response.StatusDetails, event.Response.Usage)
	}
	if event.Response.Status != "completed" || !completedMeetingSpecialistStatus(event.Response.StatusDetails) {
		return ErrMeetingSpecialistProviderProtocol
	}
	usage, usageReconciled, err := classifyMeetingSpecialistRealtimeUsage(event.Response.Usage)
	if err != nil {
		return ErrMeetingSpecialistProviderProtocol
	}
	if !usageReconciled {
		return provider.handleCompletedResponseDoneWithUnreconciledUsage(raw, event.Response.ID)
	}
	if usage.Total > provider.launch.Context.TokenBudget || usage.Output > provider.config.MaxOutputTokens || usage.OutputAudio <= 0 {
		return ErrMeetingSpecialistProviderProtocol
	}
	entry := llmUsageEntry{
		Provider: providerOpenAI, Model: provider.config.Model, Seat: seatVoiceRoom, RoomID: provider.launch.Scope.RoomID, Workflow: "meeting_specialist",
		InputTokens: usage.InputText - usage.CachedText, CachedInputTokens: usage.CachedText,
		AudioInputTokens: usage.InputAudio - usage.CachedAudio, CachedAudioInputTokens: usage.CachedAudio,
		OutputTokens: usage.OutputText, AudioOutputTokens: usage.OutputAudio, WireSuccess: true, AcceptedOutput: true,
	}
	costUSD, priced := estimateCostUSDAt(provider.config.Model, provider.config.Now().UTC(), llmTokenUsage{
		InputTokens: entry.InputTokens, CachedInputTokens: entry.CachedInputTokens, AudioInputTokens: entry.AudioInputTokens,
		CachedAudioInputTokens: entry.CachedAudioInputTokens, OutputTokens: entry.OutputTokens, AudioOutputTokens: entry.AudioOutputTokens,
	})
	if !priced {
		return ErrMeetingSpecialistProviderProtocol
	}
	costCents := int64(math.Ceil(costUSD * 100))
	provider.mu.Lock()
	if provider.activeFloor == nil || provider.activeResponse != event.Response.ID || provider.audioBytes == 0 ||
		provider.activeInputLimit <= 0 || usage.Input > provider.activeInputLimit || provider.activeOutputLimit <= 0 || usage.Output > provider.activeOutputLimit || costCents > provider.activeCostLimit ||
		provider.receipt.InputTokens+provider.receipt.OutputTokens+usage.Total > provider.launch.Context.TokenBudget ||
		provider.receipt.InputTokens+provider.receipt.OutputTokens+usage.Total > provider.launch.ApprovalLimits.TokenBudget ||
		provider.receipt.ReconciledCostCent+costCents > provider.launch.Context.CostBudgetCents ||
		provider.receipt.ReconciledCostCent+costCents > provider.launch.ApprovalLimits.CostBudgetCents ||
		provider.receipt.ReconciledCostCent+costCents > provider.launch.Policy.CostBudgetCents {
		provider.mu.Unlock()
		return ErrMeetingSpecialistProviderProtocol
	}
	floor := *provider.activeFloor
	remaining := append([]byte(nil), provider.audioBuffer...)
	provider.audioBuffer = nil
	provider.activeFloor = nil
	provider.activeResponse, provider.activeItem = "", ""
	provider.publishedAudioMS = 0
	provider.clearResponseAdmissionLocked()
	hooks := provider.hooks
	provider.usageHashes = append(provider.usageHashes, workDigest(usage))
	provider.receipt.UsageDigest = sha256Hex([]byte(strings.Join(provider.usageHashes, "\n")))
	provider.receipt.UsageStatus = "reconciled"
	provider.receipt.TerminalEventHash = sha256Hex(raw)
	provider.receipt.TerminalStatus = event.Response.Status
	provider.receipt.InputTokens += usage.Input
	provider.receipt.OutputTokens += usage.Output
	provider.receipt.OutputAudioTokens += usage.OutputAudio
	provider.receipt.ReconciledCostCent += costCents
	provider.mu.Unlock()
	publishFailureReason := ""
	if len(remaining) > 0 {
		finalSeconds := int64((len(remaining) + meetingSpecialistRealtimeSampleRate*2 - 1) / (meetingSpecialistRealtimeSampleRate * 2))
		if err := hooks.PublishAudio(floor, meetingSpecialistPCM(remaining), finalSeconds, costCents); err != nil {
			publishFailureReason = "publication_failed"
			entry.AcceptedOutput = false
			entry.OutputFailureReason = publishFailureReason
			entry.EstCostUSD = costUSD
			recordLLMUsage(entry)
			return err
		}
	} else if costCents > 0 {
		publishFailureReason = "missing_publishable_audio"
		entry.AcceptedOutput = false
		entry.OutputFailureReason = publishFailureReason
		entry.EstCostUSD = costUSD
		recordLLMUsage(entry)
		return ErrMeetingSpecialistProviderProtocol
	}
	entry.EstCostUSD = costUSD
	recordLLMUsage(entry)
	if err := hooks.CompleteTurn(floor); err != nil {
		return err
	}
	return nil
}

// A completed provider response is not publishable when its optional usage
// object is absent or too partial to price exactly. Preserve the authoritative
// terminal event, discard staged audio, release the floor, and fence the
// session before another turn can consume an unknowable budget.
func (provider *openAIMeetingSpecialistProvider) handleCompletedResponseDoneWithUnreconciledUsage(raw []byte, responseID string) error {
	provider.mu.Lock()
	if provider.activeFloor == nil || provider.activeResponse != responseID {
		provider.mu.Unlock()
		return ErrMeetingSpecialistProviderProtocol
	}
	floor := *provider.activeFloor
	provider.audioBuffer = nil
	provider.audioBytes = 0
	provider.activeFloor = nil
	provider.activeResponse, provider.activeItem = "", ""
	provider.publishedAudioMS = 0
	provider.clearResponseAdmissionLocked()
	provider.usageUnreconciled = true
	provider.receipt.UsageStatus = "usage_unreconciled"
	provider.receipt.TerminalEventHash = sha256Hex(raw)
	provider.receipt.TerminalStatus = "completed"
	hooks := provider.hooks
	provider.mu.Unlock()
	if err := hooks.CompleteTurn(floor); err != nil {
		return err
	}
	if hooks.FailSession != nil {
		go hooks.FailSession("provider_usage_unreconciled")
	}
	return nil
}

// handleUnacceptedResponseDone follows the documented response.done contract:
// failed and incomplete are terminal responses, and usage (when present)
// corresponds to billing. Staged audio is always discarded, while strictly
// reconciled usage is still written to the ledger and immutable receipt.
func (provider *openAIMeetingSpecialistProvider) handleUnacceptedResponseDone(raw []byte, responseID, status string, statusDetails, usageRaw json.RawMessage) error {
	var details struct {
		Type   string `json:"type"`
		Reason string `json:"reason,omitempty"`
		Error  *struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error,omitempty"`
	}
	detailsRaw := bytes.TrimSpace(statusDetails)
	if len(detailsRaw) > 0 && !bytes.Equal(detailsRaw, []byte("null")) {
		if specialistStrictJSON(detailsRaw, &details) != nil || details.Type != "" && details.Type != status {
			return ErrMeetingSpecialistProviderProtocol
		}
	}
	failureReason := "provider_failed"
	switch status {
	case "incomplete":
		if details.Error != nil || details.Reason != "" && !oneOf(details.Reason, "max_output_tokens", "content_filter") {
			return ErrMeetingSpecialistProviderProtocol
		}
		failureReason = "incomplete"
		if details.Reason != "" {
			failureReason += "_" + details.Reason
		}
	case "failed":
		// The Realtime response.done schema makes the nested provider error and
		// its type/code optional. The terminal status is authoritative; retain
		// any supplied classification, but do not reject a documented failed
		// terminal merely because optional diagnostics were omitted.
		if details.Reason != "" {
			return ErrMeetingSpecialistProviderProtocol
		}
	default:
		return ErrMeetingSpecialistProviderProtocol
	}

	var usage meetingSpecialistRealtimeUsage
	var entry llmUsageEntry
	var costUSD float64
	var costCents int64
	usageReconciled := false
	var err error
	usage, usageReconciled, err = classifyMeetingSpecialistRealtimeUsage(usageRaw)
	if err != nil {
		return ErrMeetingSpecialistProviderProtocol
	}
	if usageReconciled {
		if usage.Total > provider.launch.Context.TokenBudget || usage.Output > provider.config.MaxOutputTokens {
			return ErrMeetingSpecialistProviderProtocol
		}
		entry = llmUsageEntry{
			Provider: providerOpenAI, Model: provider.config.Model, Seat: seatVoiceRoom, RoomID: provider.launch.Scope.RoomID, Workflow: "meeting_specialist",
			InputTokens: usage.InputText - usage.CachedText, CachedInputTokens: usage.CachedText,
			AudioInputTokens: usage.InputAudio - usage.CachedAudio, CachedAudioInputTokens: usage.CachedAudio,
			OutputTokens: usage.OutputText, AudioOutputTokens: usage.OutputAudio, WireSuccess: status != "failed", AcceptedOutput: false, OutputFailureReason: failureReason,
		}
		var priced bool
		costUSD, priced = estimateCostUSDAt(provider.config.Model, provider.config.Now().UTC(), llmTokenUsage{
			InputTokens: entry.InputTokens, CachedInputTokens: entry.CachedInputTokens, AudioInputTokens: entry.AudioInputTokens,
			CachedAudioInputTokens: entry.CachedAudioInputTokens, OutputTokens: entry.OutputTokens, AudioOutputTokens: entry.AudioOutputTokens,
		})
		if !priced {
			return ErrMeetingSpecialistProviderProtocol
		}
		costCents = int64(math.Ceil(costUSD * 100))
	}

	provider.mu.Lock()
	if provider.activeFloor == nil || provider.activeResponse != responseID ||
		provider.activeInputLimit <= 0 || usageReconciled && usage.Input > provider.activeInputLimit || provider.activeOutputLimit <= 0 || usageReconciled && usage.Output > provider.activeOutputLimit || usageReconciled && costCents > provider.activeCostLimit ||
		provider.receipt.InputTokens+provider.receipt.OutputTokens+usage.Total > provider.launch.Context.TokenBudget ||
		provider.receipt.InputTokens+provider.receipt.OutputTokens+usage.Total > provider.launch.ApprovalLimits.TokenBudget ||
		provider.receipt.ReconciledCostCent+costCents > provider.launch.Context.CostBudgetCents ||
		provider.receipt.ReconciledCostCent+costCents > provider.launch.ApprovalLimits.CostBudgetCents ||
		provider.receipt.ReconciledCostCent+costCents > provider.launch.Policy.CostBudgetCents {
		provider.mu.Unlock()
		return ErrMeetingSpecialistProviderProtocol
	}
	floor := *provider.activeFloor
	provider.audioBuffer = nil
	provider.audioBytes = 0
	provider.activeFloor = nil
	provider.activeResponse, provider.activeItem = "", ""
	provider.publishedAudioMS = 0
	provider.clearResponseAdmissionLocked()
	hooks := provider.hooks
	if usageReconciled {
		provider.usageHashes = append(provider.usageHashes, workDigest(usage))
		provider.receipt.UsageDigest = sha256Hex([]byte(strings.Join(provider.usageHashes, "\n")))
		provider.receipt.UsageStatus = "reconciled"
		provider.receipt.InputTokens += usage.Input
		provider.receipt.OutputTokens += usage.Output
		provider.receipt.OutputAudioTokens += usage.OutputAudio
		provider.receipt.ReconciledCostCent += costCents
	} else {
		provider.usageUnreconciled = true
		provider.receipt.UsageStatus = "usage_unreconciled"
	}
	provider.receipt.TerminalEventHash = sha256Hex(raw)
	provider.receipt.TerminalStatus = status
	provider.terminalFenced = status == "failed" || !usageReconciled
	provider.mu.Unlock()
	if usageReconciled {
		entry.EstCostUSD = costUSD
		recordLLMUsage(entry)
	}
	if err := hooks.CompleteTurn(floor); err != nil {
		return err
	}
	if status == "failed" || !usageReconciled {
		reason := "provider_response_failed"
		if !usageReconciled {
			reason = "provider_usage_unreconciled"
		}
		go hooks.FailSession(reason)
	}
	return nil
}

func (provider *openAIMeetingSpecialistProvider) handleCancelledResponseDone(raw []byte, responseID string, statusDetails, usageRaw json.RawMessage) error {
	var details struct {
		Type   string  `json:"type"`
		Reason *string `json:"reason"`
	}
	detailsRaw := bytes.TrimSpace(statusDetails)
	if len(detailsRaw) > 0 && !bytes.Equal(detailsRaw, []byte("null")) {
		if specialistStrictJSON(detailsRaw, &details) != nil || details.Type != "" && details.Type != "cancelled" ||
			details.Reason != nil && !oneOf(*details.Reason, "client_cancelled", "turn_detected") {
			return ErrMeetingSpecialistProviderProtocol
		}
	}
	var usage meetingSpecialistRealtimeUsage
	var entry llmUsageEntry
	var costUSD float64
	var costCents int64
	usageReconciled := false
	var err error
	usage, usageReconciled, err = classifyMeetingSpecialistRealtimeUsage(usageRaw)
	if err != nil {
		return ErrMeetingSpecialistProviderProtocol
	}
	if usageReconciled {
		if usage.Total > provider.launch.Context.TokenBudget || usage.Output > provider.config.MaxOutputTokens {
			return ErrMeetingSpecialistProviderProtocol
		}
		entry = llmUsageEntry{
			Provider: providerOpenAI, Model: provider.config.Model, Seat: seatVoiceRoom, RoomID: provider.launch.Scope.RoomID, Workflow: "meeting_specialist",
			InputTokens: usage.InputText - usage.CachedText, CachedInputTokens: usage.CachedText,
			AudioInputTokens: usage.InputAudio - usage.CachedAudio, CachedAudioInputTokens: usage.CachedAudio,
			OutputTokens: usage.OutputText, AudioOutputTokens: usage.OutputAudio, WireSuccess: true, AcceptedOutput: false, OutputFailureReason: "cancelled",
		}
		var priced bool
		costUSD, priced = estimateCostUSDAt(provider.config.Model, provider.config.Now().UTC(), llmTokenUsage{
			InputTokens: entry.InputTokens, CachedInputTokens: entry.CachedInputTokens, AudioInputTokens: entry.AudioInputTokens,
			CachedAudioInputTokens: entry.CachedAudioInputTokens, OutputTokens: entry.OutputTokens, AudioOutputTokens: entry.AudioOutputTokens,
		})
		if !priced {
			return ErrMeetingSpecialistProviderProtocol
		}
		costCents = int64(math.Ceil(costUSD * 100))
	}
	provider.mu.Lock()
	if provider.cancelledResponse != responseID || provider.activeFloor != nil ||
		provider.activeInputLimit <= 0 || usageReconciled && usage.Input > provider.activeInputLimit || provider.activeOutputLimit <= 0 || usageReconciled && usage.Output > provider.activeOutputLimit || usageReconciled && costCents > provider.activeCostLimit ||
		provider.receipt.InputTokens+provider.receipt.OutputTokens+usage.Total > provider.launch.Context.TokenBudget ||
		provider.receipt.InputTokens+provider.receipt.OutputTokens+usage.Total > provider.launch.ApprovalLimits.TokenBudget ||
		provider.receipt.ReconciledCostCent+costCents > provider.launch.Context.CostBudgetCents ||
		provider.receipt.ReconciledCostCent+costCents > provider.launch.ApprovalLimits.CostBudgetCents ||
		provider.receipt.ReconciledCostCent+costCents > provider.launch.Policy.CostBudgetCents {
		provider.mu.Unlock()
		return ErrMeetingSpecialistProviderProtocol
	}
	provider.cancelledResponse, provider.cancelledItem = "", ""
	provider.cancelPending = false
	provider.clearResponseAdmissionLocked()
	if usageReconciled {
		provider.usageHashes = append(provider.usageHashes, workDigest(usage))
		provider.receipt.UsageDigest = sha256Hex([]byte(strings.Join(provider.usageHashes, "\n")))
		provider.receipt.UsageStatus = "reconciled"
		provider.receipt.InputTokens += usage.Input
		provider.receipt.OutputTokens += usage.Output
		provider.receipt.OutputAudioTokens += usage.OutputAudio
		provider.receipt.ReconciledCostCent += costCents
	} else {
		provider.usageUnreconciled = true
		provider.receipt.UsageStatus = "usage_unreconciled"
	}
	provider.receipt.TerminalEventHash = sha256Hex(raw)
	provider.receipt.TerminalStatus = "cancelled"
	hooks := provider.hooks
	provider.mu.Unlock()
	if usageReconciled {
		entry.EstCostUSD = costUSD
		recordLLMUsage(entry)
	} else if hooks.FailSession != nil {
		go hooks.FailSession("provider_usage_unreconciled")
	}
	return nil
}

func completedMeetingSpecialistStatus(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return true
	}
	var value struct {
		Type string `json:"type"`
	}
	return specialistStrictJSON(raw, &value) == nil && (value.Type == "" || value.Type == "completed")
}

func classifyMeetingSpecialistRealtimeUsage(raw json.RawMessage) (meetingSpecialistRealtimeUsage, bool, error) {
	var wire struct {
		Total        *int64 `json:"total_tokens"`
		Input        *int64 `json:"input_tokens"`
		Output       *int64 `json:"output_tokens"`
		InputDetails *struct {
			Text          *int64 `json:"text_tokens"`
			Audio         *int64 `json:"audio_tokens"`
			Image         *int64 `json:"image_tokens"`
			Cached        *int64 `json:"cached_tokens"`
			CachedDetails *struct {
				Text  *int64 `json:"text_tokens"`
				Audio *int64 `json:"audio_tokens"`
				Image *int64 `json:"image_tokens"`
			} `json:"cached_tokens_details"`
		} `json:"input_token_details"`
		OutputDetails *struct {
			Text  *int64 `json:"text_tokens"`
			Audio *int64 `json:"audio_tokens"`
		} `json:"output_token_details"`
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return meetingSpecialistRealtimeUsage{}, false, nil
	}
	if specialistStrictJSON(trimmed, &wire) != nil {
		return meetingSpecialistRealtimeUsage{}, false, ErrMeetingSpecialistProviderProtocol
	}
	count := func(value *int64) int64 {
		if value == nil {
			return 0
		}
		return *value
	}
	validCount := func(value *int64) bool { return value == nil || *value >= 0 }
	if !validCount(wire.Total) || !validCount(wire.Input) || !validCount(wire.Output) {
		return meetingSpecialistRealtimeUsage{}, false, ErrMeetingSpecialistProviderProtocol
	}
	usage := meetingSpecialistRealtimeUsage{Input: count(wire.Input), Output: count(wire.Output), Total: count(wire.Total)}
	var inputText, inputAudio, image, cached *int64
	var outputText, outputAudio *int64
	var cachedText, cachedAudio, cachedImage *int64
	if wire.InputDetails != nil {
		inputText, inputAudio, image, cached = wire.InputDetails.Text, wire.InputDetails.Audio, wire.InputDetails.Image, wire.InputDetails.Cached
		if !validCount(inputText) || !validCount(inputAudio) || !validCount(image) || !validCount(cached) {
			return meetingSpecialistRealtimeUsage{}, false, ErrMeetingSpecialistProviderProtocol
		}
		usage.InputText, usage.InputAudio = count(inputText), count(inputAudio)
	}
	if wire.InputDetails != nil && wire.InputDetails.CachedDetails != nil {
		cachedText, cachedAudio, cachedImage = wire.InputDetails.CachedDetails.Text, wire.InputDetails.CachedDetails.Audio, wire.InputDetails.CachedDetails.Image
		if !validCount(cachedText) || !validCount(cachedAudio) || !validCount(cachedImage) {
			return meetingSpecialistRealtimeUsage{}, false, ErrMeetingSpecialistProviderProtocol
		}
		usage.CachedText, usage.CachedAudio = count(cachedText), count(cachedAudio)
	}
	if wire.OutputDetails != nil {
		outputText, outputAudio = wire.OutputDetails.Text, wire.OutputDetails.Audio
		if !validCount(outputText) || !validCount(outputAudio) {
			return meetingSpecialistRealtimeUsage{}, false, ErrMeetingSpecialistProviderProtocol
		}
		usage.OutputText, usage.OutputAudio = count(outputText), count(outputAudio)
	}
	cachedDetailsPartial := wire.InputDetails != nil && wire.InputDetails.CachedDetails != nil && (cachedText == nil || cachedAudio == nil)
	knownCached := count(cachedText) + count(cachedAudio) + count(cachedImage)
	if image != nil && *image != 0 || cachedImage != nil && *cachedImage != 0 ||
		wire.Total != nil && wire.Input != nil && wire.Output != nil && *wire.Total != *wire.Input+*wire.Output ||
		wire.Input != nil && inputText != nil && inputAudio != nil && *wire.Input != *inputText+*inputAudio ||
		wire.Output != nil && outputText != nil && outputAudio != nil && *wire.Output != *outputText+*outputAudio ||
		cached != nil && cachedText != nil && cachedAudio != nil && *cached != *cachedText+*cachedAudio ||
		cached != nil && knownCached > *cached ||
		cachedText != nil && inputText != nil && *cachedText > *inputText || cachedAudio != nil && inputAudio != nil && *cachedAudio > *inputAudio {
		return meetingSpecialistRealtimeUsage{}, false, ErrMeetingSpecialistProviderProtocol
	}
	cachedBreakdownComplete := cached != nil && (*cached == 0 && wire.InputDetails.CachedDetails == nil || wire.InputDetails.CachedDetails != nil && !cachedDetailsPartial)
	complete := wire.Total != nil && wire.Input != nil && wire.Output != nil && wire.InputDetails != nil && inputText != nil && inputAudio != nil && cachedBreakdownComplete && wire.OutputDetails != nil && outputText != nil && outputAudio != nil
	if !complete {
		return meetingSpecialistRealtimeUsage{}, false, nil
	}
	return usage, true, nil
}

func parseMeetingSpecialistRealtimeUsage(raw json.RawMessage) (meetingSpecialistRealtimeUsage, error) {
	usage, reconciled, err := classifyMeetingSpecialistRealtimeUsage(raw)
	if err != nil || !reconciled {
		return meetingSpecialistRealtimeUsage{}, ErrMeetingSpecialistProviderProtocol
	}
	return usage, nil
}

func specialistStrictJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return ErrMeetingSpecialistProviderProtocol
	}
	return nil
}

func meetingSpecialistPCM(raw []byte) []int16 {
	pcm := make([]int16, len(raw)/2)
	for index := range pcm {
		pcm[index] = int16(binary.LittleEndian.Uint16(raw[index*2:]))
	}
	return pcm
}

func (provider *openAIMeetingSpecialistProvider) fail(reason string) {
	provider.failureOnce.Do(func() {
		provider.mu.Lock()
		failureHash := sha256Hex([]byte(reason))
		provider.receipt.SessionFailureHash = failureHash
		// response.done remains the authoritative provider terminal. A later
		// local publication/floor/teardown failure must not rewrite that provider
		// provenance, but pre-terminal protocol/read failures still need an
		// explicit terminal marker.
		if provider.receipt.TerminalEventHash == "" {
			provider.receipt.TerminalStatus = "failed"
			provider.receipt.TerminalEventHash = failureHash
		}
		hook := provider.hooks.FailSession
		provider.mu.Unlock()
		_ = provider.Close(context.Background(), reason)
		if hook != nil {
			go hook(reason)
		}
	})
}

func (provider *openAIMeetingSpecialistProvider) closeDone() {
	provider.mu.Lock()
	select {
	case <-provider.done:
		provider.mu.Unlock()
		return
	default:
		close(provider.done)
	}
	provider.mu.Unlock()
}

func (provider *openAIMeetingSpecialistProvider) MeetingSpecialistProviderReceipt() MeetingSpecialistProviderReceipt {
	if provider == nil {
		return MeetingSpecialistProviderReceipt{}
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.receipt
}
