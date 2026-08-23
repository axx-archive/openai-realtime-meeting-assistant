package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

var packagingStudioScopedRootKeys = map[string]bool{
	"evidence_scope": true, "evidence_scope_receipt": true,
	"decision_posture": true, "evidence_scope_disclosure": true,
}

var packagingStudioDeckSlideKeys = map[string]bool{
	"slide_id": true, "slide_kind": true, "thesis": true, "turn": true,
	"headline": true, "kicker": true, "body": true, "proof": true,
	"evidence_label": true, "source_label": true, "speaker_intent": true,
	"transition": true, "presenter_note": true, "claim_ids": true,
	"claim_renderings": true, "statement_type": true,
}

func packagingStudioContractString(object map[string]any, key, label string, allowEmpty bool, maxRunes int) (string, error) {
	raw, ok := object[key]
	if !ok {
		return "", fmt.Errorf("%s is missing %s", label, key)
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s %s must be a string", label, key)
	}
	value = strings.TrimSpace(value)
	if !allowEmpty && value == "" {
		return "", fmt.Errorf("%s %s must be non-empty", label, key)
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return "", fmt.Errorf("%s %s exceeds %d characters", label, key, maxRunes)
	}
	return value, nil
}

func packagingStudioContractStringArray(object map[string]any, key, label string, max int) ([]string, error) {
	raw, ok := object[key]
	values, okArray := raw.([]any)
	if !ok || !okArray || len(values) > max {
		return nil, fmt.Errorf("%s %s must be an array of at most %d strings", label, key, max)
	}
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for index, item := range values {
		value, ok := item.(string)
		value = strings.TrimSpace(value)
		if !ok || value == "" || seen[value] {
			return nil, fmt.Errorf("%s %s entry %d must be a unique non-empty string", label, key, index+1)
		}
		seen[value] = true
		out = append(out, value)
	}
	return out, nil
}

func packagingStudioWordCount(values ...string) int {
	count := 0
	for _, value := range values {
		count += len(strings.Fields(value))
	}
	return count
}

func packagingStudioStrictRawJSONObject(body, label string) (map[string]any, error) {
	body = strings.TrimSpace(body)
	if !strings.HasPrefix(body, "{") || !strings.HasSuffix(body, "}") {
		return nil, fmt.Errorf("%s must be exactly one JSON object with no prose or fence", label)
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	value, err := decodeUniqueJSONValue(decoder)
	if err != nil || ensureJSONEOF(decoder) != nil {
		return nil, fmt.Errorf("%s is malformed JSON", label)
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON object", label)
	}
	return root, nil
}

// validatePackagingStudioDeckCopyOutput is the deterministic premium-copy
// admission contract. It runs before the writer artifact is filed, so a loose
// outline cannot become the source of truth for identity, layout, or shipping.
func validatePackagingStudioDeckCopyOutput(app *kanbanBoardApp, plan *goalPlan, body string) error {
	root, err := packagingStudioStrictRawJSONObject(body, "deck_copy_v3")
	if err != nil {
		return err
	}
	for key := range root {
		if key != "slides" && key != "slide_count_inference" && !packagingStudioScopedRootKeys[key] {
			return fmt.Errorf("deck_copy_v3 contains undeclared root field %q", key)
		}
	}
	if _, ok := root["slides"]; !ok {
		return fmt.Errorf("deck_copy_v3 is missing slides")
	}
	inference, err := packagingStudioContractString(root, "slide_count_inference", "deck_copy_v3", true, 240)
	if err != nil {
		return err
	}
	if _, explicit := packagingRequestedSlideCount(plan.Objective); explicit {
		if inference != "" {
			return fmt.Errorf("deck_copy_v3 slide_count_inference must be empty for an explicit slide count")
		}
	} else if inference == "" {
		return fmt.Errorf("deck_copy_v3 must disclose its inferred slide count")
	}
	rawSlides, ok := root["slides"].([]any)
	if !ok || len(rawSlides) == 0 || len(rawSlides) > 40 {
		return fmt.Errorf("deck_copy_v3 slides must be a non-empty array of at most 40 slides")
	}
	if requested, explicit := packagingPlanSlideCount(app, plan); explicit && requested != len(rawSlides) {
		return fmt.Errorf("deck_copy_v3 has %d slides; the brief requires %d", len(rawSlides), requested)
	}
	seenIDs := map[string]bool{}
	for index, raw := range rawSlides {
		slide, ok := raw.(map[string]any)
		label := fmt.Sprintf("deck_copy_v3 slide %d", index+1)
		if !ok {
			return fmt.Errorf("%s must be an object", label)
		}
		for key := range slide {
			if !packagingStudioDeckSlideKeys[key] {
				return fmt.Errorf("%s contains undeclared field %q", label, key)
			}
		}
		for _, required := range []string{"slide_id", "slide_kind", "thesis", "turn", "headline", "kicker", "body", "proof", "evidence_label", "source_label", "speaker_intent", "transition", "presenter_note", "claim_ids", "claim_renderings"} {
			if _, exists := slide[required]; !exists {
				return fmt.Errorf("%s is missing %s", label, required)
			}
		}
		id, err := packagingStudioContractString(slide, "slide_id", label, false, 80)
		if err != nil || !deckIdentifierPattern.MatchString(id) || seenIDs[id] {
			return fmt.Errorf("%s slide_id must be a unique deck identifier", label)
		}
		seenIDs[id] = true
		kind, err := packagingStudioContractString(slide, "slide_kind", label, false, 16)
		if err != nil || !oneOf(kind, "cover", "normal", "evidence", "close") {
			return fmt.Errorf("%s slide_kind must be cover, normal, evidence, or close", label)
		}
		if index == 0 && kind != "cover" {
			return fmt.Errorf("deck_copy_v3 first slide must be the cover")
		}
		turn, err := packagingStudioContractString(slide, "turn", label, false, 16)
		if err != nil || !oneOf(turn, "open", "frame", "reveal", "prove", "contrast", "decide", "close") {
			return fmt.Errorf("%s turn is not admitted", label)
		}
		headline, err := packagingStudioContractString(slide, "headline", label, false, 160)
		if err != nil {
			return err
		}
		thesis, err := packagingStudioContractString(slide, "thesis", label, false, 160)
		if err != nil || thesis != headline {
			return fmt.Errorf("%s must carry one thesis exactly equal to its headline", label)
		}
		if packagingStudioWordCount(headline) > 12 {
			return fmt.Errorf("%s headline exceeds 12 words", label)
		}
		fields := map[string]string{}
		for _, key := range []string{"kicker", "body", "proof", "evidence_label", "source_label"} {
			fields[key], err = packagingStudioContractString(slide, key, label, true, 320)
			if err != nil {
				return err
			}
		}
		for _, key := range []string{"speaker_intent", "transition", "presenter_note"} {
			if _, err := packagingStudioContractString(slide, key, label, true, 900); err != nil {
				return err
			}
		}
		claimIDs, err := packagingStudioContractStringArray(slide, "claim_ids", label, 1)
		if err != nil {
			return err
		}
		claimRenderings, err := packagingStudioContractStringArray(slide, "claim_renderings", label, 1)
		if err != nil || len(claimIDs) != len(claimRenderings) {
			return fmt.Errorf("%s claim_ids and claim_renderings must be paired one-to-one", label)
		}
		if len(claimRenderings) == 1 {
			rendering := packagingGeneratedCanonicalText(claimRenderings[0])
			if strings.Contains(packagingGeneratedCanonicalText(fields["source_label"]), rendering) {
				return fmt.Errorf("%s source_label may cite the source but may not own or repeat the admitted claim rendering", label)
			}
			owners := 0
			for _, value := range []string{headline, fields["kicker"], fields["body"], fields["evidence_label"]} {
				if strings.Contains(packagingGeneratedCanonicalText(value), rendering) {
					owners++
				}
			}
			if owners != 1 {
				return fmt.Errorf("%s admitted claim rendering must appear in exactly one visible fact-bearing field", label)
			}
		}
		if fields["proof"] != "" && (fields["proof"] != fields["body"] || len(claimRenderings) != 1 || claimRenderings[0] != fields["proof"]) {
			return fmt.Errorf("%s proof must exactly equal its one admitted proof body", label)
		}
		if fields["source_label"] != "" && fields["evidence_label"] == "" {
			return fmt.Errorf("%s source_label requires decision-useful evidence_label", label)
		}
		if (fields["evidence_label"] != "" || fields["source_label"] != "") && len(claimIDs) != 1 {
			return fmt.Errorf("%s evidence furniture requires one admitted claim", label)
		}
		switch kind {
		case "cover":
			if fields["body"] != "" || fields["proof"] != "" || fields["evidence_label"] != "" || fields["source_label"] != "" || len(claimIDs) != 0 ||
				packagingStudioWordCount(headline, fields["kicker"]) > 16 {
				return fmt.Errorf("%s cover must be sparse: headline plus at most one short kicker", label)
			}
		case "normal", "close":
			if fields["kicker"] != "" && fields["body"] != "" {
				return fmt.Errorf("%s exceeds two primary visible text groups", label)
			}
			if packagingStudioWordCount(headline, fields["kicker"], fields["body"]) > 28 ||
				packagingStudioWordCount(headline, fields["kicker"], fields["body"], fields["evidence_label"], fields["source_label"]) > 36 {
				return fmt.Errorf("%s exceeds presentation-distance copy density", label)
			}
		case "evidence":
			if fields["kicker"] != "" || fields["proof"] == "" || fields["body"] == "" || len(claimIDs) != 1 {
				return fmt.Errorf("%s evidence slide requires one admitted proof body and no kicker", label)
			}
			if packagingStudioWordCount(headline, fields["body"], fields["evidence_label"], fields["source_label"]) > 44 {
				return fmt.Errorf("%s evidence slide exceeds presentation-distance copy density", label)
			}
		}
		if _, exists := slide["statement_type"]; exists {
			statementType, err := packagingStudioContractString(slide, "statement_type", label, false, 32)
			if err != nil {
				return err
			}
			if !oneOf(statementType, "recommendation", "proposal", "inference") {
				return fmt.Errorf("%s statement_type is not admitted", label)
			}
			owners := 0
			for _, value := range []string{headline, fields["kicker"], fields["body"], fields["evidence_label"], fields["source_label"]} {
				if packagingStudioForwardStatementOwner(value, statementType) {
					owners++
				}
			}
			if owners != 1 {
				return fmt.Errorf("%s statement_type must map to exactly one visibly labeled field", label)
			}
		}
	}
	return nil
}

func packagingStudioJSONNumber(value any) (float64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := number.Float64()
	return parsed, err == nil
}
