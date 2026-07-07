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
