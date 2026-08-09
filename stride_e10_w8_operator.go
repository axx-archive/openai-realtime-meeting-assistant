package main

import "sort"

// BuildStrideE10W8OperatorReport is deliberately non-activating. It identifies
// the exact signed dependencies and real production campaign still required;
// only ValidateStrideE10W8Activation can make Ready true.
type StrideE10W8OperatorInput struct {
	CandidateCommit    string
	ActivationManifest *StrideE10W8ActivationManifest
}

type StrideE10W8OperatorReport struct {
	Ready           bool                       `json:"ready"`
	CandidateCommit string                     `json:"candidateCommit"`
	Gaps            []StrideE10W7OperatorGap   `json:"gaps"`
	SignedResult    StrideE10W8PreflightResult `json:"signedResult"`
}

func BuildStrideE10W8OperatorReport(input StrideE10W8OperatorInput) StrideE10W8OperatorReport {
	report := StrideE10W8OperatorReport{CandidateCommit: input.CandidateCommit}
	if input.ActivationManifest != nil {
		report.SignedResult = ValidateStrideE10W8Activation(*input.ActivationManifest)
		if report.SignedResult.Ready && input.ActivationManifest.CandidateCommit == input.CandidateCommit {
			report.Ready = true
			return report
		}
	}
	commands := map[string]string{
		"w7_result":          "node scripts/e10-w7-readonly-preflight.mjs --policy <absolute-root-signed-w7-policy.json> --result <absolute-signed-w7-result.json>",
		"aj_w5_decision":     "<AJ> sign completed or explicitly-deferred W5 decision for the exact release",
		"w6_qualification":   "<W6-independent-reviewers> sign the exact five-profile/two-reviewer qualification result",
		"rollback_readiness": "<release-owner> produce independently signed exact-release rollback-readiness receipt",
		"ordered_cohorts":    "<explicit AJ activation authority required> activate one receipted cohort at a time and exercise its independent kill switch",
		"production_soak":    "after final cohort route freeze, observe unchanged exact release for >=24h and >=10 distinct sittings",
		"final_rollback":     "<independent observer> bind the final rollback drill to the production-soak receipt",
	}
	for _, gate := range []string{"w7_result", "aj_w5_decision", "w6_qualification", "rollback_readiness", "ordered_cohorts", "production_soak", "final_rollback"} {
		report.Gaps = append(report.Gaps, StrideE10W7OperatorGap{Gate: gate, State: "external_blocked", Reason: "no valid immutable-root signed W8 activation manifest proves this gate", Command: commands[gate], External: true})
	}
	if input.ActivationManifest != nil {
		for _, reason := range report.SignedResult.Reasons {
			report.Gaps = append(report.Gaps, StrideE10W7OperatorGap{Gate: "signed_manifest", State: "invalid", Reason: reason, Command: "node scripts/e10-w8-readonly-preflight.mjs --policy <absolute-root-signed-w8-policy.json> --result <absolute-signed-w8-result.json>", External: true})
		}
	}
	sort.Slice(report.Gaps, func(i, j int) bool { return report.Gaps[i].Gate < report.Gaps[j].Gate })
	return report
}
