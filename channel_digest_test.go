package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func seedPublicChannelMessages(t *testing.T, app *kanbanBoardApp, threadID string, title string, texts ...string) scoutChatThreadRecord {
	t.Helper()
	thread, _, err := app.ensureScoutChatThread(threadID, "aj@shareability.com", "AJ", title, scoutChatVisibilityPublic, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	now := time.Now().UTC()
	for index, text := range texts {
		if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", thread.ID, scoutChatMessageRecord{
			ID: threadID + "-msg-" + string(rune('a'+index)), Kind: "message", Role: "user", Text: text,
			CreatedAt: now.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: "aj@shareability.com",
		}); err != nil {
			t.Fatalf("commit message %d: %v", index, err)
		}
	}
	return thread
}

// Wave 8 D5: the channel digest producer digests a thread's messages into ONE
// current channel_digest keyed by threadId, advances its pass cursor, and
// never re-bills a consumed window.
func TestChannelDigestProducerDigestsPerThread(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)
	thread := seedPublicChannelMessages(t, app, "channel-digest-test", "Packaging",
		"Zebra confirmed the packaging pilot kicks off next Monday.",
		"AJ: we choose vendor Zebra for the packaging pilot.",
		"Tyler will draft the pricing sheet by Friday.")

	agent := channelDigestAgent()
	if agent.inputKind != meetingMemoryKindTranscript || agent.artifactKind != meetingMemoryKindChannelDigestPass || agent.cursorMetadataKey != channelDigestCursorMetadataKey || agent.inputFilter == nil {
		t.Fatalf("agent contract=%+v", agent)
	}
	calls := 0
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		if request.Workflow != "channel_digest" || request.JSONSchema == nil {
			t.Fatalf("channel digest request missing its contract: %+v", request)
		}
		if !strings.Contains(request.Input, "thread id: "+thread.ID) || !strings.Contains(request.Input, "Zebra confirmed") {
			t.Fatalf("input missing the thread/messages:\n%s", request.Input)
		}
		if !strings.Contains(request.Instructions, "channel digest compiler") {
			t.Fatal("must use the chat-specific instruction variant")
		}
		return cannedMeetingDigestJSON(), nil
	}
	pass, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1)
	if err != nil {
		t.Fatalf("channel digest pass: %v", err)
	}
	if calls != 1 || pass.Kind != meetingMemoryKindChannelDigestPass {
		t.Fatalf("calls=%d pass=%+v", calls, pass)
	}
	digest, ok := app.memory.currentDigest(meetingMemoryKindChannelDigest, thread.ID)
	if !ok {
		t.Fatal("no current channel_digest for the thread")
	}
	if digest.Metadata["threadId"] != thread.ID || digest.Metadata["channelTitle"] != "Packaging" || digest.Metadata["visibility"] != "organization" {
		t.Fatalf("digest metadata=%v", digest.Metadata)
	}
	payload, parsed := parseMeetingDigest(digest.Text)
	if !parsed || payload.MeetingID != thread.ID || len(payload.Decisions) != 1 {
		t.Fatalf("digest payload=%+v parsed=%v (meetingId must be clamped to the thread id)", payload, parsed)
	}
	transcripts := app.memory.entriesOfKind(meetingMemoryKindTranscript, 0)
	if pass.Metadata[channelDigestCursorMetadataKey] != transcripts[len(transcripts)-1].ID {
		t.Fatalf("pass cursor=%q, want the last transcript %q", pass.Metadata[channelDigestCursorMetadataKey], transcripts[len(transcripts)-1].ID)
	}
	// the digest is recall-eligible and searchable.
	if matches := app.memory.search("vendor Zebra packaging pilot", 8); len(matches) == 0 {
		t.Fatal("channel digest must be searchable")
	}
	// consumed: no second call.
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if calls != 1 {
		t.Fatalf("second pass re-billed the consumed window: calls=%d", calls)
	}
	// a channel digest pass is bookkeeping, never a timeline row.
	for _, entry := range visibleMeetingMemoryEntries(app.memory.snapshot(0), 100) {
		if entry.Kind == meetingMemoryKindChannelDigestPass || entry.Kind == meetingMemoryKindChannelDigest {
			t.Fatalf("digest bookkeeping leaked into the client timeline: %s", entry.Kind)
		}
	}
}

// Wave 8 D5: channel rows stop riding the meeting-brain prompt — the brain's
// window filter excludes them structurally, and the channel producer's window
// admits only them.
func TestChannelRowsExcludedFromMeetingBrainWindow(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	const channelCanary = "CHANNEL-ONLY-ROW-4471"
	seedPublicChannelMessages(t, app, "channel-brain-exclusion", "General", channelCanary+" posted in chat")
	if _, ok, err := app.memory.appendRoomChatTranscript("room-chat-1", "AJ", "Spoken in the room about the launch."); err != nil || !ok {
		t.Fatalf("room transcript: ok=%v err=%v", ok, err)
	}

	brain := meetingBrainAgent()
	if brain.inputFilter == nil {
		t.Fatal("meeting brain must declare its window filter")
	}
	brainWindow := app.memory.unconsumedEntriesAfterFiltered(brain.inputKind, brain.artifactKind, brain.cursorMetadataKey, 500, "", "", RecallPrincipal{}, brain.inputFilter)
	if len(brainWindow) != 1 || strings.Contains(brainWindow[0].Text, channelCanary) {
		t.Fatalf("brain window=%v, want only the spoken transcript", memoryEntryIDs(brainWindow))
	}
	channel := channelDigestAgent()
	channelWindow := app.memory.unconsumedEntriesAfterFiltered(channel.inputKind, channel.artifactKind, channel.cursorMetadataKey, 500, "", "", RecallPrincipal{}, channel.inputFilter)
	if len(channelWindow) != 1 || !strings.Contains(channelWindow[0].Text, channelCanary) {
		t.Fatalf("channel window=%v, want only the channel row", memoryEntryIDs(channelWindow))
	}
	// nil filter is the pre-wave behavior: everything.
	if all := app.memory.unconsumedEntriesAfter(meetingMemoryKindTranscript, meetingMemoryKindBrain, meetingBrainCursorMetadataKey, 500, ""); len(all) != 2 {
		t.Fatalf("unfiltered window=%d, want 2", len(all))
	}
}

// Keyless deploys never start the worker.
func TestChannelDigestWorkerKeylessNoOp(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.startChannelDigestWorker("")
	app.mu.Lock()
	_, registered := app.agentCancels[channelDigestAgentName]
	app.mu.Unlock()
	if registered {
		t.Fatal("channel digest worker must not start without an OpenAI key")
	}
	if entry, err := app.produceChannelDigests(context.Background(), "", nil, nil); err != nil || entry.ID != "" {
		t.Fatalf("empty window must be a no-op: entry=%+v err=%v", entry, err)
	}
}

// 2026-09-02 (gen 248): the worker's first boot on a store that already held
// channel rows was graded durable_cursor_ambiguous and it never ran. With the
// opt-in first-run anchor the cursor starts at the pre-boot transcript, the
// first pass seeds every pre-boot thread from its bounded live rows, a thread
// that posts again is widened with its history, and the second pass is idle.
func TestChannelDigestFirstBootAnchorsAndSeedsPreExistingThreads(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	t.Setenv("CHANNEL_DIGEST_MIN_MESSAGES", "1")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))

	seed := newKanbanBoardApp()
	alpha := seedPublicChannelMessages(t, seed, "channel-first-boot-alpha", "Alpha",
		"Zebra confirmed the packaging pilot kicks off next Monday.",
		"AJ: we choose vendor Zebra for the packaging pilot.",
		"Tyler will draft the pricing sheet by Friday.")
	beta := seedPublicChannelMessages(t, seed, "channel-first-boot-beta", "Beta",
		"Kappa wants a call about the retainer.",
		"AJ: hold the retainer until the Zebra pilot lands.")
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	app := newKanbanBoardApp()
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() {
		kanbanApp = previousApp
		_ = app.Close()
	})
	agent := channelDigestAgent()
	baseline, blocked, err := app.bootstrapAmbientContinuity(agent, officeRoomID)
	if err != nil || blocked != "" || baseline == "" {
		t.Fatalf("first boot baseline=%q blocked=%q err=%v, want an anchored, unblocked scope", baseline, blocked, err)
	}
	checkpoint, err := app.ensureAmbientScopeCheckpoint(agent, officeRoomID, baseline, blocked)
	if err != nil || !checkpoint.FirstRunAnchor {
		t.Fatalf("checkpoint=%+v err=%v, want the first-run anchor recorded", checkpoint, err)
	}
	app.setAmbientAgentBaselineID(agent.name, checkpoint.BaselineID)

	// alpha posts again before the first pass: its three pre-boot rows are
	// caught up as a chunk BEFORE the window group folds the new message with
	// that chunk as its prior; beta is caught up from the drained scan.
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", alpha.ID, scoutChatMessageRecord{
		ID: "channel-first-boot-alpha-msg-new", Kind: "message", Role: "user", Text: "Zebra signed; pilot starts Monday for real.",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: "aj@shareability.com",
	}); err != nil {
		t.Fatalf("post new alpha message: %v", err)
	}
	inputs := map[string][]string{}
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		for _, threadID := range []string{alpha.ID, beta.ID} {
			if strings.Contains(request.Input, "thread id: "+threadID) {
				inputs[threadID] = append(inputs[threadID], request.Input)
			}
		}
		return cannedMeetingDigestJSON(), nil
	}
	pass, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "test-key", responder, agent.minBatch(), officeRoomID)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if len(inputs[alpha.ID]) != 2 || len(inputs[beta.ID]) != 1 {
		t.Fatalf("calls per thread alpha=%d beta=%d, want alpha's history chunk + window and beta's chunk", len(inputs[alpha.ID]), len(inputs[beta.ID]))
	}
	chunk, window := inputs[alpha.ID][0], inputs[alpha.ID][1]
	if !strings.Contains(chunk, "Zebra confirmed") || strings.Contains(chunk, "pilot starts Monday for real") || strings.Contains(chunk, "Previous digest") {
		t.Fatalf("alpha history chunk must hold only the pre-boot rows, with no prior:\n%s", chunk)
	}
	if !strings.Contains(window, "Previous digest for this channel") || !strings.Contains(window, "pilot starts Monday for real") || strings.Contains(window, "Zebra confirmed") {
		t.Fatalf("alpha window pass must carry the chunk digest forward and fold only the new message:\n%s", window)
	}
	if !strings.Contains(inputs[beta.ID][0], "hold the retainer") {
		t.Fatalf("beta must be caught up from its live rows:\n%s", inputs[beta.ID][0])
	}
	alphaDigest, ok := app.memory.currentDigest(meetingMemoryKindChannelDigest, alpha.ID)
	if !ok || alphaDigest.Metadata["seededFromLiveRows"] == "true" || alphaDigest.Metadata["messageCount"] != "1" || alphaDigest.Metadata["visibility"] != "organization" ||
		alphaDigest.Metadata[channelDigestSeedPendingMetadataKey] != "0" || alphaDigest.Metadata[channelDigestHistoryEndMetadataKey] == "" {
		t.Fatalf("alpha digest=%v ok=%v, want the window digest carrying caught-up bookkeeping", alphaDigest.Metadata, ok)
	}
	betaDigest, ok := app.memory.currentDigest(meetingMemoryKindChannelDigest, beta.ID)
	if !ok || betaDigest.Metadata["seededFromLiveRows"] != "true" || betaDigest.Metadata["messageCount"] != "2" || betaDigest.Metadata[channelDigestSeedPendingMetadataKey] != "0" {
		t.Fatalf("beta digest=%v ok=%v, want caught up from its 2 rows", betaDigest.Metadata, ok)
	}
	if health := ambientWorkerCapabilitySnapshot(agent, time.Now().UTC(), providerOpenAI, true); health["seedPendingRows"] != 0 {
		t.Fatalf("seedPendingRows=%v after full catch-up, want 0", health["seedPendingRows"])
	}
	if pass.Kind != meetingMemoryKindChannelDigestPass || pass.Metadata[channelDigestCursorMetadataKey] == "" {
		t.Fatalf("pass=%+v, want the cursor advanced past the new message", pass)
	}
	app.mu.Lock()
	failure := app.agentFailures[agent.name]
	app.mu.Unlock()
	if failure != nil {
		t.Fatalf("first pass recorded a failure: %+v", failure)
	}
	health := ambientWorkerCapabilitySnapshot(agent, time.Now().UTC(), providerOpenAI, true)
	if health["circuit"] == "continuity_error" || health["continuityError"] == true || health["firstRunAnchorScopes"] != 1 {
		t.Fatalf("snapshot=%v, want no continuity error and one first-run anchor scope", health)
	}

	// second pass: drained, everything digested, no model calls
	calls := 0
	quiet := func(context.Context, string, openAITextRequest) (string, error) {
		calls++
		return cannedMeetingDigestJSON(), nil
	}
	if _, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "test-key", quiet, agent.minBatch(), officeRoomID); err != nil || calls != 0 {
		t.Fatalf("second pass calls=%d err=%v, want idle", calls, err)
	}

	// carry-forward still holds: a further alpha message feeds the prior
	// digest, not the history again
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", alpha.ID, scoutChatMessageRecord{
		ID: "channel-first-boot-alpha-msg-later", Kind: "message", Role: "user", Text: "Pricing sheet delivered.",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: "aj@shareability.com",
	}); err != nil {
		t.Fatalf("post later alpha message: %v", err)
	}
	var later openAITextRequest
	if _, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "test-key", func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		later = request
		return cannedMeetingDigestJSON(), nil
	}, agent.minBatch(), officeRoomID); err != nil {
		t.Fatalf("third pass: %v", err)
	}
	if !strings.Contains(later.Input, "Previous digest for this channel") || strings.Contains(later.Input, "Zebra confirmed") || !strings.Contains(later.Input, "Pricing sheet delivered") {
		t.Fatalf("third pass must carry the prior digest forward without re-feeding history:\n%s", later.Input)
	}
	if digest, ok := app.memory.currentDigest(meetingMemoryKindChannelDigest, alpha.ID); !ok || digest.Metadata["seededFromLiveRows"] == "true" {
		t.Fatalf("later digest=%v ok=%v, want an ordinary carry-forward digest", digest.Metadata, ok)
	}
}

// A 400-row pre-boot thread is caught up oldest-first in bounded chunks of
// channelDigestRebuildRowCap over successive passes, each chunk fed with the
// previous digest, until nothing is pending — no row skipped, no replay
// through the stream cursor, pending rows visible on the snapshot.
func TestChannelDigestFirstRunCatchesUpLongThreadInBoundedPasses(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))

	const rowCount = 400
	seed := newKanbanBoardApp()
	thread, _, err := seed.ensureScoutChatThread("channel-catch-up-long", "aj@shareability.com", "AJ", "Long", scoutChatVisibilityPublic, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	base := time.Now().UTC().Add(-time.Duration(rowCount+1) * time.Second)
	for index := 1; index <= rowCount; index++ {
		if _, err := seed.commitScoutChatThreadMessages("aj@shareability.com", thread.ID, scoutChatMessageRecord{
			ID: fmt.Sprintf("channel-catch-up-long-msg-%04d", index), Kind: "message", Role: "user", Text: fmt.Sprintf("Row %04d: decision %04d stands.", index, index),
			CreatedAt: base.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: "aj@shareability.com",
		}); err != nil {
			t.Fatalf("commit row %d: %v", index, err)
		}
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	app := newKanbanBoardApp()
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() {
		kanbanApp = previousApp
		_ = app.Close()
	})
	agent := channelDigestAgent()
	baseline, blocked, err := app.bootstrapAmbientContinuity(agent, officeRoomID)
	if err != nil || blocked != "" {
		t.Fatalf("first boot baseline=%q blocked=%q err=%v", baseline, blocked, err)
	}
	checkpoint, err := app.ensureAmbientScopeCheckpoint(agent, officeRoomID, baseline, blocked)
	if err != nil {
		t.Fatal(err)
	}
	app.setAmbientAgentBaselineID(agent.name, checkpoint.BaselineID)

	var seen []string
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		seen = append(seen, request.Input)
		return cannedMeetingDigestJSON(), nil
	}
	runPass := func() {
		t.Helper()
		if _, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "test-key", responder, agent.minBatch(), officeRoomID); err != nil {
			t.Fatalf("pass: %v", err)
		}
	}
	expect := func(pass int, first, last int, pending int, wantPrior bool) {
		t.Helper()
		if len(seen) != pass {
			t.Fatalf("after pass %d calls=%d, want exactly one chunk per pass", pass, len(seen))
		}
		input := seen[pass-1]
		for _, row := range []int{first, last} {
			if !strings.Contains(input, fmt.Sprintf("Row %04d:", row)) {
				t.Fatalf("pass %d chunk missing row %04d:\n%s", pass, row, input[:200])
			}
		}
		for _, row := range []int{first - 1, last + 1} {
			if row >= 1 && row <= rowCount && strings.Contains(input, fmt.Sprintf("Row %04d:", row)) {
				t.Fatalf("pass %d chunk leaked row %04d outside [%d,%d]", pass, row, first, last)
			}
		}
		if strings.Contains(input, "Previous digest for this channel") != wantPrior {
			t.Fatalf("pass %d prior fed=%v, want %v", pass, !wantPrior, wantPrior)
		}
		digest, ok := app.memory.currentDigest(meetingMemoryKindChannelDigest, thread.ID)
		if !ok || digest.Metadata[channelDigestSeedPendingMetadataKey] != strconv.Itoa(pending) || digest.Metadata[channelDigestSeedThroughMetadataKey] != fmt.Sprintf("channel-%s-channel-catch-up-long-msg-%04d", thread.ID, last) && !strings.HasSuffix(digest.Metadata[channelDigestSeedThroughMetadataKey], fmt.Sprintf("msg-%04d", last)) {
			t.Fatalf("pass %d digest=%v ok=%v, want pending=%d through row %04d", pass, digest.Metadata, ok, pending, last)
		}
		if health := ambientWorkerCapabilitySnapshot(agent, time.Now().UTC(), providerOpenAI, true); health["seedPendingRows"] != pending {
			t.Fatalf("pass %d seedPendingRows=%v, want %d", pass, health["seedPendingRows"], pending)
		}
	}
	runPass()
	expect(1, 1, channelDigestRebuildRowCap, rowCount-channelDigestRebuildRowCap, false)
	runPass()
	expect(2, channelDigestRebuildRowCap+1, 2*channelDigestRebuildRowCap, rowCount-2*channelDigestRebuildRowCap, true)
	runPass()
	expect(3, 2*channelDigestRebuildRowCap+1, rowCount, 0, true)
	runPass()
	if len(seen) != 3 {
		t.Fatalf("a caught-up thread was fed again: calls=%d", len(seen))
	}
}

// Production-shaped regression: point STRIDE_PROD_MEMORY_FIXTURE at a copy
// of the production meeting-memory.jsonl (never committed) and the worker's
// first boot must anchor, run one clean guarded pass that seeds the public
// channel threads, and stay clean on the second pass; the narrative
// maintainer's legacy rooms must resolve too. Skipped without the fixture.
func TestChannelDigestFirstRunOnProductionShapedStore(t *testing.T) {
	fixture := strings.TrimSpace(os.Getenv("STRIDE_PROD_MEMORY_FIXTURE"))
	if fixture == "" {
		t.Skip("set STRIDE_PROD_MEMORY_FIXTURE to a production-shaped meeting-memory.jsonl")
	}
	source, err := os.Open(fixture)
	if err != nil {
		t.Skipf("fixture unavailable: %v", err)
	}
	defer source.Close()
	resetCapabilityRuntimeForTest(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	dir := t.TempDir()
	memoryPath := filepath.Join(dir, "memory.jsonl")
	copyTarget, err := os.Create(memoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(copyTarget, source); err != nil {
		t.Fatal(err)
	}
	if err := copyTarget.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEETING_MEMORY_PATH", memoryPath)
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))

	app := newKanbanBoardApp()
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() {
		kanbanApp = previousApp
		_ = app.Close()
	})

	agent := channelDigestAgent()
	baseline, blocked, err := app.bootstrapAmbientContinuity(agent, officeRoomID)
	if err != nil || blocked != "" || baseline == "" {
		t.Fatalf("channel digest first boot baseline=%q blocked=%q err=%v", baseline, blocked, err)
	}
	checkpoint, err := app.ensureAmbientScopeCheckpoint(agent, officeRoomID, baseline, blocked)
	if err != nil {
		t.Fatal(err)
	}
	app.setAmbientAgentBaselineID(agent.name, checkpoint.BaselineID)
	threads := map[string]int{}
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		for _, line := range strings.Split(request.Input, "\n") {
			if strings.HasPrefix(line, "thread id: ") {
				threads[strings.TrimPrefix(line, "thread id: ")]++
			}
		}
		return cannedMeetingDigestJSON(), nil
	}
	if _, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "test-key", responder, agent.minBatch(), officeRoomID); err != nil {
		t.Fatalf("first guarded pass: %v", err)
	}
	if len(threads) == 0 {
		t.Fatal("first pass digested no thread; the production copy holds public channel rows")
	}
	for threadID, count := range threads {
		digest, ok := app.memory.currentDigest(meetingMemoryKindChannelDigest, threadID)
		if !ok || count != 1 || digest.Metadata["seededFromLiveRows"] != "true" || digest.Metadata["visibility"] == "" {
			t.Fatalf("thread %s digest=%v ok=%v calls=%d", threadID, digest.Metadata, ok, count)
		}
	}
	t.Logf("first pass seeded %d public channel thread(s)", len(threads))
	app.mu.Lock()
	failure := app.agentFailures[agent.name]
	app.mu.Unlock()
	if failure != nil {
		t.Fatalf("first pass recorded %+v", failure)
	}
	health := ambientWorkerCapabilitySnapshot(agent, time.Now().UTC(), providerOpenAI, true)
	if health["circuit"] == "continuity_error" || health["continuityError"] == true {
		t.Fatalf("channel digest snapshot=%v", health)
	}
	before := len(threads)
	if _, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "test-key", responder, agent.minBatch(), officeRoomID); err != nil || len(threads) != before {
		t.Fatalf("second pass err=%v threads=%d (was %d), want idle", err, len(threads), before)
	}

	// The narrative maintainer's legacy rooms (expired dossiers, in-place
	// cursors) must all resolve; report what the other room-scoped workers do
	// with never-run rooms, which stay fail-closed by doctrine until their next
	// active sitting repairs them.
	for _, worker := range []ambientAgentConfig{narrativeMaintainerAgent(), meetingDigestAgent(), meetingBoardAgent(), missionIntelligenceAgent(), decisionLedgerAgent()} {
		for _, roomID := range app.ambientAgentRooms(worker) {
			baseline, blocked, err := app.bootstrapAmbientContinuity(worker, roomID)
			if err != nil {
				t.Fatalf("%s/%s bootstrap: %v", worker.name, roomID, err)
			}
			t.Logf("%s room=%s baseline=%q blocked=%q", worker.name, roomID, baseline, blocked)
			if worker.name == narrativeMaintainerAgentName && blocked != "" {
				t.Fatalf("narrative room %s still blocked: %s", roomID, blocked)
			}
		}
	}
}

// Two live rows can carry the SAME timestamp (a bulk import stamps whole
// seconds). The first-run catch-up resolves its boundaries by ORDINAL
// position, so a tie at the seed high-water — or at the chain floor — stays
// pending instead of being silently skipped, which is the omission the
// catch-up exists to prevent.
func TestChannelDigestPendingHistoryResolvesTimestampTiesByPosition(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	rows := []meetingMemoryEntry{
		{ID: "tie-row-1", CreatedAt: at},
		{ID: "tie-row-2", CreatedAt: at},
		{ID: "tie-row-3", CreatedAt: at.Add(time.Second)},
	}
	seed := func(t *testing.T, metadata map[string]string) {
		t.Helper()
		metadata["threadId"] = "thread-tie"
		if _, err := app.memory.upsertDigest(meetingMemoryKindChannelDigest, "thread-tie", `{"meetingId":"thread-tie","title":"Tie"}`, metadata); err != nil {
			t.Fatalf("seed digest: %v", err)
		}
	}
	ids := func(pending []meetingMemoryEntry) []string {
		out := make([]string, 0, len(pending))
		for _, row := range pending {
			out = append(out, row.ID)
		}
		return out
	}

	seed(t, map[string]string{channelDigestSeedThroughMetadataKey: "tie-row-1", channelDigestSeedPendingMetadataKey: "2"})
	if got := ids(app.channelDigestPendingHistory("thread-tie", rows, nil, map[string]int{})); !reflect.DeepEqual(got, []string{"tie-row-2", "tie-row-3"}) {
		t.Fatalf("pending=%v, want the same-second tie still pending after tie-row-1", got)
	}

	// the chain floor is exclusive by position too: tie-row-3 belongs to the
	// window digests, tie-row-2 is still unfolded history.
	seed(t, map[string]string{
		channelDigestSeedThroughMetadataKey: "tie-row-1",
		channelDigestSeedPendingMetadataKey: "1",
		channelDigestHistoryEndMetadataKey:  "tie-row-3",
	})
	if got := ids(app.channelDigestPendingHistory("thread-tie", rows, nil, map[string]int{})); !reflect.DeepEqual(got, []string{"tie-row-2"}) {
		t.Fatalf("pending=%v, want only tie-row-2 between the high-water and the floor", got)
	}

	// caught up: nothing pending once the high-water reaches the floor.
	seed(t, map[string]string{
		channelDigestSeedThroughMetadataKey: "tie-row-2",
		channelDigestSeedPendingMetadataKey: "0",
		channelDigestHistoryEndMetadataKey:  "tie-row-3",
	})
	if got := app.channelDigestPendingHistory("thread-tie", rows, nil, map[string]int{}); len(got) != 0 {
		t.Fatalf("pending=%v, want a caught-up thread to have nothing left", ids(got))
	}
}

/* ---------- reviewer findings, 2026-09-02 first-run catch-up ---------- */

// seedChannelDigestThread files count numbered messages into one public
// channel and returns the thread plus its live transcript rows, oldest first.
func seedChannelDigestThread(t *testing.T, app *kanbanBoardApp, threadID string, title string, count int) (scoutChatThreadRecord, []meetingMemoryEntry) {
	t.Helper()
	thread, _, err := app.ensureScoutChatThread(threadID, "aj@shareability.com", "AJ", title, scoutChatVisibilityPublic, nil)
	if err != nil {
		t.Fatalf("create channel %s: %v", threadID, err)
	}
	now := time.Now().UTC()
	for index := 1; index <= count; index++ {
		if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", thread.ID, scoutChatMessageRecord{
			ID: fmt.Sprintf("%s-msg-%03d", threadID, index), Kind: "message", Role: "user",
			Text:       fmt.Sprintf("Row %03d: decision %03d stands.", index, index),
			CreatedAt:  now.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano),
			AuthorName: "AJ", AuthorEmail: "aj@shareability.com",
		}); err != nil {
			t.Fatalf("commit %s row %d: %v", threadID, index, err)
		}
	}
	rows := app.liveChannelThreadRows(thread.ID, 0)
	if len(rows) != count {
		t.Fatalf("thread %s live rows=%d, want %d", threadID, len(rows), count)
	}
	return thread, rows
}

func seedChannelDigestMetadata(t *testing.T, app *kanbanBoardApp, threadID string, metadata map[string]string) {
	t.Helper()
	metadata["threadId"] = threadID
	if _, err := app.memory.upsertDigest(meetingMemoryKindChannelDigest, threadID, `{"meetingId":"`+threadID+`","title":"Prior"}`, metadata); err != nil {
		t.Fatalf("seed digest for %s: %v", threadID, err)
	}
}

// A digest chain that predates the catch-up bookkeeping must NOT be caught up
// from `fromTranscriptId`: on a window digest that key is the first row of the
// LAST window folded, not the oldest row the chain ever folded, so treating it
// as the chain floor re-queues every already-summarized row before it — every
// existing thread would re-summarize its whole history, one provider call per
// thread per pass. A legacy REBUILD digest is the exception: a rebuild caps at
// the newest live rows and declares the rest handled, so its fromTranscriptId
// genuinely is the floor.
func TestChannelDigestPendingHistorySkipsLegacyWindowDigest(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	base := time.Now().UTC().Add(-time.Hour)
	rows := make([]meetingMemoryEntry, 0, 10)
	for index := 1; index <= 10; index++ {
		rows = append(rows, meetingMemoryEntry{ID: fmt.Sprintf("legacy-row-%02d", index), CreatedAt: base.Add(time.Duration(index) * time.Second)})
	}

	// the production shape: 10 rows folded incrementally by window passes, the
	// current digest carrying only the legacy stamps.
	seedChannelDigestMetadata(t, app, "thread-legacy", map[string]string{"fromTranscriptId": "legacy-row-09", "throughTranscriptId": "legacy-row-10"})
	if pending := app.channelDigestPendingHistory("thread-legacy", rows, nil, map[string]int{}); len(pending) != 0 {
		t.Fatalf("legacy window digest re-queued %d already-folded row(s): %v", len(pending), memoryEntryIDs(pending))
	}

	// a legacy rebuild digest still catches up the history it deliberately cut.
	seedChannelDigestMetadata(t, app, "thread-legacy", map[string]string{"rebuiltFromLiveRows": "true", "fromTranscriptId": "legacy-row-06", "throughTranscriptId": "legacy-row-10"})
	want := []string{"legacy-row-01", "legacy-row-02", "legacy-row-03", "legacy-row-04", "legacy-row-05"}
	if got := memoryEntryIDs(app.channelDigestPendingHistory("thread-legacy", rows, nil, map[string]int{})); !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy rebuild pending=%v, want %v", got, want)
	}
	// a legacy digest with no usable floor at all is caught up, never replayed.
	seedChannelDigestMetadata(t, app, "thread-legacy", map[string]string{"rebuiltFromLiveRows": "true", "throughTranscriptId": "legacy-row-10"})
	if pending := app.channelDigestPendingHistory("thread-legacy", rows, nil, map[string]int{}); len(pending) != 0 {
		t.Fatalf("floorless legacy rebuild queued %v", memoryEntryIDs(pending))
	}
}

// The chunk a legacy rebuild digest seeds must STAMP the chain floor it was
// computed against: leaving it unset let the next pass fall through to "no
// floor" and fold everything from the high-water through the newest live row.
func TestChannelDigestCatchUpStampsChainFloorFromLegacyRebuild(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	thread, rows := seedChannelDigestThread(t, app, "channel-legacy-rebuild", "Legacy", 8)
	seedChannelDigestMetadata(t, app, thread.ID, map[string]string{
		"rebuiltFromLiveRows":      "true",
		"fromTranscriptId":         rows[5].ID,
		"throughTranscriptId":      rows[7].ID,
		digestSpanStartMetadataKey: rows[5].CreatedAt.UTC().Format(time.RFC3339),
		digestSpanEndMetadataKey:   rows[7].CreatedAt.UTC().Format(time.RFC3339),
	})

	var seen []string
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		seen = append(seen, request.Input)
		return cannedMeetingDigestJSON(), nil
	}
	if _, err := app.produceChannelDigests(context.Background(), "test-key", nil, responder); err != nil {
		t.Fatalf("catch-up pass: %v", err)
	}
	if len(seen) != 1 {
		t.Fatalf("calls=%d, want one history chunk", len(seen))
	}
	if !strings.Contains(seen[0], "Row 001:") || strings.Contains(seen[0], "Row 006:") {
		t.Fatalf("chunk must hold only the rows below the legacy rebuild's floor:\n%s", seen[0])
	}
	digest, ok := app.memory.currentDigest(meetingMemoryKindChannelDigest, thread.ID)
	if !ok || digest.Metadata[channelDigestHistoryEndMetadataKey] != rows[5].ID ||
		digest.Metadata[channelDigestSeedThroughMetadataKey] != rows[4].ID || digest.Metadata[channelDigestSeedPendingMetadataKey] != "0" {
		t.Fatalf("digest=%v ok=%v, want the resolved chain floor %s carried onto the chunk", digest.Metadata, ok, rows[5].ID)
	}
	if _, err := app.produceChannelDigests(context.Background(), "test-key", nil, responder); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if len(seen) != 1 {
		t.Fatalf("a caught-up thread re-folded its history: calls=%d", len(seen))
	}
}

// Every catch-up chunk is a provider call and the whole pass shares ONE
// requestTimeout, so the catch-up is capped per pass — deferred threads catch
// up on later passes instead of walking the pass into its own deadline (which
// the runner classifies as a provider hold on the way to the restart-required
// circuit).
func TestChannelDigestCatchUpIsCappedPerPass(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	setupAuthTestEnv(t)
	t.Setenv("CHANNEL_DIGEST_MAX_CATCHUP_THREADS_PER_TICK", "2")
	app := newIsolatedKanbanBoardApp(t)
	for index := 1; index <= 5; index++ {
		seedChannelDigestThread(t, app, fmt.Sprintf("channel-capped-%02d", index), fmt.Sprintf("Capped %02d", index), 3)
	}
	if cap := channelDigestMaxCatchUpThreadsPerTick(); cap != 2 {
		t.Fatalf("cap=%d, want the env override", cap)
	}
	threads := map[string]int{}
	calls := 0
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		for _, line := range strings.Split(request.Input, "\n") {
			if strings.HasPrefix(line, "thread id: ") {
				threads[strings.TrimPrefix(line, "thread id: ")]++
			}
		}
		return cannedMeetingDigestJSON(), nil
	}
	for pass, want := range []int{2, 4, 5, 5} {
		if _, err := app.produceChannelDigests(context.Background(), "test-key", nil, responder); err != nil {
			t.Fatalf("pass %d: %v", pass+1, err)
		}
		if calls != want {
			t.Fatalf("after pass %d calls=%d, want %d (at most 2 threads caught up per pass)", pass+1, calls, want)
		}
	}
	if len(threads) != 5 {
		t.Fatalf("threads caught up=%d, want all 5 across successive passes", len(threads))
	}
	for threadID, count := range threads {
		if count != 1 {
			t.Fatalf("thread %s folded %d times, want exactly one chunk", threadID, count)
		}
	}
}

// A history chunk folds OLD rows under a digest that already reaches further
// forward: the upsert must keep the NEWER span/day/through stamps, or a thread
// mid-catch-up advertises a current digest that claims to end in the past.
func TestChannelDigestCatchUpKeepsNewerDigestReach(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	thread, rows := seedChannelDigestThread(t, app, "channel-reach", "Reach", 8)
	priorEnd := rows[7].CreatedAt.UTC().Add(26 * time.Hour).Truncate(time.Second)
	priorDay := dayBucket(priorEnd)
	seedChannelDigestMetadata(t, app, thread.ID, map[string]string{
		channelDigestHistoryEndMetadataKey: rows[6].ID,
		"fromTranscriptId":                 rows[6].ID,
		"throughTranscriptId":              rows[7].ID,
		"messageCount":                     "2",
		digestDayMetadataKey:               priorDay,
		digestSpanStartMetadataKey:         rows[6].CreatedAt.UTC().Format(time.RFC3339),
		digestSpanEndMetadataKey:           priorEnd.Format(time.RFC3339),
	})

	calls := 0
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		if !strings.Contains(request.Input, "Row 001:") || strings.Contains(request.Input, "Row 007:") {
			t.Fatalf("chunk must fold only the history below the floor:\n%s", request.Input)
		}
		return cannedMeetingDigestJSON(), nil
	}
	if _, err := app.produceChannelDigests(context.Background(), "test-key", nil, responder); err != nil {
		t.Fatalf("catch-up pass: %v", err)
	}
	digest, ok := app.memory.currentDigest(meetingMemoryKindChannelDigest, thread.ID)
	if !ok || calls != 1 {
		t.Fatalf("calls=%d digest ok=%v", calls, ok)
	}
	if digest.Metadata[digestSpanEndMetadataKey] != priorEnd.Format(time.RFC3339) || digest.Metadata[digestDayMetadataKey] != priorDay ||
		digest.Metadata["throughTranscriptId"] != rows[7].ID {
		t.Fatalf("digest=%v, want the prior's reach kept (spanEnd=%s day=%s through=%s)", digest.Metadata, priorEnd.Format(time.RFC3339), priorDay, rows[7].ID)
	}
	if digest.Metadata["fromTranscriptId"] != rows[0].ID || digest.Metadata["messageCount"] != "8" {
		t.Fatalf("digest=%v, want cumulative coverage from %s over 8 rows", digest.Metadata, rows[0].ID)
	}
	// the chunk's own reach stays visible under the seed keys.
	if digest.Metadata[channelDigestSeedThroughMetadataKey] != rows[5].ID || digest.Metadata[channelDigestSeedPendingMetadataKey] != "0" ||
		digest.Metadata[channelDigestHistoryEndMetadataKey] != rows[6].ID {
		t.Fatalf("digest=%v, want the catch-up bookkeeping intact", digest.Metadata)
	}
	if payload, parsed := parseMeetingDigest(digest.Text); !parsed || payload.Day != priorDay {
		t.Fatalf("payload day=%q parsed=%v, want the newer day %q", payload.Day, parsed, priorDay)
	}
}

// The refusal must survive the NEXT window pass: a legacy thread that posts
// again is folded by an ordinary window digest, and if that digest stamped its
// own window head as the chain floor, the pass after it would queue the whole
// already-summarized history as "pending" — finding 1 one pass later.
func TestChannelDigestWindowPassOverLegacyDigestStaysCaughtUp(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	thread, rows := seedChannelDigestThread(t, app, "channel-legacy-window", "Legacy window", 10)
	// the production shape: the last window folded rows 9-10 and stamped only
	// the legacy keys.
	seedChannelDigestMetadata(t, app, thread.ID, map[string]string{
		"fromTranscriptId":         rows[8].ID,
		"throughTranscriptId":      rows[9].ID,
		"messageCount":             "2",
		digestSpanStartMetadataKey: rows[8].CreatedAt.UTC().Format(time.RFC3339),
		digestSpanEndMetadataKey:   rows[9].CreatedAt.UTC().Format(time.RFC3339),
	})
	calls := 0
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		if strings.Contains(request.Input, "Row 001:") {
			t.Fatalf("call %d re-fed already-summarized history:\n%s", calls, request.Input)
		}
		return cannedMeetingDigestJSON(), nil
	}
	// drained pass: nothing to catch up.
	if _, err := app.produceChannelDigests(context.Background(), "test-key", nil, responder); err != nil || calls != 0 {
		t.Fatalf("drained pass calls=%d err=%v, want a legacy thread left alone", calls, err)
	}
	// a new message arrives: the ordinary window pass folds it...
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", thread.ID, scoutChatMessageRecord{
		ID: "channel-legacy-window-msg-new", Kind: "message", Role: "user", Text: "Row 011: decision 011 stands.",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: "aj@shareability.com",
	}); err != nil {
		t.Fatalf("post new message: %v", err)
	}
	live := app.liveChannelThreadRows(thread.ID, 0)
	window := []meetingMemoryEntry{live[len(live)-1]}
	if _, err := app.produceChannelDigests(context.Background(), "test-key", window, responder); err != nil {
		t.Fatalf("window pass: %v", err)
	}
	if calls != 1 {
		t.Fatalf("window pass calls=%d, want one", calls)
	}
	digest, ok := app.memory.currentDigest(meetingMemoryKindChannelDigest, thread.ID)
	if !ok || digest.Metadata[channelDigestHistoryEndMetadataKey] != "" {
		t.Fatalf("digest=%v ok=%v, want no chain floor invented from the window head", digest.Metadata, ok)
	}
	// ...and the pass after it still has nothing pending.
	if _, err := app.produceChannelDigests(context.Background(), "test-key", nil, responder); err != nil || calls != 1 {
		t.Fatalf("pass after the window digest calls=%d err=%v, want the history still caught up", calls, err)
	}
}

// PRODUCTION REPRO (gen 249, verified 2026-09-03). Release 8c7344d1 shipped the
// first-run anchor and it ran ZERO times: the live hold file still carried the
// PREVIOUS release's checkpoint for this exact scope — blockedReason
// durable_cursor_ambiguous, no firstRunAnchor field, no held window — and
// bootstrapAmbientContinuity returned that persisted block before it ever
// reached the anchor. The store held ZERO channel_digest_pass artifacts, so the
// anchor's own already-produced guard was NOT what blocked it. That made the
// blocked state permanent: no future release could have repaired it. This pins
// the exact production shape end to end — stale block in; anchored, run and
// recorded out — and pins the fail-closed guard that must survive it.
func TestChannelDigestStaleBlockedCheckpointAnchorsForNeverProducedScope(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	t.Setenv("CHANNEL_DIGEST_MIN_MESSAGES", "1")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))

	seed := newKanbanBoardApp()
	alpha := seedPublicChannelMessages(t, seed, "channel-stale-block-alpha", "Alpha",
		"Zebra confirmed the packaging pilot kicks off next Monday.",
		"AJ: we choose vendor Zebra for the packaging pilot.")
	beta := seedPublicChannelMessages(t, seed, "channel-stale-block-beta", "Beta",
		"Kappa wants a call about the retainer.")
	holdPath := seed.ambientHeldWindowPath()
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	// The live gen-248 window, field for field as production carries it.
	agent := channelDigestAgent()
	scope := ambientAgentScopeKey(agent, officeRoomID)
	stale := ambientHeldWindow{
		Agent:             agent.name,
		RoomID:            officeRoomID,
		InputKind:         agent.inputKind,
		ArtifactKind:      agent.artifactKind,
		CursorMetadataKey: agent.cursorMetadataKey,
		WindowID:          "",
		BlockedReason:     ambientContinuityAmbiguous,
	}
	if err := persistAmbientHeldWindowState(holdPath, ambientHeldWindowState{
		Version: 1, Windows: map[string]ambientHeldWindow{scope: stale},
	}); err != nil {
		t.Fatalf("persist the stale pre-release checkpoint: %v", err)
	}

	app := newKanbanBoardApp()
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() {
		kanbanApp = previousApp
		_ = app.Close()
	})

	app.memory.mu.RLock()
	passes, preBootInput := 0, app.memory.bootLatestIDs[meetingMemoryKindTranscript]
	for _, entry := range app.memory.entries {
		if entry.Kind == meetingMemoryKindChannelDigestPass {
			passes++
		}
	}
	app.memory.mu.RUnlock()
	if passes != 0 || preBootInput == "" {
		t.Fatalf("store passes=%d preBootInput=%q, want production's shape: never produced, pre-boot input waiting", passes, preBootInput)
	}

	// BEFORE: blocked — but visibly blocked-and-anchorable, not blocked-and-stuck.
	// Production could only be diagnosed because firstRunAnchorScopes was
	// ABSENT; an opted-in worker now publishes it at zero instead.
	before := ambientWorkerCheckpointDiagnostics(app, agent)
	if before["checkpointStatus"] != "blocked_anchorable" || before["blockedScopeCount"] != 1 ||
		before["blockedAnchorableScopes"] != 1 || before["firstRunAnchorScopes"] != 0 || before["ambientContinuityHealthy"] != false {
		t.Fatalf("pre-anchor diagnostics=%v, want a blocked-but-anchorable scope publishing the anchor key at zero", before)
	}
	scopes, _ := before["continuityScopes"].([]map[string]any)
	if len(scopes) != 1 || scopes[0]["anchorable"] != true || scopes[0]["blockedReason"] != ambientContinuityAmbiguous {
		t.Fatalf("pre-anchor continuity scopes=%v, want the stale ambiguous scope marked anchorable", scopes)
	}

	// THE FIX: the stale block is superseded, not obeyed.
	baseline, blocked, err := app.bootstrapAmbientContinuity(agent, officeRoomID)
	if err != nil || blocked != "" || baseline != preBootInput {
		t.Fatalf("baseline=%q blocked=%q err=%v, want the stale block superseded by the pre-boot anchor %q", baseline, blocked, err, preBootInput)
	}
	checkpoint, ok, err := app.ambientScopeCheckpoint(scope)
	if err != nil || !ok || !checkpoint.FirstRunAnchor || checkpoint.BlockedReason != "" || checkpoint.BaselineID != preBootInput {
		t.Fatalf("checkpoint=%+v ok=%v err=%v, want the supersession recorded and the block cleared", checkpoint, ok, err)
	}
	// Idempotent: the marker it just wrote is the anchor-loop stop.
	if anchor, ok := app.ambientFirstRunAnchorSupersedesCheckpoint(agent, officeRoomID, checkpoint); ok {
		t.Fatalf("a marked checkpoint re-anchored to %q; FirstRunAnchor must stop a second supersession", anchor)
	}
	app.setAmbientAgentBaselineID(agent.name, checkpoint.BaselineID)

	// ...and the pass then actually runs: alpha posts again, so its pre-boot
	// history is caught up as a chunk before the window group folds the new
	// message; beta is caught up from the drained scan.
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", alpha.ID, scoutChatMessageRecord{
		ID: "channel-stale-block-alpha-msg-new", Kind: "message", Role: "user", Text: "Zebra signed; pilot starts Monday for real.",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: "aj@shareability.com",
	}); err != nil {
		t.Fatalf("post new alpha message: %v", err)
	}
	calls := map[string]int{}
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		for _, threadID := range []string{alpha.ID, beta.ID} {
			if strings.Contains(request.Input, "thread id: "+threadID) {
				calls[threadID]++
			}
		}
		return cannedMeetingDigestJSON(), nil
	}
	pass, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "test-key", responder, agent.minBatch(), officeRoomID)
	if err != nil {
		t.Fatalf("first pass after the supersession: %v", err)
	}
	if calls[alpha.ID] != 2 || calls[beta.ID] != 1 {
		t.Fatalf("calls alpha=%d beta=%d, want alpha's history chunk + window and beta's chunk", calls[alpha.ID], calls[beta.ID])
	}
	if pass.Kind != meetingMemoryKindChannelDigestPass || pass.Metadata[channelDigestCursorMetadataKey] == "" {
		t.Fatalf("pass=%+v, want the cursor advanced past the new message", pass)
	}
	for _, thread := range []scoutChatThreadRecord{alpha, beta} {
		digest, ok := app.memory.currentDigest(meetingMemoryKindChannelDigest, thread.ID)
		if !ok || digest.Metadata[channelDigestSeedPendingMetadataKey] != "0" {
			t.Fatalf("thread %s digest=%v ok=%v, want it caught up with nothing pending", thread.ID, digest.Metadata, ok)
		}
	}
	app.mu.Lock()
	failure := app.agentFailures[scope]
	app.mu.Unlock()
	if failure != nil {
		t.Fatalf("the anchored pass recorded a failure: %+v", failure)
	}
	health := ambientWorkerCapabilitySnapshot(agent, time.Now().UTC(), providerOpenAI, true)
	if health["firstRunAnchorScopes"] != 1 || health["checkpointStatus"] != "ready" || health["blockedScopeCount"] != 0 ||
		health["seedPendingRows"] != 0 || health["continuityError"] == true || health["circuit"] == "continuity_error" {
		t.Fatalf("post-anchor snapshot=%v, want one recorded anchor, a ready checkpoint and nothing pending", health)
	}

	// Fail-closed is untouched: the scope has now produced, so the same stale
	// block shape can never be anchored past again — with or without the marker.
	if anchor, ok := app.ambientFirstRunAnchorSupersedesCheckpoint(agent, officeRoomID, stale); ok {
		t.Fatalf("anchor=%q for a scope holding a %s; a produced scope must never anchor past unprocessed input", anchor, agent.artifactKind)
	}
}
