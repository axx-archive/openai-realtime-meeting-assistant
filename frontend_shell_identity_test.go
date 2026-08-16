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
		`class="topbar__brand-wordmark"`,
		`id="topbarOrganizationSwitcher" class="topbar__organization"`,
		`aria-label="Choose organization" aria-haspopup="menu" aria-expanded="false"`,
		`id="topbarOrganizationName" class="topbar__organization-name">Organizations`,
		`id="topbarOrganizationMenu" class="topbar__organization-menu" role="menu"`,
		`id="topbarOrganizationMenuItems"`,
		`id="topbarOrganizationCreate"`,
		`<span>Create organization</span>`,
		`min-height: 40px`,
		`--shell-topbar-height: 60px`,
		`padding-left: 56px`,
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
	for _, stale := range []string{`id="topbarOrganizationRole"`, `id="topbarOrganizationCount"`, `id="topbarOrganizationPending"`, `0 of 3 active`} {
		if strings.Contains(source, stale) {
			t.Fatalf("closed organization control still exposes superseded status chrome %q", stale)
		}
	}
	if strings.Contains(source, `class="topbar__organization-name">Bonfire`) || strings.Contains(source, `Stride · Bonfire organization`) {
		t.Fatal("top bar still treats a hardcoded organization name as authority")
	}
}
