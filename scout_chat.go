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

import (
	"strings"
	"sync"
	"time"
)

// scoutChatMaxHistoryTurns bounds the per-connection conversation history;
// one turn is one user or scout message.
const scoutChatMaxHistoryTurns = 12

type scoutChatTurn struct {
	role string // "user" or "scout"
	text string
}

type scoutChatSession struct {
	mu    sync.Mutex
	send  func(event string, data any) error
	turns []scoutChatTurn
}

func newScoutChatSession(conn *threadSafeWriter) *scoutChatSession {
	return &scoutChatSession{
		send: func(event string, data any) error {
			return sendKanbanEvent(conn, event, data)
		},
	}
}

// handle answers one private chat message. The lock serializes messages per
// connection so follow-ups thread through history in order.
func (session *scoutChatSession) handle(app *kanbanBoardApp, text string) {
	if session == nil {
		return
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	text = strings.TrimSpace(text)
	if text == "" {
		session.sendEventLocked("error", "say something first")
		return
	}

	session.sendEventLocked("query", text)
	session.sendEventLocked("status", "thinking…")

	history := make([]scoutChatTurn, len(session.turns))
	copy(history, session.turns)
	result, err := app.resolveAssistantQuery(text, history)
	if err != nil {
		session.sendEventLocked("error", err.Error())
		return
	}

	session.turns = append(session.turns,
		scoutChatTurn{role: "user", text: result.query},
		scoutChatTurn{role: "scout", text: result.answer},
	)
	if len(session.turns) > scoutChatMaxHistoryTurns {
		session.turns = session.turns[len(session.turns)-scoutChatMaxHistoryTurns:]
	}

	session.sendEventLocked("answer", result.answer)
}

func (session *scoutChatSession) sendEventLocked(kind string, text string) {
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
