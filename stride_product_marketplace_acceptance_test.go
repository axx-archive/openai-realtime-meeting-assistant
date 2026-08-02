package main

import (
	"testing"
	"time"
)

func TestSTRIDEProductCatalogStagesFiveDistinctFirstPartyListingsWithRichFencedDetails(t *testing.T) {
	state := NewSTRIDEProductState()
	catalog := state.candidateCatalog()
	wanted := map[string]bool{"insights": false, "marketing": false, "research": false, "design": false, "builder": false}
	firstParty := 0
	for _, candidate := range catalog {
		if candidate.Provenance != "stride_authored" {
			continue
		}
		firstParty++
		if _, expected := wanted[candidate.Category]; expected {
			wanted[candidate.Category] = true
		}
		if candidate.LiveAvailable || !candidate.ProviderExecutionFenced || candidate.Availability != "internal_preview" || candidate.Visibility != "organization" || candidate.UpdatePolicy != "human_approval" ||
			len(candidate.Capabilities) == 0 || len(candidate.RequiredAccess) == 0 || candidate.AccessSummary == "" || candidate.CostBand == "" || candidate.Publisher != "STRIDE" || candidate.Version == "" || candidate.MemoryPolicy == "" || !isHexDigest(candidate.PackageDigest) ||
			candidate.ReceiptStatus["providerQuality"] || candidate.ReceiptStatus["humanAdmission"] {
			t.Fatalf("candidate is not a rich default-off preview: %#v", candidate)
		}
	}
	if firstParty < 5 {
		t.Fatalf("first-party listing count=%d, want at least five", firstParty)
	}
	for category, found := range wanted {
		if !found {
			t.Fatalf("missing first-party %s listing", category)
		}
	}
}

func TestSTRIDEProductMaterialUpdateCarriesHumanReadableSemanticDiffAndStaysOptIn(t *testing.T) {
	state := NewSTRIDEProductState()
	now := time.Date(2026, 7, 31, 21, 30, 0, 0, time.UTC)
	agent, err := state.beginTrial("mary-marketing", "member_aj", now)
	if err != nil {
		t.Fatal(err)
	}
	agent, err = state.proposeAgentUpdate(agent.ID, agent.Revision, "Dog Perfect launch scope", STRIDEProductAgentConfig{PersonalityNotes: "Be more concise.", Memberships: []string{"team", "dog_perfect"}, PerRunBudgetCents: 25, DailyBudgetCents: 100, Proactivity: "quiet"}, now.Add(time.Minute))
	if err != nil || len(agent.Updates) != 1 {
		t.Fatalf("propose=%#v err=%v", agent, err)
	}
	update := agent.Updates[0]
	if update.Status != "pending" || !update.SemanticDiff.PersonalityChanged || !update.SemanticDiff.PermissionChanged || !update.SemanticDiff.CostChanged || !update.SemanticDiff.ProactivityChanged || update.SemanticDiff.RuntimeChanged ||
		len(update.SemanticDiff.MembershipsAdded) != 1 || update.SemanticDiff.MembershipsAdded[0] != "dog_perfect" || update.SemanticDiff.RuntimeSummary == "" || update.SemanticDiff.MigrationSummary == "" || !isHexDigest(update.SemanticDiff.Digest) {
		t.Fatalf("semantic diff=%#v", update.SemanticDiff)
	}
	if agent.Config.PersonalityNotes != "" || len(agent.Config.Memberships) != 1 || agent.Config.Memberships[0] != "team" {
		t.Fatalf("pending update silently activated: %#v", agent.Config)
	}
	approved, err := state.resolveAgentUpdate(agent.ID, agent.Revision, update.ID, "approve", now.Add(2*time.Minute))
	if err != nil || approved.Config.PersonalityNotes != "Be more concise." {
		t.Fatalf("approved=%#v err=%v", approved, err)
	}
	rolledBack, err := state.resolveAgentUpdate(approved.ID, approved.Revision, update.ID, "rollback", now.Add(3*time.Minute))
	if err != nil || rolledBack.Config.PersonalityNotes != "" || rolledBack.Updates[0].Status != "rolled_back" {
		t.Fatalf("rolled back=%#v err=%v", rolledBack, err)
	}
}
