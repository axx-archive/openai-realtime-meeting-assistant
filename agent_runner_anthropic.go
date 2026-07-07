package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	anthropicMessagesURL       = "https://api.anthropic.com/v1/messages"
	anthropicAPIVersion        = "2023-06-01"
	defaultOrchestratorModel   = "claude-fable-5"
	defaultFallbackModel       = "claude-opus-4-8"
	controlToolReportGoalState = "report_goal_state"
)

var errAgentWorkerNotConfigured = errors.New("agent worker is not configured")

// Image-bearing request budget (Wave 5 item 21, defensive wire-layer caps).
// The vision slide juries attach the render-runner's page JPEGs as base64
// image blocks on the raw-content seam; these bounds keep a runaway caller
// (a 60-page deck, a corrupt raster) from assembling a request the API would
// reject anyway. The byte cap measures the DECODED image payload (what the
// base64 carries), so "~20MB of images" means what it says on the wire.
const (
	anthropicMaxRequestImages     = 12
	anthropicMaxRequestImageBytes = 20 << 20
)

// currentAnthropicAPIKey mirrors currentOpenAIAPIKey: a single accessor so
// tests can inject via the environment and keyless-local degrades gracefully.
// The key is never logged or persisted.
func currentAnthropicAPIKey() string {
	return strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
}

func orchestratorModel() string {
	return getenvDefault("BONFIRE_ORCHESTRATOR_MODEL", defaultOrchestratorModel)
}

// orchestratorFallbackModel is the refusal-fallback target. Fable 5's safety
// classifiers can decline a benign request (HTTP 200, stop_reason "refusal") —
// a false positive on a rights/chain-of-title prompt must not kill a goal run
// mid-pipeline, so the loop retries the same request once on this model before
// taking the needs_attention branch (packaging-os-analysis §1).
func orchestratorFallbackModel() string {
	return getenvDefault("BONFIRE_FALLBACK_MODEL", defaultFallbackModel)
}

func orchestratorMaxTurns() int {
	return positiveIntEnv("BONFIRE_ORCHESTRATOR_MAX_TURNS", 24)
}

func orchestratorMaxTokens() int {
	return positiveIntEnv("BONFIRE_ORCHESTRATOR_MAX_TOKENS", 4096)
}

// orchestratorEffort controls thinking depth / token spend on Fable 5. Default
// low keeps the control loop snappy; Fable 5 at low still clears prior-model
// ceilings. Any of low|medium|high|xhigh|max is accepted.
func orchestratorEffort() string {
	switch effort := strings.ToLower(strings.TrimSpace(os.Getenv("BONFIRE_ORCHESTRATOR_EFFORT"))); effort {
	case "low", "medium", "high", "xhigh", "max":
		return effort
	default:
		return "low"
	}
}

// orchestratorTimeout bounds one orchestrator run. Default 15m: deliverable
// subtasks now run at effort high with a 32K output ceiling, and Fable 5 single
// turns on hard tasks can legitimately run many minutes — a 5m cap converted
// slow-but-good runs into needs_attention failures. Ships in the same change as
// the effort/token bump on purpose (packaging-os-analysis §1, Wave 1 item 2).
func orchestratorTimeout() time.Duration {
	return durationEnv("BONFIRE_ORCHESTRATOR_TIMEOUT", 15*time.Minute, 30*time.Second)
}

// deliverableMaxTokens is the larger output ceiling for the terminal,
// contract-bearing deliverable subtask of a /goal. A full deliverable (every
// contract section + honest receipts) needs materially more headroom than a
// planning turn, whose default stays orchestratorMaxTokens(). Env-overridable;
// only the deliverable subtask reads it, so planning/non-goal turns are
// unchanged. A too-small ceiling is exactly what truncated the One-Pager and
// burned the wasted revision passes. 32K non-streamed would sit past Anthropic's
// HTTP ceiling, so the wire layer streams (SSE) and accumulates — see
// createAnthropicMessagesResponseHTTP.
func deliverableMaxTokens() int {
	return positiveIntEnv("BONFIRE_DELIVERABLE_MAX_TOKENS", 32768)
}

// deliverableEffort is the thinking depth for the deliverable subtask. Default
// high — the deliverable IS the product; paying Fable-5 rates for medium depth
// was the worst of both worlds (packaging-os-analysis §1). Any of
// low|medium|high|xhigh|max is accepted; anything else falls back to high.
func deliverableEffort() string {
	switch effort := strings.ToLower(strings.TrimSpace(os.Getenv("BONFIRE_DELIVERABLE_EFFORT"))); effort {
	case "low", "medium", "high", "xhigh", "max":
		return effort
	default:
		return "high"
	}
}

// --- Anthropic Messages API wire types ---------------------------------------

// anthropicCacheControl marks a prompt-cache breakpoint. Only the ephemeral
// type exists on the wire today.
type anthropicCacheControl struct {
	Type string `json:"type"`
}

var ephemeralCacheControl = &anthropicCacheControl{Type: "ephemeral"}

type anthropicTool struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	InputSchema  map[string]any         `json:"input_schema"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

// anthropicMessage.Content is raw so assistant turns (including Fable 5 thinking
// blocks, which must be echoed back byte-for-byte on the same model) round-trip
// unchanged. Blocks we author (text, tool_result) are marshaled into raw.
type anthropicMessage struct {
	Role    string            `json:"role"`
	Content []json.RawMessage `json:"content"`
}

type anthropicMessagesRequest struct {
	Model     string
	System    string
	Messages  []anthropicMessage
	Tools     []anthropicTool
	MaxTokens int
	Effort    string
	// DisableThinking sends thinking:{type:"disabled"}. Sonnet 5 runs ADAPTIVE
	// thinking when the field is omitted (a silent default change from the
	// 4.6-era models), and max_tokens caps thinking + text COMBINED — the
	// chat/follow-up text path sets this so a tight chat budget is spent on
	// the answer, matching the no-reasoning gpt-5.5 profile it replaced. Must
	// stay false for the Fable 5 orchestrator: an explicit disabled is a 400
	// there (thinking is always on; the field must be omitted).
	DisableThinking bool
}

type anthropicMessagesResponse struct {
	Model      string            `json:"model"`
	StopReason string            `json:"stop_reason"`
	Content    []json.RawMessage `json:"content"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// anthropicBlock is the decoded view of one response content block, used only to
// inspect type / text / tool_use fields — never to re-serialize the block.
type anthropicBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// anthropicMessagesResponder mirrors openAITextResponder: the injectable HTTP
// seam so the tool loop is testable against a mock endpoint with no network.
type anthropicMessagesResponder func(ctx context.Context, apiKey string, request anthropicMessagesRequest) (anthropicMessagesResponse, error)

var createAnthropicMessagesResponse anthropicMessagesResponder = createAnthropicMessagesResponseHTTP

// createAnthropicMessagesResponseHTTP always streams (stream:true) and
// accumulates the SSE events back into the same anthropicMessagesResponse the
// old non-stream path returned, so nothing above this seam changes. Always —
// not only for large budgets — because one wire path is one set of bugs, and a
// 32K deliverable ceiling non-streamed would hit Anthropic's HTTP timeout
// ceiling anyway. The wall clock is bounded by ctx (the dispatcher stamps
// Capabilities().MaxRuntime = orchestratorTimeout()); a fixed http.Client
// timeout would cut off long legitimate streams, so a deadline is guarded in
// here instead for callers that arrive without one.
func createAnthropicMessagesResponseHTTP(ctx context.Context, apiKey string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return anthropicMessagesResponse{}, fmt.Errorf("ANTHROPIC_API_KEY is not configured")
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, orchestratorTimeout())
		defer cancel()
	}
	rawPayload, err := buildAnthropicMessagesPayload(request)
	if err != nil {
		return anthropicMessagesResponse{}, err
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicMessagesURL, bytes.NewReader(rawPayload))
	if err != nil {
		return anthropicMessagesResponse{}, fmt.Errorf("create Anthropic messages request: %w", err)
	}
	httpRequest.Header.Set("x-api-key", apiKey)
	httpRequest.Header.Set("anthropic-version", anthropicAPIVersion)
	httpRequest.Header.Set("content-type", "application/json")
	httpRequest.Header.Set("accept", "text/event-stream")

	response, err := (&http.Client{}).Do(httpRequest)
	if err != nil {
		return anthropicMessagesResponse{}, fmt.Errorf("call Anthropic messages: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		// Errors arrive as a plain JSON body even on stream:true requests.
		// Reuse the OpenAI failure helper: log the upstream body server-side,
		// return a status-only error so a 401/429 body never reaches the browser.
		rawBody, _ := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
		return anthropicMessagesResponse{}, apiRequestFailedError("Anthropic messages failed", response.Status, rawBody)
	}

	body, err := decodeAnthropicSSEStream(io.LimitReader(response.Body, 64*1024*1024))
	if err != nil {
		return anthropicMessagesResponse{}, err
	}
	return body, nil
}

// buildAnthropicMessagesPayload renders the wire body, placing cache_control
// breakpoints at the stable prefixes so the 24-turn loop stops paying full
// price to resend an unchanged prefix every turn (packaging-os-analysis §1).
// Prompt caching is a prefix match over tools → system → messages, and the API
// allows at most 4 breakpoints per request, so they are spent as: 1 on the
// last tool (tools are stable across every job of one app), 1 on the system
// prompt (stable across every turn of one run), and up to 2 on the newest user
// turns (the incremental multi-turn pattern: the newest breakpoint walks back
// at most 20 blocks to find the previous entry, so marking the prior user turn
// too keeps tool-heavy turns from silently missing). Only blocks this code
// authors (user turns) are ever marked — assistant turns carry Fable thinking
// blocks that must round-trip byte-for-byte.
func buildAnthropicMessagesPayload(request anthropicMessagesRequest) ([]byte, error) {
	if err := validateAnthropicImageBudget(request.Messages); err != nil {
		return nil, err
	}
	// Fable 5: thinking is always on (omit the field), and sampling params are
	// rejected — send neither. Depth is controlled by output_config.effort.
	payload := map[string]any{
		"model":      request.Model,
		"max_tokens": request.MaxTokens,
		"messages":   cacheMarkedMessages(request.Messages),
		"stream":     true,
	}
	if strings.TrimSpace(request.System) != "" {
		payload["system"] = []map[string]any{{
			"type":          "text",
			"text":          request.System,
			"cache_control": ephemeralCacheControl,
		}}
	}
	if len(request.Tools) > 0 {
		tools := make([]anthropicTool, len(request.Tools))
		copy(tools, request.Tools)
		last := tools[len(tools)-1]
		last.CacheControl = ephemeralCacheControl
		tools[len(tools)-1] = last
		payload["tools"] = tools
	}
	if strings.TrimSpace(request.Effort) != "" {
		payload["output_config"] = map[string]any{"effort": request.Effort}
	}
	if request.DisableThinking {
		payload["thinking"] = map[string]any{"type": "disabled"}
	}

	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode Anthropic messages request: %w", err)
	}
	return rawPayload, nil
}

// cacheMarkedMessages returns a copy of messages with a cache_control
// breakpoint on the last MARKABLE content block of the newest two user turns.
// The caller's slice and its raw blocks are never mutated — the loop reuses
// them as the growing conversation history, and persisted markers would
// accumulate past the 4-breakpoint cap on later turns. Image blocks are never
// marked (the defensive Wave-5 rule: a breakpoint belongs on the text that
// follows the pages, never on a page raster itself); a turn whose every block
// is an image is skipped without spending the breakpoint budget, so an earlier
// user turn keeps its marker instead.
func cacheMarkedMessages(messages []anthropicMessage) []anthropicMessage {
	marked := make([]anthropicMessage, len(messages))
	copy(marked, messages)
	remaining := 2
	for i := len(marked) - 1; i >= 0 && remaining > 0; i-- {
		if marked[i].Role != "user" || len(marked[i].Content) == 0 {
			continue
		}
		markIndex := -1
		for j := len(marked[i].Content) - 1; j >= 0; j-- {
			if decodeAnthropicBlock(marked[i].Content[j]).Type == "image" {
				continue
			}
			markIndex = j
			break
		}
		if markIndex < 0 {
			continue
		}
		content := make([]json.RawMessage, len(marked[i].Content))
		copy(content, marked[i].Content)
		content[markIndex] = withAnthropicCacheControl(content[markIndex])
		marked[i].Content = content
		remaining--
	}
	return marked
}

// anthropicImageBlockView is the minimal decoded view an image block needs for
// budget accounting — never used to re-serialize the block.
type anthropicImageBlockView struct {
	Type   string `json:"type"`
	Source struct {
		Data string `json:"data"`
	} `json:"source"`
}

// validateAnthropicImageBudget enforces the defensive request-level image caps
// on the raw-content seam: at most anthropicMaxRequestImages image blocks and
// ~anthropicMaxRequestImageBytes of decoded image payload per request. Callers
// (the slide jury) budget below these caps themselves; tripping one here is a
// bug surfaced honestly, never a request the API rejects opaquely.
func validateAnthropicImageBudget(messages []anthropicMessage) error {
	images := 0
	totalBytes := 0
	for _, message := range messages {
		for _, raw := range message.Content {
			var view anthropicImageBlockView
			if err := json.Unmarshal(raw, &view); err != nil || view.Type != "image" {
				continue
			}
			images++
			totalBytes += base64.StdEncoding.DecodedLen(len(view.Source.Data))
		}
	}
	if images > anthropicMaxRequestImages {
		return fmt.Errorf("request carries %d image blocks, above the %d-image cap", images, anthropicMaxRequestImages)
	}
	if totalBytes > anthropicMaxRequestImageBytes {
		return fmt.Errorf("request carries ~%dMB of image payload, above the %dMB cap", totalBytes>>20, anthropicMaxRequestImageBytes>>20)
	}
	return nil
}

// withAnthropicCacheControl stamps a cache_control field onto one raw content
// block, returning the original bytes untouched if the block cannot be
// decoded (a malformed block should fail API-side, not vanish here).
func withAnthropicCacheControl(raw json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return raw
	}
	control, err := json.Marshal(ephemeralCacheControl)
	if err != nil {
		return raw
	}
	fields["cache_control"] = control
	stamped, err := json.Marshal(fields)
	if err != nil {
		return raw
	}
	return stamped
}

// --- SSE stream accumulation ---------------------------------------------------

// anthropicSSEEvent is the decoded envelope of one stream event. Only the
// fields the accumulator needs are typed; content_block stays raw so unknown
// block fields survive the round trip untouched.
type anthropicSSEEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ContentBlock json.RawMessage `json:"content_block"`
	Delta        struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// anthropicStreamBlock accumulates one content block from content_block_start
// through its deltas. fields keeps every start-block field raw (id, name, and
// anything the API adds later) so finalize only rewrites what the deltas built.
type anthropicStreamBlock struct {
	blockType string
	fields    map[string]json.RawMessage
	text      strings.Builder
	thinking  strings.Builder
	signature strings.Builder
	inputJSON strings.Builder
}

func newAnthropicStreamBlock(raw json.RawMessage) *anthropicStreamBlock {
	block := &anthropicStreamBlock{fields: map[string]json.RawMessage{}}
	_ = json.Unmarshal(raw, &block.fields)
	block.blockType = decodeAnthropicBlock(raw).Type
	// Seed the builders with the start block's own values (usually empty
	// strings) so start + deltas concatenate in order.
	var seed string
	if rawText, ok := block.fields["text"]; ok && json.Unmarshal(rawText, &seed) == nil {
		block.text.WriteString(seed)
	}
	seed = ""
	if rawThinking, ok := block.fields["thinking"]; ok && json.Unmarshal(rawThinking, &seed) == nil {
		block.thinking.WriteString(seed)
	}
	seed = ""
	if rawSignature, ok := block.fields["signature"]; ok && json.Unmarshal(rawSignature, &seed) == nil {
		block.signature.WriteString(seed)
	}
	return block
}

func (block *anthropicStreamBlock) applyDelta(event anthropicSSEEvent) {
	switch event.Delta.Type {
	case "text_delta":
		block.text.WriteString(event.Delta.Text)
	case "input_json_delta":
		block.inputJSON.WriteString(event.Delta.PartialJSON)
	case "thinking_delta":
		block.thinking.WriteString(event.Delta.Thinking)
	case "signature_delta":
		block.signature.WriteString(event.Delta.Signature)
	}
}

func (block *anthropicStreamBlock) finalize() (json.RawMessage, error) {
	switch block.blockType {
	case "text":
		block.fields["text"], _ = json.Marshal(block.text.String())
	case "thinking":
		block.fields["thinking"], _ = json.Marshal(block.thinking.String())
		if block.signature.Len() > 0 {
			block.fields["signature"], _ = json.Marshal(block.signature.String())
		}
	case "tool_use":
		// An argument-less tool call streams no input_json_delta; keep the
		// start block's empty-object input in that case.
		if input := strings.TrimSpace(block.inputJSON.String()); input != "" {
			if !json.Valid([]byte(input)) {
				return nil, fmt.Errorf("Anthropic stream tool_use input is not valid JSON")
			}
			block.fields["input"] = json.RawMessage(input)
		}
	}
	raw, err := json.Marshal(block.fields)
	if err != nil {
		return nil, fmt.Errorf("encode Anthropic stream content block: %w", err)
	}
	return raw, nil
}

// decodeAnthropicSSEStream folds a Messages API SSE stream into the exact
// response struct the non-stream path used to return: content_block_delta text
// and input_json_delta accumulate per block, message_delta carries stop_reason
// and output usage, message_start carries model and input usage. A stream that
// dies before message_stop leaves StopReason empty, which the runner's default
// branch already treats as an incomplete (never verified) turn.
func decodeAnthropicSSEStream(reader io.Reader) (anthropicMessagesResponse, error) {
	var response anthropicMessagesResponse
	blocks := map[int]*anthropicStreamBlock{}

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			// event:/id:/comment lines and blank separators carry nothing the
			// accumulator needs — the payload's own type field is dispatched on.
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		var event anthropicSSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return anthropicMessagesResponse{}, fmt.Errorf("decode Anthropic stream event: %w", err)
		}
		switch event.Type {
		case "message_start":
			if event.Message != nil {
				response.Model = event.Message.Model
				response.Usage.InputTokens = event.Message.Usage.InputTokens
				response.Usage.OutputTokens = event.Message.Usage.OutputTokens
			}
		case "content_block_start":
			blocks[event.Index] = newAnthropicStreamBlock(event.ContentBlock)
		case "content_block_delta":
			if block := blocks[event.Index]; block != nil {
				block.applyDelta(event)
			}
		case "content_block_stop":
			block := blocks[event.Index]
			if block == nil {
				continue
			}
			raw, err := block.finalize()
			if err != nil {
				return anthropicMessagesResponse{}, err
			}
			// Blocks stream strictly in order (block N stops before N+1
			// starts), so appending on stop preserves content order.
			response.Content = append(response.Content, raw)
			delete(blocks, event.Index)
		case "message_delta":
			if event.Delta.StopReason != "" {
				response.StopReason = event.Delta.StopReason
			}
			if event.Usage != nil && event.Usage.OutputTokens > 0 {
				response.Usage.OutputTokens = event.Usage.OutputTokens
			}
		case "message_stop":
			return response, nil
		case "error":
			if event.Error != nil && strings.TrimSpace(event.Error.Message) != "" {
				return anthropicMessagesResponse{}, fmt.Errorf("Anthropic messages error: %s", strings.TrimSpace(event.Error.Message))
			}
			return anthropicMessagesResponse{}, fmt.Errorf("Anthropic messages error: stream error event")
		}
	}
	if err := scanner.Err(); err != nil {
		return anthropicMessagesResponse{}, fmt.Errorf("read Anthropic messages stream: %w", err)
	}
	return response, nil
}

// --- The runner --------------------------------------------------------------

type anthropicFableRunner struct {
	app           *kanbanBoardApp
	apiKey        func() string
	model         string
	fallbackModel string
	// fallbackUsed records the fallback model once a refusal retry actually
	// served a turn, so provenance (evidence metadata + artifact footer) names
	// the model that produced the artifact. Job-local: the runner is built per
	// job (selectAgentRunner).
	fallbackUsed string
	effort       string
	maxTurns     int
	maxTokens    int
	responder    anthropicMessagesResponder
}

func newAnthropicFableRunner(app *kanbanBoardApp) *anthropicFableRunner {
	return &anthropicFableRunner{
		app:           app,
		apiKey:        currentAnthropicAPIKey,
		model:         orchestratorModel(),
		fallbackModel: orchestratorFallbackModel(),
		effort:        orchestratorEffort(),
		maxTurns:      orchestratorMaxTurns(),
		maxTokens:     orchestratorMaxTokens(),
		responder:     createAnthropicMessagesResponse,
	}
}

func (r *anthropicFableRunner) Name() string { return agentRunnerAnthropicFable }

func (r *anthropicFableRunner) Capabilities() AgentCapabilities {
	return AgentCapabilities{ToolLoop: true, MaxRuntime: orchestratorTimeout()}
}

func (r *anthropicFableRunner) RunJob(ctx context.Context, job AgentJob) (<-chan AgentProgress, error) {
	out := make(chan AgentProgress, 8)
	go func() {
		defer close(out)

		apiKey := strings.TrimSpace(r.apiKey())
		if apiKey == "" {
			out <- terminalErrorProgress(r.evidence(0), fmt.Errorf("ANTHROPIC_API_KEY is not configured"))
			return
		}

		// Per-job budget: a /goal deliverable subtask carries a heavier effort +
		// token ceiling (stamped from metadata in newAgentJob) so its
		// contract-bearing artifact does not truncate under the planning default.
		// The runner is built per job (selectAgentRunner), so mutating these
		// fields is job-local; absent overrides keep the env defaults, leaving
		// planning and non-goal threads byte-unchanged and the evidence footer
		// honest about the effort actually used.
		if job.MaxTokens > 0 {
			r.maxTokens = job.MaxTokens
		}
		if strings.TrimSpace(job.Effort) != "" {
			r.effort = job.Effort
		}

		system := r.systemPrompt(job)
		tools := r.tools()
		messages := []anthropicMessage{{
			Role:    "user",
			Content: []json.RawMessage{anthropicTextBlock(r.userPrompt(job))},
		}}

		finalText := ""
		// control is sticky across turns: the model signals completion by ending
		// its turn (end_turn), which carries no tool_use, so the last
		// report_goal_state — e.g. an approval_required gate — must survive into
		// the terminal progress rather than being reset to the verified default.
		control := AgentProgress{}
		for turn := 1; turn <= r.maxTurns; turn++ {
			request := anthropicMessagesRequest{
				Model:     r.model,
				System:    system,
				Messages:  messages,
				Tools:     tools,
				MaxTokens: r.maxTokens,
				Effort:    r.effort,
			}
			response, err := r.responder(ctx, apiKey, request)
			if err != nil {
				out <- terminalErrorProgress(r.evidence(turn), err)
				return
			}
			if response.StopReason == "refusal" {
				// Refusal fallback: Fable's classifiers false-positive on benign
				// rights/security-adjacent prompts, so the SAME request is retried
				// once on the fallback model before anything terminal. The
				// documented hand-rolled fallback pattern is to replay the
				// conversation AS-IS: Fable 5 thinking blocks replayed to a
				// different model are dropped from the prompt server-side
				// (silently, unbilled), while stripping them client-side risks
				// ordering/signature 400s — so the messages go over unchanged,
				// even on multi-turn runs whose history carries assistant
				// thinking blocks (pinned by
				// TestAnthropicFableRunnerRefusalFallbackMidRunReplaysThinkingBlocks).
				fallbackRequest := request
				fallbackRequest.Model = r.fallbackModel
				fallbackResponse, fallbackErr := r.responder(ctx, apiKey, fallbackRequest)
				if fallbackErr != nil {
					out <- terminalErrorProgress(r.evidence(turn), fallbackErr)
					return
				}
				if fallbackResponse.StopReason == "refusal" {
					// Both models declined: only now does the run take the
					// needs_attention branch.
					metadata := r.evidence(turn)
					metadata["orchestratorStop"] = "refusal"
					metadata["orchestratorFallbackModel"] = r.fallbackModel
					out <- AgentProgress{
						Terminal:        true,
						Stage:           "gate_before_shipping",
						ProgressPercent: 72,
						GoalStatus:      "needs_attention",
						ReviewGate:      "blocked",
						Note:            "orchestrator request was declined",
						Err:             fmt.Errorf("orchestrator request was declined by safety classifiers (fallback %s also declined)", r.fallbackModel),
						Metadata:        metadata,
					}
					return
				}
				r.fallbackUsed = r.fallbackModel
				response = fallbackResponse
			}

			var turnText strings.Builder
			var toolResults []json.RawMessage
			for _, rawBlock := range response.Content {
				block := decodeAnthropicBlock(rawBlock)
				switch block.Type {
				case "text":
					if strings.TrimSpace(block.Text) != "" {
						turnText.WriteString(block.Text)
						turnText.WriteByte('\n')
					}
				case "tool_use":
					if block.Name == controlToolReportGoalState {
						control = mergeGoalStateProgress(control, block.Input)
						toolResults = append(toolResults, anthropicToolResultBlock(block.ID, "goal state recorded", false))
						continue
					}
					// The orchestrator's tools ARE the in-process Go functions the
					// Realtime bridge calls — no new transport.
					args := decodeToolArgs(block.Input)
					if block.Name == "create_ticket" {
						// D4 (mirrors board_worker.go): autonomous worker-created
						// cards land as pending Scout drafts a human accepts or
						// dismisses — never as instant board cards.
						args["draft"] = true
					}
					result, _, toolErr := r.app.applyToolCallArgs(block.Name, args)
					content, isError := anthropicToolResultContent(result, toolErr)
					toolResults = append(toolResults, anthropicToolResultBlock(block.ID, content, isError))
				}
			}
			if summary := strings.TrimSpace(turnText.String()); summary != "" {
				finalText = summary
			}

			switch response.StopReason {
			case "tool_use":
				out <- r.turnProgress(finalText, control, turn, response)
				// Echo the assistant turn verbatim (preserving thinking blocks),
				// then answer every tool_use with a tool_result in one user turn.
				messages = append(messages, anthropicMessage{Role: "assistant", Content: response.Content})
				messages = append(messages, anthropicMessage{Role: "user", Content: toolResults})
			case "end_turn":
				out <- r.terminalProgress(job, finalText, control, turn, response)
				return
			default:
				// max_tokens / stop_sequence / pause_turn / unknown: the turn was
				// cut off, not completed. It must NOT earn the verified/passed
				// defaults — that would violate the orchestrator's own
				// gate-before-shipping rule and ship a truncated artifact silently.
				out <- r.incompleteProgress(job, finalText, control, turn, response)
				return
			}
		}

		out <- r.maxTurnsProgress(job, finalText)
	}()
	return out, nil
}

func (r *anthropicFableRunner) systemPrompt(job AgentJob) string {
	authority := normalizeCodexJobAuthority(job.Authority)
	// Wave-10 generation hop: for a tool-templated deliverable job, the tool's
	// full A++ prompt (its exact output contract + gate rubric) replaces the
	// generic per-mode contract, and the final-artifact instruction defers to the
	// tool's headings instead of the generic workflow ones.
	modeContract := agentThreadModeContract(job.Mode)
	finalLine := "When the goal is met, write the finished artifact as your final message using stable Markdown headings (Vision, Goal, Context used, Work decomposition, Execution log, Review, Gate, What worked, Report, Next moves, Verification). Write in a practical operator voice."
	if toolPrompt, ok := r.app.toolPromptForThread(job.thread); ok {
		modeContract = toolPrompt
		finalLine = "When the goal is met, write the finished artifact as your final message using the tool's OUTPUT CONTRACT above — its exact headings, nothing generic. Write in a practical operator voice."
	}
	// Raw-document contract: the final message IS the file — the generic
	// Markdown-headings final line is the exact instruction that looped the
	// live ship_deck child into its law-sweep block.
	if raw, ok := rawDocumentContractInstructions(job.thread.Artifact.Metadata["outputContract"]); ok {
		modeContract = raw
		finalLine = "When the goal is met, your final message is the deliverable FILE ITSELF — nothing before the <!doctype html>, nothing after the closing </html>."
	}
	return strings.Join([]string{
		"You are Scout, the in-process orchestrator for Bonfire OS.",
		"You run a real tool loop: decompose the goal, act with the Bonfire tools available to you, review against the goal, gate before anything ships, and report what matters. Do not narrate a loop you did not run.",
		"Follow the ten-step goal loop in order: identify the goal, decompose the work, assign the right agent, coordinate dependencies, execute in order, review against the original goal, gate before shipping, save what worked, report only what matters, verify the goal or name the blocker.",
		"Call report_goal_state whenever the goal status, review gate, stage, or progress changes so the operator UI stays in step.",
		"Authority: this job is " + authority + ". read_only may inspect and report; workspace_write may change the board, memory, packages, and notifications; external_write (commit, push, deploy, SSH, email, external APIs, production mutations) is never granted in this loop — if the goal needs it, stop and report that an approval gate is required. Never claim you performed shell, browser, SSH, repository, or external work; that is a handoff to the execution runner.",
		modeContract,
		finalLine,
	}, "\n")
}

func (r *anthropicFableRunner) userPrompt(job AgentJob) string {
	var builder strings.Builder
	builder.WriteString("Goal: ")
	builder.WriteString(job.Objective)
	builder.WriteString("\nMode: ")
	builder.WriteString(assistantToolLabel(job.Mode))
	builder.WriteString("\nRequested by: ")
	builder.WriteString(firstNonEmptyString(job.RequestedBy, "the room"))
	builder.WriteString("\n\nBoard and memory context: ")
	builder.WriteString(boardAndMemoryContextLine(job.Context.Board, job.Context.Memory))
	builder.WriteString("\n\nRecent durable memory:\n")
	for _, entry := range job.Context.Memory {
		builder.WriteString("- ")
		builder.WriteString(entry.Kind)
		if title := strings.TrimSpace(entry.Metadata["title"]); title != "" {
			builder.WriteString(" / ")
			builder.WriteString(title)
		}
		builder.WriteString(": ")
		builder.WriteString(compactAssistantLine(entry.Text))
		builder.WriteByte('\n')
	}
	return builder.String()
}

// orchestratorToolAllowlist is the curated subset of kanbanTools() the
// orchestrator may call in-process. It excludes UI/voice-only tools
// (control_app, set_voice_control, grill sessions) and recursive thread launches.
var orchestratorToolAllowlist = map[string]bool{
	"create_ticket":          true,
	"move_ticket":            true,
	"add_tags":               true,
	"add_key_date":           true,
	"remove_key_dates":       true,
	"update_ticket":          true,
	"create_artifact":        true,
	"update_artifact":        true,
	"answer_memory_question": true,
	"create_package":         true,
	"attach_to_package":      true,
	"advance_package_stage":  true,
	"send_notification":      true,
	"do_nothing":             true,
	// fiscal.ai financial grounding (read-only; keyless degrades to a clear
	// not-configured payload). The orchestrator gets all four, including the
	// docs + raw-query pair the voice surfaces exclude.
	"company_financial_snapshot": true,
	"financial_comps":            true,
	"fiscal_api_docs":            true,
	"fiscal_data_query":          true,
}

func (r *anthropicFableRunner) tools() []anthropicTool {
	var tools []anthropicTool
	for _, tool := range r.app.kanbanTools() {
		name := asString(tool["name"])
		if !orchestratorToolAllowlist[name] {
			continue
		}
		schema, _ := tool["parameters"].(map[string]any)
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools = append(tools, anthropicTool{
			Name:        name,
			Description: asString(tool["description"]),
			InputSchema: schema,
		})
	}
	return append(tools, reportGoalStateTool())
}

func reportGoalStateTool() anthropicTool {
	return anthropicTool{
		Name:        controlToolReportGoalState,
		Description: "Report the current goal status, review gate, stage, and progress so the Bonfire operator UI stays in step. Call whenever any of these change.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"goal_status":      map[string]any{"type": "string", "enum": []string{"running", "review", "approval_required", "verified", "needs_attention"}, "description": "Current goal status."},
				"review_gate":      map[string]any{"type": "string", "enum": []string{"pending", "passed", "blocked", "approval_required"}, "description": "Current review/ship gate state."},
				"stage":            map[string]any{"type": "string", "description": "Short stage id, e.g. execute_in_order or gate_before_shipping."},
				"progress_percent": map[string]any{"type": "integer", "description": "Completion percent from 0 to 100."},
				"note":             map[string]any{"type": "string", "description": "One short operator-voice line about the current step."},
			},
		},
	}
}

func (r *anthropicFableRunner) turnProgress(finalText string, control AgentProgress, turn int, response anthropicMessagesResponse) AgentProgress {
	progress := control
	progress.Metadata = r.evidence(turn)
	if response.Usage.OutputTokens > 0 {
		progress.Metadata["orchestratorOutputTokens"] = strconv.Itoa(response.Usage.OutputTokens)
	}
	if progress.Stage == "" {
		progress.Stage = "execute_in_order"
	}
	if progress.GoalStatus == "" {
		progress.GoalStatus = "running"
	}
	if progress.ReviewGate == "" {
		progress.ReviewGate = "pending"
	}
	if progress.Note == "" {
		progress.Note = compactAssistantLine(finalText)
	}
	return progress
}

func (r *anthropicFableRunner) terminalProgress(job AgentJob, finalText string, control AgentProgress, turn int, response anthropicMessagesResponse) AgentProgress {
	progress := control
	progress.Terminal = true
	progress.Text = r.composeArtifact(job, finalText, turn)
	progress.Metadata = r.evidence(turn)
	if response.Usage.OutputTokens > 0 {
		progress.Metadata["orchestratorOutputTokens"] = strconv.Itoa(response.Usage.OutputTokens)
	}
	if progress.GoalStatus == "" {
		progress.GoalStatus = "verified"
	}
	if progress.ReviewGate == "" {
		progress.ReviewGate = "passed"
	}
	if progress.Stage == "" {
		progress.Stage = "verify_goal_completed"
	}
	if progress.ProgressPercent == 0 {
		progress.ProgressPercent = 100
	}
	if progress.Note == "" {
		progress.Note = compactAssistantLine(finalText)
	}
	return progress
}

// incompleteProgress handles a turn the model did not finish (max_tokens, and
// any other non-end_turn/non-tool_use stop). It preserves whatever partial text
// arrived but forces needs_attention/blocked so a truncated run is never
// delivered as verified.
func (r *anthropicFableRunner) incompleteProgress(job AgentJob, finalText string, control AgentProgress, turn int, response anthropicMessagesResponse) AgentProgress {
	stop := firstNonEmptyString(response.StopReason, "unknown")
	metadata := r.evidence(turn)
	metadata["orchestratorStop"] = stop
	if response.Usage.OutputTokens > 0 {
		metadata["orchestratorOutputTokens"] = strconv.Itoa(response.Usage.OutputTokens)
	}

	progress := control
	progress.Terminal = true
	progress.GoalStatus = "needs_attention"
	progress.ReviewGate = "blocked"
	if progress.Stage == "" {
		progress.Stage = "gate_before_shipping"
	}
	if progress.ProgressPercent == 0 {
		progress.ProgressPercent = 72
	}
	progress.Note = "orchestrator response was cut off (" + stop + ")"
	progress.Metadata = metadata
	progress.Err = fmt.Errorf("orchestrator response incomplete (stop_reason=%s)", stop)

	body := strings.TrimSpace(finalText)
	if body != "" {
		body += "\n\n"
	}
	body += "The orchestrator response was cut off (stop reason: " + stop + ") before the goal was verified. Raise BONFIRE_ORCHESTRATOR_MAX_TOKENS or retry."
	progress.Text = r.composeArtifact(job, body, turn)
	return progress
}

func (r *anthropicFableRunner) maxTurnsProgress(job AgentJob, finalText string) AgentProgress {
	metadata := r.evidence(r.maxTurns)
	metadata["orchestratorStop"] = "max_turns"
	body := strings.TrimSpace(finalText)
	if body != "" {
		body += "\n\n"
	}
	body += "The orchestrator reached its turn cap before verifying the goal."
	return AgentProgress{
		Terminal:        true,
		Stage:           "gate_before_shipping",
		ProgressPercent: 72,
		GoalStatus:      "needs_attention",
		ReviewGate:      "blocked",
		Note:            "orchestrator reached the turn cap",
		Text:            r.composeArtifact(job, body, r.maxTurns),
		Err:             fmt.Errorf("orchestrator reached max turns (%d)", r.maxTurns),
		Metadata:        metadata,
	}
}

func (r *anthropicFableRunner) composeArtifact(job AgentJob, finalText string, turns int) string {
	body := strings.TrimSpace(finalText)
	if body == "" {
		body = strings.Join([]string{
			"Scout work thread",
			"",
			"Vision: " + compactAssistantLine(job.Objective),
			"Status: complete",
			"",
			"The orchestrator finished without a written summary.",
		}, "\n")
	}
	return body + "\n\n" + r.evidenceFooter(turns)
}

func (r *anthropicFableRunner) evidenceFooter(turns int) string {
	if turns < 1 {
		turns = 1
	}
	lines := []string{
		"## Orchestrator evidence",
		"- Runner: anthropic_fable (in-process tool loop)",
		"- Model: " + r.model,
	}
	if r.fallbackUsed != "" {
		lines = append(lines, "- Fallback model: "+r.fallbackUsed+" (served after a refusal)")
	}
	lines = append(lines,
		"- Effort: "+r.effort,
		"- Turns: "+strconv.Itoa(turns),
	)
	return strings.Join(lines, "\n")
}

func (r *anthropicFableRunner) evidence(turn int) map[string]string {
	metadata := map[string]string{
		"worker":             agentRunnerAnthropicFable,
		"workerBoundary":     "anthropic_fable_tool_loop",
		"orchestratorModel":  r.model,
		"orchestratorEffort": r.effort,
	}
	if r.fallbackUsed != "" {
		metadata["orchestratorFallbackModel"] = r.fallbackUsed
	}
	if turn > 0 {
		metadata["orchestratorTurns"] = strconv.Itoa(turn)
	}
	return metadata
}

func terminalErrorProgress(metadata map[string]string, err error) AgentProgress {
	return AgentProgress{
		Terminal:        true,
		Stage:           "gate_before_shipping",
		ProgressPercent: 72,
		GoalStatus:      "needs_attention",
		ReviewGate:      "blocked",
		Err:             err,
		Metadata:        metadata,
	}
}

// --- block helpers -----------------------------------------------------------

func anthropicTextBlock(text string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"type": "text", "text": text})
	return raw
}

// anthropicImageBlock builds one base64 image content block — the additive
// image support on the raw-content seam (Wave 5 item 21). The wire shape is
// {"type":"image","source":{"type":"base64","media_type":...,"data":...}};
// json.Marshal never emits newlines into the base64 string, which the API
// requires.
func anthropicImageBlock(mediaType string, data []byte) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{
		"type": "image",
		"source": map[string]any{
			"type":       "base64",
			"media_type": strings.TrimSpace(mediaType),
			"data":       base64.StdEncoding.EncodeToString(data),
		},
	})
	return raw
}

// anthropicDocumentBlock mirrors anthropicImageBlock for PDFs (card 085):
// {"type":"document","source":{"type":"base64","media_type":"application/pdf",
// "data":...}} — the native document block the chat attachments ride, no beta
// header required. json.Marshal keeps the base64 newline-free here too.
func anthropicDocumentBlock(mediaType string, data []byte) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{
		"type": "document",
		"source": map[string]any{
			"type":       "base64",
			"media_type": strings.TrimSpace(mediaType),
			"data":       base64.StdEncoding.EncodeToString(data),
		},
	})
	return raw
}

func anthropicToolResultBlock(toolUseID string, content string, isError bool) json.RawMessage {
	block := map[string]any{
		"type":        "tool_result",
		"tool_use_id": toolUseID,
		"content":     content,
	}
	if isError {
		block["is_error"] = true
	}
	raw, _ := json.Marshal(block)
	return raw
}

func decodeAnthropicBlock(raw json.RawMessage) anthropicBlock {
	var block anthropicBlock
	_ = json.Unmarshal(raw, &block)
	return block
}

func decodeToolArgs(input json.RawMessage) map[string]any {
	args := map[string]any{}
	if len(input) == 0 {
		return args
	}
	_ = json.Unmarshal(input, &args)
	if args == nil {
		args = map[string]any{}
	}
	return args
}

func anthropicToolResultContent(result map[string]any, err error) (string, bool) {
	if err != nil {
		return err.Error(), true
	}
	if len(result) == 0 {
		return "ok", false
	}
	raw, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return "ok", false
	}
	return string(raw), false
}

func mergeGoalStateProgress(current AgentProgress, input json.RawMessage) AgentProgress {
	args := decodeToolArgs(input)
	if value := asString(args["goal_status"]); value != "" {
		current.GoalStatus = value
	}
	if value := asString(args["review_gate"]); value != "" {
		current.ReviewGate = value
	}
	if value := asString(args["stage"]); value != "" {
		current.Stage = value
	}
	if value := asString(args["note"]); value != "" {
		current.Note = value
	}
	if percent, ok := asOptionalInt(args["progress_percent"]); ok {
		current.ProgressPercent = percent
	}
	return current
}

func asOptionalInt(value any) (int, bool) {
	switch number := value.(type) {
	case float64:
		return int(number), true
	case int:
		return number, true
	case json.Number:
		parsed, err := number.Int64()
		return int(parsed), err == nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(number))
		return parsed, err == nil
	default:
		return 0, false
	}
}
