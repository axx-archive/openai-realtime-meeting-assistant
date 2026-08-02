package main

type STRIDEProductAgentSemanticDiff struct {
	PersonalityChanged     bool     `json:"personalityChanged"`
	MembershipsAdded       []string `json:"membershipsAdded"`
	MembershipsRemoved     []string `json:"membershipsRemoved"`
	PermissionChanged      bool     `json:"permissionChanged"`
	PerRunBudgetDeltaCents int64    `json:"perRunBudgetDeltaCents"`
	DailyBudgetDeltaCents  int64    `json:"dailyBudgetDeltaCents"`
	CostChanged            bool     `json:"costChanged"`
	ProactivityChanged     bool     `json:"proactivityChanged"`
	RuntimeChanged         bool     `json:"runtimeChanged"`
	RuntimeSummary         string   `json:"runtimeSummary"`
	MigrationSummary       string   `json:"migrationSummary"`
	Digest                 string   `json:"digest"`
}

func newSTRIDEProductAgentSemanticDiff(previous, candidate STRIDEProductAgentConfig) (STRIDEProductAgentSemanticDiff, error) {
	previous, previousErr := normalizeSTRIDEProductConfig(previous)
	candidate, candidateErr := normalizeSTRIDEProductConfig(candidate)
	if previousErr != nil || candidateErr != nil {
		return STRIDEProductAgentSemanticDiff{}, ErrSTRIDEProductInvalid
	}
	previousMemberships := make(map[string]bool, len(previous.Memberships))
	candidateMemberships := make(map[string]bool, len(candidate.Memberships))
	for _, membership := range previous.Memberships {
		previousMemberships[membership] = true
	}
	for _, membership := range candidate.Memberships {
		candidateMemberships[membership] = true
	}
	diff := STRIDEProductAgentSemanticDiff{
		PersonalityChanged:     previous.PersonalityNotes != candidate.PersonalityNotes,
		PerRunBudgetDeltaCents: candidate.PerRunBudgetCents - previous.PerRunBudgetCents,
		DailyBudgetDeltaCents:  candidate.DailyBudgetCents - previous.DailyBudgetCents,
		ProactivityChanged:     previous.Proactivity != candidate.Proactivity,
		RuntimeChanged:         false,
		RuntimeSummary:         "No runtime or model change; provider execution remains fenced.",
		MigrationSummary:       "No data migration required.",
	}
	for _, membership := range candidate.Memberships {
		if !previousMemberships[membership] {
			diff.MembershipsAdded = append(diff.MembershipsAdded, membership)
		}
	}
	for _, membership := range previous.Memberships {
		if !candidateMemberships[membership] {
			diff.MembershipsRemoved = append(diff.MembershipsRemoved, membership)
		}
	}
	diff.PermissionChanged = len(diff.MembershipsAdded) > 0 || len(diff.MembershipsRemoved) > 0
	diff.CostChanged = diff.PerRunBudgetDeltaCents != 0 || diff.DailyBudgetDeltaCents != 0
	digest, err := STRIDEContractDigest(struct {
		PersonalityChanged     bool
		MembershipsAdded       []string
		MembershipsRemoved     []string
		PermissionChanged      bool
		PerRunBudgetDeltaCents int64
		DailyBudgetDeltaCents  int64
		CostChanged            bool
		ProactivityChanged     bool
		RuntimeChanged         bool
		RuntimeSummary         string
		MigrationSummary       string
	}{diff.PersonalityChanged, diff.MembershipsAdded, diff.MembershipsRemoved, diff.PermissionChanged, diff.PerRunBudgetDeltaCents, diff.DailyBudgetDeltaCents, diff.CostChanged, diff.ProactivityChanged, diff.RuntimeChanged, diff.RuntimeSummary, diff.MigrationSummary})
	if err != nil {
		return STRIDEProductAgentSemanticDiff{}, ErrSTRIDEProductInvalid
	}
	diff.Digest = digest
	return diff, nil
}

func validSTRIDEProductAgentSemanticDiff(diff STRIDEProductAgentSemanticDiff, previous, candidate STRIDEProductAgentConfig) bool {
	expected, err := newSTRIDEProductAgentSemanticDiff(previous, candidate)
	return err == nil && workDigest(expected) == workDigest(diff)
}
