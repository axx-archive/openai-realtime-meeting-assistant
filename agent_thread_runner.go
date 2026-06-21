package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

const defaultAgentThreadRequestTimeout = 60 * time.Second

type scoutAgentThread struct {
	ID       string              `json:"id"`
	Mode     string              `json:"mode"`
	Query    string              `json:"query"`
	Status   string              `json:"status"`
	Artifact meetingMemoryEntry  `json:"artifact"`
	Actions  []osAssistantAction `json:"actions,omitempty"`
}

var startAgentThreadAsync = func(app *kanbanBoardApp, thread scoutAgentThread) {
	go app.runAgentThread(thread)
}

func (app *kanbanBoardApp) launchAgentThread(mode string, query string, createdBy string) (scoutAgentThread, error) {
	mode = normalizeAgentThreadMode(mode)
	if mode == "" {
		return scoutAgentThread{}, fmt.Errorf("thread mode is required")
	}
	query = canonicalizeBoardText(query)
	if query == "" {
		return scoutAgentThread{}, fmt.Errorf("thread query is required")
	}

	threadID := fmt.Sprintf("agent-thread-%s-%d", mode, time.Now().UnixNano())
	worker := configuredAgentThreadWorkerName()
	content := buildAgentThreadScaffold(mode, query, app.snapshotState(), app.memorySnapshotForClients(12))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	metadata := map[string]string{
		"source":          "scout_thread",
		"threadId":        threadID,
		"threadQuery":     query,
		"agentLoop":       "realtime_controlled_workforce",
		"status":          "running",
		"threadStatus":    "running",
		"goalStatus":      "running",
		"currentStage":    "execute_in_order",
		"progressPercent": "35",
		"workflowStages":  goalWorkflowStageMetadata,
		"reviewGate":      "pending",
		"queuedAt":        now,
		"startedAt":       now,
		"published":       "false",
		"worker":          worker,
		"workerBoundary":  agentThreadWorkerBoundary(worker),
		"latestThreadRun": threadID,
	}
	for key, value := range agentThreadModeMetadata(mode) {
		metadata[key] = value
	}
	artifact, _, err := app.createOSArtifactWithMetadata(mode, query, content, createdBy, metadata)
	if err != nil {
		return scoutAgentThread{}, err
	}
	if strings.TrimSpace(artifact.ID) == "" {
		return scoutAgentThread{}, fmt.Errorf("thread artifact was not saved")
	}

	actions := app.osAssistantActions(query, mode, artifact)
	thread := scoutAgentThread{
		ID:       threadID,
		Mode:     mode,
		Query:    query,
		Status:   "running",
		Artifact: artifact,
		Actions:  actions,
	}

	broadcastKanbanEvent("memory", app.memorySnapshotForClients(20))
	broadcastAssistantEvent("action", assistantToolLabel(mode)+" thread launched", map[string]any{
		"tool":       "launch_agent_thread",
		"thread":     thread,
		"artifact":   artifact,
		"actions":    actions,
		"voiceState": "listening",
	})

	startAgentThreadAsync(app, thread)
	return thread, nil
}

func normalizeAgentThreadMode(mode string) string {
	switch normalizeRealtimeArtifactMode(mode) {
	case "artifacts", "research", "design", "grill", "workflow":
		return normalizeRealtimeArtifactMode(mode)
	default:
		return ""
	}
}

func (app *kanbanBoardApp) runAgentThread(thread scoutAgentThread) {
	ctx, cancel := context.WithTimeout(context.Background(), agentThreadRequestTimeout())
	defer cancel()

	workerResult, err := app.produceAgentThreadArtifactWithWorker(ctx, thread, createOpenAITextResponse)
	output := workerResult.Text
	if err == nil && !workerResult.Terminal {
		app.updateQueuedAgentThread(thread, workerResult)
		return
	}

	status := "complete"
	message := assistantToolLabel(thread.Mode) + " thread complete"
	metadata := map[string]string{
		"status":          "complete",
		"threadStatus":    "complete",
		"goalStatus":      "verified",
		"currentStage":    "verify_goal_completed",
		"progressPercent": "100",
		"reviewGate":      "passed",
		"completedAt":     time.Now().UTC().Format(time.RFC3339Nano),
		"latestThreadRun": thread.ID,
	}
	for key, value := range workerResult.Metadata {
		if strings.TrimSpace(value) != "" {
			metadata[key] = value
		}
	}
	if err != nil {
		status = "error"
		message = assistantToolLabel(thread.Mode) + " thread needs attention"
		output = buildAgentThreadError(thread, err)
		metadata["status"] = "error"
		metadata["threadStatus"] = "error"
		metadata["goalStatus"] = "needs_attention"
		metadata["currentStage"] = "gate_before_shipping"
		metadata["progressPercent"] = "72"
		metadata["reviewGate"] = "blocked"
		metadata["error"] = err.Error()
	}

	artifact, _, updateErr := app.updateOSArtifactWithMetadata(thread.Artifact.ID, thread.Artifact.Metadata["title"], output, scoutParticipantName, metadata)
	if updateErr != nil {
		log.Errorf("Failed to update Scout thread artifact %s: %v", thread.ID, updateErr)
		broadcastAssistantEvent("error", "Scout thread could not update its artifact", map[string]any{
			"tool":     "launch_agent_thread",
			"threadId": thread.ID,
			"artifact": thread.Artifact,
		})
		return
	}

	actions := app.osAssistantActions(thread.Query, thread.Mode, artifact)
	broadcastKanbanEvent("memory", app.memorySnapshotForClients(20))
	broadcastAssistantEvent("action", message, map[string]any{
		"tool":       "launch_agent_thread",
		"thread":     scoutAgentThread{ID: thread.ID, Mode: thread.Mode, Query: thread.Query, Status: status, Artifact: artifact, Actions: actions},
		"artifact":   artifact,
		"actions":    actions,
		"voiceState": "listening",
	})
}

func (app *kanbanBoardApp) updateQueuedAgentThread(thread scoutAgentThread, workerResult agentThreadWorkerResult) {
	output := strings.TrimSpace(workerResult.Text)
	if output == "" {
		output = thread.Artifact.Text
	}

	status := firstNonEmptyString(workerResult.Metadata["threadStatus"], workerResult.Metadata["status"], "queued")
	message := assistantToolLabel(thread.Mode) + " thread queued"
	switch status {
	case codexJobStatusApprovalRequired:
		message = assistantToolLabel(thread.Mode) + " thread needs approval"
	case codexJobStatusRunning:
		message = assistantToolLabel(thread.Mode) + " thread running"
	}

	metadata := map[string]string{
		"latestThreadRun": thread.ID,
	}
	for key, value := range workerResult.Metadata {
		if strings.TrimSpace(value) != "" {
			metadata[key] = value
		}
	}

	artifact, _, updateErr := app.updateOSArtifactWithMetadata(thread.Artifact.ID, thread.Artifact.Metadata["title"], output, scoutParticipantName, metadata)
	if updateErr != nil {
		log.Errorf("Failed to update queued Scout thread artifact %s: %v", thread.ID, updateErr)
		broadcastAssistantEvent("error", "Scout thread could not update its queued artifact", map[string]any{
			"tool":     "launch_agent_thread",
			"threadId": thread.ID,
			"artifact": thread.Artifact,
		})
		return
	}

	actions := app.osAssistantActions(thread.Query, thread.Mode, artifact)
	broadcastKanbanEvent("memory", app.memorySnapshotForClients(20))
	broadcastAssistantEvent("action", message, map[string]any{
		"tool":       "launch_agent_thread",
		"thread":     scoutAgentThread{ID: thread.ID, Mode: thread.Mode, Query: thread.Query, Status: status, Artifact: artifact, Actions: actions},
		"artifact":   artifact,
		"actions":    actions,
		"voiceState": "listening",
	})
}

func agentThreadRequestTimeout() time.Duration {
	if configuredAgentThreadWorkerMode() == agentThreadWorkerCodexExec {
		return codexExecConfigFromEnv().Timeout
	}

	return defaultAgentThreadRequestTimeout
}

type agentThreadWorkerResult struct {
	Text     string
	Metadata map[string]string
	Terminal bool
}

func (app *kanbanBoardApp) produceAgentThreadArtifactWithWorker(ctx context.Context, thread scoutAgentThread, responder openAITextResponder) (agentThreadWorkerResult, error) {
	switch configuredAgentThreadWorkerMode() {
	case agentThreadWorkerCodexExec:
		if configuredCodexRunnerMode() == codexRunnerModeLocalExec {
			return app.produceCodexAgentThreadArtifact(ctx, thread)
		}
		return app.enqueueCodexAgentThreadArtifact(ctx, thread)
	default:
		output, err := app.produceAgentThreadArtifact(ctx, thread, responder)
		return agentThreadWorkerResult{
			Text: output,
			Metadata: map[string]string{
				"worker":         "openai_text_response",
				"workerBoundary": "responses_artifact_writer",
			},
			Terminal: true,
		}, err
	}
}

func (app *kanbanBoardApp) produceAgentThreadArtifact(ctx context.Context, thread scoutAgentThread, responder openAITextResponder) (string, error) {
	if app == nil {
		return "", fmt.Errorf("assistant is unavailable")
	}
	if responder == nil {
		responder = createOpenAITextResponse
	}
	apiKey := app.currentOpenAIAPIKey()
	if strings.TrimSpace(apiKey) == "" {
		return "", fmt.Errorf("OPENAI_API_KEY is not configured")
	}

	output, err := responder(ctx, apiKey, openAITextRequest{
		Model:           meetingBrainModel(),
		Instructions:    agentThreadInstructions(thread.Mode),
		Input:           buildAgentThreadInput(thread, app.snapshotState(), app.memorySnapshotForClients(20), time.Now()),
		ReasoningEffort: "low",
		Verbosity:       "medium",
		MaxOutputTokens: 2600,
	})
	if err != nil {
		return "", err
	}
	output = strings.TrimSpace(output)
	if output == "" {
		return "", fmt.Errorf("Scout thread produced no artifact text")
	}

	return output, nil
}

func (app *kanbanBoardApp) currentOpenAIAPIKey() string {
	if app == nil {
		return ""
	}
	app.mu.Lock()
	apiKey := strings.TrimSpace(app.apiKey)
	app.mu.Unlock()
	if apiKey != "" {
		return apiKey
	}
	return strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
}

func buildAgentThreadScaffold(mode string, query string, board kanbanBoardState, memory []meetingMemoryEntry) string {
	contextLine := boardAndMemoryContextLine(board, memory)
	lines := []string{
		"Scout work thread",
		"",
		"Vision: " + compactAssistantLine(query),
		"Status: running",
		"Thread mode: " + assistantToolLabel(mode),
		"Workspace context: " + contextLine,
		"",
		"Execution log",
		"- Scout created the artifact and queued a server-side thread.",
		"- The Realtime 2 voice loop stays free while the worker runs.",
		"- The artifact will update when the worker completes or hits an error.",
	}
	return strings.Join(appendGoalWorkflow(lines, mode, query, contextLine, agentThreadDeliverable(mode), contextLine), "\n")
}

func buildAgentThreadError(thread scoutAgentThread, err error) string {
	lines := []string{
		"Scout work thread",
		"",
		"Vision: " + compactAssistantLine(thread.Query),
		"Status: needs attention",
		"Thread mode: " + assistantToolLabel(thread.Mode),
		"",
		"Execution log",
		"- Scout created the artifact and attempted the server-side worker.",
		"- Worker error: " + strings.TrimSpace(err.Error()),
		"",
		"Next action: reconnect the worker or run the Codex/MCP handoff from this artifact.",
	}
	return strings.Join(appendGoalWorkflow(lines, thread.Mode, thread.Query, err.Error(), agentThreadDeliverable(thread.Mode), "worker error recorded on artifact"), "\n")
}

func agentThreadInstructions(mode string) string {
	return strings.Join([]string{
		"You are Scout's server-side work-thread writer for Bonfire OS.",
		"Create the artifact requested by the user while preserving the structured goal workflow.",
		"Start with a one-line Vision, then provide concise Markdown sections for Goal, Context used, Work decomposition, Agent assignment, Dependency coordination, Ordered execution, Review against the original goal, Gate, What worked, Report, Next moves, and Verification.",
		"Use stable headings and short paragraphs or bullets so the artifact viewer can turn the output into a readable brief.",
		agentThreadModeContract(mode),
		"Do not claim you performed browser, SSH, repository, or external Codex work unless the input explicitly includes that evidence.",
		"Write in a practical operator voice. Keep it useful as a saved artifact, not a chat reply.",
		"Mode: " + assistantToolLabel(mode) + ".",
	}, "\n")
}

func buildAgentThreadInput(thread scoutAgentThread, board kanbanBoardState, memory []meetingMemoryEntry, now time.Time) string {
	var builder strings.Builder
	builder.WriteString("Now: ")
	builder.WriteString(now.Format(time.RFC3339))
	builder.WriteString("\nThread id: ")
	builder.WriteString(thread.ID)
	builder.WriteString("\nMode: ")
	builder.WriteString(thread.Mode)
	builder.WriteString("\nUser request: ")
	builder.WriteString(thread.Query)
	builder.WriteString("\n\nBoard and memory context: ")
	builder.WriteString(boardAndMemoryContextLine(board, memory))
	builder.WriteString("\n\nRecent durable memory:\n")
	for _, entry := range memory {
		builder.WriteString("- ")
		builder.WriteString(entry.Kind)
		if title := strings.TrimSpace(entry.Metadata["title"]); title != "" {
			builder.WriteString(" / ")
			builder.WriteString(title)
		}
		builder.WriteString(": ")
		builder.WriteString(compactAssistantLine(entry.Text))
		builder.WriteByte('\n')
	}
	return builder.String()
}

func agentThreadDeliverable(mode string) string {
	switch normalizeAgentThreadMode(mode) {
	case "research":
		return "research brief with thesis, source trail, evidence table, counterarguments, recommendation, and next checks"
	case "design":
		return "design brief with intent, context links, screens, interaction states, responsive plan, handoff notes, and build risks"
	case "grill":
		return "pressure-test scorecard with objections, hard questions, and improved ask"
	case "workflow":
		return "goal-tracked multi-agent workflow artifact with review and shipping gates"
	default:
		return "durable operating artifact with workflow, evidence, and verification notes"
	}
}

func agentThreadModeContract(mode string) string {
	switch normalizeAgentThreadMode(mode) {
	case "research":
		return "For research mode, use these exact readable headings when evidence exists: Executive Summary, Thesis, Evidence, Sources, Counterarguments, Recommendation, Open questions, Next checks, and Worker evidence. Add a short Search tags line near the top. Cite only sources or tool evidence actually used."
	case "design":
		return "For design mode, include these readable sections: Design intent, Context and research used, Core screens, Interaction states, Responsive behavior, Implementation handoff, Risks, and Next checks. If a relevant research brief appears in memory, explicitly say how it shaped the design."
	case "grill":
		return "For grill mode, include Score, Strongest objections, Tough questions, Revised ask, and Confidence gate."
	case "workflow":
		return "For workflow mode, keep the ten-step goal loop explicit and make the gate status unambiguous."
	default:
		return "For artifact mode, name the decision, evidence, risks, owner, and next move."
	}
}

func agentThreadModeMetadata(mode string) map[string]string {
	switch normalizeAgentThreadMode(mode) {
	case "research":
		return map[string]string{
			"artifactContract": "research_brief_v2",
			"artifactHeadings": "executive summary thesis evidence sources counterarguments recommendation open questions next checks worker evidence",
			"searchTags":       "required",
		}
	default:
		return nil
	}
}
