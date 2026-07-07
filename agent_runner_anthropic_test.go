package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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

// A body-echoing tool result (answer_memory_question surfacing an artifact, or a
// package tool returning a record that embeds a packaging-studio html_deck with
// base64 imagery) must never enter the orchestrator's message history whole. In
// prod a single 2.6MB deck artifact pushed the next turn's request to ~2.55M
// tokens > the 1M ceiling, 400ing every Samsung research run. The tool-result
// path had no size budget, even though the memory context lanes did (via
// truncateArtifactForContext).
func TestAnthropicToolResultContentCapsHugeResult(t *testing.T) {
	huge := strings.Repeat("A", 3_000_000) // ~3MB, mimics an inlined base64 deck body
	result := map[string]any{
		"ok":       true,
		"artifact": map[string]any{"id": "os-artifact-deck-1", "title": "Ember deck", "body": huge},
	}
	content, isErr := anthropicToolResultContent(result, nil)
	if isErr {
		t.Fatalf("isError=true, want false for a successful tool call")
	}
	if len(content) > orchestratorToolResultBudgetChars+256 {
		t.Fatalf("tool result content len=%d, want <= budget %d (+marker slack); a huge result overflows the model context", len(content), orchestratorToolResultBudgetChars)
	}
	if !strings.Contains(content, "truncated") {
		tail := content
		if len(content) > 160 {
			tail = content[len(content)-160:]
		}
		t.Fatalf("capped tool result missing a truncation marker; tail=%q", tail)
	}
}

// An error result is appended to the message history exactly like a success
// result, so it gets the same budget: a tool that wraps the offending oversized
// input into its error message must not overflow the context either.
func TestAnthropicToolResultContentCapsHugeError(t *testing.T) {
	hugeErr := errors.New(strings.Repeat("E", 2_000_000))
	content, isErr := anthropicToolResultContent(nil, hugeErr)
	if !isErr {
		t.Fatalf("isError=false, want true for an error result")
	}
	if len(content) > orchestratorToolResultBudgetChars+256 {
		t.Fatalf("error content len=%d, want <= budget %d; a huge error must be capped too", len(content), orchestratorToolResultBudgetChars)
	}
}

// The voice tool loop (OpenAI Realtime function_call_output) shares the same
// body-echoing tools; a large result must be capped there too, on a tighter
// budget than the text orchestrator.
func TestCapVoiceToolResultContentCapsHugeResult(t *testing.T) {
	huge := strings.Repeat("A", 3_000_000)
	got := capVoiceToolResultContent(huge)
	if len(got) > voiceToolResultBudgetChars+256 {
		t.Fatalf("voice result len=%d, want <= budget %d (+marker)", len(got), voiceToolResultBudgetChars)
	}
	if !strings.Contains(got, "truncated") {
		t.Fatal("capped voice result missing a truncation marker")
	}
	if voiceToolResultBudgetChars >= orchestratorToolResultBudgetChars {
		t.Fatalf("voice budget %d should be tighter than orchestrator budget %d", voiceToolResultBudgetChars, orchestratorToolResultBudgetChars)
	}
	small := `{"ok":true,"ticketId":"card-9"}`
	if capVoiceToolResultContent(small) != small {
		t.Fatalf("small voice result should pass through untouched, got %q", capVoiceToolResultContent(small))
	}
}

// The cap must leave normal-sized results untouched: the model still needs ids,
// statuses, and confirmations as verbatim, parseable JSON.
func TestAnthropicToolResultContentPreservesSmallResult(t *testing.T) {
	result := map[string]any{"ok": true, "ticketId": "card-42", "status": "backlog"}
	content, isErr := anthropicToolResultContent(result, nil)
	if isErr {
		t.Fatalf("isError=true, want false")
	}
	var round map[string]any
	if err := json.Unmarshal([]byte(content), &round); err != nil {
		t.Fatalf("small result should stay valid JSON, got %q (err %v)", content, err)
	}
	if round["ticketId"] != "card-42" || round["status"] != "backlog" {
		t.Fatalf("small result altered: %v", round)
	}
	if strings.Contains(content, "truncated") {
		t.Fatalf("small result should not be truncated: %q", content)
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

// A stop_reason "refusal" retries the SAME request once against the fallback
// model; a successful fallback turn continues the run instead of taking the
// needs_attention branch, and provenance (metadata + evidence footer) records
// which model produced the artifact.
func TestAnthropicFableRunnerRefusalFallbackSuccess(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	var requests []anthropicMessagesRequest
	responder := func(_ context.Context, _ string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		requests = append(requests, request)
		if request.Model == "claude-fable-5" {
			return anthropicMessagesResponse{StopReason: "refusal"}, nil
		}
		return anthropicMessagesResponse{
			StopReason: "end_turn",
			Content:    []json.RawMessage{mockAnthropicTextBlock("# Chain of title\n\nClean.")},
		}, nil
	}

	runner := newAnthropicFableRunner(app)
	runner.responder = responder
	runner.apiKey = func() string { return "test-key" }
	runner.model = "claude-fable-5"
	runner.fallbackModel = "claude-opus-4-8"
	runner.maxTurns = 6

	out, err := runner.RunJob(context.Background(), app.newAgentJob(scoutAgentThread{ID: "t", Mode: "workflow", Query: "is the chain of title clean?"}))
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	progresses := collectProgress(out)

	if len(requests) != 2 {
		t.Fatalf("responder called %d times, want 2 (primary refusal + fallback)", len(requests))
	}
	if requests[1].Model != "claude-opus-4-8" {
		t.Fatalf("fallback request model=%q, want claude-opus-4-8", requests[1].Model)
	}
	// The retry is the SAME request: only the model changes.
	if requests[1].System != requests[0].System || requests[1].Effort != requests[0].Effort ||
		requests[1].MaxTokens != requests[0].MaxTokens ||
		len(requests[1].Messages) != len(requests[0].Messages) ||
		len(requests[1].Tools) != len(requests[0].Tools) {
		t.Fatalf("fallback request differs beyond the model: %+v vs %+v", requests[1], requests[0])
	}

	last := progresses[len(progresses)-1]
	if !last.Terminal || last.Err != nil {
		t.Fatalf("fallback success terminal = %+v, want clean terminal", last)
	}
	if last.GoalStatus != "verified" || last.ReviewGate != "passed" {
		t.Fatalf("fallback success status wrong: %+v", last)
	}
	if last.Metadata["orchestratorFallbackModel"] != "claude-opus-4-8" {
		t.Fatalf("orchestratorFallbackModel=%q, want claude-opus-4-8", last.Metadata["orchestratorFallbackModel"])
	}
	if !strings.Contains(last.Text, "Fallback model: claude-opus-4-8") {
		t.Fatalf("evidence footer missing fallback provenance: %q", last.Text)
	}
}

// Only when the fallback ALSO refuses does the run take the existing
// needs_attention branch — with both the stop reason and the attempted
// fallback model recorded, and exactly one retry (no retry storm).
func TestAnthropicFableRunnerRefusalFallbackAlsoRefuses(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	calls := 0
	responder := func(_ context.Context, _ string, _ anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		calls++
		return anthropicMessagesResponse{StopReason: "refusal"}, nil
	}

	runner := newAnthropicFableRunner(app)
	runner.responder = responder
	runner.apiKey = func() string { return "test-key" }
	runner.fallbackModel = "claude-opus-4-8"
	runner.maxTurns = 6

	out, err := runner.RunJob(context.Background(), app.newAgentJob(scoutAgentThread{ID: "t", Mode: "workflow", Query: "x"}))
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	progresses := collectProgress(out)
	if calls != 2 {
		t.Fatalf("responder called %d times, want exactly 2 (one fallback retry)", calls)
	}
	last := progresses[len(progresses)-1]
	if !last.Terminal || last.Err == nil {
		t.Fatalf("double refusal must terminate with an error: %+v", last)
	}
	if last.GoalStatus != "needs_attention" || last.ReviewGate != "blocked" {
		t.Fatalf("double refusal status wrong: %+v", last)
	}
	if last.Metadata["orchestratorStop"] != "refusal" {
		t.Fatalf("orchestratorStop=%q, want refusal", last.Metadata["orchestratorStop"])
	}
	if last.Metadata["orchestratorFallbackModel"] != "claude-opus-4-8" {
		t.Fatalf("orchestratorFallbackModel=%q, want claude-opus-4-8 (the attempted fallback)", last.Metadata["orchestratorFallbackModel"])
	}
}

// A responder error during the fallback retry surfaces as the usual terminal
// error, never as a silent verified run.
func TestAnthropicFableRunnerRefusalFallbackError(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	calls := 0
	responder := func(_ context.Context, _ string, _ anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		calls++
		if calls == 1 {
			return anthropicMessagesResponse{StopReason: "refusal"}, nil
		}
		return anthropicMessagesResponse{}, context.DeadlineExceeded
	}

	runner := newAnthropicFableRunner(app)
	runner.responder = responder
	runner.apiKey = func() string { return "test-key" }
	runner.maxTurns = 6

	out, err := runner.RunJob(context.Background(), app.newAgentJob(scoutAgentThread{ID: "t", Mode: "workflow", Query: "x"}))
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	progresses := collectProgress(out)
	last := progresses[len(progresses)-1]
	if !last.Terminal || last.Err == nil || last.GoalStatus != "needs_attention" {
		t.Fatalf("fallback error terminal wrong: %+v", last)
	}
}

// A refusal on a LATER turn — when the replayed history already carries a
// Fable assistant turn with thinking blocks (model-specific signatures) — must
// still recover via the fallback: the documented cross-model contract is that
// a different model silently drops Fable thinking blocks from the replayed
// prompt, so the runner replays the history byte-for-byte, thinking blocks
// included, and the run continues. This is exactly the multi-turn case the
// fallback was added for.
func TestAnthropicFableRunnerRefusalFallbackMidRunReplaysThinkingBlocks(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	thinkingBlock, _ := json.Marshal(map[string]any{
		"type":      "thinking",
		"thinking":  "",
		"signature": "fable-signature-abc123",
	})

	var requests []anthropicMessagesRequest
	responder := func(_ context.Context, _ string, request anthropicMessagesRequest) (anthropicMessagesResponse, error) {
		requests = append(requests, request)
		switch len(requests) {
		case 1:
			// Turn 1 (primary): a Fable turn carrying a thinking block ahead of
			// the tool call — both must be echoed into the next turn's history.
			return anthropicMessagesResponse{
				StopReason: "tool_use",
				Content: []json.RawMessage{
					json.RawMessage(thinkingBlock),
					mockAnthropicToolUseBlock("toolu_1", "do_nothing", map[string]any{}),
				},
			}, nil
		case 2:
			// Turn 2 (primary): refusal mid-run, with thinking blocks now in history.
			return anthropicMessagesResponse{StopReason: "refusal"}, nil
		default:
			// Turn 2 (fallback): serves the terminal turn.
			return anthropicMessagesResponse{
				StopReason: "end_turn",
				Content:    []json.RawMessage{mockAnthropicTextBlock("# Done\n\nRecovered on the fallback.")},
			}, nil
		}
	}

	runner := newAnthropicFableRunner(app)
	runner.responder = responder
	runner.apiKey = func() string { return "test-key" }
	runner.model = "claude-fable-5"
	runner.fallbackModel = "claude-opus-4-8"
	runner.maxTurns = 6

	out, err := runner.RunJob(context.Background(), app.newAgentJob(scoutAgentThread{ID: "t", Mode: "workflow", Query: "multi-turn"}))
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	progresses := collectProgress(out)

	if len(requests) != 3 {
		t.Fatalf("responder called %d times, want 3 (turn 1 + turn 2 refusal + fallback)", len(requests))
	}
	if requests[2].Model != "claude-opus-4-8" {
		t.Fatalf("fallback model=%q, want claude-opus-4-8", requests[2].Model)
	}
	// The fallback replays the refused turn's history BYTE-FOR-BYTE — thinking
	// blocks included (the API drops them server-side for a different model;
	// client-side stripping is what breaks).
	refused, _ := json.Marshal(requests[1].Messages)
	replayed, _ := json.Marshal(requests[2].Messages)
	if string(refused) != string(replayed) {
		t.Fatalf("fallback history differs from the refused request:\nrefused:  %s\nreplayed: %s", refused, replayed)
	}
	foundThinking := false
	for _, message := range requests[2].Messages {
		if message.Role != "assistant" {
			continue
		}
		for _, raw := range message.Content {
			if decodeAnthropicBlock(raw).Type == "thinking" {
				foundThinking = true
			}
		}
	}
	if !foundThinking {
		t.Fatal("replayed fallback history carries no assistant thinking block — the test lost its premise")
	}

	last := progresses[len(progresses)-1]
	if !last.Terminal || last.Err != nil || last.GoalStatus != "verified" {
		t.Fatalf("mid-run fallback terminal = %+v, want clean verified terminal", last)
	}
	if last.Metadata["orchestratorFallbackModel"] != "claude-opus-4-8" {
		t.Fatalf("orchestratorFallbackModel=%q, want claude-opus-4-8", last.Metadata["orchestratorFallbackModel"])
	}
}

// The refusal-fallback model defaults to claude-opus-4-8 and stays
// env-overridable, mirroring the other Fable dials.
func TestOrchestratorFallbackModelDial(t *testing.T) {
	t.Setenv("BONFIRE_FALLBACK_MODEL", "")
	if got := orchestratorFallbackModel(); got != "claude-opus-4-8" {
		t.Fatalf("orchestratorFallbackModel()=%q, want claude-opus-4-8", got)
	}
	t.Setenv("BONFIRE_FALLBACK_MODEL", "claude-opus-4-7")
	if got := orchestratorFallbackModel(); got != "claude-opus-4-7" {
		t.Fatalf("orchestratorFallbackModel()=%q, want the claude-opus-4-7 override", got)
	}
}

// Prompt-cache breakpoints land at the stable prefixes of the wire payload —
// the last tool definition, the system prompt, and the newest two user turns —
// never on assistant blocks, and never more than the API's 4-breakpoint cap.
func TestBuildAnthropicMessagesPayloadCacheBreakpoints(t *testing.T) {
	request := anthropicMessagesRequest{
		Model:  "claude-fable-5",
		System: "You are Scout, the in-process orchestrator.",
		Tools: []anthropicTool{
			{Name: "create_ticket", InputSchema: map[string]any{"type": "object"}},
			{Name: "do_nothing", InputSchema: map[string]any{"type": "object"}},
		},
		Messages: []anthropicMessage{
			{Role: "user", Content: []json.RawMessage{mockAnthropicTextBlock("Goal: package the Aurora IP")}},
			{Role: "assistant", Content: []json.RawMessage{
				mockAnthropicTextBlock("Working."),
				mockAnthropicToolUseBlock("toolu_1", "do_nothing", map[string]any{}),
			}},
			{Role: "user", Content: []json.RawMessage{anthropicToolResultBlock("toolu_1", "ok", false)}},
			{Role: "assistant", Content: []json.RawMessage{mockAnthropicToolUseBlock("toolu_2", "do_nothing", map[string]any{})}},
			{Role: "user", Content: []json.RawMessage{
				anthropicToolResultBlock("toolu_2", "ok", false),
				mockAnthropicTextBlock("continue"),
			}},
		},
		MaxTokens: 4096,
		Effort:    "low",
	}

	raw, err := buildAnthropicMessagesPayload(request)
	if err != nil {
		t.Fatalf("buildAnthropicMessagesPayload: %v", err)
	}

	// Never more than 4 breakpoints; this multi-turn shape uses all 4.
	if count := strings.Count(string(raw), `"cache_control"`); count != 4 {
		t.Fatalf("payload carries %d cache_control breakpoints, want exactly 4: %s", count, raw)
	}

	var payload struct {
		System   []map[string]json.RawMessage `json:"system"`
		Tools    []map[string]json.RawMessage `json:"tools"`
		Messages []struct {
			Role    string                       `json:"role"`
			Content []map[string]json.RawMessage `json:"content"`
		} `json:"messages"`
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !payload.Stream {
		t.Fatal("payload lost stream:true")
	}

	// System becomes a block array whose text block carries the breakpoint.
	if len(payload.System) != 1 {
		t.Fatalf("system has %d blocks, want 1", len(payload.System))
	}
	if _, marked := payload.System[0]["cache_control"]; !marked {
		t.Fatal("system prompt block missing its cache_control breakpoint")
	}

	// Only the LAST tool carries the breakpoint (tools render first; the
	// marker caches the whole tool prefix).
	if _, marked := payload.Tools[0]["cache_control"]; marked {
		t.Fatal("first tool must not carry a cache_control breakpoint")
	}
	if _, marked := payload.Tools[1]["cache_control"]; !marked {
		t.Fatal("last tool missing its cache_control breakpoint")
	}

	// The newest two user turns' last blocks carry the remaining breakpoints;
	// assistant blocks (Fable thinking echo) and older user turns stay clean.
	for i, message := range payload.Messages {
		for j, block := range message.Content {
			_, marked := block["cache_control"]
			wantMarked := (i == 4 && j == 1) || (i == 2 && j == 0)
			if marked != wantMarked {
				t.Fatalf("message %d block %d (role %s) cache_control=%v, want %v", i, j, message.Role, marked, wantMarked)
			}
		}
	}

	// The caller's history is never mutated — persisted markers would
	// accumulate past the 4-breakpoint cap on later turns of the loop.
	for i, message := range request.Messages {
		for j, block := range message.Content {
			if strings.Contains(string(block), "cache_control") {
				t.Fatalf("buildAnthropicMessagesPayload mutated request.Messages[%d].Content[%d]", i, j)
			}
		}
	}
}

// An empty Effort keeps output_config OFF the wire entirely — the router turn
// rides claude-haiku-4-5, which 400s on output_config.effort, so the omission
// is what keeps every routing turn from silently degrading to inline answers.
func TestBuildAnthropicMessagesPayloadOmitsOutputConfigWithoutEffort(t *testing.T) {
	raw, err := buildAnthropicMessagesPayload(anthropicMessagesRequest{
		Model:     "claude-haiku-4-5",
		Messages:  []anthropicMessage{{Role: "user", Content: []json.RawMessage{mockAnthropicTextBlock("route this")}}},
		MaxTokens: 700,
		Effort:    "",
	})
	if err != nil {
		t.Fatalf("buildAnthropicMessagesPayload: %v", err)
	}
	if strings.Contains(string(raw), "output_config") {
		t.Fatalf("payload carries output_config despite empty Effort (Haiku 4.5 rejects it): %s", raw)
	}

	// And a non-empty Effort still lands inside output_config.
	raw, err = buildAnthropicMessagesPayload(anthropicMessagesRequest{
		Model:     "claude-fable-5",
		Messages:  []anthropicMessage{{Role: "user", Content: []json.RawMessage{mockAnthropicTextBlock("go")}}},
		MaxTokens: 4096,
		Effort:    "low",
	})
	if err != nil {
		t.Fatalf("buildAnthropicMessagesPayload: %v", err)
	}
	if !strings.Contains(string(raw), `"output_config":{"effort":"low"}`) {
		t.Fatalf("payload missing output_config.effort: %s", raw)
	}
}

// DisableThinking emits thinking:{type:"disabled"} (the chat/follow-up text
// path on Sonnet 5, where omitting the field silently runs ADAPTIVE thinking
// inside the same max_tokens budget); the default leaves the field off the
// wire entirely — the Fable 5 orchestrator 400s on an explicit disabled.
func TestBuildAnthropicMessagesPayloadThinkingDial(t *testing.T) {
	base := anthropicMessagesRequest{
		Model:     "claude-sonnet-5",
		Messages:  []anthropicMessage{{Role: "user", Content: []json.RawMessage{mockAnthropicTextBlock("hi")}}},
		MaxTokens: 800,
	}

	raw, err := buildAnthropicMessagesPayload(base)
	if err != nil {
		t.Fatalf("buildAnthropicMessagesPayload: %v", err)
	}
	if strings.Contains(string(raw), `"thinking"`) {
		t.Fatalf("default payload must omit the thinking field (Fable 5 rejects explicit config): %s", raw)
	}

	base.DisableThinking = true
	raw, err = buildAnthropicMessagesPayload(base)
	if err != nil {
		t.Fatalf("buildAnthropicMessagesPayload: %v", err)
	}
	if !strings.Contains(string(raw), `"thinking":{"type":"disabled"}`) {
		t.Fatalf("DisableThinking payload missing thinking:{type:disabled}: %s", raw)
	}
}

// The first turn of a run (one user message) and a bare request (no system, no
// tools) stay under the breakpoint cap and keep their markers on what exists.
func TestBuildAnthropicMessagesPayloadCacheBreakpointBounds(t *testing.T) {
	firstTurn := anthropicMessagesRequest{
		Model:  "claude-fable-5",
		System: "You are Scout.",
		Tools:  []anthropicTool{{Name: "do_nothing", InputSchema: map[string]any{"type": "object"}}},
		Messages: []anthropicMessage{
			{Role: "user", Content: []json.RawMessage{mockAnthropicTextBlock("Goal: plan")}},
		},
		MaxTokens: 4096,
	}
	raw, err := buildAnthropicMessagesPayload(firstTurn)
	if err != nil {
		t.Fatalf("buildAnthropicMessagesPayload: %v", err)
	}
	if count := strings.Count(string(raw), `"cache_control"`); count != 3 {
		t.Fatalf("first-turn payload carries %d breakpoints, want 3 (tool + system + user turn): %s", count, raw)
	}

	bare := anthropicMessagesRequest{
		Model: "claude-fable-5",
		Messages: []anthropicMessage{
			{Role: "user", Content: []json.RawMessage{mockAnthropicTextBlock("hello")}},
		},
		MaxTokens: 256,
	}
	raw, err = buildAnthropicMessagesPayload(bare)
	if err != nil {
		t.Fatalf("buildAnthropicMessagesPayload: %v", err)
	}
	if count := strings.Count(string(raw), `"cache_control"`); count != 1 {
		t.Fatalf("bare payload carries %d breakpoints, want 1 (user turn only): %s", count, raw)
	}
	if strings.Contains(string(raw), `"system"`) {
		t.Fatalf("bare payload grew a system field: %s", raw)
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

// The image block helper emits the exact Messages API wire shape —
// {"type":"image","source":{"type":"base64","media_type":...,"data":...}} —
// and the payload builder passes it through the raw-content seam untouched
// (Wave 5 item 21: the vision slide juries ride this seam).
func TestAnthropicImageBlockWireShape(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10}
	raw, err := buildAnthropicMessagesPayload(anthropicMessagesRequest{
		Model: "claude-fable-5",
		Messages: []anthropicMessage{{
			Role: "user",
			Content: []json.RawMessage{
				anthropicImageBlock("image/jpeg", jpeg),
				mockAnthropicTextBlock("Score this rendered page."),
			},
		}},
		MaxTokens: 4096,
	})
	if err != nil {
		t.Fatalf("buildAnthropicMessagesPayload: %v", err)
	}

	var payload struct {
		Messages []struct {
			Content []struct {
				Type   string `json:"type"`
				Source struct {
					Type      string `json:"type"`
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
				} `json:"source"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	block := payload.Messages[0].Content[0]
	if block.Type != "image" || block.Source.Type != "base64" || block.Source.MediaType != "image/jpeg" {
		t.Fatalf("image block wire shape wrong: %+v", block)
	}
	decoded, err := base64.StdEncoding.DecodeString(block.Source.Data)
	if err != nil || !bytes.Equal(decoded, jpeg) {
		t.Fatalf("image data did not round-trip: err=%v data=%q", err, block.Source.Data)
	}
	if strings.Contains(block.Source.Data, "\n") {
		t.Fatal("base64 image data carries newlines — the API rejects them")
	}
}

// The defensive request-level image budget: more than 12 image blocks, or more
// than ~20MB of decoded image payload, refuses to build a payload the API
// would reject opaquely. At-cap requests still build.
func TestBuildAnthropicMessagesPayloadImageCaps(t *testing.T) {
	imageTurn := func(count int, size int) anthropicMessagesRequest {
		content := make([]json.RawMessage, 0, count+1)
		for i := 0; i < count; i++ {
			content = append(content, anthropicImageBlock("image/jpeg", bytes.Repeat([]byte{0xab}, size)))
		}
		content = append(content, mockAnthropicTextBlock("score the pages"))
		return anthropicMessagesRequest{
			Model:     "claude-fable-5",
			Messages:  []anthropicMessage{{Role: "user", Content: content}},
			MaxTokens: 4096,
		}
	}

	if _, err := buildAnthropicMessagesPayload(imageTurn(anthropicMaxRequestImages, 64)); err != nil {
		t.Fatalf("at-cap request must build: %v", err)
	}
	if _, err := buildAnthropicMessagesPayload(imageTurn(anthropicMaxRequestImages+1, 64)); err == nil || !strings.Contains(err.Error(), "image blocks") {
		t.Fatalf("13-image request built (err=%v), want the image-count cap error", err)
	}
	// One image whose decoded payload alone clears the ~20MB byte budget.
	if _, err := buildAnthropicMessagesPayload(imageTurn(1, anthropicMaxRequestImageBytes+(1<<20))); err == nil || !strings.Contains(err.Error(), "image payload") {
		t.Fatalf("oversized image payload built (err=%v), want the byte-cap error", err)
	}
}

// Cache breakpoints never land on image blocks: within a marked user turn the
// marker moves to the last non-image block, and an all-image turn is skipped
// without spending the breakpoint budget.
func TestBuildAnthropicMessagesPayloadCacheBreakpointNeverOnImages(t *testing.T) {
	image := anthropicImageBlock("image/jpeg", []byte{0xff, 0xd8})
	request := anthropicMessagesRequest{
		Model: "claude-fable-5",
		Messages: []anthropicMessage{
			{Role: "user", Content: []json.RawMessage{mockAnthropicTextBlock("Goal: jury the deck")}},
			{Role: "assistant", Content: []json.RawMessage{mockAnthropicTextBlock("Working.")}},
			{Role: "user", Content: []json.RawMessage{image, mockAnthropicTextBlock("score page 1")}},
			{Role: "assistant", Content: []json.RawMessage{mockAnthropicTextBlock("Scored.")}},
			{Role: "user", Content: []json.RawMessage{image, image}}, // all-image turn
		},
		MaxTokens: 4096,
	}
	raw, err := buildAnthropicMessagesPayload(request)
	if err != nil {
		t.Fatalf("buildAnthropicMessagesPayload: %v", err)
	}
	var payload struct {
		Messages []struct {
			Role    string                       `json:"role"`
			Content []map[string]json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	for i, message := range payload.Messages {
		for j, block := range message.Content {
			_, marked := block["cache_control"]
			// The newest markable user turns: message 2 marks its TEXT block
			// (index 1, not the image at 0); the all-image turn (message 4) is
			// skipped, so the budget falls back to message 0's text block.
			wantMarked := (i == 2 && j == 1) || (i == 0 && j == 0)
			if marked != wantMarked {
				t.Fatalf("message %d block %d cache_control=%v, want %v", i, j, marked, wantMarked)
			}
			var blockType string
			_ = json.Unmarshal(block["type"], &blockType)
			if marked && blockType == "image" {
				t.Fatalf("message %d block %d is a MARKED image block", i, j)
			}
		}
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

// A research run's bar used to freeze at the 35% launch scaffold until the
// terminal write: turnProgress never set ProgressPercent. Every non-terminal
// turn now climbs a heuristic (typical runs are 3-8 turns of the 24-turn cap),
// a report_goal_state percent ahead of the heuristic wins, and the job-local
// high-water mark keeps the bar monotonic when a later report comes in lower.
func TestAnthropicFableRunnerTurnProgressRisingMonotonic(t *testing.T) {
	runner := &anthropicFableRunner{model: "test-model", effort: "high"}
	steps := []struct {
		turn     int
		reported int // percent the sticky control carries from report_goal_state
		want     int
	}{
		{turn: 1, reported: 0, want: 41},  // 35 + (55*1)/8
		{turn: 2, reported: 0, want: 48},  // keeps climbing per turn
		{turn: 3, reported: 80, want: 80}, // a model report ahead of the heuristic wins
		{turn: 4, reported: 20, want: 80}, // high-water: a lower report never walks the bar back
		{turn: 9, reported: 0, want: 92},  // heuristic parks at 92 — 100/72 stays terminal's call
		{turn: 24, reported: 0, want: 92},
	}
	for _, step := range steps {
		progress := runner.turnProgress("working", AgentProgress{ProgressPercent: step.reported}, step.turn, anthropicMessagesResponse{})
		if progress.ProgressPercent != step.want {
			t.Fatalf("turn %d (reported %d): percent=%d, want %d", step.turn, step.reported, progress.ProgressPercent, step.want)
		}
		if progress.Terminal {
			t.Fatalf("turn %d: non-terminal turn marked terminal", step.turn)
		}
	}
}

// Each turn's note names what the orchestrator is doing RIGHT NOW: an explicit
// report_goal_state note from this turn wins, else the tool being called maps
// to a short human phrase (unknown tools read as their name), else the sticky
// control note holds, else the latest assistant prose.
func TestAnthropicFableRunnerTurnProgressNotes(t *testing.T) {
	for _, tt := range []struct {
		name     string
		control  AgentProgress
		text     string
		response anthropicMessagesResponse
		want     string
	}{
		{
			name: "memory tool maps to a human phrase",
			response: anthropicMessagesResponse{Content: []json.RawMessage{
				mockAnthropicToolUseBlock("toolu_1", "answer_memory_question", map[string]any{"question": "what shipped?"}),
			}},
			want: "consulting memory",
		},
		{
			name: "fiscal tool maps to a human phrase",
			response: anthropicMessagesResponse{Content: []json.RawMessage{
				mockAnthropicToolUseBlock("toolu_1", "fiscal_data_query", map[string]any{"query": "revenue"}),
			}},
			want: "querying fiscal data",
		},
		{
			name: "report_goal_state note this turn beats the tool phrase",
			response: anthropicMessagesResponse{Content: []json.RawMessage{
				mockAnthropicToolUseBlock("toolu_1", controlToolReportGoalState, map[string]any{"note": "reviewing sources"}),
				mockAnthropicToolUseBlock("toolu_2", "answer_memory_question", map[string]any{}),
			}},
			want: "reviewing sources",
		},
		{
			name: "unknown tool falls back to its name",
			response: anthropicMessagesResponse{Content: []json.RawMessage{
				mockAnthropicToolUseBlock("toolu_1", "future_tool", map[string]any{}),
			}},
			want: "future tool",
		},
		{
			name:    "do_nothing stays silent so the sticky note holds",
			control: AgentProgress{Note: "drafting the report"},
			response: anthropicMessagesResponse{Content: []json.RawMessage{
				mockAnthropicToolUseBlock("toolu_1", "do_nothing", map[string]any{}),
			}},
			want: "drafting the report",
		},
		{
			name: "no tools falls back to the assistant prose",
			text: "Working through the evidence now.",
			want: "Working through the evidence now.",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runner := &anthropicFableRunner{model: "test-model", effort: "high"}
			progress := runner.turnProgress(tt.text, tt.control, 1, tt.response)
			if progress.Note != tt.want {
				t.Fatalf("Note=%q, want %q", progress.Note, tt.want)
			}
		})
	}
}
