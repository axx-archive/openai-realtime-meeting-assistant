package main

import (
	"os"
	"strings"
	"testing"
)

func TestFrontendMeetingSpecialistsUsesRealControlRouteAndHonestDisclosure(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(source)
	for _, want := range []string{
		`id="roomMoreSpecialists"`,
		`id="meetingSpecialistsPanel"`,
		`/api/stride/v1/meeting-specialists`,
		`data-specialist-action="request"`,
		`data-specialist-action="approved"`,
		`data-specialist-action="declined"`,
		`data-specialist-action="dismissed"`,
		`A teammate must approve the exact request before an agent can join`,
		`Provider voice remains visibly fenced`,
		`Voice joining stays off until provider qualification is complete`,
		`meetingSpecialistContextLabel(invitation.contextClasses)`,
		`meetingSpecialistAudienceLabel(invitation.audience)`,
		`meetingSpecialistLimitsLabel(invitation.hardLimits)`,
		`Approved context and limits`,
		`Hard limits`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("meeting specialist surface is missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`Mary · Marketing Agent`,
		`providerSessionStarted: true`,
		`data-specialist-action="join"`,
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("meeting specialist surface embeds forbidden claim %q", forbidden)
		}
	}
}

func TestFrontendMeetingSpecialistsEscapesEveryRuntimeInterpolation(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(source)
	start := strings.Index(html, "function renderMeetingSpecialists")
	end := strings.Index(html, "async function meetingSpecialistFetch")
	if start < 0 || end <= start {
		t.Fatal("could not isolate meeting specialist renderer")
	}
	renderer := html[start:end]
	for _, interpolation := range []string{
		"escapeHtml(invitation.displayName || invitation.agentId",
		"escapeHtml(meetingSpecialistStatusLabel(invitation.status))",
		"escapeHtml(invitation.id)",
		"escapeHtml(purpose)",
		"escapeHtml(stateCopy)",
		"escapeHtml(contextLabel)",
		"escapeHtml(audienceLabel)",
		"escapeHtml(expectedLabel)",
		"escapeHtml(limitsLabel)",
		"escapeHtml(candidate.displayName || candidate.agentId)",
		"escapeHtml(candidate.agentId)",
	} {
		if !strings.Contains(renderer, interpolation) {
			t.Fatalf("runtime interpolation %q is not escaped", interpolation)
		}
	}
}
