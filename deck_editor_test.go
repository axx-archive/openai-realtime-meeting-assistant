package main

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const faithfulDeckHTML = `<!doctype html><html><body><div id="stage"><section class="pg on" data-deck-slide="slide-1" style="background:#101014"><div data-deck-element="headline" data-deck-type="text" style="position:absolute;left:96px;top:120px;width:1600px;height:220px;z-index:2;opacity:1;transform:rotate(0deg);font-size:104px;font-family:Arial;font-weight:700;color:#ffffff">Like a Farmer</div></section></div></body></html>`

type deckReadOnlyAuthorizer struct{}

func (deckReadOnlyAuthorizer) AuthorizeArtifactHeader(_ context.Context, _ *userAccount, action ACLAction, _ ArtifactAuthorizationHeader) bool {
	return action != ACLWrite
}

type deckReauthAuthorizer struct {
	mu         sync.Mutex
	writeCalls int
}

func (authorizer *deckReauthAuthorizer) AuthorizeArtifactHeader(_ context.Context, _ *userAccount, action ACLAction, _ ArtifactAuthorizationHeader) bool {
	if action != ACLWrite {
		return true
	}
	authorizer.mu.Lock()
	defer authorizer.mu.Unlock()
	authorizer.writeCalls++
	return authorizer.writeCalls == 1
}

func setupDeckEditorHTTPTest(t *testing.T, authorizer ObjectAuthorizer) ([]*http.Cookie, meetingMemoryEntry) {
	t.Helper()
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = authorizer
	t.Cleanup(func() {
		kanbanApp = previousApp
		artifactObjectAuthorizer = previousAuthorizer
	})
	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("design", "Like a Farmer", faithfulDeckHTML, "AJ", map[string]string{
		"type": artifactTypeHTMLDeck, "visibility": "organization", "requestedBy": "aj@shareability.com",
	})
	if err != nil {
		t.Fatalf("create deck artifact: %v", err)
	}
	return loginAs(t, "aj@shareability.com", "B0NFIRE!"), artifact
}

func TestDeckDocumentValidationAndCompile(t *testing.T) {
	setupIsolatedBlobStore(t)
	ref, err := putBlob([]byte("deck image bytes"), "image/png")
	if err != nil {
		t.Fatalf("putBlob: %v", err)
	}
	deck := deckDocument{SchemaVersion: 1, Width: 1920, Height: 1080, Slides: []deckSlide{{
		ID: "slide-1", Background: "#102030", Notes: "Open with the field story [BEAT]", Elements: []deckElement{
			{ID: "background", Type: "shape", X: 0, Y: 0, Width: 1920, Height: 1080, Z: -10, Opacity: .7, Shape: "rectangle", Fill: "#102030"},
			{ID: "headline", Type: "text", X: 96, Y: 96, Width: 900, Height: 240, Z: 2, Opacity: 1, Text: "Farmers < founders", FontSize: 96, FontFamily: "Arial", FontWeight: 700, Color: "#ffffff", TextAlign: "center", LineHeight: 1.05, LetterSpacing: ".04em"},
			{ID: "image-1", Type: "image", X: 1100, Y: 0, Width: 820, Height: 1080, Z: 1, Opacity: 1, Ref: ref, Name: "fig-1.png", Fit: "cover"},
		},
	}}}
	if err := validateDeckDocument(deck, map[string]struct{}{ref: {}}); err != nil {
		t.Fatalf("validateDeckDocument: %v", err)
	}
	html := compileDeckDocumentHTML(deck, "Like a Farmer")
	for _, want := range []string{"width:1920px", "height:1080px", "@page{size:1920px 1080px;margin:0}", ".pg:last-of-type{break-after:auto;page-break-after:auto}", "Farmers &lt; founders", "text-align:center", "line-height:1.05", "letter-spacing:.04em", `<div class="notes" hidden>Open with the field story [BEAT]</div>`, "/artifacts/blob?ref=" + ref} {
		if !strings.Contains(html, want) {
			t.Fatalf("compiled HTML missing %q: %s", want, html)
		}
	}
	if strings.Contains(html, "size:13.333333in 7.5in") {
		t.Fatalf("compiled HTML regressed to a 1280x720 print page that splits a 1920x1080 native slide: %s", html)
	}
	if strings.Contains(html, "data:image") || strings.Contains(strings.ToLower(html), "<script") {
		t.Fatalf("compiled HTML must remain byte-light and script-free: %s", html)
	}

	deck.Slides[0].Elements[2].Ref = strings.Repeat("a", 64)
	if err := validateDeckDocument(deck, map[string]struct{}{ref: {}}); err == nil || !strings.Contains(err.Error(), "not attached") {
		t.Fatalf("unattached image validation error=%v, want fail closed", err)
	}
}

func TestImportLegacyDeckPreservesDataDeckGeometryAndFigAsset(t *testing.T) {
	setupIsolatedBlobStore(t)
	ref, err := putBlob([]byte("generated fig bytes"), "image/png")
	if err != nil {
		t.Fatalf("putBlob: %v", err)
	}
	assets, _ := json.Marshal([]artifactAsset{{Ref: ref, Mime: "image/png", Name: "fig-1.png", Kind: "image"}})
	artifact := meetingMemoryEntry{Metadata: map[string]string{
		"type": artifactTypeHTMLDeck, artifactAssetsMetadataKey: string(assets),
	}, Text: `<!doctype html><html><body><div id="stage"><section class="pg on" data-deck-slide="cover" style="background:#123456">
		<div class="copy" data-deck-element="headline" data-deck-type="text" style="position:absolute;left:96px;top:120px;width:920px;height:220px;z-index:4;opacity:1;transform:rotate(0deg);font-size:104px;font-family:Arial;font-weight:700;color:#ffffff">Like a Farmer</div>
		<div class="image-plate fig-1" data-deck-element="hero-image" data-deck-type="image" style="position:absolute;left:1080px;top:0;width:840px;height:1080px;z-index:2;opacity:1;transform:rotate(0deg);object-fit:cover"><div class="ph"></div></div>
	</section></div></body></html>`}

	deck, imported, quality, err := loadDeckDocument(artifact)
	if err != nil {
		t.Fatalf("loadDeckDocument: %v", err)
	}
	if !imported || quality != "faithful" {
		t.Fatalf("imported=%v quality=%q, want faithful import", imported, quality)
	}
	if len(deck.Slides) != 1 || len(deck.Slides[0].Elements) != 2 {
		t.Fatalf("deck=%+v, want two faithful elements", deck)
	}
	image := deck.Slides[0].Elements[1]
	if image.Type != "image" || image.Ref != ref || image.Name != "fig-1.png" || image.X != 1080 || image.Width != 840 || image.Height != 1080 {
		t.Fatalf("image=%+v, want exact fig asset and geometry", image)
	}
}

func TestImportLegacyDeckPreservesSafeRichTextAndEmptyGeneratedImageSlot(t *testing.T) {
	artifact := meetingMemoryEntry{Metadata: map[string]string{"type": artifactTypeHTMLDeck}, Text: `<!doctype html><html><head><style>:root{--display:Georgia,"Times New Roman",serif}.serif{font-family:var(--display)}</style></head><body><div id="stage"><section class="pg on" data-deck-slide="cover" style="background:#17211C">
		<figure class="image-plate bleed fig-1" data-deck-element="hero-image" data-deck-type="image" style="position:absolute;left:0;top:0;width:1920px;height:1080px;z-index:1;opacity:1;transform:rotate(0deg);object-fit:cover;margin:0"><div class="ph"></div><figcaption data-deck-element="caption" data-deck-type="text" style="position:absolute;left:96px;top:1010px;width:1200px;height:30px;z-index:5;opacity:1;color:#ffffff;font-size:18px;font-family:Arial;font-weight:600">Rights-safe replacement slot.</figcaption></figure>
		<div data-deck-element="score" data-deck-type="text" style="position:absolute;left:96px;top:120px;width:700px;height:300px;z-index:4;opacity:1;color:#ffffff;font-size:24px;font-family:Arial;font-weight:700">OBSERVED <span class="serif" style="display:block;color:#C79B4D;font-size:75px;line-height:.92;margin:9px 0">6.1M</span><span style="color:#B84F32">YouTube</span></div>
	</section></div></body></html>`}

	deck, imported, quality, err := loadDeckDocument(artifact)
	if err != nil {
		t.Fatalf("loadDeckDocument: %v", err)
	}
	if !imported || quality != "faithful" || len(deck.Slides) != 1 || len(deck.Slides[0].Elements) != 3 {
		t.Fatalf("imported=%v quality=%q deck=%+v, want faithful three-element scene", imported, quality, deck)
	}
	var placeholder, score deckElement
	for _, element := range deck.Slides[0].Elements {
		switch element.ID {
		case "hero-image":
			placeholder = element
		case "score":
			score = element
		}
	}
	if placeholder.Type != "shape" || placeholder.Fill != "transparent" || placeholder.Width != 1920 || placeholder.Height != 1080 {
		t.Fatalf("placeholder=%+v, want lossless transparent full-bleed slot", placeholder)
	}
	for _, want := range []string{"<span", "font-family:Georgia,Times New Roman,serif", "font-size:75px", "margin:9px 0", "6.1M", "YouTube"} {
		if !strings.Contains(score.RichText, want) {
			t.Fatalf("richText=%q missing %q", score.RichText, want)
		}
	}
	if err := validateDeckDocument(deck, nil); err != nil {
		t.Fatalf("validate imported rich scene: %v", err)
	}
	compiled := compileDeckDocumentHTML(deck, "Working Fieldbook")
	if !strings.Contains(compiled, score.RichText) || strings.Contains(compiled, `class="serif"`) {
		t.Fatalf("compiled rich text did not remain explicit and self-contained: %s", compiled)
	}

	deck.Slides[0].Elements[2].RichText = `<span style="position:absolute">unsafe</span>`
	if err := validateDeckDocument(deck, nil); err == nil || !strings.Contains(err.Error(), "text element styling") {
		t.Fatalf("unsafe rich text validation error=%v, want fail closed", err)
	}
}

func TestImportLegacyDeckPreservesInheritedTypographyAndTracking(t *testing.T) {
	source := `<!doctype html><html><head><style>
	:root{--display:Georgia,"Times New Roman",serif;--body:"Arial Narrow","Aptos Narrow","Helvetica Neue",Arial,sans-serif}
	html,body{font-family:var(--body)} .pg{font-family:var(--body)} .serif{font-family:var(--display)} .kicker{letter-spacing:.15em}
	</style></head><body><div id="stage"><section class="pg on" data-deck-slide="slide-1" style="background:#101014">
	<div class="serif" data-deck-element="headline" data-deck-type="text" style="position:absolute;left:96px;top:120px;width:1200px;height:220px;z-index:2;opacity:1;font-size:104px;font-weight:700;color:#ffffff;letter-spacing:.04em">Like a Farmer</div>
	<div class="kicker" data-deck-element="label" data-deck-type="text" style="position:absolute;left:96px;top:420px;width:800px;height:80px;z-index:2;opacity:1;font-size:28px;font-weight:700;color:#ffffff">Working Fieldbook</div>
	</section></div></body></html>`
	artifact := meetingMemoryEntry{Metadata: map[string]string{"type": artifactTypeHTMLDeck}, Text: source}
	deck, imported, quality, err := loadDeckDocument(artifact)
	if err != nil {
		t.Fatalf("loadDeckDocument: %v", err)
	}
	if !imported || quality != "faithful" || len(deck.Slides) != 1 || len(deck.Slides[0].Elements) != 2 {
		t.Fatalf("imported=%v quality=%q deck=%+v", imported, quality, deck)
	}
	headline, label := deck.Slides[0].Elements[0], deck.Slides[0].Elements[1]
	if headline.FontFamily != "Georgia,Times New Roman,serif" || headline.LetterSpacing != ".04em" {
		t.Fatalf("headline typography=%+v, want inherited serif stack and inline tracking", headline)
	}
	if label.FontFamily != "Arial Narrow,Aptos Narrow,Helvetica Neue,Arial,sans-serif" || label.LetterSpacing != ".15em" {
		t.Fatalf("label typography=%+v, want inherited body stack and class tracking", label)
	}
	compiled := compileDeckDocumentHTML(deck, "Typography roundtrip")
	if !strings.Contains(compiled, "font-family:Georgia,Times New Roman,serif") || !strings.Contains(compiled, "letter-spacing:.15em") {
		t.Fatalf("compiled deck dropped inherited typography: %s", compiled)
	}
	scene, err := json.Marshal(deck)
	if err != nil {
		t.Fatalf("marshal native scene: %v", err)
	}
	var roundtrip deckDocument
	if err := json.Unmarshal(scene, &roundtrip); err != nil || roundtrip.Slides[0].Elements[0].FontFamily != headline.FontFamily || roundtrip.Slides[0].Elements[1].LetterSpacing != label.LetterSpacing {
		t.Fatalf("typography scene roundtrip err=%v deck=%+v", err, roundtrip)
	}
}

func TestImportLegacyDeckIgnoresNestedSemanticSections(t *testing.T) {
	artifact := meetingMemoryEntry{Metadata: map[string]string{"type": artifactTypeHTMLDeck}, Text: `<!doctype html><html><body><div id="stage">
		<section class="pg on" data-deck-slide="one"><div data-deck-element="one-title" data-deck-type="text" style="position:absolute;left:96px;top:96px;width:900px;height:140px;z-index:2;opacity:1;font-size:72px;color:#fff"><section>Opening</section></div></section>
		<section class="pg" data-deck-slide="two"><div data-deck-element="two-title" data-deck-type="text" style="position:absolute;left:96px;top:96px;width:900px;height:140px;z-index:2;opacity:1;font-size:72px;color:#fff">Close</div></section>
	</div></body></html>`}
	deck, _, _, err := loadDeckDocument(artifact)
	if err != nil {
		t.Fatalf("loadDeckDocument: %v", err)
	}
	if len(deck.Slides) != 2 {
		t.Fatalf("imported %d slides, want only the two stage children", len(deck.Slides))
	}
}

func TestImportUnknownLegacyDeckIsExplicitlyApproximate(t *testing.T) {
	artifact := meetingMemoryEntry{Metadata: map[string]string{"type": artifactTypeHTMLDeck}, Text: `<!doctype html><section><h1>Outline only</h1><p>No editor contract.</p></section>`}
	deck, imported, quality, err := loadDeckDocument(artifact)
	if err != nil {
		t.Fatalf("loadDeckDocument: %v", err)
	}
	if !imported || quality != "approximate" || len(deck.Slides[0].Elements) == 0 {
		t.Fatalf("imported=%v quality=%q deck=%+v", imported, quality, deck)
	}
}

func TestArtifactRenderBodyExpandsAttachedDeckImagesAndFailsClosed(t *testing.T) {
	setupIsolatedBlobStore(t)
	ref, err := putBlob([]byte("png bytes"), "image/png")
	if err != nil {
		t.Fatalf("putBlob: %v", err)
	}
	deck := deckDocument{SchemaVersion: 1, Width: 1920, Height: 1080, Slides: []deckSlide{
		{ID: "slide-1", Background: "#101014", Elements: []deckElement{{ID: "hero", Type: "image", X: 0, Y: 0, Width: 1920, Height: 1080, Z: 1, Opacity: 1, Ref: ref, Name: "hero.png", Fit: "cover"}}},
		{ID: "slide-2", Background: "#ffffff", Elements: []deckElement{{ID: "close", Type: "text", X: 120, Y: 120, Width: 1680, Height: 200, Z: 1, Opacity: 1, Text: "Thank you", FontSize: 96, FontFamily: "Arial", FontWeight: 700, Color: "#000000"}}},
	}}
	scene, _ := json.Marshal(deck)
	sceneRef, err := putBlob(scene, "application/vnd.bonfire.deck+json")
	if err != nil {
		t.Fatalf("put scene: %v", err)
	}
	assets, _ := json.Marshal([]artifactAsset{{Ref: ref, Mime: "image/png", Name: "hero.png", Kind: "image"}})
	artifact := meetingMemoryEntry{Metadata: map[string]string{
		deckSceneRefMetadataKey: sceneRef, artifactAssetsMetadataKey: string(assets), "title": "Two-slide deck",
	}, Text: `<!doctype html><html><body><img src="https://attacker.example/not-authoritative.png"></body></html>`}
	body, err := artifactRenderBody(artifact)
	if err != nil {
		t.Fatalf("artifactRenderBody: %v", err)
	}
	if got := string(body); !strings.Contains(got, "data:image/png;base64,") || strings.Contains(got, "/artifacts/blob") || strings.Contains(got, "attacker.example") || !strings.Contains(got, `data-slide-id="slide-2"`) || !strings.Contains(got, `data-deck-navigation="trusted"`) || !strings.Contains(got, "ArrowRight") {
		t.Fatalf("expanded body=%s, want authoritative two-slide scene with trusted navigation", got)
	}

	unattached := artifact
	unattached.Metadata = map[string]string{deckSceneRefMetadataKey: sceneRef}
	if _, err := artifactRenderBody(unattached); err == nil {
		t.Fatal("unattached image rendered; want fail closed")
	}

	largeRef, err := putBlob(make([]byte, 13<<20), "image/png")
	if err != nil {
		t.Fatalf("put large blob: %v", err)
	}
	largeDeck := deckDocument{SchemaVersion: 1, Width: 1920, Height: 1080, Slides: []deckSlide{{ID: "large", Elements: []deckElement{}}}}
	for index := 0; index < 4; index++ {
		largeDeck.Slides[0].Elements = append(largeDeck.Slides[0].Elements, deckElement{ID: "large-" + strconv.Itoa(index), Type: "image", X: 0, Y: 0, Width: 1920, Height: 1080, Z: index, Opacity: 1, Ref: largeRef, Name: "large.png", Fit: "cover"})
	}
	largeScene, _ := json.Marshal(largeDeck)
	largeSceneRef, err := putBlob(largeScene, "application/vnd.bonfire.deck+json")
	if err != nil {
		t.Fatalf("put large scene: %v", err)
	}
	largeAssets, _ := json.Marshal([]artifactAsset{{Ref: largeRef, Mime: "image/png", Name: "large.png", Kind: "image"}})
	large := meetingMemoryEntry{Metadata: map[string]string{
		deckSceneRefMetadataKey: largeSceneRef, artifactAssetsMetadataKey: string(largeAssets),
	}}
	if _, err := artifactRenderBody(large); err == nil || !strings.Contains(err.Error(), "render bound") {
		t.Fatalf("oversized repeated images error=%v, want bounded expansion", err)
	}
}

func TestRenderedPageAssetsAreNotEditableDeckImagery(t *testing.T) {
	for _, testCase := range []artifactAsset{
		{Ref: strings.Repeat("a", 64), Mime: "image/jpeg", Name: "page-01.jpg", Kind: "page_image"},
		{Ref: strings.Repeat("b", 64), Mime: "image/jpeg", Name: "page-02.jpg", Kind: "image"},
	} {
		assets, _ := json.Marshal([]artifactAsset{testCase})
		artifact := meetingMemoryEntry{Metadata: map[string]string{artifactAssetsMetadataKey: string(assets)}}
		if _, allowed := artifactAssetRefSet(artifact)[testCase.Ref]; allowed {
			t.Fatalf("rendered page %q entered the editable image palette", testCase.Name)
		}
		if artifactAssetIsEditableImage(testCase) {
			t.Fatalf("rendered page %q classified as editable imagery", testCase.Name)
		}
	}
	generated := artifactAsset{Ref: strings.Repeat("c", 64), Mime: "image/png", Name: "fig-01.png", Kind: "image"}
	if !artifactAssetIsEditableImage(generated) {
		t.Fatal("generated deck imagery was excluded with rendered pages")
	}
}

func TestArtifactRenderHandlerServesTrustedTwoSlideNavigation(t *testing.T) {
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	text := func(id, value string) deckElement {
		return deckElement{ID: id, Type: "text", X: 120, Y: 120, Width: 1680, Height: 240, Z: 1, Opacity: 1, Text: value, FontSize: 96, FontFamily: "Arial", FontWeight: 700, Color: "#ffffff"}
	}
	deck := deckDocument{SchemaVersion: 1, Width: 1920, Height: 1080, Slides: []deckSlide{
		{ID: "opening", Background: "#101014", Elements: []deckElement{text("opening-title", "Opening")}},
		{ID: "closing", Background: "#101014", Elements: []deckElement{text("closing-title", "Closing")}},
	}}
	scene, _ := json.Marshal(deck)
	ref, err := putBlob(scene, "application/vnd.bonfire.deck+json")
	if err != nil {
		t.Fatalf("put scene: %v", err)
	}
	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("design", "Two slides", "untrusted stored body", "AJ", map[string]string{
		"type": artifactTypeHTMLDeck, deckSceneRefMetadataKey: ref, deckSchemaMetadataKey: "1",
	})
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	token := mintArtifactRenderTokenForArtifact(artifact, time.Now().Add(time.Minute))
	recorder := httptest.NewRecorder()
	artifactRenderHandler(recorder, httptest.NewRequest(http.MethodGet, "/artifacts/render?id="+artifact.ID+"&t="+token, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("render status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	got := recorder.Body.String()
	for _, want := range []string{`data-slide-id="opening"`, `data-slide-id="closing"`, `data-deck-navigation="trusted"`, "data-deck-next", "ArrowRight", "scale("} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered two-slide body missing %q: %s", want, got)
		}
	}
	if gotCSP := recorder.Header().Get("Content-Security-Policy"); gotCSP != artifactRenderCSP {
		t.Fatalf("CSP=%q, want pinned policy", gotCSP)
	}
}

func TestMixedLegacyDeckIsApproximateAndMutationIsRefused(t *testing.T) {
	cookies, artifact := setupDeckEditorHTTPTest(t, LegacyCompatibleObjectAuthorizer{})
	mixed := strings.Replace(faithfulDeckHTML, "</section>", `<p>This unmarked evidence would be lost.</p></section>`, 1)
	updated, _, err := kanbanApp.updateOSArtifactWithMetadata(artifact.ID, "", mixed, "AJ", nil)
	if err != nil {
		t.Fatalf("stamp mixed deck: %v", err)
	}
	get := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/deck?id="+updated.ID, "", cookies, deckEditorHandler)
	if get.Code != http.StatusOK {
		t.Fatalf("GET mixed deck status=%d body=%s", get.Code, get.Body.String())
	}
	var payload struct {
		CanWrite      bool         `json:"canWrite"`
		ImportQuality string       `json:"importQuality"`
		Deck          deckDocument `json:"deck"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ImportQuality != "approximate" || payload.CanWrite {
		t.Fatalf("mixed import quality=%q canWrite=%v, want approximate and mutation blocked", payload.ImportQuality, payload.CanWrite)
	}
	body, _ := json.Marshal(map[string]any{"artifactId": updated.ID, "expectedVersion": artifactVersion(updated), "deck": payload.Deck})
	patch := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/deck", string(body), cookies, deckEditorHandler)
	if patch.Code != http.StatusConflict || !strings.Contains(patch.Body.String(), "without losing") {
		t.Fatalf("mixed PATCH status=%d body=%s, want loss-prevention conflict", patch.Code, patch.Body.String())
	}
}

func TestLegacyPresenterChromeIsIgnoredButSlideNotesRemainEditable(t *testing.T) {
	legacy := strings.Replace(faithfulDeckHTML, "</body>", "<button id=\"prompt\">Present</button><aside id=\"railwrap\">Presenter script</aside><script>const slides=[1]; const notes=[`Founder narration [BEAT] Keep the shift line exact.`]; const escapeHTML=value=>value.replace(/[&<>\"']/g,function(ch){return ch;}); addEventListener(\"keydown\",event=>present(notes,event));</script></body>", 1)
	artifact := meetingMemoryEntry{Metadata: map[string]string{"type": artifactTypeHTMLDeck}, Text: legacy}
	deck, imported, quality, err := loadDeckDocument(artifact)
	if err != nil {
		t.Fatalf("loadDeckDocument: %v", err)
	}
	if !imported || quality != "faithful" {
		t.Fatalf("imported=%v quality=%q, want faithful presenter-chrome import", imported, quality)
	}
	if got := deck.Slides[0].Notes; got != "Founder narration [BEAT] Keep the shift line exact." {
		t.Fatalf("notes=%q, want preserved presenter script", got)
	}
	compiled := compileDeckDocumentHTML(deck, "Like a Farmer")
	if !strings.Contains(compiled, `<div class="notes" hidden>Founder narration [BEAT] Keep the shift line exact.</div>`) || strings.Contains(compiled, `id="prompt"`) || strings.Contains(compiled, "const VOICE") {
		t.Fatalf("compiled native deck did not preserve notes while dropping outer presenter chrome: %s", compiled)
	}
}

func TestDeckEndpointTreatsStaleMarkdownStampOnHTMLDeckAsEditableDeck(t *testing.T) {
	cookies, artifact := setupDeckEditorHTTPTest(t, LegacyCompatibleObjectAuthorizer{})
	updated, _, err := kanbanApp.updateOSArtifactWithMetadata(artifact.ID, "", faithfulDeckHTML, "AJ", map[string]string{"type": artifactTypeMarkdown})
	if err != nil {
		t.Fatalf("stamp legacy markdown metadata: %v", err)
	}
	if artifactType(updated) != artifactTypeMarkdown || !artifactIsDeckEditorDocument(updated) {
		t.Fatalf("artifactType=%q deckEditorDocument=%v, want scoped editor override for stale markdown stamp", artifactType(updated), artifactIsDeckEditorDocument(updated))
	}
	request := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/deck?id="+updated.ID, "", cookies, deckEditorHandler)
	if request.Code != http.StatusOK {
		t.Fatalf("GET stale-stamped deck status=%d body=%s", request.Code, request.Body.String())
	}
	var payload struct {
		CanWrite      bool   `json:"canWrite"`
		ImportQuality string `json:"importQuality"`
	}
	if err := json.Unmarshal(request.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode deck response: %v", err)
	}
	if !payload.CanWrite || payload.ImportQuality != "faithful" {
		t.Fatalf("payload=%+v, want editable faithful deck", payload)
	}
}

func TestLegacyBehaviorAndUnrepresentedVisualsStayReadOnlyWithoutLoss(t *testing.T) {
	cases := []struct {
		name string
		html string
	}{
		{
			name: "unknown presenter script outside the slide",
			html: strings.Replace(faithfulDeckHTML, "</body>", `<script>const VOICE={"slide-1":"Founder narration"};addEventListener("keydown",event=>present(VOICE,event));</script></body>`, 1),
		},
		{
			name: "unknown behavior before a valid notes array",
			html: strings.Replace(faithfulDeckHTML, "</body>", `<script>runImportantChart(); const slides=[1]; const notes=["n"]; addEventListener("keydown",event=>present(notes,event));</script></body>`, 1),
		},
		{
			name: "unknown behavior after a valid notes array",
			html: strings.Replace(faithfulDeckHTML, "</body>", `<script>const slides=[1]; const notes=["n"]; addEventListener("keydown",event=>present(notes,event)); mutateDeck();</script></body>`, 1),
		},
		{
			name: "notes marker only inside a string",
			html: strings.Replace(faithfulDeckHTML, "</body>", `<script>const decoy="const notes=['n']"; mutateDeck();</script></body>`, 1),
		},
		{
			name: "interactive behavior inside a slide",
			html: strings.Replace(faithfulDeckHTML, "</section>", `<button onclick="advance()">Advance</button><script>advance()</script></section>`, 1),
		},
		{
			name: "overlong presenter notes",
			html: strings.Replace(faithfulDeckHTML, "</section>", `<div class="notes">`+strings.Repeat("n", deckSlideNotesMaxRunes+1)+`</div></section>`, 1),
		},
		{
			name: "unmarked visual-only shape",
			html: strings.Replace(faithfulDeckHTML, "</section>", `<div class="accent-orb" style="position:absolute;left:1450px;top:80px;width:240px;height:240px;background:#ff6633;border:8px solid #ffffff;border-radius:50%"></div></section>`, 1),
		},
		{
			name: "stylesheet-only visual shape",
			html: strings.Replace(strings.Replace(faithfulDeckHTML, "</body>", `<style>.accent-orb{position:absolute;width:240px;height:240px;background:#ff6633;border:8px solid #fff}</style></body>`, 1), "</section>", `<div class="accent-orb"></div></section>`, 1),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cookies, artifact := setupDeckEditorHTTPTest(t, LegacyCompatibleObjectAuthorizer{})
			updated, _, err := kanbanApp.updateOSArtifactWithMetadata(artifact.ID, "", testCase.html, "AJ", nil)
			if err != nil {
				t.Fatalf("stamp unsupported legacy deck: %v", err)
			}
			get := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/deck?id="+updated.ID, "", cookies, deckEditorHandler)
			if get.Code != http.StatusOK {
				t.Fatalf("GET status=%d body=%s", get.Code, get.Body.String())
			}
			var payload struct {
				CanWrite      bool         `json:"canWrite"`
				ImportQuality string       `json:"importQuality"`
				Deck          deckDocument `json:"deck"`
			}
			if err := json.Unmarshal(get.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.ImportQuality != "approximate" || payload.CanWrite {
				t.Fatalf("quality=%q canWrite=%v, want read-only approximate", payload.ImportQuality, payload.CanWrite)
			}
			patchBody, _ := json.Marshal(map[string]any{"artifactId": updated.ID, "expectedVersion": artifactVersion(updated), "deck": payload.Deck})
			patch := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/deck", string(patchBody), cookies, deckEditorHandler)
			if patch.Code != http.StatusConflict {
				t.Fatalf("PATCH status=%d body=%s, want loss-prevention conflict", patch.Code, patch.Body.String())
			}
			current, ok := kanbanApp.osArtifactByID(updated.ID)
			if !ok || current.Text != testCase.html || current.Metadata[deckSceneRefMetadataKey] != "" || artifactVersion(current) != artifactVersion(updated) {
				t.Fatalf("read-only roundtrip changed legacy artifact: %+v", current)
			}
			rendered, err := artifactRenderBody(current)
			if err != nil || string(rendered) != testCase.html {
				t.Fatalf("read-only render did not preserve original behavior/visual body: err=%v body=%s", err, rendered)
			}
		})
	}
}

func TestDeckEditorGETCanWriteAndPATCHCASAtHTTPBoundary(t *testing.T) {
	t.Run("read-only capability is authoritative", func(t *testing.T) {
		cookies, artifact := setupDeckEditorHTTPTest(t, deckReadOnlyAuthorizer{})
		get := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/deck?id="+artifact.ID, "", cookies, deckEditorHandler)
		if get.Code != http.StatusOK {
			t.Fatalf("GET status=%d body=%s", get.Code, get.Body.String())
		}
		var payload struct {
			CanWrite bool         `json:"canWrite"`
			Deck     deckDocument `json:"deck"`
		}
		if err := json.Unmarshal(get.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.CanWrite {
			t.Fatal("GET canWrite=true for a read-only principal")
		}
		body, _ := json.Marshal(map[string]any{"artifactId": artifact.ID, "expectedVersion": artifactVersion(artifact), "deck": payload.Deck})
		patch := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/deck", string(body), cookies, deckEditorHandler)
		if patch.Code != http.StatusNotFound {
			t.Fatalf("read-only PATCH status=%d body=%s, want non-oracular 404", patch.Code, patch.Body.String())
		}
	})

	t.Run("write succeeds once and stale revision conflicts", func(t *testing.T) {
		cookies, artifact := setupDeckEditorHTTPTest(t, LegacyCompatibleObjectAuthorizer{})
		get := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/deck?id="+artifact.ID, "", cookies, deckEditorHandler)
		var payload struct {
			CanWrite bool         `json:"canWrite"`
			Deck     deckDocument `json:"deck"`
		}
		if get.Code != http.StatusOK || json.Unmarshal(get.Body.Bytes(), &payload) != nil || !payload.CanWrite {
			t.Fatalf("writable GET status=%d body=%s", get.Code, get.Body.String())
		}
		payload.Deck.Slides[0].Elements[0].Text = "Saved once"
		body, _ := json.Marshal(map[string]any{"artifactId": artifact.ID, "expectedVersion": artifactVersion(artifact), "deck": payload.Deck})
		first := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/deck", string(body), cookies, deckEditorHandler)
		if first.Code != http.StatusOK {
			t.Fatalf("first PATCH status=%d body=%s", first.Code, first.Body.String())
		}
		var saved struct {
			Artifact deckArtifactView `json:"artifact"`
		}
		if json.Unmarshal(first.Body.Bytes(), &saved) != nil || saved.Artifact.Version <= artifactVersion(artifact) {
			t.Fatalf("saved response=%s, want authoritative bumped version", first.Body.String())
		}
		stale := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/deck", string(body), cookies, deckEditorHandler)
		if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "currentVersion") {
			t.Fatalf("stale PATCH status=%d body=%s", stale.Code, stale.Body.String())
		}
	})
}

func TestDeckAssetUploadPersistsExactSlideAndBumpsVersion(t *testing.T) {
	cookies, artifact := setupDeckEditorHTTPTest(t, LegacyCompatibleObjectAuthorizer{})
	get := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/deck?id="+artifact.ID, "", cookies, deckEditorHandler)
	var initial struct {
		Deck deckDocument `json:"deck"`
	}
	if get.Code != http.StatusOK || json.Unmarshal(get.Body.Bytes(), &initial) != nil {
		t.Fatalf("GET status=%d body=%s", get.Code, get.Body.String())
	}
	patchBody, _ := json.Marshal(map[string]any{"artifactId": artifact.ID, "expectedVersion": artifactVersion(artifact), "deck": initial.Deck})
	patch := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/deck", string(patchBody), cookies, deckEditorHandler)
	var native struct {
		Artifact deckArtifactView `json:"artifact"`
	}
	if patch.Code != http.StatusOK || json.Unmarshal(patch.Body.Bytes(), &native) != nil {
		t.Fatalf("native PATCH status=%d body=%s", patch.Code, patch.Body.String())
	}

	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	_ = writer.WriteField("artifactId", artifact.ID)
	_ = writer.WriteField("expectedVersion", strconv.Itoa(native.Artifact.Version))
	_ = writer.WriteField("slideId", "slide-1")
	_ = writer.WriteField("placement", "full_bleed")
	file, err := writer.CreateFormFile("file", "hero.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write(append([]byte("\x89PNG\r\n\x1a\n"), []byte("bounded upload")...))
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/artifacts/deck/assets", &multipartBody)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	deckEditorAssetUploadHandler(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var uploaded struct {
		Artifact deckArtifactView `json:"artifact"`
		Deck     deckDocument     `json:"deck"`
		Element  deckElement      `json:"element"`
	}
	if json.Unmarshal(recorder.Body.Bytes(), &uploaded) != nil || uploaded.Artifact.Version <= native.Artifact.Version || uploaded.Element.Ref == "" || uploaded.Element.X != 0 || uploaded.Element.Width != 1920 {
		t.Fatalf("upload response=%s, want full-bleed exact-slide element and bumped version", recorder.Body.String())
	}
	if got := uploaded.Deck.Slides[0].Elements[len(uploaded.Deck.Slides[0].Elements)-1].Ref; got != uploaded.Element.Ref {
		t.Fatalf("last slide element ref=%q, want uploaded ref %q", got, uploaded.Element.Ref)
	}
}

func TestDeckImageGenerationReauthorizesAfterProviderWork(t *testing.T) {
	authorizer := &deckReauthAuthorizer{}
	cookies, artifact := setupDeckEditorHTTPTest(t, authorizer)
	previousGenerator := createDeckEditorImage
	generated := false
	createDeckEditorImage = func(_ context.Context, _ string, _ openAIImageOptions) (string, string, error) {
		generated = true
		ref, err := putBlob([]byte("generated image"), "image/png")
		return ref, "image/png", err
	}
	t.Cleanup(func() { createDeckEditorImage = previousGenerator })
	body, _ := json.Marshal(map[string]any{
		"artifactId": artifact.ID, "expectedVersion": artifactVersion(artifact), "slideId": "slide-1", "prompt": "A cinematic field at dawn", "placement": "image",
	})
	response := artifactAuthorizationRequest(t, http.MethodPost, "/artifacts/deck/image-generations", string(body), cookies, deckEditorImageGenerationHandler)
	if !generated || response.Code != http.StatusConflict {
		t.Fatalf("generation called=%v status=%d body=%s, want post-provider reauth conflict", generated, response.Code, response.Body.String())
	}
	current, ok := kanbanApp.osArtifactByID(artifact.ID)
	if !ok || artifactVersion(current) != artifactVersion(artifact) || len(artifactAssets(current)) != 0 || current.Metadata[deckSceneRefMetadataKey] != "" {
		t.Fatalf("denied reauth mutated artifact: %+v", current)
	}
}
