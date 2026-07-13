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
		roomScoped:        true, // W4 §7.4: per-room brain windows and cursors
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
	roomID := ambientWindowRoomID(summaries)
	windowLast := summaries[len(summaries)-1]
	// §7.3 layer 1 (the primary suppression): brains from listen-only sittings
	// are EXCLUDED from the analysis window while the cursor still advances
	// past them. When the whole window is listen-only there is nothing to
	// analyze — advance the in-memory baseline (the suggestion agent's
	// skip-while-advancing idiom; deterministic on restart) and stay silent:
	// no model call, no artifact, no board ops, no proposals.
	summaries, droppedListenOnly := app.filterListenOnly(summaries)
	servicePrincipal := sharedRoomRecallPrincipal(roomID, app.memory.currentMeetingID(roomID))
	authorizedSummaries := summaries[:0]
	for _, summary := range summaries {
		if recallEntryScopeAllowed(summary.Metadata, servicePrincipal) {
			authorizedSummaries = append(authorizedSummaries, summary)
		}
	}
	summaries = authorizedSummaries
	if ambientDerivedScopeMetadata(summaries)["visibility"] != "organization" {
		if len(summaries) == 0 {
			return meetingMemoryEntry{}, nil
		}
		metadata := applyAmbientDerivedScope(map[string]string{
			"source": "scope_guard", "model": "none", "roomId": roomID,
			"fromBrainId": summaries[0].ID, "throughBrainId": summaries[len(summaries)-1].ID,
			"brainCount": strconv.Itoa(len(summaries)), "operationCount": "0", "changedOperationCount": "0", "errorOperationCount": "0",
		}, summaries)
		entry, _, err := app.memory.appendBoardUpdate(durableTimestampID("board-update", time.Now()), "Room-only brain window reviewed; global board mutation suppressed.", metadata)
		return entry, err
	}
	if len(summaries) == 0 {
		if droppedListenOnly > 0 {
			app.setAmbientAgentBaselineID(ambientAgentKey(meetingBoardAgentName, roomID), windowLast.ID)
		}
		return meetingMemoryEntry{}, nil
	}

	model := meetingBoardModel()
	text, err := responder(ctx, apiKey, openAITextRequest{
		Model:        model,
		Seat:         seatBoard,
		Instructions: meetingBoardInstructions(),
		Input:        buildMeetingBoardInput(summaries, app.snapshotState(), app.participantSnapshotForRoom(roomID), time.Now().UTC()),
		// A2: the board step emits structured tool calls with exact args — the
		// work low reasoning effort punishes most (dropped card_ids, invented
		// statuses). Medium buys reliable argument fidelity for a cheap step.
		ReasoningEffort: "medium",
		Verbosity:       "low",
		MaxOutputTokens: 1200,
	})
	if err != nil {
		return meetingMemoryEntry{}, err
	}

	analysis, err := parseMeetingBoardAnalysis(text)
	if err != nil {
		// W0 item 6: the strict-JSON parse-failure counter — the designated
		// gate metric for any board-lane model flip (Terra pin included).
		recordEvalEvent(seatBoard, evalKindParseFailure, map[string]any{"seat": seatBoard, "model": model})
		return meetingMemoryEntry{}, err
	}
	runResult := app.applyMeetingBoardAnalysisForRoom(analysis, roomID)

	firstSummary := summaries[0]
	lastSummary := summaries[len(summaries)-1]
	// W0 item 6: board-op fidelity — the regression alarm for the Terra pin.
	// meetingBoardRunResult already computes the per-op error rail; fold it
	// into one eval event per pass (op_count, error_count, error classes).
	recordEvalEvent(seatBoard, evalKindBoardOpFidelity, map[string]any{
		"op_count":      len(runResult.Applications),
		"error_count":   runResult.ErrorCount,
		"error_classes": meetingBoardErrorClasses(runResult),
		"changed_count": runResult.ChangedCount,
		"room_id":       roomID,
	})
	// W0 item 7: proposal lineage — every proposal this pass minted carries its
	// source surface + the brain-window/transcript ids so time-to-proposal
	// (minted TS − transcript TS) is computable from events alone.
	for _, application := range runResult.Applications {
		if application.Tool != "propose_codex_task" || application.Error != "" {
			continue
		}
		proposalID := codexProposalIDFromToolResult(application.Result)
		if proposalID == "" {
			continue
		}
		recordProposalEvent(proposalEventMinted, proposalID, map[string]any{
			"source":                proposalSourceBoardWorker,
			"from_brain_id":         firstSummary.ID,
			"through_transcript_id": strings.TrimSpace(lastSummary.Metadata["throughTranscriptId"]),
			"transcript_created_at": strings.TrimSpace(lastSummary.Metadata["throughTranscriptCreatedAt"]),
			"room_id":               roomID,
		})
	}
	metadata := map[string]string{
		"source": "openai_responses",
		"model":  model,
		"roomId": roomID,
		// the cursor advances through the ORIGINAL window's tail — a dropped
		// listen-only trailing brain must not re-feed this pass forever.
		"fromBrainId":           firstSummary.ID,
		"throughBrainId":        windowLast.ID,
		"fromBrainCreatedAt":    firstSummary.CreatedAt.Format(time.RFC3339Nano),
		"throughBrainCreatedAt": windowLast.CreatedAt.Format(time.RFC3339Nano),
		"brainCount":            strconv.Itoa(len(summaries)),
		"operationCount":        strconv.Itoa(len(runResult.Applications)),
		"changedOperationCount": strconv.Itoa(runResult.ChangedCount),
		"errorOperationCount":   strconv.Itoa(runResult.ErrorCount),
	}
	metadata = applyAmbientDerivedScope(metadata, summaries)
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

	// A2 write-back resilience: a pass that changed nothing but errored on every
	// op dropped real commitments the old code still cursored past (the "created
	// two cards" summary over three failed ops). When that happens, decline to
	// append the artifact so the cursor stays put and the same window is
	// re-attempted next pass — bounded to one retry per window (see
	// shouldRetryBoardWindow) so a permanently-rejected op cannot wedge the
	// worker. On the give-up pass we fall through and append the artifact whose
	// summary is reconciled to the real failure (renderMeetingBoardUpdateArtifact).
	if runResult.ChangedCount == 0 && runResult.ErrorCount > 0 && app.shouldRetryBoardWindow(windowLast.ID) {
		broadcastAssistantEvent("action", "Scout hit errors applying meeting summaries to the board; retrying next pass.", map[string]any{"kind": meetingMemoryKindBoardUpdate})
		return meetingMemoryEntry{}, nil
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
	broadcastOfficeKanbanEvent("memory", nil)
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
	return app.applyMeetingBoardAnalysisForRoom(analysis, officeRoomID)
}

// applyMeetingBoardAnalysisForRoom applies one pass's operations for the room
// whose brains fed it. §7.3 layer 2 (choke-point backstop, grafted from
// design B): when the source room's sitting is listen-only, every mutation op
// (create/update/move/tag/date/propose) is REFUSED into the per-operation
// error rail — belt and suspenders under the window filter, so a mis-filtered
// window still cannot touch the board or mint a proposal.
func (app *kanbanBoardApp) applyMeetingBoardAnalysisForRoom(analysis meetingBoardAnalysis, roomID string) meetingBoardRunResult {
	roomID = normalizeRoomID(roomID)
	listenOnlySource := app.sittingListenOnly(roomID)
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
		if listenOnlySource && toolName != "do_nothing" {
			application.Error = "refused: listen-only sitting — board mutations and proposals are suppressed"
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
		if toolName == "propose_codex_task" {
			// §7.3 layer 2: proposals minted by this pass carry their origin
			// room so proposeCodexTask and the workflow ticker can gate them.
			args["origin_room_id"] = roomID
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

// shouldRetryBoardWindow reports whether a total-failure board pass (nothing
// changed, at least one op errored) should re-attempt the same brain-summary
// window next pass instead of advancing the cursor past dropped commitments.
// It returns true at most once per window boundary, keyed by the through-brain
// id: the first total failure for a window retries (the fix here is that the
// re-attempt now benefits from status aliasing + title resolution), a second
// gives up and lets the reconciled failure artifact advance the cursor, so a
// permanently rejected op cannot wedge the worker behind a growing backlog.
func (app *kanbanBoardApp) shouldRetryBoardWindow(throughBrainID string) bool {
	throughBrainID = strings.TrimSpace(throughBrainID)
	if throughBrainID == "" {
		return false
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	if app.boardWorkerRetriedThroughID == throughBrainID {
		return false
	}
	app.boardWorkerRetriedThroughID = throughBrainID
	return true
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
		"Required arguments per tool — an operation missing them is dropped: create_ticket needs title, notes, owner, tags, and status; update_ticket, move_ticket, add_tags, and add_key_date each need the target card_id (or, if you lack the id, the card's exact title so it can be resolved) — move_ticket also needs status, add_tags also needs tags, add_key_date also needs label and date; propose_codex_task needs title, mode, and query; do_nothing needs reason.",
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
	if result.ChangedCount == 0 && result.ErrorCount > 0 {
		// A2: reconcile the headline with execution. The model's summary asserts
		// success verbatim, so when every operation errored and nothing changed,
		// override it with the real outcome instead of rendering the rosy claim
		// (the original finding was a "created two cards" summary over three
		// failed ops). The model's own words survive as a clearly-unverified note.
		builder.WriteString(fmt.Sprintf("Scout could not apply these meeting summaries to the board: all %d board operation(s) errored and nothing changed. See Board operations below for the per-operation errors.", result.ErrorCount))
		if result.Summary != "" {
			builder.WriteString(" Model summary (unverified): ")
			builder.WriteString(result.Summary)
		}
	} else if result.Summary != "" {
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

// meetingBoardErrorClasses buckets a pass's per-operation errors into the
// stable class vocabulary the board_op_fidelity eval event reports (W0 item
// 6). Classes are unique, in first-occurrence order, so the rollup can trend
// "bad_card_id spiked after the Terra pin" without string-diffing error rails.
func meetingBoardErrorClasses(result meetingBoardRunResult) []string {
	seen := map[string]struct{}{}
	classes := make([]string, 0, 4)
	for _, application := range result.Applications {
		class := meetingBoardErrorClass(application.Error)
		if class == "" {
			continue
		}
		if _, ok := seen[class]; ok {
			continue
		}
		seen[class] = struct{}{}
		classes = append(classes, class)
	}
	return classes
}

// meetingBoardErrorClass maps one operation error onto its fidelity class.
// bad_card_id (dropped/invented card ids) and invalid_status (invented
// statuses) are the two A2-documented regressions the Terra gate watches.
func meetingBoardErrorClass(message string) string {
	switch {
	case strings.TrimSpace(message) == "":
		return ""
	case strings.Contains(message, "operation tool is required"):
		return "missing_tool"
	case strings.Contains(message, "unsupported board worker tool"):
		return "unsupported_tool"
	case strings.Contains(message, "listen-only"):
		return "listen_only_refused"
	case strings.Contains(message, "business cards require"):
		return "doctrine_rejected"
	case strings.Contains(message, "card_id"):
		return "bad_card_id"
	case strings.Contains(message, "status"):
		return "invalid_status"
	default:
		return "apply_error"
	}
}

// codexProposalIDFromToolResult digs the minted proposal's id out of a
// propose_codex_task tool result ({"ok":true,"proposal":{"id":...}} per
// proposeCodexTask) — the join key for the minted→resolved→launched→terminal
// proposal funnel.
func codexProposalIDFromToolResult(result map[string]any) string {
	if result == nil {
		return ""
	}
	proposal, ok := result["proposal"].(map[string]any)
	if !ok {
		return ""
	}
	return strings.TrimSpace(asString(proposal["id"]))
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
