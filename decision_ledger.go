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
	// decisionStatusProposedSupersession (item 2.2b) marks a NEW firm decision
	// that adjudication judged to reverse an existing active decision. It is held
	// OUT of every active lane — like proposed — so the store never co-pins two
	// contradictory decisions; a human ratifies it to flip it active AND supersede
	// the one it contradicts (markDecisionRatified). NEVER auto-supersede: a
	// missed supersession is recoverable, a false one is not.
	decisionStatusProposedSupersession = "proposed-supersession"
	// decisionReversalLowerJaccard/decisionReversalUpperJaccard bound the
	// reversal-suspect band. Below the lower bound two decisions are unrelated;
	// at/above decisionDedupeJaccard a new statement is a restatement and never
	// reaches this check (dedupe drops it first). The middle band — "about the
	// same thing, differently" — is exactly where a reversal hides, so only it is
	// spent on the adjudication call.
	decisionReversalLowerJaccard = 0.4
	decisionReversalUpperJaccard = decisionDedupeJaccard
	// decisionReversalCandidateCap bounds the adjudication batch: newest active
	// decisions compared against one new statement, overflow ignored (a missed
	// reversal is recoverable by the manual supersede door).
	decisionReversalCandidateCap = 50
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
		roomScoped:        true, // W4 §7.4: per-room brain windows and pass cursors
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
	// Directional (A5) marks a strategic LEAN the team is converging on but has
	// not firmly committed — recorded on the ledger as status=proposed (visible
	// on the ledger surface, excluded from every active/firm lane) so directional
	// convergence is captured WITHOUT polluting the firm-decision discipline.
	Directional bool `json:"directional"`
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
		"From the supplied meeting-brain write-ups, extract FIRM decisions the team actually made — commitments, choices, approvals, pricing, go/no-go — and, separately, clear DIRECTIONAL alignments.",
		"Return STRICT JSON only:",
		`{"decisions":[{"statement":string(<=200 chars, self-contained, present tense),"madeBy":string(a listed participant, or empty when unclear),"context":string(<=160 chars why/when),"directional":boolean}]}.`,
		"A FIRM decision (directional:false or omitted) is settled and acted-on. Keep the strict bar: never record an open question, a proposal still under debate, or a lean as firm.",
		"A DIRECTIONAL alignment (directional:true) is a strategic lean the team is genuinely converging on but has NOT firmly committed — a working preference, a proposed split, or ONE named person's stated position on a topic.",
		"When a directional lean is a SPECIFIC person's stated position (\"Tim wants to hold pricing\", \"AJ leans toward Zebra\"), set madeBy to that person so their stance is on record; leave madeBy empty only for a genuinely group-wide lean with no single holder.",
		`Example directional (group lean): {"statement":"The team is leaning toward Ball Dogs as the lead IP over the alternatives.","madeBy":"","context":"consensus forming, not finalized","directional":true}.`,
		`Example directional (personal position): {"statement":"Tim favors holding the current pricing rather than discounting.","madeBy":"Tim","context":"stated in the pricing debate","directional":true}.`,
		"Be CONSERVATIVE with directional: only real convergence or a clearly stated position, never routine brainstorming or a single offhand remark. When unsure whether something is even directional, omit it.",
		"Resolve every spoken relative date ('yesterday', 'next Friday', 'end of the month') to an absolute YYYY-MM-DD using the generation date above; never leave a relative date unresolved in a statement or context.",
		"Never invent decisions, people, numbers, or dates.",
		"Exclude anything already in the ALREADY RECORDED list.",
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

	// Already-recorded exclusion (prompt-layer dedupe): active decisions AND
	// pending reversals (proposed-supersession). A pending reversal is a firm
	// decision held out of the active lane awaiting ratification; without it here
	// the model re-emits the same reversal every window and it re-files as a
	// duplicate pending row (F16).
	recorded := app.activeDecisionEntries(25)
	recorded = append(recorded, app.proposedSupersessionEntries(25)...)
	if len(recorded) > 0 {
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

// proposedDecisionEntries returns up to limit kind=decision entries with status
// proposed, newest first — the directional-alignment tier (A5) plus any
// card-069 governance default awaiting ratification.
func (app *kanbanBoardApp) proposedDecisionEntries(limit int) []meetingMemoryEntry {
	if app == nil || app.memory == nil || limit <= 0 {
		return nil
	}
	entries := app.memory.entriesOfKind(meetingMemoryKindDecision, 0)
	newest := make([]meetingMemoryEntry, 0, limit)
	for index := len(entries) - 1; index >= 0 && len(newest) < limit; index-- {
		if entries[index].Metadata["status"] != decisionStatusProposed {
			continue
		}
		newest = append(newest, entries[index])
	}

	return newest
}

// proposedSupersessionEntries returns up to limit kind=decision entries with
// status proposed-supersession, newest first — pending reversals awaiting
// ratification. They are firm decisions held OUT of the active lane, so unless
// they are surfaced to the dedupe key sets AND the already-recorded list, the
// same reversal re-extracts as a fresh pending row every window (F16).
func (app *kanbanBoardApp) proposedSupersessionEntries(limit int) []meetingMemoryEntry {
	if app == nil || app.memory == nil || limit <= 0 {
		return nil
	}
	entries := app.memory.entriesOfKind(meetingMemoryKindDecision, 0)
	newest := make([]meetingMemoryEntry, 0, limit)
	for index := len(entries) - 1; index >= 0 && len(newest) < limit; index-- {
		if entries[index].Metadata["status"] != decisionStatusProposedSupersession {
			continue
		}
		newest = append(newest, entries[index])
	}

	return newest
}

// dedupeKeysFor collects the normalized dedupe keys of a decision entry slice.
func dedupeKeysFor(entries []meetingMemoryEntry) []string {
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		keys = append(keys, firstNonEmptyString(entry.Metadata["dedupeKey"], decisionDedupeKey(entry.Text)))
	}
	return keys
}

// decisionKeyDuplicate reports whether key restates any of keys — an exact match
// or token-set Jaccard at/above the dedupe threshold.
func decisionKeyDuplicate(key string, keys []string) bool {
	keyFields := strings.Fields(key)
	for _, existingKey := range keys {
		if key == existingKey || tokenSetJaccard(keyFields, strings.Fields(existingKey)) >= decisionDedupeJaccard {
			return true
		}
	}
	return false
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
//
// A proposed-supersession (item 2.2b) ratifies the reversal in one click: the
// new decision flips active AND the decision it contradicts is superseded, so
// the reversal only happens on explicit human confirmation — never automatically
// at extraction time. The new active row carries a supersedesSummary of the
// retired decision so the recall lane can render the "current / previously"
// chain inline (item 2.3a).
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
	priorStatus := firstNonEmptyString(entry.Metadata["status"], decisionStatusActive)
	switch priorStatus {
	case decisionStatusActive:
		return entry, false, nil
	case decisionStatusSuperseded:
		return meetingMemoryEntry{}, false, fmt.Errorf("decision %s is superseded and cannot be ratified", decisionID)
	}

	now := time.Now()
	metadataUpdate := map[string]string{
		"status":     decisionStatusActive,
		"ratifiedBy": strings.TrimSpace(ratifiedBy),
		"ratifiedAt": now.UTC().Format(time.RFC3339Nano),
	}
	supersedesID := strings.TrimSpace(entry.Metadata["supersedes"])
	retireSuperseded := false
	if priorStatus == decisionStatusProposedSupersession && supersedesID != "" {
		if prior, ok := app.memory.entryByKindAndID(meetingMemoryKindDecision, supersedesID); ok {
			metadataUpdate["supersedesSummary"] = decisionChainSummary(prior, now)
			retireSuperseded = true
			// F20: this row was swept past the entity-ledger decision cursor while
			// held as proposed-supersession (skipped, no fact). Now that it flips
			// active it must reach the ledger as the new canon; flag it for re-feed
			// so the next consolidation pass re-folds it even though it sits behind
			// the cursor. The pass clears the marker once it has consumed the row.
			metadataUpdate[entityLedgerRefeedMetadataKey] = "1"
		}
	}

	updated, changed, err := app.memory.updateEntryWithMetadata(meetingMemoryKindDecision, decisionID, entry.Text, metadataUpdate)
	if err != nil {
		return meetingMemoryEntry{}, false, err
	}
	// Retire the contradicted decision only AFTER the new one is active, so the
	// supersession chain (old.supersededBy → new) always resolves to a live row.
	if retireSuperseded {
		if _, _, supersedeErr := app.markDecisionSuperseded(supersedesID, decisionID); supersedeErr != nil {
			log.Errorf("ratified supersession %s could not retire %s: %v", decisionID, supersedesID, supersedeErr)
		} else if retired, ok := app.memory.entryByKindAndID(meetingMemoryKindDecision, supersedesID); ok {
			// F20: re-feed the retired decision too, so its now-terminal (superseded)
			// status closes its ledger record through the decision lane. Its own text
			// is preserved — only the re-feed marker is stamped.
			if _, _, stampErr := app.memory.updateEntryWithMetadata(meetingMemoryKindDecision, supersedesID, retired.Text, map[string]string{entityLedgerRefeedMetadataKey: "1"}); stampErr != nil {
				log.Errorf("ratified supersession %s could not flag retired %s for ledger re-feed: %v", decisionID, supersedesID, stampErr)
			}
		}
	}
	if changed {
		// Same wire event the extraction pass broadcasts, so the mission ledger
		// re-ranks the row into the active lane live.
		broadcastOfficeKanbanEvent("decision", decisionPayload(updated))
	}
	return updated, changed, nil
}

// decisionChainSummary renders a retired decision as the compact "previously"
// clause the recall lane appends to its successor (item 2.3a): the statement
// bounded to one line plus the date it stopped being current.
func decisionChainSummary(prior meetingMemoryEntry, until time.Time) string {
	statement := compactAssistantLine(prior.Text)
	if runes := []rune(statement); len(runes) > 160 {
		statement = strings.TrimSpace(string(runes[:159])) + "…"
	}
	return statement + " (until " + until.In(meetingTimeLocation()).Format("2006-01-02") + ")"
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
	// supersedes/supersedesSummary (item 2.2b/2.3a): a ratified reversal points
	// FORWARD at the decision it retired, with a compact chain summary for recall.
	if supersedes := strings.TrimSpace(entry.Metadata["supersedes"]); supersedes != "" {
		payload["supersedes"] = supersedes
	}
	if supersedesSummary := strings.TrimSpace(entry.Metadata["supersedesSummary"]); supersedesSummary != "" {
		payload["supersedesSummary"] = supersedesSummary
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
	// sourceBrainId (card 081) rides the wire so a ledger row can point back at
	// the meeting-brain write-up it was extracted from; only present when the
	// extraction pass stamped it.
	if sourceBrainID := strings.TrimSpace(entry.Metadata["sourceBrainId"]); sourceBrainID != "" {
		payload["sourceBrainId"] = sourceBrainID
	}

	return payload
}

/* ---------- item 2.2b: reversal adjudication (propose-first) ---------- */

// decisionReversalPair is one adjudication candidate: a NEW firm statement and
// the existing active decision it might reverse.
type decisionReversalPair struct {
	decisionIndex int    // index into the extraction, so a verdict routes back
	statement     string // the new statement
	candidateID   string // the existing active decision it might reverse
	candidateText string
}

func decisionReversalInstructions() string {
	return strings.Join([]string{
		"You are Bonfire's decision-reversal adjudicator.",
		"For each numbered pair, decide whether the NEW decision REVERSES or REPLACES the EXISTING decision on record — i.e. the two conflict and cannot both stand.",
		"Verdicts: \"supersedes\" = the new decision explicitly reverses, replaces, or overrides the existing one; \"different\" = both can hold at once (a refinement, a follow-on, a decision about a different facet, or an unrelated matter).",
		"Be STRICT: answer \"supersedes\" only on a genuine head-on conflict on the SAME question, never on merely-related work.",
		"Return STRICT JSON only, no markdown fence:",
		`{"verdicts":[{"i":0,"verdict":"different"}]} with exactly one verdict per pair.`,
	}, " ")
}

func buildDecisionReversalInput(pairs []decisionReversalPair, generatedAt time.Time) string {
	var builder strings.Builder
	builder.WriteString("# Generated at\n")
	builder.WriteString(generatedAt.Format(time.RFC3339))
	builder.WriteString("\n\n# Pairs\n")
	for index, pair := range pairs {
		builder.WriteString(fmt.Sprintf("- i=%d\n", index))
		builder.WriteString("  new: ")
		builder.WriteString(pair.statement)
		builder.WriteString("\n  existing: ")
		builder.WriteString(pair.candidateText)
		builder.WriteByte('\n')
	}

	return builder.String()
}

// detectDecisionReversals finds, for each new FIRM non-duplicate decision, the
// best existing active decision in the reversal-suspect band, then spends ONE
// batched adjudication call to decide which are true reversals. Returns
// decisionIndex → superseded-candidate id for the confirmed reversals only.
// Directional leans are excluded (they become positions, not reversals), and a
// restatement (>= dedupe Jaccard) never reaches here — dedupe drops it first.
func (app *kanbanBoardApp) detectDecisionReversals(ctx context.Context, apiKey string, responder openAITextResponder, decisions []extractedDecision, activeEntries []meetingMemoryEntry, activeKeys []string, pendingReversalKeys []string) map[int]string {
	if app == nil || len(decisions) == 0 || len(activeEntries) == 0 {
		return nil
	}
	pairs := make([]decisionReversalPair, 0, len(decisions))
	for decisionIndex, decision := range decisions {
		if decision.Directional {
			continue
		}
		statement := normalizeMemoryText(decision.Statement)
		if statement == "" {
			continue
		}
		key := decisionDedupeKey(statement)
		// A statement already active OR already pending as a reversal is not a new
		// reversal candidate: re-proposing it would mint a duplicate pending row
		// every window (F16).
		if key == "" || decisionKeyDuplicate(key, activeKeys) || decisionKeyDuplicate(key, pendingReversalKeys) {
			continue
		}
		keyFields := strings.Fields(key)
		best := ""
		bestText := ""
		bestScore := 0.0
		for _, entry := range activeEntries {
			candidateKey := firstNonEmptyString(entry.Metadata["dedupeKey"], decisionDedupeKey(entry.Text))
			jaccard := tokenSetJaccard(keyFields, strings.Fields(candidateKey))
			if jaccard >= decisionReversalLowerJaccard && jaccard < decisionReversalUpperJaccard && jaccard > bestScore {
				bestScore = jaccard
				best = entry.ID
				bestText = entry.Text
			}
		}
		if best == "" {
			continue
		}
		pairs = append(pairs, decisionReversalPair{
			decisionIndex: decisionIndex,
			statement:     statement,
			candidateID:   best,
			candidateText: bestText,
		})
	}
	if len(pairs) == 0 {
		return nil
	}
	if len(pairs) > decisionReversalCandidateCap {
		pairs = pairs[:decisionReversalCandidateCap]
	}

	model := meetingBrainModel()
	text, err := responder(ctx, apiKey, openAITextRequest{
		Model:           model,
		Seat:            seatDecisionLedger,
		Instructions:    decisionReversalInstructions(),
		Input:           buildDecisionReversalInput(pairs, time.Now().UTC()),
		ReasoningEffort: "low",
		Verbosity:       "low",
		MaxOutputTokens: 400,
	})
	if err != nil {
		// Degrade to no supersession: the new decisions go active as normal. A
		// missed reversal is recoverable by the manual supersede door.
		log.Errorf("%s reversal adjudication failed (%d pair(s), no supersession proposed): %v", decisionLedgerAgentName, len(pairs), err)
		return nil
	}
	output, ok := parseLedgerAdjudication(text)
	if !ok {
		recordEvalEvent(seatDecisionLedger, evalKindParseFailure, map[string]any{"seat": seatDecisionLedger, "model": model})
		log.Errorf("%s reversal adjudication returned non-JSON; no supersession proposed", decisionLedgerAgentName)
		return nil
	}

	targets := map[int]string{}
	for _, verdict := range output.Verdicts {
		if verdict.Verdict != ledgerVerdictSupersedes {
			continue
		}
		if verdict.I >= 0 && verdict.I < len(pairs) {
			targets[pairs[verdict.I].decisionIndex] = pairs[verdict.I].candidateID
		}
	}

	return targets
}

func (app *kanbanBoardApp) produceDecisionLedgerPass(ctx context.Context, apiKey string, inputs []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error) {
	model := meetingBrainModel()
	text, err := responder(ctx, apiKey, openAITextRequest{
		Model:        model,
		Seat:         seatDecisionLedger,
		Instructions: decisionLedgerInstructions(),
		Input:        app.buildDecisionLedgerInput(inputs, time.Now().UTC()),
		// Effort raise to the doctrine floor (medium): the ledger judges firm vs
		// directional commitment and emits exact-shape JSON — the discrimination
		// low reasoning effort punishes most. Mirrors the board worker's raise.
		ReasoningEffort: "medium",
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
		recordEvalEvent(seatDecisionLedger, evalKindParseFailure, map[string]any{"seat": seatDecisionLedger, "model": model})
		log.Errorf("%s returned non-JSON output; skipping this pass", decisionLedgerAgentName)
		return meetingMemoryEntry{}, nil
	}

	firstBrain := inputs[0]
	lastBrain := inputs[len(inputs)-1]

	// Server-layer dedupe (A5): a FIRM decision dedupes only against the newest
	// ACTIVE decisions — so a directional lean that hardens into a firm decision
	// is NOT blocked by its own earlier proposed row (a genuine upgrade). A
	// DIRECTIONAL lean dedupes against active AND proposed, so it is neither
	// re-proposed every pass nor proposed for something already firm. Exact key
	// match or token-set Jaccard >= 0.8 both mean "restatement, skip".
	activeEntries := app.activeDecisionEntries(decisionDedupeWindow)
	activeKeys := dedupeKeysFor(activeEntries)
	proposedKeys := dedupeKeysFor(app.proposedDecisionEntries(decisionDedupeWindow))
	// F16: pending reversals (proposed-supersession) are firm decisions held out
	// of the active lane. Their keys gate BOTH the extraction append below and the
	// reversal adjudication so a restated reversal is filed exactly ONCE.
	pendingReversalKeys := dedupeKeysFor(app.proposedSupersessionEntries(decisionDedupeWindow))

	// item 2.2b: before appending, adjudicate which new FIRM decisions REVERSE an
	// existing active decision (the reversal-suspect band, one batched call). A
	// reversal is held as proposed-supersession for one-click human ratification —
	// NEVER auto-superseded — so the store never co-pins two contradictory
	// decisions and a false positive stays fully recoverable.
	reversalTargets := app.detectDecisionReversals(ctx, apiKey, responder, extraction.Decisions, activeEntries, activeKeys, pendingReversalKeys)

	appendedCount := 0
	directionalCount := 0
	for decisionIndex, decision := range extraction.Decisions {
		statement := normalizeMemoryText(decision.Statement)
		if statement == "" {
			continue
		}
		key := decisionDedupeKey(statement)
		if key == "" {
			continue
		}
		if decisionKeyDuplicate(key, activeKeys) {
			continue
		}
		if decision.Directional && decisionKeyDuplicate(key, proposedKeys) {
			continue
		}
		// A FIRM restatement of an already-pending reversal is skipped: the pending
		// proposed-supersession row already awaits ratification, so re-filing it —
		// as another pending row OR (on adjudication failure) as an active decision
		// — would duplicate it (F16).
		if !decision.Directional && decisionKeyDuplicate(key, pendingReversalKeys) {
			continue
		}

		status := decisionStatusActive
		supersedesID := ""
		if decision.Directional {
			status = decisionStatusProposed
		} else if target := reversalTargets[decisionIndex]; target != "" {
			// adjudged to reverse an existing active decision: held for ratification,
			// NEVER auto-superseded (a missed supersession is recoverable, a false
			// one is not).
			status = decisionStatusProposedSupersession
			supersedesID = target
		}
		// Unknown names are blanked, never invented into the roster.
		madeBy := normalizeTranscriptSpeaker(decision.MadeBy)
		metadata := map[string]string{
			"madeBy":        madeBy,
			"context":       normalizeMemoryText(decision.Context),
			"sourceBrainId": lastBrain.ID,
			"dedupeKey":     key,
			"status":        status,
			"roomId":        ambientWindowRoomID(inputs),
		}
		if supersedesID != "" {
			metadata["supersedes"] = supersedesID
		}
		// card 081: stamp the decision with the meeting its source brain write-up
		// covered rather than whatever meeting is current when this pass fires up
		// to 5 min later (defaultDecisionLedgerInterval). The append-time stamp
		// (memory.go appendEntryForMeeting) stays the fallback when the brain
		// carries no meetingId; this also lines the row up with the Memory-tool
		// meeting card meetingMemoryDetails groups decisions into by this stamp.
		if meetingID := strings.TrimSpace(lastBrain.Metadata["meetingId"]); meetingID != "" {
			metadata["meetingId"] = meetingID
		}
		id := durableTimestampID("decision", time.Now())
		entry, appended, err := app.memory.appendDecision(id, statement, metadata)
		if err != nil {
			return meetingMemoryEntry{}, err
		}
		if !appended {
			continue
		}
		// Track the new key in the right lane so a later item in the SAME pass
		// dedupes correctly: a firm row lands in the active lane (blocks a repeat
		// firm AND a redundant directional), a directional row lands only in the
		// proposed lane (blocks a repeat directional but still lets it upgrade to
		// firm later), and a pending reversal lands in the pending-reversal lane
		// (blocks a repeat reversal in the same window without pinning it active).
		switch {
		case decision.Directional:
			proposedKeys = append(proposedKeys, key)
			directionalCount++
		case status == decisionStatusProposedSupersession:
			pendingReversalKeys = append(pendingReversalKeys, key)
		default:
			activeKeys = append(activeKeys, key)
		}
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
		"source":           "openai_responses",
		"model":            model,
		"roomId":           ambientWindowRoomID(inputs),
		"fromBrainId":      firstBrain.ID,
		"throughBrainId":   lastBrain.ID,
		"brainCount":       strconv.Itoa(len(inputs)),
		"decisionCount":    strconv.Itoa(appendedCount),
		"directionalCount": strconv.Itoa(directionalCount),
	}
	passEntry, _, err := app.memory.appendDecisionPass(durableTimestampID("decision-pass", time.Now()), passText, passMetadata)
	if err != nil {
		return meetingMemoryEntry{}, err
	}

	return passEntry, nil
}
