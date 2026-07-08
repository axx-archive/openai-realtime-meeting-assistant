package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func localDayStart(t *testing.T, now time.Time) time.Time {
	t.Helper()
	location := meetingTimeLocation()
	local := now.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
}

// TestBuildComprehensiveBriefingBlobFree: the map stage's kind whitelist keeps
// artifact/base64 bodies structurally unreachable, and every map input stays
// inside the chunk budget — the direct regression for the 2.6MB deck class.
func TestBuildComprehensiveBriefingBlobFree(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)
	appendTestTranscript(t, app, "tx-1", "We choose vendor Zebra for the packaging pilot.")
	appendTestTranscript(t, app, "tx-2", "Tyler will draft the pricing sheet by Friday.")
	blob := "data:image/png;base64," + strings.Repeat("QUFBQUFBUUFB", 6000)
	if _, _, err := app.memory.appendOSArtifact("artifact-deck", blob, map[string]string{"title": "Big deck"}); err != nil {
		t.Fatalf("append artifact: %v", err)
	}

	var mu sync.Mutex
	var inputs []string
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		mu.Lock()
		inputs = append(inputs, request.Input)
		mu.Unlock()
		return cannedBriefingDigestJSON(), nil
	}

	dayStart := localDayStart(t, time.Now())
	briefing, err := app.buildComprehensiveBriefing(context.Background(), "test-key", dayStart.UTC(), dayStart.AddDate(0, 0, 1).UTC(), responder)
	if err != nil {
		t.Fatalf("buildComprehensiveBriefing: %v", err)
	}
	if len(inputs) == 0 {
		t.Fatal("map stage never ran")
	}
	for _, input := range inputs {
		if strings.Contains(input, "data:image") || strings.Contains(input, "QUFBQUFBUUFB") {
			t.Fatal("artifact/base64 body leaked into a map input")
		}
		if len(input) > mapReduceChunkBudgetChars+4096 {
			t.Fatalf("map input %d chars exceeds the bounded chunk budget", len(input))
		}
	}
	if briefing.empty() {
		t.Fatal("briefing must compose from the mapped digests")
	}
	text := renderCrossMeetingBriefing(briefing)
	for _, want := range []string{"Ship the rollout Friday", "Composed on demand"} {
		if !strings.Contains(text, want) {
			t.Fatalf("briefing missing %q:\n%s", want, text)
		}
	}
}

// TestBuildComprehensiveBriefingInjectsDecisionsVerbatim: active decision-log
// statements enter the briefing verbatim (records, never re-summarized);
// non-active rows never appear.
func TestBuildComprehensiveBriefingInjectsDecisionsVerbatim(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)
	appendTestTranscript(t, app, "tx-1", "Pricing discussion about design partners.")
	statement := "We locked pricing at $99/mo with two design partners."
	if _, _, err := app.memory.appendDecision("decision-live", statement, map[string]string{"status": decisionStatusActive}); err != nil {
		t.Fatalf("append decision: %v", err)
	}
	if _, _, err := app.memory.appendDecision("decision-stale", "Old superseded pricing call.", map[string]string{"status": "superseded"}); err != nil {
		t.Fatalf("append superseded decision: %v", err)
	}

	responder := func(context.Context, string, openAITextRequest) (string, error) {
		return cannedBriefingDigestJSON(), nil
	}
	dayStart := localDayStart(t, time.Now())
	briefing, err := app.buildComprehensiveBriefing(context.Background(), "test-key", dayStart.UTC(), dayStart.AddDate(0, 0, 1).UTC(), responder)
	if err != nil {
		t.Fatalf("buildComprehensiveBriefing: %v", err)
	}
	text := renderCrossMeetingBriefing(briefing)
	if !strings.Contains(text, statement) {
		t.Fatalf("briefing missing the verbatim decision:\n%s", text)
	}
	if !strings.Contains(text, "On the decision ledger (verbatim):") {
		t.Fatalf("briefing missing the verbatim ledger section:\n%s", text)
	}
	if strings.Contains(text, "Old superseded pricing call.") {
		t.Fatalf("superseded decision leaked:\n%s", text)
	}
}

// TestBuildComprehensiveBriefingUsesCurrentDigestsAsCache is the amendment-A6
// write-through proof: a meeting whose CURRENT stored digest already covers
// its in-range material costs ZERO model calls (the digest IS the map
// output); only uncovered meetings hit the model.
func TestBuildComprehensiveBriefingUsesCurrentDigestsAsCache(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)
	now := time.Now()
	today := dayBucket(now)

	app.memory.entries = append(app.memory.entries,
		meetingMemoryEntry{ID: "cached-tx-1", Kind: meetingMemoryKindTranscript, Text: "AJ: Cached meeting material.", CreatedAt: now.Add(-2 * time.Hour).UTC(), Metadata: map[string]string{"meetingId": "meeting-cached"}},
		meetingMemoryEntry{ID: "fresh-tx-1", Kind: meetingMemoryKindTranscript, Text: "Tyler: Fresh meeting needs mapping.", CreatedAt: now.Add(-time.Hour).UTC(), Metadata: map[string]string{"meetingId": "meeting-fresh"}},
	)
	cachedDigest := `{"meetingId":"meeting-cached","title":"Cached sync","day":"` + today + `",` +
		`"decisions":[{"d":"Keep the cached decision","status":"decided","importance":4}]}`
	upsertBriefingTestDigest(t, app, "meeting-cached", cachedDigest, today,
		now.Add(-3*time.Hour).UTC().Format(time.RFC3339), now.Add(-90*time.Minute).UTC().Format(time.RFC3339))

	var mu sync.Mutex
	var mappedInputs []string
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		mu.Lock()
		mappedInputs = append(mappedInputs, request.Input)
		mu.Unlock()
		return cannedBriefingDigestJSON(), nil
	}
	// instant-based range (not the local day) so entries at now-2h/now-1h stay
	// in range even right after local midnight.
	briefing, err := app.buildComprehensiveBriefing(context.Background(), "test-key", now.Add(-6*time.Hour).UTC(), now.Add(time.Hour).UTC(), responder)
	if err != nil {
		t.Fatalf("buildComprehensiveBriefing: %v", err)
	}
	if len(mappedInputs) != 1 {
		t.Fatalf("map calls=%d, want exactly 1 (the cached meeting must cost zero)", len(mappedInputs))
	}
	if !strings.Contains(mappedInputs[0], "id: meeting-fresh") {
		t.Fatalf("mapped the wrong meeting:\n%s", mappedInputs[0])
	}
	text := renderCrossMeetingBriefing(briefing)
	if !strings.Contains(text, "Keep the cached decision") {
		t.Fatalf("cached digest facts missing from the briefing:\n%s", text)
	}
	if !strings.Contains(text, "Ship the rollout Friday") {
		t.Fatalf("mapped facts missing from the briefing:\n%s", text)
	}
}

// TestBuildComprehensiveBriefingOldDateBeyondDirectoryCap: selection scans
// store entries by CreatedAt — a meeting absent from the 200-record meetings
// directory AND the legacy null-meetingId entries (synthetic per-day key) are
// both reachable for a hard-date briefing.
func TestBuildComprehensiveBriefingOldDateBeyondDirectoryCap(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)
	location := meetingTimeLocation()
	oldMoment := time.Date(2025, 1, 15, 10, 0, 0, 0, location)

	app.memory.entries = append(app.memory.entries,
		meetingMemoryEntry{ID: "old-tx-1", Kind: meetingMemoryKindTranscript, Text: "AJ: We picked the Boot Barn theme.", CreatedAt: oldMoment.UTC(), Metadata: map[string]string{"meetingId": "meeting-ancient"}},
		meetingMemoryEntry{ID: "legacy-tx-1", Kind: meetingMemoryKindTranscript, Text: "Tyler: Legacy note before meeting ids existed.", CreatedAt: oldMoment.Add(time.Hour).UTC(), Metadata: map[string]string{}},
	)

	var mu sync.Mutex
	mappedKeys := map[string]bool{}
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		mu.Lock()
		for _, key := range []string{"meeting-ancient", "meeting-legacy-2025-01-15"} {
			if strings.Contains(request.Input, "id: "+key) {
				mappedKeys[key] = true
			}
		}
		mu.Unlock()
		return cannedBriefingDigestJSON(), nil
	}
	dayStart := time.Date(2025, 1, 15, 0, 0, 0, 0, location)
	briefing, err := app.buildComprehensiveBriefing(context.Background(), "test-key", dayStart.UTC(), dayStart.AddDate(0, 0, 1).UTC(), responder)
	if err != nil {
		t.Fatalf("buildComprehensiveBriefing: %v", err)
	}
	if !mappedKeys["meeting-ancient"] || !mappedKeys["meeting-legacy-2025-01-15"] {
		t.Fatalf("mapped keys=%v, want both the directory-less meeting and the legacy synthetic key", mappedKeys)
	}
	if len(briefing.Days) != 1 || briefing.Days[0].Day != "2025-01-15" {
		t.Fatalf("days=%+v, want exactly 2025-01-15", briefing.Days)
	}
}

// TestBuildComprehensiveBriefingSurvivesPartialMapFailure: one failing
// meeting is skipped (logged), the rest still compose; only a total failure
// errors out so the caller keeps its own last resort.
func TestBuildComprehensiveBriefingSurvivesPartialMapFailure(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)
	now := time.Now()
	app.memory.entries = append(app.memory.entries,
		meetingMemoryEntry{ID: "ok-tx-1", Kind: meetingMemoryKindTranscript, Text: "AJ: Good meeting material.", CreatedAt: now.Add(-2 * time.Hour).UTC(), Metadata: map[string]string{"meetingId": "meeting-good"}},
		meetingMemoryEntry{ID: "bad-tx-1", Kind: meetingMemoryKindTranscript, Text: "Tyler: Doomed meeting material.", CreatedAt: now.Add(-time.Hour).UTC(), Metadata: map[string]string{"meetingId": "meeting-doomed"}},
	)
	responder := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		if strings.Contains(request.Input, "id: meeting-doomed") {
			return "", fmt.Errorf("simulated model failure")
		}
		return cannedBriefingDigestJSON(), nil
	}
	rangeStart, rangeEnd := now.Add(-6*time.Hour).UTC(), now.Add(time.Hour).UTC()
	briefing, err := app.buildComprehensiveBriefing(context.Background(), "test-key", rangeStart, rangeEnd, responder)
	if err != nil {
		t.Fatalf("a partial failure must not fail the briefing: %v", err)
	}
	if briefing.empty() {
		t.Fatal("surviving meeting must still compose")
	}

	// total failure: nothing composable, the error surfaces.
	allFail := func(context.Context, string, openAITextRequest) (string, error) {
		return "", fmt.Errorf("model down")
	}
	if _, err := app.buildComprehensiveBriefing(context.Background(), "test-key", rangeStart, rangeEnd, allFail); err == nil {
		t.Fatal("total map failure must surface an error")
	}
}

// TestChunkEntriesForMapBudgetAndOverflow: greedy char-budget packing, and
// the chunk cap drops the OLDEST chunks so the carry ends on the newest
// material.
func TestChunkEntriesForMapBudgetAndOverflow(t *testing.T) {
	entries := make([]meetingMemoryEntry, 0, 10)
	for index := 0; index < 10; index++ {
		entries = append(entries, meetingMemoryEntry{
			ID:        fmt.Sprintf("tx-%02d", index),
			Kind:      meetingMemoryKindTranscript,
			Text:      strings.Repeat("x", 5000),
			CreatedAt: time.Date(2026, 6, 1, 10, index, 0, 0, time.UTC),
		})
	}

	chunks := chunkEntriesForMap(entries, 16000, 10)
	if len(chunks) != 4 {
		t.Fatalf("chunks=%d, want 4 (3+3+3+1 at ~5KB each under a 16KB budget)", len(chunks))
	}
	if len(chunks[0]) != 3 || chunks[0][0].ID != "tx-00" {
		t.Fatalf("first chunk=%d entries starting %s, want 3 starting tx-00", len(chunks[0]), chunks[0][0].ID)
	}

	capped := chunkEntriesForMap(entries, 16000, 2)
	if len(capped) != 2 {
		t.Fatalf("capped chunks=%d, want 2", len(capped))
	}
	if capped[0][0].ID != "tx-06" {
		t.Fatalf("capped chunks start at %s, want tx-06 (oldest overflow dropped)", capped[0][0].ID)
	}
	if last := capped[1]; last[len(last)-1].ID != "tx-09" {
		t.Fatalf("capped chunks must end at the newest entry, got %s", last[len(last)-1].ID)
	}
}

// TestBriefingSourceEntriesInRangeWhitelistsKinds: only brain/transcript
// material feeds the map stage; hidden entries and artifact bodies never do.
func TestBriefingSourceEntriesInRangeWhitelistsKinds(t *testing.T) {
	t.Setenv("MEETING_TIME_ZONE", "America/Los_Angeles")
	app := newIsolatedKanbanBoardApp(t)
	now := time.Now().UTC()
	app.memory.entries = append(app.memory.entries,
		meetingMemoryEntry{ID: "tx-in", Kind: meetingMemoryKindTranscript, Text: "AJ: in-range line.", CreatedAt: now, Metadata: map[string]string{"meetingId": "m1"}},
		meetingMemoryEntry{ID: "brain-in", Kind: meetingMemoryKindBrain, Text: "## Overview\nIn-range brain.", CreatedAt: now, Metadata: map[string]string{"meetingId": "m1"}},
		meetingMemoryEntry{ID: "artifact-in", Kind: meetingMemoryKindOSArtifact, Text: "data:image/png;base64,QUFB", CreatedAt: now, Metadata: map[string]string{"meetingId": "m1"}},
		meetingMemoryEntry{ID: "tx-hidden", Kind: meetingMemoryKindTranscript, Text: "AJ: quarantined line.", CreatedAt: now, Metadata: map[string]string{"meetingId": "m1", "relevance": "quarantined"}},
		meetingMemoryEntry{ID: "tx-out", Kind: meetingMemoryKindTranscript, Text: "AJ: out-of-range line.", CreatedAt: now.Add(-48 * time.Hour), Metadata: map[string]string{"meetingId": "m1"}},
	)

	groups := app.memory.briefingSourceEntriesInRange(now.Add(-time.Hour), now.Add(time.Hour))
	entries := groups["m1"]
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	if len(entries) != 2 || !strings.Contains(strings.Join(ids, ","), "tx-in") || !strings.Contains(strings.Join(ids, ","), "brain-in") {
		t.Fatalf("ids=%v, want exactly tx-in and brain-in", ids)
	}
}
