package main

// frontend_feed_design_test.go — the AAA feed redesign pins (§0-§7): the run
// log container, the one-measure feed column, the goalcard hero (headline
// title, hairline trust footer, ink primary door), the ember sweep (ember =
// "machine working NOW", nothing else), and the §7 artifact stage that opens
// deliverables IN the chat instead of detouring to Intelligence.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForFeedDesign(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// One measure: the --feed-measure token exists and the machine cards center
// on it (runlog + goalcard), while the run log CSS block is present.
func TestIndexFeedMeasureAndRunlogStyles(t *testing.T) {
	html := readIndexForFeedDesign(t)
	for _, want := range []string{
		"--feed-measure: 680px;",
		".runlog {",
		".runlog__list::before {",
		".runlog__entry[data-live=\"1\"] .runlog__node {",
		"animation: goalcard-dot-breathe var(--pulse-cycle) var(--ease) infinite;",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing feed-measure/runlog style %q", want)
		}
	}
	// both centered machine surfaces ride the shared measure
	if strings.Count(html, "width: min(var(--feed-measure), 100%);") < 2 {
		t.Error("runlog and goalcard must both center on width: min(var(--feed-measure), 100%)")
	}
}

// §7 the artifact stage: chat-context opens route to openArtifactStage; the
// panel closes on Esc/scrim/✕ with focus return, respects reduced motion,
// reuses the sandboxed deck viewer, and keeps "open in intelligence" as the
// data-room escape hatch.
func TestIndexArtifactStageContract(t *testing.T) {
	html := readIndexForFeedDesign(t)
	for _, want := range []string{
		".artifact-stage {",
		".artifact-stage__panel {",
		".artifact-stage__scrim {",
		"@media (prefers-reduced-motion: reduce) {\n        .artifact-stage__panel { animation: none; }\n      }",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing artifact stage style %q", want)
		}
	}
	body := functionBody(html, "async function openArtifactStage(artifactId)")
	if body == "" {
		t.Fatal("could not extract openArtifactStage body")
	}
	for _, want := range []string{
		// dispatch mirrors the read pane: sandboxed deck iframe, injection-safe
		// renderer, newest-pdf embed for text-less pdf payloads
		"artifactIsHTMLDeck(entry)",
		"renderArtifactDeck(body, entry",
		"renderArtifactRead(read, entry)",
		"embed.type = 'application/pdf'",
		"artifactBlobUrl(newest)",
		// the escape hatch to the data room
		"'open in intelligence'",
		"openAgentArtifact(entry)",
		// Esc + focus trap
		"if (event.key === 'Escape')",
		"closeArtifactStage()",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("openArtifactStage body missing %q", want)
		}
	}
	closeBody := functionBody(html, "function closeArtifactStage()")
	if !strings.Contains(closeBody, "back.focus()") {
		t.Error("closeArtifactStage must return focus to the opener")
	}
}

// §4 the goalcard hero: headline title, the ink primary door ("open the
// deliverable" → the stage, not Intelligence), and the trust line rebuilt as
// a hairline footer with a numeric score span and a warn ASSUMED flag.
func TestIndexGoalcardHeroContract(t *testing.T) {
	html := readIndexForFeedDesign(t)
	terminal := functionBody(html, "function goalCardRenderTerminal(card, artifact, plan, state, prevState)")
	if terminal == "" {
		t.Fatal("could not extract goalCardRenderTerminal body")
	}
	for _, want := range []string{
		"'goalcard__link goalcard__link--primary', 'open the deliverable'",
		"openArtifactStage(artifact.id)",
		"goalcard__trust-score",
		"goalcard__trust-flag",
	} {
		if !strings.Contains(terminal, want) {
			t.Errorf("goalCardRenderTerminal missing hero marker %q", want)
		}
	}
	for _, want := range []string{
		".goalcard__link--primary {",
		".goalcard__trust-score {",
		".goalcard__trust-flag { color: var(--warn); }",
		"border-top: 1px solid var(--line-1);",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing goalcard hero style %q", want)
		}
	}
}

// §6 the ember sweep: static feed chrome dropped --agent — review doors and
// stage links read ink; ember survives only on working-now signals.
func TestIndexEmberSweepStaticConsumers(t *testing.T) {
	html := readIndexForFeedDesign(t)
	if strings.Contains(html, ".goalcard__link--accent { border-color: var(--agent); color: var(--agent); }") {
		t.Error("goalcard__link--accent must not wear the ember — links are ink, ember means working NOW")
	}
	if !strings.Contains(html, ".goalcard__link--accent { border-color: var(--line-2); color: var(--text-1); }") {
		t.Error("goalcard__link--accent must re-ink to the default ink outline")
	}
	// the park line keeps its one earned ember dot
	if !strings.Contains(html, ".scout-chat-note--park::before {") {
		t.Error("missing the park-line ember dot")
	}
}
