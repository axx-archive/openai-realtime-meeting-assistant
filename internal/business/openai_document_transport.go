package business

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	openAIDocumentEndpoint      = "https://api.openai.com/v1/responses"
	OpenAIDocumentModel         = "gpt-5.6-terra"
	OpenAIDocumentPriceRevision = "openai-terra-standard-usd-2026-09-04-v1"
	openAIDocumentBodyLimit     = 256000
)

var (
	ErrOpenAIDocumentRequest    = errors.New("business: invalid frozen document request")
	ErrOpenAIDocumentTransport  = errors.New("business: document transport outcome unknown")
	ErrOpenAIDocumentEnvelope   = errors.New("business: invalid document response envelope")
	ErrOpenAIDocumentAcceptance = errors.New("business: document acceptance was not recorded")
	responseIDPattern           = regexp.MustCompile(`^resp_[A-Za-z0-9_-]{1,195}$`)
	documentRequestIDPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

// FrozenOpenAIDocumentRequest is constructed only by server preparation code,
// after current source/resource authority and token-count admission. It does not
// establish that authority itself. No HTTP browser DTO should expose this type.
// Bytes returns a copy; transport checks the exact retained digest before egress.
type FrozenOpenAIDocumentRequest struct {
	wire   []byte
	digest string
}

func (r FrozenOpenAIDocumentRequest) Bytes() []byte  { return bytes.Clone(r.wire) }
func (r FrozenOpenAIDocumentRequest) Digest() string { return r.digest }

type documentReasoning struct {
	Effort  string `json:"effort"`
	Context string `json:"context"`
}
type documentCache struct {
	Mode string `json:"mode"`
}
type documentText struct {
	Format struct {
		Type string `json:"type"`
	} `json:"format"`
}
type documentRequest struct {
	Model              string            `json:"model"`
	Instructions       string            `json:"instructions"`
	Input              string            `json:"input"`
	Background         bool              `json:"background"`
	Store              bool              `json:"store"`
	Stream             bool              `json:"stream"`
	Tools              []json.RawMessage `json:"tools"`
	ToolChoice         string            `json:"tool_choice"`
	ParallelToolCalls  bool              `json:"parallel_tool_calls"`
	ServiceTier        string            `json:"service_tier"`
	MaxOutputTokens    int64             `json:"max_output_tokens"`
	Reasoning          documentReasoning `json:"reasoning"`
	Text               documentText      `json:"text"`
	Truncation         string            `json:"truncation"`
	PromptCacheOptions documentCache     `json:"prompt_cache_options"`
	Metadata           map[string]string `json:"metadata"`
}

func FreezeOpenAIDocumentRequest(instructions, input, requestID string) (FrozenOpenAIDocumentRequest, error) {
	if strings.TrimSpace(instructions) == "" || strings.TrimSpace(input) == "" || !documentRequestIDPattern.MatchString(requestID) {
		return FrozenOpenAIDocumentRequest{}, ErrOpenAIDocumentRequest
	}
	r := documentRequest{Model: OpenAIDocumentModel, Instructions: instructions, Input: input, Background: true, Store: false, Stream: false, Tools: []json.RawMessage{}, ToolChoice: "none", ServiceTier: "default", MaxOutputTokens: 4096, Reasoning: documentReasoning{"high", "current_turn"}, Truncation: "disabled", PromptCacheOptions: documentCache{"explicit"}, Metadata: map[string]string{"stride_request_id": requestID}}
	r.Text.Format.Type = "text"
	b, e := json.Marshal(r)
	if e != nil || len(b) > openAIDocumentBodyLimit {
		return FrozenOpenAIDocumentRequest{}, ErrOpenAIDocumentRequest
	}
	return FrozenOpenAIDocumentRequest{b, documentWireDigest(b)}, nil
}

// Restore preserves exact bytes, including formatting, while requiring the
// complete frozen shape. It rejects duplicate/unknown fields and missing false
// values rather than silently adopting provider defaults on replay.
func RestoreOpenAIDocumentRequest(wire []byte, expectedDigest string) (FrozenOpenAIDocumentRequest, error) {
	if len(wire) == 0 || len(wire) > openAIDocumentBodyLimit || documentWireDigest(wire) != expectedDigest {
		return FrozenOpenAIDocumentRequest{}, ErrOpenAIDocumentRequest
	}
	var r documentRequest
	d := json.NewDecoder(bytes.NewReader(wire))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil {
		return FrozenOpenAIDocumentRequest{}, ErrOpenAIDocumentRequest
	}
	canonical, e := FreezeOpenAIDocumentRequest(r.Instructions, r.Input, r.Metadata["stride_request_id"])
	if e != nil {
		return FrozenOpenAIDocumentRequest{}, e
	}
	// Compare normalized bytes to our one legal shape; duplicate keys are rejected
	// recursively before normalization so no decoder's last-key-wins is authority.
	var normalized any
	if !documentUniqueJSON(wire) || json.Unmarshal(wire, &normalized) != nil {
		return FrozenOpenAIDocumentRequest{}, ErrOpenAIDocumentRequest
	}
	a, _ := json.Marshal(normalized)
	var want any
	_ = json.Unmarshal(canonical.wire, &want)
	b, _ := json.Marshal(want)
	if !bytes.Equal(a, b) {
		return FrozenOpenAIDocumentRequest{}, ErrOpenAIDocumentRequest
	}
	return FrozenOpenAIDocumentRequest{bytes.Clone(wire), expectedDigest}, nil
}

// RoundTripper is a server/test injection, never a supplied URL or browser value.
// Production uses a fresh HTTP/1 connection per invocation: Go's automatic
// retry of requests on reused connections is deliberately unavailable.
type OpenAIDocumentTransportConfig struct {
	APIKey, ProjectID string
	RoundTripper      http.RoundTripper
	Timeout           time.Duration
}
type OpenAIDocumentTransport struct {
	client       *http.Client
	key, project string
}

func NewOpenAIDocumentTransport(c OpenAIDocumentTransportConfig) (*OpenAIDocumentTransport, error) {
	if strings.TrimSpace(c.APIKey) == "" || strings.ContainsAny(c.APIKey, "\r\n") || len(c.APIKey) > 4096 || strings.ContainsAny(c.ProjectID, "\r\n") || len(c.ProjectID) > 200 {
		return nil, ErrOpenAIDocumentRequest
	}
	if c.Timeout == 0 {
		c.Timeout = 20 * time.Second
	}
	if c.Timeout <= 0 || c.Timeout > 20*time.Second {
		return nil, ErrOpenAIDocumentRequest
	}
	rt := c.RoundTripper
	if rt == nil {
		rt = newDocumentHTTPTransport()
	}
	return &OpenAIDocumentTransport{&http.Client{Transport: rt, Timeout: c.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, c.APIKey, c.ProjectID}, nil
}

// Explicit ALPN and Protocols avoid inheriting HTTP/2 negotiation from the
// process default while disabling its handler. That mismatch otherwise makes
// HTTP/2 frames appear as a malformed HTTP/1 response on current Go runtimes.
func newDocumentHTTPTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.DisableKeepAlives = true
	t.ForceAttemptHTTP2 = false
	t.Protocols = new(http.Protocols)
	t.Protocols.SetHTTP1(true)
	t.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{}
	}
	t.TLSClientConfig.NextProtos = []string{"http/1.1"}
	return t
}

type OpenAIDocumentAcceptance struct {
	ResponseID string `json:"responseId"`
}
type OpenAIDocumentAccepted func(context.Context, OpenAIDocumentAcceptance) error

// Usage pointers distinguish an absent field from a measured zero. Reasoning
// is a subset of output, not another billed category.
type OpenAIDocumentUsage struct {
	InputTokens  *int64 `json:"input_tokens"`
	InputDetails *struct {
		CachedTokens     *int64 `json:"cached_tokens"`
		CacheWriteTokens *int64 `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
	OutputTokens  *int64 `json:"output_tokens"`
	OutputDetails *struct {
		ReasoningTokens *int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
	TotalTokens *int64 `json:"total_tokens"`
}
type OpenAIDocumentObservation struct {
	ResponseID      string `json:"responseId"`
	Status          string `json:"status"`
	Terminal        bool   `json:"terminal"`
	Model           string `json:"model"`
	ServiceTier     string `json:"serviceTier"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	OutputState     string `json:"outputState"`
	// Content can retain bounded factual partial/refusal output. Only Usable is
	// eligible for success; neither terminal status nor nonempty text implies it.
	Content        string               `json:"content,omitempty"`
	Usable         bool                 `json:"usable"`
	Usage          *OpenAIDocumentUsage `json:"usage"`
	ActualMicros   *int64               `json:"actualMicros"`
	PriceRevision  string               `json:"priceRevision"`
	Issues         []string             `json:"issues"`
	EnvelopeDigest string               `json:"envelopeDigest,omitempty"`
	HTTPStatus     int                  `json:"httpStatus"`
}

func (t *OpenAIDocumentTransport) Create(ctx context.Context, r FrozenOpenAIDocumentRequest, accepted OpenAIDocumentAccepted) (OpenAIDocumentObservation, error) {
	checked, e := RestoreOpenAIDocumentRequest(r.wire, r.digest)
	if e != nil {
		return OpenAIDocumentObservation{}, e
	}
	var req documentRequest
	_ = json.Unmarshal(checked.wire, &req)
	return t.invoke(ctx, http.MethodPost, "", checked.wire, req.Metadata["stride_request_id"], accepted)
}

// Retrieve issues exactly one GET for the recorded ID. It never creates a
// response and never infers nonacceptance from 404, expiry or missing usage.
func (t *OpenAIDocumentTransport) Retrieve(ctx context.Context, r FrozenOpenAIDocumentRequest, responseID string, accepted OpenAIDocumentAccepted) (OpenAIDocumentObservation, error) {
	checked, e := RestoreOpenAIDocumentRequest(r.wire, r.digest)
	if e != nil {
		return OpenAIDocumentObservation{}, e
	}
	var request documentRequest
	_ = json.Unmarshal(checked.wire, &request)
	if !responseIDPattern.MatchString(responseID) {
		return OpenAIDocumentObservation{}, ErrOpenAIDocumentRequest
	}
	return t.invoke(ctx, http.MethodGet, responseID, nil, request.Metadata["stride_request_id"], accepted)
}
func (t *OpenAIDocumentTransport) invoke(parent context.Context, method, responseID string, wire []byte, requestID string, accepted OpenAIDocumentAccepted) (out OpenAIDocumentObservation, err error) {
	out.Issues = []string{}
	out.PriceRevision = OpenAIDocumentPriceRevision
	if t == nil || accepted == nil {
		return out, ErrOpenAIDocumentRequest
	}
	ctx, cancel := context.WithTimeout(parent, t.client.Timeout)
	defer cancel()
	endpoint := openAIDocumentEndpoint
	if responseID != "" {
		endpoint += "/" + responseID
	}
	// An opaque reader prevents net/http from installing a replayable GetBody.
	var body io.Reader
	if wire != nil {
		body = struct{ io.Reader }{bytes.NewReader(wire)}
	}
	req, e := http.NewRequestWithContext(ctx, method, endpoint, body)
	if e != nil {
		return out, ErrOpenAIDocumentRequest
	}
	req.Header.Set("Authorization", "Bearer "+t.key)
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	if t.project != "" {
		req.Header.Set("OpenAI-Project", t.project)
	}
	res, e := t.client.Do(req)
	if e != nil {
		return out, ErrOpenAIDocumentTransport
	}
	defer res.Body.Close()
	out.HTTPStatus = res.StatusCode
	limited := &io.LimitedReader{R: res.Body, N: openAIDocumentBodyLimit + 1}
	hash := sha256.New()
	d := json.NewDecoder(io.TeeReader(limited, hash))
	// Decode raw top-level values one at a time. Persist a valid ID before parsing
	// any later usage/output or even a syntactically malformed later field.
	fields := map[string]json.RawMessage{}
	tok, e := d.Token()
	if e != nil || tok != json.Delim('{') {
		return out, ErrOpenAIDocumentEnvelope
	}
	for d.More() {
		key, e := d.Token()
		k, ok := key.(string)
		if e != nil || !ok {
			return out, ErrOpenAIDocumentEnvelope
		}
		if _, exists := fields[k]; exists {
			return out, ErrOpenAIDocumentEnvelope
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return out, ErrOpenAIDocumentEnvelope
		}
		fields[k] = raw
		if limited.N <= 0 {
			out.Issues = append(out.Issues, "body_limit")
			return out, ErrOpenAIDocumentEnvelope
		}
		if k == "id" {
			var id string
			if json.Unmarshal(raw, &id) != nil || !responseIDPattern.MatchString(id) || (responseID != "" && id != responseID) {
				return out, ErrOpenAIDocumentEnvelope
			}
			out.ResponseID = id
			if accepted(ctx, OpenAIDocumentAcceptance{id}) != nil {
				return out, ErrOpenAIDocumentAcceptance
			}
		}
	}
	tok, e = d.Token()
	if e != nil || tok != json.Delim('}') {
		return out, ErrOpenAIDocumentEnvelope
	}
	var extra any
	if d.Decode(&extra) != io.EOF || limited.N <= 0 {
		return out, ErrOpenAIDocumentEnvelope
	}
	out.EnvelopeDigest = hex.EncodeToString(hash.Sum(nil))
	if out.ResponseID == "" || res.StatusCode < 200 || res.StatusCode >= 300 {
		return out, ErrOpenAIDocumentEnvelope
	}
	envelope, _ := json.Marshal(fields)
	if !documentUniqueJSON(envelope) {
		return out, ErrOpenAIDocumentEnvelope
	}
	return parseOpenAIDocumentObservation(out, fields, requestID), nil
}

func parseOpenAIDocumentObservation(out OpenAIDocumentObservation, f map[string]json.RawMessage, requestID string) OpenAIDocumentObservation {
	read := func(key string, target any) bool { return len(f[key]) > 0 && json.Unmarshal(f[key], target) == nil }
	var object string
	read("object", &object)
	read("status", &out.Status)
	read("model", &out.Model)
	read("service_tier", &out.ServiceTier)
	var reasoning documentReasoning
	if read("reasoning", &reasoning) {
		out.ReasoningEffort = reasoning.Effort
	}
	if object != "response" {
		out.Issues = append(out.Issues, "object_mismatch")
	}
	routeOK := out.Model == OpenAIDocumentModel && out.ServiceTier == "default"
	if !routeOK {
		out.Issues = append(out.Issues, "model_or_tier_mismatch")
	}
	if out.ReasoningEffort != "" && out.ReasoningEffort != "high" {
		out.Issues = append(out.Issues, "reasoning_mismatch")
	}
	if requestID != "" {
		var metadata map[string]string
		if !read("metadata", &metadata) || metadata["stride_request_id"] != requestID {
			out.Issues = append(out.Issues, "request_correlation_mismatch")
			routeOK = false
		}
	}
	for _, flag := range []struct {
		k    string
		want bool
	}{{"store", false}, {"background", true}, {"parallel_tool_calls", false}} {
		if raw, ok := f[flag.k]; ok {
			var got bool
			if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &got) != nil || got != flag.want {
				out.Issues = append(out.Issues, flag.k+"_mismatch")
			}
		}
	}
	var tools []json.RawMessage
	if raw, ok := f["tools"]; ok && (json.Unmarshal(raw, &tools) != nil || len(tools) > 0) {
		out.Issues = append(out.Issues, "tools_mismatch")
		routeOK = false
	}
	switch out.Status {
	case "queued", "in_progress":
		out.OutputState = "pending"
		return out
	case "completed", "failed", "cancelled", "incomplete":
		out.Terminal = true
	default:
		out.Issues = append(out.Issues, "status_unknown")
		return out
	}
	usageOK := read("usage", &out.Usage)
	if usageOK {
		strict := json.NewDecoder(bytes.NewReader(f["usage"]))
		strict.DisallowUnknownFields()
		usageOK = strict.Decode(&out.Usage) == nil
	}
	if !usageOK {
		out.Issues = append(out.Issues, "usage_invalid")
	}
	if routeOK && usageOK {
		out.ActualMicros = documentUsageCost(out.Usage)
	}
	if out.ActualMicros == nil {
		out.Issues = append(out.Issues, "cost_unknown")
	}
	if u := out.Usage; u != nil && u.InputDetails != nil {
		if (u.InputTokens != nil && *u.InputTokens > 8192) || (u.OutputTokens != nil && *u.OutputTokens > 4096) {
			out.Issues = append(out.Issues, "usage_limit_mismatch")
		}
		if u.InputDetails.CacheWriteTokens != nil && *u.InputDetails.CacheWriteTokens > 0 {
			out.Issues = append(out.Issues, "unexpected_cache_write")
		}
		if u.InputDetails.CachedTokens != nil && *u.InputDetails.CachedTokens > 0 {
			out.Issues = append(out.Issues, "unexpected_cache_read")
		}
	}
	for _, key := range []string{"error", "incomplete_details"} {
		if raw, exists := f[key]; exists && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && out.Status == "completed" {
			out.Issues = append(out.Issues, key+"_mismatch")
		}
	}
	var items []struct {
		Type    string `json:"type"`
		Status  string `json:"status"`
		Role    string `json:"role"`
		Content []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		} `json:"content"`
	}
	out.OutputState = "empty"
	valid := read("output", &items)
	var parts []string
	for _, item := range items {
		if item.Type == "reasoning" {
			continue
		}
		if item.Type != "message" || item.Role != "assistant" {
			if item.Type != "message" {
				out.ActualMicros = nil
				out.Issues = append(out.Issues, "unsupported_output_cost")
			}
			valid = false
			continue
		}
		if item.Status != "completed" {
			valid = false
		}
		for _, part := range item.Content {
			switch part.Type {
			case "output_text":
				parts = append(parts, part.Text)
			case "refusal":
				out.OutputState = "refused"
				parts = append(parts, part.Refusal)
				valid = false
			default:
				valid = false
			}
		}
	}
	out.Content = strings.Join(parts, "\n\n")
	if strings.TrimSpace(out.Content) == "" || len(out.Content) > openAIDocumentBodyLimit {
		valid = false
		out.Content = ""
	}
	if out.Status != "completed" {
		out.OutputState = "incomplete"
		return out
	}
	if !valid {
		if out.OutputState != "refused" {
			out.OutputState = "invalid"
		}
		return out
	}
	out.OutputState = "usable"
	// Missing usage alone does not hide an otherwise useful private document;
	// every other observed route/envelope mismatch quarantines its success claim.
	out.Usable = true
	for _, issue := range out.Issues {
		if issue != "usage_invalid" && issue != "cost_unknown" {
			out.Usable = false
		}
	}
	if !out.Usable {
		out.OutputState = "quarantined"
	}
	return out
}

// The retained tariff is versioned in source. Rates are USD micros per million
// tokens: 2 input, .20 cached, 2.50 cache write, 12 output dollars. Sum integer
// numerators and round upward ONCE. Reasoning is already included in output.
// Sources (retrieved 2026-09-04): https://developers.openai.com/api/docs/pricing
// and https://developers.openai.com/api/docs/models/gpt-5.6-terra .
func documentUsageCost(u *OpenAIDocumentUsage) *int64 {
	if u == nil || u.InputTokens == nil || u.OutputTokens == nil || u.TotalTokens == nil || u.InputDetails == nil || u.InputDetails.CachedTokens == nil || u.InputDetails.CacheWriteTokens == nil || u.OutputDetails == nil || u.OutputDetails.ReasoningTokens == nil {
		return nil
	}
	in, out, total, cache, write, reason := *u.InputTokens, *u.OutputTokens, *u.TotalTokens, *u.InputDetails.CachedTokens, *u.InputDetails.CacheWriteTokens, *u.OutputDetails.ReasoningTokens
	// Beyond the retained short-context tariff, do not guess the invoice band.
	if in < 0 || in > 272000 || out < 0 || out > 128000 || total < 0 || cache < 0 || write < 0 || reason < 0 || cache > in || write > in-cache || reason > out || total != in+out {
		return nil
	}
	sum := big.NewInt(0)
	for _, term := range [][2]int64{{in - cache - write, 2000000}, {cache, 200000}, {write, 2500000}, {out, 12000000}} {
		sum.Add(sum, new(big.Int).Mul(big.NewInt(term[0]), big.NewInt(term[1])))
	}
	sum.Add(sum, big.NewInt(999999))
	sum.Quo(sum, big.NewInt(1000000))
	if !sum.IsInt64() || sum.Int64() > MaxMoneyMicros {
		return nil
	}
	v := sum.Int64()
	return &v
}
func documentWireDigest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func documentUniqueJSON(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(int) bool
	walk = func(depth int) bool {
		if depth > 64 {
			return false
		}
		t, e := d.Token()
		if e != nil {
			return false
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				s, ok := k.(string)
				if e != nil || !ok || seen[s] {
					return false
				}
				seen[s] = true
				if !walk(depth + 1) {
					return false
				}
			}
			t, e = d.Token()
			return e == nil && t == json.Delim('}')
		case '[':
			for d.More() {
				if !walk(depth + 1) {
					return false
				}
			}
			t, e = d.Token()
			return e == nil && t == json.Delim(']')
		default:
			return false
		}
	}
	if !walk(0) {
		return false
	}
	_, e := d.Token()
	return e == io.EOF
}
