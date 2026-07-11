package main

import (
	"os"
	"strings"
	"testing"
)

// Pins for the theme default + persistence contract (founder call 2026-07-10):
// LIGHT is the product default (OS preference only honored for an explicit
// "system" choice), the choice persists to localStorage on every apply, and a
// signed-in user's choice syncs to their account via POST /auth/theme and is
// re-applied from /auth/me at session bootstrap. Also pins the lobby ink-
// channel tokens that keep the pre-join Rooms lobby theme-aware while the
// in-call stage stays true black.

func readIndexForTheme(t *testing.T) string {
	t.Helper()
	html, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(html)
}

func TestIndexThemeDefaultsToLight(t *testing.T) {
	html := readIndexForTheme(t)

	// pre-paint script: absent/unknown key = light; matchMedia consulted ONLY
	// for a stored 'system' choice
	if !strings.Contains(html, "if (theme === 'system') {") {
		t.Error("pre-paint script must honor an explicit stored 'system' choice")
	}
	if !strings.Contains(html, "} else if (theme !== 'light' && theme !== 'dark') {\n          theme = 'light'\n        }") {
		t.Error("pre-paint script must default an absent/unknown stored theme to LIGHT")
	}

	// runtime resolution mirrors it
	body := functionBody(html, "function storedThemePreference()")
	if body == "" {
		t.Fatal("storedThemePreference missing")
	}
	if !strings.Contains(body, "stored === 'system' ? stored : 'light'") {
		t.Error("storedThemePreference must default to 'light', not 'system'")
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

func TestIndexLobbyThemeTokens(t *testing.T) {
	html := readIndexForTheme(t)

	// the lobby ink channel: ink on paper by default, white on black in dark
	if !strings.Contains(html, "--lob-fg: 17, 17, 20;") {
		t.Error("lobby light ink-channel token missing")
	}
	if !strings.Contains(html, "[data-theme=\"dark\"] .room-empty {") || !strings.Contains(html, "--lob-fg: 255, 255, 255;") {
		t.Error("lobby dark ink-channel override missing")
	}
	// the pre-join ground follows the theme; the in-call stage keeps --bg-stage
	roomEmptyIdx := strings.Index(html, "background: var(--bg-app);\n        border-radius: 0;")
	if roomEmptyIdx == -1 {
		t.Error(".room-empty must ground on var(--bg-app) so the lobby follows the theme")
	}
	if !strings.Contains(html, "background: var(--bg-stage);\n        /* tiles sit directly on the light canvas") {
		t.Error(".hearth-stage must keep the true-black --bg-stage ground for in-call video")
	}
}
