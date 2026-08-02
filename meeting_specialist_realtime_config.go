package main

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"
)

const (
	meetingSpecialistRealtimeModel           = "gpt-realtime-2.1"
	meetingSpecialistRealtimeEndpoint        = "wss://api.openai.com/v1/realtime?model=gpt-realtime-2.1"
	meetingSpecialistRealtimeSampleRate      = 24000
	meetingSpecialistRealtimeMaxContextBytes = 64 << 10
	meetingSpecialistRealtimeMaxEventBytes   = 2 << 20
	meetingSpecialistRealtimeMaxEvents       = 4096
	meetingSpecialistRealtimeMaxAudioBytes   = 8 << 20
	// gpt-realtime-2.1 documents a 128k context window, but no provider-side
	// input-token cap for direct PCM. Every direct-PCM response therefore
	// reserves the full context window. This intentionally leaves the normal
	// 1,500-token specialist profile unable to generate until the product uses
	// authoritative transcript text input or OpenAI documents a hard input cap.
	meetingSpecialistRealtimeContextWindowTokens = 128000
	meetingSpecialistRealtimeWriteTimeout        = 2 * time.Second
	meetingSpecialistRealtimeProtocolSource      = "https://developers.openai.com/api/docs/guides/realtime-websocket"
	meetingSpecialistRealtimeModelSource         = "https://developers.openai.com/api/docs/models/gpt-realtime-2.1"
)

var (
	ErrMeetingSpecialistProviderDisabled = errors.New("meeting specialist Realtime provider is disabled")
	ErrMeetingSpecialistProviderConfig   = errors.New("meeting specialist Realtime provider configuration is invalid")
	ErrMeetingSpecialistProviderProtocol = errors.New("meeting specialist Realtime provider protocol failed closed")
	ErrMeetingSpecialistProviderBudget   = errors.New("meeting specialist Realtime provider budget is exhausted")
)

// MeetingSpecialistRealtimeConfig is server-owned configuration. APIKey is
// captured by the provider factory and is never copied into a launch, context
// envelope, receipt, browser response, or provider event.
//
// Enabled has no environment default. The application intentionally installs
// a disabled instance; a later release must construct an enabled config after
// its provider, consent, custody, pricing, and rollout gates have passed.
type MeetingSpecialistRealtimeConfig struct {
	Enabled          bool
	APIKey           string
	SafetyIdentifier string
	Model            string
	ReasoningEffort  string
	Voice            string
	MaxOutputTokens  int64
	MaxContextBytes  int
	MaxEventBytes    int64
	MaxEvents        int
	MaxAudioBytes    int64
	Now              func() time.Time
	ResolveBrief     MeetingSpecialistRealtimeBriefResolver

	dial meetingSpecialistRealtimeDialer
}

type MeetingSpecialistRealtimeBriefEvidence struct {
	Reference STRIDEReference `json:"reference"`
	Text      string          `json:"text"`
}

type MeetingSpecialistRealtimeBrief struct {
	Purpose  string                                   `json:"purpose"`
	Evidence []MeetingSpecialistRealtimeBriefEvidence `json:"evidence"`
}

type MeetingSpecialistRealtimeBriefResolver func(context.Context, MeetingSpecialistLaunch) (MeetingSpecialistRealtimeBrief, error)

type meetingSpecialistRealtimeConn interface {
	WriteJSON(any) error
	ReadMessage() (int, []byte, error)
	SetReadLimit(int64)
	SetWriteDeadline(time.Time) error
	Close() error
}

type meetingSpecialistRealtimeDialer func(context.Context, string, http.Header) (meetingSpecialistRealtimeConn, error)

func defaultOffMeetingSpecialistRealtimeConfig() MeetingSpecialistRealtimeConfig {
	return MeetingSpecialistRealtimeConfig{
		Model:           meetingSpecialistRealtimeModel,
		ReasoningEffort: "high",
		Voice:           defaultRealtimeVoice,
		MaxOutputTokens: 256,
		MaxContextBytes: meetingSpecialistRealtimeMaxContextBytes,
		MaxEventBytes:   meetingSpecialistRealtimeMaxEventBytes,
		MaxEvents:       meetingSpecialistRealtimeMaxEvents,
		MaxAudioBytes:   meetingSpecialistRealtimeMaxAudioBytes,
	}
}

func (config MeetingSpecialistRealtimeConfig) normalized() MeetingSpecialistRealtimeConfig {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.MaxContextBytes == 0 {
		config.MaxContextBytes = meetingSpecialistRealtimeMaxContextBytes
	}
	if config.MaxEventBytes == 0 {
		config.MaxEventBytes = meetingSpecialistRealtimeMaxEventBytes
	}
	if config.MaxEvents == 0 {
		config.MaxEvents = meetingSpecialistRealtimeMaxEvents
	}
	if config.MaxAudioBytes == 0 {
		config.MaxAudioBytes = meetingSpecialistRealtimeMaxAudioBytes
	}
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.SafetyIdentifier = strings.TrimSpace(config.SafetyIdentifier)
	config.Model = strings.TrimSpace(config.Model)
	config.ReasoningEffort = strings.ToLower(strings.TrimSpace(config.ReasoningEffort))
	config.Voice = strings.TrimSpace(config.Voice)
	return config
}

func (config MeetingSpecialistRealtimeConfig) validate(launch MeetingSpecialistLaunch) error {
	config = config.normalized()
	if !config.Enabled {
		return ErrMeetingSpecialistProviderDisabled
	}
	if config.APIKey == "" || config.ResolveBrief == nil || config.SafetyIdentifier != "" && !isHexDigest(config.SafetyIdentifier) || config.Model != meetingSpecialistRealtimeModel || !oneOf(config.ReasoningEffort, "low", "medium", "high") ||
		config.Voice == "" || config.MaxOutputTokens <= 0 || config.MaxOutputTokens > 4096 ||
		config.MaxOutputTokens > launch.Context.TokenBudget || config.MaxOutputTokens > launch.ApprovalLimits.TokenBudget ||
		config.MaxContextBytes <= 0 || config.MaxContextBytes > meetingSpecialistRealtimeMaxContextBytes ||
		config.MaxEventBytes <= 0 || config.MaxEventBytes > meetingSpecialistRealtimeMaxEventBytes ||
		config.MaxEvents <= 0 || config.MaxEvents > meetingSpecialistRealtimeMaxEvents ||
		config.MaxAudioBytes <= 0 || config.MaxAudioBytes > meetingSpecialistRealtimeMaxAudioBytes {
		return ErrMeetingSpecialistProviderConfig
	}
	if launch.Validate(config.Now().UTC()) != nil {
		return ErrMeetingSpecialistUnauthorized
	}
	if _, _, ok := meetingSpecialistRealtimeResponseAdmission(config, launch, 0, 0); !ok {
		return ErrMeetingSpecialistProviderConfig
	}
	return nil
}

// meetingSpecialistRealtimeResponseAdmission is the single pre-provider
// token/cost authority used both before dialing and before every response.
// Past reconciled usage is charged against the cumulative envelopes. The next
// direct-PCM turn reserves the full documented context window as audio input;
// no duration-to-token ratio is inferred.
func meetingSpecialistRealtimeResponseAdmission(config MeetingSpecialistRealtimeConfig, launch MeetingSpecialistLaunch, usedTokens, usedCostCents int64) (int64, int64, bool) {
	if usedTokens < 0 || usedCostCents < 0 {
		return 0, 0, false
	}
	tokenLimit := launch.Context.TokenBudget
	if launch.ApprovalLimits.TokenBudget < tokenLimit {
		tokenLimit = launch.ApprovalLimits.TokenBudget
	}
	remainingTokens := tokenLimit - usedTokens
	maxOutput := remainingTokens - meetingSpecialistRealtimeContextWindowTokens
	if config.MaxOutputTokens < maxOutput {
		maxOutput = config.MaxOutputTokens
	}
	if maxOutput <= 0 {
		return 0, 0, false
	}
	costLimit := launch.Context.CostBudgetCents
	if launch.ApprovalLimits.CostBudgetCents < costLimit {
		costLimit = launch.ApprovalLimits.CostBudgetCents
	}
	if launch.Policy.CostBudgetCents < costLimit {
		costLimit = launch.Policy.CostBudgetCents
	}
	remainingCost := costLimit - usedCostCents
	if remainingCost < 0 {
		return 0, 0, false
	}
	costCeiling := func(outputTokens int64) (int64, bool) {
		costUSD, priced := estimateCostUSDAt(config.Model, config.Now().UTC(), llmTokenUsage{
			AudioInputTokens:  meetingSpecialistRealtimeContextWindowTokens,
			AudioOutputTokens: outputTokens,
		})
		if !priced {
			return 0, false
		}
		return int64(math.Ceil(costUSD * 100)), true
	}
	if cents, priced := costCeiling(1); !priced || cents > remainingCost {
		return 0, 0, false
	}
	low, high, admittedOutput := int64(1), maxOutput, int64(1)
	for low <= high {
		middle := low + (high-low)/2
		cents, priced := costCeiling(middle)
		if !priced {
			return 0, 0, false
		}
		if cents <= remainingCost {
			admittedOutput = middle
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	admittedCost, priced := costCeiling(admittedOutput)
	if !priced {
		return 0, 0, false
	}
	return admittedOutput, admittedCost, true
}
