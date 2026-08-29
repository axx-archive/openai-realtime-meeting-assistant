package main

import (
	"os"
	"strings"
	"testing"
)

func contractedNetworkShell(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestRetiredContributionNetworkDoesNotMountACustomerSurface(t *testing.T) {
	html := contractedNetworkShell(t)
	for _, forbidden := range []string{
		`id="strideW2Surface"`,
		`data-tool="network"`,
		`window.openStrideContributionSurface`,
		`projectionIsBodyFree`,
		`strideW2FixtureProjection`,
		`/api/stride/v1/mobile/actions/`,
		`View as recruiter`,
		`Contribution approvals`,
	} {
		if strings.Contains(html, forbidden) {
			t.Errorf("retired contribution-network surface remains: %q", forbidden)
		}
	}
}

func TestRetiredContributionRoutesAreReadFreeCompatibilityRedirects(t *testing.T) {
	html := contractedNetworkShell(t)
	start := strings.Index(html, `const retiredNetworkPath =`)
	end := strings.Index(html[start:], `})()`)
	if start < 0 || end < 0 {
		t.Fatal("retired contribution-route redirect missing")
	}
	redirect := html[start : start+end]
	for _, marker := range []string{
		`path === '/me'`,
		`path === '/work-search'`,
		`path === '/work-record'`,
		`path === '/team'`,
		`path === '/people'`,
		`path === '/org/people'`,
		`path === '/org/requests'`,
		`path === '/org/contributions'`,
		`path === '/network'`,
		`path.startsWith('/network/')`,
		`path.startsWith('/org/recruiting')`,
		`path.startsWith('/people/')`,
		`const destination = retiredWorkPath(path) ? 'Work' : 'Home'`,
		`history.replaceState(`,
	} {
		if !strings.Contains(redirect, marker) {
			t.Errorf("compatibility redirect missing %q", marker)
		}
	}
	if strings.Contains(redirect, "fetch(") || strings.Contains(redirect, "/api/") {
		t.Fatal("retired route redirect loads projection data")
	}
}

func TestOrganizationContextRemainsAReadOnlyTenancyControl(t *testing.T) {
	html := contractedNetworkShell(t)
	for _, marker := range []string{
		`id="topbarOrganizationSwitcher"`,
		`id="topbarOrganizationName"`,
		`id="topbarOrganizationMenu"`,
		`id="topbarOrganizationMenuItems"`,
		`id="topbarOrganizationCreate"`,
		`fetch('/api/stride/v1/mobile/surfaces/organizations', { credentials: 'same-origin' })`,
		`item?.detail?.isCurrent`,
		`window.__strideCurrentOrganizationLabel = currentLabel`,
		`openSettings({ section: 'organizations'`,
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("read-only organization context missing %q", marker)
		}
	}
	organizationScriptStart := strings.Index(html, `// Organization context remains a small tenancy control`)
	organizationScriptEnd := strings.Index(html[organizationScriptStart:], `})()`)
	if organizationScriptStart < 0 || organizationScriptEnd < 0 {
		t.Fatal("could not isolate organization context control")
	}
	organizationScript := html[organizationScriptStart : organizationScriptStart+organizationScriptEnd]
	for _, forbidden := range []string{"method: 'POST'", "method: 'PUT'", "method: 'DELETE'", "/mobile/actions/"} {
		if strings.Contains(organizationScript, forbidden) {
			t.Errorf("organization context gained customer mutation behavior: %q", forbidden)
		}
	}
}
