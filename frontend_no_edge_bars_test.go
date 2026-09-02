package main

import (
	"os"
	"strings"
	"testing"
)

// AJ 2026-09-02, on the rail's Wave 10 inset bar: "no accent mark there on the
// left side, the glow is great, but that thicker curve line is a mark of AI
// design." The rule generalises: an active / selected / current item in any
// nav or list is a tinted well (and, where sanctioned, ember text) — never a
// leading-edge bar, inset stripe or side rule. This pin names every selector
// cleaned so the pattern cannot creep back one list at a time. Dots and discs
// (process steps, presence, live timers) and genuine tab underlines are not
// bars and stay out of scope here.
func TestIndexNoActiveEdgeBars(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, gone := range []string{
		// settings section nav — the springing ember tick beside the current row
		".settings-nav__item::before {",
		".settings-nav__item[aria-current=\"true\"]::before {",
		".settings-nav__item::after {",
		// drive nav — the inset accent stripe on the current destination
		".drive-nav__item.is-active {\n        background: color-mix(in srgb, var(--accent) 10%, var(--surface-3));\n        color: var(--text-1);\n        box-shadow: inset 2px 0 var(--accent);",
		".drive-nav__item.is-active::before {",
		// drive file rows — the inset accent stripe on the selected row
		".drive-file-list .files-row.is-selected { box-shadow: inset 2px 0 var(--accent); }",
		"box-shadow: inset 2px 0 var(--accent), var(--shadow-1);",
		// the rail itself (already retired, kept pinned here as the origin of the rule)
		".pd1-primary-nav__item[aria-current=\"page\"]::before {",
		".pd1-primary-nav__item[aria-current=\"page\"]::after {",
	} {
		if strings.Contains(html, gone) {
			t.Errorf("active-state edge bar must not return; found %q", gone)
		}
	}
	// no active/selected/current nav or list selector may paint an inset side
	// stripe at all — the box-shadow form is the bar written without a
	// pseudo-element
	for _, sel := range []string{
		".settings-nav__item[aria-current=\"true\"]",
		".drive-nav__item.is-active",
		".files-row.is-selected",
		".chat-thread-item[aria-pressed=\"true\"]",
		".chat-search-results .chat-thread-item.is-active",
		".memory-inspector__filter[data-active=\"true\"]",
		".studio-project-row.is-selected",
		".room-meeting-tab.is-selected",
	} {
		for idx := strings.Index(html, sel); idx >= 0; {
			rest := html[idx:]
			end := strings.Index(rest, "}")
			if end < 0 {
				break
			}
			block := rest[:end]
			for _, bar := range []string{"inset 2px 0", "inset 3px 0", "inset -2px 0", "inset -3px 0", "border-left:", "border-right:"} {
				if strings.Contains(block, bar) {
					t.Errorf("%s paints an edge bar (%q):\n%s", sel, bar, block)
				}
			}
			next := strings.Index(rest[1:], sel)
			if next < 0 {
				break
			}
			idx += 1 + next
		}
	}
	// the sanctioned active treatment stays: ember text on the 10% ember well
	for _, want := range []string{
		".settings-nav__item[aria-current=\"true\"] {\n        background: color-mix(in srgb, var(--ember) 10%, transparent);\n        color: var(--ember-text);\n        box-shadow: none;",
		".pd1-primary-nav__item[aria-current=\"page\"] {\n        color: var(--ember-text);\n        background: color-mix(in srgb, var(--ember) 10%, transparent);",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("active well treatment missing %q", want)
		}
	}
}
