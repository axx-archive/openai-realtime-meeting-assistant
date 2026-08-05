package main

import (
	"os"
	"strings"
	"testing"
)

func TestGoalCardTerminalStateAndStageLabelsStayTruthful(t *testing.T) {
	body, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		"identify_and_set_goal: 'identify_goal'",
		"decompose_work: 'decompose'",
		"assign_right_agent: 'assign'",
		"coordinate_dependencies: 'coordinate'",
		"review_against_original_goal: 'review_against_goal'",
		"report_only_what_matters: 'report'",
		"const statusIsRunning = ['running', 'queued', 'accepted'].includes(status)",
		"(!statusIsRunning && ['complete', 'completed', 'verified', 'done'].includes(goalStatus))",
		"state === 'complete'",
		"? 'Delivered'",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("goal-card terminal truth contract missing %q", want)
		}
	}
	if strings.Contains(html, "goalStageLabel[Object.keys(goalStageLabel)[currentIndex]]") {
		t.Error("goal-card stage fallback must never index status-only labels as workflow stages")
	}
}
