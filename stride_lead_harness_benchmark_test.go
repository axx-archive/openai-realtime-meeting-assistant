package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSTRIDELeadHarnessBenchmarkHasThreeBalancedDeliverableLanesAndHardGates(t *testing.T) {
	raw, err := os.ReadFile("testdata/stride/lead-harness-benchmark-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var benchmark struct {
		SchemaVersion   int                `json:"schemaVersion"`
		RetirementGates map[string]float64 `json:"retirementGates"`
		Cases           []struct {
			ID         string   `json:"id"`
			Kind       string   `json:"kind"`
			Request    string   `json:"request"`
			Context    string   `json:"context"`
			HardChecks []string `json:"hardChecks"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &benchmark); err != nil {
		t.Fatal(err)
	}
	if benchmark.SchemaVersion != 1 || len(benchmark.Cases) != 36 {
		t.Fatalf("schema=%d cases=%d, want 1 and 36", benchmark.SchemaVersion, len(benchmark.Cases))
	}
	counts, ids := map[string]int{}, map[string]bool{}
	for _, item := range benchmark.Cases {
		if item.ID == "" || ids[item.ID] || item.Request == "" || item.Context == "" || len(item.HardChecks) < 3 {
			t.Fatalf("invalid benchmark case: %+v", item)
		}
		ids[item.ID], counts[item.Kind] = true, counts[item.Kind]+1
	}
	for _, kind := range []string{"research", "presentation", "image"} {
		if counts[kind] != 12 {
			t.Fatalf("%s cases=%d, want 12", kind, counts[kind])
		}
	}
	for _, gate := range []string{"openRenderEditableRate", "reviewableWithoutFollowupRate", "falseNeedsYouRateMax", "aclOrLineageViolationsMax", "duplicateWorkCardsMax"} {
		if _, ok := benchmark.RetirementGates[gate]; !ok {
			t.Fatalf("missing retirement gate %s", gate)
		}
	}
}
