package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Chat surfaces migrate to Sonnet 5 (packaging-os-analysis §1 role matrix,
// Wave 2 item 7): scout chat Q&A, the memory Q&A path, and agent-thread
// follow-ups ride claude-sonnet-5 whenever an Anthropic key is present, and
// keep today's gpt-5.5 Responses path byte-for-byte when it is not — keyless
// deploys are unchanged. This file is the small text-in/text-out wrapper over
// the EXISTING Messages wire layer in agent_runner_anthropic.go (request
// marshaling + SSE accumulation); it adds no new wire code.
const (
	defaultChatModel = "claude-sonnet-5"
	// agentThreadWorkerAnthropic mirrors agentThreadWorkerOpenAI
	// (codex_runner.go): the honest worker stamp when Sonnet wrote the artifact.
	agentThreadWorkerAnthropic = "anthropic_text_response"
	// Re-baselined token budgets: Sonnet 5's tokenizer counts ~30% more tokens
	// for the same text than the gpt-5.5 budgets these replace (chat 500/700,
	// follow-ups 2600), so the ceilings are raised rather than copied.
	anthropicChatMaxTokens     = 800
	anthropicFollowUpMaxTokens = 3000
)

// chatModel is the conversational-surface model, distinct from the
// orchestrator's (orchestratorModel) and the ambient workers'
// (meetingBrainModel) dials. Chat is a worker seat under the routing doctrine
// (agent_runner_anthropic.go: Sonnet/Opus tier only, never Haiku), so the env
// dial rides the same guard as the orchestrator/review/fallback dials.
func chatModel() string {
	return doctrineModelOrDefault("BONFIRE_CHAT_MODEL", defaultChatModel)
}

// anthropicTextRequest mirrors openAITextRequest: one instruction block, one
// input string, one text answer. Effort maps to output_config.effort — the
// thinking-depth dial on Sonnet 5 (budget_tokens is rejected there).
type anthropicTextRequest struct {
	Model        string
	Instructions string
	Input        string
	Effort       string
	MaxTokens    int
	// Attachments are pre-built image/document content blocks (card 085,
	// attachmentContentBlocks) placed BEFORE the text block in the single
	// user turn — the documented block order for vision/document requests.
	// The keyless OpenAI fallback paths never see this field, so keyless
	// deploys keep today's text-only behavior byte-for-byte.
	Attachments []json.RawMessage
}

// anthropicTextResponder mirrors openAITextResponder: the injectable seam so
// chat seams are testable with no network.
type anthropicTextResponder func(context.Context, string, anthropicTextRequest) (string, error)

var createAnthropicTextResponse anthropicTextResponder = createAnthropicTextResponseHTTP

// createAnthropicTextResponseHTTP folds the text request into one Messages
// call through createAnthropicMessagesResponse — the existing streaming wire
// seam (SSE accumulation, deadline guard, status-only error surfacing all live
// there). No tools, one user turn, chat-shaped.
func createAnthropicTextResponseHTTP(ctx context.Context, apiKey string, request anthropicTextRequest) (string, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", fmt.Errorf("ANTHROPIC_API_KEY is not configured")
	}
	model := strings.TrimSpace(request.Model)
	if model == "" {
		model = chatModel()
	}
	maxTokens := request.MaxTokens
	if maxTokens <= 0 {
		maxTokens = anthropicChatMaxTokens
	}
	// Doctrine floor (agent_runner_anthropic.go): no Anthropic surface runs
	// below medium — callers still passing the pre-doctrine "low" clamp UP
	// here, and junk/empty falls back to the floor rather than below it.
	// Silent (no per-request warning): the clamped values are hardcoded caller
	// constants, not operator misconfiguration, and chat runs on every turn.
	effort, _ := flooredEffort(request.Effort, doctrineEffortFloor)

	// Attachment blocks (images/PDFs) go ahead of the text block in the one
	// user turn; a request without attachments assembles exactly as before.
	content := make([]json.RawMessage, 0, len(request.Attachments)+1)
	content = append(content, request.Attachments...)
	content = append(content, anthropicTextBlock(strings.TrimSpace(request.Input)))

	response, err := createAnthropicMessagesResponse(ctx, apiKey, anthropicMessagesRequest{
		Model:  model,
		System: strings.TrimSpace(request.Instructions),
		Messages: []anthropicMessage{{
			Role:    "user",
			Content: content,
		}},
		MaxTokens: maxTokens,
		Effort:    effort,
		// Sonnet 5 defaults to ADAPTIVE thinking when the field is omitted,
		// and max_tokens caps thinking + text combined — a hard question could
		// burn most of the 800-token chat budget on thinking and truncate the
		// answer. Disabled matches the no-reasoning gpt-5.5 profile this path
		// replaced (accepted on Sonnet 5; the Fable orchestrator never sets it).
		DisableThinking: true,
	})
	if err != nil {
		return "", err
	}
	// Safety classifiers answer HTTP 200 with stop_reason refusal and empty (or
	// partial) content — surface it as an error, never as a silent empty answer.
	if response.StopReason == "refusal" {
		return "", fmt.Errorf("Anthropic chat request was declined by safety classifiers")
	}
	// A max_tokens stop means the text was cut off mid-thought. Chat callers can
	// retry, but the follow-up artifact-rewrite path persists this text as the
	// COMPLETE artifact body — truncation must be an error, never a silent save.
	if response.StopReason == "max_tokens" {
		return "", fmt.Errorf("Anthropic response was cut off (stop_reason=max_tokens); raise the token budget")
	}

	text := extractAnthropicResponseText(response)
	if text == "" {
		return "", fmt.Errorf("Anthropic response did not include output text")
	}
	return text, nil
}

// extractAnthropicResponseText mirrors extractOpenAIResponseText: join the
// text blocks, skip everything else (thinking blocks stream with empty text
// on Sonnet 5 and are never part of the answer).
func extractAnthropicResponseText(response anthropicMessagesResponse) string {
	var parts []string
	for _, raw := range response.Content {
		block := decodeAnthropicBlock(raw)
		if block.Type != "text" {
			continue
		}
		if text := strings.TrimSpace(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
