package main

// Private text chat with Scout, delivered only to the requesting websocket
// connection. Answers reuse the shared room memory store and board: per-user
// memory scoping is an open product decision, so every member currently chats
// against the same room-wide knowledge while delivery stays per-connection.
//
// Wire protocol (kanban envelope, sent only to the requesting connection):
//   client -> server  ws event "scout_chat" with data {"text": "..."}
//   server -> client  kanban event "scout_chat" with data
//                     {"kind":"query"|"status"|"answer"|"thread"|"error","text":...,"ts":RFC3339Nano}
//
// Lifecycle: submit runs on the websocket read goroutine and echoes the query
// immediately (a message must never look dropped while an earlier turn is
// still answering), then hands the text to a single per-session worker that
// answers strictly FIFO. The queue is bounded; the worker's model calls are
// tied to a per-connection context cancelled when the websocket closes, so a
// disconnected client cannot leave a backlog of model calls running.

import (
	"context"
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

	if mode := scoutChatThreadModeForText(text); mode != "" {
		session.sendEvent("query", text)
		session.sendEvent("status", "launching "+assistantToolLabel(mode)+" thread...")
		session.launchThread(app, mode, text, actor)
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

func (session *scoutChatSession) launchThread(app *kanbanBoardApp, mode string, text string, actor string) {
	if app == nil {
		session.sendEvent("error", "assistant is unavailable")
		return
	}

	thread, err := app.launchAgentThread(mode, text, actor)
	if err != nil {
		session.sendEvent("error", err.Error())
		return
	}

	summary := assistantToolLabel(thread.Mode) + " thread launched"
	session.mu.Lock()
	session.turns = append(session.turns,
		scoutChatTurn{role: "user", text: thread.Query},
		scoutChatTurn{role: "scout", text: summary},
	)
	if len(session.turns) > scoutChatMaxHistoryTurns {
		session.turns = session.turns[len(session.turns)-scoutChatMaxHistoryTurns:]
	}
	session.mu.Unlock()

	session.sendThreadEvent(thread, summary)
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
// anything; full conversational keyword matching (scoutChatThreadModeForText)
// is reserved for private threads where every message is already addressed to
// Scout.
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

func scoutChatThreadModeForText(text string) string {
	lower := strings.ToLower(strings.Join(strings.Fields(text), " "))
	if hasAssistantPhrase(lower, "multi-agent", "codex", "goal loop", "goal workflow", "workflow", "shipping gate", "gate before shipping") {
		return "workflow"
	}
	if hasAssistantPhrase(lower, "design", "wireframe", "prototype", "ux", "interface", "screen", "mockup") {
		return "design"
	}
	if hasAssistantPhrase(lower, "grill", "pitch", "pressure-test", "pressure test", "scorecard", "objection", "tough questions") {
		return "grill"
	}
	if hasAssistantPhrase(lower, "research", "investigate", "source", "market", "competitive", "brief", "dig into") {
		return "research"
	}
	return ""
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

func (session *scoutChatSession) sendThreadEvent(thread scoutAgentThread, text string) {
	if session == nil || session.send == nil {
		return
	}
	if err := session.send("scout_chat", map[string]any{
		"kind":     "thread",
		"text":     text,
		"thread":   thread,
		"artifact": thread.Artifact,
		"actions":  thread.Actions,
		"ts":       time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		log.Errorf("Failed to send scout chat thread event: %v", err)
	}
}
