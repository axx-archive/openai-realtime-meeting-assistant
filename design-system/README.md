# The Bonfire — Design System

> A next-gen agentic video conference app. Sleek, intuitive, friendly, and built to feel incredible to use.

The Bonfire is a real-time meeting room where the assistant is part of the team. It listens during the meeting, maintains a shared Kanban board by voice, captures durable memory from the room, and ships notes when you're done. The whole product is built around one signature image — a warm ember at the center of the screen, halos pulsing on a 2400ms clock, the room "listening" to itself.

This design system encodes everything needed to build new Bonfire surfaces (marketing, decks, prototypes, extensions, settings, onboarding) in a way that feels native to the existing product.

---

## Sources

| Source | Where | Status |
| --- | --- | --- |
| Production codebase | `meetingassist/` (mounted via File System Access; upstream is `openai-realtime-meeting-assistant`) | Read |
| Product UI of record | `meetingassist/index.html` (3192 lines, full dark room) | Read |
| Server / agent logic | `meetingassist/kanban.go`, `meeting_notes.go`, `memory.go` | Skimmed for product copy |
| Original screenshot | `assets/product-screenshot.png` (also `meetingassist/public/screenshot.png`) | Copied |
| Memory design plans | `meetingassist/docs/plans/agentic-memory-{context,design}.md` | Available |

The codebase is a Go + Pion WebRTC server with a single-file HTML client. The visual system is large, opinionated, and consistent — the most important reference is `meetingassist/index.html`.

---

## Index

Root of this design system:

- `README.md` — this file. Company context, content + visual foundations, iconography.
- `SKILL.md` — agent skill manifest. Read this first if you're a coding agent.
- `colors_and_type.css` — tokens (colors, type families, semantic styles, spacing, shadows, motion).
- `assets/` — logo SVGs, product screenshot, sample avatars.
- `fonts/` — empty by design; the three families load from Google Fonts via `colors_and_type.css`.
- `preview/` — small specimen cards that populate the Design System tab.
- `ui_kits/bonfire-room/` — React recreation of the meeting room (the only product surface).

---

## Brand at a glance

- **Name:** The Bonfire. Always with the definite article. Capital T, capital B in product chrome (the `<h1>` reads "The Bonfire"). Everywhere else copy lowercases it on purpose.
- **Tagline (subtitle under logo):** `agentic meeting room` — lowercase, mono, wide tracking.
- **Voice metaphor:** the room itself listens, remembers, and answers. The bonfire is what people gather around.
- **Primary color:** Ember `#FF7A2B`.
- **Surface temperature:** warm darks. Never grey. Black-ish backgrounds are tinted brown (`#110D09`, `#15110D`).
- **Signature motion:** three concentric halos pulsing around the logo on a 2400ms clock — `--pulse-cycle`. Half a beat apart. Everything that "breathes" inherits this clock.

---

## CONTENT FUNDAMENTALS

How copy is written in The Bonfire.

### Casing
**Lowercase by default.** Almost every system label, button hint, status, toast, log line, and panel header is lowercase — even at the start of a sentence. The few exceptions are buttons that take direct action ("Join the room", "Send notes", "Share screen", "Create card") and proper nouns (names like "Tim", "Erick"; column names "Backlog", "In Progress", "Blocked", "Done"; tech terms "WebRTC", "RTP").

### Person
**Second person, casual.** "you", "your", "join the room". The product talks to one person at a time. The agent is never "I" — it's referred to as "the room" or "assistant" or "meeting memory".

### Vibe
Calm, mechanical-poetic, never cute. Status reads like a clean log file but lands like a sentence. Empty states are tiny italic koans.

### Examples (lifted from production)

| Where | Copy |
| --- | --- |
| Logo subtitle | `agentic meeting room` |
| Status pill, idle | `not connected` |
| Status pill, connecting | `connecting…` |
| Status pill, ready | `the room is listening` |
| Status pill, gone | `assistant offline` |
| Access hint, locked | `Verify access first. Camera and microphone start after the room lets you in.` |
| Access hint, ready | `{Name} is ready to verify.` |
| Access hint, in | `{Name} is in the room.` |
| Empty column | `nothing here yet` |
| Empty board hero | `join the room to load the board.` |
| Memory empty | `memory starts when the room speaks.` |
| Memory entries | `transcript · 10:47`, `answer · 10:48`, `archive · 10:52` |
| Assistant feed empty | `waiting for room audio.` |
| Footer log | `team meeting`, `in room · Tim, Erick`, `{Name} is verified` |
| Toasts | `new card · Add Simulcast Forwarding Controls → Backlog`, `meeting archive ready` |
| Buttons | `Join the room`, `Share screen`, `Send notes`, `New card`, `Undo delete`, `Save changes`, `Delete`, `Cancel`, `Ask` |
| Card detail subhead | `manual project capture`, `owner · Tim` |

### Punctuation
- Ellipsis is `…` (single char), not `...`. Used for "connecting…", "Generating notes".
- Middle dot ` · ` separates a label and its value: `owner · Tim`, `transcript · 10:47`, `2 projects`.
- Em arrows show a state change: `new card · {title} → Backlog`.
- Sentences end with periods in hints and longer descriptions; never in pills/labels.

### Emoji
**Never.** No emoji anywhere in the product. The warmth comes from color and motion, not from glyphs.

### Word choices
- **room** > meeting / channel / call (when speaking of the live session)
- **the room is listening** > "active", "live"
- **memory** > "history" / "transcript log"
- **send notes** > "export", "share recap"
- **archive** for the past-tense noun ("meeting archive ready")
- **sparks** > confetti (warmer metaphor; only fires on `Done`)
- **hot ember** for the brief recognition handshake
- **parchment** internally for the light kanban surface
- A ticket may be called ticket / card / task / issue / sticky note. They're all kanban cards.

---

## VISUAL FOUNDATIONS

### Color
Two surfaces play against each other:

- **Night** (warm dark): the room itself — topbar, video rail, panels, footer. Inputs sit on `rgba(11, 8, 5, 0.56)` over `--night-3`. Borders are translucent warm ivory (`rgba(255, 235, 200, 0.10)`).
- **Parchment** (warm light): the board surface (`--ash-50` = `#FAF6EE`) and modals. Inputs there sit on `#FFFCF6` with `--line-light` borders.

Ember (`#FF7A2B`) appears sparingly: brand mark, primary button, listening pill, glows, hot-ember handshake, "moved" card border, ember-300 for links. The amount of ember on screen at rest is small — it's earned by the agent doing something.

Hue discipline matters more than any single hex. Avoid cold greys, blue-purple gradients, neon. Even semantic colors (success green, danger red, info blue) are pulled toward warm — never saturated.

**The one deliberate exception:** the active-speaker green, `--speaker-accent` (`#34D399`). It is cool and saturated on purpose — "who is talking" must read instantly against a warm screen, and no warm hue can do that job. It appears only on the speaking video tile (border + `--glow-speaker-md`) and is locked in place by guard tests in the product. Do not borrow it for anything else, and do not add a second cool accent.

### Type
- **Geist** (sans) — everything UI: 11–20px, weights 400/500/600. 14/1.5 is the default.
- **Geist Mono** — system labels in `--fg-2`, uppercase, 0.08–0.10em tracking, sizes 10–11px. The "log file voice". Also used for tabular numbers (`font-variant-numeric: tabular-nums`).
- **Instrument Serif italic** — only for empty states (`nothing here yet`) and ceremonial copy. Roughly 22–32px. Italic is the default; the upright is barely used.

`font-feature-settings: "ss01" on, "cv11" on` is set on the body — the Geist stylistic alternates are part of the look.

### Spacing
Single power-of-two-ish scale: `4 / 8 / 12 / 16 / 20 / 24 / 32 / 48` (`--s-1` through `--s-8`). Grids use `gap`, never margins. Most panels are `12px` (`--s-3`) of internal padding with `12px` between siblings — dense but breathing.

### Backgrounds
- No full-bleed photography in the product itself. The "room" is a flat warm-dark surface.
- The board uses an off-white parchment color, not pure white, so the eye can rest after the dark chrome.
- The brand mark sits on a `#110D09` 14px-radius rounded square — the only piece of pure decorative geometry. A radial gradient inside it does all the work.
- One linear gradient is used: the topbar (`linear-gradient(180deg, var(--night-2), var(--night-1))`) — a 4-step subtle vertical wash to lift it off the body. No diagonal gradients, no rainbow gradients, no purple-blue gradients.
- Translucent fills (`rgba(255, 235, 200, 0.04)`) are how panels-inside-panels are layered.

### Animation
- One clock: `--pulse-cycle: 2400ms`. The halos, listening dot, and any "breathing" UI all use it.
- Two easings: `--ease-out` (`cubic-bezier(0.22, 0.61, 0.36, 1)`) for entrances; `--ease-soft` (`cubic-bezier(0.4, 0.0, 0.2, 1)`) for everything else.
- Two base durations: `--t-fast: 120ms`, `--t-base: 200ms`.
- The **mount stagger** cascades each section in by 60ms (`280ms` rise from `opacity 0.5 / +6px` to home). Topbar 60 → footer 120 → board 180 → access 320 → video 380 → memory 440 → assistant 500.
- The **card-move wiggle** is an 1100ms keyframe sequence with rotation, scale, and a damped translate — it reads as the agent *placing* the card, not as a fade.
- Sparks (not confetti) puff outward from a `Done` card: 22 warm-colored pieces with real physics (vy −180..−260, gravity 480 px/s², 1s lifetime, 0–600ms random delay). Rate-limited to once per 6s.
- The **hot-ember handshake**: when the agent recognizes speech, the pulse compresses from 2400ms → 1800ms and the mark gains an ember drop-shadow for 1.6s, then settles. Suppressed for 8s afterward so continuous speech doesn't strobe.
- `prefers-reduced-motion`: halves halo opacity, doubles its period, kills wiggle/sparks, but the heartbeat keeps going. The fire stays lit.

### Hover / press / focus
- **Hover (dark UI):** background gets a touch lighter — `rgba(255, 235, 200, 0.06) → 0.10`. Border slightly more visible.
- **Hover (parchment card):** background `#FFF → #FFFAF0` (warmer white), shadow lifts (`--shadow-light-1 → --shadow-light-2`), translate Y −1px. Critically, **shadow transitions in 60ms first, then translate kicks in at 60ms** — the warm-up means it never feels like a pop.
- **Press:** scale `0.96` for buttons, `0.985` for cards. Fast (`--t-fast`).
- **Focus-visible:** ember glow ring (`--glow-ember-sm`) + 2px outline-offset.
- **Disabled:** `opacity: 0.45`, no transform, `cursor: not-allowed`.
- **Speaking video tile:** `--speaker-accent` green border + `--glow-speaker-md`. (Not opacity changes — it's a presence signal, and the one sanctioned cool accent in the system.)

### Borders & corners
- Radii: `--r-xs: 4` (tag chips) / `--r-sm: 6` (inputs, secondary buttons) / `--r-md: 10` (panels, primary cards) / `--r-lg: 14` (modals, brand mark) / `--r-pill: 999`.
- Borders on dark: translucent ivory at 6/10/16% — never solid. The hairline practically disappears when the panel is selected.
- Borders on parchment: solid `--line-light` (`#EAE1CF`).

### Shadows
Two systems, never crossed:

- **Dark shadows** (`--shadow-1/2/3`) — for elevation in the room. Pure brown-black (`rgba(15, 8, 2, 0.45/0.55)`).
- **Light shadows** (`--shadow-light-1/2`) — for parchment cards. Warm and brown-tinted (`rgba(60, 35, 10, 0.06/0.10)`).

There's also a dedicated **glow** family: `--glow-ember-sm` (focus ring) and `--glow-ember-md` ("the room is listening", a moved card) for ember, plus `--glow-speaker-md` for the active-speaker tile — the only glow built on the cool `--speaker-accent` green. Glow is a 1px ring + tinted box-shadow blooms, not a pure box-shadow.

### Layout rules
- The room is a 3-row grid: `topbar / workspace / meeting-bar`. The meeting bar is `position: sticky; bottom: var(--s-3)` so it follows you as you scroll.
- Workspace is a 2-column grid: presentation tile (board or screen share) + a vertical rail of stacked panels (access, videos, memory, assistant).
- Breakpoints: 1100px (denser columns), 860px (rail moves above board), 640px (everything stacks).
- Toasts: bottom-right, `min(360px, calc(100vw - 36px))`, `z-index: 1100`, animate in 180ms.
- Card detail modal: centered, `min(620px, calc(100vw - 48px))`, dark backdrop blurred with `--blur-overlay`.

### Use of transparency & blur
The chrome is **liquid glass** — warm layered glass, never cool grey. The tokens:

- **Glass surfaces:** `--glass-surface` (primary) and `--glass-surface-quiet` (subdued) — translucent warm gradients that fade into the night surface.
- **Glass edges:** `--glass-edge` for the hairline border, `--glass-highlight` for the specular inset (a top highlight plus a faint ember underglow).
- **Three blur tiers**, and every `backdrop-filter` routes through one of them: `--blur-hero` (the join gate — the one hero moment), `--blur-panel` (rails, bars, toasts, inputs), `--blur-overlay` (floating chrome, menus, modal backdrops).

The rule that keeps it fast and quiet: **glass is for chrome surfaces** — panels, bars, toasts, menus, modals — applied once per surface. Never apply `backdrop-filter` per-item inside a scroller (cards, chat bubbles, feed rows): stacked backdrop blurs in an overflow container jank WebKit and mid-tier mobile, and the backdrop behind a small item is the already-glass panel anyway. Items inside a glass panel use flat translucent fills.

- Translucent fills are how layers within a dark panel separate. Anywhere you see `rgba(255, 235, 200, ...)` it's a layered surface, not a glass effect.

### Cards
Two card flavors, mirrored to the surface they live on:

- **Kanban card (parchment):** `#FFF`, 10px radius, `--line-light` border, `--shadow-light-1` resting / `--shadow-light-2` hover, identicon avatar in the top-right corner. Tags below the meta line.
- **Memory item / assistant message (night):** `rgba(255, 235, 200, 0.04)` fill, `--line-faint` border, `inset 0 1px 0 rgba(255,255,255,0.025)` + soft shadow.

### Identicon avatars
Owners get a procedural 5×5 mirrored identicon (`hashString(seed)`), warm-clamped: hue 18°–60°, saturation 60–80%, lightness 26–40%. They live in a `28px` (or `44px` "large") circle with a 2px white border and a 1px outer dark stroke. Never use stock illustrations of people; use the identicon.

---

## ICONOGRAPHY

The Bonfire is deliberately spare with icons. The room is mostly type, color, and motion — the few icons that exist are inline SVGs hand-set to match the system.

### What exists in the product

| Icon | Where | Source |
| --- | --- | --- |
| The brand mark (radial ember gradient on a `#110D09` rounded square) | Topbar | Inline SVG. Saved as `assets/logo.svg` and `assets/logo-mark-only.svg`. |
| The "leave room" phone-hang | Meeting bar, red round button | Inline SVG (Feather-style phone with strikethrough), `stroke-width: 2`, `stroke-linecap: round`, `stroke-linejoin: round`. Saved as `assets/icon-leave.svg`. |
| Identicons | Card avatars | Procedural SVG; see `assets/sample-identicons.svg` for what the algorithm produces. |
| Sparks | `Done` celebration | 6×10 rounded rects animated with CSS; not really icons. |

### Style if you need to add more

If you have to introduce new icons, match this prescription exactly:

- **Inline SVG**, not a font, not PNGs.
- **Stroke style:** `stroke-width: 2`, `stroke-linecap: round`, `stroke-linejoin: round`, `fill: none`. Feather / Lucide gives you the closest match for free — both ship at 1.5–2px stroke with round caps. Use **Lucide** for any net-new icons (CDN: `https://unpkg.com/lucide-static@latest/icons/<name>.svg`).
- **Default size:** 18×18 inside a 40×40 button. Stroke uses `currentColor`.
- **Color rules:** `--fg-1` (`#D9D0BF`) on dark by default; `--ash-600` (`#6E6457`) on parchment. Status-tinted icons use the matching semantic color.

> ⚠ Substitution flagged: the in-product hang-up icon was hand-drawn in the codebase, but the brand has no formal icon set. Lucide is the recommended fallback. If you build a real icon family for The Bonfire, replace this with your own and update this section.

### No emoji
The product never uses emoji. Don't introduce them in marketing or decks either.

### No unicode glyph icons
The product uses `×` (×, U+00D7) as the close button glyph in the card-detail modal and `·` (middle dot, U+00B7) and `→` (right arrow, U+2192) as separators in copy. These are intentional and the only unicode "icons" sanctioned.

### Logos
- `assets/logo.svg` — brand mark with the dark rounded square background (use on light surfaces or anywhere you need a self-contained mark).
- `assets/logo-mark-only.svg` — just the ember + halo, no plate (use on top of `--night-1`/`--night-2` surfaces).

---

## Surfaces / products

The Bonfire is one product, one surface: the room. There is no marketing site, settings screen, or mobile app in the codebase — only the meeting room. The UI kit (`ui_kits/bonfire-room/`) reflects that.

If you need to extend the system to new surfaces (a marketing landing, a settings page, a mobile companion), the rules above hold and the answer is almost always: more dark warm surface, very little ember, italic Instrument Serif for the one poetic moment per page, and Geist for everything else.

---

## Hearthlight (2026-06-10 revamp)

The room was recast as **one cinematic scene** — "the room is a stage" — replacing the boxed three-panel dashboard. The rules above still hold; these supersede where they conflict:

- **The stage takes the frame.** `.hearth-presentation` has no border, background, or shadow — the video tile itself (radius `--r-stage: 22px`) is the protagonist. Stage chrome floats as a single glass strip *over* the video.
- **Rails are borderless.** Scout and the board sit directly on the night. Each rail earns exactly one edge: a hairline rule under a 44px header (18px/600 sans). No panel glass on rails — backdrop-filter is reserved for genuinely floating chrome (dock, toasts, modals).
- **Type scale** `--text-2xs…--text-hero` with an 11px hard floor (10px only for in-tile media flags). Three tracking values only (`--track-sans/mono/label`). Instrument Serif never below 19px — smaller ceremony falls back to 12px italic Geist.
- **Warm presence** `--presence: #C98B4F` carries all liveness that isn't the active speaker ("in the room" pill, Scout ready dot, shipped-today stat). `--speaker-accent` green remains exclusively the speaking tile.
- **Due-date ranks:** neutral future → amber soon/today → red only on overdue.
- **The room breathes.** The stage bloom and gate halos pulse on `--pulse-cycle`; amplitude multiplies by `--breathe` (0.4 at rest → 1 when listening). The hot-ember handshake uses a dedicated 1800ms overlay halo, never a mid-flight duration change.
- **Motion grammar:** `rise` (chrome entrances, 480ms staggered), `place` (`--t-place`/`--ease-place` — the agent setting a card down), `breathe` (the heartbeat). Nothing changes state in a single frame; toasts burn out via `.is-leaving`.
- **The board sits in a parchment well** (`--board-well` + inset shadow) so white cards never cliff against the night. Stats are unboxed 28px tabular numerals over 11px mono captions. Column headers are museum labels with plain-text counts.
- **Mobile is native-grade:** viewport-fit=cover + `theme-color #110D09` (the night reaches the screen edges), a sticky ~38svh stage, ONE row of ≥48px dock verbs, 16px inputs (no iOS focus-zoom), and the dock yields to the keyboard while composing to Scout. `html/body` use `overflow-x: clip` — `hidden` would kill the sticky stage.
