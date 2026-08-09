package main

import (
	"os"
	"strings"
	"testing"
)

func TestStrideE10LegacyContributionRankingIsAbsent(t *testing.T) {
	t.Parallel()

	files := []string{"mission_intelligence.go", "index.html"}
	forbidden := []string{
		"missionContributionFuel",
		"missionContributionRow",
		"missionContributions(",
		`json:"fuel"`,
		"person.fuel",
		"fuelMax",
		"fuelTotal",
		"renderIntelContributions",
		"intelContribList",
		"contribution-fuel",
		"contribution fuel",
		"contribution-share",
		"contribution share",
	}
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		lower := strings.ToLower(string(body))
		for _, marker := range forbidden {
			if strings.Contains(lower, strings.ToLower(marker)) {
				t.Fatalf("%s reintroduced prohibited activity-volume ranking marker %q", path, marker)
			}
		}
	}
}
