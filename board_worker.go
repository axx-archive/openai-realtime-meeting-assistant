package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	meetingBoardAgentName           = "meeting board"
	defaultMeetingBoardInterval     = 2 * time.Minute
	defaultMeetingBoardMinSummaries = 1
	defaultMeetingBoardMaxSummaries = 6
	meetingBoardRequestTimeout      = 90 * time.Second
	maxMeetingBoardOperations       = 8
)

type meetingBoardAnalysis struct {
	Summary    string                  `json:"summary"`
	Operations []meetingBoardOperation `json:"operations"`
}

type meetingBoardOperation struct {
	Tool      string         `json:"tool,omitempty"`
	Name      string         `json:"name,omitempty"`
	ToolName  string         `json:"tool_name,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Args      map[string]any `json:"args,omitempty"`
}

type meetingBoardOperationApplication struct {
	Tool    string         `json:"tool"`
	Reason  string         `json:"reason,omitempty"`
	Changed bool           `json:"changed"`
	Error   string         `json:"error,omitempty"`
	Result  map[string]any `json:"result,omitempty"`
}

type meetingBoardRunResult struct {
	Summary          string
	Applications     []meetingBoardOperationApplication
	ChangedCount     int
	ErrorCount       int
	SkippedOperation int
}

func meetingBoardAgent() ambientAgentConfig {
	return ambientAgentConfig{
		name:              meetingBoardAgentName,
		defaultInterval:   defaultMeetingBoardInterval,
		intervalEnv:       "MEETING_BOARD_INTERVAL",
		disabledEnv:       "MEETING_BOARD_DISABLED",
		backfillEnv:       "MEETING_BOARD_BACKFILL",
		minBatchEnv:       "MEETING_BOARD_MIN_SUMMARIES",
		defaultMinBatch:   defaultMeetingBoardMinSummaries,
		maxBatchEnv:       "MEETING_BOARD_MAX_SUMMARIES",
		defaultMaxBatch:   defaultMeetingBoardMaxSummaries,
		inputKind:         meetingMemoryKindBrain,
		artifactKind:      meetingMemoryKindBoardUpdate,
		cursorMetadataKey: "throughBrainId",
		requestTimeout:    meetingBoardRequestTimeout,
		produce:           (*kanbanBoardApp).produceMeetingBoardUpdate,
	}
}

func (app *kanbanBoardApp) startMeetingBoardWorker(apiKey string) {
	app.startAmbientAgent(meetingBoardAgent(), apiKey)
}

func (app *kanbanBoardApp) runMeetingBoardOnce(ctx context.Context, apiKey string, responder openAITextResponder) (meetingMemoryEntry, error) {
	agent := meetingBoardAgent()
	return app.runAmbientAgentOnce(agent, ctx, apiKey, responder, agent.minBatch())
}

func (app *kanbanBoardApp) produceMeetingBoardUpdate(ctx context.Context, apiKey string, summaries []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error) {
	model := meetingBoardModel()
	text, err := responder(ctx, apiKey, openAITextRequest{
		Model:           model,
		Instructions:    meetingBoardInstructions(),
		Input:           buildMeetingBoardInput(summaries, app.snapshotState(), app.participantSnapshot(), time.Now().UTC()),
		ReasoningEffort: "low",
		Verbosity:       "low",
		MaxOutputTokens: 1200,
	})
	if err != nil {
		return meetingMemoryEntry{}, err
	}

	analysis, err := parseMeetingBoardAnalysis(text)
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	runResult := app.applyMeetingBoardAnalysis(analysis)

	firstSummary := summaries[0]
	lastSummary := summaries[len(summaries)-1]
	metadata := map[string]string{
		"source":                "openai_responses",
		"model":                 model,
		"fromBrainId":           firstSummary.ID,
		"throughBrainId":        lastSummary.ID,
		"fromBrainCreatedAt":    firstSummary.CreatedAt.Format(time.RFC3339Nano),
		"throughBrainCreatedAt": lastSummary.CreatedAt.Format(time.RFC3339Nano),
		"brainCount":            strconv.Itoa(len(summaries)),
		"operationCount":        strconv.Itoa(len(runResult.Applications)),
		"changedOperationCount": strconv.Itoa(runResult.ChangedCount),
		"errorOperationCount":   strconv.Itoa(runResult.ErrorCount),
	}
	if fromTranscriptID := strings.TrimSpace(firstSummary.Metadata["fromTranscriptId"]); fromTranscriptID != "" {
		metadata["fromTranscriptId"] = fromTranscriptID
	}
	if throughTranscriptID := strings.TrimSpace(lastSummary.Metadata["throughTranscriptId"]); throughTranscriptID != "" {
		metadata["throughTranscriptId"] = throughTranscriptID
	}
	// Board linkage for memory: the ids of every card this pass actually
	// touched ride the entry, so the meeting → "on the board" chips in the
	// Memory tool stay grounded in real mutations (D15).
	if cardIDs := meetingBoardChangedCardIDs(runResult); len(cardIDs) > 0 {
		metadata["cardIds"] = strings.Join(cardIDs, ",")
	}

	id := durableTimestampID("board-update", time.Now())
	entry, appended, err := app.memory.appendBoardUpdate(id, renderMeetingBoardUpdateArtifact(summaries, runResult), metadata)
	if err != nil || !appended {
		return entry, err
	}

	broadcastKanbanEvent("memory_board_update", entry)
	// Office memory rails stay live via the snapshot path: the entry-shaped
	// memory_board_update event stays room-only because the client's
	// addMemoryEntry does not dedupe by id.
	broadcastOfficeKanbanEvent("memory", app.memorySnapshotForClients(20))
	if runResult.ChangedCount > 0 {
		broadcastSignedInKanbanEvent("board", app.snapshotState())
		broadcastSignedInKanbanEvent("undo_available", app.canUndoDelete())
		broadcastAssistantEvent("action", "Scout updated the board from meeting summaries.", map[string]any{"kind": meetingMemoryKindBoardUpdate, "changes": runResult.ChangedCount})
		app.refreshRealtimeBoardContext("meeting board worker")
	} else {
		broadcastAssistantEvent("action", "Scout reviewed meeting summaries; no board changes were needed.", map[string]any{"kind": meetingMemoryKindBoardUpdate})
	}

	return entry, nil
}

func (app *kanbanBoardApp) applyMeetingBoardAnalysis(analysis meetingBoardAnalysis) meetingBoardRunResult {
	result := meetingBoardRunResult{
		Summary: strings.TrimSpace(analysis.Summary),
	}

	operations := analysis.Operations
	if len(operations) > maxMeetingBoardOperations {
		result.SkippedOperation = len(operations) - maxMeetingBoardOperations
		operations = operations[:maxMeetingBoardOperations]
	}

	for _, operation := range operations {
		toolName := normalizeMeetingBoardToolName(operation)
		application := meetingBoardOperationApplication{
			Tool:   toolName,
			Reason: canonicalizeBoardText(operation.Reason),
		}
		if toolName == "" {
			application.Error = "operation tool is required"
			result.ErrorCount++
			result.Applications = append(result.Applications, application)
			continue
		}
		if !meetingBoardToolAllowed(toolName) {
			application.Error = fmt.Sprintf("unsupported board worker tool %q", toolName)
			result.ErrorCount++
			result.Applications = append(result.Applications, application)
			continue
		}

		args := operation.Arguments
		if args == nil {
			args = operation.Args
		}
		if args == nil {
			args = map[string]any{}
		}
		if toolName == "create_ticket" {
			// D4: worker-created cards land as pending Scout drafts a human
			// accepts or dismisses — never as instant board cards.
			args["draft"] = true
			// Board doctrine v2: business cards are captured, never cut
			// silently — the worker must supply a named owner and a concrete
			// next step, or the create is rejected into the per-operation
			// error rail of the board-update artifact.
			if err := validateMeetingBoardCreateDoctrine(args); err != nil {
				application.Error = err.Error()
				result.ErrorCount++
				result.Applications = append(result.Applications, application)
				continue
			}
			// The owner field only holds meeting participants, so a business
			// card's named non-participant owner would collapse to Unassigned
			// in createTicket; preserve it in the notes so the named-owner
			// guarantee survives on the card.
			retainNamedBusinessOwner(args)
		}

		toolResult, changed, err := app.applyToolCallArgs(toolName, args)
		application.Changed = changed
		application.Result = toolResult
		if err != nil {
			application.Error = err.Error()
			result.ErrorCount++
		}
		if changed {
			result.ChangedCount++
		}
		result.Applications = append(result.Applications, application)
	}

	return result
}

// validateMeetingBoardCreateDoctrine enforces the business-card rules of
// board doctrine v2 at the worker's create seam: a business card requires a
// named owner and notes stating the concrete next step, so accepting or
// dismissing the draft IS the debate. Owner is a presence check only — a
// business card owned by a non-participant name still counts as owned, so
// this deliberately does not require a canonical-participant match. Human
// creates through createTicket are untouched.
func validateMeetingBoardCreateDoctrine(args map[string]any) error {
	if !boardCreateHasBusinessTag(args) {
		return nil
	}
	if owner := asString(args["owner"]); owner == "" || strings.EqualFold(owner, "Unassigned") {
		return fmt.Errorf("business cards require a named owner — name who carries this before creating it")
	}
	if cleanBoardNotes(asString(args["notes"])) == "" {
		return fmt.Errorf("business cards require notes stating the concrete next step — spell it out before creating it")
	}
	return nil
}

// boardCreateHasBusinessTag reports whether a create_ticket op is tagged
// business. canonicalizeBoardTags does not lowercase, so match with EqualFold.
func boardCreateHasBusinessTag(args map[string]any) bool {
	for _, tag := range canonicalizeBoardTags(asStringSlice(args["tags"])) {
		if strings.EqualFold(tag, "business") {
			return true
		}
	}
	return false
}

// retainNamedBusinessOwner keeps a business card's named non-participant owner
// visible on the persisted card. The board's owner field only holds meeting
// participants, so createTicket collapses a non-participant owner — the common
// business case of a vendor, landlord, or prospective hire — to "Unassigned",
// which would silently drop the named owner the doctrine gate just required.
// When that collapse will happen, fold the raw owner into the notes so the
// named-owner guarantee survives on the card even though its owner chip reads
// Unassigned. Participant owners (held intact by the owner field) and
// non-business cards are left untouched.
func retainNamedBusinessOwner(args map[string]any) {
	if !boardCreateHasBusinessTag(args) {
		return
	}
	owner := asString(args["owner"])
	if owner == "" || normalizeCardOwner(owner) != "" {
		return
	}
	notes := asString(args["notes"])
	if strings.Contains(notes, "Owner: "+owner) {
		return
	}
	ownerNote := "Owner: " + owner + "."
	if notes == "" {
		args["notes"] = ownerNote
		return
	}
	args["notes"] = notes + " " + ownerNote
}

func normalizeMeetingBoardToolName(operation meetingBoardOperation) string {
	toolName := firstNonEmptyString(operation.Tool, operation.ToolName, operation.Name)
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "create_card":
		return "create_ticket"
	case "update_card":
		return "update_ticket"
	case "move_card":
		return "move_ticket"
	case "tag_card":
		return "add_tags"
	case "date_card":
		return "add_key_date"
	default:
		return strings.TrimSpace(toolName)
	}
}

func meetingBoardToolAllowed(toolName string) bool {
	switch toolName {
	case "create_ticket", "update_ticket", "move_ticket", "add_tags", "add_key_date", "propose_codex_task", "do_nothing":
		return true
	default:
		return false
	}
}

func meetingBoardModel() string {
	if model := strings.TrimSpace(os.Getenv("OPENAI_BOARD_MODEL")); model != "" {
		return model
	}

	return meetingBrainModel()
}

func meetingBoardInstructions() string {
	return strings.Join([]string{
		"You are Scout's asynchronous board intelligence worker for Bonfire.",
		"Analyze durable meeting brain summaries and keep the Kanban board accurate as a compounding company artifact.",
		"Use only the supplied summaries and current board. Do not invent clients, owners, decisions, dates, blockers, or status changes.",
		"Create a card only for a clear commitment, decision, reported bug, or scoped work item someone expects to happen. Open questions, musings, and topics the room is still exploring stay in the brain summaries — do not card them.",
		"Prefer updating an existing card when the work is already represented. Scan the whole board snapshot first, including pending drafts: facets of one conversation belong on one card, never on sibling cards.",
		"Create at most three new cards per pass. When the discussion suggests more, keep the clearest commitments and let the rest ride in the summaries until the room commits.",
		"Write notes as a work spec in at most three sentences: what to do, why, and what done looks like. Name a person only for ownership or a commitment. When updating a card, rewrite the notes into the current best spec — never append a running chronology of who said what.",
		"Tag every card you create with exactly one category tag — build, fix, workflow, or business — plus topical tags.",
		"The board has exactly four status columns and status must be one of them, spelled exactly: Backlog, In Progress, Blocked, Done. \"Draft\" is NOT a status — draft is a boolean flag applied automatically to every card you create, so never send Draft, To Do, or Todo as a status; new work belongs in Backlog.",
		"When updating, moving, or tagging an existing card, pass its card_id exactly as it appears in the board snapshot.",
		"The board exists for work that gets BUILT, FIXED, or run as a WORKFLOW (research, decks, design).",
		"Business cards are never cut silently: they require a named owner and notes stating the concrete next step, and they always stay drafts for human accept/dismiss — that review is the debate.",
		"Workflow cards must name their deliverable — the exact artifact title — in the notes so a finished agent thread binds to the card and advances it.",
		"Never delete cards from asynchronous summaries. Ignore broad discussion, uncertain audio, greetings, filler, and facts that do not affect the board.",
		"Preserve exact proper nouns, participant names, project titles, and dates. Use owner Unassigned unless a responsible participant is clear.",
		"Return strict JSON only, with shape: {\"summary\":\"short audit summary\",\"operations\":[{\"tool\":\"update_ticket\",\"reason\":\"why this is grounded\",\"arguments\":{...}}]}.",
		"Allowed tools are create_ticket, update_ticket, move_ticket, add_tags, add_key_date, propose_codex_task, and do_nothing. Tool arguments must match the existing board tool schemas.",
		"When an action item clearly maps to an agent deliverable — a research brief, a design brief, a pressure-test, a workflow plan, or a written artifact — also emit propose_codex_task with arguments {\"title\":\"short human title\",\"mode\":\"research|design|grill|workflow|artifacts\",\"query\":\"what the agent should produce\"}.",
		"When the deliverable matches a board card — including one you create earlier in this same pass — pass its card_id if known, otherwise reuse the card's exact title so the proposal binds to it.",
		"Proposals are never auto-run: a human reviews and confirms each one, so propose at most two per pass and only when the deliverable is unmistakable. A separate workflow ticker may later launch proposals a human has already approved, but your proposal itself launches nothing.",
		"Phrase every proposal query as a read-only deliverable to research, draft, or analyze. Never ask the agent to commit, deploy, push, ssh, run migrations, or modify external systems.",
		"When no board change is warranted, return {\"summary\":\"No actionable board changes.\",\"operations\":[]}.",
	}, " ")
}

func buildMeetingBoardInput(summaries []meetingMemoryEntry, board kanbanBoardState, participants []string, generatedAt time.Time) string {
	location := meetingTimeLocation()
	rawBoard, err := json.MarshalIndent(board.Cards, "", "  ")
	if err != nil {
		rawBoard = []byte("[]")
	}

	var builder strings.Builder
	builder.WriteString("# Generated at\n")
	builder.WriteString(generatedAt.In(location).Format(time.RFC1123))
	builder.WriteString("\n\n# Active participants\n")
	if len(participants) == 0 {
		builder.WriteString("Unknown\n")
	} else {
		builder.WriteString(strings.Join(participants, ", "))
		builder.WriteByte('\n')
	}

	builder.WriteString("\n# Domain vocabulary\n")
	builder.WriteString(strings.Join(domainVocabulary(), ", "))
	builder.WriteString("\n\n# Current board snapshot\n")
	builder.Write(rawBoard)
	builder.WriteString("\n\n# Meeting brain summaries to analyze\n")
	for _, entry := range summaries {
		builder.WriteString("- id=")
		builder.WriteString(entry.ID)
		builder.WriteString(" kind=")
		builder.WriteString(entry.Kind)
		builder.WriteString(" time=")
		builder.WriteString(entry.CreatedAt.In(location).Format(time.RFC3339))
		if fromTranscriptID := strings.TrimSpace(entry.Metadata["fromTranscriptId"]); fromTranscriptID != "" {
			builder.WriteString(" fromTranscriptId=")
			builder.WriteString(fromTranscriptID)
		}
		if throughTranscriptID := strings.TrimSpace(entry.Metadata["throughTranscriptId"]); throughTranscriptID != "" {
			builder.WriteString(" throughTranscriptId=")
			builder.WriteString(throughTranscriptID)
		}
		builder.WriteByte('\n')
		for _, line := range strings.Split(entry.Text, "\n") {
			builder.WriteString("  ")
			builder.WriteString(strings.TrimSpace(line))
			builder.WriteByte('\n')
		}
	}

	return builder.String()
}

func parseMeetingBoardAnalysis(text string) (meetingBoardAnalysis, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return meetingBoardAnalysis{}, fmt.Errorf("meeting board analysis was empty")
	}

	candidates := []string{text}
	if fenced := stripJSONCodeFence(text); fenced != text {
		candidates = append(candidates, fenced)
	}
	if object := extractJSONCandidate(text); object != "" && object != text {
		candidates = append(candidates, object)
	}

	var lastErr error
	for _, candidate := range candidates {
		if analysis, err := decodeMeetingBoardAnalysis(candidate); err == nil {
			return analysis, nil
		} else {
			lastErr = err
		}
	}

	return meetingBoardAnalysis{}, fmt.Errorf("parse meeting board analysis: %w", lastErr)
}

func decodeMeetingBoardAnalysis(text string) (meetingBoardAnalysis, error) {
	var analysis struct {
		Summary    string                  `json:"summary"`
		Operations []meetingBoardOperation `json:"operations"`
		Updates    []meetingBoardOperation `json:"updates"`
		Actions    []meetingBoardOperation `json:"actions"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &analysis); err == nil {
		operations := analysis.Operations
		if len(operations) == 0 {
			operations = analysis.Updates
		}
		if len(operations) == 0 {
			operations = analysis.Actions
		}
		return meetingBoardAnalysis{
			Summary:    analysis.Summary,
			Operations: operations,
		}, nil
	}

	var operations []meetingBoardOperation
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &operations); err != nil {
		return meetingBoardAnalysis{}, err
	}

	return meetingBoardAnalysis{Operations: operations}, nil
}

func stripJSONCodeFence(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "```") {
		return text
	}

	lines := strings.Split(text, "\n")
	if len(lines) < 2 {
		return text
	}
	lines = lines[1:]
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
		lines = lines[:len(lines)-1]
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func extractJSONCandidate(text string) string {
	text = strings.TrimSpace(text)
	objectStart := strings.Index(text, "{")
	arrayStart := strings.Index(text, "[")
	if objectStart == -1 && arrayStart == -1 {
		return ""
	}
	if objectStart != -1 && (arrayStart == -1 || objectStart < arrayStart) {
		if objectEnd := strings.LastIndex(text, "}"); objectEnd > objectStart {
			return strings.TrimSpace(text[objectStart : objectEnd+1])
		}
		return ""
	}
	if arrayEnd := strings.LastIndex(text, "]"); arrayEnd > arrayStart {
		return strings.TrimSpace(text[arrayStart : arrayEnd+1])
	}

	return ""
}

func renderMeetingBoardUpdateArtifact(summaries []meetingMemoryEntry, result meetingBoardRunResult) string {
	var builder strings.Builder
	builder.WriteString("## Summary\n")
	if result.Summary != "" {
		builder.WriteString(result.Summary)
	} else if result.ChangedCount > 0 {
		builder.WriteString("Applied board updates from meeting summaries.")
	} else {
		builder.WriteString("No actionable board changes were found in the meeting summaries.")
	}
	builder.WriteString("\n\n## Source summaries\n")
	for _, summary := range summaries {
		builder.WriteString("- ")
		builder.WriteString(summary.ID)
		if throughTranscriptID := strings.TrimSpace(summary.Metadata["throughTranscriptId"]); throughTranscriptID != "" {
			builder.WriteString(" through ")
			builder.WriteString(throughTranscriptID)
		}
		builder.WriteByte('\n')
	}

	builder.WriteString("\n## Board operations\n")
	if len(result.Applications) == 0 {
		builder.WriteString("- No board operations needed.\n")
	} else {
		for _, application := range result.Applications {
			builder.WriteString("- ")
			builder.WriteString(application.Tool)
			if application.Changed {
				builder.WriteString(" changed=true")
			} else {
				builder.WriteString(" changed=false")
			}
			if target := meetingBoardApplicationTarget(application); target != "" {
				builder.WriteString(" target=")
				builder.WriteString(target)
			}
			if application.Reason != "" {
				builder.WriteString(" reason=")
				builder.WriteString(application.Reason)
			}
			if application.Error != "" {
				builder.WriteString(" error=")
				builder.WriteString(application.Error)
			}
			builder.WriteByte('\n')
		}
	}
	if result.SkippedOperation > 0 {
		builder.WriteString(fmt.Sprintf("- Skipped %d operation(s) because the worker operation cap is %d.\n", result.SkippedOperation, maxMeetingBoardOperations))
	}

	return strings.TrimSpace(builder.String())
}

// meetingBoardChangedCardIDs collects the unique card ids of the operations
// that actually changed the board this pass, in application order.
func meetingBoardChangedCardIDs(result meetingBoardRunResult) []string {
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(result.Applications))
	for _, application := range result.Applications {
		if !application.Changed {
			continue
		}
		cardID := strings.TrimSpace(meetingBoardApplicationTarget(application))
		if cardID == "" {
			continue
		}
		if _, ok := seen[cardID]; ok {
			continue
		}
		seen[cardID] = struct{}{}
		ids = append(ids, cardID)
	}
	return ids
}

func meetingBoardApplicationTarget(application meetingBoardOperationApplication) string {
	if application.Result == nil {
		return ""
	}
	if cardID := asString(application.Result["card_id"]); cardID != "" {
		return cardID
	}
	switch card := application.Result["card"].(type) {
	case kanbanCard:
		return card.ID
	case map[string]any:
		return asString(card["id"])
	default:
		return ""
	}
}

func (store *meetingMemoryStore) unprocessedBrainWriteUpsAfter(limit int, baselineBrainID string) []meetingMemoryEntry {
	return store.unconsumedEntriesAfter(meetingMemoryKindBrain, meetingMemoryKindBoardUpdate, "throughBrainId", limit, baselineBrainID)
}

func (store *meetingMemoryStore) latestBrainWriteUpID() string {
	return store.latestEntryIDOfKind(meetingMemoryKindBrain)
}
