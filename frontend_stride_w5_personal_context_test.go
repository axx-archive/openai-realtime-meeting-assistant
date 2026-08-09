package main

import (
	"os"
	"strings"
	"testing"
)

func TestStrideE10W5WebPersonalContextIsPrivateDirectAndCohesive(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, marker := range []string{
		`data-settings-section="personal-context"`,
		`Private context you control`,
		`/api/mymind/v1/sources`,
		`/api/mymind/v1/export`,
		`save correction`,
		`forget permanently`,
		`source encryption key`,
		`personalContextLoadEpoch`,
		`personalContextCurrentIdentity`,
		`cache:'no-store'`,
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("personal-context Settings integration missing %q", marker)
		}
	}
	start := strings.Index(html, `/* ---------- private personal context custody ----------`)
	end := strings.Index(html[start:], `/* ---------- account settings`)
	if start < 0 || end < 0 {
		t.Fatal("personal-context runtime block missing")
	}
	block := html[start : start+end]
	for _, forbidden := range []string{"localStorage", "pendingActionOperation", "idempotency-key", "mymind context", "MyMind"} {
		if strings.Contains(block, forbidden) {
			t.Errorf("personal context crossed a forbidden client boundary: %q", forbidden)
		}
	}
	if !strings.Contains(block, `personalContextExactKeys`) || !strings.Contains(block, `new TextEncoder().encode(value.body).byteLength > 16384`) {
		t.Fatal("personal source response is not closed and body-bounded")
	}
}
