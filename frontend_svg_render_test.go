package main

// SVG rendering frontend contract test — verifies that both fenced (```svg)
// and raw (<svg>) SVG content renders as a sanitized image, not as a link chip
// or raw text. The sanitizer must strip on* handlers and javascript:/data: hrefs.

import (
	"os"
	"strings"
	"testing"
)

func readIndexForSVGRender(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// The SVG render path must exist for both fenced code blocks and raw SVG.
// Both paths must use the shared sanitizer and produce .chat-rich__svg output.
func TestIndexSVGRenderPaths(t *testing.T) {
	html := readIndexForSVGRender(t)
	for _, want := range []string{
		// Shared sanitizer function
		"function renderSanitizedSVG(container, svgContent)",
		"chat-rich__image",
		"chat-rich__svg",
		// Fenced code block SVG detection (```svg or ```)
		`<svg[\s>]/i.test(codeContent)`,
		"renderSanitizedSVG(container, codeContent)",
		// Raw SVG detection (without fences)
		`<svg[\s>]/i.test(trimmed)`,
		"renderSanitizedSVG(container, svgContent)",
		// Must accumulate until closing tag
		`</svg>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing SVG render path hook: %q", want)
		}
	}
}

// The sanitizer must strip ALL on* event handlers and javascript:/data: hrefs.
func TestIndexSVGSanitizerStripsAllEventHandlers(t *testing.T) {
	html := readIndexForSVGRender(t)
	for _, want := range []string{
		// Strips all on* event handlers (not just onclick/onload/onerror)
		`name.startsWith('on')`,
		`el.removeAttribute(attr.name)`,
		// Strips javascript: and data: from href attributes
		`value.startsWith('javascript:')`,
		`value.startsWith('data:')`,
		// Strips dangerous elements
		`script, foreignObject`,
		`use[href^="data:"]`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html SVG sanitizer missing security hook: %q", want)
		}
	}
}

// The CSS must style the rendered SVG container with minimum dimensions.
func TestIndexSVGRenderStyles(t *testing.T) {
	html := readIndexForSVGRender(t)
	for _, want := range []string{
		".chat-rich__svg {",
		".chat-rich__svg svg {",
		"max-width: 100%",
		"max-height: 320px",
		// Must have minimum dimensions so SVG isn't a tiny dot
		"min-width: 64px",
		"min-height: 64px",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing SVG render style: %q", want)
		}
	}
}

// Parse errors fall back to code block, never silently drop content.
func TestIndexSVGParseErrorFallback(t *testing.T) {
	html := readIndexForSVGRender(t)
	for _, want := range []string{
		"parsererror",
		"chat-rich__code",
		"codeEl.textContent = svgContent",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing SVG parse-error fallback: %q", want)
		}
	}
}

// Namespace URLs (w3.org/2000/svg) must NOT be linked as clickable URLs.
// The xmlns attribute should appear as plain text, not a "source chip".
func TestIndexSVGNamespaceURLNotLinked(t *testing.T) {
	html := readIndexForSVGRender(t)
	for _, want := range []string{
		// The URL filter for namespace URLs
		`www.w3.org`,
		`2000/svg`,
		`1999/xlink`,
		// Must skip linking and output as plain text
		`target.appendChild(document.createTextNode(raw))`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing namespace URL filter: %q", want)
		}
	}
}

// SVG dimensions must be set from viewBox when width/height are missing.
func TestIndexSVGDimensionsFromViewBox(t *testing.T) {
	html := readIndexForSVGRender(t)
	for _, want := range []string{
		// Check for viewBox parsing and dimension setting
		`getAttribute('viewBox')`,
		`hasAttribute('width')`,
		`hasAttribute('height')`,
		`setAttribute('width'`,
		`setAttribute('height'`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing viewBox dimension logic: %q", want)
		}
	}
}

// Thread list preview must strip raw SVG content, showing "[image]" instead.
func TestIndexSVGPreviewStripped(t *testing.T) {
	html := readIndexForSVGRender(t)
	for _, want := range []string{
		// SVG stripping in preview - regex pattern with \s\S
		`<svg[\s\S]*?<\/svg>`,
		`[image]`,
		"chatPreviewPlainText",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing SVG preview stripping: %q", want)
		}
	}
}
