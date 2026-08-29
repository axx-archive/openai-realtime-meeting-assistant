package main

import (
	"os"
	"strings"
	"testing"
)

func strideContractedShell(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(source)
}

func TestFrontendRetiredMarketplaceTeamAndNetworkSurfacesAreRemoved(t *testing.T) {
	html := strideContractedShell(t)
	for _, forbidden := range []string{
		`id="workToolMenu"`,
		`id="teamTool"`,
		`id="strideW2Surface"`,
		`id="strideMarketplacePanel"`,
		`id="strideAgentDetail"`,
		`id="stridePrivateAgentCreate"`,
		`id="strideProjectPicker"`,
		`data-tool="team"`,
		`/api/stride/v1/marketplace`,
		`/api/stride/v1/roster`,
		`Start preview`,
		`Hire with approval`,
		`Create private agent`,
		`Curated marketplace`,
		`Work Search is not yet available`,
	} {
		if strings.Contains(html, forbidden) {
			t.Errorf("retired customer-shell concept remains: %q", forbidden)
		}
	}
}

func TestFrontendRetiredRoutesConvergeWithoutLoadingProjectionData(t *testing.T) {
	html := strideContractedShell(t)
	start := strings.Index(html, `const retiredNetworkPath =`)
	if start < 0 {
		t.Fatal("retired route compatibility redirect is missing")
	}
	end := strings.Index(html[start:], `})()`)
	if end < 0 {
		t.Fatal("could not isolate retired route compatibility redirect")
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
		`path === '/marketplace'`,
		`path === '/network'`,
		`path.startsWith('/network/')`,
		`path === '/tools'`,
		`path === '/agents'`,
		`const destination = retiredWorkPath(path) ? 'Work' : 'Home'`,
		`history.replaceState(`,
		`selectPD1Destination(destination, { push: false`,
	} {
		if !strings.Contains(redirect, marker) {
			t.Errorf("retired route contract missing %q", marker)
		}
	}
	for _, forbidden := range []string{"fetch(", "strideTeamJSON", "loadStrideTeamSurface", "openStrideContributionSurface"} {
		if strings.Contains(redirect, forbidden) {
			t.Errorf("retired route redirect loads or mounts projection state: %q", forbidden)
		}
	}
}

func TestFrontendGovernedAgentMentionsRemainAddressable(t *testing.T) {
	html := strideContractedShell(t)
	for _, marker := range []string{
		`function mentionRosterCandidates()`,
		`desktopChatMentionCandidates.forEach(candidate =>`,
		`if (!desktopChatParticipantDirectoryLoaded) void ensureDesktopChatParticipantDirectory()`,
		`kind === 'agent'`,
		`roleTitle || 'Specialist'`,
		`const privateTarget = directAgent || 'Scout'`,
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("governed agent mention addressability missing %q", marker)
		}
	}
}
