package main

// deck_theme.go owns the Deck Studio theme catalog and the layout templates
// the editor applies. Themes are a closed, server-canonical set: the client
// picks an id and the catalog supplies the palette, so a stale client can
// never write drifted colors into a scene. Layouts are plain element sets in
// the existing text|image|shape model at the 1920x1080 canvas — the client
// re-ids them per slide and drops them into an ordinary PATCH.

import "strings"

// deckTheme is the resolved palette stored inside a deckDocument.
type deckTheme struct {
	ID         string `json:"id"`
	Background string `json:"background"`
	Accent     string `json:"accent"`
	TextColor  string `json:"textColor"`
	FontFamily string `json:"fontFamily"`
}

// deckThemeView adds the human name the theme picker shows.
type deckThemeView struct {
	deckTheme
	Name string `json:"name"`
}

// deckLayoutView is one applicable layout: element templates already carrying
// the theme's colors. Element ids are template-local (`title`, `body`, ...);
// the client suffixes them with the slide id before saving so the deck-wide
// unique-id rule holds.
type deckLayoutView struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Elements    []deckElement `json:"elements"`
}

const deckDefaultThemeID = "graphite"

// deckBuiltInThemes is the closed catalog. Graphite is the legacy default and
// keeps the #111111 dark ground the editor has always shown; the one orange
// #FF5A19 is the accent everywhere it appears (design canon: one orange).
var deckBuiltInThemes = []deckThemeView{
	{Name: "Graphite", deckTheme: deckTheme{ID: "graphite", Background: "#111111", Accent: "#FF5A19", TextColor: "#FFFFFF", FontFamily: "Arial"}},
	{Name: "Putty", deckTheme: deckTheme{ID: "putty", Background: "#DDD4C6", Accent: "#FF5A19", TextColor: "#1A1D23", FontFamily: "Georgia"}},
	{Name: "Ember", deckTheme: deckTheme{ID: "ember", Background: "#FF5A19", Accent: "#111111", TextColor: "#111111", FontFamily: "Arial"}},
}

// deckThemeByID resolves a built-in theme; unknown ids report false.
func deckThemeByID(id string) (deckTheme, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, theme := range deckBuiltInThemes {
		if theme.ID == id {
			return theme.deckTheme, true
		}
	}
	return deckTheme{}, false
}

// resolveDeckTheme maps an empty id (legacy scene, or a client that never sent
// one) to graphite and every known id to its canonical values. Callers
// validate unknown ids before resolving; an unknown id here falls back to the
// default rather than persisting a palette the catalog does not own.
func resolveDeckTheme(theme deckTheme) deckTheme {
	if resolved, ok := deckThemeByID(theme.ID); ok {
		return resolved
	}
	resolved, _ := deckThemeByID(deckDefaultThemeID)
	return resolved
}

// deckThemes lists the catalog for the theme picker.
func deckThemes() []deckThemeView {
	return append([]deckThemeView(nil), deckBuiltInThemes...)
}

// deckWithThemeDefaults returns a copy of the deck whose empty slide
// backgrounds and empty text colors carry the theme values, for the HTML and
// PPTX projections. The stored scene is never rewritten by this.
func deckWithThemeDefaults(deck deckDocument) deckDocument {
	theme := resolveDeckTheme(deck.Theme)
	deck.Theme = theme
	slides := make([]deckSlide, len(deck.Slides))
	for slideIndex, slide := range deck.Slides {
		slide.Background = firstNonEmptyString(slide.Background, theme.Background)
		elements := make([]deckElement, len(slide.Elements))
		for elementIndex, element := range slide.Elements {
			if element.Type == "text" {
				element.Color = firstNonEmptyString(element.Color, theme.TextColor)
				element.FontFamily = firstNonEmptyString(element.FontFamily, theme.FontFamily)
			}
			elements[elementIndex] = element
		}
		slide.Elements = elements
		slides[slideIndex] = slide
	}
	deck.Slides = slides
	return deck
}

func deckLayoutText(id, text string, x, y, width, height, fontSize float64, weight int, align string, theme deckTheme) deckElement {
	element := defaultDeckTextElement(id, text, x, y, width, height, fontSize, weight)
	element.Color = theme.TextColor
	element.FontFamily = theme.FontFamily
	element.TextAlign = align
	if fontSize <= 48 {
		element.LineHeight = 1.3
	}
	return element
}

func deckLayoutAccentRule(id string, x, y, width float64, theme deckTheme) deckElement {
	return deckElement{ID: id, Type: "shape", X: x, Y: y, Width: width, Height: 8, Z: 1, Opacity: 1, Shape: "rectangle", Fill: theme.Accent}
}

// deckLayouts returns the four layouts resolved against one theme. The
// image-left layout ships an `image-slot` shape because an image element
// needs an attached blob ref; the client swaps the slot for the uploaded image
// (same geometry) once one exists.
func deckLayouts(theme deckTheme) []deckLayoutView {
	theme = resolveDeckTheme(theme)
	return []deckLayoutView{
		{ID: "title", Name: "Title", Description: "Centered title with a subtitle line.", Elements: []deckElement{
			deckLayoutText("title", "Title", 120, 340, 1680, 260, 104, 700, "center", theme),
			deckLayoutAccentRule("accent-rule", 860, 620, 200, theme),
			deckLayoutText("subtitle", "Subtitle", 120, 660, 1680, 120, 40, 400, "center", theme),
		}},
		{ID: "title-body", Name: "Title + body", Description: "Heading over one body column.", Elements: []deckElement{
			deckLayoutText("title", "Title", 120, 100, 1680, 180, 72, 700, "left", theme),
			deckLayoutAccentRule("accent-rule", 120, 300, 160, theme),
			deckLayoutText("body", "Body", 120, 360, 1680, 600, 40, 400, "left", theme),
		}},
		{ID: "two-column", Name: "Two column", Description: "Heading over two equal columns.", Elements: []deckElement{
			deckLayoutText("title", "Title", 120, 100, 1680, 160, 64, 700, "left", theme),
			deckLayoutAccentRule("accent-rule", 120, 280, 160, theme),
			deckLayoutText("column-left", "Left column", 120, 340, 800, 620, 36, 400, "left", theme),
			deckLayoutText("column-right", "Right column", 1000, 340, 800, 620, 36, 400, "left", theme),
		}},
		{ID: "image-left", Name: "Image left", Description: "Full-height image slot on the left, copy on the right.", Elements: []deckElement{
			{ID: "image-slot", Type: "shape", X: 0, Y: 0, Width: 880, Height: 1080, Z: 0, Opacity: 0.25, Shape: "rectangle", Fill: theme.Accent},
			deckLayoutText("title", "Title", 980, 160, 820, 260, 64, 700, "left", theme),
			deckLayoutText("body", "Body", 980, 460, 820, 480, 36, 400, "left", theme),
		}},
	}
}
