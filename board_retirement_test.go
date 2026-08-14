package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRetiredBoardHasNoReadableOrLiveClientSurface(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	cookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")

	for _, request := range []struct {
		method  string
		path    string
		handler http.HandlerFunc
	}{
		{http.MethodGet, "/assistant/board", assistantBoardHandler},
		{http.MethodPost, "/assistant/board/drafts/legacy/accept", assistantBoardDraftActionHandler},
	} {
		req := httptest.NewRequest(request.method, request.path, nil)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		request.handler(recorder, req)
		if recorder.Code != http.StatusGone || !strings.Contains(recorder.Body.String(), ErrBoardRetired.Error()) {
			t.Fatalf("%s %s status=%d body=%s, want retired 410", request.method, request.path, recorder.Code, recorder.Body.String())
		}
	}

	mainSource, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{`sendKanbanEvent(c, "board"`, `sendKanbanEvent(c, "undo_available"`, `broadcastSignedInKanbanEvent("board"`} {
		if strings.Contains(string(mainSource), retired) {
			t.Fatalf("active server still transports retired Board state: %s", retired)
		}
	}
	webSource, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{`id="toolBoard"`, `id="roomBoardPanel"`, `id="roomBoardToggle"`} {
		if strings.Contains(string(webSource), retired) {
			t.Fatalf("web still mounts retired Board control: %s", retired)
		}
	}
}

func TestRetiredBoardInventoryIsLosslessBodyFreeAndRestartStable(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	card := app.snapshotState().Cards[0]
	artifact, _, err := app.createOSArtifactWithMetadata("research", "Historical deliverable", "private artifact body", "AJ", map[string]string{
		"boardCardId": card.ID,
	})
	if err != nil {
		t.Fatalf("create historical artifact: %v", err)
	}

	before := app.snapshotState()
	inventory := app.retiredBoardInventory()
	if inventory.Version != boardRetirementInventoryVersion || inventory.CardCount != len(before.Cards) || inventory.BoardDigest == "" {
		t.Fatalf("inventory header=%+v, want %d accounted cards", inventory, len(before.Cards))
	}
	seen := map[string]boardRetirementInventoryEntry{}
	for _, entry := range inventory.Entries {
		if entry.CardID == "" || len(entry.CardDigest) != 64 || (entry.Disposition != "legacy_only" && entry.Disposition != "projected_artifacts") {
			t.Fatalf("invalid inventory entry: %+v", entry)
		}
		seen[entry.CardID] = entry
	}
	if len(seen) != len(before.Cards) {
		t.Fatalf("unique inventory cards=%d, want %d", len(seen), len(before.Cards))
	}
	linked := seen[card.ID]
	if linked.Disposition != "projected_artifacts" || len(linked.SuccessorArtifactIDs) != 1 || linked.SuccessorArtifactIDs[0] != artifact.ID {
		t.Fatalf("historical successor=%+v, want artifact %q", linked, artifact.ID)
	}
	raw, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), card.Title) || strings.Contains(string(raw), card.Notes) || strings.Contains(string(raw), "private artifact body") {
		t.Fatalf("body-free inventory leaked title, notes, or artifact content: %s", raw)
	}
	if boardSnapshotDigest(before) != boardSnapshotDigest(app.snapshotState()) {
		t.Fatal("inventory mutated archived Board state")
	}

	app.Close()
	restarted := newKanbanBoardApp()
	t.Cleanup(func() { _ = restarted.Close() })
	after := restarted.retiredBoardInventory()
	if after.BoardDigest != inventory.BoardDigest || after.CardCount != inventory.CardCount {
		t.Fatalf("restart inventory=%+v, want digest=%s count=%d", after, inventory.BoardDigest, inventory.CardCount)
	}
}

func TestRetiredBoardNeverEntersNewAgentProviderContext(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	board := app.snapshotState()
	if len(board.Cards) == 0 {
		t.Fatal("historical Board fixture is empty")
	}
	memory := []meetingMemoryEntry{
		{ID: "retired-board-context", Kind: meetingMemoryKindBoardUpdate, Text: "SECRET LEGACY BOARD INSTRUCTION"},
		{ID: "current-source-context", Kind: meetingMemoryKindReflection, Text: "Current authorized company context."},
	}
	thread := scoutAgentThread{ID: "retired-board-agent", Mode: "research", Query: "prepare the current brief"}
	input := buildAgentThreadInput(thread, board, memory, time.Now().UTC())
	scaffold := buildAgentThreadScaffold(thread.Mode, thread.Query, board, memory)
	thread.Artifact = meetingMemoryEntry{ID: "retired-board-artifact", Text: "# Current brief", Metadata: map[string]string{"requestedBy": "aj@shareability.com"}}
	job := AgentJob{JobID: thread.ID, Mode: thread.Mode, Objective: thread.Query, Context: AgentJobContext{Board: board, Memory: memory}, thread: thread}
	values := map[string]string{
		"input": input, "scaffold": scaffold,
		"artifact":  buildArtifactModeAnswer(thread.Query, "current signal", board, memory),
		"research":  buildResearchModeAnswer(thread.Query, "current signal", board, memory),
		"design":    buildDesignModeAnswer(thread.Query, "current signal", board),
		"workflow":  buildWorkflowModeAnswer(thread.Query, "current signal", board, memory),
		"follow-up": buildAgentThreadFollowUpInput(thread, thread.Artifact, 2, "tighten it", nil, board, memory, time.Now().UTC()),
		"codex":     app.buildCodexAgentJobPrompt(job, time.Now().UTC(), codexJobAuthorityReadOnly),
		"anthropic": (&anthropicFableRunner{app: app}).userPrompt(job),
	}
	for name, value := range values {
		if strings.Contains(value, "SECRET LEGACY BOARD INSTRUCTION") || strings.Contains(value, board.Cards[0].Title) || strings.Contains(value, "Board and memory context") || strings.Contains(value, "current board") {
			t.Fatalf("%s leaked retired Board context: %s", name, value)
		}
		if !strings.Contains(value, "Current authorized") && !strings.Contains(value, "current authorized") {
			t.Fatalf("%s omitted the replacement authorized-source contract: %s", name, value)
		}
	}
	filtered := activeAgentMemory(memory)
	if len(filtered) != 1 || filtered[0].ID != "current-source-context" {
		t.Fatalf("active agent memory=%+v, want only non-Board source", filtered)
	}
}

func TestRetiredBoardToolsAreAbsentFromAgentAuthority(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	job := AgentJob{Authority: codexJobAuthorityWorkspaceWrite, RequestedBy: "aj@shareability.com"}
	runner := &anthropicFableRunner{app: app}
	available := map[string]bool{}
	for _, tool := range runner.toolsForJob(job) {
		available[tool.Name] = true
	}
	for name := range retiredBoardMutationTools {
		if orchestratorToolAllowlist[name] || available[name] {
			t.Fatalf("retired Board tool %q remains available to an agent", name)
		}
		if err := authorizeOrchestratorTool(job, name); err == nil {
			t.Fatalf("retired Board tool %q retained direct dispatch authority", name)
		}
	}
}
