package main

import (
	"os"
	"strings"
	"testing"
)

// Wave 1 — mobile composer polish (docs/superpowers/specs/
// 2026-07-06-bonfire-topline-design.md). The founder flagged two defects on
// mobile: a redundant "white border" ring around the focused input, and the
// attach/tools/send icons sitting off-center in the composer bar. Both are
// grep-pinned against index.html the same way the rest of the frontend
// contracts are.

func readIndexForComposerPolish(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(data)
}

// cssRuleBody returns the declaration block for the first TOP-LEVEL occurrence
// of `selector { … }` (non-nested; composer rules are flat). "Top-level" means
// the selector starts its own line — so a compound descendant rule
// ("… .scout-chat-form {") or a pseudo variant is not mistaken for the base rule.
func cssRuleBody(html, selector string) string {
	needle := selector + " {"
	from := 0
	for {
		i := strings.Index(html[from:], needle)
		if i < 0 {
			return ""
		}
		abs := from + i
		lineStart := strings.LastIndexByte(html[:abs], '\n')
		if strings.TrimSpace(html[lineStart+1:abs]) == "" {
			rest := html[abs:]
			end := strings.IndexByte(rest, '}')
			if end < 0 {
				return ""
			}
			return rest[:end]
		}
		from = abs + len(needle)
	}
}

// The composer pill is the single focus surface (.scout-chat-form
// :focus-within). The input must NOT paint its own :focus-visible ring, or a
// redundant hairline box renders inside the pill (the founder's "white
// border").
func TestComposerInputHasNoRedundantFocusRing(t *testing.T) {
	html := readIndexForComposerPolish(t)

	rule := cssRuleBody(html, ".scout-chat-input:focus-visible")
	if rule == "" {
		t.Fatal(".scout-chat-input:focus-visible rule missing — the input will draw the global :focus-visible ring")
	}
	if !strings.Contains(rule, "box-shadow: none") {
		t.Error(".scout-chat-input:focus-visible must set box-shadow: none — --glow-accent otherwise paints a 1.5px ring on the textarea")
	}
}

// The attach (📎), tools (+) and send (↑) controls must center vertically in
// the bar, not bottom-pin against a taller input (flex-end) — which is what
// made them look off-center as the field grew.
func TestComposerControlsCenterInBar(t *testing.T) {
	html := readIndexForComposerPolish(t)

	rule := cssRuleBody(html, ".scout-chat-form")
	if rule == "" {
		t.Fatal(".scout-chat-form rule missing")
	}
	if strings.Contains(rule, "align-items: flex-end") {
		t.Error(".scout-chat-form still uses align-items: flex-end — the composer controls bottom-pin and read off-center")
	}
	if !strings.Contains(rule, "align-items: center") {
		t.Error(".scout-chat-form must use align-items: center so 📎/+/↑ center in the bar")
	}
}
