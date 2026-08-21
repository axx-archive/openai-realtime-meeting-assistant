package main

// deck_pptx.go exports the canonical, validated Deck Studio scene graph as a
// bounded editable PowerPoint package. It deliberately does not translate the
// artifact's HTML projection: HTML is a viewer/export projection, while the
// inert deckDocument is the authority for editable geometry and content.

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"html"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	xhtml "golang.org/x/net/html"
)

const (
	deckPPTXContentType       = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	deckPPTXMaxBytes          = 64 << 20
	deckPPTXMaxMediaBytes     = 48 << 20
	deckPPTXSlideWidthEMU     = 12192000
	deckPPTXSlideHeightEMU    = 6858000
	deckPPTXEMUPerCanvasUnit  = 6350
	deckPPTXRelationshipNS    = "http://schemas.openxmlformats.org/package/2006/relationships"
	deckPPTXOfficeDocumentNS  = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	deckPPTXPresentationMLNS  = "http://schemas.openxmlformats.org/presentationml/2006/main"
	deckPPTXDrawingMLNS       = "http://schemas.openxmlformats.org/drawingml/2006/main"
	deckPPTXContentTypesNS    = "http://schemas.openxmlformats.org/package/2006/content-types"
	deckPPTXCorePropertiesNS  = "http://schemas.openxmlformats.org/package/2006/metadata/core-properties"
	deckPPTXDublinCoreNS      = "http://purl.org/dc/elements/1.1/"
	deckPPTXDublinCoreTermsNS = "http://purl.org/dc/terms/"
)

type deckPPTXImageResolver func(ref string) ([]byte, string, error)

type deckPPTXMedia struct {
	Ref       string
	Path      string
	Mime      string
	Extension string
	Width     int
	Height    int
	Data      []byte
}

type deckPPTXPart struct {
	Path string
	Data []byte
}

type deckPPTXRelationship struct {
	ID     string
	Type   string
	Target string
}

// compileDeckDocumentPPTX accepts only the same bounded scene graph the Deck
// Studio save path accepts. Images are resolved through content-addressed refs
// already attached to the artifact; no URL, HTML, script, or remote fetch is
// admitted into the package compiler.
func compileDeckDocumentPPTX(deck deckDocument, allowedImageRefs map[string]struct{}, resolve deckPPTXImageResolver, title string) ([]byte, error) {
	if err := validateDeckDocument(deck, allowedImageRefs); err != nil {
		return nil, err
	}
	if resolve == nil {
		return nil, fmt.Errorf("PowerPoint image resolver is unavailable")
	}

	mediaByRef := map[string]deckPPTXMedia{}
	mediaBytes := 0
	for _, slide := range deck.Slides {
		for _, element := range slide.Elements {
			if element.Type != "image" {
				continue
			}
			if _, seen := mediaByRef[element.Ref]; seen {
				continue
			}
			if _, allowed := allowedImageRefs[element.Ref]; !allowed {
				return nil, fmt.Errorf("PowerPoint image is not attached to this artifact")
			}
			data, imageMime, err := resolve(element.Ref)
			if err != nil {
				return nil, fmt.Errorf("resolve PowerPoint image %s: %w", element.Ref, err)
			}
			imageMime = strings.ToLower(strings.TrimSpace(strings.Split(imageMime, ";")[0]))
			extension, err := deckPPTXImageExtension(imageMime)
			if err != nil {
				return nil, err
			}
			width, height, err := deckPPTXImageDimensions(data, imageMime)
			if err != nil {
				return nil, fmt.Errorf("read PowerPoint image dimensions: %w", err)
			}
			mediaBytes += len(data)
			if mediaBytes > deckPPTXMaxMediaBytes {
				return nil, fmt.Errorf("PowerPoint images exceed the %d-byte export bound", deckPPTXMaxMediaBytes)
			}
			mediaByRef[element.Ref] = deckPPTXMedia{
				Ref: element.Ref, Path: "ppt/media/image-" + element.Ref[:16] + "." + extension,
				Mime: imageMime, Extension: extension, Width: width, Height: height, Data: data,
			}
		}
	}

	noteSlides := map[int]bool{}
	for index, slide := range deck.Slides {
		if strings.TrimSpace(slide.Notes) != "" {
			noteSlides[index+1] = true
		}
	}

	parts := make([]deckPPTXPart, 0, 16+len(deck.Slides)*3+len(mediaByRef))
	add := func(path, body string) { parts = append(parts, deckPPTXPart{Path: path, Data: []byte(body)}) }

	add("_rels/.rels", deckPPTXRelationshipsXML([]deckPPTXRelationship{
		{ID: "rId1", Type: deckPPTXOfficeDocumentNS + "/officeDocument", Target: "ppt/presentation.xml"},
		{ID: "rId2", Type: deckPPTXRelationshipNS + "/metadata/core-properties", Target: "docProps/core.xml"},
		{ID: "rId3", Type: deckPPTXOfficeDocumentNS + "/extended-properties", Target: "docProps/app.xml"},
	}))
	add("docProps/core.xml", deckPPTXCoreXML(title))
	add("docProps/app.xml", deckPPTXAppXML(len(deck.Slides), len(noteSlides)))
	add("ppt/theme/theme1.xml", deckPPTXThemeXML())
	add("ppt/slideMasters/slideMaster1.xml", deckPPTXSlideMasterXML())
	add("ppt/slideMasters/_rels/slideMaster1.xml.rels", deckPPTXRelationshipsXML([]deckPPTXRelationship{
		{ID: "rId1", Type: deckPPTXOfficeDocumentNS + "/slideLayout", Target: "../slideLayouts/slideLayout1.xml"},
		{ID: "rId2", Type: deckPPTXOfficeDocumentNS + "/theme", Target: "../theme/theme1.xml"},
	}))
	add("ppt/slideLayouts/slideLayout1.xml", deckPPTXSlideLayoutXML())
	add("ppt/slideLayouts/_rels/slideLayout1.xml.rels", deckPPTXRelationshipsXML([]deckPPTXRelationship{{ID: "rId1", Type: deckPPTXOfficeDocumentNS + "/slideMaster", Target: "../slideMasters/slideMaster1.xml"}}))

	if len(noteSlides) > 0 {
		add("ppt/notesMasters/notesMaster1.xml", deckPPTXNotesMasterXML())
		add("ppt/notesMasters/_rels/notesMaster1.xml.rels", deckPPTXRelationshipsXML([]deckPPTXRelationship{{ID: "rId1", Type: deckPPTXOfficeDocumentNS + "/theme", Target: "../theme/theme1.xml"}}))
	}

	for slideIndex, slide := range deck.Slides {
		rels := []deckPPTXRelationship{{ID: "rId1", Type: deckPPTXOfficeDocumentNS + "/slideLayout", Target: "../slideLayouts/slideLayout1.xml"}}
		relationByRef := map[string]string{}
		seenRefs := map[string]struct{}{}
		orderedRefs := make([]string, 0)
		for _, element := range slide.Elements {
			if element.Type != "image" {
				continue
			}
			if _, seen := seenRefs[element.Ref]; seen {
				continue
			}
			seenRefs[element.Ref] = struct{}{}
			orderedRefs = append(orderedRefs, element.Ref)
		}
		sort.Strings(orderedRefs)
		for _, ref := range orderedRefs {
			id := fmt.Sprintf("rId%d", len(rels)+1)
			relationByRef[ref] = id
			media := mediaByRef[ref]
			rels = append(rels, deckPPTXRelationship{ID: id, Type: deckPPTXOfficeDocumentNS + "/image", Target: "../media/" + strings.TrimPrefix(media.Path, "ppt/media/")})
		}
		if noteSlides[slideIndex+1] {
			rels = append(rels, deckPPTXRelationship{ID: fmt.Sprintf("rId%d", len(rels)+1), Type: deckPPTXOfficeDocumentNS + "/notesSlide", Target: fmt.Sprintf("../notesSlides/notesSlide%d.xml", slideIndex+1)})
		}
		add(fmt.Sprintf("ppt/slides/slide%d.xml", slideIndex+1), deckPPTXSlideXML(slide, mediaByRef, relationByRef))
		add(fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", slideIndex+1), deckPPTXRelationshipsXML(rels))
		if noteSlides[slideIndex+1] {
			add(fmt.Sprintf("ppt/notesSlides/notesSlide%d.xml", slideIndex+1), deckPPTXNotesSlideXML(slide.Notes))
			add(fmt.Sprintf("ppt/notesSlides/_rels/notesSlide%d.xml.rels", slideIndex+1), deckPPTXRelationshipsXML([]deckPPTXRelationship{
				{ID: "rId1", Type: deckPPTXOfficeDocumentNS + "/notesMaster", Target: "../notesMasters/notesMaster1.xml"},
				{ID: "rId2", Type: deckPPTXOfficeDocumentNS + "/slide", Target: fmt.Sprintf("../slides/slide%d.xml", slideIndex+1)},
			}))
		}
	}

	add("ppt/presentation.xml", deckPPTXPresentationXML(len(deck.Slides), len(noteSlides) > 0))
	add("ppt/_rels/presentation.xml.rels", deckPPTXPresentationRelationshipsXML(len(deck.Slides), len(noteSlides) > 0))
	add("[Content_Types].xml", deckPPTXContentTypesXML(len(deck.Slides), noteSlides, mediaByRef))
	for _, media := range mediaByRef {
		parts = append(parts, deckPPTXPart{Path: media.Path, Data: media.Data})
	}

	sort.Slice(parts, func(i, j int) bool { return parts[i].Path < parts[j].Path })
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, part := range parts {
		header := &zip.FileHeader{Name: part.Path, Method: zip.Deflate}
		header.SetModTime(time.Unix(0, 0).UTC())
		entry, err := writer.CreateHeader(header)
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
	if output.Len() == 0 || output.Len() > deckPPTXMaxBytes {
		return nil, fmt.Errorf("PowerPoint export is %d bytes, outside the 1-%d byte bound", output.Len(), deckPPTXMaxBytes)
	}
	return output.Bytes(), nil
}

func deckPPTXExportHandler(w http.ResponseWriter, r *http.Request) {
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
		SceneRef        string `json:"sceneRef"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read PowerPoint export request")
		return
	}
	payload.ArtifactID = strings.TrimSpace(payload.ArtifactID)
	payload.SceneRef = strings.TrimSpace(payload.SceneRef)
	artifact, ok := authorizedArtifactByID(r.Context(), user, ACLExport, payload.ArtifactID)
	if !ok || !artifactIsDeckEditorDocument(artifact) {
		writeAuthError(w, http.StatusNotFound, "deck artifact not found")
		return
	}
	currentRef := strings.TrimSpace(artifact.Metadata[deckSceneRefMetadataKey])
	if currentRef == "" {
		_, imported, quality, loadErr := loadDeckDocument(artifact)
		if loadErr != nil || imported || quality != "native" {
			writeAuthError(w, http.StatusConflict, "legacy deck must be saved as a faithful native deck before PowerPoint export")
			return
		}
	}
	if payload.ExpectedVersion < 1 || artifactVersion(artifact) != payload.ExpectedVersion || !validBlobRef(payload.SceneRef) || payload.SceneRef != currentRef {
		writeAuthError(w, http.StatusConflict, "the deck changed; reopen it before downloading PowerPoint")
		return
	}
	deck, imported, quality, err := loadDeckDocument(artifact)
	if err != nil {
		writeAuthError(w, http.StatusConflict, "deck document is unavailable")
		return
	}
	if imported || quality != "native" {
		writeAuthError(w, http.StatusConflict, "legacy deck must be saved as a faithful native deck before PowerPoint export")
		return
	}
	allowedRefs := artifactAssetRefSet(artifact)
	pptx, err := compileDeckDocumentPPTX(deck, allowedRefs, func(ref string) ([]byte, string, error) {
		if _, allowed := allowedRefs[ref]; !allowed {
			return nil, "", fmt.Errorf("image is not attached")
		}
		data, meta, err := getBlob(ref)
		if err != nil {
			return nil, "", err
		}
		return data, meta.Mime, nil
	}, strings.TrimSpace(artifact.Metadata["title"]))
	if err != nil {
		writeAuthError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	filename := deckPPTXFilename(strings.TrimSpace(artifact.Metadata["title"]))
	w.Header().Set("Content-Type", deckPPTXContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("Content-Length", strconv.Itoa(len(pptx)))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pptx)
}

func deckPPTXFilename(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Presentation"
	}
	var builder strings.Builder
	for _, char := range title {
		if unicode.IsControl(char) || strings.ContainsRune(`/\\:*?"<>|`, char) {
			builder.WriteRune('-')
		} else {
			builder.WriteRune(char)
		}
		if builder.Len() >= 140 {
			break
		}
	}
	base := strings.Trim(strings.TrimSpace(builder.String()), ".")
	if base == "" {
		base = "Presentation"
	}
	if strings.HasSuffix(strings.ToLower(base), ".pptx") {
		base = strings.TrimSpace(base[:len(base)-len(".pptx")])
	}
	return base + ".pptx"
}

func deckPPTXSlideXML(slide deckSlide, media map[string]deckPPTXMedia, relationByRef map[string]string) string {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sld xmlns:a="` + deckPPTXDrawingMLNS + `" xmlns:r="` + deckPPTXOfficeDocumentNS + `" xmlns:p="` + deckPPTXPresentationMLNS + `"><p:cSld name="` + html.EscapeString(slide.ID) + `">`)
	if color, alpha, ok := deckPPTXColor(slide.Background, 1); ok {
		builder.WriteString(`<p:bg><p:bgPr><a:solidFill>` + deckPPTXColorXML(color, alpha) + `</a:solidFill><a:effectLst/></p:bgPr></p:bg>`)
	}
	builder.WriteString(deckPPTXShapeTreeStart())
	elements := append([]deckElement(nil), slide.Elements...)
	sort.SliceStable(elements, func(i, j int) bool { return elements[i].Z < elements[j].Z })
	for index, element := range elements {
		shapeID := index + 2
		switch element.Type {
		case "text":
			builder.WriteString(deckPPTXTextShapeXML(shapeID, element))
		case "shape":
			builder.WriteString(deckPPTXShapeXML(shapeID, element))
		case "image":
			builder.WriteString(deckPPTXPictureXML(shapeID, element, media[element.Ref], relationByRef[element.Ref]))
		}
	}
	builder.WriteString(`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`)
	return builder.String()
}

func deckPPTXShapeTreeStart() string {
	return `<p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`
}

func deckPPTXTransformXML(element deckElement) string {
	rotation := int64(math.Round(element.Rotation * 60000))
	rot := ""
	if rotation != 0 {
		rot = ` rot="` + strconv.FormatInt(rotation, 10) + `"`
	}
	return `<a:xfrm` + rot + `><a:off x="` + deckPPTXEMU(element.X) + `" y="` + deckPPTXEMU(element.Y) + `"/><a:ext cx="` + deckPPTXEMU(element.Width) + `" cy="` + deckPPTXEMU(element.Height) + `"/></a:xfrm>`
}

func deckPPTXTextShapeXML(id int, element deckElement) string {
	name := html.EscapeString(firstNonEmptyString(element.ID, fmt.Sprintf("Text %d", id)))
	var builder strings.Builder
	builder.WriteString(`<p:sp><p:nvSpPr><p:cNvPr id="` + strconv.Itoa(id) + `" name="` + name + `"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr>`)
	builder.WriteString(deckPPTXTransformXML(element))
	builder.WriteString(`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/><a:ln><a:noFill/></a:ln></p:spPr><p:txBody><a:bodyPr wrap="square" lIns="0" tIns="0" rIns="0" bIns="0" anchor="t"/><a:lstStyle/>`)
	text := element.Text
	if strings.TrimSpace(text) == "" && strings.TrimSpace(element.RichText) != "" {
		text = deckPPTXRichTextPlainText(element.RichText)
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	for _, line := range lines {
		builder.WriteString(deckPPTXParagraphXML(line, element))
	}
	builder.WriteString(`</p:txBody></p:sp>`)
	return builder.String()
}

func deckPPTXParagraphXML(text string, element deckElement) string {
	align := map[string]string{"left": "l", "center": "ctr", "right": "r"}[element.TextAlign]
	if align == "" {
		align = "l"
	}
	lineSpacing := ""
	if element.LineHeight > 0 {
		lineSpacing = `<a:lnSpc><a:spcPct val="` + strconv.Itoa(int(math.Round(element.LineHeight*100000))) + `"/></a:lnSpc>`
	}
	fontSize := int(math.Round(element.FontSize * 75))
	fontName := deckPPTXFontName(element.FontFamily)
	bold := "0"
	if element.FontWeight >= 600 {
		bold = "1"
	}
	color, alpha, ok := deckPPTXColor(element.Color, element.Opacity)
	fill := `<a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill>`
	if ok {
		fill = `<a:solidFill>` + deckPPTXColorXML(color, alpha) + `</a:solidFill>`
	}
	runProps := `<a:rPr lang="en-US" sz="` + strconv.Itoa(fontSize) + `" b="` + bold + `" dirty="0">` + fill + `<a:latin typeface="` + html.EscapeString(fontName) + `"/><a:ea typeface="` + html.EscapeString(fontName) + `"/><a:cs typeface="` + html.EscapeString(fontName) + `"/></a:rPr>`
	if text == "" {
		return `<a:p><a:pPr algn="` + align + `">` + lineSpacing + `</a:pPr><a:endParaRPr lang="en-US" sz="` + strconv.Itoa(fontSize) + `"/></a:p>`
	}
	return `<a:p><a:pPr algn="` + align + `">` + lineSpacing + `</a:pPr><a:r>` + runProps + `<a:t xml:space="preserve">` + html.EscapeString(text) + `</a:t></a:r><a:endParaRPr lang="en-US" sz="` + strconv.Itoa(fontSize) + `"/></a:p>`
}

func deckPPTXShapeXML(id int, element deckElement) string {
	geometry := "rect"
	if element.Shape == "ellipse" {
		geometry = "ellipse"
	}
	fill := `<a:noFill/>`
	if color, alpha, ok := deckPPTXColor(element.Fill, element.Opacity); ok {
		fill = `<a:solidFill>` + deckPPTXColorXML(color, alpha) + `</a:solidFill>`
	}
	line := `<a:ln><a:noFill/></a:ln>`
	if element.StrokeWidth > 0 {
		if color, alpha, ok := deckPPTXColor(element.Stroke, element.Opacity); ok {
			line = `<a:ln w="` + deckPPTXEMU(element.StrokeWidth) + `"><a:solidFill>` + deckPPTXColorXML(color, alpha) + `</a:solidFill></a:ln>`
		}
	}
	name := html.EscapeString(firstNonEmptyString(element.ID, fmt.Sprintf("Shape %d", id)))
	return `<p:sp><p:nvSpPr><p:cNvPr id="` + strconv.Itoa(id) + `" name="` + name + `"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr>` + deckPPTXTransformXML(element) + `<a:prstGeom prst="` + geometry + `"><a:avLst/></a:prstGeom>` + fill + line + `</p:spPr></p:sp>`
}

func deckPPTXPictureXML(id int, element deckElement, media deckPPTXMedia, relationID string) string {
	name := html.EscapeString(firstNonEmptyString(element.ID, element.Name, fmt.Sprintf("Image %d", id)))
	description := html.EscapeString(firstNonEmptyString(element.Prompt, element.Name, "Deck image"))
	position := element
	sourceRect := ""
	if media.Width > 0 && media.Height > 0 {
		frameRatio := element.Width / element.Height
		imageRatio := float64(media.Width) / float64(media.Height)
		if element.Fit == "contain" {
			if imageRatio > frameRatio {
				height := element.Width / imageRatio
				position.Y += (element.Height - height) / 2
				position.Height = height
			} else {
				width := element.Height * imageRatio
				position.X += (element.Width - width) / 2
				position.Width = width
			}
		} else if imageRatio > frameRatio {
			visible := frameRatio / imageRatio
			crop := int(math.Round((1 - visible) * 50000))
			sourceRect = `<a:srcRect l="` + strconv.Itoa(crop) + `" r="` + strconv.Itoa(crop) + `"/>`
		} else if imageRatio < frameRatio {
			visible := imageRatio / frameRatio
			crop := int(math.Round((1 - visible) * 50000))
			sourceRect = `<a:srcRect t="` + strconv.Itoa(crop) + `" b="` + strconv.Itoa(crop) + `"/>`
		}
	}
	alpha := ""
	if element.Opacity < 1 {
		alpha = `<a:alphaModFix amt="` + strconv.Itoa(int(math.Round(element.Opacity*100000))) + `"/>`
	}
	return `<p:pic><p:nvPicPr><p:cNvPr id="` + strconv.Itoa(id) + `" name="` + name + `" descr="` + description + `"/><p:cNvPicPr><a:picLocks noChangeAspect="1"/></p:cNvPicPr><p:nvPr/></p:nvPicPr><p:blipFill><a:blip r:embed="` + html.EscapeString(relationID) + `">` + alpha + `</a:blip>` + sourceRect + `<a:stretch><a:fillRect/></a:stretch></p:blipFill><p:spPr>` + deckPPTXTransformXML(position) + `<a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:ln><a:noFill/></a:ln></p:spPr></p:pic>`
}

func deckPPTXPresentationXML(slides int, hasNotes bool) string {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:presentation xmlns:a="` + deckPPTXDrawingMLNS + `" xmlns:r="` + deckPPTXOfficeDocumentNS + `" xmlns:p="` + deckPPTXPresentationMLNS + `"><p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst>`)
	next := 2
	if hasNotes {
		builder.WriteString(`<p:notesMasterIdLst><p:notesMasterId r:id="rId2"/></p:notesMasterIdLst>`)
		next++
	}
	builder.WriteString(`<p:sldIdLst>`)
	for index := 0; index < slides; index++ {
		builder.WriteString(`<p:sldId id="` + strconv.Itoa(256+index) + `" r:id="rId` + strconv.Itoa(next+index) + `"/>`)
	}
	builder.WriteString(`</p:sldIdLst><p:sldSz cx="` + strconv.Itoa(deckPPTXSlideWidthEMU) + `" cy="` + strconv.Itoa(deckPPTXSlideHeightEMU) + `" type="screen16x9"/><p:notesSz cx="6858000" cy="9144000"/><p:defaultTextStyle><a:defPPr><a:defRPr lang="en-US"/></a:defPPr></p:defaultTextStyle></p:presentation>`)
	return builder.String()
}

func deckPPTXPresentationRelationshipsXML(slides int, hasNotes bool) string {
	rels := []deckPPTXRelationship{{ID: "rId1", Type: deckPPTXOfficeDocumentNS + "/slideMaster", Target: "slideMasters/slideMaster1.xml"}}
	if hasNotes {
		rels = append(rels, deckPPTXRelationship{ID: "rId2", Type: deckPPTXOfficeDocumentNS + "/notesMaster", Target: "notesMasters/notesMaster1.xml"})
	}
	for index := 0; index < slides; index++ {
		rels = append(rels, deckPPTXRelationship{ID: fmt.Sprintf("rId%d", len(rels)+1), Type: deckPPTXOfficeDocumentNS + "/slide", Target: fmt.Sprintf("slides/slide%d.xml", index+1)})
	}
	return deckPPTXRelationshipsXML(rels)
}

func deckPPTXRelationshipsXML(rels []deckPPTXRelationship) string {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="` + deckPPTXRelationshipNS + `">`)
	for _, rel := range rels {
		builder.WriteString(`<Relationship Id="` + html.EscapeString(rel.ID) + `" Type="` + html.EscapeString(rel.Type) + `" Target="` + html.EscapeString(rel.Target) + `"/>`)
	}
	builder.WriteString(`</Relationships>`)
	return builder.String()
}

func deckPPTXContentTypesXML(slides int, noteSlides map[int]bool, media map[string]deckPPTXMedia) string {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="` + deckPPTXContentTypesNS + `"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/>`)
	overrides := []string{
		`<Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/>`,
		`<Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/>`,
		`<Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/>`,
		`<Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/>`,
		`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>`,
		`<Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>`,
	}
	if len(noteSlides) > 0 {
		overrides = append(overrides, `<Override PartName="/ppt/notesMasters/notesMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.notesMaster+xml"/>`)
	}
	for index := 1; index <= slides; index++ {
		overrides = append(overrides, `<Override PartName="/ppt/slides/slide`+strconv.Itoa(index)+`.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>`)
		if noteSlides[index] {
			overrides = append(overrides, `<Override PartName="/ppt/notesSlides/notesSlide`+strconv.Itoa(index)+`.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.notesSlide+xml"/>`)
		}
	}
	mediaParts := make([]deckPPTXMedia, 0, len(media))
	for _, item := range media {
		mediaParts = append(mediaParts, item)
	}
	sort.Slice(mediaParts, func(i, j int) bool { return mediaParts[i].Path < mediaParts[j].Path })
	for _, item := range mediaParts {
		overrides = append(overrides, `<Override PartName="/`+html.EscapeString(item.Path)+`" ContentType="`+html.EscapeString(item.Mime)+`"/>`)
	}
	sort.Strings(overrides)
	for _, override := range overrides {
		builder.WriteString(override)
	}
	builder.WriteString(`</Types>`)
	return builder.String()
}

func deckPPTXSlideMasterXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sldMaster xmlns:a="` + deckPPTXDrawingMLNS + `" xmlns:r="` + deckPPTXOfficeDocumentNS + `" xmlns:p="` + deckPPTXPresentationMLNS + `"><p:cSld name="Blank Master">` + deckPPTXShapeTreeStart() + `</p:spTree></p:cSld><p:clrMap accent1="4F81BD" accent2="C0504D" accent3="9BBB59" accent4="8064A2" accent5="4BACC6" accent6="F79646" bg1="lt1" bg2="lt2" folHlink="folHlink" hlink="hlink" tx1="dk1" tx2="dk2"/><p:sldLayoutIdLst><p:sldLayoutId id="1" r:id="rId1"/></p:sldLayoutIdLst><p:txStyles><p:titleStyle><a:lvl1pPr><a:defRPr sz="3200"/></a:lvl1pPr></p:titleStyle><p:bodyStyle><a:lvl1pPr><a:defRPr sz="1800"/></a:lvl1pPr></p:bodyStyle><p:otherStyle><a:defPPr><a:defRPr lang="en-US"/></a:defPPr></p:otherStyle></p:txStyles></p:sldMaster>`
}

func deckPPTXSlideLayoutXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sldLayout xmlns:a="` + deckPPTXDrawingMLNS + `" xmlns:r="` + deckPPTXOfficeDocumentNS + `" xmlns:p="` + deckPPTXPresentationMLNS + `" type="blank" preserve="1"><p:cSld name="Blank">` + deckPPTXShapeTreeStart() + `</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`
}

func deckPPTXNotesMasterXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:notesMaster xmlns:a="` + deckPPTXDrawingMLNS + `" xmlns:r="` + deckPPTXOfficeDocumentNS + `" xmlns:p="` + deckPPTXPresentationMLNS + `"><p:cSld name="Notes Master">` + deckPPTXShapeTreeStart() + `<p:sp><p:nvSpPr><p:cNvPr id="2" name="Notes Placeholder 1"/><p:cNvSpPr txBox="1"/><p:nvPr><p:ph type="body" idx="1"/></p:nvPr></p:nvSpPr><p:spPr><a:xfrm><a:off x="685800" y="4572000"/><a:ext cx="5486400" cy="3429000"/></a:xfrm></p:spPr><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:endParaRPr lang="en-US"/></a:p></p:txBody></p:sp></p:spTree></p:cSld><p:clrMap accent1="4F81BD" accent2="C0504D" accent3="9BBB59" accent4="8064A2" accent5="4BACC6" accent6="F79646" bg1="lt1" bg2="lt2" folHlink="folHlink" hlink="hlink" tx1="dk1" tx2="dk2"/><p:notesStyle><a:lvl1pPr><a:defRPr sz="1200"/></a:lvl1pPr></p:notesStyle></p:notesMaster>`
}

func deckPPTXNotesSlideXML(notes string) string {
	element := deckElement{Text: notes, FontSize: 16, FontFamily: "Arial", FontWeight: 400, Color: "#000000", TextAlign: "left", LineHeight: 1.2, Opacity: 1}
	var paragraphs strings.Builder
	for _, line := range strings.Split(strings.ReplaceAll(notes, "\r\n", "\n"), "\n") {
		paragraphs.WriteString(deckPPTXParagraphXML(line, element))
	}
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:notes xmlns:a="` + deckPPTXDrawingMLNS + `" xmlns:r="` + deckPPTXOfficeDocumentNS + `" xmlns:p="` + deckPPTXPresentationMLNS + `"><p:cSld>` + deckPPTXShapeTreeStart() + `<p:sp><p:nvSpPr><p:cNvPr id="2" name="Notes Placeholder 1"/><p:cNvSpPr txBox="1"/><p:nvPr><p:ph type="body" idx="1"/></p:nvPr></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/>` + paragraphs.String() + `</p:txBody></p:sp></p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:notes>`
}

func deckPPTXThemeXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><a:theme xmlns:a="` + deckPPTXDrawingMLNS + `" name="Bonfire Deck"><a:themeElements><a:clrScheme name="Bonfire"><a:dk1><a:srgbClr val="000000"/></a:dk1><a:lt1><a:srgbClr val="FFFFFF"/></a:lt1><a:dk2><a:srgbClr val="1F1F1F"/></a:dk2><a:lt2><a:srgbClr val="F3F3F3"/></a:lt2><a:accent1><a:srgbClr val="F15A24"/></a:accent1><a:accent2><a:srgbClr val="4F81BD"/></a:accent2><a:accent3><a:srgbClr val="9BBB59"/></a:accent3><a:accent4><a:srgbClr val="8064A2"/></a:accent4><a:accent5><a:srgbClr val="4BACC6"/></a:accent5><a:accent6><a:srgbClr val="F79646"/></a:accent6><a:hlink><a:srgbClr val="0000FF"/></a:hlink><a:folHlink><a:srgbClr val="800080"/></a:folHlink></a:clrScheme><a:fontScheme name="Bonfire"><a:majorFont><a:latin typeface="Arial"/><a:ea typeface=""/><a:cs typeface=""/></a:majorFont><a:minorFont><a:latin typeface="Arial"/><a:ea typeface=""/><a:cs typeface=""/></a:minorFont></a:fontScheme><a:fmtScheme name="Bonfire"><a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:fillStyleLst><a:lnStyleLst><a:ln w="6350"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln></a:lnStyleLst><a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst><a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:bgFillStyleLst></a:fmtScheme></a:themeElements></a:theme>`
}

func deckPPTXCoreXML(title string) string {
	now := "2000-01-01T00:00:00Z"
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><cp:coreProperties xmlns:cp="` + deckPPTXCorePropertiesNS + `" xmlns:dc="` + deckPPTXDublinCoreNS + `" xmlns:dcterms="` + deckPPTXDublinCoreTermsNS + `" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><dc:title>` + html.EscapeString(firstNonEmptyString(strings.TrimSpace(title), "Presentation")) + `</dc:title><dc:creator>STRIDE</dc:creator><cp:lastModifiedBy>STRIDE</cp:lastModifiedBy><dcterms:created xsi:type="dcterms:W3CDTF">` + now + `</dcterms:created><dcterms:modified xsi:type="dcterms:W3CDTF">` + now + `</dcterms:modified></cp:coreProperties>`
}

func deckPPTXAppXML(slides, notes int) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes"><Application>STRIDE</Application><PresentationFormat>Widescreen</PresentationFormat><Slides>` + strconv.Itoa(slides) + `</Slides><Notes>` + strconv.Itoa(notes) + `</Notes><HiddenSlides>0</HiddenSlides><ScaleCrop>false</ScaleCrop><AppVersion>1.0</AppVersion></Properties>`
}

func deckPPTXEMU(value float64) string {
	return strconv.FormatInt(int64(math.Round(value*deckPPTXEMUPerCanvasUnit)), 10)
}

func deckPPTXFontName(stack string) string {
	font := strings.TrimSpace(strings.Split(stack, ",")[0])
	font = strings.Trim(font, `"'`)
	if font == "" {
		return "Arial"
	}
	return font
}

func deckPPTXColor(value string, opacity float64) (string, int, bool) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || value == "transparent" {
		return "", 0, false
	}
	if value == "white" {
		value = "#ffffff"
	} else if value == "black" {
		value = "#000000"
	}
	value = strings.TrimPrefix(value, "#")
	if len(value) == 3 || len(value) == 4 {
		expanded := make([]byte, 0, len(value)*2)
		for index := range value {
			expanded = append(expanded, value[index], value[index])
		}
		value = string(expanded)
	}
	alpha := 1.0
	if len(value) == 8 {
		component, err := strconv.ParseUint(value[6:], 16, 8)
		if err != nil {
			return "", 0, false
		}
		alpha = float64(component) / 255
		value = value[:6]
	}
	if len(value) != 6 {
		return "", 0, false
	}
	if _, err := strconv.ParseUint(value, 16, 32); err != nil {
		return "", 0, false
	}
	alpha *= math.Max(0, math.Min(1, opacity))
	return strings.ToUpper(value), int(math.Round(alpha * 100000)), true
}

func deckPPTXColorXML(color string, alpha int) string {
	if alpha >= 100000 {
		return `<a:srgbClr val="` + color + `"/>`
	}
	return `<a:srgbClr val="` + color + `"><a:alpha val="` + strconv.Itoa(alpha) + `"/></a:srgbClr>`
}

func deckPPTXRichTextPlainText(fragment string) string {
	nodes, err := xhtml.ParseFragment(strings.NewReader(fragment), &xhtml.Node{Type: xhtml.ElementNode, Data: "div"})
	if err != nil {
		return ""
	}
	var builder strings.Builder
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.TextNode {
			builder.WriteString(node.Data)
		}
		if node.Type == xhtml.ElementNode && (node.Data == "br" || node.Data == "p" || node.Data == "div") && builder.Len() > 0 && !strings.HasSuffix(builder.String(), "\n") {
			builder.WriteByte('\n')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	for _, node := range nodes {
		walk(node)
	}
	return strings.TrimSpace(builder.String())
}

func deckPPTXImageExtension(imageMime string) (string, error) {
	switch imageMime {
	case "image/png":
		return "png", nil
	case "image/jpeg":
		return "jpg", nil
	case "image/gif":
		return "gif", nil
	case "image/webp":
		return "webp", nil
	default:
		return "", fmt.Errorf("PowerPoint image MIME %q is unsupported", imageMime)
	}
}

func deckPPTXImageDimensions(data []byte, imageMime string) (int, int, error) {
	if imageMime != "image/webp" {
		config, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width < 1 || config.Height < 1 {
			return 0, 0, fmt.Errorf("invalid %s image", imageMime)
		}
		return config.Width, config.Height, nil
	}
	return deckPPTXWebPDimensions(data)
}

func deckPPTXWebPDimensions(data []byte) (int, int, error) {
	if len(data) < 30 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, 0, fmt.Errorf("invalid image/webp image")
	}
	switch string(data[12:16]) {
	case "VP8X":
		width := int(data[24]) | int(data[25])<<8 | int(data[26])<<16
		height := int(data[27]) | int(data[28])<<8 | int(data[29])<<16
		return width + 1, height + 1, nil
	case "VP8 ":
		if len(data) < 30 || data[23] != 0x9d || data[24] != 0x01 || data[25] != 0x2a {
			return 0, 0, fmt.Errorf("invalid lossy image/webp image")
		}
		return int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff), int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff), nil
	case "VP8L":
		if len(data) < 25 || data[20] != 0x2f {
			return 0, 0, fmt.Errorf("invalid lossless image/webp image")
		}
		bits := binary.LittleEndian.Uint32(data[21:25])
		return int(bits&0x3fff) + 1, int((bits>>14)&0x3fff) + 1, nil
	default:
		return 0, 0, fmt.Errorf("unsupported image/webp encoding")
	}
}
