package main

// deck_editor.go owns the server-side editable deck boundary. The editable
// document is a bounded scene graph stored as a content-addressed JSON blob;
// the artifact body remains a deterministic, sandbox-rendered HTML projection.
// Generated images stay first-class artifact assets and are referenced by blob
// id from the scene graph -- image bytes are never pasted into artifact HTML.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	xhtml "golang.org/x/net/html"
)

const (
	deckDocumentSchemaVersion              = 1
	deckDocumentWidth                      = 1920
	deckDocumentHeight                     = 1080
	deckDocumentMaxBytes                   = 1 << 20
	deckDocumentMaxSlides                  = 100
	deckSlideMaxElements                   = 100
	deckElementTextMaxRunes                = 20000
	deckSlideNotesMaxRunes                 = 40000
	deckImagePromptMaxRunes                = 4000
	deckImageUploadMaxBytes                = 16 << 20
	deckSceneRefMetadataKey                = "deckSceneRef"
	deckSchemaMetadataKey                  = "deckSchemaVersion"
	legacyFieldbookPresenterSkeletonSHA256 = "8d0fda5450bf5b1fa6ca6f6501a1af1cdd10adda2123ac192b2eecb8f7a49438"
	legacyTestPresenterSkeletonSHA256      = "bd37685cfa9d83f0eff48588f689b4a89c5dd061168664c8a7d54ecbdd521b93"
)

var (
	deckIdentifierPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$`)
	deckHexColorPattern     = regexp.MustCompile(`^#[0-9A-Fa-f]{3,4}([0-9A-Fa-f]{3,4})?$`)
	deckFontPattern         = regexp.MustCompile(`^[A-Za-z0-9 ,_-]{1,80}$`)
	deckTrackingPattern     = regexp.MustCompile(`^(normal|-?(?:\d+(?:\.\d+)?|\.\d+)(?:px|em|rem)?)$`)
	legacyCSSCommentPattern = regexp.MustCompile(`(?s)/\*.*?\*/`)
	legacyCSSRulePattern    = regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`)
	legacyCSSVarPattern     = regexp.MustCompile(`(--[A-Za-z0-9_-]+)\s*:\s*([^;{}]+)`)
	legacyCSSVarUsePattern  = regexp.MustCompile(`var\(\s*(--[A-Za-z0-9_-]+)\s*\)`)
	createDeckEditorImage   = createOpenAIImage
)

type deckDocument struct {
	SchemaVersion int         `json:"schemaVersion"`
	Width         int         `json:"width"`
	Height        int         `json:"height"`
	Slides        []deckSlide `json:"slides"`
}

type deckSlide struct {
	ID         string        `json:"id"`
	Background string        `json:"background,omitempty"`
	Notes      string        `json:"notes,omitempty"`
	Elements   []deckElement `json:"elements"`
}

type deckElement struct {
	ID            string  `json:"id"`
	Type          string  `json:"type"` // text | image | shape
	X             float64 `json:"x"`
	Y             float64 `json:"y"`
	Width         float64 `json:"width"`
	Height        float64 `json:"height"`
	Z             int     `json:"z"`
	Opacity       float64 `json:"opacity"`
	Rotation      float64 `json:"rotation,omitempty"`
	Text          string  `json:"text,omitempty"`
	FontSize      float64 `json:"fontSize,omitempty"`
	FontFamily    string  `json:"fontFamily,omitempty"`
	FontWeight    int     `json:"fontWeight,omitempty"`
	Color         string  `json:"color,omitempty"`
	TextAlign     string  `json:"textAlign,omitempty"` // left | center | right
	LineHeight    float64 `json:"lineHeight,omitempty"`
	LetterSpacing string  `json:"letterSpacing,omitempty"`
	Ref           string  `json:"ref,omitempty"`
	Name          string  `json:"name,omitempty"`
	Fit           string  `json:"fit,omitempty"`   // cover | contain
	Shape         string  `json:"shape,omitempty"` // rectangle | ellipse
	Fill          string  `json:"fill,omitempty"`
	Stroke        string  `json:"stroke,omitempty"`
	StrokeWidth   float64 `json:"strokeWidth,omitempty"`
	Prompt        string  `json:"prompt,omitempty"`
	GeneratedAt   string  `json:"generatedAt,omitempty"`
}

type deckArtifactView struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Type      string `json:"type"`
	Version   int    `json:"version"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

func deckArtifactViewFromEntry(entry meetingMemoryEntry) deckArtifactView {
	return deckArtifactView{
		ID: entry.ID, Title: strings.TrimSpace(entry.Metadata["title"]), Type: artifactType(entry),
		Version: artifactVersion(entry), UpdatedAt: strings.TrimSpace(entry.Metadata["updatedAt"]),
	}
}

func deckEditorHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPatch {
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

	if r.Method == http.MethodGet {
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		artifact, ok := authorizedArtifactForActions(r.Context(), user, id, ACLReadContent)
		if !ok || artifactType(artifact) != artifactTypeHTMLDeck {
			writeAuthError(w, http.StatusNotFound, "deck artifact not found")
			return
		}
		deck, imported, importQuality, err := loadDeckDocument(artifact)
		if err != nil {
			writeAuthError(w, http.StatusConflict, "deck document is unavailable")
			return
		}
		_, aclCanWrite := authorizedArtifactForActions(r.Context(), user, id, ACLReadContent, ACLWrite)
		canWrite := aclCanWrite && importQuality != "approximate"
		response := map[string]any{
			"ok": true, "artifact": deckArtifactViewFromEntry(artifact), "deck": deck, "imported": imported, "importQuality": importQuality, "canWrite": canWrite,
		}
		if aclCanWrite && !canWrite {
			response["writeBlockedReason"] = "legacy deck cannot be edited without losing unrecognized content"
		}
		writeAuthJSON(w, http.StatusOK, response)
		return
	}

	payload := struct {
		ArtifactID      string       `json:"artifactId"`
		ExpectedVersion int          `json:"expectedVersion"`
		Deck            deckDocument `json:"deck"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, deckDocumentMaxBytes+64<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read deck update")
		return
	}
	artifact, ok := authorizedArtifactForActions(r.Context(), user, strings.TrimSpace(payload.ArtifactID), ACLReadContent, ACLWrite)
	if !ok || artifactType(artifact) != artifactTypeHTMLDeck {
		writeAuthError(w, http.StatusNotFound, "deck artifact not found")
		return
	}
	if payload.ExpectedVersion < 1 || artifactVersion(artifact) != payload.ExpectedVersion {
		writeDeckVersionConflict(w, artifact)
		return
	}
	_, imported, quality, err := loadDeckDocument(artifact)
	if err != nil {
		writeAuthError(w, http.StatusConflict, "deck document is unavailable")
		return
	}
	if imported && quality == "approximate" {
		writeAuthError(w, http.StatusConflict, "legacy deck cannot be edited without losing unrecognized content")
		return
	}
	if err := validateDeckDocument(payload.Deck, artifactAssetRefSet(artifact)); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, changed, err := persistDeckDocument(r.Context(), user, artifact, payload.Deck, nil)
	if err != nil {
		writeDeckVersionConflict(w, artifact)
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok": true, "updated": changed, "artifact": deckArtifactViewFromEntry(updated), "deck": payload.Deck,
	})
}

func deckEditorImageGenerationHandler(w http.ResponseWriter, r *http.Request) {
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
		SlideID         string `json:"slideId"`
		Prompt          string `json:"prompt"`
		Placement       string `json:"placement"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read image generation request")
		return
	}
	payload.ArtifactID = strings.TrimSpace(payload.ArtifactID)
	payload.SlideID = strings.TrimSpace(payload.SlideID)
	payload.Prompt = strings.TrimSpace(payload.Prompt)
	payload.Placement = strings.ToLower(strings.TrimSpace(payload.Placement))
	if payload.Placement == "" {
		payload.Placement = "image"
	}
	if payload.ExpectedVersion < 1 || !deckIdentifierPattern.MatchString(payload.SlideID) || payload.Prompt == "" || len([]rune(payload.Prompt)) > deckImagePromptMaxRunes || (payload.Placement != "image" && payload.Placement != "full_bleed") {
		writeAuthError(w, http.StatusBadRequest, "artifactId, expectedVersion, slideId, a bounded prompt, and placement image|full_bleed are required")
		return
	}
	artifact, ok := authorizedArtifactForActions(r.Context(), user, payload.ArtifactID, ACLReadContent, ACLWrite)
	if !ok || artifactType(artifact) != artifactTypeHTMLDeck {
		writeAuthError(w, http.StatusNotFound, "deck artifact not found")
		return
	}
	if artifactVersion(artifact) != payload.ExpectedVersion {
		writeDeckVersionConflict(w, artifact)
		return
	}
	deck, imported, quality, err := loadDeckDocument(artifact)
	if err != nil || imported && quality == "approximate" || deckSlideIndex(deck, payload.SlideID) < 0 {
		writeAuthError(w, http.StatusBadRequest, "target slide is unavailable")
		return
	}

	// This endpoint is intentionally synchronous in v1. The client owns the
	// visible blocking `generating` state for the lifetime of this request; no
	// success is returned until the blob, asset, scene, and artifact revision
	// are all durable.
	ref, mime, err := createDeckEditorImage(r.Context(), payload.Prompt, openAIImageOptions{})
	if err != nil {
		writeAuthError(w, http.StatusBadGateway, "image generation failed: "+compactAssistantLine(err.Error()))
		return
	}

	// Generation can take minutes. Reauthorize and re-check the requested
	// revision before attaching anything to the deck.
	current, ok := authorizedArtifactForActions(r.Context(), user, payload.ArtifactID, ACLReadContent, ACLWrite)
	if !ok || artifactVersion(current) != payload.ExpectedVersion {
		writeDeckVersionConflict(w, current)
		return
	}
	name := "generated-deck-image." + deckImageExtension(mime)
	deck, imported, quality, err = loadDeckDocument(current)
	if err != nil || imported && quality == "approximate" {
		writeAuthError(w, http.StatusConflict, "deck document changed during image generation")
		return
	}
	slideIndex := deckSlideIndex(deck, payload.SlideID)
	if slideIndex < 0 {
		writeAuthError(w, http.StatusConflict, "target slide changed during image generation")
		return
	}
	element := generatedDeckImageElement(deck.Slides[slideIndex], ref, name, payload.Prompt, payload.Placement)
	deck.Slides[slideIndex].Elements = append(deck.Slides[slideIndex].Elements, element)
	allowedRefs := artifactAssetRefSet(current)
	allowedRefs[ref] = struct{}{}
	if err := validateDeckDocument(deck, allowedRefs); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	provenance := map[string]any{"ref": ref, "prompt": payload.Prompt, "slideId": payload.SlideID, "placement": payload.Placement, "generatedAt": element.GeneratedAt}
	provenanceRaw, _ := json.Marshal(provenance)
	assetsRaw, err := deckAssetsMetadataWith(current, artifactAsset{Ref: ref, Mime: mime, Name: name, Kind: "image"})
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "generated image could not be attached")
		return
	}
	updated, _, err := persistDeckDocument(r.Context(), user, current, deck, map[string]string{
		"deckLastImageGeneration": string(provenanceRaw), artifactAssetsMetadataKey: assetsRaw,
	})
	if err != nil {
		writeDeckVersionConflict(w, current)
		return
	}
	writeAuthJSON(w, http.StatusCreated, map[string]any{
		"ok": true, "updated": true, "artifact": deckArtifactViewFromEntry(updated), "deck": deck,
		"image": map[string]any{"ref": ref, "mime": mime, "name": name, "prompt": payload.Prompt}, "element": element,
	})
}

func deckEditorAssetUploadHandler(w http.ResponseWriter, r *http.Request) {
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
	r.Body = http.MaxBytesReader(w, r.Body, deckImageUploadMaxBytes+1<<20)
	if err := r.ParseMultipartForm(deckImageUploadMaxBytes); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read image upload")
		return
	}
	artifactID := strings.TrimSpace(r.FormValue("artifactId"))
	expectedVersion, err := strconv.Atoi(strings.TrimSpace(r.FormValue("expectedVersion")))
	slideID := strings.TrimSpace(r.FormValue("slideId"))
	placement := strings.ToLower(strings.TrimSpace(r.FormValue("placement")))
	if placement == "" {
		placement = "image"
	}
	if artifactID == "" || expectedVersion < 1 || !deckIdentifierPattern.MatchString(slideID) || (placement != "image" && placement != "full_bleed") {
		writeAuthError(w, http.StatusBadRequest, "artifactId, expectedVersion, slideId, and placement image|full_bleed are required")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "image file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, deckImageUploadMaxBytes+1))
	if err != nil || len(data) == 0 || len(data) > deckImageUploadMaxBytes {
		writeAuthError(w, http.StatusBadRequest, "image file exceeds the 16MB limit")
		return
	}
	mime := http.DetectContentType(data)
	if !oneOf(mime, "image/png", "image/jpeg", "image/webp", "image/gif") {
		writeAuthError(w, http.StatusBadRequest, "image must be PNG, JPEG, WebP, or GIF")
		return
	}
	artifact, ok := authorizedArtifactForActions(r.Context(), user, artifactID, ACLReadContent, ACLWrite)
	if !ok || artifactType(artifact) != artifactTypeHTMLDeck {
		writeAuthError(w, http.StatusNotFound, "deck artifact not found")
		return
	}
	if artifactVersion(artifact) != expectedVersion {
		writeDeckVersionConflict(w, artifact)
		return
	}
	deck, imported, quality, err := loadDeckDocument(artifact)
	if err != nil || imported && quality == "approximate" || deckSlideIndex(deck, slideID) < 0 {
		writeAuthError(w, http.StatusBadRequest, "target slide is unavailable")
		return
	}
	ref, err := putBlob(data, mime)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "image could not be stored")
		return
	}
	name := strings.TrimSpace(filepath.Base(header.Filename))
	if name == "." || name == "" || len(name) > 200 {
		name = "deck-image." + deckImageExtension(mime)
	}
	element := generatedDeckImageElement(deck.Slides[deckSlideIndex(deck, slideID)], ref, name, "", placement)
	element.GeneratedAt = ""
	deck.Slides[deckSlideIndex(deck, slideID)].Elements = append(deck.Slides[deckSlideIndex(deck, slideID)].Elements, element)
	allowedRefs := artifactAssetRefSet(artifact)
	allowedRefs[ref] = struct{}{}
	if err := validateDeckDocument(deck, allowedRefs); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	asset := artifactAsset{Ref: ref, Mime: mime, Name: name, Kind: "image"}
	assetsRaw, err := deckAssetsMetadataWith(artifact, asset)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "image could not be attached")
		return
	}
	updated, _, err := persistDeckDocument(r.Context(), user, artifact, deck, map[string]string{artifactAssetsMetadataKey: assetsRaw})
	if err != nil {
		writeDeckVersionConflict(w, artifact)
		return
	}
	writeAuthJSON(w, http.StatusCreated, map[string]any{
		"ok": true, "updated": true, "artifact": deckArtifactViewFromEntry(updated), "deck": deck,
		"image": map[string]any{"ref": ref, "mime": mime, "name": name}, "element": element,
	})
}

func deckAssetsMetadataWith(artifact meetingMemoryEntry, additions ...artifactAsset) (string, error) {
	assets := artifactAssets(artifact)
	byRef := make(map[string]int, len(assets))
	for index, asset := range assets {
		byRef[asset.Ref] = index
	}
	for _, addition := range additions {
		if !validBlobRef(addition.Ref) {
			return "", fmt.Errorf("invalid image ref")
		}
		if index, exists := byRef[addition.Ref]; exists {
			assets[index] = addition
		} else {
			byRef[addition.Ref] = len(assets)
			assets = append(assets, addition)
		}
	}
	raw, err := json.Marshal(assets)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func writeDeckVersionConflict(w http.ResponseWriter, artifact meetingMemoryEntry) {
	payload := map[string]any{"error": "deck revision changed; reload before saving", "currentVersion": 0}
	if artifact.ID != "" {
		payload["currentVersion"] = artifactVersion(artifact)
		payload["artifactId"] = artifact.ID
	}
	writeAuthJSON(w, http.StatusConflict, payload)
}

func loadDeckDocument(artifact meetingMemoryEntry) (deckDocument, bool, string, error) {
	if ref := strings.TrimSpace(artifact.Metadata[deckSceneRefMetadataKey]); ref != "" {
		if !validBlobRef(ref) {
			return deckDocument{}, false, "", fmt.Errorf("invalid deck scene ref")
		}
		raw, _, err := getBlob(ref)
		if err != nil || len(raw) > deckDocumentMaxBytes {
			return deckDocument{}, false, "", fmt.Errorf("read deck scene")
		}
		var deck deckDocument
		if err := strictJSONBytes(raw, &deck); err != nil {
			return deckDocument{}, false, "", fmt.Errorf("decode deck scene")
		}
		if err := validateDeckDocument(deck, artifactAssetRefSet(artifact)); err != nil {
			return deckDocument{}, false, "", err
		}
		return deck, false, "native", nil
	}
	deck, quality := importLegacyDeckDocument(artifact)
	if err := validateDeckDocument(deck, artifactAssetRefSet(artifact)); err != nil {
		return deckDocument{}, true, quality, err
	}
	return deck, true, quality, nil
}

func persistDeckDocument(ctx context.Context, user *userAccount, prior meetingMemoryEntry, deck deckDocument, extraMetadata map[string]string) (meetingMemoryEntry, bool, error) {
	raw, err := json.Marshal(deck)
	if err != nil || len(raw) > deckDocumentMaxBytes {
		return meetingMemoryEntry{}, false, fmt.Errorf("deck document exceeds its storage bound")
	}
	ref, err := putBlob(raw, "application/vnd.bonfire.deck+json")
	if err != nil {
		return meetingMemoryEntry{}, false, err
	}
	metadata := map[string]string{deckSceneRefMetadataKey: ref, deckSchemaMetadataKey: strconv.Itoa(deckDocumentSchemaVersion), "type": artifactTypeHTMLDeck}
	for key, value := range extraMetadata {
		metadata[key] = value
	}
	assetSource := prior
	if encoded, supplied := metadata[artifactAssetsMetadataKey]; supplied {
		assetSource.Metadata = make(map[string]string, len(prior.Metadata)+1)
		for key, value := range prior.Metadata {
			assetSource.Metadata[key] = value
		}
		assetSource.Metadata[artifactAssetsMetadataKey] = encoded
	}
	assets := artifactAssets(assetSource)
	assetRefs := make(map[string]struct{}, len(assets))
	for _, asset := range assets {
		assetRefs[asset.Ref] = struct{}{}
	}
	assetsChanged := false
	for _, slide := range deck.Slides {
		for _, element := range slide.Elements {
			if element.Type != "image" {
				continue
			}
			if _, attached := assetRefs[element.Ref]; attached {
				continue
			}
			_, meta, err := getBlob(element.Ref)
			if err != nil || !strings.HasPrefix(strings.ToLower(meta.Mime), "image/") {
				return meetingMemoryEntry{}, false, fmt.Errorf("deck image is unavailable")
			}
			assets = append(assets, artifactAsset{Ref: element.Ref, Mime: meta.Mime, Name: firstNonEmptyString(element.Name, "deck-image."+deckImageExtension(meta.Mime)), Kind: "image"})
			assetRefs[element.Ref] = struct{}{}
			assetsChanged = true
		}
	}
	if assetsChanged || metadata[artifactAssetsMetadataKey] != "" {
		encoded, err := json.Marshal(assets)
		if err != nil {
			return meetingMemoryEntry{}, false, err
		}
		metadata[artifactAssetsMetadataKey] = string(encoded)
	}
	body := compileDeckDocumentHTML(deck, strings.TrimSpace(prior.Metadata["title"]))
	header := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(prior))
	var updated meetingMemoryEntry
	var changed bool
	err = kanbanApp.withCurrentAgentThreadSource(scoutAgentThread{Artifact: prior}, func() error {
		var updateErr error
		updated, changed, updateErr = kanbanApp.memory.updateOSArtifactWithMetadataIfHeaderMatches(header, prior.ID, prior.Metadata["title"], body, user.Name, metadata)
		return updateErr
	})
	return updated, changed, err
}

func validateDeckDocument(deck deckDocument, allowedImageRefs map[string]struct{}) error {
	if deck.SchemaVersion != deckDocumentSchemaVersion || deck.Width != deckDocumentWidth || deck.Height != deckDocumentHeight {
		return fmt.Errorf("deck must use schemaVersion 1 and a 1920x1080 canvas")
	}
	if len(deck.Slides) < 1 || len(deck.Slides) > deckDocumentMaxSlides {
		return fmt.Errorf("deck must contain 1-%d slides", deckDocumentMaxSlides)
	}
	seenSlides := map[string]struct{}{}
	seenElements := map[string]struct{}{}
	for _, slide := range deck.Slides {
		if !deckIdentifierPattern.MatchString(slide.ID) {
			return fmt.Errorf("slide id is invalid")
		}
		if _, duplicate := seenSlides[slide.ID]; duplicate {
			return fmt.Errorf("slide ids must be unique")
		}
		seenSlides[slide.ID] = struct{}{}
		if slide.Background != "" && !validDeckColor(slide.Background) {
			return fmt.Errorf("slide background is invalid")
		}
		if len([]rune(slide.Notes)) > deckSlideNotesMaxRunes {
			return fmt.Errorf("slide presenter notes exceed the storage bound")
		}
		if len(slide.Elements) > deckSlideMaxElements {
			return fmt.Errorf("a slide exceeds the %d element cap", deckSlideMaxElements)
		}
		for _, element := range slide.Elements {
			if !deckIdentifierPattern.MatchString(element.ID) {
				return fmt.Errorf("element id is invalid")
			}
			if _, duplicate := seenElements[element.ID]; duplicate {
				return fmt.Errorf("element ids must be unique")
			}
			seenElements[element.ID] = struct{}{}
			if !deckFinite(element.X, element.Y, element.Width, element.Height, element.Opacity, element.Rotation, element.FontSize, element.LineHeight, element.StrokeWidth) ||
				element.X < -deckDocumentWidth || element.X > 2*deckDocumentWidth || element.Y < -deckDocumentHeight || element.Y > 2*deckDocumentHeight ||
				element.Width < 1 || element.Width > 2*deckDocumentWidth || element.Height < 1 || element.Height > 2*deckDocumentHeight ||
				element.Z < -1000 || element.Z > 1000 || element.Opacity < 0 || element.Opacity > 1 || element.Rotation < -360 || element.Rotation > 360 {
				return fmt.Errorf("element geometry is outside the deck bounds")
			}
			switch element.Type {
			case "text":
				if len([]rune(element.Text)) > deckElementTextMaxRunes || element.FontSize < 8 || element.FontSize > 400 || element.FontWeight < 100 || element.FontWeight > 900 ||
					(element.FontFamily != "" && !deckFontPattern.MatchString(element.FontFamily)) || !validDeckColor(firstNonEmptyString(element.Color, "#ffffff")) ||
					(element.TextAlign != "" && !oneOf(element.TextAlign, "left", "center", "right")) || element.LineHeight < 0 || element.LineHeight > 4 ||
					(element.LetterSpacing != "" && !deckTrackingPattern.MatchString(element.LetterSpacing)) {
					return fmt.Errorf("text element styling is invalid")
				}
			case "image":
				if !validBlobRef(element.Ref) || len(element.Name) > 200 || (element.Fit != "cover" && element.Fit != "contain") {
					return fmt.Errorf("image element is invalid")
				}
				if _, allowed := allowedImageRefs[element.Ref]; !allowed {
					return fmt.Errorf("image element is not attached to this artifact")
				}
			case "shape":
				if (element.Shape != "rectangle" && element.Shape != "ellipse") || !validDeckColor(firstNonEmptyString(element.Fill, "transparent")) ||
					(element.Stroke != "" && !validDeckColor(element.Stroke)) || element.StrokeWidth < 0 || element.StrokeWidth > 100 {
					return fmt.Errorf("shape element styling is invalid")
				}
			default:
				return fmt.Errorf("element type must be text, image, or shape")
			}
		}
	}
	raw, err := json.Marshal(deck)
	if err != nil || len(raw) > deckDocumentMaxBytes {
		return fmt.Errorf("deck document exceeds its storage bound")
	}
	return nil
}

func deckFinite(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}

func validDeckColor(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "transparent" || value == "white" || value == "black" || deckHexColorPattern.MatchString(value)
}

func artifactAssetRefSet(artifact meetingMemoryEntry) map[string]struct{} {
	refs := map[string]struct{}{}
	for _, asset := range artifactAssets(artifact) {
		if artifactAssetIsEditableImage(asset) {
			refs[asset.Ref] = struct{}{}
		}
	}
	for ref := range legacyEmbeddedImageRefs(artifact.Text) {
		refs[ref] = struct{}{}
	}
	return refs
}

func legacyEmbeddedImageRefs(body string) map[string]struct{} {
	refs := map[string]struct{}{}
	pattern := regexp.MustCompile(`(?i)data:(image/(?:png|jpeg|webp|gif));base64,([A-Za-z0-9+/=]+)`)
	for _, match := range pattern.FindAllStringSubmatch(body, -1) {
		if len(match[2]) > (deckImageUploadMaxBytes*4/3)+8 {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(match[2])
		if err != nil || len(data) == 0 || len(data) > deckImageUploadMaxBytes {
			continue
		}
		digest := sha256.Sum256(data)
		ref := hex.EncodeToString(digest[:])
		if _, meta, err := getBlob(ref); err == nil && strings.HasPrefix(strings.ToLower(meta.Mime), "image/") {
			refs[ref] = struct{}{}
		}
	}
	return refs
}

func deckSlideIndex(deck deckDocument, id string) int {
	for index := range deck.Slides {
		if deck.Slides[index].ID == id {
			return index
		}
	}
	return -1
}

func generatedDeckImageElement(slide deckSlide, ref, name, prompt, placement string) deckElement {
	id := "image-" + ref[:12]
	used := map[string]struct{}{}
	maxZ := 0
	for _, element := range slide.Elements {
		used[element.ID] = struct{}{}
		if element.Z > maxZ {
			maxZ = element.Z
		}
	}
	for suffix := 2; ; suffix++ {
		if _, exists := used[id]; !exists {
			break
		}
		id = "image-" + ref[:12] + "-" + strconv.Itoa(suffix)
	}
	element := deckElement{ID: id, Type: "image", Ref: ref, Name: name, Fit: "cover", Opacity: 1, Prompt: prompt, GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if placement == "full_bleed" {
		element.X, element.Y, element.Width, element.Height, element.Z = 0, 0, deckDocumentWidth, deckDocumentHeight, -100
	} else {
		element.X, element.Y, element.Width, element.Height, element.Z = 960, 120, 840, 840, maxZ+1
	}
	return element
}

func deckImageExtension(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}

func compileDeckDocumentHTML(deck deckDocument, title string) string {
	var builder strings.Builder
	builder.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>")
	builder.WriteString(html.EscapeString(firstNonEmptyString(strings.TrimSpace(title), "Presentation")))
	builder.WriteString("</title><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#08080a}#stage{position:relative;width:1920px;height:1080px;transform-origin:top left}.pg{position:absolute;inset:0;display:none;overflow:hidden}.pg.on{display:block}.el{position:absolute;box-sizing:border-box}.text{white-space:pre-wrap;overflow:hidden;line-height:1.08}.image{display:block}.shape{box-sizing:border-box}@media print{@page{size:13.333333in 7.5in;margin:0}html,body{width:auto;height:auto;overflow:visible;background:#fff}#stage{width:auto;height:auto;transform:none!important}.pg,.pg.on{position:relative;display:block!important;width:1920px;height:1080px;break-after:page;page-break-after:always}}</style></head><body><div id=\"stage\">")
	for slideIndex, slide := range deck.Slides {
		className := "pg"
		if slideIndex == 0 {
			className += " on"
		}
		background := firstNonEmptyString(slide.Background, "#101014")
		builder.WriteString("<section class=\"")
		builder.WriteString(className)
		builder.WriteString("\" data-slide-id=\"")
		builder.WriteString(html.EscapeString(slide.ID))
		builder.WriteString("\" style=\"background:")
		builder.WriteString(background)
		builder.WriteString("\">")
		elements := append([]deckElement(nil), slide.Elements...)
		sort.SliceStable(elements, func(i, j int) bool { return elements[i].Z < elements[j].Z })
		for _, element := range elements {
			style := deckElementStyle(element)
			switch element.Type {
			case "text":
				builder.WriteString("<div class=\"el text\" data-element-id=\"")
				builder.WriteString(html.EscapeString(element.ID))
				builder.WriteString("\" style=\"")
				builder.WriteString(style)
				builder.WriteString(";font-size:")
				builder.WriteString(deckCSSNumber(element.FontSize))
				builder.WriteString("px;font-family:")
				builder.WriteString(firstNonEmptyString(element.FontFamily, "Arial"))
				builder.WriteString(";font-weight:")
				builder.WriteString(strconv.Itoa(element.FontWeight))
				builder.WriteString(";color:")
				builder.WriteString(firstNonEmptyString(element.Color, "#ffffff"))
				builder.WriteString(";text-align:")
				builder.WriteString(firstNonEmptyString(element.TextAlign, "left"))
				if element.LineHeight > 0 {
					builder.WriteString(";line-height:")
					builder.WriteString(deckCSSNumber(element.LineHeight))
				}
				if element.LetterSpacing != "" {
					builder.WriteString(";letter-spacing:")
					builder.WriteString(element.LetterSpacing)
				}
				builder.WriteString("\">")
				builder.WriteString(html.EscapeString(element.Text))
				builder.WriteString("</div>")
			case "image":
				builder.WriteString("<img class=\"el image\" data-element-id=\"")
				builder.WriteString(html.EscapeString(element.ID))
				builder.WriteString("\" alt=\"")
				builder.WriteString(html.EscapeString(firstNonEmptyString(element.Prompt, element.Name, "Deck image")))
				builder.WriteString("\" src=\"/artifacts/blob?ref=")
				builder.WriteString(url.QueryEscape(element.Ref))
				builder.WriteString("&amp;name=")
				builder.WriteString(url.QueryEscape(element.Name))
				builder.WriteString("\" style=\"")
				builder.WriteString(style)
				builder.WriteString(";object-fit:")
				builder.WriteString(element.Fit)
				builder.WriteString("\">")
			case "shape":
				builder.WriteString("<div class=\"el shape\" data-element-id=\"")
				builder.WriteString(html.EscapeString(element.ID))
				builder.WriteString("\" style=\"")
				builder.WriteString(style)
				builder.WriteString(";background:")
				builder.WriteString(firstNonEmptyString(element.Fill, "transparent"))
				if element.Stroke != "" && element.StrokeWidth > 0 {
					builder.WriteString(";border:")
					builder.WriteString(deckCSSNumber(element.StrokeWidth))
					builder.WriteString("px solid ")
					builder.WriteString(element.Stroke)
				}
				if element.Shape == "ellipse" {
					builder.WriteString(";border-radius:50%")
				}
				builder.WriteString("\"></div>")
			}
		}
		if strings.TrimSpace(slide.Notes) != "" {
			builder.WriteString("<div class=\"notes\" hidden>")
			builder.WriteString(html.EscapeString(slide.Notes))
			builder.WriteString("</div>")
		}
		builder.WriteString("</section>")
	}
	builder.WriteString("</div></body></html>")
	return builder.String()
}

func deckElementStyle(element deckElement) string {
	return strings.Join([]string{
		"left:" + deckCSSNumber(element.X) + "px", "top:" + deckCSSNumber(element.Y) + "px",
		"width:" + deckCSSNumber(element.Width) + "px", "height:" + deckCSSNumber(element.Height) + "px",
		"z-index:" + strconv.Itoa(element.Z), "opacity:" + deckCSSNumber(element.Opacity),
		"transform:rotate(" + deckCSSNumber(element.Rotation) + "deg)",
	}, ";")
}

func deckCSSNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func importLegacyDeckDocument(artifact meetingMemoryEntry) (deckDocument, string) {
	deck := deckDocument{SchemaVersion: deckDocumentSchemaVersion, Width: deckDocumentWidth, Height: deckDocumentHeight}
	doc, err := xhtml.Parse(strings.NewReader(artifact.Text))
	if err != nil {
		return defaultImportedDeck(artifact), "approximate"
	}
	var slideNodes []*xhtml.Node
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		isStageChild := node.Type == xhtml.ElementNode && node.Data == "section" && node.Parent != nil && legacyNodeAttr(node.Parent, "id") == "stage"
		if node.Type == xhtml.ElementNode && (legacyNodeHasClass(node, "pg") || legacyNodeHasClass(node, "slide") || isStageChild) {
			slideNodes = append(slideNodes, node)
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if len(slideNodes) == 0 {
		return defaultImportedDeck(artifact), "approximate"
	}
	if len(slideNodes) > deckDocumentMaxSlides {
		slideNodes = slideNodes[:deckDocumentMaxSlides]
	}
	outerNotes, outerPresenterSafe := legacyOuterPresenterNotes(doc, len(slideNodes))
	typography := legacyTypographyContextForDocument(doc)
	// Presenter navigation and notes controls live outside #stage and are not
	// part of the editable slide scene. Their scripts must not make a fully
	// annotated deck read-only. Unsupported behavior inside a slide still fails
	// closed because saving the inert scene would otherwise discard it.
	faithful := outerPresenterSafe
	visualClasses := legacyDocumentVisualClasses(doc)
	allowedRefs := artifactAssetRefSet(artifact)
	for index, node := range slideNodes {
		if legacyDocumentHasUnsupportedBehavior(node) {
			faithful = false
		}
		slideID := firstNonEmptyString(legacyNodeAttr(node, "data-deck-slide"), legacyNodeAttr(node, "id"), fmt.Sprintf("slide-%d", index+1))
		if !deckIdentifierPattern.MatchString(slideID) {
			slideID = fmt.Sprintf("slide-%d", index+1)
		}
		background := firstNonEmptyString(legacyStyleMap(node)["background-color"], legacyStyleMap(node)["background"], "#101014")
		if !validDeckColor(background) {
			background = "#101014"
			faithful = false
		}
		elements, recognized := legacyDataDeckElements(node, allowedRefs, artifactAssets(artifact), typography)
		if recognized && !legacyMeaningfulContentCovered(node, visualClasses) {
			recognized = false
		}
		if !recognized {
			faithful = false
			elements = legacyTextElements(node)
		}
		notes, notesSafe := legacySlideNotes(node)
		if !notesSafe {
			faithful = false
		}
		if notes == "" && index < len(outerNotes) {
			notes = outerNotes[index]
		}
		slide := deckSlide{ID: slideID, Background: background, Notes: notes, Elements: elements}
		if len(slide.Elements) == 0 {
			faithful = false
			slide.Elements = []deckElement{defaultDeckTextElement("text-"+strconv.Itoa(index+1), "Slide "+strconv.Itoa(index+1), 120, 120, 1680, 180, 72, 700)}
		}
		deck.Slides = append(deck.Slides, slide)
	}
	if faithful {
		return deck, "faithful"
	}
	return deck, "approximate"
}

// legacyMeaningfulContentCovered prevents a partially annotated HTML deck
// from being presented as faithful. Text and imagery outside a marked editor
// element would disappear on the first save, so mixed marked/unmarked slides
// are explicitly approximate and the HTTP mutation paths refuse them.
func legacyMeaningfulContentCovered(root *xhtml.Node, visualClasses map[string]struct{}) bool {
	complete := true
	var walk func(*xhtml.Node, string)
	walk = func(node *xhtml.Node, coveredType string) {
		if !complete {
			return
		}
		if node.Type == xhtml.ElementNode {
			if legacyNodeHasClass(node, "notes") || legacyNodeAttr(node, "data-deck-notes") != "" {
				return
			}
			represented := legacyNodeAttr(node, "data-deck-element") != ""
			if represented {
				coveredType = strings.ToLower(legacyNodeAttr(node, "data-deck-type"))
			}
			if node != root && !represented && legacyNodeHasUnrepresentedVisual(node, coveredType, visualClasses) {
				complete = false
				return
			}
			if coveredType != "image" {
				if node.Data == "img" || strings.Contains(strings.ToLower(legacyStyleMap(node)["background-image"]), "url(") {
					complete = false
					return
				}
				for _, className := range strings.Fields(legacyNodeAttr(node, "class")) {
					if regexp.MustCompile(`^fig-[1-9][0-9]*$`).MatchString(className) {
						complete = false
						return
					}
				}
			}
		}
		if node.Type == xhtml.TextNode && coveredType != "text" && strings.TrimSpace(node.Data) != "" {
			parentName := ""
			if node.Parent != nil {
				parentName = node.Parent.Data
			}
			if parentName != "style" && parentName != "script" {
				complete = false
				return
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, coveredType)
		}
	}
	walk(root, "")
	return complete
}

func legacySlideNotes(root *xhtml.Node) (string, bool) {
	var notes string
	safe := true
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if notes != "" || node == nil {
			return
		}
		if node.Type == xhtml.ElementNode && (legacyNodeHasClass(node, "notes") || legacyNodeAttr(node, "data-deck-notes") != "") {
			notes = strings.TrimSpace(legacyNodeTextPreservingWhitespace(node))
			if len([]rune(notes)) > deckSlideNotesMaxRunes {
				notes = ""
				safe = false
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return notes, safe
}

// legacyOuterPresenterNotes migrates the one generated-deck presenter shape
// we know how to preserve: one outer script with `const notes = [...]`, exactly
// one string per authored slide. Any other outer script fails the import closed
// as approximate; native save must never erase unknown behavior or narration.
func legacyOuterPresenterNotes(root *xhtml.Node, slideCount int) ([]string, bool) {
	if root == nil || slideCount <= 0 {
		return nil, true
	}
	var scripts []string
	var walk func(*xhtml.Node, bool)
	walk = func(node *xhtml.Node, inSlide bool) {
		if node == nil {
			return
		}
		if node.Type == xhtml.ElementNode && (legacyNodeHasClass(node, "pg") || legacyNodeAttr(node, "data-deck-slide") != "") {
			inSlide = true
		}
		if node.Type == xhtml.ElementNode && strings.EqualFold(node.Data, "script") && !inSlide {
			scripts = append(scripts, legacyNodeTextPreservingWhitespace(node))
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, inSlide)
		}
	}
	walk(root, false)
	if len(scripts) == 0 {
		return nil, true
	}
	if len(scripts) != 1 {
		return nil, false
	}
	if !legacyOuterPresenterChromeSafe(root) {
		return nil, false
	}
	notes, ok := parseLegacyJSNotesArray(scripts[0])
	if !ok || len(notes) != slideCount {
		return nil, false
	}
	for _, note := range notes {
		if len([]rune(note)) > deckSlideNotesMaxRunes {
			return nil, false
		}
	}
	return notes, true
}

func legacyOuterPresenterChromeSafe(root *xhtml.Node) bool {
	var body *xhtml.Node
	var find func(*xhtml.Node)
	find = func(node *xhtml.Node) {
		if body != nil || node == nil {
			return
		}
		if node.Type == xhtml.ElementNode && strings.EqualFold(node.Data, "body") {
			body = node
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			find(child)
		}
	}
	find(root)
	if body == nil {
		return false
	}
	allowed := map[string]string{
		"stage":    "div",
		"prompt":   "button",
		"phint":    "div",
		"prevZone": "div",
		"nextZone": "div",
		"railwrap": "aside",
	}
	for child := body.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != xhtml.ElementNode {
			continue
		}
		if strings.EqualFold(child.Data, "script") {
			continue
		}
		id := legacyNodeAttr(child, "id")
		if tag, ok := allowed[id]; !ok || !strings.EqualFold(child.Data, tag) {
			return false
		}
	}
	return true
}

func parseLegacyJSNotesArray(script string) ([]string, bool) {
	signature := sha256Hex([]byte(legacyPresenterScriptSkeleton(script)))
	if signature != legacyFieldbookPresenterSkeletonSHA256 && signature != legacyTestPresenterSkeletonSHA256 {
		return nil, false
	}
	marker := regexp.MustCompile(`\bconst\s+notes\s*=\s*\[`).FindStringIndex(script)
	if marker == nil {
		return nil, false
	}
	index := marker[1]
	notes := make([]string, 0, 16)
	for index < len(script) {
		for index < len(script) && (unicode.IsSpace(rune(script[index])) || script[index] == ',') {
			index++
		}
		if index >= len(script) {
			return nil, false
		}
		if script[index] == ']' {
			return notes, true
		}
		quote := script[index]
		if quote != '`' && quote != '\'' && quote != '"' {
			return nil, false
		}
		index++
		var value strings.Builder
		closed := false
		for index < len(script) {
			char := script[index]
			index++
			if char == quote {
				closed = true
				break
			}
			if quote == '`' && char == '$' && index < len(script) && script[index] == '{' {
				return nil, false
			}
			if char != '\\' {
				value.WriteByte(char)
				continue
			}
			if index >= len(script) {
				return nil, false
			}
			escaped := script[index]
			index++
			switch escaped {
			case 'n':
				value.WriteByte('\n')
			case 'r':
				value.WriteByte('\r')
			case 't':
				value.WriteByte('\t')
			case 'b':
				value.WriteByte('\b')
			case 'f':
				value.WriteByte('\f')
			case 'v':
				value.WriteByte('\v')
			case '\\', '\'', '"', '`', '/':
				value.WriteByte(escaped)
			default:
				return nil, false
			}
		}
		if !closed {
			return nil, false
		}
		notes = append(notes, strings.TrimSpace(value.String()))
	}
	return nil, false
}

func legacyPresenterScriptSkeleton(script string) string {
	// The generated Fieldbook presenter escapes speaker notes with this exact
	// regex. Normalize it before string-token reduction so the quotes inside the
	// character class cannot be mistaken for JavaScript string delimiters. The
	// complete normalized script still has to match an allowlisted SHA below;
	// this does not broaden the accepted presenter behavior.
	script = strings.ReplaceAll(script, `/[&<>"']/g`, "REGEX_ESCAPE_HTML")
	var skeleton strings.Builder
	space := false
	for index := 0; index < len(script); {
		char := script[index]
		next := byte(0)
		if index+1 < len(script) {
			next = script[index+1]
		}
		if char == '/' && next == '/' {
			index += 2
			for index < len(script) && script[index] != '\n' {
				index++
			}
			continue
		}
		if char == '/' && next == '*' {
			index += 2
			for index+1 < len(script) && !(script[index] == '*' && script[index+1] == '/') {
				index++
			}
			if index+1 >= len(script) {
				return ""
			}
			index += 2
			continue
		}
		if char == '"' || char == '\'' || char == '`' {
			quote := char
			index++
			closed := false
			for index < len(script) {
				current := script[index]
				index++
				if current == '\\' {
					if index < len(script) {
						index++
					}
					continue
				}
				if current == quote {
					closed = true
					break
				}
			}
			if !closed {
				return ""
			}
			skeleton.WriteByte('S')
			space = false
			continue
		}
		if unicode.IsSpace(rune(char)) {
			if !space {
				skeleton.WriteByte(' ')
				space = true
			}
			index++
			continue
		}
		skeleton.WriteByte(char)
		space = false
		index++
	}
	return strings.TrimSpace(skeleton.String())
}

func legacyDocumentHasUnsupportedBehavior(root *xhtml.Node) bool {
	unsupported := false
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if unsupported {
			return
		}
		if node.Type == xhtml.ElementNode {
			if oneOf(node.Data, "script", "iframe", "object", "embed", "form", "input", "button", "video", "audio", "canvas", "svg") {
				unsupported = true
				return
			}
			for _, attribute := range node.Attr {
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(attribute.Key)), "on") && strings.TrimSpace(attribute.Val) != "" {
					unsupported = true
					return
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return unsupported
}

func legacyDocumentVisualClasses(root *xhtml.Node) map[string]struct{} {
	classes := map[string]struct{}{}
	classPattern := regexp.MustCompile(`\.([A-Za-z_][A-Za-z0-9_-]*)`)
	visualProperty := regexp.MustCompile(`(?i)(^|;)\s*(background(?:-color|-image)?|border(?:-[a-z-]+)?|box-shadow|filter|clip-path|mask(?:-[a-z-]+)?|transform|opacity|color|font(?:-[a-z-]+)?|text-decoration|display|grid(?:-[a-z-]+)?|flex(?:-[a-z-]+)?|gap|padding(?:-[a-z-]+)?|margin(?:-[a-z-]+)?)\s*:`)
	rulePattern := regexp.MustCompile(`(?s)([^{}]+)\{([^{}]+)\}`)
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && node.Data == "style" {
			css := legacyNodeTextPreservingWhitespace(node)
			for _, rule := range rulePattern.FindAllStringSubmatch(css, -1) {
				if !visualProperty.MatchString(rule[2]) {
					continue
				}
				for _, match := range classPattern.FindAllStringSubmatch(rule[1], -1) {
					classes[match[1]] = struct{}{}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return classes
}

func legacyNodeHasUnrepresentedVisual(node *xhtml.Node, coveredType string, visualClasses map[string]struct{}) bool {
	if oneOf(node.Data, "svg", "canvas", "hr", "img", "picture", "video") {
		return true
	}
	visualProperty := regexp.MustCompile(`(?i)^(background(?:-color|-image)?|border(?:-[a-z-]+)?|box-shadow|filter|clip-path|mask(?:-[a-z-]+)?|transform|opacity|color|font(?:-[a-z-]+)?|text-decoration|display|grid(?:-[a-z-]+)?|flex(?:-[a-z-]+)?|gap|padding(?:-[a-z-]+)?|margin(?:-[a-z-]+)?|position|left|top|right|bottom|width|height)$`)
	for property, value := range legacyStyleMap(node) {
		value = strings.ToLower(strings.TrimSpace(value))
		if visualProperty.MatchString(property) && value != "" && value != "none" && value != "normal" && value != "transparent" && value != "inherit" && value != "initial" && value != "0" && value != "0px" && value != "1" {
			return true
		}
	}
	for _, className := range strings.Fields(legacyNodeAttr(node, "class")) {
		if coveredType == "image" && className == "ph" {
			continue
		}
		if _, visual := visualClasses[className]; visual {
			return true
		}
	}
	return false
}

func legacyNodeTextPreservingWhitespace(node *xhtml.Node) string {
	var builder strings.Builder
	var walk func(*xhtml.Node)
	walk = func(current *xhtml.Node) {
		if current.Type == xhtml.TextNode {
			builder.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return builder.String()
}

func defaultImportedDeck(artifact meetingMemoryEntry) deckDocument {
	title := firstNonEmptyString(strings.TrimSpace(artifact.Metadata["title"]), "Presentation")
	return deckDocument{SchemaVersion: deckDocumentSchemaVersion, Width: deckDocumentWidth, Height: deckDocumentHeight, Slides: []deckSlide{{
		ID: "slide-1", Background: "#101014", Elements: []deckElement{defaultDeckTextElement("text-1", title, 120, 120, 1680, 240, 84, 700)},
	}}}
}

func legacyTextElements(root *xhtml.Node) []deckElement {
	var values []struct{ tag, text string }
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && oneOf(node.Data, "h1", "h2", "h3", "h4", "h5", "h6", "p", "li", "blockquote") {
			text := strings.TrimSpace(legacyNodeText(node))
			if text != "" {
				values = append(values, struct{ tag, text string }{node.Data, trimForStorage(text, deckElementTextMaxRunes)})
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	if len(values) > 12 {
		values = values[:12]
	}
	y := 100.0
	elements := make([]deckElement, 0, len(values))
	for index, value := range values {
		fontSize, weight, height := 34.0, 400, 92.0
		if strings.HasPrefix(value.tag, "h") {
			fontSize, weight, height = 68, 700, 170
		}
		elements = append(elements, defaultDeckTextElement(fmt.Sprintf("text-%d", index+1), value.text, 120, y, 1680, height, fontSize, weight))
		y += height + 24
		if y > 1000 {
			break
		}
	}
	return elements
}

func defaultDeckTextElement(id, text string, x, y, width, height, fontSize float64, weight int) deckElement {
	return deckElement{ID: id, Type: "text", X: x, Y: y, Width: width, Height: height, Z: 1, Opacity: 1, Text: text, FontSize: fontSize, FontFamily: "Arial", FontWeight: weight, Color: "#ffffff", TextAlign: "left", LineHeight: 1.08, LetterSpacing: "normal"}
}

func legacyNodeHasClass(node *xhtml.Node, className string) bool {
	for _, attribute := range node.Attr {
		if attribute.Key == "class" {
			for _, value := range strings.Fields(attribute.Val) {
				if value == className {
					return true
				}
			}
		}
	}
	return false
}

func legacyNodeAttr(node *xhtml.Node, key string) string {
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, key) {
			return strings.TrimSpace(attribute.Val)
		}
	}
	return ""
}

func legacyStyleMap(node *xhtml.Node) map[string]string {
	return legacyStyleDeclarations(legacyNodeAttr(node, "style"))
}

func legacyStyleDeclarations(source string) map[string]string {
	styles := map[string]string{}
	for _, declaration := range strings.Split(source, ";") {
		parts := strings.SplitN(declaration, ":", 2)
		if len(parts) == 2 {
			styles[strings.ToLower(strings.TrimSpace(parts[0]))] = strings.TrimSpace(parts[1])
		}
	}
	return styles
}

type legacyTypographyRule struct {
	Selectors []string
	Styles    map[string]string
}

type legacyTypographyContext struct {
	Variables map[string]string
	Rules     []legacyTypographyRule
}

func legacyTypographyContextForDocument(root *xhtml.Node) legacyTypographyContext {
	context := legacyTypographyContext{Variables: map[string]string{}}
	var sheets strings.Builder
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node == nil {
			return
		}
		if node.Type == xhtml.ElementNode && strings.EqualFold(node.Data, "style") {
			sheets.WriteString(legacyNodeTextPreservingWhitespace(node))
			sheets.WriteByte('\n')
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	css := legacyCSSCommentPattern.ReplaceAllString(sheets.String(), "")
	for _, match := range legacyCSSVarPattern.FindAllStringSubmatch(css, -1) {
		if len(match) == 3 {
			context.Variables[strings.TrimSpace(match[1])] = strings.TrimSpace(match[2])
		}
	}
	for _, match := range legacyCSSRulePattern.FindAllStringSubmatch(css, -1) {
		if len(match) != 3 {
			continue
		}
		declarations := legacyStyleDeclarations(match[2])
		styles := map[string]string{}
		for _, property := range []string{"font-family", "letter-spacing"} {
			if value := strings.TrimSpace(declarations[property]); value != "" {
				styles[property] = value
			}
		}
		if len(styles) == 0 {
			continue
		}
		selectors := strings.Split(match[1], ",")
		for index := range selectors {
			selectors[index] = strings.TrimSpace(selectors[index])
		}
		context.Rules = append(context.Rules, legacyTypographyRule{Selectors: selectors, Styles: styles})
	}
	return context
}

func (context legacyTypographyContext) resolveValue(value string) string {
	return legacyCSSVarUsePattern.ReplaceAllStringFunc(value, func(use string) string {
		match := legacyCSSVarUsePattern.FindStringSubmatch(use)
		if len(match) != 2 {
			return use
		}
		return firstNonEmptyString(context.Variables[match[1]], use)
	})
}

func legacyTypographySelectorMatches(node *xhtml.Node, selector string) bool {
	selector = strings.TrimSpace(selector)
	if node == nil || node.Type != xhtml.ElementNode || selector == "" || strings.ContainsAny(selector, " >+~[") {
		return false
	}
	if colon := strings.Index(selector, ":"); colon >= 0 {
		selector = selector[:colon]
	}
	id := ""
	if hash := strings.Index(selector, "#"); hash >= 0 {
		id = selector[hash+1:]
		selector = selector[:hash]
	}
	parts := strings.Split(selector, ".")
	tag := strings.TrimSpace(parts[0])
	if tag != "" && tag != "*" && !strings.EqualFold(tag, node.Data) {
		return false
	}
	if id != "" && legacyNodeAttr(node, "id") != id {
		return false
	}
	for _, className := range parts[1:] {
		if className = strings.TrimSpace(className); className == "" || !legacyNodeHasClass(node, className) {
			return false
		}
	}
	return true
}

func (context legacyTypographyContext) resolvedForNode(node *xhtml.Node) (fontFamily, letterSpacing string, ok bool) {
	var ancestry []*xhtml.Node
	for current := node; current != nil; current = current.Parent {
		if current.Type == xhtml.ElementNode {
			ancestry = append(ancestry, current)
		}
	}
	computed := map[string]string{}
	for index := len(ancestry) - 1; index >= 0; index-- {
		current := ancestry[index]
		for _, rule := range context.Rules {
			matched := false
			for _, selector := range rule.Selectors {
				if legacyTypographySelectorMatches(current, selector) {
					matched = true
					break
				}
			}
			if matched {
				for property, value := range rule.Styles {
					computed[property] = context.resolveValue(value)
				}
			}
		}
		for _, property := range []string{"font-family", "letter-spacing"} {
			if value := strings.TrimSpace(legacyStyleMap(current)[property]); value != "" {
				computed[property] = context.resolveValue(value)
			}
		}
	}
	fontFamily = strings.TrimSpace(strings.NewReplacer(`"`, "", `'`, "").Replace(computed["font-family"]))
	letterSpacing = strings.ToLower(strings.TrimSpace(firstNonEmptyString(computed["letter-spacing"], "normal")))
	if strings.Contains(fontFamily, "var(") || !deckFontPattern.MatchString(fontFamily) || !deckTrackingPattern.MatchString(letterSpacing) {
		return "", "", false
	}
	return fontFamily, letterSpacing, true
}

func legacyDataDeckElements(root *xhtml.Node, allowedRefs map[string]struct{}, assets []artifactAsset, typography legacyTypographyContext) ([]deckElement, bool) {
	var nodes []*xhtml.Node
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && legacyNodeAttr(node, "data-deck-element") != "" {
			nodes = append(nodes, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	if len(nodes) == 0 || len(nodes) > deckSlideMaxElements {
		return nil, false
	}

	elements := make([]deckElement, 0, len(nodes))
	seen := map[string]struct{}{}
	for index, node := range nodes {
		styles := legacyStyleMap(node)
		id := firstNonEmptyString(legacyNodeAttr(node, "data-deck-element"), legacyNodeAttr(node, "id"), fmt.Sprintf("element-%d", index+1))
		if !deckIdentifierPattern.MatchString(id) {
			return nil, false
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, false
		}
		seen[id] = struct{}{}
		typ := strings.ToLower(legacyNodeAttr(node, "data-deck-type"))
		x, okX := legacyDeckNumber(firstNonEmptyString(legacyNodeAttr(node, "data-deck-x"), styles["left"]))
		y, okY := legacyDeckNumber(firstNonEmptyString(legacyNodeAttr(node, "data-deck-y"), styles["top"]))
		width, okWidth := legacyDeckNumber(firstNonEmptyString(legacyNodeAttr(node, "data-deck-width"), styles["width"]))
		height, okHeight := legacyDeckNumber(firstNonEmptyString(legacyNodeAttr(node, "data-deck-height"), styles["height"]))
		if !okX || !okY || !okWidth || !okHeight {
			return nil, false
		}
		element := deckElement{ID: id, Type: typ, X: x, Y: y, Width: width, Height: height, Opacity: 1}
		if value, ok := legacyDeckNumber(styles["opacity"]); ok {
			element.Opacity = value
		}
		if value, err := strconv.Atoi(styles["z-index"]); err == nil {
			element.Z = value
		}
		if rotation := regexp.MustCompile(`(?i)rotate\(\s*(-?[0-9.]+)deg\s*\)`).FindStringSubmatch(styles["transform"]); len(rotation) == 2 {
			element.Rotation, _ = strconv.ParseFloat(rotation[1], 64)
		}
		switch typ {
		case "text":
			fontFamily, letterSpacing, typographyOK := typography.resolvedForNode(node)
			if !typographyOK {
				return nil, false
			}
			element.Text = trimForStorage(strings.TrimSpace(legacyNodeText(node)), deckElementTextMaxRunes)
			element.FontSize, _ = legacyDeckNumber(firstNonEmptyString(styles["font-size"], "32"))
			element.FontFamily = fontFamily
			element.FontWeight = legacyFontWeight(firstNonEmptyString(styles["font-weight"], "400"))
			element.Color = firstNonEmptyString(styles["color"], "#ffffff")
			element.TextAlign = strings.ToLower(firstNonEmptyString(styles["text-align"], "left"))
			element.LetterSpacing = letterSpacing
			if value, ok := legacyDeckNumber(styles["line-height"]); ok {
				element.LineHeight = value
			} else {
				element.LineHeight = 1.08
			}
		case "shape":
			element.Shape = strings.ToLower(firstNonEmptyString(legacyNodeAttr(node, "data-deck-shape"), "rectangle"))
			if strings.Contains(styles["border-radius"], "50%") {
				element.Shape = "ellipse"
			}
			element.Fill = firstNonEmptyString(styles["fill"], styles["background-color"], styles["background"], "transparent")
			if strings.EqualFold(strings.TrimSpace(element.Fill), "none") {
				element.Fill = "transparent"
			}
		case "image":
			ref, name, ok := legacyImageSource(legacyNodeAttr(node, "src"), allowedRefs)
			if !ok {
				ref, name, ok = legacyFigAsset(node, assets)
			}
			if !ok {
				if source := legacyDescendantImageSource(node); source != "" {
					ref, name, ok = legacyImageSource(source, allowedRefs)
				}
			}
			if !ok {
				return nil, false
			}
			element.Ref, element.Name = ref, name
			element.Fit = firstNonEmptyString(styles["object-fit"], "cover")
		default:
			return nil, false
		}
		elements = append(elements, element)
	}
	return elements, true
}

func legacyFigAsset(node *xhtml.Node, assets []artifactAsset) (string, string, bool) {
	figPattern := regexp.MustCompile(`^fig-([1-9][0-9]*)$`)
	for _, className := range strings.Fields(legacyNodeAttr(node, "class")) {
		if figPattern.MatchString(className) {
			prefix := strings.ToLower(className + ".")
			for _, asset := range assets {
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(asset.Name)), prefix) && validBlobRef(asset.Ref) && artifactAssetIsEditableImage(asset) {
					return asset.Ref, asset.Name, true
				}
			}
		}
	}
	return "", "", false
}

func legacyDescendantImageSource(node *xhtml.Node) string {
	var source string
	var walk func(*xhtml.Node)
	walk = func(current *xhtml.Node) {
		if source != "" || current == nil {
			return
		}
		if current.Type == xhtml.ElementNode {
			if candidate := legacyNodeAttr(current, "src"); candidate != "" {
				source = candidate
				return
			}
			if match := regexp.MustCompile(`(?i)url\(['"]?(data:image/[^)'\"]+)['"]?\)`).FindStringSubmatch(legacyStyleMap(current)["background-image"]); len(match) == 2 {
				source = match[1]
				return
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return source
}

func legacyDeckNumber(value string) (float64, bool) {
	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "px"))
	if value == "" {
		return 0, false
	}
	number, err := strconv.ParseFloat(value, 64)
	return number, err == nil && !math.IsNaN(number) && !math.IsInf(number, 0)
}

func legacyFontWeight(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "normal":
		return 400
	case "bold", "bolder":
		return 700
	}
	weight, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 400
	}
	return weight
}

func legacyImageSource(source string, allowedRefs map[string]struct{}) (string, string, bool) {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(strings.ToLower(source), "data:image/") {
		comma := strings.IndexByte(source, ',')
		if comma < 0 || !strings.Contains(strings.ToLower(source[:comma]), ";base64") {
			return "", "", false
		}
		data, err := base64.StdEncoding.DecodeString(source[comma+1:])
		if err != nil || len(data) == 0 || len(data) > deckImageUploadMaxBytes {
			return "", "", false
		}
		digest := sha256.Sum256(data)
		ref := hex.EncodeToString(digest[:])
		if _, allowed := allowedRefs[ref]; !allowed {
			return "", "", false
		}
		return ref, "deck-image", true
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Path != "/artifacts/blob" {
		return "", "", false
	}
	ref := strings.TrimSpace(parsed.Query().Get("ref"))
	if _, allowed := allowedRefs[ref]; !allowed {
		return "", "", false
	}
	return ref, strings.TrimSpace(parsed.Query().Get("name")), true
}

func legacyNodeText(node *xhtml.Node) string {
	var builder strings.Builder
	var walk func(*xhtml.Node)
	walk = func(current *xhtml.Node) {
		if current.Type == xhtml.TextNode {
			if text := strings.TrimSpace(current.Data); text != "" {
				if builder.Len() > 0 {
					builder.WriteByte(' ')
				}
				builder.WriteString(text)
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return builder.String()
}
