package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestDeckThemeLegacyImportDefaultsToGraphite(t *testing.T) {
	entry := meetingMemoryEntry{ID: "deck-legacy", Kind: meetingMemoryKindOSArtifact, Text: faithfulDeckHTML, Metadata: map[string]string{"type": artifactTypeHTMLDeck}}
	deck, imported, quality, err := loadDeckDocument(entry)
	if err != nil || !imported || quality != "faithful" {
		t.Fatalf("legacy load: imported=%v quality=%q err=%v", imported, quality, err)
	}
	graphite, _ := deckThemeByID("graphite")
	if deck.Theme != graphite || deck.Theme.Background != "#111111" {
		t.Fatalf("legacy deck theme=%+v want graphite with the #111111 ground", deck.Theme)
	}
	// A slide without its own background paints the theme ground in the HTML
	// projection; an explicit slide background still wins.
	deck.Slides[0].Background = ""
	html := compileDeckDocumentHTML(deck, "Like a Farmer")
	if !strings.Contains(html, `style="background:#111111"`) {
		t.Fatalf("themed HTML lost the graphite ground: %s", html)
	}
	if _, ok := deckThemeByID("neon"); ok {
		t.Fatal("unknown theme id resolved")
	}
	if resolveDeckTheme(deckTheme{ID: "EMBER"}).Background != "#FF5A19" {
		t.Fatal("theme ids should resolve case-insensitively to canonical values")
	}
}

func TestDeckThemePatchPersistsAndRejectsUnknown(t *testing.T) {
	cookies, artifact := setupDeckEditorHTTPTest(t, LegacyCompatibleObjectAuthorizer{})
	get := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/deck?id="+artifact.ID, "", cookies, deckEditorHandler)
	var loaded struct {
		Deck    deckDocument     `json:"deck"`
		Themes  []deckThemeView  `json:"themes"`
		Layouts []deckLayoutView `json:"layouts"`
	}
	if get.Code != http.StatusOK || json.Unmarshal(get.Body.Bytes(), &loaded) != nil {
		t.Fatalf("GET status=%d body=%s", get.Code, get.Body.String())
	}
	if loaded.Deck.Theme.ID != "graphite" || len(loaded.Themes) != 3 || len(loaded.Layouts) != 4 {
		t.Fatalf("GET catalogs: theme=%+v themes=%d layouts=%d", loaded.Deck.Theme, len(loaded.Themes), len(loaded.Layouts))
	}
	// The client sends only the id; the server fills the canonical palette.
	loaded.Deck.Theme = deckTheme{ID: "ember", Background: "#000000"}
	body, _ := json.Marshal(map[string]any{"artifactId": artifact.ID, "expectedVersion": artifactVersion(artifact), "deck": loaded.Deck})
	patch := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/deck", string(body), cookies, deckEditorHandler)
	var saved struct {
		Deck deckDocument `json:"deck"`
	}
	if patch.Code != http.StatusOK || json.Unmarshal(patch.Body.Bytes(), &saved) != nil || saved.Deck.Theme.ID != "ember" || saved.Deck.Theme.Background != "#FF5A19" {
		t.Fatalf("PATCH ember status=%d body=%s", patch.Code, patch.Body.String())
	}
	reload := artifactAuthorizationRequest(t, http.MethodGet, "/artifacts/deck?id="+artifact.ID, "", cookies, deckEditorHandler)
	loaded.Deck = deckDocument{}
	if reload.Code != http.StatusOK || json.Unmarshal(reload.Body.Bytes(), &loaded) != nil || loaded.Deck.Theme.ID != "ember" || loaded.Deck.Theme.TextColor != "#111111" {
		t.Fatalf("reload status=%d body=%s", reload.Code, reload.Body.String())
	}
	if loaded.Layouts[0].Elements[0].Color != "#111111" {
		t.Fatalf("layouts should be resolved against the deck's theme: %+v", loaded.Layouts[0].Elements[0])
	}
	stored, _ := kanbanApp.osArtifactByID(artifact.ID)
	if !strings.Contains(stored.Text, `style="background:#101014"`) {
		t.Fatalf("explicit slide background must survive a theme change: %s", stored.Text)
	}

	loaded.Deck.Theme = deckTheme{ID: "neon"}
	body, _ = json.Marshal(map[string]any{"artifactId": artifact.ID, "expectedVersion": artifactVersion(stored), "deck": loaded.Deck})
	bad := artifactAuthorizationRequest(t, http.MethodPatch, "/artifacts/deck", string(body), cookies, deckEditorHandler)
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "theme") {
		t.Fatalf("unknown theme status=%d body=%s want 400", bad.Code, bad.Body.String())
	}
}

func TestDeckLayoutsCatalogYieldsValidElements(t *testing.T) {
	for _, theme := range deckThemes() {
		layouts := deckLayouts(theme.deckTheme)
		ids := map[string]bool{}
		for _, layout := range layouts {
			ids[layout.ID] = true
			if len(layout.Elements) == 0 || layout.Name == "" {
				t.Fatalf("layout %q is empty", layout.ID)
			}
			deck := deckDocument{SchemaVersion: 1, Width: 1920, Height: 1080, Theme: theme.deckTheme, Slides: []deckSlide{{ID: "slide-1", Elements: layout.Elements}}}
			if err := validateDeckDocument(deck, nil); err != nil {
				t.Fatalf("layout %q under %s does not validate: %v", layout.ID, theme.ID, err)
			}
			for _, element := range layout.Elements {
				if element.Type == "text" && (element.Color != theme.TextColor || element.FontFamily != theme.FontFamily) {
					t.Fatalf("layout %q text element %q ignores theme %s: %+v", layout.ID, element.ID, theme.ID, element)
				}
			}
		}
		for _, want := range []string{"title", "title-body", "two-column", "image-left"} {
			if !ids[want] {
				t.Fatalf("layout %q missing under %s: %v", want, theme.ID, ids)
			}
		}
	}
}
