package main

import (
	"os"
	"strings"
	"testing"
)

func TestCustomerViewActivityUsesReplayedWorkRunStatusAndEvents(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, required := range []string{
		"const workRun = ref?.workRun && Array.isArray(ref.workRun.activity) ? ref.workRun : null",
		"const workRunUnavailable = ref?.workRunRequired === true && !workRun",
		"const status = workRunUnavailable ? 'activity_unavailable'",
		"Activity is repairing from durable history. Provider-local status is hidden.",
		"workRun ? `${family} · ${replayedPhase}`",
		"replayedActivity.slice(-12).forEach(activity =>",
		"if (workRun && replayedActivity.length)",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("View activity is missing durable WorkRun replay contract %q", required)
		}
	}
}
