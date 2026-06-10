package main

import (
	"testing"
	"time"
)

func TestNegotiationWatchdogAction(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name           string
		firstNonStable time.Time
		offerResent    bool
		want           negotiationAction
	}{
		{"never non-stable", time.Time{}, false, negotiationActionNone},
		{"freshly stuck", now.Add(-time.Second), false, negotiationActionNone},
		{"just under resend threshold", now.Add(-negotiationResendAfter + time.Millisecond), false, negotiationActionNone},
		{"at resend threshold", now.Add(-negotiationResendAfter), false, negotiationActionResend},
		{"past resend threshold already resent", now.Add(-negotiationResendAfter - time.Second), true, negotiationActionNone},
		{"just under close threshold", now.Add(-negotiationCloseAfter + time.Millisecond), true, negotiationActionNone},
		{"at close threshold", now.Add(-negotiationCloseAfter), false, negotiationActionClose},
		{"past close threshold after resend", now.Add(-negotiationCloseAfter - time.Second), true, negotiationActionClose},
	}
	for _, tc := range cases {
		if got := negotiationWatchdogAction(tc.firstNonStable, tc.offerResent, now); got != tc.want {
			t.Errorf("%s: negotiationWatchdogAction = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestNegotiationWatchdogThresholdsAreOrdered(t *testing.T) {
	if negotiationResendAfter >= negotiationCloseAfter {
		t.Fatalf("resend threshold (%s) must come before close threshold (%s)", negotiationResendAfter, negotiationCloseAfter)
	}
}
