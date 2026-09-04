package main

import (
	"os"
	"strings"
	"testing"
)

func TestSTRIDESuggestedWorkUISurfacesRecommendationOrManualChoiceWithoutLaunching(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	// Build 18 removed the legacy Suggested Work resolver and its project
	// chooser. Work is now a first-class destination; visiting it renders
	// server-owned projects and never mutates merely because a recommendation
	// was displayed.
	for _, required := range []string{
		// Wave 11 D1: the destination reads "Packaging Studio"; the id/route stay "Work".
		`data-pd1-destination="Work" aria-label="Packaging Studio"`,
		`const PD1_DESTINATIONS = Object.freeze(['Home', 'Video', 'Conversations', 'Work', 'Drive'])`,
		"function renderStudioProjects()",
		"async function loadStudioProjects(",
		"selectPD1Destination('Work'",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("build-18 Work destination is missing %q", required)
		}
	}
	for _, retired := range []string{"destinationRecommendation", "eligibleThreadIds", "destinationMeta.dataset.routeResolver", "Project · choose or create"} {
		if strings.Contains(body, retired) {
			t.Fatalf("retired Suggested Work resolver returned: %q", retired)
		}
	}
}
