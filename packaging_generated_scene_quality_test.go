package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"
)

func packagingPremiumIdentityTokensForTest() imageryIdentityTokens {
	return imageryIdentityTokens{
		Palette: "background=#101014;foreground=#FFFFFF;accent=#F4EFE5;surface=#111111;muted=#1B3028",
		Type:    "heading=modern_grotesk;body=modern_grotesk;accent=modern_grotesk", Spacing: "airy", Grid: "editorial_12",
		GraphicMotif: "rules", ImageTreatment: "natural_editorial", DataVizTreatment: "direct_labels", Refusals: "gradients,dense_copy",
	}
}

func packagingPremiumLayoutIdentityForTest() map[string]any {
	tokens := packagingPremiumIdentityTokensForTest()
	return map[string]any{
		"selected_candidate_id": "direction_a", "strategy": "balanced_editorial", "visual_system": "editorial_restraint",
		"tokens": map[string]any{
			"palette": tokens.Palette, "type": tokens.Type, "spacing": tokens.Spacing, "grid": tokens.Grid,
			"graphic_motif": tokens.GraphicMotif, "image_treatment": tokens.ImageTreatment,
			"data_viz_treatment": tokens.DataVizTreatment, "refusals": tokens.Refusals,
		},
	}
}

func packagingPremiumStageIdentityAttributesForTest() string {
	tokens := packagingPremiumIdentityTokensForTest()
	return fmt.Sprintf(` data-deck-identity-candidate="direction_a" data-deck-identity-strategy="balanced_editorial" data-deck-identity-system="editorial_restraint" data-deck-identity-palette="%s" data-deck-identity-type="%s" data-deck-identity-spacing="%s" data-deck-identity-grid="%s" data-deck-identity-motif="%s" data-deck-identity-image-treatment="%s" data-deck-identity-data-viz="%s" data-deck-identity-refusals="%s"`, tokens.Palette, tokens.Type, tokens.Spacing, tokens.Grid, tokens.GraphicMotif, tokens.ImageTreatment, tokens.DataVizTreatment, tokens.Refusals)
}

func setPackagingPremiumTestStage(plan *goalPlan, id string, artifact meetingMemoryEntry) {
	stage := plan.subtaskByID(id)
	if stage == nil {
		plan.Subtasks = append(plan.Subtasks, goalSubtask{ID: id})
		stage = &plan.Subtasks[len(plan.Subtasks)-1]
	}
	stage.Status, stage.ArtifactID = subtaskComplete, artifact.ID
	stage.Review = &goalSubtaskReview{Verdict: goalReviewPass, By: "fixture"}
}

func installPackagingPremiumIdentityForTest(t *testing.T, app *kanbanBoardApp, plan *goalPlan, slideIDs []string) imageryDirectionDoc {
	t.Helper()
	tokens := packagingPremiumIdentityTokensForTest()
	second := tokens
	second.Palette = "background=#121212;foreground=#F8F8F8;accent=#D6402D;surface=#242424;muted=#707070"
	candidate := func(id, strategy, visual string, identity imageryIdentityTokens) map[string]any {
		return map[string]any{
			"candidate_id": id, "strategy": strategy, "visual_system": visual,
			"identity": map[string]any{
				"palette": identity.Palette, "type": identity.Type, "spacing": identity.Spacing, "grid": identity.Grid,
				"graphic_motif": identity.GraphicMotif, "image_treatment": identity.ImageTreatment,
				"data_viz_treatment": identity.DataVizTreatment, "refusals": identity.Refusals,
			},
		}
	}
	samples := make([]any, len(slideIDs))
	for index, id := range slideIDs {
		samples[index] = id
	}
	candidateValue := map[string]any{"mode": "develop", "sample_slide_ids": samples, "candidates": []any{
		candidate("direction_a", "balanced_editorial", "editorial_restraint", tokens),
		candidate("direction_b", "typography_first", "graphic_precision", second),
	}}
	candidateRaw, _ := json.Marshal(candidateValue)
	candidateBody := "```json\n" + string(candidateRaw) + "\n```"
	candidateArtifact, _, err := app.createOSArtifactWithMetadata("workflow", "identity candidates", candidateBody, scoutParticipantName, nil)
	if err != nil {
		t.Fatal(err)
	}
	setPackagingPremiumTestStage(plan, "identity_candidates", candidateArtifact)
	reviewValue := map[string]any{
		"sample_slide_ids": samples,
		"assessments": []any{
			map[string]any{"candidate_id": "direction_a", "palette": 9, "contrast": 9, "typography": 9, "spacing": 9, "image_treatment": 9, "graphic_language": 9, "audience_fit": 9, "rationale": "Coherent."},
			map[string]any{"candidate_id": "direction_b", "palette": 8, "contrast": 8, "typography": 8, "spacing": 8, "image_treatment": 8, "graphic_language": 8, "audience_fit": 8, "rationale": "Useful alternative."},
		},
		"ranking": []any{"direction_a", "direction_b"}, "recommended_candidate_id": "direction_a",
	}
	reviewRaw, _ := json.Marshal(reviewValue)
	reviewArtifact, _, err := app.createOSArtifactWithMetadata("workflow", "identity review", "```json\n"+string(reviewRaw)+"\n```", scoutParticipantName, nil)
	if err != nil {
		t.Fatal(err)
	}
	setPackagingPremiumTestStage(plan, "identity_judges", reviewArtifact)
	doc := imageryDirectionDoc{
		SelectedCandidateID: "direction_a", SelectionRationale: "Selected exactly.", Strategy: "balanced_editorial",
		VisualSystem: "editorial_restraint", Identity: tokens, Shots: []imageryDirectionShot{},
	}
	digest, err := packagingStudioSelectedCandidateDigest(app, plan, doc.SelectedCandidateID)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalPackagingStudioIdentityDirection(doc, digest)
	if err != nil {
		t.Fatal(err)
	}
	identityArtifact, _, err := app.createOSArtifactWithMetadata("workflow", "canonical identity", canonical, scoutParticipantName, map[string]string{
		packagingStudioCanonicalIdentityKey: packagingStudioCanonicalIdentityV1, packagingStudioSelectedCandidateKey: digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	setPackagingPremiumTestStage(plan, "identity", identityArtifact)
	imageryArtifact, _, err := app.createOSArtifactWithMetadata("workflow", "imagery", "Imagery — typographic", scoutParticipantName, map[string]string{"imageryShots": "0"})
	if err != nil {
		t.Fatal(err)
	}
	setPackagingPremiumTestStage(plan, "imagery_generate", imageryArtifact)
	return doc
}

func packagingGeneratedSceneTestText(id, text string, x, y, width, height int, extra string) string {
	stack, _ := packagingStudioResolvedFontStack("modern_grotesk")
	return fmt.Sprintf(`<div data-deck-element="%s" data-deck-type="text"%s style="position:absolute;left:%dpx;top:%dpx;width:%dpx;height:%dpx;z-index:2;opacity:1;transform:rotate(0deg);font-size:48px;font-family:%s;font-weight:700;color:#ffffff;text-align:left;line-height:1.05;letter-spacing:normal">%s</div>`, id, extra, x, y, width, height, stack, text)
}

func packagingPremiumDeckCopyForGeneratedSceneTest() map[string]any {
	return map[string]any{
		"slide_count_inference": "",
		"slides": []any{
			map[string]any{
				"slide_id": "cover", "slide_kind": "cover", "thesis": "Locked Cover", "turn": "open",
				"headline": "Locked Cover", "kicker": "A decisive opening", "body": "", "proof": "",
				"evidence_label": "", "source_label": "", "speaker_intent": "Open decisively.",
				"transition": "Move to proof.", "presenter_note": "Open with the decision.",
				"claim_ids": []any{}, "claim_renderings": []any{},
			},
			map[string]any{
				"slide_id": "proof", "slide_kind": "normal", "thesis": "Locked Proof", "turn": "prove",
				"headline": "Locked Proof", "kicker": "", "body": "The market moved", "proof": "",
				"evidence_label": "42%", "source_label": "", "speaker_intent": "Land the proof.",
				"transition": "", "presenter_note": "Land the proof.",
				"claim_ids": []any{strings.Repeat("a", 64)}, "claim_renderings": []any{"42%"},
			},
		},
	}
}

func installPackagingPremiumWriteForGeneratedSceneTest(t *testing.T, app *kanbanBoardApp, plan *goalPlan) meetingMemoryEntry {
	t.Helper()
	raw, err := json.Marshal(packagingPremiumDeckCopyForGeneratedSceneTest())
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePackagingStudioDeckCopyOutput(app, plan, string(raw)); err != nil {
		t.Fatalf("premium generated-scene deck copy fixture: %v", err)
	}
	artifact, _, err := app.createOSArtifactWithMetadata("workflow", "locked copy", string(raw), scoutParticipantName, nil)
	if err != nil {
		t.Fatal(err)
	}
	setPackagingPremiumTestStage(plan, "write", artifact)
	return artifact
}

func packagingGeneratedSceneGoodHTML() string {
	return `<!doctype html><html><head><style>` + packagingDeckChassisCSS + `</style></head><body><div id="stage"` + packagingPremiumStageIdentityAttributesForTest() + `>` +
		`<section class="pg on" data-deck-slide="cover" style="background:#101014">` +
		`<div data-deck-element="cover-bg" data-deck-type="shape" style="position:absolute;left:0px;top:0px;width:1920px;height:1080px;z-index:0;opacity:1;transform:rotate(0deg);background:#101014"></div>` +
		packagingGeneratedSceneTestText("cover-title", "Locked Cover", 120, 120, 1254, 180, "") +
		packagingGeneratedSceneTestText("cover-kicker", "A decisive opening", 120, 360, 970, 60, "") +
		`<div class="notes" hidden>Open with the decision.</div></section>` +
		`<section class="pg" data-deck-slide="proof" style="background:#1b3028">` +
		packagingGeneratedSceneTestText("proof-title", "Locked Proof", 120, 120, 828, 150, "") +
		packagingGeneratedSceneTestText("proof-body", "The market moved", 120, 330, 970, 200, "") +
		packagingGeneratedSceneTestText("proof-number", "42%", 1256, 300, 402, 300, "") +
		`<div class="notes" hidden>Land the proof.</div></section></div></body></html>`
}

func packagingGeneratedPremiumSceneHTML() string {
	source := packagingGeneratedSceneGoodHTML()
	source = strings.Replace(source, `data-deck-element="cover-bg" data-deck-type="shape"`, `data-deck-element="cover-bg" data-deck-type="shape" data-deck-overlap="allow"`, 1)
	source = strings.Replace(source, `data-deck-element="cover-title" data-deck-type="text"`, `data-deck-element="cover-title" data-deck-type="text" data-deck-overlap="allow"`, 1)
	source = strings.Replace(source, `data-deck-element="cover-kicker" data-deck-type="text"`, `data-deck-element="cover-kicker" data-deck-type="text" data-deck-overlap="allow"`, 1)
	source = strings.Replace(source, `height:180px;z-index:2;opacity:1;transform:rotate(0deg);font-size:48px`, `height:180px;z-index:2;opacity:1;transform:rotate(0deg);font-size:72px`, 1)
	source = strings.Replace(source, `height:60px;z-index:2;opacity:1;transform:rotate(0deg);font-size:48px`, `height:60px;z-index:2;opacity:1;transform:rotate(0deg);font-size:28px`, 1)
	source = strings.Replace(source, `height:150px;z-index:2;opacity:1;transform:rotate(0deg);font-size:48px`, `height:150px;z-index:2;opacity:1;transform:rotate(0deg);font-size:52px`, 1)
	source = strings.Replace(source, `height:200px;z-index:2;opacity:1;transform:rotate(0deg);font-size:48px`, `height:200px;z-index:2;opacity:1;transform:rotate(0deg);font-size:28px`, 1)
	return source
}

func packagingGeneratedPremiumLayoutForTest(t *testing.T, source string) string {
	t.Helper()
	artifact := meetingMemoryEntry{Text: source, Metadata: map[string]string{"type": artifactTypeHTMLDeck}}
	deck, imported, quality, err := loadDeckDocument(artifact)
	if err != nil || !imported || quality != "faithful" {
		t.Fatalf("load premium scene: imported=%v quality=%q err=%v", imported, quality, err)
	}
	roles := map[string]string{
		"cover-title": "headline", "cover-kicker": "kicker", "proof-title": "headline",
		"proof-body": "body", "proof-number": "evidence",
	}
	slides := make([]any, 0, len(deck.Slides))
	for index, slide := range deck.Slides {
		kind := "normal"
		if index == 0 {
			kind = "cover"
		}
		elements := make([]any, 0, len(slide.Elements))
		for _, element := range slide.Elements {
			entry := map[string]any{
				"id": element.ID, "type": element.Type, "x": element.X, "y": element.Y,
				"width": element.Width, "height": element.Height, "z": element.Z,
				"opacity": element.Opacity, "rotation": element.Rotation,
			}
			switch element.Type {
			case "text":
				entry["text"], entry["copy_role"] = element.Text, roles[element.ID]
				entry["claim_ids"], entry["claim_renderings"] = []any{}, []any{}
				if element.ID == "proof-number" {
					entry["claim_ids"], entry["claim_renderings"] = []any{strings.Repeat("a", 64)}, []any{"42%"}
				}
				entry["typography"] = map[string]any{
					"font_token": "modern_grotesk", "font_family": element.FontFamily, "font_size": element.FontSize, "font_weight": element.FontWeight,
					"line_height": element.LineHeight, "letter_spacing": element.LetterSpacing,
					"alignment": element.TextAlign, "color": element.Color,
				}
			case "shape":
				entry["shape"], entry["fill"], entry["stroke"], entry["stroke_width"] = element.Shape, element.Fill, element.Stroke, element.StrokeWidth
			}
			elements = append(elements, entry)
		}
		slides = append(slides, map[string]any{
			"slide_id": slide.ID, "slide_kind": kind, "composition": "single focal hierarchy",
			"background": slide.Background, "grid": "editorial_12", "elements": elements,
		})
	}
	raw, err := json.Marshal(map[string]any{"visual_identity": packagingPremiumLayoutIdentityForTest(), "slides": slides})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestPackagingGeneratedSceneQualityGoodVariedScene(t *testing.T) {
	if err := validatePackagingGeneratedScene(nil, nil, packagingGeneratedSceneGoodHTML(), nil); err != nil {
		t.Fatalf("good generated scene: %v", err)
	}
}

func TestPackagingGeneratedSceneQualityAcceptsEquivalentPixelLineHeight(t *testing.T) {
	source := strings.ReplaceAll(packagingGeneratedSceneGoodHTML(), "line-height:1.05", "line-height:50.4px")
	if err := validatePackagingGeneratedPremiumStyles(source); err != nil {
		t.Fatalf("equivalent pixel line-height: %v", err)
	}

	deck, fidelity := importLegacyDeckDocument(meetingMemoryEntry{Text: source})
	if fidelity != "faithful" || len(deck.Slides) == 0 || len(deck.Slides[0].Elements) == 0 {
		t.Fatal("pixel line-height scene did not import")
	}
	lineHeight := 0.0
	for _, element := range deck.Slides[0].Elements {
		if element.Type == "text" {
			lineHeight = element.LineHeight
			break
		}
	}
	if got := lineHeight; got != 1.05 {
		t.Fatalf("normalized line-height=%v, want 1.05", got)
	}

	bad := strings.ReplaceAll(source, "line-height:50.4px", "line-height:120px")
	if err := validatePackagingGeneratedPremiumStyles(bad); err == nil || !strings.Contains(err.Error(), "equivalent pixel leading") {
		t.Fatalf("unsafe pixel line-height error=%v, want closed-range rejection", err)
	}
}

func TestPackagingStudioLayoutShapeAllowsOnlyInvisibleStrokeOmission(t *testing.T) {
	palette := map[string]bool{"#2f5d50": true}
	layout := map[string]any{"shape": "rectangle", "fill": "transparent", "stroke": "#2F5D50", "stroke_width": json.Number("0")}
	scene := deckElement{Type: "shape", Shape: "rectangle", Fill: "transparent"}
	if err := validatePackagingStudioLayoutShape("shape", layout, scene, palette); err != nil {
		t.Fatalf("zero-width omitted scene stroke: %v", err)
	}

	layout["stroke_width"] = json.Number("2")
	if err := validatePackagingStudioLayoutShape("shape", layout, scene, palette); err == nil || !strings.Contains(err.Error(), "stroke_width drifted") {
		t.Fatalf("visible omitted stroke error=%v, want width rejection", err)
	}
	layout["stroke_width"] = json.Number("0")
	scene.Stroke = "#FFFFFF"
	if err := validatePackagingStudioLayoutShape("shape", layout, scene, map[string]bool{"#2f5d50": true, "#ffffff": true}); err == nil || !strings.Contains(err.Error(), "stroke drifted") {
		t.Fatalf("conflicting zero-width stroke error=%v, want color rejection", err)
	}
}

func TestPackagingGeneratedSceneQualityGeometryAndMappingAdversaries(t *testing.T) {
	good := packagingGeneratedSceneGoodHTML()
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "off canvas",
			source: strings.Replace(good, "left:0px;top:0px;width:1920px", "left:-1px;top:0px;width:1920px", 1),
			want:   "outside the 1920x1080 canvas",
		},
		{
			name:   "authored text outside safe zone",
			source: strings.Replace(good, "left:120px;top:120px;width:1254px", "left:80px;top:120px;width:1254px", 1),
			want:   "96px authored-text safe zone",
		},
		{
			name:   "text near collision",
			source: strings.Replace(good, "left:120px;top:360px;width:970px", "left:120px;top:320px;width:970px", 1),
			want:   "lack 24px breathing room",
		},
		{
			name: "one-sided overlap permission",
			source: strings.Replace(
				strings.Replace(good, `data-deck-element="cover-title" data-deck-type="text"`, `data-deck-element="cover-title" data-deck-type="text" data-deck-overlap="allow"`, 1),
				"left:120px;top:360px;width:970px", "left:120px;top:320px;width:970px", 1),
			want: "lack 24px breathing room",
		},
		{
			name:   "visibly empty text",
			source: strings.Replace(good, ">A decisive opening</div>", ">   </div>", 1),
			want:   "visibly empty",
		},
		{
			name:   "transparent text",
			source: strings.Replace(good, "color:#ffffff;text-align:left", "color:transparent;text-align:left", 1),
			want:   "visibly empty",
		},
		{
			name:   "hidden text",
			source: strings.Replace(good, `data-deck-element="cover-kicker" data-deck-type="text"`, `data-deck-element="cover-kicker" data-deck-type="text" hidden`, 1),
			want:   "visibly empty",
		},
		{
			name:   "missing slide mapping",
			source: strings.Replace(good, ` data-deck-slide="proof"`, "", 1),
			want:   "missing data-deck-slide",
		},
		{
			name:   "duplicate slide mapping",
			source: strings.Replace(good, `data-deck-slide="proof"`, `data-deck-slide="cover"`, 1),
			want:   "slide mapping \"cover\" is duplicated",
		},
		{
			name:   "missing element mapping",
			source: strings.Replace(good, ` data-deck-element="proof-body"`, "", 1),
			want:   "not a faithful native import",
		},
		{
			name:   "duplicate element mapping",
			source: strings.Replace(good, `data-deck-element="proof-body"`, `data-deck-element="cover-title"`, 1),
			want:   "element mapping \"cover-title\" is duplicated",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validatePackagingGeneratedScene(nil, nil, test.source, nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want substring %q", err, test.want)
			}
		})
	}
}

func TestPackagingGeneratedSceneQualityPermitsOnlyMutualIntentionalOverlap(t *testing.T) {
	source := packagingGeneratedSceneGoodHTML()
	source = strings.Replace(source, `data-deck-element="cover-title" data-deck-type="text"`, `data-deck-element="cover-title" data-deck-type="text" data-deck-overlap="allow"`, 1)
	source = strings.Replace(source, `data-deck-element="cover-kicker" data-deck-type="text"`, `data-deck-element="cover-kicker" data-deck-type="text" data-deck-overlap="allow"`, 1)
	source = strings.Replace(source, "left:120px;top:360px;width:970px", "left:120px;top:320px;width:970px", 1)
	if err := validatePackagingGeneratedScene(nil, nil, source, nil); err != nil {
		t.Fatalf("mutually permitted intentional overlap: %v", err)
	}
}

func TestPackagingGeneratedSceneQualityAllowsOnlyExplicitBackgroundFurnitureOutsideSafeZone(t *testing.T) {
	source := packagingGeneratedSceneGoodHTML()
	source = strings.Replace(source, `data-deck-element="cover-kicker" data-deck-type="text"`, `data-deck-element="cover-kicker" data-deck-type="text" data-deck-furniture="background" aria-hidden="true"`, 1)
	source = strings.Replace(source, "left:120px;top:360px;width:970px", "left:0px;top:360px;width:970px", 1)
	if err := validatePackagingGeneratedScene(nil, nil, source, nil); err != nil {
		t.Fatalf("explicit background furniture: %v", err)
	}

	missingAria := strings.Replace(source, ` aria-hidden="true"`, "", 1)
	err := validatePackagingGeneratedScene(nil, nil, missingAria, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid background-furniture exception") {
		t.Fatalf("error=%v, want two-factor furniture rejection", err)
	}

	invalidValue := strings.Replace(source, `data-deck-furniture="background"`, `data-deck-furniture="headline"`, 1)
	err = validatePackagingGeneratedScene(nil, nil, invalidValue, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid background-furniture exception") {
		t.Fatalf("error=%v, want closed furniture vocabulary", err)
	}
}

func TestPackagingGeneratedSceneQualityBindsLockedCopy(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	plan := &goalPlan{Objective: "Build the actual 2-slide deck", ProcessID: packagingStudioProcessID, ProcessVersion: 8}
	installPackagingPremiumWriteForGeneratedSceneTest(t, app, plan)
	artifact := meetingMemoryEntry{Text: packagingGeneratedSceneGoodHTML(), Metadata: map[string]string{"type": artifactTypeHTMLDeck}}
	deck, imported, quality, err := loadDeckDocument(artifact)
	if err != nil || !imported || quality != "faithful" {
		t.Fatalf("load deck: imported=%v quality=%q err=%v", imported, quality, err)
	}
	if err := validatePackagingGeneratedSceneLockedCopy(app, plan, deck); err != nil {
		t.Fatalf("scene matching locked copy: %v", err)
	}

	t.Run("missing locked slide", func(t *testing.T) {
		source := strings.Replace(packagingGeneratedSceneGoodHTML(), `<section class="pg" data-deck-slide="proof"`, `<section class="pg" data-deck-slide="other"`, 1)
		artifact.Text = source
		changed, _, _, loadErr := loadDeckDocument(artifact)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		err := validatePackagingGeneratedSceneLockedCopy(app, plan, changed)
		if err == nil || !strings.Contains(err.Error(), "locked deck_copy_v3 maps to \"proof\"") {
			t.Fatalf("error=%v, want locked slide mismatch", err)
		}
	})

	t.Run("locked visible copy changed", func(t *testing.T) {
		source := strings.Replace(packagingGeneratedSceneGoodHTML(), "The market moved", "A different assertion", 1)
		artifact.Text = source
		changed, _, _, loadErr := loadDeckDocument(artifact)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		err := validatePackagingGeneratedSceneLockedCopy(app, plan, changed)
		if err == nil || !strings.Contains(err.Error(), "undeclared visible text") {
			t.Fatalf("error=%v, want locked copy drift", err)
		}
	})

	t.Run("undeclared visible copy added", func(t *testing.T) {
		source := strings.Replace(packagingGeneratedSceneGoodHTML(), "Locked Proof", "Locked Proof plus an invented promise", 1)
		artifact.Text = source
		changed, _, _, loadErr := loadDeckDocument(artifact)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		err := validatePackagingGeneratedSceneLockedCopy(app, plan, changed)
		if err == nil || !strings.Contains(err.Error(), "undeclared visible text") {
			t.Fatalf("error=%v, want undeclared copy rejection", err)
		}
	})
}

func TestPackagingGeneratedSceneQualityRequiresExactPremiumLockedLayout(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	source := packagingGeneratedPremiumSceneHTML()
	plan := &goalPlan{Objective: "Build the actual 2-slide deck", ProcessID: packagingStudioProcessID, ProcessVersion: 8}
	installPackagingPremiumWriteForGeneratedSceneTest(t, app, plan)
	installPackagingPremiumIdentityForTest(t, app, plan, []string{"cover", "proof"})
	layoutBody := packagingGeneratedPremiumLayoutForTest(t, source)
	layoutArtifact, _, err := app.createOSArtifactWithMetadata("artifacts", "locked layout", layoutBody, scoutParticipantName, nil)
	if err != nil {
		t.Fatal(err)
	}
	setPackagingPremiumTestStage(plan, "layout_plan", layoutArtifact)
	if err := validatePackagingGeneratedScene(app, plan, source, nil); err != nil {
		t.Fatalf("scene matching exact premium locked layout: %v", err)
	}
	decoratedSlides := strings.Replace(source, `<section class="pg on" data-deck-slide="cover"`, `<section class="pg on" data-deck-slide="cover" data-deck-type="slide" data-deck-slide-kind="cover"`, 1)
	decoratedSlides = strings.Replace(decoratedSlides, `<section class="pg" data-deck-slide="proof"`, `<section class="pg" data-deck-slide="proof" data-deck-type="slide" data-deck-slide-kind="normal"`, 1)
	if err := validatePackagingGeneratedScene(app, plan, decoratedSlides, nil); err != nil {
		t.Fatalf("closed optional slide metadata: %v", err)
	}
	driftedSlideKind := strings.Replace(decoratedSlides, `data-deck-slide-kind="normal"`, `data-deck-slide-kind="close"`, 1)
	if err := validatePackagingGeneratedScene(app, plan, driftedSlideKind, nil); err == nil || !strings.Contains(err.Error(), "data-deck-slide-kind drifted") {
		t.Fatalf("slide-kind drift error=%v, want locked-layout rejection", err)
	}
	decoratedText := strings.Replace(source, `data-deck-element="cover-title" data-deck-type="text"`, `data-deck-element="cover-title" data-deck-type="text" data-deck-copy-role="headline" data-deck-font-token="modern_grotesk"`, 1)
	if err := validatePackagingGeneratedScene(app, plan, decoratedText, nil); err != nil {
		t.Fatalf("closed optional text metadata: %v", err)
	}
	driftedCopyRole := strings.Replace(decoratedText, `data-deck-copy-role="headline"`, `data-deck-copy-role="body"`, 1)
	if err := validatePackagingGeneratedScene(app, plan, driftedCopyRole, nil); err == nil || !strings.Contains(err.Error(), "data-deck-copy-role drifted") {
		t.Fatalf("copy-role drift error=%v, want locked-layout rejection", err)
	}
	driftedFontToken := strings.Replace(decoratedText, `data-deck-font-token="modern_grotesk"`, `data-deck-font-token="humanist_sans"`, 1)
	if err := validatePackagingGeneratedScene(app, plan, driftedFontToken, nil); err == nil || !strings.Contains(err.Error(), "data-deck-font-token drifted") {
		t.Fatalf("font-token drift error=%v, want locked-layout rejection", err)
	}
	styledChild := strings.Replace(source, ">Locked Proof</div>", `><span style="font-size:8px;color:#ffffff;font-family:Georgia">Locked Proof</span></div>`, 1)
	if err := validatePackagingGeneratedScene(app, plan, styledChild, nil); err == nil || (!strings.Contains(err.Error(), "inline style on an unowned span") && !strings.Contains(err.Error(), "nested or styled text markup")) {
		t.Fatalf("rich-text child bypass error=%v, want premium typography rejection", err)
	}

	authorCascade := strings.Replace(source, "<body>", `<head><style>#stage [data-deck-element]{display:none!important}</style></head><body>`, 1)
	if err := validatePackagingGeneratedScene(app, plan, authorCascade, nil); err == nil || !strings.Contains(err.Error(), "author stylesheet") {
		t.Fatalf("author stylesheet bypass error=%v, want premium cascade rejection", err)
	}
	authorScript := strings.Replace(source, "</head>", `<script>document.querySelector('[data-deck-element="proof-title"]').textContent='INVENTED'</script></head>`, 1)
	if err := validatePackagingGeneratedScene(app, plan, authorScript, nil); err == nil || !strings.Contains(err.Error(), "head may contain only") {
		t.Fatalf("author script bypass error=%v, want inert-shell rejection", err)
	}
	bodyCascade := strings.Replace(source, "<body>", `<body style="opacity:0">`, 1)
	if err := validatePackagingGeneratedScene(app, plan, bodyCascade, nil); err == nil || !strings.Contains(err.Error(), "styles body") {
		t.Fatalf("ancestor inline-style bypass error=%v, want premium cascade rejection", err)
	}
	sectionCascade := strings.Replace(source, `style="background:#1b3028"`, `style="background:#1b3028;opacity:0"`, 1)
	if err := validatePackagingGeneratedScene(app, plan, sectionCascade, nil); err == nil || !strings.Contains(err.Error(), `unowned inline property "opacity"`) {
		t.Fatalf("slide inline-style bypass error=%v, want premium cascade rejection", err)
	}
	wrapperCascade := strings.Replace(source, `<section class="pg" data-deck-slide="proof" style="background:#1b3028">`, `<section class="pg" data-deck-slide="proof" style="background:#1b3028"><div style="opacity:0"></div>`, 1)
	if err := validatePackagingGeneratedScene(app, plan, wrapperCascade, nil); err == nil || !strings.Contains(err.Error(), "inline style on an unowned div node") {
		t.Fatalf("wrapper inline-style bypass error=%v, want premium cascade rejection", err)
	}
	missingChassis := strings.Replace(source, `<style>`+packagingDeckChassisCSS+`</style>`, "", 1)
	if err := validatePackagingGeneratedScene(app, plan, missingChassis, nil); err == nil || !strings.Contains(err.Error(), "exactly one verbatim invariant deck chassis") {
		t.Fatalf("missing chassis error=%v, want exact invariant rejection", err)
	}
	hardBreakOverflow := strings.Replace(source, ">Locked Proof</div>", `>Locked<br><br><br>Proof</div>`, 1)
	if err := validatePackagingGeneratedScene(app, plan, hardBreakOverflow, nil); err == nil || !strings.Contains(err.Error(), "cannot fit its locked box") {
		t.Fatalf("hard-break overflow error=%v, want exact fit rejection", err)
	}
	transformBypass := strings.Replace(source, `transform:rotate(0deg)`, `transform:scale(0) rotate(0deg)`, 1)
	if err := validatePackagingGeneratedScene(app, plan, transformBypass, nil); err == nil || !strings.Contains(err.Error(), "transform must be exactly one") {
		t.Fatalf("transform bypass error=%v, want exact transform rejection", err)
	}
	positionBypass := strings.Replace(source, `data-deck-element="proof-title" data-deck-type="text" style="position:absolute`, `data-deck-element="proof-title" data-deck-type="text" style="position:fixed`, 1)
	if err := validatePackagingGeneratedScene(app, plan, positionBypass, nil); err == nil || !strings.Contains(err.Error(), "position must be absolute") {
		t.Fatalf("position bypass error=%v, want exact position rejection", err)
	}
	trackingBypass := strings.Replace(source, `letter-spacing:normal`, `letter-spacing:100em`, 1)
	if err := validatePackagingGeneratedScene(app, plan, trackingBypass, nil); err == nil || !strings.Contains(err.Error(), "outside the closed tracking range") {
		t.Fatalf("tracking bypass error=%v, want bounded tracking rejection", err)
	}
	shapeFillBypass := strings.Replace(source, `background:#101014`, `fill:#101014`, 1)
	if err := validatePackagingGeneratedScene(app, plan, shapeFillBypass, nil); err == nil || !strings.Contains(err.Error(), `unowned inline property "fill"`) {
		t.Fatalf("html-div fill bypass error=%v, want browser/native shape parity rejection", err)
	}
	furnitureBypass := strings.Replace(source, `data-deck-element="proof-title" data-deck-type="text"`, `data-deck-element="proof-title" data-deck-type="text" data-deck-furniture="full-bleed" aria-hidden="true"`, 1)
	if err := validatePackagingGeneratedScene(app, plan, furnitureBypass, nil); err == nil || !strings.Contains(err.Error(), "may not claim the background-furniture safe-zone exemption") {
		t.Fatalf("locked-copy furniture bypass error=%v, want role-bound safe-zone rejection", err)
	}
	reservedClass := strings.Replace(source, `data-deck-element="proof-title" data-deck-type="text"`, `data-deck-element="proof-title" data-deck-type="text" class="pg"`, 1)
	if err := validatePackagingGeneratedScene(app, plan, reservedClass, nil); err == nil || !strings.Contains(err.Error(), "unowned attribute") {
		t.Fatalf("reserved class error=%v, want closed DOM rejection", err)
	}
	missingOn := strings.Replace(source, `class="pg on"`, `class="pg"`, 1)
	if err := validatePackagingGeneratedScene(app, plan, missingOn, nil); err == nil || !strings.Contains(err.Error(), "invalid chassis classes") {
		t.Fatalf("missing first-slide on class error=%v, want closed DOM rejection", err)
	}
	outerContent := strings.Replace(source, `<body>`, `<body><p>Invented promise</p>`, 1)
	if err := validatePackagingGeneratedScene(app, plan, outerContent, nil); err == nil || !strings.Contains(err.Error(), "body may contain only #stage") {
		t.Fatalf("outer content error=%v, want closed DOM rejection", err)
	}
	hiddenWrapper := strings.Replace(source, `<section class="pg" data-deck-slide="proof" style="background:#1b3028">`, `<section class="pg" data-deck-slide="proof" style="background:#1b3028"><div hidden></div>`, 1)
	if err := validatePackagingGeneratedScene(app, plan, hiddenWrapper, nil); err == nil || !strings.Contains(err.Error(), "unowned div node") {
		t.Fatalf("hidden wrapper error=%v, want closed DOM rejection", err)
	}

	drifted := strings.Replace(source, "left:1256px;top:300px;width:402px", "left:1240px;top:300px;width:402px", 1)
	err = validatePackagingGeneratedScene(app, plan, drifted, nil)
	if err == nil || !strings.Contains(err.Error(), "locked layout requires 1256.00") {
		t.Fatalf("error=%v, want locked geometry drift", err)
	}

	testLayoutMutation := func(name string, mutate func(map[string]any), want string) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal([]byte(layoutBody), &value); err != nil {
				t.Fatal(err)
			}
			mutate(value)
			raw, _ := json.Marshal(value)
			artifact, _, err := app.createOSArtifactWithMetadata("artifacts", "mutated layout", string(raw), scoutParticipantName, nil)
			if err != nil {
				t.Fatal(err)
			}
			plan.subtaskByID("layout_plan").ArtifactID = artifact.ID
			err = validatePackagingGeneratedScene(app, plan, source, nil)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error=%v, want %q", err, want)
			}
			plan.subtaskByID("layout_plan").ArtifactID = layoutArtifact.ID
		})
	}
	element := func(value map[string]any, slide, element int) map[string]any {
		return value["slides"].([]any)[slide].(map[string]any)["elements"].([]any)[element].(map[string]any)
	}
	testLayoutMutation("copy role cannot self-declare", func(value map[string]any) {
		element(value, 1, 1)["copy_role"] = "kicker"
	}, "does not map one-to-one to locked deck_copy_v3")
	testLayoutMutation("claim metadata cannot self-declare", func(value map[string]any) {
		element(value, 1, 2)["claim_ids"] = []any{}
		element(value, 1, 2)["claim_renderings"] = []any{}
	}, "claim metadata drifted from locked deck_copy_v3")
	testLayoutMutation("font token must match selected role", func(value map[string]any) {
		element(value, 0, 1)["typography"].(map[string]any)["font_token"] = "humanist_sans"
	}, "font_token must equal the selected heading token")
	testLayoutMutation("font stack is server resolved", func(value map[string]any) {
		element(value, 0, 1)["typography"].(map[string]any)["font_family"] = "modern_grotesk"
	}, "font_family must equal the server-resolved selected heading stack")
	testLayoutMutation("grid token is used by each slide", func(value map[string]any) {
		value["slides"].([]any)[1].(map[string]any)["grid"] = "modular_8"
	}, "grid must equal the selected identity grid token")
	testLayoutMutation("scene colors come from selected palette", func(value map[string]any) {
		value["slides"].([]any)[1].(map[string]any)["background"] = "#222222"
	}, "background must be one exact selected-palette scene color")
	testLayoutMutation("visual identity is exact selected candidate", func(value map[string]any) {
		value["visual_identity"].(map[string]any)["tokens"].(map[string]any)["grid"] = "modular_12"
	}, "visual_identity drifted from the exact selected canonical identity")

	identityDrift := strings.Replace(source, `data-deck-identity-candidate="direction_a"`, `data-deck-identity-candidate="direction_b"`, 1)
	if err := validatePackagingGeneratedScene(app, plan, identityDrift, nil); err == nil || !strings.Contains(err.Error(), `#stage identity field "candidate" drifted`) {
		t.Fatalf("rendered identity drift error=%v", err)
	}

	var partial map[string]any
	if err := json.Unmarshal([]byte(layoutBody), &partial); err != nil {
		t.Fatal(err)
	}
	first := partial["slides"].([]any)[0].(map[string]any)
	first["elements"] = first["elements"].([]any)[:1]
	partialRaw, _ := json.Marshal(partial)
	partialArtifact, _, err := app.createOSArtifactWithMetadata("artifacts", "partial layout", string(partialRaw), scoutParticipantName, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan.subtaskByID("layout_plan").ArtifactID = partialArtifact.ID
	if err := validatePackagingGeneratedScene(app, plan, source, nil); err == nil || !strings.Contains(err.Error(), "missing locked headline copy") {
		t.Fatalf("partial layout escaped one-to-one validation: %v", err)
	}
}

func TestPackagingGeneratedPremiumImageResetsBrowserFigureMargin(t *testing.T) {
	style := "position:absolute;left:0px;top:0px;width:1920px;height:1080px;z-index:1;opacity:1;transform:rotate(0deg);margin:0;object-fit:cover;object-position:25% 75%"
	node := &xhtml.Node{Type: xhtml.ElementNode, Data: "figure", Attr: []xhtml.Attribute{
		{Key: "data-deck-element", Val: "hero"},
		{Key: "data-deck-type", Val: "image"},
		{Key: "style", Val: style},
	}}
	if err := validatePackagingGeneratedPremiumInlineStyle(node, style); err != nil {
		t.Fatalf("zero-margin image contract: %v", err)
	}
	missing := strings.Replace(style, ";margin:0", "", 1)
	node.Attr[2].Val = missing
	if err := validatePackagingGeneratedPremiumInlineStyle(node, missing); err == nil || !strings.Contains(err.Error(), `missing required inline property "margin"`) {
		t.Fatalf("missing figure reset error=%v", err)
	}
	nonzero := strings.Replace(style, "margin:0", "margin:1px", 1)
	node.Attr[2].Val = nonzero
	if err := validatePackagingGeneratedPremiumInlineStyle(node, nonzero); err == nil || !strings.Contains(err.Error(), "margin must be exactly zero") {
		t.Fatalf("nonzero figure reset error=%v", err)
	}
}

func TestPackagingGeneratedPremiumSceneFailsClosedWithoutMandatoryLocks(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	plan := &goalPlan{Objective: "Build the actual 2-slide deck", ProcessID: packagingStudioProcessID, ProcessVersion: 8}
	if err := validatePackagingGeneratedScene(app, plan, packagingGeneratedPremiumSceneHTML(), nil); err == nil || !strings.Contains(err.Error(), "missing its completed deck_copy_v3 lock") {
		t.Fatalf("missing write lock error=%v", err)
	}
	installPackagingPremiumWriteForGeneratedSceneTest(t, app, plan)
	plan.subtaskByID("write").Status = subtaskPending
	if err := validatePackagingGeneratedScene(app, plan, packagingGeneratedPremiumSceneHTML(), nil); err == nil || !strings.Contains(err.Error(), "deck_copy_v3 lock is not complete") {
		t.Fatalf("incomplete write lock error=%v", err)
	}
	plan.subtaskByID("write").Status = subtaskComplete
	if err := validatePackagingGeneratedScene(app, plan, packagingGeneratedPremiumSceneHTML(), nil); err == nil || !strings.Contains(err.Error(), "missing its completed layout_plan_v3 lock") {
		t.Fatalf("missing layout lock error=%v", err)
	}
}

func TestPackagingStudioLayoutTextBindsRoleClaimsStatementAndResolvedFont(t *testing.T) {
	stack, ok := packagingStudioResolvedFontStack("modern_grotesk")
	if !ok {
		t.Fatal("modern_grotesk has no server font resolution")
	}
	expected := &packagingStudioLockedLayoutText{
		Text: "Recommendation: run the 38% pilot.", Role: "body",
		ClaimIDs: []string{strings.Repeat("a", 64)}, ClaimRenderings: []string{"38%"}, StatementType: "recommendation",
	}
	scene := deckElement{
		ID: "decision-body", Type: "text", Text: expected.Text, FontFamily: stack,
		FontSize: 32, FontWeight: 600, LineHeight: 1.1, LetterSpacing: "normal", TextAlign: "left", Color: "#ffffff",
	}
	build := func() map[string]any {
		return map[string]any{
			"text": expected.Text, "copy_role": "body", "claim_ids": []any{strings.Repeat("a", 64)},
			"claim_renderings": []any{"38%"}, "statement_type": "recommendation",
			"typography": map[string]any{
				"font_token": "modern_grotesk", "font_family": stack, "font_size": json.Number("32"),
				"font_weight": json.Number("600"), "line_height": json.Number("1.1"),
				"letter_spacing": "normal", "alignment": "left", "color": "#ffffff",
			},
		}
	}
	palette := map[string]bool{"#ffffff": true}
	typeRoles := map[string]string{"heading": "modern_grotesk", "body": "modern_grotesk", "accent": "modern_grotesk"}
	if _, err := validatePackagingStudioLayoutText("decision body", "normal", build(), scene, expected, palette, typeRoles); err != nil {
		t.Fatalf("exact role/claim/statement/font binding: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{name: "role", mutate: func(value map[string]any) { value["copy_role"] = "kicker" }, want: "copy_role/text drifted"},
		{name: "claim", mutate: func(value map[string]any) { value["claim_ids"] = []any{strings.Repeat("b", 64)} }, want: "claim metadata drifted"},
		{name: "statement", mutate: func(value map[string]any) { value["statement_type"] = "inference" }, want: "statement_type drifted"},
		{name: "missing statement", mutate: func(value map[string]any) { delete(value, "statement_type") }, want: "omitted locked statement_type"},
		{name: "font token", mutate: func(value map[string]any) { value["typography"].(map[string]any)["font_token"] = "humanist_sans" }, want: "font_token must equal"},
		{name: "font stack", mutate: func(value map[string]any) { value["typography"].(map[string]any)["font_family"] = "modern_grotesk" }, want: "server-resolved"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := build()
			test.mutate(value)
			if _, err := validatePackagingStudioLayoutText("decision body", "normal", value, scene, expected, palette, typeRoles); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestPackagingStudioImageLayoutBindsFigCropAndFocalPoint(t *testing.T) {
	scene := deckElement{ID: "hero", Type: "image", Fit: "cover", Opacity: 1}
	source := packagingGeneratedSceneSource{
		ImageFig: map[string]string{"hero": "1"}, ImageCrop: map[string]string{"hero": "safe_area"},
		ImageFocalX: map[string]string{"hero": "0.25"}, ImageFocalY: map[string]string{"hero": "0.75"},
		ImageFit: map[string]string{"hero": "cover"}, ImagePosition: map[string]string{"hero": "25% 75%"},
	}
	build := func() map[string]any {
		return map[string]any{
			"fig": json.Number("1"), "fit": "cover", "crop": "safe_area",
			"focal_point": map[string]any{"x": json.Number("0.25"), "y": json.Number("0.75")},
		}
	}
	if fig, err := validatePackagingStudioLayoutImage("hero", build(), scene, source); err != nil || fig != 1 {
		t.Fatalf("exact FIG/crop/focal binding: fig=%d err=%v", fig, err)
	}
	invisible := scene
	invisible.Opacity = 0
	if _, err := validatePackagingStudioLayoutImage("hero", build(), invisible, source); err == nil || !strings.Contains(err.Error(), "not visibly rendered") {
		t.Fatalf("zero-opacity image error=%v, want fail closed", err)
	}
	hiddenSource := source
	hiddenSource.VisuallyHidden = map[string]bool{"hero": true}
	if _, err := validatePackagingStudioLayoutImage("hero", build(), scene, hiddenSource); err == nil || !strings.Contains(err.Error(), "not visibly rendered") {
		t.Fatalf("hidden image error=%v, want fail closed", err)
	}
	tests := []struct {
		name   string
		mutate func(map[string]any, packagingGeneratedSceneSource)
		want   string
	}{
		{name: "fig", mutate: func(_ map[string]any, source packagingGeneratedSceneSource) { source.ImageFig["hero"] = "2" }, want: "data-deck-fig drifted"},
		{name: "crop", mutate: func(_ map[string]any, source packagingGeneratedSceneSource) { source.ImageCrop["hero"] = "center" }, want: "data-deck-crop drifted"},
		{name: "fit", mutate: func(_ map[string]any, source packagingGeneratedSceneSource) { source.ImageFit["hero"] = "contain" }, want: "fit drifted"},
		{name: "focal attribute", mutate: func(_ map[string]any, source packagingGeneratedSceneSource) { source.ImageFocalX["hero"] = "0.5" }, want: "focal attributes/object-position drifted"},
		{name: "object position", mutate: func(_ map[string]any, source packagingGeneratedSceneSource) { source.ImagePosition["hero"] = "50% 75%" }, want: "focal attributes/object-position drifted"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			freshSource := packagingGeneratedSceneSource{
				ImageFig: map[string]string{"hero": "1"}, ImageCrop: map[string]string{"hero": "safe_area"},
				ImageFocalX: map[string]string{"hero": "0.25"}, ImageFocalY: map[string]string{"hero": "0.75"},
				ImageFit: map[string]string{"hero": "cover"}, ImagePosition: map[string]string{"hero": "25% 75%"},
			}
			test.mutate(build(), freshSource)
			if _, err := validatePackagingStudioLayoutImage("hero", build(), scene, freshSource); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestPackagingStudioImageryReceiptSemanticsFailClosed(t *testing.T) {
	check := func(t *testing.T, identity imageryDirectionDoc, metadata map[string]string) error {
		t.Helper()
		app := newIsolatedKanbanBoardApp(t)
		artifact, _, err := app.createOSArtifactWithMetadata("workflow", "imagery receipt", "receipt", scoutParticipantName, metadata)
		if err != nil {
			t.Fatal(err)
		}
		plan := &goalPlan{Subtasks: []goalSubtask{{ID: "imagery_generate", Status: subtaskComplete, ArtifactID: artifact.ID}}}
		_, err = packagingStudioGeneratedPlacementsForLayout(app, plan, identity)
		return err
	}
	directed := imageryDirectionDoc{Shots: []imageryDirectionShot{{Fig: 1, SlideID: "cover"}}}
	if err := check(t, imageryDirectionDoc{}, map[string]string{"imageryShots": "0"}); err != nil {
		t.Fatalf("deliberate zero-shot receipt: %v", err)
	}
	if err := check(t, directed, map[string]string{"imageryShots": "skipped"}); err != nil {
		t.Fatalf("directed but provider-skipped receipt: %v", err)
	}
	for name, test := range map[string]struct {
		identity imageryDirectionDoc
		metadata map[string]string
		want     string
	}{
		"zero contradicts direction": {directed, map[string]string{"imageryShots": "0"}, "claims zero shots"},
		"skip without direction":     {imageryDirectionDoc{}, map[string]string{"imageryShots": "skipped"}, "without canonical directed shots"},
		"payload count mismatch":     {directed, map[string]string{"imageryShots": "2", "imageryFigs": `[{}]`}, "count does not match"},
		"empty present payload":      {directed, map[string]string{"imageryShots": "1", "imageryFigs": `[]`}, "count does not match"},
		"noncanonical count":         {directed, map[string]string{"imageryShots": "01", "imageryFigs": `[{}]`}, "invalid generated-shot count"},
	} {
		t.Run(name, func(t *testing.T) {
			err := check(t, test.identity, test.metadata)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestPackagingStudioFontTokensResolveToPortableStacks(t *testing.T) {
	for token, stack := range packagingStudioResolvedFontStacks {
		if stack == token || !deckFontPattern.MatchString(stack) || !strings.Contains(stack, ",") {
			t.Fatalf("token %q resolved to non-portable stack %q", token, stack)
		}
		if resolved, ok := packagingStudioResolvedFontStack(token); !ok || resolved != stack {
			t.Fatalf("token %q did not resolve deterministically", token)
		}
	}
	if _, ok := packagingStudioResolvedFontStack("not_a_font_token"); ok {
		t.Fatal("unknown font token resolved")
	}
}

func TestPackagingStudioGridTokensBindConcreteGeometry(t *testing.T) {
	editorial := []packagingStudioGridElement{{ID: "headline", Type: "text", Role: "headline", X: 120, Y: 140, Width: 1680, Height: 240}}
	if err := validatePackagingStudioGridGeometry("cover", "editorial_12", editorial); err != nil {
		t.Fatalf("editorial grid: %v", err)
	}
	drifted := append([]packagingStudioGridElement(nil), editorial...)
	drifted[0].X = 121
	if err := validatePackagingStudioGridGeometry("cover", "editorial_12", drifted); err == nil || !strings.Contains(err.Error(), "concrete editorial_12") {
		t.Fatalf("editorial label-only bypass error=%v", err)
	}

	modular := []packagingStudioGridElement{{ID: "body", Type: "text", Role: "body", X: 120, Y: 144, Width: 1680, Height: 240}}
	if err := validatePackagingStudioGridGeometry("proof", "modular_12", modular); err != nil {
		t.Fatalf("modular grid: %v", err)
	}
	modular[0].Y = 145
	if err := validatePackagingStudioGridGeometry("proof", "modular_12", modular); err == nil || !strings.Contains(err.Error(), "vertical baseline") {
		t.Fatalf("modular vertical bypass error=%v", err)
	}

	split := []packagingStudioGridElement{
		{ID: "left", Type: "text", Role: "headline", X: 120, Y: 120, Width: 828, Height: 160},
		{ID: "right", Type: "image", X: 972, Y: 120, Width: 828, Height: 720},
	}
	if err := validatePackagingStudioGridGeometry("comparison", "split_6_6", split); err != nil {
		t.Fatalf("split grid: %v", err)
	}
	split[1].X, split[1].Width = 900, 200
	if err := validatePackagingStudioGridGeometry("comparison", "split_6_6", split); err == nil || !strings.Contains(err.Error(), "crosses the concrete") {
		t.Fatalf("split crossing error=%v", err)
	}

	axis := []packagingStudioGridElement{
		{ID: "headline", Type: "text", Role: "headline", X: 160, Y: 120, Width: 1200, Height: 180},
		{ID: "body", Type: "text", Role: "body", X: 160, Y: 360, Width: 900, Height: 180},
	}
	if err := validatePackagingStudioGridGeometry("close", "single_axis", axis); err != nil {
		t.Fatalf("single-axis grid: %v", err)
	}
	axis[1].X = 180
	if err := validatePackagingStudioGridGeometry("close", "single_axis", axis); err == nil || !strings.Contains(err.Error(), "single_axis anchor") {
		t.Fatalf("single-axis drift error=%v", err)
	}
	for _, token := range []string{"editorial_12", "modular_12", "split_6_6", "single_axis"} {
		if !strings.Contains(packagingStudioGridPrompt(), token) {
			t.Fatalf("layout/writer prompt omits concrete grid %q", token)
		}
	}
}

func TestPackagingGeneratedSceneQualityRunsBeforeDraftFiling(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	bad := strings.Replace(packagingGeneratedSceneGoodHTML(), "left:0px;top:0px;width:1920px", "left:-1px;top:0px;width:1920px", 1)
	shipArtifact, _, err := app.createOSArtifactWithMetadata("artifacts", "ship deck", bad, scoutParticipantName, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan := &goalPlan{
		ProcessID: packagingStudioProcessID, ProcessVersion: 8,
		Subtasks: []goalSubtask{{ID: "ship_deck", ArtifactID: shipArtifact.ID, Status: subtaskComplete}},
	}
	_, _, err = compilePackagingStudioDraft(app, plan, "goal-preflight", ProcessStage{})
	if err == nil || !strings.Contains(err.Error(), "generated presentation scene preflight failed") || !strings.Contains(err.Error(), "outside the 1920x1080 canvas") {
		t.Fatalf("compile error=%v, want pre-filing generated scene rejection", err)
	}
	if _, found := app.studioShipDeliverableByContract("goal-preflight", packagingStudioDeckContract); found {
		t.Fatal("bad generated scene was filed before preflight")
	}
}

func TestPackagingGeneratedScenePreflightScope(t *testing.T) {
	if packagingGeneratedScenePreflightRequired(nil) || packagingGeneratedScenePreflightRequired(&goalPlan{ProcessID: "other", ProcessVersion: 5}) || packagingGeneratedScenePreflightRequired(&goalPlan{ProcessID: packagingStudioProcessID, ProcessVersion: 4}) {
		t.Fatal("generated-scene preflight escaped the Packaging Studio v5 boundary")
	}
	if !packagingGeneratedScenePreflightRequired(&goalPlan{ProcessID: packagingStudioProcessID, ProcessVersion: 5}) ||
		!packagingGeneratedScenePreflightRequired(&goalPlan{ProcessID: packagingStudioProcessID, ProcessImplementationRevision: "packaging_studio.runtime.v5"}) {
		t.Fatal("generated-scene preflight did not recognize Packaging Studio v5")
	}
}
