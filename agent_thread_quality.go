package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const minimumResearchArtifactWords = 1000

var (
	researchHeadingPattern = regexp.MustCompile(`(?m)^#{1,6}\s+(.+?)\s*$`)
	researchURLPattern     = regexp.MustCompile(`https://[^\s<>()\[\]"']+`)
	researchTablePattern   = regexp.MustCompile(`(?m)^\s*\|[^\n]+\|\s*\n\s*\|(?:\s*:?-{3,}:?\s*\|){2,}`)
	researchReceiptPattern = regexp.MustCompile(`(?m)<!--\s*stride-web-citation-receipt:v1 count=([0-9]+) domains=([0-9]+) searches=([0-9]+) response=([a-f0-9]{64}) digest=([a-f0-9]{64})\s*-->`)
)

var requiredResearchHeadings = []string{
	"executive summary",
	"thesis",
	"comparable companies",
	"evidence",
	"sources",
	"counterarguments",
	"recommendation",
	"open questions",
	"next checks",
	"worker evidence",
}

// validateAgentThreadTerminalArtifact is the last gate before a worker result
// can become complete/verified/passed. Research is intentionally strict: a
// plausible-looking plan, an undefined subject, or a source-free summary is a
// needs-attention result, never a completed report.
func validateAgentThreadTerminalArtifact(thread scoutAgentThread, body string) error {
	if normalizeAgentThreadMode(thread.Mode) != "research" {
		return nil
	}
	// Packaging Studio's external-research child is an evidence INPUT to the
	// story, not a standalone market report. Applying the general research brief
	// contract here forced an unrelated 1,000-word report, comparable-company
	// table, and five-source/three-domain quota even when the brief asked one
	// narrow credibility question. Keep the strict provider-backed citation gate,
	// but validate the server-bound stage against its focused evidence ledger.
	if agentThreadUsesExternalEvidenceContract(thread) {
		return validateExternalEvidenceArtifact(body)
	}
	body = strings.TrimSpace(body)
	var failures []string
	if len(strings.Fields(body)) < minimumResearchArtifactWords {
		failures = append(failures, fmt.Sprintf("report is shorter than %d words", minimumResearchArtifactWords))
	}

	headings := map[string]bool{}
	for _, match := range researchHeadingPattern.FindAllStringSubmatch(body, -1) {
		if len(match) == 2 {
			headings[strings.ToLower(strings.TrimSpace(strings.Trim(match[1], "#*_`")))] = true
		}
	}
	for _, required := range requiredResearchHeadings {
		if !headings[required] {
			failures = append(failures, "missing "+required+" section")
		}
	}
	if !regexp.MustCompile(`(?im)^\s*(?:\*\*)?search tags(?:\*\*)?\s*:`).MatchString(body) {
		failures = append(failures, "missing Search tags")
	}
	if !researchTablePattern.MatchString(body) {
		failures = append(failures, "missing comparable-company benchmark table")
	}

	receipt, receiptErr := verifiedResearchCitationReceipt(body)
	if receiptErr != nil {
		failures = append(failures, receiptErr.Error())
	}
	if receipt.CitationCount < 5 {
		failures = append(failures, "fewer than five cited HTTPS sources")
	}
	if receipt.DomainCount < 3 {
		failures = append(failures, "sources span fewer than three domains")
	}

	lower := strings.ToLower(body)
	for _, phrase := range []string{
		"target company is undefined",
		"blocked — target",
		"blocked: target",
		"cannot complete the research",
		"need the company name",
		"need more context before",
		"insufficient context to complete",
	} {
		if strings.Contains(lower, phrase) {
			failures = append(failures, "report is a blocked plan rather than completed research")
			break
		}
	}
	query := strings.ToLower(strings.TrimSpace(thread.Query))
	if strings.Contains(query, "elevator pitch") && !strings.Contains(lower, "elevator pitch") {
		failures = append(failures, "request asked for an elevator pitch but none was delivered")
	}
	if len(failures) > 0 {
		return fmt.Errorf("research quality gate rejected output: %s", strings.Join(failures, "; "))
	}
	return nil
}

var externalEvidenceLedgerColumns = []string{
	"research question",
	"source fact",
	"source title",
	"url",
	"published / updated",
	"units",
	"confidence",
	"deck implication",
}

const agentThreadFailureClassExternalEvidenceSyntax = "external_evidence_syntax"

// externalEvidenceSyntaxError marks a provider result whose evidence receipt
// can be valid while its presentation envelope is unusable. The durable goal
// engine treats this class as terminal-until-repaired instead of paying for
// the same hosted research again. Provenance, missing-field, and unsupported
// URL failures deliberately do not use this marker.
type externalEvidenceSyntaxError struct{ err error }

func (failure *externalEvidenceSyntaxError) Error() string {
	if failure == nil || failure.err == nil {
		return "external evidence syntax is invalid"
	}
	return failure.err.Error()
}

func (failure *externalEvidenceSyntaxError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.err
}

func isExternalEvidenceSyntaxFailure(err error) bool {
	var failure *externalEvidenceSyntaxError
	return errors.As(err, &failure)
}

func agentThreadFailureClass(err error) string {
	if isExternalEvidenceSyntaxFailure(err) {
		return agentThreadFailureClassExternalEvidenceSyntax
	}
	return ""
}

func stampAgentThreadFailureClass(metadata map[string]string, err error) {
	if metadata == nil {
		return
	}
	delete(metadata, "failureClass")
	if failureClass := agentThreadFailureClass(err); failureClass != "" {
		metadata["failureClass"] = failureClass
	}
}

type externalEvidenceEnvelope struct {
	ResearchQuestions    []string                      `json:"research_questions"`
	Evidence             []externalEvidenceEnvelopeRow `json:"evidence"`
	ExcludedOrUnverified []string                      `json:"excluded_or_unverified"`
}

type externalEvidenceEnvelopeRow struct {
	ResearchQuestion   string `json:"research_question"`
	SourceFact         string `json:"source_fact"`
	SourceTitle        string `json:"source_title"`
	URL                string `json:"url"`
	PublishedOrUpdated string `json:"published_or_updated"`
	Units              string `json:"units"`
	Confidence         string `json:"confidence"`
	DeckImplication    string `json:"deck_implication"`
}

func externalEvidenceJSONSchema() *openAIJSONSchema {
	stringField := func(maxLength int) map[string]any {
		return map[string]any{"type": "string", "minLength": 1, "maxLength": maxLength}
	}
	rowProperties := map[string]any{
		"research_question":    stringField(500),
		"source_fact":          stringField(2000),
		"source_title":         stringField(500),
		"url":                  map[string]any{"type": "string", "minLength": 9, "maxLength": 2048},
		"published_or_updated": stringField(120),
		"units":                stringField(120),
		"confidence":           map[string]any{"type": "string", "enum": []string{"High", "Medium", "Low"}},
		"deck_implication":     stringField(2000),
	}
	return &openAIJSONSchema{
		Name:        "external_evidence_v2",
		Description: "A source-bound evidence ledger that the server renders into the internal Packaging Studio artifact.",
		Schema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"research_questions", "evidence", "excluded_or_unverified"},
			"properties": map[string]any{
				"research_questions": map[string]any{"type": "array", "minItems": 1, "maxItems": 12, "items": stringField(500)},
				"evidence": map[string]any{
					"type": "array", "minItems": 1, "maxItems": 12,
					"items": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"required":             []string{"research_question", "source_fact", "source_title", "url", "published_or_updated", "units", "confidence", "deck_implication"},
						"properties":           rowProperties,
					},
				},
				"excluded_or_unverified": map[string]any{"type": "array", "maxItems": 40, "items": stringField(2000)},
			},
		},
	}
}

// externalEvidenceContractInstructions replaces the generic long-form research
// brief only for a process child whose parent, subtask, and server-authored
// output contract have all been bound. The provider still receives live hosted
// web search and appends an unforgeable source receipt after generation.
func externalEvidenceContractInstructions() string {
	return strings.Join([]string{
		"This is Packaging Studio's focused external-evidence contract.",
		"Answer only the credibility-critical research questions in the approved brief. This is an internal evidence ledger feeding a presentation, not a generic market report, work log, or comparable-company exercise.",
		"Use exactly these Markdown sections: Research questions, Verified evidence ledger, and Excluded or unverified.",
		"Under Verified evidence ledger, emit one Markdown table with exactly these columns: Research question | Source fact | Source title | URL | Published / updated | Units | Confidence | Deck implication.",
		"Each row must contain one externally verified source fact and one exact bare HTTPS URL actually fetched in this run. Use the publication/update date when stated; otherwise write the access date. Use explicit units or N/A. Confidence must be High, Medium, or Low. Keep inference out of Source fact and put the bounded interpretation only in Deck implication.",
		"Prefer current primary or official sources and return at most 12 decision-useful evidence items. Synthesize toward the few decisive, slide-usable proof points; do not turn search results into rows or pad toward a word count, source quota, domain quota, or comparables section. One decisive source is better than five irrelevant ones.",
		"Put anything not verified, contradictory, stale, or outside the brief under Excluded or unverified; write None only when that is true. Never invent a citation or claim. Do not emit a Scout source receipt; the server appends it from hosted-search evidence.",
	}, "\n")
}

func externalEvidenceV2ContractInstructions() string {
	return strings.Join([]string{
		"This is Packaging Studio's focused external-evidence contract.",
		"Answer only the credibility-critical research questions in the approved brief. This is an internal evidence ledger feeding a presentation, not a generic market report, work log, or comparable-company exercise.",
		"Return only the strict external_evidence_v2 JSON object requested by the response schema. Do not emit Markdown, a code fence, prose before or after the object, or a Scout source receipt.",
		"Every evidence item must populate all eight fields: research_question, source_fact, source_title, url, published_or_updated, units, confidence, and deck_implication. Copy research_question exactly from research_questions. Use one exact bare HTTPS URL actually fetched in this run.",
		"Use the publication/update date when stated; otherwise use the access date. Use explicit units or N/A. Confidence must be High, Medium, or Low. Keep inference out of source_fact and put the bounded interpretation only in deck_implication.",
		"Prefer current primary or official sources and return at most 12 decision-useful evidence items. Synthesize toward the few decisive, slide-usable proof points; do not turn search results into rows or pad toward a word count, source quota, domain quota, or comparables section. One decisive source is better than five irrelevant ones.",
		"Put anything not verified, contradictory, stale, or outside the brief in excluded_or_unverified; use an empty array only when there is nothing to exclude. Never invent a citation or claim. The server appends the hosted-search receipt, binds every URL to it, and renders the Verified evidence ledger only after validation.",
	}, "\n")
}

func validateExternalEvidenceArtifact(body string) error {
	body = strings.TrimSpace(body)
	var syntaxFailures []string
	var evidenceFailures []string
	headings := map[string]bool{}
	for _, match := range researchHeadingPattern.FindAllStringSubmatch(body, -1) {
		if len(match) == 2 {
			headings[strings.ToLower(strings.TrimSpace(strings.Trim(match[1], "#*_`")))] = true
		}
	}
	for _, required := range []string{"research questions", "verified evidence ledger", "excluded or unverified"} {
		if !headings[required] {
			syntaxFailures = append(syntaxFailures, "missing "+required+" section")
		}
	}

	receipt, receiptErr := verifiedResearchCitationReceipt(body)
	if receiptErr != nil {
		evidenceFailures = append(evidenceFailures, receiptErr.Error())
	} else if receipt.CitationCount < 1 || receipt.DomainCount < 1 || receipt.SearchCalls < 1 {
		evidenceFailures = append(evidenceFailures, "no provider-verified external source")
	}

	rows, tableErr := externalEvidenceLedgerRows(stripOpenAIWebCitationReceipt(body))
	if tableErr != nil {
		syntaxFailures = append(syntaxFailures, tableErr.Error())
	}
	if len(rows) > 12 {
		evidenceFailures = append(evidenceFailures, "verified evidence ledger has more than 12 decision-useful rows")
	}
	for index, row := range rows {
		rowNumber := index + 1
		for columnIndex, column := range externalEvidenceLedgerColumns {
			if strings.TrimSpace(row[columnIndex]) == "" {
				evidenceFailures = append(evidenceFailures, fmt.Sprintf("evidence row %d has empty %s", rowNumber, column))
			}
		}
		confidence := strings.ToLower(strings.TrimSpace(row[6]))
		if confidence != "high" && confidence != "medium" && confidence != "low" {
			evidenceFailures = append(evidenceFailures, fmt.Sprintf("evidence row %d confidence must be High, Medium, or Low", rowNumber))
		}
		matches := researchURLPattern.FindAllString(strings.TrimSpace(row[3]), -1)
		if len(matches) != 1 || strings.TrimSpace(row[3]) != matches[0] {
			evidenceFailures = append(evidenceFailures, fmt.Sprintf("evidence row %d must contain one bare HTTPS URL", rowNumber))
			continue
		}
		parsed, err := url.Parse(matches[0])
		if err != nil || !strings.EqualFold(parsed.Scheme, "https") || strings.TrimSpace(parsed.Hostname()) == "" {
			evidenceFailures = append(evidenceFailures, fmt.Sprintf("evidence row %d URL is invalid", rowNumber))
			continue
		}
		if receiptErr == nil && !receipt.CitationURLs[matches[0]] {
			evidenceFailures = append(evidenceFailures, fmt.Sprintf("evidence row %d URL is absent from the provider citation receipt", rowNumber))
		}
	}
	failures := append(append([]string(nil), syntaxFailures...), evidenceFailures...)
	if len(failures) > 0 {
		err := fmt.Errorf("external evidence quality gate rejected output: %s", strings.Join(failures, "; "))
		if len(syntaxFailures) > 0 && len(evidenceFailures) == 0 {
			return &externalEvidenceSyntaxError{err: err}
		}
		return err
	}
	return nil
}

func decodeExternalEvidenceEnvelope(body string) (externalEvidenceEnvelope, error) {
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	value, err := decodeUniqueJSONValue(decoder)
	if err != nil || ensureJSONEOF(decoder) != nil {
		return externalEvidenceEnvelope{}, &externalEvidenceSyntaxError{err: fmt.Errorf("external evidence JSON is invalid")}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return externalEvidenceEnvelope{}, &externalEvidenceSyntaxError{err: fmt.Errorf("external evidence JSON is invalid")}
	}
	var envelope externalEvidenceEnvelope
	strict := json.NewDecoder(strings.NewReader(string(raw)))
	strict.DisallowUnknownFields()
	if err := strict.Decode(&envelope); err != nil || ensureJSONEOF(strict) != nil {
		return externalEvidenceEnvelope{}, &externalEvidenceSyntaxError{err: fmt.Errorf("external evidence JSON does not match external_evidence_v2")}
	}
	return envelope, nil
}

func validateExternalEvidenceEnvelope(envelope externalEvidenceEnvelope, receipt researchCitationReceipt) error {
	var failures []string
	if len(envelope.ResearchQuestions) < 1 || len(envelope.ResearchQuestions) > 12 {
		failures = append(failures, "research_questions must contain 1 to 12 questions")
	}
	questions := make(map[string]bool, len(envelope.ResearchQuestions))
	for index, question := range envelope.ResearchQuestions {
		question = strings.TrimSpace(question)
		if question == "" || len(question) > 500 {
			failures = append(failures, fmt.Sprintf("research question %d is empty or too long", index+1))
			continue
		}
		if questions[question] {
			failures = append(failures, fmt.Sprintf("research question %d is duplicated", index+1))
		}
		questions[question] = true
	}
	if len(envelope.Evidence) < 1 || len(envelope.Evidence) > 12 {
		failures = append(failures, "evidence must contain 1 to 12 decision-useful rows")
	}
	for index, row := range envelope.Evidence {
		rowNumber := index + 1
		fields := []struct {
			name  string
			value string
			max   int
		}{
			{"research_question", row.ResearchQuestion, 500}, {"source_fact", row.SourceFact, 2000},
			{"source_title", row.SourceTitle, 500}, {"url", row.URL, 2048},
			{"published_or_updated", row.PublishedOrUpdated, 120}, {"units", row.Units, 120},
			{"confidence", row.Confidence, 12}, {"deck_implication", row.DeckImplication, 2000},
		}
		for _, field := range fields {
			if strings.TrimSpace(field.value) == "" || len(field.value) > field.max {
				failures = append(failures, fmt.Sprintf("evidence row %d has empty or oversized %s", rowNumber, field.name))
			}
		}
		if !questions[strings.TrimSpace(row.ResearchQuestion)] {
			failures = append(failures, fmt.Sprintf("evidence row %d research_question is not an exact listed question", rowNumber))
		}
		if strings.EqualFold(strings.TrimSpace(row.PublishedOrUpdated), "N/A") {
			failures = append(failures, fmt.Sprintf("evidence row %d published_or_updated must use the source or access date", rowNumber))
		}
		confidence := strings.ToLower(strings.TrimSpace(row.Confidence))
		if confidence != "high" && confidence != "medium" && confidence != "low" {
			failures = append(failures, fmt.Sprintf("evidence row %d confidence must be High, Medium, or Low", rowNumber))
		}
		rawURL := strings.TrimSpace(row.URL)
		matches := researchURLPattern.FindAllString(rawURL, -1)
		parsed, parseErr := url.Parse(rawURL)
		if len(matches) != 1 || matches[0] != rawURL || parseErr != nil || !strings.EqualFold(parsed.Scheme, "https") || strings.TrimSpace(parsed.Hostname()) == "" {
			failures = append(failures, fmt.Sprintf("evidence row %d must contain one valid bare HTTPS URL", rowNumber))
		} else if !receipt.CitationURLs[rawURL] {
			failures = append(failures, fmt.Sprintf("evidence row %d URL is absent from the provider citation receipt", rowNumber))
		}
		for _, value := range []string{row.ResearchQuestion, row.SourceFact, row.SourceTitle, row.PublishedOrUpdated, row.Units, row.Confidence, row.DeckImplication} {
			if len(researchURLPattern.FindAllString(value, -1)) > 0 {
				failures = append(failures, fmt.Sprintf("evidence row %d contains a URL outside its url field", rowNumber))
				break
			}
		}
	}
	if len(envelope.ExcludedOrUnverified) > 40 {
		failures = append(failures, "excluded_or_unverified contains more than 40 items")
	}
	for index, item := range envelope.ExcludedOrUnverified {
		if strings.TrimSpace(item) == "" || len(item) > 2000 {
			failures = append(failures, fmt.Sprintf("excluded_or_unverified item %d is empty or too long", index+1))
		}
	}
	if receipt.CitationCount < 1 || receipt.DomainCount < 1 || receipt.SearchCalls < 1 || len(receipt.CitationURLs) < 1 {
		failures = append(failures, "no provider-verified external source")
	}
	if len(failures) > 0 {
		return fmt.Errorf("external evidence quality gate rejected output: %s", strings.Join(failures, "; "))
	}
	return nil
}

func externalEvidenceMarkdownCell(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n"))
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "|", `\|`)
	return strings.ReplaceAll(value, "\n", "<br>")
}

func renderExternalEvidenceEnvelope(envelope externalEvidenceEnvelope) string {
	lines := []string{"## Research questions"}
	for _, question := range envelope.ResearchQuestions {
		lines = append(lines, "- "+externalEvidenceMarkdownCell(question))
	}
	lines = append(lines, "", "## Verified evidence ledger", "| Research question | Source fact | Source title | URL | Published / updated | Units | Confidence | Deck implication |", "|---|---|---|---|---|---|---|---|")
	for _, row := range envelope.Evidence {
		cells := []string{row.ResearchQuestion, row.SourceFact, row.SourceTitle, row.URL, row.PublishedOrUpdated, row.Units, row.Confidence, row.DeckImplication}
		for index := range cells {
			cells[index] = externalEvidenceMarkdownCell(cells[index])
		}
		lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
	}
	lines = append(lines, "", "## Excluded or unverified")
	if len(envelope.ExcludedOrUnverified) == 0 {
		lines = append(lines, "- None")
	} else {
		for _, item := range envelope.ExcludedOrUnverified {
			lines = append(lines, "- "+externalEvidenceMarkdownCell(item))
		}
	}
	return strings.Join(lines, "\n")
}

func normalizeExternalEvidenceArtifact(body string) (string, error) {
	body = strings.TrimSpace(body)
	receipt, err := verifiedResearchCitationReceipt(body)
	if err != nil {
		return "", fmt.Errorf("external evidence quality gate rejected output: %w", err)
	}
	envelope, err := decodeExternalEvidenceEnvelope(stripOpenAIWebCitationReceipt(body))
	if err != nil {
		return "", err
	}
	if err := validateExternalEvidenceEnvelope(envelope, receipt); err != nil {
		return "", err
	}
	location := openAIWebCitationReceiptBlockPattern.FindStringIndex(body)
	if location == nil {
		return "", fmt.Errorf("external evidence quality gate rejected output: missing exact provider web-search citation receipt")
	}
	normalized := strings.TrimSpace(renderExternalEvidenceEnvelope(envelope) + "\n\n" + strings.TrimSpace(body[location[0]:location[1]]))
	if err := validateExternalEvidenceArtifact(normalized); err != nil {
		return "", err
	}
	return normalized, nil
}

func externalEvidenceLedgerRows(body string) ([][]string, error) {
	lines := strings.Split(body, "\n")
	inSection := false
	foundHeader := false
	wantSeparator := false
	rows := make([][]string, 0)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			heading := strings.ToLower(strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
			if inSection && heading != "verified evidence ledger" {
				break
			}
			inSection = heading == "verified evidence ledger"
			continue
		}
		if !inSection || !strings.HasPrefix(trimmed, "|") || !strings.HasSuffix(trimmed, "|") {
			continue
		}
		cells := markdownEvidenceTableCells(trimmed)
		if !foundHeader {
			if len(cells) != len(externalEvidenceLedgerColumns) {
				continue
			}
			matches := true
			for index := range cells {
				if strings.ToLower(cells[index]) != externalEvidenceLedgerColumns[index] {
					matches = false
					break
				}
			}
			if matches {
				foundHeader = true
				wantSeparator = true
			}
			continue
		}
		if wantSeparator {
			wantSeparator = false
			valid := len(cells) == len(externalEvidenceLedgerColumns)
			for _, cell := range cells {
				separator := strings.Trim(strings.TrimSpace(cell), ":")
				if len(separator) < 3 || strings.Trim(separator, "-") != "" {
					valid = false
				}
			}
			if !valid {
				return nil, fmt.Errorf("verified evidence ledger has no valid Markdown separator row")
			}
			continue
		}
		if len(cells) != len(externalEvidenceLedgerColumns) {
			return nil, fmt.Errorf("verified evidence ledger row has %d columns, want %d", len(cells), len(externalEvidenceLedgerColumns))
		}
		rows = append(rows, cells)
	}
	if !foundHeader {
		return nil, fmt.Errorf("missing verified evidence ledger table with the required columns")
	}
	if wantSeparator {
		return nil, fmt.Errorf("verified evidence ledger has no Markdown separator row")
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("verified evidence ledger has no evidence rows")
	}
	return rows, nil
}

func markdownEvidenceTableCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	cells := make([]string, 0, len(externalEvidenceLedgerColumns))
	var cell strings.Builder
	escaped := false
	for index := 0; index < len(line); index++ {
		character := line[index]
		if escaped {
			if character != '|' && character != '\\' {
				cell.WriteByte('\\')
			}
			cell.WriteByte(character)
			escaped = false
			continue
		}
		if character == '\\' {
			escaped = true
			continue
		}
		if character == '|' {
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
			continue
		}
		cell.WriteByte(character)
	}
	if escaped {
		cell.WriteByte('\\')
	}
	cells = append(cells, strings.TrimSpace(cell.String()))
	return cells
}

type researchCitationReceipt struct {
	CitationCount int
	DomainCount   int
	SearchCalls   int
	CitationURLs  map[string]bool
}

func verifiedResearchCitationReceipt(body string) (researchCitationReceipt, error) {
	match := researchReceiptPattern.FindStringSubmatch(body)
	if len(match) != 6 || len(researchReceiptPattern.FindAllStringSubmatch(body, -1)) != 1 {
		return researchCitationReceipt{}, fmt.Errorf("missing exact provider web-search citation receipt")
	}
	heading := strings.LastIndex(body[:strings.Index(body, match[0])], "## Scout source receipt")
	if heading < 0 {
		return researchCitationReceipt{}, fmt.Errorf("missing exact provider web-search citation receipt")
	}
	section := body[heading:strings.Index(body, match[0])]
	urls := make([]string, 0)
	domains := map[string]bool{}
	for _, raw := range researchURLPattern.FindAllString(section, -1) {
		raw = strings.TrimRight(raw, ".,;:!?")
		parsed, err := url.Parse(raw)
		if err != nil || !strings.EqualFold(parsed.Scheme, "https") || strings.TrimSpace(parsed.Hostname()) == "" {
			continue
		}
		urls = append(urls, raw)
		domains[strings.ToLower(parsed.Hostname())] = true
	}
	count, countErr := strconv.Atoi(match[1])
	domainCount, domainErr := strconv.Atoi(match[2])
	searchCalls, searchErr := strconv.Atoi(match[3])
	if countErr != nil || domainErr != nil || searchErr != nil || count != len(urls) || domainCount != len(domains) || searchCalls < 1 || match[4] == strings.Repeat("0", 64) || match[5] != sha256Hex([]byte(strings.Join(urls, "\n"))) {
		return researchCitationReceipt{}, fmt.Errorf("provider web-search citation receipt is invalid")
	}
	urlSet := make(map[string]bool, len(urls))
	for _, citedURL := range urls {
		urlSet[citedURL] = true
	}
	return researchCitationReceipt{CitationCount: count, DomainCount: domainCount, SearchCalls: searchCalls, CitationURLs: urlSet}, nil
}

func researchArtifactEvidenceMetadata(thread scoutAgentThread, body string) map[string]string {
	if normalizeAgentThreadMode(thread.Mode) != "research" {
		return nil
	}
	receipt, err := verifiedResearchCitationReceipt(body)
	if err != nil {
		return nil
	}
	return map[string]string{
		"researchQualityGate":        "passed",
		"researchWordCount":          fmt.Sprintf("%d", len(strings.Fields(body))),
		"researchCitationCount":      fmt.Sprintf("%d", receipt.CitationCount),
		"researchSourceDomainCount":  fmt.Sprintf("%d", receipt.DomainCount),
		"researchWebSearchCallCount": fmt.Sprintf("%d", receipt.SearchCalls),
		"researchSourceWindowDigest": strings.TrimSpace(thread.Artifact.Metadata["sourceWindowDigest"]),
	}
}
