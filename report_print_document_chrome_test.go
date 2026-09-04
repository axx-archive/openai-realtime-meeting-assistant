package main

import (
	"strings"
	"testing"
	"time"
)

// TestOrdinaryDocumentPrintOmitsResearchReportChrome pins the one-pass
// contract report_print.go asserts: the PDF and the .docx must agree about
// what the document IS. POST /artifacts/export-pdf routes EVERY markdown
// artifact through renderResearchReportPrintHTML, so the research-only chrome
// — the "RESEARCH REPORT" kicker, the Contents nav and the sources appendix —
// is gated on the same predicate documentExportMarkdown uses. An ordinary
// Document Studio memo must never print as a research report with no sources.
func TestOrdinaryDocumentPrintOmitsResearchReportChrome(t *testing.T) {
	t.Setenv("STRIDE_ORGANIZATION_NAME", "Acme Ventures")
	created := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	body := "# Q4 hiring memo\n\nWe should hire two engineers.\n\n## Budget\n\nTwo seats, one quarter.\n"
	memo := meetingMemoryEntry{
		ID: "os-artifact-plain-memo", Text: body, CreatedAt: created,
		Metadata: map[string]string{
			"title": "Q4 hiring memo", "type": artifactTypeMarkdown,
			"source": "document_studio", "mode": "document", "createdBy": "aj@shareability.com",
		},
	}

	page := renderResearchReportPrintHTML(memo)
	for _, reject := range []string{
		researchReportKicker,
		`<span class="kicker">`,
		`<nav class="contents">`,
		`<section class="sources" id="sources-appendix">`,
		"Sources appendix",
		"No verified external sources were recorded on this report.",
	} {
		if strings.Contains(page, reject) {
			t.Fatalf("an ordinary document's print page claims to be a research report — leaked %q:\n%s", reject, page)
		}
	}
	// The document itself still prints: cover, body flow and colophon.
	for _, want := range []string{
		`<h1 class="title">Q4 hiring memo</h1>`,
		`<p class="prepared">Prepared for Acme Ventures</p>`,
		"<h2>Budget</h2>",
		"We should hire two engineers.",
		`<footer class="colophon">Prepared by Scout for Acme Ventures · September 2, 2026</footer>`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("an ordinary document's print page lost %q:\n%s", want, page)
		}
	}

	// The two exports never drift: every research-only marker is either in
	// both passes or in neither.
	docx := documentExportMarkdown(memo)
	for _, marker := range []string{
		researchReportKicker,
		"Sources appendix",
		"No verified external sources were recorded on this report.",
		"Contents",
	} {
		if inPDF, inDOCX := strings.Contains(page, marker), strings.Contains(docx, marker); inPDF != inDOCX {
			t.Fatalf("PDF/DOCX drift on %q: pdf=%v docx=%v", marker, inPDF, inDOCX)
		}
	}

	// A real research report keeps every piece of that chrome — recognized by
	// its durable contract stamp, and by research mode alone for the reports
	// that carry no stamp.
	stamped := memo
	stamped.ID = "os-artifact-research-stamped"
	stamped.Metadata = map[string]string{"title": "Nordic mid-market", "type": artifactTypeMarkdown, "artifactContract": "research_brief_v2"}
	moded := memo
	moded.ID = "os-artifact-research-mode"
	moded.Metadata = map[string]string{"title": "Nordic mid-market", "type": artifactTypeMarkdown, "mode": "Research"}
	for _, report := range []meetingMemoryEntry{stamped, moded} {
		printed := renderResearchReportPrintHTML(report)
		for _, want := range []string{
			`<span class="kicker">` + researchReportKicker + `</span>`,
			`<nav class="contents"><h2>Contents</h2><ol>`,
			`<section class="sources" id="sources-appendix"><h2>Sources appendix</h2>`,
			"No verified external sources were recorded on this report.",
		} {
			if !strings.Contains(printed, want) {
				t.Fatalf("research report %s lost %q:\n%s", report.ID, want, printed)
			}
		}
	}
}
