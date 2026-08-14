package main

import "strings"

const (
	scoutChatBoardIntentOpen  = "open"
	scoutChatBoardIntentClear = "clear"
)

// scoutChatBoardAction is a compatibility response for old typed requests. It
// never reads or opens archived cards; old Board navigation lands in Work.
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
	action := &scoutChatBoardAction{
		Surface:          "artifacts",
		Action:           "open",
		RequestedIntent:  intent,
		ActiveCardCount:  0,
		ReadOnly:         true,
		MutationExecuted: false,
		Reason:           "board_retired",
	}
	if intent == scoutChatBoardIntentClear {
		return action, "The Board is retired and its history is preserved read-only. Nothing was changed; I’ll open Work instead."
	}
	return action, "The Board is retired. I’ll open Work, where current work is grounded in conversations, meetings, files, and artifacts."
}
