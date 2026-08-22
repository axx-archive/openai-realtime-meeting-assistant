package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func seedScoutTerminalProjection(t *testing.T, app *kanbanBoardApp, status string, metadata map[string]string) (scoutChatThreadRecord, meetingMemoryEntry, string) {
	t.Helper()
	thread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "terminal projection", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	agentThreadID := "agent-thread-research-terminal"
	base := map[string]string{
		"mode":         "research",
		"threadId":     agentThreadID,
		"threadStatus": status,
		"status":       status,
		"originKind":   agentThreadOriginChannel,
		"originId":     thread.ID,
	}
	for key, value := range metadata {
		base[key] = value
	}
	artifact, _, err := app.createOSArtifactWithMetadata("research", "current market evidence", "# Private report body\n\nPrior version claimed 91 sources.", "AJ", base)
	if err != nil {
		t.Fatal(err)
	}
	message := scoutChatMessageRecord{
		ID:        "scout-chat-message-terminal-work",
		Kind:      "thread",
		Role:      "scout",
		Text:      "research workstream confirmed — running now",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Thread: &scoutChatThreadRef{
			ID:          agentThreadID,
			Mode:        "research",
			Query:       "current market evidence",
			Status:      "running",
			ArtifactID:  artifact.ID,
			AgentID:     "research-coworker-1",
			AgentName:   "AJA",
			DelegatedBy: "AJ",
		},
		ReplyTo: &scoutChatReplyRef{MessageID: "scout-chat-message-root"},
	}
	unrelated := scoutChatMessageRecord{ID: "scout-chat-message-unrelated", Kind: "message", Role: "user", Text: "leave this exact message alone", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if _, err := app.commitScoutChatThreadMessages(thread.OwnerEmail, thread.ID, unrelated, message); err != nil {
		t.Fatal(err)
	}
	return thread, artifact, agentThreadID
}

func TestScoutTerminalProjectionUsesCurrentMetadataAndIsRestartStable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", dir+"/memory.jsonl")
	t.Setenv("KANBAN_BOARD_PATH", dir+"/board.json")
	app := newKanbanBoardApp()
	thread, artifact, agentThreadID := seedScoutTerminalProjection(t, app, "running", map[string]string{
		"researchCitationCount":      "3",
		"researchSourceDomainCount":  "2",
		"researchQualityGate":        "passed",
		"researchEvidenceBinding":    "provider_fetched_urls",
		"researchSourceWindowDigest": strings.Repeat("a", 64),
		"threadRuns":                 `[{"researchCitationCount":"91"}]`,
	})

	app.updateScoutChatThreadRefs(agentThreadID, "running", artifact.ID)
	running, _, err := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := running.Messages[1].Text; got != "Research in progress" {
		t.Fatalf("running text=%q, want bounded active copy", got)
	}
	if running.Preview != "Research in progress" {
		t.Fatalf("running preview=%q", running.Preview)
	}

	artifact, _, err = app.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "", map[string]string{
		"threadStatus":               "complete",
		"status":                     "complete",
		"researchCitationCount":      "12",
		"researchSourceDomainCount":  "10",
		"researchQualityGate":        "passed",
		"researchEvidenceBinding":    "provider_fetched_urls",
		"researchSourceWindowDigest": strings.Repeat("b", 64),
		"threadRuns":                 `[{"researchCitationCount":"91"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	app.updateScoutChatThreadRefs(agentThreadID, "complete", artifact.ID)
	completed, _, err := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantTerminal := "Research delivered · 12 cited source links · 10 domains"
	if got := completed.Messages[1].Text; got != wantTerminal {
		t.Fatalf("terminal text=%q, want exact current metadata count", got)
	}
	if got := completed.Preview; got != wantTerminal {
		t.Fatalf("terminal preview=%q", got)
	}
	payload := app.scoutChatThreadUpdatePayload(thread.OwnerEmail, completed, completed.Messages[1])
	if got, _ := payload["preview"].(string); got != completed.Preview {
		t.Fatalf("event preview=%q, want %q", got, completed.Preview)
	}
	if got := completed.Messages[0].Text; got != "leave this exact message alone" {
		t.Fatalf("unrelated message changed to %q", got)
	}
	if ref := completed.Messages[1].Thread; ref.AgentID != "research-coworker-1" || ref.AgentName != "AJA" || ref.DelegatedBy != "AJ" || completed.Messages[1].ReplyTo == nil || completed.Messages[1].ReplyTo.MessageID != "scout-chat-message-root" {
		t.Fatalf("terminal projection changed delegated identity/topology: message=%+v", completed.Messages[1])
	}
	updatedAt := completed.UpdatedAt
	beforeStale, _ := canonicalJSON(completed)
	app.updateScoutChatThreadRefs(agentThreadID, "running", artifact.ID)
	afterStale, _, _ := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	afterStaleJSON, _ := canonicalJSON(afterStale)
	if string(beforeStale) != string(afterStaleJSON) {
		t.Fatal("stale running callback regressed the terminal thread")
	}
	app.updateScoutChatThreadRefs(agentThreadID, "complete", artifact.ID)
	replayed, _, _ := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if replayed.UpdatedAt != updatedAt || replayed.Messages[1].Text != completed.Messages[1].Text || replayed.Preview != completed.Preview {
		t.Fatalf("replay mutated terminal projection: before=%q/%q/%q after=%q/%q/%q", updatedAt, completed.Messages[1].Text, completed.Preview, replayed.UpdatedAt, replayed.Messages[1].Text, replayed.Preview)
	}

	restarted := newKanbanBoardApp()
	afterRestart, _, err := restarted.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterRestart.Messages[1].Text != completed.Messages[1].Text || afterRestart.Messages[0].Text != completed.Messages[0].Text || afterRestart.Preview != completed.Preview {
		t.Fatalf("restart projection drifted: %#v", afterRestart.Messages)
	}
}

func TestScoutChatViewerProjectionNamesConcreteDeckResultWithoutChangingGoalIdentity(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	goal, _, err := app.createOSArtifactWithMetadata("workflow", "Like A Farmer work", "goal record", "AJ", map[string]string{
		"mode": "goal", "status": "approval_required", "threadStatus": "approval_required",
	})
	if err != nil {
		t.Fatal(err)
	}
	deck, _, err := app.createOSArtifactWithMetadata("workflow", "Like A Farmer deck", "<!doctype html><html><body><section class=\"pg\">deck</section></body></html>", "AJ", map[string]string{
		"type": artifactTypeHTMLDeck, "source": "packaging_studio_ship", "goalId": goal.ID, "artifactContract": packagingStudioDeckContract,
	})
	if err != nil {
		t.Fatal(err)
	}
	finalDeck, _, err := app.createOSArtifactWithMetadata("workflow", "Like A Farmer — Optimization Insights", "<!doctype html><html><body><section class=\"pg\">edited deck</section></body></html>", "AJ", map[string]string{
		"type": artifactTypeHTMLDeck, "source": "scout_thread", "goalParentId": goal.ID, "status": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	if deck.ID == finalDeck.ID {
		t.Fatal("final edited deck must be a distinct durable result")
	}
	thread := scoutChatThreadRecord{Messages: []scoutChatMessageRecord{{
		ID: "goal-card", Kind: "thread", Role: "scout", Thread: &scoutChatThreadRef{ID: "run-1", Mode: "goal", Status: "approval_required", ArtifactID: goal.ID},
	}}}
	// A generated candidate is not a channel result while render/jury/quality
	// are still in flight. Editing it during that window would invalidate the
	// exact reviewed revision.
	projected := app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	if got := projected.Messages[0].Thread.ResultArtifactID; got != "" {
		t.Fatalf("unreviewed candidate leaked into the channel as %q", got)
	}
	shipRecord, _, err := app.createOSArtifactWithMetadata("workflow", "Assemble presentation", "exact reviewed deck filed", "Scout", map[string]string{
		"source": "process_stage", "processStage": "ship_compile", "goalParentId": goal.ID, "deckArtifactId": finalDeck.ID, "status": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	readyPlan := goalPlan{ProcessID: packagingStudioProcessID, State: goalStateApproval, Subtasks: []goalSubtask{{ID: "ship_compile", Status: subtaskComplete, ArtifactID: shipRecord.ID}}}
	rawReadyPlan, _ := json.Marshal(readyPlan)
	goal, _, err = app.updateOSArtifactWithMetadata(goal.ID, "", goal.Text, "Scout", map[string]string{"goalPlan": string(rawReadyPlan)})
	if err != nil {
		t.Fatal(err)
	}
	projected = app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	ref := projected.Messages[0].Thread
	if ref.ArtifactID != goal.ID || ref.ResultArtifactID != finalDeck.ID || ref.ResultArtifactType != artifactTypeHTMLDeck || ref.ResultTitle != "Like A Farmer — Optimization Insights" {
		t.Fatalf("projected ref=%+v, want lifecycle goal plus explicit deck result", ref)
	}
	if thread.Messages[0].Thread.ResultArtifactID != "" {
		t.Fatal("read projection mutated persisted thread")
	}

	// A legacy ship approval predating acceptedResultArtifactId freezes the
	// latest deck that existed at the decision. Later retry artifacts remain in
	// history but cannot replace the approved channel handoff.
	legacyPlan := goalPlan{ProcessID: packagingStudioProcessID, Checkpoint: &goalProcessCheckpoint{
		StageID: "ship_approval", ResolvedAt: time.Now().UTC().Format(time.RFC3339Nano), LastAction: processCheckpointActionProceed,
	}}
	rawPlan, _ := json.Marshal(legacyPlan)
	goal, _, err = app.updateOSArtifactWithMetadata(goal.ID, "", goal.Text, "Scout", map[string]string{"goalPlan": string(rawPlan)})
	if err != nil {
		t.Fatal(err)
	}
	retryDeck, _, err := app.createOSArtifactWithMetadata("workflow", "retry deck", "<!doctype html><html><body><section class=\"pg\">retry</section></body></html>", "Scout", map[string]string{
		"type": artifactTypeHTMLDeck, "source": "scout_thread", "goalParentId": goal.ID, "status": "complete",
	})
	if err != nil || retryDeck.ID == "" {
		t.Fatal(err)
	}
	projected = app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	if got := projected.Messages[0].Thread.ResultArtifactID; got != finalDeck.ID {
		t.Fatalf("post-approval retry replaced accepted result with %q, want %q", got, finalDeck.ID)
	}

	other, _, err := app.createOSArtifactWithMetadata("workflow", "Wrong goal deck", "<!doctype html><html><body>wrong</body></html>", "AJ", map[string]string{
		"type": artifactTypeHTMLDeck, "source": "packaging_studio_ship", "goalId": "another-goal", "artifactContract": packagingStudioDeckContract,
	})
	if err != nil || other.ID == "" {
		t.Fatal(err)
	}
	projected = app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	if got := projected.Messages[0].Thread.ResultArtifactID; got != finalDeck.ID {
		t.Fatalf("cross-goal sibling changed result to %q", got)
	}
}

func TestScoutChatViewerProjectionExposesOnlyFinalDocumentDeliverable(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	goal, _, err := app.createOSArtifactWithMetadata("workflow", "Engagement army report", "goal record", "AJ", map[string]string{
		"mode": "goal", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	intermediate, _, err := app.createOSArtifactWithMetadata("workflow", "Research dossier", "# Research\n\nUseful but not the report.", "Scout", map[string]string{
		"type": artifactTypeMarkdown, "source": "scout_thread", "goalParentId": goal.ID, "goalSubtaskId": "research", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	report, _, err := app.createOSArtifactWithMetadata("workflow", "Insights & Opportunities", "# Insights & Opportunities\n\nThe final editable report.", "Scout", map[string]string{
		"type": artifactTypeMarkdown, "source": "scout_thread", "goalParentId": goal.ID, "goalSubtaskId": "write", "goalDeliverable": "true", "outputContract": documentReportOutputContract, "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := goalPlan{State: goalStateVerified, Subtasks: []goalSubtask{{ID: "external_research", Status: subtaskComplete, ArtifactID: intermediate.ID}, {ID: "write", Role: processRoleWriter, Status: subtaskComplete, ArtifactID: report.ID}}}
	documentProcess, ok := processByID(documentReportProcessID)
	if !ok {
		t.Fatal("document report process is unavailable")
	}
	if err := bindGoalProcessIdentity(&plan, documentProcess); err != nil {
		t.Fatal(err)
	}
	rawPlan, _ := json.Marshal(plan)
	goal, _, err = app.updateOSArtifactWithMetadata(goal.ID, "", goal.Text, "Scout", map[string]string{"goalPlan": string(rawPlan)})
	if err != nil {
		t.Fatal(err)
	}
	thread := scoutChatThreadRecord{Messages: []scoutChatMessageRecord{{
		ID: "goal-card", Kind: "thread", Role: "scout", Thread: &scoutChatThreadRef{ID: "run-report", Mode: "goal", Status: "complete", ArtifactID: goal.ID},
	}}}
	projected := app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	ref := projected.Messages[0].Thread
	if ref.ArtifactID != goal.ID || ref.ResultArtifactID != report.ID || ref.ResultArtifactType != artifactTypeMarkdown || ref.ResultTitle != "Insights & Opportunities" {
		t.Fatalf("projected ref=%+v, want exact editable report while preserving goal identity", ref)
	}
	if !strings.Contains(ref.ResultPreview, "The final editable report") || len(ref.ResultPreview) > 1200 {
		t.Fatalf("document preview=%q, want bounded current report excerpt", ref.ResultPreview)
	}
	serialized, err := json.Marshal(projected)
	var roundTrip scoutChatThreadRecord
	if err != nil || json.Unmarshal(serialized, &roundTrip) != nil || !strings.HasPrefix(roundTrip.Messages[0].Thread.ResultPreview, "# Insights & Opportunities") {
		t.Fatalf("serialized projection lost the mobile document preview: %s err=%v", serialized, err)
	}
}

func TestScoutChatViewerProjectionSuppressesInternalMarkdownAndProjectsStandaloneTerminalDocument(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	stage, _, err := app.createOSArtifactWithMetadata("workflow", "Internal stage", "# Internal\n\nDo not publish.", "Scout", map[string]string{
		"type": artifactTypeMarkdown, "source": "process_stage", "threadId": "stage-run", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := app.createOSArtifactWithMetadata("research", "Internal research", "# Research\n\nNot the report.", "Scout", map[string]string{
		"type": artifactTypeMarkdown, "source": "scout_thread", "threadId": "research-child", "goalParentId": "goal-parent", "goalSubtaskId": "external_research", "goalDeliverable": "true", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	standaloneBody := "# Market note\n\nA channel-facing document paragraph.\n\n" + strings.Repeat("Useful detail. ", 200)
	standalone, _, err := app.createOSArtifactWithMetadata("research", "Market note", standaloneBody, "Scout", map[string]string{
		"type": artifactTypeMarkdown, "source": "scout_thread", "threadId": "standalone-report", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	standaloneDeck, _, err := app.createOSArtifactWithMetadata("workflow", "Standalone deck", "<!doctype html><html><body><section class=\"pg\">A complete deck.</section></body></html>", "Scout", map[string]string{
		"type": artifactTypeHTMLDeck, "source": "scout_thread", "threadId": "standalone-deck", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	thread := scoutChatThreadRecord{Messages: []scoutChatMessageRecord{
		{ID: "stage", Kind: "thread", Role: "scout", Thread: &scoutChatThreadRef{ID: "stage-run", Mode: "artifacts", Status: "complete", ArtifactID: stage.ID}},
		{ID: "child", Kind: "thread", Role: "scout", Thread: &scoutChatThreadRef{ID: "research-child", Mode: "research", Status: "complete", ArtifactID: child.ID}},
		{ID: "standalone", Kind: "thread", Role: "scout", Thread: &scoutChatThreadRef{ID: "standalone-report", Mode: "research", Status: "complete", ArtifactID: standalone.ID}},
		{ID: "standalone-deck", Kind: "thread", Role: "scout", Thread: &scoutChatThreadRef{ID: "standalone-deck", Mode: "presentation", Status: "complete", ArtifactID: standaloneDeck.ID}},
	}}
	projected := app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	if projected.Messages[0].Thread.ResultArtifactID != "" || projected.Messages[1].Thread.ResultArtifactID != "" {
		t.Fatalf("internal artifacts became rich results: stage=%+v child=%+v", projected.Messages[0].Thread, projected.Messages[1].Thread)
	}
	ref := projected.Messages[2].Thread
	if ref.ResultArtifactID != standalone.ID || ref.ResultArtifactType != artifactTypeMarkdown || !strings.Contains(ref.ResultPreview, "channel-facing document paragraph") || len(ref.ResultPreview) > 1200 {
		t.Fatalf("standalone terminal projection=%+v", ref)
	}
	if ref.ResultQualityState != "" || !ref.ResultCanEdit || ref.ResultCanContinue || ref.ResultCanPresent || !ref.ResultCanExport {
		t.Fatalf("standalone document lost ordinary Studio capabilities: %+v", ref)
	}
	deckRef := projected.Messages[3].Thread
	if deckRef.ResultArtifactID != standaloneDeck.ID || deckRef.ResultArtifactType != artifactTypeHTMLDeck || deckRef.ResultQualityState != "" || !deckRef.ResultCanEdit || deckRef.ResultCanContinue || !deckRef.ResultCanPresent || !deckRef.ResultCanExport {
		t.Fatalf("standalone deck lost ordinary Studio capabilities: %+v", deckRef)
	}
}

func TestScoutChatViewerProjectionRequiresCurrentResultACL(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	goal, _, err := app.createOSArtifactWithMetadata("workflow", "Private report goal", "goal", "Scout", map[string]string{
		"mode": "goal", "status": "complete", "threadStatus": "complete", "visibility": "organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	report, _, err := app.createOSArtifactWithMetadata("workflow", "Private report", "# Private report\n\nOwner-only finding.", "Scout", map[string]string{
		"type": artifactTypeMarkdown, "source": "scout_thread", "goalParentId": goal.ID, "goalSubtaskId": "write", "goalDeliverable": "true",
		"outputContract": documentReportOutputContract, "status": "complete", "threadStatus": "complete", "visibility": scoutChatVisibilityPrivate, "ownerEmail": "aj@shareability.com", "requestedBy": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := goalPlan{State: goalStateVerified, Subtasks: []goalSubtask{{ID: "write", Role: processRoleWriter, Status: subtaskComplete, ArtifactID: report.ID}}}
	documentProcess, ok := processByID(documentReportProcessID)
	if !ok {
		t.Fatal("document report process is unavailable")
	}
	if err := bindGoalProcessIdentity(&plan, documentProcess); err != nil {
		t.Fatal(err)
	}
	rawPlan, _ := json.Marshal(plan)
	if _, _, err = app.updateOSArtifactWithMetadata(goal.ID, "", goal.Text, "Scout", map[string]string{"goalPlan": string(rawPlan)}); err != nil {
		t.Fatal(err)
	}
	thread := scoutChatThreadRecord{Messages: []scoutChatMessageRecord{{ID: "goal", Kind: "thread", Role: "scout", Thread: &scoutChatThreadRef{ID: goal.ID, Mode: "goal", Status: "complete", ArtifactID: goal.ID}}}}
	owner := app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	if owner.Messages[0].Thread.ResultArtifactID != report.ID || !owner.Messages[0].Thread.ResultCanEdit {
		t.Fatalf("owner lost authorized report: %+v", owner.Messages[0].Thread)
	}
	other := app.projectScoutChatThreadForViewer("tim@shareability.com", thread)
	if ref := other.Messages[0].Thread; ref.ResultArtifactID != "" || ref.ResultTitle != "" || ref.ResultPreview != "" || ref.ResultQualityState != "" || ref.ResultCanEdit || ref.ResultCanContinue || ref.ResultCanPresent || ref.ResultCanExport {
		t.Fatalf("private result leaked to non-owner: %+v", ref)
	}
}

func TestScoutChatViewerProjectionRejectsResultRevisionRace(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	report, _, err := app.createOSArtifactWithMetadata("research", "Race report", "# Race report\n\nOld authorized body.", "Scout", map[string]string{
		"type": artifactTypeMarkdown, "source": "scout_thread", "threadId": "race-report", "status": "complete", "threadStatus": "complete", "visibility": "organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	index := app.scoutChatResultIndex()
	previousProbe := artifactAuthorizationAfterCheckProbe
	mutated := false
	artifactAuthorizationAfterCheckProbe = func() {
		if mutated {
			return
		}
		mutated = true
		if _, _, updateErr := app.updateOSArtifact(report.ID, "Race report", "# Race report\n\nChanged after authorization.", "Scout"); updateErr != nil {
			t.Fatalf("race update: %v", updateErr)
		}
	}
	t.Cleanup(func() { artifactAuthorizationAfterCheckProbe = previousProbe })
	message := scoutChatMessageRecord{Thread: &scoutChatThreadRef{ID: "race-report", Mode: "research", Status: "complete", ArtifactID: report.ID}}
	app.projectScoutChatResultRef(context.Background(), accountStore().findUser("aj@shareability.com"), &message, index)
	if ref := message.Thread; ref.ResultArtifactID != "" || ref.ResultPreview != "" {
		t.Fatalf("stale result survived revision race: %+v", ref)
	}
}

func TestScoutChatViewerProjectionUsesOnlyExactBlockedDeckSalvage(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	goal, _, err := app.createOSArtifactWithMetadata("workflow", "Blocked deck goal", "goal", "Scout", map[string]string{"mode": "goal", "status": "needs_attention", "threadStatus": "needs_attention"})
	if err != nil {
		t.Fatal(err)
	}
	salvage, _, err := app.createOSArtifactWithMetadata("workflow", "Exact salvage", "<!doctype html><html><body><section class=\"pg\">salvage</section></body></html>", "Scout", map[string]string{
		"type": artifactTypeHTMLDeck, "source": "scout_thread", "goalParentId": goal.ID, "status": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	newer, _, err := app.createOSArtifactWithMetadata("workflow", "Newer unrelated attempt", "<!doctype html><html><body><section class=\"pg\">newer</section></body></html>", "Scout", map[string]string{
		"type": artifactTypeHTMLDeck, "source": "scout_thread", "goalParentId": goal.ID, "status": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := goalPlan{ProcessID: packagingStudioProcessID, State: goalStateBlocked, Report: goalReport{DeliverableArtifactID: salvage.ID}}
	rawPlan, _ := json.Marshal(plan)
	if _, _, err = app.updateOSArtifactWithMetadata(goal.ID, "", goal.Text, "Scout", map[string]string{"goalPlan": string(rawPlan)}); err != nil {
		t.Fatal(err)
	}
	thread := scoutChatThreadRecord{Messages: []scoutChatMessageRecord{{ID: "goal", Kind: "thread", Role: "scout", Thread: &scoutChatThreadRef{ID: goal.ID, Mode: "goal", Status: "needs_attention", ArtifactID: goal.ID}}}}
	projected := app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	ref := projected.Messages[0].Thread
	if got := ref.ResultArtifactID; got != salvage.ID || got == newer.ID {
		t.Fatalf("blocked deck result=%q, want exact salvage %q and never newer %q", got, salvage.ID, newer.ID)
	}
	if ref.ResultQualityState != authoredResultQualityDraftNeedsAttention || !ref.ResultCanEdit || !ref.ResultCanContinue || ref.ResultCanPresent || ref.ResultCanExport {
		t.Fatalf("blocked deck salvage projected publication capability: %+v", ref)
	}
}

func TestScoutChatHumanAcceptedDeckNeverBypassesRenderedAdmission(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	goal, _, err := app.createOSArtifactWithMetadata("workflow", "Approved deck goal", "goal", "AJ", map[string]string{
		"mode": "goal", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	deck, _, err := app.createOSArtifactWithMetadata("workflow", "Approved deck", "<!doctype html><html><body><section class=\"pg\">approved v1</section></body></html>", "AJ", map[string]string{
		"type": artifactTypeHTMLDeck, "source": "packaging_studio_ship", "goalId": goal.ID, "artifactContract": packagingStudioDeckContract,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := goalPlan{ProcessID: packagingStudioProcessID, State: goalStateVerified}
	bindGoalAcceptedResult(&plan, deck)
	rawPlan, _ := json.Marshal(plan)
	goal, _, err = app.updateOSArtifactWithMetadata(goal.ID, "", goal.Text, "AJ", map[string]string{"goalPlan": string(rawPlan)})
	if err != nil {
		t.Fatal(err)
	}
	thread := scoutChatThreadRecord{Messages: []scoutChatMessageRecord{{
		ID: "approved-goal", Kind: "thread", Role: "scout", Thread: &scoutChatThreadRef{ID: goal.ID, Mode: "goal", Status: "complete", ArtifactID: goal.ID},
	}}}
	exact := app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	exactRef := exact.Messages[0].Thread
	if exactRef.ResultArtifactID != deck.ID || exactRef.ResultApprovalState != scoutChatResultApprovalExact || !exactRef.ResultCanEdit || exactRef.ResultQualityState != authoredResultQualityDraftNeedsAttention || exactRef.ResultCanPresent || exactRef.ResultCanExport {
		t.Fatalf("human acceptance bypassed rendered admission: %+v", exactRef)
	}

	edited, _, err := app.updateOSArtifact(deck.ID, "Approved deck", "<!doctype html><html><body><section class=\"pg\">edited after approval</section></body></html>", "AJ")
	if err != nil {
		t.Fatal(err)
	}
	if artifactVersion(edited) == plan.Report.AcceptedResultArtifactVersion || artifactCapabilityDigest(edited) == plan.Report.AcceptedResultArtifactDigest {
		t.Fatalf("edit did not move the exact approved tuple: version=%d digest=%s", artifactVersion(edited), artifactCapabilityDigest(edited))
	}
	postEdit := app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	postEditRef := postEdit.Messages[0].Thread
	if postEditRef.ResultArtifactID != deck.ID || postEditRef.ResultApprovalState != scoutChatResultApprovalEdited || postEditRef.ResultQualityState != authoredResultQualityDraftNeedsAttention || postEditRef.ResultCanPresent || postEditRef.ResultCanExport {
		t.Fatalf("post-approval edit was represented as exact approval: %+v", postEditRef)
	}
}

func TestScoutChatRenderedPublishedDeckAdmissionTracksExactRevision(t *testing.T) {
	unrelatedApp := newIsolatedKanbanBoardApp(t)
	fixture := newPackagingQualityGateFixture(t, "ready", []slideJuryRepair{})
	var scorerCalls atomic.Int32
	fixture.runQualityGate(t, packagingQualityScoreJSON(9.4, "ready"), &scorerCalls)
	if scorerCalls.Load() != 0 {
		t.Fatalf("deterministic ready jury called a second scorer %d time(s)", scorerCalls.Load())
	}
	ship := fixture.plan.subtaskByID("ship_compile")
	ship.Status = subtaskRunning
	shipStage := packagingStudioStage(t, fixture.def, "ship_compile")
	body, metadata, err := compilePackagingStudioShip(fixture.app, &fixture.plan, fixture.parentID, shipStage)
	if err != nil {
		t.Fatal(err)
	}
	fixture.engine.completeProcessStage(&fixture.plan, fixture.parentID, ship, shipStage, body, "published fixture", metadata)
	fixture.plan.State = goalStateVerified
	fixture.plan.Report.DeliverableArtifactID = fixture.deck.ID
	rawPlan, _ := json.Marshal(fixture.plan)
	parent := mustArtifact(t, fixture.app, fixture.parentID)
	if _, _, err := fixture.app.updateOSArtifactWithMetadata(parent.ID, "", parent.Text, "Scout", map[string]string{"goalPlan": string(rawPlan), "status": "complete", "threadStatus": "complete"}); err != nil {
		t.Fatal(err)
	}
	thread := scoutChatThreadRecord{Messages: []scoutChatMessageRecord{{
		ID: "published-goal", Kind: "thread", Role: "scout", Thread: &scoutChatThreadRef{ID: parent.ID, Mode: "goal", Status: "complete", ArtifactID: parent.ID},
	}}}
	// Projection must resolve chat-origin security against the receiver app,
	// never whichever app instance happens to occupy the process-global HTTP
	// singleton. The broad package suite intentionally leaves a different test
	// app here to catch that cross-store header mismatch.
	previousApp := kanbanApp
	kanbanApp = unrelatedApp
	t.Cleanup(func() { kanbanApp = previousApp })
	projected := fixture.app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	ref := projected.Messages[0].Thread
	if ref.ResultArtifactID != fixture.deck.ID || ref.ResultQualityState != authoredResultQualityAdmitted || !ref.ResultCanPresent || !ref.ResultCanExport {
		review, qualityErr := resolvePublishedPackagingStudioQuality(fixture.app, &fixture.plan, fixture.parentID)
		t.Fatalf("exact rendered publication was not admitted: %+v; resolver review=%+v err=%v", ref, review, qualityErr)
	}
	handlerPreviousApp := kanbanApp
	kanbanApp = fixture.app
	t.Cleanup(func() { kanbanApp = handlerPreviousApp })
	adminCookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	admittedDeckDocument, _, _, loadErr := loadDeckDocument(fixture.deck)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	copyPayload, _ := json.Marshal(map[string]any{
		"artifactId": fixture.deck.ID, "expectedVersion": artifactVersion(fixture.deck),
		"title": "Reviewed deck copy", "fileName": "Reviewed deck copy", "folderId": "", "deck": admittedDeckDocument,
	})
	if copied := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/deck/copies", string(copyPayload), adminCookies, deckEditorCopyHandler); copied.Code != http.StatusCreated {
		t.Fatalf("exact admitted deck copy status=%d body=%s", copied.Code, copied.Body.String())
	}
	modifiedCopy := admittedDeckDocument
	modifiedCopy.Slides = append([]deckSlide(nil), admittedDeckDocument.Slides...)
	modifiedCopy.Slides[0].Elements = append([]deckElement(nil), admittedDeckDocument.Slides[0].Elements...)
	modifiedCopy.Slides[0].Elements[0].Text += " changed in copy request"
	modifiedCopyPayload, _ := json.Marshal(map[string]any{
		"artifactId": fixture.deck.ID, "expectedVersion": artifactVersion(fixture.deck),
		"title": "Unreviewed deck copy", "fileName": "Unreviewed deck copy", "folderId": "", "deck": modifiedCopy,
	})
	if copied := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/deck/copies", string(modifiedCopyPayload), adminCookies, deckEditorCopyHandler); copied.Code != http.StatusConflict {
		t.Fatalf("modified managed deck copy status=%d body=%s", copied.Code, copied.Body.String())
	}

	// The channel index is an optimization, not publication authority. Move
	// only the parent plan after the result child has been authorized: the
	// projected controls must re-evaluate the current goal instead of streaming
	// the admitted state captured in the earlier channel-wide index.
	admittedPlanJSON := string(rawPlan)
	blockedPlan := fixture.plan
	blockedPlan.State = goalStateBlocked
	blockedPlanJSON, _ := json.Marshal(blockedPlan)
	beforeCopyRaceArtifacts := len(fixture.app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0))
	beforeCopyRaceFiles := len(fixture.app.assistantFilesForUser("aj@shareability.com"))
	previousCopyProbe := authoredCopyAfterAdmissionProbe
	t.Cleanup(func() { authoredCopyAfterAdmissionProbe = previousCopyProbe })
	copyParentMoved := false
	authoredCopyAfterAdmissionProbe = func() {
		if copyParentMoved {
			return
		}
		copyParentMoved = true
		parentSnapshot := mustArtifact(t, fixture.app, fixture.parentID)
		if _, _, updateErr := fixture.app.updateOSArtifactWithMetadata(parentSnapshot.ID, "", parentSnapshot.Text, "Scout", map[string]string{"goalPlan": string(blockedPlanJSON)}); updateErr != nil {
			t.Fatalf("move parent review during deck copy: %v", updateErr)
		}
	}
	copyRace := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/deck/copies", string(copyPayload), adminCookies, deckEditorCopyHandler)
	authoredCopyAfterAdmissionProbe = previousCopyProbe
	if !copyParentMoved || copyRace.Code != http.StatusConflict {
		t.Fatalf("parent-raced deck copy status=%d body=%s moved=%t", copyRace.Code, copyRace.Body.String(), copyParentMoved)
	}
	if got := len(fixture.app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0)); got != beforeCopyRaceArtifacts {
		t.Fatalf("parent-raced deck copy left artifact rows: got=%d want=%d", got, beforeCopyRaceArtifacts)
	}
	if got := len(fixture.app.assistantFilesForUser("aj@shareability.com")); got != beforeCopyRaceFiles {
		t.Fatalf("parent-raced deck copy left Files rows: got=%d want=%d", got, beforeCopyRaceFiles)
	}
	parent = mustArtifact(t, fixture.app, fixture.parentID)
	if _, _, err := fixture.app.updateOSArtifactWithMetadata(parent.ID, "", parent.Text, "Scout", map[string]string{"goalPlan": admittedPlanJSON}); err != nil {
		t.Fatal(err)
	}
	previousAuthorizationProbe := artifactAuthorizationAfterCheckProbe
	parentMoved := false
	artifactAuthorizationAfterCheckProbe = func() {
		if parentMoved {
			return
		}
		parentMoved = true
		parentSnapshot := mustArtifact(t, fixture.app, fixture.parentID)
		if _, _, updateErr := fixture.app.updateOSArtifactWithMetadata(parentSnapshot.ID, "", parentSnapshot.Text, "Scout", map[string]string{"goalPlan": string(blockedPlanJSON)}); updateErr != nil {
			t.Fatalf("move parent review after result auth: %v", updateErr)
		}
	}
	projected = fixture.app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	artifactAuthorizationAfterCheckProbe = previousAuthorizationProbe
	if raced := projected.Messages[0].Thread; raced.ResultQualityState != authoredResultQualityDraftNeedsAttention || raced.ResultCanPresent || raced.ResultCanExport {
		t.Fatalf("parent-only review race streamed stale final controls: %+v", raced)
	}
	parent = mustArtifact(t, fixture.app, fixture.parentID)
	if _, _, err := fixture.app.updateOSArtifactWithMetadata(parent.ID, "", parent.Text, "Scout", map[string]string{"goalPlan": admittedPlanJSON}); err != nil {
		t.Fatal(err)
	}

	edited, _, err := fixture.app.updateOSArtifact(fixture.deck.ID, "", fixture.deck.Text+"\n<!-- edited -->", "AJ")
	if err != nil {
		t.Fatal(err)
	}
	if artifactVersion(edited) == artifactVersion(fixture.deck) {
		t.Fatal("fixture edit did not advance the admitted deck revision")
	}
	editedDeckDocument, _, _, loadErr := loadDeckDocument(edited)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	editedCopyPayload, _ := json.Marshal(map[string]any{
		"artifactId": edited.ID, "expectedVersion": artifactVersion(edited),
		"title": "Edited draft copy", "fileName": "Edited draft copy", "folderId": "", "deck": editedDeckDocument,
	})
	if copied := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/deck/copies", string(editedCopyPayload), adminCookies, deckEditorCopyHandler); copied.Code != http.StatusConflict {
		t.Fatalf("edited authored draft copy status=%d body=%s", copied.Code, copied.Body.String())
	}
	projected = fixture.app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	ref = projected.Messages[0].Thread
	if ref.ResultArtifactID != fixture.deck.ID || ref.ResultQualityState != authoredResultQualityEditedAfterAdmission || !ref.ResultCanContinue || ref.ResultCanPresent || ref.ResultCanExport {
		t.Fatalf("post-admission edit retained final capabilities: %+v", ref)
	}
	qualityStage := fixture.plan.subtaskByID("quality_gate")
	qualityRecord := mustArtifact(t, fixture.app, qualityStage.ArtifactID)
	juryDigest := qualityRecord.Metadata["slideJuryArtifactDigest"]
	if juryDigest == "" {
		t.Fatal("ready fixture did not bind the slide jury digest")
	}
	if _, _, err := fixture.app.memory.updateOSArtifactMetadata(qualityRecord.ID, map[string]string{"slideJuryArtifactDigest": ""}); err != nil {
		t.Fatal(err)
	}
	projected = fixture.app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	if incomplete := projected.Messages[0].Thread; incomplete.ResultQualityState != authoredResultQualityDraftNeedsAttention || incomplete.ResultCanContinue || incomplete.ResultCanPresent || incomplete.ResultCanExport {
		t.Fatalf("incomplete gate tuple represented the edit as previously admitted: %+v", incomplete)
	}
	if _, _, err := fixture.app.memory.updateOSArtifactMetadata(qualityRecord.ID, map[string]string{"slideJuryArtifactDigest": juryDigest}); err != nil {
		t.Fatal(err)
	}
	projected = fixture.app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	if restored := projected.Messages[0].Thread; restored.ResultQualityState != authoredResultQualityEditedAfterAdmission || !restored.ResultCanContinue {
		t.Fatalf("restored exact jury tuple did not recover edited-review state: %+v", restored)
	}

	assertExportHeld := func(handler http.HandlerFunc, path, body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range adminCookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		handler(recorder, req)
		if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "must pass review") {
			t.Fatalf("edited authored export status=%d body=%s, want exact-admission conflict", recorder.Code, recorder.Body.String())
		}
	}
	assertExportHeld(artifactExportPDFHandler, "/artifacts/export-pdf", fmt.Sprintf(`{"artifactId":%q,"expectedVersion":%d}`, edited.ID, artifactVersion(edited)))
	assertExportHeld(deckPPTXExportHandler, "/artifacts/export-pptx", fmt.Sprintf(`{"artifactId":%q,"expectedVersion":%d,"sceneRef":%q}`, edited.ID, artifactVersion(edited), edited.Metadata[deckSceneRefMetadataKey]))

	previousFeedbackStart := startGoalFeedbackResumeAsync
	startedReview := false
	startGoalFeedbackResumeAsync = func(func()) { startedReview = true }
	t.Cleanup(func() { startGoalFeedbackResumeAsync = previousFeedbackStart })
	review := postArtifactAction(t, adminCookies, fmt.Sprintf(`{"id":%q,"action":"review_changes","resultArtifactId":%q}`, parent.ID, edited.ID))
	if review.Code != http.StatusAccepted {
		t.Fatalf("review changes status=%d body=%s", review.Code, review.Body.String())
	}
	if !startedReview {
		t.Fatal("review changes did not schedule the persisted goal re-drive")
	}
	reopened := mustGoalPlan(t, fixture.app, parent.ID)
	if reopened.State != goalStateExecute || reopened.subtaskByID("ship_deck").ArtifactID != edited.ID || reopened.subtaskByID("draft_compile").Status != subtaskReady {
		t.Fatalf("edited deck was not rebound into the rendered-review tail: state=%q ship=%+v render=%+v", reopened.State, reopened.subtaskByID("ship_deck"), reopened.subtaskByID("draft_compile"))
	}
}

func TestAuthoredDocumentAdmissionRevokesPDFAfterEditAndReopensRenderedReview(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "document-review-test-key")
	t.Setenv("BONFIRE_AGENT_RUNNER", agentRunnerOpenAIText)
	installFakeChildRunner(t)
	fixture := seedDocumentReportQualityFixture(t, 2)
	fixture.app.apiKey = "document-review-test-key"
	previousStart := startGoalThreadAsync
	startGoalThreadAsync = func(*kanbanBoardApp, string) {}
	t.Cleanup(func() { startGoalThreadAsync = previousStart })
	work, err := launchConversationOwnedGoalForTest(t, fixture.app, goalLaunchSpec{
		Objective: "Write the western creator opportunity report", CreatedBy: "aj@shareability.com", ToolTemplate: documentReportProcessID,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := mustGoalPlan(t, fixture.app, work.Artifact.ID)
	engine := newGoalEngine(fixture.app)
	if err := engine.prepareGoalRoute(&plan, work.Artifact.ID); err != nil {
		t.Fatal(err)
	}
	resultMetadata := map[string]string{"goalParentId": work.Artifact.ID, "goalSubtaskId": "write", "goalDeliverable": "true"}
	for key, value := range goalRouteChildBindingMetadata(&plan) {
		resultMetadata[key] = value
	}
	report, changed, err := fixture.app.memory.updateOSArtifactWithMetadata(fixture.report.ID, "", fixture.report.Text, "AJ", resultMetadata)
	if err != nil || !changed {
		t.Fatalf("bind report to authored goal: changed=%t err=%v", changed, err)
	}
	fixture.parentID = work.Artifact.ID
	fixture.report = report
	fixture.plan = &plan
	plan.Subtasks = []goalSubtask{
		{ID: "write", Role: processRoleWriter, Status: subtaskComplete, ArtifactID: report.ID},
		{ID: "quality_gate", Role: processRoleGate, Status: subtaskComplete, DependsOn: []string{"write"}, Review: &goalSubtaskReview{Verdict: goalReviewPass}},
		{ID: documentReportDraftRenderStageID, Role: processRoleCompile, Status: subtaskComplete, DependsOn: []string{"write", "quality_gate"}},
		{ID: documentReportJuryStageID, Role: processRoleCompile, Status: subtaskComplete, DependsOn: []string{documentReportDraftRenderStageID}},
	}
	attachFreshDocumentRender(t, &fixture)
	fixture.fileJury(t, 9.4, documentReportMinimumJurySeats, "KEEP")
	fileAdmittedPublishedDocument(t, &fixture)
	admission := plan.subtaskByID(documentReportRenderedAdmissionID)
	admission.DependsOn = []string{documentReportJuryStageID, documentReportDraftRenderStageID, "write"}
	publish := plan.subtaskByID(documentReportPublishStageID)
	publish.DependsOn = []string{documentReportRenderedAdmissionID}
	plan.State = goalStateVerified
	plan.Report.DeliverableArtifactID = fixture.report.ID
	rawPlan, _ := json.Marshal(plan)
	parent := mustArtifact(t, fixture.app, work.Artifact.ID)
	if _, _, err := fixture.app.updateOSArtifactWithMetadata(parent.ID, "", parent.Text, "Scout", map[string]string{
		"goalPlan": string(rawPlan), "status": "complete", "threadStatus": "complete",
	}); err != nil {
		t.Fatal(err)
	}
	current := mustArtifact(t, fixture.app, fixture.report.ID)
	if quality := fixture.app.authoredResultQualityForArtifact(current); quality != authoredResultQualityAdmitted {
		t.Fatalf("exact published document quality=%q, want admitted", quality)
	}
	previousApp := kanbanApp
	kanbanApp = fixture.app
	t.Cleanup(func() { kanbanApp = previousApp })
	adminCookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	exactDocument := documentStudioDocumentFromEntry(current)
	documentCopyPayload, _ := json.Marshal(map[string]any{
		"artifactId": current.ID, "expectedVersion": artifactVersion(current),
		"title": "Reviewed report copy", "fileName": "Reviewed report copy", "folderId": "", "document": exactDocument,
	})
	if copied := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/copies", string(documentCopyPayload), adminCookies, documentEditorCopyHandler); copied.Code != http.StatusCreated {
		t.Fatalf("exact admitted document copy status=%d body=%s", copied.Code, copied.Body.String())
	}
	modifiedDocument := exactDocument
	modifiedDocument.Markdown += "\n\nUnreviewed copy change."
	modifiedDocumentCopyPayload, _ := json.Marshal(map[string]any{
		"artifactId": current.ID, "expectedVersion": artifactVersion(current),
		"title": "Unreviewed report copy", "fileName": "Unreviewed report copy", "folderId": "", "document": modifiedDocument,
	})
	if copied := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/copies", string(modifiedDocumentCopyPayload), adminCookies, documentEditorCopyHandler); copied.Code != http.StatusConflict {
		t.Fatalf("modified managed document copy status=%d body=%s", copied.Code, copied.Body.String())
	}
	blockedPlan := plan
	blockedPlan.State = goalStateBlocked
	blockedPlanJSON, _ := json.Marshal(blockedPlan)
	beforeCopyRaceArtifacts := len(fixture.app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0))
	beforeCopyRaceFiles := len(fixture.app.assistantFilesForUser("aj@shareability.com"))
	previousCopyProbe := authoredCopyAfterAdmissionProbe
	t.Cleanup(func() { authoredCopyAfterAdmissionProbe = previousCopyProbe })
	copyParentMoved := false
	authoredCopyAfterAdmissionProbe = func() {
		if copyParentMoved {
			return
		}
		copyParentMoved = true
		parentSnapshot := mustArtifact(t, fixture.app, work.Artifact.ID)
		if _, _, updateErr := fixture.app.updateOSArtifactWithMetadata(parentSnapshot.ID, "", parentSnapshot.Text, "Scout", map[string]string{"goalPlan": string(blockedPlanJSON)}); updateErr != nil {
			t.Fatalf("move parent review during document copy: %v", updateErr)
		}
	}
	copyRace := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/copies", string(documentCopyPayload), adminCookies, documentEditorCopyHandler)
	authoredCopyAfterAdmissionProbe = previousCopyProbe
	if !copyParentMoved || copyRace.Code != http.StatusConflict {
		t.Fatalf("parent-raced document copy status=%d body=%s moved=%t", copyRace.Code, copyRace.Body.String(), copyParentMoved)
	}
	if got := len(fixture.app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0)); got != beforeCopyRaceArtifacts {
		t.Fatalf("parent-raced document copy left artifact rows: got=%d want=%d", got, beforeCopyRaceArtifacts)
	}
	if got := len(fixture.app.assistantFilesForUser("aj@shareability.com")); got != beforeCopyRaceFiles {
		t.Fatalf("parent-raced document copy left Files rows: got=%d want=%d", got, beforeCopyRaceFiles)
	}
	parent = mustArtifact(t, fixture.app, work.Artifact.ID)
	if _, _, err := fixture.app.updateOSArtifactWithMetadata(parent.ID, "", parent.Text, "Scout", map[string]string{"goalPlan": string(rawPlan)}); err != nil {
		t.Fatal(err)
	}

	edited, _, err := fixture.app.updateOSArtifact(current.ID, "", current.Text+"\n\n## Edited conclusion\n\nRun the bounded pilot.", "AJ")
	if err != nil {
		t.Fatal(err)
	}
	if quality := fixture.app.authoredResultQualityForArtifact(edited); quality != authoredResultQualityEditedAfterAdmission {
		t.Fatalf("edited published document quality=%q, want edited_after_admission", quality)
	}
	editedDocumentCopyPayload, _ := json.Marshal(map[string]any{
		"artifactId": edited.ID, "expectedVersion": artifactVersion(edited),
		"title": "Edited draft report copy", "fileName": "Edited draft report copy", "folderId": "", "document": documentStudioDocumentFromEntry(edited),
	})
	if copied := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/document/copies", string(editedDocumentCopyPayload), adminCookies, documentEditorCopyHandler); copied.Code != http.StatusConflict {
		t.Fatalf("edited authored document copy status=%d body=%s", copied.Code, copied.Body.String())
	}
	request := httptest.NewRequest(http.MethodPost, "/artifacts/export-pdf", strings.NewReader(fmt.Sprintf(`{"artifactId":%q,"expectedVersion":%d}`, edited.ID, artifactVersion(edited))))
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range adminCookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	artifactExportPDFHandler(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "must pass review") {
		t.Fatalf("edited document PDF status=%d body=%s, want exact-admission conflict", recorder.Code, recorder.Body.String())
	}

	previousFeedbackStart := startGoalFeedbackResumeAsync
	startedReview := false
	startGoalFeedbackResumeAsync = func(func()) { startedReview = true }
	t.Cleanup(func() { startGoalFeedbackResumeAsync = previousFeedbackStart })
	review := postArtifactAction(t, adminCookies, fmt.Sprintf(`{"id":%q,"action":"review_changes","resultArtifactId":%q}`, parent.ID, edited.ID))
	if review.Code != http.StatusAccepted {
		t.Fatalf("document review changes status=%d body=%s", review.Code, review.Body.String())
	}
	if !startedReview {
		t.Fatal("document review changes did not schedule the persisted goal re-drive")
	}
	reopened := mustGoalPlan(t, fixture.app, parent.ID)
	if reopened.State != goalStateExecute || reopened.subtaskByID("write").ArtifactID != edited.ID || reopened.subtaskByID(documentReportDraftRenderStageID).Status != subtaskReady {
		t.Fatalf("edited document was not rebound into rendered review: state=%q write=%+v render=%+v", reopened.State, reopened.subtaskByID("write"), reopened.subtaskByID(documentReportDraftRenderStageID))
	}
}

func TestScoutChatVerifiedGoalHasNoLatestSiblingFallback(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	goal, _, err := app.createOSArtifactWithMetadata("workflow", "Legacy verified goal", "goal", "AJ", map[string]string{
		"mode": "goal", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.createOSArtifactWithMetadata("workflow", "Unbound later sibling", "<!doctype html><html><body><section class=\"pg\">later</section></body></html>", "AJ", map[string]string{
		"type": artifactTypeHTMLDeck, "source": "scout_thread", "goalParentId": goal.ID,
	}); err != nil {
		t.Fatal(err)
	}
	plan := goalPlan{ProcessID: packagingStudioProcessID, State: goalStateVerified}
	rawPlan, _ := json.Marshal(plan)
	goal, _, err = app.updateOSArtifactWithMetadata(goal.ID, "", goal.Text, "AJ", map[string]string{"goalPlan": string(rawPlan)})
	if err != nil {
		t.Fatal(err)
	}
	thread := scoutChatThreadRecord{Messages: []scoutChatMessageRecord{{ID: "goal", Kind: "thread", Role: "scout", Thread: &scoutChatThreadRef{ID: goal.ID, Mode: "goal", Status: "complete", ArtifactID: goal.ID}}}}
	projected := app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	if ref := projected.Messages[0].Thread; ref.ResultArtifactID != "" || ref.ResultApprovalState != "" {
		t.Fatalf("unbound latest sibling hijacked verified goal result: %+v", ref)
	}
}

func TestScoutChatBlockedReportProjectsOnlyExactWriterSalvage(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	goal, _, err := app.createOSArtifactWithMetadata("workflow", "Blocked report goal", "goal", "AJ", map[string]string{
		"mode": "goal", "status": "error", "threadStatus": "error",
	})
	if err != nil {
		t.Fatal(err)
	}
	research, _, err := app.createOSArtifactWithMetadata("research", "Large evidence dossier", "# Evidence\n\n"+strings.Repeat("source row ", 500), "Scout", map[string]string{
		"type": artifactTypeMarkdown, "source": "scout_thread", "goalParentId": goal.ID, "goalSubtaskId": "external_research", "goalDeliverable": "true", "outputContract": packagingStudioExternalEvidenceContract,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := goalPlan{
		ProcessID: documentReportProcessID, State: goalStateBlocked, ResultStageID: "write", ResultOutputContract: documentReportOutputContract,
		Report: goalReport{DeliverableArtifactID: research.ID},
	}
	rawPlan, _ := json.Marshal(plan)
	goal, _, err = app.updateOSArtifactWithMetadata(goal.ID, "", goal.Text, "AJ", map[string]string{"goalPlan": string(rawPlan)})
	if err != nil {
		t.Fatal(err)
	}
	thread := scoutChatThreadRecord{Messages: []scoutChatMessageRecord{{ID: "goal", Kind: "thread", Role: "scout", Thread: &scoutChatThreadRef{ID: goal.ID, Mode: "goal", Status: "needs_attention", ArtifactID: goal.ID}}}}
	projected := app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	if got := projected.Messages[0].Thread.ResultArtifactID; got != "" {
		t.Fatalf("internal research child became blocked report salvage: %q", got)
	}

	draft, _, err := app.createOSArtifactWithMetadata("workflow", "Report draft", "# Report\n\nSaved draft.", "Scout", map[string]string{
		"type": artifactTypeMarkdown, "source": "scout_thread", "goalParentId": goal.ID, "goalSubtaskId": "write", "goalDeliverable": "true", "outputContract": documentReportOutputContract,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan.Report.DeliverableArtifactID = draft.ID
	rawPlan, _ = json.Marshal(plan)
	if _, _, err = app.updateOSArtifactWithMetadata(goal.ID, "", goal.Text, "AJ", map[string]string{"goalPlan": string(rawPlan)}); err != nil {
		t.Fatal(err)
	}
	projected = app.projectScoutChatThreadForViewer("aj@shareability.com", thread)
	ref := projected.Messages[0].Thread
	if got := ref.ResultArtifactID; got != draft.ID {
		t.Fatalf("exact writer salvage=%q, want %q", got, draft.ID)
	}
	if ref.ResultQualityState != authoredResultQualityDraftNeedsAttention || !ref.ResultCanEdit || !ref.ResultCanContinue || ref.ResultCanPresent || ref.ResultCanExport {
		t.Fatalf("blocked document salvage projected publication capability: %+v", ref)
	}
}

func TestScoutTerminalProjectionRetryRunningReplacesDeliveredPreviewAndRestarts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", dir+"/memory.jsonl")
	t.Setenv("KANBAN_BOARD_PATH", dir+"/board.json")
	app := newKanbanBoardApp()
	thread, artifact, agentThreadID := seedScoutTerminalProjection(t, app, "complete", map[string]string{
		"researchCitationCount":      "4",
		"researchSourceDomainCount":  "3",
		"researchQualityGate":        "passed",
		"researchEvidenceBinding":    "provider_fetched_urls",
		"researchSourceWindowDigest": strings.Repeat("e", 64),
	})
	app.updateScoutChatThreadRefs(agentThreadID, "complete", artifact.ID)
	delivered, _, err := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil || !strings.Contains(delivered.Preview, "Research delivered") {
		t.Fatalf("initial delivery thread=%+v err=%v", delivered, err)
	}

	artifact, _, err = app.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "", map[string]string{
		"threadStatus": "running", "status": "running", "progressNote": "provider-private progress must not project",
	})
	if err != nil {
		t.Fatal(err)
	}
	app.updateScoutChatThreadRefs(agentThreadID, "running", artifact.ID)
	running, _, err := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if running.Messages[1].Text != "Research in progress" || running.Preview != "Research in progress" || strings.Contains(strings.ToLower(running.Messages[1].Text+running.Preview), "delivered") {
		t.Fatalf("running retry retained terminal projection: text=%q preview=%q", running.Messages[1].Text, running.Preview)
	}
	payload := app.scoutChatThreadUpdatePayload(thread.OwnerEmail, running, running.Messages[1])
	if got, _ := payload["preview"].(string); got != "Research in progress" {
		t.Fatalf("running event preview=%q", got)
	}
	restarted := newKanbanBoardApp()
	afterRestart, _, err := restarted.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil || afterRestart.Messages[1].Text != "Research in progress" || afterRestart.Preview != "Research in progress" {
		t.Fatalf("running restart projection=%+v err=%v", afterRestart, err)
	}

	artifact, _, err = restarted.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "", map[string]string{
		"threadStatus": "complete", "status": "complete", "progressNote": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	restarted.updateScoutChatThreadRefs(agentThreadID, "complete", artifact.ID)
	redelivered, _, err := restarted.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil || redelivered.Messages[1].Text != "Research delivered · 4 cited source links · 3 domains" || redelivered.Preview != redelivered.Messages[1].Text {
		t.Fatalf("retry completion projection=%+v err=%v", redelivered, err)
	}
}

func TestScoutTerminalProjectionRejectsStaleBindingAndContainsFailureBody(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	thread, artifact, agentThreadID := seedScoutTerminalProjection(t, app, "error", map[string]string{
		"error": "provider secret raw failure: sk-do-not-project",
	})

	other, _, err := app.createOSArtifactWithMetadata("research", "other", "other private body", "AJ", map[string]string{
		"mode": "research", "threadId": "agent-thread-other", "threadStatus": "complete", "status": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	app.updateScoutChatThreadRefs(agentThreadID, "complete", other.ID)
	stale, _, err := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Messages[1].Text != "research workstream confirmed — running now" {
		t.Fatalf("mismatched artifact changed text to %q", stale.Messages[1].Text)
	}
	// The first legacy ref update now points at the supplied artifact. A replay
	// therefore has an exact artifact ID but must still reject its mismatched
	// current thread generation.
	app.updateScoutChatThreadRefs(agentThreadID, "complete", other.ID)
	stale, _, err = app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Messages[1].Text != "research workstream confirmed — running now" {
		t.Fatalf("mismatched artifact thread changed text to %q", stale.Messages[1].Text)
	}

	// Restore the exact message binding and project the authenticated terminal
	// error postimage. Raw artifact/provider error material must stay behind the
	// artifact read boundary.
	stale.Messages[1].Thread.ArtifactID = artifact.ID
	stale.Messages[1].Thread.Status = "running"
	if err := app.saveScoutChatThread(stale); err != nil {
		t.Fatal(err)
	}
	app.updateScoutChatThreadRefs(agentThreadID, "error", artifact.ID)
	failed, _, err := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := failed.Messages[1].Text; got != "Research needs attention" {
		t.Fatalf("failure text=%q", got)
	}
	serialized := failed.Messages[1].Text
	for _, forbidden := range []string{"provider", "secret", "sk-do-not-project", artifact.Text} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("failure projection leaked %q in %q", forbidden, serialized)
		}
	}
}

func TestScoutTerminalProjectionOmitsUnverifiedResearchCounts(t *testing.T) {
	artifact := meetingMemoryEntry{ID: "artifact-unverified", Metadata: map[string]string{
		"mode": "research", "threadId": "thread-unverified", "threadStatus": "complete", "status": "complete",
		"researchCitationCount": "91", "researchSourceDomainCount": "88",
	}}
	if got, ok := scoutChatTerminalWorkCopy(artifact, "thread-unverified", "complete"); !ok || got != "Research delivered" {
		t.Fatalf("unverified counts entered terminal copy: %q %v", got, ok)
	}
	artifact.Metadata["researchQualityGate"] = "passed"
	artifact.Metadata["researchEvidenceBinding"] = "provider_fetched_urls"
	artifact.Metadata["researchSourceWindowDigest"] = "not-a-managed-receipt"
	if got, ok := scoutChatTerminalWorkCopy(artifact, "thread-unverified", "complete"); !ok || got != "Research delivered" {
		t.Fatalf("malformed receipt entered terminal copy: %q %v", got, ok)
	}
}

func TestScoutTerminalProjectionReconcilesLegacyCompletedRefAfterRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", dir+"/memory.jsonl")
	t.Setenv("KANBAN_BOARD_PATH", dir+"/board.json")
	app := newKanbanBoardApp()
	thread, artifact, agentThreadID := seedScoutTerminalProjection(t, app, "complete", map[string]string{
		"researchCitationCount":      "7",
		"researchSourceDomainCount":  "6",
		"researchQualityGate":        "passed",
		"researchEvidenceBinding":    "provider_fetched_urls",
		"researchSourceWindowDigest": strings.Repeat("c", 64),
	})

	// Model the live legacy postimage: the durable ref is already terminal,
	// while its work-card text and list preview still contain launch copy.
	legacy, _, err := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Messages[1].Thread.Status = "complete"
	legacy.Preview = "research workstream confirmed — running now"
	if err := app.saveScoutChatThread(legacy); err != nil {
		t.Fatal(err)
	}

	restarted := newKanbanBoardApp()
	restarted.updateScoutChatThreadRefs(agentThreadID, "complete", artifact.ID)
	reconciled, _, err := restarted.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := "Research delivered · 7 cited source links · 6 domains"
	if reconciled.Messages[1].Text != want || reconciled.Preview != want {
		t.Fatalf("legacy terminal projection not reconciled: text=%q preview=%q", reconciled.Messages[1].Text, reconciled.Preview)
	}
	updatedAt := reconciled.UpdatedAt
	restarted.updateScoutChatThreadRefs(agentThreadID, "complete", artifact.ID)
	replayed, _, err := restarted.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.UpdatedAt != updatedAt || replayed.Messages[1].Text != want || replayed.Preview != want {
		t.Fatalf("legacy reconciliation was not idempotent: %#v", replayed)
	}
}

func TestScoutTerminalProjectionRegisteredAdminReconciliationIsExactAndIdempotent(t *testing.T) {
	setupAuthTestEnv(t)
	adminCookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	memberCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", dir+"/memory.jsonl")
	t.Setenv("KANBAN_BOARD_PATH", dir+"/board.json")
	previousApp := kanbanApp
	kanbanApp = newKanbanBoardApp()
	t.Cleanup(func() { kanbanApp = previousApp })
	thread, artifact, _ := seedScoutTerminalProjection(t, kanbanApp, "complete", map[string]string{
		"researchCitationCount":      "9",
		"researchSourceDomainCount":  "8",
		"researchQualityGate":        "passed",
		"researchEvidenceBinding":    "provider_fetched_urls",
		"researchSourceWindowDigest": strings.Repeat("d", 64),
	})
	legacy, _, err := kanbanApp.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Messages[1].Thread.Status = "complete"
	legacy.Preview = "research workstream confirmed — running now"
	if err := kanbanApp.saveScoutChatThread(legacy); err != nil {
		t.Fatal(err)
	}
	kanbanApp = newKanbanBoardApp()

	reconcileAs := func(cookies []*http.Cookie, body string) *httptest.ResponseRecorder {
		t.Helper()
		path := "/assistant/chat-threads/" + thread.ID + "/messages/scout-chat-message-terminal-work/reconcile-terminal"
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantChatThreadHandler(recorder, request)
		return recorder
	}
	header := artifactAuthorizationHeaderFromEntry(artifact)
	body := fmt.Sprintf(`{"artifactId":%q,"expectedArtifactVersion":%d,"expectedContentDigest":%q,"expectedStatus":"complete"}`, artifact.ID, header.ContentRevision, header.ContentDigest)
	if recorder := reconcileAs(memberCookies, body); recorder.Code != http.StatusForbidden {
		t.Fatalf("member reconciliation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := reconcileAs(adminCookies, strings.TrimSuffix(body, "}")+`,"status":"complete"}`); recorder.Code != http.StatusBadRequest {
		t.Fatalf("client-supplied authority status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := reconcileAs(adminCookies, `{"artifactId":"stale-artifact","expectedArtifactVersion":1,"expectedContentDigest":"`+strings.Repeat("a", 64)+`","expectedStatus":"complete"}`); recorder.Code != http.StatusNotFound {
		t.Fatalf("stale reconciliation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	// Force a metadata-only artifact revision between the admitted snapshot and
	// the final chat save. The atomic store revalidation must refuse the stale
	// projection and leave both message text and list preview untouched.
	scoutTerminalProjectionBeforeSaveProbe = func() {
		scoutTerminalProjectionBeforeSaveProbe = nil
		if _, _, updateErr := kanbanApp.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, "", map[string]string{"progressNote": "newer current postimage"}); updateErr != nil {
			t.Errorf("interleaved artifact update: %v", updateErr)
		}
	}
	t.Cleanup(func() { scoutTerminalProjectionBeforeSaveProbe = nil })
	if recorder := reconcileAs(adminCookies, body); recorder.Code != http.StatusNotFound {
		t.Fatalf("interleaved stale reconciliation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	stillLegacy, _, err := kanbanApp.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil || stillLegacy.Preview != "research workstream confirmed — running now" || stillLegacy.Messages[1].Text != "research workstream confirmed — running now" {
		t.Fatalf("interleaved stale reconciliation mutated thread=%+v err=%v", stillLegacy, err)
	}
	recorder := reconcileAs(adminCookies, body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("admin reconciliation status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	response := struct {
		OK         bool                   `json:"ok"`
		Reconciled bool                   `json:"reconciled"`
		Thread     scoutChatThreadRecord  `json:"thread"`
		Message    scoutChatMessageRecord `json:"message"`
	}{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	want := "Research delivered · 9 cited source links · 8 domains"
	if !response.OK || !response.Reconciled || response.Thread.Preview != want || response.Message.Text != want || response.Message.Thread == nil || response.Message.Thread.Status != "complete" {
		t.Fatalf("reconciliation response=%+v", response)
	}
	replay := reconcileAs(adminCookies, body)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	replayResponse := struct {
		Reconciled bool `json:"reconciled"`
	}{}
	if err := json.Unmarshal(replay.Body.Bytes(), &replayResponse); err != nil {
		t.Fatal(err)
	}
	if replayResponse.Reconciled {
		t.Fatal("exact replay reported a second mutation")
	}
	persisted, _, err := kanbanApp.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
	if err != nil || persisted.Preview != want || persisted.Messages[1].Text != want {
		t.Fatalf("durable reconciliation thread=%+v err=%v", persisted, err)
	}
}
