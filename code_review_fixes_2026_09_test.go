package main

// Focused regression tests for the 2026-09 code-review findings (provider
// breaker M1/M2, inspector fence M3, taste cursor M4, coverage hot path M6,
// channel-digest breaker seat M7, digest invalidation M8, remember fence M9,
// digest fence M10, room-voice tool list M11, ingestion single scan M5,
// capability status B2/B3, mention directory B6).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

/* ---------- M1 / M2: provider breaker ---------- */

// M1: a breaker opened by an account-wide quota failure must NOT steer every
// call on the seat into the twin dial for the whole cooldown — the twin bills
// the same account. It fails fast with the honest open error; a breaker opened
// by a replayable class still steers to the twin.
func TestProviderBreakerOpenOnQuotaFailsFastInsteadOfReplaying(t *testing.T) {
	resetProviderBreakersForTest(t)
	providerBreakers.admit(providerOpenAI, seatChat)
	providerBreakers.recordPrimaryFailure(providerOpenAI, seatChat, providerFailureClassQuota, false)
	calls := 0
	resilient := withProviderResilience(func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		return "answer on " + request.Model, nil
	})
	_, err := resilient(context.Background(), "key", openAITextRequest{Model: defaultScoutChatModel, Seat: seatChat})
	var openErr *providerBreakerOpenError
	if !errors.As(err, &openErr) || !isProviderInvocationFailure(err) {
		t.Fatalf("quota-open seat err=%v, want the fail-closed open error", err)
	}
	if openErr.Reason != providerFailureClassQuota {
		t.Fatalf("open error reason=%q, want the honest quota class", openErr.Reason)
	}
	if calls != 0 {
		t.Fatalf("calls=%d, want no doomed twin replay while open on quota", calls)
	}
	if snapshot := providerBreakers.snapshot(providerOpenAI, seatChat); snapshot.State != providerBreakerOpen || snapshot.FallbackUsed {
		t.Fatalf("breaker=%+v, want still open with no fallback recorded", snapshot)
	}

	// A replayable open class (server errors) keeps the steer-to-twin path.
	providerBreakers.reset()
	for i := 0; i < providerBreakerFailureThreshold; i++ {
		providerBreakers.admit(providerOpenAI, seatChat)
		providerBreakers.recordPrimaryFailure(providerOpenAI, seatChat, providerFailureClassServerError, false)
	}
	var replayed []bool
	resilient = withProviderResilience(func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		replayed = append(replayed, request.FallbackReplay)
		return "answer on " + request.Model, nil
	})
	output, err := resilient(context.Background(), "key", openAITextRequest{Model: defaultScoutChatModel, Seat: seatChat})
	if err != nil || output != "answer on gpt-5.6-luna" || len(replayed) != 1 || !replayed[0] {
		t.Fatalf("server-error-open seat output=%q err=%v replayed=%v, want one twin replay", output, err, replayed)
	}
	if !providerBreakerOpenReplayable("probe_failed:"+providerFailureClassTimeout) || providerBreakerOpenReplayable("probe_failed:"+providerFailureClassQuota) || !providerBreakerOpenReplayable("") {
		t.Fatal("open-reason replayability must follow the wire class behind a probe_failed: prefix and default to replay when unstamped")
	}
}

// M2: a success whose admission predates the open (a long in-flight call
// admitted while closed) must not re-close the breaker or zero its failures;
// only a success admitted under the current epoch (the half-open probe) may.
func TestProviderBreakerStaleSuccessCannotRecloseOpenBreaker(t *testing.T) {
	clock := resetProviderBreakersForTest(t)
	stale := providerBreakers.admit(providerOpenAI, seatBrain)
	if !stale.allowPrimary || stale.epoch != 0 {
		t.Fatalf("closed admission=%+v", stale)
	}
	for i := 0; i < providerBreakerFailureThreshold; i++ {
		providerBreakers.admit(providerOpenAI, seatBrain)
		providerBreakers.recordPrimaryFailure(providerOpenAI, seatBrain, providerFailureClassServerError, false)
	}
	if snapshot := providerBreakers.snapshot(providerOpenAI, seatBrain); snapshot.State != providerBreakerOpen || snapshot.Epoch != 1 {
		t.Fatalf("breaker after failures=%+v, want open at epoch 1", snapshot)
	}
	if providerBreakers.recordPrimarySuccess(providerOpenAI, seatBrain, false, stale.epoch) {
		t.Fatal("a stale-epoch success was applied")
	}
	snapshot := providerBreakers.snapshot(providerOpenAI, seatBrain)
	if snapshot.State != providerBreakerOpen || snapshot.ConsecutiveFailures != providerBreakerFailureThreshold || snapshot.StaleSuccesses != 1 {
		t.Fatalf("breaker after stale success=%+v, want still open with failures intact and one stale success counted", snapshot)
	}

	// The same through the wrapper: the primary call opens the breaker while
	// it is in flight, then returns success — the seat stays open.
	providerBreakers.reset()
	resilient := withProviderResilience(func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if !request.FallbackReplay {
			for i := 0; i < providerBreakerFailureThreshold; i++ {
				providerBreakers.recordPrimaryFailure(providerOpenAI, seatBrain, providerFailureClassServerError, false)
			}
		}
		return "late success", nil
	})
	if output, err := resilient(context.Background(), "key", openAITextRequest{Model: defaultMeetingBrainModel, Seat: seatBrain}); err != nil || output != "late success" {
		t.Fatalf("in-flight output=%q err=%v", output, err)
	}
	if snapshot := providerBreakers.snapshot(providerOpenAI, seatBrain); snapshot.State != providerBreakerOpen || snapshot.StaleSuccesses != 1 {
		t.Fatalf("breaker after in-flight late success=%+v, want still open", snapshot)
	}

	// The half-open probe (current epoch) still closes it.
	clock.Advance(providerBreakerCooldown)
	probe := providerBreakers.admit(providerOpenAI, seatBrain)
	if !probe.probe || probe.epoch != 1 {
		t.Fatalf("probe admission=%+v", probe)
	}
	if !providerBreakers.recordPrimarySuccess(providerOpenAI, seatBrain, true, probe.epoch) {
		t.Fatal("the probe success was not applied")
	}
	if snapshot := providerBreakers.snapshot(providerOpenAI, seatBrain); snapshot.State != providerBreakerClosed || snapshot.ConsecutiveFailures != 0 {
		t.Fatalf("breaker after probe=%+v, want closed", snapshot)
	}
}

/* ---------- M3: inspector events inherit the source fence ---------- */

// M3: closing a PRIVATE work_result through the inspector must not republish
// its title org-wide — the close event inherits the record's fence, so another
// member's fold never sees the record.
func TestMemoryInspectorClosePrivateWorkResultStaysPrivate(t *testing.T) {
	app := setupInspectorTest(t)
	aj := accountStore().findUser("aj@shareability.com")
	tim := accountStore().findUser("tim@shareability.com")
	const title = "Private deck WALRUSDECK9182"
	artifact, appended, err := app.createOSArtifactWithMetadata("workflow", title, "deck body", "AJ", map[string]string{
		"title": title, "visibility": "private", "ownerEmail": aj.Email, "requestedBy": aj.Email,
	})
	if err != nil || !appended {
		t.Fatalf("artifact: appended=%v err=%v", appended, err)
	}
	if _, recorded, err := app.appendWorkResultLedgerEvent(scoutAgentThread{ID: "thread-private-deck", Mode: "workflow", Query: "make the deck"}, artifact, "complete"); err != nil || !recorded {
		t.Fatalf("work result event: recorded=%v err=%v", recorded, err)
	}
	recordID := "ldg-work_result-" + artifact.ID
	ctx := context.Background()
	if _, ok := app.scopedRecallApp(ctx, recallPrincipalForUser(aj)).memory.ledgerState()[recordID]; !ok {
		t.Fatal("owner must see the private work result in their fold")
	}
	if _, leaked := app.scopedRecallApp(ctx, recallPrincipalForUser(tim)).memory.ledgerState()[recordID]; leaked {
		t.Fatal("fixture: the private work result leaked before any inspector action")
	}

	rec := inspectActionAs(t, aj.Email, `{"id":"`+recordID+`","action":"close"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("close status=%d body=%s", rec.Code, rec.Body.String())
	}
	events := app.memory.entriesOfKind(meetingMemoryKindLedgerEvent, 0)
	closeEvent := events[len(events)-1]
	if closeEvent.Metadata["op"] != ledgerOpClose || closeEvent.Metadata["recordId"] != recordID {
		t.Fatalf("newest event=%v, want the close for %s", closeEvent.Metadata, recordID)
	}
	if closeEvent.Metadata["visibility"] != "private" || closeEvent.Metadata["ownerEmail"] != aj.Email {
		t.Fatalf("PRIVACY LEAK: close event fence=%v, want private + owner", closeEvent.Metadata)
	}
	if _, leaked := app.scopedRecallApp(ctx, recallPrincipalForUser(tim)).memory.ledgerState()[recordID]; leaked {
		t.Fatal("PRIVACY LEAK: another member's fold shows the private work result after close")
	}
	closed, ok := app.scopedRecallApp(ctx, recallPrincipalForUser(aj)).memory.ledgerState()[recordID]
	if !ok || closed.Status != ledgerStatusClosed || closed.Title != title {
		t.Fatalf("owner fold after close=%+v ok=%v, want closed", closed, ok)
	}

	// The note branch inherits the entry's fence the same way.
	note, _, err := app.rememberNote(aj, rememberNoteRequest{Text: "private canary yak4471", Private: true}, "s")
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if rec := inspectActionAs(t, aj.Email, `{"id":"`+note.ID+`","action":"correct","correction":"private canary yak4471 corrected"}`); rec.Code != http.StatusOK {
		t.Fatalf("correct status=%d body=%s", rec.Code, rec.Body.String())
	}
	events = app.memory.entriesOfKind(meetingMemoryKindLedgerEvent, 0)
	if last := events[len(events)-1]; last.Metadata["targetKind"] != meetingMemoryKindNote || last.Metadata["visibility"] != "private" || last.Metadata["ownerEmail"] != aj.Email {
		t.Fatalf("note correction event fence=%v, want private + owner", last.Metadata)
	}
}

/* ---------- M4: taste decision cursor ---------- */

// M4: the decision cursor moves to the newest CONSUMED decision, not to "now":
// with 2×maxBatch decisions the second pass consumes the rest instead of
// skipping them forever.
func TestTasteAnalystDecisionCursorAdvancesOnlyThroughConsumedBatch(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("TASTE_ANALYST_MIN_SIGNALS", "1")
	t.Setenv("TASTE_ANALYST_MAX_SIGNALS", "2")
	app := newIsolatedKanbanBoardApp(t)

	ids := []string{"decision-cursor-a", "decision-cursor-b", "decision-cursor-c", "decision-cursor-d"}
	entries := map[string]meetingMemoryEntry{}
	for index, id := range ids {
		entry, _, err := app.memory.appendDecision(id, "Tim position number "+string(rune('a'+index)), map[string]string{"status": decisionStatusProposed, "madeBy": "Tim"})
		if err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
		entries[id] = entry
		time.Sleep(2 * time.Millisecond)
	}
	var shown [][]string
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if !strings.Contains(request.Input, "# Teammate\nTim") {
			return "", nil
		}
		batch := tasteDecisionIDsFromInput(request.Input)
		shown = append(shown, batch)
		bullets := make([]string, 0, len(batch))
		for _, id := range batch {
			bullets = append(bullets, "- Holds the line ("+id+")")
		}
		payload, _ := json.Marshal(map[string]any{"profile": "## Recurring objections\n" + strings.Join(bullets, "\n")})
		return string(payload), nil
	}
	if err := app.runTasteAnalystOnce(context.Background(), "test-key", responder); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if len(shown) != 1 || strings.Join(shown[0], ",") != "decision-cursor-a,decision-cursor-b" {
		t.Fatalf("first pass shown=%v, want the first batch of two", shown)
	}
	profile, ok := app.tasteProfileForUser("Tim")
	if !ok {
		t.Fatal("no profile after the first pass")
	}
	if cursor := tasteDecisionsConsumedAt(profile, true); !cursor.Equal(entries["decision-cursor-b"].CreatedAt) {
		t.Fatalf("decision cursor=%s, want the newest consumed decision's CreatedAt %s", cursor.Format(time.RFC3339Nano), entries["decision-cursor-b"].CreatedAt.Format(time.RFC3339Nano))
	}
	if err := app.runTasteAnalystOnce(context.Background(), "test-key", responder); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if len(shown) != 2 || strings.Join(shown[1], ",") != "decision-cursor-c,decision-cursor-d" {
		t.Fatalf("second pass shown=%v, want the remaining batch (decisions beyond the first batch must not be skipped)", shown)
	}
	if err := app.runTasteAnalystOnce(context.Background(), "test-key", responder); err != nil {
		t.Fatalf("third pass: %v", err)
	}
	if len(shown) != 2 {
		t.Fatalf("third pass re-billed consumed decisions: shown=%v", shown)
	}
}

/* ---------- M6: coverage hot path ---------- */

// M6: the visible-span start comes from a read-locked timestamp scan that
// agrees with the snapshot lane's visibility rule, without cloning the store.
func TestRecallCoverageEarliestVisibleCreatedAtSkipsHiddenWithoutClone(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	if !app.memory.earliestVisibleCreatedAt().IsZero() {
		t.Fatal("empty store must report a zero earliest timestamp")
	}
	for index, id := range []string{"cov-1", "cov-2", "cov-3"} {
		if _, _, err := app.memory.appendTranscript(id, "item-"+id, "Coverage span transcript number "+string(rune('1'+index))); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	oldest, _ := app.memory.entryByID("cov-1")
	if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindTranscript, "cov-1", oldest.Text, map[string]string{relevanceMetadataKey: relevanceExpired}); err != nil {
		t.Fatalf("expire oldest: %v", err)
	}
	want := time.Time{}
	for _, entry := range app.memory.snapshot(0) {
		if want.IsZero() || entry.CreatedAt.Before(want) {
			want = entry.CreatedAt
		}
	}
	got := app.memory.earliestVisibleCreatedAt()
	if got.IsZero() || !got.Equal(want) {
		t.Fatalf("earliestVisibleCreatedAt=%s, want the snapshot lane's oldest visible %s", got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
	if !got.After(oldest.CreatedAt) {
		t.Fatalf("hidden oldest entry leaked into the visible span: got=%s hidden=%s", got.Format(time.RFC3339Nano), oldest.CreatedAt.Format(time.RFC3339Nano))
	}
	coverage := app.answerRecallCoverage("what did we say?", nil, nil, time.Now())
	if !coverage.RequestedStartUTC.Equal(got.UTC()) {
		t.Fatalf("coverage requested start=%s, want the visible span start %s", coverage.RequestedStartUTC.Format(time.RFC3339Nano), got.UTC().Format(time.RFC3339Nano))
	}
}

/* ---------- M7 + B3: channel digest worker rides the meeting-digest seat breaker ---------- */

// M7/B3: the channel digest worker bills seatMeetingDigest, so an open breaker
// on that seat pauses it, its capability lane says paused_by_breaker, and the
// rollup counts the parked worker as degraded.
func TestAmbientChannelDigestWorkerPausesBehindMeetingDigestSeatBreaker(t *testing.T) {
	clock := resetProviderBreakersForTest(t)
	resetCapabilityRuntimeForTest(t)
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("ANTHROPIC_API_KEY", "")
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	seedPublicChannelMessages(t, app, "channel-breaker-pause", "General", "one", "two", "three")

	agent := channelDigestAgent()
	if ambientAgentProviderSeat(agent) != seatMeetingDigest {
		t.Fatalf("channel digest seat=%q, want %s", ambientAgentProviderSeat(agent), seatMeetingDigest)
	}
	calls := 0
	responder := withProviderResilience(func(context.Context, string, openAITextRequest) (string, error) {
		calls++
		return cannedMeetingDigestJSON(), nil
	})
	providerBreakers.admit(providerOpenAI, seatMeetingDigest)
	providerBreakers.recordPrimaryFailure(providerOpenAI, seatMeetingDigest, providerFailureClassQuota, false)

	_, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "test-openai-key", responder, 1, officeRoomID)
	var circuitErr *ambientAgentCircuitOpenError
	if !errors.As(err, &circuitErr) || !circuitErr.PausedByBreaker {
		t.Fatalf("err=%v, want paused-by-breaker circuit error", err)
	}
	if calls != 0 {
		t.Fatalf("paused channel digest worker still called the provider: %d", calls)
	}
	health := ambientWorkerCapabilitySnapshot(agent, clock.Now(), providerOpenAI, true)
	if health["status"] != capabilityStatusPausedByBreaker || health["pausedByBreaker"] != true {
		t.Fatalf("paused channel digest health=%v", health)
	}
	snapshot, degraded := capabilitySnapshot(clock.Now())
	workers := snapshot["ambientWorkers"].(map[string]any)
	lane, ok := workers["channelDigest"].(map[string]any)
	if !ok || lane["status"] != capabilityStatusPausedByBreaker {
		t.Fatalf("channelDigest lane=%v, want paused_by_breaker in the capability snapshot", workers["channelDigest"])
	}
	if !slices.Contains(degraded, "ambient.channelDigest") {
		t.Fatalf("degraded=%v, want the breaker-parked channel digest worker listed", degraded)
	}
}

/* ---------- M8 / M10: channel digest invalidation + fence ---------- */

// channelDigestEchoResponder answers a channel digest request with a digest
// whose topics are the message lines it was shown, so the stored digest text
// mirrors exactly which messages fed it.
func channelDigestEchoResponder(t *testing.T, calls *int, inputs *[]string) openAITextResponder {
	t.Helper()
	return func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		*calls++
		*inputs = append(*inputs, request.Input)
		_, section, _ := strings.Cut(request.Input, "# New messages (oldest first)")
		lines := strings.Split(section, "\n")
		topics := make([]map[string]any, 0, 4)
		threadID := ""
		if _, after, ok := strings.Cut(request.Input, "thread id: "); ok {
			threadID = strings.TrimSpace(strings.SplitN(after, "\n", 2)[0])
		}
		for index := 0; index+1 < len(lines); index++ {
			line := strings.TrimSpace(lines[index])
			if !strings.HasPrefix(line, "- id=") {
				continue
			}
			id := strings.Fields(strings.TrimPrefix(line, "- id="))[0]
			text := strings.TrimSpace(lines[index+1])
			topics = append(topics, map[string]any{"t": trimForStorage(text, 160), "anchor": id, "at": "", "importance": 3})
		}
		payload, err := json.Marshal(map[string]any{
			"meetingId": threadID, "title": "Packaging", "attendees": []string{"AJ"},
			"topics": topics, "decisions": []any{}, "actionItems": []any{}, "openQuestions": []any{}, "themes": []string{},
		})
		if err != nil {
			t.Fatalf("encode echo digest: %v", err)
		}
		return string(payload), nil
	}
}

// M8: deleting a message invalidates the thread's cumulative digest at once
// (it leaves recall), and the next pass — with NO new message — rebuilds the
// digest from live rows only, never carrying the deleted text forward.
func TestChannelDigestInvalidatedOnDeleteRebuildsFromLiveRows(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	thread := seedPublicChannelMessages(t, app, "channel-digest-delete", "Packaging",
		"CANARYONE Zebra confirmed the pilot start.",
		"CANARYTWO the pricing sheet is due Friday.",
		"CANARYTHREE Tyler owns the vendor call.")
	agent := channelDigestAgent()
	calls := 0
	var inputs []string
	responder := channelDigestEchoResponder(t, &calls, &inputs)
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	digest, ok := app.memory.currentDigest(meetingMemoryKindChannelDigest, thread.ID)
	if !ok || !strings.Contains(digest.Text, "CANARYTWO") {
		t.Fatalf("first digest ok=%v text=%q, want the second message folded in", ok, digest.Text)
	}

	if _, err := app.deleteScoutChatThreadMessage("aj@shareability.com", thread.ID, thread.ID+"-msg-b"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, still := app.memory.currentDigest(meetingMemoryKindChannelDigest, thread.ID); still {
		t.Fatal("deleting a digested message must invalidate the current digest immediately")
	}
	if matches := app.memory.search("CANARYTWO", 8); len(matches) != 0 {
		t.Fatalf("deleted text still recallable through the digest: %v", memoryEntryIDs(matchEntries(matches)))
	}
	stale, found := app.memory.entryByID(digest.ID)
	if !found || stale.Metadata[channelDigestStaleMetadataKey] != "true" || stale.Metadata[channelDigestStaleReasonMetadataKey] != "message_deleted" || !memoryEntryHiddenFromRecall(stale) {
		t.Fatalf("stale digest=%v found=%v, want hidden + stamped stale for the delete", stale.Metadata, found)
	}

	// No new message: the drained window still rebuilds the stale thread.
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1); err != nil {
		t.Fatalf("rebuild pass: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d, want the drained pass to rebuild the stale digest", calls)
	}
	if strings.Contains(inputs[1], "CANARYTWO") || strings.Contains(inputs[1], "Previous digest") {
		t.Fatalf("rebuild input carried the deleted text or the stale digest forward:\n%s", inputs[1])
	}
	rebuilt, ok := app.memory.currentDigest(meetingMemoryKindChannelDigest, thread.ID)
	if !ok {
		t.Fatal("no current digest after the rebuild")
	}
	if strings.Contains(rebuilt.Text, "CANARYTWO") || !strings.Contains(rebuilt.Text, "CANARYONE") || !strings.Contains(rebuilt.Text, "CANARYTHREE") {
		t.Fatalf("rebuilt digest=%q, want the live messages only", rebuilt.Text)
	}
	if rebuilt.Metadata["rebuiltFromLiveRows"] != "true" || rebuilt.Metadata["messageCount"] != "2" {
		t.Fatalf("rebuilt digest metadata=%v, want a from-live-rows rebuild over 2 messages", rebuilt.Metadata)
	}
	// Rebuilt once: the next drained pass has nothing stale to do.
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1); err != nil {
		t.Fatalf("idle pass: %v", err)
	}
	if calls != 2 {
		t.Fatalf("idle pass re-billed a rebuilt thread: calls=%d", calls)
	}
}

// M10: the digest's recall fence comes from the thread's CURRENT membership,
// not from the oldest row in the window.
func TestChannelDigestFenceFollowsCurrentThreadMembership(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	thread := seedPublicChannelMessages(t, app, "channel-digest-fence", "Zebra room", "first public message", "second public message")
	entry, ok := app.memory.entryByKindAndID(meetingMemoryKindScoutChat, thread.ID)
	if !ok {
		t.Fatal("thread record missing")
	}
	current, decoded := decodeScoutChatThreadEntry(entry)
	if !decoded {
		t.Fatal("thread record undecodable")
	}
	current.MemberEmails = []string{"tim@shareability.com"}
	if err := app.saveScoutChatThread(current); err != nil {
		t.Fatalf("restrict membership: %v", err)
	}
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", thread.ID, scoutChatMessageRecord{
		ID: thread.ID + "-msg-c", Kind: "message", Role: "user", Text: "third message after the restriction",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: "aj@shareability.com",
	}); err != nil {
		t.Fatalf("commit restricted message: %v", err)
	}
	rows := make([]meetingMemoryEntry, 0, 3)
	for _, row := range app.memory.entriesOfKind(meetingMemoryKindTranscript, 0) {
		if channelSourcedTranscript(row) && row.Metadata["threadId"] == thread.ID {
			rows = append(rows, row)
		}
	}
	if len(rows) != 3 || rows[0].Metadata["visibility"] != "organization" {
		t.Fatalf("fixture rows=%d oldest visibility=%q, want 3 rows with an organization-stamped oldest row", len(rows), rows[0].Metadata["visibility"])
	}
	responder := func(context.Context, string, openAITextRequest) (string, error) {
		return cannedMeetingDigestJSON(), nil
	}
	if _, err := app.produceChannelDigests(context.Background(), "test-key", rows, responder); err != nil {
		t.Fatalf("produce: %v", err)
	}
	digest, ok := app.memory.currentDigest(meetingMemoryKindChannelDigest, thread.ID)
	if !ok {
		t.Fatal("no digest produced")
	}
	if digest.Metadata["visibility"] != "project" || !strings.Contains(digest.Metadata["memberEmails"], "tim@shareability.com") || !strings.Contains(digest.Metadata["memberEmails"], "aj@shareability.com") || digest.Metadata["ownerEmail"] != "aj@shareability.com" {
		t.Fatalf("digest fence=%v, want project + current members from the thread state", digest.Metadata)
	}
	ctx := context.Background()
	if matches := app.scopedRecallApp(ctx, recallPrincipalForUser(accountStore().findUser("tyler@shareability.com"))).memory.search("Zebra packaging pilot", 8); len(matches) != 0 {
		for _, match := range matches {
			if match.Entry.Kind == meetingMemoryKindChannelDigest {
				t.Fatalf("PRIVACY LEAK: non-member recalled the member-restricted digest: %s", match.Entry.ID)
			}
		}
	}
}

/* ---------- M5: ingestion single-scan filter ---------- */

// M5: the pure row filter the boot backfill reuses over its single corpus
// scan returns exactly one message's LIVE rows.
func TestChannelIngestionRowsFromFiltersOneMessageLiveRows(t *testing.T) {
	row := func(id, threadID, messageID, relevance string) meetingMemoryEntry {
		metadata := map[string]string{"source": transcriptSourceChannel, "threadId": threadID, "messageId": messageID}
		if relevance != "" {
			metadata[relevanceMetadataKey] = relevance
		}
		return meetingMemoryEntry{ID: id, Kind: meetingMemoryKindTranscript, Text: "x", Metadata: metadata}
	}
	corpus := []meetingMemoryEntry{
		row("live-1", "t1", "m1", ""),
		row("superseded-1", "t1", "m1", relevanceExpired),
		row("other-message", "t1", "m2", ""),
		row("other-thread", "t2", "m1", ""),
		{ID: "spoken", Kind: meetingMemoryKindTranscript, Text: "spoken", Metadata: map[string]string{"messageId": "m1", "threadId": "t1"}},
	}
	rows := channelIngestionRowsFrom(corpus, "t1", "m1")
	if len(rows) != 1 || rows[0].ID != "live-1" {
		t.Fatalf("rows=%v, want only the live row of t1/m1", memoryEntryIDs(rows))
	}
	if rows := channelIngestionRowsFrom(corpus, "", "m1"); rows != nil {
		t.Fatalf("empty thread id must match nothing, got %v", memoryEntryIDs(rows))
	}
}

/* ---------- M9 / M11: remember_note ---------- */

// M9: a note lifted from a member-restricted channel inherits the channel's
// fence (project + members) instead of widening to the organization; an
// explicit private:true still narrows further.
func TestRememberNoteInheritsMemberRestrictedChannelFence(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	aj := accountStore().findUser("aj@shareability.com")
	tim := accountStore().findUser("tim@shareability.com")
	tyler := accountStore().findUser("tyler@shareability.com")
	thread, _, err := app.ensureScoutChatThread("project-remember-fence", aj.Email, "AJ", "Zebra project", scoutChatVisibilityPublic, []string{tim.Email})
	if err != nil {
		t.Fatalf("create member-restricted channel: %v", err)
	}
	if members := scoutChatThreadMemberEmails(thread); len(members) != 2 {
		t.Fatalf("fixture members=%v, want aj + tim", members)
	}
	const canary = "capybaraprojectremember6630"
	msg := scoutChatMessageRecord{ID: "msg-project-remember", Kind: "message", Role: "user", Text: canary + " Zebra pays net-30", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: aj.Email}
	if _, err := app.commitScoutChatThreadMessages(aj.Email, thread.ID, msg); err != nil {
		t.Fatalf("commit: %v", err)
	}
	note, recorded, err := app.rememberNote(aj, rememberNoteRequest{ThreadID: thread.ID, MessageID: msg.ID}, "fence-scope")
	if err != nil || !recorded {
		t.Fatalf("remember: recorded=%v err=%v", recorded, err)
	}
	if note.Metadata["visibility"] != "project" || !strings.Contains(note.Metadata["memberEmails"], tim.Email) || !strings.Contains(note.Metadata["memberEmails"], aj.Email) {
		t.Fatalf("note fence=%v, want project + the channel's members", note.Metadata)
	}
	ctx := context.Background()
	if matches := app.scopedRecallApp(ctx, recallPrincipalForUser(tyler)).memory.search(canary, 8); len(matches) != 0 {
		t.Fatalf("PRIVACY LEAK: non-member recalled the member-restricted note: %v", memoryEntryIDs(matchEntries(matches)))
	}
	memberNotes := 0
	for _, match := range app.scopedRecallApp(ctx, recallPrincipalForUser(tim)).memory.search(canary, 8) {
		if match.Entry.Kind == meetingMemoryKindNote {
			memberNotes++
		}
	}
	if memberNotes != 1 {
		t.Fatalf("member note matches=%d, want the note (the channel's own ingestion row may match too)", memberNotes)
	}
	if matches := app.scopedRecallApp(ctx, sharedRoomRecallPrincipal(officeRoomID, "")).memory.search(canary, 8); len(matches) != 0 {
		t.Fatalf("shared-room recall found the member-restricted note: %v", memoryEntryIDs(matchEntries(matches)))
	}
	private, _, err := app.rememberNote(aj, rememberNoteRequest{ThreadID: thread.ID, MessageID: msg.ID, Private: true}, "private-fence-scope")
	if err != nil || private.Metadata["visibility"] != "private" || private.Metadata["memberEmails"] != "" {
		t.Fatalf("private remember from a member-restricted channel=%v err=%v, want private (narrower) with no member list", private.Metadata, err)
	}
	// An organization-public channel still files company memory.
	public := seedPublicChannelMessages(t, app, "public-remember-fence", "General", "public note source")
	open, _, err := app.rememberNote(aj, rememberNoteRequest{ThreadID: public.ID, MessageID: public.ID + "-msg-a"}, "public-fence-scope")
	if err != nil || open.Metadata["visibility"] != "organization" {
		t.Fatalf("public-channel remember=%v err=%v, want organization", open.Metadata, err)
	}
}

// M11: the shared room voice cannot attribute a remember, so the tool is not
// advertised there (it stays in kanbanTools for the orchestrator and the
// private voice).
func TestRememberNoteExcludedFromRoomVoiceTools(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	for _, tool := range app.realtimeRoomVoiceTools() {
		if name, _ := tool["name"].(string); name == rememberNoteToolName {
			t.Fatal("remember_note advertised to the shared room voice, whose dispatch always refuses it")
		}
	}
	inMaster := false
	for _, tool := range app.kanbanTools() {
		if name, _ := tool["name"].(string); name == rememberNoteToolName {
			inMaster = true
		}
	}
	if !inMaster || !realtimeRoomVoiceExcluded[rememberNoteToolName] {
		t.Fatal("remember_note must stay in kanbanTools and be excluded from the room voice by the exclusion map")
	}
}

/* ---------- B2 / B3: capability status ---------- */

// B2: staleness on a lane with a live producer (allocated / connected) is a
// stall and degrades; staleness with nothing allocated is ordinary idleness.
func TestCapabilityStatusStaleLiveLaneDegradesUnallocatedIdles(t *testing.T) {
	old := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	for _, tc := range []struct {
		name string
		base map[string]any
		want string
	}{
		{name: "stale allocated connected STT lane", base: map[string]any{"enabled": true, "lastSuccessAt": old, "stale": true, "allocated": true, "connected": true}, want: capabilityStatusDegraded},
		{name: "stale allocated lane without a connected key", base: map[string]any{"enabled": true, "lastSuccessAt": old, "stale": true, "allocated": true}, want: capabilityStatusDegraded},
		{name: "stale connected lane that reports no allocation key", base: map[string]any{"enabled": true, "lastSuccessAt": old, "stale": true, "connected": true}, want: capabilityStatusDegraded},
		{name: "stale connected but explicitly unallocated", base: map[string]any{"enabled": true, "lastSuccessAt": old, "stale": true, "connected": true, "allocated": false}, want: capabilityStatusIdle},
		{name: "stale with nothing allocated", base: map[string]any{"enabled": true, "lastSuccessAt": old, "stale": true, "allocated": false, "connected": false}, want: capabilityStatusIdle},
		{name: "stale typed seat", base: map[string]any{"enabled": true, "lastSuccessAt": old, "stale": true}, want: capabilityStatusIdle},
		{name: "fresh allocated connected", base: map[string]any{"enabled": true, "lastSuccessAt": old, "stale": false, "allocated": true, "connected": true}, want: capabilityStatusHealthy},
		{name: "allocated never produced", base: map[string]any{"enabled": true, "allocated": true, "connected": true}, want: capabilityStatusIdle},
	} {
		if got := capabilityStatus(tc.base, true); got != tc.want {
			t.Errorf("%s: status=%q, want %q", tc.name, got, tc.want)
		}
	}
	if !capabilityStatusCountsDegraded(capabilityStatusPausedByBreaker) || !capabilityStatusCountsDegraded(capabilityStatusDegraded) || capabilityStatusCountsDegraded(capabilityStatusFallbackActive) || capabilityStatusCountsDegraded(capabilityStatusIdle) {
		t.Fatal("degraded rollup must count paused_by_breaker + degraded and nothing else")
	}
}

// B3: a worker parked behind its seat breaker keeps its paused_by_breaker
// label on the lane but counts toward the /capabilities degraded rollup.
func TestCapabilitiesRollupCountsBreakerParkedWorkerAsDegraded(t *testing.T) {
	resetProviderBreakersForTest(t)
	resetCapabilityRuntimeForTest(t)
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("ANTHROPIC_API_KEY", "")
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	providerBreakers.admit(providerOpenAI, seatBrain)
	providerBreakers.recordPrimaryFailure(providerOpenAI, seatBrain, providerFailureClassQuota, false)
	snapshot, degraded := capabilitySnapshot(time.Now().UTC())
	workers := snapshot["ambientWorkers"].(map[string]any)
	brain := workers["brain"].(map[string]any)
	if brain["status"] != capabilityStatusPausedByBreaker || brain["pausedByBreaker"] != true {
		t.Fatalf("parked brain lane=%v, want paused_by_breaker on the lane", brain)
	}
	if !slices.Contains(degraded, "ambient.brain") {
		t.Fatalf("degraded=%v, want the breaker-parked worker listed", degraded)
	}
	recorder := httptest.NewRecorder()
	capabilitiesHandler(recorder, httptest.NewRequest(http.MethodGet, "/capabilities", nil))
	var payload struct {
		OK       bool     `json:"ok"`
		Degraded []string `json:"degraded"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if payload.OK || !slices.Contains(payload.Degraded, "ambient.brain") {
		t.Fatalf("/capabilities ok=%v degraded=%v, want ok=false with the parked worker listed", payload.OK, payload.Degraded)
	}
}

/* ---------- B6: mention directory skips offboarded accounts ---------- */

// B6: an offboarded account neither resolves as a mention target nor receives
// a mention notification.
func TestChatMentionSkipsDisabledAccounts(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("mention notifications must never invoke the model")
		return "", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	if _, err := accountStore().setDisabled("tyler@shareability.com", true, time.Now().UTC()); err != nil {
		t.Fatalf("disable tyler: %v", err)
	}
	t.Cleanup(func() { _, _ = accountStore().setDisabled("tyler@shareability.com", false, time.Now().UTC()) })
	if _, listed := chatMentionDirectoryHandles()["tyler"]; listed {
		t.Fatal("offboarded account still listed in the mention directory")
	}
	if targets := chatMentionTargetEmails("@tyler @AJ can you look?"); len(targets) != 1 || targets[0] != "aj@shareability.com" {
		t.Fatalf("targets=%v, want only the active account", targets)
	}

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "warroom", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	tim := accountStore().findUser("tim@shareability.com")
	if _, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), tim, channel.ID, "@tyler @AJ can you review the pilot cut?", nil, ""); err != nil {
		t.Fatalf("append channel message: %v", err)
	}
	if unread := kanbanApp.unreadNotificationsFor("tyler@shareability.com", notificationListLimit); len(unread) != 0 {
		t.Fatalf("offboarded account received a mention notification: %#v", unread)
	}
	if unread := kanbanApp.unreadNotificationsFor("aj@shareability.com", notificationListLimit); len(unread) != 1 {
		t.Fatalf("active account unread=%#v, want exactly one mention", unread)
	}
}

// The reply-notification path is a second resolver: replying to a message whose
// author has since been offboarded must not ring that account either.
func TestChatReplyNotificationSkipsDisabledAuthor(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("reply notifications must never invoke the model")
		return "", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	channel, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "warroom", "public")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	tyler := accountStore().findUser("tyler@shareability.com")
	posted, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), tyler, channel.ID, "pilot cut is ready for notes", nil, "")
	if err != nil {
		t.Fatalf("append tyler message: %v", err)
	}
	messageID := scoutChatAppendedMessageID(posted)
	if messageID == "" {
		t.Fatalf("append result carries no message id: %#v", posted)
	}
	if _, err := accountStore().setDisabled("tyler@shareability.com", true, time.Now().UTC()); err != nil {
		t.Fatalf("disable tyler: %v", err)
	}
	t.Cleanup(func() { _, _ = accountStore().setDisabled("tyler@shareability.com", false, time.Now().UTC()) })

	tim := accountStore().findUser("tim@shareability.com")
	if _, err := kanbanApp.appendScoutChatThreadMessageWithReplyAndTool(context.Background(), tim, channel.ID, "looks great, shipping it", nil, "", messageID, ""); err != nil {
		t.Fatalf("append reply: %v", err)
	}
	if unread := kanbanApp.unreadNotificationsFor("tyler@shareability.com", notificationListLimit); len(unread) != 0 {
		t.Fatalf("offboarded author received a reply notification: %#v", unread)
	}
}

// scoutChatAppendedMessageID digs the new message id out of an append result.
func scoutChatAppendedMessageID(result map[string]any) string {
	if result == nil {
		return ""
	}
	if record, ok := result["message"].(scoutChatMessageRecord); ok {
		return record.ID
	}
	if record, ok := result["message"].(*scoutChatMessageRecord); ok && record != nil {
		return record.ID
	}
	if nested, ok := result["message"].(map[string]any); ok {
		if id, ok := nested["id"].(string); ok {
			return id
		}
	}
	if id, ok := result["id"].(string); ok {
		return id
	}
	return ""
}
