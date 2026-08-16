package main

// Tests for simple in-thread presentation/outline routing through the workstream
// path with mode=artifacts and tool_template, bypassing the goal loop.
//
// The fix ensures that:
// 1. Simple presentation/outline asks route to propose_workstream with
//    mode=artifacts and tool_template=deck_outline, NOT the goal loop.
// 2. The workstream launch honors the tool template's output contract.
// 3. Draft emission to origin thread when goal parks on NEEDS ATTENTION.

import (
	"context"
	"strings"
	"testing"
)

// The router tools schema includes "artifacts" as a valid workstream mode
// alongside research, design, grill, and workflow.
func TestScoutRouterToolsIncludeArtifactsMode(t *testing.T) {
	tools := scoutRouterTools()
	var workstreamTool *anthropicTool
	for i := range tools {
		if tools[i].Name == "propose_workstream" {
			workstreamTool = &tools[i]
			break
		}
	}
	if workstreamTool == nil {
		t.Fatal("propose_workstream tool not found in router tools")
	}

	props, ok := workstreamTool.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("propose_workstream missing properties")
	}
	modeSchema, ok := props["mode"].(map[string]any)
	if !ok {
		t.Fatal("propose_workstream missing mode property")
	}
	modeEnum, ok := modeSchema["enum"].([]string)
	if !ok {
		t.Fatal("propose_workstream mode missing enum")
	}

	hasArtifacts := false
	for _, mode := range modeEnum {
		if mode == "artifacts" {
			hasArtifacts = true
			break
		}
	}
	if !hasArtifacts {
		t.Fatalf("propose_workstream mode enum=%v, want 'artifacts' included", modeEnum)
	}

	// Also verify tool_template field exists
	toolTemplateSchema, ok := props["tool_template"].(map[string]any)
	if !ok {
		t.Fatal("propose_workstream missing tool_template property")
	}
	if _, hasEnum := toolTemplateSchema["enum"]; !hasEnum {
		t.Fatal("propose_workstream tool_template missing enum of tool ids")
	}
}

// The conversation intent validation accepts artifacts as a valid workstream mode.
func TestConversationWorkValidationAcceptsArtifactsMode(t *testing.T) {
	work := conversationWorkDecision{
		Kind:      conversationWorkWorkstream,
		Mode:      "artifacts",
		Objective: "Create a 5-slide presentation outline",
	}
	if err := work.validateRoute(); err != nil {
		t.Fatalf("artifacts mode validation failed: %v", err)
	}

	// With tool template
	work.ToolID = "deck_outline"
	if err := work.validateRoute(); err != nil {
		t.Fatalf("artifacts mode with tool_template validation failed: %v", err)
	}

	// Invalid mode still fails
	work.Mode = "invented_mode"
	if err := work.validateRoute(); err == nil {
		t.Fatal("invented mode should fail validation")
	}
}

// The propose_workstream tool parsing correctly handles artifacts mode with tool_template.
func TestScoutRouterProposalFromToolUseArtifactsMode(t *testing.T) {
	// Basic artifacts mode without tool_template
	block := decodeAnthropicBlock(mockAnthropicToolUseBlock("toolu_ws", "propose_workstream", map[string]any{
		"mode":      "artifacts",
		"objective": "Create a quick presentation outline",
	}))
	proposal := scoutRouterProposalFromToolUse(block, "make a 5-slide outline")
	if proposal == nil {
		t.Fatal("artifacts workstream proposal must not be nil")
	}
	if proposal.Kind != scoutRouterProposalKindWorkstream {
		t.Fatalf("kind=%q, want workstream", proposal.Kind)
	}
	if proposal.Mode != "artifacts" {
		t.Fatalf("mode=%q, want artifacts", proposal.Mode)
	}

	// Artifacts mode with tool_template=deck_outline
	block = decodeAnthropicBlock(mockAnthropicToolUseBlock("toolu_ws2", "propose_workstream", map[string]any{
		"mode":          "artifacts",
		"objective":     "Create a 5-slide presentation outline for the investor pitch",
		"tool_template": "deck_outline",
	}))
	proposal = scoutRouterProposalFromToolUse(block, "make a presentation outline")
	if proposal == nil {
		t.Fatal("artifacts workstream with tool_template must not be nil")
	}
	if proposal.Mode != "artifacts" {
		t.Fatalf("mode=%q, want artifacts", proposal.Mode)
	}
	if proposal.ToolID != "deck_outline" {
		t.Fatalf("toolId=%q, want deck_outline (carried from tool_template)", proposal.ToolID)
	}
	if !strings.Contains(proposal.Summary, "Deck Outline") || !strings.Contains(proposal.Summary, "no goal loop") {
		t.Fatalf("summary=%q, want mention of tool name and no goal loop", proposal.Summary)
	}
	// Weight should be quick pass, not goal loop
	if proposal.WeightLabel != scoutProposalWeightQuickPass {
		t.Fatalf("weightLabel=%q, want quick pass", proposal.WeightLabel)
	}

	// Invalid tool_template is silently dropped (degrades to plain artifacts mode)
	block = decodeAnthropicBlock(mockAnthropicToolUseBlock("toolu_ws3", "propose_workstream", map[string]any{
		"mode":          "artifacts",
		"objective":     "Some objective",
		"tool_template": "invented_tool",
	}))
	proposal = scoutRouterProposalFromToolUse(block, "query")
	if proposal == nil {
		t.Fatal("invalid tool_template should degrade to plain artifacts, not nil")
	}
	if proposal.ToolID != "" {
		t.Fatalf("toolId=%q, want empty (invalid template dropped)", proposal.ToolID)
	}
}

// The router system prompt includes guidance for routing simple in-thread
// presentation/outline asks to propose_workstream with mode=artifacts.
func TestScoutRouterSystemPromptIncludesArtifactsGuidance(t *testing.T) {
	prompt := scoutRouterSystemPrompt()
	expectations := []string{
		"simple in-thread presentation/outline asks",
		"propose_workstream mode=artifacts tool_template=deck_outline",
		"bypass the goal loop",
	}
	for _, want := range expectations {
		if !strings.Contains(prompt, want) {
			t.Errorf("router system prompt missing %q", want)
		}
	}
}

// A simple presentation outline request routes through workstream, not goal loop.
func TestScoutChatRouterSimplePresentationRoutesToArtifactsWorkstream(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "openai-router-test")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	kanbanApp.apiKey = "openai-router-test"
	t.Cleanup(func() { kanbanApp = previousApp })

	// Mock router to return artifacts workstream with deck_outline template (via tool_id)
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if request.Workflow != "scout_route" {
			t.Fatal("expected router workflow")
		}
		return openAIScoutRouteJSON(t, openAIScoutRouterOutput{
			Outcome:   string(conversationIntentStartPrivateWork),
			Route:     "workstream",
			Mode:      "artifacts",
			ToolID:    "deck_outline",
			Objective: "Create a 5-slide investor pitch outline",
		}), nil
	})

	// Disable actual agent thread execution
	startAgentThreadAsyncPrev := startAgentThreadAsync
	var launchedThread scoutAgentThread
	startAgentThreadAsync = func(_ *kanbanBoardApp, thread scoutAgentThread) {
		launchedThread = thread
	}
	t.Cleanup(func() { startAgentThreadAsync = startAgentThreadAsyncPrev })

	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	private, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Scout", "")
	if err != nil {
		t.Fatalf("create private thread: %v", err)
	}

	// The simple request
	response, err := kanbanApp.appendScoutChatThreadMessage(context.Background(), user, private.ID, "make a 5-slide outline for the investor pitch, keep it in-thread", nil, "")
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	// Should have launched as a workstream (mode=artifacts), not goal
	launched, ok := response["agentThread"].(scoutAgentThread)
	if !ok {
		t.Fatalf("agentThread type=%T, want direct workstream launch", response["agentThread"])
	}
	if launched.Mode != "artifacts" {
		t.Fatalf("mode=%q, want artifacts (simple workstream, not goal loop)", launched.Mode)
	}
	if launchedThread.ID == "" {
		t.Fatal("agent thread was not started")
	}

	// Verify intent outcome
	if response["intentOutcome"] != string(conversationIntentStartPrivateWork) {
		t.Fatalf("intentOutcome=%q, want start_private_work", response["intentOutcome"])
	}

	// Verify work card is created
	saved := response["thread"].(scoutChatThreadRecord)
	if len(saved.Messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(saved.Messages))
	}
	workCard := saved.Messages[len(saved.Messages)-1]
	if workCard.Kind != "thread" || workCard.Thread == nil {
		t.Fatalf("work card kind=%q thread=%v, want thread card", workCard.Kind, workCard.Thread)
	}
}

// The goal engine emits salvaged drafts to the origin thread when parking on NEEDS ATTENTION.
func TestGoalSalvagedDraftEmittedToOriginThread(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	// Create a private conversation thread to be the origin
	user := accountStore().findUser("aj@shareability.com")
	if user == nil {
		t.Fatal("seed user aj@shareability.com missing")
	}
	originThread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Test Goal", "")
	if err != nil {
		t.Fatalf("create origin thread: %v", err)
	}

	// Set up the goal to fail review and block with a salvageable draft
	installFakeResponder(t, goalResponderRoutes{
		decompose: `{"subtasks":[{"id":"st-1","title":"Draft presentation","mode":"design","authority":"workspace_write","dependsOn":[]}]}`,
		review:    `{"verdict":"fail","score":7,"reasons":"good draft but missing key slides"}`,
	})

	longBody := "# Presentation Draft\n\n" + strings.Repeat("A substantial slide content paragraph. ", 15)
	folds := installAwaitableChildRunner(t, longBody)

	// Launch goal with origin in the private thread
	origin := map[string]string{
		"originKind": agentThreadOriginPrivateThread,
		"originId":   originThread.ID,
	}
	thread, err := kanbanApp.launchGoalThread(goalLaunchSpec{
		Objective: "Create presentation outline",
		CreatedBy: "aj@shareability.com",
		Origin:    origin,
	})
	if err != nil {
		t.Fatalf("launchGoalThread: %v", err)
	}

	kanbanApp.runGoalThread(thread.Artifact.ID)
	plan := waitForGoalStage(t, kanbanApp, thread.Artifact.ID, goalStateBlocked)
	folds.Wait()

	// Verify draft was salvaged
	draftID := plan.Report.DeliverableArtifactID
	if draftID == "" {
		t.Fatal("no draft was salvaged")
	}

	// Read the origin thread and verify the draft message was posted
	updatedOrigin, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", originThread.ID)
	if err != nil {
		t.Fatalf("read origin thread: %v", err)
	}

	var draftMessage *scoutChatMessageRecord
	for i := range updatedOrigin.Messages {
		msg := &updatedOrigin.Messages[i]
		if msg.Kind == "artifact" && msg.Thread != nil && msg.Thread.ArtifactID == draftID {
			draftMessage = msg
			break
		}
	}

	if draftMessage == nil {
		t.Fatalf("no draft message found in origin thread; messages=%d", len(updatedOrigin.Messages))
	}
	if !strings.Contains(draftMessage.Text, "saved") || !strings.Contains(draftMessage.Text, "needs attention") {
		t.Fatalf("draft message text=%q, want indication of saved draft needing attention", draftMessage.Text)
	}
	if draftMessage.Thread.Status != "needs_attention" {
		t.Fatalf("draft thread ref status=%q, want needs_attention", draftMessage.Thread.Status)
	}
}

// postGoalSalvagedDraftMessage correctly builds the chat message.
func TestPostGoalSalvagedDraftMessageFormat(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	// Create a mock draft artifact
	draft, _, err := app.createOSArtifactWithMetadata("artifacts", "Presentation Draft", "# Draft\n\nContent here", "tester", map[string]string{
		"title": "Presentation Draft",
	})
	if err != nil {
		t.Fatalf("create draft artifact: %v", err)
	}

	// Create origin thread
	originThread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Test", "")
	if err != nil {
		t.Fatalf("create origin thread: %v", err)
	}

	// Create parent goal artifact with origin binding
	parent, _, err := app.createOSArtifactWithMetadata("workflow", "Goal", "Goal body", "tester", map[string]string{
		"originKind": agentThreadOriginPrivateThread,
		"originId":   originThread.ID,
	})
	if err != nil {
		t.Fatalf("create parent artifact: %v", err)
	}

	// Post the salvaged draft message
	app.postGoalSalvagedDraftMessage(parent.ID, draft.ID, "missing the ask section")

	// Verify the message was posted
	updated, _, err := app.scoutChatThreadByID("aj@shareability.com", originThread.ID)
	if err != nil {
		t.Fatalf("read origin thread: %v", err)
	}

	if len(updated.Messages) == 0 {
		t.Fatal("no message posted to origin thread")
	}
	msg := updated.Messages[len(updated.Messages)-1]
	if msg.Kind != "artifact" {
		t.Fatalf("message kind=%q, want artifact", msg.Kind)
	}
	if msg.Thread == nil || msg.Thread.ArtifactID != draft.ID {
		t.Fatalf("message thread ref=%v, want artifact ref to %q", msg.Thread, draft.ID)
	}
	if !strings.Contains(msg.Text, "Presentation Draft") {
		t.Fatalf("message text=%q, want draft title", msg.Text)
	}
	if !strings.Contains(msg.Text, "missing the ask section") {
		t.Fatalf("message text=%q, want gap description", msg.Text)
	}
}

// Empty draft ID does not post anything.
func TestPostGoalSalvagedDraftMessageSkipsEmpty(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)

	originThread, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Test", "")
	if err != nil {
		t.Fatalf("create origin thread: %v", err)
	}

	parent, _, err := app.createOSArtifactWithMetadata("workflow", "Goal", "Goal body", "tester", map[string]string{
		"originKind": agentThreadOriginPrivateThread,
		"originId":   originThread.ID,
	})
	if err != nil {
		t.Fatalf("create parent artifact: %v", err)
	}

	// Post with empty draft ID - should be a no-op
	app.postGoalSalvagedDraftMessage(parent.ID, "", "some gap")
	app.postGoalSalvagedDraftMessage(parent.ID, "   ", "some gap")

	updated, _, err := app.scoutChatThreadByID("aj@shareability.com", originThread.ID)
	if err != nil {
		t.Fatalf("read origin thread: %v", err)
	}

	if len(updated.Messages) != 0 {
		t.Fatalf("expected no messages posted for empty draft ID, got %d", len(updated.Messages))
	}
}
