package main

// document_docx.go exports a Document Studio Markdown body as a bounded,
// editable Word package (pure-Go OOXML, the way deck_pptx.go builds PPTX).
// The Markdown source stays the authority: the same block grammar the print
// renderer and the native editor share (headings, paragraphs, mixed nested
// lists, escaped-pipe tables, lone --- rules, blockquotes, fenced code, safe
// links, bold/italic/strike/inline code, hard breaks, images) is compiled
// straight to WordprocessingML. Images are either artifact-attached blobs or
// bounded data: bytes and are embedded under word/media; an external image
// URL becomes a plain hyperlink — the compiler never fetches anything.
//
// SECURITY: every text span is XML-escaped and stripped of characters XML
// forbids before structure is added; hyperlinks keep the http(s)/mailto-only
// rule the print renderer enforces; image bytes are admitted only through the
// artifact's own asset set or inline data.

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

const (
	documentDOCXContentType   = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	documentDOCXMaxBytes      = 64 << 20
	documentDOCXMaxMediaBytes = 48 << 20
	documentDOCXMaxImages     = 48
	// Letter page, 1in margins: 6.5in of text width. Word geometry is EMU
	// (914400/in) in drawings and twips (1440/in) in paragraph/table props.
	documentDOCXTextWidthEMU     = 5943600
	documentDOCXMaxImageHeight   = 7315200
	documentDOCXTextWidthTwips   = 9360
	documentDOCXMainNS           = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	documentDOCXDrawingNS        = "http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
	documentDOCXPictureNS        = "http://schemas.openxmlformats.org/drawingml/2006/picture"
	documentDOCXRelTypeStyles    = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles"
	documentDOCXRelTypeNumbering = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering"
	documentDOCXRelTypeImage     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
	documentDOCXRelTypeHyperlink = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
	documentDOCXRelTypeDocument  = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument"
	documentDOCXRelTypeCore      = "http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties"
	documentDOCXBulletNumID      = 1
)

type docxRelationship struct {
	ID       string
	Type     string
	Target   string
	External bool
}

type docxMedia struct {
	RelID     string
	Path      string
	Extension string
	Width     int
	Height    int
	Data      []byte
}

type docxImage struct {
	media docxMedia
	cx    int64
	cy    int64
	alt   string
}

type docxRun struct {
	text   string
	bold   bool
	italic bool
	code   bool
	strike bool
	href   string
	brk    bool
	image  *docxImage
}

type docxListItem struct {
	level int
	numID int
	text  string
}

// docxNumInstance is one w:num: bullets share instance 1; every ordered list
// group gets its own instance so numbering restarts per list.
type docxNumInstance struct {
	numID   int
	ordered bool
	level   int
	start   int
}

type docxBuilder struct {
	body        strings.Builder
	rels        []docxRelationship
	media       []docxMedia
	mediaByKey  map[string]docxMedia
	mediaBytes  int
	nums        []docxNumInstance
	hyperlinks  map[string]string
	allowedRefs map[string]struct{}
	resolve     deckPPTXImageResolver
	drawingID   int
}

func newDocxBuilder(allowedRefs map[string]struct{}, resolve deckPPTXImageResolver) *docxBuilder {
	builder := &docxBuilder{
		mediaByKey:  map[string]docxMedia{},
		hyperlinks:  map[string]string{},
		allowedRefs: allowedRefs,
		resolve:     resolve,
		nums:        []docxNumInstance{{numID: documentDOCXBulletNumID, ordered: false}},
	}
	builder.rels = []docxRelationship{
		{ID: "rId1", Type: documentDOCXRelTypeStyles, Target: "styles.xml"},
		{ID: "rId2", Type: documentDOCXRelTypeNumbering, Target: "numbering.xml"},
	}
	return builder
}

// compileDocumentDOCX converts one Markdown body into a .docx package.
func compileDocumentDOCX(markdown string, title string, allowedImageRefs map[string]struct{}, resolve deckPPTXImageResolver) ([]byte, error) {
	if len(markdown) > documentStudioMaxBytes {
		return nil, fmt.Errorf("document exceeds the 1MB editing bound")
	}
	if allowedImageRefs == nil {
		allowedImageRefs = map[string]struct{}{}
	}
	builder := newDocxBuilder(allowedImageRefs, resolve)
	if err := builder.compileBody(markdown); err != nil {
		return nil, err
	}
	parts := []deckPPTXPart{
		{Path: "[Content_Types].xml", Data: []byte(builder.contentTypesXML())},
		{Path: "_rels/.rels", Data: []byte(docxPackageRelationshipsXML())},
		{Path: "docProps/core.xml", Data: []byte(docxCoreXML(title))},
		{Path: "word/document.xml", Data: []byte(builder.documentXML())},
		{Path: "word/styles.xml", Data: []byte(documentDOCXStylesXML)},
		{Path: "word/numbering.xml", Data: []byte(builder.numberingXML())},
		{Path: "word/_rels/document.xml.rels", Data: []byte(docxRelationshipsXML(builder.rels))},
	}
	for _, item := range builder.media {
		parts = append(parts, deckPPTXPart{Path: "word/" + item.Path, Data: item.Data})
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, part := range parts {
		entry, err := writer.CreateHeader(&zip.FileHeader{Name: part.Path, Method: zip.Deflate})
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write(part.Data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if archive.Len() > documentDOCXMaxBytes {
		return nil, fmt.Errorf("Word package exceeds the %dMB export bound", documentDOCXMaxBytes>>20)
	}
	return archive.Bytes(), nil
}

// compileBody walks the block grammar (fence before table, table before rule,
// rule before list, list before quote, heading, paragraph — the print
// renderer's order, plus fenced code which print folds into paragraphs).
func (builder *docxBuilder) compileBody(markdown string) error {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(markdown, "\r\n", "\n"), "\r", "\n"), "\n")
	var paragraph []string
	flushParagraph := func() error {
		value := strings.TrimSpace(strings.Join(paragraph, "\n"))
		paragraph = paragraph[:0]
		if value == "" {
			return nil
		}
		return builder.writeParagraph("", "", value)
	}
	index := 0
	for index < len(lines) {
		line := lines[index]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if err := flushParagraph(); err != nil {
				return err
			}
			index++
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			if err := flushParagraph(); err != nil {
				return err
			}
			fence := trimmed[:3]
			index++
			var code []string
			for index < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[index]), fence) {
				code = append(code, lines[index])
				index++
			}
			if index < len(lines) {
				index++
			}
			builder.writeCode(code)
			continue
		}
		if reportPrintTableRowPattern.MatchString(line) && index+1 < len(lines) && reportPrintTableSepPattern.MatchString(lines[index+1]) {
			if err := flushParagraph(); err != nil {
				return err
			}
			var tableLines []string
			for index < len(lines) && reportPrintTableRowPattern.MatchString(lines[index]) {
				tableLines = append(tableLines, lines[index])
				index++
			}
			if err := builder.writeTable(tableLines); err != nil {
				return err
			}
			continue
		}
		if reportPrintRulePattern.MatchString(line) {
			if err := flushParagraph(); err != nil {
				return err
			}
			builder.body.WriteString(`<w:p><w:pPr><w:pBdr><w:bottom w:val="single" w:sz="6" w:space="1" w:color="BFBFBF"/></w:pBdr><w:spacing w:before="120" w:after="240"/></w:pPr></w:p>`)
			index++
			continue
		}
		if _, ok := parseReportPrintListLine(line); ok {
			if err := flushParagraph(); err != nil {
				return err
			}
			var items []docxListItem
			next := builder.collectList(lines, index, 0, &items)
			if next <= index {
				next = index + 1
			}
			index = next
			for _, item := range items {
				if err := builder.writeListItem(item); err != nil {
					return err
				}
			}
			continue
		}
		if reportPrintQuoteStart.MatchString(line) {
			if err := flushParagraph(); err != nil {
				return err
			}
			var quote []string
			for index < len(lines) {
				match := reportPrintQuotePattern.FindStringSubmatch(lines[index])
				if match == nil {
					break
				}
				quote = append(quote, match[1])
				index++
			}
			if err := builder.writeParagraph("Quote", "", strings.TrimSpace(strings.Join(quote, "\n"))); err != nil {
				return err
			}
			continue
		}
		if match := reportPrintHeadingPattern.FindStringSubmatch(line); match != nil {
			if err := flushParagraph(); err != nil {
				return err
			}
			level := len(match[1])
			if level > 4 {
				level = 4
			}
			if err := builder.writeParagraph("Heading"+strconv.Itoa(level), "", strings.TrimSuffix(strings.TrimSpace(match[2]), ":")); err != nil {
				return err
			}
			index++
			continue
		}
		paragraph = append(paragraph, line)
		index++
	}
	return flushParagraph()
}

// collectList flattens one list tree into (level, numId, text) items; nested
// indentation deepens the level, a marker change at the same indentation
// starts a sibling group — the mixed OL/UL contract of the editor.
func (builder *docxBuilder) collectList(lines []string, startIndex int, level int, out *[]docxListItem) int {
	first, ok := parseReportPrintListLine(lines[startIndex])
	if !ok {
		return startIndex
	}
	numID := builder.allocateNum(first.ordered, level, first.start)
	index := startIndex
	for index < len(lines) {
		item, found := parseReportPrintListLine(lines[index])
		if !found || item.indent != first.indent || item.ordered != first.ordered {
			break
		}
		*out = append(*out, docxListItem{level: level, numID: numID, text: strings.TrimSpace(item.body)})
		index++
		for index < len(lines) {
			nested, nestedFound := parseReportPrintListLine(lines[index])
			if !nestedFound || nested.indent <= first.indent {
				break
			}
			nextLevel := level + 1
			if nextLevel > 8 {
				nextLevel = 8
			}
			next := builder.collectList(lines, index, nextLevel, out)
			if next <= index {
				break
			}
			index = next
		}
	}
	return index
}

func (builder *docxBuilder) allocateNum(ordered bool, level int, start int) int {
	if !ordered {
		return documentDOCXBulletNumID
	}
	if start < 1 {
		start = 1
	}
	numID := len(builder.nums) + 1
	builder.nums = append(builder.nums, docxNumInstance{numID: numID, ordered: true, level: level, start: start})
	return numID
}

func (builder *docxBuilder) writeListItem(item docxListItem) error {
	numbering := `<w:numPr><w:ilvl w:val="` + strconv.Itoa(item.level) + `"/><w:numId w:val="` + strconv.Itoa(item.numID) + `"/></w:numPr>`
	return builder.writeParagraph("ListParagraph", numbering, item.text)
}

func (builder *docxBuilder) writeParagraph(style string, extraPPr string, text string) error {
	runs, err := builder.inlineRuns(text)
	if err != nil {
		return err
	}
	builder.body.WriteString(builder.paragraphXML(style, extraPPr, runs))
	return nil
}

func (builder *docxBuilder) writeCode(lines []string) {
	if len(lines) == 0 {
		lines = []string{""}
	}
	for _, line := range lines {
		builder.body.WriteString(`<w:p><w:pPr><w:pStyle w:val="Code"/></w:pPr><w:r><w:t xml:space="preserve">` + docxText(strings.ReplaceAll(line, "\t", "    ")) + `</w:t></w:r></w:p>`)
	}
	builder.body.WriteString(`<w:p><w:pPr><w:spacing w:after="0"/></w:pPr></w:p>`)
}

func (builder *docxBuilder) writeTable(lines []string) error {
	rows := make([][]string, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, splitReportPrintTableRow(line))
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return nil
	}
	headers := rows[0]
	columns := len(headers)
	width := documentDOCXTextWidthTwips / columns
	builder.body.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/><w:tblW w:w="5000" w:type="pct"/>` + docxTableBordersXML() + `<w:tblLook w:val="04A0" w:firstRow="1" w:lastRow="0" w:firstColumn="0" w:lastColumn="0" w:noHBand="0" w:noVBand="1"/></w:tblPr><w:tblGrid>`)
	for range headers {
		builder.body.WriteString(`<w:gridCol w:w="` + strconv.Itoa(width) + `"/>`)
	}
	builder.body.WriteString(`</w:tblGrid>`)
	writeRow := func(cells []string, header bool) error {
		builder.body.WriteString(`<w:tr>`)
		if header {
			builder.body.WriteString(`<w:trPr><w:tblHeader/></w:trPr>`)
		}
		for cellIndex := 0; cellIndex < columns; cellIndex++ {
			cell := ""
			if cellIndex < len(cells) {
				cell = cells[cellIndex]
			}
			runs, err := builder.inlineRuns(cell)
			if err != nil {
				return err
			}
			if header {
				for index := range runs {
					runs[index].bold = true
				}
			}
			builder.body.WriteString(`<w:tc><w:tcPr><w:tcW w:w="` + strconv.Itoa(width) + `" w:type="dxa"/>`)
			if header {
				builder.body.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="F2F2F2"/>`)
			}
			builder.body.WriteString(`</w:tcPr>` + builder.paragraphXML("", `<w:spacing w:before="40" w:after="40"/>`, runs) + `</w:tc>`)
		}
		builder.body.WriteString(`</w:tr>`)
		return nil
	}
	if err := writeRow(headers, true); err != nil {
		return err
	}
	if len(rows) > 2 {
		for _, row := range rows[2:] {
			if err := writeRow(row, false); err != nil {
				return err
			}
		}
	}
	// Word requires a paragraph between a table and whatever follows.
	builder.body.WriteString(`</w:tbl><w:p><w:pPr><w:spacing w:after="0"/></w:pPr></w:p>`)
	return nil
}

func docxTableBordersXML() string {
	var out strings.Builder
	out.WriteString(`<w:tblBorders>`)
	for _, edge := range []string{"top", "left", "bottom", "right", "insideH", "insideV"} {
		out.WriteString(`<w:` + edge + ` w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/>`)
	}
	out.WriteString(`</w:tblBorders>`)
	return out.String()
}

// inlineRuns tokenizes one span of Markdown into runs with the print
// renderer's inline grammar. Hard breaks (two spaces + newline) become w:br;
// ordinary newlines collapse to a space.
func (builder *docxBuilder) inlineRuns(text string) ([]docxRun, error) {
	var runs []docxRun
	last := 0
	for _, match := range reportPrintInlinePattern.FindAllStringSubmatchIndex(text, -1) {
		runs = append(runs, docxTextRuns(text[last:match[0]], docxRun{})...)
		switch {
		case reportPrintMatchPresent(match, reportPrintImageAltGroup):
			image, err := builder.imageRuns(reportPrintMatchText(text, match, reportPrintImageAltGroup), reportPrintMatchText(text, match, reportPrintImageSrcGroup))
			if err != nil {
				return nil, err
			}
			runs = append(runs, image...)
		case reportPrintMatchPresent(match, reportPrintLinkLabelGroup):
			href := reportPrintMatchText(text, match, reportPrintLinkHrefGroup)
			runs = append(runs, docxRun{text: reportPrintMatchText(text, match, reportPrintLinkLabelGroup), href: builder.hyperlinkRelID(href)})
		case reportPrintMatchPresent(match, reportPrintStrongStarGroup):
			runs = append(runs, docxTextRuns(reportPrintMatchText(text, match, reportPrintStrongStarGroup), docxRun{bold: true})...)
		case reportPrintMatchPresent(match, reportPrintStrongUndGroup):
			runs = append(runs, docxTextRuns(reportPrintMatchText(text, match, reportPrintStrongUndGroup), docxRun{bold: true})...)
		case reportPrintMatchPresent(match, reportPrintEmStarGroup):
			runs = append(runs, docxTextRuns(reportPrintMatchText(text, match, reportPrintEmStarGroup), docxRun{italic: true})...)
		case reportPrintMatchPresent(match, reportPrintEmUndGroup):
			runs = append(runs, docxTextRuns(reportPrintMatchText(text, match, reportPrintEmUndGroup), docxRun{italic: true})...)
		case reportPrintMatchPresent(match, reportPrintCodeGroup):
			runs = append(runs, docxRun{text: reportPrintMatchText(text, match, reportPrintCodeGroup), code: true})
		case reportPrintMatchPresent(match, reportPrintStrikeGroup):
			runs = append(runs, docxTextRuns(reportPrintMatchText(text, match, reportPrintStrikeGroup), docxRun{strike: true})...)
		}
		last = match[1]
	}
	runs = append(runs, docxTextRuns(text[last:], docxRun{})...)
	return runs, nil
}

func docxTextRuns(text string, template docxRun) []docxRun {
	if text == "" {
		return nil
	}
	parts := strings.Split(text, "  \n")
	runs := make([]docxRun, 0, len(parts)*2)
	for index, part := range parts {
		if index > 0 {
			runs = append(runs, docxRun{brk: true})
		}
		part = strings.ReplaceAll(strings.ReplaceAll(part, "\r\n", " "), "\n", " ")
		if part == "" {
			continue
		}
		run := template
		run.text = part
		runs = append(runs, run)
	}
	return runs
}

func (builder *docxBuilder) hyperlinkRelID(href string) string {
	href = strings.TrimSpace(href)
	parsed, err := url.Parse(href)
	if err != nil || !parsed.IsAbs() || !oneOf(strings.ToLower(parsed.Scheme), "http", "https", "mailto") {
		return ""
	}
	if relID, seen := builder.hyperlinks[href]; seen {
		return relID
	}
	relID := "rId" + strconv.Itoa(len(builder.rels)+1)
	builder.rels = append(builder.rels, docxRelationship{ID: relID, Type: documentDOCXRelTypeHyperlink, Target: href, External: true})
	builder.hyperlinks[href] = relID
	return relID
}

// imageRuns embeds an attached or data: image as an inline drawing scaled to
// the editor's width token (25/50/75/100% of the text width). Anything the
// package cannot own — an external URL, an unattached ref, an over-budget
// file — degrades to its alt text (linked when the URL is a web address).
func (builder *docxBuilder) imageRuns(alt string, source string) ([]docxRun, error) {
	alt = strings.TrimSpace(alt)
	presentation := parseReportPrintImagePresentation(source)
	fallbackText := firstNonEmptyString(alt, "Image")
	fallback := func() []docxRun {
		if parsed, err := url.Parse(presentation.source); err == nil && parsed.IsAbs() && oneOf(strings.ToLower(parsed.Scheme), "http", "https") && parsed.Hostname() != "" {
			return []docxRun{{text: fallbackText + " (" + parsed.Hostname() + ")", italic: true, href: builder.hyperlinkRelID(presentation.source)}}
		}
		return []docxRun{{text: "[" + fallbackText + "]", italic: true}}
	}
	data, imageMime, key, ok := builder.resolveImageBytes(presentation.source)
	if !ok {
		return fallback(), nil
	}
	media, known := builder.mediaByKey[key]
	if !known {
		if len(builder.media) >= documentDOCXMaxImages || builder.mediaBytes+len(data) > documentDOCXMaxMediaBytes {
			return fallback(), nil
		}
		extension, err := deckPPTXImageExtension(imageMime)
		if err != nil {
			return fallback(), nil
		}
		width, height, err := deckPPTXImageDimensions(data, imageMime)
		if err != nil || width < 1 || height < 1 {
			return fallback(), nil
		}
		relID := "rId" + strconv.Itoa(len(builder.rels)+1)
		media = docxMedia{RelID: relID, Path: "media/image" + strconv.Itoa(len(builder.media)+1) + "." + extension, Extension: extension, Width: width, Height: height, Data: data}
		builder.rels = append(builder.rels, docxRelationship{ID: relID, Type: documentDOCXRelTypeImage, Target: media.Path})
		builder.media = append(builder.media, media)
		builder.mediaByKey[key] = media
		builder.mediaBytes += len(data)
	}
	cx := int64(documentDOCXTextWidthEMU) * int64(presentation.width) / 100
	cy := cx * int64(media.Height) / int64(media.Width)
	if cy > documentDOCXMaxImageHeight {
		cy = documentDOCXMaxImageHeight
		cx = cy * int64(media.Width) / int64(media.Height)
	}
	if cx < 1 || cy < 1 {
		return fallback(), nil
	}
	runs := []docxRun{{image: &docxImage{media: media, cx: cx, cy: cy, alt: fallbackText}}}
	if presentation.caption != "" {
		runs = append(runs, docxRun{brk: true}, docxRun{text: presentation.caption, italic: true})
	}
	return runs, nil
}

func (builder *docxBuilder) resolveImageBytes(source string) ([]byte, string, string, bool) {
	source = strings.TrimSpace(source)
	if header, payload, found := strings.Cut(source, ","); found && strings.HasPrefix(strings.ToLower(header), "data:image/") {
		if _, _, ok := reportPrintDataImage(source); !ok {
			return nil, "", "", false
		}
		imageMime := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(header), "data:"), ";base64"))
		data, err := base64.StdEncoding.DecodeString(payload)
		if err != nil || len(data) == 0 {
			return nil, "", "", false
		}
		return data, imageMime, docxMediaKey(data), true
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.Path != "/artifacts/blob" {
		return nil, "", "", false
	}
	ref := strings.TrimSpace(parsed.Query().Get("ref"))
	if _, allowed := builder.allowedRefs[ref]; !allowed || !validBlobRef(ref) || builder.resolve == nil {
		return nil, "", "", false
	}
	data, imageMime, err := builder.resolve(ref)
	imageMime = strings.ToLower(strings.TrimSpace(strings.Split(imageMime, ";")[0]))
	if err != nil || len(data) == 0 || len(data) > reportPrintMaxInlineImageBytes || !reportPrintSafeImageMIME[imageMime] {
		return nil, "", "", false
	}
	return data, imageMime, docxMediaKey(data), true
}

// docxMediaKey dedupes media by content: the same bytes reached through an
// attached ref and through an inline data: URI share one word/media part.
func docxMediaKey(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (builder *docxBuilder) paragraphXML(style string, extraPPr string, runs []docxRun) string {
	var out strings.Builder
	out.WriteString(`<w:p>`)
	if style != "" || extraPPr != "" {
		out.WriteString(`<w:pPr>`)
		if style != "" {
			out.WriteString(`<w:pStyle w:val="` + style + `"/>`)
		}
		out.WriteString(extraPPr + `</w:pPr>`)
	}
	for _, run := range runs {
		out.WriteString(builder.runXML(run))
	}
	out.WriteString(`</w:p>`)
	return out.String()
}

func (builder *docxBuilder) runXML(run docxRun) string {
	if run.brk {
		return `<w:r><w:br/></w:r>`
	}
	if run.image != nil {
		return builder.drawingXML(*run.image)
	}
	var props strings.Builder
	if run.href != "" {
		props.WriteString(`<w:rStyle w:val="Hyperlink"/>`)
	}
	if run.code {
		props.WriteString(`<w:rStyle w:val="CodeChar"/>`)
	}
	if run.bold {
		props.WriteString(`<w:b/><w:bCs/>`)
	}
	if run.italic {
		props.WriteString(`<w:i/><w:iCs/>`)
	}
	if run.strike {
		props.WriteString(`<w:strike/>`)
	}
	var out strings.Builder
	if run.href != "" {
		out.WriteString(`<w:hyperlink r:id="` + run.href + `">`)
	}
	out.WriteString(`<w:r>`)
	if props.Len() > 0 {
		out.WriteString(`<w:rPr>` + props.String() + `</w:rPr>`)
	}
	out.WriteString(`<w:t xml:space="preserve">` + docxText(run.text) + `</w:t></w:r>`)
	if run.href != "" {
		out.WriteString(`</w:hyperlink>`)
	}
	return out.String()
}

func (builder *docxBuilder) drawingXML(image docxImage) string {
	builder.drawingID++
	id := strconv.Itoa(builder.drawingID)
	cx, cy := strconv.FormatInt(image.cx, 10), strconv.FormatInt(image.cy, 10)
	name := docxText(image.media.Path)
	return `<w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0"><wp:extent cx="` + cx + `" cy="` + cy + `"/><wp:docPr id="` + id + `" name="Picture ` + id + `" descr="` + docxText(image.alt) + `"/><wp:cNvGraphicFramePr><a:graphicFrameLocks noChangeAspect="1"/></wp:cNvGraphicFramePr><a:graphic><a:graphicData uri="` + documentDOCXPictureNS + `"><pic:pic><pic:nvPicPr><pic:cNvPr id="0" name="` + name + `"/><pic:cNvPicPr/></pic:nvPicPr><pic:blipFill><a:blip r:embed="` + image.media.RelID + `"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill><pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="` + cx + `" cy="` + cy + `"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r>`
}

// docxText escapes a span for XML and drops the control characters XML 1.0
// forbids (Word refuses the whole package otherwise). Tabs become spaces so
// fenced code keeps its columns without w:tab elements.
func docxText(value string) string {
	var out strings.Builder
	for _, character := range value {
		switch {
		case character == '\t':
			out.WriteString("    ")
		case character == '\n' || character == '\r':
			out.WriteByte(' ')
		case character < 0x20 || character == 0xFFFE || character == 0xFFFF || (character >= 0xD800 && character <= 0xDFFF):
			continue
		case character == '&':
			out.WriteString("&amp;")
		case character == '<':
			out.WriteString("&lt;")
		case character == '>':
			out.WriteString("&gt;")
		case character == '"':
			out.WriteString("&quot;")
		default:
			out.WriteRune(character)
		}
	}
	return out.String()
}

func (builder *docxBuilder) documentXML() string {
	body := builder.body.String()
	if body == "" {
		body = `<w:p/>`
	}
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="` + documentDOCXMainNS + `" xmlns:r="` + deckPPTXOfficeDocumentNS + `" xmlns:wp="` + documentDOCXDrawingNS + `" xmlns:a="` + deckPPTXDrawingMLNS + `" xmlns:pic="` + documentDOCXPictureNS + `"><w:body>` + body +
		`<w:sectPr><w:pgSz w:w="12240" w:h="15840"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="708" w:footer="708" w:gutter="0"/></w:sectPr></w:body></w:document>`
}

func (builder *docxBuilder) numberingXML() string {
	var out strings.Builder
	out.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:numbering xmlns:w="` + documentDOCXMainNS + `">`)
	bullets := []string{"•", "◦", "▪"}
	out.WriteString(`<w:abstractNum w:abstractNumId="0"><w:multiLevelType w:val="hybridMultilevel"/>`)
	for level := 0; level < 9; level++ {
		out.WriteString(`<w:lvl w:ilvl="` + strconv.Itoa(level) + `"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="` + bullets[level%3] + `"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="` + strconv.Itoa(720*(level+1)) + `" w:hanging="360"/></w:pPr><w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial" w:hint="default"/></w:rPr></w:lvl>`)
	}
	out.WriteString(`</w:abstractNum><w:abstractNum w:abstractNumId="1"><w:multiLevelType w:val="hybridMultilevel"/>`)
	formats := []string{"decimal", "lowerLetter", "lowerRoman"}
	for level := 0; level < 9; level++ {
		out.WriteString(`<w:lvl w:ilvl="` + strconv.Itoa(level) + `"><w:start w:val="1"/><w:numFmt w:val="` + formats[level%3] + `"/><w:lvlText w:val="%` + strconv.Itoa(level+1) + `."/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="` + strconv.Itoa(720*(level+1)) + `" w:hanging="360"/></w:pPr></w:lvl>`)
	}
	out.WriteString(`</w:abstractNum>`)
	for _, instance := range builder.nums {
		abstract := "0"
		if instance.ordered {
			abstract = "1"
		}
		out.WriteString(`<w:num w:numId="` + strconv.Itoa(instance.numID) + `"><w:abstractNumId w:val="` + abstract + `"/>`)
		if instance.ordered {
			out.WriteString(`<w:lvlOverride w:ilvl="` + strconv.Itoa(instance.level) + `"><w:startOverride w:val="` + strconv.Itoa(instance.start) + `"/></w:lvlOverride>`)
		}
		out.WriteString(`</w:num>`)
	}
	out.WriteString(`</w:numbering>`)
	return out.String()
}

func (builder *docxBuilder) contentTypesXML() string {
	var out strings.Builder
	out.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="` + deckPPTXContentTypesNS + `"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/>`)
	seen := map[string]bool{}
	for _, item := range builder.media {
		if seen[item.Extension] {
			continue
		}
		seen[item.Extension] = true
		contentType := "image/" + item.Extension
		if item.Extension == "jpg" {
			contentType = "image/jpeg"
		}
		out.WriteString(`<Default Extension="` + item.Extension + `" ContentType="` + contentType + `"/>`)
	}
	out.WriteString(`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/><Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/></Types>`)
	return out.String()
}

func docxPackageRelationshipsXML() string {
	return docxRelationshipsXML([]docxRelationship{
		{ID: "rId1", Type: documentDOCXRelTypeDocument, Target: "word/document.xml"},
		{ID: "rId2", Type: documentDOCXRelTypeCore, Target: "docProps/core.xml"},
	})
}

func docxRelationshipsXML(rels []docxRelationship) string {
	var out strings.Builder
	out.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="` + deckPPTXRelationshipNS + `">`)
	for _, rel := range rels {
		out.WriteString(`<Relationship Id="` + rel.ID + `" Type="` + rel.Type + `" Target="` + docxText(rel.Target) + `"`)
		if rel.External {
			out.WriteString(` TargetMode="External"`)
		}
		out.WriteString(`/>`)
	}
	out.WriteString(`</Relationships>`)
	return out.String()
}

func docxCoreXML(title string) string {
	now := "2000-01-01T00:00:00Z"
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><cp:coreProperties xmlns:cp="` + deckPPTXCorePropertiesNS + `" xmlns:dc="` + deckPPTXDublinCoreNS + `" xmlns:dcterms="` + deckPPTXDublinCoreTermsNS + `" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><dc:title>` + docxText(firstNonEmptyString(strings.TrimSpace(title), "Document")) + `</dc:title><dc:creator>STRIDE</dc:creator><cp:lastModifiedBy>STRIDE</cp:lastModifiedBy><dcterms:created xsi:type="dcterms:W3CDTF">` + now + `</dcterms:created><dcterms:modified xsi:type="dcterms:W3CDTF">` + now + `</dcterms:modified></cp:coreProperties>`
}

const documentDOCXStylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
	`<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial" w:cs="Arial" w:eastAsia="Arial"/><w:sz w:val="22"/><w:szCs w:val="22"/><w:lang w:val="en-US"/></w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:after="160" w:line="276" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>` +
	`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="360" w:after="120"/><w:outlineLvl w:val="0"/></w:pPr><w:rPr><w:b/><w:bCs/><w:sz w:val="52"/><w:szCs w:val="52"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="280" w:after="100"/><w:outlineLvl w:val="1"/></w:pPr><w:rPr><w:b/><w:bCs/><w:sz w:val="36"/><w:szCs w:val="36"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="200" w:after="80"/><w:outlineLvl w:val="2"/></w:pPr><w:rPr><w:b/><w:bCs/><w:sz w:val="28"/><w:szCs w:val="28"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading4"><w:name w:val="heading 4"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="160" w:after="60"/><w:outlineLvl w:val="3"/></w:pPr><w:rPr><w:b/><w:bCs/><w:sz w:val="24"/><w:szCs w:val="24"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Quote"><w:name w:val="Quote"/><w:basedOn w:val="Normal"/><w:qFormat/><w:pPr><w:pBdr><w:left w:val="single" w:sz="12" w:space="8" w:color="BFBFBF"/></w:pBdr><w:ind w:left="720"/></w:pPr><w:rPr><w:i/><w:iCs/><w:color w:val="595959"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Code"><w:name w:val="Code"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:after="0" w:line="240" w:lineRule="auto"/><w:shd w:val="clear" w:color="auto" w:fill="F2F2F2"/></w:pPr><w:rPr><w:rFonts w:ascii="Courier New" w:hAnsi="Courier New" w:cs="Courier New"/><w:sz w:val="19"/><w:szCs w:val="19"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="ListParagraph"><w:name w:val="List Paragraph"/><w:basedOn w:val="Normal"/><w:qFormat/><w:pPr><w:spacing w:after="60"/><w:contextualSpacing/></w:pPr></w:style>` +
	`<w:style w:type="character" w:default="1" w:styleId="DefaultParagraphFont"><w:name w:val="Default Paragraph Font"/></w:style>` +
	`<w:style w:type="character" w:styleId="Hyperlink"><w:name w:val="Hyperlink"/><w:rPr><w:color w:val="0563C1"/><w:u w:val="single"/></w:rPr></w:style>` +
	`<w:style w:type="character" w:styleId="CodeChar"><w:name w:val="Code Char"/><w:rPr><w:rFonts w:ascii="Courier New" w:hAnsi="Courier New" w:cs="Courier New"/><w:sz w:val="19"/><w:szCs w:val="19"/><w:shd w:val="clear" w:color="auto" w:fill="F2F2F2"/></w:rPr></w:style>` +
	`<w:style w:type="table" w:default="1" w:styleId="TableNormal"><w:name w:val="Normal Table"/><w:tblPr><w:tblInd w:w="0" w:type="dxa"/><w:tblCellMar><w:top w:w="0" w:type="dxa"/><w:left w:w="108" w:type="dxa"/><w:bottom w:w="0" w:type="dxa"/><w:right w:w="108" w:type="dxa"/></w:tblCellMar></w:tblPr></w:style>` +
	`<w:style w:type="table" w:styleId="TableGrid"><w:name w:val="Table Grid"/><w:basedOn w:val="TableNormal"/><w:tblPr><w:tblBorders><w:top w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/><w:left w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/><w:bottom w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/><w:right w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/><w:insideH w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/><w:insideV w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/></w:tblBorders></w:tblPr></w:style>` +
	`</w:styles>`

func documentDOCXFilename(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Document"
	}
	var out strings.Builder
	for _, character := range title {
		if unicode.IsControl(character) || strings.ContainsRune(`/\\:*?"<>|`, character) {
			out.WriteRune('-')
		} else {
			out.WriteRune(character)
		}
		if out.Len() >= 140 {
			break
		}
	}
	base := strings.Trim(strings.TrimSpace(out.String()), ".")
	if base == "" {
		base = "Document"
	}
	if strings.HasSuffix(strings.ToLower(base), ".docx") {
		base = strings.TrimSpace(base[:len(base)-len(".docx")])
	}
	return base + ".docx"
}

// documentDOCXExportHandler POST /artifacts/export-docx {artifactId,
// expectedVersion} mirrors the PowerPoint route: ACLExport on the artifact,
// final-export admission, exact-revision binding, compile, re-check, stream.
func documentDOCXExportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil || kanbanApp.memory == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "artifacts are unavailable")
		return
	}
	payload := struct {
		ArtifactID      string `json:"artifactId"`
		ExpectedVersion int    `json:"expectedVersion"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read Word export request")
		return
	}
	payload.ArtifactID = strings.TrimSpace(payload.ArtifactID)
	artifact, ok := authorizedArtifactByID(r.Context(), user, ACLExport, payload.ArtifactID)
	if !ok || !artifactIsDocumentStudioDocument(artifact) {
		writeAuthError(w, http.StatusNotFound, "document artifact not found")
		return
	}
	if err := kanbanApp.requireFinalExportAdmission(artifact); err != nil {
		writeAuthError(w, http.StatusConflict, err.Error())
		return
	}
	if payload.ExpectedVersion < 1 || artifactVersion(artifact) != payload.ExpectedVersion {
		writeAuthError(w, http.StatusConflict, "the document changed; reopen it before downloading Word")
		return
	}
	allowedRefs := map[string]struct{}{}
	for _, asset := range artifactAssets(artifact) {
		if artifactAssetIsEditableImage(asset) {
			allowedRefs[asset.Ref] = struct{}{}
		}
	}
	title := strings.TrimSpace(artifact.Metadata["title"])
	// Research reports export through the same branded render pass the PDF
	// uses (report_print.go): cover, contents, sources appendix, colophon.
	docx, err := compileDocumentDOCX(documentExportMarkdown(artifact), title, allowedRefs, func(ref string) ([]byte, string, error) {
		if _, allowed := allowedRefs[ref]; !allowed {
			return nil, "", fmt.Errorf("image is not attached")
		}
		data, meta, err := getBlob(ref)
		if err != nil {
			return nil, "", err
		}
		return data, meta.Mime, nil
	})
	if err != nil {
		writeAuthError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	current, currentOK := authorizedArtifactByID(r.Context(), user, ACLExport, artifact.ID)
	if !currentOK || artifactVersion(current) != artifactVersion(artifact) || kanbanApp.requireFinalExportAdmission(current) != nil {
		writeAuthError(w, http.StatusConflict, "the document or its review changed; reopen it before downloading Word")
		return
	}
	w.Header().Set("Content-Type", documentDOCXContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": documentDOCXFilename(title)}))
	w.Header().Set("Content-Length", strconv.Itoa(len(docx)))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(docx)
}
