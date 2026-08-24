package main

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"
)

const (
	packagingGeneratedSceneSafeZone = 96.0
	packagingGeneratedSceneTextGap  = 24.0
	packagingGeneratedSceneEpsilon  = 0.01
)

// packagingGeneratedScenePreflightRequired keeps the stricter production
// contract on Packaging Studio's generated v5+ candidates. Deck Studio remains
// deliberately permissive so a human can move elements beyond the canvas while
// editing and recover them later.
func packagingGeneratedScenePreflightRequired(plan *goalPlan) bool {
	return plan != nil && strings.EqualFold(strings.TrimSpace(plan.ProcessID), packagingStudioProcessID) &&
		(plan.ProcessVersion >= 5 || strings.EqualFold(strings.TrimSpace(plan.ProcessImplementationRevision), "packaging_studio.runtime.v5"))
}

type packagingGeneratedSceneSource struct {
	SlideOrder     []string
	SlideKind      map[string]string
	SlideElements  map[string][]string
	ElementSlide   map[string]string
	OverlapAllowed map[string]bool
	SafeZoneExempt map[string]bool
	VisuallyHidden map[string]bool
	Identity       map[string]string
	ImageFig       map[string]string
	ImageCrop      map[string]string
	ImageFocalX    map[string]string
	ImageFocalY    map[string]string
	ImageFit       map[string]string
	ImagePosition  map[string]string
	UnsafeTextTree map[string]bool
	TextLines      map[string][]string
}

type packagingGeneratedSceneBounds struct {
	MinX float64
	MinY float64
	MaxX float64
	MaxY float64
}

// validatePackagingGeneratedScene is the deterministic gate between authored
// HTML and filing/render. It checks only facts the 1920x1080 source scene can
// prove; rendered jury review remains responsible for glyph wrapping, crop,
// contrast, and presentation-distance judgment.
func validatePackagingGeneratedScene(app *kanbanBoardApp, plan *goalPlan, sourceHTML string, assets []artifactAsset) error {
	source, err := parsePackagingGeneratedSceneSource(sourceHTML)
	if err != nil {
		return err
	}
	if packagingStudioPremiumDesignContract(plan) {
		if err := validatePackagingGeneratedPremiumStyles(sourceHTML); err != nil {
			return err
		}
		if err := validatePackagingGeneratedPremiumDOM(sourceHTML); err != nil {
			return err
		}
	}
	assetsJSON, err := json.Marshal(assets)
	if err != nil {
		return fmt.Errorf("encode generated deck assets: %w", err)
	}
	artifact := meetingMemoryEntry{Text: sourceHTML, Metadata: map[string]string{
		"type": artifactTypeHTMLDeck, artifactAssetsMetadataKey: string(assetsJSON),
	}}
	deck, imported, quality, err := loadDeckDocument(artifact)
	if err != nil {
		return fmt.Errorf("import generated deck scene: %w", err)
	}
	if !imported || quality != "faithful" {
		return fmt.Errorf("generated deck scene is not a faithful native import")
	}
	if err := validatePackagingGeneratedSceneMapping(deck, source); err != nil {
		return err
	}
	if err := validatePackagingGeneratedSceneGeometry(deck, source); err != nil {
		return err
	}
	if err := validatePackagingGeneratedSceneLockedCopy(app, plan, deck); err != nil {
		return err
	}
	if err := validatePackagingGeneratedSceneLockedLayout(app, plan, deck, source); err != nil {
		return err
	}
	return nil
}

func parsePackagingGeneratedSceneSource(sourceHTML string) (packagingGeneratedSceneSource, error) {
	doc, err := xhtml.Parse(strings.NewReader(sourceHTML))
	if err != nil {
		return packagingGeneratedSceneSource{}, fmt.Errorf("parse generated deck HTML: %w", err)
	}
	var stages []*xhtml.Node
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && legacyNodeAttr(node, "id") == "stage" {
			stages = append(stages, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if len(stages) != 1 {
		return packagingGeneratedSceneSource{}, fmt.Errorf("generated deck must contain exactly one #stage; found %d", len(stages))
	}

	source := packagingGeneratedSceneSource{
		SlideKind: make(map[string]string), SlideElements: make(map[string][]string), ElementSlide: make(map[string]string), OverlapAllowed: make(map[string]bool), SafeZoneExempt: make(map[string]bool), VisuallyHidden: make(map[string]bool),
		Identity: make(map[string]string), ImageFig: make(map[string]string), ImageCrop: make(map[string]string), ImageFocalX: make(map[string]string), ImageFocalY: make(map[string]string), ImageFit: make(map[string]string), ImagePosition: make(map[string]string), UnsafeTextTree: make(map[string]bool),
		TextLines: make(map[string][]string),
	}
	for _, key := range []string{"candidate", "strategy", "system", "palette", "type", "spacing", "grid", "motif", "image-treatment", "data-viz", "refusals"} {
		source.Identity[key] = strings.TrimSpace(legacyNodeAttr(stages[0], "data-deck-identity-"+key))
	}
	seenSlides := map[string]struct{}{}
	for node := stages[0].FirstChild; node != nil; node = node.NextSibling {
		if node.Type != xhtml.ElementNode {
			continue
		}
		if !strings.EqualFold(node.Data, "section") || !legacyNodeHasClass(node, "pg") {
			return packagingGeneratedSceneSource{}, fmt.Errorf("#stage may contain only section.pg slide children")
		}
		slideID := strings.TrimSpace(legacyNodeAttr(node, "data-deck-slide"))
		if slideID == "" {
			return packagingGeneratedSceneSource{}, fmt.Errorf("generated slide %d is missing data-deck-slide", len(source.SlideOrder)+1)
		}
		if !deckIdentifierPattern.MatchString(slideID) {
			return packagingGeneratedSceneSource{}, fmt.Errorf("generated slide mapping %q is invalid", slideID)
		}
		if _, duplicate := seenSlides[slideID]; duplicate {
			return packagingGeneratedSceneSource{}, fmt.Errorf("generated slide mapping %q is duplicated", slideID)
		}
		seenSlides[slideID] = struct{}{}
		source.SlideOrder = append(source.SlideOrder, slideID)
		source.SlideKind[slideID] = strings.ToLower(strings.TrimSpace(legacyNodeAttr(node, "data-deck-slide-kind")))

		var elementWalk func(*xhtml.Node)
		elementWalk = func(elementNode *xhtml.Node) {
			if elementNode.Type == xhtml.ElementNode {
				elementID := strings.TrimSpace(legacyNodeAttr(elementNode, "data-deck-element"))
				if elementID != "" {
					if !deckIdentifierPattern.MatchString(elementID) {
						err = fmt.Errorf("generated element mapping %q is invalid", elementID)
						return
					}
					if previous, duplicate := source.ElementSlide[elementID]; duplicate {
						err = fmt.Errorf("generated element mapping %q is duplicated on %q and %q", elementID, previous, slideID)
						return
					}
					typ := strings.ToLower(strings.TrimSpace(legacyNodeAttr(elementNode, "data-deck-type")))
					if !oneOf(typ, "text", "image", "shape") {
						err = fmt.Errorf("generated element %q has no valid data-deck-type", elementID)
						return
					}
					source.ElementSlide[elementID] = slideID
					source.SlideElements[slideID] = append(source.SlideElements[slideID], elementID)
					source.OverlapAllowed[elementID] = strings.EqualFold(strings.TrimSpace(legacyNodeAttr(elementNode, "data-deck-overlap")), "allow")
					styles := legacyStyleMap(elementNode)
					if typ == "image" {
						source.ImageFig[elementID] = strings.TrimSpace(legacyNodeAttr(elementNode, "data-deck-fig"))
						source.ImageCrop[elementID] = strings.TrimSpace(legacyNodeAttr(elementNode, "data-deck-crop"))
						source.ImageFocalX[elementID] = strings.TrimSpace(legacyNodeAttr(elementNode, "data-deck-focal-x"))
						source.ImageFocalY[elementID] = strings.TrimSpace(legacyNodeAttr(elementNode, "data-deck-focal-y"))
						source.ImageFit[elementID] = strings.TrimSpace(styles["object-fit"])
						source.ImagePosition[elementID] = strings.TrimSpace(styles["object-position"])
					} else if typ == "text" {
						source.UnsafeTextTree[elementID] = packagingGeneratedTextTreeHasVisualOverrides(elementNode)
						source.TextLines[elementID] = packagingGeneratedTextTreeLines(elementNode)
					}
					source.VisuallyHidden[elementID] = packagingGeneratedNodeHasAttribute(elementNode, "hidden") ||
						strings.EqualFold(strings.TrimSpace(styles["display"]), "none") ||
						strings.EqualFold(strings.TrimSpace(styles["visibility"]), "hidden")
					furniture := strings.ToLower(strings.TrimSpace(legacyNodeAttr(elementNode, "data-deck-furniture")))
					if furniture != "" {
						if typ != "text" || !oneOf(furniture, "background", "full-bleed") || !strings.EqualFold(strings.TrimSpace(legacyNodeAttr(elementNode, "aria-hidden")), "true") {
							err = fmt.Errorf("generated element %q has an invalid background-furniture exception", elementID)
							return
						}
						source.SafeZoneExempt[elementID] = true
					}
				}
			}
			for child := elementNode.FirstChild; child != nil && err == nil; child = child.NextSibling {
				elementWalk(child)
			}
		}
		for child := node.FirstChild; child != nil && err == nil; child = child.NextSibling {
			elementWalk(child)
		}
		if err != nil {
			return packagingGeneratedSceneSource{}, err
		}
		if len(source.SlideElements[slideID]) == 0 {
			return packagingGeneratedSceneSource{}, fmt.Errorf("generated slide %q has no mapped elements", slideID)
		}
	}
	if len(source.SlideOrder) == 0 {
		return packagingGeneratedSceneSource{}, fmt.Errorf("generated deck has no mapped slides")
	}
	return source, nil
}

// Premium scenes deliberately keep every authored aesthetic on the exact
// mapped node's inline contract. An arbitrary stylesheet can otherwise defeat
// a perfectly valid native lock after import (for example, an !important rule
// that hides all text, a body opacity rule, or a stage transform). The verbatim
// invariant chassis is the only stylesheet; generated pixels are materialized
// by the server on the exact locked .ph node rather than through the cascade.
func validatePackagingGeneratedPremiumStyles(sourceHTML string) error {
	doc, err := xhtml.Parse(strings.NewReader(sourceHTML))
	if err != nil {
		return fmt.Errorf("parse premium generated deck styles: %w", err)
	}
	chassisCount := 0
	var validationErr error
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node == nil || validationErr != nil {
			return
		}
		if node.Type == xhtml.ElementNode {
			tag := strings.ToLower(strings.TrimSpace(node.Data))
			if tag == "link" && strings.EqualFold(strings.TrimSpace(legacyNodeAttr(node, "rel")), "stylesheet") {
				validationErr = fmt.Errorf("premium generated scene may not load an author stylesheet")
				return
			}
			if tag == "style" {
				css := strings.TrimSpace(legacyNodeTextPreservingWhitespace(node))
				if packagingGeneratedCanonicalCSS(css) == packagingGeneratedCanonicalCSS(packagingDeckChassisCSS) && len(node.Attr) == 0 {
					chassisCount++
					if chassisCount > 1 {
						validationErr = fmt.Errorf("premium generated scene repeats the invariant deck chassis")
					}
					return
				}
				validationErr = fmt.Errorf("premium generated scene contains an author stylesheet outside the locked inline scene (id=%q css=%q)", strings.TrimSpace(legacyNodeAttr(node, "id")), trimForStorage(css, 96))
				return
			}
			if oneOf(tag, "html", "body") && strings.TrimSpace(legacyNodeAttr(node, "style")) != "" {
				validationErr = fmt.Errorf("premium generated scene styles %s outside the locked inline scene", tag)
				return
			}
			if legacyNodeAttr(node, "id") == "stage" && strings.TrimSpace(legacyNodeAttr(node, "style")) != "" {
				validationErr = fmt.Errorf("premium generated scene styles #stage outside the locked inline scene")
				return
			}
			if rawStyle := strings.TrimSpace(legacyNodeAttr(node, "style")); rawStyle != "" {
				if err := validatePackagingGeneratedPremiumInlineStyle(node, rawStyle); err != nil {
					validationErr = err
					return
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if validationErr == nil && chassisCount != 1 {
		return fmt.Errorf("premium generated scene must contain exactly one verbatim invariant deck chassis")
	}
	return validationErr
}

func packagingGeneratedCanonicalCSS(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

// validatePackagingGeneratedPremiumDOM closes the raw-HTML/native-scene
// ownership boundary. Premium output has no anonymous visual wrappers or
// presenter chrome: body owns one #stage, stage owns exact slides, and each
// slide owns only mapped nodes plus one optional hidden notes record. That
// prevents chassis/reserved-class collisions and hidden ancestors from making
// the browser/PDF disagree with the same locked native scene.
func validatePackagingGeneratedPremiumDOM(sourceHTML string) error {
	if !regexp.MustCompile(`(?is)^\s*<!doctype\s+html\s*>`).MatchString(sourceHTML) {
		return fmt.Errorf("premium generated deck must begin with one HTML doctype")
	}
	doc, err := xhtml.Parse(strings.NewReader(sourceHTML))
	if err != nil {
		return fmt.Errorf("parse premium generated deck DOM: %w", err)
	}
	var doctypes, htmls, heads, bodies, stages []*xhtml.Node
	var collect func(*xhtml.Node)
	collect = func(node *xhtml.Node) {
		if node.Type == xhtml.DoctypeNode {
			doctypes = append(doctypes, node)
		}
		if node.Type == xhtml.ElementNode {
			if strings.EqualFold(node.Data, "html") {
				htmls = append(htmls, node)
			}
			if strings.EqualFold(node.Data, "head") {
				heads = append(heads, node)
			}
			if strings.EqualFold(node.Data, "body") {
				bodies = append(bodies, node)
			}
			if legacyNodeAttr(node, "id") == "stage" {
				stages = append(stages, node)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	collect(doc)
	if len(doctypes) != 1 || !strings.EqualFold(strings.TrimSpace(doctypes[0].Data), "html") || len(htmls) != 1 || len(heads) != 1 || len(bodies) != 1 || heads[0].Parent != htmls[0] || bodies[0].Parent != htmls[0] || len(stages) != 1 || stages[0].Parent != bodies[0] {
		return fmt.Errorf("premium generated deck must use one exact inert html/head/body shell")
	}
	htmlNode, head := htmls[0], heads[0]
	if len(htmlNode.Attr) != 0 || len(head.Attr) != 0 {
		return fmt.Errorf("premium generated deck html and head may not carry attributes")
	}
	for child := htmlNode.FirstChild; child != nil; child = child.NextSibling {
		if child == head || child == bodies[0] || child.Type == xhtml.CommentNode || (child.Type == xhtml.TextNode && strings.TrimSpace(child.Data) == "") {
			continue
		}
		return fmt.Errorf("premium generated html may contain only head and body")
	}
	metaCharset, title, chassis := 0, 0, 0
	for child := head.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.CommentNode || (child.Type == xhtml.TextNode && strings.TrimSpace(child.Data) == "") {
			continue
		}
		if child.Type != xhtml.ElementNode {
			return fmt.Errorf("premium generated head contains unowned content")
		}
		switch strings.ToLower(strings.TrimSpace(child.Data)) {
		case "meta":
			if len(child.Attr) != 1 || !strings.EqualFold(strings.TrimSpace(child.Attr[0].Key), "charset") || !strings.EqualFold(strings.TrimSpace(child.Attr[0].Val), "utf-8") {
				return fmt.Errorf("premium generated head permits only an optional utf-8 charset meta")
			}
			metaCharset++
		case "title":
			if len(child.Attr) != 0 || packagingGeneratedHasElementChild(child) {
				return fmt.Errorf("premium generated title must be plain inert text")
			}
			title++
		case "style":
			if len(child.Attr) != 0 || packagingGeneratedCanonicalCSS(legacyNodeTextPreservingWhitespace(child)) != packagingGeneratedCanonicalCSS(packagingDeckChassisCSS) {
				return fmt.Errorf("premium generated head style must be the invariant deck chassis")
			}
			chassis++
		default:
			return fmt.Errorf("premium generated head may contain only charset, title, and the invariant chassis; found %s", child.Data)
		}
	}
	if metaCharset > 1 || title > 1 || chassis != 1 {
		return fmt.Errorf("premium generated head must carry one chassis and at most one inert charset and title")
	}
	if len(bodies) != 1 || len(stages) != 1 || stages[0].Parent != bodies[0] {
		return fmt.Errorf("premium generated deck body must own exactly one direct #stage")
	}
	body, stage := bodies[0], stages[0]
	if len(body.Attr) != 0 {
		return fmt.Errorf("premium generated deck body may not carry visual or visibility attributes")
	}
	for child := body.FirstChild; child != nil; child = child.NextSibling {
		if child == stage || child.Type == xhtml.CommentNode || (child.Type == xhtml.TextNode && strings.TrimSpace(child.Data) == "") {
			continue
		}
		return fmt.Errorf("premium generated deck body may contain only #stage")
	}
	allowedStageAttrs := map[string]bool{"id": true}
	for _, key := range []string{"candidate", "strategy", "system", "palette", "type", "spacing", "grid", "motif", "image-treatment", "data-viz", "refusals"} {
		allowedStageAttrs["data-deck-identity-"+key] = true
	}
	if err := packagingGeneratedExactAttributeKeys(stage, allowedStageAttrs, "#stage"); err != nil || len(stage.Attr) != len(allowedStageAttrs) {
		return fmt.Errorf("premium generated #stage must carry only its complete identity contract")
	}

	slideIndex := 0
	for child := stage.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.CommentNode || (child.Type == xhtml.TextNode && strings.TrimSpace(child.Data) == "") {
			continue
		}
		if child.Type != xhtml.ElementNode || !strings.EqualFold(child.Data, "section") {
			return fmt.Errorf("premium generated #stage may contain only exact slide sections")
		}
		if err := validatePackagingGeneratedPremiumSlideDOM(child, slideIndex); err != nil {
			return err
		}
		slideIndex++
	}
	if slideIndex == 0 {
		return fmt.Errorf("premium generated #stage has no slides")
	}
	return nil
}

func validatePackagingGeneratedPremiumSlideDOM(slide *xhtml.Node, slideIndex int) error {
	wantClasses := map[string]bool{"pg": true}
	if slideIndex == 0 {
		wantClasses["on"] = true
	}
	if !packagingGeneratedExactClassSet(slide, wantClasses) {
		return fmt.Errorf("premium generated slide %d has invalid chassis classes", slideIndex+1)
	}
	allowed := map[string]bool{"class": true, "data-deck-slide": true, "style": true, "data-deck-type": true, "data-deck-slide-kind": true}
	if err := packagingGeneratedExactAttributeKeys(slide, allowed, "slide"); err != nil {
		return fmt.Errorf("premium generated slide %d has attributes outside its exact contract", slideIndex+1)
	}
	typeValue := strings.ToLower(strings.TrimSpace(legacyNodeAttr(slide, "data-deck-type")))
	kindValue := strings.ToLower(strings.TrimSpace(legacyNodeAttr(slide, "data-deck-slide-kind")))
	if (typeValue == "") != (kindValue == "") || (typeValue != "" && (typeValue != "slide" || !oneOf(kindValue, "cover", "normal", "evidence", "close") || len(slide.Attr) != 5)) || (typeValue == "" && len(slide.Attr) != 3) {
		return fmt.Errorf("premium generated slide %d has invalid optional slide metadata", slideIndex+1)
	}
	notes := 0
	for child := slide.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.CommentNode || (child.Type == xhtml.TextNode && strings.TrimSpace(child.Data) == "") {
			continue
		}
		if child.Type != xhtml.ElementNode {
			return fmt.Errorf("premium generated slide %d has unowned content", slideIndex+1)
		}
		if packagingGeneratedPremiumNotesNode(child) {
			notes++
			if notes > 1 {
				return fmt.Errorf("premium generated slide %d repeats presenter notes", slideIndex+1)
			}
			continue
		}
		if strings.TrimSpace(legacyNodeAttr(child, "data-deck-element")) == "" {
			return fmt.Errorf("premium generated slide %d contains an unowned %s node", slideIndex+1, child.Data)
		}
		if err := validatePackagingGeneratedPremiumElementDOM(child); err != nil {
			return err
		}
	}
	return nil
}

func validatePackagingGeneratedPremiumElementDOM(node *xhtml.Node) error {
	typ := strings.ToLower(strings.TrimSpace(legacyNodeAttr(node, "data-deck-type")))
	common := map[string]bool{"data-deck-element": true, "data-deck-type": true, "style": true, "data-deck-overlap": true}
	if packagingGeneratedNodeHasAttribute(node, "hidden") || packagingGeneratedNodeHasAttribute(node, "inert") {
		return fmt.Errorf("premium generated mapped %s may not be hidden or inert", typ)
	}
	switch typ {
	case "text":
		if !strings.EqualFold(node.Data, "div") {
			return fmt.Errorf("premium generated text must be a direct div")
		}
		common["data-deck-furniture"] = true
		common["aria-hidden"] = true
		if err := packagingGeneratedExactAttributeKeys(node, common, "text"); err != nil {
			return err
		}
		if strings.TrimSpace(legacyNodeAttr(node, "class")) != "" {
			return fmt.Errorf("premium generated text may not carry reserved or authored classes")
		}
		furniture := strings.TrimSpace(legacyNodeAttr(node, "data-deck-furniture"))
		ariaHidden := strings.TrimSpace(legacyNodeAttr(node, "aria-hidden"))
		if (furniture == "") != (ariaHidden == "") || (furniture != "" && (!oneOf(furniture, "background", "full-bleed") || !strings.EqualFold(ariaHidden, "true"))) {
			return fmt.Errorf("premium generated text has invalid background-furniture visibility")
		}
		if packagingGeneratedTextTreeHasVisualOverrides(node) {
			return fmt.Errorf("premium generated text contains non-structural descendants")
		}
	case "shape":
		if !strings.EqualFold(node.Data, "div") {
			return fmt.Errorf("premium generated shape must be a direct div")
		}
		common["data-deck-shape"] = true
		if err := packagingGeneratedExactAttributeKeys(node, common, "shape"); err != nil {
			return err
		}
		if strings.TrimSpace(legacyNodeAttr(node, "class")) != "" || strings.TrimSpace(legacyNodeTextPreservingWhitespace(node)) != "" || packagingGeneratedHasElementChild(node) {
			return fmt.Errorf("premium generated shape must be an empty classless mapped div")
		}
	case "image":
		if !strings.EqualFold(node.Data, "figure") {
			return fmt.Errorf("premium generated image must be a direct figure")
		}
		for _, key := range []string{"class", "data-deck-fig", "data-deck-crop", "data-deck-focal-x", "data-deck-focal-y"} {
			common[key] = true
		}
		if err := packagingGeneratedExactAttributeKeys(node, common, "image"); err != nil {
			return err
		}
		fig := strings.TrimSpace(legacyNodeAttr(node, "data-deck-fig"))
		figNumber, err := strconv.Atoi(fig)
		if err != nil || figNumber < 1 {
			return fmt.Errorf("premium generated image has an invalid FIG identity")
		}
		classes := map[string]bool{"image-plate": true, "fig-" + fig: true}
		if legacyNodeHasClass(node, "bleed") {
			classes["bleed"] = true
		}
		if !packagingGeneratedExactClassSet(node, classes) {
			return fmt.Errorf("premium generated image has classes outside its exact FIG contract")
		}
		placeholders := 0
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == xhtml.CommentNode || (child.Type == xhtml.TextNode && strings.TrimSpace(child.Data) == "") {
				continue
			}
			if child.Type != xhtml.ElementNode || !strings.EqualFold(child.Data, "div") || !packagingGeneratedExactClassSet(child, map[string]bool{"ph": true}) || !packagingGeneratedInlineImagePattern.MatchString(strings.TrimSpace(legacyNodeAttr(child, "style"))) || len(child.Attr) != 2 || packagingGeneratedHasElementChild(child) || strings.TrimSpace(legacyNodeTextPreservingWhitespace(child)) != "" {
				return fmt.Errorf("premium generated image must contain one exact server-owned pixel node")
			}
			placeholders++
		}
		if placeholders != 1 {
			return fmt.Errorf("premium generated image must contain one exact server-owned pixel node")
		}
	default:
		return fmt.Errorf("premium generated mapped element has invalid type %q", typ)
	}
	return nil
}

func packagingGeneratedPremiumNotesNode(node *xhtml.Node) bool {
	if node == nil || node.Type != xhtml.ElementNode || !strings.EqualFold(node.Data, "div") || !packagingGeneratedExactClassSet(node, map[string]bool{"notes": true}) || !packagingGeneratedNodeHasAttribute(node, "hidden") || len(node.Attr) != 2 || packagingGeneratedHasElementChild(node) {
		return false
	}
	return true
}

func packagingGeneratedExactAttributeKeys(node *xhtml.Node, allowed map[string]bool, label string) error {
	seen := map[string]bool{}
	for _, attribute := range node.Attr {
		key := strings.ToLower(strings.TrimSpace(attribute.Key))
		if !allowed[key] || seen[key] || strings.HasPrefix(key, "on") {
			return fmt.Errorf("premium generated %s has unowned attribute %q", label, key)
		}
		seen[key] = true
	}
	return nil
}

func packagingGeneratedExactClassSet(node *xhtml.Node, want map[string]bool) bool {
	classes := strings.Fields(strings.TrimSpace(legacyNodeAttr(node, "class")))
	if len(classes) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, className := range classes {
		if !want[className] || seen[className] {
			return false
		}
		seen[className] = true
	}
	return true
}

func packagingGeneratedHasElementChild(node *xhtml.Node) bool {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.ElementNode {
			return true
		}
	}
	return false
}

func validatePackagingGeneratedPremiumInlineStyle(node *xhtml.Node, raw string) error {
	tag := strings.ToLower(strings.TrimSpace(node.Data))
	elementType := strings.ToLower(strings.TrimSpace(legacyNodeAttr(node, "data-deck-type")))
	if tag == "div" && legacyNodeHasClass(node, "ph") && packagingGeneratedInlineImagePattern.MatchString(raw) {
		return nil
	}
	allowed := map[string]bool{}
	required := map[string]bool{}
	if tag == "section" && legacyNodeHasClass(node, "pg") && node.Parent != nil && legacyNodeAttr(node.Parent, "id") == "stage" {
		allowed["background"] = true
		allowed["background-color"] = true
	} else if strings.TrimSpace(legacyNodeAttr(node, "data-deck-element")) != "" && oneOf(elementType, "text", "image", "shape") {
		for _, key := range []string{"position", "left", "top", "width", "height", "z-index", "opacity", "transform"} {
			allowed[key] = true
			required[key] = true
		}
		switch elementType {
		case "text":
			for _, key := range []string{"font-size", "font-family", "font-weight", "color", "text-align", "line-height", "letter-spacing"} {
				allowed[key] = true
				required[key] = true
			}
		case "image":
			allowed["margin"] = true
			allowed["object-fit"] = true
			allowed["object-position"] = true
			required["margin"] = true
			required["object-fit"] = true
			required["object-position"] = true
		case "shape":
			for _, key := range []string{"background", "background-color", "border", "border-radius"} {
				allowed[key] = true
			}
		}
	} else {
		return fmt.Errorf("premium generated scene has inline style on an unowned %s node", tag)
	}

	seen := map[string]bool{}
	for _, declaration := range strings.Split(raw, ";") {
		declaration = strings.TrimSpace(declaration)
		if declaration == "" {
			continue
		}
		parts := strings.SplitN(declaration, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("premium generated scene has a malformed inline declaration on %s", tag)
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		lowerValue := strings.ToLower(value)
		if key == "" || value == "" || seen[key] || !allowed[key] || strings.Contains(lowerValue, "!important") || strings.Contains(lowerValue, "url(") || strings.Contains(lowerValue, "var(") || strings.Contains(lowerValue, "calc(") || strings.Contains(lowerValue, "expression") || strings.ContainsAny(value, "{}@") {
			return fmt.Errorf("premium generated scene has unowned inline property %q on %s", key, tag)
		}
		seen[key] = true
	}
	if len(seen) == 0 {
		return fmt.Errorf("premium generated scene has an empty inline style on %s", tag)
	}
	styles := legacyStyleMap(node)
	if tag == "section" {
		if len(seen) != 1 || (!seen["background"] && !seen["background-color"]) {
			return fmt.Errorf("premium generated slide must carry exactly one inline background color")
		}
		value := firstNonEmptyString(styles["background"], styles["background-color"])
		if !deckHexColorPattern.MatchString(value) {
			return fmt.Errorf("premium generated slide background must be one exact hex color")
		}
		return nil
	}
	for key := range required {
		if !seen[key] {
			return fmt.Errorf("premium generated %s is missing required inline property %q", elementType, key)
		}
	}
	if styles["position"] != "absolute" {
		return fmt.Errorf("premium generated %s position must be absolute", elementType)
	}
	for _, key := range []string{"left", "top", "width", "height"} {
		value, ok := packagingGeneratedCSSNumber(styles[key], "px")
		if !ok || ((key == "width" || key == "height") && value <= 0) {
			return fmt.Errorf("premium generated %s %s must be a finite pixel value", elementType, key)
		}
	}
	z, err := strconv.Atoi(styles["z-index"])
	if err != nil || z < 0 || z > 10000 {
		return fmt.Errorf("premium generated %s z-index must be an integer from 0 to 10000", elementType)
	}
	opacity, ok := packagingGeneratedCSSNumber(styles["opacity"], "")
	if !ok || opacity <= 0 || opacity > 1 {
		return fmt.Errorf("premium generated %s opacity must be greater than zero and at most one", elementType)
	}
	if !packagingGeneratedRotatePattern.MatchString(styles["transform"]) {
		return fmt.Errorf("premium generated %s transform must be exactly one finite rotate(deg)", elementType)
	}

	switch elementType {
	case "text":
		size, sizeOK := packagingGeneratedCSSNumber(styles["font-size"], "px")
		if !sizeOK || size <= 0 {
			return fmt.Errorf("premium generated text font-size must be a positive pixel value")
		}
		weight, weightErr := strconv.Atoi(styles["font-weight"])
		if weightErr != nil || weight < 100 || weight > 900 || weight%100 != 0 {
			return fmt.Errorf("premium generated text font-weight must be a numeric 100-step from 100 to 900")
		}
		if !deckHexColorPattern.MatchString(styles["color"]) || !oneOf(styles["text-align"], "left", "center", "right") {
			return fmt.Errorf("premium generated text color or alignment is invalid")
		}
		if _, ok := packagingGeneratedNormalizedLineHeight(styles["line-height"], size); !ok {
			return fmt.Errorf("premium generated text line-height must be a unitless .8 to 2 ratio or equivalent pixel leading")
		}
		if _, ok := packagingStudioTrackingPixels(styles["letter-spacing"], 1); !ok {
			return fmt.Errorf("premium generated text letter-spacing is outside the closed tracking range")
		}
	case "image":
		if styles["margin"] != "0" {
			return fmt.Errorf("premium generated image margin must be exactly zero")
		}
		if !oneOf(styles["object-fit"], "cover", "contain") {
			return fmt.Errorf("premium generated image object-fit must be cover or contain")
		}
		if _, _, err := packagingStudioObjectPosition(styles["object-position"]); err != nil {
			return fmt.Errorf("premium generated image object-position is invalid")
		}
	case "shape":
		fills := 0
		for _, key := range []string{"background", "background-color"} {
			if seen[key] {
				fills++
				if !deckHexColorPattern.MatchString(styles[key]) {
					return fmt.Errorf("premium generated shape fill must be one exact hex color")
				}
			}
		}
		if fills != 1 {
			return fmt.Errorf("premium generated shape must carry exactly one inline fill color")
		}
		if radius := styles["border-radius"]; radius != "" && radius != "50%" {
			return fmt.Errorf("premium generated shape border-radius must be exactly 50 percent when present")
		}
		if border := styles["border"]; border != "" && !packagingGeneratedBorderPattern.MatchString(border) {
			return fmt.Errorf("premium generated shape border must be an exact pixel solid hex stroke")
		}
	}
	return nil
}

func packagingGeneratedNormalizedLineHeight(value string, fontSize float64) (float64, bool) {
	value = strings.TrimSpace(value)
	if fontSize <= 0 || value == "" {
		return 0, false
	}
	lineHeight, ok := packagingGeneratedCSSNumber(value, "")
	if !ok {
		lineHeightPixels, pixelsOK := packagingGeneratedCSSNumber(value, "px")
		if !pixelsOK {
			return 0, false
		}
		lineHeight = lineHeightPixels / fontSize
	}
	return lineHeight, lineHeight >= .8 && lineHeight <= 2
}

func packagingGeneratedNormalizedLayoutLineHeight(value, fontSize float64) (float64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) || math.IsNaN(fontSize) || math.IsInf(fontSize, 0) || fontSize <= 0 {
		return 0, false
	}
	if value > 2 {
		value /= fontSize
	}
	return value, value >= .8 && value <= 2
}

func packagingGeneratedCSSNumber(value, suffix string) (float64, bool) {
	value = strings.TrimSpace(value)
	if suffix != "" {
		if !strings.HasSuffix(value, suffix) {
			return 0, false
		}
		value = strings.TrimSuffix(value, suffix)
	}
	if value == "" {
		return 0, false
	}
	number, err := strconv.ParseFloat(value, 64)
	return number, err == nil && !math.IsNaN(number) && !math.IsInf(number, 0)
}

var (
	packagingGeneratedImagerySelectorPattern = regexp.MustCompile(`^\.fig-[1-9][0-9]*\[data-deck-crop="(?:center|top|bottom|left|right|faces|safe_area)"\] \.ph$`)
	packagingGeneratedImageryBodyPattern     = regexp.MustCompile(`^position:absolute!important;inset:0!important;width:100%!important;height:100%!important;display:block!important;visibility:visible!important;opacity:1!important;background-image:url\(data:image/[a-zA-Z0-9.+-]+;base64,[A-Za-z0-9+/=]+\)!important;background-size:(?:cover|contain)!important;background-position:-?(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)% -?(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)%!important;background-repeat:no-repeat!important$`)
	packagingGeneratedCSSRulePattern         = regexp.MustCompile(`(?s)\s*([^{}]+)\{([^{}]+)\}\s*`)
	packagingGeneratedRotatePattern          = regexp.MustCompile(`^rotate\(\s*-?(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)deg\s*\)$`)
	packagingGeneratedBorderPattern          = regexp.MustCompile(`^(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)px solid #[0-9A-Fa-f]{6}$`)
	packagingGeneratedInlineImagePattern     = regexp.MustCompile(`^position:absolute;inset:0;width:100%;height:100%;display:block;visibility:visible;opacity:1;background-image:url\(data:image/[a-zA-Z0-9.+-]+;base64,[A-Za-z0-9+/=]+\);background-size:(?:cover|contain);background-position:-?(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)% -?(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)%;background-repeat:no-repeat$`)
)

func packagingGeneratedServerImageryStyle(node *xhtml.Node, css string) bool {
	if node == nil || legacyNodeAttr(node, "id") != "bonfire-imagery" || len(node.Attr) != 1 || css == "" {
		return false
	}
	matches := packagingGeneratedCSSRulePattern.FindAllStringSubmatchIndex(css, -1)
	if len(matches) == 0 {
		return false
	}
	cursor := 0
	for _, match := range matches {
		if match[0] != cursor || match[1] <= match[0] || len(match) < 6 {
			return false
		}
		selector := strings.TrimSpace(css[match[2]:match[3]])
		body := strings.TrimSpace(css[match[4]:match[5]])
		if !packagingGeneratedImagerySelectorPattern.MatchString(selector) || !packagingGeneratedImageryBodyPattern.MatchString(body) {
			return false
		}
		cursor = match[1]
	}
	return cursor == len(css)
}

// Premium generated copy is deliberately plain. Nested styling creates a
// second, unvalidated typography surface (for example an 8px child span inside
// an admitted 52px parent). Only an attribute-free line break is structural;
// every other element descendant can alter the locked visual hierarchy.
func packagingGeneratedTextTreeHasVisualOverrides(node *xhtml.Node) bool {
	if node == nil {
		return false
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.ElementNode {
			if !strings.EqualFold(child.Data, "br") || len(child.Attr) != 0 || child.FirstChild != nil {
				return true
			}
		}
		if packagingGeneratedTextTreeHasVisualOverrides(child) {
			return true
		}
	}
	return false
}

// packagingGeneratedTextTreeLines preserves authored hard line breaks for the
// deterministic fit gate. The native import intentionally flattens <br> for
// plain-text copy matching while RichText preserves it; without this parallel
// structural read, unlimited breaks could pass a one-line character estimate
// and then clip in the browser.
func packagingGeneratedTextTreeLines(node *xhtml.Node) []string {
	lines := []string{""}
	var walk func(*xhtml.Node)
	walk = func(current *xhtml.Node) {
		if current == nil {
			return
		}
		if current.Type == xhtml.TextNode {
			lines[len(lines)-1] += current.Data
			return
		}
		if current != node && current.Type == xhtml.ElementNode && strings.EqualFold(current.Data, "br") {
			lines = append(lines, "")
			return
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return lines
}

func validatePackagingGeneratedSceneMapping(deck deckDocument, source packagingGeneratedSceneSource) error {
	if len(deck.Slides) != len(source.SlideOrder) {
		return fmt.Errorf("native scene has %d slides but source maps %d", len(deck.Slides), len(source.SlideOrder))
	}
	for slideIndex, slide := range deck.Slides {
		wantSlide := source.SlideOrder[slideIndex]
		if slide.ID != wantSlide {
			return fmt.Errorf("native slide %d maps to %q; source maps %q", slideIndex+1, slide.ID, wantSlide)
		}
		wantElements := source.SlideElements[wantSlide]
		if len(slide.Elements) != len(wantElements) {
			return fmt.Errorf("native slide %q has %d elements but source maps %d", slide.ID, len(slide.Elements), len(wantElements))
		}
		for elementIndex, element := range slide.Elements {
			if element.ID != wantElements[elementIndex] {
				return fmt.Errorf("native element %d on %q maps to %q; source maps %q", elementIndex+1, slide.ID, element.ID, wantElements[elementIndex])
			}
		}
	}
	return nil
}

func validatePackagingGeneratedSceneGeometry(deck deckDocument, source packagingGeneratedSceneSource) error {
	for _, slide := range deck.Slides {
		textElements := make([]deckElement, 0, len(slide.Elements))
		bounds := make(map[string]packagingGeneratedSceneBounds, len(slide.Elements))
		for _, element := range slide.Elements {
			box := packagingGeneratedElementBounds(element)
			bounds[element.ID] = box
			if box.MinX < -packagingGeneratedSceneEpsilon || box.MinY < -packagingGeneratedSceneEpsilon ||
				box.MaxX > float64(deckDocumentWidth)+packagingGeneratedSceneEpsilon || box.MaxY > float64(deckDocumentHeight)+packagingGeneratedSceneEpsilon {
				return fmt.Errorf("element %q on slide %q is outside the 1920x1080 canvas", element.ID, slide.ID)
			}
			if element.Type != "text" {
				continue
			}
			if strings.TrimSpace(element.Text) == "" || element.Opacity <= 0 || source.VisuallyHidden[element.ID] || packagingGeneratedTransparentTextColor(element.Color) {
				return fmt.Errorf("text element %q on slide %q is visibly empty", element.ID, slide.ID)
			}
			if !source.SafeZoneExempt[element.ID] && (box.MinX < packagingGeneratedSceneSafeZone-packagingGeneratedSceneEpsilon || box.MinY < packagingGeneratedSceneSafeZone-packagingGeneratedSceneEpsilon ||
				box.MaxX > float64(deckDocumentWidth)-packagingGeneratedSceneSafeZone+packagingGeneratedSceneEpsilon ||
				box.MaxY > float64(deckDocumentHeight)-packagingGeneratedSceneSafeZone+packagingGeneratedSceneEpsilon) {
				return fmt.Errorf("text element %q on slide %q breaches the 96px authored-text safe zone", element.ID, slide.ID)
			}
			textElements = append(textElements, element)
		}
		for left := 0; left < len(textElements); left++ {
			for right := left + 1; right < len(textElements); right++ {
				a, b := textElements[left], textElements[right]
				if !packagingGeneratedTextBoxesNear(bounds[a.ID], bounds[b.ID]) {
					continue
				}
				if source.OverlapAllowed[a.ID] && source.OverlapAllowed[b.ID] {
					continue
				}
				return fmt.Errorf("text elements %q and %q on slide %q intersect or lack 24px breathing room", a.ID, b.ID, slide.ID)
			}
		}
	}
	return nil
}

func packagingGeneratedElementBounds(element deckElement) packagingGeneratedSceneBounds {
	radians := element.Rotation * math.Pi / 180
	rotatedWidth := math.Abs(element.Width*math.Cos(radians)) + math.Abs(element.Height*math.Sin(radians))
	rotatedHeight := math.Abs(element.Width*math.Sin(radians)) + math.Abs(element.Height*math.Cos(radians))
	centerX := element.X + element.Width/2
	centerY := element.Y + element.Height/2
	return packagingGeneratedSceneBounds{
		MinX: centerX - rotatedWidth/2, MinY: centerY - rotatedHeight/2,
		MaxX: centerX + rotatedWidth/2, MaxY: centerY + rotatedHeight/2,
	}
}

func packagingGeneratedTextBoxesNear(a, b packagingGeneratedSceneBounds) bool {
	return a.MinX < b.MaxX+packagingGeneratedSceneTextGap-packagingGeneratedSceneEpsilon &&
		a.MaxX > b.MinX-packagingGeneratedSceneTextGap+packagingGeneratedSceneEpsilon &&
		a.MinY < b.MaxY+packagingGeneratedSceneTextGap-packagingGeneratedSceneEpsilon &&
		a.MaxY > b.MinY-packagingGeneratedSceneTextGap+packagingGeneratedSceneEpsilon
}

func packagingGeneratedNodeHasAttribute(node *xhtml.Node, key string) bool {
	for _, attribute := range node.Attr {
		if strings.EqualFold(strings.TrimSpace(attribute.Key), key) {
			return true
		}
	}
	return false
}

func packagingGeneratedTransparentTextColor(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "transparent" {
		return true
	}
	if len(value) == 5 && strings.HasPrefix(value, "#") {
		return value[4] == '0'
	}
	if len(value) == 9 && strings.HasPrefix(value, "#") {
		return value[7:] == "00"
	}
	return false
}

type packagingLockedCopySlide struct {
	ID      string
	Kind    string
	Visible []string
}

func validatePackagingGeneratedSceneLockedCopy(app *kanbanBoardApp, plan *goalPlan, deck deckDocument) error {
	artifact, present, err := packagingGeneratedStageArtifact(app, plan, "write", "deck_copy_v3")
	if err != nil {
		return err
	}
	if !present {
		if packagingStudioPremiumDesignContract(plan) {
			return fmt.Errorf("premium generated scene is missing its completed deck_copy_v3 lock")
		}
		return nil
	}
	if packagingStudioPremiumDesignContract(plan) {
		stage := plan.subtaskByID("write")
		if stage == nil || stage.Status != subtaskComplete {
			return fmt.Errorf("premium generated scene deck_copy_v3 lock is not complete")
		}
		if err := validatePackagingStudioDeckCopyOutput(app, plan, artifact.Text); err != nil {
			return fmt.Errorf("premium generated scene deck_copy_v3 lock is invalid: %w", err)
		}
	}
	root, err := packagingGeneratedJSONObject(artifact.Text, "deck_copy_v3")
	if err != nil {
		return err
	}
	rawSlides, ok := root["slides"].([]any)
	if !ok || len(rawSlides) == 0 {
		return fmt.Errorf("deck_copy_v3 must contain a non-empty slides array")
	}
	locked := make([]packagingLockedCopySlide, 0, len(rawSlides))
	seen := map[string]struct{}{}
	for index, raw := range rawSlides {
		slide, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("deck_copy_v3 slide %d must be an object", index+1)
		}
		id, _ := slide["slide_id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			return fmt.Errorf("deck_copy_v3 slide %d has no slide_id", index+1)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("deck_copy_v3 repeats slide_id %q", id)
		}
		seen[id] = struct{}{}
		visible := packagingGeneratedLockedVisibleCopy(slide)
		if len(visible) == 0 {
			return fmt.Errorf("deck_copy_v3 slide %q has no deterministic visible copy", id)
		}
		kind, _ := slide["slide_kind"].(string)
		locked = append(locked, packagingLockedCopySlide{ID: id, Kind: strings.TrimSpace(kind), Visible: visible})
	}
	if len(deck.Slides) != len(locked) {
		return fmt.Errorf("generated scene has %d slides; locked deck_copy_v3 has %d", len(deck.Slides), len(locked))
	}
	for index, slide := range deck.Slides {
		if slide.ID != locked[index].ID {
			return fmt.Errorf("generated slide %d maps to %q; locked deck_copy_v3 maps to %q", index+1, slide.ID, locked[index].ID)
		}
		var sceneStrings []string
		for _, element := range slide.Elements {
			if element.Type == "text" && strings.TrimSpace(element.Text) != "" {
				sceneStrings = append(sceneStrings, element.Text)
			}
		}
		if packagingStudioPremiumDesignContract(plan) {
			if err := packagingStudioCompareExactVisibleCopy(slide, locked[index]); err != nil {
				return err
			}
			continue
		}
		sceneText := packagingGeneratedCanonicalText(strings.Join(sceneStrings, " "))
		for _, value := range locked[index].Visible {
			if !strings.Contains(sceneText, packagingGeneratedCanonicalText(value)) {
				return fmt.Errorf("generated slide %q drifted from locked copy %q", slide.ID, trimForStorage(value, 96))
			}
		}
	}
	return nil
}

func packagingGeneratedLockedVisibleCopy(slide map[string]any) []string {
	visibleKeys := map[string]struct{}{
		"headline": {}, "kicker": {}, "visible_copy": {}, "visible copy": {}, "body": {},
		"evidence_label": {}, "source_label": {}, "evidence/source label": {}, "evidence_source_label": {},
	}
	var values []string
	for key, value := range slide {
		if _, visible := visibleKeys[strings.ToLower(strings.TrimSpace(key))]; !visible {
			continue
		}
		packagingGeneratedAppendVisibleStrings(&values, value)
	}
	return values
}

func packagingGeneratedAppendVisibleStrings(values *[]string, value any) {
	switch typed := value.(type) {
	case string:
		if trimmed := strings.TrimSpace(typed); trimmed != "" {
			*values = append(*values, trimmed)
		}
	case []any:
		for _, item := range typed {
			packagingGeneratedAppendVisibleStrings(values, item)
		}
	case map[string]any:
		for _, key := range []string{"text", "label", "value", "copy"} {
			if item, exists := typed[key]; exists {
				packagingGeneratedAppendVisibleStrings(values, item)
			}
		}
	}
}

func packagingGeneratedCanonicalText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func validatePackagingGeneratedSceneLockedLayoutLegacy(app *kanbanBoardApp, plan *goalPlan, deck deckDocument) error {
	artifact, present, err := packagingGeneratedStageArtifact(app, plan, "layout_plan", "layout_plan_v3")
	if err != nil || !present {
		return err
	}
	root, err := packagingGeneratedJSONObject(artifact.Text, "layout_plan_v3")
	if err != nil {
		return err
	}
	rawSlides, ok := root["slides"].([]any)
	if !ok || len(rawSlides) == 0 {
		return fmt.Errorf("layout_plan_v3 must contain a non-empty slides array")
	}
	if len(rawSlides) != len(deck.Slides) {
		return fmt.Errorf("generated scene has %d slides; locked layout_plan_v3 has %d", len(deck.Slides), len(rawSlides))
	}
	seenSlides := map[string]struct{}{}
	for slideIndex, raw := range rawSlides {
		layoutSlide, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("layout_plan_v3 slide %d must be an object", slideIndex+1)
		}
		slideID := packagingGeneratedMapString(layoutSlide, "slide_id", "id")
		if slideID == "" {
			return fmt.Errorf("layout_plan_v3 slide %d has no slide_id", slideIndex+1)
		}
		if _, duplicate := seenSlides[slideID]; duplicate {
			return fmt.Errorf("layout_plan_v3 repeats slide_id %q", slideID)
		}
		seenSlides[slideID] = struct{}{}
		if deck.Slides[slideIndex].ID != slideID {
			return fmt.Errorf("generated slide %d maps to %q; locked layout_plan_v3 maps to %q", slideIndex+1, deck.Slides[slideIndex].ID, slideID)
		}
		rawElementsValue, hasElements := layoutSlide["elements"]
		if !hasElements {
			continue
		}
		rawElements, ok := rawElementsValue.([]any)
		if !ok || len(rawElements) == 0 {
			return fmt.Errorf("layout_plan_v3 slide %q elements must be a non-empty array", slideID)
		}
		sceneElements := make(map[string]deckElement, len(deck.Slides[slideIndex].Elements))
		for _, element := range deck.Slides[slideIndex].Elements {
			sceneElements[element.ID] = element
		}
		seenElements := map[string]struct{}{}
		for elementIndex, rawElement := range rawElements {
			layoutElement, ok := rawElement.(map[string]any)
			if !ok {
				return fmt.Errorf("layout_plan_v3 element %d on %q must be an object", elementIndex+1, slideID)
			}
			elementID := packagingGeneratedMapString(layoutElement, "element_id", "id")
			if elementID == "" {
				return fmt.Errorf("layout_plan_v3 element %d on %q has no id", elementIndex+1, slideID)
			}
			if _, duplicate := seenElements[elementID]; duplicate {
				return fmt.Errorf("layout_plan_v3 repeats element %q on %q", elementID, slideID)
			}
			seenElements[elementID] = struct{}{}
			sceneElement, exists := sceneElements[elementID]
			if !exists {
				return fmt.Errorf("generated slide %q is missing locked layout element %q", slideID, elementID)
			}
			if typ := packagingGeneratedMapString(layoutElement, "type", "element_type"); typ != "" && !strings.EqualFold(typ, sceneElement.Type) {
				return fmt.Errorf("generated element %q type %q drifted from locked layout type %q", elementID, sceneElement.Type, typ)
			}
			if err := packagingGeneratedCompareLayoutGeometry(slideID, sceneElement, layoutElement); err != nil {
				return err
			}
		}
	}
	return nil
}

func packagingGeneratedCompareLayoutGeometry(slideID string, scene deckElement, layout map[string]any) error {
	comparisons := []struct {
		key    string
		actual float64
	}{
		{"x", scene.X}, {"y", scene.Y}, {"width", scene.Width}, {"height", scene.Height},
		{"z", float64(scene.Z)}, {"opacity", scene.Opacity}, {"rotation", scene.Rotation},
	}
	for _, comparison := range comparisons {
		expected, comparable, err := packagingGeneratedMapNumber(layout, comparison.key)
		if err != nil {
			return fmt.Errorf("layout_plan_v3 element %q on %q has invalid %s", scene.ID, slideID, comparison.key)
		}
		if !comparable {
			continue
		}
		if math.Abs(expected-comparison.actual) > packagingGeneratedSceneEpsilon {
			return fmt.Errorf("generated element %q on %q has %s %.2f; locked layout requires %.2f", scene.ID, slideID, comparison.key, comparison.actual, expected)
		}
	}
	return nil
}

func packagingGeneratedStageArtifact(app *kanbanBoardApp, plan *goalPlan, stageID, contract string) (meetingMemoryEntry, bool, error) {
	if app == nil || plan == nil {
		return meetingMemoryEntry{}, false, nil
	}
	stage := plan.subtaskByID(stageID)
	if stage == nil {
		return meetingMemoryEntry{}, false, nil
	}
	if strings.TrimSpace(stage.ArtifactID) == "" {
		if stage.Status == subtaskComplete {
			return meetingMemoryEntry{}, false, fmt.Errorf("%s stage is complete without its locked artifact", contract)
		}
		return meetingMemoryEntry{}, false, nil
	}
	artifact, ok := app.osArtifactByID(stage.ArtifactID)
	if !ok {
		return meetingMemoryEntry{}, false, fmt.Errorf("%s artifact is missing", contract)
	}
	return artifact, true, nil
}

func packagingGeneratedJSONObject(body, contract string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(extractJSONObject(body)))
	decoder.UseNumber()
	value, err := decodeUniqueJSONValue(decoder)
	if err != nil || ensureJSONEOF(decoder) != nil {
		return nil, fmt.Errorf("%s is malformed JSON", contract)
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON object", contract)
	}
	return root, nil
}

func packagingGeneratedMapString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func packagingGeneratedMapNumber(object map[string]any, key string) (float64, bool, error) {
	value, exists := object[key]
	if !exists {
		return 0, false, nil
	}
	switch typed := value.(type) {
	case json.Number:
		number, err := typed.Float64()
		return number, true, err
	case float64:
		return typed, true, nil
	case string:
		number, ok := legacyDeckNumber(typed)
		if !ok {
			return 0, true, fmt.Errorf("invalid number")
		}
		return number, true, nil
	default:
		return 0, true, fmt.Errorf("invalid number")
	}
}
