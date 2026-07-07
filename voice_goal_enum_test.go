package main

import "testing"

// Wave 4 — voice parity. The voice initiate_goal 'tool' preset must enumerate
// the same run-type catalog the typed router uses (buildToolsPayload), so voice
// can pick a real run-type instead of guessing from a prose example list.

func TestVoiceGoalPresetIDsFromCatalog(t *testing.T) {
	ids := packagingRunPresetIDs()
	if len(ids) == 0 {
		t.Fatal("packagingRunPresetIDs returned no ids — the voice tool enum would be empty")
	}
	// Cross-check against the single taxonomy source.
	want := 0
	for _, group := range buildToolsPayload() {
		want += len(group.Tools)
	}
	if len(ids) != want {
		t.Errorf("packagingRunPresetIDs returned %d ids, buildToolsPayload has %d tools — they must be the same taxonomy", len(ids), want)
	}
	// The headline runs the discovery starters point at must be launchable ids.
	found := map[string]bool{}
	for _, id := range ids {
		found[id] = true
	}
	for _, id := range []string{"deck_outline", "deep_research", "grill_pressure_test"} {
		if !found[id] {
			t.Errorf("run-type %q missing from the preset catalog", id)
		}
	}
}
