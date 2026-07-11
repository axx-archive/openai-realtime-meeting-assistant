package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A /goal subtask child (goalParentId set) must NOT fire its own creator
// notification — the parent goal engine notifies once on the terminal state, so
// a revised subtask can't flood "Finished Recently" with v1/v2/v3 pings. A
// standalone thread always notifies.
func TestShouldNotifyAgentThreadCreator(t *testing.T) {
	standalone := meetingMemoryEntry{Metadata: map[string]string{}}
	if !shouldNotifyAgentThreadCreator(standalone) {
		t.Fatal("standalone thread must notify its creator")
	}
	child := meetingMemoryEntry{Metadata: map[string]string{"goalParentId": "agent-thread-goal-1"}}
	if shouldNotifyAgentThreadCreator(child) {
		t.Fatal("goal subtask child must be suppressed (parent notifies once)")
	}
}

// The deliverable flag becomes the goalDeliverable metadata key (the flag the
// runner reads for the heavier budget); an unset flag stamps nothing.
func TestAgentThreadGoalSpecStampsDeliverableFlag(t *testing.T) {
	if got := (agentThreadGoalSpec{Deliverable: true}).metadata()["goalDeliverable"]; got != "true" {
		t.Fatalf("goalDeliverable=%q, want true", got)
	}
	if _, present := (agentThreadGoalSpec{}).metadata()["goalDeliverable"]; present {
		t.Fatal("goalDeliverable stamped on a non-deliverable spec")
	}
}

func TestAgentThreadProducesStructuredArtifactWithResponder(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	thread := scoutAgentThread{
		ID:     "agent-thread-research-1",
		Mode:   "research",
		Query:  "identify the evidence needed for Realtime 2 as UI",
		Status: "running",
	}

	var captured openAITextRequest
	output, err := app.produceAgentThreadArtifact(context.Background(), thread, func(_ context.Context, apiKey string, request openAITextRequest) (string, error) {
		if apiKey != "test-key" {
			t.Fatalf("apiKey=%q, want test-key", apiKey)
		}
		captured = request
		return "Vision: Realtime 2 is the operator UI.\n\nGoal: identify evidence.\n\nVerification: artifact complete.", nil
	})
	if err != nil {
		t.Fatalf("produceAgentThreadArtifact: %v", err)
	}
	if !strings.Contains(output, "Vision: Realtime 2") {
		t.Fatalf("output=%q, want responder output", output)
	}
	for _, want := range []string{"Vision", "Context used", "Review against the original goal", "Gate", "stable headings", "Executive Summary", "Search tags", "Thesis", "Sources", "Do not claim you performed browser"} {
		if !strings.Contains(captured.Instructions, want) {
			t.Fatalf("instructions missing %q: %s", want, captured.Instructions)
		}
	}
	if !strings.Contains(captured.Input, thread.Query) || !strings.Contains(captured.Input, "Board and memory context") {
		t.Fatalf("input=%q, want thread query and context", captured.Input)
	}
}

func TestAgentThreadModeContractsDifferentiateResearchAndDesign(t *testing.T) {
	for _, tt := range []struct {
		mode string
		want []string
	}{
		{mode: "research", want: []string{"Executive Summary", "Thesis", "Evidence", "Sources", "Counterarguments", "Recommendation", "Search tags"}},
		{mode: "design", want: []string{"Design intent", "Context and research used", "Core screens", "Responsive behavior", "Implementation handoff"}},
	} {
		got := agentThreadModeContract(tt.mode)
		for _, want := range tt.want {
			if !strings.Contains(got, want) {
				t.Fatalf("mode %s contract missing %q: %s", tt.mode, want, got)
			}
		}
	}
}

func TestAgentThreadResearchModeCarriesArtifactContractMetadata(t *testing.T) {
	metadata := agentThreadModeMetadata("research")
	if metadata["artifactContract"] != "research_brief_v2" {
		t.Fatalf("artifactContract=%q, want research_brief_v2", metadata["artifactContract"])
	}
	for _, want := range []string{"executive summary", "sources", "worker evidence"} {
		if !strings.Contains(metadata["artifactHeadings"], want) {
			t.Fatalf("artifactHeadings missing %q: %s", want, metadata["artifactHeadings"])
		}
	}
	if got := agentThreadModeMetadata("design"); got != nil {
		t.Fatalf("design metadata=%v, want nil", got)
	}
}

// Origin metadata is stamped at launch so completion can close the loop —
// and only the three origin keys survive into artifact metadata.
func TestLaunchAgentThreadWithOriginStampsOnlyOriginKeys(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	meetingID := app.memory.ensureMeetingID(officeRoomID)
	thread, err := app.launchAgentThreadWithOrigin("research", "map the delivery loop", "AJ", map[string]string{
		"originKind":      agentThreadOriginRoom,
		"originId":        "codex-proposal-42",
		"originMeetingId": meetingID,
		"stray":           "must be dropped",
	})
	if err != nil {
		t.Fatalf("launchAgentThreadWithOrigin: %v", err)
	}
	metadata := thread.Artifact.Metadata
	if metadata["originKind"] != agentThreadOriginRoom || metadata["originId"] != "codex-proposal-42" || metadata["originMeetingId"] != meetingID {
		t.Fatalf("origin metadata=%#v, want kind/id/meeting stamped", metadata)
	}
	if metadata["stray"] != "" {
		t.Fatalf("stray origin key leaked into metadata: %q", metadata["stray"])
	}

	// The plain launch keeps origin absent — completion stays notification-only.
	plain, err := app.launchAgentThread("research", "no origin here", "AJ")
	if err != nil {
		t.Fatalf("launchAgentThread: %v", err)
	}
	if plain.Artifact.Metadata["originKind"] != "" {
		t.Fatalf("originKind=%q on a plain launch, want empty", plain.Artifact.Metadata["originKind"])
	}
}

// A terminal queued-worker completion derives the display title from the
// finished body; the launch prompt survives as threadQuery.
func TestUpdateQueuedAgentThreadDerivesTitleOnCompletion(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	query := "dig into coyote logistics pricing and produce a brief"
	thread, err := app.launchAgentThread("research", query, "AJ")
	if err != nil {
		t.Fatalf("launchAgentThread: %v", err)
	}
	if got := thread.Artifact.Metadata["title"]; !strings.EqualFold(got, query) {
		// creation-time fallback stays the compacted prompt
		t.Fatalf("scaffold title=%q, want the launch prompt %q", got, query)
	}

	app.updateQueuedAgentThread(thread, agentThreadWorkerResult{
		Text:     "# Coyote pricing teardown\n\nExecutive Summary: margins compress.",
		Metadata: map[string]string{"status": codexJobStatusComplete, "threadStatus": codexJobStatusComplete},
		Terminal: true,
	})

	stored, ok := app.osArtifactByID(thread.Artifact.ID)
	if !ok {
		t.Fatalf("artifact %s disappeared", thread.Artifact.ID)
	}
	if stored.Metadata["title"] != "Coyote pricing teardown" {
		t.Fatalf("title=%q, want derived from the body heading", stored.Metadata["title"])
	}
	if stored.Metadata["titleSource"] != "derived" {
		t.Fatalf("titleSource=%q, want derived", stored.Metadata["titleSource"])
	}
	if stored.Metadata["threadQuery"] != query {
		t.Fatalf("threadQuery=%q, want the durable launch prompt %q", stored.Metadata["threadQuery"], query)
	}

	// A non-terminal status update keeps whatever title the artifact carries.
	app.updateQueuedAgentThread(thread, agentThreadWorkerResult{
		Metadata: map[string]string{"status": codexJobStatusRunning, "threadStatus": codexJobStatusRunning},
	})
	stored, _ = app.osArtifactByID(thread.Artifact.ID)
	if stored.Metadata["title"] != "Coyote pricing teardown" {
		t.Fatalf("title=%q after status update, want unchanged", stored.Metadata["title"])
	}
}

// Room-origin completion posts exactly one compact card into the origin
// meeting's chat (via the transcript-entering path) and never delivers twice.
func TestDeliverArtifactToOriginRoomPostsCardOnce(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	meetingID := app.memory.ensureMeetingID(officeRoomID)
	artifact, _, err := app.createOSArtifactWithMetadata("research", "coyote pricing", "# Coyote pricing teardown\n\nEvidence.", "AJ", map[string]string{
		"title":           "Coyote pricing teardown",
		"threadStatus":    "complete",
		"status":          "complete",
		"originKind":      agentThreadOriginRoom,
		"originMeetingId": meetingID,
	})
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	app.deliverArtifactToOrigin(artifact, "agent-thread-research-1")

	history := app.roomChatHistory(10)
	if len(history) != 1 {
		t.Fatalf("room chat history=%d messages, want exactly one delivery card", len(history))
	}
	payload := history[0]
	if asString(payload["artifactId"]) != artifact.ID {
		t.Fatalf("payload=%#v, want artifactId %s for the client chip", payload, artifact.ID)
	}
	if asString(payload["name"]) != scoutParticipantName {
		t.Fatalf("sender=%q, want %q", payload["name"], scoutParticipantName)
	}
	text := asString(payload["text"])
	if !strings.Contains(text, "finished") || !strings.Contains(text, "Coyote pricing teardown") {
		t.Fatalf("delivery text=%q, want finished + title", text)
	}

	stored, ok := app.osArtifactByID(artifact.ID)
	if !ok || stored.Metadata["deliveredAt"] == "" {
		t.Fatalf("metadata=%#v, want deliveredAt stamped", stored.Metadata)
	}

	// A retried completion callback re-reads the stored artifact — deliveredAt
	// makes the second delivery a no-op.
	app.deliverArtifactToOrigin(stored, "agent-thread-research-1")
	if got := len(app.roomChatHistory(10)); got != 1 {
		t.Fatalf("room chat history=%d after retry, want still 1", got)
	}
}

// A room delivery after the origin meeting rotated (archive / idle end) must
// not post into — or fabricate — a new meeting.
func TestDeliverArtifactToOriginSkipsRotatedMeeting(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	meetingID := app.memory.ensureMeetingID(officeRoomID)
	artifact, _, err := app.createOSArtifactWithMetadata("research", "coyote pricing", "# Coyote pricing teardown\n\nEvidence.", "AJ", map[string]string{
		"title":           "Coyote pricing teardown",
		"threadStatus":    "complete",
		"status":          "complete",
		"originKind":      agentThreadOriginRoom,
		"originMeetingId": meetingID,
	})
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	app.memory.rotateMeetingID(officeRoomID)
	app.deliverArtifactToOrigin(artifact, "agent-thread-research-1")

	if got := app.memory.currentMeetingID(officeRoomID); got != "" {
		t.Fatalf("meeting id %q was minted, delivery after rotation must not fabricate a meeting", got)
	}
	stored, _ := app.osArtifactByID(artifact.ID)
	if stored.Metadata["deliveredAt"] != "" {
		t.Fatalf("deliveredAt=%q, want empty when the room delivery was skipped", stored.Metadata["deliveredAt"])
	}
	if got := len(app.roomChatHistory(10)); got != 0 {
		t.Fatalf("room chat history=%d, want no delivery card after rotation", got)
	}
}

// The room delivery append is gated atomically on the origin meeting id: a
// rotation landing between deliverArtifactToOrigin's guard and the append can
// neither mint a phantom meeting nor leak into the successor meeting.
func TestRoomChatDeliveryAppendGatedOnMeetingID(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	meetingID := app.memory.ensureMeetingID(officeRoomID)

	// active origin meeting: the gated append lands.
	if _, ok := app.recordRoomChatMessageWithArtifact(officeRoomID, scoutParticipantName, "finished Research — brief", "os-artifact-live", meetingID); !ok {
		t.Fatal("gated append must land while the origin meeting is active")
	}

	// the id rotates AFTER the caller's guard would have passed: the append
	// skips and must not lazily mint a phantom meeting.
	app.memory.rotateMeetingID(officeRoomID)
	if _, ok := app.recordRoomChatMessageWithArtifact(officeRoomID, scoutParticipantName, "finished Research — brief", "os-artifact-stale", meetingID); ok {
		t.Fatal("gated append landed after the origin meeting rotated")
	}
	if got := app.memory.currentMeetingID(officeRoomID); got != "" {
		t.Fatalf("meeting id %q was minted; the skipped delivery must not fabricate a meeting", got)
	}

	// a successor meeting is running: the stale-origin append must not leak
	// into its transcript stream either.
	successorID := app.memory.ensureMeetingID(officeRoomID)
	if successorID == meetingID {
		t.Fatalf("successor id=%q, want a fresh meeting id", successorID)
	}
	if _, ok := app.recordRoomChatMessageWithArtifact(officeRoomID, scoutParticipantName, "finished Research — brief", "os-artifact-stale-2", meetingID); ok {
		t.Fatal("stale-origin delivery leaked into the successor meeting")
	}
	for _, entry := range app.memory.snapshotForMeeting(successorID, 0) {
		if entry.Metadata["artifactId"] == "os-artifact-stale-2" {
			t.Fatalf("successor meeting carries the stale delivery entry: %#v", entry)
		}
	}
}

// deliverArtifactToOrigin must not resurrect an archived channel: archiving is
// a creator-only action, and the owner-context commit would bypass the
// archived-thread guard every user-facing writer enforces. The creator
// notification remains the completion signal.
func TestDeliverArtifactToOriginSkipsArchivedChannel(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "growth channel", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("createScoutChatThread: %v", err)
	}
	if _, err := app.setScoutChatThreadArchived("aj@shareability.com", channel.ID, true); err != nil {
		t.Fatalf("archive channel: %v", err)
	}

	artifact, _, err := app.createOSArtifactWithMetadata("research", "coyote pricing", "# Coyote pricing teardown\n\nEvidence.", "AJ", map[string]string{
		"title":        "Coyote pricing teardown",
		"threadStatus": "complete",
		"status":       "complete",
		"originKind":   agentThreadOriginChannel,
		"originId":     channel.ID,
	})
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	app.deliverArtifactToOrigin(artifact, "agent-thread-research-arch")

	saved, _, err := app.scoutChatThreadByID("aj@shareability.com", channel.ID)
	if err != nil {
		t.Fatalf("scoutChatThreadByID: %v", err)
	}
	if len(saved.Messages) != 0 {
		t.Fatalf("archived channel messages=%d, want no completion card", len(saved.Messages))
	}
	if saved.ArchivedAt == "" {
		t.Fatal("channel must stay archived")
	}
	stored, _ := app.osArtifactByID(artifact.ID)
	if stored.Metadata["deliveredAt"] != "" {
		t.Fatalf("deliveredAt=%q, want empty when the archived-channel delivery was skipped", stored.Metadata["deliveredAt"])
	}
}

// GATE-FINDINGS G2: a rerun inherits origin metadata only when delivery there
// is still safe for the rerunning user; everything else drops to tool.
func TestRerunOriginForUserConditionalInheritance(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	private, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "growth", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	originArtifact := func(kind string, id string, meetingID string) meetingMemoryEntry {
		metadata := map[string]string{"originKind": kind}
		if id != "" {
			metadata["originId"] = id
		}
		if meetingID != "" {
			metadata["originMeetingId"] = meetingID
		}
		return meetingMemoryEntry{ID: "os-artifact-origin", Metadata: metadata}
	}

	// private-thread origin: the owner inherits it...
	origin := app.rerunOriginForUser(originArtifact(agentThreadOriginPrivateThread, private.ID, ""), "aj@shareability.com")
	if origin["originKind"] != agentThreadOriginPrivateThread || origin["originId"] != private.ID {
		t.Fatalf("owner rerun origin=%v, want the private thread inherited", origin)
	}
	// ...and a NON-owner drops to tool: the rerun must never post into someone
	// else's private thread.
	origin = app.rerunOriginForUser(originArtifact(agentThreadOriginPrivateThread, private.ID, ""), "tim@shareability.com")
	if origin["originKind"] != agentThreadOriginTool || origin["originId"] != "" {
		t.Fatalf("non-owner rerun origin=%v, want originKind tool with no originId", origin)
	}

	// channel origin survives while the channel is public and unarchived...
	origin = app.rerunOriginForUser(originArtifact(agentThreadOriginChannel, channel.ID, ""), "tim@shareability.com")
	if origin["originKind"] != agentThreadOriginChannel || origin["originId"] != channel.ID {
		t.Fatalf("channel rerun origin=%v, want the public channel inherited", origin)
	}
	// ...but an archived channel drops to tool.
	if _, err := app.setScoutChatThreadArchived("aj@shareability.com", channel.ID, true); err != nil {
		t.Fatalf("archive channel: %v", err)
	}
	origin = app.rerunOriginForUser(originArtifact(agentThreadOriginChannel, channel.ID, ""), "tim@shareability.com")
	if origin["originKind"] != agentThreadOriginTool {
		t.Fatalf("archived-channel rerun origin=%v, want tool", origin)
	}

	// room origin survives only while the origin meeting is still active.
	meetingID := app.memory.ensureMeetingID(officeRoomID)
	origin = app.rerunOriginForUser(originArtifact(agentThreadOriginRoom, "", meetingID), "tim@shareability.com")
	if origin["originKind"] != agentThreadOriginRoom || origin["originMeetingId"] != meetingID {
		t.Fatalf("active-room rerun origin=%v, want room inherited", origin)
	}
	app.memory.rotateMeetingID(officeRoomID)
	origin = app.rerunOriginForUser(originArtifact(agentThreadOriginRoom, "", meetingID), "tim@shareability.com")
	if origin["originKind"] != agentThreadOriginTool || origin["originMeetingId"] != "" {
		t.Fatalf("rotated-room rerun origin=%v, want tool", origin)
	}

	// absent / unresolvable origins stay tool.
	origin = app.rerunOriginForUser(meetingMemoryEntry{Metadata: map[string]string{}}, "aj@shareability.com")
	if origin["originKind"] != agentThreadOriginTool {
		t.Fatalf("absent-origin rerun=%v, want tool", origin)
	}
	origin = app.rerunOriginForUser(originArtifact(agentThreadOriginChannel, "scout-chat-missing", ""), "aj@shareability.com")
	if origin["originKind"] != agentThreadOriginTool {
		t.Fatalf("missing-channel rerun origin=%v, want tool", origin)
	}
}

// GATE-FINDINGS G2 end to end: a non-owner rerun of a private-thread-origin
// artifact completes without posting anything into the origin thread.
func TestNonOwnerRerunNeverPostsIntoPrivateOriginThread(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.mu.Lock()
	app.apiKey = "test-key"
	app.mu.Unlock()
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	originalResponder := createOpenAITextResponse
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		return "Vision: rerun complete.\n\nGoal: done.", nil
	}
	t.Cleanup(func() { createOpenAITextResponse = originalResponder })

	private, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	stored := meetingMemoryEntry{Metadata: map[string]string{
		"originKind": agentThreadOriginPrivateThread,
		"originId":   private.ID,
	}}

	// the handler computes the safe origin for the rerunning user, then
	// launches and completes the rerun.
	origin := app.rerunOriginForUser(stored, "tim@shareability.com")
	thread, err := app.launchAgentThreadWithOrigin("research", "rerun the brief", "Tim", origin)
	if err != nil {
		t.Fatalf("launchAgentThreadWithOrigin: %v", err)
	}
	app.runAgentThread(thread)

	completed, ok := app.osArtifactByID(thread.Artifact.ID)
	if !ok || completed.Metadata["threadStatus"] != "complete" {
		t.Fatalf("artifact=%#v, want a completed rerun", completed.Metadata)
	}
	if completed.Metadata["originKind"] != agentThreadOriginTool {
		t.Fatalf("originKind=%q, want tool for the non-owner rerun", completed.Metadata["originKind"])
	}
	saved, _, err := app.scoutChatThreadByID("aj@shareability.com", private.ID)
	if err != nil {
		t.Fatalf("scoutChatThreadByID: %v", err)
	}
	if len(saved.Messages) != 0 {
		t.Fatalf("victim's private thread got %d message(s) from a non-owner rerun, want none", len(saved.Messages))
	}
}

// Channel-origin completion: when the channel already holds the launch card
// (agent ref) the ref rewrite is the delivery — no duplicate; a rerun without
// a ref appends exactly one completion card.
func TestDeliverArtifactToOriginChannelDedupe(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "growth channel", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("createScoutChatThread: %v", err)
	}
	launchRef := &scoutChatThreadRef{ID: "agent-thread-research-9", Mode: "research", Query: "coyote pricing", Status: "running"}
	if _, err := app.commitScoutChatThreadMessages(channel.OwnerEmail, channel.ID, scoutChatMessageRecord{
		ID:        "scout-chat-message-launch",
		Kind:      "thread",
		Role:      "scout",
		Text:      "research thread launched",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Thread:    launchRef,
	}); err != nil {
		t.Fatalf("seed launch card: %v", err)
	}

	artifact, _, err := app.createOSArtifactWithMetadata("research", "coyote pricing", "# Coyote pricing teardown\n\nEvidence.", "AJ", map[string]string{
		"title":        "Coyote pricing teardown",
		"threadStatus": "complete",
		"status":       "complete",
		"originKind":   agentThreadOriginChannel,
		"originId":     channel.ID,
	})
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	// The launch card exists: delivery is the ref rewrite, not a new message.
	app.deliverArtifactToOrigin(artifact, "agent-thread-research-9")
	thread, _, err := app.scoutChatThreadByID(channel.OwnerEmail, channel.ID)
	if err != nil {
		t.Fatalf("scoutChatThreadByID: %v", err)
	}
	if len(thread.Messages) != 1 {
		t.Fatalf("channel messages=%d, want the launch card only (no duplicate)", len(thread.Messages))
	}
	stored, _ := app.osArtifactByID(artifact.ID)
	if stored.Metadata["deliveredAt"] != "" {
		t.Fatalf("deliveredAt=%q, want empty when the existing ref is the delivery", stored.Metadata["deliveredAt"])
	}

	// A rerun completing under a fresh thread id has no in-channel card yet:
	// exactly one completion card lands, then the retry is a no-op.
	app.deliverArtifactToOrigin(stored, "agent-thread-research-10")
	thread, _, err = app.scoutChatThreadByID(channel.OwnerEmail, channel.ID)
	if err != nil {
		t.Fatalf("scoutChatThreadByID after delivery: %v", err)
	}
	if len(thread.Messages) != 2 {
		t.Fatalf("channel messages=%d, want launch card + one completion card", len(thread.Messages))
	}
	card := thread.Messages[len(thread.Messages)-1]
	if card.Kind != "thread" || card.Thread == nil || card.Thread.ArtifactID != artifact.ID || card.Thread.Status != "complete" {
		t.Fatalf("completion card=%#v, want a complete thread ref carrying the artifact id", card)
	}
	if !strings.Contains(card.Text, "finished") || !strings.Contains(card.Text, "Coyote pricing teardown") {
		t.Fatalf("card text=%q, want finished + title", card.Text)
	}

	stored, _ = app.osArtifactByID(artifact.ID)
	if stored.Metadata["deliveredAt"] == "" {
		t.Fatal("deliveredAt must be stamped after the channel delivery")
	}
	app.deliverArtifactToOrigin(stored, "agent-thread-research-10")
	thread, _, _ = app.scoutChatThreadByID(channel.OwnerEmail, channel.ID)
	if len(thread.Messages) != 2 {
		t.Fatalf("channel messages=%d after retry, want still 2", len(thread.Messages))
	}
}

// The live Ember run's death loop, root-caused: agentThreadInstructions
// hard-demands "Markdown sections" and a "one-line Vision" from EVERY child —
// so a writer stage whose contract is a RAW document (packaging_deck_v1: the
// deck HTML file itself) had a system prompt at war with its stage prompt,
// and the model obeyed the system prompt 4 rounds straight into a block. A
// raw-document output contract must REPLACE the generic instructions.
func TestRawDocumentContractOverridesGenericInstructions(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousAsync := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousAsync })

	deck, err := app.launchAgentThreadWithSpec("artifacts", "Ship — the self-contained presenter deck", "AJ", nil, agentThreadGoalSpec{
		OutputContract: "packaging_deck_v1",
		Deliverable:    true,
	})
	if err != nil {
		t.Fatalf("launch deck child: %v", err)
	}
	if deck.Artifact.Metadata["outputContract"] != "packaging_deck_v1" {
		t.Fatalf("outputContract=%q, want the stage contract stamped on the child", deck.Artifact.Metadata["outputContract"])
	}
	instructions := app.agentThreadInstructionsForThread(deck)
	for _, banned := range []string{"Markdown sections", "one-line Vision", "Work decomposition"} {
		if strings.Contains(instructions, banned) {
			t.Fatalf("raw-document child still carries the generic instruction %q:\n%s", banned, instructions)
		}
	}
	if !strings.Contains(instructions, "<!doctype html>") {
		t.Fatalf("raw-document instructions must demand the doctype-first file:\n%s", instructions)
	}

	// A plain child keeps the generic workflow instructions byte-identical.
	plain, err := app.launchAgentThreadWithSpec("artifacts", "meeting notes summary", "AJ", nil, agentThreadGoalSpec{})
	if err != nil {
		t.Fatalf("launch plain child: %v", err)
	}
	if got := app.agentThreadInstructionsForThread(plain); !strings.Contains(got, "Markdown sections") {
		t.Fatalf("plain child lost the generic instructions:\n%s", got)
	}
}

// The founder's "alert us when done": a channel-origin completion must fire a
// COMPANY-WIDE broadcast notification (UserEmail "") — and it must fire even in
// the common case where launchApprovedProposal already posted the live launch
// card, which trips the duplicate-card dedup guard. Before the fix the alert
// sat inside that guarded block and never fired for the primary path.
func TestDeliverArtifactToChannelBroadcastsCompletionDespiteExistingLaunchCard(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Samsung", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("createScoutChatThread: %v", err)
	}
	// Seed the launch card so the dedup guard WILL trip — the case that used to
	// swallow the notification.
	if _, err := app.commitScoutChatThreadMessages(channel.OwnerEmail, channel.ID, scoutChatMessageRecord{
		ID:        "scout-chat-message-launch",
		Kind:      "thread",
		Role:      "scout",
		Text:      "research thread launched",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Thread:    &scoutChatThreadRef{ID: "agent-thread-research-77", Mode: "research", Query: "samsung tv", Status: "running"},
	}); err != nil {
		t.Fatalf("seed launch card: %v", err)
	}

	artifact, _, err := app.createOSArtifactWithMetadata("research", "samsung tv", "# Samsung TV audience\n\nEvidence.", "AJ", map[string]string{
		"title":        "Samsung TV audience report",
		"threadStatus": "complete",
		"status":       "complete",
		"originKind":   agentThreadOriginChannel,
		"originId":     channel.ID,
	})
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	before := len(app.notifications)
	app.deliverArtifactToOrigin(artifact, "agent-thread-research-77") // dedup guard trips (launch card exists)

	var broadcast *notificationRecord
	for i := before; i < len(app.notifications); i++ {
		rec := app.notifications[i]
		if rec.UserEmail == "" && strings.Contains(rec.Text, "Samsung TV audience report") {
			broadcast = &app.notifications[i]
			break
		}
	}
	if broadcast == nil {
		t.Fatalf("no company-wide completion notification fired (records added: %d); the alert must survive the dedup guard", len(app.notifications)-before)
	}
	if broadcast.ArtifactID != artifact.ID || broadcast.ThreadID != channel.ID {
		t.Fatalf("notification links wrong: artifact=%q thread=%q, want %q / %q", broadcast.ArtifactID, broadcast.ThreadID, artifact.ID, channel.ID)
	}
	if !strings.Contains(broadcast.Text, "#Samsung") {
		t.Fatalf("notification text=%q, want it to name the channel", broadcast.Text)
	}
}

// A channel-origin launch (what approving a proposal now produces) must NOT
// carry navigation actions in its room-wide broadcast — otherwise every client
// in the room gets yanked to the chat tab. Room/tool origins keep their actions
// (the initiator's own navigation rides a separate direct response).
func TestBroadcastNavigationActionsDropsChannelOriginNav(t *testing.T) {
	actions := []osAssistantAction{{Type: "open_tool", Tool: "chat", ArtifactID: "os-artifact-1"}}

	if got := broadcastNavigationActions(agentThreadOriginChannel, actions); got != nil {
		t.Fatalf("channel origin should drop broadcast navigation actions, got %+v", got)
	}
	if got := broadcastNavigationActions(agentThreadOriginRoom, actions); len(got) != 1 {
		t.Fatalf("room origin should keep its actions, got %+v", got)
	}
	if got := broadcastNavigationActions(agentThreadOriginTool, actions); len(got) != 1 {
		t.Fatalf("tool origin should keep its actions, got %+v", got)
	}
	if got := broadcastNavigationActions("", actions); len(got) != 1 {
		t.Fatalf("empty origin should keep its actions (today's default), got %+v", got)
	}
}

// buildAgentThreadError must not tell the room to "run the Codex handoff" as the
// remedy for a worker error: research/design threads run on the in-process Fable
// orchestrator, and that misleading line made a live meeting believe a failed
// research report was a Codex problem. It must surface the real error and point
// at a retry, not an external Codex worker.
func TestBuildAgentThreadErrorDoesNotPrescribeCodexHandoff(t *testing.T) {
	thread := scoutAgentThread{
		ID:    "agent-thread-research-1",
		Mode:  "research",
		Query: "run a research report on Samsung TV audience",
	}
	body := buildAgentThreadError(thread, errors.New("api request failed (400 Bad Request)"))
	if !strings.Contains(body, "400 Bad Request") {
		t.Fatalf("error body should surface the real worker error, got:\n%s", body)
	}
	if strings.Contains(body, "run the Codex/MCP handoff") || strings.Contains(body, "reconnect the worker or run the Codex") {
		t.Fatalf("error body must not prescribe a Codex handoff as the remedy:\n%s", body)
	}
	if !strings.Contains(strings.ToLower(body), "retry the run") {
		t.Fatalf("error body should point at a retry, got:\n%s", body)
	}
}

// research_brief_v2 bodies open with contract headings, which titled a live
// completed report "Executive Summary". A derived title that is a generic
// contract heading (any mode's contract, case-insensitive) falls back to the
// launch query / stored title; a real subject heading still wins.
func TestAgentThreadDisplayTitleRejectsGenericContractHeadings(t *testing.T) {
	for _, tt := range []struct {
		name     string
		body     string
		fallback string
		want     string
	}{
		{
			name:     "research contract heading falls back to the launch query",
			body:     "# Executive Summary\n\nSamsung is exposed on HBM4 supply.",
			fallback: "Samsung HBM4 exposure",
			want:     "Samsung HBM4 exposure",
		},
		{
			name:     "matching is case-insensitive",
			body:     "## EXECUTIVE SUMMARY\n\nbody",
			fallback: "the launch query",
			want:     "the launch query",
		},
		{
			name:     "workflow contract heading falls back",
			body:     "# Vision\n\nbody",
			fallback: "package the Aurora IP",
			want:     "package the Aurora IP",
		},
		{
			name:     "grill contract heading falls back",
			body:     "## Strongest objections\n\nbody",
			fallback: "grill the Q3 pitch",
			want:     "grill the Q3 pitch",
		},
		{
			name:     "real subject heading still wins",
			body:     "# Samsung HBM4 supply outlook\n\nbody",
			fallback: "the launch query",
			want:     "Samsung HBM4 supply outlook",
		},
		{
			name:     "generic derivation with empty fallback keeps the stored title",
			body:     "# Overview\n\nbody",
			fallback: "",
			want:     "",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentThreadDisplayTitle(tt.body, tt.fallback); got != tt.want {
				t.Fatalf("agentThreadDisplayTitle=%q, want %q", got, tt.want)
			}
		})
	}
}

// The W0 trigger-surface mapping: fine-grained originSurface stamps win,
// coarse originKind decides otherwise, and a bare launch reads as palette.
func TestAgentThreadTriggerSurfaceMapping(t *testing.T) {
	cases := []struct {
		name     string
		metadata map[string]string
		want     string
	}{
		{"chat surface", map[string]string{"originSurface": "chat:thread-1"}, triggerSurfaceChatRouter},
		{"goal surface", map[string]string{"originSurface": "goal_door"}, triggerSurfaceGoalDoor},
		{"palette surface", map[string]string{"originSurface": "palette"}, triggerSurfacePalette},
		{"scheduler surface", map[string]string{"originSurface": "scheduler"}, triggerSurfaceScheduler},
		{"suggestion surface", map[string]string{"originSurface": "suggestion_agent"}, triggerSurfaceSuggestionAgent},
		{"channel origin", map[string]string{"originKind": agentThreadOriginChannel}, triggerSurfaceChannel},
		{"room origin", map[string]string{"originKind": agentThreadOriginRoom}, triggerSurfaceRoomVoice},
		{"private thread origin", map[string]string{"originKind": agentThreadOriginPrivateThread}, triggerSurfaceChatRouter},
		{"bare launch", map[string]string{}, triggerSurfacePalette},
	}
	for _, testCase := range cases {
		if got := agentThreadTriggerSurface(testCase.metadata); got != testCase.want {
			t.Fatalf("%s: surface=%q, want %q", testCase.name, got, testCase.want)
		}
	}
}

func TestLaunchAgentThreadRecordsWorkflowProvenance(t *testing.T) {
	dir := boardWorkerLedgerDir(t)
	app := newIsolatedKanbanBoardApp(t)
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) {}
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	thread, err := app.launchAgentThreadWithOrigin("research", "map the churn drivers", "AJ", map[string]string{
		"originKind": agentThreadOriginChannel,
		"originId":   "channel-1",
	})
	if err != nil {
		t.Fatalf("launchAgentThreadWithOrigin: %v", err)
	}

	rows := readLedgerLines(t, filepath.Join(dir, "eval-2026-07-11.jsonl"))
	var run map[string]any
	var launched map[string]any
	for _, row := range rows {
		switch row["type"] {
		case telemetryTypeWorkflowRun:
			run = row
		case telemetryTypeProposal:
			if row["kind"] == proposalEventLaunched {
				launched = row
			}
		}
	}
	if run == nil {
		t.Fatalf("no workflow_run event recorded at launch; rows=%v", rows)
	}
	entry := run["fields"].(map[string]any)["run"].(map[string]any)
	if entry["workflow_id"] != "agent_thread_research" {
		t.Fatalf("workflow_id=%v, want agent_thread_research", entry["workflow_id"])
	}
	if entry["trigger_surface"] != triggerSurfaceChannel {
		t.Fatalf("trigger_surface=%v, want %q", entry["trigger_surface"], triggerSurfaceChannel)
	}
	if entry["outcome"] != workflowOutcomeLaunched {
		t.Fatalf("outcome=%v, want %q", entry["outcome"], workflowOutcomeLaunched)
	}
	if entry["thread_id"] != thread.ID {
		t.Fatalf("thread_id=%v, want %q", entry["thread_id"], thread.ID)
	}
	if entry["proposer"] != "AJ" {
		t.Fatalf("proposer=%v, want AJ", entry["proposer"])
	}
	if launched == nil {
		t.Fatal("no proposal launched event recorded at launch")
	}
	launchedFields := launched["fields"].(map[string]any)
	if launchedFields["path"] != triggerSurfaceChannel || launchedFields["thread_id"] != thread.ID {
		t.Fatalf("launched fields=%v, want channel path + thread id", launchedFields)
	}
}

func TestAgentRunLogRecordsTerminalProvenance(t *testing.T) {
	dir := boardWorkerLedgerDir(t)
	app := newIsolatedKanbanBoardApp(t)

	artifact := meetingMemoryEntry{
		ID: "artifact-1",
		Metadata: map[string]string{
			"startedAt":    time.Now().UTC().Add(-90 * time.Second).Format(time.RFC3339Nano),
			"proposalId":   "codex-proposal-1",
			"approvalLane": "standard",
			"originKind":   agentThreadOriginRoom,
			"title":        "Churn brief",
		},
	}
	thread := scoutAgentThread{ID: "agent-thread-research-1", Mode: "research", Query: "churn", Artifact: artifact}
	app.appendAgentRunLogEntry(thread, artifact, "complete", "## Executive Summary\nDone.")

	rows := readLedgerLines(t, filepath.Join(dir, "eval-2026-07-11.jsonl"))
	var run map[string]any
	var terminal map[string]any
	for _, row := range rows {
		switch row["type"] {
		case telemetryTypeWorkflowRun:
			run = row
		case telemetryTypeProposal:
			if row["kind"] == proposalEventTerminal {
				terminal = row
			}
		}
	}
	if run == nil {
		t.Fatalf("no terminal workflow_run event; rows=%v", rows)
	}
	entry := run["fields"].(map[string]any)["run"].(map[string]any)
	if entry["outcome"] != workflowOutcomeCompleted {
		t.Fatalf("outcome=%v, want %q", entry["outcome"], workflowOutcomeCompleted)
	}
	if entry["proposal_id"] != "codex-proposal-1" {
		t.Fatalf("proposal_id=%v, want codex-proposal-1", entry["proposal_id"])
	}
	if entry["trigger_surface"] != triggerSurfaceRoomVoice {
		t.Fatalf("trigger_surface=%v, want %q", entry["trigger_surface"], triggerSurfaceRoomVoice)
	}
	if duration, _ := entry["duration_ms"].(float64); duration <= 0 {
		t.Fatalf("duration_ms=%v, want > 0 from the startedAt stamp", entry["duration_ms"])
	}
	if terminal == nil {
		t.Fatal("no proposal terminal event recorded")
	}
	terminalFields := terminal["fields"].(map[string]any)
	if terminalFields["proposal_id"] != "codex-proposal-1" || terminalFields["outcome"] != "complete" {
		t.Fatalf("terminal fields=%v, want proposal id + complete outcome", terminalFields)
	}
}
