package main

// Track-2 Wave 7 — end-to-end verification harness + gold eval set
// (amendment A7).
//
// A synthetic multi-meeting, multi-day fixture is written as a raw
// meeting-memory.jsonl and loaded through the REAL boot path
// (newMeetingMemoryStore via newKanbanBoardApp), then the REAL producer
// chain runs over it in backfill posture with a deterministic canned
// responder — meeting digests (two passes under the 3-meetings-per-tick
// cap), the deterministic day fold, the A3 reflection, the entity-ledger
// consolidation (including the A9 decision sweep and the one-batched
// adjudication call), and the T4 company digest. The gold eval set then
// asserts, over ONE built world:
//
//   G1  "what did I miss this week" (keyless → the deterministic briefing
//       lane) is comprehensive: multi-day, multi-meeting (incl. the legacy
//       null-meetingId huddle), organized sections, importance-ranked,
//       hedged attribution, verbatim ledger decisions — never keyword scraps.
//   G2  the same query's MODEL input is token-bounded and the base64
//       artifact blob is structurally absent; the pinned digest lane leads.
//   G3  "status of the pricing sheet" answers LEDGER-first: closed history
//       (done, owner, validity window) stays findable.
//   G4  "what did we decide on the launch date" ranks the CURRENT decision
//       above the superseded one and shows the closed validity window.
//   G5  what-changed-between-days: per-day briefings (explicit start_day/
//       end_day, date math in Go) show the launch decision flipping and the
//       pricing sheet going open → done.
//   G6  the recurring-blockers REFLECTION exists (once, for the intended
//       day), saw the blocker on BOTH days, and is recall-eligible.
//   G7  the marathon meeting splits into per-calendar-day rollup slices.
//   G8  the company digest is ledger STATE + thin narrative (current
//       records only), and a generic "where do we stand" answers from it.
//   G9  anchor drill-down resolves a digest fact to the verbatim transcript
//       exchange, never crossing meetings.
//   G10 a no-range query keeps the summary layer (company digest + meeting
//       digests + brains) — the Wave-5 gate-removal fidelity, replayed E2E.
//
// A second test proves the same flagship query composes a fresh map-reduce
// briefing when NO digests exist (pre-backfill outage posture), with the
// blob firewall asserted on every map input.
//
// PROD REPLAY (operator-triggered, read-only, NEVER committed):
//   scp root@146.190.171.224:/var/lib/docker/volumes/digitalocean_meeting_data/_data/meeting-memory.jsonl /tmp/t2-replay.jsonl
//   T2_REPLAY_JSONL=/tmp/t2-replay.jsonl go test -run TestRecallProdReplaySanity .
// TestRecallProdReplaySanity loads a COPY of that file into a temp store and
// asserts the recall input stays far under the token budget with zero base64
// — the direct regression of the observed 2,505,990 > 1,000,000 token 400.
//
// Everything is hermetic: no live model call is ever made (the responder is
// canned; the flagship recall paths run keyless), and every fixture day is
// anchored to the CURRENT local week's Monday so "this week" covers all
// three days on any weekday (future days of the running week are still
// inside relativeQueryTimeRange's [Monday, Monday+7d) window).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

/* ---------- fixture ---------- */

const (
	recallGoldWeekQuery = "what did I miss this week?"
	// recallGoldBlobMarker is a distinctive base64-alphabet run: if it ever
	// appears in a model input or answer, artifact bytes leaked past the
	// Wave-0 store cap / kind whitelists.
	recallGoldBlobMarker = "R09MREJMT0I0"

	recallGoldReflectionText = "Recurring blocker: Stripe sandbox access has stayed blocked across two consecutive days (raised in the packaging kickoff and again in the pricing sync). Consensus is forming on vendor Zebra for the packaging pilot. The launch date decision was circled and moved once: July 24 was superseded by July 31. Ownership drift: none — Tyler closed out the pricing sheet."

	recallGoldCompanyNarrative = "Vendor Zebra is locked for the packaging pilot, the launch moved to July 31, and Stripe sandbox access remains the standing blocker."
)

type recallGoldFixture struct {
	location *time.Location
	genDay   string
	// day1..day3 are LOCAL midnights of Monday/Tuesday/Wednesday of the
	// current local week; d1..d3 the matching dayBucket keys.
	day1, day2, day3 time.Time
	d1, d2, d3       string
	meetingA         string
	meetingB         string
	meetingC         string
	legacyKey        string
}

// guardSameDay skips a test when local midnight was crossed after the
// fixture anchored itself to "today" — the week anchor (and every
// relative-range query) would otherwise race the wall clock.
func (fx *recallGoldFixture) guardSameDay(t *testing.T) {
	t.Helper()
	if dayBucket(time.Now()) != fx.genDay {
		t.Skip("local midnight crossed since fixture generation — week anchor moved")
	}
}

// at returns the UTC instant of hour:min local time on the given local day.
func (fx *recallGoldFixture) at(day time.Time, hour, min int) time.Time {
	return day.Add(time.Duration(hour)*time.Hour + time.Duration(min)*time.Minute).UTC()
}

func (fx *recallGoldFixture) rfc(day time.Time, hour, min int) string {
	return fx.at(day, hour, min).Format(time.RFC3339)
}

func buildRecallGoldFixture(t *testing.T) *recallGoldFixture {
	t.Helper()
	location := meetingTimeLocation()
	now := time.Now().In(location)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	weekStart := dayStart.AddDate(0, 0, -int((int(now.Weekday())+6)%7))

	fx := &recallGoldFixture{
		location: location,
		genDay:   dayBucket(time.Now()),
		day1:     weekStart,
		day2:     weekStart.AddDate(0, 0, 1),
		day3:     weekStart.AddDate(0, 0, 2),
		meetingA: "meeting-gold-a",
		meetingB: "meeting-gold-b",
		meetingC: "meeting-gold-c",
	}
	fx.d1 = fx.day1.Format(dayBucketLayout)
	fx.d2 = fx.day2.Format(dayBucketLayout)
	fx.d3 = fx.day3.Format(dayBucketLayout)
	// the synthetic key the digest producer mints for null-meetingId brains.
	fx.legacyKey = "meeting-legacy-" + fx.d1

	return fx
}

// recallGoldEntries is the synthetic store: three meetings across three
// days (one a multi-day marathon), a legacy null-meetingId brain, two
// active decision-log rows, and one base64 artifact blob that must never
// reach a prompt.
func recallGoldEntries(fx *recallGoldFixture) []meetingMemoryEntry {
	tx := func(id, meetingID, speaker, text string, at time.Time) meetingMemoryEntry {
		return meetingMemoryEntry{
			ID: id, Kind: meetingMemoryKindTranscript, Text: speaker + ": " + text, CreatedAt: at,
			Metadata: map[string]string{"meetingId": meetingID, "speaker": speaker, "source": "transcript_lane"},
		}
	}
	brain := func(id, meetingID, text string, at, from, through time.Time) meetingMemoryEntry {
		metadata := map[string]string{
			"fromTranscriptCreatedAt":    from.Format(time.RFC3339),
			"throughTranscriptCreatedAt": through.Format(time.RFC3339),
		}
		if meetingID != "" {
			metadata["meetingId"] = meetingID
		}
		return meetingMemoryEntry{ID: id, Kind: meetingMemoryKindBrain, Text: text, CreatedAt: at, Metadata: metadata}
	}
	decision := func(id, meetingID, text string, at time.Time) meetingMemoryEntry {
		return meetingMemoryEntry{
			ID: id, Kind: meetingMemoryKindDecision, Text: text, CreatedAt: at,
			Metadata: map[string]string{"status": decisionStatusActive, "madeBy": "AJ", "meetingId": meetingID},
		}
	}

	return []meetingMemoryEntry{
		// ---- day 1: meeting A (packaging kickoff) ----
		tx("tx-a1", fx.meetingA, "AJ", "We choose vendor Zebra for the packaging pilot.", fx.at(fx.day1, 10, 0)),
		tx("tx-a2", fx.meetingA, "Tyler", "I will draft the pricing sheet.", fx.at(fx.day1, 10, 5)),
		tx("tx-a3", fx.meetingA, "AJ", "Target the launch for July 24.", fx.at(fx.day1, 10, 10)),
		tx("tx-a4", fx.meetingA, "Caitlyn", "Stripe sandbox access is blocked for the payments test.", fx.at(fx.day1, 10, 12)),
		brain("brain-a1", fx.meetingA,
			"## Overview\nPackaging pilot kickoff: vendor Zebra chosen, launch targeted for July 24, Tyler owns the pricing sheet, Stripe sandbox access blocked.\n## Transcript reference\ntx-a1, tx-a2, tx-a3, tx-a4",
			fx.at(fx.day1, 10, 30), fx.at(fx.day1, 10, 0), fx.at(fx.day1, 10, 15)),
		decision("dec-gold-1", fx.meetingA, "Choose vendor Zebra for the packaging pilot.", fx.at(fx.day1, 11, 0)),
		// ---- day 1: legacy huddle (null meetingId — pre-scoping history) ----
		brain("brain-legacy-1", "",
			"## Overview\nLegacy huddle: Joel resolved the login outage.",
			fx.at(fx.day1, 12, 0), fx.at(fx.day1, 12, 0), fx.at(fx.day1, 12, 0)),
		// ---- day 1: the artifact blob (the 2.6MB-class pathology) ----
		{
			ID: "art-gold-blob", Kind: meetingMemoryKindOSArtifact,
			Text:      "data:image/png;base64," + strings.Repeat(recallGoldBlobMarker, 9000),
			CreatedAt: fx.at(fx.day1, 13, 0),
			Metadata:  map[string]string{"meetingId": fx.meetingA, "title": "Gold launch deck"},
		},
		// ---- day 1 evening: marathon meeting C begins ----
		tx("tx-c1", fx.meetingC, "Erick", "Warehouse audit kicked off.", fx.at(fx.day1, 19, 30)),
		brain("brain-c1", fx.meetingC,
			"## Overview\nOps marathon: warehouse audit kicked off.\n## Transcript reference\ntx-c1",
			fx.at(fx.day1, 20, 0), fx.at(fx.day1, 19, 0), fx.at(fx.day1, 19, 45)),
		// ---- day 2: meeting B (pricing sync) ----
		tx("tx-b1", fx.meetingB, "AJ", "The launch moves out a week to July 31.", fx.at(fx.day2, 9, 0)),
		tx("tx-b2", fx.meetingB, "Caitlyn", "Stripe sandbox access is still blocked.", fx.at(fx.day2, 9, 5)),
		brain("brain-b1", fx.meetingB,
			"## Overview\nPricing sync: launch moved to July 31 (July 24 superseded), pricing sheet done, Stripe sandbox still blocked.\n## Transcript reference\ntx-b1, tx-b2",
			fx.at(fx.day2, 9, 30), fx.at(fx.day2, 9, 0), fx.at(fx.day2, 9, 10)),
		decision("dec-gold-2", fx.meetingB, "Launch moved to July 31.", fx.at(fx.day2, 10, 0)),
		// ---- day 3: marathon meeting C ends ----
		tx("tx-c2", fx.meetingC, "Erick", "Warehouse audit finished with two findings to file.", fx.at(fx.day3, 7, 30)),
		brain("brain-c2", fx.meetingC,
			"## Overview\nOps marathon wrap: audit finished, two findings to file (Erick).\n## Transcript reference\ntx-c2",
			fx.at(fx.day3, 9, 0), fx.at(fx.day2, 22, 0), fx.at(fx.day3, 7, 45)),
	}
}

// meetingDigestJSON is the canned, deterministic model output for one
// meeting's digest — the T2 schema the real producer parses, clamps, and
// stores (server-derived meetingId/day/span override whatever is claimed
// here). Facts carry their own `at` stamps so the day fold regroups the
// marathon onto three calendar days.
func (fx *recallGoldFixture) meetingDigestJSON(key string) (string, bool) {
	var payload meetingDigestPayload
	switch key {
	case fx.meetingA:
		payload = meetingDigestPayload{
			MeetingID: key, Title: "Packaging pilot kickoff", Day: fx.d1,
			Attendees: []string{"AJ", "Tyler", "Caitlyn"},
			Topics: []meetingDigestTopic{
				{T: "Packaging pilot vendor selection", Anchor: "tx-a1", At: fx.rfc(fx.day1, 10, 0), Importance: 3},
			},
			Decisions: []meetingDigestDecision{
				{D: "Choose vendor Zebra for the packaging pilot", By: "attributed to AJ", Status: "decided", Anchor: "tx-a1", At: fx.rfc(fx.day1, 10, 0), Importance: 5},
				{D: "Target the launch for July 24", By: "attributed to AJ", Status: "decided", Anchor: "tx-a3", At: fx.rfc(fx.day1, 10, 10), Importance: 4},
			},
			ActionItems: []meetingDigestAction{
				{A: "Draft the pricing sheet", Owner: "Tyler", Status: "open", Anchor: "tx-a2", At: fx.rfc(fx.day1, 10, 5), Importance: 4},
			},
			OpenQuestions: []meetingDigestQuestion{
				{Q: "Stripe sandbox access blocked for the payments test", Anchor: "tx-a4", At: fx.rfc(fx.day1, 10, 12), Importance: 4},
			},
			Themes: []string{"packaging"},
		}
	case fx.meetingB:
		payload = meetingDigestPayload{
			MeetingID: key, Title: "Pricing sync", Day: fx.d2,
			Attendees: []string{"AJ", "Tyler", "Caitlyn"},
			Topics: []meetingDigestTopic{
				// engineered into the deterministic matcher's AMBIGUOUS band
				// against meeting C's "Warehouse audit kickoff" (token jaccard
				// 0.5): the pass must spend its ONE batched adjudication call
				// here, and the "different" verdict must keep two records.
				{T: "Warehouse audit staffing", At: fx.rfc(fx.day2, 9, 8), Importance: 2},
			},
			Decisions: []meetingDigestDecision{
				// carried-forward continuity row: the day-1 call flipped
				// terminal on day 2 → the ledger CLOSES its validity window.
				{D: "Target the launch for July 24", By: "attributed to AJ", Status: "superseded", Anchor: "tx-a3", At: fx.rfc(fx.day2, 9, 0), Importance: 4},
				{D: "Launch moved to July 31", By: "attributed to AJ", Status: "decided", Anchor: "tx-b1", At: fx.rfc(fx.day2, 9, 0), Importance: 5},
			},
			ActionItems: []meetingDigestAction{
				{A: "Draft the pricing sheet", Owner: "Tyler", Status: "done", At: fx.rfc(fx.day2, 9, 5), Importance: 3},
			},
			OpenQuestions: []meetingDigestQuestion{
				{Q: "Stripe sandbox access blocked for the payments test", Anchor: "tx-b2", At: fx.rfc(fx.day2, 9, 5), Importance: 4},
			},
			Themes: []string{"launch"},
		}
	case fx.meetingC:
		payload = meetingDigestPayload{
			MeetingID: key, Title: "Ops marathon", Day: fx.d3,
			Attendees: []string{"Erick"},
			Topics: []meetingDigestTopic{
				{T: "Warehouse audit kickoff", Anchor: "tx-c1", At: fx.rfc(fx.day1, 19, 30), Importance: 3},
				{T: "Overnight shift coverage plan", At: fx.rfc(fx.day2, 6, 0), Importance: 2},
			},
			ActionItems: []meetingDigestAction{
				{A: "File the warehouse audit findings", Owner: "Erick", Status: "open", Anchor: "tx-c2", At: fx.rfc(fx.day3, 7, 30), Importance: 4},
			},
			Themes: []string{"operations"},
		}
	case fx.legacyKey:
		payload = meetingDigestPayload{
			MeetingID: key, Title: "Legacy huddle", Day: fx.d1,
			Topics: []meetingDigestTopic{
				{T: "Login outage resolved by Joel", At: fx.rfc(fx.day1, 12, 0), Importance: 3},
			},
		}
	default:
		return "", false
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

// newRecallGoldApp writes the fixture JSONL and boots a real app over it
// (the actual newMeetingMemoryStore load path — no store poking).
func newRecallGoldApp(t *testing.T) (*kanbanBoardApp, *recallGoldFixture) {
	t.Helper()
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	// keyless primary model paths: the flagship queries must be served by the
	// deterministic lanes, so both provider keys are cleared.
	t.Setenv("ANTHROPIC_API_KEY", "")
	// the fixture is pre-boot history: the A9 decision sweep baselines at
	// boot unless the operator backfill switch is on (the plan's controlled
	// one-time backfill posture; the digest lanes run through direct
	// runAmbientAgentOnce calls whose baseline is unregistered).
	t.Setenv("ENTITY_LEDGER_BACKFILL", "1")

	fx := buildRecallGoldFixture(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.jsonl")

	var builder strings.Builder
	for _, entry := range recallGoldEntries(fx) {
		line, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("marshal fixture entry %s: %v", entry.ID, err)
		}
		builder.Write(line)
		builder.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	t.Setenv("MEETING_MEMORY_PATH", path)
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))

	return newKanbanBoardApp(), fx
}

/* ---------- deterministic responder ---------- */

type recallGoldRecorder struct {
	mu                 sync.Mutex
	digestCalls        map[string]int
	adjudicationInputs []string
	reflectionInputs   []string
	companyInputs      []string
}

func goldMeetingKeyFromInput(input string) string {
	marker := "# Meeting\nid: "
	start := strings.Index(input, marker)
	if start < 0 {
		return ""
	}
	rest := input[start+len(marker):]
	if end := strings.IndexByte(rest, '\n'); end >= 0 {
		return strings.TrimSpace(rest[:end])
	}
	return strings.TrimSpace(rest)
}

// recallGoldResponder is the hermetic model: canned digest JSON per meeting
// key, "different" verdicts for any adjudication pairs, canned reflection
// and company narrative. Every input is checked against the blob firewall —
// artifact bytes are structurally unreachable by ANY producer prompt.
func recallGoldResponder(t *testing.T, fx *recallGoldFixture, rec *recallGoldRecorder) openAITextResponder {
	return func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if strings.Contains(request.Input, recallGoldBlobMarker) || strings.Contains(request.Input, ";base64,") {
			t.Errorf("artifact blob leaked into a model input:\n%.200s", request.Input)
		}

		rec.mu.Lock()
		defer rec.mu.Unlock()
		switch request.Instructions {
		case meetingDigestInstructions():
			key := goldMeetingKeyFromInput(request.Input)
			rec.digestCalls[key]++
			text, ok := fx.meetingDigestJSON(key)
			if !ok {
				t.Errorf("meeting digest requested for unexpected key %q", key)
				return "", fmt.Errorf("unexpected digest key %q", key)
			}
			return text, nil
		case ledgerAdjudicationInstructions():
			rec.adjudicationInputs = append(rec.adjudicationInputs, request.Input)
			pairs := strings.Count(request.Input, "- i=")
			verdicts := make([]ledgerAdjudicationVerdict, 0, pairs)
			for index := 0; index < pairs; index++ {
				verdicts = append(verdicts, ledgerAdjudicationVerdict{I: index, Verdict: ledgerVerdictDifferent})
			}
			encoded, err := json.Marshal(ledgerAdjudicationOutput{Verdicts: verdicts})
			if err != nil {
				return "", err
			}
			return string(encoded), nil
		case reflectionInstructions():
			rec.reflectionInputs = append(rec.reflectionInputs, request.Input)
			return recallGoldReflectionText, nil
		case companyDigestInstructions():
			rec.companyInputs = append(rec.companyInputs, request.Input)
			return recallGoldCompanyNarrative, nil
		}
		t.Errorf("unexpected model call:\n%.120s", request.Instructions)
		return "", fmt.Errorf("unexpected model call")
	}
}

// runRecallGoldProducers replays the full rollup chain over the fixture:
// brain tier is already in the store; meeting digests → day fold →
// reflection → entity ledger → company digest, exactly the runtime agents.
func runRecallGoldProducers(t *testing.T, app *kanbanBoardApp, fx *recallGoldFixture) *recallGoldRecorder {
	t.Helper()
	rec := &recallGoldRecorder{digestCalls: map[string]int{}}
	responder := recallGoldResponder(t, fx, rec)
	ctx := context.Background()

	// Meeting digests: 4 digest keys under the default 3-meetings-per-tick
	// cap → two productive passes (deferred groups re-feed, never drop); the
	// third pass proves quiescence.
	for pass := 0; pass < 3; pass++ {
		if _, err := app.runAmbientAgentOnce(meetingDigestAgent(), ctx, "test-key", responder, 1); err != nil {
			t.Fatalf("meeting digest pass %d: %v", pass, err)
		}
	}

	// Day fold with the reflection muted: the harness owns WHEN the
	// once-per-local-day reflection fires so the eval is weekday-independent.
	t.Setenv("DAY_REFLECTION_DISABLED", "1")
	if _, err := app.runAmbientAgentOnce(dayDigestAgent(), ctx, "test-key", responder, 1); err != nil {
		t.Fatalf("day digest pass: %v", err)
	}
	os.Unsetenv("DAY_REFLECTION_DISABLED")

	// The A3 reflection, pinned: now = day3 local noon → reflect on day2,
	// whose 7-day lookback window sees the blocker raised on BOTH days.
	if _, appended, err := app.maybeEmitDailyReflection(ctx, "test-key", responder, fx.day3.Add(12*time.Hour).UTC()); err != nil {
		t.Fatalf("reflection: %v", err)
	} else if !appended {
		t.Fatal("reflection for day2 was not appended")
	}

	// Entity ledger consolidation (digest facts + the A9 decision sweep).
	if _, err := app.runAmbientAgentOnce(entityLedgerAgent(), ctx, "test-key", responder, 1); err != nil {
		t.Fatalf("entity ledger pass: %v", err)
	}
	// Company digest from the fresh ledger deltas.
	if _, err := app.runAmbientAgentOnce(companyDigestAgent(), ctx, "test-key", responder, 1); err != nil {
		t.Fatalf("company digest pass: %v", err)
	}

	// Producer-level gold checks.
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.digestCalls) != 4 {
		t.Fatalf("digest keys called = %v, want the 4 fixture keys (A, B, C marathon, legacy)", rec.digestCalls)
	}
	for _, key := range []string{fx.meetingA, fx.meetingB, fx.meetingC, fx.legacyKey} {
		if rec.digestCalls[key] == 0 {
			t.Fatalf("digest producer never ran for %s (calls=%v)", key, rec.digestCalls)
		}
	}
	// A8: the ambiguous warehouse-topic pair costs exactly ONE batched
	// adjudication call for the whole pass, and the "different" verdict must
	// never false-merge — both topics stay separate current records.
	if len(rec.adjudicationInputs) != 1 {
		t.Fatalf("adjudication called %d times, want exactly one batched call per pass", len(rec.adjudicationInputs))
	}
	if !strings.Contains(rec.adjudicationInputs[0], "Warehouse audit staffing") ||
		!strings.Contains(rec.adjudicationInputs[0], "Warehouse audit kickoff") {
		t.Fatalf("adjudication pair should be the warehouse-topic ambiguity:\n%s", rec.adjudicationInputs[0])
	}
	warehouseTitles := map[string]bool{}
	for _, record := range app.searchLedgerRecords("warehouse audit", 6) {
		if record.Entity == ledgerEntityTopic && record.current() {
			warehouseTitles[record.Title] = true
		}
	}
	if !warehouseTitles["Warehouse audit kickoff"] || !warehouseTitles["Warehouse audit staffing"] {
		t.Fatalf("adjudicated-different topics must remain SEPARATE current records, got %v", warehouseTitles)
	}
	if len(rec.companyInputs) == 0 {
		t.Fatal("company digest pass never called the model")
	}
	t.Logf("producers: digestCalls=%v adjudicationCalls=%d reflectionCalls=%d companyCalls=%d",
		rec.digestCalls, len(rec.adjudicationInputs), len(rec.reflectionInputs), len(rec.companyInputs))

	return rec
}

func goldLineWith(text string, substr string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, substr) {
			return line
		}
	}
	return ""
}

func goldAnswer(t *testing.T, app *kanbanBoardApp, query string) string {
	t.Helper()
	result, _, err := app.answerMemoryQuestion(map[string]any{"query": query})
	if err != nil {
		t.Fatalf("answerMemoryQuestion(%q): %v", query, err)
	}
	return asString(result["answer"])
}

/* ---------- the gold eval set (amendment A7) ---------- */

func TestRecallGoldEvalSet(t *testing.T) {
	app, fx := newRecallGoldApp(t)
	fx.guardSameDay(t)
	rec := runRecallGoldProducers(t, app, fx)

	t.Run("G1_what_did_i_miss_this_week_is_comprehensive", func(t *testing.T) {
		fx.guardSameDay(t)
		answer := goldAnswer(t, app, recallGoldWeekQuery)

		// organized briefing, never keyword scraps
		if !strings.Contains(answer, "# What you missed") {
			t.Fatalf("answer is not a briefing:\n%s", answer)
		}
		if strings.Contains(answer, "relevant memory item") {
			t.Fatalf("answer collapsed to the keyword-hit format:\n%s", answer)
		}

		// multi-day: one section per fixture day
		for _, day := range []string{fx.d1, fx.d2, fx.d3} {
			if !strings.Contains(answer, "## "+day) {
				t.Fatalf("briefing missing day section %s:\n%s", day, answer)
			}
		}
		// multi-meeting, including the legacy null-meetingId huddle
		for _, want := range []string{
			"Packaging pilot kickoff (" + fx.meetingA + ")",
			"Pricing sync (" + fx.meetingB + ")",
			"Ops marathon (" + fx.meetingC + ")",
			"Legacy huddle (" + fx.legacyKey + ")",
			"Login outage resolved by Joel",
		} {
			if !strings.Contains(answer, want) {
				t.Fatalf("briefing missing %q:\n%s", want, answer)
			}
		}
		// organized sections with the gold facts
		for _, want := range []string{
			"Decisions:", "Action items:", "Topics:", "Open questions:",
			"Choose vendor Zebra for the packaging pilot",
			"Launch moved to July 31",
			"Draft the pricing sheet",
			"Stripe sandbox access blocked for the payments test",
			"Warehouse audit kickoff",
			"File the warehouse audit findings",
		} {
			if !strings.Contains(answer, want) {
				t.Fatalf("briefing missing %q:\n%s", want, answer)
			}
		}
		// importance-ranked (A4): the [!5] Zebra decision leads the [!4] one
		zebra := strings.Index(answer, "[!5] Choose vendor Zebra for the packaging pilot")
		target := strings.Index(answer, "[!4] Target the launch for July 24")
		if zebra < 0 || target < 0 || zebra > target {
			t.Fatalf("day-1 decisions not importance-ranked (zebra@%d target@%d):\n%s", zebra, target, answer)
		}
		// hedged attribution + verbatim decision ledger
		if !strings.Contains(answer, "attributed to AJ") {
			t.Fatalf("who-said-what is not hedged:\n%s", answer)
		}
		if !strings.Contains(answer, "On the decision ledger (verbatim):") ||
			!strings.Contains(answer, "Launch moved to July 31.") {
			t.Fatalf("active decision-log rows must appear verbatim:\n%s", answer)
		}
		// bounded output, zero artifact bytes
		if len(answer) > 12000 {
			t.Fatalf("briefing answer is %d bytes, want a bounded organized summary", len(answer))
		}
		if strings.Contains(answer, recallGoldBlobMarker) {
			t.Fatal("artifact blob leaked into the briefing answer")
		}
	})

	t.Run("G2_model_input_token_bounded_and_blob_free", func(t *testing.T) {
		fx.guardSameDay(t)
		_, contextEntries := app.memoryMatchesAndContext(recallGoldWeekQuery)
		if len(contextEntries) == 0 {
			t.Fatal("ranged query returned no context")
		}
		// the pinned digest lane leads: day rollups first, oldest day first
		if contextEntries[0].Kind != meetingMemoryKindDayDigest {
			t.Fatalf("context[0] kind=%s, want the day digest lane to lead", contextEntries[0].Kind)
		}
		days := map[string]bool{}
		meetings := map[string]bool{}
		for _, entry := range contextEntries {
			switch entry.Kind {
			case meetingMemoryKindDayDigest:
				days[entry.Metadata[digestDayMetadataKey]] = true
			case meetingMemoryKindMeetingDigest:
				meetings[digestEntryKey(entry)] = true
			}
		}
		for _, day := range []string{fx.d1, fx.d2, fx.d3} {
			if !days[day] {
				t.Fatalf("context missing day digest %s (got %v)", day, days)
			}
		}
		for _, key := range []string{fx.meetingA, fx.meetingB, fx.meetingC, fx.legacyKey} {
			if !meetings[key] {
				t.Fatalf("context missing meeting digest %s (got %v)", key, meetings)
			}
		}
		if len(contextEntries) > defaultMemoryQuestionContextLimit+digestContextMaxEntries {
			t.Fatalf("context has %d entries, exceeding the capped budget", len(contextEntries))
		}

		input := buildMemoryQuestionInput(recallGoldWeekQuery, contextEntries, time.Now())
		// ~40K-token bound approximated by bytes — the regression guard for
		// the observed 2,505,990 > 1,000,000 token 400.
		if len(input) > 160000 {
			t.Fatalf("model input is %d bytes, want < 160000", len(input))
		}
		if strings.Contains(input, recallGoldBlobMarker) || strings.Contains(input, ";base64,") {
			t.Fatal("base64 artifact body reached the model input")
		}
	})

	t.Run("G3_status_of_pricing_sheet_ledger_first", func(t *testing.T) {
		answer := goldAnswer(t, app, "What's the status of the pricing sheet?")
		if !strings.Contains(answer, "Current ledger state:") {
			t.Fatalf("status question must answer from the ledger fold:\n%s", answer)
		}
		line := goldLineWith(answer, "Draft the pricing sheet")
		if line == "" {
			t.Fatalf("ledger answer missing the pricing sheet record:\n%s", answer)
		}
		for _, want := range []string{"status=done", "owner=Tyler", "closed="} {
			if !strings.Contains(line, want) {
				t.Fatalf("pricing sheet line missing %q: %s", want, line)
			}
		}
		if strings.Contains(answer, "# What you missed") {
			t.Fatalf("a status lookup must not return a briefing:\n%s", answer)
		}
	})

	t.Run("G4_launch_decision_current_ranks_above_superseded", func(t *testing.T) {
		answer := goldAnswer(t, app, "What did we decide on the launch date?")
		if !strings.Contains(answer, "Current ledger state:") {
			t.Fatalf("decision question must answer from the ledger fold:\n%s", answer)
		}
		current := strings.Index(answer, "Launch moved to July 31")
		superseded := strings.Index(answer, "Target the launch for July 24")
		if current < 0 || superseded < 0 {
			t.Fatalf("ledger answer must carry both validity windows:\n%s", answer)
		}
		if current > superseded {
			t.Fatalf("the CURRENT decision must rank above the superseded one:\n%s", answer)
		}
		old := goldLineWith(answer, "Target the launch for July 24")
		if !strings.Contains(old, "status=superseded") || !strings.Contains(old, "closed=") {
			t.Fatalf("superseded decision must show its closed validity window: %s", old)
		}
	})

	t.Run("G5_what_changed_between_days", func(t *testing.T) {
		day1Result, _, err := app.crossMeetingBriefingTool(map[string]any{"start_day": fx.d1, "end_day": fx.d1})
		if err != nil {
			t.Fatalf("day-1 briefing: %v", err)
		}
		day2Result, _, err := app.crossMeetingBriefingTool(map[string]any{"start_day": fx.d2, "end_day": fx.d2})
		if err != nil {
			t.Fatalf("day-2 briefing: %v", err)
		}
		day1Briefing := asString(day1Result["briefing"])
		day2Briefing := asString(day2Result["briefing"])

		// Monday: launch targeted July 24, pricing sheet open.
		if !strings.Contains(day1Briefing, "Target the launch for July 24") ||
			strings.Contains(day1Briefing, "Launch moved to July 31") {
			t.Fatalf("day-1 briefing wrong on the launch decision:\n%s", day1Briefing)
		}
		if line := goldLineWith(day1Briefing, "Draft the pricing sheet"); !strings.Contains(line, "status open") {
			t.Fatalf("day-1 pricing sheet should be open: %q\n%s", line, day1Briefing)
		}
		// Tuesday: the change is visible — July 24 superseded, July 31 in,
		// pricing sheet done.
		if line := goldLineWith(day2Briefing, "Target the launch for July 24"); !strings.Contains(line, "status superseded") {
			t.Fatalf("day-2 briefing must show the superseded call: %q\n%s", line, day2Briefing)
		}
		if !strings.Contains(day2Briefing, "Launch moved to July 31") {
			t.Fatalf("day-2 briefing missing the new decision:\n%s", day2Briefing)
		}
		if line := goldLineWith(day2Briefing, "Draft the pricing sheet"); !strings.Contains(line, "status done") {
			t.Fatalf("day-2 pricing sheet should be done: %q\n%s", line, day2Briefing)
		}
		// day scoping: Monday-only material stays out of Tuesday
		if strings.Contains(day2Briefing, "Choose vendor Zebra") {
			t.Fatalf("day-1 decision leaked into the day-2 briefing:\n%s", day2Briefing)
		}
	})

	t.Run("G6_recurring_blockers_reflection", func(t *testing.T) {
		reflections := app.memory.entriesOfKind(meetingMemoryKindReflection, 0)
		if len(reflections) != 1 {
			t.Fatalf("reflections = %d, want exactly one (once per local day)", len(reflections))
		}
		reflection := reflections[0]
		if got := reflection.Metadata[digestDayMetadataKey]; got != fx.d2 {
			t.Fatalf("reflection day = %q, want %q", got, fx.d2)
		}
		if reflection.Metadata["meetingId"] != "" {
			t.Fatalf("reflection must be mint-free (no meetingId): %+v", reflection.Metadata)
		}
		if !strings.Contains(reflection.Text, "Recurring blocker") || !strings.Contains(reflection.Text, "Stripe sandbox") {
			t.Fatalf("reflection missing the recurring-blocker synthesis:\n%s", reflection.Text)
		}
		if strings.TrimSpace(reflection.Metadata["supportingDigests"]) == "" {
			t.Fatal("reflection carries no supporting digest anchors")
		}

		// the reflection INPUT saw the blocker on BOTH days plus the ledger's
		// decision delta — synthesis over the rollups, not a re-summary of raw.
		rec.mu.Lock()
		inputs := append([]string(nil), rec.reflectionInputs...)
		rec.mu.Unlock()
		if len(inputs) != 1 {
			t.Fatalf("reflection model calls = %d, want 1", len(inputs))
		}
		if strings.Count(inputs[0], "Stripe sandbox access") < 2 {
			t.Fatalf("reflection window must surface the blocker from both days:\n%s", inputs[0])
		}
		if !strings.Contains(inputs[0], "Launch moved to July 31") {
			t.Fatalf("reflection window missing the decision delta:\n%s", inputs[0])
		}

		// recall-eligible: keyword search reaches the reflection
		found := false
		for _, match := range app.memory.search("recurring blocker", 8) {
			if match.Entry.ID == reflection.ID {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("reflection is not reachable through keyword recall")
		}
	})

	t.Run("G7_marathon_splits_per_calendar_day", func(t *testing.T) {
		rangeEnd := fx.day3.AddDate(0, 0, 1).Add(-time.Nanosecond)
		folds := map[string]dayDigestPayload{}
		for _, entry := range app.memory.digestsInRange(fx.day1.UTC(), rangeEnd.UTC()) {
			if entry.Kind != meetingMemoryKindDayDigest {
				continue
			}
			var payload dayDigestPayload
			if err := json.Unmarshal([]byte(entry.Text), &payload); err != nil {
				t.Fatalf("day digest %s is not JSON: %v", entry.ID, err)
			}
			folds[entry.Metadata[digestDayMetadataKey]] = payload
		}
		if len(folds) != 3 {
			t.Fatalf("day digests = %d (%v), want one per fixture day", len(folds), folds)
		}

		meetingsOf := func(payload dayDigestPayload) map[string]bool {
			out := map[string]bool{}
			for _, meeting := range payload.Meetings {
				out[meeting.MeetingID] = true
			}
			return out
		}
		day1Meetings := meetingsOf(folds[fx.d1])
		for _, key := range []string{fx.meetingA, fx.meetingC, fx.legacyKey} {
			if !day1Meetings[key] {
				t.Fatalf("day-1 fold missing %s: %v", key, folds[fx.d1].Meetings)
			}
		}
		if day1Meetings[fx.meetingB] {
			t.Fatalf("day-1 fold must not include the day-2 meeting: %v", folds[fx.d1].Meetings)
		}
		// the marathon contributes to every day it touched, via each fact's own stamp
		if line := goldLineWith(mustGoldJSON(t, folds[fx.d1]), "Warehouse audit kickoff"); line == "" {
			t.Fatalf("day-1 fold missing the marathon kickoff: %+v", folds[fx.d1])
		}
		foundOvernight := false
		for _, topic := range folds[fx.d2].Topics {
			if topic.T == "Overnight shift coverage plan" && topic.MeetingID == fx.meetingC {
				foundOvernight = true
			}
		}
		if !foundOvernight {
			t.Fatalf("day-2 fold missing the marathon overnight slice: %+v", folds[fx.d2].Topics)
		}
		foundFindings := false
		for _, action := range folds[fx.d3].ActionItems {
			if action.A == "File the warehouse audit findings" && action.MeetingID == fx.meetingC {
				foundFindings = true
			}
		}
		if !foundFindings {
			t.Fatalf("day-3 fold missing the marathon wrap-up action: %+v", folds[fx.d3].ActionItems)
		}
	})

	t.Run("G8_company_digest_is_current_state_plus_narrative", func(t *testing.T) {
		company, ok := app.memory.latestCompanyDigest()
		if !ok {
			t.Fatal("no company digest")
		}
		payload, parsed := parseCompanyDigest(company.Text)
		if !parsed {
			t.Fatalf("company digest is not JSON:\n%s", company.Text)
		}
		if payload.Narrative != recallGoldCompanyNarrative {
			t.Fatalf("narrative = %q, want the canned thin narrative", payload.Narrative)
		}
		titles := func(records []companyDigestRecord) map[string]bool {
			out := map[string]bool{}
			for _, record := range records {
				out[record.Title] = true
			}
			return out
		}
		decisions := titles(payload.State.Decisions)
		if !decisions["Launch moved to July 31"] || !decisions["Choose vendor Zebra for the packaging pilot"] {
			t.Fatalf("company state missing current decisions: %v", decisions)
		}
		if decisions["Target the launch for July 24"] {
			t.Fatalf("closed decision leaked into the CURRENT company state: %v", decisions)
		}
		actions := titles(payload.State.ActionItems)
		if actions["Draft the pricing sheet"] {
			t.Fatalf("done action leaked into the CURRENT company state: %v", actions)
		}
		if !actions["File the warehouse audit findings"] {
			t.Fatalf("company state missing the open action: %v", actions)
		}
		if questions := titles(payload.State.OpenQuestions); !questions["Stripe sandbox access blocked for the payments test"] {
			t.Fatalf("company state missing the standing blocker: %v", questions)
		}

		// T4 never re-summarizes rollups: its input is ledger state/deltas
		rec.mu.Lock()
		companyInput := rec.companyInputs[0]
		rec.mu.Unlock()
		if strings.Contains(companyInput, "## Overview") {
			t.Fatalf("brain markdown reached the company digest prompt:\n%s", companyInput)
		}
		if !strings.Contains(companyInput, "Launch moved to July 31") {
			t.Fatalf("company digest prompt missing the ledger delta:\n%s", companyInput)
		}

		// generic standing question answers from the same fold, current-only
		answer := goldAnswer(t, app, "Where do we stand?")
		if !strings.Contains(answer, "Current ledger state:") || !strings.Contains(answer, "Launch moved to July 31") {
			t.Fatalf("generic state question must answer from the ledger:\n%s", answer)
		}
		if strings.Contains(answer, "Target the launch for July 24") {
			t.Fatalf("closed record leaked into the generic current-state answer:\n%s", answer)
		}
	})

	t.Run("G9_anchor_drills_to_verbatim", func(t *testing.T) {
		detail, _, err := app.getMeetingDetail(map[string]any{"anchor": "tx-b1"})
		if err != nil {
			t.Fatalf("getMeetingDetail: %v", err)
		}
		verbatim := asString(detail["verbatim"])
		if !strings.Contains(verbatim, "«anchor»") || !strings.Contains(verbatim, "The launch moves out a week to July 31.") {
			t.Fatalf("anchor did not resolve to the verbatim exchange:\n%s", verbatim)
		}
		if !strings.Contains(verbatim, "Stripe sandbox access is still blocked.") {
			t.Fatalf("anchor window missing the same-meeting neighbor:\n%s", verbatim)
		}
		if strings.Contains(verbatim, "vendor Zebra") {
			t.Fatalf("anchor window crossed into another meeting:\n%s", verbatim)
		}
		if got := asString(detail["meetingId"]); got != fx.meetingB {
			t.Fatalf("anchor resolved meeting %q, want %q", got, fx.meetingB)
		}
		if digest := asString(detail["digest"]); !strings.Contains(digest, "Launch moved to July 31") {
			t.Fatalf("drill-down missing the meeting digest:\n%s", digest)
		}
	})

	t.Run("G10_no_range_query_keeps_summary_layer", func(t *testing.T) {
		_, contextEntries := app.memoryMatchesAndContext("tell me about the packaging pilot")
		kinds := map[string]int{}
		for _, entry := range contextEntries {
			kinds[entry.Kind]++
		}
		if kinds[meetingMemoryKindCompanyDigest] == 0 {
			t.Fatalf("no-range context missing the company digest: %v", kinds)
		}
		if kinds[meetingMemoryKindMeetingDigest] == 0 {
			t.Fatalf("no-range context missing meeting digests: %v", kinds)
		}
		if kinds[meetingMemoryKindBrain] == 0 {
			t.Fatalf("no-range context lost the brain layer: %v", kinds)
		}
	})
}

func mustGoldJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(encoded)
}

/* ---------- pre-backfill outage: map-reduce, never keyword scraps ---------- */

// TestRecallGoldMapReduceFallbackWhenDigestsMissing replays the flagship
// query against the SAME fixture with NO digests produced and the primary
// recall model erroring — the answer must be a fresh map-reduce briefing
// composed with the producer's own contract (blob-firewalled, bounded map
// inputs), spanning the same days and meetings.
func TestRecallGoldMapReduceFallbackWhenDigestsMissing(t *testing.T) {
	app, fx := newRecallGoldApp(t)
	fx.guardSameDay(t)
	app.apiKey = "test-key" // arm the map-reduce lane (keyless keeps keyword last resort)

	var mu sync.Mutex
	var mapInputs []string
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		switch request.Instructions {
		case memoryQuestionInstructions():
			return "", fmt.Errorf("primary recall model outage")
		case meetingDigestInstructions():
			mu.Lock()
			mapInputs = append(mapInputs, request.Input)
			mu.Unlock()
			key := goldMeetingKeyFromInput(request.Input)
			text, ok := fx.meetingDigestJSON(key)
			if !ok {
				return "", fmt.Errorf("unexpected map key %q", key)
			}
			return text, nil
		default:
			return "", fmt.Errorf("unexpected model call: %.80s", request.Instructions)
		}
	})

	answer := goldAnswer(t, app, recallGoldWeekQuery)
	for _, want := range []string{
		"# What you missed",
		"Composed on demand",
		"Choose vendor Zebra for the packaging pilot",
		"File the warehouse audit findings",
		"Login outage resolved by Joel",
		"## " + fx.d1,
		"## " + fx.d3,
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("map-reduce briefing missing %q:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "relevant memory item") {
		t.Fatalf("fallback collapsed to keyword scraps:\n%s", answer)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(mapInputs) < 4 {
		t.Fatalf("map calls = %d, want one per fixture meeting (incl. the legacy group)", len(mapInputs))
	}
	for _, input := range mapInputs {
		if strings.Contains(input, recallGoldBlobMarker) || strings.Contains(input, ";base64,") {
			t.Fatal("artifact blob leaked into a map input")
		}
		if len(input) > mapReduceChunkBudgetChars+4096 {
			t.Fatalf("map input %d chars exceeds the bounded chunk budget", len(input))
		}
	}
}

/* ---------- prod replay sanity (operator-triggered, read-only) ---------- */

// TestRecallProdReplaySanity replays a READ-ONLY copy of the production
// meeting-memory.jsonl (see the file header for the scp line; the copy is
// never committed). Skipped unless T2_REPLAY_JSONL points at the file. The
// store is loaded from a TEMP COPY so nothing ever writes back to the
// replay file, let alone prod.
func TestRecallProdReplaySanity(t *testing.T) {
	source := strings.TrimSpace(os.Getenv("T2_REPLAY_JSONL"))
	if source == "" {
		t.Skip("T2_REPLAY_JSONL not set — prod replay is an operator-triggered sanity check")
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read replay file: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "memory.jsonl")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("copy replay file: %v", err)
	}
	t.Setenv("MEETING_MEMORY_PATH", path)
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
	t.Setenv("ANTHROPIC_API_KEY", "")
	app := newKanbanBoardApp()

	entries := app.memory.snapshot(0)
	matches, contextEntries := app.memoryMatchesAndContext(recallGoldWeekQuery)
	input := buildMemoryQuestionInput(recallGoldWeekQuery, contextEntries, time.Now())
	t.Logf("prod replay: %d visible entries, %d matches, %d context entries, %d input bytes",
		len(entries), len(matches), len(contextEntries), len(input))

	// the regression that started Track 2: 2,505,990 tokens > the 1M budget.
	// ~4 bytes/token puts 600KB well under 200K tokens — an order of
	// magnitude of headroom, yet loose enough for real transcript density.
	if len(input) > 600000 {
		t.Fatalf("recall input is %d bytes — the token budget is at risk", len(input))
	}
	if strings.Contains(input, ";base64,") {
		t.Fatal("a base64 artifact body reached the recall input")
	}
	for _, entry := range contextEntries {
		if len(entry.Text) > maxPromptBodyBytes+4096 {
			t.Fatalf("context entry %s (%s) carries %d bytes past the prompt-body cap", entry.ID, entry.Kind, len(entry.Text))
		}
	}
	if len(contextEntries) == 0 && len(matches) == 0 {
		t.Fatal("prod replay yielded no recall material at all")
	}
}
