package main

// Decision Ledger — an ambient agent (agent_runner.go recipe) that consumes
// meeting-brain write-ups and extracts EXPLICIT team decisions as durable
// kind "decision" memory entries (entry.Text = the statement, so store.search
// grounds "what did we decide about X?" for free). Every pass appends exactly
// one kind "decision_pass" cursor artifact — even a zero-decision pass — so
// unconsumedEntriesAfter never re-feeds the same brain window. Individual
// decisions are appended BEFORE the pass entry.
//
// Visibility asymmetry (the load-bearing design): "decision" is NOT a
// UI-state kind (it reaches Scout search + query context) but IS excluded
// from the client memory timeline; "decision_pass" is pure UI-state cursor
// bookkeeping and excluded from both.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	decisionLedgerAgentName       = "decision ledger"
	defaultDecisionLedgerInterval = 5 * time.Minute
	decisionLedgerRequestTimeout  = 90 * time.Second
	// decisionStatusActive is what the extraction pass writes;
	// decisionStatusSuperseded is stamped by markDecisionSuperseded (+ metadata
	// "supersededBy"/"supersededAt") when a newer decision replaces an entry —
	// superseded rows keep their history but leave every active lane.
	// decisionStatusProposed (card 069) marks a recorded DEFAULT awaiting team
	// ratification: visible on the ledger surface, but excluded from every
	// active lane (Scout query pinning, dedupe window, already-recorded list)
	// until POST /assistant/decisions/ratify flips it active.
	decisionStatusActive     = "active"
	decisionStatusSuperseded = "superseded"
	decisionStatusProposed   = "proposed"
	// decisionDedupeJaccard: a new statement whose normalized token set
	// overlaps an existing active decision at or above this ratio is a
	// restatement, not a new decision.
	decisionDedupeJaccard = 0.8
	// decisionDedupeWindow bounds the server-side dedupe scan to the newest
	// active decisions; older duplicates are the supersede tool's problem.
	decisionDedupeWindow = 50
	// decisionContextLimit is how many active decisions the Scout query input
	// pins under "# Decisions on record".
	decisionContextLimit = 12
	// decisionSnapshotLimit caps the mission-payload ledger section.
	decisionSnapshotLimit = 30
)

func decisionLedgerAgent() ambientAgentConfig {
	return ambientAgentConfig{
		name:              decisionLedgerAgentName,
		defaultInterval:   defaultDecisionLedgerInterval,
		intervalEnv:       "DECISION_LEDGER_INTERVAL",
		disabledEnv:       "DECISION_LEDGER_DISABLED",
		backfillEnv:       "DECISION_LEDGER_BACKFILL",
		minBatchEnv:       "DECISION_LEDGER_MIN_INPUTS",
		defaultMinBatch:   1,
		maxBatchEnv:       "DECISION_LEDGER_MAX_INPUTS",
		defaultMaxBatch:   8,
		inputKind:         meetingMemoryKindBrain,
		artifactKind:      meetingMemoryKindDecisionPass,
		cursorMetadataKey: "throughBrainId",
		requestTimeout:    decisionLedgerRequestTimeout,
		produce:           (*kanbanBoardApp).produceDecisionLedgerPass,
	}
}

func (app *kanbanBoardApp) startDecisionLedgerWorker(apiKey string) {
	app.startAmbientAgent(decisionLedgerAgent(), apiKey)
}

type extractedDecision struct {
	Statement string `json:"statement"`
	MadeBy    string `json:"madeBy"`
	Context   string `json:"context"`
	// Package is optional and only honored on a case-insensitive EXACT match
	// against an existing venture package name (Part B contract).
	Package string `json:"package"`
}

type decisionLedgerExtraction struct {
	Decisions []extractedDecision `json:"decisions"`
}

// parseDecisionLedgerOutput validates agent output: strict JSON, with the
// same stray-markdown-fence tolerance as parseMissionInsight.
func parseDecisionLedgerOutput(text string) (decisionLedgerExtraction, bool) {
	text = strings.TrimSpace(text)
	if fenced := strings.TrimPrefix(text, "```json"); fenced != text {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fenced), "```"))
	} else if fenced := strings.TrimPrefix(text, "```"); fenced != text {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fenced), "```"))
	}
	var extraction decisionLedgerExtraction
	if text == "" || json.Unmarshal([]byte(text), &extraction) != nil {
		return decisionLedgerExtraction{}, false
	}

	return extraction, true
}

func decisionLedgerInstructions() string {
	return strings.Join([]string{
		"You are Bonfire's decision ledger.",
		"From the supplied meeting-brain write-ups, extract only EXPLICIT decisions the team actually made — commitments, choices, approvals, pricing, go/no-go.",
		"Return STRICT JSON only:",
		`{"decisions":[{"statement":string(<=200 chars, self-contained, present tense),"madeBy":string(a listed participant, or empty when unclear),"context":string(<=160 chars why/when)}]}.`,
		"Never invent decisions, people, numbers, or dates.",
		"Exclude open questions, proposals under discussion, and anything already in the ALREADY RECORDED list.",
		`Empty window → {"decisions":[]}.`,
	}, " ")
}

// buildDecisionLedgerInput assembles the extraction input: participants for
// madeBy grounding, the already-recorded exclusion list (prompt-layer dedupe),
// and the brain window formatted like buildMeetingBoardInput's summary block.
func (app *kanbanBoardApp) buildDecisionLedgerInput(inputs []meetingMemoryEntry, generatedAt time.Time) string {
	location := meetingTimeLocation()
	var builder strings.Builder
	builder.WriteString("# Generated at\n")
	builder.WriteString(generatedAt.In(location).Format(time.RFC1123))

	builder.WriteString("\n\n# Participants\n")
	builder.WriteString(strings.Join(meetingParticipantNames, ", "))
	builder.WriteByte('\n')

	if recorded := app.activeDecisionEntries(25); len(recorded) > 0 {
		builder.WriteString("\n# Already recorded decisions (do not re-emit)\n")
		for _, decision := range recorded {
			builder.WriteString("- ")
			builder.WriteString(decision.Text)
			builder.WriteByte('\n')
		}
	}

	if packages := app.venturePackagesSnapshot(); len(packages) > 0 {
		builder.WriteString("\n# Package names\n")
		builder.WriteString("When a decision clearly concerns exactly one of these venture packages, add \"package\": \"<exact name>\" to that decision object; otherwise omit the field.\n")
		for _, record := range packages {
			builder.WriteString("- ")
			builder.WriteString(record.Name)
			builder.WriteByte('\n')
		}
	}

	builder.WriteString("\n# Meeting brain write-ups to analyze\n")
	for _, entry := range inputs {
		builder.WriteString("- id=")
		builder.WriteString(entry.ID)
		builder.WriteString(" kind=")
		builder.WriteString(entry.Kind)
		builder.WriteString(" time=")
		builder.WriteString(entry.CreatedAt.In(location).Format(time.RFC3339))
		builder.WriteByte('\n')
		for _, line := range strings.Split(entry.Text, "\n") {
			builder.WriteString("  ")
			builder.WriteString(strings.TrimSpace(line))
			builder.WriteByte('\n')
		}
	}

	return builder.String()
}

// decisionDedupeKey normalizes a statement into the comparable token string
// stored in metadata and compared exactly on append.
func decisionDedupeKey(statement string) string {
	return strings.Join(uniqueMemoryTokens(canonicalizeDomainTerms(strings.ToLower(statement))), " ")
}

// activeDecisionEntries returns up to limit kind=decision entries with
// status active, newest first.
func (app *kanbanBoardApp) activeDecisionEntries(limit int) []meetingMemoryEntry {
	if app == nil || app.memory == nil || limit <= 0 {
		return nil
	}
	// entriesOfKind returns oldest-first; take an over-fetch of 0 (= all) and
	// walk backwards so "newest N active" survives interleaved superseded rows.
	entries := app.memory.entriesOfKind(meetingMemoryKindDecision, 0)
	newest := make([]meetingMemoryEntry, 0, limit)
	for index := len(entries) - 1; index >= 0 && len(newest) < limit; index-- {
		if entries[index].Metadata["status"] != decisionStatusActive {
			continue
		}
		newest = append(newest, entries[index])
	}

	return newest
}

// markDecisionSuperseded implements the reserved superseded path (§5 / Wave 2
// item 11): the older decision keeps its row — the ledger is history, never a
// delete — but drops out of every active lane built on activeDecisionEntries:
// the Scout query pinning (memory_query.go's "Decisions on record"), the
// server-side dedupe window, and the already-recorded exclusion list. The
// superseding decision must itself be on the ledger, so the chain is always
// resolvable. Idempotent: a decision that is already superseded stays exactly
// as first stamped (first supersession wins — retries and double-taps never
// rewrite history), and the stamp rides the store's JSONL rewrite so it
// survives reload.
func (app *kanbanBoardApp) markDecisionSuperseded(decisionID string, supersededByID string) (meetingMemoryEntry, bool, error) {
	if app == nil || app.memory == nil {
		return meetingMemoryEntry{}, false, fmt.Errorf("meeting memory is unavailable")
	}
	decisionID = strings.TrimSpace(decisionID)
	supersededByID = strings.TrimSpace(supersededByID)
	if decisionID == "" || supersededByID == "" {
		return meetingMemoryEntry{}, false, fmt.Errorf("decision id and superseding decision id are required")
	}
	if decisionID == supersededByID {
		return meetingMemoryEntry{}, false, fmt.Errorf("a decision cannot supersede itself")
	}
	entry, found := app.memory.entryByKindAndID(meetingMemoryKindDecision, decisionID)
	if !found {
		return meetingMemoryEntry{}, false, fmt.Errorf("decision %s not found", decisionID)
	}
	if _, found := app.memory.entryByKindAndID(meetingMemoryKindDecision, supersededByID); !found {
		return meetingMemoryEntry{}, false, fmt.Errorf("superseding decision %s not found", supersededByID)
	}
	if entry.Metadata["status"] == decisionStatusSuperseded {
		return entry, false, nil
	}
	updated, changed, err := app.memory.updateEntryWithMetadata(meetingMemoryKindDecision, decisionID, entry.Text, map[string]string{
		"status":       decisionStatusSuperseded,
		"supersededBy": supersededByID,
		"supersededAt": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return meetingMemoryEntry{}, false, err
	}
	if changed {
		// Same wire event the extraction pass broadcasts, so the mission
		// ledger re-ranks the row out of the active lane live.
		broadcastOfficeKanbanEvent("decision", decisionPayload(updated))
	}
	return updated, changed, nil
}

// assistantDecisionSupersedeHandler is the invocation seam for the superseded
// path (§5 / Wave 2 item 11): POST {decisionId, supersededById} retires a
// stale decision from every active lane. Same origin+session gates as
// assistantGoalCancelHandler; any signed-in user — the ledger is shared team
// knowledge with no per-decision owner, the operation is non-destructive
// (history is kept, chain recorded), and markDecisionSuperseded validates
// both ids against the ledger itself.
func assistantDecisionSupersedeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "assistant is unavailable")
		return
	}

	payload := struct {
		DecisionID     string `json:"decisionId"`
		SupersededByID string `json:"supersededById"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read supersede request")
		return
	}

	entry, changed, err := kanbanApp.markDecisionSuperseded(payload.DecisionID, payload.SupersededByID)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		writeAuthError(w, status, err.Error())
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"changed":  changed,
		"decision": decisionPayload(entry),
	})
}

// governanceLanesDecisionStatement is card 069's DEFAULT approval-governance
// decision — the three lanes of approval_lanes.go as one self-contained,
// present-tense ledger statement (<=200 chars, the extraction contract).
const governanceLanesDecisionStatement = "Approvals run in three lanes: quick single-pass runs launch instantly; goal loops and Scout-proposed work need one member confirm; external-write work ships only with AJ or two member endorsements."

// seedProposedGovernanceDecision records the card-069 default on the ledger
// with status=proposed so the team can ratify (or supersede) it. Idempotence
// scans ALL decision rows regardless of status — a ratified (active) or
// superseded copy must never re-seed as proposed on the next boot.
func (app *kanbanBoardApp) seedProposedGovernanceDecision() {
	if app == nil || app.memory == nil {
		return
	}
	key := decisionDedupeKey(governanceLanesDecisionStatement)
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindDecision, 0) {
		if firstNonEmptyString(entry.Metadata["dedupeKey"], decisionDedupeKey(entry.Text)) == key {
			return
		}
	}
	metadata := map[string]string{
		"madeBy":    "Scout",
		"context":   "card 069 default — awaiting team ratification",
		"dedupeKey": key,
		"status":    decisionStatusProposed,
	}
	if _, _, err := app.memory.appendDecision(durableTimestampID("decision", time.Now()), governanceLanesDecisionStatement, metadata); err != nil {
		log.Errorf("Failed to seed the governance-lanes decision: %v", err)
	}
}

// markDecisionRatified flips a PROPOSED decision to active with the ratifying
// member on record. Idempotent: an already-active decision stays exactly as
// first stamped (changed=false); a superseded decision is history and can
// never be ratified back into the active lanes.
func (app *kanbanBoardApp) markDecisionRatified(decisionID string, ratifiedBy string) (meetingMemoryEntry, bool, error) {
	if app == nil || app.memory == nil {
		return meetingMemoryEntry{}, false, fmt.Errorf("meeting memory is unavailable")
	}
	decisionID = strings.TrimSpace(decisionID)
	if decisionID == "" {
		return meetingMemoryEntry{}, false, fmt.Errorf("decision id is required")
	}
	entry, found := app.memory.entryByKindAndID(meetingMemoryKindDecision, decisionID)
	if !found {
		return meetingMemoryEntry{}, false, fmt.Errorf("decision %s not found", decisionID)
	}
	switch firstNonEmptyString(entry.Metadata["status"], decisionStatusActive) {
	case decisionStatusActive:
		return entry, false, nil
	case decisionStatusSuperseded:
		return meetingMemoryEntry{}, false, fmt.Errorf("decision %s is superseded and cannot be ratified", decisionID)
	}
	updated, changed, err := app.memory.updateEntryWithMetadata(meetingMemoryKindDecision, decisionID, entry.Text, map[string]string{
		"status":     decisionStatusActive,
		"ratifiedBy": strings.TrimSpace(ratifiedBy),
		"ratifiedAt": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return meetingMemoryEntry{}, false, err
	}
	if changed {
		// Same wire event the extraction pass broadcasts, so the mission ledger
		// re-ranks the row into the active lane live.
		broadcastOfficeKanbanEvent("decision", decisionPayload(updated))
	}
	return updated, changed, nil
}

// assistantDecisionRatifyHandler is card 069's ratify door: POST {decisionId}
// flips a PROPOSED default to an active team decision. Same origin+session
// gates as assistantDecisionSupersedeHandler; any signed-in member — the
// default was recorded precisely to collect the team's ratification, the flip
// is non-destructive, and the ratifier is stamped for the audit trail.
func assistantDecisionRatifyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "assistant is unavailable")
		return
	}

	payload := struct {
		DecisionID string `json:"decisionId"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read ratify request")
		return
	}

	entry, changed, err := kanbanApp.markDecisionRatified(payload.DecisionID, user.Name)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		writeAuthError(w, status, err.Error())
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"changed":  changed,
		"decision": decisionPayload(entry),
	})
}

// decisionLedgerSnapshot shapes the newest decisions for the all-users
// mission payload: active first, newest first within each status, capped.
// SAFE for the signed-in-wide gate: statements are model-synthesized meeting
// knowledge (same class as themes), never artifact text.
func (app *kanbanBoardApp) decisionLedgerSnapshot(limit int) []map[string]any {
	if app == nil || app.memory == nil || limit <= 0 {
		return []map[string]any{}
	}
	entries := app.memory.entriesOfKind(meetingMemoryKindDecision, 0)
	active := make([]map[string]any, 0, limit)
	inactive := make([]map[string]any, 0)
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if entry.Metadata["status"] == decisionStatusActive || strings.TrimSpace(entry.Metadata["status"]) == "" {
			active = append(active, decisionPayload(entry))
		} else {
			inactive = append(inactive, decisionPayload(entry))
		}
	}
	payloads := append(active, inactive...)
	if len(payloads) > limit {
		payloads = payloads[:limit]
	}

	return payloads
}

func decisionPayload(entry meetingMemoryEntry) map[string]any {
	payload := map[string]any{
		"id":        entry.ID,
		"statement": entry.Text,
		"madeBy":    entry.Metadata["madeBy"],
		"context":   entry.Metadata["context"],
		"meetingId": entry.Metadata["meetingId"],
		"status":    firstNonEmptyString(entry.Metadata["status"], decisionStatusActive),
		"createdAt": entry.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if packageID := strings.TrimSpace(entry.Metadata["packageId"]); packageID != "" {
		payload["packageId"] = packageID
	}
	if supersededBy := strings.TrimSpace(entry.Metadata["supersededBy"]); supersededBy != "" {
		payload["supersededBy"] = supersededBy
	}
	if supersededAt := strings.TrimSpace(entry.Metadata["supersededAt"]); supersededAt != "" {
		payload["supersededAt"] = supersededAt
	}
	if ratifiedBy := strings.TrimSpace(entry.Metadata["ratifiedBy"]); ratifiedBy != "" {
		payload["ratifiedBy"] = ratifiedBy
	}
	if ratifiedAt := strings.TrimSpace(entry.Metadata["ratifiedAt"]); ratifiedAt != "" {
		payload["ratifiedAt"] = ratifiedAt
	}

	return payload
}

func (app *kanbanBoardApp) produceDecisionLedgerPass(ctx context.Context, apiKey string, inputs []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error) {
	model := meetingBrainModel()
	text, err := responder(ctx, apiKey, openAITextRequest{
		Model:           model,
		Instructions:    decisionLedgerInstructions(),
		Input:           app.buildDecisionLedgerInput(inputs, time.Now().UTC()),
		ReasoningEffort: "low",
		Verbosity:       "low",
		MaxOutputTokens: 700,
	})
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	extraction, ok := parseDecisionLedgerOutput(text)
	if !ok {
		// Never advance the cursor on unparseable output: with no decision_pass
		// entry appended, the next pass retries the same brain window
		// (mission-intel precedent).
		log.Errorf("%s returned non-JSON output; skipping this pass", decisionLedgerAgentName)
		return meetingMemoryEntry{}, nil
	}

	firstBrain := inputs[0]
	lastBrain := inputs[len(inputs)-1]

	// Server-layer dedupe against the newest active decisions: exact key match
	// or token-set Jaccard >= 0.8 both mean "restatement, skip".
	existing := app.activeDecisionEntries(decisionDedupeWindow)
	existingKeys := make([]string, 0, len(existing))
	for _, entry := range existing {
		existingKeys = append(existingKeys, firstNonEmptyString(entry.Metadata["dedupeKey"], decisionDedupeKey(entry.Text)))
	}

	appendedCount := 0
	for _, decision := range extraction.Decisions {
		statement := normalizeMemoryText(decision.Statement)
		if statement == "" {
			continue
		}
		key := decisionDedupeKey(statement)
		if key == "" {
			continue
		}
		duplicate := false
		for _, existingKey := range existingKeys {
			if key == existingKey || tokenSetJaccard(strings.Fields(key), strings.Fields(existingKey)) >= decisionDedupeJaccard {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}

		// Unknown names are blanked, never invented into the roster.
		madeBy := normalizeTranscriptSpeaker(decision.MadeBy)
		metadata := map[string]string{
			"madeBy":        madeBy,
			"context":       normalizeMemoryText(decision.Context),
			"sourceBrainId": lastBrain.ID,
			"dedupeKey":     key,
			"status":        decisionStatusActive,
		}
		id := durableTimestampID("decision", time.Now())
		entry, appended, err := app.memory.appendDecision(id, statement, metadata)
		if err != nil {
			return meetingMemoryEntry{}, err
		}
		if !appended {
			continue
		}
		existingKeys = append(existingKeys, key)
		appendedCount++
		// Binder linkage: an exact package-name match files the decision into
		// its venture package (attachToPackage stamps packageId back onto the
		// decision entry, so re-read before broadcasting the payload).
		if record, found := app.venturePackageByExactName(decision.Package); found {
			if _, attachErr := app.attachToPackage(record.ID, packageRefTypeDecision, entry.ID, scoutParticipantName); attachErr != nil {
				log.Errorf("Failed to attach decision %s to package %s: %v", entry.ID, record.ID, attachErr)
			} else if stamped, ok := app.memory.entryByKindAndID(meetingMemoryKindDecision, entry.ID); ok {
				entry = stamped
			}
		}
		broadcastOfficeKanbanEvent("decision", decisionPayload(entry))
	}
	if appendedCount > 0 {
		broadcastAssistantEvent("action", "Scout logged "+strconv.Itoa(appendedCount)+" decision(s) to the ledger.", map[string]any{"kind": "decision"})
	}

	// The pass entry ALWAYS lands — including zero-decision windows —
	// otherwise unconsumedEntriesAfter re-feeds the same brains forever.
	passText := "Extracted " + strconv.Itoa(appendedCount) + " decision(s)"
	if appendedCount == 0 {
		passText = "No decisions in this window"
	}
	passMetadata := map[string]string{
		"source":         "openai_responses",
		"model":          model,
		"fromBrainId":    firstBrain.ID,
		"throughBrainId": lastBrain.ID,
		"brainCount":     strconv.Itoa(len(inputs)),
		"decisionCount":  strconv.Itoa(appendedCount),
	}
	passEntry, _, err := app.memory.appendDecisionPass(durableTimestampID("decision-pass", time.Now()), passText, passMetadata)
	if err != nil {
		return meetingMemoryEntry{}, err
	}

	return passEntry, nil
}
