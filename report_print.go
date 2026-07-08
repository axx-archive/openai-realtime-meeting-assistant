package main

// report_print.go — the branded BonfireOS print document for markdown
// research reports ("Download PDF" for research briefs). The export trigger
// (artifactExportPDFHandler) has always shipped deck/paper-kit HTML straight
// to the render sidecar; a research report is a MARKDOWN os_artifact body
// (the research_brief_v2 contract: Executive Summary, Thesis, Evidence,
// Sources, Counterarguments, Recommendation, Open questions, Next checks,
// Worker evidence, plus a "Search tags:" line and a "**Gate result:** ..."
// preamble), so the server converts it here into a self-contained print
// document and ships it down the text-native paper path — chromium prints it
// direct, no flatten, text stays selectable.
//
// SECURITY: the body is model text. Every artifact-derived span is
// html.EscapeString-ed BEFORE it is wrapped in a tag (the
// renderDealRoomBinderHTML law), so injected HTML/script can never execute in
// the print sandbox. The converter mirrors the SAME markdown subset the
// client reader supports (appendArtifactBodyNodes/appendArtifactInlineNodes
// in index.html): headings, pipe tables, lists, blockquotes, lone --- rules,
// [label](https://…) links, **bold**, and `inline code` — link hrefs are
// https?:// only by construction, so a javascript: URI stays literal text.
// All CSS is inline and light-only: this is a print deliverable, not a
// themed surface.

import (
	"html"
	"regexp"
	"strings"
	"time"
)

var (
	// The client reader's inline grammar, verbatim: link + bold + code, no
	// bare-URL group (appendArtifactInlineNodes).
	reportPrintInlinePattern = regexp.MustCompile("\\[([^\\]\\n]{1,140})\\]\\((https?://[^\\s)]+)\\)|\\*\\*([^*\\n]+)\\*\\*|`([^`\\n]+)`")

	reportPrintHeadingPattern  = regexp.MustCompile(`^\s*(#{1,6})\s+(.+)$`)
	reportPrintListPattern     = regexp.MustCompile(`^\s*(?:[-*]|\d+\.)\s+(.+)$`)
	reportPrintOrderedPattern  = regexp.MustCompile(`^\s*\d+\.`)
	reportPrintQuotePattern    = regexp.MustCompile(`^\s*>\s?(.*)$`)
	reportPrintQuoteStart      = regexp.MustCompile(`^\s*>\s*\S`)
	reportPrintRulePattern     = regexp.MustCompile(`^\s*-{3,}\s*$`)
	reportPrintTableRowPattern = regexp.MustCompile(`^\s*\|.*\|\s*$`)
	reportPrintTableSepPattern = regexp.MustCompile(`^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)+\|?\s*$`)

	// The two contract preamble lines the masthead absorbs: the gate verdict
	// becomes the meta strip, the search tags become chips — neither repeats
	// in the body flow.
	reportPrintGatePattern = regexp.MustCompile(`(?i)^\s*\*\*gate result:?\*\*:?\s*(.+?)\s*$`)
	reportPrintTagsPattern = regexp.MustCompile(`(?i)^\s*(?:\*\*)?search tags:?(?:\*\*)?:?\s*(.+?)\s*$`)
)

// renderResearchReportPrintHTML assembles the complete branded print document
// for one markdown research artifact: BonfireOS masthead (pure-glyph flame
// mark + wordmark + mono kicker), title, meta line (date · requested by ·
// model/worker), gate-result strip, search-tag chips, the converted sections,
// and the Scout colophon footer.
func renderResearchReportPrintHTML(artifact meetingMemoryEntry) string {
	title := firstNonEmptyString(artifact.Metadata["title"], "Research brief")
	created := artifact.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	date := created.Format("January 2, 2006")
	requestedBy := firstNonEmptyString(artifact.Metadata["requestedBy"], artifact.Metadata["createdBy"])
	worker := firstNonEmptyString(artifact.Metadata["model"], artifact.Metadata["orchestratorModel"], artifact.Metadata["worker"])

	body, gateResult, searchTags := splitResearchReportPreamble(artifact.Text)
	if gateResult == "" {
		if reviewGate := strings.TrimSpace(artifact.Metadata["reviewGate"]); reviewGate != "" && reviewGate != "pending" {
			gateResult = reviewGate
		}
	}

	var page strings.Builder
	page.WriteString("<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\">")
	page.WriteString("<title>" + html.EscapeString(title) + " · BonfireOS</title>")
	page.WriteString("<style>" + reportPrintCSS + "</style></head><body>")

	page.WriteString("<header class=\"masthead\">")
	page.WriteString("<div class=\"brand\"><span class=\"mark\" aria-hidden=\"true\">▲</span>")
	page.WriteString("<span class=\"wordmark\">BonfireOS</span>")
	page.WriteString("<span class=\"kicker\">RESEARCH BRIEF</span></div>")
	page.WriteString("<h1 class=\"title\">" + html.EscapeString(title) + "</h1>")
	page.WriteString("<div class=\"meta\"><span>" + html.EscapeString(date) + "</span>")
	if requestedBy != "" {
		page.WriteString("<span>Requested by " + html.EscapeString(requestedBy) + "</span>")
	}
	if worker != "" {
		page.WriteString("<span>" + html.EscapeString(worker) + "</span>")
	}
	page.WriteString("</div>")
	if gateResult != "" {
		page.WriteString("<div class=\"gate\"><span class=\"gate-label\">Gate result</span>" + reportPrintInlineHTML(gateResult) + "</div>")
	}
	if len(searchTags) > 0 {
		page.WriteString("<div class=\"tags\">")
		for _, tag := range searchTags {
			page.WriteString("<span class=\"tag\">" + html.EscapeString(tag) + "</span>")
		}
		page.WriteString("</div>")
	}
	page.WriteString("</header>")

	page.WriteString("<main class=\"report\">" + renderResearchReportBodyHTML(body) + "</main>")

	page.WriteString("<footer class=\"colophon\">Generated by Scout · BonfireOS · thebonfire.xyz · " + html.EscapeString(date) + "</footer>")
	page.WriteString("</body></html>")
	return page.String()
}

// splitResearchReportPreamble lifts the first "**Gate result:** ..." line and
// the first "Search tags: ..." line out of the body — the masthead renders
// both — and returns the remaining markdown untouched.
func splitResearchReportPreamble(body string) (remaining string, gateResult string, searchTags []string) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	lines := strings.Split(body, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if gateResult == "" {
			if match := reportPrintGatePattern.FindStringSubmatch(line); match != nil {
				gateResult = strings.TrimSpace(match[1])
				continue
			}
		}
		if searchTags == nil {
			if match := reportPrintTagsPattern.FindStringSubmatch(line); match != nil {
				for _, tag := range strings.Split(match[1], ",") {
					if tag = strings.TrimSpace(tag); tag != "" {
						searchTags = append(searchTags, tag)
					}
				}
				continue
			}
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n"), gateResult, searchTags
}

// renderResearchReportBodyHTML converts the markdown body to print HTML,
// section by section: every heading closes the previous <section> and opens
// the next, so page-break-inside:avoid keeps a heading with its content.
// Block grammar mirrors the client reader exactly (table before rule, rule
// before list, list before quote, heading, paragraph).
func renderResearchReportBodyHTML(body string) string {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n"), "\n")

	var out strings.Builder
	sectionOpen := false
	openSection := func() {
		if !sectionOpen {
			out.WriteString("<section>")
			sectionOpen = true
		}
	}
	closeSection := func() {
		if sectionOpen {
			out.WriteString("</section>")
			sectionOpen = false
		}
	}
	var paragraph []string
	flushParagraph := func() {
		value := strings.TrimSpace(strings.Join(paragraph, "\n"))
		paragraph = paragraph[:0]
		if value == "" {
			return
		}
		openSection()
		out.WriteString("<p>" + reportPrintInlineHTML(value) + "</p>")
	}

	index := 0
	for index < len(lines) {
		line := lines[index]
		if strings.TrimSpace(line) == "" {
			flushParagraph()
			index++
			continue
		}
		if reportPrintTableRowPattern.MatchString(line) && index+1 < len(lines) && reportPrintTableSepPattern.MatchString(lines[index+1]) {
			flushParagraph()
			tableLines := []string{}
			for index < len(lines) && reportPrintTableRowPattern.MatchString(lines[index]) {
				tableLines = append(tableLines, lines[index])
				index++
			}
			openSection()
			out.WriteString(reportPrintTableHTML(tableLines))
			continue
		}
		if reportPrintRulePattern.MatchString(line) {
			flushParagraph()
			openSection()
			out.WriteString("<hr>")
			index++
			continue
		}
		if match := reportPrintListPattern.FindStringSubmatch(line); match != nil {
			flushParagraph()
			ordered := reportPrintOrderedPattern.MatchString(line)
			tag := "ul"
			if ordered {
				tag = "ol"
			}
			openSection()
			out.WriteString("<" + tag + ">")
			for index < len(lines) {
				next := reportPrintListPattern.FindStringSubmatch(lines[index])
				if next == nil || reportPrintOrderedPattern.MatchString(lines[index]) != ordered {
					break
				}
				out.WriteString("<li>" + reportPrintInlineHTML(strings.TrimSpace(next[1])) + "</li>")
				index++
			}
			out.WriteString("</" + tag + ">")
			continue
		}
		if reportPrintQuoteStart.MatchString(line) {
			flushParagraph()
			quoteLines := []string{}
			for index < len(lines) {
				match := reportPrintQuotePattern.FindStringSubmatch(lines[index])
				if match == nil {
					break
				}
				quoteLines = append(quoteLines, match[1])
				index++
			}
			openSection()
			out.WriteString("<blockquote>" + reportPrintInlineHTML(strings.TrimSpace(strings.Join(quoteLines, "\n"))) + "</blockquote>")
			continue
		}
		if match := reportPrintHeadingPattern.FindStringSubmatch(line); match != nil {
			flushParagraph()
			closeSection()
			openSection()
			tag := "h4"
			switch len(match[1]) {
			case 1, 2:
				tag = "h2"
			case 3:
				tag = "h3"
			}
			out.WriteString("<" + tag + ">" + reportPrintInlineHTML(strings.TrimSuffix(strings.TrimSpace(match[2]), ":")) + "</" + tag + ">")
			index++
			continue
		}
		paragraph = append(paragraph, line)
		index++
	}
	flushParagraph()
	closeSection()
	return out.String()
}

// reportPrintTableHTML renders one pipe table: first row is the header, the
// second (separator) row is skipped, and every body row follows the header's
// cell count — exactly the client's artifactTableNode.
func reportPrintTableHTML(lines []string) string {
	rows := make([][]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimPrefix(trimmed, "|")
		trimmed = strings.TrimSuffix(trimmed, "|")
		cells := strings.Split(trimmed, "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		return ""
	}
	headers := rows[0]

	var out strings.Builder
	out.WriteString("<table><thead><tr>")
	for _, header := range headers {
		out.WriteString("<th>" + reportPrintInlineHTML(header) + "</th>")
	}
	out.WriteString("</tr></thead><tbody>")
	if len(rows) > 2 {
		for _, row := range rows[2:] {
			out.WriteString("<tr>")
			for cellIndex := range headers {
				cell := ""
				if cellIndex < len(row) {
					cell = row[cellIndex]
				}
				out.WriteString("<td>" + reportPrintInlineHTML(cell) + "</td>")
			}
			out.WriteString("</tr>")
		}
	}
	out.WriteString("</tbody></table>")
	return out.String()
}

// reportPrintInlineHTML renders one span of model text: the raw text is
// scanned for the client reader's inline grammar and EVERY emitted segment —
// plain runs, labels, hrefs, bold, code — is html.EscapeString-ed before it
// gets structure. The href group only ever matches https?:// by construction.
func reportPrintInlineHTML(text string) string {
	var out strings.Builder
	last := 0
	for _, match := range reportPrintInlinePattern.FindAllStringSubmatchIndex(text, -1) {
		out.WriteString(html.EscapeString(text[last:match[0]]))
		switch {
		case match[2] >= 0 && match[4] >= 0:
			out.WriteString("<a href=\"" + html.EscapeString(text[match[4]:match[5]]) + "\">" + html.EscapeString(text[match[2]:match[3]]) + "</a>")
		case match[6] >= 0:
			out.WriteString("<strong>" + html.EscapeString(text[match[6]:match[7]]) + "</strong>")
		case match[8] >= 0:
			out.WriteString("<code>" + html.EscapeString(text[match[8]:match[9]]) + "</code>")
		}
		last = match[1]
	}
	out.WriteString(html.EscapeString(text[last:]))
	return out.String()
}

// reportPrintCSS is the whole print stylesheet, inline in the document (the
// sidecar's CSP blocks every fetch). Light-only print typography: a system
// stack close to Google Sans Flex, 11pt body on a 68ch measure, hairline
// tables, sections that avoid breaking mid-block, headings that keep their
// content. The ember (#FF6B4A) appears exactly twice — the flame mark and
// the gate strip's rule — the earned-accent law in print.
const reportPrintCSS = `:root{color-scheme:light}
*{margin:0;padding:0;box-sizing:border-box}
@page{size:letter;margin:18mm 16mm 20mm}
body{font-family:"Google Sans Flex",-apple-system,"Segoe UI",sans-serif;font-size:11pt;line-height:1.55;color:#1a1d23;background:#fff;-webkit-print-color-adjust:exact;print-color-adjust:exact}
.masthead{padding-bottom:14pt;border-bottom:1.5pt solid #1a1d23;margin-bottom:16pt}
.brand{display:flex;align-items:baseline;gap:5pt;margin-bottom:16pt}
.brand .mark{color:#ff6b4a;font-size:9pt;line-height:1}
.brand .wordmark{font-weight:700;font-size:10.5pt;letter-spacing:.01em}
.brand .kicker{margin-left:auto;font-family:ui-monospace,"SF Mono",Menlo,monospace;font-size:7.5pt;letter-spacing:.18em;color:#6a7180}
.title{font-size:22pt;line-height:1.15;font-weight:700;letter-spacing:-.015em;max-width:36ch}
.meta{margin-top:8pt;font-family:ui-monospace,"SF Mono",Menlo,monospace;font-size:8pt;letter-spacing:.04em;color:#6a7180;font-variant-numeric:tabular-nums}
.meta span+span::before{content:"·";margin:0 6pt;color:#c3c9d4}
.gate{margin-top:10pt;padding:6pt 9pt;border-left:2pt solid #ff6b4a;background:#f7f8fa;font-family:ui-monospace,"SF Mono",Menlo,monospace;font-size:8pt;line-height:1.5;color:#3c424e;page-break-inside:avoid;break-inside:avoid}
.gate-label{display:block;font-size:6.5pt;letter-spacing:.16em;text-transform:uppercase;color:#6a7180;margin-bottom:2pt}
.tags{margin-top:9pt}
.tag{display:inline-block;font-family:ui-monospace,"SF Mono",Menlo,monospace;font-size:7.5pt;color:#6a7180;border:.5pt solid #d8dce4;border-radius:3pt;padding:1.5pt 5pt;margin:0 3pt 3pt 0}
.report{max-width:68ch}
.report section{page-break-inside:avoid;break-inside:avoid;margin:0 0 6pt}
.report h2{font-size:13pt;line-height:1.25;font-weight:700;letter-spacing:-.005em;margin:14pt 0 6pt;page-break-after:avoid;break-after:avoid}
.report h3{font-size:11.5pt;line-height:1.3;font-weight:700;margin:12pt 0 5pt;page-break-after:avoid;break-after:avoid}
.report h4{font-size:10.5pt;line-height:1.3;font-weight:700;color:#3c424e;margin:10pt 0 4pt;page-break-after:avoid;break-after:avoid}
.report p{margin:0 0 7pt}
.report ul,.report ol{margin:0 0 8pt;padding-left:16pt}
.report li{margin:0 0 3pt}
.report blockquote{margin:0 0 8pt;padding:3pt 10pt;border-left:1.5pt solid #d8dce4;color:#4c5261}
.report code{font-family:ui-monospace,"SF Mono",Menlo,monospace;font-size:9.5pt;background:#f1f3f7;border-radius:2pt;padding:.5pt 3pt}
.report a{color:#1a1d23;text-decoration:underline;text-decoration-thickness:.5pt;text-underline-offset:2pt}
.report hr{border:0;border-top:.5pt solid #d8dce4;margin:12pt 0}
.report table{border-collapse:collapse;width:100%;font-size:9pt;line-height:1.45;margin:2pt 0 10pt;page-break-inside:avoid;break-inside:avoid;font-variant-numeric:tabular-nums}
.report th{text-align:left;font-size:7.5pt;letter-spacing:.06em;text-transform:uppercase;color:#6a7180;border-bottom:1pt solid #1a1d23;padding:3pt 8pt 3pt 0}
.report td{border-bottom:.5pt solid #d8dce4;padding:3.5pt 8pt 3.5pt 0;vertical-align:top}
.colophon{margin-top:22pt;padding-top:8pt;border-top:.5pt solid #d8dce4;font-family:ui-monospace,"SF Mono",Menlo,monospace;font-size:7.5pt;letter-spacing:.1em;text-transform:uppercase;color:#6a7180}`
