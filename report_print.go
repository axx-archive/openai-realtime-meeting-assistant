package main

// report_print.go — the branded Stride print document for markdown
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
// native document editor supports: headings, escaped-pipe tables, mixed
// nested lists, blockquotes, lone --- rules, safe links, bold/italic/strike,
// inline code, hard breaks, and images. Links are http(s)/mailto only, while
// images are either bounded data bytes, an artifact-bound local blob expanded
// to data:, or a non-fetching semantic reference for an external URL. A
// javascript: URI therefore stays literal text and PDF creation never becomes
// an SSRF/exfiltration path. All CSS is inline and light-only: this is a print
// deliverable, not a themed surface.

import (
	"encoding/base64"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	// Document Studio's safe inline Markdown grammar. Images deliberately
	// accept only the schemes the native editor can create, plus bounded data
	// images for already-self-contained artifacts. The renderer below never
	// fetches a URL: attached/data images become data URIs and web images become
	// a semantic source card.
	reportPrintInlinePattern = regexp.MustCompile(`!\[(?P<image_alt>[^\]\n]{0,500})\]\((?P<image_src>(?:(?:https?://|/)[^\s<>)]+|data:image/(?:png|jpeg|gif|webp);base64,[A-Za-z0-9+/=]+))\)|\[(?P<link_label>[^\]\n]{1,140})\]\((?P<link_href>(?:https?://|mailto:)[^\s)]+)\)|\*\*(?P<strong_star>[^*\n]+)\*\*|__(?P<strong_under>[^_\n]+)__|\*(?P<em_star>[^*\n]+)\*|_(?P<em_under>[^_\n]+)_|\x60(?P<code>[^\x60\n]+)\x60|~~(?P<strike>[^~\n]+)~~`)

	reportPrintImageAltGroup   = reportPrintInlinePattern.SubexpIndex("image_alt")
	reportPrintImageSrcGroup   = reportPrintInlinePattern.SubexpIndex("image_src")
	reportPrintLinkLabelGroup  = reportPrintInlinePattern.SubexpIndex("link_label")
	reportPrintLinkHrefGroup   = reportPrintInlinePattern.SubexpIndex("link_href")
	reportPrintStrongStarGroup = reportPrintInlinePattern.SubexpIndex("strong_star")
	reportPrintStrongUndGroup  = reportPrintInlinePattern.SubexpIndex("strong_under")
	reportPrintEmStarGroup     = reportPrintInlinePattern.SubexpIndex("em_star")
	reportPrintEmUndGroup      = reportPrintInlinePattern.SubexpIndex("em_under")
	reportPrintCodeGroup       = reportPrintInlinePattern.SubexpIndex("code")
	reportPrintStrikeGroup     = reportPrintInlinePattern.SubexpIndex("strike")

	reportPrintHeadingPattern  = regexp.MustCompile(`^\s*(#{1,6})\s+(.+)$`)
	reportPrintListPattern     = regexp.MustCompile(`^(\s*)([-+*]|(\d+)[.)])\s+(.+)$`)
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

const (
	reportPrintMaxInlineImageBytes = 8 << 20
	reportPrintMaxImageBytes       = 16 << 20
	reportPrintMaxImages           = 24
)

var reportPrintSafeImageMIME = map[string]bool{
	"image/gif":  true,
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
}

// reportPrintRenderer owns the one stateful part of Markdown conversion:
// the bounded allowance for image bytes. Every source remains local and
// artifact-bound; no print conversion path has a network client.
type reportPrintRenderer struct {
	images         map[string]artifactAsset
	expandedBytes  int
	expandedImages int
}

func newReportPrintRenderer(artifact meetingMemoryEntry) *reportPrintRenderer {
	renderer := &reportPrintRenderer{images: map[string]artifactAsset{}}
	for _, asset := range artifactAssets(artifact) {
		if artifactAssetIsEditableImage(asset) {
			renderer.images[asset.Ref] = asset
		}
	}
	return renderer
}

// renderResearchReportPrintHTML assembles the complete branded print document
// for one markdown research artifact: Stride masthead
// mark + wordmark + mono kicker), title, meta line (date · requested by ·
// model/worker), gate-result strip, search-tag chips, the converted sections,
// and the Scout colophon footer.
func renderResearchReportPrintHTML(artifact meetingMemoryEntry) string {
	renderer := newReportPrintRenderer(artifact)
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
	page.WriteString("<title>" + html.EscapeString(title) + " · Stride</title>")
	page.WriteString("<style>" + reportPrintCSS + "</style></head><body>")

	page.WriteString("<header class=\"masthead\">")
	page.WriteString("<div class=\"brand\"><span class=\"mark\" aria-hidden=\"true\">▲</span>")
	page.WriteString("<span class=\"wordmark\">Stride</span>")
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
		page.WriteString("<div class=\"gate\"><span class=\"gate-label\">Gate result</span>" + renderer.inlineHTML(gateResult) + "</div>")
	}
	if len(searchTags) > 0 {
		page.WriteString("<div class=\"tags\">")
		for _, tag := range searchTags {
			page.WriteString("<span class=\"tag\">" + html.EscapeString(tag) + "</span>")
		}
		page.WriteString("</div>")
	}
	page.WriteString("</header>")

	page.WriteString("<main class=\"report\">" + renderer.bodyHTML(body) + "</main>")

	page.WriteString("<footer class=\"colophon\">Generated by Scout · Stride · workinstride.xyz · " + html.EscapeString(date) + "</footer>")
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
	return newReportPrintRenderer(meetingMemoryEntry{}).bodyHTML(body)
}

func (renderer *reportPrintRenderer) bodyHTML(body string) string {
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
		out.WriteString("<p>" + renderer.inlineHTML(value) + "</p>")
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
			out.WriteString(renderer.tableHTML(tableLines))
			continue
		}
		if reportPrintRulePattern.MatchString(line) {
			flushParagraph()
			openSection()
			out.WriteString("<hr>")
			index++
			continue
		}
		if _, ok := parseReportPrintListLine(line); ok {
			flushParagraph()
			openSection()
			listHTML, nextIndex := renderer.listHTML(lines, index)
			out.WriteString(listHTML)
			index = nextIndex
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
			out.WriteString("<blockquote>" + renderer.inlineHTML(strings.TrimSpace(strings.Join(quoteLines, "\n"))) + "</blockquote>")
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
			out.WriteString("<" + tag + ">" + renderer.inlineHTML(strings.TrimSuffix(strings.TrimSpace(match[2]), ":")) + "</" + tag + ">")
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

type reportPrintListItem struct {
	indent  int
	ordered bool
	start   int
	body    string
}

func parseReportPrintListLine(line string) (reportPrintListItem, bool) {
	match := reportPrintListPattern.FindStringSubmatch(line)
	if match == nil {
		return reportPrintListItem{}, false
	}
	indent := len(strings.ReplaceAll(match[1], "\t", "    "))
	start := 1
	ordered := strings.TrimSpace(match[3]) != ""
	if ordered {
		parsed, err := strconv.Atoi(match[3])
		if err != nil || parsed < 1 {
			return reportPrintListItem{}, false
		}
		start = parsed
	}
	return reportPrintListItem{indent: indent, ordered: ordered, start: start, body: match[4]}, true
}

// listHTML renders one contiguous list tree. Indentation establishes nesting,
// while a marker change at the same indentation starts a sibling list. This
// matches Document Studio's mixed OL/UL parse/serialize contract instead of
// flattening every item into the first list type.
func (renderer *reportPrintRenderer) listHTML(lines []string, startIndex int) (string, int) {
	first, ok := parseReportPrintListLine(lines[startIndex])
	if !ok {
		return "", startIndex
	}
	tag := "ul"
	if first.ordered {
		tag = "ol"
	}
	var out strings.Builder
	out.WriteString("<" + tag)
	if first.ordered && first.start != 1 {
		out.WriteString(` start="` + strconv.Itoa(first.start) + `"`)
	}
	out.WriteString(">")
	index := startIndex
	for index < len(lines) {
		item, found := parseReportPrintListLine(lines[index])
		if !found || item.indent != first.indent || item.ordered != first.ordered {
			break
		}
		out.WriteString("<li>" + renderer.inlineHTML(strings.TrimSpace(item.body)))
		index++
		for index < len(lines) {
			nested, nestedFound := parseReportPrintListLine(lines[index])
			if !nestedFound || nested.indent <= first.indent {
				break
			}
			nestedHTML, nextIndex := renderer.listHTML(lines, index)
			if nextIndex <= index {
				break
			}
			out.WriteString(nestedHTML)
			index = nextIndex
		}
		out.WriteString("</li>")
	}
	out.WriteString("</" + tag + ">")
	return out.String(), index
}

// reportPrintTableHTML renders one pipe table: first row is the header, the
// second (separator) row is skipped, and every body row follows the header's
// cell count — exactly the client's artifactTableNode.
func reportPrintTableHTML(lines []string) string {
	return newReportPrintRenderer(meetingMemoryEntry{}).tableHTML(lines)
}

func (renderer *reportPrintRenderer) tableHTML(lines []string) string {
	rows := make([][]string, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, splitReportPrintTableRow(line))
	}
	if len(rows) == 0 {
		return ""
	}
	headers := rows[0]

	var out strings.Builder
	out.WriteString("<table><thead><tr>")
	for _, header := range headers {
		out.WriteString("<th>" + renderer.inlineHTML(header) + "</th>")
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
				out.WriteString("<td>" + renderer.inlineHTML(cell) + "</td>")
			}
			out.WriteString("</tr>")
		}
	}
	out.WriteString("</tbody></table>")
	return out.String()
}

// splitReportPrintTableRow treats \| as cell text, not a delimiter. Pairs of
// backslashes remain one literal backslash, so `\\|` still separates cells
// while `\\\|` prints a backslash and a pipe in the same cell.
func splitReportPrintTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "|") {
		trimmed = trimmed[1:]
	}
	if strings.HasSuffix(trimmed, "|") && !strings.HasSuffix(trimmed, `\|`) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	var cells []string
	var cell strings.Builder
	for index := 0; index < len(trimmed); index++ {
		switch trimmed[index] {
		case '\\':
			if index+1 < len(trimmed) && (trimmed[index+1] == '|' || trimmed[index+1] == '\\') {
				cell.WriteByte(trimmed[index+1])
				index++
				continue
			}
			cell.WriteByte(trimmed[index])
		case '|':
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteByte(trimmed[index])
		}
	}
	cells = append(cells, strings.TrimSpace(cell.String()))
	return cells
}

// reportPrintInlineHTML is the stateless compatibility seam used by focused
// tests and preamble callers. Artifact export uses the renderer method so
// attached images can be verified against the artifact's exact asset set.
func reportPrintInlineHTML(text string) string {
	return newReportPrintRenderer(meetingMemoryEntry{}).inlineHTML(text)
}

// inlineHTML renders one span of model text. Every emitted text segment and
// attribute is escaped before structure is added. Image tokens delegate to
// imageHTML, which can only produce a data: image from bounded local bytes or
// a non-fetching semantic reference card.
func (renderer *reportPrintRenderer) inlineHTML(text string) string {
	var out strings.Builder
	last := 0
	for _, match := range reportPrintInlinePattern.FindAllStringSubmatchIndex(text, -1) {
		out.WriteString(reportPrintEscapedText(text[last:match[0]]))
		switch {
		case reportPrintMatchPresent(match, reportPrintImageAltGroup):
			out.WriteString(renderer.imageHTML(
				reportPrintMatchText(text, match, reportPrintImageAltGroup),
				reportPrintMatchText(text, match, reportPrintImageSrcGroup),
			))
		case reportPrintMatchPresent(match, reportPrintLinkLabelGroup):
			label := reportPrintMatchText(text, match, reportPrintLinkLabelGroup)
			href := reportPrintMatchText(text, match, reportPrintLinkHrefGroup)
			out.WriteString("<a href=\"" + html.EscapeString(href) + "\">" + html.EscapeString(label) + "</a>")
		case reportPrintMatchPresent(match, reportPrintStrongStarGroup):
			out.WriteString("<strong>" + html.EscapeString(reportPrintMatchText(text, match, reportPrintStrongStarGroup)) + "</strong>")
		case reportPrintMatchPresent(match, reportPrintStrongUndGroup):
			out.WriteString("<strong>" + html.EscapeString(reportPrintMatchText(text, match, reportPrintStrongUndGroup)) + "</strong>")
		case reportPrintMatchPresent(match, reportPrintEmStarGroup):
			out.WriteString("<em>" + html.EscapeString(reportPrintMatchText(text, match, reportPrintEmStarGroup)) + "</em>")
		case reportPrintMatchPresent(match, reportPrintEmUndGroup):
			out.WriteString("<em>" + html.EscapeString(reportPrintMatchText(text, match, reportPrintEmUndGroup)) + "</em>")
		case reportPrintMatchPresent(match, reportPrintCodeGroup):
			out.WriteString("<code>" + html.EscapeString(reportPrintMatchText(text, match, reportPrintCodeGroup)) + "</code>")
		case reportPrintMatchPresent(match, reportPrintStrikeGroup):
			out.WriteString("<s>" + html.EscapeString(reportPrintMatchText(text, match, reportPrintStrikeGroup)) + "</s>")
		}
		last = match[1]
	}
	out.WriteString(reportPrintEscapedText(text[last:]))
	return out.String()
}

func reportPrintMatchPresent(match []int, group int) bool {
	return group > 0 && group*2+1 < len(match) && match[group*2] >= 0
}

func reportPrintMatchText(text string, match []int, group int) string {
	if !reportPrintMatchPresent(match, group) {
		return ""
	}
	return text[match[group*2]:match[group*2+1]]
}

// Markdown's two-space newline is a deliberate hard break. Ordinary newlines
// remain whitespace and collapse naturally in print paragraphs.
func reportPrintEscapedText(text string) string {
	parts := strings.Split(text, "  \n")
	if len(parts) == 1 {
		return html.EscapeString(text)
	}
	for index := range parts {
		parts[index] = html.EscapeString(parts[index])
	}
	return strings.Join(parts, "<br>")
}

func (renderer *reportPrintRenderer) imageHTML(alt string, source string) string {
	alt = strings.TrimSpace(alt)
	if alt == "" {
		alt = "Document image"
	}
	if dataURI, ok := renderer.localImageDataURI(source); ok {
		return `<span class="report-image"><img src="` + html.EscapeString(dataURI) + `" alt="` + html.EscapeString(alt) + `"><span class="report-image__caption">` + html.EscapeString(alt) + `</span></span>`
	}

	// A web URL is authored content, but fetching it while exporting would
	// disclose renderer traffic and create an SSRF surface. Keep its intent and
	// provenance in the PDF without ever assigning the URL to img.src.
	sourceLabel := "Available in the native document"
	sourceHTML := html.EscapeString(sourceLabel)
	if parsed, err := url.Parse(strings.TrimSpace(source)); err == nil && parsed.IsAbs() && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != "" {
		host := parsed.Hostname()
		sourceHTML = `<a href="` + html.EscapeString(source) + `">Source · ` + html.EscapeString(host) + `</a>`
	}
	return `<span class="report-image report-image--reference"><span class="report-image__reference" role="img" aria-label="` + html.EscapeString(alt) + `"><span class="report-image__eyebrow">Image</span><span class="report-image__title">` + html.EscapeString(alt) + `</span></span><span class="report-image__caption">` + sourceHTML + `</span></span>`
}

func (renderer *reportPrintRenderer) localImageDataURI(source string) (string, bool) {
	source = strings.TrimSpace(source)
	if dataURI, bytes, ok := reportPrintDataImage(source); ok {
		if renderer.reserveImageBytes(bytes, len(dataURI)) {
			return dataURI, true
		}
		return "", false
	}

	parsed, err := url.Parse(source)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.Path != "/artifacts/blob" {
		return "", false
	}
	for key := range parsed.Query() {
		if key != "ref" && key != "name" {
			return "", false
		}
	}
	ref := strings.TrimSpace(parsed.Query().Get("ref"))
	asset, attached := renderer.images[ref]
	if !attached || !validBlobRef(ref) {
		return "", false
	}
	stat, err := blobStatForRef(ref)
	statMIME := strings.ToLower(strings.TrimSpace(stat.Mime))
	if err != nil || stat.Size < 1 || stat.Size > reportPrintMaxInlineImageBytes || !reportPrintSafeImageMIME[statMIME] || (strings.TrimSpace(asset.Mime) != "" && !strings.EqualFold(strings.TrimSpace(asset.Mime), statMIME)) {
		return "", false
	}
	data, metadata, err := getBlob(ref)
	mime := strings.ToLower(strings.TrimSpace(metadata.Mime))
	if err != nil || len(data) > reportPrintMaxInlineImageBytes || mime != statMIME || !reportPrintSafeImageMIME[mime] || (strings.TrimSpace(asset.Mime) != "" && !strings.EqualFold(strings.TrimSpace(asset.Mime), mime)) {
		return "", false
	}
	encodedSize := base64.StdEncoding.EncodedLen(len(data)) + len("data:;base64,") + len(mime)
	if !renderer.reserveImageBytes(len(data), encodedSize) {
		return "", false
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), true
}

func reportPrintDataImage(source string) (string, int, bool) {
	header, payload, found := strings.Cut(source, ",")
	if !found || !strings.HasPrefix(strings.ToLower(header), "data:image/") || !strings.HasSuffix(strings.ToLower(header), ";base64") {
		return "", 0, false
	}
	mime := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(header), "data:"), ";base64"))
	if !reportPrintSafeImageMIME[mime] || len(payload) == 0 || base64.StdEncoding.DecodedLen(len(payload)) > reportPrintMaxInlineImageBytes {
		return "", 0, false
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(decoded) == 0 || len(decoded) > reportPrintMaxInlineImageBytes {
		return "", 0, false
	}
	canonical := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(decoded)
	return canonical, len(decoded), true
}

func (renderer *reportPrintRenderer) reserveImageBytes(decodedBytes int, encodedBytes int) bool {
	if renderer == nil || decodedBytes < 1 || decodedBytes > reportPrintMaxInlineImageBytes || encodedBytes < 1 || renderer.expandedImages >= reportPrintMaxImages || encodedBytes > reportPrintMaxImageBytes-renderer.expandedBytes {
		return false
	}
	renderer.expandedImages++
	renderer.expandedBytes += encodedBytes
	return true
}

// reportPrintCSS is the whole print stylesheet, inline in the document (the
// sidecar's CSP blocks every fetch). Light-only print typography: a system
// stack close to Google Sans Flex, 11pt body on a 68ch measure, hairline
// tables, sections that avoid breaking mid-block, headings that keep their
// content. The ember (#FF5A19, Stride Orange) appears exactly twice — the mark and
// the gate strip's rule — the earned-accent law in print.
const reportPrintCSS = `:root{color-scheme:light}
*{margin:0;padding:0;box-sizing:border-box}
@page{size:letter;margin:18mm 16mm 20mm}
body{font-family:"Google Sans Flex",-apple-system,"Segoe UI",sans-serif;font-size:11pt;line-height:1.55;color:#1a1d23;background:#fff;-webkit-print-color-adjust:exact;print-color-adjust:exact}
.masthead{padding-bottom:14pt;border-bottom:1.5pt solid #1a1d23;margin-bottom:16pt}
.brand{display:flex;align-items:baseline;gap:5pt;margin-bottom:16pt}
.brand .mark{color:#ff5a19;font-size:9pt;line-height:1}
.brand .wordmark{font-weight:700;font-size:10.5pt;letter-spacing:.01em}
.brand .kicker{margin-left:auto;font-family:ui-monospace,"SF Mono",Menlo,monospace;font-size:7.5pt;letter-spacing:.18em;color:#6a7180}
.title{font-size:22pt;line-height:1.15;font-weight:700;letter-spacing:-.015em;max-width:36ch}
.meta{margin-top:8pt;font-family:ui-monospace,"SF Mono",Menlo,monospace;font-size:8pt;letter-spacing:.04em;color:#6a7180;font-variant-numeric:tabular-nums}
.meta span+span::before{content:"·";margin:0 6pt;color:#c3c9d4}
.gate{margin-top:10pt;padding:6pt 9pt;border-left:2pt solid #ff5a19;background:#f7f8fa;font-family:ui-monospace,"SF Mono",Menlo,monospace;font-size:8pt;line-height:1.5;color:#3c424e;page-break-inside:avoid;break-inside:avoid}
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
.report li>ul,.report li>ol{margin:3pt 0 2pt;padding-left:15pt}
.report blockquote{margin:0 0 8pt;padding:3pt 10pt;border-left:1.5pt solid #d8dce4;color:#4c5261}
.report code{font-family:ui-monospace,"SF Mono",Menlo,monospace;font-size:9.5pt;background:#f1f3f7;border-radius:2pt;padding:.5pt 3pt}
.report a{color:#1a1d23;text-decoration:underline;text-decoration-thickness:.5pt;text-underline-offset:2pt}
.report-image{display:block;width:100%;margin:8pt 0 11pt;page-break-inside:avoid;break-inside:avoid}
.report-image img{display:block;max-width:100%;max-height:122mm;width:auto;height:auto;margin:0 auto;border-radius:4pt;object-fit:contain}
.report-image__caption{display:block;margin-top:4pt;font-family:ui-monospace,"SF Mono",Menlo,monospace;font-size:7.5pt;line-height:1.4;color:#6a7180}
.report-image__reference{display:flex;min-height:42mm;padding:12pt;flex-direction:column;justify-content:flex-end;border:.5pt solid #d8dce4;border-radius:4pt;background:#f7f8fa}
.report-image__eyebrow{font-family:ui-monospace,"SF Mono",Menlo,monospace;font-size:7pt;letter-spacing:.16em;text-transform:uppercase;color:#6a7180}
.report-image__title{display:block;max-width:42ch;margin-top:3pt;font-size:13pt;line-height:1.25;font-weight:700;color:#1a1d23}
.report hr{border:0;border-top:.5pt solid #d8dce4;margin:12pt 0}
.report table{border-collapse:collapse;width:100%;font-size:9pt;line-height:1.45;margin:2pt 0 10pt;page-break-inside:avoid;break-inside:avoid;font-variant-numeric:tabular-nums}
.report th{text-align:left;font-size:7.5pt;letter-spacing:.06em;text-transform:uppercase;color:#6a7180;border-bottom:1pt solid #1a1d23;padding:3pt 8pt 3pt 0}
.report td{border-bottom:.5pt solid #d8dce4;padding:3.5pt 8pt 3.5pt 0;vertical-align:top}
.colophon{margin-top:22pt;padding-top:8pt;border-top:.5pt solid #d8dce4;font-family:ui-monospace,"SF Mono",Menlo,monospace;font-size:7.5pt;letter-spacing:.1em;text-transform:uppercase;color:#6a7180}`
