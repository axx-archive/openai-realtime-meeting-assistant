package main

// Recall budget by query class (Wave 8 D10). The context assembly used one
// fixed 60-entry budget for every question; the lanes it fills are shaped by
// the question, so the budget now is too:
//
//   status        40  — the ledger lane leads and the fold IS the answer; a
//                       wide raw band only dilutes it.
//   temporal      80  — "what did I miss this week" spans days and meetings;
//                       the pinned digest lane is exempt, the raw band needs
//                       room for every day in range.
//   fuzzy-topic   60  — the prior default, unchanged.
//   who-thinks-what 60 — positions lead; the raw band grounds the stance.
//
// Classification reuses the existing deterministic detectors (no model call):
// relativeQueryTimeRange for temporal, isPositionQuery / isEvolutionQuery for
// who-thinks-what, isCurrentStateQuery for status; everything else is a
// fuzzy-topic question.

import (
	"strings"
	"time"
)

type recallQueryClass string

const (
	recallQueryClassStatus        recallQueryClass = "status"
	recallQueryClassTemporal      recallQueryClass = "temporal"
	recallQueryClassFuzzyTopic    recallQueryClass = "fuzzy_topic"
	recallQueryClassWhoThinksWhat recallQueryClass = "who_thinks_what"

	memoryContextBudgetStatus        = 40
	memoryContextBudgetTemporal      = 80
	memoryContextBudgetFuzzyTopic    = defaultMemoryQuestionContextLimit
	memoryContextBudgetWhoThinksWhat = 60
)

// classifyRecallQuery routes a question to its budget class. Temporal wins
// first (a dated position question is still a briefing over a range), then
// who-thinks-what, then status, then the fuzzy default.
func classifyRecallQuery(query string, now time.Time) recallQueryClass {
	if strings.TrimSpace(query) == "" {
		return recallQueryClassFuzzyTopic
	}
	if now.IsZero() {
		now = time.Now()
	}
	if _, _, hasTimeRange := relativeQueryTimeRange(query, now); hasTimeRange {
		return recallQueryClassTemporal
	}
	if _, _, isPosition := isPositionQuery(query); isPosition {
		return recallQueryClassWhoThinksWhat
	}
	if _, isEvolution := isEvolutionQuery(query); isEvolution {
		return recallQueryClassWhoThinksWhat
	}
	if isCurrentStateQuery(query, now) || isWorkResultQuery(query) {
		return recallQueryClassStatus
	}
	return recallQueryClassFuzzyTopic
}

// recallContextBudgetForClass maps a class to its raw-entry budget.
func recallContextBudgetForClass(class recallQueryClass) int {
	switch class {
	case recallQueryClassStatus:
		return memoryContextBudgetStatus
	case recallQueryClassTemporal:
		return memoryContextBudgetTemporal
	case recallQueryClassWhoThinksWhat:
		return memoryContextBudgetWhoThinksWhat
	default:
		return memoryContextBudgetFuzzyTopic
	}
}

// recallContextBudget is the per-query budget the context assembly uses.
func recallContextBudget(query string, now time.Time) int {
	return recallContextBudgetForClass(classifyRecallQuery(query, now))
}
