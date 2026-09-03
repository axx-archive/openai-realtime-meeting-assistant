package main

// Wave 10 (integration polish + snappiness) static contracts. Each pin is a
// literal the wave shipped and a later refactor could silently drop: the
// light-mode ramp proposal, the button hierarchy, the lobby on shared tokens,
// the over-media glass migration, the motion sweep, the Work poll visibility
// gate, the settings refinement and the rich-media card system. The nav
// proposal is not pinned here: the rail and topbar were reassigned to a
// dedicated designer mid-wave and stay theirs to shape. Measured budgets (cold index, route switch first paint) live in the
// execution log, not here — a timing pin on a shared laptop is not honest.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func polishWave10Index(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

// cssSection returns the stylesheet text between two unique markers.
func polishWave10Section(t *testing.T, html, start, end string) string {
	t.Helper()
	i := strings.Index(html, start)
	if i < 0 {
		t.Fatalf("section start missing: %q", start)
	}
	j := strings.Index(html[i:], end)
	if j < 0 {
		t.Fatalf("section end missing after %q: %q", start, end)
	}
	return html[i : i+j]
}

func TestPolishWave10LightRampAndButtonHierarchy(t *testing.T) {
	html := polishWave10Index(t)
	for _, want := range []string{
		// one more lift above --paper-0 so cards on panels have a step of their own
		"--paper-25: #F9F6F0;",
		"--surface-2: var(--paper-25);",
		// warm shadow ink on the putty ground (was the cool 14,14,16)
		"--shadow-1: 0 1px 2px rgba(38, 35, 30, 0.10);",
		"--shadow-2: 0 8px 24px rgba(38, 35, 30, 0.12);",
		"--shadow-3: 0 24px 64px rgba(38, 35, 30, 0.22);",
		// the pill's box-shadow list was invalid because this token never existed
		"--shadow-mark: var(--shadow-1);",
		// the dark ramp — AJ ratified dark ladder v2 2026-09-02 (was the
		// hairline-only #050506 / #0A0A0C / #141416)
		"--surface-1: #0E0E10;",
		"--surface-2: #151518;",
		"--surface-3: #1C1C20;",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("light ramp contract missing %q", want)
		}
	}
	secondary := polishWave10Section(t, html, ".btn--secondary {", ".btn--ghost {")
	for _, want := range []string{"background: var(--surface-2);", "border-color: var(--line-strong);", "background: var(--well);"} {
		if !strings.Contains(secondary, want) {
			t.Errorf("secondary button (surface + line, hover sinks to the well) missing %q", want)
		}
	}
	// ember for text goes through --ember-text; the two light-mode failures
	// the contrast probe found were raw Stride Orange on putty. The second one
	// used to be the thread row's mono STRIDE badge; AJ cut that badge on
	// 2026-09-02 (pinned channels wear the ember title alone), so the mono
	// half of the contract is pinned on the chat unread seam instead — same
	// rule, still ember-on-mono, still through the token.
	for _, want := range []string{
		".chat-thread-item__title--bonfire-chat {\n        color: var(--ember-text);",
		"#chatTool .desktop-chat-unread {",
		"color: var(--ember-text);\n        font: var(--type-label);",
		"--type-label: 500 11px/1.2 var(--font-mono);",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("ember-text contract missing %q", want)
		}
	}
	eyebrow := polishWave10Section(t, html, ".studio-project-detail__eyebrow {", ".studio-project-detail__title-row")
	if !strings.Contains(eyebrow, "color: var(--ink-3);") || strings.Contains(eyebrow, "var(--ember)") {
		t.Error("the Work kicker is ink at rest, not an ember mix")
	}
}

func TestPolishWave10LobbyOnSharedTokens(t *testing.T) {
	html := polishWave10Index(t)
	if n := strings.Count(html, "rgba(var(--lob-fg)"); n != 0 {
		t.Errorf("lobby still has %d rgba(var(--lob-fg)) consumers; every rule reads the shared ink/line/well tokens now", n)
	}
	for _, want := range []string{
		// the pinned ink channel stays; the aliases point at the shared tokens
		"--lob-fg: 17, 17, 20;",
		"--lob-fg: 255, 255, 255;",
		"--lob-join-bg: var(--accent);",
		"--lob-join-ink: var(--on-accent);",
		"--lob-pop-bg: var(--glass-float-fill);",
		"--lob-panel-shadow: var(--glass-sheet-shadow);",
		// the preview tile is a video surface: true black, not 82% black over paper
		".room-empty .greenroom__tile { background: var(--bg-stage);",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("lobby token contract missing %q", want)
		}
	}
	panel := polishWave10Section(t, html, ".lobby__panel {", ".lobby__center {")
	for _, want := range []string{"background: var(--glass-sheet-fill);", "backdrop-filter: var(--glass-sheet-filter);", "box-shadow: var(--glass-sheet-shadow);", "border: 1px solid var(--line);"} {
		if !strings.Contains(panel, want) {
			t.Errorf("lobby panel is the sheet tier; missing %q", want)
		}
	}
	join := polishWave10Section(t, html, ".room-empty__join {", ".lobby__center > .room-empty__join")
	if !strings.Contains(join, "background: var(--accent);") || !strings.Contains(join, "color: var(--on-accent);") {
		t.Error("join is the primary button: graphite ink on --on-accent")
	}
}

func TestPolishWave10OverMediaGlassMigration(t *testing.T) {
	html := polishWave10Index(t)
	for _, block := range []struct{ start, end string }{
		{".chat-deck__nav {", ".chat-deck__nav button {"},
		{".chat-deck__download-menu {", ".chat-deck__download-menu[hidden]"},
		{".invite-pop {", "@starting-style {"},
		{".artifact-stage__hero-menu {", ".artifact-stage__hero-menu[hidden]"},
	} {
		section := polishWave10Section(t, html, block.start, block.end)
		if !strings.Contains(section, "var(--glass-media") {
			t.Errorf("%s must ride the --glass-media material", block.start)
		}
		if strings.Contains(section, "rgba(18,18,20") || strings.Contains(section, "rgba(24, 24, 27") || strings.Contains(section, "rgba(20, 20, 22") {
			t.Errorf("%s still carries a hard-coded over-media rgba", block.start)
		}
	}
}

func TestPolishWave10IconMoreDotsWeight(t *testing.T) {
	html := polishWave10Index(t)
	for _, want := range []string{
		"more: ['M5 12a.75.75 0 1 0 .01 0', 'M12 12a.75.75 0 1 0 .01 0', 'M19 12a.75.75 0 1 0 .01 0'],",
		"'more-vertical': ['M12 5a.75.75 0 1 0 .01 0', 'M12 12a.75.75 0 1 0 .01 0', 'M12 19a.75.75 0 1 0 .01 0'],",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("more glyph must draw 0.75-radius dots (the old kebab weight): %q", want)
		}
	}
}

func TestPolishWave10MotionTokensAndReducedMotion(t *testing.T) {
	html := polishWave10Index(t)
	css := polishWave10Section(t, html, "<style>", "</style>")
	// no ease-in (the -out family is the kit ease), no scale(0) enters
	easeIn := regexp.MustCompile(`ease-in[^-]`)
	for _, line := range strings.Split(css, "\n") {
		if easeIn.MatchString(line) && !strings.Contains(line, "/*") && !strings.Contains(line, "*/") && !strings.Contains(strings.TrimSpace(line), "ease-in back") {
			t.Errorf("ease-in is not a kit easing: %q", strings.TrimSpace(line))
		}
	}
	if strings.Contains(css, "scale(0)") {
		t.Error("enters must not start from scale(0)")
	}
	// the literal durations the sweep converted
	for _, want := range []string{
		"animation: home-suggestions-enter var(--dur-med) var(--ease) both;",
		"animation: chat-msg-in var(--dur-med) var(--ease) both;", // plan 009: token-speed composited entrance
		"animation: chat-reaction-pop var(--dur-slow) var(--ease-spring);",
		"transition: opacity var(--dur-fast) var(--ease), visibility 0s linear var(--dur-fast);",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("token-timed motion missing %q", want)
		}
	}
	// wave animations that are not token-timed sit in the reduced-motion block
	for _, want := range []string{
		".memory-card.is-fresh,",
		".manifest-card.is-flashed,",
		".lobby__passinput.is-shake,",
		".room-presence-bar__member[data-voice-state=\"talking\"] .room-presence-waveform__bar,",
		".goalcard__glyph--gate::before { animation: none; }",
		".is-mounting .mount-stagger { animation: none; opacity: 1; transform: none; }",
		// the wave's own animations: step pulse, typing ripple, reaction burst, captions, search hit
		".studio-project-step__node.is-pulsing::before { animation: none; }",
		".scout-chat-typing__dots span { animation: none;",
		"animation: room-reaction-rise calc(var(--dur-slow) * 4) var(--ease) forwards;",
		"animation: bf-search-hit calc(var(--dur-slow) * 5) var(--ease) forwards;",
		".room-captions__line.is-fading { opacity: 0; }",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("reduced-motion coverage missing %q", want)
		}
	}
	if !strings.Contains(html, "const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')") {
		t.Error("the JS half of the reduced-motion rule is missing")
	}
	// plan 019: the breathe rung is zeroed with the others, and every smooth
	// scroll is gated on reducedMotion (an ungated 'smooth' literal never appears)
	if !strings.Contains(css, "--dur-slow: 0ms;\n          --dur-breathe: 0ms;") {
		t.Error("reduced motion must zero --dur-breathe beside the other duration tokens")
	}
	if ungated := regexp.MustCompile(`behavior: ?'smooth'`).FindAllString(html, -1); len(ungated) != 0 {
		t.Errorf("scrollIntoView must gate 'smooth' on reducedMotion; %d ungated call(s)", len(ungated))
	}
}

func TestPolishWave10WorkPollOnlyWhileVisible(t *testing.T) {
	html := polishWave10Index(t)
	for _, want := range []string{
		"appShell.dataset.tool === 'research' && document.visibilityState === 'visible' && studioProjects.some(",
		"if (appShell.dataset.tool === 'research') void loadStudioProjects({ onlyIfChanged: true })",
		"window.clearTimeout(studioProjectsRefreshTimer)\n          return",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("Work poll visibility gate missing %q", want)
		}
	}
}

func TestPolishWave10SettingsRefinement(t *testing.T) {
	html := polishWave10Index(t)
	for _, want := range []string{
		// a status line only exists when there is a status (no orphan dots)
		".noise-mode-option--status .noise-mode-status:has(> .noise-mode-status__text:empty) {",
		// the mode tag is information, in mono, set from the live renderer
		"if (modeTag) modeTag.textContent = 'auto'",
		// switches with no effect in this build fold under Advanced
		`<details class="settings-advanced">`,
		`<summary class="settings-advanced__summary">advanced <span class="settings-advanced__tag">not active in this build</span></summary>`,
		`<summary class="settings-advanced__summary">wake word <span class="settings-advanced__tag">experimental</span></summary>`,
		// raw mic stays a real mode (pinned by the noise test) but lives under Advanced
		"<strong>raw mic</strong>",
		// every setting says what it changes
		`<small class="settings-hint">Shown beside everything you say in rooms and chat.</small>`,
		`<p class="settings-hint">Which devices rooms use. Changes apply to your next call, or right away if you are in one.</p>`,
		// the section nav reads like the rail and is keyboard-complete
		".settings-nav__item[aria-current=\"true\"] {\n        background: color-mix(in srgb, var(--ember) 10%, transparent);\n        color: var(--ember-text);",
		"settingsRegion.querySelector('.settings-nav')?.addEventListener('keydown', event => {",
		".settings-nav__item:focus-visible {",
		// AJ 2026-09-02: bell top-right, status only when not ready, theme in Settings
		`<fieldset id="themeToggle" class="noise-mode-group theme-mode-group">`,
		`<input type="radio" name="themeMode" value="light">`,
		`<input type="radio" name="themeMode" value="dark">`,
		`data-settings-section="scout-status" aria-current="false"`,
		`<section data-settings-section="scout-status" hidden aria-label="Scout">`,
		"if (name === 'scout-status') {",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("settings refinement missing %q", want)
		}
	}
	// AJ 2026-09-02: the current section is the ember text on its 10% ember
	// well and nothing else — no leading-edge bar pseudo-element in any state
	// (mirrors .pd1-primary-nav__item[aria-current="page"]; "that thicker curve
	// line is a mark of AI design")
	for _, gone := range []string{
		".settings-nav__item::before {",
		".settings-nav__item[aria-current=\"true\"]::before {",
		"current-section tick",
	} {
		if strings.Contains(html, gone) {
			t.Errorf("settings nav must carry no accent bar; found %q", gone)
		}
	}
	// the raw-mic option sits inside the Advanced disclosure of the noise fieldset
	fieldset := polishWave10Section(t, html, "<fieldset class=\"noise-mode-group\">\n                <legend>noise reduction</legend>", "</fieldset>")
	adv := strings.Index(fieldset, `<details class="settings-advanced">`)
	raw := strings.Index(fieldset, "<strong>raw mic</strong>")
	if adv < 0 || raw < 0 || raw < adv {
		t.Error("raw mic must be inside the Advanced disclosure of the noise fieldset")
	}
}

// D4: the composer's GIF picker talks only to the server's GIPHY proxy, sends a
// pick as an imported attachment, says so honestly when the key is absent, and
// is keyboard-complete (Escape, roving arrows, Enter on a cell).
func TestPolishWave10GifPicker(t *testing.T) {
	html := polishWave10Index(t)
	for _, want := range []string{
		`<button id="scoutChatGif" class="scout-chat-attach scout-chat-attach--gif" type="button" aria-label="Add a GIF" aria-haspopup="dialog" aria-expanded="false"`,
		`<div id="scoutGifPopover" class="scout-gif-popover" hidden></div>`,
		"fetch(`/assistant/giphy/search?q=${encodeURIComponent(q)}&limit=24`, {",
		"postAuthJSON('/assistant/giphy/import', { url: gif.mediaUrl, title: gif.title || '', id: gif.id || '', threadId })",
		"'GIF search is not set up on this server — set GIPHY_API_KEY to turn it on. Pasting a GIF link still works.'",
		"pendingScoutFiles.push(result.data.file)",
		"if (event.key === 'Escape' && gifPickerIsOpen()) {",
		"grid.addEventListener('keydown', gifPickerGridKeydown)",
		"scoutChatGif.disabled = !Boolean(authedUser)",
		".scout-gif__grid {\n        display: grid;\n        grid-template-columns: repeat(3, minmax(0, 1fr));",
		".scout-gif__item {\n        position: relative;\n        aspect-ratio: 1;",
		"background: var(--glass-float-fill);\n        -webkit-backdrop-filter: var(--glass-float-filter);",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("GIF picker contract missing %q", want)
		}
	}
	if strings.Contains(html, "api.giphy.com") {
		t.Error("the client must never address GIPHY's API directly; the key lives server-side")
	}
}

func TestPolishWave10RichMediaCardSystem(t *testing.T) {
	html := polishWave10Index(t)
	start := strings.Index(html, "function renderDesktopChatLinkPreview(card, fallbackURL, preview)")
	end := strings.Index(html, "function renderDesktopChatLinkPreviewFallback(card, url)")
	if start < 0 || end < 0 || end < start {
		t.Fatal("desktop link preview renderer is missing")
	}
	renderer := html[start:end]
	for _, want := range []string{
		"desktopChatLinkPreviewSiteNode(domain,",
		"const isGif = hasProxiedImage && desktopChatPreviewIsGif(preview, imageURL)",
		"card.dataset.kind = previewKind\n\t        // a bare image / GIF is its own card kind whatever the route said\n\t        if (isImage) card.dataset.kind = 'image'",
		"if (isGif) visual.appendChild(bfEl('span', 'desktop-chat-link-preview__tag', 'GIF'))",
		"play.appendChild(strideIcon('present', { size: 20 }))",
		// click-to-play only: the player mounts from the click handler, never on render
		"card.addEventListener('click', event => {",
		"attachDesktopChatVideoPlayer(card, videoId)",
	} {
		if !strings.Contains(renderer, want) {
			t.Errorf("rich-media renderer missing %q", want)
		}
	}
	for _, banned := range []string{"iframe", "embed.js", "src = preview.imageUrl", "autoplay"} {
		if strings.Contains(renderer, banned) {
			t.Errorf("the renderer must stay metadata-only; found %q", banned)
		}
	}
	player := strings.Index(html, "function attachDesktopChatVideoPlayer(card, videoId)")
	if player < end {
		t.Fatal("the inline player must be defined after the renderer range")
	}
	playerBody := html[player : player+2200]
	for _, want := range []string{
		"https://www.youtube-nocookie.com/embed/",
		"open.textContent = 'open on YouTube ↗'",
		"desktop-chat-link-preview--playing",
	} {
		if !strings.Contains(playerBody, want) {
			t.Errorf("inline player contract missing %q", want)
		}
	}
	// a bare image URL renders through the same-origin proxy without a metadata fetch
	for _, want := range []string{
		"function desktopChatDirectImageURL(url)",
		"const directImage = desktopChatDirectImageURL(url)",
		"mediaType: directImage.gif ? 'gif' : 'image',",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("direct image card missing %q", want)
		}
	}
	// card anatomy in the E4 desktop chat slice
	cssStart := strings.Index(html, "/* E4 desktop chat quality slice.")
	cssEnd := strings.Index(html[cssStart:], "/* ---------- Room chat")
	if cssStart < 0 || cssEnd < 0 {
		t.Fatal("desktop chat CSS section is missing")
	}
	css := html[cssStart : cssStart+cssEnd]
	for _, want := range []string{
		"width: min(520px, 100%);",
		"border: 1px solid var(--line);\n          border-radius: var(--r-lg);\n          background: var(--surface-2);",
		".desktop-chat-link-preview:focus-visible {",
		".desktop-chat-link-preview__site {",
		".desktop-chat-link-preview__favicon,",
		".desktop-chat-link-preview__favicon-fallback {",
		"[data-kind=\"image\"] .desktop-chat-link-preview__visual {\n          display: grid;\n          place-items: center;\n          aspect-ratio: auto;\n          min-height: 160px;\n          max-height: 420px;",
		".desktop-chat-link-preview__tag {",
		".desktop-chat-link-preview__player > iframe {",
		"aspect-ratio: 16 / 9;",
		"aspect-ratio: 9 / 16;",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("rich-media card CSS missing %q", want)
		}
	}
}

// Plan 020: the five continuity moments — the thread-list FLIP gated on reduced
// motion, step nodes on transform (never width), the theme crossfade attribute,
// the previewer's capped decode, and the move ghost's undo.
func TestPolishWave10ContinuityMoments(t *testing.T) {
	html := polishWave10Index(t)
	flip := functionBody(html, "function flipChatThreadLists(render)")
	for _, want := range []string{
		"reducedMotion.matches",
		"getBoundingClientRect().top",
		"row.style.transform = `translateY(${delta}px)`",
		"if (Math.abs(delta) < 1) continue",
		"row.classList.add('is-entering')",
	} {
		if !strings.Contains(flip, want) {
			t.Errorf("thread-list FLIP helper missing %q", want)
		}
	}
	if !strings.Contains(functionBody(html, "function renderChatAgentThreads()"), "flipChatThreadLists(() => {") {
		t.Error("renderChatAgentThreads must render through the FLIP helper")
	}
	if !strings.Contains(html, `.studio-project-step[data-state="running"] .studio-project-step__node::before { transform: scale(1.125);`) {
		t.Error("the running step node must grow on transform, not width")
	}
	if strings.Contains(html, `.studio-project-step[data-state="running"] .studio-project-step__node::before { width:`) {
		t.Error("the running step node must not tween width")
	}
	apply := functionBody(html, "function applyTheme(theme)")
	if !strings.Contains(apply, "document.documentElement.dataset.themeTransition = ''") || !strings.Contains(html, "html[data-theme-transition]") {
		t.Error("applyTheme must hold html[data-theme-transition] for the crossfade")
	}
	if !strings.Contains(html, "await Promise.race([node.decode().catch(() => {}), new Promise(resolve => window.setTimeout(resolve, 300))])") {
		t.Error("the previewer must await the next image's decode with a 300 ms cap")
	}
	ghost := functionBody(html, "function filesMoveGhost(slot, fileId, folderId, previousFolderId)")
	if !strings.Contains(ghost, "'undo'") || !strings.Contains(ghost, "moveFileToFolderClient(fileId, previousFolderId)") {
		t.Error("the move ghost must carry an undo that restores the previous folder")
	}
}
