package main

// Icon system contract (STRIDE design-system v2 §3, docs/design/
// stride-design-system-v2.md). One stroked family: strideIcon(name, {size})
// draws every chrome glyph on a 24-unit grid at stroke 1.8, round caps and
// joins, currentColor and no fill. strideChatActionIcon stays as the sized
// alias its callers and tests already use. The pins are static — they read
// index.html — so they hold with no browser in the loop.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func readIndexForIconSystem(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// strideIconTableBlock returns the STRIDE_ICON_PATHS literal.
func strideIconTableBlock(t *testing.T, html string) string {
	t.Helper()
	start := strings.Index(html, "const STRIDE_ICON_PATHS = {")
	if start < 0 {
		t.Fatal("index.html must declare STRIDE_ICON_PATHS")
	}
	end := strings.Index(html[start:], "function strideIcon(")
	if end < 0 {
		t.Fatal("STRIDE_ICON_PATHS must be followed by function strideIcon")
	}
	return html[start : start+end]
}

// strideIconFunctionBlock returns the body of strideIcon(name, options).
func strideIconFunctionBlock(t *testing.T, html string) string {
	t.Helper()
	start := strings.Index(html, "function strideIcon(name")
	if start < 0 {
		t.Fatal("index.html must define function strideIcon(name, options)")
	}
	end := strings.Index(html[start:], "function strideIconMarkup(")
	if end < 0 {
		t.Fatal("strideIcon must be followed by strideIconMarkup")
	}
	return html[start : start+end]
}

// The general icon factory exists and the chat-action alias still resolves
// through it (callers and the desktop-chat tests keep working unchanged).
func TestIconSystemStrideIconAndAlias(t *testing.T) {
	html := readIndexForIconSystem(t)
	for _, want := range []string{
		"function strideIcon(name, options = {})",
		"function strideIconMarkup(name, options = {})",
		"function strideChatActionIcon(name)",
		"return strideIcon(name, { size: 16, className: 'chat-action-mark' })",
		"strideChatActionIcon('react')",
		"strideChatActionIcon('reply')",
		"scoutFlameMark({ className: 'chat-action-mark' })",
		"strideChatActionIcon('more')",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing icon-system hook: %q", want)
		}
	}
}

// Every glyph the app's chrome needs has a name in the table.
func TestIconSystemNamesResolve(t *testing.T) {
	html := readIndexForIconSystem(t)
	table := strideIconTableBlock(t, html)
	names := []string{
		// rail
		"home", "rooms", "conversations", "work", "drive",
		// topbar
		"bell", "theme", "moon", "sun", "settings",
		// composer
		"attach", "files", "mic", "send", "gif",
		// chat
		"react", "riff", "reply", "more", "people", "bell-slash", "search", "hash", "sparkle",
		// rooms
		"cam", "share", "leave", "chat", "hand", "reactions",
		// files
		"folder", "upload", "star", "trash", "share-out", "link", "download",
		// editors
		"bold", "italic", "list", "table", "image", "undo", "redo", "present",
		// the chevron / close / check / plus set
		"chevron-down", "chevron-up", "chevron-left", "chevron-right", "close", "check", "plus",
	}
	for _, name := range names {
		key := name + ": ["
		if strings.Contains(name, "-") {
			key = "'" + name + "': ["
		}
		if !strings.Contains(table, key) {
			t.Errorf("STRIDE_ICON_PATHS missing glyph %q", name)
		}
	}
	// Circles are written as arcs so the table is plain path data.
	if strings.Contains(table, "<circle") || strings.Contains(table, "circle:") {
		t.Error("STRIDE_ICON_PATHS must hold path data only")
	}
}

// The family rules are attributes on the element, not a CSS dependency:
// 24 grid, 1.8 stroke, round caps and joins, currentColor, no fill.
func TestIconSystemFamilyAttributes(t *testing.T) {
	html := readIndexForIconSystem(t)
	body := strideIconFunctionBlock(t, html)
	for _, want := range []string{
		"svg.setAttribute('viewBox', '0 0 24 24')",
		"svg.setAttribute('fill', 'none')",
		"svg.setAttribute('stroke', 'currentColor')",
		"svg.setAttribute('stroke-width', '1.8')",
		"svg.setAttribute('stroke-linecap', 'round')",
		"svg.setAttribute('stroke-linejoin', 'round')",
		"svg.setAttribute('aria-hidden', 'true')",
		"svg.setAttribute('data-icon', String(name || ''))",
		"classes.push('stride-mark--micro')",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("strideIcon missing family rule: %q", want)
		}
	}
	// The CSS side of the family: .stride-mark carries the same recipe and
	// the micro step reads at stroke 2.
	for _, want := range []string{
		".stride-mark {",
		".stride-mark--micro {",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing icon-system CSS: %q", want)
		}
	}
}

var iconSystemSVGOpenTag = regexp.MustCompile(`<svg\b[^>]*>`)

func iconSystemRegion(t *testing.T, html, start, end string) string {
	t.Helper()
	from := strings.Index(html, start)
	if from < 0 {
		t.Fatalf("index.html missing region start %q", start)
	}
	to := strings.Index(html[from:], end)
	if to < 0 {
		t.Fatalf("index.html missing region end %q after %q", end, start)
	}
	return html[from : from+to]
}

// Rail, topbar and composer markup carry no hard-coded fill colours, and
// every 24-grid stroke glyph in them is drawn at the family width.
func TestIconSystemChromeMarkupConforms(t *testing.T) {
	html := readIndexForIconSystem(t)
	regions := []struct{ name, start, end string }{
		{"rail", `<aside id="toolRail"`, `</aside>`},
		{"topbar", `<header class="topbar mount-stagger">`, `</header>`},
		{"home composer", `<form id="homeScoutComposer"`, `</form>`},
		{"chat composer", `<form id="scoutChatForm"`, `</form>`},
		{"context reply composer", `<div class="chat-context-reply__composer">`, `</form>`},
		{"room chat composer", `<form id="roomChatForm"`, `</form>`},
	}
	for _, region := range regions {
		markup := iconSystemRegion(t, html, region.start, region.end)
		tags := iconSystemSVGOpenTag.FindAllString(markup, -1)
		if len(tags) == 0 {
			t.Errorf("%s: expected at least one inline <svg>", region.name)
		}
		for _, tag := range tags {
			if strings.Contains(tag, `fill="#`) {
				t.Errorf("%s: hard-coded fill colour on chrome icon: %s", region.name, tag)
			}
			if strings.Contains(tag, `viewBox="0 0 24 24"`) && strings.Contains(tag, `stroke="currentColor"`) && !strings.Contains(tag, `stroke-width="1.8"`) {
				t.Errorf("%s: 24-grid chrome icon off the 1.8 family width: %s", region.name, tag)
			}
		}
	}
}

// The brand marks are not icons: the rail's Stride wordmark is the production
// artwork (--wordmark-image) on its own class and is never routed through
// strideIcon. (AJ ratified 2026-09-02: wordmark back, no flame, no date, no
// status by the org name — the flame SVG that stood here is gone.)
func TestIconSystemLeavesBrandMarksAlone(t *testing.T) {
	html := readIndexForIconSystem(t)
	for _, want := range []string{
		`<span id="brandMark" class="topbar__mark" aria-hidden="true"><span class="topbar__wordmark wordmark"></span></span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("brand mark drifted: %q", want)
		}
	}
}
