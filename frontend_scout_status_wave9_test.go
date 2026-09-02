package main

// Wave 9 D3 — honest Scout status pill (Critical Rule 10). The topbar pill is
// driven by GET /readyz → capabilities.scout: the typed lanes decide the word
// (ready / backup model / paused / limited / offline), `idle` reads as ready
// and never as degraded, and clicking the pill opens a read-only bfMenu
// popover listing the four lanes. The poll is 60 s, visible-tab only, on
// setTimeout — never requestAnimationFrame. Static pins on index.html.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func readIndexForScoutStatusWave9(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	return string(raw)
}

func scoutStatusBody(t *testing.T, html, signature string) string {
	t.Helper()
	body := functionBody(html, signature)
	if body == "" {
		t.Fatalf("could not extract %s", signature)
	}
	return body
}

// The pill keeps its pinned id/classes but is now a button that opens the
// lane popover; the live region moves to the label so the button role holds.
func TestIndexScoutStatusPillMarkup(t *testing.T) {
	html := readIndexForScoutStatusWave9(t)
	for _, want := range []string{
		`<button id="statusPill" class="pill pill--idle" type="button" aria-haspopup="dialog" aria-expanded="false" aria-controls="scoutStatusPopover"`,
		`<span id="statusText" role="status" aria-live="polite">not connected</span>`,
		`#appShell[data-tool="room"] #statusPill`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing status pill markup %q", want)
		}
	}
	if strings.Contains(html, `<span id="statusPill"`) {
		t.Error("statusPill must be a button (it opens the Scout lane popover), not a span")
	}
}

// Five honest states, typed lanes first. idle maps to ready; degraded copy
// never leaks into a label or title.
func TestIndexScoutStatusPillStatesCopy(t *testing.T) {
	html := readIndexForScoutStatusWave9(t)
	body := scoutStatusBody(t, html, "function scoutStatusPillState(scout)")
	for _, want := range []string{
		"const SCOUT_READY_STATUSES = ['healthy', 'idle']",
		"state: 'ready', label: 'ready', tone: 'mono', title: 'Scout is ready'",
		// AJ 2026-09-02: never 'offline' — the pill's words are always about Scout
		"state: 'backup', label: 'Scout on a backup model', tone: 'warn'",
		"Scout is answering on a backup model",
		"state: 'paused', label: 'Scout paused', tone: 'warn'",
		"statuses.indexOf('paused_by_breaker')",
		"breaker?.retryAt",
		"scoutStatusRelative(retryAt)",
		"state: 'limited', label: 'Scout limited', tone: 'warn'",
		"state: 'offline', label: 'Scout unavailable', tone: 'danger'",
		"Scout is unavailable — the server could not be reached",
		"statuses.indexOf('fallback_active')",
		"lanes.typedRouter",
		"lanes.typedAnswer",
		"scoutLaneStatus(scout.scoutText) === 'available'",
		"typed.some(scoutLaneSucceededInWindow)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scoutStatusPillState missing %q", want)
		}
	}
	// idle is the LAST branch's territory: healthy or idle → ready, and the
	// function never spells a degraded word into user copy.
	if strings.Contains(body, "label: 'degraded'") || strings.Contains(body, "label: 'idle'") {
		t.Error("scoutStatusPillState must map idle to ready and never label the pill degraded")
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "title:") && strings.Contains(line, "degraded") {
			t.Errorf("pill title copy must say what happened, never 'degraded': %s", strings.TrimSpace(line))
		}
	}
	// the ready branch must not be reachable only through the failure path:
	// the healthy/idle return sits after every other state
	ready := strings.LastIndex(body, "title: 'Scout is ready'")
	offline := strings.LastIndex(body, "label: 'Scout unavailable'")
	if ready < offline {
		t.Error("healthy/idle → ready must be the final branch of scoutStatusPillState")
	}
	// AJ 2026-09-02: never 'offline' — no label or title may call Scout (or the app) offline
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "label: 'offline'") || strings.Contains(line, "Scout is offline") {
			t.Errorf("pill copy must never say offline: %s", strings.TrimSpace(line))
		}
	}
	// the lane row word for idle is "idle", never "degraded"
	lane := scoutStatusBody(t, html, "function scoutLaneWord(status)")
	for _, want := range []string{
		"case 'idle': return { word: 'idle', tone: 'idle' }",
		"case 'healthy': return { word: 'healthy', tone: 'mono' }",
		"case 'fallback_active': return { word: 'backup model', tone: 'warn' }",
		"case 'paused_by_breaker': return { word: 'paused', tone: 'warn' }",
	} {
		if !strings.Contains(lane, want) {
			t.Errorf("scoutLaneWord missing %q", want)
		}
	}
}

// The pill dresses only the two "all good" words; room states and the
// pre-auth copy keep their own. Warn states share .pill--warn, offline reuses
// .pill--offline, ready stays monochrome with a --text-3 dot and nothing pulses.
func TestIndexScoutStatusPillWiringAndTones(t *testing.T) {
	html := readIndexForScoutStatusWave9(t)
	apply := scoutStatusBody(t, html, "function applyStatusPillForTool()")
	for _, want := range []string{
		"label === 'ready' || label === 'memory ready'",
		"startScoutStatusPolling()",
		"scoutStatusPillState(scoutStatusSnapshot)",
		"statusPillEl.dataset.scoutState = scout.state",
		"scout.tone === 'danger' ? 'pill--offline' : 'pill--warn'",
		"delete statusPillEl.dataset.scoutState",
		// AJ 2026-09-02: bell top-right, status only when not ready, theme in Settings
		"const pillState = scoutApplies ? String(statusPillEl.dataset.scoutState || 'ready') : 'ready'",
		"statusPillEl.dataset.state = pillState",
		"statusPillEl.setAttribute('aria-hidden', String(pillState === 'ready'))",
		"renderSettingsScoutStatus()",
		"syncScoutComposerPlaceholder()",
	} {
		if !strings.Contains(apply, want) {
			t.Errorf("applyStatusPillForTool missing %q", want)
		}
	}
	for _, want := range []string{
		".pill--warn {\n        background: var(--warn-soft);\n        color: var(--warn-text);",
		".pill--offline {\n        background: var(--danger-soft);\n        color: var(--danger-text);",
		`#statusPill[data-scout-state="ready"] .pill__dot {
        background: var(--text-3);`,
		"#statusPill[data-scout-state] .pill__dot {\n        animation: none;",
		"#statusPill {\n        appearance: none;",
		// AJ 2026-09-02: bell top-right, status only when not ready, theme in Settings
		"#statusPill[data-state=\"ready\"] {\n        display: none;",
		`data-settings-section="scout-status"`,
		`id="settingsScoutLanes" class="scout-status-list"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("status pill CSS missing %q", want)
		}
	}
	// no Scout state may introduce a pulse: the only animated pill dots stay
	// the room's listening / connecting ones
	for _, rule := range []string{".pill--warn", ".scout-status-pop"} {
		start := strings.Index(html, rule+" {")
		if start < 0 {
			t.Fatalf("missing rule %s", rule)
		}
		end := strings.Index(html[start:], "}")
		if strings.Contains(html[start:start+end], "animation") {
			t.Errorf("%s must not animate (reduced-motion: no pulsing)", rule)
		}
	}
}

// The popover lists the four lanes with status word, last success (relative,
// mono), failure class and breaker state — read-only bfMenu on the float tier.
func TestIndexScoutStatusPopoverLaneRows(t *testing.T) {
	html := readIndexForScoutStatusWave9(t)
	// AJ 2026-09-02: one renderer (scoutStatusNodes) feeds both the popover
	// and Settings → Scout, so the lane list is always reachable
	nodes := scoutStatusBody(t, html, "function scoutStatusNodes()")
	for _, want := range []string{
		"['typedRouter', 'Router']",
		"['typedAnswer', 'Answers']",
		"['privateVoice', 'Private voice']",
		"['roomVoice', 'Room voice']",
		"scoutStatusLaneRow(name, lanes[key])",
		"return { head, why, rows, foot }",
	} {
		if !strings.Contains(nodes, want) {
			t.Errorf("scoutStatusNodes missing %q", want)
		}
	}
	render := scoutStatusBody(t, html, "function renderScoutStatusPopover()")
	if !strings.Contains(render, "menu.replaceChildren(head, why, ...rows, foot)") {
		t.Error("renderScoutStatusPopover must render the shared nodes")
	}
	settings := scoutStatusBody(t, html, "function renderSettingsScoutStatus()")
	if !strings.Contains(settings, "document.getElementById('settingsScoutLanes')") || !strings.Contains(settings, "host.replaceChildren(head, why, ...rows, foot)") {
		t.Error("renderSettingsScoutStatus must render the shared nodes into Settings → Scout")
	}
	row := scoutStatusBody(t, html, "function scoutStatusLaneRow(name, lane)")
	for _, want := range []string{
		"scoutLaneWord(status)",
		"scoutStatusRelative(lane?.lastSuccessAt)",
		"'scout-status-pop__mono', success || 'none yet'",
		"lane?.lastFailureClass",
		"const breaker = lane?.breaker",
		"scoutStatusRelative(breaker.retryAt)",
	} {
		if !strings.Contains(row, want) {
			t.Errorf("scoutStatusLaneRow missing %q", want)
		}
	}
	ensure := scoutStatusBody(t, html, "function ensureScoutStatusPopover()")
	for _, want := range []string{
		"bfMenu(statusPillEl, {",
		"items: [],",
		"role: 'dialog'",
		"label: 'Scout status'",
		"className: 'scout-status-pop'",
		"bindTrigger: false",
		"menu.id = 'scoutStatusPopover'",
		"menu.tabIndex = -1",
	} {
		if !strings.Contains(ensure, want) {
			t.Errorf("ensureScoutStatusPopover missing %q", want)
		}
	}
	if strings.Contains(ensure, "onSelect") {
		t.Error("the Scout status popover is read-only — no onSelect")
	}
	if !strings.Contains(html, "ensureScoutStatusPopover()?.toggle()") {
		t.Error("clicking the pill must toggle the lane popover")
	}
	// the popover rides the float tier through .bf-menu (glass-float recipe)
	if !regexp.MustCompile(`\.glass-float,\s*\n\s*\.bf-menu,`).MatchString(html) {
		t.Error(".bf-menu must stay in the .glass-float material list")
	}
}

// 60 s cadence, visible tab only, setTimeout only — and the state hoists as
// var so the boot render pass (setConnectionState → applyStatusPillForTool)
// never trips a TDZ.
func TestIndexScoutStatusPollIsVisibleOnlyAndNeverRAF(t *testing.T) {
	html := readIndexForScoutStatusWave9(t)
	if !strings.Contains(html, "var SCOUT_STATUS_POLL_MS = 60000") {
		t.Error("the Scout status poll interval must be 60 s")
	}
	refresh := scoutStatusBody(t, html, "function refreshScoutStatus()")
	for _, want := range []string{
		"fetch('/readyz', { cache: 'no-store' })",
		"payload?.capabilities?.scout",
		"scoutStatusSnapshot = { status: 'unreachable' }",
		"if (scoutStatusTabVisible()) scheduleScoutStatusPoll()",
	} {
		if !strings.Contains(refresh, want) {
			t.Errorf("refreshScoutStatus missing %q", want)
		}
	}
	visible := scoutStatusBody(t, html, "function scoutStatusTabVisible()")
	if !strings.Contains(visible, "document.visibilityState === 'visible'") {
		t.Error("scoutStatusTabVisible must read document.visibilityState")
	}
	schedule := scoutStatusBody(t, html, "function scheduleScoutStatusPoll(delay = SCOUT_STATUS_POLL_MS)")
	for _, want := range []string{"window.setTimeout(", "if (!scoutStatusTabVisible()) return"} {
		if !strings.Contains(schedule, want) {
			t.Errorf("scheduleScoutStatusPoll missing %q", want)
		}
	}
	start := scoutStatusBody(t, html, "function startScoutStatusPolling()")
	for _, want := range []string{
		"document.addEventListener('visibilitychange'",
		"window.clearTimeout(scoutStatusTimer)",
		"if (age >= SCOUT_STATUS_POLL_MS) refreshScoutStatus()",
	} {
		if !strings.Contains(start, want) {
			t.Errorf("startScoutStatusPolling missing %q", want)
		}
	}
	for _, signature := range []string{
		"function scoutStatusPillState(scout)",
		"function refreshScoutStatus()",
		"function scheduleScoutStatusPoll(delay = SCOUT_STATUS_POLL_MS)",
		"function startScoutStatusPolling()",
		"function renderScoutStatusPopover()",
		"function ensureScoutStatusPopover()",
	} {
		if strings.Contains(scoutStatusBody(t, html, signature), "requestAnimationFrame") {
			t.Errorf("%s must not use requestAnimationFrame", signature)
		}
	}
	for _, name := range []string{"SCOUT_STATUS_POLL_MS", "scoutStatusSnapshot", "scoutStatusCheckedAt", "scoutStatusTimer", "scoutStatusInFlight", "scoutStatusPollingStarted", "scoutStatusPopover"} {
		if regexp.MustCompile(`(?m)^\s*(let|const)\s+` + name + `\b`).MatchString(html) {
			t.Errorf("%s must be var (boot-reachable through applyStatusPillForTool)", name)
		}
		if !regexp.MustCompile(`(?m)^\s*var\s+` + name + `\b`).MatchString(html) {
			t.Errorf("%s var declaration missing", name)
		}
	}
}
