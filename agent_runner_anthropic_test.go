package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mockAnthropicTextBlock(text string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"type": "text", "text": text})
	return raw
}

func mockAnthropicToolUseBlock(id string, name string, input map[string]any) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"type": "tool_use", "id": id, "name": name, "input": input})
	return raw
}

func collectProgress(out <-chan AgentProgress) []AgentProgress {
	var progresses []AgentProgress
	for progress := range out {
		progresses = append(progresses, progress)
	}
	return progresses
}

// The orchestrator runs a real tool loop: a tool_use response is dispatched to
// applyToolCallArgs, the tool_result is fed back, and the loop terminates on
// end_turn with the finished artifact.
func TestAnthropicFableRunnerToolLoopRoundTrip(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	var requests []anthropicMessagesRequest
	responder := func(_ context.Context, apiKey string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		requests = append(requests, request)
		if apiKey != "test-key" {
			t.Fatalf("apiKey=%q, want test-key", apiKey)
		}
		if len(requests) == 1 {
			return anthropicMessagesResponse{
				StopReason: "tool_use",
				Content: []json.RawMessage{
					mockAnthropicTextBlock("Starting on the Aurora package."),
					mockAnthropicToolUseBlock("toolu_1", "create_ticket", map[string]any{"title": "Orchestrated card"}),
				},
			}, nil
		}
		return anthropicMessagesResponse{
			StopReason: "end_turn",
			Content:    []json.RawMessage{mockAnthropicTextBlock("# Aurora plan\n\nThe goal is complete.")},
		}, nil
	}

	runner := newAnthropicFableRunner(app)
	runner.responder = responder
	runner.apiKey = func() string { return "test-key" }
	runner.maxTurns = 6

	cardsBefore := len(app.cards)
	job := app.newAgentJob(scoutAgentThread{ID: "agent-thread-workflow-1", Mode: "workflow", Query: "package the Aurora IP"})
	out, err := runner.RunJob(context.Background(), job)
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	progresses := collectProgress(out)

	if len(requests) != 2 {
		t.Fatalf("responder called %d times, want 2", len(requests))
	}
	// The first request carries the curated tools including the control tool.
	if !toolNamesContain(requests[0].Tools, "create_ticket") || !toolNamesContain(requests[0].Tools, controlToolReportGoalState) {
		t.Fatalf("first request tools missing curated set: %v", toolNames(requests[0].Tools))
	}
	if !strings.Contains(strings.ToLower(requests[0].System), "orchestrator") {
		t.Fatalf("system prompt missing orchestrator framing: %q", requests[0].System)
	}
	// The second request echoes the assistant turn and answers the tool_use with
	// a tool_result in one user turn.
	if len(requests[1].Messages) != 3 {
		t.Fatalf("second request has %d messages, want user+assistant+tool_result", len(requests[1].Messages))
	}
	if role := requests[1].Messages[2].Role; role != "user" {
		t.Fatalf("tool_result message role=%q, want user", role)
	}
	if !blockTypePresent(requests[1].Messages[2].Content, "tool_result") {
		t.Fatal("second request missing a tool_result block")
	}

	// applyToolCallArgs actually mutated the board, and the orchestrator's
	// create_ticket landed as a D4 draft (not an instant board card).
	if len(app.cards) != cardsBefore+1 {
		t.Fatalf("board has %d cards, want %d (create_ticket dispatched)", len(app.cards), cardsBefore+1)
	}
	if newest := app.cards[len(app.cards)-1]; !newest.Draft {
		t.Fatalf("orchestrator-created card Draft=false, want true (D4 gate bypassed)")
	}

	if len(progresses) < 2 {
		t.Fatalf("emitted %d progress updates, want >=2 (turn + terminal)", len(progresses))
	}
	terminal := progresses[len(progresses)-1]
	if !terminal.Terminal || terminal.Err != nil {
		t.Fatalf("terminal progress = %+v, want clean terminal", terminal)
	}
	if terminal.GoalStatus != "verified" || terminal.ReviewGate != "passed" || terminal.ProgressPercent != 100 {
		t.Fatalf("terminal defaults wrong: %+v", terminal)
	}
	if !strings.Contains(terminal.Text, "Aurora plan") || !strings.Contains(terminal.Text, "Orchestrator evidence") {
		t.Fatalf("terminal text missing body/evidence: %q", terminal.Text)
	}
}

// A report_goal_state gate (approval_required) reported mid-loop must survive
// into the terminal progress — the model ends its turn without another tool
// call, so the gate cannot be re-reported on the terminal turn.
func TestAnthropicFableRunnerControlToolStickyGate(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	calls := 0
	responder := func(_ context.Context, _ string, _ anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		calls++
		if calls == 1 {
			return anthropicMessagesResponse{
				StopReason: "tool_use",
				Content: []json.RawMessage{
					mockAnthropicToolUseBlock("toolu_1", controlToolReportGoalState, map[string]any{
						"goal_status": "approval_required", "review_gate": "approval_required",
						"stage": "gate_before_shipping", "progress_percent": 68,
					}),
				},
			}, nil
		}
		return anthropicMessagesResponse{
			StopReason: "end_turn",
			Content:    []json.RawMessage{mockAnthropicTextBlock("Stopping at the external-write gate.")},
		}, nil
	}

	runner := newAnthropicFableRunner(app)
	runner.responder = responder
	runner.apiKey = func() string { return "test-key" }
	runner.maxTurns = 6

	out, err := runner.RunJob(context.Background(), app.newAgentJob(scoutAgentThread{ID: "t", Mode: "workflow", Query: "deploy it"}))
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	terminal := collectProgress(out)
	last := terminal[len(terminal)-1]
	if !last.Terminal {
		t.Fatal("last progress not terminal")
	}
	if last.GoalStatus != "approval_required" || last.ReviewGate != "approval_required" || last.ProgressPercent != 68 {
		t.Fatalf("sticky gate not preserved on terminal: %+v", last)
	}
}

// A response truncated by max_tokens must NOT be delivered as verified/passed —
// Fable 5's always-on thinking can exhaust BONFIRE_ORCHESTRATOR_MAX_TOKENS, and
// a cut-off artifact violates the orchestrator's own gate-before-shipping rule.
func TestAnthropicFableRunnerTruncatedResponseNeedsAttention(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	calls := 0
	responder := func(_ context.Context, _ string, _ anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		calls++
		return anthropicMessagesResponse{
			StopReason: "max_tokens",
			Content:    []json.RawMessage{mockAnthropicTextBlock("# Partial plan\n\nStep 1 of the")},
		}, nil
	}

	runner := newAnthropicFableRunner(app)
	runner.responder = responder
	runner.apiKey = func() string { return "test-key" }
	runner.maxTurns = 6

	out, err := runner.RunJob(context.Background(), app.newAgentJob(scoutAgentThread{ID: "t", Mode: "workflow", Query: "big plan"}))
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	progresses := collectProgress(out)
	if calls != 1 {
		t.Fatalf("responder called %d times, want 1 (terminal on truncation)", calls)
	}
	last := progresses[len(progresses)-1]
	if !last.Terminal || last.Err == nil {
		t.Fatalf("truncation must terminate with an error: %+v", last)
	}
	if last.GoalStatus != "needs_attention" || last.ReviewGate != "blocked" {
		t.Fatalf("truncation status wrong: goalStatus=%q reviewGate=%q, want needs_attention/blocked", last.GoalStatus, last.ReviewGate)
	}
	if last.Metadata["orchestratorStop"] != "max_tokens" {
		t.Fatalf("orchestratorStop=%q, want max_tokens", last.Metadata["orchestratorStop"])
	}
	// The partial text is preserved, not silently dropped.
	if !strings.Contains(last.Text, "Partial plan") {
		t.Fatalf("truncated terminal text dropped the partial body: %q", last.Text)
	}
}

// The hard turn cap terminates the loop even when the model keeps calling tools.
func TestAnthropicFableRunnerRespectsMaxTurns(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	calls := 0
	responder := func(_ context.Context, _ string, _ anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		calls++
		return anthropicMessagesResponse{
			StopReason: "tool_use",
			Content:    []json.RawMessage{mockAnthropicToolUseBlock("toolu_loop", "do_nothing", map[string]any{"reason": "still working"})},
		}, nil
	}

	runner := newAnthropicFableRunner(app)
	runner.responder = responder
	runner.apiKey = func() string { return "test-key" }
	runner.maxTurns = 3

	out, err := runner.RunJob(context.Background(), app.newAgentJob(scoutAgentThread{ID: "t", Mode: "research", Query: "never finish"}))
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	progresses := collectProgress(out)
	if calls != 3 {
		t.Fatalf("responder called %d times, want maxTurns=3", calls)
	}
	last := progresses[len(progresses)-1]
	if !last.Terminal || last.Err == nil {
		t.Fatalf("expected terminal error at the turn cap: %+v", last)
	}
	if last.GoalStatus != "needs_attention" {
		t.Fatalf("turn-cap goalStatus=%q, want needs_attention", last.GoalStatus)
	}
}

// Missing key degrades cleanly to a terminal needs-attention progress with no
// network call — keyless-local never blocks the shell.
func TestAnthropicFableRunnerKeylessTerminates(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	runner := newAnthropicFableRunner(app)
	runner.apiKey = func() string { return "" }
	runner.responder = func(context.Context, string, anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		t.Fatal("responder must not be called without a key")
		return anthropicMessagesResponse{}, nil
	}

	out, err := runner.RunJob(context.Background(), app.newAgentJob(scoutAgentThread{ID: "t", Mode: "workflow", Query: "x"}))
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	progresses := collectProgress(out)
	last := progresses[len(progresses)-1]
	if !last.Terminal || last.Err == nil || last.GoalStatus != "needs_attention" {
		t.Fatalf("keyless terminal wrong: %+v", last)
	}
}

// A per-job budget (set by newAgentJob for the deliverable subtask) overrides
// the runner's env defaults in the outgoing request; a job with no override
// keeps the runner default. This is the wire proof that the heavier deliverable
// budget actually reaches the model.
func TestAnthropicFableRunnerHonorsPerJobBudget(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	var got anthropicMessagesRequest
	responder := func(_ context.Context, _ string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		got = request
		return anthropicMessagesResponse{StopReason: "end_turn", Content: []json.RawMessage{mockAnthropicTextBlock("# Done")}}, nil
	}

	deliverable := newAnthropicFableRunner(app)
	deliverable.responder = responder
	deliverable.apiKey = func() string { return "test-key" }
	deliverable.maxTokens = 4096
	deliverable.effort = "low"
	job := app.newAgentJob(scoutAgentThread{ID: "t", Mode: "design", Query: "write the deliverable"})
	job.MaxTokens = 8192
	job.Effort = "medium"
	out, err := deliverable.RunJob(context.Background(), job)
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	collectProgress(out)
	if got.MaxTokens != 8192 || got.Effort != "medium" {
		t.Fatalf("deliverable request budget=%d/%q, want 8192/medium", got.MaxTokens, got.Effort)
	}

	planning := newAnthropicFableRunner(app)
	planning.responder = responder
	planning.apiKey = func() string { return "test-key" }
	planning.maxTokens = 4096
	planning.effort = "low"
	out2, err := planning.RunJob(context.Background(), app.newAgentJob(scoutAgentThread{ID: "t2", Mode: "research", Query: "plan"}))
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	collectProgress(out2)
	if got.MaxTokens != 4096 || got.Effort != "low" {
		t.Fatalf("planning request budget=%d/%q, want runner default 4096/low", got.MaxTokens, got.Effort)
	}
}

// The raised Fable dials ship together: deliverables default to effort high
// with a 32K output ceiling, and the orchestrator timeout default is 15m so
// slow-but-good high-effort runs are not manufactured into failures.
func TestFableDialDefaults(t *testing.T) {
	t.Setenv("BONFIRE_DELIVERABLE_MAX_TOKENS", "")
	t.Setenv("BONFIRE_DELIVERABLE_EFFORT", "")
	t.Setenv("BONFIRE_ORCHESTRATOR_TIMEOUT", "")

	if got := deliverableMaxTokens(); got != 32768 {
		t.Fatalf("deliverableMaxTokens()=%d, want 32768", got)
	}
	if got := deliverableEffort(); got != "high" {
		t.Fatalf("deliverableEffort()=%q, want high", got)
	}
	if got := orchestratorTimeout(); got != 15*time.Minute {
		t.Fatalf("orchestratorTimeout()=%s, want 15m", got)
	}
}

// Env overrides keep working after the default bump, and junk values fall back
// to the new defaults instead of the old ones.
func TestFableDialEnvOverrides(t *testing.T) {
	t.Setenv("BONFIRE_DELIVERABLE_MAX_TOKENS", "65536")
	t.Setenv("BONFIRE_DELIVERABLE_EFFORT", "XHigh")
	t.Setenv("BONFIRE_ORCHESTRATOR_TIMEOUT", "45m")

	if got := deliverableMaxTokens(); got != 65536 {
		t.Fatalf("deliverableMaxTokens()=%d, want 65536", got)
	}
	if got := deliverableEffort(); got != "xhigh" {
		t.Fatalf("deliverableEffort()=%q, want xhigh", got)
	}
	if got := orchestratorTimeout(); got != 45*time.Minute {
		t.Fatalf("orchestratorTimeout()=%s, want 45m", got)
	}

	t.Setenv("BONFIRE_DELIVERABLE_MAX_TOKENS", "not-a-number")
	t.Setenv("BONFIRE_DELIVERABLE_EFFORT", "galactic")
	t.Setenv("BONFIRE_ORCHESTRATOR_TIMEOUT", "5s") // below the 30s minimum

	if got := deliverableMaxTokens(); got != 32768 {
		t.Fatalf("invalid max tokens fell back to %d, want 32768", got)
	}
	if got := deliverableEffort(); got != "high" {
		t.Fatalf("invalid effort fell back to %q, want high", got)
	}
	if got := orchestratorTimeout(); got != 15*time.Minute {
		t.Fatalf("sub-minimum timeout fell back to %s, want 15m", got)
	}
}

// sseBody assembles a Messages API SSE stream body from event payloads,
// mirroring the wire format (event: line + data: line per event).
func sseBody(events ...string) string {
	var builder strings.Builder
	for _, event := range events {
		builder.WriteString("event: ignored\n")
		builder.WriteString("data: ")
		builder.WriteString(event)
		builder.WriteString("\n\n")
	}
	return builder.String()
}

// The SSE accumulator must fold a streamed response into the exact struct the
// non-stream path produced: text via content_block_delta/text_delta, tool_use
// input via input_json_delta fragments, stop_reason and usage via
// message_delta. Proven by decoding the equivalent non-stream JSON body and
// comparing field by field.
func TestDecodeAnthropicSSEStreamMatchesNonStream(t *testing.T) {
	stream := sseBody(
		`{"type":"message_start","message":{"model":"claude-fable-5","usage":{"input_tokens":120,"output_tokens":3},"content":[],"stop_reason":null}}`,
		`{"type":"ping"}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-abc"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Starting on "}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"the Aurora package."}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"create_ticket","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"title\":"}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"Orchestrated card\"}"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":58}}`,
		`{"type":"message_stop"}`,
	)

	got, err := decodeAnthropicSSEStream(strings.NewReader(stream))
	if err != nil {
		t.Fatalf("decodeAnthropicSSEStream: %v", err)
	}

	nonStreamBody := `{
		"model": "claude-fable-5",
		"stop_reason": "tool_use",
		"usage": {"input_tokens": 120, "output_tokens": 58},
		"content": [
			{"type": "thinking", "thinking": "", "signature": "sig-abc"},
			{"type": "text", "text": "Starting on the Aurora package."},
			{"type": "tool_use", "id": "toolu_1", "name": "create_ticket", "input": {"title": "Orchestrated card"}}
		]
	}`
	var want anthropicMessagesResponse
	if err := json.Unmarshal([]byte(nonStreamBody), &want); err != nil {
		t.Fatalf("decode non-stream fixture: %v", err)
	}

	if got.Model != want.Model || got.StopReason != want.StopReason || got.Usage != want.Usage {
		t.Fatalf("stream envelope=%+v, want %+v", got, want)
	}
	if len(got.Content) != len(want.Content) {
		t.Fatalf("stream produced %d blocks, want %d", len(got.Content), len(want.Content))
	}
	for i := range want.Content {
		gotBlock, wantBlock := decodeAnthropicBlock(got.Content[i]), decodeAnthropicBlock(want.Content[i])
		if gotBlock.Type != wantBlock.Type || gotBlock.Text != wantBlock.Text || gotBlock.ID != wantBlock.ID || gotBlock.Name != wantBlock.Name {
			t.Fatalf("block %d = %+v, want %+v", i, gotBlock, wantBlock)
		}
		if gotArgs, wantArgs := decodeToolArgs(gotBlock.Input), decodeToolArgs(wantBlock.Input); len(gotArgs) != len(wantArgs) || gotArgs["title"] != wantArgs["title"] {
			t.Fatalf("block %d input=%v, want %v", i, gotArgs, wantArgs)
		}
	}
	// The reconstructed thinking block carries the accumulated signature so the
	// assistant turn can be echoed back on the next request.
	var thinking map[string]string
	if err := json.Unmarshal(got.Content[0], &thinking); err != nil || thinking["signature"] != "sig-abc" {
		t.Fatalf("thinking block signature=%q err=%v, want sig-abc", thinking["signature"], err)
	}
}

// An argument-less tool call streams no input_json_delta; the start block's
// empty-object input survives so decodeToolArgs still yields a usable map.
func TestDecodeAnthropicSSEStreamKeepsEmptyToolInput(t *testing.T) {
	stream := sseBody(
		`{"type":"message_start","message":{"model":"claude-fable-5","usage":{"input_tokens":10,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_9","name":"do_nothing","input":{}}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
		`{"type":"message_stop"}`,
	)
	got, err := decodeAnthropicSSEStream(strings.NewReader(stream))
	if err != nil {
		t.Fatalf("decodeAnthropicSSEStream: %v", err)
	}
	block := decodeAnthropicBlock(got.Content[0])
	if block.Name != "do_nothing" {
		t.Fatalf("block name=%q, want do_nothing", block.Name)
	}
	if args := decodeToolArgs(block.Input); len(args) != 0 {
		t.Fatalf("empty tool input decoded to %v, want empty map", args)
	}
}

// An in-stream error event surfaces as an error, never as a silent empty
// verified response.
func TestDecodeAnthropicSSEStreamErrorEvent(t *testing.T) {
	stream := sseBody(
		`{"type":"message_start","message":{"model":"claude-fable-5","usage":{"input_tokens":10,"output_tokens":1}}}`,
		`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
	)
	if _, err := decodeAnthropicSSEStream(strings.NewReader(stream)); err == nil || !strings.Contains(err.Error(), "Overloaded") {
		t.Fatalf("error event returned err=%v, want Overloaded error", err)
	}
}

// A stream cut off before message_stop leaves StopReason empty — the runner's
// default branch treats that as an incomplete turn (needs_attention), so a
// dropped connection can never ship as verified.
func TestDecodeAnthropicSSEStreamTruncatedLeavesStopReasonEmpty(t *testing.T) {
	stream := sseBody(
		`{"type":"message_start","message":{"model":"claude-fable-5","usage":{"input_tokens":10,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
	)
	got, err := decodeAnthropicSSEStream(strings.NewReader(stream))
	if err != nil {
		t.Fatalf("decodeAnthropicSSEStream: %v", err)
	}
	if got.StopReason != "" {
		t.Fatalf("truncated stream StopReason=%q, want empty (incomplete)", got.StopReason)
	}
}

func toolNames(tools []anthropicTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func toolNamesContain(tools []anthropicTool, want string) bool {
	for _, tool := range tools {
		if tool.Name == want {
			return true
		}
	}
	return false
}

func blockTypePresent(blocks []json.RawMessage, blockType string) bool {
	for _, raw := range blocks {
		if decodeAnthropicBlock(raw).Type == blockType {
			return true
		}
	}
	return false
}
