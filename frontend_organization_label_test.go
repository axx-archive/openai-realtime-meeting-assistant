package main

import (
	"os"
	"strings"
	"testing"
)

// The workspace is single-organization; the shell must never label it
// "unavailable". Both the topbar switcher and the phone subtitle fall back
// to the organization name carried by /auth/me.
func TestIndexOrganizationChipNeverReadsUnavailable(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	if strings.Contains(html, "Organization unavailable") {
		t.Fatalf("index.html still carries the 'Organization unavailable' label")
	}
	for _, pin := range []string{
		"function workspaceOrganizationLabel()",
		"authedUser?.organization",
		"window.__strideCurrentOrganizationLabel || workspaceOrganizationLabel()",
		"const currentLabel = String(current?.title || workspaceOrganizationLabel())",
	} {
		if !strings.Contains(html, pin) {
			t.Fatalf("index.html missing organization fallback pin %q", pin)
		}
	}
}
