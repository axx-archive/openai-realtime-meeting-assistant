package main

import (
	"strings"
	"testing"
	"time"
)

func TestChatMentionCandidatesIncludeScoutAndOtherRosterMembers(t *testing.T) {
	setupAuthTestEnv(t)
	avatar := "data:image/png;base64,aGVsbG8="
	if _, err := accountStore().updateProfile("tim@shareability.com", "Tim", avatar); err != nil {
		t.Fatalf("update Tim profile: %v", err)
	}
	candidates := chatMentionCandidates("aj@shareability.com")
	if len(candidates) != len(seededAccounts) {
		t.Fatalf("candidates=%d, want Scout plus %d peers", len(candidates), len(seededAccounts)-1)
	}
	if candidates[0].Name != "Scout" || candidates[0].Kind != "scout" || candidates[0].Email != "" {
		t.Fatalf("first candidate=%+v, want Scout", candidates[0])
	}
	if candidates[0].Handle != "Scout" {
		t.Fatalf("Scout handle=%q, want Scout", candidates[0].Handle)
	}
	if candidates[0].RoleTitle != "Chief of staff" {
		t.Fatalf("Scout role title=%q, want Chief of staff", candidates[0].RoleTitle)
	}
	for _, candidate := range candidates {
		if candidate.Email == "aj@shareability.com" {
			t.Fatal("viewer should not be suggested as their own mention target")
		}
		if candidate.Email == "tim@shareability.com" && candidate.AvatarDataURL != avatar {
			t.Fatalf("Tim candidate avatar=%q, want current profile avatar", candidate.AvatarDataURL)
		}
	}
}

func TestChatMentionHandleKeepsMultiwordNamesAsOneWireToken(t *testing.T) {
	if got := chatMentionHandle(" Insights   Analyst "); got != "Insights-Analyst" {
		t.Fatalf("handle=%q, want Insights-Analyst", got)
	}
	mentions := parseChatMentionTokens("@Insights-Analyst dig into this article")
	if len(mentions) != 1 || mentions[0].handle != "insights-analyst" {
		t.Fatalf("mentions=%v, want one canonical multiword handle", mentions)
	}
}

func TestChatMentionCandidatesExposeOnlyFixedAddressableSpecialists(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	hired := hireResearchAgentForBridgeTest(t, fixture, "colton-research", strideProductAgentDirectThreadPrefix+"mention_candidate")

	candidates := fixture.app.chatMentionCandidatesForViewer(fixture.user.Email)
	found := map[string]chatMentionCandidate{}
	for _, candidate := range candidates {
		if candidate.AgentID != "" {
			found[candidate.AgentID] = candidate
		}
	}
	if _, legacyVisible := found[hired.ID]; legacyVisible {
		t.Fatalf("legacy hired seat remained customer-addressable: %+v", candidates)
	}
	if researcher := found["agent_researcher"]; researcher.Name != "Researcher" || researcher.Kind != "agent" || researcher.RoleTitle != "Researcher" {
		t.Fatalf("Researcher mention contract missing: %+v", candidates)
	}
	if presenter := found["agent_presenter"]; presenter.Name != "Presenter" || presenter.Kind != "agent" || presenter.RoleTitle != "Presentation Designer" {
		t.Fatalf("Presenter mention contract missing: %+v", candidates)
	}

	err := fixture.runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		_, err := ctx.Product.mutateAgent(hired.ID, hired.Revision, func(agent *STRIDEProductTeamAgent) error {
			agent.Status = "paused"
			agent.AccessRevoked = true
			return nil
		}, fixture.config.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range fixture.app.chatMentionCandidatesForViewer(fixture.user.Email) {
		if candidate.AgentID == hired.ID {
			t.Fatalf("paused legacy agent became mentionable: %+v", candidate)
		}
	}
}

func TestChatMentionCandidatesEnumerateRegisteredAccountsAndSkipViewer(t *testing.T) {
	setupAuthTestEnv(t)
	store := accountStore()
	seed := store.findUser("aj@shareability.com")
	if seed == nil {
		t.Fatal("seed account is unavailable")
	}
	const extraEmail = "future-teammate@shareability.com"
	const extraAvatar = "data:image/png;base64,ZnV0dXJl"
	extra := &userAccount{
		Email:             extraEmail,
		Name:              "Future Teammate",
		AvatarDataURL:     extraAvatar,
		PasswordHash:      append([]byte(nil), seed.PasswordHash...),
		WebAuthnHandle:    []byte("future-teammate-mention-handle"),
		PasswordChangedAt: time.Now().UTC(),
	}
	store.mu.Lock()
	store.users[extraEmail] = extra
	store.mu.Unlock()
	// The account store is process-global; remove the fixture so later tests
	// in the package never see a phantom non-seed account.
	t.Cleanup(func() {
		store.mu.Lock()
		delete(store.users, extraEmail)
		store.mu.Unlock()
	})
	if participantNameForEmail(extraEmail) != "" {
		t.Fatal("fixture must be a non-seed account")
	}

	candidates := chatMentionCandidates("aj@shareability.com")
	if len(candidates) != len(seededAccounts)+1 {
		t.Fatalf("candidates=%d, want Scout + %d seeded peers + the registered extra", len(candidates), len(seededAccounts)-1)
	}
	if candidates[0].Kind != "scout" || candidates[0].Name != "Scout" {
		t.Fatalf("first candidate=%+v, want Scout", candidates[0])
	}
	var found *chatMentionCandidate
	previous := ""
	for index := range candidates[1:] {
		candidate := candidates[1+index]
		if candidate.Kind != "person" {
			t.Fatalf("non-person candidate in the human roster: %+v", candidate)
		}
		if candidate.Email == "aj@shareability.com" {
			t.Fatal("viewer suggested as their own mention target")
		}
		if name := strings.ToLower(candidate.Name); name < previous {
			t.Fatalf("people are not sorted by name: %q after %q", candidate.Name, previous)
		} else {
			previous = name
		}
		if candidate.Email == extraEmail {
			found = &candidates[1+index]
		}
	}
	if found == nil {
		t.Fatalf("registered non-seed account missing from candidates: %+v", candidates)
	}
	if found.Name != "Future Teammate" || found.Handle != "Future-Teammate" || found.AvatarDataURL != extraAvatar {
		t.Fatalf("extra candidate=%+v, want account name, one-token handle, and avatar", *found)
	}

	asExtra := chatMentionCandidates(extraEmail)
	if len(asExtra) != len(seededAccounts)+1 {
		t.Fatalf("as extra viewer candidates=%d, want Scout + every seeded account", len(asExtra))
	}
	sawAJ := false
	for _, candidate := range asExtra {
		if candidate.Email == extraEmail {
			t.Fatal("extra viewer suggested as their own mention target")
		}
		if candidate.Email == "aj@shareability.com" {
			sawAJ = true
		}
	}
	if !sawAJ {
		t.Fatal("extra viewer does not see the seeded roster")
	}
}
