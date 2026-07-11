package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func runNarrativeMaintainerOnceForTest(t *testing.T, app *kanbanBoardApp, responder openAITextResponder) meetingMemoryEntry {
	t.Helper()
	entry, err := app.runAmbientAgentOnce(narrativeMaintainerAgent(), context.Background(), "test-key", responder, 1)
	if err != nil {
		t.Fatalf("runAmbientAgentOnce(narrative maintainer): %v", err)
	}
	return entry
}

func TestNarrativeMaintainerAgentContract(t *testing.T) {
	agent := narrativeMaintainerAgent()
	if agent.name != narrativeMaintainerAgentName {
		t.Fatalf("agent name=%q, want %q", agent.name, narrativeMaintainerAgentName)
	}
	if agent.inputKind != meetingMemoryKindBrain || agent.artifactKind != meetingMemoryKindNarrative {
		t.Fatalf("agent kinds=%q->%q, want brain->narrative", agent.inputKind, agent.artifactKind)
	}
	if agent.cursorMetadataKey != narrativeCursorKey {
		t.Fatalf("cursor key=%q, want %q", agent.cursorMetadataKey, narrativeCursorKey)
	}
	if agent.defaultInterval != defaultNarrativeMaintainerInterval {
		t.Fatalf("interval=%v, want %v", agent.defaultInterval, defaultNarrativeMaintainerInterval)
	}
	if agent.produce == nil {
		t.Fatal("agent produce func must be set")
	}
	// Both new kinds are knowledge, never UI state: recall must surface them.
	if isUIStateMemoryKind(meetingMemoryKindNarrative) || isUIStateMemoryKind(meetingMemoryKindRunLog) {
		t.Fatal("narrative and run_log must stay searchable (not UI-state kinds)")
	}
}

func TestNormalizeNarrativeSlug(t *testing.T) {
	if slug := normalizeNarrativeSlug(" Samsung TV+ Opportunity! "); slug != "samsung-tv-opportunity" {
		t.Fatalf("slug=%q, want samsung-tv-opportunity", slug)
	}
	if slug := normalizeNarrativeSlug("samsung-tv-plus"); slug != "samsung-tv-plus" {
		t.Fatalf("stable slug=%q, want samsung-tv-plus unchanged", slug)
	}
	if slug := normalizeNarrativeSlug("!!!"); slug != "" {
		t.Fatalf("punctuation-only slug=%q, want empty", slug)
	}
}

func TestNarrativeMaintainerCreatesUpdatesAndExpiresDossiers(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	app := newIsolatedKanbanBoardApp(t)

	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Overview\nAJ pitched Samsung TV Plus on the FAST channel bundle.", nil); err != nil || !appended {
		t.Fatalf("append brain-1: appended=%v err=%v", appended, err)
	}
	if _, appended, err := app.memory.appendOSArtifact("artifact-samsung-1", "# Samsung TV Plus teardown\n\nEvidence.", map[string]string{
		"title": "Samsung TV Plus teardown", "mode": "research",
	}); err != nil || !appended {
		t.Fatalf("append artifact: appended=%v err=%v", appended, err)
	}
	if _, _, err := app.memory.appendRunLog("run-log-agent-thread-research-1", "research run — Samsung TV Plus teardown: complete (requested by AJ, 42s). Deliverable: artifact-samsung-1.", map[string]string{
		"artifactId": "artifact-samsung-1",
	}); err != nil {
		t.Fatalf("append run log: %v", err)
	}
	if _, err := recordSignal(app.memory, "AJ", signalEventArtifactOpened, signalValencePositive, "artifact-samsung-1", "", nil); err != nil {
		t.Fatalf("record signal: %v", err)
	}

	dossierV1 := "## Storyline\nSamsung TV Plus partnership.\n\n## Current status\nPitch delivered.\n\n## Timeline\n- 2026-07-07: pitch delivered\n\n## Key people\n- AJ\n\n## Concerns & counterpoints\n- none yet\n\n## Deliverables & runs\n- Samsung TV Plus teardown (artifact-samsung-1)\n\n## Feedback so far\n- AJ opened the teardown\n\n## Open questions\n- pricing"
	var firstInput string
	entry := runNarrativeMaintainerOnceForTest(t, app, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		firstInput = request.Input
		return fmt.Sprintf(`{"narratives":[{"slug":"Samsung TV Plus","title":"Samsung TV Plus","status":"Pitch delivered, awaiting response","body":%q}]}`, dossierV1), nil
	})

	// The pass fed the model the store-derived context, not just the window.
	for _, want := range []string{"# Recent deliverables", "artifact-samsung-1", "# Recent agent runs", "# Deliverable feedback events", "artifact_opened (positive) by AJ on artifact-samsung-1", "# Brain write-up window", "brain-1"} {
		if !strings.Contains(firstInput, want) {
			t.Fatalf("maintainer input missing %q:\n%s", want, firstInput)
		}
	}
	if entry.Kind != meetingMemoryKindNarrative {
		t.Fatalf("entry kind=%q, want narrative", entry.Kind)
	}
	if entry.Metadata["slug"] != "samsung-tv-plus" {
		t.Fatalf("slug=%q, want the normalized kebab slug", entry.Metadata["slug"])
	}
	if entry.Metadata[narrativeCursorKey] != "brain-1" {
		t.Fatalf("cursor=%q, want brain-1", entry.Metadata[narrativeCursorKey])
	}

	// The dossier is recall material: search must surface it.
	foundNarrative := false
	for _, match := range app.memory.search("samsung tv plus history", 8) {
		if match.Entry.Kind == meetingMemoryKindNarrative && match.Entry.ID == entry.ID {
			foundNarrative = true
		}
	}
	if !foundNarrative {
		t.Fatal("store.search did not surface the active narrative dossier")
	}

	// Second pass: the update appends a new version and expires the predecessor.
	if _, appended, err := app.memory.appendBrainWriteUp("brain-2", "## Overview\nSamsung countered on rev share; Tim to model it.", nil); err != nil || !appended {
		t.Fatalf("append brain-2: appended=%v err=%v", appended, err)
	}
	dossierV2 := strings.Replace(dossierV1, "Pitch delivered.", "Samsung countered on rev share.", 1)
	var secondInput string
	updated := runNarrativeMaintainerOnceForTest(t, app, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		secondInput = request.Input
		if !strings.Contains(request.Input, "brain-2") || strings.Contains(request.Input, "brain-1 |") {
			t.Fatalf("second window should start after the cursor: %s", request.Input)
		}
		return fmt.Sprintf(`{"narratives":[{"slug":"samsung-tv-plus","title":"Samsung TV Plus","status":"Counter on rev share","body":%q}]}`, dossierV2), nil
	})
	if !strings.Contains(secondInput, "# Active storyline dossiers (previous versions)") || !strings.Contains(secondInput, "Pitch delivered.") {
		t.Fatalf("second pass input missing the previous dossier version:\n%s", secondInput)
	}
	if updated.Metadata["previousVersionId"] != entry.ID {
		t.Fatalf("previousVersionId=%q, want %q", updated.Metadata["previousVersionId"], entry.ID)
	}

	// Exactly ONE active dossier per slug; the predecessor is expired, not gone.
	actives := app.activeNarrativeEntries(narrativeStorylineContextLimit)
	if len(actives) != 1 || actives[0].ID != updated.ID {
		t.Fatalf("active dossiers=%d (first=%v), want exactly the new version", len(actives), actives)
	}
	predecessor, ok := app.memory.entryByKindAndID(meetingMemoryKindNarrative, entry.ID)
	if !ok {
		t.Fatal("predecessor dossier must survive on disk")
	}
	if memoryEntryRelevance(predecessor) != relevanceExpired || predecessor.Metadata["supersededBy"] != updated.ID {
		t.Fatalf("predecessor relevance=%q supersededBy=%q, want expired + the new version", memoryEntryRelevance(predecessor), predecessor.Metadata["supersededBy"])
	}
	for _, match := range app.memory.search("samsung tv plus history", 8) {
		if match.Entry.ID == predecessor.ID {
			t.Fatal("expired predecessor must never be a recall candidate")
		}
	}

	// A legitimate "nothing changed" pass still advances the cursor so the
	// same brains are never re-fed forever.
	if _, appended, err := app.memory.appendBrainWriteUp("brain-3", "## Overview\nUnrelated stand-up chatter.", nil); err != nil || !appended {
		t.Fatalf("append brain-3: appended=%v err=%v", appended, err)
	}
	if noop := runNarrativeMaintainerOnceForTest(t, app, func(context.Context, string, openAITextRequest) (string, error) {
		return `{"narratives":[]}`, nil
	}); strings.TrimSpace(noop.ID) != "" {
		t.Fatalf("no-op pass appended %q, want nothing", noop.ID)
	}
	newest, ok := app.memory.entryByKindAndID(meetingMemoryKindNarrative, updated.ID)
	if !ok || newest.Metadata[narrativeCursorKey] != "brain-3" {
		t.Fatalf("cursor after no-op=%q, want brain-3 stamped on the newest dossier", newest.Metadata[narrativeCursorKey])
	}
}

// The maintainer's effort rides the doctrine floor (agent_runner_anthropic.go):
// default medium — the summarization-maintenance level — low/minimal clamp UP,
// above-floor values pass through, junk falls back to the floor. No hardcoded
// "low" survives anywhere on this seat.
func TestNarrativeMaintainerEffortFloor(t *testing.T) {
	t.Setenv("NARRATIVE_MAINTAINER_EFFORT", "")
	if got := narrativeMaintainerEffort(); got != doctrineEffortFloor {
		t.Fatalf("narrativeMaintainerEffort() default=%q, want the %s doctrine floor", got, doctrineEffortFloor)
	}
	t.Setenv("NARRATIVE_MAINTAINER_EFFORT", "low")
	if got := narrativeMaintainerEffort(); got != "medium" {
		t.Fatalf("narrativeMaintainerEffort() with low=%q, want medium (doctrine floor)", got)
	}
	t.Setenv("NARRATIVE_MAINTAINER_EFFORT", "minimal")
	if got := narrativeMaintainerEffort(); got != "medium" {
		t.Fatalf("narrativeMaintainerEffort() with minimal=%q, want medium (doctrine floor)", got)
	}
	t.Setenv("NARRATIVE_MAINTAINER_EFFORT", "High")
	if got := narrativeMaintainerEffort(); got != "high" {
		t.Fatalf("narrativeMaintainerEffort() with high=%q, want high (above the floor passes through)", got)
	}
	t.Setenv("NARRATIVE_MAINTAINER_EFFORT", "galactic")
	if got := narrativeMaintainerEffort(); got != doctrineEffortFloor {
		t.Fatalf("narrativeMaintainerEffort() with junk=%q, want the %s floor fallback", got, doctrineEffortFloor)
	}
}

// The keyed-Anthropic pass sends the floored effort on the wire — never the
// pre-doctrine hardcoded "low" the review caught.
func TestNarrativeMaintainerAnthropicEffortNeverLow(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-anthropic-key")
	t.Setenv("NARRATIVE_MAINTAINER_EFFORT", "")
	app := newIsolatedKanbanBoardApp(t)
	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Overview\nSamsung pitch.", nil); err != nil || !appended {
		t.Fatalf("append brain-1: appended=%v err=%v", appended, err)
	}

	gotEffort := ""
	swapAnthropicTextResponder(t, func(_ context.Context, _ string, request anthropicTextRequest) (string, error) {
		gotEffort = request.Effort
		return `{"narratives":[]}`, nil
	})
	runNarrativeMaintainerOnceForTest(t, app, func(context.Context, string, openAITextRequest) (string, error) {
		t.Fatal("keyed-Anthropic pass must not ride the OpenAI responder")
		return "", nil
	})
	if gotEffort != doctrineEffortFloor {
		t.Fatalf("Anthropic effort=%q, want the %s doctrine floor (never low)", gotEffort, doctrineEffortFloor)
	}
}

// Cold start + an all-empty workspace: a pass that legitimately returns
// {"narratives":[]} before ANY dossier exists must still advance the cursor —
// via a hidden cursor-carrier entry — so the same brain window is never
// re-fed forever. The carrier is invisible to every narrative read surface.
func TestNarrativeMaintainerColdStartEmptyPassAdvancesCursor(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("NARRATIVE_MAINTAINER_EFFORT", "")
	app := newIsolatedKanbanBoardApp(t)
	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Overview\nStand-up chatter, nothing storyline-worthy.", nil); err != nil || !appended {
		t.Fatalf("append brain-1: appended=%v err=%v", appended, err)
	}

	passes := 0
	empty := func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		passes++
		if request.ReasoningEffort != doctrineEffortFloor {
			t.Fatalf("keyless effort=%q, want the %s doctrine floor (never low)", request.ReasoningEffort, doctrineEffortFloor)
		}
		return `{"narratives":[]}`, nil
	}
	if entry := runNarrativeMaintainerOnceForTest(t, app, empty); strings.TrimSpace(entry.ID) != "" {
		t.Fatalf("cold-start empty pass appended %q, want no dossier", entry.ID)
	}
	if passes != 1 {
		t.Fatalf("model passes=%d, want exactly one", passes)
	}

	// The cursor advanced even with zero dossiers: a second pass with no new
	// brains finds nothing unconsumed and never calls the model.
	if entry := runNarrativeMaintainerOnceForTest(t, app, empty); strings.TrimSpace(entry.ID) != "" {
		t.Fatalf("second empty pass appended %q, want nothing", entry.ID)
	}
	if passes != 1 {
		t.Fatalf("model passes after cursor stamp=%d, want still one (the window must not be re-fed)", passes)
	}

	// The carrier never surfaces: not an active dossier, never recall material.
	if actives := app.activeNarrativeEntries(narrativeStorylineContextLimit); len(actives) != 0 {
		t.Fatalf("cursor carrier leaked into active dossiers: %v", actives)
	}
	for _, match := range app.memory.search("narrative maintainer cursor", 8) {
		if match.Entry.Kind == meetingMemoryKindNarrative {
			t.Fatal("cursor carrier must never be a recall candidate")
		}
	}

	// New brains after the stamp ARE fed — starting after the consumed window —
	// and a real dossier takes over as the cursor holder.
	if _, appended, err := app.memory.appendBrainWriteUp("brain-2", "## Overview\nSamsung pitch delivered.", nil); err != nil || !appended {
		t.Fatalf("append brain-2: appended=%v err=%v", appended, err)
	}
	var window string
	entry := runNarrativeMaintainerOnceForTest(t, app, func(_ context.Context, _ string, request openAITextRequest) (string, error) {
		window = request.Input
		return `{"narratives":[{"slug":"samsung","title":"Samsung","status":"Pitched","body":"## Storyline\nSamsung."}]}`, nil
	})
	if strings.Contains(window, "brain-1 |") || !strings.Contains(window, "brain-2") {
		t.Fatalf("post-stamp window should start after brain-1:\n%s", window)
	}
	if entry.Metadata[narrativeCursorKey] != "brain-2" {
		t.Fatalf("dossier cursor=%q, want brain-2", entry.Metadata[narrativeCursorKey])
	}
	if actives := app.activeNarrativeEntries(narrativeStorylineContextLimit); len(actives) != 1 || actives[0].ID != entry.ID {
		t.Fatalf("active dossiers=%v, want exactly the samsung dossier", actives)
	}
}

func TestNarrativeMaintainerSkipsUnparseableOutput(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	app := newIsolatedKanbanBoardApp(t)
	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Overview\nSamsung pitch.", nil); err != nil || !appended {
		t.Fatalf("append brain-1: appended=%v err=%v", appended, err)
	}
	entry := runNarrativeMaintainerOnceForTest(t, app, func(context.Context, string, openAITextRequest) (string, error) {
		return "not json at all", nil
	})
	if strings.TrimSpace(entry.ID) != "" {
		t.Fatalf("unparseable output persisted %q, want nothing", entry.ID)
	}
	if got := app.memory.entriesOfKind(meetingMemoryKindNarrative, 0); len(got) != 0 {
		t.Fatalf("narrative entries=%d, want none", len(got))
	}
}

func TestActiveNarrativeEntriesCapsAndDedupes(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	for index := 0; index < narrativeStorylineContextLimit+2; index++ {
		slug := fmt.Sprintf("storyline-%d", index)
		if _, appended, err := app.memory.appendNarrative(fmt.Sprintf("narrative-%d", index), "## Storyline\n"+slug, map[string]string{
			"slug": slug, "title": slug, "status": "active line",
		}); err != nil || !appended {
			t.Fatalf("append %s: appended=%v err=%v", slug, appended, err)
		}
	}
	actives := app.activeNarrativeEntries(narrativeStorylineContextLimit)
	if len(actives) != narrativeStorylineContextLimit {
		t.Fatalf("actives=%d, want the %d cap", len(actives), narrativeStorylineContextLimit)
	}
	// newest first
	if actives[0].Metadata["slug"] != fmt.Sprintf("storyline-%d", narrativeStorylineContextLimit+1) {
		t.Fatalf("first active=%q, want the newest slug", actives[0].Metadata["slug"])
	}
}

// The segmented topic-timeline (D/B): narrative dossiers, scoped to the
// current sitting via record.StartedAt, become one segment per slug — ordered
// by firstSeenAt, weighted by decayed recurrence. The dominant segment (most
// recurrent) names the room, and the SAME reduce marks it "current" in the
// snapshot, so title and timeline can never contradict.
func TestMeetingSegmentsTimelineDominantAndSnapshot(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	app.noteMeetingAdmission(officeRoomID, "AJ")
	record, ok := app.meetings.activeRecord(officeRoomID)
	if !ok {
		t.Fatal("no active record after admission")
	}
	started, _ := time.Parse(time.RFC3339Nano, record.StartedAt)

	seg := func(id, slug, title string, firstOffset, lastOffset time.Duration) {
		if _, appended, err := app.memory.appendNarrative(id, "## Storyline\n"+slug, map[string]string{
			"slug":        slug,
			"title":       title,
			"status":      "active line",
			"firstSeenAt": started.Add(firstOffset).UTC().Format(time.RFC3339Nano),
			"lastSeenAt":  started.Add(lastOffset).UTC().Format(time.RFC3339Nano),
		}); err != nil || !appended {
			t.Fatalf("append %s: appended=%v err=%v", id, appended, err)
		}
	}
	// ball-dogs recurs across two passes (earlier firstSeen); impossible is a
	// single later pass. A stale prior-sitting dossier must be scoped OUT.
	seg("n-bd-1", "ball-dogs", "Ball Dogs IP", 1*time.Minute, 3*time.Minute)
	seg("n-bd-2", "ball-dogs", "Ball Dogs IP", 1*time.Minute, 8*time.Minute)
	seg("n-im-1", "impossible", "Impossible Films", 10*time.Minute, 12*time.Minute)

	segments := app.meetingSegments(record, time.Now().UTC())
	if len(segments) != 2 {
		t.Fatalf("segments=%d, want 2 (one per slug)", len(segments))
	}
	// timeline order = firstSeenAt ascending.
	if segments[0].Slug != "ball-dogs" || segments[1].Slug != "impossible" {
		t.Fatalf("timeline order=%q,%q want ball-dogs,impossible", segments[0].Slug, segments[1].Slug)
	}
	if !segments[0].FirstSeenAt.Equal(started.Add(1 * time.Minute)) {
		t.Fatalf("ball-dogs firstSeen=%v, want the earliest stamped window", segments[0].FirstSeenAt)
	}
	// recurrence → ball-dogs is dominant even though impossible is more recent.
	if idx := dominantSegmentIndex(segments); idx < 0 || segments[idx].Slug != "ball-dogs" {
		t.Fatalf("dominant index=%d, want ball-dogs; segments=%+v", idx, segments)
	}
	if got := app.dominantMeetingTitle(record, time.Now().UTC()); got != "Ball Dogs IP" {
		t.Fatalf("dominantMeetingTitle=%q, want the dominant segment's title", got)
	}

	// snapshot rows mark exactly the dominant slug "current"; the rest "past".
	rows := app.meetingSegmentRows(time.Now().UTC())
	if len(rows) != 2 {
		t.Fatalf("segment rows=%d, want 2", len(rows))
	}
	current := ""
	for _, row := range rows {
		if row["status"] == "current" {
			if current != "" {
				t.Fatalf("more than one current segment: %+v", rows)
			}
			current = row["slug"].(string)
		}
	}
	if current != "ball-dogs" {
		t.Fatalf("current segment=%q, want ball-dogs (matches the dominant title)", current)
	}
}

func TestAssistantQueryInputIncludesActiveStorylines(t *testing.T) {
	storylines := []meetingMemoryEntry{
		{
			ID: "narrative-1", Kind: meetingMemoryKindNarrative,
			Text:     "## Storyline\nSamsung TV Plus partnership.",
			Metadata: map[string]string{"slug": "samsung-tv-plus", "title": "Samsung TV Plus", "status": "Counter on rev share"},
		},
		{
			ID: "narrative-2", Kind: meetingMemoryKindNarrative,
			Text:     "## Storyline\nStationTenn packaging.",
			Metadata: map[string]string{"slug": "stationtenn", "title": "StationTenn"},
		},
	}
	input := buildAssistantQueryInput("where are we with samsung?", nil, nil, nil, storylines, nil, time.Now(), false)
	if !strings.Contains(input, "# Active storylines") {
		t.Fatalf("input missing the storylines section:\n%s", input)
	}
	if !strings.Contains(input, "- Samsung TV Plus: Counter on rev share") {
		t.Fatalf("input missing the titled status line:\n%s", input)
	}
	// A dossier without a stamped status degrades to its compact body head.
	if !strings.Contains(input, "- StationTenn: ## Storyline StationTenn packaging.") {
		t.Fatalf("input missing the fallback status line:\n%s", input)
	}
	empty := buildAssistantQueryInput("where are we?", nil, nil, nil, nil, nil, time.Now(), false)
	if strings.Contains(empty, "# Active storylines") {
		t.Fatal("empty storylines must not emit the section header")
	}
}

// A dossier discussed across several sittings accumulates a de-duped union of
// every meeting it has spanned (kanban-card-107) instead of overwriting to the
// latest sitting's single meetingId.
func TestNarrativeMaintainerAccumulatesMeetingIds(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	app := newIsolatedKanbanBoardApp(t)

	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Overview\nSamsung pitch.", nil); err != nil || !appended {
		t.Fatalf("append brain-1: appended=%v err=%v", appended, err)
	}
	meetingA := app.memory.currentMeetingID(officeRoomID)
	v1 := runNarrativeMaintainerOnceForTest(t, app, func(context.Context, string, openAITextRequest) (string, error) {
		return `{"narratives":[{"slug":"samsung","title":"Samsung","status":"pitch","body":"## Storyline\nSamsung."}]}`, nil
	})
	if got := v1.Metadata["meetingIds"]; got != meetingA {
		t.Fatalf("pass 1 meetingIds=%q, want %q", got, meetingA)
	}

	// Rotate to a fresh sitting; the same dossier updates.
	app.memory.rotateMeetingID(officeRoomID)
	if _, appended, err := app.memory.appendBrainWriteUp("brain-2", "## Overview\nSamsung counter.", nil); err != nil || !appended {
		t.Fatalf("append brain-2: appended=%v err=%v", appended, err)
	}
	meetingB := app.memory.currentMeetingID(officeRoomID)
	if meetingB == "" || meetingB == meetingA {
		t.Fatalf("expected a fresh meeting id after rotate, got %q (was %q)", meetingB, meetingA)
	}
	v2 := runNarrativeMaintainerOnceForTest(t, app, func(context.Context, string, openAITextRequest) (string, error) {
		return `{"narratives":[{"slug":"samsung","title":"Samsung","status":"counter","body":"## Storyline\nSamsung counter."}]}`, nil
	})
	ids := splitNarrativeMeetingIDs(v2.Metadata["meetingIds"])
	if len(ids) != 2 || ids[0] != meetingA || ids[1] != meetingB {
		t.Fatalf("pass 2 meetingIds=%v, want the union [%s %s] (not an overwrite)", ids, meetingA, meetingB)
	}
	// The single meetingId still tracks the current sitting (snapshotForMeeting scope).
	if v2.Metadata["meetingId"] != meetingB {
		t.Fatalf("meetingId=%q, want the current sitting %q", v2.Metadata["meetingId"], meetingB)
	}
}

/* ---------- item 1.3b / Q6: alias resolution + overflow spill ---------- */

func TestNarrativeMaintainerAliasPreventsSlugFork(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	app := newIsolatedKanbanBoardApp(t)
	// an existing dossier under slug "samsung-tv" carrying its aliases.
	if _, appended, err := app.memory.appendNarrative("narrative-samsung-tv", "## Storyline\nSamsung TV Plus opportunity.", map[string]string{
		"slug": "samsung-tv", "title": "Samsung TV", "status": "pitch",
		"roomId": officeRoomID, digestAliasesMetadataKey: "Samsung TV Plus, the Korean deal",
	}); err != nil || !appended {
		t.Fatalf("seed dossier: appended=%v err=%v", appended, err)
	}
	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Overview\nSamsung TV Plus advanced.", nil); err != nil || !appended {
		t.Fatalf("append brain-1: appended=%v err=%v", appended, err)
	}

	// the model forks a NEW slug for the SAME storyline; alias resolution catches it.
	runNarrativeMaintainerOnceForTest(t, app, func(context.Context, string, openAITextRequest) (string, error) {
		return `{"narratives":[{"slug":"samsung-tv-plus","title":"Samsung TV Plus","status":"advancing","aliases":["Samsung TV Plus deal"],"body":"## Storyline\nSamsung TV Plus advancing."}]}`, nil
	})

	active := app.activeNarrativeEntries(10)
	if len(active) != 1 {
		t.Fatalf("active dossiers = %d, want ONE (the fork resolved to the existing storyline)", len(active))
	}
	if slug := active[0].Metadata["slug"]; slug != "samsung-tv" {
		t.Fatalf("resolved slug = %q, want the existing samsung-tv (no fork)", slug)
	}
	if active[0].Metadata["previousVersionId"] != "narrative-samsung-tv" {
		t.Fatalf("new version = %+v, want it to supersede the existing dossier", active[0].Metadata)
	}
}

func TestNarrativeMeetingIdsOverflowSpill(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	app := newIsolatedKanbanBoardApp(t)
	// a predecessor dossier already at the meetingIds cap.
	full := make([]string, 0, ledgerMeetingIDCap)
	for index := 0; index < ledgerMeetingIDCap; index++ {
		full = append(full, fmt.Sprintf("m-%02d", index))
	}
	if _, appended, err := app.memory.appendNarrative("narrative-samsung", "## Storyline\nSamsung.", map[string]string{
		"slug": "samsung", "title": "Samsung", "roomId": officeRoomID,
		"meetingIds": strings.Join(full, ","),
	}); err != nil || !appended {
		t.Fatalf("seed dossier: appended=%v err=%v", appended, err)
	}
	if _, appended, err := app.memory.appendBrainWriteUp("brain-1", "## Overview\nSamsung.", nil); err != nil || !appended {
		t.Fatalf("append brain-1: appended=%v err=%v", appended, err)
	}
	meetingNow := app.memory.currentMeetingID(officeRoomID)

	version := runNarrativeMaintainerOnceForTest(t, app, func(context.Context, string, openAITextRequest) (string, error) {
		return `{"narratives":[{"slug":"samsung","title":"Samsung","status":"live","body":"## Storyline\nSamsung."}]}`, nil
	})
	ids := splitNarrativeMeetingIDs(version.Metadata["meetingIds"])
	if len(ids) != ledgerMeetingIDCap {
		t.Fatalf("meetingIds len = %d, want capped at %d", len(ids), ledgerMeetingIDCap)
	}
	if !ledgerSliceContains(splitNarrativeMeetingIDs(version.Metadata["meetingIdsOverflow"]), "m-00") {
		t.Fatalf("overflow = %q, want the evicted m-00 preserved (spill-never-shed)", version.Metadata["meetingIdsOverflow"])
	}
	if !ledgerSliceContains(ids, meetingNow) {
		t.Fatalf("primary meetingIds = %+v, want the current sitting %q on the list", ids, meetingNow)
	}
}

// F18: resolveNarrativeByAlias must scan ALL active dossiers, not just the pinned
// newest-N context window — a synonym of an older storyline resolves to it
// instead of forking a second slug.
func TestResolveNarrativeByAliasScansBeyondContextWindow(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	// the TARGET storyline is the OLDEST dossier, carrying a distinctive alias.
	if _, appended, err := app.memory.appendNarrative("narrative-zephyr", "## Storyline\nZephyr launch.", map[string]string{
		"slug": "zephyr-launch", "title": "Zephyr Launch", "status": "active",
		"roomId": officeRoomID, digestAliasesMetadataKey: "the zephyr rollout, zephyr GA",
	}); err != nil || !appended {
		t.Fatalf("seed target dossier: appended=%v err=%v", appended, err)
	}
	// push it well past the newest-N window with unrelated newer dossiers.
	for index := 0; index < narrativeStorylineContextLimit+2; index++ {
		slug := fmt.Sprintf("filler-%d", index)
		if _, appended, err := app.memory.appendNarrative(fmt.Sprintf("narrative-%s", slug), "## Storyline\n"+slug, map[string]string{
			"slug": slug, "title": slug, "status": "active", "roomId": officeRoomID,
		}); err != nil || !appended {
			t.Fatalf("seed filler %s: appended=%v err=%v", slug, appended, err)
		}
	}
	// sanity: the target is NOT in the pinned context window anymore.
	inWindow := false
	for _, dossier := range app.activeNarrativeEntries(narrativeStorylineContextLimit) {
		if dossier.Metadata["slug"] == "zephyr-launch" {
			inWindow = true
		}
	}
	if inWindow {
		t.Fatal("test setup wrong: the target must be past the pinned window")
	}

	// a fresh fork slug that is really a synonym of the OLD target.
	resolvedSlug, entry, ok := app.resolveNarrativeByAlias("zephyr-ga", "Zephyr GA rollout", []string{"the zephyr rollout"})
	if !ok {
		t.Fatal("alias resolution missed the older storyline (F18 — the unbounded scan should find it)")
	}
	if resolvedSlug != "zephyr-launch" || entry.Metadata["slug"] != "zephyr-launch" {
		t.Fatalf("resolved to %q, want zephyr-launch", resolvedSlug)
	}
}
