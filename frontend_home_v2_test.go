package main

// Home v2 (AJ 2026-09-02): Home becomes "today" and keeps its calm. Three
// quiet sections under the composer — `today`, `since you were away`,
// `continue` — each a mono kicker over hairline rows. These pins hold the
// contract a later polish could silently drop: sections render only when
// they have rows (no empty states on Home), three rows per section at most,
// no tiles or cards, the only ember on Home is the live dot, and the
// greeting + composer markup stays exactly as it was.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func homeV2Index(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func homeV2Markup(t *testing.T, html string) string {
	t.Helper()
	start := strings.Index(html, `<section id="officeTool"`)
	end := strings.Index(html, `id="briefOverlay"`)
	if start == -1 || end == -1 || start > end {
		t.Fatal("could not slice the office home markup")
	}
	return html[start:end]
}

func TestIndexHomeV2SectionsRenderOnlyWithRows(t *testing.T) {
	html := homeV2Index(t)
	home := homeV2Markup(t, html)

	// the three sections exist, in order, and start hidden
	todayAt := strings.Index(home, `<div id="homeToday" class="home-day" data-section="today" aria-label="Today" hidden></div>`)
	awayAt := strings.Index(home, `<div id="homeAway" class="home-day" data-section="since-you-were-away" aria-label="Since you were away" hidden></div>`)
	continueAt := strings.Index(home, `<div id="homeContinuity" class="home-continuity"`)
	if todayAt == -1 || awayAt == -1 || continueAt == -1 {
		t.Fatalf("home v2 sections missing (today=%d away=%d continue=%d)", todayAt, awayAt, continueAt)
	}
	if !(todayAt < awayAt && awayAt < continueAt) {
		t.Error("home v2 sections must read today · since you were away · continue")
	}
	composerAt := strings.Index(home, `<form id="homeScoutComposer"`)
	if composerAt == -1 || composerAt > todayAt {
		t.Error("the sections belong below the composer")
	}

	// a section with nothing renders nothing — no empty-state copy anywhere
	paint := functionBody(html, "function paintHomeDaySection(section, kicker, rows)")
	if paint == "" {
		t.Fatal("paintHomeDaySection missing")
	}
	for _, want := range []string{"section.hidden = !rows.length", "section.replaceChildren()", "heading.className = 'home-day__kicker'"} {
		if !strings.Contains(paint, want) {
			t.Errorf("paintHomeDaySection must %q", want)
		}
	}
	for _, banned := range []string{"nothing today", "all clear", "no unread", "empty"} {
		if strings.Contains(strings.ToLower(paint), banned) {
			t.Errorf("Home sections carry no empty state, found %q", banned)
		}
	}
	// the kickers are the founder's three words, lowercase, mono
	for _, want := range []string{"paintHomeDaySection(homeToday, 'today',", "paintHomeDaySection(homeAway, 'since you were away',", "kicker.textContent = 'continue'"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing kicker wiring %q", want)
		}
	}
	if !strings.Contains(html, ".home-day__kicker,\n      .home-continuity__kicker {") || !strings.Contains(html, "font: var(--type-label);\n        letter-spacing: var(--track-label);\n      }\n      .home-day__row {") {
		t.Error("the kickers must set in the mono label type")
	}
	// the continue section keeps its existing row style and gains the kicker
	if !strings.Contains(functionBody(html, "function renderHomeSnapshot()"), "homeContinuity.replaceChildren(kicker, ...items.map(homeContinuityNode))") {
		t.Error("continue must render its kicker ahead of the existing continuity rows")
	}
}

func TestIndexHomeV2ThreeRowCapAndRealDestinations(t *testing.T) {
	html := homeV2Index(t)
	if !strings.Contains(html, "var HOME_DAY_ROW_CAP = 3") {
		t.Fatal("the per-section cap is three rows")
	}
	for _, fn := range []string{
		"function homeDayTodayRows()",
		"function homeDayAwayRows(now = Date.now())",
		"function homeContinuityItems()",
	} {
		body := functionBody(html, fn)
		if body == "" {
			t.Errorf("%s missing", fn)
			continue
		}
		if !strings.Contains(body, ".slice(0, HOME_DAY_ROW_CAP || 3)") {
			t.Errorf("%s must cap at HOME_DAY_ROW_CAP", fn)
		}
	}
	// since you were away ranks across threads, finished work and alerts,
	// most recent first, and the baseline is Home's own last-seen stamp
	away := functionBody(html, "function homeDayAwayRows(now = Date.now())")
	for _, want := range []string{"homeDayUnreadThreadRows(now)", "homeDayFinishedWorkRows(since, now)", "homeDayNotificationRows(since, now)", "right.at - left.at", "homeDaySinceBaseline()"} {
		if !strings.Contains(away, want) {
			t.Errorf("homeDayAwayRows must %q", want)
		}
	}
	if !strings.Contains(html, "var HOME_DAY_SEEN_KEY = 'home.lastSeenAt'") {
		t.Error("the last-seen stamp lives at home.lastSeenAt")
	}
	if !strings.Contains(functionBody(html, "function homeDaySinceBaseline()"), "Date.now() - 24 * 60 * 60 * 1000") {
		t.Error("the last-seen baseline falls back to 24 h")
	}
	// every row opens the real thing through the shell's own navigation
	for fn, wants := range map[string][]string{
		"function homeDayLiveRows()":                                {"selectLobbyRoom(String(room.id || 'office'))", "selectPD1Destination('Video')"},
		"function homeDayScheduledRows(now = Date.now())":           {"selectLobbyRoom(roomId)", "selectPD1Destination('Video')", "lobbyUpcomingMeetings(now)", "lobbyScheduleWhenParts(startMs, now).time", "attendees"},
		"function homeDayUnreadThreadRows(now = Date.now())":        {"chatThreadHasUnread(thread)", "openHomeDestination({ route: 'thread', threadId: String(thread.id) })"},
		"function homeDayFinishedWorkRows(since, now = Date.now())": {"project.result", "openHomeDestination({ route: 'work', projectId: String(project.id) })"},
		"function homeDayNotificationRows(since, now = Date.now())": {"['mention', 'decision']", "openNotificationEntry(entry)"},
	} {
		body := functionBody(html, fn)
		if body == "" {
			t.Errorf("%s missing", fn)
			continue
		}
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s must %q", fn, want)
			}
		}
	}
	if !strings.Contains(functionBody(html, "function openHomeDestination(destination)"), "selectPD1Destination('Work', { projectId: String(destination?.projectId || '') })") {
		t.Error("a Work destination opens the project in Work")
	}
	// never join from Home — the live row and the scheduled row land on the lobby
	for _, banned := range []string{"joinRoom(", "lobbyUpcomingJoin("} {
		if strings.Contains(functionBody(html, "function homeDayLiveRows()"), banned) || strings.Contains(functionBody(html, "function homeDayScheduledRows(now = Date.now())"), banned) {
			t.Errorf("Home rows must never auto-join, found %q", banned)
		}
	}
	// the scheduled meetings fetch is the lobby's (Wave 7), not a new one
	if !strings.Contains(functionBody(html, "function renderHomeDay()"), "loadLobbyScheduledMeetings()") {
		t.Error("today must reuse loadLobbyScheduledMeetings")
	}
}

func TestIndexHomeV2NoTilesAndEmberOnlyOnTheLiveDot(t *testing.T) {
	html := homeV2Index(t)
	start := strings.Index(html, "      .home-day {")
	end := strings.Index(html, "      .office-hero {")
	if start == -1 || end == -1 || start > end {
		t.Fatal("could not slice the home v2 stylesheet")
	}
	css := html[start:end]
	// hairline rows, no tiles: no radius on rows, no card surfaces, no shadows
	for _, banned := range []string{"border-radius: 12px", "border-radius: 14px", "border-radius: 16px", "box-shadow: 0 8px", "box-shadow: 0 10px", "home-day__card", "home-day__tile", "grid-template-columns: repeat("} {
		if strings.Contains(css, banned) {
			t.Errorf("home v2 rows are hairline rows, not tiles, found %q", banned)
		}
	}
	if !strings.Contains(css, ".home-day__row {") || !strings.Contains(css, "border-bottom: 1px solid var(--line-1);") {
		t.Error("home v2 rows separate with the hairline")
	}
	// the only ember: the live dot
	emberRules := regexp.MustCompile(`(?m)^\s*([^{}\n]+)\{[^}]*var\(--ember`).FindAllStringSubmatch(css, -1)
	for _, match := range emberRules {
		if strings.TrimSpace(match[1]) != ".home-day__dot" {
			t.Errorf("ember on Home is earned only by the live dot, found it under %q", strings.TrimSpace(match[1]))
		}
	}
	if !strings.Contains(css, ".home-day__dot {\n        width: 7px;\n        height: 7px;\n        flex: none;\n        border-radius: 999px;\n        background: var(--ember);") {
		t.Error("the live dot is the ember")
	}
	for _, banned := range []string{"--ember-text", "--ember-soft", "--live)"} {
		if strings.Contains(css, banned) {
			t.Errorf("home v2 rows must not borrow %q", banned)
		}
	}
	// motion: the section fades in on the existing token-timed entrance; no
	// stagger, no rAF; reduced motion collapses it
	if !strings.Contains(css, "animation: home-suggestions-enter var(--dur-med) var(--ease) both;") {
		t.Error("home v2 sections reuse the Home entrance")
	}
	if strings.Contains(css, "animation-delay") || strings.Contains(css, "--stagger") {
		t.Error("home v2 sections do not stagger")
	}
	if !strings.Contains(css, "@media (prefers-reduced-motion: reduce) {\n        .home-day { animation: none; }\n        .home-day__dot { animation: none; }") {
		t.Error("home v2 must collapse its motion under reduced motion")
	}
	homeJS := html[strings.Index(html, "Home v2: today · since you were away · continue ----------"):strings.Index(html, "function setHomeRefreshFailed(failed)")]
	if strings.Contains(homeJS, "requestAnimationFrame") {
		t.Error("home v2 paints synchronously — no rAF")
	}
	// the dot is aria-hidden and the row is a real button (focus ring from the system)
	node := functionBody(html, "function homeDayRowNode(row)")
	for _, want := range []string{"button.type = 'button'", "button.className = 'home-day__row pressable'", "dot.setAttribute('aria-hidden', 'true')", "button.setAttribute('aria-label'"} {
		if !strings.Contains(node, want) {
			t.Errorf("homeDayRowNode must %q", want)
		}
	}
	// the older live pill strip yields to the today section
	if !strings.Contains(css, "#homeToday:not([hidden]) ~ .home-live { display: none; }") {
		t.Error("the live-now pill strip must not repeat the today section's live row")
	}
}

func TestIndexHomeV2GreetingAndComposerUnchanged(t *testing.T) {
	html := homeV2Index(t)
	home := homeV2Markup(t, html)
	for _, want := range []string{
		`<span class="office-launch__line" id="officeLaunchGreeting">good morning.</span>`,
		`<form id="homeScoutComposer" class="home-scout-composer" autocomplete="off">`,
		`<input id="homeScoutInput" class="home-scout-composer__input" type="text" maxlength="4000" placeholder="Message Scout" aria-label="Message Scout from home" disabled>`,
		`<button id="homeScoutSend" class="home-scout-composer__send" type="submit" aria-label="Send message to a new private Scout thread" disabled>`,
		`<p id="homeScoutComposerStatus" class="home-scout-composer-status" role="status" aria-live="polite"></p>`,
	} {
		if !strings.Contains(home, want) {
			t.Errorf("greeting/composer markup changed, missing %q", want)
		}
	}
	greeting := functionBody(html, "function renderOfficeGreeting()")
	if !strings.Contains(greeting, "`${officeGreetingWord()}, ${firstName}.`") {
		t.Error("the greeting still reads `good morning, AJ.`")
	}
	// Home's air: the sections sit at the continuity block's own rhythm
	if !strings.Contains(html, ".home-day {\n        box-sizing: border-box;\n        width: min(740px, 100%);\n        margin-top: 22px;\n        border-top: 1px solid var(--line-1);") {
		t.Error("home v2 sections share the continuity block's width and rhythm")
	}
	// last-seen is stamped only when Home is left in-app — never on a hidden
	// tab or a reload, which would clear rows the user has not acted on
	if !strings.Contains(html, "if (homeDayLastTool === 'office') markHomeSeen()") {
		t.Error("home.lastSeenAt must be stamped on Home unmount")
	}
	seen := functionBody(html, "function markHomeSeen()")
	if !strings.Contains(seen, "homeDaySinceMs = 0") {
		t.Error("the next Home mount must measure from the new stamp")
	}
	homeJS := html[strings.Index(html, "Home v2: today · since you were away · continue ----------"):strings.Index(html, "function setHomeRefreshFailed(failed)")]
	for _, banned := range []string{"visibilitychange", "pagehide"} {
		if strings.Contains(homeJS, banned) {
			t.Errorf("home.lastSeenAt must not advance on %s", banned)
		}
	}
}
