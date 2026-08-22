package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	xhtml "golang.org/x/net/html"
)

const (
	packagingGeneratedSceneSafeZone = 96.0
	packagingGeneratedSceneTextGap  = 24.0
	packagingGeneratedSceneEpsilon  = 0.01
)

// packagingGeneratedScenePreflightRequired keeps the stricter production
// contract on Packaging Studio's generated v5 candidate. Deck Studio remains
// deliberately permissive so a human can move elements beyond the canvas while
// editing and recover them later.
func packagingGeneratedScenePreflightRequired(plan *goalPlan) bool {
	return plan != nil && strings.EqualFold(strings.TrimSpace(plan.ProcessID), packagingStudioProcessID) &&
		(plan.ProcessVersion >= 5 || strings.EqualFold(strings.TrimSpace(plan.ProcessImplementationRevision), "packaging_studio.runtime.v5"))
}

type packagingGeneratedSceneSource struct {
	SlideOrder     []string
	SlideElements  map[string][]string
	ElementSlide   map[string]string
	OverlapAllowed map[string]bool
	SafeZoneExempt map[string]bool
	VisuallyHidden map[string]bool
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
	if err := validatePackagingGeneratedSceneLockedLayout(app, plan, deck); err != nil {
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
		SlideElements: make(map[string][]string), ElementSlide: make(map[string]string), OverlapAllowed: make(map[string]bool), SafeZoneExempt: make(map[string]bool), VisuallyHidden: make(map[string]bool),
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
	Visible []string
}

func validatePackagingGeneratedSceneLockedCopy(app *kanbanBoardApp, plan *goalPlan, deck deckDocument) error {
	artifact, present, err := packagingGeneratedStageArtifact(app, plan, "write", "deck_copy_v3")
	if err != nil || !present {
		return err
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
		locked = append(locked, packagingLockedCopySlide{ID: id, Visible: visible})
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

func validatePackagingGeneratedSceneLockedLayout(app *kanbanBoardApp, plan *goalPlan, deck deckDocument) error {
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
