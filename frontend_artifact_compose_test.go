package main

// P1-2 — document composition (docs/plans/live-platform-design-spec.md). The
// artifact read pane must not (a) print the hero <p> summary when it was
// borrowed from a grid section that also renders below (verbatim duplicate),
// nor (b) render a leading "# Title" heading-only section (title echo) or an
// empty-body section as a "no detail yet." tile beside a complete artifact.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForArtifactCompose(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func TestArtifactComposeHeroAndSectionGuards(t *testing.T) {
	html := readIndexForArtifactCompose(t)

	// renderArtifactRead carries an `options = {}` default param, so the shared
	// functionBody first-brace heuristic mis-scopes it — use the signature-aware
	// extractor.
	body := functionBodyAfterSignature(html, "function renderArtifactRead(container, entry, options = {})")
	if body == "" {
		t.Fatal("could not extract renderArtifactRead body")
	}

	// (a) Hero <p> is guarded on a real-abstract condition — only the leading
	// objective blurb prints; a section-borrowed summary is dropped so it does
	// not double-print the identical card one swipe below.
	if !strings.Contains(body, "const heroIsAbstract = !summarySection") {
		t.Error("renderArtifactRead must derive heroIsAbstract from a real-abstract condition (!summarySection && objective)")
	}
	if !strings.Contains(body, "if (heroIsAbstract) {") {
		t.Error("renderArtifactRead must gate the hero <p> summary on heroIsAbstract")
	}

	// (b) Empty/title-echo sections are skipped: an "any non-empty section"
	// guard plus a heading-equals-title drop for the leading "# Title" echo.
	if !strings.Contains(body, "const anySectionBody = rendered.some(") {
		t.Error("renderArtifactRead must compute an 'any non-empty section' guard before skipping empty sections")
	}
	if !strings.Contains(body, "if (!body && anySectionBody) {") {
		t.Error("renderArtifactRead must skip an empty-body section when real content lives elsewhere (no 'no detail yet.' tile on a complete artifact)")
	}
	if !strings.Contains(body, "titleEchoes.has(normalizedSectionName(heading))") {
		t.Error("renderArtifactRead must drop a leading heading-only section whose heading equals the doc title (title-echo guard)")
	}
	// the title set is sourced from the stage/display title so the echo match
	// is case/format-insensitive.
	if !strings.Contains(body, "[artifactStageTitle(entry), artifactDisplayTitle(entry)]") {
		t.Error("renderArtifactRead must build the title-echo set from artifactStageTitle/artifactDisplayTitle")
	}
}
