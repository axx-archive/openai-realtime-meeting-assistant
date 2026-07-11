package main

// Entity ledger (Track-2 Wave 3, amendment A1) — the compounding substrate: a
// canonical, deduplicated, entity-keyed registry of decisions, action items,
// topics, and open questions consolidated ACROSS meetings.
//
// Storage model (event sourcing over the existing JSONL — no new datastore):
// the append-only kind=ledger_event entries are the source of truth; each one
// carries an op (add/update/supersede/close) plus the FULL post-state of one
// ledgerRecord, so the materialized read-model is derived by folding the
// event log in order (ledgerState) and is rebuildable from scratch at any
// time. Contradictions CLOSE a record's validity window (valid_to +
// supersededBy) — history is never deleted.
//
// Consolidation runs as meeting digests land: an ambient agent
// (agent_runner.go framework) consumes kind=meeting_digest as its trigger
// window, reads the CURRENT digest per affected meeting (superseded inputs
// self-heal, the day-digest doctrine), sweeps newly appended kind=decision
// rows as a second fold source (amendment A9 — the ledger is the consolidated
// view OVER the existing decision log, never a second decision concept), and
// matches every extracted fact against the folded state: DETERMINISTIC first
// (normalized-title token overlap, the decision-dedupe precedent), one
// batched LLM adjudication call per pass ONLY for the genuinely ambiguous
// band (amendment A8 budget discipline). Writes are single-writer: the
// per-agent run lock serializes passes and appendLedgerEvents lands a whole
// pass's events in one store-mutex critical section.
//
// Read surfaces for later waves: ledgerCurrentStateView (amendment A2's T4
// company state view + A5 current-state recall routing) and
// searchLedgerRecords (A5 "status of X" O(lookup) with drill-down anchors).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	entityLedgerAgentName       = "entity ledger"
	defaultEntityLedgerInterval = 5 * time.Minute
	entityLedgerRequestTimeout  = 90 * time.Second
	// entityLedgerCursorMetadataKey rides every ledger_pass artifact; the
	// runner reads it off the newest pass to resume after the consumed
	// meeting_digest window.
	entityLedgerCursorMetadataKey = "throughMeetingDigestId"
	// entityLedgerDecisionCursorMetadataKey is the second, self-managed cursor:
	// the newest kind=decision entry the pass swept (amendment A9 fold source).
	// Carried forward on every pass so it survives decision-free ticks.
	entityLedgerDecisionCursorMetadataKey = "throughDecisionId"
	// entityLedgerRefeedMetadataKey flags a kind=decision row (behind the sweep
	// cursor) whose status changed AFTER it was consumed and must be re-folded
	// (F20): a ratified reversal flips its own row active and supersedes another,
	// but the position-based cursor already advanced past both, so neither reaches
	// the ledger through the normal decision lane. markDecisionRatified stamps this
	// marker; unconsumedDecisionEntriesForLedger re-includes the flagged rows and
	// the pass clears the marker once it has folded them.
	entityLedgerRefeedMetadataKey = "ledgerRefeed"
	// entityLedgerDecisionSweepCap bounds the decision rows folded per pass;
	// the cursor defers the rest to the next tick, never drops them.
	entityLedgerDecisionSweepCap = 16
	// entityLedgerAdjudicationPairCap bounds the single batched adjudication
	// call; overflow pairs fall back to ADD (conservative: a duplicate record
	// is recoverable by a later supersede, a silent false merge is not).
	entityLedgerAdjudicationPairCap = 16
	entityLedgerMaxOutputTokens     = 800

	// Matching thresholds — deterministic first, LLM only for the middle band.
	// Strong = same entity (the decisionDedupeJaccard 0.8 precedent, plus a
	// containment test so "draft pricing sheet" matches "draft the pricing
	// sheet for Zebra"); below ambiguous = a new entity.
	ledgerStrongMatchJaccard    = 0.8
	ledgerStrongContainment     = 0.9
	ledgerAmbiguousMatchJaccard = 0.4

	ledgerAnchorCap    = 12
	ledgerMeetingIDCap = 12
	ledgerTitleLimit   = 240
	ledgerOwnerLimit   = 60

	// ledgerAliasCap bounds the accumulated alias phrasings stored on a record
	// (item 1.3a) — the union of every digest's aliases the record folded, used
	// to catch a renamed entity before the <0.4-Jaccard band mints a duplicate.
	ledgerAliasCap = 8
	// ledgerPastOwnersCap bounds the ownership-evolution trail (item 2.3a): when
	// owner drifts newest-wins, the prior owner is retained here so the evolution
	// lane can read who held a record before the current owner.
	ledgerPastOwnersCap = 6
	// ledgerProvenanceOverflowCap bounds the spill list that catches meetingIds /
	// anchors evicted off the primary cap (item Q6, spill-never-shed): the fold
	// and prompt paths keep using the primary list (no prompt-size change), but a
	// month-long arc's origin meetings survive on the record for the evolution
	// lane and future drill-downs instead of being lost to the oldest-drop.
	ledgerProvenanceOverflowCap = 48
)

// Ledger entity kinds — the four fact classes the T2 digest schema extracts,
// plus `position` (item 2.2): a keyed (Owner + topic) stance record fed from
// directional leans, so "what does <person> think about <topic>" is O(lookup).
const (
	ledgerEntityDecision     = "decision"
	ledgerEntityActionItem   = "action_item"
	ledgerEntityTopic        = "topic"
	ledgerEntityOpenQuestion = "open_question"
	ledgerEntityPosition     = "position"
)

// Consolidation ops (the mem0 pattern).
const (
	ledgerOpAdd       = "add"
	ledgerOpUpdate    = "update"
	ledgerOpSupersede = "supersede"
	ledgerOpClose     = "close"
)

// Canonical record statuses. Facts arrive with free-text model statuses;
// normalizeLedgerStatus maps them onto this small vocabulary so restatements
// never churn UPDATE events.
const (
	ledgerStatusOpen       = "open"
	ledgerStatusActive     = "active"
	ledgerStatusInProgress = "in_progress"
	ledgerStatusDone       = "done"
	ledgerStatusClosed     = "closed"
	ledgerStatusAnswered   = "answered"
	ledgerStatusSuperseded = "superseded"
)

// ledgerRecord is one canonical entity: stable ID, temporal validity window
// (ValidTo empty = current), provenance anchors (transcript/decision entry
// ids), source meetings, and amendment A4's importance for briefing ranking.
type ledgerRecord struct {
	ID           string   `json:"id"`
	Entity       string   `json:"entity"`
	Title        string   `json:"title"`
	Status       string   `json:"status"`
	Owner        string   `json:"owner,omitempty"`
	ValidFrom    string   `json:"validFrom"`
	ValidTo      string   `json:"validTo,omitempty"`
	SupersededBy string   `json:"supersededBy,omitempty"`
	Anchors      []string `json:"anchors,omitempty"`
	MeetingIDs   []string `json:"meetingIds,omitempty"`
	Importance   int      `json:"importance,omitempty"`
	UpdatedAt    string   `json:"updatedAt,omitempty"`
	// Aliases (item 1.3a) is the accumulated set of alternate phrasings this
	// record has folded from digest aliases — matched against a new fact's title
	// so a renamed entity consolidates instead of forking a duplicate.
	Aliases []string `json:"aliases,omitempty"`
	// PastOwners (item 2.3a) is the capped ownership-evolution trail: prior
	// owners displaced by newest-wins drift, oldest-first, so recall can read
	// who held the record before the current owner.
	PastOwners []string `json:"pastOwners,omitempty"`
	// MeetingIDsOverflow / AnchorsOverflow (item Q6) catch provenance evicted
	// off the primary caps — spill-never-shed. Never rendered into a prompt; a
	// durable tail for the evolution lane and future drill-downs.
	MeetingIDsOverflow []string `json:"meetingIdsOverflow,omitempty"`
	AnchorsOverflow    []string `json:"anchorsOverflow,omitempty"`
}

// current reports whether the record's validity window is still open.
func (record ledgerRecord) current() bool {
	return strings.TrimSpace(record.ValidTo) == ""
}

// ledgerEventPayload is one ledger_event body: the op plus the full
// post-state, so the fold is a trivial last-event-wins per record id and a
// partially-applied pass can never corrupt the read-model.
type ledgerEventPayload struct {
	Op     string       `json:"op"`
	Record ledgerRecord `json:"record"`
	Reason string       `json:"reason,omitempty"`
	At     string       `json:"at"`
}

/* ---------- agent wiring ---------- */

func entityLedgerAgent() ambientAgentConfig {
	return ambientAgentConfig{
		name:              entityLedgerAgentName,
		defaultInterval:   defaultEntityLedgerInterval,
		intervalEnv:       "ENTITY_LEDGER_INTERVAL",
		disabledEnv:       "ENTITY_LEDGER_DISABLED",
		backfillEnv:       "ENTITY_LEDGER_BACKFILL",
		minBatchEnv:       "ENTITY_LEDGER_MIN_INPUTS",
		defaultMinBatch:   1,
		maxBatchEnv:       "ENTITY_LEDGER_MAX_INPUTS",
		defaultMaxBatch:   8,
		inputKind:         meetingMemoryKindMeetingDigest,
		artifactKind:      meetingMemoryKindLedgerPass,
		cursorMetadataKey: entityLedgerCursorMetadataKey,
		requestTimeout:    entityLedgerRequestTimeout,
		produce:           (*kanbanBoardApp).produceLedgerConsolidationPass,
	}
}

func (app *kanbanBoardApp) startEntityLedgerWorker(apiKey string) {
	app.startAmbientAgent(entityLedgerAgent(), apiKey)
}

/* ---------- store layer: event log + fold ---------- */

// appendLedgerEvents lands one consolidation pass's events in ONE store-mutex
// critical section and one file write, so the batch is the single-writer unit:
// a concurrent snapshot/fold never observes a half-applied pass in RAM, and
// the RAM slice only advances when the bytes are on disk. Entries must be
// kind=ledger_event; already-seen ids are skipped (idempotent replay guard).
// Mint-free by design: no meetingId is stamped — a ledger record spans
// meetings, and stamping the live id would leak cross-meeting bookkeeping
// into snapshotForMeeting (the appendAmbientEntry precedent).
func (store *meetingMemoryStore) appendLedgerEvents(entries []meetingMemoryEntry) (int, error) {
	if store == nil {
		return 0, fmt.Errorf("memory store is unavailable")
	}
	if len(entries) == 0 {
		return 0, nil
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	var buffer []byte
	accepted := make([]meetingMemoryEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Kind != meetingMemoryKindLedgerEvent {
			return 0, fmt.Errorf("appendLedgerEvents: %q is not a ledger event kind", entry.Kind)
		}
		entry.ID = strings.TrimSpace(entry.ID)
		entry.Text = normalizeMemoryEntryText(entry.Kind, entry.Text)
		if entry.ID == "" || entry.Text == "" {
			return 0, fmt.Errorf("appendLedgerEvents: event id and text are required")
		}
		if entry.CreatedAt.IsZero() {
			entry.CreatedAt = time.Now().UTC()
		}
		if _, ok := store.seen[entry.ID]; ok {
			continue
		}
		raw, err := json.Marshal(entry)
		if err != nil {
			return 0, fmt.Errorf("encode ledger event: %w", err)
		}
		buffer = append(buffer, raw...)
		buffer = append(buffer, '\n')
		accepted = append(accepted, entry)
	}
	if len(accepted) == 0 {
		return 0, nil
	}

	file, err := os.OpenFile(store.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, fmt.Errorf("open memory file: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(buffer); err != nil {
		return 0, fmt.Errorf("write ledger events: %w", err)
	}

	for _, entry := range accepted {
		store.entries = append(store.entries, entry)
		store.seen[entry.ID] = struct{}{}
	}

	return len(accepted), nil
}

// ledgerState folds the whole ledger_event log (log order) into the
// entity-keyed read-model. Every event carries the full post-state, so the
// fold is last-event-wins per record id — a from-scratch rebuild by
// construction (the amendment A1 event-sourcing contract). Malformed lines
// are skipped, never fatal.
func (store *meetingMemoryStore) ledgerState() map[string]ledgerRecord {
	if store == nil {
		return map[string]ledgerRecord{}
	}

	records := map[string]ledgerRecord{}
	for _, entry := range store.entriesOfKind(meetingMemoryKindLedgerEvent, 0) {
		var event ledgerEventPayload
		if json.Unmarshal([]byte(entry.Text), &event) != nil {
			continue
		}
		id := strings.TrimSpace(event.Record.ID)
		if id == "" {
			continue
		}
		event.Record.ID = id
		records[id] = event.Record
	}

	return records
}

/* ---------- fact extraction (fold sources) ---------- */

// ledgerFact is one candidate fact headed for consolidation, extracted from a
// current meeting_digest (T2 schema) or a kind=decision ledger row (A9).
type ledgerFact struct {
	Entity     string
	Title      string
	Status     string
	Owner      string
	Anchor     string // transcript event id (digests) or decision entry id — STABLE ids only, so re-consolidating a rebuilt digest stays a no-op
	At         string
	MeetingID  string
	Importance int
	// Aliases (item 1.3a) rides in from the source digest's model-written alias
	// phrasings; carried onto the record so a later renamed restatement matches.
	Aliases []string
}

// ledgerFactsFromDigest flattens a meeting_digest's four fact sections. The
// digest entry's own id is deliberately NOT used as an anchor: a cumulative
// digest re-mints its entry id on every rebuild, and an unstable anchor would
// mark every record changed each tick (the digest is reachable through
// MeetingIDs → latestDigestPerMeeting instead).
func ledgerFactsFromDigest(entry meetingMemoryEntry) []ledgerFact {
	payload, ok := parseMeetingDigest(entry.Text)
	if !ok {
		return nil
	}
	meetingID := digestEntryKey(entry)
	// item 1.3a: the digest's aliases describe this meeting's storyline, so they
	// ride onto the storyline-shaped facts (topics + decisions) — the records a
	// rename would fork. F17: they are gated PER-FACT (aliasesForFact) rather than
	// stamped onto every one, so an unrelated sibling decision never inherits the
	// storyline's aliases and gets dragged into the alias-bridge adjudication band.
	// A lone topic unambiguously IS the storyline, so it always carries them.
	aliases := clampDigestAliases(payload.Aliases)
	soleTopic := len(payload.Topics) == 1
	facts := make([]ledgerFact, 0, len(payload.Decisions)+len(payload.ActionItems)+len(payload.Topics)+len(payload.OpenQuestions))
	for _, decision := range payload.Decisions {
		facts = append(facts, ledgerFact{
			Entity:     ledgerEntityDecision,
			Title:      decision.D,
			Status:     decision.Status,
			Owner:      decision.By,
			Anchor:     decision.Anchor,
			At:         decision.At,
			MeetingID:  meetingID,
			Importance: clampImportance(decision.Importance),
			Aliases:    aliasesForFact(aliases, decision.D, false),
		})
	}
	for _, action := range payload.ActionItems {
		facts = append(facts, ledgerFact{
			Entity:     ledgerEntityActionItem,
			Title:      action.A,
			Status:     action.Status,
			Owner:      action.Owner,
			Anchor:     action.Anchor,
			At:         action.At,
			MeetingID:  meetingID,
			Importance: clampImportance(action.Importance),
		})
	}
	for _, topic := range payload.Topics {
		facts = append(facts, ledgerFact{
			Entity:     ledgerEntityTopic,
			Title:      topic.T,
			Anchor:     topic.Anchor,
			At:         topic.At,
			MeetingID:  meetingID,
			Importance: clampImportance(topic.Importance),
			Aliases:    aliasesForFact(aliases, topic.T, soleTopic),
		})
	}
	for _, question := range payload.OpenQuestions {
		facts = append(facts, ledgerFact{
			Entity:     ledgerEntityOpenQuestion,
			Title:      question.Q,
			Anchor:     question.Anchor,
			At:         question.At,
			MeetingID:  meetingID,
			Importance: clampImportance(question.Importance),
		})
	}

	return facts
}

// aliasesForFact gates digest-level storyline aliases onto ONE fact (item 1.3a /
// F17). An alias attaches only when it plausibly renames THIS fact — it shares a
// normalized title token — or forceAttach is set for the lone topic that
// unambiguously IS the storyline. The record-side accumulated-alias bridge
// (matchLedgerFact / searchLedgerRecords over record.Aliases) is untouched, so a
// genuine rename still consolidates.
func aliasesForFact(aliases []string, title string, forceAttach bool) []string {
	if len(aliases) == 0 {
		return nil
	}
	if forceAttach || aliasesShareTitleToken(aliases, title) {
		return aliases
	}

	return nil
}

// aliasGateStopTokens are common function words that survive the ≥3-char token
// cut and would otherwise let any alias "share a token" with any title ("the"
// appears in both "the Samsung deal" and "approve the hiring budget"). Filtered
// on BOTH sides of the alias-attachment gate so only a DISTINCTIVE shared token
// counts as a plausible rename (F17).
var aliasGateStopTokens = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "our": {}, "its": {}, "with": {},
	"that": {}, "this": {}, "from": {}, "into": {}, "per": {}, "are": {},
	"was": {}, "has": {}, "had": {}, "your": {}, "their": {}, "his": {},
	"her": {}, "any": {}, "all": {}, "not": {}, "but": {},
}

// distinctiveTitleTokens returns a title's normalized tokens with the alias-gate
// stopwords removed.
func distinctiveTitleTokens(title string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, token := range strings.Fields(ledgerTitleKey(title)) {
		if _, stop := aliasGateStopTokens[token]; stop {
			continue
		}
		set[token] = struct{}{}
	}

	return set
}

// aliasesShareTitleToken reports whether any alias phrasing shares a DISTINCTIVE
// (non-stopword) normalized token with the fact title — the cheapest
// plausible-rename signal (F17).
func aliasesShareTitleToken(aliases []string, title string) bool {
	titleSet := distinctiveTitleTokens(title)
	if len(titleSet) == 0 {
		return false
	}
	for _, alias := range aliases {
		for token := range distinctiveTitleTokens(alias) {
			if _, ok := titleSet[token]; ok {
				return true
			}
		}
	}

	return false
}

// ledgerFactFromDecisionEntry adapts one kind=decision row (amendment A9: the
// ledger consolidates OVER the existing decision log). The decision entry id
// itself is the anchor, so every canonical decision record points back at the
// ledger row it folded.
//
// Routing by status (item 2.2a):
//   - active → a canonical DECISION record (the original A9 behavior).
//   - proposed WITH a roster-groundable holder → a POSITION record (Owner =
//     holder): a directional lean IS someone's stance, so "what does <person>
//     think about <topic>" becomes O(lookup). Positions never enter the
//     firm-decision lanes.
//   - proposed WITHOUT a roster-groundable holder → skipped: a floating lean with
//     nobody real attached is not a team position, and a non-roster/system owner
//     (the card-069 governance default's madeBy="Scout") must NEVER mint a bogus
//     position under an ungroundable name (F23).
//   - proposed-supersession → skipped: a proposed reversal awaiting human
//     ratification is not a team decision yet (mirrors the proposed skip).
func ledgerFactFromDecisionEntry(entry meetingMemoryEntry) (ledgerFact, bool) {
	status := firstNonEmptyString(strings.TrimSpace(entry.Metadata["status"]), decisionStatusActive)
	owner := strings.TrimSpace(entry.Metadata["madeBy"])
	meetingID := strings.TrimSpace(entry.Metadata["meetingId"])
	at := entry.CreatedAt.UTC().Format(time.RFC3339)

	switch status {
	case decisionStatusProposedSupersession:
		return ledgerFact{}, false
	case decisionStatusProposed:
		// Roster-ground the holder BEFORE minting — an ungroundable owner is not a
		// team member's stance (F23). normalizeTranscriptSpeaker is the same
		// grounding the extraction pass applies to madeBy, so "Scout" and other
		// non-roster names resolve to "" and are skipped.
		holder := normalizeTranscriptSpeaker(owner)
		if holder == "" {
			return ledgerFact{}, false
		}
		return ledgerFact{
			Entity:     ledgerEntityPosition,
			Title:      entry.Text,
			Status:     ledgerStatusActive,
			Owner:      holder,
			Anchor:     entry.ID,
			At:         at,
			MeetingID:  meetingID,
			Importance: 3, // a stance is real signal but not a firm commitment
		}, true
	}

	return ledgerFact{
		Entity:     ledgerEntityDecision,
		Title:      entry.Text,
		Status:     status,
		Owner:      owner,
		Anchor:     entry.ID,
		At:         at,
		MeetingID:  meetingID,
		Importance: 4, // A4 scale: "4 = a real commitment or decision"
	}, true
}

// unconsumedDecisionEntriesForLedger sweeps kind=decision rows appended after
// the newest ledger_pass's throughDecisionId cursor. Absent any cursor the
// boot baseline applies (backfill-off posture, mirroring the framework);
// ENTITY_LEDGER_BACKFILL folds pre-boot history. Returns the swept rows plus
// the cursor to stamp — the prior cursor when nothing new landed, so it is
// carried forward across decision-free passes. Known limitation (documented):
// in-place status updates on old rows (supersede/ratify) do not re-feed a
// position cursor; status changes reach the ledger through the digest lane's
// carried-forward facts instead.
func (app *kanbanBoardApp) unconsumedDecisionEntriesForLedger(limit int) ([]meetingMemoryEntry, string) {
	if app == nil || app.memory == nil || limit <= 0 {
		return nil, ""
	}

	prior := ""
	passes := app.memory.entriesOfKind(meetingMemoryKindLedgerPass, 0)
	for index := len(passes) - 1; index >= 0; index-- {
		if cursor := strings.TrimSpace(passes[index].Metadata[entityLedgerDecisionCursorMetadataKey]); cursor != "" {
			prior = cursor
			break
		}
	}
	if prior == "" && !boolEnv("ENTITY_LEDGER_BACKFILL") {
		prior = app.memory.bootBaselineIDOfKind(meetingMemoryKindDecision)
	}

	all := app.memory.entriesOfKind(meetingMemoryKindDecision, 0)
	start := 0
	if prior != "" {
		for index := len(all) - 1; index >= 0; index-- {
			if all[index].ID == prior {
				start = index + 1
				break
			}
		}
	}
	swept := all[start:]
	if len(swept) > limit {
		swept = swept[:limit]
	}
	through := prior
	if len(swept) > 0 {
		through = swept[len(swept)-1].ID
	}

	// F20: re-include rows flagged for re-feed that sit BEHIND the cursor (a
	// ratified reversal whose row was swept past while it was skipped). They do
	// NOT advance `through` — the forward cursor keeps moving normally, and the
	// pass clears each flag once it has folded the row. Prepended so the closure
	// of a retired decision is folded before its successor's fresh window opens.
	inSwept := make(map[string]bool, len(swept))
	for _, entry := range swept {
		inSwept[entry.ID] = true
	}
	var refeed []meetingMemoryEntry
	for _, entry := range all[:start] {
		if entry.Metadata[entityLedgerRefeedMetadataKey] == "1" && !inSwept[entry.ID] {
			refeed = append(refeed, entry)
		}
	}
	if len(refeed) > 0 {
		swept = append(refeed, swept...)
	}

	return swept, through
}

/* ---------- deterministic matching ---------- */

// ledgerTitleKey normalizes a title into the comparable token string — the
// decisionDedupeKey pipeline (domain-term canonicalization, ≥3-char unique
// tokens) so the ledger and the decision dedupe agree on what "same" means.
func ledgerTitleKey(title string) string {
	return decisionDedupeKey(title)
}

// tokenSetContainment computes |A∩B| / min(|A|,|B|) over two token slices —
// catches a short canonical title restated with extra qualifiers, which pure
// Jaccard under-scores.
func tokenSetContainment(a []string, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	setA := make(map[string]struct{}, len(a))
	for _, token := range a {
		setA[token] = struct{}{}
	}
	setB := make(map[string]struct{}, len(b))
	for _, token := range b {
		setB[token] = struct{}{}
	}
	intersection := 0
	for token := range setA {
		if _, ok := setB[token]; ok {
			intersection++
		}
	}
	smaller := len(setA)
	if len(setB) < smaller {
		smaller = len(setB)
	}
	if smaller == 0 {
		return 0
	}

	return float64(intersection) / float64(smaller)
}

// aliasAugmentedTokens unions a base title-key token slice with the tokens of
// every alias phrasing (item 1.3a), so alias overlap counts toward a match.
func aliasAugmentedTokens(base []string, aliases []string) []string {
	tokens := append([]string(nil), base...)
	for _, alias := range aliases {
		tokens = append(tokens, strings.Fields(ledgerTitleKey(alias))...)
	}

	return tokens
}

// ledgerAliasBridgeScore is the best containment/Jaccard between a fact and a
// record once BOTH are widened by their alias phrasings (item 1.3a). Containment
// (min-set denominator) is what catches a short renamed restatement fully
// covered by an alias — e.g. fact "korean tv deal" vs a record aliased "the
// Korean TV deal" scores 1.0 even though the raw titles share nothing.
func ledgerAliasBridgeScore(factTokens []string, factAliases []string, recordTokens []string, recordAliases []string) float64 {
	factAug := aliasAugmentedTokens(factTokens, factAliases)
	recordAug := aliasAugmentedTokens(recordTokens, recordAliases)
	score := tokenSetContainment(factAug, recordAug)
	if jaccard := tokenSetJaccard(factAug, recordAug); jaccard > score {
		score = jaccard
	}

	return score
}

const (
	ledgerMatchNone = iota
	ledgerMatchAmbiguous
	ledgerMatchStrong
)

// matchLedgerFact classifies a fact against the working state: strong (same
// entity — consolidate deterministically), ambiguous (defer to the one
// batched adjudication call), or none (a new entity). Same-entity-kind
// records only; a current record beats a closed one at equal class.
func matchLedgerFact(fact ledgerFact, records []ledgerRecord) (ledgerRecord, int) {
	factKey := ledgerTitleKey(fact.Title)
	factTokens := strings.Fields(factKey)
	if len(factTokens) == 0 {
		return ledgerRecord{}, ledgerMatchNone
	}

	factOwner := normalizeLedgerOwner(fact.Owner)
	var best ledgerRecord
	bestClass := ledgerMatchNone
	bestScore := 0.0
	bestCurrent := false
	for _, record := range records {
		if record.Entity != fact.Entity {
			continue
		}
		// item 2.2a: a position is keyed by Owner + topic, so one person's stance
		// never consolidates against another's — only same-owner records compete.
		if fact.Entity == ledgerEntityPosition && normalizeLedgerOwner(record.Owner) != factOwner {
			continue
		}
		recordKey := ledgerTitleKey(record.Title)
		recordTokens := strings.Fields(recordKey)
		if len(recordTokens) == 0 {
			continue
		}
		jaccard := tokenSetJaccard(factTokens, recordTokens)
		containment := tokenSetContainment(factTokens, recordTokens)
		smaller := len(factTokens)
		if len(recordTokens) < smaller {
			smaller = len(recordTokens)
		}
		class := ledgerMatchNone
		score := jaccard
		switch {
		case recordKey == factKey:
			class = ledgerMatchStrong
			score = 1.0
		case jaccard >= ledgerStrongMatchJaccard:
			class = ledgerMatchStrong
		case containment >= ledgerStrongContainment && smaller >= 2:
			class = ledgerMatchStrong
			if containment > score {
				score = containment
			}
		case jaccard >= ledgerAmbiguousMatchJaccard:
			class = ledgerMatchAmbiguous
		default:
			// item 1.3a: the title alone says "different", but a renamed entity
			// (record folded aliases, or the fact carries them) can still match an
			// existing record's alias set. A bridged hit only rises to AMBIGUOUS —
			// adjudication decides same/supersedes/different — never an auto-merge.
			if len(record.Aliases) == 0 && len(fact.Aliases) == 0 {
				continue
			}
			bridge := ledgerAliasBridgeScore(factTokens, fact.Aliases, recordTokens, record.Aliases)
			if bridge < ledgerAmbiguousMatchJaccard {
				continue
			}
			class = ledgerMatchAmbiguous
			score = bridge
		}
		better := class > bestClass ||
			(class == bestClass && record.current() && !bestCurrent) ||
			(class == bestClass && record.current() == bestCurrent && score > bestScore)
		if better {
			best = record
			bestClass = class
			bestScore = score
			bestCurrent = record.current()
		}
	}

	return best, bestClass
}

/* ---------- status + merge semantics ---------- */

// normalizeLedgerStatus maps a free-text fact status onto the canonical
// vocabulary; anything unrecognized (or empty) lands on the entity's default
// open status so model phrasing drift never churns UPDATE events.
func normalizeLedgerStatus(entity string, raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "done", "complete", "completed", "finished", "shipped", "delivered":
		return ledgerStatusDone
	case "closed", "resolved", "cancelled", "canceled", "dropped", "abandoned", "retracted", "won't do", "wont do":
		return ledgerStatusClosed
	case "answered":
		return ledgerStatusAnswered
	case "superseded", "reversed", "replaced", "overturned", "rescinded":
		return ledgerStatusSuperseded
	case "in progress", "in_progress", "in-progress", "started", "underway", "doing":
		return ledgerStatusInProgress
	}

	return defaultLedgerStatus(entity)
}

func defaultLedgerStatus(entity string) string {
	if entity == ledgerEntityDecision || entity == ledgerEntityTopic || entity == ledgerEntityPosition {
		return ledgerStatusActive
	}

	return ledgerStatusOpen
}

// isTerminalLedgerStatus reports the statuses that close a record's validity
// window (contradiction/completion = CLOSE, never delete).
func isTerminalLedgerStatus(status string) bool {
	switch status {
	case ledgerStatusDone, ledgerStatusClosed, ledgerStatusAnswered, ledgerStatusSuperseded:
		return true
	}

	return false
}

// normalizeLedgerOwner bounds an owner/attribution string, unwrapping the
// digest prompt's hedge prefix ("attributed to X" → "X").
func normalizeLedgerOwner(owner string) string {
	owner = normalizeMemoryText(owner)
	if len(owner) >= len("attributed to ") && strings.EqualFold(owner[:len("attributed to ")], "attributed to ") {
		owner = strings.TrimSpace(owner[len("attributed to "):])
	}

	return trimForStorage(owner, ledgerOwnerLimit)
}

// appendUniqueCapped appends value when absent, oldest-first, dropping the
// oldest overflow so the newest provenance always survives the cap.
func appendUniqueCapped(values []string, value string, limit int) ([]string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return values, false
	}
	for _, existing := range values {
		if existing == value {
			return values, false
		}
	}
	values = append(values, value)
	if limit > 0 && len(values) > limit {
		values = values[len(values)-limit:]
	}

	return values, true
}

// appendUniqueCappedSpill is appendUniqueCapped that also reports the element
// evicted off the front when the cap overflows (item Q6): the caller spills the
// returned value into an overflow list instead of losing it (spill-never-shed).
// spilled is "" when nothing was dropped (no append, a duplicate, or under cap).
func appendUniqueCappedSpill(values []string, value string, limit int) (kept []string, spilled string, added bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return values, "", false
	}
	for _, existing := range values {
		if existing == value {
			return values, "", false
		}
	}
	values = append(values, value)
	if limit > 0 && len(values) > limit {
		spilled = values[0]
		values = values[len(values)-limit:]
	}

	return values, spilled, true
}

// recordFromLedgerFact mints a fresh record (a new validity window). A fact
// arriving already terminal (e.g. a decision row superseded before the ledger
// ever saw it) is recorded with the window closed on arrival — knowledge for
// briefings, never a live item.
func recordFromLedgerFact(fact ledgerFact, id string, nowStamp string) ledgerRecord {
	status := normalizeLedgerStatus(fact.Entity, fact.Status)
	validFrom := nowStamp
	if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(fact.At)); err == nil {
		validFrom = parsed.UTC().Format(time.RFC3339)
	}
	record := ledgerRecord{
		ID:         id,
		Entity:     fact.Entity,
		Title:      trimForStorage(normalizeMemoryText(fact.Title), ledgerTitleLimit),
		Status:     status,
		Owner:      normalizeLedgerOwner(fact.Owner),
		ValidFrom:  validFrom,
		Importance: clampImportance(fact.Importance),
		UpdatedAt:  nowStamp,
	}
	record.Anchors, _ = appendUniqueCapped(nil, fact.Anchor, ledgerAnchorCap)
	record.MeetingIDs, _ = appendUniqueCapped(nil, fact.MeetingID, ledgerMeetingIDCap)
	record.Aliases = mergeLedgerAliases(nil, fact.Aliases)
	if isTerminalLedgerStatus(status) {
		record.ValidTo = nowStamp
	}

	return record
}

// mergeLedgerAliases unions alias phrasings into a record's accumulated set,
// case-insensitive-deduped and capped (item 1.3a). The clamp mirrors the
// digest's own alias discipline so a poisoned alias can never balloon a record.
func mergeLedgerAliases(existing []string, incoming []string) []string {
	if len(incoming) == 0 {
		return existing
	}
	merged := existing
	for _, alias := range clampDigestAliases(incoming) {
		duplicate := false
		for _, have := range merged {
			if strings.EqualFold(have, alias) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		merged = append(merged, alias)
		if len(merged) > ledgerAliasCap {
			merged = merged[len(merged)-ledgerAliasCap:]
		}
	}

	return merged
}

// mergeLedgerFact folds a non-terminal fact into an existing record: owner
// newest-wins (ownership drift is real signal), importance ratchets up,
// anchors/meetings union under caps, status follows the newest non-terminal
// reading. Reports whether anything actually changed — an unchanged merge
// emits NO event, which is what makes re-consolidating a rebuilt cumulative
// digest a no-op.
func mergeLedgerFact(record ledgerRecord, fact ledgerFact, nowStamp string) (ledgerRecord, bool) {
	changed := false
	if owner := normalizeLedgerOwner(fact.Owner); owner != "" && owner != record.Owner {
		// ownership drift newest-wins, but the displaced owner is retained on the
		// evolution trail (item 2.3a) instead of being lost.
		if record.Owner != "" {
			record.PastOwners, _ = appendUniqueCapped(record.PastOwners, record.Owner, ledgerPastOwnersCap)
		}
		record.Owner = owner
		changed = true
	}
	if status := normalizeLedgerStatus(fact.Entity, fact.Status); !isTerminalLedgerStatus(status) && status != record.Status {
		record.Status = status
		changed = true
	}
	if importance := clampImportance(fact.Importance); importance > record.Importance {
		record.Importance = importance
		changed = true
	}
	var added bool
	var spilled string
	if record.Anchors, spilled, added = appendUniqueCappedSpill(record.Anchors, fact.Anchor, ledgerAnchorCap); added {
		if spilled != "" {
			record.AnchorsOverflow, _ = appendUniqueCapped(record.AnchorsOverflow, spilled, ledgerProvenanceOverflowCap)
		}
		changed = true
	}
	if record.MeetingIDs, spilled, added = appendUniqueCappedSpill(record.MeetingIDs, fact.MeetingID, ledgerMeetingIDCap); added {
		if spilled != "" {
			record.MeetingIDsOverflow, _ = appendUniqueCapped(record.MeetingIDsOverflow, spilled, ledgerProvenanceOverflowCap)
		}
		changed = true
	}
	// Compare CONTENT, not length: at the alias cap an add-one-evict-one rotation
	// keeps the length identical, so a length check would silently discard the
	// newer phrasing and freeze the set (F24).
	if merged := mergeLedgerAliases(record.Aliases, fact.Aliases); !stringSlicesEqual(merged, record.Aliases) {
		record.Aliases = merged
		changed = true
	}
	if changed {
		record.UpdatedAt = nowStamp
	}

	return record, changed
}

/* ---------- LLM adjudication (ambiguous band only) ---------- */

const (
	ledgerVerdictSame       = "same"
	ledgerVerdictDifferent  = "different"
	ledgerVerdictSupersedes = "supersedes"
)

type ledgerAmbiguity struct {
	fact        ledgerFact
	candidateID string
}

type ledgerAdjudicationVerdict struct {
	I       int    `json:"i"`
	Verdict string `json:"verdict"`
}

type ledgerAdjudicationOutput struct {
	Verdicts []ledgerAdjudicationVerdict `json:"verdicts"`
}

// parseLedgerAdjudication validates adjudicator output with the same
// stray-markdown-fence tolerance as the other strict-JSON agents.
func parseLedgerAdjudication(text string) (ledgerAdjudicationOutput, bool) {
	text = strings.TrimSpace(text)
	if fenced := strings.TrimPrefix(text, "```json"); fenced != text {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fenced), "```"))
	} else if fenced := strings.TrimPrefix(text, "```"); fenced != text {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fenced), "```"))
	}
	var output ledgerAdjudicationOutput
	if text == "" || json.Unmarshal([]byte(text), &output) != nil {
		return ledgerAdjudicationOutput{}, false
	}

	return output, true
}

func ledgerAdjudicationInstructions() string {
	return strings.Join([]string{
		"You are Bonfire's entity-ledger adjudicator.",
		"For each numbered pair, decide whether the NEW fact and the EXISTING ledger record describe the same underlying item.",
		"Verdicts: \"same\" = a restatement or progress update of the existing item; \"supersedes\" = the new fact explicitly replaces, reverses, or contradicts the existing record (its validity window should close); \"different\" = a genuinely distinct item.",
		"Only answer \"supersedes\" on an explicit replacement or contradiction — related-but-parallel work is \"different\".",
		"Return STRICT JSON only, no markdown fence:",
		`{"verdicts":[{"i":0,"verdict":"same"}]} with exactly one verdict per pair.`,
	}, " ")
}

func buildLedgerAdjudicationInput(pairs []ledgerAmbiguity, working map[string]ledgerRecord, generatedAt time.Time) string {
	var builder strings.Builder
	builder.WriteString("# Generated at\n")
	builder.WriteString(generatedAt.Format(time.RFC3339))
	builder.WriteString("\n\n# Pairs\n")
	for index, pair := range pairs {
		record := working[pair.candidateID]
		builder.WriteString(fmt.Sprintf("- i=%d entity=%s\n", index, pair.fact.Entity))
		builder.WriteString("  new: title=" + pair.fact.Title)
		if status := strings.TrimSpace(pair.fact.Status); status != "" {
			builder.WriteString(" | status=" + status)
		}
		if owner := normalizeLedgerOwner(pair.fact.Owner); owner != "" {
			builder.WriteString(" | owner=" + owner)
		}
		builder.WriteByte('\n')
		builder.WriteString("  existing: title=" + record.Title + " | status=" + record.Status)
		if record.Owner != "" {
			builder.WriteString(" | owner=" + record.Owner)
		}
		builder.WriteString(" | current=" + strconv.FormatBool(record.current()))
		builder.WriteByte('\n')
	}

	return builder.String()
}

// adjudicateLedgerAmbiguities spends the pass's single model call on the
// ambiguous pairs (amendment A8: consolidation batched — one call). Returns
// index→verdict; a missing/invalid verdict falls back to "different" (ADD).
func (app *kanbanBoardApp) adjudicateLedgerAmbiguities(ctx context.Context, apiKey string, responder openAITextResponder, pairs []ledgerAmbiguity, working map[string]ledgerRecord, now time.Time) (map[int]string, error) {
	model := meetingBrainModel()
	text, err := responder(ctx, apiKey, openAITextRequest{
		Model:           model,
		Instructions:    ledgerAdjudicationInstructions(),
		Input:           buildLedgerAdjudicationInput(pairs, working, now),
		ReasoningEffort: "low",
		Verbosity:       "low",
		MaxOutputTokens: entityLedgerMaxOutputTokens,
	})
	if err != nil {
		return nil, err
	}
	output, ok := parseLedgerAdjudication(text)
	if !ok {
		return nil, fmt.Errorf("adjudicator returned non-JSON output")
	}

	verdicts := make(map[int]string, len(output.Verdicts))
	for _, verdict := range output.Verdicts {
		switch verdict.Verdict {
		case ledgerVerdictSame, ledgerVerdictDifferent, ledgerVerdictSupersedes:
			if verdict.I >= 0 && verdict.I < len(pairs) {
				verdicts[verdict.I] = verdict.Verdict
			}
		}
	}

	return verdicts, nil
}

/* ---------- the consolidation pass ---------- */

// produceLedgerConsolidationPass is the entity-ledger agent's pass body; the
// wall clock is injected via runLedgerConsolidationPass so tests pin ids and
// validity stamps.
func (app *kanbanBoardApp) produceLedgerConsolidationPass(ctx context.Context, apiKey string, inputs []meetingMemoryEntry, responder openAITextResponder) (meetingMemoryEntry, error) {
	return app.runLedgerConsolidationPass(ctx, apiKey, inputs, responder, time.Now().UTC())
}

// runLedgerConsolidationPass: (1) resolve the CURRENT digest for every meeting
// the input window touches (a superseded input self-heals — the fold always
// reads live digests); (2) sweep newly appended decision rows (A9); (3)
// consolidate all extracted facts against the folded ledger state —
// deterministic first, one batched adjudication call for the ambiguous band;
// (4) land the pass's events atomically; (5) ALWAYS append the ledger_pass
// cursor artifact so a zero-event pass still advances consumption (the
// decision_pass pattern).
func (app *kanbanBoardApp) runLedgerConsolidationPass(ctx context.Context, apiKey string, inputs []meetingMemoryEntry, responder openAITextResponder, now time.Time) (meetingMemoryEntry, error) {
	if app == nil || app.memory == nil || len(inputs) == 0 {
		// the runner's minBatch gate makes this unreachable on the ticker path;
		// direct callers (a future boundary flush) get a safe no-op.
		return meetingMemoryEntry{}, nil
	}

	current := app.memory.latestDigestPerMeeting()
	seenKeys := map[string]bool{}
	facts := make([]ledgerFact, 0, 32)
	for _, input := range inputs {
		key := digestEntryKey(input)
		if key == "" || seenKeys[key] {
			continue
		}
		seenKeys[key] = true
		digest, ok := current[key]
		if !ok {
			continue
		}
		// §6.4 (RATIFIED 2026-07-09): a listen-only sitting's digest feeds the
		// canonical registry like any other — its facts must be Scout-recallable
		// company-wide. Origin stays visible on the digest's listenOnly stamp.
		facts = append(facts, ledgerFactsFromDigest(digest)...)
	}

	decisions, throughDecisionID := app.unconsumedDecisionEntriesForLedger(entityLedgerDecisionSweepCap)
	for _, decision := range decisions {
		if fact, ok := ledgerFactFromDecisionEntry(decision); ok {
			facts = append(facts, fact)
		}
	}

	appended := 0
	if len(facts) > 0 {
		count, err := app.consolidateLedgerFacts(ctx, apiKey, facts, responder, now)
		if err != nil {
			// nothing persisted (consolidate appends all-or-nothing) and no
			// cursor landed: the whole window re-feeds and retries next tick. The
			// re-feed markers stay set, so a failed pass retries them too (F20).
			return meetingMemoryEntry{}, err
		}
		appended = count
	}

	// F20: the pass has now folded the swept rows, so clear any re-feed markers —
	// done AFTER consolidation succeeds so a failed pass leaves them set to retry.
	for _, decision := range decisions {
		if decision.Metadata[entityLedgerRefeedMetadataKey] != "1" {
			continue
		}
		if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindDecision, decision.ID, decision.Text, map[string]string{entityLedgerRefeedMetadataKey: ""}); err != nil {
			log.Errorf("%s could not clear the re-feed marker on %s: %v", entityLedgerAgentName, decision.ID, err)
		}
	}

	passText := "entity ledger pass: no changes"
	if appended > 0 {
		passText = "entity ledger pass: " + strconv.Itoa(appended) + " event(s)"
	}
	metadata := map[string]string{
		entityLedgerCursorMetadataKey: inputs[len(inputs)-1].ID,
		"eventCount":                  strconv.Itoa(appended),
		"generatedAt":                 now.UTC().Format(time.RFC3339),
	}
	if throughDecisionID != "" {
		metadata[entityLedgerDecisionCursorMetadataKey] = throughDecisionID
	}
	passEntry, _, err := app.memory.appendAmbientEntry(meetingMemoryKindLedgerPass, durableTimestampID("ledger-pass", now), passText, metadata)
	if err != nil {
		return meetingMemoryEntry{}, err
	}

	return passEntry, nil
}

// consolidateLedgerFacts matches every fact against the folded state and
// emits the pass's ADD/UPDATE/SUPERSEDE/CLOSE events. Facts consolidate
// against a WORKING copy that includes this pass's own earlier ops, so two
// restatements inside one pass dedupe against each other. Adjudication
// failure degrades to ADD (a duplicate is recoverable by a later supersede; a
// false merge silently loses a record) — the pass never fails on the model.
func (app *kanbanBoardApp) consolidateLedgerFacts(ctx context.Context, apiKey string, facts []ledgerFact, responder openAITextResponder, now time.Time) (int, error) {
	nowStamp := now.UTC().Format(time.RFC3339)

	state := app.memory.ledgerState()
	working := make(map[string]ledgerRecord, len(state))
	order := make([]string, 0, len(state))
	for id, record := range state {
		working[id] = record
		order = append(order, id)
	}
	// deterministic matching order: oldest window first, id as tiebreak.
	sort.SliceStable(order, func(i, j int) bool {
		if working[order[i]].ValidFrom != working[order[j]].ValidFrom {
			return working[order[i]].ValidFrom < working[order[j]].ValidFrom
		}
		return order[i] < order[j]
	})
	workingList := func() []ledgerRecord {
		records := make([]ledgerRecord, 0, len(order))
		for _, id := range order {
			records = append(records, working[id])
		}
		return records
	}

	events := make([]ledgerEventPayload, 0, len(facts))
	apply := func(event ledgerEventPayload) {
		event.At = nowStamp
		events = append(events, event)
		if _, known := working[event.Record.ID]; !known {
			order = append(order, event.Record.ID)
		}
		working[event.Record.ID] = event.Record
	}

	seq := 0
	mint := func(entity string) string {
		seq++
		return fmt.Sprintf("ldg-%s-%d-%d", entity, now.UnixNano(), seq)
	}

	addFact := func(fact ledgerFact, reason string) {
		apply(ledgerEventPayload{
			Op:     ledgerOpAdd,
			Record: recordFromLedgerFact(fact, mint(fact.Entity), nowStamp),
			Reason: reason,
		})
	}

	// consolidateAgainst resolves a fact against one matched record — shared
	// by the deterministic strong path and the adjudicated "same" verdict.
	consolidateAgainst := func(fact ledgerFact, record ledgerRecord, reason string) {
		status := normalizeLedgerStatus(fact.Entity, fact.Status)
		if record.current() {
			merged, changed := mergeLedgerFact(record, fact, nowStamp)
			if isTerminalLedgerStatus(status) {
				// contradiction/completion: close the validity window, keep
				// the row — history is never deleted.
				merged.Status = status
				merged.ValidTo = nowStamp
				merged.UpdatedAt = nowStamp
				apply(ledgerEventPayload{Op: ledgerOpClose, Record: merged, Reason: reason})
				return
			}
			if changed {
				apply(ledgerEventPayload{Op: ledgerOpUpdate, Record: merged, Reason: reason})
			}
			return
		}
		// record already closed: a terminal restatement is a no-op; an OPEN
		// restatement opens a NEW validity window as a fresh record (the
		// temporal pattern — the closed window stays untouched).
		if isTerminalLedgerStatus(status) {
			return
		}
		addFact(fact, "reopens "+record.ID)
	}

	ambiguities := make([]ledgerAmbiguity, 0, 4)
	for _, fact := range facts {
		fact.Title = normalizeMemoryText(fact.Title)
		if fact.Title == "" || strings.TrimSpace(fact.Entity) == "" {
			continue
		}
		record, class := matchLedgerFact(fact, workingList())
		switch class {
		case ledgerMatchStrong:
			consolidateAgainst(fact, record, "deterministic match")
		case ledgerMatchAmbiguous:
			if len(ambiguities) < entityLedgerAdjudicationPairCap {
				ambiguities = append(ambiguities, ledgerAmbiguity{fact: fact, candidateID: record.ID})
			} else {
				addFact(fact, "adjudication overflow")
			}
		default:
			addFact(fact, "")
		}
	}

	if len(ambiguities) > 0 {
		verdicts, err := app.adjudicateLedgerAmbiguities(ctx, apiKey, responder, ambiguities, working, now)
		if err != nil {
			log.Errorf("%s adjudication failed (%d pair(s) fall back to add): %v", entityLedgerAgentName, len(ambiguities), err)
			verdicts = nil
		}
		for index, ambiguity := range ambiguities {
			verdict := verdicts[index]
			candidate, known := working[ambiguity.candidateID]
			if !known {
				verdict = ledgerVerdictDifferent
			}
			switch verdict {
			case ledgerVerdictSame:
				consolidateAgainst(ambiguity.fact, candidate, "adjudicated same")
			case ledgerVerdictSupersedes:
				fresh := recordFromLedgerFact(ambiguity.fact, mint(ambiguity.fact.Entity), nowStamp)
				if candidate.current() {
					closed := candidate
					closed.Status = ledgerStatusSuperseded
					closed.ValidTo = nowStamp
					closed.SupersededBy = fresh.ID
					closed.UpdatedAt = nowStamp
					apply(ledgerEventPayload{Op: ledgerOpSupersede, Record: closed, Reason: "superseded by " + fresh.ID})
				}
				apply(ledgerEventPayload{Op: ledgerOpAdd, Record: fresh, Reason: "supersedes " + candidate.ID})
			default:
				addFact(ambiguity.fact, "adjudicated different")
			}
		}
	}

	if len(events) == 0 {
		return 0, nil
	}
	entries := make([]meetingMemoryEntry, 0, len(events))
	for index, event := range events {
		raw, err := json.Marshal(event)
		if err != nil {
			return 0, fmt.Errorf("encode ledger event: %w", err)
		}
		entries = append(entries, meetingMemoryEntry{
			ID:        fmt.Sprintf("ledger-event-%d-%03d", now.UnixNano(), index),
			Kind:      meetingMemoryKindLedgerEvent,
			Text:      string(raw),
			CreatedAt: now.UTC(),
			Metadata: map[string]string{
				"op":       event.Op,
				"recordId": event.Record.ID,
				"entity":   event.Record.Entity,
			},
		})
	}

	return app.memory.appendLedgerEvents(entries)
}

/* ---------- read surfaces (Waves 4/5 consume these) ---------- */

// ledgerStateView groups the ledger for state-shaped consumers: amendment
// A2's T4 company view and A5's current-state recall routing.
type ledgerStateView struct {
	Decisions     []ledgerRecord `json:"decisions"`
	ActionItems   []ledgerRecord `json:"actionItems"`
	Topics        []ledgerRecord `json:"topics"`
	OpenQuestions []ledgerRecord `json:"openQuestions"`
}

// sortLedgerRecords orders records for briefings: importance first (A4), then
// most recently updated, then id for determinism.
func sortLedgerRecords(records []ledgerRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].Importance != records[j].Importance {
			return records[i].Importance > records[j].Importance
		}
		if records[i].UpdatedAt != records[j].UpdatedAt {
			return records[i].UpdatedAt > records[j].UpdatedAt
		}
		return records[i].ID < records[j].ID
	})
}

// ledgerCurrentStateView returns the CURRENT (validity window open) records
// grouped by entity, importance-ranked, capped per section. This is the
// materialized "what is true right now" read: open decisions, live action
// items, running topics, unanswered questions — computed in Go from the fold,
// never delegated to the LLM (amendment A5).
func (app *kanbanBoardApp) ledgerCurrentStateView(limitPerSection int) ledgerStateView {
	view := ledgerStateView{
		Decisions:     []ledgerRecord{},
		ActionItems:   []ledgerRecord{},
		Topics:        []ledgerRecord{},
		OpenQuestions: []ledgerRecord{},
	}
	if app == nil || app.memory == nil {
		return view
	}

	for _, record := range app.memory.ledgerState() {
		if !record.current() {
			continue
		}
		switch record.Entity {
		case ledgerEntityDecision:
			view.Decisions = append(view.Decisions, record)
		case ledgerEntityActionItem:
			view.ActionItems = append(view.ActionItems, record)
		case ledgerEntityTopic:
			view.Topics = append(view.Topics, record)
		case ledgerEntityOpenQuestion:
			view.OpenQuestions = append(view.OpenQuestions, record)
		}
	}
	for _, section := range []*[]ledgerRecord{&view.Decisions, &view.ActionItems, &view.Topics, &view.OpenQuestions} {
		sortLedgerRecords(*section)
		if limitPerSection > 0 && len(*section) > limitPerSection {
			*section = (*section)[:limitPerSection]
		}
	}

	return view
}

// searchLedgerRecords is the A5 ledger-first lookup for current-state queries
// ("status of X", "what's decided on Y"): normalized token overlap against
// record titles, current records ranked above closed history, then by match
// strength and importance. Anchors on the returned records drill down to the
// verbatim exchange via transcriptWindowAround.
func (app *kanbanBoardApp) searchLedgerRecords(query string, limit int) []ledgerRecord {
	if app == nil || app.memory == nil || limit <= 0 {
		return nil
	}
	queryTokens := strings.Fields(ledgerTitleKey(query))
	if len(queryTokens) == 0 {
		return nil
	}

	type scoredRecord struct {
		record ledgerRecord
		score  float64
	}
	matched := make([]scoredRecord, 0, 8)
	for _, record := range app.memory.ledgerState() {
		// item 1.3a: widen the record's comparable tokens by its folded aliases so
		// a vocabulary-drifted query ("the korean tv deal") still finds the record
		// titled "Samsung TV Plus".
		recordTokens := aliasAugmentedTokens(strings.Fields(ledgerTitleKey(record.Title)), record.Aliases)
		if len(recordTokens) == 0 {
			continue
		}
		score := tokenSetContainment(queryTokens, recordTokens)
		if jaccard := tokenSetJaccard(queryTokens, recordTokens); jaccard > score {
			score = jaccard
		}
		if score < ledgerAmbiguousMatchJaccard {
			continue
		}
		matched = append(matched, scoredRecord{record: record, score: score})
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].record.current() != matched[j].record.current() {
			return matched[i].record.current()
		}
		if matched[i].score != matched[j].score {
			return matched[i].score > matched[j].score
		}
		if matched[i].record.Importance != matched[j].record.Importance {
			return matched[i].record.Importance > matched[j].record.Importance
		}
		return matched[i].record.ID < matched[j].record.ID
	})
	if len(matched) > limit {
		matched = matched[:limit]
	}

	records := make([]ledgerRecord, 0, len(matched))
	for _, scored := range matched {
		records = append(records, scored.record)
	}

	return records
}

/* ---------- item 2.2c: position lookup (who-thinks-what) ---------- */

// searchPositionRecords is the O(lookup) answer to "what does <person> think
// about <topic>": position records for one owner, ranked current-above-closed
// so the supersession chain reads as "current stance / previous stance". An
// empty owner matches every holder (the team's stance on a topic); an empty
// topic returns all of that owner's positions. Alias-augmented, like the
// current-state lookup, so a renamed topic still resolves.
func (app *kanbanBoardApp) searchPositionRecords(owner string, topic string, limit int) []ledgerRecord {
	if app == nil || app.memory == nil || limit <= 0 {
		return nil
	}
	ownerKey := normalizeLedgerOwner(owner)
	topicTokens := strings.Fields(ledgerTitleKey(topic))

	type scoredRecord struct {
		record ledgerRecord
		score  float64
	}
	matched := make([]scoredRecord, 0, 8)
	for _, record := range app.memory.ledgerState() {
		if record.Entity != ledgerEntityPosition {
			continue
		}
		if ownerKey != "" && !strings.EqualFold(normalizeLedgerOwner(record.Owner), ownerKey) {
			continue
		}
		score := 1.0 // owner-only match when the question names no topic
		if len(topicTokens) > 0 {
			recordTokens := aliasAugmentedTokens(strings.Fields(ledgerTitleKey(record.Title)), record.Aliases)
			score = tokenSetContainment(topicTokens, recordTokens)
			if jaccard := tokenSetJaccard(topicTokens, recordTokens); jaccard > score {
				score = jaccard
			}
			if score < ledgerAmbiguousMatchJaccard {
				continue
			}
		}
		matched = append(matched, scoredRecord{record: record, score: score})
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].record.current() != matched[j].record.current() {
			return matched[i].record.current()
		}
		if matched[i].score != matched[j].score {
			return matched[i].score > matched[j].score
		}
		if matched[i].record.UpdatedAt != matched[j].record.UpdatedAt {
			return matched[i].record.UpdatedAt > matched[j].record.UpdatedAt
		}
		return matched[i].record.ID < matched[j].record.ID
	})
	if len(matched) > limit {
		matched = matched[:limit]
	}

	records := make([]ledgerRecord, 0, len(matched))
	for _, scored := range matched {
		records = append(records, scored.record)
	}

	return records
}

/* ---------- item 2.3a/b: evolution / supersession-chain reads ---------- */

// ledgerPredecessors maps each record id to the record it DIRECTLY superseded
// (the reverse of the SupersededBy forward pointer), so a current record can
// render "previously: <prior title> (until <date>)" inline without a second
// fold. A record that never superseded anything is simply absent from the map.
func (store *meetingMemoryStore) ledgerPredecessors() map[string]ledgerRecord {
	if store == nil {
		return nil
	}
	predecessors := map[string]ledgerRecord{}
	for _, record := range store.ledgerState() {
		if by := strings.TrimSpace(record.SupersededBy); by != "" {
			predecessors[by] = record
		}
	}

	return predecessors
}

// ledgerTransition is one dated post-state in a record's history (item 2.3b),
// replayed from the ledger_event log. Every event carries the FULL post-state,
// so the transition trail is materialized deterministically — no model call.
type ledgerTransition struct {
	RecordID     string
	Op           string
	Entity       string
	Title        string
	Status       string
	Owner        string
	At           string
	ValidFrom    string
	ValidTo      string
	SupersededBy string
	Reason       string
}

// ledgerLineage returns the supersession chain of record ids that subjectID
// belongs to, ordered oldest→newest, so a query landing on ANY link renders the
// whole arc. It walks backward over "the record I superseded" and forward over
// SupersededBy; a subject with no chain returns just itself.
func (store *meetingMemoryStore) ledgerLineage(subjectID string) []string {
	subjectID = strings.TrimSpace(subjectID)
	if store == nil || subjectID == "" {
		return nil
	}
	state := store.ledgerState()
	if _, ok := state[subjectID]; !ok {
		return nil
	}
	// predecessorOf[newID] = the id of the record newID superseded.
	predecessorOf := map[string]string{}
	for id, record := range state {
		if by := strings.TrimSpace(record.SupersededBy); by != "" {
			predecessorOf[by] = id
		}
	}
	origin := subjectID
	guard := map[string]bool{subjectID: true}
	for {
		prev, ok := predecessorOf[origin]
		if !ok || guard[prev] {
			break
		}
		origin = prev
		guard[prev] = true
	}
	lineage := make([]string, 0, len(guard))
	visited := map[string]bool{}
	for cursor := origin; cursor != "" && !visited[cursor]; {
		if _, ok := state[cursor]; !ok {
			break
		}
		visited[cursor] = true
		lineage = append(lineage, cursor)
		cursor = strings.TrimSpace(state[cursor].SupersededBy)
	}

	return lineage
}

// ledgerRecordEvolution replays the dated transition history for subjectID's
// whole supersession lineage in chronological (log) order (item 2.3b): the
// deterministic answer to "how did X evolve". Zero new storage — the data is
// already on disk in the ledger_event log.
func (store *meetingMemoryStore) ledgerRecordEvolution(subjectID string) []ledgerTransition {
	lineage := store.ledgerLineage(subjectID)
	if len(lineage) == 0 {
		return nil
	}
	inLineage := make(map[string]bool, len(lineage))
	for _, id := range lineage {
		inLineage[id] = true
	}

	transitions := make([]ledgerTransition, 0, len(lineage)+2)
	for _, entry := range store.entriesOfKind(meetingMemoryKindLedgerEvent, 0) {
		var event ledgerEventPayload
		if json.Unmarshal([]byte(entry.Text), &event) != nil {
			continue
		}
		id := strings.TrimSpace(event.Record.ID)
		if !inLineage[id] {
			continue
		}
		transitions = append(transitions, ledgerTransition{
			RecordID:     id,
			Op:           event.Op,
			Entity:       event.Record.Entity,
			Title:        event.Record.Title,
			Status:       event.Record.Status,
			Owner:        event.Record.Owner,
			At:           firstNonEmptyString(strings.TrimSpace(event.At), strings.TrimSpace(event.Record.UpdatedAt)),
			ValidFrom:    event.Record.ValidFrom,
			ValidTo:      event.Record.ValidTo,
			SupersededBy: event.Record.SupersededBy,
			Reason:       event.Reason,
		})
	}

	return transitions
}
