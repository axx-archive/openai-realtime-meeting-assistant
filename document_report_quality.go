package main

// document_report_quality.go owns the rendered admission boundary for native
// Document Studio reports. The prose gate still runs once, before rendering;
// this boundary then reviews the exact paper pages and makes a deterministic
// delivery decision from those page scorecards. A text-only scorer never gets
// to overrule what the rendered jury actually saw.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	documentReportRenderContract      = "document_report_render_v1"
	documentReportJuryContract        = "document_render_jury_v1"
	documentReportJurySource          = "document_render_jury"
	documentReportReadyAverageFloor   = 8.5
	documentReportMinimumJurySeats    = 2
	documentReportRepairFixMaxChars   = 600
	documentReportRepairFixMaxCount   = 36
	documentReportRenderedAdmissionID = "rendered_admission"
	documentReportDraftRenderStageID  = "draft_render"
	documentReportJuryStageID         = "document_jury"
	documentReportPublishStageID      = "publish"
)

var documentReportRenderPollInterval = 2 * time.Second

func documentReportRenderWaitTimeout() time.Duration {
	return durationEnv("BONFIRE_DOCUMENT_REPORT_RENDER_WAIT", 2*time.Minute, time.Second)
}

var documentReportJuryDimensions = []string{
	"hierarchy",
	"density",
	"tables",
	"page_breaks",
	"orphans_widows",
	"captions",
	"citations_links",
	"accessibility_contrast",
	"print_pdf_completeness",
}

var documentReportJuryBlockerCodes = []string{
	"text_clipped",
	"unreadable_table",
	"bad_page_break",
	"orphan_or_widow",
	"detached_caption",
	"broken_or_unreadable_citation",
	"low_contrast",
	"missing_content",
	"print_truncation",
}

const documentReportJurySchema = `Return STRICT JSON only, no prose outside it:
{"pages":[{"page":1,"scores":{"hierarchy":0,"density":0,"tables":0,"page_breaks":0,"orphans_widows":0,"captions":0,"citations_links":0,"accessibility_contrast":0,"print_pdf_completeness":0},"fixes":["one executable page-specific change, or the literal word KEEP"],"blockers":["text_clipped"]}]}
Rules: score EVERY rendered page shown, 0-10, and include every score key exactly once. Judge the visible result, including whether a page appropriately has no table or caption; do not penalize their absence when none is called for. blockers may contain only text_clipped, unreadable_table, bad_page_break, orphan_or_widow, detached_caption, broken_or_unreadable_citation, low_contrast, missing_content, print_truncation. Every non-KEEP fix must be an executable page-specific edit someone can apply verbatim, not general advice.`

type documentReportPageScore struct {
	Page     int                `json:"page"`
	Scores   map[string]float64 `json:"scores"`
	Fixes    []string           `json:"fixes"`
	Blockers []string           `json:"blockers"`
}

type documentReportSeatScorecard struct {
	Pages []documentReportPageScore `json:"pages"`
}

type documentReportRepair struct {
	Page  int      `json:"page"`
	Fixes []string `json:"fixes"`
}

type documentReportJuryReadiness struct {
	Verdict        string
	BlockingPages  []int
	MinimumAverage float64
	ParsedSeats    int
	SeatIDs        []string
	Repairs        []documentReportRepair
}

type documentReportRenderBinding struct {
	ArtifactID       string
	SourceVersion    int
	ArtifactVersion  int
	ContentDigest    string
	CapabilityDigest string
	PrintDigest      string
	PDFAssetRef      string
	PageCount        int
	PagesDigest      string
}

type documentReportQualityReview struct {
	Verdict          string
	ArtifactID       string
	ArtifactVersion  int
	ContentDigest    string
	CapabilityDigest string
	PDFAssetRef      string
	PageCount        int
	PagesDigest      string
	JuryID           string
	JuryDigest       string
	SeatIDs          []string
	MinimumAverage   float64
	ParsedSeats      int
	Repairs          []documentReportRepair
}

func documentReportRenderedGateDecision(review documentReportQualityReview, resolveErr error, spec ProcessGateSpec, revisions int) goalGateDecision {
	if resolveErr != nil {
		return goalGateDecision{
			Outcome: goalGateOutcomeBlocked, Verdict: goalReviewFail,
			Reasons: "rendered document review could not be bound to the exact draft: " + compactAssistantLine(resolveErr.Error()),
		}
	}
	switch review.Verdict {
	case "needs_attention":
		return goalGateDecision{
			Outcome: goalGateOutcomeBlocked, Verdict: goalReviewFail,
			Reasons: "rendered document review needs attention; no quality score or delivery decision can be made",
		}
	case "needs_changes":
		outcome := goalGateOutcomeRevise
		maxRounds := spec.MaxRounds
		if maxRounds <= 0 {
			maxRounds = goalGateDefaultMaxRounds
		}
		if revisions >= maxRounds {
			outcome = goalGateOutcomeBlocked
		}
		return goalGateDecision{
			Outcome: outcome, Verdict: goalReviewRevise,
			Reasons: "rendered document jury found blocking issues on the exact reviewed pages",
			Score:   review.MinimumAverage, Gaps: review.repairLines(),
		}
	case "ready":
		return goalGateDecision{
			Outcome: goalGateOutcomeAccept, Verdict: goalReviewPass,
			Reasons: fmt.Sprintf("rendered document jury passed the exact reviewed pages at a minimum per-page average of %.2f (floor %.2f)", review.MinimumAverage, documentReportReadyAverageFloor),
			Score:   review.MinimumAverage,
		}
	default:
		return goalGateDecision{Outcome: goalGateOutcomeBlocked, Verdict: goalReviewFail, Reasons: "rendered document review returned an unsupported verdict"}
	}
}

func (review documentReportQualityReview) repairLines() []string {
	lines := make([]string, 0, len(review.Repairs))
	for _, repair := range review.Repairs {
		for _, fix := range repair.Fixes {
			lines = append(lines, fmt.Sprintf("page %d: %s", repair.Page, fix))
		}
	}
	return lines
}

func (review documentReportQualityReview) gateMetadata() map[string]string {
	return map[string]string{
		"reviewedDocumentArtifactId":       review.ArtifactID,
		"reviewedDocumentArtifactVersion":  strconv.Itoa(review.ArtifactVersion),
		"reviewedDocumentContentDigest":    review.ContentDigest,
		"reviewedDocumentCapabilityDigest": review.CapabilityDigest,
		"reviewedDocumentPdfAssetRef":      review.PDFAssetRef,
		"reviewedDocumentPageCount":        strconv.Itoa(review.PageCount),
		"reviewedDocumentPagesDigest":      review.PagesDigest,
		"documentJuryArtifactId":           review.JuryID,
		"documentJuryArtifactDigest":       review.JuryDigest,
	}
}

func documentReportBodyDigest(report meetingMemoryEntry) string {
	return sha256Hex([]byte(report.Text))
}

func documentReportPageNumber(name string) (int, error) {
	name = strings.TrimSpace(name)
	if !strings.HasPrefix(name, "page-") || !strings.HasSuffix(name, ".jpg") {
		return 0, fmt.Errorf("rendered document page name is invalid")
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(name, "page-"), ".jpg")
	if raw == "" {
		return 0, fmt.Errorf("rendered document page number is missing")
	}
	for _, digit := range raw {
		if digit < '0' || digit > '9' {
			return 0, fmt.Errorf("rendered document page number is invalid")
		}
	}
	page, err := strconv.Atoi(raw)
	if err != nil || page < 1 || name != fmt.Sprintf("page-%02d.jpg", page) {
		return 0, fmt.Errorf("rendered document page identity is not canonical")
	}
	return page, nil
}

func documentReportPageAssets(report meetingMemoryEntry) ([]artifactAsset, error) {
	pages := append([]artifactAsset(nil), artifactPageImageAssets(report)...)
	pageNumbers := make(map[string]int, len(pages))
	for _, page := range pages {
		number, err := documentReportPageNumber(page.Name)
		if err != nil {
			return nil, fmt.Errorf("rendered document pages are missing, duplicated, or invalid")
		}
		pageNumbers[page.Ref+"\x00"+page.Name] = number
	}
	sort.Slice(pages, func(i, j int) bool {
		iNumber := pageNumbers[pages[i].Ref+"\x00"+pages[i].Name]
		jNumber := pageNumbers[pages[j].Ref+"\x00"+pages[j].Name]
		if iNumber != jNumber {
			return iNumber < jNumber
		}
		if pages[i].Name != pages[j].Name {
			return pages[i].Name < pages[j].Name
		}
		return pages[i].Ref < pages[j].Ref
	})
	seen := map[int]bool{}
	for index, page := range pages {
		pageNumber := pageNumbers[page.Ref+"\x00"+page.Name]
		expectedName := fmt.Sprintf("page-%02d.jpg", index+1)
		if !validBlobRef(page.Ref) || !strings.EqualFold(strings.TrimSpace(page.Mime), "image/jpeg") || pageNumber != index+1 || strings.TrimSpace(page.Name) != expectedName || seen[pageNumber] {
			return nil, fmt.Errorf("rendered document pages are missing, duplicated, or invalid")
		}
		seen[pageNumber] = true
	}
	return pages, nil
}

func documentReportPagesDigest(pages []artifactAsset) string {
	canonical, _ := json.Marshal(pages)
	return sha256Hex(canonical)
}

func documentReportPDFAsset(report meetingMemoryEntry, ref string) (artifactAsset, bool) {
	ref = strings.TrimSpace(ref)
	for _, asset := range artifactAssets(report) {
		if asset.Ref == ref && strings.EqualFold(strings.TrimSpace(asset.Mime), "application/pdf") && strings.EqualFold(strings.TrimSpace(asset.Kind), "pdf") {
			return asset, true
		}
	}
	return artifactAsset{}, false
}

func validateDocumentReportCompletedRender(report meetingMemoryEntry) (documentReportRenderBinding, error) {
	binding := documentReportRenderBinding{
		ArtifactID: report.ID, ArtifactVersion: artifactVersion(report),
		ContentDigest: documentReportBodyDigest(report), CapabilityDigest: artifactCapabilityDigest(report),
		PDFAssetRef: strings.TrimSpace(report.Metadata[renderPDFAssetRefMetadataKey]),
	}
	if strings.TrimSpace(report.ID) == "" || strings.TrimSpace(report.Text) == "" {
		return binding, fmt.Errorf("the document draft is missing")
	}
	if !strings.EqualFold(strings.TrimSpace(report.Metadata["renderStatus"]), renderJobStatusComplete) || normalizeRenderJobKind(report.Metadata["renderKind"]) != renderJobKindPaper {
		return binding, fmt.Errorf("the document PDF render is not complete")
	}
	printHTML := renderResearchReportPrintHTML(report)
	binding.PrintDigest = renderPDFContentDigest(renderJobKindPaper, printHTML)
	if strings.TrimSpace(report.Metadata[renderSourceContentDigestMetadataKey]) != binding.PrintDigest {
		return binding, fmt.Errorf("the completed PDF is bound to different document content")
	}
	boundVersion, err := strconv.Atoi(strings.TrimSpace(report.Metadata[renderPDFArtifactVersionMetadataKey]))
	if err != nil || boundVersion != binding.ArtifactVersion {
		return binding, fmt.Errorf("the PDF is not bound to the current document revision")
	}
	sourceVersion, err := strconv.Atoi(strings.TrimSpace(report.Metadata[renderPDFSourceVersionMetadataKey]))
	if err != nil || sourceVersion < 1 || strings.TrimSpace(report.Metadata[renderSourceArtifactVersionMetadataKey]) != strconv.Itoa(sourceVersion) {
		return binding, fmt.Errorf("the PDF has no exact source revision binding")
	}
	binding.SourceVersion = sourceVersion
	if !validBlobRef(binding.PDFAssetRef) {
		return binding, fmt.Errorf("the completed render has no valid PDF asset")
	}
	pdfAsset, ok := documentReportPDFAsset(report, binding.PDFAssetRef)
	if !ok || pdfAsset.SourceArtifactVersion != sourceVersion {
		return binding, fmt.Errorf("the bound PDF asset is not attached to the document")
	}
	pdfBytes, pdfMetadata, pdfErr := getBlob(binding.PDFAssetRef)
	if pdfErr != nil || !strings.EqualFold(strings.TrimSpace(pdfMetadata.Mime), "application/pdf") || !strings.HasPrefix(string(pdfBytes), "%PDF") {
		return binding, fmt.Errorf("the bound PDF asset is unavailable or incomplete")
	}
	pageCount, err := strconv.Atoi(strings.TrimSpace(report.Metadata["renderPageCount"]))
	if err != nil || pageCount < 1 {
		return binding, fmt.Errorf("the completed render has no valid page count")
	}
	pages, err := documentReportPageAssets(report)
	if err != nil {
		return binding, err
	}
	pageImages, imageErr := strconv.Atoi(strings.TrimSpace(report.Metadata["renderPageImages"]))
	if imageErr != nil || pageImages != pageCount || len(pages) != pageCount {
		return binding, fmt.Errorf("the document render is incomplete: expected %d page image(s), found %d", pageCount, len(pages))
	}
	for _, page := range pages {
		if page.SourceArtifactVersion != sourceVersion {
			return binding, fmt.Errorf("the document render contains a page from a different source revision")
		}
	}
	binding.PageCount = pageCount
	binding.PagesDigest = documentReportPagesDigest(pages)
	return binding, nil
}

func documentReportStageCandidate(app *kanbanBoardApp, plan *goalPlan, parentID string) (meetingMemoryEntry, error) {
	if app == nil || plan == nil {
		return meetingMemoryEntry{}, fmt.Errorf("artifact memory or plan is unavailable")
	}
	write := plan.subtaskByID("write")
	if write == nil || write.Status != subtaskComplete || strings.TrimSpace(write.ArtifactID) == "" {
		return meetingMemoryEntry{}, fmt.Errorf("the editable document draft is missing or incomplete")
	}
	report, ok := app.osArtifactByID(write.ArtifactID)
	if !ok || strings.TrimSpace(report.Metadata["goalParentId"]) != strings.TrimSpace(parentID) || strings.TrimSpace(report.Metadata["goalSubtaskId"]) != "write" {
		return meetingMemoryEntry{}, fmt.Errorf("the editable document draft is missing or belongs to a different run")
	}
	contract := firstNonEmptyString(strings.TrimSpace(report.Metadata["outputContract"]), strings.TrimSpace(report.Metadata["artifactContract"]))
	if contract != documentReportOutputContract || !strings.HasPrefix(strings.TrimSpace(report.Text), "# ") {
		return meetingMemoryEntry{}, fmt.Errorf("the draft does not satisfy %s", documentReportOutputContract)
	}
	return report, nil
}

func documentReportTextGatePassed(app *kanbanBoardApp, plan *goalPlan, parentID string) error {
	gate := plan.subtaskByID("quality_gate")
	if gate == nil || gate.Status != subtaskComplete || gate.Review == nil || gate.Review.Verdict != goalReviewPass || strings.TrimSpace(gate.ArtifactID) == "" {
		return fmt.Errorf("the pre-render text quality gate is missing or did not pass")
	}
	record, ok := app.osArtifactByID(gate.ArtifactID)
	if !ok || record.Metadata["processStage"] != "quality_gate" || strings.TrimSpace(record.Metadata["goalParentId"]) != strings.TrimSpace(parentID) {
		return fmt.Errorf("the pre-render text quality record is missing or belongs to a different run")
	}
	return nil
}

func documentReportAttentionRecord(reason string, report meetingMemoryEntry, binding documentReportRenderBinding) (string, map[string]string, error) {
	metadata := map[string]string{
		"reviewVerdict":            "needs_attention",
		"attentionReason":          compactAssistantLine(reason),
		"documentArtifactId":       strings.TrimSpace(report.ID),
		"documentArtifactVersion":  strconv.Itoa(artifactVersion(report)),
		"documentContentDigest":    documentReportBodyDigest(report),
		"documentCapabilityDigest": artifactCapabilityDigest(report),
	}
	if binding.ArtifactID != "" {
		metadata["documentArtifactVersion"] = strconv.Itoa(binding.ArtifactVersion)
		metadata["documentContentDigest"] = binding.ContentDigest
		metadata["documentCapabilityDigest"] = binding.CapabilityDigest
		metadata["renderPdfAssetRef"] = binding.PDFAssetRef
		metadata["renderPageCount"] = strconv.Itoa(binding.PageCount)
		metadata["renderPagesDigest"] = binding.PagesDigest
	}
	return strings.Join([]string{
		"Rendered document review needs attention",
		"",
		"No delivery decision was made: " + compactAssistantLine(reason),
		"Retry after the render or review provider recovers; the document cannot pass on text scoring alone.",
	}, "\n"), metadata, nil
}

// compileDocumentReportDraftRender starts one exact, revision-bound paper
// render and waits for the PDF plus every page image. Every unavailable or
// failed render path lands as needs_attention so the downstream deterministic
// admission can hold without pretending it saw pages.
func compileDocumentReportDraftRender(app *kanbanBoardApp, plan *goalPlan, parentID string, _ ProcessStage) (string, map[string]string, error) {
	report, err := documentReportStageCandidate(app, plan, parentID)
	if err != nil {
		return "", nil, err
	}
	if err := documentReportTextGatePassed(app, plan, parentID); err != nil {
		return "", nil, err
	}
	if existing, renderErr := validateDocumentReportCompletedRender(report); renderErr == nil {
		return documentReportRenderRecord(existing)
	}
	if !renderSidecarAvailable() {
		return documentReportAttentionRecord("render sidecar not available", report, documentReportRenderBinding{})
	}

	printHTML := renderResearchReportPrintHTML(report)
	binding := renderPDFJobBinding{
		ArtifactID: report.ID, Kind: renderJobKindPaper, HTML: printHTML,
		Title:                 firstNonEmptyString(report.Metadata["title"], "Document report"),
		SourceArtifactVersion: artifactVersion(report),
		SourceContentDigest:   renderPDFContentDigest(renderJobKindPaper, printHTML),
	}
	if pendingID := strings.TrimSpace(report.Metadata["renderJobId"]); pendingID != "" {
		if _, active, activeErr := activeRenderJobForBinding(report, binding); activeErr == nil && active {
			// Keep waiting on the exact already-queued revision.
		} else {
			if _, retireErr := retireStaleRenderJob(report.ID, pendingID); retireErr != nil {
				return documentReportAttentionRecord("the stale render job could not be retired", report, documentReportRenderBinding{})
			}
			report, _ = app.osArtifactByID(report.ID)
		}
	}
	if strings.TrimSpace(report.Metadata["renderJobId"]) == "" {
		job, _, recoverErr := recoverOrEnqueueStudioBoundRenderExportPDFJob(binding)
		if recoverErr != nil {
			return documentReportAttentionRecord("PDF render recovery failed: "+recoverErr.Error(), report, documentReportRenderBinding{})
		}
		renderMetadata := queuedRenderMetadataForInput(report, job.ID, renderJobKindPaper, binding.SourceContentDigest)
		if job.Status == renderJobStatusRunning {
			renderMetadata["renderStatus"] = renderJobStatusRunning
		} else if job.Status == renderJobStatusComplete {
			renderMetadata = recoveredCompletedRenderMetadataForInput(report, job.ID, renderJobKindPaper, binding.SourceContentDigest)
		}
		header := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(report))
		updated, changed, stampErr := app.memory.updateOSArtifactMetadataIfHeaderAndMetadataMatch(header, map[string]string{
			"renderJobId": strings.TrimSpace(report.Metadata["renderJobId"]),
		}, report.ID, renderMetadata)
		if stampErr != nil || !changed {
			return documentReportAttentionRecord("the render job could not bind to the exact document revision", report, documentReportRenderBinding{})
		}
		report = updated
		if job.Status == renderJobStatusComplete {
			return documentReportAttentionRecord("the exact PDF render completed, but its attachment callback is not yet confirmed", report, documentReportRenderBinding{})
		}
	}

	deadline := time.Now().Add(documentReportRenderWaitTimeout())
	for {
		current, ok := app.osArtifactByID(report.ID)
		if !ok {
			return "", nil, fmt.Errorf("the document disappeared while rendering")
		}
		if documentReportBodyDigest(current) != documentReportBodyDigest(report) {
			return documentReportAttentionRecord("the document changed while its PDF was rendering", current, documentReportRenderBinding{})
		}
		if completed, completedErr := validateDocumentReportCompletedRender(current); completedErr == nil {
			return documentReportRenderRecord(completed)
		}
		status := strings.ToLower(strings.TrimSpace(current.Metadata["renderStatus"]))
		if status == renderJobStatusFailed || status == renderJobStatusStale {
			reason := firstNonEmptyString(strings.TrimSpace(current.Metadata["renderError"]), "the PDF render "+status)
			return documentReportAttentionRecord(reason, current, documentReportRenderBinding{})
		}
		if time.Now().After(deadline) {
			return documentReportAttentionRecord("the PDF render did not finish within "+documentReportRenderWaitTimeout().String(), current, documentReportRenderBinding{})
		}
		time.Sleep(documentReportRenderPollInterval)
	}
}

func documentReportRenderRecord(binding documentReportRenderBinding) (string, map[string]string, error) {
	metadata := map[string]string{
		"artifactContract":         documentReportRenderContract,
		"reviewVerdict":            "ready",
		"documentArtifactId":       binding.ArtifactID,
		"documentSourceVersion":    strconv.Itoa(binding.SourceVersion),
		"documentArtifactVersion":  strconv.Itoa(binding.ArtifactVersion),
		"documentContentDigest":    binding.ContentDigest,
		"documentCapabilityDigest": binding.CapabilityDigest,
		"renderPrintDigest":        binding.PrintDigest,
		"renderPdfAssetRef":        binding.PDFAssetRef,
		"renderPageCount":          strconv.Itoa(binding.PageCount),
		"renderPagesDigest":        binding.PagesDigest,
	}
	body := strings.Join([]string{
		"Exact document draft rendered",
		"",
		"- Document: " + binding.ArtifactID + " @ v" + strconv.Itoa(binding.ArtifactVersion),
		"- PDF: " + binding.PDFAssetRef,
		"- Rendered pages: " + strconv.Itoa(binding.PageCount),
		"- The page jury must bind this exact revision, content digest, PDF, and ordered page set.",
	}, "\n")
	return body, metadata, nil
}

func validateDocumentReportSeat(card documentReportSeatScorecard, expectedPages []int) error {
	if len(card.Pages) != len(expectedPages) {
		return fmt.Errorf("scorecard carries %d pages, want %d", len(card.Pages), len(expectedPages))
	}
	expected := map[int]bool{}
	for _, page := range expectedPages {
		expected[page] = true
	}
	seen := map[int]bool{}
	for _, page := range card.Pages {
		if !expected[page.Page] || seen[page.Page] {
			return fmt.Errorf("scorecard page coverage is missing, duplicated, or out of range")
		}
		seen[page.Page] = true
		if len(page.Scores) != len(documentReportJuryDimensions) {
			return fmt.Errorf("page %d does not score every required dimension", page.Page)
		}
		for _, dimension := range documentReportJuryDimensions {
			score, ok := page.Scores[dimension]
			if !ok || math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 10 {
				return fmt.Errorf("page %d has an invalid %s score", page.Page, dimension)
			}
		}
		if len(page.Fixes) == 0 {
			return fmt.Errorf("page %d has no executable fix or KEEP verdict", page.Page)
		}
		for _, fix := range page.Fixes {
			fix = strings.TrimSpace(fix)
			if fix == "KEEP" {
				continue
			}
			if !validDocumentReportRepair(fix) {
				return fmt.Errorf("page %d carries a non-executable repair", page.Page)
			}
		}
		for _, blocker := range page.Blockers {
			if !slices.Contains(documentReportJuryBlockerCodes, strings.ToLower(strings.TrimSpace(blocker))) {
				return fmt.Errorf("page %d carries unsupported blocker %q", page.Page, blocker)
			}
		}
	}
	return nil
}

func validDocumentReportRepair(fix string) bool {
	fix = strings.TrimSpace(fix)
	if fix == "" || len(fix) > documentReportRepairFixMaxChars || len(strings.Fields(fix)) < 4 {
		return false
	}
	lower := strings.ToLower(fix)
	for _, vague := range []string{"make it better", "improve the layout", "fix the layout", "adjust as needed", "polish this page"} {
		if lower == vague {
			return false
		}
	}
	verb := strings.Trim(strings.Fields(lower)[0], "-:,.()")
	return slices.Contains([]string{"add", "align", "break", "change", "convert", "correct", "delete", "increase", "keep", "lower", "move", "place", "raise", "reduce", "reflow", "remove", "repeat", "replace", "resize", "rewrite", "set", "shorten", "split", "use"}, verb)
}

func documentReportJuryPersonas() []goalPanelPersona {
	return []goalPanelPersona{
		{Name: "editorial_designer", System: "You are an elite editorial designer judging the actual rendered report pages. Inspect hierarchy, density, tables, page breaks, orphans and widows, captions, citations and links, contrast, and print completeness. Score every visible page and prescribe exact layout/type edits."},
		{Name: "accessibility_print_critic", System: "You are a print-production and accessibility critic judging the actual rendered report pages. Look for clipping, missing content, weak contrast, unreadable tables or citations, detached captions, broken pagination, and PDF completeness. Score every page and give executable repairs."},
		{Name: "decision_reader", System: "You are the intended senior reader seeing the finished rendered report. Judge whether visual hierarchy and density let you follow the argument, inspect evidence and tables, and act without fighting the page. Also catch page-break, citation, caption, contrast, and print defects. Score every page and give executable repairs."},
	}
}

func documentReportExpectedPages(first int, count int) []int {
	result := make([]int, count)
	for index := range result {
		result[index] = first + index
	}
	return result
}

func documentReportSeatIDs() map[string]bool {
	result := map[string]bool{}
	for _, persona := range documentReportJuryPersonas() {
		result[persona.Name] = true
	}
	return result
}

func evaluateDocumentReportJury(voices []goalPanelVoice, expectedPages int) documentReportJuryReadiness {
	type pageVotes struct {
		total    float64
		count    int
		blockers map[string]int
		fixes    []string
	}
	result := documentReportJuryReadiness{Verdict: "ready", MinimumAverage: 10}
	validSeats := documentReportSeatIDs()
	seenSeats := map[string]bool{}
	pages := map[int]*pageVotes{}
	for _, voice := range voices {
		seat := strings.TrimSpace(voice.Persona)
		if voice.Err != nil || !validSeats[seat] || seenSeats[seat] {
			continue
		}
		var card documentReportSeatScorecard
		if json.Unmarshal([]byte(stripJSONCodeFence(strings.TrimSpace(voice.Text))), &card) != nil || validateDocumentReportSeat(card, documentReportExpectedPages(1, expectedPages)) != nil {
			continue
		}
		seenSeats[seat] = true
		result.SeatIDs = append(result.SeatIDs, seat)
		for _, page := range card.Pages {
			vote := pages[page.Page]
			if vote == nil {
				vote = &pageVotes{blockers: map[string]int{}}
				pages[page.Page] = vote
			}
			for _, dimension := range documentReportJuryDimensions {
				vote.total += page.Scores[dimension]
				vote.count++
			}
			seenBlockers := map[string]bool{}
			for _, blocker := range page.Blockers {
				blocker = strings.ToLower(strings.TrimSpace(blocker))
				if !seenBlockers[blocker] {
					vote.blockers[blocker]++
					seenBlockers[blocker] = true
				}
			}
			for _, fix := range page.Fixes {
				fix = strings.TrimSpace(fix)
				if fix != "KEEP" && validDocumentReportRepair(fix) && !slices.Contains(vote.fixes, fix) {
					vote.fixes = append(vote.fixes, fix)
				}
			}
		}
	}
	sort.Strings(result.SeatIDs)
	result.ParsedSeats = len(result.SeatIDs)
	if result.ParsedSeats < documentReportMinimumJurySeats || expectedPages < 1 {
		result.Verdict = "needs_attention"
		result.MinimumAverage = 0
		return result
	}
	for page := 1; page <= expectedPages; page++ {
		vote := pages[page]
		expectedScores := result.ParsedSeats * len(documentReportJuryDimensions)
		if vote == nil || vote.count != expectedScores {
			result.Verdict = "needs_attention"
			result.MinimumAverage = 0
			return result
		}
		average := vote.total / float64(vote.count)
		if average < result.MinimumAverage {
			result.MinimumAverage = average
		}
		blocking := average < documentReportReadyAverageFloor
		for _, count := range vote.blockers {
			if count >= documentReportMinimumJurySeats {
				blocking = true
			}
		}
		if blocking {
			result.BlockingPages = append(result.BlockingPages, page)
		}
	}
	if len(result.BlockingPages) == 0 {
		return result
	}
	result.Verdict = "needs_changes"
	totalFixes := 0
	for _, page := range result.BlockingPages {
		fixes := pages[page].fixes
		if len(fixes) == 0 || totalFixes+len(fixes) > documentReportRepairFixMaxCount {
			result.Verdict = "needs_attention"
			result.Repairs = nil
			return result
		}
		totalFixes += len(fixes)
		result.Repairs = append(result.Repairs, documentReportRepair{Page: page, Fixes: append([]string(nil), fixes...)})
	}
	return result
}

type documentReportJuryBatch struct {
	Pages  []int
	Voices []goalPanelVoice
}

func mergeDocumentReportJuryBatches(records []documentReportJuryBatch, expectedPages int) []goalPanelVoice {
	personas := documentReportJuryPersonas()
	merged := make([]goalPanelVoice, 0, len(personas))
	for _, persona := range personas {
		voice := goalPanelVoice{Persona: persona.Name}
		card := documentReportSeatScorecard{}
		for batchIndex, record := range records {
			var part *goalPanelVoice
			for index := range record.Voices {
				if record.Voices[index].Persona == persona.Name {
					part = &record.Voices[index]
					break
				}
			}
			if part == nil || part.Err != nil {
				voice.Err = fmt.Errorf("jury batch %d did not return the %s seat", batchIndex+1, persona.Name)
				break
			}
			var batchCard documentReportSeatScorecard
			if json.Unmarshal([]byte(stripJSONCodeFence(strings.TrimSpace(part.Text))), &batchCard) != nil || validateDocumentReportSeat(batchCard, record.Pages) != nil {
				voice.Err = fmt.Errorf("jury batch %d returned an invalid %s scorecard", batchIndex+1, persona.Name)
				break
			}
			card.Pages = append(card.Pages, batchCard.Pages...)
		}
		if voice.Err == nil {
			sort.Slice(card.Pages, func(i, j int) bool { return card.Pages[i].Page < card.Pages[j].Page })
			if err := validateDocumentReportSeat(card, documentReportExpectedPages(1, expectedPages)); err != nil {
				voice.Err = err
			} else if raw, err := json.Marshal(card); err != nil {
				voice.Err = err
			} else {
				voice.Text = string(raw)
			}
		}
		merged = append(merged, voice)
	}
	return merged
}

func runDocumentReportJury(ctx context.Context, app *kanbanBoardApp, goalID string, report meetingMemoryEntry, binding documentReportRenderBinding) (meetingMemoryEntry, error) {
	pages, err := documentReportPageAssets(report)
	if err != nil || len(pages) != binding.PageCount || documentReportPagesDigest(pages) != binding.PagesDigest {
		return meetingMemoryEntry{}, fmt.Errorf("the page set changed before the rendered document jury could see it")
	}
	var records []documentReportJuryBatch
	for cursor := 0; cursor < len(pages); {
		pageContent := []openAIInputContent{}
		pageNumbers := []int{}
		batchBytes := 0
		for cursor < len(pages) && slideJuryBatchCanAccept(len(pageNumbers), batchBytes, 1) {
			data, _, blobErr := getBlob(pages[cursor].Ref)
			if blobErr != nil || len(data) == 0 {
				return meetingMemoryEntry{}, fmt.Errorf("rendered page %d is unavailable", cursor+1)
			}
			if !slideJuryBatchCanAccept(len(pageNumbers), batchBytes, len(data)) {
				if len(pageNumbers) == 0 {
					return meetingMemoryEntry{}, fmt.Errorf("rendered page %d exceeds the review provider image budget", cursor+1)
				}
				break
			}
			pageNumber := cursor + 1
			pageContent = append(pageContent,
				openAIInputContent{Type: "input_text", Text: fmt.Sprintf("Rendered report page %d of %d:", pageNumber, len(pages))},
				openAIInputContent{Type: "input_image", ImageURL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)},
			)
			pageNumbers = append(pageNumbers, pageNumber)
			batchBytes += len(data)
			cursor++
		}
		engine := newGoalEngine(app)
		engine.openAIResponder = withSlideJuryOpenAIPageContent(engine.openAIResponder, pageContent)
		outcome, panelErr := engine.runGoalPanelVoices(ctx, goalPanelSpec{
			Task:   fmt.Sprintf("Judge rendered page(s) %s of %d from the finished document. Score every attached page using its exact global page number. You are seeing the print/PDF output, not Markdown source.", slideJuryPageList(pageNumbers), len(pages)),
			Schema: documentReportJurySchema, Personas: documentReportJuryPersonas(), Review: true,
			MinSuccessfulSeats: documentReportMinimumJurySeats,
		})
		if panelErr != nil {
			return meetingMemoryEntry{}, fmt.Errorf("rendered document jury failed for page(s) %s: %w", slideJuryPageList(pageNumbers), panelErr)
		}
		records = append(records, documentReportJuryBatch{Pages: pageNumbers, Voices: outcome.Voices})
	}
	voices := mergeDocumentReportJuryBatches(records, len(pages))
	readiness := evaluateDocumentReportJury(voices, len(pages))
	var body strings.Builder
	body.WriteString("# Rendered document jury\n\n")
	body.WriteString(fmt.Sprintf("Verdict: %s. Minimum per-page average: %.2f. Valid independent seats: %d.\n", readiness.Verdict, readiness.MinimumAverage, readiness.ParsedSeats))
	body.WriteString("\n## Exact seat scorecards\n")
	for _, voice := range voices {
		body.WriteString("\n### " + voice.Persona + "\n")
		if voice.Err != nil {
			body.WriteString("Unavailable: " + compactAssistantLine(voice.Err.Error()) + "\n")
		} else {
			body.WriteString(strings.TrimSpace(voice.Text) + "\n")
		}
	}
	repairs := readiness.Repairs
	if repairs == nil {
		repairs = []documentReportRepair{}
	}
	repairsRaw, _ := json.Marshal(repairs)
	seatIDs := strings.Join(readiness.SeatIDs, ",")
	metadata := map[string]string{
		"artifactContract":         documentReportJuryContract,
		"type":                     artifactTypeMarkdown,
		"source":                   documentReportJurySource,
		"goalId":                   strings.TrimSpace(goalID),
		"documentArtifactId":       binding.ArtifactID,
		"documentSourceVersion":    strconv.Itoa(binding.SourceVersion),
		"documentArtifactVersion":  strconv.Itoa(binding.ArtifactVersion),
		"documentContentDigest":    binding.ContentDigest,
		"documentCapabilityDigest": binding.CapabilityDigest,
		"renderPdfAssetRef":        binding.PDFAssetRef,
		"renderPageCount":          strconv.Itoa(binding.PageCount),
		"renderPagesDigest":        binding.PagesDigest,
		"reviewVerdict":            readiness.Verdict,
		"blockingPages":            slideJuryPageList(readiness.BlockingPages),
		"minimumAverage":           strconv.FormatFloat(readiness.MinimumAverage, 'f', 2, 64),
		"parsedSeats":              strconv.Itoa(readiness.ParsedSeats),
		"jurySeatIds":              seatIDs,
		"repairFixes":              string(repairsRaw),
	}
	metadata["jurySeatsDigest"] = sha256Hex([]byte(body.String()))
	filed, appended, err := app.createOSArtifactWithMetadata("workflow", "Rendered document jury", body.String(), scoutParticipantName, metadata)
	if err != nil || !appended || strings.TrimSpace(filed.ID) == "" {
		return meetingMemoryEntry{}, fmt.Errorf("the rendered document jury record was not saved")
	}
	return filed, nil
}

func compileDocumentReportJury(app *kanbanBoardApp, plan *goalPlan, parentID string, _ ProcessStage) (string, map[string]string, error) {
	report, err := documentReportStageCandidate(app, plan, parentID)
	if err != nil {
		return "", nil, err
	}
	renderStage := plan.subtaskByID(documentReportDraftRenderStageID)
	if renderStage == nil || renderStage.Status != subtaskComplete || strings.TrimSpace(renderStage.ArtifactID) == "" {
		return "", nil, fmt.Errorf("the exact draft render stage is missing or incomplete")
	}
	renderRecord, ok := app.osArtifactByID(renderStage.ArtifactID)
	if !ok || renderRecord.Metadata["processStage"] != documentReportDraftRenderStageID || strings.TrimSpace(renderRecord.Metadata["goalParentId"]) != strings.TrimSpace(parentID) {
		return "", nil, fmt.Errorf("the exact draft render record is missing or belongs to a different run")
	}
	if renderRecord.Metadata["reviewVerdict"] == "needs_attention" {
		return documentReportAttentionRecord(firstNonEmptyString(renderRecord.Metadata["attentionReason"], "the exact draft render is unavailable"), report, documentReportRenderBinding{})
	}
	binding, renderErr := validateDocumentReportCompletedRender(report)
	if renderErr != nil || !documentReportRenderRecordMatches(renderRecord, binding) {
		reason := "the current document no longer matches the exact draft render record"
		if renderErr != nil {
			reason = renderErr.Error()
		}
		return documentReportAttentionRecord(reason, report, binding)
	}
	if strings.TrimSpace(app.currentOpenAIAPIKey()) == "" {
		return documentReportAttentionRecord("no review provider key is configured", report, binding)
	}
	ctx, cancel := context.WithTimeout(context.Background(), orchestratorTimeout())
	defer cancel()
	jury, juryErr := runDocumentReportJury(ctx, app, parentID, report, binding)
	if juryErr != nil {
		return documentReportAttentionRecord("the rendered document jury failed: "+compactAssistantLine(juryErr.Error()), report, binding)
	}
	metadata := map[string]string{
		"documentJuryArtifactId":     jury.ID,
		"documentJuryArtifactDigest": artifactCapabilityDigest(jury),
		"reviewVerdict":              jury.Metadata["reviewVerdict"],
		"blockingPages":              jury.Metadata["blockingPages"],
		"minimumAverage":             jury.Metadata["minimumAverage"],
		"parsedSeats":                jury.Metadata["parsedSeats"],
		"jurySeatIds":                jury.Metadata["jurySeatIds"],
		"jurySeatsDigest":            jury.Metadata["jurySeatsDigest"],
		"repairFixes":                jury.Metadata["repairFixes"],
		"documentArtifactId":         binding.ArtifactID,
		"documentSourceVersion":      strconv.Itoa(binding.SourceVersion),
		"documentArtifactVersion":    strconv.Itoa(binding.ArtifactVersion),
		"documentContentDigest":      binding.ContentDigest,
		"documentCapabilityDigest":   binding.CapabilityDigest,
		"renderPdfAssetRef":          binding.PDFAssetRef,
		"renderPageCount":            strconv.Itoa(binding.PageCount),
		"renderPagesDigest":          binding.PagesDigest,
	}
	body := strings.Join([]string{
		"Rendered document jury completed",
		"",
		"- Scoreboard: " + jury.ID,
		"- Verdict: " + jury.Metadata["reviewVerdict"],
		"- Minimum per-page average: " + jury.Metadata["minimumAverage"],
		"- Valid independent seats: " + jury.Metadata["parsedSeats"],
		"- Every score is bound to the exact PDF page set named by the render record.",
	}, "\n")
	return body, metadata, nil
}

func documentReportRenderRecordMatches(record meetingMemoryEntry, binding documentReportRenderBinding) bool {
	return strings.TrimSpace(record.Metadata["reviewVerdict"]) == "ready" &&
		strings.TrimSpace(record.Metadata["documentArtifactId"]) == binding.ArtifactID &&
		strings.TrimSpace(record.Metadata["documentSourceVersion"]) == strconv.Itoa(binding.SourceVersion) &&
		strings.TrimSpace(record.Metadata["documentArtifactVersion"]) == strconv.Itoa(binding.ArtifactVersion) &&
		strings.TrimSpace(record.Metadata["documentContentDigest"]) == binding.ContentDigest &&
		strings.TrimSpace(record.Metadata["documentCapabilityDigest"]) == binding.CapabilityDigest &&
		strings.TrimSpace(record.Metadata["renderPdfAssetRef"]) == binding.PDFAssetRef &&
		strings.TrimSpace(record.Metadata["renderPageCount"]) == strconv.Itoa(binding.PageCount) &&
		strings.TrimSpace(record.Metadata["renderPagesDigest"]) == binding.PagesDigest
}

func parseDocumentReportSeatIDs(raw string) ([]string, error) {
	valid := documentReportSeatIDs()
	seen := map[string]bool{}
	var seats []string
	for _, value := range strings.Split(strings.TrimSpace(raw), ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !valid[value] || seen[value] {
			return nil, fmt.Errorf("rendered jury seats are unknown or duplicated")
		}
		seen[value] = true
		seats = append(seats, value)
	}
	sort.Strings(seats)
	if len(seats) < documentReportMinimumJurySeats {
		return nil, fmt.Errorf("rendered jury has fewer than two distinct valid seats")
	}
	return seats, nil
}

func documentReportJuryVoicesFromRecord(jury meetingMemoryEntry) []goalPanelVoice {
	text := strings.ReplaceAll(jury.Text, "\r\n", "\n")
	personas := documentReportJuryPersonas()
	voices := make([]goalPanelVoice, 0, len(personas))
	for _, persona := range personas {
		marker := "\n### " + persona.Name + "\n"
		start := strings.Index(text, marker)
		if start < 0 {
			voices = append(voices, goalPanelVoice{Persona: persona.Name, Err: fmt.Errorf("seat scorecard is missing")})
			continue
		}
		start += len(marker)
		end := len(text)
		if next := strings.Index(text[start:], "\n### "); next >= 0 {
			end = start + next
		}
		payload := strings.TrimSpace(text[start:end])
		if !strings.HasPrefix(payload, "{") {
			voices = append(voices, goalPanelVoice{Persona: persona.Name, Err: fmt.Errorf("seat scorecard is unavailable")})
			continue
		}
		voices = append(voices, goalPanelVoice{Persona: persona.Name, Text: payload})
	}
	return voices
}

func validateDocumentReportJuryReadinessMetadata(jury meetingMemoryEntry, expectedPages int) (documentReportJuryReadiness, error) {
	readiness := evaluateDocumentReportJury(documentReportJuryVoicesFromRecord(jury), expectedPages)
	repairs := readiness.Repairs
	if repairs == nil {
		repairs = []documentReportRepair{}
	}
	repairsRaw, _ := json.Marshal(repairs)
	if strings.TrimSpace(jury.Metadata["reviewVerdict"]) != readiness.Verdict ||
		strings.TrimSpace(jury.Metadata["blockingPages"]) != slideJuryPageList(readiness.BlockingPages) ||
		strings.TrimSpace(jury.Metadata["minimumAverage"]) != strconv.FormatFloat(readiness.MinimumAverage, 'f', 2, 64) ||
		strings.TrimSpace(jury.Metadata["parsedSeats"]) != strconv.Itoa(readiness.ParsedSeats) ||
		strings.TrimSpace(jury.Metadata["jurySeatIds"]) != strings.Join(readiness.SeatIDs, ",") ||
		strings.TrimSpace(jury.Metadata["repairFixes"]) != string(repairsRaw) {
		return documentReportJuryReadiness{}, fmt.Errorf("rendered jury metadata does not match its exact seat scorecards")
	}
	return readiness, nil
}

func decodeDocumentReportRepairs(raw string, blockingPages []int) ([]documentReportRepair, error) {
	var repairs []documentReportRepair
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &repairs) != nil || len(repairs) != len(blockingPages) || len(repairs) == 0 {
		return nil, fmt.Errorf("needs_changes jury has no complete structured repairs")
	}
	seenPages := map[int]bool{}
	total := 0
	for _, repair := range repairs {
		if seenPages[repair.Page] || !slices.Contains(blockingPages, repair.Page) || len(repair.Fixes) == 0 {
			return nil, fmt.Errorf("jury repair pages do not match blocking pages")
		}
		seenPages[repair.Page] = true
		seenFixes := map[string]bool{}
		for _, fix := range repair.Fixes {
			fix = strings.TrimSpace(fix)
			if !validDocumentReportRepair(fix) || seenFixes[fix] {
				return nil, fmt.Errorf("jury repairs are duplicated or non-executable")
			}
			seenFixes[fix] = true
			total++
		}
	}
	if total == 0 || total > documentReportRepairFixMaxCount {
		return nil, fmt.Errorf("jury repairs exceed the bounded repair budget")
	}
	sort.Slice(repairs, func(i, j int) bool { return repairs[i].Page < repairs[j].Page })
	return repairs, nil
}

// resolveDocumentReportQualityReview is the deterministic admission resolver.
// It re-reads every link rather than trusting copied stage metadata: current
// report id/version/body/capability, PDF, ordered pages, jury artifact, seat
// identities, and exact jury artifact digest must all still agree.
func resolveDocumentReportQualityReview(app *kanbanBoardApp, plan *goalPlan, parentID string) (documentReportQualityReview, error) {
	var review documentReportQualityReview
	report, err := documentReportStageCandidate(app, plan, parentID)
	if err != nil {
		return review, err
	}
	renderStage := plan.subtaskByID(documentReportDraftRenderStageID)
	juryStage := plan.subtaskByID(documentReportJuryStageID)
	if renderStage == nil || juryStage == nil || renderStage.Status != subtaskComplete || juryStage.Status != subtaskComplete {
		return review, fmt.Errorf("rendered document review stages are missing or incomplete")
	}
	renderRecord, renderOK := app.osArtifactByID(renderStage.ArtifactID)
	juryRecord, juryOK := app.osArtifactByID(juryStage.ArtifactID)
	if !renderOK || !juryOK || renderRecord.Metadata["processStage"] != documentReportDraftRenderStageID || juryRecord.Metadata["processStage"] != documentReportJuryStageID || strings.TrimSpace(renderRecord.Metadata["goalParentId"]) != strings.TrimSpace(parentID) || strings.TrimSpace(juryRecord.Metadata["goalParentId"]) != strings.TrimSpace(parentID) {
		return review, fmt.Errorf("rendered document review records are missing or belong to a different run")
	}
	stageVerdict := strings.TrimSpace(juryRecord.Metadata["reviewVerdict"])
	if stageVerdict == "needs_attention" {
		return documentReportQualityReview{Verdict: stageVerdict, ArtifactID: report.ID, ArtifactVersion: artifactVersion(report), ContentDigest: documentReportBodyDigest(report), CapabilityDigest: artifactCapabilityDigest(report)}, nil
	}
	if stageVerdict != "ready" && stageVerdict != "needs_changes" {
		return review, fmt.Errorf("rendered document jury has no supported verdict")
	}
	binding, err := validateDocumentReportCompletedRender(report)
	if err != nil || !documentReportRenderRecordMatches(renderRecord, binding) {
		return review, fmt.Errorf("the rendered document binding changed after page review")
	}
	juryID := strings.TrimSpace(juryRecord.Metadata["documentJuryArtifactId"])
	jury, ok := app.osArtifactByID(juryID)
	if !ok || jury.Metadata["artifactContract"] != documentReportJuryContract || jury.Metadata["source"] != documentReportJurySource || strings.TrimSpace(jury.Metadata["goalId"]) != strings.TrimSpace(parentID) {
		return review, fmt.Errorf("linked rendered document jury is missing, mistyped, or belongs to another run")
	}
	if strings.TrimSpace(juryRecord.Metadata["documentJuryArtifactDigest"]) != artifactCapabilityDigest(jury) || strings.TrimSpace(juryRecord.Metadata["jurySeatsDigest"]) != sha256Hex([]byte(jury.Text)) {
		return review, fmt.Errorf("the linked rendered jury record changed after review")
	}
	if _, readinessErr := validateDocumentReportJuryReadinessMetadata(jury, binding.PageCount); readinessErr != nil {
		return review, readinessErr
	}
	for _, key := range []string{"reviewVerdict", "blockingPages", "minimumAverage", "parsedSeats", "jurySeatIds", "jurySeatsDigest", "repairFixes", "documentArtifactId", "documentSourceVersion", "documentArtifactVersion", "documentContentDigest", "documentCapabilityDigest", "renderPdfAssetRef", "renderPageCount", "renderPagesDigest"} {
		if strings.TrimSpace(juryRecord.Metadata[key]) != strings.TrimSpace(jury.Metadata[key]) {
			return review, fmt.Errorf("rendered jury stage metadata does not match its exact scoreboard")
		}
	}
	if jury.Metadata["documentArtifactId"] != binding.ArtifactID || jury.Metadata["documentSourceVersion"] != strconv.Itoa(binding.SourceVersion) || jury.Metadata["documentArtifactVersion"] != strconv.Itoa(binding.ArtifactVersion) || jury.Metadata["documentContentDigest"] != binding.ContentDigest || jury.Metadata["documentCapabilityDigest"] != binding.CapabilityDigest || jury.Metadata["renderPdfAssetRef"] != binding.PDFAssetRef || jury.Metadata["renderPageCount"] != strconv.Itoa(binding.PageCount) || jury.Metadata["renderPagesDigest"] != binding.PagesDigest {
		return review, fmt.Errorf("rendered jury is bound to a stale document or page set")
	}
	seats, err := parseDocumentReportSeatIDs(jury.Metadata["jurySeatIds"])
	if err != nil {
		return review, err
	}
	parsedSeats, err := strconv.Atoi(strings.TrimSpace(jury.Metadata["parsedSeats"]))
	if err != nil || parsedSeats != len(seats) {
		return review, fmt.Errorf("rendered jury seat count does not match its distinct seat identities")
	}
	minimum, err := strconv.ParseFloat(strings.TrimSpace(jury.Metadata["minimumAverage"]), 64)
	if err != nil || math.IsNaN(minimum) || math.IsInf(minimum, 0) || minimum < 0 || minimum > 10 {
		return review, fmt.Errorf("rendered jury has no valid minimum per-page average")
	}
	blockingPages, err := slideJuryPageListFromMetadata(jury.Metadata["blockingPages"])
	if err != nil {
		return review, err
	}
	review = documentReportQualityReview{
		Verdict: stageVerdict, ArtifactID: binding.ArtifactID, ArtifactVersion: binding.ArtifactVersion,
		ContentDigest: binding.ContentDigest, CapabilityDigest: binding.CapabilityDigest,
		PDFAssetRef: binding.PDFAssetRef, PageCount: binding.PageCount, PagesDigest: binding.PagesDigest,
		JuryID: jury.ID, JuryDigest: artifactCapabilityDigest(jury), SeatIDs: seats,
		MinimumAverage: minimum, ParsedSeats: parsedSeats,
	}
	if stageVerdict == "ready" {
		if minimum < documentReportReadyAverageFloor || len(blockingPages) != 0 || strings.TrimSpace(jury.Metadata["repairFixes"]) != "[]" {
			return documentReportQualityReview{}, fmt.Errorf("ready rendered jury does not satisfy the 8.5 per-page floor without blocking repairs")
		}
		return review, nil
	}
	repairs, err := decodeDocumentReportRepairs(jury.Metadata["repairFixes"], blockingPages)
	if err != nil {
		return documentReportQualityReview{}, err
	}
	review.Repairs = repairs
	return review, nil
}

func resolveAdmittedDocumentReportQuality(app *kanbanBoardApp, plan *goalPlan, parentID string) (documentReportQualityReview, error) {
	var empty documentReportQualityReview
	admission := plan.subtaskByID(documentReportRenderedAdmissionID)
	if admission == nil || admission.Status != subtaskComplete || admission.Review == nil || admission.Review.Verdict != goalReviewPass || strings.TrimSpace(admission.ArtifactID) == "" {
		return empty, fmt.Errorf("the deterministic rendered admission did not pass")
	}
	record, ok := app.osArtifactByID(admission.ArtifactID)
	if !ok || record.Metadata["processStage"] != documentReportRenderedAdmissionID || strings.TrimSpace(record.Metadata["goalParentId"]) != strings.TrimSpace(parentID) {
		return empty, fmt.Errorf("the rendered admission record is missing or belongs to another run")
	}
	review, err := resolveDocumentReportQualityReview(app, plan, parentID)
	if err != nil || review.Verdict != "ready" {
		return empty, fmt.Errorf("the exact rendered document is not admitted for publication")
	}
	if record.Metadata["reviewedDocumentArtifactId"] != review.ArtifactID || record.Metadata["reviewedDocumentArtifactVersion"] != strconv.Itoa(review.ArtifactVersion) || record.Metadata["reviewedDocumentContentDigest"] != review.ContentDigest || record.Metadata["reviewedDocumentPagesDigest"] != review.PagesDigest || record.Metadata["documentJuryArtifactId"] != review.JuryID {
		return empty, fmt.Errorf("the publication candidate changed after rendered admission")
	}
	return review, nil
}

func resolvePublishedDocumentReportQuality(app *kanbanBoardApp, plan *goalPlan, parentID string) (documentReportQualityReview, error) {
	review, err := resolveAdmittedDocumentReportQuality(app, plan, parentID)
	if err != nil {
		return documentReportQualityReview{}, err
	}
	publish := plan.subtaskByID(documentReportPublishStageID)
	if publish == nil || publish.Status != subtaskComplete || strings.TrimSpace(publish.ArtifactID) == "" {
		return documentReportQualityReview{}, fmt.Errorf("the deterministic document publication record is missing or incomplete")
	}
	record, ok := app.osArtifactByID(publish.ArtifactID)
	if !ok || record.Metadata["processStage"] != documentReportPublishStageID || strings.TrimSpace(record.Metadata["goalParentId"]) != strings.TrimSpace(parentID) || record.Metadata["published"] != "true" {
		return documentReportQualityReview{}, fmt.Errorf("the deterministic document publication record is missing or belongs to another run")
	}
	if record.Metadata["documentArtifactId"] != review.ArtifactID || record.Metadata["documentArtifactVersion"] != strconv.Itoa(review.ArtifactVersion) || record.Metadata["documentContentDigest"] != review.ContentDigest || record.Metadata["documentCapabilityDigest"] != review.CapabilityDigest || record.Metadata["renderPdfAssetRef"] != review.PDFAssetRef || record.Metadata["renderPageCount"] != strconv.Itoa(review.PageCount) || record.Metadata["renderPagesDigest"] != review.PagesDigest || record.Metadata["documentJuryArtifactId"] != review.JuryID {
		return documentReportQualityReview{}, fmt.Errorf("the published document binding changed after rendered admission")
	}
	return review, nil
}

func documentReportPlanParentID(app *kanbanBoardApp, plan *goalPlan) string {
	return authoredRenderedPlanParentID(app, plan)
}

func compileDocumentReportPublish(app *kanbanBoardApp, plan *goalPlan, parentID string, _ ProcessStage) (string, map[string]string, error) {
	review, err := resolveAdmittedDocumentReportQuality(app, plan, parentID)
	if err != nil {
		return "", nil, err
	}
	body := strings.Join([]string{
		"Native document admitted for delivery",
		"",
		"- Editable document: " + review.ArtifactID + " @ v" + strconv.Itoa(review.ArtifactVersion),
		"- Downloadable PDF: " + review.PDFAssetRef,
		fmt.Sprintf("- Rendered jury: %d distinct valid seats; minimum page average %.2f (floor %.2f).", review.ParsedSeats, review.MinimumAverage, documentReportReadyAverageFloor),
		"- Publication used the exact admitted artifact and page set; no post-jury text scorer ran.",
	}, "\n")
	return body, map[string]string{
		"documentArtifactId":       review.ArtifactID,
		"shipArtifactIds":          review.ArtifactID,
		"documentArtifactVersion":  strconv.Itoa(review.ArtifactVersion),
		"documentContentDigest":    review.ContentDigest,
		"documentCapabilityDigest": review.CapabilityDigest,
		"renderPdfAssetRef":        review.PDFAssetRef,
		"renderPageCount":          strconv.Itoa(review.PageCount),
		"renderPagesDigest":        review.PagesDigest,
		"documentJuryArtifactId":   review.JuryID,
		"published":                "true",
	}, nil
}
