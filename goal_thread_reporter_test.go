package main

// goal_thread_reporter_test.go — the in-thread goal reporter (P0-2/P0-3/P0-4):
// role gating, the origin guards reused from deliverArtifactToOrigin, the
// checkpoint park's call-to-action message (whose ref ID keeps the
// scoutChatThreadHasAgentRef delivery dedupe working), the checkpoint-options
// extraction table, and the park's default-options fallback.

import (
	"strings"
	"testing"
)

// newGoalParentForReporter files a goal-shaped parent artifact with the given
// origin metadata, mirroring launchGoalThread's stamp.
func newGoalParentForReporter(t *testing.T, app *kanbanBoardApp, origin map[string]string) meetingMemoryEntry {
	t.Helper()
	metadata := map[string]string{
		"source":       "goal_thread",
		"mode":         "goal",
		"threadId":     "goal-thread-reporter-1",
		"threadQuery":  "Package the venture",
		"objective":    "Package the venture",
		"status":       "running",
		"threadStatus": "running",
	}
	for key, value := range origin {
		metadata[key] = value
	}
	parent, _, err := app.createOSArtifactWithMetadata("workflow", "Package the venture", "Goal execution thread", "AJ", metadata)
	if err != nil {
		t.Fatalf("create goal parent: %v", err)
	}
	return parent
}

// Role gating: deliverable stages narrate; plumbing stages (and role-less
// free-form subtasks) stay quiet.
func TestGoalStageRoleReportableGatesByRole(t *testing.T) {
	for _, role := range []string{processRolePanel, processRoleJudges, processRoleSynthesizer, processRoleWriter, processRoleCompile} {
		if !goalStageRoleReportable(role) {
			t.Errorf("role %q must be reportable", role)
		}
	}
	for _, role := range []string{processRoleGate, processRoleRender, processRoleHumanCheckpoint, "", "unknown"} {
		if goalStageRoleReportable(role) {
			t.Errorf("role %q must NOT be reportable", role)
		}
	}
}

func TestPostGoalStageMessagePostsToChannelOriginAndSkipsGatedRoles(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "growth", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("createScoutChatThread: %v", err)
	}
	parent := newGoalParentForReporter(t, app, map[string]string{
		"originKind": agentThreadOriginChannel,
		"originId":   channel.ID,
	})

	// gated roles post nothing
	app.postGoalStageMessage(parent.ID, "Gate the copy", processRoleGate, "os-artifact-gate", "gate is in")
	app.postGoalStageMessage(parent.ID, "Render the deck", processRoleRender, "os-artifact-render", "render is in")
	app.postGoalStageMessage(parent.ID, "Founder pass", processRoleHumanCheckpoint, "os-artifact-cp", "checkpoint is in")
	saved, _, err := app.scoutChatThreadByID("aj@shareability.com", channel.ID)
	if err != nil {
		t.Fatalf("scoutChatThreadByID: %v", err)
	}
	if len(saved.Messages) != 0 {
		t.Fatalf("gated roles posted %d messages, want none", len(saved.Messages))
	}

	// a reportable role posts the artifact ref message
	app.postGoalStageMessage(parent.ID, "Red-team — the hostile room", processRolePanel, "os-artifact-redteam", "red-team ledger is in — 5 seats, synthesized")
	saved, _, err = app.scoutChatThreadByID("aj@shareability.com", channel.ID)
	if err != nil {
		t.Fatalf("scoutChatThreadByID: %v", err)
	}
	if len(saved.Messages) != 1 {
		t.Fatalf("panel stage posted %d messages, want 1", len(saved.Messages))
	}
	message := saved.Messages[0]
	if message.Kind != "artifact" || message.Role != "scout" {
		t.Fatalf("stage message kind/role=%s/%s, want artifact/scout", message.Kind, message.Role)
	}
	if message.Text != "red-team ledger is in — 5 seats, synthesized" {
		t.Fatalf("stage message text=%q", message.Text)
	}
	if message.Thread == nil || message.Thread.ArtifactID != "os-artifact-redteam" || message.Thread.Status != "complete" {
		t.Fatalf("stage message ref=%+v, want the stage artifact ref, complete", message.Thread)
	}
	if message.Thread.Query != "Red-team — the hostile room" {
		t.Fatalf("stage message ref query=%q, want the stage title", message.Thread.Query)
	}
	// the stage ref must NOT carry the goal's agent thread id — only the park
	// message may claim the scoutChatThreadHasAgentRef dedupe slot
	if message.Thread.ID != "" {
		t.Fatalf("stage message ref id=%q, want empty", message.Thread.ID)
	}
}

func TestPostGoalStageMessageOriginGuards(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	// no origin at all: silently skip (nothing to assert beyond no panic —
	// there is no thread to inspect, so just exercise the path)
	orphan := newGoalParentForReporter(t, app, nil)
	app.postGoalStageMessage(orphan.ID, "Write", processRoleWriter, "os-artifact-w", "write is in")

	// archived channel: skip
	archived, err := app.createScoutChatThread("aj@shareability.com", "AJ", "old growth", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("createScoutChatThread: %v", err)
	}
	if _, err := app.setScoutChatThreadArchived("aj@shareability.com", archived.ID, true); err != nil {
		t.Fatalf("archive channel: %v", err)
	}
	parent := newGoalParentForReporter(t, app, map[string]string{
		"originKind": agentThreadOriginChannel,
		"originId":   archived.ID,
	})
	app.postGoalStageMessage(parent.ID, "Write", processRoleWriter, "os-artifact-w", "write is in")
	saved, _, err := app.scoutChatThreadByID("aj@shareability.com", archived.ID)
	if err != nil {
		t.Fatalf("scoutChatThreadByID: %v", err)
	}
	if len(saved.Messages) != 0 {
		t.Fatalf("archived channel got %d messages, want none", len(saved.Messages))
	}

	// a channel origin pointing at a NON-public thread: skip (the visibility
	// guard, verbatim from deliverArtifactToOrigin)
	private, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	mislabeled := newGoalParentForReporter(t, app, map[string]string{
		"originKind": agentThreadOriginChannel,
		"originId":   private.ID,
	})
	app.postGoalStageMessage(mislabeled.ID, "Write", processRoleWriter, "os-artifact-w", "write is in")
	saved, _, err = app.scoutChatThreadByID("aj@shareability.com", private.ID)
	if err != nil {
		t.Fatalf("scoutChatThreadByID: %v", err)
	}
	if len(saved.Messages) != 0 {
		t.Fatalf("non-public channel origin got %d messages, want none", len(saved.Messages))
	}
}

func TestPostGoalStageMessagePrivateThreadOriginCommitsAsOwner(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	private, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}
	parent := newGoalParentForReporter(t, app, map[string]string{
		"originKind": agentThreadOriginPrivateThread,
		"originId":   private.ID,
	})
	app.postGoalStageMessage(parent.ID, "Write — graft the winning spine", processRoleSynthesizer, "os-artifact-write", "deck copy is in")
	saved, _, err := app.scoutChatThreadByID("aj@shareability.com", private.ID)
	if err != nil {
		t.Fatalf("scoutChatThreadByID: %v", err)
	}
	if len(saved.Messages) != 1 {
		t.Fatalf("private thread got %d messages, want 1", len(saved.Messages))
	}
	if ref := saved.Messages[0].Thread; ref == nil || ref.ArtifactID != "os-artifact-write" {
		t.Fatalf("private thread message ref=%+v", saved.Messages[0].Thread)
	}
}

// goalStageMessageLine: the narration grammar, revision suffix included.
func TestGoalStageMessageLineRevisionSuffix(t *testing.T) {
	if got := goalStageMessageLine("Red-team", "5 seats, synthesized", 0); got != "Red-team is in — 5 seats, synthesized" {
		t.Fatalf("line=%q", got)
	}
	if got := goalStageMessageLine("Write", "", 2); got != "Write is in (revision 2)" {
		t.Fatalf("revision line=%q", got)
	}
}

// P0-3: the probe process run from a channel narrates its writer stage as it
// folds, and the checkpoint park lands as the call-to-action — a goal-parent
// ref whose ID carries the goal's agentThreadID, so the final origin delivery
// (deliverArtifactToOrigin) still dedupes on scoutChatThreadHasAgentRef.
func TestProcessGoalNarratesStagesAndParksCallToActionInOriginThread(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{})
	installFakeChildRunner(t)
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "growth", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("createScoutChatThread: %v", err)
	}

	thread, err := app.launchGoalThread(goalLaunchSpec{
		Objective:    "Probe the process runtime",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "process_probe",
		Origin: map[string]string{
			"originKind": agentThreadOriginChannel,
			"originId":   channel.ID,
		},
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)
	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateApproval)

	saved, _, err := app.scoutChatThreadByID("aj@shareability.com", channel.ID)
	if err != nil {
		t.Fatalf("scoutChatThreadByID: %v", err)
	}
	var stageMsg, parkMsg *scoutChatMessageRecord
	for index := range saved.Messages {
		message := &saved.Messages[index]
		switch message.Kind {
		case "artifact":
			stageMsg = message
		case "thread":
			parkMsg = message
		}
	}
	// The writer stage narrated as it folded (gate + checkpoint stayed quiet).
	if stageMsg == nil {
		t.Fatalf("no stage deliverable message in the channel: %+v", saved.Messages)
	}
	draft := plan.subtaskByID("draft")
	if draft == nil {
		t.Fatal("draft subtask missing from the plan")
	}
	if stageMsg.Thread == nil || stageMsg.Thread.ArtifactID != draft.ArtifactID {
		t.Fatalf("stage message ref=%+v, want the draft stage artifact %q", stageMsg.Thread, draft.ArtifactID)
	}
	if !strings.Contains(stageMsg.Text, "Draft the probe note is in") {
		t.Fatalf("stage message text=%q", stageMsg.Text)
	}
	// The park is the call-to-action: a goal-parent ref, approval_required.
	if parkMsg == nil {
		t.Fatalf("no park message in the channel: %+v", saved.Messages)
	}
	if !strings.HasPrefix(parkMsg.Text, "parked — ") {
		t.Fatalf("park message text=%q, want the parked prefix", parkMsg.Text)
	}
	ref := parkMsg.Thread
	if ref == nil || ref.ArtifactID != thread.Artifact.ID || ref.Mode != "goal" || ref.Status != codexJobStatusApprovalRequired {
		t.Fatalf("park ref=%+v, want the goal parent artifact, mode goal, approval_required", ref)
	}
	// The pin: the park ref carries the goal's agentThreadID, so the delivery
	// dedupe guard sees this thread as already holding the goal's card…
	if ref.ID != thread.ID {
		t.Fatalf("park ref id=%q, want the goal agent thread id %q", ref.ID, thread.ID)
	}
	if !scoutChatThreadHasAgentRef(saved, thread.ID) {
		t.Fatal("scoutChatThreadHasAgentRef must match the park message's ref")
	}
	// …and deliverArtifactToOrigin therefore never posts a duplicate card.
	parent := mustArtifact(t, app, thread.Artifact.ID)
	before := len(saved.Messages)
	app.deliverArtifactToOrigin(parent, thread.ID)
	saved, _, err = app.scoutChatThreadByID("aj@shareability.com", channel.ID)
	if err != nil {
		t.Fatalf("scoutChatThreadByID: %v", err)
	}
	if len(saved.Messages) != before {
		t.Fatalf("deliverArtifactToOrigin posted a duplicate: %d messages, want %d", len(saved.Messages), before)
	}
}

// P0-4.1: the extraction table — the array on its own last line wins, markdown
// brackets never poison it, and the whole-body scan survives as the fallback.
func TestProcessCheckpointOptionsFromTextTable(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "markdown-poisoned body with the array on the last line",
			body: "## Verdict\n- [ ] unresolved [objection](notes.md)\nWinner: cultural-moment\n[\"cultural-moment\", \"franchise-playbook\", \"founder-conviction\"]",
			want: []string{"cultural-moment", "franchise-playbook", "founder-conviction"},
		},
		{
			name: "array on the last line after prose",
			body: "The judges scored the spines.\n[\"a\", \"b\"]",
			want: []string{"a", "b"},
		},
		{
			name: "array with trailing prose lines still found by the line scan",
			body: "verdict:\n[\"x\", \"y\"]\n(see the ledger)",
			want: []string{"x", "y"},
		},
		{
			name: "inline array sharing its line falls back to the whole-body scan",
			body: `options: ["p", "q"]`,
			want: []string{"p", "q"},
		},
		{
			name: "no array at all",
			body: "just prose with [brackets] and ] noise",
			want: nil,
		},
		{
			name: "markdown checkboxes only",
			body: "- [ ] task one\n- [x] task two",
			want: nil,
		},
		{
			name: "non-string JSON array is not an options array",
			body: "[1, 2, 3]",
			want: nil,
		},
		{
			name: "empty body",
			body: "",
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := processCheckpointOptionsFromText(tc.body)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for index := range got {
				if got[index] != tc.want[index] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// P0-4.2: an OptionsFrom checkpoint whose extraction yields nothing parks with
// the generated defaults (proceed + send-back-to-input) and the disclosure in
// the question — never optionless, never silently free-form.
func TestParkProcessCheckpointGeneratesDefaultOptionsWhenExtractionFails(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	installFakeResponder(t, goalResponderRoutes{
		// The synthesizer output the checkpoint promises to extract from —
		// markdown-poisoned, no JSON string array anywhere.
		stage: "## Recommendation\nPick [option A](notes.md) — it wins on coherence.\nNo machine-readable list here.",
	})
	installFakeChildRunner(t)
	registerProcessDefinitionForTest(t, ProcessDefinition{
		ID:          "process_defaults_probe",
		Version:     1,
		Title:       "Defaults Probe",
		Description: "Test-only: synthesizer, then an OptionsFrom checkpoint with a poisoned source.",
		Authority:   toolAuthorityWorkspaceWrite,
		Hidden:      true,
		Stages: []ProcessStage{
			{ID: "recommend", Title: "Recommend a direction", Role: processRoleSynthesizer,
				PromptBody: "Recommend one direction."},
			{ID: "choose", Title: "Choose the direction", Role: processRoleHumanCheckpoint, InputFrom: []string{"recommend"},
				CheckpointSpec: &ProcessCheckpointSpec{Question: "Which direction ships?", OptionsFrom: "recommend"}},
		},
	})

	thread, err := app.launchGoalThread(goalLaunchSpec{
		Objective:    "Pick a direction",
		CreatedBy:    "aj@shareability.com",
		ToolTemplate: "process_defaults_probe",
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}
	app.runGoalThread(thread.Artifact.ID)
	plan := waitForGoalStage(t, app, thread.Artifact.ID, goalStateApproval)

	if plan.Checkpoint == nil {
		t.Fatal("goal did not park on the checkpoint")
	}
	options := plan.Checkpoint.Options
	if len(options) != 2 {
		t.Fatalf("checkpoint options=%+v, want the 2 generated defaults", options)
	}
	if options[0].Label != "proceed with the recommendation" || options[0].action() != processCheckpointActionProceed {
		t.Fatalf("default option 0=%+v, want the proceed default", options[0])
	}
	if options[1].Label != "send back with notes" || options[1].action() != processCheckpointActionRevise || options[1].Target != "recommend" {
		t.Fatalf("default option 1=%+v, want the revise-to-input default", options[1])
	}
	if !strings.Contains(plan.Checkpoint.Question, "options could not be extracted from recommend — defaults offered") {
		t.Fatalf("checkpoint question=%q, want the disclosure suffix", plan.Checkpoint.Question)
	}
	// the defaults are live: the proceed default resumes the goal to verified
	if err := app.resumeApprovedGoalWithChoice(thread.Artifact.ID, "aj@shareability.com", "proceed with the recommendation"); err != nil {
		t.Fatalf("resumeApprovedGoalWithChoice: %v", err)
	}
	waitForGoalStage(t, app, thread.Artifact.ID, goalStateVerified)
}
