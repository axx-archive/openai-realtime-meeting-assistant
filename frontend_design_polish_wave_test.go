package main

import (
	"strings"
	"testing"
)

// Wave 2 — UI polish pass (docs/superpowers/specs/
// 2026-07-06-bonfire-topline-design.md). The founder asked for a feed that is
// slick, uncrowded, and free of odd wraps: cards share one spine, the run card
// is de-chromed, the small type step exists, and deliverable titles wrap
// instead of hard-truncating. Grep-pinned against index.html.

func TestPolishSmallLabelStepDefined(t *testing.T) {
	html := readIndexForComposerPolish(t)
	// The run card references var(--type-label-sm, …); if the token is missing
	// every caption silently renders at --type-label-lg and the hierarchy flattens.
	if !strings.Contains(html, "--type-label-sm:") {
		t.Error("--type-label-sm is undefined — run-card group/meta/resolved lines lose their step down")
	}
}

func TestPolishRunCardOnFeedSpine(t *testing.T) {
	html := readIndexForComposerPolish(t)
	rule := cssRuleBody(html, ".scout-proposal-card")
	if rule == "" {
		t.Fatal(".scout-proposal-card rule missing")
	}
	if strings.Contains(rule, "align-self: stretch") {
		t.Error(".scout-proposal-card still stretches the full lane — it must center on --feed-measure like the goalcard")
	}
	if !strings.Contains(rule, "align-self: center") || !strings.Contains(rule, "var(--feed-measure)") {
		t.Error(".scout-proposal-card must center at width min(var(--feed-measure), 100%) so all feed cards share one spine")
	}
}

func TestPolishManifestCardCentered(t *testing.T) {
	html := readIndexForComposerPolish(t)
	rule := cssRuleBody(html, ".manifest-card")
	if rule == "" {
		t.Fatal(".manifest-card rule missing")
	}
	if !strings.Contains(rule, "align-self: center") {
		t.Error(".manifest-card must align-self: center — it otherwise left-aligns off the feed spine")
	}
}

func TestPolishRunCardMetaDeChromed(t *testing.T) {
	html := readIndexForComposerPolish(t)
	// The authority + weight lines must not carry the bordered-pill treatment
	// that competed with the primary Run button.
	rule := cssRuleBody(html, ".scout-proposal-card__weight")
	if strings.Contains(rule, "border: 1px solid var(--line-1)") || strings.Contains(rule, "background: var(--surface-1)") {
		t.Error("authority/weight still rendered as bordered pills — de-chrome them to a quiet caption line")
	}
}

func TestPolishManifestTitleWraps(t *testing.T) {
	html := readIndexForComposerPolish(t)
	rule := cssRuleBody(html, ".manifest-card__row-title")
	if rule == "" {
		t.Fatal(".manifest-card__row-title rule missing")
	}
	if strings.Contains(rule, "white-space: nowrap") {
		t.Error("deliverable titles still hard-truncate on one line — they must wrap (line-clamp) so real names are visible")
	}
	if !strings.Contains(rule, "-webkit-line-clamp: 2") {
		t.Error(".manifest-card__row-title must clamp to 2 lines, not ellipsis a single line")
	}
}

func TestPolishCheckpointNotesLabelHumanized(t *testing.T) {
	html := readIndexForComposerPolish(t)
	if strings.Contains(html, "notes for the next stage (do_not_touch, answers, must-keeps)") {
		t.Error("the 60-char jargon notes label still leaks internal syntax — shorten to 'notes for the next stage'")
	}
}

func TestPolishRunCardFieldsNoIOSZoom(t *testing.T) {
	html := readIndexForComposerPolish(t)
	// The coarse-pointer 16px block must include .palette__field so tapping a
	// run-card field never zooms the viewport on iOS Safari.
	i := strings.Index(html, ".scout-chat-input,\n        .palette__field,")
	if i < 0 {
		t.Error(".palette__field is not in the coarse-pointer 16px block — run-card fields will zoom on iOS tap")
	}
}

// Wave 4 — task discovery. The empty private thread must seed tappable run
// starters (the visible signpost the gesture-gated palette lacked), the
// composer must hint the launcher, and the '+' door must be labeled.

func TestDiscoveryStartersSeeded(t *testing.T) {
	html := readIndexForComposerPolish(t)
	if !strings.Contains(html, "function buildScoutStarterRow(") {
		t.Fatal("buildScoutStarterRow missing — the empty thread has no discovery starters")
	}
	if !strings.Contains(html, ".scout-starters {") {
		t.Error(".scout-starters CSS missing — starter chips have no styling")
	}
	empty := functionBody(html, "function ensureScoutChatEmptyState(")
	if empty == "" {
		t.Fatal("ensureScoutChatEmptyState not found")
	}
	if !strings.Contains(empty, "buildScoutStarterRow()") {
		t.Error("ensureScoutChatEmptyState must seed starters on the private empty state")
	}
	// The starters must lifecycle with the empty state (torn down together when
	// a message arrives), not linger orphaned.
	if !strings.Contains(html, "'.scout-chat-empty, .scout-starters'") {
		t.Error("starters are not removed alongside .scout-chat-empty — they will orphan after the first message")
	}
	// Starters route to the catalog (the tool palette), covering the headline runs.
	row := functionBody(html, "function buildScoutStarterRow(")
	for _, want := range []string{"packaging studio", "deck outline", "deep research", "grill", "openToolPalette("} {
		if !strings.Contains(row, want) {
			t.Errorf("buildScoutStarterRow missing %q", want)
		}
	}
}

func TestDiscoveryComposerHintsLauncher(t *testing.T) {
	html := readIndexForComposerPolish(t)
	if !strings.Contains(html, "ask Scout, or tap + to run a task") {
		t.Error("composer placeholder no longer hints the task launcher")
	}
	if !strings.Contains(html, `aria-label="Run a task or tool"`) {
		t.Error("the '+' tools button is not labeled as a task launcher")
	}
}
