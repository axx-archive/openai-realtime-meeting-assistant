package main

import (
	"context"
	"testing"
	"time"
)

func resetMeetingDigestPoisonCircuitForTest(t *testing.T) {
	t.Helper()
	meetingDigestPoisonCircuit.Lock()
	oldCircuit := meetingDigestPoisonCircuit.entries
	meetingDigestPoisonCircuit.entries = map[string]meetingDigestPoisonState{}
	meetingDigestPoisonCircuit.Unlock()
	t.Cleanup(func() {
		meetingDigestPoisonCircuit.Lock()
		meetingDigestPoisonCircuit.entries = oldCircuit
		meetingDigestPoisonCircuit.Unlock()
	})
}

// Wave 8 D11: on max_output_truncation the producer retries once with the
// output budget raised by 50%; a good retry lands the digest and the meeting
// record is not stuck.
func TestMeetingDigestTruncationRetriesWithRaisedBudget(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	resetMeetingDigestPoisonCircuitForTest(t)
	app := newIsolatedKanbanBoardApp(t)
	const meetingID = "meeting-20260901-142307-118263679"
	if _, started := app.meetings.startMeeting(officeRoomID, meetingID, time.Now().UTC().Add(-time.Hour), []string{"AJ"}); !started {
		t.Fatal("start meeting record")
	}
	appendDigestTestBrain(t, app, "brain-truncation", meetingID, "## Overview\nA dense window that truncates once.", nil)

	var budgets []int
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		budgets = append(budgets, request.MaxOutputTokens)
		if len(budgets) == 1 {
			return "", &openAIOutputRejection{reason: "max_output_truncation"}
		}
		return cannedMeetingDigestJSON(), nil
	}
	entry, err := app.runAmbientAgentOnce(meetingDigestAgent(), context.Background(), "test-key", responder, 1)
	if err != nil {
		t.Fatalf("digest pass: %v", err)
	}
	if meetingDigestTruncationRetryOutputTokens != 6000 {
		t.Fatalf("retry budget=%d, want 4000 raised by 50%%", meetingDigestTruncationRetryOutputTokens)
	}
	if len(budgets) != 2 || budgets[0] != meetingDigestMaxOutputTokens || budgets[1] != meetingDigestTruncationRetryOutputTokens {
		t.Fatalf("budgets=%v, want [%d %d]", budgets, meetingDigestMaxOutputTokens, meetingDigestTruncationRetryOutputTokens)
	}
	if entry.Kind != meetingMemoryKindMeetingDigest {
		t.Fatalf("no digest landed: %+v", entry)
	}
	if _, ok := app.memory.currentDigest(meetingMemoryKindMeetingDigest, meetingID); !ok {
		t.Fatal("current digest missing after the recovered retry")
	}
	record, ok := app.meetings.recordByID(meetingID)
	if !ok || record.Stuck {
		t.Fatalf("record=%+v ok=%v, want not stuck", record, ok)
	}
}

// Wave 8 D11: when the retry truncates too, the rejection counts toward the
// poison circuit AND the meeting record shows stuck:true with the reason; the
// next accepted digest clears it.
func TestMeetingDigestTruncationTwiceMarksRecordStuckThenClears(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	resetMeetingDigestPoisonCircuitForTest(t)
	app := newIsolatedKanbanBoardApp(t)
	const meetingID = "meeting-20260901-142307-118263680"
	if _, started := app.meetings.startMeeting(officeRoomID, meetingID, time.Now().UTC().Add(-time.Hour), []string{"AJ"}); !started {
		t.Fatal("start meeting record")
	}
	appendDigestTestBrain(t, app, "brain-truncation-twice", meetingID, "## Overview\nA window that truncates every time.", nil)

	calls := 0
	truncateAlways := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		return "", &openAIOutputRejection{reason: "max_output_truncation"}
	}
	_, err := app.runAmbientAgentOnce(meetingDigestAgent(), context.Background(), "test-key", truncateAlways, 1)
	if !isAmbientAgentHoldError(err) {
		t.Fatalf("error=%v, want a cursor-holding rejection", err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d, want exactly one retry", calls)
	}
	record, ok := app.meetings.recordByID(meetingID)
	if !ok || !record.Stuck || record.StuckReason != "max_output_truncation" || record.StuckAt == "" {
		t.Fatalf("record=%+v ok=%v, want stuck on max_output_truncation", record, ok)
	}
	payload := meetingRecordPayload(record, time.Now().UTC())
	if payload["stuck"] != true || payload["stuckReason"] != "max_output_truncation" {
		t.Fatalf("payload=%v, want stuck surfaced for Meeting Records", payload)
	}
	if digests := app.memory.entriesOfKind(meetingMemoryKindMeetingDigest, 0); len(digests) != 0 {
		t.Fatalf("a truncated pass must not land a digest: %d", len(digests))
	}
	if baseline := app.ambientAgentBaselineID(ambientAgentKey(meetingDigestAgentName, officeRoomID)); baseline == "brain-truncation-twice" {
		t.Fatal("rejected output advanced the raw cursor")
	}

	// recovery: the next accepted digest clears the stuck state.
	resetMeetingDigestPoisonCircuitForTest(t)
	good := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		return cannedMeetingDigestJSON(), nil
	}
	if _, err := app.runAmbientAgentOnce(meetingDigestAgent(), context.Background(), "test-key", good, 1); err != nil {
		t.Fatalf("recovery pass: %v", err)
	}
	record, _ = app.meetings.recordByID(meetingID)
	if record.Stuck || record.StuckReason != "" {
		t.Fatalf("record still stuck after recovery: %+v", record)
	}
	if _, ok := app.memory.currentDigest(meetingMemoryKindMeetingDigest, meetingID); !ok {
		t.Fatal("recovery pass did not land the digest")
	}
}
