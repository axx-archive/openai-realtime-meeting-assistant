# STRIDE design system v2 — canon

**Status:** Wave 2 deliverable (design-system canon), 2026-09-01. Written for AJ and for every later wave.
**Code of record:** `index.html` — the `:root` / `[data-theme="dark"]` token block (§1, §2, §5), the `.stride-mark` rules and `STRIDE_ICON_PATHS` / `strideIcon()` (§3), `bfMenu()` and `.bf-menu` (§4).
**Guards:** `frontend_icon_system_test.go` (icons), `frontend_brand_assets_test.go` / `frontend_shell_identity_test.go` (the mark and wordmark), `npm run test:brand` (the Strike / Signal geometry).
**The mark is not restated here.** The aperture, the Strike tile, the wordmark and the Signal Cradle are canon in [`docs/stride-signal-canon.md`](../stride-signal-canon.md); this document stops where that one starts. The only rule repeated, because every section below depends on it: the rail mark is **graphite** (`#54545C` light / `#77777D` dark), never white; the wordmark is the supplied artwork — **black on light, ember (Stride Orange) on dark** — as pinned by `scripts/stride-signal-consistency.test.mjs` ("Bonfire uses the supplied black wordmark on light and orange on dark"). RATIFIED by AJ 2026-09-02 ("orange is cool on dark mode"): black wordmark on light, ember wordmark on dark; the July graphite canon is superseded.

Contents: §1 palette · §2 liquid glass · §3 icon system · §4 menu component · §5 motion · §6 adding a surface · Appendix A icon inventory.

---

## 1. Palette canon

Everything is solved against a **warm putty** light ground and a **true black** dark ground. Values below are copied from the token block; the block's comments carry the contrast solves and must travel with any change.

### 1.1 Base ramps (theme-independent)

**Ink** — true neutral darks (the dark theme's structural greys).

<svg xmlns="http://www.w3.org/2000/svg" width="540" height="44" viewBox="0 0 540 44" role="img" aria-label="ink ramp"><rect x="0" y="0" width="60" height="32" fill="#09090B"/><rect x="60" y="0" width="60" height="32" fill="#101013"/><rect x="120" y="0" width="60" height="32" fill="#141418"/><rect x="180" y="0" width="60" height="32" fill="#1B1B21"/><rect x="240" y="0" width="60" height="32" fill="#26262E"/><rect x="300" y="0" width="60" height="32" fill="#34343E"/><rect x="360" y="0" width="60" height="32" fill="#4D4D59"/><rect x="420" y="0" width="60" height="32" fill="#6E6E7A"/><rect x="480" y="0" width="60" height="32" fill="#9A9AA4"/><text x="0" y="42" font-family="monospace" font-size="9" fill="#6E6E7A">950 · 900 · 850 · 800 · 700 · 600 · 500 · 400 · 300</text></svg>

| token | value | | token | value |
|---|---|---|---|---|
| `--ink-950` | `#09090B` | | `--ink-600` | `#34343E` |
| `--ink-900` | `#101013` | | `--ink-500` | `#4D4D59` |
| `--ink-850` | `#141418` | | `--ink-400` | `#6E6E7A` |
| `--ink-800` | `#1B1B21` | | `--ink-300` | `#9A9AA4` |
| `--ink-700` | `#26262E` | | | |

**Paper** — warm putty, not white. The desktop ramp slid one step lighter on 2026-08-03; ratified Stride Putty `#CFC5B7` stays in the system as the well. Brand assets and mobile keep putty as their field.

<svg xmlns="http://www.w3.org/2000/svg" width="300" height="44" viewBox="0 0 300 44" role="img" aria-label="paper ramp"><rect x="0" y="0" width="75" height="32" fill="#F2EDE4" stroke="#C2B7A7"/><rect x="75" y="0" width="75" height="32" fill="#DDD4C6"/><rect x="150" y="0" width="75" height="32" fill="#CFC5B7"/><rect x="225" y="0" width="75" height="32" fill="#C2B7A7"/><text x="0" y="42" font-family="monospace" font-size="9" fill="#6E6E7A">0 panels · 50 ground · 100 well (putty) · 200 deep well</text></svg>

| token | value | role |
|---|---|---|
| `--paper-0` | `#F2EDE4` | panels, cards — lifted off the ground |
| `--paper-50` | `#DDD4C6` | the ground |
| `--paper-100` | `#CFC5B7` | inputs, wells, hover fills — Stride Putty as ratified |
| `--paper-200` | `#C2B7A7` | deepest well (the contrast worst case) |

**Signal** (live / speaking green — the loudest colour in the system), **Ember** (the one warm accent — see §1.4) and the **semantic hues** (state only).

<svg xmlns="http://www.w3.org/2000/svg" width="600" height="44" viewBox="0 0 600 44" role="img" aria-label="signal, ember and semantic hues"><rect x="0" y="0" width="50" height="32" fill="#5CE08A"/><rect x="50" y="0" width="50" height="32" fill="#30D158"/><rect x="100" y="0" width="50" height="32" fill="#23A847"/><rect x="170" y="0" width="50" height="32" fill="#FFA07A"/><rect x="220" y="0" width="50" height="32" fill="#FF7F4D"/><rect x="270" y="0" width="50" height="32" fill="#FF5A19"/><rect x="320" y="0" width="50" height="32" fill="#E64100"/><rect x="390" y="0" width="50" height="32" fill="#FF453A"/><rect x="440" y="0" width="50" height="32" fill="#FF9F0A"/><rect x="490" y="0" width="50" height="32" fill="#0A84FF"/><text x="0" y="42" font-family="monospace" font-size="9" fill="#6E6E7A">signal 400·500·600      ember 300·400·500·600      red · amber · blue</text></svg>

| token | value | | token | value |
|---|---|---|---|---|
| `--signal-400` | `#5CE08A` | | `--ember-300` | `#FFA07A` |
| `--signal-500` | `#30D158` | | `--ember-400` | `#FF7F4D` |
| `--signal-600` | `#23A847` | | `--ember-500` | `#FF5A19` — **Stride Orange**, the same hex the Signal is drawn in |
| `--red-500` | `#FF453A` | | `--ember-600` | `#E64100` |
| `--amber-500` | `#FF9F0A` | | `--blue-500` | `#0A84FF` |

### 1.2 Semantic aliases — light and dark

Feature code consumes these (and the role aliases in §1.3), never a raw ramp step. One token, two values, so a caller never asks which theme it is in.

| token | light | dark | role |
|---|---|---|---|
| `--bg-app` | `--paper-50` | `#000000` | the app field |
| `--bg-room-canvas` | `--paper-100` | `#000000` | room ground follows the theme |
| `--bg-stage` | `#000000` | `#000000` | video + letterboxing, always black |
| `--surface-1` | `--paper-0` | `#050506` | panels, rails |
| `--surface-2` | `--paper-0` | `#0A0A0C` | cards on panels |
| `--surface-3` | `--paper-100` | `#141416` | inputs, wells, hover fills |
| `--text-1` | `#26231E` | `#F5F5F7` | primary ink — **a warm dark grey, not black** on putty |
| `--text-2` | `rgba(38,35,30,.87)` | `rgba(245,245,247,.66)` | secondary (6.8:1 / 7.3:1 on the well) |
| `--text-3` | `rgba(38,35,30,.75)` | `rgba(245,245,247,.50)` | tertiary — icons at rest (5.0:1 / 4.8:1) |
| `--text-disabled` | `rgba(38,35,30,.43)` | `rgba(245,245,247,.24)` | disabled |
| `--line-1` | `rgba(38,35,30,.12)` | `rgba(255,255,255,.09)` | hairline |
| `--line-2` | `rgba(38,35,30,.22)` | `rgba(255,255,255,.16)` | emphasized hairline |
| `--accent` | `#26231E` | `#F5F5F7` | accent = ink; inverts |
| `--accent-soft` | `rgba(38,35,30,.08)` | `rgba(255,255,255,.10)` | fill tint |
| `--ring` | `rgba(38,35,30,.60)` | `rgba(245,245,247,.50)` | focus ring — clears SC 1.4.11 (3.45:1 / 5.0:1) |
| `--on-accent` | `#FFFDF8` | `#0E0E10` | ink on an accent fill |
| `--live` / `--success` | `--signal-500` | same | green = live AND landed / shipped / passed |
| `--danger` · `--warn` · `--info` | red · amber · blue | same | state fills only |
| `--*-soft` (live, success, danger, warn, info) | 10–14 % washes | 14 % washes | chips, dots, rings — no text |
| `--*-text` (live, success, danger, warn, info) | `#135523` · `#135523` · `#970800` · `#6A4100` · `#004992` | the hue itself | text and glyphs in a state hue — darkened cuts on the light ground (AA on `--paper-200`) |
| `--ember` | `--ember-500` | same | fills only — see §1.4 |
| `--ember-text` | `#86290F` | `--ember-500` | ember for text and glyphs |
| `--ember-soft` | `rgba(255,90,25,.14)` | `rgba(255,90,25,.16)` | the ember wash |
| `--on-ember` | `#1A0A05` | same | ink on the ember CTA (6.84:1) |
| `--wordmark-image` | `stride-wordmark-black.png` | `stride-wordmark-orange.png` | AJ-supplied artwork; selected by `.wordmark` |
| `--scrim` | `rgba(38,35,30,.35)` | `rgba(0,0,0,.60)` | modal scrim |
| `--backdrop-ambient` | two washes on `--paper-50` | two washes on `#000` | one wash is Stride Orange at 3 % — the only ambient orange in the system |

Typography (`--type-*` / `--track-*`), spacing (`--sp-1…16`), radius (`--r-sm…full`) and elevation (`--shadow-1…3`, `--glow-*`) are unchanged from glass & ink v2 and live in the same block.

### 1.2a Wave 10 light-mode pass (RATIFIED by AJ, 2026-09-02 — "the light re-tune is nice we can do that")

AJ's lobby screenshot showed five near-identical putty steps (ground, panel, inner card, buttons, the mic/cam discs) with no separation, the lobby on its own `--lob-*` palette, a near-black join beside graphite/ember primaries elsewhere, an ink-and-ember org chip, and a flat dark-grey preview tile. This pass keeps the brand orange and the putty family and re-tunes only what follows. Dark values are untouched; the contrast walk passes on both grounds (0 failures at 1280×800 and 390×844, see the Wave 10 execution-log entry).

| token | old | new | why |
|---|---|---|---|
| `--paper-25` | — | `#F9F6F0` | a new step above `--paper-0`: cards on panels get a lift of their own (same hue as `--paper-0`, lightness +3) |
| `--surface-2` (light) | `var(--paper-0)` | `var(--paper-25)` | surface-1 and surface-2 were the same value, so a card on a panel had only a hairline to say it was a card |
| `--shadow-1` | `0 1px 2px rgba(14,14,16,.10)` | `0 1px 2px rgba(38,35,30,.10)` | shadow ink is the warm ink on putty (cool near-black read as a grey smudge) |
| `--shadow-2` | `0 8px 24px rgba(14,14,16,.12)` | `0 8px 24px rgba(38,35,30,.12)` | same |
| `--shadow-3` | `0 24px 64px rgba(14,14,16,.22)` | `0 24px 64px rgba(38,35,30,.22)` | same |
| `--glow-accent` | `… rgba(14,14,16,.14)` | `… rgba(38,35,30,.14)` | same |
| `--shadow-mark` | (never declared) | `var(--shadow-1)` | the transcription pill's `box-shadow` list — its focus ring included — was invalid |

Ramp after the pass (light): ground `#DDD4C6` → surface-1 `#F2EDE4` → surface-2 `#F9F6F0` → well `#CFC5B7`. One `--line` hue (ink alpha); shadows warm and rare.

**Button hierarchy, defined once** (`.btn` family): primary = graphite ink (`--accent` / `--on-accent`); secondary = surface + line (`--surface-2` + `--line-strong`, hover sinks to `--well`); tertiary = ghost. Ember is never a button colour except the sanctioned live/earned CTA. The lobby's join / new room / schedule and the green-room discs follow it.

**Lobby on shared tokens.** Every `rgba(var(--lob-fg), a)` consumer was rewritten by property: text → `--ink-2` / `--ink-3`; borders → `--line` / `--line-strong`; focus outlines and focus borders → `--ring`; fills → `--well` / `--accent-soft`; join → `--accent` / `--on-accent`; the ⋯ popover → `--glass-float-fill`; the panel → the sheet tier (`--glass-sheet-fill` + `--glass-sheet-filter` + `--line` + `--glass-sheet-shadow`); the rail card → `--surface-2`; the preview tile → `--bg-stage` (true black; it is a video surface). The pinned `--lob-fg` ink channel stays and the `--lob-*` aliases now point at those same tokens, so no second palette can come back.

**Org tile.** `.topbar__organization-badge` is a `--well` tile with a `--line` inset and `--ink-1` initial (was an ink→ember gradient chip).

**Ember-as-text fixes found by the probe:** `.chat-thread-item__title--bonfire-chat` and the STRIDE tag (`--ember` → `--ember-text`, 1.83:1 / 1.89:1 → AA); the Work detail kicker (`--ember` mix → `--ink-3`).

### 1.2b Dark is the default; the dark elevation ladder (Wave 10, AJ 2026-09-02)

AJ's mandate for the polish pass: "a beautiful product with a light/dark mode (dark as default), with consistency end-to-end across all tabs, menus, surface areas." Two consequences, both shipped in Wave 10:

- **Dark is the product default.** The pre-paint script, `storedThemePreference()` and the `theme-color` meta all fall back to `dark` when nothing is stored and the account carries no `themePref`; a saved `light` (device or account) still wins, and `system` still means the OS. Supersedes the light default of 2026-07-10. Pinned by `TestIndexThemeDefaultsToDark`.
- **The elevation ladder is theme-aware.** `--shadow-1/2/3` and `--glow-accent` were the light ink shadows (rgba(38,35,30,…)) in both themes — invisible on true black, so every card that leaned on them sat flat in dark. Dark now defines them as black at .40/.45/.60. Consumers reach for the tokens, never a literal shadow, and never a `[data-theme="dark"]` override of their own.

Sweep rules the pass normalised to (the one-offs it removed are listed in the execution log): radii come from the ladder (`--r-sm` chips and 26px mini buttons · `--r-md` rows, inputs, toolbar buttons · `--r-lg` cards · `--r-xl` panels and the composer · `--r-2xl` dialogs/PiP/the lobby panel · `--r-full` pills); hairlines are `--line` / `--line-strong` (also as inset box-shadow rings); rows and menu items hover to `--well`, icon-only and ghost buttons to `--accent-soft`; nav rows and toolbar controls are 40px, form fields 44px, composer glyph buttons 34px inside a 44px hit; lifts are `0 0 0 1px var(--line-1), var(--shadow-1)` (cards, active rows) or `var(--shadow-2)` (the composer), one rule for both themes.

### 1.3 Role aliases (Wave 2 D1, w2-frontend)

One vocabulary for the ramp, read the same in both themes. Every alias points at a ratified value above; nothing here re-tunes a colour. They resolve on `<html>`, so the dark block's redefinitions of the targets flow through without a second copy.

| role | points at | use it for |
|---|---|---|
| `--ground` | `--bg-app` | the field the app sits on (putty / true black) |
| `--surface-0` | `--bg-app` | ramp step 0 — the ground itself; `--surface-1..3` sit above |
| `--well` | `--surface-3` | inputs, wells, hover fills — sinks UNDER the ground |
| `--ink-1` … `--ink-4` | `--text-1` … `--text-disabled` | the text ladder, 1 primary → 4 disabled |
| `--line` · `--line-strong` | `--line-1` · `--line-2` | hairline · emphasized hairline |
| `--shadow-float` | `0 16px 38px rgba(38,35,30,.18)` light · `rgba(0,0,0,.50)` dark | the lift under every floating layer (menus, toasts) |
| `--selection` | `rgba(38,35,30,.20)` light · `rgba(245,245,247,.24)` dark | text-selection wash — ink, never ember |

New code should reach for the role first (`--ink-3` for an icon at rest, `--well` for a hover fill) and the semantic alias second; the raw ramp is an implementation input.

### 1.4 Ember doctrine

The token block carries this as a comment that tests pin verbatim; this is the same doctrine in prose.

- **One orange.** `#FF5A19` (`--ember-500`) is Stride Orange, the hex the Signal is drawn in. There is no second warm hue anywhere — the heritage flame `#FF7A2B` is retired and `--agent` aliases `--ember`.
- **Earned, never ambient.** A screen at rest holds no ember. Ember marks *ignition* — the moment work starts or is live — and nothing else. Decoration, emphasis, hover states, empty states and "brand presence" are not reasons.
- **The mark is graphite,** never orange, never `--text-1` white; **the wordmark is black on light, ember on dark** (pinned by the brand test — see the header rule and the signal canon). RATIFIED by AJ 2026-09-02 ("orange is cool on dark mode"): black wordmark on light, ember wordmark on dark; the July graphite canon is superseded.
- **Sanctioned exceptions — the complete list. Add nothing without AJ.**
  1. **The active rail tab** — the one place ember says "you are here" (ratified 2026-09-01: active tab ember, rail mark graphite).
  2. **Live / speaking** — a running call, a live ingestion pulse, a speaking tile, a running goal node (`--agent`, `--glow-agent`).
  3. **The search-hit well** — the matched term in chat / memory search.
- **Text and glyphs** in ember use `--ember-text` (`#86290F` on the light ground, 6.1:1 on the ground and 4.55:1 on the deep well; the hue itself on black, 6.37:1). **Fills** keep `--ember` — chips, glows and the CTA carry no text and have no contrast requirement, and dulling them would cost the brand for nothing. `--ember-soft` is a 14 % wash for the same three cases only.
- The 3 % orange radial in `--backdrop-ambient` is the one ambient use, and it is a wash, not an element: on a warm ground a purely neutral wash reads as dirt.

---

## 2. Liquid glass — three tiers (Wave 2 D2, w2-frontend)

One material, three thicknesses. Every tier is a `color-mix()` of `--surface-1` toward transparent, so the glass re-tints with the theme by construction; `saturate()` is the material's signature and never varies between panes. A surface keeps only its own position, radius and padding — the class carries the recipe.

| tier | class | fill | filter | edge + lift | used for |
|---|---|---|---|---|---|
| **chrome** | `.glass-chrome` | `--glass-chrome-fill` = surface-1 **88 %** | `--glass-chrome-filter` = `saturate(1.8) blur(20px) saturate(1.25)` | none (a hairline underline where it meets the canvas) | topbar, rail, in-call dock, chrome heads |
| **float** | `.glass-float` / `.bf-menu` | `--glass-float-fill` = surface-1 **94 %** | `--glass-float-filter` = same chrome blur | `1px solid var(--line)` · `--glass-float-shadow` (= `--glass-highlight` + `--shadow-float` on putty; `--shadow-float` alone on black) | menus, popovers, pickers, toasts, banners |
| **sheet** | `.glass-sheet` | `--glass-sheet-fill` = surface-1 **96 %** | `--glass-sheet-filter` = `saturate(1.8) blur(28px) saturate(1.25)` | `1px solid var(--line)` · `--glass-sheet-shadow` (= `--glass-highlight` + `--shadow-3` on putty; `--shadow-3` alone on black) | modals, side sheets, green room, settings, login |

**Fallback.** Under `@supports not ((backdrop-filter: blur(1px)) or (-webkit-backdrop-filter: blur(1px)))` (old WebKit, Firefox with the flag off, forced low-power) every tier's background rises to `--glass-opaque-fill` (surface-1 **98 %**): with nothing to blur, the mix must carry legibility by itself.

**Dark variants** live in the token block, not in class overrides: the three `--glass-*-fill` mixes re-tint through the dark `--surface-1`, and `--glass-float-shadow` / `--glass-sheet-shadow` drop the specular edge on the black ramp so the 50 % `--shadow-float` does the lifting; chrome keeps its hairline so it separates from the canvas. Measured on the sandbox: the org menu computes `oklab(0.9475 … / 0.94)` on putty and `oklab(0.1156 … / 0.94)` on black from the same rule.

**Over-media chrome** (`--glass-media*`) is deliberately outside the tiers: it rides live video and stays dark in both themes.

**Migrated surfaces** (selectors adopted into the tier rules in `index.html`; the surface's own class stays so tests keep their pins):

- *chrome* — `.topbar`, `#chatTool .chat-convo-head`, `#chatTool .chat-context-rail__head`, `.drive-detail__head`, `.artifact-stage__head`, `.content-studio-drawer__head`, `.board-dock`.
- *float* — `.bf-menu`, `.topbar__organization-menu`, `.account-menu`, `#chatTool .desktop-chat-more__menu`, `#chatTool .desktop-chat-reaction-picker__menu`, `.files-folder-menu`, `.greenroom__filters`, `.manifest-card__more-menu`, `.package-row__more-menu`, `.chat-attachment-source-menu`, `.deck-editor__popover`, `.chat-channel-activity-popover`, `.board-menu`, `.goalcard__menu-pop`, `.notification-panel`, `.board-project-select`, `.toast`, `.update-banner`, `.multi-device-chip`.
- *sheet* — `.settings-panel`, `.consent-panel`, `.login-card`, `.room-transfer__panel`, `.os-assistant__panel`, `.pip-meeting`, `.board-rail`, `.board-surface`, `.agent-output`, `.research-library`.

**Left on their own recipe, on purpose** (each is either test-pinned to its literal lines or a different material): `.chat-convo-popover` and `.chat-member-picker__list` (already the float recipe verbatim — Wave 1 pins the lines), `.stride-select` (pinned), the mobile `.tool-rail` dock (pinned) and `.voice-island` (pinned), `.drive-details` (its desktop rule is an opaque side pane; only the phone sheet is glass), `.lobby__panel` / `.lobby-pop` (the lobby's own `--lob-*` system), and the over-media set — `.controls` / `.meeting-bar` in-call dock (Wave 6's control island), `.room-more__menu`, `.artifact-stage__hero-menu`, `.chat-deck__nav` / `.chat-deck__download-menu`, `.invite-pop`, `.room-presence-bar`, `.room-board-panel` — which stay dark in both themes on `--glass-media*`. Wave 10 moved the last three (`.artifact-stage__hero-menu`, `.chat-deck__nav` / `.chat-deck__download-menu`, `.invite-pop`) onto the `--glass-media*` tokens too, so no over-media surface carries a hard-coded rgba any more; the rich-media card's brand / play / GIF chips ride the same material.

The legacy `--glass-chrome` / `--glass-panel` rgba tokens and `--glass-blur` remain for their existing consumers; new surfaces use the tier classes, not the legacy tokens.

---

## 3. Icon system (Wave 2 D3)

### 3.1 Family rules

One stroked family, derived from the chat-action glyphs that were already canonical:

| rule | value |
|---|---|
| grid | 24 units (`viewBox="0 0 24 24"`) |
| stroke | **1.8** units — 7.5 % of the grid |
| caps / joins | round / round |
| colour | `stroke="currentColor"`, `fill="none"` — the icon takes the text colour of its button, so it rides `--ink-3` at rest and `--ink-1` on hover / active without a rule of its own |
| sizes | **16 · 20 · 24** px. 16 is the working size (composer, chat head, files, menus); 20 for room controls and heads; 24 for hero placements. |
| micro step | **12** px (11–13 in existing chrome). At this size 1.8 units is under a device pixel, so the stroke reads at **2** — `.stride-mark--micro`, which `strideIcon()` adds itself when `size ≤ 12`. |
| other grids | the same 7.5 %: a 16-grid glyph strokes at **1.2**, a 20-grid at **1.5**, a 12-grid at **1**. Prefer redrawing on the 24 grid; these ratios exist so the deck/document studio's 16-grid toolbar could conform without a redraw. |
| decoration | none. No fills, no two-tone, no shadows, no filled dots — the `more` glyph is three `h.01` round-capped strokes, the way the chat actions draw it. |
| accessibility | the SVG is always `aria-hidden="true"`; the button carries the name (`aria-label` / `title` / visible text). |

Both themes read correctly by construction: `currentColor` follows `--text-2` / `--text-3`, which are solved to AA on each ground (§1.2). Verified by `getComputedStyle` on the sandbox — see §3.6.

### 3.2 API

```js
strideIcon(name, { size = 16, className })   // → <svg class="stride-mark …" data-icon="name" …>
strideIconMarkup(name, options)              // → the same element as an HTML string, for innerHTML sites
strideIconButton(className, name, options)   // → <button type="button" class=…> wrapping the glyph (caller sets aria-label)
strideChatActionIcon(name)                   // → strideIcon(name, { size: 16, className: 'chat-action-mark' }) — kept for its callers
```

`strideIcon` sets every family attribute on the element (`viewBox`, `fill`, `stroke`, `stroke-width`, caps, joins, `aria-hidden`, `data-icon`), so a glyph conforms with or without the `.stride-mark` CSS. The CSS class is belt and braces for hand-written markup: `.stride-mark { fill:none; stroke:currentColor; stroke-width:1.8; round caps/joins }` overrides any stale attribute. Unknown names return an empty (but well-formed) SVG rather than throwing.

### 3.3 Name list (`STRIDE_ICON_PATHS`)

| group | names |
|---|---|
| rail | `home` `rooms` `conversations` `work` `drive` |
| topbar | `bell` `bell-slash` `theme` (= moon) `moon` `sun` `settings` `sign-out` `user` |
| composer | `attach` `files` `mic` `mic-off` `send` `gif` |
| chat | `react` `reply` `riff` `more` `more-vertical` `people` `search` `hash` `sparkle` `thread` |
| rooms | `mic` `cam` `cam-off` `share` `leave` `chat` `hand` `reactions` `invite` `audio` `pin` `expand` `collapse` `shield` `settings` |
| files | `folder` `folder-plus` `upload` `download` `star` `trash` `share-out` `link` `file` `file-text` `image` `archive` |
| editors | `bold` `italic` `list` `list-ordered` `table` `image` `undo` `redo` `present` `text` `edit` `layers-front` `layers-back` `download` |
| chevrons & controls | `chevron-down` `chevron-up` `chevron-left` `chevron-right` `arrow-left` `arrow-right` `arrow-up` `arrow-down` `close` `check` `plus` `minus` `refresh` `copy` `external` `grip` `lock` `clock` `globe` `diamond` `filter` |

The drawings for the rail, bell, moon/sun, gear, attach, send, people, search, hash, sparkle, mic/cam/share/leave/chat/invite/audio, the file kinds and the chevron set are the shapes the app already shipped — lifted from the markup so nothing moved visually. New drawings (`gif`, `hand`, `star`, `trash`, `link`, `bold`, `italic`, `list`, `table`, `undo`, `redo`, `present`, `layers-*`, `lock`, `clock`, `globe`) follow the same construction: 2-unit inset, 2-unit radii on frames, single-stroke silhouettes.

### 3.4 Sanctioned exceptions

These are the only places a glyph may leave the family. Each is named so a reviewer can tell an exception from a straggler.

| exception | where | why |
|---|---|---|
| **active rail tab pop** — `stroke-width: 2.2` + `rail-icon-pop` | `.pd1-primary-nav__item[aria-current="page"] > svg` | the one "you are here" emphasis, paired with the ember tab (§1.4) |
| **micro step** — stroke 2 | `.stride-mark--micro`, 11–12 px badges (phase checks, lobby flags, chip dismiss) | sub-pixel at 1.8 |
| **Scout flame** — 64-grid filled silhouette | `.board-expanded-mark`, `researchBonfireIcon()`, memory-card kicker, riff/design pills, research icon, one 24-grid badge | an *identity* glyph for Scout, like the aperture is for Stride — not chrome |
| **over-media placeholder** — `stroke="rgba(255,255,255,.4)"` at 34 px | `.screen-stage__placeholder` | rides video; follows `--glass-media`, not the ink ladder |
| **occlusion fill** — `fill="var(--surface-1)"` on one inner rect | deck editor bring-front / send-back | the front layer must hide the back one; the fill is the panel colour, so it reads as a cut-out |
| **form-control glyphs** — CSS `url("data:image/svg+xml…")` | `<select>` chevrons, checkbox check masks | background images cannot take `currentColor`; they are tuned per control |
| **progress ring** | `.goalcard__ring` (44-grid, 2.5) | a meter, not an icon |
| **the brand marks** | `.topbar__mark-signal`, `.office-launch__cradle`, `.office-launch__aperture`, `.topbar__brand-wordmark` | canon in the signal doc; test-pinned |

Text arrows *inside labels* ("open artifact ▸", "copied ✓") are typography and stay; a text glyph *standing in for an icon* (a lone `×`, `☺︎`, `↻ ⌕ ✦ ◇`) is chrome and was replaced.

### 3.5 What changed, what stayed pinned

Inventory of every `<svg` in `index.html` before this wave: **201** sites — 7 CSS background data-URIs, 4 lines of code (the sanitizer / regexes), 3 brand / instrument marks, 1 progress ring, and **186 icon sites** drawn at ten different stroke widths (1.5, 1.6, 1.65, 1.7, 1.8, 1.85, 1.9, 2, 2.2, 2.25 — only 17 were at 1.8 by attribute), 13 of them filled at the element, plus 23 text-glyph chrome sites that were not SVGs at all.

After (216 `<svg` sites, the difference being the `×` closes and the home-starter glyphs that are now drawings):

| status | count |
|---|---:|
| conforms to the family | **192** |
| Scout flame identity glyph — sanctioned fill | 8 |
| CSS form-control glyph — out of scope | 7 |
| pinned brand — unchanged | 3 |
| code, not an icon | 3 |
| over-media placeholder — sanctioned | 1 |
| progress ring — not an icon | 1 |
| left as-is | 1 (the sanitizer's `<svg` string check) |

Changes made:

- **Normalized 147 open tags** from 1.5 / 1.6 / 1.65 / 1.7 / 1.85 / 1.9 / 2 / 2.2 / 2.25 to the family width (1.8 on the 24 grid, 1.2 on the 16 grid, 1.5 on the 20 grid, 1 on the 12 grid, 2 at ≤ 12 px). 45 of these carried `.stride-mark` and already *rendered* at 1.8 through the CSS — the attribute was stale; the other 102 (room dock, room controls, green room, media flags, PiP, deck / document studio toolbar, notification panel, drive nav, goal-card badges, settings nav) actually changed weight. Five 12 px badges already at 2 stayed at 2 (the micro step).
- **Replaced 8 of the 13 filled glyphs:** 4 kebabs (board dock, room "more", goal card, voice ledger) and 4 ▶ Present buttons are stroked now; the other 5 are the Scout flame (§3.4).
- **Replaced 27 text glyphs used as chrome:** 11 `×` close buttons in HTML dialogs and studios, 9 `×` dismiss / remove buttons built in JS (now `strideIconButton` / `strideIcon('close')`), the `☺︎` react trigger in the context card, and the home starter / suggestion category glyphs (`↻ ⌕ ✦ ◇` → `refresh` `search` `sparkle` `diamond`) in both the static markup (4) and the two JS render paths.
- **CSS:** `.stride-mark--micro` added; `#chatTool .stride-riff-mark` 1.6 → 1.8; `.home-starter__icon > svg, .home-suggestion__icon > svg { display: block }` so the new marks fill their 20 / 16 px line box.

Pinned, unchanged: `class="tool-rail__account-icon stride-mark"` (latency test), `data-icon="riff"` (private-riff tests), `id="roomMoreToggle"` and the control-island order, `.chat-context-card__message-action`, `scout-chat-image-viewer__close`, `[data-action="present"]`, `.chat-deck__btn--primary` + its "Present" text, every `id` / `aria-label` / `class` on every element touched (only the drawing inside changed), and the three brand marks. No test in the repo pins an inline chrome `<svg …>` open tag or a path string, so no icon had to be left off-family for a pin.

### 3.6 Verification

`frontend_icon_system_test.go` pins: `strideIcon` / `strideIconMarkup` / `strideChatActionIcon` exist and the alias routes through `strideIcon`; every rail / topbar / composer / chat / rooms / files / editors / chevron name resolves in `STRIDE_ICON_PATHS`; the factory sets `stroke="currentColor"`, `stroke-width="1.8"`, `fill="none"`, round caps and joins on the element; no `<svg` in the rail, topbar, home composer, chat composer, context-reply composer or room-chat composer carries a hard-coded `fill="#"`, and every 24-grid `currentColor` glyph there is at 1.8; the brand marks are byte-identical.

Browser check (sandbox :3171, logged in as AJ, 2026-09-01), by `getComputedStyle` rather than screenshot: 23 sites across the rail, topbar, both composers, chat head, room dock, room controls, settings nav, drive nav and three dialog closes. In **dark**: every site `stroke-width: 1.8px` (2.2px on the active rail — the sanctioned pop), `fill: none`, and `stroke` identical to the owning button's computed `color` — `rgba(245,245,247,.66)` (`--text-2`) on composer / chat / drive, `.5` (`--text-3`) on the bell and account, `rgb(255,90,25)` on the active rail. In **light**: the same shape with the putty ladder — `rgba(38,35,30,.87)` / `.75`, and the active rail resolves to `rgb(134,41,15)` = `--ember-text`, exactly as the doctrine wants glyphs in ember to read. The 12-grid org chevron reports `1px`, the deck toolbar 16-grid glyphs `1.2px`. `typeof strideIcon / strideIconMarkup / strideIconButton / strideChatActionIcon` are all `"function"` at top level; `strideIcon('close', { size: 12 })` carries `stride-mark stride-mark--micro`; the alias still yields `class="stride-mark chat-action-mark"`. The only console error was a pre-auth 401 resource load, unrelated.

---

## 4. Menu component contract — `bfMenu` (Wave 2 D4, w2-frontend)

One floating menu. Two modes, one behaviour:

```js
bfMenu(trigger, { items, origin, radio, orientation, fixed, className, label, onSelect, closeOnSelect, animate })
  // builds a .bf-menu on the float tier next to the trigger (fixed:true portals it to <body>)
bfMenu(trigger, { menu, outside, escape, bindTrigger })
  // ADOPTS an existing menu element — its id, classes and items stay exactly as tests pin them
// returns { menu, trigger, open, close, toggle, isOpen, setItems, setChecked, items, place, destroy }
```

Item spec: `{ id, label, hint, icon (a Node — use strideIcon), danger, disabled, radio, checked, title, onSelect }` or `'-'` for a separator.

| concern | contract |
|---|---|
| roles | menu `role="menu"` (+ `aria-label`); items `role="menuitem"` or `menuitemradio` with `aria-checked`; separators `role="separator"`; the trigger carries `aria-haspopup` + `aria-expanded` |
| keyboard | **ArrowDown / ArrowUp** move and wrap (**ArrowRight / ArrowLeft** when `orientation: 'horizontal'`); **Home / End**; **type-ahead** by first letter; **Tab** closes; **Escape** closes and returns focus to the trigger |
| pointer | an outside `pointerdown` closes without moving focus; one menu open at a time — opening one closes the others (`bfMenuCloseAll`) |
| focus | on open, focus lands on the checked radio, else the first item (`focusTarget()` overrides) |
| radios | `radio: true` builds radios; `setChecked(id)` syncs `aria-checked` |
| placement | `origin: 'auto'` picks above / below from the trigger rect and viewport; `transform-origin` is computed from the trigger and menu rects so the menu grows out of the button that opened it |
| motion | opacity on `--dur-fast` / `--ease`, transform on `--dur-med` / `--ease-spring`; enters via `@starting-style` from `translateY(-4px) scale(0.92)`; the built menu also transitions `display` (`allow-discrete`) so it fades out before leaving layout; adopted menus animate the entry only |
| reduced motion | `@media (prefers-reduced-motion: reduce)` sets `transition: none` on `.bf-menu` and `[data-bf-menu]`, zeroes the hidden transform and the starting style — the menu appears in place |
| static | `animate: false` → `[data-bf-menu-static]`, `transition: none` — the deck / document studios pin zero animations on their first frame |
| legacy pins | popovers whose light-dismiss lines are test-pinned keep those lines and pass `outside:false / escape:false / bindTrigger:false`; the controller still owns open / close, focus, keyboard and the origin |
| layout | `.bf-menu` grid, 6 px padding, 13 px radius, 172–320 px wide; `.bf-menu__item` 40 px min-height, 9 px radius, `--well` on hover/focus, `--press-scale` on active, `--danger-text` for `data-danger`, `--ink-4` disabled; `.bf-menu--horizontal` is an inline pill |

Icons in menu items come from `strideIcon(name)` at 16; the label is `.bf-menu__label`, an optional `.bf-menu__hint` in `--ink-3` caption.

**Migrated sites** (all adopt mode unless noted — every id, class and pinned line survives):

| site | trigger → menu | notes |
|---|---|---|
| org switcher | `#topbarOrganizationSwitcher` → `#topbarOrganizationMenu` | radio; full contract |
| account menu | `#accountMenuButton` → `#accountMenu` | dialog: no arrow keys; pinned light-dismiss / Escape lines stay, controller owns origin + registry |
| conversation bell | `#chatConvoNotifyOpen` → `#chatConvoNotifyMenu` | radio; via `chatHeaderPopoverController` (pinned wiring stays) |
| conversation members | `#chatConvoMembersOpen` → `#chatConvoMembersPopover` | dialog: keyboard off so the picker keeps its keys |
| chat more-menu | `desktopChatMoreMenuControl` → `.desktop-chat-more__menu` | full contract |
| reaction picker | `desktopChatReactionPickerControl` → `.desktop-chat-reaction-picker__menu` | `orientation: 'horizontal'`, `role="group"` kept (items are `aria-pressed` toggles) |
| Drive folder / row kebabs | `.files-folder-chip__kebab` / `.files-row__kebab` → `.files-folder-menu` | rendered open by state → `bfMenuAdoptOpen`; items now `role="menuitem"` |
| green room filters | `#greenRoomFiltersBtn` → `#greenRoomFilters` | keyboard off (swatches + switch); pinned toggle/close lines stay |
| in-call more | `#roomMoreToggle` → `#roomMoreMenu` | pinned keydown / light-dismiss stay; controller adds origin + registry |
| deck studio | `[data-action="mobile-tools-menu"]` → `#deck-mobile-tools-menu`, `[data-action="download-menu"]` → `[data-download-menu]` | `animate: false` (still first frame) |
| document studio | `[data-doc-action="more"]` → `#document-more-tools` | `animate: false`; editor's own click / Escape routing stays |
| chat-deck card download | `.chat-deck__download` → `.chat-deck__download-menu` | `animate: false` — media material, and the phone-touch contract measures its 44 px rows on open |
| artifact hero / manifest card / package row more-menus | `.artifact-stage__hero-menu`, `.manifest-card__more-menu`, `.package-row__more-menu` | full contract |
| attachment source | composer paperclip → `.chat-attachment-source-menu` | **built** (`items`), `fixed: true`, opens above the trigger |

Not a menu: the settings sections `nav.settings-nav` is a navigation list (buttons with `aria-current`), so it stays a nav; nothing to migrate.

---

## 5. Motion tokens and the reduced-motion rule

| token | value | use |
|---|---|---|
| `--ease` | `cubic-bezier(0.32, 0.72, 0, 1)` | default — the Apple sheet ease |
| `--ease-spring` | `cubic-bezier(0.34, 1.25, 0.5, 1)` | enters, PiP snap, menu scale-in, rail pop |
| `--ease-out` | = `--ease` | pinned alias for the room-entry lines |
| `--dur-fast` | 120 ms | hover, press, opacity leads |
| `--dur-med` | 220 ms | fades, toggles, toasts, menu transform |
| `--dur-slow` | 360 ms | panels, sheets, PiP travel, rail pop |
| `--dur-breathe` | 2400 ms | the ambient "listening" pulse (`--pulse-cycle` aliases it) |
| `--press-scale` | 0.97 | `.pressable:active`, `.bf-menu__item:active`, buttons |
| `--breathe` | `--breathe-rest` 0.4 → `--breathe-live` 1 | the whole room swells on one switch (`#appShell:has(.topbar__mark.is-listening)`) |

**Reduced motion is one rule with two halves.** Under `@media (prefers-reduced-motion: reduce)` the three duration tokens are **zeroed at `:root`**, so every token-timed transition and animation self-heals with no per-surface rule; anything that is *not* token-timed must either be timed by a token or sit in the reduced-motion block (the token block's checklist pins this). The JS half is `const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')` — every scripted motion (reaction pop, FLIP, scroll) gates on `reducedMotion.matches` before it runs. Menus additionally set `transition: none` (§4) because their `@starting-style` entry would otherwise still paint one frame from the offset.

A gotcha kept from wave 1: a keyframe block with `fill: both` blocks a later transition on the same property; and a `width` transition must become a `translateX` (the meters) — see the wave-2 motion notes in `docs/plans/`.

---

## 6. How to add a new surface — checklist

1. **Ground it.** Background is a role or a tier, never a hex: `--ground` / `--surface-1..3` / `--well` for flat surfaces; `.glass-chrome` / `.glass-float` / `.glass-sheet` for anything that floats (§2). Position, radius and padding are yours; the material is not.
2. **Ink it.** Text and icons use the ladder (`--ink-1..4`); hairlines `--line` / `--line-strong`; state text `--*-text`, state fills `--*-soft`. Never lower an alpha without re-running the contrast solve in the token block.
3. **No ember** unless the surface is one of the three exceptions in §1.4. If you think you have a fourth, it goes to AJ first.
4. **Icons come from `strideIcon(name, { size })`.** 16 by default, 20 for room controls, 12 only for badges. If the glyph does not exist, add it to `STRIDE_ICON_PATHS` on the 24 grid and extend the name list in `frontend_icon_system_test.go`. Never paste an `<svg>` with its own stroke width; never use a text glyph (`×`, `▶`, `⋯`) as chrome.
5. **Menus and popovers are `bfMenu`** (§4) — build new ones, adopt old ones. Keep the trigger's `aria-haspopup` / `aria-expanded`; put the accessible name on the trigger.
6. **Time motion with tokens** (`--dur-*`, `--ease*`) so reduced motion is free; gate scripted motion on `reducedMotion.matches`; `@starting-style` for enters, `allow-discrete` if `display` must ride along.
7. **Register the route** in all three places if it is a page (the three-registry rule from the design-evolution wave), and gate it with a static `TestIndex*` pin: the id, the class and the one line of markup that proves the surface exists.
8. **Verify in both themes with `getComputedStyle`,** not with a screenshot — the browser pane's screenshots lag the DOM.
9. **Do not touch** the aperture, the wordmark, the Strike or the cradle. They are canon elsewhere and pinned.

---

## Shell chrome (2026-09-02)

AJ ratified 2026-09-02 (verbatim: *"lose the flame, put the wordmark stride back, and lose the date, we also don't need 'offline' next to the org name bottom left, just the org name and stride should be left aligned like the icon is"*). Three rules, one shell:

- **Wordmark rail (AJ 2026-09-02, final).** The rail's top row is the Stride **wordmark** — `#brandMark.topbar__mark > .topbar__wordmark.wordmark`, the production artwork via `--wordmark-image` (black on putty / orange on black, colour token `--wordmark`), 83×22 px on the wide rail (36×9.5 on the slim icon rail), its left edge on the nav glyphs' left edge (x=20 wide / x=18 slim) and centred on the 60 px topbar band — still the one button `#topbarOrganizationSwitcher` that opens `#topbarOrganizationMenu` (tooltip = org, `aria-label` "Bonfire — organization", Esc returns focus). No flame, no tile, no ring, no glow; Scout presence on it is value only (brightness lift / one breath). The topbar carries **no wordmark and no date** — the surface title/context on the left, status on the right (the Room keeps elapsed · room state).
- **Topbar right = Scout status (only when not ready) + bell (AJ 2026-09-02).** `#notificationBell.topbar__notify` (36 px, the nav hover well) sits at the topbar's right edge on desktop and its window hangs below it, right edges aligned; phones keep `#topbarBell` in their header. `#statusPill` renders only while Scout is not ready (`data-state` ≠ `ready`; `aria-hidden` follows) and its words are always about Scout — "Scout on a backup model" / "Scout paused" / "Scout limited" / "Scout unavailable" — never "offline" ("you're on the app, it'll never be offline by nature"); the composer placeholder says the same. The full lane list lives in Settings → Scout (`#settingsScoutLanes`, same renderer as the popover). The theme switch is Settings → Appearance only (`#themeToggle` three-way light / dark / system, `input[name="themeMode"]`); the rail bottom holds only the account row.
- **Org identity.** `#appShell[data-org-identity]` (absent/`subtle` = default, stamped at boot): **subtle** (Buzz) = wordmark alone up top, the org rides with the account bottom-left — "AJ" over "Bonfire" (`.tool-rail__account-text`, the name ALONE, never a status), "AJ · Bonfire" in the tooltip on the icon-only rail; **lockup** (Slack) = the org name beneath the wordmark on the same left edge. Phones name the org in the header subtitle as before.

### Dark depth ladder v2 — RATIFIED by AJ, 2026-09-02 ("do what your rec is with dark mode")

AJ's mandate 2026-09-02: *"a beautiful product with light/dark mode (dark as default), consistency end-to-end … light/snappy/responsive/modern."* Dark is therefore the reference rendering for shell work from here. Measured today: the dark shell's depth is **hairline-only** — the rail paints `transparent` over `--bg-app: #000`, the topbar is `--glass-chrome` `rgba(8,8,10,.82)` over the same black, and the canvas is `#000`; the three planes differ by 0–2 luminance units and are told apart only by `--line-1` at 9 % white (`scratchpad/nav/dark-ladder-before.png`). The ladder below gives the chrome one plane above the true-black canvas, cards one above that, and relaxes the hairlines. **Applied in `index.html`** 2026-09-02 (dark token block + `[data-theme="dark"] #appShell.is-authed .tool-rail { background: var(--surface-1) }`); pins re-pinned in `frontend_theme_test.go`, `frontend_polish_wave10_test.go`, `frontend_design_system_v2_test.go`.

| token (`[data-theme="dark"]`) | before | ratified | why |
| --- | --- | --- | --- |
| `--bg-app` (canvas, ground) | `#000000` | `#000000` | canvas stays true black — OLED parity, the ember reads hottest here |
| `--surface-1` (rail, topbar, panels) | `#050506` | `#0E0E10` | the chrome becomes a plane (+5.5 L*), no longer hairline-dependent |
| `--surface-2` (cards on panels) | `#0A0A0C` | `#151518` | one legible step above the chrome |
| `--surface-3` (inputs, wells, hover) | `#141416` | `#1C1C20` | one step above cards; hover wells still `--accent-soft` |
| `--glass-chrome` | `rgba(8,8,10,.82)` | `rgba(14,14,16,.86)` | the topbar's glass matches the rail plane so the L-seam stays invisible |
| `--line-1` / `--line-2` | `.09` / `.16` white | `.07` / `.13` white | hairlines relax once the planes carry depth |
| rail paint | `background: transparent` | `background: var(--surface-1)` (`#appShell.is-authed .tool-rail`, dark only) | the rail joins the chrome plane |

Contrast held (WCAG on the new planes): `--text-1` 17.6:1 on `--surface-1`; `--text-3` (50 % alpha) 4.71:1 on `--surface-1`, 4.72:1 on `--surface-2`, 4.57:1 on `--surface-3` — all ≥ 4.5. Nothing in the light ramp changes.

Before / after (dark, subtle identity): `scratchpad/nav/dark-ladder-before.png` → `scratchpad/nav/dark-ladder-after.png` (menu open: `dark-ladder-after-menu.png`); in the tree: `scratchpad/nav/final-dark-ladder-shell.png`.

**Ladder v2 and the dark default are LANDED.** Under the same mandate, **dark is the product default**: the pre-paint theme script (`index.html`, `<head>`, "pre-paint theme") falls back to `'dark'` for an absent/unknown key (supersedes the light default of 2026-07-10; stored choices and the signed-in account preference keep winning). Pins: `TestIndexThemeDefaultsToDark` (`frontend_theme_test.go`) for the default, and the dark-ladder hex pins in `frontend_theme_test.go`, `frontend_polish_wave10_test.go` and `frontend_design_system_v2_test.go` (`--surface-1` `#0E0E10`, `--surface-2` `#151518`, `--surface-3` `#1C1C20`, `--glass-chrome` `rgba(14,14,16,.86)`).
- **Active tab = glow only.** `.pd1-primary-nav__item[aria-current="page"]` is the ember glyph on its 10 % ember well and nothing else — no leading-edge bar in any state (hover, focus-visible, active, reduced motion). The Wave 10 inset bar is retired ("that thicker curve line is a mark of AI design").
- **Chrome seam rule.** Rail and topbar are one L of `.glass-chrome` (same `--glass-chrome-fill` + `--glass-chrome-filter`, shared `--glass-highlight` top hairline). The rail's `border-right` is transparent; its edge is a `::after` hairline from the topbar's bottom edge down, so the two `--line` hairlines meet exactly once at the inner corner. Utilities (bell / theme / account) sit on the nav glyph axis with the nav's `--accent-soft` hover well. Dark lifts by plane + hairline + specular: the ladder v2 ramp above (`--surface-1` `#0E0E10`) carries fill depth, so the chrome no longer depends on the hairline alone.

## Appendix A — icon inventory (every `<svg` in `index.html`, post-wave)

Columns: line (post-edit), where it lives (`html` markup · `js` string · `css-bg` background · `code`), the nearest id / aria-label / class as the site's purpose, stroke width, fill, grid, rendered size (`css` = sized by the stylesheet), status.

| line | where | site | stroke | fill | grid | size | status |
|---:|---|---|---|---|---|---|---|
| 2208 | css-bg | `(anonymous)` | - | - | - | css | CSS background glyph (form control) — out of scope |
| 2661 | css-bg | `(anonymous)` | - | - | - | css | CSS background glyph (form control) — out of scope |
| 2667 | css-bg | `(anonymous)` | - | - | - | css | CSS background glyph (form control) — out of scope |
| 2853 | css-bg | `(anonymous)` | - | - | - | css | CSS background glyph (form control) — out of scope |
| 2854 | css-bg | `(anonymous)` | - | - | - | css | CSS background glyph (form control) — out of scope |
| 4656 | css-bg | `(anonymous)` | - | - | - | css | CSS background glyph (form control) — out of scope |
| 4668 | css-bg | `(anonymous)` | - | - | - | css | CSS background glyph (form control) — out of scope |
| 35624 | html | `brandMark` | - | - | 0 0 24 24 | css | pinned brand — unchanged |
| 35628 | html | `pd1PrimaryNav` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 35633 | html | `Rooms` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 35637 | html | `Conversations` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 35641 | html | `Work` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 35645 | html | `Drive` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 35652 | html | `notificationBell` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 35657 | html | `themeToggle` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 35658 | html | `themeToggle` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 35663 | html | `accountMenuButton` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 35684 | html | `accountMenuSettings` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 35688 | html | `accountMenuSignOut` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 35698 | html | `topbarBack` | 1.8 | none | 0 0 24 24 | 17 | conforms |
| 35706 | html | `topbarOrganizationName` | 1 | none | 0 0 12 12 | 1 | conforms |
| 35711 | html | `topbarOrganizationCreate` | 1.5 | none | 0 0 20 20 | 1.5 | conforms |
| 35739 | html | `topbarLiveLeave` | 1.8 | none | 0 0 24 24 | 14 | conforms |
| 35747 | html | `topbarBell` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 35790 | html | `officeStrideCradle` | - | - | 0 0 720 220 | css | pinned brand — unchanged |
| 35797 | html | `office-launch__aperture` | - | - | 0 -42.24 675.84 84.48 | css | pinned brand — unchanged |
| 35805 | html | `continue` | 1.8 | none | 0 0 24 24 | 20 | conforms |
| 35806 | html | `explore` | 1.8 | none | 0 0 24 24 | 20 | conforms |
| 35807 | html | `create` | 1.8 | none | 0 0 24 24 | 20 | conforms |
| 35808 | html | `challenge` | 1.8 | none | 0 0 24 24 | 20 | conforms |
| 35823 | html | `homeScoutSend` | 1.8 | none | 0 0 24 24 | 17 | conforms |
| 35830 | html | `homeProjectChooserTitle` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 35859 | html | `briefGreeting` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 35875 | html | `Close` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 35884 | html | `loginThemeToggle` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 35885 | html | `loginThemeToggle` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 35953 | html | `passkeySignIn` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 35990 | html | `Scout responses` | 1.8 | none | 0 0 24 24 | 13 | conforms |
| 36011 | html | `scoutActivityCount` | 1.8 | none | 0 0 24 24 | 14 | conforms |
| 36022 | html | `assistantForm` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36037 | html | `roomChatPanel` | 1.8 | none | 0 0 24 24 | 13 | conforms |
| 36044 | html | `roomChatClose` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36073 | html | `roomWorkActivityClose` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 36084 | html | `roomChatSend` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 36104 | html | `screenStageVideo` | 1.5 | none | 0 0 24 24 | 34 | over-media placeholder — sanctioned |
| 36153 | html | `greenRoomHint` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 36161 | html | `greenRoomMicBadge` | 1.8 | none | 0 0 24 24 | 13 | conforms |
| 36166 | html | `greenRoomMicChip` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 36169 | html | `greenRoomCamChip` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 36173 | html | `greenRoomFiltersBtn` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 36219 | html | `lobbyNewRoom` | 1.8 | none | 0 0 24 24 | 13 | conforms |
| 36256 | html | `localTileAvatar` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 36266 | html | `media-flag media-flag--camera` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 36273 | html | `media-flag media-flag--share` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 36281 | html | `Pin full screen` | 1.8 | none | 0 0 24 24 | 13 | conforms |
| 36301 | html | `chatTool` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36313 | html | `chatNewChannel` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36321 | html | `chatChannelName` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 36335 | html | `chatNewGroup` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36344 | html | `chatGroupCreateGo` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 36356 | html | `chatNewThread` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36364 | html | `chatDefaultThread` | 1.8 | none | 0 0 24 24 | 14 | conforms |
| 36382 | html | `chatConvoBack` | 1.8 | none | 0 0 24 24 | 17 | conforms |
| 36387 | html | `chatConvoRename` | 1.8 | none | 0 0 24 24 | 13 | conforms |
| 36392 | html | `chatConvoRiff` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 36400 | html | `chatConvoMembersOpen` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36420 | html | `chatConvoNotifyOpen` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36440 | html | `scoutChatThinking` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36477 | html | `scoutChatAttach` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 36482 | html | `scoutChatDeliverables` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 36488 | html | `scoutChatSend` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 36495 | html | `scoutChatProjectChooserTitle` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 36507 | html | `sentMessageProjectClose` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 36530 | html | `chatContextClose` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 36541 | html | `chatContextProjectChooserTitle` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 36550 | html | `chatContextReplyAttach` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 36554 | html | `chatContextReplySend` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 36665 | html | `artifactCountLabel` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36733 | html | `studioAppsTitle` | 1.8 | none | 0 0 24 24 | 20 | conforms |
| 36747 | html | `studio-app__icon` | 1.8 | none | 0 0 24 24 | 20 | conforms |
| 36764 | html | `studioProjectsWorkspace` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36792 | html | `memoryTool` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36819 | html | `filesNewButton` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36820 | html | `filesNewButton` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36821 | html | `Drive views` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36822 | html | `Drive views` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36836 | html | `filesNewFolderButton` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36847 | html | `filesUploadButton` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 36875 | html | `expandBoard` | 2 | none | 0 0 24 24 | 11 | conforms |
| 36904 | html | `boardSurface` | - | currentColor | 0 0 64 64 | css | Scout flame identity glyph — sanctioned fill |
| 36915 | html | `collapseBoard` | 1.8 | none | 0 0 24 24 | 14 | conforms |
| 36933 | html | `newCard` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36955 | html | `boardDockMute` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36962 | html | `boardDockCamera` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36968 | html | `boardDockShare` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 36980 | html | `boardDockMenuButton` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 37008 | html | `muteMic` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 37013 | html | `mute-icon--off` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 37023 | html | `toggleCamera` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 37027 | html | `toggleCamera` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 37035 | html | `audioSettingsButton` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 37043 | html | `screenShare` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 37053 | html | `consentToggle` | 1.8 | none | 0 0 24 24 | 17 | conforms |
| 37056 | html | `roomChatToggle` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 37069 | html | `inviteToggle` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 37082 | html | `archiveMeeting` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 37090 | html | `roomMoreToggle` | 1.8 | none | 0 0 24 24 | 20 | conforms |
| 37106 | html | `leave` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 37115 | html | `consentClose` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 37125 | html | `roomTransferPanel` | 1.8 | none | 0 0 24 24 | 25 | conforms |
| 37158 | html | `osAssistantClose` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 37175 | html | `osAssistantVoice` | 1.8 | none | 0 0 24 24 | 17 | conforms |
| 37178 | html | `osAssistantSend` | 1.8 | none | 0 0 24 24 | 17 | conforms |
| 37195 | html | `voiceIslandLedgerBtn` | 1.8 | none | 0 0 24 24 | 18 | conforms |
| 37198 | html | `voiceIslandClose` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 37214 | html | `grillStageClose` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 37260 | html | `notificationMarkAll` | 1.8 | none | 0 0 24 24 | 17 | conforms |
| 37263 | html | `notificationClearAll` | 1.8 | none | 0 0 24 24 | 17 | conforms |
| 37266 | html | `notificationClose` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 37273 | html | `notificationEmpty` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 37290 | html | `pipExpand` | 1.8 | none | 0 0 24 24 | 13 | conforms |
| 37298 | html | `pipMute` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 37303 | html | `(anonymous)` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 37313 | html | `pipCamera` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 37317 | html | `pipCamera` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 37324 | html | `pipEnd` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 37369 | html | `settingsClose` | 1.8 | none | 0 0 24 24 | 14 | conforms |
| 37377 | html | `Settings sections` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 37378 | html | `Settings sections` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 37379 | html | `Settings sections` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 37380 | html | `Settings sections` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 37382 | html | `settings-nav__icon stride-mark` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 37383 | html | `settings-nav__icon stride-mark` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 37384 | html | `settings-nav__icon stride-mark` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 37386 | html | `settings-nav__icon stride-mark` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 37387 | html | `settings-nav__icon stride-mark` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 37736 | html | `memoryImportClose` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 44137 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 44138 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 44533 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 45403 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 45404 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 45579 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 45606 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 46367 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 46542 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 14 | conforms |
| 46594 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 13 | conforms |
| 46720 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 47466 | js | `(anonymous)` | 1.2 | none | 0 0 16 16 | 1.2 | conforms |
| 47474 | js | `(anonymous)` | 1.2 | none | 0 0 16 16 | 1.2 | conforms |
| 47617 | html | `deck-editor__btn` | 1.2 | none | 0 0 16 16 | 1.2 | conforms |
| 47624 | html | `deck-editor__slide-nav` | 1.2 | none | 0 0 16 16 | 1.2 | conforms |
| 47628 | html | `(anonymous)` | 1.2 | none | 0 0 16 16 | 1.2 | conforms |
| 47633 | html | `deck-editor__btn deck-editor__btn--icon` | 1.2 | none | 0 0 16 16 | 1.2 | conforms |
| 47636 | html | `deck-editor__btn deck-editor__btn--icon` | 1.2 | none | 0 0 16 16 | 1.2 | conforms |
| 47640 | html | `deck-editor__btn deck-editor__btn--icon` | 1.2 | none | 0 0 16 16 | 1.2 | conforms |
| 47643 | html | `deck-editor__btn deck-editor__btn--icon` | 1.2 | none | 0 0 16 16 | 1.2 | conforms |
| 47648 | html | `deck-editor__btn` | 1.2 | none | 0 0 16 16 | 1.2 | conforms |
| 48979 | html | `Close Document Studio` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 49027 | html | `document-studio-inspector` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 50196 | html | `Close Deck Studio` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 50212 | html | `Previous slide` | 1.2 | none | 0 0 16 16 | 1.2 | conforms |
| 50214 | html | `Next slide` | 1.2 | none | 0 0 16 16 | 1.2 | conforms |
| 50274 | html | `deck-slide-inspector` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 51919 | html | `Previous slide` | 1.2 | none | 0 0 16 16 | 1.2 | conforms |
| 51920 | html | `Next slide` | 1.2 | none | 0 0 16 16 | 1.2 | conforms |
| 52222 | js | `(anonymous)` | 1.2 | none | 0 0 16 16 | 1.2 | conforms |
| 52230 | js | `(anonymous)` | 1.2 | none | 0 0 16 16 | 1.2 | conforms |
| 52419 | js | `(anonymous)` | 1.2 | none | 0 0 16 16 | 1.2 | conforms |
| 52488 | js | `(anonymous)` | 1.2 | none | 0 0 16 16 | 1.2 | conforms |
| 53524 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 14 | conforms |
| 53965 | js | `(anonymous)` | - | - | 0 0 64 64 | 22 | Scout flame identity glyph — sanctioned fill |
| 54249 | html | `(anonymous)` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 57576 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 14 | conforms |
| 67693 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 14 | conforms |
| 68311 | html | `goalcard__ring` | - | - | 0 0 44 44 | 44 | progress ring, not an icon |
| 68323 | html | `More` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 68535 | js | `(anonymous)` | 2 | none | 0 0 24 24 | 12 | conforms |
| 68537 | js | `(anonymous)` | 2 | none | 0 0 24 24 | 11 | conforms |
| 69697 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 69698 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 72330 | js | `(anonymous)` | 2 | none | 0 0 24 24 | 12 | conforms |
| 72715 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 14 | conforms |
| 72716 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 14 | conforms |
| 73144 | code | `(anonymous)` | - | - | - | css | code, not an icon |
| 73507 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 75203 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 80192 | code | `(anonymous)` | - | - | - | css | code, not an icon |
| 80323 | html | `(anonymous)` | - | - | - | css | left as-is |
| 80324 | code | `(anonymous)` | - | - | - | css | code, not an icon |
| 80598 | js | `(anonymous)` | - | currentColor | 0 0 24 24 | 13 | Scout flame identity glyph — sanctioned fill |
| 80765 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 17 | conforms |
| 80890 | html | `scout-chat-research__icon` | - | - | 0 0 64 64 | 15 | Scout flame identity glyph — sanctioned fill |
| 83201 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 13 | conforms |
| 83280 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 1.8 | conforms |
| 85348 | js | `(anonymous)` | 2 | none | 0 0 24 24 | 12 | conforms |
| 85548 | js | `(anonymous)` | 2 | none | 0 0 24 24 | 12 | conforms |
| 85581 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 14 | conforms |
| 86654 | js | `(anonymous)` | 2 | none | 0 0 24 24 | 11 | conforms |
| 87680 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 87682 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 87686 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 87689 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 87691 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 88201 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 13 | conforms |
| 88344 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 14 | conforms |
| 89144 | js | `(anonymous)` | - | currentColor | 0 0 64 64 | 11 | Scout flame identity glyph — sanctioned fill |
| 89216 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 89989 | js | `(anonymous)` | - | currentColor | 0 0 64 64 | css | Scout flame identity glyph — sanctioned fill |
| 90247 | js | `(anonymous)` | 1.8 | none | 0 0 24 24 | 16 | conforms |
| 91060 | js | `(anonymous)` | - | currentColor | 0 0 64 64 | css | Scout flame identity glyph — sanctioned fill |
| 91828 | js | `passcode required` | 2 | none | 0 0 24 24 | 12 | conforms |
| 91832 | js | `guests allowed` | 2 | none | 0 0 24 24 | 12 | conforms |
| 92299 | js | `(anonymous)` | 2 | none | 0 0 24 24 | 12 | conforms |
| 93027 | js | `home-live__chevron` | 1.8 | none | 0 0 24 24 | 15 | conforms |
| 93371 | js | `(anonymous)` | - | - | 0 0 64 64 | 15 | Scout flame identity glyph — sanctioned fill |
