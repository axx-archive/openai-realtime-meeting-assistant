package main

// Company digest (Track-2 Wave 4, amendment A2) — T4 is STATE, not a recursive
// summary. companyDigestWorker never re-summarizes day/meeting digests (the
// recursive-merging failure mode: hallucination amplifies, detail drops).
// Instead each company_digest is:
//
//   state      the entity ledger's CURRENT view (open decisions, live action
//              items, running topics, unanswered questions), computed
//              DETERMINISTICALLY in Go from the ledger fold
//              (ledgerCurrentStateView) — load-bearing facts are records you
//              edit, never re-summarize;
//   narrative  a THIN running prose paragraph refreshed from the LEDGER DELTAS
//              (the unconsumed kind=ledger_event window) with the previous
//              narrative carried for continuity — the pass's single model call
//              (amendment A8 budget discipline).
//
// The agent rides the generic ambient framework (agent_runner.go) with
// inputKind=ledger_event: it only ever wakes when the ledger actually changed,
// so an idle company never spends a token re-folding an unchanged state. The
// digest lands via upsertDigest under the fixed companyDigestKey (latest-only,
// supersede-in-place, mint-free — no meetingId is ever stamped), which keeps
// exactly one current company_digest recall-eligible (latestCompanyDigest) for
// the no-time-range "what's going on" lane. On a model error nothing is
// upserted and the cursor stays put, so the same delta window re-feeds and the
// prior digest stays current (the mission-intel precedent). Backfill ships OFF
// by default (COMPANY_DIGEST_BACKFILL falsy → baseline at the newest pre-boot
// ledger event).

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

const (
	companyDigestAgentName       = "company digest"
	defaultCompanyDigestInterval = 30 * time.Minute
	companyDigestRequestTimeout  = 60 * time.Second
	// companyDigestCursorMetadataKey rides every upserted company_digest; the
	// runner reads it off the newest one to resume after the consumed
	// ledger_event window (agent_runner.go unconsumedEntriesAfter).
	companyDigestCursorMetadataKey = "throughLedgerEventId"
	companyDigestMaxOutputTokens   = 450
	// companyDigestNarrativeLimit bounds the stored narrative: THIN is the
	// contract (amendment A2) — a few sentences, never a report.
	companyDigestNarrativeLimit = 1600
	// companyDigestSectionCap bounds each state section so a whole digest stays
	// small enough to ride recall context whole (digest kinds are prompt-cap
	// exempt, so the producer must bound what the cap will not).
	companyDigestSectionCap = 12
	// companyDigestStatePromptCap bounds how many records per section ride the
	// NARRATIVE prompt as context (the stored state carries the full sections).
	companyDigestStatePromptCap = 6
	// companyDigestMeetingRefCap bounds per-record meeting provenance in the
	// stored state (drill-down: meetingId → latestDigestPerMeeting).
	companyDigestMeetingRefCap = 3
	companyDigestSource        = "ledger_state_view"
)

func companyDigestAgent() ambientAgentConfig {
	return ambientAgentConfig{
		name:              companyDigestAgentName,
		defaultInterval:   defaultCompanyDigestInterval,
		intervalEnv:       "COMPANY_DIGEST_INTERVAL",
		disabledEnv:       "COMPANY_DIGEST_DISABLED",
		backfillEnv:       "COMPANY_DIGEST_BACKFILL",
		minBatchEnv:       "COMPANY_DIGEST_MIN_INPUTS",
		defaultMinBatch:   1,
		maxBatchEnv:       "COMPANY_DIGEST_MAX_INPUTS",
		defaultMaxBatch:   32,
		inputKind:         meetingMemoryKindLedgerEvent,
		artifactKind:      meetingMemoryKindCompanyDigest,
		cursorMetadataKey: companyDigestCursorMetadataKey,
		requestTimeout:    companyDigestRequestTimeout,
		produce:           (*kanbanBoardApp).produceCompanyDigest,
	}
}

func (app *kanbanBoardApp) startCompanyDigestWorker(apiKey string) {
	app.startAmbientAgent(companyDigestAgent(), apiKey)
}

/* ---------- T4 payload: ledger state + thin narrative ---------- */

// companyDigestRecord is the compact recall projection of one ledgerRecord:
// enough to answer "what's open and who owns it" plus one anchor and the
// source meetings for drill-down. The authoritative full record stays in the
// ledger fold (ledgerState / ledgerCurrentStateView).
type companyDigestRecord struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Status     string   `json:"status"`
	Owner      string   `json:"owner,omitempty"`
	Importance int      `json:"importance,omitempty"`
	Since      string   `json:"since,omitempty"`
	Anchor     string   `json:"anchor,omitempty"`
	Meetings   []string `json:"meetings,omitempty"`
}

// companyDigestState mirrors ledgerStateView's sections (amendment A2's "open
// decisions, active blockers, running themes" in this codebase's four entity
// classes), current records only, importance-ranked by the view.
type companyDigestState struct {
	Decisions     []companyDigestRecord `json:"decisions,omitempty"`
	ActionItems   []companyDigestRecord `json:"actionItems,omitempty"`
	Topics        []companyDigestRecord `json:"topics,omitempty"`
	OpenQuestions []companyDigestRecord `json:"openQuestions,omitempty"`
}

type companyDigestPayload struct {
	GeneratedAt string             `json:"generatedAt"`
	State       companyDigestState `json:"state"`
	Narrative   string             `json:"narrative,omitempty"`
}

func companyDigestRecordFromLedger(record ledgerRecord) companyDigestRecord {
	compact := companyDigestRecord{
		ID:         record.ID,
		Title:      record.Title,
		Status:     record.Status,
		Owner:      record.Owner,
		Importance: record.Importance,
		Since:      record.ValidFrom,
	}
	if len(record.Anchors) > 0 {
		compact.Anchor = record.Anchors[0]
	}
	if len(record.MeetingIDs) > 0 {
		meetings := record.MeetingIDs
		if len(meetings) > companyDigestMeetingRefCap {
			// newest provenance survives appendUniqueCapped's oldest-drop, so
			// keep the tail here too.
			meetings = meetings[len(meetings)-companyDigestMeetingRefCap:]
		}
		compact.Meetings = append([]string(nil), meetings...)
	}

	return compact
}

func companyDigestStateFromView(view ledgerStateView) companyDigestState {
	project := func(records []ledgerRecord) []companyDigestRecord {
		if len(records) == 0 {
			return nil
		}
		projected := make([]companyDigestRecord, 0, len(records))
		for _, record := range records {
			projected = append(projected, companyDigestRecordFromLedger(record))
		}
		return projected
	}

	return companyDigestState{
		Decisions:     project(view.Decisions),
		ActionItems:   project(view.ActionItems),
		Topics:        project(view.Topics),
		OpenQuestions: project(view.OpenQuestions),
	}
}

// parseCompanyDigest validates a stored company_digest body with the same
// stray-markdown-fence tolerance as the other strict-JSON kinds.
func parseCompanyDigest(text string) (companyDigestPayload, bool) {
	text = strings.TrimSpace(text)
	if fenced := strings.TrimPrefix(text, "```json"); fenced != text {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fenced), "```"))
	} else if fenced := strings.TrimPrefix(text, "```"); fenced != text {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fenced), "```"))
	}
	var payload companyDigestPayload
	if text == "" || json.Unmarshal([]byte(text), &payload) != nil {
		return companyDigestPayload{}, false
	}

	return payload, true
}

/* ---------- narrative (the pass's one model call) ---------- */

// describeLedgerDelta renders one ledger_event as a compact prompt line. The
// event payload already carries trimmed, bounded fields (entity_ledger.go), so
// the delta window stays small by construction.
func describeLedgerDelta(entry meetingMemoryEntry) (string, bool) {
	var event ledgerEventPayload
	if json.Unmarshal([]byte(entry.Text), &event) != nil || strings.TrimSpace(event.Record.Title) == "" {
		return "", false
	}
	var builder strings.Builder
	builder.WriteString("- ")
	builder.WriteString(event.Op)
	builder.WriteString(" ")
	builder.WriteString(event.Record.Entity)
	builder.WriteString(": ")
	builder.WriteString(event.Record.Title)
	if status := strings.TrimSpace(event.Record.Status); status != "" {
		builder.WriteString(" | status=")
		builder.WriteString(status)
	}
	if owner := strings.TrimSpace(event.Record.Owner); owner != "" {
		builder.WriteString(" | owner=")
		builder.WriteString(owner)
	}
	if event.Record.Importance > 0 {
		builder.WriteString(" | importance=")
		builder.WriteString(strconv.Itoa(event.Record.Importance))
	}
	if reason := strings.TrimSpace(event.Reason); reason != "" {
		builder.WriteString(" | ")
		builder.WriteString(reason)
	}

	return builder.String(), true
}

func companyDigestInstructions() string {
	return strings.Join([]string{
		"You are Bonfire's company digest narrator.",
		"The company's state is maintained as ledger RECORDS (supplied below, already consolidated and ranked); records carry the facts — you NEVER restate, enumerate, or re-summarize them, and you never summarize other summaries.",
		"Write a THIN running narrative — 2 to 6 plain sentences — of where the company stands and what just changed, folding in the ledger deltas: decisions taken or reversed, blockers opened or cleared, questions answered, ownership shifts, momentum.",
		"Refresh the previous narrative rather than rewriting history: keep what is still true, drop what resolved, weave in the deltas.",
		"Owner/speaker attribution upstream is a heuristic and can be wrong: hedge ('attributed to X'), never assert it as certain.",
		"Never invent facts, people, clients, decisions, or dates.",
		"Plain prose only — no markdown, no headers, no lists.",
	}, " ")
}

func buildCompanyDigestInput(state companyDigestState, deltas []string, priorNarrative string, generatedAt time.Time) string {
	var builder strings.Builder
	builder.WriteString("# Generated at\n")
	builder.WriteString(generatedAt.Format(time.RFC3339))

	if priorNarrative != "" {
		builder.WriteString("\n\n# Previous narrative (refresh — keep what still holds, fold in the deltas)\n")
		builder.WriteString(priorNarrative)
	}

	builder.WriteString("\n\n# Ledger deltas since the previous company digest (oldest first)\n")
	if len(deltas) == 0 {
		builder.WriteString("(none)\n")
	}
	for _, delta := range deltas {
		builder.WriteString(delta)
		builder.WriteByte('\n')
	}

	builder.WriteString("\n# Current company state (top records per section, already ranked — context only, never restate)\n")
	writeSection := func(label string, records []companyDigestRecord) {
		if len(records) == 0 {
			return
		}
		builder.WriteString(label)
		builder.WriteByte('\n')
		capped := records
		if len(capped) > companyDigestStatePromptCap {
			capped = capped[:companyDigestStatePromptCap]
		}
		for _, record := range capped {
			builder.WriteString("- [")
			builder.WriteString(record.Status)
			builder.WriteString("] ")
			builder.WriteString(record.Title)
			if record.Owner != "" {
				builder.WriteString(" (owner ")
				builder.WriteString(record.Owner)
				builder.WriteString(")")
			}
			builder.WriteByte('\n')
		}
	}
	writeSection("decisions:", state.Decisions)
	writeSection("actionItems:", state.ActionItems)
	writeSection("topics:", state.Topics)
	writeSection("openQuestions:", state.OpenQuestions)

	return builder.String()
}

/* ---------- the pass ---------- */

// produceCompanyDigest is the company-digest agent's pass body; the wall clock
// is injected via runCompanyDigestPass so tests pin stamps.
func (app *kanbanBoardApp) produceCompanyDigest(ctx context.Context, apiKey string, inputs []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error) {
	return app.runCompanyDigestPass(ctx, apiKey, inputs, responder, time.Now().UTC())
}

// runCompanyDigestPass: (1) project the ledger's CURRENT state view in Go —
// the deterministic, always-faithful half of T4; (2) spend the pass's single
// model call on the THIN narrative over the unconsumed ledger deltas with the
// prior narrative carried; (3) upsert the (state + narrative) JSON under the
// fixed company key, stamping the delta cursor. A model ERROR upserts nothing
// (cursor stays, window re-feeds, prior digest stays current); an EMPTY model
// output must not block the state refresh — the prior narrative is carried and
// the fresh state still lands.
func (app *kanbanBoardApp) runCompanyDigestPass(ctx context.Context, apiKey string, inputs []meetingMemoryEntry, responder openAITextResponder, now time.Time) (meetingMemoryEntry, error) {
	if app == nil || app.memory == nil || len(inputs) == 0 {
		// the runner's minBatch gate makes this unreachable on the ticker path;
		// direct callers (boundary flushes) get a safe no-op.
		return meetingMemoryEntry{}, nil
	}
	if responder == nil {
		responder = createOpenAITextResponse
	}

	state := companyDigestStateFromView(app.ledgerCurrentStateView(companyDigestSectionCap))

	priorNarrative := ""
	if prior, ok := app.memory.latestCompanyDigest(); ok {
		if payload, parsed := parseCompanyDigest(prior.Text); parsed {
			priorNarrative = payload.Narrative
		}
	}

	deltas := make([]string, 0, len(inputs))
	for _, input := range inputs {
		// §6.4 (RATIFIED 2026-07-09): listen-only-sitting deltas flow into the
		// company narrative like any other material — external-meeting memory
		// must be Scout-recallable company-wide. Provenance stays on the
		// upstream stamps; re-quarantining is a read-side filter on them.
		if line, ok := describeLedgerDelta(input); ok {
			deltas = append(deltas, line)
		}
	}

	model := meetingBrainModel()
	text, err := responder(ctx, apiKey, openAITextRequest{
		Model:           model,
		Seat:            seatCompanyDigest,
		Instructions:    companyDigestInstructions(),
		Input:           buildCompanyDigestInput(state, deltas, priorNarrative, now.UTC()),
		ReasoningEffort: "low",
		Verbosity:       "low",
		MaxOutputTokens: companyDigestMaxOutputTokens,
	})
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	narrative := trimForStorage(strings.TrimSpace(text), companyDigestNarrativeLimit)
	// W0 item 6 structural checks, evaluated BEFORE the prior-narrative
	// fallback so an empty model pass is visible even though the artifact
	// self-heals; state_nonempty covers the deterministic ledger fold.
	recordEvalEvent(seatCompanyDigest, evalKindDigestStructure, map[string]any{
		"check": "narrative_present", "pass": narrative != "",
	})
	recordEvalEvent(seatCompanyDigest, evalKindDigestStructure, map[string]any{
		"check": "state_nonempty",
		"pass": len(state.Decisions) > 0 || len(state.ActionItems) > 0 ||
			len(state.Topics) > 0 || len(state.OpenQuestions) > 0,
	})
	if narrative == "" {
		narrative = priorNarrative
	}

	payload := companyDigestPayload{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		State:       state,
		Narrative:   narrative,
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	metadata := map[string]string{
		"source":                       companyDigestSource,
		"model":                        model,
		companyDigestCursorMetadataKey: inputs[len(inputs)-1].ID,
		"eventCount":                   strconv.Itoa(len(inputs)),
		"generatedAt":                  now.UTC().Format(time.RFC3339),
	}

	return app.memory.upsertDigest(meetingMemoryKindCompanyDigest, companyDigestKey, string(canonical), metadata)
}
