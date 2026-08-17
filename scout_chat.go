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
	mu             sync.Mutex
	send           func(event string, data any) error
	turns          []scoutChatTurn
	queue          chan string
	ctx            context.Context
	cancel         context.CancelFunc
	workerOnce     sync.Once
	requesterEmail string
	principal      RecallPrincipal
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

	principal := session.principal
	if principal.User == nil && principal.Audience == "" {
		principal = recallPrincipalForEmail(session.requesterEmail)
	}
	result, err := app.resolveAssistantQueryContextForPrincipalWithAttachments(session.ctx, principal, session.requesterEmail, text, history, nil)
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

// scoutChatChannelModePrefixes maps explicit routing prefixes users type in
// channels onto proposed agent-thread modes. In a venture studio, "pitch",
// "brief", and "research" are everyday words, so unmentioned channel chatter
// never proposes anything. Private threads have no keyword lane at all — the
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

// scoutChatThreadModeForChannelText is the narrow compatibility guard for
// public-channel work cards. It accepts an explicit mode prefix or an
// unmistakable @Scout action request, but never routes on a topic word alone.
// Negation wins so “do not start research; just talk” cannot become a card.
func scoutChatThreadModeForChannelText(text string) string {
	lower := strings.ToLower(strings.Join(strings.Fields(text), " "))
	segments := strings.Split(lower, "@scout")
	for _, negation := range []string{"do not ", "don't ", "dont ", "not start ", "not run ", "without starting ", "without running ", "just talk", "only talk"} {
		if strings.Contains(lower, negation) {
			return ""
		}
	}
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		for _, candidate := range scoutChatChannelModePrefixes {
			if strings.HasPrefix(segment, candidate.prefix) {
				return candidate.mode
			}
		}
	}
	if len(segments) < 2 {
		return ""
	}
	requests := []struct {
		mode    string
		phrases []string
	}{
		{mode: "research", phrases: []string{"research the ", "research this ", "research report", "run research", "start research", "do research", "investigate the ", "dig into the ", "look into the "}},
		{mode: "design", phrases: []string{"design the ", "design this ", "create a design", "make a design", "mock up ", "mockup "}},
		{mode: "grill", phrases: []string{"grill the ", "grill this ", "pressure-test the ", "pressure test the "}},
	}
	for _, request := range requests {
		for _, phrase := range request.phrases {
			if strings.Contains(lower, phrase) {
				return request.mode
			}
		}
	}
	return ""
}

// --- the propose-confirm router (packaging OS §2, Wave 2 item 8) -------------
//
// Typed Scout's routing brain: one strict Responses turn per private
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
// Keyless (no OPENAI_API_KEY): no router turn at all — plain Q&A, never a
// proposal, never an error. A failed router turn degrades the same way.

const (
	// scoutRouterMaxTokens bounds the strict routing object. This is generous
	// enough for a four-choice clarification or pre-filled registry fields while
	// remaining a small Luna turn.
	scoutRouterMaxTokens = 1200

	scoutRouterProposalKindToolRun      = "tool_run"
	scoutRouterProposalKindWorkstream   = "workstream"
	scoutRouterProposalKindNativeAction = "native_action"
	// scoutRouterProposalKindGoalRun is the free-form multi-step goal proposal
	// (card 088 propose_goal): a real build/ship OBJECTIVE that spans several
	// deliverables and matches NO single registry tool. Its confirm rides the
	// card's Run button through POST /assistant/goal with NO toolTemplate — the
	// typed twin of voice initiate_goal's free-form branch — so the goal engine
	// decomposes it into a gated loop. Signal-only on the accept route, exactly
	// like scoutRouterProposalKindToolRun.
	scoutRouterProposalKindGoalRun = "goal_run"
	// scoutRouterProposalKindImage is the single-shot concept-render route. It
	// remains a proposal-shaped internal router result so the model can turn the
	// user's intent into a production-ready image prompt; private/public chat
	// execution consumes it immediately rather than persisting a confirmation
	// card.
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
	// scoutChatMessageKindImagePending is the transient-but-durable feed pill
	// shown while gpt-image-2 is running. The prompt is persisted server-side for
	// crash-safe handoff but redacted from viewer projections until an image lands.
	scoutChatMessageKindImagePending = "image_pending"

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

	// Historical router verdicts remain readable for old ledger rows. New
	// accepted turns record one of the exact five conversationIntentOutcome
	// values; confirm/dismiss events remain resolution events, not turn outcomes.
	routerVerdictProposedTool       = "proposed_tool"
	routerVerdictChoicePills        = "choice_pills"
	routerVerdictNativeAction       = "native_action"
	routerVerdictInline             = "inline"
	routerVerdictDeterministicGuard = "deterministic_guard"
	routerVerdictConfirmed          = "confirmed"
	routerVerdictDismissed          = "dismissed"
)

// isRouterRoutingVerdict reports whether a router_outcome verdict names a
// routing TURN — the denominator for the truncation rate. The confirm/dismiss
// pair rides the same evalKindRouterOutcome kind but is stamped at resolve
// time, not a turn, so it must never inflate "turns".
func isRouterRoutingVerdict(verdict string) bool {
	switch verdict {
	case routerVerdictProposedTool, routerVerdictChoicePills, routerVerdictNativeAction, routerVerdictInline, routerVerdictDeterministicGuard,
		string(conversationIntentConversationalReply), string(conversationIntentClarifyOnce), string(conversationIntentStartPrivateWork), string(conversationIntentApprovalRequired), string(conversationIntentUnavailable):
		return true
	default:
		return false
	}
}

// routerModel remains the common telemetry/test accessor for this seat. The
// route itself is OpenAI-only; an Anthropic key cannot select it.
func routerModel() string {
	return scoutRouterModel()
}

// routerEffort is retained for older evaluation call sites. The OpenAI route
// and its action-classification twin share Scout's explicit reasoning dial.
func routerEffort() string {
	return scoutRouterReasoningEffort()
}

// scoutRouterProposal is the wire/storage shape of one proposal card: enough
// data for the client to render the trust surface (tool + group, editable
// fields, target package, authority class, weight label) and for the confirm
// tap to post the identical spec the palette Run posts.
type scoutRouterProposal struct {
	Kind string `json:"kind"` // native_action | tool_run | workstream | goal_run
	// IntentOutcome distinguishes a consequential approval card from historical
	// propose-confirm compatibility records. The server stamps it; clients may
	// resolve the stored card by id but cannot supply or alter the held work.
	IntentOutcome string `json:"intentOutcome,omitempty"`
	EffectClass   string `json:"effectClass,omitempty"`
	ToolID        string `json:"toolId,omitempty"`
	ToolName      string `json:"toolName,omitempty"`
	GroupLabel    string `json:"groupLabel,omitempty"`
	Mode          string `json:"mode,omitempty"` // workstream proposals only
	// AgentID/AgentName bind a targeted @Agent work request to the exact hired
	// seat selected when the proposal was minted. The confirm route trusts the
	// persisted id, never client-supplied identity, and reauthorizes it before
	// launching the bounded runner.
	AgentID   string `json:"agentId,omitempty"`
	AgentName string `json:"agentName,omitempty"`
	Objective string `json:"objective"`
	// Query is the user message that produced this proposal — the "just
	// answer instead" escape re-asks it as Tier 0.
	Query string `json:"query,omitempty"`
	// ContextRefs are server-resolved, ACL-checked Files/chat-file identities.
	// They let an approved worker re-read the exact source that earned the
	// proposal instead of hoping a fuzzy memory search finds the same filename.
	// The accept route trusts only this persisted proposal value.
	ContextRefs string            `json:"contextRefs,omitempty"`
	PackageID   string            `json:"packageId,omitempty"`
	Fields      map[string]string `json:"fields,omitempty"`
	Authority   string            `json:"authority,omitempty"`
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
	// directionPass is a prose clarifying question about work direction
	// (Approach B). Unlike choices, it has no formal options — the user
	// responds conversationally. Results in Kind=message with
	// IntentOutcome=clarify_once.
	directionPass string
	// action is a native, principal-bound Stride operation. It executes before
	// any work proposal is considered and can never launch an agent thread.
	action *scoutNativeAction
	// source is the provenance stamp (usage_ledger.go proposalSource*
	// constant) the chat thread's proposal_minted event records:
	// deterministic_guard when the pre-router guard committed the card,
	// chat_router for a model-routed verdict (W0 item 7 lineage).
	source string
}

// scoutRouterSystemPrompt pins the three-tier policy. The trust asymmetry is
// deliberate and load-bearing: an agent that under-routes is trusted; one that
// over-launches is muted.
func scoutRouterSystemPrompt() string {
	lines := []string{
		"You are the routing brain for Scout's typed chat at Bonfire, a packaging studio.",
		"Classify the newest message into exactly one route.",
		"Native app action — app_action: ALWAYS wins when the authenticated user is asking Scout to operate Stride itself: navigate, change the Board, create/rename/archive a channel, post a message, create/rename/delete a Drive folder, delete or organize a Drive file, or change another supported in-app control. An app action is not research, a deliverable, a workstream, or a goal, even when it takes several implementation steps. Use the conversation history to resolve short confirmations such as 'yes', 'do it', or 'remove it' against Scout's immediately preceding native-action discussion.",
		"Tier 0 — answer inline: the heavily-biased default. Questions, recall, opinions, clarifications, discussion, and analysis of an image/file already in chat are ALWAYS Tier 0 — describing an image or reconstructing the likely image prompt is not research. 'what did we decide about the market?' is a question, not a research run. For Tier 0, call NO tool.",
		"Tier 1 — propose_workstream: a bounded 'go do one thing' ask (research / design / grill / workflow) that does not match a registry tool.",
		"For every proposal objective, write Scout's polished execution prompt: preserve the user's intent and constraints, remove @mentions and conversational filler, resolve obvious context from history, state the desired output and decision clearly, and never copy the request verbatim. The user will review or edit this prompt before anything runs.",
		"Tier 2 — propose_tool_run: the ask matches a registry tool's contract — the user wants a deliverable someone will read (a brief, a one-pager, a scorecard, a memo).",
		"Free-form goal — propose_goal: a real multi-step build/ship OBJECTIVE that spans SEVERAL deliverables and matches NO single registry tool ('package the Aurora IP into a one-pager AND a deck', 'take this from raw idea to a shipped pitch as one goal'). Scout decomposes it into a gated loop. A single deliverable that maps to a tool stays propose_tool_run; a full end-to-end packaging run stays packaging_studio.",
		"Ambiguous work — offer_choices: the ask is clearly work but the route is genuinely ambiguous between 2-4 concrete options, or one decisive input is missing. Ask ONE short question and offer 2-4 quick-reply options (pill labels under ~6 words); set tool_id on any option that maps to a registry tool or process. Never offer choices when one route is obvious — propose it.",
		"Intent map — route these confidently:",
		"- presentation/deck asks ('create a deck', 'make a 5-slide deck', 'presentation for this pitch', 'build the pitch deck') -> propose_tool_run packaging_studio. The packaging_studio workflow produces an html_deck artifact with the sandboxed viewer, Present button, and cover hero.",
		"- outline-only asks ('make an outline', 'outline the pitch', 'give me the slide outline', 'just the deck structure') -> propose_tool_run deck_outline. This produces a structured text outline, not a presentable deck.",
		"- full packaging studio run ('run the full packaging process', 'complete package with deck') -> propose_tool_run packaging_studio.",
		"- design identity ('develop a design identity', 'brand direction', 'look and feel', 'visual system') -> propose_tool_run brand_design_brief.",
		"- a deck built from an existing outline ('build the deck from the outline we have') -> propose_tool_run packaging_studio with the objective naming that outline as the spine; if it is unclear whether they want just an outline or the full built deck, offer_choices between deck_outline (outline only) and packaging_studio (real deck).",
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
			"- make / generate / draw / create an image, picture, poster, logo, or illustration of X -> propose_image as a hidden prompt-optimization step; write the final production-ready image prompt, not a repetition of the user's wording. This is one direct render, not a research run.",
		)
	}
	lines = append(lines,
		"When the user corrects a prior proposal or answer by naming a different tool, process, or in-app target, re-route the corrected intent. A correction naming a Stride control or object is app_action, not a workstream.",
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
			Description: "Propose a quick single-pass workstream (research / design / grill / workflow) for the user to confirm. Return Scout's polished execution prompt, not the user's raw wording.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"mode":      map[string]any{"type": "string", "enum": []string{"research", "design", "grill", "workflow"}},
					"objective": map[string]any{"type": "string", "description": "Scout's execution-ready prompt: intended outcome, key constraints, evidence or inputs to use, and the decision or deliverable to return; no @mention or conversational preamble"},
				},
				"required": []string{"mode", "objective"},
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
	// The concept-render door: a single high-quality gpt-image-2 call, offered
	// only when OpenAI image generation is configured so a keyless-OpenAI deploy
	// never routes to a render it cannot produce. Appended LAST so the three
	// text-route tools keep their pinned enum positions.
	if openAIImageGenerationAvailable() {
		tools = append(tools, anthropicTool{
			Name:        "propose_image",
			Description: "Prepare ONE image — a concept render — by optimizing the user's intent into a final production-ready prompt. Use when the user asks to make / generate / draw / create a picture, image, poster, logo, or illustration. Do not echo the request or add approval language; the app may execute this single direct render immediately.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string", "description": "the final image-generation prompt: vivid concrete subject, composition, mood, palette, lighting/material/style, and exact text only when requested; preserve the user's subject and intent"},
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
// 2026-07-05 sim and still lost to thread-context gravity inside the Sonnet-5 turn.
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

// scoutRouterImagePhrases is the reviewed, capped phrase list used as a
// deterministic image-intent fallback when the prompt-optimization router is
// unavailable. Normal image asks go through the router first so the model can
// produce the best final prompt; the fallback preserves direct execution rather
// than returning the old confirmation card.
var scoutRouterImagePhrases = []string{
	"make an image",
	"make me an image",
	"show me an image",
	"generate an image",
	"create an image",
	"draw an image",
	"make a picture",
	"make me a picture",
	"show me a picture",
	"generate a picture",
	"create a picture",
	"make a visual",
	"generate a visual",
	"create a visual",
	"make a poster",
	"make a logo",
	"make an illustration",
	"make a graphic",
	"generate a graphic",
	"create a graphic",
	"whip up",
	"render an image",
	"render a picture",
	"render a visual",
	"visualize this",
}

func scoutChatImageRequestDetected(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	for _, phrase := range scoutRouterImagePhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// scoutRouterSimpleOutlinePhrases is the reviewed phrase list that forces
// simple in-thread outline/presentation asks to conversational_reply (Tier 0).
// These asks should be answered directly in chat without any agent thread,
// workstream, or goal loop. Heavier asks that mention "review", "process",
// "full deck outline", etc. are NOT matched and can still route to tool_run.
var scoutRouterSimpleOutlinePhrases = []string{
	"slide outline",
	"5-slide outline",
	"5 slide outline",
	"five-slide outline",
	"five slide outline",
	"quick outline",
	"simple outline",
	"outline in this thread",
	"outline in-thread",
	"outline, keep in-thread",
	"outline keep in-thread",
	"outline, keep in thread",
	"outline keep in thread",
	"keep in-thread",
	"keep in thread",
	"do not email",
	"don't email",
	"in-thread outline",
	"in thread outline",
}

// scoutChatDeckRequestDetected returns true when the message is asking for a
// real presentation/deck (not just an outline). This is used to trigger HTML
// deck generation when the agent worker is unavailable.
func scoutChatDeckRequestDetected(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	// Must mention deck or presentation or slides
	hasDeckWord := strings.Contains(lower, "deck") || strings.Contains(lower, "presentation") || strings.Contains(lower, "slides")
	if !hasDeckWord {
		return false
	}
	// Exclude outline-only asks
	outlineOnly := []string{"outline only", "just the outline", "just an outline", "only the outline", "give me the outline", "slide outline"}
	for _, phrase := range outlineOnly {
		if strings.Contains(lower, phrase) {
			return false
		}
	}
	// Match deck/presentation creation phrases
	deckPhrases := []string{
		"make a deck", "create a deck", "build a deck", "make me a deck",
		"make a presentation", "create a presentation", "build a presentation",
		"make me a presentation", "create me a presentation", "build me a presentation",
		"presentation for", "deck for", "slides for",
		"slide deck", "pitch deck", "5-slide", "5 slide", "five-slide", "five slide",
		"10-slide", "10 slide", "ten-slide", "ten slide",
		"make slides", "create slides", "build slides", "make me slides",
	}
	for _, phrase := range deckPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// scoutChatSimpleOutlineRequestDetected returns true when the message matches
// the reviewed phrase list for simple in-thread outline/presentation asks.
// These are forced to conversational_reply (Tier 0) BEFORE the deterministic
// guard or router model runs, so they can never be routed to workstream/tool/goal.
func scoutChatSimpleOutlineRequestDetected(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	// Must contain "outline" or "presentation" to be an outline ask
	if !strings.Contains(lower, "outline") && !strings.Contains(lower, "presentation") {
		return false
	}
	// Heavier asks that mention review/process/full should NOT be short-circuited
	for _, heavy := range []string{"with review", "full process", "deck outline process", "run the deck outline", "full deck outline"} {
		if strings.Contains(lower, heavy) {
			return false
		}
	}
	// Check for simple outline phrases
	for _, phrase := range scoutRouterSimpleOutlinePhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// scoutAgentWorkerAvailable returns true when the agent runner is configured
// and ready to execute work. When false, no work proposals (workstream, tool_run,
// or goal) should be minted — fall through to inline answer instead.
func scoutAgentWorkerAvailable() bool {
	return selectedAgentRunnerName() != agentRunnerStub
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

// deterministicRouterGuard commits a proposal card BEFORE the Sonnet-5 router turn when a
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
	// Full-run phrases are checked FIRST so end-to-end language always wins the
	// flagship, even mid-thread about an existing package (the sim miss:
	// package_assembly stole the verdict).
	for _, phrase := range scoutRouterFullRunPhrases {
		if strings.Contains(lower, phrase) {
			if proposal := scoutRouterProposalForToolID(packagingStudioProcessID, "", text); proposal != nil {
				return &scoutRouterVerdict{proposal: proposal, source: proposalSourceDeterministicGuard}
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
					return &scoutRouterVerdict{proposal: proposal, source: proposalSourceDeterministicGuard}
				}
			}
		}
	}
	return nil
}

// routeScoutChatTurn runs the one OpenAI routing turn and returns a verdict — a
// proposal-shaped route, a quick-reply question card — or nil for Tier 0 (answer
// inline). nil is also every degraded path: keyless, router error,
// undecodable/unknown tool call — except for an explicit image ask, where the
// deterministic fallback still executes one direct render with the user's
// wording if prompt optimization is unavailable.
func (app *kanbanBoardApp) routeScoutChatTurn(ctx context.Context, text string, history []scoutChatTurn) *scoutRouterVerdict {
	return app.routeScoutChatTurnWithIntent(ctx, text, text, history)
}

// routeScoutChatTurnWithIntent separates the provider-facing context envelope
// from the human-authored intent used by deterministic guards and degraded
// fallbacks. Public-channel turns intentionally send structured identity and
// lineage to the router, but that envelope must never become an image prompt,
// proposal objective, or native-action query when the router is unavailable.
func (app *kanbanBoardApp) routeScoutChatTurnWithIntent(ctx context.Context, modelText string, intentText string, history []scoutChatTurn) *scoutRouterVerdict {
	decision := app.routeConversationIntentWithInput(ctx, modelText, conversationIntentTurn{
		Text: intentText, Modality: conversationModalityScoutChat,
	}, history)
	verdict, err := scoutRouterVerdictFromConversationIntent(decision, intentText)
	if err != nil {
		return nil
	}
	return verdict
}

func (app *kanbanBoardApp) routeConversationIntent(ctx context.Context, turn conversationIntentTurn, history []scoutChatTurn) conversationIntentDecision {
	modelText, err := conversationIntentModelText(turn)
	if err != nil {
		return unavailableConversationDecision("invalid_turn", err.Error(), proposalSourceChatRouter)
	}
	return app.routeConversationIntentWithInput(ctx, modelText, turn, history)
}

// routeConversationIntentWithInput is the one server-owned classifier used by
// every accepted natural-language surface. modelText may carry additional
// server-resolved thread lineage, but intentText remains the exact human ask
// used by deterministic safety guards and degraded-path decisions.
func (app *kanbanBoardApp) routeConversationIntentWithInput(ctx context.Context, modelText string, turn conversationIntentTurn, history []scoutChatTurn) conversationIntentDecision {
	if app == nil {
		return unavailableConversationDecision("assistant_unavailable", "Scout is unavailable right now.", proposalSourceChatRouter)
	}
	modelText = strings.TrimSpace(modelText)
	intentText := strings.TrimSpace(turn.Text)
	if intentText == "" {
		intentText = modelText
	}
	app.mu.Lock()
	apiKey := strings.TrimSpace(app.apiKey)
	app.mu.Unlock()
	if apiKey == "" {
		decision := conversationalReplyDecision(proposalSourceChatRouter)
		if scoutTurnAppearsWorkShaped(intentText) {
			decision = unavailableConversationDecision("provider_unavailable", "I can talk this through, but the private work runtime is unavailable right now.", proposalSourceChatRouter)
		}
		recordConversationIntentOutcome(decision, map[string]any{"degraded": "provider_unconfigured"})
		return decision
	}
	imageRequest := openAIImageGenerationAvailable() && scoutChatImageRequestDetected(intentText)
	if !imageRequest && scoutChatInlineAnalysisRequest(intentText) {
		decision := conversationalReplyDecision(proposalSourceDeterministicGuard)
		recordConversationIntentOutcome(decision, map[string]any{"reason": "source_analysis"})
		return decision
	}
	// Simple in-thread outline/presentation guard: when the agent worker is
	// unavailable, these asks fall back to conversational_reply so the user gets
	// a useful inline answer instead of a failed work proposal. When the worker
	// IS available, let the request route to packaging_studio (for decks) or
	// deck_outline (for outlines) which can produce real artifacts.
	if !scoutAgentWorkerAvailable() && scoutChatSimpleOutlineRequestDetected(intentText) {
		decision := conversationalReplyDecision(proposalSourceDeterministicGuard)
		recordConversationIntentOutcome(decision, map[string]any{"reason": "simple_outline_inline", "degraded": "agent_worker_unavailable"})
		return decision
	}
	// Deterministic pre-router guard: exact registry names + the reviewed
	// full-run phrase list commit the matching proposal BEFORE the model turn,
	// so thread-context gravity can never drag the literal words off the flagship
	// again. Image asks deliberately skip this guard: they need the router's
	// informed prompt interpretation before the app starts generation.
	// When the agent worker is not available, skip the guard entirely to avoid
	// minting proposals that would fail with "agent worker is not configured."
	if !imageRequest && scoutAgentWorkerAvailable() {
		if verdict := deterministicRouterGuard(intentText); verdict != nil {
			work, err := conversationWorkFromScoutProposal(verdict.proposal)
			if err != nil {
				return unavailableConversationDecision("output_contract_unavailable", "That work does not have an accepted output contract yet.", verdict.source)
			}
			decision := conversationIntentDecision{Outcome: conversationIntentStartPrivateWork, Work: &work, Source: verdict.source}
			if requiredEffect := conversationWorkRequiredEffectClass(work, ""); requiredEffect != "" {
				decision = conversationIntentDecision{Outcome: conversationIntentApprovalRequired, Approval: &conversationApprovalDecision{EffectClass: requiredEffect, Summary: "This governed action needs approval before it can run.", Work: &work}, Source: verdict.source}
			}
			recordConversationIntentOutcome(decision, map[string]any{"guard": proposalSourceDeterministicGuard})
			return decision
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	recordCapabilityPoll(capabilityTypedScoutRouter, time.Now().UTC())
	recordConversationProviderCall(ctx)
	model, effort := routerModel(), routerEffort()
	if imageRequest {
		model, effort = scoutImageDirectionModel(), scoutImageDirectionReasoningEffort()
	}
	response, err := createOpenAITextResponse(ctx, apiKey, openAITextRequest{
		Model:           model,
		Seat:            seatRouter,
		Workflow:        "scout_route",
		Instructions:    scoutRouterInstructions(),
		Input:           scoutRouterInput(modelText, history),
		ReasoningEffort: effort,
		Verbosity:       "low",
		MaxOutputTokens: scoutRouterMaxTokens,
		JSONSchema:      scoutRouterJSONSchema(),
	})
	if err != nil {
		log.Errorf("Scout router turn failed: %v", err)
		recordCapabilityFailure(capabilityTypedScoutRouter, time.Now().UTC(), err)
		decision := conversationalReplyDecision(proposalSourceChatRouter)
		if imageRequest {
			decision = conversationIntentDecision{Outcome: conversationIntentStartPrivateWork, Work: &conversationWorkDecision{Kind: conversationWorkImage, Objective: intentText}, Source: proposalSourceDeterministicGuard}
		} else if scoutTurnAppearsWorkShaped(intentText) {
			decision = unavailableConversationDecision("router_unavailable", "I couldn't safely determine the work route, so nothing started.", proposalSourceChatRouter)
		}
		recordConversationIntentOutcome(decision, map[string]any{"degraded": "router_error", "provider": providerOpenAI, "model": routerModel()})
		return decision
	}
	output, err := decodeOpenAIScoutRouterOutput(response)
	if err != nil {
		recordRouterParseFailure("strict_route")
		recordCapabilityFailure(capabilityTypedScoutRouter, time.Now().UTC(), err)
		decision := conversationalReplyDecision(proposalSourceChatRouter)
		if imageRequest {
			decision = conversationIntentDecision{Outcome: conversationIntentStartPrivateWork, Work: &conversationWorkDecision{Kind: conversationWorkImage, Objective: intentText}, Source: proposalSourceDeterministicGuard}
		} else if scoutTurnAppearsWorkShaped(intentText) {
			decision = unavailableConversationDecision("router_output_invalid", "I couldn't safely determine the work route, so nothing started.", proposalSourceChatRouter)
		}
		recordConversationIntentOutcome(decision, map[string]any{"degraded": "router_parse_error", "provider": providerOpenAI, "model": routerModel()})
		return decision
	}
	decision, err := scoutConversationIntentFromOpenAI(output, intentText)
	if err != nil {
		recordRouterParseFailure(strings.TrimSpace(output.Route))
		recordCapabilityFailure(capabilityTypedScoutRouter, time.Now().UTC(), err)
		decision := conversationalReplyDecision(proposalSourceChatRouter)
		if imageRequest {
			decision = conversationIntentDecision{Outcome: conversationIntentStartPrivateWork, Work: &conversationWorkDecision{Kind: conversationWorkImage, Objective: intentText}, Source: proposalSourceDeterministicGuard}
		} else if scoutTurnAppearsWorkShaped(intentText) {
			decision = unavailableConversationDecision("router_output_invalid", "I couldn't safely determine the work route, so nothing started.", proposalSourceChatRouter)
		}
		recordConversationIntentOutcome(decision, map[string]any{"degraded": "router_parse_error", "provider": providerOpenAI, "model": routerModel()})
		return decision
	}
	recordCapabilitySuccess(capabilityTypedScoutRouter, time.Now().UTC())
	if turn.ClarificationAlreadyAsked && decision.Outcome == conversationIntentClarifyOnce {
		decision = unavailableConversationDecision("clarification_exhausted", "I still don't have enough information to start safely. Add the missing source, audience, or constraint and try again.", proposalSourceChatRouter)
	}
	// An explicit image ask is a hard intent boundary. If the router returned a
	// non-image route, keep the direct image behavior and use the ask as the
	// conservative prompt fallback; a stray action/proposal must not steal it.
	if imageRequest && (decision.Outcome != conversationIntentStartPrivateWork || decision.Work == nil || decision.Work.Kind != conversationWorkImage) {
		decision = conversationIntentDecision{Outcome: conversationIntentStartPrivateWork, Work: &conversationWorkDecision{Kind: conversationWorkImage, Objective: intentText}, Source: proposalSourceDeterministicGuard}
	}
	// Agent worker guard: if the agent runner is not available (stub), don't
	// mint work proposals — fall through to inline answer. Image asks are
	// exempt because they use a different execution path (image generation).
	if !scoutAgentWorkerAvailable() && !imageRequest {
		if decision.Outcome == conversationIntentStartPrivateWork || decision.Outcome == conversationIntentApprovalRequired {
			decision = conversationalReplyDecision(proposalSourceChatRouter)
			recordConversationIntentOutcome(decision, map[string]any{"degraded": "agent_worker_unavailable"})
			return decision
		}
	}
	recordConversationIntentOutcome(decision, nil)
	return decision
}

func recordConversationIntentOutcome(decision conversationIntentDecision, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["verdict"] = string(decision.Outcome)
	if source := strings.TrimSpace(decision.Source); source != "" {
		fields["source"] = source
	}
	recordEvalEvent(seatRouter, evalKindRouterOutcome, fields)
}

func scoutTurnAppearsWorkShaped(text string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
	if normalized == "" || scoutChatInlineAnalysisRequest(normalized) {
		return false
	}
	for _, phrase := range []string{
		"research", "create", "build", "draft", "write", "make", "generate", "design",
		"model", "package", "revise", "redline", "translate", "regenerate", "schedule",
		"send", "publish", "delete", "deploy", "update the", "move the", "save the",
	} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

func scoutChatInlineAnalysisRequest(text string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
	if normalized == "" {
		return false
	}
	if conversationRequestsDurableMeetingWork(normalized) {
		return false
	}
	for _, durable := range []string{"research:", "deep research", "research pass", "create a report", "produce a report", "prepare a deck", "full audit", "market research"} {
		if strings.Contains(normalized, durable) {
			return false
		}
	}
	for _, ordinary := range []string{"analyze", "analyse", "describe", "what's in", "what is in", "look at", "critique", "assess", "reverse engineer", "reconstruct the prompt", "give me a prompt"} {
		if strings.Contains(normalized, ordinary) {
			return true
		}
	}
	return false
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

// recordRouterParseFailure emits one strict-JSON parse-failure event for the
// router lane (W0 item 6 — the designated gate metric for any router change):
// a tool_use block whose input the validators could not decode. tool names
// which routing schema failed; seat + model ride the fields per the
// evalKindParseFailure contract in usage_ledger.go.
func recordRouterParseFailure(tool string) {
	recordEvalEvent(seatRouter, evalKindParseFailure, map[string]any{
		"seat":  seatRouter,
		"model": routerModel(),
		"tool":  tool,
	})
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
		recordRouterParseFailure("offer_choices")
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
			recordRouterParseFailure("propose_tool_run")
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
			Mode      string `json:"mode"`
			Objective string `json:"objective"`
			Query     string `json:"query"` // backward-compatible provider fixture
		}{}
		if err := json.Unmarshal(block.Input, &args); err != nil {
			log.Errorf("Scout router propose_workstream input undecodable: %v", err)
			recordRouterParseFailure("propose_workstream")
			return nil
		}
		mode := strings.ToLower(strings.TrimSpace(args.Mode))
		switch mode {
		case "research", "design", "grill", "workflow":
		default:
			log.Errorf("Scout router proposed unknown workstream mode %q", args.Mode)
			return nil
		}
		objective := polishedWorkstreamObjective(firstNonBlank(strings.TrimSpace(args.Objective), firstNonBlank(strings.TrimSpace(args.Query), strings.TrimSpace(query))))
		return &scoutRouterProposal{
			Kind:        scoutRouterProposalKindWorkstream,
			Mode:        mode,
			Objective:   objective,
			Query:       strings.TrimSpace(query),
			Lane:        scoutProposalLane(mode, "", ""),
			WeightLabel: scoutProposalWeightQuickPass,
			Summary:     "Scout prepared an execution-ready " + assistantToolLabel(mode) + " prompt. Review or edit it before this runs once.",
		}
	case "propose_goal":
		args := struct {
			Objective     string `json:"objective"`
			AuthorityHint string `json:"authority_hint"`
			PackageID     string `json:"package_id"`
		}{}
		if err := json.Unmarshal(block.Input, &args); err != nil {
			log.Errorf("Scout router propose_goal input undecodable: %v", err)
			recordRouterParseFailure("propose_goal")
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
			recordRouterParseFailure("propose_image")
			return nil
		}
		return scoutRouterImageProposal(firstNonBlank(strings.TrimSpace(args.Prompt), strings.TrimSpace(query)), query)
	}
	return nil
}

// polishedWorkstreamObjective is the deterministic safety net for degraded or
// older router outputs. The router normally authors the execution-ready prompt;
// this helper only removes conversational addressing/filler so a raw fallback
// never reproduces "@Scout can you..." inside the approval card.
func polishedWorkstreamObjective(raw string) string {
	value := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "@") {
		if cut := strings.IndexAny(value, " \t"); cut > 1 {
			value = strings.TrimSpace(value[cut+1:])
		}
	}
	for {
		lower := strings.ToLower(value)
		trimmed := false
		for _, prefix := range []string{"can you ", "could you ", "would you ", "please ", "i need you to ", "i'd like you to "} {
			if strings.HasPrefix(lower, prefix) {
				value = strings.TrimSpace(value[len(prefix):])
				trimmed = true
				break
			}
		}
		if !trimmed {
			break
		}
	}
	value = strings.TrimLeft(value, ":,;—–- ")
	runes := []rune(value)
	if len(runes) > 0 {
		runes[0] = unicode.ToUpper(runes[0])
	}
	return strings.TrimSpace(string(runes))
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
