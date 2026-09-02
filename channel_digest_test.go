package main

import (
	"context"
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
