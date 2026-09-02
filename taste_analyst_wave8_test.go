package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// tasteDecisionIDsFromInput pulls the decision entry ids the input offered so
// the fake responder can cite exactly what it was shown.
func tasteDecisionIDsFromInput(input string) []string {
	_, section, found := strings.Cut(input, "# Decisions this teammate made")
	if !found {
		return nil
	}
	section, _, _ = strings.Cut(section, "\n# ")
	ids := []string{}
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if strings.HasPrefix(line, "decision-") {
			ids = append(ids, strings.TrimSpace(strings.SplitN(line, " ", 2)[0]))
		}
	}
	return ids
}

// Wave 8 D3: a person with ZERO signals but two decisions still gets a
// profile pass — the person model is fed by decisions and positions, not only
// UI-reaction signals — and the roster comes from the account store.
func TestTasteAnalystRunsOnDecisionsWithoutSignals(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("TASTE_ANALYST_MIN_SIGNALS", "1")
	app := newIsolatedKanbanBoardApp(t)

	roster := tasteAnalystRoster()
	if len(roster) == 0 {
		t.Fatal("roster is empty")
	}
	hasTim := false
	for _, name := range roster {
		if name == "Tim" {
			hasTim = true
		}
	}
	if !hasTim {
		t.Fatalf("roster derived from accounts must include Tim, got %v", roster)
	}

	for index, statement := range []string{"Tim favors holding the current pricing rather than discounting.", "Tim wants the Zebra pilot to ship before October."} {
		if _, _, err := app.memory.appendDecision("decision-tim-"+string(rune('a'+index)), statement, map[string]string{"status": decisionStatusProposed, "madeBy": "Tim", "context": "stated in the pricing debate"}); err != nil {
			t.Fatalf("append decision: %v", err)
		}
	}
	if signals := app.memory.unconsumedSignalsForActor("Tim", "", 10); len(signals) != 0 {
		t.Fatalf("fixture must have zero signals for Tim, got %d", len(signals))
	}
	if !tasteAnalystWorkDue(app, time.Now().UTC()) {
		t.Fatal("two decisions with zero signals must make a pass due")
	}

	calls := 0
	var shownDecisions []string
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if !strings.Contains(request.Input, "# Teammate\nTim") {
			return "", nil
		}
		calls++
		shownDecisions = tasteDecisionIDsFromInput(request.Input)
		if !strings.Contains(request.Input, "# Recorded positions from the entity ledger") {
			t.Fatalf("input must carry the positions section:\n%s", request.Input)
		}
		bullets := make([]string, 0, len(shownDecisions))
		for _, id := range shownDecisions {
			bullets = append(bullets, "- Holds pricing firm ("+id+")")
		}
		payload, _ := json.Marshal(map[string]any{"profile": "## Recurring objections\n" + strings.Join(bullets, "\n")})
		return string(payload), nil
	}
	if err := app.runTasteAnalystOnce(context.Background(), "test-key", responder); err != nil {
		t.Fatalf("taste pass: %v", err)
	}
	if calls != 1 || len(shownDecisions) != 2 {
		t.Fatalf("calls=%d shownDecisions=%v, want one Tim pass citing both decisions", calls, shownDecisions)
	}
	profile, ok := app.tasteProfileForUser("Tim")
	if !ok {
		t.Fatal("no profile persisted for Tim")
	}
	if profile.Metadata[tasteProfileDecisionCountKey] != "2" || profile.Metadata["signalCount"] != "0" || profile.Metadata[tasteDecisionsConsumedAtKey] == "" {
		t.Fatalf("profile metadata=%v, want decisionCount=2 signalCount=0 and a decision cursor", profile.Metadata)
	}
	if !strings.Contains(profile.Text, "decision-tim-a") {
		t.Fatalf("profile must cite the decision ids: %s", profile.Text)
	}
	// consumed: the same decisions never re-feed.
	if again := app.unconsumedDecisionsForPerson("Tim", tasteDecisionsConsumedAt(profile, true), 10); len(again) != 0 {
		t.Fatalf("decisions re-fed after the pass: %v", memoryEntryIDs(again))
	}
	if err := app.runTasteAnalystOnce(context.Background(), "test-key", responder); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if calls != 1 {
		t.Fatalf("second pass re-billed a consumed window: calls=%d", calls)
	}
}

// decisionMadeBy resolves names, hedged attribution, and emails.
func TestDecisionMadeByResolvesRosterForms(t *testing.T) {
	cases := map[string]bool{"Tim": true, "attributed to Tim": true, "tim@shareability.com": true, "AJ": false, "": false}
	for madeBy, want := range cases {
		entry := meetingMemoryEntry{Kind: meetingMemoryKindDecision, Metadata: map[string]string{"madeBy": madeBy}}
		if got := decisionMadeBy(entry, "Tim"); got != want {
			t.Fatalf("decisionMadeBy(%q, Tim)=%v, want %v", madeBy, got, want)
		}
	}
}
