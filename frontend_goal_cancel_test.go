package main

// The goalcard cancel affordance's frontend contract (packaging OS §2
// "misfire economics", Wave 2 item 8c). These pins hold the one-tap escape:
// the affordance only shows while there is something to stop, the confirm
// step ("stop this run?") stands between the tap and the POST, and the card
// lands on the cancelled terminal optimistically with the server echo (or the
// next sync) correcting it.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForGoalCancel(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// The affordance lives in the goalcard menu with an arm-then-confirm step —
// the first tap never cancels.
func TestIndexGoalCancelConfirmStep(t *testing.T) {
	html := readIndexForGoalCancel(t)
	if !strings.Contains(html, `data-goal-cancel hidden>Cancel run</button>`) {
		t.Error("goalcard menu missing the hidden-by-default Cancel run item")
	}
	body := functionBody(html, "function renderGoalCard(artifact)")
	if body == "" {
		t.Fatal("could not extract renderGoalCard body")
	}
	for _, want := range []string{
		"cancelBtn.dataset.armed !== '1'",
		"'stop this run?'",
		"cancelGoalFromCard(card)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("renderGoalCard body missing cancel confirm marker %q", want)
		}
	}
	// re-opening the menu disarms a half-armed cancel
	if !strings.Contains(body, "goalCancelDisarm(card)") {
		t.Error("renderGoalCard must disarm the cancel confirm when the menu re-opens")
	}
}

// Visibility: cancel exists only for running/gate cards — a terminal card
// has nothing to stop (the server rejects it anyway; don't offer the lever).
func TestIndexGoalCancelVisibility(t *testing.T) {
	html := readIndexForGoalCancel(t)
	body := functionBody(html, "function updateGoalCard(card, artifact)")
	if body == "" {
		t.Fatal("could not extract updateGoalCard body")
	}
	if !strings.Contains(body, "cancelBtn.hidden = !(state === 'running' || state === 'gate')") {
		t.Error("updateGoalCard must show the cancel affordance only for running/gate states")
	}
}

// The POST and the optimistic UI: /assistant/goal/cancel {goalId} (the exact
// handler shape, main.go assistantGoalCancelHandler), with the card moved to
// the cancelled terminal before the response and corrected on failure.
func TestIndexGoalCancelPostAndOptimisticState(t *testing.T) {
	html := readIndexForGoalCancel(t)
	body := functionBody(html, "async function cancelGoalFromCard(card)")
	if body == "" {
		t.Fatal("could not extract cancelGoalFromCard body")
	}
	for _, want := range []string{
		"postAuthJSON('/assistant/goal/cancel', { goalId })",
		// optimistic: needs_attention is the cancelled terminal the engine persists
		"goalStatus: 'needs_attention'",
		"updateGoalCard(card, optimistic)",
		// the server echo settles the card; a failure re-syncs the truth
		"updateGoalCard(card, data.artifact)",
		"goalCardScheduleSync()",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("cancelGoalFromCard body missing %q", want)
		}
	}
}
