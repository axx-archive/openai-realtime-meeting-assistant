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

type researchCitationReceipt struct {
	CitationCount int
	DomainCount   int
	SearchCalls   int
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
	return researchCitationReceipt{CitationCount: count, DomainCount: domainCount, SearchCalls: searchCalls}, nil
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
