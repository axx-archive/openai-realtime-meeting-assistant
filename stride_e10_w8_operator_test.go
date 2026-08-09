package main

import "testing"

func TestStrideE10W8OperatorRequiresSignedDependenciesCampaignAndSoak(t *testing.T) {
	report := BuildStrideE10W8OperatorReport(StrideE10W8OperatorInput{CandidateCommit: "1cf3463cf30938e956e892a5cde5c9009eaad296"})
	if report.Ready || len(report.Gaps) != 7 {
		t.Fatalf("report=%+v", report)
	}
	for _, gap := range report.Gaps {
		if !gap.External || gap.Command == "" {
			t.Fatalf("gap=%+v", gap)
		}
	}
}

func TestStrideE10W8UnsignedOperatorStateCannotActivate(t *testing.T) {
	report := BuildStrideE10W8OperatorReport(StrideE10W8OperatorInput{CandidateCommit: "1cf3463cf30938e956e892a5cde5c9009eaad296", ActivationManifest: &StrideE10W8ActivationManifest{CandidateCommit: "1cf3463cf30938e956e892a5cde5c9009eaad296"}})
	if report.Ready || report.SignedResult.Ready {
		t.Fatal("unsigned operator state became W8 activation authority")
	}
}
