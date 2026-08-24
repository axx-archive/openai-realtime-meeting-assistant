package main

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	packagingStudioCounterIDPattern   = regexp.MustCompile(`(?i)^(?:page-number|slide-number|s[0-9]{1,3}-number)$`)
	packagingStudioCounterTextPattern = regexp.MustCompile(`^[0-9]{1,3}(?:\s*/\s*[0-9]{1,3})?$`)
)

type packagingStudioLayoutIdentity struct {
	SelectedCandidateID string
	Strategy            string
	VisualSystem        string
	Tokens              imageryIdentityTokens
}

type packagingStudioLockedLayoutText struct {
	Text            string
	Role            string
	ClaimIDs        []string
	ClaimRenderings []string
	StatementType   string
}

type packagingStudioGridElement struct {
	ID     string
	Type   string
	Role   string
	X      float64
	Y      float64
	Width  float64
	Height float64
}

const (
	packagingStudioGridStart       = 120.0
	packagingStudioGridColumnWidth = 118.0
	packagingStudioGridGutter      = 24.0
	packagingStudioGridColumns     = 12
)

func packagingStudioGridPrompt() string {
	return "Grid geometry is server-owned and exact: editorial_12 uses 12 columns from x=120 to x=1800, each 118px with 24px gutters; every non-bleed text/image left and right edge must land on a column edge. modular_12 uses that same horizontal grid and a 24px y/height baseline. split_6_6 uses left panel x=120..948 and right panel x=972..1800; each non-bleed text/image must stay inside one panel or span exactly x=120..1800. single_axis requires every primary headline/kicker/body element to share one exact x anchor. Full-canvas imagery and decorative shapes are exempt; labels alone never satisfy a grid."
}

func packagingStudioNear(left, right float64) bool {
	return math.Abs(left-right) <= packagingGeneratedSceneEpsilon
}

func packagingStudioGridStartLine(value float64) bool {
	pitch := packagingStudioGridColumnWidth + packagingStudioGridGutter
	for index := 0; index < packagingStudioGridColumns; index++ {
		if packagingStudioNear(value, packagingStudioGridStart+float64(index)*pitch) {
			return true
		}
	}
	return false
}

func packagingStudioGridEndLine(value float64) bool {
	pitch := packagingStudioGridColumnWidth + packagingStudioGridGutter
	for index := 0; index < packagingStudioGridColumns; index++ {
		if packagingStudioNear(value, packagingStudioGridStart+float64(index)*pitch+packagingStudioGridColumnWidth) {
			return true
		}
	}
	return false
}

func packagingStudioGridFullCanvas(element packagingStudioGridElement) bool {
	return packagingStudioNear(element.X, 0) && packagingStudioNear(element.Y, 0) &&
		packagingStudioNear(element.Width, deckDocumentWidth) && packagingStudioNear(element.Height, deckDocumentHeight)
}

func packagingStudioGridBaseline(value float64) bool {
	units := value / packagingStudioGridGutter
	return packagingStudioNear(units, math.Round(units))
}

func validatePackagingStudioGridGeometry(slideID, grid string, elements []packagingStudioGridElement) error {
	for _, element := range elements {
		if element.Type == "shape" || packagingStudioGridFullCanvas(element) {
			continue
		}
		switch grid {
		case "editorial_12", "modular_12":
			if !packagingStudioGridStartLine(element.X) || !packagingStudioGridEndLine(element.X+element.Width) {
				return fmt.Errorf("generated element %q on %q does not use the concrete %s column geometry", element.ID, slideID, grid)
			}
			if grid == "modular_12" && (!packagingStudioGridBaseline(element.Y) || !packagingStudioGridBaseline(element.Height)) {
				return fmt.Errorf("generated element %q on %q does not use the concrete modular_12 vertical baseline", element.ID, slideID)
			}
		case "split_6_6":
			left := element.X >= 120-packagingGeneratedSceneEpsilon && element.X+element.Width <= 948+packagingGeneratedSceneEpsilon
			right := element.X >= 972-packagingGeneratedSceneEpsilon && element.X+element.Width <= 1800+packagingGeneratedSceneEpsilon
			span := packagingStudioNear(element.X, 120) && packagingStudioNear(element.X+element.Width, 1800)
			if !left && !right && !span {
				return fmt.Errorf("generated element %q on %q crosses the concrete split_6_6 panels", element.ID, slideID)
			}
		case "single_axis":
			// Enforced below across the primary hierarchy.
		default:
			return fmt.Errorf("generated slide %q has no server-owned geometry for grid %q", slideID, grid)
		}
	}
	if grid == "single_axis" {
		anchor, anchored := 0.0, false
		for _, element := range elements {
			if element.Type != "text" || !oneOf(element.Role, "headline", "kicker", "body") {
				continue
			}
			if !anchored {
				anchor, anchored = element.X, true
				continue
			}
			if !packagingStudioNear(element.X, anchor) {
				return fmt.Errorf("generated primary text %q on %q breaks the concrete single_axis anchor", element.ID, slideID)
			}
		}
		if !anchored {
			return fmt.Errorf("generated slide %q has no primary text to establish its single_axis grid", slideID)
		}
	}
	return nil
}

// Abstract identity tokens resolve once, server-side, to real portable CSS
// stacks. Layout and rendered HTML must carry this exact resolution; a model
// can never pass a literal non-font token such as `modern_grotesk` through as
// the browser font-family.
var packagingStudioResolvedFontStacks = map[string]string{
	"modern_grotesk":   "Aptos, Helvetica Neue, Arial, sans-serif",
	"editorial_serif":  "Iowan Old Style, Georgia, Times New Roman, serif",
	"humanist_sans":    "Avenir Next, Avenir, Segoe UI, sans-serif",
	"geometric_sans":   "Futura, Century Gothic, Avenir Next, sans-serif",
	"condensed_sans":   "Arial Narrow, Aptos Narrow, Helvetica Neue, sans-serif",
	"monospace_accent": "SFMono-Regular, Menlo, Consolas, monospace",
}

func packagingStudioResolvedFontStack(token string) (string, bool) {
	stack, ok := packagingStudioResolvedFontStacks[strings.TrimSpace(token)]
	return stack, ok
}

func packagingStudioFontResolutionPrompt() string {
	order := []string{"modern_grotesk", "editorial_serif", "humanist_sans", "geometric_sans", "condensed_sans", "monospace_accent"}
	parts := make([]string, 0, len(order))
	for _, token := range order {
		parts = append(parts, token+" => "+packagingStudioResolvedFontStacks[token])
	}
	return strings.Join(parts, " | ")
}

func packagingStudioIdentityPalette(tokens imageryIdentityTokens) map[string]bool {
	palette := map[string]bool{}
	for _, assignment := range strings.Split(tokens.Palette, ";") {
		parts := strings.SplitN(assignment, "=", 2)
		if len(parts) == 2 {
			palette[strings.ToLower(strings.TrimSpace(parts[1]))] = true
		}
	}
	return palette
}

func packagingStudioIdentityTypeRoles(tokens imageryIdentityTokens) map[string]string {
	roles := map[string]string{}
	for _, assignment := range strings.Split(tokens.Type, ";") {
		parts := strings.SplitN(assignment, "=", 2)
		if len(parts) == 2 {
			roles[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return roles
}

func packagingStudioParseLayoutIdentity(root map[string]any) (packagingStudioLayoutIdentity, error) {
	var identity packagingStudioLayoutIdentity
	raw, ok := root["visual_identity"].(map[string]any)
	if !ok {
		return identity, fmt.Errorf("layout_plan_v3 is missing visual_identity")
	}
	if err := packagingStudioLayoutExactKeys(raw, []string{"selected_candidate_id", "strategy", "visual_system", "tokens"}, "layout_plan_v3 visual_identity"); err != nil {
		return identity, err
	}
	var err error
	if identity.SelectedCandidateID, err = packagingStudioLayoutString(raw, "selected_candidate_id", "layout_plan_v3 visual_identity", false); err != nil || !packagingStudioIdentityCandidateIDPattern.MatchString(identity.SelectedCandidateID) {
		return identity, fmt.Errorf("layout_plan_v3 visual_identity has invalid selected_candidate_id")
	}
	if identity.Strategy, err = packagingStudioLayoutString(raw, "strategy", "layout_plan_v3 visual_identity", false); err != nil || !packagingStudioClosedEnum(identity.Strategy, "strategy") {
		return identity, fmt.Errorf("layout_plan_v3 visual_identity has invalid strategy")
	}
	if identity.VisualSystem, err = packagingStudioLayoutString(raw, "visual_system", "layout_plan_v3 visual_identity", false); err != nil || !packagingStudioClosedEnum(identity.VisualSystem, "visual_system") {
		return identity, fmt.Errorf("layout_plan_v3 visual_identity has invalid visual_system")
	}
	identity.Tokens, err = parseIdentityTokens(raw["tokens"], "layout_plan_v3 visual_identity tokens")
	return identity, err
}

func packagingStudioCanonicalIdentityForLayout(app *kanbanBoardApp, plan *goalPlan) (imageryDirectionDoc, error) {
	stage := plan.subtaskByID("identity")
	if stage == nil || stage.Status != subtaskComplete || strings.TrimSpace(stage.ArtifactID) == "" {
		return imageryDirectionDoc{}, fmt.Errorf("premium generated scene is missing its completed canonical visual identity lock")
	}
	artifact, ok := app.osArtifactByID(stage.ArtifactID)
	if !ok {
		return imageryDirectionDoc{}, fmt.Errorf("premium generated scene canonical visual identity artifact is missing")
	}
	body, err := processStageArtifactForwardText(artifact)
	if err != nil {
		return imageryDirectionDoc{}, err
	}
	return validateCanonicalPackagingStudioIdentityDirection(app, plan, artifact, body)
}

func packagingStudioForwardStatementOwner(value, statementType string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	switch statementType {
	case "recommendation":
		return strings.HasPrefix(lower, "recommendation:")
	case "proposal":
		return strings.HasPrefix(lower, "proposal:") || strings.HasPrefix(lower, "target:") || strings.HasPrefix(lower, "phase ")
	case "inference":
		return strings.HasPrefix(lower, "inference:")
	default:
		return false
	}
}

func packagingStudioLockedLayoutTextBySlide(app *kanbanBoardApp, plan *goalPlan) (map[string][]packagingStudioLockedLayoutText, error) {
	artifact, present, err := packagingGeneratedStageArtifact(app, plan, "write", "deck_copy_v3")
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, fmt.Errorf("premium generated scene is missing its completed deck_copy_v3 lock")
	}
	root, err := packagingStudioStrictRawJSONObject(artifact.Text, "deck_copy_v3")
	if err != nil {
		return nil, err
	}
	rawSlides, _ := root["slides"].([]any)
	out := make(map[string][]packagingStudioLockedLayoutText, len(rawSlides))
	for index, raw := range rawSlides {
		slide, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("deck_copy_v3 slide %d must be an object", index+1)
		}
		id, _ := slide["slide_id"].(string)
		claimIDs, err := packagingStudioContractStringArray(slide, "claim_ids", "deck_copy_v3 slide "+strings.TrimSpace(id), 1)
		if err != nil {
			return nil, err
		}
		claimRenderings, err := packagingStudioContractStringArray(slide, "claim_renderings", "deck_copy_v3 slide "+strings.TrimSpace(id), 1)
		if err != nil {
			return nil, err
		}
		statementType := ""
		if _, exists := slide["statement_type"]; exists {
			statementType, err = packagingStudioContractString(slide, "statement_type", "deck_copy_v3 slide "+strings.TrimSpace(id), false, 32)
			if err != nil {
				return nil, err
			}
		}
		fields := []struct{ key, role string }{{"headline", "headline"}, {"kicker", "kicker"}, {"body", "body"}, {"evidence_label", "evidence"}, {"source_label", "source"}}
		claimOwner, statementOwner := -1, -1
		for fieldIndex, field := range fields {
			value, _ := slide[field.key].(string)
			if field.role != "source" && len(claimRenderings) == 1 && strings.Contains(packagingGeneratedCanonicalText(value), packagingGeneratedCanonicalText(claimRenderings[0])) {
				claimOwner = fieldIndex
			}
			if statementType != "" && packagingStudioForwardStatementOwner(value, statementType) {
				if statementOwner >= 0 {
					return nil, fmt.Errorf("deck_copy_v3 slide %q maps statement_type to more than one visible field", strings.TrimSpace(id))
				}
				statementOwner = fieldIndex
			}
		}
		if statementType != "" && statementOwner < 0 {
			return nil, fmt.Errorf("deck_copy_v3 slide %q does not map statement_type to one visibly labeled field", strings.TrimSpace(id))
		}
		for fieldIndex, field := range fields {
			value, _ := slide[field.key].(string)
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			spec := packagingStudioLockedLayoutText{Text: value, Role: field.role}
			if fieldIndex == claimOwner {
				spec.ClaimIDs = append([]string(nil), claimIDs...)
				spec.ClaimRenderings = append([]string(nil), claimRenderings...)
			}
			if fieldIndex == statementOwner {
				spec.StatementType = statementType
			}
			out[strings.TrimSpace(id)] = append(out[strings.TrimSpace(id)], spec)
		}
	}
	return out, nil
}

func packagingStudioGeneratedPlacementsForLayout(app *kanbanBoardApp, plan *goalPlan, identity imageryDirectionDoc) (map[int]packagingStudioGeneratedShot, error) {
	stage := plan.subtaskByID("imagery_generate")
	if stage == nil || stage.Status != subtaskComplete || strings.TrimSpace(stage.ArtifactID) == "" {
		return nil, fmt.Errorf("premium generated scene is missing its completed imagery-generation receipt")
	}
	artifact, ok := app.osArtifactByID(stage.ArtifactID)
	if !ok {
		return nil, fmt.Errorf("premium generated scene imagery-generation receipt is missing")
	}
	shotStatus := strings.TrimSpace(artifact.Metadata["imageryShots"])
	raw := strings.TrimSpace(artifact.Metadata["imageryFigs"])
	if raw == "" {
		switch shotStatus {
		case "0":
			if len(identity.Shots) != 0 {
				return nil, fmt.Errorf("premium imagery-generation receipt claims zero shots despite %d canonical directed shot(s)", len(identity.Shots))
			}
			return map[int]packagingStudioGeneratedShot{}, nil
		case "skipped":
			if len(identity.Shots) == 0 {
				return nil, fmt.Errorf("premium imagery-generation receipt claims a skipped provider run without canonical directed shots")
			}
			return map[int]packagingStudioGeneratedShot{}, nil
		}
		return nil, fmt.Errorf("premium imagery-generation receipt does not name its generated FIG set")
	}
	declaredCount, countErr := strconv.Atoi(shotStatus)
	if countErr != nil || declaredCount < 1 || strconv.Itoa(declaredCount) != shotStatus {
		return nil, fmt.Errorf("premium imagery-generation receipt has an invalid generated-shot count")
	}
	var generated []packagingStudioGeneratedShot
	if err := json.Unmarshal([]byte(raw), &generated); err != nil || len(generated) != declaredCount {
		return nil, fmt.Errorf("premium imagery-generation FIG receipt count does not match imageryShots")
	}
	if len(generated) > len(identity.Shots) {
		return nil, fmt.Errorf("premium imagery-generation FIG receipt is malformed")
	}
	directed := make(map[int]imageryDirectionShot, len(identity.Shots))
	for _, shot := range identity.Shots {
		directed[shot.Fig] = shot
	}
	out := make(map[int]packagingStudioGeneratedShot, len(generated))
	for _, placement := range generated {
		shot, exists := directed[placement.Fig]
		if !exists || out[placement.Fig].Fig != 0 || !validBlobRef(placement.Ref) || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(placement.Mime)), "image/") {
			return nil, fmt.Errorf("premium imagery-generation FIG receipt contains an unbound, duplicate, or invalid placement")
		}
		if placement.SlideID != shot.SlideID || placement.Slot != shot.Slot || placement.Subject != shot.Subject ||
			placement.Composition != shot.Composition || placement.Temperature != shot.Temperature || placement.Treatment != shot.Treatment ||
			placement.Aspect != shot.Aspect || placement.Caption != shot.Caption || placement.Place != shot.Place || placement.Why != shot.Why ||
			placement.DepictionKind != shot.DepictionKind || placement.DepictionEntity != shot.DepictionEntity || placement.DepictionRef != shot.DepictionRef {
			return nil, fmt.Errorf("premium imagery-generation FIG %d drifted from the canonical art direction", placement.Fig)
		}
		out[placement.Fig] = placement
	}
	return out, nil
}

func packagingStudioPremiumDesignContract(plan *goalPlan) bool {
	return plan != nil && strings.EqualFold(strings.TrimSpace(plan.ProcessID), packagingStudioProcessID) &&
		(plan.ProcessVersion >= 8 || strings.EqualFold(strings.TrimSpace(plan.ProcessImplementationRevision), "packaging_studio.runtime.v8.premium-design-contract.v1"))
}

func packagingStudioServerFurniture(element deckElement) bool {
	return element.Type == "text" && packagingStudioCounterIDPattern.MatchString(element.ID) &&
		packagingStudioCounterTextPattern.MatchString(strings.TrimSpace(element.Text))
}

func packagingStudioCompareExactVisibleCopy(slide deckSlide, locked packagingLockedCopySlide) error {
	expected := map[string]int{}
	for _, value := range locked.Visible {
		expected[packagingGeneratedCanonicalText(value)]++
	}
	furniture := 0
	for _, element := range slide.Elements {
		if element.Type != "text" || strings.TrimSpace(element.Text) == "" {
			continue
		}
		value := packagingGeneratedCanonicalText(element.Text)
		if expected[value] > 0 {
			expected[value]--
			continue
		}
		if packagingStudioServerFurniture(element) && furniture == 0 {
			furniture++
			continue
		}
		return fmt.Errorf("generated slide %q introduced undeclared visible text %q", slide.ID, trimForStorage(element.Text, 96))
	}
	for value, count := range expected {
		if count > 0 {
			return fmt.Errorf("generated slide %q is missing locked copy %q", slide.ID, trimForStorage(value, 96))
		}
	}
	return nil
}

func validatePackagingGeneratedSceneLockedLayout(app *kanbanBoardApp, plan *goalPlan, deck deckDocument, source packagingGeneratedSceneSource) error {
	if !packagingStudioPremiumDesignContract(plan) {
		return validatePackagingGeneratedSceneLockedLayoutLegacy(app, plan, deck)
	}
	return validatePackagingGeneratedSceneLockedLayoutPremium(app, plan, deck, source)
}

func packagingStudioLayoutExactKeys(object map[string]any, allowed []string, label string) error {
	if len(object) != len(allowed) {
		return fmt.Errorf("%s must contain exactly %s", label, strings.Join(allowed, ", "))
	}
	for _, key := range allowed {
		if _, ok := object[key]; !ok {
			return fmt.Errorf("%s must contain exactly %s", label, strings.Join(allowed, ", "))
		}
	}
	return nil
}

func packagingStudioLayoutNumber(object map[string]any, key, label string) (float64, error) {
	value, ok := packagingStudioJSONNumber(object[key])
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("%s %s must be a finite number", label, key)
	}
	return value, nil
}

func packagingStudioLayoutString(object map[string]any, key, label string, allowEmpty bool) (string, error) {
	return packagingStudioContractString(object, key, label, allowEmpty, 320)
}

func packagingStudioRequireGeometry(slideID string, scene deckElement, layout map[string]any) error {
	label := fmt.Sprintf("layout_plan_v3 element %q on %q", scene.ID, slideID)
	comparisons := []struct {
		key    string
		actual float64
	}{
		{"x", scene.X}, {"y", scene.Y}, {"width", scene.Width}, {"height", scene.Height},
		{"z", float64(scene.Z)}, {"opacity", scene.Opacity}, {"rotation", scene.Rotation},
	}
	for _, comparison := range comparisons {
		expected, err := packagingStudioLayoutNumber(layout, comparison.key, label)
		if err != nil {
			return err
		}
		if math.Abs(expected-comparison.actual) > packagingGeneratedSceneEpsilon {
			return fmt.Errorf("generated element %q on %q has %s %.2f; locked layout requires %.2f", scene.ID, slideID, comparison.key, comparison.actual, expected)
		}
	}
	return nil
}

func packagingStudioValidateRenderedIdentity(source packagingGeneratedSceneSource, identity packagingStudioLayoutIdentity) error {
	expected := map[string]string{
		"candidate": identity.SelectedCandidateID, "strategy": identity.Strategy, "system": identity.VisualSystem,
		"palette": identity.Tokens.Palette, "type": identity.Tokens.Type, "spacing": identity.Tokens.Spacing,
		"grid": identity.Tokens.Grid, "motif": identity.Tokens.GraphicMotif, "image-treatment": identity.Tokens.ImageTreatment,
		"data-viz": identity.Tokens.DataVizTreatment, "refusals": identity.Tokens.Refusals,
	}
	for key, value := range expected {
		if source.Identity[key] != value {
			return fmt.Errorf("generated #stage identity field %q drifted from locked visual_identity", key)
		}
	}
	return nil
}

func packagingStudioFindLockedTextSpec(layout map[string]any, specs []packagingStudioLockedLayoutText, used []bool) (*packagingStudioLockedLayoutText, error) {
	text, _ := layout["text"].(string)
	role, _ := layout["copy_role"].(string)
	if strings.TrimSpace(role) == "counter" {
		return nil, nil
	}
	for index := range specs {
		if used[index] || specs[index].Role != strings.TrimSpace(role) || packagingGeneratedCanonicalText(specs[index].Text) != packagingGeneratedCanonicalText(text) {
			continue
		}
		used[index] = true
		return &specs[index], nil
	}
	return nil, fmt.Errorf("layout text %q with copy_role %q does not map one-to-one to locked deck_copy_v3", trimForStorage(text, 96), role)
}

func validatePackagingGeneratedSceneLockedLayoutPremium(app *kanbanBoardApp, plan *goalPlan, deck deckDocument, source packagingGeneratedSceneSource) error {
	if app == nil || plan == nil {
		return fmt.Errorf("premium generated scene locks are unavailable")
	}
	artifact, present, err := packagingGeneratedStageArtifact(app, plan, "layout_plan", "layout_plan_v3")
	if err != nil {
		return err
	}
	stage := plan.subtaskByID("layout_plan")
	if !present || stage == nil || stage.Status != subtaskComplete {
		return fmt.Errorf("premium generated scene is missing its completed layout_plan_v3 lock")
	}
	canonicalIdentity, err := packagingStudioCanonicalIdentityForLayout(app, plan)
	if err != nil {
		return err
	}
	lockedText, err := packagingStudioLockedLayoutTextBySlide(app, plan)
	if err != nil {
		return err
	}
	generatedPlacements, err := packagingStudioGeneratedPlacementsForLayout(app, plan, canonicalIdentity)
	if err != nil {
		return err
	}
	root, err := packagingStudioStrictRawJSONObject(artifact.Text, "layout_plan_v3")
	if err != nil {
		return err
	}
	for key := range root {
		if key != "slides" && key != "visual_identity" && !packagingStudioScopedRootKeys[key] {
			return fmt.Errorf("layout_plan_v3 contains undeclared root field %q", key)
		}
	}
	layoutIdentity, err := packagingStudioParseLayoutIdentity(root)
	if err != nil {
		return err
	}
	if layoutIdentity.SelectedCandidateID != canonicalIdentity.SelectedCandidateID || layoutIdentity.Strategy != canonicalIdentity.Strategy ||
		layoutIdentity.VisualSystem != canonicalIdentity.VisualSystem || layoutIdentity.Tokens != canonicalIdentity.Identity {
		return fmt.Errorf("layout_plan_v3 visual_identity drifted from the exact selected canonical identity")
	}
	if err := packagingStudioValidateRenderedIdentity(source, layoutIdentity); err != nil {
		return err
	}
	palette := packagingStudioIdentityPalette(layoutIdentity.Tokens)
	typeRoles := packagingStudioIdentityTypeRoles(layoutIdentity.Tokens)
	rawSlides, ok := root["slides"].([]any)
	if !ok || len(rawSlides) == 0 || len(rawSlides) != len(deck.Slides) {
		return fmt.Errorf("generated scene has %d slides; locked layout_plan_v3 must have exactly %d", len(deck.Slides), len(deck.Slides))
	}
	seenSlides := map[string]bool{}
	usedPlacements := map[int]bool{}
	for slideIndex, raw := range rawSlides {
		layoutSlide, ok := raw.(map[string]any)
		label := fmt.Sprintf("layout_plan_v3 slide %d", slideIndex+1)
		if !ok {
			return fmt.Errorf("%s must be an object", label)
		}
		if err := packagingStudioLayoutExactKeys(layoutSlide, []string{"slide_id", "slide_kind", "composition", "background", "grid", "elements"}, label); err != nil {
			return err
		}
		slideID, err := packagingStudioLayoutString(layoutSlide, "slide_id", label, false)
		if err != nil || seenSlides[slideID] || slideID != deck.Slides[slideIndex].ID {
			return fmt.Errorf("%s must map exactly once to generated slide %q", label, deck.Slides[slideIndex].ID)
		}
		seenSlides[slideID] = true
		slideKind, err := packagingStudioLayoutString(layoutSlide, "slide_kind", label, false)
		if err != nil || !oneOf(slideKind, "cover", "normal", "evidence", "close") || (slideIndex == 0 && slideKind != "cover") {
			return fmt.Errorf("%s has invalid slide_kind", label)
		}
		if _, err := packagingStudioLayoutString(layoutSlide, "composition", label, false); err != nil {
			return err
		}
		background, err := packagingStudioLayoutString(layoutSlide, "background", label, false)
		if err != nil || !deckHexColorPattern.MatchString(background) || !strings.EqualFold(background, deck.Slides[slideIndex].Background) || !palette[strings.ToLower(background)] {
			return fmt.Errorf("%s background must be one exact selected-palette scene color", label)
		}
		grid, err := packagingStudioLayoutString(layoutSlide, "grid", label, false)
		if err != nil || grid != layoutIdentity.Tokens.Grid {
			return fmt.Errorf("%s grid must equal the selected identity grid token", label)
		}
		rawElements, ok := layoutSlide["elements"].([]any)
		if !ok || len(rawElements) == 0 {
			return fmt.Errorf("%s elements must be a non-empty array", label)
		}
		sceneElements := make(map[string]deckElement, len(deck.Slides[slideIndex].Elements))
		for _, element := range deck.Slides[slideIndex].Elements {
			sceneElements[element.ID] = element
		}
		seenElements := map[string]bool{}
		roles := map[string]int{}
		counts := map[string]int{}
		gridElements := make([]packagingStudioGridElement, 0, len(rawElements))
		specs := lockedText[slideID]
		usedSpecs := make([]bool, len(specs))
		for elementIndex, rawElement := range rawElements {
			layoutElement, ok := rawElement.(map[string]any)
			elementLabel := fmt.Sprintf("%s element %d", label, elementIndex+1)
			if !ok {
				return fmt.Errorf("%s must be an object", elementLabel)
			}
			id, err := packagingStudioLayoutString(layoutElement, "id", elementLabel, false)
			if err != nil || seenElements[id] {
				return fmt.Errorf("%s id must be unique and non-empty", elementLabel)
			}
			sceneElement, exists := sceneElements[id]
			if !exists {
				return fmt.Errorf("generated slide %q is missing declared layout element %q", slideID, id)
			}
			seenElements[id] = true
			typ, err := packagingStudioLayoutString(layoutElement, "type", elementLabel, false)
			if err != nil || !oneOf(typ, "text", "image", "shape") || typ != sceneElement.Type {
				return fmt.Errorf("%s type must exactly match generated type %q", elementLabel, sceneElement.Type)
			}
			common := []string{"id", "type", "x", "y", "width", "height", "z", "opacity", "rotation"}
			switch typ {
			case "text":
				allowed := append(append([]string{}, common...), "text", "copy_role", "typography", "claim_ids", "claim_renderings")
				if _, exists := layoutElement["statement_type"]; exists {
					allowed = append(allowed, "statement_type")
				}
				if err := packagingStudioLayoutExactKeys(layoutElement, allowed, elementLabel); err != nil {
					return err
				}
				expected, err := packagingStudioFindLockedTextSpec(layoutElement, specs, usedSpecs)
				if err != nil {
					return err
				}
				if source.UnsafeTextTree[sceneElement.ID] {
					return fmt.Errorf("%s contains nested or styled text markup outside the locked typography contract", elementLabel)
				}
				role, err := validatePackagingStudioLayoutText(elementLabel, slideKind, layoutElement, sceneElement, expected, palette, typeRoles)
				if err != nil {
					return err
				}
				if source.SafeZoneExempt[sceneElement.ID] && role != "counter" {
					return fmt.Errorf("%s locked %s copy may not claim the background-furniture safe-zone exemption", elementLabel, role)
				}
				roles[role]++
				gridElements = append(gridElements, packagingStudioGridElement{ID: sceneElement.ID, Type: sceneElement.Type, Role: role, X: sceneElement.X, Y: sceneElement.Y, Width: sceneElement.Width, Height: sceneElement.Height})
				if role != "counter" {
					counts[typ]++
				}
			case "image":
				if err := packagingStudioLayoutExactKeys(layoutElement, append(common, "fig", "fit", "crop", "focal_point"), elementLabel); err != nil {
					return err
				}
				fig, err := validatePackagingStudioLayoutImage(elementLabel, layoutElement, sceneElement, source)
				if err != nil {
					return err
				}
				placement, exists := generatedPlacements[fig]
				if !exists || usedPlacements[fig] || placement.SlideID != slideID || sceneElement.Ref != placement.Ref || !strings.HasPrefix(strings.ToLower(sceneElement.Name), fmt.Sprintf("fig-%d.", fig)) {
					return fmt.Errorf("%s FIG %d is not one unique generated placement directed to slide %q", elementLabel, fig, slideID)
				}
				if placement.Slot == "bleed" && (sceneElement.X != 0 || sceneElement.Y != 0 || sceneElement.Width != 1920 || sceneElement.Height != 1080 || sceneElement.Fit != "cover") {
					return fmt.Errorf("%s directed bleed FIG %d is not full-canvas cover geometry", elementLabel, fig)
				}
				if placement.Slot == "plate" && sceneElement.X == 0 && sceneElement.Y == 0 && sceneElement.Width == 1920 && sceneElement.Height == 1080 {
					return fmt.Errorf("%s directed plate FIG %d was silently promoted to full bleed", elementLabel, fig)
				}
				usedPlacements[fig] = true
				counts[typ]++
				gridElements = append(gridElements, packagingStudioGridElement{ID: sceneElement.ID, Type: sceneElement.Type, X: sceneElement.X, Y: sceneElement.Y, Width: sceneElement.Width, Height: sceneElement.Height})
			case "shape":
				if err := packagingStudioLayoutExactKeys(layoutElement, append(common, "shape", "fill", "stroke", "stroke_width"), elementLabel); err != nil {
					return err
				}
				if err := validatePackagingStudioLayoutShape(elementLabel, layoutElement, sceneElement, palette); err != nil {
					return err
				}
				counts[typ]++
				gridElements = append(gridElements, packagingStudioGridElement{ID: sceneElement.ID, Type: sceneElement.Type, X: sceneElement.X, Y: sceneElement.Y, Width: sceneElement.Width, Height: sceneElement.Height})
			}
			if err := packagingStudioRequireGeometry(slideID, sceneElement, layoutElement); err != nil {
				return err
			}
		}
		for index, used := range usedSpecs {
			if !used {
				return fmt.Errorf("generated slide %q is missing locked %s copy %q", slideID, specs[index].Role, trimForStorage(specs[index].Text, 96))
			}
		}
		furniture := 0
		for _, sceneElement := range deck.Slides[slideIndex].Elements {
			if seenElements[sceneElement.ID] {
				continue
			}
			if packagingStudioServerFurniture(sceneElement) && furniture == 0 {
				furniture++
				continue
			}
			return fmt.Errorf("generated slide %q introduced undeclared element %q", slideID, sceneElement.ID)
		}
		if roles["headline"] != 1 {
			return fmt.Errorf("generated slide %q must have exactly one headline hierarchy", slideID)
		}
		if err := validatePackagingStudioGridGeometry(slideID, grid, gridElements); err != nil {
			return err
		}
		primary := roles["headline"] + roles["kicker"] + roles["body"]
		if slideKind == "cover" && (primary > 2 || roles["body"] > 0 || roles["evidence"] > 0 || roles["source"] > 0) {
			return fmt.Errorf("generated cover %q exceeds the sparse cover hierarchy", slideID)
		}
		if oneOf(slideKind, "normal", "close") && (primary > 2 || roles["evidence"] > 1 || roles["source"] > 1 || counts["text"] > 4) {
			return fmt.Errorf("generated slide %q exceeds two primary visible text groups", slideID)
		}
		if slideKind == "evidence" && (roles["body"] != 1 || roles["kicker"] != 0 || roles["evidence"] > 1 || roles["source"] > 1 || counts["text"] > 4) {
			return fmt.Errorf("generated evidence slide %q must have one proof body", slideID)
		}
		meaningful := counts["text"] + counts["image"] + counts["shape"]
		limit := 8
		if slideKind == "cover" {
			limit = 5
		}
		if meaningful > limit || counts["image"] > 2 || counts["shape"] > 3 {
			return fmt.Errorf("generated slide %q exceeds the premium %s element-density limit", slideID, slideKind)
		}
		if err := validatePackagingStudioSceneCollisionAndFit(deck.Slides[slideIndex], source, slideKind); err != nil {
			return err
		}
	}
	if len(usedPlacements) != len(generatedPlacements) {
		return fmt.Errorf("generated scene placed %d of %d exact generated FIGs", len(usedPlacements), len(generatedPlacements))
	}
	return nil
}

func validatePackagingStudioLayoutText(label, slideKind string, layout map[string]any, scene deckElement, expected *packagingStudioLockedLayoutText, palette map[string]bool, typeRoles map[string]string) (string, error) {
	text, err := packagingStudioLayoutString(layout, "text", label, false)
	if err != nil || packagingGeneratedCanonicalText(text) != packagingGeneratedCanonicalText(scene.Text) {
		return "", fmt.Errorf("%s text drifted from the exact generated scene", label)
	}
	role, err := packagingStudioLayoutString(layout, "copy_role", label, false)
	if err != nil || !oneOf(role, "headline", "kicker", "body", "evidence", "source", "counter") {
		return "", fmt.Errorf("%s copy_role is not admitted", label)
	}
	if role == "counter" && !packagingStudioServerFurniture(scene) {
		return "", fmt.Errorf("%s counter is outside the tiny server-furniture allowlist", label)
	}
	if role != "counter" && (expected == nil || expected.Role != role || packagingGeneratedCanonicalText(expected.Text) != packagingGeneratedCanonicalText(text)) {
		return "", fmt.Errorf("%s copy_role/text drifted from locked deck_copy_v3", label)
	}
	typography, ok := layout["typography"].(map[string]any)
	if !ok || packagingStudioLayoutExactKeys(typography, []string{"font_token", "font_family", "font_size", "font_weight", "line_height", "letter_spacing", "alignment", "color"}, label+" typography") != nil {
		return "", fmt.Errorf("%s typography must carry the complete closed typography shape", label)
	}
	typeRole := "body"
	if role == "headline" {
		typeRole = "heading"
	} else if role == "kicker" || role == "evidence" || role == "counter" {
		typeRole = "accent"
	}
	fontToken, err := packagingStudioLayoutString(typography, "font_token", label+" typography", false)
	wantToken := typeRoles[typeRole]
	wantStack, mapped := packagingStudioResolvedFontStack(wantToken)
	if err != nil || !mapped || fontToken != wantToken {
		return "", fmt.Errorf("%s font_token must equal the selected %s token", label, typeRole)
	}
	fontFamily, err := packagingStudioLayoutString(typography, "font_family", label+" typography", false)
	if err != nil || !deckFontPattern.MatchString(fontFamily) || fontFamily != scene.FontFamily || fontFamily != wantStack {
		return "", fmt.Errorf("%s font_family must equal the server-resolved selected %s stack", label, typeRole)
	}
	fontSize, err := packagingStudioLayoutNumber(typography, "font_size", label+" typography")
	if err != nil || math.Abs(fontSize-scene.FontSize) > packagingGeneratedSceneEpsilon {
		return "", fmt.Errorf("%s font_size drifted from the scene", label)
	}
	minimum := 28.0
	if role == "headline" {
		minimum = 52
		if slideKind == "cover" {
			minimum = 72
		}
	} else if role == "source" || role == "counter" {
		minimum = 18
	}
	if fontSize < minimum {
		return "", fmt.Errorf("%s %s type is %.0fpx; minimum is %.0fpx", label, role, fontSize, minimum)
	}
	fontWeight, err := packagingStudioLayoutNumber(typography, "font_weight", label+" typography")
	if err != nil || math.Abs(fontWeight-float64(scene.FontWeight)) > packagingGeneratedSceneEpsilon || fontWeight < 100 || fontWeight > 900 {
		return "", fmt.Errorf("%s font_weight drifted or is invalid", label)
	}
	lineHeight, err := packagingStudioLayoutNumber(typography, "line_height", label+" typography")
	lineHeight, lineHeightOK := packagingGeneratedNormalizedLayoutLineHeight(lineHeight, fontSize)
	if err != nil || !lineHeightOK || math.Abs(lineHeight-scene.LineHeight) > packagingGeneratedSceneEpsilon {
		return "", fmt.Errorf("%s line_height drifted or is invalid", label)
	}
	letterSpacing, err := packagingStudioLayoutString(typography, "letter_spacing", label+" typography", false)
	if err != nil || letterSpacing != scene.LetterSpacing || !deckTrackingPattern.MatchString(letterSpacing) {
		return "", fmt.Errorf("%s letter_spacing drifted or is invalid", label)
	}
	alignment, err := packagingStudioLayoutString(typography, "alignment", label+" typography", false)
	if err != nil || alignment != scene.TextAlign || !oneOf(alignment, "left", "center", "right") {
		return "", fmt.Errorf("%s alignment drifted or is invalid", label)
	}
	color, err := packagingStudioLayoutString(typography, "color", label+" typography", false)
	if err != nil || !deckHexColorPattern.MatchString(color) || !strings.EqualFold(color, scene.Color) || !palette[strings.ToLower(color)] {
		return "", fmt.Errorf("%s color drifted from the selected palette or scene", label)
	}
	claimIDs, err := packagingStudioContractStringArray(layout, "claim_ids", label, 1)
	if err != nil {
		return "", err
	}
	claimRenderings, err := packagingStudioContractStringArray(layout, "claim_renderings", label, 1)
	if err != nil {
		return "", err
	}
	wantIDs, wantRenderings, wantStatement := []string{}, []string{}, ""
	if expected != nil {
		wantIDs, wantRenderings, wantStatement = expected.ClaimIDs, expected.ClaimRenderings, expected.StatementType
	}
	if !slices.Equal(claimIDs, wantIDs) || !slices.Equal(claimRenderings, wantRenderings) {
		return "", fmt.Errorf("%s claim metadata drifted from locked deck_copy_v3", label)
	}
	statement, hasStatement := layout["statement_type"]
	if wantStatement == "" {
		if hasStatement {
			return "", fmt.Errorf("%s introduced undeclared statement_type", label)
		}
	} else {
		if !hasStatement {
			return "", fmt.Errorf("%s omitted locked statement_type", label)
		}
		statementText, err := packagingStudioLayoutString(layout, "statement_type", label, false)
		if err != nil {
			return "", err
		}
		if statementText != wantStatement || statement != statementText {
			return "", fmt.Errorf("%s statement_type drifted from locked deck_copy_v3", label)
		}
	}
	return role, nil
}

func packagingStudioFocalCoordinate(value string) (float64, error) {
	coordinate, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || math.IsNaN(coordinate) || math.IsInf(coordinate, 0) || coordinate < 0 || coordinate > 1 {
		return 0, fmt.Errorf("invalid normalized focal coordinate")
	}
	return coordinate, nil
}

func packagingStudioObjectPosition(value string) (float64, float64, error) {
	parts := strings.Fields(strings.TrimSpace(value))
	if len(parts) != 2 || !strings.HasSuffix(parts[0], "%") || !strings.HasSuffix(parts[1], "%") {
		return 0, 0, fmt.Errorf("object-position must be two percentages")
	}
	x, xerr := strconv.ParseFloat(strings.TrimSuffix(parts[0], "%"), 64)
	y, yerr := strconv.ParseFloat(strings.TrimSuffix(parts[1], "%"), 64)
	if xerr != nil || yerr != nil || math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) || x < 0 || x > 100 || y < 0 || y > 100 {
		return 0, 0, fmt.Errorf("object-position percentages are invalid")
	}
	return x / 100, y / 100, nil
}

func validatePackagingStudioLayoutImage(label string, layout map[string]any, scene deckElement, source packagingGeneratedSceneSource) (int, error) {
	fig, err := packagingStudioLayoutNumber(layout, "fig", label)
	if err != nil || fig < 1 || fig != math.Trunc(fig) {
		return 0, fmt.Errorf("%s fig must be a positive integer", label)
	}
	figNumber := int(fig)
	if source.ImageFig[scene.ID] != strconv.Itoa(figNumber) {
		return 0, fmt.Errorf("%s rendered data-deck-fig drifted from locked FIG %d", label, figNumber)
	}
	fit, err := packagingStudioLayoutString(layout, "fit", label, false)
	if err != nil || !oneOf(fit, "cover", "contain") || fit != scene.Fit || source.ImageFit[scene.ID] != fit {
		return 0, fmt.Errorf("%s fit drifted or is invalid", label)
	}
	if scene.Opacity <= 0 || source.VisuallyHidden[scene.ID] {
		return 0, fmt.Errorf("%s image is not visibly rendered", label)
	}
	crop, err := packagingStudioLayoutString(layout, "crop", label, false)
	if err != nil || !oneOf(crop, "center", "top", "bottom", "left", "right", "faces", "safe_area") {
		return 0, fmt.Errorf("%s crop must be one closed crop token", label)
	}
	if source.ImageCrop[scene.ID] != crop {
		return 0, fmt.Errorf("%s rendered data-deck-crop drifted from locked crop", label)
	}
	focal, ok := layout["focal_point"].(map[string]any)
	if !ok || len(focal) != 2 {
		return 0, fmt.Errorf("%s focal_point must be an exact x/y object", label)
	}
	x, xerr := packagingStudioLayoutNumber(focal, "x", label+" focal_point")
	y, yerr := packagingStudioLayoutNumber(focal, "y", label+" focal_point")
	if xerr != nil || yerr != nil || x < 0 || x > 1 || y < 0 || y > 1 {
		return 0, fmt.Errorf("%s focal_point must stay within normalized bounds", label)
	}
	sourceX, sourceXErr := packagingStudioFocalCoordinate(source.ImageFocalX[scene.ID])
	sourceY, sourceYErr := packagingStudioFocalCoordinate(source.ImageFocalY[scene.ID])
	positionX, positionY, positionErr := packagingStudioObjectPosition(source.ImagePosition[scene.ID])
	if sourceXErr != nil || sourceYErr != nil || positionErr != nil || math.Abs(sourceX-x) > packagingGeneratedSceneEpsilon || math.Abs(sourceY-y) > packagingGeneratedSceneEpsilon || math.Abs(positionX-x) > packagingGeneratedSceneEpsilon || math.Abs(positionY-y) > packagingGeneratedSceneEpsilon {
		return 0, fmt.Errorf("%s rendered focal attributes/object-position drifted from locked focal_point", label)
	}
	return figNumber, nil
}

func validatePackagingStudioLayoutShape(label string, layout map[string]any, scene deckElement, palette map[string]bool) error {
	shape, err := packagingStudioLayoutString(layout, "shape", label, false)
	if err != nil || !oneOf(shape, "rectangle", "ellipse") || shape != scene.Shape {
		return fmt.Errorf("%s shape drifted or is invalid", label)
	}
	fill, err := packagingStudioLayoutString(layout, "fill", label, false)
	if err != nil || (!deckHexColorPattern.MatchString(fill) && fill != "transparent") || !strings.EqualFold(fill, scene.Fill) || (fill != "transparent" && !palette[strings.ToLower(fill)]) {
		return fmt.Errorf("%s fill drifted from the selected palette or scene", label)
	}
	stroke, err := packagingStudioLayoutString(layout, "stroke", label, true)
	if err != nil || (stroke != "" && !deckHexColorPattern.MatchString(stroke)) || !strings.EqualFold(stroke, scene.Stroke) || (stroke != "" && !palette[strings.ToLower(stroke)]) {
		return fmt.Errorf("%s stroke drifted from the selected palette or scene", label)
	}
	strokeWidth, err := packagingStudioLayoutNumber(layout, "stroke_width", label)
	if err != nil || math.Abs(strokeWidth-scene.StrokeWidth) > packagingGeneratedSceneEpsilon || strokeWidth < 0 {
		return fmt.Errorf("%s stroke_width drifted or is invalid", label)
	}
	return nil
}

func packagingStudioBoundsIntersect(a, b packagingGeneratedSceneBounds) bool {
	return a.MinX < b.MaxX-packagingGeneratedSceneEpsilon && a.MaxX > b.MinX+packagingGeneratedSceneEpsilon &&
		a.MinY < b.MaxY-packagingGeneratedSceneEpsilon && a.MaxY > b.MinY+packagingGeneratedSceneEpsilon
}

func validatePackagingStudioSceneCollisionAndFit(slide deckSlide, source packagingGeneratedSceneSource, slideKind string) error {
	var headlineSize float64
	for _, element := range slide.Elements {
		if element.Type != "text" || packagingStudioServerFurniture(element) {
			continue
		}
		if element.FontSize > headlineSize {
			headlineSize = element.FontSize
		}
		tracking, _ := packagingStudioTrackingPixels(element.LetterSpacing, element.FontSize)
		averageAdvance := math.Max(element.FontSize*.35, element.FontSize*.52+tracking)
		charsPerLine := math.Max(1, element.Width/math.Max(averageAdvance, 1))
		textLines := source.TextLines[element.ID]
		if len(textLines) == 0 {
			textLines = []string{element.Text}
		}
		lines := 0.0
		for _, authoredLine := range textLines {
			runes := utf8.RuneCountInString(packagingGeneratedCanonicalText(authoredLine))
			lines += math.Max(1, math.Ceil(float64(runes)/charsPerLine))
		}
		needed := lines * element.FontSize * math.Max(element.LineHeight, 1)
		if needed > element.Height+packagingGeneratedSceneEpsilon {
			return fmt.Errorf("text element %q on slide %q cannot fit its locked box at presentation size", element.ID, slide.ID)
		}
	}
	if slideKind == "cover" && headlineSize < 72 {
		return fmt.Errorf("cover %q has no 72px focal hierarchy", slide.ID)
	}
	for left := 0; left < len(slide.Elements); left++ {
		for right := left + 1; right < len(slide.Elements); right++ {
			a, b := slide.Elements[left], slide.Elements[right]
			if a.Opacity <= 0 || b.Opacity <= 0 || (a.Type != "text" && b.Type != "text") || !packagingStudioBoundsIntersect(packagingGeneratedElementBounds(a), packagingGeneratedElementBounds(b)) {
				continue
			}
			if source.OverlapAllowed[a.ID] && source.OverlapAllowed[b.ID] {
				continue
			}
			return fmt.Errorf("elements %q and %q on slide %q collide without a bilateral overlap declaration", a.ID, b.ID, slide.ID)
		}
	}
	return nil
}

// packagingStudioTrackingPixels is the one closed tracking interpretation used
// by both CSS admission and deterministic fit. Wide tracking materially lowers
// characters-per-line; omitting it lets visually overflowing text pass a plain
// character estimate even when every other native lock is exact.
func packagingStudioTrackingPixels(value string, fontSize float64) (float64, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "normal" {
		return 0, true
	}
	unit := ""
	for _, candidate := range []string{"em", "px"} {
		if strings.HasSuffix(value, candidate) {
			unit = candidate
			value = strings.TrimSuffix(value, candidate)
			break
		}
	}
	if unit == "" {
		return 0, false
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	if unit == "em" {
		if number < -.05 || number > .25 {
			return 0, false
		}
		return number * fontSize, true
	}
	if number < -4 || number > 20 {
		return 0, false
	}
	return number, true
}
