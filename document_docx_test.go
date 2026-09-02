package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const docxFixtureMarkdown = "# Field notes\n\n" +
	"An **important** paragraph with *emphasis*, `inline code`, ~~struck~~ text and a [link](https://example.com/field?x=1&y=2).\n" +
	"Second line of the same paragraph.  \nHard break line.\n\n" +
	"## Findings\n\n" +
	"- First bullet\n- Second bullet\n  1. Nested one\n  2. Nested two\n- Third bullet\n\n" +
	"3. Numbered from three\n4. Numbered four\n\n" +
	"### Table\n\n" +
	"| Metric | Value | Note |\n|---|---|---|\n| Yield | 12% | a \\| pipe |\n| Cost | $4 | <script>alert(1)</script> |\n\n" +
	"---\n\n" +
	"> A quoted line\n> continues here\n\n" +
	"```go\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n```\n\n" +
	"Closing paragraph with a control char \x01 stripped."

func docxFixtureAssert(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	parts := deckPPTXZipParts(t, data)
	for _, want := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/styles.xml", "word/numbering.xml", "word/_rels/document.xml.rels", "docProps/core.xml"} {
		if _, ok := parts[want]; !ok {
			t.Fatalf("DOCX is missing part %s (have %v)", want, partNames(parts))
		}
	}
	for name, body := range parts {
		if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".rels") {
			docxAssertWellFormed(t, name, body)
		}
	}
	return parts
}

func partNames(parts map[string][]byte) []string {
	names := make([]string, 0, len(parts))
	for name := range parts {
		names = append(names, name)
	}
	return names
}

func docxAssertWellFormed(t *testing.T, name string, body []byte) {
	t.Helper()
	decoder := xml.NewDecoder(bytes.NewReader(body))
	for {
		_, err := decoder.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("%s is not well-formed XML: %v\n%s", name, err, body)
		}
	}
}

func TestDocxCompileProducesEditableStructure(t *testing.T) {
	docx, err := compileDocumentDOCX(docxFixtureMarkdown, "Field notes", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	parts := docxFixtureAssert(t, docx)
	document := string(parts["word/document.xml"])
	for _, want := range []string{
		`<w:pStyle w:val="Heading1"/>`, `<w:pStyle w:val="Heading2"/>`, `<w:pStyle w:val="Heading3"/>`,
		`<w:t xml:space="preserve">Field notes</w:t>`,
		`<w:rPr><w:b/><w:bCs/></w:rPr><w:t xml:space="preserve">important</w:t>`,
		`<w:rPr><w:i/><w:iCs/></w:rPr><w:t xml:space="preserve">emphasis</w:t>`,
		`<w:rStyle w:val="CodeChar"/></w:rPr><w:t xml:space="preserve">inline code</w:t>`,
		`<w:strike/></w:rPr><w:t xml:space="preserve">struck</w:t>`,
		`<w:hyperlink r:id="rId3"><w:r><w:rPr><w:rStyle w:val="Hyperlink"/></w:rPr><w:t xml:space="preserve">link</w:t></w:r></w:hyperlink>`,
		`<w:r><w:br/></w:r>`, `Hard break line.`,
		`<w:pStyle w:val="Quote"/>`, `A quoted line continues here`,
		`<w:pStyle w:val="Code"/></w:pPr><w:r><w:t xml:space="preserve">    fmt.Println(&quot;hi&quot;)</w:t>`,
		`<w:pBdr><w:bottom w:val="single"`,
		`&lt;script&gt;alert(1)&lt;/script&gt;`,
		`a | pipe`,
		`<w:sectPr>`,
	} {
		if !strings.Contains(document, want) {
			t.Fatalf("document.xml missing %q:\n%s", want, document)
		}
	}
	if strings.Contains(document, "\x01") || strings.Contains(document, "<script>") {
		t.Fatalf("document.xml leaked raw control/markup: %s", document)
	}
	rels := string(parts["word/_rels/document.xml.rels"])
	if !strings.Contains(rels, `Id="rId3" Type="`+documentDOCXRelTypeHyperlink+`" Target="https://example.com/field?x=1&amp;y=2" TargetMode="External"`) {
		t.Fatalf("hyperlink relationship missing or unescaped: %s", rels)
	}
	if !strings.Contains(rels, `Target="styles.xml"`) || !strings.Contains(rels, `Target="numbering.xml"`) {
		t.Fatalf("styles/numbering relationships missing: %s", rels)
	}
	contentTypes := string(parts["[Content_Types].xml"])
	if !strings.Contains(contentTypes, `PartName="/word/document.xml"`) || !strings.Contains(contentTypes, `PartName="/word/numbering.xml"`) {
		t.Fatalf("content types incomplete: %s", contentTypes)
	}
}

func TestDocxListAndTableRoundTrip(t *testing.T) {
	docx, err := compileDocumentDOCX(docxFixtureMarkdown, "Field notes", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	parts := docxFixtureAssert(t, docx)
	document := string(parts["word/document.xml"])
	numbering := string(parts["word/numbering.xml"])

	// Lists: three bullets at ilvl 0 on the shared bullet instance, two nested
	// ordered items at ilvl 1 on their own instance, and a top-level ordered
	// list that starts at 3 on a third instance.
	if got := strings.Count(document, `<w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr>`); got != 3 {
		t.Fatalf("bullet items=%d want 3\n%s", got, document)
	}
	if got := strings.Count(document, `<w:numPr><w:ilvl w:val="1"/><w:numId w:val="2"/></w:numPr>`); got != 2 {
		t.Fatalf("nested ordered items=%d want 2\n%s", got, document)
	}
	if got := strings.Count(document, `<w:numPr><w:ilvl w:val="0"/><w:numId w:val="3"/></w:numPr>`); got != 2 {
		t.Fatalf("top-level ordered items=%d want 2\n%s", got, document)
	}
	for _, want := range []string{
		`<w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>`,
		`<w:num w:numId="2"><w:abstractNumId w:val="1"/><w:lvlOverride w:ilvl="1"><w:startOverride w:val="1"/></w:lvlOverride></w:num>`,
		`<w:num w:numId="3"><w:abstractNumId w:val="1"/><w:lvlOverride w:ilvl="0"><w:startOverride w:val="3"/></w:lvlOverride></w:num>`,
		`<w:numFmt w:val="bullet"/>`, `<w:numFmt w:val="decimal"/>`,
	} {
		if !strings.Contains(numbering, want) {
			t.Fatalf("numbering.xml missing %q:\n%s", want, numbering)
		}
	}
	if strings.LastIndex(numbering, "<w:abstractNum ") > strings.Index(numbering, "<w:num ") {
		t.Fatalf("w:num instances must follow every w:abstractNum: %s", numbering)
	}

	// Table: one table, header + two body rows, three cells per row, header bold.
	if got := strings.Count(document, "<w:tbl>"); got != 1 {
		t.Fatalf("tables=%d want 1", got)
	}
	table := document[strings.Index(document, "<w:tbl>"):strings.Index(document, "</w:tbl>")]
	if rows := strings.Count(table, "<w:tr>"); rows != 3 {
		t.Fatalf("table rows=%d want 3\n%s", rows, table)
	}
	if cells := strings.Count(table, "<w:tc>"); cells != 9 {
		t.Fatalf("table cells=%d want 9\n%s", cells, table)
	}
	if !strings.Contains(table, `<w:trPr><w:tblHeader/></w:trPr>`) || !strings.Contains(table, `<w:rPr><w:b/><w:bCs/></w:rPr><w:t xml:space="preserve">Metric</w:t>`) {
		t.Fatalf("table header row is not marked/bold:\n%s", table)
	}
	if !strings.Contains(document, "</w:tbl><w:p>") {
		t.Fatal("a paragraph must follow the table so Word can place the next block")
	}
}

func TestDocxEmbedsAttachedAndDataImagesOnly(t *testing.T) {
	setupIsolatedBlobStore(t)
	png := deckPPTXTestPNG(t, 64, 32)
	ref, err := putBlob(png, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	unattached := strings.Repeat("b", 64)
	markdown := "![Field](/artifacts/blob?ref=" + ref + "&name=field.png#stride-doc-image?width=50&caption=Dawn)\n\n" +
		"![Stranger](/artifacts/blob?ref=" + unattached + "&name=x.png)\n\n" +
		"![Inline](data:image/png;base64," + base64.StdEncoding.EncodeToString(png) + ")\n\n" +
		"![Remote](https://images.example.com/pic.png)\n\n" +
		"![Bad](javascript:alert(1))"
	resolved := 0
	docx, err := compileDocumentDOCX(markdown, "Images", map[string]struct{}{ref: {}}, func(got string) ([]byte, string, error) {
		resolved++
		if got != ref {
			return nil, "", fmt.Errorf("unexpected ref %s", got)
		}
		return png, "image/png", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	parts := docxFixtureAssert(t, docx)
	if resolved != 1 {
		t.Fatalf("resolver calls=%d want exactly the attached ref", resolved)
	}
	if !bytes.Equal(parts["word/media/image1.png"], png) {
		t.Fatal("attached image bytes were not embedded as word/media/image1.png")
	}
	document := string(parts["word/document.xml"])
	if got := strings.Count(document, "<w:drawing>"); got != 2 {
		t.Fatalf("drawings=%d want attached + data image only\n%s", got, document)
	}
	half := strconv.Itoa(documentDOCXTextWidthEMU / 2)
	if !strings.Contains(document, `<wp:extent cx="`+half+`" cy="`+strconv.Itoa(documentDOCXTextWidthEMU/4)+`"/>`) {
		t.Fatalf("width=50 token should halve the extent with aspect kept:\n%s", document)
	}
	if !strings.Contains(document, `<w:t xml:space="preserve">Dawn</w:t>`) {
		t.Fatal("caption token was dropped")
	}
	if !strings.Contains(document, `[Stranger]`) {
		t.Fatal("unattached ref must degrade to alt text, never fetch")
	}
	if !strings.Contains(document, `Remote (images.example.com)`) || !strings.Contains(string(parts["word/_rels/document.xml.rels"]), `Target="https://images.example.com/pic.png" TargetMode="External"`) {
		t.Fatal("external image should become a non-fetching link")
	}
	// A javascript: source never matches the image grammar: it stays literal
	// text and no relationship or drawing is minted for it.
	if strings.Contains(string(parts["word/_rels/document.xml.rels"]), "javascript:") || strings.Contains(document, `<w:hyperlink r:id="rId5">`) {
		t.Fatal("javascript: image source leaked into the package")
	}
	if _, duplicated := parts["word/media/image2.png"]; duplicated {
		t.Fatal("identical bytes reached via ref and data: should share one media part")
	}
	if !strings.Contains(string(parts["[Content_Types].xml"]), `<Default Extension="png" ContentType="image/png"/>`) {
		t.Fatal("png default content type missing")
	}
}

func TestDocxFilenameIsAttachmentSafe(t *testing.T) {
	if got := documentDOCXFilename(`Q3 plan: "final"/v2.docx`); got != `Q3 plan- -final--v2.docx` {
		t.Fatalf("filename=%q", got)
	}
	if got := documentDOCXFilename(""); got != "Document.docx" {
		t.Fatalf("empty title filename=%q", got)
	}
}

func TestDocxOpensInLibreOfficeWhenAvailable(t *testing.T) {
	soffice, err := exec.LookPath("soffice")
	if err != nil {
		t.Skip("LibreOffice is unavailable")
	}
	docx, err := compileDocumentDOCX(docxFixtureMarkdown, "Field notes", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "field-notes.docx")
	if err := os.WriteFile(path, docx, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, soffice, "--headless", "--convert-to", "pdf", "--outdir", dir, path)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("LibreOffice could not open/export generated DOCX: %v\n%s", err, output)
	}
	pdf, err := os.ReadFile(filepath.Join(dir, "field-notes.pdf"))
	if err != nil || len(pdf) < 1000 || !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("LibreOffice produced no valid PDF: bytes=%d err=%v output=%s", len(pdf), err, output)
	}
}

func docxExportRequest(t *testing.T, artifactID string, version int, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"artifactId": artifactID, "expectedVersion": version})
	return artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/export-docx", string(body), cookies, documentDOCXExportHandler)
}

func TestDocxExportHandlerBindsACLVersionAndDownloadHeaders(t *testing.T) {
	cookies, artifact := setupDocumentEditorHTTPTest(t)
	response := docxExportRequest(t, artifact.ID, artifactVersion(artifact), cookies)
	if response.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != documentDOCXContentType {
		t.Fatalf("Content-Type=%q", got)
	}
	disposition, params, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
	if err != nil || disposition != "attachment" || params["filename"] != "Field notes.docx" {
		t.Fatalf("Content-Disposition=%q params=%v err=%v", response.Header().Get("Content-Disposition"), params, err)
	}
	if response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Content-Length") != strconv.Itoa(response.Body.Len()) {
		t.Fatalf("download headers=%v", response.Header())
	}
	parts := docxFixtureAssert(t, response.Body.Bytes())
	if !strings.Contains(string(parts["word/document.xml"]), "Original paragraph.") {
		t.Fatal("exported document lost the body")
	}
	after, _ := kanbanApp.osArtifactByID(artifact.ID)
	if artifactVersion(after) != artifactVersion(artifact) {
		t.Fatal("read-only export mutated the artifact")
	}

	stale := docxExportRequest(t, artifact.ID, artifactVersion(artifact)+1, cookies)
	if stale.Code != http.StatusConflict || stale.Header().Get("Content-Type") == documentDOCXContentType {
		t.Fatalf("stale export status=%d body=%s", stale.Code, stale.Body.String())
	}
	if anonymous := docxExportRequest(t, artifact.ID, artifactVersion(artifact), nil); anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out status=%d", anonymous.Code)
	}
	private, _, err := kanbanApp.createOSArtifactWithMetadata("artifacts", "Private notes", "# Private", "AJ", map[string]string{
		"type": artifactTypeMarkdown, "visibility": "private", "requestedBy": "aj@shareability.com", "ownerEmail": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	nonOwner := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	if denied := docxExportRequest(t, private.ID, artifactVersion(private), nonOwner); denied.Code != http.StatusNotFound || strings.Contains(denied.Body.String(), "Private") {
		t.Fatalf("non-owner export status=%d body=%s want non-oracular 404", denied.Code, denied.Body.String())
	}
	if owner := docxExportRequest(t, private.ID, artifactVersion(private), cookies); owner.Code != http.StatusOK {
		t.Fatalf("owner private export status=%d body=%s", owner.Code, owner.Body.String())
	}
}
