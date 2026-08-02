package e10probe

// This file deliberately implements a one-shot provider qualification probe,
// not a reusable application Realtime client. It is intentionally restrictive:
// one connection, one user text turn, one audio response, no tools and no retry.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	ScoutRealtimeModel      = "gpt-realtime-2.1"
	ScoutReasoningEffort    = "high"
	DefaultScoutRealtimeURL = "wss://api.openai.com/v1/realtime?model=gpt-realtime-2.1"
	MaxScoutOutputTokens    = int64(256)
	MaxScoutInputTokens     = int64(512)
	MaxScoutTotalTokens     = MaxScoutInputTokens + MaxScoutOutputTokens
	MaxScoutInputTextBytes  = 256
	MaxScoutAudioBytes      = MaxRealtimeServerEventBytes
	scoutEventSchema        = "openai-realtime-scout-events-2026-08-01-added-done-status-aware-v2"
	scoutPrompt             = "You are Scout. Give one short, factual greeting. Do not call tools."
	scoutUserText           = "Hello Scout. Please say a one-sentence hello."
	scoutAudioInputUSD      = 32.00
	scoutAudioCachedUSD     = 0.40
	scoutAudioOutputUSD     = 64.00
	scoutTextInputUSD       = 4.00
	scoutTextCachedUSD      = 0.40
	scoutTextOutputUSD      = 24.00
)

// RealtimeScoutConfig is invocation-local only. WebSocketURL can override the
// production endpoint only for loopback tests; production has no redirect path.
type RealtimeScoutConfig struct {
	Config
	InteractionID string
	WebSocketURL  string
}

// RealtimeScoutReceipt contains hashes and aggregate counters only. It never
// retains prompt/audio/transcript/event bodies, provider identifiers, or keys.
type RealtimeScoutReceipt struct {
	Schema                      string  `json:"schema"`
	Classification              string  `json:"classification"`
	Success                     bool    `json:"success"`
	Probe                       string  `json:"probe"`
	Endpoint                    string  `json:"endpoint"`
	Model                       string  `json:"model"`
	ReasoningEffort             string  `json:"reasoningEffort"`
	Outcome                     string  `json:"outcome"`
	FailureClass                string  `json:"failureClass,omitempty"`
	CandidateManifestSHA256     string  `json:"candidateManifestSha256"`
	AcknowledgementSHA256       string  `json:"acknowledgementSha256"`
	RequestShapeSHA256          string  `json:"requestShapeSha256"`
	InteractionIDSHA256         string  `json:"interactionIdSha256"`
	SessionIDSHA256             string  `json:"sessionIdSha256,omitempty"`
	ConversationCreatedObserved bool    `json:"conversationCreatedObserved"`
	ResponseIDSHA256            string  `json:"responseIdSha256,omitempty"`
	ResponseStatus              string  `json:"responseStatus,omitempty"`
	ResponseStatusDetailType    string  `json:"responseStatusDetailType,omitempty"`
	ResponseStatusReason        string  `json:"responseStatusReason,omitempty"`
	ResponseErrorTypeSHA256     string  `json:"responseErrorTypeSha256,omitempty"`
	ResponseErrorCodeSHA256     string  `json:"responseErrorCodeSha256,omitempty"`
	UserItemIDSHA256            string  `json:"userItemIdSha256,omitempty"`
	UserFinalizedIDSHA256       string  `json:"userFinalizedItemIdSha256,omitempty"`
	UserItemLifecycle           string  `json:"userItemLifecycle,omitempty"`
	AssistantItemIDSHA256       string  `json:"assistantItemIdSha256,omitempty"`
	AssistantFinalizedIDSHA256  string  `json:"assistantFinalizedItemIdSha256,omitempty"`
	AssistantItemLifecycle      string  `json:"assistantItemLifecycle,omitempty"`
	ProviderIDSHA256            string  `json:"providerIdsSha256,omitempty"`
	ServerEventIDSHA256         string  `json:"serverEventIdsSha256,omitempty"`
	EventOrderSHA256            string  `json:"eventOrderSha256"`
	AssistantAudioSHA256        string  `json:"assistantAudioSha256,omitempty"`
	AssistantTranscriptSHA256   string  `json:"assistantTranscriptSha256,omitempty"`
	AssistantAudioBytes         int64   `json:"assistantAudioBytes"`
	AssistantTranscriptChars    int     `json:"assistantTranscriptChars"`
	PartialOutputObserved       bool    `json:"partialOutputObserved"`
	CorrelationVerified         bool    `json:"correlationVerified"`
	EventCount                  int     `json:"eventCount"`
	ServerEventBytes            int64   `json:"serverEventBytes"`
	CredentialScope             string  `json:"credentialScope"`
	RequestProjectSHA256        string  `json:"requestProjectSha256,omitempty"`
	ExpectedProjectSHA256       string  `json:"expectedProjectSha256,omitempty"`
	ResponseProjectSHA256       string  `json:"responseProjectSha256,omitempty"`
	ResponseOrganizationSHA256  string  `json:"responseOrganizationSha256,omitempty"`
	RequestIDSHA256             string  `json:"requestIdSha256,omitempty"`
	AttributionVerified         bool    `json:"attributionVerified"`
	AttributionState            string  `json:"attributionState"`
	HTTPStatus                  int     `json:"httpStatus"`
	HandshakeLatencyMS          int64   `json:"handshakeLatencyMs"`
	SessionUpdateLatencyMS      int64   `json:"sessionUpdateLatencyMs,omitempty"`
	ResponseLatencyMS           int64   `json:"responseLatencyMs,omitempty"`
	TotalLatencyMS              int64   `json:"totalLatencyMs"`
	InputTextTokens             int64   `json:"inputTextTokens"`
	InputAudioTokens            int64   `json:"inputAudioTokens"`
	CachedTextTokens            int64   `json:"cachedTextTokens"`
	CachedAudioTokens           int64   `json:"cachedAudioTokens"`
	OutputTextTokens            int64   `json:"outputTextTokens"`
	OutputAudioTokens           int64   `json:"outputAudioTokens"`
	TotalTokens                 int64   `json:"totalTokens"`
	ReasoningTokenAccounting    string  `json:"reasoningTokenAccounting"`
	CostBasis                   string  `json:"costBasis"`
	UsageObserved               bool    `json:"usageObserved"`
	CostReconciled              bool    `json:"costReconciled"`
	CostState                   string  `json:"costState"`
	ComputedCostUSD             float64 `json:"computedCostUsd"`
	MaxUSD                      float64 `json:"maxUsd"`
	MaxOutputTokens             int64   `json:"maxOutputTokens"`
	MaxInputTokens              int64   `json:"maxInputTokens"`
	MaxTotalTokens              int64   `json:"maxTotalTokens"`
	MaxServerEvents             int     `json:"maxServerEvents"`
	MaxServerEventBytes         int64   `json:"maxServerEventBytes"`
	MaxInputTextBytes           int     `json:"maxInputTextBytes"`
	MaxAssistantAudioBytes      int64   `json:"maxAssistantAudioBytes"`
	MaxSessionMS                int64   `json:"maxSessionMs"`
	EventSchemaSHA256           string  `json:"eventSchemaSha256"`
	PriceSourceSHA256           string  `json:"priceSourceSha256"`
	PriceSourceURL              string  `json:"priceSourceUrl"`
	PriceSourceRevision         string  `json:"priceSourceRevision"`
	CreatedAt                   string  `json:"createdAt"`
}

// RunRealtimeScout performs one gpt-realtime-2.1 @ high, audio-output session.
// The interaction uses fixed synthetic text so no personal data or real work
// context crosses the provider boundary. Tools are explicitly absent.
func RunRealtimeScout(ctx context.Context, cfg RealtimeScoutConfig) (RealtimeScoutReceipt, error) {
	if err := validateRealtimeScoutConfig(cfg); err != nil {
		return RealtimeScoutReceipt{}, err
	}
	dir, err := newPrivateDir(cfg.ReceiptDir)
	if err != nil {
		return RealtimeScoutReceipt{}, err
	}
	now := cfg.now
	if now == nil {
		now = time.Now
	}
	endpoint := cfg.WebSocketURL
	if endpoint == "" {
		endpoint = DefaultScoutRealtimeURL
	}
	shape := canonicalScoutShape(digest(cfg.InteractionID))
	receipt := RealtimeScoutReceipt{
		Schema: "stride.e10.openai-realtime-scout-receipt/v1", Classification: "provider_contract_attempt",
		Probe: "scout-realtime-2.1-high-single-turn", Endpoint: scoutEndpointLabel(endpoint), Model: ScoutRealtimeModel,
		ReasoningEffort: ScoutReasoningEffort, Outcome: "transport_error", CandidateManifestSHA256: strings.ToLower(cfg.CandidateDigest),
		AcknowledgementSHA256: digest(cfg.Acknowledgement), RequestShapeSHA256: digest(shape), InteractionIDSHA256: digest(cfg.InteractionID),
		EventOrderSHA256: digest(""), CredentialScope: credentialScope(cfg.Config), RequestProjectSHA256: optionalDigest(cfg.Project),
		ExpectedProjectSHA256: strings.ToLower(strings.TrimSpace(cfg.ExpectedProjectSHA256)), AttributionState: initialAttributionState(cfg.Config),
		ReasoningTokenAccounting: "response.done usage has only audio/text token categories; any unclassified category fails closed",
		CostBasis:                "response.done documented modality token usage at 2026-08-01 standard rates", MaxUSD: cfg.MaxUSD,
		CostState:       "unreconciled_no_valid_response_done_usage",
		MaxOutputTokens: MaxScoutOutputTokens, MaxInputTokens: MaxScoutInputTokens, MaxTotalTokens: MaxScoutTotalTokens,
		MaxServerEvents: MaxRealtimeServerEvents, MaxServerEventBytes: MaxRealtimeServerEventBytes,
		MaxInputTextBytes: MaxScoutInputTextBytes, MaxAssistantAudioBytes: MaxScoutAudioBytes,
		MaxSessionMS:      RealtimeProbeTimeout.Milliseconds(),
		EventSchemaSHA256: digest(scoutEventSchema), PriceSourceSHA256: digest(scoutPriceDeclaration()),
		PriceSourceURL: "https://developers.openai.com/api/docs/pricing", PriceSourceRevision: priceSourceRevision,
		CreatedAt: now().UTC().Format(time.RFC3339Nano),
	}
	tracker := &realtimeEventTracker{}
	state := scoutRunState{}
	started := time.Now()
	finish := func(outcome, class string, runErr error) (RealtimeScoutReceipt, error) {
		receipt.AssistantAudioBytes = int64(len(state.audio))
		if len(state.audio) != 0 {
			receipt.AssistantAudioSHA256 = digestBytes(state.audio)
		}
		transcript := state.transcript.String()
		if transcript != "" {
			receipt.AssistantTranscriptSHA256 = digest(transcript)
			receipt.AssistantTranscriptChars = len([]rune(strings.Join(strings.Fields(transcript), " ")))
		}
		receipt.PartialOutputObserved = len(state.audio) != 0 || transcript != ""
		receipt.Outcome, receipt.FailureClass, receipt.TotalLatencyMS = outcome, class, time.Since(started).Milliseconds()
		receipt.EventCount, receipt.ServerEventBytes = tracker.count, tracker.bytes
		receipt.EventOrderSHA256 = digest(strings.Join(tracker.order, "\n"))
		if len(tracker.eventIDs) != 0 {
			receipt.ServerEventIDSHA256 = digest(strings.Join(tracker.eventIDs, "\n"))
		}
		if runErr == nil {
			receipt.Success = true
		}
		if err := writeRealtimeScoutReceipt(dir, receipt); err != nil {
			return receipt, err
		}
		return receipt, runErr
	}
	runCtx, cancel := boundedRealtimeContext(ctx)
	defer cancel()
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+cfg.APIKey)
	if cfg.Project != "" {
		headers.Set("OpenAI-Project", cfg.Project)
	}
	dialer := *websocket.DefaultDialer
	dialer.Proxy = nil
	dialer.HandshakeTimeout = RealtimeProbeTimeout
	hs := time.Now()
	conn, response, dialErr := dialer.DialContext(runCtx, endpoint, headers)
	receipt.HandshakeLatencyMS = time.Since(hs).Milliseconds()
	if response != nil {
		receipt.HTTPStatus = response.StatusCode
		captureScoutHeaders(&receipt, response.Header)
	}
	if dialErr != nil {
		if response != nil && response.Body != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, MaxRealtimeServerEventBytes))
			_ = response.Body.Close()
		}
		class := "transport"
		if response != nil {
			if response.StatusCode >= 300 && response.StatusCode < 400 {
				class = "redirect"
			} else {
				class = failureClassForRealtimeHTTP(response.StatusCode)
			}
		}
		return finish("transport_error", class, errors.New("Scout Realtime WebSocket connection failed"))
	}
	defer conn.Close()
	stopCancel := context.AfterFunc(runCtx, func() { _ = conn.Close() })
	defer stopCancel()
	conn.SetReadLimit(MaxRealtimeServerEventBytes + 1)
	if deadline, ok := runCtx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
		_ = conn.SetWriteDeadline(deadline)
	}
	if err := verifyScoutAttribution(&receipt); err != nil {
		return finish("attribution_mismatch", "project_attribution", err)
	}

	raw, typ, err := readRealtimeScoutEvent(conn, tracker)
	if err != nil {
		return finish(realtimeReadOutcome(err), realtimeReadFailureClass(err), err)
	}
	if typ != "session.created" {
		return finish("out_of_order", "event_order", safeUnexpectedRealtimeEventError(typ))
	}
	sessionID, eventID, err := parseScoutSessionCreated(raw)
	if err != nil {
		return finish("schema_mismatch", "event_schema", err)
	}
	tracker.eventIDs = append(tracker.eventIDs, eventID)
	receipt.SessionIDSHA256 = digest(sessionID)
	updateStarted := time.Now()
	if err := writeRealtimeJSON(conn, scoutSessionUpdate(cfg.InteractionID)); err != nil {
		return finish("transport_error", "write", err)
	}
	conversationCreatedSeen := false
	sessionUpdatedSeen := false
	for !sessionUpdatedSeen {
		raw, typ, err = readRealtimeScoutEvent(conn, tracker)
		if err != nil {
			return finish(realtimeReadOutcome(err), realtimeReadFailureClass(err), err)
		}
		switch typ {
		case "conversation.created":
			if conversationCreatedSeen {
				return finish("extra_conversation", "event_correlation", errors.New("provider emitted more than one conversation.created event"))
			}
			eventID, err = parseConversationCreated(raw)
			conversationCreatedSeen = err == nil
			receipt.ConversationCreatedObserved = conversationCreatedSeen
		case "session.updated":
			if sessionUpdatedSeen {
				return finish("extra_session_update", "event_correlation", errors.New("provider emitted more than one session.updated event"))
			}
			eventID, err = parseScoutSessionUpdated(raw, sessionID)
			sessionUpdatedSeen = err == nil
		default:
			return finish(outcomeForUnexpectedRealtimeEvent(typ), classForUnexpectedRealtimeEvent(typ), safeUnexpectedRealtimeEventError(typ))
		}
		if err != nil {
			return finish("schema_mismatch", "event_schema", err)
		}
		tracker.eventIDs = append(tracker.eventIDs, eventID)
	}
	receipt.SessionUpdateLatencyMS = time.Since(updateStarted).Milliseconds()
	if err := writeRealtimeJSON(conn, scoutUserItem(cfg.InteractionID)); err != nil {
		return finish("transport_error", "write", err)
	}
	if err := writeRealtimeJSON(conn, scoutResponseCreate(cfg.InteractionID)); err != nil {
		return finish("transport_error", "write", err)
	}
	responseStarted := time.Now()
	for {
		raw, typ, err = readRealtimeScoutEvent(conn, tracker)
		if err != nil {
			return finish(realtimeReadOutcome(err), realtimeReadFailureClass(err), err)
		}
		switch typ {
		case "conversation.created":
			if conversationCreatedSeen || state.userItem.admitted || state.responseID != "" || state.observedResponseID != "" {
				return finish("extra_conversation", "event_correlation", errors.New("conversation.created arrived outside session initialization"))
			}
			eid, parseErr := parseConversationCreated(raw)
			if parseErr != nil {
				return finish("schema_mismatch", "event_schema", parseErr)
			}
			conversationCreatedSeen = true
			receipt.ConversationCreatedObserved = true
			tracker.eventIDs = append(tracker.eventIDs, eid)
		case "conversation.item.created", "conversation.item.added":
			item, err := parseScoutConversationItem(raw, typ)
			if err != nil {
				return finish("schema_mismatch", "event_schema", err)
			}
			tracker.eventIDs = append(tracker.eventIDs, item.EventID)
			if item.Role == "user" {
				if state.userItem.admitted {
					return finish("extra_item", "event_correlation", errors.New("duplicate Scout user item"))
				}
				if item.PreviousItemID != "" {
					return finish("correlation_mismatch", "event_correlation", errors.New("Scout user item did not preserve the root chain"))
				}
				state.userItem = newScoutItemLifecycle(item.ID, typ, item.Status)
				receipt.UserItemIDSHA256 = digest(item.ID)
				receipt.UserItemLifecycle = state.userItem.family
				if state.userItem.finalized {
					receipt.UserFinalizedIDSHA256 = digest(item.ID)
				}
			} else if item.Role == "assistant" {
				if state.assistantItem.admitted {
					return finish("extra_item", "event_correlation", errors.New("duplicate Scout assistant item"))
				}
				if item.PreviousItemID != "" && item.PreviousItemID != state.userItem.id {
					return finish("correlation_mismatch", "event_correlation", errors.New("Scout assistant item did not follow the user item"))
				}
				if state.assistantID != "" && state.assistantID != item.ID {
					return finish("correlation_mismatch", "event_correlation", errors.New("assistant item did not match output lifecycle"))
				}
				state.assistantItem = newScoutItemLifecycle(item.ID, typ, item.Status)
				state.assistantID = item.ID
				receipt.AssistantItemIDSHA256 = digest(item.ID)
				receipt.AssistantItemLifecycle = state.assistantItem.family
				if state.assistantItem.finalized {
					receipt.AssistantFinalizedIDSHA256 = digest(item.ID)
				}
			} else {
				return finish("schema_mismatch", "event_schema", errors.New("unexpected conversation item role"))
			}
		case "conversation.item.done":
			item, err := parseScoutConversationItem(raw, typ)
			if err != nil {
				return finish("schema_mismatch", "event_schema", err)
			}
			var lifecycle *scoutItemLifecycle
			if item.Role == "user" {
				lifecycle = &state.userItem
				if item.PreviousItemID != "" {
					return finish("correlation_mismatch", "event_correlation", errors.New("Scout user item finalization did not preserve the root chain"))
				}
			} else {
				lifecycle = &state.assistantItem
				if item.PreviousItemID != "" && item.PreviousItemID != state.userItem.id {
					return finish("correlation_mismatch", "event_correlation", errors.New("Scout assistant item finalization did not follow the user item"))
				}
			}
			if !lifecycle.admitted || lifecycle.family != "modern_added_done" {
				return finish("out_of_order", "event_order", errors.New("Scout item finalized without a modern added lifecycle"))
			}
			if lifecycle.finalized {
				return finish("extra_item", "event_correlation", errors.New("Scout item was finalized more than once"))
			}
			if lifecycle.id != item.ID {
				return finish("correlation_mismatch", "event_correlation", errors.New("Scout item finalization identifier did not correlate"))
			}
			lifecycle.finalized, lifecycle.status = true, item.Status
			tracker.eventIDs = append(tracker.eventIDs, item.EventID)
			if item.Role == "user" {
				receipt.UserFinalizedIDSHA256 = digest(item.ID)
			} else {
				receipt.AssistantFinalizedIDSHA256 = digest(item.ID)
			}
		case "response.created":
			id, eid, err := parseScoutResponseCreated(raw)
			if err != nil {
				return finish("schema_mismatch", "event_schema", err)
			}
			if !state.userItem.finalized {
				return finish("out_of_order", "event_order", errors.New("Scout response began before the user item was finalized"))
			}
			if state.responseID != "" || (state.observedResponseID != "" && state.observedResponseID != id) {
				return finish("extra_response", "event_correlation", errors.New("more than one response"))
			}
			state.responseID = id
			tracker.eventIDs = append(tracker.eventIDs, eid)
			receipt.ResponseIDSHA256 = digest(id)
		case "response.output_audio.delta":
			id, eid, itemID, audio, err := parseScoutAudioDelta(raw)
			if err != nil {
				return finish("schema_mismatch", "event_schema", err)
			}
			if err := state.observeResponse(id); err != nil {
				return finish("correlation_mismatch", "event_correlation", errors.New("audio delta response mismatch"))
			}
			if err := state.observeAssistant(itemID); err != nil {
				return finish("correlation_mismatch", "event_correlation", errors.New("audio delta item mismatch"))
			}
			if int64(len(state.audio)+len(audio)) > MaxScoutAudioBytes {
				return finish("audio_cap_exceeded", "post_call_audio_cap", errors.New("assistant audio exceeded hard byte cap"))
			}
			tracker.eventIDs = append(tracker.eventIDs, eid)
			state.audio = append(state.audio, audio...)
		case "response.output_audio_transcript.delta":
			id, eid, itemID, delta, err := parseScoutTranscriptDelta(raw)
			if err != nil {
				return finish("schema_mismatch", "event_schema", err)
			}
			if err := state.observeResponse(id); err != nil {
				return finish("correlation_mismatch", "event_correlation", errors.New("transcript delta response mismatch"))
			}
			if err := state.observeAssistant(itemID); err != nil {
				return finish("correlation_mismatch", "event_correlation", errors.New("transcript delta item mismatch"))
			}
			tracker.eventIDs = append(tracker.eventIDs, eid)
			state.transcript.WriteString(delta)
		case "rate_limits.updated":
			if eid, err := parseScoutRateLimits(raw); err != nil {
				return finish("schema_mismatch", "event_schema", err)
			} else if eid != "" {
				tracker.eventIDs = append(tracker.eventIDs, eid)
			}
		case "response.output_item.added":
			id, eid, itemID, err := parseScoutOutputAdded(raw)
			if err != nil {
				return finish("schema_mismatch", "event_schema", err)
			}
			if err := state.observeResponse(id); err != nil {
				return finish("correlation_mismatch", "event_correlation", errors.New("auxiliary event response mismatch"))
			}
			if err := state.observeAssistant(itemID); err != nil || state.outputAdded {
				return finish("correlation_mismatch", "event_correlation", errors.New("output item added did not identify one assistant item"))
			}
			state.outputAdded = true
			tracker.eventIDs = append(tracker.eventIDs, eid)
		case "response.content_part.added", "response.content_part.done":
			id, eid, itemID, err := parseScoutContentPartEvent(raw, typ)
			if err != nil {
				return finish("schema_mismatch", "event_schema", err)
			}
			if err := state.observeResponse(id); err != nil || state.observeAssistant(itemID) != nil {
				return finish("correlation_mismatch", "event_correlation", errors.New("content part did not correlate"))
			}
			if typ == "response.content_part.added" {
				if state.contentAdded {
					return finish("duplicate_terminal", "event_terminal", errors.New("duplicate content part added event"))
				}
				state.contentAdded = true
			} else {
				if state.contentDone || !state.contentAdded {
					return finish("duplicate_terminal", "event_terminal", errors.New("duplicate content part done event"))
				}
				state.contentDone = true
			}
			tracker.eventIDs = append(tracker.eventIDs, eid)
		case "response.output_audio.done":
			id, eid, itemID, _, err := parseScoutStreamDone(raw, typ)
			if err != nil {
				return finish("schema_mismatch", "event_schema", err)
			}
			if err := state.observeResponse(id); err != nil || state.observeAssistant(itemID) != nil || state.audioDone || len(state.audio) == 0 {
				return finish("duplicate_terminal", "event_terminal", errors.New("audio terminal did not identify one response item"))
			}
			state.audioDone = true
			tracker.eventIDs = append(tracker.eventIDs, eid)
		case "response.output_audio_transcript.done":
			id, eid, itemID, transcript, err := parseScoutStreamDone(raw, typ)
			if err != nil {
				return finish("schema_mismatch", "event_schema", err)
			}
			if err := state.observeResponse(id); err != nil || state.observeAssistant(itemID) != nil || state.transcriptDone || state.transcript.Len() == 0 {
				return finish("duplicate_terminal", "event_terminal", errors.New("transcript terminal did not identify one response item"))
			}
			if transcript != state.transcript.String() {
				return finish("correlation_mismatch", "event_correlation", errors.New("transcript terminal did not equal accumulated transcript deltas"))
			}
			state.transcriptDone = true
			tracker.eventIDs = append(tracker.eventIDs, eid)
		case "response.output_item.done":
			id, eid, itemID, status, err := parseScoutOutputDone(raw)
			if err != nil {
				return finish("schema_mismatch", "event_schema", err)
			}
			if err := state.observeResponse(id); err != nil {
				return finish("correlation_mismatch", "event_correlation", errors.New("output item response mismatch"))
			}
			if state.observeAssistant(itemID) != nil {
				return finish("correlation_mismatch", "event_correlation", errors.New("output item mismatch"))
			}
			if state.outputDone {
				return finish("duplicate_terminal", "event_terminal", errors.New("duplicate output item terminal"))
			}
			state.outputDone = true
			state.outputItemStatus = status
			tracker.eventIDs = append(tracker.eventIDs, eid)
		case "response.done":
			done, err := parseScoutResponseDone(raw)
			recordScoutDoneStatus(&receipt, done)
			if err != nil {
				return finish("schema_mismatch", "event_schema", err)
			}
			tracker.eventIDs = append(tracker.eventIDs, done.EventID)
			if state.responseDone {
				return finish("duplicate_terminal", "event_terminal", errors.New("provider emitted more than one response.done event"))
			}
			state.responseDone = true
			if err := state.observeResponse(done.ResponseID); err != nil || state.responseID == "" || !state.userItem.finalized || !state.assistantItem.finalized ||
				!state.outputAdded || !state.contentAdded || !state.contentDone || !state.audioDone || !state.transcriptDone || !state.outputDone || len(state.audio) == 0 {
				return finish("correlation_mismatch", "event_correlation", errors.New("response terminal lifecycle was incomplete"))
			}
			if done.Usage.InputTotal > MaxScoutInputTokens || done.Usage.OutputTotal > MaxScoutOutputTokens || done.Usage.Total > MaxScoutTotalTokens {
				return finish("usage_cap_exceeded", "post_call_usage_cap", errors.New("provider usage exceeded hard cap"))
			}
			cost, err := scoutCost(done.Usage)
			if err != nil {
				return finish("unverifiable_cost", "usage_schema", err)
			}
			receipt.UsageObserved, receipt.CostReconciled = true, true
			receipt.CostState, receipt.ComputedCostUSD = "provider_response_done_usage_reconciled", cost
			if cost > cfg.MaxUSD {
				return finish("cost_cap_exceeded", "post_call_cost_cap", errors.New("provider-reported usage exceeded --max-usd"))
			}
			receipt.InputTextTokens, receipt.InputAudioTokens = done.Usage.InputText, done.Usage.InputAudio
			receipt.CachedTextTokens, receipt.CachedAudioTokens = done.Usage.CachedText, done.Usage.CachedAudio
			receipt.OutputTextTokens, receipt.OutputAudioTokens, receipt.TotalTokens = done.Usage.OutputText, done.Usage.OutputAudio, done.Usage.Total
			receipt.ProviderIDSHA256 = digest(strings.Join([]string{sessionID, state.responseID, state.userItem.family, state.userItem.id, state.assistantItem.family, state.assistantItem.id}, "\n"))
			receipt.CorrelationVerified = state.userItem.id != "" && state.assistantItem.id != "" && state.assistantItem.id == state.assistantID &&
				receipt.UserItemIDSHA256 == receipt.UserFinalizedIDSHA256 && receipt.AssistantItemIDSHA256 == receipt.AssistantFinalizedIDSHA256
			if !receipt.CorrelationVerified {
				return finish("correlation_mismatch", "event_correlation", errors.New("Scout provider item identifiers did not fully correlate"))
			}
			if done.Status != "completed" {
				return finish("response_"+done.Status, "provider_completion", errors.New("Scout response did not complete"))
			}
			if state.outputItemStatus != "completed" || (state.userItem.family == "modern_added_done" && state.userItem.status != "completed") ||
				(state.assistantItem.family == "modern_added_done" && state.assistantItem.status != "completed") {
				return finish("correlation_mismatch", "event_correlation", errors.New("completed Scout response contained a non-completed item"))
			}
			receipt.ResponseLatencyMS = time.Since(responseStarted).Milliseconds()
			if err := verifyNoPostTerminalScoutEvent(runCtx, conn, tracker); err != nil {
				switch {
				case errors.Is(err, errRealtimeDuplicateTerminal):
					return finish("duplicate_terminal", "event_terminal", err)
				case errors.Is(err, errRealtimePostTerminalEvent):
					return finish("post_terminal_event", "event_order", err)
				default:
					return finish(realtimeReadOutcome(err), realtimeReadFailureClass(err), err)
				}
			}
			return finish("pass", "", nil)
		case "error", "response.failed":
			return finish("provider_error", "provider_event", safeUnexpectedRealtimeEventError(typ))
		default:
			return finish(outcomeForUnexpectedRealtimeEvent(typ), classForUnexpectedRealtimeEvent(typ), safeUnexpectedRealtimeEventError(typ))
		}
	}
}

type scoutRunState struct {
	userItem, assistantItem                             scoutItemLifecycle
	outputAdded, contentAdded, contentDone              bool
	audioDone, transcriptDone, outputDone, responseDone bool
	assistantID, responseID, observedResponseID         string
	outputItemStatus                                    string
	audio                                               []byte
	transcript                                          strings.Builder
}

type scoutItemLifecycle struct {
	id, family, status  string
	admitted, finalized bool
}

func newScoutItemLifecycle(id, eventType, status string) scoutItemLifecycle {
	if eventType == "conversation.item.created" {
		return scoutItemLifecycle{id: id, family: "legacy_created", status: status, admitted: true, finalized: true}
	}
	return scoutItemLifecycle{id: id, family: "modern_added_done", status: status, admitted: true}
}

func (s *scoutRunState) observeResponse(id string) error {
	if id == "" || (s.responseID != "" && s.responseID != id) || (s.observedResponseID != "" && s.observedResponseID != id) {
		return errors.New("response identifier did not correlate")
	}
	s.observedResponseID = id
	return nil
}

func (s *scoutRunState) observeAssistant(id string) error {
	if id == "" || (s.assistantID != "" && s.assistantID != id) {
		return errors.New("assistant item identifier did not correlate")
	}
	s.assistantID = id
	return nil
}

type scoutUsage struct {
	InputText, InputAudio, CachedText, CachedAudio int64
	OutputText, OutputAudio                        int64
	InputTotal, OutputTotal, Total                 int64
}
type scoutDone struct {
	EventID, ResponseID, Status string
	StatusDetails               scoutResponseStatusDetails
	Usage                       scoutUsage
}
type scoutResponseStatusDetails struct {
	Type, Reason, ErrorType, ErrorCode string
}

func recordScoutDoneStatus(receipt *RealtimeScoutReceipt, done scoutDone) {
	receipt.ResponseStatus = done.Status
	receipt.ResponseStatusDetailType = done.StatusDetails.Type
	receipt.ResponseStatusReason = done.StatusDetails.Reason
	if done.StatusDetails.ErrorType != "" {
		receipt.ResponseErrorTypeSHA256 = digest(done.StatusDetails.ErrorType)
	}
	if done.StatusDetails.ErrorCode != "" {
		receipt.ResponseErrorCodeSHA256 = digest(done.StatusDetails.ErrorCode)
	}
}

func validateRealtimeScoutConfig(cfg RealtimeScoutConfig) error {
	if err := validateCommon(cfg.Config); err != nil {
		return err
	}
	if cfg.BaseURL != "" || cfg.Client != nil {
		return errors.New("HTTP overrides do not control the Scout Realtime probe")
	}
	if err := validateProjectBinding(cfg.Config); err != nil {
		return err
	}
	if len(scoutUserText) > MaxScoutInputTextBytes || len(scoutPrompt) > MaxScoutInputTextBytes {
		return errors.New("fixed Scout synthetic interaction exceeds input byte cap")
	}
	if cfg.Model != ScoutRealtimeModel {
		return fmt.Errorf("Scout Realtime probe only permits model %q", ScoutRealtimeModel)
	}
	if cfg.MaxUSD <= 0 || cfg.MaxUSD > MaxProbeUSD {
		return fmt.Errorf("--max-usd must be greater than 0 and no more than %.2f", MaxProbeUSD)
	}
	if cfg.MaxUSD < scoutWorstCaseCost() {
		return fmt.Errorf("--max-usd %.6f is below the configured hard-cap maximum %.6f", cfg.MaxUSD, scoutWorstCaseCost())
	}
	if err := validateRealtimeSegmentID(cfg.InteractionID); err != nil {
		return fmt.Errorf("interaction ID: %w", err)
	}
	endpoint := cfg.WebSocketURL
	if endpoint == "" {
		endpoint = DefaultScoutRealtimeURL
	}
	return validateScoutWebSocketURL(endpoint)
}

func validateScoutWebSocketURL(value string) error {
	u, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("invalid Scout Realtime WebSocket URL: %w", err)
	}
	if u.User != nil || u.Fragment != "" || u.Path != "/v1/realtime" || u.RawQuery != "model="+ScoutRealtimeModel {
		return errors.New("Scout Realtime WebSocket URL must use /v1/realtime?model=gpt-realtime-2.1 without user info or fragment")
	}
	if u.Scheme == "wss" && u.Host == "api.openai.com" {
		return nil
	}
	host := u.Hostname()
	if (u.Scheme == "ws" || u.Scheme == "wss") && (host == "localhost" || net.ParseIP(host).IsLoopback()) {
		return nil
	}
	return errors.New("Scout Realtime WebSocket URL must be the official gpt-realtime-2.1 endpoint (loopback is test-only)")
}

func scoutSessionUpdate(id string) map[string]any {
	return map[string]any{"type": "session.update", "event_id": id + "-session", "session": map[string]any{"type": "realtime", "instructions": scoutPrompt, "max_output_tokens": MaxScoutOutputTokens, "output_modalities": []string{"audio"}, "tools": []any{}, "tool_choice": "none", "parallel_tool_calls": false, "reasoning": map[string]any{"effort": ScoutReasoningEffort}, "tracing": nil, "truncation": "disabled", "audio": map[string]any{"output": map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": 24000}, "voice": "marin"}}}}
}
func scoutUserItem(id string) map[string]any {
	return map[string]any{"type": "conversation.item.create", "event_id": id + "-user", "previous_item_id": nil, "item": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": scoutUserText}}}}
}
func scoutResponseCreate(id string) map[string]any {
	return map[string]any{"type": "response.create", "event_id": id + "-response", "response": map[string]any{"output_modalities": []string{"audio"}, "max_output_tokens": MaxScoutOutputTokens, "tools": []any{}, "tool_choice": "none"}}
}
func canonicalScoutShape(interactionSHA string) string {
	return strings.Join([]string{"e10-scout-realtime-shape-v2", "endpoint=/v1/realtime?model=" + ScoutRealtimeModel, "session_type=realtime", "model=" + ScoutRealtimeModel, "reasoning_effort=" + ScoutReasoningEffort, "instructions_sha256=" + digest(scoutPrompt), "user_text_sha256=" + digest(scoutUserText), "output_modalities=audio", "output_format=audio/pcm@24000", "voice=marin", "tools=[]", "tool_choice=none", "parallel_tool_calls=false", "tracing=null", "truncation=disabled", "max_output_tokens=256", "max_input_tokens=512", "max_input_text_bytes=256", "max_audio_bytes=1048576", "max_session_ms=30000", "user_item_count=1", "response_create_count=1", "interaction_sha256=" + interactionSHA}, "\n")
}
func scoutPriceDeclaration() string {
	return strings.Join([]string{"official-pricing-declaration-v1", "source=https://developers.openai.com/api/docs/pricing", "revision=" + priceSourceRevision, "model=" + ScoutRealtimeModel, "input_audio_usd_per_million=32", "cached_audio_usd_per_million=0.40", "output_audio_usd_per_million=64", "input_text_usd_per_million=4", "cached_text_usd_per_million=0.40", "output_text_usd_per_million=24"}, "\n")
}
func scoutEndpointLabel(value string) string {
	u, err := url.Parse(value)
	if err != nil {
		return "/v1/realtime?model=" + ScoutRealtimeModel
	}
	return u.Path + "?" + u.RawQuery
}

func readRealtimeScoutEvent(conn *websocket.Conn, tracker *realtimeEventTracker) ([]byte, string, error) {
	raw, eventType, err := readRealtimeEvent(conn, tracker)
	if err != nil {
		return nil, "", err
	}
	var envelope struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || strings.TrimSpace(envelope.EventID) == "" {
		return nil, "", errors.New("Scout Realtime server event did not include a documented event_id")
	}
	if tracker.seenIDs == nil {
		tracker.seenIDs = make(map[string]struct{})
	}
	if _, seen := tracker.seenIDs[envelope.EventID]; seen {
		return nil, "", errRealtimeDuplicateEvent
	}
	tracker.seenIDs[envelope.EventID] = struct{}{}
	return raw, eventType, nil
}

func verifyNoPostTerminalScoutEvent(ctx context.Context, conn *websocket.Conn, tracker *realtimeEventTracker) error {
	_ = conn.SetReadDeadline(time.Now().Add(realtimeTerminalObserveWindow))
	for {
		raw, eventType, err := readRealtimeScoutEvent(conn, tracker)
		if err != nil {
			if errors.Is(err, errRealtimeReadTimeout) || errors.Is(err, errRealtimeTerminalClosed) || ctx.Err() != nil {
				return nil
			}
			return err
		}
		if eventType == "rate_limits.updated" {
			eventID, parseErr := parseScoutRateLimits(raw)
			if parseErr != nil {
				return errRealtimePostTerminalEvent
			}
			if eventID != "" {
				tracker.eventIDs = append(tracker.eventIDs, eventID)
			}
			continue
		}
		if eventType == "response.done" {
			return errRealtimeDuplicateTerminal
		}
		return errRealtimePostTerminalEvent
	}
}

func parseScoutSessionCreated(raw []byte) (string, string, error) {
	var e struct {
		Type    string `json:"type"`
		EventID string `json:"event_id"`
		Session struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			Model string `json:"model"`
		} `json:"session"`
	}
	if err := json.Unmarshal(raw, &e); err != nil || e.Type != "session.created" || e.EventID == "" || e.Session.ID == "" || e.Session.Type != "realtime" || (e.Session.Model != "" && e.Session.Model != ScoutRealtimeModel) {
		return "", "", errors.New("session.created did not match bounded Scout session")
	}
	return e.Session.ID, e.EventID, nil
}
func parseScoutSessionUpdated(raw []byte, sessionID string) (string, error) {
	var e struct {
		Type    string `json:"type"`
		EventID string `json:"event_id"`
		Session struct {
			ID        string `json:"id"`
			Type      string `json:"type"`
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
			Max        int64             `json:"max_output_tokens"`
			Modalities []string          `json:"output_modalities"`
			Tools      []json.RawMessage `json:"tools"`
			ToolChoice string            `json:"tool_choice"`
			Parallel   bool              `json:"parallel_tool_calls"`
			Tracing    json.RawMessage   `json:"tracing"`
			Truncation json.RawMessage   `json:"truncation"`
			Audio      struct {
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
	if err := json.Unmarshal(raw, &e); err != nil || e.Type != "session.updated" || e.EventID == "" || e.Session.ID != sessionID || e.Session.Type != "realtime" || e.Session.Reasoning.Effort != ScoutReasoningEffort || e.Session.Max != MaxScoutOutputTokens || len(e.Session.Modalities) != 1 || e.Session.Modalities[0] != "audio" || len(e.Session.Tools) != 0 || e.Session.ToolChoice != "none" || e.Session.Parallel || !bytes.Equal(bytes.TrimSpace(e.Session.Tracing), []byte("null")) || !bytes.Equal(bytes.TrimSpace(e.Session.Truncation), []byte("\"disabled\"")) || e.Session.Audio.Output.Voice != "marin" || e.Session.Audio.Output.Format.Type != "audio/pcm" || e.Session.Audio.Output.Format.Rate != 24000 {
		return "", errors.New("session.updated did not echo bounded Scout configuration")
	}
	return e.EventID, nil
}

type scoutConversationItemEvent struct {
	ID, EventID, Role, PreviousItemID, Status string
}

func parseScoutConversationItem(raw []byte, expectedType string) (scoutConversationItemEvent, error) {
	if expectedType != "conversation.item.created" && expectedType != "conversation.item.added" && expectedType != "conversation.item.done" {
		return scoutConversationItemEvent{}, errors.New("Scout conversation item event type was not permitted")
	}
	var e struct {
		Type           string  `json:"type"`
		EventID        string  `json:"event_id"`
		PreviousItemID *string `json:"previous_item_id"`
		Item           struct {
			ID      string          `json:"id"`
			Object  string          `json:"object"`
			Type    string          `json:"type"`
			Status  string          `json:"status"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"item"`
	}
	if err := json.Unmarshal(raw, &e); err != nil || e.Type != expectedType || e.EventID == "" || e.Item.ID == "" ||
		(e.Item.Object != "" && e.Item.Object != "realtime.item") || e.Item.Type != "message" ||
		(e.Item.Role != "user" && e.Item.Role != "assistant") || !validScoutItemStatus(e.Item.Status, expectedType == "conversation.item.done") {
		return scoutConversationItemEvent{}, errors.New("conversation item did not match Scout lifecycle schema")
	}
	if (expectedType == "conversation.item.added" && e.Item.Role == "user") || expectedType == "conversation.item.done" {
		content := bytes.TrimSpace(e.Item.Content)
		if len(content) == 0 || bytes.Equal(content, []byte("null")) {
			return scoutConversationItemEvent{}, errors.New("Scout conversation item omitted its required content array")
		}
		var parts []json.RawMessage
		if err := json.Unmarshal(content, &parts); err != nil {
			return scoutConversationItemEvent{}, errors.New("Scout conversation item content was not an array")
		}
	}
	previous := ""
	if e.PreviousItemID != nil {
		previous = *e.PreviousItemID
	}
	return scoutConversationItemEvent{ID: e.Item.ID, EventID: e.EventID, Role: e.Item.Role, PreviousItemID: previous, Status: e.Item.Status}, nil
}

func validScoutItemStatus(status string, finalized bool) bool {
	if finalized {
		return status == "completed" || status == "incomplete"
	}
	return status == "" || status == "in_progress" || status == "completed" || status == "incomplete"
}
func parseScoutResponseCreated(raw []byte) (string, string, error) {
	var e struct {
		Type     string `json:"type"`
		EventID  string `json:"event_id"`
		Response struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &e); err != nil || e.Type != "response.created" || e.EventID == "" || e.Response.ID == "" || (e.Response.Status != "in_progress" && e.Response.Status != "") {
		return "", "", errors.New("response.created did not match Scout lifecycle schema")
	}
	return e.Response.ID, e.EventID, nil
}
func parseScoutAudioDelta(raw []byte) (string, string, string, []byte, error) {
	var e struct {
		Type         string `json:"type"`
		EventID      string `json:"event_id"`
		ResponseID   string `json:"response_id"`
		ItemID       string `json:"item_id"`
		Delta        string `json:"delta"`
		OutputIndex  int    `json:"output_index"`
		ContentIndex int    `json:"content_index"`
	}
	if err := json.Unmarshal(raw, &e); err != nil || e.Type != "response.output_audio.delta" || e.EventID == "" || e.ResponseID == "" || e.ItemID == "" || e.OutputIndex != 0 || e.ContentIndex != 0 || e.Delta == "" {
		return "", "", "", nil, errors.New("audio delta did not match Scout event schema")
	}
	// Decode into an ephemeral aggregate that will be hashed in the receipt; no
	// provider audio body is written to disk or returned to a caller.
	audio, err := base64.StdEncoding.DecodeString(e.Delta)
	if err != nil || len(audio) == 0 {
		return "", "", "", nil, errors.New("audio delta was not valid non-empty base64")
	}
	return e.ResponseID, e.EventID, e.ItemID, audio, nil
}
func parseScoutTranscriptDelta(raw []byte) (string, string, string, string, error) {
	var e struct {
		Type         string `json:"type"`
		EventID      string `json:"event_id"`
		ResponseID   string `json:"response_id"`
		ItemID       string `json:"item_id"`
		Delta        string `json:"delta"`
		OutputIndex  int    `json:"output_index"`
		ContentIndex int    `json:"content_index"`
	}
	if err := json.Unmarshal(raw, &e); err != nil || e.Type != "response.output_audio_transcript.delta" || e.EventID == "" || e.ResponseID == "" || e.ItemID == "" || e.OutputIndex != 0 || e.ContentIndex != 0 || e.Delta == "" {
		return "", "", "", "", errors.New("audio transcript delta did not match Scout event schema")
	}
	return e.ResponseID, e.EventID, e.ItemID, e.Delta, nil
}
func parseScoutRateLimits(raw []byte) (string, error) {
	var e struct {
		Type    string `json:"type"`
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(raw, &e); err != nil || e.Type != "rate_limits.updated" {
		return "", errors.New("rate limits event did not match its documented envelope")
	}
	return e.EventID, nil
}

func parseScoutOutputAdded(raw []byte) (responseID, eventID, itemID string, err error) {
	var e struct {
		Type        string `json:"type"`
		EventID     string `json:"event_id"`
		ResponseID  string `json:"response_id"`
		OutputIndex int    `json:"output_index"`
		Item        struct {
			ID     string `json:"id"`
			Object string `json:"object"`
			Type   string `json:"type"`
			Status string `json:"status"`
			Role   string `json:"role"`
		} `json:"item"`
	}
	if json.Unmarshal(raw, &e) != nil || e.Type != "response.output_item.added" || e.EventID == "" || e.ResponseID == "" || e.OutputIndex != 0 || e.Item.ID == "" ||
		(e.Item.Object != "" && e.Item.Object != "realtime.item") || e.Item.Type != "message" || (e.Item.Status != "" && e.Item.Status != "in_progress") || e.Item.Role != "assistant" {
		return "", "", "", errors.New("output item added did not contain the bounded assistant item")
	}
	return e.ResponseID, e.EventID, e.Item.ID, nil
}

func parseScoutContentPartEvent(raw []byte, wantType string) (responseID, eventID, itemID string, err error) {
	var e struct {
		Type         string `json:"type"`
		EventID      string `json:"event_id"`
		ResponseID   string `json:"response_id"`
		ItemID       string `json:"item_id"`
		OutputIndex  int    `json:"output_index"`
		ContentIndex int    `json:"content_index"`
		Part         struct {
			Type string `json:"type"`
		} `json:"part"`
	}
	if json.Unmarshal(raw, &e) != nil || e.Type != wantType || e.EventID == "" || e.ResponseID == "" || e.ItemID == "" || e.OutputIndex != 0 || e.ContentIndex != 0 || e.Part.Type != "audio" {
		return "", "", "", errors.New("content part event did not match the bounded audio part")
	}
	return e.ResponseID, e.EventID, e.ItemID, nil
}

func parseScoutStreamDone(raw []byte, wantType string) (responseID, eventID, itemID, transcript string, err error) {
	var e struct {
		Type         string `json:"type"`
		EventID      string `json:"event_id"`
		ResponseID   string `json:"response_id"`
		ItemID       string `json:"item_id"`
		OutputIndex  int    `json:"output_index"`
		ContentIndex int    `json:"content_index"`
		Transcript   string `json:"transcript"`
	}
	if json.Unmarshal(raw, &e) != nil || e.Type != wantType || e.EventID == "" || e.ResponseID == "" || e.ItemID == "" || e.OutputIndex != 0 || e.ContentIndex != 0 {
		return "", "", "", "", errors.New("stream terminal did not match the bounded response item")
	}
	if wantType == "response.output_audio_transcript.done" && strings.TrimSpace(e.Transcript) == "" {
		return "", "", "", "", errors.New("transcript terminal was empty")
	}
	return e.ResponseID, e.EventID, e.ItemID, e.Transcript, nil
}
func parseScoutOutputDone(raw []byte) (string, string, string, string, error) {
	var e struct {
		Type        string `json:"type"`
		EventID     string `json:"event_id"`
		ResponseID  string `json:"response_id"`
		OutputIndex int    `json:"output_index"`
		Item        struct {
			ID     string `json:"id"`
			Object string `json:"object"`
			Type   string `json:"type"`
			Status string `json:"status"`
			Role   string `json:"role"`
		} `json:"item"`
	}
	if err := json.Unmarshal(raw, &e); err != nil || e.Type != "response.output_item.done" || e.EventID == "" || e.ResponseID == "" || e.OutputIndex != 0 || e.Item.ID == "" ||
		(e.Item.Object != "" && e.Item.Object != "realtime.item") || e.Item.Type != "message" || (e.Item.Status != "completed" && e.Item.Status != "incomplete") || e.Item.Role != "assistant" {
		return "", "", "", "", errors.New("output item did not match Scout event schema")
	}
	return e.ResponseID, e.EventID, e.Item.ID, e.Item.Status, nil
}
func parseScoutResponseDone(raw []byte) (scoutDone, error) {
	var e struct {
		Type     string `json:"type"`
		EventID  string `json:"event_id"`
		Response struct {
			ID            string          `json:"id"`
			Status        string          `json:"status"`
			StatusDetails json.RawMessage `json:"status_details"`
			Usage         json.RawMessage `json:"usage"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &e); err != nil || e.Type != "response.done" || e.EventID == "" || e.Response.ID == "" {
		return scoutDone{}, errors.New("response.done did not match Scout terminal schema")
	}
	done := scoutDone{EventID: e.EventID, ResponseID: e.Response.ID, Status: e.Response.Status}
	statusDetails, err := parseScoutResponseStatus(e.Response.Status, e.Response.StatusDetails)
	if err != nil {
		return done, err
	}
	done.StatusDetails = statusDetails
	u, err := parseScoutUsage(e.Response.Usage)
	if err != nil {
		return done, err
	}
	done.Usage = u
	return done, nil
}

func parseScoutResponseStatus(status string, raw json.RawMessage) (scoutResponseStatusDetails, error) {
	detailsRaw := bytes.TrimSpace(raw)
	if status == "completed" {
		if len(detailsRaw) == 0 || bytes.Equal(detailsRaw, []byte("null")) {
			return scoutResponseStatusDetails{}, nil
		}
		var details struct {
			Type string `json:"type"`
		}
		if err := decodeStrictJSON(detailsRaw, &details); err != nil || details.Type != "completed" {
			return scoutResponseStatusDetails{}, errors.New("completed response.done used invalid status details")
		}
		return scoutResponseStatusDetails{Type: details.Type}, nil
	}
	if status != "incomplete" && status != "cancelled" && status != "failed" {
		return scoutResponseStatusDetails{}, errors.New("response.done used a non-terminal status")
	}
	if len(detailsRaw) == 0 || bytes.Equal(detailsRaw, []byte("null")) {
		return scoutResponseStatusDetails{}, errors.New("non-completed response.done omitted status details")
	}
	var details struct {
		Type   string  `json:"type"`
		Reason *string `json:"reason"`
		Error  *struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := decodeStrictJSON(detailsRaw, &details); err != nil || details.Type != status {
		return scoutResponseStatusDetails{}, errors.New("response.done status details did not match its status")
	}
	result := scoutResponseStatusDetails{Type: details.Type}
	switch status {
	case "incomplete":
		if details.Reason == nil || (*details.Reason != "max_output_tokens" && *details.Reason != "content_filter") || details.Error != nil {
			return scoutResponseStatusDetails{}, errors.New("incomplete response.done used an undocumented reason")
		}
		result.Reason = *details.Reason
		return result, nil
	case "cancelled":
		if details.Reason == nil || (*details.Reason != "turn_detected" && *details.Reason != "client_cancelled") || details.Error != nil {
			return scoutResponseStatusDetails{}, errors.New("cancelled response.done used an undocumented reason")
		}
		result.Reason = *details.Reason
		return result, nil
	default:
		if details.Reason != nil || details.Error == nil {
			return scoutResponseStatusDetails{}, errors.New("failed response.done omitted its provider error classification")
		}
		result.Reason = "provider_failed"
		result.ErrorType = details.Error.Type
		result.ErrorCode = details.Error.Code
		return result, nil
	}
}
func parseScoutUsage(raw json.RawMessage) (scoutUsage, error) {
	var u struct {
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
	if err := decodeStrictJSON(raw, &u); err != nil {
		return scoutUsage{}, fmt.Errorf("response usage must have no unclassified fields: %w", err)
	}
	if u.Total == nil || u.Input == nil || u.Output == nil || u.InputDetails == nil || u.OutputDetails == nil {
		return scoutUsage{}, errors.New("response usage omitted totals or detail partitions required for cost reconciliation")
	}
	total, input, output := *u.Total, *u.Input, *u.Output
	inputText, inputAudio, inputImage, cached := scoutOptionalCount(u.InputDetails.Text), scoutOptionalCount(u.InputDetails.Audio), scoutOptionalCount(u.InputDetails.Image), scoutOptionalCount(u.InputDetails.Cached)
	var cachedText, cachedAudio, cachedImage int64
	if u.InputDetails.CachedDetails != nil {
		cachedText = scoutOptionalCount(u.InputDetails.CachedDetails.Text)
		cachedAudio = scoutOptionalCount(u.InputDetails.CachedDetails.Audio)
		cachedImage = scoutOptionalCount(u.InputDetails.CachedDetails.Image)
	}
	outputText, outputAudio := scoutOptionalCount(u.OutputDetails.Text), scoutOptionalCount(u.OutputDetails.Audio)
	if total < 0 || input < 0 || output < 0 || inputText < 0 || inputAudio < 0 || inputImage != 0 || cached < 0 ||
		cachedText < 0 || cachedAudio < 0 || cachedImage != 0 || outputText < 0 || outputAudio < 0 ||
		cached != cachedText+cachedAudio || input != inputText+inputAudio || output != outputText+outputAudio || total != input+output ||
		cachedText > inputText || cachedAudio > inputAudio {
		return scoutUsage{}, errors.New("response usage categories were not a fully-accounted nonnegative token partition")
	}
	if input == 0 || inputText == 0 || inputAudio != 0 || cachedAudio != 0 || output == 0 || outputAudio == 0 {
		return scoutUsage{}, errors.New("response usage did not account for the fixed text input and non-empty audio output")
	}
	return scoutUsage{InputText: inputText, InputAudio: inputAudio, CachedText: cachedText, CachedAudio: cachedAudio, OutputText: outputText, OutputAudio: outputAudio, InputTotal: input, OutputTotal: output, Total: total}, nil
}
func scoutOptionalCount(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
func scoutCost(u scoutUsage) (float64, error) {
	if u.CachedText > u.InputText || u.CachedAudio > u.InputAudio {
		return 0, errors.New("cached usage exceeded input usage")
	}
	return (float64(u.InputText-u.CachedText)*scoutTextInputUSD + float64(u.CachedText)*scoutTextCachedUSD + float64(u.InputAudio-u.CachedAudio)*scoutAudioInputUSD + float64(u.CachedAudio)*scoutAudioCachedUSD + float64(u.OutputText)*scoutTextOutputUSD + float64(u.OutputAudio)*scoutAudioOutputUSD) / 1_000_000, nil
}
func scoutWorstCaseCost() float64 {
	return (float64(MaxScoutInputTokens)*scoutTextInputUSD + float64(MaxScoutOutputTokens)*scoutAudioOutputUSD) / 1_000_000
}
func captureScoutHeaders(r *RealtimeScoutReceipt, h http.Header) {
	if v := h.Get("X-Request-ID"); v != "" {
		r.RequestIDSHA256 = digest(v)
	}
	if v := h.Get("OpenAI-Project"); v != "" {
		r.ResponseProjectSHA256 = digest(v)
	}
	if v := h.Get("OpenAI-Organization"); v != "" {
		r.ResponseOrganizationSHA256 = digest(v)
	}
}
func verifyScoutAttribution(r *RealtimeScoutReceipt) error {
	if r.ResponseProjectSHA256 == "" || r.RequestProjectSHA256 == "" || r.ExpectedProjectSHA256 == "" {
		return nil
	}
	if r.ResponseProjectSHA256 != r.RequestProjectSHA256 || r.ResponseProjectSHA256 != r.ExpectedProjectSHA256 {
		return errors.New("provider project attribution echo did not match request-bound project")
	}
	r.AttributionVerified = true
	r.AttributionState = "provider_verified"
	return nil
}
func writeRealtimeScoutReceipt(dir string, r RealtimeScoutReceipt) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(dir, "receipt.json")
	if err := os.WriteFile(p, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Chmod(p, 0o600)
}
