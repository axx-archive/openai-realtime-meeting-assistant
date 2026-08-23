package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

type deckPPTXDenyExportAuthorizer struct{}

func (deckPPTXDenyExportAuthorizer) AuthorizeArtifactHeader(_ context.Context, _ *userAccount, action ACLAction, _ ArtifactAuthorizationHeader) bool {
	return action != ACLExport
}

func deckPPTXTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			canvas.SetRGBA(x, y, color.RGBA{R: uint8(40 + x%160), G: uint8(70 + y%150), B: 130, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func deckPPTXFixture(t *testing.T) (deckDocument, string, []byte) {
	t.Helper()
	imageBytes := deckPPTXTestPNG(t, 160, 90)
	digest := fmt.Sprintf("%x", sha256Bytes(imageBytes))
	deck := deckDocument{SchemaVersion: 1, Width: 1920, Height: 1080, Slides: []deckSlide{
		{ID: "cover", Background: "#10141c", Notes: "Open with the field story [BEAT]", Elements: []deckElement{
			{ID: "scrim", Type: "shape", X: 0, Y: 0, Width: 1920, Height: 1080, Z: 0, Opacity: .55, Shape: "rectangle", Fill: "#000000"},
			{ID: "hero-image", Type: "image", X: 960, Y: 0, Width: 960, Height: 1080, Z: 1, Opacity: .8, Rotation: 2.5, Ref: digest, Name: "field.png", Prompt: "Working farm at first light", Fit: "cover"},
			{ID: "headline", Type: "text", X: 120, Y: 100, Width: 920, Height: 240, Z: 2, Opacity: 1, Text: "Farmers < founders", FontSize: 80, FontFamily: "Arial, sans-serif", FontWeight: 700, Color: "#ffffff", TextAlign: "left", LineHeight: 1.08},
			{ID: "proof", Type: "shape", X: 120, Y: 650, Width: 260, Height: 260, Z: 3, Opacity: .7, Shape: "ellipse", Fill: "#ff6633", Stroke: "#ffffff", StrokeWidth: 4},
		}},
		{ID: "close", Background: "#f2eee5", Elements: []deckElement{
			{ID: "contained-image", Type: "image", X: 0, Y: 0, Width: 400, Height: 400, Z: 0, Opacity: 1, Ref: digest, Name: "field.png", Fit: "contain"},
			{ID: "close-title", Type: "text", X: 180, Y: 180, Width: 1500, Height: 180, Z: 1, Opacity: 1, Text: "The second slide", FontSize: 72, FontFamily: "Georgia", FontWeight: 600, Color: "#151515", TextAlign: "center", LineHeight: 1.1},
			{ID: "contained-image-2", Type: "image", X: 500, Y: 500, Width: 400, Height: 400, Z: 2, Opacity: 1, Ref: digest, Name: "field.png", Fit: "contain"},
		}},
	}}
	return deck, digest, imageBytes
}

func sha256Bytes(data []byte) [32]byte {
	return sha256.Sum256(data)
}

func deckPPTXZipParts(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open PPTX zip: %v", err)
	}
	parts := make(map[string][]byte, len(reader.File))
	for _, file := range reader.File {
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(stream)
		_ = stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		parts[file.Name] = body
	}
	return parts
}

func TestCompileDeckDocumentPPTXProducesEditableBoundedOOXML(t *testing.T) {
	deck, imageRef, imageBytes := deckPPTXFixture(t)
	pptx, err := compileDeckDocumentPPTX(deck, map[string]struct{}{imageRef: {}}, func(ref string) ([]byte, string, error) {
		if ref != imageRef {
			return nil, "", fmt.Errorf("unexpected ref")
		}
		return imageBytes, "image/png", nil
	}, "Like a Farmer")
	if err != nil {
		t.Fatalf("compile PPTX: %v", err)
	}
	if len(pptx) < 1000 || len(pptx) > deckPPTXMaxBytes {
		t.Fatalf("PPTX bytes=%d, outside expected bounds", len(pptx))
	}
	repeated, err := compileDeckDocumentPPTX(deck, map[string]struct{}{imageRef: {}}, func(string) ([]byte, string, error) {
		return imageBytes, "image/png", nil
	}, "Like a Farmer")
	if err != nil || !bytes.Equal(pptx, repeated) {
		t.Fatalf("same canonical deck did not compile to deterministic PPTX bytes: err=%v equal=%v", err, bytes.Equal(pptx, repeated))
	}
	parts := deckPPTXZipParts(t, pptx)
	required := []string{
		"[Content_Types].xml", "_rels/.rels", "ppt/presentation.xml", "ppt/_rels/presentation.xml.rels",
		"ppt/slideMasters/slideMaster1.xml", "ppt/slideLayouts/slideLayout1.xml", "ppt/theme/theme1.xml",
		"ppt/slides/slide1.xml", "ppt/slides/slide2.xml", "ppt/slides/_rels/slide1.xml.rels",
		"ppt/notesMasters/notesMaster1.xml", "ppt/notesSlides/notesSlide1.xml", "ppt/notesSlides/_rels/notesSlide1.xml.rels",
		"ppt/media/image-" + imageRef[:16] + ".png",
	}
	for _, path := range required {
		if _, ok := parts[path]; !ok {
			t.Errorf("PPTX missing required part %s", path)
		}
	}
	if !bytes.Equal(parts["ppt/media/image-"+imageRef[:16]+".png"], imageBytes) {
		t.Fatal("PPTX image bytes differ from the attached content-addressed image")
	}

	// Every XML part must be well-formed, not merely present in a ZIP.
	paths := make([]string, 0, len(parts))
	for path := range parts {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if !strings.HasSuffix(path, ".xml") && !strings.HasSuffix(path, ".rels") {
			continue
		}
		decoder := xml.NewDecoder(bytes.NewReader(parts[path]))
		for {
			if _, err := decoder.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("PPTX XML %s is malformed: %v\n%s", path, err, parts[path])
			}
		}
	}

	presentation := string(parts["ppt/presentation.xml"])
	if strings.Count(presentation, "<p:sldId ") != 2 || !strings.Contains(presentation, `cx="12192000" cy="6858000"`) {
		t.Fatalf("presentation slide count/size drifted: %s", presentation)
	}
	slide := string(parts["ppt/slides/slide1.xml"])
	for _, want := range []string{
		`name="scrim"`, `prst="rect"`, `name="hero-image"`, `descr="Working farm at first light"`,
		`name="headline"`, `Farmers &lt; founders`, `typeface="Arial"`, `sz="6000"`,
		`name="proof"`, `prst="ellipse"`, `rot="150000"`, `<a:alpha val="55000"/>`, `<a:alphaModFix amt="80000"/>`, `<a:srcRect`,
	} {
		if !strings.Contains(slide, want) {
			t.Errorf("slide XML missing %q: %s", want, slide)
		}
	}
	indices := []int{strings.Index(slide, `name="scrim"`), strings.Index(slide, `name="hero-image"`), strings.Index(slide, `name="headline"`), strings.Index(slide, `name="proof"`)}
	if indices[0] < 0 || indices[0] >= indices[1] || indices[1] >= indices[2] || indices[2] >= indices[3] {
		t.Fatalf("editable element XML order does not preserve z order: %v", indices)
	}
	if notes := string(parts["ppt/notesSlides/notesSlide1.xml"]); !strings.Contains(notes, "Open with the field story [BEAT]") {
		t.Fatalf("speaker notes missing: %s", notes)
	}
	if rels := string(parts["ppt/slides/_rels/slide1.xml.rels"]); !strings.Contains(rels, "../media/image-") || !strings.Contains(rels, "/notesSlide") {
		t.Fatalf("slide relationships do not bind image and notes: %s", rels)
	}
	contained := string(parts["ppt/slides/slide2.xml"])
	if !strings.Contains(contained, `name="contained-image"`) || !strings.Contains(contained, `<a:off x="0" y="555625"/><a:ext cx="2540000" cy="1428750"/>`) || strings.Contains(contained, `<a:srcRect`) {
		t.Fatalf("contain-fit image did not preserve aspect ratio inside its frame: %s", contained)
	}
	if rels := string(parts["ppt/slides/_rels/slide2.xml.rels"]); strings.Count(rels, deckPPTXOfficeDocumentNS+`/image`) != 1 {
		t.Fatalf("reused image ref should create one slide relationship: %s", rels)
	}
}

func deckPPTXRunPropertiesForText(t *testing.T, slide, value string) string {
	t.Helper()
	textXML := `<a:t xml:space="preserve">` + value + `</a:t>`
	textIndex := strings.Index(slide, textXML)
	if textIndex < 0 {
		t.Fatalf("PPTX slide is missing editable text run %q: %s", value, slide)
	}
	runStart := strings.LastIndex(slide[:textIndex], `<a:rPr`)
	if runStart < 0 {
		t.Fatalf("PPTX text %q is not backed by editable run properties: %s", value, slide)
	}
	runEndOffset := strings.Index(slide[runStart:textIndex], `</a:rPr>`)
	if runEndOffset < 0 {
		t.Fatalf("PPTX text %q is not backed by editable run properties: %s", value, slide)
	}
	return slide[runStart : runStart+runEndOffset+len(`</a:rPr>`)]
}

func TestDeckPPTXPictureHonorsNativeFocalPoint(t *testing.T) {
	left, top := .25, .75
	element := deckElement{
		ID: "hero", Type: "image", X: 0, Y: 0, Width: 400, Height: 400, Opacity: 1,
		Fit: "cover", Crop: "safe_area", FocalX: &left, FocalY: &top,
	}
	wide := deckPPTXPictureXML(1, element, deckPPTXMedia{Width: 800, Height: 400}, "rId1")
	if !strings.Contains(wide, `<a:srcRect l="12500" r="37500"/>`) {
		t.Fatalf("wide focal crop was recentered: %s", wide)
	}
	tall := deckPPTXPictureXML(1, element, deckPPTXMedia{Width: 400, Height: 800}, "rId1")
	if !strings.Contains(tall, `<a:srcRect t="37500" b="12500"/>`) {
		t.Fatalf("tall focal crop was recentered: %s", tall)
	}
	for _, test := range []struct {
		focal float64
		want  string
	}{{0, `l="0" r="50000"`}, {.25, `l="12500" r="37500"`}, {.5, `l="25000" r="25000"`}, {.75, `l="37500" r="12500"`}, {1, `l="50000" r="0"`}} {
		element.FocalX = &test.focal
		got := deckPPTXPictureXML(1, element, deckPPTXMedia{Width: 800, Height: 400}, "rId1")
		if !strings.Contains(got, test.want) {
			t.Fatalf("CSS focal %.2f did not map to proportional cover crop %q: %s", test.focal, test.want, got)
		}
	}
	element.FocalX = &left

	element.Fit = "contain"
	contained := deckPPTXPictureXML(1, element, deckPPTXMedia{Width: 800, Height: 400}, "rId1")
	// A 2:1 image inside a square becomes 400x200; focalY=.75 places it 150px
	// below the top, which is 952500 EMU at the deck's 1920px scale.
	if !strings.Contains(contained, `<a:off x="0" y="952500"/><a:ext cx="2540000" cy="1270000"/>`) {
		t.Fatalf("contained focal position was recentered: %s", contained)
	}
}

func TestCompileDeckDocumentPPTXPreservesCanonicalMixedRichTextRunsAndHierarchy(t *testing.T) {
	richText := `OBSERVED <span style="color:#C79B4D;display:block;font-family:Georgia,serif;font-size:75px;font-weight:700;letter-spacing:.04em;line-height:.92;margin:9px 0">6.1M</span><span style="color:#B84F32"><strong>You</strong><em>Tube</em></span><br><span style="text-decoration:underline">trusted</span> <span style="text-decoration:line-through">old</span> H<sub>2</sub>O<sup>+</sup>`
	element := deckElement{
		ID: "score", Type: "text", X: 100, Y: 100, Width: 900, Height: 600, Z: 1, Opacity: .8,
		Text: "FLATTENED FALLBACK MUST NOT WIN", RichText: richText, FontSize: 24, FontFamily: "Arial, sans-serif",
		FontWeight: 400, Color: "#ffffff", TextAlign: "left", LineHeight: 1.2, LetterSpacing: "normal",
	}
	deck := deckDocument{SchemaVersion: 1, Width: 1920, Height: 1080, Slides: []deckSlide{{
		ID: "proof", Background: "#111111", Elements: []deckElement{element},
	}}}
	if err := validateDeckDocument(deck, nil); err != nil {
		t.Fatalf("rich-text fixture must obey the Deck Studio save contract: %v", err)
	}
	pptx, err := compileDeckDocumentPPTX(deck, nil, func(string) ([]byte, string, error) {
		return nil, "", fmt.Errorf("image resolver must not run for a text-only deck")
	}, "Rich text proof")
	if err != nil {
		t.Fatalf("compile rich-text PPTX: %v", err)
	}
	slide := string(deckPPTXZipParts(t, pptx)["ppt/slides/slide1.xml"])
	if strings.Contains(slide, "FLATTENED FALLBACK") {
		t.Fatalf("PPTX preferred the flat compatibility text over canonical rich text: %s", slide)
	}
	orderedText := []string{"OBSERVED ", "6.1M", "You", "Tube", "trusted", "old", " H", "2", "O", "+"}
	previous := -1
	for _, value := range orderedText {
		index := strings.Index(slide, `<a:t xml:space="preserve">`+value+`</a:t>`)
		if index < 0 || index <= previous {
			t.Fatalf("editable rich-text run %q is missing or out of order: %s", value, slide)
		}
		previous = index
	}
	if got := strings.Count(slide, "<a:p>"); got != 4 {
		t.Fatalf("rich block and hard-break hierarchy produced %d paragraphs, want 4: %s", got, slide)
	}

	blockText := strings.Index(slide, `>6.1M</a:t>`)
	if blockText < 0 {
		t.Fatalf("rich block text is missing: %s", slide)
	}
	blockStart := strings.LastIndex(slide[:blockText], "<a:p>")
	if blockStart < 0 {
		t.Fatalf("rich block did not produce an editable paragraph: %s", slide)
	}
	blockEnd := strings.Index(slide[blockStart:], "</a:p>")
	if blockEnd < 0 {
		t.Fatalf("rich block did not produce an editable paragraph: %s", slide)
	}
	blockParagraph := slide[blockStart : blockStart+blockEnd]
	for _, want := range []string{`<a:spcPct val="92000"/>`, `<a:spcBef><a:spcPts val="675"/>`, `<a:spcAft><a:spcPts val="675"/>`} {
		if !strings.Contains(blockParagraph, want) {
			t.Errorf("rich block paragraph missing %q: %s", want, blockParagraph)
		}
	}

	assertRun := func(value string, wants ...string) {
		t.Helper()
		properties := deckPPTXRunPropertiesForText(t, slide, value)
		for _, want := range wants {
			if !strings.Contains(properties, want) {
				t.Errorf("run %q properties missing %q: %s", value, want, properties)
			}
		}
	}
	assertRun("6.1M", `sz="5625"`, `b="1"`, `spc="225"`, `val="C79B4D"`, `<a:alpha val="80000"/>`, `typeface="Georgia"`)
	assertRun("You", `b="1"`, `val="B84F32"`)
	assertRun("Tube", `b="0"`, `i="1"`, `val="B84F32"`)
	assertRun("trusted", `u="sng"`)
	assertRun("old", `strike="sngStrike"`)
	assertRun("2", `baseline="-25000"`)
	assertRun("+", `baseline="30000"`)

	if _, ok := deckPPTXRichTextParagraphs(`<span onclick="alert(1)">unsafe</span>`, element); ok {
		t.Fatal("PPTX rich-text projection accepted markup rejected by the Deck Studio sanitizer")
	}
}

func TestCompileDeckDocumentPPTXFailsClosedForUnattachedOrInvalidImage(t *testing.T) {
	deck, imageRef, imageBytes := deckPPTXFixture(t)
	if _, err := compileDeckDocumentPPTX(deck, nil, func(string) ([]byte, string, error) { return imageBytes, "image/png", nil }, ""); err == nil || !strings.Contains(err.Error(), "not attached") {
		t.Fatalf("unattached image error=%v, want fail closed", err)
	}
	if _, err := compileDeckDocumentPPTX(deck, map[string]struct{}{imageRef: {}}, func(string) ([]byte, string, error) { return []byte("not png"), "image/png", nil }, ""); err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("invalid image error=%v, want dimension validation", err)
	}
}

func TestDeckPPTXFilenameIsAttachmentSafe(t *testing.T) {
	got := deckPPTXFilename("../Board: Q3\r\nPitch?.pptx")
	if got != "-Board- Q3--Pitch-.pptx" || strings.ContainsAny(got, "/\\\r\n") {
		t.Fatalf("safe PowerPoint filename=%q", got)
	}
}

func TestCompileDeckDocumentPPTXOpensInLibreOfficeWhenAvailable(t *testing.T) {
	soffice, err := exec.LookPath("soffice")
	if err != nil {
		t.Skip("LibreOffice is unavailable")
	}
	deck, imageRef, imageBytes := deckPPTXFixture(t)
	// Exercise editable mixed runs in the real office-suite smoke test, not
	// only in structural XML assertions.
	deck.Slides[0].Elements[2].RichText = `<strong>Farmers</strong> &lt; <em>founders</em>`
	pptx, err := compileDeckDocumentPPTX(deck, map[string]struct{}{imageRef: {}}, func(string) ([]byte, string, error) {
		return imageBytes, "image/png", nil
	}, "Like a Farmer")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "like-a-farmer.pptx")
	if err := os.WriteFile(path, pptx, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, soffice, "--headless", "--convert-to", "pdf", "--outdir", dir, path)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("LibreOffice could not open/export generated PPTX: %v\n%s", err, output)
	}
	pdf, err := os.ReadFile(filepath.Join(dir, "like-a-farmer.pdf"))
	if err != nil || len(pdf) < 1000 || !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("LibreOffice produced no valid PDF: bytes=%d err=%v output=%s", len(pdf), err, output)
	}
	if pdfinfo, lookupErr := exec.LookPath("pdfinfo"); lookupErr == nil {
		info, infoErr := exec.CommandContext(ctx, pdfinfo, filepath.Join(dir, "like-a-farmer.pdf")).CombinedOutput()
		if infoErr != nil {
			t.Fatalf("inspect LibreOffice PDF: %v\n%s", infoErr, info)
		}
		pageCount := ""
		for _, line := range strings.Split(string(info), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "Pages:" {
				pageCount = fields[1]
			}
		}
		if pageCount != "2" {
			t.Fatalf("LibreOffice round trip page count=%q, want exact two slides\n%s", pageCount, info)
		}
	}
}

func setupDeckPPTXHTTPFixture(t *testing.T, authorizer ObjectAuthorizer) ([]*http.Cookie, meetingMemoryEntry, deckDocument) {
	t.Helper()
	setupAuthTestEnv(t)
	setupIsolatedBlobStore(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = authorizer
	t.Cleanup(func() { kanbanApp = previousApp; artifactObjectAuthorizer = previousAuthorizer })
	deck, imageRef, imageBytes := deckPPTXFixture(t)
	storedRef, err := putBlob(imageBytes, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if storedRef != imageRef {
		t.Fatalf("stored image ref=%s want=%s", storedRef, imageRef)
	}
	scene, err := json.Marshal(deck)
	if err != nil {
		t.Fatal(err)
	}
	sceneRef, err := putBlob(scene, "application/vnd.bonfire.deck+json")
	if err != nil {
		t.Fatal(err)
	}
	assets, _ := json.Marshal([]artifactAsset{{Ref: imageRef, Mime: "image/png", Name: "field.png", Kind: "image"}})
	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("design", "Like a Farmer", compileDeckDocumentHTML(deck, "Like a Farmer"), "AJ", map[string]string{
		"type": artifactTypeHTMLDeck, "title": "Like a Farmer", "visibility": "organization", "requestedBy": "aj@shareability.com",
		deckSceneRefMetadataKey: sceneRef, deckSchemaMetadataKey: "1", artifactAssetsMetadataKey: string(assets),
	})
	if err != nil {
		t.Fatal(err)
	}
	return loginAs(t, "aj@shareability.com", "B0NFIRE!"), artifact, deck
}

func deckPPTXRequest(t *testing.T, artifact meetingMemoryEntry, cookies []*http.Cookie, mutate func(map[string]any)) *httptest.ResponseRecorder {
	t.Helper()
	payload := map[string]any{"artifactId": artifact.ID, "expectedVersion": artifactVersion(artifact), "sceneRef": artifact.Metadata[deckSceneRefMetadataKey]}
	if mutate != nil {
		mutate(payload)
	}
	body, _ := json.Marshal(payload)
	return artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/export-pptx", string(body), cookies, deckPPTXExportHandler)
}

func TestDeckPPTXExportHandlerBindsACLVersionSceneAndDownloadHeaders(t *testing.T) {
	t.Run("authorized exact native scene downloads", func(t *testing.T) {
		cookies, artifact, _ := setupDeckPPTXHTTPFixture(t, LegacyCompatibleObjectAuthorizer{})
		get := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/deck?id="+artifact.ID, "", cookies, deckEditorHandler)
		var deckPayload struct {
			Artifact deckArtifactView `json:"artifact"`
		}
		if get.Code != http.StatusOK || json.Unmarshal(get.Body.Bytes(), &deckPayload) != nil || deckPayload.Artifact.SceneRef != artifact.Metadata[deckSceneRefMetadataKey] {
			t.Fatalf("deck GET did not expose the exact scene-ref export binding: status=%d payload=%s", get.Code, get.Body.String())
		}
		response := deckPPTXRequest(t, artifact, cookies, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("export status=%d body=%s", response.Code, response.Body.String())
		}
		if got := response.Header().Get("Content-Type"); got != deckPPTXContentType {
			t.Fatalf("Content-Type=%q want=%q", got, deckPPTXContentType)
		}
		disposition, params, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
		if err != nil || disposition != "attachment" || params["filename"] != "Like a Farmer.pptx" {
			t.Fatalf("Content-Disposition=%q parsed=%q params=%v err=%v", response.Header().Get("Content-Disposition"), disposition, params, err)
		}
		if response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Content-Length") != strconv.Itoa(response.Body.Len()) {
			t.Fatalf("download cache/length headers=%v", response.Header())
		}
		parts := deckPPTXZipParts(t, response.Body.Bytes())
		if _, ok := parts["ppt/slides/slide2.xml"]; !ok {
			t.Fatal("downloaded PowerPoint lost the second slide")
		}
		after, ok := kanbanApp.osArtifactByID(artifact.ID)
		if !ok || artifactVersion(after) != artifactVersion(artifact) || after.Metadata[deckSceneRefMetadataKey] != artifact.Metadata[deckSceneRefMetadataKey] || after.Metadata[artifactAssetsMetadataKey] != artifact.Metadata[artifactAssetsMetadataKey] {
			t.Fatalf("read-only PowerPoint download mutated the artifact: before=%+v after=%+v", artifact.Metadata, after.Metadata)
		}
	})

	t.Run("signed out and denied ACLExport are non-oracular", func(t *testing.T) {
		cookies, artifact, _ := setupDeckPPTXHTTPFixture(t, deckPPTXDenyExportAuthorizer{})
		if response := deckPPTXRequest(t, artifact, nil, nil); response.Code != http.StatusUnauthorized {
			t.Fatalf("signed-out status=%d body=%s", response.Code, response.Body.String())
		}
		if response := deckPPTXRequest(t, artifact, cookies, nil); response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "Like a Farmer") {
			t.Fatalf("denied export status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("stale version and scene ref conflict", func(t *testing.T) {
		cookies, artifact, _ := setupDeckPPTXHTTPFixture(t, LegacyCompatibleObjectAuthorizer{})
		for _, mutate := range []func(map[string]any){
			func(payload map[string]any) { payload["expectedVersion"] = artifactVersion(artifact) + 1 },
			func(payload map[string]any) { payload["sceneRef"] = strings.Repeat("a", 64) },
		} {
			response := deckPPTXRequest(t, artifact, cookies, mutate)
			if response.Code != http.StatusConflict || response.Header().Get("Content-Type") == deckPPTXContentType || response.Body.Len() >= 1000 {
				t.Fatalf("stale export status=%d headers=%v bytes=%d body=%s", response.Code, response.Header(), response.Body.Len(), response.Body.String())
			}
		}
	})

	t.Run("cross origin fails before export", func(t *testing.T) {
		cookies, artifact, _ := setupDeckPPTXHTTPFixture(t, LegacyCompatibleObjectAuthorizer{})
		payload, _ := json.Marshal(map[string]any{"artifactId": artifact.ID, "expectedVersion": artifactVersion(artifact), "sceneRef": artifact.Metadata[deckSceneRefMetadataKey]})
		req := httptest.NewRequest(http.MethodPost, "/artifacts/export-pptx", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://attacker.invalid")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		deckPPTXExportHandler(response, req)
		if response.Code != http.StatusForbidden || response.Body.Len() >= 1000 {
			t.Fatalf("cross-origin status=%d bytes=%d body=%s", response.Code, response.Body.Len(), response.Body.String())
		}
	})
}

func TestDeckPPTXExportRejectsApproximateLegacyAndUnattachedNativeScene(t *testing.T) {
	t.Run("approximate legacy never exports", func(t *testing.T) {
		cookies, artifact := setupDeckEditorHTTPTest(t, LegacyCompatibleObjectAuthorizer{})
		unsupported := strings.Replace(faithfulDeckHTML, "</section>", `<button onclick="advance()">Advance</button></section>`, 1)
		artifact, _, err := kanbanApp.updateOSArtifactWithMetadata(artifact.ID, "", unsupported, "AJ", nil)
		if err != nil {
			t.Fatal(err)
		}
		response := deckPPTXRequest(t, artifact, cookies, func(payload map[string]any) { payload["sceneRef"] = strings.Repeat("a", 64) })
		if response.Code != http.StatusConflict || response.Body.Len() >= 1000 {
			t.Fatalf("approximate legacy status=%d bytes=%d body=%s", response.Code, response.Body.Len(), response.Body.String())
		}
	})

	t.Run("native scene with unattached image fails closed", func(t *testing.T) {
		cookies, artifact, deck := setupDeckPPTXHTTPFixture(t, LegacyCompatibleObjectAuthorizer{})
		current, _ := kanbanApp.osArtifactByID(artifact.ID)
		current.Metadata[artifactAssetsMetadataKey] = "[]"
		scene, _ := json.Marshal(deck)
		sceneRef, err := putBlob(scene, "application/vnd.bonfire.deck+json")
		if err != nil {
			t.Fatal(err)
		}
		updated, _, err := kanbanApp.updateOSArtifactWithMetadata(current.ID, "", current.Text, "AJ", map[string]string{artifactAssetsMetadataKey: "[]", deckSceneRefMetadataKey: sceneRef})
		if err != nil {
			t.Fatal(err)
		}
		response := deckPPTXRequest(t, updated, cookies, nil)
		if response.Code != http.StatusConflict || response.Body.Len() >= 1000 {
			t.Fatalf("unattached native status=%d bytes=%d body=%s", response.Code, response.Body.Len(), response.Body.String())
		}
	})
}
