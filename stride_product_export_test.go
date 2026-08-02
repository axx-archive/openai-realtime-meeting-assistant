package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSTRIDEProductAgentExportContainsNoTenantDataMemoryAssignmentsOrCredentials(t *testing.T) {
	now := time.Date(2026, 7, 31, 18, 30, 0, 0, time.UTC)
	agent := STRIDEProductTeamAgent{
		ID: "agent_private_export", ListingID: "mary-marketing", DisplayName: "Mary", Category: "marketing", Status: "offboarded",
		OwnerID: "member_secret_owner", DirectThreadID: "thread_secret_direct", Revision: 7,
		Config:                  STRIDEProductAgentConfig{PersonalityNotes: "private personality overlay", Memberships: []string{"secret_project"}, PerRunBudgetCents: 25, DailyBudgetCents: 100, Proactivity: "disabled"},
		Assignments:             []STRIDEProductAgentAssignment{{ID: "assignment_secret", ProjectOrChannel: "secret_project", Role: "marketing_partner", Responsibility: "confidential acquisition plan", Destination: "thread_secret_project", Status: "active_fenced", CreatedAt: now}},
		Learning:                []STRIDEProductAgentLearning{{ID: "learning_secret", Subject: "marketing", Scope: "team", Summary: "founder prefers private detail", Status: "forgotten", Revision: 2, CreatedAt: now, UpdatedAt: now}},
		ProviderExecutionFenced: true, AccessRevoked: true, Lifecycle: []string{"offboarded_and_export_preserved"}, CreatedAt: now, UpdatedAt: now,
	}
	exported, err := safeSTRIDEProductAgentExport(agent)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{agent.ID, agent.OwnerID, agent.DirectThreadID, "secret_project", "confidential acquisition plan", "founder prefers private detail", "private personality overlay"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("clean export leaked %q: %s", secret, raw)
		}
	}
	if exported.ContainsTenantData || exported.ContainsCredentials || exported.ContainsMemory || exported.ContainsAssignments || exported.ContainsPrivateEvidence || !exported.ProviderExecutionFenced || !exported.AccessRevoked || !isHexDigest(exported.HistoricalAttributionHash) {
		t.Fatalf("unsafe export=%#v", exported)
	}
}
