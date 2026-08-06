package main

import "testing"

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

func TestChatMentionCandidatesIncludeOnlyCurrentlyHiredValidAgents(t *testing.T) {
	fixture := newSTRIDEProjectAuthorityFixture(t)
	hired := hireResearchAgentForBridgeTest(t, fixture, "colton-research", strideProductAgentDirectThreadPrefix+"mention_candidate")

	candidates := fixture.app.chatMentionCandidatesForViewer(fixture.user.Email)
	found := false
	for _, candidate := range candidates {
		if candidate.AgentID != hired.ID {
			continue
		}
		found = candidate.Kind == "agent" && candidate.Name == hired.DisplayName && candidate.Handle == chatMentionHandle(hired.DisplayName) && candidate.Email == "" && candidate.RoleTitle == "Research Partner"
	}
	if !found {
		t.Fatalf("hired Colton missing from agent mention candidates: %+v", candidates)
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
			t.Fatalf("paused agent remained mentionable: %+v", candidate)
		}
	}
}
