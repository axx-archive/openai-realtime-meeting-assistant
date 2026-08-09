package main

import (
	"encoding/json"
	"testing"
)

func TestStrideE10W7OperatorReportFailsClosedOnHistoricalNativeAndLiveExternalGaps(t *testing.T) {
	commit := "1cf3463cf30938e956e892a5cde5c9009eaad296"
	ledger := []byte(`{"active":{"releaseCommit":"` + commit + `"}}`)
	health := []byte(`{"release":{"releaseCommit":"` + commit + `","processQualified":true,"externallyAttested":false}}`)
	ready := []byte(`{"checks":{"backup":{"encrypted":false,"offsite":"dormant","restoreVerified":false}}}`)
	eas := []byte(`{"status":"FINISHED","appBuildVersion":"49","gitCommitHash":"d4c827c2adc7e1c6258f843e20fd4f9256c7310b","distribution":"STORE","isForIosSimulator":false}`)
	report := BuildStrideE10W7OperatorReport(StrideE10W7OperatorInput{CandidateCommit: commit, CandidateBuild: 50, ActiveReleaseLedgerJSON: ledger, HealthJSON: health, ReadinessJSON: ready, EASBuildJSON: eas})
	if report.Ready || report.LiveReleaseCommit != commit || report.NativeBuild != "49" || report.NativeCommit == commit {
		t.Fatalf("report=%+v", report)
	}
	for _, gate := range []string{"exact_native_build", "native_distribution", "iphone_physical", "ipad_physical", "accessibility_privacy", "restrictive_turn_webrtc", "encrypted_offsite_restore", "ha_failover", "independent_release_attestation"} {
		found := false
		for _, gap := range report.Gaps {
			if gap.Gate == gate && gap.Command != "" {
				found = true
			}
		}
		if !found {
			t.Errorf("missing executable gap %s: %+v", gate, report.Gaps)
		}
	}
	if body, err := json.Marshal(report); err != nil || len(body) == 0 {
		t.Fatalf("marshal report: %v", err)
	}
}

func TestStrideE10W7OperatorSelfAssertionsCannotProduceAcceptance(t *testing.T) {
	commit := "1cf3463cf30938e956e892a5cde5c9009eaad296"
	eas := []byte(`{"status":"FINISHED","appBuildVersion":"50","gitCommitHash":"` + commit + `","distribution":"STORE","isForIosSimulator":false}`)
	report := BuildStrideE10W7OperatorReport(StrideE10W7OperatorInput{CandidateCommit: commit, CandidateBuild: 50, EASBuildJSON: eas})
	if report.Ready {
		t.Fatal("unsigned operator observations became W7 acceptance")
	}
}
