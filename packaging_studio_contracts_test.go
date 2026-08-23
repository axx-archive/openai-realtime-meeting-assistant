package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func packagingStudioValidDeckCopyForTest() map[string]any {
	slide := func(id, kind, turn, headline, kicker, body string) map[string]any {
		return map[string]any{
			"slide_id": id, "slide_kind": kind, "thesis": headline, "turn": turn,
			"headline": headline, "kicker": kicker, "body": body, "proof": "",
			"evidence_label": "", "source_label": "", "speaker_intent": "",
			"transition": "", "presenter_note": "", "claim_ids": []any{}, "claim_renderings": []any{},
		}
	}
	return map[string]any{
		"slide_count_inference": "",
		"slides": []any{
			slide("cover", "cover", "open", "The room already moved", "See what changed", ""),
			slide("decision", "normal", "decide", "Back the format people repeat", "", "Run one bounded pilot before scaling."),
		},
	}
}

func packagingStudioDeckCopyJSONForTest(t *testing.T, value map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func packagingStudioCloneDeckCopyForTest(t *testing.T) map[string]any {
	t.Helper()
	raw := packagingStudioDeckCopyJSONForTest(t, packagingStudioValidDeckCopyForTest())
	var clone map[string]any
	if err := json.Unmarshal([]byte(raw), &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func TestPackagingStudioDeckCopyPremiumContract(t *testing.T) {
	plan := &goalPlan{Objective: "Build the actual 2-slide deck"}
	if err := validatePackagingStudioDeckCopyOutput(nil, plan, packagingStudioDeckCopyJSONForTest(t, packagingStudioValidDeckCopyForTest())); err != nil {
		t.Fatalf("valid sparse deck copy rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{name: "undeclared root", mutate: func(value map[string]any) { value["creative_note"] = "surprise" }, want: "undeclared root"},
		{name: "undeclared slide field", mutate: func(value map[string]any) { value["slides"].([]any)[1].(map[string]any)["subtitle_two"] = "more" }, want: "undeclared field"},
		{name: "competing thesis", mutate: func(value map[string]any) { value["slides"].([]any)[1].(map[string]any)["thesis"] = "A second idea" }, want: "one thesis"},
		{name: "three primary groups", mutate: func(value map[string]any) { value["slides"].([]any)[1].(map[string]any)["kicker"] = "Another heading" }, want: "two primary"},
		{name: "crowded cover", mutate: func(value map[string]any) {
			value["slides"].([]any)[0].(map[string]any)["body"] = "A body does not belong here."
		}, want: "cover must be sparse"},
		{name: "unpaired claim", mutate: func(value map[string]any) {
			value["slides"].([]any)[1].(map[string]any)["claim_ids"] = []any{strings.Repeat("a", 64)}
		}, want: "paired one-to-one"},
		{name: "evidence furniture without claim", mutate: func(value map[string]any) {
			value["slides"].([]any)[1].(map[string]any)["evidence_label"] = "38% conversion"
		}, want: "requires one admitted claim"},
		{name: "claim rendering absent from visible fact field", mutate: func(value map[string]any) {
			slide := value["slides"].([]any)[1].(map[string]any)
			slide["claim_ids"], slide["claim_renderings"] = []any{strings.Repeat("a", 64)}, []any{"38%"}
		}, want: "exactly one visible fact-bearing field"},
		{name: "claim rendering duplicated across visible fields", mutate: func(value map[string]any) {
			slide := value["slides"].([]any)[1].(map[string]any)
			slide["body"], slide["evidence_label"] = "Run a 38% pilot.", "38%"
			slide["claim_ids"], slide["claim_renderings"] = []any{strings.Repeat("a", 64)}, []any{"38%"}
		}, want: "exactly one visible fact-bearing field"},
		{name: "source label cannot steal claim ownership", mutate: func(value map[string]any) {
			slide := value["slides"].([]any)[1].(map[string]any)
			slide["body"], slide["evidence_label"], slide["source_label"] = "Run a 38% pilot.", "Decision proof", "Source study, 38%"
			slide["claim_ids"], slide["claim_renderings"] = []any{strings.Repeat("a", 64)}, []any{"38%"}
		}, want: "source_label may cite the source but may not own or repeat"},
		{name: "invalid statement type", mutate: func(value map[string]any) {
			slide := value["slides"].([]any)[1].(map[string]any)
			slide["statement_type"] = "prediction"
		}, want: "not admitted"},
		{name: "statement type without exactly one visible owner", mutate: func(value map[string]any) {
			slide := value["slides"].([]any)[1].(map[string]any)
			slide["statement_type"] = "recommendation"
		}, want: "exactly one visibly labeled field"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := packagingStudioCloneDeckCopyForTest(t)
			test.mutate(value)
			err := validatePackagingStudioDeckCopyOutput(nil, plan, packagingStudioDeckCopyJSONForTest(t, value))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestPackagingStudioDeckCopyBindsStatementTypeToOneVisibleField(t *testing.T) {
	value := packagingStudioCloneDeckCopyForTest(t)
	slide := value["slides"].([]any)[1].(map[string]any)
	slide["body"] = "Recommendation: run one bounded pilot."
	slide["statement_type"] = "recommendation"
	if err := validatePackagingStudioDeckCopyOutput(nil, &goalPlan{Objective: "Build the actual 2-slide deck"}, packagingStudioDeckCopyJSONForTest(t, value)); err != nil {
		t.Fatalf("one visibly owned statement type rejected: %v", err)
	}
	slide["headline"] = "Recommendation: run one bounded pilot."
	slide["thesis"] = slide["headline"]
	if err := validatePackagingStudioDeckCopyOutput(nil, &goalPlan{Objective: "Build the actual 2-slide deck"}, packagingStudioDeckCopyJSONForTest(t, value)); err == nil || !strings.Contains(err.Error(), "exactly one visibly labeled field") {
		t.Fatalf("duplicate statement owners escaped: %v", err)
	}
}

func TestPackagingStudioDeckCopyRequiresCountInferenceOnlyWhenNeeded(t *testing.T) {
	value := packagingStudioValidDeckCopyForTest()
	value["slide_count_inference"] = "Two slides are the shortest complete argument."
	if err := validatePackagingStudioDeckCopyOutput(nil, &goalPlan{Objective: "Build a presentation"}, packagingStudioDeckCopyJSONForTest(t, value)); err != nil {
		t.Fatalf("bounded inferred count rejected: %v", err)
	}
	value["slide_count_inference"] = ""
	if err := validatePackagingStudioDeckCopyOutput(nil, &goalPlan{Objective: "Build a presentation"}, packagingStudioDeckCopyJSONForTest(t, value)); err == nil || !strings.Contains(err.Error(), "disclose") {
		t.Fatalf("missing inferred count did not fail closed: %v", err)
	}
}

func TestPackagingStudioDeckCopyRejectsWrapperProse(t *testing.T) {
	body := packagingStudioDeckCopyJSONForTest(t, packagingStudioValidDeckCopyForTest())
	if err := validatePackagingStudioDeckCopyOutput(nil, &goalPlan{Objective: "Build the actual 2-slide deck"}, "Here is the deck:\n"+body); err == nil || !strings.Contains(err.Error(), "no prose") {
		t.Fatalf("wrapper prose escaped the exact deck-copy contract: %v", err)
	}
}
