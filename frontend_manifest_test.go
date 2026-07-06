package main

// The package manifest card's frontend contract (redesign sheet s05 + §2c).
// Grep-style pins in the frontend_router_test.go grammar: a persisted
// Kind=manifest message renders as the handover card (badge rows, pill
// actions, amber skip bullets, mono footer), the held variant quiets to
// open-only with share dark, the pdf action is a direct blob download with
// the ref validated first, present routes through the stage's sandboxed deck
// presenter, and "open the deliverable" on a goalcard focuses the mounted
// manifest before falling back to the artifact stage.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForManifest(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func TestIndexManifestCardComponentContract(t *testing.T) {
	html := readIndexForManifest(t)
	for _, want := range []string{
		// render branch: a manifest message becomes the card on live send and
		// reload alike (the proposal/choices dispatch law)
		"=== 'manifest' && message.manifest",
		"function scoutManifestCardNode(message)",
		// the card's anatomy: kicker + provenance, badge rows, pill actions,
		// skip bullets, footer — and the one clearly-marked CSS block
		"manifest-card__kicker",
		"manifest-card__provenance",
		"manifest-card__badge",
		"manifest-card__pill manifest-card__pill--primary",
		"manifest-card__pill--outline",
		"manifest-card__skip-dot",
		"manifest-card__foot-link",
		".manifest-card {",
		"Package manifest card (sheet s05",
		// the green/amber status voice: shipped, shipped with skips, held
		"manifest-card__status--shipped",
		"manifest-card__status--skips",
		// provenance grammar: gate score to one decimal, assumed, decisions
		"gate ${gate.toFixed(1)}",
		"assumed",
		// the footer doors: share mints via the EXISTING machinery; the
		// findings record opens in the stage
		"function manifestMintShareLink",
		"see how it was attacked",
		"manifest.findingsArtifactId",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing manifest card hook %q", want)
		}
	}
}

// The pdf action is a DIRECT download off the session-gated blob route: the
// ref is validated to the 64-hex sha256 shape before any url is built, the
// href comes from the shared artifactBlobUrl helper, and the anchor downloads.
func TestIndexManifestPdfDownloadShape(t *testing.T) {
	html := readIndexForManifest(t)
	for _, want := range []string{
		"/^[0-9a-f]{64}$/.test(String(deliverable.pdfRef || ''))",
		"artifactBlobUrl({ ref: String(deliverable.pdfRef), name: String(deliverable.pdfName || '') })",
		"pdf.setAttribute('download', String(deliverable.pdfName || ''))",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing manifest pdf download hook %q", want)
		}
	}
}

// Held quieting (sheet §2c): the muted card variant exists, present/pdf are
// gated off held, and the share door only renders un-held with an eligible
// artifact on the record — share links stay dark under a hold.
func TestIndexManifestHeldQuieting(t *testing.T) {
	html := readIndexForManifest(t)
	for _, want := range []string{
		"manifest-card--held",
		".manifest-card--held {",
		"opacity: 0.82;",
		"if (!held && deliverable.present)",
		"if (!held && /^[0-9a-f]{64}$/",
		"if (!held && manifest.shareArtifactId)",
		"release requires ${manifestActorShortName(manifest.heldBy || 'admin')}",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing manifest held-quieting hook %q", want)
		}
	}
}

// Present routes through the artifact stage's sandboxed deck presenter: the
// manifest pill opens the stage with { present: true }, openArtifactStage
// threads it as autoPresent, and renderArtifactDeck fullscreens the SAME
// sandboxed frame (rejection tolerated, never an error).
func TestIndexManifestPresentRouting(t *testing.T) {
	html := readIndexForManifest(t)
	for _, want := range []string{
		"function openArtifactStage(artifactId, fallbackTitle, options)",
		"{ present: true }",
		"autoPresent: Boolean(options?.present)",
		"if (options.autoPresent) frame.requestFullscreen?.()?.catch?.(() => {})",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing manifest present-routing hook %q", want)
		}
	}
}

// "open the deliverable" on a complete goalcard focuses the thread's mounted
// manifest card (scroll + one flash) and only falls back to the artifact
// stage when no manifest is mounted.
func TestIndexManifestFocusRouting(t *testing.T) {
	html := readIndexForManifest(t)
	for _, want := range []string{
		"function focusManifestCardForGoal(goalId)",
		"if (focusManifestCardForGoal(artifact.id)) return",
		"CSS.escape(id)",
		"card.classList.add('is-flashed')",
		".manifest-card.is-flashed",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing manifest focus-routing hook %q", want)
		}
	}
	// the fallback survives: the same click handler still opens the stage
	start := strings.Index(html, "'open the deliverable'")
	if start < 0 {
		t.Fatal("cannot find the open-the-deliverable button")
	}
	scope := html[start:]
	if end := strings.Index(scope, "actions.appendChild(open)"); end > 0 {
		scope = scope[:end]
	}
	if !strings.Contains(scope, "openArtifactStage(artifact.id") {
		t.Fatal("open-the-deliverable lost its artifact-stage fallback")
	}
}

// The manifest card only OPENS things — stage, download, share mint. No
// launch door of any kind lives in the component (the choices-pill law).
func TestIndexManifestCardNeverLaunches(t *testing.T) {
	html := readIndexForManifest(t)
	start := strings.Index(html, "function scoutManifestCardNode(message)")
	if start < 0 {
		t.Fatal("cannot find the manifest component")
	}
	// the CSS block carries the same end marker — scope to the one AFTER the
	// function, which closes the JS component
	end := strings.Index(html[start:], "end package manifest card")
	if end < 0 {
		t.Fatal("cannot scope the manifest component body")
	}
	body := html[start : start+end]
	for _, banned := range []string{"runGoalPipeline", "/assistant/goal", "startAgentThread", "submitApproval"} {
		if strings.Contains(body, banned) {
			t.Fatalf("the manifest card contains launch/approval hook %q — it may only open things", banned)
		}
	}
}
