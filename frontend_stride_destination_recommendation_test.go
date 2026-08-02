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
	for _, required := range []string{
		"destinationRecommendation",
		"eligibleThreadIds",
		"eligibleThreadSet.has(String(thread.id))",
		"Recommended · ${strideWords(strideField(record, 'destinationTitle', 'DestinationTitle'), 'Project')} · ${confidence}% · access checked",
		"Project · pick the right match",
		"Project · choose or create",
		"destinationMeta.dataset.routeResolver",
		"destinationMeta.dataset.routeDigest",
		"}, !destination))",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("Suggested Work UI is missing %q", required)
		}
	}
	if strings.Contains(body, "recommendationStatus === 'recommended' && destination) {\n          await strideTeamJSON") {
		t.Fatal("rendering a recommendation unexpectedly performs a mutation")
	}
}
