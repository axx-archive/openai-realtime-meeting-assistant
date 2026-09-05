package business

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

const OpenAIDocumentInputTokenLimit int64 = 8192

var ErrOpenAIDocumentTokenCount = errors.New("business: document input count unavailable or outside admitted route")

// OpenAIDocumentTokenCount is factual preparation evidence, not execution or
// source authority. Retain it privately with the exact request and grant binding.
// Never accept a browser-supplied instance as proof of provider counting.
type OpenAIDocumentTokenCount struct {
	RequestDigest  string    `json:"requestDigest"`
	EnvelopeDigest string    `json:"envelopeDigest"`
	InputTokens    int64     `json:"inputTokens"`
	CountedAt      time.Time `json:"countedAt"`
}

// CountInputTokens sends the same frozen payload to the official counting
// endpoint, including instructions and formatting, before generation admission.
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIDocumentEndpoint+"/input_tokens", bytes.NewReader(checked.wire))
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
		return out, ErrOpenAIDocumentTokenCount
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, ErrOpenAIDocumentTokenCount
	}
	wire, err := io.ReadAll(io.LimitReader(resp.Body, 8193))
	if err != nil || len(wire) > 8192 || !documentUniqueJSON(wire) {
		return out, ErrOpenAIDocumentTokenCount
	}
	var envelope struct {
		Object      string `json:"object"`
		InputTokens *int64 `json:"input_tokens"`
	}
	if json.Unmarshal(wire, &envelope) != nil || envelope.Object != "response.input_tokens" || envelope.InputTokens == nil || *envelope.InputTokens <= 0 || *envelope.InputTokens > OpenAIDocumentInputTokenLimit {
		return out, ErrOpenAIDocumentTokenCount
	}
	return OpenAIDocumentTokenCount{RequestDigest: checked.digest, EnvelopeDigest: documentWireDigest(wire), InputTokens: *envelope.InputTokens, CountedAt: time.Now().UTC()}, nil
}
