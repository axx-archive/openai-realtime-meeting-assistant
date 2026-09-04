package main

// report_print_test.go pins the branded print document for markdown research
// reports: strict escape-first conversion (hostile markdown never becomes
// live HTML), the Stride masthead/meta/footer chrome, the markdown subset
// the client reader supports (headings, pipe tables, lists, blockquotes,
// rules, links, bold, inline code), and the export trigger's routing —
// markdown enqueues the converted document as kind paper while the deck path
// stays byte-for-byte.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The body is model text: every span must be escaped BEFORE it gets
// structure. A <script> planted in a paragraph, heading, list item, table
// cell, bold span, code span, or link label must come out inert, and a
// javascript: URI must never become an href.
func TestResearchReportPrintHTMLEscapesHostileMarkdown(t *testing.T) {
	hostile := strings.Join([]string{
		"## Executive <script>alert('h')</script> Summary",
		"",
		"A paragraph with <script>alert('p')</script> and **bold <script>alert('b')</script>** text.",
		"",
		"- item <img src=x onerror=alert('l')>",
		"",
		"| Head <script>alert('th')</script> | Value |",
		"| --- | --- |",
		"| cell <script>alert('td')</script> | `code <script>alert('c')</script>` |",
		"",
		"> quote <script>alert('q')</script>",
		"",
		"[label <b>bold</b>](https://example.com/?q=\"><script>alert('u')</script>)",
		"[not a link](javascript:alert('js'))",
	}, "\n")

	doc := renderResearchReportPrintHTML(meetingMemoryEntry{
		ID:   "os-artifact-research-1",
		Text: hostile,
		Metadata: map[string]string{
			"title":       "Hostile <script>alert('t')</script> title",
			"requestedBy": "aj@shareability.com",
		},
	})

	// No live tag may form: every < in model text is escaped, so the raw
	// sequences can only exist if a span skipped escaping. (The onerror=
	// substring itself survives as inert TEXT inside the escaped &lt;img …
	// run — dangerous only if the tag around it forms, which is what these
	// pin.)
	if strings.Contains(doc, "<script>") || strings.Contains(doc, "<img") {
		t.Fatalf("hostile markdown leaked live HTML into the print document:\n%s", doc)
	}
	if !strings.Contains(doc, "&lt;script&gt;") {
		t.Fatal("hostile markup must survive as escaped text, not be dropped")
	}
	if strings.Contains(doc, `href="javascript:`) {
		t.Fatal("a javascript: URI must never become an href")
	}
	// The link URL's quote must be escaped inside the href attribute.
	if strings.Contains(doc, `?q="><`) {
		t.Fatalf("link href leaked an unescaped quote:\n%s", doc)
	}
	// Structure still forms around the escaped text.
	for _, want := range []string{"<h2>", "<li>", "<td>", "<blockquote>", "<strong>", "<code>"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("print document lost the %s structure around escaped content", want)
		}
	}
}

// The document carries the Stride brand chrome: masthead,
// wordmark, mono kicker), title, meta line (date · requested by · worker),
// the gate-result strip and search-tag chips lifted out of the body, the
// contract sections, and the Scout colophon footer.
func TestResearchReportPrintHTMLMastheadSectionsAndFooter(t *testing.T) {
	body := strings.Join([]string{
		"**Gate result:** PASS — every dimension at or above bar.",
		"",
		"Search tags: samsung, licensing, dram",
		"",
		"## Executive Summary",
		"",
		"The short version a partner can act on.",
		"",
		"## Evidence",
		"",
		"| Company | Revenue |",
		"| --- | --- |",
		"| Samsung | $198B (FY2025) |",
		"",
		"- finding one with [a source](https://example.com/filing)",
		"1. numbered check",
		"",
		"> the strongest counter-voice",
		"",
		"---",
		"",
		"## Recommendation",
		"",
		"Do the deal.",
	}, "\n")

	created := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	doc := renderResearchReportPrintHTML(meetingMemoryEntry{
		ID:        "os-artifact-research-2",
		Text:      body,
		CreatedAt: created,
		Metadata: map[string]string{
			"title":       "Samsung licensing brief",
			"requestedBy": "aj@shareability.com",
			"model":       "claude-fable-5",
			// The research chrome below (kicker, Contents nav, sources
			// appendix) is gated on the artifact's own research stamp, so a
			// research fixture has to carry one — see
			// TestOrdinaryDocumentPrintOmitsResearchReportChrome.
			"mode": "research",
		},
	})

	for _, want := range []string{
		"Bonfire",
		"RESEARCH REPORT",
		"Prepared for Bonfire",
		"Samsung licensing brief",
		"July 7, 2026",
		"Requested by aj@shareability.com",
		"claude-fable-5",
		"Gate result",
		"PASS — every dimension at or above bar.",
		">samsung</span>",
		"<h2>Executive Summary</h2>",
		"<h2>Evidence</h2>",
		"<h2>Recommendation</h2>",
		"<th>Company</th>",
		"<td>Samsung</td>",
		"<a href=\"https://example.com/filing\">a source</a>",
		"<ol><li>numbered check</li></ol>",
		"<blockquote>the strongest counter-voice</blockquote>",
		"<hr>",
		"Prepared by Scout for Bonfire · July 7, 2026",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("print document missing %q:\n%s", want, doc)
		}
	}
	// The gate line and search tags moved into the masthead: neither repeats
	// in the body flow.
	if strings.Contains(doc, "**Gate result:**") || strings.Contains(doc, "Search tags:") {
		t.Fatal("gate/search-tag preamble lines must move into the masthead, not repeat in the body")
	}
	// Self-contained print document: no external fetches of any kind.
	for _, banned := range []string{"<link", "src=\"http", "@import", "url(http"} {
		if strings.Contains(doc, banned) {
			t.Fatalf("print document must be self-contained, found %q", banned)
		}
	}
}

func TestResearchReportPrintHTMLPreservesDocumentFormatting(t *testing.T) {
	body := strings.Join([]string{
		"## Activation plan",
		"",
		"The opportunity is *culturally specific*, _human led_, and ~~mass blasted~~ community powered.  ",
		"This line is intentionally next.",
		"",
		"1. Recruit",
		"  - Rodeo creators",
		"    3. Verify audience fit",
		"    4. Brief the cohort",
		"  - Music creators",
		"2. Activate",
		"  1. Launch the first batch",
		"  2. Measure participation",
		"",
		"| Segment | Signal |",
		"| --- | --- |",
		`| Rodeo \| western sport | High intent |`,
	}, "\n")

	doc := renderResearchReportPrintHTML(meetingMemoryEntry{Text: body})
	for _, want := range []string{
		"<em>culturally specific</em>",
		"<em>human led</em>",
		"<s>mass blasted</s>",
		"community powered.<br>This line is intentionally next.",
		"<ol><li>Recruit<ul><li>Rodeo creators<ol start=\"3\"><li>Verify audience fit</li><li>Brief the cohort</li></ol></li><li>Music creators</li></ul></li><li>Activate<ol><li>Launch the first batch</li><li>Measure participation</li></ol></li></ol>",
		"<td>Rodeo | western sport</td><td>High intent</td>",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("feature-faithful print document missing %q:\n%s", want, doc)
		}
	}
	if strings.Count(doc, "<td>") != 2 {
		t.Fatalf("escaped table pipe created an extra cell:\n%s", doc)
	}
}

func TestResearchReportPrintHTMLInlinesOnlyBoundedLocalImages(t *testing.T) {
	setupIsolatedBlobStore(t)
	png := tinyPNG(t)
	attachedRef, err := putBlob(png, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	unattachedRef, err := putBlob([]byte("private but unattached"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	assets, err := json.Marshal([]artifactAsset{{Ref: attachedRef, Mime: "image/png", Name: "field.png", Kind: "image"}})
	if err != nil {
		t.Fatal(err)
	}
	inline := base64.StdEncoding.EncodeToString([]byte("small inline image"))
	body := strings.Join([]string{
		"## Field evidence",
		"",
		"![Attached field](/artifacts/blob?ref=" + attachedRef + "&name=field.png)",
		"",
		"![Inline diagram](data:image/webp;base64," + inline + ")",
		"",
		"![Remote campaign](https://images.example.test/campaign.jpg?view=wide&v=2)",
		"",
		"![Unattached image](/artifacts/blob?ref=" + unattachedRef + ")",
	}, "\n")
	doc := renderResearchReportPrintHTML(meetingMemoryEntry{
		Text:     body,
		Metadata: map[string]string{artifactAssetsMetadataKey: string(assets)},
	})

	attachedData := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	inlineData := "data:image/webp;base64," + inline
	for _, want := range []string{
		`<img src="` + attachedData + `" alt="Attached field">`,
		`<img src="` + inlineData + `" alt="Inline diagram">`,
		`role="img" aria-label="Remote campaign"`,
		`href="https://images.example.test/campaign.jpg?view=wide&amp;v=2">Source · images.example.test</a>`,
		`role="img" aria-label="Unattached image"`,
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("safe image export missing %q:\n%s", want, doc)
		}
	}
	if strings.Contains(doc, `src="http`) || strings.Contains(doc, base64.StdEncoding.EncodeToString([]byte("private but unattached"))) {
		t.Fatalf("remote or unattached bytes entered an image src:\n%s", doc)
	}
	for _, banned := range []string{"<script", `src="/artifacts/blob`, `src="https://`} {
		if strings.Contains(doc, banned) {
			t.Fatalf("network-closed print document contains %q:\n%s", banned, doc)
		}
	}
}

func TestResearchReportPrintHTMLPreservesNativeImagePresentationMetadata(t *testing.T) {
	setupIsolatedBlobStore(t)
	png := tinyPNG(t)
	ref, err := putBlob(png, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	assets, err := json.Marshal([]artifactAsset{{Ref: ref, Mime: "image/png", Name: "field.png", Kind: "image"}})
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		"## Visual evidence",
		"",
		"![Attached field](/artifacts/blob?ref=" + ref + "&name=field.png#stride-doc-image?width=50&align=right&caption=Measured+field+%3Cproof%3E)",
		"",
		"![Remote campaign](https://images.example.test/campaign.jpg?view=wide&v=2#stride-doc-image?width=25&align=left&caption=Campaign+reference)",
	}, "\n")
	doc := renderResearchReportPrintHTML(meetingMemoryEntry{
		Text:     body,
		Metadata: map[string]string{artifactAssetsMetadataKey: string(assets)},
	})

	attachedData := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	for _, want := range []string{
		`class="report-image report-image--width-50 report-image--align-right"><img src="` + attachedData + `" alt="Attached field">`,
		`<span class="report-image__caption">Measured field &lt;proof&gt;</span>`,
		`class="report-image report-image--width-25 report-image--align-left report-image--reference"`,
		`<span class="report-image__eyebrow">External image reference</span>`,
		`<span class="report-image__caption">Campaign reference</span>`,
		`External image not embedded in this PDF · <a href="https://images.example.test/campaign.jpg?view=wide&amp;v=2">Source · images.example.test</a>`,
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("presentation-faithful image export missing %q:\n%s", want, doc)
		}
	}
	for _, unwanted := range []string{
		"#stride-doc-image?",
		`src="https://images.example.test`,
		`<span class="report-image__caption">Attached field</span>`,
	} {
		if strings.Contains(doc, unwanted) {
			t.Fatalf("presentation metadata or unsafe fallback leaked %q:\n%s", unwanted, doc)
		}
	}
	for _, css := range []string{
		`.report-image--width-50{width:50%}`,
		`.report-image--align-right{margin-left:auto;margin-right:0;text-align:right}`,
	} {
		if !strings.Contains(doc, css) {
			t.Fatalf("print stylesheet does not implement %q", css)
		}
	}
}

func TestReportPrintImagePresentationRejectsUnboundedOrUnknownLayoutTokens(t *testing.T) {
	presentation := parseReportPrintImagePresentation("https://example.test/image.jpg#stride-doc-image?width=90&align=justify&caption=One%09two%0Athree%00")
	if presentation.source != "https://example.test/image.jpg" || presentation.width != 100 || presentation.align != "center" || presentation.caption != "One two three" {
		t.Fatalf("unexpected bounded presentation: %+v", presentation)
	}

	oversized := parseReportPrintImagePresentation("https://example.test/image.jpg#stride-doc-image?caption=" + strings.Repeat("x", reportPrintMaxImageParamsBytes+1))
	if oversized.source != "https://example.test/image.jpg" || oversized.width != 100 || oversized.align != "center" || oversized.caption != "" {
		t.Fatalf("oversized presentation parameters must be stripped and ignored: %+v", oversized)
	}
}

func TestArtifactExportPDFMarkdownQueuesLocalImageBytesWithoutRemoteFetch(t *testing.T) {
	_, member := shareLinkTestEnv(t)
	queueDir := setupRenderSidecarEnv(t)
	if err := writeHealthyRenderRunnerHeartbeatForTest(t, "test-runner"); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}

	png := tinyPNG(t)
	ref, err := putBlob(png, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	assets, err := json.Marshal([]artifactAsset{{Ref: ref, Mime: "image/png", Name: "proof.png", Kind: "image"}})
	if err != nil {
		t.Fatal(err)
	}
	report := seedShareArtifact(t, "draft", strings.Join([]string{
		"## Proof",
		"",
		"![Attached proof](/artifacts/blob?ref=" + ref + "&name=proof.png)",
		"",
		"![Remote context](https://images.example.test/context.jpg)",
	}, "\n"), map[string]string{artifactAssetsMetadataKey: string(assets)})

	recorder := shareLinkRequest(t, http.MethodPost, "/artifacts/export-pdf", fmt.Sprintf(`{"artifactId":%q}`, report.ID), member)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("markdown image export status=%d body=%s, want 202", recorder.Code, recorder.Body.String())
	}
	payload := decodeJSON(t, recorder)
	job := readRenderJobForTest(t, queueDir, fmt.Sprint(payload["jobId"]))
	if !strings.Contains(job.HTML, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(png)) {
		t.Fatalf("queued PDF job did not inline the attached image: %s", job.HTML)
	}
	if strings.Contains(job.HTML, `img src="https://`) || strings.Contains(job.HTML, `img src="/artifacts/`) {
		t.Fatalf("queued PDF job retained a network-capable image source: %s", job.HTML)
	}
	if !strings.Contains(job.HTML, "Source · images.example.test") {
		t.Fatalf("queued PDF job lost the remote image's semantic reference: %s", job.HTML)
	}
}

// The export trigger routes a markdown artifact to the paper kind with the
// converted print document as the job HTML. Legacy self-contained decks stay
// byte-for-byte; native scene decks expand their attached image refs before
// crossing the isolated render boundary.
func TestArtifactExportPDFMarkdownRoutesToPaperDeckUnchanged(t *testing.T) {
	_, member := shareLinkTestEnv(t)
	queueDir := setupRenderSidecarEnv(t)
	if err := writeHealthyRenderRunnerHeartbeatForTest(t, "test-runner"); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}

	report := seedShareArtifact(t, "draft", "## Executive Summary\n\nThe finding, with <script>alert(1)</script> inside.", nil)
	recorder := shareLinkRequest(t, http.MethodPost, "/artifacts/export-pdf", fmt.Sprintf(`{"artifactId":%q}`, report.ID), member)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("markdown export status=%d body=%s, want 202", recorder.Code, recorder.Body.String())
	}
	payload := decodeJSON(t, recorder)
	if fmt.Sprint(payload["kind"]) != renderJobKindPaper {
		t.Fatalf("markdown export kind=%v, want paper", payload["kind"])
	}
	job := readRenderJobForTest(t, queueDir, fmt.Sprint(payload["jobId"]))
	if job.Kind != renderJobKindPaper {
		t.Fatalf("queued markdown job kind=%q, want paper", job.Kind)
	}
	if !strings.Contains(job.HTML, "Prepared by Scout for") || !strings.Contains(job.HTML, "<h2>Executive Summary</h2>") {
		t.Fatal("queued markdown job must carry the branded print document, not the raw body")
	}
	if strings.Contains(job.HTML, "<script>") {
		t.Fatal("queued print document leaked live hostile markup")
	}
	if entry, _ := kanbanApp.osArtifactByID(report.ID); entry.Metadata["renderKind"] != renderJobKindPaper {
		t.Fatalf("markdown artifact stamped renderKind=%q, want paper", entry.Metadata["renderKind"])
	}
	// A stale client naming "deck" for a markdown report is a 400, never a
	// silent rewrite.
	if recorder := shareLinkRequest(t, http.MethodPost, "/artifacts/export-pdf", fmt.Sprintf(`{"artifactId":%q,"kind":"deck"}`, report.ID), member); recorder.Code != http.StatusBadRequest {
		t.Fatalf("deck-kind export of markdown status=%d, want 400", recorder.Code)
	}

	// Deck path unchanged: the queued job HTML is the artifact body
	// byte-for-byte and the kind stays deck.
	deckBody := "<!doctype html><html><body>deck body stays verbatim</body></html>"
	deck := seedShareArtifact(t, "draft", deckBody, map[string]string{"type": "html_deck"})
	recorder = shareLinkRequest(t, http.MethodPost, "/artifacts/export-pdf", fmt.Sprintf(`{"artifactId":%q}`, deck.ID), member)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("deck export status=%d body=%s, want 202", recorder.Code, recorder.Body.String())
	}
	deckPayload := decodeJSON(t, recorder)
	deckJob := readRenderJobForTest(t, queueDir, fmt.Sprint(deckPayload["jobId"]))
	if deckJob.Kind != renderJobKindDeck {
		t.Fatalf("queued deck job kind=%q, want deck", deckJob.Kind)
	}
	if deckJob.HTML != deckBody {
		t.Fatalf("deck job HTML changed:\n got %q\nwant %q", deckJob.HTML, deckBody)
	}

	setupIsolatedBlobStore(t)
	imageRef, err := putBlob([]byte("native deck image"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	scene := deckDocument{SchemaVersion: 1, Width: 1920, Height: 1080, Slides: []deckSlide{{
		ID: "slide-1", Elements: []deckElement{{ID: "hero", Type: "image", X: 0, Y: 0, Width: 1920, Height: 1080, Z: 1, Opacity: 1, Ref: imageRef, Name: "hero.png", Fit: "cover"}},
	}}}
	sceneBytes, err := json.Marshal(scene)
	if err != nil {
		t.Fatal(err)
	}
	sceneRef, err := putBlob(sceneBytes, "application/vnd.bonfire.deck+json")
	if err != nil {
		t.Fatal(err)
	}
	assets, err := json.Marshal([]artifactAsset{{Ref: imageRef, Mime: "image/png", Name: "hero.png", Kind: "image"}})
	if err != nil {
		t.Fatal(err)
	}
	native := seedShareArtifact(t, "draft", `<!doctype html><html><body><img src="/artifacts/blob?ref=`+imageRef+`"></body></html>`, map[string]string{
		"type": artifactTypeHTMLDeck, deckSceneRefMetadataKey: sceneRef, artifactAssetsMetadataKey: string(assets),
	})
	recorder = shareLinkRequest(t, http.MethodPost, "/artifacts/export-pdf", fmt.Sprintf(`{"artifactId":%q}`, native.ID), member)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("native deck export status=%d body=%s, want 202", recorder.Code, recorder.Body.String())
	}
	nativePayload := decodeJSON(t, recorder)
	nativeJob := readRenderJobForTest(t, queueDir, fmt.Sprint(nativePayload["jobId"]))
	if !strings.Contains(nativeJob.HTML, "data:image/png;base64,") || strings.Contains(nativeJob.HTML, "/artifacts/blob") {
		t.Fatalf("native deck export did not inline its attached image: %s", nativeJob.HTML)
	}
}

// readRenderJobForTest decodes one queued render job file.
func readRenderJobForTest(t *testing.T, queueDir string, jobID string) renderRunnerJob {
	t.Helper()
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		t.Fatal("export response carried no jobId")
	}
	raw, err := os.ReadFile(filepath.Join(queueDir, jobID+".json"))
	if err != nil {
		t.Fatalf("read queued job: %v", err)
	}
	var job renderRunnerJob
	if err := json.Unmarshal(raw, &job); err != nil {
		t.Fatalf("decode queued job: %v", err)
	}
	return job
}
