package main

import (
	"strconv"
	"strings"
)

const (
	scoutChatBoardIntentOpen  = "open"
	scoutChatBoardIntentClear = "clear"
)

// scoutChatBoardAction is a navigation contract, not mutation authority. The
// typed-chat client may use it to open the Board, while RequestedIntent keeps
// the user's original intent visible. In particular, a clear request remains
// read-only until the product has a durable, recoverable Trash operation.
type scoutChatBoardAction struct {
	Surface          string `json:"surface"`
	Action           string `json:"action"`
	RequestedIntent  string `json:"requestedIntent"`
	ActiveCardCount  int    `json:"activeCardCount"`
	ReadOnly         bool   `json:"readOnly"`
	MutationExecuted bool   `json:"mutationExecuted"`
	Reason           string `json:"reason,omitempty"`
}

// scoutChatBoardIntent recognizes only direct Board navigation and whole-board
// destructive requests. It deliberately ignores card-level wording so normal
// discussion and future scoped card actions continue through their own lanes.
func scoutChatBoardIntent(text string) string {
	lower := strings.ToLower(strings.Join(strings.Fields(text), " "))
	if lower == "" || (!strings.Contains(lower, "kanban board") && !containsWholeWord(lower, "board")) {
		return ""
	}

	wholeBoardTarget := strings.Contains(lower, "everything") || containsWholeWord(lower, "all") || strings.Contains(lower, "clear out")
	cardLevelTarget := containsWholeWord(lower, "card") && !wholeBoardTarget
	clearWholeBoard := containsWholeWord(lower, "clear") && !cardLevelTarget
	destructiveVerb := clearWholeBoard || containsWholeWord(lower, "empty") || containsWholeWord(lower, "delete") || containsWholeWord(lower, "remove")
	if destructiveVerb && (clearWholeBoard || wholeBoardTarget || containsWholeWord(lower, "empty")) {
		return scoutChatBoardIntentClear
	}

	for _, phrase := range []string{"open", "show", "view", "go to", "take me to", "bring up"} {
		if strings.Contains(lower, phrase) {
			return scoutChatBoardIntentOpen
		}
	}
	return ""
}

func containsWholeWord(text string, word string) bool {
	for _, field := range strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if field == word {
			return true
		}
	}
	return false
}

func (app *kanbanBoardApp) scoutChatBoardActionForIntent(intent string) (*scoutChatBoardAction, string) {
	if app == nil || (intent != scoutChatBoardIntentOpen && intent != scoutChatBoardIntentClear) {
		return nil, ""
	}
	activeCardCount := len(app.snapshotState().Cards)
	action := &scoutChatBoardAction{
		Surface:          "board",
		Action:           "open",
		RequestedIntent:  intent,
		ActiveCardCount:  activeCardCount,
		ReadOnly:         true,
		MutationExecuted: false,
	}
	itemLabel := "items"
	if activeCardCount == 1 {
		itemLabel = "item"
	}
	if intent == scoutChatBoardIntentClear {
		action.Reason = "durable_trash_required"
		return action, "I found " + boardCountLabel(activeCardCount, itemLabel) + " on the Board. I haven’t changed anything—clearing the Board needs a durable Trash flow first so every item stays recoverable. I’ll open it for you."
	}
	return action, "Opening the Board—there are " + boardCountLabel(activeCardCount, itemLabel) + " on it right now."
}

func boardCountLabel(count int, itemLabel string) string {
	return strconv.Itoa(count) + " " + itemLabel
}
