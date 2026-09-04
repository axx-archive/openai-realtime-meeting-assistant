package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Wave 11 D15 — AJ 2026-09-02: "look at each tab and how it handles
// headers/subheaders, search — should we have one unified top search, to the
// left of the notification bell, or should each tab have its own?" Decision:
// ONE header system (the topbar carries the tab name + one mono subline of
// machine facts on every tab) and ONE unified search (#globalSearch, ⌘K, a
// .glass-float results rail). In-content page titles and per-tab search
// fields are gone; inside a tab, filtering is chips only.

func readIndexForShellHeaderSearch(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func shellHeaderSlice(t *testing.T, html, start, end string) string {
	t.Helper()
	from := strings.Index(html, start)
	if from < 0 {
		t.Fatalf("missing %q", start)
	}
	to := strings.Index(html[from:], end)
	if to < 0 {
		t.Fatalf("missing %q after %q", end, start)
	}
	return html[from : from+to]
}

// Every tab has a topbar title (the rail word) and a subline; the subline
// is mono (machine facts), the title map names every destination.
func TestIndexShellHeaderEveryTabHasTopbarTitleAndSubline(t *testing.T) {
	html := readIndexForShellHeaderSearch(t)
	titles := shellHeaderSlice(t, html, "const toolTitles = {", "}")
	for _, want := range []string{
		"office: 'Home',",
		"room: 'Rooms',",
		"chat: 'Conversations',",
		"research: 'Work',",
		"files: 'Drive',",
		"memory: 'Memory',",
	} {
		if !strings.Contains(titles, want) {
			t.Errorf("toolTitles missing %q", want)
		}
	}
	sub := shellHeaderSlice(t, html, "function toolSubtitle(tool) {", "\n      }\n")
	for _, want := range []string{
		"case 'office':\n            return 'today'",
		"return open ? `lobby · ${open} ${open === 1 ? 'room' : 'rooms'}` : 'lobby'",
		"case 'chat':\n            return chatTopbarSubline()",
		"return 'meeting records · what scout knows'",
		"return `company files · ${driveScopeDefinition()[0]}`",
		"return kind ? `${kind} · ${facts}` : facts",
	} {
		if !strings.Contains(sub, want) {
			t.Errorf("toolSubtitle missing %q", want)
		}
	}
	// the Conversations subline mirrors the composer destination line
	mirror := shellHeaderSlice(t, html, "function chatTopbarSubline(thread = selectedScoutChatThread()) {", "\n      }\n")
	for _, want := range []string{
		"return `#${title} · ${team ? 'everyone in the office' : 'project members only'}`",
		"return 'scout · private to you'",
	} {
		if !strings.Contains(mirror, want) {
			t.Errorf("chatTopbarSubline missing %q", want)
		}
	}
	// Drive re-syncs the bar when the scope changes
	if !strings.Contains(html, "if (appShell?.dataset.tool === 'files' && typeof syncToolTopbar === 'function') syncToolTopbar()") {
		t.Error("renderFilesSurface must re-sync the topbar subline on a scope change")
	}
	// the subline is the machine voice
	subtitle := shellHeaderSlice(t, html, "      .topbar__subtitle {", "}")
	if !strings.Contains(subtitle, "font: var(--type-label);") {
		t.Error(".topbar__subtitle must set in the mono label style (machine facts)")
	}
	// no tab hides the topbar title any more
	for _, gone := range []string{
		`#appShell[data-tool="office"] .topbar__heading {`,
		`#appShell[data-tool="chat"] .topbar__heading,`,
		`#appShell[data-pd1-destination="Work"] .topbar__context { display: none; }`,
	} {
		if strings.Contains(html, gone) {
			t.Errorf("a tab still hides its topbar title: %q", gone)
		}
	}
}

// In-content page titles are gone: the headings stay for assistive tech only.
func TestIndexShellHeaderNoInContentPageTitles(t *testing.T) {
	html := readIndexForShellHeaderSearch(t)
	for _, gone := range []string{
		`<p class="agent-tool__eyebrow">packaging studio</p>`,
		`<h2 id="studioProjectsTitle" class="agent-tool__title">`,
		"<div>\n                  <h2 id=\"filesScopeTitle\">Home</h2>",
	} {
		if strings.Contains(html, gone) {
			t.Errorf("in-content page title must not return: %q", gone)
		}
	}
	for _, want := range []string{
		`<header class="studio-projects__head sr-only">`,
		`<h2 id="studioProjectsTitle">Work</h2>`,
		"<div class=\"sr-only\">\n                  <h2 id=\"filesScopeTitle\">Home</h2>",
		"      .sr-only {",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("accessible heading missing %q", want)
		}
	}
	// every tab's first content row starts on --content-top (topbar + 24px)
	if !strings.Contains(html, "--content-top: calc(var(--shell-topbar-height, 60px) + 24px);") {
		t.Error("--content-top token missing")
	}
	for _, want := range []string{
		"padding: 24px 8px 12px;",                      // chat thread rail
		"padding: 24px 14px 18px; /* Wave 11 D15",      // drive sidebar
		"padding: 24px clamp(22px, 2.6vw, 40px) 48px;", // drive main
		".studio-projects__head.sr-only + .packaging-studios { margin-top: 0; }",
		"padding: 24px max(28px, env(safe-area-inset-right)) 44px max(28px, env(safe-area-inset-left));", // studio
	} {
		if !strings.Contains(html, want) {
			t.Errorf("content-top alignment missing %q", want)
		}
	}
}

// One search: the pill left of the bell, the rail, five scopes, keyboard
// contract, honest empty rows, and no per-tab search fields.
func TestIndexShellUnifiedSearch(t *testing.T) {
	html := readIndexForShellHeaderSearch(t)
	if got := strings.Count(html, `id="globalSearch"`); got != 1 {
		t.Fatalf("exactly one #globalSearch, found %d", got)
	}
	topbar := shellHeaderSlice(t, html, `<header class="topbar mount-stagger">`, "</header>")
	pill := strings.Index(topbar, `id="globalSearch"`)
	phoneBell := strings.Index(topbar, `id="topbarBell"`)
	desktopBell := strings.Index(topbar, `id="notificationBell"`)
	if pill < 0 || phoneBell < 0 || desktopBell < 0 || pill > phoneBell || pill > desktopBell {
		t.Error("#globalSearch must sit in the topbar left of both bells")
	}
	for _, want := range []string{
		`<span class="global-search-pill__label">Search</span>`,
		`<kbd class="global-search-pill__kbd" aria-hidden="true">⌘K</kbd>`,
		`<section id="globalSearchRail" class="global-search glass-float" role="dialog" aria-label="Search" hidden>`,
		`id="globalSearchInput" class="global-search__input" type="search"`,
		`role="combobox"`,
		`<div id="globalSearchResults" class="global-search__results" role="listbox"`,
		`data-search-scope="all" aria-pressed="true"`,
		`data-search-scope="conversations"`,
		`data-search-scope="files"`,
		`data-search-scope="studio"`,
		`data-search-scope="meetings"`,
		`data-search-scope="memory"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("unified search markup missing %q", want)
		}
	}
	// the phone folds the pill to its glyph
	if !strings.Contains(html, ".global-search-pill__label, .global-search-pill__kbd { display: none; }") {
		t.Error("phone pill must fold to the glyph")
	}
	js := shellHeaderSlice(t, html, "/* ---------- Wave 11 D15: unified search (⌘K) ----------", "/* live chip — shows whenever you are in the room behind another tab */")
	for _, want := range []string{
		"const globalSearchDebounceMs = 250",
		"const globalSearchMinChars = 2",
		"`/assistant/chat-search?q=${encodeURIComponent(query)}&limit=${globalSearchRowCap}`",
		"`/assistant/files?q=${encodeURIComponent(query)}`",
		"'/api/studio-projects/v1?limit=200'",
		"'/assistant/meetings/scheduled?upcoming=1'",
		"'/assistant/meetings?view=index&limit=60'",
		"`/assistant/memory/inspect?subject=${encodeURIComponent(query)}`",
		// real destinations
		"open: () => openScoutChatThread(thread.id)",
		"open: () => openChatSearchResult(result)",
		"open: () => openDriveFileByID(file.id)",
		"selectPD1Destination('Work', { projectId: project.id, closeOverlays: true })",
		"open: () => openMeetingRecordDeepLink(meeting.id)",
		"open: () => openMemoryInspectorItem(item.id)",
		// grouped by kind under mono kickers, honest empty rows
		"bfEl('p', 'global-search__kicker', rows.length ? `${scope} · ${rows.length}` : scope)",
		"bfEl('p', 'global-search__empty', `nothing in ${scope}`)",
		"bfEl('p', 'global-search__empty', `searching ${scope}…`)",
		// keyboard contract — review fix: Escape is bound on the document (the
		// rail has no focus trap, so a rail-scoped listener died once Tab left it)
		"if (event.defaultPrevented || event.key !== 'Escape' || !globalSearchIsOpen()) return",
		"if (event.key === 'ArrowDown') {",
		"} else if (event.key === 'ArrowUp') {",
		"} else if (event.key === 'Enter') {",
		"void openGlobalSearchRow(globalSearch.rows[globalSearch.active] || globalSearch.rows[0])",
		"if (String(event.key || '').toLowerCase() !== 'k') return",
		"globalSearchInput?.setAttribute('aria-activedescendant', node.id)",
		// abort + generation fence
		"const generation = ++globalSearch.generation",
		"if (generation !== globalSearch.generation) return",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("unified search JS missing %q", want)
		}
	}
	// the results rail is the float tier and animates only through the
	// duration tokens (reduced motion zeroes them)
	rail := shellHeaderSlice(t, html, "      .global-search {\n", "\n      }\n")
	for _, want := range []string{"transition-duration: var(--dur-fast), var(--dur-med), var(--dur-med);", "transform-origin: right top;"} {
		if !strings.Contains(rail, want) {
			t.Errorf(".global-search missing %q", want)
		}
	}
	// no per-tab search inputs remain on the five tab surfaces
	for _, gone := range []string{
		`id="chatThreadSearch"`,
		`id="studioProjectSearch"`,
		`id="filesSearch"`,
		`placeholder="Search Drive"`,
		`placeholder="Search the studio"`,
		`placeholder="search threads…"`,
	} {
		if strings.Contains(html, gone) {
			t.Errorf("per-tab search input must not return: %q", gone)
		}
	}
	// the active scope chip is the sanctioned well (ember text on the 10%
	// ember well), never a bar
	chip := shellHeaderSlice(t, html, `.global-search__scope[aria-pressed="true"] {`, "}")
	if !strings.Contains(chip, "color: var(--ember-text);") || strings.Contains(chip, "inset 2px 0") || strings.Contains(chip, "border-left") {
		t.Errorf("scope chip active treatment is off:\n%s", chip)
	}
	edge := regexp.MustCompile(`\.global-search__row\.is-active \{[^}]*\}`).FindString(html)
	for _, bar := range []string{"inset 2px 0", "inset 3px 0", "border-left:"} {
		if strings.Contains(edge, bar) {
			t.Errorf("the active result row paints an edge bar (%q)", bar)
		}
	}
}
