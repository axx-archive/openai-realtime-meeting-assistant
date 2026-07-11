package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// swapAnthropicMessagesResponder installs a fake Messages wire seam (the same
// injectable seam the Fable runner tests use) and restores it after the test.
func swapAnthropicMessagesResponder(t *testing.T, responder anthropicMessagesResponder) {
	t.Helper()
	original := createAnthropicMessagesResponse
	createAnthropicMessagesResponse = responder
	t.Cleanup(func() { createAnthropicMessagesResponse = original })
}

// swapAnthropicTextResponder installs a fake chat text seam (mirrors the
// createOpenAITextResponse swap idiom used across the suite) and restores it
// after the test.
func swapAnthropicTextResponder(t *testing.T, responder anthropicTextResponder) {
	t.Helper()
	original := createAnthropicTextResponse
	createAnthropicTextResponse = responder
	t.Cleanup(func() { createAnthropicTextResponse = original })
}

// swapOpenAITextResponder mirrors swapAnthropicTextResponder for the gpt-5.5
// fallback seam.
func swapOpenAITextResponder(t *testing.T, responder openAITextResponder) {
	t.Helper()
	original := createOpenAITextResponse
	createOpenAITextResponse = responder
	t.Cleanup(func() { createOpenAITextResponse = original })
}

// The text helper is a thin fold over the EXISTING Messages wire seam: system
// carries the instructions, one user turn carries the input as a text block,
// no tools, and the Sonnet budgets (max_tokens + output_config.effort) land on
// the wire request unchanged.
func TestCreateAnthropicTextResponseWrapsMessagesSeam(t *testing.T) {
	t.Setenv("BONFIRE_CHAT_MODEL", "")

	var got anthropicMessagesRequest
	swapAnthropicMessagesResponder(t, func(_ context.Context, apiKey string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		if apiKey != "sk-ant-test" {
			t.Fatalf("apiKey=%q, want sk-ant-test", apiKey)
		}
		got = request
		return anthropicMessagesResponse{
			StopReason: "end_turn",
			Content: []json.RawMessage{
				mockAnthropicTextBlock("Pricing locked at $99/mo."),
				mockAnthropicTextBlock("Ask Tim for the design-partner deck."),
			},
		}, nil
	})

	text, err := createAnthropicTextResponse(context.Background(), "sk-ant-test", anthropicTextRequest{
		Instructions: "Answer from memory only.",
		Input:        "what did we decide on pricing?",
		Effort:       "low",
		MaxTokens:    anthropicChatMaxTokens,
		Seat:         seatMemoryQA,
	})
	if err != nil {
		t.Fatalf("createAnthropicTextResponse: %v", err)
	}

	if got.Model != "claude-sonnet-5" {
		t.Fatalf("model=%q, want the claude-sonnet-5 chat default", got.Model)
	}
	// The caller asked for effort low; the doctrine floor (never below medium)
	// clamps it UP on the wire.
	if got.MaxTokens != 800 || got.Effort != "medium" {
		t.Fatalf("budget=%d/%q, want 800/medium (doctrine floor over the caller's low)", got.MaxTokens, got.Effort)
	}
	// Sonnet 5 runs ADAPTIVE thinking when the field is omitted, and
	// max_tokens caps thinking + text combined — the chat path must disable
	// thinking so the 800-token budget is spent on the answer.
	if !got.DisableThinking {
		t.Fatal("chat request must set DisableThinking (Sonnet 5 defaults to adaptive thinking inside the same budget)")
	}
	if got.System != "Answer from memory only." {
		t.Fatalf("system=%q, want the instructions", got.System)
	}
	// The caller's ledger seat rides through to the wire seam untouched (W0
	// item 3): each text-helper caller owns its tag, and the wire seam files
	// the usage entry under it.
	if got.Seat != seatMemoryQA {
		t.Fatalf("seat=%q, want %q threaded through to the wire request", got.Seat, seatMemoryQA)
	}
	if len(got.Tools) != 0 {
		t.Fatalf("chat request carries %d tools, want none", len(got.Tools))
	}
	if len(got.Messages) != 1 || got.Messages[0].Role != "user" || len(got.Messages[0].Content) != 1 {
		t.Fatalf("messages=%+v, want exactly one user turn with one block", got.Messages)
	}
	block := decodeAnthropicBlock(got.Messages[0].Content[0])
	if block.Type != "text" || block.Text != "what did we decide on pricing?" {
		t.Fatalf("input block=%+v, want the text input", block)
	}
	if text != "Pricing locked at $99/mo.\nAsk Tim for the design-partner deck." {
		t.Fatalf("text=%q, want the joined text blocks", text)
	}
}

// BONFIRE_CHAT_MODEL swaps the chat model within the doctrine (a Haiku id is
// refused in favor of the Sonnet default — never-Haiku routing doctrine); an
// explicit request.Model wins over the env dial; junk effort and a zero
// budget fall back to medium/800 so a mis-set caller can never send an
// invalid Sonnet request or dip below the effort floor.
func TestCreateAnthropicTextResponseModelAndBudgetDials(t *testing.T) {
	t.Setenv("BONFIRE_CHAT_MODEL", "")
	if got := chatModel(); got != "claude-sonnet-5" {
		t.Fatalf("chatModel()=%q, want claude-sonnet-5", got)
	}
	t.Setenv("BONFIRE_CHAT_MODEL", "claude-opus-4-8")
	if got := chatModel(); got != "claude-opus-4-8" {
		t.Fatalf("chatModel() env override=%q, want claude-opus-4-8", got)
	}
	// Never-Haiku guard: a haiku id on the dial is refused, the doctrine
	// default stands.
	t.Setenv("BONFIRE_CHAT_MODEL", "claude-haiku-4-5")
	if got := chatModel(); got != "claude-sonnet-5" {
		t.Fatalf("chatModel() haiku override=%q, want the claude-sonnet-5 doctrine default", got)
	}
	t.Setenv("BONFIRE_CHAT_MODEL", "")

	var got anthropicMessagesRequest
	swapAnthropicMessagesResponder(t, func(_ context.Context, _ string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		got = request
		return anthropicMessagesResponse{
			StopReason: "end_turn",
			Content:    []json.RawMessage{mockAnthropicTextBlock("ok")},
		}, nil
	})

	if _, err := createAnthropicTextResponse(context.Background(), "sk-ant-test", anthropicTextRequest{
		Model:  "claude-opus-4-8",
		Input:  "x",
		Effort: "galactic",
	}); err != nil {
		t.Fatalf("createAnthropicTextResponse: %v", err)
	}
	if got.Model != "claude-opus-4-8" {
		t.Fatalf("model=%q, want the explicit request model over the env dial", got.Model)
	}
	if got.MaxTokens != anthropicChatMaxTokens || got.Effort != "medium" {
		t.Fatalf("fallback budget=%d/%q, want %d/medium (the doctrine floor)", got.MaxTokens, got.Effort, anthropicChatMaxTokens)
	}
	// No seat on the request: the helper passes empty through and the ledger
	// records "untagged" at the seam — an unlabeled call site stays visible.
	if got.Seat != "" {
		t.Fatalf("seat=%q, want empty when the caller sets none (untagged at the seam)", got.Seat)
	}
}

// Keyless returns the configuration error without touching the wire; a
// refusal stop and a text-less response both surface as errors, never as a
// silent empty answer.
func TestCreateAnthropicTextResponseGuards(t *testing.T) {
	swapAnthropicMessagesResponder(t, func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		t.Fatal("wire seam must not be called without a key")
		return anthropicMessagesResponse{}, nil
	})
	if _, err := createAnthropicTextResponse(context.Background(), "  ", anthropicTextRequest{Input: "x"}); err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("keyless err=%v, want ANTHROPIC_API_KEY configuration error", err)
	}

	swapAnthropicMessagesResponder(t, func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		return anthropicMessagesResponse{StopReason: "refusal"}, nil
	})
	if _, err := createAnthropicTextResponse(context.Background(), "sk-ant-test", anthropicTextRequest{Input: "x"}); err == nil || !strings.Contains(err.Error(), "declined") {
		t.Fatalf("refusal err=%v, want declined error", err)
	}

	// Thinking blocks stream with empty text on Sonnet 5 — a response with no
	// text block is an error, not an empty answer.
	thinkingOnly, _ := json.Marshal(map[string]any{"type": "thinking", "thinking": ""})
	swapAnthropicMessagesResponder(t, func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		return anthropicMessagesResponse{StopReason: "end_turn", Content: []json.RawMessage{thinkingOnly}}, nil
	})
	if _, err := createAnthropicTextResponse(context.Background(), "sk-ant-test", anthropicTextRequest{Input: "x"}); err == nil || !strings.Contains(err.Error(), "output text") {
		t.Fatalf("empty-text err=%v, want output-text error", err)
	}

	// A max_tokens stop is a truncated answer — for the follow-up rewrite path
	// that would persist a cut-off artifact body as complete, so it is an
	// error even when partial text arrived.
	swapAnthropicMessagesResponder(t, func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		return anthropicMessagesResponse{
			StopReason: "max_tokens",
			Content:    []json.RawMessage{mockAnthropicTextBlock("partial answer that got cut")},
		}, nil
	})
	if _, err := createAnthropicTextResponse(context.Background(), "sk-ant-test", anthropicTextRequest{Input: "x"}); err == nil || !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("truncation err=%v, want max_tokens error", err)
	}
}
