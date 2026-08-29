package main

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"
)

const strideLeadBenchmarkV1Path = "testdata/stride/lead-harness-benchmark-v1.json"

type STRIDELeadBenchmark struct {
	SchemaVersion   int                                `json:"schemaVersion"`
	Purpose         string                             `json:"purpose"`
	RetirementGates STRIDELeadBenchmarkRetirementGates `json:"retirementGates"`
	Cases           []STRIDELeadBenchmarkCase          `json:"cases"`
}

type STRIDELeadBenchmarkRetirementGates struct {
	OpenRenderEditableRate        float64 `json:"openRenderEditableRate"`
	ReviewableWithoutFollowupRate float64 `json:"reviewableWithoutFollowupRate"`
	FalseNeedsYouRateMax          float64 `json:"falseNeedsYouRateMax"`
	ACLOrLineageViolationsMax     float64 `json:"aclOrLineageViolationsMax"`
	DuplicateWorkCardsMax         float64 `json:"duplicateWorkCardsMax"`
}

type STRIDELeadBenchmarkCase struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Request    string   `json:"request"`
	Context    string   `json:"context"`
	HardChecks []string `json:"hardChecks"`
}

func LoadSTRIDELeadBenchmark(path string) (STRIDELeadBenchmark, error) {
	if strings.TrimSpace(path) == "" {
		path = strideLeadBenchmarkV1Path
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return STRIDELeadBenchmark{}, err
	}
	var benchmark STRIDELeadBenchmark
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&benchmark); err != nil || benchmark.Validate() != nil {
		return STRIDELeadBenchmark{}, ErrSTRIDELeadHarnessInvalid
	}
	return benchmark, nil
}

func (benchmark STRIDELeadBenchmark) Validate() error {
	if benchmark.SchemaVersion != 1 || !humanActivitySummary(benchmark.Purpose) || len(benchmark.Cases) == 0 ||
		benchmark.RetirementGates.OpenRenderEditableRate <= 0 || benchmark.RetirementGates.OpenRenderEditableRate > 1 ||
		benchmark.RetirementGates.ReviewableWithoutFollowupRate <= 0 || benchmark.RetirementGates.ReviewableWithoutFollowupRate > 1 ||
		benchmark.RetirementGates.FalseNeedsYouRateMax < 0 || benchmark.RetirementGates.FalseNeedsYouRateMax > 1 ||
		benchmark.RetirementGates.ACLOrLineageViolationsMax < 0 || benchmark.RetirementGates.DuplicateWorkCardsMax < 0 {
		return ErrSTRIDELeadHarnessInvalid
	}
	seen := map[string]bool{}
	for _, item := range benchmark.Cases {
		if !strideIdentifier(item.ID) || seen[item.ID] || !oneOf(item.Kind, "research", "presentation", "image") ||
			strings.TrimSpace(item.Request) == "" || !strideIdentifier(item.Context) || len(item.HardChecks) < 1 {
			return ErrSTRIDELeadHarnessInvalid
		}
		seen[item.ID] = true
		for _, check := range item.HardChecks {
			if !strideIdentifier(check) {
				return ErrSTRIDELeadHarnessInvalid
			}
		}
	}
	return nil
}

type STRIDELeadBenchmarkLane string

const (
	STRIDELeadBenchmarkHarness STRIDELeadBenchmarkLane = "lead_harness"
	STRIDELeadBenchmarkLegacy  STRIDELeadBenchmarkLane = "legacy_stride"
	STRIDELeadBenchmarkDirect  STRIDELeadBenchmarkLane = "direct_model"
)

type STRIDELeadBenchmarkCandidate struct {
	CaseID        string
	Lane          STRIDELeadBenchmarkLane
	Artifact      STRIDEReference
	WorkCardCount int
	NeedsYou      bool
}

type STRIDELeadBenchmarkBlindPacket struct {
	CaseID     string          `json:"caseId"`
	BlindID    string          `json:"blindId"`
	Artifact   STRIDEReference `json:"artifact"`
	HardChecks []string        `json:"hardChecks"`
}

type STRIDELeadBenchmarkBlindReceipt struct {
	CaseID                    string          `json:"caseId"`
	BlindID                   string          `json:"blindId"`
	Artifact                  STRIDEReference `json:"artifact"`
	Evaluator                 string          `json:"evaluator"`
	HardCheckResults          map[string]bool `json:"hardCheckResults"`
	OpenRenderEditable        bool            `json:"openRenderEditable"`
	ReviewableWithoutFollowup bool            `json:"reviewableWithoutFollowup"`
	NeedsYouShown             bool            `json:"needsYouShown"`
	NeedsYouJustified         bool            `json:"needsYouJustified"`
	ACLOrLineageViolations    int             `json:"aclOrLineageViolations"`
	DuplicateWorkCards        int             `json:"duplicateWorkCards"`
	ObservedAt                time.Time       `json:"observedAt"`
}

type strideLeadBenchmarkReveal struct {
	CaseID        string
	Lane          STRIDELeadBenchmarkLane
	Artifact      STRIDEReference
	BlindArtifact STRIDEReference
	WorkCardCount int
	NeedsYou      bool
}

type STRIDELeadBenchmarkRunner struct {
	Benchmark STRIDELeadBenchmark
	reveal    map[string]strideLeadBenchmarkReveal
}

func NewSTRIDELeadBenchmarkRunner(benchmark STRIDELeadBenchmark, candidates []STRIDELeadBenchmarkCandidate) (*STRIDELeadBenchmarkRunner, []STRIDELeadBenchmarkBlindPacket, error) {
	if benchmark.Validate() != nil {
		return nil, nil, ErrSTRIDELeadHarnessInvalid
	}
	byCase := map[string]STRIDELeadBenchmarkCase{}
	for _, item := range benchmark.Cases {
		byCase[item.ID] = item
	}
	runner := &STRIDELeadBenchmarkRunner{Benchmark: benchmark, reveal: map[string]strideLeadBenchmarkReveal{}}
	packets := make([]STRIDELeadBenchmarkBlindPacket, 0, len(candidates))
	seen := map[string]bool{}
	for _, candidate := range candidates {
		item, ok := byCase[candidate.CaseID]
		if !ok || !validSTRIDELeadBenchmarkLane(candidate.Lane) || candidate.Artifact.Validate() != nil || candidate.WorkCardCount < 0 {
			return nil, nil, ErrSTRIDELeadHarnessInvalid
		}
		key := candidate.CaseID + "\x00" + string(candidate.Lane)
		if seen[key] {
			return nil, nil, ErrSTRIDELeadHarnessInvalid
		}
		seen[key] = true
		blindID := "blind_" + sha256Hex([]byte("stride-lead-benchmark/v1\x00" + key + "\x00" + candidate.Artifact.Digest))[:24]
		blindArtifact := candidate.Artifact
		blindArtifact.ID = blindID
		runner.reveal[blindID] = strideLeadBenchmarkReveal{candidate.CaseID, candidate.Lane, candidate.Artifact, blindArtifact, candidate.WorkCardCount, candidate.NeedsYou}
		packets = append(packets, STRIDELeadBenchmarkBlindPacket{CaseID: candidate.CaseID, BlindID: blindID, Artifact: blindArtifact, HardChecks: append([]string(nil), item.HardChecks...)})
	}
	for _, item := range benchmark.Cases {
		for _, lane := range []STRIDELeadBenchmarkLane{STRIDELeadBenchmarkHarness, STRIDELeadBenchmarkLegacy, STRIDELeadBenchmarkDirect} {
			if !seen[item.ID+"\x00"+string(lane)] {
				return nil, nil, ErrSTRIDELeadHarnessInvalid
			}
		}
	}
	sort.Slice(packets, func(i, j int) bool { return packets[i].BlindID < packets[j].BlindID })
	return runner, packets, nil
}

type STRIDELeadBenchmarkLaneResult struct {
	Lane                          STRIDELeadBenchmarkLane `json:"lane"`
	Cases                         int                     `json:"cases"`
	OpenRenderEditableRate        float64                 `json:"openRenderEditableRate"`
	ReviewableWithoutFollowupRate float64                 `json:"reviewableWithoutFollowupRate"`
	FalseNeedsYouRate             float64                 `json:"falseNeedsYouRate"`
	ACLOrLineageViolations        int                     `json:"aclOrLineageViolations"`
	DuplicateWorkCards            int                     `json:"duplicateWorkCards"`
	HardCheckPassRate             float64                 `json:"hardCheckPassRate"`
	PassesRetirementGates         bool                    `json:"passesRetirementGates"`
}

func (runner *STRIDELeadBenchmarkRunner) Evaluate(receipts []STRIDELeadBenchmarkBlindReceipt) ([]STRIDELeadBenchmarkLaneResult, error) {
	if runner == nil || runner.Benchmark.Validate() != nil || len(receipts) != len(runner.reveal) {
		return nil, ErrSTRIDELeadHarnessInvalid
	}
	type accumulator struct{ cases, open, reviewable, falseNeeds, violations, duplicates, checks, passed int }
	acc := map[STRIDELeadBenchmarkLane]*accumulator{}
	seen := map[string]bool{}
	cases := map[string]STRIDELeadBenchmarkCase{}
	for _, item := range runner.Benchmark.Cases {
		cases[item.ID] = item
	}
	for _, receipt := range receipts {
		reveal, ok := runner.reveal[receipt.BlindID]
		item := cases[receipt.CaseID]
		if !ok || seen[receipt.BlindID] || reveal.CaseID != receipt.CaseID || reveal.BlindArtifact != receipt.Artifact ||
			receipt.NeedsYouShown != reveal.NeedsYou ||
			!strideIdentifier(receipt.Evaluator) || receipt.ObservedAt.IsZero() || receipt.ObservedAt.Location() != time.UTC ||
			receipt.ACLOrLineageViolations < 0 || receipt.DuplicateWorkCards < 0 || len(receipt.HardCheckResults) != len(item.HardChecks) {
			return nil, ErrSTRIDELeadHarnessInvalid
		}
		seen[receipt.BlindID] = true
		row := acc[reveal.Lane]
		if row == nil {
			row = &accumulator{}
			acc[reveal.Lane] = row
		}
		row.cases++
		if receipt.OpenRenderEditable {
			row.open++
		}
		if receipt.ReviewableWithoutFollowup {
			row.reviewable++
		}
		if receipt.NeedsYouShown && !receipt.NeedsYouJustified {
			row.falseNeeds++
		}
		row.violations += receipt.ACLOrLineageViolations
		row.duplicates += receipt.DuplicateWorkCards + maxInt(0, reveal.WorkCardCount-1)
		for _, check := range item.HardChecks {
			passed, exists := receipt.HardCheckResults[check]
			if !exists {
				return nil, ErrSTRIDELeadHarnessInvalid
			}
			row.checks++
			if passed {
				row.passed++
			}
		}
	}
	if len(seen) != len(runner.reveal) {
		return nil, ErrSTRIDELeadHarnessInvalid
	}
	results := make([]STRIDELeadBenchmarkLaneResult, 0, 3)
	for _, lane := range []STRIDELeadBenchmarkLane{STRIDELeadBenchmarkHarness, STRIDELeadBenchmarkLegacy, STRIDELeadBenchmarkDirect} {
		row := acc[lane]
		if row == nil || row.cases == 0 {
			return nil, ErrSTRIDELeadHarnessInvalid
		}
		result := STRIDELeadBenchmarkLaneResult{Lane: lane, Cases: row.cases, OpenRenderEditableRate: float64(row.open) / float64(row.cases), ReviewableWithoutFollowupRate: float64(row.reviewable) / float64(row.cases), FalseNeedsYouRate: float64(row.falseNeeds) / float64(row.cases), ACLOrLineageViolations: row.violations, DuplicateWorkCards: row.duplicates, HardCheckPassRate: float64(row.passed) / float64(row.checks)}
		gates := runner.Benchmark.RetirementGates
		result.PassesRetirementGates = result.OpenRenderEditableRate >= gates.OpenRenderEditableRate && result.ReviewableWithoutFollowupRate >= gates.ReviewableWithoutFollowupRate && result.FalseNeedsYouRate <= gates.FalseNeedsYouRateMax && float64(result.ACLOrLineageViolations) <= gates.ACLOrLineageViolationsMax && float64(result.DuplicateWorkCards) <= gates.DuplicateWorkCardsMax
		results = append(results, result)
	}
	return results, nil
}

func validSTRIDELeadBenchmarkLane(lane STRIDELeadBenchmarkLane) bool {
	return lane == STRIDELeadBenchmarkHarness || lane == STRIDELeadBenchmarkLegacy || lane == STRIDELeadBenchmarkDirect
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
