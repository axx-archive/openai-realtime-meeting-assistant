package main

import (
	"os"
	"strings"
	"testing"
)

// Pins for the theme default + persistence contract. DARK is the product
// default (AJ, Wave 10, 2026-09-02: "a beautiful product with a light/dark
// mode, dark as default" — supersedes the light default of 2026-07-10; a
// saved 'light' still wins; OS preference only honored for an explicit
// "system" choice), the choice persists to localStorage on every apply, and a
// signed-in user's choice syncs to their account via POST /auth/theme and is
// re-applied from /auth/me at session bootstrap. Also pins the lobby ink-
// channel tokens and the room-canvas token so both pre-join and in-call room
// grounds follow the selected theme while video media stays true black.

func readIndexForTheme(t *testing.T) string {
	t.Helper()
	html, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(html)
}

func TestIndexThemeDefaultsToDark(t *testing.T) {
	html := readIndexForTheme(t)

	// pre-paint script: absent/unknown key = dark; matchMedia consulted ONLY
	// for a stored 'system' choice; a stored 'light' is honored as-is
	if !strings.Contains(html, "if (theme === 'system') {") {
		t.Error("pre-paint script must honor an explicit stored 'system' choice")
	}
	if !strings.Contains(html, "} else if (theme !== 'light' && theme !== 'dark') {\n          theme = 'dark'\n        }") {
		t.Error("pre-paint script must default an absent/unknown stored theme to DARK")
	}
	if !strings.Contains(html, "if (theme === 'dark') document.documentElement.dataset.theme = 'dark'") {
		t.Error("pre-paint script must stamp data-theme=dark before first paint")
	}
	// the tab strip colour before any script runs is the dark ground
	if !strings.Contains(html, `<meta name="theme-color" content="#000000">`) {
		t.Error("theme-color meta must start on the dark ground")
	}

	// runtime resolution mirrors it
	body := functionBody(html, "function storedThemePreference()")
	if body == "" {
		t.Fatal("storedThemePreference missing")
	}
	if !strings.Contains(body, "stored === 'system' ? stored : 'dark'") {
		t.Error("storedThemePreference must default to 'dark', not 'system' or 'light'")
	}
	if !strings.Contains(body, "stored === 'light' || stored === 'dark' || stored === 'system' ? stored") {
		t.Error("a saved 'light' choice must still win over the dark default")
	}
}

func TestIndexThemePersistsToAccount(t *testing.T) {
	html := readIndexForTheme(t)

	apply := functionBody(html, "function applyTheme(theme)")
	if !strings.Contains(apply, "pushThemePref(next)") {
		t.Error("applyTheme must sync the choice to the account")
	}
	push := functionBody(html, "function pushThemePref(mode)")
	if !strings.Contains(push, "'/auth/theme'") {
		t.Error("pushThemePref must POST /auth/theme")
	}
	setPref := functionBody(html, "function setThemePreference(mode)")
	if !strings.Contains(setPref, "'system'") || !strings.Contains(setPref, "pushThemePref('system')") {
		t.Error("an explicit system choice must be stored and synced, not dropped")
	}
	boot := functionBody(html, "async function refreshAuthState()")
	if !strings.Contains(boot, "applyAccountThemePref(authedUser?.themePref)") {
		t.Error("session bootstrap must re-apply the account theme preference")
	}
	accountApply := functionBody(html, "function applyAccountThemePref(mode)")
	if accountApply == "" {
		t.Fatal("applyAccountThemePref missing")
	}
	if strings.Contains(accountApply, "pushThemePref(") {
		t.Error("applyAccountThemePref must not echo a POST back to the server")
	}
}

func TestIndexRoomThemeTokens(t *testing.T) {
	html := readIndexForTheme(t)

	// the lobby ink channel: ink on paper by default, white on black in dark
	if !strings.Contains(html, "--lob-fg: 17, 17, 20;") {
		t.Error("lobby light ink-channel token missing")
	}
	if !strings.Contains(html, "[data-theme=\"dark\"] .room-empty {") || !strings.Contains(html, "--lob-fg: 255, 255, 255;") {
		t.Error("lobby dark ink-channel override missing")
	}
	// The pre-join ground follows the theme.
	roomEmptyIdx := strings.Index(html, "background: var(--bg-app);\n        border-radius: 0;")
	if roomEmptyIdx == -1 {
		t.Error(".room-empty must ground on var(--bg-app) so the lobby follows the theme")
	}
	// The in-call canvas is paper in light mode and true black in dark mode.
	for _, want := range []string{
		"--bg-room-canvas: var(--paper-100);",
		"--bg-room-canvas: #000000;",
		"background: var(--bg-room-canvas);",
		"--bg-stage: #000000;",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("room theme contract missing %q", want)
		}
	}
	if strings.Contains(html, "#appShell.is-in-room .hearth-presentation,\n        #appShell.is-authed[data-tool=\"room\"]:not(.is-in-room) .hearth-presentation {\n          background: var(--bg-stage);") {
		t.Error("in-call .hearth-presentation must not force the true-black media token in light mode")
	}
	if !strings.Contains(html, "color: var(--text-3);") {
		t.Error("room canvas metadata must use theme-aware text color")
	}
}

func TestIndexDarkThemeUsesNativeParityBlackCanvas(t *testing.T) {
	html := readIndexForTheme(t)
	darkStart := strings.Index(html, "[data-theme=\"dark\"] {")
	if darkStart == -1 {
		t.Fatal("dark theme block missing")
	}
	darkEnd := strings.Index(html[darkStart:], "\n      }")
	if darkEnd == -1 {
		t.Fatal("dark theme block is not bounded")
	}
	dark := html[darkStart : darkStart+darkEnd]
	for _, want := range []string{
		"--bg-app: #000000;",
		// AJ ratified dark ladder v2 2026-09-02: the canvas stays true black;
		// the chrome is one plane above it, cards and wells one step each
		// above that (was #050506 / #0A0A0C / #141416 and rgba(8, 8, 10, 0.82)).
		"--surface-1: #0E0E10;",
		"--surface-2: #151518;",
		"--surface-3: #1C1C20;",
		"--glass-chrome: rgba(14, 14, 16, 0.86);",
		"--glass-panel: rgba(8, 8, 10, 0.62);",
	} {
		if !strings.Contains(dark, want) {
			t.Errorf("native-parity dark ramp missing %q", want)
		}
	}

	for _, selector := range []string{
		`[data-theme="dark"] #chatTool .chat-threads`,
		`[data-theme="dark"] #chatTool .chat-conversation`,
		`[data-theme="dark"] #chatTool .chat-convo-head`,
	} {
		if !strings.Contains(html, selector) {
			t.Errorf("dark chat structural black override missing %q", selector)
		}
	}
	if !strings.Contains(html, `background: var(--bg-app);`) {
		t.Error("dark chat structural panes must use the true-black app canvas")
	}
}
