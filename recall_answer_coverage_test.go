package main

import (
	"context"
	"testing"
	"time"
)

// Wave 8 D7: every generic answer carries a coverage grade from the shared
// recall_coverage.go derivation — unavailable on an empty store, complete when
// fresh evidence covers the question, partial when it does not.
func TestAnswerCoverageStampedOnQueryResult(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	const requester = "aj@shareability.com"

	empty, err := app.resolveAssistantQueryContextForUser(context.Background(), requester, "what did we learn about the Samsung TV audience?", nil)
	if err != nil {
		t.Fatalf("empty-store query: %v", err)
	}
	if empty.coverage != string(RecallCoverageUnavailable) || empty.coverageReason == "" {
		t.Fatalf("empty store coverage=%q reason=%q, want unavailable with a reason", empty.coverage, empty.coverageReason)
	}

	if _, _, err := app.memory.appendTranscript("event-samsung", "item-1", "Alice said the Samsung TV audience skews older."); err != nil {
		t.Fatalf("appendTranscript: %v", err)
	}
	covered, err := app.resolveAssistantQueryContextForUser(context.Background(), requester, "what did we learn about the Samsung TV audience?", nil)
	if err != nil {
		t.Fatalf("covered query: %v", err)
	}
	if covered.coverage != string(RecallCoverageComplete) {
		t.Fatalf("covered coverage=%q reason=%q, want complete", covered.coverage, covered.coverageReason)
	}
	if covered.matches == 0 || covered.contextSize == 0 {
		t.Fatalf("result=%+v, want matches + context", covered)
	}

	// a named range with NO evidence is unavailable; with evidence that
	// covers only part of the range it is honestly partial — and the record
	// validates like every other RecallCoverage.
	if none := app.answerRecallCoverage("what did I miss last 30 days", nil, nil, time.Now()); none.Status != RecallCoverageUnavailable || none.Reason == "" {
		t.Fatalf("ranged empty coverage=%q reason=%q, want unavailable with a reason", none.Status, none.Reason)
	}
	transcript, _ := app.memory.entryByID("event-samsung")
	ranged := app.answerRecallCoverage("what did I miss last 30 days", nil, []meetingMemoryEntry{transcript}, time.Now())
	if ranged.Status != RecallCoveragePartial || ranged.Reason == "" {
		t.Fatalf("ranged partial coverage=%q reason=%q, want partial with a reason", ranged.Status, ranged.Reason)
	}
	if err := ranged.Validate(); err != nil {
		t.Fatalf("coverage record must validate: %v", err)
	}

	// the tool / HTTP payloads expose the same field.
	payload, _, err := app.answerMemoryQuestion(map[string]any{"query": "Samsung TV audience"})
	if err != nil {
		t.Fatalf("answerMemoryQuestion: %v", err)
	}
	if payload["coverage"] != string(RecallCoverageComplete) {
		t.Fatalf("tool payload coverage=%v", payload["coverage"])
	}
	answer, _, err := app.answerAssistantQuery("Samsung TV audience")
	if err != nil {
		t.Fatalf("answerAssistantQuery: %v", err)
	}
	if answer["coverage"] != string(RecallCoverageComplete) {
		t.Fatalf("assistant payload coverage=%v", answer["coverage"])
	}
	// archived evidence downgrades to partial.
	if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindTranscript, "event-samsung", "Alice said the Samsung TV audience skews older.", map[string]string{relevanceMetadataKey: relevanceArchived}); err != nil {
		t.Fatalf("archive: %v", err)
	}
	stale, err := app.resolveAssistantQueryContextForUser(context.Background(), requester, "what did we learn about the Samsung TV audience?", nil)
	if err != nil {
		t.Fatalf("stale query: %v", err)
	}
	if stale.coverage != string(RecallCoveragePartial) {
		t.Fatalf("archived-evidence coverage=%q reason=%q, want partial", stale.coverage, stale.coverageReason)
	}
}
