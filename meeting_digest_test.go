package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

/* ---------- helpers ---------- */

func appendDigestTestBrain(t *testing.T, app *kanbanBoardApp, id string, meetingID string, text string, metadata map[string]string) {
	t.Helper()
	stamped := map[string]string{}
	for key, value := range metadata {
		stamped[key] = value
	}
	if meetingID != "" {
		stamped["meetingId"] = meetingID
	}
	if _, appended, err := app.memory.appendBrainWriteUp(id, text, stamped); err != nil {
		t.Fatalf("append brain %s: %v", id, err)
	} else if !appended {
		t.Fatalf("brain %s appended=false, want true", id)
	}
}

func cannedMeetingDigestJSON() string {
	return `{"meetingId":"model-wrong-id","title":"Packaging pilot","day":"1999-01-01","attendees":["AJ","Tyler"],` +
		`"topics":[{"t":"Vendor Zebra packaging pilot","anchor":"tx-1","at":"2026-07-06T10:05:00Z","importance":4}],` +
		`"decisions":[{"d":"Choose vendor Zebra for the packaging pilot","by":"attributed to AJ","status":"decided","anchor":"tx-1","at":"2026-07-06T10:06:00Z","importance":5}],` +
		`"actionItems":[{"a":"Draft the pricing sheet","owner":"Tyler","status":"open","anchor":"tx-2","at":"2026-07-06T10:07:00Z","importance":9}],` +
		`"openQuestions":[{"q":"Which SKU ships first?","anchor":"","at":"","importance":0}],` +
		`"themes":["packaging"]}`
}

/* ---------- meeting digest worker ---------- */

func TestMeetingDigestWorkerProducesAnchoredImportanceJSON(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)

	appendTestTranscript(t, app, "tx-1", "We choose vendor Zebra for the packaging pilot.")
	appendTestTranscript(t, app, "tx-2", "Tyler will draft the pricing sheet by Friday.")
	meetingID := app.memory.currentMeetingID(officeRoomID)
	if meetingID == "" {
		t.Fatal("expected a minted meeting id after the first transcript")
	}
	appendDigestTestBrain(t, app, "brain-1", meetingID,
		"## Overview\nVendor Zebra chosen for the packaging pilot.\n## Transcript reference\ntx-1, tx-2",
		map[string]string{
			"fromTranscriptCreatedAt":    "2026-07-06T16:55:00Z",
			"throughTranscriptCreatedAt": "2026-07-06T17:10:00Z",
		})

	calls := 0
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		if !strings.Contains(request.Instructions, "importance scores each fact 1-5") {
			t.Errorf("instructions missing the A4 importance contract: %s", request.Instructions)
		}
		if !strings.Contains(request.Instructions, "hedge who-said-what") {
			t.Errorf("instructions missing hedged attribution: %s", request.Instructions)
		}
		if !strings.Contains(request.Input, "brain-1") || !strings.Contains(request.Input, "Vendor Zebra chosen") {
			t.Errorf("digest input missing the brain window: %s", request.Input)
		}
		return cannedMeetingDigestJSON(), nil
	}

	agent := meetingDigestAgent()
	entry, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1)
	if err != nil {
		t.Fatalf("runAmbientAgentOnce: %v", err)
	}
	if calls != 1 {
		t.Fatalf("model calls = %d, want 1", calls)
	}
	if entry.Kind != meetingMemoryKindMeetingDigest || digestEntryKey(entry) != meetingID {
		t.Fatalf("digest kind/key = %s/%s, want %s/%s", entry.Kind, digestEntryKey(entry), meetingMemoryKindMeetingDigest, meetingID)
	}
	if got := entry.Metadata["meetingId"]; got != meetingID {
		t.Fatalf("digest meetingId stamp = %q, want %q", got, meetingID)
	}
	if got := entry.Metadata[meetingDigestCursorMetadataKey]; got != "brain-1" {
		t.Fatalf("cursor = %q, want brain-1", got)
	}
	wantDay := dayBucket(time.Date(2026, 7, 6, 17, 10, 0, 0, time.UTC))
	if got := entry.Metadata[digestDayMetadataKey]; got != wantDay {
		t.Fatalf("day stamp = %q, want %q", got, wantDay)
	}
	if entry.Metadata[digestSpanStartMetadataKey] == "" || entry.Metadata[digestSpanEndMetadataKey] == "" {
		t.Fatalf("span stamps missing: %+v", entry.Metadata)
	}

	var payload meetingDigestPayload
	if err := json.Unmarshal([]byte(entry.Text), &payload); err != nil {
		t.Fatalf("stored digest is not JSON: %v\n%s", err, entry.Text)
	}
	if payload.MeetingID != meetingID {
		t.Fatalf("payload meetingId = %q, want the server-derived %q (model claim overridden)", payload.MeetingID, meetingID)
	}
	if payload.Day != wantDay {
		t.Fatalf("payload day = %q, want %q (model claim overridden)", payload.Day, wantDay)
	}
	if len(payload.Decisions) != 1 || payload.Decisions[0].Importance != 5 {
		t.Fatalf("decisions = %+v, want one importance-5 decision", payload.Decisions)
	}
	if len(payload.ActionItems) != 1 || payload.ActionItems[0].Importance != 5 {
		t.Fatalf("action importance = %+v, want clamped to 5", payload.ActionItems)
	}
	if len(payload.OpenQuestions) != 1 || payload.OpenQuestions[0].Importance != meetingDigestDefaultImportance {
		t.Fatalf("question importance = %+v, want default %d for an absent score", payload.OpenQuestions, meetingDigestDefaultImportance)
	}
	if !strings.HasPrefix(payload.Decisions[0].By, "attributed to") {
		t.Fatalf("attribution = %q, want hedged", payload.Decisions[0].By)
	}

	// the anchor drills to the verbatim exchange in the same meeting.
	window := app.memory.transcriptWindowAround(payload.Decisions[0].Anchor, 1)
	if len(window) == 0 {
		t.Fatalf("anchor %q did not resolve to a transcript window", payload.Decisions[0].Anchor)
	}
	foundAnchor := false
	for _, transcript := range window {
		if transcript.ID == "tx-1" {
			foundAnchor = true
		}
		if got := strings.TrimSpace(transcript.Metadata["meetingId"]); got != meetingID {
			t.Fatalf("window crossed meetings: %q", got)
		}
	}
	if !foundAnchor {
		t.Fatalf("window %v missing the anchor transcript", window)
	}

	if latest, ok := app.memory.latestDigestPerMeeting()[meetingID]; !ok || latest.ID != entry.ID {
		t.Fatalf("latestDigestPerMeeting missing the new digest")
	}
}

func TestMeetingDigestWorkerNeverConsumesArtifacts(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)

	appendTestTranscript(t, app, "tx-1", "Boot Barn kickoff planning notes.")
	meetingID := app.memory.currentMeetingID(officeRoomID)
	oversizeBody := "data:image/png;base64," + strings.Repeat("QUJDRA==", 40000) // ~320KB of the blob class
	if _, appended, err := app.memory.appendOSArtifact("art-1", oversizeBody, map[string]string{"title": "deck"}); err != nil || !appended {
		t.Fatalf("append artifact: appended=%v err=%v", appended, err)
	}
	appendDigestTestBrain(t, app, "brain-1", meetingID, "## Overview\nBoot Barn kickoff summarized.", nil)

	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if strings.Contains(request.Input, ";base64,") {
			t.Errorf("digest input carries a base64 body")
		}
		if len(request.Input) > 50_000 {
			t.Errorf("digest input = %d bytes, want a bounded brain-only window", len(request.Input))
		}
		return cannedMeetingDigestJSON(), nil
	}
	if _, err := app.runAmbientAgentOnce(meetingDigestAgent(), context.Background(), "test-key", responder, 1); err != nil {
		t.Fatalf("runAmbientAgentOnce: %v", err)
	}
	if len(app.memory.entriesOfKind(meetingMemoryKindMeetingDigest, 0)) != 1 {
		t.Fatalf("expected exactly one digest")
	}
}

func TestMeetingDigestWorkerRebuildOnlyWhenNewerBrainAndCarriesContinuity(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)

	appendTestTranscript(t, app, "tx-1", "Boot Barn kickoff planning notes.")
	meetingID := app.memory.currentMeetingID(officeRoomID)
	appendDigestTestBrain(t, app, "brain-1", meetingID, "## Overview\nFirst window: kickoff planning.", nil)

	calls := 0
	var lastInput string
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		lastInput = request.Input
		return `{"meetingId":"x","title":"V1Marker","topics":[{"t":"kickoff","importance":3}]}`, nil
	}

	agent := meetingDigestAgent()
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}

	// no new brains: the cursor gate keeps the model silent.
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d after a no-new-brain pass, want still 1 (cursor gate)", calls)
	}

	// a newer brain triggers a rebuild that carries the prior digest.
	appendDigestTestBrain(t, app, "brain-2", meetingID, "## Overview\nSecond window: budget agreed.", nil)
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1); err != nil {
		t.Fatalf("third pass: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if !strings.Contains(lastInput, "# Previous digest") || !strings.Contains(lastInput, "V1Marker") {
		t.Fatalf("rebuild input missing prior-digest continuity: %s", lastInput)
	}
	if !strings.Contains(lastInput, "brain-2") || strings.Contains(lastInput, "First window: kickoff planning") {
		t.Fatalf("rebuild input should carry only the NEW brains: %s", lastInput)
	}

	generations := app.memory.entriesOfKind(meetingMemoryKindMeetingDigest, 0)
	if len(generations) != 2 {
		t.Fatalf("digest generations = %d, want 2 (supersede-in-place)", len(generations))
	}
	currentCount := 0
	for _, generation := range generations {
		if digestEntryCurrent(generation) {
			currentCount++
		}
	}
	if currentCount != 1 {
		t.Fatalf("current digests = %d, want exactly 1", currentCount)
	}
}

func TestMeetingDigestWorkerCapsMeetingsPerTick(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	t.Setenv("MEETING_DIGEST_MAX_MEETINGS_PER_TICK", "2")
	app := newIsolatedKanbanBoardApp(t)

	for _, seed := range []struct{ brainID, meetingID string }{
		{"brain-1", "m1"}, {"brain-2", "m2"}, {"brain-3", "m3"}, {"brain-4", "m4"},
	} {
		appendDigestTestBrain(t, app, seed.brainID, seed.meetingID, "## Overview\nNotes for "+seed.meetingID+".", nil)
	}

	calls := 0
	responder := func(context.Context, string, openAITextRequest) (string, error) {
		calls++
		return `{"meetingId":"x","topics":[{"t":"notes","importance":2}]}`, nil
	}

	agent := meetingDigestAgent()
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (per-tick meeting cap)", calls)
	}
	latest := app.memory.latestDigestPerMeeting()
	if _, ok := latest["m1"]; !ok {
		t.Fatalf("m1 digest missing after first pass")
	}
	if _, ok := latest["m2"]; !ok {
		t.Fatalf("m2 digest missing after first pass")
	}
	if _, ok := latest["m3"]; ok {
		t.Fatalf("m3 digested despite the cap")
	}
	if got := latest["m2"].Metadata[meetingDigestCursorMetadataKey]; got != "brain-2" {
		t.Fatalf("pass cursor = %q, want brain-2 (never past a capped meeting's brains)", got)
	}

	// capped meetings are deferred, not dropped: the next tick digests them.
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if calls != 4 {
		t.Fatalf("calls = %d, want 4", calls)
	}
	latest = app.memory.latestDigestPerMeeting()
	for _, key := range []string{"m1", "m2", "m3", "m4"} {
		if _, ok := latest[key]; !ok {
			t.Fatalf("%s digest missing after second pass", key)
		}
	}
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1); err != nil {
		t.Fatalf("third pass: %v", err)
	}
	if calls != 4 {
		t.Fatalf("calls = %d after everything consumed, want still 4", calls)
	}
}

func TestMeetingDigestWorkerBadJSONKeepsPriorDigest(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)

	appendDigestTestBrain(t, app, "brain-1", "m1", "## Overview\nKickoff planning.", nil)
	goodThenBad := []string{`{"meetingId":"x","topics":[{"t":"kickoff","importance":3}]}`, "definitely not json", `{"meetingId":"x","topics":[{"t":"kickoff plus budget","importance":4}]}`}
	calls := 0
	var lastInput string
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		lastInput = request.Input
		index := calls
		if index >= len(goodThenBad) {
			index = len(goodThenBad) - 1
		}
		calls++
		return goodThenBad[index], nil
	}

	agent := meetingDigestAgent()
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	first := app.memory.latestDigestPerMeeting()["m1"]
	if first.ID == "" {
		t.Fatalf("first digest missing")
	}

	appendDigestTestBrain(t, app, "brain-2", "m1", "## Overview\nBudget agreed.", nil)
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1); err != nil {
		t.Fatalf("bad-JSON pass must not error: %v", err)
	}
	if len(app.memory.entriesOfKind(meetingMemoryKindMeetingDigest, 0)) != 1 {
		t.Fatalf("bad JSON must not write a digest generation")
	}
	if got := app.memory.latestDigestPerMeeting()["m1"]; got.ID != first.ID {
		t.Fatalf("prior digest clobbered by bad JSON")
	}

	// the cursor stayed put: the same window re-feeds and succeeds.
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1); err != nil {
		t.Fatalf("retry pass: %v", err)
	}
	if !strings.Contains(lastInput, "brain-2") {
		t.Fatalf("retry did not re-feed the unconsumed brain: %s", lastInput)
	}
	second := app.memory.latestDigestPerMeeting()["m1"]
	if second.ID == first.ID {
		t.Fatalf("retry did not produce the replacement digest")
	}
}

/* ---------- day digest fold ---------- */

func TestDayDigestBucketsMarathonMeetingByCalendarDay(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	t.Setenv(reflectionDisabledEnv, "1")
	app := newIsolatedKanbanBoardApp(t)

	marathon := `{"meetingId":"m-marathon","title":"Marathon","day":"2026-07-01",` +
		`"topics":[{"t":"Monday scope review","at":"2026-06-29T10:00:00-07:00","importance":3},` +
		`{"t":"Tuesday vendor call","at":"2026-06-30T11:00:00-07:00","importance":4}],` +
		`"decisions":[{"d":"Ship the pilot Wednesday","by":"attributed to AJ","at":"2026-07-01T09:00:00-07:00","importance":5}],` +
		`"themes":["pilot"]}`
	digest, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, "m-marathon", marathon, map[string]string{
		"meetingId":                "m-marathon",
		digestDayMetadataKey:       "2026-07-01",
		digestSpanStartMetadataKey: "2026-06-29T09:00:00-07:00",
		digestSpanEndMetadataKey:   "2026-07-01T10:00:00-07:00",
	})
	if err != nil {
		t.Fatalf("seed marathon digest: %v", err)
	}

	responder := func(context.Context, string, openAITextRequest) (string, error) {
		t.Error("the day fold is deterministic: no model call expected")
		return "", nil
	}
	agent := dayDigestAgent()
	passEntry, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1)
	if err != nil {
		t.Fatalf("day pass: %v", err)
	}
	if passEntry.Kind != meetingMemoryKindDayDigestPass {
		t.Fatalf("pass artifact kind = %s, want %s", passEntry.Kind, meetingMemoryKindDayDigestPass)
	}
	if got := passEntry.Metadata[dayDigestCursorMetadataKey]; got != digest.ID {
		t.Fatalf("pass cursor = %q, want %q", got, digest.ID)
	}

	dayDigests := app.memory.entriesOfKind(meetingMemoryKindDayDigest, 0)
	if len(dayDigests) != 3 {
		keys := make([]string, 0, len(dayDigests))
		for _, entry := range dayDigests {
			keys = append(keys, digestEntryKey(entry))
		}
		t.Fatalf("day digests = %v, want the marathon split into 3 calendar days", keys)
	}
	byDay := map[string]dayDigestPayload{}
	for _, entry := range dayDigests {
		if !digestEntryCurrent(entry) {
			t.Fatalf("day digest %s not current", entry.ID)
		}
		var payload dayDigestPayload
		if err := json.Unmarshal([]byte(entry.Text), &payload); err != nil {
			t.Fatalf("day digest %s not JSON: %v", entry.ID, err)
		}
		byDay[digestEntryKey(entry)] = payload
	}
	monday, ok := byDay["2026-06-29"]
	if !ok || len(monday.Topics) != 1 || monday.Topics[0].MeetingID != "m-marathon" {
		t.Fatalf("2026-06-29 slice = %+v, want the Monday topic with meeting provenance", monday)
	}
	if _, ok := byDay["2026-06-30"]; !ok {
		t.Fatalf("2026-06-30 slice missing")
	}
	wednesday, ok := byDay["2026-07-01"]
	if !ok || len(wednesday.Decisions) != 1 || wednesday.Decisions[0].Importance != 5 {
		t.Fatalf("2026-07-01 slice = %+v, want the importance-5 decision", wednesday)
	}

	// consumed window: a second tick folds nothing new and adds no pass.
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1); err != nil {
		t.Fatalf("second day pass: %v", err)
	}
	if got := len(app.memory.entriesOfKind(meetingMemoryKindDayDigestPass, 0)); got != 1 {
		t.Fatalf("pass artifacts = %d, want 1 (cursor consumed the window)", got)
	}
	if got := len(app.memory.entriesOfKind(meetingMemoryKindDayDigest, 0)); got != 3 {
		t.Fatalf("day digest generations = %d, want still 3", got)
	}
}

func TestDayDigestPassAdvancesCursorPastUnfoldableInputs(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	t.Setenv(reflectionDisabledEnv, "1")
	app := newIsolatedKanbanBoardApp(t)

	// a meeting digest whose body is not parseable JSON: nothing can fold,
	// but the cursor must still advance (decision_pass pattern) or the window
	// re-feeds forever and eventually starves newer digests past maxBatch.
	if _, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, "m-broken", "not json at all", map[string]string{
		"meetingId":          "m-broken",
		digestDayMetadataKey: "2026-07-01",
	}); err != nil {
		t.Fatalf("seed broken digest: %v", err)
	}

	responder := func(context.Context, string, openAITextRequest) (string, error) {
		t.Error("no model call expected")
		return "", nil
	}
	agent := dayDigestAgent()
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1); err != nil {
		t.Fatalf("day pass: %v", err)
	}
	if got := len(app.memory.entriesOfKind(meetingMemoryKindDayDigest, 0)); got != 0 {
		t.Fatalf("day digests = %d, want 0 (nothing foldable)", got)
	}
	if got := len(app.memory.entriesOfKind(meetingMemoryKindDayDigestPass, 0)); got != 1 {
		t.Fatalf("pass artifacts = %d, want 1", got)
	}
	// window consumed: the next tick does not re-feed it.
	if _, err := app.runAmbientAgentOnce(agent, context.Background(), "test-key", responder, 1); err != nil {
		t.Fatalf("second day pass: %v", err)
	}
	if got := len(app.memory.entriesOfKind(meetingMemoryKindDayDigestPass, 0)); got != 1 {
		t.Fatalf("pass artifacts = %d after consumed window, want still 1", got)
	}
}

func TestFoldDayDigestRanksByImportanceAcrossMeetings(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")

	current := map[string]meetingMemoryEntry{
		"m1": {
			ID:   "digest-m1",
			Kind: meetingMemoryKindMeetingDigest,
			Text: `{"meetingId":"m1","title":"Standup","decisions":[{"d":"Rename the feed","at":"2026-07-06T10:00:00-07:00","importance":2}]}`,
			Metadata: map[string]string{
				digestKeyMetadataKey:     "m1",
				digestCurrentMetadataKey: digestCurrentTrue,
				digestDayMetadataKey:     "2026-07-06",
			},
		},
		"m2": {
			ID:   "digest-m2",
			Kind: meetingMemoryKindMeetingDigest,
			Text: `{"meetingId":"m2","title":"Pricing","decisions":[{"d":"Freeze pricing until Q3","at":"2026-07-06T15:00:00-07:00","importance":5},{"d":"Other-day decision","at":"2026-07-05T15:00:00-07:00","importance":4}]}`,
			Metadata: map[string]string{
				digestKeyMetadataKey:     "m2",
				digestCurrentMetadataKey: digestCurrentTrue,
				digestDayMetadataKey:     "2026-07-06",
			},
		},
	}

	payload, ok := foldDayDigest("2026-07-06", current)
	if !ok {
		t.Fatalf("fold returned no payload")
	}
	if len(payload.Meetings) != 2 {
		t.Fatalf("meetings = %+v, want both contributors", payload.Meetings)
	}
	if len(payload.Decisions) != 2 {
		t.Fatalf("decisions = %+v, want the two same-day decisions only", payload.Decisions)
	}
	if payload.Decisions[0].Importance != 5 || payload.Decisions[0].MeetingID != "m2" {
		t.Fatalf("decisions not importance-ranked with provenance: %+v", payload.Decisions)
	}
	if payload.Decisions[1].MeetingID != "m1" {
		t.Fatalf("second decision provenance = %q, want m1", payload.Decisions[1].MeetingID)
	}
	for _, decision := range payload.Decisions {
		if decision.D == "Other-day decision" {
			t.Fatalf("a 2026-07-05 fact leaked into the 2026-07-06 fold")
		}
	}
}

/* ---------- reflection (amendment A3) ---------- */

func TestDayDigestTickEmitsReflectionForCompletedDayOnce(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)
	location := meetingTimeLocation()
	fixedNow := time.Date(2026, 7, 7, 17, 0, 0, 0, location)

	yesterdayDigest, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, "m-y", `{"meetingId":"m-y","title":"Gmail review","topics":[{"t":"Gmail OAuth review keeps slipping","at":"2026-07-06T10:00:00-07:00","importance":4}]}`, map[string]string{
		"meetingId":                "m-y",
		digestDayMetadataKey:       "2026-07-06",
		digestSpanStartMetadataKey: "2026-07-06T09:00:00-07:00",
		digestSpanEndMetadataKey:   "2026-07-06T11:00:00-07:00",
	})
	if err != nil {
		t.Fatalf("seed digest: %v", err)
	}

	calls := 0
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		calls++
		if !strings.Contains(request.Instructions, "Circling without closure") {
			t.Errorf("reflection instructions missing the A3 questions: %s", request.Instructions)
		}
		if !strings.Contains(request.Input, "2026-07-06") || !strings.Contains(request.Input, "Gmail OAuth") {
			t.Errorf("reflection input missing the digest window: %s", request.Input)
		}
		return "## Recurring blockers\n- Gmail OAuth review keeps slipping (attributed to Tyler).", nil
	}

	if _, err := app.runDayDigestPass(context.Background(), "test-key", []meetingMemoryEntry{yesterdayDigest}, responder, fixedNow.UTC()); err != nil {
		t.Fatalf("day pass: %v", err)
	}
	if calls != 1 {
		t.Fatalf("model calls = %d, want exactly 1 (the reflection; folds are deterministic)", calls)
	}

	reflections := app.memory.entriesOfKind(meetingMemoryKindReflection, 0)
	if len(reflections) != 1 {
		t.Fatalf("reflections = %d, want 1", len(reflections))
	}
	reflection := reflections[0]
	if got := reflection.Metadata[digestDayMetadataKey]; got != "2026-07-06" {
		t.Fatalf("reflection day = %q, want the completed local day 2026-07-06", got)
	}
	if got := strings.TrimSpace(reflection.Metadata["meetingId"]); got != "" {
		t.Fatalf("reflection carries meetingId %q, want none (past-day entry)", got)
	}
	if strings.TrimSpace(reflection.Metadata["supportingDigests"]) == "" {
		t.Fatalf("reflection has no supporting-digest anchors: %+v", reflection.Metadata)
	}
	if got := app.memory.currentMeetingID(officeRoomID); got != "" {
		t.Fatalf("day pass minted meeting id %q at idle, want none", got)
	}

	// recall-eligible: keyword search grounds on the reflection.
	foundInSearch := false
	for _, match := range app.memory.search("recurring blockers", 10) {
		if match.Entry.ID == reflection.ID {
			foundInSearch = true
		}
	}
	if !foundInSearch {
		t.Fatalf("reflection not reachable via store search")
	}

	// at most one per local day: a later tick the same day reflects nothing,
	// while the fold still rebuilds the day digest.
	v2, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, "m-y", `{"meetingId":"m-y","title":"Gmail review","topics":[{"t":"Gmail OAuth review keeps slipping","at":"2026-07-06T10:00:00-07:00","importance":4},{"t":"Second window","at":"2026-07-06T12:00:00-07:00","importance":2}]}`, map[string]string{
		"meetingId":          "m-y",
		digestDayMetadataKey: "2026-07-06",
	})
	if err != nil {
		t.Fatalf("seed v2 digest: %v", err)
	}
	if _, err := app.runDayDigestPass(context.Background(), "test-key", []meetingMemoryEntry{v2}, responder, fixedNow.Add(time.Hour).UTC()); err != nil {
		t.Fatalf("second day pass: %v", err)
	}
	if calls != 1 {
		t.Fatalf("model calls = %d after the once-per-day guard, want still 1", calls)
	}
	if got := len(app.memory.entriesOfKind(meetingMemoryKindReflection, 0)); got != 1 {
		t.Fatalf("reflections = %d, want still 1", got)
	}
	if got := len(app.memory.entriesOfKind(meetingMemoryKindDayDigest, 0)); got != 2 {
		t.Fatalf("day digest generations = %d, want 2 (fold still rebuilt)", got)
	}
}

func TestReflectionSkippedWhenDayHasNoMaterial(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)
	location := meetingTimeLocation()
	fixedNow := time.Date(2026, 7, 7, 17, 0, 0, 0, location)

	// only TODAY has material — yesterday is empty, so no reflection fires.
	digest, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, "m-t", `{"meetingId":"m-t","topics":[{"t":"Live work","at":"2026-07-07T10:00:00-07:00","importance":3}]}`, map[string]string{
		"meetingId":          "m-t",
		digestDayMetadataKey: "2026-07-07",
	})
	if err != nil {
		t.Fatalf("seed digest: %v", err)
	}
	responder := func(context.Context, string, openAITextRequest) (string, error) {
		t.Error("reflection must not fire for a day without material")
		return "", nil
	}
	if _, err := app.runDayDigestPass(context.Background(), "test-key", []meetingMemoryEntry{digest}, responder, fixedNow.UTC()); err != nil {
		t.Fatalf("day pass: %v", err)
	}
	if got := len(app.memory.entriesOfKind(meetingMemoryKindReflection, 0)); got != 0 {
		t.Fatalf("reflections = %d, want 0", got)
	}
}

/* ---------- kind classification + boot resume ---------- */

func TestDigestBookkeepingKindsClassification(t *testing.T) {
	if !isUIStateMemoryKind(meetingMemoryKindDayDigestPass) {
		t.Fatalf("day_digest_pass must be UI-state bookkeeping")
	}
	if isUIStateMemoryKind(meetingMemoryKindReflection) {
		t.Fatalf("reflection must be recall-eligible knowledge, not UI state")
	}
	if !isPromptBodyCapExemptKind(meetingMemoryKindReflection) {
		t.Fatalf("reflection is a bounded model summary and must ride the prompt cap exemption")
	}

	entries := []meetingMemoryEntry{
		{ID: "t1", Kind: meetingMemoryKindTranscript, Text: "hello"},
		{ID: "d1", Kind: meetingMemoryKindMeetingDigest, Text: "{}"},
		{ID: "d2", Kind: meetingMemoryKindDayDigest, Text: "{}"},
		{ID: "d3", Kind: meetingMemoryKindCompanyDigest, Text: "{}"},
		{ID: "r1", Kind: meetingMemoryKindReflection, Text: "## Recurring blockers"},
		{ID: "p1", Kind: meetingMemoryKindDayDigestPass, Text: "pass"},
	}
	visible := visibleMeetingMemoryEntries(entries, 0)
	if len(visible) != 1 || visible[0].ID != "t1" {
		ids := make([]string, 0, len(visible))
		for _, entry := range visible {
			ids = append(ids, entry.ID)
		}
		t.Fatalf("client timeline shows %v, want only the transcript", ids)
	}
}

func TestAmbientBookkeepingNeverSteersBootResume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	store, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("newMeetingMemoryStore: %v", err)
	}
	if _, appended, err := store.appendAttributedTranscript("tx-1", "tx-1", "Tom", "dominant", "Boot Barn kickoff planning notes for resume."); err != nil || !appended {
		t.Fatalf("append transcript: appended=%v err=%v", appended, err)
	}
	meetingID := store.currentMeetingID(officeRoomID)
	if meetingID == "" {
		t.Fatal("expected a minted meeting id")
	}

	if _, appended, err := store.appendAmbientEntry(meetingMemoryKindReflection, "refl-1", "## Recurring blockers\n- none yet", map[string]string{digestDayMetadataKey: "2026-07-06"}); err != nil || !appended {
		t.Fatalf("append reflection: appended=%v err=%v", appended, err)
	}
	reloaded, err := newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("reload after reflection: %v", err)
	}
	if got := reloaded.currentMeetingID(officeRoomID); got != meetingID {
		t.Fatalf("resume after reflection = %q, want the in-flight meeting %q", got, meetingID)
	}

	if _, appended, err := store.appendAmbientEntry(meetingMemoryKindDayDigestPass, "pass-1", "day digest pass: no day rebuilt", map[string]string{dayDigestCursorMetadataKey: "digest-1"}); err != nil || !appended {
		t.Fatalf("append pass artifact: appended=%v err=%v", appended, err)
	}
	reloaded, err = newMeetingMemoryStore(path)
	if err != nil {
		t.Fatalf("reload after pass artifact: %v", err)
	}
	if got := reloaded.currentMeetingID(officeRoomID); got != meetingID {
		t.Fatalf("resume after pass artifact = %q, want %q", got, meetingID)
	}
}
