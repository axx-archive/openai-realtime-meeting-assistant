package main

import (
	"bytes"
	"context"
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
	anthropicMessagesURL      = "https://api.anthropic.com/v1/messages"
	anthropicAPIVersion       = "2023-06-01"
	defaultOrchestratorModel  = "claude-fable-5"
	controlToolReportGoalState = "report_goal_state"
)

var errAgentWorkerNotConfigured = errors.New("agent worker is not configured")

// currentAnthropicAPIKey mirrors currentOpenAIAPIKey: a single accessor so
// tests can inject via the environment and keyless-local degrades gracefully.
// The key is never logged or persisted.
func currentAnthropicAPIKey() string {
	return strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
}

func orchestratorModel() string {
	return getenvDefault("BONFIRE_ORCHESTRATOR_MODEL", defaultOrchestratorModel)
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

func orchestratorTimeout() time.Duration {
	return durationEnv("BONFIRE_ORCHESTRATOR_TIMEOUT", 5*time.Minute, 30*time.Second)
}

// --- Anthropic Messages API wire types ---------------------------------------

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
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

func createAnthropicMessagesResponseHTTP(ctx context.Context, apiKey string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return anthropicMessagesResponse{}, fmt.Errorf("ANTHROPIC_API_KEY is not configured")
	}
	// Fable 5: thinking is always on (omit the field), and sampling params are
	// rejected — send neither. Depth is controlled by output_config.effort.
	payload := map[string]any{
		"model":      request.Model,
		"max_tokens": request.MaxTokens,
		"messages":   request.Messages,
	}
	if strings.TrimSpace(request.System) != "" {
		payload["system"] = request.System
	}
	if len(request.Tools) > 0 {
		payload["tools"] = request.Tools
	}
	if strings.TrimSpace(request.Effort) != "" {
		payload["output_config"] = map[string]any{"effort": request.Effort}
	}

	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return anthropicMessagesResponse{}, fmt.Errorf("encode Anthropic messages request: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicMessagesURL, bytes.NewReader(rawPayload))
	if err != nil {
		return anthropicMessagesResponse{}, fmt.Errorf("create Anthropic messages request: %w", err)
	}
	httpRequest.Header.Set("x-api-key", apiKey)
	httpRequest.Header.Set("anthropic-version", anthropicAPIVersion)
	httpRequest.Header.Set("content-type", "application/json")

	response, err := (&http.Client{Timeout: 4 * time.Minute}).Do(httpRequest)
	if err != nil {
		return anthropicMessagesResponse{}, fmt.Errorf("call Anthropic messages: %w", err)
	}
	defer response.Body.Close()

	rawBody, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
	if err != nil {
		return anthropicMessagesResponse{}, fmt.Errorf("read Anthropic messages response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		// Reuse the OpenAI failure helper: log the upstream body server-side,
		// return a status-only error so a 401/429 body never reaches the browser.
		return anthropicMessagesResponse{}, apiRequestFailedError("Anthropic messages failed", response.Status, rawBody)
	}

	var body anthropicMessagesResponse
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return anthropicMessagesResponse{}, fmt.Errorf("decode Anthropic messages response: %w", err)
	}
	if body.Error != nil && strings.TrimSpace(body.Error.Message) != "" {
		return anthropicMessagesResponse{}, fmt.Errorf("Anthropic messages error: %s", strings.TrimSpace(body.Error.Message))
	}
	return body, nil
}

// --- The runner --------------------------------------------------------------

type anthropicFableRunner struct {
	app       *kanbanBoardApp
	apiKey    func() string
	model     string
	effort    string
	maxTurns  int
	maxTokens int
	responder anthropicMessagesResponder
}

func newAnthropicFableRunner(app *kanbanBoardApp) *anthropicFableRunner {
	return &anthropicFableRunner{
		app:       app,
		apiKey:    currentAnthropicAPIKey,
		model:     orchestratorModel(),
		effort:    orchestratorEffort(),
		maxTurns:  orchestratorMaxTurns(),
		maxTokens: orchestratorMaxTokens(),
		responder: createAnthropicMessagesResponse,
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
			response, err := r.responder(ctx, apiKey, anthropicMessagesRequest{
				Model:     r.model,
				System:    system,
				Messages:  messages,
				Tools:     tools,
				MaxTokens: r.maxTokens,
				Effort:    r.effort,
			})
			if err != nil {
				out <- terminalErrorProgress(r.evidence(turn), err)
				return
			}
			if response.StopReason == "refusal" {
				metadata := r.evidence(turn)
				metadata["orchestratorStop"] = "refusal"
				out <- AgentProgress{
					Terminal:        true,
					Stage:           "gate_before_shipping",
					ProgressPercent: 72,
					GoalStatus:      "needs_attention",
					ReviewGate:      "blocked",
					Note:            "orchestrator request was declined",
					Err:             fmt.Errorf("orchestrator request was declined by safety classifiers"),
					Metadata:        metadata,
				}
				return
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
	return strings.Join([]string{
		"You are Scout, the in-process orchestrator for Bonfire OS.",
		"You run a real tool loop: decompose the goal, act with the Bonfire tools available to you, review against the goal, gate before anything ships, and report what matters. Do not narrate a loop you did not run.",
		"Follow the ten-step goal loop in order: identify the goal, decompose the work, assign the right agent, coordinate dependencies, execute in order, review against the original goal, gate before shipping, save what worked, report only what matters, verify the goal or name the blocker.",
		"Call report_goal_state whenever the goal status, review gate, stage, or progress changes so the operator UI stays in step.",
		"Authority: this job is " + authority + ". read_only may inspect and report; workspace_write may change the board, memory, packages, and notifications; external_write (commit, push, deploy, SSH, email, external APIs, production mutations) is never granted in this loop — if the goal needs it, stop and report that an approval gate is required. Never claim you performed shell, browser, SSH, repository, or external work; that is a handoff to the execution runner.",
		agentThreadModeContract(job.Mode),
		"When the goal is met, write the finished artifact as your final message using stable Markdown headings (Vision, Goal, Context used, Work decomposition, Execution log, Review, Gate, What worked, Report, Next moves, Verification). Write in a practical operator voice.",
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
	return strings.Join([]string{
		"## Orchestrator evidence",
		"- Runner: anthropic_fable (in-process tool loop)",
		"- Model: " + r.model,
		"- Effort: " + r.effort,
		"- Turns: " + strconv.Itoa(turns),
	}, "\n")
}

func (r *anthropicFableRunner) evidence(turn int) map[string]string {
	metadata := map[string]string{
		"worker":             agentRunnerAnthropicFable,
		"workerBoundary":     "anthropic_fable_tool_loop",
		"orchestratorModel":  r.model,
		"orchestratorEffort": r.effort,
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
