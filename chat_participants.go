package main

import (
	"net/http"
	"strings"
)

type chatMentionCandidate struct {
	Name          string `json:"name"`
	Handle        string `json:"handle,omitempty"`
	Email         string `json:"email,omitempty"`
	AgentID       string `json:"agentId,omitempty"`
	Kind          string `json:"kind"`
	AvatarDataURL string `json:"avatarDataURL,omitempty"`
}

func chatMentionHandle(name string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(name)), "-")
}

func chatMentionCandidates(viewerEmail string) []chatMentionCandidate {
	viewerEmail = normalizeAccountEmail(viewerEmail)
	candidates := []chatMentionCandidate{{Name: "Scout", Handle: "Scout", Kind: "scout"}}
	for _, seed := range seededAccounts {
		email := normalizeAccountEmail(seed.Email)
		if email == "" || email == viewerEmail {
			continue
		}
		name := strings.TrimSpace(participantNameForEmail(email))
		if name == "" {
			continue
		}
		avatarDataURL := ""
		if user := accountStore().findUser(email); user != nil {
			avatarDataURL = user.AvatarDataURL
		}
		candidates = append(candidates, chatMentionCandidate{Name: name, Handle: chatMentionHandle(name), Email: email, Kind: "person", AvatarDataURL: avatarDataURL})
	}
	return candidates
}

// chatMentionCandidatesForViewer extends the human directory with the
// currently valid hired STRIDE seats. A mention candidate is identity and
// discovery data only: channel membership, capability, provider, and approval
// fences are re-checked when an explicit work proposal is minted and again
// when that persisted proposal is accepted.
func (app *kanbanBoardApp) chatMentionCandidatesForViewer(viewerEmail string) []chatMentionCandidate {
	candidates := chatMentionCandidates(viewerEmail)
	for _, profile := range app.strideMentionableAgentProfiles() {
		candidates = append(candidates, chatMentionCandidate{
			Name:    profile.DisplayName,
			Handle:  chatMentionHandle(profile.DisplayName),
			AgentID: profile.AgentID,
			Kind:    "agent",
		})
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
		"ok": true, "participants": kanbanApp.chatMentionCandidatesForViewer(user.Email),
	})
}
