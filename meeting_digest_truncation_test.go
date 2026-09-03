package main

import (
	"context"
	"strings"
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

// The prefix cursor follows the window the accepted output covers: a group the
// truncation chassis halved stops the prefix at its last FOLDED brain, while an
// unhalved group still consumes its whole prefix.
func TestDigestPassCursorStopsAtTheFoldedWindow(t *testing.T) {
	brain := func(id string) meetingMemoryEntry {
		return meetingMemoryEntry{ID: id, Metadata: map[string]string{"meetingId": "m1"}}
	}
	inputs := []meetingMemoryEntry{brain("b1"), brain("b2"), brain("b3")}
	processed := map[string]bool{"m1": true}
	halved := brainDigestGroup{key: "m1", brains: inputs[:2]}
	if cursor := digestPassCursor(inputs, processed, halved); cursor != "b2" {
		t.Fatalf("halved cursor=%q, want b2 — b3 was never folded", cursor)
	}
	full := brainDigestGroup{key: "m1", brains: inputs}
	if cursor := digestPassCursor(inputs, processed, full); cursor != "b3" {
		t.Fatalf("unhalved cursor=%q, want the whole prefix consumed", cursor)
	}
	other := []meetingMemoryEntry{brain("b1"), {ID: "b2", Metadata: map[string]string{"meetingId": "m2"}}}
	if cursor := digestPassCursor(other, processed, brainDigestGroup{key: "m1", brains: other[:1]}); cursor != "b1" {
		t.Fatalf("cursor=%q, want the prefix to stop before an undigested meeting", cursor)
	}
}

// A brain GROUP that truncates is retried with its OLDEST half, and the digest
// that lands stamps its cursor at the window it ACTUALLY folded — the dropped
// newer half re-feeds next pass instead of being silently consumed by a digest
// that never saw it.
func TestMeetingDigestTruncationHalvesBrainGroupThenLands(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	resetMeetingDigestPoisonCircuitForTest(t)
	app := newIsolatedKanbanBoardApp(t)
	const meetingID = "meeting-20260901-142307-118263681"
	if _, started := app.meetings.startMeeting(officeRoomID, meetingID, time.Now().UTC().Add(-time.Hour), []string{"AJ"}); !started {
		t.Fatal("start meeting record")
	}
	appendDigestTestBrain(t, app, "brain-halve-1", meetingID, "## Overview\nFirst half of a dense sitting.", nil)
	appendDigestTestBrain(t, app, "brain-halve-2", meetingID, "## Overview\nSecond half of a dense sitting.", nil)

	var seen []openAITextRequest
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		seen = append(seen, request)
		if len(seen) == 1 {
			return "", &openAIOutputRejection{reason: ambientTruncationReason}
		}
		return cannedMeetingDigestJSON(), nil
	}
	entry, err := app.runAmbientAgentOnce(meetingDigestAgent(), context.Background(), "test-key", responder, 1)
	if err != nil {
		t.Fatalf("digest pass: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("provider calls=%d, want exactly one retry", len(seen))
	}
	if !strings.Contains(seen[0].Input, "id=brain-halve-2") {
		t.Fatalf("first attempt did not carry the whole group: %q", seen[0].Input)
	}
	if strings.Contains(seen[1].Input, "id=brain-halve-2") || !strings.Contains(seen[1].Input, "id=brain-halve-1") {
		t.Fatalf("retry must keep the OLDEST half: %q", seen[1].Input)
	}
	if seen[1].MaxOutputTokens != meetingDigestMaxOutputTokens {
		t.Fatalf("halved retry budget=%d, want the group budget unchanged at %d", seen[1].MaxOutputTokens, meetingDigestMaxOutputTokens)
	}
	if entry.Kind != meetingMemoryKindMeetingDigest {
		t.Fatalf("no digest landed: %+v", entry)
	}
	if got := entry.Metadata[meetingDigestCursorMetadataKey]; got != "brain-halve-1" {
		t.Fatalf("cursor=%q, want the halved window's last brain; the dropped half must not be consumed", got)
	}
	if got := entry.Metadata["brainCount"]; got != "1" {
		t.Fatalf("brainCount=%q, want the folded window's count", got)
	}
	if record, ok := app.meetings.recordByID(meetingID); !ok || record.Stuck {
		t.Fatalf("record=%+v ok=%v, want not stuck after a recovered retry", record, ok)
	}
	if deadLetters := app.memory.entriesOfKind(meetingMemoryKindDeadLetter, 0); len(deadLetters) != 0 {
		t.Fatalf("a recovered truncation left dead letters: %+v", deadLetters)
	}

	// the dropped newer half re-feeds on the next pass
	next, err := app.runAmbientAgentOnce(meetingDigestAgent(), context.Background(), "test-key", responder, 1)
	if err != nil {
		t.Fatalf("next pass: %v", err)
	}
	if got := next.Metadata[meetingDigestCursorMetadataKey]; got != "brain-halve-2" {
		t.Fatalf("next cursor=%q, want brain-halve-2 folded", got)
	}
	if last := seen[len(seen)-1]; strings.Contains(last.Input, "id=brain-halve-1") || !strings.Contains(last.Input, "id=brain-halve-2") {
		t.Fatalf("next window must re-feed only the dropped half: %q", last.Input)
	}
}

// Wave 8 D11, re-pinned 2026-09-02: when the halved retry truncates too the
// head brain is SKIPPED through the shared chassis — durable baseline advanced,
// dead-letter tombstone stamped max_output_truncation, pass lands clean — so a
// truncation never accrues toward ambientProviderMaxWindowAttempts and never
// opens the restart-required provider circuit (the gen-248 incident class). The
// Meeting Record still shows stuck:true as the surfaced UI state until the next
// accepted digest clears it.
func TestMeetingDigestTruncationTwiceSkipsBrainWithoutOpeningCircuit(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	resetMeetingDigestPoisonCircuitForTest(t)
	resetCapabilityRuntimeForTest(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	const meetingID = "meeting-20260901-142307-118263680"
	if _, started := app.meetings.startMeeting(officeRoomID, meetingID, time.Now().UTC().Add(-time.Hour), []string{"AJ"}); !started {
		t.Fatal("start meeting record")
	}
	appendDigestTestBrain(t, app, "brain-truncation-twice", meetingID, "## Overview\nA window that truncates every time.", nil)

	agent := meetingDigestAgent()
	key := ambientAgentKey(meetingDigestAgentName, officeRoomID)
	calls := 0
	truncateAlways := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		return "", &openAIOutputRejection{reason: ambientTruncationReason}
	}
	entry, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "test-key", truncateAlways, 1, officeRoomID)
	if err != nil {
		t.Fatalf("guarded pass returned %v, want a clean skip", err)
	}
	if entry.ID != "" || calls != 2 {
		t.Fatalf("entry=%+v calls=%d, want no artifact after exactly two attempts", entry, calls)
	}
	app.mu.Lock()
	failure := app.agentFailures[key]
	app.mu.Unlock()
	if failure != nil {
		t.Fatalf("truncation skip recorded a circuit failure: %+v", failure)
	}
	if baseline := app.ambientAgentBaselineID(key); baseline != "brain-truncation-twice" {
		t.Fatalf("baseline=%q, want advanced past the stuck brain", baseline)
	}
	checkpoint, ok, err := app.ambientScopeCheckpoint(key)
	if err != nil || !ok || checkpoint.BaselineID != "brain-truncation-twice" || checkpoint.WindowID != "" {
		t.Fatalf("durable checkpoint=%+v ok=%v err=%v, want the skipped brain baselined with no held window", checkpoint, ok, err)
	}
	deadLetters := app.memory.entriesOfKind(meetingMemoryKindDeadLetter, 0)
	if len(deadLetters) != 1 || deadLetters[0].Metadata[deadLetterReasonMetadataKey] != ambientTruncationReason || deadLetters[0].Metadata[deadLetterAgentMetadataKey] != meetingDigestAgentName {
		t.Fatalf("dead letters=%+v, want one stuck-input tombstone stamped %s", deadLetters, ambientTruncationReason)
	}
	health := ambientWorkerCapabilitySnapshot(agent, time.Now().UTC(), providerOpenAI, true)
	if health["stuckInputs"] != 1 || health["deadLetter"] != 1 {
		t.Fatalf("snapshot stuckInputs=%v deadLetter=%v, want 1/1", health["stuckInputs"], health["deadLetter"])
	}
	if circuit, _ := health["circuit"].(string); circuit != "" && circuit != "closed" {
		t.Fatalf("snapshot circuit=%q after a truncation skip, want closed", circuit)
	}
	record, ok := app.meetings.recordByID(meetingID)
	if !ok || !record.Stuck || record.StuckReason != ambientTruncationReason || record.StuckAt == "" {
		t.Fatalf("record=%+v ok=%v, want stuck on max_output_truncation", record, ok)
	}
	payload := meetingRecordPayload(record, time.Now().UTC())
	if payload["stuck"] != true || payload["stuckReason"] != ambientTruncationReason {
		t.Fatalf("payload=%v, want stuck surfaced for Meeting Records", payload)
	}
	if digests := app.memory.entriesOfKind(meetingMemoryKindMeetingDigest, 0); len(digests) != 0 {
		t.Fatalf("a truncated pass must not land a digest: %d", len(digests))
	}

	// recovery: the skipped brain never re-feeds, and the next accepted digest
	// clears the stuck badge.
	resetMeetingDigestPoisonCircuitForTest(t)
	appendDigestTestBrain(t, app, "brain-after-skip", meetingID, "## Overview\nThe sitting continued.", nil)
	var recovered []openAITextRequest
	good := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		recovered = append(recovered, request)
		return cannedMeetingDigestJSON(), nil
	}
	if _, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "test-key", good, 1, officeRoomID); err != nil {
		t.Fatalf("recovery pass: %v", err)
	}
	if len(recovered) != 1 || strings.Contains(recovered[0].Input, "id=brain-truncation-twice") {
		t.Fatalf("skipped brain re-fed: %d call(s) %+v", len(recovered), recovered)
	}
	record, _ = app.meetings.recordByID(meetingID)
	if record.Stuck || record.StuckReason != "" {
		t.Fatalf("record still stuck after recovery: %+v", record)
	}
	if _, ok := app.memory.currentDigest(meetingMemoryKindMeetingDigest, meetingID); !ok {
		t.Fatal("recovery pass did not land the digest")
	}
}
