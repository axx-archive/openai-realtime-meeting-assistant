package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Pins the six measured 2026-08-27 UX corrections after their integration
// into the larger four-destination simplification diff.
func TestMeasuredUXAuditFixesSurviveRadicalSimplification(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		"--warn-text: #6A4100;",
		"--danger-text: #970800;",
		"--info-text: #004992;",
		"--live-text: #135523;",
		"--success-text: #135523;",
		"--warn-text: var(--warn);",
		"--danger-text: var(--danger);",
		".goalcard__trust-flag { color: var(--warn-text); }",
		"grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));",
		"min-height: clamp(220px, 40vh, 420px);",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("integrated UX correction missing %q", want)
		}
	}

	stack := functionBodyAfterSignature(html, "function renderActiveScoutThread(options = {})")
	if stack == "" {
		t.Fatal("surviving Conversations renderer is missing")
	}
	for selector, property := range map[string]string{
		`.scout-chat-msg__stack`:     "position: relative;",
		`.memory-tool__search-input`: "align-self: stretch;",
		`.studio-project-row`:        "gap: 8px;",
	} {
		start := strings.Index(html, selector+" {")
		if start < 0 {
			t.Errorf("selector %s is missing", selector)
			continue
		}
		body := html[start:]
		if end := strings.Index(body, "}"); end >= 0 {
			body = body[:end+1]
		}
		if !strings.Contains(body, property) {
			t.Errorf("selector %s missing measured correction %q", selector, property)
		}
	}

	// Raw state hues remain text only on the deliberately dark media surfaces
	// documented in the CSS (muted-mic badge, translucent media badge, and —
	// 2026-09-02 — the video-tile quality badge). All light-surface text uses
	// *-text.
	rawStateColor := regexp.MustCompile(`(?m)(^|[^-])color: var\(--(?:warn|danger|info|live|success)\);`)
	if got := len(rawStateColor.FindAllString(html, -1)); got != 3 {
		t.Fatalf("raw semantic text colors=%d, want the three documented dark-media exceptions", got)
	}
}
