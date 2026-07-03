package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Board draft flow (D4): Scout's board worker proposes cards as pending
// drafts; a human accepts ("Add to board") or dismisses them from the Board
// surface. Reads/writes are session-authed HTTP like the codex-proposal
// actions — drafts are actionable from any signed-in tab, without joining
// the call.

// assistantBoardDraftActionHandler serves
// POST /assistant/board/drafts/{cardId}/{accept|dismiss}
// with the same origin + session guards as the proposal handler. Any
// signed-in user may accept or dismiss a Scout draft.
func assistantBoardDraftActionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
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

	suffix := strings.Trim(strings.TrimPrefix(r.URL.Path, "/assistant/board/drafts/"), "/")
	parts := strings.Split(suffix, "/")
	if suffix == "" || len(parts) != 2 || parts[0] == "" || (parts[1] != "accept" && parts[1] != "dismiss") {
		http.NotFound(w, r)
		return
	}
	cardID := parts[0]
	action := parts[1]

	// Body is optional ({}); dismissals may carry a short reason for memory.
	payload := struct {
		Reason string `json:"reason"`
	}{}
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&payload)
	}

	result, changed, err := kanbanApp.resolveBoardDraft(cardID, action, user.Name, payload.Reason)
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
		kanbanApp.refreshRealtimeBoardContext("board draft " + action)
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"action": action,
		"card":   result["card"],
	})
}

// resolveBoardDraft applies an accept/dismiss to a Scout draft card and
// narrates the outcome to the room feed. Dismissals write a durable memory
// note — "scout will remember why" stays honest.
func (app *kanbanBoardApp) resolveBoardDraft(cardID string, action string, actorName string, reason string) (map[string]any, bool, error) {
	actorName = canonicalRoomActorName(actorName)
	args := map[string]any{"card_id": cardID}

	switch action {
	case "accept":
		result, changed, err := app.acceptDraftTicket(args)
		if err != nil {
			return nil, false, err
		}
		if card, ok := result["card"].(kanbanCard); ok && changed {
			broadcastAssistantEvent("action", fmt.Sprintf("%s added Scout's draft \"%s\" to the board", actorName, card.Title), nil)
		}
		return result, changed, nil
	case "dismiss":
		result, changed, err := app.dismissDraftTicket(args)
		if err != nil {
			return nil, false, err
		}
		if card, ok := result["card"].(kanbanCard); ok && changed {
			app.rememberDismissedDraft(card, actorName, reason)
			broadcastAssistantEvent("action", fmt.Sprintf("%s dismissed Scout's draft \"%s\"", actorName, card.Title), nil)
		}
		return result, changed, nil
	default:
		return nil, false, fmt.Errorf("unknown draft action %q", action)
	}
}

// rememberDismissedDraft records the dismissed draft in meeting memory so
// the board worker sees prior rejections and Scout does not redraft the
// same card.
func (app *kanbanBoardApp) rememberDismissedDraft(card kanbanCard, actorName string, reason string) {
	if app.memory == nil {
		return
	}
	reason = trimForStorage(strings.TrimSpace(reason), 300)
	var text strings.Builder
	text.WriteString("## Summary\n")
	fmt.Fprintf(&text, "%s dismissed Scout's draft card %q (%s). Do not redraft this card unless the room raises it again.", actorName, card.Title, card.Status)
	if reason != "" {
		fmt.Fprintf(&text, " Reason: %s", reason)
	}
	if notes := strings.TrimSpace(card.Notes); notes != "" {
		text.WriteString("\n\n## Draft notes\n")
		text.WriteString(notes)
	}
	metadata := map[string]string{
		"source":      "board_draft_dismiss",
		"cardId":      card.ID,
		"cardTitle":   card.Title,
		"dismissedBy": actorName,
	}
	id := durableTimestampID("draft-dismiss", time.Now())
	if _, _, err := app.memory.appendBoardUpdate(id, text.String(), metadata); err != nil {
		log.Errorf("Could not remember dismissed board draft: %v", err)
	}
}
