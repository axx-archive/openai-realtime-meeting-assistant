package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// assistantBoardCardsHandler gives signed-in native clients the same manual
// create/edit/delete/undo operations as the room WebSocket board editor.
func assistantBoardCardsHandler(w http.ResponseWriter, r *http.Request) {
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "the board is unavailable")
		return
	}

	base := "/assistant/board/cards"
	suffix := strings.Trim(strings.TrimPrefix(r.URL.Path, base), "/")
	if r.URL.Path != base && !strings.HasPrefix(r.URL.Path, base+"/") {
		http.NotFound(w, r)
		return
	}

	actor := canonicalRoomActorName(user.Name)
	if suffix == "undo" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeBoardCardMutation(w, actor, "restored the last deleted card", kanbanApp.restoreLastDeletedTicket)
		return
	}

	switch r.Method {
	case http.MethodPost:
		if suffix != "" {
			http.NotFound(w, r)
			return
		}
		args, ok := decodeBoardCardBody(w, r)
		if !ok {
			return
		}
		delete(args, "draft")
		writeBoardCardMutation(w, actor, "created a card", func() (map[string]any, bool, error) {
			return kanbanApp.createTicket(args)
		})
	case http.MethodPut, http.MethodPatch:
		if suffix == "" || strings.Contains(suffix, "/") {
			http.NotFound(w, r)
			return
		}
		args, ok := decodeBoardCardBody(w, r)
		if !ok {
			return
		}
		args["card_id"] = suffix
		writeBoardCardMutation(w, actor, "updated a card", func() (map[string]any, bool, error) {
			return kanbanApp.updateTicketDetails(args)
		})
	case http.MethodDelete:
		if suffix == "" || strings.Contains(suffix, "/") {
			http.NotFound(w, r)
			return
		}
		writeBoardCardMutation(w, actor, "deleted a card", func() (map[string]any, bool, error) {
			return kanbanApp.deleteTicket(map[string]any{"card_id": suffix})
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func decodeBoardCardBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	args := map[string]any{}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := decoder.Decode(&args); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read board edit")
		return nil, false
	}
	return args, true
}

func writeBoardCardMutation(w http.ResponseWriter, actor string, action string, apply func() (map[string]any, bool, error)) {
	result, changed, err := apply()
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "unknown card_id") {
			status = http.StatusNotFound
		}
		writeAuthError(w, status, err.Error())
		return
	}
	if changed {
		broadcastSignedInKanbanEvent("board", kanbanApp.snapshotState())
		broadcastSignedInKanbanEvent("undo_available", kanbanApp.canUndoDelete())
		broadcastAssistantEvent("action", fmt.Sprintf("%s %s", actor, action), nil)
		kanbanApp.refreshRealtimeBoardContext(action)
	}
	result["changed"] = changed
	writeAuthJSON(w, http.StatusOK, result)
}
