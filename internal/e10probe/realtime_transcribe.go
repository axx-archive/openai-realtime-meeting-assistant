package e10probe

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
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
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

const (
	DefaultRealtimeTranscriptionURL = "wss://api.openai.com/v1/realtime?intent=transcription"
	RealtimeProbeTimeout            = 30 * time.Second
	MaxRealtimeServerEvents         = 128
	MaxRealtimeServerEventBytes     = int64(1 << 20)
	realtimeTerminalObserveWindow   = 75 * time.Millisecond
	realtimeEventSchemaRevision     = "openai-realtime-transcription-events-2026-08-01-added-done"
)

// RealtimeTranscribeConfig extends the provider-independent Config with the
// one application segment identifier and WebSocket endpoint needed by the
// committed-turn Realtime transcription contract. WebSocketURL may differ from
// DefaultRealtimeTranscriptionURL only for a loopback test server.
type RealtimeTranscribeConfig struct {
	Config
	SegmentID    string
	WebSocketURL string
}

// RealtimeTranscriptionReceipt is deliberately body-free. It stores hashes of
// application/provider identifiers, event order, the transcript, and the exact
// PCM payload, but never their raw values or any request/response body.
type RealtimeTranscriptionReceipt struct {
	Schema                     string   `json:"schema"`
	Classification             string   `json:"classification"`
	Success                    bool     `json:"success"`
	Probe                      string   `json:"probe"`
	Endpoint                   string   `json:"endpoint"`
	Model                      string   `json:"model"`
	Outcome                    string   `json:"outcome"`
	FailureClass               string   `json:"failureClass,omitempty"`
	CandidateManifestSHA256    string   `json:"candidateManifestSha256"`
	AcknowledgementSHA256      string   `json:"acknowledgementSha256"`
	RequestShapeSHA256         string   `json:"requestShapeSha256"`
	FixtureSHA256              string   `json:"fixtureSha256"`
	ReferenceSHA256            string   `json:"referenceSha256"`
	PCMDataSHA256              string   `json:"pcmDataSha256"`
	SegmentIDSHA256            string   `json:"segmentIdSha256"`
	SessionIDSHA256            string   `json:"sessionIdSha256,omitempty"`
	CommitItemIDSHA256         string   `json:"commitItemIdSha256,omitempty"`
	CreatedItemIDSHA256        string   `json:"createdItemIdSha256,omitempty"`
	FinalizedItemIDSHA256      string   `json:"finalizedItemIdSha256,omitempty"`
	CompletedItemIDSHA256      string   `json:"completedItemIdSha256,omitempty"`
	ItemLifecycle              string   `json:"itemLifecycle,omitempty"`
	ProviderIDSHA256           string   `json:"providerIdsSha256,omitempty"`
	ServerEventIDSHA256        string   `json:"serverEventIdsSha256,omitempty"`
	EventOrderSHA256           string   `json:"eventOrderSha256"`
	TranscriptSHA256           string   `json:"transcriptSha256,omitempty"`
	NormalizedTranscriptChars  int      `json:"normalizedTranscriptChars,omitempty"`
	PreviousItemState          string   `json:"previousItemState,omitempty"`
	CorrelationVerified        bool     `json:"correlationVerified"`
	EventCount                 int      `json:"eventCount"`
	ServerEventBytes           int64    `json:"serverEventBytes"`
	CredentialScope            string   `json:"credentialScope"`
	RequestProjectSHA256       string   `json:"requestProjectSha256,omitempty"`
	ExpectedProjectSHA256      string   `json:"expectedProjectSha256,omitempty"`
	ResponseProjectSHA256      string   `json:"responseProjectSha256,omitempty"`
	ResponseOrganizationSHA256 string   `json:"responseOrganizationSha256,omitempty"`
	RequestIDSHA256            string   `json:"requestIdSha256,omitempty"`
	AttributionVerified        bool     `json:"attributionVerified"`
	AttributionState           string   `json:"attributionState"`
	HTTPStatus                 int      `json:"httpStatus"`
	HandshakeLatencyMS         int64    `json:"handshakeLatencyMs"`
	SessionUpdateLatencyMS     int64    `json:"sessionUpdateLatencyMs,omitempty"`
	TranscriptionLatencyMS     int64    `json:"transcriptionLatencyMs,omitempty"`
	TotalLatencyMS             int64    `json:"totalLatencyMs"`
	LocalDurationMS            int64    `json:"localDurationMs"`
	ReportedDurationMS         int64    `json:"reportedDurationMs,omitempty"`
	ReportedUsageType          string   `json:"reportedUsageType,omitempty"`
	ReportedInputTokens        *int64   `json:"reportedInputTokens,omitempty"`
	ReportedAudioTokens        *int64   `json:"reportedAudioTokens,omitempty"`
	ReportedOutputTokens       *int64   `json:"reportedOutputTokens,omitempty"`
	ReportedTotalTokens        *int64   `json:"reportedTotalTokens,omitempty"`
	ReportedUsageSeconds       *float64 `json:"reportedUsageSeconds,omitempty"`
	CostBasis                  string   `json:"costBasis"`
	ComputedCostUSD            float64  `json:"computedCostUsd"`
	EstimatedCostUSD           float64  `json:"estimatedCostUsd"`
	MaxUSD                     float64  `json:"maxUsd"`
	MaxDurationMS              int64    `json:"maxDurationMs"`
	MaxInputBytes              int64    `json:"maxInputBytes"`
	MaxServerEvents            int      `json:"maxServerEvents"`
	MaxServerEventBytes        int64    `json:"maxServerEventBytes"`
	FixtureByteCount           int64    `json:"fixtureByteCount"`
	PCMByteCount               int64    `json:"pcmByteCount"`
	EventSchemaSHA256          string   `json:"eventSchemaSha256"`
	PriceSourceSHA256          string   `json:"priceSourceSha256"`
	PriceSourceURL             string   `json:"priceSourceUrl"`
	PriceSourceRevision        string   `json:"priceSourceRevision"`
	CreatedAt                  string   `json:"createdAt"`
}

type realtimeEventTracker struct {
	count    int
	bytes    int64
	order    []string
	eventIDs []string
	seenIDs  map[string]struct{}
}

// RunRealtimeTranscription verifies one bounded, manually committed
// gpt-transcribe turn over the Realtime WebSocket API. It performs no retry,
// redirect, fallback, or second audio append.
func RunRealtimeTranscription(ctx context.Context, cfg RealtimeTranscribeConfig) (RealtimeTranscriptionReceipt, error) {
	if err := validateRealtimeTranscriptionConfig(cfg); err != nil {
		return RealtimeTranscriptionReceipt{}, err
	}

	wav, duration, fixtureSHA, err := loadApprovedWAV(cfg.AudioPath, cfg.ExpectedFixtureSHA256)
	if err != nil {
		return RealtimeTranscriptionReceipt{}, err
	}
	if duration > MaxProbeDuration {
		return RealtimeTranscriptionReceipt{}, fmt.Errorf("audio duration %s exceeds hard maximum %s", duration, MaxProbeDuration)
	}
	pcm, err := extractApprovedPCM24Mono16(wav, duration)
	if err != nil {
		return RealtimeTranscriptionReceipt{}, err
	}
	if _, err := loadApprovedReference(cfg.ReferencePath, cfg.ExpectedReferenceSHA256); err != nil {
		return RealtimeTranscriptionReceipt{}, err
	}
	estimatedCost := costFor(duration)
	if cfg.MaxUSD < estimatedCost {
		return RealtimeTranscriptionReceipt{}, fmt.Errorf("--max-usd %.6f is below estimated maximum cost %.6f", cfg.MaxUSD, estimatedCost)
	}

	receiptDir, err := newPrivateDir(cfg.ReceiptDir)
	if err != nil {
		return RealtimeTranscriptionReceipt{}, err
	}
	now := cfg.now
	if now == nil {
		now = time.Now
	}
	endpoint := DefaultRealtimeTranscriptionURL
	if cfg.WebSocketURL != "" {
		endpoint = cfg.WebSocketURL
	}
	shape := canonicalRealtimeTranscriptionShape(fixtureSHA, digestBytes(pcm), strings.ToLower(cfg.ExpectedReferenceSHA256), digest(cfg.SegmentID))
	receipt := RealtimeTranscriptionReceipt{
		Schema:                  "stride.e10.openai-realtime-transcription-receipt/v1",
		Classification:          "provider_contract_attempt",
		Probe:                   "transcribe-realtime-committed-turn",
		Endpoint:                realtimeEndpointLabel(endpoint),
		Model:                   cfg.Model,
		Outcome:                 "transport_error",
		CandidateManifestSHA256: strings.ToLower(cfg.CandidateDigest),
		AcknowledgementSHA256:   digest(cfg.Acknowledgement),
		RequestShapeSHA256:      digest(shape),
		FixtureSHA256:           fixtureSHA,
		ReferenceSHA256:         strings.ToLower(cfg.ExpectedReferenceSHA256),
		PCMDataSHA256:           digestBytes(pcm),
		SegmentIDSHA256:         digest(cfg.SegmentID),
		EventOrderSHA256:        digest(""),
		CredentialScope:         credentialScope(cfg.Config),
		RequestProjectSHA256:    optionalDigest(cfg.Project),
		ExpectedProjectSHA256:   strings.ToLower(strings.TrimSpace(cfg.ExpectedProjectSHA256)),
		AttributionState:        initialAttributionState(cfg.Config),
		LocalDurationMS:         duration.Milliseconds(),
		CostBasis:               "local_wav_duration",
		EstimatedCostUSD:        estimatedCost,
		ComputedCostUSD:         estimatedCost,
		MaxUSD:                  cfg.MaxUSD,
		MaxDurationMS:           MaxProbeDuration.Milliseconds(),
		MaxInputBytes:           MaxProbeBytes,
		MaxServerEvents:         MaxRealtimeServerEvents,
		MaxServerEventBytes:     MaxRealtimeServerEventBytes,
		FixtureByteCount:        int64(len(wav)),
		PCMByteCount:            int64(len(pcm)),
		EventSchemaSHA256:       digest(realtimeEventSchemaRevision),
		PriceSourceSHA256:       digest(realtimeTranscriptionPriceDeclaration()),
		PriceSourceURL:          priceSourceURL,
		PriceSourceRevision:     priceSourceRevision,
		CreatedAt:               now().UTC().Format(time.RFC3339Nano),
	}
	tracker := &realtimeEventTracker{}
	started := time.Now()
	finish := func(outcome, failureClass string, runErr error) (RealtimeTranscriptionReceipt, error) {
		receipt.Outcome = outcome
		receipt.FailureClass = failureClass
		receipt.TotalLatencyMS = time.Since(started).Milliseconds()
		receipt.EventCount = tracker.count
		receipt.ServerEventBytes = tracker.bytes
		receipt.EventOrderSHA256 = digest(strings.Join(tracker.order, "\n"))
		if len(tracker.eventIDs) > 0 {
			receipt.ServerEventIDSHA256 = digest(strings.Join(tracker.eventIDs, "\n"))
		}
		if runErr == nil {
			receipt.Success = true
		}
		writeErr := writeRealtimeTranscriptionReceipt(receiptDir, receipt)
		if writeErr != nil {
			return receipt, writeErr
		}
		return receipt, runErr
	}

	runCtx, cancel := boundedRealtimeContext(ctx)
	defer cancel()
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+cfg.APIKey)
	if strings.TrimSpace(cfg.Project) != "" {
		headers.Set("OpenAI-Project", cfg.Project)
	}
	dialer := *websocket.DefaultDialer
	dialer.Proxy = nil
	dialer.HandshakeTimeout = RealtimeProbeTimeout
	handshakeStarted := time.Now()
	conn, response, dialErr := dialer.DialContext(runCtx, endpoint, headers)
	receipt.HandshakeLatencyMS = time.Since(handshakeStarted).Milliseconds()
	if response != nil {
		receipt.HTTPStatus = response.StatusCode
		captureRealtimeResponseHeaders(&receipt, response.Header)
	}
	if dialErr != nil {
		if response != nil && response.Body != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, MaxRealtimeServerEventBytes))
			_ = response.Body.Close()
		}
		failureClass := "transport"
		if response != nil {
			if response.StatusCode >= 300 && response.StatusCode < 400 {
				failureClass = "redirect"
			} else {
				failureClass = failureClassForRealtimeHTTP(response.StatusCode)
			}
		}
		return finish("transport_error", failureClass, errors.New("realtime WebSocket connection failed"))
	}
	defer conn.Close()
	// A context cancellation does not by itself interrupt a gorilla/websocket
	// ReadMessage call. Closing the per-probe connection is the bounded escape
	// hatch; stop the callback before the normal deferred close returns.
	stopCancel := context.AfterFunc(runCtx, func() { _ = conn.Close() })
	defer stopCancel()
	conn.SetReadLimit(MaxRealtimeServerEventBytes + 1)
	if deadline, ok := runCtx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
		_ = conn.SetWriteDeadline(deadline)
	}
	if err := verifyRealtimeAttribution(&receipt); err != nil {
		return finish("attribution_mismatch", "project_attribution", err)
	}

	createdRaw, createdType, err := readRealtimeTranscriptionEvent(conn, tracker)
	if err != nil {
		return finish(realtimeReadOutcome(err), realtimeReadFailureClass(err), err)
	}
	if createdType != "session.created" {
		return finish(outcomeForUnexpectedRealtimeEvent(createdType), classForUnexpectedRealtimeEvent(createdType), safeUnexpectedRealtimeEventError(createdType))
	}
	sessionID, createdEventID, err := parseSessionCreated(createdRaw)
	if err != nil {
		return finish("schema_mismatch", "event_schema", err)
	}
	tracker.eventIDs = append(tracker.eventIDs, createdEventID)
	receipt.SessionIDSHA256 = digest(sessionID)

	sessionUpdate := realtimeSessionUpdate(cfg.SegmentID)
	updateStarted := time.Now()
	if err := writeRealtimeJSON(conn, sessionUpdate); err != nil {
		return finish("transport_error", "write", err)
	}
	conversationCreatedSeen := false
	for {
		updatedRaw, updatedType, readErr := readRealtimeTranscriptionEvent(conn, tracker)
		if readErr != nil {
			return finish(realtimeReadOutcome(readErr), realtimeReadFailureClass(readErr), readErr)
		}
		switch updatedType {
		case "session.updated":
			updatedEventID, parseErr := parseSessionUpdated(updatedRaw, sessionID)
			if parseErr != nil {
				return finish("schema_mismatch", "event_schema", parseErr)
			}
			tracker.eventIDs = append(tracker.eventIDs, updatedEventID)
			goto sessionUpdated
		case "conversation.created":
			if conversationCreatedSeen {
				return finish("extra_conversation", "event_correlation", errors.New("provider emitted more than one conversation.created event"))
			}
			eventID, parseErr := parseConversationCreated(updatedRaw)
			if parseErr != nil {
				return finish("schema_mismatch", "event_schema", parseErr)
			}
			tracker.eventIDs = append(tracker.eventIDs, eventID)
			conversationCreatedSeen = true
		default:
			return finish(outcomeForUnexpectedRealtimeEvent(updatedType), classForUnexpectedRealtimeEvent(updatedType), safeUnexpectedRealtimeEventError(updatedType))
		}
	}

sessionUpdated:
	receipt.SessionUpdateLatencyMS = time.Since(updateStarted).Milliseconds()

	appendEvent := map[string]any{
		"type":     "input_audio_buffer.append",
		"event_id": cfg.SegmentID + "-append",
		"audio":    base64.StdEncoding.EncodeToString(pcm),
	}
	commitEvent := map[string]any{
		"type":     "input_audio_buffer.commit",
		"event_id": cfg.SegmentID + "-commit",
	}
	if err := writeRealtimeJSON(conn, appendEvent); err != nil {
		return finish("transport_error", "write", err)
	}
	commitStarted := time.Now()
	if err := writeRealtimeJSON(conn, commitEvent); err != nil {
		return finish("transport_error", "write", err)
	}

	var commitItemID string
	var createdItemID string
	var finalizedItemID string
	var itemLifecycle string
	var itemDone bool
	var completed *realtimeCompleted
	for {
		raw, eventType, readErr := readRealtimeTranscriptionEvent(conn, tracker)
		if readErr != nil {
			return finish(realtimeReadOutcome(readErr), realtimeReadFailureClass(readErr), readErr)
		}
		switch eventType {
		case "conversation.created":
			// The server documents this as a session-lifecycle event emitted right
			// after session creation. It can be interleaved before the first commit,
			// but is not part of the committed audio item's lifecycle.
			if commitItemID != "" || conversationCreatedSeen {
				return finish("extra_conversation", "event_correlation", errors.New("unexpected conversation.created event after session initialization"))
			}
			eventID, parseErr := parseConversationCreated(raw)
			if parseErr != nil {
				return finish("schema_mismatch", "event_schema", parseErr)
			}
			tracker.eventIDs = append(tracker.eventIDs, eventID)
			conversationCreatedSeen = true
		case "input_audio_buffer.committed":
			if commitItemID != "" {
				return finish("extra_item", "event_correlation", errors.New("provider emitted more than one commit acknowledgement"))
			}
			itemID, eventID, parseErr := parseCommitAcknowledgement(raw)
			if parseErr != nil {
				return finish("schema_mismatch", "event_schema", parseErr)
			}
			tracker.eventIDs = append(tracker.eventIDs, eventID)
			commitItemID = itemID
			receipt.CommitItemIDSHA256 = digest(itemID)
			receipt.PreviousItemState = "root"
			if createdItemID != "" && createdItemID != commitItemID {
				return finish("correlation_mismatch", "event_correlation", errors.New("created item did not match the commit acknowledgement"))
			}
		case "conversation.item.created", "conversation.item.added":
			// The current event surface uses added -> done, while the legacy
			// committed-buffer contract still documents created as the completed
			// user-item event. Accept either lifecycle, never a mixture of both.
			if commitItemID == "" {
				return finish("out_of_order", "event_order", errors.New("conversation item arrived before the commit acknowledgement"))
			}
			if createdItemID != "" {
				return finish("extra_item", "event_correlation", errors.New("provider emitted more than one conversation item"))
			}
			itemID, eventID, parseErr := parseInputAudioConversationItem(raw, eventType)
			if parseErr != nil {
				return finish("schema_mismatch", "event_schema", parseErr)
			}
			if commitItemID != "" && itemID != commitItemID {
				return finish("correlation_mismatch", "event_correlation", errors.New("created item did not match the commit acknowledgement"))
			}
			tracker.eventIDs = append(tracker.eventIDs, eventID)
			createdItemID = itemID
			itemLifecycle = eventType
			itemDone = eventType == "conversation.item.created"
			receipt.CreatedItemIDSHA256 = digest(itemID)
			if itemDone {
				finalizedItemID = itemID
				receipt.ItemLifecycle = "legacy_created"
				receipt.FinalizedItemIDSHA256 = digest(itemID)
			} else {
				receipt.ItemLifecycle = "modern_added_done"
			}
		case "conversation.item.done":
			// A modern added event is only admission evidence. Require its one
			// correlated done event before the provider contract can pass.
			if createdItemID == "" || itemLifecycle != "conversation.item.added" {
				return finish("out_of_order", "event_order", errors.New("conversation item finalized without the modern added lifecycle"))
			}
			if itemDone {
				return finish("extra_item", "event_correlation", errors.New("provider finalized the conversation item more than once"))
			}
			itemID, eventID, parseErr := parseInputAudioConversationItem(raw, eventType)
			if parseErr != nil {
				return finish("schema_mismatch", "event_schema", parseErr)
			}
			if itemID != createdItemID || (commitItemID != "" && itemID != commitItemID) {
				return finish("correlation_mismatch", "event_correlation", errors.New("finalized item did not match the committed conversation item"))
			}
			tracker.eventIDs = append(tracker.eventIDs, eventID)
			itemDone = true
			finalizedItemID = itemID
			receipt.FinalizedItemIDSHA256 = digest(itemID)
		case "conversation.item.input_audio_transcription.delta":
			if commitItemID == "" || createdItemID == "" {
				return finish("out_of_order", "event_order", errors.New("transcription delta arrived before the committed conversation item"))
			}
			if completed != nil {
				return finish("post_terminal_event", "event_order", errors.New("transcription delta arrived after terminal completion"))
			}
			eventID, parseErr := parseTranscriptionDelta(raw, commitItemID)
			if parseErr != nil {
				return finish("correlation_mismatch", "event_correlation", parseErr)
			}
			tracker.eventIDs = append(tracker.eventIDs, eventID)
		case "conversation.item.input_audio_transcription.completed":
			if commitItemID == "" || createdItemID == "" {
				return finish("out_of_order", "event_order", errors.New("transcription completed before the committed conversation item"))
			}
			if completed != nil {
				return finish("duplicate_terminal", "event_terminal", errors.New("provider emitted more than one transcription completion event"))
			}
			parsedCompleted, parseErr := parseRealtimeTranscriptionCompleted(raw, commitItemID, duration)
			if parseErr != nil {
				if errors.Is(parseErr, errRealtimeCorrelation) {
					return finish("correlation_mismatch", "event_correlation", parseErr)
				}
				return finish("schema_mismatch", "event_schema", parseErr)
			}
			tracker.eventIDs = append(tracker.eventIDs, parsedCompleted.EventID)
			receipt.CompletedItemIDSHA256 = digest(parsedCompleted.ItemID)
			receipt.TranscriptSHA256 = digest(parsedCompleted.Transcript)
			receipt.NormalizedTranscriptChars = utf8.RuneCountInString(strings.Join(strings.Fields(parsedCompleted.Transcript), " "))
			receipt.ReportedUsageType = parsedCompleted.Usage.Type
			receipt.ReportedInputTokens = parsedCompleted.Usage.InputTokens
			receipt.ReportedAudioTokens = parsedCompleted.Usage.AudioTokens
			receipt.ReportedOutputTokens = parsedCompleted.Usage.OutputTokens
			receipt.ReportedTotalTokens = parsedCompleted.Usage.TotalTokens
			receipt.ReportedUsageSeconds = parsedCompleted.Usage.Seconds
			if parsedCompleted.Usage.Seconds != nil {
				receipt.ReportedDurationMS = int64(*parsedCompleted.Usage.Seconds * 1000)
				receipt.CostBasis = "provider_duration"
				receipt.ComputedCostUSD = costFor(time.Duration(*parsedCompleted.Usage.Seconds * float64(time.Second)))
			}
			if receipt.ComputedCostUSD > cfg.MaxUSD {
				return finish("cost_cap_exceeded", "post_call_cost_cap", errors.New("provider-reported cost basis exceeded --max-usd"))
			}
			receipt.TranscriptionLatencyMS = time.Since(commitStarted).Milliseconds()
			completed = &parsedCompleted
		case "error", "conversation.item.input_audio_transcription.failed":
			return finish("provider_error", "provider_event", safeUnexpectedRealtimeEventError(eventType))
		default:
			return finish(outcomeForUnexpectedRealtimeEvent(eventType), classForUnexpectedRealtimeEvent(eventType), safeUnexpectedRealtimeEventError(eventType))
		}
		if commitItemID != "" && createdItemID != "" && itemDone && completed != nil {
			receipt.ProviderIDSHA256 = digest(strings.Join([]string{receipt.ItemLifecycle, sessionID, commitItemID, createdItemID, finalizedItemID, completed.ItemID}, "\n"))
			receipt.CorrelationVerified = commitItemID == createdItemID && createdItemID == finalizedItemID && finalizedItemID == completed.ItemID &&
				receipt.CommitItemIDSHA256 == receipt.CreatedItemIDSHA256 && receipt.CreatedItemIDSHA256 == receipt.FinalizedItemIDSHA256 &&
				receipt.FinalizedItemIDSHA256 == receipt.CompletedItemIDSHA256 &&
				(receipt.ItemLifecycle == "legacy_created" || receipt.ItemLifecycle == "modern_added_done")
			if !receipt.CorrelationVerified {
				return finish("correlation_mismatch", "event_correlation", errors.New("provider item identifiers did not correlate"))
			}
			if err := verifyNoPostTerminalRealtimeEvent(runCtx, conn, tracker); err != nil {
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
		}
	}
}

func validateRealtimeTranscriptionConfig(cfg RealtimeTranscribeConfig) error {
	if err := validateCommon(cfg.Config); err != nil {
		return err
	}
	if cfg.BaseURL != "" {
		return errors.New("HTTP BaseURL does not control the Realtime probe; use WebSocketURL explicitly")
	}
	if cfg.Client != nil {
		return errors.New("HTTP Client is not accepted by the Realtime WebSocket probe")
	}
	if err := validateProjectBinding(cfg.Config); err != nil {
		return err
	}
	if cfg.Model != TranscribeModel {
		return fmt.Errorf("realtime transcription probe only permits model %q", TranscribeModel)
	}
	if cfg.MaxUSD <= 0 || cfg.MaxUSD > MaxProbeUSD {
		return fmt.Errorf("--max-usd must be greater than 0 and no more than %.2f", MaxProbeUSD)
	}
	if err := validateRealtimeSegmentID(cfg.SegmentID); err != nil {
		return err
	}
	endpoint := cfg.WebSocketURL
	if endpoint == "" {
		endpoint = DefaultRealtimeTranscriptionURL
	}
	return validateRealtimeWebSocketURL(endpoint)
}

func validateRealtimeSegmentID(id string) error {
	if len(id) < 16 || len(id) > 128 {
		return errors.New("segment ID must contain 16 to 128 URL-safe characters")
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return errors.New("segment ID must contain only ASCII letters, digits, hyphen, or underscore")
	}
	return nil
}

func validateRealtimeWebSocketURL(value string) error {
	u, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("invalid Realtime WebSocket URL: %w", err)
	}
	if u.User != nil || u.Fragment != "" || u.Path != "/v1/realtime" || u.RawQuery != "intent=transcription" {
		return errors.New("Realtime WebSocket URL must use /v1/realtime?intent=transcription without user info or fragment")
	}
	if u.Scheme == "wss" && u.Host == "api.openai.com" {
		return nil
	}
	host := u.Hostname()
	if (u.Scheme == "ws" || u.Scheme == "wss") && (host == "localhost" || net.ParseIP(host).IsLoopback()) {
		return nil
	}
	return errors.New("Realtime WebSocket URL must be wss://api.openai.com/v1/realtime?intent=transcription (loopback is test-only)")
}

func boundedRealtimeContext(parent context.Context) (context.Context, context.CancelFunc) {
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) <= RealtimeProbeTimeout {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, RealtimeProbeTimeout)
}

func realtimeSessionUpdate(segmentID string) map[string]any {
	return map[string]any{
		"type":     "session.update",
		"event_id": segmentID + "-session",
		"session": map[string]any{
			"type": "transcription",
			"audio": map[string]any{
				"input": map[string]any{
					"format": map[string]any{"type": "audio/pcm", "rate": 24000},
					"transcription": map[string]any{
						"model":     TranscribeModel,
						"prompt":    fixedPrompt,
						"keywords":  []string{fixedKeyword},
						"languages": []string{fixedLanguage},
					},
					"turn_detection": nil,
				},
			},
		},
	}
}

func canonicalRealtimeTranscriptionShape(fixtureSHA, pcmSHA, referenceSHA, segmentSHA string) string {
	return strings.Join([]string{
		"e10-realtime-transcription-shape-v1",
		"endpoint=/v1/realtime?intent=transcription",
		"session_type=transcription",
		"format=audio/pcm",
		"rate=24000",
		"model=" + TranscribeModel,
		"prompt_sha256=" + digest(fixedPrompt),
		"keyword_sha256=" + digest(fixedKeyword),
		"language_sha256=" + digest(fixedLanguage),
		"turn_detection=null",
		"append_count=1",
		"commit_count=1",
		"fixture_sha256=" + fixtureSHA,
		"pcm_sha256=" + pcmSHA,
		"reference_sha256=" + referenceSHA,
		"segment_sha256=" + segmentSHA,
	}, "\n")
}

func realtimeTranscriptionPriceDeclaration() string {
	return strings.Join([]string{
		"official-pricing-declaration-v1",
		"source=" + priceSourceURL,
		"revision=" + priceSourceRevision,
		"model=" + TranscribeModel,
		"operation=transcribe-realtime-committed-turn",
		"unit=usd_per_minute",
		"rate=0.0045",
	}, "\n")
}

func extractApprovedPCM24Mono16(wav []byte, approvedDuration time.Duration) ([]byte, error) {
	if len(wav) < 12 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return nil, errors.New("approved fixture must be a RIFF/WAVE PCM file")
	}
	if int(binary.LittleEndian.Uint32(wav[4:8]))+8 != len(wav) {
		return nil, errors.New("approved fixture RIFF size is incoherent")
	}
	var pcm []byte
	seenFormat := false
	for offset := 12; offset < len(wav); {
		if len(wav)-offset < 8 {
			return nil, errors.New("approved fixture contains a truncated WAV chunk")
		}
		kind := string(wav[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(wav[offset+4 : offset+8]))
		start := offset + 8
		end := start + size
		if size < 0 || end < start || end > len(wav) {
			return nil, errors.New("approved fixture WAV chunk exceeds the file")
		}
		switch kind {
		case "fmt ":
			if seenFormat || size < 16 {
				return nil, errors.New("approved fixture must contain one valid fmt chunk")
			}
			format := wav[start:end]
			if binary.LittleEndian.Uint16(format[0:2]) != 1 ||
				binary.LittleEndian.Uint16(format[2:4]) != 1 ||
				binary.LittleEndian.Uint32(format[4:8]) != 24000 ||
				binary.LittleEndian.Uint32(format[8:12]) != 48000 ||
				binary.LittleEndian.Uint16(format[12:14]) != 2 ||
				binary.LittleEndian.Uint16(format[14:16]) != 16 {
				return nil, errors.New("approved fixture must be mono 24 kHz signed 16-bit PCM")
			}
			seenFormat = true
		case "data":
			if pcm != nil || size == 0 || size%2 != 0 {
				return nil, errors.New("approved fixture must contain one non-empty frame-aligned data chunk")
			}
			pcm = append([]byte(nil), wav[start:end]...)
		}
		offset = end
		if size%2 == 1 {
			offset++
		}
		if offset > len(wav) {
			return nil, errors.New("approved fixture contains invalid WAV padding")
		}
	}
	if !seenFormat || len(pcm) == 0 {
		return nil, errors.New("approved fixture is missing its PCM format or data")
	}
	pcmDuration := time.Duration(float64(len(pcm)) / 48000 * float64(time.Second))
	if pcmDuration <= 0 || pcmDuration != approvedDuration {
		return nil, errors.New("approved fixture PCM data duration is incoherent")
	}
	return pcm, nil
}

func writeRealtimeJSON(conn *websocket.Conn, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return errors.New("could not encode bounded Realtime client event")
	}
	if err := conn.WriteMessage(websocket.TextMessage, encoded); err != nil {
		return errors.New("could not write bounded Realtime client event")
	}
	return nil
}

func readRealtimeEvent(conn *websocket.Conn, tracker *realtimeEventTracker) ([]byte, string, error) {
	messageType, raw, err := conn.ReadMessage()
	if err != nil {
		if websocket.IsCloseError(err, websocket.CloseMessageTooBig) || strings.Contains(err.Error(), "read limit") {
			return nil, "", errRealtimeEventCap
		}
		if networkErr, ok := err.(net.Error); ok && networkErr.Timeout() {
			return nil, "", errRealtimeReadTimeout
		}
		if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
			return nil, "", errRealtimeTerminalClosed
		}
		return nil, "", errors.New("could not read Realtime server event")
	}
	if messageType != websocket.TextMessage {
		return nil, "", errors.New("Realtime server event was not a text frame")
	}
	tracker.count++
	tracker.bytes += int64(len(raw))
	if tracker.count > MaxRealtimeServerEvents || tracker.bytes > MaxRealtimeServerEventBytes {
		return nil, "", errRealtimeEventCap
	}
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || strings.TrimSpace(envelope.Type) == "" {
		return nil, "", errors.New("Realtime server event was not the documented JSON envelope")
	}
	tracker.order = append(tracker.order, envelope.Type)
	return raw, envelope.Type, nil
}

var errRealtimeEventCap = errors.New("Realtime server event count or byte cap exceeded")
var errRealtimeCorrelation = errors.New("Realtime provider item correlation failed")
var errRealtimeDuplicateEvent = errors.New("Realtime server event_id was not unique")
var errRealtimeReadTimeout = errors.New("Realtime server event observation timed out")
var errRealtimeTerminalClosed = errors.New("Realtime server closed after terminal event")
var errRealtimeDuplicateTerminal = errors.New("Realtime server emitted a duplicate terminal event")
var errRealtimePostTerminalEvent = errors.New("Realtime server emitted an event after terminal completion")

// readRealtimeTranscriptionEvent adds transcription-probe-only validation to
// the shared Realtime frame reader. Scout uses the shared reader too, and some
// of its advisory events may omit event_id; every transcription server event
// used by this contract documents a unique event_id.
func readRealtimeTranscriptionEvent(conn *websocket.Conn, tracker *realtimeEventTracker) ([]byte, string, error) {
	raw, eventType, err := readRealtimeEvent(conn, tracker)
	if err != nil {
		return nil, "", err
	}
	var envelope struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || strings.TrimSpace(envelope.EventID) == "" {
		return nil, "", errors.New("Realtime transcription server event did not include a documented event_id")
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

// verifyNoPostTerminalRealtimeEvent gives a terminal completion a small,
// bounded observation window. A dedicated transcription session has exactly
// one committed item in this probe, so any subsequent server event is either
// a duplicate terminal or an unexpected lifecycle event. A normal close is
// accepted because the provider may close an otherwise completed probe.
func verifyNoPostTerminalRealtimeEvent(ctx context.Context, conn *websocket.Conn, tracker *realtimeEventTracker) error {
	_ = conn.SetReadDeadline(time.Now().Add(realtimeTerminalObserveWindow))
	_, eventType, err := readRealtimeTranscriptionEvent(conn, tracker)
	if err != nil {
		// The completion is already correlated and usage-bounded. Only the
		// bounded observation timeout, a documented normal close, or actual
		// context cancellation can end the drain successfully. Malformed frames,
		// missing IDs, caps, duplicate IDs, and generic transport failures remain
		// contract failures even after the terminal event.
		if errors.Is(err, errRealtimeReadTimeout) || errors.Is(err, errRealtimeTerminalClosed) || ctx.Err() != nil {
			return nil
		}
		return err
	}
	if eventType == "conversation.item.input_audio_transcription.completed" {
		return errRealtimeDuplicateTerminal
	}
	return errRealtimePostTerminalEvent
}

func parseConversationCreated(raw []byte) (string, error) {
	var event struct {
		Type         string `json:"type"`
		EventID      string `json:"event_id"`
		Conversation struct {
			Object string `json:"object"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(raw, &event); err != nil || event.Type != "conversation.created" || event.EventID == "" ||
		(event.Conversation.Object != "" && event.Conversation.Object != "realtime.conversation") {
		return "", errors.New("conversation.created did not match the documented session lifecycle schema")
	}
	return event.EventID, nil
}

func parseSessionCreated(raw []byte) (string, string, error) {
	var event struct {
		Type    string `json:"type"`
		EventID string `json:"event_id"`
		Session struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"session"`
	}
	if err := json.Unmarshal(raw, &event); err != nil || event.Type != "session.created" || event.EventID == "" || event.Session.ID == "" || event.Session.Type != "transcription" {
		return "", "", errors.New("session.created did not match the transcription-session schema")
	}
	return event.Session.ID, event.EventID, nil
}

func parseSessionUpdated(raw []byte, sessionID string) (string, error) {
	var event struct {
		Type    string `json:"type"`
		EventID string `json:"event_id"`
		Session struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			Audio *struct {
				Input *struct {
					Format *struct {
						Type *string `json:"type"`
						Rate *int    `json:"rate"`
					} `json:"format"`
					Transcription *struct {
						Language  *string   `json:"language"`
						Languages *[]string `json:"languages"`
						Model     *string   `json:"model"`
						Prompt    *string   `json:"prompt"`
					} `json:"transcription"`
					TurnDetection json.RawMessage `json:"turn_detection"`
				} `json:"input"`
			} `json:"audio"`
		} `json:"session"`
	}
	if err := json.Unmarshal(raw, &event); err != nil || event.Type != "session.updated" || event.EventID == "" || event.Session.ID != sessionID || event.Session.Type != "transcription" {
		return "", errors.New("session.updated did not identify the expected transcription session")
	}
	// The documented transcription-session response marks every audio field as
	// optional and does not expose keywords at all. session.updated itself is
	// the acknowledgement that the update was applied unless an error event is
	// returned. Validate every field the provider does echo, while accepting a
	// schema-conformant omission instead of inventing an exact-echo guarantee.
	if audio := event.Session.Audio; audio != nil && audio.Input != nil {
		input := audio.Input
		if input.Format != nil {
			if input.Format.Type != nil && *input.Format.Type != "audio/pcm" {
				return "", errors.New("session.updated echoed a different input audio format")
			}
			if input.Format.Rate != nil && *input.Format.Rate != 24000 {
				return "", errors.New("session.updated echoed a different input audio rate")
			}
		}
		if transcription := input.Transcription; transcription != nil {
			if transcription.Model != nil && *transcription.Model != TranscribeModel {
				return "", errors.New("session.updated echoed a different transcription model")
			}
			if transcription.Prompt != nil && *transcription.Prompt != fixedPrompt {
				return "", errors.New("session.updated echoed a different transcription prompt")
			}
			if transcription.Languages != nil && (len(*transcription.Languages) != 1 || (*transcription.Languages)[0] != fixedLanguage) {
				return "", errors.New("session.updated echoed different transcription languages")
			}
			if transcription.Language != nil && *transcription.Language != fixedLanguage {
				return "", errors.New("session.updated echoed a different legacy transcription language")
			}
		}
		if trimmed := bytes.TrimSpace(input.TurnDetection); len(trimmed) != 0 && !bytes.Equal(trimmed, []byte("null")) {
			return "", errors.New("session.updated enabled turn detection for the committed-turn probe")
		}
	}
	return event.EventID, nil
}

func parseCommitAcknowledgement(raw []byte) (string, string, error) {
	var event struct {
		Type           string          `json:"type"`
		EventID        string          `json:"event_id"`
		ItemID         string          `json:"item_id"`
		PreviousItemID json.RawMessage `json:"previous_item_id"`
	}
	if err := json.Unmarshal(raw, &event); err != nil || event.Type != "input_audio_buffer.committed" || event.EventID == "" || event.ItemID == "" {
		return "", "", errors.New("commit acknowledgement did not match the documented schema")
	}
	if !realtimeOptionalRoot(event.PreviousItemID) {
		return "", "", errors.New("first committed item did not have a valid root previous_item_id")
	}
	return event.ItemID, event.EventID, nil
}

func parseInputAudioConversationItem(raw []byte, expectedType string) (string, string, error) {
	if expectedType != "conversation.item.created" && expectedType != "conversation.item.added" && expectedType != "conversation.item.done" {
		return "", "", errors.New("conversation item event type was not permitted")
	}
	var event struct {
		Type           string          `json:"type"`
		EventID        string          `json:"event_id"`
		PreviousItemID json.RawMessage `json:"previous_item_id"`
		Item           struct {
			ID      string          `json:"id"`
			Object  string          `json:"object"`
			Type    string          `json:"type"`
			Status  string          `json:"status"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"item"`
	}
	if err := json.Unmarshal(raw, &event); err != nil || event.Type != expectedType || event.EventID == "" ||
		event.Item.ID == "" ||
		(event.Item.Object != "" && event.Item.Object != "realtime.item") || event.Item.Type != "message" ||
		(event.Item.Status != "" && event.Item.Status != "completed") || event.Item.Role != "user" {
		return "", "", errors.New("conversation item lifecycle event did not match the committed user audio item")
	}
	content := bytes.TrimSpace(event.Item.Content)
	if len(content) == 0 || bytes.Equal(content, []byte("null")) {
		return "", "", errors.New("conversation item lifecycle event omitted the required content array")
	}
	var parts []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(content, &parts); err != nil {
		return "", "", errors.New("conversation item lifecycle content was not an array")
	}
	if expectedType != "conversation.item.created" && (len(parts) != 1 || parts[0].Type != "input_audio") {
		return "", "", errors.New("modern conversation item did not contain the committed input audio part")
	}
	if !realtimeOptionalRoot(event.PreviousItemID) {
		return "", "", errors.New("conversation item lifecycle event did not preserve the root previous_item_id chain")
	}
	return event.Item.ID, event.EventID, nil
}

func realtimeOptionalRoot(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

func parseTranscriptionDelta(raw []byte, expectedItemID string) (string, error) {
	var event struct {
		Type         string `json:"type"`
		EventID      string `json:"event_id"`
		ItemID       string `json:"item_id"`
		ContentIndex int    `json:"content_index"`
		Delta        string `json:"delta"`
	}
	if err := json.Unmarshal(raw, &event); err != nil || event.Type != "conversation.item.input_audio_transcription.delta" || event.EventID == "" || event.ItemID != expectedItemID || event.ContentIndex != 0 || event.Delta == "" {
		return "", errors.New("transcription delta did not correlate to the committed item")
	}
	return event.EventID, nil
}

type realtimeCompleted struct {
	EventID    string
	ItemID     string
	Transcript string
	Usage      usageSummary
}

func parseRealtimeTranscriptionCompleted(raw []byte, expectedItemID string, localDuration time.Duration) (realtimeCompleted, error) {
	var event struct {
		Type         string          `json:"type"`
		EventID      string          `json:"event_id"`
		ItemID       string          `json:"item_id"`
		ContentIndex int             `json:"content_index"`
		Transcript   string          `json:"transcript"`
		Usage        json.RawMessage `json:"usage"`
		Languages    json.RawMessage `json:"languages"`
	}
	if err := json.Unmarshal(raw, &event); err != nil || event.Type != "conversation.item.input_audio_transcription.completed" || event.EventID == "" || event.ItemID == "" || event.ContentIndex != 0 || strings.TrimSpace(event.Transcript) == "" {
		return realtimeCompleted{}, errors.New("transcription completion did not match the committed item schema")
	}
	if event.ItemID != expectedItemID {
		return realtimeCompleted{}, fmt.Errorf("%w: completion item did not match the commit acknowledgement", errRealtimeCorrelation)
	}
	usage, err := parseStrictRealtimeUsage(event.Usage, localDuration)
	if err != nil {
		return realtimeCompleted{}, fmt.Errorf("transcription completion usage was invalid: %w", err)
	}
	if len(event.Languages) > 0 && string(event.Languages) != "null" {
		var languages []struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(event.Languages, &languages); err != nil {
			return realtimeCompleted{}, errors.New("transcription completion languages were invalid")
		}
		seen := make(map[string]bool, len(languages))
		for _, language := range languages {
			if strings.TrimSpace(language.Code) == "" || seen[language.Code] {
				return realtimeCompleted{}, errors.New("transcription completion languages were invalid")
			}
			seen[language.Code] = true
		}
	}
	return realtimeCompleted{EventID: event.EventID, ItemID: event.ItemID, Transcript: event.Transcript, Usage: usage}, nil
}

func parseStrictRealtimeUsage(raw json.RawMessage, localDuration time.Duration) (usageSummary, error) {
	var kind struct {
		Type string `json:"type"`
	}
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &kind) != nil {
		return usageSummary{}, errors.New("usage is required")
	}
	switch kind.Type {
	case "tokens":
		var usage struct {
			Type              string `json:"type"`
			InputTokens       *int64 `json:"input_tokens"`
			OutputTokens      *int64 `json:"output_tokens"`
			TotalTokens       *int64 `json:"total_tokens"`
			InputTokenDetails *struct {
				TextTokens  *int64 `json:"text_tokens"`
				AudioTokens *int64 `json:"audio_tokens"`
			} `json:"input_token_details"`
		}
		if err := decodeStrictJSON(raw, &usage); err != nil {
			return usageSummary{}, err
		}
		if usage.InputTokens == nil || usage.OutputTokens == nil || usage.TotalTokens == nil {
			return usageSummary{}, errors.New("token usage requires input, output, and total counts")
		}
		counts := []*int64{usage.InputTokens, usage.OutputTokens, usage.TotalTokens}
		if usage.InputTokenDetails != nil {
			if usage.InputTokenDetails.TextTokens != nil {
				counts = append(counts, usage.InputTokenDetails.TextTokens)
			}
			if usage.InputTokenDetails.AudioTokens != nil {
				counts = append(counts, usage.InputTokenDetails.AudioTokens)
			}
		}
		for _, count := range counts {
			if *count < 0 || *count > MaxProbeUsageTokens {
				return usageSummary{}, errors.New("token usage count is outside the bounded range")
			}
		}
		if *usage.TotalTokens != *usage.InputTokens+*usage.OutputTokens {
			return usageSummary{}, errors.New("token usage totals are inconsistent")
		}
		var audioTokens *int64
		if usage.InputTokenDetails != nil {
			if usage.InputTokenDetails.TextTokens != nil && *usage.InputTokenDetails.TextTokens > *usage.InputTokens {
				return usageSummary{}, errors.New("token usage text details exceed input tokens")
			}
			if usage.InputTokenDetails.AudioTokens != nil && *usage.InputTokenDetails.AudioTokens > *usage.InputTokens {
				return usageSummary{}, errors.New("token usage audio details exceed input tokens")
			}
			if usage.InputTokenDetails.TextTokens != nil && usage.InputTokenDetails.AudioTokens != nil &&
				*usage.InputTokens != *usage.InputTokenDetails.TextTokens+*usage.InputTokenDetails.AudioTokens {
				return usageSummary{}, errors.New("token usage input details are inconsistent")
			}
			audioTokens = usage.InputTokenDetails.AudioTokens
		}
		return usageSummary{
			Type: usage.Type, InputTokens: usage.InputTokens, AudioTokens: audioTokens,
			OutputTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens,
		}, nil
	case "duration":
		var usage struct {
			Type    string  `json:"type"`
			Seconds float64 `json:"seconds"`
		}
		if err := decodeStrictJSON(raw, &usage); err != nil {
			return usageSummary{}, err
		}
	default:
		return usageSummary{}, errors.New("usage type must be tokens or duration")
	}
	return parseUsage(raw, localDuration)
}

func decodeStrictJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("usage contained fields outside the documented schema")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("usage was not exactly one JSON object")
	}
	return nil
}

func captureRealtimeResponseHeaders(receipt *RealtimeTranscriptionReceipt, headers http.Header) {
	if value := headers.Get("X-Request-ID"); value != "" {
		receipt.RequestIDSHA256 = digest(value)
	}
	if value := headers.Get("OpenAI-Project"); value != "" {
		receipt.ResponseProjectSHA256 = digest(value)
	}
	if value := headers.Get("OpenAI-Organization"); value != "" {
		receipt.ResponseOrganizationSHA256 = digest(value)
	}
}

func verifyRealtimeAttribution(receipt *RealtimeTranscriptionReceipt) error {
	if receipt.ResponseProjectSHA256 == "" {
		return nil
	}
	if receipt.RequestProjectSHA256 == "" || receipt.ExpectedProjectSHA256 == "" {
		return nil
	}
	if receipt.ResponseProjectSHA256 != receipt.RequestProjectSHA256 || receipt.ResponseProjectSHA256 != receipt.ExpectedProjectSHA256 {
		return errors.New("provider project attribution echo did not match request-bound project")
	}
	receipt.AttributionVerified = true
	receipt.AttributionState = "provider_verified"
	return nil
}

func writeRealtimeTranscriptionReceipt(dir string, receipt RealtimeTranscriptionReceipt) error {
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "receipt.json")
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func realtimeEndpointLabel(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "/v1/realtime?intent=transcription"
	}
	return u.Path + "?" + u.RawQuery
}

func realtimeReadOutcome(err error) string {
	if errors.Is(err, errRealtimeEventCap) {
		return "event_cap_exceeded"
	}
	if errors.Is(err, errRealtimeDuplicateEvent) {
		return "duplicate_event"
	}
	return "transport_error"
}

func realtimeReadFailureClass(err error) string {
	if errors.Is(err, errRealtimeEventCap) {
		return "event_cap"
	}
	if errors.Is(err, errRealtimeDuplicateEvent) {
		return "event_id"
	}
	return "read"
}

func outcomeForUnexpectedRealtimeEvent(eventType string) string {
	if eventType == "error" || eventType == "conversation.item.input_audio_transcription.failed" {
		return "provider_error"
	}
	if eventType == "conversation.item.created" {
		return "extra_item"
	}
	return "out_of_order"
}

func classForUnexpectedRealtimeEvent(eventType string) string {
	if eventType == "error" || eventType == "conversation.item.input_audio_transcription.failed" {
		return "provider_event"
	}
	if eventType == "conversation.item.created" {
		return "event_correlation"
	}
	return "event_order"
}

func safeUnexpectedRealtimeEventError(eventType string) error {
	switch eventType {
	case "error", "conversation.item.input_audio_transcription.failed":
		return errors.New("provider emitted a documented failure event")
	default:
		return errors.New("provider emitted an unexpected Realtime event")
	}
}

func failureClassForRealtimeHTTP(status int) string {
	if status == http.StatusSwitchingProtocols {
		return "websocket_upgrade"
	}
	return failureClass(status)
}
