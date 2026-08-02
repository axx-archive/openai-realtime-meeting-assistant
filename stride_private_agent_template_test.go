package main

import (
	"errors"
	"testing"
	"time"
)

func stridePrivateTemplateRequestForTest() STRIDEProductPrivateTemplateRequest {
	return STRIDEProductPrivateTemplateRequest{
		TemplateID: "launch_partner", DisplayName: "Lane", Category: "launch", OutcomeSummary: "Turns approved launch context into bounded briefs.", PersonalitySummary: "Candid, calm, and concise.",
		SampleOutputs: []string{"Launch brief", "Risk review"}, RequestedCapabilities: []string{"approved_project_brief"}, RequiredAccess: []string{"approved_project_context"}, CostBand: "low",
		Memberships: []string{"team"}, PerRunBudgetCents: 25, DailyBudgetCents: 100, MonthlyBudgetCents: 500, Concurrency: 1, Proactivity: "disabled",
	}
}

func TestSTRIDEPrivateTemplateCreatesOneFencedOrganizationCandidateAndReplays(t *testing.T) {
	state := NewSTRIDEProductState()
	now := time.Date(2026, 7, 31, 21, 0, 0, 0, time.UTC)
	request := stridePrivateTemplateRequestForTest()
	created, fresh, err := state.createPrivateTemplateCandidate(request, "member_aj", now)
	if err != nil || !fresh || created.ID != "private-launch_partner" || created.LiveAvailable || !created.ProviderExecutionFenced || !created.ReceiptStatus["closedTemplate"] {
		t.Fatalf("created=%#v fresh=%v err=%v", created, fresh, err)
	}
	replayed, fresh, err := state.createPrivateTemplateCandidate(request, "member_aj", now.Add(time.Minute))
	if err != nil || fresh || workDigest(replayed) != workDigest(created) || stridePrivateTemplateLifecycleLabel(replayed) == "" {
		t.Fatalf("replayed=%#v fresh=%v err=%v", replayed, fresh, err)
	}
	changed := request
	changed.PersonalitySummary = "Different personality"
	if _, _, err := state.createPrivateTemplateCandidate(changed, "member_aj", now); !errors.Is(err, ErrSTRIDEProductConflict) {
		t.Fatalf("changed template error=%v", err)
	}
	snapshot, err := state.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreSTRIDEProductState(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if candidate, found := restored.candidate(created.ID); !found || workDigest(candidate) != workDigest(created) {
		t.Fatalf("private template did not survive deterministic restore: %#v found=%v", candidate, found)
	}
}

func TestSTRIDEPrivateTemplateRejectsMissingOrUnsafeAuthorityShaping(t *testing.T) {
	state := NewSTRIDEProductState()
	now := time.Date(2026, 7, 31, 21, 0, 0, 0, time.UTC)
	base := stridePrivateTemplateRequestForTest()
	tests := []struct {
		name   string
		mutate func(*STRIDEProductPrivateTemplateRequest)
	}{
		{"no capability", func(value *STRIDEProductPrivateTemplateRequest) { value.RequestedCapabilities = nil }},
		{"no access", func(value *STRIDEProductPrivateTemplateRequest) { value.RequiredAccess = nil }},
		{"invalid membership", func(value *STRIDEProductPrivateTemplateRequest) { value.Memberships = []string{"../production"} }},
		{"unbounded concurrency", func(value *STRIDEProductPrivateTemplateRequest) { value.Concurrency = 999 }},
		{"unbounded budget", func(value *STRIDEProductPrivateTemplateRequest) { value.MonthlyBudgetCents = 10_000_001 }},
		{"proactive authority", func(value *STRIDEProductPrivateTemplateRequest) { value.Proactivity = "autonomous" }},
	}
	before, _ := state.Snapshot()
	for _, test := range tests {
		request := base
		test.mutate(&request)
		if _, _, err := state.createPrivateTemplateCandidate(request, "member_aj", now); err == nil {
			t.Fatalf("%s accepted", test.name)
		}
	}
	after, _ := state.Snapshot()
	if before.Digest != after.Digest {
		t.Fatalf("invalid templates mutated catalog before=%s after=%s", before.Digest, after.Digest)
	}
}
