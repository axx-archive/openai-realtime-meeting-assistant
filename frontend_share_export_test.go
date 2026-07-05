package main

// Frontend contract pins for the Wave-3 share/export UI (packaging OS §4
// items 14 + 14b wiring): the Export PDF button on html_deck artifacts (probe
// + disabled tooltip when the render sidecar is absent), the pdf-asset viewer
// (browser-native <object> + /artifacts/blob download links, DOM-built), and
// the Share panel (mint with expiry choice, list, revoke, copy — mirroring
// but never replacing the server's approved/final gate).

import (
	"os"
	"strings"
	"testing"
)

func readIndexForShareExport(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// The three new detail-pane elements exist in the markup, born disabled (and
// Export PDF hidden — it only shows on html_deck artifacts).
func TestIndexShareExportElementsExist(t *testing.T) {
	html := readIndexForShareExport(t)
	for _, want := range []string{
		`id="artifactExportPdfButton"`,
		`id="artifactShareButton"`,
		`id="artifactSharePanel"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing %q", want)
		}
	}
}

// The capability probe reads the render-runner heartbeat the server exposes
// on /readyz (checks.agents.renderRunner.heartbeatOK) — sidecar absence must
// come from the server's own snapshot, never a client guess.
func TestIndexRenderSidecarProbeContract(t *testing.T) {
	html := readIndexForShareExport(t)
	body := functionBody(html, "function probeRenderSidecar()")
	if body == "" {
		t.Fatal("could not extract probeRenderSidecar body")
	}
	for _, want := range []string{
		"'/readyz'",
		"renderRunner",
		"heartbeatOK === true",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("probeRenderSidecar body missing %q", want)
		}
	}
}

// Export PDF: shown only for html_deck artifacts (the same client sniff the
// deck viewer uses), disabled with the explanatory tooltip while the sidecar
// heartbeat is absent, and the trigger POSTs to /artifacts/export-pdf.
func TestIndexExportPdfButtonContract(t *testing.T) {
	html := readIndexForShareExport(t)
	detail := functionBody(html, "function renderArtifactDetail()")
	if detail == "" {
		t.Fatal("could not extract renderArtifactDetail body")
	}
	for _, want := range []string{
		"artifactIsHTMLDeck(artifact)",
		"probeRenderSidecar()",
		"!deckArtifact || !renderSidecarReady",
		"render sidecar not available — PDF export is disabled",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("renderArtifactDetail body missing export-pdf marker %q", want)
		}
	}

	trigger := functionBody(html, "function exportSelectedArtifactPdf()")
	if trigger == "" {
		t.Fatal("could not extract exportSelectedArtifactPdf body")
	}
	for _, want := range []string{
		"'/artifacts/export-pdf'",
		"method: 'POST'",
		"kind: 'deck'",
	} {
		if !strings.Contains(trigger, want) {
			t.Errorf("exportSelectedArtifactPdf body missing %q", want)
		}
	}
}

// pdf assets render through the browser-native <object type="application/pdf">
// viewer plus a download link against the session-gated /artifacts/blob route.
// Asset refs are validated to the 64-hex sha256 shape before any URL is built,
// and the section is DOM-built — innerHTML never touches asset data.
func TestIndexArtifactAssetsViewerContract(t *testing.T) {
	html := readIndexForShareExport(t)

	entries := functionBody(html, "function artifactAssetEntries(entry)")
	if entries == "" {
		t.Fatal("could not extract artifactAssetEntries body")
	}
	if !strings.Contains(entries, "/^[0-9a-f]{64}$/") {
		t.Error("artifactAssetEntries must validate refs to the 64-hex sha256 shape")
	}

	blobURL := functionBody(html, "function artifactBlobUrl(asset)")
	if blobURL == "" {
		t.Fatal("could not extract artifactBlobUrl body")
	}
	if !strings.Contains(blobURL, "/artifacts/blob?ref=${encodeURIComponent(asset.ref)}") {
		t.Error("artifactBlobUrl must target the session-gated /artifacts/blob route with an encoded ref")
	}

	assets := functionBody(html, "function renderArtifactAssets(container, entry)")
	if assets == "" {
		t.Fatal("could not extract renderArtifactAssets body")
	}
	for _, want := range []string{
		"createElement('object')",
		"'application/pdf'",
		"setAttribute('download'",
	} {
		if !strings.Contains(assets, want) {
			t.Errorf("renderArtifactAssets body missing %q", want)
		}
	}
	if strings.Contains(assets, "innerHTML") {
		t.Error("renderArtifactAssets must stay DOM-built — no innerHTML")
	}

	// Both render paths list assets: the safe renderer AND the deck branch
	// (a deck with an exported pdf shows the asset under the iframe).
	read := functionBodyAfterSignature(html, "function renderArtifactRead(container, entry, options = {})")
	if read == "" {
		t.Fatal("could not extract renderArtifactRead body")
	}
	if strings.Count(read, "renderArtifactAssets(container, entry)") < 2 {
		t.Error("renderArtifactRead must list assets on both the deck branch and the safe path")
	}
}

// The Share affordance mirrors the SERVER's approved/final status gate (the
// route is the enforcement point), and the panel speaks the three verbs on
// /artifacts/share: GET list scoped by artifactId, POST mint with the expiry
// choice, DELETE revoke by id — with the copied URL absolutized against
// location.origin.
func TestIndexShareLinkPanelContract(t *testing.T) {
	html := readIndexForShareExport(t)

	gate := functionBody(html, "function artifactShareable(entry)")
	if gate == "" {
		t.Fatal("could not extract artifactShareable body")
	}
	for _, want := range []string{"'approved'", "humanApprovedAt", "'complete'", "'published'"} {
		if !strings.Contains(gate, want) {
			t.Errorf("artifactShareable body missing %q", want)
		}
	}
	// The untracked "final" alias must NOT be honored: nothing produces it,
	// and a future gate-passed-but-unapproved value must not bypass approval.
	if strings.Contains(gate, "'final'") {
		t.Error("artifactShareable must not honor the untracked 'final' status alias")
	}

	detail := functionBody(html, "function renderArtifactDetail()")
	if !strings.Contains(detail, "artifactShareable(artifact)") {
		t.Error("renderArtifactDetail must gate the Share button through artifactShareable")
	}
	if !strings.Contains(detail, "share links need an approved artifact") {
		t.Error("renderArtifactDetail must explain the disabled Share button")
	}

	panel := functionBody(html, "function renderArtifactSharePanel(artifact)")
	if panel == "" {
		t.Fatal("could not extract renderArtifactSharePanel body")
	}
	for _, want := range []string{
		"/artifacts/share?artifactId=${encodeURIComponent(artifact.id)}",
		"'/artifacts/share'",
		"method: 'POST'",
		"method: 'DELETE'",
		"expiresDays",
	} {
		if !strings.Contains(panel, want) {
			t.Errorf("renderArtifactSharePanel body missing %q", want)
		}
	}

	absolute := functionBody(html, "function shareLinkAbsoluteUrl(link)")
	if absolute == "" {
		t.Fatal("could not extract shareLinkAbsoluteUrl body")
	}
	if !strings.Contains(absolute, "window.location.origin") {
		t.Error("shareLinkAbsoluteUrl must absolutize against window.location.origin")
	}
}
