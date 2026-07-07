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
	// scoutRouterProposalKindGoalRun is the free-form multi-step goal proposal
	// (card 088 propose_goal): a real build/ship OBJECTIVE that spans several
	// deliverables and matches NO single registry tool. Its confirm rides the
	// card's Run button through POST /assistant/goal with NO toolTemplate — the
	// typed twin of voice initiate_goal's free-form branch — so the goal engine
	// decomposes it into a gated loop. Signal-only on the accept route, exactly
	// like scoutRouterProposalKindToolRun.
	scoutRouterProposalKindGoalRun = "goal_run"
	// scoutRouterProposalKindImage is the single-shot concept-render proposal
	// (card 096): a direct gpt-image-2 call, NOT a contract-gated goal run, so
	// its confirm generates one image and files a design artifact rather than
	// launching the pipeline.
	scoutRouterProposalKindImage = "image"

	// scoutChatMessageKindProposal marks a persisted proposal card among the
	// existing "message"/"thread" message kinds.
	scoutChatMessageKindProposal = "proposal"

	// scoutChatMessageKindChoices marks a persisted quick-reply question: Scout
	// asked ONE clarifying question and offered 2-4 pill options (the dv-opts
	// dialogue design). Like a proposal, the card is DATA — tapping a pill sends
	// that text as the user's reply; a tool-armed pill only ARMS the proposal
	// card. NEVER a launch.
	scoutChatMessageKindChoices = "choices"

	// scoutChatMessageKindImage marks a persisted concept-render message (card
	// 096): the picture rides as DATA (scoutChatImageRef) that renders inline
	// via the session-gated /artifacts/blob route, beside its filed artifact.
	scoutChatMessageKindImage = "image"

	// Weight labels — the card's honest cost line (§2: the card is also the
	// cost gate while concurrency limits are global).
	scoutProposalWeightGoalLoop  = "multi-agent goal loop, ~5-15 min"
	scoutProposalWeightQuickPass = "quick single pass"
	// scoutProposalWeightImageRender is the concept-render card's cost line: one
	// gpt-image-2 call, back in under a minute.
	scoutProposalWeightImageRender = "one concept render, under a minute"

	// Router signal events (§2 misfire economics: measure proposal-acceptance
	// from day one; below ~50%, tighten the trigger). Defined here beside the
	// router rather than in signals.go — the seam owns its event names, the
	// store just carries them.
	signalEventRouterProposalAccepted  = "router_proposal_accepted"
	signalEventRouterProposalDismissed = "router_proposal_dismissed"
	// signalEventRouterChoiceSelected records one quick-reply pill tap — the
	// per-option acceptance signal that tells us which offered routes people
	// actually take.
	signalEventRouterChoiceSelected = "router_choice_selected"
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
	Query     string            `json:"query,omitempty"`
	PackageID string            `json:"packageId,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
	Authority string            `json:"authority,omitempty"`
	// Lane is the card's 069 governance lane (approval_lanes.go: auto | standard
	// | heavy), classified from the same dimensions the ship gates enforce.
	// Scout-proposed work is system-proposed, so a card is never "auto" — it is
	// the one-member confirm the standard lane requires, and external_write work
	// classifies heavy. Carried as DATA so the honest approval caption renders on
	// the card and the accept/dismiss signal is measurable per lane (card 088
	// Slice A — the 067 ticker reads this same field to know what auto-approves).
	Lane        string `json:"lane,omitempty"`
	WeightLabel string `json:"weightLabel"`
	// Summary is the one legible sentence the card leads with.
	Summary string `json:"summary"`
	// Status flips to accepted/dismissed once the user acts, so a reloaded
	// thread renders the card inert instead of re-offering a spent launch.
	Status string `json:"status,omitempty"`
}

// scoutChatChoiceOption is one quick-reply pill. Label is what the pill shows;
// Reply is the text sent as the user's message when tapped (defaults to Label);
// ToolID, when set, arms that registry tool/process as a deterministic proposal
// card on the reply — the propose-confirm law's trust surface, never a launch.
type scoutChatChoiceOption struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Reply  string `json:"reply,omitempty"`
	ToolID string `json:"toolId,omitempty"`
}

// scoutChatChoices is the wire/storage shape of one quick-reply question card
// (Kind "choices"): the one-sentence question plus 2-4 pill options. Query is
// the user message that produced the question — it becomes the proposal's
// Tier-0 escape query and the objective fallback. Status flips to "answered"
// (with SelectedID) on the first tap, so a reloaded thread renders the card
// inert instead of re-offering spent pills.
type scoutChatChoices struct {
	Question   string                  `json:"question"`
	Options    []scoutChatChoiceOption `json:"options"`
	Query      string                  `json:"query,omitempty"`
	Status     string                  `json:"status,omitempty"`
	SelectedID string                  `json:"selectedId,omitempty"`
}

// scoutRouterVerdict is one routing turn's outcome beyond Tier 0: exactly one
// of proposal (Tier 1/2) or choices (the clarifying question) is set. A nil
// verdict is Tier 0 — answer inline.
type scoutRouterVerdict struct {
	proposal *scoutRouterProposal
	choices  *scoutChatChoices
}

// scoutRouterSystemPrompt pins the three-tier policy. The trust asymmetry is
// deliberate and load-bearing: an agent that under-routes is trusted; one that
// over-launches is muted.
func scoutRouterSystemPrompt() string {
	lines := []string{
		"You are the routing brain for Scout's typed chat at Bonfire, a packaging studio.",
		"Classify the newest message into exactly one of three tiers.",
		"Tier 0 — answer inline: the heavily-biased default. Questions, recall, opinions, clarifications, and discussion are ALWAYS Tier 0 — 'what did we decide about the market?' is a question, not a research run. For Tier 0, call NO tool.",
		"Tier 1 — propose_workstream: a bounded 'go do one thing' ask (research / design / grill / workflow) that does not match a registry tool.",
		"Tier 2 — propose_tool_run: the ask matches a registry tool's contract — the user wants a deliverable someone will read (a brief, a one-pager, a scorecard, a memo).",
		"Free-form goal — propose_goal: a real multi-step build/ship OBJECTIVE that spans SEVERAL deliverables and matches NO single registry tool ('package the Aurora IP into a one-pager AND a deck', 'take this from raw idea to a shipped pitch as one goal'). Scout decomposes it into a gated loop. A single deliverable that maps to a tool stays propose_tool_run; a full end-to-end packaging run stays packaging_studio.",
		"Ambiguous work — offer_choices: the ask is clearly work but the route is genuinely ambiguous between 2-4 concrete options, or one decisive input is missing. Ask ONE short question and offer 2-4 quick-reply options (pill labels under ~6 words); set tool_id on any option that maps to a registry tool or process. Never offer choices when one route is obvious — propose it.",
		"Intent map — route these confidently:",
		"- pitch outline work ('work on the pitch outline', 'outline the deck', 'sequence the narrative slide by slide') -> propose_tool_run deck_outline.",
		"- design identity ('develop a design identity', 'brand direction', 'look and feel', 'visual system') -> propose_tool_run brand_design_brief.",
		"- a deck built from an existing outline ('build the deck from the outline we have') -> propose_tool_run packaging_studio with the objective naming that outline as the spine; if it is unclear whether they want outline work or the built deck, offer_choices between deck_outline and packaging_studio.",
		"- full end-to-end packaging ('package this end to end', 'the full packaging run', 'take it from 0 to 100') -> propose_tool_run packaging_studio.",
		"- package_assembly is ONLY 'compile the artifacts we already made into the send-ready binder'; any end-to-end / full-run / from-scratch language is packaging_studio, even when the thread was already discussing an existing package; genuinely torn between the two -> offer_choices ('compile what we have' [package_assembly] / 'the full staged run' [packaging_studio]).",
		"- economics / business model / unit economics / projections / 'does the deal work' -> propose_tool_run economics_waterfall.",
		"- ground truth / market digging -> deep_research; what-it-sold-for / pricing -> comps_precedent; landscape / whitespace -> market_map; hostile-room prep ('grill it', 'pressure test it') -> grill_pressure_test; who to attach -> talent_match.",
	}
	// The single-image door only appears when generation is actually configured
	// (a keyless-OpenAI deploy must never be told to propose a render it cannot
	// produce). The matching propose_image tool is gated the same way.
	if openAIImageGenerationAvailable() {
		lines = append(lines,
			"- make / generate / draw / create an image, picture, poster, logo, or illustration of X -> propose_image with a prompt describing X; this is one direct render, not a research run.",
		)
	}
	lines = append(lines,
		"When the user corrects a prior proposal or answer by naming a different tool or process ('no, the full Packaging Studio staged run'), the correction IS the work ask — propose that named id confidently; a correction is never Tier 0, re-route it.",
		"A proposal or a question card is only ever a suggestion the user must act on; you can never launch anything. Propose at most one thing.",
		"When in doubt, answer inline. An agent that under-routes is trusted; one that over-launches is muted.",
	)
	return strings.Join(lines, "\n")
}

// scoutRouterTools builds the routing function schemas with names, promises,
// and enums INJECTED from the tool registry, so the registry stays the single
// taxonomy source (the typed twin of voice initiate_goal).
// packagingRunPresetIDs is the flat list of launchable run-type ids from the
// single taxonomy (buildToolsPayload) — registry tools plus non-hidden
// processes. The voice initiate_goal 'tool' preset enumerates these so voice
// can pick a real run-type the same way the typed router (scoutRouterTools)
// does, instead of guessing from a short prose list of examples.
func packagingRunPresetIDs() []string {
	ids := make([]string, 0, 16)
	for _, group := range buildToolsPayload() {
		for _, tool := range group.Tools {
			ids = append(ids, tool.ID)
		}
	}
	return ids
}

func scoutRouterTools() []anthropicTool {
	ids := make([]string, 0, 12)
	lines := make([]string, 0, 12)
	for _, group := range buildToolsPayload() {
		for _, tool := range group.Tools {
			ids = append(ids, tool.ID)
			lines = append(lines, fmt.Sprintf("%s (%s): %s", tool.ID, group.Label, tool.Promise))
		}
	}
	tools := []anthropicTool{
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
		{
			Name:        "offer_choices",
			Description: "Ask ONE short clarifying question with 2-4 quick-reply pill options when the ask is work but the route is genuinely ambiguous. An option with a tool_id arms that tool's confirmation card when tapped — nothing launches without the user's explicit confirm.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"question": map[string]any{"type": "string", "description": "one sentence, ending in the question"},
					"options": map[string]any{
						"type":     "array",
						"minItems": 2,
						"maxItems": 4,
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"label":   map[string]any{"type": "string", "description": "the pill text, under ~6 words"},
								"reply":   map[string]any{"type": "string", "description": "the full reply sent when tapped; defaults to the label"},
								"tool_id": map[string]any{"type": "string", "enum": ids, "description": "registry tool/process this option arms; omit for a plain reply"},
							},
							"required": []string{"label"},
						},
					},
				},
				"required": []string{"question", "options"},
			},
		},
		{
			Name:        "propose_goal",
			Description: "Propose a free-form multi-step GOAL run for the user to confirm — a real build/ship objective that spans several deliverables and matches NO single registry tool (e.g. 'package the Aurora IP into a one-pager AND a deck', 'take this from raw idea to a shipped pitch as one goal'). Scout decomposes it into a gated goal loop. This is the typed twin of the voice initiate_goal free-form branch; nothing launches without the user's tap.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"objective":  map[string]any{"type": "string", "description": "the end-to-end goal in the user's own words: what the run should ultimately produce"},
					"package_id": map[string]any{"type": "string", "description": "target venture package id if the conversation names one; else omit"},
					"authority_hint": map[string]any{
						"type":        "string",
						"description": "read_only for research/analysis goals; workspace_write when the goal produces or edits work. external_write is never available here — it is earned only at the ship gate with human approval.",
						"enum":        []string{toolAuthorityReadOnly, toolAuthorityWorkspaceWrite},
					},
				},
				"required": []string{"objective"},
			},
		},
	}
	// The concept-render door (card 096): a single gpt-image-2 call, offered
	// only when OpenAI image generation is configured so a keyless-OpenAI deploy
	// never proposes a render it cannot produce. Appended LAST so the three
	// text-route tools keep their pinned enum positions.
	if openAIImageGenerationAvailable() {
		tools = append(tools, anthropicTool{
			Name:        "propose_image",
			Description: "Propose generating ONE image — a concept render — for the user to confirm. Use when the user asks to make / generate / draw / create a picture, image, poster, logo, or illustration. This is a single direct render, not a contract-gated run; nothing generates without the user's tap.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string", "description": "the image prompt: what to depict, in vivid concrete terms and the user's own subject"},
					"title":  map[string]any{"type": "string", "description": "a short title for the filed artifact; optional"},
				},
				"required": []string{"prompt"},
			},
		})
	}
	return tools
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

// scoutRouterFullRunPhrases is the reviewed, capped phrase list the
// deterministic pre-router guard matches to the flagship end-to-end run
// (packaging_studio) — the literal words that named the full run in the
// 2026-07-05 sim and still lost to thread-context gravity inside the Haiku turn.
// Capped and code-reviewed on purpose (the analysis doc's keyword-sniffing
// tripwire): "package" ALONE never appears here — only unambiguous full-run
// phrases — and a match may only ever PROPOSE a card, never launch.
var scoutRouterFullRunPhrases = []string{
	"end to end",
	"end-to-end",
	"the full run",
	"full packaging run",
	"0 to 100",
	"zero to 100",
	"packaging studio",
}

// scoutRouterImagePhrases is the reviewed, capped phrase list the deterministic
// pre-router guard matches to the single-shot concept render (card 096 — the
// fix for AJ's "image request failed" complaint: the literal ask can never be
// dragged off-route by the Haiku turn). Capped and code-reviewed like the
// full-run list: only unambiguous "make a picture/image" imperatives, and a
// match may only ever PROPOSE the concept-render card, never generate.
var scoutRouterImagePhrases = []string{
	"make an image",
	"make me an image",
	"generate an image",
	"create an image",
	"draw an image",
	"make a picture",
	"make me a picture",
	"generate a picture",
	"create a picture",
}

// scoutGuardEligibleMessage returns true when a message is work-shaped enough
// for the deterministic guard to arm a proposal: not a question (a question
// defers to the answer brain, which now carries the capabilities digest +
// offer-never-deny) and not an action-negating message. A BARE leading "no" is
// deliberately NOT a skip — "no, the full Packaging Studio staged run" is a
// correction toward MORE work, and the design's correction rule wants it armed;
// only tokens that negate the action itself ("don't", "no need", "instead of")
// skip the guard.
func scoutGuardEligibleMessage(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return false
	}
	if strings.HasSuffix(t, "?") {
		return false
	}
	// Statement-form questions and explanatory lead-ins carry no trailing "?" but
	// are still informational asks ("what is packaging studio", "explain the
	// packaging studio", "tell me about deep research"). Defer them to the answer
	// brain (which now carries the capabilities digest + offer-never-deny) rather
	// than let the guard arm a Run card off the flagship phrase list. Imperative
	// work asks ("package this end to end", "run the deck outline") do not lead
	// with these tokens and stay armed. "do" is deliberately EXCLUDED: it is the
	// one auxiliary that is also a work imperative ("do a deep research pass"), so
	// bare "do" stays armed; its question form is caught by the "do you"/"do we"
	// prefixes below.
	if strings.HasPrefix(t, "tell me") {
		return false
	}
	if fields := strings.Fields(t); len(fields) > 0 {
		switch strings.Trim(fields[0], ".,!:;\"'") {
		case "what", "whats", "what's", "which", "who", "whom", "whose",
			"when", "where", "why", "how",
			"is", "are", "was", "were", "does", "did",
			"explain", "describe":
			return false
		}
	}
	for _, q := range []string{"can you", "could you", "can we", "do you", "do we", "would you", "are you able", "is there a way", "what can you", "what do you", "how do i", "how do we", "how can i"} {
		if strings.HasPrefix(t, q) {
			return false
		}
	}
	for _, n := range []string{"don't", "do not", "dont", "no need", "not now", "not yet", "never mind", "nevermind", "instead of", "rather than", "without ", "won't", "wont", "skip the", "hold off"} {
		if strings.Contains(t, n) {
			return false
		}
	}
	return true
}

// deterministicRouterGuard commits a proposal card BEFORE the Haiku turn when a
// work-shaped, non-negated message contains either a reviewed full-run phrase
// (-> the flagship packaging_studio) or an exact registry tool/process name
// (-> that capability). This is the flagship's second guarantee (item 3): the
// literal words can never again be dragged off-target by the 6-turn context
// fold inside scoutRouterInput. Propose-only — it returns the same
// scoutRouterProposalForToolID shape a pill arms, and the card's Run stays the
// only launch door. nil when nothing matches, so the model turn still runs.
func deterministicRouterGuard(text string) *scoutRouterVerdict {
	if !scoutGuardEligibleMessage(text) {
		return nil
	}
	lower := strings.ToLower(text)
	// Image asks route to the single-shot concept render BEFORE the model turn,
	// but only when generation is actually configured (a keyless-OpenAI deploy
	// can never generate, so it must never offer the card). Propose-only — the
	// card's Run stays the only door.
	if openAIImageGenerationAvailable() {
		for _, phrase := range scoutRouterImagePhrases {
			if strings.Contains(lower, phrase) {
				if proposal := scoutRouterImageProposal(text, text); proposal != nil {
					return &scoutRouterVerdict{proposal: proposal}
				}
			}
		}
	}
	// Full-run phrases are checked FIRST so end-to-end language always wins the
	// flagship, even mid-thread about an existing package (the sim miss:
	// package_assembly stole the verdict).
	for _, phrase := range scoutRouterFullRunPhrases {
		if strings.Contains(lower, phrase) {
			if proposal := scoutRouterProposalForToolID(packagingStudioProcessID, "", text); proposal != nil {
				return &scoutRouterVerdict{proposal: proposal}
			}
		}
	}
	// Exact registry tool/process names, straight from the single taxonomy
	// source. Short names are skipped to keep casual prose from tripping a card;
	// names with punctuation ("Grill / Pressure-Test") only ever match a verbatim
	// type-out, which is exactly the deterministic-intent signal we want.
	for _, group := range buildToolsPayload() {
		for _, tool := range group.Tools {
			name := strings.ToLower(strings.TrimSpace(tool.Name))
			if len(name) < 6 {
				continue
			}
			if strings.Contains(lower, name) {
				if proposal := scoutRouterProposalForToolID(tool.ID, "", text); proposal != nil {
					return &scoutRouterVerdict{proposal: proposal}
				}
			}
		}
	}
	return nil
}

// routeScoutChatTurn runs the one routing turn and returns a verdict — a
// proposal card, a quick-reply question card — or nil for Tier 0 (answer
// inline). nil is also every degraded path: keyless, router error,
// undecodable/unknown tool call — the caller falls through to the normal Q&A,
// so the router can only ever ADD a card, never break chat.
func (app *kanbanBoardApp) routeScoutChatTurn(ctx context.Context, text string, history []scoutChatTurn) *scoutRouterVerdict {
	apiKey := currentAnthropicAPIKey()
	if apiKey == "" {
		return nil // keyless: plain Q&A — never a proposal, never an error
	}
	// Deterministic pre-router guard: exact registry names + the reviewed
	// full-run phrase list commit the matching proposal BEFORE the model turn,
	// so thread-context gravity can never drag the literal words off the flagship
	// again. Propose-only, never a launch (see deterministicRouterGuard).
	if verdict := deterministicRouterGuard(text); verdict != nil {
		return verdict
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
		if block.Name == "offer_choices" {
			if choices := scoutChatChoicesFromToolUse(block, text); choices != nil {
				return &scoutRouterVerdict{choices: choices}
			}
			continue
		}
		if proposal := scoutRouterProposalFromToolUse(block, text); proposal != nil {
			return &scoutRouterVerdict{proposal: proposal}
		}
	}
	return nil
}

// routerToolByID resolves a proposable id against the same set scoutRouterTools
// injects into the enum: the 12 registry tools plus non-hidden processes mapped
// onto the tool shape (processPaletteEntry). Hidden processes stay unproposable
// even if the model hallucinates their id. Without the process branch, the
// router could name packaging_studio (it is in the enum via the fifth payload
// group) and validation would silently drop the proposal.
func routerToolByID(id string) (packagingTool, bool) {
	if tool, ok := toolByID(id); ok {
		return tool, true
	}
	if def, ok := processByID(strings.TrimSpace(strings.ToLower(id))); ok && !def.Hidden {
		return processPaletteEntry(def), true
	}
	return packagingTool{}, false
}

// scoutRouterProposalForToolID builds the deterministic tool-run proposal card
// for one registry tool or process — shared by the router's propose_tool_run
// validation and the quick-reply pill arm (a tool-armed pill commits exactly
// this card; the card's Run button stays the only launch door).
func scoutRouterProposalForToolID(toolID string, objective string, query string) *scoutRouterProposal {
	tool, ok := routerToolByID(toolID)
	if !ok {
		return nil
	}
	objective = firstNonEmptyString(strings.TrimSpace(objective), strings.TrimSpace(query), tool.Name)
	return &scoutRouterProposal{
		Kind:        scoutRouterProposalKindToolRun,
		ToolID:      tool.ID,
		ToolName:    tool.Name,
		GroupLabel:  toolGroupLabels[tool.Group],
		Objective:   objective,
		Query:       strings.TrimSpace(query),
		Authority:   tool.Authority,
		Lane:        scoutProposalLane("goal", tool.ID, tool.Authority),
		WeightLabel: scoutProposalWeightGoalLoop,
		Summary:     scoutRouterToolRunSummary(tool, objective),
	}
}

// scoutProposalLane classifies a proposal card into its 069 governance lane
// (approval_lanes.go). Every router proposal is SYSTEM-proposed — Scout wrote
// it, the card is the trust surface that collects the human confirm — so
// systemProposed is always true here: approvalLaneFor never returns "auto" for
// a card (the confirm IS the standard lane's one-member approval), and
// external_write work classifies heavy. This is the single seam that keeps the
// card's lane in lockstep with what the ship gates actually enforce.
func scoutProposalLane(mode string, toolTemplate string, authority string) string {
	return approvalLaneFor(mode, toolTemplate, authority, true)
}

// scoutChatChoicesFromToolUse validates one offer_choices call: a non-empty
// question and 2-4 usable options. An option with an unknown tool_id keeps its
// label as a plain reply pill (the arm is dropped, the conversation survives);
// fewer than 2 usable options degrades to nil — an inline answer, never an
// error.
func scoutChatChoicesFromToolUse(block anthropicBlock, query string) *scoutChatChoices {
	args := struct {
		Question string `json:"question"`
		Options  []struct {
			Label  string `json:"label"`
			Reply  string `json:"reply"`
			ToolID string `json:"tool_id"`
		} `json:"options"`
	}{}
	if err := json.Unmarshal(block.Input, &args); err != nil {
		log.Errorf("Scout router offer_choices input undecodable: %v", err)
		return nil
	}
	question := trimForStorage(args.Question, 240)
	if question == "" {
		return nil
	}
	options := make([]scoutChatChoiceOption, 0, 4)
	for _, raw := range args.Options {
		label := trimForStorage(raw.Label, 80)
		if label == "" {
			continue
		}
		toolID := ""
		if wanted := strings.TrimSpace(raw.ToolID); wanted != "" {
			if tool, ok := routerToolByID(wanted); ok {
				toolID = tool.ID
			} else {
				log.Errorf("Scout router offered unknown tool %q on a pill — keeping the plain reply", wanted)
			}
		}
		options = append(options, scoutChatChoiceOption{
			ID:     fmt.Sprintf("opt-%d", len(options)+1),
			Label:  label,
			Reply:  trimForStorage(raw.Reply, 400),
			ToolID: toolID,
		})
		if len(options) == 4 {
			break
		}
	}
	if len(options) < 2 {
		return nil
	}
	return &scoutChatChoices{
		Question: question,
		Options:  options,
		Query:    strings.TrimSpace(query),
	}
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
		tool, ok := routerToolByID(args.ToolID)
		if !ok {
			log.Errorf("Scout router proposed unknown tool %q", args.ToolID)
			return nil
		}
		proposal := scoutRouterProposalForToolID(tool.ID, args.Objective, query)
		if proposal == nil {
			return nil
		}
		proposal.PackageID = strings.TrimSpace(args.PackageID)
		// Only field keys the registry declares survive — the card's inputs
		// render from the tool's own form definition.
		for _, field := range tool.FormFields {
			if value := strings.TrimSpace(asString(args.Fields[field.Key])); value != "" {
				if proposal.Fields == nil {
					proposal.Fields = map[string]string{}
				}
				proposal.Fields[field.Key] = value
			}
		}
		return proposal
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
			Lane:        scoutProposalLane(mode, "", ""),
			WeightLabel: scoutProposalWeightQuickPass,
			Summary:     "this looks like a quick " + assistantToolLabel(mode) + " pass — confirm and it runs once: " + objective,
		}
	case "propose_goal":
		args := struct {
			Objective     string `json:"objective"`
			AuthorityHint string `json:"authority_hint"`
			PackageID     string `json:"package_id"`
		}{}
		if err := json.Unmarshal(block.Input, &args); err != nil {
			log.Errorf("Scout router propose_goal input undecodable: %v", err)
			return nil
		}
		return scoutRouterGoalProposal(firstNonBlank(strings.TrimSpace(args.Objective), strings.TrimSpace(query)), args.AuthorityHint, strings.TrimSpace(args.PackageID), query)
	case "propose_image":
		args := struct {
			Prompt string `json:"prompt"`
			Title  string `json:"title"`
		}{}
		if err := json.Unmarshal(block.Input, &args); err != nil {
			log.Errorf("Scout router propose_image input undecodable: %v", err)
			return nil
		}
		return scoutRouterImageProposal(firstNonBlank(strings.TrimSpace(args.Prompt), strings.TrimSpace(query)), query)
	}
	return nil
}

// scoutRouterImageProposal builds the single-shot concept-render proposal card
// (card 096): the editable prompt is the objective, the originating ask stays
// the Tier-0 escape query, and the authority is a plain workspace write (a
// generated image files to the design library, nothing external). Shared by the
// deterministic guard and the propose_image validation so both arm the same
// card the confirm resolves.
func scoutRouterImageProposal(prompt string, query string) *scoutRouterProposal {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = strings.TrimSpace(query)
	}
	if prompt == "" {
		return nil
	}
	return &scoutRouterProposal{
		Kind:        scoutRouterProposalKindImage,
		Objective:   prompt,
		Query:       strings.TrimSpace(query),
		Authority:   toolAuthorityWorkspaceWrite,
		Lane:        scoutProposalLane("", "", toolAuthorityWorkspaceWrite),
		WeightLabel: scoutProposalWeightImageRender,
		Summary:     scoutRouterImageSummary(prompt),
	}
}

// scoutRouterGoalProposal builds the free-form multi-step goal proposal card
// (card 088 propose_goal): the editable objective drives a plain goal-engine
// run (no toolTemplate), the authority is clamped exactly like voice
// initiate_goal and assistantGoalHandler — read_only or workspace_write, NEVER
// external_write (that is earned only at the ship gate with human approval) —
// and the originating ask stays the Tier-0 escape query. Shared by the
// propose_goal validation branch; the card's Run posts POST /assistant/goal.
func scoutRouterGoalProposal(objective string, authorityHint string, packageID string, query string) *scoutRouterProposal {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return nil
	}
	authority := toolAuthorityWorkspaceWrite
	if strings.EqualFold(strings.TrimSpace(authorityHint), toolAuthorityReadOnly) {
		authority = toolAuthorityReadOnly
	}
	return &scoutRouterProposal{
		Kind:        scoutRouterProposalKindGoalRun,
		Objective:   objective,
		Query:       strings.TrimSpace(query),
		PackageID:   strings.TrimSpace(packageID),
		Authority:   authority,
		Lane:        scoutProposalLane("goal", "", authority),
		WeightLabel: scoutProposalWeightGoalLoop,
		Summary:     scoutRouterGoalRunSummary(objective),
	}
}

// scoutRouterGoalRunSummary is the free-form goal card's one legible sentence:
// the multi-step loop it launches (decompose -> run subtasks -> review against
// the goal -> gate -> report), the human-checkpoint law, and the honest cost
// gate (one explicit tap).
func scoutRouterGoalRunSummary(objective string) string {
	objective = strings.TrimRight(strings.TrimSpace(objective), ".")
	return "this launches the multi-step goal loop — " + objective + ". Scout decomposes it, runs the subtasks, reviews against the goal, and gates before anything ships; nothing runs until you tap Run."
}

// scoutRouterImageSummary is the concept-render card's one legible sentence:
// what runs (one gpt-image-2 render), where it lands (the design library), and
// the honest cost gate (a single explicit tap).
func scoutRouterImageSummary(prompt string) string {
	prompt = strings.TrimRight(strings.TrimSpace(prompt), ".")
	return "this generates one concept render — " + prompt + ". a single image on the OpenAI images API; nothing else runs, and it files to the design library when it lands."
}

// scoutRouterToolRunSummary is the card's one legible sentence: what runs,
// against what gate, with the kill condition named — the in-context tutorial
// for the tool. Processes carry no single rubric (each stage gates itself), so
// their sentence names the checkpoint law instead.
func scoutRouterToolRunSummary(tool packagingTool, objective string) string {
	// The router-authored objective usually ends in "." — joining it before
	// ". gate:…" / ". it parks…" ships a double period the reader sees.
	objective = strings.TrimRight(strings.TrimSpace(objective), ".")
	if tool.Group == toolGroupProcesses {
		return "this is the " + tool.Name + " staged process — " + objective + ". it parks at each human checkpoint; nothing ships without your approval."
	}
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
