package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSlopDecayFixture seeds a memory.jsonl with dated rows (the recall gold
// fixture technique) so entries can be older than the decay threshold.
func writeSlopDecayFixture(t *testing.T, path string, entries []meetingMemoryEntry) {
	t.Helper()
	var lines []string
	for _, entry := range entries {
		raw, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("marshal fixture entry: %v", err)
		}
		lines = append(lines, string(raw))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

// Wave 8 D4: a brain older than BRAIN_DECAY_DAYS whose meeting has a current
// meeting_digest becomes a slop candidate; an uncovered one does not; a
// decision never does.
func TestSlopBrainDecayCandidates(t *testing.T) {
	dir := t.TempDir()
	memoryPath := filepath.Join(dir, "memory.jsonl")
	t.Setenv("MEETING_MEMORY_PATH", memoryPath)
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "board.json"))
	t.Setenv("BRAIN_DECAY_DAYS", "45")
	now := time.Now().UTC()
	old := now.Add(-60 * 24 * time.Hour)
	writeSlopDecayFixture(t, memoryPath, []meetingMemoryEntry{
		{ID: "brain-covered", Kind: meetingMemoryKindBrain, Text: "## Overview\nCovered meeting write-up.", CreatedAt: old, Metadata: map[string]string{"meetingId": "meeting-covered", "roomId": officeRoomID}},
		{ID: "brain-uncovered", Kind: meetingMemoryKindBrain, Text: "## Overview\nUncovered meeting write-up.", CreatedAt: old, Metadata: map[string]string{"meetingId": "meeting-uncovered", "roomId": officeRoomID}},
		{ID: "brain-young", Kind: meetingMemoryKindBrain, Text: "## Overview\nRecent write-up.", CreatedAt: now.Add(-10 * 24 * time.Hour), Metadata: map[string]string{"meetingId": "meeting-covered", "roomId": officeRoomID}},
		{ID: "decision-old", Kind: meetingMemoryKindDecision, Text: "Choose vendor Zebra", CreatedAt: old, Metadata: map[string]string{"status": decisionStatusActive, "meetingId": "meeting-covered"}},
	})
	app := newKanbanBoardApp()
	payload := cannedMeetingDigestJSON()
	if _, err := app.memory.upsertDigest(meetingMemoryKindMeetingDigest, "meeting-covered", payload, map[string]string{"meetingId": "meeting-covered", digestDayMetadataKey: dayBucket(now)}); err != nil {
		t.Fatalf("upsert digest: %v", err)
	}

	candidates, _ := app.buildSlopCandidates(slopClassifierAgent(), now)
	ids := map[string]bool{}
	for _, candidate := range candidates {
		ids[candidate.ID] = true
	}
	if !ids["brain-covered"] {
		t.Fatalf("covered old brain must be a candidate, got %v", ids)
	}
	if ids["brain-uncovered"] {
		t.Fatal("an uncovered brain must never be a candidate")
	}
	if ids["brain-young"] {
		t.Fatal("a brain younger than the decay threshold must never be a candidate")
	}
	if ids["decision-old"] {
		t.Fatal("a decision must never be a candidate")
	}

	// deny-list table: the kind gate + decay age, in code.
	oldBrain := meetingMemoryEntry{ID: "b", Kind: meetingMemoryKindBrain, Text: "x", CreatedAt: old}
	if !slopCandidateEligible(oldBrain, now) {
		t.Fatal("old brain must pass the deny-list age gate")
	}
	if slopCandidateEligible(meetingMemoryEntry{ID: "b", Kind: meetingMemoryKindBrain, Text: "x", CreatedAt: now.Add(-30 * 24 * time.Hour)}, now) {
		t.Fatal("a 30-day brain is under the 45-day decay threshold")
	}
	pinned := oldBrain
	pinned.Metadata = map[string]string{"pinned": "true"}
	if slopCandidateEligible(pinned, now) {
		t.Fatal("a pinned brain is exempt")
	}
	if slopCandidateEligible(meetingMemoryEntry{ID: "d", Kind: meetingMemoryKindDecision, Text: "x", CreatedAt: old}, now) {
		t.Fatal("decisions never decay")
	}
	// the classifier input labels the brain honestly.
	input := app.buildSlopClassifierInput(candidates, now)
	if !strings.Contains(input, "kind=brain") || !strings.Contains(input, "meeting=meeting-covered digest_covered=true") {
		t.Fatalf("classifier input missing the brain coverage line:\n%s", input)
	}
	// already-classified brains are not re-billed.
	if _, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindBrain, "brain-covered", "## Overview\nCovered meeting write-up.", map[string]string{"classifierVerdict": "keep"}); err != nil {
		t.Fatalf("stamp verdict: %v", err)
	}
	if again, _ := app.buildSlopCandidates(slopClassifierAgent(), now); len(again) != 0 {
		t.Fatalf("classified brain re-fed: %v", memoryEntryIDs(again))
	}
}
