package main

// gen-249 hotfix port: this branch carries the truncation chassis only where
// the two ported workers use it — the meeting digest's brain groups
// (meeting_digest_truncation_test.go) and the day-digest reflection below.
// The chassis rollout to mission intelligence, the narrative maintainer, the
// decision/entity ledgers and the taste analyst stays on main, and so do its
// tests.

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
