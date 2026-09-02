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
		`id="topbarOrganizationSwitcher" class="topbar__organization"`,
		`aria-label="Choose organization" aria-haspopup="menu" aria-expanded="false"`,
		`id="topbarOrganizationName" class="topbar__organization-name">Organizations`,
		`id="topbarOrganizationMenu" class="topbar__organization-menu" role="menu"`,
		`id="topbarOrganizationMenuItems"`,
		`id="topbarOrganizationCreate"`,
		`<span>Create organization</span>`,
		`min-height: 40px`,
		`--shell-topbar-height: 60px`,
		// AJ ratified the wide labelled rail 2026-09-02: the shell offset is the
		// --rail-width token (56px slim / 168px labelled), not a literal.
		`--rail-width: 56px`,
		`padding-left: var(--rail-width, 56px)`,
		`#appShell.is-authed .tool-rail > .topbar__mark`,
		`topbarBrandLockupEl?.classList.add('is-listening')`,
		`topbarBrandLockupEl?.classList.add('is-wake')`,
	} {
		if !strings.Contains(source, marker) {
			t.Fatalf("desktop shell missing %q", marker)
		}
	}
	// The fallback is the workspace label, never "Organization unavailable"
	// (org chip rule, pinned by TestIndexOrganizationChipNeverReadsUnavailable;
	// re-pinned here 2026-09-02 so the two pins agree).
	if !strings.Contains(source, `return window.__strideCurrentOrganizationLabel || workspaceOrganizationLabel()`) {
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
	// AJ: org-first shell 2026-09-02 — the topbar carries no Stride wordmark
	// and no identity lockup; the organization is named in the rail.
	for _, retired := range []string{`class="topbar__brand-lockup"`, `class="topbar__brand-wordmark"`, `class="topbar__identity-divider"`} {
		if strings.Contains(source, retired) {
			t.Fatalf("org-first shell: the topbar must not carry %q", retired)
		}
	}
	// AJ ratified 2026-09-02: wordmark back, no flame, no date, no status by
	// the org name. The rail's top row is the Stride wordmark (the production
	// artwork via --wordmark-image, colour token --wordmark) inside the
	// organization button; the account row names the org alone.
	for _, want := range []string{
		`id="brandMark" class="topbar__mark" aria-hidden="true"><span class="topbar__wordmark wordmark"></span></span>`,
		"      .topbar__mark {\n        display: inline-flex;",
		"        color: var(--wordmark);\n        overflow: visible;",
		"switcher.setAttribute('aria-label', `${currentLabel} — organization`)",
		"if (accountOrgLine) accountOrgLine.textContent = orgLabel",
		"accountButton.title = person ? `${person} · ${orgLabel}` : orgLabel",
		`#appShell.is-authed[data-org-identity="lockup"] .tool-rail > .topbar__organization > .topbar__mark { grid-area: mark;`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("rail wordmark shell missing %q", want)
		}
	}
	// AJ 2026-09-02: bell top-right, status only when not ready, theme in Settings
	for _, want := range []string{
		`<div class="tool-rail__utilities" aria-label="Account">`,
		`id="notificationBell" class="topbar__notify tool-rail__bell"`,
		`data-state="ready" aria-hidden="true"`,
		`#statusPill[data-state="ready"] {`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("topbar chrome missing %q", want)
		}
	}
	// the status pill owns connectivity state now — the org switcher carries no
	// "offline" tag (markup, wiring, or stylesheet) in any identity mode
	for _, gone := range []string{`class="topbar__mark-signal"`, "orgFlameGlow", "orgFlameCore", "--org-tile", `id="topbarDate"`, "${orgLabel} · ${String(offlineTag.textContent", `class="tool-rail__tool tool-rail__theme"`, `class="tool-rail__tool tool-rail__bell"`, `id="topbarOrganizationTag"`, `class="topbar__organization-tag"`, ".topbar__organization-tag", "offlineTag", "organizationsOffline", ">offline<"} {
		if strings.Contains(source, gone) {
			t.Fatalf("AJ 2026-09-02 retired the flame tile, the topbar date and the status by the org name; found %q", gone)
		}
	}
}
