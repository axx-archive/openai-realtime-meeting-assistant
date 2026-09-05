package business

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const OpenAIDocumentInputTokenLimit int64 = 8192

var ErrOpenAIDocumentTokenCount = errors.New("business: document input count unavailable or outside admitted route")

type CountStageError struct{ Stage string }

func (e *CountStageError) Error() string           { return e.FailureCategory() }
func (e *CountStageError) Unwrap() error           { return ErrOpenAIDocumentTokenCount }
func (e *CountStageError) FailureCategory() string { return "input count " + e.Stage }

// CountHTTPError exposes only an HTTP status and an allowlisted parameter name,
// never a provider message or echoed private request.
type CountHTTPError struct {
	Status    int
	Parameter string
}

func (e *CountHTTPError) Error() string { return e.FailureCategory() }
func (e *CountHTTPError) Unwrap() error { return ErrOpenAIDocumentTokenCount }
func (e *CountHTTPError) FailureCategory() string {
	return fmt.Sprintf("input count HTTP %d parameter %s", e.Status, e.Parameter)
}
func countHTTPError(response *http.Response) error {
	out := &CountHTTPError{Status: response.StatusCode, Parameter: "unspecified"}
	wire, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
	var envelope struct {
		Error struct {
			Param string `json:"param"`
		} `json:"error"`
	}
	if json.Unmarshal(wire, &envelope) == nil {
		switch envelope.Error.Param {
		case "background", "store", "stream", "model", "instructions", "input", "tools", "tool_choice", "parallel_tool_calls", "reasoning", "reasoning.effort", "reasoning.context", "text", "truncation":
			out.Parameter = envelope.Error.Param
		}
	}
	return out
}

// OpenAIDocumentTokenCount is factual preparation evidence, not execution or
// source authority. Retain it privately with the exact request and grant binding.
// Never accept a browser-supplied instance as proof of provider counting.
type OpenAIDocumentTokenCount struct {
	RequestDigest  string    `json:"requestDigest"`
	EnvelopeDigest string    `json:"envelopeDigest"`
	InputTokens    int64     `json:"inputTokens"`
	CountedAt      time.Time `json:"countedAt"`
}

// CountInputTokens projects the frozen request onto the counting endpoint
// schema, preserving all input/instruction/tool/reasoning/text-format fields.
// Generation lifecycle, billing and retention options are not count parameters.
// The caller must authorize this private-source egress before invoking it.
// There is one bounded request, no retry, and no fallback character estimate.
// Contract: https://developers.openai.com/api/docs/guides/token-counting
func (t *OpenAIDocumentTransport) CountInputTokens(parent context.Context, r FrozenOpenAIDocumentRequest) (OpenAIDocumentTokenCount, error) {
	var out OpenAIDocumentTokenCount
	if t == nil {
		return out, ErrOpenAIDocumentTokenCount
	}
	checked, err := RestoreOpenAIDocumentRequest(r.wire, r.digest)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(parent, t.client.Timeout)
	defer cancel()
	// The guide says "same payload", but the actual count schema rejects
	// background/store/stream/metadata and other generation-only fields. Keep
	// the complete request digest in the receipt; derive this one fixed projection.
	var generation documentRequest
	if json.Unmarshal(checked.wire, &generation) != nil {
		return out, ErrOpenAIDocumentTokenCount
	}
	countWire, err := json.Marshal(struct {
		Model             string            `json:"model"`
		Instructions      string            `json:"instructions"`
		Input             string            `json:"input"`
		Tools             []json.RawMessage `json:"tools"`
		ToolChoice        string            `json:"tool_choice"`
		ParallelToolCalls bool              `json:"parallel_tool_calls"`
		Reasoning         documentReasoning `json:"reasoning"`
		Text              documentText      `json:"text"`
		Truncation        string            `json:"truncation"`
	}{generation.Model, generation.Instructions, generation.Input, generation.Tools, generation.ToolChoice, generation.ParallelToolCalls, generation.Reasoning, generation.Text, generation.Truncation})
	if err != nil {
		return out, ErrOpenAIDocumentTokenCount
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIDocumentEndpoint+"/input_tokens", bytes.NewReader(countWire))
	if err != nil {
		return out, ErrOpenAIDocumentTokenCount
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.key)
	if t.project != "" {
		req.Header.Set("OpenAI-Project", t.project)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return out, &CountStageError{Stage: "transport failed"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, countHTTPError(resp)
	}
	wire, err := io.ReadAll(io.LimitReader(resp.Body, 8193))
	if err != nil {
		return out, &CountStageError{Stage: "body read failed"}
	}
	if len(wire) > 8192 {
		return out, &CountStageError{Stage: "oversized envelope"}
	}
	if !documentUniqueJSON(wire) {
		return out, &CountStageError{Stage: "invalid JSON envelope"}
	}
	var envelope struct {
		Object      string `json:"object"`
		InputTokens *int64 `json:"input_tokens"`
	}
	if json.Unmarshal(wire, &envelope) != nil {
		return out, &CountStageError{Stage: "invalid counter schema"}
	}
	if envelope.Object != "response.input_tokens" || envelope.InputTokens == nil {
		return out, &CountStageError{Stage: "missing counter"}
	}
	if *envelope.InputTokens <= 0 || *envelope.InputTokens > OpenAIDocumentInputTokenLimit {
		return out, &CountStageError{Stage: "counter outside route limit"}
	}
	return OpenAIDocumentTokenCount{RequestDigest: checked.digest, EnvelopeDigest: documentWireDigest(wire), InputTokens: *envelope.InputTokens, CountedAt: time.Now().UTC()}, nil
}
