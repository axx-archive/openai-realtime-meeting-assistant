package main

// The sandboxed HTML deck viewer's frontend contract (packaging OS §4 item 1,
// Wave 2 item 9). These grep-style pins hold the security model: a deck
// renders through the tokened /artifacts/render route inside
// <iframe sandbox="allow-scripts"> — NEVER srcdoc, NEVER allow-same-origin —
// while every non-HTML artifact stays on the injection-safe escaped renderer.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForDeckViewer(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// functionBodyAfterSignature scopes a function body when the signature itself
// contains braces (e.g. an `options = {}` default parameter), which defeats
// the shared functionBody helper's first-brace heuristic. The signature must
// be the COMPLETE `function name(...)` text; brace counting starts after it.
func functionBodyAfterSignature(source string, signature string) string {
	start := strings.Index(source, signature)
	if start == -1 {
		return ""
	}
	rest := source[start+len(signature):]
	open := strings.Index(rest, "{")
	if open == -1 {
		return ""
	}
	depth := 0
	for index := open; index < len(rest); index++ {
		switch rest[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return rest[open : index+1]
			}
		}
	}
	return ""
}

// The client sniff mirrors the server's artifactIsHTMLDocument
// (artifact_render.go): declared metadata type=html_deck, or a body that
// starts as an HTML document — never a mid-body <html> mention.
func TestIndexDeckSniffMirrorsServer(t *testing.T) {
	html := readIndexForDeckViewer(t)
	body := functionBody(html, "function artifactIsHTMLDeck(entry)")
	if body == "" {
		t.Fatal("could not extract artifactIsHTMLDeck body")
	}
	for _, want := range []string{
		"'html_deck'",
		".trim().toLowerCase()",
		"startsWith('<!doctype html')",
		"startsWith('<html')",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("artifactIsHTMLDeck body missing %q", want)
		}
	}
}

// The deck branch lives inside renderArtifactRead's real body, so every
// surface that reads an artifact (detail pane, design output, agent modal)
// gets the viewer — and the escaped renderer stays the fallback (forceSafe).
func TestIndexDeckBranchInsideSafeRenderer(t *testing.T) {
	html := readIndexForDeckViewer(t)
	body := functionBodyAfterSignature(html, "function renderArtifactRead(container, entry, options = {})")
	if body == "" {
		t.Fatal("could not extract renderArtifactRead body")
	}
	for _, want := range []string{
		"!options.forceSafe && artifactIsHTMLDeck(entry)",
		"renderArtifactDeck(container, entry, options)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("renderArtifactRead body missing deck branch marker %q", want)
		}
	}
}

// The viewer itself: mint via the session-gated /artifacts/render-token
// endpoint, point the sandboxed iframe at the returned tokened URL, and the
// Present button fullscreens the SAME sandboxed iframe — never a top-level
// navigation to the render URL, which would run the deck outside the iframe
// sandbox. A failed mint falls back to the escaped renderer — never srcdoc.
func TestIndexDeckViewerSecurityContract(t *testing.T) {
	html := readIndexForDeckViewer(t)
	body := functionBodyAfterSignature(html, "function renderArtifactDeck(container, entry, options = {})")
	if body == "" {
		t.Fatal("could not extract renderArtifactDeck body")
	}
	for _, want := range []string{
		`setAttribute('sandbox', 'allow-scripts')`,
		"/artifacts/render-token?id=${encodeURIComponent(artifactId)}",
		"frame.src = payload.url",
		"frame.requestFullscreen?.()",
		"renderArtifactRead(container, entry, { ...options, forceSafe: true })",
		// stale-mint guard: the pane may have moved on while the token minted
		"if (!deck.isConnected) return",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("renderArtifactDeck body missing %q", want)
		}
	}
	// Present must never open the render URL as a top-level document — the
	// old window.open path escaped the iframe sandbox (client-side; the
	// server CSP sandbox directive is the backstop).
	if strings.Contains(body, "window.open") {
		t.Error("renderArtifactDeck must not window.open the render URL — Present fullscreens the sandboxed iframe")
	}

	// The banned mechanisms must not exist ANYWHERE in the monolith: srcdoc
	// would run the deck same-origin with the OS, and allow-same-origin would
	// hand a sandboxed deck the render route's origin back.
	for _, banned := range []string{
		"srcdoc",
		"allow-same-origin",
	} {
		if strings.Contains(html, banned) {
			t.Errorf("index.html contains banned deck-viewer mechanism %q", banned)
		}
	}
}
