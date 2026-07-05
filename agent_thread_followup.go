package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Conversational agent threads: an in-thread reply re-runs the SAME artifact
// in place (stable artifact id, threadVersion bumped, prior body archived
// under a canonical "## Previous run" heading). Follow-ups ALWAYS ride the
// bounded OpenAI text worker — one Responses call, 60s, no codex queue or
// sandbox — even when BONFIRE_CODEX_AGENT_THREADS is set. That is the cost
// envelope: a follow-up can never fan out.
const (
	// agentThreadFollowUpMaxPriorBody caps the latest-version body fed back
	// into the follow-up prompt (bytes, rune-boundary safe).
	agentThreadFollowUpMaxPriorBody = 8000
	// agentThreadFollowUpMaxReplies caps team messages since the last run.
	agentThreadFollowUpMaxReplies = 30
	// agentThreadFollowUpMaxReplyLen caps each reply line fed to the worker.
	agentThreadFollowUpMaxReplyLen = 700
	// agentThreadMaxArchivedRuns bounds artifact growth: older Previous-run
	// sections beyond this are dropped at merge time.
	agentThreadMaxArchivedRuns = 4
	// agentThreadRunLogCap bounds the threadRuns trajectory metadata.
	agentThreadRunLogCap = 12
)

// agentThreadPrevRunHeading is the canonical version boundary. Worker output
// is sanitized against it before every merge so a forged marker can never
// corrupt future splits.
var agentThreadPrevRunHeading = regexp.MustCompile(`(?m)^## Previous run · v(\d+) · .*$`)

// readinessLinePattern parses the machine-readable grill contract line
// ("READINESS: 6.5/10"). Tolerant: case-insensitive, optional spaces, first
// match anywhere in the document wins.
var readinessLinePattern = regexp.MustCompile(`(?mi)^\s*READINESS:\s*([0-9]+(?:\.[0-9]+)?)\s*/\s*10\b`)

// agentThreadFollowUpStatusKeys are snapshotted at launch (inside the
// per-artifact lock) and written back verbatim when the follow-up run fails,
// so an error never clobbers a good artifact's terminal state or version.
var agentThreadFollowUpStatusKeys = []string{"status", "threadStatus", "goalStatus", "currentStage", "progressPercent", "reviewGate", "threadVersion", "latestThreadRun"}

// agentThreadFollowUpRun carries one armed follow-up from launch to the async
// worker, including the pre-run metadata snapshots the error path restores.
type agentThreadFollowUpRun struct {
	thread      scoutAgentThread
	artifactID  string
	runID       string
	version     int
	requestedBy string
	input       string
	prevMeta    map[string]string
	prevStatus  map[string]string
}

// startAgentThreadFollowUpAsync is the test seam mirroring
// startAgentThreadAsync (agent_thread_runner.go).
var startAgentThreadFollowUpAsync = func(app *kanbanBoardApp, run agentThreadFollowUpRun) {
	go app.runAgentThreadFollowUp(run)
}

// agentThreadRunLock returns the per-artifact mutex serializing follow-up
// validate+mark-running (mirrors scoutChatThreadLock). The model call stays
// outside the lock.
func (app *kanbanBoardApp) agentThreadRunLock(artifactID string) *sync.Mutex {
	app.mu.Lock()
	defer app.mu.Unlock()

	if app.agentThreadRunLocks == nil {
		app.agentThreadRunLocks = map[string]*sync.Mutex{}
	}
	lock, ok := app.agentThreadRunLocks[artifactID]
	if !ok {
		lock = &sync.Mutex{}
		app.agentThreadRunLocks[artifactID] = lock
	}
	return lock
}

// agentThreadStatusValue reads the same status keys the client's
// artifactStatusValue reads: threadStatus first, then status.
func agentThreadStatusValue(artifact meetingMemoryEntry) string {
	return strings.ToLower(strings.TrimSpace(firstNonEmptyString(artifact.Metadata["threadStatus"], artifact.Metadata["status"])))
}

// launchAgentThreadFollowUp validates and marks an existing agent-thread
// artifact as running a new in-place version, then hands the bounded text
// run to the async worker. Any signed-in user may follow up; the run itself
// is server-side.
func (app *kanbanBoardApp) launchAgentThreadFollowUp(artifactID string, replyText string, requestedBy string, teamReplies []scoutChatMessageRecord) (scoutAgentThread, error) {
	if app == nil || app.memory == nil {
		return scoutAgentThread{}, fmt.Errorf("assistant is unavailable")
	}
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return scoutAgentThread{}, fmt.Errorf("artifact id is required")
	}

	lock := app.agentThreadRunLock(artifactID)
	lock.Lock()
	defer lock.Unlock()

	artifact, ok := app.osArtifactByID(artifactID)
	if !ok {
		return scoutAgentThread{}, fmt.Errorf("that report is unavailable")
	}
	if artifact.Metadata["source"] != "scout_thread" {
		return scoutAgentThread{}, fmt.Errorf("follow-ups only run on agent thread reports")
	}
	mode := normalizeAgentThreadMode(firstNonEmptyString(artifact.Metadata["mode"], artifact.Kind))
	if mode == "" {
		// Same fallback as the rerun action (codex_runner_queue.go).
		mode = "workflow"
	}
	switch agentThreadStatusValue(artifact) {
	case "complete", "published", "error", "failed":
	default:
		return scoutAgentThread{}, fmt.Errorf("thread is still running — wait for it to finish")
	}

	version := 1
	if parsed, err := strconv.Atoi(strings.TrimSpace(artifact.Metadata["threadVersion"])); err == nil && parsed > 0 {
		version = parsed
	}
	nextVersion := version + 1
	runID := fmt.Sprintf("agent-thread-%s-followup-%d", mode, time.Now().UnixNano())
	// The ORIGINAL threadId keeps ref rewrites flipping the existing chat
	// cards; the fresh runID only records this run.
	threadID := firstNonEmptyString(strings.TrimSpace(artifact.Metadata["threadId"]), runID)

	prevMeta := make(map[string]string, len(artifact.Metadata))
	for key, value := range artifact.Metadata {
		prevMeta[key] = value
	}
	prevStatus := make(map[string]string, len(agentThreadFollowUpStatusKeys))
	for _, key := range agentThreadFollowUpStatusKeys {
		prevStatus[key] = artifact.Metadata[key]
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	requestedByName := firstNonEmptyString(canonicalRoomActorName(requestedBy), strings.TrimSpace(requestedBy))
	// Mark running WITHOUT touching text or title: a failed follow-up must be
	// able to restore the prior good state untouched.
	updated, _, err := app.updateOSArtifactWithMetadata(artifact.ID, "", artifact.Text, scoutParticipantName, map[string]string{
		"status":            "running",
		"threadStatus":      "running",
		"goalStatus":        "running",
		"currentStage":      "execute_in_order",
		"progressPercent":   "35",
		"reviewGate":        "pending",
		"latestThreadRun":   runID,
		"threadVersion":     strconv.Itoa(nextVersion),
		"followUpBy":        requestedByName,
		"followUpStartedAt": now,
		"followUpError":     "",
	})
	if err != nil {
		return scoutAgentThread{}, err
	}

	query := firstNonEmptyString(artifact.Metadata["threadQuery"], artifact.Metadata["title"])
	actions := app.osAssistantActions(query, mode, updated)
	thread := scoutAgentThread{
		ID:       threadID,
		Mode:     mode,
		Query:    query,
		Status:   "running",
		Artifact: updated,
		Actions:  actions,
	}
	input := buildAgentThreadFollowUpInput(thread, artifact, nextVersion, replyText, teamReplies, app.snapshotState(), app.memorySnapshotForClients(12), time.Now())

	// Signal capture (signals.go): asking for a re-run means v(N) missed — a
	// negative signal whose payload carries WHAT was asked for. Log-and-continue.
	app.recordSignalEvent(requestedByName, signalEventArtifactRerun, signalValenceNegative, artifact.ID, artifact.Metadata["packageId"], map[string]string{
		"instruction": truncateAgentThreadText(replyText, 500),
	})

	broadcastSignedInKanbanEvent("memory", app.memorySnapshotForClients(20))
	broadcastAssistantEvent("action", assistantToolLabel(mode)+" follow-up running", map[string]any{
		"tool":       "launch_agent_thread",
		"thread":     thread,
		"artifact":   updated,
		"actions":    actions,
		"voiceState": "listening",
	})
	app.updateScoutChatThreadRefs(thread.ID, "running", updated.ID)

	startAgentThreadFollowUpAsync(app, agentThreadFollowUpRun{
		thread:      thread,
		artifactID:  artifact.ID,
		runID:       runID,
		version:     nextVersion,
		requestedBy: requestedByName,
		input:       input,
		prevMeta:    prevMeta,
		prevStatus:  prevStatus,
	})
	return thread, nil
}

func (app *kanbanBoardApp) runAgentThreadFollowUp(run agentThreadFollowUpRun) {
	app.runAgentThreadFollowUpWithResponder(run, createOpenAITextResponse)
}

func (app *kanbanBoardApp) runAgentThreadFollowUpWithResponder(run agentThreadFollowUpRun, responder openAITextResponder) {
	// Always the default text-worker timeout — never codexExecConfigFromEnv():
	// follow-ups do not consult the configured agent-thread worker mode.
	ctx, cancel := context.WithTimeout(context.Background(), defaultAgentThreadRequestTimeout)
	defer cancel()

	if responder == nil {
		responder = createOpenAITextResponse
	}
	output, err := func() (string, error) {
		apiKey := app.currentOpenAIAPIKey()
		if strings.TrimSpace(apiKey) == "" {
			return "", fmt.Errorf("OPENAI_API_KEY is not configured")
		}
		raw, responderErr := responder(ctx, apiKey, openAITextRequest{
			Model:           meetingBrainModel(),
			Instructions:    agentThreadFollowUpInstructions(run.thread.Mode, run.version),
			Input:           run.input,
			ReasoningEffort: "low",
			Verbosity:       "medium",
			MaxOutputTokens: 2600,
		})
		if responderErr != nil {
			return "", responderErr
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return "", fmt.Errorf("Scout follow-up produced no artifact text")
		}
		return raw, nil
	}()

	// Re-read: the title (or text, via manual edits) may have changed while
	// the worker ran; the merge and restore both build on the stored state.
	prev, ok := app.osArtifactByID(run.artifactID)
	if !ok {
		log.Errorf("Follow-up artifact %s disappeared mid-run", run.artifactID)
		return
	}

	if err != nil {
		// Failed follow-ups never clobber a good body: metadata-only restore
		// of the pre-run terminal state plus the error stamp.
		metadata := make(map[string]string, len(run.prevStatus)+1)
		for key, value := range run.prevStatus {
			metadata[key] = value
		}
		metadata["followUpError"] = err.Error()
		artifact, _, updateErr := app.updateOSArtifactWithMetadata(run.artifactID, "", prev.Text, scoutParticipantName, metadata)
		if updateErr != nil {
			log.Errorf("Failed to restore follow-up artifact %s: %v", run.artifactID, updateErr)
			return
		}
		message := assistantToolLabel(run.thread.Mode) + " follow-up needs attention"
		actions := app.osAssistantActions(run.thread.Query, run.thread.Mode, artifact)
		prevRefStatus := firstNonEmptyString(run.prevStatus["threadStatus"], run.prevStatus["status"], "complete")
		broadcastSignedInKanbanEvent("memory", app.memorySnapshotForClients(20))
		broadcastAssistantEvent("action", message, map[string]any{
			"tool":       "launch_agent_thread",
			"thread":     scoutAgentThread{ID: run.thread.ID, Mode: run.thread.Mode, Query: run.thread.Query, Status: prevRefStatus, Artifact: artifact, Actions: actions},
			"artifact":   artifact,
			"actions":    actions,
			"voiceState": "listening",
		})
		app.updateScoutChatThreadRefs(run.thread.ID, prevRefStatus, artifact.ID)
		app.notifyAgentThreadFollowUp(artifact, message)
		return
	}

	prevStampedAt := firstNonEmptyString(run.prevMeta["completedAt"], run.prevMeta["updatedAt"])
	newText := mergeAgentThreadVersions(prev.Text, output, run.version-1, prevStampedAt)
	metadata := map[string]string{
		"status":          "complete",
		"threadStatus":    "complete",
		"goalStatus":      "verified",
		"currentStage":    "verify_goal_completed",
		"progressPercent": "100",
		"reviewGate":      "passed",
		"completedAt":     time.Now().UTC().Format(time.RFC3339Nano),
		"latestThreadRun": run.runID,
		"worker":          "openai_text_response",
		"workerBoundary":  "responses_artifact_writer",
		"followUpError":   "",
	}
	stampReadinessMetadata(prev, run.thread.Mode, output, metadata)
	appendThreadRunLog(prev, metadata, run.runID, run.version, run.requestedBy)

	artifact, _, updateErr := app.updateOSArtifactWithMetadata(run.artifactID, "", newText, scoutParticipantName, metadata)
	if updateErr != nil {
		log.Errorf("Failed to update follow-up artifact %s: %v", run.artifactID, updateErr)
		broadcastAssistantEvent("error", "Scout follow-up could not update its artifact", map[string]any{
			"tool":     "launch_agent_thread",
			"threadId": run.thread.ID,
			"artifact": prev,
		})
		return
	}

	message := assistantToolLabel(run.thread.Mode) + " follow-up complete"
	actions := app.osAssistantActions(run.thread.Query, run.thread.Mode, artifact)
	broadcastSignedInKanbanEvent("memory", app.memorySnapshotForClients(20))
	broadcastAssistantEvent("action", message, map[string]any{
		"tool":       "launch_agent_thread",
		"thread":     scoutAgentThread{ID: run.thread.ID, Mode: run.thread.Mode, Query: run.thread.Query, Status: "complete", Artifact: artifact, Actions: actions},
		"artifact":   artifact,
		"actions":    actions,
		"voiceState": "listening",
	})
	app.updateScoutChatThreadRefs(run.thread.ID, "complete", artifact.ID)
	// Same terminal contract as the primary seam (runAgentThread): the board
	// card advances (idempotent — moveTicket reports changed=false when the
	// card is already Done) and the completion card closes the loop back to
	// the origin surface (idempotent via the deliveredAt stamp, so artifacts
	// whose v1 already delivered are naturally skipped). This matters most on
	// the error→follow-up-success path, where v1 left the card Blocked and
	// never delivered.
	app.syncLinkedCardForArtifact(artifact, "complete")
	app.deliverArtifactToOrigin(artifact, run.thread.ID)
	app.notifyAgentThreadFollowUp(artifact, message)
}

// notifyAgentThreadFollowUp notifies the artifact creator (as every terminal
// seam does) AND, when a different teammate asked for the follow-up, that
// requester too. Grill completions carry the readiness dial in the text.
func (app *kanbanBoardApp) notifyAgentThreadFollowUp(artifact meetingMemoryEntry, message string) {
	text := agentThreadNotificationText(message, artifact) + readinessNotificationSuffix(artifact.Metadata)
	app.notifyAgentThreadCreator(artifact, notificationKindAgent, text)
	followUpEmail := normalizeAccountEmail(participantEmail(artifact.Metadata["followUpBy"]))
	creatorEmail := normalizeAccountEmail(participantEmail(artifact.Metadata["createdBy"]))
	if followUpEmail == "" || followUpEmail == creatorEmail {
		return
	}
	if _, err := app.createNotification(followUpEmail, notificationKindAgent, text, "", artifact.ID, "", false); err != nil {
		log.Errorf("Failed to create follow-up notification: %v", err)
	}
}

// readinessNotificationSuffix renders the re-grill dial for notification text
// when a delta exists: " (readiness 6.2 → 7.1)".
func readinessNotificationSuffix(metadata map[string]string) string {
	previous := strings.TrimSpace(metadata["readinessPrevScore"])
	next := strings.TrimSpace(metadata["readinessScore"])
	if previous == "" || next == "" {
		return ""
	}
	return fmt.Sprintf(" (readiness %s → %s)", previous, next)
}

// agentThreadFollowUpInstructions extends the base mode instructions with the
// tighter follow-up deliverable contract.
func agentThreadFollowUpInstructions(mode string, version int) string {
	lines := []string{
		agentThreadInstructions(mode),
		fmt.Sprintf("This is follow-up run v%d revising an existing artifact. Rewrite the FULL deliverable, not a diff. Keep prior sections that still hold; update or delete what the team's replies changed.", version),
		fmt.Sprintf("Add a 'What changed in v%d' section immediately after the Vision line (for grill mode: immediately after the READINESS line) listing the specific changes and which team reply drove each.", version),
		"Do not reproduce the 'Previous run' archive sections — output only the new version.",
	}
	if normalizeAgentThreadMode(mode) == "grill" {
		lines = append(lines, "Re-score honestly: the READINESS line must reflect the answers actually given, not effort. If an objection was resolved, name it; if dodged, keep the score flat and say why.")
	}
	return strings.Join(lines, "\n")
}

// buildAgentThreadFollowUpInput mirrors buildAgentThreadInput and adds the
// prior body, the team replies that landed since the last run, and the
// explicit follow-up request. Memory context is smaller (12) than the initial
// run's — the artifact body is the primary context now.
func buildAgentThreadFollowUpInput(thread scoutAgentThread, artifact meetingMemoryEntry, version int, replyText string, teamReplies []scoutChatMessageRecord, board kanbanBoardState, memory []meetingMemoryEntry, now time.Time) string {
	latest, _ := splitAgentThreadVersions(artifact.Text)
	var builder strings.Builder
	builder.WriteString("Now: ")
	builder.WriteString(now.Format(time.RFC3339))
	builder.WriteString("\nThread id: ")
	builder.WriteString(thread.ID)
	builder.WriteString("\nMode: ")
	builder.WriteString(thread.Mode)
	builder.WriteString(fmt.Sprintf("\nRun: follow-up v%d", version))
	builder.WriteString("\nUser request: ")
	builder.WriteString(thread.Query)
	builder.WriteString(fmt.Sprintf("\n\nPrior artifact (v%d) body:\n", version-1))
	builder.WriteString(truncateAgentThreadText(latest, agentThreadFollowUpMaxPriorBody))
	builder.WriteString("\n\nTeam replies since the last run (chronological):\n")
	replies := teamReplies
	if len(replies) > agentThreadFollowUpMaxReplies {
		replies = replies[len(replies)-agentThreadFollowUpMaxReplies:]
	}
	if len(replies) == 0 {
		builder.WriteString("- (none)\n")
	}
	for _, message := range replies {
		line := truncateAgentThreadText(compactAssistantLine(scoutChatMessageModelText(message)), agentThreadFollowUpMaxReplyLen)
		builder.WriteString(fmt.Sprintf("- [%s · %s] %s\n", firstNonEmptyString(strings.TrimSpace(message.AuthorName), "teammate"), scoutChatReplyClock(message.CreatedAt), line))
	}
	builder.WriteString("\nFollow-up request: ")
	builder.WriteString(strings.TrimSpace(replyText))
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

func scoutChatReplyClock(createdAt string) string {
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(createdAt)); err == nil {
		return parsed.UTC().Format("15:04")
	}
	return "earlier"
}

// truncateAgentThreadText truncates on a byte budget while backing off to a
// rune boundary (same idiom as sanitizeScoutChatFiles).
func truncateAgentThreadText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) && len(value) > 0 {
		value = value[:len(value)-1]
	}
	return strings.TrimSpace(value)
}

// splitAgentThreadVersions splits an artifact body at the first Previous-run
// heading: latest = the live version, archive = every archived section.
func splitAgentThreadVersions(text string) (string, string) {
	loc := agentThreadPrevRunHeading.FindStringIndex(text)
	if loc == nil {
		return strings.TrimSpace(text), ""
	}
	latest := strings.TrimSpace(text[:loc[0]])
	// Drop the trailing horizontal rule that separates versions.
	latest = strings.TrimSpace(strings.TrimSuffix(latest, "---"))
	return latest, strings.TrimSpace(text[loc[0]:])
}

// stripAgentThreadRunMarkers removes any line in worker output matching the
// version-boundary heading so a forged marker cannot break future splits.
func stripAgentThreadRunMarkers(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if agentThreadPrevRunHeading.MatchString(line) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// mergeAgentThreadVersions assembles the new in-place version: sanitized new
// output on top, prior latest body archived under the canonical heading, and
// the existing archive trimmed to agentThreadMaxArchivedRuns sections.
func mergeAgentThreadVersions(priorText string, newOutput string, prevVersion int, prevStampedAt string) string {
	sanitized := stripAgentThreadRunMarkers(newOutput)
	latest, archive := splitAgentThreadVersions(priorText)
	if prevVersion < 1 {
		prevVersion = 1
	}
	heading := fmt.Sprintf("## Previous run · v%d · %s", prevVersion, firstNonEmptyString(strings.TrimSpace(prevStampedAt), "unknown time"))
	archiveBlock := heading + "\n\n" + latest
	if archive != "" {
		archiveBlock += "\n\n" + archive
	}
	archiveBlock = capAgentThreadArchive(archiveBlock, agentThreadMaxArchivedRuns)
	return sanitized + "\n\n---\n\n" + archiveBlock
}

func capAgentThreadArchive(archive string, maxSections int) string {
	locs := agentThreadPrevRunHeading.FindAllStringIndex(archive, -1)
	if maxSections <= 0 || len(locs) <= maxSections {
		return archive
	}
	return strings.TrimSpace(archive[:locs[maxSections][0]])
}

// parseReadinessScore returns the first READINESS line's score, clamped to
// [0,10] and rounded to one decimal.
func parseReadinessScore(text string) (float64, bool) {
	match := readinessLinePattern.FindStringSubmatch(text)
	if len(match) < 2 {
		return 0, false
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, false
	}
	if value < 0 {
		value = 0
	}
	if value > 10 {
		value = 10
	}
	return math.Round(value*10) / 10, true
}

func formatReadiness(value float64) string {
	return strconv.FormatFloat(value, 'f', 1, 64)
}

// stampReadinessMetadata applies the grill READINESS contract at every
// artifact-finalizing seam. Fail-soft: a missing/reformatted line leaves the
// prior score untouched (stale beats wrong) and flags readinessParse=missing.
func stampReadinessMetadata(prev meetingMemoryEntry, mode string, output string, metadata map[string]string) {
	if normalizeAgentThreadMode(firstNonEmptyString(mode, prev.Metadata["mode"])) != "grill" {
		return
	}
	score, ok := parseReadinessScore(output)
	if !ok {
		metadata["readinessParse"] = "missing"
		return
	}
	metadata["readinessParse"] = ""
	metadata["readinessScore"] = formatReadiness(score)
	prevScore := strings.TrimSpace(prev.Metadata["readinessScore"])
	if prevScore == "" {
		return
	}
	metadata["readinessPrevScore"] = prevScore
	if prevValue, err := strconv.ParseFloat(prevScore, 64); err == nil {
		metadata["readinessDelta"] = fmt.Sprintf("%+.1f", score-prevValue)
	}
}

// agentThreadRunLogEntry is one row of the compact threadRuns trajectory the
// package binder charts (score omitted for non-grill runs).
type agentThreadRunLogEntry struct {
	Version int    `json:"v"`
	At      string `json:"at"`
	Run     string `json:"run,omitempty"`
	Score   string `json:"score,omitempty"`
	By      string `json:"by,omitempty"`
}

// appendThreadRunLog appends this run to metadata["threadRuns"], backfilling
// the prior run for artifacts written before the log existed, capped at
// agentThreadRunLogCap. Decode failures start the log fresh.
func appendThreadRunLog(prev meetingMemoryEntry, metadata map[string]string, runID string, version int, by string) {
	var runs []agentThreadRunLogEntry
	if raw := strings.TrimSpace(prev.Metadata["threadRuns"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &runs); err != nil {
			runs = nil
		}
	}
	if len(runs) == 0 && version > 1 {
		runs = append(runs, agentThreadRunLogEntry{
			Version: version - 1,
			At:      firstNonEmptyString(prev.Metadata["completedAt"], prev.Metadata["updatedAt"]),
			Run:     firstNonEmptyString(prev.Metadata["latestThreadRun"], prev.Metadata["threadId"]),
			Score:   strings.TrimSpace(prev.Metadata["readinessScore"]),
			By:      strings.TrimSpace(prev.Metadata["createdBy"]),
		})
	}
	runs = append(runs, agentThreadRunLogEntry{
		Version: version,
		At:      time.Now().UTC().Format(time.RFC3339Nano),
		Run:     runID,
		Score:   strings.TrimSpace(metadata["readinessScore"]),
		By:      strings.TrimSpace(by),
	})
	if len(runs) > agentThreadRunLogCap {
		runs = runs[len(runs)-agentThreadRunLogCap:]
	}
	if raw, err := json.Marshal(runs); err == nil {
		metadata["threadRuns"] = string(raw)
	}
}
