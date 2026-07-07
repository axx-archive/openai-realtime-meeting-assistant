package main

import (
	"strings"
	"testing"
)

// Wave 6 — deliverables drawer + feedback linkage (docs/superpowers/specs/
// 2026-07-06-bonfire-topline-design.md). One composer action drives the live
// follow-up seam for ANY deliverable; a drive-style drawer lets you drop a
// deliverable into any thread/channel and give it feedback that resumes work.

func TestFeedbackArmHelperGeneralized(t *testing.T) {
	html := readIndexForComposerPolish(t)
	fn := functionBody(html, "function armScoutFollowUpTarget(")
	if fn == "" {
		t.Fatal("armScoutFollowUpTarget missing — no generalized arm for goal/packaged deliverables")
	}
	for _, want := range []string{"scoutFollowUpTarget =", "artifactId", "renderScoutFollowUpTarget()"} {
		if !strings.Contains(fn, want) {
			t.Errorf("armScoutFollowUpTarget missing %q", want)
		}
	}
}

func TestDeliverablesDrawerPresent(t *testing.T) {
	html := readIndexForComposerPolish(t)
	// composer affordance + popover mount
	if !strings.Contains(html, `id="scoutChatDeliverables"`) {
		t.Error("deliverables drawer button missing from the composer")
	}
	if !strings.Contains(html, `id="scoutDeliverablesPopover"`) {
		t.Error("deliverables popover mount missing")
	}
	for _, fn := range []string{"function toggleDeliverablesDrawer(", "function deliverableDrawerEntries(", "function renderDeliverablesDrawer("} {
		if !strings.Contains(html, fn) {
			t.Errorf("drawer logic missing: %s", fn)
		}
	}
	// a picked deliverable arms the follow-up seam, not a plain prefill
	render := functionBody(html, "function renderDeliverablesDrawer(")
	if !strings.Contains(render, "armScoutFollowUpTarget(") {
		t.Error("picking a drawer deliverable must arm the follow-up seam")
	}
	// the drawer is styled (floats above the composer)
	if !strings.Contains(html, ".scout-deliverables__sheet {") {
		t.Error(".scout-deliverables__sheet CSS missing — drawer has no floating sheet")
	}
}

func TestManifestRowsHaveFeedbackDoor(t *testing.T) {
	html := readIndexForComposerPolish(t)
	fn := functionBody(html, "function scoutManifestCardNode(")
	if fn == "" {
		t.Fatal("scoutManifestCardNode not found")
	}
	if !strings.Contains(fn, "armScoutFollowUpTarget(") {
		t.Error("shipped-package deliverable rows have no feedback door — the manifest card cannot resume a specific deliverable")
	}
	if !strings.Contains(fn, "'feedback'") {
		t.Error("manifest rows must render a 'feedback' pill")
	}
}

func TestGoalCardSendNotesReachesWorker(t *testing.T) {
	html := readIndexForComposerPolish(t)
	// The old behavior only prefilled composer text; it must now arm the seam so
	// feedback actually reaches the worker and resumes the goal.
	if strings.Contains(html, "promptScoutForWork('', `notes on") {
		t.Error("goal-card 'send notes' still only prefills text — it must arm armScoutFollowUpTarget to resume the goal")
	}
}
