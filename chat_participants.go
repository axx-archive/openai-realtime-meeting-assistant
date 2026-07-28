package main

import (
	"net/http"
	"strings"
)

type chatMentionCandidate struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	Kind  string `json:"kind"`
}

func chatMentionCandidates(viewerEmail string) []chatMentionCandidate {
	viewerEmail = normalizeAccountEmail(viewerEmail)
	candidates := []chatMentionCandidate{{Name: "Scout", Kind: "scout"}}
	for _, seed := range seededAccounts {
		email := normalizeAccountEmail(seed.Email)
		if email == "" || email == viewerEmail {
			continue
		}
		name := strings.TrimSpace(participantNameForEmail(email))
		if name == "" {
			continue
		}
		candidates = append(candidates, chatMentionCandidate{Name: name, Email: email, Kind: "person"})
	}
	return candidates
}

func assistantChatParticipantsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
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
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok": true, "participants": chatMentionCandidates(user.Email),
	})
}
