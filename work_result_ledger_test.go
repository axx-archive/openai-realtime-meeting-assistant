package main

import (
	"strings"
	"testing"
	"time"
)

// Wave 8 D9: a terminal artifact mints a work_result ledger event (artifact,
// title, forEmail, originThreadId) that folds into the ledger and answers
// "what did our agents produce" deterministically.
func TestWorkResultReachesLedger(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	artifact, appended, err := app.memory.appendOSArtifact("artifact-work-1", "# Samsung TV Plus research\nFindings...", map[string]string{
		"title": "Samsung TV Plus research", "status": "complete", "threadStatus": "complete", "requestedBy": "aj@shareability.com", "mode": "research",
	})
	if err != nil || !appended {
		t.Fatalf("artifact: appended=%v err=%v", appended, err)
	}
	thread := scoutAgentThread{ID: "thread-work-1", Mode: "research", Query: "Research Samsung TV Plus", Status: "complete", Artifact: artifact}

	event, recorded, err := app.appendWorkResultLedgerEvent(thread, artifact, "complete")
	if err != nil || !recorded {
		t.Fatalf("appendWorkResultLedgerEvent: recorded=%v err=%v", recorded, err)
	}
	if event.Kind != meetingMemoryKindLedgerEvent || event.Metadata["kind"] != ledgerEntityWorkResult || event.Metadata["artifactId"] != "artifact-work-1" ||
		event.Metadata["forEmail"] != "aj@shareability.com" || event.Metadata["originThreadId"] != "thread-work-1" || event.Metadata["title"] != "Samsung TV Plus research" {
		t.Fatalf("event=%+v", event)
	}
	record, ok := app.memory.ledgerState()["ldg-work_result-artifact-work-1"]
	if !ok || record.Entity != ledgerEntityWorkResult || record.Owner != "aj@shareability.com" || record.Anchors[0] != "artifact-work-1" || record.Status != "complete" {
		t.Fatalf("folded record=%+v ok=%v", record, ok)
	}
	// never a live decision/action item.
	view := app.ledgerCurrentStateView(10)
	if len(view.Decisions)+len(view.ActionItems)+len(view.Topics)+len(view.OpenQuestions) != 0 {
		t.Fatalf("work result leaked into the current-state view: %+v", view)
	}
	// idempotent per (artifact, status).
	if _, again, err := app.appendWorkResultLedgerEvent(thread, artifact, "complete"); err != nil || again {
		t.Fatalf("duplicate terminal twin recorded=%v err=%v", again, err)
	}

	answer, ok := app.ledgerStatusAnswer("what did our agents produce this week?")
	if !ok || !strings.Contains(answer, "Samsung TV Plus research") || !strings.Contains(answer, "for=aj@shareability.com") || !strings.Contains(answer, "artifact=artifact-work-1") {
		t.Fatalf("work-result answer=%q ok=%v", answer, ok)
	}
	lane := app.ledgerContextLane("what did our agents produce?", time.Now())
	if len(lane) != 1 || lane[0].Kind != memoryContextKindLedgerState || !strings.Contains(lane[0].Text, "thread=thread-work-1") {
		t.Fatalf("work-result lane=%+v", lane)
	}
	if lane := app.ledgerContextLane("status of the packaging pilot", time.Now()); len(lane) != 0 {
		t.Fatalf("a status question must not take the work-result lane: %+v", lane)
	}
	// the run-log funnel calls the same seam.
	app.appendAgentRunLogEntry(scoutAgentThread{ID: "thread-work-2", Mode: "research", Query: "Second run"}, meetingMemoryEntry{ID: "artifact-work-2", Kind: meetingMemoryKindOSArtifact, Metadata: map[string]string{"title": "Second deliverable", "requestedBy": "tim@shareability.com"}}, "error", "provider failed")
	if second, ok := app.memory.ledgerState()["ldg-work_result-artifact-work-2"]; !ok || second.Status != "error" || second.Owner != "tim@shareability.com" {
		t.Fatalf("appendAgentRunLogEntry did not mint the work_result event: %+v ok=%v", second, ok)
	}
}
