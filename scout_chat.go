package main

// Private text chat with Scout, delivered only to the requesting websocket
// connection. Answers reuse the shared room memory store and board: per-user
// memory scoping is an open product decision, so every member currently chats
// against the same room-wide knowledge while delivery stays per-connection.
//
// Wire protocol (kanban envelope, sent only to the requesting connection):
//   client -> server  ws event "scout_chat" with data {"text": "..."}
//   server -> client  kanban event "scout_chat" with data
//                     {"kind":"query"|"status"|"answer"|"error","text":...,"ts":RFC3339Nano}
//
// Lifecycle: submit runs on the websocket read goroutine and echoes the query
// immediately (a message must never look dropped while an earlier turn is
// still answering), then hands the text to a single per-session worker that
// answers strictly FIFO. The queue is bounded; the worker's model calls are
// tied to a per-connection context cancelled when the websocket closes, so a
// disconnected client cannot leave a backlog of model calls running.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	// scoutChatMaxHistoryTurns bounds the per-connection conversation history;
	// one turn is one user or scout message.
	scoutChatMaxHistoryTurns = 12
	// scoutChatMaxQueuedTurns bounds unanswered messages per connection.
	scoutChatMaxQueuedTurns = 8
)

type scoutChatTurn struct {
	role string // "user" or "scout"
	text string
}

type scoutChatTurnPayload struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

func scoutChatHistoryFromPayload(turns []scoutChatTurnPayload) []scoutChatTurn {
	if len(turns) == 0 {
		return nil
	}
	start := 0
	if len(turns) > scoutChatMaxHistoryTurns {
		start = len(turns) - scoutChatMaxHistoryTurns
	}
	history := make([]scoutChatTurn, 0, len(turns)-start)
	for _, turn := range turns[start:] {
		role := strings.ToLower(strings.TrimSpace(turn.Role))
		switch role {
		case "assistant", "scout":
			role = "scout"
		case "user":
			role = "user"
		default:
			continue
		}
		text := strings.TrimSpace(turn.Text)
		if text == "" {
			continue
		}
		history = append(history, scoutChatTurn{role: role, text: text})
	}
	return history
}

type scoutChatSession struct {
	mu         sync.Mutex
	send       func(event string, data any) error
	turns      []scoutChatTurn
	queue      chan string
	ctx        context.Context
	cancel     context.CancelFunc
	workerOnce sync.Once
}

func newScoutChatSession(conn *threadSafeWriter) *scoutChatSession {
	return newScoutChatSessionWithSend(func(event string, data any) error {
		return sendKanbanEvent(conn, event, data)
	})
}

func newScoutChatSessionWithSend(send func(event string, data any) error) *scoutChatSession {
	ctx, cancel := context.WithCancel(context.Background())

	return &scoutChatSession{
		send:   send,
		queue:  make(chan string, scoutChatMaxQueuedTurns),
		ctx:    ctx,
		cancel: cancel,
	}
}

// close stops the worker and cancels any queued or in-flight model calls;
// called when the owning websocket connection ends.
func (session *scoutChatSession) close() {
	if session == nil || session.cancel == nil {
		return
	}
	session.cancel()
}

// submit accepts one private chat message on the websocket read goroutine:
// it echoes the query and a thinking status synchronously (before any model
// work), then queues the message for the FIFO worker.
func (session *scoutChatSession) submit(app *kanbanBoardApp, text string, actor string) {
	if session == nil {
		return
	}

	text = strings.TrimSpace(text)
	if text == "" {
		session.sendEvent("error", "say something first")
		return
	}

	session.sendEvent("query", text)
	session.sendEvent("status", "thinking…")

	session.workerOnce.Do(func() {
		go session.runWorker(app)
	})

	select {
	case session.queue <- text:
	default:
		session.sendEvent("error", "Scout is still answering — try again in a moment")
	}
}

// runWorker answers queued messages strictly FIFO until the session closes.
func (session *scoutChatSession) runWorker(app *kanbanBoardApp) {
	for {
		select {
		case <-session.ctx.Done():
			return
		case text := <-session.queue:
			session.answer(app, text)
		}
	}
}

// answer resolves one queued message against the shared answer engine and
// threads the turn into this session's history.
func (session *scoutChatSession) answer(app *kanbanBoardApp, text string) {
	if session.ctx != nil && session.ctx.Err() != nil {
		return // connection gone; drop the backlog silently
	}

	session.mu.Lock()
	history := make([]scoutChatTurn, len(session.turns))
	copy(history, session.turns)
	session.mu.Unlock()

	result, err := app.resolveAssistantQueryContext(session.ctx, text, history)
	if session.ctx != nil && session.ctx.Err() != nil {
		return // cancelled mid-call; nobody is listening for this answer
	}
	if err != nil {
		session.sendEvent("error", err.Error())
		return
	}

	session.mu.Lock()
	session.turns = append(session.turns,
		scoutChatTurn{role: "user", text: result.query},
		scoutChatTurn{role: "scout", text: result.answer},
	)
	if len(session.turns) > scoutChatMaxHistoryTurns {
		session.turns = session.turns[len(session.turns)-scoutChatMaxHistoryTurns:]
	}
	session.mu.Unlock()

	session.sendEvent("answer", result.answer)
}

// scoutChatChannelModePrefixes maps the explicit launch prefixes users type in
// channels onto agent-thread modes. In a venture studio, "pitch", "brief", and
// "research" are everyday words, so unmentioned channel chatter never launches
// anything. Private threads have no keyword lane at all anymore — the
// propose-confirm router below handles conversational work asks with a card.
var scoutChatChannelModePrefixes = []struct {
	prefix string
	mode   string
}{
	{prefix: "grill:", mode: "grill"},
	{prefix: "research:", mode: "research"},
	{prefix: "design:", mode: "design"},
	{prefix: "workflow:", mode: "workflow"},
}

// scoutChatWorkstreamKeywords are the design workstreams a bare keyword can
// summon in a channel — but only alongside an explicit @scout mention (D5):
// the mention is itself the invocation, so the false-positive guard's purpose
// is preserved while "@scout research …" routes straight to the workstream.
var scoutChatWorkstreamKeywords = []string{"research", "design", "grill"}

// scoutChatThreadModeForChannelText launches a channel agent run on either
// (1) an explicit "mode:" prefix — standalone at the start of the message or
// immediately after an @scout mention — or (2) an @scout mention combined
// with a bare workstream keyword (research / design / grill). Bare keywords
// WITHOUT @scout never trigger anything.
func scoutChatThreadModeForChannelText(text string) string {
	lower := strings.ToLower(strings.Join(strings.Fields(text), " "))
	segments := strings.Split(lower, "@scout")
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		for _, candidate := range scoutChatChannelModePrefixes {
			if strings.HasPrefix(segment, candidate.prefix) {
				return candidate.mode
			}
		}
	}
	if len(segments) < 2 {
		// No @scout mention — a bare keyword stays conversation.
		return ""
	}
	tokens := strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '-'
	})
	for _, keyword := range scoutChatWorkstreamKeywords {
		for _, token := range tokens {
			if token == keyword {
				return keyword
			}
		}
	}
	return ""
}

// --- the propose-confirm router (packaging OS §2, Wave 2 item 8) -------------
//
// Typed Scout's routing brain: one Haiku function-calling turn per private
// thread message decides whether the ask is answerable inline (Tier 0, the
// heavily-biased default), worth PROPOSING as a quick single-shot workstream
// (Tier 1), or worth PROPOSING as a contract-gated goal pipeline run (Tier 2).
// A proposal is DATA on the reply — the confirmation card is the trust
// surface, and NOTHING launches until the user's explicit confirm posts the
// identical spec the palette Run button posts. This section replaced the
// keyword sniffing that lived here (scoutChatThreadModeForText): "what did we
// decide about the market?" used to silently launch a workstream — the only
// silent heavy invoke in the system, retired per spec.
//
// Keyless (no ANTHROPIC_API_KEY): no router turn at all — plain Q&A, never a
// proposal, never an error. A failed router turn degrades the same way.

const (
	// defaultRouterModel: routing is classification-shaped — strict tool use,
	// enum over the registry — so it rides Haiku, not the chat model (§1 role
	// matrix: "the enabling primitive for §2").
	defaultRouterModel = "claude-haiku-4-5"
	// scoutRouterMaxTokens bounds the routing turn: a proposal is one small
	// tool call, an inline verdict is no call at all.
	scoutRouterMaxTokens = 700

	scoutRouterProposalKindToolRun    = "tool_run"
	scoutRouterProposalKindWorkstream = "workstream"

	// scoutChatMessageKindProposal marks a persisted proposal card among the
	// existing "message"/"thread" message kinds.
	scoutChatMessageKindProposal = "proposal"

	// Weight labels — the card's honest cost line (§2: the card is also the
	// cost gate while concurrency limits are global).
	scoutProposalWeightGoalLoop  = "multi-agent goal loop, ~5-15 min"
	scoutProposalWeightQuickPass = "quick single pass"

	// Router signal events (§2 misfire economics: measure proposal-acceptance
	// from day one; below ~50%, tighten the trigger). Defined here beside the
	// router rather than in signals.go — the seam owns its event names, the
	// store just carries them.
	signalEventRouterProposalAccepted  = "router_proposal_accepted"
	signalEventRouterProposalDismissed = "router_proposal_dismissed"
)

// routerModel is the routing-turn dial, distinct from chatModel() and
// orchestratorModel().
func routerModel() string {
	return getenvDefault("BONFIRE_ROUTER_MODEL", defaultRouterModel)
}

// scoutRouterProposal is the wire/storage shape of one proposal card: enough
// data for the client to render the trust surface (tool + group, editable
// fields, target package, authority class, weight label) and for the confirm
// tap to post the identical spec the palette Run posts.
type scoutRouterProposal struct {
	Kind       string `json:"kind"` // tool_run | workstream
	ToolID     string `json:"toolId,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
	GroupLabel string `json:"groupLabel,omitempty"`
	Mode       string `json:"mode,omitempty"` // workstream proposals only
	Objective  string `json:"objective"`
	// Query is the user message that produced this proposal — the "just
	// answer instead" escape re-asks it as Tier 0.
	Query       string            `json:"query,omitempty"`
	PackageID   string            `json:"packageId,omitempty"`
	Fields      map[string]string `json:"fields,omitempty"`
	Authority   string            `json:"authority,omitempty"`
	WeightLabel string            `json:"weightLabel"`
	// Summary is the one legible sentence the card leads with.
	Summary string `json:"summary"`
	// Status flips to accepted/dismissed once the user acts, so a reloaded
	// thread renders the card inert instead of re-offering a spent launch.
	Status string `json:"status,omitempty"`
}

// scoutRouterSystemPrompt pins the three-tier policy. The trust asymmetry is
// deliberate and load-bearing: an agent that under-routes is trusted; one that
// over-launches is muted.
func scoutRouterSystemPrompt() string {
	return strings.Join([]string{
		"You are the routing brain for Scout's typed chat at Bonfire, a packaging studio.",
		"Classify the newest message into exactly one of three tiers.",
		"Tier 0 — answer inline: the heavily-biased default. Questions, recall, opinions, clarifications, and discussion are ALWAYS Tier 0 — 'what did we decide about the market?' is a question, not a research run. For Tier 0, call NO tool.",
		"Tier 1 — propose_workstream: a bounded 'go do one thing' ask (research / design / grill / workflow) that does not match a registry tool.",
		"Tier 2 — propose_tool_run: the ask matches a registry tool's contract — the user wants a deliverable someone will read (a brief, a one-pager, a scorecard, a memo).",
		"A proposal is only ever a suggestion card the user must confirm; you can never launch anything. Propose at most one thing.",
		"When in doubt, answer inline. An agent that under-routes is trusted; one that over-launches is muted.",
	}, "\n")
}

// scoutRouterTools builds the routing function schemas with names, promises,
// and enums INJECTED from the tool registry, so the registry stays the single
// taxonomy source (the typed twin of voice initiate_goal).
func scoutRouterTools() []anthropicTool {
	ids := make([]string, 0, 12)
	lines := make([]string, 0, 12)
	for _, group := range buildToolsPayload() {
		for _, tool := range group.Tools {
			ids = append(ids, tool.ID)
			lines = append(lines, fmt.Sprintf("%s (%s): %s", tool.ID, group.Label, tool.Promise))
		}
	}
	return []anthropicTool{
		{
			Name:        "propose_tool_run",
			Description: "Propose ONE registry tool run — a contract-gated multi-agent goal pipeline — for the user to confirm. Nothing launches without their tap. Tools:\n" + strings.Join(lines, "\n"),
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tool_id":    map[string]any{"type": "string", "enum": ids},
					"objective":  map[string]any{"type": "string", "description": "one sentence: what the run should produce, in the user's own terms"},
					"package_id": map[string]any{"type": "string", "description": "target venture package id if the conversation names one; else omit"},
					"fields":     map[string]any{"type": "object", "description": "pre-filled values for the tool's form fields, keyed by field key", "additionalProperties": map[string]any{"type": "string"}},
				},
				"required": []string{"tool_id", "objective"},
			},
		},
		{
			Name:        "propose_workstream",
			Description: "Propose a quick single-pass workstream (research / design / grill / workflow) for the user to confirm — a bounded 'go do one thing' ask that does not match a registry tool.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"mode":  map[string]any{"type": "string", "enum": []string{"research", "design", "grill", "workflow"}},
					"query": map[string]any{"type": "string", "description": "what the single pass should do"},
				},
				"required": []string{"mode", "query"},
			},
		},
	}
}

// scoutRouterInput folds the recent conversation plus the new message into the
// routing turn's single user block — enough context to tell a follow-up
// question from a fresh deliverable ask, bounded so routing stays cheap.
func scoutRouterInput(text string, history []scoutChatTurn) string {
	var builder strings.Builder
	if len(history) > 0 {
		builder.WriteString("# Conversation so far\n")
		start := 0
		if len(history) > 6 {
			start = len(history) - 6
		}
		for _, turn := range history[start:] {
			builder.WriteString(turn.role + ": " + truncateAgentThreadText(turn.text, 400) + "\n")
		}
		builder.WriteString("\n")
	}
	builder.WriteString("# New message\n" + text)
	return builder.String()
}

// routeScoutChatProposal runs the one routing turn and returns a proposal, or
// nil for Tier 0 (answer inline). nil is also every degraded path: keyless,
// router error, undecodable/unknown tool call — the caller falls through to
// the normal Q&A, so the router can only ever ADD a card, never break chat.
func (app *kanbanBoardApp) routeScoutChatProposal(ctx context.Context, text string, history []scoutChatTurn) *scoutRouterProposal {
	apiKey := currentAnthropicAPIKey()
	if apiKey == "" {
		return nil // keyless: plain Q&A — never a proposal, never an error
	}
	if ctx == nil {
		ctx = context.Background()
	}
	response, err := createAnthropicMessagesResponse(ctx, apiKey, anthropicMessagesRequest{
		Model:  routerModel(),
		System: scoutRouterSystemPrompt(),
		Messages: []anthropicMessage{{
			Role:    "user",
			Content: []json.RawMessage{anthropicTextBlock(scoutRouterInput(text, history))},
		}},
		Tools:     scoutRouterTools(),
		MaxTokens: scoutRouterMaxTokens,
		// No Effort on the routing turn: the default router model is
		// claude-haiku-4-5, and the API rejects output_config.effort on Haiku
		// 4.5 (supported on Sonnet 4.6+/Opus 4.5+ tiers only) — sending it
		// 400s EVERY routing turn and silently degrades all proposals to
		// inline answers. Empty Effort makes buildAnthropicMessagesPayload
		// omit output_config entirely.
		Effort: "",
	})
	if err != nil {
		log.Errorf("Scout router turn failed (degrading to inline answer): %v", err)
		return nil
	}
	for _, raw := range response.Content {
		block := decodeAnthropicBlock(raw)
		if block.Type != "tool_use" {
			continue
		}
		if proposal := scoutRouterProposalFromToolUse(block, text); proposal != nil {
			return proposal
		}
	}
	return nil
}

// scoutRouterProposalFromToolUse validates one routing tool call against the
// registry and builds the proposal card data. Anything malformed returns nil
// (inline answer) — a hallucinated tool id must never reach the client.
func scoutRouterProposalFromToolUse(block anthropicBlock, query string) *scoutRouterProposal {
	switch block.Name {
	case "propose_tool_run":
		args := struct {
			ToolID    string         `json:"tool_id"`
			Objective string         `json:"objective"`
			PackageID string         `json:"package_id"`
			Fields    map[string]any `json:"fields"`
		}{}
		if err := json.Unmarshal(block.Input, &args); err != nil {
			log.Errorf("Scout router propose_tool_run input undecodable: %v", err)
			return nil
		}
		tool, ok := toolByID(args.ToolID)
		if !ok {
			log.Errorf("Scout router proposed unknown tool %q", args.ToolID)
			return nil
		}
		objective := firstNonBlank(strings.TrimSpace(args.Objective), strings.TrimSpace(query))
		// Only field keys the registry declares survive — the card's inputs
		// render from the tool's own form definition.
		var fields map[string]string
		for _, field := range tool.FormFields {
			if value := strings.TrimSpace(asString(args.Fields[field.Key])); value != "" {
				if fields == nil {
					fields = map[string]string{}
				}
				fields[field.Key] = value
			}
		}
		return &scoutRouterProposal{
			Kind:        scoutRouterProposalKindToolRun,
			ToolID:      tool.ID,
			ToolName:    tool.Name,
			GroupLabel:  toolGroupLabels[tool.Group],
			Objective:   objective,
			Query:       strings.TrimSpace(query),
			PackageID:   strings.TrimSpace(args.PackageID),
			Fields:      fields,
			Authority:   tool.Authority,
			WeightLabel: scoutProposalWeightGoalLoop,
			Summary:     scoutRouterToolRunSummary(tool, objective),
		}
	case "propose_workstream":
		args := struct {
			Mode  string `json:"mode"`
			Query string `json:"query"`
		}{}
		if err := json.Unmarshal(block.Input, &args); err != nil {
			log.Errorf("Scout router propose_workstream input undecodable: %v", err)
			return nil
		}
		mode := strings.ToLower(strings.TrimSpace(args.Mode))
		switch mode {
		case "research", "design", "grill", "workflow":
		default:
			log.Errorf("Scout router proposed unknown workstream mode %q", args.Mode)
			return nil
		}
		objective := firstNonBlank(strings.TrimSpace(args.Query), strings.TrimSpace(query))
		return &scoutRouterProposal{
			Kind:        scoutRouterProposalKindWorkstream,
			Mode:        mode,
			Objective:   objective,
			Query:       strings.TrimSpace(query),
			WeightLabel: scoutProposalWeightQuickPass,
			Summary:     "this looks like a quick " + assistantToolLabel(mode) + " pass — confirm and it runs once: " + objective,
		}
	}
	return nil
}

// scoutRouterToolRunSummary is the card's one legible sentence: what runs,
// against what gate, with the kill condition named — the in-context tutorial
// for the tool.
func scoutRouterToolRunSummary(tool packagingTool, objective string) string {
	return "this is a " + tool.Name + " run — " + objective + ". gate: rubric-scored (" + tool.Rubric.Ref + "), kill condition: " + tool.KillCondition()
}

func (session *scoutChatSession) sendEvent(kind string, text string) {
	if session.send == nil {
		return
	}
	if err := session.send("scout_chat", map[string]any{
		"kind": kind,
		"text": text,
		"ts":   time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		log.Errorf("Failed to send scout chat event: %v", err)
	}
}
