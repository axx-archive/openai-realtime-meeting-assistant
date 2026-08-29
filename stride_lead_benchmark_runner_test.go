package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSTRIDELeadBenchmarkRunnerProducesBlindThreeLaneReceiptsAndHardGates(t *testing.T) {
	benchmark, err := LoadSTRIDELeadBenchmark("")
	if err != nil {
		t.Fatal(err)
	}
	var candidates []STRIDELeadBenchmarkCandidate
	for index, item := range benchmark.Cases {
		for laneIndex, lane := range []STRIDELeadBenchmarkLane{STRIDELeadBenchmarkHarness, STRIDELeadBenchmarkLegacy, STRIDELeadBenchmarkDirect} {
			candidates = append(candidates, STRIDELeadBenchmarkCandidate{
				CaseID: item.ID, Lane: lane, WorkCardCount: 1,
				Artifact: STRIDEReference{ContractType: STRIDEContractOutcome, ID: "benchmark_" + item.ID + "_" + string(lane), Revision: 1, Digest: sha256Hex([]byte(item.ID + string(lane) + string(rune(index+laneIndex+1))))},
			})
		}
	}
	runner, packets, err := NewSTRIDELeadBenchmarkRunner(benchmark, candidates)
	if err != nil || len(packets) != len(benchmark.Cases)*3 {
		t.Fatalf("packets=%d err=%v", len(packets), err)
	}
	for _, packet := range packets {
		raw, _ := json.Marshal(packet)
		if strings.Contains(string(raw), "lead_harness") || strings.Contains(string(raw), "legacy_stride") || strings.Contains(string(raw), "direct_model") {
			t.Fatalf("blind packet leaked lane: %s", raw)
		}
	}
	at := time.Date(2026, 8, 25, 22, 0, 0, 0, time.UTC)
	receipts := make([]STRIDELeadBenchmarkBlindReceipt, 0, len(packets))
	for _, packet := range packets {
		checks := map[string]bool{}
		for _, check := range packet.HardChecks {
			checks[check] = true
		}
		receipts = append(receipts, STRIDELeadBenchmarkBlindReceipt{
			CaseID: packet.CaseID, BlindID: packet.BlindID, Artifact: packet.Artifact, Evaluator: "reviewer_1",
			HardCheckResults: checks, OpenRenderEditable: true, ReviewableWithoutFollowup: true, ObservedAt: at,
		})
	}
	results, err := runner.Evaluate(receipts)
	if err != nil || len(results) != 3 {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	for _, result := range results {
		if !result.PassesRetirementGates || result.HardCheckPassRate != 1 || result.DuplicateWorkCards != 0 || result.ACLOrLineageViolations != 0 {
			t.Fatalf("lane result=%+v", result)
		}
	}
}
