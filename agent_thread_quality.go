package main

import (
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
		"Prefer current primary or official sources. Do not pad toward a word count, source quota, domain quota, or comparables section. One decisive source is better than five irrelevant ones.",
		"Put anything not verified, contradictory, stale, or outside the brief under Excluded or unverified; write None only when that is true. Never invent a citation or claim. Do not emit a Scout source receipt; the server appends it from hosted-search evidence.",
	}, "\n")
}

func validateExternalEvidenceArtifact(body string) error {
	body = strings.TrimSpace(body)
	var failures []string
	headings := map[string]bool{}
	for _, match := range researchHeadingPattern.FindAllStringSubmatch(body, -1) {
		if len(match) == 2 {
			headings[strings.ToLower(strings.TrimSpace(strings.Trim(match[1], "#*_`")))] = true
		}
	}
	for _, required := range []string{"research questions", "verified evidence ledger", "excluded or unverified"} {
		if !headings[required] {
			failures = append(failures, "missing "+required+" section")
		}
	}

	receipt, receiptErr := verifiedResearchCitationReceipt(body)
	if receiptErr != nil {
		failures = append(failures, receiptErr.Error())
	} else if receipt.CitationCount < 1 || receipt.DomainCount < 1 || receipt.SearchCalls < 1 {
		failures = append(failures, "no provider-verified external source")
	}

	rows, tableErr := externalEvidenceLedgerRows(stripOpenAIWebCitationReceipt(body))
	if tableErr != nil {
		failures = append(failures, tableErr.Error())
	}
	for index, row := range rows {
		rowNumber := index + 1
		for columnIndex, column := range externalEvidenceLedgerColumns {
			if strings.TrimSpace(row[columnIndex]) == "" {
				failures = append(failures, fmt.Sprintf("evidence row %d has empty %s", rowNumber, column))
			}
		}
		confidence := strings.ToLower(strings.TrimSpace(row[6]))
		if confidence != "high" && confidence != "medium" && confidence != "low" {
			failures = append(failures, fmt.Sprintf("evidence row %d confidence must be High, Medium, or Low", rowNumber))
		}
		matches := researchURLPattern.FindAllString(strings.TrimSpace(row[3]), -1)
		if len(matches) != 1 || strings.TrimSpace(row[3]) != matches[0] {
			failures = append(failures, fmt.Sprintf("evidence row %d must contain one bare HTTPS URL", rowNumber))
			continue
		}
		parsed, err := url.Parse(matches[0])
		if err != nil || !strings.EqualFold(parsed.Scheme, "https") || strings.TrimSpace(parsed.Hostname()) == "" {
			failures = append(failures, fmt.Sprintf("evidence row %d URL is invalid", rowNumber))
			continue
		}
		if receiptErr == nil && !receipt.CitationURLs[matches[0]] {
			failures = append(failures, fmt.Sprintf("evidence row %d URL is absent from the provider citation receipt", rowNumber))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("external evidence quality gate rejected output: %s", strings.Join(failures, "; "))
	}
	return nil
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
	raw := strings.Split(line, "|")
	cells := make([]string, len(raw))
	for index := range raw {
		cells[index] = strings.TrimSpace(raw[index])
	}
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
