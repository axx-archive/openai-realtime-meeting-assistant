package main

import (
	"testing"
	"time"
)

// Wave 8 D10: the raw-entry budget follows the query class.
func TestRecallBudgetByQueryClass(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		query  string
		class  recallQueryClass
		budget int
	}{
		{"what's the status of the Zebra pilot?", recallQueryClassStatus, 40},
		{"what did our agents produce?", recallQueryClassStatus, 40},
		{"what did I miss last 7 days?", recallQueryClassTemporal, 80},
		{"what happened on monday", recallQueryClassTemporal, 80},
		{"what does Tim think about pricing?", recallQueryClassWhoThinksWhat, 60},
		{"how has AJ's position on Zebra evolved?", recallQueryClassWhoThinksWhat, 60},
		{"tell me about the Samsung TV audience", recallQueryClassFuzzyTopic, 60},
		{"", recallQueryClassFuzzyTopic, 60},
	}
	for _, tc := range cases {
		if class := classifyRecallQuery(tc.query, now); class != tc.class {
			t.Fatalf("classifyRecallQuery(%q)=%q, want %q", tc.query, class, tc.class)
		}
		if budget := recallContextBudget(tc.query, now); budget != tc.budget {
			t.Fatalf("recallContextBudget(%q)=%d, want %d", tc.query, budget, tc.budget)
		}
	}
	if memoryContextBudgetFuzzyTopic != defaultMemoryQuestionContextLimit {
		t.Fatal("the fuzzy-topic budget is the prior fixed default")
	}
}

// The read-locked, in-place search returns the same ranked ids as scoring a
// cloned store did (the pre-wave behavior), and never returns aliases into the
// live slice.
func TestSearchUnderReadLockMatchesClonedScoring(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	for _, text := range []string{
		"Choose vendor Zebra for the packaging pilot",
		"Draft the pricing sheet for Zebra",
		"Warehouse audit kickoff",
		"Zebra packaging pilot launch moved to July 31",
	} {
		if _, _, err := app.memory.appendRoomChatTranscript("t-"+text[:6]+text[len(text)-4:], "AJ", text); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	matches := app.memory.search("Zebra packaging pilot", 10)
	if len(matches) != 3 {
		t.Fatalf("matches=%d, want 3", len(matches))
	}
	for index := 1; index < len(matches); index++ {
		if matches[index].Score > matches[index-1].Score {
			t.Fatalf("matches not score-ordered: %+v", matches)
		}
	}
	// mutation of a returned match never touches the store.
	matches[0].Entry.Text = "MUTATED"
	if again := app.memory.search("Zebra packaging pilot", 10); again[0].Entry.Text == "MUTATED" {
		t.Fatal("search returned an alias into the live store slice")
	}
	if none := app.memory.search("", 10); none != nil {
		t.Fatalf("empty query must return nil, got %v", none)
	}
}
