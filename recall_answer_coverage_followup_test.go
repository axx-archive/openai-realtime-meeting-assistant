package main

import (
	"testing"
	"time"
)

// A question answered entirely from a current digest (the pinned digest
// lane, no raw rows in the lexical band) is complete coverage — the digest is
// the T2 rollup of its raw evidence. Production graded this shape partial
// with an empty reason ("0 sources" being the empty lexical band).
func TestAnswerRecallCoverageDigestOnlyContextIsComplete(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	digest, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, "meeting-golf", `{"meetingId":"meeting-golf","title":"Country Golf","decisions":[{"d":"Country Golf pilot goes ahead in October","by":"AJ","importance":4}]}`, map[string]string{
		"meetingId": "meeting-golf", digestDayMetadataKey: "2026-09-01",
	})
	if err != nil {
		t.Fatalf("seed digest: %v", err)
	}
	now := time.Now().UTC()
	coverage := app.answerRecallCoverage("what is the latest thing we discussed about Country Golf", nil, []meetingMemoryEntry{digest}, now)
	if coverage.Status != RecallCoverageComplete {
		t.Fatalf("digest-only coverage=%s reason=%q, want complete", coverage.Status, coverage.Reason)
	}
	if coverage.Lanes.Digest != RecallLaneActive || coverage.Lanes.Raw != RecallLaneNotRequired {
		t.Fatalf("lanes=%+v, want digest active and raw not required", coverage.Lanes)
	}

	// an archived (superseded) digest is stale evidence and stays partial
	digest.Metadata[relevanceMetadataKey] = relevanceArchived
	stale := app.answerRecallCoverage("what is the latest thing we discussed about Country Golf", nil, []meetingMemoryEntry{digest}, now)
	if stale.Status != RecallCoveragePartial || stale.StaleSources != 1 {
		t.Fatalf("stale digest coverage=%s stale=%d, want partial", stale.Status, stale.StaleSources)
	}
	// no evidence at all is unavailable, never partial
	if empty := app.answerRecallCoverage("anything", nil, nil, now); empty.Status != RecallCoverageUnavailable {
		t.Fatalf("empty coverage=%s, want unavailable", empty.Status)
	}
}
