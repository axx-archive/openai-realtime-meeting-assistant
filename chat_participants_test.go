package main

import "testing"

func TestChatMentionCandidatesIncludeScoutAndOtherRosterMembers(t *testing.T) {
	candidates := chatMentionCandidates("aj@shareability.com")
	if len(candidates) != len(seededAccounts) {
		t.Fatalf("candidates=%d, want Scout plus %d peers", len(candidates), len(seededAccounts)-1)
	}
	if candidates[0].Name != "Scout" || candidates[0].Kind != "scout" || candidates[0].Email != "" {
		t.Fatalf("first candidate=%+v, want Scout", candidates[0])
	}
	for _, candidate := range candidates {
		if candidate.Email == "aj@shareability.com" {
			t.Fatal("viewer should not be suggested as their own mention target")
		}
	}
}
