package main

// Regressions for the gen-248 `continuity_error` follow-up (2026-09-02):
// the ambient continuity resolver graded healthy legacy scopes ambiguous, and
// the per-room lazy bootstrap surfaced that on the FIRST ticker sweep after a
// successful office pass (narrative, meetingDigest, channelDigest on /readyz).

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func appendContinuityArtifact(t *testing.T, app *kanbanBoardApp, agent ambientAgentConfig, id, roomID, cursorID string, hidden bool) meetingMemoryEntry {
	t.Helper()
	metadata := map[string]string{"roomId": normalizeRoomID(roomID)}
	if cursorID != "" {
		metadata[agent.cursorMetadataKey] = cursorID
	}
	if hidden {
		metadata[relevanceMetadataKey] = relevanceExpired
	}
	entry, appended, err := app.memory.appendEntry(agent.artifactKind, id, "artifact "+id, metadata)
	if err != nil || !appended {
		t.Fatalf("append artifact %s: appended=%v err=%v", id, appended, err)
	}
	return entry
}

// An expired dossier still proves the brains it folded were consumed: a room
// whose narratives have all expired is NOT ambiguous (production had two).
func TestAmbientContinuityCountsExpiredArtifactsAsConsumptionEvidence(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	agent := newHeldWindowTestAgent("expired evidence", true, new([][]string))
	room := "room-legacy"
	appendHeldWindowBrain(t, app, "legacy-brain-1", room)
	appendHeldWindowBrain(t, app, "legacy-brain-2", room)
	appendContinuityArtifact(t, app, agent, "legacy-dossier", room, "legacy-brain-2", true)

	baseline, clean, ambiguous := app.memory.ambientContinuityBaseline(agent, room)
	if ambiguous || clean || baseline != "legacy-brain-2" {
		t.Fatalf("baseline=%q clean=%v ambiguous=%v, want the expired dossier's cursor", baseline, clean, ambiguous)
	}
}

// A dossier updated in place carries a cursor newer than its own slot
// (production had two more rooms like this): it resolves, and the furthest
// cursor across every artifact wins.
func TestAmbientContinuityResolvesCursorNewerThanArtifactSlot(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	agent := newHeldWindowTestAgent("in place cursor", true, new([][]string))
	room := "room-inplace"
	appendHeldWindowBrain(t, app, "ip-brain-1", room)
	dossier := appendContinuityArtifact(t, app, agent, "ip-dossier", room, "ip-brain-1", false)
	appendHeldWindowBrain(t, app, "ip-brain-2", room)
	appendHeldWindowBrain(t, app, "ip-brain-3", room)
	if _, _, err := app.memory.updateEntryWithMetadata(agent.artifactKind, dossier.ID, dossier.Text, map[string]string{agent.cursorMetadataKey: "ip-brain-3"}); err != nil {
		t.Fatalf("update dossier cursor: %v", err)
	}
	// an older, slot-later artifact with an older cursor must not pull the
	// baseline back
	appendContinuityArtifact(t, app, agent, "ip-other", room, "ip-brain-2", false)

	baseline, _, ambiguous := app.memory.ambientContinuityBaseline(agent, room)
	if ambiguous || baseline != "ip-brain-3" {
		t.Fatalf("baseline=%q ambiguous=%v, want the in-place cursor ip-brain-3", baseline, ambiguous)
	}
	// a genuinely broken newest cursor stays ambiguous
	appendContinuityArtifact(t, app, agent, "ip-broken", room, "no-such-brain", false)
	if _, _, ambiguous := app.memory.ambientContinuityBaseline(agent, room); !ambiguous {
		t.Fatal("an unresolvable newest cursor must stay ambiguous")
	}
}

// A cursor whose target brain is hidden normalizes to the newest visible
// input before it, so the sidecar baseline always validates.
func TestAmbientContinuityHiddenCursorTargetNormalizesToVisiblePredecessor(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	agent := newHeldWindowTestAgent("hidden target", false, new([][]string))
	appendHeldWindowBrain(t, app, "ht-brain-1", officeRoomID)
	if _, appended, err := app.memory.appendBrainWriteUp("ht-brain-2", "## Overview\nHidden later.", map[string]string{"visibility": "organization", relevanceMetadataKey: relevanceExpired}); err != nil || !appended {
		t.Fatalf("append hidden brain: appended=%v err=%v", appended, err)
	}
	appendContinuityArtifact(t, app, agent, "ht-artifact", officeRoomID, "ht-brain-2", false)

	baseline, _, ambiguous := app.memory.ambientContinuityBaseline(agent, officeRoomID)
	if ambiguous || baseline != "ht-brain-1" {
		t.Fatalf("baseline=%q ambiguous=%v, want the visible predecessor ht-brain-1", baseline, ambiguous)
	}
	if _, baselineOK, _ := app.memory.normalizeAmbientCheckpointReferences(agent, officeRoomID, baseline, ""); !baselineOK {
		t.Fatal("normalized baseline must validate as a typed input cursor")
	}
}

// The window read resolves a baseline/cursor POSITIONALLY: a cursor at a row
// that later became hidden holds its place instead of rewinding to history.
func TestAmbientWindowCursorAtHiddenRowDoesNotRewind(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	agent := newHeldWindowTestAgent("positional cursor", false, new([][]string))
	appendHeldWindowBrain(t, app, "pc-brain-1", officeRoomID)
	appendHeldWindowBrain(t, app, "pc-brain-2", officeRoomID)
	appendContinuityArtifact(t, app, agent, "pc-artifact", officeRoomID, "pc-brain-2", false)
	if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindBrain, "pc-brain-2", "## Overview\nDurable held input pc-brain-2.", map[string]string{relevanceMetadataKey: relevanceExpired}); err != nil {
		t.Fatalf("hide brain: %v", err)
	}
	principal := app.currentRoomMediaRecallPrincipal(officeRoomID, "")
	if window := app.memory.unconsumedEntriesAfterFiltered(agent.inputKind, agent.artifactKind, agent.cursorMetadataKey, 8, "", "", principal, nil); len(window) != 0 {
		t.Fatalf("hidden cursor rewound the window to %+v", window)
	}
	if window := app.memory.unconsumedEntriesAfterFiltered(agent.inputKind, agent.artifactKind, agent.cursorMetadataKey, 8, "pc-brain-2", "", principal, nil); len(window) != 0 {
		t.Fatalf("hidden baseline rewound the window to %+v", window)
	}
	appendHeldWindowBrain(t, app, "pc-brain-3", officeRoomID)
	if window := app.memory.unconsumedEntriesAfterFiltered(agent.inputKind, agent.artifactKind, agent.cursorMetadataKey, 8, "", "", principal, nil); len(window) != 1 || window[0].ID != "pc-brain-3" {
		t.Fatalf("window=%+v, want only the new brain", window)
	}
}

// The production shape: a room-scoped worker boots clean for the office,
// runs one good pass, then the safety-floor sweep touches a legacy room whose
// dossiers have all expired. Before the fix that lazy room bootstrap opened a
// continuity_error on the WHOLE worker; now the room resolves from its
// expired evidence, keeps its cursor, and only new brains run.
func TestAmbientRoomSweepAfterSuccessfulOfficePassDoesNotOpenContinuityError(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	t.Setenv("HELD_WINDOW_INTERVAL", "1h")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
	var observed [][]string
	agent := newHeldWindowTestAgent("room sweep", true, &observed)
	room := "room-legacy"

	seed := newKanbanBoardApp()
	appendHeldWindowBrain(t, seed, "sweep-legacy-brain", room)
	appendContinuityArtifact(t, seed, agent, "sweep-legacy-dossier", room, "sweep-legacy-brain", true)
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
	app.startAmbientAgent(agent, "injected-only")
	appendHeldWindowBrain(t, app, "sweep-office-brain", officeRoomID)
	responder := func(context.Context, string, openAITextRequest) (string, error) { return "injected", nil }
	if _, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "injected-only", responder, 1, officeRoomID); err != nil {
		t.Fatalf("office pass: %v", err)
	}
	if health := ambientWorkerCapabilitySnapshot(agent, time.Now().UTC(), providerOpenAI, true); health["circuit"] == "continuity_error" {
		t.Fatalf("office pass alone reported continuity_error: %v", health)
	}

	// the safety-floor sweep: every room holding input of the agent's kind
	rooms := app.ambientAgentRooms(agent)
	if len(rooms) != 2 {
		t.Fatalf("sweep rooms=%v, want office + the legacy room", rooms)
	}
	for _, roomID := range rooms {
		_, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "injected-only", responder, 1, roomID)
		var circuitErr *ambientAgentCircuitOpenError
		if errors.As(err, &circuitErr) {
			t.Fatalf("sweep of %s opened a circuit: %+v", roomID, circuitErr)
		}
		if err != nil {
			t.Fatalf("sweep of %s: %v", roomID, err)
		}
	}
	app.mu.Lock()
	failure := app.agentFailures[ambientAgentKey(agent.name, room)]
	app.mu.Unlock()
	if failure != nil {
		t.Fatalf("legacy room recorded a failure: %+v", failure)
	}
	health := ambientWorkerCapabilitySnapshot(agent, time.Now().UTC(), providerOpenAI, true)
	if health["circuit"] == "continuity_error" || health["continuityError"] == true || health["blockedScopeCount"] != 0 {
		t.Fatalf("sweep reported continuity_error: %v", health)
	}
	if baseline := app.ambientAgentBaselineID(ambientAgentKey(agent.name, room)); baseline != "sweep-legacy-brain" {
		t.Fatalf("legacy room baseline=%q, want the expired dossier's cursor", baseline)
	}
	if len(observed) != 1 || observed[0][0] != "sweep-office-brain" {
		t.Fatalf("observed windows=%v, want only the office brain (legacy history stays consumed)", observed)
	}
	// a new brain in the legacy room runs, alone
	appendHeldWindowBrain(t, app, "sweep-legacy-brain-2", room)
	if _, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "injected-only", responder, 1, room); err != nil {
		t.Fatalf("legacy room pass: %v", err)
	}
	if len(observed) != 2 || len(observed[1]) != 1 || observed[1][0] != "sweep-legacy-brain-2" {
		t.Fatalf("observed windows=%v, want the new legacy brain only", observed)
	}
}

// The first-run anchor is opt-in: a never-run worker that declares it anchors
// at the pre-boot input (checkpoint flagged, diagnostics count it) and
// consumes only post-boot work; without the opt-in the pinned fail-closed
// contract (TestNoSidecarAmbiguousRawContinuityStaysFailClosedAcrossRestart)
// is untouched.
func TestNoSidecarFirstRunAnchorIsOptInAndVisible(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("EMBEDDINGS_DISABLED", "true")
	t.Setenv("HELD_WINDOW_INTERVAL", "1h")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
	var observed [][]string
	agent := newHeldWindowTestAgent("first run anchor", false, &observed)
	agent.firstRunAnchor = true

	seed := newKanbanBoardApp()
	appendHeldWindowBrain(t, seed, "anchor-pre-boot", officeRoomID)
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
	app.startAmbientAgent(agent, "injected-only")
	app.mu.Lock()
	failure := app.agentFailures[agent.name]
	app.mu.Unlock()
	if failure != nil {
		t.Fatalf("opted-in first run opened a circuit: %+v", failure)
	}
	if baseline := app.ambientAgentBaselineID(agent.name); baseline != "anchor-pre-boot" {
		t.Fatalf("baseline=%q, want the pre-boot anchor", baseline)
	}
	checkpoint, ok, err := app.ambientScopeCheckpoint(agent.name)
	if err != nil || !ok || !checkpoint.FirstRunAnchor || checkpoint.BaselineID != "anchor-pre-boot" || checkpoint.BlockedReason != "" {
		t.Fatalf("checkpoint=%+v ok=%v err=%v, want a flagged first-run anchor", checkpoint, ok, err)
	}
	health := ambientWorkerCapabilitySnapshot(agent, time.Now().UTC(), providerOpenAI, true)
	if health["firstRunAnchorScopes"] != 1 || health["continuityError"] == true || health["checkpointStatus"] != "ready" {
		t.Fatalf("snapshot=%v, want firstRunAnchorScopes=1 and a ready checkpoint", health)
	}
	responder := func(context.Context, string, openAITextRequest) (string, error) { return "injected", nil }
	if _, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "injected-only", responder, 1, officeRoomID); err != nil {
		t.Fatalf("drained first pass: %v", err)
	}
	appendHeldWindowBrain(t, app, "anchor-post-boot", officeRoomID)
	if _, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "injected-only", responder, 1, officeRoomID); err != nil {
		t.Fatalf("post-boot pass: %v", err)
	}
	if len(observed) != 1 || len(observed[0]) != 1 || observed[0][0] != "anchor-post-boot" {
		t.Fatalf("observed=%v, want only the post-boot brain", observed)
	}

	// a worker that HAS produced for the scope never anchors, even opted in
	produced := newHeldWindowTestAgent("produced before", false, new([][]string))
	produced.firstRunAnchor = true
	appendContinuityArtifact(t, app, produced, "produced-artifact", officeRoomID, "", true)
	if anchor, ok := app.ambientFirstRunAnchor(produced, officeRoomID); ok {
		t.Fatalf("anchor=%q ok=%v for a worker with (hidden) artifacts, want no first run", anchor, ok)
	}
}
