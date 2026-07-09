package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

/* ---------- helpers ---------- */

// cannedBriefingDigestJSON is a mapped/stored digest body whose facts carry
// NO per-fact `at` stamps, so every fact falls onto the digest's own day
// metadata — the stable fixture for map-reduce tests where the day is derived
// from the source window at runtime.
func cannedBriefingDigestJSON() string {
	return `{"meetingId":"model-wrong","title":"Rollout sync","day":"1999-01-01",` +
		`"topics":[{"t":"Rollout timeline","anchor":"tx-1","importance":3}],` +
		`"decisions":[{"d":"Ship the rollout Friday","by":"attributed to AJ","status":"decided","anchor":"tx-1","importance":5}],` +
		`"actionItems":[{"a":"Draft the rollout memo","owner":"Tyler","status":"open","importance":4}],` +
		`"openQuestions":[{"q":"Who owns support?","importance":2}],` +
		`"themes":["rollout"]}`
}

func upsertBriefingTestDigest(t *testing.T, app *kanbanBoardApp, key string, text string, day string, spanStart string, spanEnd string) {
	t.Helper()
	metadata := map[string]string{
		"meetingId":                key,
		digestDayMetadataKey:       day,
		digestSpanStartMetadataKey: spanStart,
		digestSpanEndMetadataKey:   spanEnd,
	}
	if _, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, key, text, metadata); err != nil {
		t.Fatalf("upsertDigest %s: %v", key, err)
	}
}

/* ---------- deterministic composition ---------- */

// TestCrossMeetingBriefingGroupsByDayAndMeeting is the Wave-6 flagship for
// the deterministic path: facts from TWO meetings regroup onto local calendar
// days (a fact's own `at` stamp splits a multi-day meeting across days),
// importance leads inside each section, every fact carries its source
// meeting, and the active decision ledger is quoted verbatim on its day.
func TestCrossMeetingBriefingGroupsByDayAndMeeting(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)
	location := meetingTimeLocation()

	digestA := `{"meetingId":"meeting-a","title":"Packaging pilot","day":"2026-06-01",` +
		`"decisions":[` +
		`{"d":"Delay the launch a week","status":"decided","at":"2026-06-02T18:00:00Z","importance":3},` +
		`{"d":"Choose vendor Zebra","by":"attributed to AJ","status":"decided","anchor":"tx-a1","at":"2026-06-01T18:00:00Z","importance":5}],` +
		`"actionItems":[{"a":"Draft pricing sheet","owner":"Tyler","status":"open","at":"2026-06-01T19:00:00Z","importance":4}],` +
		`"themes":["packaging"]}`
	upsertBriefingTestDigest(t, app, "meeting-a", digestA, "2026-06-02", "2026-06-01T17:00:00Z", "2026-06-02T19:00:00Z")

	digestB := `{"meetingId":"meeting-b","title":"Design sync","day":"2026-06-01",` +
		`"topics":[{"t":"Ember palette refresh","at":"2026-06-01T20:00:00Z","importance":2}],` +
		`"openQuestions":[{"q":"Font licensing?","at":"2026-06-01T20:30:00Z","importance":3}]}`
	upsertBriefingTestDigest(t, app, "meeting-b", digestB, "2026-06-01", "2026-06-01T19:30:00Z", "2026-06-01T21:00:00Z")

	// active ledger decision recorded on day 2 — quoted verbatim, never folded.
	app.memory.entries = append(app.memory.entries, meetingMemoryEntry{
		ID:        "ledger-decision-1",
		Kind:      meetingMemoryKindDecision,
		Text:      "We will ship the rollout on Friday, owned by Tyler.",
		CreatedAt: time.Date(2026, 6, 2, 18, 30, 0, 0, time.UTC),
		Metadata:  map[string]string{"status": decisionStatusActive},
	})
	// superseded decisions never ride into a briefing.
	app.memory.entries = append(app.memory.entries, meetingMemoryEntry{
		ID:        "ledger-decision-stale",
		Kind:      meetingMemoryKindDecision,
		Text:      "Old superseded call that must not appear.",
		CreatedAt: time.Date(2026, 6, 2, 18, 31, 0, 0, time.UTC),
		Metadata:  map[string]string{"status": "superseded"},
	})

	rangeStart := time.Date(2026, 6, 1, 0, 0, 0, 0, location)
	briefing := app.composeCrossMeetingBriefing(rangeStart.UTC(), rangeStart.AddDate(0, 0, 3).UTC())

	if briefing.Source != briefingSourceDigests {
		t.Fatalf("source=%q, want %q", briefing.Source, briefingSourceDigests)
	}
	if briefing.DigestedMeetings != 2 {
		t.Fatalf("DigestedMeetings=%d, want 2", briefing.DigestedMeetings)
	}
	if len(briefing.Days) != 2 || briefing.Days[0].Day != "2026-06-01" || briefing.Days[1].Day != "2026-06-02" {
		t.Fatalf("days=%+v, want 2026-06-01 then 2026-06-02", briefing.Days)
	}

	day1 := briefing.Days[0]
	if !day1.HasFold || len(day1.Fold.Meetings) != 2 {
		t.Fatalf("day1 meetings=%+v, want both meeting-a and meeting-b", day1.Fold.Meetings)
	}
	if len(day1.Fold.Decisions) != 1 || day1.Fold.Decisions[0].D != "Choose vendor Zebra" || day1.Fold.Decisions[0].MeetingID != "meeting-a" {
		t.Fatalf("day1 decisions=%+v, want the day-1 Zebra decision with meeting-a provenance", day1.Fold.Decisions)
	}
	if len(day1.Fold.Topics) != 1 || day1.Fold.Topics[0].MeetingID != "meeting-b" {
		t.Fatalf("day1 topics=%+v, want the meeting-b topic", day1.Fold.Topics)
	}

	day2 := briefing.Days[1]
	if !day2.HasFold || len(day2.Fold.Decisions) != 1 || day2.Fold.Decisions[0].D != "Delay the launch a week" {
		t.Fatalf("day2 decisions=%+v, want the day-2 slice of meeting-a", day2.Fold.Decisions)
	}
	if len(day2.LedgerDecisions) != 1 || day2.LedgerDecisions[0] != "We will ship the rollout on Friday, owned by Tyler." {
		t.Fatalf("day2 ledger=%+v, want the verbatim active decision", day2.LedgerDecisions)
	}

	text := renderCrossMeetingBriefing(briefing)
	for _, want := range []string{
		"## 2026-06-01 — Packaging pilot (meeting-a), Design sync (meeting-b)",
		"[!5] Choose vendor Zebra (attributed to AJ; status decided; meeting-a)",
		"[!4] Draft pricing sheet (owner Tyler; status open; meeting-a)",
		"## 2026-06-02",
		"We will ship the rollout on Friday, owned by Tyler.",
		"Themes: packaging",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("briefing missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Old superseded call") {
		t.Fatalf("superseded ledger decision leaked into the briefing:\n%s", text)
	}
}

/* ---------- drill-down ---------- */

func TestGetMeetingDetailDrillsToVerbatim(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	appendTestTranscript(t, app, "tx-1", "We are kicking off the packaging pilot.")
	appendTestTranscript(t, app, "tx-2", "We choose vendor Zebra for the pilot.")
	appendTestTranscript(t, app, "tx-3", "Tyler will draft the pricing sheet.")
	meetingID := app.memory.currentMeetingID(officeRoomID)
	if meetingID == "" {
		t.Fatal("expected a minted meeting id")
	}
	if _, _, err := app.memory.appendBrainWriteUp("brain-1", "## Overview\nVendor Zebra chosen for the pilot.", map[string]string{"meetingId": meetingID}); err != nil {
		t.Fatalf("append brain: %v", err)
	}
	digest := `{"meetingId":"` + meetingID + `","title":"Packaging pilot","day":"2026-06-01",` +
		`"decisions":[{"d":"Choose vendor Zebra","anchor":"tx-2","importance":5}]}`
	upsertBriefingTestDigest(t, app, meetingID, digest, "2026-06-01", "2026-06-01T17:00:00Z", "2026-06-01T19:00:00Z")

	result, _, err := app.getMeetingDetail(map[string]any{"meeting_id": meetingID})
	if err != nil {
		t.Fatalf("getMeetingDetail: %v", err)
	}
	if !strings.Contains(asString(result["digest"]), "Choose vendor Zebra") {
		t.Fatalf("digest missing: %+v", result)
	}
	brains, ok := result["brains"].([]string)
	if !ok || len(brains) != 1 || !strings.Contains(brains[0], "Vendor Zebra chosen") {
		t.Fatalf("brains=%+v, want the write-up", result["brains"])
	}

	// anchor drill-down: the verbatim exchange plus neighbors, anchor marked.
	result, _, err = app.getMeetingDetail(map[string]any{"meeting_id": meetingID, "anchor": "tx-2"})
	if err != nil {
		t.Fatalf("getMeetingDetail anchor: %v", err)
	}
	verbatim := asString(result["verbatim"])
	for _, want := range []string{"kicking off the packaging pilot", "choose vendor Zebra", "draft the pricing sheet", "«anchor»"} {
		if !strings.Contains(verbatim, want) {
			t.Fatalf("verbatim missing %q:\n%s", want, verbatim)
		}
	}

	// anchor alone resolves its meeting.
	result, _, err = app.getMeetingDetail(map[string]any{"anchor": "tx-2"})
	if err != nil {
		t.Fatalf("getMeetingDetail anchor-only: %v", err)
	}
	if asString(result["meetingId"]) != meetingID {
		t.Fatalf("meetingId=%q, want %q", asString(result["meetingId"]), meetingID)
	}

	if _, _, err := app.getMeetingDetail(map[string]any{"meeting_id": "meeting-nope"}); err == nil {
		t.Fatal("unknown meeting must error, not fabricate detail")
	}
	if _, _, err := app.getMeetingDetail(map[string]any{}); err == nil {
		t.Fatal("missing meeting_id and anchor must error")
	}
}

/* ---------- range parsing ---------- */

func TestBriefingRangeFromArgs(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	location := meetingTimeLocation()
	now := time.Date(2026, 6, 5, 10, 0, 0, 0, location) // Friday

	start, end, label, err := briefingRangeFromArgs(map[string]any{"start_day": "2026-06-01", "end_day": "2026-06-03"}, now)
	if err != nil {
		t.Fatalf("explicit days: %v", err)
	}
	if label != "2026-06-01 to 2026-06-03" {
		t.Fatalf("label=%q", label)
	}
	wantStart := time.Date(2026, 6, 1, 0, 0, 0, 0, location)
	if !start.Equal(wantStart) || !end.Equal(wantStart.AddDate(0, 0, 3)) {
		t.Fatalf("range=[%s, %s], want [%s, %s] (end_day inclusive)", start, end, wantStart, wantStart.AddDate(0, 0, 3))
	}

	start, end, _, err = briefingRangeFromArgs(map[string]any{}, now)
	if err != nil {
		t.Fatalf("default range: %v", err)
	}
	monday := time.Date(2026, 6, 1, 0, 0, 0, 0, location)
	if !start.Equal(monday) || !end.Equal(monday.AddDate(0, 0, 7)) {
		t.Fatalf("default=[%s, %s], want this week", start, end)
	}

	if _, _, _, err := briefingRangeFromArgs(map[string]any{"start_day": "junk"}, now); err == nil {
		t.Fatal("bad start_day must error")
	}
	if _, _, _, err := briefingRangeFromArgs(map[string]any{"start_day": "2026-06-03", "end_day": "2026-06-01"}, now); err == nil {
		t.Fatal("end before start must error")
	}
	if _, _, _, err := briefingRangeFromArgs(map[string]any{"range": "the vibes"}, now); err == nil {
		t.Fatal("unparseable range phrase must error")
	}
}

/* ---------- fallback: briefing, not eight keyword hits ---------- */

// TestAnswerMemoryQuestionFallbackIsBriefingNotEightHits: a model outage on a
// time-ranged recall query now degrades to the composed digest briefing —
// never to buildMemoryAnswer's keyword scraps (the pre-Track-2 silent
// collapse).
func TestAnswerMemoryQuestionFallbackIsBriefingNotEightHits(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	app := newIsolatedKanbanBoardApp(t) // keyless: the primary model path errors

	now := time.Now()
	today := dayBucket(now)
	digest := `{"meetingId":"meeting-today","title":"Rollout sync","day":"` + today + `",` +
		`"decisions":[{"d":"Ship the rollout Friday","by":"attributed to AJ","status":"decided","importance":5}],` +
		`"actionItems":[{"a":"Draft the rollout memo","owner":"Tyler","status":"open","importance":4}]}`
	// spanEnd = now so the digest overlaps "today" even right after local
	// midnight (a now-1h span end would fall on yesterday and flake).
	upsertBriefingTestDigest(t, app, "meeting-today", digest, today,
		now.Add(-2*time.Hour).UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))

	result, _, err := app.answerMemoryQuestion(map[string]any{"query": "what did I miss today?"})
	if err != nil {
		t.Fatalf("answerMemoryQuestion: %v", err)
	}
	answer := asString(result["answer"])
	for _, want := range []string{"# What you missed", "## " + today, "Ship the rollout Friday"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("answer missing %q — the ranged fallback must be a briefing:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "relevant memory item") {
		t.Fatalf("answer collapsed to the keyword-hit format:\n%s", answer)
	}
}

// TestAnswerMemoryQuestionRangedFallbackMapReducesWhenDigestsMissing: the
// same outage with NO digest coverage composes fresh via the on-demand
// map-reduce (bounded map calls with the producer's own digest contract).
func TestAnswerMemoryQuestionRangedFallbackMapReducesWhenDigestsMissing(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	app := newIsolatedKanbanBoardApp(t)
	app.apiKey = "test-key"
	appendTestTranscript(t, app, "tx-1", "We choose vendor Zebra for the packaging pilot.")
	appendTestTranscript(t, app, "tx-2", "Tyler will draft the pricing sheet by Friday.")

	mapCalls := 0
	swapOpenAITextResponder(t, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		switch request.Instructions {
		case memoryQuestionInstructions():
			return "", fmt.Errorf("primary recall model outage")
		case meetingDigestInstructions():
			mapCalls++
			if !strings.Contains(request.Input, "vendor Zebra") {
				t.Errorf("map input missing the raw transcript material: %s", request.Input)
			}
			return cannedBriefingDigestJSON(), nil
		default:
			t.Errorf("unexpected model call instructions: %s", request.Instructions)
			return "", fmt.Errorf("unexpected call")
		}
	})

	result, _, err := app.answerMemoryQuestion(map[string]any{"query": "what did I miss today?"})
	if err != nil {
		t.Fatalf("answerMemoryQuestion: %v", err)
	}
	if mapCalls == 0 {
		t.Fatal("map-reduce fallback never ran")
	}
	answer := asString(result["answer"])
	for _, want := range []string{"# What you missed", "Ship the rollout Friday", "Composed on demand"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("answer missing %q:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "relevant memory item") {
		t.Fatalf("answer collapsed to the keyword-hit format:\n%s", answer)
	}
}

// TestRangedFallbackKeepsKeywordLastResort: keyless AND digest-less, the old
// keyword answer remains the true last resort (never a fabricated briefing).
func TestRangedFallbackKeepsKeywordLastResort(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	app := newIsolatedKanbanBoardApp(t)
	appendTestTranscript(t, app, "tx-1", "Boot Barn kickoff planning notes.")

	result, _, err := app.answerMemoryQuestion(map[string]any{"query": "what did I miss today?"})
	if err != nil {
		t.Fatalf("answerMemoryQuestion: %v", err)
	}
	answer := asString(result["answer"])
	if strings.Contains(answer, "# What you missed") {
		t.Fatalf("empty stores must not fabricate a briefing:\n%s", answer)
	}
	if answer == "" {
		t.Fatal("last-resort answer must not be empty")
	}
}
