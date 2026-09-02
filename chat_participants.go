package main

import (
	"net/http"
	"sort"
	"strings"
)

type chatMentionCandidate struct {
	Name          string `json:"name"`
	Handle        string `json:"handle,omitempty"`
	Email         string `json:"email,omitempty"`
	AgentID       string `json:"agentId,omitempty"`
	RoleTitle     string `json:"roleTitle,omitempty"`
	Kind          string `json:"kind"`
	AvatarDataURL string `json:"avatarDataURL,omitempty"`
}

func chatMentionHandle(name string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(name)), "-")
}

// accountDisplayName is the one human display-name rule for registered
// accounts: the roster name for a seeded account, else the account's own name,
// else the email local-part. Not participantNameForAccount, which
// canonicalizes through the fixed meetingParticipantNames roster and returns
// "" for anyone else.
func accountDisplayName(user *userAccount) string {
	if user == nil {
		return ""
	}
	if name := strings.TrimSpace(participantNameForEmail(user.Email)); name != "" {
		return name
	}
	if name := strings.Join(strings.Fields(user.Name), " "); name != "" {
		return name
	}
	email := normalizeAccountEmail(user.Email)
	if at := strings.Index(email, "@"); at > 0 {
		return email[:at]
	}
	return email
}

// chatMentionCandidates lists Scout plus every registered human account
// except the viewer. It reads the account store, not the seeded roster, so a
// person added after the seed list still shows up in @-completion (D7). Scout
// stays first; people are sorted by display name (roster name when the account
// is a seed, otherwise the account's own name) so the picker is stable across
// devices regardless of store insertion order.
func chatMentionCandidates(viewerEmail string) []chatMentionCandidate {
	viewerEmail = normalizeAccountEmail(viewerEmail)
	candidates := []chatMentionCandidate{{Name: "Scout", Handle: "Scout", RoleTitle: "Chief of staff", Kind: "scout"}}
	store := accountStore()
	people := make([]chatMentionCandidate, 0, len(seededAccounts))
	seen := map[string]bool{}
	for _, raw := range store.accountEmails() {
		email := normalizeAccountEmail(raw)
		if email == "" || email == viewerEmail || seen[email] {
			continue
		}
		seen[email] = true
		user := store.findUser(email)
		if user == nil || user.disabled() {
			// Offboarded accounts (Wave 5 D11) stay on disk for history but are
			// never offered as a mention target.
			continue
		}
		name := accountDisplayName(user)
		if name == "" {
			continue
		}
		people = append(people, chatMentionCandidate{Name: name, Handle: chatMentionHandle(name), Email: email, Kind: "person", AvatarDataURL: user.AvatarDataURL})
	}
	sort.SliceStable(people, func(i, j int) bool {
		left, right := strings.ToLower(people[i].Name), strings.ToLower(people[j].Name)
		if left != right {
			return left < right
		}
		return people[i].Email < people[j].Email
	})
	return append(candidates, people...)
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
			Name:      profile.DisplayName,
			Handle:    chatMentionHandle(profile.DisplayName),
			AgentID:   profile.AgentID,
			RoleTitle: profile.RoleTitle,
			Kind:      "agent",
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
