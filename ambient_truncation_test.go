package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// truncatingResponder fails the first `truncations` calls with
// max_output_truncation and then answers with `success`, recording every
// request it saw.
func truncatingResponder(truncations int, success func(request openAITextRequest) string) (openAITextResponder, *[]openAITextRequest) {
	seen := &[]openAITextRequest{}
	return func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		*seen = append(*seen, request)
		if len(*seen) <= truncations {
			return "", &openAIOutputRejection{reason: ambientTruncationReason}
		}
		return success(request), nil
	}, seen
}

func appendTruncationBrains(t *testing.T, app *kanbanBoardApp, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, appended, err := app.memory.appendBrainWriteUp(id, "## Overview\nToken "+strings.ToUpper(id)+" was discussed.", nil); err != nil || !appended {
			t.Fatalf("append %s: appended=%v err=%v", id, appended, err)
		}
	}
}

func TestAmbientTruncationRetryPlan(t *testing.T) {
	if keep, budget, ok := ambientTruncationRetryPlan(3, 900); !ok || keep != 2 || budget != 900 {
		t.Fatalf("3 inputs: keep=%d budget=%d ok=%v, want the oldest 2 at the same budget", keep, budget, ok)
	}
	if keep, budget, ok := ambientTruncationRetryPlan(2, 900); !ok || keep != 1 || budget != 900 {
		t.Fatalf("2 inputs: keep=%d budget=%d ok=%v, want 1", keep, budget, ok)
	}
	if keep, budget, ok := ambientTruncationRetryPlan(1, 900); !ok || keep != 1 || budget != 1350 {
		t.Fatalf("1 input: keep=%d budget=%d ok=%v, want the budget raised 1.5x", keep, budget, ok)
	}
	if _, budget, ok := ambientTruncationRetryPlan(1, ambientTruncationRetryOutputTokenCap); ok || budget != ambientTruncationRetryOutputTokenCap {
		t.Fatalf("1 input at the cap: budget=%d ok=%v, want no second identical attempt", budget, ok)
	}
	if _, _, ok := ambientTruncationRetryPlan(1, 0); ok {
		t.Fatal("provider-default budget cannot be raised")
	}
}

// A look-back's one retry must be materially different in BOTH dimensions:
// the reflection's failure was output-envelope exhaustion, and a second
// truncation abandons that day permanently, so halving the window at an
// unchanged budget would spend the retry on the same envelope. The chassis
// plan (oldest half, pinned budget) is untouched.
func TestAmbientTruncationLookbackRetryPlanRaisesBudget(t *testing.T) {
	raised := ambientTruncationRaisedOutputBudget(reflectionMaxOutputTokens)
	if raised <= reflectionMaxOutputTokens {
		t.Fatalf("raised=%d, want headroom above %d", raised, reflectionMaxOutputTokens)
	}
	if keep, budget, ok := ambientTruncationLookbackRetryPlan(3, reflectionMaxOutputTokens); !ok || keep != 2 || budget != raised {
		t.Fatalf("look-back 3 inputs: keep=%d budget=%d ok=%v, want the newest 2 at the raised budget %d", keep, budget, ok, raised)
	}
	if _, budget, ok := ambientTruncationRetryPlan(3, reflectionMaxOutputTokens); !ok || budget != reflectionMaxOutputTokens {
		t.Fatalf("chassis 3 inputs: budget=%d ok=%v, want the pinned budget unchanged", budget, ok)
	}
	// a lone input keeps the single-input contract (raise once, then stuck).
	if keep, budget, ok := ambientTruncationLookbackRetryPlan(1, reflectionMaxOutputTokens); !ok || keep != 1 || budget != raised {
		t.Fatalf("look-back 1 input: keep=%d budget=%d ok=%v", keep, budget, ok)
	}
	if _, budget, ok := ambientTruncationLookbackRetryPlan(1, ambientTruncationRetryOutputTokenCap); ok || budget != ambientTruncationRetryOutputTokenCap {
		t.Fatalf("look-back 1 input at the cap: budget=%d ok=%v, want no second identical attempt", budget, ok)
	}
}

// Mission intelligence: a truncated window retries once with the OLDEST half
// (cursor stays contiguous), the insight lands with the halved cursor, and the
// dropped newer half re-feeds on the next pass.
func TestMissionIntelTruncationHalvesWindowThenLands(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	appendTruncationBrains(t, app, "brain-1", "brain-2", "brain-3")

	responder, seen := truncatingResponder(1, func(openAITextRequest) string {
		return `{"themes":[],"openQuestions":[],"alignments":["Recovered."]}`
	})
	entry, err := app.runAmbientAgentOnce(missionIntelligenceAgent(), context.Background(), "test-key", responder, 1)
	if err != nil {
		t.Fatalf("mission pass: %v", err)
	}
	if len(*seen) != 2 {
		t.Fatalf("provider calls=%d, want exactly one retry", len(*seen))
	}
	first, retry := (*seen)[0], (*seen)[1]
	if !strings.Contains(first.Input, "BRAIN-3") || strings.Contains(retry.Input, "BRAIN-3") || !strings.Contains(retry.Input, "BRAIN-2") {
		t.Fatalf("retry must drop the newest half: first=%q retry=%q", first.Input, retry.Input)
	}
	if retry.MaxOutputTokens != missionIntelMaxOutputTokens {
		t.Fatalf("multi-input retry changed the budget to %d", retry.MaxOutputTokens)
	}
	if entry.Kind != meetingMemoryKindMissionInsight || entry.Metadata["throughBrainId"] != "brain-2" {
		t.Fatalf("insight=%+v, want the cursor stamped at the halved window's last brain", entry)
	}
	if deadLetters := app.memory.entriesOfKind(meetingMemoryKindDeadLetter, 0); len(deadLetters) != 0 {
		t.Fatalf("a recovered truncation left dead letters: %+v", deadLetters)
	}

	// the newer half re-feeds
	next, err := app.runAmbientAgentOnce(missionIntelligenceAgent(), context.Background(), "test-key", responder, 1)
	if err != nil || next.Metadata["throughBrainId"] != "brain-3" {
		t.Fatalf("next pass entry=%+v err=%v, want brain-3 consumed", next, err)
	}
	if last := (*seen)[len(*seen)-1]; strings.Contains(last.Input, "BRAIN-1") || !strings.Contains(last.Input, "BRAIN-3") {
		t.Fatalf("next window re-fed consumed brains: %q", last.Input)
	}
}

// Mission intelligence: truncation on the retry too skips the head input —
// baseline advanced, tombstone with the reason, stuckInputs on the snapshot —
// and NOTHING counts toward the provider circuit (production wedged this
// worker behind a four-strike circuit on exactly this input shape).
func TestMissionIntelTruncationTwiceSkipsHeadWithoutOpeningCircuit(t *testing.T) {
	resetCapabilityRuntimeForTest(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	appendTruncationBrains(t, app, "brain-1", "brain-2")

	agent := missionIntelligenceAgent()
	key := ambientAgentKey(agent.name, officeRoomID)
	responder, seen := truncatingResponder(99, func(openAITextRequest) string { return "" })
	entry, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "test-key", responder, 1, officeRoomID)
	if err != nil {
		t.Fatalf("guarded pass returned %v, want a clean skip", err)
	}
	if entry.ID != "" || len(*seen) != 2 {
		t.Fatalf("entry=%+v calls=%d, want no artifact after exactly two attempts", entry, len(*seen))
	}
	app.mu.Lock()
	failure := app.agentFailures[key]
	app.mu.Unlock()
	if failure != nil {
		t.Fatalf("truncation skip recorded a circuit failure: %+v", failure)
	}
	if baseline := app.ambientAgentBaselineID(key); baseline != "brain-1" {
		t.Fatalf("baseline=%q, want advanced past the stuck head brain-1", baseline)
	}
	checkpoint, ok, err := app.ambientScopeCheckpoint(key)
	if err != nil || !ok || checkpoint.BaselineID != "brain-1" || checkpoint.WindowID != "" {
		t.Fatalf("durable checkpoint=%+v ok=%v err=%v, want baseline brain-1 with no held window", checkpoint, ok, err)
	}
	deadLetters := app.memory.entriesOfKind(meetingMemoryKindDeadLetter, 0)
	if len(deadLetters) != 1 || deadLetters[0].Metadata[deadLetterReasonMetadataKey] != ambientTruncationReason || deadLetters[0].Metadata[deadLetterAgentMetadataKey] != agent.name {
		t.Fatalf("dead letters=%+v, want one stuck-input tombstone stamped %s", deadLetters, ambientTruncationReason)
	}
	health := ambientWorkerCapabilitySnapshot(agent, time.Now().UTC(), providerOpenAI, true)
	if health["stuckInputs"] != 1 || health["deadLetter"] != 1 {
		t.Fatalf("snapshot stuckInputs=%v deadLetter=%v, want 1/1", health["stuckInputs"], health["deadLetter"])
	}
	if circuit, _ := health["circuit"].(string); circuit != "" && circuit != "closed" {
		t.Fatalf("snapshot circuit=%q after a truncation skip, want closed", circuit)
	}

	// the remainder runs on the next pass
	success, seenNext := truncatingResponder(0, func(openAITextRequest) string {
		return `{"themes":[],"openQuestions":[],"alignments":["Recovered."]}`
	})
	next, err := app.invokeAmbientAgentGuarded(agent, context.Background(), "test-key", success, 1, officeRoomID)
	if err != nil || next.Metadata["throughBrainId"] != "brain-2" {
		t.Fatalf("next pass entry=%+v err=%v, want brain-2 consumed", next, err)
	}
	if strings.Contains((*seenNext)[0].Input, "BRAIN-1") {
		t.Fatalf("skipped head re-fed: %q", (*seenNext)[0].Input)
	}
}

func TestNarrativeTruncationHalvesThenSkips(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	app := newIsolatedKanbanBoardApp(t)
	appendTruncationBrains(t, app, "brain-1", "brain-2", "brain-3")

	responder, seen := truncatingResponder(1, func(openAITextRequest) string {
		return `{"narratives":[{"slug":"samsung-tv-plus","title":"Samsung TV Plus","status":"Pitch delivered","body":"## Samsung TV Plus\n\nThe pitch landed and the bundle is under review by the buyer this week."}]}`
	})
	entry, err := app.runAmbientAgentOnce(narrativeMaintainerAgent(), context.Background(), "test-key", responder, 1)
	if err != nil {
		t.Fatalf("narrative pass: %v", err)
	}
	if len(*seen) != 2 || strings.Contains((*seen)[1].Input, "BRAIN-3") {
		t.Fatalf("calls=%d retry=%q, want one retry over the oldest half", len(*seen), (*seen)[1].Input)
	}
	if entry.Kind != meetingMemoryKindNarrative || entry.Metadata[narrativeCursorKey] != "brain-2" {
		t.Fatalf("narrative=%+v, want cursor brain-2", entry)
	}

	// brain-3 alone: a single input raises the budget once, then is skipped
	always, seenAlways := truncatingResponder(99, func(openAITextRequest) string { return "" })
	skipped, err := app.runAmbientAgentOnce(narrativeMaintainerAgent(), context.Background(), "test-key", always, 1)
	if err != nil || skipped.ID != "" {
		t.Fatalf("skip pass entry=%+v err=%v, want clean no-artifact", skipped, err)
	}
	if len(*seenAlways) != 2 || (*seenAlways)[1].MaxOutputTokens != ambientTruncationRaisedOutputBudget(narrativeMaintainerMaxOutputTokens) || (*seenAlways)[1].MaxOutputTokens != ambientTruncationRetryOutputTokenCap {
		t.Fatalf("single-input retry calls=%d budget=%d, want one retry at the capped raise (%d)", len(*seenAlways), (*seenAlways)[1].MaxOutputTokens, ambientTruncationRetryOutputTokenCap)
	}
	if baseline := app.ambientAgentBaselineID(narrativeMaintainerAgentName); baseline != "brain-3" {
		t.Fatalf("baseline=%q, want brain-3 skipped", baseline)
	}
	if app.memory.countStuckInputs(narrativeMaintainerAgentName) != 1 {
		t.Fatal("stuck input not counted for the narrative maintainer")
	}
}

func TestDecisionLedgerTruncationHalvesThenSkips(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	appendTruncationBrains(t, app, "brain-1", "brain-2")

	responder, seen := truncatingResponder(1, func(openAITextRequest) string {
		return `{"decisions":[{"statement":"Grill tier is priced at $500 per month.","madeBy":"AJ","context":"pricing call"}]}`
	})
	entry, err := app.runAmbientAgentOnce(decisionLedgerAgent(), context.Background(), "test-key", responder, 1)
	if err != nil {
		t.Fatalf("decision pass: %v", err)
	}
	if len(*seen) != 2 || entry.Kind != meetingMemoryKindDecisionPass || entry.Metadata["throughBrainId"] != "brain-1" {
		t.Fatalf("calls=%d pass=%+v, want the halved window (brain-1) consumed", len(*seen), entry)
	}
	if decisions := app.memory.entriesOfKind(meetingMemoryKindDecision, 10); len(decisions) != 1 {
		t.Fatalf("decisions=%d, want the recovered extraction persisted", len(decisions))
	}

	always, _ := truncatingResponder(99, func(openAITextRequest) string { return "" })
	if skipped, err := app.runAmbientAgentOnce(decisionLedgerAgent(), context.Background(), "test-key", always, 1); err != nil || skipped.ID != "" {
		t.Fatalf("skip pass entry=%+v err=%v", skipped, err)
	}
	if baseline := app.ambientAgentBaselineID(decisionLedgerAgentName); baseline != "brain-2" {
		t.Fatalf("baseline=%q, want brain-2 skipped", baseline)
	}
	if app.memory.countStuckInputs(decisionLedgerAgentName) != 1 {
		t.Fatal("stuck input not counted for the decision ledger")
	}
}

// Entity ledger: the adjudication call rides the same envelope. A truncated
// pass re-runs its body over half the digests (nothing was landed), the
// raised budget reaches the nested adjudicator through the context, and a
// still-stuck head digest is skipped.
func TestEntityLedgerTruncationRerunsWindowThenSkips(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	upsertLedgerTestDigest(t, app, "meeting-a", meetingDigestPayload{Decisions: []meetingDigestDecision{{
		D: "Ship the pilot with vendor Zebra packaging", By: "AJ", Anchor: "tx-1", Importance: 4,
	}}})
	runLedgerPass(t, app, forbiddenLedgerResponder(t))

	contradiction := meetingDigestPayload{Decisions: []meetingDigestDecision{{
		D: "Use vendor Kappa for the packaging pilot instead of Zebra", By: "AJ", Anchor: "tx-7", Importance: 5,
	}}}
	digestB := upsertLedgerTestDigest(t, app, "meeting-b", contradiction)
	digestC := upsertLedgerTestDigest(t, app, "meeting-c", meetingDigestPayload{Topics: []meetingDigestTopic{{T: "Kappa onboarding timeline", Anchor: "tx-9", Importance: 2}}})

	responder, seen := truncatingResponder(1, func(openAITextRequest) string {
		return `{"verdicts":[{"i":0,"verdict":"supersedes"}]}`
	})
	pass := runLedgerPass(t, app, responder)
	if len(*seen) != 2 {
		t.Fatalf("adjudication calls=%d, want one retry", len(*seen))
	}
	if pass.Metadata[entityLedgerCursorMetadataKey] != digestB.ID {
		t.Fatalf("pass cursor=%q, want the halved window's last digest %q (meeting-c re-feeds)", pass.Metadata[entityLedgerCursorMetadataKey], digestB.ID)
	}
	if !strings.Contains((*seen)[1].Input, "Kappa") {
		t.Fatalf("retry lost the contradiction pair: %q", (*seen)[1].Input)
	}

	// A fresh base record lands deterministically (no call); its
	// contradiction plus an unrelated digest form the next window.
	upsertLedgerTestDigest(t, app, "meeting-d", meetingDigestPayload{Decisions: []meetingDigestDecision{{
		D: "Ship the launch with vendor Delta signage", By: "AJ", Anchor: "tx-11", Importance: 4,
	}}})
	// any near-match against the older records adjudicates as "different"
	lenient := func(context.Context, string, openAITextRequest) (string, error) { return `{"verdicts":[]}`, nil }
	runLedgerPass(t, app, lenient)
	digestE := upsertLedgerTestDigest(t, app, "meeting-e", meetingDigestPayload{Decisions: []meetingDigestDecision{{
		D: "Use vendor Echo for the signage launch instead of Delta", By: "AJ", Anchor: "tx-13", Importance: 5,
	}}})
	digestF := upsertLedgerTestDigest(t, app, "meeting-f", meetingDigestPayload{Topics: []meetingDigestTopic{{T: "Echo onboarding timeline", Anchor: "tx-15", Importance: 2}}})
	_ = digestC

	// [e, f] truncates, the body re-runs over [e] (still the contradiction,
	// still truncating), and the stuck head e is skipped; f re-feeds alone.
	always, seenAlways := truncatingResponder(99, func(openAITextRequest) string { return "" })
	skipped, err := app.runAmbientAgentOnce(entityLedgerAgent(), context.Background(), "test-key", always, 1)
	if err != nil {
		t.Fatalf("skip pass: %v", err)
	}
	if len(*seenAlways) != 2 || skipped.ID != "" {
		t.Fatalf("skip pass calls=%d entry=%+v, want two truncated adjudications and no artifact", len(*seenAlways), skipped)
	}
	if !strings.Contains((*seenAlways)[1].Input, "Echo") {
		t.Fatalf("halved retry lost the contradiction: %q", (*seenAlways)[1].Input)
	}
	if baseline := app.ambientAgentBaselineID(entityLedgerAgentName); baseline != digestE.ID {
		t.Fatalf("baseline=%q, want the stuck head digest %q skipped", baseline, digestE.ID)
	}
	if app.memory.countStuckInputs(entityLedgerAgentName) != 1 {
		t.Fatal("stuck input not counted for the entity ledger")
	}
	landedF := runLedgerPass(t, app, lenient)
	if landedF.Metadata[entityLedgerCursorMetadataKey] != digestF.ID {
		t.Fatalf("remainder cursor=%q, want meeting-f (%q)", landedF.Metadata[entityLedgerCursorMetadataKey], digestF.ID)
	}
}

// Taste analyst: its two-list window halves the same way; a row that still
// truncates is stamped stuck and left behind by the next per-user window.
func TestTasteAnalystTruncationHalvesThenSkipsRow(t *testing.T) {
	app := tasteTestApp(t)
	t.Setenv("TASTE_ANALYST_MIN_SIGNALS", "1")
	first := recordTasteTestSignal(t, app, "AJ", signalEventArtifactEdited, map[string]string{"removedSections": "Intro"})
	second := recordTasteTestSignal(t, app, "AJ", signalEventSurveyOff, map[string]string{"note": "too breathless"})
	third := recordTasteTestSignal(t, app, "AJ", signalEventSurveyOff, map[string]string{"note": "too long"})

	responder, seen := truncatingResponder(1, func(request openAITextRequest) string {
		return tasteTestResponse(t, []string{first.ID, second.ID}, nil)
	})
	if err := app.runTasteAnalystOnce(context.Background(), "test-key", responder); err != nil {
		t.Fatalf("taste pass: %v", err)
	}
	if len(*seen) != 2 || strings.Contains((*seen)[1].Input, third.ID) || !strings.Contains((*seen)[1].Input, second.ID) {
		t.Fatalf("calls=%d retry=%q, want the oldest two signals only", len(*seen), (*seen)[1].Input)
	}
	profile, ok := app.tasteProfileForUser("AJ")
	if !ok || profile.Metadata[tasteAnalystCursorKey] != second.ID {
		t.Fatalf("profile=%+v ok=%v, want the cursor at the halved window's last signal", profile.Metadata, ok)
	}

	// third alone: raised budget once, then stuck.
	always, seenAlways := truncatingResponder(99, func(openAITextRequest) string { return "" })
	if err := app.runTasteAnalystOnce(context.Background(), "test-key", always); err != nil {
		t.Fatalf("skip pass: %v", err)
	}
	if len(*seenAlways) != 2 || (*seenAlways)[1].MaxOutputTokens != tasteAnalystMaxOutputTokens*3/2 {
		t.Fatalf("single-row retry calls=%d budget=%d", len(*seenAlways), (*seenAlways)[1].MaxOutputTokens)
	}
	stuck, ok := app.memory.entryByKindAndID(meetingMemoryKindSignal, third.ID)
	if !ok || stuck.Metadata[tasteStuckInputKey] != ambientTruncationReason {
		t.Fatalf("stuck signal=%+v ok=%v, want %s stamped", stuck.Metadata, ok, tasteStuckInputKey)
	}
	if window := app.memory.unconsumedSignalsForActor("AJ", second.ID, 10); len(window) != 0 {
		t.Fatalf("stuck signal still in the window: %+v", window)
	}
	if app.memory.countStuckInputs(tasteAnalystAgentName) != 1 {
		t.Fatal("stuck input not counted for the taste analyst")
	}
	quiet, seenQuiet := truncatingResponder(0, func(openAITextRequest) string { return "" })
	if err := app.runTasteAnalystOnce(context.Background(), "test-key", quiet); err != nil || len(*seenQuiet) != 0 {
		t.Fatalf("next pass calls=%d err=%v, want nothing left to distill", len(*seenQuiet), err)
	}
}

// Day-digest reflection: a look-back keeps the NEWEST half on retry (the
// day cursor already moved), and a reflection that truncates twice is
// abandoned for the day with a tombstone instead of retrying every tick.
func TestDayDigestReflectionTruncationKeepsNewestHalfThenAbandonsDay(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)
	location := meetingTimeLocation()
	fixedNow := time.Date(2026, 7, 7, 17, 0, 0, 0, location)
	seedDigest := func(meetingID, title, start, end string) meetingMemoryEntry {
		entry, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, meetingID, `{"meetingId":"`+meetingID+`","title":"`+title+`","topics":[{"t":"`+title+` keeps slipping","at":"`+start+`","importance":4}]}`, map[string]string{
			"meetingId": meetingID, digestDayMetadataKey: "2026-07-06", digestSpanStartMetadataKey: start, digestSpanEndMetadataKey: end,
		})
		if err != nil {
			t.Fatalf("seed %s: %v", meetingID, err)
		}
		return entry
	}
	older := seedDigest("m-older", "Gmail OAuth review", "2026-07-06T09:00:00-07:00", "2026-07-06T10:00:00-07:00")
	newer := seedDigest("m-newer", "Packaging pilot", "2026-07-06T14:00:00-07:00", "2026-07-06T15:00:00-07:00")

	responder, seen := truncatingResponder(1, func(openAITextRequest) string {
		return "## Recurring blockers\n- Packaging pilot keeps slipping."
	})
	if _, err := app.runDayDigestPass(context.Background(), "test-key", []meetingMemoryEntry{older, newer}, responder, fixedNow.UTC()); err != nil {
		t.Fatalf("day pass: %v", err)
	}
	if len(*seen) != 2 {
		t.Fatalf("reflection calls=%d, want one retry", len(*seen))
	}
	// digestsInRange lists the folded day digest first, then the meeting
	// digests: the newest half of that window drops the day digest and keeps
	// the meeting digests, never the other way round.
	if first, retry := (*seen)[0], (*seen)[1]; !strings.Contains(first.Input, "kind=day_digest") || strings.Contains(retry.Input, "kind=day_digest") || !strings.Contains(retry.Input, "key=m-newer") {
		t.Fatalf("look-back retry must keep the newest half: first=%q retry=%q", first.Input, retry.Input)
	}
	// the retry must also change the ENVELOPE: the production symptom was
	// output exhaustion at reflectionMaxOutputTokens, and a second truncation
	// abandons the day for good.
	if first, retry := (*seen)[0], (*seen)[1]; first.MaxOutputTokens != reflectionMaxOutputTokens || retry.MaxOutputTokens != ambientTruncationRaisedOutputBudget(reflectionMaxOutputTokens) {
		t.Fatalf("reflection budgets first=%d retry=%d, want %d then the raised %d", first.MaxOutputTokens, retry.MaxOutputTokens, reflectionMaxOutputTokens, ambientTruncationRaisedOutputBudget(reflectionMaxOutputTokens))
	}
	reflections := app.memory.entriesOfKind(meetingMemoryKindReflection, 0)
	if len(reflections) != 1 || !strings.Contains(reflections[0].Metadata["supportingDigests"], newer.ID) {
		t.Fatalf("reflections=%+v, want one anchored on the retried window", reflections)
	}
	_ = older

	// A second app day (2026-07-08 looks back at 2026-07-07): truncate twice.
	nextDay := fixedNow.AddDate(0, 0, 1)
	seedDigest("m-today", "Board prep", "2026-07-07T09:00:00-07:00", "2026-07-07T10:00:00-07:00")
	always, seenAlways := truncatingResponder(99, func(openAITextRequest) string { return "" })
	if _, err := app.runDayDigestPass(context.Background(), "test-key", app.memory.entriesOfKind(meetingMemoryKindMeetingDigest, 1), always, nextDay.UTC()); err != nil {
		t.Fatalf("second day pass: %v", err)
	}
	if len(*seenAlways) != 2 {
		t.Fatalf("second-day reflection calls=%d, want exactly two attempts", len(*seenAlways))
	}
	if reflections := app.memory.entriesOfKind(meetingMemoryKindReflection, 0); len(reflections) != 1 {
		t.Fatalf("a twice-truncated reflection was persisted: %+v", reflections)
	}
	if app.memory.countEntriesOfKindByMetadata(meetingMemoryKindDeadLetter, reflectionStuckDayMetadataKey, "2026-07-07") != 1 || app.memory.countStuckInputs(dayDigestAgentName) != 1 {
		t.Fatal("abandoned reflection left no stuck-input tombstone for 2026-07-07")
	}
	// the next tick does not spend the same calls again
	if _, err := app.runDayDigestPass(context.Background(), "test-key", app.memory.entriesOfKind(meetingMemoryKindMeetingDigest, 1), always, nextDay.Add(time.Hour).UTC()); err != nil {
		t.Fatalf("third day pass: %v", err)
	}
	if len(*seenAlways) != 2 {
		t.Fatalf("abandoned day retried: calls=%d", len(*seenAlways))
	}
}
