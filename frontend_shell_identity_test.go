package main

import (
	"os"
	"strings"
	"testing"
)

func TestDesktopShellNamesProductAndOrganization(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, marker := range []string{
		`class="topbar__brand-lockup"`,
		`class="wordmark topbar__brand-wordmark"`,
		`class="topbar__organization-label">Organization`,
		`class="topbar__organization-name">Bonfire`,
		`--shell-topbar-height: 60px`,
		`padding-left: 72px`,
		`#appShell.is-authed .tool-rail > .topbar__mark`,
		`topbarBrandLockupEl?.classList.add('is-listening')`,
		`topbarBrandLockupEl?.classList.add('is-wake')`,
	} {
		if !strings.Contains(source, marker) {
			t.Fatalf("desktop shell missing %q", marker)
		}
	}
	if !strings.Contains(source, `case 'office':`+"\n            return 'Bonfire'") {
		t.Fatal("phone shell does not surface the Bonfire organization")
	}
}
