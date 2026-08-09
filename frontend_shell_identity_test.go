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
		`id="topbarOrganizationSwitcher"`,
		`id="topbarOrganizationRole" class="topbar__organization-label">not loaded`,
		`id="topbarOrganizationName" class="topbar__organization-name">Organization unavailable`,
		`id="topbarOrganizationCount" class="topbar__organization-meta">0 of 3 active`,
		`id="topbarOrganizationPending"`,
		`min-height: 40px`,
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
	if !strings.Contains(source, `return window.__strideCurrentOrganizationLabel || 'Organization unavailable'`) {
		t.Fatal("phone shell must use the same server-projected organization label")
	}
	if strings.Contains(source, `class="topbar__organization-name">Bonfire`) || strings.Contains(source, `Stride · Bonfire organization`) {
		t.Fatal("top bar still treats a hardcoded organization name as authority")
	}
}
