package main

import (
	"os"
	"strings"
	"testing"
)

// The live-platform design audit's P0 ship-blockers (docs/plans/
// live-platform-design-spec.md). Grep-pinned against index.html the same way
// the rest of the frontend contracts are.

func readIndexForDesignP0(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(data)
}

// P0-1 — the founder-named class: markdown source must render as language in
// every deliverable body, the checkpoint brief, and previews — never leak as
// raw syntax.
func TestDesignP0MarkdownRendersNotLeaks(t *testing.T) {
	html := readIndexForDesignP0(t)

	inline := functionBody(html, "function appendArtifactInlineNodes(")
	if inline == "" {
		t.Fatal("appendArtifactInlineNodes not found")
	}
	for _, want := range []string{`\*\*([^*\n]+)\*\*`, "`([^`\\n]+)`", "artifact-read__code"} {
		if !strings.Contains(inline, want) {
			t.Errorf("appendArtifactInlineNodes missing %q — bold/code no longer rendered", want)
		}
	}

	body := functionBody(html, "function appendArtifactBodyNodes(")
	if body == "" {
		t.Fatal("appendArtifactBodyNodes not found")
	}
	for _, want := range []string{`#{1,6}`, "artifact-read__subhead"} {
		if !strings.Contains(body, want) {
			t.Errorf("appendArtifactBodyNodes missing %q — a raw ## heading would leak its hashes", want)
		}
	}

	// The checkpoint brief (the founder's literal cited leak) must route
	// through the safe block renderer, not a raw text dump.
	if strings.Contains(html, "bfEl('div', 'goalcard__checkpoint-brief', briefText)") {
		t.Error("checkpoint brief still dumps raw text — appendArtifactBodyNodes must render it")
	}
	if !strings.Contains(html, "appendArtifactBodyNodes(brief, briefText)") {
		t.Error("checkpoint brief must render through appendArtifactBodyNodes(brief, briefText)")
	}

	// Previews strip inline tokens so no ** / backtick reaches a card.
	preview := functionBody(html, "function compactArtifactPreview(")
	if !strings.Contains(preview, `.replace(/\*\*([^*]+)\*\*/g`) || !strings.Contains(preview, "`([^`]+)`") {
		t.Error("compactArtifactPreview must strip inline markdown tokens")
	}

	// The stylesheet carries the two new inline classes.
	for _, want := range []string{".artifact-read__code {", ".artifact-read__subhead {"} {
		if !strings.Contains(html, want) {
			t.Errorf("stylesheet missing %q", want)
		}
	}
}

// P0-2 — the document header must not speak machine to the reader.
func TestDesignP0HeaderSpeaksToReader(t *testing.T) {
	html := readIndexForDesignP0(t)

	kicker := functionBody(html, "function artifactStageKicker(")
	if !strings.Contains(kicker, "'document'") {
		t.Error("artifactStageKicker must map raw formats (markdown/md/text) to 'document', never show a file-format word")
	}

	mode := functionBody(html, "function artifactModeLabel(")
	if !strings.Contains(mode, `artifacts?$`) {
		t.Error("artifactModeLabel must collapse an artifact(s)-suffixed mode so it never doubles to 'artifacts artifact'")
	}

	worker := functionBody(html, "function artifactWorkerLabel(")
	if !strings.Contains(worker, "claude · fable 5") {
		t.Error("artifactWorkerLabel must speak the model in the reader's voice (claude · fable 5), never a raw worker slug")
	}

	// the settled-percent guard (renderArtifactRead has nested arrows that
	// defeat brace-matched extraction — grep the whole file for the marker)
	if !strings.Contains(html, "const settled = status === 'complete' || status === 'published'") ||
		!strings.Contains(html, "settled ? '' : `${Math.round(progress)}%`") {
		t.Error("renderArtifactRead must omit the percent chip once the artifact is settled (100% beside 'complete' is noise)")
	}
}

// P0-3 + P0-4 — mobile hit targets and legibility (CSS pins).
func TestDesignP0MobileErgonomics(t *testing.T) {
	html := readIndexForDesignP0(t)

	// P0-3: send + attach get a 44px hit extension; choices bump on mobile.
	if !strings.Contains(html, ".scout-chat-attach::before,") || !strings.Contains(html, ".scout-chat-send::before {") {
		t.Error("send/attach must carry a ::before hit extension")
	}
	if !strings.Contains(html, ".goalcard__choice { min-height: var(--hit-min); }") {
		t.Error("checkpoint choices must be >= --hit-min on mobile")
	}

	// P0-4: title clamps to 2 lines, the amber parkline wraps instead of
	// truncating, and the mono meta is NEVER hidden (telemetry-loss guard).
	for _, want := range []string{
		"-webkit-line-clamp: 2",
		".goalcard__parkline { flex-wrap: wrap; }",
		".goalcard__parkline-label { white-space: normal; flex: 1 1 100%; }",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("mobile goalcard rule missing %q", want)
		}
	}
	// the mobile relocation must not be a bare hide (the state-scoped
	// cancelled/complete hides are legitimate and pre-existing; guard only
	// against a NEW unqualified mobile hide via the relocation markers).
	if !strings.Contains(html, ".goalcard__meta { order: 99; flex-basis: 100%;") {
		t.Error("the mobile goalcard must RELOCATE .goalcard__meta (order/flex-basis), never hide it — telemetry-loss guard")
	}
}
